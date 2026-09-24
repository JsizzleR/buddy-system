package cli

// THE BASE A SESSION MEASURED AGAINST (D-038, issue #28).
//
// Measured, field notes §6: a session reported the shared status file at
// 1999/2000 lines — one line of headroom — and it went out as a fleet
// emergency with an archive roll assigned. `main` was at 1736. The session's
// worktree was three landings behind, before a roll that removed 363 lines:
// its number was true about its tree and meaningless about main, and nothing
// on the roster or in `who` said which commit any session's tree was on. With
// N sessions on N worktrees at N bases, "how stale am I?" is the unstated
// assumption in every report a session makes.
//
// So the Stop hook, which already runs once per turn, records HEAD, and the
// two views print it with its lag behind main computed AT READ TIME — main
// moves without the session doing anything, so a stored lag would be stale
// in the one direction that matters. Not on `beat`: that is the per-tool-call
// path with a 100 ms budget, and it forks no git by design.
//
// NOT "NOT ON MAIN". The issue proposed flagging a base that is not an
// ancestor of main; in a fleet that commits on worktree branches and lands
// them, that is every session with unlanded work, and a flag that fires on
// every busy session gets read past. Both counts print instead: `2 ahead`
// alone is unlanded work on current main; `2 ahead, 3 behind` is a tree that
// needs a rebase before its numbers mean anything about main — which is also
// how the orphaned-base case (§16: a peer rebased onto a commit main had
// amended away) shows, since an ancestor test cannot tell that case from
// unlanded work either.

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

// baseBudget bounds each git call here. The hook side is the Stop hook, off
// the tool-call path; the read side runs once per distinct base per listing.
const baseBudget = time.Second

// baseGit runs one read-only git command in dir with the dirty scan's clean
// environment (a background read, not a commit-time gate — CLAUDE.md), so a
// GIT_DIR or GIT_INDEX_FILE inherited from the harness cannot re-point it.
func baseGit(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), baseBudget)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = cleanGitEnv()
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// isSHA accepts a full object name only (SHA-1 or SHA-256), lower-case hex.
// It is what the ledger stores and what reaches git's argv on the read side,
// so nothing that could parse as an option or a revision expression gets in.
func isSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// headOf is the Stop hook's sample: HEAD in the session's cwd, or "" when it
// cannot be read (not a repo, an unborn branch, git missing). A failed sample
// records nothing and leaves the previous one standing with its own age —
// the age is what keeps an old observation honest.
func headOf(dir string) string {
	out, err := baseGit(dir, "rev-parse", "--verify", "-q", "HEAD^{commit}")
	if err != nil || !isSHA(out) {
		return ""
	}
	return out
}

// mainTip resolves the branch every base is compared against: `main`, else
// `master`, as local branches — worktrees of one checkout share refs, and
// landing means moving the local branch. ok=false when neither exists.
func mainTip(dir string) (name, sha string, ok bool) {
	for _, n := range []string{"main", "master"} {
		if out, err := baseGit(dir, "rev-parse", "--verify", "-q", "refs/heads/"+n+"^{commit}"); err == nil && isSHA(out) {
			return n, out, true
		}
	}
	return "", "", false
}

// baseLag counts commits on each side: ahead = reachable from sha and not
// from tip, behind = the reverse. err when git cannot place sha at all (an
// object this repo does not have).
func baseLag(dir, sha, tip string) (ahead, behind int, err error) {
	out, err := baseGit(dir, "rev-list", "--left-right", "--count", sha+"..."+tip)
	if err != nil {
		return 0, 0, err
	}
	f := strings.Fields(out)
	if len(f) != 2 {
		return 0, 0, fmt.Errorf("rev-list --count: unexpected %q", out)
	}
	if ahead, err = strconv.Atoi(f[0]); err != nil {
		return 0, 0, err
	}
	behind, err = strconv.Atoi(f[1])
	return ahead, behind, err
}

// baseReader renders bases for one listing: main is resolved once, and each
// distinct sha is placed once however many sessions share it.
type baseReader struct {
	dir     string
	main    string
	tip     string
	hasMain bool
	cache   map[string]string
}

func newBaseReader(dir string) *baseReader {
	r := &baseReader{dir: dir, cache: map[string]string{}}
	r.main, r.tip, r.hasMain = mainTip(dir)
	return r
}

// where says where sha stands against main, in words: `on main`, `3 behind
// main`, `2 ahead of main`, `2 ahead, 3 behind main`.
func (r *baseReader) where(sha string) string {
	if w, ok := r.cache[sha]; ok {
		return w
	}
	if !r.hasMain {
		r.cache[sha] = "no main branch to compare"
		return r.cache[sha]
	}
	var w string
	switch ahead, behind, err := baseLag(r.dir, sha, r.tip); {
	case err != nil:
		w = "not in this repo's history"
	case ahead == 0 && behind == 0:
		w = "on " + r.main
	case ahead == 0:
		w = fmt.Sprintf("%d behind %s", behind, r.main)
	case behind == 0:
		w = fmt.Sprintf("%d ahead of %s", ahead, r.main)
	default:
		w = fmt.Sprintf("%d ahead, %d behind %s", ahead, behind, r.main)
	}
	r.cache[sha] = w
	return w
}

// note is the one rendering both views print: `base 079dd6a7 (3 behind main,
// 12m ago)`. The age is the TURN's, since that is when HEAD was read.
func (r *baseReader) note(now time.Time, b store.Base) string {
	if !isSHA(b.SHA) {
		return "base " + fence.Line(b.SHA, 16) + " (unreadable)"
	}
	return fmt.Sprintf("base %s (%s, %s ago)", b.SHA[:8], r.where(b.SHA), age(now, b.TurnAt))
}
