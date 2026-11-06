package store

import (
	"errors"
	"slices"
	"testing"
)

// Issue #67. One session holds an exclusive claim parked in an open run and an active
// shared claim on one path. The fix is words, so this file pins VERDICTS, and it uses
// nothing the fix added: it compiles and passes against the commit before the fix, which
// is the evidence that no verdict moved. The words are rider_test.go's.

// holderShared: alpha's "own", exclusive, parked in run-1, and its active shared "note",
// on crossPath. The two orders that form the state at the claim door.
func holderShared(t *testing.T, sharedFirst bool) *parkFx {
	t.Helper()
	f := newParkFx(t)
	if sharedFirst {
		if err := f.sharedClaim(f.coord, "note", "", crossPath); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.claim(f.coord, "own", "", crossPath); err != nil {
		t.Fatal(err)
	}
	f.parkHere(f.coord, parkRun, "own")
	if !sharedFirst {
		if err := f.sharedClaim(f.coord, "note", "", crossPath); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// conflictsOf is what the forecast and the claim both name for delta's request, asserted
// equal, the request taken by nobody.
func (f *parkFx) conflictsOf(slug, based string, shared bool) []string {
	f.t.Helper()
	req := ClaimRequest{Slug: slug, Desc: "d", Scopes: []string{crossPath}, BasedOn: based, Shared: shared}
	_, conflicts, _, _, err := f.st.ClaimForecast(f.d.SessionID, f.d.Incarnation, req)
	if err != nil {
		f.t.Fatal(err)
	}
	var got []string
	for _, c := range conflicts {
		got = append(got, c.Slug)
	}
	_, err = f.st.ClaimDeclared(f.d.SessionID, f.d.Incarnation, req)
	var r ErrRefused
	if !errors.As(err, &r) {
		f.t.Fatalf("claim %s: got %v, want a refusal naming %v", slug, err, got)
	}
	named := []string{r.Slug}
	for _, m := range r.More {
		named = append(named, m.Slug)
	}
	slices.Sort(got)
	slices.Sort(named)
	if !slices.Equal(got, named) {
		f.t.Fatalf("claim %s: refused naming %v, forecast %v", slug, named, got)
	}
	f.notOpen(slug)
	return got
}

// theStateRefuses is the measurement the issue asked for: what the record said, with one
// difference. The commit had a peer's --shared declaration GRANTED and then refused at
// the gate; measured, the claim door refuses it, so no peer holds anything here.
func (f *parkFx) theStateRefuses(run string) {
	f.t.Helper()
	for _, c := range []struct {
		name, slug, based string
		shared            bool
		want              []string
	}{
		{"exclusive", "p1", "", false, []string{"note", "own"}},
		{"the rider's declarer", "p2", run, false, []string{"note"}},
		{"shared", "p3", "", true, []string{"own"}},
		{"shared, declaring", "p4", run, true, []string{"own"}},
	} {
		if got := f.conflictsOf(c.slug, c.based, c.shared); !slices.Equal(got, c.want) {
			f.t.Fatalf("%s: refused by %v, want %v", c.name, got, c.want)
		}
	}
	f.gateHeldBy(crossPath, f.d, "own")
}

func TestHolderSharedVerdicts(t *testing.T) {
	for name, sharedFirst := range map[string]bool{"the shared claim first": true, "the park first": false} {
		t.Run(name, func(t *testing.T) {
			f := holderShared(t, sharedFirst)
			f.theStateRefuses(parkRun)
			f.gateFree(crossPath, f.coord)
		})
	}
	t.Run("the state forms at the transfer door too", func(t *testing.T) {
		f := newParkFx(t)
		if err := f.sharedClaim(f.coord, "note", "", "tmp/note"); err != nil {
			t.Fatal(err)
		}
		f.coverPath("note", crossPath)
		if err := f.claim(f.lane, "own", "", "tmp/own"); err != nil {
			t.Fatal(err)
		}
		f.coverPath("own", crossPath)
		f.park(f.lane, f.coord, parkRun, "own") // to the holder: a session's own claims unscanned
		f.theStateRefuses(parkRun)
	})
	t.Run("a re-park moves the rider, and the state with it", func(t *testing.T) {
		f := holderShared(t, true)
		f.run(f.coord, crossRun)
		f.parkHere(f.coord, crossRun, "own")
		f.theStateRefuses(crossRun)
		if got := f.conflictsOf("old", parkRun, false); !slices.Equal(got, []string{"note", "own"}) {
			t.Fatalf("declaring the old run: %v", got)
		}
	})
	t.Run("the run and both claims handed on", func(t *testing.T) {
		f := holderShared(t, true)
		if err := f.hand(f.coord, f.c, parkRun, "own", "rider", "note"); err != nil {
			t.Fatal(err)
		}
		f.theStateRefuses(parkRun)
		f.gateFree(crossPath, f.c)
	})
	t.Run("the run handed on alone: the rider yields as before, the shared claim refuses", func(t *testing.T) {
		f := holderShared(t, true)
		if err := f.hand(f.coord, f.c, parkRun, "rider"); err != nil {
			t.Fatal(err)
		}
		f.theStateRefuses(parkRun)
	})
	t.Run("either claim handed on alone is refused by the other", func(t *testing.T) {
		f := holderShared(t, true)
		var r ErrRefused
		if err := f.hand(f.coord, f.c, "note"); !errors.As(err, &r) || r.Slug != "own" {
			t.Fatalf("the shared claim: %v", err)
		}
		if err := f.hand(f.coord, f.c, "own"); !errors.As(err, &r) || r.Slug != "note" {
			t.Fatalf("the rider: %v", err)
		}
		f.theStateRefuses(parkRun)
	})
	t.Run("the run closing ends the trap, and neither claim's refusal", func(t *testing.T) {
		f := holderShared(t, true)
		f.closeRunOf(f.coord, parkRun)
		if got := f.conflictsOf("p1", parkRun, false); !slices.Equal(got, []string{"note", "own"}) {
			t.Fatalf("declaring a closed run: %v", got)
		}
		if got := f.conflictsOf("p2", "", true); !slices.Equal(got, []string{"own"}) {
			t.Fatalf("shared: %v", got)
		}
		f.gateHeldBy(crossPath, f.d, "own")
		f.gateFree(crossPath, f.coord)
	})
}

// What gets a peer out, each cure tried: the words say to release the shared claim.
func TestHolderSharedCures(t *testing.T) {
	t.Run("the shared claim released: the declarer is granted and passes alone", func(t *testing.T) {
		f := holderShared(t, true)
		if err := f.st.Release(f.coord.SessionID, f.coord.Incarnation, "note"); err != nil {
			t.Fatal(err)
		}
		f.granted(f.d, "y", parkRun, crossPath)
		f.oneWinner(f.d, f.coord)
	})
	t.Run("the shared claim parked in the same run: a shared rider yields to the declarer", func(t *testing.T) {
		f := holderShared(t, true)
		f.parkHere(f.coord, parkRun, "note")
		f.granted(f.d, "y", parkRun, crossPath)
		f.oneWinner(f.d, f.coord)
	})
	t.Run("the rider released: a shared claim joins, D-042's own", func(t *testing.T) {
		f := holderShared(t, true)
		if err := f.st.Release(f.coord.SessionID, f.coord.Incarnation, "own"); err != nil {
			t.Fatal(err)
		}
		if err := f.sharedClaim(f.d, "y", "", crossPath); err != nil {
			t.Fatal(err)
		}
		f.gateFree(crossPath, f.d)
		f.gateFree(crossPath, f.coord)
	})
	t.Run("the shared claim handed to the rider's holder changes nothing", func(t *testing.T) {
		f := newParkFx(t)
		if err := f.sharedClaim(f.c, "note", "", "tmp/note"); err != nil {
			t.Fatal(err)
		}
		f.coverPath("note", crossPath)
		if err := f.claim(f.coord, "own", "", "tmp/own"); err != nil {
			t.Fatal(err)
		}
		f.coverPath("own", crossPath)
		f.parkHere(f.coord, parkRun, "own")
		if err := f.hand(f.c, f.coord, "note"); err != nil {
			t.Fatal(err)
		}
		f.theStateRefuses(parkRun)
	})
}

// The state with a declarer standing in it, through the doors: a shared rider yields to the
// declarer, then its holder unparks it. Nothing is revoked, so nobody passes, and the gate's
// shared hold is what the declarer meets; its own exclusive claim survives a refused retry.
func TestHolderSharedWithADeclarerStanding(t *testing.T) {
	f := newParkFx(t)
	if err := f.claim(f.coord, "own", "", crossPath); err != nil {
		t.Fatal(err)
	}
	f.parkHere(f.coord, parkRun, "own")
	if err := f.sharedClaim(f.coord, "note", "", crossPath); err != nil {
		t.Fatal(err)
	}
	f.parkHere(f.coord, parkRun, "note")
	f.granted(f.d, "y", parkRun, crossPath)
	f.oneWinner(f.d, f.coord) // the control: the declarer passes before the unpark
	_, standing, err := f.st.UnparkClaim(f.coord.SessionID, f.coord.Incarnation, "note")
	if err != nil || !slices.Equal(standingSlugs(standing), []string{"y"}) {
		t.Fatalf("%v, standing %v", err, standingSlugs(standing))
	}
	f.gateHeldBy(crossPath, f.d, "note")
	f.gateHeldBy(crossPath, f.coord, "y")
	f.sharedRefused(f.d, "y", parkRun, crossPath, "own")
	f.gateHeldBy(crossPath, f.d, "note") // the refusal left the exclusive claim standing
	if err := f.st.Release(f.coord.SessionID, f.coord.Incarnation, "note"); err != nil {
		t.Fatal(err)
	}
	f.oneWinner(f.d, f.coord)
}
