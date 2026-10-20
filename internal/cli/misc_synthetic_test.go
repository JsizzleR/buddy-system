package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// syntheticRecord is a placeholder record as the harness wrote it after a
// refused request (copied from a real transcript, text and ids replaced): model
// "<synthetic>", every count zero, isApiErrorMessage set.
const syntheticRecord = `{"parentUuid":"X","isSidechain":false,"type":"assistant","uuid":"X","timestamp":"2026-08-14T13:00:00.000Z",` +
	`"message":{"diagnostics":null,"id":"X","container":null,"model":"<synthetic>","role":"assistant","stop_details":null,` +
	`"stop_reason":"stop_sequence","stop_sequence":"","type":"message","usage":{"output_tokens_details":null,"input_tokens":0,` +
	`"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"server_tool_use":{"web_search_requests":0,` +
	`"web_fetch_requests":0},"service_tier":null,"cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":0},` +
	`"inference_geo":null,"iterations":null,"speed":null,"fallback_credit":null},"content":[{"type":"text","text":"X"}],` +
	`"context_management":null},"requestId":"X","error":"oauth_org_not_allowed","isApiErrorMessage":true,"apiErrorStatus":403,` +
	`"apiErrorCode":"oauth_not_allowed_for_organization","perTurnEffort":"xhigh","session_id":"X","userType":"external",` +
	`"entrypoint":"cli","cwd":"X","sessionId":"X","version":"X","gitBranch":"X"}`

func TestLastUsageSkipsRecordsThatAreNotRequests(t *testing.T) {
	boundedParallel(t)
	real := turnLine("2026-08-14T12:00:00.000Z", "claude-opus-5-5", "high", 3, 180000, 2000, 400, false, 0)
	for _, tc := range []struct {
		name  string
		after string
	}{
		{"synthetic placeholder", syntheticRecord},
		{"all-zero under a real model name", turnLine("2026-08-14T13:00:00.000Z", "claude-opus-5-5", "high", 0, 0, 0, 0, false, 0)},
		{"synthetic with counts", turnLine("2026-08-14T13:00:00.000Z", "<synthetic>", "", 5, 0, 0, 1, false, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "t.jsonl")
			if err := os.WriteFile(path, []byte(real+"\n"+tc.after+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			got, ok := lastUsage(path)
			if !ok {
				t.Fatal("the real request before the placeholder must stay the sample")
			}
			if got.Model != "claude-opus-5-5" || got.Prompt != 182003 ||
				!got.At.Equal(time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)) {
				t.Fatalf("placeholder read as the last request: %+v", got)
			}
		})
	}
	// The positive control: the same file with the placeholder replaced by a
	// real newer request reads the newer one, so the skip is not "newest
	// record ignored".
	path := filepath.Join(t.TempDir(), "t.jsonl")
	newer := turnLine("2026-08-14T13:00:00.000Z", "claude-opus-5-5", "high", 7, 190000, 0, 10, false, 0)
	if err := os.WriteFile(path, []byte(real+"\n"+newer+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := lastUsage(path); !ok || got.Prompt != 190007 {
		t.Fatalf("a real newer request must win: %+v %v", got, ok)
	}
}
