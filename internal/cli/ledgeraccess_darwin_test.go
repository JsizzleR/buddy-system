//go:build darwin

package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// F1 under a real seatbelt, the trial's condition. Measured 2026-10-14: Codex's
// own runner (`codex sandbox -P :workspace`) and this one-line profile fail a
// fresh ledger identically — "inspect ledger schema version: unable to open
// database file (14)" and a `file-write-data` denial on buddy.db — so the
// profile stands in for Codex without launching it. The verb runs in a child
// of this test binary, because a sandbox applies to a whole process.

const ledgerChildEnv = "BUDDY_TEST_LEDGER_CHILD"

// TestLedgerAccessChild is not a test on its own: it is the process the
// seatbelt leg runs under sandbox-exec, and it returns at once otherwise.
func TestLedgerAccessChild(t *testing.T) {
	if os.Getenv(ledgerChildEnv) == "" {
		return
	}
	args := strings.Split(os.Getenv(ledgerChildEnv), "\x1f")
	cwd, _ := os.Getwd()
	os.Exit(Run(args, Env{Stdin: strings.NewReader(""), Stdout: os.Stdout, Stderr: os.Stderr, Cwd: cwd}))
}

func TestLedgerAccessUnderSeatbelt(t *testing.T) {
	boundedParallel(t)
	sb, err := exec.LookPath("sandbox-exec")
	if err != nil {
		t.Skip("no sandbox-exec on this machine")
	}
	f := newFixture(t)
	f.initAndHello(t)
	gitDir, err := filepath.EvalSymlinks(filepath.Join(f.repo, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	profile := `(version 1)(allow default)(deny file-write* (subpath "` + gitDir + `"))`
	run := func(t *testing.T, sandboxed bool, args ...string) (string, int) {
		t.Helper()
		argv := []string{os.Args[0], "-test.run=^TestLedgerAccessChild$", "-test.count=1"}
		if sandboxed {
			argv = append([]string{sb, "-p", profile}, argv...)
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Dir = f.wtB // a linked worktree: the ledger is in ANOTHER directory's .git
		// Only what the verb needs: never the developer's own harness identity,
		// which this suite may well be running under.
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "TMPDIR=" + os.Getenv("TMPDIR"),
			ledgerChildEnv + "=" + strings.Join(args, "\x1f"),
			EnvSession + "=sess-b", envCodexSandbox + "=seatbelt",
		}
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return out.String(), code
	}
	// Positive control, unsandboxed: the child really runs the verb.
	if out, code := run(t, false, "status"); code != 0 || !strings.Contains(out, "bravo") {
		t.Fatalf("control: unsandboxed child: %d %s", code, out)
	}
	out, code := run(t, true, "status")
	if strings.Contains(out, "sandbox_apply") {
		// Already inside a sandbox, which may not nest: the leg cannot run
		// here, and says so rather than passing.
		t.Skipf("sandbox-exec cannot apply a profile here: %s", out)
	}
	if code == 0 {
		t.Fatalf("a seatbelted ledger must fail the verb: %s", out)
	}
	for _, w := range []string{
		"unable to open database file (14)",                         // the trial's error, byte for byte
		"operation not permitted",                                   // EPERM: seatbelt, not file modes
		ledgerCause, "$CODEX_SANDBOX=seatbelt", "require_escalated", // cause and fix
	} {
		if !strings.Contains(out, w) {
			t.Errorf("seatbelted status lacks %q:\n%s", w, out)
		}
	}
}
