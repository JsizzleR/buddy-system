// Package tocwire is a minimal, transport-only TOC protocol client for
// open-oscar-server, used by chatd. It owns the measured handshake, a read
// loop that surfaces server lines as typed events, and escaped/quoted sends.
// It deliberately has NO reconnect/backoff policy — the daemon owns that.
package tocwire

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/mk6i/open-oscar-server/wire"
)

const (
	defaultHandshakeTimeout = 10 * time.Second
	defaultKeepAlive        = 60 * time.Second

	// defaultChatExchange: 5 is the "public" exchange the API can pre-create
	// rooms on; 4 is the "buddy chat" exchange AIM 5.x's own invite dialog
	// uses (measured: the dialog created `lobby` on 4 while the concierge
	// sat on 5 — two rooms, one name). WithChatExchange overrides.
	defaultChatExchange = "5"
)

// ErrClosed is returned by send methods after Close (or after the connection
// died).
var ErrClosed = errors.New("toc: client closed")

type options struct {
	handshakeTimeout time.Duration
	keepAlive        time.Duration
	chatExchange     string
}

// Option configures Dial.
type Option func(*options)

// WithHandshakeTimeout bounds the whole dial+handshake. Default 10s.
func WithHandshakeTimeout(d time.Duration) Option {
	return func(o *options) { o.handshakeTimeout = d }
}

// WithChatExchange sets the exchange ChatJoin uses (default 5; AIM 5.x's
// invite dialog lives on 4).
func WithChatExchange(n int) Option {
	return func(o *options) { o.chatExchange = fmt.Sprintf("%d", n) }
}

// WithKeepAlive sets the FLAP keepalive interval. Zero disables keepalives.
// Default 60s.
func WithKeepAlive(d time.Duration) Option {
	return func(o *options) { o.keepAlive = d }
}

// Client is a live TOC connection. Send methods are safe for concurrent use.
type Client struct {
	conn net.Conn
	fc   *wire.FlapClient

	events   chan Event
	closed   chan struct{} // closed by Close; unblocks the read loop's delivery
	readDone chan struct{} // closed when the read loop has exited

	chatExchange string

	closeOnce sync.Once
	closeErr  error

	errMu sync.Mutex
	err   error
}

// Dial connects to a TOC server at addr and performs the full measured
// handshake (docs/p0-facts.md): FLAPON preamble, server signon frame, client
// signon frame carrying the screen name TLV, toc_signon with the roasted
// password, wait for SIGN_ON, then toc_init_done. On success the read loop is
// running and Events() is live.
func Dial(ctx context.Context, addr, screenName, password string, opts ...Option) (*Client, error) {
	o := options{
		handshakeTimeout: defaultHandshakeTimeout,
		keepAlive:        defaultKeepAlive,
		chatExchange:     defaultChatExchange,
	}
	for _, opt := range opts {
		opt(&o)
	}
	if err := checkBareArg("screen name", screenName); err != nil {
		return nil, err
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("toc: dial %s: %w", addr, err)
	}

	// Bound the whole handshake: a hard deadline on the conn, plus a watcher
	// that closes the conn if ctx is cancelled mid-handshake.
	deadline := time.Now().Add(o.handshakeTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		conn.Close()
		return nil, fmt.Errorf("toc: set handshake deadline: %w", err)
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
	stopWatcher := func() {
		close(watcherDone)
		<-watcherExited
	}

	fc, err := handshake(conn, screenName, password)
	if err != nil {
		stopWatcher()
		conn.Close()
		if ctx.Err() != nil {
			return nil, fmt.Errorf("toc: handshake: %w", ctx.Err())
		}
		return nil, err
	}
	stopWatcher()
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("toc: clear handshake deadline: %w", err)
	}

	c := &Client{
		conn:         conn,
		fc:           fc,
		events:       make(chan Event),
		closed:       make(chan struct{}),
		readDone:     make(chan struct{}),
		chatExchange: o.chatExchange,
	}
	go c.readLoop()
	if o.keepAlive > 0 {
		go c.keepAliveLoop(o.keepAlive)
	}
	return c, nil
}

// handshake speaks the measured client side of signon on conn.
func handshake(conn net.Conn, screenName, password string) (*wire.FlapClient, error) {
	if _, err := conn.Write([]byte("FLAPON\r\n\r\n")); err != nil {
		return nil, fmt.Errorf("toc: send FLAPON: %w", err)
	}

	fc := wire.NewFlapClient(0, conn, conn)

	// Server sends its FLAP signon frame first.
	if _, err := fc.ReceiveSignonFrame(); err != nil {
		return nil, fmt.Errorf("toc: receive server signon frame: %w", err)
	}

	// Client signon frame with TLV 0x01 screen name.
	err := fc.SendSignonFrame([]wire.TLV{
		wire.NewTLVBE(wire.LoginTLVTagsScreenName, screenName),
	})
	if err != nil {
		return nil, fmt.Errorf("toc: send client signon frame: %w", err)
	}

	roasted := hex.EncodeToString(wire.RoastTOCPassword([]byte(password)))
	signon := fmt.Sprintf("toc_signon 127.0.0.1 9898 %s 0x%s english buddylist", screenName, roasted)
	if err := fc.SendDataFrame([]byte(signon)); err != nil {
		return nil, fmt.Errorf("toc: send toc_signon: %w", err)
	}

	// Wait for SIGN_ON. ERROR:nnn (e.g. bad credentials, rate limit) is a
	// refusal; anything else unexpected is a protocol error.
	for {
		frame, err := fc.ReceiveFLAP()
		if err != nil {
			return nil, fmt.Errorf("toc: receive signon reply: %w", err)
		}
		switch frame.FrameType {
		case wire.FLAPFrameKeepAlive:
			continue
		case wire.FLAPFrameData:
			line := payloadLine(frame.Payload)
			switch {
			case strings.HasPrefix(line, "SIGN_ON:"):
				if err := fc.SendDataFrame([]byte("toc_init_done")); err != nil {
					return nil, fmt.Errorf("toc: send toc_init_done: %w", err)
				}
				return fc, nil
			case strings.HasPrefix(line, "ERROR:"):
				return nil, fmt.Errorf("toc: signon refused: %s", line)
			default:
				return nil, fmt.Errorf("toc: unexpected signon reply %q", line)
			}
		default:
			return nil, fmt.Errorf("toc: unexpected FLAP frame type %d during signon", frame.FrameType)
		}
	}
}

// Events returns the stream of server events. The channel closes when the
// connection dies (or on Close); Err reports the terminal error after close.
func (c *Client) Events() <-chan Event { return c.events }

// Err returns the terminal read-loop error. It is meaningful once Events()
// has closed.
func (c *Client) Err() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.err
}

// Close shuts the connection down and waits for the read loop to exit. It is
// idempotent and safe to call concurrently.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		// The read loop's finish() also closes the conn on a read error, and
		// our own close(c.closed) can trigger exactly that read error first —
		// losing that benign race is not a Close failure.
		if err := c.conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			c.closeErr = err
		}
	})
	<-c.readDone
	return c.closeErr
}

// readLoop parses server frames into events until the connection dies.
func (c *Client) readLoop() {
	defer close(c.readDone)
	defer close(c.events)
	for {
		frame, err := c.fc.ReceiveFLAP()
		if err != nil {
			c.finish(fmt.Errorf("toc: read: %w", err))
			return
		}
		switch frame.FrameType {
		case wire.FLAPFrameData:
			ev := parseEvent(fromWire([]byte(payloadLine(frame.Payload))))
			select {
			case c.events <- ev:
			case <-c.closed:
				c.finish(ErrClosed)
				return
			}
		case wire.FLAPFrameSignoff:
			c.finish(errors.New("toc: server signoff"))
			return
		default:
			// Keepalives and anything else non-data: nothing to deliver.
		}
	}
}

// finish records the terminal error and makes sure the conn is closed so the
// keepalive loop and any in-flight send fail fast.
func (c *Client) finish(err error) {
	c.errMu.Lock()
	if c.err == nil {
		c.err = err
	}
	c.errMu.Unlock()
	c.conn.Close()
}

func (c *Client) keepAliveLoop(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if err := c.fc.SendKeepAliveFrame(); err != nil {
				return
			}
		case <-c.closed:
			return
		case <-c.readDone:
			return
		}
	}
}

// payloadLine converts a data-frame payload to a command line, dropping any
// trailing NUL terminators (the server tolerates and trims them likewise).
func payloadLine(p []byte) string {
	return string(trimTrailingNULs(p))
}

func trimTrailingNULs(p []byte) []byte {
	for len(p) > 0 && p[len(p)-1] == 0 {
		p = p[:len(p)-1]
	}
	return p
}

// send transmits one TOC command line. wire.FlapClient serializes concurrent
// writers internally.
func (c *Client) send(verb, line string) error {
	select {
	case <-c.closed:
		return fmt.Errorf("toc: %s: %w", verb, ErrClosed)
	default:
	}
	if err := c.fc.SendDataFrame(toWire(line)); err != nil {
		return fmt.Errorf("toc: %s: %w", verb, err)
	}
	return nil
}

// ChatJoin joins a chat room on the client's exchange (default 5; see
// WithChatExchange). The room id arrives asynchronously as a ChatJoin event.
func (c *Client) ChatJoin(room string) error {
	return c.send("toc_chat_join", "toc_chat_join "+c.chatExchange+" "+quoteText(room))
}

// ChatSend sends text to a joined room by the id from the ChatJoin event.
func (c *Client) ChatSend(roomID, text string) error {
	if err := checkBareArg("room id", roomID); err != nil {
		return err
	}
	return c.send("toc_chat_send", "toc_chat_send "+roomID+" "+quoteText(text))
}

// IM sends an instant message.
func (c *Client) IM(to, text string) error {
	if err := checkBareArg("screen name", to); err != nil {
		return err
	}
	return c.send("toc_send_im", "toc_send_im "+to+" "+quoteText(text))
}

// SetAway sets the away message; an empty string clears it.
func (c *Client) SetAway(text string) error {
	if text == "" {
		return c.send("toc_set_away", "toc_set_away")
	}
	return c.send("toc_set_away", "toc_set_away "+quoteText(text))
}

// AddBuddies adds screen names to the buddy list (presence via UpdateBuddy
// events). A no-op with zero names.
func (c *Client) AddBuddies(names ...string) error {
	if len(names) == 0 {
		return nil
	}
	for _, n := range names {
		if err := checkBareArg("screen name", n); err != nil {
			return err
		}
	}
	return c.send("toc_add_buddy", "toc_add_buddy "+strings.Join(names, " "))
}

// checkBareArg refuses values that would break the server's space-separated,
// quote-aware tokenizer when sent unquoted (screen names, room ids). Text
// arguments are escaped and quoted instead and take any bytes.
func checkBareArg(kind, v string) error {
	if v == "" {
		return fmt.Errorf("toc: empty %s", kind)
	}
	if strings.ContainsAny(v, " \t\r\n\"\\") {
		return fmt.Errorf("toc: %s %q contains whitespace or quoting characters", kind, v)
	}
	return nil
}
