//go:build darwin

package cli

// The gate's verdict under a real seatbelt, the Codex trial's condition
// (ledgeraccess_darwin_test.go says why this one-line profile stands in for
// Codex's own). ledgeraccess_gate_test.go holds the runner, the file-mode rows
// and what every row is guarded against; these rows are the same states with
// the sandbox, not a mode, refusing the write (D-240):
//
//	seatbelt deny-write on the git directory, no sidecars    refused, CANTOPEN (14)
//	the same, a peer holds the ledger open                   judged by its claims
//	the same, the peer closed and its sidecars gone          refused, CANTOPEN (14)
//
// They differ from the file-mode rows in three ways worth a row each. The
// ledger file's mode is untouched (0644, as on a writable ledger: the only
// difference from the control is the profile), and a sandbox's refusal to
// create the sidecars reaches SQLite as CANTOPEN where a 0555 directory's
// reaches it as READONLY_DIRECTORY. And a sandboxed session really sits in
// one of these two states, by whether any process on the machine has the
// ledger open at that moment: `buddy wait --block` keeps its store open for
// its whole life, so beside one parked lane it is the judged one. And the
// peer's end shows here where file modes hide it: a peer outside the sandbox
// unlinks its sidecars as it closes, which a 0555 directory refuses to anyone,
// so the third row falls back to the first where its file-mode twin stays
// judged.
//
// The profile is applied to the CHILD only; this process, the peer, stays
// outside it, as a peer session's process would be. What shows the profile
// applied: with no sidecars the same child that was just judged without it is
// refused with it; with a peer, the judged child's write is refused with
// READONLY (8) and EPERM ("operation not permitted": a seatbelt, where a file
// mode answers EACCES).

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLedgerAccessGateVerdictUnderSeatbelt(t *testing.T) {
	boundedParallel(t)
	sb, err := exec.LookPath("sandbox-exec")
	if err != nil {
		t.Skip("no sandbox-exec on this machine")
	}
	seatbelt := func(t *testing.T, g *ledgerGate) ledgerWrap {
		t.Helper()
		// Resolved: the profile matches the path the kernel sees, and TMPDIR
		// is reached through a symlink here (/var -> /private/var).
		gitDir, err := filepath.EvalSymlinks(g.gitDir)
		if err != nil {
			t.Fatal(err)
		}
		return ledgerWrap{
			argv: []string{sb, "-p", `(version 1)(allow default)(deny file-write* (subpath "` + gitDir + `"))`},
			// As Codex sets it in every command its sandbox runs. The gate
			// decides nothing from it (GIVEN 85), and without it here a gate
			// that did would pass every row.
			env:    []string{envCodexSandbox + "=seatbelt"},
			cannot: seatbeltUnapplied,
		}
	}
	rows := []ledgerGateRow{
		{name: "seatbelt, no sidecars", lock: seatbelt,
			sqlite: "unable to open database file (14)"},
		{name: "seatbelt, a peer holds it open", peer: true, lock: seatbelt, judged: true,
			also: []string{"operation not permitted"}},
		// What a live peer was worth: it closes (this process, outside the
		// sandbox, so the close unlinks both sidecars), and the same sandboxed
		// child that was judged a moment before is refused as in the first row.
		{name: "seatbelt, the peer closed and its sidecars went with it", peer: true, peerGone: true, lock: seatbelt,
			sqlite: "unable to open database file (14)"},
	}
	for _, r := range rows {
		t.Run(r.name, r.run)
	}
}
