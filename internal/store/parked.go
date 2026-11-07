package store

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"slices"
	"strings"

	"modernc.org/sqlite"
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
// ONE ROLE AT A TIME (issue #65). A claim parked in an open run declares nothing,
// and its based_on sleeps: unparked, or once that run closes, it counts again. The
// form this closes: x parked in R1 declaring R2 beside y parked in R2 declaring R1,
// exclusive, one path. Each yielded to the other, so both sessions passed the gate.
// With one role per claim, at most one session passes a path two exclusive claims
// hold, at any one ledger state, for any number of claims and runs: X passing needs an X claim that is not
// parked and declares, and that claim never yields, so it refuses Y.
//
// WHY NOT THE TWO NARROWER SHAPES. "A mutual yield is no yield" leaves x(in R1, based
// R2) beside y(in R2) admitted, the narrower change, but the gate ORs over a caller's
// claims, so two sessions with two claims each pass through four runs, with every pair
// one-way and nothing reported: x1(in a, based b), x2(in c, based d), y1(in b, based
// c), y2(in d, based a). A refusal at `claim --based-on` is one door diverging: it
// misses declare-then-park and leaves a standing pair passing. This is on the pair, so
// a pair formed before it needs no migration: each side now refuses the other.
//
// WHAT IT COSTS. It is wider than the mutual pair: x(in R1, based R2) beside y(in R2)
// under another holder, x passed and now does not. No recipe holds one: a lane's
// claim and the rider it was admitted over park under one holder, and a session's own
// claims are not scanned. A run handed on alone, its rider left behind, is the one
// reach: the admitted lane's park is refused until the rider follows the run, and the
// refusal says a claim parked in an open run is handed on beside a rider only to that
// rider's holder. Parking in place is not so refused: it reports the overlap (#63).
//
// A SHARED CLAIM DECLARES NOTHING (issue #66), the same shape as #65 and the other half
// of its sentence. The forms this closes: an exclusive claim parked in R, its holder's
// shared claim, a peer's shared claim declaring R; and the crossed form, each session with
// an exclusive rider and a shared declarer of the other's run. The rider yielded, the two
// shared claims overlap by D-042, and both sessions passed a path an exclusive claim held.
// Now, on a path ANY exclusive claim covers, at most one session passes, for any number
// of sessions, claims and runs, at any ledger state: passing a foreign exclusive claim
// takes a declaration, so an exclusive claim not parked in an open run, and that claim
// never yields, so it refuses everyone else; and passing with no foreign exclusive claim
// means the exclusive claim is yours, which every other session has to pass. A path only
// shared claims cover is D-042's invitation, untouched (TestAtMostOneSessionPasses).
//
// WHAT IT COSTS. Two lanes on a run's tree, both --shared --based-on, over one exclusive
// rider: the first now claims exclusive and the second waits. No skill taught that form.
// A lane appending to a playbook claims --shared from the start, so the rider on that file
// is shared too, and D-042 admits the next lane with no declaration. An exclusive rider
// never admitted a shared claim with no declaration, and that is unchanged.
//
// WHY NOT THE TWO OTHER SHAPES. A parked shared claim yielding to nobody touches neither
// form: the claim yielding in both is exclusive. Keeping the yield unless the rider's holder
// also holds a shared claim reads three claims: `claim` does not scan the caller's own, the
// holder's shared claim can come after the declarer, and the crossed form takes a second clause.
//
// `--shared --based-on` IS GRANTED WITH A NOTE, not refused. A refusal is one door diverging,
// reaches no row already standing, and would refuse a harmless pair: a shared claim beside a
// shared rider. The row keeps based_on, so `ls` still finds that lane for a coordinator, and
// a claim's mode changes only by a re-claim, which rewrites based_on too, so it never wakes.
//
// TWO REMEDIES, EACH THE OTHER'S REFUSAL (issue #67): an exclusive rider of an open run and
// an active shared claim on what a peer asks for. --shared meets the rider, the rider's
// declarer meets the shared claim, and each door's old advice sent the peer to the other. It
// fails safe, so the fix is words at the two doors that gave that advice, and no verdict
// moved (TestHolderSharedVerdicts). twoRemedies asks the two questions by the scan that
// would answer them, so the words cannot disagree with the retry they describe. It is
// asked over the whole request, as a claim is refused whole: a rider on one scope and a
// shared claim on another are the same trap.
//
// WHY NOT A RULE. Letting the declarer through has the active shared claim yield because of
// a THIRD claim its holder holds, the shape #66 rejected, and takes a path from a holder
// that never parked it: D-042's mode term, at every door, for a pair no recipe holds. Letting
// a shared peer through is #66's own form. Refusing the state at a door misses a row already
// standing, and it forms at `claim`, at a transfer and at an unpark. A standing row reads the
// same, since the words are computed at read time.
//
// KEYED ON THE RUN CLAIM'S ID, never its slug. A run re-formed under the same
// slug is a new claim, and riders of the old one do not yield to it; a run handed
// to a successor keeps its id, and they still do.
//
// OPEN MEANS THE ROW IS OPEN, ITS HOLDER HAS NOT SAID BYE, AND IT HOLDS ITS OWN SLOT
// (the third term is issue #70's, at runOpenSQL). The second term is
// what keeps a dry run the same computation as its claim: a real claim orphans an
// ended holder's run in its own transaction before it scans, and a dry run cannot
// write (D-026's reasoning, same shape). An inner join, so a run whose session
// row is missing reads as closed: this exemption fails toward refusing.
//
// THE DECLARATION IS NOT CHECKED against any tree, by any door: #65 changes what a
// parked claim's declaration MEANS and checks nothing.
// The residual is recorded in D-133 and the charter's GIVEN rather than here as a
// promise: a lane at an older HEAD that
// copies the flag is admitted, and a disjoint patch integrates without complaint.
// Squashing makes a legitimate READY-tip base fail an ancestry test, and ancestry
// cannot establish preserved code anyway. This is D-005's cooperative case.
//
// WHAT THE LEDGER DOES NOT DO. It revokes nothing. When the run closes, on any
// outcome, or a rider is unparked, overlaps already granted stand, and each side
// then refuses the other at the gate: the admitted lane's edit on that path is
// DENIED until someone narrows or moves one of them. Who does that is skill text.
// The exception is a pair, never a session's verdict: a rider whose run closed declares
// again (#65), and may now win over a rider of the run it names.
// A stale parked claim is still not reaped (invariant 11).
//
// PARKING A CLAIM YOU KEEP (issue #63): `release <item> --in-run <run>`, no --to.
// Without it a rider whose run closed, or one its coordinator unparked, could not
// go into the next run: `--to` yourself is refused and a re-claim clears the mark,
// so the coordinator had to hold a run's claim open through a red to keep its
// riders parked. Same parkTarget, same mark, same scan; only the verdict on the
// scan differs (ParkClaims says why). It also lets a coordinator park its own work,
// because keeping that out would have taken a predicate of its own, and nothing
// separates the two: a parked claim yields only to a declarer of that run.

// runOpenSQL is the one spelling of "the run this claim is parked in is open",
// over a claims row aliased c. Every door's query embeds it, in the same
// statement as the rows it judges, so a door cannot read a mark from one ledger
// state and the run's openness from another.
//
// ITS THIRD TERM IS WHAT A RUN IS (issue #70): the run claim still holds
// .buddy/slot/<its slug>. Without it a run narrowed off its slot (`release R --scope
// .buddy/slot/R`, or a re-claim without it) read OPEN here, so its riders kept
// yielding, while declaredRun and parkTarget, which test the slot, called it no run:
// `--based-on R` exited 1 and the words advised it. Its riders read CLOSED, at every
// door, as D-133 defined a run: they yield to nobody and their declarations wake (#65),
// so verdicts move as when a run closes, both ways (a woken declaration can win over
// another run's rider; the panel). `--based-on R` still exits 1. A re-claim restoring the slot keeps the claim id, so it reads open
// again, as a re-park into the same open run does (#63). Refusing the narrowing was
// rejected: it reaches no row already standing. buddy_fold is fold, registered below,
// because SQLite's lower() folds ASCII only and the test must be holdsRunSlot's.
const runOpenSQL = `EXISTS (SELECT 1 FROM claims r JOIN sessions rs ON rs.session_id=r.session_id
	WHERE r.claim_id=c.in_run AND r.state='open' AND rs.ended IS NULL
	AND EXISTS (SELECT 1 FROM claim_scopes rslot WHERE rslot.claim_id=r.claim_id
		AND rslot.folded=buddy_fold('` + SlotPrefix + `/' || r.slug)))`

// The driver installs a registered function on every connection opened after it,
// so this runs at package init, before any Open. Pure and deterministic; nothing
// persisted (no schema, index or trigger) names it, so an older binary reading the
// same ledger never needs it.
func init() {
	if err := sqlite.RegisterDeterministicScalarFunction("buddy_fold", 1,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			switch v := args[0].(type) {
			case string:
				return fold(v), nil
			case []byte:
				return fold(string(v)), nil
			}
			return nil, nil
		}); err != nil {
		panic(err)
	}
}

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
	shared  bool   // the claim's mode (D-042); the mode term and declaration() both read it here
}

// declaration is the run this claim declares as the rule reads it: none while it
// is parked in an open run (issue #65, the header's ONE ROLE AT A TIME), and none
// from a shared claim (issue #66, the header's A SHARED CLAIM DECLARES NOTHING). Every
// door reads a declaration through here. A closed run's rider declares again, and that
// same runOpen is what stops it yielding, so a claim never has both roles.
func (m claimMarks) declaration() string {
	if m.shared || (m.inRun != "" && m.runOpen) {
		return ""
	}
	return m.basedOn
}

// exempts reports whether the pair is the rule's, whichever side is parked. A
// `claim` request has no inRun, so at that door only the held side can yield.
func (req claimMarks) exempts(reqScope, heldScope string, held claimMarks) bool {
	return parkedYields(held.inRun, held.runOpen, heldScope, req.declaration()) ||
		parkedYields(req.inRun, req.runOpen, reqScope, held.declaration())
}

// TwoRemedies is the state issue #67 names, for one session's request: Rider, an exclusive
// claim parked in an open run, refuses a shared claim, and Shared, a claim held shared,
// refuses the exclusive claim declaring that run. Either may have other company: this is
// what each retry meets, and never a promise that clearing one is enough.
type TwoRemedies struct {
	Rider, Shared Conflict
}

// stuckPairs is the (rider, shared claim) pairs of that state over the request's scopes
// that keep admits, the first of each key, in order. Filtered before deduplicated, since a
// key that drops the rider can drop the very pair a filter wants (Codex design pass, #68),
// and both done as the pairs are made, over the scopes once each: kept whole, a hundred
// riders beside a hundred shared claims on one path, named a hundred times, was a hundred
// million entries (Codex code pass). It asks what `--shared` would meet and, for each rider of an open run among
// that, what declaring its run would meet. A run narrowed off its own slot reads closed
// (#70), so its riders are not riders here, and declaredRun calls it no run anyway.
func (s *Store) stuckPairs(q querier, sessionID string, norm []string, keep func(TwoRemedies) bool, key func(TwoRemedies) string) ([]TwoRemedies, error) {
	once := map[string]bool{}
	norm = slices.DeleteFunc(slices.Clone(norm), func(n string) bool {
		f := fold(n)
		dup := once[f]
		once[f] = true
		return dup
	})
	seen := map[string]bool{}
	riders, err := s.scopeConflicts(q, sessionID, norm, claimMarks{shared: true})
	if err != nil {
		return nil, err
	}
	var out []TwoRemedies
	blockersOf := map[string][]Conflict{}
	for _, r := range riders {
		if r.Shared || r.ParkedIn == "" || !r.ParkedOpen {
			continue
		}
		blockers, asked := blockersOf[r.ParkedIn]
		if !asked {
			run, closed, err := declaredRun(q, r.ParkedIn)
			if err == nil && !closed {
				if blockers, err = s.scopeConflicts(q, sessionID, norm, claimMarks{basedOn: run}); err != nil {
					return out, err
				}
			}
			blockersOf[r.ParkedIn] = blockers
		}
		for _, b := range blockers {
			t := TwoRemedies{Rider: r, Shared: b}
			if b.Shared && (keep == nil || keep(t)) && !seen[key(t)] {
				seen[key(t)] = true
				out = append(out, t)
			}
		}
	}
	return out, nil
}

// twoRemedies is the state for one session's request, one entry per remedy a peer can
// ask for: a run to declare and a shared claim whose holder to ask. Words only: an error
// here is no state to report, and costs no caller its verdict.
func (s *Store) twoRemedies(q querier, sessionID string, norm []string) []TwoRemedies {
	pairs, _ := s.stuckPairs(q, sessionID, norm, nil, func(t TwoRemedies) string { return t.Rider.ParkedIn + "\x00" + t.Shared.Slug })
	return pairs
}

// StuckBeside is issue #68: when one of the named open claims of sessionID stands in a pair
// of that state, every pair standing on their scopes, for the door that just wrote them (a
// claim, a park, a hand-on, an unpark). A session's own claims are never conflicts, so the
// holder forming the pair was told nothing. It reads as a peer holding nothing would
// (sessionID ""), over the named claims and every open claim parked IN one of them, under
// any holder (a run regaining its slot, or handed on). A pair counts only if its two scopes
// overlap: a rider on one path and a shared claim on another trap no single path. ALL the
// pairs on those scopes, not only the named claims' (the panel): with s1 and s2 both shared
// beside one rider, a door naming only s2 had "release s2" read as the way out while s1 kept
// the trap. A fresh snapshot after the commit: the words say what stands then, which may
// include a pair this write did not form, and decide nothing.
func (s *Store) StuckBeside(sessionID string, slugs []string) []TwoRemedies {
	var out []TwoRemedies
	_ = s.readSnapshot(func(q querier) error {
		in := map[string]bool{}
		var ids []string
		for _, slug := range slugs {
			var id string
			if q.QueryRow(`SELECT claim_id FROM claims WHERE slug=? AND session_id=? AND state='open'`,
				slug, sessionID).Scan(&id) == nil {
				ids = append(ids, id)
				in[sessionID+"\x00"+slug] = true
			}
		}
		named := len(ids)
		for i := 0; i < named; i++ {
			rows, err := q.Query(`SELECT claim_id, session_id, slug FROM claims WHERE in_run=? AND state='open'`, ids[i])
			if err != nil {
				return err
			}
			for rows.Next() {
				var id, sess, slug string
				if rows.Scan(&id, &sess, &slug) == nil {
					ids = append(ids, id)
					in[sess+"\x00"+slug] = true
				}
			}
			rows.Close()
		}
		var scopes []string
		for _, id := range ids {
			rows, err := q.Query(`SELECT scope FROM claim_scopes WHERE claim_id=?`, id)
			if err != nil {
				return err
			}
			for rows.Next() {
				var sc string
				if rows.Scan(&sc) == nil {
					scopes = append(scopes, sc)
				}
			}
			rows.Close()
		}
		pairs, _ := s.stuckPairs(q, "", scopes, func(t TwoRemedies) bool {
			return scopesOverlap(fold(t.Rider.Their), fold(t.Shared.Their))
		}, func(t TwoRemedies) string { return t.Rider.Slug + "\x00" + t.Shared.Slug })
		if !slices.ContainsFunc(pairs, func(t TwoRemedies) bool {
			return in[t.Rider.Session+"\x00"+t.Rider.Slug] || in[t.Shared.Session+"\x00"+t.Shared.Slug]
		}) {
			return nil
		}
		out = pairs
		return nil
	})
	return out
}

// TwoRemediesAt is twoRemedies for the gate's deny, which has a path and no request. It
// runs only on a deny, so the hot path pays nothing, and that deny stands whatever it says.
func (s *Store) TwoRemediesAt(sessionID, relPath string) []TwoRemedies {
	var out []TwoRemedies
	_ = s.readSnapshot(func(q querier) error {
		out = s.twoRemedies(q, sessionID, []string{relPath})
		return nil
	})
	return out
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

// parkTarget resolves `--in-run` inside the caller's transaction, before any
// row moves, and refuses whatever could not be parked. The recipient must hold
// the open run already. That also refuses a run parked in itself, with no check
// of its own at the transfer door: a claim in the moved set is the sender's until
// it moves, and the sender is never the recipient (`--to` yourself refused before
// this runs). Parking in place, the caller IS the recipient, and what refuses a run
// parked in itself is the .buddy test below: a run holds .buddy/slot/<its slug>,
// which holdsRunSlot has just established.
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
		return "", ErrTransferRefused{Why: fmt.Sprintf("--in-run %q: %s does not hold that run, %s does; a rider parks only with its run's holder", run, toLabel, holder)}
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

// markParked is the one write of a park: the mark, and the producer. The first
// park records the producer; a re-park keeps it, so a rider re-parked after a red
// is still its lane's work. Both CASEs read the row as it was: SQLite evaluates an
// UPDATE's right-hand sides before it assigns any of them.
func markParked(tx *sql.Tx, claimID, runID, producerSession, producerIncarnation string) error {
	_, err := tx.Exec(`UPDATE claims SET in_run=?,
		producer_session=CASE WHEN producer_session='' THEN ? ELSE producer_session END,
		producer_incarnation=CASE WHEN producer_session='' THEN ? ELSE producer_incarnation END
		WHERE claim_id=?`, runID, producerSession, producerIncarnation, claimID)
	return err
}

// ParkClaims parks the caller's own open claims in an open run it holds, all or
// nothing, with no transfer (issue #63). The owner does not change, so every wait
// on them stands; a wait or READY on the OLD run does not follow.
//
// A STANDING OVERLAP IS REPORTED, NEVER REFUSED, where the transfer door rolls
// back. That door's scan stops a NEW owner taking a path; here nobody gets a path
// they did not hold, and the ledger revokes nothing either way. As UnparkClaim's
// does, the scan says what stands. A refusal would hold a coordinator on a red
// until every admitted lane had narrowed, which is the cost this door exists to end.
//
// IT MAY TIGHTEN AS WELL AS LOOSEN. A claim moved from open run R1 to R2 stops
// yielding to a declarer of R1, and parkedYields keys on the run claim's id, so no
// declaration of R1 (or of a run re-formed under R1's slug) is ever admitted by R2.
// A claim parked with a declaration of its own stops being declared (issue #65), so
// a rider that yielded to it no longer does. Each such claim comes back as standing.
// The verdict on a session can still differ
// from the pair's: another covering claim of that session declaring R2 clears the
// gate for it, so the caller reports pairs and never a session's verdict (Codex
// design pass: the sequence is TestParkInPlaceReportsPairsNotVerdicts).
func (s *Store) ParkClaims(sessionID, incarnation string, slugs []string, run string) (standing []Conflict, err error) {
	if len(slugs) == 0 {
		return nil, errors.New("a park names at least one claim")
	}
	err = s.tx(func(tx *sql.Tx) error {
		claims, err := s.callersOpenClaims(tx, sessionID, incarnation, slugs)
		if err != nil {
			return err
		}
		label, err := s.labelOf(tx, sessionID)
		if err != nil {
			return err
		}
		runID, err := s.parkTarget(tx, run, sessionID, label, claims)
		if err != nil {
			return err
		}
		for _, m := range claims {
			if err := markParked(tx, m.ClaimID, runID, sessionID, incarnation); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE claims SET renewed=? WHERE claim_id=?`, s.now().Unix(), m.ClaimID); err != nil {
				return err
			}
		}
		standing, err = s.conflictsOfClaims(tx, sessionID, claims)
		return err
	})
	if err != nil {
		return nil, err
	}
	return standing, nil
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
		// The declaration survives the unpark, so what stands is read with it: a rider
		// of the declared run is not named (Codex code pass). The CLI reports a pair and
		// no session's verdict, for the reason releasePark gives (Codex design pass, #65).
		standing, err = s.scopeConflicts(tx, sessionID, scopes, claimMarks{basedOn: basedOn, shared: shared})
		return err
	})
	if err != nil {
		return "", nil, err
	}
	return runSlug, standing, nil
}
