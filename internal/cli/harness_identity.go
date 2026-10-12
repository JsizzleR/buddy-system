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
func nativeSession(env Env) (id, source string, err error) {
	claude, codex := env.getenv(EnvClaudeSession), env.getenv(EnvCodexThread)
	if claude != "" && codex != "" && claude != codex {
		return "", "", fmt.Errorf("$%s and $%s disagree (%s and %s); clear the inherited identity before continuing",
			EnvClaudeSession, EnvCodexThread, fence.Line(claude, 128), fence.Line(codex, 128))
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
