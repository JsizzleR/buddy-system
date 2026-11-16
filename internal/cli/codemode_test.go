package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// Code Mode's code output (D-195, issue #107). Measured on Claude Code
// 2.1.325: background code whose output lands after the turn's Stop opens a
// turn in which no hook's context reaches the model. A message queued after
// that Stop was marked delivered there by the late inner call's beat (2 of 2),
// and busy's context on the poll_event is discarded too (a marker emitted on
// every one never reached the transcript). The payload shapes below are the
// recorded ones, trimmed.

// codeHook is a hook payload for sess-b with the Code Mode fields set: a tool
// call's tool_use_id and tool_response, or a prompt's source.
func codeHook(f *fixture, tool string, toolUseID, resp, source any) string {
	in := map[string]any{"session_id": "sess-b", "cwd": f.wtB, "tool_name": tool,
		"tool_input": map[string]any{}}
	if toolUseID != nil && toolUseID != "" {
		in["tool_use_id"] = toolUseID
	}
	if resp != nil {
		in["tool_response"] = resp
	}
	if source != nil && source != "" {
		in["source"] = source
		in["prompt"] = `<event kind="repl-eval" at="2026-11-16T05:27:40.443Z" eval="4" status="late">` +
			"\n<log>\"BG-DONE-4\"</log>\n</event>"
	}
	b, _ := json.Marshal(in)
	return string(b)
}

func TestCodeModeOutputDrainsNothing(t *testing.T) {
	boundedParallel(t)
	bashOut := map[string]any{"stdout": "BG-DONE-4", "stderr": "", "interrupted": false,
		"isImage": false, "noOutputExpected": false}
	for _, tc := range []struct {
		name       string
		verb, tool string
		useID      any
		resp       any
		source     any
		drains     bool
	}{
		// The two late PostToolUse events of a measured loss, in their order.
		{"a late inner call", "beat", "Bash", "repl_1257ad16-8138-417a-a0ea-d38dd9267a6a", bashOut, "", false},
		{"the REPL call re-reported late", "beat", "REPL", "toolu_016AnszDgPQVBzu9gkSqCgDu",
			map[string]any{"code": "$`...`.then(self.log)", "result": nil, "stdout": `"BG-DONE-4"`,
				"stderr": "", "evalId": 4, "late": true}, "", false},
		{"a poll event", "busy", "", "", nil, "poll_event", false},
		// Positive controls: the main thread's own events, and shapes that
		// must not trip the guard.
		{"a REPL call settling in its turn", "beat", "REPL", "toolu_01EnaXWoj7AjvjYdAG4",
			map[string]any{"code": "...", "result": "JS-DONE", "stdout": "", "stderr": ""}, "", true},
		{"an ordinary call", "beat", "Bash", "toolu_01X5UiycW9Htk98Dyb19xvAp", bashOut, "", true},
		{"late is false", "beat", "REPL", "toolu_01", map[string]any{"late": false}, "", true},
		{"late is not a bool", "beat", "REPL", "toolu_01", map[string]any{"late": "yes"}, "", true},
		{"a tool response that is a string", "beat", "Read", "toolu_01", "file text", "", true},
		{"a tool response that is an array", "beat", "mcp__x__y", "toolu_01",
			[]any{map[string]any{"type": "text", "text": "late"}}, "", true},
		{"repl_ elsewhere in the id", "beat", "Bash", "toolu_repl_01", bashOut, "", true},
		// Codex code pass: the late arm is the REPL tool's only, and a field
		// of another shape decodes as no marker rather than failing the hook.
		{"late on another tool", "beat", "mcp__x__y", "toolu_01", map[string]any{"late": true}, "", true},
		{"a tool_use_id that is not a string", "beat", "Bash", 42, bashOut, "", true},
		{"a source that is not a string", "busy", "", "", nil, map[string]any{"kind": "poll_event"}, true},
		{"a typed prompt", "busy", "", "", nil, "", true},
		{"a prompt with another source", "busy", "", "", nil, "startup", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.initAndHello(t)
			const body = "R-107: queued after the Stop"
			if _, errw, code := f.run(t, f.repo, "", "msg", "bravo", body); code != 0 {
				t.Fatal(errw)
			}
			seenBefore := sessionRow(t, f, "bravo")
			f.clock = f.clock.Add(10 * time.Minute)
			out, errw, code := f.run(t, f.wtB, codeHook(f, tc.tool, tc.useID, tc.resp, tc.source), tc.verb)
			if code != 0 {
				t.Fatalf("%s: exit %d: %s", tc.verb, code, errw)
			}
			if tc.drains {
				if !strings.Contains(additionalContext(t, out), body) {
					t.Fatalf("the main thread's %s must deliver the queued message:\n%s", tc.verb, out)
				}
				if n := f.undeliveredTo(t, "sess-b", "bravo"); n != 0 {
					t.Fatalf("delivered, so marked: %d still queued", n)
				}
				return
			}
			// Nothing at all is written: a document there is discarded.
			if out != "" {
				t.Fatalf("code output must write nothing, got:\n%s", out)
			}
			if n := f.undeliveredTo(t, "sess-b", "bravo"); n != 1 {
				t.Fatalf("code output must leave the message queued, %d queued", n)
			}
			// Still the session's heartbeat: a late call is the session's work.
			if tc.verb == "beat" {
				if row := sessionRow(t, f, "bravo"); row == seenBefore || !strings.Contains(row, "seen 0s") {
					t.Fatalf("a late call is still the session's heartbeat:\nbefore %s\nafter  %s", seenBefore, row)
				}
			}
			// Positive control: the same session's next main-thread call
			// delivers it, so the silence above was the guard, not an empty
			// inbox.
			if ctx := f.beatB(t); !strings.Contains(ctx, body) {
				t.Fatalf("the next main-thread beat must deliver what code output left queued:\n%s", ctx)
			}
		})
	}
}

// busy on a poll event returns before the handoff note too: HandoffTell
// claims its step while the note is composed, so a discarded document would
// silence the line for that step for good.
func TestPollEventKeepsTheHandoffLine(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.observeB(t, 507_000)
	f.env[EnvHandoffAt] = "500k"
	out, errw, code := f.run(t, f.wtB, codeHook(f, "", "", nil, "poll_event"), "busy")
	if code != 0 {
		t.Fatal(errw)
	}
	if out != "" {
		t.Fatalf("a poll event must write nothing, got:\n%s", out)
	}
	if ctx := additionalContext(t, f.busyB(t)); !strings.Contains(ctx, handoffMark) {
		t.Fatalf("the next typed prompt must still carry the handoff line:\n%s", ctx)
	}
}

// The poll event still retracts the idle mark: the session is working.
func TestPollEventRetractsTheIdleMark(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	db := filepath.Join(f.repo, ".git", "buddy.db")
	isIdle := func() bool {
		st, err := store.Open(db, func() time.Time { return f.clock })
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		idle, err := st.IdleSessions()
		if err != nil {
			t.Fatal(err)
		}
		_, ok := idle["sess-b"]
		return ok
	}
	st, err := store.Open(db, func() time.Time { return f.clock })
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkIdle("sess-b", f.clock, time.Time{}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	if !isIdle() {
		t.Fatal("control: sess-b must be idle before the poll event")
	}
	if _, errw, code := f.run(t, f.wtB, codeHook(f, "", "", nil, "poll_event"), "busy"); code != 0 {
		t.Fatal(errw)
	}
	if isIdle() {
		t.Fatal("a poll event must retract the idle mark")
	}
}

// A session on both ledgers (D-193): the poll event retracts the fleet's idle
// mark as well as the home's, as a typed prompt's busy does. The positive
// control is the Stop's own marks, read before the poll event.
func TestPollEventRetractsBothIdleMarks(t *testing.T) {
	boundedParallel(t)
	fe, _ := bothEnv(t)
	fe.hook(t, "hello", "sess-x", fe.repo)
	fe.clock = fe.clock.Add(time.Minute)
	if _, errw, code := fe.run(t, fe.repo, stopTranscript(t, fe, "sess-x", fe.clock), "idle"); code != 0 {
		t.Fatal(errw)
	}
	if !strings.Contains(homeRow(t, fe, "sess-x"), "idle") || !strings.Contains(fleetRow(t, fe, "sess-x"), "idle") {
		t.Fatalf("control: the Stop marks both: home %q fleet %q", homeRow(t, fe, "sess-x"), fleetRow(t, fe, "sess-x"))
	}
	in, _ := json.Marshal(map[string]any{"session_id": "sess-x", "cwd": fe.repo,
		"hook_event_name": "UserPromptSubmit", "source": "poll_event", "prompt": "<event/>"})
	out, errw, code := fe.run(t, fe.repo, string(in), "busy")
	if code != 0 || out != "" {
		t.Fatalf("a poll event must write nothing: exit %d out %q err %s", code, out, errw)
	}
	if h, f := homeRow(t, fe, "sess-x"), fleetRow(t, fe, "sess-x"); strings.Contains(h, "idle") || strings.Contains(f, "idle") {
		t.Fatalf("a poll event must retract both marks: home %q fleet %q", h, f)
	}
}

// The new fields are raw so a value of another shape cannot fail the shared
// decode, which in the gate is a DENY (Codex code pass). The positive control
// is a field that IS typed failing the same way: the gate does refuse a
// payload it cannot decode, so its silence here is the decode succeeding.
func TestGateDecodesCodeModeFieldsOfAnyShape(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	target := filepath.Join(f.wtB, "notes.md")
	for name, extra := range map[string]map[string]any{
		"source an object":     {"source": map[string]any{}},
		"tool_use_id a number": {"tool_use_id": 42},
		"tool_response a bool": {"tool_response": true},
	} {
		in := map[string]any{"session_id": "sess-b", "cwd": f.wtB, "tool_name": "Write",
			"tool_input": map[string]any{"file_path": target}}
		for k, v := range extra {
			in[k] = v
		}
		b, _ := json.Marshal(in)
		out, errw, code := f.run(t, f.wtB, string(b), "gate")
		if code != 0 || out != "" {
			t.Fatalf("%s: an unclaimed write must be allowed silently: exit %d out %q err %s", name, code, out, errw)
		}
	}
	bad, _ := json.Marshal(map[string]any{"session_id": "sess-b", "cwd": f.wtB, "tool_name": "Write",
		"tool_input": "not an object"})
	if out, _, _ := f.run(t, f.wtB, string(bad), "gate"); !strings.Contains(out, "deny") {
		t.Fatalf("control: a payload the gate cannot decode is refused: %q", out)
	}
}

// An unreadable or missing fleet's line is written at every typed prompt
// (D-191), and not on a poll event, where it would be discarded (Codex code
// pass). The control is the typed prompt beside it saying it.
func TestPollEventWritesNoFleetLine(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fe.env[EnvFleet] = "missing"
	in, _ := json.Marshal(map[string]any{"session_id": "lane-1", "cwd": fe.plain,
		"hook_event_name": "UserPromptSubmit", "source": "poll_event", "prompt": "<event/>"})
	if out, errw, code := fe.run(t, fe.plain, string(in), "busy"); code != 0 || out != "" {
		t.Fatalf("a poll event must write nothing: exit %d out %q err %s", code, out, errw)
	}
	if out, _, code := fe.hook(t, "busy", "lane-1", fe.plain); code != 0 || !strings.Contains(promptContext(t, out), "names no fleet ledger") {
		t.Fatalf("control: a typed prompt names the missing fleet: code %d %q", code, out)
	}
}
