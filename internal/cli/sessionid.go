package cli

// The paste rule and the refusal wording for session ids (D-201).

import (
	"errors"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

// pasteID is the ONE rule for printing a session id as part of a command to
// paste: the id itself when it meets fence.ValidSessionID, else ok=false and
// the caller prints a placeholder instead.
//
// THE RULE WAS FOUR RULES. Three sites guarded with `fence.Line(id,128)==id`
// plus shellQuote (helloCountLine, unregisteredClause, ghSlotHolder), one also
// refused a leading `-` (byeRemedy), and nine guarded nothing: an id fenced
// for display was printed as a command, so `sess<TAB>x` pasted as `sess x`,
// another session (fleet.go's roster notes, ghstack.go's slot denial,
// assertedNotLive, errUncorroborated, bye --force, wait clear). Even the
// guarded form failed on a backtick: shellQuote makes it shell-safe and the
// fence keeps it, but these commands sit in markdown code spans, which a
// backtick ends early (dirty.go's comment records the same failure; Codex
// design pass). An id the predicate admits is ASCII alphanumerics and `._-`
// with no leading `-`, so shellQuote is the identity, no fence alters it, no
// span ends, no flag is read; and a hello remedy can never name an id that
// hello would refuse (panel, lens 1). Applied to the RAW id, never a fenced
// copy: assertedNotLive used to overwrite its id with the fenced one first,
// which would approve `sess x` for a raw `sess<TAB>x` (Codex).
//
// What it costs: a row an older binary registered under a hostile id is
// still printed (fenced) but never as a command; the placeholder stands in.
func pasteID(id string) (string, bool) {
	if fence.ValidSessionID(id) {
		return id, true
	}
	return "", false
}

// pasteIDOr is pasteID with its placeholder.
func pasteIDOr(id, placeholder string) string {
	if p, ok := pasteID(id); ok {
		return p
	}
	return placeholder
}

// isSessionIDRefused reports a ledger refusal to register an id (D-201).
func isSessionIDRefused(err error) bool { return errors.Is(err, store.ErrSessionIDRefused) }
