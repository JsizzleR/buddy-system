package cli

// Verification regressions for V-03: a dangling symlink chain and a `..`
// inside readlink contents both reached a held path the gate did not place.
// Each syscall control proves the actual destination before checking the gate.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A single spelling creates four different candidates. Simpler links make
// lexical and physical pairs equal and cannot detect losing an individual arm.
func TestGateFourDistinctTargetSpellings(t *testing.T) {
	for _, held := range []string{"lexical-dest/file.go", "docs/entry", "physical-dest/file.go", "physical/entry"} {
		t.Run(held, func(t *testing.T) {
			f := newFixture(t)
			f.initAndHello(t)
			for _, p := range []string{"lexical-dest/file.go", "physical-dest/file.go", "physical/deep/marker"} {
				mustWrite(t, filepath.Join(f.repo, p), "x\n")
			}
			if err := os.MkdirAll(filepath.Join(f.repo, "docs"), 0755); err != nil {
				t.Fatal(err)
			}
			for link, to := range map[string]string{"docs/link": "../physical/deep", "docs/entry": "../lexical-dest/file.go", "physical/entry": "../physical-dest/file.go"} {
				if err := os.Symlink(to, filepath.Join(f.repo, link)); err != nil {
					t.Fatal(err)
				}
			}
			raw := f.repo + "/docs/link/../entry"
			targets, err := gateTargets(f.repo, raw)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("held=%s targets=%v", held, targets)
			wantGate(t, f, f.repo, hookJSON("sess-b", f.repo, "Edit", raw), false, "")
			mustClaim(t, f, f.repo, "one-spelling", "sess-a", held)
			wantGate(t, f, f.repo, hookJSON("sess-b", f.repo, "Edit", raw), true, "one-spelling")
			wantGate(t, f, f.repo, hookJSON("sess-a", f.repo, "Edit", raw), false, "")
		})
	}
}

func TestGateDanglingSymlinkChain(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	if err := os.MkdirAll(filepath.Join(f.repo, "free"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.repo, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	for link, to := range map[string]string{"free/first": "second", "free/second": "../docs/new.go"} {
		if err := os.Symlink(to, filepath.Join(f.repo, link)); err != nil {
			t.Fatal(err)
		}
	}
	raw := filepath.Join(f.repo, "free/first")
	targets, err := gateTargets(f.repo, raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("candidates=%v", targets)
	mustClaim(t, f, f.repo, "docs", "sess-a", "docs")
	// Isolated syscall control: the write actually reaches the claimed target.
	if err := os.WriteFile(raw, []byte("kernel control\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(f.repo, "docs/new.go")); err != nil || string(got) != "kernel control\n" {
		t.Fatalf("write did not land at target: %q %v", got, err)
	}
	if err := os.Remove(filepath.Join(f.repo, "docs/new.go")); err != nil {
		t.Fatal(err)
	}
	wantGate(t, f, f.repo, hookJSON("sess-b", f.repo, "Write", raw), true, "docs")
}

func TestGateDanglingSymlinkPhysicalDotDot(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	for _, p := range []string{"free/marker", "other/marker", "docs/deep/marker"} {
		mustWrite(t, filepath.Join(f.repo, p), "x\n")
	}
	for link, to := range map[string]string{"free/first": "../other/redirect/../new.go", "other/redirect": "../docs/deep"} {
		if err := os.Symlink(to, filepath.Join(f.repo, link)); err != nil {
			t.Fatal(err)
		}
	}
	raw := filepath.Join(f.repo, "free/first")
	targets, err := gateTargets(f.repo, raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("candidates=%v", targets)
	mustClaim(t, f, f.repo, "docs", "sess-a", "docs")
	if err := os.WriteFile(raw, []byte("kernel control\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(f.repo, "docs/new.go")); err != nil || string(got) != "kernel control\n" {
		t.Fatalf("write did not land at target: %q %v", got, err)
	}
	if err := os.Remove(filepath.Join(f.repo, "docs/new.go")); err != nil {
		t.Fatal(err)
	}
	wantGate(t, f, f.repo, hookJSON("sess-b", f.repo, "Write", raw), true, "docs")
}

func TestGateDanglingSymlinkResolutionBound(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	plain := t.TempDir()
	for _, dir := range []string{f.repo, plain} {
		for link, to := range map[string]string{"cycle-a": "cycle-b", "cycle-b": "cycle-a", "control": "new.go"} {
			if err := os.Symlink(to, filepath.Join(dir, link)); err != nil {
				t.Fatal(err)
			}
		}
		wantGate(t, f, dir, hookJSON("sess-b", dir, "Write", filepath.Join(dir, "control")), false, "")
	}
	// The same unresolved target is refused in a governed home or target repo,
	// and remains feature-off when neither spelling has a ledger.
	wantGate(t, f, f.repo, hookJSON("sess-b", f.repo, "Write", filepath.Join(f.repo, "cycle-a")), true, "symlink traversals")
	wantGate(t, f, plain, hookJSON("sess-b", plain, "Write", filepath.Join(f.repo, "cycle-a")), true, "symlink traversals")
	wantGate(t, f, plain, hookJSON("sess-b", plain, "Write", filepath.Join(plain, "cycle-a")), false, "")
	for n := 0; n < 42; n++ {
		target := "new.go"
		if n < 41 {
			target = fmt.Sprintf("chain-%d", n+1)
		}
		if err := os.Symlink(target, filepath.Join(f.repo, fmt.Sprintf("chain-%d", n))); err != nil {
			t.Fatal(err)
		}
	}
	wantGate(t, f, f.repo, hookJSON("sess-b", f.repo, "Write", filepath.Join(f.repo, "chain-0")), true, "symlink traversals")
}

func TestGateShellWrapperPatchAndQuotedControls(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	patch := patchText("*** Delete File: held.go\n")
	for _, command := range []string{
		"bash -lc 'apply_patch <<EOF\n" + patch + "EOF'",
		"/bin/zsh -c \"apply_patch < p.patch\"",
		"sh -c 'bash -lc \"apply_patch < p.patch\"'",
		"sudo -u x apply_patch < p.patch",
		"env -i apply_patch < p.patch",
		"env -C /tmp apply_patch < p.patch",
		"$(apply_patch < p.patch)",
		"echo \"$(apply_patch < p.patch)\"",
		"echo `apply_patch < p.patch`",
		`apply_""patch < p.patch`,
		`apply_\patch < p.patch`,
	} {
		t.Run(command, func(t *testing.T) {
			wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, command), true, "apply_patch tool")
		})
	}
	for _, command := range []string{
		`git commit -m "apply_patch fix"`,
		`make apply_patch`,
		`tee apply_patch`,
		`printf '%s\n' 'foo; apply_patch fix'`,
		`rg '(apply_patch|applypatch)' internal/cli/patch.go`,
		`echo '$(apply_patch < p.patch)'`,
		"cat <<'EOF'\napply_patch\nEOF\n",
		`bash -lc 'printf "%s\n" "apply_patch"'`,
		`command -v apply_patch`,
	} {
		t.Run(command, func(t *testing.T) { wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, command), false, "") })
	}
}

func TestGateResolutionErrorStillChecksOtherSpellings(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	mustClaim(t, f, f.repo, "docs", "sess-a", "docs")
	plain := t.TempDir()
	for _, dir := range []string{"free", "physical/deep"} {
		if err := os.MkdirAll(filepath.Join(plain, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(f.repo, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	for link, to := range map[string]string{"free/link": "../physical/deep", "free/entry": "entry", "physical/entry": filepath.Join(f.repo, "docs/new.go")} {
		if err := os.Symlink(to, filepath.Join(plain, link)); err != nil {
			t.Fatal(err)
		}
	}
	raw := plain + "/free/link/../entry"
	if err := os.WriteFile(raw, []byte("control"), 0644); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(f.repo, "docs/new.go")); err != nil || string(got) != "control" {
		t.Fatalf("kernel control %q %v", got, err)
	}
	if err := os.Remove(filepath.Join(f.repo, "docs/new.go")); err != nil {
		t.Fatal(err)
	}
	wantGate(t, f, plain, hookJSON("sess-b", plain, "Write", raw), true, "resolve the target")
}

func TestGateShellStaticQuotingAndNestedJurisdiction(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	plain := t.TempDir()
	for _, cmd := range []string{`$'apply_\x70atch' < p.patch`, `bash -lc $'apply_patch < p.patch\n'`} {
		t.Run(cmd, func(t *testing.T) {
			wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, cmd), true, "apply_patch tool")
		})
	}
	for _, cmd := range []string{`command -pv apply_patch`, `command -pV apply_patch`, `printf '%s' $'apply_\x70atch'`} {
		t.Run(cmd, func(t *testing.T) { wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, cmd), false, "") })
	}
	for _, target := range []string{f.repo, plain} {
		patch := patchText("*** Delete File: held.go\n")
		inner := "cd " + shellArg(target) + " && apply_patch <<EOF\n" + patch + "EOF\n"
		for _, cmd := range []string{"bash -lc " + shellQuote(inner), "'cd' " + shellArg(target) + " && apply_patch <<EOF\n" + patch + "EOF\n", "env -C " + shellArg(target) + " apply_patch < p.patch"} {
			t.Run(cmd, func(t *testing.T) {
				wantGate(t, f, plain, bashHookJSON("sess-b", plain, cmd), target == f.repo, "apply_patch tool")
			})
		}
	}
}

func TestGateShellBudgetRequiresJurisdiction(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	plain := t.TempDir()
	nested := "true"
	for i := 0; i < 8; i++ {
		nested = "sh -c " + shellQuote(nested)
	}
	for name, cmd := range map[string]string{
		"bytes": "printf %s " + strings.Repeat("x", 128*1024+1),
		"depth": nested,
	} {
		t.Run(name, func(t *testing.T) {
			// Exhausting a parser budget does not enable Buddy in an unmanaged
			// directory; a known home ledger still refuses the uncertain route.
			wantGate(t, f, plain, bashHookJSON("sess-b", plain, cmd), false, "")
			wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, cmd), true, "apply_patch tool")
		})
	}
	for _, target := range []string{f.repo, plain} {
		// Preserve jurisdiction found before a nested script exhausts its
		// budget. Unseen directories beyond the bound remain unobserved.
		cmd := "cd " + shellQuote(target) + " && " + nested
		wantGate(t, f, plain, bashHookJSON("sess-b", plain, cmd), target == f.repo, "apply_patch tool")
	}
}
