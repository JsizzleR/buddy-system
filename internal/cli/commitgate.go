package cli

// The commit gate: the claims check at the commit boundary.
//
// WHY A SECOND LINE EXISTS AT ALL. The PreToolUse gate adjudicates the path a
// tool DECLARES, so it sees an Edit and a Write and nothing else. A file
// written by a code generator, by a Bash redirect, by a formatter run over the
// tree, or by any process that never went through the harness reaches the index
// without the gate ever being asked. Those writes are invisible at the tool
// boundary and visible at the commit boundary, which is the whole argument for
// checking again here.
//
// WHAT IT DELIBERATELY DOES NOT DO, each cut for a stated reason:
//
//   - It does not report paths that are inside NOBODY's claim. The original
//     one-line spec asked for "writes outside the session's own claims", but
//     that fires on almost every commit — a README nobody reserved is not a
//     collision with anyone — and a warning that fires on everything is read as
//     noise and then disabled. It is also not computable from what the ledger
//     is asked here: OwnerOf answering "no other session holds this" does not
//     establish that the path is unclaimed, because it may sit inside the
//     COMMITTING session's own claim.
//   - It does not consult dirty_paths. "Another session's tool call last named
//     this file" is not authorship of the staged hunks, and deliberate handoff
//     produces exactly the same signal. That table is documented as advisory
//     and answers "who do I talk to?", which is `buddy whose`, not this.
//   - It does not refuse when it cannot tell which session is committing. A
//     human typing `git commit` in their own terminal has no session id, and
//     blocking that is how the hook gets uninstalled.
//   - It does not deduplicate across commits. A per-path cooldown would hide
//     the second conflicting edit to the same file, which is a real event and
//     not a repeat of the first. It groups WITHIN one report instead.
//
// HONEST COVERAGE, because overclaiming here would be the worse failure. This
// runs only when a hook is installed and only on the ordinary commit path.
// `git commit --no-verify` skips it, so does `git commit-tree`; merge commits
// run pre-merge-commit instead, and the commits that rebase, cherry-pick,
// revert and `git am` create do not run pre-commit at all. It reveals a
// reservation conflict on the paths of a pending commit. It does not prevent
// the write that created them, does not establish who made them, and does not
// cover commits whose creation never invokes it.
//
// A pre-push variant was considered and CUT: it would compare historical
// commits against CURRENT claims, which is a different question — the claim may
// have been released before the push, or taken after the commit — and an
// endpoint diff over a range cannot see an edit that a later commit reverted.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

// commitGateBudget bounds the staged-path listing. Longer than the dirty scan's
// budget because this one runs while the committing git holds .git/index.lock
// and a person is watching a prompt, not on a heartbeat; still short enough
// that a wedged git costs a pause rather than a hang.
const commitGateBudget = 5 * time.Second

// maxStagedBytes bounds one listing. A var so the over-cap arm can be tested
// without staging a megabyte — which means the test that shrinks it MUST stay
// serial: every other test in the package runs in parallel, and one that read a
// temporarily-shrunken cap would refuse its own commit for a reason that has
// nothing to do with what it is testing.
// without generating a megabyte of filenames.
var maxStagedBytes = 1 << 20

// Reported rows are capped so that a commit which sweeps up a whole directory
// cannot bury the prompt. The counts are stated when they bite, never silently
// truncated: a report that quietly drops rows is indistinguishable from a
// smaller conflict.
const (
	maxReportClaims = 10
	maxReportPaths  = 12
)

// Exit codes. Git treats any nonzero as "stop the commit", but the two failures
// are different events and a done-check must be able to tell them apart:
// 1 is a claim collision under the deny posture, 2 is the gate itself failing.
const (
	commitGateAllow  = 0
	commitGateDenied = 1
	commitGateBroken = 2
)

// EnvCommitPosture selects warn (default), deny, or off.
//
// Default WARN, and it stays warn until there is evidence to move it. Nobody
// has yet measured how often a commit in a shared checkout legitimately touches
// a peer's claimed scope — a deliberate handoff looks exactly like a mistake
// from here — and shipping deny first would be enforcing against an unmeasured
// false-positive rate. Flipping it is one environment variable when that
// measurement exists.
const EnvCommitPosture = "BUDDY_COMMIT_GATE"

// EnvCommitSkip turns the gate off for one command, ahead of everything else.
//
// A separate switch from the posture on purpose: a kill switch has to work when
// the thing it disables is the thing that is broken, so it is read first and
// answers before any ledger, git call or identity lookup can fail.
const EnvCommitSkip = "BUDDY_COMMIT_GATE_SKIP"

type posture int

const (
	postureWarn posture = iota
	postureDeny
	postureOff
)

func cmdCommitGate(args []string, env Env) int {
	// The kill switch answers FIRST, before flag parsing and before any lookup.
	// A switch that works only while the command is otherwise healthy is not a
	// kill switch.
	if env.getenv(EnvCommitSkip) == "1" {
		return commitGateAllow
	}

	fs := flag.NewFlagSet("commit-gate", flag.ContinueOnError)
	// flag prints its own diagnostics containing the offending argument
	// VERBATIM, and an argument may contain a newline — which would put an
	// unindented line of somebody else's choosing into this gate's output,
	// where it can read as the gate's own verdict. Discard flag's copy and
	// render a fenced one.
	fs.SetOutput(io.Discard)
	var session string
	sessionFlag(fs, &session)
	deny := fs.Bool("deny", false, "refuse the commit on a collision instead of warning")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(env.Stderr, "buddy commit-gate: %s\n  usage: buddy commit-gate [--session <id>] [--deny]\n",
			fence.Line(err.Error(), 256))
		return commitGateBroken
	}
	if fs.NArg() > 0 {
		// Go's flag package STOPS at the first operand, so a stray word is not
		// merely ignored — it swallows every flag after it. `commit-gate typo
		// --deny` would quietly choose the warn posture the operator was trying
		// to override, and report a collision while exiting 0.
		fmt.Fprintf(env.Stderr, "buddy commit-gate: unexpected argument %s — flags after it would be IGNORED\n"+
			"  usage: buddy commit-gate [--session <id>] [--deny]\n",
			strconv.Quote(fence.Line(fs.Arg(0), 128)))
		return commitGateBroken
	}

	pos := postureWarn
	badPosture := ""
	switch v := env.getenv(EnvCommitPosture); v {
	case "", "warn":
	case "deny":
		pos = postureDeny
	case "off":
		pos = postureOff
	default:
		// HELD, not printed yet. A typo in the posture is worth reporting —
		// somebody may believe they turned enforcement on — but the no-ledger
		// arm below has to stay silent, and a repo that never enabled the
		// feature must not start talking on every commit because of a stray
		// variable in the environment.
		badPosture = v
	}
	if *deny {
		pos = postureDeny
	}
	if pos == postureOff {
		return commitGateAllow
	}

	// LEDGER FIRST, before identity and before git. The two verdicts must stay
	// apart: provably no ledger means the feature was never turned on here and
	// the gate is silent, while a ledger that exists and cannot be read is a
	// safety mechanism that has stopped working and must say so. Resolving
	// identity first would let an unresolvable session take an early exit
	// straight past an unreadable ledger, which is the same silent-allow arm
	// this project keeps refusing to build.
	st, _, err := openRepo(env.Cwd, env)
	if errors.Is(err, errNoLedger) {
		return commitGateAllow
	}
	if err != nil {
		// The ledger lives in the git COMMON dir, not in `.git` — a linked
		// worktree's `.git` is a file, so advice naming `.git/buddy.db` sends
		// the reader somewhere the database is not.
		fmt.Fprintf(env.Stderr, "buddy commit-gate: ledger unavailable (%s); refusing the commit.\n"+
			"  Fix it, or delete buddy.db from `git rev-parse --git-common-dir` to turn the\n"+
			"  Buddy System off in this repo.\n"+
			"  Deliberate bypass for this one commit: git commit --no-verify\n",
			fence.Line(err.Error(), 512))
		return commitGateBroken
	}
	defer st.Close()
	if badPosture != "" {
		fmt.Fprintf(env.Stderr, "buddy commit-gate: unknown %s=%q (want warn, deny or off); using warn\n",
			EnvCommitPosture, fence.Line(badPosture, 64))
	}

	// Identity is BEST-EFFORT and its failure is never fatal. whoAmI reports an
	// unresolvable session as an error naming the remedy, which is right for an
	// operator verb typed at a prompt and wrong here: the commonest caller is a
	// human whose terminal carries no session id at all.
	//
	// The empty string is not a neutral value to pass on — OwnerOf excludes
	// `session_id <> ?`, so "" excludes nothing and every open claim becomes a
	// hit, including the committer's own. That is the safe direction (it cannot
	// miss a collision) but it is NOT a peer collision, and the report has to
	// say which of the two it is rather than accusing somebody of colliding
	// with themselves.
	me, idErr := whoAmI(st, Env{Stdin: env.Stdin, Stdout: env.Stdout, Cwd: env.Cwd, Now: env.Now, Getenv: env.Getenv}, session)
	mine := me.SessionID

	paths, err := stagedPaths(env.Cwd)
	if err != nil {
		fmt.Fprintf(env.Stderr, "buddy commit-gate: could not read the staged paths (%s); refusing the commit.\n"+
			"  Deliberate bypass for this one commit: git commit --no-verify\n", fence.Line(err.Error(), 512))
		return commitGateBroken
	}
	if len(paths) == 0 {
		return commitGateAllow
	}

	// Grouped by claim rather than listed per path: twenty paths under one
	// scope is ONE conflict with one person, and printing the same slug,
	// description and owner twenty times buries that.
	type group struct {
		claim store.ClaimInfo
		paths []string
	}
	var order []string
	hits := 0
	groups := map[string]*group{}
	for _, p := range paths {
		c, held, err := st.OwnerOf(p, mine)
		if err != nil {
			fmt.Fprintf(env.Stderr, "buddy commit-gate: ledger read failed (%s); refusing the commit.\n"+
				"  Deliberate bypass for this one commit: git commit --no-verify\n", fence.Line(err.Error(), 512))
			return commitGateBroken
		}
		if !held {
			continue
		}
		g, ok := groups[c.ClaimID]
		if !ok {
			g = &group{claim: c}
			groups[c.ClaimID] = g
			order = append(order, c.ClaimID)
		}
		g.paths = append(g.paths, p)
		hits++
	}
	if len(order) == 0 {
		// Silent on the clean path, INCLUDING when identity could not be
		// resolved. An identity complaint on every commit that collides with
		// nothing is exactly the warning-on-everything failure this gate is
		// built to avoid.
		return commitGateAllow
	}

	now := nowOf(env)
	w := env.Stderr
	if mine == "" {
		fmt.Fprintf(w, "\nbuddy commit-gate: could not tell which session is committing, so this cannot say\n"+
			"whether these claims are yours. %s covered by an open claim:\n\n", headline(hits, len(order)))
	} else {
		fmt.Fprintf(w, "\nbuddy commit-gate: %s inside ANOTHER session's open claim:\n\n", headline(hits, len(order)))
	}

	shown := order
	if len(shown) > maxReportClaims {
		shown = shown[:maxReportClaims]
	}
	for _, id := range shown {
		g := groups[id]
		for i, p := range g.paths {
			if i == maxReportPaths {
				fmt.Fprintf(w, "  ...and %d more path(s) under the same claim\n", len(g.paths)-maxReportPaths)
				break
			}
			// QUOTED, and behind a fixed label. Fencing stops a path forging a
			// newline, but not from occupying a line that reads exactly like
			// one of this report's own rows: scopes are exact paths, so a claim
			// can name a single file, and a file called
			// `...and 99 more path(s) under the same claim` then prints as a
			// byte-identical copy of the truncation notice below it. Quoting
			// puts every path inside delimiters it cannot escape, and the label
			// makes a data row structurally unlike a syntax row.
			fmt.Fprintf(w, "  path %s\n", strconv.Quote(fence.Line(p, 512)))
		}
		c := g.claim
		fmt.Fprintf(w, "      claim %q held by %s (%s)\n", fence.Line(c.Slug, 128),
			fence.Line(c.Owner.Label, 64), claimState(c, now))
		fmt.Fprintf(w, "      scope %s%s — %s\n\n",
			sharedWord(c.Shared), fence.Line(strings.Join(c.Scopes, ", "), 512), fence.Line(c.Desc, 512))
	}
	if len(order) > maxReportClaims {
		fmt.Fprintf(w, "  ...and %d more claim(s) not shown\n\n", len(order)-maxReportClaims)
	}

	if mine == "" && idErr != nil {
		// The remedy, only where it is actionable — attached to a real finding
		// rather than printed on its own as a standing grievance.
		fmt.Fprintf(w, "who is committing could not be determined: %s\n\n", fence.Line(idErr.Error(), 512))
	}
	fmt.Fprint(w, "Coordinate with the owner before committing, or have them `buddy release <slug>`.\n"+
		"`buddy ls` lists open claims; the operator can `buddy sweep --force` a claim whose\n"+
		"session is gone. This is a claims check, not proof of authorship.\n")

	if pos == postureDeny && mine != "" {
		fmt.Fprintf(w, "\nREFUSING the commit (%s=deny). Deliberate bypass: git commit --no-verify\n", EnvCommitPosture)
		return commitGateDenied
	}
	if pos == postureDeny {
		// Deny was asked for and is being withheld, so say so. Enforcing
		// against an unidentified committer would refuse a human's commit for
		// the crime of not being an agent.
		fmt.Fprintf(w, "\nNOT refusing: %s=deny enforces only when the committing session is known.\n", EnvCommitPosture)
		return commitGateAllow
	}
	if mine == "" {
		// Pointing an unidentified committer at the deny posture would be a
		// remedy that does nothing: deny enforces only when the session is
		// known, so the first step is being identifiable at all.
		fmt.Fprintf(w, "\nWarning only — the commit proceeds. %s=deny would NOT refuse this one:\n"+
			"it enforces only when the committing session is known (export %s=<id>).\n",
			EnvCommitPosture, EnvSession)
		return commitGateAllow
	}
	fmt.Fprintf(w, "\nWarning only — the commit proceeds. Set %s=deny to refuse instead.\n", EnvCommitPosture)
	return commitGateAllow
}

// headline counts paths and claims separately, because "3 staged paths, 1
// claim" is one conversation with one person and "3 staged paths, 3 claims" is
// three — a single total conflates them.
func headline(paths, claims int) string {
	p, c := "paths", "claims"
	if paths == 1 {
		p = "path"
	}
	if claims == 1 {
		c = "claim"
	}
	return fmt.Sprintf("%d staged %s in %d %s", paths, p, claims, c)
}

// claimState describes the claim the way the reader has to act on it. An open
// claim whose owner has ENDED needs `buddy sweep --force`, not a conversation,
// and calling that owner "live" sends the reader to talk to nobody. OwnerOf
// applies no liveness or staleness filter of its own, so this is the only place
// the distinction gets made.
func claimState(c store.ClaimInfo, now time.Time) string {
	switch {
	case !c.Owner.Live():
		return "session ENDED — nobody will answer; `buddy sweep --force` releases it"
	case c.Stale(now):
		return "live but SILENT for " + age(now, c.Owner.LastSeen) + " — it may be gone"
	default:
		return "live, last seen " + age(now, c.Owner.LastSeen)
	}
}

// stagedPaths lists the repo-relative paths of the pending commit.
//
// Every flag here is load-bearing, and three were settled by measuring git
// rather than assuming:
//
//   - `-z`, because the default format C-QUOTES any path with a space, a quote
//     or a non-ASCII byte (`"b/caf\303\251.txt"`). A quoted path matches no
//     scope in the ledger, so the gate would go quiet on exactly the filenames
//     nobody tests with.
//   - `--no-renames`, because rename detection reports only the DESTINATION of
//     a rename. Moving a file OUT of a peer's claimed scope is a write to that
//     scope, and with detection on it is invisible. Off, the source arrives as
//     a delete and the destination as an add — both adjudicated.
//   - `-c diff.relative=false`, because a user who sets diff.relative in their
//     own config gets cwd-relative output, and `z.txt` instead of
//     `deep/dir/z.txt` matches no scope. Measured: it silently changes the
//     answer.
//   - `--no-optional-locks` so the listing never takes .git/index.lock, which
//     the committing git is holding at this moment.
//   - `core.fsmonitor` pinned off because it is a PATH TO A PROGRAM that git
//     executes, read from .git/config — a file no claim or gate protects.
//
// There is deliberately no `--diff-filter`: an add, a modify, a delete and a
// typechange are all writes to the path, and all four belong to whoever holds
// the scope.
//
// No explicit HEAD, and no empty-tree special case for the first commit:
// `diff --cached` already diffs against the empty tree when HEAD does not exist
// (measured on git 2.50.1).
func stagedPaths(dir string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commitGateBudget)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git",
		"-c", "core.fsmonitor=false", "-c", "diff.relative=false",
		"--no-optional-locks", "diff", "--cached", "--name-only", "--no-renames",
		"--ignore-submodules=none", "-z")
	// Run IN dir rather than with `-C dir`, because GIT_INDEX_FILE arrives
	// relative (".git/index") on an ordinary commit and is resolved against the
	// working directory.
	cmd.Dir = dir
	cmd.Env = commitGitEnv()
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("git diff --cached: %w", err)
	}
	out, readErr := io.ReadAll(io.LimitReader(pipe, int64(maxStagedBytes)+1))
	io.Copy(io.Discard, pipe)
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("git diff --cached: %w", err)
	}
	if readErr != nil {
		return nil, fmt.Errorf("git diff --cached: %w", readErr)
	}
	if len(out) > maxStagedBytes {
		// Refusing beats truncating. A truncated list is a list the gate then
		// reports as conflict-free, which is the silent-allow failure again.
		return nil, fmt.Errorf("staged path list exceeds %d bytes", maxStagedBytes)
	}
	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// commitGitEnv pins the child's locale and strips config injection while
// KEEPING the rest of git's exported environment.
//
// This deliberately diverges from cleanGitEnv, which drops the whole GIT_*
// namespace, and the difference is the entire correctness of this command. In a
// hook, git's environment is how git STATES what is being committed: a partial
// commit (`git commit -- <path>`) builds a TEMPORARY index and points
// GIT_INDEX_FILE at it (measured: `.git/next-index-28362.lock`). Strip that and
// the child reads the real index instead, reporting files that are staged but
// NOT part of this commit — a warning naming a path the commit does not touch,
// which is worse than no warning. Measured on git 2.50.1: with the variable
// inherited the listing is exactly the committed path; with it stripped, two.
//
// GIT_CONFIG* still goes, because it is the one part of that environment that
// is configuration rather than a statement of what is being committed, and
// leaving it would let a peer who can write .git/config reinstate the
// fsmonitor program the -c flags above exist to pin off.
func commitGitEnv() []string {
	out := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		k, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(k, "GIT_CONFIG") || k == "LC_ALL" {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "LC_ALL=C")
}
