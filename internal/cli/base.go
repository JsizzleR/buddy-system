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
//
// A REWRITTEN MAIN (D-048, field notes §16). A session amended a commit on the shared
// main twice; a peer had already rebased onto the first version, and the
// rebase succeeded silently onto a commit no longer reachable from main.
// Caught only by a hand-run `git merge-base --is-ancestor`, and the cheap
// check a careful person runs — same parent, same subject — says "same
// commit". The ancestor test is the alarm polarity (§20): it fires on every
// session with unlanded work, which is why D-038 cut it.
//
// The discriminator that does not fire on healthy work is main's own REFLOG.
// Every tip main has had is in it; a former tip that current main no longer
// reaches is a commit main DROPPED. A base that carries one is built on
// history main rewrote away. A worktree branch's unlanded commits were never
// main's tip, so they never appear in that set — "what does a healthy tree
// print?" is answered by the first test, and it prints nothing new.
//
// It is read-side only: one reflog walk per listing, and the per-base walk
// runs only when that set is non-empty AND the base is ahead of main. The
// reflog is the whole evidence, so a main whose reflog is off or expired
// (gc's default keeps unreachable entries 30 days) shows no drop — silence
// here says "no drop the reflog still records", never "not rewritten".

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	return baseGitIn(dir, "", args...)
}

// baseGitIn is baseGit with stdin: a reflog can hold thousands of tips, and
// they go to `rev-list --stdin` rather than onto argv.
func baseGitIn(dir, stdin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), baseBudget)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = cleanGitEnv()
	cmd.Stdin = strings.NewReader(stdin)
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

// reflogTips is every value main's reflog records main having held, one per
// line for `rev-list --stdin`. Walked back to where they rejoin the current
// tip (droppedFrom), they are the commits main DROPPED. Empty when main was
// never rewritten inside the reflog's window, or when the reflog cannot be
// read — the caller cannot tell those apart, and does not claim to.
//
// Two sources, because each misses what the other holds (Codex, D-048).
// `rev-list --walk-reflogs` is plumbing (no user `log.*` config adds lines)
// and reads any ref backend, but it emits each entry's NEW value only. The
// OLD value of the oldest retained entry is therefore lost: main at X, every
// earlier entry expired, then `reset --hard X~1` leaves one entry old=X
// new=X~1, and the walk says only X~1 — the dropped X is invisible to it
// while its evidence is still on disk. The files backend's log carries both
// columns, so it is read too when it exists (never under reftable).
func reflogTips(dir, name string) string {
	var tips []string
	if out, err := baseGit(dir, "rev-list", "--walk-reflogs", "refs/heads/"+name); err == nil {
		tips = strings.Fields(out)
	}
	if p, err := baseGit(dir, "rev-parse", "--git-path", "logs/refs/heads/"+name); err == nil && p != "" {
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		if raw, err := os.ReadFile(p); err == nil {
			for _, ln := range strings.Split(string(raw), "\n") {
				if f := strings.Fields(ln); len(f) >= 2 {
					tips = append(tips, f[0], f[1])
				}
			}
		}
	}
	seen := map[string]bool{}
	var in strings.Builder
	for _, s := range tips {
		// The all-zero name is a creation or deletion entry's missing side.
		if isSHA(s) && strings.Trim(s, "0") != "" && !seen[s] {
			seen[s] = true
			in.WriteString(s + "\n")
		}
	}
	return in.String()
}

// droppedFrom walks the former tips (reflogTips' stdin form) back to where
// they rejoin tip: what is left is what main dropped.
func droppedFrom(dir, tips, tip string) map[string]bool {
	if tips == "" {
		return nil
	}
	out, err := baseGitIn(dir, tips, "rev-list", "--stdin", "^"+tip)
	if err != nil {
		return nil
	}
	var d map[string]bool
	for _, s := range strings.Fields(out) {
		if isSHA(s) {
			if d == nil {
				d = map[string]bool{}
			}
			d[s] = true
		}
	}
	return d
}

// baseReader renders bases for one listing: main is resolved once, main's
// dropped commits are collected once, and each distinct sha is placed once
// however many sessions share it.
type baseReader struct {
	dir     string
	main    string
	tip     string
	hasMain bool
	dropped map[string]bool
	cache   map[string]placed
}

type placed struct{ w, drop string }

// afterReflogRead is a test seam: a landing between the reflog read and the
// tip read is the race the order below exists for, and it cannot be timed
// from outside.
var afterReflogRead = func(dir string) {}

// newBaseReader reads main's reflog BEFORE the tip every comparison uses
// (Codex, D-048). The other order races a landing: tip read at A, main
// fast-forwards to B, the reflog now names B, and B minus A's history reads
// as a commit main DROPPED when main only moved forward. Reflog first, a
// later tip can only be further along — a fast-forward in between removes
// commits from the set, never adds one — and a rewrite in between is still
// caught, since the tip it dropped was already in the reflog.
func newBaseReader(dir string) *baseReader {
	r := &baseReader{dir: dir, cache: map[string]placed{}}
	name, _, ok := mainTip(dir)
	if !ok {
		return r
	}
	tips := reflogTips(dir, name)
	afterReflogRead(dir)
	if r.main, r.tip, r.hasMain = mainTip(dir); r.hasMain && r.main == name {
		r.dropped = droppedFrom(dir, tips, r.tip)
	}
	return r
}

// carried names the commits in sha's history that main dropped: how many,
// and one of them. Not "the newest": rev-list's date-ordered walk can reach
// an older commit first across a merge (Codex, D-048), so the name is an
// example to look up, never a ranking.
// Only a base AHEAD of main can carry one, so the caller asks only then.
func (r *baseReader) carried(sha string) (n int, one string) {
	if len(r.dropped) == 0 {
		return 0, ""
	}
	out, err := baseGit(r.dir, "rev-list", sha, "^"+r.tip)
	if err != nil {
		return 0, ""
	}
	for _, s := range strings.Fields(out) {
		if r.dropped[s] {
			if n == 0 {
				one = s
			}
			n++
		}
	}
	return n, one
}

// where says where sha stands against main, in words: `on main`, `3 behind
// main`, `2 ahead of main`, `2 ahead, 3 behind main` — and, apart, what it
// carries that main dropped, or "".
func (r *baseReader) where(sha string) (w, drop string) {
	if p, ok := r.cache[sha]; ok {
		return p.w, p.drop
	}
	if !r.hasMain {
		r.cache[sha] = placed{w: "no main branch to compare"}
		return r.cache[sha].w, ""
	}
	ahead, behind, err := baseLag(r.dir, sha, r.tip)
	switch {
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
	if err == nil && ahead > 0 {
		switch n, c := r.carried(sha); {
		case n == 1:
			drop = fmt.Sprintf("carries 1 commit %s DROPPED: %s", r.main, c[:8])
		case n > 1:
			drop = fmt.Sprintf("carries %d commits %s DROPPED, incl. %s", n, r.main, c[:8])
		}
	}
	r.cache[sha] = placed{w, drop}
	return w, drop
}

// note is the one rendering both views print: `base 079dd6a7 (3 behind main,
// 12m ago)`, or `(1 ahead, 1 behind main, 12m ago; carries 1 commit main
// DROPPED: 8ace1954)`. The age is the TURN's, since that is when HEAD was read.
func (r *baseReader) note(now time.Time, b store.Base) string {
	if !isSHA(b.SHA) {
		return "base " + fence.Line(b.SHA, 16) + " (unreadable)"
	}
	w, drop := r.where(b.SHA)
	if drop != "" {
		drop = "; " + drop
	}
	return fmt.Sprintf("base %s (%s, %s ago%s)", b.SHA[:8], w, age(now, b.TurnAt), drop)
}
