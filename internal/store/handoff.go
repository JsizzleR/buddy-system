package store

import "database/sql"

// The handoff notice's one-shot marks (D-096, amending D-055).
//
// busy used to say "your prompt was N, at or past the size you hand off at"
// on EVERY turn-opening prompt past it. On one fleet of 87 sessions that was
// 442 lines, up to 27 in one session, including on task notifications,
// subagent hand-backs and /loop keep-alives of a lane already parked after
// its handoff, and twice when two queued prompts opened together. The notice
// now speaks once per STEP: at the declared size, and again at each further
// step past it (internal/cli decides the step and the grid).
//
// NO SCHEMA BUMP. The state is one fact per (session, incarnation, step):
// "told". authority_warned is already exactly that register — (session,
// incarnation, what, version) → told once, never rendered, read only by its
// exact key (D-028) — so a step is stored there under a key no watched file
// can have. Not merely a leading "/": NormalizeScope tests for one BEFORE it
// turns backslashes into slashes, so `authority add '\handoff-at'` stores
// "/handoff-at" (Codex code pass, D-096). A doubled slash is what path.Clean
// can never return, so no normalized path, and neither CLAUDE.md nor
// AGENTS.md, can be this key. A new column would have bumped the schema, and
// a bump makes every older binary on the machine DENY (D-037) for the sake of
// one integer.
const handoffKey = "//handoff-at"

// HandoffTell claims the notice for step on this incarnation: true when this
// call is the first to claim it, false when the step was told already. The
// claim is taken BEFORE the line is written, not after (the reverse of the
// inbox's write-then-mark): two prompts queued together run two busy hooks,
// and mark-after let both of them print. The cost is the other direction — a
// write that fails after the claim loses that step's line — and the next
// step says it again.
//
// step < 0 means under the declared size (a compaction, usually): forget
// every step told, claim nothing, so the next crossing is told again. Only
// UNDER forgets. The first shape also forgot the steps above any lower step,
// and the observation is read outside this transaction, so a busy that read
// 300k and stalled deleted the step-1 mark a newer busy had just printed at
// 325k, and a third printed it again (Codex code pass, D-096). Under the size
// that race needs a sample from before the crossing, and costs one repeated
// line; a compaction that stays over the size is told again only at a new
// step.
func (s *Store) HandoffTell(sessionID, incarnation string, step int64) (bool, error) {
	told := false
	err := s.tx(func(tx *sql.Tx) error {
		if step < 0 {
			_, err := tx.Exec(`DELETE FROM authority_warned WHERE session_id=? AND incarnation=? AND folded=?`,
				sessionID, incarnation, handoffKey)
			return err
		}
		res, err := tx.Exec(`INSERT OR IGNORE INTO authority_warned (session_id, incarnation, folded, mtime_ns, warned) VALUES (?,?,?,?,?)`,
			sessionID, incarnation, handoffKey, step, s.now().Unix())
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		told = n == 1
		return err
	})
	return told, err
}
