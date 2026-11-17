package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/JsizzleR/buddy-system/internal/fence"
)

// ErrLabelRefused is the ledger's refusal to store a SUPPLIED label (D-206):
// one outside fence.ValidLabel, or one that would take another session's name.
// Returned by every writer of sessions.label (helloFrom's insert, revive and
// refresh arms; BeginLogical; RefreshLogical) inside the write's own
// transaction, which then rolls back: a refused hello writes nothing.
//
// Typed, because the caller answers it by who is there to read it: a
// hook-driven hello retries with no label (it must never cost the session its
// registration, invariant 2) and warns; a hand-run hello and `lifecycle begin`
// return it. The gate never writes a label, so no verdict depends on it.
var ErrLabelRefused = errors.New("label refused")

// checkLabel is the one check every label writer runs, in its transaction.
//
// "" is no label: the writer keeps the stored one or mints the default, and a
// DERIVED label is never put to the rule (orc-3's condition 1: a worktree
// basename may hold upper case or `/`). Nor is the row's CURRENT label
// re-asserted unchanged: a hand-run `hello --label <my default label>` is a
// no-op, not a refusal (lens 1).
//
// The ledger-relative clauses close what no lexical rule can (panel lens 2,
// lens 1 #6-7), each a measured path into another session's mail:
//   - another row's SESSION ID: Undelivered matches `target IN (id, label)` and
//     PausedFor `target IN (id, label, 'all')`, so a session labelled with a
//     victim's UUID drained the victim's id-addressed mail and shared its
//     brake, and target.go's canonical-id check then refused every name for
//     the victim. Ended rows count: their mail still waits under their id.
//   - another session's OPEN claim slug: resolution takes the live label tier
//     before the slug tier, so a lane labelled `orc-<project>` received every
//     lane's READY and the coordinator's brake. EXCEPT a claim parked in a run
//     (in_run set): a lane wearing the slug of the item it parked under its
//     coordinator is the fleet's own recipe (orc-3's condition 3), and while
//     it is live a message to that slug reaching it is what was meant. And
//     EXCEPT a claim whose holder has ENDED: a lane relaunched under the
//     label of a predecessor that said bye, whose claim is still open, is not
//     refused its own name (the stale claim is never reaped, invariant 11).
//     A predecessor that CRASHED has no bye, stays live, and still refuses
//     it: the relaunch wears its default label and is warned (Fable).
//
// A label two live sessions wear stays a WARNING (hello's twin line, issue
// #17), not a refusal here: it is reachable by full id and said to both.
func checkLabel(tx *sql.Tx, sessionID, label string) error {
	if label == "" {
		return nil
	}
	var current string
	switch err := tx.QueryRow(`SELECT label FROM sessions WHERE session_id=?`, sessionID).Scan(&current); {
	case err == nil && current == label:
		return nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return err
	}
	if !fence.ValidLabel(label) {
		return fmt.Errorf("%w: %s is not a label a session may choose (%s)",
			ErrLabelRefused, fence.Slot(label, fence.LabelCap), fence.LabelRule)
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE session_id=? AND session_id<>?`, label, sessionID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: %s is another session's id, and a session wearing it would receive that session's mail and pauses", ErrLabelRefused, label)
	}
	var holder string
	err := tx.QueryRow(`SELECT c.session_id FROM claims c JOIN sessions s ON s.session_id=c.session_id
		WHERE c.state='open' AND c.slug=? AND c.session_id<>? AND c.in_run='' AND s.ended IS NULL`,
		label, sessionID).Scan(&holder)
	switch {
	case err == nil:
		return fmt.Errorf("%w: %s is the slug of an open claim held by %s, and a session wearing it would receive the messages and pauses meant for that claim's holder",
			ErrLabelRefused, label, fence.Field(holder, fence.SessionIDCap))
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	return nil
}
