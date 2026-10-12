package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type fakeBackend struct {
	sends, approves, interrupts int
	events                      chan Event
	approveErr                  error
	// unrecorded models a backend path that returns a worker without
	// spawning through its launch authorization; the controller must refuse it.
	unrecorded bool
}

func (b *fakeBackend) Start(_ context.Context, _ Launch, auth *LaunchAuthorization) (string, error) {
	if b.unrecorded {
		return "native-1", nil
	}
	if err := fakeSpawn(auth); err != nil {
		return "", err
	}
	return "native-1", nil
}
func (b *fakeBackend) Resume(_ context.Context, _ Launch, id string, auth *LaunchAuthorization) (string, error) {
	if b.unrecorded {
		return id, nil
	}
	if err := fakeSpawn(auth); err != nil {
		return "", err
	}
	return id, nil
}
func (b *fakeBackend) Send(context.Context, string, string) (Delivery, error) {
	b.sends++
	return Delivery{State: "delivered", TurnID: "turn-1"}, nil
}
func (b *fakeBackend) Inspect(context.Context, string) (Observation, error) {
	return Observation{State: "unknown"}, nil
}
func (b *fakeBackend) Interrupt(context.Context, string, string) error { b.interrupts++; return nil }
func (b *fakeBackend) Approve(context.Context, ApprovalDecision) error {
	b.approves++
	return b.approveErr
}
func (b *fakeBackend) Events() <-chan Event { return b.events }
func (b *fakeBackend) Capabilities() Capabilities {
	return Capabilities{"start": {Support: "experimental"}}
}
func (b *fakeBackend) Close() error { return nil }

type fakeLedger struct {
	identity Identity
	events   []string
	stale    bool
	queued   bool
	beginErr error
}

func (l *fakeLedger) Begin(_ context.Context, h, id, _ string) (Identity, error) {
	l.identity = Identity{"buddy.lifecycle.v1", h, id, "captured-token"}
	return l.identity, l.beginErr
}
func (l *fakeLedger) Event(_ context.Context, i Identity, e string) error {
	if l.stale || !i.Same(l.identity) {
		return errors.New("stale incarnation")
	}
	l.events = append(l.events, e)
	return nil
}
func (l *fakeLedger) Inspect(_ context.Context, i Identity) (json.RawMessage, error) {
	if l.stale || !i.Same(l.identity) {
		return nil, errors.New("stale incarnation")
	}
	return json.RawMessage(`{"schema":"buddy.adapter.v1"}`), nil
}
func (l *fakeLedger) Queued(context.Context, Identity, int64) (bool, error) { return l.queued, nil }
func controllerFixture(t *testing.T) (*Controller, *fakeBackend, *fakeLedger, string) {
	t.Helper()
	b := &fakeBackend{}
	l := &fakeLedger{queued: true}
	path := filepath.Join(t.TempDir(), "receipt.json")
	c, err := NewController(b, l, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Start(context.Background(), "codex", Launch{Worktree: t.TempDir()}, "test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, b, l, path
}
func TestControllerCapturedIdentityFencesEverySideEffect(t *testing.T) {
	c, b, l, _ := controllerFixture(t)
	ctx := context.Background()
	q := Request{Schema: Schema, Operation: "send", Identity: l.identity, MessageID: 7}
	r, err := c.Handle(ctx, q)
	if err != nil || r.Delivery.State != "delivered" || b.sends != 1 {
		t.Fatalf("positive send: %#v %v", r, err)
	}
	if r.Completion.State != "unknown" || r.Acknowledgment.State != "unknown" || r.BodyDelivery.State != "queued" {
		t.Fatalf("wake promoted: %#v", r)
	}
	// Both malformed caller identity and an actual ledger successor refuse.
	q.Identity.Incarnation = "successor"
	if _, err = c.Handle(ctx, q); err == nil {
		t.Fatal("wrong captured caller allowed")
	}
	l.stale = true
	q.Identity = l.identity
	q.MessageID = 8
	if _, err = c.Handle(ctx, q); err == nil {
		t.Fatal("stale recipient allowed")
	}
	if _, err = c.Observe(ctx, Event{Kind: "ended", NativeSessionID: l.identity.SessionID}); err == nil {
		t.Fatal("delayed end allowed")
	}
	if b.sends != 1 || len(l.events) != 0 {
		t.Fatalf("stale side effects: %#v %#v", b, l)
	}
}
func TestControllerDuplicateWakeSurvivesRestart(t *testing.T) {
	c, b, l, path := controllerFixture(t)
	ctx := context.Background()
	q := Request{Schema: Schema, Operation: "send", Identity: l.identity, MessageID: 7}
	if _, err := c.Handle(ctx, q); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Handle(ctx, q); err != nil {
		t.Fatal(err)
	}
	if b.sends != 1 {
		t.Fatal("duplicate wake")
	}
	if _, err := NewController(&fakeBackend{}, l, path); err == nil {
		t.Fatal("concurrent controller acquired journal")
	}
	c.Close()
	b2 := &fakeBackend{}
	c2, err := NewController(b2, l, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if _, err = c2.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = c2.Handle(ctx, q); err != nil {
		t.Fatal(err)
	}
	if b2.sends != 0 {
		t.Fatal("restart replayed wake")
	}
}
func TestControllerPeerControlCannotApprove(t *testing.T) {
	c, b, l, _ := controllerFixture(t)
	ctx := context.Background()
	a := Approval{ID: json.RawMessage(`9007199254740993`), NativeSessionID: l.identity.SessionID, TurnID: "t1", Kind: "command"}
	r, err := c.Observe(ctx, Event{Kind: "approval", NativeSessionID: l.identity.SessionID, Approval: &a})
	if err != nil {
		t.Fatal(err)
	}
	if r.State.Detail != "waiting_for_approval" || r.PendingApproval.State != "observed" {
		t.Fatalf("approval appears idle: %#v", r)
	}
	q := Request{Schema: Schema, Operation: "approve", Identity: l.identity, Approval: &ApprovalDecision{ID: a.ID, NativeSessionID: a.NativeSessionID, TurnID: a.TurnID, Decision: "allow"}}
	if _, err = c.Handle(ctx, q); err == nil || b.approves != 0 {
		t.Fatal("peer approved")
	}
	q.Approval.TurnID = "old"
	if _, err = c.OperatorApprove(ctx, q); err == nil {
		t.Fatal("stale turn approved")
	}
	q.Approval.TurnID = a.TurnID
	if _, err = c.OperatorApprove(ctx, q); err != nil || b.approves != 1 {
		t.Fatalf("operator positive: %v", err)
	}
	if _, err = c.OperatorApprove(ctx, q); err == nil || b.approves != 1 {
		t.Fatal("approval replay")
	}
}
func TestControllerNativeResultAndDisconnectDoNotEndSession(t *testing.T) {
	c, _, l, _ := controllerFixture(t)
	ctx := context.Background()
	for _, kind := range []string{"result", "acknowledged", "disconnected"} {
		r, err := c.Observe(ctx, Event{Kind: kind, NativeSessionID: l.identity.SessionID, ResultRef: "native-result"})
		if err != nil {
			t.Fatal(err)
		}
		if r.Completion.State != "unknown" || r.Acknowledgment.State != "unknown" {
			t.Fatal("native event promoted into work completion")
		}
	}
	if len(l.events) != 0 {
		t.Fatal("disconnect ended session")
	}
	if _, err := c.Observe(ctx, Event{Kind: "idle", NativeSessionID: l.identity.SessionID}); err != nil {
		t.Fatal(err)
	}
	if len(l.events) != 1 || l.events[0] != "idle" {
		t.Fatal("positive idle did not translate")
	}
}
func TestControllerFailedPublicationNeverRecaptures(t *testing.T) {
	b := &fakeBackend{}
	l := &fakeLedger{}
	path := filepath.Join(t.TempDir(), "receipt")
	c, err := NewController(b, l, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = os.WriteFile(path, []byte("already captured"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Start(context.Background(), "codex", Launch{}, ""); err == nil {
		t.Fatal("existing receipt overwritten")
	}
	if l.identity.Valid() {
		t.Fatal("begin ran despite existing receipt")
	}
}

func TestControllerApprovalAttemptSurvivesResume(t *testing.T) {
	c, b, l, path := controllerFixture(t)
	ctx := context.Background()
	a := Approval{ID: json.RawMessage(`"request"`), NativeSessionID: l.identity.SessionID, TurnID: "t1", Kind: "command"}
	if _, err := c.Observe(ctx, Event{Kind: "approval", NativeSessionID: l.identity.SessionID, Approval: &a}); err != nil {
		t.Fatal(err)
	}
	q := Request{Schema: Schema, Operation: "approve", Identity: l.identity, Approval: &ApprovalDecision{ID: a.ID, NativeSessionID: a.NativeSessionID, TurnID: a.TurnID, Decision: "allow"}}
	b.approveErr = errors.New("lost native response")
	if _, err := c.OperatorApprove(ctx, q); err == nil {
		t.Fatal("missing uncertain failure")
	}
	c.Close()
	b2 := &fakeBackend{}
	c2, err := NewController(b2, l, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if _, err = c2.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = c2.Observe(ctx, Event{Kind: "approval", NativeSessionID: l.identity.SessionID, Approval: &a}); err != nil {
		t.Fatal(err)
	}
	if _, err = c2.OperatorApprove(ctx, q); err == nil || b2.approves != 0 {
		t.Fatal("uncertain approval replayed after restart")
	}
}
func TestControllerDefiniteApprovalRefusalAllowsDeny(t *testing.T) {
	c, b, l, _ := controllerFixture(t)
	ctx := context.Background()
	a := Approval{ID: json.RawMessage(`"interactive"`), NativeSessionID: l.identity.SessionID, TurnID: "t1", Kind: "interactive"}
	c.Observe(ctx, Event{Kind: "approval", NativeSessionID: l.identity.SessionID, Approval: &a})
	q := Request{Schema: Schema, Operation: "approve", Identity: l.identity, Approval: &ApprovalDecision{ID: a.ID, NativeSessionID: a.NativeSessionID, TurnID: a.TurnID, Decision: "allow"}}
	b.approveErr = ErrNotSubmitted
	r, err := c.OperatorApprove(ctx, q)
	if err == nil {
		t.Fatal("unsupported allow accepted")
	}
	if r.ApprovalDelivery == nil || r.ApprovalDelivery.State != "not_submitted" {
		t.Fatalf("refusal falsely submitted: %#v", r)
	}
	b.approveErr = nil
	q.Approval.Decision = "deny"
	if _, err = c.OperatorApprove(ctx, q); err != nil || b.approves != 2 {
		t.Fatalf("denial lost after refused allowance: %v", err)
	}
}
func TestControllerUncertainBeginBlocksFreshStart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "receipt")
	l := &fakeLedger{beginErr: errors.New("receipt pipe failed after commit")}
	c, err := NewController(&fakeBackend{}, l, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Start(ctx, "codex", Launch{}, ""); err == nil {
		t.Fatal("expected uncertain begin")
	}
	c.Close()
	l.beginErr = nil
	c2, err := NewController(&fakeBackend{}, l, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if _, err = c2.Start(ctx, "codex", Launch{}, ""); err == nil {
		t.Fatal("fresh begin ignored unresolved registration intent")
	}
}

func TestControllerApprovalSerializationCannotReplay(t *testing.T) {
	for _, id := range []string{`"a<b&c>"`, `"line\u2028break"`, `"line\u2029break"`, `9007199254740993`, `"9007199254740993"`} {
		t.Run(id, func(t *testing.T) {
			c, b, l, path := controllerFixture(t)
			ctx := context.Background()
			a := Approval{ID: json.RawMessage(id), NativeSessionID: l.identity.SessionID, TurnID: "t1", Kind: "command"}
			if _, err := c.Observe(ctx, Event{Kind: "approval", NativeSessionID: l.identity.SessionID, Approval: &a}); err != nil {
				t.Fatal(err)
			}
			q := Request{Schema: Schema, Operation: "approve", Identity: l.identity, Approval: &ApprovalDecision{ID: a.ID, NativeSessionID: a.NativeSessionID, TurnID: a.TurnID, Decision: "allow"}}
			b.approveErr = errors.New("lost native response")
			if _, err := c.OperatorApprove(ctx, q); err == nil {
				t.Fatal("expected uncertain response")
			}
			c.Close()
			b2 := &fakeBackend{}
			c2, err := NewController(b2, l, path)
			if err != nil {
				t.Fatal(err)
			}
			defer c2.Close()
			if _, err = c2.Resume(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err = c2.Observe(ctx, Event{Kind: "approval", NativeSessionID: l.identity.SessionID, Approval: &a}); err != nil {
				t.Fatal(err)
			}
			if _, err = c2.OperatorApprove(ctx, q); err == nil || b2.approves != 0 {
				t.Fatal("serialized approval replayed")
			}
		})
	}
}

func TestControllerApprovalIDSpellingAndType(t *testing.T) {
	c, b, l, _ := controllerFixture(t)
	ctx := context.Background()
	cases := [][2]string{{`"a<b"`, `"a\u003cb"`}, {`9007199254740993`, `9007199254740993`}, {`"9007199254740993"`, `"9007199254740993"`}, {`9007199254740992`, `9007199254740992`}}
	for _, ids := range cases {
		a := Approval{ID: json.RawMessage(ids[0]), NativeSessionID: l.identity.SessionID, TurnID: "same-turn", Kind: "command"}
		if _, err := c.Observe(ctx, Event{Kind: "approval", NativeSessionID: l.identity.SessionID, Approval: &a}); err != nil {
			t.Fatal(err)
		}
		q := Request{Schema: Schema, Operation: "approve", Identity: l.identity, Approval: &ApprovalDecision{ID: json.RawMessage(ids[1]), NativeSessionID: a.NativeSessionID, TurnID: a.TurnID, Decision: "deny"}}
		if _, err := c.OperatorApprove(ctx, q); err != nil {
			t.Fatalf("%s -> %s: %v", ids[0], ids[1], err)
		}
	}
	if b.approves != len(cases) {
		t.Fatal("native id types or exact numeric values collapsed")
	}
}
