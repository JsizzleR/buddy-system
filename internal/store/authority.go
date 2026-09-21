package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// The authority watch list and its one-shot marks (D-028, issue #19).
//
// A session's copy of the standing rules is a snapshot from session start,
// and nothing told a nine-hour-old coordinator that the file it was quoting
// had been corrected on disk seven hours earlier. The list of files worth
// that check lives HERE and not in git config, because the check runs on
// the beat hook and reading git config is a fork (7-9 ms against a 100 ms
// budget) while the ledger is already open. CLAUDE.md is watched whether or
// not it is listed (internal/cli decides that); this is the extension list.

// MaxAuthority bounds the list, because every entry is one Stat on every
// tool call of every session. Three is the measured need (a constitution,
// a playbook, a conventions file); eight is room.
const MaxAuthority = 8

// AuthorityPaths lists the configured watch list, sorted.
func (s *Store) AuthorityPaths() ([]string, error) {
	rows, err := s.db.Query(`SELECT path FROM authority ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AuthorityAdd puts a normalized repo-relative path on the list. Idempotent
// on the folded path; refused past MaxAuthority, naming the bound.
func (s *Store) AuthorityAdd(path string) error {
	return s.tx(func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM authority WHERE folded<>?`, fold(path)).Scan(&n); err != nil {
			return err
		}
		if n >= MaxAuthority {
			return fmt.Errorf("the watch list holds %d paths and every one is a stat on every tool call; remove one first", MaxAuthority)
		}
		_, err := tx.Exec(`INSERT INTO authority (path, folded, added) VALUES (?,?,?)
			ON CONFLICT(folded) DO NOTHING`, path, fold(path), s.now().Unix())
		return err
	})
}

// AuthorityRemove takes a path off the list; false when it was not there.
func (s *Store) AuthorityRemove(path string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM authority WHERE folded=?`, fold(path))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// AuthorityWarnPending reports whether this incarnation has yet to be told
// about path at this modification time. The mtime is the dedup key, in
// nanoseconds so two writes in one second are two changes: a file changed
// twice warns twice, a file changed once warns once, and a file restored to
// an OLDER mtime — or rewritten with the same one — is not detected, which
// the advisory wording states (D-028).
func (s *Store) AuthorityWarnPending(sessionID, incarnation, path string, mtimeNS int64) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM authority_warned WHERE session_id=? AND incarnation=? AND folded=? AND mtime_ns=?`,
		sessionID, incarnation, fold(path), mtimeNS).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return false, err
}

// MarkAuthorityWarned records the notice as delivered. Called only after
// the write that carried it succeeded (at-least-once, like the inbox).
func (s *Store) MarkAuthorityWarned(sessionID, incarnation, path string, mtimeNS int64) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO authority_warned (session_id, incarnation, folded, mtime_ns, warned) VALUES (?,?,?,?,?)`,
		sessionID, incarnation, fold(path), mtimeNS, s.now().Unix())
	return err
}
