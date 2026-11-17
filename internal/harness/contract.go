// Package harness translates native worker controls. It owns no claims or task
// database: the caller supplies an immutable Buddy lifecycle receipt, and a
// native turn result never means review, validation, or landing succeeded.
package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/JsizzleR/buddy-system/internal/fence"
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

// nativeErrorCap bounds the part of an error a native server wrote (a Codex
// RPC error reply, a Claude control_response error) before it becomes a
// receipt's error (D-198). The error was the server's text verbatim, up to
// the 4 MiB frame bound, on every receipt the journal keeps and the
// coordinator reads. 512 is the desc cap (invariant 9) and the bound the
// renewal receipt already puts on its ledger error; it keeps a JSON-RPC
// code and message whole.
const nativeErrorCap = 512

// nativeTokenCap bounds a native identifier spliced into an error (a result
// subtype or terminal reason): short names, 64 as codexStatusCap.
const nativeTokenCap = 64

// nativeIDCap bounds a native identifier a receipt only DISPLAYS (D-199): an
// item id, a native approval id, a result reference. Each was the server's
// string verbatim, up to the 4 MiB frame bound, in every native_event and,
// for an approval, in the pending list every later receipt repeats. 128 is
// the slug cap (invariant 9) and nativeKey's bound; real ids (UUIDs,
// toolu_ ids) are under 64.
const nativeIDCap = 128

// displayNativeID fences an identifier nothing echoes back to the adapter:
// fence.Field, so it stays one token (a space shows as ␣) and a value made
// only of controls stays NONEMPTY (∅) where the controller tests emptiness;
// an absent id stays absent (omitempty). An ordinary id is byte-for-byte
// unchanged. Never for an id the operator or the server sends back (the Codex
// thread and turn ids, a request id): a fenced copy would not match (D-199).
func displayNativeID(id string) string {
	if id == "" {
		return ""
	}
	return fence.Field(id, nativeIDCap)
}

// nativeKey admits a native turn id or string request id the adapter STORES
// or EMITS and is later sent back (D-202): 1..128 bytes of [A-Za-z0-9._:/-],
// tested on the DECODED value. Such a key cannot be fenced where it is
// rendered, because the receipt IS the round trip: the coordinator copies
// turn_id and an approval's id out of a receipt into interrupt and approve,
// and a fenced copy matches nothing (codex.go Interrupt, takeApproval). Raw,
// it went up to the 4 MiB frame bound into every receipt, with bidi and
// zero-width characters intact (D-199 left it open). Measured on the
// 2026-10-13 A-07 rerun's Codex 0.160.0 traces: thread and turn ids are
// 36-byte UUIDv7 but for one turn id `auto-compact-0` (so not a UUID check),
// approval request ids are integers; Claude 2.1.291 and 2.1.312 request ids
// are UUIDs. Inferred, not measured, for the admitted 0.162.1-0.165.0. A key
// that passes is exactly what fence.Field(s, 128) returns, so every later
// render is one token with no copy. Never inside Identity.Valid,
// approvalIDKey or journal reload: those would refuse a whole older journal.
// It was codexPlainTurnID (D-197), the progress turn id's check.
func nativeKey(s string) bool {
	if s == "" || len(s) > nativeIDCap {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_' || b == '.' || b == '/' || b == ':') {
			return false
		}
	}
	return true
}

// The native SESSION id (the Codex thread id) is not a nativeKey: it becomes
// the ledger's session id through `buddy lifecycle begin --session`, so it
// takes the ledger's own predicate, fence.ValidSessionID (D-201). A thread id
// "a/b" passes nativeKey, and the ledger would then refuse it at Begin, after
// the child is up, as an uncertain registration (D-202).

// nativeKeyRefusal names a key the adapter refused without echoing it: the
// field and its byte length only, so the error is bounded and one line.
func nativeKeyRefusal(field, value string) string {
	return fmt.Sprintf("%s outside the plain key set (%d bytes)", field, len(value))
}

// nativeRefusalWrite bounds the reader's own native answer to a request it
// refuses (D-202): the reader writes it, so a server that stops reading its
// stdin while the adapter echoes an id up to the frame bound would hold the
// reader in Encode with no deadline, publishing nothing, until Close. Past
// the bound WriteContext closes the transport and the reader reports the
// disconnect; an approval_refused is published only after a write that
// succeeded.
const nativeRefusalWrite = 10 * time.Second

func writeRefusal(w interface {
	WriteContext(context.Context, any) error
}, v any) error {
	ctx, cancel := context.WithTimeout(context.Background(), nativeRefusalWrite)
	defer cancel()
	return w.WriteContext(ctx, v)
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
