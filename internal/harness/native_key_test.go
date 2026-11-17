package harness

import (
	"context"
	"encoding/json"
	"testing"
)

// A Codex approval request id is keyed by its canonical int64 spelling only
// (D-202). `-0` and `0` used to be two backend keys (raw spelling) but one
// controller key (parsed value): the receipt showed one approval, the other
// stayed natively pending, and an answer by either id approved whichever the
// backend matched. Each non-canonical spelling is refused (no approval
// published, no answer accepted); the canonical one is the positive control.
func TestCodexRequestIDCanonicalInteger(t *testing.T) {
	for _, tc := range []struct {
		id       string
		accepted bool
	}{
		{"0", true},
		{"1", true},
		{"-1", true},
		{"9007199254740993", true},
		{"-0", false},
		{"0.0", false},
		{"1e0", false},
		{"1.0", false},
		{"10E-1", false},
	} {
		t.Run(tc.id, func(t *testing.T) {
			key, err := codexRequestKey(json.RawMessage(tc.id))
			if (err == nil) != tc.accepted {
				t.Fatalf("codexRequestKey(%s) = %q, %v", tc.id, key, err)
			}
			c, p, l := codexFixture(t)
			id := codexStart(t, c, l)
			p.frame(map[string]any{"id": json.RawMessage(tc.id), "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": id, "turnId": "turn/one", "itemId": "item/one"}})
			// turn/started is a stream-progress barrier: an approval the
			// backend took arrives before it.
			p.notify("turn/started", map[string]any{"threadId": id, "turn": map[string]string{"id": "turn/one"}})
			e := codexEvent(t, c)
			if tc.accepted {
				if e.Kind != "approval" || e.Approval == nil || string(e.Approval.ID) != tc.id {
					t.Fatalf("canonical id not published: %#v", e)
				}
				if err := c.Approve(context.Background(), ApprovalDecision{ID: json.RawMessage(tc.id), NativeSessionID: id, TurnID: "turn/one", Decision: "deny"}); err != nil {
					t.Fatalf("canonical id not answerable: %v", err)
				}
				return
			}
			if e.Kind != "busy" {
				t.Fatalf("non-canonical id published: %#v", e)
			}
			if err := c.Approve(context.Background(), ApprovalDecision{ID: json.RawMessage(tc.id), NativeSessionID: id, TurnID: "turn/one", Decision: "allow"}); err == nil {
				t.Fatal("non-canonical id answered")
			}
		})
	}
}

// The alias itself: 0 pending, then -0 for different work. Only 0 is listed,
// and an answer spelled 0 answers 0, the request the receipt showed.
func TestCodexNegativeZeroCannotShadowZero(t *testing.T) {
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	p.frame(map[string]any{"id": 0, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": id, "turnId": "turn/one", "itemId": "item/one", "command": "ls"}})
	if e := codexEvent(t, c); e.Kind != "approval" || string(e.Approval.ID) != "0" {
		t.Fatalf("positive control: %#v", e)
	}
	p.frame(map[string]any{"id": json.RawMessage("-0"), "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": id, "turnId": "turn/one", "itemId": "item/two", "command": "rm -rf x"}})
	p.notify("turn/started", map[string]any{"threadId": id, "turn": map[string]string{"id": "turn/one"}})
	if e := codexEvent(t, c); e.Kind != "busy" {
		t.Fatalf("-0 published beside 0: %#v", e)
	}
	c.mu.Lock()
	n := len(c.approvals)
	c.mu.Unlock()
	if n != 1 {
		t.Fatalf("backend holds %d pending approvals, want 1", n)
	}
}
