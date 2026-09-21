package store

import (
	"errors"
	"testing"
	"time"
)

// Whether a STALE claim blocks a new claim on the same scope (issue #18).
//
// The rule is settled in code and was never ambiguous: scopeConflicts tests
// state='open' and nothing else, so staleness has no bearing on acquisition.
// It was UNWRITTEN, and on one afternoon two careful sessions measured
// opposite answers ten minutes apart — one of them generalising a single
// observation of a roster row for a claim that had just been released. A
// third session sat blocked ~3h while a fourth insisted the scope was free.
//
// These tests exist so the answer stops being folklore. They assert the rule
// in BOTH directions: a stale claim refuses, and the only thing that frees a
// scope is the claim actually leaving the open state.

func TestStaleClaimStillRefusesANewClaim(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")

	if err := st.Claim(a.SessionID, a.Incarnation, "held", "holding it", []string{"internal/api"}); err != nil {
		t.Fatalf("setup claim: %v", err)
	}

	// POSITIVE CONTROL: the scope is genuinely contested while the holder is
	// FRESH. Without this, "it refused" and "the test never armed" look alike.
	if err := st.Claim(b.SessionID, b.Incarnation, "wants", "wants it", []string{"internal/api"}); err == nil {
		t.Fatal("a fresh holder did not refuse, so the stale case below proves nothing")
	}

	// Past StaleAfter with no renewal: the holder is STALE by every reader's
	// reckoning, and the roster says so.
	clk.advance(StaleAfter + time.Minute)
	claims, err := st.Claims(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || !claims[0].Stale(clk.now()) {
		t.Fatalf("setup: expected exactly one claim, stale; got %d", len(claims))
	}

	err = st.Claim(b.SessionID, b.Incarnation, "wants", "wants it", []string{"internal/api"})
	if err == nil {
		t.Fatal("a STALE claim did not refuse a new claim on its scope — staleness marks, it never reaps (invariant 11)")
	}
	var refused ErrRefused
	if !errors.As(err, &refused) {
		t.Fatalf("refusal was not an ErrRefused: %v", err)
	}
	if refused.Renewed.IsZero() {
		t.Fatal("the refusal carries no holder clock, so the CLI cannot say the holder went quiet")
	}
	if !refused.Renewed.Equal(claims[0].Renewed) {
		t.Fatalf("refusal clock %v is not the holder's renewed %v", refused.Renewed, claims[0].Renewed)
	}

	// The dry run must forecast the SAME thing, clock included: a forecast that
	// disagrees with the refusal it predicts is worse than no forecast (D-019).
	free, conflicts, _, err := st.ClaimConflicts(b.SessionID, b.Incarnation, "wants", []string{"internal/api"})
	if err != nil {
		t.Fatal(err)
	}
	if len(free) != 0 || len(conflicts) != 1 {
		t.Fatalf("dry run disagreed with the refusal: free=%v conflicts=%d", free, len(conflicts))
	}
	if !conflicts[0].Renewed.Equal(refused.Renewed) {
		t.Fatalf("dry run clock %v != refusal clock %v", conflicts[0].Renewed, refused.Renewed)
	}
}

// The other direction, and the one the field report actually confused: what
// frees a scope is the claim leaving the OPEN state. A released claim's row
// lingers in `ls --all` labelled "released", and reading that row as a live
// hold is how "a stale claim does not block" got believed.
//
// THE BYE CASE HAS CHANGED SIDES ONCE, and the history is the point. Under
// D-022 it was pinned `wantFreed: false`: Bye stamps sessions.ended and
// touches no claim row (invariant 12), orphaning ran only in hello and sweep,
// and so a session that finished and exited left its scopes held, blocking
// every peer, with the holder gone — issue #15's incident, where a coordinator
// verified a session's work was landed, told it to exit, and it still held two
// claims. D-026 made Claim run the same orphaning first, so a peer's claim now
// frees the scopes of a holder that has SAID BYE. Bye itself still touches no
// claim row, and `ended` is positive evidence — which is what a silent holder
// (the next test) never provides.
func TestOnlyLeavingOpenFreesAScope(t *testing.T) {
	cases := []struct {
		name      string
		act       func(t *testing.T, st *Store, clk *pinnedClock, a SessionInfo)
		wantFreed bool
	}{
		{name: "released by its holder", wantFreed: true,
			act: func(t *testing.T, st *Store, clk *pinnedClock, a SessionInfo) {
				if err := st.Release(a.SessionID, a.Incarnation, "held"); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "the holder says bye and exits", wantFreed: true,
			act: func(t *testing.T, st *Store, clk *pinnedClock, a SessionInfo) {
				if err := st.Bye(a.SessionID, a.Incarnation); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "the holder comes back, and hello orphans the old incarnation", wantFreed: true,
			act: func(t *testing.T, st *Store, clk *pinnedClock, a SessionInfo) {
				if err := st.Bye(a.SessionID, a.Incarnation); err != nil {
					t.Fatal(err)
				}
				hello(t, st, a.SessionID, "alpha", "/wt/a")
			}},
		{name: "sweep --force, the operator's explicit act", wantFreed: true,
			act: func(t *testing.T, st *Store, clk *pinnedClock, a SessionInfo) {
				const forceAfter = 24 * time.Hour // cli.ForceAfter, passed in
				clk.advance(forceAfter + time.Hour)
				if _, _, err := st.Sweep(24*time.Hour, forceAfter, true); err != nil {
					t.Fatal(err)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, clk := openTest(t)
			a := hello(t, st, "sess-a", "alpha", "/wt/a")
			b := hello(t, st, "sess-b", "bravo", "/wt/b")
			if err := st.Claim(a.SessionID, a.Incarnation, "held", "holding it", []string{"internal/api"}); err != nil {
				t.Fatal(err)
			}
			// CONTROL: contested before the act, so a later "free" is the act
			// and not a scope that was never held.
			if err := st.Claim(b.SessionID, b.Incarnation, "wants", "x", []string{"internal/api"}); err == nil {
				t.Fatal("not contested beforehand, so this case proves nothing")
			}

			tc.act(t, st, clk, a)

			err := st.Claim(b.SessionID, b.Incarnation, "wants", "x", []string{"internal/api"})
			switch {
			case tc.wantFreed && err != nil:
				t.Fatalf("scope was not freed by %q: %v", tc.name, err)
			case !tc.wantFreed && err == nil:
				t.Fatalf("scope was freed by %q, but only release, orphaning of an ended owner (hello/claim/sweep) and sweep --force may free one", tc.name)
			}
		})
	}
}

// Staleness must not leak into the verdict by the back door either: a holder
// that has gone quiet for a very long time still refuses.
func TestAVeryOldClaimStillRefuses(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.Claim(a.SessionID, a.Incarnation, "held", "holding it", []string{"internal/api"}); err != nil {
		t.Fatal(err)
	}
	clk.advance(30 * 24 * time.Hour)
	if err := st.Claim(b.SessionID, b.Incarnation, "wants", "x", []string{"internal/api"}); err == nil {
		t.Fatal("a month-old claim was silently taken over; only sweep --force may do that, and only by the operator")
	}
}

// TWO stale holders, both annotated, on BOTH paths.
//
// Codex predicted the mutation this closes: dropping Renewed from the More
// entries of ErrRefused. Every other test here exercises a single conflict, so
// the first conflict's clock is the only one asserted — the dry run would
// annotate both holders and the refusal only the first, and no test would say
// so. That is the forecast/refusal divergence D-019 exists to prevent, arriving
// one field further in.
func TestEveryConflictCarriesItsOwnHolderClock(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	c := hello(t, st, "sess-c", "charlie", "/wt/c")
	if err := st.Claim(a.SessionID, a.Incarnation, "pa", "a", []string{"pkg/a"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Claim(b.SessionID, b.Incarnation, "pb", "b", []string{"pkg/b"}); err != nil {
		t.Fatal(err)
	}
	clk.advance(3 * time.Hour) // both holders quiet

	_, conflicts, _, err := st.ClaimConflicts(c.SessionID, c.Incarnation, "all-pkg", []string{"pkg"})
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 2 {
		t.Fatalf("want two holders under pkg, got %d", len(conflicts))
	}
	for _, cf := range conflicts {
		if cf.Renewed.IsZero() {
			t.Fatalf("dry run conflict %q carries no holder clock", cf.Slug)
		}
	}

	err = st.Claim(c.SessionID, c.Incarnation, "all-pkg", "x", []string{"pkg"})
	var refused ErrRefused
	if !errors.As(err, &refused) {
		t.Fatalf("want a refusal, got %v", err)
	}
	if len(refused.More) != 1 {
		t.Fatalf("want both holders in the refusal, got 1+%d", len(refused.More))
	}
	// EVERY entry, not just the first: the More entries are what the CLI
	// rebuilds the annotated set from.
	if refused.Renewed.IsZero() {
		t.Fatal("the first conflict lost its clock")
	}
	if refused.More[0].Renewed.IsZero() {
		t.Fatal("a LATER conflict lost its clock, so the refusal would annotate fewer holders than the dry run forecast")
	}
	// And they agree with the forecast, holder by holder.
	byslug := map[string]time.Time{}
	for _, cf := range conflicts {
		byslug[cf.Slug] = cf.Renewed
	}
	for _, r := range append([]ErrRefused{refused}, refused.More...) {
		if want, ok := byslug[r.Slug]; !ok || !want.Equal(r.Renewed) {
			t.Fatalf("refusal clock for %q (%v) disagrees with the forecast (%v)", r.Slug, r.Renewed, want)
		}
	}
}
