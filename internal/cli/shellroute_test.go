package cli

// Audit A-01 (docs/CODEX-ORCHESTRATOR-AUDIT.md, 2026-10-07): the shell-patch
// recognizer passed static forms that real bash and zsh run. It enumerated
// wrappers that EXECUTE an operand and let every other program through, so
// each unlisted spelling (`nice`, `bash -c --`, `eval`, a shell fed on stdin, a
// brace expansion, a zsh precommand modifier) reached Codex's in-process
// patch engine past the gate. Codex's design review then added forms whose
// script is PRODUCED at run time (`printf ... | bash`, `bash -c "$(...)"`),
// which no enumeration of spellings closes.
//
// Every refused form below carries a KERNEL CONTROL where the box can run it:
// the same command under a real shell, with a fake apply_patch first on PATH
// that drops a marker. A form the shell does not actually execute would make
// its refusal a false denial dressed up as coverage, so the control has to
// see the marker before the gate's verdict means anything.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

// kernelRuns reports whether shell, given cmd with -c in dir, executes the
// word apply_patch: a fake apply_patch first on PATH touches $MARK.
// shellOnBox says whether a kernel control's shell exists here, and logs
// when it does not. A missing shell skips ONLY that control, never the
// gate's verdict: the gate reads text and needs no shell, so its refusal of
// a form is asserted on every platform, while "the shell really runs
// apply_patch for this form" can only be proved where the shell is. CI's
// ubuntu runner has no /bin/zsh (measured: every zsh control there failed
// with "no such file or directory" and took the gate's assertion with it).
func shellOnBox(t *testing.T, shell string) bool {
	t.Helper()
	if _, err := os.Stat(shell); err != nil {
		t.Logf("kernel control not run: %s is not on this box", shell)
		return false
	}
	return true
}

func kernelRuns(t *testing.T, shell, dir, cmd string) bool {
	t.Helper()
	bin := t.TempDir()
	mark := filepath.Join(t.TempDir(), "ran")
	// wrap stands in for any program the gate does not know that runs its
	// operands (an unknown wrapper).
	for name, body := range map[string]string{"apply_patch": ": > \"$MARK\"\n", "wrap": "exec \"$@\"\n"} {
		mustWrite(t, filepath.Join(bin, name), "#!/bin/sh\n"+body)
		if err := os.Chmod(filepath.Join(bin, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	c := exec.Command(shell, "-c", cmd)
	c.Dir = dir
	c.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "MARK=" + mark, "HOME=" + dir}
	c.Stdin = nil
	out, _ := c.CombinedOutput()
	// coproc and trap run the command asynchronously or at exit.
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(mark); err == nil {
			return true
		}
	}
	t.Logf("%s did not run apply_patch for %q: %s", shell, cmd, out)
	return false
}

func shellRouteWorkDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "p.patch"), "x\n")
	mustWrite(t, filepath.Join(dir, "list"), "p.patch\n")
	// route:/apply_patch: a real relative path that merely looks like a URL.
	mustWrite(t, filepath.Join(dir, "route:", "apply_patch"), "#!/bin/sh\n: > \"$MARK\"\n")
	if err := os.Chmod(filepath.Join(dir, "route:", "apply_patch"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestGateShellRouteFormsTheShellRuns(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	work := shellRouteWorkDir(t)
	// Homebrew's bash 5, for the forms macOS's bash 3.2 lacks; a missing
	// shell skips only that control, never the gate's verdict.
	const bash5 = "/opt/homebrew/bin/bash"
	const p = "apply_patch < p.patch"
	q := shellQuote(p)
	// Controls that hold only on macOS, each with the fact it rests on. The
	// gate refuses these forms everywhere (the assertion below still runs);
	// only the proof that the SHELL runs apply_patch is platform-bound, and
	// on CI's ubuntu runner each of these three failed the control for the
	// reason given (measured 2026-10-20), not because the gate was wrong.
	darwinOnly := map[string]string{
		"APPLY_PATCH < p.patch":                  "the default macOS volume folds case, so APPLY_PATCH resolves to apply_patch; Linux's does not",
		"caffeinate -i " + p:                     "caffeinate is a macOS binary",
		"xargs -I % -S 512 apply_patch % < list": "-S is BSD xargs; GNU xargs rejects it",
	}
	cases := []struct {
		cmd    string
		shells []string // a kernel control: each must really run apply_patch
	}{
		// The audit's static forms (A-01).
		{"bash -c -- " + q, []string{"/bin/bash"}},
		{"sh -c -- " + q, []string{"/bin/sh"}},
		{"zsh -c -- " + q, []string{"/bin/zsh"}},
		{"bash +x -c " + q, []string{"/bin/bash"}},
		{"bash +o posix -c " + q, []string{"/bin/bash"}},
		{"eval " + q, []string{"/bin/bash", "/bin/zsh"}},
		{"trap " + q + " EXIT", []string{"/bin/bash", "/bin/zsh"}},
		{"bash <<'EOF'\n" + p + "\nEOF\n", []string{"/bin/bash", "/bin/zsh"}},
		{"bash -s <<'EOF'\n" + p + "\nEOF\n", []string{"/bin/bash"}},
		{"sh -s <<'EOF'\n" + p + "\nEOF\n", []string{"/bin/bash"}},
		{"bash <<< " + q, []string{"/bin/bash", "/bin/zsh"}},
		{"bash -s x <<'EOF'\n" + p + "\nEOF\n", []string{"/bin/bash"}},
		{"bash <<EOF\n$(printf " + shellQuote(p) + ")\nEOF\n", []string{"/bin/bash"}},
		{"echo " + q + " | cat | bash", []string{"/bin/bash"}},
		{"echo " + q + " | { bash; }", []string{"/bin/bash"}},
		{"echo " + q + " | bash /dev/stdin", []string{"/bin/bash"}},
		{"bash < <(echo " + q + ")", []string{"/bin/bash"}},
		{"wrap bash <<'EOF'\n" + p + "\nEOF\n", []string{"/bin/bash"}},
		{"APPLY_PATCH < p.patch", []string{"/bin/bash"}}, // the default volume folds case
		{"fish --command " + q, nil},
		{"script -c " + q + " out.txt", nil}, // util-linux script
		{"echo " + q + " | bash", []string{"/bin/bash", "/bin/zsh"}},
		{"bash <(echo " + q + ")", []string{"/bin/bash", "/bin/zsh"}},
		{"bash -c '{apply_patch,} < p.patch'", []string{"/bin/bash"}},
		{"{apply_patch,} < p.patch", []string{"/bin/bash"}}, // zsh keeps "apply_patch," here
		{"csh -c " + q, []string{"/bin/csh"}},
		{"tcsh -c " + q, []string{"/bin/tcsh"}},
		{"csh <<'EOF'\n" + p + "\nEOF\n", []string{"/bin/csh"}}, // a stdin-fed csh: no -c text to read speculatively
		{"fish -c " + q, nil},
		{"nice " + p, []string{"/bin/bash"}},
		{"caffeinate -i " + p, []string{"/bin/bash"}},
		{"arch -arm64 " + p, nil}, // runs only where the binary has that slice
		{"stdbuf -o0 " + p, nil},
		{"timeout 60 " + p, nil},
		{"env -P /usr/bin " + p, nil},
		{"xargs -I % -S 512 apply_patch % < list", []string{"/bin/bash"}},
		{"noglob " + p, []string{"/bin/zsh"}},
		{"nocorrect " + p, []string{"/bin/zsh"}},
		{"repeat 1 " + p, []string{"/bin/zsh"}},
		{"=apply_patch < p.patch", []string{"/bin/zsh"}},
		{`() { apply_""patch < p.patch }`, []string{"/bin/zsh"}},
		{"coproc " + p, []string{"/bin/zsh", bash5}}, // macOS /bin/bash 3.2 has no coproc
		{`find . -maxdepth 0 -exec apply_patch p.patch \;`, []string{"/bin/bash"}},
		{"script -q /dev/null " + p, nil},
		// Codex's design review (pass 1): a data word among an unknown
		// program's operands is not at a command position...
		{"script -q echo " + p, nil},
		// ...execution operands of otherwise data-only programs...
		{"git bisect run " + p, nil},
		{"git submodule foreach " + q, nil},
		{"git rebase -x " + q + " HEAD~1", nil},
		{"man -P apply_patch ls", nil},
		{"sort --compress-program=apply_patch -S 1 list", nil},
		// ...each find action on its own...
		{`find . -maxdepth 0 -exec echo {} \; -exec apply_patch p.patch \;`, []string{"/bin/bash"}},
		// ...and script text produced at run time.
		{`bash -c "$(printf '%s\n' ` + q + `)"`, []string{"/bin/bash"}},
		{"cat <<< " + q + " | bash", []string{"/bin/bash"}},
		{"emit() { printf '%s\\n' " + q + "; }; emit | bash", []string{"/bin/bash"}},
		{`printf 'apply_%s < p.patch\n' patch | bash`, []string{"/bin/bash"}},
		{"bash -c \"$(cat <<'EOF'\n" + p + "\nEOF\n)\"", []string{"/bin/bash"}},
		{"source <(echo " + q + ")", []string{"/bin/zsh", bash5}}, // bash 3.2's source reads nothing from it
		{`eval "$(echo ` + q + `)"`, []string{"/bin/bash", "/bin/zsh"}},
		{`x=` + q + `; bash -c "$x"`, []string{"/bin/bash"}},
		// zsh-only syntax the bash-mode parser rejects, with spellings a
		// de-quoting fallback would miss.
		{`() { $'apply\x5fpatch' < p.patch }`, []string{"/bin/zsh"}},
		{`() { apply_{pa,xx}tch < p.patch }`, []string{"/bin/zsh"}},
	}
	for _, c := range cases {
		t.Run(c.cmd, func(t *testing.T) {
			for _, sh := range c.shells {
				if why, ok := darwinOnly[c.cmd]; ok && runtime.GOOS != "darwin" {
					t.Logf("kernel control not run on %s: %s", runtime.GOOS, why)
					break
				}
				if !shellOnBox(t, sh) {
					continue
				}
				if !kernelRuns(t, sh, work, c.cmd) {
					t.Fatalf("kernel control: %s does not run apply_patch for this form", sh)
				}
			}
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, c.cmd), true, "buddy gate")
		})
	}
}

// The other half of A-01's constraint: ordinary commands an agent runs in a
// governed repo, many of which MENTION the word, must keep passing. Each was
// allowed before this change (the audit's false-denial controls plus the
// ones this repo runs daily).
func TestGateShellRouteFalseDenialControls(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	for _, cmd := range []string{
		"cat <<'EOF'\napply_patch\nEOF\n",
		"grep apply_patch file",
		`git commit -m "apply_patch fix"`,
		"git log --grep=apply_patch",
		"echo apply_patch",
		"echo 'apply_patch < p.patch'",
		"bash -c 'echo apply_patch'",
		"env apply_patch=1 make",
		"which apply_patch",
		"type apply_patch",
		"command -v apply_patch",
		"xargs grep apply_patch",
		"nice grep apply_patch file",
		"find . -name apply_patch",
		"find . -name '*.go' -exec grep -l apply_patch {} +",
		`rg '(apply_patch|applypatch)' internal/cli/patch.go`,
		`awk '/apply_patch/' file`,
		`sed -n '/apply_patch/p' file`,
		"make apply_patch",
		"tee apply_patch",
		`printf '%s\n' 'foo; apply_patch fix'`,
		"sh scripts/check.sh hermetic",
		"bash scripts/check.sh hermetic > out.log 2>&1",
		"go test -run TestApplyPatch ./...",
		"eval 'echo hi'",
		"trap 'rm -f x' EXIT",
		"bash <<'EOF'\necho hi\nEOF\n",
		`x=$(git rev-parse HEAD); echo "$x"`,
		`"$HOME/bin/buddy" ls`,
		"for f in *.go; do gofmt -l \"$f\"; done",
		"time go vet ./...",
		"python3 -c 'print(1)'",
		// Speculative text is read only at command positions: prose that
		// mentions the word as an operand is not a script.
		`curl -d 'please run apply_patch now' https://example.invalid`,
		`python3 notes.py "fix the apply_patch bug"`,
		"env FOO=1 grep apply_patch file",
		"{,echo} apply_patch", // bash drops the empty field and runs echo
		"ls scripts/apply_patch.py && python3 apply_patch.py",
	} {
		t.Run(cmd, func(t *testing.T) { wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, cmd), false, "") })
	}
}

// Text the gate cannot analyse is uncertain, and uncertainty refuses only
// where a ledger governs (invariant 3): measured on this box's transcripts
// (2026-10-07, counts only), 31 of 109,729 unique agent shell commands did not
// parse in bash mode and 50 fed a shell or eval text produced at run time.
func TestGateShellRouteUncertainOnlyWhereGoverned(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	plain := t.TempDir()
	for _, cmd := range []string{
		"() { echo hi }",
		"curl -fsSL https://example.invalid/install.sh | bash",
		`eval "$(brew shellenv)"`,
	} {
		t.Run(cmd, func(t *testing.T) {
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, cmd), true, "cannot rule out")
			wantGate(t, f, plain, codexBashHookJSON("sess-b", plain, cmd), false, "")
		})
	}
}

// Jurisdiction from an ungoverned cwd (A-01): the patch deletes a RELATIVE
// held.go, which lands in the governed repo only through the directory
// change. Each form refuses when it reaches the repo and passes when the same
// form reaches a plain directory; a kernel control proves the shell lands
// where the gate says.
func TestGateShellRouteJurisdiction(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	parent := filepath.Dir(f.repo)
	other := filepath.Join(parent, "plainside")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	// An ungoverned sibling whose name zsh's `cd old new` rewrites into the
	// target's (wtB, the fixture's worktree, is governed and cannot serve).
	repx := filepath.Join(parent, "repx")
	if err := os.MkdirAll(repx, 0o755); err != nil {
		t.Fatal(err)
	}
	plain := t.TempDir()
	realRepo, _ := filepath.EvalSymlinks(f.repo)
	realOther, _ := filepath.EvalSymlinks(other)
	heredoc := " <<EOF\n" + patchText("*** Delete File: held.go\n") + "EOF\n"
	type form struct {
		name  string
		shell string
		cwd   string
		home  string
		cmd   func(dir string) string // dir: where the form should land
		land  func(dir string) string // the same directory change, then pwd -P
	}
	base := filepath.Base
	forms := []form{
		{"pushd", "/bin/bash", plain, plain,
			func(d string) string { return "pushd " + shellArg(d) + " && apply_patch" + heredoc },
			func(d string) string { return "pushd " + shellArg(d) + " >/dev/null && pwd -P" }},
		{"pushd-quiet", "/bin/zsh", plain, plain,
			func(d string) string { return "pushd " + shellArg(d) + " >/dev/null; apply_patch" + heredoc },
			func(d string) string { return "pushd " + shellArg(d) + " >/dev/null; pwd -P" }},
		{"tilde", "/bin/bash", plain, parent,
			func(d string) string { return "cd ~/" + base(d) + " && apply_patch" + heredoc },
			func(d string) string { return "cd ~/" + base(d) + " && pwd -P" }},
		{"tilde-zsh", "/bin/zsh", plain, parent,
			func(d string) string { return "cd ~/" + base(d) + " && apply_patch" + heredoc },
			func(d string) string { return "cd ~/" + base(d) + " && pwd -P" }},
		{"home-override", "/bin/zsh", plain, plain,
			func(d string) string {
				return "env HOME=" + shellArg(d) + " zsh -c " + shellQuote("cd ~ && apply_patch"+heredoc)
			},
			func(d string) string { return "env HOME=" + shellArg(d) + " zsh -c 'cd ~ && pwd -P'" }},
		{"chdir", "/bin/zsh", plain, plain,
			func(d string) string { return "chdir " + shellArg(d) + " && apply_patch" + heredoc },
			func(d string) string { return "chdir " + shellArg(d) + " && pwd -P" }},
		{"cd-old-new", "/bin/zsh", repx, plain,
			func(d string) string { return "cd repx " + base(d) + " && apply_patch" + heredoc },
			func(d string) string { return "cd repx " + base(d) + " && pwd -P" }},
		{"bash-c-dashdash", "/bin/bash", plain, plain,
			func(d string) string { return "bash -c -- " + shellQuote("cd "+shellArg(d)+" && apply_patch"+heredoc) },
			func(d string) string { return "bash -c -- " + shellQuote("cd "+shellArg(d)+" && pwd -P") }},
		{"bash-heredoc", "/bin/bash", plain, plain,
			func(d string) string {
				return "bash <<'OUTER'\ncd " + shellArg(d) + " && apply_patch" + heredoc + "OUTER\n"
			},
			func(d string) string { return "bash <<'OUTER'\ncd " + shellArg(d) + " && pwd -P\nOUTER\n" }},
		{"unparseable", "/bin/zsh", plain, plain,
			func(d string) string { return "cd " + shellArg(d) + "; () { apply_patch < p.patch }" },
			func(d string) string { return "cd " + shellArg(d) + "; () { pwd -P }" }},
	}
	for _, fm := range forms {
		for _, target := range []struct {
			dir, real string
			deny      bool
		}{{f.repo, realRepo, true}, {other, realOther, false}} {
			t.Run(fm.name+"/"+base(target.dir), func(t *testing.T) {
				if _, err := os.Stat(fm.shell); err != nil {
					t.Skipf("%s not on this box", fm.shell)
				}
				k := exec.Command(fm.shell, "-c", fm.land(target.dir))
				k.Dir = fm.cwd
				k.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + fm.home}
				out, err := k.CombinedOutput()
				if got := strings.TrimSpace(string(out)); err != nil || got != target.real {
					t.Fatalf("kernel control: %s lands in %q (err %v), want %q", fm.shell, got, err, target.real)
				}
				f.env["HOME"] = fm.home
				defer delete(f.env, "HOME")
				wantGate(t, f, fm.cwd, codexBashHookJSON("sess-b", fm.cwd, fm.cmd(target.dir)), target.deny, "buddy gate")
			})
		}
	}
}

// Audit A-14: guards no mutant covered. The wrapper grammar's option-value
// table lands the scan on the wrapper's command; without it an option's value
// becomes the command position, and the data program after it is no longer
// established, so these ordinary commands would be refused.
func TestGateShellRouteWrapperGrammar(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	for _, cmd := range []string{
		"xargs -I % grep apply_patch %",
		"xargs -n 1 grep apply_patch",
		"xargs -L 1 grep apply_patch",
		"nice -n 5 grep apply_patch file",
		"timeout 60 grep apply_patch file",
		"stdbuf -o L grep apply_patch file",
		"sudo -u root grep apply_patch file",
		"env -u X grep apply_patch file",
		"script -q out.txt grep apply_patch file",
		"touch x{1..500}",
		"env -S 'echo hi'",
	} {
		t.Run("allow/"+cmd, func(t *testing.T) { wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, cmd), false, "") })
	}
	for _, cmd := range []string{
		"env -S 'apply_patch < p.patch'",
		"env -S'apply_patch < p.patch'",
		"env --split-string='apply_patch < p.patch'",
		// Counted before expanding, then refused as uncertain where governed.
		"foo {1..5000}",
		// One hook's speculative parses are bounded too.
		"foo" + strings.Repeat(" 'a b'", shellMaxParses+1),
	} {
		t.Run("deny/"+cmd, func(t *testing.T) {
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, cmd), true, "buddy gate")
		})
	}
	plain := t.TempDir()
	other := t.TempDir()
	patch := " <<EOF\n" + patchText("*** Delete File: held.go\n") + "EOF\n"
	for _, target := range []struct {
		dir  string
		deny bool
	}{{f.repo, true}, {other, false}} {
		for _, cmd := range []string{
			"sudo -D " + shellArg(target.dir) + " apply_patch" + patch,
			"sudo --chdir=" + shellArg(target.dir) + " apply_patch" + patch,
			"env -C" + shellArg(target.dir) + " apply_patch" + patch,
		} {
			t.Run("place/"+cmd, func(t *testing.T) {
				wantGate(t, f, plain, codexBashHookJSON("sess-b", plain, cmd), target.deny, "buddy gate")
			})
		}
	}
	// The count is the only thing between one hook and an unbounded
	// expansion: it must answer quickly, not after allocating the fields.
	start := time.Now()
	wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, "foo {1..99999999}{1..99999999}"), true, "buddy gate")
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("an over-budget brace expansion took %v", took)
	}
}

// Codex's code review of the recognizer (2026-10-07, two passes, both
// REJECT) named these inputs. Each failed against the first implementation.
func TestGateShellRouteCodeReviewForms(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	work := shellRouteWorkDir(t)
	const p = "apply_patch < p.patch"
	q := shellQuote(p)
	for _, c := range []struct {
		cmd    string
		shells []string
	}{
		// A compound statement's stdin reaches the shell inside it.
		{"{ bash; } <<'SH'\n" + p + "\nSH\n", []string{"/bin/bash"}},
		// A pipe reaches a shell behind && inside the block it feeds.
		{`printf 'apply_%s < p.patch\n' patch | { true && bash; }`, []string{"/bin/bash"}},
		// A literal here-document duplicated onto stdin.
		{"bash 3<<'SH' 0<&3\n" + p + "\nSH\n", []string{"/bin/bash"}},
		{"git rebase -x" + q + " HEAD~1", nil},
		// env -S's split words are followed by the rest of argv.
		{"env -S env " + p, []string{"/bin/bash"}},
		// sh here is flock's lock file; apply_patch is its command.
		{"flock sh " + p, nil},
	} {
		t.Run(c.cmd, func(t *testing.T) {
			for _, sh := range c.shells {
				if !kernelRuns(t, sh, work, c.cmd) {
					t.Fatalf("kernel control: %s does not run apply_patch for this form", sh)
				}
			}
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, c.cmd), true, "buddy gate")
		})
	}
	for _, cmd := range []string{
		"bash -n -c " + q, // syntax check only: nothing runs
		"env touch x{1..500}",
		"curl -I https://example.com/apply_patch",
	} {
		t.Run("allow/"+cmd, func(t *testing.T) { wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, cmd), false, "") })
	}
}

func TestGateShellRouteCodeReviewJurisdiction(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	spaced := newFixtureNamed(t, "gov repo")
	spaced.initAndHello(t)
	plain := t.TempDir()
	// A directory literally named ~ that leads to the repo: a QUOTED or
	// mid-word ~ is not expanded, so this is where `-C'~/repo'` lands.
	if err := os.Symlink(filepath.Dir(f.repo), filepath.Join(plain, "~")); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(plain, f.repo)
	if err != nil {
		t.Fatal(err)
	}
	f.env["HOME"] = t.TempDir() // the hook's HOME leads nowhere governed
	type form struct{ name, shell, land, cmd string }
	for _, fm := range []form{
		{"relative-home", "/bin/bash", "HOME=" + rel + "; cd; pwd -P", "HOME=" + rel + "; cd; " + "apply_patch < p.patch"},
		{"quoted-inline-tilde", "/bin/bash", "cd '~/repo' && pwd -P", "env -C'~/repo' apply_patch < p.patch"},
		{"spaced-unparseable", "/bin/zsh", "cd " + shellArg(spaced.repo) + "; () { pwd -P }", "cd " + shellArg(spaced.repo) + "; () { apply_patch < p.patch }"},
	} {
		t.Run(fm.name, func(t *testing.T) {
			if shellOnBox(t, fm.shell) {
				k := exec.Command(fm.shell, "-c", fm.land)
				k.Dir = plain
				k.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + plain}
				out, err := k.CombinedOutput()
				if err != nil || !strings.HasSuffix(strings.TrimSpace(string(out)), "repo") {
					t.Fatalf("kernel control: %s lands in %q (err %v)", fm.shell, out, err)
				}
			}
			wantGate(t, f, plain, codexBashHookJSON("sess-b", plain, fm.cmd), true, "buddy gate")
		})
	}
	t.Run("oversize-script", func(t *testing.T) {
		cmd := "cd " + shellArg(f.repo) + "; apply_patch < p.patch\n#" + strings.Repeat("x", 128<<10)
		wantGate(t, f, plain, codexBashHookJSON("sess-b", plain, cmd), true, "buddy gate")
	})
	t.Run("many-directories", func(t *testing.T) {
		// One cd per distinct directory used to re-resolve every directory
		// against every retained base: quadratic before the cap, and still
		// M^2 per directory after it (9.3 s for this command). The witness is
		// the resolution count, not wall time: the gate's ledger discovery
		// over the retained candidates dominates time, and load varies it.
		var b strings.Builder
		for i := 0; i < 1000; i++ {
			dir := filepath.Join(plain, "u", strconv.Itoa(i))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			b.WriteString("cd " + shellArg(dir) + "; ")
		}
		b.WriteString("apply_patch < p.patch")
		wantGate(t, f, plain, codexBashHookJSON("sess-b", plain, b.String()), false, "")
		ctx := &shellPatchContext{bases: []string{plain}}
		(&shellWalker{ctx: ctx}).analyze(b.String(), 0, true, nil)
		if ctx.resolutions > maxPatchPaths+1 {
			t.Fatalf("1000 absolute directory changes cost %d resolutions, want at most %d", ctx.resolutions, maxPatchPaths+1)
		}
		if ctx.err == nil {
			t.Fatal("saturating the candidate directories must be reported")
		}
	})
}

// braceFields must saturate, never wrap: x{0..9223372036854775807} counted
// as one field would hand a 2^63-field expansion to expand.Braces.
func TestShellBraceFieldsSaturate(t *testing.T) {
	for _, src := range []string{"x{0..9223372036854775807}", "x{-9223372036854775808..9223372036854775807}", "x{1..300}"} {
		tree, err := syntax.NewParser().Parse(strings.NewReader(src), "")
		if err != nil {
			t.Fatal(err)
		}
		word := tree.Stmts[0].Cmd.(*syntax.CallExpr).Args[0]
		split := &syntax.Word{Parts: append([]syntax.WordPart(nil), word.Parts...)}
		if !syntax.SplitBraces(split) {
			t.Fatalf("%s: no brace expansion parsed", src)
		}
		if n := braceFields(split.Parts); n <= shellMaxWordFields {
			t.Fatalf("%s counted %d fields, want more than %d", src, n, shellMaxWordFields)
		}
	}
}

// Codex's follow-up code review (2026-10-08, two passes, both REJECT) named
// these against the first round of fixes. Each failed before its repair.
func TestGateShellRouteFollowUpReviewForms(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	work := shellRouteWorkDir(t)
	const p = "apply_patch < p.patch"
	q := shellQuote(p)
	for _, c := range []struct {
		cmd    string
		shells []string
	}{
		// An over-budget brace word among a shell's operands is uncertain.
		{"bash {-c,apply_patch,x{1..500}} < p.patch", []string{"/bin/bash"}},
		// +n turns execution back on.
		{"bash -n +n -c " + q, []string{"/bin/bash"}},
		// A block's fd 3 reaches the statement inside it.
		{"{ bash 0<&3; } 3<<'SH'\n" + p + "\nSH\n", []string{"/bin/bash"}},
		// At a command position a "URL" is a path like any other.
		{"route://apply_patch < p.patch", []string{"/bin/bash"}},
	} {
		t.Run(c.cmd, func(t *testing.T) {
			for _, sh := range c.shells {
				if !kernelRuns(t, sh, work, c.cmd) {
					t.Fatalf("kernel control: %s does not run apply_patch for this form", sh)
				}
			}
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, c.cmd), true, "buddy gate")
		})
	}
	t.Run("env-S-growth", func(t *testing.T) {
		// env -S used to rebuild its script by repeated string concatenation:
		// about 4 GB copied for this 120 KB command (0.29 s measured). Bytes
		// allocated is the deterministic witness; wall time is not, under -race.
		cmd := "env -S true" + strings.Repeat(" $_", 40000) // 120,011 bytes: inside the script budget
		wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, cmd), true, "buddy gate")
		h := hookInput{ToolName: "Bash"}
		h.ToolInput.Command = cmd
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		shellPatchRoute(h)
		runtime.ReadMemStats(&after)
		if n := after.TotalAlloc - before.TotalAlloc; n > 64<<20 {
			t.Fatalf("recognizing one 120 KB env -S command allocated %d bytes", n)
		}
	})
}

func TestGateShellRouteFollowUpReviewJurisdiction(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	dollar := newFixtureNamed(t, "gov$repo")
	dollar.initAndHello(t)
	plain := t.TempDir()
	t.Run("oversize-attached-C", func(t *testing.T) {
		cmd := "env -C" + f.repo + " apply_patch < p.patch\n#" + strings.Repeat("x", 128<<10)
		wantGate(t, f, plain, codexBashHookJSON("sess-b", plain, cmd), true, "buddy gate")
	})
	t.Run("double-quoted-escape", func(t *testing.T) {
		quoted := `"` + strings.ReplaceAll(dollar.repo, "$", `\$`) + `"`
		if shellOnBox(t, "/bin/zsh") {
			k := exec.Command("/bin/zsh", "-c", "cd "+quoted+"; () { pwd -P }")
			k.Dir = plain
			k.Env = []string{"PATH=/usr/bin:/bin"}
			if out, err := k.CombinedOutput(); err != nil || !strings.HasSuffix(strings.TrimSpace(string(out)), "gov$repo") {
				t.Fatalf("kernel control: zsh lands in %q (err %v)", out, err)
			}
		}
		cmd := "cd " + quoted + "; () { apply_patch < p.patch }"
		wantGate(t, f, plain, codexBashHookJSON("sess-b", plain, cmd), true, "buddy gate")
	})
}

// Codex's third code pass (2026-10-08, REJECT) named these against the
// second round of fixes. Each failed before its repair.
func TestGateShellRouteThirdReviewForms(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	work := shellRouteWorkDir(t)
	const p = "apply_patch < p.patch"
	for _, c := range []struct {
		cmd    string
		shells []string
	}{
		// +o noexec turns execution back on, like +n.
		{"bash -n +o noexec -c " + shellQuote(p), []string{"/bin/bash"}},
		// 0<&3- MOVES fd 3 onto stdin.
		{"bash 3<<'SH' 0<&3-\n" + p + "\nSH\n", []string{"/bin/bash"}},
	} {
		t.Run(c.cmd, func(t *testing.T) {
			for _, sh := range c.shells {
				if !kernelRuns(t, sh, work, c.cmd) {
					t.Fatalf("kernel control: %s does not run apply_patch for this form", sh)
				}
			}
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, c.cmd), true, "buddy gate")
		})
	}
	t.Run("continued-cd-unparseable", func(t *testing.T) {
		plain := t.TempDir()
		inner := "cd \\\n" + f.repo + "; () { pwd -P }"
		if shellOnBox(t, "/bin/zsh") {
			k := exec.Command("/bin/zsh", "-c", inner)
			k.Dir = plain
			k.Env = []string{"PATH=/usr/bin:/bin"}
			if out, err := k.CombinedOutput(); err != nil || !strings.HasSuffix(strings.TrimSpace(string(out)), "repo") {
				t.Fatalf("kernel control: zsh lands in %q (err %v)", out, err)
			}
		}
		cmd := "zsh -c " + shellQuote("cd \\\n"+f.repo+"; () { apply_patch < p.patch }")
		wantGate(t, f, plain, codexBashHookJSON("sess-b", plain, cmd), true, "buddy gate")
	})
	t.Run("many-descriptors", func(t *testing.T) {
		// Each redirection used to copy the statement's whole descriptor
		// table: 8,000 of them cost about 32 million entry copies.
		var b strings.Builder
		b.WriteString(":")
		for n := 3; n < 8003; n++ {
			b.WriteString(" " + strconv.Itoa(n) + "</dev/null")
		}
		h := hookInput{ToolName: "Bash"}
		h.ToolInput.Command = b.String()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		shellPatchRoute(h)
		runtime.ReadMemStats(&after)
		if n := after.TotalAlloc - before.TotalAlloc; n > 64<<20 {
			t.Fatalf("recognizing 8,000 redirections allocated %d bytes", n)
		}
	})
	t.Run("descriptor-budget", func(t *testing.T) {
		// Past 64 tracked descriptors the route is uncertain: dropping one
		// could hide `3<<'SH' ... 0<&3`. Uncertain refuses only where governed.
		var b strings.Builder
		b.WriteString(":")
		for n := 3; n < 3+shellMaxFDs+1; n++ {
			b.WriteString(" " + strconv.Itoa(n) + "</dev/null")
		}
		wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, b.String()), true, "descriptor budget")
		plain := t.TempDir()
		wantGate(t, f, plain, codexBashHookJSON("sess-b", plain, b.String()), false, "")
	})
}

// Codex's fourth code pass (2026-10-08, REJECT), ranked by realism. Each
// failed before its repair.
func TestGateShellRouteFourthReviewForms(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	work := shellRouteWorkDir(t)
	const p = "apply_patch < p.patch"
	for _, c := range []struct {
		cmd    string
		shells []string
	}{
		// A function defined in the same command that wraps a stdin shell.
		{"run_script() { bash -s; }\nrun_script <<'SH'\n" + p + "\nSH\n", []string{"/bin/bash"}},
		// GNU env -S processes escapes; \c ends the string.
		{`env -S 'apply_patch\c' < p.patch`, nil},
	} {
		t.Run(c.cmd, func(t *testing.T) {
			for _, sh := range c.shells {
				if !kernelRuns(t, sh, work, c.cmd) {
					t.Fatalf("kernel control: %s does not run apply_patch for this form", sh)
				}
			}
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, c.cmd), true, "buddy gate")
		})
	}
	plain := t.TempDir()
	other := t.TempDir()
	for _, target := range []struct {
		dir  string
		deny bool
	}{{f.repo, true}, {other, false}} {
		// Grouped short options: -vC DIR is -v -C DIR.
		cmd := "env -vC " + shellArg(target.dir) + " apply_patch < p.patch"
		t.Run("grouped/"+filepath.Base(target.dir), func(t *testing.T) {
			wantGate(t, f, plain, codexBashHookJSON("sess-b", plain, cmd), target.deny, "buddy gate")
		})
	}
	t.Run("nested-descriptor-tables", func(t *testing.T) {
		var b strings.Builder
		b.WriteString(strings.Repeat("{ ", 6000) + "true; ")
		for fd := 3; fd < 6003; fd++ {
			b.WriteString("} " + strconv.Itoa(fd) + "</dev/null; ")
		}
		h := hookInput{ToolName: "Bash", Cwd: plain}
		h.ToolInput.Command = b.String()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		shellPatchDirs(h, "")
		runtime.ReadMemStats(&after)
		if n := after.TotalAlloc - before.TotalAlloc; n > 64<<20 {
			t.Fatalf("discovery over 6,000 nested redirected blocks allocated %d bytes", n)
		}
	})
}

// Codex's fifth code pass (2026-10-08, REJECT on four P2s; the first
// plausible, three crafted). Each failed before its repair.
func TestGateShellRouteFifthReviewForms(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	work := shellRouteWorkDir(t)
	const p = "apply_patch < p.patch"
	for _, c := range []struct {
		cmd    string
		shells []string
	}{
		// rg's preprocessor is a program it runs.
		{"rg --pre=apply_patch needle p.patch", nil},
		{"rg --pre apply_patch needle p.patch", nil},
		// A local function shadows a data command.
		{"cat() { bash -s; }\ncat <<'SH'\n" + p + "\nSH\n", []string{"/bin/bash"}},
	} {
		t.Run(c.cmd, func(t *testing.T) {
			for _, sh := range c.shells {
				if !kernelRuns(t, sh, work, c.cmd) {
					t.Fatalf("kernel control: %s does not run apply_patch for this form", sh)
				}
			}
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, c.cmd), true, "buddy gate")
		})
	}
	t.Run("allow/rg --pre=cat", func(t *testing.T) {
		wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, "rg --pre=cat apply_patch docs"), false, "")
	})
	plain := t.TempDir()
	other := t.TempDir()
	for _, target := range []struct {
		dir  string
		deny bool
	}{{f.repo, true}, {other, false}} {
		for name, cmd := range map[string]string{
			"git-C":      "git -C " + shellArg(target.dir) + " rebase --exec " + shellQuote(p) + " HEAD~1",
			"lexical-vC": "zsh -c " + shellQuote("env -vC "+shellArg(target.dir)+" "+p+"; () { :; }"),
		} {
			t.Run(name+"/"+filepath.Base(target.dir), func(t *testing.T) {
				wantGate(t, f, plain, codexBashHookJSON("sess-b", plain, cmd), target.deny, "buddy gate")
			})
		}
	}
}

// Codex's sixth code pass (2026-10-08, REJECT): two plausible P1s and a
// crafted P2. Each failed before its repair.
func TestGateShellRouteSixthReviewForms(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	work := shellRouteWorkDir(t)
	const p = "apply_patch < p.patch"
	for _, c := range []struct {
		cmd    string
		shells []string
	}{
		// An attached env -S value holding '='.
		{"env -S'X=1 apply_patch' < p.patch", nil},
		// A nested script's stdin is the outer command's.
		{"bash -c 'bash -s' <<'SH'\n" + p + "\nSH\n", []string{"/bin/bash"}},
		{"eval 'bash -s' <<'SH'\n" + p + "\nSH\n", []string{"/bin/bash"}},
		// A program-valued option hidden in an over-budget brace word.
		{"rg {--pre=apply_patch,-e{1..500}} p.patch", nil},
	} {
		t.Run(c.cmd, func(t *testing.T) {
			for _, sh := range c.shells {
				if !kernelRuns(t, sh, work, c.cmd) {
					t.Fatalf("kernel control: %s does not run apply_patch for this form", sh)
				}
			}
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, c.cmd), true, "buddy gate")
		})
	}
}

// Codex's seventh code pass (2026-10-08, REJECT): two plausible P1s and two
// crafted P2s. Each failed before its repair.
func TestGateShellRouteSeventhReviewForms(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	work := shellRouteWorkDir(t)
	const p = "apply_patch < p.patch"
	for _, c := range []struct {
		cmd    string
		shells []string
	}{
		// fish runs its init-command text.
		{"fish -C " + shellQuote(p) + " -c true", nil},
		{"fish --init-command=" + shellQuote(p) + " -c true", nil},
		// A nested script inherits every descriptor, not just stdin.
		{"bash -c 'bash <&3' 3<<'SH'\n" + p + "\nSH\n", []string{"/bin/bash"}},
	} {
		t.Run(c.cmd, func(t *testing.T) {
			for _, sh := range c.shells {
				if !kernelRuns(t, sh, work, c.cmd) {
					t.Fatalf("kernel control: %s does not run apply_patch for this form", sh)
				}
			}
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, c.cmd), true, "buddy gate")
		})
	}
	plain := t.TempDir()
	other := t.TempDir()
	for _, target := range []struct {
		dir  string
		deny bool
	}{{f.repo, true}, {other, false}} {
		// env processes the options its -S text splits into.
		cmd := "env -S " + shellQuote("-C "+target.dir+" apply_patch") + " < p.patch"
		t.Run("env-S-options/"+filepath.Base(target.dir), func(t *testing.T) {
			wantGate(t, f, plain, codexBashHookJSON("sess-b", plain, cmd), target.deny, "buddy gate")
		})
	}
	t.Run("rebuilt-heredoc", func(t *testing.T) {
		// One inherited here-document read by 8,000 shells used to be rebuilt
		// and retained once per shell: about 640 MB by Codex's count.
		cmd := "{ " + strings.Repeat("bash; ", 8000) + "} <<'SH'\n#" + strings.Repeat("x", 80000) + "\nSH\n"
		h := hookInput{ToolName: "Bash", Cwd: plain}
		h.ToolInput.Command = cmd
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		shellPatchDirs(h, "")
		runtime.ReadMemStats(&after)
		if n := after.TotalAlloc - before.TotalAlloc; n > 64<<20 {
			t.Fatalf("discovery over one here-document fed to 8,000 shells allocated %d bytes", n)
		}
	})
}

// Codex's eighth code pass (2026-10-08, REJECT): one plausible P1 and three
// crafted P2s. Each failed before its repair.
func TestGateShellRouteEighthReviewForms(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	work := shellRouteWorkDir(t)
	for _, c := range []struct {
		cmd    string
		shells []string
	}{
		// fish takes -c's script attached.
		{"fish -capply_patch < p.patch", nil},
		// A comment inside env -S does not swallow the argv after it.
		{"env -S '# comment' apply_patch < p.patch", nil},
		// A stdin-fed script keeps the other inherited descriptors.
		{"bash 3<<<'apply_patch < p.patch' <<<'bash <&3'", []string{"/bin/bash"}},
	} {
		t.Run(c.cmd, func(t *testing.T) {
			for _, sh := range c.shells {
				if !kernelRuns(t, sh, work, c.cmd) {
					t.Fatalf("kernel control: %s does not run apply_patch for this form", sh)
				}
			}
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, c.cmd), true, "buddy gate")
		})
	}
	t.Run("deep-rebuilt-heredoc", func(t *testing.T) {
		// At the depth limit each of 8,000 shells used to rebuild the
		// inherited body before analyze refused it, without counting it.
		s := strings.Repeat("bash;", 8000)
		for i := 0; i < 7; i++ {
			s = "bash -c " + shellQuote(s)
		}
		cmd := "{ " + s + "; } <<'SH'\n#" + strings.Repeat("x", 79998) + "\nSH\n"
		h := hookInput{ToolName: "Bash", Cwd: t.TempDir()}
		h.ToolInput.Command = cmd
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		shellPatchDirs(h, "")
		runtime.ReadMemStats(&after)
		if n := after.TotalAlloc - before.TotalAlloc; n > 64<<20 {
			t.Fatalf("discovery at the depth limit allocated %d bytes", n)
		}
	})
}

// Codex's ninth code pass (2026-10-08, REJECT): two plausible P1s and a
// crafted P2, fixed here. Its other crafted P2, a persistent `exec <<'SH'`
// redirection inherited by later statements, is recorded open in D-073.
func TestGateShellRouteNinthReviewForms(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	const p = "apply_patch < p.patch"
	for _, cmd := range []string{
		// sudo -s with no command runs a shell that reads stdin.
		"sudo -s <<'SH'\n" + p + "\nSH\n",
		"sudo -i <<'SH'\n" + p + "\nSH\n",
		// fish: a C inside -c's attached script is not the -C option.
		"fish '-capply_patch < CHANGE.patch'",
	} {
		t.Run(cmd, func(t *testing.T) {
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, cmd), true, "buddy gate")
		})
	}
	t.Run("allow/sudo -s with a command", func(t *testing.T) {
		wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, "sudo -s grep apply_patch file <<'SH'\napply_patch\nSH\n"), false, "")
	})
	plain := t.TempDir()
	other := t.TempDir()
	for _, target := range []struct {
		dir  string
		deny bool
	}{{f.repo, true}, {other, false}} {
		// Uncertain -S text must not hide the argv after it.
		cmd := "env -S '# x' -C " + shellArg(target.dir) + " apply_patch < p.patch"
		t.Run("env-S-then-C/"+filepath.Base(target.dir), func(t *testing.T) {
			wantGate(t, f, plain, codexBashHookJSON("sess-b", plain, cmd), target.deny, "buddy gate")
		})
	}
}

// Found by the final mutation sweep (2026-10-08): no test put an over-budget
// brace word at the command position itself, where skipping it as a dynamic
// name would let bash run the apply_patch it expands to.
func TestGateShellRouteOverBudgetCommandWord(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	cmd := "{apply_patch,x{1..500}} < p.patch"
	if !kernelRuns(t, "/bin/bash", shellRouteWorkDir(t), cmd) {
		t.Fatal("kernel control: bash does not run apply_patch for this form")
	}
	wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, cmd), true, "buddy gate")
}

// Found by the 2026-10-11 run of the 39 mutants the 2026-10-08 sweep left
// unrun: each guard below passed the whole package with its check removed.

// A data program's operands are skipped before brace expansion, so they do not
// spend the field budget the WHOLE command shares. Spending it there would make
// a later brace word over budget, and an over-budget word at the command
// position refuses.
func TestGateShellRouteDataOperandsSpendNoFieldBudget(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	spend := "touch x{1..1100}" // more than shellMaxFields on its own
	// Control: the walker reads the brace word after the data program, so an
	// allow below is the budget left unspent, not a statement never read.
	refused := spend + "; {apply_patch,} < p.patch"
	if !kernelRuns(t, "/bin/bash", shellRouteWorkDir(t), refused) {
		t.Fatal("kernel control: bash does not run apply_patch for this form")
	}
	wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, refused), true, "buddy gate")
	wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, spend+"; {ls,-l}"), false, "")
}

// An over-budget word in text a command runs as shell is refused as over
// budget. Without its own check it falls into the dynamic-text arm, and the
// refusal tells the agent the text is "built at run time": false of a static
// brace word, and the wrong fix.
func TestGateShellRouteOverBudgetScriptWordSaysBudget(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	cmd := "eval {apply_patch,x{1..300}} < p.patch"
	if !kernelRuns(t, "/bin/bash", shellRouteWorkDir(t), cmd) {
		t.Fatal("kernel control: bash does not run apply_patch for this form")
	}
	wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, cmd), true, "brace expansion budget")
}

// Scripts the walker retains for the embedded-patch scan are bounded. The scan
// runs over every retained script after the walk, so retention is work even
// when the strings are shared and allocate nothing (which is why the
// allocation checks above cannot see these).
func TestShellRetainedScriptsAreBounded(t *testing.T) {
	retained := func(cmd string) (total, longest int, uncertain string) {
		ctx := &shellPatchContext{bases: []string{t.TempDir()}}
		w := &shellWalker{ctx: ctx}
		w.analyze(cmd, 0, true, nil)
		for _, s := range ctx.scripts {
			total += len(s)
			longest = max(longest, len(s))
		}
		return total, longest, w.route.uncertain
	}
	t.Run("env-S-tail", func(t *testing.T) {
		// A 60 KB command whose env -S tail brace-expands to about 7 MB. The
		// builder stops just past the script budget.
		cmd := "env -S 'echo' {" + strings.Repeat("a", 60<<10) + ",b}{1..120}"
		if len(cmd) > shellMaxScript {
			t.Fatalf("fixture: the command itself must fit the script budget (%d bytes)", len(cmd))
		}
		_, longest, uncertain := retained(cmd)
		if !strings.Contains(uncertain, "script budget") {
			t.Fatalf("control: the -S text must be built past the script budget and refused, got %q", uncertain)
		}
		if longest > 2*shellMaxScript {
			t.Fatalf("retained a %d-byte env -S script; the builder must stop near %d", longest, shellMaxScript)
		}
	})
	t.Run("eval-fields-read-by-many-shells", func(t *testing.T) {
		// Four shells read one here-document whose eval joins 256 brace
		// fields of 60 KB: four 15 MB scripts, each refused as over the script
		// budget and kept for the scan. (A here-document's own reads are
		// budgeted before analysis, so they never reach this bound.)
		cmd := "{ bash; bash; bash; bash; } <<'SH'\neval " + strings.Repeat("a", 60<<10) + "{1..256}\nSH\n"
		total, _, uncertain := retained(cmd)
		if !strings.Contains(uncertain, "script budget") {
			t.Fatalf("control: the joined eval text must be refused as over the script budget, got %q", uncertain)
		}
		if limit := shellMaxLexedBytes + shellMaxParsedBytes + len(cmd); total > limit {
			t.Fatalf("retained %d script bytes, want at most %d", total, limit)
		}
	})
}
