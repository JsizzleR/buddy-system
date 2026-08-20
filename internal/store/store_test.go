package store

import (
	"errors"
	"path/filepath"
	"strings"
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
	if err := st.Release(a.SessionID, a.Incarnation, "done"); err != nil {
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

func TestHelloAfterByeOrphansOldIncarnationClaims(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.Claim(a.SessionID, a.Incarnation, "fix-auth", "auth work", []string{"src"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Bye(a.SessionID, ""); err != nil {
		t.Fatal(err)
	}
	// Revive the same session id: the old incarnation's claim must be
	// orphaned NOW — after the revive clears ended, orphanEnded can no
	// longer see it, and it would sit open-but-unreleasable forever.
	a2 := hello(t, st, "sess-a", "alpha", "/wt/a")
	if a2.Incarnation == a.Incarnation {
		t.Fatal("revive must mint a new incarnation")
	}
	claims, err := st.Claims(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range claims {
		if c.Slug == "fix-auth" && c.State != "orphaned" {
			t.Fatalf("old-incarnation claim must be orphaned on revive, got state=%q", c.State)
		}
	}
	// And the scope is actually free again for everyone.
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.Claim(b.SessionID, b.Incarnation, "takeover", "x", []string{"src"}); err != nil {
		t.Fatalf("scope still blocked after its owner's revive: %v", err)
	}
}

// Release is fenced by incarnation, as Claim is. A session that byes, re-hellos
// and re-takes the same slug holds a NEW claim; a release resolved against the
// previous incarnation must not hand it back. (An id-only release is an ABA:
// identity is resolved before the mutation, and the session can turn over in
// between.)
func TestReleaseIsFencedByIncarnation(t *testing.T) {
	st, _ := openTest(t)
	a1 := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.Claim(a1.SessionID, a1.Incarnation, "w", "d", []string{"pkg"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Bye("sess-a", ""); err != nil {
		t.Fatal(err)
	}
	a2 := hello(t, st, "sess-a", "alpha", "/wt/a") // ended -> fresh incarnation
	if a2.Incarnation == a1.Incarnation {
		t.Fatal("hello over an ended session must mint a new incarnation")
	}
	if err := st.Claim(a2.SessionID, a2.Incarnation, "w", "d", []string{"pkg"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Release("sess-a", a1.Incarnation, "w"); err == nil {
		t.Fatal("a release resolved against the OLD incarnation must not release the new claim")
	}
	if err := st.Release("sess-a", a2.Incarnation, "w"); err != nil {
		t.Fatalf("the current incarnation must be able to release its own claim: %v", err)
	}
}

// A failed release must say WHICH failure it was. The four outcomes need
// different next actions -- "you already released it" and "someone else holds
// it" are opposites -- and the message they shared could not tell them apart.
func TestReleaseDiagnosesWhyItDidNothing(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")

	// (1) no such slug
	var e ErrNoRelease
	if err := st.Release(a.SessionID, a.Incarnation, "never-existed"); !errors.As(err, &e) || e.Exists {
		t.Fatalf("want ErrNoRelease{Exists:false}, got %v", err)
	}
	if !strings.Contains(e.Error(), "no claim") || !strings.Contains(e.Error(), "ls --all") {
		t.Fatalf("an unknown slug should say so and point at closed claims: %s", e.Error())
	}

	// (2) already released -- must report the state AND how long ago, not
	// "held by someone else"
	if err := st.Claim(a.SessionID, a.Incarnation, "mine", "d", []string{"pkg/a"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Release(a.SessionID, a.Incarnation, "mine"); err != nil {
		t.Fatal(err)
	}
	clk.advance(22 * time.Minute)
	err := st.Release(a.SessionID, a.Incarnation, "mine")
	if !errors.As(err, &e) || e.State != "released" || !e.Yours {
		t.Fatalf("want ErrNoRelease{State:released,Yours:true}, got %+v (%v)", e, err)
	}
	for _, want := range []string{"already released", "22m ago", "yours"} {
		if !strings.Contains(e.Error(), want) {
			t.Fatalf("a released claim must report state and age (missing %q): %s", want, e.Error())
		}
	}
	if strings.Contains(e.Error(), "held by") {
		t.Fatalf("a claim you already released must not read as someone else's: %s", e.Error())
	}

	// (3) open, but held by another session -- names them and a remedy
	if err := st.Claim(b.SessionID, b.Incarnation, "theirs", "d", []string{"pkg/b"}); err != nil {
		t.Fatal(err)
	}
	err = st.Release(a.SessionID, a.Incarnation, "theirs")
	if !errors.As(err, &e) || e.State != "open" || e.Yours {
		t.Fatalf("want ErrNoRelease{State:open,Yours:false}, got %+v (%v)", e, err)
	}
	for _, want := range []string{"held by bravo", "sweep --force"} {
		if !strings.Contains(e.Error(), want) {
			t.Fatalf("another session's claim must name them and a remedy (missing %q): %s", want, e.Error())
		}
	}

	// (4) open under an earlier incarnation of the SAME session
	if err := st.Claim(a.SessionID, a.Incarnation, "stale-inc", "d", []string{"pkg/c"}); err != nil {
		t.Fatal(err)
	}
	a2 := hello(t, st, "sess-a", "alpha", "/wt/a") // live hello: incarnation preserved
	if a2.Incarnation != a.Incarnation {
		t.Fatal("hello over a LIVE session must preserve the incarnation")
	}
	err = st.Release(a.SessionID, "some-other-incarnation", "stale-inc")
	if !errors.As(err, &e) || !e.Yours || e.State != "open" {
		t.Fatalf("want ErrNoRelease{State:open,Yours:true}, got %+v (%v)", e, err)
	}
	if !strings.Contains(e.Error(), "EARLIER incarnation") {
		t.Fatalf("an incarnation mismatch must say so, not blame another session: %s", e.Error())
	}
}

func TestDefaultLabelNamesWorktreeAndSession(t *testing.T) {
	st, _ := openTest(t)
	si := hello2(t, st, "abcd-1234-eeee", "", "/Users/x/Projects/buddy-system")
	if si.Label != "buddy-system/s-abcd1234" {
		t.Fatalf("default label should say where and who: got %q", si.Label)
	}
	// Explicit labels are untouched, and labels are stable across refreshes
	// even if the worktree moves (they are pause/msg targets).
	si2 := hello2(t, st, "abcd-1234-eeee", "", "/elsewhere/wt")
	if si2.Label != si.Label {
		t.Fatalf("label drifted on refresh: %q -> %q", si.Label, si2.Label)
	}
	named := hello2(t, st, "other-session", "alpha", "/Users/x/Projects/buddy-system")
	if named.Label != "alpha" {
		t.Fatalf("explicit label overridden: %q", named.Label)
	}
}

func hello2(t *testing.T, st *Store, id, label, wt string) SessionInfo {
	t.Helper()
	si, err := st.Hello(id, label, wt, 1234)
	if err != nil {
		t.Fatal(err)
	}
	return si
}
