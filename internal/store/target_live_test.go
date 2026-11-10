package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// #80. Target resolution matched labels over EVERY session row, ended ones
// included, before it tried claim slugs, and session rows are never deleted.
// Two wrong behaviours followed in the ordinary fleet pattern of a lane
// handing its slug to a successor and saying bye:
//
//	(a) the ended lane's label equals the slug its successor now holds, and
//	    `pause <slug>` / `msg <slug>` reached the DEAD lane via its label;
//	(b) one ended and one live session wear a label, and `pause <label>` was
//	    refused as ambiguous for good, though only one of them can act.
//
// The fix resolves among live sessions and open slugs held by live sessions
// first, and falls back to ended rows only when nothing live matches. The
// positive controls below are what keep it from collapsing into "ended rows
// never resolve" (which would silently refuse a message to a just-ended lane)
// or "pick one" (which would let a live twin label through).

// handOn is the fleet pattern: the lane wearing label `lane` claims slug
// `lane`, hands it to its successor, and says bye.
func handOn(t *testing.T, st *Store, lane string) (old, next SessionInfo) {
	t.Helper()
	old = hello(t, st, "sess-old", lane, "/tmp/wt-old")
	next = hello(t, st, "sess-next", lane+"-2", "/tmp/wt-next")
	if err := st.Claim(old.SessionID, old.Incarnation, lane, "the item", []string{"internal/x"}); err != nil {
		t.Fatal(err)
	}
	to, err := st.ResolveTarget(next.Label)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.TransferClaim(old.SessionID, old.Incarnation, lane, to); err != nil {
		t.Fatal(err)
	}
	if err := st.Bye(old.SessionID, old.Incarnation); err != nil {
		t.Fatal(err)
	}
	return old, next
}

// (a): the slug's live holder, not the ended session wearing the same label.
func TestResolveTargetPrefersALiveSlugHolderToAnEndedLabel(t *testing.T) {
	st, _ := openTest(t)
	old, next := handOn(t, st, "fix-74")

	got, err := st.ResolveTarget("fix-74")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != next.SessionID || got.Via != "slug" || !got.Live {
		t.Fatalf("ResolveTarget(fix-74) = %+v, want the live slug holder %s via slug", got, next.SessionID)
	}

	// And the brake lands where the work is: the successor is paused, and
	// the row does not name the dead lane.
	if err := st.Pause(got, "stop"); err != nil {
		t.Fatal(err)
	}
	if _, paused, err := st.PausedFor(next.SessionID, next.Label); err != nil || !paused {
		t.Fatalf("pause by slug did not reach the live holder: paused=%v err=%v", paused, err)
	}
	if _, paused, err := st.PausedFor(old.SessionID, old.Label); err != nil || paused {
		t.Fatalf("pause by slug reached the ended lane: paused=%v err=%v", paused, err)
	}
}

// (b): one ended and one live session wear a label; the live one answers.
func TestResolveTargetPrefersTheLiveTwinOfAnEndedLabel(t *testing.T) {
	st, _ := openTest(t)
	// TWO ended twins: an implementation that decided ambiguity before it
	// split the tiers would refuse here (refusal lens).
	for _, id := range []string{"sess-gone", "sess-gone-2"} {
		gone := hello(t, st, id, "alpha", "/tmp/"+id)
		if err := st.Bye(gone.SessionID, gone.Incarnation); err != nil {
			t.Fatal(err)
		}
	}
	live := hello(t, st, "sess-live", "alpha", "/tmp/wtB")

	got, err := st.ResolveTarget("alpha")
	if err != nil {
		t.Fatalf("a label one live session wears must resolve past its ended twin: %v", err)
	}
	if got.ID != live.SessionID || got.Via != "label" || !got.Live {
		t.Fatalf("ResolveTarget(alpha) = %+v, want %s via label, live", got, live.SessionID)
	}
}

// Positive control for (b): two LIVE sessions wearing one label are still
// ambiguous, an ended third beside them changes nothing, and the refusal
// names the live candidates only (the ended one is not a remedy).
func TestResolveTargetStillRefusesALiveTwinLabel(t *testing.T) {
	st, _ := openTest(t)
	gone := hello(t, st, "sess-gone", "alpha", "/tmp/wtA")
	if err := st.Bye(gone.SessionID, gone.Incarnation); err != nil {
		t.Fatal(err)
	}
	hello(t, st, "sess-b", "alpha", "/tmp/wtB")
	hello(t, st, "sess-c", "alpha", "/tmp/wtC")

	_, err := st.ResolveTarget("alpha")
	if !errors.Is(err, ErrAmbiguousTarget) {
		t.Fatalf("two live sessions wearing one label must refuse as ambiguous, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "sess-b") || !strings.Contains(msg, "sess-c") || !strings.Contains(msg, "names 2 sessions") {
		t.Errorf("the refusal must name both live candidates: %v", msg)
	}
	if strings.Contains(msg, "sess-gone") {
		t.Errorf("the refusal named the ended session as a candidate: %v", msg)
	}
	if !strings.Contains(msg, "use a full session id") {
		t.Errorf("the refusal must name the remedy: %v", msg)
	}
}

// Positive control for the fallback: a just-ended lane's label still resolves
// when nothing live matches, so `msg` to it is queued (and warned) rather than
// silently refused, as D-013 decided.
func TestResolveTargetFallsBackToAnEndedLabel(t *testing.T) {
	st, _ := openTest(t)
	gone := hello(t, st, "sess-gone", "alpha", "/tmp/wtA")
	hello(t, st, "sess-other", "bravo", "/tmp/wtB")
	if err := st.Bye(gone.SessionID, gone.Incarnation); err != nil {
		t.Fatal(err)
	}

	got, err := st.ResolveTarget("alpha")
	if err != nil {
		t.Fatalf("an ended lane's label must still resolve when nothing live matches: %v", err)
	}
	if got.ID != gone.SessionID || got.Via != "label" || got.Live {
		t.Fatalf("ResolveTarget(alpha) = %+v, want the ended %s via label, not live", got, gone.SessionID)
	}
	// And the send is QUEUED against its id, not refused.
	if err := st.Msg(got, "operator", "are you back?"); err != nil {
		t.Fatalf("msg to a just-ended lane must queue, got %v", err)
	}
	msgs, err := st.Undelivered(gone.SessionID, gone.Label)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Body != "are you back?" {
		t.Fatalf("the ended lane's queue = %+v, want the one message", msgs)
	}
}

// The fallback keeps its own ambiguity: two ENDED sessions wearing a label,
// nothing live, still refuse rather than pick one.
func TestResolveTargetRefusesEndedTwinsWithNothingLive(t *testing.T) {
	st, _ := openTest(t)
	for _, id := range []string{"sess-a", "sess-b"} {
		si := hello(t, st, id, "alpha", "/tmp/"+id)
		if err := st.Bye(si.SessionID, si.Incarnation); err != nil {
			t.Fatal(err)
		}
	}
	_, err := st.ResolveTarget("alpha")
	if !errors.Is(err, ErrAmbiguousTarget) {
		t.Fatalf("two ended sessions wearing one label must refuse as ambiguous, got %v", err)
	}
	for _, want := range []string{"sess-a", "sess-b", "use a full session id"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the ended-tier refusal must name %q: %v", want, err)
		}
	}
}

// The exact session id still resolves first, ended or not: it is the remedy
// every ambiguity names, so a live slug spelled like it must not capture it.
func TestResolveTargetEndedSessionIDStillWinsFirst(t *testing.T) {
	st, _ := openTest(t)
	gone := hello(t, st, "sess-gone", "alpha", "/tmp/wtA")
	live := hello(t, st, "sess-live", "bravo", "/tmp/wtB")
	if err := st.Claim(live.SessionID, live.Incarnation, "sess-gone", "a slug spelled like an id", []string{"internal/x"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Bye(gone.SessionID, gone.Incarnation); err != nil {
		t.Fatal(err)
	}
	got, err := st.ResolveTarget("sess-gone")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != gone.SessionID || got.Via != "id" {
		t.Fatalf("ResolveTarget(sess-gone) = %+v, want the exact id first", got)
	}
}

// The tiers keep their old order INSIDE themselves: a live label still beats
// a live slug holder (TestResolveTargetPrefersLabelOverSlug), and a live short
// form beats an ended session whose label is spelled like it.
func TestResolveTargetLiveShortFormBeatsAnEndedLabel(t *testing.T) {
	st, _ := openTest(t)
	gone := hello(t, st, "sess-gone", "s-1111aaaa", "/tmp/wtA")
	if err := st.Bye(gone.SessionID, gone.Incarnation); err != nil {
		t.Fatal(err)
	}
	live := hello(t, st, "1111aaaa-2222-3333-4444-555566667777", "", "/tmp/repo")

	got, err := st.ResolveTarget("s-1111aaaa")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != live.SessionID || got.Via != "short" {
		t.Fatalf("ResolveTarget(s-1111aaaa) = %+v, want the live session via short", got)
	}
}

// An open slug whose holder has ENDED (not yet swept) is an ended row: a live
// session wearing that string as its label wins, and with nothing live the
// slug still resolves to its ended holder rather than refusing.
func TestResolveTargetOpenSlugOfAnEndedHolderIsTheFallback(t *testing.T) {
	st, _ := openTest(t)
	gone := hello(t, st, "sess-gone", "alpha", "/tmp/wtA")
	if err := st.Claim(gone.SessionID, gone.Incarnation, "the-item", "left open", []string{"internal/x"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Bye(gone.SessionID, gone.Incarnation); err != nil {
		t.Fatal(err)
	}

	got, err := st.ResolveTarget("the-item")
	if err != nil {
		t.Fatalf("an open slug of an ended holder must still resolve when nothing live matches: %v", err)
	}
	if got.ID != gone.SessionID || got.Via != "slug" || got.Live {
		t.Fatalf("ResolveTarget(the-item) = %+v, want the ended holder via slug", got)
	}

}

// The other half: a live session wearing the slug's string as its label beats
// an ended holder's still-open slug. A CONTROL more than a tier test: label
// precedes slug in the untiered order too; the tier split itself is held by
// TestResolveTargetEndedTierKeepsLabelBeforeSlug and the tiers-reversed
// mutant. The live session helloes BEFORE the bye:
// a hello (or claim) after it orphans the ended holder's claim, and the slug
// would no longer compete (Codex code pass on this test's first version).
func TestResolveTargetLiveLabelBeatsAnEndedHoldersSlug(t *testing.T) {
	st, _ := openTest(t)
	gone := hello(t, st, "sess-gone", "alpha", "/tmp/wtA")
	if err := st.Claim(gone.SessionID, gone.Incarnation, "the-item", "left open", []string{"internal/x"}); err != nil {
		t.Fatal(err)
	}
	live := hello(t, st, "sess-live", "the-item", "/tmp/wtB")
	if err := st.Bye(gone.SessionID, gone.Incarnation); err != nil {
		t.Fatal(err)
	}
	got, err := st.ResolveTarget("the-item")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != live.SessionID || got.Via != "label" {
		t.Fatalf("ResolveTarget(the-item) = %+v, want the live label over an ended holder's slug", got)
	}
}

// Inside the ENDED tier D-013's order still holds: an ended session's label
// beats an open slug an ended holder left behind. Without the tier split on
// the slug arm, the live pass would take that slug before the ended labels
// were ever read.
func TestResolveTargetEndedTierKeepsLabelBeforeSlug(t *testing.T) {
	st, _ := openTest(t)
	holder := hello(t, st, "sess-holder", "alpha", "/tmp/wtA")
	if err := st.Claim(holder.SessionID, holder.Incarnation, "x", "left open", []string{"internal/x"}); err != nil {
		t.Fatal(err)
	}
	wearer := hello(t, st, "sess-wearer", "x", "/tmp/wtB")
	for _, si := range []SessionInfo{holder, wearer} {
		if err := st.Bye(si.SessionID, si.Incarnation); err != nil {
			t.Fatal(err)
		}
	}
	// No hello after the byes: hello orphans an ended holder's open claims,
	// and then the slug arm this test guards would never run (the mutation
	// run caught exactly that in this test's first version).

	got, err := st.ResolveTarget("x")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != wearer.SessionID || got.Via != "label" {
		t.Fatalf("ResolveTarget(x) = %+v, want the ended label wearer before the ended holder's slug", got)
	}
}

// The id/label collision check reads ENDED rows too: an ended session
// revives under its own id and label, and a pause stored against an id that
// is its label would brake it then (Codex design pass, Q6).
func TestResolveTargetCollisionCheckReadsEndedLabels(t *testing.T) {
	st, _ := openTest(t)
	hello(t, st, "sess-a", "alpha", "/tmp/wtA")
	b := hello(t, st, "sess-b", "sess-a", "/tmp/wtB")
	if err := st.Bye(b.SessionID, b.Incarnation); err != nil {
		t.Fatal(err)
	}
	_, err := st.ResolveTarget("alpha")
	if !errors.Is(err, ErrAmbiguousTarget) || !strings.Contains(err.Error(), "also the label of sess-b") {
		t.Fatalf("an ended session whose label is the resolved id must still refuse the collision, got %v", err)
	}
	// The full id refuses alike, so the refusal must not offer it as the
	// remedy (refusal lens). sess-a is live, so `all` does reach it.
	_, err = st.ResolveTarget("sess-a")
	if !errors.Is(err, ErrAmbiguousTarget) {
		t.Fatalf("the full id collides too, got %v", err)
	}
	if msg := err.Error(); strings.Contains(msg, "use a full session id") || !strings.Contains(msg, "must wear another label") || !strings.Contains(msg, "`all` reaches it") {
		t.Errorf("the collision refusal must name the remedies that work for a live target: %v", err)
	}
}

// The same collision with the RESOLVED session ended: `all` reaches live
// sessions only, so the refusal must not offer it (Codex code pass).
func TestResolveTargetCollisionOnAnEndedTargetOffersNoBroadcast(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/tmp/wtA")
	if err := st.Bye(a.SessionID, a.Incarnation); err != nil {
		t.Fatal(err)
	}
	hello(t, st, "sess-b", "sess-a", "/tmp/wtB")
	_, err := st.ResolveTarget("alpha")
	if !errors.Is(err, ErrAmbiguousTarget) {
		t.Fatalf("the collision must refuse, got %v", err)
	}
	if msg := err.Error(); strings.Contains(msg, "`all`") || !strings.Contains(msg, "sess-b must wear another label") {
		t.Errorf("an ended target's collision must not offer the broadcast: %v", err)
	}
}

// "Live" is SessionInfo.Live, "not ended", and nothing else: a session last
// seen hours ago that never said bye still answers ahead of its ended twin
// (Fable cross-check: no fixture had a stale-but-live session, so a
// staleness predicate in place of Live() passed every test).
func TestResolveTargetLiveMeansNotEndedNotRecentlySeen(t *testing.T) {
	st, clk := openTest(t)
	quiet := hello(t, st, "sess-quiet", "alpha", "/tmp/wtA")
	clk.advance(3 * time.Hour)
	gone := hello(t, st, "sess-gone", "alpha", "/tmp/wtB")
	if err := st.Bye(gone.SessionID, gone.Incarnation); err != nil {
		t.Fatal(err)
	}
	got, err := st.ResolveTarget("alpha")
	if err != nil {
		t.Fatalf("a not-ended session must answer past its ended twin however long it has been quiet: %v", err)
	}
	if got.ID != quiet.SessionID || got.Via != "label" {
		t.Fatalf("ResolveTarget(alpha) = %+v, want the quiet but not-ended %s", got, quiet.SessionID)
	}
}

// The short form is tiered too: ended and live sessions in two worktrees share
// a first-8-hex prefix, so both default labels end "/s-abc12345". The live one
// answers; with both ended, it refuses naming both (refusal lens).
func TestResolveTargetShortFormIsTiered(t *testing.T) {
	st, _ := openTest(t)
	gone := hello(t, st, "abc12345-0000-0000-0000-000000000001", "", "/tmp/wtA")
	if err := st.Bye(gone.SessionID, gone.Incarnation); err != nil {
		t.Fatal(err)
	}
	live := hello(t, st, "abc12345-0000-0000-0000-000000000002", "", "/tmp/wtB")
	got, err := st.ResolveTarget("s-abc12345")
	if err != nil {
		t.Fatalf("a short form one live session answers to must resolve past its ended twin: %v", err)
	}
	if got.ID != live.SessionID || got.Via != "short" {
		t.Fatalf("ResolveTarget(s-abc12345) = %+v, want the live session via short", got)
	}

	if err := st.Bye(live.SessionID, live.Incarnation); err != nil {
		t.Fatal(err)
	}
	_, err = st.ResolveTarget("s-abc12345")
	if !errors.Is(err, ErrAmbiguousTarget) || !strings.Contains(err.Error(), gone.SessionID) || !strings.Contains(err.Error(), live.SessionID) {
		t.Fatalf("two ended sessions sharing a short form must refuse naming both, got %v", err)
	}
}

// An ambiguity ENDS resolution inside its tier: two live wearers of `x`
// refuse even though a third live session holds the open slug `x`.
func TestResolveTargetLiveLabelAmbiguityDoesNotFallToTheSlug(t *testing.T) {
	st, _ := openTest(t)
	hello(t, st, "sess-b", "x", "/tmp/wtB")
	hello(t, st, "sess-c", "x", "/tmp/wtC")
	d := hello(t, st, "sess-d", "delta", "/tmp/wtD")
	if err := st.Claim(d.SessionID, d.Incarnation, "x", "the slug", []string{"internal/x"}); err != nil {
		t.Fatal(err)
	}
	_, err := st.ResolveTarget("x")
	if !errors.Is(err, ErrAmbiguousTarget) {
		t.Fatalf("a live label ambiguity must refuse, not fall to the slug, got %v", err)
	}
	if strings.Contains(err.Error(), "sess-d") {
		t.Errorf("the slug holder is not a label candidate: %v", err)
	}
}

// The reserved word: a session whose id is literally "all" refuses by every
// other name, and `all` itself is still the broadcast (its positive control).
func TestResolveTargetRefusesASessionWhoseIDIsAll(t *testing.T) {
	st, _ := openTest(t)
	hello(t, st, "all", "zulu", "/tmp/wtZ")
	if _, err := st.ResolveTarget("zulu"); !errors.Is(err, ErrAmbiguousTarget) {
		t.Fatalf("a session whose id is the reserved word must refuse, got %v", err)
	}
	got, err := st.ResolveTarget("all")
	if err != nil || got.Via != "all" {
		t.Fatalf("ResolveTarget(all) = %+v, %v; want the broadcast", got, err)
	}
}
