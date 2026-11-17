package cli

import (
	"strings"
	"testing"
)

// Four ways a verb did something other than what was typed, each found by the
// 2026-11 audit and reproduced against a binary built from a96d9f4 (issues
// #115, #114, #127, #112). Each refusal below writes nothing, so every case
// reads the ledger back through a verb (ls --all, the inbox, the gate's brake,
// ids ls) rather than trusting the exit code, and each has a positive control
// proving the same fixture does write when the line is well formed: "it
// refused" and "it never ran" look identical otherwise.

// lastLine is what a `2>&1 | tail -1` reader keeps (D-098).
func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// #115: Go's flag package takes the argument after a value flag as its value
// whatever it looks like, so an empty variable (`--scope $S` with S empty, in
// zsh) made the NEXT flag the value: `claim v2 --scope --dry-run` wrote a
// claim on a scope named "--dry-run" where a forecast was asked for, and
// `pause bravo --note --help` braked bravo where a usage line was asked for.
func TestValueFlagMissingItsValueRefusesAndWritesNothing(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "claim", "held", "--session", "sess-a", "--desc", "d", "--scope", "held"); code != 0 {
		t.Fatalf("setup claim: %s", errw)
	}
	// Seeded, so a take that slipped past the flag check would take a block
	// rather than be refused for an unknown space (the audit's ledger had one).
	if _, errw, code := f.run(t, f.repo, "", "ids", "seed", "sp", "100"); code != 0 {
		t.Fatalf("setup seed: %s", errw)
	}

	cases := []struct {
		name string
		args []string
		want string // on the last line, after "refused: "
	}{
		{"claim --scope swallows --dry-run", []string{"claim", "v2", "--session", "sess-a", "--scope", "--dry-run", "--desc", "d"}, "--scope needs a value, and the next argument was the flag --dry-run"},
		{"claim --desc swallows --dry-run", []string{"claim", "v1", "--session", "sess-a", "--desc", "--dry-run", "--scope", "vv"}, "--desc needs a value, and the next argument was the flag --dry-run"},
		{"claim --desc swallows --help", []string{"claim", "v4", "--session", "sess-a", "--desc", "--help", "--scope", "v4"}, "--desc needs a value, and the next argument was the flag --help"},
		{"claim --desc swallows -h", []string{"claim", "v5", "--session", "sess-a", "--desc", "-h", "--scope", "v5"}, "the next argument was the flag -h"},
		{"claim single-dash spelling", []string{"claim", "v6", "--session", "sess-a", "-desc", "-scope=x", "--scope", "v6"}, "-desc needs a value, and the next argument was the flag -scope=x"},
		{"claim --session swallows --desc", []string{"claim", "v7", "--session", "--desc", "d", "--scope", "v7"}, "--session needs a value, and the next argument was the flag --desc"},
		{"claim value is the terminator", []string{"claim", "v8", "--session", "sess-a", "--desc", "--", "--scope", "v8"}, "--desc needs a value, and the next argument was the flag --"},
		// The scan walks past an inline value and a bool flag to the swallow.
		{"claim past an inline value and a bool", []string{"claim", "v9", "--session=sess-a", "--shared", "--scope", "--dry-run", "--desc", "d"}, "--scope needs a value, and the next argument was the flag --dry-run"},
		{"release --note swallows --help", []string{"release", "held", "--session", "sess-a", "--note", "--help", "--outcome", "pass"}, "--note needs a value, and the next argument was the flag --help"},
		{"pause --note swallows --help", []string{"pause", "bravo", "--note", "--help"}, "--note needs a value, and the next argument was the flag --help"},
		{"msg --measured swallows --dry-run", []string{"msg", "bravo", "--measured", "--dry-run", "hi"}, "--measured needs a value, and the next argument was the flag --dry-run"},
		{"msg --from swallows --dry-run", []string{"msg", "bravo", "--from", "--dry-run", "hi"}, "--from needs a value, and the next argument was the flag --dry-run"},
		{"ids take --note swallows --help", []string{"ids", "take", "sp", "2", "--session", "sess-a", "--note", "--help"}, "--note needs a value, and the next argument was the flag --help"},
		{"wait --on swallows --ready", []string{"wait", "--session", "sess-a", "--on", "--ready", "HEAD"}, "--on needs a value, and the next argument was the flag --ready"},
		// The walk stops where Parse stops: an inline flag it does not know
		// is Parse's refusal, not a later swallow's.
		{"inline unknown flag first", []string{"claim", "v11", "--session", "sess-a", "--unknown=x", "--scope", "--dry-run"}, "not defined: -unknown"},
	}
	for _, c := range cases {
		_, errw, code := f.run(t, f.repo, "", c.args...)
		if code == 0 {
			t.Errorf("%s: exit 0, the flag was taken as a value: %s", c.name, errw)
			continue
		}
		if ll := lastLine(errw); !strings.HasPrefix(strings.TrimSpace(ll), "refused: ") || !strings.Contains(ll, c.want) {
			t.Errorf("%s: last line %q, want a refusal containing %q", c.name, ll, c.want)
		}
	}

	// An inline help spelling is still help (exit 0, the usage, nothing
	// written), as Parse answers it before the swallow behind it.
	if out, errw, code := f.run(t, f.repo, "", "claim", "v10", "--session", "sess-a", "--help=true", "--scope", "--dry-run"); code != 0 || !strings.HasPrefix(out, "usage: buddy claim") {
		t.Errorf("--help=true before a swallow: exit %d %q %q, want the usage", code, out, errw)
	}

	// Nothing was written: no claim v1..v8, held still open, bravo unbraked,
	// no message queued, no id block, no wait.
	ls, _, _ := f.run(t, f.repo, "", "ls", "--all")
	for _, slug := range []string{"v1 ", "v2 ", "v4 ", "v5 ", "v6 ", "v7 ", "v8 ", "v9 ", "v10 ", "v11 ", "--dry-run", "--help"} {
		if strings.Contains(ls, slug) {
			t.Errorf("a refused claim wrote a row (%q):\n%s", slug, ls)
		}
	}
	if !strings.Contains(ls, "held") || strings.Contains(ls, "released") {
		t.Errorf("a refused release released \"held\":\n%s", ls)
	}
	if reason, denied := gateDenied(t, f, f.wtB, hookJSON("sess-b", f.wtB, "Edit", f.wtB+"/x.go")); denied {
		t.Errorf("a refused pause braked bravo: %q", reason)
	}
	if in, _, _ := f.run(t, f.wtB, "", "inbox", "--session", "sess-b"); strings.Contains(in, "hi") {
		t.Errorf("a refused msg was queued:\n%s", in)
	}
	if wl, _, _ := f.run(t, f.repo, "", "wait", "ls"); strings.Contains(wl, "alpha") {
		t.Errorf("a refused wait was declared:\n%s", wl)
	}
	// The control take gets the space's FIRST block only if the refused one
	// took none.
	if out, errw, code := f.run(t, f.repo, "", "ids", "take", "sp", "1", "--session", "sess-a", "--note", "n"); code != 0 || !strings.Contains(out, "took sp 101..101") {
		t.Errorf("a refused ids take took a block (control got %q %s)", out, errw)
	}

	// POSITIVE CONTROLS on the same fixture: a value that merely starts with a
	// dash, or names no flag of the verb, is a value; an inline `--desc=--x`
	// is explicit and stays one; and the well-formed lines do write.
	if _, errw, code := f.run(t, f.repo, "", "claim", "ok1", "--session", "sess-a", "--desc", "-1 rows", "--scope", "ok1"); code != 0 {
		t.Errorf("a dash-led value that names no flag was refused: %s", errw)
	}
	if _, errw, code := f.run(t, f.repo, "", "claim", "ok2", "--session", "sess-a", "--desc", "--verbose", "--scope", "ok2"); code != 0 {
		t.Errorf("a value spelling another verb's flag was refused: %s", errw)
	}
	if _, errw, code := f.run(t, f.repo, "", "claim", "ok3", "--session", "sess-a", "--desc=--dry-run", "--scope", "ok3"); code != 0 {
		t.Errorf("an inline --desc=--dry-run was refused: %s", errw)
	}
	// A bool flag takes no value, so the flag after it is a flag.
	if _, errw, code := f.run(t, f.repo, "", "claim", "ok4", "--session", "sess-a", "--shared", "--desc", "d", "--scope", "ok4"); code != 0 {
		t.Errorf("a flag after a bool flag was refused: %s", errw)
	}
	if _, errw, code := f.run(t, f.repo, "", "pause", "bravo", "--note", "hold"); code != 0 {
		t.Fatalf("control pause: %s", errw)
	}
	if _, denied := gateDenied(t, f, f.wtB, hookJSON("sess-b", f.wtB, "Edit", f.wtB+"/x.go")); !denied {
		t.Errorf("control: a well-formed pause must brake bravo")
	}
	ls, _, _ = f.run(t, f.repo, "", "ls")
	for _, slug := range []string{"ok1", "ok2", "ok3", "ok4"} {
		if !strings.Contains(ls, slug) {
			t.Errorf("control claim %s not written:\n%s", slug, ls)
		}
	}
}

// #114: `release <slug> --outcome ""` (an unset `$RESULT`) released with the
// outcome dropped, exit 0, so every rider read a bare release, which the core
// skill says tells them nothing. An empty --note alone did the same.
func TestReleaseRefusesAnEmptyOutcomeOrNote(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "claim", "land1", "--session", "sess-a", "--desc", "x", "--scope", "a"); code != 0 {
		t.Fatalf("setup: %s", errw)
	}
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"empty --outcome", []string{"--outcome", ""}, "--outcome needs pass, fail or aborted"},
		{"blank --outcome", []string{"--outcome", "  "}, "--outcome needs pass, fail or aborted"},
		{"inline empty --outcome", []string{"--outcome="}, "--outcome needs pass, fail or aborted"},
		// The empty outcome is the reason, not "none was given".
		{"empty --outcome with a note", []string{"--outcome", "", "--note", "tested abc123"}, "--outcome needs pass, fail or aborted"},
		{"empty --note alone", []string{"--note", ""}, "--note needs a line"},
		{"empty --note with an outcome", []string{"--outcome", "pass", "--note", ""}, "--note needs a line"},
	}
	for _, c := range cases {
		args := append([]string{"release", "land1", "--session", "sess-a"}, c.args...)
		_, errw, code := f.run(t, f.repo, "", args...)
		if code == 0 {
			t.Errorf("%s: released, exit 0", c.name)
			break // the claim is gone; later cases would read "no such claim"
		}
		if ll := lastLine(errw); !strings.HasPrefix(strings.TrimSpace(ll), "refused: ") || !strings.Contains(ll, c.want) {
			t.Errorf("%s: last line %q, want a refusal containing %q", c.name, ll, c.want)
		}
	}
	if ls, _, _ := f.run(t, f.repo, "", "ls"); !strings.Contains(ls, "land1") {
		t.Fatalf("a refused release released land1:\n%s", ls)
	}
	// POSITIVE CONTROL: the same claim, released with an outcome, records it.
	out, errw, code := f.run(t, f.repo, "", "release", "land1", "--session", "sess-a", "--outcome", "pass", "--note", "tested abc123")
	if code != 0 || !strings.Contains(out, "outcome PASS") {
		t.Fatalf("control release: %s %s", out, errw)
	}
}

// #127: Go's parser stops at the first positional, so `msg bravo hello
// --dry-run` SENT "hello --dry-run", `msg bravo fix --supersedes 1` sent an
// uncorrecting message, and `msg bravo <finding> --measured <what>` sent the
// declaration as body text, unlabelled (17 of 8,125 sends in 21 days). Refused,
// not parsed: a kind is never guessed from the body (D-045), so words in the
// body never become a declaration; `--` before the body sends them as text.
func TestMsgRefusesItsOwnFlagAfterTheBody(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"--dry-run after the body", []string{"hello", "--dry-run"}, "--dry-run after the body would be sent as text"},
		{"--supersedes after the body", []string{"fix", "--supersedes", "1"}, "--supersedes after the body would be sent as text"},
		{"--measured after the body", []string{"finding", "--measured", "rows over 3 runs"}, "--measured after the body would be sent as text"},
		{"--lead after the body", []string{"finding", "--lead"}, "--lead after the body"},
		{"--help after the body", []string{"hi", "--help"}, "--help after the body"},
		{"-h after the body", []string{"hi", "-h"}, "-h after the body"},
		{"inline spelling after the body", []string{"hi", "--relay=orc"}, "--relay=orc after the body"},
		{"single dash after the body", []string{"hi", "-lead"}, "-lead after the body"},
	}
	for _, c := range cases {
		args := append([]string{"msg", "bravo"}, c.args...)
		_, errw, code := f.run(t, f.repo, "", args...)
		if code == 0 {
			t.Errorf("%s: sent, exit 0", c.name)
			continue
		}
		if ll := lastLine(errw); !strings.HasPrefix(strings.TrimSpace(ll), "refused: ") || !strings.Contains(ll, c.want) {
			t.Errorf("%s: last line %q, want a refusal containing %q", c.name, ll, c.want)
		}
	}
	if in, _, _ := f.run(t, f.wtB, "", "inbox", "--session", "sess-b"); strings.Contains(in, "[operator]") {
		t.Fatalf("a refused msg was queued:\n%s", in)
	}

	// POSITIVE CONTROLS: `--` before the body sends the flag words as text; a
	// dash word that is no flag of msg's, and a flag spelled INSIDE one quoted
	// argument, are body text as before; flags before the body still parse.
	sends := [][]string{
		{"msg", "bravo", "--", "hello", "--dry-run"},
		{"msg", "bravo", "ship", "it", "--verbose"},
		{"msg", "bravo", "use --dry-run first"},
		{"msg", "bravo", "--lead", "early", "guess"},
	}
	for _, s := range sends {
		if _, errw, code := f.run(t, f.repo, "", s...); code != 0 {
			t.Errorf("control %q refused: %s", s, errw)
		}
	}
	in, _, _ := f.run(t, f.wtB, "", "inbox", "--session", "sess-b")
	for _, want := range []string{"hello --dry-run", "ship it --verbose", "use --dry-run first", "declared LEAD"} {
		if !strings.Contains(in, want) {
			t.Errorf("control body %q not delivered:\n%s", want, in)
		}
	}
}

// #112: the core skill's FAIL recipe re-declared `--ready` on the run that had
// just closed, which is always refused, and the refusal told a lane whose work
// had just FAILED that the claim "has landed". A run released with a fail or
// aborted outcome now says so and where to declare instead; a pass, or a
// release with no outcome, keeps the old words (the control).
func TestWaitOnAFailedRunSaysFailedNotLanded(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	// bravo's standing wait: a refused declaration must leave it as it was.
	if _, errw, code := f.run(t, f.wtB, "", "wait", "--session", "sess-b", "--until", "1h", "--note", "keep-me"); code != 0 {
		t.Fatalf("setup wait: %s", errw)
	}
	// r9: one slug, reused, released PASS then FAIL in the same second (the
	// fixture's clock does not move), so only the row order tells them apart.
	for _, oc := range []string{"pass", "fail"} {
		if _, errw, code := f.run(t, f.repo, "", "claim", "r9", "--session", "sess-a", "--desc", "run", "--scope", ".buddy/slot/r9"); code != 0 {
			t.Fatalf("setup r9: %s", errw)
		}
		if _, errw, code := f.run(t, f.repo, "", "release", "r9", "--session", "sess-a", "--outcome", oc, "--note", "x"); code != 0 {
			t.Fatalf("release r9 %s: %s", oc, errw)
		}
	}
	for _, c := range []struct {
		slug, outcome, want, not string
	}{
		{"r5", "fail", `released 0s ago with outcome FAIL`, "has landed"},
		{"r6", "aborted", `released 0s ago with outcome ABORTED`, "has landed"},
		{"r7", "pass", "has landed", "with outcome"},
		{"r8", "", "has landed", "with outcome"},
		{"r9", "", "released 0s ago with outcome FAIL", "has landed"}, // set up above
	} {
		if c.slug != "r9" {
			if _, errw, code := f.run(t, f.repo, "", "claim", c.slug, "--session", "sess-a", "--desc", "run", "--scope", ".buddy/slot/"+c.slug); code != 0 {
				t.Fatalf("setup %s: %s", c.slug, errw)
			}
			rel := []string{"release", c.slug, "--session", "sess-a"}
			if c.outcome != "" {
				rel = append(rel, "--outcome", c.outcome, "--note", "red in cli")
			}
			if _, errw, code := f.run(t, f.repo, "", rel...); code != 0 {
				t.Fatalf("release %s: %s", c.slug, errw)
			}
		}
		_, errw, code := f.run(t, f.wtB, "", "wait", "--session", "sess-b", "--on", c.slug, "--ready", "HEAD")
		if code == 0 {
			t.Fatalf("%s: a wait on a released run was recorded", c.slug)
		}
		if !strings.Contains(errw, c.want) || strings.Contains(errw, c.not) {
			t.Errorf("%s (%q): %q, want %q and not %q", c.slug, c.outcome, errw, c.want, c.not)
		}
		if strings.Contains(c.want, "with outcome") {
			if !strings.Contains(errw, "the next run your orchestrator names") {
				t.Errorf("%s: the refusal does not say where to declare: %q", c.slug, errw)
			}
		}
		if wl, _, _ := f.run(t, f.repo, "", "wait", "ls"); !strings.Contains(wl, "keep-me") {
			t.Errorf("%s: a refused declaration changed bravo's wait:\n%s", c.slug, wl)
		}
	}
	// POSITIVE CONTROL: on an OPEN run the same declaration is recorded,
	// replacing bravo's standing wait, so the read-back above can see one.
	if _, errw, code := f.run(t, f.repo, "", "claim", "r10", "--session", "sess-a", "--desc", "run", "--scope", ".buddy/slot/r10"); code != 0 {
		t.Fatalf("setup r10: %s", errw)
	}
	if _, errw, code := f.run(t, f.wtB, "", "wait", "--session", "sess-b", "--on", "r10", "--ready", "HEAD"); code != 0 {
		t.Fatalf("control: a wait on an open run was refused: %s", errw)
	}
	if wl, _, _ := f.run(t, f.repo, "", "wait", "ls"); !strings.Contains(wl, "r10") || strings.Contains(wl, "keep-me") {
		t.Errorf("control: the declaration on r10 did not read back:\n%s", wl)
	}
}
