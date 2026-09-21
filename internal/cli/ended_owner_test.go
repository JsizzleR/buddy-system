package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// D-026 end to end: the dry run says what a claim would displace, the claim
// takes it, and the gate's deny names the remedy for a holder that has said
// bye.
func TestClaimFreesAHolderThatSaidByeAndTheDryRunSaysSo(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "claim", "api-work", "--session", "sess-a", "--desc", "x", "--scope", "internal/api"); code != 0 {
		t.Fatalf("claim: %s", errw)
	}
	// CONTROL: refused while alpha is live, and the dry run says conflict.
	if _, _, code := f.run(t, f.wtB, "", "claim", "wants", "--session", "sess-b", "--dry-run", "--desc", "x", "--scope", "internal/api/x.go"); code == 0 {
		t.Fatal("not contested beforehand")
	}
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "bye"); code != 0 {
		t.Fatalf("bye: %s", errw)
	}
	out, errw, code := f.run(t, f.wtB, "", "claim", "wants", "--session", "sess-b", "--dry-run", "--desc", "x", "--scope", "internal/api/x.go")
	if code != 0 {
		t.Fatalf("dry run against an ended holder must not refuse: %s %s", out, errw)
	}
	if !strings.Contains(out, "note: internal/api/x.go is held by ENDED session alpha (claim \"api-work\", scope \"internal/api\"); a real claim frees it") {
		t.Fatalf("the dry run must say what it displaces:\n%s", out)
	}
	if !strings.Contains(out, "would claim: internal/api/x.go") || strings.Contains(out, "REFUSED") {
		t.Fatalf("and forecast the take:\n%s", out)
	}
	// The gate, meanwhile, still denies bravo on the ended holder's scope —
	// the residual D-026 states — and its deny names the plain sweep.
	target := filepath.Join(f.wtB, "internal/api/x.go")
	gate, _, _ := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Edit", target), "gate")
	reason, denied := decodeDeny(t, gate)
	if !denied || !strings.Contains(reason, "plain `buddy sweep`") || !strings.Contains(reason, "`buddy claim`") {
		t.Fatalf("the deny must name the remedy for a holder that has said bye: %v %q", denied, reason)
	}
	if _, errw, code := f.run(t, f.wtB, "", "claim", "wants", "--session", "sess-b", "--desc", "x", "--scope", "internal/api/x.go"); code != 0 {
		t.Fatalf("the real claim must succeed: %s", errw)
	}
	ls, _, _ := f.run(t, f.repo, "", "ls", "--all")
	if !strings.Contains(ls, "orphaned") || !strings.Contains(ls, "wants") {
		t.Fatalf("the ended holder's claim must be orphaned and the new one open:\n%s", ls)
	}
	// And now the gate lets bravo through.
	gate, _, _ = f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Edit", target), "gate")
	if _, denied := decodeDeny(t, gate); denied {
		t.Fatalf("the new holder must not be denied: %q", gate)
	}
}
