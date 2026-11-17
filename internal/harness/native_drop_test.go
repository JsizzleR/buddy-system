package harness

// adapter-drops: native events the adapter used to drop whole (D-202's Open).
// Each malformed case runs beside its plain twin, the positive control that
// the path was reached and the verdict is the malformation's, not the
// fixture's.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// codexBarrier proves nothing was published: a barrier event the adapter
// always emits must be the next one.
func codexBarrier(t *testing.T, c *codexBackend, p *fakeCodexProcess, id, turn string) {
	t.Helper()
	p.notify("turn/started", map[string]any{"threadId": id, "turn": map[string]string{"id": turn}})
	if e := codexEvent(t, c); e.Kind != "busy" || e.TurnID != turn {
		t.Fatalf("published before the barrier: %#v", e)
	}
}

// A field a state-moving method needs, unreadable, ENDS the transport, naming
// the method, the field and its kind, never the value. Each was a whole-frame
// drop: turn/completed's left the adapter busy for good.
func TestCodexUnreadableStateFieldEndsTransport(t *testing.T) {
	const hostile = "evil‮value"
	completed := func(turn any) map[string]any {
		return map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "", "turn": turn}}
	}
	for _, tc := range []struct {
		name  string
		frame func(id string) map[string]any // malformed
		plain func(id string) map[string]any // its twin
		kind  string                         // the twin's event
		want  string
	}{
		{"completed-turn-id-number",
			func(id string) map[string]any { return completed(map[string]any{"id": 7, "status": "completed"}) },
			func(id string) map[string]any {
				return completed(map[string]any{"id": "turn/one", "status": "completed"})
			},
			"result", "Codex turn/completed turn.id is a JSON number"},
		{"completed-turn-a-string",
			func(id string) map[string]any { return completed(hostile) },
			func(id string) map[string]any {
				return completed(map[string]any{"id": "turn/one", "status": "completed"})
			},
			"result", "turn.id is absent"},
		{"completed-status-absent",
			func(id string) map[string]any { return completed(map[string]any{"id": "turn/one"}) },
			func(id string) map[string]any {
				return completed(map[string]any{"id": "turn/one", "status": "interrupted"})
			},
			"result", "unrecognised status absent"},
		{"completed-status-number",
			func(id string) map[string]any { return completed(map[string]any{"id": "turn/one", "status": 7}) },
			func(id string) map[string]any { return completed(map[string]any{"id": "turn/one", "status": "failed"}) },
			"result", "unrecognised status a JSON number"},
		{"completed-status-unknown",
			func(id string) map[string]any {
				return completed(map[string]any{"id": "turn/one", "status": "cancelled"})
			},
			func(id string) map[string]any {
				return completed(map[string]any{"id": "turn/one", "status": "completed"})
			},
			"result", "unrecognised status cancelled"},
		{"completed-status-in-progress",
			func(id string) map[string]any {
				return completed(map[string]any{"id": "turn/one", "status": "inProgress"})
			},
			func(id string) map[string]any {
				return completed(map[string]any{"id": "turn/one", "status": "completed"})
			},
			"result", "unrecognised status inProgress"},
		{"completed-status-hostile",
			func(id string) map[string]any { return completed(map[string]any{"id": "turn/one", "status": hostile}) },
			func(id string) map[string]any {
				return completed(map[string]any{"id": "turn/one", "status": "completed"})
			},
			"result", "unrecognised status evil"},
		{"completed-params-array",
			func(id string) map[string]any { return map[string]any{"method": "turn/completed", "params": []any{1}} },
			func(id string) map[string]any {
				return completed(map[string]any{"id": "turn/one", "status": "completed"})
			},
			"result", "Codex turn/completed params is a JSON array"},
		{"completed-params-null",
			func(id string) map[string]any { return map[string]any{"method": "turn/completed", "params": nil} },
			func(id string) map[string]any {
				return completed(map[string]any{"id": "turn/one", "status": "completed"})
			},
			"result", "params is null"},
		{"completed-thread-number",
			func(id string) map[string]any {
				return map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": 7, "turn": map[string]any{"id": "turn/one", "status": "completed"}}}
			},
			func(id string) map[string]any {
				return completed(map[string]any{"id": "turn/one", "status": "completed"})
			},
			"result", "threadId is a JSON number"},
		{"completed-thread-absent",
			func(id string) map[string]any {
				return map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]any{"id": "turn/one", "status": "completed"}}}
			},
			func(id string) map[string]any {
				return completed(map[string]any{"id": "turn/one", "status": "completed"})
			},
			"result", "threadId is absent"},
		{"started-turn-id-object",
			func(id string) map[string]any {
				return map[string]any{"method": "turn/started", "params": map[string]any{"threadId": id, "turn": map[string]any{"id": map[string]int{"n": 1}}}}
			},
			func(id string) map[string]any {
				return map[string]any{"method": "turn/started", "params": map[string]any{"threadId": id, "turn": map[string]any{"id": "turn/two"}}}
			},
			"busy", "Codex turn/started turn.id is a JSON object"},
		{"approval-turn-id-number",
			func(id string) map[string]any {
				return map[string]any{"id": 3, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": id, "turnId": 7, "itemId": "i"}}
			},
			func(id string) map[string]any {
				return map[string]any{"id": 3, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": id, "turnId": "turn/one", "itemId": "i"}}
			},
			"approval", "Codex item/commandExecution/requestApproval turnId is a JSON number"},
		{"archived-thread-bool",
			func(id string) map[string]any {
				return map[string]any{"method": "thread/archived", "params": map[string]any{"threadId": true}}
			},
			func(id string) map[string]any {
				return map[string]any{"method": "thread/archived", "params": map[string]any{"threadId": id}}
			},
			"ended", "Codex thread/archived threadId is a JSON bool"},
	} {
		for _, malformed := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/plain", true: "/malformed"}[malformed], func(t *testing.T) {
				c, p, l := codexFixture(t)
				id := codexStart(t, c, l)
				codexBarrier(t, c, p, id, "turn/one")
				make := tc.plain
				if malformed {
					make = tc.frame
				}
				f := make(id)
				if params, ok := f["params"].(map[string]any); ok {
					if v, ok := params["threadId"].(string); ok && v == "" {
						params["threadId"] = id
					}
				}
				p.frame(f)
				e := codexEvent(t, c)
				if !malformed {
					if e.Kind != tc.kind {
						t.Fatalf("positive control: %#v", e)
					}
					return
				}
				if e.Kind != "disconnected" || !strings.Contains(e.Err, "the adapter ended the transport: ") || !strings.Contains(e.Err, tc.want) {
					t.Fatalf("not ended over the field: %#v", e)
				}
				if strings.Contains(e.Err, "‮") || strings.Contains(e.Err, "\n") || len(e.Err) > 600 {
					t.Fatalf("native value in the error: %q", e.Err)
				}
				if !strings.Contains(e.Err, "stays live in the ledger") {
					t.Fatalf("no D-202 tail: %q", e.Err)
				}
				if !p.closed() {
					t.Fatal("transport left open")
				}
			})
		}
	}
}

// An EMPTY threadId is named as empty, never "a JSON string" (the table above
// fills an empty threadId with the thread's own, so it cannot carry this row).
// The positive control is the table's completed-thread-* rows.
func TestCodexEmptyThreadIDNamedEmpty(t *testing.T) {
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	codexBarrier(t, c, p, id, "turn/one")
	p.frame(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "", "turn": map[string]any{"id": "turn/one", "status": "completed"}}})
	e := codexEvent(t, c)
	if e.Kind != "disconnected" || !strings.Contains(e.Err, "Codex turn/completed threadId is empty, where a thread id was expected") {
		t.Fatalf("empty threadId: %#v", e)
	}
}

// A field the method does not use never drops it: the shared struct failed
// the whole notification. The measured collision is remoteControl/status/
// changed's string `status` (0.160.0) against thread/status/changed's
// object. Keys are exact: a case variant is another key.
func TestCodexUnusedFieldNeverDrops(t *testing.T) {
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	// Not a method the adapter uses: never decoded; nothing published.
	p.notify("remoteControl/status/changed", map[string]any{"status": "connected"})
	codexBarrier(t, c, p, id, "turn/one")
	// The approval carries a mistyped display-only id: listed, the field left out.
	p.frame(map[string]any{"id": 4, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": id, "turnId": "turn/one", "itemId": 7, "approvalId": []int{1}, "status": "x"}})
	if e := codexEvent(t, c); e.Kind != "approval" || e.Approval.ItemID != "" || e.Approval.NativeApprovalID != "" {
		t.Fatalf("approval with a mistyped display id: %#v", e)
	}
	// turn/completed carrying fields it never reads, mistyped, and a case
	// variant of its turn id: completes turn/one, exactly.
	p.notify("turn/completed", map[string]any{"threadId": id, "turnId": 7, "status": "idle", "itemId": map[string]int{}, "turn": map[string]any{"id": "turn/one", "ID": "turn/two", "status": "completed", "items": 3}})
	if e := codexEvent(t, c); e.Kind != "result" || e.TurnID != "turn/one" {
		t.Fatalf("completion: %#v", e)
	}
	if e := codexEvent(t, c); e.Kind != "idle" {
		t.Fatalf("idle: %#v", e)
	}
	c.mu.Lock()
	n := len(c.approvals)
	c.mu.Unlock()
	if n != 0 {
		t.Fatal("the turn's approval outlived it")
	}
}

// An unusable status for a turn that is NOT the active one moves no state:
// it never ends the worker running the active turn; a gap names it. A
// replayed completion of a finished turn ends nothing, whatever its status.
func TestCodexUnknownStatusOfAnotherTurn(t *testing.T) {
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	codexBarrier(t, c, p, id, "turn/one")
	p.notify("turn/completed", map[string]any{"threadId": id, "turn": map[string]any{"id": "turn/one", "status": "completed"}})
	_, _ = codexEvent(t, c), codexEvent(t, c) // result, idle
	p.notify("turn/completed", map[string]any{"threadId": id, "turn": map[string]any{"id": "turn/one", "status": 7}})
	codexBarrier(t, c, p, id, "turn/two")
	p.notify("turn/completed", map[string]any{"threadId": id, "turn": map[string]any{"id": "turn/zero", "status": "cancelled"}})
	e := codexEvent(t, c)
	if e.Kind != "observation_gap" || e.TurnID != "turn/two" || !strings.Contains(e.Err, "turn/zero, not the active turn") {
		t.Fatalf("another turn's status: %#v", e)
	}
	p.notify("turn/completed", map[string]any{"threadId": id, "turn": map[string]any{"id": "turn/two", "status": "completed"}})
	if e := codexEvent(t, c); e.Kind != "result" || e.TurnID != "turn/two" {
		t.Fatalf("the active turn ended: %#v", e)
	}
}

// A request with no native answer is left pending ONLY where the receipt can
// name a turn Interrupt accepts, and then published as observation_gap,
// never approval_refused; otherwise the transport ends. A decline on offer
// with an answerable id is written and published as approval_refused, the
// empty-turn request included (it was dropped).
func TestCodexUnanswerableRequestVerdicts(t *testing.T) {
	const perm = "item/permissions/requestApproval"
	const cmd = "item/commandExecution/requestApproval"
	const elicit = "mcpServer/elicitation/request"
	for _, tc := range []struct {
		name   string
		active string // turn started before the request; "" none
		id     any
		method string
		params map[string]any
		want   string // gap, unscoped, ended, refused, dropped, listed
	}{
		{"permissions-bad-id-own-turn", "turn/one", "a b", perm, map[string]any{"turnId": "turn/one"}, "gap"},
		{"permissions-bad-id-no-turn", "", "a b", perm, map[string]any{"turnId": "turn/one"}, "ended"},
		{"permissions-bad-id-other-turn", "turn/two", "a b", perm, map[string]any{"turnId": "turn/one"}, "ended"},
		{"user-input-bad-id-own-turn", "turn/one", json.RawMessage("1.5"), "item/tool/requestUserInput", map[string]any{"turnId": "turn/one"}, "gap"},
		{"no-decline-bad-id-own-turn", "turn/one", "a b", cmd, map[string]any{"turnId": "turn/one", "availableDecisions": []string{"accept"}}, "gap"},
		{"object-id-decline-offered", "turn/one", map[string]int{"n": 1}, cmd, map[string]any{"turnId": "turn/one"}, "gap"},
		{"object-id-no-turn", "", map[string]int{"n": 1}, cmd, map[string]any{"turnId": "turn/one"}, "ended"},
		{"elicitation-no-turn-id-active", "turn/one", 9, elicit, map[string]any{}, "unscoped"},
		{"elicitation-no-turn-id-idle", "", 9, elicit, map[string]any{}, "unscoped"},
		{"elicitation-bad-id-no-turn-id", "turn/one", "a b", elicit, map[string]any{}, "unscoped"},
		{"command-empty-turn-id-decline", "turn/one", 9, cmd, map[string]any{"turnId": ""}, "refused"},
		{"command-absent-turn-id-decline", "", "req/9", cmd, map[string]any{}, "refused"},
		{"permissions-absent-id", "turn/one", nil, perm, map[string]any{"turnId": "turn/one"}, "dropped"},
		{"permissions-good-id-control", "turn/one", 9, perm, map[string]any{"turnId": "turn/one"}, "listed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, p, l := codexFixture(t)
			id := codexStart(t, c, l)
			if tc.active != "" {
				codexBarrier(t, c, p, id, tc.active)
			}
			params := map[string]any{"threadId": id, "itemId": "item/one"}
			for k, v := range tc.params {
				params[k] = v
			}
			f := map[string]any{"method": tc.method, "params": params}
			if tc.id != nil {
				f["id"] = tc.id
			}
			before := len(p.allWrites())
			p.frame(f)
			if tc.want == "dropped" {
				codexBarrier(t, c, p, id, "turn/three")
				return
			}
			e := codexEvent(t, c)
			switch tc.want {
			case "listed":
				if e.Kind != "approval" {
					t.Fatalf("positive control: %#v", e)
				}
			case "ended":
				if e.Kind != "disconnected" || !p.closed() {
					t.Fatalf("not ended: %#v", e)
				}
			case "refused":
				if e.Kind != "approval_refused" || e.TurnID != "" || !strings.Contains(e.Err, "carries no turn id") || !strings.Contains(e.Err, "declined natively") {
					t.Fatalf("not declined: %#v", e)
				}
				writes := p.allWrites()
				want, _ := json.Marshal(tc.id)
				if len(writes) != before+1 || !strings.Contains(string(writes[len(writes)-1]), `"id":`+string(want)) || !strings.Contains(string(writes[len(writes)-1]), `"decline"`) {
					t.Fatalf("decline %s", writes[len(writes)-1])
				}
			case "unscoped":
				// A request naming no turn is no turn's: scoped to the active
				// one, that turn's idle cleared it while it still waited.
				if e.Kind != "observation_gap" || e.TurnID != "" || e.Approval != nil || !strings.Contains(e.Err, "stays pending natively") || !strings.Contains(e.Err, "no turn of its own") {
					t.Fatalf("not an unscoped gap: %#v", e)
				}
				if len(p.allWrites()) != before || p.closed() {
					t.Fatal("answered or ended")
				}
				p.frame(f) // the replay publishes nothing
				codexBarrier(t, c, p, id, "turn/three")
			case "gap":
				if e.Kind != "observation_gap" || e.TurnID != tc.active || e.Approval != nil || !strings.Contains(e.Err, "stays pending natively") || !strings.Contains(e.Err, "interrupting turn "+tc.active) {
					t.Fatalf("not a gap naming the active turn: %#v", e)
				}
				if len(p.allWrites()) != before {
					t.Fatal("an answer was written to a request with none on offer")
				}
				// The replay publishes nothing.
				p.frame(f)
				codexBarrier(t, c, p, id, tc.active)
				// The named control is accepted.
				if err := c.Interrupt(context.Background(), id, tc.active); err != nil {
					t.Fatalf("the named interrupt was refused: %v", err)
				}
				c.mu.Lock()
				n, closed := len(c.approvals), c.closed
				c.mu.Unlock()
				if n != 0 || closed {
					t.Fatalf("listed %d or closed %v", n, closed)
				}
			}
		})
	}
}

// A REQUEST the adapter does not handle (another method, another thread, no
// readable thread) stays natively pending if dropped: an unscoped gap,
// nothing written, the worker kept; its method fenced. Past the cap of
// distinct ones in a turn, the transport ends. Notifications of unknown
// methods publish nothing.
func TestCodexUnhandledRequestIsAGap(t *testing.T) {
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	codexBarrier(t, c, p, id, "turn/one")
	before := len(p.allWrites())
	p.frame(map[string]any{"method": "item/applyPatch/requestApproval", "params": map[string]any{"threadId": id}})
	p.frame(map[string]any{"id": 5, "method": "item/applyPatch/requestApproval\nforged line", "params": map[string]any{"threadId": id}})
	e := codexEvent(t, c)
	if e.Kind != "observation_gap" || e.TurnID != "" || strings.Contains(e.Err, "\n") || !strings.Contains(e.Err, "does not handle") {
		t.Fatalf("unhandled request: %#v", e)
	}
	// Its replay publishes nothing: the next event is the next request's.
	p.frame(map[string]any{"id": 5, "method": "item/applyPatch/requestApproval\nforged line", "params": map[string]any{"threadId": id}})
	p.frame(map[string]any{"id": 6, "method": "item/permissions/requestApproval", "params": map[string]any{"threadId": "another-thread", "turnId": "turn/one"}})
	if e := codexEvent(t, c); e.Kind != "observation_gap" || e.TurnID != "" || strings.Contains(e.Err, "another-thread") || !strings.Contains(e.Err, "for another thread") {
		t.Fatalf("another thread's request: %#v", e)
	}
	p.frame(map[string]any{"id": 7, "method": "mcpServer/elicitation/request", "params": map[string]any{"serverName": "x"}})
	if e := codexEvent(t, c); e.Kind != "observation_gap" || !strings.Contains(e.Err, "no readable thread id") {
		t.Fatalf("thread-less elicitation: %#v", e)
	}
	if len(p.allWrites()) != before {
		t.Fatal("an unhandled request was answered")
	}
	for i := 0; i < codexRefusalCap; i++ {
		p.frame(map[string]any{"id": 100 + i, "method": "x/request", "params": map[string]any{}})
		if e := codexEvent(t, c); i < codexRefusalCap-3 && e.Kind != "observation_gap" {
			t.Fatalf("request %d: %#v", i, e)
		} else if i >= codexRefusalCap-3 {
			if e.Kind == "disconnected" {
				if !strings.Contains(e.Err, strconv.Itoa(codexRefusalCap)+" requests since the last idle") {
					t.Fatalf("cap: %#v", e)
				}
				return
			}
		}
	}
	t.Fatal("no cap on unlisted requests")
}

// At most codexPendingCap approvals are listed; the next ends the transport.
func TestCodexPendingApprovalCap(t *testing.T) {
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	codexBarrier(t, c, p, id, "turn/one")
	for i := 0; i <= codexPendingCap; i++ {
		p.frame(map[string]any{"id": i, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": id, "turnId": "turn/one", "pad": strings.Repeat("x", 1024)}})
		e := codexEvent(t, c)
		if i < codexPendingCap && e.Kind != "approval" {
			t.Fatalf("approval %d: %#v", i, e)
		}
		if i == codexPendingCap && (e.Kind != "disconnected" || !strings.Contains(e.Err, "more than "+strconv.Itoa(codexPendingCap)+" approvals")) {
			t.Fatalf("no cap: %#v", e)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.seenApprovals {
		_ = s.digest // a digest, never the params (compile-time shape)
	}
}

// Busy with no tracked turn (a replayed `active` after turn/completed): Send
// refuses ("inspect before sending"), and the inspect's thread/read reply,
// claimed on the reader, makes the backend idle, so the next Send starts a
// turn. Only from busy: an ended thread stays ended.
func TestCodexInspectRepairsBusyWithNoTurn(t *testing.T) {
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	codexBarrier(t, c, p, id, "turn/one")
	p.notify("turn/completed", map[string]any{"threadId": id, "turn": map[string]any{"id": "turn/one", "status": "completed"}})
	_, _ = codexEvent(t, c), codexEvent(t, c)
	p.notify("thread/status/changed", map[string]any{"threadId": id, "status": map[string]any{"type": "active"}})
	codexBarrier(t, c, p, id, "turn/two") // turn/one is finished: a barrier needs a new turn
	c.mu.Lock()
	c.activeTurn, c.state = "", "busy" // the replay's state: busy, no turn
	c.mu.Unlock()
	if _, err := c.Send(context.Background(), id, "wake"); err == nil || !strings.Contains(err.Error(), "inspect before sending") {
		t.Fatalf("positive control, busy with no turn: %v", err)
	}
	if _, err := c.Inspect(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	state := c.state
	c.mu.Unlock()
	if state != "idle" {
		t.Fatalf("inspect did not repair: %s", state)
	}
	if d, err := c.Send(context.Background(), id, "wake"); err != nil || d.TurnID != "turn/one" {
		t.Fatalf("send after repair: %#v %v", d, err)
	}
}

func TestCodexIdleRepairOnlyFromBusy(t *testing.T) {
	for _, state := range []string{"busy", "ended", "unknown"} {
		t.Run(state, func(t *testing.T) {
			c, _, l := codexFixture(t)
			id := codexStart(t, c, l)
			c.mu.Lock()
			c.state, c.activeTurn = state, ""
			c.mu.Unlock()
			if _, err := c.Inspect(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			c.mu.Lock()
			got := c.state
			c.mu.Unlock()
			if want := map[string]string{"busy": "idle", "ended": "ended", "unknown": "unknown"}[state]; got != want {
				t.Fatalf("%s became %s", state, got)
			}
		})
	}
	t.Run("tracked-turn", func(t *testing.T) {
		c, p, l := codexFixture(t)
		id := codexStart(t, c, l)
		codexBarrier(t, c, p, id, "turn/one")
		if _, err := c.Inspect(context.Background(), id); err != nil {
			t.Fatal(err)
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.state != "busy" {
			t.Fatalf("a tracked turn was idled by an inspect: %s", c.state)
		}
	})
}

// Ordered on the reader: a thread/read reply saying idle, then a
// thread/status/changed `active` for a server-started turn: the newer busy
// stands (orc-3's condition; repairing from Inspect's goroutine after the
// call would overwrite it).
//
// The newer `active` installs no tracked turn (a turn/started did, and then
// the repair refused for that reason, so a repair moved into Inspect passed:
// the Codex code pass and the Fable cross-check). One P makes the reader run
// on past the reply it handed over: it reads the queued `active` and the
// barrier before Inspect's goroutine runs, so a repair there lands last.
func TestCodexIdleRepairIsOrderedOnTheReader(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	for i := 0; i < 20; i++ {
		c, p, l := codexFixture(t)
		id := codexStart(t, c, l)
		c.mu.Lock()
		c.state, c.activeTurn = "busy", ""
		c.mu.Unlock()
		inner := p.onWrite
		p.onWrite = func(e codexEnvelope) {
			inner(e)
			if e.Method == "thread/read" {
				p.notify("thread/status/changed", map[string]any{"threadId": id, "status": map[string]any{"type": "active"}})
				// The barrier moves no backend state.
				p.frame(map[string]any{"id": 1, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": id, "turnId": "turn/x"}})
			}
		}
		if _, err := c.Inspect(context.Background(), id); err != nil {
			t.Fatal(err)
		}
		if e := codexEvent(t, c); e.Kind != "approval" {
			t.Fatalf("barrier: %#v", e)
		}
		c.mu.Lock()
		state, turn := c.state, c.activeTurn
		c.mu.Unlock()
		if state != "busy" || turn != "" {
			t.Fatalf("run %d: an older idle overwrote a newer busy: %s %q", i, state, turn)
		}
		_ = c.Close()
	}
}

// The reader installs the active turn from turn/start's own reply before the
// next frame: a request for that turn read right after it is addressable,
// never ended for want of a tracked turn (Codex design pass).
func TestCodexTurnStartReplyInstallsActiveTurnOnTheReader(t *testing.T) {
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	// The reader alone, no Send: a slot as Send's call leaves it, then the
	// reply and, right behind it, a request for that turn with no answer on
	// offer. Only the reader can have made the turn active.
	slot := make(chan codexReply, 1)
	c.mu.Lock()
	c.pending["s:99"], c.pendingMethod["s:99"] = slot, "turn/start"
	c.mu.Unlock()
	p.frame(map[string]any{"id": "99", "result": map[string]any{"turn": map[string]string{"id": "turn/one"}}})
	p.frame(map[string]any{"id": "a b", "method": "item/permissions/requestApproval", "params": map[string]any{"threadId": id, "turnId": "turn/one"}})
	e := codexEvent(t, c)
	if e.Kind != "observation_gap" || e.TurnID != "turn/one" {
		t.Fatalf("the reply's turn was not active for the next frame: %#v", e)
	}
	if reply := <-slot; reply.err != nil {
		t.Fatal(reply.err)
	}
}

// A reply's turn id is read per method: a mistyped field the method never
// uses no longer ends the transport over a delivered turn.
func TestCodexReplyTurnIDPerMethod(t *testing.T) {
	for _, tc := range []struct {
		method string
		result string
		want   string
	}{
		{"turn/start", `{"turn":{"id":"turn/one","status":7},"turnId":7}`, "turn/one"},
		{"turn/start", `{"turn":{"id":"turn/one","ID":"turn/two"}}`, "turn/one"},
		{"turn/steer", `{"turnId":"turn/one","turn":"x"}`, "turn/one"},
		{"turn/start", `{"turnId":"turn/one"}`, ""},
		{"turn/steer", `{"turn":{"id":"turn/one"}}`, ""},
		{"turn/start", `null`, ""},
	} {
		if got := codexReplyTurnID(tc.method, json.RawMessage(tc.result)); got != tc.want {
			t.Errorf("%s %s: %q, want %q", tc.method, tc.result, got, tc.want)
		}
	}
}

// The envelope is read by exact key: a case variant is not the method.
func TestCodexEnvelopeExactKeys(t *testing.T) {
	for _, tc := range []struct {
		raw    string
		ok     bool
		method string
	}{
		{`{"method":"turn/started","params":{}}`, true, "turn/started"},
		{`{"Method":"turn/started","params":{}}`, true, ""},
		{`{"method":null,"id":"1"}`, true, ""},
		{`{"method":7}`, false, ""},
		{`[1]`, false, ""},
		{`null`, false, ""},
	} {
		e, ok := codexDecodeEnvelope(json.RawMessage(tc.raw))
		if ok != tc.ok || e.Method != tc.method {
			t.Errorf("%s: %v %q", tc.raw, ok, e.Method)
		}
	}
}

// fileChange item/completed fields unreadable during an owned turn report
// renewal coverage unknown, never a silent miss; outside one, nothing.
func TestCodexUnreadableFileChangeLosesCoverage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params func(id string) any
		lost   bool
	}{
		{"item-type-number", func(id string) any {
			return map[string]any{"threadId": id, "turnId": "turn/one", "item": map[string]any{"type": 7}}
		}, true},
		{"turn-id-number", func(id string) any {
			return map[string]any{"threadId": id, "turnId": 7, "item": map[string]any{"type": "fileChange", "status": "completed"}}
		}, true},
		{"thread-id-absent", func(id string) any {
			return map[string]any{"turnId": "turn/one", "item": map[string]any{"type": "fileChange"}}
		}, true},
		{"params-string", func(id string) any { return "x" }, true},
		{"command-item-control", func(id string) any {
			return map[string]any{"threadId": id, "turnId": "turn/one", "item": map[string]any{"type": "commandExecution"}}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, p, l := codexFixture(t)
			m := newRenewalMailbox()
			c.observeRenewals(m)
			id := codexStart(t, c, l)
			if _, err := c.Send(context.Background(), id, "wake"); err != nil {
				t.Fatal(err)
			}
			p.notify("item/completed", tc.params(id))
			codexBarrier(t, c, p, id, "turn/one")
			m.mu.Lock()
			lost := m.gaps > 0
			m.mu.Unlock()
			if lost != tc.lost {
				t.Fatalf("coverage lost %v, want %v", lost, tc.lost)
			}
		})
	}
}

// An unreadable resolution with approvals listed is a turn-scoped gap.
func TestCodexUnreadableResolutionIsAGap(t *testing.T) {
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	codexBarrier(t, c, p, id, "turn/one")
	p.notify("serverRequest/resolved", map[string]any{"threadId": id, "requestId": map[string]int{}})
	p.frame(map[string]any{"id": 1, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": id, "turnId": "turn/one"}})
	if e := codexEvent(t, c); e.Kind != "approval" {
		t.Fatalf("nothing listed, so no gap first: %#v", e)
	}
	p.notify("serverRequest/resolved", map[string]any{"threadId": id, "requestId": map[string]int{}})
	if e := codexEvent(t, c); e.Kind != "observation_gap" || e.TurnID != "turn/one" || !strings.Contains(e.Err, "a JSON object") {
		t.Fatalf("resolution: %#v", e)
	}
}

// The controller: a turn-scoped gap reads pending approvals unknown until
// idle; an unscoped one survives our turn's idle and clears at ended or
// disconnected; a result ends its own turn's listed approvals only.
func TestControllerObservationGapScopes(t *testing.T) {
	c, _, l, _ := controllerFixture(t)
	ctx := context.Background()
	sid := l.identity.SessionID
	observe := func(e Event) Receipt {
		t.Helper()
		e.NativeSessionID = sid
		r, err := c.Observe(ctx, e)
		if err != nil {
			t.Fatalf("%s: %v", e.Kind, err)
		}
		return r
	}
	observe(Event{Kind: "busy", TurnID: "t1"})
	if r := observe(Event{Kind: "approval_refused", TurnID: "t1", Err: "declined natively"}); r.PendingApproval.State != "observed_absent" {
		t.Fatalf("positive control, approval_refused: %#v", r.PendingApproval)
	}
	r := observe(Event{Kind: "observation_gap", TurnID: "t1", Err: "stays pending natively\nforged"})
	if r.PendingApproval.State != "unknown" || !strings.Contains(r.PendingApproval.Detail, "stays pending natively") || strings.Contains(r.PendingApproval.Detail, "\n") {
		t.Fatalf("scoped gap: %#v", r.PendingApproval)
	}
	if r := observe(Event{Kind: "idle", TurnID: "t1"}); r.PendingApproval.State != "observed_absent" {
		t.Fatalf("idle did not clear the scoped gap: %#v", r.PendingApproval)
	}
	observe(Event{Kind: "observation_gap", Err: "another thread's request"})
	observe(Event{Kind: "busy", TurnID: "t2"})
	if r := observe(Event{Kind: "idle", TurnID: "t2"}); r.PendingApproval.State != "unknown" || !strings.Contains(r.PendingApproval.Detail, "another thread's request") {
		t.Fatalf("our idle cleared an unscoped gap: %#v", r.PendingApproval)
	}
	if r := observe(Event{Kind: "disconnected", Err: "x"}); r.PendingApproval.State != "unknown" {
		t.Fatalf("disconnect: %#v", r.PendingApproval)
	}
	if c.unscopedGap != "" {
		t.Fatal("disconnect kept the unscoped gap")
	}
	for _, bad := range []Event{
		{Kind: "observation_gap", NativeSessionID: sid},
		{Kind: "observation_gap", NativeSessionID: sid, Err: "x", Approval: &Approval{ID: json.RawMessage(`1`)}},
	} {
		if _, err := c.Observe(ctx, bad); err == nil {
			t.Fatalf("malformed observation_gap accepted: %#v", bad)
		}
	}
}

func TestControllerResultEndsItsTurnsApprovals(t *testing.T) {
	c, _, l, _ := controllerFixture(t)
	ctx := context.Background()
	sid := l.identity.SessionID
	for _, e := range []Event{
		{Kind: "busy", TurnID: "t1"},
		{Kind: "approval", TurnID: "t1", Approval: &Approval{ID: json.RawMessage(`7`), NativeSessionID: sid, TurnID: "t1", Kind: "item/commandExecution/requestApproval"}},
		{Kind: "approval", TurnID: "t2", Approval: &Approval{ID: json.RawMessage(`8`), NativeSessionID: sid, TurnID: "t2", Kind: "item/commandExecution/requestApproval"}},
	} {
		e.NativeSessionID = sid
		if _, err := c.Observe(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	r, err := c.Observe(ctx, Event{Kind: "result", NativeSessionID: sid, TurnID: "t1", ResultRef: "t1", ResultStatus: "completed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Approvals) != 1 || string(r.Approvals[0].ID) != "8" {
		t.Fatalf("approvals after t1's result: %#v", r.Approvals)
	}
}

// Claude recovery ignores observation_gap (orc-3's condition): treated as
// activity with no turn id it would make every worker unresumable.
func TestClaudeRecoveryIgnoresObservationGap(t *testing.T) {
	for _, gap := range []bool{false, true} {
		var s claudeRecovery
		id := Identity{Harness: "claude", SessionID: "native"}
		receipts := []Receipt{
			{Identity: id, NativeEvent: &Event{Kind: "busy", NativeSessionID: "native", TurnID: "u1"}},
			{Identity: id, NativeEvent: &Event{Kind: "result", NativeSessionID: "native", TurnID: "u1", ResultRef: "r1", ResultStatus: "completed"}},
			{Identity: id, NativeEvent: &Event{Kind: "idle", NativeSessionID: "native", TurnID: "u1"}},
		}
		if gap {
			receipts = append(receipts, Receipt{Identity: id, NativeEvent: &Event{Kind: "observation_gap", NativeSessionID: "native", Err: "x"}})
		}
		for _, r := range receipts {
			s.observe(r)
		}
		if !s.resumable(map[int64]Delivery{1: {State: "delivered", TurnID: "u1"}}, "") {
			t.Fatalf("gap=%v: not resumable", gap)
		}
	}
}

// Claude's F-07 answers (the subagent / turn-less deny, the unsupported
// control reply) are written under the deadline, and a failed one ends the
// transport with its cause named; a written one publishes nothing.
func TestClaudeF07AnswersCarryADeadline(t *testing.T) {
	deny, _ := json.Marshal(map[string]any{"subtype": "can_use_tool", "tool_name": "Bash", "tool_use_id": "toolu_1", "input": map[string]any{"command": "true"}, "agent_id": "sub-1"})
	other, _ := json.Marshal(map[string]any{"subtype": "set_model"})
	for _, tc := range []struct {
		name string
		req  json.RawMessage
		what string
	}{
		{"subagent-deny", deny, "deny of a subagent's or turn-less request"},
		{"unsupported-control", other, "reply to an unsupported control request"},
	} {
		for _, fail := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/control", true: "/fails"}[fail], func(t *testing.T) {
				c := claudeIDBackend()
				c.proc = &claudeFakeProcess{write: func(ctx context.Context, _ any) error {
					if _, ok := ctx.Deadline(); !ok {
						t.Error("F-07 answer written with no deadline")
					}
					if fail {
						return context.DeadlineExceeded
					}
					return nil
				}}
				err := c.permission(json.RawMessage(`"req-1"`), tc.req)
				if !fail {
					if err != nil {
						t.Fatalf("positive control: %v", err)
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), tc.what) || !strings.Contains(err.Error(), "within 10s") || !strings.Contains(err.Error(), "the adapter ended the transport") {
					t.Fatalf("unwrapped: %v", err)
				}
			})
		}
	}
}

// A Claude result uuid is kept as a 32-byte digest, deduplicated exactly.
func TestClaudeResultUUIDDigest(t *testing.T) {
	c := claudeIDBackend()
	big := strings.Repeat("u", 1<<20)
	frame := func(uuid string) json.RawMessage {
		raw, _ := json.Marshal(map[string]any{"type": "result", "subtype": "success", "session_id": "native", "uuid": uuid})
		return raw
	}
	for _, uuid := range []string{big, big, big + "x"} {
		if err := c.frame(frame(uuid)); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.results) != 2 {
		t.Fatalf("%d result keys, want 2", len(c.results))
	}
	for k := range c.results {
		if len(k) != 32 { // a raw uuid key is the uuid's own length (1 MiB here)
			t.Fatalf("result key of %d bytes, want a 32-byte digest", len(k))
		}
	}
	n := 0
	for len(c.events) > 0 {
		if e := <-c.events; e.Kind == "result" {
			n++
			if len(e.ResultRef) > 200 {
				t.Fatalf("result ref unbounded: %d", len(e.ResultRef))
			}
		}
	}
	if n != 2 {
		t.Fatalf("%d results published, want 2", n)
	}
}

// A thread status of idle repairs busy-with-no-turn (a replayed `active`
// after turn/completed), and only that: a tracked turn stays busy. The
// barrier is an approval event, which moves no backend state.
func TestCodexIdleStatusRepairsBusyWithNoTurn(t *testing.T) {
	for _, tc := range []struct{ state, tracked, want string }{
		{"busy", "", "idle"},
		{"busy", "turn/one", "busy"},
		{"unknown", "", "unknown"},
		{"ended", "", "ended"},
	} {
		tracked := tc.tracked
		c, p, l := codexFixture(t)
		id := codexStart(t, c, l)
		c.mu.Lock()
		c.state, c.activeTurn = tc.state, tracked
		c.mu.Unlock()
		p.notify("thread/status/changed", map[string]any{"threadId": id, "status": map[string]any{"type": "idle"}})
		p.frame(map[string]any{"id": 1, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": id, "turnId": "turn/x"}})
		if e := codexEvent(t, c); e.Kind != "approval" {
			t.Fatalf("barrier: %#v", e)
		}
		c.mu.Lock()
		state := c.state
		c.mu.Unlock()
		if state != tc.want {
			t.Fatalf("%s, tracked %q: %s, want %s", tc.state, tracked, state, tc.want)
		}
	}
}

// A frame with an id is a request, whatever its method: a thread/status/
// changed carrying one moved the backend busy while it stayed natively
// pending (the Codex code pass). The notification is the positive control.
func TestCodexRequestOnANotificationMethod(t *testing.T) {
	for _, request := range []bool{false, true} {
		c, p, l := codexFixture(t)
		id := codexStart(t, c, l)
		f := map[string]any{"method": "thread/status/changed", "params": map[string]any{"threadId": id, "status": map[string]any{"type": "active"}}}
		if request {
			f["id"] = 17
		}
		p.frame(f)
		p.frame(map[string]any{"id": 1, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": id, "turnId": "turn/x"}})
		e := codexEvent(t, c)
		if request {
			if e.Kind != "observation_gap" || e.TurnID != "" || !strings.Contains(e.Err, "thread/status/changed (a method the adapter does not handle as a request)") {
				t.Fatalf("request: %#v", e)
			}
			e = codexEvent(t, c)
		}
		if e.Kind != "approval" {
			t.Fatalf("barrier: %#v", e)
		}
		c.mu.Lock()
		state := c.state
		c.mu.Unlock()
		if want := map[bool]string{false: "busy", true: "idle"}[request]; state != want {
			t.Fatalf("request=%v: %s, want %s", request, state, want)
		}
	}
}

// A FRESH request naming a finished turn was dropped whole; a replay of one
// seen while the turn ran still publishes nothing (the Codex code pass).
func TestCodexFreshRequestOnAFinishedTurn(t *testing.T) {
	const cmd = "item/commandExecution/requestApproval"
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	codexBarrier(t, c, p, id, "turn/one")
	seen := map[string]any{"id": 4, "method": cmd, "params": map[string]any{"threadId": id, "turnId": "turn/one"}}
	p.frame(seen)
	if e := codexEvent(t, c); e.Kind != "approval" {
		t.Fatalf("positive control: %#v", e)
	}
	p.notify("turn/completed", map[string]any{"threadId": id, "turn": map[string]any{"id": "turn/one", "status": "completed"}})
	_, _ = codexEvent(t, c), codexEvent(t, c)
	before := len(p.allWrites())
	p.frame(seen) // the replay
	p.frame(map[string]any{"id": 18, "method": cmd, "params": map[string]any{"threadId": id, "turnId": "turn/one"}})
	e := codexEvent(t, c)
	if e.Kind != "approval_refused" || e.TurnID != "turn/one" || !strings.Contains(e.Err, "names turn turn/one, which has finished") || !strings.Contains(e.Err, "declined natively") {
		t.Fatalf("fresh request on a finished turn: %#v", e)
	}
	if w := p.allWrites(); len(w) != before+1 || !strings.Contains(string(w[len(w)-1]), `"id":18`) || !strings.Contains(string(w[len(w)-1]), `"decline"`) {
		t.Fatalf("writes: %d new", len(w)-before)
	}
	// No decline on offer and no tracked turn of its own: the end.
	p.frame(map[string]any{"id": 19, "method": "item/permissions/requestApproval", "params": map[string]any{"threadId": id, "turnId": "turn/one"}})
	if e := codexEvent(t, c); e.Kind != "disconnected" || !strings.Contains(e.Err, "which has finished") || !p.closed() {
		t.Fatalf("undeclinable on a finished turn: %#v", e)
	}
}

// The reuse rule runs before a refusal: a listed id reused with no turn id
// was declined natively, answering the LISTED request, which stayed listed
// (the Fable cross-check). A fresh id is the positive control (declined).
func TestCodexReuseRuleBeforeRefusal(t *testing.T) {
	const cmd = "item/commandExecution/requestApproval"
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	codexBarrier(t, c, p, id, "turn/one")
	p.frame(map[string]any{"id": 5, "method": cmd, "params": map[string]any{"threadId": id, "turnId": "turn/one"}})
	if e := codexEvent(t, c); e.Kind != "approval" {
		t.Fatalf("listed: %#v", e)
	}
	p.frame(map[string]any{"id": 6, "method": cmd, "params": map[string]any{"threadId": id}})
	if e := codexEvent(t, c); e.Kind != "approval_refused" {
		t.Fatalf("positive control: %#v", e)
	}
	before := len(p.allWrites())
	p.frame(map[string]any{"id": 5, "method": cmd, "params": map[string]any{"threadId": id, "other": true}})
	if e := codexEvent(t, c); e.Kind != "disconnected" || !strings.Contains(e.Err, "reused an approval request ID") {
		t.Fatalf("reuse: %#v", e)
	}
	if len(p.allWrites()) != before {
		t.Fatal("a decline was written to the listed request's id")
	}
}

// Approve reads availableDecisions by exact key: an `AvailableDecisions`
// null alias erased a decline-only list, and allow sent accept (the Codex
// code pass). Decline is the positive control.
func TestCodexApproveOfferedDecisionsExactKey(t *testing.T) {
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	p.frame(json.RawMessage(`{"id":3,"method":"item/commandExecution/requestApproval","params":{"threadId":"` + id + `","turnId":"turn/one","availableDecisions":["decline"],"AvailableDecisions":null}}`))
	if e := codexEvent(t, c); e.Kind != "approval" {
		t.Fatalf("listed: %#v", e)
	}
	d := ApprovalDecision{ID: json.RawMessage(`3`), NativeSessionID: id, TurnID: "turn/one", Decision: "allow"}
	if err := c.Approve(context.Background(), d); err == nil || !strings.Contains(err.Error(), "does not offer this decision") {
		t.Fatalf("allow over a decline-only list: %v", err)
	}
	d.Decision = "deny"
	if err := c.Approve(context.Background(), d); err != nil {
		t.Fatalf("positive control, decline: %v", err)
	}
	// A list with a non-string element is no offer at all.
	p.frame(json.RawMessage(`{"id":4,"method":"item/commandExecution/requestApproval","params":{"threadId":"` + id + `","turnId":"turn/one","availableDecisions":["accept",7]}}`))
	if e := codexEvent(t, c); e.Kind != "approval" {
		t.Fatalf("listed: %#v", e)
	}
	d = ApprovalDecision{ID: json.RawMessage(`4`), NativeSessionID: id, TurnID: "turn/one", Decision: "allow"}
	if err := c.Approve(context.Background(), d); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("allow over a mixed list: %v", err)
	}
}

// An unusable status ends the transport when no turn is tracked (the other
// turn's case, a gap, is TestCodexUnknownStatusOfAnotherTurn).
func TestCodexUnknownStatusWithNoTrackedTurnEnds(t *testing.T) {
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	p.notify("turn/completed", map[string]any{"threadId": id, "turn": map[string]any{"id": "turn/two", "status": "cancelled"}})
	if e := codexEvent(t, c); e.Kind != "disconnected" || !strings.Contains(e.Err, "unrecognised status cancelled") || !p.closed() {
		t.Fatalf("no tracked turn: %#v", e)
	}
}

// The refusal set is bounded since the last idle, and the idle clears it:
// 63 refusals in one turn, then 64 more in the next are all declined; the
// 65th since that idle ends the transport.
func TestCodexRefusalCapSinceTheLastIdle(t *testing.T) {
	const cmd = "item/commandExecution/requestApproval"
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	codexBarrier(t, c, p, id, "turn/one")
	n := 0
	refuse := func() Event {
		n++
		p.frame(map[string]any{"id": 1000 + n, "method": cmd, "params": map[string]any{"threadId": id}})
		return codexEvent(t, c)
	}
	for i := 0; i < codexRefusalCap-1; i++ {
		if e := refuse(); e.Kind != "approval_refused" {
			t.Fatalf("turn one, %d: %#v", i, e)
		}
	}
	p.notify("turn/completed", map[string]any{"threadId": id, "turn": map[string]any{"id": "turn/one", "status": "completed"}})
	_, _ = codexEvent(t, c), codexEvent(t, c)
	codexBarrier(t, c, p, id, "turn/two")
	for i := 0; i < codexRefusalCap; i++ {
		if e := refuse(); e.Kind != "approval_refused" {
			t.Fatalf("after the idle, %d: %#v", i, e)
		}
	}
	if e := refuse(); e.Kind != "disconnected" || !strings.Contains(e.Err, strconv.Itoa(codexRefusalCap)+" requests since the last idle") {
		t.Fatalf("no cap: %#v", e)
	}
}

// The repair reads the reply's own thread: another thread's idle repairs
// nothing (TestCodexInspectRepairsBusyWithNoTurn is the positive control).
func TestCodexRepairNeedsTheRepliedThread(t *testing.T) {
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	c.mu.Lock()
	c.state, c.activeTurn = "busy", ""
	c.mu.Unlock()
	inner := p.onWrite
	p.onWrite = func(e codexEnvelope) {
		if e.Method == "thread/read" {
			p.reply(e, map[string]any{"thread": map[string]any{"id": "native-2", "status": map[string]string{"type": "idle"}}})
			return
		}
		inner(e)
	}
	if _, err := c.Inspect(context.Background(), id); err == nil {
		t.Fatal("another thread's reply accepted")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != "busy" {
		t.Fatalf("another thread's idle repaired: %s", c.state)
	}
}

// The controller: ended clears an unscoped gap; a listed approval beside an
// unscoped gap reads unknown, never observed; a result that drops the last
// listed approval leaves no waiting_for_approval state behind.
func TestControllerUnscopedGapEndsAndListedApprovals(t *testing.T) {
	c, _, l, _ := controllerFixture(t)
	ctx := context.Background()
	sid := l.identity.SessionID
	observe := func(e Event) Receipt {
		t.Helper()
		e.NativeSessionID = sid
		r, err := c.Observe(ctx, e)
		if err != nil {
			t.Fatalf("%s: %v", e.Kind, err)
		}
		return r
	}
	observe(Event{Kind: "busy", TurnID: "t1"})
	if r := observe(Event{Kind: "approval", TurnID: "t1", Approval: &Approval{ID: json.RawMessage(`7`), NativeSessionID: sid, TurnID: "t1", Kind: "item/commandExecution/requestApproval"}}); r.PendingApproval.State != "observed" {
		t.Fatalf("positive control: %#v", r.PendingApproval)
	}
	if r := observe(Event{Kind: "observation_gap", Err: "unscoped request"}); r.PendingApproval.State != "unknown" || !strings.Contains(r.PendingApproval.Detail, "unscoped request") {
		t.Fatalf("listed beside an unscoped gap: %#v", r.PendingApproval)
	}
	r := observe(Event{Kind: "result", TurnID: "t1", ResultRef: "t1", ResultStatus: "completed"})
	if len(r.Approvals) != 0 || r.State.Detail == "waiting_for_approval" {
		t.Fatalf("result left %d approvals, state %#v", len(r.Approvals), r.State)
	}
	if r := observe(Event{Kind: "ended"}); r.PendingApproval.State != "observed_absent" || c.unscopedGap != "" {
		t.Fatalf("ended kept the unscoped gap: %#v", r.PendingApproval)
	}
}

// An unscoped gap the ledger could not record stays unscoped: as a turn gap
// the next idle cleared it, and the backend never republishes it (the Fable
// cross-check).
func TestControllerUnrecordedUnscopedGapStaysUnscoped(t *testing.T) {
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
	sid := l.identity.SessionID
	if _, err := c.Observe(ctx, Event{Kind: "busy", NativeSessionID: sid, TurnID: "t1"}); err != nil {
		t.Fatal(err)
	}
	l.fail = "inspect"
	if _, err := c.Observe(ctx, Event{Kind: "observation_gap", NativeSessionID: sid, Err: "unscoped request"}); err == nil {
		t.Fatal("positive control: the ledger did not fail")
	}
	l.fail = ""
	r, err := c.Observe(ctx, Event{Kind: "idle", NativeSessionID: sid, TurnID: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	if r.PendingApproval.State != "unknown" || !strings.Contains(r.PendingApproval.Detail, "observation_gap event was not recorded") {
		t.Fatalf("idle cleared an unrecorded unscoped gap: %#v", r.PendingApproval)
	}
}
