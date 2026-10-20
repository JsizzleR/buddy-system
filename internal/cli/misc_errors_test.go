package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// D-088: a claim refused by a holder exits ExitRefused, distinct from a usage
// error, from not-a-repository and from no-ledger, each of which used to be
// exit 1 too; one fleet's driver read not-a-repository as "slot held".
func TestClaimExitCodesTellHeldFromElsewhere(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "claim", "held", "--session", "sess-a", "--desc", "h", "--scope", "docs"); code != 0 {
		t.Fatal(errw)
	}
	plain := t.TempDir()
	noLedger := t.TempDir()
	if out, err := exec.Command("git", "-C", noLedger, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	for _, tc := range []struct {
		name string
		cwd  string
		args []string
		want int
	}{
		{"scope held", f.wtB, []string{"claim", "mine", "--session", "sess-b", "--desc", "m", "--scope", "docs/a.md"}, ExitRefused},
		{"slug held", f.wtB, []string{"claim", "held", "--session", "sess-b", "--desc", "m", "--scope", "pkg"}, ExitRefused},
		{"dry run forecasts a holder", f.wtB, []string{"claim", "mine", "--session", "sess-b", "--desc", "m", "--dry-run", "--scope", "docs"}, ExitRefused},
		// The positive control: the same verb, granted.
		{"granted", f.wtB, []string{"claim", "mine", "--session", "sess-b", "--desc", "m", "--scope", "pkg"}, 0},
		{"not a repository", plain, []string{"claim", "x", "--session", "sess-b", "--desc", "m", "--scope", "pkg"}, 1},
		{"no ledger", noLedger, []string{"claim", "x", "--session", "sess-b", "--desc", "m", "--scope", "pkg"}, 1},
		{"unknown flag", f.wtB, []string{"claim", "x", "--sesion", "sess-b"}, 1},
		{"comma scope", f.wtB, []string{"claim", "x", "--session", "sess-b", "--desc", "m", "--scope", "a,b"}, 1},
		{"no verb args", f.wtB, nil, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errw, code := f.run(t, tc.cwd, "", tc.args...)
			if code != tc.want {
				t.Fatalf("exit %d, want %d: %s", code, tc.want, errw)
			}
		})
	}
	// Said where a caller reads it.
	out, _, _ := f.run(t, f.repo, "", "claim", "--help")
	if !strings.Contains(out, "exit 4") {
		t.Fatalf("claim --help must name its refusal exit code: %q", out)
	}
}

// D-088: the two "nothing here" errors say where buddy looked, and the
// no-ledger one no longer sends a second clone to make a second ledger.
func TestNoRepoAndNoLedgerSayWhere(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	plain := filepath.Join(t.TempDir(), "notes")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	_, errw, code := f.run(t, plain, "", "ls")
	if code != 1 || !strings.Contains(errw, "not a git repository: "+plain) ||
		!strings.Contains(errw, "whose ledger your peers use") {
		t.Fatalf("not-a-repository must name the directory and the remedy: %d %q", code, errw)
	}
	// f.repo has no ledger yet: never inited.
	_, errw, code = f.run(t, f.repo, "", "ls")
	if code != 1 || !strings.Contains(errw, "no ledger at ") || !strings.Contains(errw, "coordinate with nobody") ||
		!strings.Contains(errw, "meant to be coordinated on its own") {
		t.Fatalf("no-ledger must say a second ledger coordinates with nobody: %d %q", code, errw)
	}
	if strings.Contains(errw, "run `buddy init` in this repo first") {
		t.Fatalf("the old advice is back: %q", errw)
	}
	// The verdicts are unchanged (invariant 3): a hook in either place is
	// still a silent no-op, never an error.
	for _, dir := range []string{plain, f.repo} {
		out, errw, code := f.run(t, dir, hookJSON("sess-z", dir, "Write", filepath.Join(dir, "x")), "gate")
		if code != 0 || out != "" || errw != "" {
			t.Fatalf("feature off must stay silent in %s: %d %q %q", dir, code, out, errw)
		}
	}
}
