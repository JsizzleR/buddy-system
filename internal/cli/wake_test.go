package cli

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// D-039 (issue #25): `msg` names the harness's wake address for a quiet
// recipient and wakes nothing itself.

// listenSock makes a real unix socket at dir/<pid>.sock, as the harness does.
// The directory is short and under /tmp because a unix socket path is capped
// at 104 bytes on darwin, and t.TempDir's is longer than that.
func listenSock(t *testing.T, dir string, pid int) string {
	t.Helper()
	p := filepath.Join(dir, strconv.Itoa(pid)+".sock")
	l, err := net.Listen("unix", p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return p
}

func shortSockDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "bw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// beatAs registers pid as bravo's harness process the way a real hook does.
func beatAs(t *testing.T, f *fixture, pid int) {
	t.Helper()
	f.asProcess(pid, 7, func() {
		if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Bash", ""), "beat"); code != 0 {
			t.Fatalf("beat: %s", errw)
		}
	})
}

func idleB(t *testing.T, f *fixture) {
	t.Helper()
	if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "", ""), "idle"); code != 0 {
		t.Fatalf("idle: %s", errw)
	}
}

func TestMsgNamesTheWakeAddressOfAQuietRecipient(t *testing.T) {
	boundedParallel(t)
	const body = "take the router bundle"
	cases := []struct {
		name  string
		setup func(t *testing.T, f *fixture)
		to    string
		wake  bool
	}{
		{"idle, one live process, its socket", func(t *testing.T, f *fixture) {
			beatAs(t, f, 400)
			listenSock(t, f.sockDir, 400)
			idleB(t, f)
			f.clock = f.clock.Add(10 * time.Minute)
		}, "bravo", true},
		{"quiet past the stale mark, no idle report", func(t *testing.T, f *fixture) {
			beatAs(t, f, 400)
			listenSock(t, f.sockDir, 400)
			f.clock = f.clock.Add(2 * time.Hour)
		}, "bravo", true},
		// The positive control above, less one condition each.
		{"idle, no socket", func(t *testing.T, f *fixture) {
			beatAs(t, f, 400)
			idleB(t, f)
		}, "bravo", false},
		{"idle, a regular file where the socket would be", func(t *testing.T, f *fixture) {
			beatAs(t, f, 400)
			if err := os.WriteFile(filepath.Join(f.sockDir, "400.sock"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			idleB(t, f)
		}, "bravo", false},
		// The measured case: a fresh session at its first prompt. The beat
		// lands in the registration second, which the ledger's whole seconds
		// cannot tell from none.
		{"registered and not seen since", func(t *testing.T, f *fixture) {
			beatAs(t, f, 400)
			listenSock(t, f.sockDir, 400)
			f.clock = f.clock.Add(4 * time.Second)
		}, "bravo", true},
		{"seen recently", func(t *testing.T, f *fixture) {
			f.clock = f.clock.Add(time.Second)
			beatAs(t, f, 400)
			listenSock(t, f.sockDir, 400)
			f.clock = f.clock.Add(4 * time.Second)
		}, "bravo", false},
		{"its process is gone", func(t *testing.T, f *fixture) {
			beatAs(t, f, 400)
			listenSock(t, f.sockDir, 400)
			idleB(t, f)
			f.kill(400)
		}, "bravo", false},
		{"two live processes are ambiguous", func(t *testing.T, f *fixture) {
			beatAs(t, f, 400)
			beatAs(t, f, 401)
			listenSock(t, f.sockDir, 400)
			listenSock(t, f.sockDir, 401)
			idleB(t, f)
		}, "bravo", false},
		{"ended", func(t *testing.T, f *fixture) {
			beatAs(t, f, 400)
			listenSock(t, f.sockDir, 400)
			if _, errw, code := f.run(t, f.repo, "", "bye", "sess-b", "--force"); code != 0 {
				t.Fatalf("bye: %s", errw)
			}
		}, "sess-b", false},
		{"a broadcast", func(t *testing.T, f *fixture) {
			beatAs(t, f, 400)
			listenSock(t, f.sockDir, 400)
			idleB(t, f)
		}, "all", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.sockDir = shortSockDir(t)
			f.initAndHello(t)
			tc.setup(t, f)
			out, errw, code := f.run(t, f.repo, "", "msg", tc.to, body)
			if code != 0 || !strings.HasPrefix(out, "queued for ") {
				t.Fatalf("control: the send itself must succeed: %s %s", out, errw)
			}
			// ONE line either way (D-041): a sender reading `| head -1`
			// dropped the address when it was a second line, and an idle lane
			// sat unwoken.
			if n := strings.Count(out, "\n"); n != 1 || !strings.HasSuffix(out, "\n") {
				t.Fatalf("msg must answer on exactly one line, got %d:\n%s", n, out)
			}
			has := strings.Contains(out, "to wake it now:")
			if has != tc.wake {
				t.Fatalf("wake address present=%v, want %v:\n%s", has, tc.wake, out)
			}
			if !tc.wake {
				return
			}
			want := `; to wake it now: SendMessage to "uds:` + filepath.Join(f.sockDir, "400.sock") +
				`" with the text "buddy mail is queued for you: run buddy inbox" — the harness delivers that as a message from another session, never as your user's turn (D-039); a session in a different permission mode holds it for its user, and this copy stays queued either way` + "\n"
			if !strings.HasSuffix(out, want) {
				t.Fatalf("want the wake address to end the result line:\n%s\nin:\n%s", want, out)
			}
			// The wake carries no content: the message stays in the ledger.
			if strings.Contains(want, body) || strings.Count(out, body) != 0 {
				t.Fatalf("the message body leaked into the output:\n%s", out)
			}
			if n := f.undeliveredTo(t, "sess-b", "bravo"); n != 1 {
				t.Fatalf("the ledger copy must stay queued, got %d", n)
			}
		})
	}
}
