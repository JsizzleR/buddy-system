package store

import (
	"sync"
	"testing"
)

// D-096: the handoff notice is told once per (session, incarnation, step). The
// sequence is one session's life: the crossing, a turn that did not grow a
// step, a step, a skipped step, a stale lower sample, and a fall under the
// declared size. Each "false" sits next to a "true" one input away.
func TestHandoffTellOncePerStep(t *testing.T) {
	st, _ := openTest(t)
	for i, tc := range []struct {
		step int64
		want bool
	}{
		{-1, false}, // under the size: nothing to claim
		{0, true},   // the crossing
		{0, false},  // the next turn, same step: silent
		{1, true},   // grown a step
		{3, true},   // grown two at once: told at the new step
		{3, false},  // and only once
		{1, false},  // a lower sample read before step 3 was claimed: silent...
		{3, false},  // ...and it forgot nothing: step 3 is not told twice
		{2, true},   // an untold step below the highest is still its own
		{-1, false}, // under the size again
		{0, true},   // the next crossing is told
	} {
		got, err := st.HandoffTell("s1", "inc1", tc.step)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Fatalf("call %d (step %d): told=%v, want %v", i, tc.step, got, tc.want)
		}
	}
	// Bound to the incarnation and the session: another incarnation of s1, and
	// another session at the same step, are each told their own crossing.
	for _, who := range [][2]string{{"s1", "inc2"}, {"s2", "inc1"}} {
		if got, err := st.HandoffTell(who[0], who[1], 0); err != nil || !got {
			t.Fatalf("%v: told=%v err=%v, want its own first notice", who, got, err)
		}
	}
	// The register it lives in still answers for the authority watch: a
	// handoff mark is not a watched file, and a watched file's mark is not a
	// handoff step — even one whose path normalizes to a leading slash.
	if pending, err := st.AuthorityWarnPending("s1", "inc1", "CLAUDE.md", 0); err != nil || !pending {
		t.Fatalf("a handoff mark must not read as an authority warning: pending=%v err=%v", pending, err)
	}
	for _, spelled := range []string{`\handoff-at`, "handoff-at", "./handoff-at", "a//handoff-at"} {
		n, err := NormalizeScope(spelled)
		if err != nil {
			continue
		}
		if fold(n) == handoffKey {
			t.Fatalf("authority path %q normalizes to the handoff key %q", spelled, n)
		}
		if err := st.MarkAuthorityWarned("s1", "inc1", n, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := st.HandoffTell("s1", "inc1", -1); err != nil {
			t.Fatal(err)
		}
		if pending, err := st.AuthorityWarnPending("s1", "inc1", n, 0); err != nil || pending {
			t.Fatalf("forgetting the handoff steps erased the authority mark for %q: pending=%v err=%v", n, pending, err)
		}
	}
}

// Two prompts queued together run two busy hooks at once; exactly one may
// print. The claim is one transaction, so the race has one winner.
func TestHandoffTellConcurrentHasOneWinner(t *testing.T) {
	st, _ := openTest(t)
	const n = 8
	var wg sync.WaitGroup
	won := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := st.HandoffTell("s1", "inc1", 0)
			if err != nil {
				t.Error(err)
			}
			won <- got
		}()
	}
	wg.Wait()
	close(won)
	winners := 0
	for w := range won {
		if w {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("%d of %d concurrent claims were told, want exactly 1", winners, n)
	}
}
