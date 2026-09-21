package store

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The two halves of the same field report (wishlist §5b): a claim that names
// one busy path is refused WHOLE and reports ONE conflict, so a four-path
// request costs a round trip per collision; and a release is whole too, so a
// holder that has finished with two of its four scopes can only say so in
// prose, which enforcement does not read. Neither side changes D-001's
// "granted whole or refused whole": the dry run writes nothing, and the
// partial release is the holder narrowing its own reservation.

func TestClaimConflictsReportsTheWholeSetAndWritesNothing(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.Claim(a.SessionID, a.Incarnation, "router", "r", []string{"internal/router", "docs"}); err != nil {
		t.Fatal(err)
	}

	free, conflicts, _, err := st.ClaimConflicts(b.SessionID, b.Incarnation, "mine", []string{"internal/router/proxy.go", "Docs/a.md", "pkg"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(free, []string{"pkg"}) {
		t.Fatalf("free scopes: want [pkg], got %v", free)
	}
	if len(conflicts) != 2 {
		t.Fatalf("want 2 conflicts, got %+v", conflicts)
	}
	for _, c := range conflicts {
		if c.Claimant != "alpha" || c.Slug != "router" {
			t.Fatalf("conflict must name the holder and slug: %+v", c)
		}
	}
	// The refusal names the scope AS REQUESTED (the caller's spelling) and the
	// held scope AS CLAIMED, as Claim's refusal does.
	if conflicts[1].Scope != "Docs/a.md" || conflicts[1].Their != "docs" {
		t.Fatalf("spellings: %+v", conflicts[1])
	}

	// Nothing was written: the board is unchanged.
	claims, err := st.Claims(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 {
		t.Fatalf("a dry run must not write a row; board has %d claims", len(claims))
	}

	// Positive control that the free set was right: claiming exactly it
	// succeeds, and claiming the original set is refused naming BOTH conflicts.
	if err := st.Claim(b.SessionID, b.Incarnation, "mine", "m", free); err != nil {
		t.Fatalf("the reported free set must be claimable: %v", err)
	}
	err = st.Claim(b.SessionID, b.Incarnation, "mine2", "m", []string{"internal/router/proxy.go", "docs/a.md", "cmd"})
	var refused ErrRefused
	if !errors.As(err, &refused) {
		t.Fatalf("want ErrRefused, got %v", err)
	}
	if len(refused.More) != 1 {
		t.Fatalf("a refusal must carry the rest of the conflict set; got %+v", refused)
	}
	if !strings.Contains(refused.Error(), "internal/router/proxy.go") || !strings.Contains(refused.Error(), "and 1 more") {
		t.Fatalf("the refusal should count what it did not print: %q", refused.Error())
	}
	// A refusal is WHOLE: the uncontended "cmd" was not taken on the side.
	// Checked with a third session, because b re-claiming its own slug would
	// refresh a partial acquisition and hide it.
	c := hello(t, st, "sess-c", "charlie", "/wt/c")
	if err := st.Claim(c.SessionID, c.Incarnation, "cc", "c", []string{"cmd"}); err != nil {
		t.Fatalf("the refused request must have taken nothing: cmd is held: %v", err)
	}
	if claims, _ := st.Claims(true); len(claims) != 3 { // router, mine, cc — and no mine2
		t.Fatalf("board after a refusal: want 3 claims, got %d", len(claims))
	}
}

func TestClaimConflictsEnumeratesEveryHolderOfOneRequestedScope(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	c := hello(t, st, "sess-c", "charlie", "/wt/c")
	if err := st.Claim(a.SessionID, a.Incarnation, "pa", "a", []string{"pkg/a"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Claim(b.SessionID, b.Incarnation, "pb", "b", []string{"pkg/b"}); err != nil {
		t.Fatal(err)
	}
	// One requested prefix, two holders under it: both are reported, by both paths.
	_, conflicts, _, err := st.ClaimConflicts(c.SessionID, c.Incarnation, "all-pkg", []string{"pkg"})
	if err != nil {
		t.Fatal(err)
	}
	// Exact tuples, not a count: a count of two would accept the first holder
	// twice with the second omitted.
	want := map[Conflict]bool{
		{Scope: "pkg", Their: "pkg/a", Slug: "pa", Claimant: "alpha", Session: a.SessionID}: true,
		{Scope: "pkg", Their: "pkg/b", Slug: "pb", Claimant: "bravo", Session: b.SessionID}: true,
	}
	got := map[Conflict]bool{}
	for _, c := range conflicts {
		// The holder's clock is zeroed for this comparison ONLY: this test is
		// about WHICH holders are enumerated, and the tuple equality is what
		// stops a count of two from accepting the first holder twice. That the
		// clock is carried, and that the refusal and the dry run carry the
		// SAME one, is pinned in TestStaleClaimStillRefusesANewClaim.
		if c.Renewed.IsZero() {
			t.Fatal("a conflict came back with no holder clock")
		}
		c.Renewed = time.Time{}
		got[c] = true
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("want both holders as exact tuples, got %+v", conflicts)
	}
	err = st.Claim(c.SessionID, c.Incarnation, "all-pkg", "x", []string{"pkg"})
	var refused ErrRefused
	if !errors.As(err, &refused) || len(refused.More) != 1 {
		t.Fatalf("want both holders in the refusal, got %v", err)
	}
	// Slug AND scope collide: the forecast and the refusal agree on the whole
	// set, slug first. (The slug check used to return before the scope scan.)
	_, conflicts, _, err = st.ClaimConflicts(c.SessionID, c.Incarnation, "pa", []string{"pkg/a/x.go"})
	if err != nil || len(conflicts) != 2 || conflicts[0].Scope != "" || conflicts[1].Scope != "pkg/a/x.go" {
		t.Fatalf("slug+scope forecast: %+v %v", conflicts, err)
	}
	err = st.Claim(c.SessionID, c.Incarnation, "pa", "x", []string{"pkg/a/x.go"})
	if !errors.As(err, &refused) || refused.Scope != "" || len(refused.More) != 1 || refused.More[0].Scope != "pkg/a/x.go" {
		t.Fatalf("slug+scope refusal must carry both: %+v", err)
	}
	// The one-line rendering of a SLUG-first refusal counts the rest too.
	if !strings.Contains(refused.Error(), "and 1 more") {
		t.Fatalf("slug-first refusal must count the rest: %q", refused.Error())
	}
}

func TestClaimConflictsRefusesAnEndedSessionUnderItsOwnIncarnation(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.Bye(a.SessionID, a.Incarnation); err != nil {
		t.Fatal(err)
	}
	// Correct incarnation, ended session: refused. M8 proved the incarnation
	// predicate is load-bearing; this proves `ended IS NULL` is too.
	if _, _, _, err := st.ClaimConflicts(a.SessionID, a.Incarnation, "x", []string{"pkg"}); err == nil {
		t.Fatal("an ended session must not be forecast free")
	}
}

func TestClaimRefreshWithAConflictLeavesTheOriginalUntouched(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.Claim(a.SessionID, a.Incarnation, "w", "first", []string{"pkg"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Claim(b.SessionID, b.Incarnation, "bd", "b", []string{"docs"}); err != nil {
		t.Fatal(err)
	}
	before, _ := st.Claims(false)
	clk.advance(time.Minute)
	// A refresh that adds a peer-held path and a free one is refused WHOLE:
	// the original claim keeps its scopes, its description and its renewed
	// time, and the free path stays free.
	err := st.Claim(a.SessionID, a.Incarnation, "w", "second", []string{"pkg", "docs/x.md", "cmd"})
	var refused ErrRefused
	if !errors.As(err, &refused) || refused.Scope != "docs/x.md" {
		t.Fatalf("want a refusal on docs/x.md, got %v", err)
	}
	after, _ := st.Claims(false)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("a refused refresh must change nothing:\n%+v\n%+v", before, after)
	}
	c := hello(t, st, "sess-c", "charlie", "/wt/c")
	if err := st.Claim(c.SessionID, c.Incarnation, "cc", "c", []string{"cmd"}); err != nil {
		t.Fatalf("the free path must still be free: %v", err)
	}
	// And a clean refresh replaces scopes and metadata under the same claim id.
	if err := st.Claim(a.SessionID, a.Incarnation, "w", "third", []string{"pkg", "internal"}); err != nil {
		t.Fatal(err)
	}
	now, _ := st.Claims(false)
	for _, cl := range now {
		if cl.Slug == "w" {
			if cl.ClaimID != before[0].ClaimID && cl.ClaimID != before[1].ClaimID {
				t.Fatalf("refresh must keep the claim id, got %+v", cl)
			}
			if cl.Desc != "third" || !reflect.DeepEqual(cl.Scopes, []string{"internal", "pkg"}) {
				t.Fatalf("refresh must replace desc and scopes: %+v", cl)
			}
		}
	}
}

func TestClaimConflictsSlugAndLivenessArePartOfTheSet(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.Claim(a.SessionID, a.Incarnation, "router", "r", []string{"internal/router"}); err != nil {
		t.Fatal(err)
	}
	// Somebody else's slug is a conflict with no scope.
	free, conflicts, _, err := st.ClaimConflicts(b.SessionID, b.Incarnation, "router", []string{"pkg"})
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 || conflicts[0].Scope != "" || conflicts[0].Claimant != "alpha" {
		t.Fatalf("slug conflict: %+v", conflicts)
	}
	if !reflect.DeepEqual(free, []string{"pkg"}) {
		t.Fatalf("free: %v", free)
	}
	// Your own slug is a refresh, not a conflict.
	_, conflicts, _, err = st.ClaimConflicts(a.SessionID, a.Incarnation, "router", []string{"internal/router", "docs"})
	if err != nil || len(conflicts) != 0 {
		t.Fatalf("own slug/scopes must not conflict: %v %+v", err, conflicts)
	}
	// A session the ledger does not know cannot dry-run either: the answer
	// would be "free" for a claim it could never take.
	if _, _, _, err := st.ClaimConflicts("nobody", "inc", "x", []string{"pkg"}); err == nil {
		t.Fatal("unknown session must be refused, not told its scopes are free")
	}
	// A superseded incarnation is refused too: the write would be, and a
	// forecast of "free" for it is a false one.
	if _, _, _, err := st.ClaimConflicts(b.SessionID, "stale-inc", "x", []string{"pkg"}); err == nil {
		t.Fatal("stale incarnation must be refused, not forecast free")
	}
}

func TestReleaseScopesNarrowsAndTheLastOneReleases(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.Claim(a.SessionID, a.Incarnation, "w", "d", []string{"pkg", "docs", "cmd"}); err != nil {
		t.Fatal(err)
	}
	// Control: docs is held.
	if err := st.Claim(b.SessionID, b.Incarnation, "bd", "x", []string{"docs/a.md"}); err == nil {
		t.Fatal("control: docs must be held before the partial release")
	}

	clk.advance(1)
	remaining, err := st.ReleaseScopes(a.SessionID, a.Incarnation, "w", []string{"Docs/"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(remaining, []string{"cmd", "pkg"}) {
		t.Fatalf("remaining: want [cmd pkg], got %v", remaining)
	}
	claims, _ := st.Claims(false)
	if len(claims) != 1 || !reflect.DeepEqual(claims[0].Scopes, []string{"cmd", "pkg"}) {
		t.Fatalf("board after narrowing: %+v", claims)
	}
	// The released scope is free for a peer; the kept ones are not.
	if err := st.Claim(b.SessionID, b.Incarnation, "bd", "x", []string{"docs/a.md"}); err != nil {
		t.Fatalf("released scope must be claimable by a peer: %v", err)
	}
	if err := st.Claim(b.SessionID, b.Incarnation, "bp", "x", []string{"pkg/x.go"}); err == nil {
		t.Fatal("kept scope must still be held")
	}

	// Releasing the rest releases the claim itself.
	remaining, err = st.ReleaseScopes(a.SessionID, a.Incarnation, "w", []string{"cmd", "pkg"})
	if err != nil || len(remaining) != 0 {
		t.Fatalf("want empty remaining, got %v %v", remaining, err)
	}
	all, _ := st.Claims(true)
	var ws []ClaimInfo
	for _, c := range all {
		if c.Slug == "w" {
			ws = append(ws, c)
		}
	}
	if len(ws) != 1 || ws[0].State != "released" {
		t.Fatalf("a claim with no scopes left must be RELEASED, not deleted: %+v", ws)
	}
}

func TestReleaseScopesRefusesWhatItCannotNarrow(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.Claim(a.SessionID, a.Incarnation, "w", "d", []string{"pkg", "docs"}); err != nil {
		t.Fatal(err)
	}
	// A scope the claim does not hold, exactly: containment is not release.
	// Releasing "pkg/sub" would leave "pkg" held and report success.
	for _, sc := range []string{"pkg/sub", "p", "internal"} {
		_, err := st.ReleaseScopes(a.SessionID, a.Incarnation, "w", []string{sc})
		var nh ErrScopeNotHeld
		if !errors.As(err, &nh) {
			t.Fatalf("%q: want ErrScopeNotHeld, got %v", sc, err)
		}
		if !strings.Contains(err.Error(), "docs") || !strings.Contains(err.Error(), "pkg") {
			t.Fatalf("the refusal must list what IS held: %q", err)
		}
	}
	// A mixed request — one held, one not — releases NOTHING: all-or-nothing
	// over the named scopes, or "released two of the three you asked for" is
	// the partial-acquisition shape from the other side.
	if _, err := st.ReleaseScopes(a.SessionID, a.Incarnation, "w", []string{"docs", "missing"}); err == nil {
		t.Fatal("mixed request must be refused")
	}
	claims, _ := st.Claims(false)
	if !reflect.DeepEqual(claims[0].Scopes, []string{"docs", "pkg"}) {
		t.Fatalf("refused partial release must not narrow: %v", claims[0].Scopes)
	}
	// The incarnation fence Release has, this has too.
	_, err := st.ReleaseScopes(a.SessionID, "stale-inc", "w", []string{"docs"})
	var nr ErrNoRelease
	if !errors.As(err, &nr) {
		t.Fatalf("want ErrNoRelease under a foreign incarnation, got %v", err)
	}
	// And no scopes at all is a usage error, not a full release by accident.
	if _, err := st.ReleaseScopes(a.SessionID, a.Incarnation, "w", nil); err == nil {
		t.Fatal("empty scope list must be refused")
	}
}
