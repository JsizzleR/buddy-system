package buddylist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/JsizzleR/buddy-system/internal/tocwire"
)

// maxBody bounds any single journaled body: server frames are hostile-ish
// input and the journal is read back into agent context.
const maxBody = 4096

// Conn is the slice of tocwire.Client the daemon uses; a seam for hermetic tests.
type Conn interface {
	Events() <-chan tocwire.Event
	Err() error
	ChatJoin(room string) error
	ChatSend(roomID, text string) error
	IM(to, text string) error
	SetAway(text string) error
	Close() error
}

// Dialer opens a connection to the chat server.
type Dialer func(ctx context.Context) (Conn, error)

// Config for the daemon.
type Config struct {
	Rooms []string // rooms to join and journal
	// APIAddr is the server's Management API (host:port). When set AND the
	// exchange is 5, rooms are created there before each connect: on the
	// pinned server a TOC join does NOT create a missing EXCHANGE-5 room
	// (chatNavService.CreateRoom errors → ERROR:913; measured). Exchange-4
	// joins create on demand (also measured), so no API step is needed there.
	APIAddr string
	// Exchange the daemon's rooms live on. 0 means 5 (public). AIM 5.x's own
	// Buddy Chat dialog creates rooms on exchange 4, so a fleet the operator
	// joins from real AIM should run with Exchange=4.
	Exchange   int
	SocketPath string
	Journal    *Journal
	Dial       Dialer
	Log        *slog.Logger
	// MaxBackoff caps the reconnect backoff (default 60s).
	MaxBackoff time.Duration
	// now is a test seam; nil = wall clock.
	Now func() time.Time
}

// Daemon is the concierge: one server connection, a journal, a socket API.
type Daemon struct {
	cfg Config
	log *slog.Logger

	mu        sync.Mutex
	conn      Conn
	status    string                     // last requested away/status text; reapplied on reconnect
	roomIDs   map[string]string          // room name (folded) → room id
	roomNames map[string]string          // room id → room name
	members   map[string]map[string]bool // room name → screen names present
	lastError string
}

func New(cfg Config) (*Daemon, error) {
	if cfg.Journal == nil || cfg.Dial == nil || cfg.SocketPath == "" {
		return nil, errors.New("buddylist: Journal, Dial, and SocketPath are required")
	}
	// Unix socket paths are capped near 104 bytes on darwin; bind fails with
	// the cryptic "invalid argument", so refuse loudly up front.
	if len(cfg.SocketPath) > 100 {
		return nil, fmt.Errorf("buddylist: socket path is %d bytes; unix sockets cap near 104 — use a shorter path", len(cfg.SocketPath))
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 60 * time.Second
	}
	return &Daemon{
		cfg:       cfg,
		log:       cfg.Log,
		roomIDs:   map[string]string{},
		roomNames: map[string]string{},
		members:   map[string]map[string]bool{},
	}, nil
}

// Run serves the socket and maintains the server connection until ctx ends.
func (d *Daemon) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// A daemon without its socket is headless: a socket failure ends the run
	// rather than leaving a connected-but-unreachable ghost (Codex finding).
	srvErr := make(chan error, 1)
	go func() {
		err := d.serveSocket(ctx)
		if err != nil {
			d.log.Error("socket server failed; shutting down", "err", err)
			cancel()
		}
		srvErr <- err
	}()

	backoff := time.Second
	for {
		if ctx.Err() != nil {
			break
		}
		c, err := d.cfg.Dial(ctx)
		if err != nil {
			d.noteSystem(fmt.Sprintf("connect failed: %v (retrying in ~%s)", err, backoff))
			if !sleepCtx(ctx, jitter(backoff)) {
				break
			}
			backoff = min(backoff*2, d.cfg.MaxBackoff)
			continue
		}
		// The watcher must be armed BEFORE ensureRooms/joins so a shutdown can
		// cut a connection wedged in either (Codex finding); it is released
		// when this connection ends either way. The event range itself only
		// ends when the connection dies.
		connCtx, connDone := context.WithCancel(ctx)
		go func() {
			<-connCtx.Done()
			c.Close()
		}()
		if err := d.ensureRooms(ctx); err != nil {
			d.log.Warn("room ensure failed; joins may fail on a fresh server", "err", err)
		}
		backoff = time.Second
		d.setConn(c)
		d.noteSystem("connected")
		d.mu.Lock()
		status := d.status
		d.mu.Unlock()
		if status != "" {
			if err := c.SetAway(status); err != nil {
				d.log.Error("status reapply failed", "err", err)
			}
		}
		for _, room := range d.cfg.Rooms {
			if err := c.ChatJoin(room); err != nil {
				d.log.Error("join failed", "room", room, "err", err)
			}
		}
		for ev := range c.Events() {
			d.handle(ev)
		}
		connDone()
		d.setConn(nil)
		d.noteSystem(fmt.Sprintf("disconnected: %v", c.Err()))
	}
	<-srvErr
	return ctx.Err()
}

// ensureRooms creates the configured rooms via the Management API (idempotent:
// 201 created, 409 exists). Skipped when no APIAddr is configured.
func (d *Daemon) ensureRooms(ctx context.Context) error {
	if d.cfg.APIAddr == "" || (d.cfg.Exchange != 0 && d.cfg.Exchange != 5) {
		return nil // only exchange-5 rooms need (or have) API pre-creation
	}
	client := &http.Client{Timeout: 5 * time.Second}
	for _, room := range d.cfg.Rooms {
		body, _ := json.Marshal(map[string]string{"name": room})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			"http://"+d.cfg.APIAddr+"/chat/room/public", bytes.NewReader(body))
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
			return fmt.Errorf("create room %q: HTTP %d", room, resp.StatusCode)
		}
	}
	return nil
}

func (d *Daemon) setConn(c Conn) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.conn = c
	if c == nil {
		// Room ids are per-connection; membership is unknown while away.
		d.roomIDs = map[string]string{}
		d.roomNames = map[string]string{}
		d.members = map[string]map[string]bool{}
	}
}

func (d *Daemon) handle(ev tocwire.Event) {
	switch e := ev.(type) {
	case tocwire.ChatJoin:
		d.mu.Lock()
		d.roomIDs[fold(e.Room)] = e.RoomID
		d.roomNames[e.RoomID] = e.Room
		d.mu.Unlock()
	case tocwire.ChatIn:
		room := d.roomName(e.RoomID)
		d.append(room, e.From, "chat", htmlToText(e.Text))
	case tocwire.IMIn:
		d.append("@dm", e.From, "im", htmlToText(e.Text))
	case tocwire.ChatUpdateBuddy:
		room := d.roomName(e.RoomID)
		d.mu.Lock()
		set := d.members[room]
		if set == nil {
			set = map[string]bool{}
			d.members[room] = set
		}
		for _, n := range e.Names {
			set[n] = e.Present
		}
		d.mu.Unlock()
		verb := "joined"
		if !e.Present {
			verb = "left"
		}
		d.append(room, "", "presence", strings.Join(e.Names, ", ")+" "+verb)
	case tocwire.ServerError:
		d.append("", "", "system", "server error "+e.Code)
	case tocwire.UpdateBuddy, tocwire.Unknown:
		// Buddy-level presence and unrecognized lines are not journaled;
		// they are frequent and carry nothing a reader acts on. (P4 presence
		// will consume UpdateBuddy from its own connection.)
	}
}

func (d *Daemon) roomName(id string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if n, ok := d.roomNames[id]; ok {
		return n
	}
	return "room-" + id
}

func (d *Daemon) append(room, sender, kind, body string) {
	if len(body) > maxBody {
		body = body[:maxBody] + "…[truncated]"
	}
	if _, err := d.cfg.Journal.Append(room, sender, kind, body); err != nil {
		d.log.Error("journal append failed", "err", err)
	}
}

func (d *Daemon) noteSystem(body string) {
	d.mu.Lock()
	d.lastError = body
	d.mu.Unlock()
	d.append("", "", "system", body)
}

// Say relays text into a room as [from]. It fails visibly when disconnected
// or not yet joined — never a silent apparent success. Semantics are
// SUBMITTED, NOT CONFIRMED: the room's authoritative row is written when the
// server reflects the message back, and a durable intent row goes to the
// "@sent" outbox FIRST, so a send whose echo never returns is still on the
// record instead of vanishing (Codex finding). A success can still race a
// dying connection — that window is inherent to TCP and accepted.
func (d *Daemon) Say(room, from, text string) error {
	d.mu.Lock()
	c := d.conn
	id, joined := d.roomIDs[fold(room)]
	d.mu.Unlock()
	if c == nil {
		return errors.New("not connected to the chat server")
	}
	if !joined {
		return fmt.Errorf("not joined to room %q", room)
	}
	if from != "" {
		text = "[" + from + "] " + text
	}
	d.append("@sent", from, "system", room+": "+text)
	return c.ChatSend(id, text)
}

// DM sends an instant message. Same visibility rules as Say.
func (d *Daemon) DM(to, from, text string) error {
	d.mu.Lock()
	c := d.conn
	d.mu.Unlock()
	if c == nil {
		return errors.New("not connected to the chat server")
	}
	if from != "" {
		text = "[" + from + "] " + text
	}
	return c.IM(to, text)
}

// Status sets (or with "" clears) the concierge's away text, remembering it
// across reconnects. Fails visibly while disconnected, like Say.
func (d *Daemon) Status(text string) error {
	d.mu.Lock()
	d.status = text
	c := d.conn
	d.mu.Unlock()
	if c == nil {
		return errors.New("not connected to the chat server")
	}
	return c.SetAway(text)
}

// Who reports current room membership (empty while disconnected: presence is
// per-connection state and unknown history is reported as unknown, not as
// empty rooms that look authoritative — the connected flag disambiguates).
func (d *Daemon) Who() (connected bool, rooms map[string][]string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rooms = map[string][]string{}
	for room, set := range d.members {
		for n, present := range set {
			if present {
				rooms[room] = append(rooms[room], n)
			}
		}
	}
	return d.conn != nil, rooms
}

// Health reports connection state and the last system note.
func (d *Daemon) Health() (connected bool, note string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.conn != nil, d.lastError
}

func fold(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func jitter(d time.Duration) time.Duration {
	return d/2 + time.Duration(rand.Int63n(int64(d)/2+1))
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
