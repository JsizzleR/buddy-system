package harness

// D-079: admission by committed, exact-artifact records. Each refusal here
// has a positive control on the same fixture, so "refused" cannot be
// "never armed". The first two are Codex's migration step 1 inputs.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// countingClaude writes a fake that records every execution (--version
// included) in marker, so "zero probes and zero spawns" is observed, not
// assumed from a code path.
func countingClaude(t *testing.T, b Backend, marker string) {
	t.Helper()
	fake := strings.Replace(claudeFake, "import json,sys\n", "import json,sys\nopen("+strconvQuote(marker)+",'a').write(' '.join(sys.argv[1:2])+'\\n')\n", 1)
	if err := os.WriteFile(b.(*claudeBackend).executable, []byte(fake), 0700); err != nil {
		t.Fatal(err)
	}
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

func executions(t *testing.T, marker string) int {
	t.Helper()
	data, err := os.ReadFile(marker)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

// testRecord is a record for exactly the file at executable, as a
// qualification would write it (without suites: tests bypass ParseAdmission
// and install records directly, so validate's evidence rules are tested
// separately below).
func testRecord(t *testing.T, harness, version, executable string, tiers ...string) AdmissionRecord {
	t.Helper()
	sum, size, err := hashFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	r := AdmissionRecord{Schema: AdmissionSchema, Harness: harness, Version: version, VersionOutput: versionOutput(harness, version), OS: runtime.GOOS,
		Arch: runtime.GOARCH, AdapterContract: adapterContract(harness), Tiers: tiers, Gate: gateContract(harness),
		Executable: AdmissionFile{Path: filepath.Base(executable), SHA256: sum, Size: size}}
	if harness == "claude" {
		r.Claude = &ClaudeAdmission{ModProfile: "builtin-only-" + version, Builtins: claudeKnownBuiltins}
	}
	return r
}

// Test 1 (Codex migration step 1): no record for the declared version means
// admission_missing, with nothing executed: not --version, not the worker.
func TestAdmissionMissingRecordRunsNothing(t *testing.T) {
	for _, control := range []bool{false, true} {
		b, launch, ctx := claudeFixture(t)
		launch.HarnessVersion, launch.Hooks.Mods = "2.1.291", "builtin-only-2.1.291"
		marker := filepath.Join(t.TempDir(), "runs")
		countingClaude(t, b, marker)
		if err := os.WriteFile(b.(*claudeBackend).executable, []byte(strings.ReplaceAll(mustRead(t, b.(*claudeBackend).executable), claudeContractVersion, "2.1.291")), 0700); err != nil {
			t.Fatal(err)
		}
		if control {
			withAdmissions(t, testRecord(t, "claude", "2.1.291", b.(*claudeBackend).executable, TierStart))
		} else {
			withAdmissions(t)
		}
		_, err := b.Start(ctx, launch, testAuthorization(t))
		if control {
			if err != nil || executions(t, marker) < 2 {
				t.Fatalf("positive control: err=%v runs=%d", err, executions(t, marker))
			}
			continue
		}
		if AdmissionCode(err) != "admission_missing" || executions(t, marker) != 0 {
			t.Fatalf("got %v with %d executions, want admission_missing and none", err, executions(t, marker))
		}
	}
}

func mustRead(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Test 2: a record binding sha256(A) over a file now holding B, rewritten in
// place so device, inode, size, mtime and mode are all unchanged (the case
// executable.go's identity check concedes), refuses as artifact_mismatch
// before anything runs.
func TestAdmissionArtifactMismatchBeforeExecution(t *testing.T) {
	for _, control := range []bool{false, true} {
		b, launch, ctx := claudeFixture(t)
		exe := b.(*claudeBackend).executable
		marker := filepath.Join(t.TempDir(), "runs")
		countingClaude(t, b, marker)
		withAdmissions(t, testRecord(t, "claude", claudeContractVersion, exe, TierStart))
		before, err := os.Stat(exe)
		if err != nil {
			t.Fatal(err)
		}
		if !control {
			a := mustRead(t, exe)
			// Same length: one character of a comment-free literal changes.
			bBytes := strings.Replace(a, "flush=True", "flush=1==1", 1)
			if len(bBytes) != len(a) || bBytes == a {
				t.Fatal("fixture rewrite must keep the size")
			}
			f, err := os.OpenFile(exe, os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.WriteAt([]byte(bBytes), 0); err != nil {
				t.Fatal(err)
			}
			f.Close()
			if err = os.Chtimes(exe, before.ModTime(), before.ModTime()); err != nil {
				t.Fatal(err)
			}
			after, err := os.Stat(exe)
			if err != nil || !os.SameFile(before, after) || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || after.Mode() != before.Mode() {
				t.Fatal("fixture must keep the file's identity")
			}
		}
		_, err = b.Start(ctx, launch, testAuthorization(t))
		if control {
			if err != nil {
				t.Fatalf("positive control: %v", err)
			}
			continue
		}
		if AdmissionCode(err) != "artifact_mismatch" || executions(t, marker) != 0 {
			t.Fatalf("got %v with %d executions, want artifact_mismatch and none", err, executions(t, marker))
		}
	}
}

// A start-only record must never reach resume, at the backend (before the
// file runs) and at the controller (before the ledger is refreshed).
func TestAdmissionStartOnlyRecordCannotResume(t *testing.T) {
	for _, tiers := range [][]string{{TierStart}, {TierStart, TierResume}} {
		b, launch, ctx := claudeFixture(t)
		marker := filepath.Join(t.TempDir(), "runs")
		countingClaude(t, b, marker)
		fake := strings.ReplaceAll(mustRead(t, b.(*claudeBackend).executable), "--session-id", "--resume")
		if err := os.WriteFile(b.(*claudeBackend).executable, []byte(fake), 0700); err != nil {
			t.Fatal(err)
		}
		withAdmissions(t, testRecord(t, "claude", claudeContractVersion, b.(*claudeBackend).executable, tiers...))
		if err := b.(*claudeBackend).precheck(launch, TierResume); (err == nil) != (len(tiers) == 2) {
			t.Fatalf("tiers %v: controller precheck %v", tiers, err)
		}
		_, err := b.Resume(ctx, launch, launch.NativeSessionID, testAuthorization(t))
		if len(tiers) == 2 {
			if err != nil {
				t.Fatalf("positive control: %v", err)
			}
			if got := b.Capabilities()["resume"].Support; got != "experimental" {
				t.Fatalf("resume capability %q with a resume grant", got)
			}
			continue
		}
		if AdmissionCode(err) != "tier_not_granted" || executions(t, marker) != 0 {
			t.Fatalf("start-only record: %v with %d executions", err, executions(t, marker))
		}
	}
}

// After a start-only launch the capability report withdraws resume (and,
// without the tier, compaction_observation); with every tier both stay.
func TestAdmissionCapabilitiesIntersectRecord(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(map[bool]string{false: "start-only", true: "all-tiers"}[all], func(t *testing.T) {
			tiers := []string{TierStart}
			want := "unsupported"
			if all {
				tiers, want = []string{TierStart, TierResume, TierCompaction}, "experimental"
			}
			b, launch, ctx := claudeFixture(t)
			launch.HarnessVersion, launch.Hooks.Mods = "2.1.291", "builtin-only-2.1.291"
			exe := b.(*claudeBackend).executable
			if err := os.WriteFile(exe, []byte(strings.ReplaceAll(claudeFake, claudeContractVersion, "2.1.291")), 0700); err != nil {
				t.Fatal(err)
			}
			withAdmissions(t, testRecord(t, "claude", "2.1.291", exe, tiers...))
			if _, err := b.Start(ctx, launch, testAuthorization(t)); err != nil {
				t.Fatal(err)
			}
			caps := b.Capabilities()
			if caps["resume"].Support != want || caps["compaction_observation"].Support != want {
				t.Fatalf("claude caps %+v %+v, want %s", caps["resume"], caps["compaction_observation"], want)
			}
			c, _, l := codexFixture(t)
			r := testRecord(t, "codex", "0.160.0", c.executable, tiers...)
			var err error
			if r.Package, err = buildPackageManifest(filepath.Dir(c.executable)); err != nil {
				t.Fatal(err)
			}
			withAdmissions(t, r)
			codexStart(t, c, l)
			caps = c.Capabilities()
			if caps["resume"].Support != want || caps["compaction_observation"].Support != want || caps["reconcile"].Support != "unsupported" {
				t.Fatalf("codex caps %+v %+v %+v, want %s", caps["resume"], caps["compaction_observation"], caps["reconcile"], want)
			}
		})
	}
}

// A second record of the same version cannot widen the first: a tier holds
// by version only when EVERY record of that version grants it.
func TestAdmissionVersionGrantsNeedsEveryRecord(t *testing.T) {
	a := AdmissionRecord{Harness: "claude", Version: "9.9.9", Tiers: []string{TierStart, TierResume}}
	b := AdmissionRecord{Harness: "claude", Version: "9.9.9", Tiers: []string{TierStart}}
	withAdmissions(t, a, a)
	if !admissionVersionGrants("claude", "9.9.9", TierResume) {
		t.Fatal("positive control: two resume records")
	}
	withAdmissions(t, a, b)
	if admissionVersionGrants("claude", "9.9.9", TierResume) {
		t.Fatal("a start-only record of the same version was widened by another")
	}
	if admissionVersionGrants("claude", "9.9.10", TierStart) {
		t.Fatal("a version no record names was granted")
	}
}

// A captured receipt whose version no record admits for resume is refused
// before the ledger is refreshed; with a resume grant the same receipt goes
// on to the later checks.
func TestAdmissionControllerResumeRefusesBeforeRefresh(t *testing.T) {
	for _, tiers := range [][]string{{TierStart}, {TierStart, TierResume}} {
		b, launch, _ := claudeFixture(t)
		withAdmissions(t, testRecord(t, "claude", claudeContractVersion, b.(*claudeBackend).executable, tiers...))
		receipt := filepath.Join(t.TempDir(), "receipt.json")
		id := Identity{"buddy.lifecycle.v1", "claude", "00000000-0000-4000-8000-000000000001", "captured"}
		data, _ := json.Marshal(Saved{Schema: Schema, Identity: id, Launch: launch})
		if err := os.WriteFile(receipt, data, 0600); err != nil {
			t.Fatal(err)
		}
		// An empty journal: the positive control then fails at the journal
		// identity check, after admission.
		if err := os.WriteFile(receipt+".events", nil, 0600); err != nil {
			t.Fatal(err)
		}
		ledger := &fakeLedger{}
		c, err := NewController(b, ledger, receipt)
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Resume(context.Background())
		c.Close()
		if len(tiers) == 1 {
			if AdmissionCode(err) != "tier_not_granted" || len(ledger.events) != 0 {
				t.Fatalf("start-only resume: %v, ledger events %v", err, ledger.events)
			}
			continue
		}
		if err == nil || AdmissionCode(err) != "" {
			t.Fatalf("positive control should pass admission and fail later: %v", err)
		}
	}
}

// The controller refuses an unrecorded start before the `.start` intent and
// a start-only resume before the ledger refresh. With a backend that has
// records to consult (a native backend), the precheck runs; resume's
// receipt-reading path is exercised by the controller tests with the
// fixture registry.
func TestAdmissionControllerRefusesBeforeSideEffects(t *testing.T) {
	b, launch, _ := claudeFixture(t)
	withAdmissions(t)
	receipt := filepath.Join(t.TempDir(), "receipt.json")
	ledger := &countingLedger{}
	c, err := NewController(b, ledger, receipt)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Start(context.Background(), "claude", launch, "worker"); AdmissionCode(err) != "admission_missing" {
		t.Fatalf("start: %v", err)
	}
	if _, err := os.Lstat(receipt + ".start"); !os.IsNotExist(err) || ledger.calls != 0 {
		t.Fatalf("refused start left an intent (%v) or touched the ledger (%d)", err, ledger.calls)
	}
	// The precheck runs BEFORE the intent is created: with the receipt's
	// directory unwritable, an unrecorded version still reads as
	// admission_missing rather than as an intent that could not be written.
	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0700); err != nil {
		t.Fatal(err)
	}
	lc, err := NewController(b, ledger, filepath.Join(locked, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer lc.Close()
	if err := os.Chmod(locked, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0700) })
	if _, err := lc.Start(context.Background(), "claude", launch, "worker"); AdmissionCode(err) != "admission_missing" {
		t.Fatalf("precheck must precede the intent: %v", err)
	}
	// A recorded version whose bytes differ passes the precheck and is refused
	// by the backend before anything runs; the intent it wrote is removed.
	// Positive control: a refusal after the authorization was used keeps it.
	other := testRecord(t, "claude", claudeContractVersion, b.(*claudeBackend).executable, TierStart)
	other.Executable.SHA256 = strings.Repeat("0", 64)
	withAdmissions(t, other)
	if _, err := c.Start(context.Background(), "claude", launch, "worker"); AdmissionCode(err) != "artifact_mismatch" {
		t.Fatalf("start: %v", err)
	}
	if _, err := os.Lstat(receipt + ".start"); !os.IsNotExist(err) || ledger.calls != 0 {
		t.Fatalf("artifact refusal left an intent (%v) or touched the ledger (%d)", err, ledger.calls)
	}
	used := &LaunchAuthorization{consumed: true}
	if used.untouched() || !(&LaunchAuthorization{}).untouched() {
		t.Fatal("untouched must report an unused authorization only")
	}
}

type countingLedger struct {
	fakeLedger
	calls int
}

func (l *countingLedger) Begin(ctx context.Context, h, id, label string) (Identity, error) {
	l.calls++
	return l.fakeLedger.Begin(ctx, h, id, label)
}

// A changed sidecar under an unchanged bin/codex refuses, and so does an
// undeclared addition; the unchanged package admits.
func TestAdmissionCodexWholePackage(t *testing.T) {
	for _, fault := range []string{"", "sidecar", "addition", "missing", "mode", "link"} {
		t.Run(fault, func(t *testing.T) {
			root := t.TempDir()
			for name, body := range map[string]string{"bin/codex": "#!/bin/sh\nexit 1\n", "bin/codex-code-mode-host": "host", "codex-path/rg": "rg", "codex-package.json": "{}"} {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink("bin/codex", filepath.Join(root, "codex")); err != nil {
				t.Fatal(err)
			}
			exe := filepath.Join(root, "bin", "codex")
			r := testRecord(t, "codex", "0.160.0", exe, TierStart)
			r.Executable.Path = "bin/codex"
			var err error
			if r.Package, err = buildPackageManifest(root); err != nil {
				t.Fatal(err)
			}
			withAdmissions(t, r)
			switch fault {
			case "sidecar":
				err = os.WriteFile(filepath.Join(root, "bin", "codex-code-mode-host"), []byte("HOST"), 0755)
			case "addition":
				err = os.WriteFile(filepath.Join(root, "codex-path", "extra"), []byte("x"), 0644)
			case "missing":
				err = os.Remove(filepath.Join(root, "codex-path", "rg"))
			case "mode":
				err = os.Chmod(filepath.Join(root, "codex-package.json"), 0644)
			case "link":
				if err = os.Remove(filepath.Join(root, "codex")); err == nil {
					err = os.Symlink("bin/codex-code-mode-host", filepath.Join(root, "codex"))
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = admitExecutable("codex", exe, statOf(t, exe), Launch{HarnessVersion: "0.160.0"}, TierStart)
			if fault == "" {
				if err != nil {
					t.Fatalf("positive control: %v", err)
				}
				return
			}
			if AdmissionCode(err) != "package_mismatch" {
				t.Fatalf("%s: %v", fault, err)
			}
		})
	}
}

// The Codex launch path calls admission before its version probe or start.
func TestAdmissionCodexLaunchRunsNothingWithoutRecord(t *testing.T) {
	for _, control := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-record", true: "control"}[control], func(t *testing.T) {
			c, _, l := codexFixture(t)
			probes := 0
			c.version = func(context.Context, string) (string, error) { probes++; return "codex-cli 0.160.0", nil }
			if !control {
				withAdmissions(t)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_, err := c.Start(ctx, l, testAuthorization(t))
			cancel()
			if control {
				if err != nil || probes != 1 {
					t.Fatalf("positive control: %v probes=%d", err, probes)
				}
				return
			}
			if AdmissionCode(err) != "admission_missing" || probes != 0 || c.process != nil {
				t.Fatalf("got %v, %d probes, process %v", err, probes, c.process)
			}
		})
	}
}

// A record qualified under another adapter contract, on another platform,
// for another profile, or answering another --version, refuses; the
// unchanged record admits.
func TestAdmissionRecordBindings(t *testing.T) {
	for _, fault := range []string{"", "contract", "platform", "profile", "version-text"} {
		t.Run(fault, func(t *testing.T) {
			b, launch, ctx := claudeFixture(t)
			r := testRecord(t, "claude", claudeContractVersion, b.(*claudeBackend).executable, TierStart)
			want := ""
			switch fault {
			case "contract":
				r.AdapterContract, want = "claude-stream-json.0", "contract_mismatch"
			case "platform":
				r.Arch, want = "riscv64", "platform_mismatch"
			case "profile":
				launch.Hooks.Mods, want = "builtin-only-2.1.290", "profile_mismatch"
			case "version-text":
				r.VersionOutput, want = claudeContractVersion+" (Claude Code) extra", "version_mismatch"
			}
			withAdmissions(t, r)
			_, err := b.Start(ctx, launch, testAuthorization(t))
			if AdmissionCode(err) != want || (want == "") != (err == nil) {
				t.Fatalf("got %v, want %q", err, want)
			}
		})
	}
}

// The native built-in inventory must equal the record's list: an extra
// built-in refuses unless the record lists it, and a record cannot list a
// built-in this code does not know.
func TestAdmissionBuiltinInventory(t *testing.T) {
	inventory := func(names ...string) []byte {
		var plugins []map[string]string
		for _, n := range names {
			plugins = append(plugins, map[string]string{"name": n, "path": "builtin", "source": n + "@builtin"})
		}
		b, _ := json.Marshal(map[string]any{"plugins": plugins, "error_count": 0, "mcpServers": []any{}})
		return b
	}
	both := []string{"cc-plugin-agents-md", "cc-plugin-plugin-authoring"}
	if err := claudeModCompatibility(inventory(both...), both); err != nil {
		t.Fatalf("positive control: %v", err)
	}
	if err := claudeModCompatibility(inventory(both...), both[:1]); err == nil {
		t.Fatal("a built-in the record does not list was admitted")
	}
	if err := claudeModCompatibility(inventory(both[1]), both[:1]); err == nil {
		t.Fatal("an inventory naming a built-in the record does not list was admitted")
	}
	if err := claudeModCompatibility(inventory(both[0]), both[:1]); err != nil {
		t.Fatalf("narrower record, matching inventory: %v", err)
	}
	if err := claudeModCompatibility(inventory(append(both, "cc-plugin-managed-guard")...), append(both, "cc-plugin-managed-guard")); err == nil {
		t.Fatal("an unknown built-in was admitted because a list named it")
	}
	data := embeddedRecordJSON(t, "claude")
	if _, err := ParseAdmission(data); err != nil {
		t.Fatalf("positive control: %v", err)
	}
	data = []byte(strings.Replace(string(data), `"cc-plugin-plugin-authoring"`, `"cc-plugin-plugin-authoring", "cc-plugin-managed-guard"`, 1))
	if _, err := ParseAdmission(data); AdmissionCode(err) != "record_invalid" {
		t.Fatalf("record listing an unknown built-in: %v", err)
	}
}

func embeddedRecordJSON(t *testing.T, harness string) []byte {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join("admissions", harness, "*", "admission.json"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("no committed %s record: %v", harness, err)
	}
	return []byte(mustRead(t, matches[0]))
}

// Strict parsing: each malformation has its own code, and the committed
// records are the positive control.
func TestAdmissionStrictParsing(t *testing.T) {
	for _, harness := range []string{"claude", "codex"} {
		good := string(embeddedRecordJSON(t, harness))
		if _, err := ParseAdmission([]byte(good)); err != nil {
			t.Fatalf("%s positive control: %v", harness, err)
		}
		var sha string
		{
			var r AdmissionRecord
			_ = json.Unmarshal([]byte(good), &r)
			sha = r.Executable.SHA256
		}
		for _, c := range []struct{ name, from, to, code string }{
			{"unknown field", `"schema": `, `"extra": 1, "schema": `, "record_unknown_field"},
			{"nested unknown field", `"qualified_on": `, `"signed_by": "x", "qualified_on": `, "record_unknown_field"},
			{"missing field", `"os": "darwin",`, ``, "record_missing_field"},
			{"duplicate field", `"os": "darwin",`, `"os": "darwin", "os": "linux",`, "record_duplicate_field"},
			{"malformed hash", sha, strings.ToUpper(sha), "record_malformed_hash"},
			{"short hash", sha, sha[:63], "record_malformed_hash"},
			{"reconcile", `"tiers": [`, `"tiers": [ "reconcile",`, "record_reconcile_withdrawn"},
			{"unearned tier", `"checker_passed": true`, `"checker_passed": false`, "record_tier_unearned"},
			{"no start", `"start",`, ``, "record_invalid"},
			{"version text", `"version_output": "`, `"version_output": "x`, "record_invalid"},
			{"gate", `"command": "'<adapter>'`, `"command": "'<other>'`, "record_invalid"},
		} {
			bad := strings.Replace(good, c.from, c.to, 1)
			if bad == good {
				t.Fatalf("%s/%s: fixture edit did not apply", harness, c.name)
			}
			if _, err := ParseAdmission([]byte(bad)); AdmissionCode(err) != c.code {
				t.Errorf("%s/%s: got %v, want %s", harness, c.name, err, c.code)
			}
		}
	}
	codex := string(embeddedRecordJSON(t, "codex"))
	for _, c := range []struct{ name, from, to string }{
		{"escaping link", `"target": "bin/codex"`, `"target": "../../bin/codex"`},
		{"absolute link", `"target": "bin/codex"`, `"target": "/usr/bin/true"`},
		{"dotted path", `"path": "codex-package.json"`, `"path": "./codex-package.json"`},
	} {
		bad := strings.Replace(codex, c.from, c.to, 1)
		if bad == codex {
			t.Fatalf("%s: fixture edit did not apply", c.name)
		}
		if _, err := ParseAdmission([]byte(bad)); AdmissionCode(err) != "record_invalid" {
			t.Errorf("%s: got %v", c.name, err)
		}
	}
}

// The committed records: exactly the two backfilled artifacts, every
// granted tier earned, each suite's receipt digest present in the committed
// summary it names. 2.1.289 and 2.1.290 are retired (no binary, no hash) and
// refuse.
func TestEmbeddedAdmissions(t *testing.T) {
	records, err := embeddedAdmissions()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range records {
		got[r.Harness+" "+r.Version] = r.Executable.SHA256
		for _, s := range r.Suites {
			summary := mustRead(t, filepath.Join("..", "..", filepath.FromSlash(s.Summary)))
			if !strings.Contains(summary, s.ReceiptSHA256) {
				t.Errorf("%s %s: receipt %s is not in %s", r.Harness, s.Suite, s.ReceiptSHA256, s.Summary)
			}
		}
	}
	want := map[string]string{"claude 2.1.291": "9a1d2ed6bb4421e8fc80c892c0413f293be3ee50ae3d7dda1a7622197a056690",
		"codex 0.160.0": "112fae7a5a1223e673c8a1791d32338f37df8b527ff1159bb8adac6c4dbf1b4b",
		// The first record qualify-release.py wrote (D-081, 2026-10-14).
		"codex 0.162.1": "74c6a6d263f40d65f99c27a815275f5b4ed0bcc2baecc396b4cf3255498bdfc0",
		// qualify-release.py's first Claude record (2026-10-14, at 784ec7b).
		"claude 2.1.299": "ba5ab7b8c195fa03ab8d5bf3915c76153b576a2c40e79a5c46e265e7ed9744b3",
		// The installed Codex 0.164.0, every tier (2026-10-26, at 9b6980b). Its
		// release notes were read first: nothing the contract reads changed, and
		// the gate's denials fired with no hooks feature key in the fixture.
		"codex 0.164.0": "d8990e3ee7acfbc26f91265b12a1a83f0fee40d0e0f8f1f40a37d4ec21f499cf",
		// The installed Claude 2.1.311, every tier (2026-10-27, at 10081f5), once
		// the compaction fixture's reply carried the <summary> block 2.1.311
		// requires; at 0e1a5d9 it had earned start and resume only.
		"claude 2.1.311": "85d8c08707fcfd1e78ffa0204eefbc6ec9e4fe22f375fb2767ef85d957ed3161",
		// The installed Claude 2.1.312, every tier (2026-10-29, at bd503ce). Its
		// release notes were read first: nothing the contract reads changed (the
		// hook concurrency cap is for prompt and agent hooks, the gate is a
		// command hook; the resume fix is the interactive terminal's).
		"claude 2.1.312": "6b5ec691195e551b02c41df9f7d723f4ea61eaa03c2df90acefe3736f1aeb620",
		// The installed Codex 0.165.0, every tier (2026-10-29, at f43a767), once
		// the gate suite took its own refusal of the malformed patch: 0.165.0
		// verifies a patch's grammar before PreToolUse hooks, so the gate never
		// sees that one (at b44e8d3 start was not earned).
		"codex 0.165.0": "a45099154d7fb4a5ed8a56f40966a95deb938e508bab23e6985035c709e9a85a"}
	if len(got) != len(want) {
		t.Fatalf("embedded records %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("embedded records %v", got)
		}
	}
	previous := admissionRecords
	admissionRecords = func(string, string) ([]AdmissionRecord, error) { return embeddedAdmissions() }
	defer func() { admissionRecords = previous }()
	exe := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"2.1.289", "2.1.290", "2.1.296"} {
		if _, err := admitExecutable("claude", exe, statOf(t, exe), Launch{HarnessVersion: v, Hooks: Compatibility{Mods: "builtin-only-" + v}}, TierStart); AdmissionCode(err) != "admission_missing" {
			t.Errorf("%s: %v", v, err)
		}
	}
	if _, err := admitExecutable("claude", exe, statOf(t, exe), Launch{HarnessVersion: "2.1.291", Hooks: Compatibility{Mods: "builtin-only-2.1.291"}}, TierStart); AdmissionCode(err) != "artifact_mismatch" {
		t.Errorf("an unrecorded file declared as 2.1.291: %v", err)
	}
	t.Setenv("HOME", "/Users/fixture")
	path, err := RetainedExecutable("claude", "2.1.291")
	if err != nil || path != "/Users/fixture/.buddy/harness/claude/2.1.291-9a1d2ed6/claude" {
		t.Fatalf("retained claude: %q %v", path, err)
	}
	path, err = RetainedExecutable("codex", "0.160.0")
	if err != nil || path != "/Users/fixture/.buddy/harness/codex/0.160.0-112fae7a/bin/codex" {
		t.Fatalf("retained codex: %q %v", path, err)
	}
	if _, err = RetainedExecutable("claude", "2.1.290"); AdmissionCode(err) != "admission_missing" {
		t.Fatalf("retired version has a retained path: %v", err)
	}
}

// A malformed or misplaced record makes the whole set unreadable, which
// refuses every launch: fail closed, never skip the bad file.
func TestAdmissionLoaderFailsClosed(t *testing.T) {
	good := embeddedRecordJSON(t, "claude")
	var r AdmissionRecord
	if err := json.Unmarshal(good, &r); err != nil {
		t.Fatal(err)
	}
	dir := "admissions/claude/" + r.Version + "-" + r.Executable.SHA256[:8]
	for _, c := range []struct{ name, path, body string }{
		{"", dir, string(good)},
		{"misplaced", "admissions/claude/2.1.291-00000000", string(good)},
		{"malformed", dir, string(good[:len(good)/2])},
		{"same bytes twice", "", ""},
	} {
		fsys := fstest.MapFS{c.path + "/admission.json": &fstest.MapFile{Data: []byte(c.body)}}
		if c.name == "same bytes twice" {
			// One artifact recorded under two versions: refused, never a choice.
			twin := strings.Replace(string(good), `"version": "`+r.Version+`"`, `"version": "`+r.Version+`.1"`, 1)
			twin = strings.Replace(twin, `"version_output": "`+r.Version, `"version_output": "`+r.Version+`.1`, 1)
			fsys = fstest.MapFS{dir + "/admission.json": &fstest.MapFile{Data: good},
				"admissions/claude/" + r.Version + ".1-" + r.Executable.SHA256[:8] + "/admission.json": &fstest.MapFile{Data: []byte(twin)}}
		}
		_, err := loadAdmissionFS(fsys, "admissions")
		if (c.name == "") != (err == nil) {
			t.Errorf("%q: %v", c.name, err)
		}
	}
}

// The reconcile capability stays unsupported whatever records exist.
func TestAdmissionNeverRevivesReconcile(t *testing.T) {
	c, _, l := codexFixture(t)
	codexStart(t, c, l)
	if got := c.Capabilities()["reconcile"].Support; got != "unsupported" {
		t.Fatalf("reconcile %q", got)
	}
}

// The fixture build tag must never reach an installed binary.
func TestAdmissionFixtureNotInDefaultBuild(t *testing.T) {
	src := mustRead(t, "admission_fixture.go")
	if !strings.HasPrefix(src, "//go:build buddy_admission_fixture\n") {
		t.Fatal("admission_fixture.go must be build-tagged")
	}
	for _, script := range []string{"install.sh", "setup-clone.sh"} {
		if strings.Contains(mustRead(t, filepath.Join("..", "..", "scripts", script)), "buddy_admission_fixture") {
			t.Fatalf("%s builds with the fixture tag", script)
		}
	}
}
