package harness

// D-080: the qualification candidate route. Each refusal has a positive
// control in the same table, so "refused" and "the guard never ran" read
// differently.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withCandidate binds c for one test and undoes the binding and the record
// set it replaced.
func withCandidate(t *testing.T, c QualificationCandidate) error {
	t.Helper()
	previous := admissionRecords
	t.Cleanup(func() {
		admissionRecords = previous
		candidateState.mu.Lock()
		candidateState.active = nil
		candidateState.mu.Unlock()
	})
	_, err := EnableQualificationCandidate(c)
	return err
}

func sha(t *testing.T, name string) string {
	t.Helper()
	sum, _, err := hashFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

// A Claude candidate with no record: refused without the binding, admitted
// with it inside a loopback fixture, and refused for every way the binding or
// the fixture can be wrong, with nothing executed.
func TestCandidateClaudeBinding(t *testing.T) {
	for _, tc := range []struct {
		name, base, want string
		mutate           func(t *testing.T, exe string, c *QualificationCandidate, launch *Launch)
	}{
		{name: "control", base: "http://127.0.0.1:9/"},
		{name: "control-ipv6", base: "http://[::1]:9"},
		{name: "remote-provider", base: "https://api.anthropic.com", want: "candidate_outside_fixture"},
		{name: "no-provider", base: "", want: "candidate_outside_fixture"},
		{name: "localhost-name", base: "http://localhost:9", want: "candidate_outside_fixture"},
		{name: "https-loopback", base: "https://127.0.0.1:9", want: "candidate_outside_fixture"},
		// The candidate record set holds only the bound version, so another
		// declared version has no record at all.
		{name: "other-version", base: "http://127.0.0.1:9", want: "admission_missing",
			mutate: func(t *testing.T, exe string, c *QualificationCandidate, l *Launch) { c.Version = "2.1.290" }},
		// The file is rewritten after binding, below: the typed digest
		// still governs.
		{name: "rewritten-after-bind", base: "http://127.0.0.1:9", want: "artifact_mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, launch, ctx := claudeFixture(t)
			exe := b.(*claudeBackend).executable
			marker := filepath.Join(t.TempDir(), "runs")
			countingClaude(t, b, marker)
			withAdmissions(t)
			t.Setenv("ANTHROPIC_BASE_URL", tc.base)
			c := QualificationCandidate{Harness: "claude", Version: claudeContractVersion, Artifact: exe, SHA256: sha(t, exe)}
			if tc.mutate != nil {
				tc.mutate(t, exe, &c, &launch)
			}
			if err := withCandidate(t, c); err != nil {
				t.Fatal(err)
			}
			if tc.name == "rewritten-after-bind" {
				if err := os.WriteFile(exe, []byte(strings.Replace(mustRead(t, exe), "flush=True", "flush=1==1", 1)), 0700); err != nil {
					t.Fatal(err)
				}
			}
			_, err := b.Start(ctx, launch, testAuthorization(t))
			if tc.want == "" {
				if err != nil || executions(t, marker) < 2 {
					t.Fatalf("positive control: %v runs=%d", err, executions(t, marker))
				}
				if !admissionVersionGrants("claude", claudeContractVersion, TierResume) {
					t.Fatal("candidate record must answer the version-keyed tier checks")
				}
				return
			}
			if AdmissionCode(err) != tc.want || executions(t, marker) != 0 {
				t.Fatalf("got %v with %d runs, want %s and none", err, executions(t, marker), tc.want)
			}
		})
	}
}

// The binding is checked when it is made: a wrong digest, a link and a
// relative path refuse; a second binding in one process refuses.
func TestCandidateEnableRefusals(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "claude")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(exe, link); err != nil {
		t.Fatal(err)
	}
	good := QualificationCandidate{Harness: "claude", Version: "9.9.9", Artifact: exe, SHA256: sha(t, exe)}
	for name, c := range map[string]QualificationCandidate{
		"wrong-digest": {Harness: "claude", Version: "9.9.9", Artifact: exe, SHA256: strings.Repeat("0", 64)},
		"link":         {Harness: "claude", Version: "9.9.9", Artifact: link, SHA256: sha(t, exe)},
		// candidate.go exists relative to the test's cwd, with its own digest:
		// only the absolute-path rule refuses it.
		"relative":    {Harness: "claude", Version: "9.9.9", Artifact: "candidate.go", SHA256: sha(t, "candidate.go")},
		"unclean":     {Harness: "claude", Version: "9.9.9", Artifact: dir + "/./claude", SHA256: sha(t, exe)},
		"codex-shape": {Harness: "codex", Version: "9.9.9", Artifact: exe, SHA256: sha(t, exe)},
		"bad-harness": {Harness: "gemini", Version: "9.9.9", Artifact: exe, SHA256: sha(t, exe)},
	} {
		t.Run(name, func(t *testing.T) {
			if err := withCandidate(t, c); AdmissionCode(err) != "candidate_mismatch" || ActiveQualificationCandidate() != nil {
				t.Fatalf("got %v", err)
			}
		})
	}
	t.Run("once", func(t *testing.T) {
		if err := withCandidate(t, good); err != nil {
			t.Fatalf("positive control: %v", err)
		}
		if _, err := EnableQualificationCandidate(good); AdmissionCode(err) != "candidate_mismatch" {
			t.Fatalf("second binding: %v", err)
		}
	})
}

// A bound observer runs in place of the candidate and must be unchanged; any
// other executable refuses. The record binds the candidate's bytes.
func TestCandidateObserver(t *testing.T) {
	for _, fault := range []string{"", "observer-changed", "unbound-executable"} {
		t.Run(fault, func(t *testing.T) {
			b, launch, ctx := claudeFixture(t)
			native := b.(*claudeBackend).executable
			dir := t.TempDir()
			observer := filepath.Join(dir, "observer")
			if err := os.WriteFile(observer, []byte("#!/bin/sh\nexec "+native+" \"$@\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			withAdmissions(t)
			t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:9")
			bound := observer
			if fault == "unbound-executable" {
				bound = ""
			}
			if err := withCandidate(t, QualificationCandidate{Harness: "claude", Version: claudeContractVersion, Artifact: native,
				SHA256: sha(t, native), Executable: bound}); err != nil {
				t.Fatal(err)
			}
			if fault == "observer-changed" {
				if err := os.WriteFile(observer, []byte("#!/bin/sh\nexec "+native+" \"$@\" #\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			ob, err := NewClaude(observer)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ob.Close() })
			_, err = ob.Start(ctx, launch, testAuthorization(t))
			if fault == "" {
				if err != nil {
					t.Fatalf("positive control: %v", err)
				}
				return
			}
			if AdmissionCode(err) != "candidate_mismatch" {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func codexCandidateTree(t *testing.T) (exe, hooks string) {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{"bin/codex": "#!/bin/sh\nexit 1\n", "bin/codex-code-mode-host": "host", "codex-path/rg": "rg"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0755); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(root, "bin", "codex"), filepath.Join(t.TempDir(), "hooks.json")
}

const codexFixtureConfig = `model_provider = "buddy_fixture"
model = "gpt-6.1-sol"
[model_providers.buddy_fixture]
name = "Buddy deterministic local fixture"
base_url = "http://127.0.0.1:4242/v1"
wire_api = "responses"
[features]
apps = false
`

// Codex reads its provider from CODEX_HOME/config.toml (the hook config's
// directory); only a provider on a loopback IP is a fixture, and anything
// that could select a provider another way refuses. The package is the
// candidate's tree as bound: an addition after binding refuses.
func TestCandidateCodexFixture(t *testing.T) {
	for _, tc := range []struct{ name, config, want string }{
		{"control", codexFixtureConfig, ""},
		{"remote", strings.Replace(codexFixtureConfig, "http://127.0.0.1:4242/v1", "https://api.openai.com/v1", 1), "candidate_outside_fixture"},
		{"other-provider", strings.Replace(codexFixtureConfig, `model_provider = "buddy_fixture"`, `model_provider = "openai"`, 1), "candidate_outside_fixture"},
		{"profile", "profile = \"real\"\n" + codexFixtureConfig, "candidate_outside_fixture"},
		{"profiles-table", codexFixtureConfig + "[profiles.real]\nmodel_provider = \"openai\"\n", "candidate_outside_fixture"},
		{"dotted-top", "model_providers.buddy_fixture.base_url = \"https://x\"\n" + codexFixtureConfig, "candidate_outside_fixture"},
		{"no-config", "", "candidate_outside_fixture"},
		{"package-addition", codexFixtureConfig, "package_mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exe, hooks := codexCandidateTree(t)
			if tc.config != "" {
				if err := os.WriteFile(filepath.Join(filepath.Dir(hooks), "config.toml"), []byte(tc.config), 0600); err != nil {
					t.Fatal(err)
				}
			}
			withAdmissions(t)
			if err := withCandidate(t, QualificationCandidate{Harness: "codex", Version: "0.170.0", Artifact: exe, SHA256: sha(t, exe)}); err != nil {
				t.Fatal(err)
			}
			if tc.name == "package-addition" {
				if err := os.WriteFile(filepath.Join(filepath.Dir(exe), "extra"), []byte("x"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			_, err := admitExecutable("codex", exe, statOf(t, exe), Launch{HarnessVersion: "0.170.0", HookConfig: hooks}, TierResume)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("positive control: %v", err)
				}
				return
			}
			if AdmissionCode(err) != tc.want {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
		})
	}
}

// Every control receipt names the candidate, and a candidate's captured
// receipt resumes only under the same candidate; a production receipt never
// resumes under one. The positive controls pass this check and fail later
// (an empty journal), so the refusal is the candidate rule and nothing else.
func TestCandidateResumeAndReceipts(t *testing.T) {
	for _, tc := range []struct {
		name          string
		saved, active bool
		otherSum      bool
		want          string
	}{
		{"production-control", false, false, false, ""},
		{"candidate-control", true, true, false, ""},
		{"candidate-receipt-production-resume", true, false, false, "candidate_mismatch"},
		{"production-receipt-candidate-resume", false, true, false, "candidate_mismatch"},
		{"other-candidate", true, true, true, "candidate_mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, launch, _ := claudeFixture(t)
			exe := b.(*claudeBackend).executable
			t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:9")
			bound := QualificationCandidate{Harness: "claude", Version: claudeContractVersion, Artifact: exe, SHA256: sha(t, exe)}
			saved := Saved{Schema: Schema, Identity: Identity{"buddy.lifecycle.v1", "claude", "00000000-0000-4000-8000-000000000001", "captured"}, Launch: launch}
			if tc.saved {
				s := bound
				if tc.otherSum {
					s.SHA256 = strings.Repeat("a", 64)
				}
				saved.Candidate = &s
			}
			if tc.active {
				if err := withCandidate(t, bound); err != nil {
					t.Fatal(err)
				}
			}
			receipt := filepath.Join(t.TempDir(), "receipt.json")
			data, _ := json.Marshal(saved)
			if err := os.WriteFile(receipt, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(receipt+".events", nil, 0600); err != nil {
				t.Fatal(err)
			}
			ledger := &fakeLedger{}
			c, err := NewController(b, ledger, receipt)
			if err != nil {
				t.Fatal(err)
			}
			r, err := c.Resume(context.Background())
			c.Close()
			if got, want := r.QualificationCandidate, map[bool]string{true: bound.SHA256, false: ""}[tc.active]; got != want {
				t.Fatalf("receipt names candidate %q, want %q", got, want)
			}
			if tc.want == "" {
				if err == nil || AdmissionCode(err) != "" {
					t.Fatalf("positive control should pass the candidate rule and fail later: %v", err)
				}
				return
			}
			if AdmissionCode(err) != tc.want || len(ledger.events) != 0 {
				t.Fatalf("got %v, ledger events %v", err, ledger.events)
			}
		})
	}
}

// The binding REPLACES the record set: a build that a record admits in an
// ordinary adapter is refused by a candidate adapter, so no launch from it
// can be a production launch wearing a candidate receipt.
func TestCandidateReplacesRecords(t *testing.T) {
	recorded, _ := codexCandidateTree(t)
	cand, hooks := codexCandidateTree(t)
	if err := os.WriteFile(cand, []byte("#!/bin/sh\nexit 2\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(hooks), "config.toml"), []byte(codexFixtureConfig), 0600); err != nil {
		t.Fatal(err)
	}
	r := testRecord(t, "codex", "0.160.0", recorded, TierStart)
	r.Executable.Path = "bin/codex"
	var err error
	if r.Package, err = buildPackageManifest(filepath.Dir(filepath.Dir(recorded))); err != nil {
		t.Fatal(err)
	}
	withAdmissions(t, r)
	launch := Launch{HarnessVersion: "0.160.0", HookConfig: hooks}
	if _, err := admitExecutable("codex", recorded, statOf(t, recorded), launch, TierStart); err != nil {
		t.Fatalf("positive control: the record admits its build: %v", err)
	}
	if err := withCandidate(t, QualificationCandidate{Harness: "codex", Version: "0.160.0", Artifact: cand, SHA256: sha(t, cand)}); err != nil {
		t.Fatal(err)
	}
	if _, err := admitExecutable("codex", recorded, statOf(t, recorded), launch, TierStart); err == nil {
		t.Fatal("a candidate adapter admitted a recorded build")
	}
	if records, _ := admissionRecords("codex", ""); len(records) != 1 || records[0].Executable.SHA256 != sha(t, cand) {
		t.Fatalf("record set under a candidate: %d records", len(records))
	}
}
