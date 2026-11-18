package main

// The launch's only safety hook reads its immutable receipt. Native lifecycle
// handlers are intentionally not installed alongside the controller: duplicate
// unfenced hello/bye hooks could lend delayed events a successor's registration.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/JsizzleR/buddy-system/internal/harness"
)

func runHook(args []string, in io.Reader, out, errout io.Writer) error {
	fs := flag.NewFlagSet("buddy-adapter hook", flag.ContinueOnError)
	fs.SetOutput(errout)
	receipt := fs.String("receipt", "", "immutable lifecycle receipt path")
	buddy := fs.String("buddy", "", "absolute Buddy binary")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || !filepath.IsAbs(*receipt) || !filepath.IsAbs(*buddy) {
		return errors.New("hook requires absolute receipt and buddy paths")
	}
	b, err := io.ReadAll(io.LimitReader(in, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(b) > 1<<20 {
		return errors.New("hook input too large")
	}
	var input struct {
		SessionID       string `json:"session_id"`
		Cwd             string `json:"cwd"`
		Event           string `json:"hook_event_name"`
		AgentID         string `json:"agent_id"`
		ParentSessionID string `json:"parent_session_id"`
		ParentThreadID  string `json:"parent_thread_id"`
	}
	if err = json.Unmarshal(b, &input); err != nil {
		return err
	}
	if input.Event != "PreToolUse" || input.AgentID != "" || input.ParentSessionID != "" || input.ParentThreadID != "" {
		return errors.New("adapter hook supports only top-level PreToolUse")
	}
	data, err := os.ReadFile(*receipt)
	if err != nil {
		return err
	}
	// DecodeSaved: the receipt is reread on every call, and a plain decode
	// reads `"own_claim": null`, "" or a case-variant key as off (D-235).
	saved, err := harness.DecodeSaved(data)
	if err != nil {
		return err
	}
	if saved.Schema != harness.Schema || !saved.Identity.Valid() || saved.Identity.SessionID != input.SessionID {
		return errors.New("hook does not match captured worker")
	}
	if err = harness.CheckOwnClaim(saved.Identity.Harness, saved.Launch.OwnClaim); err != nil {
		return err
	}
	posture := saved.Launch.OwnClaimPosture()
	// The safety steps (cwd check, identity check, then gate) share one budget
	// and fail closed when it runs out. The advisory endpoint step between them runs on
	// its own clock, which is not charged to the gate (F-29): it used to share
	// the deadline, so a slow lock-holder on its write transaction made the
	// gate start late or not at all, and the tool call was denied for an
	// observation that is allowed to fail.
	started := time.Now()
	// The cwd check reads the filesystem at a path the worker chose, which
	// can stall (a network mount gone away), and a hook that outlives its
	// native timeout is neither a deny nor exit 2: the tool call goes ahead
	// (internal/harness/claude.go). So it runs against the budget, never
	// ahead of it (Fable cross-check); a stalled check is a refusal.
	judged := make(chan bool, 1)
	// Read before the goroutine starts: a stalled check outlives this call,
	// and must not read the variable a test restores (-race).
	judge := cwdJudge
	go func() { judged <- judge(saved.Launch.Worktree, input.Cwd) }()
	select {
	case ok := <-judged:
		if !ok {
			return errors.New("hook cwd is not judged from the captured worktree's ledger at its root")
		}
	case <-time.After(harness.HookGateBudget):
		return errors.New("hook cwd check did not finish within the gate budget")
	}
	inspectCtx, cancel := context.WithTimeout(context.Background(), harness.HookGateBudget-time.Since(started))
	l := harness.CLILedger{Binary: *buddy, Worktree: saved.Launch.Worktree}
	inspection, err := l.Inspect(inspectCtx, saved.Identity)
	cancel()
	if err != nil {
		return err
	}
	// Any posture but off needs a --buddy whose gate reads it. Checked on the
	// inspection this call already made, so it costs no process, and on EVERY
	// call: a --buddy rebuilt or replaced after launch is caught at its next
	// gated call, not only at launch (D-235, panel lens a).
	if posture != harness.OwnClaimOff && !gateHonours(inspection, posture) {
		return fmt.Errorf("%w: the launch's posture is %s, and this --buddy does not report an own-claim gate (built before D-228?): rebuild it from this checkout, or launch with own_claim off", harness.ErrOwnClaim, posture)
	}
	spent := time.Since(started)
	if saved.Identity.Harness == "claude" {
		// Endpoint discovery is advisory. No exported inbox is observed absence;
		// an unbound/unsupported process is unknown and never an invented socket.
		endpointCtx, endpointCancel := context.WithTimeout(context.Background(), harness.HookEndpointBudget)
		cmd := exec.CommandContext(endpointCtx, *buddy, "adapter", "endpoint", "--session", saved.Identity.SessionID, "--incarnation", saved.Identity.Incarnation)
		cmd.Dir = saved.Launch.Worktree
		// This is the native payload already checked against the captured
		// receipt, not an id inferred from an ancestor or the sender's env.
		cmd.Env = append(os.Environ(), "BUDDY_HARNESS=claude", "CLAUDE_CODE_SESSION_ID="+input.SessionID)
		cmd.Stderr = errout
		// A killed child's inherited stderr must not hold the step open.
		cmd.WaitDelay = harness.HookEndpointBudget
		_ = cmd.Run()
		endpointCancel()
	}
	gateCtx, gateCancel := context.WithTimeout(context.Background(), harness.HookGateBudget-spent)
	defer gateCancel()
	cmd := exec.CommandContext(gateCtx, *buddy, "gate")
	cmd.Dir = saved.Launch.Worktree
	// The receipt's posture LAST, so it wins over an inherited value (exec
	// keeps the last of a duplicated key). It was `off` always (D-228's D9):
	// the qualified contract was deny-or-nothing (check-codex-gate.py's
	// deny()), and qualify-adapters.py writes an unclaimed allowed.txt beside
	// a peer's guarded.txt, armed and uncovered, so a deny refuses a call that
	// suite passes. D-235 answers O4 with a TYPED launch field instead: absent
	// is off, the same bytes as before; deny is the envelope every start tier
	// measures; warn is a Claude worker's only on a build whose records grant
	// TierOwnClaimWarn (the controller refuses it otherwise, and a Codex warn
	// at all). Never the inherited value: it is the coordinator's shell, which
	// nothing records and every qualify script's clean_env strips.
	cmd.Env = append(os.Environ(), "BUDDY_HARNESS="+saved.Identity.Harness, "BUDDY_OWN_CLAIM="+posture)
	cmd.Stdin = bytes.NewReader(b)
	cmd.Stdout = out
	cmd.Stderr = errout
	return cmd.Run()
}

// gateHonours reports whether an `adapter inspect` document says its gate
// honours this own-claim posture (internal/cli/adapter.go's "gate").
func gateHonours(inspection []byte, posture string) bool {
	var v struct {
		Gate struct {
			OwnClaim []string `json:"own_claim"`
		} `json:"gate"`
	}
	if json.Unmarshal(inspection, &v) != nil {
		return false
	}
	for _, p := range v.Gate.OwnClaim {
		if p == posture {
			return true
		}
	}
	return false
}

// judgedFromWorktree reports whether the gate, handed this payload cwd, judges
// the call from the captured worktree's own ledger with the worktree's root as
// its placement root: the only jurisdiction the receipt vouches for (#134).
//
// WHAT HAPPENED. This was `Clean(worktree) == Clean(cwd)`. The payload's cwd
// is the worker's shell directory, so a worker that ran `cd sub` had every
// later gated call refused: the refusal BLOCKS, before the gate runs, and under
// a matcher that sends Bash (`""`, `*`, Codex's) the `cd ..` that would repair
// it was refused too.
// And the lexical Clean let `<worktree>/out/..` through, `out` a link to
// X/deep: equal to the worktree after Clean, X to the kernel, and the gate,
// judging from X, found no ledger and allowed (reproduced, hermetic).
//
// THE RULE has two halves, both PHYSICAL.
//
// 1. Containment: both sides through EvalSymlinks (which applies a `..` to the
// directory a link reached, as the kernel does), then invariant 14's exact
// form. No fold: the adapter links no store, and a fold admits /x/Repo for
// /x/repo on a case-sensitive volume; a case or firmlink alias of the worktree
// stays refused, as before. A relative, empty or unresolvable cwd refuses: an
// empty one is the gate's cue to read its OWN process directory, a relative one
// to resolve against it, and a vanished one has no physical path to contain.
// The payload goes to the gate unchanged; a cwd rewritten to the worktree would
// place `sub/peer.txt` as `peer.txt` against its root and miss the claim.
//
// 2. No repository boundary on the way down. Containment alone was a NEW allow
// (the panel, reproduced): with a cwd in `<worktree>/vendor/gov`, a repository
// with its own ledger, the gate's home is gov's ledger, where the worker is not
// even registered, and a peer's `vendor` claim and `pause all` in the
// worktree's ledger both passed it, where base blocked the call and a call from
// the worktree root is denied. A linked worktree of the worktree's own
// repository, a `.git` symlink to its git directory or a `gitdir:` pointer at
// it, inside, keeps the ledger but moves the placement root, so the same claim
// missed `vendor/same/x.c`. So every directory strictly below the worktree root
// down to the cwd is tested the way git's discovery tests each level, and any
// level git could stop at refuses: a `.git` entry of any type (a directory, a
// gitdir file, a link, even a broken one), or the directory itself a git
// directory by git's NECESSARY condition (a `HEAD` entry, Lstat so a dangling
// symbolic HEAD counts, plus `objects` or a `commondir` redirect), or another
// device (git stops discovery at a filesystem boundary; the gate keeps
// GIT_DISCOVERY_ACROSS_FILESYSTEM on purpose, D-137, and with it set
// `gh stack` from a mount found the mounted repository and skipped the
// gh-stack slot a walked home does not read: panel lens a). Any Lstat error
// but "does not exist" refuses. That test is necessary for git only under the
// gate's own scrub of git's local environment (internal/cli/hookgitenv.go:
// GIT_DIR, GIT_COMMON_DIR, GIT_OBJECT_DIRECTORY, the ceiling): with
// GIT_OBJECT_DIRECTORY set HEAD plus refs is a repository, so that list must
// not be trimmed without revisiting this. A cwd that passes is one whose
// discovery climbs to the worktree root and finds the worktree's own `.git`.
//
// WHAT IT REFUSES THAT IT NEED NOT (recorded open, D-227): a submodule, a
// nested repository with no ledger, an empty `.git` directory git walks past,
// and a Claude `--worktree` lane inside the worktree. D-212 governs the first
// three correctly, but every one of them was blocked before this change too.
// Admitting a nested repository whose ledger is PROVED absent (the operator's
// option B') needs a second copy of D-212's absence proof here, and a copy that
// drifts into "absent" where the gate finds a ledger is this very escape; it
// is revisited when #145 makes every governed ancestor apply. Not a recognizer
// of nested ledgers: Lstat only, no git process, no ledger read.
// cwdJudge is judgedFromWorktree, a variable only so a test can stall it.
var cwdJudge = judgedFromWorktree

func judgedFromWorktree(worktree, cwd string) bool {
	if !filepath.IsAbs(worktree) || !filepath.IsAbs(cwd) {
		return false
	}
	w, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		return false
	}
	c, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return false
	}
	if c == w {
		return true
	}
	if !strings.HasPrefix(c, w+"/") {
		return false
	}
	root, err := os.Stat(w)
	if err != nil {
		return false
	}
	dev, ok := deviceOf(root)
	if !ok {
		return false
	}
	// EvalSymlinks returned a clean path with no link left in it, so each
	// level below is a real directory entry and the walk is over the
	// PHYSICAL path, never the payload's spelling (where `sub/linkgov` ->
	// gov/deeper would hold no `.git` on the way down, and only the IsDir
	// test at the link itself would refuse it).
	level := w
	for _, name := range strings.Split(c[len(w)+1:], "/") {
		level += "/" + name
		if !plainLevel(level, dev) {
			return false
		}
	}
	return true
}

// plainLevel reports that git's discovery, reaching this directory on its way
// up, would not stop here (judgedFromWorktree's half 2).
func plainLevel(dir string, dev uint64) bool {
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() {
		return false
	}
	if d, ok := deviceOf(fi); !ok || d != dev {
		return false
	}
	if present, ok := lstatPresent(dir + "/.git"); !ok || present {
		return false
	}
	head, ok := lstatPresent(dir + "/HEAD")
	if !ok {
		return false
	}
	if !head {
		return true
	}
	for _, name := range []string{"objects", "commondir"} {
		if present, ok := lstatPresent(dir + "/" + name); !ok || present {
			return false
		}
	}
	return true
}

// lstatPresent is Lstat's answer as (present, known): known is false for any
// error but "does not exist", which the caller refuses.
func lstatPresent(path string) (present, known bool) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, true
	}
	return false, errors.Is(err, os.ErrNotExist)
}

// deviceOf is a variable only so a test can stand a level on another device:
// a hermetic test cannot mount one, and the ROOT's side of the comparison
// (stat the worktree, never the cwd) is what a mount directly below the root
// tests (Codex code pass).
var deviceOf = func(fi os.FileInfo) (uint64, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true
}
