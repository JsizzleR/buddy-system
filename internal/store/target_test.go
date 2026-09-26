package store

import (
	"errors"
	"strings"
	"testing"
)

// WHY THIS FILE EXISTS. `pause`, `resume` and `msg` all take one argument the
// help calls "<session|label|all>", and every one of them wrote it into the
// ledger VERBATIM. A target that named nothing produced a row that matched
// nothing, and every one of those verbs reported success. Measured on this
// machine's busiest ledger: 31 targeted inbox messages, 16 never delivered, and
// 8 of those addressed in forms the delivery query cannot match — five bare
// short ids and three CLAIM SLUGS. The slug case is the one that matters, since
// the project's own measurement (D-009) is that peers address each other by
// slug and never by identity.
//
// The same namespace is the operator's brake. `pause <slug>` printed "takes
// effect on their next mutating tool call" and paused nobody.

func TestResolveTargetAcceptsEveryAdvertisedForm(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "1111aaaa-2222-3333-4444-555566667777", "", "/tmp/repo")
	if err := st.Claim(a.SessionID, a.Incarnation, "router-work", "edge cap", []string{"internal/router"}); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, in, wantVia string
	}{
		{"full session id", a.SessionID, "id"},
		{"label", a.Label, "label"},
		{"short id from the label", "s-1111aaaa", "short"},
		{"open claim slug", "router-work", "slug"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := st.ResolveTarget(tc.in)
			if err != nil {
				t.Fatalf("ResolveTarget(%q): %v", tc.in, err)
			}
			if got.ID != a.SessionID {
				t.Errorf("ResolveTarget(%q).ID = %q, want %q", tc.in, got.ID, a.SessionID)
			}
			if got.Via != tc.wantVia {
				t.Errorf("ResolveTarget(%q).Via = %q, want %q", tc.in, got.Via, tc.wantVia)
			}
		})
	}
}

func TestResolveTargetKeepsAllReserved(t *testing.T) {
	st, _ := openTest(t)
	hello(t, st, "sess-a", "alpha", "/tmp/repo")

	got, err := st.ResolveTarget("all")
	if err != nil {
		t.Fatalf("ResolveTarget(all): %v", err)
	}
	if got.ID != "all" || got.Via != "all" {
		t.Fatalf("ResolveTarget(all) = %+v, want the reserved fleet target", got)
	}
}

// THE CENTRAL REFUSAL. Before this, an unresolvable target was written to the
// ledger and reported as success.
func TestResolveTargetRefusesWhatNamesNothing(t *testing.T) {
	st, _ := openTest(t)
	hello(t, st, "sess-a", "alpha", "/tmp/repo")

	// POSITIVE CONTROL first: without it, "it refused" and "the resolver never
	// ran" are the same observation.
	if _, err := st.ResolveTarget("alpha"); err != nil {
		t.Fatalf("control: ResolveTarget(alpha) must resolve, got %v", err)
	}

	for _, in := range []string{"s-deadbeef", "no-such-slug", "bravo", ""} {
		_, err := st.ResolveTarget(in)
		if !errors.Is(err, ErrNoSuchTarget) {
			t.Errorf("ResolveTarget(%q) error = %v, want ErrNoSuchTarget", in, err)
		}
	}
}

// A RELEASED claim's slug must not resolve. The slug is only unique among OPEN
// claims (the claims_open_slug partial index), so resolving a released one
// could name any of the sessions that ever held it — and the answer would
// change as history accumulated.
func TestResolveTargetIgnoresReleasedClaimSlugs(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/tmp/repo")
	if err := st.Claim(a.SessionID, a.Incarnation, "router-work", "edge cap", []string{"internal/router"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ResolveTarget("router-work"); err != nil {
		t.Fatalf("control: an OPEN claim slug must resolve, got %v", err)
	}

	if err := st.Release(a.SessionID, a.Incarnation, "router-work"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ResolveTarget("router-work"); !errors.Is(err, ErrNoSuchTarget) {
		t.Fatalf("released slug resolved; want ErrNoSuchTarget, got %v", err)
	}
}

// An ambiguous short id must REFUSE, not pick. Two sessions whose ids share a
// first-8-hex prefix land in different worktrees and so carry different labels,
// but the same "s-<8hex>" short form.
func TestResolveTargetRefusesAmbiguousShortID(t *testing.T) {
	st, _ := openTest(t)
	hello(t, st, "abc12345-1111-2222-3333-444455556666", "", "/tmp/repo")
	hello(t, st, "abc12345-9999-8888-7777-666655554444", "", "/tmp/wtB")

	_, err := st.ResolveTarget("s-abc12345")
	if !errors.Is(err, ErrAmbiguousTarget) {
		t.Fatalf("ambiguous short id error = %v, want ErrAmbiguousTarget", err)
	}
	// The message has to name the candidates, or the operator cannot act on it.
	if !strings.Contains(err.Error(), "repo/s-abc12345") || !strings.Contains(err.Error(), "wtB/s-abc12345") {
		t.Errorf("ambiguity error does not name both candidates: %v", err)
	}
}

// Resolution order is most-specific-first and must be DETERMINISTIC: a claim
// slug that happens to equal another session's label is the label's session.
func TestResolveTargetPrefersLabelOverSlug(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/tmp/repo")
	b := hello(t, st, "sess-b", "bravo", "/tmp/wtB")
	// A takes a claim whose slug collides with B's label.
	if err := st.Claim(a.SessionID, a.Incarnation, "bravo", "confusing", []string{"internal/x"}); err != nil {
		t.Fatal(err)
	}

	got, err := st.ResolveTarget("bravo")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != b.SessionID || got.Via != "label" {
		t.Fatalf("ResolveTarget(bravo) = %+v, want B by label", got)
	}
}

// Pause writes the RESOLVED id, so PausedFor keeps matching on (id, label,
// "all") and the gate's hot path is untouched.
func TestPauseBySlugReachesTheOwner(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/tmp/repo")
	b := hello(t, st, "sess-b", "bravo", "/tmp/wtB")
	if err := st.Claim(a.SessionID, a.Incarnation, "router-work", "edge cap", []string{"internal/router"}); err != nil {
		t.Fatal(err)
	}

	tgt, err := st.ResolveTarget("router-work")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Pause(tgt, "hold the router"); err != nil {
		t.Fatal(err)
	}

	note, paused, err := st.PausedFor(a.SessionID, a.Label)
	if err != nil {
		t.Fatal(err)
	}
	if !paused || note != "hold the router" {
		t.Fatalf("slug-targeted pause did not reach its owner: paused=%v note=%q", paused, note)
	}
	// And it must not spill onto the peer.
	if _, paused, err := st.PausedFor(b.SessionID, b.Label); err != nil || paused {
		t.Fatalf("pause leaked to bravo: paused=%v err=%v", paused, err)
	}
}

// A pause row written before targets were resolved carries a raw label. Resume
// must still clear it, or this change strands controls it cannot see.
func TestResumeClearsLegacyRawTargetRows(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/tmp/repo")

	// Exactly what the old Pause wrote: the operator's literal argument.
	if _, err := st.db.Exec(`INSERT INTO controls (kind, target, note, created) VALUES ('pause',?,?,?)`,
		a.Label, "legacy hold", st.now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, paused, _ := st.PausedFor(a.SessionID, a.Label); !paused {
		t.Fatal("control: the legacy row should pause via the label arm")
	}

	tgt, err := st.ResolveTarget(a.Label)
	if err != nil {
		t.Fatal(err)
	}
	n, err := st.Resume(tgt)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("Resume cleared %d legacy rows, want 1", n)
	}
	if _, paused, _ := st.PausedFor(a.SessionID, a.Label); paused {
		t.Fatal("legacy pause row survived resume")
	}
}

func TestMsgBySlugIsDeliverableToTheOwner(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/tmp/repo")
	if err := st.Claim(a.SessionID, a.Incarnation, "router-work", "edge cap", []string{"internal/router"}); err != nil {
		t.Fatal(err)
	}

	tgt, err := st.ResolveTarget("router-work")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Msg(tgt, "ana", "release the router please"); err != nil {
		t.Fatal(err)
	}

	msgs, err := st.Undelivered(a.SessionID, a.Label)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Body != "release the router please" {
		t.Fatalf("slug-targeted message not deliverable to its owner: %+v", msgs)
	}
}

// ---------------------------------------------------------------------------
// Defects found by the Codex code pass on this change, each reproduced here
// before it was fixed. Codex cannot build or test, so a finding is not evidence
// until a test has been watched to fail.
// ---------------------------------------------------------------------------

// C-1. Labels are NOT unique — there is no index on sessions.label and Hello
// never checks one, so `hello --label alpha` twice is legal. Resolving to
// LIMIT 1 then silently picks one of them. It is also a behaviour REGRESSION:
// the old code stored the label, and PausedFor's label arm matched BOTH.
func TestResolveTargetRefusesADuplicatedLabel(t *testing.T) {
	st, _ := openTest(t)
	hello(t, st, "sess-a", "alpha", "/tmp/repo")
	hello(t, st, "sess-b", "alpha", "/tmp/wtB")

	_, err := st.ResolveTarget("alpha")
	if !errors.Is(err, ErrAmbiguousTarget) {
		t.Fatalf("a label held by two sessions must be ambiguous, got %v", err)
	}
	if !strings.Contains(err.Error(), "sess-a") || !strings.Contains(err.Error(), "sess-b") {
		t.Errorf("the ambiguity must name both sessions: %v", err)
	}
}

// C-2/C-5. The short-id arm matched with SQL LIKE, whose metacharacters were
// never escaped and whose default collation folds ASCII case. Both were
// verified against sqlite directly: 'repo/s-1111aaaa' LIKE '%/s-1111aaa_' is 1,
// and so is LIKE '%/s-1111AAAA'. So a claim slug of the form "s-1111aaa_" was
// captured by the SHORT arm — which runs first — and pointed at a different
// session entirely, and a miscased short id resolved though this file documents
// matching as exact and never folded.
func TestShortIDArmIsLiteralAndCaseExact(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "1111aaaa-2222-3333-4444-555566667777", "", "/tmp/repo")
	b := hello(t, st, "sess-b", "bravo", "/tmp/wtB")
	// B owns a slug that is a LIKE pattern matching A's label.
	if err := st.Claim(b.SessionID, b.Incarnation, "s-1111aaa_", "wildcard slug", []string{"internal/x"}); err != nil {
		t.Fatal(err)
	}

	// Control: the literal short id still resolves to A.
	if got, err := st.ResolveTarget("s-1111aaaa"); err != nil || got.ID != a.SessionID {
		t.Fatalf("control: s-1111aaaa must resolve to A, got %+v err=%v", got, err)
	}

	got, err := st.ResolveTarget("s-1111aaa_")
	if err != nil {
		t.Fatalf("a slug shaped like a short id must still resolve: %v", err)
	}
	if got.ID != b.SessionID {
		t.Errorf("underscore was treated as a wildcard: resolved to %q, want B (%q) by slug", got.ID, b.SessionID)
	}

	if _, err := st.ResolveTarget("s-1111AAAA"); !errors.Is(err, ErrNoSuchTarget) {
		t.Errorf("matching is documented as case-exact; s-1111AAAA resolved anyway (err=%v)", err)
	}
}

// C-3. Storing the CANONICAL id can widen who a row reaches, because the
// readers still match on label: if B's LABEL equals A's ID, a pause written
// against A's id matches B's label arm too. The old code stored "alpha" and hit
// only A. Refusing is the cheap correct answer — the alternative is migrating
// every legacy row and dropping the readers' label arm.
func TestResolveTargetRefusesWhenTheCanonicalIDIsAnotherSessionsLabel(t *testing.T) {
	st, _ := openTest(t)
	hello(t, st, "sess-a", "alpha", "/tmp/repo")
	hello(t, st, "sess-b", "sess-a", "/tmp/wtB") // B's label IS A's id

	_, err := st.ResolveTarget("alpha")
	if !errors.Is(err, ErrAmbiguousTarget) {
		t.Fatalf("resolving to an id that is another session's label must refuse, got %v", err)
	}
}

// C-4. Resume must clear a legacy raw row no matter WHICH alias is used to
// resume, or a pause taken before this change is liftable only by retyping the
// exact string it was taken with.
func TestResumeClearsALegacyRowThroughAnyAlias(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/tmp/repo")
	if err := st.Claim(a.SessionID, a.Incarnation, "router-work", "edge cap", []string{"internal/router"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`INSERT INTO controls (kind, target, note, created) VALUES ('pause',?,?,?)`,
		"alpha", "legacy hold", st.now().Unix()); err != nil {
		t.Fatal(err)
	}

	// Resume by the SLUG, not by the label the row carries.
	n, err := st.Resume(mustResolve(t, st, "router-work"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("resume by slug cleared %d legacy rows, want 1", n)
	}
	if _, paused, _ := st.PausedFor(a.SessionID, a.Label); paused {
		t.Fatal("legacy pause survived a resume through a different alias")
	}
}

// C-6. "The compiler is the guard" was overstated: Target's fields are
// exported, so a hand-built or zero value is a legal argument and would insert
// an unresolved target. The type stops a STRING; only a marker only
// ResolveTarget can set stops a forged Target.
func TestUnresolvedTargetIsRefusedAtTheWriteBoundary(t *testing.T) {
	st, _ := openTest(t)
	hello(t, st, "sess-a", "alpha", "/tmp/repo")

	for _, tc := range []struct {
		name string
		tgt  Target
	}{
		{"hand-built", Target{Raw: "nope", ID: "no-such-target", Via: "id"}},
		{"zero value", Target{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := st.Pause(tc.tgt, "hold"); err == nil {
				t.Error("Pause accepted an unresolved Target")
			}
			if err := st.Msg(tc.tgt, "ana", "hi"); err == nil {
				t.Error("Msg accepted an unresolved Target")
			}
			if _, err := st.Resume(tc.tgt); err == nil {
				t.Error("Resume accepted an unresolved Target")
			}
		})
	}
	// POSITIVE CONTROL: a properly resolved Target still writes.
	if err := st.Pause(mustResolve(t, st, "alpha"), "real"); err != nil {
		t.Fatalf("control: a resolved Target must be accepted, got %v", err)
	}
}
