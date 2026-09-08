package buddylist

// Retention used to live in cmd/buddylist: a closure over the --keep flag, a
// launch call, and a fourteen-line hourly goroutine, all in main(). Nothing
// there could be tested — the period was a literal time.Hour and the only way
// to observe a second pass was to wait an hour — so the behaviour that keeps
// journal.db from growing without bound was covered by nothing at all.
//
// These are the two facts the move has to preserve, and one it introduces:
//
//   - the launch pass runs, and runs BEFORE the socket accepts, so no caller
//     is mid-read of rows about to be deleted;
//   - the loop reapplies it, which is the whole reason it is a loop;
//   - Keep == 0 means retention is OFF, not "trim everything". Trim(0) puts the
//     cutoff at now, so the wrong reading of a zero value silently empties the
//     journal — and every hermetic fixture in this package constructs a Daemon
//     without a Keep.
//
// Ordering is observed, never slept on: serveSocket is started immediately
// after the launch trim, so a socket request that gets an answer proves the
// trim has already returned. That is what makes the Keep==0 case (a negative:
// "nothing was deleted") mean something, and its positive control is the row
// beside it in the same table, on the same fixture, which does delete.
//
// The corollary, and the reason startRetention ages the rows itself: Run's
// FIRST act is the launch trim, so a caller that advances the clock after
// `go d.Run(ctx)` is racing it. Nothing orders the advance ahead of the trim;
// if the daemon goroutine wins, the cutoff is computed at t0, both rows
// survive, and the positive-control row fails with the same message that
// deleting the launch trim produces — so the mutation could no longer tell
// "the trim was removed" from "the trim ran too early". (Measured: it does not
// flake on its own, because Go's runnext slot keeps the spawning goroutine
// ahead; a 100ms sleep after the `go` statement fails it every run. A
// scheduler implementation detail is not an ordering guarantee.) Aging before
// the daemon exists needs no ordering at all.

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// testClock is a hand-advanced clock. Retention is a question about time, and
// the only honest way to ask it in milliseconds is to move time rather than
// wait for it.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// retentionFixture starts a daemon whose only job is retention: it never
// connects, because Dial blocks until shutdown. A Dial that returned an error
// instead would spin the reconnect loop and journal a "connect failed" note
// per attempt, which is noise in the very table these tests count rows in.
type retentionFixture struct {
	j     *Journal
	clock *testClock
	sock  string
}

// age is how far the clock moves AFTER the rows are written and BEFORE the
// daemon is constructed: it is the rows' apparent age at the launch trim, set
// where no goroutine can race it.
func startRetention(t *testing.T, keep, every, age time.Duration) *retentionFixture {
	t.Helper()
	clock := &testClock{t: time.Unix(1755216000, 0)}
	j, err := OpenJournal(filepath.Join(t.TempDir(), "journal.db"), clock.now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	// Unix socket paths cap near 104 bytes and t.TempDir embeds the test name,
	// which overflows it; the socket gets its own short dir.
	sockDir, err := os.MkdirTemp("", "rt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })

	f := &retentionFixture{j: j, clock: clock, sock: filepath.Join(sockDir, "d.sock")}
	for _, body := range []string{"first", "second"} {
		if _, err := j.Append("lobby", "peer", "chat", body); err != nil {
			t.Fatal(err)
		}
	}
	f.clock.advance(age)
	d, err := New(Config{
		Rooms:      []string{"lobby"},
		SocketPath: f.sock,
		Journal:    j,
		Log:        slog.Default(),
		Keep:       keep,
		TrimEvery:  every,
		Now:        clock.now,
		Dial: func(ctx context.Context) (Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
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
	return f
}

// ready blocks until the socket answers, which is the observable proof that
// the launch trim has already returned (Run calls it before starting
// serveSocket).
func (f *retentionFixture) ready(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(settleBudget)
	for {
		if _, err := Call(f.sock, Request{Op: "health"}, time.Second); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("socket never answered; cannot tell a skipped trim from a slow one")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (f *retentionFixture) rows(t *testing.T) int {
	t.Helper()
	msgs, _, err := f.j.ReadAfter("lobby", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	return len(msgs)
}

func TestDaemonTrimsAtLaunchAndLeavesAZeroKeepAlone(t *testing.T) {
	cases := []struct {
		name string
		keep time.Duration
		want int
	}{
		// The positive control. Without it, "Keep=0 deleted nothing" and "the
		// launch trim never ran at all" are the same observation.
		{"a configured window reaps what is past it", 24 * time.Hour, 0},
		{"zero keep is retention OFF, not trim-everything", 0, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Both rows are already 48h old when the daemon is built, so the
			// launch pass is the only pass that can act on them: the 1h loop
			// period cannot fire inside this test and rescue a launch trim
			// that never ran.
			f := startRetention(t, tc.keep, time.Hour, 48*time.Hour)
			f.ready(t)
			if got := f.rows(t); got != tc.want {
				t.Fatalf("after the launch pass: %d rows, want %d", got, tc.want)
			}
		})
	}
}

func TestDaemonReappliesRetentionOnItsOwnSchedule(t *testing.T) {
	// age 0: nothing is trimmable at launch, so a pass that only ran once
	// cannot produce the end state this asserts. Here the advance IS after
	// Run starts, deliberately — the question is whether a LATER pass sees
	// it — and it is polled for rather than assumed.
	f := startRetention(t, 24*time.Hour, 5*time.Millisecond, 0)
	f.ready(t)
	if got := f.rows(t); got != 2 {
		t.Fatalf("launch pass must not reap rows inside the window: %d rows, want 2", got)
	}
	f.clock.advance(48 * time.Hour)
	deadline := time.Now().Add(settleBudget)
	for {
		if f.rows(t) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the retention loop never reapplied the window after time moved past it")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
