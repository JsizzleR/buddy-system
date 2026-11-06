package store

import (
	"database/sql"
	"errors"
	"time"
)

// browser.go — when a session last completed a browser tool call (D-134, issue
// #64): the reading behind `who`'s "browser tools used 3m ago".
//
// THE FAILURE. Now and then, closing an orchestrator that had browser work lost
// its tabs or a running job. Those live only in the session, and the close test
// read claims, wait and shells, all of which such a session passes. A rule in
// the skills (hold .buddy/slot/browser) rests on the session remembering, so
// this records what it did whether or not it took the slot.
//
// AN OBSERVATION, in the sense of invariant 10: a tool call completed. Not that
// a tab is open, and its absence is not that none is. No verb refuses, releases
// or decides on it. The tool's name is not stored: the reader does not need it,
// and a stored name would be one more value to fence on its way back out.

// RecordBrowserUse records a completed browser tool call for this incarnation,
// the shape of RecordContext: a silent no-op for a session that has ended or
// been revived under another incarnation, so a beat racing a bye revives
// nothing. A new incarnation replaces the row whole, and within one the reading
// never moves backwards.
//
// WHAT THE FENCE DOES NOT COVER. The hook payload names no incarnation, so the
// caller's token was read from the session row when the hook ran: a hook delayed
// across a bye and a hello stamps the new incarnation with the old call. It is
// accepted because this is a report: the wrong answer is one extra question
// before a close, never a refusal (Codex design pass, D-134).
func (s *Store) RecordBrowserUse(sessionID, incarnation string) error {
	return s.tx(func(tx *sql.Tx) error {
		var inc string
		err := tx.QueryRow(`SELECT incarnation FROM sessions WHERE session_id=? AND ended IS NULL`, sessionID).Scan(&inc)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if inc != incarnation {
			return nil
		}
		_, err = tx.Exec(`INSERT INTO session_browser (session_id, incarnation, used) VALUES (?,?,?)
			ON CONFLICT(session_id) DO UPDATE SET incarnation=excluded.incarnation, used=excluded.used
			WHERE excluded.incarnation<>session_browser.incarnation OR excluded.used>=session_browser.used`,
			sessionID, incarnation, s.now().Unix())
		return err
	})
}

// BrowserUse returns when this incarnation last completed a browser tool call.
// false means none was observed, which is not that none was made. A row from an
// earlier incarnation is not this one's: that process is gone, and this one has
// no way to know what it left open, so a resume after a bye starts silent.
func (s *Store) BrowserUse(sessionID, incarnation string) (time.Time, bool, error) {
	var used int64
	err := s.db.QueryRow(`SELECT used FROM session_browser WHERE session_id=? AND incarnation=?`,
		sessionID, incarnation).Scan(&used)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return time.Unix(used, 0), true, nil
}
