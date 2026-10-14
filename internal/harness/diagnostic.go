package harness

// Offline compatibility reporting deliberately never constructs a controller,
// starts a backend (even for --version), or reloads native plugins. Captures
// supplied by the operator can explain an earlier admission; they cannot prove
// today's installed version, effective hooks, trust or enforcement.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/JsizzleR/buddy-system/internal/fence"
)

const DiagnosticSchema = "buddy.diagnostic.v1"

type DiagnosticEvidence struct {
	Schema       string `json:"schema"`
	Harness      string `json:"harness"`
	CapturedAt   string `json:"captured_at"`
	Worktree     string `json:"worktree"`
	ConfigSHA256 string `json:"config_sha256"`
	Version      string `json:"version"`
	// The unwrapped hooks/list result, not a launch flag asserting trust.
	CodexHooks json.RawMessage `json:"codex_hooks,omitempty"`
}

type HookDiagnostic struct {
	Configuration          Observation `json:"configuration"`
	Trust                  Observation `json:"trust"`
	Enforcement            Observation `json:"enforcement"`
	EffectiveConfiguration Observation `json:"effective_configuration"`
	Mods                   Observation `json:"mods"`
	ConfigSHA256           string      `json:"config_sha256,omitempty"`
}

type HarnessDiagnostic struct {
	Harness                      string         `json:"harness"`
	InstalledVersion             Observation    `json:"installed_version"`
	DeclaredVersion              string         `json:"declared_version,omitempty"`
	DeclaredVersionCompatibility string         `json:"declared_version_compatibility"`
	CapturedVersion              string         `json:"captured_version,omitempty"`
	CapturedVersionCompatibility string         `json:"captured_version_compatibility,omitempty"`
	Evidence                     Observation    `json:"evidence"`
	EvidenceSHA256               string         `json:"evidence_sha256,omitempty"`
	Hooks                        HookDiagnostic `json:"hooks"`
	AdapterCapabilities          Capabilities   `json:"adapter_capabilities"`
	RuntimeCapabilities          Observation    `json:"runtime_capabilities"`
}

// ReadDiagnosticFile never follows a final symlink or blocks on a FIFO. These
// paths are operator-supplied observations, not programs to execute. Callers
// report fixed errors rather than dumping settings (which may contain secrets).
func ReadDiagnosticFile(path string) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("diagnostic input requires an absolute path")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errors.New("diagnostic input is unreadable")
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > 1<<20 {
		return nil, errors.New("diagnostic input must be a regular file of at most 1 MiB")
	}
	b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(b) > 1<<20 {
		return nil, errors.New("diagnostic input exceeds read bound")
	}
	return b, nil
}

func Diagnose(kind string, launch *Launch, evidence *DiagnosticEvidence) HarnessDiagnostic {
	unknown := Observation{State: "unknown"}
	r := HarnessDiagnostic{Harness: kind, InstalledVersion: Observation{State: "unknown", Detail: "native executable was not run"}, DeclaredVersionCompatibility: "unknown", Evidence: unknown,
		Hooks: HookDiagnostic{Configuration: unknown, Trust: unknown, Enforcement: unknown, EffectiveConfiguration: unknown, Mods: unknown}, RuntimeCapabilities: unknown}
	// Recognition is "some committed admission record names this version"
	// (D-079): a historical match, never a launch decision. The bytes are
	// not hashed here, so a match says nothing about the installed file.
	switch kind {
	case "codex":
		r.AdapterCapabilities = (&codexBackend{}).Capabilities()
	case "claude":
		r.AdapterCapabilities = (&claudeBackend{}).Capabilities()
	default:
		return r
	}
	if launch == nil {
		return r
	}
	r.DeclaredVersion = fence.Line(launch.HarnessVersion, 128)
	r.DeclaredVersionCompatibility = "unsupported"
	if admissionVersionKnown(kind, launch.HarnessVersion) {
		r.DeclaredVersionCompatibility = "matches_adapter_contract"
	}
	data, err := ReadDiagnosticFile(launch.HookConfig)
	if err != nil {
		r.Hooks.Configuration = Observation{State: "unreadable"}
		return r
	}
	hash := sha256.Sum256(data)
	r.Hooks.ConfigSHA256 = hex.EncodeToString(hash[:])
	r.Hooks.Configuration = Observation{State: "present", Detail: "file presence and hash only; effective native configuration unknown"}
	if kind == "claude" {
		if launch.GateCommand == "" || claudeSettingsCompatibility(data, launch.GateCommand) != nil {
			r.Hooks.Configuration = Observation{State: "unsupported", Detail: "settings do not match the bounded adapter gate contract"}
		} else {
			r.Hooks.Configuration = Observation{State: "configured", Detail: "supplied settings match the gate contract; effective settings and Mods were not queried"}
		}
	}
	if evidence == nil {
		return r
	}
	if evidence.Schema != "buddy.compatibility-evidence.v1" || evidence.Harness != kind || evidence.Worktree != launch.Worktree || evidence.ConfigSHA256 != r.Hooks.ConfigSHA256 {
		r.Evidence = Observation{State: "mismatch", Detail: "capture does not name this harness, worktree and configuration hash"}
		return r
	}
	if _, err := time.Parse(time.RFC3339Nano, evidence.CapturedAt); err != nil {
		r.Evidence = Observation{State: "invalid", Detail: "capture requires an RFC3339 timestamp"}
		return r
	}
	r.Evidence = Observation{State: "supplied_snapshot", Detail: evidence.CapturedAt}
	r.CapturedVersion = fence.Line(evidence.Version, 128)
	r.CapturedVersionCompatibility = "unsupported"
	if admissionOutputKnown(kind, evidence.Version) {
		r.CapturedVersionCompatibility = "matches_adapter_contract"
	}
	if r.CapturedVersionCompatibility != "matches_adapter_contract" {
		return r
	}
	if kind == "codex" && len(evidence.CodexHooks) != 0 {
		r.Hooks.EffectiveConfiguration, r.Hooks.Trust = diagnoseCodexHooks(evidence.CodexHooks, *launch)
	}
	return r
}

func diagnoseCodexHooks(raw []byte, launch Launch) (Observation, Observation) {
	unknown := Observation{State: "unknown"}
	bad := Observation{State: "unsupported_snapshot", Detail: "capture does not match the adapter's exact synchronous apply_patch and Bash gate"}
	var result struct {
		Data []struct {
			CWD      string            `json:"cwd"`
			Errors   []json.RawMessage `json:"errors"`
			Warnings []string          `json:"warnings"`
			Hooks    []struct {
				Enabled     bool   `json:"enabled"`
				EventName   string `json:"eventName"`
				HandlerType string `json:"handlerType"`
				Command     string `json:"command"`
				Matcher     string `json:"matcher"`
				TrustStatus string `json:"trustStatus"`
				CurrentHash string `json:"currentHash"`
				Async       bool   `json:"async"`
			} `json:"hooks"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &result) != nil || len(result.Data) != 1 || result.Data[0].CWD != launch.Worktree || len(result.Data[0].Errors) > 0 || len(result.Data[0].Warnings) > 0 || launch.GateCommand == "" || launch.GateHash == "" {
		return bad, unknown
	}
	count, trusted := 0, true
	for _, h := range result.Data[0].Hooks {
		if !h.Enabled {
			continue
		}
		if h.EventName != "preToolUse" || h.HandlerType != "command" || h.Async || h.Command != launch.GateCommand || h.CurrentHash != launch.GateHash {
			return bad, unknown
		}
		if !codexGateCoversWrites(h.Matcher) {
			return bad, unknown
		}
		count++
		if h.TrustStatus != "trusted" {
			trusted = false
		}
	}
	if count == 0 {
		return Observation{State: "absent_snapshot"}, unknown
	}
	configuration := Observation{State: "configured_snapshot"}
	if !trusted {
		return configuration, Observation{State: "not_trusted_snapshot"}
	}
	return configuration, Observation{State: "trusted_snapshot", Detail: "supplied native metadata only; current trust and enforcement remain unknown"}
}
