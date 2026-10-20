package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Handing an open claim to another live session (D-063): `release <slug> --to
// <target>`.
//
// THE FAILURE. An orchestrator handing its role to a successor held an
// in-flight run claim (.buddy/slot/<run> + .buddy/slot/main) with two riders
// parked on it. The only way to pass it was release-then-claim, which mints a
// NEW claim id, and a wait is resolved once to a claim id (D-033). So every
// rider's wait went LANDED with "no outcome was reported — ask its holder",
// the holder being the session that had just left; each rider's READY sha
// went with its wait, so `who <run>` showed the successor no riders at all
// until both re-declared by hand; and between the release and the re-claim
// `msg <run>` was refused and main's slot was anybody's. Measured once
// (2026-09-29), re-pointed by hand within a minute. One missed check later,
// the successor would have landed a run whose riders it could not see.
//
// THE RULE. The holder moves the row: one immediate transaction changes the
// claim's owner and incarnation and nothing else, so the claim id, its
// scopes, mode, slug, description and `created` stand, and every wait on it
// keeps waiting. `renewed` is set to now: the move is an explicit act, and
// inheriting the old holder's quiet would mark the new holder's claim stale
// for a silence that was not theirs.
//
// WHAT IT REFUSES, each found by a design pass (Codex) rather than a
// measurement, and each a test:
//   - a sender that is not live under the incarnation it names. Release lets a
//     dead incarnation's leftover call close a claim, which is safe; a
//     transfer would instead keep it open under someone else.
//   - a recipient that has ended, or whose incarnation changed since it was
//     resolved (it byed and re-helloed: not the session that answered).
//   - the sender itself, and the fleet word.
//   - an overlap the recipient could not have claimed itself. A session may
//     hold overlapping claims of its own, so the sender's X on `pkg` beside
//     its Y on `pkg/f` is legal, and handing X alone to B would leave two
//     sessions holding one path. The ordinary conflict scan runs against the
//     NEW owner, after the move, inside the transaction, and a conflict rolls
//     the move back.
//
// SEVERAL CLAIMS MOVE AS ONE (D-093). One fleet of 144 sessions made about 93
// `release --to` calls, 41 of them inside for-loops, one slug per call; one
// loop sent its recipient 8-10 notices and printed 8 wake lines. Worse, a
// sender whose OWN claims overlap each other could not hand them on at all:
// each one alone left its siblings behind under the sender, the overlap scan
// refused it, and the refusal said "and 7 more conflict(s), listed above"
// over a listing that was never printed. So TransferClaims takes every slug,
// moves every claim, and scans each against everyone but the new owner —
// which, after the move, is exactly "everyone except the claims moving
// together". All or nothing: one slug that is not the sender's to hand, or
// one conflict anywhere in the set, rolls back every move.
//
// A RECIPIENT WAITING ON A CLAIM IT IS HANDED IS NOT REFUSED (D-093, narrowing
// D-063). D-063 refused it, reasoning that the recipient would then be waiting
// on its own claim. Measured, that refusal fired on exactly the session a
// handover is FOR: a successor parks on the claim it is about to inherit, so
// 4+ handovers on one fleet each cost a refusal, a round trip asking the
// successor to clear, and a hand-rolled poll — one of them over a wait that
// had EXPIRED 14 minutes earlier and still blocked (the count ignored the
// deadline). Nothing is repaired or cleared here: a target the waiter itself
// now holds counts as landed, computed at read time (WaitTarget.HandedToWaiter
// in wait.go), so the move in this transaction IS the resolution, and the
// recipient's `wait check` and beat say LANDED "handed to you". D-063's worry
// — that clearing one target of a multi-target wait lands it early — does not
// arise: the other targets stay pending until they close.
//
// WHAT IT DOES NOT DO. Ask the recipient: consent was considered and cut —
// the holder already decides how long the scopes stay reserved, and the
// skill sequences the move after the successor answers. Record the previous
// holder: no schema for it; the CLI's notice to the recipient names the
// sender, and that notice can fail after the move commits, so history is
// simply not kept. Move the sender's other claims, its messages, or its
// waits: the claims named, by slug, and nothing else.

// ErrTransferRefused is a transfer refused before anything was written. The
// message names a remedy; it carries peer labels, and the caller fences it.
type ErrTransferRefused struct{ Why string }

func (e ErrTransferRefused) Error() string { return e.Why }

// Moved is one claim a transfer moved: its slug as named and its id.
// RecipientWaited says the recipient had an open wait naming it, which the
// move has just resolved (D-093).
type Moved struct {
	Slug            string
	ClaimID         string
	RecipientWaited bool
}

// TransferClaim moves the sender's open claim `slug` to the resolved session
// `to`, returning the claim id and the recipient's label as read inside the
// transaction.
func (s *Store) TransferClaim(sessionID, incarnation, slug string, to Target) (claimID, toLabel string, err error) {
	moved, toLabel, err := s.TransferClaims(sessionID, incarnation, []string{slug}, to)
	if err != nil {
		return "", "", err
	}
	return moved[0].ClaimID, toLabel, nil
}

// TransferClaims moves every named open claim of the sender to `to` in ONE
// immediate transaction, all or nothing, in the order named (a repeated slug
// counts once). See the header for what it refuses and why the set is one.
func (s *Store) TransferClaims(sessionID, incarnation string, slugs []string, to Target) (moved []Moved, toLabel string, err error) {
	if len(slugs) == 0 {
		return nil, "", errors.New("a transfer names at least one claim")
	}
	if err := to.check(); err != nil {
		return nil, "", err
	}
	if to.ID == AllTarget {
		return nil, "", ErrTransferRefused{Why: fmt.Sprintf("a claim is held by one session; %q is every session", AllTarget)}
	}
	if to.ID == sessionID {
		return nil, "", ErrTransferRefused{Why: fmt.Sprintf("claim %q is already yours", slugs[0])}
	}
	err = s.tx(func(tx *sql.Tx) error {
		now := s.now()
		moved = nil

		var live int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE session_id=? AND incarnation=? AND ended IS NULL`,
			sessionID, incarnation).Scan(&live); err != nil {
			return err
		}
		if live == 0 {
			return ErrTransferRefused{Why: fmt.Sprintf("session %s (incarnation %s) is not live, so it hands nothing on; a claim it left open is freed by `buddy sweep`", sessionID, incarnation)}
		}

		seen := map[string]bool{}
		for _, slug := range slugs {
			if seen[slug] {
				continue
			}
			seen[slug] = true
			var id string
			err := tx.QueryRow(`SELECT claim_id FROM claims WHERE slug=? AND session_id=? AND incarnation=? AND state='open'`,
				slug, sessionID, incarnation).Scan(&id)
			if errors.Is(err, sql.ErrNoRows) {
				return s.whyNotReleased(tx, sessionID, slug)
			}
			if err != nil {
				return err
			}
			moved = append(moved, Moved{Slug: slug, ClaimID: id})
		}

		var inc string
		var ended sql.NullInt64
		err := tx.QueryRow(`SELECT incarnation, label, ended FROM sessions WHERE session_id=?`, to.ID).Scan(&inc, &toLabel, &ended)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTransferRefused{Why: fmt.Sprintf("session %s is no longer in the ledger", to.ID)}
		}
		if err != nil {
			return err
		}
		if ended.Valid {
			return ErrTransferRefused{Why: fmt.Sprintf("%s said bye %s ago; hand it to a live session", toLabel, humanAge(now.Sub(time.Unix(ended.Int64, 0))))}
		}
		if inc != to.Incarnation {
			return ErrTransferRefused{Why: fmt.Sprintf("%s re-registered after it was resolved (it is a new incarnation, not the one you named); check `buddy who %s` and hand it on again", toLabel, to.ID)}
		}

		// Every claim moves BEFORE any is scanned, so a sibling moving in the
		// same set is the new owner's and never a conflict.
		for i, m := range moved {
			// Reported, never refused (D-093): the move resolves the wait.
			// Counted over the recipient's open declaration of its current
			// incarnation, the one its check would read.
			var waiting int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM session_wait_targets t
				JOIN session_waits w ON w.session_id=t.session_id AND w.decl=t.decl
				WHERE t.claim_id=? AND w.session_id=? AND w.incarnation=? AND w.cleared IS NULL`,
				m.ClaimID, to.ID, inc).Scan(&waiting); err != nil {
				return err
			}
			moved[i].RecipientWaited = waiting > 0
			if _, err := tx.Exec(`UPDATE claims SET session_id=?, incarnation=?, renewed=? WHERE claim_id=? AND state='open'`,
				to.ID, inc, now.Unix(), m.ClaimID); err != nil {
				return err
			}
		}

		var conflicts []Conflict
		for _, m := range moved {
			var shared bool
			if err := tx.QueryRow(`SELECT shared FROM claims WHERE claim_id=?`, m.ClaimID).Scan(&shared); err != nil {
				return err
			}
			scopes, err := scopesOf(tx, m.ClaimID)
			if err != nil {
				return err
			}
			c, err := s.scopeConflicts(tx, to.ID, scopes, shared)
			if err != nil {
				return err
			}
			conflicts = append(conflicts, c...)
		}
		if len(conflicts) > 0 {
			return refusedFrom(conflicts)
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return moved, toLabel, nil
}

// scopesOf reads one claim's scopes inside the caller's transaction.
func scopesOf(tx *sql.Tx, claimID string) ([]string, error) {
	rows, err := tx.Query(`SELECT scope FROM claim_scopes WHERE claim_id=?`, claimID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var scopes []string
	for rows.Next() {
		var sc string
		if err := rows.Scan(&sc); err != nil {
			return nil, err
		}
		scopes = append(scopes, sc)
	}
	return scopes, rows.Err()
}
