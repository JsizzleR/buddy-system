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
// LEVEL-TRIGGERED, once per turn-opening prompt, and ONLY there — never on
// beat. Beat fires on every tool call, and a line repeated fifty times a turn
// is a line that gets tuned out, at a context cost on exactly the session that
// can least afford it. Once per turn is the grain a handoff is decided at;
// and a turn that runs no tool at all (an orchestrator answering its
// operator in prose) still gets it, which beat could not give.
//
// AN OBSERVATION, NOT A CONTROL (invariant 10). It refuses nothing, reserves
// no slug (D-030), names no role, and schedules nothing: what to do about it
// is the skill's recipe ("Running a fleet"), which the line only points at.
func handoffNote(now time.Time, at int64, c store.ContextSample, observed bool) string {
	if at <= 0 || !observed || c.Prompt < at {
		return ""
	}
	return fmt.Sprintf("BUDDY: your prompt was %s at your last observed request (turn %s ago), "+
		"at or past the %s you hand off at (%s) — finish the round, write the handoff, "+
		"and start your successor (buddy skill: Running a fleet)\n",
		tokens(c.Prompt), age(now, c.TurnAt), tokens(at), EnvHandoffAt)
}

// busyHandoff reads what handoffNote needs for one session: its declaration
// from this process's environment and its observation from the ledger, for
// its CURRENT incarnation only (sampleOf) — a predecessor's footprint is not
// this session's, and a revived session that has not yet taken a turn has no
// number to be told about.
func busyHandoff(st *store.Store, si store.SessionInfo, known bool, env Env) string {
	at := declaredWindow(env.getenv(EnvHandoffAt))
	if at <= 0 || !known {
		return "" // the common case costs no ledger read
	}
	c, observed := sampleOf(st, si)
	return handoffNote(nowOf(env), at, c, observed)
}
