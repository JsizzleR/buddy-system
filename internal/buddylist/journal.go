// Package buddylist is the concierge chat daemon: one persistent TOC connection
// to the AIM server, a durable SQLite journal of everything it sees (the room
// history OSCAR lacks), and a unix-socket API for hooks, CLIs, and MCP.
package buddylist

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const journalSchema = `
CREATE TABLE IF NOT EXISTS messages (
	seq    INTEGER PRIMARY KEY AUTOINCREMENT,
	room   TEXT NOT NULL,
	sender TEXT NOT NULL,
	kind   TEXT NOT NULL CHECK (kind IN ('chat','im','presence','system')),
	body   TEXT NOT NULL,
	at     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS messages_room_seq ON messages(room, seq);
-- Per-room high-water mark of trimmed seqs. seq is GLOBAL (one AUTOINCREMENT
-- across rooms), so "cursor < MIN(seq) of the room" cannot distinguish trimmed
-- rows from seqs that simply belonged to other rooms; this table can.
CREATE TABLE IF NOT EXISTS trim_horizon (
	room TEXT PRIMARY KEY,
	seq  INTEGER NOT NULL
);
`

// Journal is the durable message store. seq is monotonic for the life of the
// database (AUTOINCREMENT), so a client cursor survives chatd restarts; a
// cursor older than the retention horizon is reported as a gap, never as
// silence.
type Journal struct {
	db  *sql.DB
	now func() time.Time
}

// Msg is one journal row.
type Msg struct {
	Seq    int64  `json:"seq"`
	Room   string `json:"room"`
	Sender string `json:"sender"`
	Kind   string `json:"kind"`
	Body   string `json:"body"`
	At     int64  `json:"at"`
}

func OpenJournal(path string, now func() time.Time) (*Journal, error) {
	if now == nil {
		now = time.Now
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("open journal: %w", err)
	}
	if _, err := db.Exec(journalSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate journal: %w", err)
	}
	return &Journal{db: db, now: now}, nil
}

func (j *Journal) Close() error { return j.db.Close() }

// Append records a message and returns its seq.
func (j *Journal) Append(room, sender, kind, body string) (int64, error) {
	res, err := j.db.Exec(`INSERT INTO messages (room, sender, kind, body, at) VALUES (?,?,?,?,?)`,
		room, sender, kind, body, j.now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ReadAfter returns up to limit messages in room with seq > after, oldest
// first, plus gap=true when rows between `after` and the oldest retained row
// were trimmed (the caller's cursor predates retention).
func (j *Journal) ReadAfter(room string, after int64, limit int) (msgs []Msg, gap bool, err error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	// A gap is exact: Trim records, per room, the highest seq it deleted. A
	// cursor below that horizon has provably lost this room's messages to
	// retention; anything else (including seq gaps from other rooms sharing
	// the global AUTOINCREMENT) is not a gap. after=0 is a fresh start, not
	// a stale cursor.
	var horizon int64
	err = j.db.QueryRow(`SELECT seq FROM trim_horizon WHERE room=?`, room).Scan(&horizon)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	gap = after > 0 && after < horizon
	rows, err := j.db.Query(`SELECT seq, room, sender, kind, body, at FROM messages
		WHERE room=? AND seq>? ORDER BY seq LIMIT ?`, room, after, limit)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var m Msg
		if err := rows.Scan(&m.Seq, &m.Room, &m.Sender, &m.Kind, &m.Body, &m.At); err != nil {
			return nil, false, err
		}
		msgs = append(msgs, m)
	}
	return msgs, gap, rows.Err()
}

// Trim deletes messages older than keep, returning how many were removed.
// Before deleting it records each affected room's highest trimmed seq in
// trim_horizon, so ReadAfter can report exact gaps; both steps share one
// transaction — a horizon without its delete (or vice versa) would lie.
// Known caveat: trims performed before this table existed left no horizon,
// so gaps they created go unreported (a false NEGATIVE that heals with the
// first post-upgrade trim); back-filling from MIN(seq) would instead revive
// the false-positive heuristic this replaced.
func (j *Journal) Trim(keep time.Duration) (int64, error) {
	cutoff := j.now().Add(-keep).Unix()
	tx, err := j.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO trim_horizon (room, seq)
		SELECT room, MAX(seq) FROM messages WHERE at < ? GROUP BY room
		ON CONFLICT(room) DO UPDATE SET seq=MAX(seq, excluded.seq)`, cutoff); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`DELETE FROM messages WHERE at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, tx.Commit()
}
