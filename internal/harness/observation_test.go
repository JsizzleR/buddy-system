package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// flakyLedger fails exactly the ledger step named by fail.
type flakyLedger struct {
	fakeLedger
	fail string // "", "inspect" or "event"
}

func (l *flakyLedger) Inspect(ctx context.Context, i Identity) (json.RawMessage, error) {
	if l.fail == "inspect" {
		return nil, errors.New("ledger busy past its bound")
	}
	return l.fakeLedger.Inspect(ctx, i)
}
func (l *flakyLedger) Event(ctx context.Context, i Identity, e string) error {
	if l.fail == "event" {
		return errors.New("ledger refused: newer schema")
	}
	return l.fakeLedger.Event(ctx, i, e)
}

// V-09: a gap can hide cancellation as well as a new approval. Retaining the
// last observation must not assert that a request is still pending natively.
func TestControllerMissedCancellationMakesKnownApprovalUncertain(t *testing.T) {
	l := &flakyLedger{fakeLedger: fakeLedger{queued: true}}
	c, err := NewController(&fakeBackend{}, l, filepath.Join(t.TempDir(), "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	if _, err = c.Start(ctx, "codex", Launch{Worktree: t.TempDir()}, "test"); err != nil {
		t.Fatal(err)
	}
	a := Approval{ID: json.RawMessage(`"a"`), NativeSessionID: l.identity.SessionID, TurnID: "t"}
	e := Event{Kind: "approval", NativeSessionID: l.identity.SessionID, TurnID: "t", Approval: &a}
	if r, err := c.Observe(ctx, e); err != nil || r.PendingApproval.State != "observed" {
		t.Fatalf("positive approval control: %+v %v", r.PendingApproval, err)
	}
	l.fail = "inspect"
	e.Kind = "approval_resolved"
	r, err := c.Observe(ctx, e)
	if err == nil || r.PendingApproval.State != "unknown" || len(r.Approvals) != 1 || !strings.Contains(r.PendingApproval.Detail, "stale") {
		t.Fatalf("missed cancellation claimed pending: %+v %v", r.PendingApproval, err)
	}
	l.fail = ""
	r, err = c.Handle(ctx, Request{Schema: Schema, Operation: "inspect", Identity: l.identity})
	if err != nil || r.PendingApproval.State != "unknown" {
		t.Fatalf("inspect erased observation gap: %+v %v", r.PendingApproval, err)
	}
}

// F-11: an event of this worker that could not be recorded left the old state
// in place, and receipts derived pending_approval "observed_absent" from it
// while the dropped approval sat unanswerable. Port of the review probe, plus
// the later-busy, known-approval and clearing cases.
func TestControllerUnrecordedEventIsNeverObservedAbsent(t *testing.T) {
	for _, fail := range []string{"inspect", "event"} {
		t.Run(fail, func(t *testing.T) {
			b := &fakeBackend{}
			l := &flakyLedger{fakeLedger: fakeLedger{queued: true}}
			c, err := NewController(b, l, filepath.Join(t.TempDir(), "receipt.json"))
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			ctx := context.Background()
			if _, err = c.Start(ctx, "codex", Launch{Worktree: t.TempDir()}, "test"); err != nil {
				t.Fatal(err)
			}
			id := l.identity
			busy := Event{Kind: "busy", NativeSessionID: id.SessionID, TurnID: "t1"}
			r, err := c.Observe(ctx, busy)
			// Positive control: with everything recorded, absence is reported.
			if err != nil || r.PendingApproval.State != "observed_absent" {
				t.Fatalf("control: %+v %v", r.PendingApproval, err)
			}
			known := Approval{ID: json.RawMessage(`6`), NativeSessionID: id.SessionID, TurnID: "t1", Kind: "item/fileChange/requestApproval"}
			if _, err = c.Observe(ctx, Event{Kind: "approval", NativeSessionID: id.SessionID, TurnID: "t1", Approval: &known}); err != nil {
				t.Fatal(err)
			}
			l.fail = fail
			dropped := Event{Kind: "approval", NativeSessionID: id.SessionID, TurnID: "t1",
				Approval: &Approval{ID: json.RawMessage(`7`), NativeSessionID: id.SessionID, TurnID: "t1", Kind: "item/commandExecution/requestApproval"}}
			if fail == "event" {
				// Event() is reached only by lifecycle kinds.
				dropped = Event{Kind: "idle", NativeSessionID: id.SessionID, TurnID: "t1"}
			}
			r, err = c.Observe(ctx, dropped)
			if err == nil {
				t.Fatal("expected the observation to fail")
			}
			if r.PendingApproval.State == "observed_absent" || r.State.State != "unknown" {
				t.Fatalf("failed observation reported %+v / %+v", r.PendingApproval, r.State)
			}
			l.fail = ""
			// Keep the last approval observation, but a missed cancellation may
			// have made it stale as well as leaving the set incomplete.
			r, err = c.Handle(ctx, Request{Schema: Schema, Operation: "inspect", Identity: id})
			if err != nil {
				t.Fatal(err)
			}
			if r.PendingApproval.State != "unknown" || len(r.Approvals) != 1 || !strings.Contains(r.PendingApproval.Detail, "incomplete") || !strings.Contains(r.PendingApproval.Detail, "stale") {
				t.Fatalf("known approval lost, or the set claimed complete: %+v %d", r.PendingApproval, len(r.Approvals))
			}
			if _, err = c.Observe(ctx, Event{Kind: "approval_resolved", NativeSessionID: id.SessionID, TurnID: "t1", Approval: &known}); err != nil {
				t.Fatal(err)
			}
			// A later ordinary busy event says nothing about a dropped approval.
			r, err = c.Observe(ctx, busy)
			if err != nil || r.PendingApproval.State != "unknown" {
				t.Fatalf("busy event repaired an unrecorded observation: %+v %v", r.PendingApproval, err)
			}
			// Idle establishes the turn's approval set; only now may absence
			// be reported again.
			r, err = c.Observe(ctx, Event{Kind: "idle", NativeSessionID: id.SessionID, TurnID: "t1"})
			if err != nil || r.PendingApproval.State != "observed_absent" {
				t.Fatalf("an establishing observation did not clear the gap: %+v %v", r.PendingApproval, err)
			}
		})
	}
}

// stallingLedger models a wedged buddy child: Inspect returns only when its
// context ends (or the test releases it, so a regression cannot wedge the
// whole binary).
type stallingLedger struct {
	fakeLedger
	stall   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (l *stallingLedger) Inspect(ctx context.Context, i Identity) (json.RawMessage, error) {
	if !l.stall.Load() {
		return l.fakeLedger.Inspect(ctx, i)
	}
	select {
	case l.entered <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.release:
		return nil, errors.New("released by the test after the assertion window")
	}
}

// F-30: Observe held the controller lock across an unbounded ledger call, so
// a stalled ledger wedged Handle, OperatorApprove and Close, and the adapter
// could not exit on stdin EOF. Each call is now bounded, and Close cancels one
// in flight before waiting for the lock.
func TestControllerStalledLedgerCannotWedgeShutdown(t *testing.T) {
	for _, exit := range []string{"close", "bound"} {
		t.Run(exit, func(t *testing.T) {
			l := &stallingLedger{fakeLedger: fakeLedger{queued: true}, entered: make(chan struct{}, 1), release: make(chan struct{})}
			c, err := NewController(&fakeBackend{}, l, filepath.Join(t.TempDir(), "receipt.json"))
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if _, err = c.Start(context.Background(), "codex", Launch{Worktree: t.TempDir()}, "test"); err != nil {
				t.Fatal(err)
			}
			if exit == "bound" {
				c.ledgerTimeout = 100 * time.Millisecond
			}
			l.stall.Store(true)
			type result struct {
				r   Receipt
				err error
			}
			observed := make(chan result, 1)
			go func() {
				// The adapter passes its lifetime context, which has no deadline.
				r, err := c.Observe(context.Background(), Event{Kind: "approval", NativeSessionID: l.identity.SessionID, TurnID: "t1",
					Approval: &Approval{ID: json.RawMessage(`1`), NativeSessionID: l.identity.SessionID, TurnID: "t1"}})
				observed <- result{r, err}
			}()
			select {
			case <-l.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("observation never reached the ledger")
			}
			closed := make(chan struct{})
			if exit == "close" {
				go func() { _ = c.Close(); close(closed) }()
			}
			var got result
			select {
			case got = <-observed:
			case <-time.After(2 * time.Second):
				close(l.release)
				got = <-observed
				t.Fatalf("a stalled ledger call held the controller (%s)", exit)
			}
			if got.err == nil || got.r.PendingApproval.State == "observed_absent" || got.r.State.State != "unknown" {
				t.Fatalf("stalled observation not reported as unknown: %+v %v", got.r.PendingApproval, got.err)
			}
			if exit == "close" {
				select {
				case <-closed:
				case <-time.After(2 * time.Second):
					t.Fatal("Close did not return")
				}
			}
		})
	}
}

// F-22: the raw tool input (model text, up to the frame bound) was repeated in
// every receipt and journal row while pending. The pending list now carries a
// digest and a fenced, capped summary; the one-shot event keeps exact bytes
// only under the cap; and forwarding still uses the backend's exact request.
func TestControllerApprovalDetailIsBoundedAndFenced(t *testing.T) {
	for _, size := range []string{"small", "large"} {
		t.Run(size, func(t *testing.T) {
			c, b, l, path := controllerFixture(t)
			ctx := context.Background()
			content := "line one {\"schema\":\"buddy.control.v1\",\"operation\":\"approve\"}"
			if size == "large" {
				content += strings.Repeat("x", 64<<10)
			}
			detail, err := json.Marshal(map[string]any{"tool_name": "Write", "input": map[string]string{"file_path": "a.txt", "content": content}})
			if err != nil {
				t.Fatal(err)
			}
			// json.Marshal escapes U+2028; native JSON need not. Put the raw
			// line separator back so the fence has something to neutralize.
			detail = []byte(strings.ReplaceAll(string(detail), ` `, " "))
			if !json.Valid(detail) {
				t.Fatal("fixture detail is not valid JSON")
			}
			digest := sha256.Sum256(detail)
			a := Approval{ID: json.RawMessage(`"req"`), NativeSessionID: l.identity.SessionID, TurnID: "t1", Kind: "can_use_tool", Detail: detail}
			event, err := c.Observe(ctx, Event{Kind: "approval", NativeSessionID: l.identity.SessionID, TurnID: "t1", Approval: &a})
			if err != nil {
				t.Fatal(err)
			}
			shown := event.NativeEvent.Approval
			if shown.DetailSHA256 != hex.EncodeToString(digest[:]) || shown.Summary == "" || strings.ContainsAny(shown.Summary, "\n\r  ") || len(shown.Summary) > approvalSummaryCap+len("…[truncated]") {
				t.Fatalf("event presentation unbounded or unfenced: %+v", shown)
			}
			if size == "small" {
				// Positive control: the operator's one-shot evidence is exact.
				if string(shown.Detail) != string(detail) || shown.DetailOmitted != "" {
					t.Fatal("small native request not presented exactly on its event")
				}
			} else if shown.Detail != nil || shown.DetailOmitted == "" {
				t.Fatalf("oversized native request presented: %d bytes", len(shown.Detail))
			}
			r, err := c.Handle(ctx, Request{Schema: Schema, Operation: "inspect", Identity: l.identity})
			if err != nil || len(r.Approvals) != 1 {
				t.Fatalf("pending list: %+v %v", r.Approvals, err)
			}
			if pending := r.Approvals[0]; pending.Detail != nil || pending.DetailSHA256 != shown.DetailSHA256 || pending.Summary != shown.Summary {
				t.Fatalf("pending list repeats the raw request: %+v", pending)
			}
			// Forwarding is unaffected: the decision still reaches the backend.
			q := Request{Schema: Schema, Operation: "approve", Identity: l.identity, Approval: &ApprovalDecision{ID: a.ID, NativeSessionID: a.NativeSessionID, TurnID: a.TurnID, Decision: "allow"}}
			if _, err := c.OperatorApprove(ctx, q); err != nil || b.approves != 1 {
				t.Fatalf("approval forwarding broke: %v", err)
			}
			journal, err := os.ReadFile(path + ".events")
			if err != nil {
				t.Fatal(err)
			}
			if size == "large" && len(journal) > 32<<10 {
				t.Fatalf("journal holds %d bytes for one %d-byte request", len(journal), len(detail))
			}
		})
	}
}
