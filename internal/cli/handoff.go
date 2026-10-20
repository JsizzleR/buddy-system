package cli

import (
	"fmt"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// EnvHandoffAt is the operator's declaration of the prompt size at which THIS
// session should hand its work to a successor, in tokens ("500k", "1M"),
// parsed exactly as EnvContextWindow is (declaredWindow: the same suffixes,
// the same wrap guard, and unset or unparseable is OFF).
//
// WHY (#46, D-055). An orchestrator cannot tell it has grown past the point
// its operator wants it handed off, and nothing told it. Measured over one
// fleet repo, 15 days: 4 orchestrator successions, and in 3 of them the
// OPERATOR noticed the size and started the handoff. The one that went well
// was told early (~290k) and took 43 s with no operator prompt; the one that
// went badly auto-compacted at 970k, grew back to 919k before anyone asked,
// and its successor took 17 operator prompts over 2 hours to re-orient. The
// roster already prints `prompt 507k` for anyone who looks — but the model
// does not know its own prompt size, and `buddy status` is a place it has no
// reason to look.
//
// DECLARED, like the window (D-015), and for the same reason: it cannot be
// derived. Nothing in a transcript says which session is the orchestrator,
// and roles were cut (D-015) — so the operator sets it on the ONE process it
// applies to (`BUDDY_HANDOFF_AT=500k claude`), and lanes are never nagged. A
// hook is a child of its session's own process, so the declaration reaches
// that session's hooks and no other session's.
const EnvHandoffAt = "BUDDY_HANDOFF_AT"

// handoffNote is the one line busy adds to the prompt that opens a turn when
// the session's LAST OBSERVED prompt is at or past its declared handoff size,
// and "" otherwise: undeclared, unparseable, no observation yet, or under.
//
// THE OBSERVATION LAGS BY ONE REQUEST. It is the footprint the Stop hook (or
// the last beat) read from the session's own transcript — the same number the
// roster prints as `prompt N` — so the line says "last observed" and carries
// the turn's age, never "now". A prompt hook cannot know the size of the
// request it is about to open; claiming to would be the confident wrong
// number D-015 exists to stop.
//
// ONCE PER STEP, on a turn-opening prompt, and ONLY there — never on beat.
// Beat fires on every tool call, and a line repeated fifty times a turn is a
// line that gets tuned out, at a context cost on exactly the session that can
// least afford it. A turn that runs no tool at all (an orchestrator answering
// its operator in prose) still gets it, which beat could not give. It used to
// be once per TURN (D-055); see busyHandoff for why it is now once per step
// (D-096).
//
// AN OBSERVATION, NOT A CONTROL (invariant 10). It refuses nothing, reserves
// no slug (D-030), names no role, and schedules nothing: what to do about it
// is the skill's recipe, which the line only points at. It names BOTH arms
// (D-064): once the size is declared fleet-wide in settings, a lane hears it
// too, and a lane must not open its own successor (D-056) — it finishes its
// item and reports to its orchestrator.
func handoffNote(now time.Time, at int64, c store.ContextSample, observed bool) string {
	if at <= 0 || !observed || c.Prompt < at {
		return ""
	}
	return fmt.Sprintf("BUDDY: your prompt was %s at your last observed request (turn %s ago), "+
		"at or past the %s you hand off at (%s) — finish the round and hand off "+
		"(buddy skill: a coordinator, \"Hand off before you are full\"; a lane, \"Landing through an orchestrator\" step 6)\n",
		tokens(c.Prompt), age(now, c.TurnAt), tokens(at), EnvHandoffAt)
}

// handoffStep is the growth past the declared size after which the line is
// said again: at the size, then at every 25k past it (D-096).
//
// WHY 25k, from the counts. Per turn, the prompt grows by a median of 1.9k
// (p75 6k, p90 24k, p95 48k; 662 main-thread turns of this repo's own
// sessions), so 25k is about a dozen ordinary turns, and only one turn in ten
// grows past it alone. On one fleet the line fired 442 times in 87 sessions,
// up to 27 in one, while lanes ran to ~350k against a 300k declaration: one
// line per turn past it. At 25k that lane hears three lines (300k, 325k,
// 350k): enough that a session still growing hears it again, few enough that
// it is still read.
const handoffStep = 25_000

// busyHandoff reads what handoffNote needs for one session: its declaration
// from this process's environment and its observation from the ledger, for
// its CURRENT incarnation only (sampleOf) — a predecessor's footprint is not
// this session's, and a revived session that has not yet taken a turn has no
// number to be told about.
//
// ONCE PER STEP (D-096, amending D-055's "per turn"). The line said the same
// thing on every turn-opening prompt past the size: 442 times in 87 sessions
// on one fleet, up to 27 in one session, on the task notifications, subagent
// hand-backs and /loop keep-alives of lanes already parked after their
// handoff, and twice when two queued prompts opened together. Now the
// observation's STEP past the size — 0 at the size, 1 at +25k, … — is claimed
// in the ledger (store.HandoffTell) and the line printed only by the call that
// claimed it. Under the size, the claims are forgotten, so a session that
// compacts and grows back is told at its next crossing.
//
// EVERY KIND OF TURN, because the payload cannot tell them apart. The turn's
// origin (typed, task notification, peer message or hand-back, scheduled
// wake) is on the TRANSCRIPT's user record (`origin.kind`, `turnOrigin`), but
// the UserPromptSubmit payload carries only the prompt text, prompt_id and
// the session fields (read from the 2.1.304 hook-input builder), and the
// record is the harness's to write, with no measured guarantee it lands
// before this hook runs. And deferring the line to a typed prompt would starve
// exactly the session it is for: in this repo's own transcripts 234 turns
// opened on a task notification and 137 on a peer message against 329 typed,
// and a fleet coordinator can go hours between typed prompts. Told once per
// step, a turn of any kind carries it into the session's context, and the
// repetition the fleet complained of is gone.
//
// A ledger error while claiming says nothing: the line is advice (invariant
// 10), and a missed step is told at the next one.
func busyHandoff(st *store.Store, si store.SessionInfo, known bool, env Env) string {
	at := declaredWindow(env.getenv(EnvHandoffAt))
	if at <= 0 || !known {
		return "" // the common case costs no ledger read
	}
	c, observed := sampleOf(st, si)
	if !observed {
		return ""
	}
	step := int64(-1) // under: forget what was told, claim nothing
	if c.Prompt >= at {
		step = (c.Prompt - at) / handoffStep
	}
	if told, err := st.HandoffTell(si.SessionID, si.Incarnation, step); err != nil || !told {
		return ""
	}
	return handoffNote(nowOf(env), at, c, observed)
}
