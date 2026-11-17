package harness

// The app-server owns Codex processes and turns; Buddy only consumes translated
// observations. One backend owns one launched thread. This intentionally cannot
// attach to another client's thread or turn a transport exit into SessionEnd.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/JsizzleR/buddy-system/internal/fence"
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
	executable string
	start      codexStarter
	version    func(context.Context, string) (string, error)
	opMu       sync.Mutex
	mu         sync.Mutex
	process    codexProcess
	nativeID   string
	activeTurn string
	state      string
	started    bool
	closed     bool
	seq        uint64
	pending    map[string]chan codexReply
	approvals  map[string]Approval
	// seenApprovals keeps, per request key, the kind and a digest of the
	// exact params for the reuse rule, never the params: a request flood
	// with 4 MiB params held every one for the worker's life (adapter-drops,
	// the panel's hostile-server lens).
	seenApprovals map[string]codexSeen
	// refused holds, for the turn in progress, a digest of each request the
	// adapter refused or left unlisted (adapter-drops): an identical replay
	// publishes nothing, and past codexRefusalCap distinct ones the
	// transport ends. Cleared when a turn goes idle.
	refused   map[[32]byte]bool
	finished  map[string]bool
	events    chan Event
	done      chan struct{}
	stop      chan struct{}
	closeOnce sync.Once
	stopOnce  sync.Once
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
	// The admitted record's version, once a launch has been admitted; the
	// capability report intersects with that record's tiers (D-079).
	launchedVersion string
	ownTurns        map[string]bool
}

func (c *codexBackend) observeRenewals(m *renewalMailbox) {
	c.mu.Lock()
	c.renewals = m
	c.mu.Unlock()
}

// codexStatusCap bounds the thread status type inspect carries into Detail.
// The native values are short identifiers; 64 is the label/room cap
// (invariant 9) and leaves room for one a later build adds.
const codexStatusCap = 64

type codexReply struct {
	result json.RawMessage
	err    error
}

// codexRPCError is the server's own error reply to a request: the request
// reached it and was answered, unlike a transport loss or a timeout, whose
// outcome is unknown. Inspect tells the two apart (D-197).
//
// raw is the server's JSON, up to the 4 MiB frame bound, and Error() is what
// becomes a receipt's error on the journal and the coordinator's stream
// (D-198): it is fenced to one line and bounded there, once, where it is
// made. raw itself stays exact for whoever inspects the type.
type codexRPCError struct{ raw json.RawMessage }

func (e *codexRPCError) Error() string {
	return "Codex RPC rejected request: " + fence.Line(string(e.raw), nativeErrorCap)
}

type codexEnvelope struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

// codexSeen is what the reuse rule compares for a request key: its kind and
// the digest of its exact params.
type codexSeen struct {
	kind   string
	digest [32]byte
}

// Bounds on what a native server can make the adapter hold (adapter-drops).
// Measured, Codex 0.160.0 asks at most 2 approvals per run (the A-07
// rerun's 19 requests); 64 is the room cap (invariant 9), far past a real
// turn and far short of a flood. Past either, the transport ends naming the
// count: a server asking that much is not one the operator can follow.
const (
	codexPendingCap = 64
	codexRefusalCap = 64
)

// Decoding the Codex wire (adapter-drops). nativeEvent decoded every method
// into ONE struct, so a field one method types differently from another
// failed the whole Unmarshal and dropped the notification whole: `"turnId": 7`
// on turn/completed left the adapter busy for good (D-202's Open). Measured on
// the 0.160.0 wire, remoteControl/status/changed sends `status` as a STRING
// where thread/status/changed sends an object: the class, live. Go struct
// tags also match keys case-insensitively, so {"turn":{"id":"t1","ID":"t2"}}
// read t2 in a struct and t1 in an exact reader; the Python checkers read
// exact keys. So the wire is read through these: exact keys, each method its
// own fields, and a field's JSON kind named from its first byte, a fixed
// vocabulary an error can carry without echoing the value.

// jsonKind names a raw value's kind: absent, null, string, number, object,
// array or bool. Never the value.
func jsonKind(raw json.RawMessage) string {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 {
		return "absent"
	}
	switch t[0] {
	case 'n':
		return "null"
	case '"':
		return "string"
	case '{':
		return "object"
	case '[':
		return "array"
	case 't', 'f':
		return "bool"
	}
	return "number"
}

// codexObject reads an exact-key JSON object; a null, an array or a scalar
// is not one (a map decode would take null as an empty object).
func codexObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || t[0] != '{' {
		return nil, false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(t, &m) != nil {
		return nil, false
	}
	return m, true
}

// codexStr reads the string at an exact-key path. kind is "string" only
// when the value at the end of the path is one; otherwise that value's kind,
// and "absent" when the path cannot reach it (a missing key, or an
// intermediate that is no object: `"turn": "x"` has no turn.id, and must not
// report the intermediate's "string" as the field's).
func codexStr(obj map[string]json.RawMessage, path ...string) (s, kind string) {
	var raw json.RawMessage
	for i, key := range path {
		if i > 0 {
			next, ok := codexObject(raw)
			if !ok {
				return "", "absent"
			}
			obj = next
		}
		raw = obj[key]
	}
	if v, ok := jsonString(raw); ok {
		return v, "string"
	}
	return "", jsonKind(raw)
}

// codexDecodeEnvelope reads a frame's id, method, params, result and error
// by exact key. A frame that is no object, or whose method is present and
// neither a string nor null, is malformed, as a struct decode made it; a
// bare `null` frame, which a struct decode let through as empty, is too.
func codexDecodeEnvelope(raw json.RawMessage) (codexEnvelope, bool) {
	obj, ok := codexObject(raw)
	if !ok {
		return codexEnvelope{}, false
	}
	e := codexEnvelope{ID: obj["id"], Params: obj["params"], Result: obj["result"], Error: obj["error"]}
	if m, present := obj["method"]; present && jsonKind(m) != "null" {
		s, ok := jsonString(m)
		if !ok {
			return codexEnvelope{}, false
		}
		e.Method = s
	}
	return e, true
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
		pending: make(map[string]chan codexReply), approvals: make(map[string]Approval), seenApprovals: make(map[string]codexSeen), refused: make(map[[32]byte]bool),
		finished: make(map[string]bool), events: make(chan Event, 128), done: make(chan struct{}), stop: make(chan struct{}),
		pendingMethod: make(map[string]string), ownTurns: make(map[string]bool)}
}

// Capabilities is the code's support intersected with the launched
// version's admission record (D-079). Before a launch only the code's own
// support is known; reconcile is withdrawn whatever any record says.
func (c *codexBackend) Capabilities() Capabilities {
	c.mu.Lock()
	version := c.launchedVersion
	c.mu.Unlock()
	resume := Capability{Support: "experimental", Reason: "thread/resume by captured native thread ID; native recovery not qualified"}
	compact := Capability{Support: "experimental", Reason: "contextCompaction item lifecycle only; no automatic retry or compaction control"}
	if version == "" {
		pending := Capability{Support: "unknown", Reason: "decided by the admission record of the build a launch admits"}
		resume, compact = pending, pending
	} else if !admissionVersionGrants("codex", version, TierResume) {
		resume = Capability{Support: "unsupported", Reason: "the launched version's admission record does not grant resume"}
	}
	if version != "" && !admissionVersionGrants("codex", version, TierCompaction) {
		compact = Capability{Support: "unsupported", Reason: "the launched version's admission record does not grant compaction_observation"}
	}
	return Capabilities{
		"start":                  {Support: "experimental", Reason: "Codex app-server v2 contract (" + codexAdapterContract + ") for exactly the admitted packages; launched workers only"},
		"send":                   {Support: "experimental", Reason: "turn/start or exact-turn turn/steer; native acceptance is not worker acknowledgement"},
		"inspect":                {Support: "experimental", Reason: "thread/read runtime status; the latest turn's id and status from thread/turns/list (limit 1, itemsView notLoaded), progress only, never completion, is sent to no admitted build until an admission measures that it honours notLoaded; not a ledger or validation verdict"},
		"reconcile":              {Support: "unsupported", Reason: "the 0.160.0 history read (thread/read includeTurns) is unbounded; an oversized reply would end the live worker's transport, and a paged read is not qualified"},
		"resume":                 resume,
		"interrupt":              {Support: "experimental", Reason: "turn/interrupt with explicit native thread and turn IDs"},
		"approval":               {Support: "experimental", Reason: "exact request-scoped command/file allow or deny only; no session or policy grants"},
		"attach":                 {Support: "unsupported", Reason: "only the worker launched by this adapter is controllable"},
		"permission_grants":      {Support: "unsupported", Reason: "permission-profile requests are surfaced but never translated into broad grants"},
		"peer_identity":          {Support: "unsupported", Reason: "wake text is explicitly peer data; app-server text input is not a native authenticated peer envelope"},
		"wake":                   {Support: "experimental", Reason: "bounded peer inbox notification over the owned thread input transport"},
		"telemetry":              {Support: "unknown", Reason: "no usage or cache translation in this bounded adapter"},
		"compaction_observation": compact,
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
	checked, err := observeExecutable(c.executable)
	if err != nil {
		return "", err
	}
	// Admission before the file runs at all (D-079): bin/codex AND the whole
	// release tree around it must be a committed record's, because the
	// package execs its sidecars (codex-code-mode-host, codex-path/rg) later.
	op := TierStart
	if resumeID != "" {
		op = TierResume
	}
	rec, err := admitExecutable("codex", c.executable, checked, launch, op)
	if err != nil {
		return "", err
	}
	version, err := c.version(ctx, c.executable)
	if err != nil || version != rec.VersionOutput {
		return "", refuse("version_mismatch", "installed Codex does not report %q", rec.VersionOutput)
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
	c.launchedVersion = rec.Version
	c.nativeID = resumeID
	c.generation = auth.generation()
	c.mu.Unlock()
	// The caller supplies the isolated home. No user configuration or hook trust
	// is installed or rewritten by a control adapter.
	proc, err := c.start(ctx, auth, c.executable, []string{"app-server", "--stdio"}, launch.Worktree, []string{"CODEX_HOME=" + filepath.Dir(launch.HookConfig), "DISABLE_AUTOUPDATER=1"})
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
	// The thread id is the identity: every request's threadId, the ledger's
	// session id (lifecycle begin --session), resume's match, the wake text.
	// It takes the ledger's own predicate (D-201), checked HERE, before
	// ledger.Begin: a thread id the ledger would refuse, refused after the
	// child is up, reads as an uncertain registration (D-202). Close kills
	// the owned child; nothing has reached the ledger or a receipt.
	if !fence.ValidSessionID(result.Thread.ID) {
		_ = c.Close()
		return "", fmt.Errorf("Codex start/resume returned a native thread ID outside the session-id rule (%s; %d bytes)", fence.SessionIDRule, len(result.Thread.ID))
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
	// The reader's own reading of this reply (adapter-drops): per method,
	// exact keys, so a field the method never uses cannot fail it.
	turnID := codexReplyTurnID(method, raw)
	if turnID == "" || (method == "turn/steer" && turnID != turn) {
		return Delivery{State: "unknown"}, errors.New("Codex input reply has a missing or mismatched turn ID")
	}
	c.mu.Lock()
	if !c.finished[turnID] && !c.closed {
		c.activeTurn, c.state = turnID, "busy"
	}
	c.mu.Unlock()
	return Delivery{State: "delivered", TurnID: turnID, ResultRef: turnID}, nil
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
	// Read-only inspection never overwrites newer event-stream state. The
	// status type is the server's free string, and it is Detail's first
	// words: unfenced, a newline in it forged a receipt line and its length
	// was the server's to pick, while only the progress suffix below was
	// held to a charset (Fable cross-check of D-197). It goes through the
	// one fence, bounded; the native values are short identifiers (idle,
	// active, notLoaded, systemError), and State reads the raw value, which
	// it only ever compares.
	obs := Observation{State: codexState(result.Thread.Status.Type), Detail: fence.Line(result.Thread.Status.Type, codexStatusCap)}
	c.mu.Lock()
	version := c.launchedVersion
	c.mu.Unlock()
	if !codexTurnsListBuilds[version] {
		return obs, nil
	}
	progress, err := c.latestTurn(ctx, nativeID)
	if err != nil {
		var rejected *codexRPCError
		if errors.As(err, &rejected) {
			// A build without the method (or with other params) rejects the
			// request: that is today's answer, never an error or a refusal.
			return obs, nil
		}
		return Observation{State: "unknown"}, err
	}
	if progress != "" {
		obs.Detail += "; " + progress
	}
	return obs, nil
}

// codexTurnsListBuilds names the launched versions (the admitted record's
// Version) on which inspect sends thread/turns/list. Empty, on purpose: the
// fallback below covers a build that REJECTS the request, not one that
// accepts it and ignores itemsView notLoaded. Such a build answers with the
// latest turn's items; past the 4 MiB frame bound that frame ends the
// transport and with it the live worker, mid-turn, where thread/read alone
// was bounded. That is the unqualified read reconcile is withdrawn for. The
// admitted builds (0.162.1, 0.164.0, 0.165.0) carry the method's strings, but
// nobody has measured that they honour notLoaded, so every one of them, and
// an unknown version, gets today's thread/read answer exactly (Fable
// cross-check of D-197). A version is added here, in reviewed code, only by
// an admission that measured it; D-197 says what that measures. Keyed by
// version, not tier: a record cannot grant it.
var codexTurnsListBuilds = map[string]bool{}

// latestTurn is inspect's bounded progress read (D-197, #106). The issue
// proposed 0.169.0's thread/sample; its schema refutes that: thread/sample is
// experimental-only and runs a NEW model response over the thread from a
// required prompt, replying with assistant text the schema does not bound. An
// inspect built on it would spend a provider request per look and put model
// text, steered by the worker's untrusted context, into an observation. What
// inspect wants is the latest turn's id and status: the stable
// thread/turns/list with limit 1, newest first, itemsView notLoaded, which
// returns one Turn without its items (the string is in the 0.162.1, 0.164.0
// and 0.165.0 binaries; its params there are not schema-checked, hence the
// fallback above). The status is reported as PROGRESS in Detail and never
// moves State: an inProgress or completed turn is activity, not a verdict on
// the work. Only an enumerated status and a plain id are carried; anything
// else reports nothing, not an error. The 4 MiB frame bound stands: a server
// that ignored notLoaded and sent a frame past it would end the transport, as
// any oversized frame does, which is why this is sent only to a build in
// codexTurnsListBuilds, and no test pretends otherwise (D-197).
func (c *codexBackend) latestTurn(ctx context.Context, nativeID string) (string, error) {
	raw, err := c.call(ctx, "thread/turns/list", map[string]any{"threadId": nativeID, "limit": 1, "sortDirection": "desc", "itemsView": "notLoaded"})
	if err != nil {
		return "", err
	}
	// Data is a pointer so that {}, null and {"data":null} carry nothing:
	// only an explicit empty array says no turn is listed.
	var page struct {
		Data *[]struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &page) != nil || page.Data == nil || len(*page.Data) > 1 {
		return "", nil
	}
	if len(*page.Data) == 0 {
		return "no turn listed", nil
	}
	turn := (*page.Data)[0]
	switch turn.Status {
	case "inProgress", "completed", "interrupted", "failed":
	default:
		return "", nil
	}
	if !nativeKey(turn.ID) {
		return "", nil
	}
	return "latest turn " + turn.ID + " " + turn.Status + " (progress, not completion)", nil
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
	// By exact key, as admission reads it (codexOffersDecline): a struct
	// tag matched `AvailableDecisions` too, and a later `null` alias erased
	// a list that offered only decline, so allow sent accept (the Codex code
	// pass, adapter-drops).
	detail, ok := codexObject(a.Detail)
	if !ok {
		return nil, nil, errors.New("Codex approval detail is malformed")
	}
	if raw, present := detail["availableDecisions"]; present && jsonKind(raw) != "null" {
		options, ok := jsonArray(raw)
		if !ok {
			return nil, nil, errors.New("Codex approval detail is malformed")
		}
		available := false
		for _, option := range options {
			value, ok := jsonString(option)
			if !ok {
				// A list the adapter cannot read whole is no offer: skipping
				// the non-string let ["accept",7] admit accept (the Codex
				// re-verify).
				return nil, nil, errors.New("Codex approval detail is malformed")
			}
			if value == native {
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
		// Only inside nativeKey, so a pending key is bounded and plain
		// (D-202). Real approval ids are integers (0.160.0); string ids are
		// the fixtures', and the replies to our own requests carry our
		// decimal ids.
		if nativeKey(v) {
			return "s:" + v, nil
		}
		return "", errors.New("Codex native request ID is a string outside the plain key set")
	case json.Number:
		// Only the canonical decimal spelling (D-202). The key was the raw
		// spelling while the controller's approvalIDKey keys the parsed
		// value, so `0` and `-0` were two pending approvals here and one
		// there: the receipt listed one, the other stayed natively pending
		// and unlisted, and an answer the coordinator re-spelled (JSON.stringify
		// of -0 is "0") approved the request the receipt did not show. A
		// non-canonical id is refused, never re-spelled: re-spelling would make
		// the two collide here as well and answer a request by a spelling
		// Codex never sent. Measured: Codex 0.160.0 sends 0 and 1.
		if n, err := v.Int64(); err == nil && strconv.FormatInt(n, 10) == string(v) {
			return "n:" + string(v), nil
		}
	}
	return "", errors.New("Codex native request ID must be a string or a canonical int64")
}

// codexCanonicalID is the request id an Approval carries: the JSON of the
// DECODED key, so a string id spelled with \u escapes renders at most 130
// bytes, not up to six times that, and every receipt spells one request one
// way. Codex decodes the id it is answered with, so the answer is unchanged.
func codexCanonicalID(key string) json.RawMessage {
	if v, ok := strings.CutPrefix(key, "s:"); ok {
		raw, _ := json.Marshal(v)
		return raw
	}
	return json.RawMessage(strings.TrimPrefix(key, "n:"))
}

// codexReplyTurnID reads the turn id a turn/start (turn.id) or turn/steer
// (turnId) reply names, each method its own field by exact key: one struct
// for both made a mistyped field the method never uses (a turn/start reply's
// `"turnId": 7`) fail the decode, and the reader then ended the transport
// over a delivered turn (adapter-drops). Send reads the same function, so
// the reader and Send cannot disagree; Send applies its own missing and
// mismatch checks.
func codexReplyTurnID(method string, result json.RawMessage) string {
	obj, ok := codexObject(result)
	if !ok {
		return ""
	}
	path := []string{"turnId"}
	if method == "turn/start" {
		path = []string{"turn", "id"}
	}
	id, _ := codexStr(obj, path...)
	return id
}

// codexKeyFault is the error a key fault ends the transport with: the field
// and its length, never the value; the plain active turn it ended; and what
// stays true, so the ordinary "disconnected" receipt is not read as EOF.
func codexKeyFault(where, field, value, active string) string {
	msg := where + ": " + nativeKeyRefusal(field, value) + "; the adapter cannot address it and ended the transport"
	return codexEndTail(msg, active)
}

// codexEnded is the error every other fault the adapter ends the transport
// over carries (adapter-drops): a fixed leading phrase, so it reads apart from
// EOF ("native output stream closed") and a server's own disconnect, then the
// method (always a case's literal, never the frame's method), the field and
// its JSON kind, never the value; then D-202's tail.
func codexEnded(msg, active string) string {
	return codexEndTail("the adapter ended the transport: "+msg, active)
}

func codexEndTail(msg, active string) string {
	if active != "" {
		msg += "; active turn " + active + " ended unobserved"
	}
	return msg + "; the logical session is not ended and stays live in the ledger, and a wake in flight stays unknown"
}

// codexKindPhrase names a kind in a sentence: "absent", "null", "empty" (a
// string with no bytes), or "a JSON number".
func codexKindPhrase(kind string) string {
	if kind == "absent" || kind == "null" || kind == "empty" {
		return kind
	}
	return "a JSON " + kind
}

// endLocked ends the transport with the error text made from the active
// turn it ends. Called with c.mu held, from the reader; it unlocks.
func (c *codexBackend) endLocked(text func(active string) string) {
	active := c.activeTurn
	c.closed, c.state, c.activeTurn = true, "unknown", ""
	c.approvals = make(map[string]Approval)
	proc := c.process
	nativeID := c.nativeID
	c.mu.Unlock()
	c.terminal(proc, Event{Kind: "disconnected", NativeSessionID: nativeID, Err: text(active)})
}

// keyFault ends the transport over a turn id the adapter would have to store
// and send back (D-202), the path a reused request id takes. Called with c.mu
// held, from the reader; it unlocks.
func (c *codexBackend) keyFault(where, field, value string) {
	c.endLocked(func(active string) string { return codexKeyFault(where, field, value, active) })
}

// fieldFault ends the transport over a field a state-moving method needs and
// the adapter cannot read (adapter-drops): dropping the notification is what
// left the adapter busy for good. method is the case's literal. Called with
// c.mu held; it unlocks.
func (c *codexBackend) fieldFault(method, field, kind, want string) {
	c.endLocked(func(active string) string {
		return codexEnded("Codex "+method+" "+field+" is "+codexKindPhrase(kind)+", where "+want+" was expected", active)
	})
}

// codexDigest identifies one native request for the refusal set: its method,
// its id's exact bytes and its params' exact bytes.
func codexDigest(method string, id, params json.RawMessage) [32]byte {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte{0})
	h.Write(bytes.TrimSpace(id))
	h.Write([]byte{0})
	h.Write(params)
	var d [32]byte
	copy(d[:], h.Sum(nil))
	return d
}

// codexOffersDecline reports whether a command or file-change request
// offers `decline`; no availableDecisions list means every decision is.
func codexOffersDecline(method string, params map[string]json.RawMessage) bool {
	if method != "item/commandExecution/requestApproval" && method != "item/fileChange/requestApproval" {
		return false
	}
	raw, present := params["availableDecisions"]
	if !present || jsonKind(raw) == "null" {
		return true
	}
	options, ok := jsonArray(raw)
	if !ok {
		return false
	}
	for _, option := range options {
		if value, ok := jsonString(option); ok && value == "decline" {
			return true
		}
	}
	return false
}

// refuseApproval handles an approval request the adapter cannot list: its id
// cannot be a key (D-202: a string outside nativeKey, a number that is not a
// canonical int64, or a kind that is no id at all) or it carries no turn id
// (adapter-drops; it was dropped, natively pending and unlisted). Never the
// worker's end by itself where something can release it:
//   - a command or file change offering decline, by an id that can be
//     answered: declined natively by the id exactly as Codex sent it, and
//     published as approval_refused (answered; the worker keeps running);
//   - otherwise nothing can be answered (permissions, user input and
//     elicitation have no answer the adapter supports even with a good id;
//     takeApproval refuses them): it is left natively pending ONLY when the
//     backend tracks an active turn that is the request's own (or the
//     request names none), and published as observation_gap naming that
//     turn: the controller then reads pending approvals as unknown, never
//     absent, and Interrupt accepts that turn id. Ending the transport for it
//     (D-202) killed the turn's work over a request a good id would only
//     have listed unanswerable. Where no tracked turn is its turn, nothing
//     the receipt could name would release it: the transport ends.
//
// A request is refused once per turn: an identical replay publishes nothing
// (a decline is written again, the server asked again), and past
// codexRefusalCap distinct ones the transport ends. Called with c.mu held; it
// unlocks.
func (c *codexBackend) refuseApproval(method string, envelope codexEnvelope, params map[string]json.RawMessage, turnID, itemID string, keyErr error, finished bool) {
	trimmed := bytes.TrimSpace(envelope.ID)
	idKind := jsonKind(trimmed)
	reason := "carries no turn id"
	if finished {
		// turnID passed nativeKey when its turn was tracked.
		reason = "names turn " + turnID + ", which has finished"
	}
	value := string(trimmed)
	if keyErr != nil {
		switch idKind {
		case "string":
			s, _ := jsonString(trimmed)
			value = s // the decoded length, on every path
			reason = nativeKeyRefusal("id", s)
		case "number":
			reason = "id is not a canonical int64 (" + strconv.Itoa(len(trimmed)) + " bytes)"
		default:
			reason = "id is " + codexKindPhrase(idKind) + ", which cannot be answered"
		}
		if turnID == "" {
			reason += " and it carries no turn id"
		} else if finished {
			reason += " and it names turn " + turnID + ", which has finished"
		}
	}
	answerable := idKind == "string" || idKind == "number"
	digest := codexDigest(method, trimmed, envelope.Params)
	replay := c.refused[digest]
	if !replay && len(c.refused) >= codexRefusalCap {
		c.endLocked(func(active string) string {
			return codexEnded("Codex sent "+strconv.Itoa(codexRefusalCap)+" requests since the last idle the adapter could not list", active)
		})
		return
	}
	if answerable && codexOffersDecline(method, params) {
		c.refused[digest] = true
		proc := c.process
		nativeID := c.nativeID
		c.mu.Unlock()
		if writeRefusal(proc, map[string]any{"id": json.RawMessage(append([]byte(nil), trimmed...)), "result": map[string]string{"decision": "decline"}}) != nil {
			// Not answered, so no receipt says "declined". A failed Encode has
			// closed the transport, but a deadline that ran out waiting for the
			// write lock has not, and the request would stay natively pending
			// with nothing listed: end it here either way.
			c.mu.Lock()
			if keyErr != nil {
				c.keyFault("Codex "+method+" (its decline could not be written)", "request id", value)
				return
			}
			c.endLocked(func(active string) string {
				return codexEnded("Codex "+method+" request "+reason+": its decline could not be written", active)
			})
			return
		}
		if !replay {
			c.emit(Event{Kind: "approval_refused", NativeSessionID: nativeID, TurnID: turnID, ItemID: displayNativeID(itemID), Err: "Codex approval request " + reason + "; declined natively, the worker keeps running"})
		}
		return
	}
	if replay {
		c.mu.Unlock()
		return
	}
	active := c.activeTurn
	if turnID == "" {
		// A request naming no turn is not the active turn's: scoped to it,
		// that turn's idle cleared the gap and every later receipt read
		// absent while the request still waited (the Fable cross-check).
		// Unscoped, as unlistedRequest, and never an end by itself: an
		// elicitation need not belong to a turn (unmeasured).
		c.refused[digest] = true
		nativeID := c.nativeID
		c.mu.Unlock()
		c.emit(Event{Kind: "observation_gap", NativeSessionID: nativeID, ItemID: displayNativeID(itemID),
			Err: "Codex " + method + " request " + reason + "; no native refusal is on offer, so it stays pending natively and is not listed, and no turn of its own can be interrupted"})
		return
	}
	if active == "" || turnID != active {
		if keyErr != nil && idKind == "string" {
			// D-202's wording, kept for the key refusal it names.
			c.keyFault("Codex "+method+" (no native refusal on offer and no tracked turn of its own to interrupt)", "request id", value)
			return
		}
		c.endLocked(func(active string) string {
			return codexEnded("Codex "+method+" request "+reason+": no native refusal is on offer and no tracked turn of its own can be interrupted", active)
		})
		return
	}
	c.refused[digest] = true
	nativeID := c.nativeID
	c.mu.Unlock()
	c.emit(Event{Kind: "observation_gap", NativeSessionID: nativeID, TurnID: active, ItemID: displayNativeID(itemID),
		Err: "Codex " + method + " request " + reason + "; no native refusal is on offer, so it stays pending natively and is not listed; interrupting turn " + active + " is the only control on offer (whether that releases the request is unmeasured)"})
}

// unlistedRequest handles a REQUEST (it carries an id) the adapter does not
// handle: a method outside the five approval methods, or one of them for
// another thread or none it can read (adapter-drops). On app-server stdio the
// adapter is the only client, so a request it drops stays natively pending
// for good; some may block no turn at all (an auth refresh: unmeasured), so
// it is never an end by itself. Nothing is written (how Codex treats a
// JSON-RPC error answer is unmeasured) and an UNSCOPED observation_gap is
// published, no turn named, which the controller clears only at the end of
// the logical session or the transport. what is already safe to render: a
// case's literal or fence.Field of the method; another thread's id is never
// echoed. Measured: 0 such requests in the 1,458 frames Codex 0.160.0 sent the
// A-07 rerun's adapters (its 19 requests were all command approvals). Called
// with c.mu held; it unlocks.
func (c *codexBackend) unlistedRequest(what string, envelope codexEnvelope) {
	digest := codexDigest(what, envelope.ID, envelope.Params)
	if c.refused[digest] {
		c.mu.Unlock()
		return
	}
	if len(c.refused) >= codexRefusalCap {
		c.endLocked(func(active string) string {
			return codexEnded("Codex sent "+strconv.Itoa(codexRefusalCap)+" requests since the last idle the adapter could not list", active)
		})
		return
	}
	c.refused[digest] = true
	nativeID := c.nativeID
	c.mu.Unlock()
	c.emit(Event{Kind: "observation_gap", NativeSessionID: nativeID,
		Err: "Codex " + what + "; the adapter does not handle it, answered nothing and listed nothing, and it may stay pending natively for the transport's life"})
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

// repairIdle is the one place a thread/read answer moves the backend
// (adapter-drops): a thread the server reports idle, while the backend is
// busy with NO tracked turn, becomes idle. Busy with no turn is reached by a
// thread/status/changed `active` (a replayed one, after turn/completed) and
// by a resume into an active thread; from there Send refused every wake
// ("active turn is unknown; inspect before sending") and inspect moved
// nothing, so the advice had no path. It runs on the reader, where the reply
// is claimed, so it is ordered against every notification: done from
// Inspect's goroutine, a thread/status/changed `active` the reader took after
// the read (a server-started turn, auto-compact-0 measured) would be
// overwritten by the older idle, and Send would turn/start into a running
// turn (orc-3's condition). Only from state "busy" exactly: never from ended
// (thread/archived leaves closed false) or unknown, never with a tracked turn
// (turn/completed is that turn's authority), never once closed. The
// controller is told nothing: its state is not moved by inspect (D-197).
// Adding a build to codexTurnsListBuilds must revisit this: its progress
// read is a second answer about the same turn.
func (c *codexBackend) repairIdle(result json.RawMessage) {
	obj, ok := codexObject(result)
	if !ok {
		return
	}
	thread, _ := codexStr(obj, "thread", "id")
	status, _ := codexStr(obj, "thread", "status", "type")
	c.mu.Lock()
	if status == "idle" && thread != "" && thread == c.nativeID && !c.closed && c.state == "busy" && c.activeTurn == "" {
		c.state = "idle"
	}
	c.mu.Unlock()
}

func (c *codexBackend) readLoop(proc codexProcess) {
	defer close(c.events)
	for raw := range proc.Read() {
		envelope, ok := codexDecodeEnvelope(raw)
		if !ok {
			c.terminal(proc, Event{Kind: "disconnected", Err: "malformed Codex JSON frame"})
			return
		}
		if envelope.Method == "" {
			key, err := codexRequestKey(envelope.ID)
			if err != nil {
				continue
			}
			// The reader claims the slot: a duplicate reply finds none, so it
			// can neither reach Send nor mark a second turn ours (a buffered
			// channel Send has already drained would take it). Send's own
			// deferred delete is then a no-op.
			c.mu.Lock()
			ch := c.pending[key]
			method := c.pendingMethod[key]
			delete(c.pending, key)
			delete(c.pendingMethod, key)
			c.mu.Unlock()
			failed := len(envelope.Error) > 0 && string(envelope.Error) != "null"
			// A turn reply's id is decided HERE, on the reader, never in Send
			// (D-202). A turn id outside nativeKey names a turn the adapter can
			// never interrupt, steer or approve, while the worker runs it: end
			// the transport, and Send, never handed this reply, sees the
			// disconnect and keeps the wake unknown. Ending it from Send would
			// put a terminal event on c.events from a second goroutine, racing
			// this loop's deferred close(c.events): a send on a closed channel.
			turn := ""
			if ch != nil && !failed && (method == "turn/start" || method == "turn/steer") {
				turn = codexReplyTurnID(method, envelope.Result)
				// Empty fails it too: a delivered turn the adapter cannot name.
				if !nativeKey(turn) {
					c.mu.Lock()
					active := c.activeTurn
					c.closed, c.state, c.activeTurn = true, "unknown", ""
					c.approvals = make(map[string]Approval)
					c.mu.Unlock()
					c.terminal(proc, Event{Kind: "disconnected", Err: codexKeyFault("Codex "+method+" reply", "turn id", turn, active)})
					return
				}
			}
			// Before the caller is handed the reply, so an inspect that
			// returns has already seen its own repair (mutation check).
			if ch != nil && !failed && method == "thread/read" {
				c.repairIdle(envelope.Result)
			}
			if ch != nil {
				reply := codexReply{result: envelope.Result}
				if failed {
					reply.err = &codexRPCError{raw: append(json.RawMessage(nil), envelope.Error...)}
				}
				select {
				case ch <- reply:
					// Only the reply that claimed Send's slot marks its turn
					// ours: a duplicate reply naming another turn used to
					// mark that one too, letting a turn the adapter never
					// reported renew claims (D-078) once it started (D-202).
					// The slot is buffered, so a reply landing after Send's
					// ctx ended still marks it: that turn is still the one
					// this launch requested.
					//
					// The reader also installs it as the active turn, here,
					// before it reads the next frame (adapter-drops, Codex
					// design pass): Send set it only once scheduled, so an
					// approval request read right after this reply found no
					// active turn and could be refused as unaddressable.
					// Send's own set stays, the same value.
					if method == "turn/start" && turn != "" {
						c.mu.Lock()
						c.ownTurns[turn] = true
						if !c.closed && !c.finished[turn] {
							c.activeTurn, c.state = turn, "busy"
						}
						c.mu.Unlock()
					}
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

// nativeEvent reads one notification or server request, each method its own
// fields by exact key (adapter-drops; the helpers above say why). What a
// field the method needs does when it cannot be read is decided by what the
// adapter does with it:
//   - state-moving (busy, idle, ended, a request the worker waits on): the
//     transport ends, naming the method, the field and its kind; a dropped
//     turn/completed left the adapter busy for good;
//   - a request the adapter cannot list: refuseApproval or unlistedRequest;
//   - an observation (a compaction item, a status flag, a resolution): the
//     observation is dropped, and a renewal it would have offered reports
//     coverage unknown;
//   - display-only (an item or approval id): the field is left out.
//
// A method the adapter does not use is never decoded past the envelope.
func (c *codexBackend) nativeEvent(envelope codexEnvelope) {
	method := envelope.Method
	if kind := jsonKind(envelope.ID); kind != "absent" && kind != "null" && !codexApprovalMethod(method) {
		// A frame with an id is a request, whatever its method: read as a
		// notification it moved state (a thread/status/changed `active`)
		// while the request stayed natively pending and unlisted (the Codex
		// code pass, adapter-drops).
		c.mu.Lock()
		if c.closed || c.nativeID == "" {
			c.mu.Unlock()
			return
		}
		c.unlistedRequest("request "+fence.Field(method, nativeTokenCap)+" (a method the adapter does not handle as a request)", envelope)
		return
	}
	switch method {
	case "turn/started", "turn/completed", "thread/archived", "thread/deleted", "thread/closed",
		"item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval", "item/tool/requestUserInput", "mcpServer/elicitation/request":
		c.stateEvent(method, envelope)
	case "item/started", "item/completed", "serverRequest/resolved", "thread/status/changed":
		c.observationEvent(method, envelope)
	}
	// Any other method here is a notification the adapter does not use:
	// nothing waits on it. A request was handled above.
}

func codexApprovalMethod(method string) bool {
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval", "item/tool/requestUserInput", "mcpServer/elicitation/request":
		return true
	}
	return false
}

// stateEvent: method is one of nativeEvent's state-moving literals.
func (c *codexBackend) stateEvent(method string, envelope codexEnvelope) {
	params, isObject := codexObject(envelope.Params)
	c.mu.Lock()
	nativeID := c.nativeID
	if c.closed || nativeID == "" {
		c.mu.Unlock()
		return
	}
	approval := codexApprovalMethod(method)
	if idKind := jsonKind(envelope.ID); approval && (idKind == "absent" || idKind == "null") {
		// {"id":null} or no id: a notification, nothing waits (D-202).
		c.mu.Unlock()
		return
	}
	thread, threadKind := "", jsonKind(envelope.Params)
	if isObject {
		thread, threadKind = codexStr(params, "threadId")
	}
	if !isObject || threadKind != "string" || thread == "" {
		if approval {
			// A request with no thread the adapter can read: an MCP
			// elicitation need not belong to one (unmeasured), so it is
			// never an end by itself.
			c.unlistedRequest(method+" request with no readable thread id", envelope)
			return
		}
		if !isObject {
			c.fieldFault(method, "params", jsonKind(envelope.Params), "an object")
			return
		}
		if threadKind == "string" {
			threadKind = "empty" // "is a JSON string" would misname it
		}
		c.fieldFault(method, "threadId", threadKind, "a thread id")
		return
	}
	if thread != nativeID {
		if approval {
			c.unlistedRequest(method+" request for another thread", envelope)
			return
		}
		c.mu.Unlock() // another thread's notification is not ours
		return
	}
	switch method {
	case "turn/started":
		id, kind := codexStr(params, "turn", "id")
		if kind != "string" {
			c.fieldFault(method, "turn.id", kind, "a turn id")
			return
		}
		if c.finished[id] {
			c.mu.Unlock()
			return
		}
		if !nativeKey(id) {
			c.keyFault("Codex turn/started", "turn id", id)
			return
		}
		c.activeTurn, c.state = id, "busy"
		c.mu.Unlock()
		c.emit(Event{Kind: "busy", NativeSessionID: thread, TurnID: id})
	case "turn/completed":
		id, kind := codexStr(params, "turn", "id")
		if kind != "string" {
			c.fieldFault(method, "turn.id", kind, "a turn id")
			return
		}
		// A replay of a finished turn never ends anything, whatever its
		// status: checked before the status.
		if c.finished[id] {
			c.mu.Unlock()
			return
		}
		if !nativeKey(id) {
			// Dropping it would leave the adapter busy for good with a turn
			// it cannot see finish (D-202); an empty id fails nativeKey too.
			c.keyFault("Codex turn/completed", "turn id", id)
			return
		}
		status, statusKind := codexStr(params, "turn", "status")
		if statusKind != "string" || status != "completed" && status != "interrupted" && status != "failed" {
			// Today's drop left the adapter busy for good (adapter-drops).
			// Idle is not inferred: an unknown status may be a turn that is
			// not over (inProgress), and idle would admit a turn/start into
			// it. A status is a token, so a string one is shown fenced
			// (D-198), anything else by kind.
			shown := "status " + codexKindPhrase(statusKind)
			if statusKind == "string" {
				shown = "status " + fence.Field(status, nativeTokenCap)
			}
			if c.activeTurn == "" || c.activeTurn == id {
				c.endLocked(func(active string) string {
					return codexEnded("Codex turn/completed reported turn "+id+" finished with an unrecognised "+shown+"; the adapter does not infer idle", active)
				})
				return
			}
			// Another turn's: it moves no state, so it never ends the
			// worker running the active one. Its listed approvals stay and
			// may be stale: a gap until the next idle.
			active := c.activeTurn
			c.mu.Unlock()
			c.emit(Event{Kind: "observation_gap", NativeSessionID: thread, TurnID: active,
				Err: "Codex turn/completed reported turn " + id + ", not the active turn, finished with an unrecognised " + shown + "; its listed approvals may be stale"})
			return
		}
		c.finished[id] = true
		delete(c.ownTurns, id)
		active := c.activeTurn == "" || c.activeTurn == id
		if active {
			c.activeTurn, c.state = "", "idle"
			c.refused = make(map[[32]byte]bool)
		}
		for key, a := range c.approvals {
			if a.TurnID == id {
				delete(c.approvals, key)
			}
		}
		c.mu.Unlock()
		c.emit(Event{Kind: "result", NativeSessionID: thread, TurnID: id, ResultRef: id, ResultStatus: status})
		if active {
			c.emit(Event{Kind: "idle", NativeSessionID: thread, TurnID: id})
		}
	case "thread/archived", "thread/deleted":
		c.state, c.activeTurn = "ended", ""
		c.approvals = make(map[string]Approval)
		c.mu.Unlock()
		c.emit(Event{Kind: "ended", NativeSessionID: thread})
	case "thread/closed":
		// Unloading a thread is not archiving or ending its logical session.
		c.state, c.activeTurn = "unknown", ""
		c.closed = true
		c.approvals = make(map[string]Approval)
		proc := c.process
		c.mu.Unlock()
		c.terminal(proc, Event{Kind: "disconnected", NativeSessionID: thread, Err: "Codex thread unloaded; logical end was not observed"})
	default: // the five approval requests
		turnID, turnKind := codexStr(params, "turnId")
		switch turnKind {
		case "string":
		case "absent", "null":
			turnID = ""
		default:
			c.fieldFault(method, "turnId", turnKind, "a turn id")
			return
		}
		finished := turnID != "" && c.finished[turnID]
		if turnID != "" && !finished && !nativeKey(turnID) {
			c.keyFault("Codex "+method, "turn id", turnID)
			return
		}
		// Display-only: a non-string is left out, the request kept.
		itemID, _ := codexStr(params, "itemId")
		approvalID, _ := codexStr(params, "approvalId")
		key, keyErr := codexRequestKey(envelope.ID)
		seen := codexSeen{kind: method, digest: sha256.Sum256(envelope.Params)}
		// The reuse rule runs before any refusal: a declined reuse wrote a
		// decline to the LISTED request's id, which stayed listed (the Fable
		// cross-check, adapter-drops).
		if previous, exists := c.seenApprovals[key]; keyErr == nil && exists {
			if previous == seen {
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
			c.terminal(proc, Event{Kind: "disconnected", NativeSessionID: thread, Err: "Codex reused an approval request ID for different work"})
			return
		}
		if keyErr != nil || turnID == "" || finished {
			// An empty turn id was dropped, the request natively pending and
			// unlisted (D-202's Open): an approval the operator answers
			// names its turn, so it is refused, never listed turn-less. A
			// FRESH request naming a finished turn was dropped the same way
			// (a replay of a seen one stopped above; the Codex code pass).
			c.refuseApproval(method, envelope, params, turnID, itemID, keyErr, finished)
			return
		}
		a := Approval{ID: codexCanonicalID(key), NativeSessionID: thread, TurnID: turnID, ItemID: displayNativeID(itemID), NativeApprovalID: displayNativeID(approvalID), Kind: method, Detail: append(json.RawMessage(nil), envelope.Params...)}
		if len(c.approvals) >= codexPendingCap {
			c.endLocked(func(active string) string {
				return codexEnded("Codex asked for more than "+strconv.Itoa(codexPendingCap)+" approvals at once", active)
			})
			return
		}
		c.approvals[key] = a
		c.seenApprovals[key] = seen
		c.mu.Unlock()
		c.emit(Event{Kind: "approval", NativeSessionID: thread, TurnID: turnID, Approval: &a})
	}
}

// observationEvent: method is one of nativeEvent's observation literals.
// Nothing here ends the transport.
func (c *codexBackend) observationEvent(method string, envelope codexEnvelope) {
	params, isObject := codexObject(envelope.Params)
	c.mu.Lock()
	if c.closed || c.nativeID == "" {
		c.mu.Unlock()
		return
	}
	ownActive := c.activeTurn != "" && c.ownTurns[c.activeTurn]
	renewals := c.renewals
	// lost drops the observation; an item/completed during an owned turn may
	// have been a completed write, so its renewal coverage is reported
	// unknown (D-078), never missed in silence.
	lost := func(why string) {
		c.mu.Unlock()
		if method == "item/completed" && ownActive {
			renewals.unknown("a Codex item/completed " + why + " during an owned turn")
		}
	}
	thread, threadKind := "", ""
	if isObject {
		thread, threadKind = codexStr(params, "threadId")
	}
	if !isObject || threadKind != "string" {
		lost("had no readable thread id")
		return
	}
	if thread != c.nativeID {
		c.mu.Unlock()
		return
	}
	switch method {
	case "item/started", "item/completed":
		itemType, itemKind := codexStr(params, "item", "type")
		if itemKind != "string" {
			lost("had no readable item type")
			return
		}
		turnID, turnKind := codexStr(params, "turnId")
		// History compaction neither ends a logical session nor completes its
		// assignments. Keep exact item/turn identity without changing control
		// state; a delayed observation cannot clear a newer approval or turn.
		if itemType == "contextCompaction" {
			itemID, _ := codexStr(params, "item", "id")
			if itemID == "" || !nativeKey(turnID) {
				// An observation only, keyed on nothing: dropped. The same
				// turn's turn/started or turn/completed ends the transport
				// over a bad turn id (D-202); ending it here would kill a
				// worker mid-compaction for an id the adapter never keys on.
				c.mu.Unlock()
				return
			}
			kind := "compaction_started"
			if method == "item/completed" {
				kind = "compaction_completed"
			}
			c.mu.Unlock()
			// The item id is displayed only; the turn id is a key (D-199).
			c.emit(Event{Kind: kind, NativeSessionID: thread, TurnID: turnID, ItemID: displayNativeID(itemID)})
			return
		}
		if method != "item/completed" || itemType != "fileChange" {
			c.mu.Unlock()
			return
		}
		// Only an observation for renewal (D-078): no control state
		// changes, and nothing is emitted on the event channel.
		if turnKind != "string" {
			lost("fileChange had no readable turn id")
			return
		}
		active := turnID != "" && turnID == c.activeTurn && c.ownTurns[turnID]
		generation := c.generation
		c.mu.Unlock()
		if !active {
			return
		}
		status, _ := codexStr(params, "item", "status")
		var changes json.RawMessage
		if item, ok := codexObject(params["item"]); ok {
			changes = item["changes"]
		}
		paths, renew, gone := codexFileChangePaths(status, changes)
		if renew {
			renewals.offer(generation, paths)
		}
		if gone != "" {
			renewals.unknown(gone)
		}
	case "serverRequest/resolved":
		raw := params["requestId"]
		key, err := codexRequestKey(raw)
		if err != nil {
			if len(c.approvals) == 0 {
				c.mu.Unlock()
				return
			}
			// The resolution of a listed request may be the one lost: the
			// list may be stale, a gap until the next idle.
			active := c.activeTurn
			c.mu.Unlock()
			c.emit(Event{Kind: "observation_gap", NativeSessionID: thread, TurnID: active,
				Err: "a Codex serverRequest/resolved named a request id the adapter cannot read (" + codexKindPhrase(jsonKind(raw)) + "); the listed approvals may be stale"})
			return
		}
		a, exists := c.approvals[key]
		delete(c.approvals, key)
		c.mu.Unlock()
		if exists {
			c.emit(Event{Kind: "approval_resolved", NativeSessionID: thread, TurnID: a.TurnID, Approval: &a})
		}
	case "thread/status/changed":
		// A status without turn identity cannot clear a newer active turn;
		// turn/completed carries the comparison needed for that mutation.
		// An idle status while the backend is busy with NO tracked turn is
		// the one it may repair (adapter-drops): a replayed `active` after
		// turn/completed left Send refusing every wake for good.
		switch status, _ := codexStr(params, "status", "type"); status {
		case "active":
			c.state = "busy"
		case "idle":
			if c.state == "busy" && c.activeTurn == "" {
				c.state = "idle"
			}
		}
		c.mu.Unlock()
	default:
		c.mu.Unlock()
	}
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
