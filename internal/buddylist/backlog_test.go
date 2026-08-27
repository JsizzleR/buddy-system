package buddylist

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testJournal(t *testing.T) *Journal {
	t.Helper()
	clock := time.Date(2026, 8, 22, 11, 0, 0, 0, time.UTC)
	j, err := OpenJournal(filepath.Join(t.TempDir(), "j.db"), func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	return j
}

func seqsOf(msgs []Msg) []int64 {
	out := make([]int64, len(msgs))
	for i, m := range msgs {
		out[i] = m.Seq
	}
	return out
}

func fill(t *testing.T, j *Journal, room string, n int) []int64 {
	t.Helper()
	var seqs []int64
	for i := 1; i <= n; i++ {
		s, err := j.Append(room, "peer", "chat", fmt.Sprintf("msg %d", i))
		if err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, s)
	}
	return seqs
}

// The whole point of tail: reaching the newest rows without walking the
// middle. The ORDER of the result is separately load-bearing — every renderer
// prints oldest-first, so a tail that handed back its DESC scan verbatim would
// print the room backwards.
func TestJournalTailReturnsNewestRowsOldestFirst(t *testing.T) {
	j := testJournal(t)
	seqs := fill(t, j, "lobby", 10)
	msgs, gap, err := j.Read(ReadOpts{Room: "lobby", Tail: 3})
	if err != nil || gap {
		t.Fatalf("tail read: err=%v gap=%v", err, gap)
	}
	want := seqs[7:]
	if got := seqsOf(msgs); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("tail=3 must be the newest three, ascending: got %v want %v", got, want)
	}
}

func TestJournalBeforeWalksBackwards(t *testing.T) {
	j := testJournal(t)
	seqs := fill(t, j, "lobby", 10)
	msgs, _, err := j.Read(ReadOpts{Room: "lobby", Before: seqs[7], Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	want := seqs[4:7] // the three rows immediately before seqs[7]
	if got := seqsOf(msgs); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("before must take the rows just BELOW it: got %v want %v", got, want)
	}
	// Walking back off the end is empty, not an error and not a wrap-around.
	if msgs, _, err = j.Read(ReadOpts{Room: "lobby", Before: seqs[0]}); err != nil || len(msgs) != 0 {
		t.Fatalf("before the oldest row must be empty: %v %v", seqsOf(msgs), err)
	}
}

// A room a session shares with a peer whose messages are not for it: the
// filter is the difference between reading 3 lines and reading 1900.
func TestJournalMentionFilterMatchesNamesCaseInsensitively(t *testing.T) {
	j := testJournal(t)
	for _, body := range []string{
		"broadcast: landing soon",
		"@s-abc12 you hold internal/router",
		"unrelated chatter",
		"S-ABC12 again, shout if you object",
	} {
		if _, err := j.Append("lobby", "peer", "chat", body); err != nil {
			t.Fatal(err)
		}
	}
	msgs, _, err := j.Read(ReadOpts{Room: "lobby", Mentions: []string{"s-abc12"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("want the two addressed rows (case-insensitively), got %d: %v", len(msgs), seqsOf(msgs))
	}
	if !strings.Contains(msgs[1].Body, "S-ABC12") {
		t.Fatalf("the upper-case spelling must match too: %+v", msgs)
	}
}

// LIKE's wildcards are DATA here. An unescaped "%" would widen the filter into
// an unfiltered read — the exact failure the filter exists to prevent, wearing
// a green light.
func TestJournalMentionWildcardsAreLiteral(t *testing.T) {
	j := testJournal(t)
	// Each decoy is reachable ONLY by reading the token's wildcard as a
	// wildcard: "a%b" spans "alpha beta", "a_c" spans "abc". A token whose
	// literal spelling is the only match would pass with no escaping at all.
	for _, body := range []string{"alpha beta gamma", "an a%b literal", "abc", "a_c literal"} {
		if _, err := j.Append("lobby", "peer", "chat", body); err != nil {
			t.Fatal(err)
		}
	}
	msgs, _, err := j.Read(ReadOpts{Room: "lobby", Mentions: []string{"a%b"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || !strings.Contains(msgs[0].Body, "a%b literal") {
		t.Fatalf("%% in a token must be literal, not a wildcard: %d rows %v", len(msgs), seqsOf(msgs))
	}
	if msgs, _, err = j.Read(ReadOpts{Room: "lobby", Mentions: []string{"a_c"}}); err != nil || len(msgs) != 1 {
		t.Fatalf("_ in a token must be literal: %d rows (err=%v)", len(msgs), err)
	}
	if !strings.Contains(msgs[0].Body, "a_c literal") {
		t.Fatalf("_ matched the wrong row: %+v", msgs[0])
	}
}

func TestJournalShortMentionTokenRefused(t *testing.T) {
	j := testJournal(t)
	fill(t, j, "lobby", 3)
	// Clamping instead of refusing would return every row while claiming to
	// be filtered — worse than an error, because the caller would believe it.
	if _, _, err := j.Read(ReadOpts{Room: "lobby", Mentions: []string{"ab"}}); err == nil {
		t.Fatal("a 2-character mention token must be refused, not silently matched")
	}
	// Every token here is long enough to clear the floor, so the COUNT cap is
	// what has to refuse this — a leg built from one-character tokens would
	// be answered by the floor and never reach it.
	// The count is a LITERAL nine, not maxMentionTokens+1: derived from the
	// constant, this leg would move with any change to it and assert nothing.
	// Nine tokens pins the cap at eight; changing the cap changes this line.
	many := []string{"token-01", "token-02", "token-03", "token-04", "token-05",
		"token-06", "token-07", "token-08", "token-09"}
	if _, _, err := j.Read(ReadOpts{Room: "lobby", Mentions: many}); err == nil {
		t.Fatal("nine mention tokens must be refused: the cap is eight")
	}
}

func TestJournalCursorIsPerSessionAndMonotonic(t *testing.T) {
	j := testJournal(t)
	seqs := fill(t, j, "lobby", 5)
	if cur, err := j.Cursor("alpha", "lobby"); err != nil || cur != 0 {
		t.Fatalf("an unknown session starts at 0: %d %v", cur, err)
	}
	if cur, err := j.SetCursor("alpha", "lobby", seqs[2]); err != nil || cur != seqs[2] {
		t.Fatalf("set: %d %v", cur, err)
	}
	// A second session is unaffected: sharing a cursor would let one session
	// mark another's backlog read.
	if cur, _ := j.Cursor("beta", "lobby"); cur != 0 {
		t.Fatalf("cursors must be per session, beta saw %d", cur)
	}
	// Backwards is a no-op, and the caller is told the value that stands.
	cur, err := j.SetCursor("alpha", "lobby", seqs[0])
	if err != nil || cur != seqs[2] {
		t.Fatalf("a rewind must be declined and reported: got %d want %d (%v)", cur, seqs[2], err)
	}
	// Per room, too.
	if cur, _ := j.Cursor("alpha", "ops"); cur != 0 {
		t.Fatalf("a cursor is per room, ops saw %d", cur)
	}
	if _, err := j.SetCursor("", "lobby", 1); err == nil {
		t.Fatal("a cursor with no session id must be refused, never shared")
	}
}

func TestJournalStatCountsUnreadAndAddressed(t *testing.T) {
	j := testJournal(t)
	fill(t, j, "lobby", 4)
	if _, err := j.Append("lobby", "peer", "chat", "@alpha this one is yours"); err != nil {
		t.Fatal(err)
	}
	fill(t, j, "ops", 2)
	if _, err := j.SetCursor("alpha", "lobby", 2); err != nil {
		t.Fatal(err)
	}

	stats, err := j.Stat("alpha", []string{"alpha"})
	if err != nil {
		t.Fatal(err)
	}
	byRoom := map[string]RoomStat{}
	for _, s := range stats {
		byRoom[s.Room] = s
	}
	lobby := byRoom["lobby"]
	if lobby.Unread != 3 || lobby.Addressed != 1 || lobby.NewestSeq != 5 || lobby.Cursor != 2 {
		t.Fatalf("lobby stat wrong: %+v", lobby)
	}
	if ops := byRoom["ops"]; ops.Unread != 2 || ops.Cursor != 0 || ops.Addressed != 0 {
		t.Fatalf("ops stat wrong: %+v", ops)
	}
	// No session: unread is the whole retained room, and it must say so by
	// reporting the total rather than a backlog nobody has.
	nosess, err := j.Stat("", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range nosess {
		if s.Unread != s.Retained {
			t.Fatalf("with no cursor, unread must equal retained: %+v", s)
		}
	}
}

// ---- MCP level ----

func TestMCPToolsListNamesEveryTool(t *testing.T) {
	// Named, not counted: a count cannot see one tool substituted for another.
	want := map[string]bool{"chat_send": true, "chat_read": true, "chat_status": true,
		"chat_ack": true, "chat_who": true, "dm": true, "set_status": true}
	got := map[string]bool{}
	for _, tool := range mcpTools {
		got[tool["name"].(string)] = true
	}
	for name := range want {
		if !got[name] {
			t.Errorf("tool %q is missing from tools/list", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("unexpected tool %q — add it here deliberately", name)
		}
	}
}

// bigMsgs builds n rows fat enough that the byte budget cannot hold them all.
func bigMsgs(n int) []Msg {
	msgs := make([]Msg, 0, n)
	for i := 1; i <= n; i++ {
		msgs = append(msgs, Msg{Seq: int64(i), Room: "lobby", Sender: "peer", Kind: "chat",
			Body: fmt.Sprintf("m%03d ", i) + strings.Repeat("x", 1500), At: 1755216000})
	}
	return msgs
}

// The directional property: a forward page drops the NEWEST rows (so its
// cursor never steps over one), a tail page drops the OLDEST (so it answers
// the question it was asked). Getting this backwards makes tail useless while
// still returning rows.
func TestMCPTailDropsOldestUnderByteBudgetForwardDropsNewest(t *testing.T) {
	deps := MCPDeps{Call: func(req Request, _ time.Duration) (Response, error) {
		if req.Op == "read" {
			return Response{OK: true, Msgs: bigMsgs(20)}, nil
		}
		return Response{OK: true}, nil
	}}
	call := func(argsJSON string) string {
		resps := driveMCP(t, deps,
			`{"jsonrpc":"2.0","id":100,"method":"initialize","params":{}}`,
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"chat_read","arguments":`+argsJSON+`}}`)
		text, isErr := toolText(t, resps[1])
		if isErr {
			t.Fatalf("unexpected error result: %s", text)
		}
		return text
	}

	tail := call(`{"room":"lobby","tail":20}`)
	if !strings.Contains(tail, "m020") {
		t.Fatalf("a tail read that drops the NEWEST row answers the wrong question:\n%s", firstLines(tail))
	}
	if strings.Contains(tail, "m001") {
		t.Fatal("the whole set fits — this test proves nothing; make the rows bigger")
	}
	if !strings.Contains(tail, "OLDER rows omitted") || !strings.Contains(tail, "before=") {
		t.Fatalf("a truncated tail must hand back a backwards pointer:\n%s", firstLines(tail))
	}

	fwd := call(`{"room":"lobby"}`)
	if !strings.Contains(fwd, "m001") || strings.Contains(fwd, "m020") {
		t.Fatalf("a forward page must keep the OLDEST rows so its cursor skips nothing:\n%s", firstLines(fwd))
	}
	if !strings.Contains(fwd, "newer rows omitted") {
		t.Fatalf("forward truncation notice missing:\n%s", firstLines(fwd))
	}
}

func firstLines(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) > 6 {
		lines = lines[:6]
	}
	return strings.Join(lines, "\n")
}

// since_last must save exactly what it SHOWED. Acking the newest row the
// daemon returned — rather than the newest row that survived the byte budget
// — would drop the truncated remainder permanently.
func TestMCPSinceLastAcksOnlyWhatWasRendered(t *testing.T) {
	var acked Request
	deps := MCPDeps{
		SessionID: func() string { return "sess-1" },
		Label:     func() string { return "repo/s-1" },
		Call: func(req Request, _ time.Duration) (Response, error) {
			switch req.Op {
			case "read":
				if !req.SinceLast || req.Session != "sess-1" {
					t.Fatalf("since_last must reach the daemon with the session: %+v", req)
				}
				return Response{OK: true, Msgs: bigMsgs(20), Cursor: 0}, nil
			case "ack":
				acked = req
				return Response{OK: true, Cursor: req.Seq}, nil
			}
			return Response{OK: true}, nil
		},
	}
	resps := driveMCP(t, deps,
		`{"jsonrpc":"2.0","id":100,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"chat_read","arguments":{"room":"lobby","since_last":true}}}`)
	text, isErr := toolText(t, resps[1])
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if acked.Op != "ack" || acked.Room != "lobby" {
		t.Fatalf("since_last must save a cursor: %+v", acked)
	}
	if !strings.Contains(text, fmt.Sprintf("after=%d", acked.Seq)) {
		t.Fatalf("the saved cursor and the reported cursor must agree:\n%s", firstLines(text))
	}
	if acked.Seq >= 20 {
		t.Fatalf("acked seq %d includes rows the byte budget dropped", acked.Seq)
	}
	if !strings.Contains(text, "m001") {
		t.Fatalf("since_last is a forward read; it must start at the oldest unread:\n%s", firstLines(text))
	}
}

func TestMCPSinceLastNeedsIdentityAndAckFailureIsReported(t *testing.T) {
	// No identity: refuse rather than key a cursor to "" that every
	// unidentified session would then share.
	resps := driveMCP(t, MCPDeps{Call: func(Request, time.Duration) (Response, error) {
		t.Fatal("must not reach the daemon without an identity")
		return Response{}, nil
	}},
		`{"jsonrpc":"2.0","id":100,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"chat_read","arguments":{"room":"lobby","since_last":true}}}`)
	if text, isErr := toolText(t, resps[1]); !isErr || !strings.Contains(text, "session identity") {
		t.Fatalf("want an identity error, got isErr=%v %q", isErr, text)
	}

	deps := MCPDeps{
		SessionID: func() string { return "sess-1" },
		Call: func(req Request, _ time.Duration) (Response, error) {
			if req.Op == "ack" {
				return Response{}, errFake("journal is read-only")
			}
			return Response{OK: true, Msgs: []Msg{{Seq: 9, Room: "lobby", Sender: "p", Kind: "chat", Body: "hi", At: 1755216000}}}, nil
		},
	}
	resps = driveMCP(t, deps,
		`{"jsonrpc":"2.0","id":100,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"chat_read","arguments":{"room":"lobby","since_last":true}}}`)
	text, _ := toolText(t, resps[1])
	if !strings.Contains(text, "NOT saved") {
		t.Fatalf("a failed cursor save must be reported, never swallowed:\n%s", text)
	}
	if !strings.Contains(text, "hi") {
		t.Fatal("the messages themselves must still be delivered")
	}
}

func TestMCPMentionsMeDerivesTokensFromIdentity(t *testing.T) {
	var got Request
	deps := MCPDeps{
		SessionID: func() string { return "e284b102-c678-4fce" },
		Label:     func() string { return "bastle/s-e284b102" },
		Call: func(req Request, _ time.Duration) (Response, error) {
			got = req
			return Response{OK: true}, nil
		},
	}
	driveMCP(t, deps,
		`{"jsonrpc":"2.0","id":100,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"chat_read","arguments":{"room":"lobby","mentions_me":true,"mentions":["b81-inspect-markers"]}}}`)
	want := []string{"b81-inspect-markers", "e284b102-c678-4fce", "bastle/s-e284b102", "s-e284b102"}
	if fmt.Sprint(got.Mentions) != fmt.Sprint(want) {
		t.Fatalf("mention tokens: got %v want %v", got.Mentions, want)
	}
}

func TestMCPMentionsMeWithoutIdentityIsAnActionableError(t *testing.T) {
	deps := MCPDeps{Call: func(Request, time.Duration) (Response, error) { return Response{OK: true}, nil }}
	resps := driveMCP(t, deps,
		`{"jsonrpc":"2.0","id":100,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"chat_read","arguments":{"room":"lobby","mentions_me":true}}}`)
	text, isErr := toolText(t, resps[1])
	if !isErr || !strings.Contains(text, "mentions=") {
		t.Fatalf("want an error naming the way out, got isErr=%v %q", isErr, text)
	}
}

func TestMCPChatStatusIsCountsNotContent(t *testing.T) {
	deps := MCPDeps{
		SessionID: func() string { return "sess-1" },
		Label:     func() string { return "repo/s-1" },
		Call: func(req Request, _ time.Duration) (Response, error) {
			if req.Op != "stat" || req.Session != "sess-1" {
				t.Fatalf("bad stat request: %+v", req)
			}
			return Response{OK: true, Stats: []RoomStat{
				{Room: "ops", NewestSeq: 12, NewestAt: 1755216000, Unread: 0},
				{Room: "lobby", NewestSeq: 1923, NewestAt: 1755216000, Unread: 12, Addressed: 2},
				{Room: "", NewestSeq: 88, NewestAt: 1755216000, Unread: 1, Gap: true},
			}}, nil
		},
	}
	resps := driveMCP(t, deps,
		`{"jsonrpc":"2.0","id":100,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"chat_status","arguments":{}}}`)
	text, isErr := toolText(t, resps[1])
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	// Busiest first, so a digest reads top-down. Asserted over all three
	// rooms: newest-first and alphabetical agree on lobby-before-ops and
	// disagree only on where the system room lands, so checking two rooms
	// would pass under either rule.
	lines := strings.Split(text, "\n")
	li, si, oi := indexOfLine(lines, "lobby"), indexOfLine(lines, "(system)"), indexOfLine(lines, "ops")
	if li < 0 || si < 0 || oi < 0 {
		t.Fatalf("every room must be listed:\n%s", text)
	}
	if !(li < si && si < oi) {
		t.Fatalf("rooms must be ordered newest-first (lobby 1923, system 88, ops 12):\n%s", text)
	}
	// The room argument narrows; without it the digest is every room.
	resps = driveMCP(t, deps,
		`{"jsonrpc":"2.0","id":100,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"chat_status","arguments":{"room":"ops"}}}`)
	one, _ := toolText(t, resps[1])
	if !strings.Contains(one, "ops") || strings.Contains(one, "lobby") {
		t.Fatalf("room=ops must report ops and only ops:\n%s", one)
	}
	for _, want := range []string{"1923", "12", "(system)", "GAP", "since_last"} {
		if !strings.Contains(text, want) {
			t.Fatalf("status missing %q:\n%s", want, text)
		}
	}
}

func indexOfLine(lines []string, prefix string) int {
	for i, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return i
		}
	}
	return -1
}

func TestMCPChatAckReportsARefusedRewind(t *testing.T) {
	deps := MCPDeps{
		SessionID: func() string { return "sess-1" },
		Call: func(req Request, _ time.Duration) (Response, error) {
			if req.Op != "ack" || req.Seq != 5 || req.Session != "sess-1" {
				t.Fatalf("bad ack: %+v", req)
			}
			return Response{OK: true, Cursor: 40}, nil // already further along
		},
	}
	resps := driveMCP(t, deps,
		`{"jsonrpc":"2.0","id":100,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"chat_ack","arguments":{"room":"lobby","seq":5}}}`)
	text, isErr := toolText(t, resps[1])
	if isErr {
		t.Fatalf("a declined rewind is a fact, not a failure: %s", text)
	}
	if !strings.Contains(text, "40") || !strings.Contains(text, "not applied") {
		t.Fatalf("the cursor that STANDS must be reported, not the one asked for:\n%s", text)
	}
}

// ---- socket dispatch ----

// The refusals that keep a cursor honest. Each combination would advance the
// cursor over rows the caller was never shown, and those rows are then
// unreachable BY THAT CURSOR forever — so the daemon refuses instead of
// quietly picking a winner between the two selections.
func TestDispatchSinceLastRefusesWindowsThatWouldSkip(t *testing.T) {
	h := start(t)
	for _, seq := range []int64{1, 2, 3} {
		if _, err := h.j.Append("lobby", "peer", "chat", fmt.Sprintf("m%d", seq)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		req  Request
		want string
	}{
		{"no session", Request{Op: "read", Room: "lobby", SinceLast: true}, "session id"},
		{"with after", Request{Op: "read", Room: "lobby", SinceLast: true, Session: "s", After: 2}, "pass one"},
		{"with tail", Request{Op: "read", Room: "lobby", SinceLast: true, Session: "s", Tail: 2}, "before/tail"},
		{"with before", Request{Op: "read", Room: "lobby", SinceLast: true, Session: "s", Before: 3}, "before/tail"},
		{"with mentions", Request{Op: "read", Room: "lobby", SinceLast: true, Session: "s", Mentions: []string{"alpha"}}, "mentions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.call(t, tc.req)
			if err == nil {
				t.Fatal("must be refused")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refusal must name the conflict: %v", err)
			}
		})
	}
	// The control: since_last alone works, and it reports the cursor it used
	// rather than the one the caller did not pass.
	if _, err := h.j.SetCursor("s", "lobby", 2); err != nil {
		t.Fatal(err)
	}
	resp, err := h.call(t, Request{Op: "read", Room: "lobby", SinceLast: true, Session: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Cursor != 2 || len(resp.Msgs) != 1 || resp.Msgs[0].Body != "m3" {
		t.Fatalf("since_last must start at the saved cursor: cursor=%d msgs=%+v", resp.Cursor, resp.Msgs)
	}
}

func TestDispatchStatAndAckRoundTrip(t *testing.T) {
	h := start(t)
	for i := 1; i <= 4; i++ {
		if _, err := h.j.Append("lobby", "peer", "chat", fmt.Sprintf("msg-%03d", i)); err != nil {
			t.Fatal(err)
		}
	}
	resp, err := h.call(t, Request{Op: "ack", Room: "lobby", Session: "s", Seq: 3})
	if err != nil || resp.Cursor != 3 {
		t.Fatalf("ack: cursor=%d err=%v", resp.Cursor, err)
	}
	resp, err = h.call(t, Request{Op: "stat", Session: "s", Mentions: []string{"msg-004"}})
	if err != nil {
		t.Fatal(err)
	}
	var lobby *RoomStat
	for i := range resp.Stats {
		if resp.Stats[i].Room == "lobby" {
			lobby = &resp.Stats[i]
		}
	}
	if lobby == nil || lobby.Unread != 1 || lobby.Addressed != 1 || lobby.Cursor != 3 {
		t.Fatalf("stat after ack: %+v", lobby)
	}
	if _, err := h.call(t, Request{Op: "ack", Room: "lobby", Seq: 3}); err == nil {
		t.Fatal("ack without a session must be refused")
	}
}

// The footer is an instruction, so it may not name a mode this caller cannot
// use: with no identity since_last refuses, and pointing at it would
// contradict the header the same result just printed.
func TestMCPChatStatusFooterMatchesWhatThisSessionCanDo(t *testing.T) {
	stat := func(id string) string {
		deps := MCPDeps{
			SessionID: func() string { return id },
			Call: func(Request, time.Duration) (Response, error) {
				return Response{OK: true, Stats: []RoomStat{{Room: "lobby", NewestSeq: 5, Unread: 5}}}, nil
			},
		}
		resps := driveMCP(t, deps,
			`{"jsonrpc":"2.0","id":100,"method":"initialize","params":{}}`,
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"chat_status","arguments":{}}}`)
		text, _ := toolText(t, resps[1])
		return text
	}
	if text := stat(""); strings.Contains(text, "since_last") {
		t.Fatalf("no identity: the footer must not point at a mode that would refuse:\n%s", text)
	}
	if text := stat("sess-1"); !strings.Contains(text, "since_last") {
		t.Fatalf("with an identity, since_last is the cheap route and must be named:\n%s", text)
	}
}
