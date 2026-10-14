package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE DOC'S OWN EXAMPLE IS A FIXTURE. docs/USAGE.md ("Opt-in wiring") shows
// the one Codex hooks.json layer an operator is told to install, and the
// paragraph under it promises that `buddy diagnose --codex-hooks` reports the
// marker it carries. Nothing held the two together: the prose example and the
// diagnostic are edited by hand, on different days, and a block that drifts (the
// marker respelled, a wrapper added, a second hook) keeps reading as correct
// while the diagnostic says `unmarked` to whoever pastes it. So the block is
// read out of the doc, the placeholder path is replaced by an absolute
// executable the way the doc says to, and the diagnostic is run on that file.
//
// The positive control strips the marker from the same block and expects
// `unmarked`. Without it, a diagnostic that said `marked` for every layer would
// pass the first check, and "the doc's example is marked" and "the check never
// looked" would be the same observation.

// usageOptInWiringBlock returns the JSON fenced block inside the "Opt-in
// wiring" passage of docs/USAGE.md, without its fence lines. The search is
// bounded to that passage (up to the next bold-led paragraph), so a block moved
// elsewhere fails here rather than being read from another section.
func usageOptInWiringBlock(t *testing.T, doc string) string {
	t.Helper()
	const section = "**Opt-in wiring.**"
	i := strings.Index(doc, section)
	if i < 0 {
		t.Fatalf("docs/USAGE.md has no %q paragraph; this test reads the Codex hook example that follows it", section)
	}
	passage := doc[i+len(section):]
	if end := strings.Index(passage, "\n**"); end >= 0 {
		passage = passage[:end]
	}
	const open = "\n```json\n"
	j := strings.Index(passage, open)
	if j < 0 {
		t.Fatalf("no ```json block follows the %q paragraph of docs/USAGE.md", section)
	}
	block := passage[j+len(open):]
	k := strings.Index(block, "\n```")
	if k < 0 {
		t.Fatalf("the ```json block after %q in docs/USAGE.md is not closed", section)
	}
	return block[:k]
}

func TestUsageCodexHookExampleDiagnosesMarked(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	body, err := os.ReadFile("../../docs/USAGE.md")
	if err != nil {
		t.Fatal(err)
	}
	block := usageOptInWiringBlock(t, string(body))

	// The doc says to replace this placeholder with the built binary's absolute
	// path. The diagnostic parses the command and never runs it, so an
	// executable stub named buddy stands in; what the substitution must keep is
	// the shape the doc requires: absolute, executable, base name buddy.
	const placeholder = "/absolute/path/to/buddy"
	if n := strings.Count(block, placeholder); n != 1 {
		t.Fatalf("the Opt-in wiring example names %q %d time(s), want exactly 1; the doc tells the operator to replace it:\n%s", placeholder, n, block)
	}
	bin := filepath.Join(t.TempDir(), "bin", "buddy")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(bin); err != nil || fi.Mode()&0o111 == 0 || !filepath.IsAbs(bin) {
		t.Fatalf("stub %s is not an absolute executable: %v %v", bin, fi, err)
	}
	wired := strings.Replace(block, placeholder, bin, 1)
	if !json.Valid([]byte(wired)) {
		t.Fatalf("the Opt-in wiring example is not valid JSON once the path is substituted:\n%s", wired)
	}

	layer := func(t *testing.T, content string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "hooks.json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	r := diagnosticOK(t, f, f.repo, "--codex-hooks", layer(t, wired))
	d := r.CodexHooks
	if d == nil {
		t.Fatalf("diagnose --codex-hooks produced no codex_hooks report: %+v", r)
	}
	if d.Verdict != "marked" || d.Unmarked != 0 || d.Unrecognized != 0 {
		t.Fatalf("the doc's Opt-in wiring example diagnoses as %+v, want verdict marked with nothing unmarked or unrecognized: fix the example or the diagnostic, in the same commit", d)
	}
	// The paragraph says the example "deliberately wires only the gate": one
	// PreToolUse command, verb gate, marker codex, and no finding (gate is not
	// a lifecycle verb, so nothing is refused by design).
	want := codexHookCommand{Event: "PreToolUse", Verb: "gate", Marker: "codex"}
	if len(d.Commands) != 1 || d.Commands[0] != want {
		t.Fatalf("the example's commands diagnose as %+v, want exactly [%+v]: the doc says it wires only the gate", d.Commands, want)
	}

	t.Run("positive control: the same block with the marker stripped is unmarked", func(t *testing.T) {
		stripped := strings.Replace(wired, EnvHarness+"=codex ", "", 1)
		if stripped == wired {
			t.Fatalf("no %s=codex marker was found to strip in:\n%s", EnvHarness, wired)
		}
		r := diagnosticOK(t, f, f.repo, "--codex-hooks", layer(t, stripped))
		d := r.CodexHooks
		if d == nil || d.Verdict != "unmarked" || d.Unmarked != 1 {
			t.Fatalf("the stripped example diagnoses as %+v, want verdict unmarked with 1 unmarked: the diagnostic is not reading the marker, so the check above proves nothing", d)
		}
	})
}
