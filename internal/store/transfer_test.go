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

// D-093: a recipient waiting on the claim it is handed is not refused; the
// move resolves its wait, computed at read time. The cases are the measured
// shapes: a successor parked on the claim it inherits (single target), the
// same with a deadline already past (which D-063's count still refused), a
// multi-target wait that must NOT land on one handover, and a bystander
// waiting on the claim, whose wait follows it as before.
func TestTransferResolvesTheRecipientsOwnWaitOnIt(t *testing.T) {
	for _, tc := range []struct {
		name    string
		extra   bool          // charlie also waits on alpha's "other"
		advance time.Duration // after declaring, before the transfer
		want    WaitVerdict
	}{
		{name: "the successor parked on it lands", want: WaitLanded},
		{name: "an expired wait never blocks, and lands", advance: 2 * time.Hour, want: WaitLanded},
		{name: "a second target still open keeps it pending", extra: true, want: WaitPending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, clk := openTest(t)
			a := hello(t, st, "sess-a", "alpha", "/wt/a")
			b := hello(t, st, "sess-b", "bravo", "/wt/b")
			c := hello(t, st, "sess-c", "charlie", "/wt/c")
			if err := st.Claim(a.SessionID, a.Incarnation, "run", "x", []string{"pkg"}); err != nil {
				t.Fatal(err)
			}
			if err := st.Claim(a.SessionID, a.Incarnation, "other", "x", []string{"lib"}); err != nil {
				t.Fatal(err)
			}
			on := []string{"run"}
			if tc.extra {
				on = append(on, "other")
			}
			if _, _, err := st.DeclareWait(c.SessionID, c.Incarnation, on, time.Hour, ""); err != nil {
				t.Fatal(err)
			}
			if _, _, err := st.DeclareWait(b.SessionID, b.Incarnation, []string{"run"}, 3*time.Hour, ""); err != nil {
				t.Fatal(err)
			}
			clk.advance(tc.advance)
			// Positive control: before the move, charlie is waiting (or has
			// expired), and nothing about its target says handed.
			w, _, _ := st.WaitOf(c.SessionID)
			if w.Landed() || w.Targets[0].HandedToWaiter {
				t.Fatalf("before the transfer charlie's wait must not have landed: %+v", w.Targets)
			}

			moved, _, err := st.TransferClaims(a.SessionID, a.Incarnation, []string{"run"}, mustResolve(t, st, "charlie"))
			if err != nil {
				t.Fatalf("a recipient waiting on the claim must be handed it: %v", err)
			}
			if len(moved) != 1 || !moved[0].RecipientWaited {
				t.Fatalf("the transfer must report that the recipient waited on it: %+v", moved)
			}
			w, _, _ = st.WaitOf(c.SessionID)
			if !w.Targets[0].HandedToWaiter || w.Targets[0].Pending() {
				t.Fatalf("charlie's target must read as handed to it: %+v", w.Targets[0])
			}
			got := w.Verdict(clk.now())
			if tc.advance > 0 {
				// LANDED wins over EXPIRED (Verdict's rule): the thing waited
				// for happened.
				if got != WaitLanded {
					t.Fatalf("an expired wait whose claim was handed over is LANDED, got %v", got)
				}
			} else if got != tc.want {
				t.Fatalf("verdict %v, want %v", got, tc.want)
			}
			// The bystander's wait follows the claim and keeps waiting.
			wb, _, _ := st.WaitOf(b.SessionID)
			if wb.Verdict(clk.now()) != WaitPending || wb.Targets[0].HandedToWaiter {
				t.Fatalf("bravo's wait on the claim must still be pending: %+v", wb.Targets)
			}
			// Handing it on again un-does "handed to charlie": computed, never stored.
			if _, _, err := st.TransferClaim(c.SessionID, c.Incarnation, "run", mustResolve(t, st, "alpha")); err != nil {
				t.Fatal(err)
			}
			if w, _, _ = st.WaitOf(c.SessionID); w.Targets[0].HandedToWaiter {
				t.Fatalf("a claim handed away again is not the waiter's: %+v", w.Targets[0])
			}
		})
	}
}

// A recipient waiting on some OTHER claim is unaffected by being handed one.
func TestTransferLeavesTheRecipientsOtherWaitAlone(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	c := hello(t, st, "sess-c", "charlie", "/wt/c")
	if err := st.Claim(a.SessionID, a.Incarnation, "run", "x", []string{"pkg"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Claim(b.SessionID, b.Incarnation, "theirs", "x", []string{"lib"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.DeclareWait(c.SessionID, c.Incarnation, []string{"theirs"}, time.Hour, ""); err != nil {
		t.Fatal(err)
	}
	moved, _, err := st.TransferClaims(a.SessionID, a.Incarnation, []string{"run"}, mustResolve(t, st, "charlie"))
	if err != nil || moved[0].RecipientWaited {
		t.Fatalf("moved %+v err %v: charlie waited on another claim, not this one", moved, err)
	}
	if w, _, _ := st.WaitOf(c.SessionID); w.Verdict(clk.now()) != WaitPending {
		t.Fatalf("charlie's wait on bravo's claim must still be pending: %+v", w.Targets)
	}
}

// D-093: several claims move in one transaction. A sender's claims that
// overlap EACH OTHER can only move together; one alone is refused naming the
// WHOLE set it leaves behind; and the set is all or nothing.
func TestTransferClaimsMovesASetWhole(t *testing.T) {
	setup := func(t *testing.T) (*Store, SessionInfo) {
		st, _ := openTest(t)
		a := hello(t, st, "sess-a", "alpha", "/wt/a")
		hello(t, st, "sess-c", "charlie", "/wt/c")
		for slug, scope := range map[string]string{"outer": "pkg", "in1": "pkg/a.go", "in2": "pkg/b.go"} {
			if err := st.Claim(a.SessionID, a.Incarnation, slug, "x", []string{scope}); err != nil {
				t.Fatal(err)
			}
		}
		return st, a
	}
	owners := func(t *testing.T, st *Store) map[string]string {
		out := map[string]string{}
		for _, s := range []string{"outer", "in1", "in2"} {
			out[s] = claimBySlug(t, st, s).Owner.Label
		}
		return out
	}

	t.Run("one alone is refused, naming every claim it leaves behind", func(t *testing.T) {
		st, a := setup(t)
		_, _, err := st.TransferClaims(a.SessionID, a.Incarnation, []string{"outer"}, mustResolve(t, st, "charlie"))
		var r ErrRefused
		if !errors.As(err, &r) || len(r.More) != 1 {
			t.Fatalf("want one refusal carrying both overlaps (first + 1 more), got %v", err)
		}
		slugs := map[string]bool{r.Slug: true, r.More[0].Slug: true}
		if !slugs["in1"] || !slugs["in2"] {
			t.Fatalf("the set must name in1 and in2, got %v", slugs)
		}
		if o := owners(t, st); o["outer"] != "alpha" {
			t.Fatalf("a refused transfer moved nothing: %v", o)
		}
	})
	t.Run("named together they move together", func(t *testing.T) {
		st, a := setup(t)
		moved, label, err := st.TransferClaims(a.SessionID, a.Incarnation, []string{"outer", "in1", "in2", "in1"}, mustResolve(t, st, "charlie"))
		if err != nil || label != "charlie" || len(moved) != 3 {
			t.Fatalf("moved %+v label %q err %v; want the three, a repeat counted once", moved, label, err)
		}
		for s, o := range owners(t, st) {
			if o != "charlie" {
				t.Fatalf("%s is still %s's", s, o)
			}
		}
	})
	t.Run("one slug that is not the sender's rolls every move back", func(t *testing.T) {
		st, a := setup(t)
		_, _, err := st.TransferClaims(a.SessionID, a.Incarnation, []string{"outer", "in1", "in2", "nope"}, mustResolve(t, st, "charlie"))
		var nr ErrNoRelease
		if !errors.As(err, &nr) || nr.Slug != "nope" {
			t.Fatalf("want ErrNoRelease for nope, got %v", err)
		}
		for s, o := range owners(t, st) {
			if o != "alpha" {
				t.Fatalf("%s moved to %s although the set was refused", s, o)
			}
		}
	})
}
