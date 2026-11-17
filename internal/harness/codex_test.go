package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// Fake framed native transport: no installed harness, provider, user settings,
// or ledger is touched. Tests assert bytes at the protocol boundary and then
// drive the event sequence which makes the adapter's observations true.
type fakeCodexProcess struct {
	mu           sync.Mutex
	lines        chan json.RawMessage
	writes       []json.RawMessage
	onWrite      func(codexEnvelope)
	writeErr     error
	writeContext func(context.Context, any) error
	closeOnce    sync.Once
}

func (p *fakeCodexProcess) Read() <-chan json.RawMessage { return p.lines }
func (p *fakeCodexProcess) Err() error                   { return nil }
func (p *fakeCodexProcess) Close() error                 { p.closeOnce.Do(func() { close(p.lines) }); return nil }
func (p *fakeCodexProcess) Write(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	p.mu.Lock()
	if p.writeErr != nil {
		err := p.writeErr
		p.mu.Unlock()
		return err
	}
	p.writes = append(p.writes, raw)
	callback := p.onWrite
	p.mu.Unlock()
	if callback != nil {
		var envelope codexEnvelope
		_ = json.Unmarshal(raw, &envelope)
		callback(envelope)
	}
	return nil
}

func (p *fakeCodexProcess) WriteContext(ctx context.Context, v any) error {
	if p.writeContext != nil {
		return p.writeContext(ctx, v)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.Write(v)
}
func (p *fakeCodexProcess) frame(v any) { raw, _ := json.Marshal(v); p.lines <- raw }
func (p *fakeCodexProcess) reply(e codexEnvelope, result any) {
	p.frame(map[string]any{"id": e.ID, "result": result})
}
func (p *fakeCodexProcess) notify(method string, params any) {
	p.frame(map[string]any{"method": method, "params": params})
}
func (p *fakeCodexProcess) allWrites() []json.RawMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]json.RawMessage(nil), p.writes...)
}

func codexFixture(t *testing.T) (*codexBackend, *fakeCodexProcess, Launch) {
	t.Helper()
	root := t.TempDir()
	config := filepath.Join(root, "config.toml")
	if err := os.WriteFile(config, []byte("# isolated fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p := &fakeCodexProcess{lines: make(chan json.RawMessage, 128)}
	p.onWrite = func(e codexEnvelope) {
		switch e.Method {
		case "initialize":
			p.reply(e, map[string]any{})
		case "hooks/list":
			p.reply(e, map[string]any{"data": []any{map[string]any{"cwd": root, "errors": []any{}, "warnings": []string{}, "hooks": []any{map[string]any{"enabled": true, "eventName": "preToolUse", "handlerType": "command", "command": "fixture-gate", "currentHash": "exact-reviewed-hash", "matcher": "^(apply_patch|Bash)$", "trustStatus": "trusted", "async": false}}}}})
		case "thread/start", "thread/resume":
			p.reply(e, map[string]any{"thread": map[string]any{"id": "native-1", "status": map[string]string{"type": "idle"}}})
		case "thread/read":
			p.reply(e, map[string]any{"thread": map[string]any{"id": "native-1", "status": map[string]string{"type": "idle"}}})
		case "thread/turns/list":
			p.reply(e, map[string]any{"data": []any{}, "nextCursor": nil, "backwardsCursor": nil})
		case "turn/start":
			p.reply(e, map[string]any{"turn": map[string]string{"id": "turn/one", "status": "inProgress"}})
		case "turn/steer":
			p.reply(e, map[string]string{"turnId": "turn/one"})
		case "turn/interrupt":
			p.reply(e, map[string]any{})
		}
	}
	// A real file: launch takes its identity before the version probe and
	// re-checks it before starting (F-23). The fake starter never runs it.
	executable := filepath.Join(root, "fixture-codex")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	c := newCodex(executable, func(ctx context.Context, _ *LaunchAuthorization, exe string, args []string, cwd string, env []string) (codexProcess, error) {
		if exe != executable || !reflect.DeepEqual(args, []string{"app-server", "--stdio"}) || cwd != root || !reflect.DeepEqual(env, []string{"CODEX_HOME=" + root, "DISABLE_AUTOUPDATER=1"}) {
			t.Errorf("unexpected launch: %q %q %q %q", exe, args, cwd, env)
		}
		return p, nil
	})
	c.version = func(context.Context, string) (string, error) { return "codex-cli 0.160.0", nil }
	t.Cleanup(func() { _ = c.Close() })
	return c, p, Launch{Worktree: root, PermissionMode: "workspace-write", HookConfig: config, HarnessVersion: "0.160.0", GateCommand: "fixture-gate", GateHash: "exact-reviewed-hash", Hooks: Compatibility{Configured: "observed", Trusted: "observed", Observed: "unknown"}}
}

func codexStart(t *testing.T, c *codexBackend, l Launch) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), liveBound)
	defer cancel()
	id, err := c.Start(ctx, l, testAuthorization(t))
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func codexEvent(t *testing.T, c *codexBackend) Event {
	t.Helper()
	select {
	case e, ok := <-c.Events():
		if !ok {
			t.Fatal("event stream closed")
		}
		return e
	case <-time.After(liveBound):
		t.Fatal("missing native event")
	}
	return Event{}
}
func codexRequest(t *testing.T, p *fakeCodexProcess, method string) codexEnvelope {
	t.Helper()
	for _, raw := range p.allWrites() {
		var e codexEnvelope
		_ = json.Unmarshal(raw, &e)
		if e.Method == method {
			return e
		}
	}
	t.Fatalf("missing %s request", method)
	return codexEnvelope{}
}

func TestCodexControlPreservesIDsAndAcceptanceBoundary(t *testing.T) {
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	if id != "native-1" {
		t.Fatal(id)
	}
	start := codexRequest(t, p, "thread/start")
	var params map[string]any
	_ = json.Unmarshal(start.Params, &params)
	if params["approvalPolicy"] != "on-request" || params["approvalsReviewer"] != "user" || params["sandbox"] != "workspace-write" {
		t.Fatalf("permission escalation: %s", start.Params)
	}
	if _, exists := params["input"]; exists {
		t.Fatal("Start submitted model input before identity capture")
	}
	d, err := c.Send(context.Background(), id, "peer wake; run buddy inbox")
	if err != nil || d.State != "delivered" || d.TurnID != "turn/one" || d.ResultRef != "turn/one" {
		t.Fatalf("delivery %#v %v", d, err)
	}
	d, err = c.Send(context.Background(), id, "second peer wake")
	if err != nil || d.State != "delivered" {
		t.Fatalf("steer %#v %v", d, err)
	}
	steer := codexRequest(t, p, "turn/steer")
	_ = json.Unmarshal(steer.Params, &params)
	if params["expectedTurnId"] != "turn/one" || params["threadId"] != id {
		t.Fatal(string(steer.Params))
	}
	if err := c.Interrupt(context.Background(), id, "wrong-turn"); err == nil {
		t.Fatal("interrupted another turn")
	}
	if err := c.Interrupt(context.Background(), id, "turn/one"); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-c.Events():
		t.Fatalf("RPC acceptance manufactured event %#v", e)
	default:
	}
	p.notify("turn/completed", map[string]any{"threadId": id, "turn": map[string]string{"id": "turn/one", "status": "interrupted"}})
	result := codexEvent(t, c)
	idle := codexEvent(t, c)
	if result.Kind != "result" || result.ResultRef != "turn/one" || result.ResultStatus != "interrupted" || idle.Kind != "idle" {
		t.Fatalf("result %#v; idle %#v", result, idle)
	}
	if _, err := c.Send(context.Background(), "foreign", "wake"); err == nil {
		t.Fatal("sent to an unowned thread")
	}
	observation, err := c.Inspect(context.Background(), id)
	if err != nil || observation.State != "idle" {
		t.Fatalf("inspect %#v %v", observation, err)
	}
	_ = p.Close()
	if e := codexEvent(t, c); e.Kind != "disconnected" {
		t.Fatalf("transport exit became %#v", e)
	}
}

func TestCodexResumeByIDNeverPathOrHistory(t *testing.T) {
	c, p, l := codexFixture(t)
	id, err := c.Resume(context.Background(), l, "native-1", testAuthorization(t))
	if err != nil || id != "native-1" {
		t.Fatalf("resume %q %v", id, err)
	}
	e := codexRequest(t, p, "thread/resume")
	var params map[string]any
	_ = json.Unmarshal(e.Params, &params)
	if params["threadId"] != id {
		t.Fatal(string(e.Params))
	}
	for _, key := range []string{"history", "path", "input"} {
		if _, exists := params[key]; exists {
			t.Fatalf("resume used %s", key)
		}
	}
	if _, err := c.Resume(context.Background(), l, id, testAuthorization(t)); err == nil {
		t.Fatal("backend reused for successor")
	}
}

func TestCodexResumeRejectsChangedNativeID(t *testing.T) {
	c, _, l := codexFixture(t)
	if _, err := c.Resume(context.Background(), l, "captured-predecessor", testAuthorization(t)); err == nil {
		t.Fatal("resume accepted a different native identity")
	}
}

func TestCodexPreflightRefusesBeforeLaunch(t *testing.T) {
	for _, name := range []string{"version", "installed-version", "mode", "relative-worktree", "missing-config", "not-config-toml"} {
		t.Run(name, func(t *testing.T) {
			c, p, l := codexFixture(t)
			switch name {
			case "version":
				l.HarnessVersion = "0.161.0"
			case "installed-version":
				c.version = func(context.Context, string) (string, error) { return "codex-cli 0.161.0", nil }
			case "mode":
				l.PermissionMode = "danger-full-access"
			case "relative-worktree":
				l.Worktree = "."
			case "missing-config":
				l.HookConfig = filepath.Join(t.TempDir(), "config.toml")
			case "not-config-toml":
				l.HookConfig = filepath.Join(t.TempDir(), "settings.json")
			}
			if _, err := c.Start(context.Background(), l, testAuthorization(t)); err == nil {
				t.Fatal("unsafe launch permitted")
			}
			if len(p.allWrites()) != 0 {
				t.Fatal("preflight refusal sent native request")
			}
		})
	}
}

func TestCodexNativeHookPreflightRefusesSkippedOrCompetingHooks(t *testing.T) {
	for _, name := range []string{"missing", "untrusted", "modified", "disabled", "async", "hash", "command", "lifecycle", "permission", "matcher", "cwd", "warnings", "errors", "undeclared"} {
		t.Run(name, func(t *testing.T) {
			c, p, l := codexFixture(t)
			hook := map[string]any{"enabled": true, "eventName": "preToolUse", "handlerType": "command", "command": l.GateCommand, "currentHash": l.GateHash, "matcher": "^(apply_patch|Bash)$", "trustStatus": "trusted", "async": false}
			entry := map[string]any{"cwd": l.Worktree, "errors": []any{}, "warnings": []string{}, "hooks": []any{hook}}
			switch name {
			case "missing":
				entry["hooks"] = []any{}
			case "untrusted", "modified":
				hook["trustStatus"] = name
			case "disabled":
				hook["enabled"] = false
			case "async":
				hook["async"] = true
			case "hash":
				hook["currentHash"] = "changed"
			case "command":
				hook["command"] = "echo harmless-but-not-a-gate"
			case "lifecycle":
				entry["hooks"] = []any{hook, map[string]any{"enabled": true, "eventName": "sessionStart", "handlerType": "command", "command": "buddy hello"}}
			case "permission":
				entry["hooks"] = []any{hook, map[string]any{"enabled": true, "eventName": "permissionRequest", "handlerType": "command", "command": "approve"}}
			case "matcher":
				hook["matcher"] = "^Write$"
			case "cwd":
				entry["cwd"] = "/foreign"
			case "warnings":
				entry["warnings"] = []string{"unrecognized config"}
			case "errors":
				entry["errors"] = []any{map[string]string{"message": "bad configuration", "path": l.HookConfig}}
			case "undeclared":
				l.Hooks.Trusted = "unknown"
			}
			base := p.onWrite
			p.onWrite = func(e codexEnvelope) {
				if e.Method == "hooks/list" {
					p.reply(e, map[string]any{"data": []any{entry}})
				} else {
					base(e)
				}
			}
			if _, err := c.Start(context.Background(), l, testAuthorization(t)); err == nil {
				t.Fatal("incompatible hooks started worker")
			}
			for _, raw := range p.allWrites() {
				var e codexEnvelope
				_ = json.Unmarshal(raw, &e)
				if e.Method == "thread/start" {
					t.Fatalf("refusal occurred after thread creation: %s", raw)
				}
			}
		})
	}
}

// V-01: an apply_patch-only hook misses patches routed by Codex's shell
// engine because the native hook calls those Bash. Admission must cover both
// entry points before thread creation, and the offline projection must agree.
func TestCodexGateRequiresShellPatchCoverage(t *testing.T) {
	for _, tc := range []struct {
		matcher string
		allow   bool
	}{
		{"^apply_patch$", false}, {"apply_patch", false}, {"^Bash$", false},
		{"^(apply_patch|Bash)$", true}, {"^(Bash|apply_patch)$", true},
		{"", true}, {".*", true},
	} {
		t.Run(tc.matcher, func(t *testing.T) {
			c, p, l := codexFixture(t)
			raw, err := json.Marshal(map[string]any{"data": []any{map[string]any{
				"cwd": l.Worktree, "errors": []any{}, "warnings": []string{},
				"hooks": []any{map[string]any{"enabled": true, "eventName": "preToolUse", "handlerType": "command", "command": l.GateCommand, "currentHash": l.GateHash, "matcher": tc.matcher, "trustStatus": "trusted", "async": false}},
			}}})
			if err != nil {
				t.Fatal(err)
			}
			base := p.onWrite
			p.onWrite = func(e codexEnvelope) {
				if e.Method == "hooks/list" {
					p.reply(e, json.RawMessage(raw))
				} else {
					base(e)
				}
			}
			_, err = c.Start(context.Background(), l, testAuthorization(t))
			if (err == nil) != tc.allow {
				t.Errorf("matcher %q: allow=%v, start error=%v", tc.matcher, tc.allow, err)
			}
			started := false
			for _, write := range p.allWrites() {
				var e codexEnvelope
				_ = json.Unmarshal(write, &e)
				started = started || e.Method == "thread/start"
			}
			if started != tc.allow {
				t.Errorf("matcher %q: native thread started=%v", tc.matcher, started)
			}
			configuration, trust := diagnoseCodexHooks(raw, l)
			if (configuration.State == "configured_snapshot") != tc.allow || (trust.State == "trusted_snapshot") != tc.allow {
				t.Errorf("diagnostic disagrees with admission: %+v %+v", configuration, trust)
			}
		})
	}
}

func TestCodexApprovalRoundtripRawIDAndCallbackID(t *testing.T) {
	for _, requestID := range []json.RawMessage{json.RawMessage(`"request/1"`), json.RawMessage(`9007199254740993`)} {
		t.Run(string(requestID), func(t *testing.T) {
			c, p, l := codexFixture(t)
			id := codexStart(t, c, l)
			p.frame(map[string]any{"id": requestID, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": id, "turnId": "turn/one", "itemId": "item/shared", "approvalId": "callback-distinct", "command": "untrusted command"}})
			e := codexEvent(t, c)
			if e.Kind != "approval" || e.Approval == nil || string(e.Approval.ID) != string(requestID) || e.Approval.NativeApprovalID != "callback-distinct" || e.Approval.ItemID != "item/shared" {
				t.Fatalf("approval %#v", e)
			}
			if len(p.allWrites()) != 4 {
				t.Fatal("approval automatically answered")
			}
			d := ApprovalDecision{ID: requestID, NativeSessionID: id, TurnID: "wrong", Decision: "allow"}
			if err := c.Approve(context.Background(), d); err == nil {
				t.Fatal("cross-turn approval accepted")
			}
			d.TurnID = "turn/one"
			d.Decision = "acceptForSession"
			if err := c.Approve(context.Background(), d); err == nil {
				t.Fatal("persistent approval accepted")
			}
			d.Decision = "allow"
			if err := c.Approve(context.Background(), d); err != nil {
				t.Fatal(err)
			}
			writes := p.allWrites()
			var reply codexEnvelope
			_ = json.Unmarshal(writes[len(writes)-1], &reply)
			if string(reply.ID) != string(requestID) || string(reply.Result) != `{"decision":"accept"}` {
				t.Fatalf("wrong reply %s", writes[len(writes)-1])
			}
			if err := c.Approve(context.Background(), d); err == nil {
				t.Fatal("resolved approval replayed")
			}
		})
	}
}

func TestCodexUnsupportedPermissionApprovalAndResolvedRequest(t *testing.T) {
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	p.frame(map[string]any{"id": "permission/1", "method": "item/permissions/requestApproval", "params": map[string]any{"threadId": id, "turnId": "turn/one", "itemId": "item/one", "permissions": map[string]bool{"network": true}}})
	e := codexEvent(t, c)
	if e.Approval == nil {
		t.Fatal(e)
	}
	d := ApprovalDecision{ID: e.Approval.ID, NativeSessionID: id, TurnID: "turn/one", Decision: "allow"}
	if err := c.Approve(context.Background(), d); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("broader grant: %v", err)
	}
	p.notify("serverRequest/resolved", map[string]any{"threadId": id, "requestId": "permission/1"})
	if e := codexEvent(t, c); e.Kind != "approval_resolved" || e.Approval == nil || string(e.Approval.ID) != `"permission/1"` {
		t.Fatalf("missing resolution %#v", e)
	}
	// A following busy event fences consumption of the resolved notification.
	p.notify("turn/started", map[string]any{"threadId": id, "turn": map[string]string{"id": "turn/one"}})
	_ = codexEvent(t, c)
	if err := c.Approve(context.Background(), d); err == nil {
		t.Fatal("resolved request retained")
	}
}

func TestCodexApprovalIDCannotBeRebound(t *testing.T) {
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	request := map[string]any{"id": "same", "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": id, "turnId": "turn/one", "itemId": "first-item"}}
	p.frame(request)
	_ = codexEvent(t, c)
	request["params"] = map[string]any{"threadId": id, "turnId": "turn/one", "itemId": "different-item"}
	p.frame(request)
	if e := codexEvent(t, c); e.Kind != "disconnected" {
		t.Fatalf("rebound request was not refused: %#v", e)
	}
	if err := c.Approve(context.Background(), ApprovalDecision{ID: json.RawMessage(`"same"`), NativeSessionID: id, TurnID: "turn/one", Decision: "allow"}); err == nil {
		t.Fatal("old answer authorized rebound request")
	}
}

func TestCodexResolvedApprovalCannotBeReopenedByDuplicate(t *testing.T) {
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	request := map[string]any{"id": "same", "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": id, "turnId": "turn/one", "itemId": "first-item"}}
	p.frame(request)
	_ = codexEvent(t, c)
	d := ApprovalDecision{ID: json.RawMessage(`"same"`), NativeSessionID: id, TurnID: "turn/one", Decision: "allow"}
	if err := c.Approve(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	p.frame(request)
	// Busy provides a positive stream-progress barrier after the duplicate.
	p.notify("turn/started", map[string]any{"threadId": id, "turn": map[string]string{"id": "turn/one"}})
	if e := codexEvent(t, c); e.Kind != "busy" {
		t.Fatalf("duplicate reopened approval: %#v", e)
	}
	if err := c.Approve(context.Background(), d); err == nil {
		t.Fatal("resolved approval reopened")
	}
	request["params"] = map[string]any{"threadId": id, "turnId": "turn/one", "itemId": "second-item"}
	p.frame(request)
	if e := codexEvent(t, c); e.Kind != "disconnected" {
		t.Fatalf("resolved ID rebound: %#v", e)
	}
}

func TestCodexDelayedAndForeignEventsDoNotRewriteCurrentTurn(t *testing.T) {
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	p.notify("turn/started", map[string]any{"threadId": "foreign", "turn": map[string]string{"id": "foreign-turn"}})
	p.notify("turn/started", map[string]any{"threadId": id, "turn": map[string]string{"id": "turn/new"}})
	if e := codexEvent(t, c); e.TurnID != "turn/new" {
		t.Fatal(e)
	}
	p.notify("turn/completed", map[string]any{"threadId": id, "turn": map[string]string{"id": "turn/old", "status": "completed"}})
	if e := codexEvent(t, c); e.Kind != "result" || e.TurnID != "turn/old" {
		t.Fatal(e)
	}
	if err := c.Interrupt(context.Background(), id, "turn/new"); err != nil {
		t.Fatalf("old completion cleared active turn: %v", err)
	}
	select {
	case e := <-c.Events():
		t.Fatalf("old completion emitted current idle %#v", e)
	default:
	}
	p.notify("thread/closed", map[string]string{"threadId": id})
	if e := codexEvent(t, c); e.Kind != "disconnected" {
		t.Fatal(e)
	}
}

func TestCodexUnknownDeliveryDoesNotClaimAcceptance(t *testing.T) {
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	p.mu.Lock()
	p.writeErr = errors.New("broken pipe")
	p.mu.Unlock()
	d, err := c.Send(context.Background(), id, "wake")
	if err == nil || d.State != "unknown" {
		t.Fatalf("uncertain input %#v %v", d, err)
	}
}

func TestCodexBlockedApprovalCanCancelAndClose(t *testing.T) {
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	p.frame(map[string]any{"id": "blocked", "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": id, "turnId": "turn/one", "itemId": "item/one"}})
	_ = codexEvent(t, c)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	base := p.onWrite
	p.onWrite = func(e codexEnvelope) {
		if e.Method == "" {
			once.Do(func() { close(entered) })
			<-release
		} else {
			base(e)
		}
	}
	p.writeContext = func(ctx context.Context, v any) error {
		once.Do(func() { close(entered) })
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	approvalDone := make(chan error, 1)
	d := ApprovalDecision{ID: json.RawMessage(`"blocked"`), NativeSessionID: id, TurnID: "turn/one", Decision: "allow"}
	go func() { approvalDone <- c.Approve(ctx, d) }()
	select {
	case <-entered:
	case <-time.After(liveBound):
		t.Fatal("approval write never armed")
	}
	cancel()
	var approvalErr error
	approvalReturned := false
	select {
	case approvalErr = <-approvalDone:
		approvalReturned = true
		if approvalErr == nil {
			t.Error("canceled approval reported success")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("cancellation did not release blocked approval")
	}
	closeDone := make(chan struct{})
	go func() { _ = c.Close(); close(closeDone) }()
	closed := false
	select {
	case <-closeDone:
		closed = true
	case <-time.After(100 * time.Millisecond):
		t.Error("blocked approval retained mutex needed by Close")
	}
	close(release)
	if !approvalReturned {
		<-approvalDone
	}
	if !closed {
		<-closeDone
	}
}

func TestCodexApprovalRefusalAndUncertainWriteAreDistinct(t *testing.T) {
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	p.frame(map[string]any{"id": "once", "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": id, "turnId": "turn/one", "itemId": "item/one"}})
	_ = codexEvent(t, c)
	d := ApprovalDecision{ID: json.RawMessage(`"once"`), NativeSessionID: id, TurnID: "turn/one", Decision: "allow"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Approve(ctx, d); !errors.Is(err, ErrNotSubmitted) {
		t.Fatalf("pre-cancel not definite refusal: %v", err)
	}
	p.mu.Lock()
	p.writeErr = errors.New("uncertain pipe failure")
	p.mu.Unlock()
	if err := c.Approve(context.Background(), d); err == nil || errors.Is(err, ErrNotSubmitted) {
		t.Fatalf("attempted write not uncertain: %v", err)
	}
	p.mu.Lock()
	p.writeErr = nil
	p.mu.Unlock()
	if err := c.Approve(context.Background(), d); !errors.Is(err, ErrNotSubmitted) {
		t.Fatalf("uncertain approval can be replayed: %v", err)
	}
}

func TestCodexTerminalAdmissionClosesBeforeBlockedNotification(t *testing.T) {
	for _, trigger := range []string{"eof", "malformed", "rebound"} {
		t.Run(trigger, func(t *testing.T) {
			c, p, l := codexFixture(t)
			c.events = make(chan Event, 1)
			id := codexStart(t, c, l)
			request := map[string]any{"id": "terminal", "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": id, "turnId": "turn/one", "itemId": "item/one"}}
			p.frame(request)
			_ = codexEvent(t, c)
			c.events <- Event{Kind: "test_backpressure"}
			switch trigger {
			case "eof":
				_ = p.Close()
			case "malformed":
				p.lines <- json.RawMessage(`{"method":9}`)
			case "rebound":
				request["params"] = map[string]any{"threadId": id, "turnId": "turn/one", "itemId": "different"}
				p.frame(request)
			}
			select {
			case <-c.done:
			case <-time.After(100 * time.Millisecond):
				t.Error("terminal native condition retained control admission behind event backpressure")
			}
			d := ApprovalDecision{ID: json.RawMessage(`"terminal"`), NativeSessionID: id, TurnID: "turn/one", Decision: "allow"}
			if err := c.Approve(context.Background(), d); err == nil {
				t.Error("terminal transport still allowed approval")
			}
			if e := codexEvent(t, c); e.Kind != "test_backpressure" {
				t.Fatal(e)
			}
			if e := codexEvent(t, c); e.Kind != "disconnected" || e.NativeSessionID != id {
				t.Fatal(e)
			}
		})
	}
}

func TestCodexRequestIDRejectsCoercion(t *testing.T) {
	for _, raw := range []string{`null`, `true`, `{}`, `1.5`, `9223372036854775808`, `"x" true`} {
		if _, err := codexRequestKey(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted malformed ID %s", raw)
		}
	}
	s, _ := codexRequestKey(json.RawMessage(`"12"`))
	n, _ := codexRequestKey(json.RawMessage(`12`))
	if s == n {
		t.Fatal("numeric and string IDs collided")
	}
}

// codexInspectWith answers inspect's two reads: thread/read with the given
// thread status, and thread/turns/list with turns (a full result) or rpcErr
// (the server's own error reply). silent leaves turns/list unanswered. The
// launched build is marked as one measured to honour notLoaded, so inspect
// sends turns/list at all; codexInspectFixture leaves that to the caller.
func codexInspectWith(t *testing.T, threadStatus string, turns any, rpcErr any, silent bool) (*codexBackend, *fakeCodexProcess, string) {
	t.Helper()
	return codexInspectFixture(t, threadStatus, turns, rpcErr, silent, true)
}

func codexInspectFixture(t *testing.T, threadStatus string, turns any, rpcErr any, silent, measured bool) (*codexBackend, *fakeCodexProcess, string) {
	t.Helper()
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	if measured {
		codexMeasureTurnsList(t, c.launchedVersion)
	}
	base := p.onWrite
	p.onWrite = func(e codexEnvelope) {
		switch e.Method {
		case "thread/read":
			p.reply(e, map[string]any{"thread": map[string]any{"id": id, "status": map[string]string{"type": threadStatus}}})
		case "thread/turns/list":
			switch {
			case silent:
			case rpcErr != nil:
				p.frame(map[string]any{"id": e.ID, "error": rpcErr})
			default:
				p.reply(e, turns)
			}
		default:
			base(e)
		}
	}
	return c, p, id
}

// codexMeasureTurnsList stands in for an admission that measured a build
// honours notLoaded: inspect sends thread/turns/list to it (D-197). No test
// in this package runs in parallel, so the shared set is safe to edit.
func codexMeasureTurnsList(t *testing.T, version string) {
	t.Helper()
	if version == "" || codexTurnsListBuilds[version] {
		t.Fatalf("measured version %q is empty or already listed", version)
	}
	codexTurnsListBuilds[version] = true
	t.Cleanup(func() { delete(codexTurnsListBuilds, version) })
}

func codexTurns(turns ...map[string]any) map[string]any {
	data := []any{}
	for _, turn := range turns {
		data = append(data, turn)
	}
	return map[string]any{"data": data, "nextCursor": nil, "backwardsCursor": nil}
}

// Inspect's progress read (D-197): the latest turn's id and status ride in
// Detail and never move State; the request is exactly the bounded one.
func TestCodexInspectLatestTurnIsProgressNotCompletion(t *testing.T) {
	for _, tc := range []struct {
		name, threadStatus, turnStatus, wantState string
	}{
		{"idle thread, turn still listed in progress", "idle", "inProgress", "idle"},
		{"active thread, latest turn completed", "active", "completed", "busy"},
		{"active thread, latest turn failed", "active", "failed", "busy"},
		{"unknown thread status, interrupted turn", "notLoaded", "interrupted", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, p, id := codexInspectWith(t, tc.threadStatus,
				codexTurns(map[string]any{"id": "019a7f3e-turn", "status": tc.turnStatus, "items": []any{}, "itemsView": "notLoaded"}), nil, false)
			obs, err := c.Inspect(context.Background(), id)
			want := Observation{State: tc.wantState, Detail: tc.threadStatus + "; latest turn 019a7f3e-turn " + tc.turnStatus + " (progress, not completion)"}
			if err != nil || obs != want {
				t.Fatalf("inspect %#v %v, want %#v", obs, err, want)
			}
			var params map[string]any
			if err := json.Unmarshal(codexRequest(t, p, "thread/turns/list").Params, &params); err != nil ||
				!reflect.DeepEqual(params, map[string]any{"threadId": id, "limit": float64(1), "sortDirection": "desc", "itemsView": "notLoaded"}) {
				t.Fatalf("turns/list params %#v %v", params, err)
			}
			var read map[string]any
			if err := json.Unmarshal(codexRequest(t, p, "thread/read").Params, &read); err != nil || read["includeTurns"] != false {
				t.Fatalf("thread/read params %#v %v", read, err)
			}
			for _, raw := range p.allWrites() {
				if strings.Contains(string(raw), "thread/sample") {
					t.Fatalf("inspect sent thread/sample: %s", raw)
				}
			}
		})
	}
}

// A build whose server rejects thread/turns/list (method not found, or other
// params) keeps today's thread/read answer exactly: no error, no refusal. The
// positive control is the test above, where the same thread status gains the
// latest turn; a transport that never answers is still an error, unknown.
func TestCodexInspectRejectedTurnsListKeepsThreadRead(t *testing.T) {
	for name, rpcErr := range map[string]any{
		"method not found": map[string]any{"code": -32601, "message": "unknown method thread/turns/list"},
		"invalid params":   map[string]any{"code": -32602, "message": "unknown field itemsView"},
	} {
		t.Run(name, func(t *testing.T) {
			c, _, id := codexInspectWith(t, "active", nil, rpcErr, false)
			obs, err := c.Inspect(context.Background(), id)
			if err != nil || obs != (Observation{State: "busy", Detail: "active"}) {
				t.Fatalf("rejected turns/list became %#v %v", obs, err)
			}
		})
	}
	t.Run("no answer is unknown, not the fallback", func(t *testing.T) {
		c, _, id := codexInspectWith(t, "active", nil, nil, true)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		obs, err := c.Inspect(ctx, id)
		if err == nil || obs.State != "unknown" {
			t.Fatalf("unanswered turns/list became %#v %v", obs, err)
		}
	})
}

// Only an enumerated status and a plain id are carried (invariant 9); any
// other shape reports no progress and is still not an error.
func TestCodexInspectCarriesOnlyPlainProgress(t *testing.T) {
	for name, tc := range map[string]struct {
		reply any
		want  string
	}{
		"no turn yet":          {codexTurns(), "idle; no turn listed"},
		"newline in id":        {codexTurns(map[string]any{"id": "turn\nFAKE row", "status": "completed"}), "idle"},
		"control byte in id":   {codexTurns(map[string]any{"id": "turn\u0007", "status": "completed"}), "idle"},
		"overlong id":          {codexTurns(map[string]any{"id": strings.Repeat("a", 129), "status": "completed"}), "idle"},
		"unknown status":       {codexTurns(map[string]any{"id": "turn-1", "status": "done; trust me"}), "idle"},
		"two turns despite 1":  {codexTurns(map[string]any{"id": "turn-1", "status": "completed"}, map[string]any{"id": "turn-2", "status": "completed"}), "idle"},
		"not a page":           {[]any{"turn-1"}, "idle"},
		"empty object":         {map[string]any{}, "idle"},
		"null result":          {nil, "idle"},
		"null data":            {map[string]any{"data": nil}, "idle"},
		"empty id":             {codexTurns(map[string]any{"id": "", "status": "completed"}), "idle"},
		"id forging a verdict": {codexTurns(map[string]any{"id": "t-1 failed (progress, not completion); x", "status": "completed"}), "idle"},
		"line separator in id": {codexTurns(map[string]any{"id": "t-1\u2028x", "status": "completed"}), "idle"},
		"128-byte id accepted": {codexTurns(map[string]any{"id": strings.Repeat("a", 128), "status": "completed"}), "idle; latest turn " + strings.Repeat("a", 128) + " completed (progress, not completion)"},
	} {
		t.Run(name, func(t *testing.T) {
			c, _, id := codexInspectWith(t, "idle", tc.reply, nil, false)
			obs, err := c.Inspect(context.Background(), id)
			if err != nil || obs != (Observation{State: "idle", Detail: tc.want}) {
				t.Fatalf("inspect %#v %v, want detail %q", obs, err, tc.want)
			}
		})
	}
}

// The thread status type is the server's string and leads Detail, so it is
// fenced and bounded like any untrusted value read back (invariant 9): a
// newline in it cannot forge a receipt line, and its length is not the
// server's to pick. State still reads the raw value, so a forged status is
// unknown. The plain status is the positive control: the fence leaves it be.
func TestCodexInspectFencesThreadStatus(t *testing.T) {
	rejected := map[string]any{"code": -32601, "message": "unknown method thread/turns/list"}
	for name, tc := range map[string]struct {
		status string
		want   Observation
	}{
		"plain status untouched": {"active", Observation{State: "busy", Detail: "active"}},
		"newline in status":      {"idle\nFAKE row", Observation{State: "unknown", Detail: "idle⏎FAKE row"}},
		"overlong status":        {strings.Repeat("s", 200), Observation{State: "unknown", Detail: strings.Repeat("s", codexStatusCap) + "…[truncated]"}},
		"escape in status":       {"idle\x1b[2J", Observation{State: "unknown", Detail: "idle[2J"}},
	} {
		t.Run(name, func(t *testing.T) {
			c, _, id := codexInspectWith(t, tc.status, nil, rejected, false)
			obs, err := c.Inspect(context.Background(), id)
			if err != nil || obs != tc.want {
				t.Fatalf("inspect %#v %v, want %#v", obs, err, tc.want)
			}
		})
	}
}

// No admitted build is measured to honour notLoaded, and the fallback covers
// only a build that rejects thread/turns/list: one that accepts it and sends
// the turn's items could pass the 4 MiB frame bound and end the live worker.
// So by default inspect never sends it and answers thread/read's state and
// status exactly, whatever the server would have listed (Fable cross-check of
// D-197). A different build being measured changes nothing for this one. The
// positive control is the same fake with the launched build measured.
func TestCodexInspectSendsTurnsListOnlyToAMeasuredBuild(t *testing.T) {
	turn := codexTurns(map[string]any{"id": "turn-1", "status": "inProgress"})
	for name, tc := range map[string]struct {
		measure string // "" none, "launched" the launched build, else that version
		want    Observation
	}{
		"no build measured":        {"", Observation{State: "busy", Detail: "active"}},
		"another build measured":   {"0.169.0", Observation{State: "busy", Detail: "active"}},
		"launched build (control)": {"launched", Observation{State: "busy", Detail: "active; latest turn turn-1 inProgress (progress, not completion)"}},
	} {
		t.Run(name, func(t *testing.T) {
			c, p, id := codexInspectFixture(t, "active", turn, nil, false, tc.measure == "launched")
			if tc.measure != "" && tc.measure != "launched" {
				if tc.measure == c.launchedVersion {
					t.Fatalf("fixture launched %q, the other build's version", tc.measure)
				}
				codexMeasureTurnsList(t, tc.measure)
			}
			obs, err := c.Inspect(context.Background(), id)
			if err != nil || obs != tc.want {
				t.Fatalf("inspect %#v %v, want %#v", obs, err, tc.want)
			}
			sent := false
			for _, raw := range p.allWrites() {
				sent = sent || strings.Contains(string(raw), "thread/turns/list")
			}
			if sent != (tc.measure == "launched") {
				t.Fatalf("thread/turns/list sent %v, measure %q", sent, tc.measure)
			}
		})
	}
}
