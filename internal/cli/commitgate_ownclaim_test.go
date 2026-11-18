package cli

// The commit gate's second section (D-228, amends D-012): staged paths nobody
// holds that no claim of the committer covers, REPORT-ONLY in every posture.
// Every silent row runs beside the same fixture's report (row 27), so
// "silent" cannot mean "the section never ran".

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JsizzleR/buddy-system/internal/store"
)

const section2 = "inside no claim of this session"

func commitGate(t *testing.T, f *fixture, session string, env map[string]string) (string, int) {
	t.Helper()
	saved := map[string]string{}
	for k, v := range env {
		saved[k] = f.env[k]
		f.env[k] = v
	}
	defer func() {
		for k, v := range saved {
			f.env[k] = v
		}
	}()
	var errw string
	var code int
	run := func() { _, errw, code = f.run(t, f.wtB, "", "commit-gate") }
	if session == "" {
		run()
	} else {
		f.asSession(session, run)
	}
	return errw, code
}

func TestCommitGateOwnClaimSection(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "router-work", "edge cap", "internal/router")
	f.stage(t, f.wtB, "docs/a.md", ".gitignore")

	// Row 27: reported, and the commit proceeds under BUDDY_COMMIT_GATE=deny
	// too (deny refuses collisions only).
	for _, env := range []map[string]string{nil, {EnvCommitPosture: "deny"}, {EnvOwnClaim: "deny"}} {
		errw, code := commitGate(t, f, "sess-b", env)
		if code != commitGateAllow || !strings.Contains(errw, section2) || !strings.Contains(errw, `"docs/a.md"`) {
			t.Fatalf("row 27 %v: section 2, allowed: code=%d err=%q", env, code, errw)
		}
	}
	// Silences beside it: an unknown committer (the operator's terminal has
	// no session to register), the arm off, the gate off or skipped.
	for name, c := range map[string]struct {
		session string
		env     map[string]string
	}{
		"unknown committer": {"", nil},
		"own-claim off":     {"sess-b", map[string]string{EnvOwnClaim: "off"}},
		"commit gate off":   {"sess-b", map[string]string{EnvCommitPosture: "off"}},
		"skip":              {"sess-b", map[string]string{EnvCommitSkip: "1"}},
	} {
		errw, code := commitGate(t, f, c.session, c.env)
		if code != commitGateAllow || strings.Contains(errw, section2) {
			t.Fatalf("%s: no section 2: code=%d err=%q", name, code, errw)
		}
	}
	// Covered by the committer: not named.
	f.claimFor(t, "sess-b", "docs", "d", "docs")
	errw, _ := commitGate(t, f, "sess-b", nil)
	if strings.Contains(errw, `"docs/a.md"`) || !strings.Contains(errw, `".gitignore"`) {
		t.Fatalf("a covered path is not named, the uncovered one still is: %q", errw)
	}
	// Row 27b/27c: the committer holding .buddy/slot/main (the landing
	// integrator replaying riders' work) is not shown the section.
	f.claimFor(t, "sess-b", "main-slot", "d", ".buddy/slot/main")
	if errw, code := commitGate(t, f, "sess-b", nil); code != commitGateAllow || strings.Contains(errw, section2) {
		t.Fatalf("row 27b: the integrator holding .buddy/slot/main sees no section 2: %q", errw)
	}
	if _, errw, code := f.run(t, f.repo, "", "release", "main-slot", "--session", "sess-b"); code != 0 {
		t.Fatal(errw)
	}
	if errw, _ := commitGate(t, f, "sess-b", nil); !strings.Contains(errw, section2) {
		t.Fatalf("row 27c: the slot released, section 2 is back: %q", errw)
	}
	// Unarmed: nobody else holds anything, so nothing is said (D2).
	if _, errw, code := f.run(t, f.repo, "", "release", "router-work", "--session", "sess-a"); code != 0 {
		t.Fatal(errw)
	}
	if errw, _ := commitGate(t, f, "sess-b", nil); strings.Contains(errw, section2) {
		t.Fatalf("unarmed: no section 2: %q", errw)
	}
}

// Beside a collision: section 2 still speaks, after the collision report,
// and names only the path no one holds (Fable cross-check: dropping the call
// on the collision path, or the !held test, survived).
func TestCommitGateOwnClaimSectionBesideACollision(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "router-work", "edge cap", "internal/router")
	f.stage(t, f.wtB, "internal/router/x.go", "docs/a.md")
	errw, code := commitGate(t, f, "sess-b", nil)
	i := strings.Index(errw, section2)
	if code != commitGateAllow || !strings.Contains(errw, "router-work") || i < 0 {
		t.Fatalf("both sections, allowed: code=%d err=%q", code, errw)
	}
	if two := errw[i:]; !strings.Contains(two, `"docs/a.md"`) || strings.Contains(two, "internal/router/x.go") {
		t.Fatalf("section 2 names the unheld path only: %q", two)
	}
	// Under deny the collision refuses; section 2 must not say the commit
	// proceeds (Fable cross-check: it said so just above "REFUSING").
	errw, code = commitGate(t, f, "sess-b", map[string]string{EnvCommitPosture: "deny"})
	if code == commitGateAllow || !strings.Contains(errw, "this section refuses nothing") || strings.Contains(errw, "the commit proceeds") {
		t.Fatalf("deny: the collision refuses, section 2 claims nothing about the commit: code=%d %q", code, errw)
	}
}

// A read only section 2 makes that fails is said, and refuses nothing (c8).
func TestCommitGateOwnClaimSectionReadErrorIsSaid(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "buddy.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	var ok bytes.Buffer
	ownClaimSection(&ok, st, "sess-b", []string{"docs/a.md"})
	if strings.Contains(ok.String(), "skipped") {
		t.Fatalf("control: an open ledger reads: %q", ok.String())
	}
	st.Close()
	var w bytes.Buffer
	ownClaimSection(&w, st, "sess-b", []string{"docs/a.md"})
	if !strings.Contains(w.String(), "own-claim report was skipped") || !strings.Contains(w.String(), "refuses nothing") {
		t.Fatalf("a failed read is said: %q", w.String())
	}
}
