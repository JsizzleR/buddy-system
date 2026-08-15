package buddylist

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/tocwire"
)

// fakeConn scripts a server connection. Sends are recorded; events are pushed
// by the test. Closing the events channel simulates connection death.
type fakeConn struct {
	mu     sync.Mutex
	events chan tocwire.Event
	sends  []string
	err    error
	closed bool
}

func newFakeConn() *fakeConn { return &fakeConn{events: make(chan tocwire.Event, 16)} }

func (f *fakeConn) Events() <-chan tocwire.Event { return f.events }
func (f *fakeConn) Err() error                   { f.mu.Lock(); defer f.mu.Unlock(); return f.err }
func (f *fakeConn) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends = append(f.sends, s)
}
func (f *fakeConn) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sends...)
}
func (f *fakeConn) ChatJoin(room string) error {
	f.record("join " + room)
	f.events <- tocwire.ChatJoin{RoomID: "7", Room: room}
	return nil
}
func (f *fakeConn) ChatSend(roomID, text string) error {
	f.record("send " + roomID + " " + text)
	return nil
}
func (f *fakeConn) IM(to, text string) error { f.record("im " + to + " " + text); return nil }
func (f *fakeConn) SetAway(text string) error {
	f.record("away " + text)
	return nil
}
func (f *fakeConn) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.events)
	}
	return nil
}
func (f *fakeConn) die(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
	f.Close()
}

type harness struct {
	d      *Daemon
	j      *Journal
	sock   string
	conns  chan *fakeConn // Run consumes one per (re)connect
	cancel context.CancelFunc
	done   chan struct{}
}

func start(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	j, err := OpenJournal(filepath.Join(dir, "journal.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	// Unix socket paths cap near 104 bytes; t.TempDir embeds the test name
	// and overflows it, so the socket gets its own short-lived short dir.
	sockDir, err := os.MkdirTemp("", "fd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	h := &harness{j: j, sock: filepath.Join(sockDir, "buddylistd.sock"), conns: make(chan *fakeConn, 4), done: make(chan struct{})}
	d, err := New(Config{
		Rooms:      []string{"lobby"},
		SocketPath: h.sock,
		Journal:    j,
		Log:        slog.Default(),
		MaxBackoff: 20 * time.Millisecond,
		Dial: func(ctx context.Context) (Conn, error) {
			select {
			case c := <-h.conns:
				return c, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.d = d
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { d.Run(ctx); close(h.done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-h.done:
		case <-time.After(2 * time.Second):
			t.Error("daemon did not shut down")
		}
	})
	return h
}

// call round-trips one socket request, retrying briefly while the socket
// file appears (observable readiness, not a tuned sleep).
func (h *harness) call(t *testing.T, req Request) (Response, error) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err := Call(h.sock, req, time.Second)
		if err != nil && strings.Contains(err.Error(), "not reachable") && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		return resp, err
	}
}

// waitJournal polls the journal until pred matches or times out.
func (h *harness) waitJournal(t *testing.T, room string, pred func([]Msg) bool) []Msg {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		msgs, _, err := h.j.ReadAfter(room, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		if pred(msgs) {
			return msgs
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("journal never matched")
	return nil
}

// waitJoined blocks until the daemon has joined via this connection —
// mirroring the real server, which never delivers CHAT_IN before CHAT_JOIN.
func waitJoined(t *testing.T, c *fakeConn, room string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range c.recorded() {
			if s == "join "+room {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("daemon never joined %s: %v", room, c.recorded())
}

func TestRelayBothWaysAndJournal(t *testing.T) {
	h := start(t)
	c := newFakeConn()
	h.conns <- c

	// Wait until joined (via who over the REAL socket).
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err := h.call(t, Request{Op: "who"})
		if err == nil && resp.Connected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never connected")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Inbound: a peer talks in the room → journaled under the room name.
	c.events <- tocwire.ChatIn{RoomID: "7", From: "jay", Text: "hello fleet"}
	msgs := h.waitJournal(t, "lobby", func(m []Msg) bool { return len(m) >= 1 })
	if msgs[0].Sender != "jay" || msgs[0].Body != "hello fleet" || msgs[0].Kind != "chat" {
		t.Fatalf("bad journal row: %+v", msgs[0])
	}

	// Outbound: say → prefixed relay through the connection.
	if _, err := h.call(t, Request{Op: "say", Room: "lobby", From: "alpha", Text: "claiming router"}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range c.recorded() {
		if s == "send 7 [alpha] claiming router" {
			found = true
		}
	}
	if !found {
		t.Fatalf("say did not relay with the [alpha] prefix: %v", c.recorded())
	}

	// DM out.
	if _, err := h.call(t, Request{Op: "dm", To: "jay", From: "alpha", Text: "psst"}); err != nil {
		t.Fatal(err)
	}

	// Inbound IM → journaled under @dm.
	c.events <- tocwire.IMIn{From: "nightly", Text: "state=RED"}
	h.waitJournal(t, "@dm", func(m []Msg) bool { return len(m) == 1 && m[0].Sender == "nightly" })
}

func TestSendFailsVisiblyWhileDisconnected(t *testing.T) {
	h := start(t)
	// No connection delivered yet: say must error, not silently succeed.
	if _, err := h.call(t, Request{Op: "say", Room: "lobby", Text: "x"}); err == nil {
		t.Fatal("say while disconnected must fail visibly")
	} else if !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("error should say why: %v", err)
	}
}

func TestReconnectRejoinsAndJournalSurvives(t *testing.T) {
	h := start(t)
	c1 := newFakeConn()
	h.conns <- c1
	waitJoined(t, c1, "lobby")
	c1.events <- tocwire.ChatIn{RoomID: "7", From: "jay", Text: "before the drop"}
	h.waitJournal(t, "lobby", func(m []Msg) bool { return len(m) >= 1 })

	c1.die(errors.New("server went away"))

	c2 := newFakeConn()
	h.conns <- c2
	// The daemon reconnects and rejoins the room on the NEW connection.
	deadline := time.Now().Add(2 * time.Second)
	for {
		joined := false
		for _, s := range c2.recorded() {
			if s == "join lobby" {
				joined = true
			}
		}
		if joined {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no rejoin after reconnect: %v", c2.recorded())
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Late-join catch-up: history from before the drop is still readable.
	msgs, gap, err := h.j.ReadAfter("lobby", 0, 100)
	if err != nil || gap {
		t.Fatalf("history read: err=%v gap=%v", err, gap)
	}
	if len(msgs) == 0 || msgs[0].Body != "before the drop" {
		t.Fatalf("journal must survive the connection: %+v", msgs)
	}
	// And the disconnect itself is on the record.
	sys, _, _ := h.j.ReadAfter("", 0, 100)
	foundDrop := false
	for _, m := range sys {
		if m.Kind == "system" && strings.Contains(m.Body, "server went away") {
			foundDrop = true
		}
	}
	if !foundDrop {
		t.Fatalf("disconnect must be journaled: %+v", sys)
	}
}

func TestJournalCursorAndGap(t *testing.T) {
	dir := t.TempDir()
	clock := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	j, err := OpenJournal(filepath.Join(dir, "j.db"), func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	var seqs []int64
	for _, body := range []string{"one", "two", "three"} {
		s, err := j.Append("r", "u", "chat", body)
		if err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, s)
	}
	// Cursor semantics: after=seqs[0] returns two,three; no gap.
	msgs, gap, _ := j.ReadAfter("r", seqs[0], 10)
	if gap || len(msgs) != 2 || msgs[0].Body != "two" {
		t.Fatalf("cursor read wrong: gap=%v msgs=%+v", gap, msgs)
	}
	// Fresh cursor 0: everything, no gap.
	if msgs, gap, _ = j.ReadAfter("r", 0, 10); gap || len(msgs) != 3 {
		t.Fatalf("fresh read wrong: gap=%v n=%d", gap, len(msgs))
	}

	// Retention trims one and three... (age the first two rows)
	clock = clock.Add(48 * time.Hour)
	if _, err := j.Append("r", "u", "chat", "four"); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Trim(24 * time.Hour); err != nil {
		t.Fatal(err)
	}
	// A cursor from before the horizon reports the GAP explicitly.
	msgs, gap, _ = j.ReadAfter("r", seqs[0], 10)
	if !gap {
		t.Fatal("a cursor older than retention must report gap=true, never silence")
	}
	if len(msgs) != 1 || msgs[0].Body != "four" {
		t.Fatalf("post-trim read wrong: %+v", msgs)
	}
	// A cursor pointing at the newest retained row: no gap, no msgs.
	msgs, gap, _ = j.ReadAfter("r", msgs[0].Seq, 10)
	if gap || len(msgs) != 0 {
		t.Fatalf("up-to-date cursor must be quiet: gap=%v msgs=%+v", gap, msgs)
	}
}

func TestOversizeBodyTruncatedInJournal(t *testing.T) {
	h := start(t)
	c := newFakeConn()
	h.conns <- c
	waitJoined(t, c, "lobby")
	huge := strings.Repeat("x", maxBody+1000)
	c.events <- tocwire.ChatIn{RoomID: "7", From: "jay", Text: huge}
	msgs := h.waitJournal(t, "lobby", func(m []Msg) bool { return len(m) >= 1 })
	if len(msgs[0].Body) > maxBody+32 || !strings.HasSuffix(msgs[0].Body, "…[truncated]") {
		t.Fatalf("hostile-size body must be truncated, got len=%d", len(msgs[0].Body))
	}
}
