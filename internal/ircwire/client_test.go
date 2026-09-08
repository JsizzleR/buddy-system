package ircwire

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/tocwire"
)

// fakeServer is a scripted IRC server on a real loopback listener.
type fakeServer struct {
	t    *testing.T
	ln   net.Listener
	mu   sync.Mutex
	got  []string
	conn net.Conn
	br   *bufio.Reader
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return &fakeServer{t: t, ln: ln}
}

func (f *fakeServer) addr() string { return f.ln.Addr().String() }

// acceptAndRegister consumes NICK/USER and answers 001.
func (f *fakeServer) acceptAndRegister() {
	f.t.Helper()
	conn, err := f.ln.Accept()
	if err != nil {
		f.t.Error(err)
		return
	}
	f.conn = conn
	f.br = bufio.NewReader(conn)
	sawCap, sawUser := false, false
	for !sawUser {
		line := f.readLine()
		if strings.HasPrefix(line, "CAP REQ") {
			sawCap = true
		}
		if strings.HasPrefix(line, "USER ") {
			sawUser = true
		}
	}
	if !sawCap {
		f.t.Error("client must request echo-message")
	}
	f.send(":buddy.local CAP SmarterChild ACK :echo-message")
	if end := f.readLine(); end != "CAP END" {
		f.t.Errorf("client must CAP END after ACK, got %q", end)
	}
	f.send(":buddy.local 001 SmarterChild :Welcome to the BuddySystem IRC Network SmarterChild")
}

func (f *fakeServer) readLine() string {
	f.t.Helper()
	f.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := f.br.ReadString('\n')
	if err != nil {
		f.t.Fatalf("fake server read: %v", err)
	}
	line = strings.TrimRight(line, "\r\n")
	f.mu.Lock()
	f.got = append(f.got, line)
	f.mu.Unlock()
	return line
}

// tryReadLine is readLine for an assertion that nothing arrives: it returns
// false on timeout instead of failing the test.
func (f *fakeServer) tryReadLine(d time.Duration) (string, bool) {
	f.t.Helper()
	f.conn.SetReadDeadline(time.Now().Add(d))
	line, err := f.br.ReadString('\n')
	if err != nil {
		return "", false
	}
	line = strings.TrimRight(line, "\r\n")
	f.mu.Lock()
	f.got = append(f.got, line)
	f.mu.Unlock()
	return line, true
}

func (f *fakeServer) send(lines ...string) {
	f.t.Helper()
	for _, l := range lines {
		if _, err := f.conn.Write([]byte(l + "\r\n")); err != nil {
			f.t.Fatalf("fake server write: %v", err)
		}
	}
}

func dialOK(t *testing.T, f *fakeServer) *Client {
	t.Helper()
	done := make(chan struct{})
	go func() { f.acceptAndRegister(); close(done) }()
	c, err := Dial(context.Background(), f.addr(), "SmarterChild", "", WithKeepAlive(0))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	<-done
	return c
}

func wantEvent(t *testing.T, c *Client) tocwire.Event {
	t.Helper()
	select {
	case ev, ok := <-c.Events():
		if !ok {
			t.Fatalf("events closed: %v", c.Err())
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
	}
	return nil
}

func TestRegistrationAnswersPing(t *testing.T) {
	f := newFakeServer(t)
	go func() {
		conn, err := f.ln.Accept()
		if err != nil {
			t.Error(err)
			return
		}
		f.conn = conn
		f.br = bufio.NewReader(conn)
		for line := f.readLine(); !strings.HasPrefix(line, "USER "); line = f.readLine() {
		}
		f.send("PING :challenge")
		pong := f.readLine()
		if pong != "PONG :challenge" {
			t.Errorf("registration must answer PING, got %q", pong)
		}
		f.send(":buddy.local 001 SmarterChild :Welcome")
	}()
	c, err := Dial(context.Background(), f.addr(), "SmarterChild", "", WithKeepAlive(0))
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestNickCollisionIsAnError(t *testing.T) {
	f := newFakeServer(t)
	go func() {
		conn, _ := f.ln.Accept()
		f.conn = conn
		f.br = bufio.NewReader(conn)
		for line := f.readLine(); !strings.HasPrefix(line, "USER "); line = f.readLine() {
		}
		f.send(":buddy.local 433 * SmarterChild :Nickname is already in use")
	}()
	if _, err := Dial(context.Background(), f.addr(), "SmarterChild", "", WithKeepAlive(0)); err == nil {
		t.Fatal("433 must be a dial error (daemon backoff retries it)")
	} else if !strings.Contains(err.Error(), "433") {
		t.Fatalf("error should carry the numeric: %v", err)
	}
}

func TestJoinNamesAndMessages(t *testing.T) {
	f := newFakeServer(t)
	c := dialOK(t, f)

	if err := c.ChatJoin("lobby"); err != nil {
		t.Fatal(err)
	}
	if got := f.readLine(); got != "JOIN #lobby" {
		t.Fatalf("bare room name must get its #: %q", got)
	}
	f.send(
		":SmarterChild!u@h JOIN #lobby",
		":buddy.local 353 SmarterChild = #lobby :@operator SmarterChild",
		":buddy.local 366 SmarterChild #lobby :End of NAMES",
	)
	ev := wantEvent(t, c)
	join, ok := ev.(tocwire.ChatJoin)
	if !ok || join.RoomID != "#lobby" || join.Room != "lobby" {
		t.Fatalf("own JOIN must be ChatJoin with Room sans #: %#v", ev)
	}
	ev = wantEvent(t, c)
	names, ok := ev.(tocwire.ChatUpdateBuddy)
	if !ok || !names.Present || len(names.Names) != 2 || names.Names[0] != "operator" {
		t.Fatalf("NAMES must become presence with @-prefixes stripped: %#v", ev)
	}
	// 366 is not an event; next: inbound chat.
	f.send(":operator!u@h PRIVMSG #lobby :hello — with an em dash")
	ev = wantEvent(t, c)
	chat, ok := ev.(tocwire.ChatIn)
	if !ok || chat.From != "operator" || chat.Text != "hello — with an em dash" || chat.RoomID != "#lobby" {
		t.Fatalf("channel PRIVMSG must be ChatIn (UTF-8 untouched): %#v", ev)
	}
	// DM.
	f.send(":operator!u@h PRIVMSG SmarterChild :psst")
	if im, ok := wantEvent(t, c).(tocwire.IMIn); !ok || im.From != "operator" || im.Text != "psst" {
		t.Fatal("direct PRIVMSG must be IMIn")
	}
	// CTCP ACTION.
	f.send(":operator!u@h PRIVMSG #lobby :\x01ACTION waves\x01")
	if chat, ok := wantEvent(t, c).(tocwire.ChatIn); !ok || chat.Text != "* waves" {
		t.Fatalf("ACTION must render as * text: %#v", chat)
	}

	// Outbound: newline split + injection guard in one.
	if err := c.ChatSend("#lobby", "line one\nline two\r\nQUIT :evil"); err != nil {
		t.Fatal(err)
	}
	if got := f.readLine(); got != "PRIVMSG #lobby :line one" {
		t.Fatalf("first line: %q", got)
	}
	if got := f.readLine(); got != "PRIVMSG #lobby :line two" {
		t.Fatalf("second line (CR stripped): %q", got)
	}
	if got := f.readLine(); got != "PRIVMSG #lobby :QUIT :evil" {
		t.Fatalf("injected command must arrive as TEXT, never a command: %q", got)
	}
}

func TestQuitBecomesPerRoomPresence(t *testing.T) {
	f := newFakeServer(t)
	c := dialOK(t, f)
	c.ChatJoin("lobby")
	f.readLine()
	f.send(
		":SmarterChild!u@h JOIN #lobby",
		":buddy.local 353 SmarterChild = #lobby :operator SmarterChild",
	)
	wantEvent(t, c) // ChatJoin
	wantEvent(t, c) // NAMES presence
	f.send(":operator!u@h QUIT :Quit: bye")
	ev := wantEvent(t, c)
	left, ok := ev.(tocwire.ChatUpdateBuddy)
	if !ok || left.Present || left.RoomID != "#lobby" || left.Names[0] != "operator" {
		t.Fatalf("QUIT must surface as per-room departure: %#v", ev)
	}
}

func TestServerPingAnsweredAndErrorsSurface(t *testing.T) {
	f := newFakeServer(t)
	c := dialOK(t, f)
	f.send("PING :abc")
	if got := f.readLine(); got != "PONG :abc" {
		t.Fatalf("server PING must be answered: %q", got)
	}
	f.send(":buddy.local 404 SmarterChild #lobby :Cannot send to channel")
	if se, ok := wantEvent(t, c).(tocwire.ServerError); !ok || !strings.Contains(se.Code, "404") {
		t.Fatal("4xx numerics must surface as ServerError")
	}
}

func TestLongMessageChunksOnRuneBoundary(t *testing.T) {
	f := newFakeServer(t)
	c := dialOK(t, f)
	long := strings.Repeat("é", 300) // 600 bytes of 2-byte runes
	if err := c.IM("operator", long); err != nil {
		t.Fatal(err)
	}
	first := f.readLine()
	second := f.readLine()
	payload1 := strings.TrimPrefix(first, "PRIVMSG operator :")
	payload2 := strings.TrimPrefix(second, "PRIVMSG operator :")
	if len(payload1) > maxChunk || !strings.HasPrefix(payload1, "é") || !strings.HasSuffix(payload1, "é") {
		t.Fatalf("chunk must respect rune boundaries: len=%d", len(payload1))
	}
	if payload1+payload2 != long {
		t.Fatal("chunks must reassemble to the original text")
	}
}

func TestCloseUnblocksAndSendsAfterCloseFail(t *testing.T) {
	f := newFakeServer(t)
	c := dialOK(t, f)
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close must unblock the read loop")
	}
	if err := c.ChatSend("#lobby", "x"); err == nil {
		t.Fatal("sends after Close must fail")
	}
}

func TestCodexFixes(t *testing.T) {
	f := newFakeServer(t)
	c := dialOK(t, f)

	// SetAway with an injected newline: flattened to one line, never a command.
	if err := c.SetAway("away\nQUIT :injected"); err != nil {
		t.Fatal(err)
	}
	if got := f.readLine(); got != "AWAY :away QUIT :injected" {
		t.Fatalf("away newline must flatten, got %q", got)
	}

	// PING in param form gets the token back.
	f.send("PING token123")
	if got := f.readLine(); got != "PONG :token123" {
		t.Fatalf("param-form PING must be answered with its token: %q", got)
	}

	// IRCv3 tags never break parsing.
	f.send("@time=2026-08-15T00:00:00Z :operator!u@h PRIVMSG SmarterChild :tagged")
	if im, ok := wantEvent(t, c).(tocwire.IMIn); !ok || im.Text != "tagged" || im.From != "operator" {
		t.Fatal("tagged PRIVMSG must parse")
	}

	// Comma-smuggled multi-target is refused before the wire.
	if err := c.IM("a,b", "x"); err == nil {
		t.Fatal("comma multi-target must be refused")
	}

	// Long-target chunk budget: payloads shrink so the line stays <= 512.
	longTarget := "#" + strings.Repeat("c", 200)
	if err := c.ChatSend(longTarget, strings.Repeat("x", 500)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if got := f.readLine(); len(got)+2 > 512 {
			t.Fatalf("wire line exceeds 512 bytes: %d", len(got)+2)
		}
	}
}

func TestCapLSDoesNotEndNegotiationEarly(t *testing.T) {
	f := newFakeServer(t)
	go func() {
		conn, _ := f.ln.Accept()
		f.conn = conn
		f.br = bufio.NewReader(conn)
		for line := f.readLine(); !strings.HasPrefix(line, "USER "); line = f.readLine() {
		}
		// An LS before the ACK must NOT trigger CAP END.
		f.send(":buddy.local CAP * LS :echo-message server-time")
		f.send(":buddy.local CAP * ACK :echo-message")
		if end := f.readLine(); end != "CAP END" {
			f.t.Errorf("CAP END must follow ACK, got %q", end)
		}
		f.send(":buddy.local 001 SmarterChild :Welcome")
	}()
	c, err := Dial(context.Background(), f.addr(), "SmarterChild", "", WithKeepAlive(0))
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestFatalRegistrationNumerics(t *testing.T) {
	for _, code := range []string{"432", "436", "464"} {
		f := newFakeServer(t)
		go func() {
			conn, _ := f.ln.Accept()
			f.conn = conn
			f.br = bufio.NewReader(conn)
			for line := f.readLine(); !strings.HasPrefix(line, "USER "); line = f.readLine() {
			}
			f.send(":buddy.local " + code + " * SmarterChild :refused")
		}()
		if _, err := Dial(context.Background(), f.addr(), "SmarterChild", "", WithKeepAlive(0)); err == nil {
			t.Fatalf("%s must be a dial error", code)
		}
	}
}

func TestSelfPartClearsMembership(t *testing.T) {
	f := newFakeServer(t)
	c := dialOK(t, f)
	c.ChatJoin("lobby")
	f.readLine()
	f.send(
		":SmarterChild!u@h JOIN #lobby",
		":buddy.local 353 SmarterChild = #lobby :operator SmarterChild",
	)
	wantEvent(t, c)
	wantEvent(t, c)
	f.send(":SmarterChild!u@h PART #lobby")
	wantEvent(t, c) // own departure (presence)
	if left, ok := wantEvent(t, c).(tocwire.ChatLeft); !ok || left.RoomID != "#lobby" {
		t.Fatalf("self-PART must emit ChatLeft so consumers drop room state, got %#v", left)
	}
	// operator's later QUIT must NOT produce a stale #lobby departure.
	f.send(":operator!u@h QUIT :bye")
	f.send(":operator!u@h PRIVMSG SmarterChild :still here?")
	if im, ok := wantEvent(t, c).(tocwire.IMIn); !ok || im.Text != "still here?" {
		t.Fatal("stale membership leaked a QUIT event for a parted channel")
	}
}

func TestSplitMessageNarrowLimitMakesProgress(t *testing.T) {
	done := make(chan []string, 1)
	go func() { done <- splitMessageN("[agent] 😀 hi", 3) }()
	select {
	case chunks := <-done:
		if got := strings.Join(chunks, ""); got != "[agent] 😀 hi" {
			t.Fatalf("content mangled across chunks: %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("splitMessageN hung: no progress on a limit narrower than one rune")
	}
}

// presenceCall runs Presence off the test goroutine, which has to stay free to
// play the server.
type presenceResult struct {
	online []string
	ok     bool
	err    error
}

func presenceCall(c *Client, names ...string) <-chan presenceResult {
	res := make(chan presenceResult, 1)
	go func() {
		online, ok, err := c.Presence(names...)
		res <- presenceResult{online, ok, err}
	}()
	return res
}

func wantPresence(t *testing.T, res <-chan presenceResult) presenceResult {
	t.Helper()
	select {
	case r := <-res:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("Presence never returned")
	}
	return presenceResult{}
}

// ISON is the presence question, and its answer lists only who is there —
// in EITHER of two shapes. Measured against ergo 2.19.1: `ISON jsizl` answers
// `303 me jsizl` (one name, so no trailing colon is needed) while `ISON jsizl
// SmarterChild` answers `303 me :jsizl SmarterChild`. Reading only the
// trailing form passed every hermetic test written against the two-name shape
// and then reported NOBODY online for the one-name query a DM actually makes.
func TestPresenceAnswersFromISON(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ask   []string
		query string
		reply string
		want  []string
	}{
		{
			name:  "one name comes back as a plain parameter",
			ask:   []string{"jay"},
			query: "ISON jay",
			reply: ":buddy.local 303 SmarterChild jay",
			want:  []string{"jay"},
		},
		{
			name:  "several names come back as a trailing list",
			ask:   []string{"jay", "ghost", "kim"},
			query: "ISON jay ghost kim",
			reply: ":buddy.local 303 SmarterChild :Jay kim",
			want:  []string{"Jay", "kim"}, // the server's spelling, untouched
		},
		{
			name:  "nobody online is an empty answer, not a missing one",
			ask:   []string{"ghost"},
			query: "ISON ghost",
			reply: ":buddy.local 303 SmarterChild :",
			want:  nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeServer(t)
			c := dialOK(t, f)

			res := presenceCall(c, tc.ask...)
			if line := f.readLine(); line != tc.query {
				t.Fatalf("wrong query: %q", line)
			}
			f.send(tc.reply)

			r := wantPresence(t, res)
			if r.err != nil || !r.ok {
				t.Fatalf("ISON answered but Presence did not: ok=%v err=%v", r.ok, r.err)
			}
			if len(r.online) != len(tc.want) {
				t.Fatalf("online=%q, want %q", r.online, tc.want)
			}
			for i := range tc.want {
				if r.online[i] != tc.want[i] {
					t.Fatalf("online=%q, want %q", r.online, tc.want)
				}
			}
		})
	}
}

// A server without ISON answers by refusing the command. "Cannot tell" is a
// different fact from "nobody is there", and the caller must be able to see
// the difference — otherwise every DM to such a server is refused.
func TestPresenceUnknownWhenServerRefusesISON(t *testing.T) {
	f := newFakeServer(t)
	c := dialOK(t, f)

	res := presenceCall(c, "jay")
	f.readLine()
	f.send(":buddy.local 421 SmarterChild ISON :Unknown command")

	r := wantPresence(t, res)
	if r.err != nil {
		t.Fatalf("a refused command is an answer, not a failure: %v", r.err)
	}
	if r.ok {
		t.Fatal("a server that cannot answer must not be reported as answering")
	}
	// The numeric is still surfaced: a provoked error is not swallowed.
	if ev, want := wantEvent(t, c), "421 Unknown command"; ev != (tocwire.ServerError{Code: want}) {
		t.Fatalf("421 should still reach the event stream as %q, got %#v", want, ev)
	}
}

// A dead connection must wake the waiter with the reason, not leave it to
// discover the death by timing out.
func TestPresenceWakesOnConnectionDeath(t *testing.T) {
	// Long enough that the timeout cannot be what returns: if the waiter is
	// not woken by the death itself, this test hangs and fails, instead of
	// passing on a timeout error that looks the same from the outside.
	old := presenceTimeout
	presenceTimeout = 30 * time.Second
	t.Cleanup(func() { presenceTimeout = old })

	f := newFakeServer(t)
	c := dialOK(t, f)

	res := presenceCall(c, "jay")
	f.readLine()
	f.conn.Close()

	r := wantPresence(t, res)
	if r.err == nil {
		t.Fatal("a Presence outstanding when the connection died must report it")
	}
	if r.ok {
		t.Fatalf("a dead connection cannot have answered: %#v", r)
	}
}

// RPL_ISON carries no request tag, so a reply can only be matched to a query
// by there being exactly one outstanding. A query the server never answers
// therefore leaves a reply owed with nothing to identify it — and the answer
// is to stop asking on this connection, not to hand that reply to whoever
// asks next. Measured provocation: an over-long ISON draws `417 Input line
// too long` and no 303, on a connection that stays up.
//
// Stopping must not be permanent, though. The usual reason a reply is late is
// a busy connection, not a silent server: ergo fakelags a non-oper client, and
// one 4 KB message ahead of the query pushes its 303 past three seconds
// (measured). So the owed reply, when it lands, puts the connection back in
// sync — otherwise one busy minute would disable presence for days.
func TestUnansweredQueryPausesPresenceUntilTheOwedReplyArrives(t *testing.T) {
	old := presenceTimeout
	presenceTimeout = 50 * time.Millisecond
	t.Cleanup(func() { presenceTimeout = old })

	f := newFakeServer(t)
	c := dialOK(t, f)

	first := presenceCall(c, "alpha")
	if line := f.readLine(); line != "ISON alpha" {
		t.Fatalf("wrong query: %q", line)
	}
	f.send(":buddy.local 417 SmarterChild :Input line too long") // answered, but never with a 303
	if r := wantPresence(t, first); r.err == nil || r.ok {
		t.Fatalf("an unanswered query must fail: %#v", r)
	}
	wantEvent(t, c) // the 417 still surfaces as a server error

	// While a reply is owed, nothing may go out — there would be no way to
	// tell the two answers apart.
	paused := wantPresence(t, presenceCall(c, "beta"))
	switch {
	case paused.err != nil:
		t.Fatalf("a paused presence check reports \"cannot tell\", not an error: %v", paused.err)
	case paused.ok:
		t.Fatalf("a paused connection must not claim to have answered: %#v", paused)
	case len(paused.online) != 0:
		t.Fatalf("the second caller was handed the first query's answer: %q", paused.online)
	}
	if line, ok := f.tryReadLine(100 * time.Millisecond); ok {
		t.Fatalf("a paused connection must not keep querying, got %q", line)
	}

	// The owed reply finally lands. Nobody is waiting for it, and taking it
	// is what puts the connection back in sync. The PRIVMSG behind it is the
	// synchronisation: the read loop handles lines in order, so its event
	// cannot arrive before the 303 has been dealt with.
	f.send(":buddy.local 303 SmarterChild :alpha",
		":jay!u@h PRIVMSG SmarterChild :the reply has been consumed")
	if ev := wantEvent(t, c); ev != (tocwire.IMIn{From: "jay", Text: "the reply has been consumed"}) {
		t.Fatalf("unexpected event: %#v", ev)
	}

	resumed := presenceCall(c, "gamma")
	if line := f.readLine(); line != "ISON gamma" {
		t.Fatalf("presence should be asking again, got %q", line)
	}
	f.send(":buddy.local 303 SmarterChild :gamma")
	if r := wantPresence(t, resumed); !r.ok || len(r.online) != 1 || r.online[0] != "gamma" {
		t.Fatalf("the connection did not resume cleanly: %#v", r)
	}
}

// A reply with nobody waiting for it must be dropped, not held — the read
// loop is what delivers it, and blocking there stops the connection dead
// while holding the lock every other answer needs.
//
// Against a mailbox that is left installed, this test does not fail cleanly:
// it HANGS, because the wedged read loop never closes readDone and Close waits
// for it. That is the defect being demonstrated, not a flaw in the test — run
// it with -timeout if you are mutating this code.
func TestASecondReplyDoesNotWedgeTheReadLoop(t *testing.T) {
	f := newFakeServer(t)
	c := dialOK(t, f)

	res := presenceCall(c, "alpha")
	if line := f.readLine(); line != "ISON alpha" {
		t.Fatalf("wrong query: %q", line)
	}
	// Three replies for one query: the mailbox holds one, so an implementation
	// that leaves it installed blocks on the third — inside the lock every
	// other answer needs, on the goroutine that reads the socket.
	f.send(":buddy.local 303 SmarterChild :alpha",
		":buddy.local 303 SmarterChild :stranger", // more than was asked for
		":buddy.local 303 SmarterChild :another",
		":jay!u@h PRIVMSG SmarterChild :still reading")

	if r := wantPresence(t, res); !r.ok || len(r.online) != 1 || r.online[0] != "alpha" {
		t.Fatalf("caller got the wrong answer: %#v", r)
	}
	if ev := wantEvent(t, c); ev != (tocwire.IMIn{From: "jay", Text: "still reading"}) {
		t.Fatalf("the read loop stopped after the extra reply: %#v", ev)
	}
}

// The timer and the reply can become ready together, and only one of them
// wins the select. Which one is a scheduling coincidence, so the decision to
// pause the connection cannot rest on it: clearWaiter settles "was this query
// answered?" under the same lock the delivery takes. A test cannot stage that
// interleaving, so this pins the helper it turns on directly.
func TestAnAnsweredQueryIsNeverPaused(t *testing.T) {
	f := newFakeServer(t)
	c := dialOK(t, f)

	ch := make(chan presenceReply, 1)
	c.qmu.Lock()
	c.waiter = ch
	c.qmu.Unlock()

	// The reply arrives — this is what the read loop does.
	c.answerPresence(presenceReply{online: []string{"alpha"}, ok: true})

	// ...and only now does the timing-out caller try to give up.
	c.clearWaiter(ch, true)

	c.qmu.Lock()
	dead := c.dead
	c.qmu.Unlock()
	if dead {
		t.Fatal("a query that WAS answered paused the connection; nothing is owed, so nothing would ever un-pause it")
	}
	select {
	case r := <-ch:
		if !r.ok || len(r.online) != 1 || r.online[0] != "alpha" {
			t.Fatalf("the answer was mangled: %#v", r)
		}
	default:
		t.Fatal("the answer was thrown away")
	}
}

// One query at a time, held across the whole round-trip. Waiters that queue
// under one lock and write under another can reach the wire in the opposite
// order, and then each caller is told about the other's names — which for a
// DM means refusing a recipient who is online.
func TestOnlyOneQueryIsOutstandingAtATime(t *testing.T) {
	f := newFakeServer(t)
	c := dialOK(t, f)

	first := presenceCall(c, "alpha")
	if line := f.readLine(); line != "ISON alpha" {
		t.Fatalf("wrong query: %q", line)
	}

	// A second caller arrives while the first is still outstanding: nothing
	// of theirs may reach the wire yet.
	second := presenceCall(c, "beta")
	if line, ok := f.tryReadLine(150 * time.Millisecond); ok {
		t.Fatalf("a second query went out while one was outstanding: %q", line)
	}

	f.send(":buddy.local 303 SmarterChild :alpha")
	if r := wantPresence(t, first); !r.ok || len(r.online) != 1 || r.online[0] != "alpha" {
		t.Fatalf("first caller got the wrong answer: %#v", r)
	}

	if line := f.readLine(); line != "ISON beta" {
		t.Fatalf("second query should follow the first: %q", line)
	}
	f.send(":buddy.local 303 SmarterChild :beta")
	if r := wantPresence(t, second); !r.ok || len(r.online) != 1 || r.online[0] != "beta" {
		t.Fatalf("second caller got the wrong answer: %#v", r)
	}
}

// The name comes from the caller — `buddylist dm <name>` — so an absurd one
// must not reach the wire. ergo answers an over-long line with 417 and no
// 303, which would cost this connection its presence check for good.
func TestPresenceRefusesAnOverLongQuery(t *testing.T) {
	f := newFakeServer(t)
	c := dialOK(t, f)

	r := wantPresence(t, presenceCall(c, strings.Repeat("n", 600)))
	if r.err == nil || r.ok {
		t.Fatalf("an over-long query must be refused: %#v", r)
	}
	if line, ok := f.tryReadLine(100 * time.Millisecond); ok {
		t.Fatalf("the refused query still reached the wire: %q", line)
	}

	// And the connection is untouched: the refusal cost only its own caller.
	next := presenceCall(c, "alpha")
	if line := f.readLine(); line != "ISON alpha" {
		t.Fatalf("wrong query: %q", line)
	}
	f.send(":buddy.local 303 SmarterChild :alpha")
	if r := wantPresence(t, next); !r.ok || len(r.online) != 1 {
		t.Fatalf("a later query should work normally: %#v", r)
	}
}

// noLF is a peer that streams bytes and never sends the line terminator. The
// old reader accumulated until the LF arrived and checked the length only
// afterwards, so this input grew memory without bound and the "line exceeds
// 32KB" verdict could never be reached. The bound is the reader's buffer now.
var noLF = strings.Repeat("x", 40<<10)

// awaitEventsClosed is the bounded wait that makes a hang a failure: with
// ReadString in place, the read loop never returns on noLF.
func awaitEventsClosed(t *testing.T, c *Client) {
	t.Helper()
	for {
		select {
		case _, ok := <-c.Events():
			if !ok {
				return
			}
		case <-time.After(5 * time.Second):
			t.Fatal("read loop must end on an over-long line without an LF")
		}
	}
}

func TestInboundLineIsBoundedWithoutAnLF(t *testing.T) {
	t.Run("read loop", func(t *testing.T) {
		f := newFakeServer(t)
		c := dialOK(t, f)

		// Positive control: an ordinary long line still parses.
		long := strings.Repeat("y", 480)
		f.send(":operator!u@h PRIVMSG #lobby :" + long)
		if in, ok := wantEvent(t, c).(tocwire.ChatIn); !ok || in.Text != long {
			t.Fatalf("a 500-byte line must still parse: %#v", in)
		}

		if _, err := f.conn.Write([]byte(noLF)); err != nil {
			t.Fatal(err)
		}
		awaitEventsClosed(t, c)
		if err := c.Err(); err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("the terminal error must be the line-too-long verdict, got %v", err)
		}
	})

	t.Run("registration", func(t *testing.T) {
		cases := []struct {
			name    string
			payload string // written after USER, in place of 001
			wantErr string // "" means Dial must succeed
		}{
			{"500-byte 001 still registers", ":buddy.local 001 SmarterChild :" + strings.Repeat("w", 480) + "\r\n", ""},
			{"40KB without an LF is refused", noLF, "exceeds"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				f := newFakeServer(t)
				go func() {
					conn, err := f.ln.Accept()
					if err != nil {
						t.Error(err)
						return
					}
					f.conn = conn
					f.br = bufio.NewReader(conn)
					for line := f.readLine(); !strings.HasPrefix(line, "USER "); line = f.readLine() {
					}
					conn.Write([]byte(tc.payload))
				}()
				// Shorter than the default so the wrong outcome under the old
				// reader — a handshake timeout — is reached, and named, fast.
				c, err := Dial(context.Background(), f.addr(), "SmarterChild", "", WithKeepAlive(0), WithHandshakeTimeout(3*time.Second))
				if tc.wantErr == "" {
					if err != nil {
						t.Fatal(err)
					}
					c.Close()
					return
				}
				if err == nil {
					c.Close()
					t.Fatal("Dial must fail")
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Dial error %v must carry %q", err, tc.wantErr)
				}
			})
		}
	})
}

// The read loop can end with the socket still open — the over-long line does
// exactly that — and the client is then dead as far as the daemon can tell
// (Events closed). Two things used to outlive it: the keepalive loop, which
// selected only on closed and so PINGed the peer for as long as it kept the
// socket open, and a Presence call, which installed its mailbox, sent its ISON
// over the healthy socket, and waited the full presenceTimeout for a reply no
// read loop was left to deliver. Both are answered by the read loop's exit
// closing the conn and the keepalive watching readDone.
func TestNothingOutlivesTheReadLoop(t *testing.T) {
	// endReadLoop drives the read loop off the too-long path while the fake
	// server keeps its end of the socket open.
	endReadLoop := func(t *testing.T, f *fakeServer, c *Client) {
		t.Helper()
		if _, err := f.conn.Write([]byte(noLF)); err != nil {
			t.Fatal(err)
		}
		awaitEventsClosed(t, c)
	}

	t.Run("keepalive stops", func(t *testing.T) {
		f := newFakeServer(t)
		done := make(chan struct{})
		go func() { f.acceptAndRegister(); close(done) }()
		c, err := Dial(context.Background(), f.addr(), "SmarterChild", "", WithKeepAlive(20*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		<-done

		// Positive control: the keepalive is armed while the client lives.
		if got := f.readLine(); got != "PING :keepalive" {
			t.Fatalf("keepalive must PING while alive, got %q", got)
		}

		endReadLoop(t, f, c)
		// A PING written in the instant before the loop noticed readDone is
		// not a failure; one written after a settle is. Drain, then listen.
		settle := time.Now().Add(100 * time.Millisecond)
		for time.Now().Before(settle) {
			if _, ok := f.tryReadLine(50 * time.Millisecond); !ok {
				break
			}
		}
		if line, ok := f.tryReadLine(200 * time.Millisecond); ok {
			t.Fatalf("keepalive must stop once the read loop has ended, got %q", line)
		}
	})

	t.Run("keepalive goroutine exits at once, not at its next tick", func(t *testing.T) {
		// A closed conn alone also silences the PINGs — the next tick's send
		// fails — but at the 60s default that leaves the goroutine alive for
		// up to a minute after the daemon has moved on. The interval here is
		// longer than the wait, so only the readDone arm can pass this.
		f := newFakeServer(t)
		done := make(chan struct{})
		go func() { f.acceptAndRegister(); close(done) }()
		c, err := Dial(context.Background(), f.addr(), "SmarterChild", "", WithKeepAlive(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		<-done

		endReadLoop(t, f, c)
		select {
		case <-c.keepAliveDone:
		case <-time.After(2 * time.Second):
			t.Fatal("keepalive goroutine must exit when the read loop ends, not at its next tick")
		}
	})

	t.Run("presence returns promptly", func(t *testing.T) {
		// Long enough that the timeout cannot be what returns: if the dead
		// connection does not fail the send, this test hangs and fails.
		old := presenceTimeout
		presenceTimeout = 30 * time.Second
		t.Cleanup(func() { presenceTimeout = old })

		f := newFakeServer(t)
		c := dialOK(t, f)
		endReadLoop(t, f, c)

		r := wantPresence(t, presenceCall(c, "jay"))
		if r.err == nil || r.ok {
			t.Fatalf("a Presence on a dead connection must fail at once: %#v", r)
		}
	})
}

// A middle parameter ends at the first space. PASS sent that way delivered
// `hunter two` to the server as `hunter`, and the refusal read as a wrong
// password. The trailing form carries the whole value.
func TestPassIsSentAsTheTrailingParameter(t *testing.T) {
	cases := []struct{ name, password, want string }{
		{"plain", "secret", "PASS :secret"},
		{"with a space", "hunter two", "PASS :hunter two"},
		{"injection bytes are stripped, not split on", "a\r\nb\x00c", "PASS :abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeServer(t)
			done := make(chan struct{})
			go func() { f.acceptAndRegister(); close(done) }()
			c, err := Dial(context.Background(), f.addr(), "SmarterChild", tc.password, WithKeepAlive(0))
			if err != nil {
				t.Fatal(err)
			}
			<-done
			c.Close()
			f.mu.Lock()
			got := append([]string(nil), f.got...)
			f.mu.Unlock()
			if len(got) == 0 || got[0] != tc.want {
				t.Fatalf("first line on the wire must be %q, got %q", tc.want, got)
			}
		})
	}
}
