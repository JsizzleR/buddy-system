package cli

// The process a hook arrived from, and whether it is still there.
//
// WHY THIS EXISTS (D-025). The SessionEnd hook carries a session id and no
// incarnation, so `bye` could only say "end whichever incarnation of this id
// is live" — and a delayed bye from a dead incarnation ended a LIVE one
// (`claude --resume` keeps the id). The one thing the old and the new
// incarnation provably do not share is the harness PROCESS that spawns their
// hooks. Measured 2026-09-20 on this box, 17 of 17 sampled hook processes
// had the claude process as their DIRECT parent, and `kern.procargs2` names
// it: exec path `.../bin/claude`, argv[0] `claude`. (`p_comm` does NOT — the
// kernel records the basename of the file actually exec'd, which for the
// launcher is a version string such as `2.1.278`; matching on it would never
// anchor. Measured before it was believed.)
//
// THE ANCHOR IS FOUND BY NAME, NOT BY DEPTH. "The hook's parent" breaks the
// moment an operator wraps the hook line in `timeout` or a script: the
// wrapper is short-lived, a session anchored to it is dead by bye time, and
// the Codex design pass showed that the "dead anchor, so end" arm then
// recreates the very defect. Walking up until a process NAMED claude is
// found goes through any wrapper and stops at the harness, and finding no
// such ancestor within 16 hops records NO anchor — the session is unbound
// and its bye behaves as it always did, which is the honest degraded state
// rather than a fence that fires on the wrong process.
//
// THE PID TRAVELS WITH ITS BIRTH TIME. A pid is a recycled number; the
// process's start time as the kernel reports it is not. Liveness is "this
// pid exists AND was born when we recorded" — a recycled pid reads as dead.
//
// Nothing here is identity: identity stays (session_id, incarnation). This
// is the proof `bye` carries of WHICH process is saying it, and the charter
// says so (GIVEN 10, amended by D-025).

import (
	"os"
	"path/filepath"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// harnessNames are the executable basenames a registration is anchored to.
// Only the compiled `claude` binary is known; an npm-installed harness runs
// as `node` and is deliberately NOT matched — a hook written in node would
// anchor to itself, which is the wrapper failure above wearing a different
// name. Such a setup records no anchor and stays unbound.
var harnessNames = map[string]bool{"claude": true}

// maxAnchorHops bounds the walk. Measured depth is 1; a shell and a wrapper
// or two is the most a hook line can add.
const maxAnchorHops = 16

// procInfo is the platform seam: the parent pid, the names the process goes
// by (its exec path and its argv[0], either may be missing), and its start
// time. ok is false when the pid does not exist or the platform cannot
// answer. Implemented in proc_darwin.go; proc_other.go answers false for
// everything, which leaves every session on that platform unbound.

// lastProcErr is the error the most recent procInfo drew from the kernel,
// for procAlive to classify through procGone. Not concurrent: hooks are
// single-threaded and the roster reads pids one at a time.
var lastProcErr error

// anchorProc finds the harness process this hook was spawned by, or reports
// that there is none.
func anchorProc() (store.ProcRef, bool) {
	pid := os.Getppid()
	for hop := 0; hop < maxAnchorHops && pid > 1; hop++ {
		ppid, names, born, ok := procInfo(pid)
		if !ok {
			return store.ProcRef{}, false
		}
		for _, name := range names {
			if harnessNames[filepath.Base(name)] {
				return store.ProcRef{PID: pid, Born: born}, true
			}
		}
		pid = ppid
	}
	return store.ProcRef{}, false
}

// procAlive reports whether the registered process is still the process
// that registered: it exists, and — when a birth time was recorded — it was
// born then. It is the `alive` seam ByeFrom takes, and its failure
// direction matters: a wrong "alive" keeps a session live until the
// operator looks; a wrong "dead" is the delayed-bye defect. So a pid that
// exists with an UNKNOWN birth time (the platform could not say) is alive.
func procAlive(p store.ProcRef) bool {
	if p.PID <= 0 {
		return false
	}
	_, _, born, ok := procInfo(p.PID)
	if !ok {
		// Dead only when the kernel said "no such process"; any other
		// answer is "cannot say", and cannot-say is alive.
		return !procGone(lastProcErr)
	}
	return p.Born == 0 || born == 0 || born == p.Born
}

// terminalHandle is the terminal the harness inherited its environment from,
// as "<provider>:<id>": a herdr pane, else a tmux pane, else nothing. It is
// reported at registration for the OPERATOR — "which window is this row" —
// and buddy never acts on it; the value is fenced wherever it is rendered
// because it is read from an environment and printed into other sessions'
// context. Only a hook-driven hello records it: a hand-run hello from the
// operator's own terminal would record the operator's pane.
func terminalHandle(env Env) string {
	if v := env.getenv("HERDR_PANE_ID"); v != "" {
		return "herdr:" + v
	}
	if v := env.getenv("TMUX_PANE"); v != "" {
		return "tmux:" + v
	}
	return ""
}
