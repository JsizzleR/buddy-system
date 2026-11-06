package store

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// D-133 (issue #61). A claim parked in run R yields to a claim declaring R, while
// R is open. Every test here has its control in the same fixture: the same path
// and the same caller, refused, so a grant cannot be a path nobody held.

type parkFx struct {
	t              *testing.T
	st             *Store
	clk            *pinnedClock
	coord, lane, c SessionInfo
	d              SessionInfo
}

const (
	parkRun  = "run-1"
	parkPath = "src/a.go"
)

// newParkFx: alpha holds run-1; bravo made the item "rider" and parked it there,
// so alpha holds it; charlie and delta are lanes.
func newParkFx(t *testing.T) *parkFx {
	t.Helper()
	st, clk := openTest(t)
	f := &parkFx{t: t, st: st, clk: clk,
		coord: hello(t, st, "sess-a", "alpha", "/wt/a"), lane: hello(t, st, "sess-b", "bravo", "/wt/b"),
		c: hello(t, st, "sess-c", "charlie", "/wt/c"), d: hello(t, st, "sess-d", "delta", "/wt/d")}
	f.run(f.coord, parkRun)
	if err := st.Claim(f.lane.SessionID, f.lane.Incarnation, "rider", "the item", []string{parkPath, "src/b.go"}); err != nil {
		t.Fatal(err)
	}
	f.park(f.lane, f.coord, parkRun, "rider")
	return f
}

func (f *parkFx) run(by SessionInfo, slug string) {
	f.t.Helper()
	if err := f.st.Claim(by.SessionID, by.Incarnation, slug, "FORMING", []string{SlotPrefix + "/" + slug}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *parkFx) park(from, to SessionInfo, run string, slugs ...string) {
	f.t.Helper()
	if _, _, err := f.st.TransferClaimsInRun(from.SessionID, from.Incarnation, slugs, mustResolve(f.t, f.st, to.Label), run); err != nil {
		f.t.Fatalf("park %v in %s: %v", slugs, run, err)
	}
}

func (f *parkFx) claim(by SessionInfo, slug, basedOn string, scopes ...string) error {
	f.t.Helper()
	_, err := f.st.ClaimDeclared(by.SessionID, by.Incarnation, ClaimRequest{Slug: slug, Desc: "d", Scopes: scopes, BasedOn: basedOn})
	return err
}

// refusedNaming asserts a whole refusal naming a holder, and that the dry run
// forecasts it: the forecast and the refusal are one computation (D-019).
func (f *parkFx) refusedNaming(by SessionInfo, slug, basedOn, holder string, scopes ...string) ErrRefused {
	f.t.Helper()
	_, conflicts, _, _, ferr := f.st.ClaimForecast(by.SessionID, by.Incarnation, ClaimRequest{Slug: slug, Scopes: scopes, BasedOn: basedOn})
	if ferr != nil {
		f.t.Fatalf("forecast of %s: %v", slug, ferr)
	}
	err := f.claim(by, slug, basedOn, scopes...)
	var r ErrRefused
	if !errors.As(err, &r) {
		f.t.Fatalf("claim %s (based on %q, scopes %v): want a refusal, got %v", slug, basedOn, scopes, err)
	}
	if 1+len(r.More) != len(conflicts) {
		f.t.Fatalf("claim %s: refused with %d conflict(s), forecast %d", slug, 1+len(r.More), len(conflicts))
	}
	names := []string{r.Claimant}
	for _, m := range r.More {
		names = append(names, m.Claimant)
	}
	if !slices.Contains(names, holder) {
		f.t.Fatalf("claim %s: the refusal names %v, not %s", slug, names, holder)
	}
	f.notOpen(slug)
	return r
}

func (f *parkFx) granted(by SessionInfo, slug, basedOn string, scopes ...string) {
	f.t.Helper()
	_, conflicts, _, _, ferr := f.st.ClaimForecast(by.SessionID, by.Incarnation, ClaimRequest{Slug: slug, Scopes: scopes, BasedOn: basedOn})
	if ferr != nil || len(conflicts) != 0 {
		f.t.Fatalf("forecast of %s (based on %q): %v %+v", slug, basedOn, ferr, conflicts)
	}
	if err := f.claim(by, slug, basedOn, scopes...); err != nil {
		f.t.Fatalf("claim %s (based on %q): %v", slug, basedOn, err)
	}
}

func (f *parkFx) notOpen(slug string) {
	f.t.Helper()
	all, err := f.st.Claims(false)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, c := range all {
		if c.Slug == slug {
			f.t.Fatalf("a refused claim %q is open: %+v", slug, c)
		}
	}
}

func (f *parkFx) gate(path string, by SessionInfo) (string, bool) {
	f.t.Helper()
	c, held, err := f.st.OwnerOf(path, by.SessionID)
	if err != nil {
		f.t.Fatal(err)
	}
	return c.Slug, held
}

func (f *parkFx) gateHeldBy(path string, by SessionInfo, slug string) {
	f.t.Helper()
	got, held := f.gate(path, by)
	if !held || got != slug {
		f.t.Fatalf("gate on %s for %s: held %v by %q, want %q", path, by.Label, held, got, slug)
	}
}

// gateHeld is for a path two claims hold: which one the gate names is table order,
// and either refuses.
func (f *parkFx) gateHeld(path string, by SessionInfo) {
	f.t.Helper()
	if _, held := f.gate(path, by); !held {
		f.t.Fatalf("gate on %s for %s: free, want held", path, by.Label)
	}
}

func (f *parkFx) gateFree(path string, by SessionInfo) {
	f.t.Helper()
	if got, held := f.gate(path, by); held {
		f.t.Fatalf("gate on %s for %s: held by %q, want free", path, by.Label, got)
	}
}

func (f *parkFx) hand(from, to SessionInfo, slugs ...string) error {
	f.t.Helper()
	_, _, err := f.st.TransferClaims(from.SessionID, from.Incarnation, slugs, mustResolve(f.t, f.st, to.Label))
	return err
}

// The predicate's whole truth table: one function, so the doors cannot differ on it.
func TestParkedYieldsTruthTable(t *testing.T) {
	for _, tc := range []struct {
		name        string
		parked      string
		open        bool
		scope, decl string
		want        bool
	}{
		{"the rule", "R", true, "src/a.go", "R", true},
		{"not parked", "", true, "src/a.go", "R", false},
		{"not parked, nothing declared: '' never matches ''", "", true, "src/a.go", "", false},
		{"nothing declared", "R", true, "src/a.go", "", false},
		{"another run declared", "R", true, "src/a.go", "S", false},
		{"run closed", "R", false, "src/a.go", "R", false},
		{"a slot never yields", "R", true, ".buddy/slot/x", "R", false},
		{"the slots' own prefix", "R", true, ".buddy/slot", "R", false},
		{"a scope holding the slots", "R", true, ".buddy", "R", false},
		{"a name beside the slots is no slot", "R", true, ".buddy/slotty", "R", true},
	} {
		if got := parkedYields(tc.parked, tc.open, tc.scope, tc.decl); got != tc.want {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
	// exempts holds the pair whichever side is parked, and never a pair with no
	// parked side at all.
	parked := claimMarks{inRun: "R", runOpen: true}
	declaring := claimMarks{basedOn: "R"}
	if !declaring.exempts("src/a", "src/a", parked) || !parked.exempts("src/a", "src/a", declaring) {
		t.Fatal("the pair must hold whichever side is asking")
	}
	if declaring.exempts("src/a", "src/a", declaring) {
		t.Fatal("two claims declaring one run are two active claims, and collide")
	}
}

func TestParkedRiderYieldsOnlyToItsDeclaredRun(t *testing.T) {
	f := newParkFx(t)
	f.run(f.coord, "run-2")

	// The control: the same path, the same caller.
	f.refusedNaming(f.c, "next", "", "alpha", parkPath)
	f.refusedNaming(f.c, "next", "run-2", "alpha", parkPath)
	f.gateHeldBy(parkPath, f.c, "rider")

	f.granted(f.c, "next", parkRun, parkPath)
	f.gateFree(parkPath, f.c)
	// Overlap only where it was declared: the rider's other path still refuses
	// at the gate, because no claim of charlie's covers it.
	f.gateHeldBy("src/b.go", f.c, "rider")
	// And everyone else is still refused by both.
	f.refusedNaming(f.d, "other", "", "alpha", parkPath)
	f.gateHeld(parkPath, f.d)
	if got := claimBySlug(t, f.st, "rider"); got.State != "open" || got.InRunSlug != parkRun || !got.InRunOpen {
		t.Fatalf("the rider must stand, parked: %+v", got)
	}
	if got := claimBySlug(t, f.st, "next"); got.BasedOnSlug != parkRun {
		t.Fatalf("the declaration is not recorded: %+v", got)
	}
}

// A refused whole claim is still whole (D-001): one path exempt, one not.
func TestParkedRefusalIsWhole(t *testing.T) {
	f := newParkFx(t)
	if err := f.claim(f.d, "peer", "", "other/x.go"); err != nil {
		t.Fatal(err)
	}
	r := f.refusedNaming(f.c, "next", parkRun, "delta", parkPath, "other/x.go")
	if r.Scope != "other/x.go" || len(r.More) != 0 {
		t.Fatalf("only the unexempted path conflicts, and it must be named: %+v", r)
	}
	f.gateHeldBy(parkPath, f.c, "rider") // nothing was taken, so nothing was declared
	f.granted(f.c, "next", parkRun, parkPath)
}

// The door that takes no flag: the declaration is on the caller's own covering claim.
func TestGateYieldsOnlyToACoveringDeclaredClaim(t *testing.T) {
	st, _ := openTest(t)
	f := &parkFx{t: t, st: st, coord: hello(t, st, "sess-a", "alpha", "/wt/a"), lane: hello(t, st, "sess-b", "bravo", "/wt/b"),
		c: hello(t, st, "sess-c", "charlie", "/wt/c")}
	f.run(f.coord, parkRun)
	f.run(f.coord, "run-2")
	if err := st.Claim(f.lane.SessionID, f.lane.Incarnation, "rider", "d", []string{"pkg"}); err != nil {
		t.Fatal(err)
	}
	f.park(f.lane, f.coord, parkRun, "rider")

	f.gateHeldBy("pkg/sub/f.go", f.c, "rider")
	f.granted(f.c, "narrow", parkRun, "pkg/sub")
	f.gateFree("pkg/sub/f.go", f.c)
	f.gateHeldBy("pkg/other.go", f.c, "rider") // beside the claim
	f.gateHeldBy("pkg", f.c, "rider")          // above it: rules/section admits nothing about rules
	// A caller with no identity declares nothing.
	if _, held, _ := st.OwnerOf("pkg/sub/f.go", ""); !held {
		t.Fatal("an unidentified caller must still be refused")
	}
	// Another claim of the caller's, covering the path, declaring another run.
	f.refusedNaming(f.c, "wrong", "run-2", "alpha", "pkg/other.go")
}

// A yield leaves every other holder of the path standing. The coordinator may
// hold overlapping claims of its own, so a rider and an active claim, or riders
// of two runs, can sit on one path: clearing one must not clear the other.
func TestAYieldLeavesOtherHolders(t *testing.T) {
	f := newParkFx(t)
	if err := f.claim(f.coord, "second", "", parkPath); err != nil {
		t.Fatal(err)
	}
	// The claim door: the rider yields, the active claim does not.
	r := f.refusedNaming(f.c, "next", parkRun, "alpha", parkPath)
	if r.Slug != "second" || len(r.More) != 0 {
		t.Fatalf("only the active claim conflicts: %+v", r)
	}

	// The gate door, with a second rider. No verb reaches this row: a claim on
	// that path is refused once charlie holds it, so it is planted, and what is
	// under test is the loop's reading of it.
	f.run(f.coord, "run-2")
	for _, q := range []string{
		`UPDATE claims SET in_run=(SELECT claim_id FROM claims WHERE slug='run-2') WHERE slug='second'`,
		`INSERT INTO claims (claim_id, session_id, incarnation, slug, descr, created, renewed, state, based_on)
			SELECT 'planted', '` + f.c.SessionID + `', '` + f.c.Incarnation + `', 'planted', 'd', 1, 1, 'open', claim_id FROM claims WHERE slug='` + parkRun + `'`,
		`INSERT INTO claim_scopes (claim_id, scope, folded) VALUES ('planted', '` + parkPath + `', '` + parkPath + `')`,
	} {
		if _, err := f.st.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	f.gateHeldBy(parkPath, f.c, "second")
	if err := f.st.Release(f.coord.SessionID, f.coord.Incarnation, "second"); err != nil {
		t.Fatal(err)
	}
	f.gateFree(parkPath, f.c) // the control: with run-2's rider gone, the path is free
}

// A shared parked rider yields too, and a shared hold still needs a covering claim.
func TestSharedParkedRider(t *testing.T) {
	st, _ := openTest(t)
	f := &parkFx{t: t, st: st, coord: hello(t, st, "sess-a", "alpha", "/wt/a"), lane: hello(t, st, "sess-b", "bravo", "/wt/b"),
		c: hello(t, st, "sess-c", "charlie", "/wt/c")}
	f.run(f.coord, parkRun)
	if err := st.ClaimMode(f.lane.SessionID, f.lane.Incarnation, "rider-docs", "d", []string{"docs/x.md"}, true); err != nil {
		t.Fatal(err)
	}
	f.park(f.lane, f.coord, parkRun, "rider-docs")
	f.gateHeldBy("docs/x.md", f.c, "rider-docs")
	f.refusedNaming(f.c, "next", "", "alpha", "docs/x.md") // exclusive, undeclared
	f.granted(f.c, "next", parkRun, "docs/x.md")           // exclusive, declared
	f.gateFree("docs/x.md", f.c)
	// The exemption ended: an exclusive claim beside a shared rider is the one state
	// D-042 alone could never make, and owning a claim must not wave it through. The
	// consequence D-133 states holds for a shared rider as for an exclusive one.
	f.closeRun("fail")
	f.gateHeldBy("docs/x.md", f.c, "rider-docs")
}

// A shared claim beside a shared rider is D-042's own overlap, needing no
// exemption, so the end of the run changes nothing for it: the control for the
// denial above, which must come from the exclusive mode and not from the close.
func TestSharedDeclarerBesideASharedRiderOutlivesTheRun(t *testing.T) {
	st, _ := openTest(t)
	f := &parkFx{t: t, st: st, coord: hello(t, st, "sess-a", "alpha", "/wt/a"), lane: hello(t, st, "sess-b", "bravo", "/wt/b"),
		c: hello(t, st, "sess-c", "charlie", "/wt/c")}
	f.run(f.coord, parkRun)
	if err := st.ClaimMode(f.lane.SessionID, f.lane.Incarnation, "rider-docs", "d", []string{"docs/x.md"}, true); err != nil {
		t.Fatal(err)
	}
	f.park(f.lane, f.coord, parkRun, "rider-docs")
	if _, err := st.ClaimDeclared(f.c.SessionID, f.c.Incarnation, ClaimRequest{Slug: "next", Desc: "d", Scopes: []string{"docs/x.md"}, Shared: true, BasedOn: parkRun}); err != nil {
		t.Fatal(err)
	}
	f.gateFree("docs/x.md", f.c)
	f.closeRun("fail")
	f.gateFree("docs/x.md", f.c)
}

// An active peer's claim, a slot and a slug collision still refuse a declarer.
func TestDeclaringCallerStillRefusedByWhatIsNotParked(t *testing.T) {
	f := newParkFx(t)
	if err := f.claim(f.d, "active", "", "other/x.go"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, slug string
		scope      string
		holder     string
	}{
		{"an active peer's path", "n1", "other/x.go", "delta"},
		{"the run's own slot", "n2", SlotPrefix + "/" + parkRun, "alpha"},
		{"a slug the rider holds", "rider", "free/path.go", "alpha"},
		{"an active peer's slug", "active", "free/path.go", "delta"},
	} {
		f.refusedNaming2(tc.name, tc.slug, tc.holder, tc.scope)
	}
	// Two lanes declaring one run are two active claims: only the rider yields.
	f.granted(f.c, "next", parkRun, parkPath)
	f.refusedNaming(f.d, "second", parkRun, "charlie", parkPath)
	// And the coordinator's re-claim of its rider over the admitted lane is
	// refused: the claim door never reverses, and the mark stays.
	var r ErrRefused
	if err := f.claim(f.coord, "rider", "", parkPath, "src/b.go"); !errors.As(err, &r) || r.Claimant != "charlie" {
		t.Fatalf("a re-claim over the admitted lane: %v", err)
	}
	if got := claimBySlug(t, f.st, "rider"); got.InRunSlug != parkRun {
		t.Fatalf("a refused re-claim changed the mark: %+v", got)
	}
}

// refusedNaming2 is refusedNaming for a slug that may already be open: a slug
// collision leaves the holder's row, so notOpen cannot be asked.
func (f *parkFx) refusedNaming2(name, slug, holder, scope string) {
	f.t.Helper()
	err := f.claim(f.c, slug, parkRun, scope)
	var r ErrRefused
	if !errors.As(err, &r) || r.Claimant != holder {
		f.t.Fatalf("%s: slug %s scope %s: want refused by %s, got %v", name, slug, scope, holder, err)
	}
}

// Not parked does not yield: a READY lane holding its own claim, a plain handoff.
func TestAClaimNotParkedDoesNotYield(t *testing.T) {
	f := newParkFx(t)
	if err := f.claim(f.d, "ready-lane", "", "own/x.go"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.st.DeclareWaitReady(f.d.SessionID, f.d.Incarnation, []string{parkRun}, time.Hour, "", "abc"); err != nil {
		t.Fatal(err)
	}
	f.refusedNaming(f.c, "n", parkRun, "delta", "own/x.go")
	f.gateHeldBy("own/x.go", f.c, "ready-lane")
	if err := f.hand(f.d, f.coord, "ready-lane"); err != nil {
		t.Fatal(err)
	}
	f.refusedNaming(f.c, "n", parkRun, "alpha", "own/x.go")
	f.gateHeldBy("own/x.go", f.c, "ready-lane")
	// The control, same caller: the parked one yields.
	f.granted(f.c, "n", parkRun, parkPath)
}

func TestInRunRefusedAndNothingMoves(t *testing.T) {
	f := newParkFx(t)
	f.run(f.d, "dave-run")
	if err := f.claim(f.d, "not-a-run", "", "notes/x"); err != nil {
		t.Fatal(err)
	}
	if err := f.claim(f.coord, "closed-run", "", SlotPrefix+"/closed-run"); err != nil {
		t.Fatal(err)
	}
	if err := f.st.Release(f.coord.SessionID, f.coord.Incarnation, "closed-run"); err != nil {
		t.Fatal(err)
	}
	if err := f.claim(f.lane, "item", "", "item/x.go"); err != nil {
		t.Fatal(err)
	}
	if err := f.claim(f.lane, "with-slot", "", "item/y.go", SlotPrefix+"/mine"); err != nil {
		t.Fatal(err)
	}
	if err := f.claim(f.lane, "with-buddy", "", ".BUDDY/notes"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, slug, run, why string
	}{
		{"the recipient does not hold it", "item", "dave-run", "does not hold"},
		{"no such run", "item", "no-such", "no open claim"},
		{"the run has closed", "item", "closed-run", "no open claim"},
		{"an open claim that is no run", "item", "not-a-run", "not a run"},
		{"a claim holding a slot", "with-slot", parkRun, "never parked"},
		{"a claim under .buddy, any case", "with-buddy", parkRun, "never parked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := f.st.TransferClaimsInRun(f.lane.SessionID, f.lane.Incarnation, []string{tc.slug}, mustResolve(t, f.st, "alpha"), tc.run)
			var r ErrTransferRefused
			if !errors.As(err, &r) || !strings.Contains(r.Why, tc.why) {
				t.Fatalf("want a refusal saying %q, got %v", tc.why, err)
			}
			got := claimBySlug(t, f.st, tc.slug)
			if got.Owner.SessionID != f.lane.SessionID || got.InRun != "" || got.Producer != "" {
				t.Fatalf("nothing may move: %+v", got)
			}
		})
	}
	// A set is one transaction: a good claim beside a bad one stays too.
	if _, _, err := f.st.TransferClaimsInRun(f.lane.SessionID, f.lane.Incarnation, []string{"item", "with-slot"}, mustResolve(t, f.st, "alpha"), parkRun); err == nil {
		t.Fatal("a set holding a slot was parked")
	}
	if got := claimBySlug(t, f.st, "item"); got.Owner.SessionID != f.lane.SessionID || got.InRun != "" {
		t.Fatalf("a refused set moved a sibling: %+v", got)
	}
	// A run cannot park in itself, even moved in the same set.
	var selfPark ErrTransferRefused
	if _, _, err := f.st.TransferClaimsInRun(f.d.SessionID, f.d.Incarnation, []string{"dave-run", "not-a-run"}, mustResolve(t, f.st, "alpha"), "dave-run"); !errors.As(err, &selfPark) || !strings.Contains(selfPark.Why, "does not hold") {
		t.Fatalf("a run parked in itself must be refused by the recipient test: %v", err)
	}
	if err := f.hand(f.coord, f.d, parkRun); err != nil { // delta now holds run-1
		t.Fatal(err)
	}
	if _, _, err := f.st.TransferClaimsInRun(f.d.SessionID, f.d.Incarnation, []string{parkRun}, mustResolve(t, f.st, "alpha"), parkRun); !errors.As(err, &selfPark) || !strings.Contains(selfPark.Why, "does not hold") {
		t.Fatalf("a run parked in itself must be refused by the recipient test: %v", err)
	}
	// The positive control: the same claim, to the session that holds the run.
	f.park(f.lane, f.d, parkRun, "item")
	if got := claimBySlug(t, f.st, "item"); got.Owner.SessionID != f.d.SessionID || got.InRunSlug != parkRun {
		t.Fatalf("the control did not park: %+v", got)
	}
}

// Every way a run stops being open, and an ejection: the exemption ends at all
// three doors, overlaps already granted stand, and a later claim reports every
// blocker.
func TestExemptionEndsAtEveryDoor(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  func(f *parkFx)
	}{
		{"pass", func(f *parkFx) { f.closeRun("pass") }},
		{"fail", func(f *parkFx) { f.closeRun("fail") }},
		{"aborted", func(f *parkFx) { f.closeRun("aborted") }},
		{"released with no outcome", func(f *parkFx) { f.closeRun("") }},
		{"ejected while the run is open", func(f *parkFx) {
			run, standing, err := f.st.UnparkClaim(f.coord.SessionID, f.coord.Incarnation, "rider")
			if err != nil || run != parkRun {
				f.t.Fatalf("unpark: %q %v", run, err)
			}
			if len(standing) != 1 || standing[0].Slug != "next" {
				f.t.Fatalf("the overlap that stands must be named: %+v", standing)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newParkFx(t)
			f.granted(f.c, "next", parkRun, parkPath)
			f.gateFree(parkPath, f.c) // the control for every door below
			if err := f.claim(f.d, "beside", "", "other/x.go"); err != nil {
				t.Fatal(err)
			}
			// Door three's control is taken before the end: a declarer's handoff
			// passes while the exemption stands, then comes back to charlie.
			if err := f.hand(f.c, f.d, "next"); err != nil {
				t.Fatalf("handoff while the exemption stands: %v", err)
			}
			if err := f.hand(f.d, f.c, "next"); err != nil {
				t.Fatal(err)
			}

			tc.end(f)

			// Standing: nothing was revoked.
			for _, slug := range []string{"rider", "next"} {
				if got := claimBySlug(t, f.st, slug); got.State != "open" {
					t.Fatalf("%s: the ledger revokes nothing: %+v", slug, got)
				}
			}
			// allConflicts, on a path nobody admitted anybody to: a new claim, declaring
			// where the run is still open (the ejected arm) and nothing where it closed.
			f.refusedNaming(f.d, "late", parkRunOrEmpty(tc.name), "alpha", "src/b.go")
			// Every blocker, not the first.
			r := f.refusedNaming(f.d, "late", parkRunOrEmpty(tc.name), "alpha", parkPath, "src/b.go")
			if got := 1 + len(r.More); got != 3 {
				t.Fatalf("want 3 blockers (rider twice, next once), got %d: %+v", got, r)
			}
			// OwnerOf: the admitted lane's own edit on that path is now denied.
			f.gateHeldBy(parkPath, f.c, "rider")
			// scopeConflicts from a transfer: the admitted lane cannot hand on.
			var tr ErrRefused
			if err := f.hand(f.c, f.d, "next"); !errors.As(err, &tr) {
				t.Fatalf("a transfer past the exemption: %v", err)
			}
			if got := claimBySlug(t, f.st, "next"); got.Owner.SessionID != f.c.SessionID {
				t.Fatalf("a refused transfer moved the claim: %+v", got)
			}
			// Nor can the rider be handed to a successor over it.
			if err := f.hand(f.coord, f.d, "rider"); !errors.As(err, &tr) {
				t.Fatalf("a transfer of the rider past the exemption: %v", err)
			}
		})
	}
}

func parkRunOrEmpty(name string) string {
	if strings.HasPrefix(name, "ejected") {
		return parkRun // the run is open: a live declaration, and still no yield
	}
	return "" // a closed run declares nothing: TestBasedOnAClosedOrUnknownRun
}

func (f *parkFx) closeRun(outcome string) {
	f.t.Helper()
	if _, err := f.st.ReleaseOutcome(f.coord.SessionID, f.coord.Incarnation, parkRun, outcome, ""); err != nil {
		f.t.Fatal(err)
	}
}

// A run whose holder has said bye: the dry run cannot orphan it, the real claim
// does before it scans, and the two must agree. Parked here under a holder other
// than the run's, so the rider itself is not ended with it.
func TestRunWhoseHolderSaidByeIsClosedAtEveryDoor(t *testing.T) {
	f := newParkFx(t)
	if err := f.hand(f.coord, f.d, parkRun); err != nil { // delta holds the run, alpha the rider
		t.Fatal(err)
	}
	if got := claimBySlug(t, f.st, "rider"); got.InRunSlug != parkRun {
		t.Fatalf("the run moving alone must leave its rider parked: %+v", got)
	}
	f.granted(f.c, "next", parkRun, parkPath) // the control: still open
	f.gateFree(parkPath, f.c)
	if err := f.st.Bye(f.d.SessionID, f.d.Incarnation); err != nil {
		f.t.Fatal(err)
	}
	f.gateHeldBy(parkPath, f.c, "rider") // nothing orphaned it yet: the gate reads the bye
	_, conflicts, _, closed, err := f.st.ClaimForecast(f.lane.SessionID, f.lane.Incarnation, ClaimRequest{Slug: "late", Scopes: []string{"src/b.go"}, BasedOn: parkRun})
	if err != nil || len(conflicts) == 0 || !closed {
		t.Fatalf("the forecast must read that run closed: %v %+v closed=%v", err, conflicts, closed)
	}
	grant, err := f.st.ClaimDeclared(f.lane.SessionID, f.lane.Incarnation, ClaimRequest{Slug: "late", Desc: "d", Scopes: []string{"src/b.go"}, BasedOn: parkRun})
	var r ErrRefused
	if !errors.As(err, &r) || !grant.RunClosed {
		t.Fatalf("the claim must agree with its forecast: %v closed=%v", err, grant.RunClosed)
	}
}

// Keyed on the id: a run re-formed under one slug is another run.
func TestReformedRunDoesNotInheritRiders(t *testing.T) {
	f := newParkFx(t)
	f.closeRun("fail")
	f.run(f.coord, parkRun) // the same slug, a new claim
	f.refusedNaming(f.c, "next", parkRun, "alpha", parkPath)
	f.gateHeldBy(parkPath, f.c, "rider")
	if got := claimBySlug(t, f.st, "rider"); got.InRunOpen {
		t.Fatalf("the rider reads an old run as open: %+v", got)
	}
	// A declaration made against the OLD run does not carry across: with a lane
	// admitted over the rider, re-parking the rider in the re-formed run meets a
	// claim declaring a different id, so it is refused, whatever the slugs say.
	g := newParkFx(t)
	g.granted(g.c, "next", parkRun, parkPath)
	g.closeRun("fail")
	g.run(g.d, parkRun)
	var r ErrRefused
	if _, _, err := g.st.TransferClaimsInRun(g.coord.SessionID, g.coord.Incarnation, []string{"rider"}, mustResolve(t, g.st, "delta"), parkRun); !errors.As(err, &r) || r.Claimant != "charlie" {
		t.Fatalf("a declaration of the old run must not admit the new one: %v", err)
	}
}

// A dry run and its claim, on an open NON-run whose holder said bye: the claim
// orphans that row first and reads a closed claim, so the forecast has to as well.
func TestBasedOnAnEndedHoldersClaimForecastsAsTheClaimDoes(t *testing.T) {
	f := newParkFx(t)
	if err := f.claim(f.d, "not-a-run", "", "notes/x"); err != nil {
		t.Fatal(err)
	}
	var e ErrNotARun
	if _, _, _, _, err := f.st.ClaimForecast(f.c.SessionID, f.c.Incarnation, ClaimRequest{Slug: "n", Scopes: []string{"free/x.go"}, BasedOn: "not-a-run"}); !errors.As(err, &e) {
		t.Fatalf("the control, its holder live: %v", err)
	}
	if err := f.st.Bye(f.d.SessionID, f.d.Incarnation); err != nil {
		t.Fatal(err)
	}
	_, conflicts, _, closed, err := f.st.ClaimForecast(f.c.SessionID, f.c.Incarnation, ClaimRequest{Slug: "n", Scopes: []string{"free/x.go"}, BasedOn: "not-a-run"})
	if err != nil || len(conflicts) != 0 || !closed {
		t.Fatalf("the forecast: %v %+v closed=%v", err, conflicts, closed)
	}
	grant, err := f.st.ClaimDeclared(f.c.SessionID, f.c.Incarnation, ClaimRequest{Slug: "n", Desc: "d", Scopes: []string{"free/x.go"}, BasedOn: "not-a-run"})
	if err != nil || !grant.RunClosed {
		t.Fatalf("the claim its forecast promised: %v %+v", err, grant)
	}
}

func TestBasedOnAClosedOrUnknownRun(t *testing.T) {
	f := newParkFx(t)
	if err := f.claim(f.d, "not-a-run", "", "notes/x"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, run, why string }{
		{"no such claim", "no-such", "no claim of that name"},
		{"an open claim that is no run", "not-a-run", "not a run"},
		{"a rider is no run", "rider", "not a run"},
	} {
		err := f.claim(f.c, "n", tc.run, "free/x.go")
		var e ErrNotARun
		if !errors.As(err, &e) || !strings.Contains(err.Error(), tc.why) {
			t.Fatalf("%s: want ErrNotARun saying %q, got %v", tc.name, tc.why, err)
		}
		f.notOpen("n")
		if _, _, _, _, ferr := f.st.ClaimForecast(f.c.SessionID, f.c.Incarnation, ClaimRequest{Slug: "n", Scopes: []string{"free/x.go"}, BasedOn: tc.run}); !errors.As(ferr, &e) {
			t.Fatalf("%s: the forecast must refuse with the claim's own words: %v", tc.name, ferr)
		}
	}
	f.closeRun("pass")
	// Free path: granted, with nothing declared, and the caller is told.
	grant, err := f.st.ClaimDeclared(f.c.SessionID, f.c.Incarnation, ClaimRequest{Slug: "n", Desc: "d", Scopes: []string{"free/x.go"}, BasedOn: parkRun})
	if err != nil || !grant.RunClosed {
		t.Fatalf("a closed run on a free path: %v %+v", err, grant)
	}
	if got := claimBySlug(t, f.st, "n"); got.BasedOn != "" {
		t.Fatalf("a closed run was recorded as a declaration: %+v", got)
	}
	// A blocked path: every blocker, exit 4's error, and the same flag.
	r := f.refusedNaming(f.c, "m", parkRun, "alpha", parkPath, "src/b.go")
	if 1+len(r.More) != 2 {
		t.Fatalf("every blocker: %+v", r)
	}
}

// D-063's mid-run handoff, with an overlap standing: the pair holds whichever
// side moves, a plain --to keeps the mark only for the run's holder, and a fixer
// handed a rider withdraws it.
func TestTransferDoor(t *testing.T) {
	t.Run("the run and its riders move together", func(t *testing.T) {
		f := newParkFx(t)
		f.granted(f.c, "next", parkRun, parkPath)
		if err := f.hand(f.coord, f.d, parkRun, "rider"); err != nil {
			t.Fatalf("a handoff with an overlap standing: %v", err)
		}
		got := claimBySlug(t, f.st, "rider")
		if got.Owner.SessionID != f.d.SessionID || got.InRunSlug != parkRun || !got.InRunOpen || got.Producer != "bravo" {
			t.Fatalf("the mark and the producer stand: %+v", got)
		}
		f.gateFree(parkPath, f.c) // the exemption too, keyed on the id
		f.refusedNaming(f.lane, "x", "", "delta", "src/b.go")
	})
	t.Run("the run first, the riders after", func(t *testing.T) {
		f := newParkFx(t)
		f.granted(f.c, "next", parkRun, parkPath)
		if err := f.hand(f.coord, f.d, parkRun); err != nil {
			t.Fatal(err)
		}
		if err := f.hand(f.coord, f.d, "rider"); err != nil {
			t.Fatalf("the rider following its run: %v", err)
		}
		if got := claimBySlug(t, f.st, "rider"); got.InRunSlug != parkRun {
			t.Fatalf("the mark must stand: %+v", got)
		}
	})
	t.Run("a declaring claim moves", func(t *testing.T) {
		f := newParkFx(t)
		f.granted(f.c, "next", parkRun, parkPath)
		if err := f.hand(f.c, f.d, "next"); err != nil {
			t.Fatalf("a declarer's handoff: %v", err)
		}
		f.gateFree(parkPath, f.d)
		f.gateHeld(parkPath, f.c) // the old holder declares nothing now
		if got := claimBySlug(t, f.st, "next"); got.BasedOnSlug != parkRun {
			t.Fatalf("the declaration rides the claim: %+v", got)
		}
	})
	t.Run("a declaring claim parks in a later run, and stays declared", func(t *testing.T) {
		f := newParkFx(t)
		f.run(f.coord, "run-2")
		f.granted(f.c, "next", parkRun, parkPath)
		f.park(f.c, f.coord, "run-2", "next")
		got := claimBySlug(t, f.st, "next")
		if got.InRunSlug != "run-2" || got.BasedOnSlug != parkRun || got.Producer != "charlie" {
			t.Fatalf("both marks and the producer: %+v", got)
		}
		// A lane on run-2's tree is refused by the rider of run-1 it overlaps.
		f.refusedNaming(f.d, "third", "run-2", "alpha", parkPath)
	})
	t.Run("a rider handed to a fixer is withdrawn", func(t *testing.T) {
		f := newParkFx(t)
		if err := f.hand(f.coord, f.d, "rider"); err != nil {
			t.Fatal(err)
		}
		got := claimBySlug(t, f.st, "rider")
		if got.InRun != "" || got.Producer != "" {
			t.Fatalf("a fixer's claim is an ordinary claim, its work its own: %+v", got)
		}
		f.refusedNaming(f.c, "next", parkRun, "delta", parkPath)
	})
	t.Run("handed to a fixer over an admitted lane: refused, nothing moved", func(t *testing.T) {
		f := newParkFx(t)
		f.granted(f.c, "next", parkRun, parkPath)
		var r ErrRefused
		if err := f.hand(f.coord, f.d, "rider"); !errors.As(err, &r) || r.Claimant != "charlie" {
			t.Fatalf("want refused naming charlie: %v", err)
		}
		got := claimBySlug(t, f.st, "rider")
		if got.Owner.SessionID != f.coord.SessionID || got.InRunSlug != parkRun || got.Producer != "bravo" {
			t.Fatalf("a refused handoff changed the row: %+v", got)
		}
	})
}

func TestUnparkAndRefreshWithdraw(t *testing.T) {
	f := newParkFx(t)
	// Not the holder, or nothing parked: refused, nothing changed.
	if _, _, err := f.st.UnparkClaim(f.c.SessionID, f.c.Incarnation, "rider"); err == nil {
		t.Fatal("a stranger unparked a rider")
	}
	f.run(f.coord, "run-2")
	var np ErrNotParked
	if _, _, err := f.st.UnparkClaim(f.coord.SessionID, f.coord.Incarnation, "run-2"); !errors.As(err, &np) {
		t.Fatalf("an unparked claim: %v", err)
	}
	if got := claimBySlug(t, f.st, "rider"); got.InRunSlug != parkRun {
		t.Fatalf("a refused unpark changed the mark: %+v", got)
	}

	// A re-claim with no overlap succeeds and withdraws, saying so; the id and
	// the producer stand.
	before := claimBySlug(t, f.st, "rider")
	grant, err := f.st.ClaimDeclared(f.coord.SessionID, f.coord.Incarnation, ClaimRequest{Slug: "rider", Desc: "d", Scopes: []string{parkPath, "src/b.go"}})
	if err != nil || grant.Unparked != parkRun {
		t.Fatalf("a refresh of a parked rider: %v %+v", err, grant)
	}
	got := claimBySlug(t, f.st, "rider")
	if got.ClaimID != before.ClaimID || got.InRun != "" || got.Producer != "bravo" {
		t.Fatalf("after the refresh: %+v", got)
	}
	f.refusedNaming(f.c, "next", parkRun, "alpha", parkPath)
}

// A refresh takes what it is given (D-042's rule for the mode): a lane renewing
// without --based-on drops the declaration, and the rider then refuses it.
func TestRefreshWithoutTheDeclarationIsRefused(t *testing.T) {
	f := newParkFx(t)
	f.granted(f.c, "next", parkRun, parkPath)
	if err := f.claim(f.c, "next", "", parkPath); err == nil {
		t.Fatal("a re-claim without the declaration was granted over the rider")
	}
	if got := claimBySlug(t, f.st, "next"); got.BasedOnSlug != parkRun {
		t.Fatalf("a refused refresh changed the declaration: %+v", got)
	}
	if err := f.claim(f.c, "next", parkRun, parkPath); err != nil {
		t.Fatalf("the control, the same re-claim declaring: %v", err)
	}
	// A GRANTED refresh takes what it is given too: on a path nothing overlaps, the
	// same re-claim succeeds and the declaration is gone, not carried. The refused
	// arm above cannot see this: a refused refresh writes nothing either way.
	if err := f.claim(f.c, "free", parkRun, "free/x.go"); err != nil {
		t.Fatal(err)
	}
	if got := claimBySlug(t, f.st, "free"); got.BasedOnSlug != parkRun {
		t.Fatalf("the control: declared on a free path: %+v", got)
	}
	if err := f.claim(f.c, "free", "", "free/x.go"); err != nil {
		t.Fatal(err)
	}
	if got := claimBySlug(t, f.st, "free"); got.BasedOn != "" || got.BasedOnSlug != "" {
		t.Fatalf("a refresh without the flag kept the declaration: %+v", got)
	}
}

// An unpark ejects the rider, and the work is still the lane's: the producer stays,
// so the model report keeps attributing it.
func TestUnparkKeepsTheProducer(t *testing.T) {
	f := newParkFx(t)
	if got := claimBySlug(t, f.st, "rider"); got.Producer != "bravo" || got.InRunSlug != parkRun {
		t.Fatalf("the control, before the unpark: %+v", got)
	}
	if _, _, err := f.st.UnparkClaim(f.coord.SessionID, f.coord.Incarnation, "rider"); err != nil {
		t.Fatal(err)
	}
	if got := claimBySlug(t, f.st, "rider"); got.Producer != "bravo" || got.InRun != "" || got.State != "open" {
		t.Fatalf("after the unpark: %+v", got)
	}
}

// The overlaps an unpark names as standing are the ones the gate would refuse. A
// claim parked in one run can declare another, and the unpark leaves the declaration,
// so the rider of the declared run still yields to it (Codex code pass). The control
// that a real overlap IS named: the ejected arm of TestExemptionEndsAtEveryDoor.
func TestUnparkNamesOnlyOverlapsThatStand(t *testing.T) {
	f := newParkFx(t)
	f.granted(f.c, "next", parkRun, parkPath)
	f.run(f.d, "run-2")
	f.park(f.c, f.d, "run-2", "next") // parked in run-2, still declaring run-1
	_, standing, err := f.st.UnparkClaim(f.d.SessionID, f.d.Incarnation, "next")
	if err != nil {
		t.Fatal(err)
	}
	f.gateFree(parkPath, f.d) // what the gate says, which the report must match
	if len(standing) != 0 {
		t.Fatalf("an overlap reported as standing that the gate does not refuse: %+v", standing)
	}
}

// Schema 17's job and this item's declaration are one claim's business: one request
// carries both, a refused one declares neither, and a job rides the park and a
// refresh that names none (D-131's rule, through the path this item added).
func TestJobAndDeclarationOnOneClaim(t *testing.T) {
	f := newParkFx(t)
	job := &JobDecl{PID: 42, Born: 7, Boot: "boot"}
	req := ClaimRequest{Slug: "next", Desc: "d", Scopes: []string{parkPath}, BasedOn: parkRun, Job: job}
	if _, err := f.st.ClaimDeclared(f.c.SessionID, f.c.Incarnation, req); err != nil {
		t.Fatal(err)
	}
	if got := claimBySlug(t, f.st, "next"); got.Job.PID != 42 || got.BasedOnSlug != parkRun {
		t.Fatalf("both on one claim: %+v", got)
	}
	var r ErrRefused
	refused := ClaimRequest{Slug: "later", Desc: "d", Scopes: []string{parkPath}, Job: job}
	if _, err := f.st.ClaimDeclared(f.d.SessionID, f.d.Incarnation, refused); !errors.As(err, &r) {
		t.Fatalf("the control, undeclared: %v", err)
	}
	f.notOpen("later")
	if _, err := f.st.ClaimDeclared(f.c.SessionID, f.c.Incarnation, ClaimRequest{Slug: "next", Desc: "e", Scopes: []string{parkPath}, BasedOn: parkRun}); err != nil {
		t.Fatal(err)
	}
	if got := claimBySlug(t, f.st, "next"); got.Job.PID != 42 {
		t.Fatalf("a refresh naming no job must keep it: %+v", got)
	}
	f.run(f.d, "run-2")
	f.park(f.c, f.d, "run-2", "next")
	if got := claimBySlug(t, f.st, "next"); got.Job.PID != 42 || got.InRunSlug != "run-2" {
		t.Fatalf("a park must carry the job: %+v", got)
	}
}

// The one place the MOVED claim's own run-open read decides anything: a plain --to
// to whoever holds the rider's run keeps the mark, and if that run has closed, the
// rider must not yield to the lane it admitted. Riders and runs have separate
// holders here, as they do after a run alone was handed to a successor.
func TestMovedRiderWithAClosedRunDoesNotYield(t *testing.T) {
	setup := func(t *testing.T) *parkFx {
		f := newParkFx(t)
		if err := f.hand(f.coord, f.d, parkRun); err != nil { // delta holds the run, alpha the rider
			t.Fatal(err)
		}
		return f
	}
	closeByDelta := func(f *parkFx) {
		f.t.Helper()
		if _, err := f.st.ReleaseOutcome(f.d.SessionID, f.d.Incarnation, parkRun, "fail", ""); err != nil {
			f.t.Fatal(err)
		}
	}
	t.Run("control: run open, the same move yields", func(t *testing.T) {
		f := setup(t)
		f.granted(f.c, "next", parkRun, parkPath)
		if err := f.hand(f.coord, f.d, "rider"); err != nil {
			t.Fatalf("a rider moving to its run's holder over an admitted lane: %v", err)
		}
		if got := claimBySlug(t, f.st, "rider"); got.InRunSlug != parkRun || !got.InRunOpen {
			t.Fatalf("the mark must stand: %+v", got)
		}
	})
	t.Run("run closed, an admitted lane: refused, nothing moves", func(t *testing.T) {
		f := setup(t)
		f.granted(f.c, "next", parkRun, parkPath)
		closeByDelta(f)
		var r ErrRefused
		if err := f.hand(f.coord, f.d, "rider"); !errors.As(err, &r) || r.Claimant != "charlie" {
			t.Fatalf("a moved rider read a closed run as open: %v", err)
		}
		if got := claimBySlug(t, f.st, "rider"); got.Owner.SessionID != f.coord.SessionID || got.InRunSlug != parkRun {
			t.Fatalf("a refused transfer moved the claim: %+v", got)
		}
	})
	t.Run("run closed, no overlap: moves, keeps the mark, reads closed", func(t *testing.T) {
		f := setup(t)
		closeByDelta(f)
		if err := f.hand(f.coord, f.d, "rider"); err != nil {
			t.Fatal(err)
		}
		if got := claimBySlug(t, f.st, "rider"); got.InRunSlug != parkRun || got.InRunOpen || got.Producer != "bravo" {
			t.Fatalf("to the closed run's holder: %+v", got)
		}
	})
}

// Invariant 11: staleness marks. A plain sweep reaps no parked claim, and a stale
// one refuses a non-declarer like a fresh one.
func TestStaleParkedClaimIsNotReaped(t *testing.T) {
	f := newParkFx(t)
	f.clk.advance(3 * StaleAfter)
	if _, err := f.st.Sweep(time.Hour, 365*24*time.Hour, SweepOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := claimBySlug(t, f.st, "rider"); got.State != "open" || got.InRunSlug != parkRun {
		t.Fatalf("a sweep touched a stale parked claim: %+v", got)
	}
	f.refusedNaming(f.c, "next", "", "alpha", parkPath)
	f.gateHeldBy(parkPath, f.c, "rider")
	f.granted(f.c, "next", parkRun, parkPath)
}

// The first park records the producer, a re-park keeps it, and its release
// carries its outcome under the claim the lane opened.
func TestProducerSurvivesAndOutcomeLands(t *testing.T) {
	f := newParkFx(t)
	read := func(slug string) (session, inc, outcome string) {
		t.Helper()
		if err := f.st.db.QueryRow(`SELECT producer_session, producer_incarnation, outcome FROM claims WHERE slug=?`, slug).
			Scan(&session, &inc, &outcome); err != nil {
			t.Fatal(err)
		}
		return
	}
	s, i, _ := read("rider")
	if s != f.lane.SessionID || i != f.lane.Incarnation {
		t.Fatalf("the producer is not the lane: %q %q", s, i)
	}
	id := claimBySlug(t, f.st, "rider").ClaimID
	f.run(f.d, "run-3")
	f.park(f.coord, f.d, "run-3", "rider")
	s, i, _ = read("rider")
	if s != f.lane.SessionID || i != f.lane.Incarnation {
		t.Fatalf("a re-park replaced the producer: %q %q", s, i)
	}
	if got := claimBySlug(t, f.st, "rider"); got.ClaimID != id || got.InRunSlug != "run-3" {
		t.Fatalf("a re-park: %+v", got)
	}
	gotID, err := f.st.ReleaseOutcome(f.d.SessionID, f.d.Incarnation, "rider", "pass", "landed")
	if err != nil || gotID != id {
		t.Fatalf("the item's own release: %q %v", gotID, err)
	}
	if _, _, o := read("rider"); o != "pass" {
		t.Fatalf("outcome %q", o)
	}
}

// Issue #63: a holder parks its own claim in a run it holds, with no transfer.

func (f *parkFx) parkHere(by SessionInfo, run string, slugs ...string) []Conflict {
	f.t.Helper()
	standing, err := f.st.ParkClaims(by.SessionID, by.Incarnation, slugs, run)
	if err != nil {
		f.t.Fatalf("park %v in %s in place: %v", slugs, run, err)
	}
	return standing
}

func (f *parkFx) closeRunOf(by SessionInfo, slug string) {
	f.t.Helper()
	if _, err := f.st.ReleaseOutcome(by.SessionID, by.Incarnation, slug, "fail", "red"); err != nil {
		f.t.Fatal(err)
	}
}

func standingSlugs(standing []Conflict) []string {
	var out []string
	for _, c := range standing {
		out = append(out, c.Slug)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// The issue's case: a run closes on a red, and its coordinator puts the rider in
// the next run it forms. At every door the claim then yields to that run's declarer
// and to no one else.
func TestParkInPlaceAfterTheRunClosed(t *testing.T) {
	f := newParkFx(t)
	before := claimBySlug(t, f.st, "rider")
	f.closeRunOf(f.coord, parkRun)
	f.run(f.coord, "run-2")
	f.refusedNaming(f.c, "next", "run-2", "alpha", parkPath) // the control: closed run, it refuses

	f.clk.advance(time.Hour)
	if standing := f.parkHere(f.coord, "run-2", "rider"); len(standing) != 0 {
		t.Fatalf("nothing overlaps it yet: %+v", standing)
	}
	got := claimBySlug(t, f.st, "rider")
	if got.ClaimID != before.ClaimID || got.InRunSlug != "run-2" || !got.InRunOpen || got.State != "open" || got.Owner.SessionID != f.coord.SessionID {
		t.Fatalf("after the park: %+v", got)
	}
	if !got.Renewed.Equal(f.clk.now()) {
		t.Fatalf("a park is the holder's explicit act, so it renews: renewed %v, now %v", got.Renewed, f.clk.now())
	}

	f.refusedNaming(f.d, "plain", "", "alpha", parkPath) // no declaration
	f.gateHeldBy(parkPath, f.c, "rider")                 // the gate's control: no claim of its own yet
	f.granted(f.c, "next", "run-2", parkPath)            // the claim door
	f.gateFree(parkPath, f.c)                            // the gate
	f.gateHeld(parkPath, f.d)                            // and it still refuses a third session

	// The transfer door, by the pair rule: the run and its rider move together
	// over the admitted lane, as D-063's handoff does for a rider parked by --to.
	if err := f.hand(f.coord, f.d, "run-2", "rider"); err != nil {
		t.Fatalf("a handoff of the run and its re-parked rider: %v", err)
	}
	if got := claimBySlug(t, f.st, "rider"); got.InRunSlug != "run-2" {
		t.Fatalf("the handoff lost the mark: %+v", got)
	}
}

// A rider ejected on a red and put back. The same open run restores its yield: the
// coordinator is saying the work is in that run again.
func TestParkInPlaceAfterAnUnpark(t *testing.T) {
	f := newParkFx(t)
	f.granted(f.c, "next", parkRun, parkPath)
	if _, _, err := f.st.UnparkClaim(f.coord.SessionID, f.coord.Incarnation, "rider"); err != nil {
		t.Fatal(err)
	}
	f.gateHeld(parkPath, f.c) // the control: ejected, it refuses the lane again
	if standing := f.parkHere(f.coord, parkRun, "rider"); len(standing) != 0 {
		t.Fatalf("back in the run its declarer names, nothing stands: %+v", standing)
	}
	f.gateFree(parkPath, f.c)
}

// Several slugs are one transaction, a repeat counts once, and parking a claim
// already in that run changes nothing that matters.
func TestParkInPlaceSeveral(t *testing.T) {
	f := newParkFx(t)
	if err := f.claim(f.coord, "own", "", "own/x.go"); err != nil {
		t.Fatal(err)
	}
	f.parkHere(f.coord, parkRun, "rider", "own", "rider")
	for _, slug := range []string{"rider", "own"} {
		if got := claimBySlug(t, f.st, slug); got.InRunSlug != parkRun || !got.InRunOpen {
			t.Fatalf("%s: %+v", slug, got)
		}
	}
	f.granted(f.c, "next", parkRun, parkPath, "own/x.go")
}

// Everything parkTarget refuses at the transfer door it refuses here, and nothing
// changes: every claim keeps its mark. The last call is the control: the same
// claims and a run the caller holds, granted.
func TestParkInPlaceRefusedAndNothingChanges(t *testing.T) {
	f := newParkFx(t)
	f.run(f.d, "run-d")
	f.run(f.coord, "run-2")
	for _, c := range []struct {
		by     SessionInfo
		slug   string
		scopes []string
	}{
		{f.coord, "plain", []string{"notes/x"}},
		{f.coord, "mine", []string{"own/x.go"}},
		{f.coord, "slotted", []string{"own/y.go", SlotPrefix + "/thing"}},
		{f.c, "theirs", []string{"theirs/x.go"}},
		{f.coord, "gone", []string{"gone/x.go"}},
	} {
		if err := f.claim(c.by, c.slug, "", c.scopes...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.st.ReleaseOutcome(f.coord.SessionID, f.coord.Incarnation, "gone", "pass", "n"); err != nil {
		t.Fatal(err)
	}
	f.run(f.coord, "run-gone")
	f.closeRunOf(f.coord, "run-gone")
	for _, tc := range []struct {
		name  string
		by    SessionInfo
		slugs []string
		run   string
		want  string
	}{
		{"a run someone else holds", f.coord, []string{"mine"}, "run-d", "does not hold that run"},
		{"no such run", f.coord, []string{"mine"}, "no-such", "no open claim of that name"},
		{"a closed run", f.coord, []string{"mine"}, "run-gone", "no open claim of that name"},
		{"an open claim that is no run", f.coord, []string{"mine"}, "plain", "not a run"},
		{"a run parked in itself", f.coord, []string{"run-2"}, "run-2", "never parked"},
		{"a run parked in another", f.coord, []string{"run-2"}, parkRun, "never parked"},
		{"a claim carrying a slot", f.coord, []string{"slotted"}, "run-2", "never parked"},
		{"another session's claim", f.d, []string{"theirs"}, "run-d", "held by charlie"},
		{"your own closed claim", f.coord, []string{"gone"}, "run-2", "already released"},
		{"one bad slug of several", f.coord, []string{"rider", "mine", "slotted"}, "run-2", "never parked"},
		{"one unknown slug of several", f.coord, []string{"rider", "no-such-claim"}, "run-2", "no claim \"no-such-claim\""},
	} {
		_, err := f.st.ParkClaims(tc.by.SessionID, tc.by.Incarnation, tc.slugs, tc.run)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %v, want %q", tc.name, err, tc.want)
		}
		if got := claimBySlug(t, f.st, "rider"); got.InRunSlug != parkRun {
			t.Fatalf("%s moved the rider's mark: %+v", tc.name, got)
		}
		for _, slug := range []string{"mine", "slotted", "theirs", "run-2"} {
			if got := claimBySlug(t, f.st, slug); got.InRun != "" {
				t.Fatalf("%s marked %s: %+v", tc.name, slug, got)
			}
		}
		var closedMark string
		if err := f.st.db.QueryRow(`SELECT in_run FROM claims WHERE slug='gone'`).Scan(&closedMark); err != nil || closedMark != "" {
			t.Fatalf("%s marked a closed claim: %q %v", tc.name, closedMark, err)
		}
	}
	// Liveness: a dead incarnation's leftover call parks nothing.
	if _, err := f.st.ParkClaims(f.coord.SessionID, "not-the-incarnation", []string{"mine"}, "run-2"); err == nil || !strings.Contains(err.Error(), "not live") {
		t.Fatalf("a stale incarnation: %v", err)
	}
	// A transfer to yourself parks nothing either, and its words name this door.
	if _, _, err := f.st.TransferClaimsInRun(f.coord.SessionID, f.coord.Incarnation, []string{"mine"}, mustResolve(t, f.st, f.coord.Label), "run-2"); err == nil || !strings.Contains(err.Error(), "no --to") {
		t.Fatalf("--to yourself --in-run: %v", err)
	}
	if got := claimBySlug(t, f.st, "mine"); got.InRun != "" {
		t.Fatalf("a refused call marked it: %+v", got)
	}
	// Only --in-run earns that hint: a plain --to yourself has no park to redirect.
	if err := f.hand(f.coord, f.coord, "mine"); err == nil || strings.Contains(err.Error(), "no --to") {
		t.Fatalf("a plain --to yourself: %v", err)
	}
	f.parkHere(f.coord, "run-2", "rider", "mine") // the control
	if got := claimBySlug(t, f.st, "mine"); got.InRunSlug != "run-2" {
		t.Fatalf("the control: %+v", got)
	}
}

// A claim on exactly .buddy is coordination too, which the row on a slot does not
// reach: it needs a fixture where nobody else holds anything under .buddy.
func TestParkInPlaceRefusesTheBuddyDirItself(t *testing.T) {
	f := newParkFx(t)
	if err := f.claim(f.coord, "dotted", "", ".buddy"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.ParkClaims(f.coord.SessionID, f.coord.Incarnation, []string{"dotted"}, parkRun); err == nil || !strings.Contains(err.Error(), "never parked") {
		t.Fatalf("a claim on .buddy: %v", err)
	}
	if got := claimBySlug(t, f.st, "dotted"); got.InRun != "" {
		t.Fatalf("marked: %+v", got)
	}
	f.parkHere(f.coord, parkRun, "rider") // the control
}

// An ended session under its OWN incarnation, which the slug lookup alone would
// pass while the claim row is open: only the liveness check stops this park.
func TestParkInPlaceRefusesAnEndedSession(t *testing.T) {
	f := newParkFx(t)
	if err := f.claim(f.coord, "mine", "", "own/x.go"); err != nil {
		t.Fatal(err)
	}
	if err := f.st.Bye(f.coord.SessionID, f.coord.Incarnation); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.ParkClaims(f.coord.SessionID, f.coord.Incarnation, []string{"mine"}, parkRun); err == nil || !strings.Contains(err.Error(), "not live") {
		t.Fatalf("an ended session: %v", err)
	}
	if got := claimBySlug(t, f.st, "mine"); got.InRun != "" {
		t.Fatalf("marked: %+v", got)
	}
}

// The design's question: re-parking must not admit a declaration of the old run.
// The mark is keyed on the run claim's id, so neither the next run nor a run
// re-formed under the old slug admits it, and the coordinator is told it stands.
func TestParkInPlaceDoesNotAdmitAnOldDeclaration(t *testing.T) {
	f := newParkFx(t)
	f.granted(f.c, "next", parkRun, parkPath)
	f.gateFree(parkPath, f.c) // the control: admitted while its run holds the rider

	// Moved from the open run to another: the yield ends, reported.
	f.run(f.coord, "run-2")
	standing := f.parkHere(f.coord, "run-2", "rider")
	if got := standingSlugs(standing); !slices.Equal(got, []string{"next"}) || standing[0].Claimant != "charlie" {
		t.Fatalf("standing: %+v", standing)
	}
	f.gateHeldBy(parkPath, f.c, "rider")

	// The run closes and comes back under its own slug: a new claim, a new id.
	f.closeRunOf(f.coord, parkRun)
	f.run(f.coord, parkRun)
	standing = f.parkHere(f.coord, parkRun, "rider")
	if got := standingSlugs(standing); !slices.Equal(got, []string{"next"}) {
		t.Fatalf("standing, under a re-formed slug: %+v", standing)
	}
	f.gateHeldBy(parkPath, f.c, "rider")

	// Admitted again only by saying so: the re-formed run's declaration is a new one.
	if err := f.claim(f.c, "next", "", parkPath); err == nil {
		t.Fatal("a re-claim with no declaration must be refused")
	}
	if err := f.claim(f.c, "next", parkRun, parkPath); err != nil {
		t.Fatalf("a re-claim declaring the run the rider is parked in: %v", err)
	}
	f.gateFree(parkPath, f.c)
}

// A standing overlap is a pair of claims, not a session's verdict: charlie's old
// claim gets no yield, and its new one does, so the gate passes it (Codex design
// pass, this sequence). The report names the old claim and not the new.
func TestParkInPlaceReportsPairsNotVerdicts(t *testing.T) {
	f := newParkFx(t)
	f.run(f.coord, "run-2")
	f.granted(f.c, "old", parkRun, parkPath)
	f.parkHere(f.coord, "run-2", "rider")
	f.gateHeldBy(parkPath, f.c, "rider") // the control: the old claim alone
	f.granted(f.c, "new", "run-2", parkPath)
	f.gateFree(parkPath, f.c)
	if got := standingSlugs(f.parkHere(f.coord, "run-2", "rider")); !slices.Equal(got, []string{"old"}) {
		t.Fatalf("standing: %v", got)
	}
}

// The first park sets the producer, a re-park keeps it, and this door writes them
// as the transfer door does. f.lane is bravo, f.c the fixer, f.coord alpha.
func TestParkInPlaceProducer(t *testing.T) {
	read := func(t *testing.T, f *parkFx, slug string) (session, inc string) {
		t.Helper()
		if err := f.st.db.QueryRow(`SELECT producer_session, producer_incarnation FROM claims WHERE slug=?`, slug).Scan(&session, &inc); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Run("a re-park keeps the lane", func(t *testing.T) {
		f := newParkFx(t)
		f.closeRunOf(f.coord, parkRun)
		f.run(f.coord, "run-2")
		f.parkHere(f.coord, "run-2", "rider")
		if s, i := read(t, f, "rider"); s != f.lane.SessionID || i != f.lane.Incarnation {
			t.Fatalf("%q %q", s, i)
		}
	})
	t.Run("an unpark and a re-park keep the lane", func(t *testing.T) {
		f := newParkFx(t)
		if _, _, err := f.st.UnparkClaim(f.coord.SessionID, f.coord.Incarnation, "rider"); err != nil {
			t.Fatal(err)
		}
		f.parkHere(f.coord, parkRun, "rider")
		if s, _ := read(t, f, "rider"); s != f.lane.SessionID {
			t.Fatalf("%q", s)
		}
	})
	t.Run("a fixer handed it plain becomes the producer, and a re-park keeps that", func(t *testing.T) {
		f := newParkFx(t)
		if _, _, err := f.st.UnparkClaim(f.coord.SessionID, f.coord.Incarnation, "rider"); err != nil {
			t.Fatal(err)
		}
		if err := f.hand(f.coord, f.c, "rider"); err != nil {
			t.Fatal(err)
		}
		if s, _ := read(t, f, "rider"); s != "" {
			t.Fatalf("a plain --to clears it: %q", s)
		}
		f.park(f.c, f.coord, parkRun, "rider")
		f.parkHere(f.coord, parkRun, "rider")
		if s, i := read(t, f, "rider"); s != f.c.SessionID || i != f.c.Incarnation {
			t.Fatalf("%q %q", s, i)
		}
	})
	t.Run("the caller's own work and a plain hand-on: the holder at the first park", func(t *testing.T) {
		f := newParkFx(t)
		if err := f.claim(f.coord, "own", "", "own/x.go"); err != nil {
			t.Fatal(err)
		}
		if err := f.claim(f.lane, "handed", "", "handed/x.go"); err != nil {
			t.Fatal(err)
		}
		if err := f.hand(f.lane, f.coord, "handed"); err != nil {
			t.Fatal(err)
		}
		f.parkHere(f.coord, parkRun, "own", "handed")
		for _, slug := range []string{"own", "handed"} {
			if s, i := read(t, f, slug); s != f.coord.SessionID || i != f.coord.Incarnation {
				t.Fatalf("%s: %q %q", slug, s, i)
			}
		}
	})
}

// RECORDED OPEN, a crafted form (D-133's amendment): two holders who each park a
// claim declaring the other's run yield to each other, so two exclusive claims both
// pass the gate. Each step is the rule's, and no recipe cross-declares. The transfer
// door reached this with a third session; this door takes two. Pinned so a fix, which
// would have to change the rule itself, has to say so. The controls are the two
// refusals before the marks cross.
func TestCrossDeclaredParksYieldToEachOther(t *testing.T) {
	f := newParkFx(t)
	f.run(f.d, "run-9")
	if err := f.claim(f.coord, "own", "", "own/x.go"); err != nil {
		t.Fatal(err)
	}
	f.refusedNaming(f.d, "held", parkRun, "alpha", "own/x.go") // the control: not parked yet
	f.parkHere(f.coord, parkRun, "own")
	f.granted(f.d, "held", parkRun, "own/x.go")
	f.gateHeldBy("own/x.go", f.coord, "held") // the control: the declarer refuses alpha
	f.parkHere(f.d, "run-9", "held")
	if err := f.claim(f.coord, "own", "run-9", "own/x.go"); err != nil {
		t.Fatal(err)
	}
	if standing := f.parkHere(f.coord, parkRun, "own"); len(standing) != 0 {
		t.Fatalf("standing: %+v", standing)
	}
	f.gateFree("own/x.go", f.coord)
	f.gateFree("own/x.go", f.d)
}

// A coordinator's own work comes free with this door, and it is held by the rule
// that holds a rider: it yields only to a declarer, and the declarer's active claim
// still refuses the coordinator.
func TestParkInPlaceOwnWork(t *testing.T) {
	f := newParkFx(t)
	if err := f.claim(f.coord, "own", "", "own/x.go"); err != nil {
		t.Fatal(err)
	}
	f.refusedNaming(f.c, "next", parkRun, "alpha", "own/x.go") // the control
	f.parkHere(f.coord, parkRun, "own")
	f.refusedNaming(f.d, "plain", "", "alpha", "own/x.go")
	f.granted(f.c, "next", parkRun, "own/x.go")
	f.gateFree("own/x.go", f.c)
	f.gateHeldBy("own/x.go", f.coord, "next")
}

// A ledger at the schema before this one: its claims gain the columns, and every
// row refuses as it did.
func TestMigrationToSchema18(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.Claim(a.SessionID, a.Incarnation, "old", "d", []string{"src/a.go"}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`ALTER TABLE claims DROP COLUMN in_run`, `ALTER TABLE claims DROP COLUMN based_on`,
		`ALTER TABLE claims DROP COLUMN producer_session`, `ALTER TABLE claims DROP COLUMN producer_incarnation`,
		`PRAGMA user_version = 17`, // the literal: schemaVersion-1 skips this arm after a later bump
	} {
		if _, err := st.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := migrate(st.db); err != nil {
		t.Fatal(err)
	}
	var ver int
	if err := st.db.QueryRow(`PRAGMA user_version`).Scan(&ver); err != nil || ver != schemaVersion {
		t.Fatalf("user_version %d %v", ver, err)
	}
	got := claimBySlug(t, st, "old")
	if got.InRun != "" || got.BasedOn != "" || got.Producer != "" || got.State != "open" {
		t.Fatalf("a migrated row changed meaning: %+v", got)
	}
	var r ErrRefused
	if err := st.Claim(b.SessionID, b.Incarnation, "n", "d", []string{"src/a.go"}); !errors.As(err, &r) {
		t.Fatalf("a migrated row must refuse: %v", err)
	}
}
