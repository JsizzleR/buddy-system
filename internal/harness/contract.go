// Package harness translates native worker controls. It owns no claims or task
// database: the caller supplies an immutable Buddy lifecycle receipt, and a
// native turn result never means review, validation, or landing succeeded.
package harness

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// ErrNotSubmitted denotes a definite refusal before any native side effect.
// It must never wrap a partial write, timeout after sending, or lost response.
// A backend returns it only for a refusal made before its request reached
// WriteContext; anything that returned from WriteContext stays uncertain.
var ErrNotSubmitted = errors.New("native operation not submitted")

const Schema = "buddy.control.v1"

// Budgets for the adapter's PreToolUse wrapper (buddy-adapter hook), F-29.
// The identity check and the gate are the safety steps: they share
// HookGateBudget and fail closed when it runs out. The Claude endpoint
// observation is advisory and used to share that one deadline, so a slow
// lock-holder on its write transaction (busy_timeout 5 s) could spend the
// gate's time and turn an advisory step into a denied tool call. It now has
// its own budget, which does not count against the gate's. The hook latency
// target is 100 ms and a gate measured 20 ms; the endpoint step is the same
// shape (one buddy process, one write), so 250 ms is about twelve times its
// expected cost: enough that ordinary scheduling does not drop the
// observation, while contention costs at most a quarter second of latency and
// an "unknown" endpoint, never the tool call. Not yet measured under load.
const (
	HookGateBudget     = 3 * time.Second
	HookEndpointBudget = 250 * time.Millisecond
)

type Capability struct {
	Support string `json:"support"`
	Reason  string `json:"reason,omitempty"`
}
type Capabilities map[string]Capability

// Compatibility is evidence supplied for this exact launch configuration.
// Configured/trusted are not observed enforcement. Unknown is never absence.
type Compatibility struct {
	Configured string `json:"configured"`
	Trusted    string `json:"trusted"`
	Observed   string `json:"observed"`
	// Mods names the pinned native profile to observe, never an absence claim.
	Mods string `json:"mods,omitempty"`
	// NoMods is retained only to reject legacy Claude absence assertions loudly.
	NoMods string `json:"no_mods,omitempty"`
}
type Launch struct {
	GateCommand     string        `json:"gate_command"`
	GateHash        string        `json:"gate_hash,omitempty"`
	Worktree        string        `json:"worktree"`
	NativeSessionID string        `json:"native_session_id,omitempty"`
	PermissionMode  string        `json:"permission_mode"`
	HookConfig      string        `json:"hook_config"`
	HarnessVersion  string        `json:"harness_version"`
	Hooks           Compatibility `json:"hooks"`
}
type Observation struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}
type Delivery struct {
	State     string `json:"state"`
	TurnID    string `json:"turn_id,omitempty"`
	ResultRef string `json:"result_ref,omitempty"`
}

// TurnObservation is a read of one recorded native turn, never a receipt for
// tool effects, message consumption, approval resolution or completed work.
// Unknown includes missing history: absence cannot authorize replay.
type TurnObservation struct {
	NativeSessionID string      `json:"native_session_id"`
	TurnID          string      `json:"turn_id,omitempty"`
	Status          Observation `json:"status"`
	Source          string      `json:"source"`
}

// TurnReader is optional; harnesses without qualified native history retain
// unsupported reconciliation rather than guessing from their current idle state.
type TurnReader interface {
	ReadTurn(context.Context, string, string) (TurnObservation, error)
}
type Approval struct {
	ID               json.RawMessage `json:"id"`
	NativeSessionID  string          `json:"native_session_id"`
	TurnID           string          `json:"turn_id,omitempty"`
	ItemID           string          `json:"item_id,omitempty"`
	NativeApprovalID string          `json:"native_approval_id,omitempty"`
	Kind             string          `json:"kind"`
	Detail           json.RawMessage `json:"detail,omitempty"`
	// Receipts carry the exact Detail only on the one-shot approval event
	// and only up to a bound (F-22). The pending list repeated in every
	// receipt carries this digest of the exact bytes and a fenced, capped
	// summary instead. The backend keeps the exact request for forwarding.
	DetailSHA256  string `json:"detail_sha256,omitempty"`
	Summary       string `json:"summary,omitempty"`
	DetailOmitted string `json:"detail_omitted,omitempty"`
}
type ApprovalDecision struct {
	ID              json.RawMessage `json:"id"`
	NativeSessionID string          `json:"native_session_id"`
	TurnID          string          `json:"turn_id,omitempty"`
	Decision        string          `json:"decision"`
}
type Event struct {
	Kind            string    `json:"kind"`
	ResultStatus    string    `json:"result_status,omitempty"`
	NativeSessionID string    `json:"native_session_id"`
	TurnID          string    `json:"turn_id,omitempty"`
	ItemID          string    `json:"item_id,omitempty"`
	ResultRef       string    `json:"result_ref,omitempty"`
	Approval        *Approval `json:"approval,omitempty"`
	Err             string    `json:"error,omitempty"`
}

// Backend handles one worker it launched. Attach, discovery of unrelated
// workers, scheduler policy, and operator authorization are not inferred here.
// Start and Resume take the controller's single-use LaunchAuthorization and
// must spawn their worker through it (StartJSONProcess), so the ownership
// record exists before the first native byte (A-02); the controller refuses a
// backend that returns a worker the authorization never recorded.
type Backend interface {
	Start(context.Context, Launch, *LaunchAuthorization) (string, error)
	Send(context.Context, string, string) (Delivery, error)
	Inspect(context.Context, string) (Observation, error)
	Interrupt(context.Context, string, string) error
	Resume(context.Context, Launch, string, *LaunchAuthorization) (string, error)
	Approve(context.Context, ApprovalDecision) error
	Events() <-chan Event
	Capabilities() Capabilities
	Close() error
}
