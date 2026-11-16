package harness

import (
	"encoding/json"
	"strings"
	"testing"
)

// D-198: a native server's error text became a receipt's error verbatim, up to
// the 4 MiB frame bound, on the journal and the coordinator's stream. Every
// source that splices server text into an error is driven here with the same
// table: a line break of each kind and an oversized value come back on one
// line within the cap, and an ordinary value is unchanged (the positive
// control: the fence is armed and does not eat what the operator needs).

type nativeErrorCase struct {
	name     string
	server   string // the server-supplied part
	ordinary bool   // unchanged apart from the bound
}

func nativeErrorCases() []nativeErrorCase {
	return []nativeErrorCase{
		{name: "ordinary", server: `{"code":-32600,"message":"thread not loaded"}`, ordinary: true},
		{name: "newline", server: "{\"code\":1,\"message\":\"a\"}\n{\"fake\":\"receipt\"}\r\nnext"},
		{name: "u2028", server: "{\"message\":\"a\u2028b\u2029c\u0085d\"}"},
		{name: "oversized", server: `{"message":"` + strings.Repeat("x", 1<<20) + `"}`},
		{name: "escape", server: "{\"message\":\"\x1b[2J\u202ereversed\"}"},
	}
}

// checkNativeError asserts the shape every case owes: the prefix kept, one
// line, no control or format character, and the server's part within cap.
// Callers pass the cap as a LITERAL: against the constant, a mutant raising
// nativeErrorCap to the frame bound survived.
func checkNativeError(t *testing.T, got, prefix string, tc nativeErrorCase, cap int) {
	t.Helper()
	if !strings.HasPrefix(got, prefix) {
		t.Fatalf("prefix lost: %.120q", got)
	}
	if tc.ordinary {
		if got != prefix+tc.server {
			t.Fatalf("ordinary error changed: %q", got)
		}
		return
	}
	if strings.ContainsAny(got, "\n\r\u2028\u2029\u0085\x1b\u202e") {
		t.Fatalf("error not on one line or carries a control: %.200q", got)
	}
	if n := len(got) - len(prefix); n > cap+len("…[truncated]") {
		t.Fatalf("server part is %d bytes, cap %d", n, cap)
	}
}

func TestCodexRPCErrorFencedAndBounded(t *testing.T) {
	for _, tc := range nativeErrorCases() {
		t.Run(tc.name, func(t *testing.T) {
			e := &codexRPCError{raw: json.RawMessage(tc.server)}
			checkNativeError(t, e.Error(), "Codex RPC rejected request: ", tc, 512)
			if string(e.raw) != tc.server {
				t.Fatal("raw reply was altered; only its rendering is fenced")
			}
		})
	}
}

func TestClaudeControlErrorFencedAndBounded(t *testing.T) {
	for _, tc := range nativeErrorCases() {
		t.Run(tc.name, func(t *testing.T) {
			c := newClaude("unused")
			ch := make(chan claudeReply, 1)
			c.waiters["req-1"] = ch
			frame, _ := json.Marshal(map[string]any{"type": "control_response",
				"response": map[string]any{"subtype": "error", "request_id": "req-1", "error": tc.server}})
			if err := c.frame(frame); err != nil {
				t.Fatal(err)
			}
			reply := <-ch
			if reply.err == nil {
				t.Fatal("refused control request reported success")
			}
			checkNativeError(t, reply.err.Error(), "Claude refused control request: ", tc, 512)
		})
	}
}

func TestClaudeResultErrorFencedAndBounded(t *testing.T) {
	for _, tc := range nativeErrorCases() {
		t.Run(tc.name, func(t *testing.T) {
			// Identifiers, not JSON: the ordinary case is a real one. The two
			// operands differ, so rendering one of them twice fails (Codex's
			// lite pass: identical operands let that through).
			subtype, reason := tc.server, "r"+tc.server
			if tc.ordinary {
				subtype, reason = "error_during_execution", "aborted_tools"
			}
			c := newClaude("unused")
			c.nativeID = "native"
			frame, _ := json.Marshal(map[string]any{"type": "result", "subtype": subtype, "session_id": "native",
				"uuid": "r", "is_error": true, "terminal_reason": reason})
			if err := c.frame(frame); err != nil {
				t.Fatal(err)
			}
			e := <-c.events
			if e.Kind != "result" || e.Err == "" {
				t.Fatalf("error result not reported: %+v", e)
			}
			tc.server = subtype + "/" + reason
			checkNativeError(t, e.Err, "native result: ", tc, 2*64+1+len("…[truncated]"))
		})
	}
}

// The settings checker names the settings' own keys and hook events in its
// refusals, from the native get_settings reply or the worker's settings files.
func TestClaudeSettingsErrorFencedAndBounded(t *testing.T) {
	for _, tc := range nativeErrorCases() {
		t.Run(tc.name, func(t *testing.T) {
			// An unsupported hook event is rendered `%s`, unquoted.
			event := tc.server
			if tc.ordinary {
				event = "PostToolUse"
			}
			if len(event) > 64<<10 {
				// Past 128 KiB the checker refuses the whole input first.
				event = event[:64<<10]
			}
			raw, _ := json.Marshal(map[string]any{"hooks": map[string]any{event: []any{}}})
			err := claudeSettingsCompatibility(raw, settingsGate)
			if err == nil {
				t.Fatal("unsupported hook event admitted")
			}
			prefix := "unsupported Claude hook "
			got := err.Error()
			tc.server = event
			if tc.ordinary {
				tc.server += ": controller owns lifecycle translation"
			}
			if !tc.ordinary {
				got = strings.TrimSuffix(got, ": controller owns lifecycle translation")
			}
			checkNativeError(t, got, prefix, tc, 512)
		})
	}
}
