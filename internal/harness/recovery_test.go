package harness

// The identity receipt alone cannot recover deduplication state. Missing journal
// bytes must never turn a previously accepted wake into a fresh native input.
import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

type crashAtTransport struct {
	fakeBackend
	operation string
}

func (b *crashAtTransport) Send(ctx context.Context, id, text string) (Delivery, error) {
	if b.operation == "send" {
		panic("fixture crash before wake result")
	}
	return b.fakeBackend.Send(ctx, id, text)
}
func (b *crashAtTransport) Approve(ctx context.Context, d ApprovalDecision) error {
	if b.operation == "approve" {
		panic("fixture crash before approval result")
	}
	return b.fakeBackend.Approve(ctx, d)
}

// Stop the call before it can journal a transport result. Resume must recover
// from the actual synced intent bytes, without tests manufacturing a journal.
func TestControllerIntentOnlyCrashNeverReplays(t *testing.T) {
	for _, operation := range []string{"send", "approve"} {
		t.Run(operation, func(t *testing.T) {
			c, _, l, path := controllerFixture(t)
			ctx := context.Background()
			a := Approval{ID: []byte(`"pending"`), NativeSessionID: l.identity.SessionID, TurnID: "t1"}
			e := Event{Kind: "approval", NativeSessionID: l.identity.SessionID, Approval: &a}
			q := Request{Schema: Schema, Operation: operation, Identity: l.identity, MessageID: 7,
				Approval: &ApprovalDecision{ID: a.ID, NativeSessionID: a.NativeSessionID, TurnID: a.TurnID, Decision: "allow"}}
			if operation == "approve" {
				if _, err := c.Observe(ctx, e); err != nil {
					t.Fatal(err)
				}
			}
			c.backend = &crashAtTransport{operation: operation}
			func() {
				defer func() {
					want := "fixture crash before wake result"
					if operation == "approve" {
						want = "fixture crash before approval result"
					}
					if got := recover(); got != want {
						t.Fatalf("wrong crash: %v", got)
					}
				}()
				if operation == "send" {
					c.Handle(ctx, q)
				} else {
					c.OperatorApprove(ctx, q)
				}
			}()
			c.Close()
			data, err := os.ReadFile(path + ".events")
			if err != nil {
				t.Fatal(err)
			}
			rows := strings.Split(strings.TrimSpace(string(data)), "\n")
			var last Receipt
			if err = json.Unmarshal([]byte(rows[len(rows)-1]), &last); err != nil {
				t.Fatal(err)
			}
			if last.Operation != operation || (operation == "send" && (last.Delivery == nil || last.Delivery.State != "unknown")) ||
				(operation == "approve" && (last.ApprovalDelivery == nil || last.ApprovalDelivery.State != "unknown")) {
				t.Fatalf("missing durable intent: %#v", last)
			}
			b := &fakeBackend{}
			resumed, err := NewController(b, l, path)
			if err != nil {
				t.Fatal(err)
			}
			defer resumed.Close()
			if _, err = resumed.Resume(ctx); err != nil {
				t.Fatal(err)
			}
			if operation == "send" {
				r, err := resumed.Handle(ctx, q)
				if err != nil || r.Delivery.State != "unknown" || r.Delivery.TurnID != "" || b.sends != 0 {
					t.Fatalf("wake replayed: %#v %v", r, err)
				}
				q.MessageID++
				if _, err = resumed.Handle(ctx, q); err != nil || b.sends != 1 {
					t.Fatal("fresh wake refused")
				}
			} else {
				if _, err = resumed.Observe(ctx, e); err != nil {
					t.Fatal(err)
				}
				if _, err = resumed.OperatorApprove(ctx, q); err == nil || b.approves != 0 {
					t.Fatal("approval intent replayed")
				}
				a.ID = []byte(`"fresh"`)
				q.Approval.ID = a.ID
				if _, err = resumed.Observe(ctx, e); err != nil {
					t.Fatal(err)
				}
				if _, err = resumed.OperatorApprove(ctx, q); err != nil || b.approves != 1 {
					t.Fatal("fresh approval refused")
				}
			}
		})
	}
}

// A response written to a pipe has no native resolution receipt. Presenting
// that spent prompt as waiting for the operator invites a second decision.
func TestControllerAttemptedApprovalIsNotPending(t *testing.T) {
	for _, outcome := range []string{"submitted", "uncertain", "not_submitted"} {
		t.Run(outcome, func(t *testing.T) {
			c, b, l, path := controllerFixture(t)
			ctx := context.Background()
			a := Approval{ID: []byte(`"pending"`), NativeSessionID: l.identity.SessionID, TurnID: "t1"}
			e := Event{Kind: "approval", NativeSessionID: l.identity.SessionID, Approval: &a}
			if _, err := c.Observe(ctx, e); err != nil {
				t.Fatal(err)
			}
			q := Request{Schema: Schema, Identity: l.identity, Approval: &ApprovalDecision{ID: a.ID, NativeSessionID: a.NativeSessionID, TurnID: a.TurnID, Decision: "allow"}}
			if outcome == "uncertain" {
				b.approveErr = errors.New("partial transport write")
			}
			if outcome == "not_submitted" {
				b.approveErr = ErrNotSubmitted
			}
			r, err := c.OperatorApprove(ctx, q)
			if (err == nil) != (outcome == "submitted") {
				t.Fatalf("wrong outcome: %v", err)
			}
			check := func(r Receipt) {
				t.Helper()
				if outcome == "not_submitted" {
					if len(r.Approvals) != 1 || r.PendingApproval.State != "observed" {
						t.Fatalf("unsubmitted prompt lost: %#v", r)
					}
				} else if len(r.Approvals) != 0 || r.PendingApproval.State != "unknown" || r.State.State != "unknown" {
					t.Fatalf("attempted prompt still current: %#v", r)
				}
			}
			check(r)
			c.Close()
			b2 := &fakeBackend{}
			resumed, err := NewController(b2, l, path)
			if err != nil {
				t.Fatal(err)
			}
			defer resumed.Close()
			if _, err = resumed.Resume(ctx); err != nil {
				t.Fatal(err)
			}
			// A repeated native request cannot erase the durable attempt.
			r, err = resumed.Observe(ctx, e)
			if err != nil {
				t.Fatal(err)
			}
			check(r)
			_, err = resumed.OperatorApprove(ctx, q)
			if outcome == "not_submitted" {
				if err != nil || b2.approves != 1 {
					t.Fatal("unsubmitted positive control refused")
				}
			} else if err == nil || b2.approves != 0 {
				t.Fatal("spent approval replayed")
			}
			// A fresh native request remains actionable after every outcome.
			a.ID = []byte(`"fresh"`)
			if _, err = resumed.Observe(ctx, e); err != nil {
				t.Fatal(err)
			}
			q.Approval.ID = a.ID
			if _, err = resumed.OperatorApprove(ctx, q); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestControllerResumeRequiresRecoveryArtifacts(t *testing.T) {
	for _, damage := range []string{"intact", "missing", "empty", "partial", "start-intent"} {
		t.Run(damage, func(t *testing.T) {
			c, _, l, path := controllerFixture(t)
			ctx := context.Background()
			q := Request{Schema: Schema, Operation: "send", Identity: l.identity, MessageID: 7}
			if _, err := c.Handle(ctx, q); err != nil {
				t.Fatal(err)
			}
			c.Close()
			var err error
			switch damage {
			case "missing":
				err = os.Remove(path + ".events")
			case "empty":
				err = os.WriteFile(path+".events", nil, 0600)
			case "partial":
				err = os.WriteFile(path+".events", []byte(`{"schema":`), 0600)
			case "start-intent":
				err = os.WriteFile(path+".start", []byte("unresolved"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			b := &fakeBackend{}
			resumed, err := NewController(b, l, path)
			if err == nil {
				defer resumed.Close()
				_, err = resumed.Resume(ctx)
			}
			if damage != "intact" {
				if err == nil || len(l.events) != 0 {
					t.Fatalf("damaged recovery refreshed or resumed: %v, events %v", err, l.events)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			r, err := resumed.Handle(ctx, q)
			if err != nil || r.Delivery.State != "delivered" || b.sends != 0 {
				t.Fatalf("intact recovery replayed wake: %#v, %v, sends %d", r, err, b.sends)
			}
			q.MessageID++
			if _, err = resumed.Handle(ctx, q); err != nil || b.sends != 1 {
				t.Fatalf("fresh wake positive control: %v, sends %d", err, b.sends)
			}
		})
	}
}

// Reconciliation names a prior journalled wake, never a peer-supplied native ID.
// A history read cannot consume mail, finish work, or resurrect an old approval.
type recoveryBackend struct {
	fakeBackend
	reads    int
	observed TurnObservation
}

func (b *recoveryBackend) ReadTurn(_ context.Context, session, turn string) (TurnObservation, error) {
	b.reads++
	return b.observed, nil
}
func TestControllerReconcilesOnlyRecordedTurn(t *testing.T) {
	c, _, l, path := controllerFixture(t)
	ctx := context.Background()
	q := Request{Schema: Schema, Operation: "send", Identity: l.identity, MessageID: 7}
	if _, err := c.Handle(ctx, q); err != nil {
		t.Fatal(err)
	}
	c.Close()
	b := &recoveryBackend{observed: TurnObservation{NativeSessionID: l.identity.SessionID, TurnID: "turn-1", Status: Observation{State: "completed"}, Source: "native_thread_read"}}
	c, err := NewController(b, l, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err = c.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	q.Operation = "reconcile"
	r, err := c.Handle(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if r.TurnObservation == nil || r.TurnObservation.Status.State != "completed" || r.MessageID != 7 || b.reads != 1 {
		t.Fatalf("lost result not reconciled: %#v reads %d", r, b.reads)
	}
	if r.Completion.State != "unknown" || r.BodyDelivery.State != "unknown" || r.PendingApproval.State != "unknown" || len(l.events) != 1 {
		t.Fatalf("history promoted into current effects: %#v %v", r, l.events)
	}
	q.MessageID = 8
	if _, err = c.Handle(ctx, q); err == nil || b.reads != 1 {
		t.Fatal("unrecorded wake queried native history")
	}
	q.MessageID, q.TurnID = 7, "other-turn"
	if _, err = c.Handle(ctx, q); err == nil || b.reads != 1 {
		t.Fatal("peer substituted turn ID")
	}
	q.TurnID = ""
	b.observed.TurnID = "other-turn"
	if _, err = c.Handle(ctx, q); err == nil {
		t.Fatal("mismatched native result accepted")
	}
	b.observed.TurnID = "turn-1"
	l.stale = true
	if _, err = c.Handle(ctx, q); err == nil || b.reads != 2 {
		t.Fatal("stale token queried history")
	}
	l.stale = false
	c.sent[7] = Delivery{State: "unknown"} // crash after intent, before native reply
	r, err = c.Handle(ctx, q)
	if err != nil || r.TurnObservation == nil || r.TurnObservation.Status.State != "unknown" || b.reads != 2 {
		t.Fatalf("unknown turn guessed from thread history: %#v %v", r, err)
	}
	q.Operation = "send"
	if _, err = c.Handle(ctx, q); err != nil || b.sends != 0 {
		t.Fatal("reconciliation enabled replay")
	}
}

func TestControllerDisconnectInvalidatesPendingApproval(t *testing.T) {
	c, b, l, _ := controllerFixture(t)
	ctx := context.Background()
	a := Approval{ID: []byte(`"pending"`), NativeSessionID: l.identity.SessionID, TurnID: "t1"}
	if _, err := c.Observe(ctx, Event{Kind: "approval", NativeSessionID: l.identity.SessionID, Approval: &a}); err != nil {
		t.Fatal(err)
	}
	r, err := c.Observe(ctx, Event{Kind: "disconnected", NativeSessionID: l.identity.SessionID})
	if err != nil || r.PendingApproval.State != "unknown" || len(r.Approvals) != 0 {
		t.Fatalf("dead approval still current: %#v %v", r, err)
	}
	q := Request{Schema: Schema, Identity: l.identity, Approval: &ApprovalDecision{ID: a.ID, NativeSessionID: a.NativeSessionID, TurnID: a.TurnID, Decision: "allow"}}
	if _, err = c.OperatorApprove(ctx, q); err == nil || b.approves != 0 {
		t.Fatal("disconnected approval submitted")
	}
}

// The history projection is kept for a bounded read once one is qualified; it
// still refuses mismatched, duplicated or unknown history and never guesses.
func TestCodexHistoryProjectionIsExact(t *testing.T) {
	const id = "native-α"
	for _, scenario := range []string{"completed", "interrupted", "failed", "inProgress", "missing", "wrong-thread", "duplicate", "unknown-status", "malformed"} {
		t.Run(scenario, func(t *testing.T) {
			thread := id
			turns := []map[string]string{{"id": "unrelated", "status": "completed"}}
			if scenario != "missing" {
				turns = append(turns, map[string]string{"id": "lost", "status": scenario})
			}
			if scenario == "wrong-thread" {
				thread = "peer"
				turns[1]["status"] = "completed"
			}
			if scenario == "duplicate" {
				turns = append(turns[:1], map[string]string{"id": "lost", "status": "completed"}, map[string]string{"id": "lost", "status": "completed"})
			}
			raw, err := json.Marshal(map[string]any{"thread": map[string]any{"id": thread, "status": map[string]string{"type": "idle"}, "turns": turns}})
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "malformed" {
				raw = json.RawMessage(`"not an object"`)
			}
			obs, err := codexTurnFromHistory(raw, id, "lost")
			bad := scenario == "wrong-thread" || scenario == "duplicate" || scenario == "unknown-status" || scenario == "malformed"
			if bad {
				if err == nil {
					t.Fatalf("accepted bad history: %#v", obs)
				}
				return
			}
			want := scenario
			if scenario == "missing" {
				want = "unknown"
			}
			if err != nil || obs.Status.State != want || obs.NativeSessionID != id || obs.TurnID != "lost" || obs.Source != "native_thread_read" {
				t.Fatalf("history: %#v %v", obs, err)
			}
		})
	}
}

// F-08: the only qualified history read returns the WHOLE thread on the live
// control transport, and a reply over the 4 MiB frame bound ended the worker.
// Reconcile now refuses before any native request: no thread/read with turns
// is written, the refusal is a definite not-submitted, the worker stays live
// (a bounded inspect still round-trips) and its control state is unchanged.
func TestCodexReconcileRefusesUnboundedHistoryRead(t *testing.T) {
	c, p, launch := codexFixture(t)
	id := codexStart(t, c, launch)
	if got := c.Capabilities()["reconcile"].Support; got != "unsupported" {
		t.Fatalf("reconcile capability still %q", got)
	}
	before := len(p.allWrites())
	obs, err := c.ReadTurn(context.Background(), id, "lost")
	if !errors.Is(err, ErrNotSubmitted) || obs.Status.State != "unknown" || obs.TurnID != "lost" {
		t.Fatalf("reconcile was not a definite pre-transport refusal: %#v %v", obs, err)
	}
	for _, raw := range p.allWrites()[before:] {
		t.Fatalf("reconcile wrote a native request: %s", raw)
	}
	// Positive control: the transport is live and bounded reads still work.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	state, err := c.Inspect(ctx, id)
	if err != nil || state.State != "idle" {
		t.Fatalf("worker not live after refused reconcile: %#v %v", state, err)
	}
	var q struct {
		IncludeTurns bool `json:"includeTurns"`
	}
	if err := json.Unmarshal(codexRequest(t, p, "thread/read").Params, &q); err != nil || q.IncludeTurns {
		t.Fatalf("inspect asked for full history: %v", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != "idle" || c.activeTurn != "" || len(c.approvals) != 0 || len(c.finished) != 0 || len(c.events) != 0 || c.closed {
		t.Fatal("refused reconcile altered current controls")
	}
}
