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
	tr := filepath.Join(t.TempDir(), "bravo.jsonl")
	line := turnLineTTL(f.clock.UTC().Format("2006-01-02T15:04:05.000Z"), 2, 1000, 10, 0, 10)
	if err := os.WriteFile(tr, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in, _ := json.Marshal(map[string]any{"session_id": "sess-b", "cwd": f.wtB, "transcript_path": tr})
	if _, errw, code := f.run(t, f.wtB, string(in), "idle"); code != 0 {
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
