package cli

// The JOBS line on `buddy status` / `buddy who` (D-065): the shells still
// running under a session's harness process, counted, with their pids and
// ages. An observation for the operator and a handing-off orchestrator; it
// grants, refuses and ends nothing (invariant 10, D-027).
//
// THE FAILURE. An outgoing orchestrator told its operator "this session holds
// nothing and the checkout is clean, so you can close it" while two of its
// own shells were still running as children of its claude process: a gate
// script eight minutes in, writing the summary its successor was told to
// poll, and a 42-minute watcher on a remote nightly whose only output was an
// echo to the session about to close (measured 2026-09-29). EXIT was right —
// it speaks for the ledger — and was read as speaking for the session. The
// skill now makes the handoff account for its running jobs (3a0a450); this
// line is the instrument that shows one it forgot.
//
// WHAT COUNTS. Direct children of the session's REGISTERED harness process
// (D-025's session_procs, alive under its recorded birth time) whose
// executable is a shell. Measured on this machine: a Bash tool call runs as
// `/bin/zsh -c …`, a direct child of claude, in its OWN process group — so
// D-061's process-group walk cannot see it, and this reads the whole process
// table (kern.proc.all) and filters by parent. D-061 cut that scan for a
// DIFFERENT purpose (ending its own test binaries); here it is the only way to
// find a child in another group. The same claude's other children — MCP
// servers, a language server, `caffeinate` — are not shells and are not
// counted. The report's own shell is (it is a Bash tool call too), so the
// caller's ancestor chain is excluded. A hook or status line the harness
// happens to be running when the table is sampled is a shell and is counted:
// the line says "when sampled" and nothing more. A job that exec'd into
// another program, or runs under a wrapper, is NOT a direct-child shell and
// is not seen, so the empty arm says so rather than "nothing is running"
// (Codex code pass).
//
// THE ANCHOR IS RE-IDENTIFIED IN THE TABLE. procAlive checks the registered
// pid's start time, and the table is a second read: a pid reused between
// the two would lend this session another process's shells (Codex code
// pass). So the table's own row for the anchor must carry the registered
// start time, or the line cannot say.
//
// WHAT IT PRINTS. Pids and ages, never a command line: argv is whatever the
// session typed, which may carry a secret, and a line built from it would be
// peer text in another session's context. A platform that cannot read the
// table, or a session with no live registered process, says it cannot say,
// never "none" — "no row means unknown" (D-016).

import (
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// procEntry is one row of the process table: what the JOBS line reads.
type procEntry struct {
	PID, PPID int
	Comm      string // the kernel's name for the executable (a basename)
	Born      int64  // start time, microseconds since the epoch
}

// shellNames are the executables counted as a session's jobs.
var shellNames = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true}

// shellsUnder returns the shells whose parent is anchor, minus the pids in
// skip, oldest first.
func shellsUnder(anchor int, table []procEntry, skip map[int]bool) []procEntry {
	var out []procEntry
	for _, p := range table {
		if p.PPID != anchor || skip[p.PID] {
			continue
		}
		if shellNames[strings.TrimPrefix(path.Base(p.Comm), "-")] {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Born < out[j].Born })
	return out
}

// selfChain is the caller and its ancestors as the table records them,
// bounded as the anchor walk is.
func selfChain(self int, table []procEntry) map[int]bool {
	parent := make(map[int]int, len(table))
	for _, p := range table {
		parent[p.PID] = p.PPID
	}
	chain := map[int]bool{}
	for hop := 0; hop < maxAnchorHops && self > 1 && !chain[self]; hop++ {
		chain[self] = true
		self = parent[self]
	}
	return chain
}

// jobsLine renders the JOBS line for a LIVE session, or "" when there is
// nothing to say (an ended session: EXIT already says so).
func jobsLine(env Env, st *store.Store, si store.SessionInfo) string {
	if !si.Live() {
		return ""
	}
	procs, err := st.SessionProcs()
	if err != nil {
		return "JOBS         cannot say: the ledger's process register could not be read"
	}
	var anchors []store.ProcRef
	for _, p := range procs[si.SessionID] {
		if env.procAlive(p) {
			anchors = append(anchors, p)
		}
	}
	if len(anchors) == 0 {
		return "JOBS         cannot say: no live harness process is registered for it"
	}
	table, self, ok := env.procTable()
	if !ok {
		return "JOBS         cannot say: this platform's process table is not read"
	}
	byPID := make(map[int]procEntry, len(table))
	for _, p := range table {
		byPID[p.PID] = p
	}
	skip := selfChain(self, table)
	now := nowOf(env)
	var shells []procEntry
	var pids []string
	for _, a := range anchors {
		if e, found := byPID[a.PID]; !found || (a.Born != 0 && e.Born != a.Born) {
			return fmt.Sprintf("JOBS         cannot say: its harness process (pid %d) was not the registered one when the table was sampled", a.PID)
		}
		pids = append(pids, fmt.Sprint(a.PID))
		shells = append(shells, shellsUnder(a.PID, table, skip)...)
	}
	under := "its harness process (pid " + strings.Join(pids, ", ") + ")"
	if len(shells) == 0 {
		return "JOBS         no shell found directly under " + under + " when sampled, besides any running this report (a job that exec'd into another program, or runs under a wrapper, is not seen)"
	}
	items := make([]string, 0, len(shells))
	for _, s := range shells {
		items = append(items, fmt.Sprintf("pid %d %s", s.PID, age(now, time.UnixMicro(s.Born))))
	}
	return fmt.Sprintf("JOBS         %d shell(s) running directly under %s when sampled: %s — the ledger does not track them, and closing its pane may end them",
		len(shells), under, joinCapped(items, 256))
}

// procTable reads the process table and the caller's own pid, or reports
// that this platform cannot.
func (e Env) procTable() ([]procEntry, int, bool) {
	if e.ProcTable != nil {
		return e.ProcTable()
	}
	table, ok := readProcTable()
	return table, os.Getpid(), ok
}
