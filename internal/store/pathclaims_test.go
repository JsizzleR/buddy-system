package store

// PathClaims and OwnClaimFacts (D-228, the own-claim arm). PathClaims is
// OwnerOf's own scan with the caller's coverage read off it, so its Owner and
// Held legs ride OwnerOf's tests (claimsnapshot_test.go names PathClaims in
// each); here: what COVERS, and what arms.

import "testing"

func covered(t *testing.T, st *Store, rel, session string) bool {
	t.Helper()
	v, err := st.PathClaims(rel, session)
	if err != nil {
		t.Fatal(err)
	}
	// One function, two answers: OwnerOf must agree on Owner and Held.
	c, held, err := st.OwnerOf(rel, session)
	if err != nil || held != v.Held || c.ClaimID != v.Owner.ClaimID {
		t.Fatalf("PathClaims and OwnerOf disagree on %s for %s: %+v vs %v %+v %v", rel, session, v, held, c, err)
	}
	return v.Covered
}

// Every OPEN claim row of the caller counts, in any mode: exclusive, shared,
// --based-on a run, a rider parked TO the caller (the integrator), one handed
// over. Not: a claim under the path, a prefix without the slash, a peer's
// claim, a released one.
func TestPathClaimsCoveredInEveryMode(t *testing.T) {
	f := newParkFx(t)
	st := f.st
	// A rider parked to the coordinator's run is the coordinator's row.
	if !covered(t, st, parkPath, f.coord.SessionID) {
		t.Fatal("a rider parked to the caller covers it")
	}
	if covered(t, st, parkPath, f.lane.SessionID) {
		t.Fatal("a claim the caller parked AWAY is not the caller's row")
	}
	// --based-on the run, over the parked rider.
	f.granted(f.c, "next", parkRun, parkPath)
	if !covered(t, st, parkPath, f.c.SessionID) {
		t.Fatal("a --based-on claim of the caller covers")
	}
	// Exclusive and shared.
	if err := st.Claim(f.d.SessionID, f.d.Incarnation, "docs", "x", []string{"docs"}); err != nil {
		t.Fatal(err)
	}
	if err := st.ClaimMode(f.c.SessionID, f.c.Incarnation, "shared", "x", []string{"notes"}, true); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		rel     string
		session SessionInfo
		want    bool
	}{
		{"docs/USAGE.md", f.d, true}, {"docs", f.d, true}, {"Docs/usage.md", f.d, true},
		{"docsx/a", f.d, false}, {"doc", f.d, false},
		{"notes/a.md", f.c, true},
		{"notes/a.md", f.d, false}, // a peer's shared claim covers nobody else
		{"docs/USAGE.md", f.c, false},
	} {
		if got := covered(t, st, c.rel, c.session.SessionID); got != c.want {
			t.Fatalf("covered(%s, %s) = %v, want %v", c.rel, c.session.Label, got, c.want)
		}
	}
	// One fold (invariant 13): a claim spelled decomposed covers the composed
	// path, which lowercasing alone does not (the gate folds before it asks,
	// so only this caller-level row sees the store's own fold).
	if err := st.Claim(f.d.SessionID, f.d.Incarnation, "cafe", "x", []string{"Caf\u0065\u0301.md"}); err != nil {
		t.Fatal(err)
	}
	if !covered(t, st, "caf\u00e9.md", f.d.SessionID) || covered(t, st, "cafe.md", f.d.SessionID) {
		t.Fatal("NFD claim, NFC path: one folded spelling; the plain e is another file")
	}
	// A claim under the path does not cover it.
	if err := st.Claim(f.d.SessionID, f.d.Incarnation, "deep", "x", []string{"lib/a/b"}); err != nil {
		t.Fatal(err)
	}
	if covered(t, st, "lib/a", f.d.SessionID) {
		t.Fatal("a claim under the path does not cover the path")
	}
	// Handed over: the receiver covers, the giver no longer does.
	if err := f.hand(f.d, f.c, "docs"); err != nil {
		t.Fatal(err)
	}
	if !covered(t, st, "docs/a", f.c.SessionID) || covered(t, st, "docs/a", f.d.SessionID) {
		t.Fatal("release --to moves coverage with the claim")
	}
	// Released: covers nothing.
	if err := st.Release(f.c.SessionID, f.c.Incarnation, "docs"); err != nil {
		t.Fatal(err)
	}
	if covered(t, st, "docs/a", f.c.SessionID) {
		t.Fatal("a released claim covers nothing")
	}
	// A no-session caller never covers (the scan's exclude is empty).
	if covered(t, st, parkPath, "") {
		t.Fatal("an empty session covers nothing")
	}
}

// OwnClaimFacts: known/ended from the caller's row; armed by another session
// not positively ended holding an open claim, computed only where the ledger
// judges the caller.
func TestOwnClaimFacts(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	facts := func(session string, judgeUnknown bool) (bool, bool, bool) {
		t.Helper()
		k, e, armed, err := st.OwnClaimFacts(session, judgeUnknown)
		if err != nil {
			t.Fatal(err)
		}
		return k, e, armed
	}
	if k, e, armed := facts(b.SessionID, false); !k || e || armed {
		t.Fatalf("nobody holds anything: known=%v ended=%v armed=%v", k, e, armed)
	}
	if err := st.Claim(a.SessionID, a.Incarnation, "router", "x", []string{"internal/router"}); err != nil {
		t.Fatal(err)
	}
	if _, _, armed := facts(b.SessionID, false); !armed {
		t.Fatal("a live peer's open claim arms")
	}
	if _, _, armed := facts(a.SessionID, false); armed {
		t.Fatal("the caller's own claims never arm")
	}
	// An unknown caller: armed only where the ledger judges one it has no row for.
	if k, _, armed := facts("sess-x", false); k || armed {
		t.Fatalf("an unknown caller, not judged: known=%v armed=%v (the scan must not run)", k, armed)
	}
	if k, _, armed := facts("sess-x", true); k || !armed {
		t.Fatalf("an unknown caller in the cwd's own ledger: known=%v armed=%v", k, armed)
	}
	// A holder that said bye still holds (OwnerOf) and arms nothing.
	if err := st.Bye(a.SessionID, a.Incarnation); err != nil {
		t.Fatal(err)
	}
	if _, held, err := st.OwnerOf("internal/router/x.go", b.SessionID); err != nil || !held {
		t.Fatalf("control: the ended holder's claim still holds: held=%v err=%v", held, err)
	}
	if _, _, armed := facts(b.SessionID, false); armed {
		t.Fatal("an ended holder arms nothing")
	}
	if k, e, _ := facts(a.SessionID, false); !k || !e {
		t.Fatalf("an ended caller is known and ended: known=%v ended=%v", k, e)
	}
	// A holder with NO row arms: nothing says it ended.
	c := hello(t, st, "sess-c", "charlie", "/wt/c")
	if err := st.Claim(c.SessionID, c.Incarnation, "c", "x", []string{"lib"}); err != nil {
		t.Fatal(err)
	}
	dropSessionRow(t, st, "sess-c")
	if _, _, armed := facts(b.SessionID, false); !armed {
		t.Fatal("a holder with no row arms")
	}
	st.Close()
	if _, _, _, err := st.OwnClaimFacts(b.SessionID, true); err == nil {
		t.Fatal("a snapshot that cannot open must be an error")
	}
}
