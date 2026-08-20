// Package ircwire is a minimal, transport-only IRC client for the Buddy
// System's chat daemon — the second implementation of the buddylist.Conn
// seam (tocwire was first). It deliberately reuses tocwire's event types as
// the fleet's chat-event vocabulary, owns registration, a read loop with
// PING/PONG, per-channel membership tracking (so QUITs become per-room
// presence events), and injection-safe sends. NO reconnect policy — the
// daemon owns that. IRC is UTF-8 end to end; no charset conversion needed.
package ircwire

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/JsizzleR/buddy-system/internal/tocwire"
)

// ErrClosed is returned by send methods after Close.
var ErrClosed = errors.New("irc: client closed")

const (
	defaultHandshakeTimeout = 10 * time.Second
	defaultKeepAlive        = 60 * time.Second
	// maxChunk bounds one PRIVMSG payload; the IRC line limit is 512 bytes
	// including command, target, and CRLF.
	maxChunk = 400
)

type options struct {
	handshakeTimeout time.Duration
	keepAlive        time.Duration
}

// Option configures Dial.
type Option func(*options)

// WithHandshakeTimeout bounds dial+registration. Default 10s.
func WithHandshakeTimeout(d time.Duration) Option {
	return func(o *options) { o.handshakeTimeout = d }
}

// WithKeepAlive sets the client PING interval. Zero disables. Default 60s.
func WithKeepAlive(d time.Duration) Option {
	return func(o *options) { o.keepAlive = d }
}

// Client is a live IRC connection. Send methods are safe for concurrent use.
type Client struct {
	conn net.Conn
	nick string

	wmu sync.Mutex // serializes writes

	events   chan tocwire.Event
	closed   chan struct{}
	readDone chan struct{}

	closeOnce sync.Once
	closeErr  error

	errMu sync.Mutex
	err   error

	// members tracks channel membership so QUIT/NICK (which carry no channel)
	// can be surfaced as per-room presence events.
	mmu     sync.Mutex
	members map[string]map[string]bool // channel → nicks
}

// Dial connects and registers (NICK/USER, wait for 001). password, when
// non-empty, is sent as PASS before registration. A nick collision (433) is
// an error — the daemon's reconnect backoff retries, by which time the dead
// predecessor's socket has freed the nick.
func Dial(ctx context.Context, addr, nick, password string, opts ...Option) (*Client, error) {
	o := options{handshakeTimeout: defaultHandshakeTimeout, keepAlive: defaultKeepAlive}
	for _, opt := range opts {
		opt(&o)
	}
	if err := checkBare("nick", nick); err != nil {
		return nil, err
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("irc: dial %s: %w", addr, err)
	}

	deadline := time.Now().Add(o.handshakeTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		conn.Close()
		return nil, fmt.Errorf("irc: set handshake deadline: %w", err)
	}
	watcherDone := make(chan struct{})
	watcherExited := make(chan struct{})
	go func() {
		defer close(watcherExited)
		select {
		case <-ctx.Done():
			conn.Close()
		case <-watcherDone:
		}
	}()
	stop := func() { close(watcherDone); <-watcherExited }

	br := bufio.NewReader(conn)
	send := func(line string) error {
		_, err := conn.Write([]byte(line + "\r\n"))
		return err
	}
	if password != "" {
		if err := send("PASS " + sanitizeParam(password)); err != nil {
			stop()
			conn.Close()
			return nil, fmt.Errorf("irc: send PASS: %w", err)
		}
	}
	// Request echo-message so our own PRIVMSGs reflect back — the journal's
	// contract is "what the server saw", and without the echo our relays
	// would be invisible to room readers (measured against ergo). A server
	// that never ACKs simply registers without it; one that ACKs gets CAP END.
	if err := send("CAP REQ :echo-message"); err == nil {
		if err = send("NICK " + nick); err == nil {
			err = send("USER " + nick + " 0 * :" + nick)
		}
	}
	if err != nil {
		stop()
		conn.Close()
		return nil, fmt.Errorf("irc: register: %w", err)
	}

	for {
		raw, err := br.ReadString('\n')
		if err != nil {
			stop()
			conn.Close()
			if ctx.Err() != nil {
				return nil, fmt.Errorf("irc: registration: %w", ctx.Err())
			}
			return nil, fmt.Errorf("irc: registration read: %w", err)
		}
		msg := parseLine(strings.TrimRight(raw, "\r\n"))
		switch msg.cmd {
		case "PING":
			if err := send("PONG :" + msg.firstParamOrTrailing()); err != nil {
				stop()
				conn.Close()
				return nil, fmt.Errorf("irc: registration PONG: %w", err)
			}
		case "CAP":
			// Only ACK/NAK conclude our request; an LS or other subcommand
			// must not end negotiation early (Codex finding). Subcommand is
			// the param after the client identifier: "CAP * ACK :echo-message".
			sub := ""
			if len(msg.params) >= 2 {
				sub = strings.ToUpper(msg.params[1])
			}
			if sub == "ACK" || sub == "NAK" {
				if err := send("CAP END"); err != nil {
					stop()
					conn.Close()
					return nil, fmt.Errorf("irc: CAP END: %w", err)
				}
			}
		case "001":
			stop()
			conn.SetDeadline(time.Time{})
			c := &Client{
				conn:     conn,
				nick:     nick,
				events:   make(chan tocwire.Event),
				closed:   make(chan struct{}),
				readDone: make(chan struct{}),
				members:  map[string]map[string]bool{},
			}
			go c.readLoop(br)
			if o.keepAlive > 0 {
				go c.keepAliveLoop(o.keepAlive)
			}
			return c, nil
		case "432", "433", "436", "464", "465":
			stop()
			conn.Close()
			return nil, fmt.Errorf("irc: registration refused (%s %s) for nick %q — a dead predecessor may not have timed out yet, or the nick/password is invalid", msg.cmd, msg.trailing, nick)
		case "ERROR":
			stop()
			conn.Close()
			return nil, fmt.Errorf("irc: server refused registration: %s", msg.trailing)
		}
	}
}

// Events returns the server event stream; closes when the connection dies.
func (c *Client) Events() <-chan tocwire.Event { return c.events }

// Err reports the terminal error once Events has closed.
func (c *Client) Err() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.err
}

// Close is idempotent and waits for the read loop to exit.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		c.closeErr = c.conn.Close()
	})
	<-c.readDone
	return c.closeErr
}

// ChatJoin joins #room (the # is implied for bare names).
func (c *Client) ChatJoin(room string) error {
	if err := checkBare("room", room); err != nil {
		return err
	}
	return c.sendLine("JOIN " + channelOf(room))
}

// ChatSend sends text to a joined channel (the RoomID from the ChatJoin
// event). Newlines split into separate messages; long lines are chunked.
func (c *Client) ChatSend(roomID, text string) error {
	if err := checkBare("room id", roomID); err != nil {
		return err
	}
	return c.privmsg(roomID, text)
}

// IM sends a direct message.
func (c *Client) IM(to, text string) error {
	if err := checkBare("nick", to); err != nil {
		return err
	}
	return c.privmsg(to, text)
}

// SetAway sets (or with "" clears) the away message.
func (c *Client) SetAway(text string) error {
	if text == "" {
		return c.sendLine("AWAY")
	}
	// Away text is single-line by nature; newlines become spaces (sendLine
	// would otherwise reject the injection attempt outright).
	oneLine := strings.Map(func(r rune) rune {
		switch r {
		case '\r', '\n':
			return ' '
		case 0:
			return -1
		}
		return r
	}, text)
	return c.sendLine("AWAY :" + oneLine)
}

// AddBuddies is a no-op on IRC (channel membership IS presence here;
// MONITOR could back this later if buddy-level presence is wanted).
func (c *Client) AddBuddies(names ...string) error { return nil }

func (c *Client) privmsg(target, text string) error {
	// Budget the payload against the 512-byte line limit for THIS target,
	// and hold the write lock across the whole logical message so concurrent
	// multi-chunk sends never interleave (Codex findings).
	limit := maxChunk
	if overhead := len("PRIVMSG ") + len(target) + len(" :") + 2; overhead+limit > 510 {
		limit = 510 - overhead
		if limit < 1 {
			return fmt.Errorf("irc: target %q too long", target)
		}
	}
	lines := splitMessageN(text, limit)
	select {
	case <-c.closed:
		return ErrClosed
	default:
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	for _, line := range lines {
		if err := c.writeLocked("PRIVMSG " + target + " :" + line); err != nil {
			return err
		}
	}
	return nil
}

// sendLine writes one full IRC line. The line must already be sanitized:
// this is the ONLY writer, and it appends the sole CRLF.
func (c *Client) sendLine(line string) error {
	// Callers sanitize their inputs; this rejects any residual CR/LF/NUL as
	// defense in depth — one line in, one line on the wire, no exceptions
	// (the Codex pass found SetAway letting a newline through; this arm
	// makes that whole class unshippable).
	if strings.ContainsAny(line, "\r\n\x00") {
		return fmt.Errorf("irc: refusing line with CR/LF/NUL: %q", line)
	}
	select {
	case <-c.closed:
		return ErrClosed
	default:
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.writeLocked(line)
}

func (c *Client) writeLocked(line string) error {
	if _, err := c.conn.Write([]byte(line + "\r\n")); err != nil {
		return fmt.Errorf("irc: send: %w", err)
	}
	return nil
}

func (c *Client) keepAliveLoop(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-c.closed:
			return
		case <-t.C:
			if err := c.sendLine("PING :keepalive"); err != nil {
				return
			}
		}
	}
}

func (c *Client) finish(err error) {
	c.errMu.Lock()
	select {
	case <-c.closed:
		err = fmt.Errorf("irc: closed: %w", ErrClosed)
	default:
	}
	if c.err == nil {
		c.err = err
	}
	c.errMu.Unlock()
}

func (c *Client) deliver(ev tocwire.Event) bool {
	select {
	case c.events <- ev:
		return true
	case <-c.closed:
		return false
	}
}

func (c *Client) readLoop(br *bufio.Reader) {
	defer close(c.readDone)
	defer close(c.events)
	for {
		raw, err := br.ReadString('\n')
		if err != nil {
			c.finish(fmt.Errorf("irc: read: %w", err))
			return
		}
		if len(raw) > 32*1024 { // ergo enforces far less; a hostile peer gets cut off
			c.finish(fmt.Errorf("irc: read: line exceeds 32KB"))
			return
		}
		line := strings.TrimRight(raw, "\r\n")
		if line == "" {
			continue
		}
		msg := parseLine(line)
		for _, ev := range c.handle(msg, line) {
			if !c.deliver(ev) {
				return
			}
		}
	}
}

// handle turns one server message into zero or more events, updating the
// membership map as a side effect.
func (c *Client) handle(m ircMsg, raw string) []tocwire.Event {
	switch m.cmd {
	case "":
		return nil // whitespace-only line
	case "PING":
		// Answer directly; not an event. Both forms occur: "PING :token"
		// and "PING token" (Codex finding).
		c.sendLine("PONG :" + m.firstParamOrTrailing())
		return nil
	case "PONG", "001":
		return nil
	case "JOIN":
		ch := m.firstParamOrTrailing()
		nick := m.nick()
		c.addMember(ch, nick)
		if nick == c.nick {
			return []tocwire.Event{tocwire.ChatJoin{RoomID: ch, Room: strings.TrimPrefix(ch, "#")}}
		}
		return []tocwire.Event{tocwire.ChatUpdateBuddy{RoomID: ch, Present: true, Names: []string{nick}}}
	case "353": // RPL_NAMREPLY: <me> <sym> <chan> :nick1 nick2...
		if len(m.params) >= 3 {
			ch := m.params[2]
			var names []string
			for _, n := range strings.Fields(m.trailing) {
				n = strings.TrimLeft(n, "@+%~&")
				names = append(names, n)
				c.addMember(ch, n)
			}
			if len(names) > 0 {
				return []tocwire.Event{tocwire.ChatUpdateBuddy{RoomID: ch, Present: true, Names: names}}
			}
		}
		return nil
	case "PART":
		ch := m.firstParamOrTrailing()
		nick := m.nick()
		if nick == c.nick {
			c.dropChannel(ch) // our whole view of the room is stale now
			return []tocwire.Event{
				tocwire.ChatUpdateBuddy{RoomID: ch, Present: false, Names: []string{nick}},
				tocwire.ChatLeft{RoomID: ch}, // consumers must drop room state too
			}
		}
		c.dropMember(ch, nick)
		return []tocwire.Event{tocwire.ChatUpdateBuddy{RoomID: ch, Present: false, Names: []string{nick}}}
	case "KICK":
		if len(m.params) >= 2 {
			if m.params[1] == c.nick {
				c.dropChannel(m.params[0])
				return []tocwire.Event{
					tocwire.ChatUpdateBuddy{RoomID: m.params[0], Present: false, Names: []string{m.params[1]}},
					tocwire.ChatLeft{RoomID: m.params[0]},
				}
			}
			c.dropMember(m.params[0], m.params[1])
			return []tocwire.Event{tocwire.ChatUpdateBuddy{RoomID: m.params[0], Present: false, Names: []string{m.params[1]}}}
		}
		return nil
	case "QUIT":
		nick := m.nick()
		var evs []tocwire.Event
		for _, ch := range c.channelsOf(nick) {
			c.dropMember(ch, nick)
			evs = append(evs, tocwire.ChatUpdateBuddy{RoomID: ch, Present: false, Names: []string{nick}})
		}
		return evs
	case "NICK":
		oldNick := m.nick()
		newNick := m.firstParamOrTrailing()
		if oldNick == c.nick {
			c.nick = newNick
		}
		var evs []tocwire.Event
		for _, ch := range c.channelsOf(oldNick) {
			c.dropMember(ch, oldNick)
			c.addMember(ch, newNick)
			evs = append(evs,
				tocwire.ChatUpdateBuddy{RoomID: ch, Present: false, Names: []string{oldNick}},
				tocwire.ChatUpdateBuddy{RoomID: ch, Present: true, Names: []string{newNick}})
		}
		return evs
	case "PRIVMSG":
		if len(m.params) < 1 {
			return nil
		}
		target, text := m.params[0], m.trailing
		// CTCP ACTION ("/me waves") renders as "* waves".
		if strings.HasPrefix(text, "\x01ACTION ") && strings.HasSuffix(text, "\x01") {
			text = "* " + strings.TrimSuffix(strings.TrimPrefix(text, "\x01ACTION "), "\x01")
		}
		if strings.HasPrefix(target, "#") {
			return []tocwire.Event{tocwire.ChatIn{RoomID: target, From: m.nick(), Text: text}}
		}
		return []tocwire.Event{tocwire.IMIn{From: m.nick(), Text: text}}
	case "ERROR":
		return []tocwire.Event{tocwire.ServerError{Code: "ERROR: " + m.trailing}}
	default:
		if len(m.cmd) == 3 && m.cmd[0] >= '0' && m.cmd[0] <= '9' {
			if m.cmd[0] >= '4' { // error numerics
				return []tocwire.Event{tocwire.ServerError{Code: m.cmd + " " + m.trailing}}
			}
			return nil // informational numerics (MOTD, NAMES-end, ...) are not events
		}
		return []tocwire.Event{tocwire.Unknown{Raw: raw}}
	}
}

func (c *Client) addMember(ch, nick string) {
	c.mmu.Lock()
	defer c.mmu.Unlock()
	set := c.members[ch]
	if set == nil {
		set = map[string]bool{}
		c.members[ch] = set
	}
	set[nick] = true
}

func (c *Client) dropChannel(ch string) {
	c.mmu.Lock()
	defer c.mmu.Unlock()
	delete(c.members, ch)
}

func (c *Client) dropMember(ch, nick string) {
	c.mmu.Lock()
	defer c.mmu.Unlock()
	delete(c.members[ch], nick)
}

func (c *Client) channelsOf(nick string) []string {
	c.mmu.Lock()
	defer c.mmu.Unlock()
	var out []string
	for ch, set := range c.members {
		if set[nick] {
			out = append(out, ch)
		}
	}
	return out
}

// ---- message plumbing ----

type ircMsg struct {
	prefix   string
	cmd      string
	params   []string
	trailing string
}

func (m ircMsg) nick() string {
	n, _, _ := strings.Cut(m.prefix, "!")
	return n
}

func (m ircMsg) firstParamOrTrailing() string {
	if len(m.params) > 0 {
		return m.params[0]
	}
	return m.trailing
}

// parseLine splits ":prefix CMD p1 p2 :trailing with spaces".
func parseLine(line string) ircMsg {
	var m ircMsg
	if strings.HasPrefix(line, "@") { // IRCv3 message tags: not requested, but never a parse break
		_, rest, ok := strings.Cut(line, " ")
		if !ok {
			return m
		}
		line = rest
	}
	if strings.HasPrefix(line, ":") {
		var rest string
		m.prefix, rest, _ = strings.Cut(line[1:], " ")
		line = rest
	}
	head, trailing, hasTrailing := strings.Cut(line, " :")
	fields := strings.Fields(head)
	if len(fields) > 0 {
		m.cmd = strings.ToUpper(fields[0])
		m.params = fields[1:]
	}
	if hasTrailing {
		m.trailing = trailing
	}
	return m
}

func channelOf(room string) string {
	if strings.HasPrefix(room, "#") {
		return room
	}
	return "#" + room
}

// checkBare refuses names that could smuggle extra protocol tokens.
func checkBare(what, s string) error {
	if s == "" || strings.ContainsAny(s, " ,\r\n\x00") {
		return fmt.Errorf("irc: invalid %s %q", what, s)
	}
	return nil
}

// sanitizeParam strips line-injection bytes from a single parameter.
func sanitizeParam(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0 {
			return -1
		}
		return r
	}, s)
}

// sanitizeText strips CR/NUL (newlines are handled by splitMessage).
func sanitizeText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == 0 {
			return -1
		}
		return r
	}, s)
}

// splitMessageN turns arbitrary text into safe PRIVMSG payloads: one per
// line, each chunked under limit bytes on rune boundaries. This is the
// injection guard — a payload can never carry CR/LF into sendLine.
func splitMessageN(text string, limit int) []string {
	var out []string
	for _, line := range strings.Split(sanitizeText(text), "\n") {
		if line == "" {
			continue
		}
		for len(line) > limit {
			cut := limit
			for cut > 0 && line[cut]&0xC0 == 0x80 { // don't split a UTF-8 rune
				cut--
			}
			if cut == 0 {
				// limit is narrower than the leading rune; emit the whole
				// rune rather than loop forever making no progress.
				_, cut = utf8.DecodeRuneInString(line)
			}
			out = append(out, line[:cut])
			line = line[cut:]
		}
		out = append(out, line)
	}
	return out
}
