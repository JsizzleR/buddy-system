package harness

// The measured-frame replay behind D-079's no-bump reading of the 2026-10-14
// tool_result_meta rule (D-078 addendum). Every native user frame of the
// 2.1.291 and 2.1.299 evidence that carried tool_result_meta or answered a
// main-thread Write or Edit is in testdata/claude-tool-result-meta.json,
// reduced to what the renewal reader reads, with the tool uses that preceded
// it. Each is replayed alone through a fresh backend of its version.
//
// `before` is what the first rule (any entry naming a result = not executed)
// produced for the frame, replayed at 447387b; `outcome` is what this code
// must produce. On every 2.1.291 frame they must be equal: that is the claim a
// version-keyed parse rule needs to leave the 2.1.291 record's contract id
// alone, and changing a 2.1.291 outcome here means bumping the contract and
// requalifying, not editing `before`. 2.1.299 frames are where the outcome
// moved (a completed Write or Edit used to be skipped silently).
//
// This file uses only what 447387b already had (the backend, its mailbox and
// the frame helpers), so it compiles and runs against that tree: copied there
// with the fixture, its 2.1.291 rows pass and its accepted 2.1.299 rows fail.

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

type claudeMetaReplayOutcome struct {
	Renews []string `json:"renews"`
	Lost   bool     `json:"lost"`
}

type claudeMetaReplayRow struct {
	Source  string `json:"source"`
	Version string `json:"version"`
	Uses    []struct {
		ID     string          `json:"id"`
		Name   string          `json:"name"`
		Path   string          `json:"path"`
		Parent json.RawMessage `json:"parent_tool_use_id"`
	} `json:"uses"`
	Frame    map[string]any           `json:"frame"`
	Verdicts map[string]string        `json:"verdicts"`
	Before   *claudeMetaReplayOutcome `json:"before"`
	Outcome  *claudeMetaReplayOutcome `json:"outcome"`
}

func loadClaudeMetaFixture(t *testing.T) (arms []json.RawMessage, rows []claudeMetaReplayRow) {
	t.Helper()
	raw, err := os.ReadFile("testdata/claude-tool-result-meta.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Arms     []json.RawMessage     `json:"arms"`
		Measured []claudeMetaReplayRow `json:"measured"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Arms, doc.Measured
}

// replayClaudeMetaRow sends the row's tool uses and then its result frame to
// a fresh backend of the row's version, and reads what the mailbox received.
func replayClaudeMetaRow(t *testing.T, row claudeMetaReplayRow) claudeMetaReplayOutcome {
	t.Helper()
	b, m := claudeRenewalBackend(row.Version)
	sid, _ := row.Frame["session_id"].(string)
	b.nativeID = sid
	for _, u := range row.Uses {
		input := map[string]any{"command": "elided"}
		switch u.Name {
		case "Write", "Edit":
			input = map[string]any{"file_path": u.Path}
		case "NotebookEdit":
			input = map[string]any{"notebook_path": u.Path}
		}
		var parent any
		if len(u.Parent) > 0 {
			if err := json.Unmarshal(u.Parent, &parent); err != nil {
				t.Fatal(err)
			}
		}
		use := map[string]any{"type": "assistant", "session_id": sid, "uuid": "a-" + u.ID, "parent_tool_use_id": parent,
			"message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": u.ID, "name": u.Name, "input": input}}}}
		if err := sendCompactFrame(t, b, use); err != nil {
			t.Fatalf("tool use refused: %v", err)
		}
	}
	if err := sendCompactFrame(t, b, row.Frame); err != nil {
		t.Fatalf("result frame refused: %v", err)
	}
	batch, _, lost, _ := m.take("gen-1", func(string) bool { return true }, renewBatchMax)
	if batch == nil {
		batch = []string{}
	}
	return claudeMetaReplayOutcome{Renews: batch, Lost: lost}
}

func TestClaudeMetaReplayOfMeasuredFrames(t *testing.T) {
	_, rows := loadClaudeMetaFixture(t)
	versions := map[string]int{}
	for _, row := range rows {
		row := row
		versions[row.Version]++
		uuid, _ := row.Frame["uuid"].(string)
		t.Run(row.Version+"/"+row.Source+"/"+uuid, func(t *testing.T) {
			if row.Before == nil || row.Outcome == nil {
				t.Fatal("a measured row without its before and outcome")
			}
			if row.Before.Renews == nil || row.Outcome.Renews == nil {
				t.Fatal("renews must be a list, [] for none")
			}
			if got := replayClaudeMetaRow(t, row); !reflect.DeepEqual(got, *row.Outcome) {
				t.Fatalf("replayed %+v, want %+v", got, *row.Outcome)
			}
			if row.Version == "2.1.291" && !reflect.DeepEqual(*row.Before, *row.Outcome) {
				t.Fatalf("a 2.1.291 outcome moved (%+v -> %+v): that is a contract change (D-079), not a fixture edit",
					*row.Before, *row.Outcome)
			}
		})
	}
	// A floor, so a fixture that lost its rows cannot pass as a replay of
	// nothing: 38 frames of 2.1.291 (17 with tool_result_meta) and 41 of
	// 2.1.299 were measured.
	if versions["2.1.291"] < 38 || versions["2.1.299"] < 41 {
		t.Fatalf("replayed %v measured frames, want at least 38 of 2.1.291 and 41 of 2.1.299", versions)
	}
}
