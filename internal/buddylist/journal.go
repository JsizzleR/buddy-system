// Package buddylist is the concierge chat daemon: one persistent TOC connection
// to the AIM server, a durable SQLite journal of everything it sees (the room
// history OSCAR lacks), and a unix-socket API for hooks, CLIs, and MCP.
package buddylist

import (
	"database/sql"
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
	var minSeq sql.NullInt64
	if err := j.db.QueryRow(`SELECT MIN(seq) FROM messages WHERE room=?`, room).Scan(&minSeq); err != nil {
		return nil, false, err
	}
	// A cursor below the oldest retained row (and not the fresh-start cursor
	// pointing directly at it) has lost messages to retention.
	gap = minSeq.Valid && after < minSeq.Int64-1 && after > 0
	if !minSeq.Valid && after > 0 {
		gap = true // the whole room's history is gone
	}
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
func (j *Journal) Trim(keep time.Duration) (int64, error) {
	res, err := j.db.Exec(`DELETE FROM messages WHERE at < ?`, j.now().Add(-keep).Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
