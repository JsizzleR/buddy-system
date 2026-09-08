package buddylist

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/tocwire"
)

// pinned is the presence clock. Presence ages sessions, so every idle/drop
// assertion here is a clock advance rather than a sleep.
type pinned struct {
	mu sync.Mutex
	t  time.Time
}

func (p *pinned) now() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.t
}

func (p *pinned) advance(d time.Duration) {
	p.mu.Lock()
	p.t = p.t.Add(d)
	p.mu.Unlock()
}

// dialScript answers DialAs. It records the names asked for, in order, which
// is what the collision-walk assertions are actually about.
type dialScript struct {
	mu    sync.Mutex
	asked []string
	fail  map[string]error
	conns map[string]*fakeConn

	// hold, when set, parks every dial after it has been recorded until the
	// channel is closed (or ctx ends). It is how a test puts a retirement
	// INSIDE the dial window, which is the only place the orphaned-connection
	// race can be reproduced deterministically. held reports each parked
	// dial so the test knows the window is open before it acts.
	hold chan struct{}
	held chan string
}

func newDialScript() *dialScript {
	return &dialScript{fail: map[string]error{}, conns: map[string]*fakeConn{}}
}

func (s *dialScript) dial(ctx context.Context, nick string) (Conn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, nick)
	if err := s.fail[nick]; err != nil {
		return nil, err
	}
	if s.hold != nil {
		// Released so the parked dial never deadlocks the fixture's other
		// callers; the gate is the test's, not the mutex's.
		s.mu.Unlock()
		s.held <- nick
		select {
		case <-s.hold:
		case <-ctx.Done():
			s.mu.Lock()
			return nil, ctx.Err()
		}
		s.mu.Lock()
	}
	c := newFakeConn()
	s.conns[nick] = c
	return c, nil
}

func (s *dialScript) names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.asked...)
}

func (s *dialScript) conn(nick string) *fakeConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns[nick]
}

type presenceHarness struct {
	d      *Daemon
	j      *Journal
	script *dialScript
	clk    *pinned
	conn   *fakeConn // the concierge's own connection
}

// startPresence runs a daemon serving two project rooms with presence on.
func startPresence(t *testing.T) *presenceHarness {
	t.Helper()
	dir := t.TempDir()
	j, err := OpenJournal(filepath.Join(dir, "journal.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	sockDir, err := os.MkdirTemp("", "fp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })

	h := &presenceHarness{
		j:      j,
		script: newDialScript(),
		clk:    &pinned{t: time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)},
		conn:   newFakeConn(),
	}
	served := make(chan *fakeConn, 1)
	served <- h.conn
	d, err := New(Config{
		Rooms:      []string{"buddy-system", "harbor"},
		SocketPath: filepath.Join(sockDir, "d.sock"),
		Journal:    j,
		Log:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		MaxBackoff: 20 * time.Millisecond,
		Now:        h.clk.now,
		Dial: func(ctx context.Context) (Conn, error) {
			select {
			case c := <-served:
				return c, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
		DialAs: h.script.dial,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.d = d
	// Set before Run: the manager reads its tick when the loop starts. Short,
	// because a wake is the normal trigger and the ticker is only the backstop.
	d.presence.tick = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(settleBudget):
			t.Error("daemon did not shut down")
		}
	})
	return h
}

// waitFor polls until pred holds. Presence is reconciled by a background loop,
// so every assertion about it is an eventual one.
func waitFor(t *testing.T, what string, pred func() bool) {
	t.Helper()
	deadline := time.Now().Add(settleBudget)
	for time.Now().Before(deadline) {
		if pred() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func recordedHas(c *fakeConn, want string) bool {
	if c == nil {
		return false
	}
	for _, s := range c.recorded() {
		if s == want {
			return true
		}
	}
	return false
}

// The main path, driven through the socket op that actually carries presence
// in production: the PostToolUse alert. A session that reports in becomes a
// buddy in its OWN project's room, wearing its claim.
func TestPresenceJoinsTheProjectRoomAndWearsTheClaim(t *testing.T) {
	h := startPresence(t)
	resp := h.d.dispatch(Request{Op: "alerts", Session: "sess-a", Label: "harbor/s-e284b102",
		Slugs: []string{"api-inspect-markers"}, Mentions: []string{"api-inspect-markers"}})
	if !resp.OK {
		t.Fatalf("alerts refused: %s", resp.Error)
	}
	waitFor(t, "buddy to join its room", func() bool {
		return recordedHas(h.script.conn("harbor-s-e284b102"), "join harbor")
	})
	waitFor(t, "away text to carry the claim", func() bool {
		return recordedHas(h.script.conn("harbor-s-e284b102"), "away claim: api-inspect-markers · active")
	})
	// The room is derived from the label, so a session in another project
	// must not land in this one.
	if names := h.script.names(); len(names) != 1 || names[0] != "harbor-s-e284b102" {
		t.Fatalf("unexpected dials: %v", names)
	}
}

// A label whose project this daemon serves no room for is not presented at
// all. Presence must never invent a room: an unserved project silently
// joining one would put a session in a room its operator is not watching.
func TestPresenceSkipsLabelsWithNoServedRoom(t *testing.T) {
	h := startPresence(t)
	h.d.presence.note("sess-x", "jayclark.ai/s-11111111", []string{"site-copy"})
	h.d.presence.note("sess-y", "harbor/s-22222222", nil)
	waitFor(t, "the served session to join", func() bool {
		return recordedHas(h.script.conn("harbor-s-22222222"), "join harbor")
	})
	for _, n := range h.script.names() {
		if strings.HasPrefix(n, "jayclark") {
			t.Fatalf("unserved project was dialed: %v", h.script.names())
		}
	}
	if online, wanted := h.d.presence.live(); online != 1 || wanted != 1 {
		t.Fatalf("presence counted an unserved session: online=%d wanted=%d", online, wanted)
	}
}

// A taken name walks the suffix: the common cause is this session's own dead
// predecessor whose socket has not timed out yet.
func TestPresenceWalksTheSuffixOnANameCollision(t *testing.T) {
	h := startPresence(t)
	h.script.fail["harbor-s-33333333"] = fmt.Errorf("registration refused (433): %w", ErrNickInUse)
	h.d.presence.note("sess-c", "harbor/s-33333333", []string{"claimed"})
	waitFor(t, "the suffixed name to connect", func() bool {
		return recordedHas(h.script.conn("harbor-s-33333333-2"), "join harbor")
	})
	if names := h.script.names(); len(names) != 2 || names[1] != "harbor-s-33333333-2" {
		t.Fatalf("collision walk: %v", names)
	}
}

// A server or network failure must NOT walk the suffix. Renaming a session
// over a connection refusal would hide the real fault behind a buddy that
// answers to a name nobody addresses.
func TestPresenceKeepsItsNameWhenTheFailureIsNotACollision(t *testing.T) {
	h := startPresence(t)
	h.script.fail["harbor-s-44444444"] = errors.New("dial tcp 127.0.0.1:6667: connection refused")
	h.d.presence.note("sess-d", "harbor/s-44444444", nil)
	waitFor(t, "the failed dial to be recorded", func() bool { return len(h.script.names()) > 0 })
	// Give the manager room to make a wrong second attempt if it were going to.
	time.Sleep(50 * time.Millisecond)
	for _, n := range h.script.names() {
		if n != "harbor-s-44444444" {
			t.Fatalf("a non-collision failure renamed the session: %v", h.script.names())
		}
	}
	if online, wanted := h.d.presence.live(); online != 0 || wanted != 1 {
		t.Fatalf("a failed dial must stay wanted but not online: online=%d wanted=%d", online, wanted)
	}
}

// Idle is a status, not a disconnect: an agent waiting on its operator is
// still there. Leaving happens later, at the ledger's own STALE threshold, so
// the room and `buddy ls` tell the same story.
func TestPresenceGoesIdleThenLeaves(t *testing.T) {
	h := startPresence(t)
	h.d.presence.note("sess-e", "harbor/s-55555555", []string{"slow-work"})
	waitFor(t, "buddy online", func() bool {
		return recordedHas(h.script.conn("harbor-s-55555555"), "away claim: slow-work · active")
	})

	h.clk.advance(presenceIdleAfter + time.Minute)
	h.d.presence.wakeSoon()
	waitFor(t, "idle status", func() bool {
		return recordedHas(h.script.conn("harbor-s-55555555"), "away claim: slow-work · idle 5m")
	})

	h.clk.advance(presenceDropAfter)
	h.d.presence.wakeSoon()
	waitFor(t, "buddy to leave", func() bool {
		_, wanted := h.d.presence.live()
		return wanted == 0
	})
	c := h.script.conn("harbor-s-55555555")
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if !closed {
		t.Fatal("a retired buddy must close its connection, not leak it")
	}
}

// SessionEnd retires a buddy at once rather than leaving a ghost in the room
// for half an hour.
func TestPresenceGoneRetiresImmediately(t *testing.T) {
	h := startPresence(t)
	h.d.presence.note("sess-f", "harbor/s-66666666", nil)
	waitFor(t, "buddy online", func() bool {
		return recordedHas(h.script.conn("harbor-s-66666666"), "join harbor")
	})
	resp := h.d.dispatch(Request{Op: "presence", Session: "sess-f", Gone: true})
	if !resp.OK {
		t.Fatalf("presence op refused: %s", resp.Error)
	}
	if online, wanted := h.d.presence.live(); online != 0 || wanted != 0 {
		t.Fatalf("gone must retire the buddy: online=%d wanted=%d", online, wanted)
	}
	c := h.script.conn("harbor-s-66666666")
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if !closed {
		t.Fatal("gone must close the connection")
	}
}

// A retirement that lands while the dial is in flight must not leave a ghost.
// forget, drop-after expiry, and shutdown can only close the conn they can see,
// and a dialing buddy's is nil; the dial then landed on the orphaned struct,
// installed the conn, and started drain. Nothing ever closed it: a nick that
// stayed in the room until the daemon restarted, a leaked socket, and a
// connection that neither the 16-cap nor live() could count. The positive
// control is the same parked dial with no retirement, proving the park itself
// does not stop a join.
func TestPresenceDialLandingAfterRetirementIsClosed(t *testing.T) {
	const session, label, nick = "sess-g", "harbor/s-77777777", "harbor-s-77777777"
	cases := []struct {
		name       string
		retire     func(t *testing.T, h *presenceHarness) // nil: the positive control
		wantClosed bool
		wantOnline int
	}{
		{name: "positive control: no retirement, the parked dial joins", wantClosed: false, wantOnline: 1},
		{name: "forget (SessionEnd) during the dial", wantClosed: true, wantOnline: 0,
			retire: func(t *testing.T, h *presenceHarness) {
				h.d.presence.forget(session)
			}},
		{name: "drop-after expiry during the dial", wantClosed: true, wantOnline: 0,
			retire: func(t *testing.T, h *presenceHarness) {
				h.clk.advance(h.d.presence.dropAfter + time.Minute)
				waitFor(t, "reconcile to expire the buddy", func() bool {
					_, wanted := h.d.presence.live()
					return wanted == 0
				})
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := startPresence(t)
			h.script.hold, h.script.held = make(chan struct{}), make(chan string, 1)
			h.d.presence.note(session, label, nil)
			select {
			case got := <-h.script.held:
				if got != nick {
					t.Fatalf("parked dial for %q, want %q", got, nick)
				}
			case <-time.After(settleBudget):
				t.Fatal("dial never started")
			}
			if tc.retire != nil {
				tc.retire(t, h)
			}
			close(h.script.hold)

			what := "the parked dial to join"
			if tc.wantClosed {
				what = "the landed connection to be closed"
			}
			waitFor(t, what, func() bool {
				c := h.script.conn(nick)
				if c == nil {
					return false
				}
				if !tc.wantClosed {
					return recordedHas(c, "join harbor")
				}
				c.mu.Lock()
				defer c.mu.Unlock()
				return c.closed
			})
			c := h.script.conn(nick)
			c.mu.Lock()
			closed := c.closed
			c.mu.Unlock()
			if closed != tc.wantClosed {
				t.Fatalf("closed=%v, want %v", closed, tc.wantClosed)
			}
			if online, wanted := h.d.presence.live(); online != tc.wantOnline || wanted != tc.wantOnline {
				t.Fatalf("live()=%d/%d, want %d/%d", online, wanted, tc.wantOnline, tc.wantOnline)
			}
		})
	}
}

// Session connections see the same room traffic the concierge does. If they
// journaled it, every message would be stored once per live session and the
// journal's "what the server saw" contract — one connection's view — would be
// meaningless.
func TestPresenceConnectionsNeverJournal(t *testing.T) {
	h := startPresence(t)
	h.d.presence.note("sess-g", "harbor/s-77777777", nil)
	waitFor(t, "buddy online", func() bool {
		return recordedHas(h.script.conn("harbor-s-77777777"), "join harbor")
	})
	c := h.script.conn("harbor-s-77777777")
	c.push(t, tocwire.ChatIn{RoomID: "7", From: "operator", Text: "seen by every session"})
	c.push(t, tocwire.IMIn{From: "operator", Text: "a dm to somebody"})

	// The concierge's own room join is the observable that proves the daemon
	// processed events at all while the presence rows stayed out.
	waitJoined(t, h.conn, "harbor")
	time.Sleep(50 * time.Millisecond)
	for _, room := range []string{"harbor", "@dm"} {
		msgs, _, err := h.j.ReadAfter(room, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range msgs {
			if strings.Contains(m.Body, "seen by every session") || strings.Contains(m.Body, "a dm to somebody") {
				t.Fatalf("a presence connection journaled %q into %s", m.Body, room)
			}
		}
	}
}

// The socket exempts localhost from its own connection limits, so the bound on
// sockets a runaway fleet can open is ours to enforce.
func TestPresenceCapsConcurrentBuddies(t *testing.T) {
	h := startPresence(t)
	for i := 0; i < maxPresenceBuddies+4; i++ {
		h.d.presence.note(fmt.Sprintf("sess-%02d", i), fmt.Sprintf("harbor/s-%08d", i), nil)
	}
	waitFor(t, "the cap to fill", func() bool {
		online, _ := h.d.presence.live()
		return online == maxPresenceBuddies
	})
	time.Sleep(50 * time.Millisecond)
	if online, wanted := h.d.presence.live(); online != maxPresenceBuddies || wanted != maxPresenceBuddies {
		t.Fatalf("cap exceeded: online=%d wanted=%d cap=%d", online, wanted, maxPresenceBuddies)
	}
	if n := len(h.script.names()); n > maxPresenceBuddies {
		t.Fatalf("dialed %d connections past a cap of %d", n, maxPresenceBuddies)
	}
}

// Presence is optional. With no DialAs the daemon is exactly what it was
// before this existed, and the carrier op must not care.
func TestPresenceDisabledIsInert(t *testing.T) {
	h := start(t) // the plain harness: no DialAs
	if h.d.presence != nil {
		t.Fatal("presence must be off without DialAs")
	}
	resp := h.d.dispatch(Request{Op: "alerts", Session: "sess-h", Label: "lobby/s-1",
		Slugs: []string{"some-claim"}, Mentions: []string{"some-claim"}})
	if !resp.OK {
		t.Fatalf("alerts must work with presence off: %s", resp.Error)
	}
	if resp := h.d.dispatch(Request{Op: "presence", Session: "sess-h"}); !resp.OK {
		t.Fatalf("presence op must answer OK with presence off: %s", resp.Error)
	}
}

// The room lookup folds by the ONE rule the ledger uses (invariant 13),
// which includes NFC. Before, fold here was ToLower(TrimSpace) — a second
// rule — so a room configured precomposed never matched a label whose
// project half arrived decomposed, and that session was never presented.
// The lookup is exercised in both directions and with the padding --rooms
// leaves in, so a fix that normalized only one side would show.
func TestServedRoomFoldsLikeTheLedger(t *testing.T) {
	const nfc, nfd = "caf\u00e9", "cafe\u0301"
	cases := []struct {
		name  string
		rooms []string
		label string
		want  string
	}{
		{"exact", []string{"harbor"}, "harbor/s-11111111", "harbor"},
		{"case folds", []string{"Harbor"}, "harbor/s-11111111", "Harbor"},
		{"NFC room, NFD label", []string{nfc}, nfd + "/s-11111111", nfc},
		{"NFD room, NFC label", []string{nfd}, nfc + "/s-11111111", nfd},
		{"padded room from --rooms", []string{" harbor"}, "harbor/s-11111111", " harbor"},
		{"no such project", []string{"harbor"}, "elsewhere/s-11111111", ""},
		{"empty project half", []string{"harbor"}, "/s-11111111", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &Daemon{cfg: Config{Rooms: tc.rooms}}
			if got := d.servedRoom(tc.label); got != tc.want {
				t.Fatalf("servedRoom(%q) with rooms %q = %q, want %q", tc.label, tc.rooms, got, tc.want)
			}
		})
	}
}

func TestNickForDerivesALegalName(t *testing.T) {
	cases := []struct{ label, want string }{
		{"harbor/s-e284b102", "harbor-s-e284b102"},
		{"buddy-system/s-03bf0f5c", "buddy-system-s-03bf0f5c"},
		{"jayclark.ai/s-11111111", "jayclark-ai-s-11111111"},
		{"weird name!/s-1", "weird-name-s-1"},
		{"//s-1", "s-1"},
		{"", ""},
		{"///", ""},
		// Over-long labels lose project characters, never the identifying
		// tail — two sessions of one project must not collapse to one nick.
		{"a-very-long-project-name-indeed/s-abcdef12", "long-project-name-indeed-s-abcdef12"[len("long-project-name-indeed-s-abcdef12")-maxNickLen:]},
	}
	for _, c := range cases {
		if got := nickFor(c.label); got != c.want {
			t.Errorf("nickFor(%q) = %q, want %q", c.label, got, c.want)
		}
		if got := nickFor(c.label); len(got) > maxNickLen {
			t.Errorf("nickFor(%q) = %q exceeds %d bytes", c.label, got, maxNickLen)
		}
	}
}
