package store

import (
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
func (s *Store) scopeConflicts(q querier, sessionID string, norm []string) ([]Conflict, error) {
	// NO STALENESS TERM, and that is the rule (issue #18): state='open' is the
	// whole test. A stale claim REFUSES exactly like a fresh one — staleness
	// marks, it never reaps (invariant 11), and acquisition never silently
	// takes over a scope another session believes it holds. Two sessions
	// measured opposite answers on one afternoon because a roster row for a
	// just-released claim looks the same as a takeover; the rule was never
	// ambiguous, only unwritten. The holder's renewed time comes back so the
	// refusal can SAY the holder is quiet, which is the actionable half.
	rows, err := q.Query(`SELECT cs.folded, cs.scope, c.slug, c.session_id, c.renewed FROM claim_scopes cs
		JOIN claims c ON c.claim_id=cs.claim_id WHERE c.state='open' AND c.session_id<>?`, sessionID)
	if err != nil {
		return nil, err
	}
	type held struct {
		folded, scope, slug, session string
		renewed                      time.Time
	}
	var theirs []held
	for rows.Next() {
		var h held
		if err := rows.Scan(&h.folded, &h.scope, &h.slug, &h.session, unixScan{&h.renewed}); err != nil {
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
			if scopesOverlap(nf, h.folded) {
				label, _ := s.labelOf(q, h.session)
				out = append(out, Conflict{Scope: n, Their: h.scope, Slug: h.slug, Claimant: label, Session: h.session, Renewed: h.renewed})
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
func (s *Store) allConflicts(q querier, sessionID, slug string, norm []string) ([]Conflict, error) {
	var out []Conflict
	if claimID, owner, taken, err := openSlugOwner(q, slug); err != nil {
		return nil, err
	} else if taken && owner != sessionID {
		label, _ := s.labelOf(q, owner)
		// The slug conflict carries the holder's clock too. Without it the
		// REFUSAL path and the DRY RUN would annotate different subsets of the
		// same set, which is the disagreement allConflicts exists to prevent.
		var renewed time.Time
		_ = q.QueryRow(`SELECT renewed FROM claims WHERE claim_id=?`, claimID).Scan(unixScan{&renewed})
		out = append(out, Conflict{Slug: slug, Claimant: label, Session: owner, Renewed: renewed})
	}
	sc, err := s.scopeConflicts(q, sessionID, norm)
	if err != nil {
		return nil, err
	}
	return append(out, sc...), nil
}

// refusedFrom turns a non-empty conflict set into the error Claim has always
// returned: the first conflict in the named fields, the rest in More, so
// existing callers see what they saw and a caller that prints the set can.
func refusedFrom(conflicts []Conflict) ErrRefused {
	first := ErrRefused{Slug: conflicts[0].Slug, Scope: conflicts[0].Scope, Their: conflicts[0].Their,
		Claimant: conflicts[0].Claimant, Renewed: conflicts[0].Renewed}
	for _, c := range conflicts[1:] {
		first.More = append(first.More, ErrRefused{Slug: c.Slug, Scope: c.Scope, Their: c.Their,
			Claimant: c.Claimant, Renewed: c.Renewed})
	}
	return first
}

// ClaimConflicts answers "what would `claim` refuse, and what would it take"
// WITHOUT taking anything: the requested scopes that overlap nothing, and
// every conflict, computed by the same code path as the refusal.
//
// It runs outside a transaction, on autocommit reads, deliberately: the
// ledger opens with _txlock=immediate, so a transaction here would take the
// WRITE lock for a read-only answer (the same cost Open once paid on every
// gate, see Open). The price is that the slug check and the scope scan are two
// reads that a peer's claim can land between — a dry run is a forecast, and
// the real Claim re-checks everything under its own lock.
//
// An unknown, ended or superseded session is refused, not told its scopes are
// free: the forecast is for a claim that session could never take. The
// INCARNATION is checked for the same reason Claim checks it — a caller that
// resolved itself before a bye and a hello would otherwise be forecast "free"
// and then refused at the write (Codex code pass, 2026-09-20).
func (s *Store) ClaimConflicts(sessionID, incarnation, slug string, scopes []string) (free []string, conflicts []Conflict, err error) {
	if len(scopes) == 0 {
		return nil, nil, errors.New("a claim needs at least one --scope")
	}
	norm := make([]string, 0, len(scopes))
	for _, sc := range scopes {
		n, err := NormalizeScope(sc)
		if err != nil {
			return nil, nil, err
		}
		norm = append(norm, n)
	}
	var live int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE session_id=? AND incarnation=? AND ended IS NULL`,
		sessionID, incarnation).Scan(&live); err != nil {
		return nil, nil, err
	}
	if live == 0 {
		return nil, nil, fmt.Errorf("session %s (incarnation %s) is not live; run buddy hello first", sessionID, incarnation)
	}
	conflicts, err = s.allConflicts(s.db, sessionID, slug, norm)
	if err != nil {
		return nil, nil, err
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
	return free, conflicts, nil
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
	if len(scopes) == 0 {
		return nil, errors.New("release --scope needs at least one scope; `release <slug>` alone releases the whole claim")
	}
	norm := make([]string, 0, len(scopes))
	for _, sc := range scopes {
		n, err := NormalizeScope(sc)
		if err != nil {
			return nil, err
		}
		norm = append(norm, n)
	}
	err = s.tx(func(tx *sql.Tx) error {
		var claimID string
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
		return nil, err
	}
	return remaining, nil
}
