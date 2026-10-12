package store

// Logical lifecycle events carry the incarnation captured by their adapter at
// registration. Looking it up when an event arrives would lend a delayed beat,
// Stop or end the successor's identity. A shared app-server pid cannot provide
// the process fence used by Claude hooks, so these entry points require the
// captured token and never register or infer a harness process.

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrLogicalIdentity means a lifecycle event did not establish the expected
// session incarnation. No event-side lookup or implicit hello may repair it.
var ErrLogicalIdentity = errors.New("logical session identity refused")

// BeginLogical explicitly starts a logical session and returns its newly
// captured incarnation. A missing session requires an empty previous token; an
// ended session requires its exact previous token. A live session always refuses:
// a repeated begin is not evidence that its caller owns the live incarnation.
// Adapters which already captured a token use RefreshLogical instead.
func (s *Store) BeginLogical(sessionID, label, worktree, previousIncarnation string) (SessionInfo, error) {
	var out SessionInfo
	if sessionID == "" {
		return out, fmt.Errorf("%w: session id is required", ErrLogicalIdentity)
	}
	err := s.tx(func(tx *sql.Tx) error {
		var current string
		var ended sql.NullInt64
		err := tx.QueryRow(`SELECT incarnation, ended FROM sessions WHERE session_id=?`, sessionID).Scan(&current, &ended)
		missing := errors.Is(err, sql.ErrNoRows)
		switch {
		case missing && previousIncarnation != "":
			return fmt.Errorf("%w: previous session does not exist", ErrLogicalIdentity)
		case err != nil && !missing:
			return err
		case !missing && !ended.Valid:
			return fmt.Errorf("%w: session is live; refresh with its captured incarnation", ErrLogicalIdentity)
		case !missing && (previousIncarnation == "" || current != previousIncarnation):
			return fmt.Errorf("%w: previous incarnation does not match the ended session", ErrLogicalIdentity)
		}
		now := s.now().Unix()
		// Run while the predecessor is still ended: reviving first would make
		// its claims and wait look live and strand them behind the new token.
		if err := cleanupEnded(tx, now); err != nil {
			return err
		}
		incarnation := newToken()
		if missing {
			if label == "" {
				label = defaultLabel(worktree, sessionID)
			}
			_, err = tx.Exec(`INSERT INTO sessions (session_id, incarnation, label, worktree, pid, terminal, started, last_seen)
				VALUES (?,?,?,?,0,'',?,?)`, sessionID, incarnation, label, worktree, now, now)
		} else {
			_, err = tx.Exec(`UPDATE sessions SET incarnation=?, worktree=?, pid=0, terminal='', started=?, last_seen=?, ended=NULL,
				label=COALESCE(NULLIF(?,''), label) WHERE session_id=? AND incarnation=? AND ended IS NOT NULL`,
				incarnation, worktree, now, now, label, sessionID, previousIncarnation)
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM session_procs WHERE session_id=?`, sessionID); err != nil {
			return err
		}
		out, err = logicalSession(tx, sessionID, incarnation, false)
		return err
	})
	return out, err
}

// RefreshLogical is registration's retry path once the adapter has captured a
// token. It keeps that incarnation and never revives an ended one. Registration
// alone is not a busy report, so it preserves any existing idle observation.
func (s *Store) RefreshLogical(sessionID, incarnation, label, worktree string) (SessionInfo, error) {
	var out SessionInfo
	err := s.tx(func(tx *sql.Tx) error {
		if _, err := logicalSession(tx, sessionID, incarnation, false); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE sessions SET last_seen=?, worktree=?, label=COALESCE(NULLIF(?,''), label)
			WHERE session_id=? AND incarnation=? AND ended IS NULL`, s.now().Unix(), worktree, label, sessionID, incarnation)
		if err != nil {
			return err
		}
		out, err = logicalSession(tx, sessionID, incarnation, false)
		return err
	})
	return out, err
}

// BeatLogical records a tool event, clears only this incarnation's idle mark,
// and renews only its covering claims. All three effects share the identity
// check's transaction so a delayed event cannot partially affect a successor.
//
// relPaths are the repo-relative paths the event OBSERVED (a PostToolUse names
// them; "" entries are skipped). Several, because one apply_patch touches
// several files: with a single path, and the lifecycle hook passing none, a
// worker that patched its claimed file every ten minutes showed that claim
// STALE after forty (review F-26, 2026-10-06). Only claims covering an observed
// path renew; a generic busy or idle event renews nothing.
func (s *Store) BeatLogical(sessionID, incarnation string, relPaths ...string) error {
	return s.tx(func(tx *sql.Tx) error {
		if _, err := logicalSession(tx, sessionID, incarnation, false); err != nil {
			return err
		}
		now := s.now().Unix()
		if err := logicalBusy(tx, sessionID, incarnation, now); err != nil {
			return err
		}
		var rels []string
		for _, p := range relPaths {
			if p != "" {
				rels = append(rels, fold(p))
			}
		}
		if len(rels) == 0 {
			return nil
		}
		rows, err := tx.Query(`SELECT c.claim_id, cs.folded FROM claims c JOIN claim_scopes cs ON cs.claim_id=c.claim_id
			WHERE c.session_id=? AND c.incarnation=? AND c.state='open'`, sessionID, incarnation)
		if err != nil {
			return err
		}
		renew := map[string]bool{}
		for rows.Next() {
			var id, scope string
			if err := rows.Scan(&id, &scope); err != nil {
				rows.Close()
				return err
			}
			for _, rel := range rels {
				if scopeCovers(scope, rel) {
					renew[id] = true
				}
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for id := range renew {
			if _, err := tx.Exec(`UPDATE claims SET renewed=? WHERE claim_id=? AND incarnation=? AND state='open'`, now, id, incarnation); err != nil {
				return err
			}
		}
		return nil
	})
}

// BusyLogical covers turns that run no tool. Absence of an idle row remains
// unknown to readers; this records an event, not an inferred working state.
func (s *Store) BusyLogical(sessionID, incarnation string) error {
	return s.tx(func(tx *sql.Tx) error {
		if _, err := logicalSession(tx, sessionID, incarnation, false); err != nil {
			return err
		}
		return logicalBusy(tx, sessionID, incarnation, s.now().Unix())
	})
}

func logicalBusy(tx *sql.Tx, sessionID, incarnation string, now int64) error {
	if _, err := tx.Exec(`UPDATE sessions SET last_seen=? WHERE session_id=? AND incarnation=? AND ended IS NULL`, now, sessionID, incarnation); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM session_idle WHERE session_id=? AND incarnation=?`, sessionID, incarnation)
	return err
}

// IdleLogical records the adapter's idle report under its captured identity.
// A zero since dates the report now; token identity supplies the fence without
// guessing an incarnation from an arrival time or a shared process's lifetime.
func (s *Store) IdleLogical(sessionID, incarnation string, since time.Time) error {
	return s.tx(func(tx *sql.Tx) error {
		if _, err := logicalSession(tx, sessionID, incarnation, false); err != nil {
			return err
		}
		if since.IsZero() {
			since = s.now()
		}
		_, err := tx.Exec(`INSERT INTO session_idle (session_id, incarnation, since) VALUES (?,?,?)
			ON CONFLICT(session_id) DO UPDATE SET incarnation=excluded.incarnation, since=excluded.since`, sessionID, incarnation, since.Unix())
		return err
	})
}

// EndLogical marks only the captured incarnation ended. Repeating that exact
// end succeeds without changing its date; a missing or superseded token refuses.
// As with Bye, claims are never orphaned inline: registration/sweep/claim own
// cleanup, while a late end can never reach a successor's reservations.
func (s *Store) EndLogical(sessionID, incarnation string) error {
	return s.tx(func(tx *sql.Tx) error {
		current, err := logicalSession(tx, sessionID, incarnation, true)
		if err != nil || !current.Live() {
			return err
		}
		if _, err := tx.Exec(`UPDATE sessions SET ended=? WHERE session_id=? AND incarnation=? AND ended IS NULL`, s.now().Unix(), sessionID, incarnation); err != nil {
			return err
		}
		_, err = tx.Exec(`DELETE FROM session_procs WHERE session_id=?`, sessionID)
		return err
	})
}

func logicalSession(tx *sql.Tx, sessionID, incarnation string, allowEnded bool) (SessionInfo, error) {
	var out SessionInfo
	if sessionID == "" || incarnation == "" {
		return out, fmt.Errorf("%w: session id and captured incarnation are required", ErrLogicalIdentity)
	}
	err := tx.QueryRow(`SELECT session_id, incarnation, label, worktree, pid, terminal, started, last_seen, COALESCE(ended,0)
		FROM sessions WHERE session_id=? AND incarnation=?`, sessionID, incarnation).Scan(
		&out.SessionID, &out.Incarnation, &out.Label, &out.Worktree, &out.PID, &out.Terminal,
		unixScan{&out.Started}, unixScan{&out.LastSeen}, unixScan{&out.Ended})
	if errors.Is(err, sql.ErrNoRows) {
		return SessionInfo{}, fmt.Errorf("%w: captured incarnation does not match a registered session", ErrLogicalIdentity)
	}
	if err != nil {
		return SessionInfo{}, err
	}
	if !allowEnded && !out.Live() {
		return SessionInfo{}, fmt.Errorf("%w: captured incarnation has ended", ErrLogicalIdentity)
	}
	return out, nil
}
