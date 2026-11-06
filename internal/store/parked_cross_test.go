package store

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"testing"
)

// Issue #65. A claim parked in an open run declares nothing, so two parked claims
// that declare each other's runs no longer both pass the gate. TestCrossDeclared
// walks every door to the form, in both orders; TestEveryPairOfMarks reads every pair
// of marks at the gate and the transfer scan on one ledger state, so a door cannot drift.

const (
	crossRun  = "run-9"
	crossPath = "own/x.go"
)

// crossPair stands the form through the doors, one order: alpha's "own" parked in
// run-1 declaring run-9, delta's "held" parked in run-9 declaring run-1. Each step
// asserts its verdict, so this is also the "claim --based-on, then park" door, on
// both sides, and the #63 re-park with no --to.
func (f *parkFx) crossPair() {
	f.t.Helper()
	f.run(f.d, crossRun)
	if err := f.claim(f.coord, "own", "", crossPath); err != nil {
		f.t.Fatal(err)
	}
	f.refusedNaming(f.d, "held", parkRun, "alpha", crossPath) // the control: not parked yet
	f.parkHere(f.coord, parkRun, "own")
	f.granted(f.d, "held", parkRun, crossPath)
	f.gateFree(crossPath, f.d) // the legitimate overlap: an active declarer over a rider
	f.gateHeldBy(crossPath, f.coord, "held")

	// delta parks its declarer. Reported, never refused (#63), and it stops declaring.
	if got := standingSlugs(f.parkHere(f.d, crossRun, "held")); !slices.Equal(got, []string{"own"}) {
		f.t.Fatalf("delta's park reports %v", got)
	}
	f.gateHeldBy(crossPath, f.d, "own")
	f.gateHeldBy(crossPath, f.coord, "held")

	// alpha's re-claim is active and declares run-9, so "held" yields: one winner.
	if err := f.claim(f.coord, "own", crossRun, crossPath); err != nil {
		f.t.Fatal(err)
	}
	f.gateFree(crossPath, f.coord)
	f.gateHeldBy(crossPath, f.d, "own")

	// alpha parks it again, and the form stands: reported, and neither passes.
	if got := standingSlugs(f.parkHere(f.coord, parkRun, "own")); !slices.Equal(got, []string{"held"}) {
		f.t.Fatalf("alpha's park reports %v", got)
	}
	f.gateHeldBy(crossPath, f.coord, "held")
	f.gateHeldBy(crossPath, f.d, "own")
}

func (f *parkFx) holderOf(slug string) string {
	f.t.Helper()
	var id string
	if err := f.st.db.QueryRow(`SELECT session_id FROM claims WHERE slug=? AND state='open'`, slug).Scan(&id); err != nil {
		f.t.Fatalf("holder of %s: %v", slug, err)
	}
	return id
}

// oneWinner asserts one session passes the gate on the path and the other is denied.
func (f *parkFx) oneWinner(win, lose SessionInfo) {
	f.t.Helper()
	f.gateFree(crossPath, win)
	f.gateHeld(crossPath, lose)
}

func TestCrossDeclared(t *testing.T) {
	t.Run("declare then park, both sides: neither passes", func(t *testing.T) {
		newParkFx(t).crossPair()
	})
	t.Run("park then declare, the other order: one winner until the second park", func(t *testing.T) {
		f := newParkFx(t)
		f.run(f.d, crossRun)
		if err := f.claim(f.d, "held", "", crossPath); err != nil {
			t.Fatal(err)
		}
		f.parkHere(f.d, crossRun, "held")
		f.granted(f.coord, "own", crossRun, crossPath)
		f.oneWinner(f.coord, f.d)
		if got := standingSlugs(f.parkHere(f.coord, parkRun, "own")); !slices.Equal(got, []string{"held"}) {
			t.Fatalf("reports %v", got)
		}
		f.gateHeld(crossPath, f.coord)
		f.gateHeld(crossPath, f.d)
		// delta's re-claim declaring run-1: "own" yields, and the mark on "held" clears.
		if err := f.claim(f.d, "held", parkRun, crossPath); err != nil {
			t.Fatal(err)
		}
		f.oneWinner(f.d, f.coord)
		f.parkHere(f.d, crossRun, "held")
		f.gateHeld(crossPath, f.coord)
		f.gateHeld(crossPath, f.d)
	})
	t.Run("a third session's transfer: refused, nothing moves", func(t *testing.T) {
		// The reach the issue records. bravo's "held" goes to delta parked and declaring
		// run-1; charlie's lane declares run-9 and is admitted over it; handing that lane
		// to alpha parked in run-1 is what would cross the marks.
		f := newParkFx(t)
		f.run(f.d, crossRun)
		if err := f.claim(f.lane, "held", parkRun, crossPath); err != nil {
			t.Fatal(err)
		}
		f.park(f.lane, f.d, crossRun, "held")
		f.granted(f.c, "x", crossRun, crossPath)
		f.oneWinner(f.c, f.d)
		_, _, err := f.st.TransferClaimsInRun(f.c.SessionID, f.c.Incarnation, []string{"x"}, mustResolve(t, f.st, f.coord.Label), parkRun)
		var r ErrRefused
		if !errors.As(err, &r) || r.Slug != "held" {
			t.Fatalf("got %v, want a refusal naming held", err)
		}
		if f.holderOf("x") != f.c.SessionID {
			t.Fatal("the refused transfer moved the claim")
		}
		f.oneWinner(f.c, f.d)
		// The control: the same lane parks beside "held" under its holder, one session.
		f.park(f.c, f.d, crossRun, "x")
		if f.holderOf("x") != f.d.SessionID {
			t.Fatal("the control did not move")
		}
	})
	t.Run("the same, the declaring side parked first", func(t *testing.T) {
		f := newParkFx(t)
		f.run(f.d, crossRun)
		if err := f.claim(f.c, "x", crossRun, crossPath); err != nil {
			t.Fatal(err)
		}
		f.park(f.c, f.coord, parkRun, "x")
		f.granted(f.lane, "y", parkRun, crossPath)
		_, _, err := f.st.TransferClaimsInRun(f.lane.SessionID, f.lane.Incarnation, []string{"y"}, mustResolve(t, f.st, f.d.Label), crossRun)
		var r ErrRefused
		if !errors.As(err, &r) || r.Slug != "x" {
			t.Fatalf("got %v, want a refusal naming x", err)
		}
		if f.holderOf("y") != f.lane.SessionID {
			t.Fatal("the refused transfer moved the claim")
		}
		f.gateFree(crossPath, f.lane)
		f.gateHeldBy(crossPath, f.coord, "y")
		// The control: the same claim parks under x's holder, in x's run.
		f.park(f.lane, f.coord, parkRun, "y")
		if f.holderOf("y") != f.coord.SessionID {
			t.Fatal("the control did not move")
		}
	})
	t.Run("a run handed on alone changes neither verdict", func(t *testing.T) {
		f := newParkFx(t)
		f.crossPair()
		if err := f.hand(f.coord, f.c, parkRun); err != nil {
			t.Fatal(err)
		}
		if err := f.hand(f.d, f.lane, crossRun); err != nil {
			t.Fatal(err)
		}
		f.gateHeldBy(crossPath, f.coord, "held")
		f.gateHeldBy(crossPath, f.d, "own")
		// The control: the marks still read after the handoff, or "both denied" would
		// also be what a broken yield looks like. The ids moved with the runs.
		if _, _, err := f.st.UnparkClaim(f.coord.SessionID, f.coord.Incarnation, "own"); err != nil {
			t.Fatal(err)
		}
		f.oneWinner(f.coord, f.d)
	})
	t.Run("a run handed on with its side of the pair: refused, nothing moves", func(t *testing.T) {
		f := newParkFx(t)
		f.crossPair()
		err := f.hand(f.coord, f.c, parkRun, "own")
		var r ErrRefused
		if !errors.As(err, &r) || r.Slug != "held" {
			t.Fatalf("got %v, want a refusal naming held", err)
		}
		if f.holderOf(parkRun) != f.coord.SessionID || f.holderOf("own") != f.coord.SessionID {
			t.Fatal("the refused handoff moved something")
		}
		// The control: the same call with a rider outside the pair (D-133's ruling 2).
		if err := f.hand(f.coord, f.c, parkRun, "rider"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a side handed on plain: withdrawn, and it wins", func(t *testing.T) {
		f := newParkFx(t)
		f.crossPair()
		if err := f.hand(f.coord, f.c, "own"); err != nil {
			t.Fatal(err)
		}
		f.oneWinner(f.c, f.d)
	})
	t.Run("the other side handed on plain", func(t *testing.T) {
		f := newParkFx(t)
		f.crossPair()
		if err := f.hand(f.d, f.c, "held"); err != nil {
			t.Fatal(err)
		}
		f.oneWinner(f.c, f.coord)
	})
	t.Run("unpark resolves it, either side", func(t *testing.T) {
		for _, tc := range []struct {
			side string
			win  func(*parkFx) (SessionInfo, SessionInfo)
		}{
			{"own", func(f *parkFx) (SessionInfo, SessionInfo) { return f.coord, f.d }},
			{"held", func(f *parkFx) (SessionInfo, SessionInfo) { return f.d, f.coord }},
		} {
			f := newParkFx(t)
			f.crossPair()
			win, lose := tc.win(f)
			_, standing, err := f.st.UnparkClaim(win.SessionID, win.Incarnation, tc.side)
			if err != nil || len(standing) != 0 {
				t.Fatalf("%s: %v, standing %v", tc.side, err, standingSlugs(standing))
			}
			f.oneWinner(win, lose)
		}
	})
	t.Run("a re-claim resolves it only with its declaration", func(t *testing.T) {
		f := newParkFx(t)
		f.crossPair()
		err := f.claim(f.coord, "own", "", crossPath)
		var r ErrRefused
		if !errors.As(err, &r) || r.Claimant != "delta" {
			t.Fatalf("a bare re-claim: %v", err)
		}
		f.gateHeld(crossPath, f.coord) // the refusal left the mark
		if err := f.claim(f.coord, "own", crossRun, crossPath); err != nil {
			t.Fatal(err)
		}
		f.oneWinner(f.coord, f.d)
	})
	t.Run("a run closing wakes its rider's declaration", func(t *testing.T) {
		f := newParkFx(t)
		f.crossPair()
		f.closeRunOf(f.coord, parkRun)
		f.oneWinner(f.coord, f.d)
		// Both closed: neither is parked and neither declaration names an open run.
		f.closeRunOf(f.d, crossRun)
		f.gateHeld(crossPath, f.coord)
		f.gateHeld(crossPath, f.d)
	})
	t.Run("a third lane still has to get past the side it did not declare", func(t *testing.T) {
		f := newParkFx(t)
		f.crossPair()
		// One conflict each: the side it declared did yield, so neither both-refuse
		// nor a broken yield reads the same as this.
		if r := f.refusedNaming(f.c, "third", parkRun, "delta", crossPath); r.Slug != "held" || len(r.More) != 0 {
			t.Fatalf("declaring run-1: %+v", r)
		}
		if r := f.refusedNaming(f.c, "third", crossRun, "alpha", crossPath); r.Slug != "own" || len(r.More) != 0 {
			t.Fatalf("declaring run-9: %+v", r)
		}
	})
}

// The positive control the issue asks for: a lane branched from a run's tree stays
// admitted over that run's rider, through its own park and a handoff of the run.
func TestDeclarerOverARiderSurvives(t *testing.T) {
	f := newParkFx(t)
	f.refusedNaming(f.c, "next", "", "alpha", parkPath) // the control: undeclared
	f.granted(f.c, "next", parkRun, parkPath)
	f.gateFree(parkPath, f.c)
	f.gateHeldBy(parkPath, f.coord, "next")

	// READY, pane closing: it parks under the rider's holder, so no pair is scanned.
	f.park(f.c, f.coord, parkRun, "next")
	f.gateFree(parkPath, f.coord)

	// The next lane on that tree is admitted over both.
	f.granted(f.d, "later", parkRun, parkPath)
	f.gateFree(parkPath, f.d)
	f.gateHeldBy(parkPath, f.coord, "later")

	// D-133's ruling 2: the parked side moves and meets an active declarer.
	if err := f.hand(f.coord, f.lane, parkRun, "rider", "next"); err != nil {
		t.Fatal(err)
	}
	f.gateFree(parkPath, f.d)
	f.gateHeldBy(parkPath, f.lane, "later")
}

// What the rule costs, from the Codex design pass: a run handed on alone leaves its
// rider with the predecessor, and the admitted lane's park is refused until the rider
// follows. The fleet skill names a run and its riders in one call, which never meets this.
func TestRunHandedOnAloneRefusesAnAdmittedLanesPark(t *testing.T) {
	f := newParkFx(t)
	if err := f.hand(f.coord, f.d, parkRun); err != nil {
		t.Fatal(err)
	}
	f.granted(f.c, "next", parkRun, parkPath)
	f.gateFree(parkPath, f.c)
	_, _, err := f.st.TransferClaimsInRun(f.c.SessionID, f.c.Incarnation, []string{"next"}, mustResolve(t, f.st, f.d.Label), parkRun)
	var r ErrRefused
	if !errors.As(err, &r) || r.Slug != "rider" {
		t.Fatalf("got %v, want a refusal naming rider", err)
	}
	if f.holderOf("next") != f.c.SessionID {
		t.Fatal("the refused park moved the claim")
	}
	f.gateFree(parkPath, f.c) // it stays admitted, unparked

	// The remedy the refusal names, which is also the control.
	if err := f.hand(f.coord, f.d, "rider"); err != nil {
		t.Fatal(err)
	}
	f.park(f.c, f.d, parkRun, "next")
	if f.holderOf("next") != f.d.SessionID {
		t.Fatal("the park after the rider moved did not take")
	}
}

// markClaim writes the marks straight onto a claim: the state a pair formed by an
// earlier binary reads as, and every other state, with no door in the way. The rows
// are the schema's; nothing here is a form the doors have to reach.
func (f *parkFx) markClaim(slug, inRun, basedOn string) {
	f.t.Helper()
	if _, err := f.st.db.Exec(`UPDATE claims SET in_run=?, based_on=? WHERE slug=? AND state='open'`, inRun, basedOn, slug); err != nil {
		f.t.Fatal(err)
	}
}

func (f *parkFx) markShared(slug string, shared bool) {
	f.t.Helper()
	if _, err := f.st.db.Exec(`UPDATE claims SET shared=? WHERE slug=? AND state='open'`, shared, slug); err != nil {
		f.t.Fatal(err)
	}
}

// coverPath moves a claim made on a path of its own onto the shared one, since no
// door grants the second claim before the marks exist.
func (f *parkFx) coverPath(slug, path string) {
	f.t.Helper()
	if _, err := f.st.db.Exec(`UPDATE claim_scopes SET scope=?, folded=? WHERE claim_id=(SELECT claim_id FROM claims WHERE slug=? AND state='open')`, path, fold(path), slug); err != nil {
		f.t.Fatal(err)
	}
}

func (f *parkFx) claimIDOf(slug string) string {
	f.t.Helper()
	var id string
	if err := f.st.db.QueryRow(`SELECT claim_id FROM claims WHERE slug=? ORDER BY created DESC LIMIT 1`, slug).Scan(&id); err != nil {
		f.t.Fatalf("id of %s: %v", slug, err)
	}
	return id
}

// scanConflicts is what the transfer scan says of the claim where it stands.
func (f *parkFx) scanConflicts(owner SessionInfo, claimID string) int {
	f.t.Helper()
	var n int
	if err := f.st.tx(func(tx *sql.Tx) error {
		c, err := f.st.conflictsOfClaims(tx, owner.SessionID, []Moved{{ClaimID: claimID}})
		n = len(c)
		return err
	}); err != nil {
		f.t.Fatal(err)
	}
	return n
}

// Every pair of marks and modes over two open runs and a closed one: the gate and the transfer
// scan read one ledger state, the same answer, and two sessions both pass only on
// two shared claims. A pair standing when this binary arrives is one row of this table: no migration, and
// each side reads as it does here.
func TestEveryPairOfMarks(t *testing.T) {
	f := newParkFx(t)
	f.run(f.coord, "run-a")
	f.run(f.d, "run-b")
	f.run(f.coord, "run-z")
	f.closeRunOf(f.coord, "run-z")
	a, b, z := f.claimIDOf("run-a"), f.claimIDOf("run-b"), f.claimIDOf("run-z")
	marks := []string{"", a, b, z}
	if err := f.claim(f.coord, "x", "", "tmp/x"); err != nil {
		t.Fatal(err)
	}
	if err := f.claim(f.d, "y", "", "tmp/y"); err != nil {
		t.Fatal(err)
	}
	xID, yID := f.claimIDOf("x"), f.claimIDOf("y")
	f.coverPath("x", crossPath)
	f.coverPath("y", crossPath)

	parkedOpen := func(in string) bool { return in == a || in == b }
	yields := func(in, otherIn, otherBased string, otherShared bool) bool {
		return parkedOpen(in) && !parkedOpen(otherIn) && !otherShared && in == otherBased
	}
	var none, one, shared int
	modes := []bool{false, true}
	for _, xShared := range modes {
		for _, yShared := range modes {
			f.markShared("x", xShared)
			f.markShared("y", yShared)
			both := xShared && yShared
			for _, xIn := range marks {
				for _, xBased := range marks {
					for _, yIn := range marks {
						for _, yBased := range marks {
							f.markClaim("x", xIn, xBased)
							f.markClaim("y", yIn, yBased)
							name := fmt.Sprintf("x(shared %v, in %q, based %q) y(shared %v, in %q, based %q)", xShared, xIn, xBased, yShared, yIn, yBased)
							_, xHeld := f.gate(crossPath, f.coord)
							_, yHeld := f.gate(crossPath, f.d)
							if !xHeld && !yHeld && !both {
								t.Fatalf("%s: both pass a path an exclusive claim covers", name)
							}
							// Two shared claims overlap by D-042, whatever their marks say.
							xWins, yWins := both || yields(yIn, xIn, xBased, xShared), both || yields(xIn, yIn, yBased, yShared)
							if xWins != !xHeld || yWins != !yHeld {
								t.Fatalf("%s: gate x held %v, y held %v; the rule says x wins %v, y wins %v", name, xHeld, yHeld, xWins, yWins)
							}
							exempt := xWins || yWins
							switch {
							case both:
								shared++
							case exempt:
								one++
							default:
								none++
							}
							// The transfer scan, from either side, agrees with the gate.
							if got := f.scanConflicts(f.coord, xID) == 0; got != exempt {
								t.Fatalf("%s: the scan from x says exempt %v, the gate %v", name, got, exempt)
							}
							if got := f.scanConflicts(f.d, yID) == 0; got != exempt {
								t.Fatalf("%s: the scan from y says exempt %v, the gate %v", name, got, exempt)
							}
						}
					}
				}
			}
		}
	}
	if none == 0 || one == 0 || shared == 0 {
		t.Fatalf("the table never reached every verdict: %d neither, %d one, %d both shared", none, one, shared)
	}
}

// The shape rejected in parked.go: "a mutual yield is no yield" passes both of these
// sessions through four runs, every pair one-way and nothing reported.
func TestFourRunCycleDoesNotPassBoth(t *testing.T) {
	f := newParkFx(t)
	for _, r := range []struct {
		by   SessionInfo
		slug string
	}{{f.coord, "run-a"}, {f.coord, "run-c"}, {f.d, "run-b"}, {f.d, "run-d"}} {
		f.run(r.by, r.slug)
	}
	id := map[string]string{}
	for _, slug := range []string{"run-a", "run-b", "run-c", "run-d"} {
		id[slug] = f.claimIDOf(slug)
	}
	for _, c := range []struct {
		by       SessionInfo
		slug     string
		in, base string
	}{
		{f.coord, "x1", "run-a", "run-b"},
		{f.coord, "x2", "run-c", "run-d"},
		{f.d, "y1", "run-b", "run-c"},
		{f.d, "y2", "run-d", "run-a"},
	} {
		if err := f.claim(c.by, c.slug, "", "tmp/"+c.slug); err != nil {
			t.Fatal(err)
		}
		f.coverPath(c.slug, crossPath)
		f.markClaim(c.slug, id[c.in], id[c.base])
	}
	f.gateHeld(crossPath, f.coord)
	f.gateHeld(crossPath, f.d)
	// The control: the same rows with alpha's claims unparked do pass alpha.
	f.markClaim("x1", "", id["run-b"])
	f.markClaim("x2", "", id["run-d"])
	f.gateFree(crossPath, f.coord)
	f.gateHeld(crossPath, f.d)
	// And delta's, with alpha's parked again.
	f.markClaim("x1", id["run-a"], id["run-b"])
	f.markClaim("x2", id["run-c"], id["run-d"])
	f.markClaim("y1", "", id["run-c"])
	f.markClaim("y2", "", id["run-a"])
	f.gateFree(crossPath, f.d)
	f.gateHeld(crossPath, f.coord)
}

func TestDeclarationReadsOneRole(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    claimMarks
		want string
	}{
		{"never parked", claimMarks{basedOn: "r"}, "r"},
		{"parked in an open run", claimMarks{inRun: "p", runOpen: true, basedOn: "r"}, ""},
		{"parked in the run it declares", claimMarks{inRun: "r", runOpen: true, basedOn: "r"}, ""},
		{"its run closed", claimMarks{inRun: "p", runOpen: false, basedOn: "r"}, "r"},
		{"no declaration", claimMarks{}, ""},
		{"shared", claimMarks{basedOn: "r", shared: true}, ""},
		{"shared, its run closed", claimMarks{inRun: "p", runOpen: false, basedOn: "r", shared: true}, ""},
	} {
		if got := tc.m.declaration(); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
