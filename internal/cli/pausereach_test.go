package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #81: `buddy pause <session>` did not stop that session's Bash when the
// call's cwd had no ledger, even when the command itself changed into a
// governed repository and wrote there. The brake was read only from the home
// ledger, so a session started in a parent directory of its repository was
// never braked for Bash, which is how most of its writes happen. Reproduced by
// the reviewer at main 9c509ec and here at 7b17cc2: allowed, 0 bytes.
//
// The fix reads the brake in each ledger a literal `cd` / `git -C` directory
// reaches (the directories shellPatchDirs already computes, D-073). A
// directory reached through a variable or a subshell the parser cannot place
// stays open, and is recorded rather than recognized.

// pausedBravo is the fixture with bravo (sess-b) paused in f.repo, and a plain
// directory outside every repository.
func pausedBravo(t *testing.T) (*fixture, string) {
	t.Helper()
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "pause", "bravo", "--note", "hold the line"); code != 0 {
		t.Fatal(errw)
	}
	return f, t.TempDir()
}

func gateDenied(t *testing.T, f *fixture, cwd, input string) (string, bool) {
	t.Helper()
	out, _, _ := f.run(t, cwd, input, "gate")
	return decodeDeny(t, out)
}

func TestPauseStopsABashThatCdsIntoTheRepo(t *testing.T) {
	boundedParallel(t)
	f, plain := pausedBravo(t)
	cmd := "cd " + f.repo + " && echo x > dir2/x.go"

	// The positive control: the same command from a cwd inside the repo.
	if reason, denied := gateDenied(t, f, f.repo, bashHookJSON("sess-b", f.repo, cmd)); !denied || !strings.Contains(reason, "hold the line") {
		t.Fatalf("control: the paused session's Bash from the repo must deny: denied=%v %q", denied, reason)
	}
	// The issue's exact input.
	if reason, denied := gateDenied(t, f, plain, bashHookJSON("sess-b", plain, cmd)); !denied || !strings.Contains(reason, "hold the line") {
		t.Fatalf("a paused session's cd into the repo must deny: denied=%v %q", denied, reason)
	}
	// An unpaused session's same command passes: it is the brake, not the cd.
	if reason, denied := gateDenied(t, f, plain, bashHookJSON("sess-a", plain, cmd)); denied {
		t.Fatalf("an unpaused session's cd must pass: %q", reason)
	}
	// So does the paused session's command that reaches no repository.
	if reason, denied := gateDenied(t, f, plain, bashHookJSON("sess-b", plain, "cd "+plain+" && echo x > y")); denied {
		t.Fatalf("a cd that reaches no ledger must pass: %q", reason)
	}
}

func TestPauseStopsGitDashCIntoTheRepo(t *testing.T) {
	boundedParallel(t)
	f, plain := pausedBravo(t)
	if reason, denied := gateDenied(t, f, plain, bashHookJSON("sess-b", plain, "git -C "+f.repo+" commit -qam x")); !denied || !strings.Contains(reason, "hold the line") {
		t.Fatalf("a paused session's git -C into the repo must deny: denied=%v %q", denied, reason)
	}
	if reason, denied := gateDenied(t, f, plain, bashHookJSON("sess-a", plain, "git -C "+f.repo+" commit -qam x")); denied {
		t.Fatalf("control: an unpaused session's git -C must pass: %q", reason)
	}
}

// The label is the one the REACHED ledger knows the session by. A pause by
// label in the reached repo stops the session it names there; a session the
// reached ledger has never seen is stopped only by `pause all`.
func TestPauseReachLooksTheLabelUpInTheReachedLedger(t *testing.T) {
	boundedParallel(t)
	f, plain := pausedBravo(t)
	cmd := "cd " + f.repo + " && make"
	if reason, denied := gateDenied(t, f, plain, bashHookJSON("sess-z", plain, cmd)); denied {
		t.Fatalf("control: a session the reached ledger has never seen is not bravo: %q", reason)
	}
	if _, errw, code := f.run(t, f.repo, "", "pause", "all", "--note", "everyone stop"); code != 0 {
		t.Fatal(errw)
	}
	if reason, denied := gateDenied(t, f, plain, bashHookJSON("sess-z", plain, cmd)); !denied || !strings.Contains(reason, "everyone stop") {
		t.Fatalf("pause all in the reached repo must stop an unknown session's cd: denied=%v %q", denied, reason)
	}
}

// From INSIDE one governed repository, a cd into ANOTHER whose brake stops the
// caller: the home ledger's brake is not the only one.
func TestPauseReachAppliesTheOtherRepositorysBrake(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	g := newFixtureNamed(t, "second")
	if _, errw, code := g.run(t, g.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := g.run(t, g.repo, "", "pause", "all", "--note", "second is frozen"); code != 0 {
		t.Fatal(errw)
	}
	cmd := "cd " + g.repo + " && echo x > y"
	if reason, denied := gateDenied(t, f, f.repo, bashHookJSON("sess-a", f.repo, cmd)); !denied || !strings.Contains(reason, "second is frozen") {
		t.Fatalf("a cd from one governed repo into a paused one must deny: denied=%v %q", denied, reason)
	}
	if reason, denied := gateDenied(t, f, f.repo, bashHookJSON("sess-a", f.repo, "echo x > y")); denied {
		t.Fatalf("control: the same session's call that stays home must pass: %q", reason)
	}
	// By label: the label is the one the REACHED ledger knows the session by.
	// sess-a is "alpha" at home and "golf" in g; a pause of golf in g stops
	// it, which a lookup of the home label would miss.
	if _, errw, code := g.run(t, g.repo, "", "resume", "all"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := g.run(t, g.repo, hookJSON("sess-a", g.repo, "", ""), "hello", "--label", "golf"); code != 0 {
		t.Fatal(errw)
	}
	if reason, denied := gateDenied(t, f, f.repo, bashHookJSON("sess-a", f.repo, cmd)); denied {
		t.Fatalf("control: with g resumed the cd must pass: %q", reason)
	}
	if _, errw, code := g.run(t, g.repo, "", "pause", "golf", "--note", "golf only"); code != 0 {
		t.Fatal(errw)
	}
	if reason, denied := gateDenied(t, f, f.repo, bashHookJSON("sess-a", f.repo, cmd)); !denied || !strings.Contains(reason, "golf only") {
		t.Fatalf("a pause by the label g knows must stop the cd into g: denied=%v %q", denied, reason)
	}
}

// Invariant 3 at a reached directory: a ledger that exists and cannot be read
// denies; a repository never `buddy init`ed is feature-off.
func TestPauseReachUnreadableLedgerDeniesAndNoLedgerPasses(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	plain := t.TempDir()
	cmd := "cd " + f.repo + " && echo x > y"
	if reason, denied := gateDenied(t, f, plain, bashHookJSON("sess-b", plain, cmd)); denied {
		t.Fatalf("control: a cd into a repo with no ledger must pass: %q", reason)
	}
	// A dangling ledger symlink: present, unopenable.
	if err := os.Symlink(filepath.Join(plain, "nowhere", "buddy.db"), filepath.Join(f.repo, ".git", "buddy.db")); err != nil {
		t.Fatal(err)
	}
	if reason, denied := gateDenied(t, f, plain, bashHookJSON("sess-b", plain, cmd)); !denied || !strings.Contains(reason, "unavailable") {
		t.Fatalf("a cd into a repo whose ledger cannot be read must deny: denied=%v %q", denied, reason)
	}
}

// An ambiguous identity refuses wherever a ledger governs the call, and a
// literal cd is how a Bash call from a plain cwd reaches one. The early arm
// collected shell directories only for a shell-routed patch or a gh stack
// route, so `cd <governed> && ...` with conflicting native ids passed (Codex
// design pass, P1).
func TestAmbiguousIdentityRefusesABashThatCdsIntoAGovernedRepo(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	plain := t.TempDir()
	f.env = map[string]string{"CLAUDE_CODE_SESSION_ID": "claude-1", "CODEX_THREAD_ID": "codex-parent"}
	wantGate(t, f, plain, bashHookJSON("claude-1", plain, "cd "+plain+" && make"), false, "")
	wantGate(t, f, plain, bashHookJSON("claude-1", plain, "cd "+f.repo+" && make"), true, "ambiguous")
	wantGate(t, f, plain, bashHookJSON("claude-1", plain, "git -C "+f.repo+" status"), true, "ambiguous")
	// Control: a consistent identity's same call passes.
	f.env = map[string]string{"CLAUDE_CODE_SESSION_ID": "claude-1"}
	wantGate(t, f, plain, bashHookJSON("claude-1", plain, "cd "+f.repo+" && make"), false, "")
}

// The brake reads only directories the recognizer places as reached: a
// directory named as DATA (an operand of printf, a quoted here-document body,
// a comment) is not a cd (Codex design pass, Q7 vectors). The paused
// session's real cd in the same fixture is the positive control.
func TestPauseReachIgnoresADirectoryNamedAsData(t *testing.T) {
	boundedParallel(t)
	f, plain := pausedBravo(t)
	for _, cmd := range []string{
		"printf '%s\\n' 'cd " + f.repo + "'",
		"cat <<'EOF'\ncd " + f.repo + "\nEOF\n",
		"# git -C " + f.repo + " status\necho ok",
		"echo cd " + f.repo,
	} {
		if reason, denied := gateDenied(t, f, plain, bashHookJSON("sess-b", plain, cmd)); denied {
			t.Errorf("%q names the repo as data, not a directory change: %q", cmd, reason)
		}
	}
	if reason, denied := gateDenied(t, f, plain, bashHookJSON("sess-b", plain, "cd "+f.repo+" && ls")); !denied || !strings.Contains(reason, "hold the line") {
		t.Fatalf("control: the paused session's real cd must deny with the brake: denied=%v %q", denied, reason)
	}
}

// The brake reads at most maxReachedBrakes ledgers per call, the hook's
// latency bound: a paused repository after three other directories is read,
// after four it is not (recorded open in D-138, a crafted command).
func TestPauseReachIsBoundedAtMaxReachedBrakes(t *testing.T) {
	boundedParallel(t)
	f, plain := pausedBravo(t)
	cds := func(n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			d := filepath.Join(plain, "d"+string(rune('a'+i)))
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
			b.WriteString("cd " + d + "; ")
		}
		return b.String() + "cd " + f.repo + " && echo x > y"
	}
	if reason, denied := gateDenied(t, f, plain, bashHookJSON("sess-b", plain, cds(maxReachedBrakes-1))); !denied || !strings.Contains(reason, "hold the line") {
		t.Fatalf("within the bound the paused repo's brake must deny: denied=%v %q", denied, reason)
	}
	if reason, denied := gateDenied(t, f, plain, bashHookJSON("sess-b", plain, cds(maxReachedBrakes))); denied {
		t.Fatalf("past the bound the rest are not read (the recorded open form): %q", reason)
	}
}

// A cd inside an unknown program's quoted operand is read for the brake:
// su -c, watch and flock -c RUN that text, and the parser cannot tell them
// from logger, which prints it. A fold that skipped such operands (Codex code
// pass) let a paused session's `su me -c 'cd <repo> && ...'` through (Fable
// cross-check); logger's denial is the accepted over-refusal D-138 records.
// The allowed control: the same commands by a session nobody paused.
func TestPauseReachReadsACdInsideAnUnknownProgramsOperand(t *testing.T) {
	boundedParallel(t)
	f, plain := pausedBravo(t)
	for _, cmd := range []string{
		"su me -c 'cd " + f.repo + " && echo x > y'",
		"watch -n5 'cd " + f.repo + " && make'",
		"flock /tmp/l -c 'cd " + f.repo + " && make'",
		"logger 'cd " + f.repo + "'",
		"logger 'cd " + f.repo + "'; cd src",
	} {
		if reason, denied := gateDenied(t, f, plain, bashHookJSON("sess-b", plain, cmd)); !denied || !strings.Contains(reason, "hold the line") {
			t.Fatalf("%s: the paused repo's brake must deny: denied=%v %q", cmd, denied, reason)
		}
		if reason, denied := gateDenied(t, f, plain, bashHookJSON("sess-a", plain, cmd)); denied {
			t.Fatalf("%s: control: a session nobody paused passes: %q", cmd, reason)
		}
	}
}

// Every literal directory change D-138 and USAGE name reaches the brake, not
// only cd and git -C: pushd, env -C, sudo -D. The control is the same command
// aimed at a plain directory.
func TestPauseReachCoversEveryLiteralDirectoryForm(t *testing.T) {
	boundedParallel(t)
	f, plain := pausedBravo(t)
	for _, form := range []string{"pushd %s && echo x > y", "env -C %s touch y", "sudo -D %s touch y"} {
		if reason, denied := gateDenied(t, f, plain, bashHookJSON("sess-b", plain, strings.Replace(form, "%s", f.repo, 1))); !denied || !strings.Contains(reason, "hold the line") {
			t.Fatalf("%s: the paused repo's brake must deny: denied=%v %q", form, denied, reason)
		}
		if reason, denied := gateDenied(t, f, plain, bashHookJSON("sess-b", plain, strings.Replace(form, "%s", plain, 1))); denied {
			t.Fatalf("%s: control: aimed at a plain directory it passes: %q", form, reason)
		}
	}
}

// Mutation run on the final code: the bound test above is written in
// maxReachedBrakes, so it followed the constant down to 1, and its plain
// directories each have their own key, so a bound counting directories
// instead of keys survived. Three literal plain directories are within the
// bound D-138 records (4); five directories of ONE plain repository are one
// key, so the paused repository after them is still read.
func TestPauseReachBoundCountsKeysNotDirectories(t *testing.T) {
	boundedParallel(t)
	f, plain := pausedBravo(t)
	var three strings.Builder
	for _, n := range []string{"pa", "pb", "pc"} {
		d := filepath.Join(plain, n)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		three.WriteString("cd " + d + "; ")
	}
	if reason, denied := gateDenied(t, f, plain, bashHookJSON("sess-b", plain, three.String()+"cd "+f.repo+" && echo x > y")); !denied || !strings.Contains(reason, "hold the line") {
		t.Fatalf("three plain directories then the paused repo is within the bound of 4: denied=%v %q", denied, reason)
	}
	one := filepath.Join(plain, "onerepo")
	if err := os.MkdirAll(one, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, one, "init", "-q")
	var same strings.Builder
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		d := filepath.Join(one, n)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		same.WriteString("cd " + d + "; ")
	}
	if reason, denied := gateDenied(t, f, plain, bashHookJSON("sess-b", plain, same.String()+"cd "+f.repo+" && echo x > y")); !denied || !strings.Contains(reason, "hold the line") {
		t.Fatalf("five directories of one repository are one key: the paused repo after them must be read: denied=%v %q", denied, reason)
	}
}
