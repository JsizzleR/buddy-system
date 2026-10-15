package cli

// Native logical identity is separate from process ancestry. Codex app-server
// can host several threads, so adding "codex" to the Claude process matcher
// would turn a shared transport's lifetime into a session lifetime. This first
// integration deliberately leaves Codex unbound and refuses lifecycle events
// whose incarnation cannot yet be established.

import (
	"fmt"

	"github.com/JsizzleR/buddy-system/internal/fence"
)

// EnvCodexThread is an OBSERVED, experimental contract: codex-cli 0.160.0 on
// 2026-10-04 exported this value into a tool shell, equal to its native thread
// id. It is not a documented portable hook environment contract. Installation
// marks Codex hook commands with BUDDY_HARNESS instead of depending on this
// variable being present there. CODEX_SESSION_ID is not used as a fallback.
const EnvCodexThread = "CODEX_THREAD_ID"

const EnvHarness = "BUDDY_HARNESS"

// nativeSession answers only a harness-provided identity. In particular an
// ordinary BUDDY_SESSION override is never evidence for consuming a wait's
// completion notice. Conflicting inherited native variables cannot be ordered:
// a Claude worker launched from Codex (or the reverse) must clear the parent's
// variable instead of silently borrowing its identity.
//
// Nothing in the environment says which of the two is the inherited one: every
// variable either harness sets passes to a child launched from its shell, and
// the ancestry walk is the identity of ONE decision (D-025), not a tiebreak
// here (D-068). So the refusal names the fix for each harness and leaves the
// choice to the caller, who knows which harness it is running under. F2 (the
// 2026-10-14 trial) measured where the leak comes from: a lane started as a
// CHILD of a harness's shell inherits it, and a herdr tab does not, because
// its shell takes the herdr server's environment, not the launcher's.
func nativeSession(env Env) (id, source string, err error) {
	claude, codex := env.getenv(EnvClaudeSession), env.getenv(EnvCodexThread)
	if claude != "" && codex != "" && claude != codex {
		return "", "", fmt.Errorf("$%s and $%s disagree (%s and %s): one was inherited from the shell that launched this harness. "+
			"Under Codex run `env -u %s buddy ...`; under Claude Code run `env -u %s buddy ...`. "+
			"Launch the next lane in a herdr tab, which does not inherit your shell's environment, or with the parent's variable unset",
			EnvClaudeSession, EnvCodexThread, fence.Line(claude, 128), fence.Line(codex, 128), EnvClaudeSession, EnvCodexThread)
	}
	if codex != "" {
		return codex, EnvCodexThread, nil
	}
	return claude, EnvClaudeSession, nil
}

// environmentSession keeps the ordinary override precedence without using cwd.
// It is shared by registration and message attribution; whoAmI adds --session
// ahead of it and its constrained directory inference after it.
func environmentSession(env Env) (id, source string, err error) {
	if id := env.getenv(EnvSession); id != "" {
		return id, EnvSession, nil
	}
	return nativeSession(env)
}

// classifyHook preserves the native payload's session_id. The explicit marker
// makes hook classification independent of undocumented environment exports.
// This is cooperative identification, not authentication: the machine user
// already owns the ledger. Never guess that a Codex process is Claude merely
// because the ancestry walker eventually finds a Claude ancestor.
func classifyHook(h *hookInput, env Env) error {
	marker := env.getenv(EnvHarness)
	if marker != "" && h.Harness != "" && marker != h.Harness {
		return fmt.Errorf("$%s and buddy_harness disagree", EnvHarness)
	}
	if marker == "" {
		marker = h.Harness
	}
	if marker != "" && marker != "claude" && marker != "codex" {
		return fmt.Errorf("unsupported harness marker %q (want claude or codex)", fence.Line(marker, 64))
	}
	if marker == "" && env.getenv(EnvCodexThread) != "" {
		marker = "codex"
	}
	if marker == "" {
		return nil // preserve the established Claude hook contract
	}
	id, source, err := nativeSession(env)
	if err != nil {
		return err
	}
	if id != "" && (id != h.SessionID || (marker == "claude") != (source == EnvClaudeSession)) {
		return fmt.Errorf("%s hook session_id and $%s disagree; refusing to mix harness identities", marker, source)
	}
	if b := env.getenv(EnvSession); b != "" && b != h.SessionID {
		return fmt.Errorf("%s hook session_id and $%s disagree; use the native session identity consistently", marker, EnvSession)
	}
	h.Harness = marker
	return nil
}

func hookAnchor(h hookInput, env Env) (harnessProc, bool) {
	if h.Harness == "codex" {
		return harnessProc{}, false
	}
	return env.anchor()
}

func unsupportedCodexLifecycle(verb string) error {
	return fmt.Errorf("unfenced Codex %s hooks are unsupported; use lifecycle hook --incarnation with the adapter's captured token (turn completion is not session end)", verb)
}

// heardVerb is errVerb for a verb a session runs AS ITSELF: before the verb
// runs, the session the harness names is recorded as heard from (F3, the
// 2026-10-14 Codex fleet trial). Before, not after, so the verb's own output
// already reads the present: a Claude session's PreToolUse beat also lands
// before its command runs, and `buddy status` should say `seen 0s` under
// either harness.
//
// Measured there: a Codex orchestrator re-claimed and, six minutes later, the
// roster called it "NOT SEEN FOR 2h" and `msg` said the same to everyone who
// wrote to it. A Claude session's `last_seen` moves on every tool call because
// its PreToolUse hook beats; a Codex session has no buddy lifecycle hooks
// (unfenced ones are refused, D-069), so only `hello` ever moved it, and an
// active coordinator read as stale. The verbs it does run are the evidence.
//
// ONLY the native harness identity counts ($CLAUDE_CODE_SESSION_ID or
// $CODEX_THREAD_ID, refusing their disagreement), never --session or
// $BUDDY_SESSION: those say whom the caller is acting FOR, and the operator
// running `buddy status --session X` at a terminal is not X being heard from.
// The same rule `wait` uses to say "THIS session" (wait.go). Failure is
// silent and costs the verb nothing: this is an observation, and a ledger
// that cannot be opened or written here fails the verb itself next, with its
// own error (and ledgeraccess.go's explanation).
//
// Not wrapped, each for its own reason: init (may create the ledger); the
// hook verbs hello, bye, beat, idle, busy, gate and commit-gate (each has its
// own contract, bye must never look like activity, and the gate has a 100 ms
// budget); lifecycle and adapter (logical events carry a captured
// incarnation, D-069, and are the adapter's register); diagnose (never opens
// the ledger with SQLite, D-072).
func heardVerb(name string, fn func([]string, Env) error) func([]string, Env) int {
	return errVerb(name, func(args []string, env Env) error {
		noteHeard(env)
		return fn(args, env)
	})
}

func noteHeard(env Env) {
	id, _, err := nativeSession(env)
	if err != nil || id == "" {
		return
	}
	st, _, err := openRepo(env.Cwd, env)
	if err != nil {
		return
	}
	defer st.Close()
	st.Heard(id) //nolint:errcheck // an observation; see heardVerb
}
