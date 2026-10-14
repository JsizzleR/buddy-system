package harness

// Reproductions of the constrained Codex review of D-079 (2026-10-13), each
// watched to fail before its fix, each with a positive control.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func embeddedCodexRecord(t *testing.T) AdmissionRecord {
	t.Helper()
	var r AdmissionRecord
	if err := json.Unmarshal(embeddedRecordJSON(t, "codex"), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// P1: a link chain resolving outside the package. a/out -> b/../../external
// is lexically "external", but a/b -> ../d resolves first, so the real path
// is the package root's parent. Positive control: a chain that stays inside.
func TestReviewLinkChainCannotEscapePackage(t *testing.T) {
	for _, escape := range []bool{false, true} {
		r := embeddedCodexRecord(t)
		r.Package = append(r.Package,
			PackageEntry{Path: "a", Type: "dir", Mode: "0755"}, PackageEntry{Path: "d", Type: "dir", Mode: "0755"},
			PackageEntry{Path: "a/b", Type: "symlink", Target: "../d"})
		if escape {
			r.Package = append(r.Package, PackageEntry{Path: "a/out", Type: "symlink", Target: "b/../../external"})
		} else {
			r.Package = append(r.Package, PackageEntry{Path: "a/in", Type: "symlink", Target: "b/../codex-package.json"})
		}
		err := r.validate()
		if escape != (AdmissionCode(err) == "record_invalid") || !escape && err != nil {
			t.Fatalf("escape=%v: %v", escape, err)
		}
	}
}

// P2: a valid record followed by a stray closing delimiter is malformed JSON.
func TestReviewTrailingDelimiterRefused(t *testing.T) {
	good := embeddedRecordJSON(t, "claude")
	if _, err := ParseAdmission(good); err != nil {
		t.Fatalf("positive control: %v", err)
	}
	for _, tail := range []string{"}", "]", " }", "{}"} {
		if _, err := ParseAdmission(append(append([]byte(nil), good...), tail...)); AdmissionCode(err) != "record_invalid" {
			t.Errorf("trailing %q: %v", tail, err)
		}
	}
}

// P2: setuid, setgid and sticky bits are not representable in the manifest
// mode, so a package carrying one refuses rather than hashing as "0755".
func TestReviewSpecialModeBitsRefused(t *testing.T) {
	for _, special := range []bool{false, true} {
		root := t.TempDir()
		exe := filepath.Join(root, "bin", "codex")
		if err := os.MkdirAll(filepath.Dir(exe), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0755); err != nil {
			t.Fatal(err)
		}
		r := testRecord(t, "codex", "0.160.0", exe, TierStart)
		r.Executable.Path = "bin/codex"
		var err error
		if r.Package, err = buildPackageManifest(root); err != nil {
			t.Fatal(err)
		}
		withAdmissions(t, r)
		if special {
			if err := os.Chmod(exe, 0755|os.ModeSetuid); err != nil {
				t.Fatal(err)
			}
			if info, _ := os.Stat(exe); info.Mode()&os.ModeSetuid == 0 {
				t.Skip("this filesystem refused the setuid bit")
			}
		}
		_, err = admitExecutable("codex", exe, statOf(t, exe), Launch{HarnessVersion: "0.160.0"}, TierStart)
		if special != (AdmissionCode(err) == "package_mismatch") || !special && err != nil {
			t.Fatalf("special=%v: %v", special, err)
		}
	}
}

// P2: before any launch, nothing is admitted, so resume and
// compaction_observation must not read wider than any record grants.
func TestReviewPrelaunchCapabilitiesNotWider(t *testing.T) {
	r := embeddedCodexRecord(t)
	r.Tiers = []string{TierStart}
	withAdmissions(t, r)
	for name, caps := range map[string]Capabilities{"codex": (&codexBackend{}).Capabilities(), "claude": newClaude("").Capabilities()} {
		for _, c := range []string{"resume", "compaction_observation"} {
			if caps[c].Support == "experimental" || caps[c].Support == "supported" {
				t.Errorf("%s %s before launch: %+v", name, c, caps[c])
			}
		}
	}
}

// P1: the file replaced after it was hashed and before --version runs is
// refused before it executes. The swap is a rename (a new inode), the case
// an identity re-check can see; an in-place rewrite of the same inode in
// that window remains the race F-23 states.
func TestReviewReplacementAfterHashNeverRuns(t *testing.T) {
	for _, swap := range []bool{false, true} {
		b, launch, ctx := claudeFixture(t)
		exe := b.(*claudeBackend).executable
		marker := filepath.Join(t.TempDir(), "runs")
		countingClaude(t, b, marker)
		withAdmissions(t, testRecord(t, "claude", claudeContractVersion, exe, TierStart))
		if swap {
			other := exe + ".new"
			if err := os.WriteFile(other, []byte(strings.Replace(mustRead(t, exe), "flush=True", "flush=1==1", 1)), 0700); err != nil {
				t.Fatal(err)
			}
			admissionHashed = func() {
				if err := os.Rename(other, exe); err != nil {
					t.Error(err)
				}
			}
			t.Cleanup(func() { admissionHashed = nil })
		}
		_, err := b.Start(ctx, launch, testAuthorization(t))
		admissionHashed = nil
		if !swap {
			if err != nil {
				t.Fatalf("positive control: %v", err)
			}
			continue
		}
		if err == nil || executions(t, marker) != 0 {
			t.Fatalf("replaced after hashing: %v with %d executions", err, executions(t, marker))
		}
	}
}

func statOf(t *testing.T, name string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	return info
}
