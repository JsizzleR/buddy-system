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

// WaitCeiling is the longest wait a session may declare. The over-12h gap
// bucket measured a net LOSS; no single tier or review slot in the field
// notes came near it. Nobody should wait through a night on a timer. The
// break-even behind it was first put at sixteen hours on Opus, pricing a check
// as one read; a check is a turn of about three (D-054), which puts it near
// 5.5 h on Opus 5, 11 h on Opus 5.5 and 22 h on Fable 5.1. The ceiling stays
// where it is: it bounds a mistake, and whether the session is resumed at all
// is the question that decides a wait's worth.
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
	// HandedToWaiter: the claim is open and held by the waiter itself, under
	// the incarnation that declared the wait. Nothing but `release --to`
	// makes that true — DeclareWait refuses a wait on your own claim, and a
	// claim id changes hands only by transfer — so it means "a holder handed
	// the awaited claim to you" (D-093). Computed from the claims table at
	// read time like every other verdict input (D-020's rule): nothing is
	// written when it happens, and handing the claim on again un-does it.
	HandedToWaiter bool
}

// IsOpen reports whether the awaited claim is still open. A row that is gone
// is closed: sweep deletes only released and orphaned rows.
func (t WaitTarget) IsOpen() bool { return t.State == "open" }

// Pending reports whether the waiter is still waiting on this target: the
// claim is open and somebody ELSE holds it. A claim handed to the waiter is
// open and over (D-093): what the waiter waited for was the claim, and it has
// it.
func (t WaitTarget) Pending() bool { return t.IsOpen() && !t.HandedToWaiter }

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
	// MailAfter is a MAIL wait's watermark (D-208): the inbox's highest
	// msg_id when it was declared, 0 on an empty inbox; -1 for every other
	// wait. IsMail is the one test (never MailAfter > 0).
	MailAfter int64
	mail      bool
	// Mail is what has arrived for a mail wait, read in the same SELECT as
	// the row (one snapshot); zero for any other wait, or when nothing has.
	Mail MailArrival
}

// MailArrival is the first message that lands a mail wait (a QUEUED one
// first, so the line points at what is still unread, never at a message
// already drained while a later one waits: Fable cross-check), and how many do.
//
// WHAT COUNTS (D-208). A message to the waiter that is not adapter mail
// (D-076: its own register), not the waiter's own send (a lane's correction
// or broadcast never wakes itself), not a broadcast, and either QUEUED (to
// the waiter's id with no delivery row for it, whatever its id: mail that sat
// unread under the drain's bounds, a held Stop or a Codex harness whose hooks
// never drain must not be slept over) or ARRIVED (above the watermark, to the
// waiter's id or delivered to it, so a message a woken session already drained
// with `buddy inbox` still ends the wait instead of leaving it to its deadline).
//
// MONOTONE: what counts keeps counting. The queued arm was first "no delivery
// row", and a message queued before the declaration then LEFT it when drained:
// `who` said LANDED, the next check STILL WAITING (Codex code pass, GIVEN 36).
// So a message at or below the watermark is excluded only by a delivery
// recorded BEFORE the declaration (`delivered < since`), which a later drain
// cannot write. A delivery in the declaring second counts: a spurious landing
// on mail just read costs one short turn; a lost one, the deadline.
//
// NOT BROADCASTS. One `buddy msg all` from any shell with no session reads as
// the operator; landing on it would end every parked mail wait at once, a
// turn each at full context, every fifteen seconds it is repeated. Today that
// send wakes nobody (no wake clause is named for a broadcast), and it still
// reaches the session by the drains (hostile panel lens).
//
// TARGETS ARE SESSION IDS. Send stores the resolved id (inbox.go), so the
// direct arm is `target = waiter` and there is no label arm: a label is peer
// text a session can set to another's id, and a label that changed between a
// drain and a read would un-land a wait (two panel lenses). A legacy row
// addressed by label still counts once it is delivered.
type MailArrival struct {
	Found bool
	ID    int64
	More  int // how many more count besides ID
	// SenderKnown false: the message's sender_session was NULL (unresolved).
	// SenderSession "" with SenderKnown is a send from no session. Neither
	// is ever inbox.sender, a sender's own free text.
	SenderKnown   bool
	SenderSession string
	SenderLabel   string // the ledger's label for SenderSession; peer text: the caller fences it
	Created       time.Time
	Delivered     bool // the waiter has a delivery row for ID
}

// IsMail reports whether the wait waits on mail (D-208). A flag the scan
// sets, not MailAfter >= 0: the zero Wait would otherwise read as a mail wait
// (Fable cross-check).
func (w Wait) IsMail() bool { return w.mail }

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

// Landed reports whether every awaited claim is closed or was handed to the
// waiter (D-093). A wait with no
// target is a pure timer: it NEVER lands, it expires — it exists so a session
// parked on something outside the ledger (an external test tier) keeps its
// cache and its inbox without inventing a claim to wait on.
func (w Wait) Landed() bool {
	if w.IsMail() {
		return w.Mail.Found
	}
	if len(w.Targets) == 0 {
		return false
	}
	for _, t := range w.Targets {
		if t.Pending() {
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
	return s.declareWait(sessionID, incarnation, slugs, until, note, readySHA, false)
}

// DeclareMailWait declares a wait that lands when mail comes (D-208): no
// targets, the watermark read INSIDE the declaration's write transaction
// (sends serialize on that lock and AUTOINCREMENT never reuses an id, so every
// later message is above it). Refused, writing nothing, over the session's own
// open --ready wait: one row per session, so the mail wait would REPLACE the
// READY and drop the run's outcome (orc-3's call, 2026-11-17). Over its own
// open mail wait it KEEPS that watermark and its `since`: a message drained
// between the old block's last read and this declaration would otherwise land
// neither.
func (s *Store) DeclareMailWait(sessionID, incarnation string, until time.Duration, note string) (Wait, *Wait, error) {
	return s.declareWait(sessionID, incarnation, nil, until, note, "", true)
}

func (s *Store) declareWait(sessionID, incarnation string, slugs []string, until time.Duration, note, readySHA string, mail bool) (Wait, *Wait, error) {
	if mail && (len(slugs) > 0 || readySHA != "") {
		return Wait{}, nil, ErrWaitRefused{Why: "a mail wait names no claim: \"every claim closed\" and \"you have mail\" are two meanings of LANDED; nothing was recorded"}
	}
	if readySHA != "" && len(slugs) == 0 {
		return Wait{}, nil, ErrWaitRefused{Why: "--ready says whose run your work is ready for, so it needs --on <slug>: a timer has nobody to hand it to; nothing was recorded"}
	}
	if until < WaitFloor {
		return Wait{}, nil, ErrWaitRefused{Why: fmt.Sprintf("--until %s is under the %s floor: the harness schedules nothing sooner than a minute", until, WaitFloor)}
	}
	if until > WaitCeiling {
		return Wait{}, nil, ErrWaitRefused{Why: fmt.Sprintf("--until %s is over the %s ceiling: nobody should wait through a night on a timer (what a keep-alive costs per hour is in the USAGE guide) — wait less, and ask before waiting again",
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
		mailAfter, since := int64(-1), now.Unix()
		if mail {
			if replaced != nil && replaced.ReadySHA != "" {
				return ErrWaitRefused{Why: "you have an open --ready wait, and a mail wait would replace it and drop your READY and the run's outcome; `buddy wait clear` drops it deliberately; nothing was recorded"}
			}
			if replaced != nil && replaced.IsMail() {
				// The watermark AND `since`: mailCounts excludes a message at
				// or below the watermark only by a delivery before `since`,
				// so a fresh `since` over a kept watermark dropped one queued
				// before the old declaration and drained between the two
				// (Codex re-verify). `since` is otherwise read only for an
				// age and an order; the deadline is the new declaration's.
				mailAfter, since = replaced.MailAfter, replaced.Since.Unix()
			} else if err := tx.QueryRow(`SELECT COALESCE(MAX(msg_id), 0) FROM inbox`).Scan(&mailAfter); err != nil {
				return err
			}
		}
		// One row per session and its targets with it: the old declaration's
		// targets go whatever state it was in, because nothing reads a
		// declaration that is no longer the session's.
		if _, err := tx.Exec(`DELETE FROM session_wait_targets WHERE session_id=?`, sessionID); err != nil {
			return err
		}
		decl := newToken()
		// mail_after is SET on every declaration, -1 for a non-mail one: an
		// upsert leaves an unnamed column as it was, and a timer or claim wait
		// declared after a mail wait would inherit its watermark and land on
		// mail (migration panel lens).
		if _, err := tx.Exec(`INSERT INTO session_waits (session_id, incarnation, decl, since, deadline, note, last_check, checks, told, cleared, reason, ready_sha, mail_after)
			VALUES (?,?,?,?,?,?,0,0,0,NULL,NULL,?,?)
			ON CONFLICT(session_id) DO UPDATE SET incarnation=excluded.incarnation, decl=excluded.decl, since=excluded.since,
				deadline=excluded.deadline, note=excluded.note, last_check=0, checks=0, told=0, cleared=NULL, reason=NULL,
				ready_sha=excluded.ready_sha, mail_after=excluded.mail_after`,
			sessionID, incarnation, decl, since, now.Add(until).Unix(), note, readySHA, mailAfter); err != nil {
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
//
// A MAIL wait whose mail has come is closed "landed" instead (D-208), judged
// in this transaction: a session that read MAIL in `who` and cleared would
// otherwise get CLEARED from its block, two verdicts for one wait (GIVEN 36).
// The caller prints the verdict the returned Reason names. Claim waits keep
// the plain clear.
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
		reason := "cleared"
		if w.IsMail() && w.Landed() {
			reason = "landed"
		}
		if _, err := tx.Exec(`UPDATE session_waits SET cleared=?, reason=? WHERE session_id=? AND decl=?`,
			now.Unix(), reason, sessionID, w.Decl); err != nil {
			return err
		}
		w.Cleared, w.Reason = now, reason
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

// SettleEnded runs the housekeeping a check runs first (cleanupEnded) in its
// own short transaction, and nothing else. For `wait --block` (D-095), which
// reads without writing and so would otherwise sit on a claim whose holder has
// said bye until its deadline, the failure cleanupEnded's own header names;
// it calls this only when a read shows such a holder.
func (s *Store) SettleEnded() error {
	return s.tx(func(tx *sql.Tx) error { return cleanupEnded(tx, s.now().Unix()) })
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
// mailCounts is the one predicate for "message m lands mail wait w" (see
// MailArrival), written once and used for both the first arrival and the
// count, so the two cannot disagree. Every arm excludes adapter mail.
const mailCounts = `m.target<>'all'
	AND NOT EXISTS (SELECT 1 FROM adapter_messages a WHERE a.msg_id=m.msg_id)
	AND (m.sender_session IS NULL OR m.sender_session<>w.session_id)
	AND (m.target=w.session_id OR EXISTS
		(SELECT 1 FROM inbox_delivery d WHERE d.msg_id=m.msg_id AND d.session_id=w.session_id))
	AND (m.msg_id>w.mail_after OR NOT EXISTS
		(SELECT 1 FROM inbox_delivery d WHERE d.msg_id=m.msg_id AND d.session_id=w.session_id AND d.delivered<w.since))`

func waitsWhere(q querier, where string, args ...any) ([]Wait, error) {
	// The mail columns are behind CASE on mail_after so a wait that is not a
	// mail wait (every claim wait beat reads on every tool call) never runs
	// the inbox subqueries.
	rows, err := q.Query(`SELECT w.session_id, w.incarnation, w.decl, w.since, w.deadline, w.note, w.last_check, w.checks, w.told,
			COALESCE(w.cleared,0), COALESCE(w.reason,''), w.ready_sha,
			COALESCE(t.claim_id,''), COALESCE(t.slug,''), COALESCE(c.state,''), COALESCE(c.renewed,0),
			COALESCE(c.outcome,''), COALESCE(c.outcome_note,''),
			COALESCE(h.session_id,''), COALESCE(h.incarnation,''), COALESCE(h.label,''), COALESCE(h.worktree,''),
			COALESCE(h.started,0), COALESCE(h.last_seen,0), COALESCE(h.ended,0),
			COALESCE(r.claim_id,''), COALESCE(rh.label,''), COALESCE(r.created,0),
			COALESCE(c.session_id,''), COALESCE(c.incarnation,''),
			w.mail_after, COALESCE(mm.msg_id,0), mm.sender_session, COALESCE(ms.label,''), COALESCE(mm.created,0),
			CASE WHEN mm.msg_id IS NULL THEN 0 ELSE EXISTS
				(SELECT 1 FROM inbox_delivery d WHERE d.msg_id=mm.msg_id AND d.session_id=w.session_id) END,
			CASE WHEN w.mail_after<0 THEN 0 ELSE (SELECT COUNT(*) FROM inbox m WHERE `+mailCounts+`) END
		FROM session_waits w
		LEFT JOIN inbox mm ON mm.msg_id = CASE WHEN w.mail_after<0 THEN NULL ELSE COALESCE(
			(SELECT MIN(m.msg_id) FROM inbox m WHERE `+mailCounts+` AND NOT EXISTS
				(SELECT 1 FROM inbox_delivery d WHERE d.msg_id=m.msg_id AND d.session_id=w.session_id)),
			(SELECT MIN(m.msg_id) FROM inbox m WHERE `+mailCounts+`)) END
		LEFT JOIN sessions ms ON ms.session_id=mm.sender_session
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
		var reopenedID, ownerID, ownerInc string
		var mailSender sql.NullString
		var mailCount int
		if err := rows.Scan(&w.SessionID, &w.Incarnation, &w.Decl, unixScan{&w.Since}, unixScan{&w.Deadline}, &w.Note,
			unixScan{&w.LastCheck}, &w.Checks, &told, unixScan{&w.Cleared}, &w.Reason, &w.ReadySHA,
			&t.ClaimID, &t.Slug, &t.State, &renewed, &t.Outcome, &t.OutcomeNote,
			&t.Holder.SessionID, &t.Holder.Incarnation, &t.Holder.Label, &t.Holder.Worktree,
			unixScan{&t.Holder.Started}, unixScan{&t.Holder.LastSeen}, unixScan{&t.Holder.Ended},
			&reopenedID, &t.ReopenedBy, unixScan{&t.ReopenedAt}, &ownerID, &ownerInc,
			&w.MailAfter, &w.Mail.ID, &mailSender, &w.Mail.SenderLabel, unixScan{&w.Mail.Created},
			&w.Mail.Delivered, &mailCount); err != nil {
			return nil, err
		}
		w.mail = w.MailAfter >= 0
		if w.Mail.ID != 0 {
			w.Mail.Found = true
			w.Mail.More = mailCount - 1
			w.Mail.SenderKnown, w.Mail.SenderSession = mailSender.Valid, mailSender.String
		}
		w.Told = told != 0
		t.Reopened = reopenedID != ""
		t.HandedToWaiter = t.State == "open" && ownerID == w.SessionID && ownerInc == w.Incarnation
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
