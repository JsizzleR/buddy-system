package cli

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// idlewait.go — what the Stop hook records when the turn's reply is not on
// disk yet, and busy's re-read that picks it up (D-060, issue #52).
//
// WHAT WAS WRONG. `idle` reads the turn's final usage record from the
// transcript to date the idle mark, fence it to this incarnation (D-016, #11)
// and record the footprint the roster and the handoff note print (D-015,
// D-020, D-055). The harness has not written that record yet when Stop runs.
// Measured on 2.1.283 in an interactive Haiku lane, from the hook process's
// own start: the reply lands at 96.2 ms (p50), 98.0 (p90), 108.3 (max), n=20,
// in a tight cluster that looks like a write-behind flush — and `idle` itself
// is done by 26 ms (p50; 46 max). So `idle` read the PREVIOUS request. In a
// prose-only session that is the previous turn, and three things followed:
// the footprint and the handoff note lagged a whole turn (a first turn got
// none); `idle N` counted from the previous request, not from the turn that
// had just ended; and after a revival the previous request is the
// predecessor's, so the fence refused the revived session's first Stop and it
// reported no idle mark at all.
//
// WHAT THIS DOES. readTurn says, from the file itself, whether the reply has
// landed: a prompt or tool result newer than the newest usage record is still
// waiting for its answer (transcript.go). Landed — rare at Stop, the normal
// case for a hand-run or late hook — everything is dated and fenced by the
// reply, exactly as before. Pending — the normal case — no footprint is
// recorded (the reply on disk is the previous request's, and the ledger
// already has it or a newer one), the idle mark is dated by the write, and it
// is fenced by the pending record: this turn's own prompt or tool result,
// which a revived session's first turn passes and a Stop delayed across a bye
// and a hello still fails. The footprint of the turn that ended is then
// recorded by the next hook that can see it: busy, at the prompt that opens
// the following turn (measured: the reply was on disk for 7 of 7 prompts a
// human or a separate message started, and 3 of 4 a queued message started
// within 150 ms of the Stop), or the next beat.
//
// WHAT THE ROSTER SHOWS BETWEEN TURNS. A prose session's footprint (`prompt
// N`, and D-020's `cache 1h hot/cold` clock) is therefore normally one turn
// old while it rests — the observation busy made at the START of the turn that
// just ended — and older when that busy lost the queued-prompt race. It
// always carries its own request's time, never a fabricated "now". The error
// runs in the conservative direction for the cache clock: the real last
// request is newer than the observed one, so the roster can call a warm cache
// colder than it is, and never a cold one warm.
//
// CONSIDERED AND CUT. A bounded wait in `idle` for the reply: inside the
// hook's 100 ms budget (CLAUDE.md), a wait that ends 85 ms after process start
// caught 1 of the 20 measured; catching 19 of 20 takes ~110 ms of waiting, a
// ~120 ms hook, on every Stop, to record a number the next prompt records
// anyway. Waiting in a detached process instead: a writer racing the next
// turn's beat and busy is a new class of bug for the same number. Reading the
// Stop payload: its `last_assistant_message` is the reply's TEXT, a string
// with no usage in it, and buddy reads no message text by rule. Guessing
// freshness from the clock ("a reply more than N seconds old is stale"): the
// file says it exactly.

// sampleOfUsage is one usage record as the ledger stores it.
func sampleOfUsage(u usageSample, env Env) store.ContextSample {
	return store.ContextSample{
		Observed: nowOf(env), TurnAt: u.At, Model: u.Model, Effort: u.Effort,
		Prompt: u.Prompt, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite,
		Output: u.Output, Window: declaredWindow(env.getenv(EnvContextWindow)),
		Cache5m: u.Cache5m, Cache1h: u.Cache1h, TierAt: u.TierAt,
		HandoffAt: declaredWindow(env.getenv(EnvHandoffAt)),
	}
}

// recordTurnEnd is the Stop hook's whole write: the footprint and base of the
// turn that just ended, and the idle mark, under the fences described above.
func recordTurnEnd(st *store.Store, h hookInput, env Env) error {
	r := readTurn(h.TranscriptPath, h.PromptID)
	if r.superseded {
		// This Stop's turn is on disk behind a newer prompt: the session has
		// begun another turn — a queued prompt whose busy may already have
		// run, or a successor incarnation after a bye and a hello, whose
		// pending prompt the time fence alone would have admitted (Codex code
		// pass). Late, so it records nothing: not a footprint, not a base, not
		// "idle" for a session that is working.
		return nil
	}
	var since, fence time.Time
	switch {
	case r.pendingAt.IsZero() && r.ok:
		// The reply is on disk. THE SAMPLE IS FENCED BY THE TURN'S TIME, the
		// fence MarkIdle applies too. The transcript is read BEFORE the
		// identity here — the reverse of beat's order — so a Stop that sampled
		// incarnation I's turn, then lost its session to a bye and a revival,
		// stamped the NEW incarnation J with I's footprint and cache tier:
		// RecordContext checks only that J is current, and it is (Codex design
		// pass, D-033). A turn that ended before J registered cannot be J's.
		since, fence = r.u.At, r.u.At
		if si, known, err := st.SessionByID(h.SessionID); err == nil && known && si.Live() && !r.u.At.Before(si.Started) {
			_ = st.RecordContext(h.SessionID, si.Incarnation, sampleOfUsage(r.u, env))
			// THE BASE (D-038): the commit this session's tree was on as the
			// turn ended, under the same fence as the footprint. Here and not
			// on beat, which forks no git by design. Best effort: a HEAD that
			// cannot be read records nothing, and the previous base keeps its
			// own age.
			if sha := headOf(h.Cwd); sha != "" {
				_ = st.RecordBase(h.SessionID, si.Incarnation, sha, r.u.At)
			}
		}
	case !r.pendingAt.IsZero():
		// The reply is not written yet. The newest usage record is the
		// PREVIOUS request's, so it is recorded as nothing: not as this turn's
		// footprint, and not as this turn's end. The mark is dated by the
		// write (a zero since) and fenced by the pending record, the one thing
		// on disk that is this turn's own. The base is this turn's HEAD
		// whatever the reply says, so it is recorded, dated by the observation
		// and behind the same fence.
		fence = r.pendingAt
		if si, known, err := st.SessionByID(h.SessionID); err == nil && known && si.Live() && !fence.Before(si.Started) {
			if sha := headOf(h.Cwd); sha != "" {
				_ = st.RecordBase(h.SessionID, si.Incarnation, sha, nowOf(env))
			}
		}
	}
	// Neither: nothing readable, and the mark is written unfenced and dated by
	// the write, as it was before issue #11.
	return stopTurn(st, h, env, since, fence)
}

// stopLead opens a Stop's held-open turn. It is buddy's own line, ahead of
// the inbox block the header marks as peer text.
const stopLead = "BUDDY: mail reached you as this turn ended, so the Stop hook kept the turn open to hand it over (D-099). Act on it if it asks for something; otherwise end the turn.\n"

// stopTurn is the idle mark, or the mail that would have been stranded
// behind it (D-099, amending D-016 and D-039).
//
// WHAT WAS WRONG. A lane's pattern is "msg the orchestrator, stop": the reply
// lands seconds after the lane's last tool call and before or at its Stop.
// msg saw "last seen 4s ago", named no wake (a session seen recently drains
// on its own), and the Stop then marked the session idle with the mail still
// queued. Nothing runs a tool at a prompt, so it sat there: six confirmed
// stalls of 6 to 75 minutes on one fleet (one: msg at 20:13:20.7, the
// target's last tool call 20:13:13, its turn ended 20:13:21.9).
//
// WHAT THIS DOES. In ONE transaction with the idle mark (store.StopTurn), the
// Stop looks for queued mail. If there is any, it is drained by beat's rules
// (bound, fence, write first and mark after) into a Stop `decision: block`
// whose reason is the mail, so the turn goes on one more step with it, and
// the session is not marked idle, since it is not resting. Otherwise idle is
// recorded exactly as before. One transaction is what closes the race from
// the other side: a msg whose insert commits first is seen here, and one that
// commits after finds the idle mark when it re-reads it (cmdMsg), and names
// the wake.
//
// NEVER TWICE IN A ROW: a turn a Stop hook already continued says so
// (stop_hook_active) and is marked idle with its mail queued, so a peer
// writing in a loop cannot keep a session from resting. RESIDUAL (Codex code
// pass): mail queued during such a continued turn, after its last tool call,
// is stranded as before D-099 — msg saw no idle mark and this Stop may not
// hold the turn again. Closing it needs a second hold (overturning "never
// twice in a row") or the time of the last hold (a schema change); mail sent
// after this Stop's idle mark is woken by msg. Never for a subagent (cmdIdle
// returns first), never for adapter mail (Undelivered excludes it). This is
// not inferring busy (D-016): the mark is withheld because the hook itself
// is continuing the turn.
//
// CONSIDERED AND CUT. Delivering the mail as additionalContext on Stop: the
// documentation says the conversation continues for that too, but whether it
// sets stop_hook_active on the next Stop is unmeasured, and the loop guard
// rests on it; `decision: block` is documented to.
func stopTurn(st *store.Store, h hookInput, env Env, since, fence time.Time) error {
	label := ""
	if si, known, err := st.SessionByID(h.SessionID); err == nil && known {
		label = si.Label
	}
	now := nowOf(env)
	var deliverErr error
	_, err := st.StopTurn(h.SessionID, label, since, fence, !h.StopHookActive, func(msgs []store.InboxMsg) ([]int64, error) {
		shown := beatDrain(msgs, len(stopLead), now)
		if len(shown) == 0 {
			return nil, nil
		}
		var b strings.Builder
		b.WriteString(stopLead)
		ids := writeInbox(&b, shown, now)
		enc, err := json.Marshal(map[string]any{"decision": "block", "reason": b.String()})
		if err == nil {
			_, err = env.Stdout.Write(append(enc, '\n'))
		}
		if err != nil {
			deliverErr = err
			return nil, err
		}
		return ids, nil
	})
	if deliverErr != nil {
		// The write failed, so nothing was marked and the turn is not held:
		// the session IS resting, with its mail queued. Record that, so the
		// next msg names the wake, rather than leave no idle mark at all.
		_ = st.MarkIdle(h.SessionID, since, fence)
	}
	return err
}

// resampleFootprint is busy's read of the transcript before it answers the
// handoff question (D-055). By the time a turn-opening prompt runs, the
// previous turn's reply has usually landed, so the note sees the turn that
// just ended rather than the one before it, and a prose-only session's
// footprint is recorded at all (above: the Stop that ended it could not). In
// the race — a queued prompt within ~100 ms of the Stop — it reads the
// previous request, which the ledger already holds, and RecordContext's
// newer-or-equal-turn rule makes that a no-op. The order is beat's: the
// identity was read before the transcript, so a revival in between drops the
// sample instead of stamping a successor. And the fence is idle's: a reply
// from before this incarnation registered is its predecessor's, and D-055
// decided a revived session is not told its predecessor's size — at its
// first prompt the newest reply on disk is exactly that.
func resampleFootprint(st *store.Store, h hookInput, me store.SessionInfo, known bool, env Env) {
	if !known || !me.Live() {
		return
	}
	if u, ok := lastUsage(h.TranscriptPath); ok && !u.At.Before(me.Started) {
		_ = st.RecordContext(h.SessionID, me.Incarnation, sampleOfUsage(u, env))
	}
}
