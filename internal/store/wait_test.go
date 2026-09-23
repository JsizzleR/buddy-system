package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// D-033: the wait register. Every refusal here has a positive control beside
// it — the same call shape accepted — because "it refused" and "it never
// ran" are otherwise the same observation.

// waitFixture: a waiter (alpha), a holder (bravo) with two open claims, and a
// bystander (charlie).
func waitFixture(t *testing.T) (*Store, *pinnedClock, SessionInfo, SessionInfo, SessionInfo) {
	t.Helper()
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	c := hello(t, st, "sess-c", "charlie", "/wt/c")
	for _, cl := range []struct{ slug, scope string }{{"api-work", "internal/api"}, {"docs-pass", "docs"}} {
		if err := st.Claim(b.SessionID, b.Incarnation, cl.slug, "x", []string{cl.scope}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Claim(a.SessionID, a.Incarnation, "mine", "x", []string{"cmd"}); err != nil {
		t.Fatal(err)
	}
	return st, clk, a, b, c
}

func mustWaitOf(t *testing.T, st *Store, sessionID string) (Wait, bool) {
	t.Helper()
	w, ok, err := st.WaitOf(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return w, ok
}

func TestDeclareWaitRefusesBeforeAnyWrite(t *testing.T) {
	cases := []struct {
		name    string
		slugs   []string
		until   time.Duration
		want    string // a phrase the refusal must carry
		control []string
		ctlFor  time.Duration
	}{
		{"unknown slug", []string{"no-such"}, time.Hour, "no claim by that slug", []string{"api-work"}, time.Hour},
		{"released slug", []string{"gone"}, time.Hour, "already released", []string{"api-work"}, time.Hour},
		{"own claim", []string{"mine"}, time.Hour, "your own claim", []string{"api-work"}, time.Hour},
		{"one bad target among good ones", []string{"api-work", "no-such"}, time.Hour, "no claim by that slug", []string{"api-work", "docs-pass"}, time.Hour},
		{"over the ceiling", []string{"api-work"}, 20 * time.Hour, "over the 12h0m0s ceiling", []string{"api-work"}, WaitCeiling},
		{"under the floor", []string{"api-work"}, 30 * time.Second, "under the 1m0s floor", []string{"api-work"}, WaitFloor},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, _, a, b, _ := waitFixture(t)
			if err := st.Claim(b.SessionID, b.Incarnation, "gone", "x", []string{"gone"}); err != nil {
				t.Fatal(err)
			}
			if err := st.Release(b.SessionID, b.Incarnation, "gone"); err != nil {
				t.Fatal(err)
			}
			// A wait that already exists must survive the refused
			// re-declaration untouched: row, deadline, note and targets.
			before, _, err := st.DeclareWait(a.SessionID, a.Incarnation, []string{"docs-pass"}, 2*time.Hour, "keep me")
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = st.DeclareWait(a.SessionID, a.Incarnation, tc.slugs, tc.until, "new note")
			var refused ErrWaitRefused
			if !errors.As(err, &refused) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want a refusal carrying %q, got %v", tc.want, err)
			}
			after, _ := mustWaitOf(t, st, a.SessionID)
			if after.Decl != before.Decl || after.Note != "keep me" || !after.Deadline.Equal(before.Deadline) ||
				len(after.Targets) != 1 || after.Targets[0].Slug != "docs-pass" {
				t.Fatalf("a refused declaration changed the existing wait: %+v", after)
			}
			// POSITIVE CONTROL: the same call with a good target / in bounds.
			w, replaced, err := st.DeclareWait(a.SessionID, a.Incarnation, tc.control, tc.ctlFor, "new note")
			if err != nil {
				t.Fatalf("control: %v", err)
			}
			if replaced == nil || replaced.Decl != before.Decl {
				t.Fatalf("the accepted re-declaration must report the wait it replaced, got %+v", replaced)
			}
			if w.Note != "new note" || len(w.Targets) != len(tc.control) {
				t.Fatalf("control wait not recorded as declared: %+v", w)
			}
		})
	}
}

func TestDeclareWaitRefusesAndWritesNothingWithNoPriorWait(t *testing.T) {
	st, _, a, _, _ := waitFixture(t)
	if _, _, err := st.DeclareWait(a.SessionID, a.Incarnation, []string{"no-such"}, time.Hour, ""); err == nil {
		t.Fatal("an unknown slug must be refused")
	}
	if _, ok := mustWaitOf(t, st, a.SessionID); ok {
		t.Fatal("a refused declaration wrote a row")
	}
	if _, _, err := st.DeclareWait(a.SessionID, a.Incarnation, []string{"api-work", "api-work"}, time.Hour, ""); err != nil {
		t.Fatalf("control: %v", err)
	}
	w, ok := mustWaitOf(t, st, a.SessionID)
	if !ok || len(w.Targets) != 1 {
		t.Fatalf("control: want one row with duplicate slugs collapsed to one target, got ok=%v %+v", ok, w)
	}
}

func TestDeclareWaitRequiresTheLiveIncarnation(t *testing.T) {
	st, _, a, _, _ := waitFixture(t)
	if _, _, err := st.DeclareWait(a.SessionID, "not-its-incarnation", []string{"api-work"}, time.Hour, ""); err == nil {
		t.Fatal("a stale incarnation must not declare")
	}
	if _, _, err := st.DeclareWait(a.SessionID, a.Incarnation, []string{"api-work"}, time.Hour, ""); err != nil {
		t.Fatalf("control: %v", err)
	}
}

// LANDED means EVERY target closed, and a timer never lands. The mutations
// this is here to kill: all -> any, and "no open target" being vacuously
// true for a wait with no targets.
func TestWaitVerdict(t *testing.T) {
	st, clk, a, b, _ := waitFixture(t)
	w, _, err := st.DeclareWait(a.SessionID, a.Incarnation, []string{"api-work", "docs-pass"}, time.Hour, "")
	if err != nil {
		t.Fatal(err)
	}
	if v := w.Verdict(clk.now()); v != WaitPending {
		t.Fatalf("fresh: want pending, got %v", v)
	}
	if err := st.Release(b.SessionID, b.Incarnation, "api-work"); err != nil {
		t.Fatal(err)
	}
	w, _ = mustWaitOf(t, st, a.SessionID)
	if v := w.Verdict(clk.now()); v != WaitPending {
		t.Fatalf("one of two released: want pending, got %v", v)
	}
	if err := st.Release(b.SessionID, b.Incarnation, "docs-pass"); err != nil {
		t.Fatal(err)
	}
	w, _ = mustWaitOf(t, st, a.SessionID)
	if v := w.Verdict(clk.now()); v != WaitLanded {
		t.Fatalf("both released: want landed, got %v", v)
	}
	// LANDED WINS over EXPIRED.
	if v := w.Verdict(clk.now().Add(2 * time.Hour)); v != WaitLanded {
		t.Fatalf("landed past the deadline: want landed, got %v", v)
	}

	// A timer: no targets. Pending up to the deadline, EXPIRED exactly at it.
	timer, _, err := st.DeclareWait(a.SessionID, a.Incarnation, nil, time.Hour, "external tier")
	if err != nil {
		t.Fatal(err)
	}
	if timer.Landed() {
		t.Fatal("a wait with no target must never land")
	}
	if v := timer.Verdict(timer.Deadline.Add(-time.Second)); v != WaitPending {
		t.Fatalf("a second before the deadline: want pending, got %v", v)
	}
	if v := timer.Verdict(timer.Deadline); v != WaitExpired {
		t.Fatalf("at the deadline: want expired, got %v", v)
	}
}

func TestWaitCheckLandsOnceThenReportsNoWait(t *testing.T) {
	st, clk, a, b, _ := waitFixture(t)
	if _, _, err := st.DeclareWait(a.SessionID, a.Incarnation, []string{"api-work"}, 3*time.Hour, "then: rebase"); err != nil {
		t.Fatal(err)
	}
	clk.advance(10 * time.Minute)
	r, err := st.WaitCheck(a.SessionID, a.Incarnation)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Found || r.Verdict != WaitPending || !r.Wait.IsOpen() || r.Wait.Checks != 1 || !r.Wait.LastCheck.Equal(clk.now()) {
		t.Fatalf("first check: want a pending, recorded, open wait, got %+v", r)
	}
	if err := st.Release(b.SessionID, b.Incarnation, "api-work"); err != nil {
		t.Fatal(err)
	}
	clk.advance(20 * time.Minute)
	r, err = st.WaitCheck(a.SessionID, a.Incarnation)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Found || r.Verdict != WaitLanded || r.Wait.Note != "then: rebase" {
		t.Fatalf("after the release: want LANDED with the note, got %+v", r)
	}
	if got := r.Wait.Targets[0]; got.State != "released" || got.Closed.IsZero() {
		t.Fatalf("the target must say how and when it closed: %+v", got)
	}
	// The check does NOT close it: the caller closes it after writing the
	// verdict. Until then a second check says LANDED again (at-least-once).
	if again, _ := st.WaitCheck(a.SessionID, a.Incarnation); !again.Found || again.Verdict != WaitLanded {
		t.Fatalf("an unclosed LANDED must be said again, got %+v", again)
	}
	if closed, err := st.CloseWait(a.SessionID, r.Wait.Decl, "landed"); err != nil || !closed {
		t.Fatalf("close: %v %v", closed, err)
	}
	if closed, _ := st.CloseWait(a.SessionID, r.Wait.Decl, "landed"); closed {
		t.Fatal("a second close must change nothing")
	}
	r, err = st.WaitCheck(a.SessionID, a.Incarnation)
	if err != nil {
		t.Fatal(err)
	}
	if r.Found || r.Wait.Reason != "landed" {
		t.Fatalf("the check after LANDED: want no open wait and the last one's reason, got %+v", r)
	}
	if w, _ := mustWaitOf(t, st, a.SessionID); w.Checks != 3 {
		t.Fatalf("a check with no open wait must write nothing; checks=%d", w.Checks)
	}
}

func TestWaitCheckExpiresATimer(t *testing.T) {
	st, clk, a, _, _ := waitFixture(t)
	if _, _, err := st.DeclareWait(a.SessionID, a.Incarnation, nil, time.Hour, ""); err != nil {
		t.Fatal(err)
	}
	clk.advance(59 * time.Minute)
	if r, _ := st.WaitCheck(a.SessionID, a.Incarnation); r.Verdict != WaitPending {
		t.Fatalf("control: a minute early must be pending, got %+v", r)
	}
	clk.advance(time.Minute)
	r, err := st.WaitCheck(a.SessionID, a.Incarnation)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != WaitExpired {
		t.Fatalf("at the deadline: want EXPIRED, got %+v", r)
	}
	if closed, err := st.CloseWait(a.SessionID, r.Wait.Decl, "expired"); err != nil || !closed {
		t.Fatalf("close: %v %v", closed, err)
	}
	if w, _ := mustWaitOf(t, st, a.SessionID); w.IsOpen() || w.Reason != "expired" {
		t.Fatalf("want the row closed as expired, got %+v", w)
	}
}

// Codex design pass, P1: a notice composed about one declaration must never
// mark its replacement, even one declared inside the same second.
func TestMarkWaitToldCannotMarkAReplacementDeclaredInTheSameSecond(t *testing.T) {
	st, _, a, b, _ := waitFixture(t)
	if _, _, err := st.DeclareWait(a.SessionID, a.Incarnation, []string{"api-work"}, time.Hour, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Release(b.SessionID, b.Incarnation, "api-work"); err != nil {
		t.Fatal(err)
	}
	first, _ := mustWaitOf(t, st, a.SessionID) // what a beat would compose its notice from
	// Replaced in the same second (the clock does not move).
	second, _, err := st.DeclareWait(a.SessionID, a.Incarnation, []string{"docs-pass"}, time.Hour, "")
	if err != nil {
		t.Fatal(err)
	}
	if !second.Since.Equal(first.Since) {
		t.Fatal("fixture: the two declarations must share a second")
	}
	if err := st.MarkWaitTold(a.SessionID, first.Decl); err != nil {
		t.Fatal(err)
	}
	if got, _ := mustWaitOf(t, st, a.SessionID); got.Told {
		t.Fatal("the mark for the replaced declaration landed on its successor")
	}
	// Control: the successor's own mark does land.
	if err := st.MarkWaitTold(a.SessionID, second.Decl); err != nil {
		t.Fatal(err)
	}
	if got, _ := mustWaitOf(t, st, a.SessionID); !got.Told {
		t.Fatal("control: the successor's own mark did not land")
	}
}

// Codex design pass: a check from an incarnation that has said bye must not
// read its old row as STILL WAITING and bump its counters.
func TestWaitCheckRefusesAnEndedSession(t *testing.T) {
	st, _, a, _, _ := waitFixture(t)
	if _, _, err := st.DeclareWait(a.SessionID, a.Incarnation, []string{"api-work"}, time.Hour, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.WaitCheck(a.SessionID, a.Incarnation); err != nil {
		t.Fatalf("control: a live session's check must run: %v", err)
	}
	if err := st.Bye(a.SessionID, a.Incarnation); err != nil {
		t.Fatal(err)
	}
	if _, err := st.WaitCheck(a.SessionID, a.Incarnation); err == nil {
		t.Fatal("an ended session's check must be refused")
	}
	if w, _ := mustWaitOf(t, st, a.SessionID); w.Checks != 1 {
		t.Fatalf("the refused check wrote to the row: checks=%d", w.Checks)
	}
}

// A waiter that said bye: its wait is invisible to OpenWaits at once (the
// read joins live sessions), and each cleanup entry point closes the row as
// "ended" on its own. The dry-run sweep is the rollback control.
func TestEndedWaitersRowIsClosedByEachCleanupEntryPoint(t *testing.T) {
	entries := []struct {
		name   string
		run    func(st *Store, c SessionInfo) error
		closes bool
	}{
		{"peer hello", func(st *Store, c SessionInfo) error { _, err := st.Hello("sess-d", "delta", "/wt/d", 1); return err }, true},
		{"peer claim", func(st *Store, c SessionInfo) error {
			return st.Claim(c.SessionID, c.Incarnation, "other", "x", []string{"other"})
		}, true},
		{"sweep", func(st *Store, c SessionInfo) error {
			_, err := st.Sweep(time.Hour, time.Hour, SweepOpts{})
			return err
		}, true},
		{"sweep --dry-run", func(st *Store, c SessionInfo) error {
			_, err := st.Sweep(time.Hour, time.Hour, SweepOpts{DryRun: true})
			return err
		}, false},
	}
	for _, e := range entries {
		t.Run(e.name, func(t *testing.T) {
			st, _, a, _, c := waitFixture(t)
			if _, _, err := st.DeclareWait(a.SessionID, a.Incarnation, []string{"api-work"}, time.Hour, ""); err != nil {
				t.Fatal(err)
			}
			if open, _ := st.OpenWaits(); len(open) != 1 {
				t.Fatalf("control: want the live waiter listed, got %d", len(open))
			}
			if err := st.Bye(a.SessionID, a.Incarnation); err != nil {
				t.Fatal(err)
			}
			if open, _ := st.OpenWaits(); len(open) != 0 {
				t.Fatalf("an ended session's wait must not be listed as open, got %d", len(open))
			}
			if err := e.run(st, c); err != nil {
				t.Fatal(err)
			}
			w, _ := mustWaitOf(t, st, a.SessionID)
			if e.closes && (w.IsOpen() || w.Reason != "ended") {
				t.Fatalf("want the row closed as ended, got open=%v reason=%q", w.IsOpen(), w.Reason)
			}
			if !e.closes && !w.IsOpen() {
				t.Fatalf("a dry run must roll the close back, got reason=%q", w.Reason)
			}
		})
	}
}

// The session's own revival closes its predecessor's wait before the new
// incarnation exists, as it orphans the predecessor's claims.
func TestRevivalClosesThePredecessorsWait(t *testing.T) {
	st, _, a, _, _ := waitFixture(t)
	w, _, err := st.DeclareWait(a.SessionID, a.Incarnation, []string{"api-work"}, time.Hour, "then: rebase")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Bye(a.SessionID, a.Incarnation); err != nil {
		t.Fatal(err)
	}
	revived := hello(t, st, a.SessionID, "", "/wt/a")
	if revived.Incarnation == a.Incarnation {
		t.Fatal("fixture: the revival must mint a new incarnation")
	}
	got, _ := mustWaitOf(t, st, a.SessionID)
	if got.IsOpen() || got.Reason != "ended" || got.Incarnation != a.Incarnation || got.Decl != w.Decl {
		t.Fatalf("the predecessor's wait must be closed as ended and stay the predecessor's, got %+v", got)
	}
	if r, err := st.WaitCheck(revived.SessionID, revived.Incarnation); err != nil || r.Found {
		t.Fatalf("the new incarnation must have no open wait, got found=%v err=%v", r.Found, err)
	}
}

// Fable design pass: a wait on a holder that has said bye lands at the
// waiter's next check, because the check orphans ended holders first — the
// statement `claim` runs (D-026). Without it the wait sat STILL WAITING and
// then EXPIRED "with the claim still open" although the ledger knew.
func TestWaitOnAHolderThatSaidByeLandsAtTheNextCheck(t *testing.T) {
	st, _, a, b, _ := waitFixture(t)
	if _, _, err := st.DeclareWait(a.SessionID, a.Incarnation, []string{"api-work"}, time.Hour, ""); err != nil {
		t.Fatal(err)
	}
	if r, _ := st.WaitCheck(a.SessionID, a.Incarnation); r.Verdict != WaitPending {
		t.Fatalf("control: pending while the holder is live, got %v", r.Verdict)
	}
	if err := st.Bye(b.SessionID, b.Incarnation); err != nil {
		t.Fatal(err)
	}
	r, err := st.WaitCheck(a.SessionID, a.Incarnation)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != WaitLanded || r.Wait.Targets[0].State != "orphaned" {
		t.Fatalf("want LANDED with the target orphaned, got %v / %+v", r.Verdict, r.Wait.Targets)
	}
	// A declaration on such a claim is refused, naming the bye — and it does
	// NOT orphan it: the refusal rolls back, and the first shape explained
	// itself with an orphaning that never committed (Codex code pass).
	c := hello(t, st, "sess-e", "echo", "/wt/e")
	if err := st.Claim(c.SessionID, c.Incarnation, "late", "x", []string{"late"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Bye(c.SessionID, c.Incarnation); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.DeclareWait(a.SessionID, a.Incarnation, []string{"late"}, time.Hour, ""); err == nil || !strings.Contains(err.Error(), "its holder echo said bye") {
		t.Fatalf("a wait on an ended holder's claim must be refused naming the bye, got %v", err)
	}
	claims, _ := st.Claims(false)
	stillOpen := false
	for _, cl := range claims {
		stillOpen = stillOpen || cl.Slug == "late"
	}
	if !stillOpen {
		t.Fatal("a refused declaration must leave the ended holder's claim exactly as it was")
	}
}

// Fable design pass: a slug the holder took again under a new claim id is
// named, so LANDED does not send the waiter into work still in progress.
func TestLandedTargetNamesTheSlugOpenAgain(t *testing.T) {
	st, _, a, b, _ := waitFixture(t)
	if _, _, err := st.DeclareWait(a.SessionID, a.Incarnation, []string{"api-work"}, time.Hour, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Release(b.SessionID, b.Incarnation, "api-work"); err != nil {
		t.Fatal(err)
	}
	w, _ := mustWaitOf(t, st, a.SessionID)
	if w.Targets[0].Reopened {
		t.Fatal("control: nothing has re-taken the slug yet")
	}
	// Several closed predecessors under the slug, then one open successor:
	// still ONE target, named as open again once.
	for i := 0; i < 2; i++ {
		if err := st.Claim(b.SessionID, b.Incarnation, "api-work", "again", []string{"internal/api"}); err != nil {
			t.Fatal(err)
		}
		if err := st.Release(b.SessionID, b.Incarnation, "api-work"); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Claim(b.SessionID, b.Incarnation, "api-work", "round four", []string{"internal/api"}); err != nil {
		t.Fatal(err)
	}
	w, _ = mustWaitOf(t, st, a.SessionID)
	if len(w.Targets) != 1 {
		t.Fatalf("history under the slug multiplied the target: %d", len(w.Targets))
	}
	tg := w.Targets[0]
	if w.Verdict(time.Now()) != WaitLanded || tg.State != "released" || !tg.Reopened || tg.ReopenedBy != "bravo" {
		t.Fatalf("want LANDED on the released id, with the slug named as open again under bravo, got %+v", tg)
	}
}

// A target still OPEN is not "reopened" by itself: the join excludes the
// awaited claim's own row.
func TestAnOpenTargetIsNotReportedAsReopened(t *testing.T) {
	st, _, a, _, _ := waitFixture(t)
	w, _, err := st.DeclareWait(a.SessionID, a.Incarnation, []string{"api-work"}, time.Hour, "")
	if err != nil {
		t.Fatal(err)
	}
	if tg := w.Targets[0]; !tg.IsOpen() || tg.Reopened {
		t.Fatalf("an open target named as open again under itself: %+v", tg)
	}
}

// Two waits declared in the same second, each on two claims, come back as
// two whole waits with their own targets in declaration order.
func TestOpenWaitsGroupsTargetsByTheirOwnWait(t *testing.T) {
	st, _, a, b, c := waitFixture(t)
	for _, cl := range []struct{ slug, scope string }{{"c-one", "c1"}, {"c-two", "c2"}} {
		if err := st.Claim(c.SessionID, c.Incarnation, cl.slug, "x", []string{cl.scope}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := st.DeclareWait(a.SessionID, a.Incarnation, []string{"docs-pass", "c-one"}, time.Hour, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.DeclareWait(b.SessionID, b.Incarnation, []string{"c-two", "mine"}, time.Hour, ""); err != nil {
		t.Fatal(err)
	}
	open, err := st.OpenWaits()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, w := range open {
		for _, tg := range w.Targets {
			got[w.SessionID] = append(got[w.SessionID], tg.Slug)
		}
	}
	if len(open) != 2 || strings.Join(got["sess-a"], ",") != "docs-pass,c-one" || strings.Join(got["sess-b"], ",") != "c-two,mine" {
		t.Fatalf("want two whole waits in declaration order, got %d: %v", len(open), got)
	}
}

// Two declarations that drew the same token must still not share targets:
// the join is on the session as well (Codex code pass). The collision is
// planted, since a real one is a 2^-64 event.
func TestTargetsNeverCrossSessionsEvenOnACollidingDeclarationID(t *testing.T) {
	st, _, a, b, c := waitFixture(t)
	if err := st.Claim(c.SessionID, c.Incarnation, "c-one", "x", []string{"c1"}); err != nil {
		t.Fatal(err)
	}
	wa, _, err := st.DeclareWait(a.SessionID, a.Incarnation, []string{"docs-pass"}, time.Hour, "")
	if err != nil {
		t.Fatal(err)
	}
	wb, _, err := st.DeclareWait(b.SessionID, b.Incarnation, []string{"c-one"}, time.Hour, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`UPDATE session_waits SET decl=? WHERE decl=?`, `UPDATE session_wait_targets SET decl=? WHERE decl=?`} {
		if _, err := st.db.Exec(q, wa.Decl, wb.Decl); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := mustWaitOf(t, st, a.SessionID)
	if len(got.Targets) != 1 || got.Targets[0].Slug != "docs-pass" {
		t.Fatalf("a colliding declaration id pulled in another session's targets: %+v", got.Targets)
	}
}

// A close keyed to a declaration never closes its replacement.
func TestCloseWaitIsKeyedToTheDeclaration(t *testing.T) {
	st, _, a, _, _ := waitFixture(t)
	first, _, err := st.DeclareWait(a.SessionID, a.Incarnation, nil, time.Hour, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.DeclareWait(a.SessionID, a.Incarnation, nil, 2*time.Hour, ""); err != nil {
		t.Fatal(err)
	}
	if closed, err := st.CloseWait(a.SessionID, first.Decl, "expired"); err != nil || closed {
		t.Fatalf("a close for the replaced declaration must touch nothing: %v %v", closed, err)
	}
	if w, _ := mustWaitOf(t, st, a.SessionID); !w.IsOpen() {
		t.Fatalf("the replacement was closed: %+v", w)
	}
	if _, err := st.CloseWait(a.SessionID, first.Decl, "cleared"); err == nil {
		t.Fatal("a check closes as landed or expired only")
	}
}

// A target whose claim row sweep has since deleted is closed, not open.
func TestTargetWhoseClaimRowWasSweptIsClosed(t *testing.T) {
	st, clk, a, b, _ := waitFixture(t)
	if _, _, err := st.DeclareWait(a.SessionID, a.Incarnation, []string{"api-work"}, WaitCeiling, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Release(b.SessionID, b.Incarnation, "api-work"); err != nil {
		t.Fatal(err)
	}
	clk.advance(2 * time.Hour)
	if _, err := st.Sweep(time.Hour, 24*time.Hour, SweepOpts{}); err != nil {
		t.Fatal(err)
	}
	w, _ := mustWaitOf(t, st, a.SessionID)
	if len(w.Targets) != 1 || w.Targets[0].State != "" || w.Targets[0].IsOpen() || !w.Landed() {
		t.Fatalf("a swept target must read as closed and the wait as landed, got %+v", w.Targets)
	}
}

// Invariant 10, word for word: a wait gives its waiter no standing. A third
// session claims the freed scope over a declared waiter, and a stale waiter's
// wait survives even sweep --force (it is not a reservation to reap).
func TestAWaitReservesNothingAndIsNeverReaped(t *testing.T) {
	st, clk, a, b, c := waitFixture(t)
	if _, _, err := st.DeclareWait(a.SessionID, a.Incarnation, []string{"api-work"}, WaitCeiling, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Release(b.SessionID, b.Incarnation, "api-work"); err != nil {
		t.Fatal(err)
	}
	if err := st.Claim(c.SessionID, c.Incarnation, "cuts-in", "x", []string{"internal/api"}); err != nil {
		t.Fatalf("a declared waiter must not refuse anybody: %v", err)
	}
	clk.advance(3 * time.Hour) // alpha is silent past StaleAfter and ForceAfter
	if _, err := st.Sweep(time.Hour, time.Hour, SweepOpts{Force: true}); err != nil {
		t.Fatal(err)
	}
	if w, _ := mustWaitOf(t, st, a.SessionID); !w.IsOpen() {
		t.Fatalf("sweep --force closed a live session's wait: reason=%q", w.Reason)
	}
}

func TestClearWait(t *testing.T) {
	st, _, a, _, _ := waitFixture(t)
	if got, err := st.ClearWait(a.SessionID, a.Incarnation); err != nil || got != nil {
		t.Fatalf("nothing to clear must be (nil, nil), got %+v %v", got, err)
	}
	if _, _, err := st.DeclareWait(a.SessionID, a.Incarnation, []string{"api-work"}, time.Hour, ""); err != nil {
		t.Fatal(err)
	}
	got, err := st.ClearWait(a.SessionID, a.Incarnation)
	if err != nil || got == nil || got.Reason != "cleared" || len(got.Targets) != 1 {
		t.Fatalf("want the cleared wait back, got %+v %v", got, err)
	}
	if r, _ := st.WaitCheck(a.SessionID, a.Incarnation); r.Found || r.Wait.Reason != "cleared" {
		t.Fatalf("after clear: want no open wait and reason cleared, got %+v", r)
	}
}

// A ledger at schema 8 gains the two tables and keeps its rows.
func TestSchema9MigratesAnOlderLedger(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.Claim(a.SessionID, a.Incarnation, "keep", "x", []string{"keep"}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DROP TABLE session_wait_targets`, `DROP TABLE session_waits`, `PRAGMA user_version = 8`} {
		if _, err := st.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrate(st.db); err != nil {
		t.Fatal(err)
	}
	var ver int
	if err := st.db.QueryRow(`PRAGMA user_version`).Scan(&ver); err != nil || ver != schemaVersion || schemaVersion != 9 {
		t.Fatalf("want user_version stamped %d (=9), got %d (err=%v)", schemaVersion, ver, err)
	}
	if _, _, err := st.DeclareWait(a.SessionID, a.Incarnation, nil, time.Hour, ""); err != nil {
		t.Fatalf("the migrated ledger must accept a wait: %v", err)
	}
	if claims, _ := st.Claims(false); len(claims) != 1 {
		t.Fatalf("the migration lost a claim: %d", len(claims))
	}
}
