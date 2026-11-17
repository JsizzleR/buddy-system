package harness

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// D-199: the native identifiers a receipt only DISPLAYS were the server's
// strings verbatim, up to the 4 MiB frame bound, in every native_event and in
// the pending-approval list each later receipt repeats. Every such field is
// driven through its real source here with one table: a line break of each
// kind, a format character, a space, a value made only of controls and an
// oversized value come back on one line, one token, nonempty, within the cap;
// an ordinary id comes back byte-for-byte (the positive control: the fence is
// armed and leaves a real id alone). The ids the adapter is SENT BACK (the
// Codex thread and turn ids, a request id) are not fenced, and the round-trip
// test below holds that a hostile request id still answers its approval.

type nativeIDCase struct {
	name string
	raw  string // "" means the field's own ordinary value
}

func nativeIDCases() []nativeIDCase {
	return []nativeIDCase{
		{name: "ordinary"},
		{name: "newline", raw: "id-1\n{\"kind\":\"idle\"}\r\nnext"},
		{name: "u2028", raw: "id b c\u0085d"},
		{name: "format", raw: "id\x1b[2J‮di​"},
		{name: "space", raw: "id idle 0s"},
		{name: "controls-only", raw: "\x1b\x07‮"},
		{name: "oversized", raw: "id-" + strings.Repeat("x", 1<<20)},
		// The fence strips a zero-width space: the ordinary value plus one
		// fences INTO the ordinary value. A display-only id may collide (the
		// fence is not injective, by contract); a status compared against
		// an enum must not (collides is set per field below).
		{name: "collides", raw: "\u200b"},
	}
}

// checkNativeID asserts what every displayed id owes. cap is a LITERAL at
// each caller (D-198's lesson: against the constant, a raised cap survived).
// token says the field is Field-fenced (one token, never emptied); a status
// is Line-fenced and may hold a space.
func checkNativeID(t *testing.T, field, got, ordinary string, tc nativeIDCase, cap int, token bool) {
	t.Helper()
	if tc.raw == "" {
		if got != ordinary {
			t.Fatalf("%s: ordinary id changed: %q, want %q", field, got, ordinary)
		}
		return
	}
	if got == "" {
		t.Fatalf("%s: a present id became absent", field)
	}
	if tc.name == "collides" && !token && got == ordinary {
		t.Fatalf("%s: a hostile status fenced into the ordinary %q", field, ordinary)
	}
	if strings.ContainsAny(got, "\n\r  \u0085\x1b\x07‮​") {
		t.Fatalf("%s: not on one line or carries a control: %.200q", field, got)
	}
	if token && strings.ContainsAny(got, " \t") {
		t.Fatalf("%s: not one token: %.200q", field, got)
	}
	if len(got) > cap+len("…[truncated]") {
		t.Fatalf("%s: %d bytes, cap %d", field, len(got), cap)
	}
}

func nativeIDOr(tc nativeIDCase, ordinary string) string {
	switch tc.name {
	case "ordinary":
		return ordinary
	case "collides":
		return ordinary + tc.raw
	}
	return tc.raw
}

func codexIDBackend() *codexBackend {
	c := newCodex("unused", nil)
	c.nativeID, c.activeTurn, c.state = "thread-1", "turn-1", "busy"
	return c
}

func TestCodexCompactionItemIDFenced(t *testing.T) {
	const ordinary = "019a2b3c-item-compaction"
	for _, tc := range nativeIDCases() {
		t.Run(tc.name, func(t *testing.T) {
			c := codexIDBackend()
			p, _ := json.Marshal(map[string]any{"threadId": "thread-1", "turnId": "turn-1",
				"item": map[string]string{"id": nativeIDOr(tc, ordinary), "type": "contextCompaction"}})
			c.nativeEvent(codexEnvelope{Method: "item/completed", Params: p})
			e := codexEvent(t, c)
			if e.Kind != "compaction_completed" || e.TurnID != "turn-1" {
				t.Fatalf("compaction not reported: %+v", e)
			}
			checkNativeID(t, "item_id", e.ItemID, ordinary, tc, 128, true)
		})
	}
}

func TestCodexApprovalIDsFenced(t *testing.T) {
	const item, approval = "call_7Hq2item", "approval-019a2b3c"
	for _, tc := range nativeIDCases() {
		t.Run(tc.name, func(t *testing.T) {
			c := codexIDBackend()
			p, _ := json.Marshal(map[string]any{"threadId": "thread-1", "turnId": "turn-1",
				"itemId": nativeIDOr(tc, item), "approvalId": nativeIDOr(tc, approval), "command": "true"})
			c.nativeEvent(codexEnvelope{ID: json.RawMessage(`"req-1"`), Method: "item/commandExecution/requestApproval", Params: p})
			e := codexEvent(t, c)
			if e.Kind != "approval" || e.Approval == nil {
				t.Fatalf("approval not reported: %+v", e)
			}
			checkNativeID(t, "item_id", e.Approval.ItemID, item, tc, 128, true)
			checkNativeID(t, "native_approval_id", e.Approval.NativeApprovalID, approval, tc, 128, true)
			// The keys the decision is matched by stay exact.
			if string(e.Approval.ID) != `"req-1"` || e.Approval.TurnID != "turn-1" || e.Approval.NativeSessionID != "thread-1" {
				t.Fatalf("approval keys altered: %+v", e.Approval)
			}
		})
	}
	// An absent id stays absent: Field alone would print ∅ for it.
	c := codexIDBackend()
	p, _ := json.Marshal(map[string]any{"threadId": "thread-1", "turnId": "turn-1", "command": "true"})
	c.nativeEvent(codexEnvelope{ID: json.RawMessage(`7`), Method: "item/commandExecution/requestApproval", Params: p})
	if e := codexEvent(t, c); e.Approval == nil || e.Approval.ItemID != "" || e.Approval.NativeApprovalID != "" {
		t.Fatalf("absent ids rendered: %+v", e.Approval)
	}
}

func claudeIDBackend() *claudeBackend {
	c := newClaude("unused")
	c.nativeID, c.active = "native", "turn"
	c.proc = &claudeFakeProcess{write: func(context.Context, any) error { return nil }}
	return c
}

func claudeApprovalFrame(t *testing.T, c *claudeBackend, requestID, toolUseID string) Event {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"subtype": "can_use_tool", "tool_name": "Bash", "tool_use_id": toolUseID, "input": map[string]any{"command": "true"}})
	id, _ := json.Marshal(requestID)
	if err := c.permission(id, b); err != nil {
		t.Fatal(err)
	}
	e := <-c.events
	if e.Kind != "approval" || e.Approval == nil {
		t.Fatalf("approval not reported: %+v", e)
	}
	return e
}

func TestClaudeApprovalIDsFenced(t *testing.T) {
	const tool, request = "toolu_01ABCdef", "req-7f3c"
	for _, tc := range nativeIDCases() {
		t.Run(tc.name, func(t *testing.T) {
			// The request id is a KEY and stays plain (D-202: a hostile one
			// is refused, native_key_test.go); the tool_use_id varies.
			c := claudeIDBackend()
			e := claudeApprovalFrame(t, c, request, nativeIDOr(tc, tool))
			checkNativeID(t, "item_id", e.Approval.ItemID, tool, tc, 128, true)
			if e.Approval.NativeApprovalID != request {
				t.Fatalf("plain native_approval_id changed: %q", e.Approval.NativeApprovalID)
			}

			// The cancel names the same approval: the same fence.
			b, _ := json.Marshal(map[string]any{"type": "control_cancel_request", "request_id": request})
			if err := c.frame(b); err != nil {
				t.Fatal(err)
			}
			r := <-c.events
			if r.Kind != "approval_resolved" || r.Approval == nil {
				t.Fatalf("cancel not reported: %+v", r)
			}
			checkNativeID(t, "cancel item_id", r.Approval.ItemID, tool, tc, 128, true)
			if r.Approval.NativeApprovalID != request {
				t.Fatalf("cancel native_approval_id changed: %q", r.Approval.NativeApprovalID)
			}
		})
	}
}

// The tool_use_id Claude is answered with is the raw one, not the receipt's
// fenced copy. (The request id, a KEY, is refused outside nativeKey since
// D-202; D-199 kept it exact and let a hostile one round-trip.)
func TestClaudeHostileToolUseIDStillAnswered(t *testing.T) {
	for _, tc := range nativeIDCases()[1:] {
		t.Run(tc.name, func(t *testing.T) {
			c := claudeIDBackend()
			var wrote any
			c.proc = &claudeFakeProcess{write: func(_ context.Context, v any) error { wrote = v; return nil }}
			e := claudeApprovalFrame(t, c, "req-7f3c", tc.raw)
			if string(e.Approval.ID) != `"req-7f3c"` {
				t.Fatalf("request id altered: %s", e.Approval.ID)
			}
			d := ApprovalDecision{ID: e.Approval.ID, NativeSessionID: e.Approval.NativeSessionID, TurnID: e.Approval.TurnID, Decision: "deny"}
			if err := c.Approve(context.Background(), d); err != nil {
				t.Fatalf("decision echoing the receipt refused: %v", err)
			}
			out, _ := json.Marshal(wrote)
			want, _ := json.Marshal(tc.raw)
			if !strings.Contains(string(out), `"toolUseID":`+string(want)) {
				t.Fatalf("Claude not answered with its own tool_use_id: %.200s", out)
			}
		})
	}
}

func TestClaudeResultRefAndStatusFenced(t *testing.T) {
	const ref, reason = "5f0c6a2e-8d1b-4c3a-9e7f-1a2b3c4d5e6f", "completed"
	for _, tc := range nativeIDCases() {
		t.Run(tc.name, func(t *testing.T) {
			c := newClaude("unused")
			c.nativeID = "native"
			frame, _ := json.Marshal(map[string]any{"type": "result", "subtype": "success", "session_id": "native",
				"uuid": nativeIDOr(tc, ref), "is_error": false, "terminal_reason": nativeIDOr(tc, reason)})
			if err := c.frame(frame); err != nil {
				t.Fatal(err)
			}
			e := <-c.events
			if e.Kind != "result" {
				t.Fatalf("result not reported: %+v", e)
			}
			checkNativeID(t, "result_ref", e.ResultRef, ref, tc, 128, true)
			checkNativeID(t, "result_status", e.ResultStatus, reason, tc, 64, false)
			if tc.name != "ordinary" && e.ResultStatus != "unknown" {
				// A reason the fence would alter is stored as unknown, so no
				// hostile one reads as an enum value recovery trusts; its
				// fenced text rides in the error.
				t.Fatalf("hostile reason stored as %q, want unknown", e.ResultStatus)
			}
			if tc.name != "ordinary" && !strings.HasPrefix(e.Err, "native result: success/") {
				t.Fatalf("hostile reason not reported in the error: %.120q", e.Err)
			}
		})
	}
	// An absent reason still reads unknown (unchanged).
	c := newClaude("unused")
	c.nativeID = "native"
	if err := c.frame(json.RawMessage(`{"type":"result","subtype":"success","session_id":"native","uuid":"r","is_error":false}`)); err != nil {
		t.Fatal(err)
	}
	if e := <-c.events; e.ResultStatus != "unknown" || e.ResultRef != "r" {
		t.Fatalf("absent reason: %+v", e)
	}
}

// D-199, Codex's lite pass: a parsed RFC 3339 timestamp is unbounded in its
// fraction, and captured_at was diagnose's Detail whole.
func TestDiagnoseCapturedAtBounded(t *testing.T) {
	for _, tc := range []struct{ at, want string }{
		{"2026-10-05T00:00:00Z", "2026-10-05T00:00:00Z"},
		{"2026-10-05T00:00:00.123456789+05:30", "2026-10-05T00:00:00.123456789+05:30"},
		{"2026-10-05T00:00:00." + strings.Repeat("1", 10000) + "Z", ""},
	} {
		l, e := diagnosticFixture(t)
		e.CapturedAt = tc.at
		r := Diagnose("codex", &l, &e)
		if r.Evidence.State != "supplied_snapshot" {
			t.Fatalf("%.40q: evidence %+v", tc.at, r.Evidence)
		}
		if tc.want != "" && r.Evidence.Detail != tc.want {
			t.Fatalf("ordinary timestamp changed: %q", r.Evidence.Detail)
		}
		if len(r.Evidence.Detail) > 64+len("…[truncated]") {
			t.Fatalf("captured_at detail is %d bytes, cap 64", len(r.Evidence.Detail))
		}
	}
}

// The collision that matters: recovery treats a stored aborted_tools as
// terminal without reading the error, so a reason that fences INTO it must
// not be stored as it.
func TestClaudeStatusCannotFenceIntoRecoveryEnum(t *testing.T) {
	for _, reason := range []string{"aborted_tools\u200b", "aborted_streaming\u202e", "completed\u2060"} {
		c := newClaude("unused")
		c.nativeID = "native"
		frame, _ := json.Marshal(map[string]any{"type": "result", "subtype": "success", "session_id": "native",
			"uuid": "r", "is_error": false, "terminal_reason": reason})
		if err := c.frame(frame); err != nil {
			t.Fatal(err)
		}
		e := <-c.events
		var rec claudeRecovery
		rec.observe(Receipt{Identity: Identity{SessionID: "native"}, NativeEvent: &e})
		if e.ResultStatus != "unknown" || rec.turns[e.TurnID] == "result" {
			t.Fatalf("%q stored as %q, recovery reads %q", reason, e.ResultStatus, rec.turns[e.TurnID])
		}
	}
	// Positive control: the real aborted_tools is stored and read as terminal.
	c := newClaude("unused")
	c.nativeID = "native"
	if err := c.frame(json.RawMessage(`{"type":"result","subtype":"success","session_id":"native","uuid":"r","is_error":false,"terminal_reason":"aborted_tools"}`)); err != nil {
		t.Fatal(err)
	}
	e := <-c.events
	var rec claudeRecovery
	rec.observe(Receipt{Identity: Identity{SessionID: "native"}, NativeEvent: &e})
	if e.ResultStatus != "aborted_tools" || rec.turns[e.TurnID] != "result" {
		t.Fatalf("real aborted_tools: stored %q, recovery %q", e.ResultStatus, rec.turns[e.TurnID])
	}
}
