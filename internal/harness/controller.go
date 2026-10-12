package harness

// Controller is a transport owner for one adapter-launched worker. The durable
// receipt is captured once; every callback closes over it. Disconnection is an
// unknown observation, never session end. The ledger remains the assignment
// body store and only an explicit worker retrieval may record body delivery.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/JsizzleR/buddy-system/internal/fence"
)

type Identity struct {
	Schema      string `json:"schema"`
	Harness     string `json:"harness"`
	SessionID   string `json:"session_id"`
	Incarnation string `json:"incarnation"`
}

func (i Identity) Valid() bool {
	return i.Schema == "buddy.lifecycle.v1" && (i.Harness == "codex" || i.Harness == "claude") && i.SessionID != "" && i.Incarnation != ""
}
func (i Identity) Same(j Identity) bool { return i.Valid() && i == j }

type Ledger interface {
	Begin(context.Context, string, string, string) (Identity, error)
	Event(context.Context, Identity, string) error
	Inspect(context.Context, Identity) (json.RawMessage, error)
	Queued(context.Context, Identity, int64) (bool, error)
}
type Saved struct {
	Schema   string   `json:"schema"`
	Identity Identity `json:"identity"`
	Launch   Launch   `json:"launch"`
}
type Request struct {
	Schema    string            `json:"schema"`
	ID        string            `json:"id"`
	Operation string            `json:"operation"`
	Identity  Identity          `json:"identity"`
	MessageID int64             `json:"message_id,omitempty"`
	TurnID    string            `json:"turn_id,omitempty"`
	Approval  *ApprovalDecision `json:"approval,omitempty"`
}
type Receipt struct {
	Schema           string            `json:"schema"`
	RequestID        string            `json:"request_id,omitempty"`
	Operation        string            `json:"operation"`
	Identity         Identity          `json:"identity"`
	State            Observation       `json:"state"`
	Capabilities     Capabilities      `json:"capabilities,omitempty"`
	Delivery         *Delivery         `json:"wake_delivery,omitempty"`
	MessageID        int64             `json:"message_id,omitempty"`
	BodyDelivery     Observation       `json:"body_delivery"`
	Acknowledgment   Observation       `json:"acknowledgment"`
	Completion       Observation       `json:"completion"`
	PendingApproval  Observation       `json:"pending_approval"`
	Approvals        []Approval        `json:"approvals,omitempty"`
	NativeEvent      *Event            `json:"native_event,omitempty"`
	Ledger           json.RawMessage   `json:"ledger,omitempty"`
	Error            string            `json:"error,omitempty"`
	ApprovalResponse *ApprovalDecision `json:"approval_response,omitempty"`
	ApprovalDelivery *Observation      `json:"approval_delivery,omitempty"`
	TurnObservation  *TurnObservation  `json:"turn_observation,omitempty"`
}

// observeLedgerTimeout bounds each ledger call an Observe makes (F-30). Observe
// holds the controller lock across those calls, so an unbounded one (a wedged
// buddy child) used to wedge Handle, OperatorApprove and Close with it, and
// the adapter could not exit on stdin EOF. buddy's SQLite busy_timeout is
// 5 s, so a legitimately contended write may take that long; 10 s leaves
// room for process start and still bounds a wedged child. Close cancels a
// call in flight before it waits for the lock.
const observeLedgerTimeout = 10 * time.Second

// Approval presentation bounds (F-22). The summary follows the repo's 512-byte
// convention for a fenced description; the exact request on the one-shot event
// is capped so one Write of a large file cannot put megabytes of model text
// into the coordinator's context or the fsynced journal.
const (
	approvalSummaryCap     = 512
	approvalDetailExactCap = 16 << 10
)

type Controller struct {
	mu              sync.Mutex
	backend         Backend
	ledger          Ledger
	saved           Saved
	receiptPath     string
	journal         *os.File
	recorder        ownershipRecorder
	state           Observation
	approvals       map[string]Approval
	sent            map[int64]Delivery
	broken          error
	journalIdentity Identity
	approvalSent    map[string]bool
	claudeRecovery  claudeRecovery
	// observationGap is set when a native event for this worker could not be
	// recorded (F-11), and cleared only by an observation that establishes
	// the approval set (idle or ended). Until then no receipt says absent.
	observationGap string
	life           context.Context
	endLife        context.CancelFunc
	ledgerTimeout  time.Duration
	closed         bool
}

func NewController(b Backend, l Ledger, receiptPath string) (*Controller, error) {
	if !filepath.IsAbs(receiptPath) {
		return nil, errors.New("receipt path must be absolute")
	}
	// No automatic directory creation: choose the private artifact destination
	// deliberately. Existing receipt files are never replaced by a fresh start.
	flags := os.O_RDWR | os.O_APPEND
	if _, err := os.Lstat(receiptPath); os.IsNotExist(err) {
		flags |= os.O_CREATE
	} else if err != nil {
		return nil, err
	}
	// Recreating a lost journal beside a saved identity would forget every
	// accepted/uncertain wake and approval. Recovery needs both artifacts.
	j, err := os.OpenFile(receiptPath+".events", flags, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(j.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		j.Close()
		return nil, ErrJournalOwned
	}
	// The flock is held from here through admission, refresh, intent, spawn
	// and the controller's lifetime (A-02): it is what serializes live
	// adapters on one receipt. The ownership record binds to this journal's
	// device and inode, so a record copied beside another receipt cannot
	// satisfy it, and an interrupted publication's .tmp is cleared now, under
	// the lock, before anything reads the record.
	journal, err := fileIdentity(j)
	if err != nil {
		j.Close()
		return nil, err
	}
	recorder := ownershipRecorder{receiptPath: receiptPath, journal: journal}
	recorder.removeStaleTemp()
	life, endLife := context.WithCancel(context.Background())
	c := &Controller{backend: b, ledger: l, receiptPath: receiptPath, journal: j, recorder: recorder, state: Observation{State: "unknown"}, approvals: map[string]Approval{}, sent: map[int64]Delivery{}, approvalSent: map[string]bool{},
		life: life, endLife: endLife, ledgerTimeout: observeLedgerTimeout}
	dec := json.NewDecoder(j)
	for {
		var r Receipt
		err := dec.Decode(&r)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			j.Close()
			endLife()
			return nil, fmt.Errorf("uncertain journal: %w", err)
		}
		if r.Schema != Schema || !r.Identity.Valid() || c.journalIdentity.Valid() && !c.journalIdentity.Same(r.Identity) {
			j.Close()
			endLife()
			return nil, errors.New("journal has incompatible captured identity")
		}
		c.journalIdentity = r.Identity
		if r.Identity.Harness == "claude" {
			c.claudeRecovery.observe(r)
		}
		if r.MessageID > 0 && r.Delivery != nil {
			if r.Delivery.State == "not_submitted" {
				// A durable definite refusal releases its own intent's
				// reservation (F-10), and only that: it must follow an open,
				// turn-less intent for the same message. Neither row may carry
				// native turn/result evidence: a contradictory refusal cannot
				// make an uncertain wake replayable (verification V-02).
				if prior, open := c.sent[r.MessageID]; !open || prior.State != "unknown" || prior.TurnID != "" || prior.ResultRef != "" || r.Delivery.TurnID != "" || r.Delivery.ResultRef != "" {
					j.Close()
					endLife()
					return nil, errors.New("journal records a not-submitted wake without its open intent")
				}
				delete(c.sent, r.MessageID)
			} else {
				c.sent[r.MessageID] = *r.Delivery
			}
		}
		if d := r.ApprovalResponse; d != nil {
			_, idErr := approvalIDKey(d.ID)
			if idErr != nil || d.NativeSessionID != r.Identity.SessionID || d.Decision != "allow" && d.Decision != "deny" {
				j.Close()
				endLife()
				return nil, errors.New("invalid journal approval ownership")
			}
			key := approvalKey(*d)
			c.approvalSent[key] = r.ApprovalDelivery == nil || r.ApprovalDelivery.State != "not_submitted"
		}
	}
	return c, nil
}

func (c *Controller) persist(r Receipt) error {
	if c.broken != nil {
		return c.broken
	}
	if err := json.NewEncoder(c.journal).Encode(r); err != nil {
		c.broken = err
		return err
	}
	if err := c.journal.Sync(); err != nil {
		c.broken = err
		return err
	}
	return nil
}
func (c *Controller) receipt(op string) Receipt {
	aps := make([]Approval, 0, len(c.approvals))
	for _, a := range c.approvals {
		aps = append(aps, a)
	}
	pending := Observation{State: "observed_absent"}
	if !c.saved.Identity.Valid() || c.state.State == "unknown" {
		pending.State = "unknown"
	}
	if c.observationGap != "" {
		pending = Observation{State: "unknown", Detail: c.observationGap}
	}
	if len(aps) > 0 {
		pending = Observation{State: "observed"}
		if c.observationGap != "" {
			pending.State = "unknown"
			pending.Detail = "the listed approvals are last observations and may be stale; the set may be incomplete: " + c.observationGap
		}
	}
	return Receipt{Schema: Schema, Operation: op, Identity: c.saved.Identity, State: c.state, Capabilities: c.backend.Capabilities(), BodyDelivery: Observation{State: "unknown"}, Acknowledgment: Observation{State: "unknown"}, Completion: Observation{State: "unknown"}, PendingApproval: pending, Approvals: aps}
}

// scoped ties a call to the controller's lifetime, so Close can end a native
// or ledger call that holds the lock instead of waiting behind it.
func (c *Controller) scoped(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.life, cancel)
	return ctx, func() { stop(); cancel() }
}

// ledgerScoped is scoped plus the per-call ledger bound Observe uses.
func (c *Controller) ledgerScoped(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, c.ledgerTimeout)
	stop := context.AfterFunc(c.life, cancel)
	return ctx, func() { stop(); cancel() }
}

func (c *Controller) Start(ctx context.Context, harness string, launch Launch, label string) (Receipt, error) {
	ctx, done := c.scoped(ctx)
	defer done()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.saved.Identity.Valid() {
		return c.receipt("start"), errors.New("worker already bound")
	}
	if c.journalIdentity.Valid() {
		return c.receipt("start"), errors.New("journal already records a registration; reconcile its original receipt, never begin again")
	}
	if _, err := os.Lstat(c.receiptPath); !os.IsNotExist(err) {
		return c.receipt("start"), errors.New("receipt exists or cannot be inspected; use resume with captured receipt")
	}
	// An ownership record with no receipt is an earlier attempt that spawned
	// (or intended to) and never published; its worker may live. The `.start`
	// intent normally refuses this first; this covers a `.start` removed by hand.
	if _, err := os.Lstat(c.recorder.path()); !os.IsNotExist(err) {
		return c.receipt("start"), fmt.Errorf("ownership record %s exists without a receipt: an earlier attempt's worker may live; reconcile it, never begin again over it", c.recorder.path())
	}
	// A crash after native start or a committed Begin must leave a durable
	// barrier even before the lifecycle receipt can be published. An unresolved
	// intent requires human reconciliation, never a new native identity.
	intent, err := os.OpenFile(c.receiptPath+".start", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return c.receipt("start"), fmt.Errorf("unresolved start intent or unavailable artifact path: %w", err)
	}
	err = json.NewEncoder(intent).Encode(struct {
		Schema, Harness string
		Launch          Launch
	}{Schema, harness, launch})
	if err == nil {
		err = intent.Sync()
	}
	closeErr := intent.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = syncDirectory(c.receiptPath)
	}
	if err != nil {
		c.broken = err
		return c.receipt("start"), err
	}
	auth, err := c.authorize("start", nil)
	if err != nil {
		return c.receipt("start"), err
	}
	id, err := c.backend.Start(ctx, launch, auth)
	if err != nil {
		return c.receipt("start"), err
	}
	if !auth.Published() {
		// The assertion behind the guard: a backend that returned a worker
		// without spawning through its authorization left nothing a resume
		// could identify. Close it and fail.
		c.backend.Close()
		return c.receipt("start"), errors.New("backend returned a worker without a published ownership record; refusing to control a worker nothing recorded")
	}
	identity, err := c.ledger.Begin(ctx, harness, id, label)
	if err != nil {
		c.backend.Close()
		return c.receipt("start"), fmt.Errorf("registration outcome may be uncertain; do not retry begin: %w", err)
	}
	if !identity.Valid() || identity.SessionID != id || identity.Harness != harness {
		c.backend.Close()
		return c.receipt("start"), errors.New("invalid lifecycle receipt; registration outcome uncertain")
	}
	c.saved = Saved{Schema: Schema, Identity: identity, Launch: launch}
	f, err := os.OpenFile(c.receiptPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		err = json.NewEncoder(f).Encode(c.saved)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err != nil {
		c.broken = err
		c.backend.Close()
		return c.receipt("start"), fmt.Errorf("registration committed but receipt publication uncertain: %w", err)
	}
	if err = syncDirectory(c.receiptPath); err != nil {
		c.broken = err
		c.backend.Close()
		return c.receipt("start"), err
	}
	// The receipt is published: republish the spawned record for the same
	// attempt with the captured identity, BEFORE `.start` is removed, so a
	// crash anywhere in here leaves `.start` (refused) and never a
	// start-written record that admission would have to accept unidentified.
	if err = auth.bind(identity); err != nil {
		c.broken = err
		c.backend.Close()
		return c.receipt("start"), fmt.Errorf("receipt published but ownership record not bound to it: %w", err)
	}
	if err = os.Remove(c.receiptPath + ".start"); err == nil {
		err = syncDirectory(c.receiptPath)
	}
	if err != nil {
		c.broken = err
		c.backend.Close()
		return c.receipt("start"), err
	}
	c.state = Observation{State: "unknown", Detail: "registered; no native activity observation"}
	r := c.receipt("start")
	return r, c.persist(r)
}

func (c *Controller) Resume(ctx context.Context) (Receipt, error) {
	ctx, done := c.scoped(ctx)
	defer done()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.saved.Identity.Valid() {
		return c.receipt("resume"), errors.New("worker already bound")
	}
	saved, err := c.resumeAdmission()
	if err != nil {
		return c.receipt("resume"), err
	}
	if err = c.ledger.Event(ctx, saved.Identity, "refresh"); err != nil {
		return c.receipt("resume"), err
	}
	auth, err := c.authorize("resume", &saved.Identity)
	if err != nil {
		return c.receipt("resume"), err
	}
	id, err := c.backend.Resume(ctx, saved.Launch, saved.Identity.SessionID, auth)
	if err != nil {
		return c.receipt("resume"), err
	}
	if !auth.Published() {
		c.backend.Close()
		return c.receipt("resume"), errors.New("backend returned a worker without a published ownership record; refusing to control a worker nothing recorded")
	}
	if id != saved.Identity.SessionID {
		c.backend.Close()
		return c.receipt("resume"), errors.New("native resume changed session id")
	}
	c.saved = saved
	c.state = Observation{State: "unknown", Detail: "resumed captured incarnation; pending native state unknown"}
	r := c.receipt("resume")
	return r, c.persist(r)
}

// resumeAdmission is every passive resume check, in order, with the lock
// held: the receipt, the `.start` intent, the journal's identity, Claude's
// terminal evidence, and then the ownership record and the processes it names
// (A-02). It writes nothing and signals nothing, so `--wait-prior` can re-run
// it until it admits; Resume runs it again under the lock before refreshing
// the ledger and minting the launch authorization.
func (c *Controller) resumeAdmission() (Saved, error) {
	if c.backend.Capabilities()["resume"].Support == "unsupported" {
		return Saved{}, errors.New("unsupported native resume")
	}
	b, err := os.ReadFile(c.receiptPath)
	if err != nil {
		return Saved{}, err
	}
	var saved Saved
	if err = json.Unmarshal(b, &saved); err != nil || saved.Schema != Schema || !saved.Identity.Valid() {
		return Saved{}, errors.New("invalid captured receipt")
	}
	// An empty journal, or a start that never finished publishing, cannot
	// establish recovery state. Refuse before refreshing the ledger or starting
	// native control; uncertainty requires reconciliation of original artifacts.
	if _, err = os.Lstat(c.receiptPath + ".start"); !os.IsNotExist(err) {
		return Saved{}, errors.New("unresolved start intent or unavailable artifact path; reconcile before resume")
	}
	if !c.journalIdentity.Same(saved.Identity) {
		return Saved{}, errors.New("journal differs from captured lifecycle receipt")
	}
	if saved.Identity.Harness == "claude" && !c.claudeRecovery.resumable(c.sent, saved.Launch.HarnessVersion) {
		return Saved{}, errors.New("Claude resume requires journalled terminal results and idle; unfinished or uncertain recovery is unsupported")
	}
	rec, refusal := c.recorder.load(saved.Identity)
	if refusal != nil {
		return Saved{}, refusal
	}
	if refusal = classifyOwnership(rec, observeOwnership(rec)); refusal != nil {
		return Saved{}, refusal
	}
	return saved, nil
}

// authorize mints the single-use launch authorization for one attempt, with
// the lock and the journal flock held. The record it will publish is bound
// to this journal and receipt, names the identity when it is already known
// (resume), and carries this adapter's pid and start time.
func (c *Controller) authorize(operation string, identity *Identity) (*LaunchAuthorization, error) {
	attempt, err := newAttemptID()
	if err != nil {
		return nil, err
	}
	self, err := observeProcess(os.Getpid())
	if err != nil {
		return nil, fmt.Errorf("this adapter cannot pin itself, so it could not pin a worker either: %w", err)
	}
	return &LaunchAuthorization{rec: c.recorder, template: OwnershipRecord{Schema: OwnershipSchema, Attempt: attempt, Operation: operation,
		Journal: c.recorder.journal, Receipt: c.receiptPath, Identity: identity, Adapter: self}}, nil
}

// Handle accepts trusted coordinator control only. There is intentionally no
// assignment text or operator=true field. Approval responses use a different
// entry point, so a peer receipt cannot approve itself through this dispatcher.
func (c *Controller) Handle(ctx context.Context, q Request) (Receipt, error) {
	ctx, done := c.scoped(ctx)
	defer done()
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.receipt(q.Operation)
	r.RequestID = q.ID
	if q.Schema != Schema || !q.Identity.Same(c.saved.Identity) {
		return r, errors.New("captured identity or schema mismatch")
	}
	if c.broken != nil {
		return r, fmt.Errorf("journal unavailable: %w", c.broken)
	}
	// Validate the captured token before any native side effect. The
	// transaction belongs to Buddy; never replace a refused token with current.
	data, err := c.ledger.Inspect(ctx, c.saved.Identity)
	if err != nil {
		return r, err
	}
	r.Ledger = data
	switch q.Operation {
	case "inspect":
		obs, err := c.backend.Inspect(ctx, c.saved.Identity.SessionID)
		if err != nil {
			return r, err
		}
		r.State = obs
	case "reconcile":
		prior, ok := c.sent[q.MessageID]
		if !ok || q.MessageID <= 0 {
			return r, errors.New("reconcile requires a journalled Buddy wake message id")
		}
		if q.TurnID != "" && q.TurnID != prior.TurnID {
			return r, errors.New("reconcile turn differs from journalled wake")
		}
		r.MessageID = q.MessageID
		r.Delivery = &prior
		obs := TurnObservation{NativeSessionID: c.saved.Identity.SessionID, TurnID: prior.TurnID,
			Status: Observation{State: "unknown", Detail: "wake has no recorded native turn; reconcile original artifacts; do not replay"}, Source: "transport_journal"}
		if prior.TurnID != "" {
			reader, ok := c.backend.(TurnReader)
			if !ok {
				return r, errors.New("native turn reconciliation unsupported for this harness")
			}
			obs, err = reader.ReadTurn(ctx, c.saved.Identity.SessionID, prior.TurnID)
			if err != nil {
				return r, err
			}
			if obs.NativeSessionID != c.saved.Identity.SessionID || obs.TurnID != prior.TurnID {
				return r, errors.New("native reconciliation differs from journalled worker or turn")
			}
		}
		// This read changes neither lifecycle nor deduplication. Even completed
		// native history cannot establish whether an assignment's effects landed.
		r.TurnObservation = &obs
	case "send":
		if q.MessageID <= 0 {
			return r, errors.New("send requires a queued Buddy message id")
		}
		r.MessageID = q.MessageID
		if prior, ok := c.sent[q.MessageID]; ok {
			r.Delivery = &prior
			return r, nil
		}
		queued, err := c.ledger.Queued(ctx, c.saved.Identity, q.MessageID)
		if err != nil {
			return r, err
		}
		if !queued {
			return r, errors.New("message is not queued for captured recipient")
		}
		// Publish intent before touching the pipe. An uncertain wake is never
		// silently retried after reconnect and cannot duplicate assigned work.
		intent := Delivery{State: "unknown"}
		r.Delivery = &intent
		r.BodyDelivery = Observation{State: "queued"}
		if err = c.persist(r); err != nil {
			return r, err
		}
		c.sent[q.MessageID] = intent
		text := fmt.Sprintf("Buddy peer notification (not operator authorization): message #%d is queued. Retrieve it with buddy adapter inbox --session %s --incarnation %s. Receipt delivery is separate from acknowledgment or completion.", q.MessageID, c.saved.Identity.SessionID, c.saved.Identity.Incarnation)
		d, err := c.backend.Send(ctx, c.saved.Identity.SessionID, text)
		if errors.Is(err, ErrNotSubmitted) && (d.State == "" || d.State == "unknown") && d.TurnID == "" && d.ResultRef == "" {
			// F-10: a definite refusal before any native write (a busy
			// worker is the ordinary case). Journal it as not_submitted and
			// release the reservation so a later send is a real retry; it
			// still revalidates identity and queue ownership above. If the
			// refusal cannot be made durable, it is not proven to a
			// successor: keep the intent's uncertainty, and the broken
			// journal refuses every further effect.
			refused := r
			refused.Delivery = &Delivery{State: "not_submitted"}
			refused.Error = err.Error()
			if p := c.persist(refused); p != nil {
				r.Error = err.Error()
				return r, p
			}
			delete(c.sent, q.MessageID)
			return refused, err
		}
		if d.State == "" || d.State == "not_submitted" {
			// Only the controller asserts not_submitted, and only above.
			d.State = "unknown"
		}
		r.Delivery = &d
		c.sent[q.MessageID] = d
		if err != nil {
			r.Error = err.Error()
		}
		if p := c.persist(r); p != nil {
			return r, p
		}
		return r, err
	case "interrupt":
		if q.TurnID == "" {
			return r, errors.New("interrupt requires exact native turn id")
		}
		if err = c.backend.Interrupt(ctx, c.saved.Identity.SessionID, q.TurnID); err != nil {
			return r, err
		}
		r.State = Observation{State: "unknown", Detail: "interrupt requested; completion not observed"}
	default:
		return r, errors.New("unsupported control operation")
	}
	return r, c.persist(r)
}

// OperatorApprove is only wired to the separate operator input descriptor.
// Native approval IDs remain JSON values (large numeric IDs must not round).
func (c *Controller) OperatorApprove(ctx context.Context, q Request) (Receipt, error) {
	ctx, done := c.scoped(ctx)
	defer done()
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.receipt("approve")
	r.RequestID = q.ID
	if q.Schema != Schema || !q.Identity.Same(c.saved.Identity) || q.Approval == nil {
		return r, errors.New("approval requires captured identity")
	}
	if c.broken != nil {
		return r, c.broken
	}
	key, keyErr := approvalIDKey(q.Approval.ID)
	if keyErr != nil {
		return r, keyErr
	}
	a, ok := c.approvals[key]
	if !ok || c.approvalSent[approvalKey(*q.Approval)] || a.NativeSessionID != q.Approval.NativeSessionID || a.TurnID != q.Approval.TurnID {
		return r, errors.New("approval request is not pending for captured worker and turn")
	}
	if q.Approval.Decision != "allow" && q.Approval.Decision != "deny" {
		return r, errors.New("unsupported approval decision")
	}
	if _, err := c.ledger.Inspect(ctx, c.saved.Identity); err != nil {
		return r, err
	}
	// Journal the attempt first. Failure after sending remains uncertain;
	// never turn a transport error into another approval with a new id.
	r.ApprovalResponse = q.Approval
	r.ApprovalDelivery = &Observation{State: "unknown", Detail: "approval response intent"}
	if err := c.persist(r); err != nil {
		return r, err
	}
	c.approvalSent[approvalKey(*q.Approval)] = true
	err := c.backend.Approve(ctx, *q.Approval)
	if errors.Is(err, ErrNotSubmitted) {
		delete(c.approvalSent, approvalKey(*q.Approval))
		r = c.receipt("approve")
		r.RequestID = q.ID
		r.ApprovalResponse = q.Approval
		r.ApprovalDelivery = &Observation{State: "not_submitted", Detail: "native request refused before transport"}
		r.Error = err.Error()
		if p := c.persist(r); p != nil {
			return r, p
		}
		return r, err
	}
	// Once transport was attempted, this prompt is history, not another
	// actionable operator request. Only a definite pre-send refusal retains it.
	delete(c.approvals, key)
	c.state = Observation{State: "unknown", Detail: "approval response attempted; native resolution pending"}
	r = c.receipt("approve")
	r.RequestID = q.ID
	r.ApprovalResponse = q.Approval
	r.ApprovalDelivery = &Observation{State: "unknown", Detail: "native resolution pending"}
	if err != nil {
		r.Error = "approval outcome unknown: " + err.Error()
	} else {
		r.ApprovalDelivery = &Observation{State: "submitted", Detail: "response written; native resolution pending"}
	}
	if p := c.persist(r); p != nil {
		return r, p
	}
	return r, err
}

func (c *Controller) Observe(ctx context.Context, e Event) (Receipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.receipt("event")
	r.NativeEvent = presentEvent(e)
	if !c.saved.Identity.Valid() || e.NativeSessionID != c.saved.Identity.SessionID {
		return r, errors.New("event does not belong to captured worker")
	}
	r, err := c.observe(ctx, e)
	if err != nil {
		// F-11: this worker's event happened natively and was not recorded
		// (ledger busy past its bound, a newer ledger after an install, a
		// cancelled call, a broken journal). Returning with the old state
		// let receipts derive pending_approval "observed_absent" from it,
		// while a dropped approval sat unanswerable. Known approvals stay
		// listed; the set is no longer claimed complete.
		c.observationGap = fmt.Sprintf("native %s event was not recorded: %s", fence.Line(e.Kind, 64), fence.Line(err.Error(), 256))
		c.state = Observation{State: "unknown", Detail: "native event not recorded; activity and pending approvals unknown"}
		r = c.receipt("event")
		r.NativeEvent = presentEvent(e)
	}
	return r, err
}

func (c *Controller) observe(ctx context.Context, e Event) (Receipt, error) {
	r := c.receipt("event")
	r.NativeEvent = presentEvent(e)
	if c.broken != nil {
		return r, c.broken
	}
	ctx, done := c.ledgerScoped(ctx)
	defer done()
	if _, err := c.ledger.Inspect(ctx, c.saved.Identity); err != nil {
		return r, err
	}
	switch e.Kind {
	case "busy", "idle", "ended":
		ev := e.Kind
		if ev == "ended" {
			ev = "end"
		}
		if err := c.ledger.Event(ctx, c.saved.Identity, ev); err != nil {
			return r, err
		}
		c.state = Observation{State: "observed", Detail: e.Kind}
		if e.Kind != "busy" {
			// Idle and ended establish that nothing of this turn is pending,
			// so they are what may close an observation gap. A busy event
			// does not: it says nothing about an approval that was dropped.
			c.approvals = map[string]Approval{}
			c.observationGap = ""
		}
	case "approval":
		if e.Approval == nil || e.Approval.NativeSessionID != c.saved.Identity.SessionID {
			return r, errors.New("invalid approval event")
		}
		key, err := approvalIDKey(e.Approval.ID)
		if err != nil {
			return r, err
		}
		if c.approvalSent[approvalKey(ApprovalDecision{ID: e.Approval.ID, NativeSessionID: e.Approval.NativeSessionID, TurnID: e.Approval.TurnID})] {
			// Native re-emission after recovery does not prove that a lost
			// response was unsubmitted. Keep its durable one-shot reservation.
			c.state = Observation{State: "unknown", Detail: "approval response already attempted; native resolution unknown"}
			break
		}
		c.approvals[key] = presentApproval(*e.Approval, false)
		c.state = Observation{State: "observed", Detail: "waiting_for_approval"}
	case "approval_resolved":
		if e.Approval != nil {
			key, err := approvalIDKey(e.Approval.ID)
			if err != nil {
				return r, err
			}
			delete(c.approvals, key)
		}
		if len(c.approvals) == 0 {
			c.state = Observation{State: "unknown", Detail: "approval resolved; next native activity not yet observed"}
		}
	case "disconnected":
		c.state = Observation{State: "unknown", Detail: "native transport disconnected; session end not observed"}
		// Old prompts survive in the journal as history, never as actionable
		// requests on a dead connection. A successor must observe its own requests.
		c.approvals = map[string]Approval{}
	case "compaction_boundary":
		if c.saved.Identity.Harness != "claude" || !claudeManualCompactionQualified(c.saved.Launch.HarnessVersion) ||
			!claudeValidUUID(e.ItemID) || e.TurnID != "" || e.ResultRef != "" || e.ResultStatus != "" {
			return r, errors.New("Claude manual boundary requires qualified version and native boundary UUID, without inferred turn identity")
		}
		// Session history observation only; a separate exact-input local
		// result and subsequent idle are required for completed resume.
	case "compaction_started", "compaction_completed":
		if c.saved.Identity.Harness != "codex" || e.TurnID == "" || e.ItemID == "" {
			return r, errors.New("compaction observation requires Codex thread, turn and item identity")
		}
		// Persist the observation only. A completed history item is not an
		// idle turn, consumed inbox body, resolved approval or completed job.
	case "result", "acknowledged":
		// A native result references a TURN, not an assignment's completion.
	default:
		return r, errors.New("unsupported native event")
	}
	r = c.receipt("event")
	r.NativeEvent = presentEvent(e)
	return r, c.persist(r)
}

// Close ends the controller's lifetime BEFORE taking the lock, so a ledger or
// native call holding it returns instead of wedging shutdown (F-30).
func (c *Controller) Close() error {
	c.endLife()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.broken == nil {
		c.broken = errors.New("controller closed")
	}
	_ = c.backend.Close()
	return c.journal.Close()
}

// presentApproval bounds what a receipt repeats about a native request
// (F-22). The raw tool input is model text, up to the 4 MiB frame bound, and
// receipts reach the coordinator and the fsynced journal, re-embedded in every
// receipt while the prompt is pending. The pending list keeps identity, a
// digest of the exact bytes and a fenced 512-byte summary. exact keeps the
// bytes too, for the one-shot event the operator decides from, unless they are
// over the cap; truncated JSON would be a different request, so an oversized
// one is omitted, said so, and left to the summary and digest.
func presentApproval(a Approval, exact bool) Approval {
	if len(a.Detail) == 0 {
		return a
	}
	sum := sha256.Sum256(a.Detail)
	a.DetailSHA256 = hex.EncodeToString(sum[:])
	preview := a.Detail
	if len(preview) > 4*approvalSummaryCap {
		preview = preview[:4*approvalSummaryCap]
	}
	a.Summary = fence.Line(string(preview), approvalSummaryCap)
	if !exact {
		a.Detail = nil
		return a
	}
	if len(a.Detail) > approvalDetailExactCap {
		a.DetailOmitted = fmt.Sprintf("native request is %d bytes, over the %d-byte receipt bound; only its digest and summary are shown", len(a.Detail), approvalDetailExactCap)
		a.Detail = nil
		return a
	}
	a.Detail = append(json.RawMessage(nil), a.Detail...)
	return a
}

func presentEvent(e Event) *Event {
	if e.Approval != nil {
		a := presentApproval(*e.Approval, true)
		e.Approval = &a
	}
	return &e
}

func approvalKey(d ApprovalDecision) string {
	id, _ := approvalIDKey(d.ID) // callers validate first; never converts a number through float64
	key, _ := json.Marshal([3]string{d.NativeSessionID, d.TurnID, id})
	return string(key)
}

// Native string identity is its decoded value, not one JSON spelling. Default
// journal encoding escapes '<', '&', U+2028 and U+2029; raw-byte map keys would
// miss the same request after restart. Integers retain their exact decimal
// value and a distinct type prefix, including those beyond float64 precision.
func approvalIDKey(raw json.RawMessage) (string, error) {
	if !json.Valid(raw) {
		return "", errors.New("invalid native approval id")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", err
	}
	switch value := v.(type) {
	case string:
		return "string:" + value, nil
	case json.Number:
		if n, err := value.Int64(); err == nil {
			return fmt.Sprintf("integer:%d", n), nil
		}
	}
	return "", errors.New("native approval id must be a string or int64")
}
func syncDirectory(path string) error {
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
