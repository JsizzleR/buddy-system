//go:build darwin

package cli

import (
	"os"
	"os/exec"
	"testing"
)

// The real sysctl, read once: this test's OWN child shell must be found under
// this test's own pid (never under a claude process — the suite runs under
// one, and a fixture must not read it; see Env.Anchor). The positive control
// for readProcTable: every other JOBS test injects the table.
func TestReadProcTableFindsOurOwnChildShell(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "sleep 30; true") // two commands, so sh cannot exec sleep in its place
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	table, ok := readProcTable()
	if !ok || len(table) == 0 {
		t.Fatal("kern.proc.all read nothing")
	}
	got := shellsUnder(os.Getpid(), table, nil)
	found := false
	for _, p := range got {
		found = found || p.PID == cmd.Process.Pid
	}
	if !found {
		t.Fatalf("our child sh (pid %d) is not among the shells under us: %+v", cmd.Process.Pid, got)
	}
}
