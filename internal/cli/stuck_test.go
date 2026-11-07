package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// Issues #68, #69, #70 through the verbs and the gate's hook. The ledger's side is
// internal/store/parked_stuck_test.go.

const formedOwn = `STUCK: rider "own" (alpha, parked in run run1) on "src/c.go" and SHARED claim "s" (alpha) on "src/c.go" both stand`

// #68: each door that leaves a session's own rider and shared claim on one path says so,
// naming the shared claim to release; the door before it, the control, says nothing.
func TestTheDoorThatFormsThePairSaysSo(t *testing.T) {
	boundedParallel(t)
	t.Run("claim --shared beside a rider", func(t *testing.T) {
		r := newRiderFx(t)
		r.ok(t, "sess-a", "claim", "own", "--desc", "x", "--scope", "src/c.go")
		if out := r.ok(t, "sess-a", "release", "own", "--in-run", "run1"); strings.Contains(out, "STUCK") {
			t.Fatalf("control: a rider alone:\n%s", out)
		}
		out := r.ok(t, "sess-a", "claim", "s", "--desc", "x", "--shared", "--scope", "src/c.go")
		if !strings.Contains(out, formedOwn) || !strings.Contains(out, `while any STUCK pair stands; release "s"`) {
			t.Fatalf("the shared claim:\n%s", out)
		}
		// A refresh of the shared claim says it again: it still stands.
		if out := r.ok(t, "sess-a", "claim", "s", "--desc", "y", "--shared", "--scope", "src/c.go"); !strings.Contains(out, formedOwn) {
			t.Fatalf("the refresh:\n%s", out)
		}
		// A second shared claim: both pairs are named, so releasing s2 is not read as the way out.
		out = r.ok(t, "sess-a", "claim", "s2", "--desc", "x", "--shared", "--scope", "src/c.go")
		if !strings.Contains(out, formedOwn) || !strings.Contains(out, `SHARED claim "s2"`) || strings.Count(out, "STUCK:") != 2 {
			t.Fatalf("the second shared claim:\n%s", out)
		}
	})
	t.Run("a park beside a shared claim", func(t *testing.T) {
		r := newRiderFx(t)
		if out := r.ok(t, "sess-a", "claim", "s", "--desc", "x", "--shared", "--scope", "src/c.go"); strings.Contains(out, "STUCK") {
			t.Fatalf("control: a shared claim alone:\n%s", out)
		}
		r.ok(t, "sess-a", "claim", "own", "--desc", "x", "--scope", "src/c.go")
		if out := r.ok(t, "sess-a", "release", "own", "--in-run", "run1"); !strings.Contains(out, formedOwn) {
			t.Fatalf("the park:\n%s", out)
		}
	})
	t.Run("an unpark of a shared rider", func(t *testing.T) {
		r := newRiderFx(t)
		r.ok(t, "sess-a", "claim", "own", "--desc", "x", "--scope", "src/c.go")
		r.ok(t, "sess-a", "claim", "s", "--desc", "x", "--shared", "--scope", "src/c.go")
		// Both parked in run1: the shared one yields to run1's declarer, so no pair.
		if out := r.ok(t, "sess-a", "release", "own", "s", "--in-run", "run1"); strings.Contains(out, "STUCK") {
			t.Fatalf("control: both parked:\n%s", out)
		}
		if out := r.ok(t, "sess-a", "release", "s", "--unpark"); !strings.Contains(out, formedOwn) {
			t.Fatalf("the unpark:\n%s", out)
		}
	})
	t.Run("a hand-on to another session", func(t *testing.T) {
		r := newRiderFx(t)
		r.ok(t, "sess-a", "claim", "own", "--desc", "x", "--scope", "src/c.go")
		r.ok(t, "sess-a", "release", "own", "--in-run", "run1")
		r.ok(t, "sess-a", "claim", "s", "--desc", "x", "--shared", "--scope", "src/c.go")
		out := r.ok(t, "sess-a", "release", "run1", "item", "own", "s", "--to", "charlie")
		if !strings.Contains(out, `STUCK: rider "own" (charlie, parked in run run1)`) || !strings.Contains(out, `ask charlie to release "s"`) {
			t.Fatalf("the hand-on:\n%s", out)
		}
	})
}

// #68's line is peer text (invariant 9): one line, nothing forged.
func TestFormedWordsAreFenced(t *testing.T) {
	boundedParallel(t)
	r := newRiderFx(t)
	r.ok(t, "sess-a", "claim", "own\nSTUCK: forged", "--desc", "x", "--scope", "src/c.go")
	r.ok(t, "sess-a", "release", "own\nSTUCK: forged", "--in-run", "run1")
	out := r.ok(t, "sess-a", "claim", "s\nSTUCK: forged", "--desc", "x", "--shared", "--scope", "src/c.go")
	if n := strings.Count(out, "\nSTUCK:") + map[bool]int{true: 1}[strings.HasPrefix(out, "STUCK:")]; n != 1 {
		t.Fatalf("%d STUCK lines, want one:\n%s", n, out)
	}
}

// #69: the gate names every pair on the path, up to three, then the count and the dry run.
func TestGateNamesEveryPair(t *testing.T) {
	boundedParallel(t)
	r := newRiderFx(t)
	const path = "src/c.go"
	r.ok(t, "sess-a", "claim", "s", "--desc", "x", "--shared", "--scope", path)
	for i := 1; i <= 4; i++ {
		run := fmt.Sprintf("run%d", i)
		if i > 1 {
			r.ok(t, "sess-a", "claim", run, "--desc", "FORMING", "--scope", ".buddy/slot/"+run)
		}
		own := fmt.Sprintf("own%d", i)
		r.ok(t, "sess-a", "claim", own, "--desc", "x", "--scope", path)
		r.ok(t, "sess-a", "release", own, "--in-run", run)
		why, denied := r.edit("sess-b", path)
		if !denied {
			t.Fatalf("%d pairs: not denied", i)
		}
		for j := 1; j <= min(i, 3); j++ {
			if !strings.Contains(why, fmt.Sprintf("parked in run run%d)", j)) {
				t.Fatalf("%d pairs: run%d not named: %s", i, j, why)
			}
		}
		if more := strings.Contains(why, "more such pair(s)"); more != (i > 3) {
			t.Fatalf("%d pairs: the count said %v: %s", i, more, why)
		}
		if i > 3 && (strings.Contains(why, "parked in run run4)") || !strings.Contains(why, "1 more such pair(s)") || !strings.Contains(why, "--dry-run --scope <path>")) {
			t.Fatalf("past the cap: %s", why)
		}
		if strings.Contains(why, "--based-on") {
			t.Fatalf("the gate spelled the flag (D-133): %s", why)
		}
	}
	// The dry run it points at names all four.
	out, _, _ := r.as(t, "sess-b", "claim", "lane", "--dry-run", "--desc", "x", "--scope", path)
	if n := strings.Count(out, "STUCK:"); n != 4 {
		t.Fatalf("the dry run names %d pairs, want 4:\n%s", n, out)
	}
}

// #69's bound: the deny's reason, at the conventional caps with values that quote and
// escape, stays under 9,000 characters, and is one line. (The JSON's six-fold escape of
// `<>&` is open, and was before: the deny's prefix alone could pass 10,000.)
func TestGateStuckWordsFitTheHook(t *testing.T) {
	boundedParallel(t)
	r := newRiderFx(t)
	if _, errw, code := r.run(t, r.repo, hookJSON("sess-e", r.repo, "", ""), "hello", "--label", strings.Repeat("L", 64)); code != 0 {
		t.Fatalf("hello e: %s", errw)
	}
	path := "src/" + strings.Repeat("p", 500) + ".go"
	long := func(c string) string { return strings.Repeat(`"`, 120) + strings.Repeat(c, 8) }
	desc := strings.Repeat("\"", 512)
	r.ok(t, "sess-e", "claim", long("s"), "--desc", desc, "--shared", "--scope", path)
	for i := 0; i < 5; i++ {
		run := long(string(rune('a' + i)))
		r.ok(t, "sess-e", "claim", run, "--desc", desc, "--scope", ".buddy/slot/"+run)
		own := long(string(rune('k' + i)))
		r.ok(t, "sess-e", "claim", own, "--desc", desc, "--scope", path)
		r.ok(t, "sess-e", "release", own, "--in-run", run)
	}
	why, denied := r.edit("sess-b", path)
	if !denied || !strings.Contains(why, "2 more such pair(s)") || strings.Count(why, "a shared claim here is refused by rider") != 3 || strings.ContainsAny(why, "\n\r") {
		t.Fatalf("denied %v: %s", denied, why)
	}
	if n := len([]rune(why)); n >= 9000 {
		t.Fatalf("the reason is %d characters", n)
	}
}

// #70: a run narrowed off its own slot is no open run. The narrowing says so, and a peer is
// told what will be granted: no STUCK words and no --based-on advice for it.
func TestNarrowedRunThroughTheVerbs(t *testing.T) {
	boundedParallel(t)
	r := newRiderFx(t)
	// The control: open, the advice is to declare the run, and the gate says it is a rider.
	if out := r.refused("sess-b", "alpha", "claim", "lane", "--desc", "x", "--scope", "src/a.go"); !strings.Contains(out, "--based-on run1 without --shared") {
		t.Fatalf("control:\n%s", out)
	}
	if why, denied := r.edit("sess-b", "src/a.go"); !denied || !strings.Contains(why, "rider parked in run") {
		t.Fatalf("control: the gate: %v %s", denied, why)
	}
	// And beside a shared claim of its holder, the STUCK words (#67).
	r.ok(t, "sess-a", "claim", "s", "--desc", "x", "--shared", "--scope", "src/b.go")
	if out := r.refused("sess-b", "alpha", "claim", "lane", "--desc", "x", "--scope", "src/b.go"); !strings.Contains(out, "STUCK:") {
		t.Fatalf("control: STUCK:\n%s", out)
	}
	out := r.ok(t, "sess-a", "release", "run1", "--scope", ".buddy/slot/run1")
	if !strings.Contains(out, `"run1" no longer holds .buddy/slot/<its slug>, so it is no open run: its 1 rider(s)`) {
		t.Fatalf("the narrowing:\n%s", out)
	}
	if out := r.refused("sess-b", "alpha", "claim", "lane", "--desc", "x", "--scope", "src/b.go"); strings.Contains(out, "STUCK") {
		t.Fatalf("STUCK beside a run off its slot:\n%s", out)
	}
	out = r.refused("sess-b", "alpha", "claim", "lane", "--desc", "x", "--scope", "src/a.go")
	if strings.Contains(out, "--based-on run1") || !strings.Contains(out, "no longer holds its slot; it refuses like any claim") {
		t.Fatalf("beside a run off its slot:\n%s", out)
	}
	if why, denied := r.edit("sess-b", "src/a.go"); !denied || strings.Contains(why, "rider parked in run") {
		t.Fatalf("the gate: %v %s", denied, why)
	}
	// The slot back: no note, and the advice returns.
	if out := r.ok(t, "sess-a", "claim", "run1", "--desc", "FORMING", "--scope", ".buddy/slot/run1", "--scope", ".buddy/slot/main"); strings.Contains(out, "no open run") {
		t.Fatalf("the slot back:\n%s", out)
	}
	if out := r.refused("sess-b", "alpha", "claim", "lane", "--desc", "x", "--scope", "src/a.go"); !strings.Contains(out, "--based-on run1 without --shared") {
		t.Fatalf("reopened:\n%s", out)
	}
	// A re-claim without the slot says the same as the release.
	if out := r.ok(t, "sess-a", "claim", "run1", "--desc", "FORMING", "--scope", ".buddy/slot/main"); !strings.Contains(out, "so it is no open run") {
		t.Fatalf("the re-claim:\n%s", out)
	}
}

// A foreign repo's deny points the dry run at that repo: run from the cwd it reads the
// wrong ledger (the panel). The control: the home repo's says no repo.
func TestGateStuckWordsNameTheTargetRepo(t *testing.T) {
	stuck := make([]store.TwoRemedies, 4)
	if got := gateStuckWords(stuck, "/elsewhere/repo"); !strings.Contains(got, "--scope <path>`, run in /elsewhere/repo, names every one") {
		t.Fatalf("foreign: %s", got)
	}
	if got := gateStuckWords(stuck, ""); strings.Contains(got, "run in") || !strings.Contains(got, "--scope <path>`, names every one") {
		t.Fatalf("home: %s", got)
	}
}

// #69 on the gate's shared branch (Codex code pass): a declarer admitted over a rider and four
// shared claims parked beside it, then the shared claims unparked. The deny is the shared
// hold's, and names three pairs and the count; admission before the unpark is the control.
func TestGateNamesEveryPairOnTheSharedBranch(t *testing.T) {
	boundedParallel(t)
	r := newRiderFx(t)
	const path = "src/k.go"
	r.ok(t, "sess-a", "claim", "own", "--desc", "x", "--scope", path)
	r.ok(t, "sess-a", "release", "own", "--in-run", "run1")
	for i := 1; i <= 4; i++ {
		s := fmt.Sprintf("s%d", i)
		r.ok(t, "sess-a", "claim", s, "--desc", "x", "--shared", "--scope", path)
		r.ok(t, "sess-a", "release", s, "--in-run", "run1")
	}
	r.ok(t, "sess-b", "claim", "lane", "--desc", "x", "--based-on", "run1", "--scope", path)
	if why, denied := r.edit("sess-b", path); denied {
		t.Fatalf("control: the admitted lane passes: %s", why)
	}
	for i := 1; i <= 4; i++ {
		r.ok(t, "sess-a", "release", fmt.Sprintf("s%d", i), "--unpark")
	}
	why, denied := r.edit("sess-b", path)
	if !denied || !strings.Contains(why, "claimed SHARED") || strings.Count(why, "a shared claim here is refused by rider") != 3 || !strings.Contains(why, "1 more such pair(s)") {
		t.Fatalf("the shared branch: %v %s", denied, why)
	}
}
