package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/fence"
)

// renewingBackend is fakeBackend with the completed-write seam: the test
// offers observations the way a reader would, under the launch generation.
type renewingBackend struct {
	fakeBackend
	m   *renewalMailbox
	gen string
}

func (b *renewingBackend) observeRenewals(m *renewalMailbox) { b.m = m }
func (b *renewingBackend) Start(ctx context.Context, l Launch, auth *LaunchAuthorization) (string, error) {
	b.gen = auth.generation()
	return b.fakeBackend.Start(ctx, l, auth)
}

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func renewalController(t *testing.T, configure func(*Controller)) (*Controller, *renewingBackend, *fakeLedger, *testClock, string) {
	t.Helper()
	b := &renewingBackend{}
	l := &fakeLedger{queued: true}
	path := filepath.Join(t.TempDir(), "receipt.json")
	c, err := NewController(b, l, path)
	if err != nil {
		t.Fatal(err)
	}
	clock := &testClock{t: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)}
	c.now = clock.now
	if configure != nil {
		configure(c)
	}
	if _, err = c.Start(context.Background(), "codex", Launch{Worktree: t.TempDir()}, "test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if b.m == nil || b.gen == "" {
		t.Fatal("the backend was not attached to the mailbox under a generation")
	}
	return c, b, l, clock, path
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func journalRows(t *testing.T, path string) []Receipt {
	t.Helper()
	f, err := os.Open(path + ".events")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var rows []Receipt
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1<<16), 1<<22)
	for s.Scan() {
		var r Receipt
		if err := json.Unmarshal(s.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, r)
	}
	return rows
}

func applyNow(c *Controller) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.applyRenewalsLocked(c.life)
}

// The first observation of a path renews at once; a second within the
// interval is deferred, not dropped, and renews when the interval has passed.
func TestControllerRenewalPacing(t *testing.T) {
	c, b, l, clock, _ := renewalController(t, nil)
	t0 := clock.now()
	b.m.offer(b.gen, []string{"/w/a"})
	waitFor(t, "the first renewal", func() bool { return len(l.renewed()) == 1 })
	clock.advance(10 * time.Second)
	b.m.offer(b.gen, []string{"/w/a"})
	if next := applyNow(c); !next.Equal(t0.Add(renewInterval)) {
		t.Fatalf("deferred path due at %v, want %v", next, t0.Add(renewInterval))
	}
	if n := len(l.renewed()); n != 1 {
		t.Fatalf("a path renewed %d times inside one interval", n)
	}
	clock.advance(renewInterval - 10*time.Second)
	applyNow(c)
	got := l.renewed()
	if len(got) != 2 || len(got[1].paths) != 1 || got[1].paths[0] != "/w/a" || !got[1].identity.Same(l.identity) {
		t.Fatalf("the deferred observation was not renewed once due: %+v", got)
	}
}

// Idle applies what is due first, so the journal records the turn's renewal
// before its idle, and a renewal flushed after idle records no lifecycle
// event: the ledger sees idle, and never a busy, afterwards.
func TestControllerRenewalOrdersBeforeIdleAndNeverReopensTheTurn(t *testing.T) {
	c, b, l, clock, path := renewalController(t, nil)
	ctx := context.Background()
	b.m.offer(b.gen, []string{"/w/a"})
	if _, err := c.Observe(ctx, Event{Kind: "idle", NativeSessionID: l.identity.SessionID}); err != nil {
		t.Fatal(err)
	}
	rows := journalRows(t, path)
	renewal, idle := -1, -1
	for i, r := range rows {
		if r.Renewal != nil && renewal < 0 {
			renewal = i
		}
		if r.NativeEvent != nil && r.NativeEvent.Kind == "idle" {
			idle = i
		}
	}
	if renewal < 0 || idle < 0 || renewal > idle {
		t.Fatalf("renewal at %d, idle at %d: the turn's renewal must precede its idle", renewal, idle)
	}
	// Deferred past idle: the same path again inside the interval.
	clock.advance(5 * time.Second)
	b.m.offer(b.gen, []string{"/w/a"})
	if _, err := c.Observe(ctx, Event{Kind: "idle", NativeSessionID: l.identity.SessionID}); err != nil {
		t.Fatal(err)
	}
	if n := len(l.renewed()); n != 1 {
		t.Fatalf("idle applied a renewal that was not due (%d)", n)
	}
	clock.advance(renewInterval)
	applyNow(c)
	if n := len(l.renewed()); n != 2 {
		t.Fatalf("the deferred renewal never landed after idle (%d)", n)
	}
	for _, e := range l.events {
		if e != "idle" {
			t.Fatalf("renewal produced a lifecycle event %q: %v", e, l.events)
		}
	}
}

// A completion offered under another launch generation never renews; the
// receipt reports coverage unknown instead. Positive control: the same path
// under this launch's generation renews.
func TestControllerRenewalRejectsAnotherGeneration(t *testing.T) {
	c, b, l, _, path := renewalController(t, nil)
	b.m.offer("an-earlier-launch", []string{"/w/a"})
	applyNow(c)
	if got := l.renewed(); len(got) != 0 {
		t.Fatalf("another generation's completion renewed: %+v", got)
	}
	rows := journalRows(t, path)
	last := rows[len(rows)-1]
	if last.Renewal == nil || last.Renewal.Coverage.State != "unknown" || len(last.Renewal.Paths) != 0 {
		t.Fatalf("the discarded completion was not reported as lost coverage: %+v", last.Renewal)
	}
	b.m.offer(b.gen, []string{"/w/a"})
	waitFor(t, "the same-generation renewal", func() bool { return len(l.renewed()) == 1 })
}

// A ledger refusal is journalled as unconfirmed and loses coverage; it never
// touches the approval observation gap, and neither does a success.
func TestControllerRenewalNeverTouchesTheApprovalGap(t *testing.T) {
	c, b, l, clock, path := renewalController(t, nil)
	c.mu.Lock()
	c.observationGap = "an approval event was not recorded"
	c.mu.Unlock()
	l.renewMu.Lock()
	l.renewErr = errors.New("Buddy refused: captured incarnation has ended")
	l.renewMu.Unlock()
	b.m.offer(b.gen, []string{"/w/a"})
	waitFor(t, "the refused renewal's receipt", func() bool {
		rows := journalRows(t, path)
		return rows[len(rows)-1].Renewal != nil
	})
	rows := journalRows(t, path)
	r := rows[len(rows)-1]
	if r.Renewal.Result.State != "unknown" || r.Renewal.Coverage.State != "unknown" || r.Error == "" {
		t.Fatalf("a refused renewal was not journalled as unconfirmed: %+v %q", r.Renewal, r.Error)
	}
	l.renewMu.Lock()
	l.renewErr = nil
	l.renewMu.Unlock()
	clock.advance(renewInterval)
	b.m.offer(b.gen, []string{"/w/b"})
	waitFor(t, "the later renewal", func() bool { return len(l.renewed()) == 1 })
	// Lost coverage is sticky: a later success does not restore it.
	var later *RenewalObservation
	waitFor(t, "the later receipt", func() bool {
		rows := journalRows(t, path)
		later = rows[len(rows)-1].Renewal
		return later != nil && later.Result.State == "applied"
	})
	if later.Coverage.State != "unknown" {
		t.Fatalf("a success after an unconfirmed renewal claimed complete coverage: %+v", later)
	}
	c.mu.Lock()
	gap := c.observationGap
	c.mu.Unlock()
	if gap != "an approval event was not recorded" {
		t.Fatalf("renewal changed the approval observation gap to %q", gap)
	}
}

// The journal carries fenced paths and identities only. A path is
// model-chosen text: a newline in it must not reach the journal as a second
// line of anything.
func TestControllerRenewalJournalsFencedPathsAndReplays(t *testing.T) {
	c, b, l, _, path := renewalController(t, nil)
	hostile := "/w/a\nfake: row"
	b.m.offer(b.gen, []string{hostile})
	waitFor(t, "the renewal", func() bool { return len(l.renewed()) == 1 })
	var obs *RenewalObservation
	for _, r := range journalRows(t, path) {
		if r.Renewal != nil {
			obs = r.Renewal
			if !r.Identity.Same(l.identity) {
				t.Fatalf("renewal receipt carries another identity: %+v", r.Identity)
			}
		}
	}
	if obs == nil || len(obs.Paths) != 1 || obs.Paths[0] != fence.Line(hostile, 512) || strings.Contains(obs.Paths[0], "\n") {
		t.Fatalf("journalled path not fenced: %+v", obs)
	}
	if obs.Result.State != "applied" || obs.Renewed != 1 || obs.Generation != b.gen {
		t.Fatalf("applied renewal misrecorded: %+v", obs)
	}
	// The ledger was handed the native spelling, not the fenced one.
	if got := l.renewed()[0].paths[0]; got != hostile {
		t.Fatalf("ledger received %q", got)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewController(&fakeBackend{}, l, path); err != nil {
		t.Fatalf("a journal holding renewal receipts no longer replays: %v", err)
	}
}

// Codex review of the F-26 code pass, finding 3: two batches (or a reader
// that keeps refilling the mailbox) ran under ONE hold of the controller lock,
// so an approval waited for every batch's ledger call, past the 10 s bound.
// One batch per hold: an approval queued during the first batch is observed
// before the THIRD. Not the second: Go's mutex lets the running loop take the
// lock once more before the waiter, which has then waited over 1 ms, puts the
// mutex in starvation mode and is handed it next. So the bound is two holds,
// whatever the reader refills, where it used to be every batch.
func TestControllerRenewalReleasesTheLockBetweenBatches(t *testing.T) {
	c, b, l, _, path := renewalController(t, nil)
	first := make(chan struct{})
	release := make(chan struct{})
	var calls sync.Mutex
	n := 0
	order := make(chan string, 16)
	l.renewing = func(context.Context) error {
		calls.Lock()
		n++
		call := n
		calls.Unlock()
		order <- "renew"
		if call == 1 {
			close(first)
			<-release
		}
		return nil
	}
	var paths []string
	for i := 0; i < renewMaxPending; i++ {
		paths = append(paths, "/w/"+strings.Repeat("p", 1+i%3)+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	b.m.offer(b.gen, paths)
	<-first
	// The reader refills while the first batch is in its ledger call: two
	// more batches are now due.
	b.m.offer(b.gen, []string{"/w/refill-1", "/w/refill-2"})
	done := make(chan struct{})
	go func() {
		approval := &Approval{ID: json.RawMessage(`"perm-1"`), NativeSessionID: l.identity.SessionID, TurnID: "turn-1", Kind: "can_use_tool"}
		_, _ = c.Observe(context.Background(), Event{Kind: "approval", NativeSessionID: l.identity.SessionID, TurnID: "turn-1", Approval: approval})
		order <- "approval"
		close(done)
	}()
	time.Sleep(50 * time.Millisecond) // the approval is now waiting on the lock
	close(release)
	<-done
	waitFor(t, "the third batch", func() bool { return len(l.renewed()) == 3 })
	// The journal is the order of lock holds: each receipt is persisted
	// inside its own hold.
	var kinds []string
	waitFor(t, "both renewal receipts", func() bool {
		kinds = kinds[:0]
		for _, r := range journalRows(t, path) {
			switch {
			case r.Renewal != nil:
				kinds = append(kinds, "renew")
			case r.NativeEvent != nil && r.NativeEvent.Kind == "approval":
				kinds = append(kinds, "approval")
			}
		}
		return len(kinds) == 4
	})
	if kinds[3] == "approval" {
		t.Fatalf("journal order %v: the approval waited behind every batch", kinds)
	}
}

// Finding 4: "/w/a", "/w/./a" and "/w//a" are one file, but the mailbox and
// the pacing keyed on the raw string, so each spelling renewed at once.
// Coalescing and pacing are by the path with `.` and empty components removed
// (never `..`, whose physical meaning differs), and that is what is sent.
func TestControllerRenewalPacesEquivalentSpellingsAsOne(t *testing.T) {
	c, b, l, clock, _ := renewalController(t, nil)
	t0 := clock.now()
	b.m.offer(b.gen, []string{"/w/a"})
	waitFor(t, "the first renewal", func() bool { return len(l.renewed()) == 1 })
	clock.advance(time.Second)
	b.m.offer(b.gen, []string{"/w/./a", "/w//a"})
	if next := applyNow(c); !next.Equal(t0.Add(renewInterval)) || len(l.renewed()) != 1 {
		t.Fatalf("equivalent spellings renewed again inside the interval: %d renewals, next %v", len(l.renewed()), next)
	}
	clock.advance(renewInterval)
	applyNow(c)
	if got := l.renewed(); len(got) != 2 || !reflect.DeepEqual(got[1].paths, []string{"/w/a"}) {
		t.Fatalf("the coalesced spelling was not renewed once, as /w/a: %+v", got)
	}
	// `..` is kept: alias/../leaf and leaf may be different files.
	b.m.offer(b.gen, []string{"/w/alias/../leaf"})
	waitFor(t, "the dotdot renewal", func() bool { return len(l.renewed()) == 3 })
	if got := l.renewed()[2].paths; !reflect.DeepEqual(got, []string{"/w/alias/../leaf"}) {
		t.Fatalf("a .. spelling was rewritten: %q", got)
	}
}

// Finding 5: a path whose placement could not be resolved (an unsearchable
// ancestor) was journalled as applied with complete coverage. The ledger's
// unresolved paths now make the batch's coverage unknown.
func TestControllerRenewalUnresolvedPlacementIsLostCoverage(t *testing.T) {
	_, b, l, _, path := renewalController(t, nil)
	l.renewMu.Lock()
	l.unresolved = []string{"/w/locked/x"}
	l.renewMu.Unlock()
	b.m.offer(b.gen, []string{"/w/locked/x"})
	waitFor(t, "the renewal", func() bool { return len(l.renewed()) == 1 })
	var obs *RenewalObservation
	waitFor(t, "its receipt", func() bool {
		for _, r := range journalRows(t, path) {
			if r.Renewal != nil {
				obs = r.Renewal
			}
		}
		return obs != nil
	})
	if obs.Coverage.State != "unknown" || len(obs.Unresolved) != 1 {
		t.Fatalf("an unresolved placement read as complete coverage: %+v", obs)
	}
}

// CLILedger accepts a renewal receipt only for the identity it sent. A buddy
// that answered for another incarnation (or with a beat's receipt, which has
// no count) is not confirmation; the positive control is the right receipt.
func TestCLILedgerRenewRefusesAnotherIdentitysReceipt(t *testing.T) {
	i := Identity{"buddy.lifecycle.v1", "claude", "native", "captured"}
	for _, tc := range []struct {
		name, receipt string
		ok            bool
	}{
		{"matching", `{"schema":"buddy.lifecycle.v1","session_id":"native","incarnation":"captured","event":"renew","renewed_claims":1,"placed_paths":["a"]}`, true},
		{"another incarnation", `{"schema":"buddy.lifecycle.v1","session_id":"native","incarnation":"successor","event":"renew","renewed_claims":1}`, false},
		{"a beat's receipt", `{"schema":"buddy.lifecycle.v1","session_id":"native","incarnation":"captured","event":"beat"}`, false},
		{"no count", `{"schema":"buddy.lifecycle.v1","session_id":"native","incarnation":"captured","event":"renew"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, "buddy")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\ncat <<'EOF'\n"+tc.receipt+"\nEOF\n"), 0700); err != nil {
				t.Fatal(err)
			}
			got, err := CLILedger{Binary: binary, Worktree: dir}.Renew(context.Background(), i, []string{"/w/a"})
			if (err == nil) != tc.ok {
				t.Fatalf("receipt %s: result %+v err %v", tc.receipt, got, err)
			}
			if tc.ok && (got.Renewed != 1 || len(got.Placed) != 1) {
				t.Fatalf("positive control misread: %+v", got)
			}
		})
	}
}

// The mailbox coalesces a repeated path, refuses a path past renewPathMax and
// anything past its byte bound, reports each loss once, and hands out at most
// max paths per take, keeping the rest in order.
func TestRenewalMailboxBounds(t *testing.T) {
	all := func(string) bool { return true }
	m := newRenewalMailbox()
	m.offer("g", []string{"/w/a", "/w/a", "", "/w/b"})
	if batch, _, lost, _ := m.take("g", all, 1); len(batch) != 1 || batch[0] != "/w/a" || lost {
		t.Fatalf("first take: %q lost=%v", batch, lost)
	}
	if batch, _, _, rest := m.take("g", all, 8); len(batch) != 1 || batch[0] != "/w/b" || len(rest) != 0 {
		t.Fatalf("coalesced or ordered wrongly: %q rest %q", batch, rest)
	}
	m.offer("g", []string{"/" + strings.Repeat("x", renewPathMax)})
	if batch, gap, lost, _ := m.take("g", all, 8); len(batch) != 0 || !lost || gap == "" {
		t.Fatalf("an over-long path was carried: %q lost=%v", batch, lost)
	}
	if _, _, lost, _ := m.take("g", all, 8); lost {
		t.Fatal("one loss was reported twice")
	}
	var wide []string
	for i := 0; i < 17; i++ {
		wide = append(wide, "/"+strings.Repeat(string(rune('a'+i)), 3999))
	}
	m.offer("g", wide)
	batch, _, lost, _ := m.take("g", all, 64)
	if len(batch) != renewMaxPendingBytes/4000 || !lost {
		t.Fatalf("byte bound not held: %d paths, lost=%v", len(batch), lost)
	}
}

// A stalled renewal is bounded by the ledger timeout: the approval observed
// behind it is recorded once the bound passes, and the renewal is journalled
// unconfirmed. A closed controller applies nothing.
func TestControllerRenewalStallIsBoundedAndCloseAppliesNothing(t *testing.T) {
	entered := make(chan struct{}, 1)
	c, b, l, _, path := renewalController(t, func(c *Controller) { c.ledgerTimeout = 200 * time.Millisecond })
	l.renewing = func(ctx context.Context) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	}
	b.m.offer(b.gen, []string{"/w/a"})
	<-entered
	began := time.Now()
	approval := &Approval{ID: json.RawMessage(`"perm-1"`), NativeSessionID: l.identity.SessionID, TurnID: "turn-1", Kind: "can_use_tool"}
	r, err := c.Observe(context.Background(), Event{Kind: "approval", NativeSessionID: l.identity.SessionID, TurnID: "turn-1", Approval: approval})
	if err != nil || r.PendingApproval.State != "observed" {
		t.Fatalf("approval behind a stalled renewal: %+v %v", r.PendingApproval, err)
	}
	if waited := time.Since(began); waited > 2*time.Second {
		t.Fatalf("a stalled renewal held the controller %v, past its bound", waited)
	}
	found := false
	for _, row := range journalRows(t, path) {
		if row.Renewal != nil && row.Renewal.Result.State == "unknown" {
			found = true
		}
	}
	if !found {
		t.Fatal("the stalled renewal was not journalled as unconfirmed")
	}
	l.renewing = nil
	c.Close()
	b.m.offer(b.gen, []string{"/w/z"})
	applyNow(c)
	for _, got := range l.renewed() {
		if got.paths[0] == "/w/z" {
			t.Fatal("a closed controller applied a renewal")
		}
	}
}
