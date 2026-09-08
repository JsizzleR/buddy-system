package buddylist

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// RoomAlert is one room's unalerted addressed backlog for one session.
//
// It carries seqs and a count, never a body: the alert exists to say "go
// look", and auto-injecting room text into an agent's context is exactly what
// the MCP reader's byte budget and fence exist to make deliberate.
type RoomAlert struct {
	Room string `json:"room"`
	// Msgs are the matching rows, oldest first. Bodies are present on the
	// wire because the same rows answer "how many" and "which seqs"; the
	// renderer prints neither.
	Msgs []Msg `json:"msgs,omitempty"`
	// More reports that the scan hit its row cap, so the alert is a FLOOR:
	// the remainder arrives on the next call, once this one is acked.
	More bool `json:"more,omitempty"`
	// Advance is the seq the alert cursor may move to once these rows have
	// been REPORTED. It is the end of the window the scan provably covered,
	// which is the room's newest seq unless the cap cut the scan short.
	Advance int64 `json:"advance,omitempty"`
}

const (
	// alertRowCap bounds one room's alert. The alert is counts and seqs, so
	// the cap is about the SCAN, not about output size.
	alertRowCap = 20
	// ownSendWindow is how many of a session's own submissions the
	// self-authored filter compares against. A session's echo comes back
	// within seconds of its send, so a small window covers it; the cost of a
	// miss is one self-alert, not a lost message.
	ownSendWindow = 25
	// sentRoom is the outbox pseudo-room. Say writes one row here per
	// submission, BEFORE the server echoes it, so every row in it is
	// self-authored by construction — alerting on it would tell every session
	// about its own posts.
	sentRoom = "@sent"
)

// Addressed returns, per room, the messages naming `tokens` that `session` has
// not been alerted about yet.
//
// Rows the session sent ITSELF are dropped. That filter is not cosmetic: a
// session announces its own claim slug in the room, the announcement is
// chunked by the wire into rows only the first of which carries the "[label] "
// attribution prefix, and the slug is the one token that matches anything. So
// without it the first alert every session ever receives is about its own
// post. The discriminator is the outbox: measured against the live journal,
// every echoed chunk of a 3566-byte send was a literal substring of the @sent
// row that recorded it (13 of 13). Containment can only ever suppress an
// alert, never invent one, so its failure direction is a missed alert.
//
// When a room has nothing to report, its cursor is advanced HERE — there is
// nothing that a lost reply could lose, and leaving it parked would make every
// later call rescan the same history. A room WITH something to report is left
// alone for the caller to ack after it has actually delivered the alert.
func (j *Journal) Addressed(session, label string, tokens []string, limit int) ([]RoomAlert, error) {
	if session == "" {
		return nil, errNoSession
	}
	clause, margs, err := mentionClause(tokens)
	if err != nil {
		return nil, err
	}
	if clause == "" {
		return nil, nil // no names to answer to: nothing can be addressed
	}
	if limit <= 0 || limit > alertRowCap {
		limit = alertRowCap
	}

	// The room list carries each room's newest seq, and the scan below is
	// bounded by it. Taking the bound FIRST and holding the scan inside it is
	// what makes "advance to newest" provable: a row appended between the two
	// queries is outside the window, so it is not skipped, it is simply next
	// time's work.
	rooms, err := j.alertRoomTops()
	if err != nil {
		return nil, err
	}
	// One read of this session's whole cursor set, not one per room: see
	// alertCursors. A room with no row reads back as 0, which is the same
	// "never alerted" AlertCursor reports for a missing row.
	cursors, err := j.alertCursors(session)
	if err != nil {
		return nil, err
	}

	own, err := j.ownSends(label)
	if err != nil {
		return nil, err
	}

	var out []RoomAlert
	for _, r := range rooms {
		cur := cursors[r.name]
		if cur >= r.newest {
			continue
		}
		// limit+1 distinguishes "this is all of it" from "the cap cut it".
		args := append([]any{r.name, cur, r.newest}, margs...)
		args = append(args, limit+1)
		mrows, err := j.db.Query(`SELECT seq, room, sender, kind, body, at FROM messages
			WHERE room=? AND seq>? AND seq<=? AND kind IN ('chat','im') AND `+clause+`
			ORDER BY seq LIMIT ?`, args...)
		if err != nil {
			return nil, err
		}
		var msgs []Msg
		for mrows.Next() {
			var m Msg
			if err := mrows.Scan(&m.Seq, &m.Room, &m.Sender, &m.Kind, &m.Body, &m.At); err != nil {
				mrows.Close()
				return nil, err
			}
			msgs = append(msgs, m)
		}
		mrows.Close()
		if err := mrows.Err(); err != nil {
			return nil, err
		}

		a := RoomAlert{Room: r.name, Advance: r.newest}
		if len(msgs) > limit {
			msgs = msgs[:limit]
			a.More = true
			// The scan stopped at the last row it took, so that row — not the
			// room's newest — is the end of the covered window.
			a.Advance = msgs[len(msgs)-1].Seq
		}
		for _, m := range msgs {
			if !own.authored(m.Body) {
				a.Msgs = append(a.Msgs, m)
			}
		}
		if len(a.Msgs) == 0 {
			// Nothing to deliver, so nothing to lose: bank the scan.
			if _, err := j.SetAlertCursor(session, r.name, a.Advance); err != nil {
				return nil, err
			}
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

// roomTop is one room and the newest seq in it that could possibly alert.
type roomTop struct {
	name   string
	newest int64
}

// alertRoomTopsQuery lists every room holding at least one chat/im row,
// ordered by room, with that room's newest such seq (NULL when it holds none).
//
// The obvious spelling of this is one aggregate, and it is what shipped:
//
//	SELECT room, MAX(seq) FROM messages
//	 WHERE room<>'@sent' AND room<>'' AND kind IN ('chat','im')
//	 GROUP BY room ORDER BY room
//
// That is LINEAR IN THE WHOLE JOURNAL. Measured plan, 60k-row synthetic
// journal over 8 rooms:
//
//	SCAN messages USING INDEX messages_room_seq
//
// The index hands over the grouping for free, but `kind` is not in it, so each
// of the 60k index entries pays a row lookup and the scan is not covering.
// This runs on the PostToolUse hook, once per tool call, in a freshly spawned
// process — so what the aggregate costs is what RETENTION costs. Today's real
// journal is ~553 rows, where none of this is measurable; the number below is
// how the cost GROWS, not a speedup anybody would feel today.
//
// So the rooms are walked by SEEK. A loose index scan (the "MIN(room) WHERE
// room > the previous one" idiom) enumerates the distinct rooms in R+1
// covering seeks, and each room's newest message is one more seek into that
// room's own slice of the same index. Measured plan on the same 60k rows —
// EVERY line EXPLAIN QUERY PLAN emits, indented by its parent link, because an
// abridged quote of a plan sends the next reader looking for the lines it
// dropped:
//
//	CO-ROUTINE rooms
//	  SETUP
//	    SEARCH messages USING COVERING INDEX messages_room_seq
//	  RECURSIVE STEP
//	    SCAN rooms
//	    CORRELATED SCALAR SUBQUERY 2
//	      SEARCH messages USING COVERING INDEX messages_room_seq (room>?)
//	SCAN rooms
//	CORRELATED SCALAR SUBQUERY 4
//	  SEARCH m USING INDEX messages_room_seq (room=?)
//	USE TEMP B-TREE FOR ORDER BY
//
// The two `SCAN rooms` are the CTE being consumed — once by the recursive step,
// once by the outer SELECT — and the temp b-tree sorts that CTE's output for the
// final `ORDER BY room`. Both are over R rows, never over the journal, which is
// the whole point of the rewrite: no line of this plan grows with retention.
//
// Measured on the same 60k rows: the aggregate 27.8-30.7 ms/call, this walk
// 0.041-0.047 ms. The whole Addressed call was 27.6 ms before and 0.078 ms
// after, in the steady state where every cursor is parked at its room's newest
// — which is the state every tool call finds. So the aggregate WAS the call.
//
// The exclusions live INSIDE the MIN() subqueries, so @sent and "" never become
// states of the walk and cost neither a seek of their own nor a MAX() subquery.
// That placement is cost, NOT correctness — and the two mutations that probe it
// are LINKED, not independent. Neither alone fails a single test in this package
// (both watched):
//
//   - Moving both exclusions out to the final filter returns the same set: what
//     advances the walk is room>previous, so the outbox becomes one more state
//     the final WHERE then drops.
//   - Making an exclusion the recursion GUARD (`FROM rooms WHERE rooms.room IS
//     NOT NULL AND rooms.room<>?`) is a NO-OP against this placement, and that is
//     the placement's doing: with the exclusion inside the MIN(), @sent is never
//     a state of the walk, so a guard testing for it can never fire.
//
// It is the PAIR that loses rooms. Exclusions in the final filter put @sent back
// into the walk; the guard then ends the walk AT it and silently drops every room
// sorting after — watched, on the test's outbox-straddling fixture: seeks
// [{!alpha 4} {@dm 3}] against aggr [{!alpha 4} {@dm 3} {zulu 1}]. That
// combination is what TestAlertRoomTopsMatchTheAggregateItReplaced kills.
//
// Note what that means for a later edit: there is NO defence in depth here. The
// exclusions appear only in the MIN() subqueries and are not repeated in the
// final `WHERE room IS NOT NULL`, so moving them "somewhere equivalent" is one
// step away from a walk that ends at the outbox. The test is the only guard.
//
// REFUTED alternative: have the caller pass in the rooms it serves (the
// daemon's configured room list, plus the DM pseudo-room). Cut, because the
// reported set must not depend on the config: @dm is not a configured room, and
// a room dropped from the config still holds an unacked backlog somebody is
// owed. Deriving the set from the same table with the same predicates makes
// "exactly the rooms the aggregate listed" true by construction instead of by
// review — and TestAlertRoomTopsMatchTheAggregateItReplaced pins it there.
//
// What this trades is linear-in-ROWS for linear-in-DISTINCT-ROOMS, which is the
// right way round while rooms are the handful the daemon joins plus @dm. A
// journal that grew a room per peer would want a rooms table instead.
const alertRoomTopsQuery = `WITH RECURSIVE rooms(room) AS (
	SELECT MIN(room) FROM messages WHERE room<>'' AND room<>?
	UNION ALL
	SELECT (SELECT MIN(room) FROM messages WHERE room>rooms.room AND room<>'' AND room<>?)
		FROM rooms WHERE rooms.room IS NOT NULL
)
SELECT room, (SELECT MAX(seq) FROM messages m WHERE m.room=rooms.room AND m.kind IN ('chat','im'))
FROM rooms WHERE room IS NOT NULL ORDER BY room`

func (j *Journal) alertRoomTops() ([]roomTop, error) {
	rows, err := j.db.Query(alertRoomTopsQuery, sentRoom, sentRoom)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []roomTop
	for rows.Next() {
		var name string
		var newest sql.NullInt64
		if err := rows.Scan(&name, &newest); err != nil {
			return nil, err
		}
		if !newest.Valid {
			// A room holding only presence/system rows has no message anybody
			// addressed. The aggregate did not list it either — GROUP BY over a
			// kind-filtered WHERE produces no group at all — so dropping it here
			// is what keeps the two sets identical.
			continue
		}
		out = append(out, roomTop{name: name, newest: newest.Int64})
	}
	return out, rows.Err()
}

// alertCursors reads this session's ENTIRE alert cursor set in one query.
//
// One AlertCursor call per room is R round trips on a path that runs on every
// tool call, and they all answer from the same index: (session, room) is the
// table's primary key, so its automatic index answers the whole set from a
// single seek at the session prefix. Measured plan:
//
//	SEARCH alert_cursor USING INDEX sqlite_autoindex_alert_cursor_1 (session=?)
//
// A room absent from the map reads back as 0, which is exactly what
// AlertCursor returns for sql.ErrNoRows: "never alerted, start at the
// horizon". The empty-session refusal is kept here rather than left to the
// per-room helper, because that helper is no longer on this path and a cursor
// keyed to "" is one every unidentified session would share.
func (j *Journal) alertCursors(session string) (map[string]int64, error) {
	if session == "" {
		return nil, errNoSession
	}
	rows, err := j.db.Query(`SELECT room, seq FROM `+alertCursorTable+` WHERE session=?`, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var room string
		var seq int64
		if err := rows.Scan(&room, &seq); err != nil {
			return nil, err
		}
		out[room] = seq
	}
	return out, rows.Err()
}

// ownSends is a session's recent outbox, used to recognize the echoes of its
// own messages.
type ownSends []string

func (o ownSends) authored(body string) bool {
	if body == "" {
		return false
	}
	for _, sent := range o {
		if strings.Contains(sent, body) {
			return true
		}
	}
	return false
}

func (j *Journal) ownSends(label string) (ownSends, error) {
	if label == "" {
		// No label means no way to tell this session's sends from anyone
		// else's. Filtering nothing over-alerts; guessing would under-alert
		// on somebody else's message, which is the failure that matters.
		return nil, nil
	}
	rows, err := j.db.Query(`SELECT body FROM messages WHERE room=? AND sender=?
		ORDER BY seq DESC LIMIT ?`, sentRoom, label, ownSendWindow)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out ownSends
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// AlertDeps is what the proactive alert needs: a daemon socket and the names
// this session answers to. Slugs come from the claims ledger; see
// cli.ChatIdentity for why they are the ones that matter.
type AlertDeps struct {
	Call      func(Request, time.Duration) (Response, error)
	SessionID string
	Label     string
	Slugs     []string
}

// alertCallTime bounds the socket round-trip. This runs on EVERY tool call, so
// the bound is the whole cost a wedged chat daemon can impose on a session:
// short, because the alert is a courtesy and the session's work is not.
const alertCallTime = 750 * time.Millisecond

// RunAlert asks the daemon what has named this session since it was last
// told, hands the rendering to emit, and only then acks.
//
// The ordering is the inbox drain's, for the inbox drain's reason: a cursor
// advanced before a successful delivery turns a lost write into a permanently
// lost alert. emit is called at most once and only when there is something to
// say — silence is the common case and must cost nothing.
func RunAlert(deps AlertDeps, emit func(text string) error) error {
	if deps.SessionID == "" && deps.Label == "" && len(deps.Slugs) == 0 {
		return nil // no identity resolved: nothing can be addressed to us
	}
	tokens, err := mentionSet(nil, sessionNames(deps.SessionID, deps.Label, deps.Slugs))
	if err != nil {
		return err
	}
	if len(tokens) == 0 {
		return nil
	}
	// Slugs ride along for presence: this call already carries the identity
	// the daemon needs to show the session as a buddy in its room, and the
	// ledger read that produced them has already happened here. It adds no
	// round-trip and the daemon does no I/O with them.
	resp, err := deps.Call(Request{Op: "alerts", Session: deps.SessionID, Label: deps.Label,
		Slugs: deps.Slugs, Mentions: tokens, Limit: alertRowCap}, alertCallTime)
	if err != nil {
		return err // Call already turns a !OK reply into this error
	}
	if len(resp.Alerts) == 0 {
		return nil
	}
	if err := emit(renderAlert(resp.Alerts, tokens)); err != nil {
		return err
	}
	var firstErr error
	for _, a := range resp.Alerts {
		r, err := deps.Call(Request{Op: "alertack", Session: deps.SessionID,
			Room: a.Room, Seq: a.Advance}, alertCallTime)
		if err == nil && !r.OK {
			err = errors.New(r.Error)
		}
		if err != nil && firstErr == nil {
			// A failed ack re-alerts next tool call. That is the direction to
			// fail in, and it is reported rather than swallowed so a
			// permanently unackable cursor does not look like a chatty room.
			firstErr = fmt.Errorf("alert cursor for room %q NOT advanced: %w", Fence(a.Room, 64), err)
		}
	}
	return firstErr
}

// renderAlert writes the notice. It prints seqs and counts and NO message
// body: the alert's job is to say "go look", and the deliberate read has the
// byte budget and the untrusted-content framing that an auto-injected body
// would bypass. Room names and tokens are fenced anyway — a slug is free text
// out of the ledger, and this text lands in an agent's context.
func renderAlert(alerts []RoomAlert, tokens []string) string {
	quoted := make([]string, 0, len(tokens))
	for _, t := range tokens {
		quoted = append(quoted, strconv.Quote(Fence(t, maxMentionBytes)))
	}
	names := strings.Join(quoted, ",")

	var b strings.Builder
	b.WriteString("BUDDY CHAT ALERT — room messages name you. Counts and seqs only; no chat text is injected here.\n")
	for _, a := range alerts {
		seqs := make([]string, 0, len(a.Msgs))
		for _, m := range a.Msgs {
			seqs = append(seqs, strconv.FormatInt(m.Seq, 10))
		}
		more := ""
		if a.More {
			more = " (a FLOOR — the scan hit its cap, more may follow)"
		}
		fmt.Fprintf(&b, "  %s: %d message(s) — seq %s%s\n",
			Fence(a.Room, 64), len(a.Msgs), strings.Join(seqs, ", "), more)
		fmt.Fprintf(&b, "    read them: chat_read room=%s after=%d mentions=[%s]\n",
			strconv.Quote(Fence(a.Room, 64)), a.Msgs[0].Seq-1, names)
	}
	return b.String()
}
