package harness

// The hermetic fixture registry. Most of this package's tests launch a fake
// native harness (a short script) to exercise the wire, recovery and
// ownership code, and no committed record admits a script's bytes. So for
// this test binary, and only here, admissionRecords synthesizes a record for
// each fixture version below over WHATEVER file is being launched: the real
// admission checks (version selection, tier, contract, platform, Codex
// package manifest, Claude profile and built-ins) all still run, but the
// bytes always match. The admission tests (admission_test.go) replace this
// with explicit records via withAdmissions, and so do not rest on it.
//
// The fixture versions are the strings the fakes print. They are NOT what
// production admits (only the embedded records): 2.1.289 stands for a
// record granting start and resume without compaction_observation, 2.1.290
// and 2.1.291 for one granting all three tiers.

import (
	"path/filepath"
	"runtime"
	"testing"
)

const claudeContractVersion = "2.1.289"

var fixtureTiers = map[string]map[string][]string{
	"claude": {
		"2.1.289": {TierStart, TierResume},
		"2.1.290": {TierStart, TierResume, TierCompaction},
		"2.1.291": {TierStart, TierResume, TierCompaction},
	},
	"codex": {"0.160.0": {TierStart, TierResume, TierCompaction}},
}

func fixtureAdmissions(harness, executable string) ([]AdmissionRecord, error) {
	var out []AdmissionRecord
	for version, tiers := range fixtureTiers[harness] {
		r := AdmissionRecord{Schema: AdmissionSchema, Harness: harness, Version: version, VersionOutput: versionOutput(harness, version),
			OS: runtime.GOOS, Arch: runtime.GOARCH, AdapterContract: adapterContract(harness), Tiers: tiers, Gate: gateContract(harness)}
		if executable != "" {
			if sum, size, err := hashFile(executable); err == nil {
				r.Executable = AdmissionFile{Path: filepath.Base(executable), SHA256: sum, Size: size}
			}
		}
		switch harness {
		case "claude":
			r.Claude = &ClaudeAdmission{ModProfile: "builtin-only-" + version, Builtins: claudeKnownBuiltins}
		case "codex":
			if executable != "" {
				r.Package, _ = buildPackageManifest(filepath.Dir(executable))
			}
		}
		out = append(out, r)
	}
	return out, nil
}

func init() { admissionRecords = fixtureAdmissions }

// withAdmissions makes records the only admission source for one test.
func withAdmissions(t *testing.T, records ...AdmissionRecord) {
	t.Helper()
	previous := admissionRecords
	admissionRecords = func(string, string) ([]AdmissionRecord, error) { return records, nil }
	t.Cleanup(func() { admissionRecords = previous })
}
