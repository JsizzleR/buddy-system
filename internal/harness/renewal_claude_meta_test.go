package harness

// R', the 2026-10-14 tool_result_meta rule (D-078 addendum), arm by arm. The
// table is testdata/claude-tool-result-meta.json's `arms`, which
// scripts/check-claude-gate.py reads too (meta_self_test), so the Go reader
// and the checker's mirror are held to one table. Rows marked `measured` are
// shapes a native build sent; the rest probe the edges of each arm, and every
// arm has a row on each side of it.

import (
	"encoding/json"
	"strings"
	"testing"
)

type claudeMetaArm struct {
	Name    string `json:"name"`
	Results []struct {
		ID      string          `json:"id"`
		IsError json.RawMessage `json:"is_error"`
	} `json:"results"`
	ID       string          `json:"id"`
	Meta     json.RawMessage `json:"meta"`
	Verdict  string          `json:"verdict"`
	Measured string          `json:"measured"`
}

// claudeMetaVerdictFor is the whole rule for one result: the frame-level read
// (R' 0 and 1), then the result's entry (R' 2).
func claudeMetaVerdictFor(meta json.RawMessage, results map[string]bool, id string, isError json.RawMessage) (string, string) {
	m, bad := readClaudeResultMeta(meta, results)
	if bad != "" {
		return "unknown", bad
	}
	v, why := m.verdict(id, isError)
	return map[claudeMetaVerdict]string{claudeMetaProceed: "proceed", claudeMetaUnexecuted: "unexecuted", claudeMetaUnknown: "unknown"}[v], why
}

func TestClaudeMetaArms(t *testing.T) {
	raw, _ := loadClaudeMetaFixture(t)
	seen := map[string]int{}
	measured := 0
	for _, r := range raw {
		var arm claudeMetaArm
		if err := json.Unmarshal(r, &arm); err != nil {
			t.Fatal(err)
		}
		seen[arm.Verdict]++
		if arm.Measured != "" {
			measured++
		}
		t.Run(arm.Name, func(t *testing.T) {
			results := map[string]bool{}
			var isError json.RawMessage
			for _, res := range arm.Results {
				if res.ID != "" {
					results[res.ID] = true
				}
				if res.ID == arm.ID {
					isError = res.IsError
				}
			}
			got, why := claudeMetaVerdictFor(arm.Meta, results, arm.ID, isError)
			if got != arm.Verdict {
				t.Fatalf("verdict %s (%s), want %s", got, why, arm.Verdict)
			}
			if (got == "unknown") != (why != "") {
				t.Fatalf("an unknown verdict must carry its reason and no other may: %s %q", got, why)
			}
		})
	}
	if seen["proceed"] < 5 || seen["unexecuted"] < 5 || seen["unknown"] < 20 || measured < 6 {
		t.Fatalf("the arms table lost rows: %v, %d measured", seen, measured)
	}
}

// The measured frames, read result by result: each verdict the fixture
// records (the checker's mirror computed it) is the Go reader's too.
func TestClaudeMetaVerdictsOfMeasuredFrames(t *testing.T) {
	_, rows := loadClaudeMetaFixture(t)
	for _, row := range rows {
		frame, _ := json.Marshal(row.Frame)
		var f struct {
			Message struct {
				Content []struct {
					Type      string          `json:"type"`
					ToolUseID string          `json:"tool_use_id"`
					IsError   json.RawMessage `json:"is_error"`
				} `json:"content"`
			} `json:"message"`
			Meta json.RawMessage `json:"tool_result_meta"`
		}
		if err := json.Unmarshal(frame, &f); err != nil {
			t.Fatal(err)
		}
		results := map[string]bool{}
		for _, b := range f.Message.Content {
			if b.Type == "tool_result" && b.ToolUseID != "" {
				results[b.ToolUseID] = true
			}
		}
		if len(row.Verdicts) != len(results) {
			t.Fatalf("%s: %d verdicts for %d results", row.Source, len(row.Verdicts), len(results))
		}
		// The replay runs each frame alone, after only its own tool uses. That
		// loses nothing only while no measured frame takes the frame-level
		// path (R' 0 and 1): it is the one reading whose effect reaches past
		// its own frame, reporting coverage unknown for a use another frame
		// left outstanding (Codex code review, finding 4). Per-result verdicts
		// depend on nothing but the frame and the result's own correlation.
		if _, bad := readClaudeResultMeta(f.Meta, results); bad != "" {
			t.Errorf("%s: a measured frame takes the frame-level path (%s); replay it in its stream, not alone", row.Source, bad)
		}
		for _, b := range f.Message.Content {
			got, why := claudeMetaVerdictFor(f.Meta, results, b.ToolUseID, b.IsError)
			if got != row.Verdicts[b.ToolUseID] {
				t.Errorf("%s %s: Go reads %s (%s), the checker's mirror %s", row.Source, b.ToolUseID, got, why, row.Verdicts[b.ToolUseID])
			}
		}
	}
}

// End to end, through the backend: what each verdict does to a recorded
// main-thread Write. The positive controls are the rows that renew.
func TestClaudeMetaVerdictOutcomes(t *testing.T) {
	const path = "/w/claimed.txt"
	use := map[string]any{"type": "assistant", "session_id": compactSession, "uuid": "a", "parent_tool_use_id": nil,
		"message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": "u1", "name": "Write", "input": map[string]any{"file_path": path}}}}}
	witness := map[string]any{"type": "create", "filePath": path}
	result := func(isError any, meta any) map[string]any {
		block := map[string]any{"type": "tool_result", "tool_use_id": "u1"}
		if isError != "omit" {
			block["is_error"] = isError
		}
		f := map[string]any{"type": "user", "session_id": compactSession, "uuid": "r", "parent_tool_use_id": nil, "tool_use_result": witness,
			"message": map[string]any{"role": "user", "content": []any{block}}}
		if meta != "omit" {
			f["tool_result_meta"] = meta
		}
		return f
	}
	withBlock := func(f map[string]any, block map[string]any) map[string]any {
		msg := f["message"].(map[string]any)
		msg["content"] = append(msg["content"].([]any), block)
		return f
	}
	accept := []any{map[string]any{"id": "u1", "permission_decision": map[string]any{"decision": "accept", "source": "user_temporary"}}}
	denied := []any{map[string]any{"id": "u1", "non_execution_kind": "permission-rule"}}
	for _, tc := range []struct {
		name   string
		frame  map[string]any
		renews bool
		lost   bool
		reason string
	}{
		{"accepted, explicit false: renews", result(false, accept), true, false, ""},
		{"accepted, omitted, the 2.1.291 witness: renews", result("omit", accept), true, false, ""},
		{"accepted, is_error true: renews nothing", result(true, accept), false, false, ""},
		{"denied, is_error true: renews nothing and says nothing", result(true, denied), false, false, ""},
		// The kind outranks a witness: not measured, but it is R's reading.
		{"denied, is_error omitted, with a witness: renews nothing", result("omit", denied), false, false, ""},
		{"denied beside explicit false: unknown", result(false, denied), false, true, "did not establish whether it ran"},
		{"an id-only entry beside explicit false: unknown", result(false, []any{map[string]any{"id": "u1"}}), false, true, "did not establish whether it ran"},
		{"an accept carrying remedy: unknown", result(false, []any{map[string]any{"id": "u1", "remedy": "x",
			"permission_decision": map[string]any{"decision": "accept"}}}), false, true, "unmeasured shape"},
		{"two entries: unknown", result(false, append(append([]any{}, accept...), accept...)), false, true, "more than one"},
		{"a permission_decision that is not an object: unknown", result(false, []any{map[string]any{"id": "u1",
			"permission_decision": "accept"}}), false, true, "unmeasured shape"},
		{"an entry naming no result of the frame: unknown", result(false, []any{map[string]any{"id": "u9",
			"non_execution_kind": "permission-rule"}}), false, true, "naming no tool_result of its frame"},
		// A result block with no tool_use_id is not a result an entry can name.
		{"an empty id beside an id-less block: unknown", withBlock(result(false, []any{map[string]any{"id": "",
			"non_execution_kind": "permission-rule"}}), map[string]any{"type": "tool_result"}), false, true, "naming no tool_result of its frame"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, m := claudeRenewalBackend("2.1.291")
			for _, f := range []map[string]any{use, tc.frame} {
				if err := sendCompactFrame(t, b, f); err != nil {
					t.Fatal(err)
				}
			}
			batch, gap, lost, _ := m.take("gen-1", func(string) bool { return true }, renewBatchMax)
			if (len(batch) == 1 && batch[0] == path) != tc.renews || len(batch) > 1 {
				t.Fatalf("renewed %q, want renews=%v", batch, tc.renews)
			}
			if lost != tc.lost || !strings.Contains(gap, tc.reason) {
				t.Fatalf("coverage lost=%v (%q), want %v (%q)", lost, gap, tc.lost, tc.reason)
			}
		})
	}
}
