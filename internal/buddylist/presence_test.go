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
		Rooms:      []string{"buddy-system", "bastle"},
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
		case <-time.After(2 * time.Second):
			t.Error("daemon did not shut down")
		}
	})
	return h
}

// waitFor polls until pred holds. Presence is reconciled by a background loop,
// so every assertion about it is an eventual one.
func waitFor(t *testing.T, what string, pred func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
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
	resp := h.d.dispatch(Request{Op: "alerts", Session: "sess-a", Label: "bastle/s-e284b102",
		Slugs: []string{"b81-inspect-markers"}, Mentions: []string{"b81-inspect-markers"}})
	if !resp.OK {
		t.Fatalf("alerts refused: %s", resp.Error)
	}
	waitFor(t, "buddy to join its room", func() bool {
		return recordedHas(h.script.conn("bastle-s-e284b102"), "join bastle")
	})
	waitFor(t, "away text to carry the claim", func() bool {
		return recordedHas(h.script.conn("bastle-s-e284b102"), "away claim: b81-inspect-markers · active")
	})
	// The room is derived from the label, so a session in another project
	// must not land in this one.
	if names := h.script.names(); len(names) != 1 || names[0] != "bastle-s-e284b102" {
		t.Fatalf("unexpected dials: %v", names)
	}
}

// A label whose project this daemon serves no room for is not presented at
// all. Presence must never invent a room: an unserved project silently
// joining one would put a session in a room its operator is not watching.
func TestPresenceSkipsLabelsWithNoServedRoom(t *testing.T) {
	h := startPresence(t)
	h.d.presence.note("sess-x", "jayclark.ai/s-11111111", []string{"site-copy"})
	h.d.presence.note("sess-y", "bastle/s-22222222", nil)
	waitFor(t, "the served session to join", func() bool {
		return recordedHas(h.script.conn("bastle-s-22222222"), "join bastle")
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
	h.script.fail["bastle-s-33333333"] = fmt.Errorf("registration refused (433): %w", ErrNickInUse)
	h.d.presence.note("sess-c", "bastle/s-33333333", []string{"claimed"})
	waitFor(t, "the suffixed name to connect", func() bool {
		return recordedHas(h.script.conn("bastle-s-33333333-2"), "join bastle")
	})
	if names := h.script.names(); len(names) != 2 || names[1] != "bastle-s-33333333-2" {
		t.Fatalf("collision walk: %v", names)
	}
}

// A server or network failure must NOT walk the suffix. Renaming a session
// over a connection refusal would hide the real fault behind a buddy that
// answers to a name nobody addresses.
func TestPresenceKeepsItsNameWhenTheFailureIsNotACollision(t *testing.T) {
	h := startPresence(t)
	h.script.fail["bastle-s-44444444"] = errors.New("dial tcp 127.0.0.1:6667: connection refused")
	h.d.presence.note("sess-d", "bastle/s-44444444", nil)
	waitFor(t, "the failed dial to be recorded", func() bool { return len(h.script.names()) > 0 })
	// Give the manager room to make a wrong second attempt if it were going to.
	time.Sleep(50 * time.Millisecond)
	for _, n := range h.script.names() {
		if n != "bastle-s-44444444" {
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
	h.d.presence.note("sess-e", "bastle/s-55555555", []string{"slow-work"})
	waitFor(t, "buddy online", func() bool {
		return recordedHas(h.script.conn("bastle-s-55555555"), "away claim: slow-work · active")
	})

	h.clk.advance(presenceIdleAfter + time.Minute)
	h.d.presence.wakeSoon()
	waitFor(t, "idle status", func() bool {
		return recordedHas(h.script.conn("bastle-s-55555555"), "away claim: slow-work · idle 5m")
	})

	h.clk.advance(presenceDropAfter)
	h.d.presence.wakeSoon()
	waitFor(t, "buddy to leave", func() bool {
		_, wanted := h.d.presence.live()
		return wanted == 0
	})
	c := h.script.conn("bastle-s-55555555")
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
	h.d.presence.note("sess-f", "bastle/s-66666666", nil)
	waitFor(t, "buddy online", func() bool {
		return recordedHas(h.script.conn("bastle-s-66666666"), "join bastle")
	})
	resp := h.d.dispatch(Request{Op: "presence", Session: "sess-f", Gone: true})
	if !resp.OK {
		t.Fatalf("presence op refused: %s", resp.Error)
	}
	if online, wanted := h.d.presence.live(); online != 0 || wanted != 0 {
		t.Fatalf("gone must retire the buddy: online=%d wanted=%d", online, wanted)
	}
	c := h.script.conn("bastle-s-66666666")
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if !closed {
		t.Fatal("gone must close the connection")
	}
}

// Session connections see the same room traffic the concierge does. If they
// journaled it, every message would be stored once per live session and the
// journal's "what the server saw" contract — one connection's view — would be
// meaningless.
func TestPresenceConnectionsNeverJournal(t *testing.T) {
	h := startPresence(t)
	h.d.presence.note("sess-g", "bastle/s-77777777", nil)
	waitFor(t, "buddy online", func() bool {
		return recordedHas(h.script.conn("bastle-s-77777777"), "join bastle")
	})
	c := h.script.conn("bastle-s-77777777")
	c.push(t, tocwire.ChatIn{RoomID: "7", From: "operator", Text: "seen by every session"})
	c.push(t, tocwire.IMIn{From: "operator", Text: "a dm to somebody"})

	// The concierge's own room join is the observable that proves the daemon
	// processed events at all while the presence rows stayed out.
	waitJoined(t, h.conn, "bastle")
	time.Sleep(50 * time.Millisecond)
	for _, room := range []string{"bastle", "@dm"} {
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
		h.d.presence.note(fmt.Sprintf("sess-%02d", i), fmt.Sprintf("bastle/s-%08d", i), nil)
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

func TestNickForDerivesALegalName(t *testing.T) {
	cases := []struct{ label, want string }{
		{"bastle/s-e284b102", "bastle-s-e284b102"},
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
