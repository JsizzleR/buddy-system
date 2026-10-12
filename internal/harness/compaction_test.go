package harness

// Compaction is a native history observation. It must survive the transport
// journal without becoming idle, approval resolution, delivery or completion.
import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestCodexCompactionKeepsExactItemIdentity(t *testing.T) {
	c, _, launch := codexFixture(t)
	id := codexStart(t, c, launch)
	if _, err := c.Send(context.Background(), id, "active work"); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"started", "completed"} {
		for _, bad := range []string{"foreign", "missing-turn", "missing-item", "other-item"} {
			p := map[string]any{"threadId": id, "turnId": "turn/compact", "item": map[string]string{"id": "item/α", "type": "contextCompaction"}}
			switch bad {
			case "foreign":
				p["threadId"] = "foreign"
			case "missing-turn":
				delete(p, "turnId")
			case "missing-item":
				p["item"] = map[string]string{"type": "contextCompaction"}
			case "other-item":
				p["item"] = map[string]string{"id": "item/α", "type": "agentMessage"}
			}
			raw, _ := json.Marshal(p)
			c.nativeEvent(codexEnvelope{Method: "item/" + phase, Params: raw})
			select {
			case e := <-c.Events():
				t.Fatalf("%s %s admitted: %#v", phase, bad, e)
			default:
			}
		}
		p, _ := json.Marshal(map[string]any{"threadId": id, "turnId": "turn/compact", "item": map[string]string{"id": "item/α", "type": "contextCompaction"}})
		c.nativeEvent(codexEnvelope{Method: "item/" + phase, Params: p})
		e := codexEvent(t, c)
		if e.Kind != "compaction_"+phase || e.NativeSessionID != id || e.TurnID != "turn/compact" || e.ItemID != "item/α" || e.ResultStatus != "" || e.ResultRef != "" {
			t.Fatalf("compaction identity changed or promoted: %#v", e)
		}
		c.mu.Lock()
		turn, state := c.activeTurn, c.state
		c.mu.Unlock()
		if turn != "turn/one" || state != "busy" {
			t.Fatalf("compaction cleared active work: %s %s", turn, state)
		}
	}
}

func TestControllerCompactionIsOnlyAnObservation(t *testing.T) {
	c, b, l, path := controllerFixture(t)
	ctx := context.Background()
	q := Request{Schema: Schema, Operation: "send", Identity: l.identity, MessageID: 7}
	if _, err := c.Handle(ctx, q); err != nil {
		t.Fatal(err)
	}
	a := Approval{ID: json.RawMessage(`"pending"`), NativeSessionID: l.identity.SessionID, TurnID: "work", Kind: "command"}
	before, err := c.Observe(ctx, Event{Kind: "approval", NativeSessionID: l.identity.SessionID, Approval: &a})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"compaction_started", "compaction_completed"} {
		e := Event{Kind: kind, NativeSessionID: l.identity.SessionID, TurnID: "compact", ItemID: "item-1"}
		r, err := c.Observe(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		if r.State != before.State || r.PendingApproval != before.PendingApproval || !reflect.DeepEqual(r.Approvals, before.Approvals) || r.Completion.State != "unknown" || r.BodyDelivery.State != "unknown" || r.Acknowledgment.State != "unknown" || len(l.events) != 0 {
			t.Fatalf("observation changed coordination: %#v; ledger %v", r, l.events)
		}
	}
	// Refusals come after the comparisons: a refused event of THIS worker is
	// a native event that was not recorded (F-11), so it rightly makes the
	// approval set uncertain. It keeps the known approval and never reads as
	// absent; a foreign thread is not this worker's event and changes nothing.
	wantApprovalState := "observed"
	for _, kind := range []string{"compaction_started", "compaction_completed"} {
		e := Event{Kind: kind, NativeSessionID: l.identity.SessionID, TurnID: "compact", ItemID: "item-1"}
		for _, field := range []string{"thread", "turn", "item"} {
			bad := e
			switch field {
			case "thread":
				bad.NativeSessionID = "other"
			case "turn":
				bad.TurnID = ""
			case "item":
				bad.ItemID = ""
			}
			r, err := c.Observe(ctx, bad)
			if err == nil {
				t.Fatalf("missing/wrong %s accepted", field)
			}
			if field != "thread" {
				wantApprovalState = "unknown"
			}
			if r.PendingApproval.State != wantApprovalState || len(r.Approvals) != 1 {
				t.Fatalf("refused %s lost the known approval or claimed absence: %#v", field, r.PendingApproval)
			}
		}
	}
	l.stale = true
	if _, err := c.Observe(ctx, Event{Kind: "compaction_completed", NativeSessionID: l.identity.SessionID, TurnID: "compact", ItemID: "item-1"}); err == nil {
		t.Fatal("old incarnation compaction accepted")
	}
	l.stale = false
	c.Close()
	b2 := &fakeBackend{}
	c2, err := NewController(b2, l, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if _, err := c2.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Handle(ctx, q); err != nil {
		t.Fatal(err)
	}
	if b.sends != 1 || b2.sends != 0 {
		t.Fatal("compaction forgot wake replay guard")
	}
	data, err := os.ReadFile(path + ".events")
	if err != nil {
		t.Fatal(err)
	}
	var found int
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var r Receipt
		if err := json.Unmarshal(line, &r); err != nil {
			t.Fatal(err)
		}
		if r.NativeEvent != nil && r.NativeEvent.ItemID == "item-1" {
			found++
			if r.Identity != l.identity || r.NativeEvent.TurnID != "compact" {
				t.Fatal("journal lost identity")
			}
		}
	}
	if found != 2 {
		t.Fatalf("journal retained %d compaction observations", found)
	}
}
