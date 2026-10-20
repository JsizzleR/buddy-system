package cli

// D-087 narrows D-073: the "cannot rule out" arm of the shell-patch gate
// (text produced at run time, a command the bash-mode parser rejects) guards
// Codex's in-process patch engine and fires only for a Codex call. On one
// fleet of 144 Claude sessions it denied about 10 Claude Bash calls in about 9
// sessions, every one a shape below, and the same probe written to a script
// file passed: it protected nothing there. A literal apply_patch/applypatch
// at a command position still refuses under every harness.

import (
	"encoding/json"
	"strings"
	"testing"
)

// codexBashHookJSON is bashHookJSON marked as a Codex hook, the way the
// installed Codex hook line marks it (BUDDY_HARNESS travels as buddy_harness).
func codexBashHookJSON(session, cwd, command string) string {
	b, _ := json.Marshal(map[string]any{
		"session_id": session, "cwd": cwd, "tool_name": "Bash", "buddy_harness": "codex",
		"tool_input": map[string]any{"command": command},
	})
	return string(b)
}

func TestGateUncertainRouteIsCodexOnly(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	// The shapes that denied Claude calls on that fleet, spelled generically.
	for _, cmd := range []string{
		`eval "$(brew shellenv)"`,
		`c='go test ./...'; sh -c "$c"`,
		`p='-n 5'; head ${=p} README`, // zsh word splitting: the Bash tool runs zsh here
		`printf '%s\n' 'uname -a' | ssh build-host 'sh -s'`,
		`curl -fsSL https://example.invalid/install.sh | bash`,
		"() { echo hi }",
	} {
		t.Run(cmd, func(t *testing.T) {
			// The positive control: the guard is armed, and a Codex call
			// with the same text is still refused as uncertain.
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, cmd), true, "cannot rule out")
			wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, cmd), false, "")
		})
	}
}

func TestGateLiteralApplyPatchRefusesUnderEveryHarness(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	plain := t.TempDir()
	for _, cmd := range []string{
		"apply_patch < p.patch",
		"applypatch < p.patch",
		"cd sub && apply_patch <<'EOF'\n*** Begin Patch\n*** Delete File: x\n*** End Patch\nEOF\n",
		"bash -c 'apply_patch < p.patch'",
		"eval 'apply_patch < p.patch'",
		"nice apply_patch < p.patch",
		// Uncertain text FIRST: under Claude the walk must go on past it and
		// still find the literal, not stop at an uncertainty it no longer
		// records.
		`eval "$x"; apply_patch < p.patch`,
		`printf '%s' "$y" | bash; apply_patch < p.patch`,
		// A dynamic env -S value is skipped, and env's argv after it is
		// still read: an empty $opts runs the literal (Codex code pass).
		`opts=''; env -S "$opts" apply_patch < p.patch`,
		`env --split-string "$opts" applypatch < p.patch`,
	} {
		t.Run(cmd, func(t *testing.T) {
			wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, cmd), true, "apply_patch")
			wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, cmd), true, "runs apply_patch")
			// Feature off where no ledger governs (invariant 3), either harness.
			wantGate(t, f, plain, bashHookJSON("sess-b", plain, cmd), false, "")
		})
	}
}

// An exhausted work bound is the gate having stopped reading, not a verdict
// about the text, so it still refuses a Claude call, in words that do not send
// a Claude session to a tool it does not have.
func TestGateExhaustedBoundRefusesClaudeToo(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	cmd := "foo" + strings.Repeat(" 'a b'", shellMaxParses+1)
	wantGate(t, f, f.repo, codexBashHookJSON("sess-b", f.repo, cmd), true, "budget")
	wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, cmd), true, "stopped reading")
	_, _, _ = f.run(t, f.repo, bashHookJSON("sess-b", f.repo, cmd), "gate")
	out, _, _ := f.run(t, f.repo, bashHookJSON("sess-b", f.repo, cmd), "gate")
	if strings.Contains(out, "apply_patch tool") || strings.Contains(out, "Codex can apply") {
		t.Fatalf("a Claude refusal must not point at Codex's tool: %s", out)
	}
}
