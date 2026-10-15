package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// codedErr is an error carrying an SQLite-style result code, as modernc's does.
type codedErr struct {
	code int
	msg  string
}

func (e codedErr) Error() string { return e.msg }
func (e codedErr) Code() int     { return e.code }

// F1: a ledger this process may not write gets its cause and fix named, on
// the verb path only, and the gate still denies over it (invariants 2, 3).
//
// File modes stand in for a sandbox here, so the suite runs everywhere; the
// OS answers EACCES where seatbelt answers EPERM, and both count.
// ledgeraccess_darwin_test.go runs the real seatbelt.

const ledgerCause = "cause: this process may not write the ledger"

// lockLedger makes the fixture's ledger unwritable to this process, and with
// dirToo its directory as well, so SQLite cannot create the -shm beside it
// either: that is the shape with no peer holding the ledger open, which fails
// at open with CANTOPEN. Without dirToo a read succeeds and a write fails
// with READONLY. Restored at cleanup so TempDir can be removed.
func lockLedger(t *testing.T, f *fixture, dirToo bool) string {
	t.Helper()
	gitDir := filepath.Join(f.repo, ".git")
	ledger := filepath.Join(gitDir, ledgerName)
	if err := os.Chmod(ledger, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(ledger, 0o644) })
	if dirToo {
		if err := os.Chmod(gitDir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(gitDir, 0o755) })
	}
	// The guard is armed only if the OS really refuses: as root it would not,
	// and every assertion below would then pass for the wrong reason. Probed
	// here directly, not through writeDenied, which is under test.
	if fh, err := os.OpenFile(ledger, os.O_RDWR, 0); err == nil {
		fh.Close()
		t.Skip("file modes do not refuse this process a write; the seatbelt leg covers the case")
	}
	return ledger
}

func TestLedgerAccessNamesCauseAndFix(t *testing.T) {
	boundedParallel(t)
	cases := []struct {
		name    string
		dirToo  bool
		sandbox string
		args    []string
		want    []string // substrings of stderr, in addition to the cause line
	}{
		// The trial's shape: no peer holds the ledger open, so even a verb
		// that only reads fails at open. Under file modes SQLite says
		// READONLY_DIRECTORY (1544) there; seatbelt makes it CANTOPEN (14),
		// which the darwin leg pins.
		{"read verb fails at open", true, "", []string{"status", "--session", "sess-a"},
			[]string{"inspect ledger schema version", "require_escalated", "read-only ones included"}},
		// A peer holding it open: the read would pass, the write does not.
		{"write verb is refused", false, "", []string{"claim", "w", "--session", "sess-a", "--desc", "d", "--scope", "pkg"},
			[]string{"readonly database", "require_escalated"}},
		// Through a verb that flattens its error with fencedErr first.
		{"flattened error still explained", false, "", []string{"msg", "bravo", "hi"},
			[]string{"readonly database"}},
		{"names the Codex sandbox when it says so", true, "seatbelt", []string{"status", "--session", "sess-a"},
			[]string{"$CODEX_SANDBOX=seatbelt"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.initAndHello(t)
			f.env[EnvSession] = "sess-a"
			if tc.sandbox != "" {
				f.env[envCodexSandbox] = tc.sandbox
			}
			// Positive control: the same verb against a writable ledger
			// succeeds and says nothing about access, so the text below is
			// caused by the lock and not by the verb.
			args := tc.args
			if args[0] == "claim" {
				args = append([]string{}, args...)
				args[1] = "control"
			}
			if _, errw, code := f.run(t, f.repo, "", args...); code != 0 || strings.Contains(errw, ledgerCause) {
				t.Fatalf("control on a writable ledger: %d %q", code, errw)
			}
			ledger := lockLedger(t, f, tc.dirToo)
			_, errw, code := f.run(t, f.repo, "", tc.args...)
			if code == 0 {
				t.Fatalf("an unwritable ledger must still fail the verb: %q", errw)
			}
			for _, w := range append([]string{ledgerCause, ledger}, tc.want...) {
				if !strings.Contains(errw, w) {
					t.Errorf("stderr lacks %q:\n%s", w, errw)
				}
			}
		})
	}
}

// The explanation is for an ACCESS failure on a ledger this process may not
// write, and for nothing else: not a different error over a locked ledger,
// and not an access-shaped error over a ledger that is writable.
func TestLedgerAccessSaysNothingOtherwise(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	env := Env{Cwd: f.repo, Getenv: func(k string) string { return f.env[k] }}
	shaped := errors.New("inspect ledger schema version: unable to open database file (14)")
	if got := explainLedgerAccess(shaped, env); got != shaped {
		t.Fatalf("a writable ledger must not be blamed: %v", got)
	}
	lockLedger(t, f, false)
	if got := explainLedgerAccess(shaped, env); !strings.Contains(got.Error(), ledgerCause) {
		t.Fatalf("control: the same error over the locked ledger is explained: %v", got)
	}
	// SQLite's code is read from the chain even when its message differs.
	coded := codedErr{code: 14, msg: "disk I/O shaped differently"}
	if got := explainLedgerAccess(coded, env); !strings.Contains(got.Error(), ledgerCause) {
		t.Fatalf("a CANTOPEN code in the chain is explained whatever its text: %v", got)
	}
	other := errors.New(`no live session matches "nobody"`)
	if got := explainLedgerAccess(other, env); got != other {
		t.Fatalf("an error that is not about access must come back unchanged: %v", got)
	}
	// An open refused for another reason is not a sandbox's doing: a ledger
	// path that is a directory answers EISDIR.
	g := newFixture(t)
	g.initAndHello(t)
	gl := filepath.Join(g.repo, ".git", ledgerName)
	if err := os.Remove(gl); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(gl, 0o755); err != nil {
		t.Fatal(err)
	}
	genv := Env{Cwd: g.repo, Getenv: env.Getenv}
	if got := explainLedgerAccess(shaped, genv); got != shaped {
		t.Fatalf("EISDIR must not be blamed on a sandbox: %v", got)
	}
	// Outside any repo there is no ledger to blame.
	out := Env{Cwd: t.TempDir(), Getenv: env.Getenv}
	if got := explainLedgerAccess(shaped, out); got != shaped {
		t.Fatalf("no repo, no explanation: %v", got)
	}
}

// Invariant 3 over the same lock: the gate reads a ledger it cannot open as
// UNREADABLE and denies, and the explanation is not routed through it.
func TestLedgerAccessGateStillDenies(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	call := hookJSON("sess-a", f.repo, "Edit", filepath.Join(f.repo, "free.go"))
	if out, _, _ := f.run(t, f.repo, call, "gate"); strings.Contains(out, `"deny"`) {
		t.Fatalf("control: an unclaimed path on a readable ledger is allowed: %s", out)
	}
	lockLedger(t, f, true)
	out, _, _ := f.run(t, f.repo, call, "gate")
	reason, denied := decodeDeny(t, out)
	if !denied {
		t.Fatalf("an unopenable ledger must DENY, never read as feature-off: %q", out)
	}
	if strings.Contains(reason, ledgerCause) {
		t.Fatalf("the gate's verdict text is not where this explanation goes: %q", reason)
	}
}
