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
//
// Both tests fold the stored SPELLING, never the folded column (D-222): that
// column holds the key a binary before D-222 reads, so a row it wrote (or one
// this binary writes) under one spelling is the same entry as every spelling
// the fold calls the same. Keyed by the column, `rm` of a row written as
// `ſtyle.md` answered "not on the list" under every spelling and the row held
// one of the eight slots for good (D-100's class), and `add` listed a second
// row for the same file (panel lens c, Codex design pass P2).
func (s *Store) AuthorityAdd(path string) error {
	return s.tx(func(tx *sql.Tx) error {
		listed, err := authorityRows(tx)
		if err != nil {
			return err
		}
		pf, legacy, others := fold(path), legacyFold(path), 0
		var collides string
		for _, r := range listed {
			if fold(r.path) == pf {
				return nil
			}
			if r.folded == legacy {
				collides = r.path
			}
			others++
		}
		if others >= MaxAuthority {
			return fmt.Errorf("the watch list holds %d paths and every one is a stat on every tool call; remove one first", MaxAuthority)
		}
		// The column's own uniqueness is on the old key: a path whose old key
		// equals a listed one's while the fold tells them apart (U+0130 against
		// i) cannot be stored. Refused, naming the entry, where the insert's
		// DO NOTHING printed "watching" and stored nothing (Fable cross-check).
		if collides != "" {
			return fmt.Errorf("%s cannot be listed beside %s: they share the key an older buddy reads (D-222); remove %s first", path, collides, collides)
		}
		_, err = tx.Exec(`INSERT INTO authority (path, folded, added) VALUES (?,?,?)
			ON CONFLICT(folded) DO NOTHING`, path, legacy, s.now().Unix())
		return err
	})
}

// AuthorityRemove takes a path off the list; false when it was not there.
// Every row whose spelling folds to the path's is removed, by its own stored
// key (D-222; see AuthorityAdd).
func (s *Store) AuthorityRemove(path string) (bool, error) {
	removed := false
	err := s.tx(func(tx *sql.Tx) error {
		listed, err := authorityRows(tx)
		if err != nil {
			return err
		}
		pf := fold(path)
		for _, r := range listed {
			if fold(r.path) != pf {
				continue
			}
			if _, err := tx.Exec(`DELETE FROM authority WHERE folded=?`, r.folded); err != nil {
				return err
			}
			removed = true
		}
		return nil
	})
	return removed && err == nil, err
}

type authorityRow struct{ path, folded string }

// authorityRows reads the whole list: at most MaxAuthority rows.
func authorityRows(tx *sql.Tx) ([]authorityRow, error) {
	rows, err := tx.Query(`SELECT path, folded FROM authority`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []authorityRow
	for rows.Next() {
		var r authorityRow
		if err := rows.Scan(&r.path, &r.folded); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
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
