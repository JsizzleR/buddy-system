package tocwire

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mk6i/open-oscar-server/wire"
)

// Handshake constants shared by the fake server. The roasted hex for password
// "pass" is written out by hand (XOR with "Tic/Toc"), NOT computed via the
// same helper the client calls, so the test cannot agree with a bug.
const (
	testName     = "chatd"
	testPassword = "pass"
	// 'p'^'T'=0x24 'a'^'i'=0x08 's'^'c'=0x10 's'^'/'=0x5c
	wantSignonLine = "toc_signon 127.0.0.1 9898 chatd 0x2408105c english buddylist"
)

// listen returns a loopback listener plus a channel carrying the single
// accepted connection.
func listen(t *testing.T) (addr string, conns <-chan net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	ch := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		conn.SetDeadline(time.Now().Add(10 * time.Second))
		ch <- conn
	}()
	return ln.Addr().String(), ch
}

// serveSignon speaks the measured server side of the handshake and asserts
// the exact client bytes. Returns the server's FlapClient for the rest of the
// script, or an error (it runs on a non-test goroutine, so no fatals here).
func serveSignon(conn net.Conn) (*wire.FlapClient, error) {
	preamble := make([]byte, 10)
	if _, err := io.ReadFull(conn, preamble); err != nil {
		return nil, fmt.Errorf("read FLAPON: %w", err)
	}
	if string(preamble) != "FLAPON\r\n\r\n" {
		return nil, fmt.Errorf("preamble = %q, want FLAPON\\r\\n\\r\\n", preamble)
	}

	fc := wire.NewFlapClient(0, conn, conn)
	if err := fc.SendSignonFrame(nil); err != nil {
		return nil, fmt.Errorf("send server signon frame: %w", err)
	}

	signonFrame, err := fc.ReceiveSignonFrame()
	if err != nil {
		return nil, fmt.Errorf("receive client signon frame: %w", err)
	}
	sn, ok := signonFrame.String(wire.LoginTLVTagsScreenName)
	if !ok {
		return nil, errors.New("client signon frame missing screen name TLV 0x01")
	}
	if sn != testName {
		return nil, fmt.Errorf("screen name TLV = %q, want %q", sn, testName)
	}

	line, err := recvDataLine(fc)
	if err != nil {
		return nil, fmt.Errorf("receive toc_signon: %w", err)
	}
	if line != wantSignonLine {
		return nil, fmt.Errorf("toc_signon line = %q, want %q", line, wantSignonLine)
	}
	return fc, nil
}

// serveLogin completes a successful signon: replies SIGN_ON etc. and consumes
// toc_init_done.
func serveLogin(conn net.Conn) (*wire.FlapClient, error) {
	fc, err := serveSignon(conn)
	if err != nil {
		return nil, err
	}
	for _, m := range []string{"SIGN_ON:TOC1.0", "CONFIG:", "NICK:" + testName} {
		if err := fc.SendDataFrame([]byte(m)); err != nil {
			return nil, fmt.Errorf("send %q: %w", m, err)
		}
	}
	line, err := recvDataLine(fc)
	if err != nil {
		return nil, fmt.Errorf("receive toc_init_done: %w", err)
	}
	if line != "toc_init_done" {
		return nil, fmt.Errorf("post-signon line = %q, want toc_init_done", line)
	}
	return fc, nil
}

// recvDataLine reads frames until a data frame arrives and returns its
// payload as a string.
func recvDataLine(fc *wire.FlapClient) (string, error) {
	for {
		frame, err := fc.ReceiveFLAP()
		if err != nil {
			return "", err
		}
		if frame.FrameType == wire.FLAPFrameData {
			return string(frame.Payload), nil
		}
	}
}

// dialOK runs a fake server through a successful login and returns the
// connected client plus the server-side FlapClient and conn.
func dialOK(t *testing.T, opts ...Option) (*Client, *wire.FlapClient, net.Conn) {
	t.Helper()
	addr, conns := listen(t)

	type serverResult struct {
		fc  *wire.FlapClient
		err error
	}
	srvCh := make(chan serverResult, 1)
	connCh := make(chan net.Conn, 1)
	go func() {
		conn := <-conns
		connCh <- conn
		fc, err := serveLogin(conn)
		srvCh <- serverResult{fc, err}
	}()

	client, err := Dial(context.Background(), addr, testName, testPassword, opts...)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	res := <-srvCh
	if res.err != nil {
		t.Fatalf("fake server: %v", res.err)
	}

	// The post-SIGN_ON login lines (CONFIG:, NICK:) surface as Unknown events
	// by design; drain them so each test starts with a quiet stream.
	for {
		select {
		case ev, ok := <-client.Events():
			if !ok {
				t.Fatal("Events() closed while draining login lines")
			}
			u, isUnknown := ev.(Unknown)
			if !isUnknown {
				t.Fatalf("unexpected typed event during login drain: %#v", ev)
			}
			if strings.HasPrefix(u.Raw, "NICK:") {
				return client, res.fc, <-connCh
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out draining login lines")
		}
	}
}

// TestDialHandshake drives the full measured handshake against a fake server
// that asserts every byte the client sends (FLAPON, screen-name TLV, the
// exact toc_signon line with hand-computed roasted hex, toc_init_done).
func TestDialHandshake(t *testing.T) {
	client, _, _ := dialOK(t)
	if err := client.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if _, ok := <-client.Events(); ok {
		t.Error("Events() open after Close, want closed")
	}
}

// TestDialSignonRefused verifies an ERROR:nnn reply during signon fails Dial
// with the code in the error.
func TestDialSignonRefused(t *testing.T) {
	addr, conns := listen(t)
	done := make(chan error, 1)
	go func() {
		conn := <-conns
		fc, err := serveSignon(conn)
		if err != nil {
			done <- err
			return
		}
		done <- fc.SendDataFrame([]byte("ERROR:980"))
	}()

	_, err := Dial(context.Background(), addr, testName, testPassword)
	if err == nil {
		t.Fatal("Dial succeeded, want ERROR:980 refusal")
	}
	if !strings.Contains(err.Error(), "980") {
		t.Errorf("Dial error = %v, want it to carry code 980", err)
	}
	if srvErr := <-done; srvErr != nil {
		t.Fatalf("fake server: %v", srvErr)
	}
}

// TestDialHandshakeTimeout verifies a server that accepts and then goes
// silent fails Dial via the handshake deadline rather than hanging.
func TestDialHandshakeTimeout(t *testing.T) {
	addr, conns := listen(t)
	go func() { <-conns }() // accept, then say nothing

	start := time.Now()
	_, err := Dial(context.Background(), addr, testName, testPassword,
		WithHandshakeTimeout(150*time.Millisecond))
	if err == nil {
		t.Fatal("Dial succeeded against a silent server, want timeout")
	}
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Errorf("Dial took %v, want it bounded by the handshake timeout", elapsed)
	}
}

// TestDialContextCanceled verifies cancelling the dial context aborts a
// handshake in flight.
func TestDialContextCanceled(t *testing.T) {
	addr, conns := listen(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-conns // accept, then say nothing
		cancel()
	}()

	_, err := Dial(ctx, addr, testName, testPassword)
	if err == nil {
		t.Fatal("Dial succeeded, want cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Dial error = %v, want context.Canceled", err)
	}
}

// TestParseEvent table-tests the event parser for every event type,
// including text containing colons and raw backslash escapes, and asserts
// malformed or unrecognized lines surface as Unknown rather than vanishing.
func TestParseEvent(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want Event
	}{
		{
			name: "chat join",
			raw:  "CHAT_JOIN:3:lobby",
			want: ChatJoin{RoomID: "3", Room: "lobby"},
		},
		{
			name: "chat in with colons in text",
			raw:  "CHAT_IN:3:jay:F:deploy at 12:30: ok?",
			want: ChatIn{RoomID: "3", From: "jay", Whisper: false, Text: "deploy at 12:30: ok?"},
		},
		{
			name: "chat in whisper flag with raw escapes in text",
			raw:  `CHAT_IN:3:jay:T:brace \{x\} and \"q\"`,
			want: ChatIn{RoomID: "3", From: "jay", Whisper: true, Text: `brace \{x\} and \"q\"`},
		},
		{
			name: "im in with colons in text",
			raw:  "IM_IN:nightly:F:state=GREEN: all clear",
			want: IMIn{From: "nightly", Auto: false, Text: "state=GREEN: all clear"},
		},
		{
			name: "im in auto",
			raw:  "IM_IN:jay:T:away",
			want: IMIn{From: "jay", Auto: true, Text: "away"},
		},
		{
			name: "update buddy online keeps raw",
			raw:  "UPDATE_BUDDY:jay:T:0:1755000000:0: O ",
			want: UpdateBuddy{Raw: "UPDATE_BUDDY:jay:T:0:1755000000:0: O ", Name: "jay", Online: true},
		},
		{
			name: "update buddy departed",
			raw:  "UPDATE_BUDDY:jay:F:0:0:0:   ",
			want: UpdateBuddy{Raw: "UPDATE_BUDDY:jay:F:0:0:0:   ", Name: "jay", Online: false},
		},
		{
			name: "chat update buddy arrivals",
			raw:  "CHAT_UPDATE_BUDDY:3:T:jay:chatd:nightly",
			want: ChatUpdateBuddy{RoomID: "3", Present: true, Names: []string{"jay", "chatd", "nightly"}},
		},
		{
			name: "chat update buddy departure no names",
			raw:  "CHAT_UPDATE_BUDDY:3:F",
			want: ChatUpdateBuddy{RoomID: "3", Present: false, Names: []string{}},
		},
		{
			name: "error with args",
			raw:  "ERROR:983:rate limited",
			want: ServerError{Code: "983"},
		},
		{
			name: "error bare",
			raw:  "ERROR:980",
			want: ServerError{Code: "980"},
		},
		{
			name: "unrecognized verb",
			raw:  "NICK:chatd",
			want: Unknown{Raw: "NICK:chatd"},
		},
		{
			name: "similar verb is not a prefix match",
			raw:  "IM_IN2:jay:F:F:hello",
			want: Unknown{Raw: "IM_IN2:jay:F:F:hello"},
		},
		{
			name: "malformed known verb",
			raw:  "CHAT_IN:3:jay",
			want: Unknown{Raw: "CHAT_IN:3:jay"},
		},
		{
			name: "bare word",
			raw:  "PAUSE",
			want: Unknown{Raw: "PAUSE"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseEvent(tt.raw)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseEvent(%q) = %#v, want %#v", tt.raw, got, tt.want)
			}
		})
	}
}

// TestEventsDelivered runs typed lines end-to-end through the read loop and
// asserts they arrive in order; then the server drops the connection and the
// channel must close with a terminal Err.
func TestEventsDelivered(t *testing.T) {
	client, srvFC, srvConn := dialOK(t)

	lines := []string{
		"CHAT_JOIN:3:lobby",
		"CHAT_IN:3:jay:F:deploy at 12:30: ok?",
		"IM_IN:nightly:F:state=GREEN",
		"NICK:chatd",
	}
	want := []Event{
		ChatJoin{RoomID: "3", Room: "lobby"},
		ChatIn{RoomID: "3", From: "jay", Whisper: false, Text: "deploy at 12:30: ok?"},
		IMIn{From: "nightly", Auto: false, Text: "state=GREEN"},
		Unknown{Raw: "NICK:chatd"},
	}
	for _, l := range lines {
		if err := srvFC.SendDataFrame([]byte(l)); err != nil {
			t.Fatalf("server send %q: %v", l, err)
		}
	}
	for i, w := range want {
		select {
		case got, ok := <-client.Events():
			if !ok {
				t.Fatalf("Events() closed before event %d", i)
			}
			if !reflect.DeepEqual(got, w) {
				t.Errorf("event %d = %#v, want %#v", i, got, w)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for event %d", i)
		}
	}

	srvConn.Close()
	select {
	case _, ok := <-client.Events():
		if ok {
			t.Fatal("got an extra event after server close, want channel close")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Events() did not close after server dropped the connection")
	}
	if client.Err() == nil {
		t.Error("Err() = nil after connection death, want terminal error")
	}
}

// TestServerSignoff verifies a FLAP signoff frame ends the stream with a
// signoff terminal error.
func TestServerSignoff(t *testing.T) {
	client, srvFC, _ := dialOK(t)

	if err := srvFC.NewSignoff(wire.TLVRestBlock{}); err != nil {
		t.Fatalf("server signoff: %v", err)
	}
	select {
	case _, ok := <-client.Events():
		if ok {
			t.Fatal("got an event after signoff, want channel close")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Events() did not close after signoff")
	}
	if err := client.Err(); err == nil || !strings.Contains(err.Error(), "signoff") {
		t.Errorf("Err() = %v, want signoff terminal error", err)
	}
}

// TestSendsEscapedAndQuoted asserts the exact bytes each send method puts on
// the wire: TOC escaping of \ { } ( ) [ ] $ " and double-quoting of text
// arguments. Expected payloads are literals, not recomputed via the escaper.
func TestSendsEscapedAndQuoted(t *testing.T) {
	client, srvFC, _ := dialOK(t)

	tricky := `He said "hi" \ {ok} (really) [yes] $5 a:b`
	steps := []struct {
		name string
		send func() error
		want string
	}{
		{
			name: "chat join quotes room on exchange 5",
			send: func() error { return client.ChatJoin("lobby dev") },
			want: `toc_chat_join 5 "lobby dev"`,
		},
		{
			name: "chat send escapes and quotes text",
			send: func() error { return client.ChatSend("3", tricky) },
			want: `toc_chat_send 3 "He said \"hi\" \\ \{ok\} \(really\) \[yes\] \$5 a:b"`,
		},
		{
			name: "im escapes and quotes text",
			send: func() error { return client.IM("jay", `poke [now] "please"`) },
			want: `toc_send_im jay "poke \[now\] \"please\""`,
		},
		{
			name: "set away quotes text",
			send: func() error { return client.SetAway("busy (deploying)") },
			want: `toc_set_away "busy \(deploying\)"`,
		},
		{
			name: "set away empty clears with no argument",
			send: func() error { return client.SetAway("") },
			want: "toc_set_away",
		},
		{
			name: "add buddies space separated",
			send: func() error { return client.AddBuddies("jay", "nightly") },
			want: "toc_add_buddy jay nightly",
		},
	}
	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			if err := st.send(); err != nil {
				t.Fatalf("send: %v", err)
			}
			got, err := recvDataLine(srvFC)
			if err != nil {
				t.Fatalf("server receive: %v", err)
			}
			if got != st.want {
				t.Errorf("wire payload = %q, want %q", got, st.want)
			}
		})
	}
}

// TestBareArgValidation verifies unquoted arguments (screen names, room ids)
// that would break the server's tokenizer are refused before anything is
// sent.
func TestBareArgValidation(t *testing.T) {
	c := &Client{} // validation fails before any connection state is touched
	tests := []struct {
		name string
		call func() error
	}{
		{"im to name with space", func() error { return c.IM("j ay", "x") }},
		{"im to empty name", func() error { return c.IM("", "x") }},
		{"chat send quote in room id", func() error { return c.ChatSend(`3"`, "x") }},
		{"add buddy backslash", func() error { return c.AddBuddies(`ja\y`) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); err == nil {
				t.Error("call succeeded, want validation error")
			}
		})
	}
}

// TestCloseDuringBlockedRead verifies Close unblocks a read loop parked in
// ReceiveFLAP, closes Events, and stays idempotent.
func TestCloseDuringBlockedRead(t *testing.T) {
	client, _, _ := dialOK(t) // server now silent; read loop is blocked

	done := make(chan error, 1)
	go func() { done <- client.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return while the read loop was blocked")
	}
	if _, ok := <-client.Events(); ok {
		t.Error("Events() open after Close, want closed")
	}
	if err := client.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestCloseWithUndeliveredEvent verifies Close cannot deadlock against a read
// loop blocked delivering an event nobody is consuming.
func TestCloseWithUndeliveredEvent(t *testing.T) {
	client, srvFC, _ := dialOK(t)

	// Prove the read loop is live by consuming one event...
	if err := srvFC.SendDataFrame([]byte("CHAT_JOIN:3:lobby")); err != nil {
		t.Fatalf("server send: %v", err)
	}
	select {
	case <-client.Events():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first event")
	}
	// ...then park it on a delivery nobody consumes.
	if err := srvFC.SendDataFrame([]byte("IM_IN:jay:F:unread")); err != nil {
		t.Fatalf("server send: %v", err)
	}
	time.Sleep(20 * time.Millisecond) // best effort to reach the blocked-delivery interleaving

	done := make(chan error, 1)
	go func() { done <- client.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close deadlocked against an undelivered event")
	}
}

// TestSendAfterClose verifies sends refuse with ErrClosed once closed.
func TestSendAfterClose(t *testing.T) {
	client, _, _ := dialOK(t)
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := client.IM("jay", "late"); !errors.Is(err, ErrClosed) {
		t.Errorf("IM after Close = %v, want ErrClosed", err)
	}
}

// TestKeepAlive verifies the optional ticker emits FLAP keepalive frames
// (type 0x05) on the wire.
func TestKeepAlive(t *testing.T) {
	client, srvFC, _ := dialOK(t, WithKeepAlive(20*time.Millisecond))
	defer client.Close()

	deadline := time.After(5 * time.Second)
	got := make(chan uint8, 1)
	go func() {
		for {
			frame, err := srvFC.ReceiveFLAP()
			if err != nil {
				return
			}
			if frame.FrameType == wire.FLAPFrameKeepAlive {
				got <- frame.FrameType
				return
			}
		}
	}()
	select {
	case <-got:
	case <-deadline:
		t.Fatal("no keepalive frame arrived")
	}
}
