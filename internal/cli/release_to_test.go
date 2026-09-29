package cli

import (
	"strings"
	"testing"
)

// D-063: `release <slug> --to <target>` hands an open claim on, keeping it.
// The measured failure was a successor re-claiming an in-flight run, which
// landed every rider with "no outcome" and dropped its READY; here the rider
// keeps waiting, stays READY on `who`, and lands on the NEW holder's outcome.
func TestReleaseToHandsTheRunOnWithItsRiders(t *testing.T) {
	boundedParallel(t)
	b := newBatchFx(t)
	b.form(t)
	b.ok(t, "sess-c", "wait", "--on", "herm", "--ready", "HEAD")

	// Refusals write nothing: herm stays alpha's through each.
	b.refused(t, "sess-a", "an outcome ends the job --to hands on", "release", "herm", "--to", "bravo", "--outcome", "pass")
	b.refused(t, "sess-a", "a transfer is of the whole claim", "release", "herm", "--to", "bravo", "--scope", ".buddy/slot/main")
	b.refused(t, "sess-a", "a target naming nobody", "release", "herm", "--to", "nobody-at-all")
	b.refused(t, "sess-a", "an empty --to is not a plain release", "release", "herm", "--to", "")
	b.refused(t, "sess-a", "a note rides an outcome, which --to takes none of", "release", "herm", "--to", "bravo", "--note", "x")
	b.refused(t, "sess-a", "the rider cannot be handed the claim it waits on", "release", "herm", "--to", "charlie")
	if w := b.ok(t, "sess-a", "who", "herm"); !strings.Contains(w, "held by:\n* alpha ") {
		t.Fatalf("refused transfers must leave herm with alpha:\n%s", w)
	}

	out := b.ok(t, "sess-a", "release", "herm", "--to", "bravo")
	if !strings.HasPrefix(out, `handed "herm" to bravo — it stays open, with its scopes, and the 1 wait(s) on it follow it: charlie`) ||
		!strings.Contains(out, "; notice queued for them: message #") || strings.Count(out, "\n") != 1 {
		t.Fatalf("one result line naming the recipient, the riders and the notice:\n%s", out)
	}
	w := b.ok(t, "sess-a", "who", "herm")
	if !strings.Contains(w, "held by:\n- bravo ") || !strings.Contains(w, "1 READY, 0 not:") {
		t.Fatalf("bravo holds herm, and charlie is still READY on it:\n%s", w)
	}
	if ctx := b.beat(t, "sess-b"); !strings.Contains(ctx, `handed you claim \"herm\"`) && !strings.Contains(ctx, `handed you claim "herm"`) {
		t.Fatalf("the recipient is told, signed by the sender:\n%s", ctx)
	}
	if out := b.ok(t, "sess-c", "wait", "check"); !strings.HasPrefix(out, "STILL WAITING") {
		t.Fatalf("the rider's wait did not land on the handover:\n%s", out)
	}
	// The old holder no longer holds it; the new one releases with an outcome
	// and that is what the rider lands on.
	b.refused(t, "sess-a", "alpha handed it on", "release", "herm")
	b.ok(t, "sess-b", "release", "herm", "--outcome", "pass", "--note", "landed abc")
	if out := b.ok(t, "sess-c", "wait", "check"); !strings.HasPrefix(out, `LANDED: claim "herm" released 0s ago, outcome PASS "landed abc"`) ||
		strings.Contains(out, "No outcome") {
		t.Fatalf("the rider lands on the new holder's outcome:\n%s", out)
	}
}
