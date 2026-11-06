package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// A parked rider claim (D-133, issue #61): `release <item> --to <coordinator>
// --in-run <run>`, and the lane that may claim over it: `claim --based-on <run>`.
//
// THE FAILURE. A lane hands its claims to the coordinator at READY so its pane
// can close. The coordinator then holds every READY lane's paths until the run
// lands, and a new lane branching from the run's tree (the fleet skill's "never
// hold new lanes for a run in flight") is refused on paths whose work is already
// in that tree. Six hand releases in one run, each a round trip through a busy
// coordinator (the issue's figure, not re-measured).
//
// WHY NOT THE GIVE-BACK. The first shape took no schema: the coordinator handed
// the path back with `release --scope`. That frees the path for EVERYONE: a lane
// on an older tree is granted it too, and the coordinator never hears of it,
// because no refusal happens (Codex, consensus round 1). The claim does two jobs,
// the path and the item's outcome, and a whole release at READY loses the second:
// a red finds no holder and no claim for the outcome.
//
// THE RULE, one sentence, on a PAIR of claims: a claim parked in run R yields to
// a claim declaring R, while R is open. parkedYields is the one spelling of it,
// and all three doors call it:
//   - allConflicts, for `claim` and its dry run, through scopeConflicts;
//   - scopeConflicts from TransferClaims, for `release --to`;
//   - OwnerOf, for the gate and the commit gate.
//
// DIRECTIONAL, and that is the part easy to get wrong. Only a parked claim ever
// yields. A declaring claim is an ordinary active claim that refuses everyone,
// so two lanes declaring R still collide, and a coordinator re-claiming its
// parked rider over a lane it admitted is refused. Which claim is MOVING is a
// different question: at the transfer door the pair may be either way round,
// because `release <run> <riders> --to <successor>` moves the parked side, and
// a handoff refused for as long as any overlap stands would end D-063's mid-run
// handoff in this feature's normal state. The claim door never sees that form:
// a request is never parked, because a re-claim clears the mark.
//
// KEYED ON THE RUN CLAIM'S ID, never its slug. A run re-formed under the same
// slug is a new claim, and riders of the old one do not yield to it; a run handed
// to a successor keeps its id, and they still do.
//
// OPEN MEANS THE ROW IS OPEN AND ITS HOLDER HAS NOT SAID BYE. The second term is
// what keeps a dry run the same computation as its claim: a real claim orphans an
// ended holder's run in its own transaction before it scans, and a dry run cannot
// write (D-026's reasoning, same shape). An inner join, so a run whose session
// row is missing reads as closed: this exemption fails toward refusing.
//
// THE DECLARATION IS NOT CHECKED, and the residual is recorded in D-133 and the
// charter's GIVEN rather than here as a promise: a lane at an older HEAD that
// copies the flag is admitted, and a disjoint patch integrates without complaint.
// Squashing makes a legitimate READY-tip base fail an ancestry test, and ancestry
// cannot establish preserved code anyway. This is D-005's cooperative case.
//
// WHAT THE LEDGER DOES NOT DO. It revokes nothing. When the run closes, on any
// outcome, or a rider is unparked, overlaps already granted stand, and each side
// then refuses the other at the gate: the admitted lane's edit on that path is
// DENIED until someone narrows or moves one of them. Who does that is skill text.
// A stale parked claim is still not reaped (invariant 11).

// runOpenSQL is the one spelling of "the run this claim is parked in is open",
// over a claims row aliased c. Every door's query embeds it, in the same
// statement as the rows it judges, so a door cannot read a mark from one ledger
// state and the run's openness from another.
const runOpenSQL = `EXISTS (SELECT 1 FROM claims r JOIN sessions rs ON rs.session_id=r.session_id
	WHERE r.claim_id=c.in_run AND r.state='open' AND rs.ended IS NULL)`

// runSlugSQL names the claim id in a column of c, closed or not, for a reader.
func runSlugSQL(column string) string {
	return `COALESCE((SELECT r.slug FROM claims r WHERE r.claim_id=c.` + column + `),'')`
}

// parkedYields is THE RULE: the claim parked in parkedRun, on the folded scope
// parkedScope, yields to a claim declaring declaredRun. A slot never yields,
// whatever a row says: capacity 1 is what a slot is (D-035). `--in-run` already
// refuses a claim carrying a .buddy scope, and a parked claim gains no scope
// without its mark clearing, so that term has no reachable row today; it is here
// so that a slot's capacity does not rest on those two facts staying true.
func parkedYields(parkedRun string, runOpen bool, parkedScope, declaredRun string) bool {
	return parkedRun != "" && runOpen && parkedRun == declaredRun && !scopesOverlap(parkedScope, SlotPrefix)
}

// claimMarks is what the rule reads about one side of an overlap.
type claimMarks struct {
	inRun   string // the run claim's id it is parked in, or ""
	runOpen bool   // that run is open
	basedOn string // the run claim's id it declares, or ""
}

// exempts reports whether the pair is the rule's, whichever side is parked. A
// `claim` request has no inRun, so at that door only the held side can yield.
func (req claimMarks) exempts(reqScope, heldScope string, held claimMarks) bool {
	return parkedYields(held.inRun, held.runOpen, heldScope, req.basedOn) ||
		parkedYields(req.inRun, req.runOpen, reqScope, held.basedOn)
}

// ErrNotARun refuses a declaration naming something no run could be. A closed
// run is not this: see declaredRun.
type ErrNotARun struct {
	Slug string
	Why  string
}

func (e ErrNotARun) Error() string {
	return fmt.Sprintf("--based-on %q: %s; a run is an open claim holding %s/<its slug>; nothing was taken", e.Slug, e.Why, SlotPrefix)
}

// holdsRunSlot reports whether the claim holds .buddy/slot/<slug>: what makes a
// claim a run. Without it `--in-run <any claim>` would park on anything, and the
// rider's yield would end whenever a stranger's unrelated claim did.
func holdsRunSlot(q rowQuerier, claimID, slug string) (bool, error) {
	var n int
	err := q.QueryRow(`SELECT COUNT(*) FROM claim_scopes WHERE claim_id=? AND folded=?`,
		claimID, fold(SlotPrefix+"/"+slug)).Scan(&n)
	return n > 0, err
}

// declaredRun resolves a `claim --based-on` slug to the run claim's id.
//
// A CLOSED run is not refused: it comes back as no declaration, closed=true.
// The claim then reports every blocker in the ordinary way, or is granted with a
// note, because a lane's brief can outlive the run it names, and a run that
// landed green leaves that lane nothing to be refused about. A slug that never
// existed, or an open claim that is no run, is a mistake and refuses. An open
// claim whose holder said bye reads as closed: the real claim has orphaned it by
// now, and the dry run has to say the same.
func declaredRun(q rowQuerier, slug string) (id string, closed bool, err error) {
	if slug == "" {
		return "", false, nil
	}
	claimID, owner, taken, err := openSlugOwner(q, slug)
	if err != nil {
		return "", false, err
	}
	if !taken {
		var n int
		if err := q.QueryRow(`SELECT COUNT(*) FROM claims WHERE slug=?`, slug).Scan(&n); err != nil {
			return "", false, err
		}
		if n == 0 {
			return "", false, ErrNotARun{Slug: slug, Why: "no claim of that name, open or closed"}
		}
		return "", true, nil
	}
	// Ended before the slot: a real claim orphans such a row, run or not, before it
	// gets here, and reads it closed, so a dry run must not call it "not a run".
	if sessionEnded(q, owner) {
		return "", true, nil
	}
	ok, err := holdsRunSlot(q, claimID, slug)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, ErrNotARun{Slug: slug, Why: "that open claim is not a run"}
	}
	return claimID, false, nil
}

// parkTarget resolves `--in-run` inside the transfer's transaction, before any
// row moves, and refuses whatever could not be parked. The recipient must hold
// the open run already. That also refuses a run parked in itself, with no check
// of its own: a claim in the moved set is the sender's until it moves, and the
// sender is never the recipient (`--to` yourself refused before this runs).
func (s *Store) parkTarget(tx *sql.Tx, run, toID, toLabel string, moved []Moved) (string, error) {
	id, owner, taken, err := openSlugOwner(tx, run)
	if err != nil {
		return "", err
	}
	if !taken {
		return "", ErrTransferRefused{Why: fmt.Sprintf("--in-run %q: no open claim of that name; a claim parks only in an open run", run)}
	}
	ok, err := holdsRunSlot(tx, id, run)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrTransferRefused{Why: fmt.Sprintf("--in-run %q: that open claim is not a run; a run holds %s/<its slug>", run, SlotPrefix)}
	}
	if owner != toID {
		holder, _ := s.labelOf(tx, owner)
		return "", ErrTransferRefused{Why: fmt.Sprintf("--in-run %q: %s does not hold that run, %s does; the recipient must hold the run its rider parks in", run, toLabel, holder)}
	}
	for _, m := range moved {
		scopes, err := scopesOf(tx, m.ClaimID)
		if err != nil {
			return "", err
		}
		for _, sc := range scopes {
			if f := fold(sc); f == buddyDir || strings.HasPrefix(f, buddyDir+"/") {
				return "", ErrTransferRefused{Why: fmt.Sprintf("claim %q holds %q under %s, and coordination is never parked: a slot does not yield", m.Slug, sc, buddyDir)}
			}
		}
	}
	return id, nil
}

// buddyDir is where coordination claims live; the model report's test is the same.
const buddyDir = ".buddy"

// ErrNotParked refuses an --unpark of a claim with no mark to clear.
type ErrNotParked struct{ Slug string }

func (e ErrNotParked) Error() string {
	return fmt.Sprintf("claim %q is not parked in any run; nothing changed", e.Slug)
}

// UnparkClaim withdraws one rider from its run in place: the claim stays open,
// the mark clears, and from now on it refuses like any claim (the ejection the
// design names). Nothing else could do this. A re-claim is refused once a lane
// has been admitted over the path, which is exactly when it matters, and `--to`
// yourself is refused. It RESERVES nothing new, so it needs no conflict scan to
// be allowed; the scan runs only to say which overlaps stand, since the ledger
// revokes none of them. The producer stays: the work is still the lane's.
func (s *Store) UnparkClaim(sessionID, incarnation, slug string) (runSlug string, standing []Conflict, err error) {
	err = s.tx(func(tx *sql.Tx) error {
		standing = nil
		var id, inRun, basedOn string
		var shared bool
		err := tx.QueryRow(`SELECT c.claim_id, c.in_run, c.based_on, c.shared, `+runSlugSQL("in_run")+` FROM claims c
			WHERE c.slug=? AND c.session_id=? AND c.incarnation=? AND c.state='open'`,
			slug, sessionID, incarnation).Scan(&id, &inRun, &basedOn, &shared, &runSlug)
		if errors.Is(err, sql.ErrNoRows) {
			return s.whyNotReleased(tx, sessionID, slug)
		}
		if err != nil {
			return err
		}
		if inRun == "" {
			return ErrNotParked{Slug: slug}
		}
		if _, err := tx.Exec(`UPDATE claims SET in_run='', renewed=? WHERE claim_id=?`, s.now().Unix(), id); err != nil {
			return err
		}
		scopes, err := scopesOf(tx, id)
		if err != nil {
			return err
		}
		// The declaration survives the unpark, so what stands is read with it: the
		// CLI tells the coordinator each side now refuses the other, and a rider of
		// the declared run does not (Codex code pass).
		standing, err = s.scopeConflicts(tx, sessionID, scopes, shared, claimMarks{basedOn: basedOn})
		return err
	})
	if err != nil {
		return "", nil, err
	}
	return runSlug, standing, nil
}
