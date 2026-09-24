package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The conflict SET, and the partial release. Both come from one field report
// (wishlist §5b, a 14-session run, 2026-09-20), and they are the two faces of
// the same all-or-nothing shape:
//
//   - A claim naming four paths was refused WHOLE because one of them was
//     held, and the refusal named that one conflict. The three free paths
//     were not taken, and finding the next collision cost another round trip.
//     Correct, and expensive: the coordinator had issued an assignment whose
//     scopes could not all be satisfied, and neither party could see that
//     until the claim was tried.
//   - A holder that had finished with two of its four scopes narrowed them by
//     SAYING SO — its description ends "Docs scopes RELEASED." — and the
//     recorded scope was unchanged, because release was whole too. The gate
//     kept enforcing four paths the holder believed it had handed back, a
//     waiter correctly stayed off two of them, and nothing contradicted either
//     side. Acquisition fails loudly; a prose release fails silently in both
//     directions.
//
// WHAT THIS DOES NOT CHANGE. D-001's "granted whole or refused whole" stands:
// there is no partial ACQUISITION here. Partial acquisition was the first
// thing asked for and it is the wrong fix — a claim that comes back holding
// three of four paths has changed shape under its caller, and the caller's
// next edit lands on the fourth as if it were held. What ships instead is
// (1) the whole conflict set, computed the same way the refusal is and
// available without writing (`claim --dry-run`), so a request can be narrowed
// in one round trip; and (2) release of NAMED scopes by the holder, which is
// the holder shrinking its own reservation and reserves nothing for anyone.
//
// EXACT SCOPES ONLY, ON RELEASE. Releasing "pkg/sub" from a claim holding
// "pkg" is refused rather than treated as "release the part of pkg under sub":
// prefix scopes have no subtraction (D-002 cut glob math for the same reason),
// so the only honest result would be "pkg" still held and success reported,
// which is the prose failure again with a command in front of it.

// Conflict is one requested scope against one held scope, or a slug held by
// another session (Scope == "").
type Conflict struct {
	Scope    string // the requested scope, as normalized from the caller's spelling; "" for a slug conflict
	Their    string // the held scope, as claimed
	Slug     string // the holding claim's slug
	Claimant string // the holder's label
	Session  string // the holder's session id
	// Renewed is when the holding claim was last renewed, so a refusal can say
	// the holder has gone quiet (issue #18). Zero means not recorded, which
	// must never render as STALE — "unknown" and "abandoned" are different.
	Renewed time.Time
	// Shared is the HOLDER's mode (D-042). A shared holder refuses only an
	// exclusive request, so the refusal can say that --shared would clear
	// this one scope conflict (and never that it would clear a slug one).
	Shared bool
}

// querier is what the conflict scan needs from either a *sql.Tx (inside
// Claim's transaction) or a *sql.DB (the dry run, outside one).
type querier interface {
	rowQuerier
	Query(query string, args ...any) (*sql.Rows, error)
}

// scopeConflicts is THE conflict computation: every (requested, held) pair
// that overlaps, against every OTHER session's open claims. Claim and
// ClaimConflicts both call it, so a dry run cannot disagree with the refusal
// it predicts — the one way a dry run becomes worse than no dry run.
//
// ONE MODE TERM (D-042): an overlap is a conflict unless BOTH sides are
// shared. Shared against exclusive refuses in both directions, which is what
// keeps "an exclusive claim is the only claim on its paths" true across
// sessions, and what OwnerOf's reading of a shared hold relies on.
func (s *Store) scopeConflicts(q querier, sessionID string, norm []string, shared bool) ([]Conflict, error) {
	// NO STALENESS TERM, and that is the rule (issue #18): a stale claim
	// REFUSES exactly like a fresh one — staleness marks, it never reaps
	// (invariant 11), and acquisition never silently takes over a scope
	// another session believes it holds. Two sessions measured opposite
	// answers on one afternoon because a roster row for a just-released claim
	// looks the same as a takeover; the rule was never ambiguous, only
	// unwritten. The holder's renewed time comes back so the refusal can SAY
	// the holder is quiet, which is the actionable half.
	//
	// ONE TERM BESIDES state='open': the owner has not said BYE (D-026, issue
	// #15). Claim orphans ended owners' claims in its own transaction before
	// it scans, so an ended owner's row is never a conflict there; the dry
	// run cannot write, and this is what keeps its forecast the same
	// computation as the refusal it predicts. `ended` is POSITIVE evidence —
	// the session said so — which is the one thing staleness is not, and it
	// is trustworthy only since D-025 made a bye unable to end a live
	// incarnation; that is why this shipped after it and not before.
	//
	// LEFT JOIN, so a claim whose session row is somehow missing still
	// refuses: nothing would ever orphan it, and an inner join would make it
	// vanish from the scan while the gate keeps enforcing it (Codex design
	// pass, D-026). Nothing deletes a sessions row today; the join is what
	// makes that a fact this code does not depend on.
	rows, err := q.Query(`SELECT cs.folded, cs.scope, c.slug, c.session_id, c.renewed, c.shared FROM claim_scopes cs
		JOIN claims c ON c.claim_id=cs.claim_id LEFT JOIN sessions ses ON ses.session_id=c.session_id
		WHERE c.state='open' AND c.session_id<>? AND ses.ended IS NULL`, sessionID)
	if err != nil {
		return nil, err
	}
	type held struct {
		folded, scope, slug, session string
		renewed                      time.Time
		shared                       bool
	}
	var theirs []held
	for rows.Next() {
		var h held
		if err := rows.Scan(&h.folded, &h.scope, &h.slug, &h.session, unixScan{&h.renewed}, &h.shared); err != nil {
			rows.Close()
			return nil, err
		}
		theirs = append(theirs, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []Conflict
	for _, n := range norm {
		nf := fold(n)
		for _, h := range theirs {
			if scopesOverlap(nf, h.folded) && !(shared && h.shared) {
				label, _ := s.labelOf(q, h.session)
				out = append(out, Conflict{Scope: n, Their: h.scope, Slug: h.slug, Claimant: label, Session: h.session,
					Renewed: h.renewed, Shared: h.shared})
			}
		}
	}
	return out, nil
}

// allConflicts is the WHOLE set: another session's hold on the slug first
// (Scope == ""), then every overlapping (requested, held) pair. Claim (inside
// its transaction) and ClaimConflicts (outside one) both call it, so the
// forecast and the refusal enumerate the same things in the same order. The
// first version shared only the scope scan, and Claim's slug check returned
// before it — so a request that collided on slug AND scope was forecast with
// two conflicts and refused with one (Codex code pass, 2026-09-20).
//
// Your own open slug is not a conflict: Claim treats that as a refresh.
func (s *Store) allConflicts(q querier, sessionID, slug string, norm []string, shared bool) ([]Conflict, error) {
	var out []Conflict
	if claimID, owner, taken, err := openSlugOwner(q, slug); err != nil {
		return nil, err
	} else if taken && owner != sessionID && !sessionEnded(q, owner) {
		label, _ := s.labelOf(q, owner)
		// The slug conflict carries the holder's clock too. Without it the
		// REFUSAL path and the DRY RUN would annotate different subsets of the
		// same set, which is the disagreement allConflicts exists to prevent.
		// A slug is ONE holder whatever the modes: Shared is left false on a
		// slug conflict, because --shared would not clear it.
		var renewed time.Time
		_ = q.QueryRow(`SELECT renewed FROM claims WHERE claim_id=?`, claimID).Scan(unixScan{&renewed})
		out = append(out, Conflict{Slug: slug, Claimant: label, Session: owner, Renewed: renewed})
	}
	sc, err := s.scopeConflicts(q, sessionID, norm, shared)
	if err != nil {
		return nil, err
	}
	return append(out, sc...), nil
}

// sessionEnded reports whether the session has said bye. An unknown session
// reads as NOT ended: a claim whose owner row is missing is a dangling row
// nothing will orphan, and it must keep refusing rather than vanish from the
// scan (Codex design pass, D-026).
func sessionEnded(q rowQuerier, sessionID string) bool {
	var ended sql.NullInt64
	if err := q.QueryRow(`SELECT ended FROM sessions WHERE session_id=?`, sessionID).Scan(&ended); err != nil {
		return false
	}
	return ended.Valid
}

// endedOverlaps lists the open claims of ENDED owners that the requested
// scopes or slug would displace: what Claim's orphanEnded is about to free.
// Only the dry run asks, because only the dry run has to SAY it — "acquirable
// once the ended holder is cleaned up" and "nobody holds this" are different
// facts, and a forecast that printed `would claim` for both would be read as
// the second (Codex design pass, D-026).
func (s *Store) endedOverlaps(q querier, sessionID, slug string, norm []string) ([]Conflict, error) {
	var out []Conflict
	if _, owner, taken, err := openSlugOwner(q, slug); err != nil {
		return nil, err
	} else if taken && owner != sessionID && sessionEnded(q, owner) {
		label, _ := s.labelOf(q, owner)
		out = append(out, Conflict{Slug: slug, Claimant: label, Session: owner})
	}
	rows, err := q.Query(`SELECT cs.scope, cs.folded, c.slug, c.session_id FROM claim_scopes cs
		JOIN claims c ON c.claim_id=cs.claim_id JOIN sessions ses ON ses.session_id=c.session_id
		WHERE c.state='open' AND c.session_id<>? AND ses.ended IS NOT NULL`, sessionID)
	if err != nil {
		return nil, err
	}
	type held struct{ scope, folded, slug, session string }
	var theirs []held
	for rows.Next() {
		var h held
		if err := rows.Scan(&h.scope, &h.folded, &h.slug, &h.session); err != nil {
			rows.Close()
			return nil, err
		}
		theirs = append(theirs, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, n := range norm {
		nf := fold(n)
		for _, h := range theirs {
			if scopesOverlap(nf, h.folded) {
				label, _ := s.labelOf(q, h.session)
				out = append(out, Conflict{Scope: n, Their: h.scope, Slug: h.slug, Claimant: label, Session: h.session})
			}
		}
	}
	return out, nil
}

// refusedFrom turns a non-empty conflict set into the error Claim has always
// returned: the first conflict in the named fields, the rest in More, so
// existing callers see what they saw and a caller that prints the set can.
func refusedFrom(conflicts []Conflict) ErrRefused {
	first := ErrRefused{Slug: conflicts[0].Slug, Scope: conflicts[0].Scope, Their: conflicts[0].Their,
		Claimant: conflicts[0].Claimant, Renewed: conflicts[0].Renewed, Shared: conflicts[0].Shared}
	for _, c := range conflicts[1:] {
		first.More = append(first.More, ErrRefused{Slug: c.Slug, Scope: c.Scope, Their: c.Their,
			Claimant: c.Claimant, Renewed: c.Renewed, Shared: c.Shared})
	}
	return first
}

// ClaimConflicts answers "what would `claim` refuse, and what would it take"
// WITHOUT taking anything: the requested scopes that overlap nothing, and
// every conflict, computed by the same code path as the refusal.
//
// It runs inside ONE READ SNAPSHOT — `BEGIN DEFERRED` on a dedicated
// connection, which in WAL mode reads a consistent snapshot without taking
// the write lock — and not inside s.tx, because the ledger opens with
// _txlock=immediate and a transaction there would take the WRITE lock for a
// read-only answer (the same cost Open once paid on every gate, see Open).
// The first shape ran three autocommit reads and could assemble a result no
// single ledger state ever had: a peer's bye landing between the conflict
// scan and the displacement scan reported the same claim as BOTH blocking
// and displaced (Codex code pass, D-026). A forecast may be stale by the
// time it is read; it must not contradict itself.
//
// An unknown, ended or superseded session is refused, not told its scopes are
// free: the forecast is for a claim that session could never take. The
// INCARNATION is checked for the same reason Claim checks it — a caller that
// resolved itself before a bye and a hello would otherwise be forecast "free"
// and then refused at the write (Codex code pass, 2026-09-20).
//
// displaced is what a real claim would ORPHAN on its way in: open claims of
// owners that have said bye, which Claim frees before it scans (D-026). They
// are reported apart from the free set and apart from the conflicts because
// they are neither.
func (s *Store) ClaimConflicts(sessionID, incarnation, slug string, scopes []string) (free []string, conflicts, displaced []Conflict, err error) {
	return s.ClaimConflictsMode(sessionID, incarnation, slug, scopes, false)
}

// ClaimConflictsMode is ClaimConflicts for a request of the named mode
// (D-042): the forecast of ClaimMode, by the same computation.
func (s *Store) ClaimConflictsMode(sessionID, incarnation, slug string, scopes []string, shared bool) (free []string, conflicts, displaced []Conflict, err error) {
	norm, err := claimScopes(scopes, shared)
	if err != nil {
		return nil, nil, nil, err
	}
	err = s.readSnapshot(func(q querier) error {
		var live int
		if err := q.QueryRow(`SELECT COUNT(*) FROM sessions WHERE session_id=? AND incarnation=? AND ended IS NULL`,
			sessionID, incarnation).Scan(&live); err != nil {
			return err
		}
		if live == 0 {
			return fmt.Errorf("session %s (incarnation %s) is not live; run buddy hello first", sessionID, incarnation)
		}
		conflicts, err = s.allConflicts(q, sessionID, slug, norm, shared)
		if err != nil {
			return err
		}
		if s.betweenReads != nil {
			s.betweenReads()
		}
		displaced, err = s.endedOverlaps(q, sessionID, slug, norm)
		return err
	})
	if err != nil {
		return nil, nil, nil, err
	}
	busy := map[string]bool{}
	for _, c := range conflicts {
		if c.Scope != "" {
			busy[c.Scope] = true
		}
	}
	for _, n := range norm {
		if !busy[n] {
			free = append(free, n)
		}
	}
	return free, conflicts, displaced, nil
}

// connQuerier adapts a *sql.Conn to the querier the scans take.
type connQuerier struct{ c *sql.Conn }

func (q connQuerier) QueryRow(query string, args ...any) *sql.Row {
	return q.c.QueryRowContext(context.Background(), query, args...)
}

func (q connQuerier) Query(query string, args ...any) (*sql.Rows, error) {
	return q.c.QueryContext(context.Background(), query, args...)
}

// readSnapshot runs fn against one consistent read of the ledger. BEGIN
// DEFERRED, issued by hand on a dedicated connection, is a READ transaction
// in WAL mode: it pins a snapshot, blocks no writer and is blocked by none.
// database/sql's Begin cannot do this here, because the DSN's
// _txlock=immediate makes every driver-level BEGIN take the write lock.
func (s *Store) readSnapshot(fn func(q querier) error) error {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN DEFERRED`); err != nil {
		return err
	}
	// A read transaction has nothing to roll back, but ending it releases
	// the snapshot; COMMIT and ROLLBACK are equivalent here and COMMIT is
	// what a reader expects to see.
	defer conn.ExecContext(ctx, `COMMIT`)
	return fn(connQuerier{conn})
}

// ErrScopeNotHeld says a partial release named a scope the claim does not hold
// exactly, and lists what it does hold so the caller can name one of those.
type ErrScopeNotHeld struct {
	Slug  string
	Scope string
	Held  []string
}

func (e ErrScopeNotHeld) Error() string {
	return fmt.Sprintf("claim %q does not hold scope %q exactly — it holds: %s (release names a held scope as claimed; containment is not release)",
		e.Slug, e.Scope, strings.Join(e.Held, ", "))
}

// ReleaseScopes hands back the NAMED scopes of the caller's own open claim and
// returns what it still holds. Releasing the last scope releases the claim,
// exactly as `release <slug>` would, so a claim can never sit open over
// nothing. Fenced by incarnation as Release is, and diagnosed the same way
// when it matches no claim.
//
// All-or-nothing over the named scopes, in one transaction: one scope that is
// not held refuses the whole command and narrows nothing, because "released
// two of the three you asked for" is the partial-acquisition shape from the
// other side.
func (s *Store) ReleaseScopes(sessionID, incarnation, slug string, scopes []string) (remaining []string, err error) {
	_, remaining, err = s.ReleaseScopesID(sessionID, incarnation, slug, scopes)
	return remaining, err
}

// ReleaseScopesID is ReleaseScopes that also returns the id of the claim it
// narrowed, read inside its own transaction — for the reason ReleaseID
// gives: the caller names the waiters of the claim that CLOSED (D-033).
func (s *Store) ReleaseScopesID(sessionID, incarnation, slug string, scopes []string) (releasedID string, remaining []string, err error) {
	if len(scopes) == 0 {
		return "", nil, errors.New("release --scope needs at least one scope; `release <slug>` alone releases the whole claim")
	}
	norm := make([]string, 0, len(scopes))
	for _, sc := range scopes {
		n, err := NormalizeScope(sc)
		if err != nil {
			return "", nil, err
		}
		norm = append(norm, n)
	}
	var claimID string
	err = s.tx(func(tx *sql.Tx) error {
		err := tx.QueryRow(`SELECT claim_id FROM claims WHERE slug=? AND session_id=? AND incarnation=? AND state='open'`,
			slug, sessionID, incarnation).Scan(&claimID)
		if errors.Is(err, sql.ErrNoRows) {
			return s.whyNotReleased(tx, sessionID, slug)
		}
		if err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT scope, folded FROM claim_scopes WHERE claim_id=? ORDER BY scope`, claimID)
		if err != nil {
			return err
		}
		type held struct{ scope, folded string }
		var have []held
		for rows.Next() {
			var h held
			if err := rows.Scan(&h.scope, &h.folded); err != nil {
				rows.Close()
				return err
			}
			have = append(have, h)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		drop := map[string]bool{}
		for _, n := range norm {
			nf := fold(n)
			found := false
			for _, h := range have {
				if h.folded == nf {
					found = true
					break
				}
			}
			if !found {
				e := ErrScopeNotHeld{Slug: slug, Scope: n}
				for _, h := range have {
					e.Held = append(e.Held, h.scope)
				}
				return e
			}
			drop[nf] = true
		}
		now := s.now().Unix()
		remaining = remaining[:0]
		for _, h := range have {
			if drop[h.folded] {
				if _, err := tx.Exec(`DELETE FROM claim_scopes WHERE claim_id=? AND folded=?`, claimID, h.folded); err != nil {
					return err
				}
				continue
			}
			remaining = append(remaining, h.scope)
		}
		if len(remaining) == 0 {
			_, err = tx.Exec(`UPDATE claims SET state='released', renewed=? WHERE claim_id=?`, now, claimID)
			return err
		}
		_, err = tx.Exec(`UPDATE claims SET renewed=? WHERE claim_id=?`, now, claimID)
		return err
	})
	if err != nil {
		return "", nil, err
	}
	return claimID, remaining, nil
}

// ClaimsTouching lists the OPEN claims that bear on relPath: one whose scope
// COVERS it (invariant 14's containment, the rule the gate adjudicates), and,
// for a directory argument, any held UNDER it.
//
// THE FAILURE (issue #13). `buddy whose <path>` reports who has uncommitted
// CHANGES to a path — dirty-worktree attribution — and its name reads as
// ownership, so it got used to answer "is this claimed?". It is silent in
// exactly the state that matters most: a session that has claimed a file and
// not yet started editing it is invisible to a dirty scan, and that is the
// state every session is in immediately after claiming. Measured: two sessions
// in one day read a whose result as "unheld" and were wrong; one decided it
// could write a shared file on that basis, and the file had been claimed by
// another session for hours. It caught the error only because a coordinator
// happened to hold the claim table and contradicted the answer.
//
// The two registers are reported separately and the CLAIM comes first, because
// it is the one that reserves anything. Both are advisory, and saying which is
// which is the whole point: coinciding often enough to look reliable is what
// made the trap.
// ClaimTouch is one open claim that bears on a path, and HOW it bears on it.
// Covers is invariant 14's containment — the relation the gate adjudicates.
// Anything else is a claim held UNDER the path, which reserves nothing about
// the path itself and is reported because "who has anything in src/?" is a
// real question, not because it is the same answer.
type ClaimTouch struct {
	Claim  ClaimInfo
	Covers bool
}

// ClaimsTouching lists the OPEN claims that bear on relPath.
//
// THE FAILURE (issue #13). `buddy whose <path>` reports who has uncommitted
// CHANGES to a path — dirty-worktree attribution — and its name reads as
// ownership, so it got used to answer "is this claimed?". It is silent in
// exactly the state that matters most: a session that has claimed a path and
// not yet started editing it is invisible to a dirty scan, and that is the
// state every session is in immediately after claiming. Measured: two sessions
// in one day read a whose result as "unheld" and were wrong; one decided it
// could write a shared file on that basis, and the file had been claimed by
// another session for hours.
//
// NO FILESYSTEM. The first shape took an asDir flag that `whose` computed with
// os.Stat, and applied the held-under arm only when the directory existed
// LOCALLY. A peer claiming `internal/newpkg/foo.go` — the natural state for a
// package somebody has just reserved in order to CREATE it — therefore read
// back as `CLAIMED BY (none)`, which is this command's own defect wearing the
// new feature's clothes (Fable review, issue #13). A claim is declared intent:
// it can name a path that does not exist here, or anywhere, yet. The dirty
// register still consults the filesystem, because git only knows files that
// exist; the claim register must not.
func (s *Store) ClaimsTouching(relPath string) ([]ClaimTouch, error) {
	// ONE snapshot, then filter in Go.
	//
	// Codex finding (issue #13): an earlier shape scanned claim_scopes to pick
	// claim ids, then materialized each id in a SECOND query. A claim can be
	// refreshed or released between the two — a refresh keeps the claim id and
	// REPLACES its scopes — so `whose internal/api/server.go` could print
	// `CLAIMED BY api-work` with `scopes: docs`: a claim reported as covering a
	// path whose recorded scopes do not cover it, or a released claim rendered
	// as held. Reading every open claim once and matching against the scopes
	// that came back with it cannot disagree with itself, and it is the same
	// consistency `buddy ls` already has rather than a new class of read.
	rel := fold(relPath)
	all, err := s.claimsWhere(`WHERE c.state='open'`)
	if err != nil {
		return nil, err
	}
	var out []ClaimTouch
	for _, c := range all {
		covers, under := false, false
		for _, sc := range c.Scopes {
			folded := fold(sc)
			if scopeCovers(folded, rel) {
				covers = true
			} else if scopeCovers(rel, folded) {
				under = true
			}
		}
		if covers || under {
			out = append(out, ClaimTouch{Claim: c, Covers: covers})
		}
	}
	return out, nil
}

// SlotPrefix is the reserved home of resource slots (D-035): a claim on
// `.buddy/slot/<name>` reserves a capacity-1 resource rather than a file.
const SlotPrefix = ".buddy/slot"

// claimScopes normalizes a request's scopes, and refuses a SHARED request
// that reaches a slot (D-042). A slot is capacity 1; two shared claims over
// one would each believe they held a resource that is reserved for nobody.
// OVERLAP, not containment: `--shared --scope .buddy` covers every slot
// without lying under the prefix (Codex design pass, D-042). Here and not in
// the CLI, because ClaimMode and its forecast are the one door a claim
// comes through.
func claimScopes(scopes []string, shared bool) ([]string, error) {
	if len(scopes) == 0 {
		return nil, errors.New("a claim needs at least one --scope")
	}
	norm := make([]string, 0, len(scopes))
	for _, sc := range scopes {
		n, err := NormalizeScope(sc)
		if err != nil {
			return nil, err
		}
		if shared && scopesOverlap(fold(n), SlotPrefix) {
			return nil, fmt.Errorf("scope %q reaches the resource slots under %s, and a slot is capacity 1; claim it without --shared", n, SlotPrefix)
		}
		norm = append(norm, n)
	}
	return norm, nil
}
