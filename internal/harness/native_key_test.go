package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/JsizzleR/buddy-system/internal/fence"
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
			if e.Kind != "approval_refused" || e.Approval != nil {
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
	if e := codexEvent(t, c); e.Kind != "approval_refused" {
		t.Fatalf("-0 published beside 0: %#v", e)
	}
	c.mu.Lock()
	n := len(c.approvals)
	c.mu.Unlock()
	if n != 1 {
		t.Fatalf("backend holds %d pending approvals, want 1", n)
	}
}

// hostileKeys are inputs nativeKey refuses: each would otherwise ride in
// every receipt (size), or reach the coordinator's context with a line break,
// a bidi override or a zero-width character, as a value it must copy back.
func hostileKeys() map[string]string {
	return map[string]string{
		"empty-after-trim": " ",
		"space":            "turn one",
		"newline":          "t\n{\"kind\":\"idle\"}",
		"bidi":             "t‮exe.txt",
		"zero-width":       "turn/one​",
		"non-ascii":        "native-α",
		"plus":             "a+b",
		"nul":              "a\x00b",
		"oversized":        strings.Repeat("x", 129),
		"huge":             strings.Repeat("x", 1<<20),
	}
}

// What a key may be (D-202): what the harnesses were measured to send passes,
// and a passing key is exactly what fence.Field(s, 128) returns, so every
// later render is one token with no copy.
func TestNativeKeyPredicate(t *testing.T) {
	for _, ok := range []string{"019a7f3e-0c2d-7b41-9a8e-5f6b7c8d9e0f", "auto-compact-0", "turn/one", "a:b_c.d", "0", strings.Repeat("x", 128)} {
		if !nativeKey(ok) {
			t.Errorf("measured key refused: %q", ok)
		}
		if fence.Field(ok, 128) != ok {
			t.Errorf("a passing key is altered by fence.Field: %q", ok)
		}
	}
	for name, bad := range hostileKeys() {
		if nativeKey(bad) {
			t.Errorf("%s admitted", name)
		}
	}
	if nativeKey("") {
		t.Error("empty key admitted")
	}
}

// codexWith replaces one method's fixture reply; the rest stay the fixture's.
func codexWith(t *testing.T, method string, reply func(p *fakeCodexProcess, e codexEnvelope)) (*codexBackend, *fakeCodexProcess, Launch) {
	t.Helper()
	c, p, l := codexFixture(t)
	base := p.onWrite
	p.onWrite = func(e codexEnvelope) {
		if e.Method == method {
			reply(p, e)
			return
		}
		base(e)
	}
	return c, p, l
}

func (p *fakeCodexProcess) closed() bool {
	select {
	case _, ok := <-p.lines:
		return !ok
	default:
		return false
	}
}

// The Codex thread id is the ledger's session id: refused at the Start and
// Resume reply unless it meets the ledger's rule, before ledger.Begin, with
// the owned child closed. "a/b" passes nativeKey and fails that rule: the
// failing input that made the thread id take fence.ValidSessionID.
func TestCodexThreadIDRefusedAtStartAndResume(t *testing.T) {
	cases := hostileKeys()
	cases["slash"] = "a/b"
	cases["all"] = "all"
	cases["short-form"] = "s-0123abcd"
	cases["leading-dash"] = "-x"
	for name, bad := range cases {
		for _, resume := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/start", true: "/resume"}[resume], func(t *testing.T) {
				method := map[bool]string{false: "thread/start", true: "thread/resume"}[resume]
				c, p, l := codexWith(t, method, func(p *fakeCodexProcess, e codexEnvelope) {
					p.reply(e, map[string]any{"thread": map[string]any{"id": bad, "status": map[string]string{"type": "idle"}}})
				})
				var err error
				if resume {
					_, err = c.Resume(context.Background(), l, bad, testAuthorization(t))
				} else {
					_, err = c.Start(context.Background(), l, testAuthorization(t))
				}
				if err == nil || !strings.Contains(err.Error(), "outside the session-id rule") {
					t.Fatalf("thread id %q admitted: %v", name, err)
				}
				if strings.Contains(err.Error(), bad) && len(bad) > 3 {
					t.Fatalf("refusal echoes the value: %.120q", err)
				}
				if !p.closed() {
					t.Fatal("owned child left running")
				}
			})
		}
	}
	// Positive control: a UUIDv7, as 0.160.0 sends.
	const real = "019a7f3e-0c2d-7b41-9a8e-5f6b7c8d9e0f"
	c, _, l := codexWith(t, "thread/start", func(p *fakeCodexProcess, e codexEnvelope) {
		p.reply(e, map[string]any{"thread": map[string]any{"id": real, "status": map[string]string{"type": "idle"}}})
	})
	if id, err := c.Start(context.Background(), l, testAuthorization(t)); err != nil || id != real {
		t.Fatalf("real thread id refused: %q %v", id, err)
	}
}

// A turn reply naming a turn the adapter cannot key ends the transport from
// the reader; Send is never handed the reply and returns an UNCERTAIN wake
// (not ErrNotSubmitted: the turn started natively).
func TestCodexTurnReplyKeyEndsTransport(t *testing.T) {
	cases := hostileKeys()
	cases["empty"] = "" // fails the 1..128 rule like any other (Codex code pass)
	for name, bad := range cases {
		for _, method := range []string{"turn/start", "turn/steer"} {
			t.Run(name+"/"+method, func(t *testing.T) {
				c, p, l := codexWith(t, method, func(p *fakeCodexProcess, e codexEnvelope) {
					if method == "turn/start" {
						p.reply(e, map[string]any{"turn": map[string]string{"id": bad, "status": "inProgress"}})
					} else {
						p.reply(e, map[string]string{"turnId": bad})
					}
				})
				id := codexStart(t, c, l)
				if method == "turn/steer" {
					p.notify("turn/started", map[string]any{"threadId": id, "turn": map[string]string{"id": "turn/one"}})
					if e := codexEvent(t, c); e.Kind != "busy" {
						t.Fatalf("setup: %#v", e)
					}
				}
				d, err := c.Send(context.Background(), id, "wake")
				if err == nil || errors.Is(err, ErrNotSubmitted) || d.State != "unknown" || d.TurnID != "" {
					t.Fatalf("delivery %#v %v", d, err)
				}
				e := codexEvent(t, c)
				if e.Kind != "disconnected" || !strings.Contains(e.Err, "turn id outside the plain key set") || !strings.Contains(e.Err, "stays live in the ledger") {
					t.Fatalf("not ended: %#v", e)
				}
				if method == "turn/steer" && !strings.Contains(e.Err, "active turn turn/one ended unobserved") {
					t.Fatalf("ended turn not named: %q", e.Err)
				}
				if len(bad) > 3 && strings.Contains(e.Err, bad) {
					t.Fatal("error echoes the key")
				}
				c.mu.Lock()
				owned := len(c.ownTurns)
				c.mu.Unlock()
				if owned != 0 {
					t.Fatal("refused turn marked ours")
				}
			})
		}
	}
}

// Only the reply Send receives marks a turn ours (D-078 renewal scope).
func TestCodexDuplicateTurnReplyOwnsNothing(t *testing.T) {
	c, p, l := codexWith(t, "turn/start", func(p *fakeCodexProcess, e codexEnvelope) {
		p.reply(e, map[string]any{"turn": map[string]string{"id": "turn/one", "status": "inProgress"}})
		p.reply(e, map[string]any{"turn": map[string]string{"id": "turn/two", "status": "inProgress"}})
	})
	id := codexStart(t, c, l)
	if d, err := c.Send(context.Background(), id, "wake"); err != nil || d.TurnID != "turn/one" {
		t.Fatalf("positive control: %#v %v", d, err)
	}
	// turn/started is a barrier past the duplicate reply.
	p.notify("turn/started", map[string]any{"threadId": id, "turn": map[string]string{"id": "turn/one"}})
	_ = codexEvent(t, c)
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ownTurns["turn/one"] || c.ownTurns["turn/two"] {
		t.Fatalf("own turns %v", c.ownTurns)
	}
}

// Every notification that stores or emits a turn id ends the transport on a
// key fault; compaction, an observation only, is dropped. Each plain twin is
// the positive control.
func TestCodexNotificationTurnKeys(t *testing.T) {
	type frame func(id, turn string) map[string]any
	notes := map[string]struct {
		frame frame
		want  string // the plain twin's event
		drop  bool
	}{
		"turn/started": {func(id, turn string) map[string]any {
			return map[string]any{"method": "turn/started", "params": map[string]any{"threadId": id, "turn": map[string]string{"id": turn}}}
		}, "busy", false},
		"turn/completed": {func(id, turn string) map[string]any {
			return map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": id, "turn": map[string]string{"id": turn, "status": "completed"}}}
		}, "result", false},
		"approval": {func(id, turn string) map[string]any {
			return map[string]any{"id": 4, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": id, "turnId": turn, "itemId": "item/one"}}
		}, "approval", false},
		"compaction": {func(id, turn string) map[string]any {
			return map[string]any{"method": "item/started", "params": map[string]any{"threadId": id, "turnId": turn, "item": map[string]string{"id": "item/c", "type": "contextCompaction"}}}
		}, "compaction_started", true},
	}
	for nname, n := range notes {
		t.Run(nname+"/plain", func(t *testing.T) {
			c, p, l := codexFixture(t)
			id := codexStart(t, c, l)
			p.frame(n.frame(id, "turn/one"))
			if e := codexEvent(t, c); e.Kind != n.want || e.TurnID != "turn/one" {
				t.Fatalf("positive control: %#v", e)
			}
		})
		cases := hostileKeys()
		if nname == "turn/started" || nname == "turn/completed" {
			// Empty was dropped before the key check, leaving the adapter
			// busy with a turn it could not see finish (Codex code pass).
			cases["empty"] = ""
		}
		for kname, bad := range cases {
			t.Run(nname+"/"+kname, func(t *testing.T) {
				c, p, l := codexFixture(t)
				id := codexStart(t, c, l)
				p.frame(n.frame(id, bad))
				if n.drop {
					p.notify("turn/started", map[string]any{"threadId": id, "turn": map[string]string{"id": "turn/one"}})
					if e := codexEvent(t, c); e.Kind != "busy" {
						t.Fatalf("hostile compaction not dropped: %#v", e)
					}
					return
				}
				e := codexEvent(t, c)
				if e.Kind != "disconnected" || !strings.Contains(e.Err, "turn id outside the plain key set") {
					t.Fatalf("hostile turn id kept: %#v", e)
				}
				if !p.closed() {
					t.Fatal("transport left open")
				}
			})
		}
	}
}

// A request id alone outside the key set never ends the worker when the
// request can be declined: the decline answers the id as Codex sent it, and
// approval_refused is published. Other methods, or no decline on offer, end
// the transport when no tracked turn of the request's own can be
// interrupted (here none is started); an id that is absent or null is a
// notification, dropped; any other non-id ends it the same way.
func TestCodexRequestKeyRefusal(t *testing.T) {
	approval := func(id any, method string, extra map[string]any) map[string]any {
		params := map[string]any{"threadId": "", "turnId": "turn/one", "itemId": "item/one"}
		for k, v := range extra {
			params[k] = v
		}
		f := map[string]any{"method": method, "params": params}
		if id != nil {
			f["id"] = id
		}
		return f
	}
	const cmd = "item/commandExecution/requestApproval"
	for _, tc := range []struct {
		name   string
		id     any
		method string
		extra  map[string]any
		want   string // approval_refused, disconnected, dropped
	}{
		{"string-space", "a b", cmd, nil, "approval_refused"},
		{"string-huge", strings.Repeat("r", 1<<20), "item/fileChange/requestApproval", nil, "approval_refused"},
		{"float", json.RawMessage("1.5"), cmd, nil, "approval_refused"},
		{"negative-zero", json.RawMessage("-0"), cmd, map[string]any{"availableDecisions": []string{"accept", "decline"}}, "approval_refused"},
		{"no-decline-offered", "a b", cmd, map[string]any{"availableDecisions": []string{"accept"}}, "disconnected"},
		{"permissions", "a b", "item/permissions/requestApproval", nil, "disconnected"},
		{"missing", nil, cmd, nil, "dropped"},
		{"null", json.RawMessage("null"), cmd, nil, "dropped"},
		// No string or number: it cannot be answered. Dropped, it stayed
		// natively pending and unlisted; with no tracked turn of its own to
		// interrupt, the transport ends (adapter-drops; a tracked turn makes
		// it an observation_gap, native_drop_test.go).
		{"object", map[string]int{"n": 1}, cmd, nil, "ended"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, p, l := codexFixture(t)
			id := codexStart(t, c, l)
			f := approval(tc.id, tc.method, tc.extra)
			f["params"].(map[string]any)["threadId"] = id
			before := len(p.allWrites())
			p.frame(f)
			if tc.want != "disconnected" && tc.want != "ended" {
				// A barrier, sent only where the transport stays open: the
				// fake's channel is closed by a disconnect.
				p.notify("turn/started", map[string]any{"threadId": id, "turn": map[string]string{"id": "turn/one"}})
			}
			e := codexEvent(t, c)
			switch tc.want {
			case "dropped":
				if e.Kind != "busy" || len(p.allWrites()) != before {
					t.Fatalf("not dropped: %#v", e)
				}
			case "disconnected":
				if e.Kind != "disconnected" || !strings.Contains(e.Err, "request id outside the plain key set") {
					t.Fatalf("not ended: %#v", e)
				}
			case "ended":
				if e.Kind != "disconnected" || !strings.Contains(e.Err, "id is a JSON object, which cannot be answered") {
					t.Fatalf("not ended: %#v", e)
				}
			case "approval_refused":
				if e.Kind != "approval_refused" || e.Approval != nil || e.TurnID != "turn/one" || e.ItemID != "item/one" || !strings.Contains(e.Err, "declined natively") {
					t.Fatalf("not refused: %#v", e)
				}
				if len(e.Err) > 400 {
					t.Fatalf("refusal unbounded: %d bytes", len(e.Err))
				}
				writes := p.allWrites()
				var reply codexEnvelope
				_ = json.Unmarshal(writes[len(writes)-1], &reply)
				want, _ := json.Marshal(tc.id)
				if string(reply.ID) != string(want) || string(reply.Result) != `{"decision":"decline"}` {
					t.Fatalf("decline %s", writes[len(writes)-1])
				}
				if next := codexEvent(t, c); next.Kind != "busy" {
					t.Fatalf("worker not kept: %#v", next)
				}
				c.mu.Lock()
				n, closed := len(c.approvals), c.closed
				c.mu.Unlock()
				if n != 0 || closed {
					t.Fatalf("refused id kept pending (%d) or transport closed (%v)", n, closed)
				}
			}
		})
	}
}

// One request is spelled one way: a string id sent with \u escapes is the
// decoded key's JSON in the receipt, and still answers the request.
func TestCodexRequestIDCanonicalString(t *testing.T) {
	c, p, l := codexFixture(t)
	id := codexStart(t, c, l)
	p.lines <- json.RawMessage(`{"id":"\u0072eq/1","method":"item/commandExecution/requestApproval","params":{"threadId":"` + id + `","turnId":"turn/one","itemId":"item/one"}}`)
	e := codexEvent(t, c)
	if e.Kind != "approval" || string(e.Approval.ID) != `"req/1"` {
		t.Fatalf("approval %#v", e)
	}
	if err := c.Approve(context.Background(), ApprovalDecision{ID: e.Approval.ID, NativeSessionID: id, TurnID: "turn/one", Decision: "deny"}); err != nil {
		t.Fatal(err)
	}
	writes := p.allWrites()
	if !strings.Contains(string(writes[len(writes)-1]), `"id":"req/1"`) {
		t.Fatalf("answer %s", writes[len(writes)-1])
	}
}

// A Claude request id outside the key set: the F-07 native deny by the id
// as sent, approval_refused published, no tombstone, the worker kept. The
// subagent branch is refused the same way. A cancel by such an id stores
// nothing.
func TestClaudeRequestKeyRefusal(t *testing.T) {
	cases := hostileKeys()
	cases["empty"] = "" // was a disconnect before the key check (Codex code pass)
	for name, bad := range cases {
		for _, agent := range []string{"", "sub-1"} {
			t.Run(name+"/agent="+agent, func(t *testing.T) {
				c := claudeIDBackend()
				var wrote []any
				c.proc = &claudeFakeProcess{write: func(ctx context.Context, v any) error {
					if _, ok := ctx.Deadline(); !ok {
						t.Error("refusal written with no deadline")
					}
					wrote = append(wrote, v)
					return nil
				}}
				b, _ := json.Marshal(map[string]any{"subtype": "can_use_tool", "tool_name": "Bash", "tool_use_id": "toolu_1", "agent_id": agent, "input": map[string]any{"command": "true"}})
				raw, _ := json.Marshal(bad)
				if err := c.permission(raw, b); err != nil {
					t.Fatalf("worker ended: %v", err)
				}
				e := <-c.events
				if e.Kind != "approval_refused" || e.Approval != nil || e.TurnID != "turn" || e.NativeSessionID != "native" || !strings.Contains(e.Err, "id outside the plain key set") {
					t.Fatalf("not refused: %#v", e)
				}
				if len(wrote) != 1 {
					t.Fatalf("%d writes", len(wrote))
				}
				out, _ := json.Marshal(wrote[0])
				if !strings.Contains(string(out), `"behavior":"deny"`) || !strings.Contains(string(out), `"request_id":`+string(raw)) {
					t.Fatalf("deny %.200s", out)
				}
				if len(c.approvals) != 0 || len(c.seenApprovals) != 0 || c.closed {
					t.Fatalf("stored %d/%d or closed %v", len(c.approvals), len(c.seenApprovals), c.closed)
				}
				cancel, _ := json.Marshal(map[string]any{"type": "control_cancel_request", "request_id": bad})
				if err := c.frame(cancel); err != nil || len(c.seenApprovals) != 0 {
					t.Fatalf("cancel stored a tombstone: %v %d", err, len(c.seenApprovals))
				}
			})
		}
	}
	// Positive control: a UUID request id, as Claude sends, becomes an
	// approval spelled canonically even when sent with \u escapes.
	c := claudeIDBackend()
	b, _ := json.Marshal(map[string]any{"subtype": "can_use_tool", "tool_name": "Bash", "tool_use_id": "toolu_1", "input": map[string]any{"command": "true"}})
	if err := c.permission(json.RawMessage(`"\u0035f0c6a2e-8d1b-4c3a-9e7f-1a2b3c4d5e6f"`), b); err != nil {
		t.Fatal(err)
	}
	if e := <-c.events; e.Kind != "approval" || string(e.Approval.ID) != `"5f0c6a2e-8d1b-4c3a-9e7f-1a2b3c4d5e6f"` {
		t.Fatalf("positive control: %#v", e)
	}
}

// The controller records approval_refused as an observation: no gap, nothing
// pending changes, state unmoved; a malformed one is refused.
func TestControllerRecordsApprovalRefused(t *testing.T) {
	c, _, l, _ := controllerFixture(t)
	ctx := context.Background()
	before := c.state
	r, err := c.Observe(ctx, Event{Kind: "approval_refused", NativeSessionID: l.identity.SessionID, TurnID: "t1", Err: "Codex approval request id outside the plain key set (3 bytes); declined natively"})
	if err != nil || r.NativeEvent == nil || r.NativeEvent.Kind != "approval_refused" || c.observationGap != "" || c.state != before {
		t.Fatalf("receipt %#v err %v gap %q", r, err, c.observationGap)
	}
	for _, bad := range []Event{
		{Kind: "approval_refused", NativeSessionID: l.identity.SessionID},
		{Kind: "approval_refused", NativeSessionID: l.identity.SessionID, Err: "x", Approval: &Approval{ID: json.RawMessage(`1`)}},
	} {
		if _, err := c.Observe(ctx, bad); err == nil {
			t.Fatalf("malformed approval_refused accepted: %#v", bad)
		}
	}
}

// Resume refuses a captured session id outside the ledger's rule BEFORE any
// side effect: no ledger refresh, no spawn. The unedited receipt resumes (the
// positive control).
func TestControllerResumeRefusesCapturedSessionIDFirst(t *testing.T) {
	c, _, l, path := controllerFixture(t)
	c.Close()
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(original), `"session_id":"native-1"`, `"session_id":"a/b"`, 1)
	if edited == string(original) {
		t.Fatalf("fixture receipt has no session id to edit: %s", original)
	}
	if err := os.WriteFile(path, []byte(edited), 0600); err != nil {
		t.Fatal(err)
	}
	b2 := &fakeBackend{}
	c2, err := NewController(b2, l, path)
	if err != nil {
		t.Fatal(err)
	}
	events := len(l.events)
	if _, err := c2.Resume(context.Background()); err == nil || !strings.Contains(err.Error(), "outside the session-id rule") {
		t.Fatalf("resume admitted: %v", err)
	}
	if len(l.events) != events {
		t.Fatalf("ledger touched before the refusal: %v", l.events[events:])
	}
	c2.Close()
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	c3, err := NewController(&fakeBackend{}, l, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c3.Close()
	if _, err := c3.Resume(context.Background()); err != nil {
		t.Fatalf("positive control: %v", err)
	}
}

// A delivery journalled with a raw turn id (an older adapter) is deduplicated
// exactly but shown through displayNativeID when a repeated send returns it.
func TestControllerRepublishedDeliveryIsFenced(t *testing.T) {
	c, b, l, _ := controllerFixture(t)
	b.sendTurn = "t‮" + strings.Repeat("x", 1<<20)
	q := Request{Schema: Schema, Operation: "send", Identity: l.identity, MessageID: 7}
	if _, err := c.Handle(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	r, err := c.Handle(context.Background(), q)
	if err != nil || b.sends != 1 {
		t.Fatalf("dedupe: %v sends %d", err, b.sends)
	}
	if got := r.Delivery.TurnID; len(got) > 128+len("…[truncated]") || strings.ContainsRune(got, '‮') {
		t.Fatalf("republished turn id raw: %d bytes", len(got))
	}
	if c.sent[7].TurnID != b.sendTurn {
		t.Fatal("deduplication record altered")
	}
	// Positive control: an ordinary turn id is shown unchanged.
	b.sendTurn = ""
	q.MessageID = 8
	_, _ = c.Handle(context.Background(), q)
	if r, _ := c.Handle(context.Background(), q); r.Delivery.TurnID != "turn-1" {
		t.Fatalf("ordinary turn id altered: %q", r.Delivery.TurnID)
	}
}

// The wake text goes into the worker's prompt and is typed back as an
// argument: a session id outside the ledger's rule is never printed raw. It
// is unreachable through Start and resume (both refuse such an id), so the
// identity is set directly; the fixture's own id is the positive control.
func TestControllerWakeTextPrintsOnlyARuledSessionID(t *testing.T) {
	c, b, l, _ := controllerFixture(t)
	q := Request{Schema: Schema, Operation: "send", Identity: l.identity, MessageID: 7}
	if _, err := c.Handle(context.Background(), q); err != nil || !strings.Contains(b.lastText, "--session native-1 ") {
		t.Fatalf("positive control: %v %q", err, b.lastText)
	}
	bad := "a b\u202e"
	l.identity.SessionID = bad
	c.saved.Identity.SessionID = bad
	q = Request{Schema: Schema, Operation: "send", Identity: l.identity, MessageID: 8}
	if _, err := c.Handle(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.lastText, bad) || !strings.Contains(b.lastText, "--session @unprintable-session-id ") {
		t.Fatalf("raw session id in the wake text: %q", b.lastText)
	}
}

// A native refusal that could not be written is never reported as made: no
// approval_refused saying "declined natively", and the transport ends (the
// real WriteContext closes it on a failed write). The written refusal carries
// a deadline, so a server that stops reading cannot hold the reader.
func TestCodexRefusalWriteFailureIsNotReported(t *testing.T) {
	// "fails" closes as a failed Encode does; "times-out" does not, as a
	// deadline spent waiting for the write lock does not (re-verify N1).
	for _, mode := range []string{"control", "fails", "times-out"} {
		fail := mode != "control"
		t.Run(mode, func(t *testing.T) {
			c, p, l := codexFixture(t)
			id := codexStart(t, c, l)
			p.writeContext = func(ctx context.Context, v any) error {
				if _, ok := ctx.Deadline(); !ok {
					t.Error("refusal written with no deadline")
				}
				switch mode {
				case "fails":
					_ = p.Close()
					return errors.New("native input write failed")
				case "times-out":
					return context.DeadlineExceeded
				}
				return p.Write(v)
			}
			p.frame(map[string]any{"id": "a b", "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": id, "turnId": "turn/one", "itemId": "item/one"}})
			e := codexEvent(t, c)
			if fail && (e.Kind != "disconnected" || strings.Contains(e.Err, "declined natively")) {
				t.Fatalf("unwritten decline reported: %#v", e)
			}
			if fail && !p.closed() {
				t.Fatal("transport left open over an unanswered request")
			}
			if !fail && e.Kind != "approval_refused" {
				t.Fatalf("positive control: %#v", e)
			}
		})
	}
}

func TestClaudeRefusalWriteFailureIsNotReported(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "control", true: "fails"}[fail], func(t *testing.T) {
			c := claudeIDBackend()
			c.proc = &claudeFakeProcess{write: func(context.Context, any) error {
				if fail {
					return errors.New("native input write failed")
				}
				return nil
			}}
			b, _ := json.Marshal(map[string]any{"subtype": "can_use_tool", "tool_name": "Bash", "tool_use_id": "toolu_1", "input": map[string]any{"command": "true"}})
			err := c.permission(json.RawMessage(`"a b"`), b)
			if fail != (err != nil) {
				t.Fatalf("fail=%v err=%v", fail, err)
			}
			// null decodes into a string with no error: still malformed, not
			// denied as an empty id (re-verify N3), and its cancel refused too.
			if err := c.permission(json.RawMessage(` null`), b); err == nil {
				t.Fatal("null request id admitted as a string")
			}
			if err := c.frame(json.RawMessage(`{"type":"control_cancel_request","request_id":null}`)); err == nil {
				t.Fatal("null cancel id ignored as a string")
			}
			select {
			case e := <-c.events:
				if fail {
					t.Fatalf("unwritten deny reported: %#v", e)
				}
			default:
				if !fail {
					t.Fatal("positive control: no approval_refused")
				}
			}
		})
	}
}

type turnReaderBackend struct{ *fakeBackend }

func (b turnReaderBackend) ReadTurn(_ context.Context, session, turn string) (TurnObservation, error) {
	return TurnObservation{NativeSessionID: session, TurnID: turn, Status: Observation{State: "unknown"}, Source: "native_history"}, nil
}

// A reconcile journals the EXACT delivery, not the display copy it returns:
// reload copies the journalled delivery into c.sent, and a display copy there
// was the turn id the next reconcile compared, and refused (Fable cross-check,
// F1). The plain turn is the positive control.
func TestControllerReconcileJournalsExactDelivery(t *testing.T) {
	for _, turn := range []string{"turn-plain", "turn one\u202e" + strings.Repeat("x", 200)} {
		t.Run(fmt.Sprint(len(turn)), func(t *testing.T) {
			c, b, l, path := controllerFixture(t)
			b.sendTurn = turn
			if _, err := c.Handle(context.Background(), Request{Schema: Schema, Operation: "send", Identity: l.identity, MessageID: 7}); err != nil {
				t.Fatal(err)
			}
			c.Close()
			for round := 0; round < 2; round++ {
				c2, err := NewController(turnReaderBackend{&fakeBackend{}}, l, path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := c2.Resume(context.Background()); err != nil {
					t.Fatal(err)
				}
				if c2.sent[7].TurnID != turn {
					t.Fatalf("round %d: journalled turn id altered: %d bytes", round, len(c2.sent[7].TurnID))
				}
				r, err := c2.Handle(context.Background(), Request{Schema: Schema, Operation: "reconcile", Identity: l.identity, MessageID: 7, TurnID: turn})
				if err != nil {
					t.Fatalf("round %d: %v", round, err)
				}
				if r.Delivery.TurnID != displayNativeID(turn) || r.TurnObservation.TurnID != displayNativeID(turn) {
					t.Fatalf("round %d: receipt not presented: %q", round, r.Delivery.TurnID)
				}
				c2.Close()
			}
			// An early error return presents the delivery too (re-verify
			// N2): no TurnReader, so reconcile fails after reading it.
			c3, err := NewController(&fakeBackend{}, l, path)
			if err != nil {
				t.Fatal(err)
			}
			defer c3.Close()
			if _, err := c3.Resume(context.Background()); err != nil {
				t.Fatal(err)
			}
			r, err := c3.Handle(context.Background(), Request{Schema: Schema, Operation: "reconcile", Identity: l.identity, MessageID: 7})
			if err == nil || r.Delivery == nil || r.Delivery.TurnID != displayNativeID(turn) {
				t.Fatalf("error receipt: %v %#v", err, r.Delivery)
			}
		})
	}
}
