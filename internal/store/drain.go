package store

import (
	"database/sql"
	"errors"
	"time"
)

// drain.go — ONE drain at a time per ledger, and a Stop that hands its mail
// over instead of resting on it.
//
// WHAT WAS WRONG (two findings from one fleet of 144 sessions, 2026-10-20).
//
//  1. Three parallel Agent calls in one session each fired PostToolUse, and
//     each `beat` read the same undelivered message, wrote it, and marked it:
//     one message injected three times in one millisecond. The drain was
//     Undelivered (a read) then a write then MarkDelivered (INSERT OR IGNORE),
//     so nothing stopped two readers both finding it unmarked. At-least-once
//     was the contract for a FAILED write; it was never meant to be N-times
//     for N concurrent hooks.
//  2. A message queued between a session's last tool call and its Stop was
//     stranded at its prompt: `msg` saw "last seen 4s ago" and named no wake
//     (D-039: a session seen recently drains on its own), and the Stop hook
//     then marked the session idle with the mail still queued. Six stalls of
//     6 to 75 minutes were confirmed; at least 30 sends per bucket printed no
//     wake for this reason.
//
// WHAT THIS DOES. Drain is the drain with the ledger's write lock held from
// before the read until after the mark: BEGIN IMMEDIATE, read, hand the
// messages to the caller's deliver (which writes the hook document), mark
// what it wrote, commit. A second drain blocks in BEGIN (busy_timeout) until
// the first has committed, and then reads nothing. WRITE-THEN-MARK STANDS: a
// deliver that fails rolls the transaction back and marks nothing, and a
// process that dies after its write but before its commit leaves the rows
// unmarked, so the message is redelivered, exactly as before. Only the race
// between two successful drains is gone.
//
// StopTurn is the Stop hook's write under the same lock: either it hands the
// queued mail to deliver (the caller blocks the Stop with it, so the turn
// continues with its mail) or it records the idle mark, and never neither.
// Because the mail check and the idle mark are one transaction, a `msg`
// whose insert commits before it is seen by the Stop, and one that commits
// after it finds the idle mark when it looks (cmdMsg re-reads idle after its
// insert for exactly this).
//
// CONSIDERED AND CUT. Marking first and writing after (claim, then render):
// it turns a failed write into a lost message, the failure write-then-mark
// exists to prevent. A lease column on inbox_delivery: a schema bump for
// what a transaction already gives. Holding the lock across a stdout write
// costs one pipe write of at most ~9 KB (beat's bound), which the pipe
// buffer takes without the harness reading.
//
// The read runs on ANOTHER pooled connection while this one holds the write
// lock. Under WAL a reader is never blocked, and with the write lock held no
// one else can commit, so the snapshot it reads is the latest and cannot
// move before the mark. That keeps Undelivered (and its link reads) as the
// one query that decides what a session is owed, rather than a second copy
// of its WHERE clause written against a *sql.Tx.

// Drain delivers the session's undelivered mail through deliver, which gets
// the messages read under the write lock (possibly none) and returns the ids
// it wrote. Those, and only those, are marked delivered in the same
// transaction. An error from deliver rolls back and marks nothing.
func (s *Store) Drain(sessionID, label string, deliver func([]InboxMsg) ([]int64, error)) error {
	return s.tx(func(tx *sql.Tx) error {
		msgs, err := s.Undelivered(sessionID, label)
		if err != nil {
			return err
		}
		ids, err := deliver(msgs)
		if err != nil {
			return err
		}
		return s.markDelivered(tx, sessionID, ids)
	})
}

// StopOutcome is what StopTurn did.
type StopOutcome int

const (
	// StopNone: an ended or unknown session, or an earlier incarnation's
	// turn arriving late. Nothing was written.
	StopNone StopOutcome = iota
	// StopIdle: no mail was handed over; the idle mark is recorded.
	StopIdle
	// StopDrained: deliver wrote mail, it is marked delivered, and the
	// session is NOT marked idle (any earlier mark is retracted): the caller
	// has blocked the Stop, so the turn goes on.
	StopDrained
)

// StopTurn is the Stop hook's one transaction: MarkIdle's fence, then, when
// drain is true and mail is queued, deliver it instead of marking idle.
// deliver returning no ids (it wrote nothing, e.g. nothing fitted) falls
// through to the idle mark.
func (s *Store) StopTurn(sessionID, label string, since, fenceAt time.Time, drain bool, deliver func([]InboxMsg) ([]int64, error)) (StopOutcome, error) {
	out := StopNone
	err := s.tx(func(tx *sql.Tx) error {
		inc, ok, err := idleFence(tx, sessionID, since, fenceAt)
		if err != nil || !ok {
			return err
		}
		if drain {
			msgs, err := s.Undelivered(sessionID, label)
			if err != nil {
				return err
			}
			if len(msgs) > 0 {
				ids, err := deliver(msgs)
				if err != nil {
					return err
				}
				if len(ids) > 0 {
					if err := s.markDelivered(tx, sessionID, ids); err != nil {
						return err
					}
					// The turn continues, so the session is not resting: a
					// mark left by an earlier toolless turn would say it is.
					if _, err := tx.Exec(`DELETE FROM session_idle WHERE session_id=?`, sessionID); err != nil {
						return err
					}
					out = StopDrained
					return nil
				}
			}
		}
		if err := s.writeIdle(tx, sessionID, inc, since); err != nil {
			return err
		}
		out = StopIdle
		return nil
	})
	if err != nil {
		return StopNone, err
	}
	return out, nil
}

// InboxRead is one message re-read by a session it is addressed to, with
// that session's delivery of it.
type InboxRead struct {
	Msg       InboxMsg
	Delivered time.Time // zero: no recorded delivery to this session
	Expired   bool      // a broadcast past BroadcastKeep, never delivered
}

// ErrNotAddressed is the one answer for a message id that does not exist,
// is addressed to another session, or travels on the adapter channel: the
// caller learns nothing about another session's mail from the difference.
var ErrNotAddressed = errors.New("not addressed to this session")

// InboxMessage returns message id if it is addressed to the session — by its
// id or label (the predicate Undelivered uses), or a broadcast whose
// recipient snapshot holds it — in any delivery state, expired broadcasts
// included. It marks nothing. Adapter mail is its own register (D-076) and
// is not readable here.
func (s *Store) InboxMessage(sessionID, label string, id int64) (InboxRead, error) {
	var r InboxRead
	m := &r.Msg
	err := s.db.QueryRow(`SELECT m.msg_id, m.sender, m.body, m.created, m.supersedes, m.kind, m.kind_note FROM inbox m
			WHERE m.msg_id=?
			AND ((m.target<>'all' AND m.target IN (?, ?)) OR (m.target='all' AND EXISTS
				(SELECT 1 FROM inbox_recipient r WHERE r.msg_id=m.msg_id AND r.session_id=?)))
			AND NOT EXISTS (SELECT 1 FROM adapter_messages a WHERE a.msg_id=m.msg_id)`,
		id, sessionID, label, sessionID).
		Scan(&m.ID, &m.From, &m.Body, unixScan{&m.Created}, &m.Supersedes, &m.Kind, &m.KindNote)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotAddressed
	}
	if err != nil {
		return r, err
	}
	msgs := []InboxMsg{*m}
	if err := s.links(sessionID, msgs); err != nil {
		return r, err
	}
	r.Msg = msgs[0]
	d, err := s.deliveryOf(id, sessionID, s.now().Add(-BroadcastKeep))
	if err != nil {
		return r, err
	}
	r.Delivered, r.Expired = d.Delivered, d.Expired
	return r, nil
}
