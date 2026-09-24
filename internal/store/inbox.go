package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CORRECTIONS (D-043, issue #34, wishlist §4). A coordinator broadcast seven
// wrong measurements in one fleet run, and each was caught by a session,
// sometimes after peers had acted on it. There was no way for a correction to
// name what it corrected, and no way to answer "who has seen it?".
//
// A correction is a message with a link. It ANNOTATES and never withholds:
// the original is still delivered wherever it is still queued, marked, because
// a correction may be partial ("ignore point 3") and dropping the original
// would lose the rest. Withholding would also hand one session power over
// what another sees. Three rules make the link trustworthy (Codex design pass):
//   - OWNERSHIP by the sending SESSION, recorded from the resolved identity
//     and never from --from. A session corrects only its own messages; the
//     operator at a bare terminal only messages sent with no session; and a
//     message whose sender is UNKNOWN (NULL: sent before the column existed,
//     or from a session id that did not resolve) cannot be corrected at all.
//   - AUDIENCE: a correction goes to exactly the original's audience. A direct
//     message's target, or a broadcast's recipient SNAPSHOT copied row for
//     row, not a fresh one. Otherwise a session could be told "SUPERSEDED by
//     #11" and never be sent #11, or be sent a correction of a message it
//     never had.
//   - One transaction checks ownership and audience and writes the link.

// SendOpts is what a send knows about itself beyond the target and text.
type SendOpts struct {
	// SenderSession is the session sending ("" = the operator, no session).
	// SenderKnown false records the sender as UNKNOWN: a session id was
	// present and did not resolve to a live session, which must never be
	// read as the operator.
	SenderSession string
	SenderKnown   bool
	// Supersedes is the message this one corrects; 0 = none.
	Supersedes int64
}

// ErrNotCorrectable is a refused --supersedes, naming why.
type ErrNotCorrectable struct {
	ID     int64
	Reason string
}

func (e ErrNotCorrectable) Error() string {
	return fmt.Sprintf("cannot correct message #%d: %s", e.ID, e.Reason)
}

// Send queues a message against a RESOLVED target and returns its id. A
// broadcast snapshots its live recipients in the same transaction, unless it
// is a correction, which inherits the original's snapshot instead.
func (s *Store) Send(t Target, from, body string, o SendOpts) (int64, error) {
	if err := t.check(); err != nil {
		return 0, err
	}
	var id int64
	err := s.tx(func(tx *sql.Tx) error {
		if o.Supersedes != 0 {
			if err := correctable(tx, t, o); err != nil {
				return err
			}
		}
		var sender any // NULL when unknown
		if o.SenderKnown {
			sender = o.SenderSession
		}
		res, err := tx.Exec(`INSERT INTO inbox (target, sender, body, created, sender_session, supersedes) VALUES (?,?,?,?,?,?)`,
			t.ID, from, body, s.now().Unix(), sender, o.Supersedes)
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		if t.ID != AllTarget {
			return nil
		}
		if o.Supersedes != 0 {
			_, err = tx.Exec(`INSERT INTO inbox_recipient (msg_id, session_id)
				SELECT ?, session_id FROM inbox_recipient WHERE msg_id=?`, id, o.Supersedes)
			return err
		}
		_, err = tx.Exec(`INSERT INTO inbox_recipient (msg_id, session_id)
			SELECT ?, session_id FROM sessions WHERE ended IS NULL`, id)
		return err
	})
	return id, err
}

// CheckCorrection is the dry run's view of a correction: the same test Send
// makes, in one read snapshot, writing nothing.
func (s *Store) CheckCorrection(t Target, o SendOpts) error {
	if err := t.check(); err != nil {
		return err
	}
	return s.readSnapshot(func(q querier) error { return correctable(q, t, o) })
}

// correctable is the whole authority test for a correction: inside the send's
// own transaction, or the dry run's snapshot.
func correctable(q rowQuerier, t Target, o SendOpts) error {
	var target string
	var owner sql.NullString
	err := q.QueryRow(`SELECT target, sender_session FROM inbox WHERE msg_id=?`, o.Supersedes).Scan(&target, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotCorrectable{o.Supersedes, "no such message"}
	}
	if err != nil {
		return err
	}
	switch {
	case !owner.Valid:
		return ErrNotCorrectable{o.Supersedes, "its sender is unknown (sent before senders were recorded, or from a session that did not resolve), so nobody can claim it"}
	case !o.SenderKnown:
		return ErrNotCorrectable{o.Supersedes, "this send could not tell which session is sending"}
	case owner.String != o.SenderSession:
		who := "the operator (no session)"
		if owner.String != "" {
			who = "session " + owner.String
		}
		return ErrNotCorrectable{o.Supersedes, "it was sent by " + who + ", and a session corrects only its own messages"}
	case target != t.ID:
		return ErrNotCorrectable{o.Supersedes, fmt.Sprintf("it was addressed to %q, and a correction goes to exactly the original's audience", target)}
	}
	return nil
}

// links fills each message's correction links as they stand for sessionID.
// One indexed point query per drained message (inbox_supersedes), and the
// delivery lookups only for a message that IS a correction.
func (s *Store) links(sessionID string, msgs []InboxMsg) error {
	cutoff := s.now().Add(-BroadcastKeep)
	for i := range msgs {
		m := &msgs[i]
		// `supersedes<>0` repeated so the planner can use the partial index.
		rows, err := s.db.Query(`SELECT msg_id FROM inbox WHERE supersedes=? AND supersedes<>0 ORDER BY msg_id`, m.ID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			m.SupersededBy = append(m.SupersededBy, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if m.Supersedes == 0 {
			continue
		}
		st, err := s.deliveryOf(m.Supersedes, sessionID, cutoff)
		if err != nil {
			return err
		}
		m.OrigDelivered, m.OrigExpired = st.Delivered, st.Expired
	}
	return nil
}

// Delivery is one message's standing with one addressed session: a RECORDED
// delivery (the hook wrote it into that session's context and marked it), or
// expired undelivered (a broadcast past BroadcastKeep), or neither (queued).
// A recorded delivery is evidence of a write, never of reading. A write whose
// mark then failed leaves no row, and is redelivered.
type Delivery struct {
	Delivered time.Time
	Expired   bool
}

func (s *Store) deliveryOf(msgID int64, sessionID string, cutoff time.Time) (Delivery, error) {
	var d Delivery
	var at int64
	err := s.db.QueryRow(`SELECT delivered FROM inbox_delivery WHERE msg_id=? AND session_id=?`, msgID, sessionID).Scan(&at)
	switch {
	case err == nil:
		d.Delivered = time.Unix(at, 0)
		return d, nil
	case !errors.Is(err, sql.ErrNoRows):
		return d, err
	}
	var target string
	var created int64
	if err := s.db.QueryRow(`SELECT target, created FROM inbox WHERE msg_id=?`, msgID).Scan(&target, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return d, nil
		}
		return d, err
	}
	d.Expired = target == AllTarget && time.Unix(created, 0).Before(cutoff)
	return d, nil
}

// SentRecipient is one addressed session in a sender's report.
type SentRecipient struct {
	SessionID string
	Label     string // "" when the session row is gone
	Delivery
	// Original is this session's standing with the message a correction
	// corrects; zero for a message that corrects nothing.
	Original Delivery
}

// SentInfo is the sender-side report on one message. It carries no body.
type SentInfo struct {
	ID            int64
	Target        string
	Sender        string
	Created       time.Time
	Supersedes    int64
	SupersededBy  []int64
	OwnerKnown    bool
	SenderSession string
	Recipients    []SentRecipient
}

// Sent reports one message: its links and, for every session it addressed,
// whether a delivery is recorded. The report grants nothing and reads no body.
func (s *Store) Sent(id int64) (SentInfo, bool, error) {
	var si SentInfo
	var owner sql.NullString
	err := s.db.QueryRow(`SELECT msg_id, target, sender, created, supersedes, sender_session FROM inbox WHERE msg_id=?`, id).
		Scan(&si.ID, &si.Target, &si.Sender, unixScan{&si.Created}, &si.Supersedes, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		return si, false, nil
	}
	if err != nil {
		return si, false, err
	}
	si.OwnerKnown, si.SenderSession = owner.Valid, owner.String
	msgs := []InboxMsg{{ID: si.ID}}
	if err := s.links("", msgs); err != nil {
		return si, false, err
	}
	si.SupersededBy = msgs[0].SupersededBy

	var addressed []string
	if si.Target == AllTarget {
		rows, err := s.db.Query(`SELECT session_id FROM inbox_recipient WHERE msg_id=? ORDER BY session_id`, id)
		if err != nil {
			return si, false, err
		}
		for rows.Next() {
			var sid string
			if err := rows.Scan(&sid); err != nil {
				rows.Close()
				return si, false, err
			}
			addressed = append(addressed, sid)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return si, false, err
		}
	} else {
		addressed = []string{si.Target}
	}
	cutoff := s.now().Add(-BroadcastKeep)
	for _, sid := range addressed {
		r := SentRecipient{SessionID: sid}
		_ = s.db.QueryRow(`SELECT label FROM sessions WHERE session_id=?`, sid).Scan(&r.Label)
		if r.Delivery, err = s.deliveryOf(id, sid, cutoff); err != nil {
			return si, false, err
		}
		if si.Supersedes != 0 {
			if r.Original, err = s.deliveryOf(si.Supersedes, sid, cutoff); err != nil {
				return si, false, err
			}
		}
		si.Recipients = append(si.Recipients, r)
	}
	return si, true, nil
}

// SentBy lists the newest messages sent by one session ("" = the operator),
// newest first, at most limit, as full reports.
func (s *Store) SentBy(senderSession string, limit int) ([]SentInfo, error) {
	rows, err := s.db.Query(`SELECT msg_id FROM inbox WHERE sender_session=? ORDER BY msg_id DESC LIMIT ?`, senderSession, limit)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]SentInfo, 0, len(ids))
	for _, id := range ids {
		si, ok, err := s.Sent(id)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, si)
		}
	}
	return out, nil
}
