package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// D-049: one long run closes out several sessions. The integrator holds the
// run's slot claim; riders declare `wait --on <slot> --ready <commit>`; `who`
// lists who is in; `release --outcome` says what became of the run, and every
// rider's LANDED carries it. Every refusal has a positive control beside it.

// batchFx: alpha (sess-a, repo) integrates; bravo (sess-b, wtB) and charlie
// (sess-c, wtC) ride.
type batchFx struct {
	*fixture
	wtC string
}

func newBatchFx(t *testing.T) *batchFx {
	t.Helper()
	f := newFixture(t)
	f.initAndHello(t)
	b := &batchFx{fixture: f, wtC: filepath.Join(filepath.Dir(f.wtB), "wtC")}
	gitIn(t, f.repo, "worktree", "add", "-q", b.wtC)
	if _, errw, code := f.run(t, b.wtC, hookJSON("sess-c", b.wtC, "", ""), "hello", "--label", "charlie"); code != 0 {
		t.Fatalf("hello c: %s", errw)
	}
	return b
}

func (b *batchFx) cwdOf(sid string) string {
	switch sid {
	case "sess-b":
		return b.wtB
	case "sess-c":
		return b.wtC
	}
	return b.repo
}

func (b *batchFx) as(t *testing.T, sid string, args ...string) (out, errw string, code int) {
	t.Helper()
	b.asSession(sid, func() { out, errw, code = b.run(t, b.cwdOf(sid), "", args...) })
	return out, errw, code
}

func (b *batchFx) ok(t *testing.T, sid string, args ...string) string {
	t.Helper()
	out, errw, code := b.as(t, sid, args...)
	if code != 0 {
		t.Fatalf("%v as %s: exit %d: %s", args, sid, code, errw)
	}
	return out
}

func (b *batchFx) refused(t *testing.T, sid, why string, args ...string) string {
	t.Helper()
	out, errw, code := b.as(t, sid, args...)
	if code == 0 {
		t.Fatalf("%s: %v as %s must be refused:\n%s", why, args, sid, out)
	}
	return errw
}

func (b *batchFx) beat(t *testing.T, sid string) string {
	t.Helper()
	out, errw, code := b.run(t, b.cwdOf(sid), hookJSON(sid, b.cwdOf(sid), "Bash", ""), "beat")
	if code != 0 {
		t.Fatalf("beat %s: %s", sid, errw)
	}
	return additionalContext(t, out)
}

func (b *batchFx) form(t *testing.T) {
	t.Helper()
	b.ok(t, "sess-a", "claim", "herm", "--desc", "FORMING: wait --on herm --ready HEAD",
		"--scope", ".buddy/slot/herm", "--scope", ".buddy/slot/main")
}

func TestABatchedRunListsItsRidersAndCarriesItsOutcome(t *testing.T) {
	boundedParallel(t)
	b := newBatchFx(t)
	b.form(t)

	// bravo is in, not ready yet: an ordinary wait with its ETA as a note.
	b.ok(t, "sess-b", "wait", "--on", "herm", "--note", "done in 10")
	// Control: with no rider declaring --ready, WAITED ON keeps its one line.
	if w := b.ok(t, "sess-a", "who", "herm"); !strings.Contains(w, "WAITED ON    by 1 session(s): bravo ") || strings.Contains(w, "READY") {
		t.Fatalf("no rider declared ready, so the short form:\n%s", w)
	}

	commitN(t, b.wtC, "cwork", 1)
	c := gitIn(t, b.wtC, "rev-parse", "HEAD")
	out := b.ok(t, "sess-c", "wait", "--on", "herm", "--ready", "HEAD")
	if !strings.Contains(lines(out)[0], "; you declared your work ready at "+c[:8]) {
		t.Fatalf("the declaration says what it recorded:\n%s", out)
	}
	w := b.ok(t, "sess-a", "who", "herm")
	if !strings.Contains(w, "WAITED ON    by 2 session(s), 1 READY, 1 not:\n") {
		t.Fatalf("who counts the riders:\n%s", w)
	}
	i := strings.Index(w, "READY at "+c[:8])
	j := strings.Index(w, `not ready (declared 0s ago, seen 0s ago) — note "done in 10"`)
	if i < 0 || j < 0 || i > j || !strings.Contains(w, "\n  charlie ") || !strings.Contains(w, "\n  bravo ") {
		t.Fatalf("one line per rider, READY first, the note as bravo's own words:\n%s", w)
	}

	// Refusals write nothing: bravo's not-ready wait survives each of them.
	b.refused(t, "sess-b", "a commit git cannot name", "wait", "--on", "herm", "--ready", "no-such-rev")
	b.refused(t, "sess-b", "readiness with nobody to hand it to", "wait", "--ready", "HEAD")
	b.refused(t, "sess-b", "an option where a commit goes", "wait", "--on", "herm", "--ready", "--all")
	if w := b.ok(t, "sess-a", "who", "herm"); !strings.Contains(w, "1 READY, 1 not:") || !strings.Contains(w, `note "done in 10"`) {
		t.Fatalf("a refused re-declaration left bravo's wait as it was:\n%s", w)
	}
	b.ok(t, "sess-b", "wait", "--on", "herm", "--ready", "HEAD")
	if w := b.ok(t, "sess-a", "who", "herm"); !strings.Contains(w, "2 READY, 0 not:") {
		t.Fatalf("bravo is ready now:\n%s", w)
	}

	// A malformed report releases nothing: each refusal leaves herm open.
	b.refused(t, "sess-a", "an outcome not on the list", "release", "herm", "--outcome", "maybe")
	b.refused(t, "sess-a", "a note with no outcome", "release", "herm", "--note", "x")
	b.refused(t, "sess-a", "an outcome on a narrowing", "release", "herm", "--scope", ".buddy/slot/main", "--outcome", "pass")
	if w := b.ok(t, "sess-a", "who", "herm"); !strings.Contains(w, "2 READY, 0 not:") {
		t.Fatalf("herm must still be open after refused releases:\n%s", w)
	}

	rel := b.ok(t, "sess-a", "release", "herm", "--outcome", "pass", "--note", "landed 7d3a0b1c; delta NEXT")
	if !strings.Contains(rel, `released "herm" — outcome PASS "landed 7d3a0b1c; delta NEXT" recorded for its waiters — 2 session(s)`) {
		t.Fatalf("the release says what it recorded:\n%s", rel)
	}
	if ctx := b.beat(t, "sess-c"); !strings.Contains(ctx, `claim "herm" released 0s ago, outcome PASS "landed 7d3a0b1c; delta NEXT"`) ||
		strings.Contains(ctx, "No outcome") {
		t.Fatalf("the one-shot LANDED carries the outcome:\n%s", ctx)
	}
	out = b.ok(t, "sess-c", "wait", "check")
	if !strings.HasPrefix(out, `LANDED: claim "herm" released 0s ago, outcome PASS "landed 7d3a0b1c; delta NEXT" — the wait is over`) ||
		strings.Contains(out, "No outcome") {
		t.Fatalf("the check's LANDED carries the outcome:\n%s", out)
	}
}

// Codex design pass: an integrator that aborts before running anything
// releases too, so a rider's LANDED must not read as "your work went
// through" when no outcome came with it. A waiter that declared nothing ready
// is not a rider and hears nothing extra (the control).
func TestARiderIsToldWhenAReleaseReportedNoOutcome(t *testing.T) {
	boundedParallel(t)
	b := newBatchFx(t)
	b.form(t)
	b.ok(t, "sess-b", "wait", "--on", "herm")
	b.ok(t, "sess-c", "wait", "--on", "herm", "--ready", "HEAD")
	b.ok(t, "sess-a", "release", "herm")

	if ctx := b.beat(t, "sess-c"); !strings.Contains(ctx, "No outcome was reported with that release") {
		t.Fatalf("the rider's notice says no outcome came with the release:\n%s", ctx)
	}
	if out := b.ok(t, "sess-c", "wait", "check"); !strings.Contains(out, "\nNo outcome was reported with that release") {
		t.Fatalf("the rider's check says it too:\n%s", out)
	}
	if ctx := b.beat(t, "sess-b"); !strings.Contains(ctx, "your wait LANDED") || strings.Contains(ctx, "No outcome") {
		t.Fatalf("a plain waiter gets the plain notice:\n%s", ctx)
	}
	if out := b.ok(t, "sess-b", "wait", "check"); !strings.HasPrefix(out, "LANDED: ") || strings.Contains(out, "No outcome") {
		t.Fatalf("a plain waiter gets the plain LANDED:\n%s", out)
	}
}

// An integrator that ENDS holding the run's claim: the claim is orphaned at
// the next cleanup, never released, and a rider is told so rather than read
// "landed" as the run having happened.
func TestARiderIsToldWhenTheRunsClaimWasOrphaned(t *testing.T) {
	boundedParallel(t)
	b := newBatchFx(t)
	b.form(t)
	b.ok(t, "sess-c", "wait", "--on", "herm", "--ready", "HEAD")
	if _, errw, code := b.run(t, b.repo, hookJSON("sess-a", b.repo, "", ""), "bye"); code != 0 {
		t.Fatalf("bye: %s", errw)
	}
	out := b.ok(t, "sess-c", "wait", "check")
	if !strings.HasPrefix(out, `LANDED: claim "herm" orphaned`) || !strings.Contains(out, "That claim was ORPHANED, not released") {
		t.Fatalf("an orphaned run's rider is told it was not released:\n%s", out)
	}
}

// Codex code pass: beat's notices were written ahead of an inbox drain bounded
// only by its own 8 KiB, and a long LANDED line (a 512-byte outcome note, a
// rider's caveat, a 512-byte wait note) plus a full drain crossed the
// harness's 10,000-character cap — past which the model gets a preview while
// every message beside it is marked delivered. The drain now takes the room
// the notices leave. Control: messages are still delivered on that beat, and
// what did not fit is delivered on the next.
func TestBeatsNoticesAndDrainShareOneBudget(t *testing.T) {
	boundedParallel(t)
	b := newBatchFx(t)
	slug := "herm-" + strings.Repeat("s", 123) // 128 bytes, the cap
	b.ok(t, "sess-a", "claim", slug, "--desc", "d", "--scope", ".buddy/slot/herm")
	b.ok(t, "sess-a", "claim", slug+"x", "--desc", "d", "--scope", ".buddy/slot/main")
	b.ok(t, "sess-c", "wait", "--on", slug, "--on", slug+"x", "--ready", "HEAD", "--note", strings.Repeat("n", maxWaitNote))
	b.ok(t, "sess-a", "release", slug, "--outcome", "fail", "--note", strings.Repeat("o", maxOutcomeNote))
	b.ok(t, "sess-a", "release", slug+"x") // no outcome: the rider's caveat rides too
	const n = 20
	for i := range n {
		// ~1,950 bytes a line: four pack an 8 KiB drain nearly full, which
		// with the notice passes the budget unless the drain yields room
		// (3,000-byte lines left slack either way, and a mutation survived).
		b.ok(t, "sess-a", "msg", "charlie", fmt.Sprintf("m%02d ", i)+strings.Repeat("b", 1900))
	}
	ctx := b.beat(t, "sess-c")
	if !strings.Contains(ctx, "your wait LANDED") || !strings.Contains(ctx, "No outcome was reported") {
		t.Fatalf("control: the notice and its caveat are there:\n%.300s", ctx)
	}
	if len(ctx) > helloBudget {
		t.Fatalf("beat wrote %d bytes, over the %d budget (the harness cap is 10,000)", len(ctx), helloBudget)
	}
	first := strings.Count(ctx, "[alpha]")
	if first == 0 || first == n {
		t.Fatalf("the drain must deliver some and leave the rest (delivered %d of %d)", first, n)
	}
	if next := strings.Count(b.beat(t, "sess-c"), "[alpha]"); next == 0 {
		t.Fatal("what did not fit must arrive on the next beat")
	}
}

// Codex code pass: `rev^{commit}` in one step lets a PATH revision swallow
// the suffix — a tracked file literally named `x^{commit}` made `HEAD:x`
// resolve to that file's blob, recorded as the commit a rider is ready at.
func TestReadyRefusesABlobSpelledAsACommit(t *testing.T) {
	boundedParallel(t)
	b := newBatchFx(t)
	b.form(t)
	// Both files: `x` makes HEAD:x resolve at all (so the PEEL is what
	// refuses it), and `x^{commit}` is what the one-step spelling resolved.
	// With the peel in place the one-step spelling alone is also refused,
	// so a mutation back to it is expected to survive; the peel's does not.
	for _, name := range []string{"x", "x^{commit}"} {
		if err := os.WriteFile(filepath.Join(b.wtC, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, b.wtC, "add", name)
	}
	gitIn(t, b.wtC, "commit", "-q", "-m", "a file named like a peel")
	b.refused(t, "sess-c", "a blob is not a commit", "wait", "--on", "herm", "--ready", "HEAD:x")
	// Control: the same tree's commit is accepted.
	b.ok(t, "sess-c", "wait", "--on", "herm", "--ready", "HEAD")
}

// Codex code pass: sweep deletes a closed claim's row after its ttl, and the
// reported outcome goes with it. A rider that checks after that is told the
// outcome is gone, never shown a LANDED that reads like nothing was said.
func TestARiderIsToldASweptOutcomeIsGone(t *testing.T) {
	boundedParallel(t)
	b := newBatchFx(t)
	b.form(t)
	b.ok(t, "sess-c", "wait", "--on", "herm", "--ready", "HEAD", "--until", "12h")
	b.ok(t, "sess-a", "release", "herm", "--outcome", "pass", "--note", "landed")
	b.clock = b.clock.Add(SweepTTL + time.Hour)
	b.ok(t, "sess-a", "sweep")
	out := b.ok(t, "sess-c", "wait", "check")
	if !strings.HasPrefix(out, "LANDED: ") || !strings.Contains(out, "is no longer on record") {
		t.Fatalf("the rider is told the outcome is gone:\n%s", out)
	}
}
