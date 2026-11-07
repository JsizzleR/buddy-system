package store

import (
	"errors"
	"slices"
	"testing"
)

// Issues #68 and #70. #70 moves a verdict: a run narrowed off its own slot reads closed, at
// every door. #68 is words: StuckBeside, the pairs a door's own claims now stand in. The CLI's
// words and #69's gate deny are internal/cli/stuck_test.go.

// narrowRun takes run-1 off its own slot, keeping it open on .buddy/slot/main, by each of the
// two spellings D-133's Open named.
func (f *parkFx) narrowRun(how string) {
	f.t.Helper()
	if err := f.st.Claim(f.coord.SessionID, f.coord.Incarnation, parkRun, "FORMING",
		[]string{SlotPrefix + "/" + parkRun, SlotPrefix + "/main"}); err != nil {
		f.t.Fatal(err)
	}
	switch how {
	case "release --scope":
		if _, err := f.st.ReleaseScopes(f.coord.SessionID, f.coord.Incarnation, parkRun, []string{SlotPrefix + "/" + parkRun}); err != nil {
			f.t.Fatal(err)
		}
	case "re-claim":
		if err := f.st.Claim(f.coord.SessionID, f.coord.Incarnation, parkRun, "FORMING", []string{SlotPrefix + "/main"}); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *parkFx) riderOpen(slug string) bool {
	f.t.Helper()
	all, err := f.st.Claims(false)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, c := range all {
		if c.Slug == slug {
			return c.InRunOpen
		}
	}
	f.t.Fatalf("no open claim %q", slug)
	return false
}

func TestNarrowedRunReadsClosed(t *testing.T) {
	for _, how := range []string{"release --scope", "re-claim"} {
		t.Run(how, func(t *testing.T) {
			f := newParkFx(t)
			// The control: open, the rider yields to charlie's declaration at every door.
			f.granted(f.c, "lane", parkRun, parkPath)
			f.gateFree(parkPath, f.c)
			// The hand-on control: granted while the run is open, and handed back.
			if err := f.hand(f.c, f.d, "lane"); err != nil {
				t.Fatalf("control: the hand-on while open: %v", err)
			}
			if err := f.hand(f.d, f.c, "lane"); err != nil {
				t.Fatal(err)
			}
			if !f.riderOpen("rider") {
				t.Fatal("control: the rider of an open run reads closed")
			}
			f.narrowRun(how)
			// The gate: the admitted lane is denied, as for a run closed before its riders.
			f.gateHeldBy(parkPath, f.c, "rider")
			// ls and who read the same.
			if f.riderOpen("rider") {
				t.Fatal("a rider of a run off its slot reads open")
			}
			// claim and its dry run: refused naming the rider, parked in a run not open.
			r := f.refusedNaming(f.d, "d1", "", "alpha", parkPath)
			found := false
			for _, c := range append([]ErrRefused{r}, r.More...) {
				if c.Slug == "rider" {
					found = c.ParkedIn == parkRun && !c.ParkedOpen
				}
			}
			if !found {
				t.Fatalf("refusal does not name the rider parked in a run not open: %+v", r)
			}
			// --based-on names no run, as before (unchanged; the doors now agree with it).
			var nr ErrNotARun
			if err := f.claim(f.d, "d2", parkRun, "src/z.go"); !errors.As(err, &nr) {
				t.Fatalf("--based-on a run off its slot: %v", err)
			}
			// release --to: charlie's lane handed to delta is refused, naming the rider.
			var refused ErrRefused
			if err := f.hand(f.c, f.d, "lane"); !errors.As(err, &refused) || refused.Slug != "rider" {
				t.Fatalf("a hand-on over a rider of a run off its slot: %v", err)
			}
			// The slot back: the same claim id, so open again, and the yield returns.
			if err := f.st.Claim(f.coord.SessionID, f.coord.Incarnation, parkRun, "FORMING",
				[]string{SlotPrefix + "/" + parkRun, SlotPrefix + "/main"}); err != nil {
				t.Fatal(err)
			}
			f.gateFree(parkPath, f.c)
		})
	}
}

// The slot test folds as holdsRunSlot does: a run whose slug and slot differ in case and
// normalization is a run at every door. SQLite's lower() alone reads this one closed.
func TestRunSlotFoldsAsGoDoes(t *testing.T) {
	f := newParkFx(t)
	const slug = "Rün-2"            // decomposed u-umlaut
	const slot = ".buddy/slot/RÜN-2" // composed, upper case
	if err := f.st.Claim(f.coord.SessionID, f.coord.Incarnation, slug, "FORMING", []string{slot}); err != nil {
		t.Fatal(err)
	}
	if err := f.claim(f.lane, "item2", "", "src/q.go"); err != nil {
		t.Fatal(err)
	}
	f.park(f.lane, f.coord, slug, "item2")
	if !f.riderOpen("item2") {
		t.Fatal("a rider of a run whose slot folds to its slug reads closed")
	}
	f.granted(f.c, "lane2", slug, "src/q.go")
	f.gateFree("src/q.go", f.c)
}

func pairNames(pairs []TwoRemedies) []string {
	var out []string
	for _, p := range pairs {
		out = append(out, p.Rider.Slug+"+"+p.Shared.Slug)
	}
	slices.Sort(out)
	return out
}

func (f *parkFx) stuckBeside(by SessionInfo, want []string, slugs ...string) {
	f.t.Helper()
	if got := pairNames(f.st.StuckBeside(by.SessionID, slugs)); !slices.Equal(got, want) {
		f.t.Fatalf("StuckBeside(%s, %v) = %v, want %v", by.Label, slugs, got, want)
	}
}

func TestStuckBesideNamesThePairTheDoorFormed(t *testing.T) {
	for name, sharedFirst := range map[string]bool{"the shared claim first": true, "the park first": false} {
		t.Run(name, func(t *testing.T) {
			f := holderShared(t, sharedFirst)
			f.stuckBeside(f.coord, []string{"own+note"}, "note")
			f.stuckBeside(f.coord, []string{"own+note"}, "own")
			// The run names its riders, under any holder.
			f.stuckBeside(f.coord, []string{"own+note"}, parkRun)
			// Released, the words go: a positive control for the empty answers below.
			if err := f.st.Release(f.coord.SessionID, f.coord.Incarnation, "note"); err != nil {
				t.Fatal(err)
			}
			f.stuckBeside(f.coord, nil, "own")
		})
	}
}

// Codex's design-pass case: a second rider joins a pair already standing. Deduplicating
// before the filter kept own+note and dropped own2+note, so the door that formed it said nothing.
func TestStuckBesideASecondRider(t *testing.T) {
	f := holderShared(t, false)
	if err := f.claim(f.coord, "own2", "", crossPath); err != nil {
		t.Fatal(err)
	}
	f.parkHere(f.coord, parkRun, "own2")
	// Every pair on its scopes, not only its own: releasing note ends both.
	f.stuckBeside(f.coord, []string{"own+note", "own2+note"}, "own2")
}

func TestStuckBesideOnlyForAPair(t *testing.T) {
	f := newParkFx(t)
	// A rider on one path and a shared claim on another: no single path is trapped.
	if err := f.claim(f.coord, "x", "", "p/x.go"); err != nil {
		t.Fatal(err)
	}
	f.parkHere(f.coord, parkRun, "x")
	if err := f.sharedClaim(f.coord, "s", "", "p/y.go"); err != nil {
		t.Fatal(err)
	}
	// Both named, so both disjoint sides reach the overlap filter (Codex code pass); the
	// overlapping s2 below is its positive control.
	f.stuckBeside(f.coord, nil, "x", "s")
	// A shared claim parked in the same open run yields to its declarer: no pair.
	if err := f.sharedClaim(f.coord, "s2", "", "p/x.go"); err != nil {
		t.Fatal(err)
	}
	f.stuckBeside(f.coord, []string{"x+s2"}, "s2")
	f.parkHere(f.coord, parkRun, "s2")
	f.stuckBeside(f.coord, nil, "s2")
	// Parked in a different open run, it does not yield to run-1's declarer: a pair.
	f.run(f.coord, "run-2")
	f.parkHere(f.coord, "run-2", "s2")
	f.stuckBeside(f.coord, []string{"x+s2"}, "s2")
	// The run off its slot: the rider reads closed, so there is no pair (#70).
	f.narrowRun("release --scope")
	f.stuckBeside(f.coord, nil, "s2")
}

// The door speaks only when a claim it names stands in a pair: a new exclusive claim of the
// holder on a path a pair already traps is in no pair (the guard's control is the pair above).
func TestStuckBesideSilentForAClaimInNoPair(t *testing.T) {
	f := holderShared(t, false)
	f.stuckBeside(f.coord, []string{"own+note"}, "note")
	if err := f.claim(f.coord, "plain", "", crossPath); err != nil {
		t.Fatal(err)
	}
	f.stuckBeside(f.coord, nil, "plain")
}
