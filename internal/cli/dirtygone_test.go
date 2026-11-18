package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #125: `buddy whose` listed "uncommitted" rows for worktrees that no longer
// exist. One ledger held 165 dirty-path rows, at most 4 in a worktree still on
// disk, and `whose internal/cli/cli.go` printed 5 DIRTY IN rows, 4 of them in
// removed worktrees, 35 h to 27 d old. A removed worktree holds no hunk, so the
// row answers nothing, in the view a session reads before it edits a file.
//
// The fix HIDES such a row where it renders and deletes nothing (invariant 10:
// a scan may retract, never add or refuse; a view verb writes nothing), so a
// directory that comes back (a remounted volume, a worktree re-added at the
// same path) brings its rows back with it.

func TestWhoseHidesRowsOfARemovedWorktree(t *testing.T) {
	boundedParallel(t)
	for _, how := range []string{"rm -rf", "git worktree remove"} {
		t.Run(how, func(t *testing.T) {
			f := newFixture(t)
			if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
				t.Fatal(errw)
			}
			f.helloIn(t, f.repo, "sess-a", "alpha")
			f.helloIn(t, f.wtB, "sess-b", "bravo")
			f.edit(t, f.repo, "sess-a", "shared.txt", "alpha's\n")
			f.edit(t, f.wtB, "sess-b", "shared.txt", "bravo's\n")
			f.edit(t, f.wtB, "sess-b", "onlyb.txt", "bravo's alone\n")

			// The positive control: while wtB is on disk its rows are shown.
			out, _, _ := f.run(t, f.repo, "", "whose", "shared.txt")
			if !strings.Contains(out, "bravo") || !strings.Contains(out, "alpha") {
				t.Fatalf("before the removal both holders must show:\n%s", out)
			}
			if strings.Contains(out, "no longer exist") {
				t.Fatalf("nothing is gone yet, and the view says something is:\n%s", out)
			}

			switch how {
			case "rm -rf":
				// git still lists it (prunable) until a prune.
				if err := os.RemoveAll(f.wtB); err != nil {
					t.Fatal(err)
				}
			default:
				gitIn(t, f.repo, "worktree", "remove", "--force", f.wtB)
			}

			out, errw, code := f.run(t, f.repo, "", "whose", "shared.txt")
			if code != 0 {
				t.Fatalf("whose: %d %s", code, errw)
			}
			if !strings.Contains(out, "alpha") {
				t.Fatalf("the row in a worktree that exists must stay:\n%s", out)
			}
			dirtyIn := out[strings.Index(out, "DIRTY IN"):]
			if strings.Contains(dirtyIn, "bravo") {
				t.Fatalf("a row in a removed worktree is still listed as uncommitted:\n%s", out)
			}
			if !strings.Contains(out, "1 row(s) recorded in a worktree that no longer exists") {
				t.Fatalf("a hidden row must be counted, or a smaller answer looks like a whole one:\n%s", out)
			}

			// Every holder gone: the (none) answer, still counting the hidden.
			out, _, _ = f.run(t, f.repo, "", "whose", "onlyb.txt")
			if !strings.Contains(out, "DIRTY IN     (none)") || strings.Contains(out[strings.Index(out, "DIRTY IN"):], "bravo") ||
				!strings.Contains(out, "1 row(s) recorded in a worktree that no longer exists") {
				t.Fatalf("with its only holder's worktree gone, whose must answer (none) and count the hidden row:\n%s", out)
			}

			// who reports the session's own rows the same way.
			out, _, _ = f.run(t, f.repo, "", "who", "bravo")
			if !strings.Contains(out, "DIRTY PATHS  (none in a worktree that exists; 2 recorded in a worktree that no longer exists") {
				t.Fatalf("who must hide and count the rows of a removed worktree:\n%s", out)
			}

			// Hidden, never deleted: the directory back, the rows back.
			if err := os.MkdirAll(f.wtB, 0o755); err != nil {
				t.Fatal(err)
			}
			out, _, _ = f.run(t, f.repo, "", "whose", "shared.txt")
			if !strings.Contains(out[strings.Index(out, "DIRTY IN"):], "bravo") {
				t.Fatalf("the view deleted the row: with the directory back it must show again:\n%s", out)
			}
		})
	}
}

// The git arm. A stat that finds nothing is NOT enough: on a case-sensitive
// volume the folded key of a mixed-case path never exists, so a row is gone
// only when git's worktree list names no existing directory with that key
// either. And git that cannot be asked hides nothing.
func TestGoneNeedsGitToAgreeWithTheStat(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	missing := func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	wtB := worktreeKey(f.wtB)

	if goneWorktreesWith(f.repo, missing)(wtB) {
		t.Fatal("git lists wtB and it exists: a stat that missed must not hide its rows")
	}
	// The positive control: a key git does not list, with the stat missing.
	if !goneWorktreesWith(f.repo, missing)(wtB + "-never") {
		t.Fatal("a key neither the stat nor git finds must read as gone")
	}
	// git cannot be asked (no repository there): nothing is gone.
	if goneWorktreesWith(filepath.Join(t.TempDir(), "no-repo"), missing)(wtB + "-never") {
		t.Fatal("with git unreadable, nothing may be judged gone")
	}
	// Paths the listing must carry whole: a newline (without -z git prints it
	// raw and the line splits; measured on git 2.54) and a non-ASCII name.
	for i, name := range []string{"wt-nl\nx", "wt-é"} {
		odd := filepath.Join(filepath.Dir(f.wtB), name)
		gitIn(t, f.repo, "worktree", "add", "-q", "-b", fmt.Sprintf("odd-%d", i), odd)
		if goneWorktreesWith(f.repo, missing)(worktreeKey(odd)) {
			t.Fatalf("git lists %q and it exists: its rows must not be hidden", name)
		}
	}
	// A listed worktree whose stat fails for another reason (an unsearchable
	// parent, EACCES) is uncertain, never gone (Codex code pass).
	locked := filepath.Join(filepath.Dir(f.wtB), "locked")
	lockedWT := filepath.Join(locked, "wt")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, f.repo, "worktree", "add", "-q", lockedWT)
	lockedKey := worktreeKey(lockedWT)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	if _, err := os.Stat(lockedWT); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Skipf("cannot make a stat fail other than not-exist here (root?): %v", err)
	}
	if goneWorktreesWith(f.repo, missing)(lockedKey) {
		t.Fatal("a listed worktree whose stat failed with EACCES must not read as gone")
	}
	os.Chmod(locked, 0o755)
	// A stat that finds the directory settles it without git.
	if goneWorktreesWith("", os.Stat)(wtB) {
		t.Fatal("a key the stat finds is not gone, whatever git says")
	}
}
