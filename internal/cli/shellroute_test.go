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
	"errors"
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
	out, err := c.CombinedOutput()
	// A non-zero EXIT is fine (the form may fail after running, or not run), but
	// a LAUNCH failure (the shell binary missing, say) must not pass as "did not
	// run" — that makes a negative control (a `kernelRuns(...) == false` assertion
	// for an ALLOW form) vacuously true (re-verify r3 finding 3).
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("kernel control: %s could not launch for %q: %v", shell, cmd, err)
	}
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

// The 2026-10-12 re-review of wrapper() and shell() (a narrowed constrained
// Codex pass, archived read-only at
// ~/Archives/buddy-system/2026-10-12-a01-rereview/, verdict3.md) returned
// REJECT on A, B, C and E and REFUTED D as stated. Each is a shell command a
// real shell runs apply_patch (or changes directory into a governed repo) for,
// while the recognizer returned neither found nor uncertainty. The fixes are in
// patch.go; these are the regressions, each watched to fail without its fix.
// F1/G1/G2 (fish) were then FIXED at the operator's decision, asserted in
// TestGateShellRouteReviewFolds (fish not on this box, controls NOT RUN; D-087's
// harness dimension is noted per finding). Each form here carries a kernel
// control where its shell is on this box; a form the shell does not actually
// run would make the gate's refusal a false denial dressed up as coverage.
func TestGateShellRouteReReviewForms(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	work := shellRouteWorkDir(t)

	// C: BSD and util-linux `script` with a transcript file (or none) and NO
	// command spawn $SHELL, which reads its script from the inherited stdin.
	// The gate missed it: wrapper() consumed the transcript operand and reached
	// the end of argv without reading stdin. C refuses under EVERY harness: the
	// heredoc holds a literal apply_patch at a command position. The kernel
	// control needs the heredoc PADDED well past script's pty line-discipline
	// buffer; a short body is swallowed before the spawned shell reads it
	// (measured on this box: 200 ':' lines never ran, 600 ran 8/8 with zero
	// leaked children, which is also why the re-review saw a short body not
	// run). darwin-only: the form is BSD here, and the pty timing is not
	// reproduced on CI's util-linux script. The gate's verdict is asserted on
	// every platform; only the proof that the shell runs it is platform-bound.
	t.Run("C/script-no-command-reads-stdin", func(t *testing.T) {
		cForm := "script -q /dev/null <<'SH'\n" + strings.Repeat(":\n", 1500) + "apply_patch < p.patch\nexit\nSH\n"
		if runtime.GOOS == "darwin" && shellOnBox(t, "/bin/bash") {
			if !kernelRuns(t, "/bin/bash", work, cForm) {
				t.Fatal("kernel control: a padded `script` heredoc does not run apply_patch")
			}
		} else {
			t.Logf("kernel control not run on %s: script's no-command stdin shell is BSD- and pty-timing-specific", runtime.GOOS)
		}
		wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, cForm), true, "buddy gate")
		wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, cForm), true, "runs apply_patch")
	})

	// E: an over-budget brace word consumed as an OPTION VALUE was skipped
	// unchecked, but a shell expands it IN PLACE into more options and a
	// command. `env -u {X,-S,apply_patch,x{1..300}}` becomes
	// `env -u X -S apply_patch ...`; `bash -o {posix,-c,apply_patch,x{1..300}}`
	// becomes `bash -o posix -c apply_patch ...`; `--rcfile` is the same. An
	// exhausted bound refuses under EVERY harness (the walk stopped before it
	// could rule out a literal), and only where a ledger governs (invariant 3).
	// The redirect is dropped from the finding's spelling so the control's temp
	// dir needs no p.patch; the route is identical.
	plain := t.TempDir()
	for _, eForm := range []string{
		"env -u {X,-S,apply_patch,x{1..300}}",
		"bash -o {posix,-c,apply_patch,x{1..300}}",
		"bash --rcfile {f,-c,apply_patch,x{1..300}}",
		// An over-budget brace as the -S VALUE itself: a shell expands it into
		// env's own argv, so `env -S {apply_patch,x{1..500}}` runs the
		// apply_patch hidden in the first field, with no literal outside the
		// brace for `found` to catch.
		"env -S {apply_patch,x{1..500}}",
	} {
		t.Run("E/"+eForm, func(t *testing.T) {
			if !kernelRuns(t, "/bin/bash", work, eForm) {
				t.Fatalf("kernel control: bash does not run apply_patch for %q", eForm)
			}
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, eForm), true, "budget")
			wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, eForm), true, "budget")
			// Only where a ledger governs.
			wantGate(t, f, plain, codexBashHookJSON("sess-b", plain, eForm), false, "")
		})
	}

	// The persistent `exec <<'SH'` redirection a LATER statement's shell reads
	// is the ONE crafted form left open by decision (D-073, round 9): the
	// walker scopes a redirection to its own statement, so `exec`'s heredoc is
	// not carried to the trailing `bash`. This is a documented cooperative
	// limit, not a fix: the gate ALLOWS it even where a ledger governs, and the
	// control proves a real shell runs it, so the gap is genuine and accepted,
	// not a form the shell would never run. It also guards the finding-C fix
	// from over-reaching: `exec` is not `script`, so the no-command stdin arm
	// must not fire for it.
	t.Run("exec-heredoc-cooperative-limit", func(t *testing.T) {
		execForm := "exec <<'SH'\napply_patch\nSH\nbash\n"
		for _, sh := range []string{"/bin/bash", "/bin/zsh"} {
			if shellOnBox(t, sh) && !kernelRuns(t, sh, work, execForm) {
				t.Fatalf("control: %s does not run apply_patch for the exec-heredoc form", sh)
			}
		}
		wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, execForm), false, "")
		wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, execForm), false, "")
	})
}

// Jurisdiction from an UNGOVERNED cwd: findings A and B put the directory
// change inside env's -S handling, which the recognizer failed to read, so a
// patch bound for a governed repo was allowed from outside it. Each form
// changes to the target and runs apply_patch there; it must refuse when the
// target is governed and pass when it is not. The kernel control reproduces the
// directory change with `pwd -P` and asserts the shell lands where the gate
// says (the pattern of TestGateShellRouteJurisdiction).
//
// Harness dimension (D-087): A changes the CODEX verdict only — under Claude a
// dynamic -S value is no refusal, so the walker already read past it to the
// literal apply_patch and discovered -C; under Codex it stopped at the
// uncertainty and missed -C. B changes BOTH harnesses: its -S value is a known
// literal, and jurisdiction discovery (always non-literalOnly) skipped the -C
// inside it under either harness.
func TestGateShellRouteReReviewJurisdiction(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	plain := t.TempDir()
	other := t.TempDir()
	realRepo, _ := filepath.EvalSymlinks(f.repo)
	realOther, _ := filepath.EvalSymlinks(other)

	// A: `env -S "$x" -C <dir> apply_patch`. A real env with an empty or
	// option-only $x still honours -C; the gate's uncertain branch returned
	// before reading it. The assertion is under Codex, the harness the fix
	// changes.
	t.Run("A/env-S-dynamic-then-C", func(t *testing.T) {
		for _, target := range []struct {
			dir, real string
			deny      bool
		}{{f.repo, realRepo, true}, {other, realOther, false}} {
			name := filepath.Base(target.dir)
			if shellOnBox(t, "/bin/sh") {
				land := "x=; env -S \"$x\" -C " + shellArg(target.dir) + " pwd -P"
				k := exec.Command("/bin/sh", "-c", land)
				k.Dir = plain
				k.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + plain}
				if out, err := k.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != target.real {
					t.Fatalf("kernel control: sh lands in %q (err %v), want %q", out, err, target.real)
				}
			}
			cmd := "x=; env -S \"$x\" -C " + shellArg(target.dir) + " apply_patch < p.patch"
			t.Run(name, func(t *testing.T) {
				wantGate(t, f, plain, codexBashHookJSON("sess-b", plain, cmd), target.deny, "buddy gate")
			})
		}
	})

	// B: `env -S '-C <dir> #' apply_patch`. BSD/macOS env splits `-C <dir>` out
	// of the -S string and ignores the `#` comment, changing directory before
	// the trailing argv runs; the gate skipped the whole -S text. B refuses
	// under BOTH harnesses, so both are asserted. darwin-only control: GNU env's
	// `#` handling is not assumed.
	t.Run("B/env-S-dir-then-comment", func(t *testing.T) {
		for _, target := range []struct {
			dir, real string
			deny      bool
		}{{f.repo, realRepo, true}, {other, realOther, false}} {
			name := filepath.Base(target.dir)
			if runtime.GOOS == "darwin" && shellOnBox(t, "/bin/sh") {
				land := "env -S " + shellQuote("-C "+target.dir+" #") + " pwd -P"
				k := exec.Command("/bin/sh", "-c", land)
				k.Dir = plain
				k.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + plain}
				if out, err := k.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != target.real {
					t.Fatalf("kernel control: sh lands in %q (err %v), want %q", out, err, target.real)
				}
			}
			cmd := "env -S " + shellQuote("-C "+target.dir+" #") + " apply_patch < p.patch"
			t.Run(name, func(t *testing.T) {
				wantGate(t, f, plain, codexBashHookJSON("sess-b", plain, cmd), target.deny, "buddy gate")
				wantGate(t, f, plain, bashHookJSON("sess-b", plain, cmd), target.deny, "")
			})
		}
	})
}

// The FULL-tier review of D-106 (Codex design pass + a three-subagent panel)
// surfaced adjacent gaps in the C and E fixes, folded here. Each is confirmed on
// this box where its shell is BSD; the util-linux `script` log-option desync is
// NOT RUN here (macOS script lacks those options) and is closed by over-reading
// the option table, below.
func TestGateShellRouteReReviewFollowUp(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	work := shellRouteWorkDir(t)
	plain := t.TempDir()

	// F4 (a false denial the first C fix introduced): `script -p` REPLAYS a
	// recording and spawns no shell, so it never reads stdin as a script.
	// The gate must ALLOW it. Control: the real shell runs NOTHING for it
	// (otherwise the allow would be a miss).
	t.Run("F4/script-playback-allowed", func(t *testing.T) {
		pb := "script -p /dev/null <<'SH'\n" + strings.Repeat(":\n", 1500) + "apply_patch < p.patch\nexit\nSH\n"
		if runtime.GOOS == "darwin" && shellOnBox(t, "/bin/bash") {
			if kernelRuns(t, "/bin/bash", work, pb) {
				t.Fatal("control: script -p playback unexpectedly ran apply_patch; the allow would be a miss")
			}
		} else {
			t.Logf("kernel control not run on %s: BSD script -p playback", runtime.GOOS)
		}
		wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, pb), false, "")
		wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, pb), false, "")
	})

	// F1a (a C miss): `script -q -- /dev/null` with no command still reads
	// stdin; the `--` used to stop the scan before the transcript file was
	// consumed. Caught under every harness (literal in the heredoc).
	t.Run("F1a/script-dashdash-reads-stdin", func(t *testing.T) {
		dd := "script -q -- /dev/null <<'SH'\n" + strings.Repeat(":\n", 1500) + "apply_patch < p.patch\nexit\nSH\n"
		if runtime.GOOS == "darwin" && shellOnBox(t, "/bin/bash") {
			if !kernelRuns(t, "/bin/bash", work, dd) {
				t.Fatal("kernel control: `script -q -- /dev/null` heredoc does not run apply_patch")
			}
		} else {
			t.Logf("kernel control not run on %s: BSD script", runtime.GOOS)
		}
		wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, dd), true, "buddy gate")
		wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, dd), true, "runs apply_patch")
	})

	// finding 6 (a false denial an earlier fold introduced and this one
	// removed): `env -S echo x{1..300}` reconstructs to `env echo "$_"`; echo is
	// a data command, so its over-budget args decide nothing and the command
	// must be ALLOWED. The crafted inverse (a clean -S OPTION value then an
	// over-budget brace tail that expands to `-S apply_patch`) is recorded open
	// in D-106 as a cooperative residue rather than false-denying echo.
	t.Run("finding6/env-S-data-command-overbudget-args-allowed", func(t *testing.T) {
		form := "env -S echo x{1..300}"
		wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, form), false, "")
		wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, form), false, "")
	})

	// F6 (an over-budget value consumer outside wrapper/shell): `git -C <brace>`
	// takes the brace as the directory, but a shell expands it into git's argv
	// (`git -C . bisect run apply_patch ...`). Now exhausted; refuses under both,
	// only where governed. Control NOT RUN: the running route needs an active
	// bisect in a real repo (crafted); the verdict is pure parsing.
	t.Run("F6/git-C-overbudget", func(t *testing.T) {
		form := "git -C {.,bisect,run,apply_patch,x{1..300}}"
		wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, form), true, "budget")
		wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, form), true, "budget")
		wantGate(t, f, plain, codexBashHookJSON("sess-b", plain, form), false, "")
	})
}

// The FULL-tier review's codex@xhigh CODE pass (REJECT, 10 findings) and the
// opus@xhigh cross-check (ACCEPT WITH CHANGES, 9) on the first fold found
// adjacent gaps — several regressions the first follow-up fold introduced.
// Folded here (D-106). Forms whose shell or option set is NOT on this box
// (util-linux `script` log/timing options; `timeout`; fish; an active git
// bisect; a sourcing shell fed stdin) are asserted on the gate's verdict alone,
// said loudly; the gate is pure text parsing and needs no shell.
func TestGateShellRouteReviewFolds(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	work := shellRouteWorkDir(t)
	plain := t.TempDir()
	const h = "<<'SH'\napply_patch < p.patch\nSH\n"

	// Playback precision (codex 1,2): util-linux `-T` (timing) and `-O<file>`
	// (log-out) DO launch the stdin shell; the first fold read 'T' and the 'p'
	// inside `-O/tmp/x.log` as BSD playback and allowed them. util-linux only,
	// NOT RUN here (BSD `-T` is playback, `-O` is not an option).
	for _, form := range []string{
		"script -q -T timing /dev/null " + h,
		"script -q -O/tmp/session.log " + h,
		"script -q --log-timing timing /dev/null " + h, // codex 3: --log-timing added to the table
	} {
		t.Run("script-utillinux/"+form[:24], func(t *testing.T) {
			t.Logf("kernel control NOT RUN: util-linux script option on this BSD box")
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, form), true, "buddy gate")
			wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, form), true, "runs apply_patch")
		})
	}

	// script -c supplies a command (codex 5): with a dynamic value the scan
	// continued to the end under Claude and hit the new stdin arm, a false
	// denial. `scriptCmd` suppresses the arm. util-linux -c, NOT RUN here.
	t.Run("finding5/script-c-dynamic-not-stdin", func(t *testing.T) {
		form := "cmd=cat; script -q -c \"$cmd\" /dev/null " + h
		t.Logf("kernel control NOT RUN: util-linux script -c on this BSD box")
		wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, form), false, "")
	})

	// Over-budget words consumed where a command could expand in place, at
	// positions that are NOT bulk data: the `--` lead operand, a sourced script
	// name, and trap's action (codex 8 + cross-check source/trap). Each refuses
	// as exhausted, only where governed. The find and env-S-tail over-budget
	// guards were reverted in round 2 (they false-denied common data braces);
	// those forms are recorded open in D-106. trap RUNS here; timeout/source do
	// not (not on box / crafted).
	overBudget := []struct {
		name, form string
		run        bool
	}{
		{"timeout-dashdash", "timeout -- {1,apply_patch,x{1..300}}", false}, // codex 8; timeout not on box
		{"source-devstdin", "source {/dev/stdin,x{1..300}}", false},         // cross-check; needs a sourcing shell on stdin
		{"trap-action", "trap {apply_patch,EXIT,x{1..300}}; true", true},    // cross-check
	}
	for _, c := range overBudget {
		t.Run("overbudget/"+c.name, func(t *testing.T) {
			if c.run {
				if !kernelRuns(t, "/bin/bash", work, c.form) {
					t.Fatalf("kernel control: bash does not run apply_patch for %q", c.form)
				}
			} else {
				t.Logf("kernel control NOT RUN: shell/tool not on this box or route is crafted")
			}
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, c.form), true, "budget")
			wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, c.form), true, "budget")
			wantGate(t, f, plain, codexBashHookJSON("sess-b", plain, c.form), false, "")
		})
	}

	// Fish F1/G1/G2 (operator's decision, 2026-10-27, relayed by orc-7), from
	// the stated behaviour in verdict3.md, NOT re-derived from fish's source.
	// fish is NOT installed on this box, so every kernel control is NOT RUN,
	// said loudly; the gate verdict is asserted regardless.
	t.Run("fish/F1-repeated-c-refuses", func(t *testing.T) {
		// fish runs BOTH -c commands; the second is dynamic, so it is uncertain
		// and refuses under Codex (like any dynamic command text, D-087) and is
		// allowed under Claude.
		form := `x=apply_patch; fish -c true -c "$x" < p.patch`
		t.Logf("kernel control NOT RUN: fish is not installed on this box")
		wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, form), true, "cannot rule out")
		wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, form), false, "")
	})
	for _, g := range []struct{ name, form string }{
		{"G1-no-execute-long", "fish --no-execute -c 'apply_patch < p.patch'"},
		{"G2-n-before-attached-c", "fish -nc'apply_patch < p.patch'"},
	} {
		t.Run("fish/"+g.name, func(t *testing.T) {
			// --no-execute / -n make fish check syntax only: it runs nothing, so
			// the gate must ALLOW, under every harness.
			t.Logf("kernel control NOT RUN: fish is not installed on this box")
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, g.form), false, "")
			wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, g.form), false, "")
		})
	}
}

// The codex@xhigh re-verify (round 1, REJECT, 8) on the fold found more — five
// side effects of the fold itself. Folded (D-106). fish is NOT on this box, so
// its controls are NOT RUN, said loudly; the gate verdict is asserted anyway.
func TestGateShellRouteReVerifyR1(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)

	// Finding 1: fish's LONG --command went to operands, which the fish early
	// return ignores, so `fish --command='apply_patch' -c true` ran apply_patch.
	// The long --command now joins fishCmds like a short -c. The value is a
	// literal apply_patch, so it refuses under every harness.
	t.Run("finding1/fish-long-command", func(t *testing.T) {
		form := `fish --command='apply_patch < p.patch' -c true`
		t.Logf("kernel control NOT RUN: fish is not installed on this box")
		wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, form), true, "apply_patch")
		wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, form), true, "apply_patch")
	})

	// Finding 2: an empty-string arg decoded as a known "" and the fish -c case
	// indexed t[0] without a length guard — a PANIC. It must now ALLOW (true is
	// analysed, "" ignored) and, above all, not crash. The positive control is
	// that the gate answers at all.
	t.Run("finding2/fish-empty-arg-no-panic", func(t *testing.T) {
		form := `fish -c true ""`
		t.Logf("kernel control NOT RUN: fish is not installed on this box")
		wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, form), false, "")
		wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, form), false, "")
	})

	// Finding 5: the blanket gitRoute over-budget guard false-denied data
	// pathspecs. `git diff -- path{1..300}` is 300 data pathspecs with no
	// command and must be ALLOWED. (The over-budget subcommand-position brace
	// `git -C . {bisect,run,…}` is recorded OPEN in D-106 as crafted.)
	t.Run("finding5/git-diff-pathspecs-allowed", func(t *testing.T) {
		form := "git diff -- path{1..300}"
		wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, form), false, "")
		wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, form), false, "")
	})

	// An env -S with a DATA command and over-budget args is allowed (the
	// over-budget tail is now always masked; the crafted env/-u/wrapper forms
	// are recorded open in D-106). The control proves the shell does NOT run
	// apply_patch, so the allow is not a miss (re-verify r2 finding 9).
	t.Run("env-S-echo-allowed", func(t *testing.T) {
		work := shellRouteWorkDir(t)
		form := "env -S echo {apply_patch,x{1..300}}"
		if kernelRuns(t, "/bin/bash", work, form) {
			t.Fatal("control: `env -S echo {…}` unexpectedly ran apply_patch; the allow would be a miss")
		}
		wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, form), false, "")
		wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, form), false, "")
	})
}

// The codex@xhigh re-verify round 2 (REJECT, 9) found that round 1's find and
// env-S-tail over-budget guards false-denied common DATA braces, and that the
// fish init-command / missing-value paths mis-handled no-execute and command
// mode. Per the operator's direction (a false denial costs every Claude session
// a tool call; a crafted brace-budget bypass costs nothing under Claude's allow
// arm), the data-brace guards were REVERTED (the crafted forms recorded open in
// D-106) and the fish paths fixed. These assert the false denials are gone.
func TestGateShellRouteReVerifyR2(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	work := shellRouteWorkDir(t)

	// Finding 4: an over-budget DATA brace inside/at a find action is no longer
	// refused. grep runs; nothing else. Control: it does not run apply_patch.
	t.Run("finding4/find-exec-data-brace-allowed", func(t *testing.T) {
		form := `find . -maxdepth 0 -exec grep -l foo path{1..300} \;`
		if kernelRuns(t, "/bin/bash", work, form) {
			t.Fatal("control: find -exec grep data brace unexpectedly ran apply_patch")
		}
		wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, form), false, "")
		wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, form), false, "")
	})

	// Finding 5: env -S whose split value runs a data command (possibly quoted,
	// behind `env` or a leading option) with an over-budget data tail is no
	// longer refused. Controls: none runs apply_patch.
	for _, form := range []string{
		`env -S '"echo"' path{1..300}`,
		"env -S 'env echo' path{1..300}",
		"env -S '-i echo' path{1..300}",
	} {
		t.Run("finding5/"+form[:18], func(t *testing.T) {
			if kernelRuns(t, "/bin/bash", work, form) {
				t.Fatalf("control: %q unexpectedly ran apply_patch", form)
			}
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, form), false, "")
			wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, form), false, "")
		})
	}

	// Finding 6: fish's init commands are deferred, so a LATER --no-execute
	// suppresses them; `fish --command true --no-execute --init-command
	// apply_patch` runs nothing → ALLOW. fish not on box, control NOT RUN.
	t.Run("finding6/fish-init-command-noexec-allowed", func(t *testing.T) {
		form := "fish --command true --no-execute --init-command apply_patch"
		t.Logf("kernel control NOT RUN: fish is not installed on this box")
		wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, form), false, "")
		wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, form), false, "")
	})

	// Finding 7: `fish --command` with NO value is a fish error (missing
	// required argument), not a stdin shell, so the heredoc must not be read →
	// ALLOW. fishSawCommand keeps it out of the stdin arm. fish not on box.
	t.Run("finding7/fish-command-missing-value-allowed", func(t *testing.T) {
		form := "fish --command <<'SH'\napply_patch < p.patch\nSH\n"
		t.Logf("kernel control NOT RUN: fish is not installed on this box")
		wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, form), false, "")
		wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, form), false, "")
	})
}

// codex@xhigh re-verify round 3 (REJECT, 5) found a P1 regression from the r2
// fold (init commands wrongly suppressing stdin) and a pre-existing fish -C
// cluster false denial. fish is NOT on this box, so controls are NOT RUN, said
// loudly; the gate verdict is asserted regardless.
func TestGateShellRouteReVerifyR3(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)

	// Finding 1: an init command (`-C`/`--init-command`) alone does NOT put fish
	// in command mode, so fish still reads its main script from stdin or a file.
	// `fish -C true <<'SH' … apply_patch … SH` reads the heredoc; the r2 early
	// return (on len(fishCmds) > 0) skipped it. Now it is caught — DENY.
	for _, form := range []string{
		"fish -C true <<'SH'\napply_patch < p.patch\nSH\n",
		"fish --init-command=true /dev/stdin <<'SH'\napply_patch < p.patch\nSH\n",
	} {
		t.Run("finding1/fish-init-then-stdin", func(t *testing.T) {
			t.Logf("kernel control NOT RUN: fish is not installed on this box")
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, form), true, "buddy gate")
			wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, form), true, "runs apply_patch")
		})
	}

	// Finding 2: `fish -nC apply_patch` — the n before the C in the cluster is
	// no-execute, so the init command is only syntax-checked → ALLOW. The -C
	// branch used to ignore the preceding n (the -c branch already handled it).
	t.Run("finding2/fish-nC-noexec-allowed", func(t *testing.T) {
		form := "fish -nC apply_patch"
		t.Logf("kernel control NOT RUN: fish is not installed on this box")
		wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, form), false, "")
		wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, form), false, "")
	})
}

// codex@xhigh re-verify round 4 (REJECT, 6) on 41e06e5: round 3's fish `-nC`
// fold read a C inside ANOTHER option's value (a P1 bypass under both
// harnesses), and two fish false denials (an init command missing its value;
// --version). fish is NOT on this box, so every kernel control is NOT RUN, said
// loudly; the gate's verdict is asserted regardless, each refusal beside a
// control that allows and each allow beside a same-shape form that refuses.
func TestGateShellRouteReVerifyR4(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	plain := t.TempDir()

	// Finding 1: fish reads a cluster as getopt does, so the first letter that
	// takes a value (c C d o f p, fish 3's D) takes the rest of the cluster or
	// the next word. `-p/tmp/initCommands.prof` is a profile path; the C inside
	// it, and its n read as -n, allowed the -c after it. A detached value was
	// read as the script file, and fish's -o value as bash's `-o noexec`. Each
	// runs a literal apply_patch, so it refuses under every harness, and only
	// where a ledger governs.
	for _, c := range []struct{ name, form string }{
		{"p-attached", "fish -p/tmp/initCommands.prof -c 'apply_patch < p.patch'"},
		{"p-detached", "fish -p /tmp/initCommands.prof -c 'apply_patch < p.patch'"},
		{"profile-long-detached", "fish --profile /tmp/initCommands.prof -c 'apply_patch < p.patch'"},
		{"o-value-is-not-noexec", "fish -o noexec -c 'apply_patch < p.patch'"},
		{"d-detached-then-stdin", "fish -d proc <<'SH'\napply_patch < p.patch\nSH\n"},
	} {
		t.Run("finding1/"+c.name, func(t *testing.T) {
			t.Logf("kernel control NOT RUN: fish is not installed on this box")
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, c.form), true, "apply_patch")
			wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, c.form), true, "apply_patch")
			wantGate(t, f, plain, codexBashHookJSON("sess-b", plain, c.form), false, "")
		})
	}
	// The value takes the WHOLE rest of the cluster: with no -c after it the
	// command is data, and in `-pc` the c is the profile path, so the quoted
	// text is the script FILE (cooperative) — both allowed under every harness.
	for _, c := range []struct{ name, form string }{
		{"p-attached-data-command", "fish -p/tmp/initCommands.prof -c 'echo ok'"},
		{"pc-c-is-the-value", "fish -pc 'apply_patch < p.patch'"},
	} {
		t.Run("finding1/allow/"+c.name, func(t *testing.T) {
			t.Logf("kernel control NOT RUN: fish is not installed on this box")
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, c.form), false, "")
			wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, c.form), false, "")
		})
	}
	// A consumed value is still checked for the brace budget (finding E): a
	// shell expands `{x,-c,apply_patch,…}` in place into `-p x -c apply_patch`.
	for _, form := range []string{
		"fish -p {x,-c,apply_patch,x{1..300}}",
		"fish --profile {x,-c,apply_patch,x{1..300}}",
	} {
		t.Run("finding1/budget/"+form[:10], func(t *testing.T) {
			t.Logf("kernel control NOT RUN: fish is not installed on this box")
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, form), true, "budget")
			wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, form), true, "budget")
			wantGate(t, f, plain, codexBashHookJSON("sess-b", plain, form), false, "")
		})
	}

	// Finding 2: a value option ending the line is a fish usage error, so fish
	// exits before reading stdin: ALLOW. Control: the same line with the value
	// given reads stdin and refuses.
	const hs = " <<<'apply_patch < p.patch'"
	for _, form := range []string{
		"fish -C true -C" + hs,
		"fish -C true --init-command" + hs,
		"fish -C true --profile" + hs,
	} {
		t.Run("finding2/"+form[13:], func(t *testing.T) {
			t.Logf("kernel control NOT RUN: fish is not installed on this box")
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, form), false, "")
			wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, form), false, "")
		})
	}
	t.Run("finding2/control-value-given", func(t *testing.T) {
		form := "fish -C true -C true" + hs
		wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, form), true, "apply_patch")
		wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, form), true, "apply_patch")
	})

	// Finding 3: --version (-v) and --print-debug-categories print and exit
	// while fish reads its options, before any command or script runs
	// (documented): ALLOW. Control: without them fish reads stdin and refuses.
	for _, form := range []string{
		"fish -C true --version" + hs,
		"fish -C true -v" + hs,
		"fish -C true --print-debug-categories" + hs,
		"fish -v -c 'apply_patch < p.patch'",
	} {
		t.Run("finding3/"+form[5:16], func(t *testing.T) {
			t.Logf("kernel control NOT RUN: fish is not installed on this box")
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, form), false, "")
			wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, form), false, "")
		})
	}
	t.Run("finding3/control-no-exit-option", func(t *testing.T) {
		form := "fish -C true" + hs
		wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, form), true, "apply_patch")
		wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, form), true, "apply_patch")
	})
}
