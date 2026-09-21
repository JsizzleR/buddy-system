package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Issue #23. `buddy sweep --help` performed a real sweep (47 rows on the
// operator's ledger), because sweep read args[0] == "--force" and ignored
// everything else; `sweep --dry-run` was equally unrecognised, ran a SECOND
// real sweep, and printed the plausible zero of a population the first had
// consumed; and `sweep --verbose --force` ran unforced because --force had to
// be first. The issue's own note on testing this: it needs a SEEDED
// population — on an empty ledger every variant prints "0 orphaned, 0
// deleted" and all of them look correct.
//
// The population: `gone` released 25h ago (deleted by any sweep); `left`
// whose owner said bye (orphaned by any sweep); `held` whose owner is live
// but silent 25h (orphaned under --force only).
func seedSweepPopulation(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-c", f.repo, "", ""), "hello", "--label", "charlie"); code != 0 {
		t.Fatalf("hello c: %s", errw)
	}
	for _, args := range [][]string{
		{"claim", "gone", "--session", "sess-a", "--desc", "d", "--scope", "a"},
		{"release", "gone", "--session", "sess-a"},
		{"claim", "held", "--session", "sess-b", "--desc", "d", "--scope", "b"},
		{"claim", "left", "--session", "sess-c", "--desc", "d", "--scope", "c"},
		{"bye", "sess-c"},
	} {
		if _, errw, code := f.run(t, f.repo, "", args...); code != 0 {
			t.Fatalf("%v: %s", args, errw)
		}
	}
	f.clock = f.clock.Add(25 * time.Hour)
	return f
}

func TestSweepHelpAndUnknownArgumentsWriteNothing(t *testing.T) {
	boundedParallel(t)
	f := seedSweepPopulation(t)
	forecast := func(t *testing.T, args ...string) string {
		t.Helper()
		out, errw, code := f.run(t, f.repo, "", args...)
		if code != 0 {
			t.Fatalf("%v: rc=%d %s", args, code, errw)
		}
		return out
	}
	// The population as a dry run sees it, before anything below has run.
	const plain = "sweep --dry-run: would orphan 1, would delete 1"
	if out := forecast(t, "sweep", "--dry-run"); !strings.Contains(out, plain) || !strings.Contains(out, "would orphan left held by charlie (sess-c)") {
		t.Fatalf("the seeded forecast is not what the population implies:\n%s", out)
	}

	cases := []struct {
		name     string
		args     []string
		wantCode int
		stdout   string // required substring when wantCode == 0
		stderr   string // required substring when wantCode != 0
	}{
		{"--help prints usage and exits 0", []string{"sweep", "--help"}, 0, "usage: buddy sweep", ""},
		{"-h too", []string{"sweep", "-h"}, 0, "usage: buddy sweep", ""},
		{"--help after a flag", []string{"sweep", "--force", "--help"}, 0, "usage: buddy sweep", ""},
		{"an unknown flag is refused", []string{"sweep", "--bogus"}, 1, "", "bogus"},
		{"a stray word is refused", []string{"sweep", "extra"}, 1, "", "extra"},
		{"--force behind an unknown flag is refused, not run unforced", []string{"sweep", "--verbose", "--force"}, 1, "", "verbose"},
		{"a dry run forecasts", []string{"sweep", "--dry-run"}, 0, plain, ""},
		{"a forced dry run forecasts the force", []string{"sweep", "--dry-run", "--force"}, 0, "sweep --dry-run: would orphan 2, would delete 1", ""},
		{"flag order does not matter", []string{"sweep", "--force", "--dry-run"}, 0, "would orphan held held by bravo (sess-b)", ""},
	}
	for _, tc := range cases {
		out, errw, code := f.run(t, f.repo, "", tc.args...)
		if code != tc.wantCode {
			t.Errorf("%s: %v rc=%d, want %d\nstdout: %s\nstderr: %s", tc.name, tc.args, code, tc.wantCode, out, errw)
			continue
		}
		if tc.wantCode == 0 && (!strings.Contains(out, tc.stdout) || errw != "") {
			t.Errorf("%s: %v\nstdout: %q\nstderr: %q", tc.name, tc.args, out, errw)
		}
		if tc.wantCode != 0 && (!strings.Contains(errw, tc.stderr) || !strings.Contains(errw, "usage: buddy sweep") || out != "") {
			t.Errorf("%s: %v must refuse naming the argument and the usage, on stderr only\nstdout: %q\nstderr: %q", tc.name, tc.args, out, errw)
		}
	}
	// None of the above may have consumed the population: the forecast is
	// unchanged. This is the assertion that fails against the old code for
	// EVERY row above except the refusals — --help swept, --dry-run swept,
	// and --verbose --force swept.
	if out := forecast(t, "sweep", "--dry-run"); !strings.Contains(out, plain) {
		t.Fatalf("something above wrote to the ledger; the forecast moved:\n%s", out)
	}
	// Positive control: the real sweep does what every forecast said, names
	// what it orphaned, and leaves a forecast of nothing behind it.
	out := forecast(t, "sweep", "--force")
	for _, want := range []string{"sweep: 2 orphaned, 1 deleted", "orphaned held held by bravo (sess-b)", "orphaned left held by charlie (sess-c)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("real sweep lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "--force orphans") {
		t.Fatalf("a forced sweep must not advertise --force:\n%s", out)
	}
	if out := forecast(t, "sweep", "--dry-run", "--force"); !strings.Contains(out, "would orphan 0, would delete 0") {
		t.Fatalf("after the real sweep the forecast must be empty:\n%s", out)
	}
}

// Every verb answers --help with its own usage line, exits 0, and does
// nothing else — without a ledger, without reading stdin, and without
// creating anything. `init --help` used to create the ledger, which turns
// the feature ON for a repo whose operator was asking what init does.
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	ledger := filepath.Join(f.repo, ".git", "buddy.db")
	if len(verbs) < 20 {
		t.Fatalf("the command table has %d verbs; did dispatch move out of it?", len(verbs))
	}
	for name, v := range verbs {
		if !strings.HasPrefix(v.usage, "usage: buddy "+name) {
			t.Errorf("verb %q: usage line must begin %q, got %q", name, "usage: buddy "+name, v.usage)
		}
		for _, h := range []string{"--help", "-h", "-help"} {
			out, errw, code := f.run(t, f.repo, "", name, h)
			if code != 0 || out != v.usage+"\n" || errw != "" {
				t.Errorf("%s %s: rc=%d\nstdout: %q\nstderr: %q", name, h, code, out, errw)
			}
		}
	}
	if _, err := os.Stat(ledger); !os.IsNotExist(err) {
		t.Fatalf("some verb's --help created or touched the ledger (stat: %v)", err)
	}
	// Positive control: the path above is where init puts the ledger.
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	if _, err := os.Stat(ledger); err != nil {
		t.Fatalf("init did not create the ledger where the test looks: %v", err)
	}
	// And the top-level help is still there.
	if out, _, code := f.run(t, f.repo, "", "--help"); code != 0 || !strings.Contains(out, "every verb answers --help") {
		t.Fatalf("buddy --help: rc=%d %q", code, out)
	}
}

// An argument a verb does not understand is refused before anything is
// written — the same family as #14 (a usage error that silently did nothing)
// and #23 (one that silently did something). Each refusal has a positive
// control in the same table: the well-formed spelling works.
func TestUnknownArgumentsAreRefusedNotIgnored(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "claim", "w", "--session", "sess-a", "--desc", "d", "--scope", "pkg"); code != 0 {
		t.Fatal(errw)
	}
	cases := []struct {
		args     []string
		wantCode int
		stderr   string
	}{
		{[]string{"ls", "--all"}, 0, ""},
		{[]string{"ls", "--bogus"}, 1, "bogus"},
		{[]string{"ls", "--al"}, 1, "al"},
		{[]string{"ls", "extra"}, 1, "extra"},
		{[]string{"init", "extra"}, 1, "extra"},
		{[]string{"inbox", "--session", "sess-a"}, 0, ""},
		{[]string{"inbox", "extra", "--session", "sess-a"}, 1, "extra"},
		{[]string{"claim", "y", "--session", "sess-a", "--desc", "d", "--scope", "p", "extra"}, 1, "extra"},
		{[]string{"release", "w", "--session", "sess-a", "extra"}, 1, "extra"},
		{[]string{"pause", "bravo", "--note", "n", "extra"}, 1, "extra"},
		{[]string{"msg", "bravo", "--bogus", "hi"}, 1, "bogus"},
		{[]string{"resume", "--bogus"}, 1, "usage: buddy resume"},
	}
	for _, tc := range cases {
		out, errw, code := f.run(t, f.repo, "", tc.args...)
		if code != tc.wantCode {
			t.Errorf("%v: rc=%d want %d\nstdout: %s\nstderr: %s", tc.args, code, tc.wantCode, out, errw)
		}
		if tc.wantCode != 0 && !strings.Contains(errw, tc.stderr) {
			t.Errorf("%v: refusal must name the argument, got %q", tc.args, errw)
		}
	}
	// The refused writes did not happen: y was not claimed, w was not
	// released, bravo was not paused.
	out, _, _ := f.run(t, f.repo, "", "ls")
	if strings.Contains(out, "y ") || !strings.Contains(out, "w ") {
		t.Fatalf("a refused claim or release still wrote:\n%s", out)
	}
	if out, _, _ := f.run(t, f.repo, "", "sessions"); strings.Contains(out, "PAUSED") {
		t.Fatalf("a refused pause still wrote:\n%s", out)
	}
}
