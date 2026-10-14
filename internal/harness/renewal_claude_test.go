package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Each case feeds one attempt (assistant block and/or can_use_tool) and one
// result to an in-process backend, then reads what the mailbox received. The
// "renews" cases are each other case's positive control: the same fixture with
// the one property flipped.
func TestClaudeRenewalOnlyForAMatchedCompletedMainThreadWrite(t *testing.T) {
	const sid = "00000000-0000-4000-8000-000000000001"
	const path = "/w/claimed.txt"
	assistant := func(name, id string, input map[string]any, parent any) map[string]any {
		return map[string]any{"type": "assistant", "session_id": sid, "uuid": "a-" + id, "parent_tool_use_id": parent,
			"message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}}}}
	}
	result := func(id string, isError any, extra map[string]any) map[string]any {
		block := map[string]any{"type": "tool_result", "tool_use_id": id, "content": "done"}
		if isError != "omit" {
			block["is_error"] = isError
		}
		f := map[string]any{"type": "user", "session_id": sid, "uuid": "r-" + id, "parent_tool_use_id": nil,
			"message": map[string]any{"role": "user", "content": []any{block}}}
		for k, v := range extra {
			if v == "delete" {
				delete(f, k)
				continue
			}
			f[k] = v
		}
		return f
	}
	write := map[string]any{"file_path": path, "content": "x"}
	witness := map[string]any{"tool_use_result": map[string]any{"type": "update", "filePath": path}}
	for _, tc := range []struct {
		name    string
		version string
		frames  []map[string]any
		renews  []string
		lost    bool
	}{
		{"explicit false renews on every version", "2.1.289", []map[string]any{assistant("Edit", "u1", map[string]any{"file_path": path}, nil), result("u1", false, nil)}, []string{path}, false},
		{"is_error true renews nothing", "2.1.291", []map[string]any{assistant("Write", "u1", write, nil), result("u1", true, witness)}, nil, false},
		{"omitted is_error with the 2.1.291 Write witness renews", "2.1.291", []map[string]any{assistant("Write", "u1", write, nil), result("u1", "omit", witness)}, []string{path}, false},
		{"witness naming another path renews nothing", "2.1.291", []map[string]any{assistant("Write", "u1", write, nil),
			result("u1", "omit", map[string]any{"tool_use_result": map[string]any{"type": "update", "filePath": "/w/other.txt"}})}, nil, true},
		{"a witness of another kind renews nothing", "2.1.291", []map[string]any{assistant("Write", "u1", write, nil),
			result("u1", "omit", map[string]any{"tool_use_result": map[string]any{"type": "text", "filePath": path}})}, nil, true},
		{"a string tool_use_result is no witness", "2.1.291", []map[string]any{assistant("Write", "u1", write, nil),
			result("u1", "omit", map[string]any{"tool_use_result": "Error: Denied"})}, nil, true},
		{"omission is unqualified on 2.1.290", "2.1.290", []map[string]any{assistant("Write", "u1", write, nil), result("u1", "omit", witness)}, nil, true},
		{"omitted is_error with the 2.1.299 witness and its accept entry renews", "2.1.299", []map[string]any{assistant("Write", "u1", write, nil),
			result("u1", "omit", map[string]any{"tool_use_result": map[string]any{"type": "update", "filePath": path},
				"tool_result_meta": []any{map[string]any{"id": "u1", "permission_decision": map[string]any{"decision": "accept", "source": "user_temporary"}}}})},
			[]string{path}, false},
		{"omission is unqualified for Edit on 2.1.299 too", "2.1.299", []map[string]any{assistant("Edit", "u1", map[string]any{"file_path": path}, nil), result("u1", "omit", witness)}, nil, true},
		{"omission is unqualified for Edit", "2.1.291", []map[string]any{assistant("Edit", "u1", map[string]any{"file_path": path}, nil), result("u1", "omit", witness)}, nil, true},
		{"an unknown tool_use_id renews nothing", "2.1.291", []map[string]any{assistant("Write", "u1", write, nil), result("u2", false, nil)}, nil, false},
		{"a subagent's result renews nothing", "2.1.291", []map[string]any{assistant("Write", "u1", write, nil), result("u1", false, map[string]any{"parent_tool_use_id": "toolu_parent"})}, nil, false},
		{"a result without provenance is lost coverage", "2.1.291", []map[string]any{assistant("Write", "u1", write, nil), result("u1", false, map[string]any{"parent_tool_use_id": "delete"})}, nil, true},
		{"a subagent's tool_use is never recorded", "2.1.291", []map[string]any{assistant("Write", "u1", write, "toolu_parent"), result("u1", false, nil)}, nil, false},
		{"Bash renews nothing, even successful", "2.1.291", []map[string]any{assistant("Bash", "u1", map[string]any{"command": "sed -i s/a/b/ " + path}, nil), result("u1", false, nil)}, nil, false},
		{"a result native marks unexecuted renews nothing", "2.1.291", []map[string]any{assistant("Write", "u1", write, nil),
			result("u1", true, map[string]any{"tool_result_meta": []any{map[string]any{"id": "u1", "non_execution_kind": "permission-rule"}}})}, nil, false},
		// Explicit false says it completed; the kind says it never ran. R'
		// (Codex, consult round 2): a contradiction is unknown, not a skip.
		{"a non-execution kind beside explicit false is lost coverage", "2.1.291", []map[string]any{assistant("Write", "u1", write, nil),
			result("u1", false, map[string]any{"tool_result_meta": []any{map[string]any{"id": "u1", "non_execution_kind": "permission-rule"}}})}, nil, true},
		{"a replayed result renews nothing", "2.1.291", []map[string]any{assistant("Write", "u1", write, nil), result("u1", false, map[string]any{"isReplay": true})}, nil, false},
		{"a synthetic frame renews nothing", "2.1.291", []map[string]any{assistant("Write", "u1", write, nil), result("u1", false, map[string]any{"isSynthetic": true})}, nil, false},
		{"NotebookEdit names notebook_path", "2.1.289", []map[string]any{assistant("NotebookEdit", "u1", map[string]any{"notebook_path": "/w/n.ipynb"}, nil), result("u1", false, nil)}, []string{"/w/n.ipynb"}, false},
		{"a non-boolean is_error is lost coverage", "2.1.291", []map[string]any{assistant("Write", "u1", write, nil), result("u1", "false", nil)}, nil, true},
		{"a file tool naming no path is lost coverage", "2.1.291", []map[string]any{assistant("Write", "u1", map[string]any{"content": "x"}, nil), result("u1", false, nil)}, nil, true},
		{"one witness cannot vouch for two results", "2.1.291", []map[string]any{assistant("Write", "u1", write, nil), assistant("Write", "u2", map[string]any{"file_path": "/w/second.txt"}, nil),
			{"type": "user", "session_id": sid, "uuid": "r-both", "parent_tool_use_id": nil, "tool_use_result": map[string]any{"type": "update", "filePath": path},
				"message": map[string]any{"role": "user", "content": []any{
					map[string]any{"type": "tool_result", "tool_use_id": "u1", "content": "done"},
					map[string]any{"type": "tool_result", "tool_use_id": "u2", "content": "done"}}}}}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, m := claudeRenewalBackend(tc.version)
			for _, f := range tc.frames {
				if err := sendCompactFrame(t, b, f); err != nil {
					t.Fatalf("frame refused: %v", err)
				}
			}
			batch, gap, unreported, _ := m.take("gen-1", func(string) bool { return true }, renewBatchMax)
			if !reflect.DeepEqual(batch, tc.renews) {
				t.Fatalf("renewed %q, want %q", batch, tc.renews)
			}
			if unreported != tc.lost {
				t.Fatalf("coverage lost=%v (%q), want %v", unreported, gap, tc.lost)
			}
		})
	}
}

func claudeRenewalBackend(version string) (*claudeBackend, *renewalMailbox) {
	b := newClaude("")
	b.nativeID, b.version, b.active, b.generation = compactSession, version, "input", "gen-1"
	m := newRenewalMailbox()
	b.observeRenewals(m)
	return b, m
}

// The can_use_tool request is a second sighting of the same use. It records
// the use when no assistant block did, and a request naming a different input
// for an already-recorded id drops the correlation: neither spelling renews.
func TestClaudeRenewalCorrelatesThePermissionRequest(t *testing.T) {
	const path = "/w/claimed.txt"
	request := func(id string, p string) json.RawMessage {
		raw, _ := json.Marshal(map[string]any{"subtype": "can_use_tool", "tool_name": "Write", "tool_use_id": id,
			"input": map[string]any{"file_path": p, "content": "x"}})
		return raw
	}
	ok := map[string]any{"type": "user", "session_id": compactSession, "uuid": "r", "parent_tool_use_id": nil,
		"message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "u1", "is_error": false}}}}
	t.Run("request alone records the use", func(t *testing.T) {
		b, m := claudeRenewalBackend("2.1.291")
		if err := b.permission(json.RawMessage(`"perm-1"`), request("u1", path)); err != nil {
			t.Fatal(err)
		}
		if err := sendCompactFrame(t, b, ok); err != nil {
			t.Fatal(err)
		}
		if batch, _, _, _ := m.take("gen-1", func(string) bool { return true }, renewBatchMax); !reflect.DeepEqual(batch, []string{path}) {
			t.Fatalf("renewed %q", batch)
		}
	})
	t.Run("a conflicting second sighting drops the correlation", func(t *testing.T) {
		b, m := claudeRenewalBackend("2.1.291")
		frame := map[string]any{"type": "assistant", "session_id": compactSession, "uuid": "a", "parent_tool_use_id": nil,
			"message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": "u1", "name": "Write", "input": map[string]any{"file_path": path}}}}}
		if err := sendCompactFrame(t, b, frame); err != nil {
			t.Fatal(err)
		}
		if err := b.permission(json.RawMessage(`"perm-1"`), request("u1", "/w/elsewhere.txt")); err != nil {
			t.Fatal(err)
		}
		if err := sendCompactFrame(t, b, ok); err != nil {
			t.Fatal(err)
		}
		if batch, _, unreported, _ := m.take("gen-1", func(string) bool { return true }, renewBatchMax); len(batch) != 0 || !unreported {
			t.Fatalf("conflicting inputs renewed %q (lost=%v)", batch, unreported)
		}
	})
}

// Codex review of the F-26 code pass, findings 1, 2 and 6, each reproduced
// here before its fix. 1: a non-file sighting of a recorded id (a Bash
// can_use_tool naming the Write's id) left the Write correlated, and the Bash
// result renewed a file nothing wrote. 2: a rejected (subagent) result naming
// the id consumed the correlation, so the genuine main-thread result that
// followed renewed nothing and said nothing. 6: a result frame of an
// unexpected shape returned silently, with no coverage gap.
func TestClaudeRenewalReviewFindings(t *testing.T) {
	use := func(id, name string, input map[string]any) map[string]any {
		return map[string]any{"type": "assistant", "session_id": compactSession, "uuid": "a-" + id, "parent_tool_use_id": nil,
			"message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}}}}
	}
	ok := func(id string, parent any) map[string]any {
		return map[string]any{"type": "user", "session_id": compactSession, "uuid": "r-" + id, "parent_tool_use_id": parent,
			"message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "is_error": false}}}}
	}
	all := func(string) bool { return true }
	t.Run("a non-file sighting of a recorded id drops it", func(t *testing.T) {
		b, m := claudeRenewalBackend("2.1.291")
		if err := sendCompactFrame(t, b, use("T", "Write", map[string]any{"file_path": "/w/never.txt"})); err != nil {
			t.Fatal(err)
		}
		bash, _ := json.Marshal(map[string]any{"subtype": "can_use_tool", "tool_name": "Bash", "tool_use_id": "T", "input": map[string]any{"command": "true"}})
		if err := b.permission(json.RawMessage(`"perm-bash"`), bash); err != nil {
			t.Fatal(err)
		}
		if err := sendCompactFrame(t, b, ok("T", nil)); err != nil {
			t.Fatal(err)
		}
		if batch, _, lost, _ := m.take("gen-1", all, renewBatchMax); len(batch) != 0 || !lost {
			t.Fatalf("a Bash result renewed the Write it shared an id with: %q (lost=%v)", batch, lost)
		}
	})
	t.Run("a rejected result does not consume the correlation", func(t *testing.T) {
		b, m := claudeRenewalBackend("2.1.291")
		for _, f := range []map[string]any{use("T", "Edit", map[string]any{"file_path": "/w/a"}), ok("T", "toolu_parent"), ok("T", nil)} {
			if err := sendCompactFrame(t, b, f); err != nil {
				t.Fatal(err)
			}
		}
		if batch, _, _, _ := m.take("gen-1", all, renewBatchMax); !reflect.DeepEqual(batch, []string{"/w/a"}) {
			t.Fatalf("the main-thread result after a subagent's renewed %q", batch)
		}
	})
	for _, shape := range []struct {
		name  string
		frame map[string]any
	}{
		{"content is an object", map[string]any{"type": "user", "session_id": compactSession, "uuid": "r", "parent_tool_use_id": nil,
			"message": map[string]any{"content": map[string]any{"type": "tool_result", "tool_use_id": "T", "is_error": false}}}},
		{"tool_result_meta is not a list", map[string]any{"type": "user", "session_id": compactSession, "uuid": "r", "parent_tool_use_id": nil, "tool_result_meta": "T",
			"message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "T", "is_error": false}}}}},
	} {
		t.Run("unexpected shape: "+shape.name, func(t *testing.T) {
			b, m := claudeRenewalBackend("2.1.291")
			for _, f := range []map[string]any{use("T", "Edit", map[string]any{"file_path": "/w/a"}), shape.frame} {
				if err := sendCompactFrame(t, b, f); err != nil {
					t.Fatal(err)
				}
			}
			if batch, _, lost, _ := m.take("gen-1", all, renewBatchMax); len(batch) != 0 || !lost {
				t.Fatalf("an unexpected result shape renewed %q or lost no coverage (lost=%v)", batch, lost)
			}
		})
	}
	t.Run("a plain text user frame is not a gap", func(t *testing.T) {
		b, m := claudeRenewalBackend("2.1.291")
		text := map[string]any{"type": "user", "session_id": compactSession, "uuid": "r", "parent_tool_use_id": nil, "message": map[string]any{"content": "hello"}}
		for _, f := range []map[string]any{use("T", "Edit", map[string]any{"file_path": "/w/a"}), text} {
			if err := sendCompactFrame(t, b, f); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, lost, _ := m.take("gen-1", all, renewBatchMax); lost {
			t.Fatal("a text user frame was reported as lost coverage")
		}
	})
}

// A correlation belongs to the backend that saw the tool_use, so it cannot
// cross a resume (a fresh backend), and a turn's end discards uses that never
// got a result: a late result for one renews nothing.
func TestClaudeRenewalNeverPairsAcrossLaunchesOrTurns(t *testing.T) {
	use := map[string]any{"type": "assistant", "session_id": compactSession, "uuid": "a", "parent_tool_use_id": nil,
		"message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": "u1", "name": "Edit", "input": map[string]any{"file_path": "/w/a"}}}}}
	done := map[string]any{"type": "user", "session_id": compactSession, "uuid": "r", "parent_tool_use_id": nil,
		"message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "u1", "is_error": false}}}}
	first, _ := claudeRenewalBackend("2.1.291")
	if err := sendCompactFrame(t, first, use); err != nil {
		t.Fatal(err)
	}
	resumed, m := claudeRenewalBackend("2.1.291")
	if err := sendCompactFrame(t, resumed, done); err != nil {
		t.Fatal(err)
	}
	if batch, _, _, _ := m.take("gen-1", func(string) bool { return true }, renewBatchMax); len(batch) != 0 {
		t.Fatalf("a result paired with another launch's tool_use: %q", batch)
	}
	b, m := claudeRenewalBackend("2.1.291")
	if err := sendCompactFrame(t, b, use); err != nil {
		t.Fatal(err)
	}
	end := map[string]any{"type": "result", "subtype": "success", "is_error": false, "terminal_reason": "completed", "uuid": "result-1", "session_id": compactSession,
		"user_message_uuid": "input", "user_message_uuids": []string{"input"}, "queued_turn_count": 0}
	if err := sendCompactFrame(t, b, end); err != nil {
		t.Fatal(err)
	}
	b.active = "input-2"
	if err := sendCompactFrame(t, b, done); err != nil {
		t.Fatal(err)
	}
	if batch, _, _, _ := m.take("gen-1", func(string) bool { return true }, renewBatchMax); len(batch) != 0 {
		t.Fatalf("a result after its turn ended renewed: %q", batch)
	}
	// Positive control: the same use and result inside one turn renews.
	b, m = claudeRenewalBackend("2.1.291")
	for _, f := range []map[string]any{use, done} {
		if err := sendCompactFrame(t, b, f); err != nil {
			t.Fatal(err)
		}
	}
	if batch, _, _, _ := m.take("gen-1", func(string) bool { return true }, renewBatchMax); !reflect.DeepEqual(batch, []string{"/w/a"}) {
		t.Fatalf("positive control renewed %q", batch)
	}
}

// Codex's flood case at the reader: a thousand completed writes neither block
// the reader nor occupy the event channel, so a control response is still
// routed to its waiter and an approval still reaches the channel, while the
// mailbox (nobody draining it) holds its bound and reports the overflow.
func TestClaudeRenewalFloodNeverBlocksTheReader(t *testing.T) {
	b, m := claudeRenewalBackend("2.1.291")
	for i := 0; i < 1000; i++ {
		id := "u" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + string(rune('a'+i/676))
		p := "/w/f" + id
		for _, f := range []map[string]any{
			{"type": "assistant", "session_id": compactSession, "uuid": "a" + id, "parent_tool_use_id": nil,
				"message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": id, "name": "Edit", "input": map[string]any{"file_path": p}}}}},
			{"type": "user", "session_id": compactSession, "uuid": "r" + id, "parent_tool_use_id": nil,
				"message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "is_error": false}}}},
		} {
			if err := sendCompactFrame(t, b, f); err != nil {
				t.Fatal(err)
			}
		}
	}
	if n := len(b.events); n != 0 {
		t.Fatalf("completed writes occupied %d event-channel slots", n)
	}
	reply := make(chan claudeReply, 1)
	b.waiters["control-1"] = reply
	if err := sendCompactFrame(t, b, map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": "control-1"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reply:
	default:
		t.Fatal("a control response was not routed after the flood")
	}
	if err := b.permission(json.RawMessage(`"perm-flood"`), json.RawMessage(`{"subtype":"can_use_tool","tool_name":"Write","tool_use_id":"later","input":{"file_path":"/w/later"}}`)); err != nil {
		t.Fatal(err)
	}
	if e := <-b.events; e.Kind != "approval" {
		t.Fatalf("approval not delivered after the flood: %+v", e)
	}
	batch, _, unreported, _ := m.take("gen-1", func(string) bool { return true }, 1<<20)
	if len(batch) != renewMaxPending || !unreported {
		t.Fatalf("mailbox held %d paths (lost=%v), want its bound %d and a reported overflow", len(batch), unreported, renewMaxPending)
	}
}

// The Write omission set lives in three places: claudeWriteOmissionVersions
// here, and WRITE_RENEWAL_QUALIFIED in qualify-claude.py (which outcome the
// runner expects) and check-claude-gate.py (which outcome the checker
// re-derives). A version added to one and not the others is a native run
// whose runner, checker and adapter disagree, found only by an
// operator-authorized launch. So every script that names the set must spell it
// as exactly one `WRITE_RENEWAL_QUALIFIED = ("x", ...)` line equal to the Go
// set: a second assignment or an extension is refused (a new occurrence), and
// so is a respelled one (a list, a computed value), which would otherwise read
// as "no set here" and pass by saying nothing.
var (
	// A tuple literal only: one string with its trailing comma, or two or
	// more separated by ", " (a trailing comma allowed). Adjacent literals
	// and a lone parenthesized string are something else in Python.
	pyQualifiedLine    = regexp.MustCompile(`(?m)^WRITE_RENEWAL_QUALIFIED = \(((?:"[0-9][0-9.]*",)|(?:"[0-9][0-9.]*"(?:, "[0-9][0-9.]*")+,?))\)$`)
	pyQualifiedWrites  = regexp.MustCompile(`WRITE_RENEWAL_QUALIFIED\s*(?:[-+|&]?=[^=]|\.\s*(?:append|extend|add|update)\b)`)
	pyQualifiedVersion = regexp.MustCompile(`"([0-9][0-9.]*)"`)
)

func pyWriteRenewalQualified(src []byte) ([]string, error) {
	lines := pyQualifiedLine.FindAllSubmatch(src, -1)
	if len(lines) != 1 {
		return nil, fmt.Errorf("want exactly one `WRITE_RENEWAL_QUALIFIED = (\"x\", ...)` line, found %d", len(lines))
	}
	if n := len(pyQualifiedWrites.FindAll(src, -1)); n != 1 {
		return nil, fmt.Errorf("WRITE_RENEWAL_QUALIFIED is assigned or extended %d times, want once", n)
	}
	var versions []string
	for _, m := range pyQualifiedVersion.FindAllSubmatch(lines[0][1], -1) {
		versions = append(versions, string(m[1]))
	}
	return versions, nil
}

func TestClaudeWriteOmissionVersionsMatchThePythonMirrors(t *testing.T) {
	spell := func(vs []string) string {
		q := make([]string, len(vs))
		for i, v := range vs {
			q[i] = `"` + v + `"`
		}
		if len(q) == 1 {
			return "WRITE_RENEWAL_QUALIFIED = (" + q[0] + ",)\n"
		}
		return "WRITE_RENEWAL_QUALIFIED = (" + strings.Join(q, ", ") + ")\n"
	}
	exact := spell(claudeWriteOmissionVersions)
	// Positive controls: the exact spelling is read as the Go set, and each
	// way a mirror can drift or hide is refused or read as different.
	if got, err := pyWriteRenewalQualified([]byte("x = 1\n" + exact + "if v in WRITE_RENEWAL_QUALIFIED:\n")); err != nil || !reflect.DeepEqual(got, claudeWriteOmissionVersions) {
		t.Fatalf("the exact spelling read as %q (%v)", got, err)
	}
	for name, src := range map[string]string{
		"a version short":      spell(claudeWriteOmissionVersions[:len(claudeWriteOmissionVersions)-1]),
		"a version extra":      spell(append(append([]string{}, claudeWriteOmissionVersions...), "9.9.9")),
		"respelled as a list":  strings.Replace(strings.Replace(exact, "(", "[", 1), ")", "]", 1),
		"assigned twice":       exact + exact,
		"extended":             exact + "WRITE_RENEWAL_QUALIFIED += (\"9.9.9\",)\n",
		"reassigned elsewhere": exact + "WRITE_RENEWAL_QUALIFIED = tuple(sorted(WRITE_RENEWAL_QUALIFIED))\n",
		"absent":               "WRITE_QUALIFIED = ()\n",
	} {
		got, err := pyWriteRenewalQualified([]byte(src))
		if err == nil && reflect.DeepEqual(got, claudeWriteOmissionVersions) {
			t.Errorf("the gate did not fire on a mirror %s: %q", name, src)
		}
	}
	// Spellings Python reads as something other than a tuple of these
	// strings, so the gate must refuse them outright, whatever versions they
	// name (Codex code review, finding 5): adjacent literals concatenate into
	// one string, and a lone parenthesized string is a str, on which `in` is
	// a substring test ("2.1.29" in "2.1.291").
	for name, src := range map[string]string{
		"adjacent literals":         `WRITE_RENEWAL_QUALIFIED = ("2.1.291" "2.1.299",)` + "\n",
		"adjacent literals, spaced": `WRITE_RENEWAL_QUALIFIED = ("2.1.291" "2.1.299")` + "\n",
		"a lone string":             `WRITE_RENEWAL_QUALIFIED = ("2.1.291")` + "\n",
		"a doubled comma":           `WRITE_RENEWAL_QUALIFIED = ("2.1.291",, "2.1.299")` + "\n",
	} {
		if got, err := pyWriteRenewalQualified([]byte(src)); err == nil {
			t.Errorf("the gate read a non-tuple spelling (%s) as %q: %q", name, got, src)
		}
	}
	scripts, err := filepath.Glob("../../scripts/*.py")
	if err != nil {
		t.Fatal(err)
	}
	mirrors := map[string]bool{}
	for _, path := range scripts {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(src, []byte("WRITE_RENEWAL_QUALIFIED")) {
			continue
		}
		mirrors[filepath.Base(path)] = true
		got, err := pyWriteRenewalQualified(src)
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(path), err)
		} else if !reflect.DeepEqual(got, claudeWriteOmissionVersions) {
			t.Errorf("%s says Write omission is qualified on %q; claude_renewal.go says %q", filepath.Base(path), got, claudeWriteOmissionVersions)
		}
	}
	for _, want := range []string{"qualify-claude.py", "check-claude-gate.py"} {
		if !mirrors[want] {
			t.Errorf("%s no longer names WRITE_RENEWAL_QUALIFIED: renamed, and so no longer checked", want)
		}
	}
}
