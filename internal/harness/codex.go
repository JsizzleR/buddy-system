package harness

// The app-server owns Codex processes and turns; Buddy only consumes translated
// observations. One backend owns one launched thread. This intentionally cannot
// attach to another client's thread or turn a transport exit into SessionEnd.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

type codexProcess interface {
	Write(any) error
	WriteContext(context.Context, any) error
	Read() <-chan json.RawMessage
	Close() error
	Err() error
}

// codexStarter spawns the app-server through the controller's launch
// authorization; a fake starter in tests takes the same parameter.
type codexStarter func(context.Context, *LaunchAuthorization, string, []string, string, []string) (codexProcess, error)

type codexBackend struct {
	executable    string
	start         codexStarter
	version       func(context.Context, string) (string, error)
	opMu          sync.Mutex
	mu            sync.Mutex
	process       codexProcess
	nativeID      string
	activeTurn    string
	state         string
	started       bool
	closed        bool
	seq           uint64
	pending       map[string]chan codexReply
	approvals     map[string]Approval
	seenApprovals map[string]Approval
	finished      map[string]bool
	events        chan Event
	done          chan struct{}
	stop          chan struct{}
	closeOnce     sync.Once
	stopOnce      sync.Once
	// Completed-write observation (D-078): the mailbox and this launch's
	// generation. A fileChange renews only inside the turn this launch has
	// active, so a completion replayed from an earlier launch never does.
	renewals   *renewalMailbox
	generation string
	// pendingMethod names each outstanding request's method, so the reader
	// can record, from turn/start's own reply and before it reads any later
	// frame, the turns this launch started (ownTurns). Codex review of the
	// code pass: activeTurn alone is set by any turn/started, so a turn
	// replayed into a resumed thread renewed under the new generation.
	pendingMethod map[string]string
	ownTurns      map[string]bool
}

func (c *codexBackend) observeRenewals(m *renewalMailbox) {
	c.mu.Lock()
	c.renewals = m
	c.mu.Unlock()
}

type codexReply struct {
	result json.RawMessage
	err    error
}

type codexEnvelope struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

// NewCodex requires an absolute executable and resolves it once, so the
// version probe and the launch name the same file (executable.go, F-23).
func NewCodex(executable string) (Backend, error) {
	resolved, err := resolveExecutable(executable)
	if err != nil {
		return nil, err
	}
	return newCodex(resolved, func(ctx context.Context, auth *LaunchAuthorization, exe string, args []string, cwd string, env []string) (codexProcess, error) {
		return StartJSONProcess(ctx, auth, exe, args, cwd, env)
	}), nil
}

func newCodex(executable string, start codexStarter) *codexBackend {
	return &codexBackend{executable: executable, start: start, state: "unknown", version: probeNativeVersion,
		pending: make(map[string]chan codexReply), approvals: make(map[string]Approval), seenApprovals: make(map[string]Approval),
		finished: make(map[string]bool), events: make(chan Event, 128), done: make(chan struct{}), stop: make(chan struct{}),
		pendingMethod: make(map[string]string), ownTurns: make(map[string]bool)}
}

func (c *codexBackend) Capabilities() Capabilities {
	return Capabilities{
		"start":                  {Support: "experimental", Reason: "Codex app-server v2 contract inspected in codex-cli 0.160.0; launched workers only"},
		"send":                   {Support: "experimental", Reason: "turn/start or exact-turn turn/steer; native acceptance is not worker acknowledgement"},
		"inspect":                {Support: "experimental", Reason: "thread/read runtime status; not a ledger or validation verdict"},
		"reconcile":              {Support: "unsupported", Reason: "the 0.160.0 history read (thread/read includeTurns) is unbounded; an oversized reply would end the live worker's transport, and a paged read is not qualified"},
		"resume":                 {Support: "experimental", Reason: "thread/resume by captured native thread ID; native recovery not qualified"},
		"interrupt":              {Support: "experimental", Reason: "turn/interrupt with explicit native thread and turn IDs"},
		"approval":               {Support: "experimental", Reason: "exact request-scoped command/file allow or deny only; no session or policy grants"},
		"attach":                 {Support: "unsupported", Reason: "only the worker launched by this adapter is controllable"},
		"permission_grants":      {Support: "unsupported", Reason: "permission-profile requests are surfaced but never translated into broad grants"},
		"peer_identity":          {Support: "unsupported", Reason: "wake text is explicitly peer data; app-server text input is not a native authenticated peer envelope"},
		"wake":                   {Support: "experimental", Reason: "bounded peer inbox notification over the owned thread input transport"},
		"telemetry":              {Support: "unknown", Reason: "no usage or cache translation in this bounded adapter"},
		"compaction_observation": {Support: "experimental", Reason: "contextCompaction item lifecycle only; no automatic retry or compaction control"},
		"claim_renewal":          {Support: "experimental", Reason: "item/completed fileChange with status completed, in the active turn: every changes[].path and kind.move_path; commandExecution renews nothing"},
	}
}

func (c *codexBackend) Events() <-chan Event { return c.events }

func (c *codexBackend) Start(ctx context.Context, launch Launch, auth *LaunchAuthorization) (string, error) {
	return c.launch(ctx, launch, "", auth)
}

func (c *codexBackend) Resume(ctx context.Context, launch Launch, nativeID string, auth *LaunchAuthorization) (string, error) {
	if nativeID == "" {
		return "", errors.New("Codex resume requires the captured native thread ID")
	}
	return c.launch(ctx, launch, nativeID, auth)
}

func (c *codexBackend) launch(ctx context.Context, launch Launch, resumeID string, auth *LaunchAuthorization) (string, error) {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	if launch.HarnessVersion != "0.160.0" {
		return "", errors.New("unsupported Codex version: this adapter contract requires codex-cli 0.160.0")
	}
	checked, err := observeExecutable(c.executable)
	if err != nil {
		return "", err
	}
	version, err := c.version(ctx, c.executable)
	if err != nil || version != "codex-cli 0.160.0" {
		return "", errors.New("unsupported installed Codex executable: expected codex-cli 0.160.0")
	}
	if launch.PermissionMode != "read-only" && launch.PermissionMode != "workspace-write" {
		return "", errors.New("Codex permission mode must be read-only or workspace-write")
	}
	if !filepath.IsAbs(launch.Worktree) {
		return "", errors.New("Codex worktree must be an absolute path")
	}
	if !filepath.IsAbs(launch.HookConfig) || filepath.Base(launch.HookConfig) != "config.toml" {
		return "", errors.New("Codex requires an explicit absolute isolated config.toml path")
	}
	info, err := os.Stat(launch.HookConfig)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("Codex isolated config.toml must be a readable regular file")
	}
	if err := sameExecutable(c.executable, checked); err != nil {
		return "", err
	}
	c.mu.Lock()
	if c.started || c.closed {
		c.mu.Unlock()
		return "", errors.New("Codex backend already launched or closed; create a new adapter for a successor")
	}
	c.started = true
	c.nativeID = resumeID
	c.generation = auth.generation()
	c.mu.Unlock()
	// The caller supplies the isolated home. No user configuration or hook trust
	// is installed or rewritten by a control adapter.
	proc, err := c.start(ctx, auth, c.executable, []string{"app-server", "--stdio"}, launch.Worktree, []string{"CODEX_HOME=" + filepath.Dir(launch.HookConfig)})
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.process = proc
	c.mu.Unlock()
	go c.readLoop(proc)
	if _, err = c.call(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]string{"name": "buddy_harness_adapter", "version": "1"},
		"capabilities": map[string]bool{"experimentalApi": true},
	}); err != nil {
		_ = c.Close()
		return "", err
	}
	if err = proc.WriteContext(ctx, map[string]any{"method": "initialized"}); err != nil {
		_ = c.Close()
		return "", err
	}
	if err = c.checkHooks(ctx, launch); err != nil {
		_ = c.Close()
		return "", err
	}
	method := "thread/start"
	params := map[string]any{"cwd": launch.Worktree, "sandbox": launch.PermissionMode, "approvalPolicy": "on-request", "approvalsReviewer": "user"}
	if resumeID != "" {
		method = "thread/resume"
		params["threadId"] = resumeID
		params["excludeTurns"] = true
	}
	raw, err := c.call(ctx, method, params)
	if err != nil {
		_ = c.Close()
		return "", err
	}
	var result struct {
		Thread struct {
			ID     string `json:"id"`
			Status struct {
				Type string `json:"type"`
			} `json:"status"`
		} `json:"thread"`
	}
	if err = json.Unmarshal(raw, &result); err != nil || result.Thread.ID == "" || (resumeID != "" && result.Thread.ID != resumeID) {
		_ = c.Close()
		return "", errors.New("Codex start/resume returned a missing or mismatched native thread ID")
	}
	c.mu.Lock()
	c.nativeID = result.Thread.ID
	c.state = codexState(result.Thread.Status.Type)
	c.mu.Unlock()
	return result.Thread.ID, nil
}

// A declared compatibility flag is not evidence that the native harness loaded
// the gate. Inspect effective hook metadata before thread creation, and support
// only the reviewed synchronous gate. Controller lifecycle translation and
// native lifecycle hooks cannot both own the same captured registration.
func (c *codexBackend) checkHooks(ctx context.Context, launch Launch) error {
	if launch.Hooks.Configured != "observed" || launch.Hooks.Trusted != "observed" || launch.GateCommand == "" || launch.GateHash == "" {
		return errors.New("Codex requires declared configured/trusted hook evidence and the reviewed gate command/hash")
	}
	raw, err := c.call(ctx, "hooks/list", map[string]any{"cwds": []string{launch.Worktree}})
	if err != nil {
		return fmt.Errorf("Codex hook compatibility unavailable: %w", err)
	}
	var result struct {
		Data []struct {
			CWD      string            `json:"cwd"`
			Errors   []json.RawMessage `json:"errors"`
			Warnings []string          `json:"warnings"`
			Hooks    []struct {
				Enabled     bool   `json:"enabled"`
				EventName   string `json:"eventName"`
				HandlerType string `json:"handlerType"`
				Command     string `json:"command"`
				Matcher     string `json:"matcher"`
				TrustStatus string `json:"trustStatus"`
				CurrentHash string `json:"currentHash"`
				Async       bool   `json:"async"`
			} `json:"hooks"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &result) != nil || len(result.Data) != 1 || result.Data[0].CWD != launch.Worktree || len(result.Data[0].Errors) > 0 || len(result.Data[0].Warnings) > 0 {
		return errors.New("Codex effective hook configuration is missing, ambiguous, or reports diagnostics")
	}
	gate := false
	for _, h := range result.Data[0].Hooks {
		if !h.Enabled {
			continue
		}
		if h.EventName != "preToolUse" || h.HandlerType != "command" || h.Async || h.Command != launch.GateCommand || h.CurrentHash != launch.GateHash || h.TrustStatus != "trusted" {
			return errors.New("unsupported Codex hooks: only the exact trusted synchronous gate is compatible with adapter lifecycle ownership")
		}
		if !codexGateCoversWrites(h.Matcher) {
			return errors.New("unsupported Codex gate matcher: apply_patch and Bash coverage required")
		}
		gate = true
	}
	if !gate {
		return errors.New("Codex has no enabled trusted apply_patch and Bash gate")
	}
	return nil
}

// Codex 0.160.0 routes shell patches through its own patch engine, but emits
// Bash to PreToolUse. A patch-only matcher therefore admits a worker whose
// shell patches never reach Buddy (V-01). Keep admission and diagnose on the
// same narrow native-qualified matcher contract; do not guess regexp coverage.
func codexGateCoversWrites(matcher string) bool {
	switch matcher {
	case "", ".*", "^(apply_patch|Bash)$", "^(Bash|apply_patch)$":
		return true
	default:
		return false
	}
}

func codexState(native string) string {
	switch native {
	case "idle":
		return "idle"
	case "active":
		return "busy"
	default:
		return "unknown"
	}
}

func (c *codexBackend) checkID(nativeID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.process == nil || nativeID == "" || nativeID != c.nativeID {
		return errors.New("Codex request does not identify this live adapter-launched worker")
	}
	return nil
}

// Refusals before the request is written are ErrNotSubmitted (F-10), as are
// call's own pre-write refusals; a write error or lost reply stays uncertain.
func (c *codexBackend) Send(ctx context.Context, nativeID, text string) (Delivery, error) {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	if err := c.checkID(nativeID); err != nil {
		return Delivery{State: "unknown"}, fmt.Errorf("%w: %v", ErrNotSubmitted, err)
	}
	if text == "" || len(text) > 4096 {
		return Delivery{State: "unknown"}, fmt.Errorf("%w: Codex wake text must contain 1 to 4096 bytes", ErrNotSubmitted)
	}
	c.mu.Lock()
	turn, state := c.activeTurn, c.state
	c.mu.Unlock()
	params := map[string]any{"threadId": nativeID, "input": []map[string]any{{"type": "text", "text": text}}}
	method := "turn/start"
	if state != "idle" {
		if turn == "" {
			return Delivery{State: "unknown"}, fmt.Errorf("%w: Codex active turn is unknown; inspect before sending", ErrNotSubmitted)
		}
		method = "turn/steer"
		params["expectedTurnId"] = turn
	}
	raw, err := c.call(ctx, method, params)
	if err != nil {
		return Delivery{State: "unknown"}, err
	}
	var result struct {
		TurnID string `json:"turnId"`
		Turn   struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return Delivery{State: "unknown"}, err
	}
	if method == "turn/start" {
		result.TurnID = result.Turn.ID
	}
	if result.TurnID == "" || (method == "turn/steer" && result.TurnID != turn) {
		return Delivery{State: "unknown"}, errors.New("Codex input reply has a missing or mismatched turn ID")
	}
	c.mu.Lock()
	if !c.finished[result.TurnID] {
		c.activeTurn, c.state = result.TurnID, "busy"
	}
	c.mu.Unlock()
	return Delivery{State: "delivered", TurnID: result.TurnID, ResultRef: result.TurnID}, nil
}

func (c *codexBackend) Inspect(ctx context.Context, nativeID string) (Observation, error) {
	if err := c.checkID(nativeID); err != nil {
		return Observation{State: "unknown"}, err
	}
	raw, err := c.call(ctx, "thread/read", map[string]any{"threadId": nativeID, "includeTurns": false})
	if err != nil {
		return Observation{State: "unknown"}, err
	}
	var result struct {
		Thread struct {
			ID     string `json:"id"`
			Status struct {
				Type string `json:"type"`
			} `json:"status"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.Thread.ID != nativeID {
		return Observation{State: "unknown"}, errors.New("Codex inspect reply has a mismatched native thread ID")
	}
	// Read-only inspection never overwrites newer event-stream state.
	return Observation{State: codexState(result.Thread.Status.Type), Detail: result.Thread.Status.Type}, nil
}

// ReadTurn refuses before making any native request (F-08). The only history
// read in the pinned 0.160.0 contract is thread/read with includeTurns, whose
// params are just {threadId, includeTurns}: the reply is the WHOLE history
// (command output, diffs) and outgrows the 4 MiB frame bound on a long thread.
// On the shared live transport that is not "an error for this read": the
// oversized frame fails the transport and ends the worker, possibly mid-turn,
// from what was meant to be a read-only observation. The 0.160.0 binary also
// carries a paged thread/turns/list (a five-field params struct with an
// itemsView), but its parameters and reply bounds are not qualified, so it is
// not guessed at here. Until a bounded read is qualified, reconcile reports
// unsupported and the journalled wake stays unknown, never replayable.
func (c *codexBackend) ReadTurn(ctx context.Context, nativeID, turnID string) (TurnObservation, error) {
	obs := TurnObservation{NativeSessionID: nativeID, TurnID: turnID,
		Status: Observation{State: "unknown"}, Source: "native_thread_read"}
	if err := c.checkID(nativeID); err != nil {
		return obs, fmt.Errorf("%w: %v", ErrNotSubmitted, err)
	}
	if turnID == "" {
		return obs, fmt.Errorf("%w: Codex reconciliation requires an exact recorded turn ID", ErrNotSubmitted)
	}
	return obs, fmt.Errorf("%w: Codex reconcile is unsupported: the qualified history read is unbounded and an oversized reply would end the live worker; the wake stays unknown, do not replay", ErrNotSubmitted)
}

// codexTurnFromHistory projects one exact turn from a thread/read history
// reply. It is kept, with its tests, for a bounded read once one is qualified;
// nothing calls it on the live transport today. Missing history is unknown.
func codexTurnFromHistory(raw json.RawMessage, nativeID, turnID string) (TurnObservation, error) {
	obs := TurnObservation{NativeSessionID: nativeID, TurnID: turnID,
		Status: Observation{State: "unknown"}, Source: "native_thread_read"}
	var result struct {
		Thread struct {
			ID    string `json:"id"`
			Turns []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"turns"`
		} `json:"thread"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Thread.ID != nativeID {
		return obs, errors.New("Codex history has a missing or mismatched thread ID")
	}
	found := false
	for _, turn := range result.Thread.Turns {
		if turn.ID != turnID {
			continue
		}
		if found {
			return obs, errors.New("Codex history repeats the requested turn ID")
		}
		found = true
		switch turn.Status {
		case "completed", "failed", "interrupted", "inProgress":
			obs.Status = Observation{State: turn.Status, Detail: "native history observation; tool effects and work completion not established"}
		default:
			return obs, errors.New("Codex history has an unsupported turn status")
		}
	}
	if !found {
		obs.Status.Detail = "recorded turn absent from returned history; do not replay"
	}
	return obs, nil
}

func (c *codexBackend) Interrupt(ctx context.Context, nativeID, turnID string) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	if err := c.checkID(nativeID); err != nil {
		return err
	}
	c.mu.Lock()
	active := c.activeTurn
	c.mu.Unlock()
	if turnID == "" || turnID != active {
		return errors.New("Codex interrupt requires the exact observed active turn ID")
	}
	_, err := c.call(ctx, "turn/interrupt", map[string]any{"threadId": nativeID, "turnId": turnID})
	// Acceptance is not completion: wait for the native turn/completed event.
	return err
}

func (c *codexBackend) Approve(ctx context.Context, decision ApprovalDecision) error {
	proc, response, err := c.takeApproval(ctx, decision)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotSubmitted, err)
	}
	// Reservation is one-shot even when a partially written native response has
	// an uncertain outcome. Keep the mutex out of blocking I/O so Close can
	// always shut down the owned transport, and cancellation can end the write.
	if err := proc.WriteContext(ctx, response); err != nil {
		return fmt.Errorf("Codex approval outcome unknown: %w", err)
	}
	return nil
}

func (c *codexBackend) takeApproval(ctx context.Context, decision ApprovalDecision) (codexProcess, any, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err := c.checkID(decision.NativeSessionID); err != nil {
		return nil, nil, err
	}
	if decision.Decision != "allow" && decision.Decision != "deny" {
		return nil, nil, errors.New("Codex approval supports request-scoped allow or deny only")
	}
	key, err := codexRequestKey(decision.ID)
	if err != nil {
		return nil, nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if c.closed {
		return nil, nil, errors.New("Codex transport disconnected")
	}
	a, ok := c.approvals[key]
	if !ok || a.NativeSessionID != decision.NativeSessionID || a.TurnID != decision.TurnID {
		return nil, nil, errors.New("Codex approval does not match an unresolved request for this worker and turn")
	}
	if a.Kind != "item/commandExecution/requestApproval" && a.Kind != "item/fileChange/requestApproval" {
		return nil, nil, errors.New("Codex permission-profile approval resolution is unsupported")
	}
	native := "decline"
	if decision.Decision == "allow" {
		native = "accept"
	}
	var detail struct {
		AvailableDecisions []json.RawMessage `json:"availableDecisions"`
	}
	if json.Unmarshal(a.Detail, &detail) != nil {
		return nil, nil, errors.New("Codex approval detail is malformed")
	}
	if detail.AvailableDecisions != nil {
		available := false
		for _, option := range detail.AvailableDecisions {
			var value string
			if json.Unmarshal(option, &value) == nil && value == native {
				available = true
			}
		}
		if !available {
			return nil, nil, errors.New("Codex native request does not offer this decision")
		}
	}
	delete(c.approvals, key)
	return c.process, map[string]any{"id": a.ID, "result": map[string]string{"decision": native}}, nil
}

func codexRequestKey(raw json.RawMessage) (string, error) {
	if !json.Valid(raw) {
		return "", errors.New("invalid Codex native request ID")
	}
	var value any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return "", errors.New("invalid Codex native request ID")
	}
	switch v := value.(type) {
	case string:
		return "s:" + v, nil
	case json.Number:
		if _, err := v.Int64(); err == nil {
			return "n:" + string(v), nil
		}
	}
	return "", errors.New("Codex native request ID must be a string or int64")
}

func (c *codexBackend) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotSubmitted, err)
	}
	c.mu.Lock()
	if c.closed || c.process == nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("%w: Codex transport is not connected", ErrNotSubmitted)
	}
	c.seq++
	id := strconv.FormatUint(c.seq, 10)
	ch := make(chan codexReply, 1)
	c.pending["s:"+id] = ch
	c.pendingMethod["s:"+id] = method
	proc := c.process
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, "s:"+id); delete(c.pendingMethod, "s:"+id); c.mu.Unlock() }()
	if err := proc.WriteContext(ctx, map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	select {
	case reply := <-ch:
		return reply.result, reply.err
	case <-ctx.Done():
		return nil, fmt.Errorf("Codex %s outcome unknown: %w", method, ctx.Err())
	case <-c.done:
		return nil, errors.New("Codex transport disconnected; request outcome unknown")
	}
}

func (c *codexBackend) emit(event Event) bool {
	select {
	case c.events <- event:
		return true
	case <-c.done:
		return false
	}
}

// Terminal admission changes precede publication. A slow consumer must never
// keep an EOF or protocol-error transport eligible to accept operator approval.
// The separate explicit-close signal lets callers abandon a backed-up terminal
// notification without making ordinary EOF silently discard its observation.
func (c *codexBackend) terminal(proc codexProcess, event Event) {
	c.mu.Lock()
	if event.NativeSessionID == "" {
		event.NativeSessionID = c.nativeID
	}
	c.mu.Unlock()
	c.finish()
	_ = proc.Close()
	select {
	case c.events <- event:
	case <-c.stop:
	}
}

func (c *codexBackend) readLoop(proc codexProcess) {
	defer close(c.events)
	for raw := range proc.Read() {
		var envelope codexEnvelope
		if err := json.Unmarshal(raw, &envelope); err != nil {
			c.terminal(proc, Event{Kind: "disconnected", Err: "malformed Codex JSON frame"})
			return
		}
		if envelope.Method == "" {
			key, err := codexRequestKey(envelope.ID)
			if err != nil {
				continue
			}
			c.mu.Lock()
			ch := c.pending[key]
			if c.pendingMethod[key] == "turn/start" && (len(envelope.Error) == 0 || string(envelope.Error) == "null") {
				var started struct {
					Turn struct {
						ID string `json:"id"`
					} `json:"turn"`
				}
				if json.Unmarshal(envelope.Result, &started) == nil && started.Turn.ID != "" {
					c.ownTurns[started.Turn.ID] = true
				}
			}
			c.mu.Unlock()
			if ch != nil {
				reply := codexReply{result: envelope.Result}
				if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
					reply.err = fmt.Errorf("Codex RPC rejected request: %s", envelope.Error)
				}
				select {
				case ch <- reply:
				default:
				}
			}
			continue
		}
		c.nativeEvent(envelope)
		select {
		case <-c.done:
			return
		default:
		}
	}
	c.mu.Lock()
	nativeID := c.nativeID
	c.mu.Unlock()
	detail := "Codex transport closed; logical session end was not observed"
	if err := proc.Err(); err != nil {
		detail = err.Error()
	}
	c.terminal(proc, Event{Kind: "disconnected", NativeSessionID: nativeID, Err: detail})
}

func (c *codexBackend) nativeEvent(envelope codexEnvelope) {
	var p struct {
		ThreadID   string          `json:"threadId"`
		TurnID     string          `json:"turnId"`
		ItemID     string          `json:"itemId"`
		ApprovalID string          `json:"approvalId"`
		RequestID  json.RawMessage `json:"requestId"`
		Item       struct {
			ID      string          `json:"id"`
			Type    string          `json:"type"`
			Status  string          `json:"status"`
			Changes json.RawMessage `json:"changes"`
		} `json:"item"`
		Turn struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"turn"`
		Status struct {
			Type string `json:"type"`
		} `json:"status"`
	}
	if json.Unmarshal(envelope.Params, &p) != nil {
		return
	}
	c.mu.Lock()
	nativeID := c.nativeID
	if c.closed || nativeID == "" || p.ThreadID != nativeID {
		c.mu.Unlock()
		return
	}
	switch envelope.Method {
	case "item/started", "item/completed":
		// History compaction neither ends a logical session nor completes its
		// assignments. Keep exact item/turn identity without changing control
		// state; a delayed observation cannot clear a newer approval or turn.
		if p.Item.Type == "contextCompaction" && p.Item.ID != "" && p.TurnID != "" {
			kind := "compaction_started"
			if envelope.Method == "item/completed" {
				kind = "compaction_completed"
			}
			c.mu.Unlock()
			c.emit(Event{Kind: kind, NativeSessionID: p.ThreadID, TurnID: p.TurnID, ItemID: p.Item.ID})
			return
		}
		if envelope.Method == "item/completed" && p.Item.Type == "fileChange" {
			// Only an observation for renewal (D-078): no control state
			// changes, and nothing is emitted on the event channel.
			active := p.TurnID != "" && p.TurnID == c.activeTurn && c.ownTurns[p.TurnID]
			m, generation := c.renewals, c.generation
			c.mu.Unlock()
			if !active {
				return
			}
			paths, renew, lost := codexFileChangePaths(p.Item.Status, p.Item.Changes)
			if renew {
				m.offer(generation, paths)
			}
			if lost != "" {
				m.unknown(lost)
			}
			return
		}
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval", "item/tool/requestUserInput", "mcpServer/elicitation/request":
		key, err := codexRequestKey(envelope.ID)
		if err != nil || p.TurnID == "" || c.finished[p.TurnID] {
			c.mu.Unlock()
			return
		}
		a := Approval{ID: append(json.RawMessage(nil), envelope.ID...), NativeSessionID: p.ThreadID, TurnID: p.TurnID, ItemID: p.ItemID, NativeApprovalID: p.ApprovalID, Kind: envelope.Method, Detail: append(json.RawMessage(nil), envelope.Params...)}
		if previous, exists := c.seenApprovals[key]; exists {
			if previous.Kind == a.Kind && bytes.Equal(previous.Detail, a.Detail) {
				c.mu.Unlock()
				return
			}
			// A reused request ID could make a delayed operator answer name
			// different work. Disconnect rather than substitute the newer request.
			delete(c.approvals, key)
			c.closed = true
			c.state = "unknown"
			c.approvals = make(map[string]Approval)
			proc := c.process
			c.mu.Unlock()
			c.terminal(proc, Event{Kind: "disconnected", NativeSessionID: p.ThreadID, Err: "Codex reused an approval request ID for different work"})
			return
		}
		c.approvals[key] = a
		c.seenApprovals[key] = a
		c.mu.Unlock()
		c.emit(Event{Kind: "approval", NativeSessionID: p.ThreadID, TurnID: p.TurnID, Approval: &a})
		return
	case "serverRequest/resolved":
		if key, err := codexRequestKey(p.RequestID); err == nil {
			a, exists := c.approvals[key]
			delete(c.approvals, key)
			if exists {
				c.mu.Unlock()
				c.emit(Event{Kind: "approval_resolved", NativeSessionID: p.ThreadID, TurnID: a.TurnID, Approval: &a})
				return
			}
		}
	case "turn/started":
		if p.Turn.ID == "" || c.finished[p.Turn.ID] {
			c.mu.Unlock()
			return
		}
		c.activeTurn, c.state = p.Turn.ID, "busy"
		c.mu.Unlock()
		c.emit(Event{Kind: "busy", NativeSessionID: p.ThreadID, TurnID: p.Turn.ID})
		return
	case "turn/completed":
		if p.Turn.ID == "" || c.finished[p.Turn.ID] || (p.Turn.Status != "completed" && p.Turn.Status != "interrupted" && p.Turn.Status != "failed") {
			c.mu.Unlock()
			return
		}
		c.finished[p.Turn.ID] = true
		delete(c.ownTurns, p.Turn.ID)
		active := c.activeTurn == "" || c.activeTurn == p.Turn.ID
		if active {
			c.activeTurn, c.state = "", "idle"
		}
		for key, a := range c.approvals {
			if a.TurnID == p.Turn.ID {
				delete(c.approvals, key)
			}
		}
		c.mu.Unlock()
		c.emit(Event{Kind: "result", NativeSessionID: p.ThreadID, TurnID: p.Turn.ID, ResultRef: p.Turn.ID, ResultStatus: p.Turn.Status})
		if active {
			c.emit(Event{Kind: "idle", NativeSessionID: p.ThreadID, TurnID: p.Turn.ID})
		}
		return
	case "thread/status/changed":
		// A status without turn identity cannot clear a newer active turn. The
		// turn/completed event carries the comparison needed for that mutation.
		if p.Status.Type == "active" {
			c.state = "busy"
		}
	case "thread/archived", "thread/deleted":
		c.state, c.activeTurn = "ended", ""
		c.approvals = make(map[string]Approval)
		c.mu.Unlock()
		c.emit(Event{Kind: "ended", NativeSessionID: p.ThreadID})
		return
	case "thread/closed":
		// Unloading a thread is not archiving or ending its logical session.
		c.state, c.activeTurn = "unknown", ""
		c.closed = true
		c.approvals = make(map[string]Approval)
		proc := c.process
		c.mu.Unlock()
		c.terminal(proc, Event{Kind: "disconnected", NativeSessionID: p.ThreadID, Err: "Codex thread unloaded; logical end was not observed"})
		return
	}
	c.mu.Unlock()
}

// codexFileChangePaths classifies one fileChange item/completed. Measured on
// 0.160.0 (receipts of the 2026-10-13 rerun): changes[].path is absolute, a
// move's destination is kind.move_path on the same change, and kind.type is
// add, update or delete. Only status "completed" renews, and then every path
// and destination of the item, or none: a change whose shape is not that one
// means the item cannot be attributed, and nothing of it renews. failed,
// declined and inProgress are expected non-renewals; any other status is an
// unexpected shape, reported as lost coverage.
func codexFileChangePaths(status string, raw json.RawMessage) (paths []string, renew bool, lost string) {
	switch status {
	case "completed":
	case "failed", "declined", "inProgress":
		return nil, false, ""
	default:
		return nil, false, "a fileChange completed with an unrecognized status"
	}
	var changes []struct {
		Path string `json:"path"`
		Kind struct {
			Type     string          `json:"type"`
			MovePath json.RawMessage `json:"move_path"`
		} `json:"kind"`
	}
	if json.Unmarshal(raw, &changes) != nil || len(changes) == 0 {
		return nil, false, "a completed fileChange named no readable changes"
	}
	for _, ch := range changes {
		switch ch.Kind.Type {
		case "add", "update", "delete":
		default:
			return nil, false, "a completed fileChange carried an unrecognized change kind"
		}
		if ch.Path == "" {
			return nil, false, "a completed fileChange named an empty path"
		}
		paths = append(paths, ch.Path)
		if ch.Kind.MovePath != nil && string(ch.Kind.MovePath) != "null" {
			dest, ok := jsonString(ch.Kind.MovePath)
			if !ok || dest == "" {
				return nil, false, "a completed fileChange carried an unreadable move destination"
			}
			paths = append(paths, dest)
		}
	}
	return paths, true, ""
}

func (c *codexBackend) finish() {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.state = "unknown"
		c.activeTurn = ""
		c.approvals = make(map[string]Approval)
		c.mu.Unlock()
		close(c.done)
	})
}

func (c *codexBackend) Close() error {
	c.finish()
	c.stopOnce.Do(func() { close(c.stop) })
	c.mu.Lock()
	proc := c.process
	c.mu.Unlock()
	if proc != nil {
		return proc.Close()
	}
	return nil
}
