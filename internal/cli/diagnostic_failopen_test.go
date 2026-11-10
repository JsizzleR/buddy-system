package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A GATE LINE THAT FAILS OPEN (Codex report B2). Codex lets the tool call
// through when a PreToolUse hook fails: a timeout, a spawn error, a signal, or
// an exit other than 0 and 2 (codex-rs/hooks events/pre_tool_use.rs; the
// installed 0.166.0 binary carries the same strings). So a bare
// `BUDDY_HARNESS=codex /abs/buddy gate` whose buddy cannot decide (`cp` over the
// running Mach-O gets SIGKILL, a corrupt binary exits 126, a missing one 127)
// lets the patch through, and
// `diagnose --codex-hooks` called that line `marked` with no finding. USAGE's
// line (D-107) turns every such death into exit 2 with a reason on stderr,
// which is what Codex blocks on; exit 2 with an EMPTY stderr is a failed hook
// too, so `|| exit 2` alone is not the wrapper. An `async: true` hook runs in
// the background and can apply no control effect at all.
//
// The finding is beside the command and the verdict stays the marker verdict:
// diagnose describes a file and promotes nothing (D-071..D-081). Only the
// documented shape is recognised; any other spelling draws the finding, the
// safe direction for a report. The positive control is the documented line,
// with and without `async: false`.

// failClosed wraps a gate call in USAGE's fail-closed line (D-107).
func failClosed(gate string) string {
	return "{ " + gate + `; } 2>/dev/null || { echo "buddy gate failed (status $?); refusing the tool call (the gate fails closed). Operator: rebuild the binary this hook names" >&2; exit 2; }`
}

func TestDiagnoseCodexGateFailOpen(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	const gate = "BUDDY_HARNESS=codex /abs/bin/buddy gate"
	const reason = `echo "buddy gate failed (status $?); refusing the tool call (the gate fails closed). Operator: rebuild the binary this hook names" >&2`
	documented := failClosed(gate)
	if documented != "{ "+gate+"; } 2>/dev/null || { "+reason+"; exit 2; }" {
		t.Fatalf("failClosed and this test's reason line disagree: %s", documented)
	}
	const failOpen = "not wrapped fail-closed"
	const async = "async"
	for _, tc := range []struct {
		name    string
		event   string
		command string
		extra   map[string]any
		want    []string // findings expected; nil means none at all
		verdict string
	}{
		{"positive control: the documented line", "PreToolUse", documented, nil, nil, "marked"},
		{"positive control: documented, async false", "PreToolUse", documented, map[string]any{"async": false}, nil, "marked"},
		{"positive control: documented with a timeout", "PreToolUse", documented, map[string]any{"timeout": 30}, nil, "marked"},
		{"bare gate", "PreToolUse", gate, nil, []string{failOpen}, "marked"},
		{"exit 0 after the gate", "PreToolUse", gate + "; exit 0", nil, []string{failOpen}, "marked"},
		{"or true", "PreToolUse", gate + " || true", nil, []string{failOpen}, "marked"},
		{"or exit 2 with no reason on stderr", "PreToolUse", gate + " || exit 2", nil, []string{failOpen}, "marked"},
		{"reason on stdout, not stderr", "PreToolUse", "{ " + gate + "; } 2>/dev/null || { echo failed; exit 2; }", nil, []string{failOpen}, "marked"},
		{"exit 1, not 2", "PreToolUse", "{ " + gate + "; } 2>/dev/null || { " + reason + "; exit 1; }", nil, []string{failOpen}, "marked"},
		{"stdout discarded, so a deny is lost", "PreToolUse", "{ " + gate + "; } >/dev/null 2>&1 || { " + reason + "; exit 2; }", nil, []string{failOpen}, "marked"},
		{"exit 0 inside the group", "PreToolUse", "{ " + gate + "; exit 0; } 2>/dev/null || { " + reason + "; exit 2; }", nil, []string{failOpen}, "marked"},
		{"negated", "PreToolUse", "! { " + gate + "; } 2>/dev/null || { " + reason + "; exit 2; }", nil, []string{failOpen}, "marked"},
		{"backgrounded", "PreToolUse", documented + " &", nil, []string{failOpen}, "marked"},
		{"a second statement after the wrapper", "PreToolUse", documented + "; exit 0", nil, []string{failOpen}, "marked"},
		{"exit after the reason is not last", "PreToolUse", "{ " + gate + "; } 2>/dev/null || { " + reason + "; exit 2; true; }", nil, []string{failOpen}, "marked"},
		{"and, not or", "PreToolUse", "{ " + gate + "; } 2>/dev/null && { " + reason + "; exit 2; }", nil, []string{failOpen}, "marked"},
		{"reason not from echo, and empty", "PreToolUse", "{ " + gate + "; } 2>/dev/null || { true >&2; exit 2; }", nil, []string{failOpen}, "marked"},
		{"reason group's stderr discarded", "PreToolUse", "{ " + gate + "; } 2>/dev/null || { " + reason + "; exit 2; } 2>/dev/null", nil, []string{failOpen}, "marked"},
		// The Codex code pass's four: each fails open and was accepted.
		{"empty reason from printf", "PreToolUse", "{ " + gate + "; } 2>/dev/null || { printf '' >&2; exit 2; }", nil, []string{failOpen}, "marked"},
		{"empty reason from echo", "PreToolUse", "{ " + gate + `; } 2>/dev/null || { echo "" >&2; exit 2; }`, nil, []string{failOpen}, "marked"},
		{"echo -n prints nothing", "PreToolUse", "{ " + gate + "; } 2>/dev/null || { echo -n >&2; exit 2; }", nil, []string{failOpen}, "marked"},
		{"reason sent to /dev/null first", "PreToolUse", "{ " + gate + "; } 2>/dev/null || { echo failed 2>/dev/null >&2; exit 2; }", nil, []string{failOpen}, "marked"},
		{"stdout moved and closed", "PreToolUse", "{ " + gate + "; } 2>&1- || { " + reason + "; exit 2; }", nil, []string{failOpen}, "marked"},
		{"an argument after gate", "PreToolUse", failClosed(gate + " --help"), nil, []string{failOpen}, "marked"},
		// The mutation run's survivors: each clause of the recognizer, one
		// input it alone refuses.
		{"exit with a second argument", "PreToolUse", "{ " + gate + "; } 2>/dev/null || { " + reason + "; exit 2 3; }", nil, []string{failOpen}, "marked"},
		{"an assignment before exit", "PreToolUse", "{ " + gate + "; } 2>/dev/null || { " + reason + "; X=1 exit 2; }", nil, []string{failOpen}, "marked"},
		{"exec, not exit", "PreToolUse", "{ " + gate + "; } 2>/dev/null || { " + reason + "; exec 2; }", nil, []string{failOpen}, "marked"},
		{"exit redirected", "PreToolUse", "{ " + gate + "; } 2>/dev/null || { " + reason + "; exit 2 >/dev/null; }", nil, []string{failOpen}, "marked"},
		{"reason negated", "PreToolUse", "{ " + gate + "; } 2>/dev/null || { ! " + reason + "; exit 2; }", nil, []string{failOpen}, "marked"},
		{"reason backgrounded", "PreToolUse", "{ " + gate + "; } 2>/dev/null || { " + reason + " & exit 2; }", nil, []string{failOpen}, "marked"},
		{"reason to stdout by >&1", "PreToolUse", "{ " + gate + "; } 2>/dev/null || { echo refused >&1; exit 2; }", nil, []string{failOpen}, "marked"},
		{"reason from fd 3", "PreToolUse", "{ " + gate + "; } 2>/dev/null || { echo refused 3>&2; exit 2; }", nil, []string{failOpen}, "marked"},
		{"reason to a file named 2", "PreToolUse", "{ " + gate + "; } 2>/dev/null || { echo refused > 2; exit 2; }", nil, []string{failOpen}, "marked"},
		{"reason to stderr then /dev/null", "PreToolUse", "{ " + gate + "; } 2>/dev/null || { echo refused >&2 >/dev/null; exit 2; }", nil, []string{failOpen}, "marked"},
		{"a bare assignment as the reason", "PreToolUse", "{ " + gate + "; } 2>/dev/null || { X=1 >&2; exit 2; }", nil, []string{failOpen}, "marked"},
		{"gate's stdout discarded inside the group", "PreToolUse", "{ " + gate + " >/dev/null; } 2>/dev/null || { " + reason + "; exit 2; }", nil, []string{failOpen}, "marked"},
		{"gate negated inside the group", "PreToolUse", "{ ! " + gate + "; } 2>/dev/null || { " + reason + "; exit 2; }", nil, []string{failOpen}, "marked"},
		// Round two: one row per clause still unguarded.
		{"stdout discarded by an explicit 1>", "PreToolUse", "{ " + gate + "; } 1>/dev/null || { " + reason + "; exit 2; }", nil, []string{failOpen}, "marked"},
		{"a blank reason", "PreToolUse", "{ " + gate + `; } 2>/dev/null || { echo " " >&2; exit 2; }`, nil, []string{failOpen}, "marked"},
		{"a reason from true", "PreToolUse", "{ " + gate + "; } 2>/dev/null || { true refused >&2; exit 2; }", nil, []string{failOpen}, "marked"},
		// Narrow on purpose: printf with literal text prints, but its first
		// word is a format, so only echo is the documented shape.
		{"printf is not the documented reason", "PreToolUse", "{ " + gate + "; } 2>/dev/null || { printf refused >&2; exit 2; }", nil, []string{failOpen}, "marked"},
		{"stderr appended, not discarded", "PreToolUse", "{ " + gate + "; } 2>>/dev/null || { " + reason + "; exit 2; }", nil, []string{failOpen}, "marked"},
		{"stderr to a file", "PreToolUse", "{ " + gate + "; } 2>/tmp/gate.log || { " + reason + "; exit 2; }", nil, []string{failOpen}, "marked"},
		{"the wrapped call is another buddy verb", "PreToolUse", "/abs/bin/buddy hello 2>/dev/null || { echo \"x $(" + gate + ")\" >&2; exit 2; }", nil, []string{failOpen}, "unmarked"},
		{"the wrapped call is not buddy", "PreToolUse", "/abs/bin/other gate 2>/dev/null || { echo \"x $(" + gate + ")\" >&2; exit 2; }", nil, []string{failOpen}, "marked"},
		{"positive control: a single-quoted reason", "PreToolUse", "{ " + gate + "; } 2>/dev/null || { echo 'refused' >&2; exit 2; }", nil, nil, "marked"},
		{"positive control: a plain reason", "PreToolUse", "{ " + gate + "; } 2>/dev/null || { echo refused >&2; exit 2; }", nil, nil, "marked"},
		{"async documented line", "PreToolUse", documented, map[string]any{"async": true}, []string{async}, "marked"},
		{"async bare line draws both", "PreToolUse", gate, map[string]any{"async": true}, []string{failOpen, async}, "marked"},
		{"unmarked bare line keeps its verdict", "PreToolUse", "/abs/bin/buddy gate", nil, []string{"classified as a Claude hook", failOpen}, "unmarked"},
		// Only the gate decides; a lifecycle verb is refused by design and
		// draws that finding, not this one.
		{"lifecycle verb is not a gate", "Stop", "BUDDY_HARNESS=codex /abs/bin/buddy bye", nil, []string{"refused it by design", "no PreToolUse buddy gate"}, "marked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := map[string]any{"type": "command", "command": tc.command}
			for k, v := range tc.extra {
				h[k] = v
			}
			b, err := json.Marshal(map[string]any{"hooks": map[string]any{
				tc.event: []any{map[string]any{"matcher": "^(apply_patch|Bash)$", "hooks": []any{h}}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "hooks.json")
			if err := os.WriteFile(path, b, 0o600); err != nil {
				t.Fatal(err)
			}
			d := diagnosticOK(t, f, f.repo, "--codex-hooks", path).CodexHooks
			if d == nil || d.Verdict != tc.verdict {
				t.Fatalf("verdict %+v, want %s: the fail-open finding must not change the marker verdict", d, tc.verdict)
			}
			var findings []string
			for _, c := range d.Commands {
				if c.Finding != "" {
					findings = append(findings, c.Finding)
				}
			}
			joined := strings.Join(findings, " | ")
			if tc.want == nil {
				if joined != "" {
					t.Fatalf("positive control drew findings: %s", joined)
				}
				return
			}
			for _, w := range tc.want {
				if !strings.Contains(joined, w) {
					t.Errorf("findings %q missing %q", joined, w)
				}
			}
			for _, absent := range []string{failOpen, "cannot block"} {
				expected := false
				for _, w := range tc.want {
					expected = expected || w == absent || (absent == "cannot block" && w == async)
				}
				if !expected && strings.Contains(joined, absent) {
					t.Errorf("findings %q carry %q, which this line does not owe", joined, absent)
				}
			}
		})
	}
}
