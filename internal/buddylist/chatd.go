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

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/tocwire"
	"golang.org/x/text/unicode/norm"
)

// maxBody bounds any single journaled body: server frames are hostile-ish
// input and the journal is read back into agent context.
const maxBody = 4096

// journalTrimEvery is how often retention is reapplied. Trimming is continuous,
// not launch-only: a daemon that stays up for weeks would otherwise grow
// journal.db without bound, and the operator's only lever would be a restart.
const journalTrimEvery = time.Hour

// Conn is the slice of tocwire.Client the daemon uses; a seam for hermetic tests.
type Conn interface {
	Events() <-chan tocwire.Event
	Err() error
	ChatJoin(room string) error
	ChatSend(roomID, text string) error
	IM(to, text string) error
	SetAway(text string) error
	// Presence reports which of names the server currently knows to be
	// online. The second result is false when the backend CANNOT answer —
	// which is a different fact from "nobody is there" and must never be
	// read as absence. Names come back spelled the way the server spells
	// them, so callers compare folded.
	Presence(names ...string) ([]string, bool, error)
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
	// Keep is the journal retention window: rows older than it are trimmed
	// once at startup and then every TrimEvery. Retention lives here, next to
	// the journal, rather than in the binary that happens to own the flag — it
	// is a policy of the daemon's storage, and as fourteen lines of goroutine
	// in main() nothing could test it.
	//
	// Zero DISABLES trimming. That is not a defaulting convenience: Trim(0)
	// puts the cutoff at now and deletes the whole journal, so a daemon
	// constructed without a retention window (every hermetic fixture) must do
	// nothing rather than silently reap the rows the test just wrote.
	Keep time.Duration
	// TrimEvery is the retention loop's period; 0 means journalTrimEvery. A
	// test seam like Now — production has no reason to set it.
	TrimEvery time.Duration
	Dial      Dialer
	// DialAs opens an ADDITIONAL connection under a chosen name, which is how
	// a session becomes its own buddy in the room. Nil disables per-session
	// presence entirely and the daemon runs exactly as it did before it
	// existed — the concierge relay is untouched either way.
	//
	// An implementation must report a name collision as ErrNickInUse (wrapped
	// is fine); anything else is treated as a server or network failure and
	// retried under the same name.
	DialAs func(ctx context.Context, name string) (Conn, error)
	Log    *slog.Logger
	// MaxBackoff caps the reconnect backoff (default 60s).
	MaxBackoff time.Duration
	// now is a test seam; nil = wall clock.
	Now func() time.Time
}

// Daemon is the concierge: one server connection, a journal, a socket API.
type Daemon struct {
	cfg Config
	log *slog.Logger

	// presence is the per-session buddy manager, or nil when Config.DialAs is
	// unset. It is its own connections and its own state: nothing on the
	// concierge path consults it.
	presence *presence

	mu        sync.Mutex
	conn      Conn
	status    string                     // last requested away/status text; reapplied on reconnect
	roomIDs   map[string]string          // room name (folded) → room id
	roomNames map[string]string          // room id → room name
	members   map[string]map[string]bool // room name → screen names present
	lastError string
}

// now is the daemon's clock seam; presence ages sessions by it.
func (d *Daemon) now() time.Time {
	if d.cfg.Now != nil {
		return d.cfg.Now()
	}
	return time.Now()
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
	d := &Daemon{
		cfg:       cfg,
		log:       cfg.Log,
		roomIDs:   map[string]string{},
		roomNames: map[string]string{},
		members:   map[string]map[string]bool{},
	}
	if cfg.DialAs != nil {
		d.presence = newPresence(d)
	}
	return d, nil
}

// Run serves the socket and maintains the server connection until ctx ends.
func (d *Daemon) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Retention, before the socket accepts anything: the launch pass used to
	// run in main() ahead of New, and keeping it ahead of the listener keeps
	// that ordering — no caller can be mid-read of rows this is about to
	// delete.
	d.trimJournal()
	if d.cfg.Keep > 0 {
		go d.retainJournal(ctx)
	}

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

	// Presence runs beside the concierge, not inside it: its connections are
	// unaffected by a concierge reconnect, and a presence stall cannot hold up
	// the relay or the journal.
	if d.presence != nil {
		go d.presence.run(ctx)
	}

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
	// A socket failure is the reason we are exiting: report IT, not the
	// context.Canceled produced by our own shutdown cancel — supervisors
	// treat a clean exit as "don't restart".
	if err := <-srvErr; err != nil {
		return err
	}
	return ctx.Err()
}

// trimJournal applies the retention window once. A failure is LOGGED, not
// returned: a journal that cannot be trimmed is a disk problem, and taking the
// relay and the socket down over it would turn a growing file into an outage.
func (d *Daemon) trimJournal() {
	if d.cfg.Keep <= 0 {
		return // retention off; see Config.Keep for why 0 is not "trim everything"
	}
	n, err := d.cfg.Journal.Trim(d.cfg.Keep)
	if err != nil {
		d.log.Error("journal trim failed", "err", err)
		return
	}
	if n > 0 {
		d.log.Info("journal trimmed", "rows", n)
	}
}

// retainJournal reapplies it until ctx ends.
func (d *Daemon) retainJournal(ctx context.Context) {
	every := d.cfg.TrimEvery
	if every <= 0 {
		every = journalTrimEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.trimJournal()
		}
	}
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
	case tocwire.ChatLeft:
		// WE left the room (kicked or parted). Prune it so Say fails visibly
		// ("not joined") instead of claiming success on a dead membership, and
		// Who stops reporting the pre-kick roster as live. No auto-rejoin: an
		// operator kick is respected until the next (re)connect.
		room := d.roomName(e.RoomID)
		d.mu.Lock()
		delete(d.roomIDs, fold(room))
		delete(d.roomNames, e.RoomID)
		delete(d.members, room)
		d.mu.Unlock()
		d.append(room, "", "system", "concierge left the room (kicked or parted); relaying is off until reconnect")
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

// DM sends an instant message. It fails visibly like Say — but a DM has a
// second way to vanish that a room message does not: there is no offline
// delivery, so a message to a name with no session is simply refused by the
// server, long after the caller has been told it succeeded.
//
// Measured on the live stack before this check existed: `buddylist dm
// nobody-here-12345 "…"` printed nothing and exited 0. The server's refusal
// did arrive, and was journaled — as a roomless system row reading "server
// error 401 No such nick", naming neither the recipient nor the message, well
// after the process was gone. That is the shape of a silent failure: the
// evidence exists and reaches nobody who could act on it.
//
// So the recipient is looked up BEFORE the send and an absent one is refused,
// with nothing sent. Only a definite "not online" refuses: a backend that
// cannot answer, a probe that errors, a server without the query — all send,
// exactly as before. A failing presence check must cost a diagnosis, never
// the message.
//
// The window between the answer and the send is real, and is the same one Say
// already accepts: the recipient can quit inside it. This closes the failure
// that actually happens — nobody there at all, all night, which is precisely
// when a nightly report is sent — and does not pretend to close the one that
// races.
func (d *Daemon) DM(to, from, text string) error {
	d.mu.Lock()
	c := d.conn
	d.mu.Unlock()
	if c == nil {
		return errors.New("not connected to the chat server")
	}
	switch online, known, err := c.Presence(to); {
	case err != nil:
		d.log.Warn("presence check failed, sending anyway", "to", to, "err", err)
	case known && !containsFold(online, to):
		// The name is the caller's, and this error is read back by a model
		// through the MCP tool as well as by a person: one line, always.
		return fmt.Errorf("%s is not on the chat server: nothing was sent", fence.Line(to, 64))
	}
	if from != "" {
		text = "[" + from + "] " + text
	}
	return c.IM(to, text)
}

// containsFold reports whether names holds name, under the daemon's one
// folding rule — the server echoes its own spelling of a nick, which need not
// be the caller's.
func containsFold(names []string, name string) bool {
	want := fold(name)
	for _, n := range names {
		if fold(n) == want {
			return true
		}
	}
	return false
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
	connected, note = d.conn != nil, d.lastError
	d.mu.Unlock()
	// Presence is silent by design, which makes "is it working?" unanswerable
	// without a surface to ask from. This is that surface.
	if online, wanted := d.presence.live(); wanted > 0 {
		note = fmt.Sprintf("%s; presence: %d/%d session buddies online", note, online, wanted)
	}
	return connected, note
}

// fold is the ONE folding rule (invariant 13), spelled exactly as store.Fold
// spells it: strings.ToLower(norm.NFC.String(s)). It is restated here rather
// than imported because the chat half must not take a compile-time dependency
// on the ledger package for a string function — the ledger is the safety half,
// and this package must remain droppable without it. If store.Fold ever
// changes, this line changes with it; there is no third spelling.
//
// The failure that motivated it: the previous spelling was
// ToLower(TrimSpace(s)), a second rule with no normalization. A room
// configured as precomposed "café" then never matched a session label whose
// project half arrived decomposed (labels are derived from directory names,
// and a path can come back from the filesystem in either form), so the
// session was never presented and its room lookups keyed on a name nothing
// else used.
//
// TrimSpace stays as its own explicit step, not part of the rule: --rooms is
// comma-split without trimming, so "lobby, ops" reaches here padded.
func fold(s string) string { return strings.ToLower(norm.NFC.String(strings.TrimSpace(s))) }

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
