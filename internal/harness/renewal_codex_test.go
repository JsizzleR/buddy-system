package harness

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// The Codex review's residual question on the code pass: activeTurn is set by
// any turn/started, so a turn this launch never started (one replayed into a
// resumed thread) passed "turnId == activeTurn" and its completed fileChange
// renewed under the new generation. Only a turn whose turn/start THIS backend
// sent, read from that request's own reply on the reader goroutine, renews.
// The positive control is the same item in a turn this launch started.
func TestCodexRenewalOnlyInATurnThisLaunchStarted(t *testing.T) {
	item := map[string]any{"id": "call_1", "type": "fileChange", "status": "completed",
		"changes": []any{map[string]any{"path": "/w/a", "kind": map[string]any{"type": "update"}}}}
	for _, own := range []bool{false, true} {
		c, p, l := codexFixture(t)
		m := newRenewalMailbox()
		c.observeRenewals(m)
		id := codexStart(t, c, l)
		turn := "turn/replayed"
		if own {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			d, err := c.Send(ctx, id, "go")
			cancel()
			if err != nil || d.TurnID != "turn/one" {
				t.Fatalf("send: %+v %v", d, err)
			}
			turn = d.TurnID
		}
		p.notify("turn/started", map[string]any{"threadId": id, "turn": map[string]any{"id": turn, "status": "inProgress"}})
		p.notify("item/completed", map[string]any{"threadId": id, "turnId": turn, "item": item})
		p.notify("turn/completed", map[string]any{"threadId": id, "turn": map[string]any{"id": turn, "status": "completed"}})
		for e := codexEvent(t, c); e.Kind != "result"; e = codexEvent(t, c) {
		}
		batch, _, _, _ := m.take(c.generation, func(string) bool { return true }, renewBatchMax)
		if own != (len(batch) == 1) {
			t.Fatalf("own turn=%v renewed %q", own, batch)
		}
	}
}

// The measured 0.160.0 shape (2026-10-13 rerun, codex-path-move-control):
// item/completed, type fileChange, status completed, changes[].path absolute,
// a move's destination in kind.move_path. Every case runs in a live fixture
// turn and ends with that turn's completion, so by the time its result event
// is read the reader has classified the item.
func TestCodexRenewalOnlyForACompletedFileChangeInTheActiveTurn(t *testing.T) {
	change := func(path, kind string, move any) map[string]any {
		k := map[string]any{"type": kind}
		if move != nil {
			k["move_path"] = move
		}
		return map[string]any{"path": path, "kind": k, "diff": "@@"}
	}
	for _, tc := range []struct {
		name   string
		method string
		turn   string
		item   map[string]any
		renews []string
		lost   bool
	}{
		{"completed update renews", "item/completed", "turn/one", map[string]any{"type": "fileChange", "status": "completed", "changes": []any{change("/w/a", "update", nil)}}, []string{"/w/a"}, false},
		{"a move renews both ends", "item/completed", "turn/one", map[string]any{"type": "fileChange", "status": "completed", "changes": []any{change("/w/src", "update", "/w/dst")}}, []string{"/w/src", "/w/dst"}, false},
		{"a multi-file patch renews every file", "item/completed", "turn/one", map[string]any{"type": "fileChange", "status": "completed", "changes": []any{change("/w/a", "add", nil), change("/w/b", "delete", nil)}}, []string{"/w/a", "/w/b"}, false},
		{"failed renews nothing", "item/completed", "turn/one", map[string]any{"type": "fileChange", "status": "failed", "changes": []any{change("/w/a", "update", nil)}}, nil, false},
		{"declined renews nothing", "item/completed", "turn/one", map[string]any{"type": "fileChange", "status": "declined", "changes": []any{change("/w/a", "update", nil)}}, nil, false},
		{"inProgress renews nothing", "item/completed", "turn/one", map[string]any{"type": "fileChange", "status": "inProgress", "changes": []any{change("/w/a", "update", nil)}}, nil, false},
		{"a start renews nothing", "item/started", "turn/one", map[string]any{"type": "fileChange", "status": "completed", "changes": []any{change("/w/a", "update", nil)}}, nil, false},
		{"a successful command renews nothing", "item/completed", "turn/one", map[string]any{"type": "commandExecution", "status": "completed", "command": "sed -i s/a/b/ /w/a", "exitCode": 0}, nil, false},
		{"another turn's completion renews nothing", "item/completed", "turn/stale", map[string]any{"type": "fileChange", "status": "completed", "changes": []any{change("/w/a", "update", nil)}}, nil, false},
		{"an unknown status is lost coverage", "item/completed", "turn/one", map[string]any{"type": "fileChange", "status": "partial", "changes": []any{change("/w/a", "update", nil)}}, nil, true},
		{"an unknown kind renews none of the item", "item/completed", "turn/one", map[string]any{"type": "fileChange", "status": "completed", "changes": []any{change("/w/a", "update", nil), change("/w/b", "rename", nil)}}, nil, true},
		{"an unreadable destination renews none of the item", "item/completed", "turn/one", map[string]any{"type": "fileChange", "status": "completed", "changes": []any{change("/w/a", "update", 7)}}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, p, l := codexFixture(t)
			m := newRenewalMailbox()
			c.observeRenewals(m)
			id := codexStart(t, c, l)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			if d, err := c.Send(ctx, id, "go"); err != nil || d.TurnID != "turn/one" {
				t.Fatalf("send: %+v %v", d, err)
			}
			cancel()
			p.notify("turn/started", map[string]any{"threadId": id, "turn": map[string]any{"id": "turn/one", "status": "inProgress"}})
			item := map[string]any{"id": "call_1"}
			for k, v := range tc.item {
				item[k] = v
			}
			p.notify(tc.method, map[string]any{"threadId": id, "turnId": tc.turn, "item": item})
			p.notify("turn/completed", map[string]any{"threadId": id, "turn": map[string]any{"id": "turn/one", "status": "completed"}})
			for e := codexEvent(t, c); e.Kind != "result"; e = codexEvent(t, c) {
			}
			batch, gap, unreported, _ := m.take(c.generation, func(string) bool { return true }, renewBatchMax)
			if !reflect.DeepEqual(batch, tc.renews) {
				t.Fatalf("renewed %q, want %q", batch, tc.renews)
			}
			if unreported != tc.lost {
				t.Fatalf("coverage lost=%v (%q), want %v", unreported, gap, tc.lost)
			}
			if c.generation == "" {
				t.Fatal("the launch did not take its authorization's generation")
			}
		})
	}
}
