package store

import (
	"errors"
	"testing"
	"time"
)

// F-26, Codex's stricter point 2: an adapter's renewal of claims covering a
// completed write can be applied AFTER its turn's idle was recorded (the
// renewal mailbox is drained off the native reader). Routed through the only
// claim-renewing logical event, BeatLogical, that delayed batch ran
// logicalBusy and erased the idle observation: the roster then said the
// worker was mid-turn while it waited for its coordinator. Reproduced against
// BeatLogical before RenewLogical existed; BeatLogical stays here as the
// positive control proving the fixture's idle row is erasable at all.
func TestLogicalRenewalAfterIdleKeepsIdle(t *testing.T) {
	for _, route := range []string{"renew", "beat"} {
		t.Run(route, func(t *testing.T) {
			st, clk := openTest(t)
			si := beginLogical(t, st, "logical", "")
			for _, c := range [][2]string{{"covered", "pkg"}, {"control", "other"}} {
				if err := st.Claim(si.SessionID, si.Incarnation, c[0], "", []string{c[1]}); err != nil {
					t.Fatal(err)
				}
			}
			clk.advance(time.Minute)
			if err := st.IdleLogical(si.SessionID, si.Incarnation, time.Time{}); err != nil {
				t.Fatal(err)
			}
			seen := logicalInfo(t, st, si.SessionID).LastSeen
			clk.advance(time.Minute)
			switch route {
			case "renew":
				n, err := st.RenewLogical(si.SessionID, si.Incarnation, "", "pkg/a.go", "PKG/b.go")
				if err != nil || n != 1 {
					t.Fatalf("renewal: n=%d err=%v, want the one covering claim", n, err)
				}
			case "beat":
				if err := st.BeatLogical(si.SessionID, si.Incarnation, "pkg/a.go"); err != nil {
					t.Fatal(err)
				}
			}
			idle, err := st.IdleSessions()
			if err != nil {
				t.Fatal(err)
			}
			_, stillIdle := idle[si.SessionID]
			if stillIdle != (route == "renew") {
				t.Fatalf("%s after idle: idle row present=%v, want %v", route, stillIdle, route == "renew")
			}
			if route == "renew" && !logicalInfo(t, st, si.SessionID).LastSeen.Equal(seen) {
				t.Fatal("a renewal is not a lifecycle event and must not move last_seen")
			}
			claims, err := st.Claims(false)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range claims {
				want := clk.now().Add(-2 * time.Minute)
				if c.Slug == "covered" {
					want = clk.now()
				}
				if !c.Renewed.Equal(want) {
					t.Fatalf("%s: claim %s renewed %v, want %v", route, c.Slug, c.Renewed, want)
				}
			}
		})
	}
}

// The batch carries the incarnation captured at registration. Queued under I1
// and applied after the session was ended and begun again as I2, it must renew
// nothing: not I1's orphaned claim and not I2's own claim on the same path.
// The current-token positive control proves the fixture's claim renews.
func TestRenewLogicalRefusesAnythingButTheCapturedLiveIncarnation(t *testing.T) {
	st, clk := openTest(t)
	first := beginLogical(t, st, "logical", "")
	if _, err := st.RenewLogical(first.SessionID, "", "pkg/a.go"); !errors.Is(err, ErrLogicalIdentity) {
		t.Fatalf("empty token: want identity refusal, got %v", err)
	}
	if err := st.EndLogical(first.SessionID, first.Incarnation); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RenewLogical(first.SessionID, first.Incarnation, "pkg/a.go"); !errors.Is(err, ErrLogicalIdentity) {
		t.Fatalf("ended incarnation: want identity refusal, got %v", err)
	}
	second := beginLogical(t, st, first.SessionID, first.Incarnation)
	if err := st.Claim(second.SessionID, second.Incarnation, "successor-work", "", []string{"pkg"}); err != nil {
		t.Fatal(err)
	}
	claimed := clk.now()
	clk.advance(time.Minute)
	if n, err := st.RenewLogical(first.SessionID, first.Incarnation, "pkg/a.go"); !errors.Is(err, ErrLogicalIdentity) || n != 0 {
		t.Fatalf("predecessor's batch: n=%d err=%v, want identity refusal", n, err)
	}
	claims, err := st.Claims(false)
	if err != nil || len(claims) != 1 || !claims[0].Renewed.Equal(claimed) {
		t.Fatalf("predecessor's batch renewed the successor's claim: %+v %v", claims, err)
	}
	if n, err := st.RenewLogical(second.SessionID, second.Incarnation, "pkg/a.go"); err != nil || n != 1 {
		t.Fatalf("current-token positive control: n=%d err=%v", n, err)
	}
}
