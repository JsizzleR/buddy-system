package store

// The adapter is a consumer of the existing ledger, not a task system. Queue
// identity is captured once and persists beside the inbox row. A later receipt
// cannot look up today's incarnation to repair a message addressed yesterday.
// Reads never certify delivery; the recipient reports delivery separately after
// it has successfully ingested the output. Neither receipt means acknowledged,
// completed, validated, or authorized by the operator.

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const AdapterSchema = "buddy.adapter.v1"
const AdapterMessageLimit = 20
const AdapterBodyLimit = 4096

type AdapterIdentity struct {
	SessionID   string `json:"session_id"`
	Incarnation string `json:"incarnation"`
}

// Status uses observed, observed_absent, unknown, or unsupported. Missing
// telemetry is unknown, not an observed idle/busy state or an empty approval.
type AdapterObservation struct {
	Status string `json:"status"`
	Value  string `json:"value,omitempty"`
}

type AdapterEndpoint struct {
	Status   string `json:"status"`
	Socket   string `json:"socket,omitempty"`
	PID      int    `json:"pid,omitempty"`
	Born     int64  `json:"born,omitempty"`
	Observed int64  `json:"observed_at,omitempty"`
}

type AdapterDelivery struct {
	MessageID    int64              `json:"message_id"`
	State        string             `json:"state"`
	Sender       AdapterIdentity    `json:"sender"`
	Recipient    AdapterIdentity    `json:"recipient"`
	Created      int64              `json:"created_at"`
	Delivered    int64              `json:"delivered_at,omitempty"`
	Body         string             `json:"body,omitempty"`
	Provenance   string             `json:"provenance"`
	Acknowledged AdapterObservation `json:"acknowledged"`
	Completed    AdapterObservation `json:"completed"`
}

type AdapterMessages struct {
	Status string            `json:"status"`
	Count  int               `json:"count"`
	Items  []AdapterDelivery `json:"items"`
	More   bool              `json:"more"`
}

type AdapterRunOutcome struct {
	ClaimID string             `json:"claim_id"`
	Slug    string             `json:"slug"`
	State   AdapterObservation `json:"state"`
	Outcome AdapterObservation `json:"outcome"`
}

type AdapterInspection struct {
	Identity        AdapterIdentity     `json:"identity"`
	Worktree        string              `json:"worktree"`
	Label           string              `json:"label"`
	Lifecycle       AdapterObservation  `json:"lifecycle"`
	ObservedState   AdapterObservation  `json:"observed_state"`
	PendingApproval AdapterObservation  `json:"pending_approval"`
	Endpoint        AdapterEndpoint     `json:"endpoint"`
	QueuedMessages  AdapterMessages     `json:"queued_messages"`
	ReadySHA        AdapterObservation  `json:"ready_sha"`
	RunOutcomes     []AdapterRunOutcome `json:"run_outcomes"`
}

func adapterSession(q rowQuerier, identity AdapterIdentity, allowEnded bool) (SessionInfo, error) {
	var si SessionInfo
	if strings.TrimSpace(identity.SessionID) == "" || strings.TrimSpace(identity.Incarnation) == "" {
		return si, fmt.Errorf("%w: session id and captured incarnation required", ErrLogicalIdentity)
	}
	err := q.QueryRow(`SELECT session_id, incarnation, label, worktree, COALESCE(ended,0)
		FROM sessions WHERE session_id=? AND incarnation=?`, identity.SessionID, identity.Incarnation).
		Scan(&si.SessionID, &si.Incarnation, &si.Label, &si.Worktree, unixScan{&si.Ended})
	if errors.Is(err, sql.ErrNoRows) || err == nil && !allowEnded && !si.Live() {
		return si, fmt.Errorf("%w: captured session is missing, superseded, or ended", ErrLogicalIdentity)
	}
	return si, err
}

// QueueAdapter checks both captured identities and writes both the inbox and
// delivery identity in one transaction. A failed second insert rolls back the
// message, so a tokenless legacy drain cannot see a half-published assignment.
func (s *Store) QueueAdapter(sender, recipient AdapterIdentity, body string) (AdapterDelivery, error) {
	var out AdapterDelivery
	if strings.TrimSpace(body) == "" || !utf8.ValidString(body) || len(body) > AdapterBodyLimit {
		return out, errors.New("adapter body must be nonempty UTF-8 and at most 4096 bytes")
	}
	err := s.tx(func(tx *sql.Tx) error {
		from, err := adapterSession(tx, sender, false)
		if err != nil {
			return err
		}
		if _, err := adapterSession(tx, recipient, false); err != nil {
			return err
		}
		now := s.now().Unix()
		res, err := tx.Exec(`INSERT INTO inbox (target,sender,body,created,sender_session) VALUES (?,?,?,?,?)`,
			recipient.SessionID, from.Label, body, now, sender.SessionID)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO adapter_messages (msg_id,sender_incarnation,recipient_incarnation) VALUES (?,?,?)`,
			id, sender.Incarnation, recipient.Incarnation); err != nil {
			return err
		}
		out = adapterDelivery(id, sender, recipient, now)
		return nil
	})
	return out, err
}

func adapterDelivery(id int64, sender, recipient AdapterIdentity, created int64) AdapterDelivery {
	return AdapterDelivery{MessageID: id, State: "queued", Sender: sender, Recipient: recipient, Created: created,
		Provenance: "peer", Acknowledged: AdapterObservation{Status: "unknown"}, Completed: AdapterObservation{Status: "unknown"}}
}

// AdapterInbox is an explicit bounded fetch, never a consume operation. Only
// adapter-queued messages belong to this versioned incarnation-bound channel;
// the pre-existing operator/legacy inbox retains its separate hook protocol.
func (s *Store) AdapterInbox(recipient AdapterIdentity) (AdapterMessages, error) {
	var out AdapterMessages
	err := s.readSnapshot(func(q querier) error {
		if _, err := adapterSession(q, recipient, false); err != nil {
			return err
		}
		var err error
		out, err = adapterInbox(q, recipient, true, 0)
		return err
	})
	return out, err
}

// AdapterInboxMessage looks up one queued message without confusing a message
// outside the bounded first page with observed absence. Wake adapters use this
// point query before sending a hint; it still never marks body delivery.
func (s *Store) AdapterInboxMessage(recipient AdapterIdentity, id int64) (AdapterMessages, error) {
	var out AdapterMessages
	if id <= 0 {
		return out, errors.New("adapter message id must be positive")
	}
	err := s.readSnapshot(func(q querier) error {
		if _, err := adapterSession(q, recipient, false); err != nil {
			return err
		}
		var err error
		out, err = adapterInbox(q, recipient, true, id)
		return err
	})
	return out, err
}

func adapterInbox(q querier, recipient AdapterIdentity, bodies bool, id int64) (AdapterMessages, error) {
	out := AdapterMessages{Status: "observed", Items: []AdapterDelivery{}}
	where := ` FROM inbox m JOIN adapter_messages a ON a.msg_id=m.msg_id
		WHERE m.target=? AND a.recipient_incarnation=? AND NOT EXISTS
		(SELECT 1 FROM inbox_delivery d WHERE d.msg_id=m.msg_id AND d.session_id=m.target)`
	args := []any{recipient.SessionID, recipient.Incarnation}
	if id != 0 {
		where += ` AND m.msg_id=?`
		args = append(args, id)
	}
	if err := q.QueryRow(`SELECT COUNT(*)`+where, args...).Scan(&out.Count); err != nil {
		return out, err
	}
	rows, err := q.Query(`SELECT m.msg_id,m.sender_session,a.sender_incarnation,m.created,m.body`+where+` ORDER BY m.msg_id LIMIT 20`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, created int64
		var sender AdapterIdentity
		var body string
		if err := rows.Scan(&id, &sender.SessionID, &sender.Incarnation, &created, &body); err != nil {
			return out, err
		}
		msg := adapterDelivery(id, sender, recipient, created)
		if bodies {
			msg.Body = body
		}
		out.Items = append(out.Items, msg)
	}
	out.More = out.Count > len(out.Items)
	return out, rows.Err()
}

// DeliverAdapter only records successful ingestion claimed by the captured
// recipient. The original sender may have ended: durable mail outlives its
// sender, whose token stays exactly as recorded. A duplicate receipt is safe.
func (s *Store) DeliverAdapter(recipient AdapterIdentity, id int64) (AdapterDelivery, error) {
	var out AdapterDelivery
	if id <= 0 {
		return out, errors.New("adapter message id must be positive")
	}
	err := s.tx(func(tx *sql.Tx) error {
		if _, err := adapterSession(tx, recipient, false); err != nil {
			return err
		}
		var sender AdapterIdentity
		var created int64
		err := tx.QueryRow(`SELECT m.sender_session,a.sender_incarnation,m.created FROM inbox m
			JOIN adapter_messages a ON a.msg_id=m.msg_id WHERE m.msg_id=? AND m.target=? AND a.recipient_incarnation=?`,
			id, recipient.SessionID, recipient.Incarnation).Scan(&sender.SessionID, &sender.Incarnation, &created)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("adapter delivery refused: message does not address this captured incarnation")
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO inbox_delivery (msg_id,session_id,delivered) VALUES (?,?,?)`,
			id, recipient.SessionID, s.now().Unix()); err != nil {
			return err
		}
		out = adapterDelivery(id, sender, recipient, created)
		out.State = "delivered"
		return tx.QueryRow(`SELECT delivered FROM inbox_delivery WHERE msg_id=? AND session_id=?`, id, recipient.SessionID).Scan(&out.Delivered)
	})
	return out, err
}

// InspectAdapter is a single ledger snapshot. It does not sample the harness,
// infer busy from missing idle, inspect approvals, check/close waits or consume
// mail. A missing endpoint or approval observation remains unknown.
func (s *Store) InspectAdapter(identity AdapterIdentity) (AdapterInspection, error) {
	out := AdapterInspection{Identity: identity, ObservedState: AdapterObservation{Status: "unknown"},
		PendingApproval: AdapterObservation{Status: "unknown"}, Endpoint: AdapterEndpoint{Status: "unknown"},
		ReadySHA: AdapterObservation{Status: "observed_absent"}, RunOutcomes: []AdapterRunOutcome{}}
	err := s.readSnapshot(func(q querier) error {
		si, err := adapterSession(q, identity, true)
		if err != nil {
			return err
		}
		out.Worktree, out.Label = si.Worktree, si.Label
		out.Lifecycle = AdapterObservation{Status: "observed", Value: "live"}
		if !si.Live() {
			out.Lifecycle.Value = "ended"
		}
		var since int64
		err = q.QueryRow(`SELECT since FROM session_idle WHERE session_id=? AND incarnation=?`, identity.SessionID, identity.Incarnation).Scan(&since)
		if err == nil && si.Live() {
			out.ObservedState = AdapterObservation{Status: "observed", Value: "idle"}
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if si.Live() {
			err = q.QueryRow(`SELECT e.state,e.socket,e.pid,e.born,e.observed FROM session_endpoints e
				JOIN session_procs p ON p.session_id=e.session_id AND p.pid=e.pid AND p.born=e.born
				WHERE e.session_id=? AND e.incarnation=?`, identity.SessionID, identity.Incarnation).
				Scan(&out.Endpoint.Status, &out.Endpoint.Socket, &out.Endpoint.PID, &out.Endpoint.Born, &out.Endpoint.Observed)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		out.QueuedMessages, err = adapterInbox(q, identity, false, 0)
		if err != nil {
			return err
		}
		waits, err := waitsWhere(q, `WHERE w.session_id=? AND w.incarnation=?`, identity.SessionID, identity.Incarnation)
		if err != nil {
			return err
		}
		if len(waits) == 0 {
			return nil
		}
		if waits[0].ReadySHA != "" {
			out.ReadySHA = AdapterObservation{Status: "observed", Value: waits[0].ReadySHA}
		}
		for _, target := range waits[0].Targets {
			run := AdapterRunOutcome{ClaimID: target.ClaimID, Slug: target.Slug, State: AdapterObservation{Status: "unknown"}, Outcome: AdapterObservation{Status: "unknown"}}
			if target.State != "" {
				run.State = AdapterObservation{Status: "observed", Value: target.State}
				run.Outcome.Status = "observed_absent"
				if target.Outcome != "" {
					run.Outcome = AdapterObservation{Status: "observed", Value: target.Outcome}
				}
			}
			out.RunOutcomes = append(out.RunOutcomes, run)
		}
		return nil
	})
	return out, err
}

// RegisterAdapterEndpoint records a receiving-side environment observation.
// The CLI verifies this is the receiving Claude process; the transaction binds
// that captured process birth to the exact logical incarnation. Empty socket
// is an explicit observed absence, never a guessed /tmp/cc-socks path.
func (s *Store) RegisterAdapterEndpoint(identity AdapterIdentity, proc ProcRef, socket string) (AdapterEndpoint, error) {
	var out AdapterEndpoint
	if proc.PID <= 1 || proc.Born <= 0 {
		return out, errors.New("adapter endpoint requires a captured process pid and birth time")
	}
	if socket != "" && (!filepath.IsAbs(socket) || filepath.Clean(socket) != socket || len(socket) > 1024 || strings.ContainsAny(socket, "\x00\r\n")) {
		return out, errors.New("adapter endpoint must be a clean absolute socket path")
	}
	err := s.tx(func(tx *sql.Tx) error {
		if _, err := adapterSession(tx, identity, false); err != nil {
			return err
		}
		now := s.now().Unix()
		if err := registerProc(tx, identity.SessionID, proc, now); err != nil {
			return err
		}
		out = AdapterEndpoint{Status: "observed", Socket: socket, PID: proc.PID, Born: proc.Born, Observed: now}
		if socket == "" {
			out.Status = "observed_absent"
		}
		_, err := tx.Exec(`INSERT INTO session_endpoints (session_id,incarnation,pid,born,state,socket,observed) VALUES (?,?,?,?,?,?,?)
			ON CONFLICT(session_id) DO UPDATE SET incarnation=excluded.incarnation,pid=excluded.pid,born=excluded.born,
			state=excluded.state,socket=excluded.socket,observed=excluded.observed`, identity.SessionID, identity.Incarnation,
			proc.PID, proc.Born, out.Status, socket, now)
		return err
	})
	return out, err
}

// AdapterBacklog is what the legacy views say about a session's ADAPTER mail.
// Undelivered counts the legacy channel only, and adapter rows are excluded
// from it on purpose (legacy hooks carry no captured token), so `who`, `status`
// and `msg` reported "0 undelivered" over a full adapter queue and offered a
// legacy wake that could not surface it (review F-12/F-24, 2026-10-06). Kept as
// a separate register, never merged into the legacy count: the two channels
// are read by different programs.
type AdapterBacklog struct {
	// Known retains positive adapter evidence for this incarnation after its
	// last message is delivered (V-05). Delivery drains a queue, not a channel.
	Known bool
	// Current is the undelivered adapter mail captured for the session's
	// CURRENT incarnation, the queue its adapter's inbox fetch returns.
	Current       int
	CurrentOldest time.Time
	// Earlier is undelivered adapter mail captured for an incarnation that is
	// no longer current. No fetch by the present incarnation returns it.
	Earlier int
	// Endpoint: the receiving harness registered an endpoint for this session
	// (an adapter-run Claude worker). Its wakes belong to its adapter, whose
	// journal serializes them; a legacy socket wake bypasses that (D-070).
	Endpoint bool
}

// AdapterBacklogOf reads both registers in one snapshot.
func (s *Store) AdapterBacklogOf(sessionID, incarnation string) (AdapterBacklog, error) {
	var b AdapterBacklog
	err := s.readSnapshot(func(q querier) error {
		rows, err := q.Query(`SELECT a.recipient_incarnation, m.created FROM inbox m
			JOIN adapter_messages a ON a.msg_id=m.msg_id
			WHERE m.target=? AND NOT EXISTS
			(SELECT 1 FROM inbox_delivery d WHERE d.msg_id=m.msg_id AND d.session_id=m.target)`, sessionID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var inc string
			var created int64
			if err := rows.Scan(&inc, &created); err != nil {
				return err
			}
			if inc != incarnation {
				b.Earlier++
				continue
			}
			b.Current++
			if t := time.Unix(created, 0); b.CurrentOldest.IsZero() || t.Before(b.CurrentOldest) {
				b.CurrentOldest = t
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		var n int
		if err := q.QueryRow(`SELECT COUNT(*) FROM adapter_messages a JOIN inbox m ON m.msg_id=a.msg_id
			WHERE m.target=? AND a.recipient_incarnation=?`, sessionID, incarnation).Scan(&n); err != nil {
			return err
		}
		b.Known = n > 0
		if err := q.QueryRow(`SELECT COUNT(*) FROM session_endpoints WHERE session_id=? AND incarnation=?`, sessionID, incarnation).Scan(&n); err != nil {
			return err
		}
		b.Endpoint = n > 0
		return nil
	})
	return b, err
}

// isAdapterMessage reports whether msg id travels on the adapter channel.
func isAdapterMessage(q rowQuerier, id int64) (bool, error) {
	var n int
	err := q.QueryRow(`SELECT COUNT(*) FROM adapter_messages WHERE msg_id=?`, id).Scan(&n)
	return n > 0, err
}
