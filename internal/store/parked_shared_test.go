package store

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
)

// Issue #66. A shared claim declares nothing. Both forms the #65 lane recorded open,
// each door to them in either order, what a pair standing when this binary arrives
// reads as, and D-042 and D-133's own overlaps, which must not move.

// sharedClaim is `claim --shared [--based-on]`.
func (f *parkFx) sharedClaim(by SessionInfo, slug, based, path string) error {
	f.t.Helper()
	_, err := f.st.ClaimDeclared(by.SessionID, by.Incarnation, ClaimRequest{Slug: slug, Desc: "d", Scopes: []string{path}, BasedOn: based, Shared: true})
	return err
}

// sharedRefused asserts the request is refused naming the claim, by the forecast and
// by the claim.
func (f *parkFx) sharedRefused(by SessionInfo, slug, based, path, holding string) {
	f.t.Helper()
	req := ClaimRequest{Slug: slug, Desc: "d", Scopes: []string{path}, BasedOn: based, Shared: true}
	_, conflicts, _, _, ferr := f.st.ClaimForecast(by.SessionID, by.Incarnation, req)
	if ferr != nil || !slices.ContainsFunc(conflicts, func(c Conflict) bool { return c.Slug == holding }) {
		f.t.Fatalf("forecast of %s: %v, %+v, want a conflict naming %s", slug, ferr, conflicts, holding)
	}
	_, err := f.st.ClaimDeclared(by.SessionID, by.Incarnation, req)
	var r ErrRefused
	if !errors.As(err, &r) || r.Slug != holding {
		f.t.Fatalf("claim %s: got %v, want a refusal naming %s", slug, err, holding)
	}
}

// riderHere: alpha's "own", exclusive, parked in run-1 on crossPath.
func riderHere(t *testing.T) *parkFx {
	t.Helper()
	f := newParkFx(t)
	if err := f.claim(f.coord, "own", "", crossPath); err != nil {
		t.Fatal(err)
	}
	f.parkHere(f.coord, parkRun, "own")
	return f
}

// standingDeclarer leaves delta's "y" shared and declaring run-1 beside "own", as an
// earlier binary granted it. Claimed elsewhere and moved onto the path, because `claim`
// no longer grants this; the row is what that binary wrote.
func (f *parkFx) standingDeclarer() {
	f.t.Helper()
	if err := f.sharedClaim(f.d, "y", parkRun, "tmp/y"); err != nil {
		f.t.Fatal(err)
	}
	f.coverPath("y", crossPath)
}

// The form with one run and no crossing, which was both sessions passing.
func TestSharedDeclarerBesideARiderIsRefused(t *testing.T) {
	f := riderHere(t)
	f.sharedRefused(f.d, "y", parkRun, crossPath, "own")
	// The refusal's positive control: the same lane, the same run, exclusive.
	f.granted(f.d, "y", parkRun, crossPath)
	f.gateFree(crossPath, f.d)
	f.gateHeldBy(crossPath, f.coord, "y")
}

// The crossed form, through the doors: no shared claim of it can be taken.
func TestSharedDeclarersOverCrossedRidersAreRefused(t *testing.T) {
	f := riderHere(t)
	f.run(f.d, crossRun)
	f.sharedRefused(f.d, "s-y", parkRun, crossPath, "own") // the crossing's first step
	f.granted(f.d, "y", parkRun, crossPath)                // exclusive, D-133's own
	if got := standingSlugs(f.parkHere(f.d, crossRun, "y")); !slices.Contains(got, "own") {
		t.Fatalf("the park must report the overlap: %v", got)
	}
	f.sharedRefused(f.coord, "s-x", crossRun, crossPath, "y") // and its second
	f.gateHeld(crossPath, f.coord)
	f.gateHeld(crossPath, f.d)
	// The control for the second refusal: the same claim, exclusive, is admitted over "y".
	f.granted(f.coord, "s-x", crossRun, crossPath)
	f.oneWinner(f.coord, f.d)
}

// A pair standing when this binary arrives needs no migration. Nothing is revoked, and
// this is what each form then reads as, door by door.
func TestStandingSharedDeclarerReads(t *testing.T) {
	t.Run("one run: the rider's holder passes on its shared claim, the declarer does not", func(t *testing.T) {
		f := riderHere(t)
		f.standingDeclarer()
		f.gateHeldBy(crossPath, f.d, "own")
		f.gateHeldBy(crossPath, f.coord, "y") // the control: alpha has no shared claim yet
		if err := f.sharedClaim(f.coord, "s", "", crossPath); err != nil {
			t.Fatal(err)
		}
		f.gateFree(crossPath, f.coord)
		f.gateHeldBy(crossPath, f.d, "own")
	})
	t.Run("crossed: neither passes", func(t *testing.T) {
		f := riderHere(t)
		f.run(f.d, crossRun)
		for _, c := range []struct {
			by   SessionInfo
			slug string
		}{{f.d, "s-y"}, {f.coord, "s-x"}} {
			if err := f.sharedClaim(c.by, c.slug, "", "tmp/"+c.slug); err != nil {
				t.Fatal(err)
			}
			f.coverPath(c.slug, crossPath)
		}
		f.markClaim("s-y", "", f.claimIDOf(parkRun))
		f.markClaim("s-x", "", f.claimIDOf(crossRun))
		if err := f.claim(f.d, "y", "", "tmp/held"); err != nil {
			t.Fatal(err)
		}
		f.coverPath("y", crossPath)
		f.markClaim("y", f.claimIDOf(crossRun), f.claimIDOf(parkRun))
		f.gateHeld(crossPath, f.coord)
		f.gateHeld(crossPath, f.d)
		// The cure: unparking "y" wakes its exclusive declaration of run-1, one winner, so
		// "neither" above is the rule and not a broken yield. Unparking "own" would cure
		// nothing, because it declares nothing (Codex design pass).
		if _, _, err := f.st.UnparkClaim(f.coord.SessionID, f.coord.Incarnation, "own"); err != nil {
			t.Fatal(err)
		}
		f.gateHeld(crossPath, f.coord)
		f.gateHeld(crossPath, f.d)
		f.parkHere(f.coord, parkRun, "own")
		if _, _, err := f.st.UnparkClaim(f.d.SessionID, f.d.Incarnation, "y"); err != nil {
			t.Fatal(err)
		}
		f.oneWinner(f.d, f.coord)
	})
	t.Run("its holder cures it with an exclusive re-claim", func(t *testing.T) {
		f := riderHere(t)
		f.standingDeclarer()
		f.sharedRefused(f.d, "y", parkRun, crossPath, "own") // repeating itself cures nothing
		if err := f.claim(f.d, "y", parkRun, crossPath); err != nil {
			t.Fatal(err)
		}
		f.oneWinner(f.d, f.coord)
	})
	t.Run("the transfer scan refuses it, from either side", func(t *testing.T) {
		f := riderHere(t)
		f.standingDeclarer()
		err := f.hand(f.coord, f.c, parkRun, "own", "rider")
		var r ErrRefused
		if !errors.As(err, &r) || r.Slug != "y" {
			t.Fatalf("the run and its rider: got %v, want a refusal naming y", err)
		}
		if f.holderOf(parkRun) != f.coord.SessionID {
			t.Fatal("the refused handoff moved something")
		}
		err = f.hand(f.d, f.c, "y")
		if !errors.As(err, &r) || r.Slug != "own" {
			t.Fatalf("the declarer: got %v, want a refusal naming own", err)
		}
		// The control: to the rider's holder it moves, a session's own claims unscanned.
		if err := f.hand(f.d, f.coord, "y"); err != nil {
			t.Fatal(err)
		}
		// And the run moves with everything once the pair is one holder's.
		if err := f.hand(f.coord, f.c, parkRun, "own", "rider", "y"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a re-park reports it", func(t *testing.T) {
		f := riderHere(t)
		f.standingDeclarer()
		if got := standingSlugs(f.parkHere(f.coord, parkRun, "own")); !slices.Equal(got, []string{"y"}) {
			t.Fatalf("reports %v", got)
		}
	})
	t.Run("a run closing changes nothing it decided", func(t *testing.T) {
		f := riderHere(t)
		f.standingDeclarer()
		f.closeRunOf(f.coord, parkRun)
		f.gateHeldBy(crossPath, f.d, "own")
		f.gateHeldBy(crossPath, f.coord, "y")
	})
	t.Run("unparking the rider changes nothing it decided", func(t *testing.T) {
		f := riderHere(t)
		f.standingDeclarer()
		_, standing, err := f.st.UnparkClaim(f.coord.SessionID, f.coord.Incarnation, "own")
		if err != nil || !slices.Equal(standingSlugs(standing), []string{"y"}) {
			t.Fatalf("%v, standing %v", err, standingSlugs(standing))
		}
		f.gateHeldBy(crossPath, f.d, "own")
	})
	t.Run("a side handed on plain meets it like any claim", func(t *testing.T) {
		f := riderHere(t)
		f.standingDeclarer()
		err := f.hand(f.coord, f.c, "own")
		var r ErrRefused
		if !errors.As(err, &r) || r.Slug != "y" {
			t.Fatalf("got %v, want a refusal naming y", err)
		}
	})
}

// The doors that reach the pair with nothing planted: a shared claim first, the
// exclusive one admitted over it while it was itself parked, then the park.
func TestSharedDeclarerBeforeThePark(t *testing.T) {
	f := newParkFx(t)
	f.run(f.d, crossRun)
	if err := f.sharedClaim(f.d, "y", parkRun, crossPath); err != nil {
		t.Fatal(err)
	}
	f.parkHere(f.d, crossRun, "y")
	f.granted(f.coord, "own", crossRun, crossPath) // over a shared rider, D-133's own
	f.gateHeld(crossPath, f.d)
	if got := standingSlugs(f.parkHere(f.coord, parkRun, "own")); !slices.Equal(got, []string{"y"}) {
		t.Fatalf("the park reports %v", got)
	}
	_, standing, err := f.st.UnparkClaim(f.d.SessionID, f.d.Incarnation, "y")
	if err != nil || !slices.Equal(standingSlugs(standing), []string{"own"}) {
		t.Fatalf("%v, standing %v", err, standingSlugs(standing))
	}
	// y is shared and declares run-1 beside a rider of run-1. Before #66, this passed.
	f.gateHeldBy(crossPath, f.d, "own")
}

// A mode change is a re-claim, and the re-claim takes what it is given.
func TestDeclarerChangingModeAtTheClaimDoor(t *testing.T) {
	f := riderHere(t)
	f.granted(f.d, "y", parkRun, crossPath)
	f.sharedRefused(f.d, "y", parkRun, crossPath, "own")
	f.gateFree(crossPath, f.d) // the refusal left the exclusive declaration standing
}

// What must not move. D-042: two shared claims overlap, whatever a run says, and each
// session still needs its own. D-133: the exclusive declarer over either kind of rider.
func TestSharedDeclarationLeavesD042AndD133Alone(t *testing.T) {
	t.Run("two shared claims, no run", func(t *testing.T) {
		f := newParkFx(t)
		if err := f.sharedClaim(f.c, "a", "", crossPath); err != nil {
			t.Fatal(err)
		}
		f.gateFree(crossPath, f.c)
		f.gateHeldBy(crossPath, f.d, "a") // an invitation, not an open door
		if err := f.sharedClaim(f.d, "b", "", crossPath); err != nil {
			t.Fatal(err)
		}
		f.gateFree(crossPath, f.c)
		f.gateFree(crossPath, f.d)
		f.refusedNaming(f.coord, "x", "", "charlie", crossPath) // shared refuses exclusive
	})
	t.Run("a shared claim beside a shared rider, declaring or not", func(t *testing.T) {
		f := newParkFx(t)
		if err := f.sharedClaim(f.lane, "r", "", crossPath); err != nil {
			t.Fatal(err)
		}
		f.park(f.lane, f.coord, parkRun, "r")
		for _, c := range []struct {
			by         SessionInfo
			slug, base string
		}{{f.c, "a", ""}, {f.d, "b", parkRun}} {
			if err := f.sharedClaim(c.by, c.slug, c.base, crossPath); err != nil {
				t.Fatal(err)
			}
			f.gateFree(crossPath, c.by)
		}
		f.gateFree(crossPath, f.coord) // its holder too: the rider itself is a shared cover
	})
	t.Run("an exclusive declarer over a shared rider yields as before", func(t *testing.T) {
		f := newParkFx(t)
		if err := f.sharedClaim(f.lane, "r", "", crossPath); err != nil {
			t.Fatal(err)
		}
		f.park(f.lane, f.coord, parkRun, "r")
		f.granted(f.c, "next", parkRun, crossPath)
		f.gateFree(crossPath, f.c)
		f.gateHeldBy(crossPath, f.coord, "next")
	})
	t.Run("a shared claim with no declaration over an exclusive rider was refused and is", func(t *testing.T) {
		f := riderHere(t)
		f.sharedRefused(f.c, "s", "", crossPath, "own")
	})
}

// THE STATEMENT parked.go makes: on a path any exclusive claim covers, at most one
// session passes, whatever the marks. Three sessions with two claims each, drawn from
// every mode and mark over two open runs and a closed one, seeded so a failure repeats.
// A second claim per session is what TestFourRunCycleDoesNotPassBoth needed against a
// rule read pair by pair.
func TestAtMostOneSessionPasses(t *testing.T) {
	f := newParkFx(t)
	f.run(f.coord, "run-a")
	f.run(f.d, "run-b")
	f.run(f.coord, "run-z")
	f.closeRunOf(f.coord, "run-z")
	marks := []string{"", f.claimIDOf("run-a"), f.claimIDOf("run-b"), f.claimIDOf("run-z")}
	sessions := []SessionInfo{f.coord, f.d, f.c}
	var slugs []string
	for i, by := range sessions {
		for j := range 2 {
			slug := fmt.Sprintf("c%d%d", i, j)
			if err := f.claim(by, slug, "", "tmp/"+slug); err != nil {
				t.Fatal(err)
			}
			f.coverPath(slug, crossPath)
			slugs = append(slugs, slug)
		}
	}
	rng := rand.New(rand.NewPCG(66, 66))
	openRun := func(id string) bool { return id == marks[1] || id == marks[2] }
	type drawn struct {
		session, in, based string
		shared             bool
	}
	var nobody, one, yielded, allShared, theForm int
	for range 1500 {
		anyExclusive := false
		var rows []drawn
		for i, slug := range slugs {
			shared := rng.IntN(2) == 0
			anyExclusive = anyExclusive || !shared
			in, based := marks[rng.IntN(len(marks))], marks[rng.IntN(len(marks))]
			f.markShared(slug, shared)
			f.markClaim(slug, in, based)
			rows = append(rows, drawn{sessions[i/2].SessionID, in, based, shared})
		}
		// The form that matters: another session's shared claim, not parked in an open run,
		// names the run an exclusive claim is parked in. A yield to it is what #66 ended.
		if slices.ContainsFunc(rows, func(r drawn) bool {
			return !r.shared && openRun(r.in) && slices.ContainsFunc(rows, func(d drawn) bool {
				return d.shared && d.session != r.session && d.based == r.in && !openRun(d.in)
			})
		}) {
			theForm++
		}
		var passing []SessionInfo
		for _, by := range sessions {
			if _, held := f.gate(crossPath, by); !held {
				passing = append(passing, by)
			}
		}
		switch {
		case !anyExclusive:
			if len(passing) != len(sessions) {
				t.Fatalf("every claim shared and %d pass: D-042 says all", len(passing))
			}
			allShared++
		case len(passing) > 1:
			t.Fatalf("%d sessions pass a path an exclusive claim covers", len(passing))
		case len(passing) == 1:
			one++
			if f.foreignExclusive(passing[0]) {
				yielded++
			}
		default:
			nobody++
		}
	}
	// Armed: each verdict was reached, a yield included, or "at most one" says nothing.
	if nobody == 0 || one == 0 || yielded == 0 || allShared == 0 || theForm == 0 {
		t.Fatalf("not all reached: nobody %d, one %d, a yield %d, all shared %d, the form %d", nobody, one, yielded, allShared, theForm)
	}
}

// foreignExclusive reports an exclusive claim of another session on crossPath.
func (f *parkFx) foreignExclusive(by SessionInfo) bool {
	f.t.Helper()
	var n int
	if err := f.st.db.QueryRow(`SELECT COUNT(*) FROM claims c JOIN claim_scopes cs ON cs.claim_id=c.claim_id
		WHERE c.state='open' AND c.shared=0 AND c.session_id<>? AND cs.folded=?`, by.SessionID, fold(crossPath)).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n > 0
}
