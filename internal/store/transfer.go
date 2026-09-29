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
//   - a recipient waiting on this very claim: it would be waiting on its own
//     claim, which only it can land (the rule DeclareWait enforces). Refused,
//     never repaired: clearing one target of a multi-target wait can LAND it
//     without the claim having finished.
//   - an overlap the recipient could not have claimed itself. A session may
//     hold overlapping claims of its own, so the sender's X on `pkg` beside
//     its Y on `pkg/f` is legal, and handing X alone to B would leave two
//     sessions holding one path. The ordinary conflict scan runs against the
//     NEW owner, after the move, inside the transaction, and a conflict rolls
//     the move back.
//
// WHAT IT DOES NOT DO. Ask the recipient: consent was considered and cut —
// the holder already decides how long the scopes stay reserved, and the
// skill sequences the move after the successor answers. Record the previous
// holder: no schema for it; the CLI's notice to the recipient names the
// sender, and that notice can fail after the move commits, so history is
// simply not kept. Move the sender's other claims, its messages, or its
// waits: one claim, by slug.

// ErrTransferRefused is a transfer refused before anything was written. The
// message names a remedy; it carries peer labels, and the caller fences it.
type ErrTransferRefused struct{ Why string }

func (e ErrTransferRefused) Error() string { return e.Why }

// TransferClaim moves the sender's open claim `slug` to the resolved session
// `to`, returning the claim id and the recipient's label as read inside the
// transaction.
func (s *Store) TransferClaim(sessionID, incarnation, slug string, to Target) (claimID, toLabel string, err error) {
	if err := to.check(); err != nil {
		return "", "", err
	}
	if to.ID == AllTarget {
		return "", "", ErrTransferRefused{Why: fmt.Sprintf("a claim is held by one session; %q is every session", AllTarget)}
	}
	if to.ID == sessionID {
		return "", "", ErrTransferRefused{Why: fmt.Sprintf("claim %q is already yours", slug)}
	}
	err = s.tx(func(tx *sql.Tx) error {
		now := s.now()

		var live int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE session_id=? AND incarnation=? AND ended IS NULL`,
			sessionID, incarnation).Scan(&live); err != nil {
			return err
		}
		if live == 0 {
			return ErrTransferRefused{Why: fmt.Sprintf("session %s (incarnation %s) is not live, so it hands nothing on; a claim it left open is freed by `buddy sweep`", sessionID, incarnation)}
		}

		err := tx.QueryRow(`SELECT claim_id FROM claims WHERE slug=? AND session_id=? AND incarnation=? AND state='open'`,
			slug, sessionID, incarnation).Scan(&claimID)
		if errors.Is(err, sql.ErrNoRows) {
			return s.whyNotReleased(tx, sessionID, slug)
		}
		if err != nil {
			return err
		}

		var inc string
		var ended sql.NullInt64
		err = tx.QueryRow(`SELECT incarnation, label, ended FROM sessions WHERE session_id=?`, to.ID).Scan(&inc, &toLabel, &ended)
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

		var waiting int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM session_wait_targets t
			JOIN session_waits w ON w.session_id=t.session_id AND w.decl=t.decl
			WHERE t.claim_id=? AND w.session_id=? AND w.incarnation=? AND w.cleared IS NULL`,
			claimID, to.ID, inc).Scan(&waiting); err != nil {
			return err
		}
		if waiting > 0 {
			return ErrTransferRefused{Why: fmt.Sprintf("%s is waiting on this claim, and would then be waiting on its own; it runs `buddy wait clear` (or declares without it) first", toLabel)}
		}

		if _, err := tx.Exec(`UPDATE claims SET session_id=?, incarnation=?, renewed=? WHERE claim_id=? AND state='open'`,
			to.ID, inc, now.Unix(), claimID); err != nil {
			return err
		}

		var scopes []string
		var shared bool
		if err := tx.QueryRow(`SELECT shared FROM claims WHERE claim_id=?`, claimID).Scan(&shared); err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT scope FROM claim_scopes WHERE claim_id=?`, claimID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var sc string
			if err := rows.Scan(&sc); err != nil {
				rows.Close()
				return err
			}
			scopes = append(scopes, sc)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		conflicts, err := s.scopeConflicts(tx, to.ID, scopes, shared)
		if err != nil {
			return err
		}
		if len(conflicts) > 0 {
			return refusedFrom(conflicts)
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return claimID, toLabel, nil
}
