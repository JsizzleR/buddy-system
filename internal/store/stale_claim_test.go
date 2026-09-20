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
	free, conflicts, err := st.ClaimConflicts(b.SessionID, b.Incarnation, "wants", []string{"internal/api"})
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
// frees a scope is the claim leaving the OPEN state, and only four things do
// that. A released claim's row lingers in `ls --all` labelled "released", and
// reading that row as a live hold is how "a stale claim does not block" got
// believed.
//
// THE BYE CASE IS THE ONE WORTH READING, and it was written here expecting the
// opposite. A clean exit does NOT free a claim: Bye stamps sessions.ended and
// touches no claim row, because orphaning happens in hello and sweep and never
// inline in bye (invariant 12) — a delayed bye from a dead incarnation must not
// orphan a live one's work. So a session that finishes and exits leaves its
// scopes held, blocking every peer, with the holder gone. That is exactly the
// incident in issue #15: a coordinator verified a session's work was landed and
// its tree clean, told it to exit, and it still held two claims.
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
		{name: "the holder says bye and exits", wantFreed: false,
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
				t.Fatalf("scope was freed by %q, but only release, hello's orphaning and sweep --force may free one", tc.name)
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
