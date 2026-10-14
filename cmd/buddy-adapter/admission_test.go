package main

// D-079 for the real adapter binary. The entry tests build buddy-adapter
// with -tags buddy_admission_fixture, which lets it read admission records
// from the file BUDDY_ADMISSION_FIXTURE names, through the same strict parser
// and launch checks (internal/harness/admission_fixture.go). writeCodex
// writes the fake Codex as a one-file package and the record admitting
// exactly its bytes, so every entry test runs real admission. The untagged
// build below, the one install.sh produces, ignores that file and refuses
// the same fake before running anything.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/harness"
)

const fixtureHash = "0000000000000000000000000000000000000000000000000000000000000000"

func fixtureSuites() []harness.AdmissionSuite {
	var out []harness.AdmissionSuite
	for _, s := range []struct{ tier, suite, runner, checker string }{
		{"start", "codex-shell-lifecycle-edge", "qualify-codex.py", "check-codex-gate.py"},
		{"start", "standalone-adapters", "qualify-adapters.py", "check-adapters.py"},
		{"resume", "recovery", "qualify-recovery.py", "check-recovery.py"},
		{"resume", "interrupted", "qualify-interrupted.py", "check-interrupted.py"},
		{"resume", "uncertain", "qualify-uncertain.py", "check-uncertain.py"},
		{"compaction_observation", "compaction", "qualify-compaction.py", "check-compaction.py"},
		{"compaction_observation", "interrupted-compaction", "qualify-interrupted-compaction.py", "check-interrupted-compaction.py"},
	} {
		out = append(out, harness.AdmissionSuite{Tier: s.tier, Suite: s.suite, Runner: s.runner, RunnerSHA256: fixtureHash, ReceiptSHA256: fixtureHash,
			ChecksPassed: 1, ChecksTotal: 1, Checker: s.checker, CheckerSHA256: fixtureHash, CheckerLogSHA256: fixtureHash, CheckerPassed: true,
			CheckerChecksPassed: 1, CheckerChecksTotal: 1, BuddySHA256: fixtureHash, AdapterSHA256: fixtureHash,
			SourceHead: "0000000000000000000000000000000000000000", Summary: "fixture/summary.json"})
	}
	return out
}

// writeCodex writes body as the fake package's bin/codex and the fixture
// record for exactly those bytes and that package.
func (f *entryFixture) writeCodex(t *testing.T, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.codex), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.codex, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	mode := func(p string) string {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%04o", info.Mode().Perm())
	}
	sum := sha256.Sum256([]byte(body))
	size := int64(len(body))
	record := harness.AdmissionRecord{Schema: harness.AdmissionSchema, Harness: "codex", Version: "0.160.0", VersionOutput: "codex-cli 0.160.0",
		OS: runtime.GOOS, Arch: runtime.GOARCH, AdapterContract: "codex-app-server-v2.1", Tiers: []string{"start", "resume", "compaction_observation"},
		Executable: harness.AdmissionFile{Path: "bin/codex", SHA256: hex.EncodeToString(sum[:]), Size: size},
		Package: []harness.PackageEntry{{Path: "bin", Type: "dir", Mode: mode(filepath.Dir(f.codex))},
			{Path: "bin/codex", Type: "file", Mode: mode(f.codex), SHA256: hex.EncodeToString(sum[:]), Size: &size}},
		Gate:     harness.AdmissionGate{Command: "'<adapter>' hook --receipt '<receipt>' --buddy '<buddy>'", Hash: "per launch: hooks/list currentHash equals the launch gate_hash, trusted"},
		Suites:   fixtureSuites(),
		Evidence: harness.AdmissionEvidence{QualifiedOn: "2026-10-13", Source: "hermetic fixture", Note: "admits only this test's fake bytes"}}
	data, err := json.Marshal([]harness.AdmissionRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.ParseAdmission(mustMarshal(t, record)); err != nil {
		t.Fatalf("fixture record does not parse: %v", err)
	}
	if err := os.WriteFile(f.records, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The installed (untagged) adapter refuses the fake before running it at
// all, even with a fixture record on offer; the tagged build admits it.
func TestUntaggedAdapterRefusesUnrecordedBinaryBeforeAnySpawn(t *testing.T) {
	f := newEntryFixture(t, "")
	dir := t.TempDir()
	untagged := filepath.Join(dir, "buddy-adapter")
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", untagged, "./cmd/buddy-adapter")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	for _, adapter := range []string{untagged, f.adapter} {
		// The gate command names the adapter that runs; rewrite the launch for each.
		quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
		var launch map[string]any
		if err := json.Unmarshal([]byte(mustReadFile(t, f.launch)), &launch); err != nil {
			t.Fatal(err)
		}
		launch["gate_command"] = quote(adapter) + " hook --receipt " + quote(f.receipt) + " --buddy " + quote(f.buddy)
		if err := os.WriteFile(f.launch, mustMarshal(t, launch), 0600); err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(f.marker)
		cmd := exec.Command(adapter, "--harness", "codex", "--executable", f.codex, "--buddy", f.buddy, "--launch", f.launch, "--receipt", f.receipt, "--label", "worker")
		cmd.Env = f.env
		cmd.Stdin = strings.NewReader("")
		out, err := cmd.CombinedOutput()
		runs := mustReadFile(t, f.marker)
		if adapter == f.adapter {
			// Positive control: the same fake, admitted by its fixture record, runs.
			if !strings.Contains(runs, `"kind": "version"`) {
				t.Fatalf("tagged adapter never ran the admitted fake: %v\n%s", err, out)
			}
			continue
		}
		if err == nil || !strings.Contains(string(out), "artifact_mismatch") || runs != "" {
			t.Fatalf("untagged adapter: err=%v runs=%q\n%s", err, runs, out)
		}
		// The refusal left no `.start` barrier, so the next launch can proceed.
		if _, err := os.Lstat(f.receipt + ".start"); !os.IsNotExist(err) {
			t.Fatalf("artifact refusal left a start intent: %v", err)
		}
	}
}

// --retained names ~/.buddy/harness/<harness>/<version>-<sha8>/<path> from the
// record and nothing else: with no copy there, the launch fails to resolve it
// (the adapter copies nothing), and it cannot be combined with --executable.
// Positive control: a copy placed there is launched and admitted by hash.
func TestRetainedNamesThePinnedCopy(t *testing.T) {
	f := newEntryFixture(t, "")
	home := t.TempDir()
	env := append(append([]string(nil), f.env...), "HOME="+home)
	run := func(args ...string) (string, error) {
		cmd := exec.Command(f.adapter, append([]string{"--harness", "codex", "--buddy", f.buddy, "--launch", f.launch, "--receipt", f.receipt, "--label", "worker"}, args...)...)
		cmd.Env = env
		cmd.Stdin = strings.NewReader("")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("--retained", "--executable", f.codex); err == nil || !strings.Contains(out, "do not also pass --executable") {
		t.Fatalf("--retained with --executable: %v %s", err, out)
	}
	var record []harness.AdmissionRecord
	if err := json.Unmarshal([]byte(mustReadFile(t, f.records)), &record); err != nil {
		t.Fatal(err)
	}
	pinned := filepath.Join(home, ".buddy", "harness", "codex", "0.160.0-"+record[0].Executable.SHA256[:8])
	_ = os.Remove(f.marker)
	if out, err := run("--retained"); err == nil || !strings.Contains(out, "resolve native harness executable") || !strings.Contains(out, "/.buddy") || mustReadFile(t, f.marker) != "" {
		t.Fatalf("--retained with no copy: %v %s", err, out)
	}
	// Retain the fixture package byte-exact at its pin (a move keeps every
	// mode and byte), as the operator's retention step would.
	if err := os.MkdirAll(filepath.Dir(pinned), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Dir(filepath.Dir(f.codex)), pinned); err != nil {
		t.Fatal(err)
	}
	out, err := run("--retained")
	if !strings.Contains(mustReadFile(t, f.marker), `"kind": "version"`) {
		t.Fatalf("the pinned copy was not launched: %v %s", err, out)
	}
}

func mustReadFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
