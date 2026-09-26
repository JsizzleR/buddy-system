package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// D-038 (issue #28): the Stop hook records the commit a session's tree is on,
// and the roster and `who` print where it stands against main NOW.

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitN(t *testing.T, dir, name string, n int) {
	t.Helper()
	for i := range n {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Repeat("x", i+1)), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, dir, "add", name)
		gitIn(t, dir, "commit", "-q", "-m", name)
	}
}

// stopB runs bravo's Stop hook with a transcript whose last turn ended at the
// fixture clock — the only path that records a base.
func stopB(t *testing.T, f *fixture) {
	t.Helper()
	stopIn(t, f, "sess-b", f.wtB)
}

func stopIn(t *testing.T, f *fixture, session, cwd string) {
	t.Helper()
	tr := filepath.Join(t.TempDir(), session+".jsonl")
	line := turnLineTTL(f.clock.UTC().Format("2006-01-02T15:04:05.000Z"), 2, 1000, 10, 0, 10)
	if err := os.WriteFile(tr, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in, _ := json.Marshal(map[string]any{"session_id": session, "cwd": cwd, "transcript_path": tr})
	if _, errw, code := f.run(t, cwd, string(in), "idle"); code != 0 {
		t.Fatalf("idle: %s", errw)
	}
}

func rowOf(t *testing.T, f *fixture, label string) string {
	t.Helper()
	out, errw, code := f.run(t, f.repo, "", "sessions", "--session", "sess-a")
	if code != 0 {
		t.Fatal(errw)
	}
	for _, ln := range strings.Split(out, "\n") {
		if fs := strings.Fields(ln); len(fs) > 1 && fs[1] == label {
			return ln
		}
	}
	t.Fatalf("no row for %s:\n%s", label, out)
	return ""
}

func whoOf(t *testing.T, f *fixture, label string) string {
	t.Helper()
	out, errw, code := f.run(t, f.repo, "", "who", label)
	if code != 0 {
		t.Fatal(errw)
	}
	return out
}

func TestTheRosterSaysWhereASessionsBaseStandsAgainstMain(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	gitIn(t, f.repo, "branch", "-M", "main") // independent of the machine's init.defaultBranch

	// Nothing recorded yet: the row says nothing, and who says so in words.
	if row := rowOf(t, f, "bravo"); strings.Contains(row, "base ") {
		t.Fatalf("no Stop has run, so no base may print:\n%s", row)
	}
	if w := whoOf(t, f, "bravo"); !strings.Contains(w, "BASE         (none recorded") {
		t.Fatalf("who must say the base is unrecorded:\n%s", w)
	}

	stopB(t, f)
	head := gitIn(t, f.wtB, "rev-parse", "HEAD")[:8]
	if row := rowOf(t, f, "bravo"); !strings.Contains(row, "base "+head+" (on main, ") {
		t.Fatalf("a fresh worktree is on main:\n%s", row)
	}

	// main lands three commits; bravo does nothing. The lag is computed at
	// read time, so the SAME stored base now reads 3 behind.
	commitN(t, f.repo, "landed", 3)
	if row := rowOf(t, f, "bravo"); !strings.Contains(row, "base "+head+" (3 behind main, ") {
		t.Fatalf("main moved; the base must read 3 behind:\n%s", row)
	}

	// bravo commits twice on its own branch and ends a turn: ahead AND behind.
	commitN(t, f.wtB, "work", 2)
	f.clock = f.clock.Add(1e9)
	stopB(t, f)
	head2 := gitIn(t, f.wtB, "rev-parse", "HEAD")[:8]
	if row := rowOf(t, f, "bravo"); !strings.Contains(row, "base "+head2+" (2 ahead, 3 behind main, ") {
		t.Fatalf("unlanded work on a stale base:\n%s", row)
	}
	if w := whoOf(t, f, "bravo"); !strings.Contains(w, "BASE         "+head2+" (2 ahead, 3 behind main, ") {
		t.Fatalf("who prints the same base:\n%s", w)
	}
	// alpha never ran the Stop hook: its row has no base (the control that
	// the note is per session, not per repo).
	if row := rowOf(t, f, "alpha"); strings.Contains(row, "base ") {
		t.Fatalf("alpha has no recorded base:\n%s", row)
	}

	// No main (and no master): say so rather than compare against nothing.
	gitIn(t, f.repo, "branch", "-M", "trunk")
	if row := rowOf(t, f, "bravo"); !strings.Contains(row, "base "+head2+" (no main branch to compare, ") {
		t.Fatalf("no main branch:\n%s", row)
	}
	gitIn(t, f.repo, "branch", "-M", "master")
	if row := rowOf(t, f, "bravo"); !strings.Contains(row, "base "+head2+" (2 ahead, 3 behind master, ") {
		t.Fatalf("master is the fallback:\n%s", row)
	}
}

// Field notes §16: main was amended after a peer had rebased onto it, and
// the peer's tree was silently built on a commit main no longer reaches. The
// discriminator is main's reflog, and the test that matters is §20's "what
// does a HEALTHY tree print?": charlie has unlanded work on an older base and
// reads exactly the same counts as bravo — 2 ahead, 1 behind — and only
// bravo carries a commit main dropped.
func TestABaseCarryingACommitMainDroppedSaysSo(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	gitIn(t, f.repo, "branch", "-M", "main")
	wtC := filepath.Join(filepath.Dir(f.wtB), "wtC")
	gitIn(t, f.repo, "worktree", "add", "-q", wtC)
	if _, errw, code := f.run(t, wtC, hookJSON("sess-c", wtC, "", ""), "hello", "--label", "charlie"); code != 0 {
		t.Fatalf("hello c: %s", errw)
	}
	stop := func() {
		t.Helper()
		f.clock = f.clock.Add(1e9)
		stopIn(t, f, "sess-b", f.wtB)
		stopIn(t, f, "sess-c", wtC)
	}

	// charlie: two commits of its own, forked before anything landed.
	commitN(t, wtC, "cwork", 2)
	// main lands X; bravo rebases onto it and commits once.
	commitN(t, f.repo, "landed", 1)
	x := gitIn(t, f.repo, "rev-parse", "HEAD")
	gitIn(t, f.wtB, "reset", "-q", "--hard", "main")
	commitN(t, f.wtB, "bwork", 1)
	stop()
	// Control: nothing was rewritten yet, so neither row may say DROPPED.
	for _, who := range []string{"bravo", "charlie"} {
		if row := rowOf(t, f, who); strings.Contains(row, "DROPPED") {
			t.Fatalf("main was never rewritten:\n%s", row)
		}
	}

	// The §16 move: main amends X. bravo's tree is unchanged.
	gitIn(t, f.repo, "commit", "-q", "--amend", "-m", "landed, amended")
	row := rowOf(t, f, "bravo")
	if !strings.Contains(row, "(2 ahead, 1 behind main, ") ||
		!strings.Contains(row, "ago; carries 1 commit main DROPPED: "+x[:8]+")") {
		t.Fatalf("bravo is built on the commit main amended away:\n%s", row)
	}
	if w := whoOf(t, f, "bravo"); !strings.Contains(w, "carries 1 commit main DROPPED: "+x[:8]) {
		t.Fatalf("who prints the same drop:\n%s", w)
	}
	// The healthy tree: the same counts, and no drop.
	if row := rowOf(t, f, "charlie"); !strings.Contains(row, "(2 ahead, 1 behind main, ") || strings.Contains(row, "DROPPED") {
		t.Fatalf("charlie's unlanded work is not a drop:\n%s", row)
	}

	// bravo rebuilds on current main: the note clears, though the reflog
	// still remembers X.
	gitIn(t, f.wtB, "reset", "-q", "--hard", "main")
	commitN(t, f.wtB, "bwork2", 1)
	stop()
	if row := rowOf(t, f, "bravo"); !strings.Contains(row, "(1 ahead of main, ") || strings.Contains(row, "DROPPED") {
		t.Fatalf("a base rebuilt on current main carries no drop:\n%s", row)
	}

	// A reset rather than an amend, dropping two: main lands Y1 and Y2 in ONE
	// fast-forward (so Y1 was never main's tip and is in no reflog entry —
	// the walk back from Y2 is what finds it), bravo builds on Y2, and main
	// is reset back past both and lands Z.
	gitIn(t, f.repo, "checkout", "-q", "-b", "side")
	commitN(t, f.repo, "y", 2)
	gitIn(t, f.repo, "checkout", "-q", "main")
	gitIn(t, f.repo, "merge", "-q", "--ff-only", "side")
	y2 := gitIn(t, f.repo, "rev-parse", "HEAD")
	gitIn(t, f.wtB, "reset", "-q", "--hard", "main")
	commitN(t, f.wtB, "bwork3", 1)
	stop()
	y1 := gitIn(t, f.repo, "rev-parse", "HEAD~1")
	gitIn(t, f.repo, "reset", "-q", "--hard", "HEAD~2")
	// Reset only, nothing landed after it: bravo is AHEAD and not behind at
	// all, and still carries the drop (Codex: a gate on "ahead AND behind"
	// survived the test without this state).
	if row := rowOf(t, f, "bravo"); !strings.Contains(row, "(3 ahead of main, ") ||
		!strings.Contains(row, "; carries 2 commits main DROPPED, incl. ") {
		t.Fatalf("a reset alone leaves bravo ahead and carrying the drop:\n%s", row)
	}
	commitN(t, f.repo, "z", 1)
	// The named commit is an example, not a ranking: rev-list's date order
	// does not define "newest" across a merge (Codex).
	row = rowOf(t, f, "bravo")
	if !strings.Contains(row, "; carries 2 commits main DROPPED, incl. "+y2[:8]+")") &&
		!strings.Contains(row, "; carries 2 commits main DROPPED, incl. "+y1[:8]+")") {
		t.Fatalf("bravo carries both commits the reset dropped:\n%s", row)
	}
	if row := rowOf(t, f, "charlie"); strings.Contains(row, "DROPPED") {
		t.Fatalf("charlie still carries nothing main dropped:\n%s", row)
	}
}

// Codex, D-048: `rev-list --walk-reflogs` emits each entry's NEW value only.
// With every earlier entry expired, a reset leaves one entry old=X new=A, and
// the walk alone names only A — the dropped X is invisible to it while the
// entry recording it is still on disk. The files backend's log is read too.
func TestADropSurvivesTheReflogExpiringBeforeIt(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	gitIn(t, f.repo, "branch", "-M", "main")
	commitN(t, f.repo, "landed", 1)
	x := gitIn(t, f.repo, "rev-parse", "HEAD")
	gitIn(t, f.wtB, "reset", "-q", "--hard", "main")
	stopB(t, f)
	gitIn(t, f.repo, "reflog", "expire", "--expire=now", "--expire-unreachable=now", "refs/heads/main")
	gitIn(t, f.repo, "reset", "-q", "--hard", "HEAD~1")
	a := gitIn(t, f.repo, "rev-parse", "HEAD")
	// Control: the scenario is the one described — the walk names A alone.
	if walk := gitIn(t, f.repo, "rev-list", "--walk-reflogs", "refs/heads/main"); walk != a {
		t.Fatalf("the walk should name only the reset's new value %s, got:\n%s", a[:8], walk)
	}
	if row := rowOf(t, f, "bravo"); !strings.Contains(row, "(1 ahead of main, ") ||
		!strings.Contains(row, "; carries 1 commit main DROPPED: "+x[:8]+")") {
		t.Fatalf("the reset's old value is still on disk and names the drop:\n%s", row)
	}
}

// Codex, D-048: a landing between reading main's tip and reading its reflog
// made a fast-forward read as a drop. The reflog is read first; the seam
// lands bravo's commit onto main at exactly that moment. Not parallel: the
// seam is a package variable, and the parallel tests are paused while this
// one runs.
func TestAFastForwardDuringTheReadIsNotADrop(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	gitIn(t, f.repo, "branch", "-M", "main")
	commitN(t, f.wtB, "bwork", 1)
	b := gitIn(t, f.wtB, "rev-parse", "HEAD")
	stopB(t, f)
	fired := false
	afterReflogRead = func(dir string) {
		if !fired {
			fired = true
			gitIn(t, f.repo, "merge", "-q", "--ff-only", b)
		}
	}
	t.Cleanup(func() { afterReflogRead = func(string) {} })
	row := rowOf(t, f, "bravo")
	if !fired {
		t.Fatal("the seam never fired, so the race was not exercised")
	}
	if !strings.Contains(row, "(on main, ") || strings.Contains(row, "DROPPED") {
		t.Fatalf("main only moved forward onto bravo's base:\n%s", row)
	}
}

// isSHA is what keeps the stored value and the read-side argv to a full
// object name: nothing that parses as a flag or a revision expression.
func TestIsSHA(t *testing.T) {
	for s, want := range map[string]bool{
		strings.Repeat("a", 40): true, strings.Repeat("0", 64): true,
		strings.Repeat("A", 40): false, strings.Repeat("a", 39): false,
		"--all" + strings.Repeat("a", 35): false, "HEAD": false, "": false,
		strings.Repeat("a", 38) + "^!": false,
	} {
		if got := isSHA(s); got != want {
			t.Errorf("isSHA(%q) = %v, want %v", s, got, want)
		}
	}
}
