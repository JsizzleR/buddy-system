package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// The wait register (D-033, issue #26).
//
// THE FAILURE. A session in a multi-session run parks itself waiting for
// something another session is doing — a claim to be released, a serialized
// hour-long test tier, a review slot — and while it waits it runs no turn. The
// harness writes every prompt-cache entry on the 1h tier (D-020), so a session
// that waits past the hour comes back to a cold cache and its next request
// re-writes the whole prefix at twice the base input rate. Measured over 14
// days of this box's transcripts: 187 requests after a gap over an hour
// re-wrote 54M tokens, 108 of them under four hours. And a parked session runs
// no tool, so nothing drains its inbox either (field notes §5c).
//
// WHAT THIS IS. A DECLARED wait: which session, on which claims, since when,
// until when, and what it means to do when they land. The session enters it
// through the CLI, so it is authoritative in the only sense this ledger has
// (invariant 4); it is never inferred — a refused claim, an idle report and
// silence are not waits (D-016). The keep-alive itself is the session's OWN
// scheduled prompt running `buddy wait check`; nothing in buddy wakes,
// schedules or types into anything.
//
// WHAT THIS IS NOT. It reserves nothing and refuses nothing (invariant 10
// word for word): no gate reads it, no claim consults it, and a waiter has no
// more standing on the scope it waits for than a session that never said so.
//
// TARGETS ARE CLAIM IDS, resolved ONCE at declaration (D-013: resolved before
// it is stored, refused when it names nothing). A released slug deliberately
// stops resolving in the target namespace, and the same slug may be taken
// again by anybody; judging "landed" on the slug would change the answer as
// history grew. A claim id is closed forever once closed — no path sets a
// released or orphaned row back to open — so a wait on it lands exactly once.
//
// NO STORED VERDICT (D-020's rule). LANDED and EXPIRED are functions of the
// clock and of the claims table, computed by Verdict at read time, and every
// surface renders that one function. What IS written is what an act did:
// `cleared`/`reason` when a check, a clear or a cleanup closed the row, and
// `told`, the one-shot mark beat sets after it announced a landing.
//
// THE DECLARATION HAS ITS OWN IDENTITY (`decl`, a random token), not its
// `since`. Codex design pass (P1): `since` is whole seconds, so a beat that
// composed a LANDED notice about one declaration and then marked "the row
// declared at second S" would mark a REPLACEMENT declared in that same second
// — whose own landing would then never be announced. The targets are keyed to
// the declaration for the same reason: a row and its targets are one
// declaration, and a reader joining them on `decl` cannot pair one
// declaration's deadline with another's claims.

// WaitCeiling is the longest wait a session may declare. The cost model's
// break-even on Opus is about sixteen hours of 50-minute reads against one
// re-write, and the over-12h gap bucket measured a net LOSS; no single tier
// or review slot in the field notes came near it. Nobody should wait through
// a night on a timer.
const WaitCeiling = 12 * time.Hour

// WaitFloor is the shortest: the harness's scheduler has a one-minute floor,
// so a wait that ends sooner than any check can come is a typo.
const WaitFloor = time.Minute

// WaitTarget is one awaited claim, joined to its current row.
type WaitTarget struct {
	ClaimID string
	Slug    string // as declared: display only, never matched on
	// State is the claim's state now: "open", "released", "orphaned", or ""
	// when the row is gone (sweep deletes closed rows past its ttl).
	State string
	// Closed is when a closed claim closed: release and orphaning both stamp
	// `renewed`. Zero while open or when the row is gone.
	Closed time.Time
	// Holder is the claim's owner as the ledger has it now. Zero-valued when
	// the claim row is gone. Label is peer text: the caller fences it.
	Holder SessionInfo
	// Reopened names a DIFFERENT claim open now under the same slug, when
	// there is one: the awaited claim closed and somebody took the slug
	// again — its holder resuming under a new incarnation is the common way
	// (Fable design pass). The wait still LANDED, because what was awaited
	// was that claim; a verdict that stayed silent about the new one would
	// send the waiter into work that is visibly still in progress.
	// ReopenedBy is peer text: the caller fences it.
	Reopened   bool
	ReopenedBy string
	ReopenedAt time.Time
	// Outcome is what the holder reported when it released the claim
	// (D-049): "pass", "fail", "aborted", or "" when it reported nothing or
	// has not released. OutcomeNote is peer text: the caller fences it.
	Outcome     string
	OutcomeNote string
}

// IsOpen reports whether the awaited claim is still open. A row that is gone
// is closed: sweep deletes only released and orphaned rows.
func (t WaitTarget) IsOpen() bool { return t.State == "open" }

// Wait is one session's declared wait, with its targets.
type Wait struct {
	SessionID   string
	Incarnation string // the declarer; a row from another incarnation is not this session's wait
	Decl        string // the declaration's own identity (see the header)
	Since       time.Time
	Deadline    time.Time
	Note        string // the declarer's own reminder; peer text to every other reader
	LastCheck   time.Time
	Checks      int
	Told        bool      // beat has announced this declaration's landing
	Cleared     time.Time // zero while open
	Reason      string    // "landed", "expired", "cleared", "ended" once cleared
	ReadySHA    string    // D-049: the commit the waiter declared its work ready at, or ""
	Targets     []WaitTarget
}

// IsOpen reports whether nothing has closed the row yet. It says nothing
// about whether the wait is OVER — Verdict does.
func (w Wait) IsOpen() bool { return w.Cleared.IsZero() }

// WaitVerdict is the one computation every surface renders.
type WaitVerdict int

const (
	// WaitPending: some awaited claim is still open (or there are none) and
	// the deadline has not passed.
	WaitPending WaitVerdict = iota
	// WaitLanded: there is at least one target and every one is closed.
	WaitLanded
	// WaitExpired: the deadline has passed and it did not land.
	WaitExpired
)

// Landed reports whether every awaited claim is closed. A wait with no
// target is a pure timer: it NEVER lands, it expires — it exists so a session
// parked on something outside the ledger (an external test tier) keeps its
// cache and its inbox without inventing a claim to wait on.
func (w Wait) Landed() bool {
	if len(w.Targets) == 0 {
		return false
	}
	for _, t := range w.Targets {
		if t.IsOpen() {
			return false
		}
	}
	return true
}

// Verdict is LANDED if every target closed, else EXPIRED at or after the
// deadline, else PENDING. LANDED WINS over EXPIRED: the thing waited for
// happened, and telling a session to "ask before waiting longer" about work
// that already landed would send it the wrong way. Computed, never stored.
func (w Wait) Verdict(now time.Time) WaitVerdict {
	switch {
	case w.Landed():
		return WaitLanded
	case !now.Before(w.Deadline):
		return WaitExpired
	default:
		return WaitPending
	}
}

// ErrWaitRefused is a declaration refused before anything was written. Slug
// is the target at fault ("" for a refusal about the wait as a whole). The
// message names a remedy; Slug is peer text and the caller fences the error.
type ErrWaitRefused struct {
	Slug string
	Why  string
}

func (e ErrWaitRefused) Error() string {
	if e.Slug == "" {
		return e.Why
	}
	return fmt.Sprintf("--on %q: %s", e.Slug, e.Why)
}

// DeclareWait records that the session waits on the named claims for `until`,
// REPLACING any wait it had (the replaced one, if it was still open, comes
// back so the caller can say what it replaced). One immediate transaction:
// every target resolves or nothing is written, and an existing wait survives
// a refused re-declaration untouched.
//
// Refused: a session that is not live under this incarnation; `until` outside
// [WaitFloor, WaitCeiling]; a slug that names no OPEN claim (saying whether it
// closed or never existed, because "already released" is the answer the
// caller was waiting for and "no such claim" is a typo); and the caller's own
// claim — waiting on yourself is a timer with a confusing name, and nothing
// but the caller could ever land it.
//
// A claim whose holder has ENDED is refused too, saying so: nothing is going
// to release it, and a plain `buddy claim` of its scopes displaces it
// (D-026). The declaration does NOT orphan it first, as `claim` and `wait
// check` do — a refused declaration rolls its transaction back, and the first
// shape explained the refusal with an orphaning that never committed ("already
// orphaned (0s ago)" over a claim still open; Codex code pass). Duplicate
// slugs collapse to one target.
func (s *Store) DeclareWait(sessionID, incarnation string, slugs []string, until time.Duration, note string) (Wait, *Wait, error) {
	return s.DeclareWaitReady(sessionID, incarnation, slugs, until, note, "")
}

// DeclareWaitReady is DeclareWait that also records readySHA, the commit the
// waiter declares its work ready at (D-049): a rider on a batched run saying
// "count my work in, it is at this commit". It is the waiter's declaration and
// nothing checks it against any tree — the integrator rebases every rider, so
// an ancestry test would fail every correctly integrated item (Codex and Fable
// design passes). A timer has nobody to hand readiness to, so it is refused.
func (s *Store) DeclareWaitReady(sessionID, incarnation string, slugs []string, until time.Duration, note, readySHA string) (Wait, *Wait, error) {
	if readySHA != "" && len(slugs) == 0 {
		return Wait{}, nil, ErrWaitRefused{Why: "--ready says whose run your work is ready for, so it needs --on <slug>: a timer has nobody to hand it to; nothing was recorded"}
	}
	if until < WaitFloor {
		return Wait{}, nil, ErrWaitRefused{Why: fmt.Sprintf("--until %s is under the %s floor: the harness schedules nothing sooner than a minute", until, WaitFloor)}
	}
	if until > WaitCeiling {
		return Wait{}, nil, ErrWaitRefused{Why: fmt.Sprintf("--until %s is over the %s ceiling: past about sixteen hours a keep-alive has spent more in reads than the re-write it prevents, and nobody should wait through a night on a timer — wait less, and ask before waiting again",
			until, WaitCeiling)}
	}
	var out Wait
	var replaced *Wait
	err := s.tx(func(tx *sql.Tx) error {
		now := s.now()
		if err := liveIncarnation(tx, sessionID, incarnation); err != nil {
			return err
		}
		type target struct{ claimID, slug string }
		var targets []target
		seen := map[string]bool{}
		for _, slug := range slugs {
			if seen[slug] {
				continue
			}
			seen[slug] = true
			claimID, owner, taken, err := openSlugOwner(tx, slug)
			if err != nil {
				return err
			}
			if !taken {
				return whyNoOpenClaim(tx, slug, now)
			}
			if owner == sessionID {
				return ErrWaitRefused{Slug: slug, Why: "that is your own claim — nothing but you can land it; a wait is for another session's work (with no --on it is a plain timer)"}
			}
			var label string
			var ended sql.NullInt64
			if err := tx.QueryRow(`SELECT label, ended FROM sessions WHERE session_id=?`, owner).Scan(&label, &ended); err != nil {
				return err
			}
			if ended.Valid {
				return ErrWaitRefused{Slug: slug, Why: fmt.Sprintf("its holder %s said bye %s ago, so nothing is going to release it — a plain `buddy claim` of its scopes displaces it (D-026); nothing was recorded",
					label, humanAge(now.Sub(time.Unix(ended.Int64, 0))))}
			}
			targets = append(targets, target{claimID, slug})
		}
		prev, err := waitsWhere(tx, `WHERE w.session_id=?`, sessionID)
		if err != nil {
			return err
		}
		if len(prev) == 1 && prev[0].IsOpen() && prev[0].Incarnation == incarnation {
			p := prev[0]
			replaced = &p
		}
		// One row per session and its targets with it: the old declaration's
		// targets go whatever state it was in, because nothing reads a
		// declaration that is no longer the session's.
		if _, err := tx.Exec(`DELETE FROM session_wait_targets WHERE session_id=?`, sessionID); err != nil {
			return err
		}
		decl := newToken()
		if _, err := tx.Exec(`INSERT INTO session_waits (session_id, incarnation, decl, since, deadline, note, last_check, checks, told, cleared, reason, ready_sha)
			VALUES (?,?,?,?,?,?,0,0,0,NULL,NULL,?)
			ON CONFLICT(session_id) DO UPDATE SET incarnation=excluded.incarnation, decl=excluded.decl, since=excluded.since,
				deadline=excluded.deadline, note=excluded.note, last_check=0, checks=0, told=0, cleared=NULL, reason=NULL,
				ready_sha=excluded.ready_sha`,
			sessionID, incarnation, decl, now.Unix(), now.Add(until).Unix(), note, readySHA); err != nil {
			return err
		}
		for i, t := range targets {
			if _, err := tx.Exec(`INSERT INTO session_wait_targets (decl, session_id, incarnation, ord, claim_id, slug) VALUES (?,?,?,?,?,?)`,
				decl, sessionID, incarnation, i, t.claimID, t.slug); err != nil {
				return err
			}
		}
		got, err := waitsWhere(tx, `WHERE w.session_id=?`, sessionID)
		if err != nil {
			return err
		}
		if len(got) != 1 {
			return fmt.Errorf("wait for %s did not read back after it was written", sessionID)
		}
		out = got[0]
		return nil
	})
	if err != nil {
		return Wait{}, nil, err
	}
	return out, replaced, nil
}

// whyNoOpenClaim explains a slug that names no open claim, inside the
// declaration's transaction so the answer cannot race it.
func whyNoOpenClaim(tx *sql.Tx, slug string, now time.Time) error {
	var state string
	var renewed int64
	err := tx.QueryRow(`SELECT state, renewed FROM claims WHERE slug=? ORDER BY renewed DESC LIMIT 1`, slug).Scan(&state, &renewed)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrWaitRefused{Slug: slug, Why: "no claim by that slug in this ledger (`buddy ls` lists the open ones); nothing was recorded"}
	}
	if err != nil {
		return err
	}
	return ErrWaitRefused{Slug: slug, Why: fmt.Sprintf("that claim is already %s (%s ago) — it has landed, so there is nothing to wait for; nothing was recorded",
		state, humanAge(now.Sub(time.Unix(renewed, 0))))}
}

// liveIncarnation refuses a session that is not live under exactly this
// incarnation. Every WRITE to a wait goes through it, the same fence Claim
// has: a `wait check` from an incarnation that has since said bye must not
// read its old row as STILL WAITING and bump its counters (Codex design pass).
func liveIncarnation(tx *sql.Tx, sessionID, incarnation string) error {
	var live int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE session_id=? AND incarnation=? AND ended IS NULL`,
		sessionID, incarnation).Scan(&live); err != nil {
		return err
	}
	if live == 0 {
		return fmt.Errorf("session %s (incarnation %s) is not live; run buddy hello first", sessionID, incarnation)
	}
	return nil
}

// WaitCheckResult is what one `wait check` found.
type WaitCheckResult struct {
	// Found is true when the session had an OPEN wait of this incarnation.
	// Wait is then that wait as evaluated, with LastCheck/Checks as this
	// check recorded them; otherwise Wait is the session's last row, cleared
	// or from another incarnation, or the zero Wait when it never declared one.
	Found   bool
	Wait    Wait
	Verdict WaitVerdict
}

// WaitCheck evaluates the caller's open wait against the clock and the claims
// table and records the check, in one immediate transaction. It does NOT
// close a wait that has landed or expired: the caller closes it with
// CloseWait AFTER the verdict has been written, the rule every notice here
// follows (D-028). The first shape closed it here, and a check whose output
// then failed to arrive lost its LANDED for good — beat reads only open rows,
// so nothing would ever say it again (Codex code pass). The verdict cannot
// un-happen in between: a closed claim never reopens and the clock does not
// run backwards. A check of a session with no open wait writes nothing and
// reports the last row, so NO WAIT can say what became of it.
func (s *Store) WaitCheck(sessionID, incarnation string) (WaitCheckResult, error) {
	var r WaitCheckResult
	err := s.tx(func(tx *sql.Tx) error {
		now := s.now()
		if err := liveIncarnation(tx, sessionID, incarnation); err != nil {
			return err
		}
		if err := cleanupEnded(tx, now.Unix()); err != nil {
			return err
		}
		got, err := waitsWhere(tx, `WHERE w.session_id=?`, sessionID)
		if err != nil {
			return err
		}
		if len(got) == 0 {
			return nil
		}
		w := got[0]
		if !w.IsOpen() || w.Incarnation != incarnation {
			r.Wait = w
			return nil
		}
		r.Found = true
		r.Verdict = w.Verdict(now)
		w.LastCheck, w.Checks = now, w.Checks+1
		r.Wait = w
		_, err = tx.Exec(`UPDATE session_waits SET last_check=?, checks=checks+1 WHERE session_id=? AND decl=?`,
			now.Unix(), sessionID, w.Decl)
		return err
	})
	return r, err
}

// CloseWait closes one declaration with the verdict a check has just WRITTEN
// ("landed" or "expired"). Keyed by the declaration, so a close that arrives
// after a re-declaration touches nothing; a row already closed stays as it was.
// It reports whether it closed anything.
func (s *Store) CloseWait(sessionID, decl, reason string) (bool, error) {
	if reason != "landed" && reason != "expired" {
		return false, fmt.Errorf("a check closes a wait as landed or expired, not %q", reason)
	}
	res, err := s.db.Exec(`UPDATE session_waits SET cleared=?, reason=? WHERE session_id=? AND decl=? AND cleared IS NULL`,
		s.now().Unix(), reason, sessionID, decl)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ClearWait withdraws the caller's open wait (reason "cleared") and returns
// it; nil when there was none to clear, which is not an error.
func (s *Store) ClearWait(sessionID, incarnation string) (*Wait, error) {
	var out *Wait
	err := s.tx(func(tx *sql.Tx) error {
		now := s.now()
		if err := liveIncarnation(tx, sessionID, incarnation); err != nil {
			return err
		}
		got, err := waitsWhere(tx, `WHERE w.session_id=? AND w.incarnation=? AND w.cleared IS NULL`, sessionID, incarnation)
		if err != nil || len(got) == 0 {
			return err
		}
		w := got[0]
		if _, err := tx.Exec(`UPDATE session_waits SET cleared=?, reason='cleared' WHERE session_id=? AND decl=?`,
			now.Unix(), sessionID, w.Decl); err != nil {
			return err
		}
		w.Cleared, w.Reason = now, "cleared"
		out = &w
		return nil
	})
	return out, err
}

// WaitOf returns the session's wait row — open or cleared, of any
// incarnation — with its targets, or false when it never declared one. The
// caller compares Incarnation and IsOpen: hello needs a predecessor's cleared
// row, and every other surface needs only the current incarnation's open one.
func (s *Store) WaitOf(sessionID string) (Wait, bool, error) {
	got, err := waitsWhere(s.db, `WHERE w.session_id=?`, sessionID)
	if err != nil || len(got) == 0 {
		return Wait{}, false, err
	}
	return got[0], true, nil
}

// OpenWaits lists every open wait of a LIVE session's CURRENT incarnation,
// oldest first. The join is the read side of the incarnation rule, and the
// liveness predicate is IdleSessions' reason: a session that ended between a
// caller's two queries must not be printed as waiting off the earlier one.
func (s *Store) OpenWaits() ([]Wait, error) {
	return waitsWhere(s.db, `JOIN sessions ws ON ws.session_id=w.session_id AND ws.incarnation=w.incarnation
		WHERE w.cleared IS NULL AND ws.ended IS NULL`)
}

// MarkWaitTold records that beat announced this declaration's landing.
// Keyed by the declaration, never by the session alone or by `since` (see the
// header), and called only after the write that carried the notice succeeded
// — at-least-once, like every beat-borne notice (D-028).
func (s *Store) MarkWaitTold(sessionID, decl string) error {
	_, err := s.db.Exec(`UPDATE session_waits SET told=1 WHERE session_id=? AND decl=? AND cleared IS NULL`, sessionID, decl)
	return err
}

// clearEndedWaits closes, as "ended", every open wait whose session has
// positively ended or whose declaring incarnation is no longer the session's.
// The second arm is what closes a predecessor's wait on REVIVAL: hello clears
// `ended` before its housekeeping runs, so the first arm cannot see that row,
// and an explicit clear in the revival branch was the same rule written
// twice (removing it was a mutation that survived — D-033).
// It runs wherever orphanEnded runs (hello, claim, sweep): cleanup of a
// positively-ended session happens there and nowhere else (invariant 12's
// placement), and a wait of a live session that merely went quiet is never
// closed early — it expires by its own deadline, computed at read time
// (invariant 11: staleness marks, it never reaps).
func clearEndedWaits(tx *sql.Tx, now int64) error {
	_, err := tx.Exec(`UPDATE session_waits SET cleared=?, reason='ended' WHERE cleared IS NULL AND session_id IN
		(SELECT s.session_id FROM sessions s WHERE s.session_id=session_waits.session_id
			AND (s.ended IS NOT NULL OR s.incarnation<>session_waits.incarnation))`, now)
	return err
}

// cleanupEnded is the housekeeping a check runs first: claims of
// positively-ended holders orphaned (orphanEnded), and waits of ended
// sessions closed (clearEndedWaits).
//
// WHY A CHECK ORPHANS AT ALL. D-026 made `claim` orphan ended holders first
// so that a session which has said bye cannot block a peer; a wait on such a
// holder had the same shape one step removed. Without this, a holder's bye
// at 10:00 with no hello, claim or sweep after it left the waiter printing
// STILL WAITING at every check and then EXPIRED "with the claim still open"
// at its deadline — while the ledger had known since 10:00 (Fable design
// pass). A check is already an immediate write transaction; the statement is
// the same idempotent one hello, claim and sweep run, keyed to rows still
// ended when it runs, so it adds no new way for a scope to be freed —
// only one more of the places D-026 already frees it from. `bye` itself
// still touches nothing (invariant 12).
func cleanupEnded(tx *sql.Tx, now int64) error {
	if _, err := orphanEnded(tx, now); err != nil {
		return err
	}
	return clearEndedWaits(tx, now)
}

// waitsWhere reads waits and their targets in ONE statement, so a reader
// never pairs one declaration's row with another's targets: the join is on
// `decl` — and on the session too, so that even two declarations drawing the
// same random token (2^-64) cannot share targets (Codex code pass) — and a
// single SELECT is a single snapshot. The claim and holder
// columns are LEFT JOINed because a claim row can be gone (sweep deletes
// closed rows past its ttl) and a target must survive that as "closed".
func waitsWhere(q querier, where string, args ...any) ([]Wait, error) {
	rows, err := q.Query(`SELECT w.session_id, w.incarnation, w.decl, w.since, w.deadline, w.note, w.last_check, w.checks, w.told,
			COALESCE(w.cleared,0), COALESCE(w.reason,''), w.ready_sha,
			COALESCE(t.claim_id,''), COALESCE(t.slug,''), COALESCE(c.state,''), COALESCE(c.renewed,0),
			COALESCE(c.outcome,''), COALESCE(c.outcome_note,''),
			COALESCE(h.session_id,''), COALESCE(h.incarnation,''), COALESCE(h.label,''), COALESCE(h.worktree,''),
			COALESCE(h.started,0), COALESCE(h.last_seen,0), COALESCE(h.ended,0),
			COALESCE(r.claim_id,''), COALESCE(rh.label,''), COALESCE(r.created,0)
		FROM session_waits w
		LEFT JOIN session_wait_targets t ON t.decl=w.decl AND t.session_id=w.session_id
		LEFT JOIN claims c ON c.claim_id=t.claim_id
		LEFT JOIN sessions h ON h.session_id=c.session_id
		LEFT JOIN claims r ON r.slug=t.slug AND r.state='open' AND r.claim_id<>t.claim_id
		LEFT JOIN sessions rh ON rh.session_id=r.session_id
		`+where+`
		ORDER BY w.since, w.session_id, t.ord`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Wait
	for rows.Next() {
		var w Wait
		var told int
		var t WaitTarget
		var renewed int64
		var reopenedID string
		if err := rows.Scan(&w.SessionID, &w.Incarnation, &w.Decl, unixScan{&w.Since}, unixScan{&w.Deadline}, &w.Note,
			unixScan{&w.LastCheck}, &w.Checks, &told, unixScan{&w.Cleared}, &w.Reason, &w.ReadySHA,
			&t.ClaimID, &t.Slug, &t.State, &renewed, &t.Outcome, &t.OutcomeNote,
			&t.Holder.SessionID, &t.Holder.Incarnation, &t.Holder.Label, &t.Holder.Worktree,
			unixScan{&t.Holder.Started}, unixScan{&t.Holder.LastSeen}, unixScan{&t.Holder.Ended},
			&reopenedID, &t.ReopenedBy, unixScan{&t.ReopenedAt}); err != nil {
			return nil, err
		}
		w.Told = told != 0
		t.Reopened = reopenedID != ""
		if t.State != "" && t.State != "open" && renewed != 0 {
			t.Closed = time.Unix(renewed, 0)
		}
		if n := len(out); n > 0 && out[n-1].SessionID == w.SessionID && out[n-1].Decl == w.Decl {
			if t.ClaimID != "" {
				out[n-1].Targets = append(out[n-1].Targets, t)
			}
			continue
		}
		if t.ClaimID != "" {
			w.Targets = []WaitTarget{t}
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
