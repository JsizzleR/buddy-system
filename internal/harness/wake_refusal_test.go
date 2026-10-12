package harness

// F-10. A wake refused before anything reached native input was journalled as
// an uncertain delivery. It was then deduplicated forever (the message could
// never be woken) and Claude resume, which requires every journalled wake to
// be delivered, was refused permanently. Waking a busy worker is the ordinary
// case. A refusal proven to precede the write is now not_submitted; anything
// that returned from the write stays uncertain.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type wakeOutcomeBackend struct {
	fakeBackend
	outcome string // refuse | ambiguous | deliver
	onSend  func()
}

func (b *wakeOutcomeBackend) Send(context.Context, string, string) (Delivery, error) {
	b.sends++
	if b.onSend != nil {
		b.onSend()
	}
	switch b.outcome {
	case "refuse":
		return Delivery{State: "unknown"}, fmt.Errorf("%w: worker busy", ErrNotSubmitted)
	case "ambiguous":
		return Delivery{State: "unknown", TurnID: "partial"}, errors.New("native input write failed part-way; outcome unknown")
	}
	return Delivery{State: "delivered", TurnID: "turn-2"}, nil
}

func lastJournalRow(t *testing.T, path string) Receipt {
	t.Helper()
	data, err := os.ReadFile(path + ".events")
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimSpace(string(data)), "\n")
	var r Receipt
	if err := json.Unmarshal([]byte(rows[len(rows)-1]), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestControllerNotSubmittedWakeIsRetryable(t *testing.T) {
	for _, name := range []string{"refused-then-retried", "refused-reopened-retried", "ambiguous-stays-uncertain", "refusal-not-durable"} {
		t.Run(name, func(t *testing.T) {
			b := &wakeOutcomeBackend{outcome: "refuse"}
			if name == "ambiguous-stays-uncertain" {
				b.outcome = "ambiguous"
			}
			l := &fakeLedger{queued: true}
			path := filepath.Join(t.TempDir(), "receipt.json")
			c, err := NewController(b, l, path)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			ctx := context.Background()
			if _, err = c.Start(ctx, "codex", Launch{Worktree: t.TempDir()}, "test"); err != nil {
				t.Fatal(err)
			}
			if name == "refusal-not-durable" {
				// The journal fails exactly when the refusal must be recorded.
				b.onSend = func() { _ = c.journal.Close() }
			}
			q := Request{Schema: Schema, Operation: "send", Identity: l.identity, MessageID: 9}
			first, err := c.Handle(ctx, q)
			switch name {
			case "ambiguous-stays-uncertain":
				if err == nil || errors.Is(err, ErrNotSubmitted) || first.Delivery.State != "unknown" {
					t.Fatalf("a write that returned an error was not kept uncertain: %+v %v", first.Delivery, err)
				}
			case "refusal-not-durable":
				if err == nil || first.Delivery.State != "unknown" {
					t.Fatalf("an unrecorded refusal was reported as proven: %+v %v", first.Delivery, err)
				}
			default:
				if !errors.Is(err, ErrNotSubmitted) || first.Delivery.State != "not_submitted" {
					t.Fatalf("definite refusal not reported: %+v %v", first.Delivery, err)
				}
				if row := lastJournalRow(t, path); row.Delivery == nil || row.Delivery.State != "not_submitted" || row.MessageID != 9 {
					t.Fatalf("refusal not journalled: %+v", row)
				}
			}
			b.outcome, b.onSend = "deliver", nil
			if name == "refusal-not-durable" {
				// The broken journal refuses every further effect in this
				// process; nothing reaches the backend again.
				if _, err := c.Handle(ctx, Request{Schema: Schema, Operation: "send", Identity: l.identity, MessageID: 10}); err == nil || b.sends != 1 {
					t.Fatalf("effects continued after the refusal could not be recorded: %v sends=%d", err, b.sends)
				}
			}
			retry := c
			if name != "refused-then-retried" && name != "ambiguous-stays-uncertain" {
				c.Close()
				retryBackend := &wakeOutcomeBackend{outcome: "deliver"}
				retry, err = NewController(retryBackend, l, path)
				if err != nil {
					t.Fatal(err)
				}
				defer retry.Close()
				if _, err = retry.Resume(ctx); err != nil {
					t.Fatal(err)
				}
				b = retryBackend
			}
			second, err := retry.Handle(ctx, q)
			switch name {
			case "refused-then-retried", "refused-reopened-retried":
				if err != nil || second.Delivery.State != "delivered" || b.sends == 0 {
					t.Fatalf("a not-submitted wake was burned: %+v %v sends=%d", second.Delivery, err, b.sends)
				}
			default:
				// Positive controls for the boundary: an ambiguous write, and a
				// refusal whose record was lost, are never replayed.
				wantSends := 1
				if name == "refusal-not-durable" {
					wantSends = 0
				}
				if b.sends != wantSends || second.Delivery == nil || second.Delivery.State != "unknown" {
					t.Fatalf("uncertain wake replayed: %+v %v sends=%d", second.Delivery, err, b.sends)
				}
			}
		})
	}
}

// A not_submitted row releases only its own open intent. Anything else is a
// journal the controller cannot interpret, and it refuses to open it.
func TestControllerJournalNotSubmittedRequiresOpenIntent(t *testing.T) {
	for _, name := range []string{"intent-then-refusal", "refusal-without-intent", "refusal-after-delivery"} {
		t.Run(name, func(t *testing.T) {
			id := Identity{"buddy.lifecycle.v1", "codex", "native-1", "captured"}
			path := filepath.Join(t.TempDir(), "receipt.json")
			rows := []Receipt{{Schema: Schema, Identity: id, Operation: "start"}}
			wake := func(state, turn string) {
				rows = append(rows, Receipt{Schema: Schema, Identity: id, Operation: "send", MessageID: 4, Delivery: &Delivery{State: state, TurnID: turn}})
			}
			switch name {
			case "intent-then-refusal":
				wake("unknown", "")
			case "refusal-after-delivery":
				wake("unknown", "")
				wake("delivered", "turn-1")
			}
			wake("not_submitted", "")
			f, err := os.Create(path + ".events")
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				if err := json.NewEncoder(f).Encode(r); err != nil {
					t.Fatal(err)
				}
			}
			f.Close()
			c, err := NewController(&fakeBackend{}, &fakeLedger{identity: id}, path)
			if name == "intent-then-refusal" {
				if err != nil {
					t.Fatalf("positive control refused: %v", err)
				}
				defer c.Close()
				if _, held := c.sent[4]; held {
					t.Fatal("replayed refusal kept the wake reserved")
				}
				return
			}
			if err == nil {
				c.Close()
				t.Fatal("contradictory not_submitted row accepted")
			}
		})
	}
}

// Port of the review probe: a wake sent while the Claude worker is busy is
// refused before the pipe, retried once idle, and the session still resumes.
// The positive control has no busy wake.
func TestControllerClaudeBusyWakeRefusalRetriesAndResumes(t *testing.T) {
	for _, busyWake := range []bool{false, true} {
		name := "positive-control"
		if busyWake {
			name = "busy-wake"
		}
		t.Run(name, func(t *testing.T) {
			b, launch, ctx := claudeFixture(t)
			// Each interrupt result needs its own native reference; the shared
			// fake reuses one, which the backend dedupes.
			fake := strings.Replace(claudeFake, `'uuid':'native-interrupted-result'`, `'uuid':'native-interrupted-'+active['uuid']`, 1)
			if fake == claudeFake {
				t.Fatal("fixture mutation did not apply")
			}
			if err := os.WriteFile(b.(*claudeBackend).executable, []byte(fake), 0700); err != nil {
				t.Fatal(err)
			}
			l := &fakeLedger{queued: true}
			path := filepath.Join(t.TempDir(), "receipt.json")
			c, err := NewController(b, l, path)
			if err != nil {
				t.Fatal(err)
			}
			r, err := c.Start(ctx, "claude", launch, "w")
			if err != nil {
				t.Fatal(err)
			}
			id := r.Identity
			idle := make(chan string, 8)
			var observed sync.WaitGroup
			observed.Add(1)
			go func() {
				defer observed.Done()
				for e := range b.Events() {
					_, _ = c.Observe(context.Background(), e)
					if e.Kind == "idle" {
						idle <- e.TurnID
					}
				}
			}()
			finish := func(turn string) {
				t.Helper()
				if _, err := c.Handle(ctx, Request{Schema: Schema, Operation: "interrupt", Identity: id, TurnID: turn}); err != nil {
					t.Fatal(err)
				}
				select {
				case got := <-idle:
					if got != turn {
						t.Fatalf("idle for %s, want %s", got, turn)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("no idle after interrupt")
				}
			}
			r1, err := c.Handle(ctx, Request{Schema: Schema, Operation: "send", Identity: id, MessageID: 1})
			if err != nil || r1.Delivery == nil || r1.Delivery.State != "delivered" {
				t.Fatalf("wake 1: %+v %v", r1.Delivery, err)
			}
			if busyWake {
				r2, err := c.Handle(ctx, Request{Schema: Schema, Operation: "send", Identity: id, MessageID: 2})
				if !errors.Is(err, ErrNotSubmitted) || r2.Delivery == nil || r2.Delivery.State != "not_submitted" {
					t.Fatalf("busy wake was not a definite refusal: %+v %v", r2.Delivery, err)
				}
			}
			finish(r1.Delivery.TurnID)
			if busyWake {
				r2, err := c.Handle(ctx, Request{Schema: Schema, Operation: "send", Identity: id, MessageID: 2})
				if err != nil || r2.Delivery == nil || r2.Delivery.State != "delivered" {
					t.Fatalf("message #2 could not be woken once idle: %+v %v", r2.Delivery, err)
				}
				finish(r2.Delivery.TurnID)
			}
			c.Close()
			observed.Wait()
			rc := &resumeCounter{}
			c2, err := NewController(rc, l, path)
			if err != nil {
				t.Fatal(err)
			}
			defer c2.Close()
			if _, err = c2.Resume(context.Background()); err != nil || rc.resumes != 1 {
				t.Fatalf("resume refused after a wake that never touched the pipe: %v (resumes %d)", err, rc.resumes)
			}
		})
	}
}

// Backend boundary: refusals made before the request reaches WriteContext are
// ErrNotSubmitted and write nothing; a failed write is not.
func TestClaudeWakeRefusalsBeforeTransportAreNotSubmitted(t *testing.T) {
	for _, name := range []string{"busy", "interrupt-uncertain", "closed", "foreign-session", "cancelled", "write-failed"} {
		t.Run(name, func(t *testing.T) {
			c := newClaude("unused")
			c.nativeID = "native"
			writes := 0
			c.proc = &claudeFakeProcess{write: func(context.Context, any) error {
				writes++
				if name == "write-failed" {
					return errors.New("broken pipe after a partial frame")
				}
				return nil
			}}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			target := "native"
			switch name {
			case "busy":
				c.active = "earlier-input"
			case "interrupt-uncertain":
				c.interruptUncertain = true
			case "closed":
				c.closed = true
			case "foreign-session":
				target = "another"
			case "cancelled":
				cancel()
			}
			_, err := c.Send(ctx, target, "peer hint")
			if name == "write-failed" {
				if err == nil || errors.Is(err, ErrNotSubmitted) || writes != 1 {
					t.Fatalf("a failed write was classified as not submitted: %v writes=%d", err, writes)
				}
				return
			}
			if !errors.Is(err, ErrNotSubmitted) || writes != 0 {
				t.Fatalf("%s: err=%v writes=%d", name, err, writes)
			}
		})
	}
}

func TestCodexWakeRefusalsBeforeTransportAreNotSubmitted(t *testing.T) {
	for _, name := range []string{"unknown-active-turn", "oversized-text", "foreign-thread", "cancelled", "write-failed"} {
		t.Run(name, func(t *testing.T) {
			c, p, launch := codexFixture(t)
			id := codexStart(t, c, launch)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			text, target := "peer hint", id
			switch name {
			case "unknown-active-turn":
				c.mu.Lock()
				c.state, c.activeTurn = "busy", ""
				c.mu.Unlock()
			case "oversized-text":
				text = strings.Repeat("x", 4097)
			case "foreign-thread":
				target = "another"
			case "cancelled":
				cancel()
			case "write-failed":
				p.mu.Lock()
				p.writeErr = errors.New("broken pipe after a partial frame")
				p.mu.Unlock()
			}
			before := len(p.allWrites())
			_, err := c.Send(ctx, target, text)
			if name == "write-failed" {
				if err == nil || errors.Is(err, ErrNotSubmitted) {
					t.Fatalf("a failed write was classified as not submitted: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrNotSubmitted) || len(p.allWrites()) != before {
				t.Fatalf("%s: err=%v wrote=%d", name, err, len(p.allWrites())-before)
			}
		})
	}
}

// The same boundary for an operator approval. Every refusal Approve makes
// before the pipe is not_submitted and writes nothing, so the operator may
// answer again; an approval refused as uncertain could never be answered. The
// verification's mutants turning each of these six refusals into an ordinary
// error survived every permanent test. The two decisions that are written are
// the positive controls: the refusals are about the decision, not a fixture
// that cannot write.
func TestClaudeApprovalRefusalsBeforeTransportAreNotSubmitted(t *testing.T) {
	for _, name := range []string{"cancelled", "malformed-id", "cancelled-under-lock", "unknown-request", "foreign-session", "other-turn", "closed", "invalid-decision", "interactive-allow", "interactive-deny", "allow"} {
		t.Run(name, func(t *testing.T) {
			c := newClaude("unused")
			c.nativeID, c.active = "native", "turn"
			writes := 0
			c.proc = &claudeFakeProcess{write: func(context.Context, any) error {
				writes++
				return nil
			}}
			if err := claudePending(t, c, "request", "Write", strings.HasPrefix(name, "interactive")); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			d := ApprovalDecision{ID: json.RawMessage(`"request"`), NativeSessionID: "native", TurnID: "turn", Decision: "allow"}
			switch name {
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "malformed-id":
				d.ID = json.RawMessage(`1`)
			case "cancelled-under-lock":
				ctx = &cancelledOnSecondCheck{Context: ctx}
			case "unknown-request":
				d.ID = json.RawMessage(`"absent"`)
			case "foreign-session":
				d.NativeSessionID = "another"
			case "other-turn":
				d.TurnID = "earlier-turn"
			case "closed":
				c.closed = true
			case "invalid-decision":
				d.Decision = "maybe"
			case "interactive-deny":
				d.Decision = "deny"
			}
			err := c.Approve(ctx, d)
			if name == "allow" || name == "interactive-deny" {
				if err != nil || writes != 1 {
					t.Fatalf("%s: err=%v writes=%d", name, err, writes)
				}
				return
			}
			if !errors.Is(err, ErrNotSubmitted) || writes != 0 {
				t.Fatalf("%s: err=%v writes=%d", name, err, writes)
			}
		})
	}
}

// cancelledOnSecondCheck is live when Approve first checks it and cancelled
// when Approve checks again, under the backend lock, after decoding the ID.
type cancelledOnSecondCheck struct {
	context.Context
	checks int
}

func (c *cancelledOnSecondCheck) Err() error {
	c.checks++
	if c.checks > 1 {
		return context.Canceled
	}
	return nil
}

// Codex's remaining refusals before the transport. A reconciliation read that
// names another thread or no recorded turn refuses before anything is
// written, and so does call itself once the transport is gone. That last
// check fires when the worker exits between a caller's identity check and its
// write, which no test can schedule, so call is driven directly. Each of the
// three was deletable without a permanent test failing (verification audit).
func TestCodexRefusalsBeforeTransportAreNotSubmitted(t *testing.T) {
	for _, name := range []string{"read-foreign-thread", "read-without-turn", "call-without-transport", "call-live"} {
		t.Run(name, func(t *testing.T) {
			c, p, launch := codexFixture(t)
			id := codexStart(t, c, launch)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			read := map[string]any{"threadId": id, "includeTurns": false}
			before := len(p.allWrites())
			var err error
			switch name {
			case "read-foreign-thread":
				_, err = c.ReadTurn(ctx, "another", "turn/one")
			case "read-without-turn":
				_, err = c.ReadTurn(ctx, id, "")
			case "call-without-transport":
				c.mu.Lock()
				proc := c.process
				c.process = nil
				c.mu.Unlock()
				_, err = c.call(ctx, "thread/read", read)
				c.mu.Lock()
				c.process = proc
				c.mu.Unlock()
			case "call-live":
				// Positive control: with the transport present the same
				// call is written and answered.
				if _, err = c.call(ctx, "thread/read", read); err != nil || len(p.allWrites()) != before+1 {
					t.Fatalf("live call: err=%v wrote=%d", err, len(p.allWrites())-before)
				}
				return
			}
			if !errors.Is(err, ErrNotSubmitted) || len(p.allWrites()) != before {
				t.Fatalf("%s: err=%v wrote=%d", name, err, len(p.allWrites())-before)
			}
		})
	}
}
