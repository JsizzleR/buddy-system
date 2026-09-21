package store

import (
	"testing"
)

// D-026: a holder that has said bye cannot refuse a peer's claim. Claim runs
// orphanEnded first, in its own transaction; the conflict scan excludes ended
// owners so the dry run forecasts the same thing; and the dry run SAYS what
// it would displace, because "free once cleaned up" and "nobody holds this"
// are different facts.

func TestClaimOrphansAnEndedOwnerRatherThanLeavingTwoOpenRows(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.Claim(a.SessionID, a.Incarnation, "api-work", "x", []string{"internal/api"}); err != nil {
		t.Fatal(err)
	}
	// CONTROL: contested while alpha is live.
	if err := st.Claim(b.SessionID, b.Incarnation, "wants", "x", []string{"internal/api/server.go"}); err == nil {
		t.Fatal("not contested beforehand, so this test proves nothing")
	}
	if err := st.Bye(a.SessionID, a.Incarnation); err != nil {
		t.Fatal(err)
	}
	if err := st.Claim(b.SessionID, b.Incarnation, "wants", "x", []string{"internal/api/server.go"}); err != nil {
		t.Fatalf("a holder that has said bye must not refuse: %v", err)
	}
	// The ended holder's row is ORPHANED, not left open beside the new claim:
	// two open rows over one scope is what the gate would then adjudicate
	// against the new holder.
	all, err := st.Claims(true)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, c := range all {
		states[c.Slug] = c.State
	}
	if states["api-work"] != "orphaned" || states["wants"] != "open" {
		t.Fatalf("want api-work orphaned and wants open, got %v", states)
	}
	// The gate now names the new holder to a third party and nobody to the
	// holder itself.
	if c, held, _ := st.OwnerOf("internal/api/server.go", "sess-c"); !held || c.Slug != "wants" {
		t.Fatalf("OwnerOf for a third session: held=%v slug=%q", held, c.Slug)
	}
	if _, held, _ := st.OwnerOf("internal/api/server.go", b.SessionID); held {
		t.Fatal("the new holder must not be refused by the orphaned row")
	}
}

func TestDryRunReportsWhatAClaimWouldDisplace(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.Claim(a.SessionID, a.Incarnation, "api-work", "x", []string{"internal/api", "docs"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Bye(a.SessionID, a.Incarnation); err != nil {
		t.Fatal(err)
	}
	// Same slug AND an overlapping scope: both displacements are reported,
	// slug first, and neither is a conflict nor silently "free".
	free, conflicts, displaced, err := st.ClaimConflicts(b.SessionID, b.Incarnation, "api-work", []string{"internal/api/x.go", "cmd"})
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("an ended holder is not a conflict: %+v", conflicts)
	}
	if len(free) != 2 {
		t.Fatalf("both scopes would be taken: %v", free)
	}
	if len(displaced) != 2 || displaced[0].Scope != "" || displaced[0].Slug != "api-work" || displaced[0].Claimant != "alpha" ||
		displaced[1].Scope != "internal/api/x.go" || displaced[1].Their != "internal/api" || displaced[1].Slug != "api-work" {
		t.Fatalf("displacements: %+v", displaced)
	}
	// The forecast and the claim agree.
	if err := st.Claim(b.SessionID, b.Incarnation, "api-work", "x", []string{"internal/api/x.go", "cmd"}); err != nil {
		t.Fatalf("the claim the dry run forecast must succeed: %v", err)
	}
	// A LIVE holder is displaced by nothing and reported as a conflict.
	c := hello(t, st, "sess-c", "charlie", "/wt/c")
	_, conflicts, displaced, err = st.ClaimConflicts(c.SessionID, c.Incarnation, "other", []string{"cmd/main.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 || len(displaced) != 0 {
		t.Fatalf("live holder: conflicts=%d displaced=%d", len(conflicts), len(displaced))
	}
}

// A claim whose session row is missing — nothing deletes sessions today, so
// this is planted — must keep refusing. An inner join would drop it from the
// scan while the gate kept enforcing it: a claim that grants and then denies.
func TestADanglingClaimStillRefuses(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.Claim(a.SessionID, a.Incarnation, "api-work", "x", []string{"internal/api"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`DELETE FROM sessions WHERE session_id='sess-a'`); err != nil {
		t.Fatal(err)
	}
	if err := st.Claim(b.SessionID, b.Incarnation, "wants", "x", []string{"internal/api"}); err == nil {
		t.Fatal("a dangling claim vanished from the scan")
	}
	if _, conflicts, _, err := st.ClaimConflicts(b.SessionID, b.Incarnation, "wants", []string{"internal/api"}); err != nil || len(conflicts) != 1 {
		t.Fatalf("dry run must see it too: %v %d", err, len(conflicts))
	}
}
