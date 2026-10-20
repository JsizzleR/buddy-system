package cli

import (
	"strings"
	"testing"
)

// D-093: `release a b c --to X` hands a set on in ONE transaction with ONE
// notice. The measured failure: a sender whose own claims overlapped each
// other could hand none of them on one at a time, and each refusal said "and
// N more conflict(s), listed above" over a listing that was never printed.
func TestReleaseToHandsASetWithOneNoticeAndPrintsTheWholeRefusal(t *testing.T) {
	boundedParallel(t)
	b := newBatchFx(t)
	b.ok(t, "sess-a", "claim", "outer", "--desc", "d", "--scope", "pkg")
	b.ok(t, "sess-a", "claim", "in1", "--desc", "d", "--scope", "pkg/a.go")
	b.ok(t, "sess-a", "claim", "in2", "--desc", "d", "--scope", "pkg/b.go")

	// One alone: refused, and the refusal PRINTS what "listed above" names.
	out, errw, code := b.as(t, "sess-a", "release", "outer", "--to", "bravo")
	if code == 0 {
		t.Fatalf("outer alone must be refused:\n%s", out)
	}
	if !strings.Contains(errw, "1 more conflict(s), listed above; nothing was handed on") {
		t.Fatalf("the error names the rest of the set:\n%s", errw)
	}
	if strings.Count(out, "REFUSED: ") != 2 || !strings.Contains(out, `claim "in1"`) || !strings.Contains(out, `claim "in2"`) {
		t.Fatalf("every conflict the error counts must be listed, one REFUSED line each:\n%s", out)
	}

	// Plain multi-release is whole or nothing too: a slug that is not alpha's
	// releases none of them.
	if errw := b.refused(t, "sess-a", "one stranger in the set", "release", "outer", "in1", "nope"); !strings.Contains(errw, "nothing was released") {
		t.Fatalf("the refusal says nothing was released:\n%s", errw)
	}
	b.refused(t, "sess-a", "an outcome reports on one job", "release", "outer", "in1", "--outcome", "pass")
	b.refused(t, "sess-a", "--scope narrows one claim", "release", "outer", "in1", "--scope", "pkg")
	if ls := b.ok(t, "sess-a", "ls"); strings.Count(ls, "alpha") != 3 {
		t.Fatalf("refused releases left all three with alpha:\n%s", ls)
	}

	// Together: one line, one notice, one transaction.
	out = b.ok(t, "sess-a", "release", "outer", "in1", "in2", "--to", "bravo")
	if !strings.HasPrefix(out, `handed 3 claims to bravo in one transaction: "outer", "in1", "in2"`) ||
		strings.Count(out, "message #") != 1 || strings.Count(out, "\n") != 1 {
		t.Fatalf("one result line naming the set and one notice:\n%s", out)
	}
	ctx := b.beat(t, "sess-b")
	if strings.Count(ctx, "handed you") != 1 || !strings.Contains(ctx, "3 claims in one transaction") {
		t.Fatalf("the recipient gets ONE notice for the set:\n%s", ctx)
	}
	if ls := b.ok(t, "sess-a", "ls"); strings.Count(ls, "bravo") != 3 {
		t.Fatalf("bravo holds all three:\n%s", ls)
	}

	// And bravo releases the set in one call, a line per claim.
	out = b.ok(t, "sess-b", "release", "outer", "in1", "in2")
	if strings.Count(out, "released ") != 3 {
		t.Fatalf("a line per released claim:\n%s", out)
	}
	if ls := b.ok(t, "sess-a", "ls"); !strings.Contains(ls, "no claims") {
		t.Fatalf("nothing is held after the set release:\n%s", ls)
	}
}

// D-093, narrowing D-063: the successor waiting on the claim it inherits is
// handed it, and its own check and beat say LANDED, handed to you.
func TestASuccessorWaitingOnTheClaimIsHandedItAndLands(t *testing.T) {
	boundedParallel(t)
	b := newBatchFx(t)
	b.form(t)
	b.ok(t, "sess-c", "wait", "--on", "herm", "--until", "1h")
	b.ok(t, "sess-b", "wait", "--on", "herm", "--ready", "HEAD")
	// Positive control: the check is armed and says it is waiting.
	if out := b.ok(t, "sess-c", "wait", "check"); !strings.HasPrefix(out, "STILL WAITING") {
		t.Fatalf("charlie waits before the handover:\n%s", out)
	}

	out := b.ok(t, "sess-a", "release", "herm", "--to", "charlie")
	if !strings.Contains(out, `charlie was waiting on "herm", and that wait is resolved by this`) ||
		!strings.Contains(out, "the 1 wait(s) on it follow it: bravo") {
		t.Fatalf("the sender is told the recipient's wait resolved and the rider's follows:\n%s", out)
	}
	ctx := b.beat(t, "sess-c")
	if !strings.Contains(ctx, "your wait LANDED") || !strings.Contains(ctx, `"herm" handed to you`) ||
		!strings.Contains(ctx, "Your wait on it is resolved") {
		t.Fatalf("charlie's beat carries the notice and the LANDED, handed to you:\n%s", ctx)
	}
	if out := b.ok(t, "sess-c", "wait", "check"); !strings.HasPrefix(out, `LANDED: claim "herm" (handed to charlie, which waited on it`) ||
		!strings.Contains(out, `"herm" handed to you`) {
		t.Fatalf("charlie's check says LANDED, handed to you:\n%s", out)
	}
	// The rider is unaffected: still waiting, still READY, on charlie now.
	if out := b.ok(t, "sess-b", "wait", "check"); !strings.HasPrefix(out, `STILL WAITING on claim "herm" (held by charlie`) {
		t.Fatalf("bravo still waits, on the new holder:\n%s", out)
	}
}
