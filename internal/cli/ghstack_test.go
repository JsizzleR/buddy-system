package cli

// D-108: gh stack's rewriting subcommands need .buddy/slot/gh-stack. Every
// refused spelling carries a KERNEL CONTROL where the box can run it: the same
// command under a real shell with recording stubs `gh` and `gh-stack` first on
// PATH, which must receive the subcommand in their argv. A form the shell does
// not hand to gh would make its refusal a false denial dressed up as coverage.
// Every refusal has its positive control (the holder is admitted for the same
// spelling), and every allow list runs with the guard armed in the same
// fixture, so "it refused" and "it never ran" cannot look alike.

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// ghStubs puts recording gh and gh-stack stubs, and an unknown wrapper, in a
// fresh directory. Each stub appends its own name and argv as one line to $MARK.
func ghStubs(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	rec := "#!/bin/sh\n{ printf '%s' \"$(basename \"$0\")\"; for a in \"$@\"; do printf ' %s' \"$a\"; done; printf '\\n'; } >> \"$MARK\"\n"
	for name, body := range map[string]string{"gh": rec, "gh-stack": rec, "wrap": "exec \"$@\"\n"} {
		if body != rec {
			body = "#!/bin/sh\n" + body
		}
		mustWrite(t, filepath.Join(bin, name), body)
		if err := os.Chmod(filepath.Join(bin, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

// ghKernelRuns runs shell -c cmd in dir with the stubs first on PATH and
// returns what they recorded ("" when neither ran within 3 s).
func ghKernelRuns(t *testing.T, shell, bin, dir, cmd string) string {
	t.Helper()
	mark := filepath.Join(t.TempDir(), "argv")
	c := exec.Command(shell, "-c", cmd)
	c.Dir = dir
	c.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "MARK=" + mark, "HOME=" + dir}
	c.Stdin = nil
	out, err := c.CombinedOutput()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("kernel control: %s could not launch for %q: %v", shell, cmd, err)
	}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if b, err := os.ReadFile(mark); err == nil && strings.Contains(string(b), "\n") {
			return string(b)
		}
	}
	t.Logf("%s ran neither stub for %q: %s", shell, cmd, out)
	return ""
}

// wantGateBoth asserts the verdict under the Claude and the Codex hook shapes.
func wantGateBoth(t *testing.T, f *fixture, cwd, session, cmd string, deny bool, contains string) {
	t.Helper()
	wantGate(t, f, cwd, bashHookJSON(session, cwd, cmd), deny, contains)
	wantGate(t, f, cwd, codexBashHookJSON(session, cwd, cmd), deny, contains)
}

func claimGHSlot(t *testing.T, f *fixture, session, desc string) {
	t.Helper()
	if out, errw, code := f.run(t, f.repo, "", "claim", "gh-stack", "--session", session, "--desc", desc, "--scope", ".buddy/slot/gh-stack"); code != 0 {
		t.Fatalf("claim gh-stack as %s: %s %s", session, out, errw)
	}
}

func TestGateGHStackFormsTheShellRuns(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	bin := ghStubs(t)
	work := t.TempDir()
	if err := os.Mkdir(filepath.Join(work, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The recipe's exact one-call shape (skills/buddy/SKILL.md), with the
	// fixture's directories: subshells, sandbox-exec, the dynamic profile word.
	recipe := "off='(version 1)(allow default)(deny network*)'\nexport GH_STACK_NO_UPDATE_NOTIFIER=1\n" +
		"(cd " + shellArg(f.repo) + " &&\n  sandbox-exec -p \"$off\" gh stack init --base main a b </dev/null &&\n" +
		"  sandbox-exec -p \"$off\" gh stack rebase --no-trunk b </dev/null) &&\n" +
		"(cd " + shellArg(f.wtB) + " && sandbox-exec -p \"$off\" gh stack unstack --local </dev/null)"
	darwinOnly := map[string]string{
		"GH stack rebase":               "the default macOS volume folds case, so GH resolves to gh; Linux's does not",
		"caffeinate -i gh stack rebase": "caffeinate is a macOS binary",
		"sandbox-exec -p '(version 1)(allow default)(deny network*)' gh stack rebase --no-trunk b": "sandbox-exec is a macOS binary",
		recipe: "sandbox-exec is a macOS binary",
	}
	cases := []struct {
		cmd    string
		shells []string // kernel controls: each must hand the stub the subcommand
		want   string   // what the stub's recorded argv must contain
		sub    string   // the subcommand the deny names
	}{
		// Every gated subcommand, plainly.
		{"gh stack rebase", []string{"/bin/bash", "/bin/zsh"}, "gh stack rebase", "rebase"},
		{"gh stack rebase --no-trunk b", []string{"/bin/bash"}, "gh stack rebase --no-trunk b", "rebase"},
		{"gh stack rebase --abort", []string{"/bin/bash"}, "stack rebase --abort", "rebase"},
		{"gh stack sync --prune", []string{"/bin/bash"}, "stack sync", "sync"},
		{"gh stack push", []string{"/bin/bash"}, "stack push", "push"},
		{"gh stack submit", []string{"/bin/bash"}, "stack submit", "submit"},
		{"gh stack merge", []string{"/bin/bash"}, "stack merge", "merge"},
		{"gh stack link", []string{"/bin/bash"}, "stack link", "link"},
		{"gh stack unstack --local", []string{"/bin/bash"}, "stack unstack --local", "unstack"},
		{"gh stack delete", []string{"/bin/bash"}, "stack delete", "delete"},
		{"gh stack init --base main a b", []string{"/bin/bash"}, "stack init", "init"},
		{"gh stack add x -m 'msg'", []string{"/bin/bash"}, "stack add", "add"},
		{"gh stack modify --continue", []string{"/bin/bash"}, "stack modify", "modify"},
		{"gh stack checkout 12", []string{"/bin/bash"}, "stack checkout", "checkout"},
		{"gh stack alias", []string{"/bin/bash"}, "stack alias", "alias"},
		// The extension-exec spellings and the binary by path (basename).
		{"gh extension exec stack rebase", []string{"/bin/bash"}, "extension exec stack rebase", "rebase"},
		{"gh ext exec stack rebase", []string{"/bin/bash"}, "ext exec stack rebase", "rebase"},
		{"gh extensions exec stack rebase", []string{"/bin/bash"}, "extensions exec stack rebase", "rebase"},
		{"gh-stack rebase", []string{"/bin/bash"}, "gh-stack rebase", "rebase"},
		{bin + "/gh-stack rebase --no-trunk b", []string{"/bin/bash"}, "gh-stack rebase", "rebase"},
		{"GH stack rebase", []string{"/bin/bash"}, "stack rebase", "rebase"},
		// Through wrappers, known and unknown.
		{"env GH_STACK_NO_UPDATE_NOTIFIER=1 gh stack rebase", []string{"/bin/bash"}, "stack rebase", "rebase"},
		{"command gh stack rebase", []string{"/bin/bash"}, "stack rebase", "rebase"},
		{"exec gh stack rebase", []string{"/bin/bash"}, "stack rebase", "rebase"},
		{"nice gh stack rebase", []string{"/bin/bash"}, "stack rebase", "rebase"},
		{"caffeinate -i gh stack rebase", []string{"/bin/bash"}, "stack rebase", "rebase"},
		{"wrap gh stack rebase", []string{"/bin/bash"}, "stack rebase", "rebase"},
		{"wrap -x gh stack rebase", nil, "", "rebase"}, // an unknown wrapper's operands are all command positions
		{"sandbox-exec -p '(version 1)(allow default)(deny network*)' gh stack rebase --no-trunk b", []string{"/bin/bash"}, "stack rebase --no-trunk b", "rebase"},
		{"echo b | xargs gh stack rebase", []string{"/bin/bash"}, "stack rebase b", "rebase"},
		{`find . -maxdepth 0 -exec gh stack rebase \;`, []string{"/bin/bash"}, "stack rebase", "rebase"},
		{"git -C . bisect run gh stack rebase", nil, "", "rebase"}, // needs an active bisect; the route is gitRoute's
		// Inside shells, functions, compound commands.
		{"bash -c 'gh stack rebase'", []string{"/bin/bash"}, "stack rebase", "rebase"},
		{"sh -c 'gh stack rebase'", []string{"/bin/sh"}, "stack rebase", "rebase"},
		{"zsh -c 'gh stack rebase'", []string{"/bin/zsh"}, "stack rebase", "rebase"},
		{"eval 'gh stack rebase'", []string{"/bin/bash", "/bin/zsh"}, "stack rebase", "rebase"},
		{"trap 'gh stack rebase' EXIT", []string{"/bin/bash", "/bin/zsh"}, "stack rebase", "rebase"},
		{"bash <<'EOF'\ngh stack rebase\nEOF\n", []string{"/bin/bash"}, "stack rebase", "rebase"},
		{"run() { gh stack rebase; }; run", []string{"/bin/bash", "/bin/zsh"}, "stack rebase", "rebase"},
		{"{ gh stack rebase; }", []string{"/bin/bash"}, "stack rebase", "rebase"},
		{"( gh stack rebase )", []string{"/bin/bash"}, "stack rebase", "rebase"},
		{"if true; then gh stack rebase; fi", []string{"/bin/bash"}, "stack rebase", "rebase"},
		{"for x in 1; do gh stack rebase; done", []string{"/bin/bash"}, "stack rebase", "rebase"},
		{"gh stack view && gh stack rebase", []string{"/bin/bash"}, "stack rebase", "rebase"},
		{"(cd sub && gh stack rebase)", []string{"/bin/bash"}, "stack rebase", "rebase"},
		{recipe, []string{"/bin/bash"}, "stack init --base main a b", "init"},
		// Decoding: brace expansion, concatenation.
		{"gh {stack,} rebase", []string{"/bin/bash"}, "stack rebase", "rebase"},
		{`gh stack re"ba"se`, []string{"/bin/bash"}, "stack rebase", "rebase"},
		{"gh 'stack' rebase", []string{"/bin/bash"}, "stack rebase", "rebase"},
		// Help cancelled or placed before the subcommand: the extension binary
		// RUNS rebase for these (measured, D-108). The stubs show the shell
		// hands them over; what gh-stack then does is the native measurement.
		{"gh stack rebase --help --help=false", []string{"/bin/bash"}, "stack rebase --help --help=false", "rebase"},
		{"gh stack --help=false rebase", []string{"/bin/bash"}, "stack --help=false rebase", "rebase"},
		{"gh stack --help --help=false rebase", []string{"/bin/bash"}, "stack --help --help=false rebase", "rebase"},
		// A value flag BEFORE the subcommand: cobra hands the next word to the
		// flag and finds the subcommand after it, so the extension binary RAN
		// init and add for the first three (measured; the panel's bypass).
		// The chain and the dynamic value are refused by the same reading;
		// `--remote` makes init itself fail on the unknown flag (measured).
		{"gh stack --base main init a b", []string{"/bin/bash"}, "stack --base main init a b", "init"},
		{"gh stack -m wip add x", []string{"/bin/bash"}, "stack -m wip add x", "add"},
		{"gh stack --base -- init a b", []string{"/bin/bash"}, "stack --base -- init a b", "init"},
		{"gh stack --a 1 --b 2 init", []string{"/bin/bash"}, "stack --a 1 --b 2 init", "init"},
		{"gh stack --base \"$b\" init a b", []string{"/bin/bash"}, "init a b", "init"},
		{"gh stack --base main --remote origin init a b", []string{"/bin/bash"}, "stack --base main --remote origin init a b", "init"},
		{"gh extension exec stack --base main init a b", []string{"/bin/bash"}, "exec stack --base main init a b", "init"},
		{"gh-stack -m wip add x", []string{"/bin/bash"}, "gh-stack -m wip add x", "add"},
		// A dash word before gh's own command: gh passes `stack` through past
		// `--help=false` and the extension RAN rebase (measured). gh refuses
		// `--x`, `-R x` and `--` before `stack` itself; the gate skips every
		// leading dash word and over-refuses those rather than read gh's
		// unpinned root flags.
		{"gh --help=false stack rebase", []string{"/bin/bash"}, "gh --help=false stack rebase", "rebase"},
		{"gh --x stack rebase", []string{"/bin/bash"}, "gh --x stack rebase", "rebase"},
		// An empty word is dropped by cobra at a command position: `gh ''
		// stack --version` dispatched and `gh-stack '' rebase` RAN rebase
		// (measured). The stub records the empty argument as two spaces.
		{"gh '' stack rebase", []string{"/bin/bash", "/bin/zsh"}, "gh  stack rebase", "rebase"},
		{"gh stack '' rebase", []string{"/bin/bash"}, "stack  rebase", "rebase"},
		{"gh stack --base '' init a b", []string{"/bin/bash"}, "stack --base  init a b", "init"},
		{"gh extension '' exec stack rebase", []string{"/bin/bash"}, "extension  exec stack rebase", "rebase"},
		{"gh-stack '' init a b", []string{"/bin/bash"}, "gh-stack  init a b", "init"},
		// The bare operand `gh` of an unknown program is a command position
		// (the recipe's `sandbox-exec … gh stack init`), so a program handed
		// files literally named gh, stack and rebase is refused: an accepted
		// over-refusal (the Codex code pass), pinned so a change is deliberate.
		{"sed -n '1p' gh stack rebase", nil, "", "rebase"},
		// Documented over-refusals (the operator's Q4): help after a flag or
		// before an operand, `--` before the subcommand, and a gated word in
		// a value position. The extension prints help or fails on the flag
		// for these and runs nothing (measured); the gate does not model
		// pflag and refuses the shape.
		{"gh stack rebase --no-trunk --help", []string{"/bin/bash"}, "stack rebase --no-trunk --help", "rebase"},
		{"gh stack rebase -h x", []string{"/bin/bash"}, "stack rebase -h x", "rebase"},
		{"gh stack -- rebase", []string{"/bin/bash"}, "stack -- rebase", "rebase"},
		{"gh stack --base rebase", []string{"/bin/bash"}, "stack --base rebase", "rebase"},
		{"gh stack -h init a b", []string{"/bin/bash"}, "stack -h init a b", "init"},
	}
	control := func(t *testing.T, c struct {
		cmd    string
		shells []string
		want   string
		sub    string
	}) {
		for _, sh := range c.shells {
			if why, ok := darwinOnly[c.cmd]; ok && runtime.GOOS != "darwin" {
				t.Logf("kernel control not run on %s: %s", runtime.GOOS, why)
				break
			}
			if !shellOnBox(t, sh) {
				continue
			}
			if got := ghKernelRuns(t, sh, bin, work, c.cmd); !strings.Contains(strings.ToLower(got), strings.ToLower(c.want)) {
				t.Fatalf("kernel control: %s did not hand the stub %q for this form; recorded %q", sh, c.want, got)
			}
		}
	}
	// Nobody holds the slot: refused, naming the slot and the subcommand.
	for _, c := range cases {
		t.Run("nobody/"+c.cmd, func(t *testing.T) {
			control(t, c)
			for _, cwd := range []string{f.repo, f.wtB} {
				wantGateBoth(t, f, cwd, "sess-b", c.cmd, true, "Nobody holds .buddy/slot/gh-stack")
				wantGateBoth(t, f, cwd, "sess-b", c.cmd, true, "`gh stack "+c.sub+"`")
			}
		})
	}
	claimGHSlot(t, f, "sess-a", "restacking b on a")
	for _, c := range cases {
		t.Run("held/"+c.cmd, func(t *testing.T) {
			// The holder is admitted from either worktree (one ledger per
			// checkout); the other session is refused, told who holds it.
			for _, cwd := range []string{f.repo, f.wtB} {
				wantGateBoth(t, f, cwd, "sess-a", c.cmd, false, "")
				wantGateBoth(t, f, cwd, "sess-b", c.cmd, true, `held by session alpha (slug "gh-stack": restacking b on a; live, last seen`)
				wantGateBoth(t, f, cwd, "sess-b", c.cmd, true, "ask the holder: buddy msg gh-stack")
			}
		})
	}
}

// Ordinary gh use and every command that merely MENTIONS the phrase keep
// passing, with the guard armed in the same fixture (the control is the
// refusal of `gh stack rebase` at the end).
func TestGateGHStackFalseDenialControls(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	for _, cmd := range []string{
		"gh stack view", "gh stack view --json | jq .", "gh stack view --short",
		"gh stack up", "gh stack down 2", "gh stack top", "gh stack bottom", "gh stack trunk", "gh stack switch",
		"gh stack bottom --print-path", "gh stack feedback",
		"gh stack", "gh stack --help", "gh stack -h", "gh stack -v", "gh stack --version",
		"gh stack --base main view", "gh stack --base=main view", "gh stack --base view", "gh stack --x --y",
		"gh --help=false stack view", "gh --help=false pr list", "gh '' stack view", "gh stack '' view", "gh stack ''",
		// A dynamic word among the leading flags ends the route (recorded
		// OPEN, the Codex code pass): the subcommand after it is not read.
		"gh stack --base=\"$b\" init a b", "gh stack \"$flag\" init a b",
		"gh stack rebase --help", "gh stack rebase -h", "gh stack help rebase", "gh help stack",
		"gh stack completion zsh", "gh stack bogus",
		"gh pr comment 1 -b \"ran gh stack rebase\"", "gh api repos/{owner}/{repo}/pulls",
		"gh api graphql -f query='{ viewer { login } }'", "gh api repos/x/y --jq .",
		"gh pr list --json number,title", "gh pr list --label x{1..300}", "gh run list", "gh run watch", "gh co 12",
		"gh extension list", "gh ext exec other rebase", "gh extension exec stack view",
		"gh stack view --json x{1..300}",
		"echo \"gh stack rebase\"", "printf 'gh stack rebase\\n' > notes",
		"cat <<'EOF'\ngh stack rebase --no-trunk b\nEOF\n",
		"buddy msg bravo \"gh stack rebase next\"", "git commit -m \"gh stack rebase recipe\"",
		"git log --grep='gh stack rebase'", "grep -r \"gh stack rebase\" docs",
		"herdr agent start p -- claude \"orchestrator runs gh stack rebase later\"",
		"claude -p \"the orchestrator will run gh stack rebase\"",
		// A quoted operand of an UNKNOWN program is speculative text, which
		// the gh route never reads (panel false-denial pass): sed and awk
		// patterns whose first word's basename is gh, and prompts that begin
		// with, or carry after a separator or in backticks, the invocation.
		"sed -i '' 's/gh stack rebase /gh stack rebase --no-trunk /' skills/buddy/SKILL.md",
		"perl -pi -e 's/gh stack push /gh stack push --x /' f",
		"rg -l 'gh stack' docs | xargs sed -i '' 's/gh stack sync --x/gh stack sync/'",
		"awk '/gh stack rebase /{n++} END{print n}' log",
		"sed -n '/gh stack rebase /,$p' docs/USAGE.md",
		"claude -p \"gh stack rebase next\"",
		"claude -p 'Review the diff, then run `gh stack rebase --no-trunk`.'",
		"claude -p \"take the slot; gh stack rebase --upstack; report\"",
		"herdr agent start p -- claude \"read brief.md && gh stack sync when READY\"",
		// The same boundary leaves these OPEN (recorded in D-108): a quoted
		// command line an unknown program runs.
		"watch 'gh stack rebase'", "flock -c 'gh stack rebase' /tmp/l", "ssh host 'gh stack rebase'",
		"gs rebase", // the alias gh stack alias installs: recorded OPEN, so a fold that models it is deliberate
		"ghq get x", "gh-stacker rebase", "github stack rebase",
		// Dynamic words at the command, stack and subcommand positions stay
		// OPEN under EVERY harness (no Codex-only uncertainty, D-108).
		"gh stack \"$op\"", "$GH stack rebase", "gh \"$c\" rebase", "gh stack ${X:-rebase}",
		"gh stack rebase$suffix",
	} {
		t.Run(cmd, func(t *testing.T) {
			for _, cwd := range []string{f.repo, f.wtB} {
				wantGateBoth(t, f, cwd, "sess-b", cmd, false, "")
			}
		})
	}
	// The guard was armed the whole time: the same words at a CERTAIN
	// command position refuse.
	wantGateBoth(t, f, f.repo, "sess-b", "gh stack rebase", true, "Nobody holds")
	wantGateBoth(t, f, f.repo, "sess-b", "bash -c 'gh stack rebase'", true, "Nobody holds")
	wantGateBoth(t, f, f.repo, "sess-b", "wrap gh stack rebase", true, "Nobody holds")
}

// Containment admits (invariant 14): a claim on the slot, on `.buddy/slot` or
// on `.buddy` covers it; a sibling, a longer name or a claim UNDER the slot
// does not.
func TestGateGHStackSlotByContainment(t *testing.T) {
	for _, c := range []struct {
		scope  string
		admits bool
	}{
		{".buddy/slot/gh-stack", true},
		{".buddy/slot", true},
		{".buddy", true},
		{".buddy/slot/gh-stack-2", false},
		{".buddy/slots", false},
		{".buddy/slot/gh-stack/child", false},
	} {
		t.Run(c.scope, func(t *testing.T) {
			f := newFixture(t)
			f.initAndHello(t)
			mustClaim(t, f, f.repo, "s", "sess-a", c.scope)
			wantGateBoth(t, f, f.repo, "sess-a", "gh stack rebase", !c.admits, "")
			if !c.admits {
				// Not held by the caller, and nobody COVERS it either.
				wantGateBoth(t, f, f.repo, "sess-a", "gh stack rebase", true, "Nobody holds")
			}
			// Another session is refused in every case (the positive control
			// that a claim was in the ledger: a covering one is named).
			want := "Nobody holds"
			if c.admits {
				want = `held by session alpha (slug "s"`
			}
			wantGateBoth(t, f, f.repo, "sess-b", "gh stack rebase", true, want)
		})
	}
}

// The holder's state is worded the way the reader must act (D-090): ENDED is
// displaced by the caller's own claim (D-026), SILENT and GONE refuse like a
// fresh claim (D-022) and point at the operator.
func TestGateGHStackHolderStates(t *testing.T) {
	t.Run("ended", func(t *testing.T) {
		f := newFixture(t)
		f.initAndHello(t)
		claimGHSlot(t, f, "sess-a", "restack")
		if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "bye"); code != 0 {
			t.Fatalf("bye: %s", errw)
		}
		wantGateBoth(t, f, f.repo, "sess-b", "gh stack rebase", true, "session ENDED — nobody will answer; your own buddy claim displaces it")
		// D-026: the refused session's own claim displaces the ended holder.
		claimGHSlot(t, f, "sess-b", "restack, taken over")
		wantGateBoth(t, f, f.repo, "sess-b", "gh stack rebase", false, "")
	})
	t.Run("silent", func(t *testing.T) {
		f := newFixture(t)
		f.initAndHello(t)
		claimGHSlot(t, f, "sess-a", "restack")
		f.clock = f.clock.Add(store.StaleAfter + time.Minute)
		wantGateBoth(t, f, f.repo, "sess-b", "gh stack rebase", true, "live but SILENT for")
		wantGateBoth(t, f, f.repo, "sess-b", "gh stack rebase", true, "ask the operator: buddy release gh-stack --session sess-a")
		wantGateBoth(t, f, f.repo, "sess-a", "gh stack rebase", false, "") // a stale holder is still the holder
	})
	t.Run("silent-id-quoted", func(t *testing.T) {
		// The holder's id is pasted back as a shell word: a space in it is
		// quoted, a newline in it is named instead of printed as a command.
		f := newFixture(t)
		f.initAndHello(t)
		if _, errw, code := f.run(t, f.repo, hookJSON("sess c", f.repo, "", ""), "hello", "--label", "charlie"); code != 0 {
			t.Fatalf("hello: %s", errw)
		}
		claimGHSlot(t, f, "sess c", "restack")
		f.clock = f.clock.Add(store.StaleAfter + time.Minute)
		wantGateBoth(t, f, f.repo, "sess-b", "gh stack rebase", true, "ask the operator: buddy release gh-stack --session 'sess c'")
		g := newFixtureNamed(t, "other")
		g.initAndHello(t)
		if _, errw, code := g.run(t, g.repo, hookJSON("sess\nd", g.repo, "", ""), "hello", "--label", "delta"); code != 0 {
			t.Fatalf("hello: %s", errw)
		}
		claimGHSlot(t, g, "sess\nd", "restack")
		g.clock = g.clock.Add(store.StaleAfter + time.Minute)
		wantGateBoth(t, g, g.repo, "sess-b", "gh stack rebase", true, "buddy release <the slug> --session <the holder's id> (neither pastes back as printed)")
	})
	t.Run("slug-resolves-elsewhere", func(t *testing.T) {
		// A slot slugged `all` must not be the name the reader messages: that
		// broadcasts (D-090's msgTargetFor); the holder's id is used instead.
		f := newFixture(t)
		f.initAndHello(t)
		mustClaim(t, f, f.repo, "all", "sess-a", ".buddy/slot/gh-stack")
		out, _, _ := f.run(t, f.repo, bashHookJSON("sess-b", f.repo, "gh stack rebase"), "gate")
		reason, denied := decodeDeny(t, out)
		if !denied || !strings.Contains(reason, "ask the holder: buddy msg sess-a") || strings.Contains(reason, "buddy msg all") {
			t.Fatalf("the message target must resolve to the holder: %q", reason)
		}
	})
	t.Run("gone", func(t *testing.T) {
		f := newFixture(t)
		f.initAndHello(t)
		f.asProcess(100, 1, func() {
			if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello", "--label", "alpha"); code != 0 {
				t.Fatalf("hello: %s", errw)
			}
		})
		claimGHSlot(t, f, "sess-a", "restack")
		f.kill(100)
		wantGateBoth(t, f, f.repo, "sess-b", "gh stack rebase", true, "live but its harness process is GONE")
	})
}

// Jurisdiction: no ledger anywhere is feature-off; a literal directory change
// into a governed repo is adjudicated against THAT repo's ledger; a slot held
// in one checkout admits nothing in another (the design pass's P1); an
// unreadable target ledger denies; an ambiguous identity is refused where a
// gh stack route reaches a governed repo (P1, the second).
func TestGateGHStackJurisdiction(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	g := newFixtureNamed(t, "other")
	g.initAndHello(t)
	plain := t.TempDir()
	for _, cmd := range []string{"gh stack rebase", "cd sub && gh stack rebase"} {
		wantGateBoth(t, f, plain, "sess-a", cmd, false, "")
	}
	for _, cmd := range []string{
		"cd " + shellArg(f.repo) + " && gh stack rebase",
		"env -C " + shellArg(f.repo) + " gh stack rebase",
		"(cd " + shellArg(f.wtB) + " && gh stack rebase)",
	} {
		t.Run(cmd, func(t *testing.T) {
			wantGateBoth(t, f, plain, "sess-a", cmd, true, "Nobody holds .buddy/slot/gh-stack — in the TARGET repo's buddy ledger")
			wantGateBoth(t, f, plain, "sess-a", cmd, true, "after buddy hello --session sess-a there")
		})
	}
	claimGHSlot(t, f, "sess-a", "restack in f")
	// From an ungoverned cwd the holder is admitted into its own checkout, the
	// other session is not.
	cdF := "cd " + shellArg(f.repo) + " && gh stack rebase"
	wantGateBoth(t, f, plain, "sess-a", cdF, false, "")
	wantGateBoth(t, f, plain, "sess-b", cdF, true, "held by session alpha")
	// A slot held in f admits nothing in g, from f's own cwd or from none.
	cdG := "cd " + shellArg(g.repo) + " && gh stack rebase"
	wantGateBoth(t, f, f.repo, "sess-a", cdG, true, "TARGET repo's buddy ledger")
	wantGateBoth(t, f, plain, "sess-a", cdF+" && "+cdG, true, "TARGET repo's buddy ledger")
	// The association is by command: a directory change AFTER the call still
	// counts (over-approximation, recorded in D-108).
	wantGateBoth(t, f, f.repo, "sess-a", "gh stack rebase; cd "+shellArg(g.repo), true, "TARGET repo's buddy ledger")
	// Holding it in both admits both.
	claimGHSlot(t, g, "sess-a", "restack in g")
	wantGateBoth(t, f, plain, "sess-a", cdF+" && "+cdG, false, "")
	wantGateBoth(t, f, f.repo, "sess-a", cdG, false, "")
	// An unreadable target ledger denies (invariant 3), even for the holder.
	ledger := filepath.Join(g.repo, ".git", "buddy.db")
	if err := os.Chmod(ledger, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(ledger, 0o644) })
	wantGateBoth(t, f, f.repo, "sess-a", cdG, true, "ledger is unavailable")
	if err := os.Chmod(ledger, 0o644); err != nil {
		t.Fatal(err)
	}
	// An ambiguous identity: the harness's own variable disagrees with the
	// payload. Refused where the route reaches a governed repo, silent where
	// it does not.
	f.env[EnvHarness], f.env[EnvClaudeSession] = "claude", "sess-x"
	t.Cleanup(func() { delete(f.env, EnvHarness); delete(f.env, EnvClaudeSession) })
	wantGate(t, f, plain, bashHookJSON("sess-b", plain, cdF), true, "session identity is ambiguous")
	wantGate(t, f, plain, bashHookJSON("sess-b", plain, "gh stack rebase"), false, "")
	wantGate(t, f, plain, bashHookJSON("sess-b", plain, "cd sub && gh stack rebase"), false, "")
}

// A ledger whose repository has no worktree root (bare) refuses rather than
// placing the slot against nothing, as the paths gate does.
func TestGateGHStackBareRepoRefuses(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	bare := filepath.Join(t.TempDir(), "bare.git")
	c := exec.Command("git", "init", "-q", "--bare", bare)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v %s", err, out)
	}
	db, err := os.ReadFile(filepath.Join(f.repo, ".git", "buddy.db"))
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(bare, "buddy.db"), string(db))
	wantGate(t, f, bare, bashHookJSON("sess-a", bare, "gh stack rebase"), true, "could not resolve the worktree root")
	// Reached by a directory change from an ungoverned cwd, the same ledger
	// refuses the same way; from a governed cwd the home ledger is asked
	// first and its refusal wins (the order pinned here).
	cdBare := "cd " + shellArg(bare) + " && gh stack rebase"
	plain := t.TempDir()
	wantGate(t, f, plain, bashHookJSON("sess-a", plain, cdBare), true, "could not resolve the worktree root")
	wantGate(t, f, f.repo, bashHookJSON("sess-a", f.repo, cdBare), true, "Nobody holds")
	// Control: the same ledger copied beside a worktree admits the holder.
	claimGHSlot(t, f, "sess-a", "restack")
	wantGate(t, f, f.repo, bashHookJSON("sess-a", f.repo, "gh stack rebase"), false, "")
	wantGate(t, f, f.repo, bashHookJSON("sess-a", f.repo, cdBare), true, "could not resolve the worktree root")
}

// Precedence: the brake, then a patch literal or Codex's uncertainty, then the
// slot. Exhaustion at a position the gh route reads refuses everywhere a
// ledger governs; an over-budget word elsewhere in gh's argv is data.
func TestGateGHStackPrecedenceAndBudget(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	plain := t.TempDir()
	claimGHSlot(t, f, "sess-a", "restack")
	// A patch literal in the same command wins, slot or no slot.
	wantGate(t, f, f.repo, bashHookJSON("sess-a", f.repo, "gh stack rebase && apply_patch < p"), true, "runs apply_patch")
	wantGate(t, f, f.repo, codexBashHookJSON("sess-a", f.repo, "gh stack rebase && apply_patch < p"), true, "apply_patch")
	// The brake comes first.
	if _, errw, code := f.run(t, f.repo, "", "pause", "alpha", "--note", "hold the restack"); code != 0 {
		t.Fatal(errw)
	}
	wantGateBoth(t, f, f.repo, "sess-a", "gh stack rebase", true, "hold the restack")
	if _, errw, code := f.run(t, f.repo, "", "resume", "alpha"); code != 0 {
		t.Fatal(errw)
	}
	wantGateBoth(t, f, f.repo, "sess-a", "gh stack rebase", false, "")
	// The other repo's brake applies to a call reaching it (F-28).
	g := newFixtureNamed(t, "other")
	g.initAndHello(t)
	claimGHSlot(t, g, "sess-a", "restack in g")
	if _, errw, code := g.run(t, g.repo, "", "pause", "alpha", "--note", "paused in g"); code != 0 {
		t.Fatal(errw)
	}
	wantGateBoth(t, f, f.repo, "sess-a", "cd "+shellArg(g.repo)+" && gh stack rebase", true, "paused in g")
	// Codex's uncertainty arm refuses before the gh word is read; a Claude
	// call keeps reading (D-087) and reaches the slot arm.
	wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, `eval "$x"; gh stack rebase`), true, "cannot rule out")
	wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, `eval "$x"; gh stack rebase`), true, "held by session alpha")
	// Script text a pipeline produces is D-087's class, unchanged by this arm:
	// Codex refuses it as uncertain, a Claude call never reads it (recorded
	// OPEN in D-108, with `bash -c "$(...)"` and a script file).
	wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, "echo 'gh stack rebase' | bash"), true, "cannot rule out")
	wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, "echo 'gh stack rebase' | bash"), false, "")
	// Budget: exhausted at a position the route reads, under every harness,
	// only where a ledger governs.
	for _, cmd := range []string{
		"gh stack {rebase,x{1..300}}",
		"gh {stack,x{1..300}} rebase",
		"gh extension {exec,x{1..300}} stack rebase",
		"gh extension exec {stack,x{1..300}} rebase",
		"gh stack {--help,x{1..300}} rebase",
		"gh stack --base {main,x{1..300}} init",
		"gh {--help=false,x{1..300}} stack rebase",
	} {
		t.Run(cmd, func(t *testing.T) {
			wantGateBoth(t, f, f.repo, "sess-a", cmd, true, "budget")
			wantGateBoth(t, f, plain, "sess-a", cmd, false, "")
		})
	}
	// An over-budget word AFTER the subcommand is data: the verdict is the
	// slot's, not the budget's.
	wantGateBoth(t, f, f.repo, "sess-b", "gh stack rebase --remote x{1..300}", true, "held by session alpha")
	wantGateBoth(t, f, f.repo, "sess-a", "gh stack rebase --remote x{1..300}", false, "")
}

// A subagent's hook fires under the parent's session_id (D-099): the holder's
// subagents are admitted with it, a lane's are refused like the lane. The deny
// is one fenced line (invariant 9).
func TestGateGHStackSubagentAndFence(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	claimGHSlot(t, f, "sess-a", "line one\nline two")
	sub := func(session string) string {
		b, _ := json.Marshal(map[string]any{
			"session_id": session, "agent_id": "agent-1", "cwd": f.repo, "tool_name": "Bash",
			"tool_input": map[string]any{"command": "gh stack rebase"},
		})
		return string(b)
	}
	wantGate(t, f, f.repo, sub("sess-a"), false, "")
	wantGate(t, f, f.repo, sub("sess-b"), true, "held by session alpha")
	out, _, _ := f.run(t, f.repo, bashHookJSON("sess-b", f.repo, "gh stack rebase"), "gate")
	reason, denied := decodeDeny(t, out)
	if !denied || strings.Contains(reason, "\n") || !strings.Contains(reason, "line one⏎line two") {
		t.Fatalf("the deny must be one line with the desc fenced: %q", reason)
	}
}
