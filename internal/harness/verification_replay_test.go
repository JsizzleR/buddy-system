package harness

// Verification V-02: not_submitted is evidence that no native turn or result
// exists. A contradictory refusal must not erase the journal's reservation,
// even when the preceding intent was otherwise well formed.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestControllerJournalRefusalRejectsNativeEvidence(t *testing.T) {
	for _, test := range []struct {
		name    string
		prior   Delivery
		refusal *Delivery
		refuse  bool
	}{
		{"legacy intent", Delivery{State: "unknown"}, nil, false},
		{"legacy accepted", Delivery{State: "accepted", TurnID: "turn-1"}, nil, false},
		{"legacy delivered", Delivery{State: "delivered", TurnID: "turn-1", ResultRef: "result-1"}, nil, false},
		{"valid refusal", Delivery{State: "unknown"}, &Delivery{State: "not_submitted"}, false},
		{"prior turn", Delivery{State: "unknown", TurnID: "turn-1"}, &Delivery{State: "not_submitted"}, true},
		{"prior result", Delivery{State: "unknown", ResultRef: "result-1"}, &Delivery{State: "not_submitted"}, true},
		{"refusal turn", Delivery{State: "unknown"}, &Delivery{State: "not_submitted", TurnID: "turn-1"}, true},
		{"refusal result", Delivery{State: "unknown"}, &Delivery{State: "not_submitted", ResultRef: "result-1"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			id := Identity{"buddy.lifecycle.v1", "codex", "native-1", "captured"}
			path := filepath.Join(t.TempDir(), "receipt.json")
			rows := []Receipt{{Schema: Schema, Identity: id, Operation: "start"},
				{Schema: Schema, Identity: id, Operation: "send", MessageID: 42, Delivery: &test.prior}}
			if test.refusal != nil {
				rows = append(rows, Receipt{Schema: Schema, Identity: id, Operation: "send", MessageID: 42, Delivery: test.refusal})
			}
			f, err := os.Create(path + ".events")
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if err := json.NewEncoder(f).Encode(row); err != nil {
					f.Close()
					t.Fatal(err)
				}
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			c, err := NewController(&fakeBackend{}, &fakeLedger{identity: id}, path)
			if err != nil {
				if !test.refuse {
					t.Fatalf("compatible journal refused: %v", err)
				}
				return
			}
			defer c.Close()
			if test.refuse {
				t.Fatal("contradictory refusal discarded the native wake reservation")
			}
			got, held := c.sent[42]
			if test.refusal == nil && (!held || got != test.prior) {
				t.Fatalf("legacy reservation changed: %+v held=%v", got, held)
			}
			if test.refusal != nil && held {
				t.Fatal("valid refusal retained the reservation")
			}
		})
	}
}

type contradictoryRefusalBackend struct {
	fakeBackend
	delivery Delivery
}

func (b *contradictoryRefusalBackend) Send(context.Context, string, string) (Delivery, error) {
	b.sends++
	return b.delivery, ErrNotSubmitted
}

func TestControllerLiveRefusalWithNativeEvidenceStaysReserved(t *testing.T) {
	for _, delivery := range []Delivery{{State: "unknown", TurnID: "turn-1"}, {State: "unknown", ResultRef: "result-1"}} {
		t.Run(delivery.TurnID+delivery.ResultRef, func(t *testing.T) {
			c, _, l, _ := controllerFixture(t)
			b := &contradictoryRefusalBackend{delivery: delivery}
			c.backend = b
			q := Request{Schema: Schema, Operation: "send", Identity: l.identity, MessageID: 42}
			r, err := c.Handle(context.Background(), q)
			if err == nil || r.Delivery == nil || *r.Delivery != delivery {
				t.Fatalf("contradictory refusal became a safe retry: %+v %v", r.Delivery, err)
			}
			if _, err := c.Handle(context.Background(), q); err != nil || b.sends != 1 {
				t.Fatalf("contradictory refusal replayed: sends=%d err=%v", b.sends, err)
			}
		})
	}
}
