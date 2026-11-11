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
// budget) while the ledger is already open. CLAUDE.md and AGENTS.md are watched
// whether or not listed (internal/cli decides that); this is the extension list.

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

// AuthorityLast is the key of the newest authority_warned row for this
// incarnation and path: the copy of the file the session is last known to
// hold, as hello found it, its own write left it, or a notice announced it.
// ok is false when there is no row.
//
// THE COLUMN IS mtime_ns, AND SINCE D-167 IT HOLDS A CONTENT KEY. The table
// keyed the one-shot notice on the file's mtime (D-028), and the token-overhead
// report found 102 of 193 notices in 14 days were the session's own act: its
// own edit, or its own checkout writing the same bytes with a fresh mtime.
// Keying on content needs a value per (incarnation, file) and nothing else;
// this column is one, so no schema bump (none was authorised). internal/cli
// computes the key (a hash of the bytes, or the mtime where the file cannot
// be read); this side stores an int64 and compares it. A row an older binary
// wrote holds an mtime, which never equals a hash in practice, so the only
// cost of a mixed ledger is one more notice.
//
// NEWEST BY ROWID, not by `warned`: that is a Unix second, and a notice and
// the session's own write can land in the same one. AuthorityRecord's
// INSERT OR REPLACE gives a re-recorded key a new rowid, so a file that went
// A, B, A is last known as A, not B.
func (s *Store) AuthorityLast(sessionID, incarnation, path string) (key int64, ok bool, err error) {
	err = s.db.QueryRow(`SELECT mtime_ns FROM authority_warned WHERE session_id=? AND incarnation=? AND folded=? ORDER BY rowid DESC LIMIT 1`,
		sessionID, incarnation, fold(path)).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return key, err == nil, err
}

// AuthorityRecord makes key the newest known copy of path for this
// incarnation. Called at hello (the copy the session started with), after a
// session's own write of the file, and after the hook output that announced a
// change was written (at-least-once, like the inbox): a lost record repeats
// the notice once and costs nothing else.
func (s *Store) AuthorityRecord(sessionID, incarnation, path string, key int64) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO authority_warned (session_id, incarnation, folded, mtime_ns, warned) VALUES (?,?,?,?,?)`,
		sessionID, incarnation, fold(path), key, s.now().Unix())
	return err
}
