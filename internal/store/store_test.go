package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// pinnedClock is the injectable-clock idiom: every dated fixture pins it.
type pinnedClock struct{ t time.Time }

func (p *pinnedClock) now() time.Time          { return p.t }
func (p *pinnedClock) advance(d time.Duration) { p.t = p.t.Add(d) }

func openTest(t *testing.T) (*Store, *pinnedClock) {
	t.Helper()
	clk := &pinnedClock{t: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)}
	st, err := Open(filepath.Join(t.TempDir(), "fleet.db"), clk.now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st, clk
}

func hello(t *testing.T, st *Store, id, label, wt string) SessionInfo {
	t.Helper()
	si, err := st.Hello(id, label, wt, 1234)
	if err != nil {
		t.Fatal(err)
	}
	return si
}

func TestClaimOverlapRefusedNamingClaimant(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")

	if err := st.Claim(a.SessionID, a.Incarnation, "edge-cap", "router work", []string{"internal/router"}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		scope string
	}{
		{"exact", "internal/router"},
		{"child", "internal/router/proxy.go"},
		{"parent", "internal"},
		{"case-fold", "Internal/Router"},
		{"trailing-slash", "internal/router/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := st.Claim(b.SessionID, b.Incarnation, "other-"+tc.name, "x", []string{tc.scope})
			var refused ErrRefused
			if !errors.As(err, &refused) {
				t.Fatalf("scope %q: want ErrRefused, got %v", tc.scope, err)
			}
			if refused.Claimant != "alpha" {
				t.Fatalf("refusal must name the claimant label, got %q", refused.Claimant)
			}
		})
	}

	// Same slug, different scope: refused naming claimant.
	err := st.Claim(b.SessionID, b.Incarnation, "edge-cap", "x", []string{"docs"})
	var refused ErrRefused
	if !errors.As(err, &refused) || refused.Claimant != "alpha" {
		t.Fatalf("slug conflict must refuse naming alpha, got %v", err)
	}

	// Disjoint scope: allowed.
	if err := st.Claim(b.SessionID, b.Incarnation, "docs-pass", "docs", []string{"docs"}); err != nil {
		t.Fatalf("disjoint claim should succeed: %v", err)
	}
}

func TestClaimIsAtomicAcrossScopes(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.Claim(a.SessionID, a.Incarnation, "held", "x", []string{"pkg/two"}); err != nil {
		t.Fatal(err)
	}
	// Second scope collides → NOTHING from the multi-scope claim may persist.
	err := st.Claim(b.SessionID, b.Incarnation, "multi", "x", []string{"pkg/one", "pkg/two"})
	if err == nil {
		t.Fatal("want refusal")
	}
	if _, held, _ := st.OwnerOf("pkg/one/f.go", "nobody"); held {
		t.Fatal("partial acquisition: pkg/one persisted after refused multi-scope claim")
	}
}

func TestClaimRequiresLiveIncarnation(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.Bye(a.SessionID, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Claim(a.SessionID, a.Incarnation, "zombie", "x", []string{"pkg"}); err == nil {
		t.Fatal("ended incarnation must not claim")
	}
	// Re-hello mints a new incarnation; the OLD token still must not claim.
	a2 := hello(t, st, "sess-a", "", "/wt/a")
	if a2.Incarnation == a.Incarnation {
		t.Fatal("re-hello after bye must mint a fresh incarnation")
	}
	if err := st.Claim(a.SessionID, a.Incarnation, "zombie", "x", []string{"pkg"}); err == nil {
		t.Fatal("stale incarnation token must not claim")
	}
	if err := st.Claim(a2.SessionID, a2.Incarnation, "fresh", "x", []string{"pkg"}); err != nil {
		t.Fatal(err)
	}
}

func TestSweepNeverReapsOpenClaimsOfLiveSessions(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.Claim(a.SessionID, a.Incarnation, "old-but-live", "x", []string{"pkg"}); err != nil {
		t.Fatal(err)
	}
	clk.advance(90 * 24 * time.Hour) // far past any TTL

	orphaned, deleted, err := st.Sweep(24*time.Hour, 24*time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	if orphaned != 0 || deleted != 0 {
		t.Fatalf("plain sweep touched a live session's open claim: orphaned=%d deleted=%d", orphaned, deleted)
	}
	claims, _ := st.Claims(false)
	if len(claims) != 1 || claims[0].State != "open" {
		t.Fatalf("open claim of live session must survive any age: %+v", claims)
	}
	if !claims[0].Stale(clk.now()) {
		t.Fatal("it must however show STALE")
	}
}

func TestSweepLifecycle(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.Claim(a.SessionID, a.Incarnation, "done", "x", []string{"pkg/a"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Claim(b.SessionID, b.Incarnation, "abandoned", "x", []string{"pkg/b"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Release(a.SessionID, "done"); err != nil {
		t.Fatal(err)
	}
	if err := st.Bye(b.SessionID, ""); err != nil {
		t.Fatal(err)
	}

	// First sweep: bye already orphaned b's claim; released row too young to delete.
	orphaned, deleted, err := st.Sweep(24*time.Hour, 24*time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Fatalf("young closed rows must be kept: deleted=%d", deleted)
	}
	_ = orphaned

	clk.advance(25 * time.Hour)
	_, deleted, err = st.Sweep(24*time.Hour, 24*time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("aged released+orphaned rows should be deleted, got %d", deleted)
	}
}

func TestSweepForceOrphansSilentSessions(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.Claim(a.SessionID, a.Incarnation, "silent", "x", []string{"pkg"}); err != nil {
		t.Fatal(err)
	}
	clk.advance(25 * time.Hour)
	orphaned, _, err := st.Sweep(24*time.Hour, 24*time.Hour, true)
	if err != nil {
		t.Fatal(err)
	}
	if orphaned != 1 {
		t.Fatalf("force sweep should orphan the silent session's claim, got %d", orphaned)
	}
	// But a session heard from recently is untouched even under force.
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.Claim(b.SessionID, b.Incarnation, "fresh", "x", []string{"pkg2"}); err != nil {
		t.Fatal(err)
	}
	orphaned, _, err = st.Sweep(24*time.Hour, 24*time.Hour, true)
	if err != nil {
		t.Fatal(err)
	}
	if orphaned != 0 {
		t.Fatal("force sweep must not orphan a recently-seen session's claim")
	}
}

func TestBeatRenewsOnlyCoveringClaims(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.Claim(a.SessionID, a.Incarnation, "router", "x", []string{"internal/router"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Claim(a.SessionID, a.Incarnation, "docs", "x", []string{"docs"}); err != nil {
		t.Fatal(err)
	}
	clk.advance(20 * time.Minute)
	if err := st.Beat(a.SessionID, "internal/router/proxy.go"); err != nil {
		t.Fatal(err)
	}
	claims, _ := st.Claims(false)
	for _, c := range claims {
		switch c.Slug {
		case "router":
			if c.Renewed != clk.now() && !c.Renewed.Equal(clk.now()) {
				t.Fatalf("covering claim not renewed: %v vs %v", c.Renewed, clk.now())
			}
		case "docs":
			if c.Renewed.Equal(clk.now()) {
				t.Fatal("non-covering claim must NOT be renewed by an unrelated path")
			}
		}
	}
}

func TestBeatCannotResurrectEndedSession(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.Bye(a.SessionID, ""); err != nil {
		t.Fatal(err)
	}
	clk.advance(time.Minute)
	if err := st.Beat(a.SessionID, ""); err != nil {
		t.Fatal(err)
	}
	sessions, _ := st.Sessions()
	if sessions[0].Live() {
		t.Fatal("a delayed beat resurrected an ended session")
	}
}

func TestDelayedByeCannotEndNewIncarnation(t *testing.T) {
	st, _ := openTest(t)
	a1 := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.Bye(a1.SessionID, a1.Incarnation); err != nil {
		t.Fatal(err)
	}
	a2 := hello(t, st, "sess-a", "", "/wt/a")
	// The old process's delayed bye arrives with the OLD incarnation.
	if err := st.Bye(a1.SessionID, a1.Incarnation); err != nil {
		t.Fatal(err)
	}
	sessions, _ := st.Sessions()
	if !sessions[0].Live() {
		t.Fatal("delayed bye from a dead incarnation ended the live one")
	}
	_ = a2
}

func TestInboxAtLeastOnceAndBroadcast(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")

	if err := st.Msg(a.SessionID, "operator", "direct to a"); err != nil {
		t.Fatal(err)
	}
	if err := st.Msg("bravo", "operator", "by label to b"); err != nil {
		t.Fatal(err)
	}
	if err := st.Msg("all", "operator", "everyone"); err != nil {
		t.Fatal(err)
	}

	am, err := st.Undelivered(a.SessionID, a.Label)
	if err != nil || len(am) != 2 {
		t.Fatalf("a should see direct+broadcast, got %v (%v)", am, err)
	}
	// Simulate a crash between read and mark: nothing marked → same result again.
	am2, _ := st.Undelivered(a.SessionID, a.Label)
	if len(am2) != 2 {
		t.Fatal("unmarked messages must be redelivered")
	}
	if err := st.MarkDelivered(a.SessionID, []int64{am[0].ID, am[1].ID}); err != nil {
		t.Fatal(err)
	}
	if am3, _ := st.Undelivered(a.SessionID, a.Label); len(am3) != 0 {
		t.Fatal("delivered messages must not repeat")
	}
	// b still sees its label-addressed + the broadcast, independent of a's marks.
	bm, _ := st.Undelivered(b.SessionID, b.Label)
	if len(bm) != 2 {
		t.Fatalf("b should see label+broadcast regardless of a's delivery, got %d", len(bm))
	}
}

func TestPauseTargetsAndResume(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	for _, target := range []string{a.SessionID, "alpha", "all"} {
		if err := st.Pause(target, "hold on"); err != nil {
			t.Fatal(err)
		}
		if _, paused, _ := st.PausedFor(a.SessionID, a.Label); !paused {
			t.Fatalf("pause target %q did not pause the session", target)
		}
		if _, err := st.Resume(target); err != nil {
			t.Fatal(err)
		}
		if _, paused, _ := st.PausedFor(a.SessionID, a.Label); paused {
			t.Fatalf("resume %q did not clear", target)
		}
	}
}

func TestNormalizeScope(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{"internal/router", "internal/router", false},
		{"internal/router/", "internal/router", false},
		{"./internal/router", "internal/router", false},
		{"internal//router", "internal/router", false},
		{"internal\\router", "internal/router", false},
		{"/abs/path", "", true},
		{"../escape", "", true},
		{"a/../../escape", "", true},
		{".", "", true},
		{"", "", true},
		{"a/./b", "a/b", false},
	}
	for _, tc := range cases {
		got, err := NormalizeScope(tc.in)
		if tc.wantErr != (err != nil) {
			t.Errorf("NormalizeScope(%q): err=%v, wantErr=%v", tc.in, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && got != tc.want {
			t.Errorf("NormalizeScope(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestByeDoesNotOrphanUntilHelloOrSweep(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.Claim(a.SessionID, a.Incarnation, "w", "x", []string{"pkg"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Bye(a.SessionID, ""); err != nil {
		t.Fatal(err)
	}
	// Protection persists after bye: the claim is still open (conservative).
	if _, held, _ := st.OwnerOf("pkg/f.go", "someone-else"); !held {
		t.Fatal("bye must not drop protection before hello/sweep orphan the claim")
	}
	// Any session's hello performs the orphaning housekeeping.
	hello(t, st, "sess-b", "bravo", "/wt/b")
	claims, _ := st.Claims(true)
	for _, c := range claims {
		if c.Slug == "w" && c.State != "orphaned" {
			t.Fatalf("hello should orphan ended sessions' claims, state=%s", c.State)
		}
	}
}

func TestFreshOrphanIsNotDeletedInSameSweep(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.Claim(a.SessionID, a.Incarnation, "w", "x", []string{"pkg"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Bye(a.SessionID, ""); err != nil {
		t.Fatal(err)
	}
	clk.advance(25 * time.Hour) // claim is old, but its ORPHANING is new
	orphaned, deleted, err := st.Sweep(24*time.Hour, 24*time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	if orphaned != 1 || deleted != 0 {
		t.Fatalf("orphan and delete must not happen in one pass: orphaned=%d deleted=%d", orphaned, deleted)
	}
	clk.advance(25 * time.Hour)
	_, deleted, _ = st.Sweep(24*time.Hour, 24*time.Hour, false)
	if deleted != 1 {
		t.Fatalf("aged orphan should be deleted on the later pass, got %d", deleted)
	}
}

func TestSweepRejectsNonPositiveTTL(t *testing.T) {
	st, _ := openTest(t)
	if _, _, err := st.Sweep(0, 24*time.Hour, false); err == nil {
		t.Fatal("ttl=0 must be rejected")
	}
	if _, _, err := st.Sweep(24*time.Hour, -time.Hour, false); err == nil {
		t.Fatal("negative forceAfter must be rejected")
	}
}

func TestSweepGCsAgedInbox(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.Msg("all", "operator", "old news"); err != nil {
		t.Fatal(err)
	}
	clk.advance(25 * time.Hour)
	if _, _, err := st.Sweep(24*time.Hour, 24*time.Hour, false); err != nil {
		t.Fatal(err)
	}
	if msgs, _ := st.Undelivered(a.SessionID, a.Label); len(msgs) != 0 {
		t.Fatalf("aged inbox rows must be GCed by sweep, got %d", len(msgs))
	}
}

func TestClaimsReportsTakingIncarnation(t *testing.T) {
	st, _ := openTest(t)
	a1 := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.Claim(a1.SessionID, a1.Incarnation, "w", "x", []string{"pkg"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Bye(a1.SessionID, ""); err != nil {
		t.Fatal(err)
	}
	a2 := hello(t, st, "sess-a", "", "/wt/a") // reincarnate; hello orphans the old claim
	claims, _ := st.Claims(true)
	if len(claims) != 1 {
		t.Fatalf("want 1 claim, got %d", len(claims))
	}
	if claims[0].Incarnation != a1.Incarnation {
		t.Fatalf("claim must report the incarnation that took it (%s), not the current one (%s)",
			a1.Incarnation, a2.Incarnation)
	}
}
