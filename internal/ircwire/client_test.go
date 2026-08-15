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
	wantEvent(t, c) // own departure event
	// operator's later QUIT must NOT produce a stale #lobby departure.
	f.send(":operator!u@h QUIT :bye")
	f.send(":operator!u@h PRIVMSG SmarterChild :still here?")
	if im, ok := wantEvent(t, c).(tocwire.IMIn); !ok || im.Text != "still here?" {
		t.Fatal("stale membership leaked a QUIT event for a parted channel")
	}
}
