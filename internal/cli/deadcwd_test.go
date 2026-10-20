package cli

// A hook cwd that no longer exists (D-102). Measured in run1 (2026-10-20): a
// lane removed its own linked worktree, which lived INSIDE the main checkout,
// and the gate then denied every Bash call it made, its DONE report included
// ("repo discovery failed: exit status 128"), while a worktree removed from
// BESIDE the checkout was silently allowed. The rule these tests hold the gate
// to: the verdict is what a session whose cwd were an EMPTY DIRECTORY at that
// path gets, except that a relative tool path from it is unplaceable.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// deadLane registers session "sess-l" (label "lane") from a linked worktree
// INSIDE f.repo, then removes the worktree, the measured run1 shape. It
// returns the vanished cwd.
func deadLane(t *testing.T, f *fixture) string {
	t.Helper()
	f.initAndHello(t)
	lane := filepath.Join(f.repo, ".wt", "lane")
	gitIn(t, f.repo, "worktree", "add", "-q", lane)
	if _, errw, code := f.run(t, lane, hookJSON("sess-l", lane, "", ""), "hello", "--label", "lane"); code != 0 {
		t.Fatalf("hello lane: %s", errw)
	}
	gitIn(t, f.repo, "worktree", "remove", lane)
	if _, err := os.Lstat(lane); !os.IsNotExist(err) {
		t.Fatalf("control: the lane's worktree should be gone: %v", err)
	}
	return lane
}

// gateFrom runs the gate the way the harness does: the hook process runs in a
// directory that exists, and the payload names the session's (dead) cwd.
func gateFrom(t *testing.T, f *fixture, stdin string) (string, bool) {
	t.Helper()
	out, _, _ := f.run(t, f.repo, stdin, "gate")
	return decodeDeny(t, out)
}

func TestGateFromAVanishedWorktreeInsideTheCheckout(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	lane := deadLane(t, f)
	mustClaim(t, f, f.repo, "peer", "sess-a", "held.go")

	cases := []struct {
		name   string
		stdin  string
		denied bool
		want   string // a substring of the deny reason
	}{
		// (1) The measured brick: the lane could not even report DONE.
		{"bash with no path", bashHookJSON("sess-l", lane, `buddy msg orc "DONE"`), false, ""},
		// (3) Absolute targets are placed under the checkout the lane's cwd
		// was inside, and its claims apply.
		{"absolute write on a peer's claim", hookJSON("sess-l", lane, "Write", filepath.Join(f.repo, "held.go")), true, "alpha"},
		{"absolute write, unclaimed", hookJSON("sess-l", lane, "Write", filepath.Join(f.repo, "free.go")), false, ""},
		// (4) A relative path from a vanished cwd cannot be placed: the
		// lane's claims were on ITS worktree's files, and <lane>/held.go now
		// places as .wt/lane/held.go in the checkout and reads unclaimed.
		{"relative write", hookJSON("sess-l", lane, "Write", "held.go"), true, "no longer exists"},
	}
	for _, tc := range cases {
		reason, denied := gateFrom(t, f, tc.stdin)
		if denied != tc.denied {
			t.Fatalf("%s: denied=%v, want %v: %q", tc.name, denied, tc.denied, reason)
		}
		if denied && !strings.Contains(reason, tc.want) {
			t.Fatalf("%s: deny reason %q does not say %q", tc.name, reason, tc.want)
		}
		if tc.name == "relative write" && !strings.Contains(reason, lane) {
			t.Fatalf("the deny must name the vanished cwd %s: %q", lane, reason)
		}
	}
}

// (2) The positive control that the HOME ledger was found: the operator's
// brake on the lane still stops a path-less Bash call after its cwd is gone.
func TestPauseStillStopsAVanishedCwd(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	lane := deadLane(t, f)
	if _, errw, code := f.run(t, f.repo, "", "pause", "lane", "--note", "hold"); code != 0 {
		t.Fatalf("pause: %s", errw)
	}
	reason, denied := gateFrom(t, f, bashHookJSON("sess-l", lane, "git push"))
	if !denied || !strings.Contains(reason, "paused") {
		t.Fatalf("a paused lane with a vanished cwd must stay paused: denied=%v %q", denied, reason)
	}
}

// (5) A worktree removed from BESIDE the checkout has no repository above it:
// feature-off, as an empty directory there would be. Targets still find their
// own repository's ledger, and a relative one that lands in it is refused.
func TestGateFromAVanishedWorktreeBesideTheCheckout(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	mustClaim(t, f, f.repo, "peer", "sess-a", "held.go")
	gitIn(t, f.repo, "worktree", "remove", f.wtB)
	plain := t.TempDir()
	cases := []struct {
		name   string
		stdin  string
		denied bool
	}{
		{"bash with no path", bashHookJSON("sess-b", f.wtB, "ls"), false},
		{"absolute write on a claim in the governed repo", hookJSON("sess-b", f.wtB, "Write", filepath.Join(f.repo, "held.go")), true},
		{"absolute write, unclaimed", hookJSON("sess-b", f.wtB, "Write", filepath.Join(f.repo, "free.go")), false},
		{"relative write into the governed repo", hookJSON("sess-b", f.wtB, "Write", "../repo/free.go"), true},
		{"absolute write outside every repo", hookJSON("sess-b", f.wtB, "Write", filepath.Join(plain, "x.go")), false},
	}
	for _, tc := range cases {
		reason, denied := gateFrom(t, f, tc.stdin)
		if denied != tc.denied {
			t.Fatalf("%s: denied=%v, want %v: %q", tc.name, denied, tc.denied, reason)
		}
	}
}

// (6) Only ENOENT takes the fallback. A cwd behind a directory this process
// may not search is not proof of anything, and keeps today's DENY. The
// control is the same path with the permission restored and the directory
// gone, which the fallback allows.
func TestAnUnsearchableCwdStillDenies(t *testing.T) {
	boundedParallel(t)
	if os.Geteuid() == 0 {
		t.Skip("root searches a mode-000 directory")
	}
	f := newFixture(t)
	f.initAndHello(t)
	locked := filepath.Join(f.repo, "locked")
	cwd := filepath.Join(locked, "sub")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	reason, denied := gateFrom(t, f, bashHookJSON("sess-a", cwd, "ls"))
	if !denied || !strings.Contains(reason, "repo discovery failed") {
		t.Fatalf("an EACCES cwd must keep today's deny: denied=%v %q", denied, reason)
	}
	if err := os.Chmod(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(locked); err != nil {
		t.Fatal(err)
	}
	if reason, denied := gateFrom(t, f, bashHookJSON("sess-a", cwd, "ls")); denied {
		t.Fatalf("control: the same cwd, vanished, is discovered from the checkout: %q", reason)
	}
}

// (7) The ancestor's verdict is the verdict, and a deny says why: a refused
// repository WITH a ledger is unreadable (D-044) whether the cwd exists or not.
func TestAVanishedCwdUnderARefusedRepositoryDeniesNamingIt(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	main, _ := refusedRepo(t)
	dead := filepath.Join(main, "gone", "deeper")
	if reason, denied := gateFrom(t, f, bashHookJSON("sess-a", dead, "ls")); denied {
		t.Fatalf("control: a refused repo with NO ledger is feature-off from a vanished cwd too: %q", reason)
	}
	if err := os.WriteFile(filepath.Join(main, ".git", "buddy.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	reason, denied := gateFrom(t, f, bashHookJSON("sess-a", dead, "ls"))
	if !denied {
		t.Fatal("a refused repo with a ledger is unreadable and must deny")
	}
	for _, want := range []string{dead, "no longer exists", main} {
		if !strings.Contains(reason, want) {
			t.Fatalf("the deny must name the vanished cwd and where it was discovered from (%q): %q", want, reason)
		}
	}
}

// A dangling symlink above the vanished cwd is followed, not climbed past:
// an empty directory at <plain>/link/lane would live in the link's target,
// inside the governed repository, whose brake applies. Climbing lexically to
// <plain> read it as feature-off (Codex design pass).
func TestAVanishedCwdThroughADanglingLinkFollowsTheLink(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	plain := t.TempDir()
	link := filepath.Join(plain, "link")
	if err := os.Symlink(filepath.Join(f.repo, "gone"), link); err != nil {
		t.Fatal(err)
	}
	dead := filepath.Join(link, "lane")
	if reason, denied := gateFrom(t, f, bashHookJSON("sess-b", dead, "ls")); denied {
		t.Fatalf("control: unpaused, a path-less call is allowed: %q", reason)
	}
	if _, errw, code := f.run(t, f.repo, "", "pause", "all"); code != 0 {
		t.Fatalf("pause: %s", errw)
	}
	if reason, denied := gateFrom(t, f, bashHookJSON("sess-b", dead, "ls")); !denied || !strings.Contains(reason, "paused") {
		t.Fatalf("the link's target is in the paused repo: denied=%v %q", denied, reason)
	}
}

// git's own discovery knobs make "discover from the ancestor" differ from
// "discover from an empty directory here" (a relative GIT_DIR, a ceiling at the
// ancestor), so with any of them set the fallback stands down and today's
// verdict holds. Not parallel: t.Setenv, and revParse inherits the process
// environment.
func TestTheFallbackStandsDownUnderGitDiscoveryEnv(t *testing.T) {
	f := newFixture(t)
	lane := deadLane(t, f)
	if reason, denied := gateFrom(t, f, bashHookJSON("sess-l", lane, "ls")); denied {
		t.Fatalf("control: with no git env the fallback finds the checkout: %q", reason)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(lane))
	if reason, denied := gateFrom(t, f, bashHookJSON("sess-l", lane, "ls")); !denied || !strings.Contains(reason, "repo discovery failed") {
		t.Fatalf("under a discovery knob the fallback must stand down: denied=%v %q", denied, reason)
	}
}

// The fallback is for hooks. `buddy init` CREATES a ledger where discovery
// lands, and a vanished cwd must not quietly turn the feature on in whatever
// repository encloses it (Codex design pass, Q3).
func TestInitFromAVanishedCwdCreatesNothing(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	dead := filepath.Join(f.repo, "gone")
	if _, _, code := f.run(t, dead, "", "init"); code == 0 {
		t.Fatal("init from a vanished cwd must fail")
	}
	if _, err := os.Lstat(filepath.Join(f.repo, ".git", "buddy.db")); !os.IsNotExist(err) {
		t.Fatalf("init from a vanished cwd created a ledger in the enclosing repo: %v", err)
	}
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatalf("control: init from the checkout works: %s", errw)
	}
}

// beat attributes a dirty path only where it can place it. A relative path
// from a vanished cwd would land as .wt/lane/<p> in the checkout, a file no
// tool wrote; an absolute one is recorded as before.
func TestBeatFromAVanishedCwdRecordsOnlyAbsolutePaths(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	lane := deadLane(t, f)
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-l", lane, "Write", "rel.go"), "beat"); code != 0 {
		t.Fatalf("beat: %s", errw)
	}
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-l", lane, "Write", filepath.Join(f.repo, "abs.go")), "beat"); code != 0 {
		t.Fatalf("beat: %s", errw)
	}
	out, _, _ := f.run(t, f.repo, "", "who", "lane")
	if strings.Contains(out, "rel.go") {
		t.Fatalf("a relative path from a vanished cwd was attributed: %s", out)
	}
	if !strings.Contains(out, "abs.go") {
		t.Fatalf("control: an absolute path is attributed from the checkout's ledger: %s", out)
	}
}

// A Codex PostToolUse renews the claims covering the paths it names (D-076).
// From a vanished cwd a relative path would be placed under the enclosing
// checkout and renew a claim on a file no tool wrote; it renews nothing. The
// control is an absolute path in the same call, which still renews.
func TestLifecycleHookFromAVanishedCwdRenewsOnlyAbsolutePaths(t *testing.T) {
	boundedParallel(t)
	f, current := lifecycleFixture(t)
	for _, c := range [][2]string{{"gone-work", "gone"}, {"abs-work", "abs"}} {
		if _, errw, code := f.run(t, f.repo, "", "claim", c[0], "--session", current.SessionID, "--desc", "work", "--scope", c[1]); code != 0 {
			t.Fatal(errw)
		}
	}
	claimed := f.clock
	f.clock = f.clock.Add(time.Minute)
	dead := filepath.Join(f.repo, "gone")
	// Codex names a patch's files relative to its cwd, so this is the shape
	// a worker whose cwd was removed actually sends.
	for _, p := range []string{"x.go", filepath.Join(canon(f.repo), "abs", "a.go")} {
		lifecycleHookOK(t, f, current.Incarnation, "PostToolUse", map[string]any{
			"cwd": dead, "tool_name": "apply_patch",
			"tool_input": map[string]any{"command": "*** Begin Patch\n*** Update File: " + p + "\n@@\n-a\n+b\n*** End Patch\n"},
		})
	}
	renewed := map[string]time.Time{}
	for _, c := range lifecycleSnapshot(t, lifecycleStore(t, f), current.SessionID).Claims {
		renewed[c.Slug] = c.Renewed
	}
	if !renewed["gone-work"].Equal(claimed) {
		t.Fatalf("a relative path from a vanished cwd renewed a claim: %v", renewed)
	}
	if !renewed["abs-work"].Equal(f.clock) {
		t.Fatalf("control: an absolute path renews its covering claim: %v", renewed)
	}
}

// resolveRepo re-reads both ends after the ancestor answers. A cwd recreated
// in that window has its own verdict (here a .git file that does not parse,
// which denies, D-044); an ancestor that vanished in it is no evidence of
// feature-off. Not parallel: the seam is a package variable.
func TestVanishedCwdRereadsAfterAncestorDiscovery(t *testing.T) {
	f := newFixture(t)
	lane := deadLane(t, f)
	if reason, denied := gateFrom(t, f, bashHookJSON("sess-l", lane, "ls")); denied {
		t.Fatalf("control: with no race the fallback allows: %q", reason)
	}
	cases := []struct {
		name string
		race func()
	}{
		{"cwd recreated as an unreadable repository", func() {
			mustWrite(t, filepath.Join(lane, ".git"), "not a gitdir line\n")
		}},
		{"ancestor vanished", func() {
			if err := os.Rename(filepath.Dir(lane), filepath.Dir(lane)+".moved"); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		afterAncestorDiscovery = tc.race
		reason, denied := gateFrom(t, f, bashHookJSON("sess-l", lane, "ls"))
		afterAncestorDiscovery = nil
		if !denied {
			t.Fatalf("%s: the re-read must keep the deny", tc.name)
		}
		if tc.name == "ancestor vanished" && !strings.Contains(reason, "vanished during discovery") {
			t.Fatalf("%s: %q", tc.name, reason)
		}
		os.RemoveAll(lane)
		os.Rename(filepath.Dir(lane)+".moved", filepath.Dir(lane))
	}
}

// A ledger the ancestor's discovery found but that will not open (here a
// dangling symlink) denies as it would from any cwd, and the deny says the cwd
// is gone and where discovery ran from: that is the half of the cause its
// reader can act on.
func TestAnUnopenableLedgerFromAVanishedCwdNamesIt(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	lane := deadLane(t, f)
	ledger := filepath.Join(f.repo, ".git", "buddy.db")
	for _, sfx := range []string{"", "-wal", "-shm"} {
		os.Remove(ledger + sfx)
	}
	if err := os.Symlink(filepath.Join(f.repo, "nowhere.db"), ledger); err != nil {
		t.Fatal(err)
	}
	reason, denied := gateFrom(t, f, bashHookJSON("sess-l", lane, "ls"))
	if !denied {
		t.Fatal("a dangling ledger symlink is unreadable and must deny")
	}
	for _, want := range []string{"dangling symlink", lane, "no longer exists", "close the pane"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("the deny must say %q: %q", want, reason)
		}
	}
}

// ENOTDIR is not ENOENT: a cwd under a path component that is now a FILE could
// not be an empty directory without removing that file, so today's deny holds.
// The control is the same cwd once the file is gone.
func TestACwdUnderAFileStillDenies(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	file := filepath.Join(f.repo, "was-a-dir")
	mustWrite(t, file, "x")
	cwd := filepath.Join(file, "sub")
	reason, denied := gateFrom(t, f, bashHookJSON("sess-a", cwd, "ls"))
	if !denied || !strings.Contains(reason, "repo discovery failed") {
		t.Fatalf("an ENOTDIR cwd must keep today's deny: denied=%v %q", denied, reason)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if reason, denied := gateFrom(t, f, bashHookJSON("sess-a", cwd, "ls")); denied {
		t.Fatalf("control: the same cwd, vanished, is discovered from the checkout: %q", reason)
	}
}

// A `..` in a dangling link's target is applied after the links before it, as
// the kernel does. With jump -> <repo>/sub and link -> jump/../gone, an empty
// directory at <plain>/link/lane lives in <repo>, whose brake applies;
// collapsing jump/.. lexically first landed in <plain>, feature-off (Codex
// code pass). Both a relative and an absolute target, after an unpaused
// control.
func TestAVanishedCwdThroughALinkWithDotDotResolvesPhysically(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if err := os.Mkdir(filepath.Join(f.repo, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	plain := t.TempDir()
	if err := os.Symlink(filepath.Join(f.repo, "sub"), filepath.Join(plain, "jump")); err != nil {
		t.Fatal(err)
	}
	links := map[string]string{
		"rel": "jump/../gone",
		"abs": plain + "/jump/../gone", // not filepath.Join, which cleans the `..` away
		// A missing component and a `..` out of it: an empty directory there
		// would be created, so the `..` climbs back to plain, then jump.
		"missing": "nothere/../jump/gone",
	}
	// And a `..` that climbs physically OUT of the repo: jump/../.. is the
	// directory holding the fixture's repo, which no repository governs, so
	// the cwd is allowed even while the repo is paused. Ignoring the `..`
	// would leave it in <repo>/sub.
	out := filepath.Join(plain, "out")
	if err := os.Symlink("jump/../../gone", out); err != nil {
		t.Fatal(err)
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(plain, name)); err != nil {
			t.Fatal(err)
		}
		if reason, denied := gateFrom(t, f, bashHookJSON("sess-b", filepath.Join(plain, name, "lane"), "ls")); denied {
			t.Fatalf("control (%s): unpaused, a path-less call is allowed: %q", name, reason)
		}
	}
	if _, errw, code := f.run(t, f.repo, "", "pause", "all"); code != 0 {
		t.Fatalf("pause: %s", errw)
	}
	for name := range links {
		reason, denied := gateFrom(t, f, bashHookJSON("sess-b", filepath.Join(plain, name, "lane"), "ls"))
		if !denied || !strings.Contains(reason, "paused") {
			t.Fatalf("%s: the link's target resolves into the paused repo: denied=%v %q", name, denied, reason)
		}
	}
	if reason, denied := gateFrom(t, f, bashHookJSON("sess-b", filepath.Join(out, "lane"), "ls")); denied {
		t.Fatalf("jump/../.. resolves out of the paused repo: %q", reason)
	}
}

// A shell-routed patch from a vanished cwd is placed where the cwd's own
// discovery placed it. Inside the governed repo, a dangling link out to a plain
// directory: an empty directory at the cwd would be in the plain one, so a
// Bash `apply_patch` there is feature-off. shellPatchDirs used to climb past
// the link lexically into the repo and refuse it (Codex code pass). The
// control is a vanished cwd lexically inside the repo, which refuses.
func TestAShellPatchFromAVanishedCwdIsPlacedLikeTheCwd(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	plain := t.TempDir()
	if err := os.Symlink(filepath.Join(plain, "gone"), filepath.Join(f.repo, "out")); err != nil {
		t.Fatal(err)
	}
	if reason, denied := gateFrom(t, f, bashHookJSON("sess-a", filepath.Join(f.repo, "gone", "lane"), "apply_patch")); !denied {
		t.Fatal("control: a shell-routed patch from a vanished cwd inside the repo is refused")
	} else if !strings.Contains(reason, "apply_patch") {
		t.Fatalf("control refused for another reason: %q", reason)
	}
	for _, cmd := range []string{"apply_patch", "cd .. && apply_patch"} {
		// `cd ..` lands on the link's own entry, <repo>/out, which exists
		// and does not resolve: it too is placed where it resolves (Codex
		// re-verify pass).
		if reason, denied := gateFrom(t, f, bashHookJSON("sess-a", filepath.Join(f.repo, "out", "lane"), cmd)); denied {
			t.Fatalf("%s: the cwd resolves through the link to an ungoverned directory: %q", cmd, reason)
		}
	}
}

// A symlink with an EMPTY target cannot be traversed (XNU: ENOENT) and no
// directory created below it would change that, so the walk gives up and the
// cwd keeps today's verdict: a .git above whose ledger is present denies. The
// split used to read "" as "." and discover from the link's directory (Codex
// re-verify pass). The control is a plain vanished cwd beside it.
func TestAVanishedCwdThroughAnEmptyLinkKeepsTodaysVerdict(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if err := os.Symlink("", filepath.Join(f.repo, "e")); err != nil {
		t.Skipf("this volume refuses an empty symlink target: %v", err)
	}
	if reason, denied := gateFrom(t, f, bashHookJSON("sess-a", filepath.Join(f.repo, "gone", "lane"), "ls")); denied {
		t.Fatalf("control: a plain vanished cwd is discovered from the checkout: %q", reason)
	}
	reason, denied := gateFrom(t, f, bashHookJSON("sess-a", filepath.Join(f.repo, "e", "lane"), "ls"))
	if !denied || !strings.Contains(reason, "repo discovery failed") {
		t.Fatalf("a cwd through an empty link must keep today's deny: denied=%v %q", denied, reason)
	}
}
