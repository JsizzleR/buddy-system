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
	ID   string          `json:"id"`
	Meta json.RawMessage `json:"meta"`
	// Witness: the frame carries the Write success witness for the judged
	// result (create/update naming the recorded path, one result block).
	Witness  bool   `json:"witness"`
	Verdict  string `json:"verdict"`
	Measured string `json:"measured"`
}

// claudeMetaVerdictFor is the whole rule for one result: the frame-level read
// (R' 0 and 1), then the result's entry (R' 2).
func claudeMetaVerdictFor(meta json.RawMessage, results map[string]bool, id string, isError json.RawMessage, witness bool) (string, string) {
	m, bad := readClaudeResultMeta(meta, results)
	if bad != "" {
		return "unknown", bad
	}
	v, why := m.verdict(id, isError, witness)
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
			got, why := claudeMetaVerdictFor(arm.Meta, results, arm.ID, isError, arm.Witness)
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
//
// And no measured result takes the witness arm (a non-execution kind beside
// an omitted is_error and the Write success witness, unknown since the
// 2026-10-14 decision; R' as agreed read it unexecuted): every verdict is the
// same with the witness as without it. That is what lets the arm change
// without moving a measured outcome (D-079's no-bump reading). The witness
// count is the positive control: the witness is computed here, and found on
// every measured successful Write.
func TestClaudeMetaVerdictsOfMeasuredFrames(t *testing.T) {
	_, rows := loadClaudeMetaFixture(t)
	witnessed := 0
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
			Meta          json.RawMessage `json:"tool_result_meta"`
			ToolUseResult json.RawMessage `json:"tool_use_result"`
		}
		if err := json.Unmarshal(frame, &f); err != nil {
			t.Fatal(err)
		}
		results, blocks := map[string]bool{}, 0
		for _, b := range f.Message.Content {
			if b.Type == "tool_result" {
				blocks++
				if b.ToolUseID != "" {
					results[b.ToolUseID] = true
				}
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
			witness := false
			for _, u := range row.Uses {
				if u.ID == b.ToolUseID && u.Name == "Write" {
					witness = blocks == 1 && claudeWriteWitness(f.ToolUseResult, u.Path)
				}
			}
			if witness {
				witnessed++
			}
			got, why := claudeMetaVerdictFor(f.Meta, results, b.ToolUseID, b.IsError, witness)
			if got != row.Verdicts[b.ToolUseID] {
				t.Errorf("%s %s: Go reads %s (%s), the checker's mirror %s", row.Source, b.ToolUseID, got, why, row.Verdicts[b.ToolUseID])
			}
			if bare, _ := claudeMetaVerdictFor(f.Meta, results, b.ToolUseID, b.IsError, false); bare != got {
				t.Errorf("%s %s: a measured result takes the witness arm (%s without the witness, %s with it)", row.Source, b.ToolUseID, bare, got)
			}
		}
	}
	// 30 successful main-thread Writes were measured: 19 on 2.1.291, 11 on 2.1.299.
	if witnessed < 30 {
		t.Fatalf("the success witness was found on %d measured results, want at least 30", witnessed)
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
	withWitness := func(f map[string]any, w any) map[string]any {
		f["tool_use_result"] = w
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
		// A witness beside a kind is contradictory evidence, like explicit
		// false beside one: unknown (the 2026-10-14 decision; R' as agreed
		// read it unexecuted, silently). Not measured. Its controls: without
		// the witness, with a witness of another path, or beside is_error
		// true (the row above, which carries the witness too), the kind
		// stands and nothing is reported.
		{"denied, is_error omitted, with a witness: unknown", result("omit", denied), false, true, "did not establish whether it ran"},
		{"denied, is_error omitted, no witness: renews nothing and says nothing", withWitness(result("omit", denied), "<str>"), false, false, ""},
		{"denied, is_error omitted, a witness of another path: says nothing", withWitness(result("omit", denied),
			map[string]any{"type": "create", "filePath": "/w/other.txt"}), false, false, ""},
		{"user-rejected, is_error omitted, with a witness: unknown", result("omit", []any{map[string]any{"id": "u1",
			"non_execution_kind": "user-rejected"}}), false, true, "did not establish whether it ran"},
		{"the 2.1.299 gate denial, is_error omitted, with a witness: unknown", result("omit", []any{map[string]any{"id": "u1",
			"non_execution_kind": "permission-rule", "permission_decision": map[string]any{"decision": "reject", "source": "hook", "reason_type": "hook"}}}),
			false, true, "did not establish whether it ran"},
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
