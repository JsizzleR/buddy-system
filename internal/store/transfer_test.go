package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// D-063: `release <slug> --to <target>` moves an open claim to another live
// session in one transaction, keeping its id, so the waits on it keep waiting.

func TestTransferKeepsTheClaimSoItsWaitsKeepWaiting(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	c := hello(t, st, "sess-c", "charlie", "/wt/c")
	if err := st.Claim(a.SessionID, a.Incarnation, "run", "RUNNING", []string{".buddy/slot/run", ".buddy/slot/main"}); err != nil {
		t.Fatal(err)
	}
	before := claimBySlug(t, st, "run")
	if _, _, err := st.DeclareWaitReady(b.SessionID, b.Incarnation, []string{"run"}, 3*time.Hour, "", "abc123"); err != nil {
		t.Fatal(err)
	}
	clk.advance(10 * time.Minute)

	id, label, err := st.TransferClaim(a.SessionID, a.Incarnation, "run", mustResolve(t, st, "charlie"))
	if err != nil {
		t.Fatal(err)
	}
	if id != before.ClaimID || label != "charlie" {
		t.Fatalf("got id %q label %q; want the same claim %q, handed to charlie", id, label, before.ClaimID)
	}
	after := claimBySlug(t, st, "run")
	if after.ClaimID != before.ClaimID || after.State != "open" || after.Owner.SessionID != c.SessionID || after.Incarnation != c.Incarnation {
		t.Fatalf("claim after transfer: %+v", after)
	}
	if !after.Created.Equal(before.Created) || !after.Renewed.Equal(clk.now()) || after.Desc != "RUNNING" || len(after.Scopes) != 2 {
		t.Fatalf("created must stand, renewed must be now, desc and scopes untouched: %+v", after)
	}

	w, ok, err := st.WaitOf(b.SessionID)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if v := w.Verdict(clk.now()); v != WaitPending || w.ReadySHA != "abc123" {
		t.Fatalf("the rider's wait must still be pending with its READY sha: verdict %v sha %q", v, w.ReadySHA)
	}

	// The gate follows the owner: the old holder is refused, the new one is not.
	if _, held, _ := st.OwnerOf(".buddy/slot/main", a.SessionID); !held {
		t.Fatal("the old holder must now be refused by the moved claim")
	}
	if _, held, _ := st.OwnerOf(".buddy/slot/main", c.SessionID); held {
		t.Fatal("the new holder must not be refused by its own claim")
	}
	// The new holder refreshes it like any claim of its own, keeping the id,
	// and its release with an outcome is what lands the rider.
	if err := st.Claim(c.SessionID, c.Incarnation, "run", "RUNNING under charlie", []string{".buddy/slot/run", ".buddy/slot/main"}); err != nil {
		t.Fatal(err)
	}
	if claimBySlug(t, st, "run").ClaimID != before.ClaimID {
		t.Fatal("a refresh by the new holder must keep the claim id")
	}
	if _, err := st.ReleaseOutcome(c.SessionID, c.Incarnation, "run", "pass", "landed"); err != nil {
		t.Fatal(err)
	}
	w, _, _ = st.WaitOf(b.SessionID)
	if w.Verdict(clk.now()) != WaitLanded || w.Targets[0].Outcome != "pass" {
		t.Fatalf("the rider lands on the new holder's release, with its outcome: %+v", w.Targets)
	}
}

// The positive control for the test above: release-then-claim, the only road
// before D-063, lands the rider with no outcome. If this stops failing the
// rider, the transfer test above proves nothing about the transfer.
func TestReleaseThenClaimLandsTheRidersWithNoOutcome(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	c := hello(t, st, "sess-c", "charlie", "/wt/c")
	if err := st.Claim(a.SessionID, a.Incarnation, "run", "x", []string{".buddy/slot/run"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.DeclareWaitReady(b.SessionID, b.Incarnation, []string{"run"}, 3*time.Hour, "", "abc123"); err != nil {
		t.Fatal(err)
	}
	if err := st.Release(a.SessionID, a.Incarnation, "run"); err != nil {
		t.Fatal(err)
	}
	if err := st.Claim(c.SessionID, c.Incarnation, "run", "x", []string{".buddy/slot/run"}); err != nil {
		t.Fatal(err)
	}
	w, _, _ := st.WaitOf(b.SessionID)
	if w.Verdict(clk.now()) != WaitLanded || w.Targets[0].Outcome != "" {
		t.Fatalf("release-then-claim should land the rider with no outcome: %v %+v", w.Verdict(clk.now()), w.Targets)
	}
}

func TestTransferRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		// setup runs after alpha claims "run" on pkg; it returns the target to
		// resolve BEFORE its mutate step, and mutate runs between resolution
		// and the transfer.
		setup  func(t *testing.T, st *Store) string
		mutate func(t *testing.T, st *Store)
		sender func(a SessionInfo) SessionInfo
		want   string
	}{
		{name: "control: a live recipient is handed the claim", setup: func(t *testing.T, st *Store) string { return "charlie" }},
		{name: "to itself", setup: func(t *testing.T, st *Store) string { return "alpha" }, want: "already yours"},
		{name: "to every session", setup: func(t *testing.T, st *Store) string { return AllTarget }, want: "every session"},
		{name: "recipient ended", setup: func(t *testing.T, st *Store) string { return "charlie" },
			mutate: func(t *testing.T, st *Store) { byeLive(t, st, "sess-c") }, want: "said bye"},
		{name: "recipient re-registered after it was resolved", setup: func(t *testing.T, st *Store) string { return "charlie" },
			mutate: func(t *testing.T, st *Store) { byeLive(t, st, "sess-c"); hello(t, st, "sess-c", "charlie", "/wt/c") },
			want:   "re-registered"},
		{name: "recipient waits on this claim", setup: func(t *testing.T, st *Store) string {
			c, _, _ := st.SessionByID("sess-c")
			if _, _, err := st.DeclareWait(c.SessionID, c.Incarnation, []string{"run"}, time.Hour, ""); err != nil {
				t.Fatal(err)
			}
			return "charlie"
		}, want: "waiting on this claim"},
		{name: "sender not live under its incarnation", setup: func(t *testing.T, st *Store) string { return "charlie" },
			sender: func(a SessionInfo) SessionInfo { a.Incarnation = "stale-inc"; return a }, want: "not live"},
		{name: "sender ended but its claim not yet orphaned", setup: func(t *testing.T, st *Store) string { return "charlie" },
			mutate: func(t *testing.T, st *Store) { byeLive(t, st, "sess-a") }, want: "not live"},
		{name: "the recipient could not have claimed it: the sender's own overlap stays behind", setup: func(t *testing.T, st *Store) string {
			a, _, _ := st.SessionByID("sess-a")
			if err := st.Claim(a.SessionID, a.Incarnation, "inner", "x", []string{"pkg/f.go"}); err != nil {
				t.Fatal(err)
			}
			return "charlie"
		}, want: "overlaps"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := openTest(t)
			a := hello(t, st, "sess-a", "alpha", "/wt/a")
			hello(t, st, "sess-c", "charlie", "/wt/c")
			if err := st.Claim(a.SessionID, a.Incarnation, "run", "x", []string{"pkg"}); err != nil {
				t.Fatal(err)
			}
			to := mustResolve(t, st, tc.setup(t, st))
			if tc.mutate != nil {
				tc.mutate(t, st)
			}
			sender := a
			if tc.sender != nil {
				sender = tc.sender(a)
			}
			_, _, err := st.TransferClaim(sender.SessionID, sender.Incarnation, "run", to)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("control refused: %v", err)
				}
				if got := claimBySlug(t, st, "run").Owner.Label; got != "charlie" {
					t.Fatalf("control: owner is %q", got)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want a refusal containing %q, got %v", tc.want, err)
			}
			// Refused whole: the claim stands where it was, and nothing moved.
			if got := claimBySlug(t, st, "run"); got.Owner.SessionID != "sess-a" {
				t.Fatalf("a refused transfer moved the claim to %s", got.Owner.SessionID)
			}
		})
	}
}

func TestTransferOfAClaimYouDoNotHoldSaysWhy(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	c := hello(t, st, "sess-c", "charlie", "/wt/c")
	if err := st.Claim(a.SessionID, a.Incarnation, "run", "x", []string{"pkg"}); err != nil {
		t.Fatal(err)
	}
	_, _, err := st.TransferClaim(c.SessionID, c.Incarnation, "run", mustResolve(t, st, "alpha"))
	var nr ErrNoRelease
	if !errors.As(err, &nr) || nr.Yours || !nr.Exists {
		t.Fatalf("want ErrNoRelease naming alpha's claim, got %v", err)
	}
}

func claimBySlug(t *testing.T, st *Store, slug string) ClaimInfo {
	t.Helper()
	all, err := st.Claims(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range all {
		if c.Slug == slug {
			return c
		}
	}
	t.Fatalf("no open claim %q", slug)
	return ClaimInfo{}
}

func byeLive(t *testing.T, st *Store, id string) {
	t.Helper()
	si, ok, err := st.SessionByID(id)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := st.Bye(si.SessionID, si.Incarnation); err != nil {
		t.Fatal(err)
	}
}
