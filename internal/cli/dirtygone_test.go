package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JsizzleR/buddy-system/internal/store"
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
// same path) brings its rows back with it. A worktree `git worktree remove`d
// comes back only re-added: a plain directory there is no worktree, and on a
// case-sensitive volume no probe finds it (TestGoneOnACaseSensitiveVolume).

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

			// Hidden, never deleted: the worktree back, the rows back. After
			// `git worktree remove` it comes back only as a worktree git lists:
			// a plain directory there is matched by no probe on a
			// case-sensitive volume (CI run 44251996503, ext4; this arm
			// recreated it with MkdirAll and went red there alone;
			// TestGoneOnACaseSensitiveVolume).
			if how == "rm -rf" {
				if err := os.MkdirAll(f.wtB, 0o755); err != nil {
					t.Fatal(err)
				}
			} else {
				gitIn(t, f.repo, "worktree", "add", "-q", f.wtB, "wtB")
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

// caseSensitiveStatUnder returns a stat that behaves, below root, as a
// case-sensitive volume would: each component under root must be spelled
// exactly as its directory lists it, so the FOLDED key of a mixed-case path
// never stats, as on the ubuntu runner's ext4 (CI run 44251996503 on 20bd147).
// The macOS volume the suite is developed on is case-insensitive and finds
// every spelling, which is how the run went red on Linux alone. Above root
// (the fixture's own directory, real spelling) it is a plain stat of the
// path's root half: listing every ancestor would need read permission a real
// stat does not, and a searchable but unreadable TMPDIR ancestor would fail the
// control for nothing (Codex code pass). The case under test lives in wtB.
func caseSensitiveStatUnder(root string) func(string) (os.FileInfo, error) {
	rootParts := strings.Split(filepath.Clean(root), "/")
	return func(p string) (os.FileInfo, error) {
		parts := strings.Split(filepath.Clean(p), "/")
		if len(parts) < len(rootParts) || store.Fold(strings.Join(parts[:len(rootParts)], "/")) != store.Fold(root) {
			return os.Stat(p) // outside the fixture: the volume decides
		}
		cur := root
		for _, c := range parts[len(rootParts):] {
			ents, err := os.ReadDir(cur)
			if err != nil {
				return nil, err
			}
			found := false
			for _, e := range ents {
				if e.Name() == c {
					found = true
					break
				}
			}
			if !found {
				return nil, &os.PathError{Op: "stat", Path: p, Err: os.ErrNotExist}
			}
			cur = filepath.Join(cur, c)
		}
		return os.Stat(cur)
	}
}

// A removed worktree COMES BACK, on a case-sensitive volume, only as a
// worktree git lists again. The rm -rf arm passed on the runner because git
// still lists the removed worktree (prunable) at its real spelling, so the
// recreated directory stats there; after `git worktree remove` git lists
// nothing, and a plain directory recreated at the path is matched by no probe:
// the stat of the folded key misses and no listed entry has that key. That is
// the design (a directory that is no worktree holds no hunk of this
// repository), and the CLI test above re-adds the worktree with git instead.
func TestGoneOnACaseSensitiveVolume(t *testing.T) {
	boundedParallel(t)
	for _, how := range []string{"rm -rf", "git worktree remove"} {
		t.Run(how, func(t *testing.T) {
			f := newFixture(t)
			key := worktreeKey(f.wtB)
			caseSensitiveStat := caseSensitiveStatUnder(canon(filepath.Dir(f.wtB)))
			// The positive control: the emulation is armed. The folded key
			// misses (the fixture's wtB is mixed case), the real spelling stats.
			if _, err := caseSensitiveStat(key); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("the folded key %q must not stat on an emulated case-sensitive volume: %v", key, err)
			}
			if _, err := caseSensitiveStat(canon(f.wtB)); err != nil {
				t.Fatalf("the real spelling must stat: %v", err)
			}
			if goneWorktreesWith(f.repo, caseSensitiveStat)(key) {
				t.Fatal("git lists wtB and it exists: its rows must show")
			}
			switch how {
			case "rm -rf":
				if err := os.RemoveAll(f.wtB); err != nil {
					t.Fatal(err)
				}
			default:
				gitIn(t, f.repo, "worktree", "remove", "--force", f.wtB)
			}
			if !goneWorktreesWith(f.repo, caseSensitiveStat)(key) {
				t.Fatal("the removed worktree must read as gone")
			}
			if err := os.MkdirAll(f.wtB, 0o755); err != nil {
				t.Fatal(err)
			}
			gone := goneWorktreesWith(f.repo, caseSensitiveStat)(key)
			if how == "rm -rf" {
				if gone {
					t.Fatal("git still lists the worktree and its directory is back: its rows must show again")
				}
				return
			}
			// CI's red, pinned as the design: a plain directory at the path of
			// a worktree git no longer lists is no worktree.
			if !gone {
				t.Fatal("a plain directory where git lists no worktree: on a case-sensitive volume no probe finds the folded key")
			}
			if err := os.Remove(f.wtB); err != nil {
				t.Fatal(err)
			}
			gitIn(t, f.repo, "worktree", "add", "-q", f.wtB, "wtB")
			if goneWorktreesWith(f.repo, caseSensitiveStat)(key) {
				t.Fatal("the worktree re-added at its path: its rows must show again")
			}
		})
	}
}
