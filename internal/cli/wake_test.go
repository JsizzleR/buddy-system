package cli

import (
	"net"
	"os"
	"path/filepath"
	"regexp"
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
			// Stderr carries the wake too (issue #42), because a sender that
			// discards stdout as a receipt lost it; and carries NOTHING when
			// there is no wake, so a stderr reader is never told to act on a
			// session that drains on its own.
			if !tc.wake {
				if errw != "" {
					t.Fatalf("no wake, so stderr must be empty, got:\n%s", errw)
				}
				return
			}
			// The text names the send's id and the local time the wake was
			// named (D-059), spelled out here rather than read back from
			// wakeText, so the test is a second statement of the format.
			clause := `to wake it now: SendMessage to "uds:` + filepath.Join(f.sockDir, "400.sock") +
				`" with the text "buddy mail #1 is queued for you (` + f.clock.Local().Format("15:04:05") +
				`): run buddy inbox" — the harness delivers that as a message from another session, never as your user's turn (D-039); a session in a different permission mode holds it for its user, and this copy stays queued either way`
			// Stderr carries the SAME clause, whole (D-052).
			if wantErr := "buddy: message #1 to " + tc.to + " is queued; " + clause + "\n"; errw != wantErr {
				t.Fatalf("want exactly one stderr line\n%s(and no body), got:\n%s", wantErr, errw)
			}
			want := "; " + clause + "\n"
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

// Issue #42's second half: `buddy sent` names the same wake for a recipient
// that still has the message queued and that msg's rule says needs one.
func TestSentNamesTheWakeOfAQueuedQuietRecipient(t *testing.T) {
	boundedParallel(t)
	const wakeHead = "to wake it now: SendMessage to \"uds:"
	cases := []struct {
		name  string
		to    string
		after func(t *testing.T, f *fixture) // between the send and the report
		wake  bool
	}{
		{"queued to an idle session with its socket", "bravo", func(t *testing.T, f *fixture) {}, true},
		// The positive control above, less one condition each.
		{"delivered, then idle again", "bravo", func(t *testing.T, f *fixture) {
			beatAs(t, f, 400) // the beat drains it
			idleB(t, f)
			f.clock = f.clock.Add(10 * time.Minute)
		}, false},
		// Not "seen since": every hook that marks a session seen (beat,
		// busy) also drains its inbox, so that case is "delivered" above.
		{"queued, its process gone", "bravo", func(t *testing.T, f *fixture) {
			f.kill(400)
		}, false},
		{"queued, its socket gone", "bravo", func(t *testing.T, f *fixture) {
			os.Remove(filepath.Join(f.sockDir, "400.sock"))
		}, false},
		{"a broadcast", "all", func(t *testing.T, f *fixture) {}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.sockDir = shortSockDir(t)
			f.initAndHello(t)
			beatAs(t, f, 400)
			l, err := net.Listen("unix", filepath.Join(f.sockDir, "400.sock"))
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			idleB(t, f)
			f.clock = f.clock.Add(10 * time.Minute)
			var out, errw string
			var code int
			f.asSession("sess-a", func() { out, errw, code = f.run(t, f.repo, "", "msg", tc.to, "take the router bundle") })
			if code != 0 {
				t.Fatalf("msg: %s %s", out, errw)
			}
			tc.after(t, f)
			var list, one string
			f.asSession("sess-a", func() {
				list, errw, code = f.run(t, f.repo, "", "sent")
				if code == 0 {
					one, errw, code = f.run(t, f.repo, "", "sent", "1")
				}
			})
			if code != 0 {
				t.Fatalf("sent: %s", errw)
			}
			// The report is still one header per send, and one row per
			// addressed session.
			if n := strings.Count(list, "\n"); n != 1 || !strings.HasPrefix(list, "#1 to ") {
				t.Fatalf("sent must list one send on one line, got:\n%s", list)
			}
			var bravo string
			for _, ln := range strings.Split(one, "\n") {
				if strings.HasPrefix(strings.TrimSpace(ln), "bravo") {
					bravo = ln
				}
			}
			if bravo == "" {
				t.Fatalf("control: sent 1 must list bravo:\n%s", one)
			}
			if has := strings.Contains(bravo, wakeHead); has != tc.wake {
				t.Fatalf("bravo's row: wake present=%v, want %v:\n%s", has, tc.wake, one)
			}
			if strings.Count(one, wakeHead) > 1 {
				t.Fatalf("only bravo can be woken (alpha has no socket):\n%s", one)
			}
			if !tc.wake {
				if strings.Contains(list, "wake") {
					t.Fatalf("no wake, so the list must not mention one:\n%s", list)
				}
				return
			}
			if !strings.Contains(bravo, "queued; "+wakeHead+filepath.Join(f.sockDir, "400.sock")) {
				t.Fatalf("the wake must follow bravo's queued standing:\n%s", bravo)
			}
			switch tc.to {
			case "all":
				if !strings.Contains(list, "; 1 addressed session(s) still have it queued and can be woken: buddy sent 1 names each wake address") {
					t.Fatalf("a broadcast counts its wakeable sessions and names the report:\n%s", list)
				}
			default:
				if !strings.Contains(list, "; "+wakeHead) {
					t.Fatalf("a direct send carries its wake on the list line:\n%s", list)
				}
			}
			if strings.Contains(list+one, "router") {
				t.Fatalf("sent prints no body:\n%s%s", list, one)
			}
		})
	}
}

// D-059 (issue #51): Claude Code drops a peer message identical to the
// previous one from the same sender, so a fixed wake text lost the second wake
// to a lane. Each suggestion is now its own text: two sends in the same
// second differ by id, and `sent` naming the same message again differs by
// the time it was named. Neither carries the body.
func TestWakeTextIsUniquePerSuggestion(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.sockDir = shortSockDir(t)
	f.initAndHello(t)
	beatAs(t, f, 400)
	listenSock(t, f.sockDir, 400)
	idleB(t, f)
	f.clock = f.clock.Add(10 * time.Minute)
	sentAt := f.clock

	textRe := regexp.MustCompile(`with the text "([^"]*)"`)
	texts := func(where, s string) []string {
		t.Helper()
		var got []string
		for _, m := range textRe.FindAllStringSubmatch(s, -1) {
			got = append(got, m[1])
		}
		if len(got) == 0 {
			t.Fatalf("%s: no wake text in:\n%s", where, s)
		}
		return got
	}
	var fromMsg []string
	for i, body := range []string{"take the router bundle", "take the parser bundle"} {
		var out, errw string
		var code int
		f.asSession("sess-a", func() { out, errw, code = f.run(t, f.repo, "", "msg", "bravo", body) })
		if code != 0 {
			t.Fatalf("msg %d: %s %s", i+1, out, errw)
		}
		got := texts("msg stdout", out)[0]
		if e := texts("msg stderr", errw)[0]; e != got {
			t.Fatalf("stderr must carry the same wake text as stdout: %q vs %q", e, got)
		}
		if strings.Contains(out+errw, "bundle") {
			t.Fatalf("the wake carries no body:\n%s%s", out, errw)
		}
		fromMsg = append(fromMsg, got)
	}
	// The clock did not move between the two sends: the id alone tells them
	// apart, and each names its own.
	hhmmss := sentAt.Local().Format("15:04:05")
	for i, want := range []string{
		"buddy mail #1 is queued for you (" + hhmmss + "): run buddy inbox",
		"buddy mail #2 is queued for you (" + hhmmss + "): run buddy inbox",
	} {
		if fromMsg[i] != want {
			t.Fatalf("send %d: wake text %q, want %q", i+1, fromMsg[i], want)
		}
	}

	// A re-wake of message #1 from `sent`, 90 s later: same message, a
	// different text, so the harness does not drop it as a repeat.
	f.clock = f.clock.Add(90 * time.Second)
	var one, list, errw string
	var code int
	f.asSession("sess-a", func() {
		one, errw, code = f.run(t, f.repo, "", "sent", "1")
		if code == 0 {
			list, errw, code = f.run(t, f.repo, "", "sent")
		}
	})
	if code != 0 {
		t.Fatalf("sent: %s", errw)
	}
	again := texts("sent 1", one)
	if len(again) != 1 {
		t.Fatalf("sent 1 names one wake (bravo's), got %d:\n%s", len(again), one)
	}
	if want := "buddy mail #1 is queued for you (" + f.clock.Local().Format("15:04:05") + "): run buddy inbox"; again[0] != want || again[0] == fromMsg[0] {
		t.Fatalf("sent's re-wake must be %q, and differ from msg's %q; got %q", want, fromMsg[0], again[0])
	}
	// The list observes bravo ONCE and still names each send's own id on
	// that send's line: a memo of the whole clause would print one send's
	// wake on the other's line.
	if strings.Count(list, "\n") != 2 || strings.Contains(one+list, "bundle") {
		t.Fatalf("want one line per send and no body:\n%s", list)
	}
	now := f.clock.Local().Format("15:04:05")
	for _, ln := range strings.Split(strings.TrimSuffix(list, "\n"), "\n") {
		id := strings.Fields(ln)[0] // "#1", "#2"
		if got := texts("sent list", ln); len(got) != 1 || got[0] != "buddy mail "+id+" is queued for you ("+now+"): run buddy inbox" {
			t.Fatalf("the line for %s must name its own wake, at the time sent ran (%s), got %q:\n%s", id, now, got, list)
		}
	}

	// A correction is a send of its own: its wake names the correction's id,
	// never the id it corrects, on msg and on its sent row.
	var fix, errw2 string
	f.asSession("sess-a", func() {
		fix, errw2, code = f.run(t, f.repo, "", "msg", "bravo", "--supersedes", "1", "take the lexer bundle")
		if code == 0 {
			one, errw2, code = f.run(t, f.repo, "", "sent", "3")
		}
	})
	if code != 0 {
		t.Fatalf("correction: %s %s", fix, errw2)
	}
	want3 := "buddy mail #3 is queued for you (" + now + "): run buddy inbox"
	if got := texts("correction msg", fix); got[0] != want3 {
		t.Fatalf("a correction's wake names its own id: got %q, want %q", got[0], want3)
	}
	if got := texts("sent 3", one); len(got) != 1 || got[0] != want3 {
		t.Fatalf("sent 3 names the correction's own id: got %q, want %q", got, want3)
	}
}
