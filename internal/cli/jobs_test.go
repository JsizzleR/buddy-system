package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// D-065: the JOBS line counts the shells still running under a session's
// registered harness process. The table below is the tree measured on this
// machine (a Bash tool call is `/bin/zsh`, a direct child of claude; the same
// claude's MCP servers, language server and caffeinate are not shells).

func measuredTable(anchor int) []procEntry {
	return []procEntry{
		{PID: anchor, PPID: 1, Comm: "claude", Born: 1},
		{PID: 10, PPID: anchor, Comm: "npm exec @playwright/mcp@latest", Born: 2},
		{PID: 11, PPID: anchor, Comm: "buddylist", Born: 2},
		{PID: 12, PPID: anchor, Comm: "gopls", Born: 3},
		{PID: 13, PPID: anchor, Comm: "caffeinate", Born: 4},
		{PID: 20, PPID: anchor, Comm: "zsh", Born: 300},       // a background gate script
		{PID: 21, PPID: 20, Comm: "sh", Born: 301},            // its child: not a DIRECT child
		{PID: 22, PPID: anchor, Comm: "/bin/bash", Born: 100}, // a watcher, older
		{PID: 30, PPID: anchor, Comm: "zsh", Born: 900},       // the shell running this report
		{PID: 31, PPID: 30, Comm: "buddy", Born: 901},
		{PID: 40, PPID: 999, Comm: "zsh", Born: 5}, // another session's shell
	}
}

func TestShellsUnderCountsDirectChildShellsOnly(t *testing.T) {
	table := measuredTable(500)
	got := shellsUnder(500, table, selfChain(31, table))
	if len(got) != 2 || got[0].PID != 22 || got[1].PID != 20 {
		t.Fatalf("want the watcher then the gate script, oldest first: %+v", got)
	}
	// Control: without the self chain the report's own shell counts too, so
	// the exclusion above is what removed it.
	if n := len(shellsUnder(500, table, nil)); n != 3 {
		t.Fatalf("without skipping the caller: want 3 shells, got %d", n)
	}
	if n := len(shellsUnder(999, table, nil)); n != 1 {
		t.Fatalf("another anchor's shell belongs to it: got %d", n)
	}
}

func TestSelfChainIsBoundedOnACycle(t *testing.T) {
	table := []procEntry{{PID: 7, PPID: 8}, {PID: 8, PPID: 7}}
	if c := selfChain(7, table); len(c) != 2 || !c[7] || !c[8] {
		t.Fatalf("a cycle ends the walk: %v", c)
	}
}

func TestStatusReportsJobsUnderTheHarnessProcess(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	// Unbound (no registered process): cannot say, never "none".
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-u", f.repo, "", ""), "hello", "--label", "unbound"); code != 0 {
		t.Fatal(errw)
	}
	if out, _, _ := f.run(t, f.repo, "", "who", "unbound"); !strings.Contains(out, "JOBS         cannot say: no live harness process is registered for it\n") {
		t.Fatalf("an unbound session cannot be said to have no jobs:\n%s", out)
	}

	f.asProcess(500, 1, func() {
		if _, errw, code := f.run(t, f.repo, hookJSON("sess-o", f.repo, "", ""), "hello", "--label", "orch"); code != 0 {
			t.Fatal(errw)
		}
	})
	tbl := measuredTable(500)
	started := f.clock.Add(-42 * time.Minute).UnixMicro()
	tbl[5].Born, tbl[7].Born = started, started-60_000_000 // pid 20 at 42m, pid 22 a minute older
	f.procs, f.self = tbl, 31
	out, _, _ := f.run(t, f.repo, "", "who", "orch")
	want := "JOBS         2 shell(s) running directly under its harness process (pid 500) when sampled: pid 22 43m, pid 20 42m — the ledger does not track them, and closing its pane may end them\n"
	if !strings.Contains(out, want) {
		t.Fatalf("want %q in:\n%s", want, out)
	}
	if i, j := strings.Index(out, "JOBS "), strings.Index(out, "EXIT "); i < 0 || j < i {
		t.Fatalf("JOBS sits before EXIT, which speaks for the ledger:\n%s", out)
	}

	// Control: the same session with no shells left says so.
	f.procs = measuredTable(500)[:5]
	if out, _, _ := f.run(t, f.repo, "", "who", "orch"); !strings.Contains(out, "JOBS         no shell found directly under its harness process (pid 500) when sampled") {
		t.Fatalf("no shells:\n%s", out)
	}
	// Codex code pass: the pid reused between the liveness check and the
	// table read. The table's pid 500 is a different process (born 2, not
	// the registered 1), so its shells are not this session's.
	reused := measuredTable(500)
	reused[0].Born = 2
	f.procs = reused
	if out, _, _ := f.run(t, f.repo, "", "who", "orch"); !strings.Contains(out, "JOBS         cannot say: its harness process (pid 500) was not the registered one") {
		t.Fatalf("a reused pid must not lend its shells:\n%s", out)
	}
	// A platform that cannot read the table says so.
	var buf strings.Builder
	Run([]string{"who", "orch"}, Env{Stdout: &buf, Stderr: &buf, Cwd: f.repo, Now: func() time.Time { return f.clock },
		Getenv: func(k string) string { return f.env[k] }, ProcAlive: func(store.ProcRef) bool { return true },
		ProcTable: func() ([]procEntry, int, bool) { return nil, 0, false }})
	if !strings.Contains(buf.String(), "JOBS         cannot say: this platform's process table is not read") {
		t.Fatalf("an unreadable table:\n%s", buf.String())
	}
	// The harness process gone: cannot say, and the ended session prints no JOBS.
	delete(f.alive, 500)
	if out, _, _ := f.run(t, f.repo, "", "who", "orch"); !strings.Contains(out, "JOBS         cannot say: no live harness process") {
		t.Fatalf("a dead anchor:\n%s", out)
	}
}
