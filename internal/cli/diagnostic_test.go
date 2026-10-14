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

// F-31: a Codex hook that runs buddy without BUDDY_HARNESS=codex is classified
// as a Claude hook unless the hook process happens to inherit CODEX_THREAD_ID
// (observed in a tool shell, not documented for hooks), and a legacy lifecycle
// verb then changes a logical session without its token. The docs require the
// marker; diagnose reads a hand-wired Codex hook layer and says which commands
// lack it.
func TestDiagnoseCodexHookMarkers(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	layer := func(t *testing.T, events map[string][]string) string {
		t.Helper()
		hooks := map[string]any{}
		for event, cmds := range events {
			var entries []map[string]any
			for _, c := range cmds {
				entries = append(entries, map[string]any{"type": "command", "command": c})
			}
			hooks[event] = []map[string]any{{"matcher": "^(apply_patch|Bash)$", "hooks": entries}}
		}
		b, err := json.Marshal(map[string]any{"hooks": hooks})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "hooks.json")
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for _, tc := range []struct {
		name      string
		events    map[string][]string
		verdict   string
		unmarked  int
		finding   string
		noFinding bool
	}{
		{"marked gate", map[string][]string{"PreToolUse": {"BUDDY_HARNESS=codex /abs/bin/buddy gate"}}, "marked", 0, "", true},
		{"marked through env", map[string][]string{"PreToolUse": {"env BUDDY_HARNESS='codex' /abs/bin/buddy gate"}}, "marked", 0, "", true},
		{"unmarked gate", map[string][]string{"PreToolUse": {"/abs/bin/buddy gate"}}, "unmarked", 1, "classified as a Claude hook", false},
		{"one of two unmarked", map[string][]string{"PreToolUse": {"BUDDY_HARNESS=codex /abs/bin/buddy gate", "/abs/bin/buddy gate"}}, "unmarked", 1, "classified as a Claude hook", false},
		{"unmarked lifecycle", map[string][]string{"PostToolUse": {"/abs/bin/buddy beat"}}, "unmarked", 1, "without its captured token", false},
		{"marked lifecycle is refused by design", map[string][]string{"Stop": {"BUDDY_HARNESS=codex /abs/bin/buddy bye"}}, "marked", 0, "refused", false},
		{"marked claude in a Codex layer", map[string][]string{"PreToolUse": {"BUDDY_HARNESS=claude /abs/bin/buddy gate"}}, "unmarked", 1, "names claude", false},
		{"no buddy command", map[string][]string{"PreToolUse": {"/usr/bin/true"}}, "no_buddy_command_recognized", 0, "no PreToolUse buddy gate", false},
		{"a command word that is not a fixed literal", map[string][]string{"PreToolUse": {`"$BUDDY" gate`}}, "unrecognized", 0, "not a fixed literal", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := diagnosticOK(t, f, f.repo, "--codex-hooks", layer(t, tc.events))
			d := r.CodexHooks
			if d == nil || d.Verdict != tc.verdict || d.Unmarked != tc.unmarked || len(d.SHA256) != 64 {
				t.Fatalf("got %+v", d)
			}
			var findings []string
			for _, c := range d.Commands {
				if c.Finding != "" {
					findings = append(findings, c.Finding)
				}
			}
			joined := strings.Join(findings, " | ")
			if tc.noFinding && joined != "" || !tc.noFinding && !strings.Contains(joined, tc.finding) {
				t.Fatalf("findings %q, want %q", joined, tc.finding)
			}
		})
	}
	t.Run("refusals", func(t *testing.T) {
		good := layer(t, map[string][]string{"PreToolUse": {"/abs/bin/buddy gate"}})
		bad := filepath.Join(t.TempDir(), "bad.json")
		if err := os.WriteFile(bad, []byte("{not json"), 0600); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{
			{"--harness", "claude", "--codex-hooks", good},
			{"--codex-hooks", "hooks.json"},
			{"--codex-hooks", bad},
		} {
			_, errw, code := f.run(t, f.repo, "", append([]string{"diagnose"}, args...)...)
			if code == 0 || errw == "" {
				t.Fatalf("diagnose %v accepted", args)
			}
		}
	})
}

// A marker-only report can look healthy while no gate sees writes. Each
// missing-coverage case keeps its marker verdict and has a working absolute,
// marked, dual-route gate as the positive control. Unknown wrappers remain
// unknown; an offline layer never proves that Codex loaded or ran its gate.
func TestDiagnoseCodexHookGateCoverage(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	const absoluteGate = "BUDDY_HARNESS=codex /abs/bin/buddy gate"
	const missingGate = "no PreToolUse buddy gate"
	const badMatcher = "matcher does not satisfy the apply_patch and Bash coverage contract"
	const relativeCommand = "buddy command word is not an absolute path"
	for _, tc := range []struct {
		name     string
		event    string
		matcher  string
		command  string
		verdict  string
		findings []string
	}{
		{"positive control", "PreToolUse", "^(apply_patch|Bash)$", absoluteGate, "marked", nil},
		{"reverse order", "PreToolUse", "^(Bash|apply_patch)$", absoluteGate, "marked", nil},
		{"empty matcher covers all", "PreToolUse", "", absoluteGate, "marked", nil},
		{"dot star covers all", "PreToolUse", ".*", absoluteGate, "marked", nil},
		{"env absolute control", "PreToolUse", ".*", "env BUDDY_HARNESS=codex '/abs path/buddy' gate", "marked", nil},
		{"no hooks", "", "", "", "no_buddy_command_recognized", []string{missingGate}},
		{"no buddy command", "PreToolUse", ".*", "/usr/bin/true", "no_buddy_command_recognized", []string{missingGate}},
		{"wrong event", "PostToolUse", ".*", absoluteGate, "marked", []string{missingGate}},
		{"event case matters", "preToolUse", ".*", absoluteGate, "marked", []string{missingGate}},
		{"other buddy verb", "PreToolUse", ".*", "BUDDY_HARNESS=codex /abs/bin/buddy status", "marked", []string{missingGate}},
		{"wrapper not inspected", "PreToolUse", ".*", "sh -c 'BUDDY_HARNESS=codex /abs/bin/buddy gate'", "no_buddy_command_recognized", []string{missingGate}},
		{"patch only", "PreToolUse", "^apply_patch$", absoluteGate, "marked", []string{badMatcher}},
		{"bash only", "PreToolUse", "^Bash$", absoluteGate, "marked", []string{badMatcher}},
		{"plain alternation not admitted", "PreToolUse", "apply_patch|Bash", absoluteGate, "marked", []string{badMatcher}},
		{"star not admitted", "PreToolUse", "*", absoluteGate, "marked", []string{badMatcher}},
		{"broader regex not guessed", "PreToolUse", "^(apply_patch|Bash|Write)$", absoluteGate, "marked", []string{badMatcher}},
		{"bare buddy", "PreToolUse", ".*", "BUDDY_HARNESS=codex buddy gate", "marked", []string{relativeCommand}},
		{"relative buddy", "PreToolUse", ".*", "BUDDY_HARNESS=codex ./bin/buddy gate", "marked", []string{relativeCommand}},
		{"env bare buddy", "PreToolUse", ".*", "env BUDDY_HARNESS=codex buddy gate", "marked", []string{relativeCommand}},
		{"both command findings", "PreToolUse", "apply_patch", "BUDDY_HARNESS=codex buddy gate", "marked", []string{relativeCommand, badMatcher}},
		{"marker finding preserved", "PreToolUse", "apply_patch", "buddy gate", "unmarked", []string{"classified as a Claude hook", relativeCommand, badMatcher}},
		{"non gate relative command", "Stop", "", "BUDDY_HARNESS=codex buddy bye", "marked", []string{"refused it by design", relativeCommand, missingGate}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hooks := map[string]any{}
			if tc.event != "" {
				hooks[tc.event] = []any{map[string]any{"matcher": tc.matcher, "hooks": []any{map[string]any{"type": "command", "command": tc.command}}}}
			}
			b, err := json.Marshal(map[string]any{"hooks": hooks})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "hooks.json")
			if err := os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			d := diagnosticOK(t, f, f.repo, "--codex-hooks", path).CodexHooks
			if d == nil || d.Verdict != tc.verdict {
				t.Fatalf("marker verdict changed: %+v, want %s", d, tc.verdict)
			}
			var findings []string
			for _, c := range d.Commands {
				if c.Finding != "" {
					findings = append(findings, c.Finding)
				}
			}
			joined := strings.Join(findings, " | ")
			if len(tc.findings) == 0 && joined != "" {
				t.Fatalf("positive control has findings: %s", joined)
			}
			for _, want := range tc.findings {
				if !strings.Contains(joined, want) {
					t.Errorf("findings %q missing %q", joined, want)
				}
			}
			for _, finding := range []string{missingGate, badMatcher, relativeCommand} {
				want := false
				for _, expected := range tc.findings {
					want = want || expected == finding
				}
				if strings.Contains(joined, finding) != want {
					t.Errorf("finding %q presence differs: %s", finding, joined)
				}
			}
		})
	}
}
