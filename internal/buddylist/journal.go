// Package buddylist is the concierge chat daemon: one persistent TOC connection
// to the AIM server, a durable SQLite journal of everything it sees (the room
// history OSCAR lacks), and a unix-socket API for hooks, CLIs, and MCP.
package buddylist

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

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
-- Per-session read cursor: the highest seq a session has been SHOWN in a
-- room. It exists so a returning session can ask for its own backlog in one
-- call instead of walking the room from the retention horizon. It is
-- advisory bookkeeping, never authority: losing it costs a session one
-- re-read, and a session that never acks simply never has one.
CREATE TABLE IF NOT EXISTS read_cursor (
	session TEXT NOT NULL,
	room    TEXT NOT NULL,
	seq     INTEGER NOT NULL,
	at      INTEGER NOT NULL,
	PRIMARY KEY (session, room)
);
`

const (
	// maxReadRows caps one journal read regardless of what a caller asks for.
	maxReadRows = 500
	// A mention token is a name a session answers to. The floor is what stops
	// the filter from degenerating into an unfiltered read; the ceiling and
	// the count keep one call's SQL bounded.
	minMentionToken  = 3
	maxMentionBytes  = 128
	maxMentionTokens = 8
)

var errNoSession = errors.New("no session id: this journal operation is per-session")

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

// ReadOpts selects one window of a room's history.
//
// Selection has three modes and they do not mix: forward from After (the
// default), backwards from Before, or the Tail newest rows. Forward is the
// only mode a read cursor may advance over, because it is the only one that
// cannot leave unseen rows behind it.
type ReadOpts struct {
	Room   string
	After  int64 // seq > After; 0 starts at the retention horizon
	Before int64 // seq < Before; walks backwards from a known point
	Tail   int   // return at most this many of the NEWEST rows
	Limit  int
	// Mentions restricts the window to rows whose body names one of these
	// tokens (case-insensitive substring). Empty means every row.
	Mentions []string
}

// ReadAfter returns up to limit messages in room with seq > after, oldest
// first, plus gap=true when rows between `after` and the oldest retained row
// were trimmed (the caller's cursor predates retention).
func (j *Journal) ReadAfter(room string, after int64, limit int) (msgs []Msg, gap bool, err error) {
	return j.Read(ReadOpts{Room: room, After: after, Limit: limit})
}

// Read returns one window of a room, ALWAYS oldest-first regardless of the
// selection mode, plus gap=true when rows between `After` and the oldest
// retained row were trimmed (the caller's cursor predates retention).
func (j *Journal) Read(o ReadOpts) (msgs []Msg, gap bool, err error) {
	limit := o.Limit
	if limit <= 0 || limit > maxReadRows {
		limit = maxReadRows
	}
	if o.Tail > 0 && o.Tail < limit {
		limit = o.Tail
	}
	horizon, err := j.horizon(o.Room)
	if err != nil {
		return nil, false, err
	}
	// A gap is exact: Trim records, per room, the highest seq it deleted. A
	// cursor below that horizon has provably lost this room's messages to
	// retention; anything else (including seq gaps from other rooms sharing
	// the global AUTOINCREMENT) is not a gap. after=0 is a fresh start, not
	// a stale cursor.
	gap = o.After > 0 && o.After < horizon

	where := []string{"room=?", "seq>?"}
	args := []any{o.Room, o.After}
	if o.Before > 0 {
		where = append(where, "seq<?")
		args = append(args, o.Before)
	}
	clause, margs, err := mentionClause(o.Mentions)
	if err != nil {
		return nil, false, err
	}
	if clause != "" {
		where = append(where, clause)
		args = append(args, margs...)
	}
	// Tail and Before want the rows nearest the NEWEST end of the window, so
	// they are selected descending and flipped back below; LIMIT applied to
	// an ascending scan would hand back the oldest rows instead.
	newestFirst := o.Tail > 0 || o.Before > 0
	order := "ORDER BY seq"
	if newestFirst {
		order = "ORDER BY seq DESC"
	}
	args = append(args, limit)
	rows, err := j.db.Query(`SELECT seq, room, sender, kind, body, at FROM messages
		WHERE `+strings.Join(where, " AND ")+` `+order+` LIMIT ?`, args...)
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
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if newestFirst {
		for i, k := 0, len(msgs)-1; i < k; i, k = i+1, k-1 {
			msgs[i], msgs[k] = msgs[k], msgs[i]
		}
	}
	return msgs, gap, nil
}

func (j *Journal) horizon(room string) (int64, error) {
	var seq int64
	err := j.db.QueryRow(`SELECT seq FROM trim_horizon WHERE room=?`, room).Scan(&seq)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	return seq, nil
}

// mentionClause builds the "names one of these tokens" filter. A token is
// matched as a case-insensitive substring of the body: session labels and
// claim slugs are distinctive strings, and anything token-aware would have to
// guess at the punctuation humans and agents actually write around a name
// ("@alpha,", "[alpha]", "alpha's"). Tokens shorter than minMentionToken are
// REFUSED rather than clamped — a two-character token matches nearly every
// message, which would silently turn an "addressed to me" filter into an
// unfiltered read and hide exactly what it was asked to surface.
func mentionClause(tokens []string) (string, []any, error) {
	if len(tokens) == 0 {
		return "", nil, nil
	}
	if len(tokens) > maxMentionTokens {
		return "", nil, fmt.Errorf("too many mention tokens (%d; cap %d)", len(tokens), maxMentionTokens)
	}
	var parts []string
	var args []any
	for _, t := range tokens {
		t = strings.TrimSpace(t)
		if utf8.RuneCountInString(t) < minMentionToken {
			return "", nil, fmt.Errorf("mention token %q is shorter than %d characters; it would match nearly every message", t, minMentionToken)
		}
		if len(t) > maxMentionBytes {
			return "", nil, fmt.Errorf("mention token is %d bytes; cap %d", len(t), maxMentionBytes)
		}
		// LIKE's own wildcards are data here: an unescaped "%" in a token
		// would widen the filter instead of narrowing it.
		parts = append(parts, `lower(body) LIKE ? ESCAPE '\'`)
		args = append(args, "%"+likeEscape(strings.ToLower(t))+"%")
	}
	return "(" + strings.Join(parts, " OR ") + ")", args, nil
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)

func likeEscape(s string) string { return likeEscaper.Replace(s) }

// Cursor is the highest seq `session` has been shown in `room`; 0 when the
// session has never read it (which is indistinguishable from "read nothing",
// and deliberately so: both mean "start at the horizon").
func (j *Journal) Cursor(session, room string) (int64, error) {
	if session == "" {
		return 0, errNoSession
	}
	var seq int64
	err := j.db.QueryRow(`SELECT seq FROM read_cursor WHERE session=? AND room=?`, session, room).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return seq, err
}

// SetCursor advances a session's read cursor and returns the cursor in force
// afterwards. It is MONOTONIC: a seq below the stored one is recorded as a
// no-op rather than applied. A cursor only ever means "you have been shown
// everything up to here", so moving it backwards would re-deliver a backlog
// the session already has, and letting a late or out-of-order caller do that
// is worse than ignoring it — the caller is told the value that actually
// stands.
func (j *Journal) SetCursor(session, room string, seq int64) (int64, error) {
	if session == "" {
		return 0, errNoSession
	}
	if seq < 0 {
		seq = 0
	}
	if _, err := j.db.Exec(`INSERT INTO read_cursor (session, room, seq, at) VALUES (?,?,?,?)
		ON CONFLICT(session, room) DO UPDATE SET seq=MAX(seq, excluded.seq), at=excluded.at`,
		session, room, seq, j.now().Unix()); err != nil {
		return 0, err
	}
	return j.Cursor(session, room)
}

// RoomStat is one room's backlog for one session. Every field is a COUNT or a
// seq: a digest built from this puts no chat text into anybody's context, so
// it can be surfaced where the messages themselves must not be.
type RoomStat struct {
	Room      string `json:"room"`
	NewestSeq int64  `json:"newest_seq"`
	NewestAt  int64  `json:"newest_at"`
	Retained  int64  `json:"retained"`
	Cursor    int64  `json:"cursor"`
	Unread    int64  `json:"unread"`
	Addressed int64  `json:"addressed"`
	Gap       bool   `json:"gap"`
}

// Stat reports every room the journal retains, with this session's unread and
// addressed-unread counts. An empty session means "no cursor": unread then
// equals everything retained, which is the honest answer for a caller that
// has never read the room, not a claim that the room is unread.
func (j *Journal) Stat(session string, mentions []string) ([]RoomStat, error) {
	clause, margs, err := mentionClause(mentions)
	if err != nil {
		return nil, err
	}
	rows, err := j.db.Query(`SELECT room, MAX(seq), MAX(at), COUNT(*) FROM messages GROUP BY room ORDER BY room`)
	if err != nil {
		return nil, err
	}
	var stats []RoomStat
	for rows.Next() {
		var s RoomStat
		if err := rows.Scan(&s.Room, &s.NewestSeq, &s.NewestAt, &s.Retained); err != nil {
			rows.Close()
			return nil, err
		}
		stats = append(stats, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range stats {
		s := &stats[i]
		if session != "" {
			if s.Cursor, err = j.Cursor(session, s.Room); err != nil {
				return nil, err
			}
		}
		horizon, err := j.horizon(s.Room)
		if err != nil {
			return nil, err
		}
		s.Gap = s.Cursor > 0 && s.Cursor < horizon
		if err := j.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE room=? AND seq>?`,
			s.Room, s.Cursor).Scan(&s.Unread); err != nil {
			return nil, err
		}
		if clause == "" {
			continue
		}
		args := append([]any{s.Room, s.Cursor}, margs...)
		if err := j.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE room=? AND seq>? AND `+clause,
			args...).Scan(&s.Addressed); err != nil {
			return nil, err
		}
	}
	return stats, nil
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
