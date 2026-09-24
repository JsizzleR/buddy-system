package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A repository git REFUSES is not thereby unreadable to buddy. Measured
// 2026-09-24: an empty `git init` in /private/tmp made git refuse every path
// under /tmp ("dubious ownership"), and the gate denied every Edit and Write
// there, in every session, for a repository with no commits and no ledger.
// Git's refusal is reproduced here with a repository format it does not know,
// which exits 128 without saying "not a git repository".

// refusedRepo makes a real repository (with a linked worktree beside it) and
// then a format git refuses to read.
func refusedRepo(t *testing.T) (main, wt string) {
	t.Helper()
	root := t.TempDir()
	main = filepath.Join(root, "main")
	wt = filepath.Join(root, "wt")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "init", "-q")
	gitIn(t, main, "commit", "-q", "--allow-empty", "-m", "c")
	gitIn(t, main, "worktree", "add", "-q", wt)
	gitIn(t, main, "config", "core.repositoryformatversion", "99")
	return main, wt
}

func TestGateOnARefusedRepository(t *testing.T) {
	cases := []struct {
		name   string
		target func(main, wt string) string
		ledger bool // a buddy.db in the common git dir
		denied bool
	}{
		{"refused, no ledger", func(m, _ string) string { return filepath.Join(m, "a.txt") }, false, false},
		{"refused, ledger present", func(m, _ string) string { return filepath.Join(m, "a.txt") }, true, true},
		{"linked worktree, no ledger", func(_, w string) string { return filepath.Join(w, "a.txt") }, false, false},
		{"linked worktree, ledger in the COMMON dir", func(_, w string) string { return filepath.Join(w, "a.txt") }, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.initAndHello(t)
			main, wt := refusedRepo(t)
			if tc.ledger {
				if err := os.WriteFile(filepath.Join(main, ".git", "buddy.db"), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			out, _, _ := f.run(t, f.repo, hookJSON("sess-a", f.repo, "Write", tc.target(main, wt)), "gate")
			reason, denied := decodeDeny(t, out)
			if denied != tc.denied {
				t.Fatalf("denied=%v, want %v: %q", denied, tc.denied, reason)
			}
			if denied && !strings.Contains(reason, "ledger is unavailable") {
				t.Fatalf("a refused repo WITH a ledger is unreadable, and the deny must say so: %q", reason)
			}
		})
	}
}

// A .git FILE that does not parse proves nothing, so the gate keeps denying.
func TestGateOnAnUnparsableGitFile(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("not a gitdir line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Positive control that git refuses this without "not a git repository".
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--git-common-dir").CombinedOutput()
	if err == nil || strings.Contains(string(out), "not a git repository") {
		t.Skipf("this git answers a bad .git file differently (%v: %s); the case needs a refusal", err, out)
	}
	res, _, _ := f.run(t, f.repo, hookJSON("sess-a", f.repo, "Write", filepath.Join(dir, "a.txt")), "gate")
	if _, denied := decodeDeny(t, res); !denied {
		t.Fatal("an unparsable .git file must not be read as a repository with no ledger")
	}
}

// With GIT_DIR set, git is not using the nearest .git, so its absence of a
// ledger proves nothing about the repository git was told to use. Asked of
// the function directly: through the gate, GIT_DIR would also redirect the
// HOME repo's discovery, and a deny for that reason would pass this vacuously.
func TestFallbackStandsDownUnderGitDir(t *testing.T) {
	main, _ := refusedRepo(t)
	if !ledgerProvablyAbsent(main) {
		t.Fatal("control: a refused repo with no ledger is provably ledger-free")
	}
	t.Setenv("GIT_DIR", filepath.Join(main, ".git"))
	if ledgerProvablyAbsent(main) {
		t.Fatal("under GIT_DIR the nearest-.git fallback must not answer")
	}
}
