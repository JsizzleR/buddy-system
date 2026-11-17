package cli

// The paste rule and the refusal wording for session ids (D-201).

import (
	"errors"
	"fmt"
	"io"
	"strings"

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

// nativeIDFrom reports an id the session cannot choose: the hook payload's or
// a harness's own variable. Anything else (--session, $BUDDY_SESSION) the
// caller typed or exported, and can change.
func nativeIDFrom(from string) bool {
	return from == "hook" || from == EnvClaudeSession || from == EnvCodexThread
}

// idSource is where an id came from for its refusal's wording: a --session or
// $BUDDY_SESSION value EQUAL to the harness's own id is that native id, typed
// again, and gets the native wording; read by syntax alone, it got the
// charset recipe, which is the alias trap (Fable cross-check).
func idSource(env Env, id, from string) string {
	if nativeIDFrom(from) {
		return from
	}
	if nid, src, err := nativeSession(env); err == nil && nid != "" && nid == id {
		return src
	}
	return from
}

// unregistrableAdvice is what to do about an id the ledger will not register,
// worded by where it came from (orc-2's ruling, D-201). A NATIVE id gets no
// charset recipe: a lane that followed one and ran `hello --session <alias>`
// would then claim as the alias while the gate still judges the payload id,
// so the gate refuses it on its own claims; and with a harness marker plus
// $BUDDY_SESSION set to the alias, classifyHook denies every gated call (the
// alias trap, TestNativeRefusalNamesNoAlias). A chosen id gets the rule.
func unregistrableAdvice(from string) string {
	if nativeIDFrom(from) {
		return "it is this session's harness id, so do not say hello under another id (the gate judges every tool call by the harness id, and would refuse an alias on its own claims); tell the operator"
	}
	return "the ledger registers " + fence.SessionIDRule + "; give one that is"
}

// idRefusalLine is the one-line refusal to register an id, for hello's
// digest (a hook's stdout is the one place a SessionStart is read: the wired
// line drops stderr) and for the fleet beside a home ledger.
//
// What being unregistered costs depends on the ledger, so a native refusal
// says it per ledger: a fleet has no brake (`pause` refuses there, D-191) and
// claims only slots; a fleet refusing beside a home whose older-binary row
// still refreshes leaves the home half registered, so the home's sentence
// would be false there (Codex code pass).
func idRefusalLine(id, from, where string, fleet bool) string {
	cost := ""
	if nativeIDFrom(from) {
		cost = ". Until then this session can hold no claim here, peers' claims still refuse it, and only `buddy pause all` brakes it"
		if fleet {
			cost = ". Until then it is not on the fleet's roster: no fleet mail reaches it and it can hold no fleet slot"
		}
	}
	return fmt.Sprintf("BUDDY: session id %s cannot be registered in %s (D-201): %s%s\n",
		fence.Field(id, fence.SessionIDCap), where, unregistrableAdvice(from), cost)
}

// idRefusal is hello's error for a hand-run registration the ledger refused.
// It unwraps to store.ErrSessionIDRefused, so the fleet's deferred handler
// tells it from a fleet failure (isSessionIDRefused).
type idRefusal struct {
	msg   string
	cause error
}

func (e idRefusal) Error() string { return e.msg }
func (e idRefusal) Unwrap() error { return e.cause }

// helloRefused answers a hello whose id the ledger refused. Hook-driven: the
// line on stdout and exit 0, as a fleet that did not open is said (D-191):
// SessionStart cannot block, and stdout is all the session reads. Hand-run:
// an error, so the status says it.
func helloRefused(env Env, id, from, where string, fleet, hookDriven bool) error {
	line := idRefusalLine(id, from, where, fleet)
	if hookDriven {
		_, err := io.WriteString(env.Stdout, line)
		return err
	}
	return idRefusal{msg: strings.TrimSuffix(strings.TrimPrefix(line, "BUDDY: "), "\n"), cause: store.ErrSessionIDRefused}
}

// helloRemedy is a roster note's `buddy hello` remedy for a session id the
// ledger has no row for: the command when the id would register, else the
// refusal's own advice (a payload id is native: no alias, D-201).
func helloRemedy(id, does string) string {
	if p, ok := pasteID(id); ok {
		return "`buddy hello --session " + p + " </dev/null` " + does
	}
	return "its harness id cannot be registered (D-201): tell the operator, and do not say hello under another id"
}

// reachArg is the dirty warning's `buddy msg` operand: the holder's label
// only when it pastes back as typed and cannot broadcast (a label `all`
// pasted as `buddy msg 'all'` reached every session, since target resolution
// takes `all` before any label; a tab pasted as a space reached whoever wore
// `a b`: panel, lens 3), else a placeholder naming where to read the holder.
// The test is shellArg's own fence leaving the label as it is: this line has
// no markdown code span (dirty.go), so a quote or backtick in a label is made
// inert by shellArg's quoting and still names that label.
func reachArg(label string) string {
	if label != store.AllTarget && label != "" && fence.Line(label, 512) == label {
		return shellArg(label)
	}
	return "<the holder: see buddy whose>"
}
