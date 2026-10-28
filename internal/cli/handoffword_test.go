package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// D-113: the roster says which sessions are past the handoff size. busy tells
// the lane (D-055, D-096); nothing told the orchestrator, whose roster printed
// `prompt 342k` with no reference point. The size is the READER's declared
// BUDDY_HANDOFF_AT, parsed as busy parses it; the word is a word, never a
// sort or a refusal (invariant 10).

// The decision, as a table over contextNote. Every silent row has a speaking
// row beside it that differs in ONE input, so "no word" cannot mean "never
// armed".
func TestContextNoteSaysOverHandoff(t *testing.T) {
	boundedParallel(t)
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		declared string
		prompt   int64
		window   int64
		want     string // the word, or "" for none
	}{
		{"unset", "", 900_000, 0, ""},
		{"malformed", "lots", 900_000, 0, ""},
		{"a fraction is not a size the parser takes", "0.3M", 900_000, 0, ""},
		{"zero", "0", 900_000, 0, ""},
		{"wraps when multiplied: undeclared", "18446744073709552k", 900_000, 0, ""},
		{"under by one token", "300k", 299_999, 0, ""},
		{"exactly at", "300k", 300_000, 0, " OVER handoff 300k"},
		{"over", "300k", 342_917, 0, " OVER handoff 300k"},
		{"over, control for the malformed row", "300k", 900_000, 0, " OVER handoff 300k"},
		{"the M form", "1M", 1_200_000, 0, " OVER handoff 1.0M"},
		{"spaces and an upper-case suffix", " 300K ", 300_000, 0, " OVER handoff 300k"},
		{"a bare count", "250000", 250_000, 0, " OVER handoff 250k"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := store.ContextSample{Prompt: tc.prompt, TurnAt: now.Add(-3 * time.Minute), Window: tc.window}
			got := contextNote(now, c, declaredWindow(tc.declared))
			if tc.want == "" {
				if strings.Contains(got, "OVER") || strings.Contains(got, "handoff") {
					t.Fatalf("want no word, got %q", got)
				}
				return
			}
			// Beside the prompt, before the turn age: the size and the word
			// read as one.
			if want := "prompt " + tokens(tc.prompt) + tc.want + " turn 3m"; !strings.Contains(got, want) {
				t.Fatalf("want %q in %q", want, got)
			}
		})
	}
	// With a declared window the word follows the percentage.
	c := store.ContextSample{Prompt: 342_000, TurnAt: now, Window: 1_000_000}
	if got, want := contextNote(now, c, 300_000), "prompt 342k/1.0M 34% OVER handoff 300k turn 0s"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// End to end: the reader's declaration decides, on live rows only, and the
// rows keep their order.
func TestRosterSaysOverHandoffOnLiveRows(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.observeB(t, 342_000)
	row := func(t *testing.T, label string) string {
		t.Helper()
		out, errw, code := f.run(t, f.repo, "", "sessions")
		if code != 0 {
			t.Fatalf("sessions: exit %d: %s", code, errw)
		}
		for _, ln := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			if fs := strings.Fields(ln); len(fs) > 1 && fs[1] == label {
				return ln
			}
		}
		t.Fatalf("no row for %q in:\n%s", label, out)
		return ""
	}
	order := func(t *testing.T) string {
		t.Helper()
		out, _, _ := f.run(t, f.repo, "", "sessions")
		var labels []string
		for _, ln := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			if fs := strings.Fields(ln); len(fs) > 1 {
				labels = append(labels, fs[1])
			}
		}
		return strings.Join(labels, " ")
	}
	before := order(t)

	// Unset: the prompt prints and no word does.
	if got := row(t, "bravo"); !strings.Contains(got, "prompt 342k turn") {
		t.Fatalf("positive control: the observation must print with no declaration:\n  %s", got)
	}
	f.env[EnvHandoffAt] = "350k"
	if got := row(t, "bravo"); strings.Contains(got, "OVER") {
		t.Fatalf("under the reader's size, no word:\n  %s", got)
	}
	f.env[EnvHandoffAt] = "300k"
	if got := row(t, "bravo"); !strings.Contains(got, "prompt 342k OVER handoff 300k turn") {
		t.Fatalf("over the reader's size, the word beside the prompt:\n  %s", got)
	}
	if got := row(t, "alpha"); strings.Contains(got, "OVER") {
		t.Fatalf("a row with no observation says nothing about a size:\n  %s", got)
	}
	if after := order(t); after != before {
		t.Fatalf("the word must not reorder the roster: %q, then %q", before, after)
	}

	// Ended: the prompt still prints, the word does not.
	if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "", ""), "bye"); code != 0 {
		t.Fatal(errw)
	}
	got := row(t, "bravo")
	if !strings.Contains(got, "ended") || !strings.Contains(got, "prompt 342k turn") {
		t.Fatalf("positive control: the ended row must still carry its prompt:\n  %s", got)
	}
	if strings.Contains(got, "OVER") {
		t.Fatalf("an ended session hands nothing off, so no word:\n  %s", got)
	}
}
