package store

// The handoff notice's one-shot mark (D-096, amending D-055).
//
// busy used to say "your prompt was N, at or past the size you hand off at"
// on EVERY turn-opening prompt past it. On one fleet of 87 sessions that was
// 442 lines, up to 27 in one session, including on task notifications,
// subagent hand-backs and /loop keep-alives of a lane already parked after
// its handoff, and twice when two queued prompts opened together. The notice
// now speaks once per STEP: at the declared size, and again at each further
// step past it (internal/cli decides the step and the grid).
//
// ONE COLUMN, session_context.handoff_step (schema 16): the highest step this
// incarnation has been told. It lives on the context row because the step is
// computed from that row's prompt and belongs to the same incarnation; a new
// incarnation's first RecordContext resets it to -1 in the same upsert. The
// first shape stored each step as a row of authority_warned under a reserved
// key, to avoid the bump; the operator chose the column (2026-10-20), and the
// reuse had already needed a second key once a backslash-spelled authority
// path was found to normalize onto the first.

// HandoffTell claims the notice for step on this incarnation: true when step
// is higher than any step told to it so far, false otherwise (told already,
// a lower step, no context row, another incarnation's row, or a row that no
// longer holds the prompt the step was computed from).
//
// ONE conditional UPDATE, so the claim is its own compare-and-set: two prompts
// queued together run two busy hooks, and exactly one of them moves the column
// and prints. The claim is taken BEFORE the line is written, not after (the
// reverse of the inbox's write-then-mark), because mark-after let both print.
// The cost is the other direction: a write that fails after the claim loses
// that step's line, and the next step says it again.
//
// THE HIGHEST STEP, never a set, so a stale sample cannot undo a newer one: a
// busy that read 300k (step 0) and stalled while another told step 1 at 325k
// changes nothing when it lands, because 0 is not above 1 (Codex code pass on
// the first shape, which forgot steps above any lower one and printed step 1
// twice).
//
// THE FALL UNDER THE SIZE IS RecordContext's, not this function's. A sample
// under the declared size (c.HandoffAt) resets the column to -1 inside the
// upsert, which already applies only a sample at least as new as the row's
// turn, so the NEWEST observation decides. A first fix reset here instead,
// from the step < 0 a busy computed, guarded on the row still holding the
// sample: that stopped a late reset re-arming a step told after it (Codex
// design pass), but a reset that lost its race to a newer over-size sample
// also lost a real dip under the size, and the re-crossing after it was never
// told (Codex re-verify). Ordered by the turn's time, a dip that was the
// newest sample when written resets, and one written late does not.
//
// AND ONLY AGAINST THE PROMPT THE STEP WAS COMPUTED FROM: the claim matches
// the row's prompt, so a claim whose sample has since been replaced moves
// nothing. Without it, step 2 read at 350k and landing after a newer 290k
// sample had reset the column left 2 standing, and the next crossing at 300k
// was never told (Codex design pass). The prompt and not the turn's time,
// because the step is a function of the prompt alone: a call whose prompt is
// the row's computes exactly what a fresh call would. The cost is the
// accepted direction: a busy whose sample went stale says nothing, and the
// next one, reading the newer row, decides.
//
// What it does not cover (Codex code pass and re-verify): RecordContext
// replaces a row on an EQUAL turn_ms, so a busy holding a different prompt for
// the same millisecond could write it back after a newer one. A higher prompt
// written back that way can claim its step and hold the steps below it untold
// until the next higher one; an under-size one re-arms a told step. And every
// hook of one session must carry the same declared size: a sample from a
// process that declared none resets nothing, so a dip only it observed is
// lost (Codex re-verify). The hooks share the session's environment, so it
// holds wherever the size is declared in settings or by the launcher. One turn_ms is
// one assistant record's usage, so one prompt: it needs two API responses
// stamped in the same millisecond.
func (s *Store) HandoffTell(sessionID, incarnation string, prompt, step int64) (bool, error) {
	if step < 0 {
		return false, nil // under the size: RecordContext has already reset
	}
	res, err := s.db.Exec(`UPDATE session_context SET handoff_step=?
		WHERE session_id=? AND incarnation=? AND prompt=? AND handoff_step<?`,
		step, sessionID, incarnation, prompt, step)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
