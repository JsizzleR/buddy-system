package cli

import (
	"strings"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// browser.go — the browser tool calls `who`'s EXIT line reports (D-134, issue
// #64). Why the reading exists: store/browser.go.
//
// WHICH HOOK. PostToolUse's beat, wired on matcher `*`, measured firing on
// mcp__playwright__ and mcp__claude-in-chrome__ in real transcripts. The gate's
// PreToolUse matcher never sees an MCP tool. So a call still in flight has not
// been observed, and a Codex session or an adapter's worker never is (a lifecycle
// beat records no browser call): the skills say a running job is the session's
// own to name, and no clause on EXIT is never "no browser".

// browserToolPrefixes are the MCP servers whose state lives in the session
// that drives them, each as its exact tool-name prefix. Narrow on purpose: a
// repo's own server (mcp__chrome-notes__x, mcp__playwright-docs__x) must not
// read as a browser, so this is no substring match, and a server not listed
// reads as nothing. Exact case: MCP names are case-sensitive.
var browserToolPrefixes = []string{
	"mcp__claude-in-chrome__",
	"mcp__playwright__",
	"mcp__plugin_playwright_playwright__", // the same server, installed as a plugin
	"mcp__Claude_Browser__",
	"mcp__remote-devices__Claude_Browser__",
}

// isBrowserTool says whether a tool name is a call on a browser server. Every
// tool on one counts, read-only ones too: what is recorded is that a call
// happened, and closing a tab is no proof that none is left.
func isBrowserTool(name string) bool {
	for _, p := range browserToolPrefixes {
		if rest, ok := strings.CutPrefix(name, p); ok && rest != "" {
			return true
		}
	}
	return false
}

// browserClause renders the EXIT line's clause for a live session, or "" when no
// browser call was observed. Silence and not "no browser tools used": the ledger
// cannot make that claim (see the header). "cannot say" stays apart from none,
// as the jobs clause keeps it (D-016). It has no expiry, because a tab left open
// an hour ago is still open: the age says how old the observation is.
func browserClause(st *store.Store, si store.SessionInfo, now time.Time) string {
	used, ok, err := st.BrowserUse(si.SessionID, si.Incarnation)
	if err != nil {
		return "browser tools: cannot say"
	}
	if !ok {
		return ""
	}
	return "browser tools used " + age(now, used) + " ago (a call observed, not that a tab is open)"
}
