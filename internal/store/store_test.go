package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
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

// mustResolve is the test-side door onto ResolveTarget. Pause/Resume/Msg take a
// resolved Target so the compiler stops an unresolved string reaching a row;
// these tests go through the same door the CLI does.
func mustResolve(t *testing.T, st *Store, target string) Target {
	t.Helper()
	tgt, err := st.ResolveTarget(target)
	if err != nil {
		t.Fatalf("ResolveTarget(%q): %v", target, err)
	}
	return tgt
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
	sessions, _ := st.Sessions(ByLastSeen)
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
	sessions, _ := st.Sessions(ByLastSeen)
	if !sessions[0].Live() {
		t.Fatal("delayed bye from a dead incarnation ended the live one")
	}
	_ = a2
}

func TestInboxAtLeastOnceAndBroadcast(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")

	if err := st.Msg(mustResolve(t, st, a.SessionID), "operator", "direct to a"); err != nil {
		t.Fatal(err)
	}
	if err := st.Msg(mustResolve(t, st, "bravo"), "operator", "by label to b"); err != nil {
		t.Fatal(err)
	}
	if err := st.Msg(mustResolve(t, st, "all"), "operator", "everyone"); err != nil {
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

	// "all" means the fleet that existed when the message was sent. A later
	// session must not inherit standing context from a historical broadcast.
	c := hello(t, st, "sess-c", "charlie", "/wt/c")
	if cm, err := st.Undelivered(c.SessionID, c.Label); err != nil || len(cm) != 0 {
		t.Fatalf("future session inherited a broadcast: %v (%v)", cm, err)
	}
}

func TestBroadcastSnapshotsOnlyLiveRecipients(t *testing.T) {
	st, _ := openTest(t)
	live := hello(t, st, "sess-live", "live", "/wt/live")
	ended := hello(t, st, "sess-ended", "ended", "/wt/ended")
	if err := st.Bye(ended.SessionID, ended.Incarnation); err != nil {
		t.Fatal(err)
	}
	if err := st.Msg(mustResolve(t, st, "all"), "operator", "current fleet only"); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Undelivered(live.SessionID, live.Label); len(got) != 1 {
		t.Fatalf("live recipient missed broadcast: %v", got)
	}
	revived := hello(t, st, ended.SessionID, ended.Label, ended.Worktree)
	if got, _ := st.Undelivered(revived.SessionID, revived.Label); len(got) != 0 {
		t.Fatalf("session revived after broadcast inherited it: %v", got)
	}
}

func TestBroadcastExpiresWithoutDependingOnSweep(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.Msg(mustResolve(t, st, "all"), "operator", "time-sensitive"); err != nil {
		t.Fatal(err)
	}
	clk.advance(BroadcastKeep + time.Second)
	if got, err := st.Undelivered(a.SessionID, a.Label); err != nil || len(got) != 0 {
		t.Fatalf("expired broadcast remained deliverable: %v (%v)", got, err)
	}
	// Direct interjections keep their existing durable semantics.
	if err := st.Msg(mustResolve(t, st, a.SessionID), "operator", "still relevant"); err != nil {
		t.Fatal(err)
	}
	clk.advance(BroadcastKeep + time.Second)
	if got, err := st.Undelivered(a.SessionID, a.Label); err != nil || len(got) != 1 {
		t.Fatalf("direct message expired with broadcasts: %v (%v)", got, err)
	}
}

func TestLegacyBroadcastWithoutAudienceIsInert(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if _, err := st.db.Exec(`INSERT INTO inbox (target, sender, body, created) VALUES ('all','operator','legacy',?)`, st.now().Unix()); err != nil {
		t.Fatal(err)
	}
	if got, err := st.Undelivered(a.SessionID, a.Label); err != nil || len(got) != 0 {
		t.Fatalf("pre-migration broadcast without a recipient snapshot was replayed: %v (%v)", got, err)
	}
}

func TestRecipientMigrationReconstructsOnlyTheAudienceAtSendTime(t *testing.T) {
	clk := &pinnedClock{t: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)}
	path := filepath.Join(t.TempDir(), "legacy.db")
	st, err := Open(path, clk.now)
	if err != nil {
		t.Fatal(err)
	}
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if _, err := st.db.Exec(`DROP TABLE inbox_recipient`); err != nil {
		t.Fatal(err)
	}
	// A ledger from before the marker table is also from before the version
	// stamp: it reads user_version 0. Without resetting it here the fixture is
	// a CURRENT ledger with a table missing, which Open rightly never repairs.
	if _, err := st.db.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`INSERT INTO inbox (target, sender, body, created) VALUES ('all','operator','legacy',?)`, clk.now().Unix()); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = Open(path, clk.now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	var recipients int
	if err := st.db.QueryRow(`SELECT count(*) FROM inbox_recipient`).Scan(&recipients); err != nil || recipients != 1 {
		t.Fatalf("migration must commit its marker and backfill together: recipients=%d err=%v", recipients, err)
	}
	if got, _ := st.Undelivered(a.SessionID, a.Label); len(got) != 1 {
		t.Fatalf("migration lost the session live at send time: %v", got)
	}
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if got, _ := st.Undelivered(b.SessionID, b.Label); len(got) != 0 {
		t.Fatalf("migration admitted a future session: %v", got)
	}
}

func TestPauseTargetsAndResume(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	for _, target := range []string{a.SessionID, "alpha", "all"} {
		if err := st.Pause(mustResolve(t, st, target), "hold on"); err != nil {
			t.Fatal(err)
		}
		if _, paused, _ := st.PausedFor(a.SessionID, a.Label); !paused {
			t.Fatalf("pause target %q did not pause the session", target)
		}
		if _, err := st.Resume(mustResolve(t, st, target)); err != nil {
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
	if err := st.Msg(mustResolve(t, st, "all"), "operator", "old news"); err != nil {
		t.Fatal(err)
	}
	clk.advance(25 * time.Hour)
	if _, _, err := st.Sweep(24*time.Hour, 24*time.Hour, false); err != nil {
		t.Fatal(err)
	}
	if msgs, _ := st.Undelivered(a.SessionID, a.Label); len(msgs) != 0 {
		t.Fatalf("aged inbox rows must be GCed by sweep, got %d", len(msgs))
	}
	var recipients int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM inbox_recipient`).Scan(&recipients); err != nil || recipients != 0 {
		t.Fatalf("sweep left broadcast recipients: count=%d err=%v", recipients, err)
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
	if !strings.Contains(e.Error(), "DIFFERENT incarnation") {
		t.Fatalf("an incarnation mismatch must say so, not blame another session: %s", e.Error())
	}
	if strings.Contains(e.Error(), "EARLIER") {
		t.Fatalf("it must not name a DIRECTION it cannot know: a release delayed across a bye "+
			"and a hello arrives with the old incarnation while the open claim belongs to the "+
			"NEW one: %s", e.Error())
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

// Open on a ledger already at schemaVersion must not ask for the write lock.
// It used to BEGIN IMMEDIATE for CREATE TABLE IF NOT EXISTS on every open, so
// the read-only gate hook queued behind any peer's write transaction for up to
// busy_timeout — 5000 ms against a 100 ms hook budget. Here a second connection
// holds that lock while Open runs on a third; the positive control proves the
// lock was really held, or "returned promptly" and "never contended" would be
// the same observation.
func TestOpenOnACurrentLedgerTakesNoWriteLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.db")
	st, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	holder, err := sql.Open("sqlite", "file:"+path+"?_txlock=immediate&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	held, err := holder.Begin() // BEGIN IMMEDIATE: the ledger's write lock
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()

	// Positive control: a writer that will not wait is refused right now.
	probe, err := sql.Open("sqlite", "file:"+path+"?_txlock=immediate&_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	if ptx, err := probe.Begin(); err == nil {
		ptx.Rollback()
		t.Fatal("control: the write lock was not actually held, so this test proves nothing")
	}

	start := time.Now()
	st2, err := Open(path, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Open queued behind a peer's write lock and failed after %v: %v", elapsed, err)
	}
	st2.Close()
	if elapsed > time.Second {
		t.Fatalf("Open of a current ledger took %v; it must not touch the write lock", elapsed)
	}
}

// A ledger written before the version stamp existed reads user_version 0 with
// every table present. Open must still run the schema once — that is how a new
// IF NOT EXISTS index reaches an existing ledger — and stamp it, so the next
// open takes the fast path.
func TestOpenMigratesAnUnstampedLedgerAndStampsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	st, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DROP INDEX claim_scopes_claim`, `PRAGMA user_version = 0`} {
		if _, err := st.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	st.Close()

	st, err = Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	var ver int
	if err := st.db.QueryRow(`PRAGMA user_version`).Scan(&ver); err != nil || ver != schemaVersion {
		t.Fatalf("migration must stamp user_version=%d, got %d (err=%v)", schemaVersion, ver, err)
	}
	var one int
	if err := st.db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='index' AND name='claim_scopes_claim'`).Scan(&one); err != nil {
		t.Fatalf("migration must add the index an older ledger lacks: %v", err)
	}
}

// Scopes come back sorted within their claim, attached to THEIR claim, with
// the claims in creation order — the shape the per-claim query produced before
// the scope lookup became one statement grouped in Go.
func TestClaimsScopesAreSortedAndAttachedToTheirOwnClaim(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.Claim(a.SessionID, a.Incarnation, "first", "x", []string{"z/last", "a/first", "m/mid"}); err != nil {
		t.Fatal(err)
	}
	clk.advance(time.Second)
	if err := st.Claim(a.SessionID, a.Incarnation, "second", "x", []string{"q/two", "b/two"}); err != nil {
		t.Fatal(err)
	}
	claims, err := st.Claims(false)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		slug   string
		scopes []string
	}{
		{"first", []string{"a/first", "m/mid", "z/last"}},
		{"second", []string{"b/two", "q/two"}},
	}
	if len(claims) != len(want) {
		t.Fatalf("want %d claims, got %d", len(want), len(claims))
	}
	for i, w := range want {
		if claims[i].Slug != w.slug || !reflect.DeepEqual(claims[i].Scopes, w.scopes) {
			t.Fatalf("claim %d: got %s %v, want %s %v", i, claims[i].Slug, claims[i].Scopes, w.slug, w.scopes)
		}
	}
	// The single-claim path (what the gate uses on a hit) attaches the same way.
	owner, held, err := st.OwnerOf("m/mid/f.go", "someone-else")
	if err != nil || !held || !reflect.DeepEqual(owner.Scopes, want[0].scopes) {
		t.Fatalf("OwnerOf scopes: held=%v %v (err=%v)", held, owner.Scopes, err)
	}
}

// Session and SessionByID share one contract: a live row, an ended row, and no
// row are three different answers, and both verbs must give the same one.
func TestSessionAgreesWithSessionByID(t *testing.T) {
	st, _ := openTest(t)
	live := hello(t, st, "sess-live", "live", "/wt/live")
	ended := hello(t, st, "sess-ended", "ended", "/wt/ended")
	if err := st.Bye(ended.SessionID, ended.Incarnation); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		id       string
		wantOK   bool
		wantLive bool
	}{
		{"live", live.SessionID, true, true},
		{"ended", ended.SessionID, true, false},
		{"unknown", "never-said-hello", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s1, ok1, err1 := st.Session(tc.id)
			s2, ok2, err2 := st.SessionByID(tc.id)
			if err1 != nil || err2 != nil {
				t.Fatalf("errors: %v / %v", err1, err2)
			}
			if ok1 != tc.wantOK || (ok1 && s1.Live() != tc.wantLive) {
				t.Fatalf("Session: ok=%v live=%v, want ok=%v live=%v", ok1, s1.Live(), tc.wantOK, tc.wantLive)
			}
			if ok1 != ok2 || !reflect.DeepEqual(s1, s2) {
				t.Fatalf("Session and SessionByID disagree: (%v,%+v) vs (%v,%+v)", ok1, s1, ok2, s2)
			}
		})
	}
}

// A refusal names the peer's scope AS CLAIMED, not its folded form: the
// operator goes looking for the line `buddy ls` prints, and "src/api" is not
// on it when the peer claimed "Src/API".
func TestRefusalNamesTheScopeAsClaimed(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.Claim(a.SessionID, a.Incarnation, "api", "x", []string{"Src/API"}); err != nil {
		t.Fatal(err)
	}
	err := st.Claim(b.SessionID, b.Incarnation, "under", "x", []string{"src/api/x"})
	var refused ErrRefused
	if !errors.As(err, &refused) {
		t.Fatalf("want ErrRefused, got %v", err)
	}
	if refused.Their != "Src/API" {
		t.Fatalf("refusal must name the scope as claimed (Src/API), got %q", refused.Their)
	}
	if refused.Scope != "src/api/x" {
		t.Fatalf("refusal must name the requested scope as typed, got %q", refused.Scope)
	}
}

// Hello's label rule, over both UPDATE arms: an empty label keeps the stored
// one (labels are pause/msg targets and must not drift), a non-empty one
// replaces it.
func TestHelloLabelKeepsOrReplaces(t *testing.T) {
	cases := []struct {
		name  string
		ended bool
		label string
		want  string
	}{
		{"live refresh keeps", false, "", "alpha"},
		{"live refresh replaces", false, "beta", "beta"},
		{"revive keeps", true, "", "alpha"},
		{"revive replaces", true, "gamma", "gamma"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := openTest(t)
			a := hello(t, st, "sess-a", "alpha", "/wt/a")
			if tc.ended {
				if err := st.Bye(a.SessionID, a.Incarnation); err != nil {
					t.Fatal(err)
				}
			}
			got := hello(t, st, "sess-a", tc.label, "/wt/a")
			if got.Label != tc.want {
				t.Fatalf("label %q after hello(%q): want %q", got.Label, tc.label, tc.want)
			}
			if (got.Incarnation != a.Incarnation) != tc.ended {
				t.Fatalf("incarnation changed=%v, want %v", got.Incarnation != a.Incarnation, tc.ended)
			}
		})
	}
}

// sample is a context observation with the fields these tests care about.
func sample(turn time.Time, prompt int64) ContextSample {
	return ContextSample{Observed: turn, TurnAt: turn, Model: "m", Effort: "xhigh", Prompt: prompt}
}

func TestContextSampleBelongsToOneIncarnation(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.RecordContext(a.SessionID, a.Incarnation, sample(clk.t, 90_000)); err != nil {
		t.Fatal(err)
	}
	got, err := st.ContextSamples()
	if err != nil {
		t.Fatal(err)
	}
	if got["sess-a"].Prompt != 90_000 {
		t.Fatalf("a live session's own observation must read back: %+v", got["sess-a"])
	}

	// A revived session is a new session wearing an old id, and its
	// predecessor's footprint is not its own.
	clk.advance(time.Hour)
	if err := st.Bye("sess-a", a.Incarnation); err != nil {
		t.Fatal(err)
	}
	b := hello(t, st, "sess-a", "alpha", "/wt/a")
	got, err = st.ContextSamples()
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := got["sess-a"]; ok {
		t.Errorf("the previous incarnation's sample must not be reported as this one's: %+v", c)
	}

	// And the fresh incarnation can record its own.
	if err := st.RecordContext("sess-a", b.Incarnation, sample(clk.t, 5_000)); err != nil {
		t.Fatal(err)
	}
	got, _ = st.ContextSamples()
	if got["sess-a"].Prompt != 5_000 {
		t.Errorf("positive control: the revived incarnation records its own, got %+v", got["sess-a"])
	}

	// And the observation of an incarnation that has since been superseded is
	// DROPPED, not re-stamped onto its successor. A beat reads the transcript,
	// its session dies and revives, and the write arrives late.
	if err := st.RecordContext("sess-a", a.Incarnation, sample(clk.t, 77_000)); err != nil {
		t.Fatal(err)
	}
	got, _ = st.ContextSamples()
	if got["sess-a"].Prompt != 5_000 {
		t.Errorf("a late write from the previous incarnation must not land on this one, got %+v",
			got["sess-a"])
	}
	if got["sess-a"].Incarnation != b.Incarnation {
		t.Errorf("a sample must name the incarnation it belongs to, so a caller holding an older "+
			"session row can tell: got %q, want %q", got["sess-a"].Incarnation, b.Incarnation)
	}
}

// TestContextSampleOrdersInsideOneSecond is issue #7. The guard's whole
// purpose is "an older turn never replaces a newer one", and at one-second
// resolution two turns inside one second compared EQUAL, so the older one won.
func TestContextSampleOrdersInsideOneSecond(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	base := clk.t.Truncate(time.Second)
	newer, older := base.Add(900*time.Millisecond), base.Add(100*time.Millisecond)
	if newer.Unix() != older.Unix() {
		t.Fatal("test bug: the two turns must share a whole second, or this proves nothing")
	}
	if err := st.RecordContext(a.SessionID, a.Incarnation, sample(newer, 2000)); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordContext(a.SessionID, a.Incarnation, sample(older, 1000)); err != nil {
		t.Fatal(err)
	}
	got := mustSamples(t, st)["sess-a"]
	if got.Prompt != 2000 {
		t.Errorf("the older turn overwrote the newer one inside a shared second: prompt %d, want 2000", got.Prompt)
	}
	if !got.TurnAt.Equal(newer) {
		t.Errorf("the stored turn time must keep its sub-second part: got %v, want %v", got.TurnAt, newer)
	}
	// Positive control: forward inside the same second still lands.
	if err := st.RecordContext(a.SessionID, a.Incarnation, sample(base.Add(950*time.Millisecond), 3000)); err != nil {
		t.Fatal(err)
	}
	if got := mustSamples(t, st)["sess-a"]; got.Prompt != 3000 {
		t.Errorf("a NEWER turn inside the same second must still land: prompt %d, want 3000", got.Prompt)
	}
}

// TestContextSampleCarriesModelAndEffort: an orchestrator picks partly on what
// the session IS, and an unrecorded effort must stay empty rather than default
// to something that reads as a fact.
func TestContextSampleCarriesModelAndEffort(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	c := sample(clk.t, 1000)
	c.Model, c.Effort = "claude-opus-5", "xhigh"
	if err := st.RecordContext(a.SessionID, a.Incarnation, c); err != nil {
		t.Fatal(err)
	}
	if got := mustSamples(t, st)["sess-a"]; got.Model != "claude-opus-5" || got.Effort != "xhigh" {
		t.Errorf("model and effort must round-trip: %+v", got)
	}
	clk.advance(time.Minute)
	c = sample(clk.t, 1000)
	c.Model, c.Effort = "claude-opus-5", ""
	if err := st.RecordContext(a.SessionID, a.Incarnation, c); err != nil {
		t.Fatal(err)
	}
	if got := mustSamples(t, st)["sess-a"]; got.Effort != "" {
		t.Errorf("an unrecorded effort must stay empty, got %q", got.Effort)
	}
}

func mustSamples(t *testing.T, st *Store) map[string]ContextSample {
	t.Helper()
	got, err := st.ContextSamples()
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestContextSampleNeverGoesBackwardsOrResurrects(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	t1, t2 := clk.t, clk.t.Add(time.Hour)
	if err := st.RecordContext(a.SessionID, a.Incarnation, sample(t2, 90_000)); err != nil {
		t.Fatal(err)
	}
	// Two beats can reach the ledger out of order, and a failed capture
	// leaves the previous tail in place to be re-read.
	if err := st.RecordContext(a.SessionID, a.Incarnation, sample(t1, 10_000)); err != nil {
		t.Fatal(err)
	}
	got, _ := st.ContextSamples()
	if got["sess-a"].Prompt != 90_000 || !got["sess-a"].TurnAt.Equal(t2) {
		t.Errorf("an older turn must not replace a newer one, got %+v", got["sess-a"])
	}

	// A beat that arrives after bye must not write a row for a dead session —
	// the same rule that stops a delayed heartbeat resurrecting one.
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.Bye(b.SessionID, b.Incarnation); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordContext(b.SessionID, b.Incarnation, sample(clk.t, 1_000)); err != nil {
		t.Fatalf("a late sample is a silent no-op, not an error: %v", err)
	}
	if err := st.RecordContext("sess-never-said-hello", "whatever", sample(clk.t, 1_000)); err != nil {
		t.Fatalf("a sample for an unknown session is a silent no-op, not an error: %v", err)
	}
	got, _ = st.ContextSamples()
	if c, ok := got["sess-b"]; ok {
		t.Errorf("an ended session must have no sample recorded: %+v", c)
	}
	if c, ok := got["sess-never-said-hello"]; ok {
		t.Errorf("an unknown session must have no sample recorded: %+v", c)
	}
}

func TestIdleMarkBelongsToOneIncarnationAndOneLiveSession(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.MarkIdle("sess-a", time.Time{}); err != nil {
		t.Fatal(err)
	}
	idle, err := st.IdleSessions()
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := idle["sess-a"]; !ok || got.Incarnation != a.Incarnation || !got.Since.Equal(clk.t) {
		t.Fatalf("a live session's own report must read back with its incarnation and time: %+v", got)
	}

	// A repeated Stop with no tool call between is a NEW turn ending: the
	// session became idle again, later.
	clk.advance(10 * time.Minute)
	if err := st.MarkIdle("sess-a", time.Time{}); err != nil {
		t.Fatal(err)
	}
	idle, _ = st.IdleSessions()
	if !idle["sess-a"].Since.Equal(clk.t) {
		t.Errorf("a second Stop moves `since` forward, got %v want %v", idle["sess-a"].Since, clk.t)
	}

	// The mark does not survive its incarnation. This is the STORE's half of
	// the rule; the renderer compares as well, and either guard alone would
	// hide a break in the other.
	if err := st.Bye("sess-a", a.Incarnation); err != nil {
		t.Fatal(err)
	}
	b := hello(t, st, "sess-a", "alpha", "/wt/a")
	idle, _ = st.IdleSessions()
	if got, ok := idle["sess-a"]; ok {
		t.Errorf("the previous incarnation's idle mark is not this one's: %+v", got)
	}
	if err := st.MarkIdle("sess-a", time.Time{}); err != nil {
		t.Fatal(err)
	}
	idle, _ = st.IdleSessions()
	if got := idle["sess-a"]; got.Incarnation != b.Incarnation {
		t.Errorf("positive control: the revived incarnation can report idle itself, got %+v", got)
	}

	// A mark made while live does not survive the session ending. The row is
	// still tagged with the incarnation that is still the current one, so
	// only the live predicate can hide it — and without it a caller that
	// listed the sessions a moment earlier would print "idle" for a session
	// that has since gone: available, and gone.
	d := hello(t, st, "sess-d", "dee", "/wt/d")
	if err := st.MarkIdle(d.SessionID, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := mustIdle(t, st)["sess-d"]; !ok {
		t.Fatal("positive control: a live session's mark must be there before the bye")
	}
	if err := st.Bye(d.SessionID, d.Incarnation); err != nil {
		t.Fatal(err)
	}
	if got, ok := mustIdle(t, st)["sess-d"]; ok {
		t.Errorf("an ended session is not waiting for work: %+v", got)
	}

	// An ended or unknown session is a silent no-op, like every other late
	// hook — a Stop arriving after bye must not mark a corpse available.
	c := hello(t, st, "sess-c", "cee", "/wt/c")
	if err := st.Bye(c.SessionID, c.Incarnation); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"sess-c", "sess-never-said-hello"} {
		if err := st.MarkIdle(id, time.Time{}); err != nil {
			t.Fatalf("%s: a late idle mark is a no-op, not an error: %v", id, err)
		}
	}
	idle, _ = st.IdleSessions()
	for _, id := range []string{"sess-c", "sess-never-said-hello"} {
		if got, ok := idle[id]; ok {
			t.Errorf("%s: must have no idle mark: %+v", id, got)
		}
		// And no ROW either. The read hides an ended session's mark, so the
		// reader alone cannot tell "never written" from "written and
		// hidden" — and the write guard is the one that stops a Stop
		// arriving after bye from leaving a mark for the next incarnation
		// to inherit the moment it registers.
		if idleRowExists(t, st, id) {
			t.Errorf("%s: a late Stop must write NOTHING, not a hidden row", id)
		}
	}
}

// TestMarkIdleDatesTheTurnAndRefusesAnEarlierOne is issue #11: the hook
// payload names no incarnation and no event time, so a Stop delayed across a
// bye and a hello marked the NEW incarnation idle on the OLD one's turn.
func TestMarkIdleDatesTheTurnAndRefusesAnEarlierOne(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	turnEnded := clk.t
	clk.advance(30 * time.Second) // the hook was slow to start
	if err := st.MarkIdle(a.SessionID, turnEnded); err != nil {
		t.Fatal(err)
	}
	if got := mustIdle(t, st)["sess-a"]; !got.Since.Equal(turnEnded) {
		t.Errorf("since must date the TURN, not the write: got %v, want %v", got.Since, turnEnded)
	}

	// The turn that ended before this incarnation registered is not this
	// incarnation's turn.
	if err := st.Bye(a.SessionID, a.Incarnation); err != nil {
		t.Fatal(err)
	}
	clk.advance(time.Minute)
	b := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.MarkIdle("sess-a", turnEnded); err != nil {
		t.Fatal(err)
	}
	if got, ok := mustIdle(t, st)["sess-a"]; ok {
		t.Errorf("a turn that ended before this incarnation started must be refused: %+v", got)
	}
	// Positive control: this incarnation's own turn lands.
	clk.advance(time.Second)
	if err := st.MarkIdle("sess-a", clk.t); err != nil {
		t.Fatal(err)
	}
	if got := mustIdle(t, st)["sess-a"]; got.Incarnation != b.Incarnation || !got.Since.Equal(clk.t) {
		t.Errorf("this incarnation's own turn must land: %+v", got)
	}

	// With NO event time the check cannot run, and the old behaviour stands:
	// written, dated by the clock. Degrading beats refusing — an unreadable
	// transcript must not turn the feature off.
	clk.advance(time.Minute)
	if err := st.MarkIdle("sess-a", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if got := mustIdle(t, st)["sess-a"]; !got.Since.Equal(clk.t) {
		t.Errorf("a zero event time falls back to the write time: got %v, want %v", got.Since, clk.t)
	}
}

// TestClearIdleRetracts is issue #10's half: the turn that runs no tool at
// all clears nothing, so something else has to.
func TestClearIdleRetracts(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.MarkIdle(a.SessionID, clk.t); err != nil {
		t.Fatal(err)
	}
	if _, ok := mustIdle(t, st)["sess-a"]; !ok {
		t.Fatal("positive control: the mark must be there to be retracted")
	}
	if err := st.ClearIdle("sess-a"); err != nil {
		t.Fatal(err)
	}
	if got, ok := mustIdle(t, st)["sess-a"]; ok {
		t.Errorf("ClearIdle must retract the mark: %+v", got)
	}
	// Retracting what is not there, and for a session that never existed, is
	// a no-op and not an error: a retraction can only ever remove a claim.
	for _, id := range []string{"sess-a", "sess-never"} {
		if err := st.ClearIdle(id); err != nil {
			t.Errorf("ClearIdle(%q): %v", id, err)
		}
	}
}

func mustIdle(t *testing.T, st *Store) map[string]IdleState {
	t.Helper()
	idle, err := st.IdleSessions()
	if err != nil {
		t.Fatal(err)
	}
	return idle
}

// idleRowExists reads the session_idle table directly, past IdleSessions'
// live-and-current-incarnation join: the two guards are independent and a
// test that can only see one of them cannot tell which one is working.
func idleRowExists(t *testing.T, st *Store, sessionID string) bool {
	t.Helper()
	var one int
	err := st.db.QueryRow(`SELECT 1 FROM session_idle WHERE session_id=?`, sessionID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	return true
}

// The cache tiers ride the context row as raw counts and read back as they
// were written; and a ledger from before them (schema 4) is rebuilt with the
// columns, since session_context is the one table a migration may drop.
func TestContextSampleCarriesCacheTiersAcrossTheMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.db")
	clk := &pinnedClock{t: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)}
	st, err := Open(path, clk.now)
	if err != nil {
		t.Fatal(err)
	}
	// Age the ledger to schema 4: the table in its old shape, the version
	// stamped back. Recreated rather than ALTERed, because SQLite's DROP
	// COLUMN rewrites the stored CREATE text and refuses one that carries
	// comments — and this is the shape a real schema-4 ledger has anyway.
	for _, q := range []string{`DROP TABLE session_context`,
		`CREATE TABLE session_context (session_id TEXT PRIMARY KEY, incarnation TEXT NOT NULL,
			observed INTEGER NOT NULL, turn_ms INTEGER NOT NULL, model TEXT NOT NULL,
			effort TEXT NOT NULL DEFAULT '', prompt INTEGER NOT NULL, cache_read INTEGER NOT NULL,
			cache_write INTEGER NOT NULL, output INTEGER NOT NULL, declared_window INTEGER NOT NULL DEFAULT 0)`,
		`PRAGMA user_version = 4`} {
		if _, err := st.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	st.Close()
	st, err = Open(path, clk.now)
	if err != nil {
		t.Fatalf("a schema-4 ledger must open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	c := sample(clk.t, 90_000)
	c.Cache5m, c.Cache1h = 12, 4_119
	if err := st.RecordContext(a.SessionID, a.Incarnation, c); err != nil {
		t.Fatalf("recording on the migrated table: %v", err)
	}
	got := mustSamples(t, st)["sess-a"]
	if got.Cache5m != 12 || got.Cache1h != 4_119 {
		t.Fatalf("tiers must read back as written, got 5m=%d 1h=%d", got.Cache5m, got.Cache1h)
	}
	var ver int
	if err := st.db.QueryRow(`PRAGMA user_version`).Scan(&ver); err != nil || ver != schemaVersion {
		t.Fatalf("stamp: %d (%v)", ver, err)
	}
}

// The tier columns ride the row's ON CONFLICT update, guarded by THEIR clock.
// Two samples of the same turn can carry different tier evidence (a writer
// appended between two reads of the tail), and the row rule alone lets the
// later write win whatever it says.
func TestContextSampleTierIsGuardedByItsOwnClock(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	turn := clk.t
	older, newer := turn.Add(-2*time.Minute), turn.Add(-time.Minute)

	// A changed tier on a newer turn is taken: the update path, not the insert.
	c := sample(turn.Add(-5*time.Minute), 1000)
	c.Cache1h, c.TierAt = 50, turn.Add(-5*time.Minute)
	if err := st.RecordContext(a.SessionID, a.Incarnation, c); err != nil {
		t.Fatal(err)
	}
	c = sample(turn, 2000)
	c.Cache5m, c.TierAt = 20, newer
	if err := st.RecordContext(a.SessionID, a.Incarnation, c); err != nil {
		t.Fatal(err)
	}
	got := mustSamples(t, st)["sess-a"]
	if got.Cache5m != 20 || got.Cache1h != 0 || !got.TierAt.Equal(newer) {
		t.Fatalf("a newer tier must replace on the update path: %+v", got)
	}

	// The SAME turn again, with OLDER tier evidence (a re-read of a tail whose
	// newest writer was appended out of order): the prompt row is rewritten,
	// the tier is not.
	c = sample(turn, 2000)
	c.Cache1h, c.TierAt = 50, older
	if err := st.RecordContext(a.SessionID, a.Incarnation, c); err != nil {
		t.Fatal(err)
	}
	got = mustSamples(t, st)["sess-a"]
	if got.Cache5m != 20 || got.Cache1h != 0 || !got.TierAt.Equal(newer) {
		t.Fatalf("older tier evidence on an equal turn must not regress the tier: %+v", got)
	}

	// A stale turn is refused whole, tier included.
	c = sample(turn.Add(-time.Minute), 3000)
	c.Cache1h, c.TierAt = 99, turn.Add(time.Minute)
	if err := st.RecordContext(a.SessionID, a.Incarnation, c); err != nil {
		t.Fatal(err)
	}
	got = mustSamples(t, st)["sess-a"]
	if got.Prompt != 2000 || got.Cache5m != 20 || got.Cache1h != 0 {
		t.Fatalf("a stale turn must change nothing: %+v", got)
	}

	// A sample with NO tier on a newer turn keeps the stored tier: "unrecorded"
	// is not "gone". (A pure read reports the writer's tier anyway; this is
	// the older-harness shape.)
	c = sample(turn.Add(time.Minute), 4000)
	if err := st.RecordContext(a.SessionID, a.Incarnation, c); err != nil {
		t.Fatal(err)
	}
	got = mustSamples(t, st)["sess-a"]
	if got.Prompt != 4000 || got.Cache5m != 20 || !got.TierAt.Equal(newer) {
		t.Fatalf("a tierless newer sample keeps the tier it cannot contradict: %+v", got)
	}

	// A fresh incarnation takes the new row whole, tier and all, even with
	// older tier evidence.
	clk.advance(time.Hour)
	if err := st.Bye("sess-a", a.Incarnation); err != nil {
		t.Fatal(err)
	}
	b := hello(t, st, "sess-a", "alpha", "/wt/a")
	c = sample(clk.t, 100)
	c.Cache1h, c.TierAt = 7, older
	if err := st.RecordContext("sess-a", b.Incarnation, c); err != nil {
		t.Fatal(err)
	}
	got = mustSamples(t, st)["sess-a"]
	if got.Cache1h != 7 || got.Cache5m != 0 || !got.TierAt.Equal(older) {
		t.Fatalf("a new incarnation's row is its own: %+v", got)
	}
}
