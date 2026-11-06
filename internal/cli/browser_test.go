package cli

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIsBrowserToolIsNarrow(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"mcp__claude-in-chrome__navigate", true},
		{"mcp__claude-in-chrome__tabs_context_mcp", true},
		{"mcp__playwright__browser_snapshot", true},
		{"mcp__plugin_playwright_playwright__browser_navigate", true},
		{"mcp__Claude_Browser__navigate", true},
		{"mcp__remote-devices__Claude_Browser__navigate", true},

		// A repo's own MCP tools and the servers on this machine that are not
		// browsers.
		{"mcp__buddylist__chat_read", false},
		{"mcp__claude_ai_Claude_Docs__read", false},
		{"mcp__my-chrome-notes__navigate", false},
		{"mcp__playwright-docs__search", false},
		{"mcp__claude-in-chrome-notes__navigate", false},
		{"mcp__remote-devices__computer_click", false},
		{"mcp__plugin_playwright_docs__search", false},
		{"mcp__playwright", false},
		{"mcp__playwright__", false},
		{"xmcp__playwright__browser_click", false},
		{"MCP__PLAYWRIGHT__browser_click", false},
		{"mcp__claude_browser__navigate", false},

		// Native tools, and the one with a browser-shaped word in its name.
		{"Bash", false},
		{"Edit", false},
		{"WebFetch", false},
		{"", false},
	} {
		if got := isBrowserTool(tc.name); got != tc.want {
			t.Errorf("isBrowserTool(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// install.sh's matcher report carries its own copy of the list, in Python: a
// server added here and not there would be called excluded by every install.
func TestInstallKnowsEveryBrowserPrefix(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "scripts", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if len(browserToolPrefixes) == 0 {
		t.Fatal("control: the list is empty")
	}
	for _, p := range browserToolPrefixes {
		if !strings.Contains(string(b), `"`+p+`"`) {
			t.Errorf("scripts/install.sh does not name %s", p)
		}
	}
}

// exitOf is who's EXIT row for a session of the fixture.
func exitOf(t *testing.T, f *fixture, who string) string {
	t.Helper()
	out, errw, code := f.run(t, f.repo, "", "who", who)
	if code != 0 {
		t.Fatalf("who: %s", errw)
	}
	return rowWith(t, out, "EXIT ")
}

func browserFixture(t *testing.T) *fixture {
	t.Helper()
	boundedParallel(t)
	f := newFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-o", f.repo, "", ""), "hello", "--label", "orch"); code != 0 {
		t.Fatal(errw)
	}
	return f
}

func TestExitLineSaysABrowserToolWasUsed(t *testing.T) {
	const clause = "browser tools used"
	f := browserFixture(t)

	// Nothing observed: no clause, and not a claim that nothing happened.
	before := exitOf(t, f, "orch")
	if strings.Contains(before, "browser") {
		t.Fatalf("no call observed must print nothing: %q", before)
	}

	// Calls that are not a browser's leave the line byte for byte as it was.
	for _, tool := range []string{"Bash", "mcp__buddylist__chat_read", "mcp__my-chrome-notes__navigate"} {
		if _, errw, code := f.run(t, f.repo, hookJSON("sess-o", f.repo, tool, ""), "beat"); code != 0 {
			t.Fatalf("beat %s: %s", tool, errw)
		}
		if got := exitOf(t, f, "orch"); got != before {
			t.Fatalf("%s changed the line: %q", tool, got)
		}
	}

	// A browser call: named with its age, and saying what it is.
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-o", f.repo, "mcp__claude-in-chrome__navigate", ""), "beat"); code != 0 {
		t.Fatalf("beat: %s", errw)
	}
	f.clock = f.clock.Add(3 * time.Minute)
	got := exitOf(t, f, "orch")
	if !strings.Contains(got, "; browser tools used 3m ago (a call observed, not that a tab is open) — ending now would leave no claim") {
		t.Fatalf("the clause: %q", got)
	}

	// The rest of the line speaks as it did, for claims alone.
	if !strings.HasPrefix(got, "EXIT         no claims held; no wait declared;") {
		t.Fatalf("the rest of the line changed: %q", got)
	}

	// It does not expire: a tab left open a month ago is still open.
	f.clock = f.clock.Add(30 * 24 * time.Hour)
	if got := exitOf(t, f, "orch"); !strings.Contains(got, "browser tools used 30d ago") {
		t.Fatalf("after a month: %q", got)
	}

	// `status` is the same report on the caller.
	out, errw, code := f.run(t, f.repo, "", "status", "--session", "sess-o")
	if code != 0 || !strings.Contains(rowWith(t, out, "EXIT "), clause) {
		t.Fatalf("status: %d %s %s", code, out, errw)
	}

	// Ended: the line says so, and the reading has nothing left to add.
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-o", f.repo, "", ""), "bye"); code != 0 {
		t.Fatalf("bye: %s", errw)
	}
	if got := exitOf(t, f, "orch"); got != "EXIT         already ended" {
		t.Fatalf("ended: %q", got)
	}
}

// REPORT ONLY (D-134): a browser beat and a reading change no verdict and no
// delivery. Each arm runs against a session that holds a claim and carries a
// reading, so a refusal, a release or a skipped drain added anywhere would show.
func TestABrowserReadingChangesNothingElse(t *testing.T) {
	f := browserFixture(t)
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-p", f.repo, "", ""), "hello", "--label", "peer"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := f.run(t, f.repo, "", "claim", "held", "--session", "sess-o", "--desc", "x", "--scope", "a.go"); code != 0 {
		t.Fatalf("claim: %s", errw)
	}
	if _, errw, code := f.run(t, f.repo, "", "msg", "orch", "first"); code != 0 {
		t.Fatalf("msg: %s", errw)
	}

	// The beat itself still drains the inbox, and leaves the claim held.
	browser := hookJSON("sess-o", f.repo, "mcp__playwright__browser_click", "")
	out, errw, code := f.run(t, f.repo, browser, "beat")
	if code != 0 || !strings.Contains(out, "first") {
		t.Fatalf("a browser beat must deliver the inbox: %d %q %s", code, out, errw)
	}
	exit := exitOf(t, f, "orch")
	if !strings.Contains(exit, "browser tools used") || !strings.HasSuffix(exit, "first: held") {
		t.Fatalf("the claim and the reading sit side by side: %q", exit)
	}

	// The gate: the holder's own edit passes and a peer's is denied, each as with
	// no reading. The peer's denial is the control that the gate is armed.
	aGo := filepath.Join(f.repo, "a.go")
	if out, _, _ := f.run(t, f.repo, hookJSON("sess-o", f.repo, "Edit", aGo), "gate"); func() bool { _, d := decodeDeny(t, out); return d }() {
		t.Fatalf("the reading denied its holder: %s", out)
	}
	if out, _, _ := f.run(t, f.repo, hookJSON("sess-p", f.repo, "Edit", aGo), "gate"); !func() bool { _, d := decodeDeny(t, out); return d }() {
		t.Fatalf("control: a peer's edit in the claim must be denied: %s", out)
	}
	// A session with a reading is denied nothing it was not denied before.
	free := filepath.Join(f.repo, "free.go")
	if out, _, _ := f.run(t, f.repo, hookJSON("sess-o", f.repo, "Edit", free), "gate"); func() bool { _, d := decodeDeny(t, out); return d }() {
		t.Fatalf("a free path was denied: %s", out)
	}

	// Another beat, a sweep and a peer's refused claim leave the claim where it is.
	if _, errw, code := f.run(t, f.repo, browser, "beat"); code != 0 {
		t.Fatalf("second beat: %s", errw)
	}
	if _, errw, code := f.run(t, f.repo, "", "sweep"); code != 0 {
		t.Fatalf("sweep: %s", errw)
	}
	if _, _, code := f.run(t, f.repo, "", "claim", "peers", "--session", "sess-p", "--desc", "x", "--scope", "a.go"); code != 4 {
		t.Fatalf("the holder's claim must still refuse with exit 4, got %d", code)
	}
	if got := exitOf(t, f, "orch"); !strings.HasSuffix(got, "first: held") {
		t.Fatalf("the claim moved: %q", got)
	}

	// And its release is the holder's, as ever.
	if _, errw, code := f.run(t, f.repo, "", "release", "held", "--session", "sess-o"); code != 0 {
		t.Fatalf("release: %s", errw)
	}
}

// A subagent's call fires the hook under the parent's id, and its browser lives
// in the parent's process, so it counts.
func TestASubagentsBrowserCallCounts(t *testing.T) {
	f := browserFixture(t)
	in, err := json.Marshal(map[string]any{
		"session_id": "sess-o", "cwd": f.repo, "agent_id": "agent-1",
		"tool_name": "mcp__playwright__browser_click", "tool_input": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, errw, code := f.run(t, f.repo, string(in), "beat"); code != 0 {
		t.Fatalf("beat: %s", errw)
	}
	if got := exitOf(t, f, "orch"); !strings.Contains(got, "browser tools used 0s ago") {
		t.Fatalf("a subagent's call: %q", got)
	}
}

func TestABeatForAnEndedSessionRecordsNoBrowserUse(t *testing.T) {
	f := browserFixture(t)
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-o", f.repo, "", ""), "bye"); code != 0 {
		t.Fatalf("bye: %s", errw)
	}
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-o", f.repo, "mcp__playwright__browser_click", ""), "beat"); code != 0 {
		t.Fatalf("beat: %s", errw)
	}
	// Counted directly: `who` reads under the revival's token, so a row written
	// under the ended incarnation would not show on its line.
	db, err := sql.Open("sqlite", "file:"+filepath.Join(f.repo, ".git", "buddy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_browser`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("a beat for an ended session wrote a row: %d %v", rows, err)
	}
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-o", f.repo, "", ""), "hello", "--label", "orch"); code != 0 {
		t.Fatal(errw)
	}
	if got := exitOf(t, f, "orch"); strings.Contains(got, "browser") {
		t.Fatalf("a revival read a call its predecessor's beat raced in: %q", got)
	}
	// Positive control: the same beat on the live revival records.
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-o", f.repo, "mcp__playwright__browser_click", ""), "beat"); code != 0 {
		t.Fatalf("beat: %s", errw)
	}
	if got := exitOf(t, f, "orch"); !strings.Contains(got, "browser tools used") {
		t.Fatalf("control: %q", got)
	}
}

// A reading that cannot be had keeps "cannot say" apart from none, and costs
// neither the report nor a beat.
func TestBrowserReadingFailureCostsNothing(t *testing.T) {
	f := browserFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "msg", "orch", "still-delivered"); code != 0 {
		t.Fatalf("msg: %s", errw)
	}
	dropBrowserTable(t, f)
	if got := exitOf(t, f, "orch"); !strings.Contains(got, "; browser tools: cannot say") {
		t.Fatalf("cannot say: %q", got)
	}
	out, errw, code := f.run(t, f.repo, hookJSON("sess-o", f.repo, "mcp__playwright__browser_click", ""), "beat")
	if code != 0 {
		t.Fatalf("a failed reading must not cost the beat: %s", errw)
	}
	if !strings.Contains(out, "still-delivered") {
		t.Fatalf("a failed reading must not cost the inbox: %q", out)
	}
}

// dropBrowserTable breaks the one table, the rest of the ledger staying readable.
func dropBrowserTable(t *testing.T, f *fixture) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(f.repo, ".git", "buddy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`DROP TABLE session_browser`); err != nil {
		t.Fatal(err)
	}
}
