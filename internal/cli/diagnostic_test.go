package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JsizzleR/buddy-system/internal/harness"
)

func diagnosticOK(t *testing.T, f *fixture, cwd string, args ...string) diagnosticReport {
	t.Helper()
	out, errw, code := f.run(t, cwd, "", append([]string{"diagnose"}, args...)...)
	var r diagnosticReport
	if code != 0 || errw != "" || json.Unmarshal([]byte(out), &r) != nil {
		t.Fatalf("diagnose: exit=%d stdout=%s stderr=%s", code, out, errw)
	}
	if r.Schema != harness.DiagnosticSchema || r.Readiness != "not_established" {
		t.Fatalf("wrong envelope: %+v", r)
	}
	return r
}

func TestDiagnoseDoesNotCreateOrMigrateLedger(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	path := filepath.Join(f.repo, ".git", "buddy.db")
	if r := diagnosticOK(t, f, f.repo); r.Ledger.State != "absent" {
		t.Fatalf("absent: %+v", r)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("diagnostic created ledger")
	}
	f.initAndHello(t)
	// Header user_version is the compatibility discriminator. Downgrading just
	// this copy tests that even a perfectly readable old schema is not migrated.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b[60], b[61], b[62], b[63] = 0, 0, 0, 14
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	r := diagnosticOK(t, f, f.wtB)
	if r.Ledger.State != "migration_required" || r.Ledger.SchemaVersion == nil || *r.Ledger.SchemaVersion != 14 {
		t.Fatalf("old ledger through linked worktree: %+v", r)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, after) {
		t.Fatal("diagnostic migrated source ledger")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Fatalf("created source %s", suffix)
		}
	}
}

func TestDiagnoseCapturedIdentityAndWorktree(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	id := lifecycleOK(t, f, "begin", "--session", "diagnostic-worker")
	saved := harness.Saved{Schema: harness.Schema, Identity: harness.Identity{Schema: id.Schema, Harness: id.Harness, SessionID: id.SessionID, Incarnation: id.Incarnation}, Launch: harness.Launch{Worktree: f.repo, HarnessVersion: "0.160.0", HookConfig: "/missing-config"}}
	path := filepath.Join(t.TempDir(), "saved.json")
	write := func() {
		t.Helper()
		b, _ := json.Marshal(saved)
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	ledgerPath := filepath.Join(f.repo, ".git", "buddy.db")
	before, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	r := diagnosticOK(t, f, f.repo, "--receipt", path)
	if r.Ledger.Identity != "matches_snapshot" || r.ReceiptBinding != "worktree_matches" {
		t.Fatalf("positive receipt: %+v", r)
	}
	r = diagnosticOK(t, f, f.wtB, "--receipt", path)
	if r.Ledger.Identity != "unknown" || r.ReceiptBinding != "worktree_mismatch" {
		t.Fatalf("wrong worktree: %+v", r)
	}
	saved.Identity.Incarnation = "old-token"
	write()
	r = diagnosticOK(t, f, f.repo, "--receipt", path)
	if r.Ledger.Identity != "incarnation_mismatch" {
		t.Fatalf("stale receipt: %+v", r)
	}
	after, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("diagnose changed session/inbox/claims state")
	}
}

func TestDiagnoseHelpAndInvalidInputDoNotReadStdinOrRunNative(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	// A panic reader proves the command is not accidentally treated as a hook.
	var out, errw bytes.Buffer
	if code := Run([]string{"diagnose", "--help"}, Env{Cwd: "/missing", Stdin: diagnosticPanicReader{}, Stdout: &out, Stderr: &errw}); code != 0 || !strings.Contains(out.String(), "--receipt") {
		t.Fatalf("help: %d %s %s", code, out.String(), errw.String())
	}
	for _, args := range [][]string{{"--harness", "other"}, {"--unknown"}, {"stray"}, {"--launch", "/missing"}, {"--receipt", "/missing", "--launch", "/missing"}, {"--evidence", "/missing"}} {
		if out, _, code := f.run(t, f.repo, "", append([]string{"diagnose"}, args...)...); code == 0 || out != "" {
			t.Fatalf("accepted malformed args %v: %s", args, out)
		}
	}
	for _, kind := range []string{"codex", "claude"} {
		r := diagnosticOK(t, f, f.repo, "--harness", kind)
		if r.Harness.InstalledVersion.State != "unknown" || r.Harness.RuntimeCapabilities.State != "unknown" || len(r.Harness.AdapterCapabilities) == 0 {
			t.Fatalf("offline capabilities: %+v", r.Harness)
		}
	}
}

type diagnosticPanicReader struct{}

func (diagnosticPanicReader) Read([]byte) (int, error) { panic("diagnostic read stdin") }
