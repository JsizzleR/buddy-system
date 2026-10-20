package store

import (
	"database/sql"
	"errors"
)

// ReleaseClaims releases several of the caller's open claims in ONE
// immediate transaction, all or nothing (D-093): `buddy release a b c`.
//
// WHY THE SAME RULE AS `release --to`. A lane winding down releases two or
// more claims every time (its exclusive slug and its `-shared` one, the
// skill's recipe), and a fleet's for-loops over `release` are where a typo'd
// or already-handed slug used to leave the loop half done: the claims before
// it released, the ones after it still held, and the result lines scattered
// over the loop's output. Whole or nothing gives the caller one answer. It
// takes no outcome and no scope narrowing: an outcome reports on ONE job, and
// a narrowing names scopes of ONE claim, so both stay single-slug.
//
// The ids come back in the order named (a repeated slug counts once), so the
// caller can name each claim's waiters from the release's own transaction, as
// ReleaseOutcome's caller does (D-033).
func (s *Store) ReleaseClaims(sessionID, incarnation string, slugs []string) ([]string, error) {
	if len(slugs) == 0 {
		return nil, errors.New("a release names at least one claim")
	}
	var ids []string
	err := s.tx(func(tx *sql.Tx) error {
		ids = nil
		now := s.now().Unix()
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
			if _, err := tx.Exec(`UPDATE claims SET state='released', renewed=?, outcome='', outcome_note='' WHERE claim_id=?`, now, id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}
