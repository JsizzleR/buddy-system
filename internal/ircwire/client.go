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
	// maxLine bounds one INBOUND line, and it is the reader's buffer size,
	// not a check applied afterwards. ergo enforces far less, so nothing
	// legitimate comes near it; the bound exists for a peer that streams
	// bytes with no LF at all. The earlier shape — ReadString('\n') and then
	// `len(raw) > 32*1024` — could never fire on that peer, because
	// ReadString accumulates until the LF arrives and the check ran only
	// once it had: memory grew without bound and the guard sat unreached.
	maxLine = 32 << 10
)

// errLineTooLong is the terminal error for a line that overran maxLine. It is
// the same verdict the old post-hoc check gave, now reachable.
var errLineTooLong = fmt.Errorf("irc: read: line exceeds %dKB", maxLine>>10)

// readLine returns one line without its CRLF, or errLineTooLong when the
// reader's buffer filled before an LF arrived. ReadSlice is what makes the
// bound real: it hands back at most the buffer and reports ErrBufferFull
// instead of growing. The slice aliases the buffer, so it is copied to a
// string before the next read can overwrite it.
func readLine(br *bufio.Reader) (string, error) {
	raw, err := br.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return "", errLineTooLong
	}
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(raw), "\r\n"), nil
}

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
	// keepAliveDone closes when the keepalive goroutine exits (at once when
	// keepalive is off). Observability only: it is how a test tells "stopped
	// because the read loop ended" from "lingered until the next tick found
	// the socket closed" — which, at the 60s default, would be a goroutine
	// outliving its connection by up to a minute.
	keepAliveDone chan struct{}

	closeOnce sync.Once
	closeErr  error

	errMu sync.Mutex
	err   error

	// members tracks channel membership so QUIT/NICK (which carry no channel)
	// can be surfaced as per-room presence events.
	mmu     sync.Mutex
	members map[string]map[string]bool // channel → nicks

	// asking serializes ISON: ONE outstanding query per connection, held
	// across the whole round-trip. RPL_ISON carries no request tag, so a
	// reply can only be matched to a query by knowing that exactly one is
	// outstanding — and a queue of them was measurably wrong twice. Waiters
	// enqueued under one lock and written under another could go out in the
	// opposite order, so one caller got another's answer and refused a DM to
	// a recipient who was right there (reproduced, 2 failures in 100 runs
	// under -race). And a query the server answers with something other than
	// 303 — measured: an over-long line answers `417 Input line too long`
	// and nothing else — left a waiter that no reply would ever pop, which
	// misaligned every later query for the life of the connection.
	//
	// Concurrency here bought nothing anyway: a DM asks about one name.
	asking sync.Mutex

	// waiter is the single outstanding query's mailbox (nil when none), and
	// dead latches the connection out of asking at all once a query has gone
	// unanswered. After that, presence reports "cannot tell" and every caller
	// falls back to its unchecked behaviour — the only safe direction, since
	// the alternative is refusing to send to someone who is reachable.
	qmu    sync.Mutex
	waiter chan presenceReply
	dead   bool
}

// presenceReply is one answer to one ISON. ok distinguishes "the server told
// us who is online" from "the server cannot answer that question", which the
// caller must NOT read as absence.
type presenceReply struct {
	online []string
	ok     bool
	err    error
}

// ErrNickInUse reports a registration refused because the name is already
// taken (433 nickname in use, 436 nick collision). Per-session presence walks
// a collision suffix on this and ONLY this: every other refusal is about the
// server, the network, or the credentials, and renaming a session over one
// would hide the real fault behind a session that quietly answers to the
// wrong name.
var ErrNickInUse = errors.New("irc: nick in use")

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

	br := bufio.NewReaderSize(conn, maxLine)
	send := func(line string) error {
		_, err := conn.Write([]byte(line + "\r\n"))
		return err
	}
	if password != "" {
		// Trailing, not a middle parameter: a middle parameter ends at the
		// first space, so `hunter two` reached the server as `hunter` and
		// the registration was refused for a password nobody had mistyped.
		if err := send("PASS :" + sanitizeParam(password)); err != nil {
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
		line, err := readLine(br)
		if err != nil {
			stop()
			conn.Close()
			if ctx.Err() != nil {
				return nil, fmt.Errorf("irc: registration: %w", ctx.Err())
			}
			return nil, fmt.Errorf("irc: registration read: %w", err)
		}
		msg := parseLine(line)
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
			// A deadline that fails to clear would end the connection at
			// the handshake timeout and look like a server that went away.
			if err := conn.SetDeadline(time.Time{}); err != nil {
				conn.Close()
				return nil, fmt.Errorf("irc: clear handshake deadline: %w", err)
			}
			c := &Client{
				conn:          conn,
				nick:          nick,
				events:        make(chan tocwire.Event),
				closed:        make(chan struct{}),
				readDone:      make(chan struct{}),
				keepAliveDone: make(chan struct{}),
				members:       map[string]map[string]bool{},
			}
			go c.readLoop(br)
			if o.keepAlive > 0 {
				go c.keepAliveLoop(o.keepAlive)
			} else {
				close(c.keepAliveDone)
			}
			return c, nil
		case "432", "433", "436", "464", "465":
			stop()
			conn.Close()
			refused := fmt.Errorf("irc: registration refused (%s %s) for nick %q — a dead predecessor may not have timed out yet, or the nick/password is invalid", msg.cmd, msg.trailing, nick)
			if msg.cmd == "433" || msg.cmd == "436" {
				return nil, fmt.Errorf("%s: %w", refused, ErrNickInUse)
			}
			return nil, refused
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
		// finish() also closes the conn when the read loop ends on its own,
		// and closing c.closed can be what ends it — losing that benign race
		// is not a Close failure (the same filter tocwire adopted).
		if err := c.conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			c.closeErr = err
		}
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

// presenceTimeout bounds one ISON round-trip.
//
// The reply is a lookup, but the connection is not always free to receive it:
// ergo applies fakelag to a non-oper client, and this one never sends OPER.
// Measured against the live server (burst 5, 2 messages/second): with nothing
// sent first, the 303 comes back in 0 ms; after 10 PRIVMSGs — which is ONE
// 4 KB message, since maxChunk is 400 — it takes 3.008 s, and after 30, 13 s.
// So a timeout here is an ordinary consequence of a busy connection, not a
// broken one, which is why one must not be terminal (see answerPresence).
//
// It stays at 3s rather than rising to cover fakelag because the callers'
// own deadlines are 5s: a probe that outlives them turns a courtesy check
// into the reason a DM fails. Under a burst, the message goes out unchecked
// and the next one is checked again — the same trade as any other failure of
// this mechanism.
//
// A var, not a const: the tests that drive a timeout have to out-wait it, and
// a three-second test is a test people delete.
var presenceTimeout = 3 * time.Second

// Presence reports which of names the server currently knows to be online,
// spelled the way the SERVER spells them. ok is false when the server cannot
// answer at all; a caller must then treat presence as unknown, never as
// absence — refusing to send on "we could not ask" would turn a probe outage
// into lost messages.
//
// ISON is the right question here and the only one asked: it is a lookup with
// a definite answer, unlike waiting out the ABSENCE of an error numeric after
// a send. Measured against ergo 2.19.1: `ISON alice nobody-here-12345
// SmarterChild` answers `303 :alice SmarterChild` — present names listed,
// absent ones simply omitted.
//
// One query at a time (see the asking lock), and a query that goes unanswered
// retires the mechanism instead of leaving a reply owed: every failure here
// degrades to "cannot tell", which is exactly what the caller does when the
// backend has no presence query at all.
func (c *Client) Presence(names ...string) ([]string, bool, error) {
	if len(names) == 0 {
		return nil, true, nil
	}
	for _, n := range names {
		if err := checkBare("nick", n); err != nil {
			return nil, false, err
		}
	}
	line := "ISON " + strings.Join(names, " ")
	// Refuse an over-long query BEFORE it reaches the wire. ergo answers one
	// with `417 Input line too long` and no 303 (measured), and a caller
	// supplies the name: `buddylist dm <500-byte name>` would otherwise have
	// cost this connection its presence check permanently.
	if len(line)+2 > 512 {
		return nil, false, fmt.Errorf("irc: ISON for %d name(s) exceeds the 512-byte line limit", len(names))
	}

	c.asking.Lock()
	defer c.asking.Unlock()

	ch := make(chan presenceReply, 1) // buffered: the read loop must never block handing off
	c.qmu.Lock()
	if c.dead {
		c.qmu.Unlock()
		return nil, false, nil
	}
	c.waiter = ch
	c.qmu.Unlock()

	if err := c.sendLine(line); err != nil {
		// A write error does not prove nothing went out: a Write can report
		// failure having transmitted the whole line. So this retires rather
		// than simply clearing — if the query DID reach the server, its reply
		// is owed, and answerPresence will un-retire the connection when it
		// lands. Clearing alone would let that reply answer the next caller.
		c.clearWaiter(ch, true)
		return nil, false, err
	}

	t := time.NewTimer(presenceTimeout)
	defer t.Stop()
	select {
	case r := <-ch:
		c.clearWaiter(ch, false)
		return r.online, r.ok, r.err
	case <-c.closed:
		c.clearWaiter(ch, false)
		return nil, false, ErrClosed
	case <-t.C:
		// The reply can land in the instant the timer fires, and then this
		// query is not unanswered at all. clearWaiter settles that under the
		// same lock the delivery takes — it only pauses the connection if
		// this mailbox was still installed — so the decision cannot fall
		// between the two facts. Pausing while holding the answer would
		// disable presence over a scheduling coincidence AND leave nothing
		// owed to bring it back.
		c.clearWaiter(ch, true)
		select {
		case r := <-ch:
			return r.online, r.ok, r.err
		default:
		}
		// A reply is still owed and can arrive at any time, with nothing in
		// it to say which query it belongs to. Rather than let it be handed
		// to the next caller, this connection stops asking until that reply
		// arrives and puts it back in sync.
		return nil, false, fmt.Errorf("irc: no ISON reply within %s (presence checks paused for this connection)", presenceTimeout)
	}
}

// clearWaiter releases this query's mailbox and, when asked, pauses presence
// on the connection — but ONLY if the mailbox was still installed. A mailbox
// that is gone was taken by answerPresence, which means the reply is already
// in it: this query was answered, nothing is owed, and pausing here would
// retire a healthy connection with no owed reply left to revive it.
//
// The identity comparison is also what stops a stale caller from clearing a
// live query's mailbox.
func (c *Client) clearWaiter(ch chan presenceReply, pause bool) {
	c.qmu.Lock()
	defer c.qmu.Unlock()
	if c.waiter != ch {
		return // answered, or never installed: not ours to clear or to pause
	}
	c.waiter = nil
	if pause {
		c.dead = true
	}
}

// answerPresence hands one reply to the outstanding query, if any is still
// waiting. A late or unsolicited reply lands here with no waiter and is
// dropped, which is the whole point of there being at most one.
//
// Dropping it is also what UN-retires the connection. At most one reply can
// ever be owed — a query only goes out when none is outstanding and the
// connection is not retired — so a reply that arrives with nobody waiting IS
// the owed one, and taking it puts the connection back in sync. That matters
// because the common way to time out here is a slow server rather than a
// silent one (see presenceTimeout): without this, one busy minute would
// disable presence until the next reconnect, which can be days.
func (c *Client) answerPresence(r presenceReply) {
	c.qmu.Lock()
	defer c.qmu.Unlock()
	if c.waiter == nil {
		c.dead = false
		return
	}
	c.waiter <- r
	c.waiter = nil
}

// failPresence wakes an outstanding query when the connection ends, so a
// caller learns the connection died instead of waiting out its own timeout.
// A dead connection has nothing owed to it, so this must not un-retire.
func (c *Client) failPresence(err error) {
	c.qmu.Lock()
	defer c.qmu.Unlock()
	if c.waiter == nil {
		return
	}
	c.waiter <- presenceReply{err: err}
	c.waiter = nil
}

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
	defer close(c.keepAliveDone)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-c.closed:
			return
		case <-c.readDone:
			// The read loop can end with the socket still writable — an
			// over-long line is the reachable case — and Events has closed,
			// so nobody is listening for the PONG. Without this arm the
			// loop PINGed a connection the daemon had already given up on,
			// for as long as the peer kept the socket open.
			return
		case <-t.C:
			if err := c.sendLine("PING :keepalive"); err != nil {
				return
			}
		}
	}
}

// finish records the terminal error, wakes an outstanding Presence, and
// closes the conn. Closing is what makes the death visible to writers: the
// read loop does not always end because the socket did (the over-long-line
// path ends it with the socket healthy), and a Presence installed after
// that point would otherwise send its ISON fine and then wait out the full
// presenceTimeout for a reply no loop is left to deliver.
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
	c.failPresence(err)
	c.conn.Close() // Close() filters the resulting net.ErrClosed
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
		line, err := readLine(br)
		if err != nil {
			if errors.Is(err, errLineTooLong) {
				c.finish(err) // a hostile peer gets cut off
			} else {
				c.finish(fmt.Errorf("irc: read: %w", err))
			}
			return
		}
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
	case "303": // RPL_ISON: <me> [:]<nick> <nick> ... — present names only.
		// The list is everything after the client identifier, and it does NOT
		// always arrive as a trailing parameter: measured on ergo 2.19.1,
		// `ISON alice` answers `303 me alice` — one name needs no colon —
		// while `ISON alice SmarterChild` answers `303 me :alice
		// SmarterChild`. Reading only the trailing form made every
		// single-name query, which is exactly what a DM asks, report that
		// nobody was online.
		online := append([]string(nil), m.params[min(1, len(m.params)):]...)
		online = append(online, strings.Fields(m.trailing)...)
		c.answerPresence(presenceReply{online: online, ok: true})
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
	case "421": // ERR_UNKNOWNCOMMAND: <me> <command> :Unknown command
		// A server without ISON answers the question by refusing it. That is
		// still an answer — "cannot tell" — and the waiter gets it now
		// instead of timing out. It stays an error event as well: a numeric
		// this client provoked is not something to swallow.
		if len(m.params) >= 2 && strings.ToUpper(m.params[1]) == "ISON" {
			c.answerPresence(presenceReply{ok: false})
		}
		return []tocwire.Event{tocwire.ServerError{Code: m.cmd + " " + m.trailing}}
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

// dropRunes removes every rune of bad from s. The two sanitizers below are
// this with different bad sets; they were two copies of the same rune map.
func dropRunes(s, bad string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(bad, r) {
			return -1
		}
		return r
	}, s)
}

// sanitizeParam strips line-injection bytes from a single parameter.
func sanitizeParam(s string) string { return dropRunes(s, "\r\n\x00") }

// sanitizeText strips CR/NUL (newlines are handled by splitMessage).
func sanitizeText(s string) string { return dropRunes(s, "\r\x00") }

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
