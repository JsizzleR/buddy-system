package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// D-042 (issue #33): a SHARED claim through the commands. The store tests hold
// the rule; these hold what a session reads and what the gate does with it.

func TestSharedClaimThroughTheGate(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	claim := func(cwd, session, slug string, extra ...string) (string, string, int) {
		t.Helper()
		args := append([]string{"claim", slug, "--session", session, "--desc", "append a rule", "--scope", "docs/playbook.md"}, extra...)
		return f.run(t, cwd, "", args...)
	}
	gateB := func() (string, bool) {
		t.Helper()
		out, _, _ := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Edit", filepath.Join(f.wtB, "docs/playbook.md")), "gate")
		return decodeDeny(t, out)
	}

	out, errw, code := claim(f.repo, "sess-a", "playbook-a", "--shared")
	if code != 0 || !strings.Contains(out, "SHARED scopes: docs/playbook.md") {
		t.Fatalf("a shared claim must say so on its result line: %d %s %s", code, out, errw)
	}
	// B has not joined: the gate refuses, and names the move that clears it.
	reason, denied := gateB()
	if !denied || !strings.Contains(reason, "claimed SHARED by session alpha") || !strings.Contains(reason, "--shared --scope") {
		t.Fatalf("an unjoined edit under a shared claim must be denied naming --shared: %q", reason)
	}

	// B asks exclusively: refused, and the refusal says --shared would clear it.
	out, _, code = claim(f.wtB, "sess-b", "playbook-b")
	if code == 0 || !strings.Contains(out, `held SHARED by alpha`) || !strings.Contains(out, "claiming --shared would clear them") {
		t.Fatalf("an exclusive request against a shared holder must be refused with the SHARED note: %d %s", code, out)
	}
	// The dry run says the same, by the same computation.
	out, _, _ = claim(f.wtB, "sess-b", "playbook-b", "--dry-run")
	if !strings.Contains(out, "claiming --shared would clear them") {
		t.Fatalf("the dry run must carry the SHARED note the refusal does: %s", out)
	}

	// B joins: admitted, and its edit passes the gate.
	if out, errw, code := claim(f.wtB, "sess-b", "playbook-b", "--shared"); code != 0 {
		t.Fatalf("a shared request against a shared holder must be admitted: %s %s", out, errw)
	}
	if reason, denied := gateB(); denied {
		t.Fatalf("a joined session must be admitted by the gate: %q", reason)
	}

	// ls marks both, with buddy's own word ahead of the fenced scopes.
	out, _, _ = f.run(t, f.repo, "", "ls")
	if strings.Count(out, "SHARED docs/playbook.md") != 2 {
		t.Fatalf("ls must mark both shared claims:\n%s", out)
	}
}

// Positive control for everything above: an EXCLUSIVE hold refuses a shared
// request and says nothing about --shared, because it would not help.
func TestExclusiveHoldPrintsNoSharedNote(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "claim", "p", "--session", "sess-a", "--desc", "d", "--scope", "docs/playbook.md"); code != 0 {
		t.Fatal(errw)
	}
	out, _, code := f.run(t, f.wtB, "", "claim", "q", "--session", "sess-b", "--desc", "d", "--scope", "docs/playbook.md", "--shared")
	if code == 0 || !strings.Contains(out, "REFUSED: docs/playbook.md") {
		t.Fatalf("control: an exclusive hold must refuse a shared request: %s", out)
	}
	if strings.Contains(out, "SHARED") {
		t.Fatalf("an exclusive holder must not be described as shared, or --shared offered:\n%s", out)
	}
	reason, denied := decodeDeny(t, func() string {
		out, _, _ := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Edit", filepath.Join(f.wtB, "docs/playbook.md")), "gate")
		return out
	}())
	if !denied || strings.Contains(reason, "SHARED") {
		t.Fatalf("an exclusive hold's deny must be the ordinary one: %q", reason)
	}
}
