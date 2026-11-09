package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
		{"mcp__chrome-devtools__navigate_page", true},
		{"mcp__plugin_chrome-devtools-mcp_chrome-devtools__take_screenshot", true},
		{"mcp__browser-use__browser_navigate", true},

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
		{"mcp__chrome-devtools-notes__navigate_page", false},
		{"mcp__chrome-devtools__", false},
		{"mcp__plugin_chrome-devtools-mcp_docs__search", false},
		{"mcp__browser-use", false},
		{"mcp__browser-user__browser_click", false},

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

// install.sh's matcher report (and --wire-hooks, D-135) reads its own copy of
// the list, in Python, from scripts/gate-hook.py: a server added here and not
// there would be called excluded by every install.
func TestInstallKnowsEveryBrowserPrefix(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "scripts", "gate-hook.py"))
	if err != nil {
		t.Fatal(err)
	}
	if len(browserToolPrefixes) == 0 {
		t.Fatal("control: the list is empty")
	}
	for _, p := range browserToolPrefixes {
		if !strings.Contains(string(b), `"`+p+`"`) {
			t.Errorf("scripts/gate-hook.py does not name %s", p)
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

// eventJSON is a hook payload for one event, as Claude Code sends PreToolUse and
// PostToolUseFailure: the PostToolUse fields plus hook_event_name.
func eventJSON(t *testing.T, event, session, cwd, tool string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"session_id": session, "cwd": cwd, "hook_event_name": event,
		"tool_name": tool, "tool_input": map[string]any{}, "error": "boom",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A PreToolUse hook can refuse its call, on exit 2 or a decision on stdout (#71).
// So a beat for either event that is not a completed call exits 0 and prints
// nothing on every input. The control in each case: the browser call is recorded
// where the ledger can take it, and the inbox waits for the completed call.
func TestABeatBeforeOrAfterAFailedCallRecordsAndSaysNothing(t *testing.T) {
	for _, event := range []string{"PreToolUse", "PostToolUseFailure"} {
		t.Run(event, func(t *testing.T) {
			f := browserFixture(t)
			if _, errw, code := f.run(t, f.repo, "", "msg", "orch", "kept-for-later"); code != 0 {
				t.Fatalf("msg: %s", errw)
			}
			silent := func(what, stdin string) {
				t.Helper()
				out, errw, code := f.run(t, f.repo, stdin, "beat")
				if code != 0 || out != "" || errw != "" {
					t.Fatalf("%s: exit %d, stdout %q, stderr %q; want 0 and nothing", what, code, out, errw)
				}
			}

			// Not a browser's: nothing recorded, nothing said.
			silent("a native tool", eventJSON(t, event, "sess-o", f.repo, "Bash"))
			silent("another server", eventJSON(t, event, "sess-o", f.repo, "mcp__buddylist__chat_read"))
			if got := exitOf(t, f, "orch"); strings.Contains(got, "browser") {
				t.Fatalf("a non-browser call was recorded: %q", got)
			}

			// A browser call: recorded, silently, and the inbox not drained.
			silent("a browser call", eventJSON(t, event, "sess-o", f.repo, "mcp__chrome-devtools__navigate_page"))
			if got := exitOf(t, f, "orch"); !strings.Contains(got, "browser tools used 0s ago") {
				t.Fatalf("control: the browser call was not recorded: %q", got)
			}
			out, errw, code := f.run(t, f.repo, hookJSON("sess-o", f.repo, "Bash", ""), "beat")
			if code != 0 || !strings.Contains(out, "kept-for-later") {
				t.Fatalf("the completed call's beat delivers what the others left: %d %q %s", code, out, errw)
			}

			// An unregistered session: nothing (an ended one: store/browser_test.go).
			silent("an unknown session", eventJSON(t, event, "sess-x", f.repo, "mcp__browser-use__browser_click"))

			// A table that cannot be written: swallowed.
			dropBrowserTable(t, f)
			silent("a failed write", eventJSON(t, event, "sess-o", f.repo, "mcp__playwright__browser_click"))
		})
	}
}

func TestABeatOffSuccessIsSilentWithNoOrABrokenLedger(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	for _, event := range []string{"PreToolUse", "PostToolUseFailure"} {
		// No ledger: the repository was never initialised.
		in := eventJSON(t, event, "sess-o", f.repo, "mcp__playwright__browser_click")
		if out, errw, code := f.run(t, f.repo, in, "beat"); code != 0 || out != "" || errw != "" {
			t.Fatalf("%s, no ledger: %d %q %q", event, code, out, errw)
		}
		// Not a repository.
		dir := t.TempDir()
		if out, errw, code := f.run(t, dir, eventJSON(t, event, "sess-o", dir, "mcp__playwright__browser_click"), "beat"); code != 0 || out != "" || errw != "" {
			t.Fatalf("%s, no repository: %d %q %q", event, code, out, errw)
		}
	}
	// An unreadable ledger: written over with junk.
	common, err := exec.Command("git", "-C", f.repo, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(strings.TrimSpace(string(common)), "buddy.db"), []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"PreToolUse", "PostToolUseFailure"} {
		in := eventJSON(t, event, "sess-o", f.repo, "mcp__playwright__browser_click")
		if out, errw, code := f.run(t, f.repo, in, "beat"); code != 0 || out != "" || errw != "" {
			t.Fatalf("%s, unreadable ledger: %d %q %q", event, code, out, errw)
		}
	}
	// Control: the completed call's beat does see that ledger as unreadable.
	if _, _, code := f.run(t, f.repo, hookJSON("sess-o", f.repo, "mcp__playwright__browser_click", ""), "beat"); code == 0 {
		t.Fatal("control: a PostToolUse beat on a junk ledger exited 0, so the arms above proved nothing")
	}
}

// A payload beat cannot read never names its event, so beat cannot tell it was a
// PreToolUse: it fails as any beat does, non-zero, never 2, and with nothing on
// stdout; the hook line's `exit 0` (USAGE, below) then holds the call harmless.
func TestAMalformedPayloadPrintsNothingAndNeverExitsTwo(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	for _, in := range []string{`{"hook_event_name":"PreToolUse","session_id":`, `{"hook_event_name":"PreToolUse"}`, `[]`} {
		out, _, code := f.run(t, f.repo, in, "beat")
		if out != "" || code == 2 {
			t.Fatalf("%s: exit %d, stdout %q", in, code, out)
		}
	}
}

// A busy ledger holds the write for busy_timeout, then the beat gives up, saying
// nothing and exiting 0; the next call records as usual.
func TestABeatOffSuccessOnABusyLedgerGivesUpSilently(t *testing.T) {
	f := browserFixture(t)
	db, err := sql.Open("sqlite", "file:"+filepath.Join(f.repo, ".git", "buddy.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	in := eventJSON(t, "PreToolUse", "sess-o", f.repo, "mcp__claude-in-chrome__navigate")
	start := time.Now()
	out, errw, code := f.run(t, f.repo, in, "beat")
	took := time.Since(start)
	if code != 0 || out != "" || errw != "" {
		t.Fatalf("busy: %d %q %q", code, out, errw)
	}
	if took < 4*time.Second {
		t.Fatalf("control: the lock was not held (%v), so busy was not tested", took)
	}
	if _, err := conn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if got := exitOf(t, f, "orch"); strings.Contains(got, "browser") {
		t.Fatalf("a write under the lock landed: %q", got)
	}
	if _, errw, code := f.run(t, f.repo, in, "beat"); code != 0 {
		t.Fatalf("after: %s", errw)
	}
	if got := exitOf(t, f, "orch"); !strings.Contains(got, "browser tools used") {
		t.Fatalf("control: the free ledger records: %q", got)
	}
}

// USAGE's optional entries are the two lines the operator adds by hand. Each
// runs beat on mcp__.* and exits 0 whatever buddy does: run against a buddy that
// exits 2, the line still exits 0. Stdout passes through the line untouched, so
// that no beat prints a decision rests on beat's silence, tested above.
func TestUsagesOptionalBeatLinesNeverBlock(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "USAGE.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	at := strings.Index(text, "### The browser slot, and what `who` observes")
	if at < 0 {
		t.Fatal("no browser slot heading")
	}
	sec := text[at+1:]
	for _, next := range []string{"\n## ", "\n### "} {
		if i := strings.Index(sec, next); i >= 0 {
			sec = sec[:i]
		}
	}
	blocks := regexp.MustCompile("(?s)```json\n(.*?)\n```").FindAllStringSubmatch(sec, 1)
	if len(blocks) < 1 {
		t.Fatal("USAGE has no json block under the browser slot")
	}
	var opt map[string][]struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(blocks[0][1]), &opt); err != nil {
		t.Fatalf("the optional block: %v", err)
	}
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	ran := filepath.Join(home, "ran")
	fake := "#!/bin/sh\ncat >/dev/null\necho \"$1\" >> '" + ran + "'\necho oops >&2\nexit 2\n"
	if err := os.WriteFile(filepath.Join(bin, "buddy"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"PreToolUse", "PostToolUseFailure"} {
		es := opt[event]
		if len(es) != 1 || es[0].Matcher != "mcp__.*" || len(es[0].Hooks) != 1 {
			t.Fatalf("%s: want one entry on mcp__.*, got %+v", event, es)
		}
		cmd := exec.Command("sh", "-c", es[0].Hooks[0].Command)
		cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
		cmd.Stdin = strings.NewReader("{}")
		var out strings.Builder
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s: the line did not exit 0 over a buddy that exits 2: %v", event, err)
		}
		if out.String() != "" {
			t.Fatalf("%s: stdout %q", event, out.String())
		}
	}
	// Control: the fake ran, once per line, as `beat`.
	if got, err := os.ReadFile(ran); err != nil || string(got) != "beat\nbeat\n" {
		t.Fatalf("control: the lines did not run buddy beat: %q %v", got, err)
	}
}
