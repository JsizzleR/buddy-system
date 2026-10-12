package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func diagnosticFixture(t *testing.T) (Launch, DiagnosticEvidence) {
	t.Helper()
	dir := t.TempDir()
	data := []byte("# captured isolated configuration\n")
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	l := Launch{Worktree: dir, HookConfig: path, HarnessVersion: "0.160.0", GateCommand: "/tmp/buddy-adapter hook --receipt /tmp/receipt --buddy /tmp/buddy", GateHash: "native-gate-hash", Hooks: Compatibility{Configured: "observed", Trusted: "observed", Observed: "observed"}}
	e := DiagnosticEvidence{Schema: "buddy.compatibility-evidence.v1", Harness: "codex", CapturedAt: "2026-10-05T00:00:00Z", Worktree: dir, ConfigSHA256: hex.EncodeToString(hash[:]), Version: "codex-cli 0.160.0"}
	e.CodexHooks, _ = json.Marshal(map[string]any{"data": []any{map[string]any{"cwd": dir, "errors": []any{}, "warnings": []string{}, "hooks": []any{map[string]any{
		"enabled": true, "eventName": "preToolUse", "handlerType": "command", "command": l.GateCommand, "matcher": "^(apply_patch|Bash)$", "trustStatus": "trusted", "currentHash": l.GateHash, "async": false,
	}}}}})
	return l, e
}

func TestDiagnoseSeparatesDeclarationsSnapshotsAndCurrentEvidence(t *testing.T) {
	l, e := diagnosticFixture(t)
	r := Diagnose("codex", &l, nil)
	if r.InstalledVersion.State != "unknown" || r.Hooks.Trust.State != "unknown" || r.Hooks.Enforcement.State != "unknown" || r.Hooks.Configuration.State != "present" {
		t.Fatalf("declaration became observation: %+v", r)
	}
	r = Diagnose("codex", &l, &e)
	if r.Evidence.State != "supplied_snapshot" || r.Hooks.Trust.State != "trusted_snapshot" || r.Hooks.EffectiveConfiguration.State != "configured_snapshot" || r.CapturedVersionCompatibility != "matches_adapter_contract" {
		t.Fatalf("positive snapshot: %+v", r)
	}
	if r.InstalledVersion.State != "unknown" || r.RuntimeCapabilities.State != "unknown" || r.Hooks.Enforcement.State != "unknown" {
		t.Fatalf("snapshot became current readiness: %+v", r)
	}
	for _, tc := range []struct {
		name   string
		change func(*DiagnosticEvidence)
		state  string
	}{
		{"hash", func(e *DiagnosticEvidence) { e.ConfigSHA256 = "changed" }, "mismatch"},
		{"worktree", func(e *DiagnosticEvidence) { e.Worktree = "/another" }, "mismatch"},
		{"harness", func(e *DiagnosticEvidence) { e.Harness = "claude" }, "mismatch"},
		{"schema", func(e *DiagnosticEvidence) { e.Schema = "future" }, "mismatch"},
		{"timestamp", func(e *DiagnosticEvidence) { e.CapturedAt = "not a date" }, "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := e
			tc.change(&bad)
			got := Diagnose("codex", &l, &bad)
			if got.Evidence.State != tc.state || got.Hooks.Trust.State != "unknown" {
				t.Fatalf("mismatched capture accepted: %+v", got)
			}
		})
	}
	// Changing the file after capture is a different input even if the launch
	// still asserts all three observed compatibility booleans.
	if err := os.WriteFile(l.HookConfig, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := Diagnose("codex", &l, &e); got.Evidence.State != "mismatch" || got.Hooks.Trust.State != "unknown" {
		t.Fatalf("stale capture: %+v", got)
	}
}

func TestDiagnoseCodexHookNegativeControls(t *testing.T) {
	l, e := diagnosticFixture(t)
	for _, tc := range []struct{ name, old, new, config, trust string }{
		{"untrusted", `"trustStatus":"trusted"`, `"trustStatus":"untrusted"`, "configured_snapshot", "not_trusted_snapshot"},
		{"changed-hash", `"currentHash":"native-gate-hash"`, `"currentHash":"new"`, "unsupported_snapshot", "unknown"},
		{"async", `"async":false`, `"async":true`, "unsupported_snapshot", "unknown"},
		{"disabled", `"enabled":true`, `"enabled":false`, "absent_snapshot", "unknown"},
		{"matcher", `"matcher":"^(apply_patch|Bash)$"`, `"matcher":"Write"`, "unsupported_snapshot", "unknown"},
		{"permission-hook", `"eventName":"preToolUse"`, `"eventName":"permissionRequest"`, "unsupported_snapshot", "unknown"},
		{"warnings", `"warnings":[]`, `"warnings":["ignored"]`, "unsupported_snapshot", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := e
			raw := strings.Replace(string(e.CodexHooks), tc.old, tc.new, 1)
			if raw == string(e.CodexHooks) {
				t.Fatal("mutation missed fixture")
			}
			bad.CodexHooks = []byte(raw)
			r := Diagnose("codex", &l, &bad)
			if r.Hooks.Trust.State != tc.trust || r.Hooks.EffectiveConfiguration.State != tc.config || r.Hooks.Enforcement.State != "unknown" {
				t.Fatalf("bad hook: %+v", r)
			}
		})
	}
	e.Version = "codex-cli future\nFORGED"
	r := Diagnose("codex", &l, &e)
	if r.CapturedVersionCompatibility != "unsupported" || strings.Contains(r.CapturedVersion, "\n") || r.Hooks.Trust.State != "unknown" {
		t.Fatalf("unsupported version: %+v", r)
	}
}

func TestDiagnoseClaudeConfigurationDoesNotRunHooks(t *testing.T) {
	l, _ := diagnosticFixture(t)
	l.HarnessVersion = claudeContractVersion
	raw, _ := json.Marshal(map[string]any{"hooks": map[string]any{"PreToolUse": []any{map[string]any{"matcher": "Edit|Write|NotebookEdit", "hooks": []any{map[string]any{"type": "command", "command": l.GateCommand}}}}}})
	if err := os.WriteFile(l.HookConfig, raw, 0600); err != nil {
		t.Fatal(err)
	}
	r := Diagnose("claude", &l, nil)
	if r.Hooks.Configuration.State != "configured" || r.Hooks.Mods.State != "unknown" || r.Hooks.Enforcement.State != "unknown" || r.Hooks.Trust.State != "unknown" || r.AdapterCapabilities["resume"].Support != "experimental" || r.RuntimeCapabilities.State != "unknown" {
		t.Fatalf("Claude: %+v", r)
	}
	bad := strings.Replace(string(raw), "Edit|Write|NotebookEdit", "Edit", 1)
	if err := os.WriteFile(l.HookConfig, []byte(bad), 0600); err != nil {
		t.Fatal(err)
	}
	if r = Diagnose("claude", &l, nil); r.Hooks.Configuration.State != "unsupported" {
		t.Fatalf("incomplete coverage: %+v", r)
	}
}

func TestDiagnoseClaudeExactVersionsRemainHistorical(t *testing.T) {
	for _, version := range []string{"2.1.289", "2.1.290", "2.1.291", "2.1.292", "2.1.290-extra", "2.1.291-extra", ""} {
		t.Run(version, func(t *testing.T) {
			_, l, _ := claudeFixture(t)
			l.HarnessVersion = version
			data, err := os.ReadFile(l.HookConfig)
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(data)
			e := DiagnosticEvidence{Schema: "buddy.compatibility-evidence.v1", Harness: "claude",
				CapturedAt: "2026-10-05T00:00:00Z", Worktree: l.Worktree, ConfigSHA256: hex.EncodeToString(hash[:]), Version: version + " (Claude Code)"}
			want := "unsupported"
			if version == "2.1.289" || version == "2.1.290" || version == "2.1.291" {
				want = "matches_adapter_contract"
			}
			r := Diagnose("claude", &l, &e)
			if r.DeclaredVersionCompatibility != want || r.CapturedVersionCompatibility != want || r.Evidence.State != "supplied_snapshot" {
				t.Fatalf("exact version misclassified: %+v", r)
			}
			if r.InstalledVersion.State != "unknown" || r.Hooks.Mods.State != "unknown" || r.Hooks.Enforcement.State != "unknown" {
				t.Fatalf("historical contract match became live readiness: %+v", r)
			}
		})
	}
}

func TestDiagnosticInputRejectsSpecialAndOversizedFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDiagnosticFile(path); err == nil {
		t.Fatal("accepted FIFO")
	}
	os.Remove(path)
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDiagnosticFile(path); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDiagnosticFile(link); err == nil {
		t.Fatal("accepted symlink")
	}
	if err := os.WriteFile(path, make([]byte, (1<<20)+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDiagnosticFile(path); err == nil {
		t.Fatal("accepted oversized input")
	}
}
