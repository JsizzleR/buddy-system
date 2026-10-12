package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// A lifecycle event must carry the incarnation captured when its adapter
// started. Looking up today's incarnation when yesterday's Stop arrives would
// pass an otherwise plausible happy-path test and end the replacement worker.
// Exercise that delayed-event shape through the CLI, beside matching-token
// controls that prove the lifecycle operations actually write observations.
type lifecycleReceiptForTest struct {
	Schema      string `json:"schema"`
	Harness     string `json:"harness"`
	SessionID   string `json:"session_id"`
	Incarnation string `json:"incarnation"`
	State       string `json:"state"`
	Event       string `json:"event"`
}

func TestLifecycleBeginNamesOnlyDeclaredSupportedHarness(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	for _, harness := range []string{"", "unknown", "claude --dangerous"} {
		lifecycleRefused(t, f, "begin", "--session", "new-claude", "--harness", harness)
	}
	out, errw, code := f.run(t, f.repo, "", "lifecycle", "begin", "--session", "new-claude", "--harness", "claude")
	var got lifecycleReceiptForTest
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 || errw != "" || got.Schema != "buddy.lifecycle.v1" || got.Harness != "claude" || got.Incarnation == "" {
		t.Fatalf("Claude begin receipt: code=%d out=%s stderr=%s err=%v", code, out, errw, err)
	}
	identity := got
	for _, event := range []string{"refresh", "beat", "busy", "idle", "end"} {
		for _, badHarness := range []string{"", "unsupported"} {
			lifecycleRefused(t, f, event, "--session", identity.SessionID, "--incarnation", identity.Incarnation, "--harness", badHarness)
		}
		out, errw, code := f.run(t, f.repo, "", "lifecycle", event, "--session", identity.SessionID, "--incarnation", identity.Incarnation, "--harness", identity.Harness)
		var receipt lifecycleReceiptForTest
		if err := json.Unmarshal([]byte(out), &receipt); err != nil || code != 0 || errw != "" || receipt.Harness != identity.Harness || receipt.SessionID != identity.SessionID || receipt.Incarnation != identity.Incarnation || receipt.Event != event {
			t.Fatalf("%s lost captured harness: code=%d out=%s stderr=%s err=%v", event, code, out, errw, err)
		}
	}
	// The native Codex hook adapter must not become a raw Claude hook route.
	lifecycleRefused(t, f, "hook", "--incarnation", identity.Incarnation, "--harness", "claude")
}

func lifecycleOK(t *testing.T, f *fixture, args ...string) lifecycleReceiptForTest {
	t.Helper()
	out, errw, code := f.run(t, f.repo, "", append([]string{"lifecycle"}, args...)...)
	if code != 0 || errw != "" {
		t.Fatalf("lifecycle %v: exit=%d stdout=%q stderr=%q", args, code, out, errw)
	}
	var got lifecycleReceiptForTest
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("lifecycle receipt is not one JSON document: %q: %v", out, err)
	}
	if got.Schema != "buddy.lifecycle.v1" || got.Harness != "codex" || got.SessionID == "" || got.Incarnation == "" || got.Event != args[0] {
		t.Fatalf("incomplete or wrong lifecycle receipt: %+v", got)
	}
	wantState := "live"
	if args[0] == "end" {
		wantState = "ended"
	}
	if got.State != wantState {
		t.Fatalf("%s state=%q, want %q", args[0], got.State, wantState)
	}
	for i := 1; i+1 < len(args); i += 2 {
		if args[i] == "--session" && got.SessionID != args[i+1] {
			t.Fatalf("receipt names session %q, operation named %q", got.SessionID, args[i+1])
		}
		if args[i] == "--incarnation" && got.Incarnation != args[i+1] {
			t.Fatalf("receipt names incarnation %q, operation named %q", got.Incarnation, args[i+1])
		}
	}
	return got
}

func lifecycleFixture(t *testing.T) (*fixture, lifecycleReceiptForTest) {
	t.Helper()
	f := newFixture(t)
	if out, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatalf("init: %d %s %s", code, out, errw)
	}
	return f, lifecycleOK(t, f, "begin", "--session", "codex-worker", "--label", "worker-label")
}

func lifecycleStore(t *testing.T, f *fixture) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(f.repo, ".git", "buddy.db"), func() time.Time { return f.clock })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

type lifecycleSnapshotForTest struct {
	Session store.SessionInfo
	Claims  []store.ClaimInfo
	Idle    map[string]store.IdleState
	Dirty   []store.DirtyRecord
	Queued  int
}

func lifecycleSnapshot(t *testing.T, st *store.Store, id string) lifecycleSnapshotForTest {
	t.Helper()
	si, ok, err := st.SessionByID(id)
	if err != nil || !ok {
		t.Fatalf("session %q: known=%v err=%v", id, ok, err)
	}
	claims, err := st.Claims(true)
	if err != nil {
		t.Fatal(err)
	}
	idle, err := st.IdleSessions()
	if err != nil {
		t.Fatal(err)
	}
	dirty, err := st.DirtyPathsOf(id)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := st.Undelivered(id, si.Label)
	if err != nil {
		t.Fatal(err)
	}
	return lifecycleSnapshotForTest{si, claims, idle, dirty, len(queued)}
}

func lifecycleRefused(t *testing.T, f *fixture, args ...string) {
	t.Helper()
	out, errw, code := f.run(t, f.repo, "", append([]string{"lifecycle"}, args...)...)
	if code == 0 {
		t.Fatalf("lifecycle %v accepted: stdout=%q stderr=%q", args, out, errw)
	}
}

func lifecycleHookJSON(t *testing.T, f *fixture, event string, extra map[string]any) string {
	t.Helper()
	in := map[string]any{"session_id": "codex-worker", "cwd": f.repo, "hook_event_name": event}
	for k, v := range extra {
		in[k] = v
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func lifecycleHookOK(t *testing.T, f *fixture, token, event string, extra map[string]any) {
	t.Helper()
	out, errw, code := f.run(t, f.repo, lifecycleHookJSON(t, f, event, extra), "lifecycle", "hook", "--incarnation", token)
	// In particular, Stop must receive valid empty hook JSON. A lifecycle
	// receipt is adapter data, not model context or an inbox-delivery record.
	if code != 0 || errw != "" || strings.TrimSpace(out) != "{}" {
		t.Fatalf("%s hook: exit=%d stdout=%q stderr=%q", event, code, out, errw)
	}
}

func TestCodexLifecycleBeginRequiresEndedIncarnationCAS(t *testing.T) {
	boundedParallel(t)
	f, first := lifecycleFixture(t)
	st := lifecycleStore(t, f)
	before := lifecycleSnapshot(t, st, first.SessionID)
	if before.Session.Label != "worker-label" || before.Session.Incarnation != first.Incarnation {
		t.Fatalf("begin receipt differs from registration: %+v %+v", first, before.Session)
	}
	for _, args := range [][]string{
		{"begin", "--session", first.SessionID},
		{"begin", "--session", first.SessionID, "--previous", first.Incarnation},
		{"begin", "--session", first.SessionID, "--previous", "wrong-token"},
	} {
		lifecycleRefused(t, f, args...)
		if got := lifecycleSnapshot(t, st, first.SessionID); !reflect.DeepEqual(got, before) {
			t.Fatalf("refused begin changed live session: before=%+v after=%+v", before, got)
		}
	}
	lifecycleOK(t, f, "end", "--session", first.SessionID, "--incarnation", first.Incarnation)
	for _, args := range [][]string{
		{"begin", "--session", first.SessionID},
		{"begin", "--session", first.SessionID, "--previous", "wrong-token"},
	} {
		lifecycleRefused(t, f, args...)
	}
	f.clock = f.clock.Add(time.Minute)
	second := lifecycleOK(t, f, "begin", "--session", first.SessionID, "--previous", first.Incarnation)
	if second.Incarnation == first.Incarnation {
		t.Fatal("revival reused the ended incarnation")
	}
	lifecycleOK(t, f, "end", "--session", second.SessionID, "--incarnation", second.Incarnation)
	// A replay must not succeed simply because the replacement has also ended.
	lifecycleRefused(t, f, "begin", "--session", first.SessionID, "--previous", first.Incarnation)
	third := lifecycleOK(t, f, "begin", "--session", second.SessionID, "--previous", second.Incarnation)
	if third.Incarnation == second.Incarnation {
		t.Fatal("second revival reused its predecessor's token")
	}
}

func TestCodexLifecycleDirectCommandsUseExplicitAdapterIdentity(t *testing.T) {
	boundedParallel(t)
	f, current := lifecycleFixture(t)
	// A coordinator may report observations for its adapter-launched worker.
	// This explicit CLI ownership is different from native hooks, which must
	// match their payload to any native identity and override in their process.
	f.env = map[string]string{"CODEX_THREAD_ID": "coordinator", EnvSession: "coordinator"}
	lifecycleOK(t, f, "refresh", "--session", current.SessionID, "--incarnation", current.Incarnation)
	lifecycleOK(t, f, "begin", "--session", "second-worker")
	st := lifecycleStore(t, f)
	if _, exists, err := st.SessionByID("coordinator"); err != nil || exists {
		t.Fatalf("explicit worker command registered its coordinator: exists=%v err=%v", exists, err)
	}
}

func TestCodexLifecycleDelayedEventsCannotMutateReplacement(t *testing.T) {
	boundedParallel(t)
	f, first := lifecycleFixture(t)
	lifecycleOK(t, f, "end", "--session", first.SessionID, "--incarnation", first.Incarnation)
	f.clock = f.clock.Add(time.Minute)
	current := lifecycleOK(t, f, "begin", "--session", first.SessionID, "--previous", first.Incarnation)
	if _, errw, code := f.run(t, f.repo, "", "claim", "worker-work", "--session", current.SessionID, "--desc", "current work", "--scope", "pkg"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := f.run(t, f.repo, "", "msg", current.SessionID, "still queued"); code != 0 {
		t.Fatal(errw)
	}
	lifecycleOK(t, f, "idle", "--session", current.SessionID, "--incarnation", current.Incarnation)
	st := lifecycleStore(t, f)
	before := lifecycleSnapshot(t, st, current.SessionID)
	if len(before.Idle) != 1 || len(before.Claims) != 1 || before.Queued != 1 {
		t.Fatalf("delayed-event controls were not armed: %+v", before)
	}
	f.clock = f.clock.Add(time.Minute)
	for _, event := range []string{"refresh", "beat", "busy", "idle", "end"} {
		args := []string{event, "--session", current.SessionID, "--incarnation", first.Incarnation}
		if event == "beat" {
			args = append(args, "--path", "pkg/current.go")
		}
		lifecycleRefused(t, f, args...)
		if got := lifecycleSnapshot(t, st, current.SessionID); !reflect.DeepEqual(got, before) {
			t.Fatalf("delayed %s changed replacement: before=%+v after=%+v", event, before, got)
		}
	}
	// The same refresh with the captured current token advances its heartbeat.
	lifecycleOK(t, f, "refresh", "--session", current.SessionID, "--incarnation", current.Incarnation)
	if got := lifecycleSnapshot(t, st, current.SessionID); !got.Session.LastSeen.Equal(f.clock) || got.Queued != 1 {
		t.Fatalf("matching refresh failed or consumed inbox: %+v", got)
	}
}

func TestCodexLifecycleCurrentEventsAndEndPreserveClaims(t *testing.T) {
	boundedParallel(t)
	f, current := lifecycleFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "claim", "worker-work", "--session", current.SessionID, "--desc", "work", "--scope", "pkg"); code != 0 {
		t.Fatal(errw)
	}
	st := lifecycleStore(t, f)
	for _, clearEvent := range []string{"beat", "busy"} {
		f.clock = f.clock.Add(time.Minute)
		lifecycleOK(t, f, "idle", "--session", current.SessionID, "--incarnation", current.Incarnation)
		if got := lifecycleSnapshot(t, st, current.SessionID); len(got.Idle) != 1 {
			t.Fatalf("idle did not establish control for %s: %+v", clearEvent, got)
		}
		f.clock = f.clock.Add(time.Minute)
		args := []string{clearEvent, "--session", current.SessionID, "--incarnation", current.Incarnation}
		if clearEvent == "beat" {
			args = append(args, "--path", "pkg/current.go")
		}
		lifecycleOK(t, f, args...)
		got := lifecycleSnapshot(t, st, current.SessionID)
		if len(got.Idle) != 0 || !got.Session.LastSeen.Equal(f.clock) {
			t.Fatalf("%s failed to update observed lifecycle: %+v", clearEvent, got)
		}
		if clearEvent == "beat" && !got.Claims[0].Renewed.Equal(f.clock) {
			t.Fatalf("path heartbeat did not renew its covering claim: %+v", got.Claims)
		}
	}
	lifecycleOK(t, f, "end", "--session", current.SessionID, "--incarnation", current.Incarnation)
	after := lifecycleSnapshot(t, st, current.SessionID)
	if after.Session.Live() || len(after.Claims) != 1 || after.Claims[0].State != "open" {
		t.Fatalf("end must mark session, leave claims for cleanup: %+v", after)
	}
	f.clock = f.clock.Add(time.Minute)
	for _, event := range []string{"refresh", "beat", "busy", "idle"} {
		lifecycleRefused(t, f, event, "--session", current.SessionID, "--incarnation", current.Incarnation)
		if got := lifecycleSnapshot(t, st, current.SessionID); !reflect.DeepEqual(got, after) {
			t.Fatalf("%s resurrected ended session or altered claims: %+v", event, got)
		}
	}
	if _, errw, code := f.run(t, f.repo, "", "sweep"); code != 0 {
		t.Fatal(errw)
	}
	if got := lifecycleSnapshot(t, st, current.SessionID); got.Claims[0].State != "orphaned" {
		t.Fatalf("ordinary cleanup did not orphan ended owner's claim: %+v", got.Claims)
	}
}

func TestCodexLifecycleRefusesIncompleteOrUnknownArguments(t *testing.T) {
	boundedParallel(t)
	f, current := lifecycleFixture(t)
	st := lifecycleStore(t, f)
	before := lifecycleSnapshot(t, st, current.SessionID)
	for _, args := range [][]string{
		{}, {"unknown"}, {"begin"}, {"begin", "--session", "new-worker", "--dry-run"},
		{"begin", "--session", "new-worker", "extra"},
		{"refresh", "--session", current.SessionID},
		{"beat", "--session", current.SessionID},
		{"busy", "--session", current.SessionID},
		{"idle", "--session", current.SessionID},
		{"end", "--session", current.SessionID},
		{"idle", "--incarnation", current.Incarnation},
		{"idle", "--session", current.SessionID, "--incarnation", current.Incarnation, "--since", "1"},
		{"end", "--session", current.SessionID, "--incarnation", current.Incarnation, "--force"},
		{"refresh", "--session", "worker-label", "--incarnation", current.Incarnation},
	} {
		lifecycleRefused(t, f, args...)
		if got := lifecycleSnapshot(t, st, current.SessionID); !reflect.DeepEqual(got, before) {
			t.Fatalf("refused %v changed lifecycle: %+v", args, got)
		}
	}
	f.clock = f.clock.Add(time.Minute)
	lifecycleOK(t, f, "idle", "--session", current.SessionID, "--incarnation", current.Incarnation)
	if got := lifecycleSnapshot(t, st, current.SessionID); len(got.Idle) != 1 {
		t.Fatal("valid idle control did not run")
	}
}

func TestCodexLifecycleNativeHooksObserveWithoutDeliveringInbox(t *testing.T) {
	boundedParallel(t)
	f, current := lifecycleFixture(t)
	f.env["BUDDY_HARNESS"] = "codex"
	f.env["CODEX_THREAD_ID"] = current.SessionID
	if _, errw, code := f.run(t, f.repo, "", "msg", current.SessionID, "private assignment stays queued"); code != 0 {
		t.Fatal(errw)
	}
	st := lifecycleStore(t, f)
	for _, event := range []string{"SessionStart", "Stop", "UserPromptSubmit", "Stop", "PostToolUse", "SessionEnd"} {
		f.clock = f.clock.Add(time.Minute)
		lifecycleHookOK(t, f, current.Incarnation, event, map[string]any{
			"tool_name": "apply_patch", "tool_input": map[string]any{"file_path": "invented-dirty.go"},
		})
		got := lifecycleSnapshot(t, st, current.SessionID)
		if got.Queued != 1 || len(got.Dirty) != 0 {
			t.Fatalf("%s confused observation with delivery or path attribution: %+v", event, got)
		}
		if event == "Stop" && len(got.Idle) != 1 {
			t.Fatal("Stop did not mark idle")
		}
		if (event == "UserPromptSubmit" || event == "PostToolUse") && len(got.Idle) != 0 {
			t.Fatalf("%s failed to clear preceding Stop observation", event)
		}
		if event == "SessionEnd" && got.Session.Live() {
			t.Fatal("SessionEnd did not mark ended")
		}
	}
}

func TestCodexLifecycleHookRefusesWrongIdentityAndUnsupportedEvents(t *testing.T) {
	boundedParallel(t)
	f, current := lifecycleFixture(t)
	st := lifecycleStore(t, f)
	before := lifecycleSnapshot(t, st, current.SessionID)
	tests := []struct {
		name  string
		env   map[string]string
		event string
		extra map[string]any
		token string
	}{
		{"wrong native thread", map[string]string{"CODEX_THREAD_ID": "another-thread"}, "Stop", nil, current.Incarnation},
		{"wrong override", map[string]string{EnvSession: "another-thread"}, "Stop", nil, current.Incarnation},
		{"wrong harness", map[string]string{"BUDDY_HARNESS": "claude"}, "Stop", nil, current.Incarnation},
		{"unknown harness", map[string]string{"BUDDY_HARNESS": "unrecognized"}, "Stop", nil, current.Incarnation},
		{"missing event", nil, "", nil, current.Incarnation},
		{"unknown event", nil, "Unknown", nil, current.Incarnation},
		{"gate belongs to separate path", nil, "PreToolUse", nil, current.Incarnation},
		{"parent id", nil, "Stop", map[string]any{"parent_session_id": "parent"}, current.Incarnation},
		{"parent thread", nil, "Stop", map[string]any{"parent_thread_id": "parent"}, current.Incarnation},
		{"subagent id", nil, "Stop", map[string]any{"agent_id": "child"}, current.Incarnation},
		{"missing token", nil, "Stop", nil, ""},
		{"stale token", nil, "Stop", nil, "stale-incarnation"},
		{"label is not native id", nil, "Stop", map[string]any{"session_id": "worker-label"}, current.Incarnation},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f.env = map[string]string{"BUDDY_HARNESS": "codex"}
			for k, v := range tc.env {
				f.env[k] = v
			}
			out, errw, code := f.run(t, f.repo, lifecycleHookJSON(t, f, tc.event, tc.extra), "lifecycle", "hook", "--incarnation", tc.token)
			if code == 0 {
				t.Fatalf("unsupported hook accepted: stdout=%q stderr=%q", out, errw)
			}
			if got := lifecycleSnapshot(t, st, current.SessionID); !reflect.DeepEqual(got, before) {
				t.Fatalf("refused hook mutated lifecycle: before=%+v after=%+v", before, got)
			}
		})
	}
	f.env = map[string]string{"BUDDY_HARNESS": "codex", "CODEX_THREAD_ID": current.SessionID, EnvSession: current.SessionID}
	lifecycleHookOK(t, f, current.Incarnation, "Stop", nil)
	if got := lifecycleSnapshot(t, st, current.SessionID); len(got.Idle) != 1 {
		t.Fatal("matching identity hook control did not mark idle")
	}
}

func TestCodexLifecycleHookFeatureOffDoesNotCreateLedger(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.env["BUDDY_HARNESS"] = "codex"
	lifecycleHookOK(t, f, "captured-token", "Stop", nil)
	if _, err := os.Stat(filepath.Join(f.repo, ".git", "buddy.db")); !os.IsNotExist(err) {
		t.Fatalf("feature-off hook created or could not inspect ledger: %v", err)
	}
}

func TestCodexLifecycleHookDelayedEventsCannotMutateReplacement(t *testing.T) {
	boundedParallel(t)
	f, first := lifecycleFixture(t)
	lifecycleOK(t, f, "end", "--session", first.SessionID, "--incarnation", first.Incarnation)
	current := lifecycleOK(t, f, "begin", "--session", first.SessionID, "--previous", first.Incarnation)
	// The native hook entry point supplies its own Codex classification. The
	// positive control therefore deliberately has no native environment marker.
	lifecycleHookOK(t, f, current.Incarnation, "Stop", nil)
	st := lifecycleStore(t, f)
	before := lifecycleSnapshot(t, st, current.SessionID)
	f.clock = f.clock.Add(time.Minute)
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "SessionEnd"} {
		out, errw, code := f.run(t, f.repo, lifecycleHookJSON(t, f, event, nil), "lifecycle", "hook", "--incarnation", first.Incarnation)
		if code == 0 {
			t.Fatalf("delayed %s accepted: %q %q", event, out, errw)
		}
		if got := lifecycleSnapshot(t, st, current.SessionID); !reflect.DeepEqual(got, before) {
			t.Fatalf("delayed %s altered replacement: before=%+v after=%+v", event, before, got)
		}
	}
	lifecycleHookOK(t, f, current.Incarnation, "UserPromptSubmit", nil)
	if got := lifecycleSnapshot(t, st, current.SessionID); len(got.Idle) != 0 || !got.Session.LastSeen.Equal(f.clock) {
		t.Fatalf("matching hook did not advance lifecycle: %+v", got)
	}
}

func TestCodexLifecycleHookMalformedInputAndUnreadableLedgerRefuse(t *testing.T) {
	boundedParallel(t)
	f, current := lifecycleFixture(t)
	st := lifecycleStore(t, f)
	before := lifecycleSnapshot(t, st, current.SessionID)
	valid := lifecycleHookJSON(t, f, "Stop", nil)
	for _, input := range []string{"", "{", "{}", valid + "{}", lifecycleHookJSON(t, f, "Stop", map[string]any{"session_id": ""})} {
		if out, errw, code := f.run(t, f.repo, input, "lifecycle", "hook", "--incarnation", current.Incarnation); code == 0 {
			t.Fatalf("malformed hook input %q accepted: %q %q", input, out, errw)
		}
		if got := lifecycleSnapshot(t, st, current.SessionID); !reflect.DeepEqual(got, before) {
			t.Fatalf("malformed hook changed session: %+v", got)
		}
	}
	for _, args := range [][]string{
		{"lifecycle", "hook", "--incarnation", current.Incarnation, "--session", current.SessionID},
		{"lifecycle", "hook", "--incarnation", current.Incarnation, "--force"},
		{"lifecycle", "hook", "--incarnation", current.Incarnation, "extra"},
	} {
		if out, errw, code := f.run(t, f.repo, valid, args...); code == 0 {
			t.Fatalf("unsupported hook arguments %v accepted: %q %q", args, out, errw)
		}
	}
	lifecycleHookOK(t, f, current.Incarnation, "Stop", nil)
	if got := lifecycleSnapshot(t, st, current.SessionID); len(got.Idle) != 1 {
		t.Fatal("valid hook control did not mark idle")
	}

	// A present but invalid database must never take the feature-off arm.
	broken := newFixture(t)
	if err := os.WriteFile(filepath.Join(broken.repo, ".git", "buddy.db"), []byte("not a SQLite ledger"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, errw, code := broken.run(t, broken.repo, lifecycleHookJSON(t, broken, "Stop", nil), "lifecycle", "hook", "--incarnation", "captured-token"); code == 0 {
		t.Fatalf("unreadable ledger treated as feature off: %q %q", out, errw)
	}
}
