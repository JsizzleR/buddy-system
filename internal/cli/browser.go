package cli

import (
	"strings"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// browser.go — the browser tool calls `who`'s EXIT line reports (D-134, issue
// #64). Why the reading exists: store/browser.go.
//
// WHICH HOOKS. PostToolUse's beat, wired on matcher `*`, measured firing on
// mcp__playwright__ and mcp__claude-in-chrome__ in real transcripts; it fires
// only on success. The gate's PreToolUse matcher never sees an MCP tool. Two more
// entries the operator adds by hand (#71) run the same beat line on PreToolUse,
// which sees a call before it runs, so a call in flight is observed, and on
// PostToolUseFailure, a call that failed. Measured over the transcripts on this
// machine, 2026-11-07: 0 of 6,838 failed calls carried a PostToolUse row, against
// 4,128 of about 198,000 that succeeded. A Codex session or an adapter's worker
// is never observed (a lifecycle beat records no browser call): the skills say a
// running job is the session's own to name, and no clause on EXIT is never "no
// browser".

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
	// Google's Chrome DevTools server, as its README's `claude mcp add` names it
	// and as its own plugin (chrome-devtools-mcp) names it: navigate_page,
	// new_page, click, take_screenshot.
	"mcp__chrome-devtools__",
	"mcp__plugin_chrome-devtools-mcp_chrome-devtools__",
	// browser-use's local server, as its docs' `claude mcp add` names it:
	// browser_navigate, browser_click, browser_list_tabs. Its marketplace plugin's
	// server name was not read, so that form is not here.
	"mcp__browser-use__",
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

// beatOffSuccess is the whole of beat for the two events that are not a
// completed call, PreToolUse and PostToolUseFailure (#71): it records a call on a
// browser server and does nothing else, so it reports true for either event and
// the caller returns. No heartbeat, no inbox, no dirty path, no notice: a call
// that has not run, or failed, wrote nothing, and the completed call's beat does
// the rest. A PreToolUse hook can refuse the call it runs before, on exit 2 or a
// decision on stdout, so this writes NOTHING to stdout and returns no error: every
// failure is swallowed, and the hook line's `exit 0` covers a beat that died. A
// tool that is not a browser's opens no ledger. A busy ledger can still delay that
// browser call by busy_timeout (5 s) per wait, a migration adding one more.
func beatOffSuccess(h hookInput, env Env) bool {
	if h.HookEventName != "PreToolUse" && h.HookEventName != "PostToolUseFailure" {
		return false
	}
	if !isBrowserTool(h.ToolName) {
		return true
	}
	st, _, err := openRepo(h.Cwd, env)
	if err != nil {
		return true
	}
	defer st.Close()
	if me, known, err := st.SessionByID(h.SessionID); err == nil && known && me.Live() {
		_ = st.RecordBrowserUse(h.SessionID, me.Incarnation)
	}
	return true
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
