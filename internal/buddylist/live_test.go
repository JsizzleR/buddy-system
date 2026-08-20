//go:build oscarlive

// Live integration against a real open-oscar-server binary, spawned per test.
// Run via scripts/check.sh, which builds the pinned server first; the build
// tag keeps `go test ./...` hermetic. BUDDY_OSCAR_BIN overrides the binary.
package buddylist

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/tocwire"
)

func serverBin(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("BUDDY_OSCAR_BIN"); p != "" {
		return p
	}
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err == nil {
		p := filepath.Join(strings.TrimSpace(string(root)), ".cache", "oscar-server")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Fatal("no server binary: run scripts/get-oscar.sh or set BUDDY_OSCAR_BIN (this leg must not be skipped silently — scripts/check.sh provides the binary)")
	return ""
}

func freePorts(t *testing.T, n int) []int {
	t.Helper()
	var ports []int
	var lns []net.Listener
	for i := 0; i < n; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		lns = append(lns, ln)
		ports = append(ports, ln.Addr().(*net.TCPAddr).Port)
	}
	for _, ln := range lns {
		ln.Close()
	}
	return ports
}

type liveServer struct {
	cmd  *exec.Cmd
	bos  int
	tocP int
	api  int
	dir  string
}

func startServer(t *testing.T, bin, dir string, ports []int) *liveServer {
	t.Helper()
	s := &liveServer{bos: ports[0], tocP: ports[1], api: ports[2], dir: dir}
	cmd := exec.Command(bin)
	cmd.Env = []string{
		"PATH=/usr/bin:/bin",
		fmt.Sprintf("OSCAR_LISTENERS=LOCAL://127.0.0.1:%d", s.bos),
		fmt.Sprintf("OSCAR_ADVERTISED_LISTENERS_PLAIN=LOCAL://127.0.0.1:%d", s.bos),
		fmt.Sprintf("TOC_LISTENERS=127.0.0.1:%d", s.tocP),
		fmt.Sprintf("API_LISTENER=127.0.0.1:%d", s.api),
		"DB_PATH=" + filepath.Join(dir, "oscar.sqlite"),
		"DISABLE_AUTH=true",
		"LOG_LEVEL=debug",
	}
	cmd.Dir = dir
	logf, err := os.OpenFile(filepath.Join(dir, "server.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s.cmd = cmd
	t.Cleanup(func() { s.stop() })

	// Observable readiness: the TOC port accepts.
	deadline := time.Now().Add(15 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", s.tocP), time.Second)
		if err == nil {
			c.Close()
			return s
		}
		if time.Now().After(deadline) {
			log, _ := os.ReadFile(filepath.Join(dir, "server.log"))
			t.Fatalf("server never listened; log:\n%s", log)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (s *liveServer) stop() {
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
		s.cmd.Wait()
		s.cmd = nil
	}
}

func TestLiveEndToEnd(t *testing.T) {
	bin := serverBin(t)
	dir := t.TempDir()
	ports := freePorts(t, 3)
	srv := startServer(t, bin, dir, ports)

	sockDir, err := os.MkdirTemp("", "fdl")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockDir)
	sock := filepath.Join(sockDir, "buddylistd.sock")

	j, err := OpenJournal(filepath.Join(dir, "journal.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	tocAddr := fmt.Sprintf("127.0.0.1:%d", srv.tocP)
	d, err := New(Config{
		Rooms:      []string{"lobby"},
		APIAddr:    fmt.Sprintf("127.0.0.1:%d", srv.api),
		SocketPath: sock,
		Journal:    j,
		Log:        slog.Default(),
		MaxBackoff: 500 * time.Millisecond,
		Dial: func(ctx context.Context) (Conn, error) {
			return tocwire.Dial(ctx, tocAddr, "SmarterChild", "SmarterChild")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("daemon did not shut down")
		}
	}()

	call := func(req Request) (Response, error) {
		return Call(sock, req, 3*time.Second)
	}
	waitHealthy := func(want bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for {
			resp, err := call(Request{Op: "health"})
			if err == nil && resp.Connected == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("health never reached connected=%v (err=%v)", want, err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	waitHealthy(true)

	// A real second client joins and speaks; chatd journals it.
	newPeer := func(name string) (*tocwire.Client, string) {
		t.Helper()
		p, err := tocwire.Dial(context.Background(), tocAddr, name, "pw")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { p.Close() })
		if err := p.ChatJoin("lobby"); err != nil {
			t.Fatal(err)
		}
		timer := time.After(5 * time.Second)
		var seen []string
		for {
			select {
			case ev, ok := <-p.Events():
				if !ok {
					t.Fatalf("peer %s died: %v (seen: %v)", name, p.Err(), seen)
				}
				seen = append(seen, fmt.Sprintf("%T:%+v", ev, ev))
				if cj, isJoin := ev.(tocwire.ChatJoin); isJoin {
					return p, cj.RoomID
				}
			case <-timer:
				log, _ := os.ReadFile(filepath.Join(dir, "server.log"))
				var errLines []string
				for _, ln := range strings.Split(string(log), "\n") {
					if strings.Contains(ln, "error") {
						errLines = append(errLines, ln)
					}
				}
				t.Fatalf("peer %s never joined; events seen: %v\nserver errors:\n%s", name, seen, strings.Join(errLines, "\n"))
			}
		}
	}
	waitPeerChat := func(p *tocwire.Client, substr string) {
		t.Helper()
		timer := time.After(10 * time.Second)
		for {
			select {
			case ev, ok := <-p.Events():
				if !ok {
					t.Fatalf("peer died waiting for %q: %v", substr, p.Err())
				}
				if ci, isChat := ev.(tocwire.ChatIn); isChat && strings.Contains(ci.Text, substr) {
					return
				}
			case <-timer:
				t.Fatalf("peer never saw %q", substr)
			}
		}
	}
	readUntil := func(substr string) Response {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			resp, err := call(Request{Op: "read", Room: "lobby", After: 0, Limit: 200})
			if err == nil {
				for _, m := range resp.Msgs {
					if strings.Contains(m.Body, substr) {
						return resp
					}
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("journal never showed %q (last resp: %+v, err=%v)", substr, resp, err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	peer, peerRoom := newPeer("jay")
	if err := peer.ChatSend(peerRoom, "hello from jay"); err != nil {
		t.Fatal(err)
	}
	resp := readUntil("hello from jay")
	found := false
	for _, m := range resp.Msgs {
		if m.Body == "hello from jay" && m.Sender == "jay" && m.Kind == "chat" && m.Room == "lobby" {
			found = true
		}
	}
	if !found {
		t.Fatalf("journal row wrong: %+v", resp.Msgs)
	}

	// Outbound relay: socket say → the peer sees the prefixed message.
	if _, err := call(Request{Op: "say", Room: "lobby", From: "alpha", Text: "claiming the router"}); err != nil {
		t.Fatal(err)
	}
	waitPeerChat(peer, "[alpha] claiming the router")

	// Server death: sends fail VISIBLY, health goes disconnected.
	srv.stop()
	waitHealthy(false)
	if _, err := call(Request{Op: "say", Room: "lobby", Text: "into the void"}); err == nil {
		t.Fatal("say while the server is down must fail visibly")
	}

	// Restart on the same ports: chatd reconnects and rejoins on its own.
	startServer(t, bin, dir, ports)
	waitHealthy(true)
	peer2, _ := newPeer("jay2")
	_ = peer2
	if _, err := call(Request{Op: "say", Room: "lobby", From: "alpha", Text: "back online"}); err != nil {
		// The daemon may report connected before the room join lands; retry briefly.
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err = call(Request{Op: "say", Room: "lobby", From: "alpha", Text: "back online"}); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("say after reconnect never succeeded: %v", err)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	waitPeerChat(peer2, "[alpha] back online")

	// Late-join catch-up: pre-drop history is intact from a fresh cursor.
	resp = readUntil("back online")
	if resp.Gap {
		t.Fatal("fresh cursor must not report a gap")
	}
	preDrop := false
	for _, m := range resp.Msgs {
		if m.Body == "hello from jay" {
			preDrop = true
		}
	}
	if !preDrop {
		t.Fatal("journal must retain pre-restart history for late joiners")
	}
}
