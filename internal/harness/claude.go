package harness

// Claude's streaming CLI is the transport used by Agent SDK 0.3.202. This
// adapter deliberately implements only its small control/input subset, pinned
// to the native builds its committed admission records name (D-079). It neither imports an npm cache nor
// treats a successful pipe write as delivery. The replayed native user UUID is
// the acceptance evidence; a result UUID is a separate, later observation.
// Native 2.1.289 qualification also observed exact input UUID fields on results
// that the cached SDK declarations omit. It drops caller-supplied peer origin;
// only the textual peer framing is preserved, not authenticated peer provenance.
//
// Wire provenance: installed @anthropic-ai/claude-agent-sdk/sdk.d.ts,
// SDKControlRequest, SDKControlResponse, SDKUserMessage and SDKUserMessageReplay.
// Admitting a new build takes its qualification and a committed record, never
// a semver range; changing this wire subset bumps claudeAdapterContract.
import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Which native builds this adapter may launch, with which Mod profile and
// which capability tiers, is no longer a version table here: it is the
// committed admission records (admission.go, D-079). The compaction and
// resume checks below ask those records by the captured version.

type claudeProcess interface {
	Write(any) error
	WriteContext(context.Context, any) error
	Read() <-chan json.RawMessage
	Close() error
}

type claudeBackend struct {
	executable         string
	probe              func(context.Context, string) (string, error)
	mu                 sync.Mutex
	op                 chan struct{}
	proc               claudeProcess
	nativeID           string
	version            string
	compactBoundary    string
	compactInput       string
	seenBoundaries     map[string]bool
	state              string
	active             string
	activeAck          bool
	interruptUncertain bool
	closed             bool
	events             chan Event
	done               chan struct{}
	stop               chan struct{}
	stopOnce           sync.Once
	waiters            map[string]chan claudeReply
	approvals          map[string]claudePermission
	seenApprovals      map[string]string
	results            map[string]bool
	// Completed-write observation (claude_renewal.go, D-078): the mailbox,
	// this launch's generation, and the outstanding main-thread file-tool
	// uses awaiting their tool_result, bounded in count and bytes.
	renewals   *renewalMailbox
	generation string
	fileTools  map[string]claudeFileTool
	fileBytes  int
}

type claudeReply struct {
	err  error
	data json.RawMessage
}

type claudePermission struct {
	id                  json.RawMessage
	input               json.RawMessage
	toolID              string
	turnID              string
	requiresInteraction bool
}

// NewClaude controls only the process it launches. Existing interactive and
// background sessions are not a transport and are never attached by PID. The
// executable must be absolute; it is resolved once (executable.go, F-23).
func NewClaude(executable string) (Backend, error) {
	resolved, err := resolveExecutable(executable)
	if err != nil {
		return nil, err
	}
	return newClaude(resolved), nil
}

func newClaude(executable string) *claudeBackend {
	c := &claudeBackend{executable: executable, probe: probeNativeVersion, state: "unknown", events: make(chan Event, 64), op: make(chan struct{}, 1),
		done: make(chan struct{}), stop: make(chan struct{}), waiters: make(map[string]chan claudeReply), approvals: make(map[string]claudePermission), seenApprovals: make(map[string]string), results: make(map[string]bool)}
	c.op <- struct{}{}
	return c
}

// The native interrupt has no turn ID. Keep the next input outside the pipe
// until that request has been acknowledged; a mutex on state alone leaves a
// result(A), Send(B), late-interrupt(A) race. Waiting for this gate is cancellable.
func (c *claudeBackend) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return errors.New("Claude adapter closed")
	case <-c.op:
	}
	if err := ctx.Err(); err != nil {
		c.release()
		return err
	}
	return nil
}
func (c *claudeBackend) release() { c.op <- struct{}{} }

func (c *claudeBackend) Capabilities() Capabilities {
	c.mu.Lock()
	version := c.version
	c.mu.Unlock()
	compact := Capability{Support: "unsupported", Reason: "manual compaction observations need an admission record granting compaction_observation for the launched version"}
	if admissionVersionGrants("claude", version, TierCompaction) {
		compact = Capability{Support: "experimental", Reason: "manual compact_boundary UUID and correlated local-command result only; no compaction control or automatic threshold"}
	}
	// Before a launch the code's own support is reported; after one, a
	// record that does not grant resume withdraws it (the intersection).
	resume := Capability{Support: "experimental", Reason: "captured identity after journalled completed or aborted_streaming/aborted_tools results and idle; unfinished recovery unsupported"}
	if version == "" {
		resume = Capability{Support: "unknown", Reason: "decided by the admission record of the build a launch admits"}
	} else if !admissionVersionGrants("claude", version, TierResume) {
		resume = Capability{Support: "unsupported", Reason: "the launched version's admission record does not grant resume"}
	}
	r := "experimental native stream-json contract (" + claudeAdapterContract + ") for exactly the admitted artifacts / Agent SDK 0.3.202 wire subset; provider qualification pending"
	return Capabilities{
		"compaction_observation": compact,
		"start":                  {Support: "experimental", Reason: r}, "send": {Support: "experimental", Reason: r},
		"inspect":   {Support: "supported", Reason: "adapter's captured stream observations only"},
		"interrupt": {Support: "experimental", Reason: r}, "approval": {Support: "experimental", Reason: r},
		"reconcile":     {Support: "unsupported", Reason: "native turn history reconciliation is not implemented"},
		"resume":        resume,
		"attach":        {Support: "unsupported", Reason: "only adapter-launched workers are controlled"},
		"wake":          {Support: "experimental", Reason: "textually peer-framed input on the owned stream; native peer provenance is unsupported"},
		"telemetry":     {Support: "unknown", Reason: "no telemetry translation in this bounded adapter"},
		"claim_renewal": {Support: "experimental", Reason: "a matched main-thread Edit/Write/NotebookEdit tool_result with is_error:false, or on 2.1.291 a Write whose omitted is_error has the structured create/update witness; Bash and subagents renew nothing"},
	}
}

func (c *claudeBackend) Events() <-chan Event { return c.events }

func (c *claudeBackend) Start(ctx context.Context, launch Launch, auth *LaunchAuthorization) (string, error) {
	return c.launch(ctx, launch, "", auth)
}

// Resume selects exactly the saved conversation. --session-id would start a
// new conversation, and --continue would pick an unrelated latest session.
// Controller requires qualified terminal results and idle in the journal first.
func (c *claudeBackend) Resume(ctx context.Context, launch Launch, id string, auth *LaunchAuthorization) (string, error) {
	if !claudeValidUUID(id) || launch.NativeSessionID != "" && launch.NativeSessionID != id {
		return "", errors.New("Claude resume differs from captured native session")
	}
	return c.launch(ctx, launch, id, auth)
}

func (c *claudeBackend) launch(ctx context.Context, launch Launch, resumeID string, auth *LaunchAuthorization) (string, error) {
	if err := c.acquire(ctx); err != nil {
		return "", err
	}
	defer c.release()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.proc != nil || c.closed {
		return "", errors.New("Claude adapter already started or closed")
	}
	if launch.Hooks.NoMods != "" {
		return "", errors.New("unsupported Claude no_mods assertion: builtin Mods exist; use the qualified mods profile")
	}
	if launch.HookConfig == "" || !filepath.IsAbs(launch.HookConfig) {
		return "", errors.New("Claude launch requires an explicit absolute hook settings path")
	}
	if fi, err := os.Stat(launch.HookConfig); err != nil || !fi.Mode().IsRegular() {
		return "", errors.New("Claude hook settings must be a readable regular file")
	}
	if err := claudeHookCompatibility(launch.HookConfig, launch.GateCommand); err != nil {
		return "", err
	}
	for _, name := range []string{"CLAUDE_CODE_SAFE_MODE", "CLAUDE_CODE_SIMPLE"} {
		value := strings.ToLower(os.Getenv(name))
		if value != "" && value != "0" && value != "false" {
			return "", fmt.Errorf("Claude launch refuses inherited %s: Buddy hooks could be skipped", name)
		}
	}
	// Plan/manual are the only native launch modes here. No auto, acceptEdits, bypass,
	// permission-rule updates or caller-provided arbitrary CLI arguments.
	mode := ""
	switch launch.PermissionMode {
	case "read-only":
		mode = "plan"
	case "workspace-write":
		mode = "manual"
	default:
		return "", errors.New("unsupported Claude permission mode: require read-only or workspace-write")
	}
	if !filepath.IsAbs(launch.Worktree) {
		return "", errors.New("Claude worktree must be absolute")
	}
	checked, err := observeExecutable(c.executable)
	if err != nil {
		return "", err
	}
	// Admission before the file runs at all, even for --version (D-079): the
	// bytes must be a committed record's, granting this operation, under the
	// declared profile. Only then is the exact version text asked for.
	op := TierStart
	if resumeID != "" {
		op = TierResume
	}
	rec, err := admitExecutable("claude", c.executable, checked, launch, op)
	if err != nil {
		return "", err
	}
	version, err := c.probe(ctx, c.executable)
	if err != nil {
		return "", fmt.Errorf("observe Claude version: %w", err)
	}
	if version != rec.VersionOutput {
		return "", refuse("version_mismatch", "installed Claude reports %q; its admission record says %q", version, rec.VersionOutput)
	}
	id := launch.NativeSessionID
	if resumeID != "" {
		id = resumeID
	}
	if id == "" {
		id, err = claudeUUID()
		if err != nil {
			return "", err
		}
	}
	if !claudeValidUUID(id) {
		return "", errors.New("Claude native session id must be a UUID")
	}
	identityFlag := "--session-id"
	if resumeID != "" {
		identityFlag = "--resume"
	}
	args := []string{"--print", "--verbose", "--input-format", "stream-json", "--output-format", "stream-json",
		"--replay-user-messages", "--include-hook-events", "--permission-prompt-tool", "stdio",
		"--permission-mode", mode, identityFlag, id, "--setting-sources", "", "--settings", launch.HookConfig,
		"--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`}
	if err := sameExecutable(c.executable, checked); err != nil {
		return "", err
	}
	// A worker runs the pinned bytes it was admitted on. The interactive
	// harness updates freely; a worker must not update itself mid-run.
	proc, err := startClaudeProcess(ctx, auth, c.executable, args, launch.Worktree, []string{"DISABLE_AUTOUPDATER=1"})
	if err != nil {
		return "", err
	}
	c.proc, c.nativeID, c.state, c.version = proc, id, "unknown", launch.HarnessVersion
	c.generation = auth.generation()
	go c.read()
	// Initialization does not spend a model turn. Unlock while awaiting the
	// native response; the reader must be able to route it to this waiter.
	c.mu.Unlock()
	err = c.control(ctx, map[string]any{"subtype": "initialize"})
	if err == nil {
		err = c.effectiveSettings(ctx, launch.GateCommand)
	}
	if err == nil {
		// This native control reloads the launched child's plugins; it is not
		// a read-only diagnostic. Unlike a caller's no_mods assertion, its
		// response identifies the effective plugin set before a model input.
		// The pinned builtin profile permits AGENTS.md context support and
		// the authoring skill, not arbitrary permission-affecting Mods.
		var inventory json.RawMessage
		inventory, err = c.controlData(ctx, map[string]any{"subtype": "reload_plugins"})
		if err == nil {
			err = claudeModCompatibility(inventory, rec.Claude.Builtins)
		}
	}
	if err == nil {
		// Reload can change session components. Re-observe settings afterward
		// so an earlier valid snapshot cannot qualify a changed gate.
		err = c.effectiveSettings(ctx, launch.GateCommand)
	}
	c.mu.Lock()
	if err != nil {
		_ = proc.Close()
		return "", fmt.Errorf("initialize Claude streaming transport: %w", err)
	}
	return id, nil
}

func (c *claudeBackend) effectiveSettings(ctx context.Context, command string) error {
	// --setting-sources excludes user/project settings, not managed policy.
	data, err := c.controlData(ctx, map[string]any{"subtype": "get_settings"})
	if err != nil {
		return err
	}
	var observed struct {
		Effective json.RawMessage `json:"effective"`
		Errors    json.RawMessage `json:"errors"`
	}
	if json.Unmarshal(data, &observed) != nil || len(observed.Effective) == 0 {
		return errors.New("Claude effective settings response is missing or malformed")
	}
	if len(observed.Errors) != 0 && string(observed.Errors) != "[]" {
		return errors.New("Claude effective settings contain skipped or unknown configuration errors")
	}
	return claudeSettingsCompatibility(observed.Effective, command)
}

// This inventory is a startup compatibility snapshot, not ongoing enforcement
// or proof of no Mods. In the measured natives agents-md is a built-in Mod and
// plugin-authoring is a builtin skill without Mod code. The inventory must be
// EXACTLY the admission record's built-in list, each a name this code knows
// (claudeKnownBuiltins). Other builtins (including the managed-policy guard),
// installed Mods and any uncertain reload refuse.
func claudeModCompatibility(data []byte, builtins []string) error {
	var observed struct {
		Plugins    json.RawMessage `json:"plugins"`
		MCPServers json.RawMessage `json:"mcpServers"`
		ErrorCount *int            `json:"error_count"`
		Held       *bool           `json:"held"`
	}
	if len(data) > 4<<20 || json.Unmarshal(data, &observed) != nil || observed.ErrorCount == nil || *observed.ErrorCount != 0 || observed.Held != nil && *observed.Held {
		return errors.New("Claude Mod inventory reload is missing, malformed, held or has errors")
	}
	var servers []json.RawMessage
	if json.Unmarshal(observed.MCPServers, &servers) != nil || servers == nil || len(servers) != 0 {
		return errors.New("Claude Mod profile requires an observed empty native MCP server set")
	}
	var plugins []struct {
		Name   string `json:"name"`
		Path   string `json:"path"`
		Source string `json:"source"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(observed.Plugins)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&plugins) != nil || len(plugins) != len(builtins) {
		return errors.New("Claude Mod inventory differs from the qualified builtin profile")
	}
	seen := map[string]bool{}
	for _, plugin := range plugins {
		if plugin.Path != "builtin" || plugin.Source != plugin.Name+"@builtin" || seen[plugin.Name] ||
			!slices.Contains(builtins, plugin.Name) || !slices.Contains(claudeKnownBuiltins, plugin.Name) {
			return errors.New("Claude Mod inventory contains an unqualified plugin identity")
		}
		seen[plugin.Name] = true
	}
	return nil
}

func (c *claudeBackend) Inspect(_ context.Context, nativeID string) (Observation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.proc == nil || nativeID != c.nativeID {
		return Observation{State: "unknown"}, errors.New("Claude session is not owned by this adapter")
	}
	return Observation{State: c.state, Detail: "observed on this adapter's native stream; no ledger or process inference"}, nil
}

// Every refusal above the pipe write is ErrNotSubmitted (F-10): nothing reached
// native input, so the controller may record not_submitted and free the wake
// for a retry. Waking a busy worker is the ordinary case; journalling that
// refusal as an uncertain delivery burned the message and refused resume
// forever. Anything returned from WriteContext onward stays uncertain.
func (c *claudeBackend) Send(ctx context.Context, nativeID, text string) (Delivery, error) {
	refused := func(err error) (Delivery, error) {
		return Delivery{State: "unknown"}, fmt.Errorf("%w: %v", ErrNotSubmitted, err)
	}
	if err := c.acquire(ctx); err != nil {
		return refused(err)
	}
	defer c.release()
	uuid, err := claudeUUID()
	if err != nil {
		return refused(err)
	}
	c.mu.Lock()
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return refused(err)
	}
	if c.proc == nil || c.closed || nativeID != c.nativeID {
		c.mu.Unlock()
		return refused(errors.New("Claude session is not owned by this adapter"))
	}
	if c.active != "" {
		c.mu.Unlock()
		return refused(errors.New("Claude adapter permits only one outstanding turn"))
	}
	if c.interruptUncertain {
		c.mu.Unlock()
		return refused(errors.New("Claude interrupt outcome is uncertain; next-turn admission is refused"))
	}
	ch := make(chan claudeReply, 1)
	c.waiters[uuid], c.active = ch, uuid
	c.activeAck = false
	c.compactBoundary, c.compactInput = "", ""
	proc := c.proc
	c.mu.Unlock()
	// Keep the peer marker requested by the SDK wire contract, but the installed
	// 2.1.289 stream drops it. The controller's fixed textual provenance reminder
	// is required; this adapter cannot claim authenticated native peer input.
	err = proc.WriteContext(ctx, map[string]any{"type": "user", "uuid": uuid, "session_id": nativeID,
		"parent_tool_use_id": nil, "origin": map[string]string{"kind": "peer", "from": "buddy-adapter"},
		"message": map[string]string{"role": "user", "content": text}})
	if err == nil {
		err = c.wait(ctx, uuid, ch)
	} else {
		c.mu.Lock()
		delete(c.waiters, uuid)
		c.mu.Unlock()
	}
	if err != nil {
		// A timed-out write/replay is uncertain, not safe to retransmit. Keep
		// the active UUID until its result or disconnection reconciles it.
		return Delivery{State: "unknown", TurnID: uuid}, err
	}
	return Delivery{State: "delivered", TurnID: uuid}, nil
}

func (c *claudeBackend) Interrupt(ctx context.Context, nativeID, turnID string) error {
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()
	c.mu.Lock()
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return err
	}
	ok := c.proc != nil && !c.closed && nativeID == c.nativeID && c.active != "" && turnID == c.active && !c.interruptUncertain
	c.mu.Unlock()
	if !ok {
		return errors.New("Claude interrupt requires the active captured native input UUID")
	}
	// A control acknowledgment is not turn completion. The result remains in
	// the stream and must be observed before another Send can start a turn.
	err := c.control(ctx, map[string]any{"subtype": "interrupt"})
	if err != nil {
		c.mu.Lock()
		c.interruptUncertain = true
		c.state = "unknown"
		c.mu.Unlock()
	}
	return err
}

func (c *claudeBackend) Approve(ctx context.Context, decision ApprovalDecision) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrNotSubmitted, err)
	}
	key, err := claudeIDKey(decision.ID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotSubmitted, err)
	}
	c.mu.Lock()
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return fmt.Errorf("%w: %v", ErrNotSubmitted, err)
	}
	p, ok := c.approvals[key]
	if !ok || decision.NativeSessionID != c.nativeID || decision.TurnID != p.turnID || c.closed {
		c.mu.Unlock()
		return fmt.Errorf("%w: Claude approval does not match a pending captured request", ErrNotSubmitted)
	}
	if decision.Decision != "allow" && decision.Decision != "deny" {
		c.mu.Unlock()
		return fmt.Errorf("%w: Claude approval must be allow or deny", ErrNotSubmitted)
	}
	if p.requiresInteraction && decision.Decision == "allow" {
		c.mu.Unlock()
		return fmt.Errorf("%w: unsupported: native approval requires its interactive user surface", ErrNotSubmitted)
	}
	response := map[string]any{"behavior": decision.Decision, "toolUseID": p.toolID}
	if decision.Decision == "allow" {
		response["updatedInput"] = p.input
	} else {
		response["message"] = "Denied by the operator control interface"
	}
	// Reserve one-shot delivery before touching the pipe, retaining the
	// lifetime fingerprint. A partial/cancelled write is uncertain and must
	// never turn this decision back into an eligible approval.
	delete(c.approvals, key)
	if len(c.approvals) == 0 {
		c.state = "busy"
	}
	proc := c.proc
	c.mu.Unlock()
	return proc.WriteContext(ctx, map[string]any{"type": "control_response", "response": map[string]any{
		"subtype": "success", "request_id": p.id, "response": response}})
}

func (c *claudeBackend) Close() error {
	// Explicit shutdown can release a reader publishing a terminal event to
	// a full consumer queue. The separate done signal already closed control
	// admission; closing it must not silently discard the disconnection event.
	c.stopOnce.Do(func() { close(c.stop) })
	return c.finish()
}

func (c *claudeBackend) finish() error {
	c.mu.Lock()
	proc := c.finishLocked()
	c.mu.Unlock()
	if proc != nil {
		return proc.Close()
	}
	return nil
}

// Must run under mu, before any terminal notification can block. A lost
// native event stream invalidates approval eligibility, even while the child
// still accepts stdin. It proves neither logical SessionEnd nor work completion.
func (c *claudeBackend) finishLocked() claudeProcess {
	if !c.closed {
		c.closed = true
		close(c.done)
	}
	c.state, c.active = "unknown", ""
	c.approvals = make(map[string]claudePermission)
	c.fileTools, c.fileBytes = nil, 0
	for _, ch := range c.waiters {
		select {
		case ch <- claudeReply{err: errors.New("Claude transport disconnected")}:
		default:
		}
	}
	return c.proc
}

func (c *claudeBackend) control(ctx context.Context, request any) error {
	_, err := c.controlData(ctx, request)
	return err
}

func (c *claudeBackend) controlData(ctx context.Context, request any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id, err := claudeUUID()
	if err != nil {
		return nil, err
	}
	ch := make(chan claudeReply, 1)
	c.mu.Lock()
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	if c.proc == nil || c.closed {
		c.mu.Unlock()
		return nil, errors.New("Claude transport is not running")
	}
	c.waiters[id] = ch
	proc := c.proc
	c.mu.Unlock()
	if err := proc.WriteContext(ctx, map[string]any{"type": "control_request", "request_id": id, "request": request}); err != nil {
		c.mu.Lock()
		delete(c.waiters, id)
		c.mu.Unlock()
		return nil, err
	}
	return c.waitData(ctx, id, ch)
}

func (c *claudeBackend) wait(ctx context.Context, id string, ch <-chan claudeReply) error {
	_, err := c.waitData(ctx, id, ch)
	return err
}

func (c *claudeBackend) waitData(ctx context.Context, id string, ch <-chan claudeReply) (json.RawMessage, error) {
	defer func() { c.mu.Lock(); delete(c.waiters, id); c.mu.Unlock() }()
	select {
	case reply := <-ch:
		return reply.data, reply.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, errors.New("Claude transport closed before native acknowledgment")
	}
}

func (c *claudeBackend) emit(event Event) {
	select {
	case c.events <- event:
	case <-c.stop:
	}
}

func (c *claudeBackend) read() {
	defer close(c.events)
	for raw := range c.proc.Read() {
		if err := c.frame(raw); err != nil {
			_ = c.finish()
			c.emit(Event{Kind: "disconnected", NativeSessionID: c.nativeID, Err: err.Error()})
			return
		}
	}
	_ = c.finish()
	// EOF, even exit 0, does not prove SessionEnd or logical completion. Carry
	// the transport's own reason: an oversized frame (F-08) is a frame we
	// never read, which must read as unknown, not as a quiet end of stream.
	detail := "native stream closed"
	if p, ok := c.proc.(interface{ Err() error }); ok && p.Err() != nil {
		detail = p.Err().Error()
	}
	c.emit(Event{Kind: "disconnected", NativeSessionID: c.nativeID, Err: detail})
}

func (c *claudeBackend) frame(raw json.RawMessage) error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return errors.New("Claude transport admission is closed")
	}
	var f struct {
		Type            string `json:"type"`
		Subtype         string `json:"subtype"`
		SessionID       string `json:"session_id"`
		UUID            string `json:"uuid"`
		IsReplay        bool   `json:"isReplay"`
		IsError         *bool  `json:"is_error"`
		LocalCommand    string `json:"local_command"`
		NumTurns        *int   `json:"num_turns"`
		CompactMetadata struct {
			Trigger string `json:"trigger"`
		} `json:"compact_metadata"`
		TerminalReason   string          `json:"terminal_reason"`
		UserMessageUUID  string          `json:"user_message_uuid"`
		UserMessageUUIDs []string        `json:"user_message_uuids"`
		QueuedTurnCount  *int            `json:"queued_turn_count"`
		RequestID        json.RawMessage `json:"request_id"`
		Request          json.RawMessage `json:"request"`
		Response         struct {
			Subtype   string          `json:"subtype"`
			RequestID string          `json:"request_id"`
			Error     string          `json:"error"`
			Data      json.RawMessage `json:"response"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return errors.New("malformed Claude stream frame")
	}
	if f.SessionID != "" && f.SessionID != c.nativeID {
		return errors.New("Claude native session changed unexpectedly")
	}
	switch f.Type {
	case "system":
		// The boundary has its own UUID, but NO input UUID. Preserve that
		// distinction on the wire. Only a later exact local-command result
		// can bind the observed boundary to our single outstanding input.
		c.mu.Lock()
		qualified := admissionVersionGrants("claude", c.version, TierCompaction) && f.Subtype == "compact_boundary" &&
			f.SessionID == c.nativeID && claudeValidUUID(f.UUID) && f.CompactMetadata.Trigger == "manual" && !c.seenBoundaries[f.UUID]
		if qualified {
			// A delayed duplicate keeps its original meaning. It cannot lend
			// the previous input's boundary to a newly active local command.
			if c.seenBoundaries == nil {
				c.seenBoundaries = make(map[string]bool)
			}
			c.seenBoundaries[f.UUID] = true
			c.compactBoundary, c.compactInput = f.UUID, c.active
		}
		c.mu.Unlock()
		if qualified {
			c.emit(Event{Kind: "compaction_boundary", NativeSessionID: c.nativeID, ItemID: f.UUID})
		}
	case "control_response":
		c.mu.Lock()
		ch := c.waiters[f.Response.RequestID]
		c.mu.Unlock()
		if ch != nil {
			var err error
			if f.Response.Subtype != "success" {
				err = errors.New("Claude refused control request: " + f.Response.Error)
			}
			select {
			case ch <- claudeReply{err: err, data: f.Response.Data}:
			default:
			}
		}
	case "control_request":
		return c.permission(f.RequestID, f.Request)
	case "control_cancel_request":
		key, err := claudeIDKey(f.RequestID)
		if err != nil {
			return err
		}
		c.mu.Lock()
		approval, existed := c.approvals[key]
		delete(c.approvals, key)
		if _, seen := c.seenApprovals[key]; !seen {
			c.seenApprovals[key] = "cancelled-before-request"
		}
		if len(c.approvals) == 0 && c.active != "" {
			c.state = "busy"
		}
		c.mu.Unlock()
		if existed {
			c.emit(Event{Kind: "approval_resolved", NativeSessionID: c.nativeID, TurnID: approval.turnID, ResultStatus: "cancelled", Approval: &Approval{ID: approval.id, NativeSessionID: c.nativeID, TurnID: approval.turnID, ItemID: approval.toolID, NativeApprovalID: key, Kind: "can_use_tool"}})
		}
	case "assistant":
		if !f.IsReplay && f.SessionID == c.nativeID {
			c.assistantToolUses(raw)
		}
	case "user":
		if !f.IsReplay {
			// A tool's result is a non-replay user frame. Classifying it
			// changes no control state; it can only offer a renewal.
			c.toolResults(raw)
			return nil
		}
		if f.SessionID != c.nativeID {
			return nil
		}
		c.mu.Lock()
		ch := c.waiters[f.UUID]
		active := c.active == f.UUID && !c.activeAck
		if active {
			c.activeAck = true
			if len(c.approvals) == 0 {
				c.state = "busy"
			}
		}
		c.mu.Unlock()
		if active {
			if ch != nil {
				select {
				case ch <- claudeReply{}:
				default:
				}
			}
			c.emit(Event{Kind: "acknowledged", NativeSessionID: c.nativeID, TurnID: f.UUID})
			c.emit(Event{Kind: "busy", NativeSessionID: c.nativeID, TurnID: f.UUID})
		}
	case "result":
		if f.SessionID != c.nativeID || f.UUID == "" {
			return errors.New("Claude result lacks native identity/reference")
		}
		c.mu.Lock()
		if c.results[f.UUID] {
			c.mu.Unlock()
			return nil
		}
		c.results[f.UUID] = true
		turn := ""
		boundary := ""
		// Native results also arrive for background notifications. The result
		// UUID and optional origin cannot identify our input. Native 2.1.289
		// emits both exact input UUID fields and an explicit queued-turn count;
		// require the observed single-input shape and an empty queue. Missing,
		// coalesced or contradictory evidence must not admit the next wake.
		if !c.closed && c.active != "" && f.UserMessageUUID == c.active &&
			len(f.UserMessageUUIDs) == 1 && f.UserMessageUUIDs[0] == c.active &&
			f.QueuedTurnCount != nil && *f.QueuedTurnCount == 0 {
			turn = c.active
			// Measured 2.1.290 and 2.1.291 /compact omit terminal_reason. A generic
			// success (or boundary alone) must not repair unknown terminal
			// evidence. Require its zero-turn local result, exact acceptance
			// and a manual boundary observed during THIS input, with no
			// unresolved approval. Keep a distinct status and boundary ref.
			// Interrupting the held summary response also emits this native
			// success shape, but without a boundary (both qualified versions).
			// It must stay unknown, just like a lost boundary notification.
			if admissionVersionGrants("claude", c.version, TierCompaction) && c.activeAck && len(c.approvals) == 0 &&
				c.compactBoundary != "" && c.compactInput == turn && f.LocalCommand == "compact" &&
				f.TerminalReason == "" && f.Subtype == "success" && f.IsError != nil && !*f.IsError &&
				f.NumTurns != nil && *f.NumTurns == 0 {
				boundary = c.compactBoundary
			}
			c.compactBoundary, c.compactInput = "", ""
			c.active, c.state = "", "idle"
			c.approvals = make(map[string]claudePermission)
			// A use with no result by the turn's end never completed here.
			c.fileTools, c.fileBytes = nil, 0
		}
		c.mu.Unlock()
		errText := ""
		if f.IsError != nil && *f.IsError || f.Subtype != "success" || (f.TerminalReason != "" && f.TerminalReason != "completed") {
			errText = "native result: " + f.Subtype + "/" + f.TerminalReason
		}
		status := f.TerminalReason
		if status == "" {
			status = "unknown"
		}
		if boundary != "" {
			status = "local_compact_completed"
		}
		c.emit(Event{Kind: "result", NativeSessionID: c.nativeID, TurnID: turn, ItemID: boundary, ResultRef: f.UUID, ResultStatus: status, Err: errText})
		if turn != "" {
			c.emit(Event{Kind: "idle", NativeSessionID: c.nativeID, TurnID: turn})
		}
	}
	return nil
}

func (c *claudeBackend) permission(id, raw json.RawMessage) error {
	key, err := claudeIDKey(id)
	if err != nil {
		return err
	}
	var req struct {
		Subtype             string          `json:"subtype"`
		ToolName            string          `json:"tool_name"`
		Input               json.RawMessage `json:"input"`
		ToolUseID           string          `json:"tool_use_id"`
		AgentID             string          `json:"agent_id"`
		RequiresInteraction bool            `json:"requires_user_interaction"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return errors.New("malformed Claude control request")
	}
	if req.Subtype != "can_use_tool" {
		// Unknown controls cannot become approvals or implicit permissions.
		return c.proc.Write(map[string]any{"type": "control_response", "response": map[string]any{
			"subtype": "error", "request_id": id, "error": "unsupported Buddy adapter control request"}})
	}
	var input map[string]json.RawMessage
	if req.ToolName == "" || req.ToolUseID == "" || json.Unmarshal(req.Input, &input) != nil || input == nil {
		// Malformed: no correct native answer can be formed, so close
		// admission rather than guess one.
		return errors.New("unsupported or malformed Claude approval identity")
	}
	// Fingerprint the complete native request, including tool name,
	// interaction-only requirements and future permission-relevant fields.
	// Canonical JSON tolerates object ordering without rounding numeric input.
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return errors.New("malformed Claude approval fingerprint")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(canonical)
	fingerprint := hex.EncodeToString(digest[:])
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("Claude approval admission is closed")
	}
	if old, exists := c.seenApprovals[key]; exists {
		if old != fingerprint {
			// Invalidate the old pending decision atomically with detecting the
			// conflict. Merely returning an error left an allow eligible while
			// the reader waited to publish its terminal notification.
			proc := c.finishLocked()
			c.mu.Unlock()
			if proc != nil {
				_ = proc.Close()
			}
			return errors.New("Claude reused approval id for different request")
		}
		c.mu.Unlock()
		return nil
	}
	c.seenApprovals[key] = fingerprint
	turn := c.active
	if turn == "" || req.AgentID != "" {
		// Well-formed but unsupported (F-07): a subagent's request, or one
		// with no captured active input, cannot become an operator approval
		// for this worker. It used to tear the whole transport down, and
		// every teardown ended the worker. The protocol has a native deny
		// (the same control_response Approve sends for "deny"), so answer
		// with that and keep the worker. The id stays tombstoned above: a
		// replay is ignored and a reused id for different work still
		// closes admission. The operator is not asked and nothing is
		// published; the model sees the denial message.
		proc := c.proc
		c.mu.Unlock()
		return proc.Write(map[string]any{"type": "control_response", "response": map[string]any{
			"subtype": "success", "request_id": json.RawMessage(append([]byte(nil), id...)), "response": map[string]any{
				"behavior": "deny", "toolUseID": req.ToolUseID,
				"message": "Denied by the Buddy adapter: only the captured worker's active input can request operator approval"}}})
	}
	c.approvals[key] = claudePermission{id: append(json.RawMessage(nil), id...), input: append(json.RawMessage(nil), req.Input...), toolID: req.ToolUseID, turnID: turn, requiresInteraction: req.RequiresInteraction}
	c.state = "waiting_approval"
	lost := c.recordFileToolLocked(req.ToolUseID, req.ToolName, req.Input)
	renewals := c.renewals
	c.mu.Unlock()
	if lost != "" {
		renewals.unknown(lost)
	}
	c.emit(Event{Kind: "approval", NativeSessionID: c.nativeID, TurnID: turn, Approval: &Approval{
		ID: append(json.RawMessage(nil), id...), NativeSessionID: c.nativeID, TurnID: turn, ItemID: req.ToolUseID,
		NativeApprovalID: key, Kind: "can_use_tool", Detail: append(json.RawMessage(nil), raw...)}})
	return nil
}

// This is a check of the caller-supplied, bounded launch configuration, not
// proof that native managed policy ran the gate. Start applies the same check
// to the native get_settings response before and after the plugin reload. The
// exact native builtin profile is checked separately; a permissive installed
// Mod can override a user's PreToolUse refusal, and bare/safe modes skip it.
func claudeHookCompatibility(path, expectedCommand string) error {
	if expectedCommand == "" {
		return errors.New("Claude launch requires the exact captured gate command")
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read Claude hook settings: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (128<<10)+1))
	if err != nil {
		return fmt.Errorf("read Claude hook settings: %w", err)
	}
	return claudeSettingsCompatibility(data, expectedCommand)
}

// claudeGatedTools are the native write tools the gate must cover. Bash is
// pause-only in the gate, so a matcher may add it but never replace these.
var claudeGatedTools = []string{"Edit", "Write", "NotebookEdit"}

// Native Claude cancels a command hook at its configured timeout, and a
// cancelled hook is neither a deny decision nor exit 2, so the tool call goes
// ahead. The wrapper must therefore always answer before that timeout: its own
// worst case is HookGateBudget (identity check + gate) plus HookEndpointBudget
// plus two process starts, about 3.5 s. Ten seconds is the floor, and is what
// every native qualification fixture configures. The ceiling keeps the value
// far below Node's setTimeout limit of 2^31-1 ms, past which a delay collapses
// to 1 ms, and below a wait that would wedge a turn. An absent timeout keeps
// the native default, which is above the floor.
const (
	claudeHookTimeoutMinSeconds = 10
	claudeHookTimeoutMaxSeconds = 600
)

func claudeSettingsCompatibility(data []byte, expectedCommand string) error {
	if len(data) > 128<<10 {
		return errors.New("Claude hook settings exceed the bounded adapter contract")
	}
	// F-21: Go's struct decoding folds key case and keeps the LAST of two
	// duplicate keys, while native JSON.parse reads the exact key. So
	// {"command":"true","Command":"<gate>"} was admitted and natively ran
	// true. Every level is now an exact, case-sensitive allowlist decoded by a
	// token walk that refuses a duplicate key instead of choosing one.
	settings, err := strictJSONObject(data)
	if err != nil {
		return fmt.Errorf("Claude hook settings must be one JSON object: %w", err)
	}
	for name, raw := range settings {
		switch name {
		case "hooks":
		case "disableAllHooks", "allowManagedHooksOnly":
			if value, ok := jsonBool(raw); !ok || value {
				return fmt.Errorf("Claude hook compatibility refuses %s", name)
			}
		case "enabledPlugins":
			plugins, err := strictJSONObject(raw)
			if err != nil {
				return errors.New("Claude enabledPlugins must be an object of explicit booleans")
			}
			for _, enabled := range plugins {
				value, ok := jsonBool(enabled)
				if !ok {
					return errors.New("Claude enabledPlugins must be explicit booleans")
				}
				if value {
					return errors.New("Claude hook compatibility refuses enabled plugins or mods")
				}
			}
		default:
			return fmt.Errorf("unsupported Claude adapter settings key %q; effective hook compatibility is unknown", name)
		}
	}
	raw, ok := settings["hooks"]
	if !ok {
		return errors.New("Claude launch requires configured PreToolUse gate hooks")
	}
	events, err := strictJSONObject(raw)
	if err != nil {
		return fmt.Errorf("Claude launch requires configured PreToolUse gate hooks: %w", err)
	}
	covered := map[string]bool{}
	for event, rawGroups := range events {
		if event != "PreToolUse" {
			return fmt.Errorf("unsupported Claude hook %s: controller owns lifecycle translation", event)
		}
		groups, ok := jsonArray(rawGroups)
		if !ok {
			return errors.New("Claude PreToolUse hooks must be an array of matcher groups")
		}
		for _, rawGroup := range groups {
			group, err := strictJSONObject(rawGroup)
			if err != nil {
				return fmt.Errorf("Claude PreToolUse matcher group: %w", err)
			}
			matcher := ""
			var hooks []json.RawMessage
			hooksSeen := false
			for key, value := range group {
				switch key {
				case "matcher":
					if matcher, ok = jsonString(value); !ok {
						return errors.New("Claude PreToolUse matcher must be a string")
					}
				case "hooks":
					if hooks, ok = jsonArray(value); !ok {
						return errors.New("Claude PreToolUse group hooks must be an array")
					}
					hooksSeen = true
				default:
					return fmt.Errorf("unsupported Claude PreToolUse group key %q", key)
				}
			}
			if !hooksSeen {
				return errors.New("Claude PreToolUse group has no hooks")
			}
			if err := claudeMatcherCoversGate(matcher); err != nil {
				return err
			}
			for _, rawHook := range hooks {
				if err := claudeGateHook(rawHook, expectedCommand); err != nil {
					return err
				}
				for _, tool := range claudeGatedTools {
					covered[tool] = true
				}
			}
		}
	}
	for _, tool := range claudeGatedTools {
		if !covered[tool] {
			return fmt.Errorf("Claude PreToolUse gate does not cover %s", tool)
		}
	}
	return nil
}

func claudeGateHook(raw json.RawMessage, expectedCommand string) error {
	hook, err := strictJSONObject(raw)
	if err != nil {
		return fmt.Errorf("Claude gate hook: %w", err)
	}
	command, typed := "", false
	for key, value := range hook {
		switch key {
		case "type":
			kind, ok := jsonString(value)
			if !ok || kind != "command" {
				return errors.New("Claude gate requires a synchronous command hook")
			}
			typed = true
		case "command":
			var ok bool
			if command, ok = jsonString(value); !ok {
				return errors.New("Claude gate command must be a string")
			}
		case "async":
			// Absent or literally false. null, 0 or "false" are not a
			// qualified spelling of synchronous.
			if async, ok := jsonBool(value); !ok || async {
				return errors.New("Claude gate requires a synchronous command hook")
			}
		case "timeout":
			seconds, ok := jsonNumber(value)
			if !ok || seconds < claudeHookTimeoutMinSeconds || seconds > claudeHookTimeoutMaxSeconds {
				return fmt.Errorf("Claude gate timeout must be a number of seconds from %d to %d: a native timeout cancels the hook, and a cancelled hook does not deny", claudeHookTimeoutMinSeconds, claudeHookTimeoutMaxSeconds)
			}
		default:
			return fmt.Errorf("unsupported Claude gate hook key %q", key)
		}
	}
	if !typed {
		return errors.New("Claude gate requires a synchronous command hook")
	}
	if command != expectedCommand {
		return errors.New("Claude PreToolUse command differs from the captured gate command")
	}
	words, err := claudeHookWords(command)
	if err != nil || len(words) != 6 || !filepath.IsAbs(words[0]) || filepath.Base(words[0]) != "buddy-adapter" || words[1] != "hook" || words[2] != "--receipt" || !filepath.IsAbs(words[3]) || words[4] != "--buddy" || !filepath.IsAbs(words[5]) {
		return errors.New("Claude gate must be the exact absolute buddy-adapter hook --receipt <path> --buddy <path> command")
	}
	return nil
}

// claudeMatcherCoversGate admits only matchers whose meaning is the same to Go
// and to native Claude, and which select all three gated tools (F-09).
// Native 2.1.291 compares a matcher of plain name characters as an EXACT list
// split on '|' and ','; anything else is a JavaScript RegExp. Reading every
// matcher as an unanchored Go regexp admitted "Edit|Write" (Go finds "Edit"
// inside "NotebookEdit"; natively NotebookEdit is ungated) and "dit|rite"
// (natively matches nothing). The admitted forms:
//   - "", "*" or ".*": every tool;
//   - an exact list of names joined by '|' or ',' (no spaces);
//   - "^(A|B|...)$": an anchored alternation of plain names, which is an
//     exact list under both regex engines. This is the form every native
//     qualification fixture configured.
//
// Each list must name Edit, Write and NotebookEdit exactly (case-sensitive);
// it may name more. Every other pattern is refused, however plausible.
func claudeMatcherCoversGate(matcher string) error {
	switch matcher {
	case "", "*", ".*":
		return nil
	}
	list, separators := matcher, "|,"
	if strings.HasPrefix(matcher, "^(") && strings.HasSuffix(matcher, ")$") {
		list, separators = matcher[2:len(matcher)-2], "|"
	}
	isSeparator := func(r rune) bool { return strings.ContainsRune(separators, r) }
	names := map[string]bool{}
	for _, name := range strings.FieldsFunc(list, isSeparator) {
		names[name] = true
	}
	plain := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || isSeparator(r)
	}
	// FieldsFunc drops empty names; refuse them instead of reading
	// "Edit||Write" or a leading separator as something narrower or wider.
	empty := list == "" || isSeparator(rune(list[0])) || isSeparator(rune(list[len(list)-1]))
	for i := 1; !empty && i < len(list); i++ {
		empty = isSeparator(rune(list[i-1])) && isSeparator(rune(list[i]))
	}
	if empty || strings.IndexFunc(list, func(r rune) bool { return !plain(r) }) >= 0 {
		return fmt.Errorf("unsupported Claude PreToolUse matcher %q: use \"\", \"*\", \".*\", an exact Edit|Write|NotebookEdit list, or ^(Edit|Write|NotebookEdit)$", matcher)
	}
	for _, tool := range claudeGatedTools {
		if !names[tool] {
			return fmt.Errorf("Claude PreToolUse matcher %q does not name %s exactly; NotebookEdit is not covered by Edit", matcher, tool)
		}
	}
	return nil
}

// strictJSONObject decodes exactly one JSON object into its members, refusing
// a duplicate key rather than keeping the last one. Keys are compared after
// JSON unescaping, as JSON.parse does, so "command" duplicates "command".
func strictJSONObject(data []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, errors.New("not valid JSON")
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, errors.New("not a JSON object")
	}
	out := map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, errors.New("not valid JSON")
		}
		key, ok := tok.(string)
		if !ok {
			return nil, errors.New("not valid JSON")
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("duplicate key %q", key)
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, errors.New("not valid JSON")
		}
		out[key] = value
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, errors.New("not valid JSON")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data after the JSON object")
	}
	return out, nil
}

// The typed readers below accept only the named JSON type. json.Unmarshal
// would turn null into a zero value, and a null matcher must not read as "".
func jsonString(raw json.RawMessage) (string, bool) {
	var s string
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' || json.Unmarshal(trimmed, &s) != nil {
		return "", false
	}
	return s, true
}

func jsonBool(raw json.RawMessage) (bool, bool) {
	switch string(bytes.TrimSpace(raw)) {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

func jsonNumber(raw json.RawMessage) (float64, bool) {
	var n float64
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || !(trimmed[0] == '-' || trimmed[0] >= '0' && trimmed[0] <= '9') || json.Unmarshal(trimmed, &n) != nil {
		return 0, false
	}
	return n, true
}

func jsonArray(raw json.RawMessage) ([]json.RawMessage, bool) {
	var items []json.RawMessage
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' || json.Unmarshal(trimmed, &items) != nil {
		return nil, false
	}
	return items, true
}

// Only literal tokens and POSIX single quotes are accepted. Parsing arbitrary
// shell code to guess whether it eventually invokes the gate would accept an
// echo, a conditional skip, or an asynchronous command as enforcement.
func claudeHookWords(command string) ([]string, error) {
	var words []string
	var word strings.Builder
	quoted, started := false, false
	for _, r := range command {
		if r == '\'' {
			quoted = !quoted
			started = true
			continue
		}
		if quoted {
			word.WriteRune(r)
			continue
		}
		if r == ' ' || r == '\t' {
			if started {
				words = append(words, word.String())
				word.Reset()
				started = false
			}
			continue
		}
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-:", r) {
			return nil, errors.New("nonliteral shell hook")
		}
		word.WriteRune(r)
		started = true
	}
	if quoted {
		return nil, errors.New("unclosed hook quote")
	}
	if started {
		words = append(words, word.String())
	}
	return words, nil
}

func claudeIDKey(id json.RawMessage) (string, error) {
	var s string
	if json.Unmarshal(id, &s) != nil || s == "" {
		return "", errors.New("Claude control request id must be a nonempty native string")
	}
	return s, nil
}

func claudeUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}

func claudeValidUUID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	return err == nil
}
