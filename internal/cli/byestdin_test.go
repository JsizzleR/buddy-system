package cli

import (
	"os"
	"strings"
	"testing"
	"time"
)

// #116: a typed `buddy bye <id>` refused after two seconds whenever its stdin
// was open and empty, which is what an agent harness hands a Bash call whose
// command line holds a heredoc or `<` (a socket, measured on Claude Code), and
// what `sleep 4 | buddy bye <id>` hands it; `echo | buddy bye <id>` refused a
// third way, "parse hook input". None of the refusals named a way out. The read
// stays first (D-069, F-27: an open pipe is never "no hook"); the refusal now
// names the redirect with the id it was given, and the redirect is pinned to
// work. Skipping the read on a positional is OPEN in D-218.

// openPipe is a pipe whose writer stays open until the test ends: the stdin an
// inherited socket or `sleep N |` hands a verb. written goes in first.
func openPipe(t *testing.T, written string) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close(); r.Close() })
	if written != "" {
		if _, err := w.WriteString(written); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

// closedPipe is a pipe holding written whose writer is already closed: what
// `echo |` and `</dev/null` (written empty) hand a verb.
func closedPipe(t *testing.T, written string) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	if _, err := w.WriteString(written); err != nil {
		t.Fatal(err)
	}
	w.Close()
	return r
}

// byeOn runs `buddy bye args...` on stdin and bounds the wait well past the
// verb's own two-second read, so a read that never returns fails the test
// rather than hanging the suite.
func byeOn(t *testing.T, f *fixture, stdin *os.File, args ...string) (string, int) {
	t.Helper()
	type verdict struct {
		errw string
		code int
	}
	done := make(chan verdict, 1)
	go func() {
		_, errw, code := f.runReader(t, f.repo, stdin, append([]string{"bye"}, args...)...)
		done <- verdict{errw, code}
	}()
	select {
	case v := <-done:
		return v.errw, v.code
	case <-time.After(15 * time.Second):
		t.Fatalf("bye %v did not return within 15 s", args)
		return "", 0
	}
}

func sessionEnded(t *testing.T, f *fixture, id string) bool {
	t.Helper()
	out, errw, code := f.run(t, f.repo, "", "who", id)
	if code != 0 {
		t.Fatalf("who %s: %d %s", id, code, errw)
	}
	return strings.Contains(out, "EXIT         already ended")
}

func TestATypedByeOnAnUnreadableStdinNamesTheRedirect(t *testing.T) {
	boundedParallel(t)
	for _, tc := range []struct {
		name string
		in   func(t *testing.T) *os.File
		why  string
	}{
		{"open and empty (a socket, sleep 4 |)", func(t *testing.T) *os.File { return openPipe(t, "") }, "stayed open"},
		{"a stray line, still open (a loop's stdin)", func(t *testing.T) *os.File { return openPipe(t, "next-item\n") }, "stayed open"},
		{"a newline, then closed (echo |)", func(t *testing.T) *os.File { return closedPipe(t, "\n") }, "parse hook input"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.initAndHello(t)
			errw, code := byeOn(t, f, tc.in(t), "sess-a")
			if code == 0 {
				t.Fatal("bytes or an open pipe are never 'no hook' (F-27): the typed bye must still refuse")
			}
			if !strings.Contains(errw, tc.why) || !strings.Contains(errw, "`buddy bye sess-a </dev/null`") {
				t.Fatalf("the refusal must keep its cause and name the redirect with the id given: %q", errw)
			}
			if strings.Count(strings.TrimRight(errw, "\n"), "\n") != 0 {
				t.Fatalf("the refusal must stay one line: %q", errw)
			}
			if sessionEnded(t, f, "sess-a") {
				t.Fatal("a refused bye ended the session")
			}
			// The control: the redirect it names works.
			if errw, code := byeOn(t, f, closedPipe(t, ""), "sess-a"); code != 0 {
				t.Fatalf("the named redirect must end the session: %d %q", code, errw)
			}
			if !sessionEnded(t, f, "sess-a") {
				t.Fatal("bye sess-a </dev/null exited 0 and did not end the session")
			}
		})
	}
}

// The hook form: with no id to paste the remedy names the placeholder, and a
// payload still ends ITS session through the hook path, whatever the argv.
func TestABareByeStillReadsItsHookPayload(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)

	errw, code := byeOn(t, f, openPipe(t, ""))
	if code == 0 || !strings.Contains(errw, "stayed open") || !strings.Contains(errw, "`buddy bye <session id> </dev/null`") {
		t.Fatalf("a bare bye on an open pipe must refuse and name the typed form: %d %q", code, errw)
	}
	if sessionEnded(t, f, "sess-a") || sessionEnded(t, f, "sess-b") {
		t.Fatal("a refused bye ended a session")
	}
	// A payload with a positional beside it: D-069's order, the payload wins.
	if errw, code := byeOn(t, f, closedPipe(t, hookJSON("sess-b", f.wtB, "", "")), "sess-a"); code != 0 {
		t.Fatalf("a hook payload on a closed pipe is the SessionEnd hook: %d %q", code, errw)
	}
	if !sessionEnded(t, f, "sess-b") || sessionEnded(t, f, "sess-a") {
		t.Fatal("the payload must end its own session and not the argument's")
	}
}
