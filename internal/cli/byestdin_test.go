package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// #116 (D-218): a typed `buddy bye <id>` waited two seconds and refused
// whenever its stdin was open, which is what an agent harness hands a Bash
// call whose command line holds a heredoc or `<` (a socket, measured on Claude
// Code), and what `sleep 4 | buddy bye <id>` hands it; `echo | buddy bye <id>`
// refused a third way, "parse hook input". D-218 made the refusal name
// `</dev/null`; D-234 skips the read for a typed bye whose caller cannot be a
// hook line (cmdBye's comment names each condition), so a coordinator's
// `buddy bye <lane id>` takes no redirect. Where a hook line could be calling,
// the read stays first (D-069, F-27: an open pipe is never "no hook") and the
// D-218 remedy stands; those arms are pinned here beside the skip.

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
// rather than hanging the suite. It returns how long the verb took.
func byeOn(t *testing.T, f *fixture, stdin *os.File, args ...string) (string, int, time.Duration) {
	t.Helper()
	type verdict struct {
		out, errw string
		code      int
	}
	done := make(chan verdict, 1)
	start := time.Now()
	go func() {
		out, errw, code := f.runReader(t, f.repo, stdin, append([]string{"bye"}, args...)...)
		done <- verdict{out, errw, code}
	}()
	select {
	case v := <-done:
		// bye writes nothing to stdout in any arm: a refusal is stderr, an
		// end is silent (Codex code pass: the hook's silence went unchecked).
		if v.out != "" {
			t.Fatalf("bye %v wrote to stdout: %q", args, v.out)
		}
		return v.errw, v.code, time.Since(start)
	case <-time.After(15 * time.Second):
		t.Fatalf("bye %v did not return within 15 s", args)
		return "", 0, 0
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

// A typed bye naming another session reads no stdin: whatever is there (an
// open pipe, a stray line, a newline, even a payload naming a different
// session) it ends the session it names, at once. The duration bound is what
// tells a skipped read from one that ran and timed out.
func TestATypedByeReadsNoStdin(t *testing.T) {
	boundedParallel(t)
	for _, tc := range []struct {
		name string
		in   func(t *testing.T, f *fixture) *os.File
	}{
		{"open and empty (a socket, sleep 4 |)", func(t *testing.T, f *fixture) *os.File { return openPipe(t, "") }},
		{"a stray line, still open (a loop's stdin)", func(t *testing.T, f *fixture) *os.File { return openPipe(t, "next-item\n") }},
		{"a newline, then closed (echo |)", func(t *testing.T, f *fixture) *os.File { return closedPipe(t, "\n") }},
		{"another session's payload", func(t *testing.T, f *fixture) *os.File {
			return closedPipe(t, hookJSON("sess-b", f.wtB, "", ""))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.initAndHello(t)
			f.env[EnvClaudeSession] = "coordinator" // the caller is some other session
			if sessionEnded(t, f, "sess-a") {
				t.Fatal("control: sess-a must start live")
			}
			errw, code, took := byeOn(t, f, tc.in(t, f), "sess-a")
			if code != 0 || errw != "" {
				t.Fatalf("a typed bye of an unbound session must end it: %d %q", code, errw)
			}
			if took >= hookInputWait {
				t.Fatalf("the typed bye took %v: it waited on stdin", took)
			}
			if !sessionEnded(t, f, "sess-a") || sessionEnded(t, f, "sess-b") {
				t.Fatal("the typed bye must end the session it names and only that one")
			}
		})
	}
}

// Skipping the read grants nothing `</dev/null` did not: no process ref, so a
// live registered process still refuses it, and promptly.
func TestASkippedReadIsStillRefusedByALiveProcess(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	f.asProcess(42, 100, func() {
		if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello"); code != 0 {
			t.Fatal(errw)
		}
	})
	errw, code, took := byeOn(t, f, openPipe(t, ""), "sess-a")
	if code == 0 || !strings.Contains(errw, "stays live") || !strings.Contains(errw, "42") {
		t.Fatalf("a live registration must refuse a typed bye: %d %q", code, errw)
	}
	if took >= hookInputWait {
		t.Fatalf("the refused typed bye took %v: it waited on stdin", took)
	}
	if sessionEnded(t, f, "sess-a") {
		t.Fatal("a refused bye ended the session")
	}
	// The control: once the process is gone the same line ends it.
	f.kill(42)
	if errw, code, _ := byeOn(t, f, openPipe(t, ""), "sess-a"); code != 0 || !sessionEnded(t, f, "sess-a") {
		t.Fatalf("with its process gone the typed bye must end it: %d %q", code, errw)
	}
}

// Where the caller could be a hook line, the read stays first: an open pipe
// still refuses after the bound and names the redirect, and the redirect it
// names works (the positive control that the arm was reached, not skipped).
func TestATypedByeKeepsTheReadWhereAHookCouldBeCalling(t *testing.T) {
	boundedParallel(t)
	for _, tc := range []struct {
		name   string
		env    map[string]string
		args   []string
		remedy string
	}{
		{"its own Claude id", map[string]string{EnvClaudeSession: "sess-a"}, []string{"sess-a"}, "`buddy bye sess-a </dev/null`"},
		{"its own BUDDY_SESSION", map[string]string{EnvSession: "sess-a"}, []string{"sess-a"}, "`buddy bye sess-a </dev/null`"},
		{"its own CODEX_SESSION_ID", map[string]string{"CODEX_SESSION_ID": "sess-a"}, []string{"sess-a"}, "`buddy bye sess-a </dev/null`"},
		{"--force", nil, []string{"sess-a", "--force"}, "`buddy bye sess-a --force </dev/null`"},
		{"the Codex hook marker", map[string]string{EnvHarness: "codex"}, []string{"sess-a"}, "`buddy bye sess-a </dev/null`"},
		{"any harness marker", map[string]string{EnvHarness: "claude"}, []string{"sess-a"}, "`buddy bye sess-a </dev/null`"},
		{"a Codex thread id, another one", map[string]string{EnvCodexThread: "thr-other"}, []string{"sess-a"}, "`buddy bye sess-a </dev/null`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.initAndHello(t)
			for k, v := range tc.env {
				f.env[k] = v
			}
			errw, code, _ := byeOn(t, f, openPipe(t, ""), tc.args...)
			if code == 0 || !strings.Contains(errw, "stayed open") || !strings.Contains(errw, tc.remedy) {
				t.Fatalf("the read must run and its refusal name %s: %d %q", tc.remedy, code, errw)
			}
			if strings.Count(strings.TrimRight(errw, "\n"), "\n") != 0 {
				t.Fatalf("the refusal must stay one line: %q", errw)
			}
			if sessionEnded(t, f, "sess-a") {
				t.Fatal("a refused bye ended the session")
			}
			if errw, code, _ := byeOn(t, f, closedPipe(t, ""), tc.args...); code != 0 || !sessionEnded(t, f, "sess-a") {
				t.Fatalf("the named redirect must end the session: %d %q", code, errw)
			}
		})
	}
}

// The failing inputs the refuted draft (skip on any positional) admitted, each
// with its control.
func TestAHookLineWithAnArgumentIsStillAHook(t *testing.T) {
	boundedParallel(t)

	// A marked Codex SessionEnd line passing the thread id it took from the
	// payload, delivered after the thread was begun again: a logical session
	// has no process to refuse a manual bye, so a skipped read would end the
	// live successor with no token (D-069). The read classifies it Codex.
	t.Run("a marked Codex hook naming a logical session", func(t *testing.T) {
		f := newFixture(t)
		if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
			t.Fatal(errw)
		}
		if _, errw, code := f.run(t, f.repo, "", "lifecycle", "begin", "--session", "thr-1", "--harness", "codex"); code != 0 {
			t.Fatal(errw)
		}
		f.env[EnvHarness] = "codex"
		errw, code, _ := byeOn(t, f, closedPipe(t, hookJSON("thr-1", f.repo, "", "")), "thr-1")
		if code == 0 || !strings.Contains(errw, "unfenced Codex bye") {
			t.Fatalf("a marked Codex hook must refuse: %d %q", code, errw)
		}
		if sessionEnded(t, f, "thr-1") {
			t.Fatal("a Codex hook line ended a live logical session without its token")
		}
		// The control: the operator's typed bye, unmarked, still ends it (the
		// manual authority GIVEN 70 keeps).
		delete(f.env, EnvHarness)
		if errw, code, _ := byeOn(t, f, openPipe(t, ""), "thr-1"); code != 0 || !sessionEnded(t, f, "thr-1") {
			t.Fatalf("a typed bye must end a logical session: %d %q", code, errw)
		}
	})

	// A hook line `bye <id> --force` with its payload: a hook is never forced,
	// so the live registration still holds it.
	t.Run("--force on a hook line", func(t *testing.T) {
		f := newFixture(t)
		if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
			t.Fatal(errw)
		}
		f.asProcess(42, 100, func() {
			if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello"); code != 0 {
				t.Fatal(errw)
			}
		})
		errw, code, _ := byeOn(t, f, closedPipe(t, hookJSON("sess-a", f.repo, "", "")), "sess-a", "--force")
		if code != 0 || !strings.Contains(errw, "stays live") {
			t.Fatalf("the hook's refusal is a stderr note and exit 0: %d %q", code, errw)
		}
		if sessionEnded(t, f, "sess-a") {
			t.Fatal("a hook line's --force ended a session under a live registration")
		}
		// The control: the typed --force, no payload, is the operator's act.
		if errw, code, _ := byeOn(t, f, closedPipe(t, ""), "sess-a", "--force"); code != 0 || !sessionEnded(t, f, "sess-a") {
			t.Fatalf("a typed --force must end it: %d %q", code, errw)
		}
	})
}

// The read path: a bare bye still reads its payload; a payload naming a
// session other than the argument is refused, never silently preferred.
func TestABareByeStillReadsItsHookPayload(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)

	errw, code, _ := byeOn(t, f, openPipe(t, ""))
	if code == 0 || !strings.Contains(errw, "stayed open") || !strings.Contains(errw, "`buddy bye <session id> </dev/null`") {
		t.Fatalf("a bare bye on an open pipe must refuse and name the typed form: %d %q", code, errw)
	}
	if sessionEnded(t, f, "sess-a") || sessionEnded(t, f, "sess-b") {
		t.Fatal("a refused bye ended a session")
	}

	f.env[EnvClaudeSession] = "sess-a" // the own-id arm: the read runs
	errw, code, _ = byeOn(t, f, closedPipe(t, hookJSON("sess-b", f.wtB, "", "")), "sess-a")
	if code == 0 || !strings.Contains(errw, "disagree") {
		t.Fatalf("a payload naming another session than the argument must refuse: %d %q", code, errw)
	}
	if sessionEnded(t, f, "sess-a") || sessionEnded(t, f, "sess-b") {
		t.Fatal("a disagreeing bye ended a session")
	}
	// The controls: an agreeing payload, and a bare one, each end their own.
	if errw, code, _ := byeOn(t, f, closedPipe(t, hookJSON("sess-a", f.repo, "", "")), "sess-a"); code != 0 || !sessionEnded(t, f, "sess-a") {
		t.Fatalf("an agreeing payload is the SessionEnd hook: %d %q", code, errw)
	}
	delete(f.env, EnvClaudeSession)
	if errw, code, _ := byeOn(t, f, closedPipe(t, hookJSON("sess-b", f.wtB, "", ""))); code != 0 || !sessionEnded(t, f, "sess-b") {
		t.Fatalf("a bare payload is the SessionEnd hook: %d %q", code, errw)
	}
}

// Argv is judged before any read: a payload behind a bad argv no longer turns
// it into the hook (the closed pipe), and the refusal does not wait for stdin
// (the open one, which a read-first order would hold for the bound; Codex
// code pass).
func TestByeJudgesItsArgumentsFirst(t *testing.T) {
	boundedParallel(t)
	stdins := []struct {
		name string
		in   func(t *testing.T, f *fixture) *os.File
	}{
		{"behind a payload", func(t *testing.T, f *fixture) *os.File { return closedPipe(t, hookJSON("sess-a", f.repo, "", "")) }},
		{"on an open pipe", func(t *testing.T, f *fixture) *os.File { return openPipe(t, "") }},
	}
	for _, tc := range []struct {
		name string
		args []string
		why  string
	}{
		{"an unknown flag", []string{"--bogus"}, "usage: buddy bye"},
		{"an empty id", []string{""}, "empty session id"},
		{"an empty id before a real one", []string{"", "sess-a"}, "empty session id"},
		{"two ids", []string{"sess-a", "sess-b"}, "takes one session"},
	} {
		for _, in := range stdins {
			t.Run(tc.name+", "+in.name, func(t *testing.T) {
				f := newFixture(t)
				f.initAndHello(t)
				errw, code, took := byeOn(t, f, in.in(t, f), tc.args...)
				if code == 0 || !strings.Contains(errw, tc.why) {
					t.Fatalf("want a refusal naming %q: %d %q", tc.why, code, errw)
				}
				if took >= hookInputWait {
					t.Fatalf("an argv refusal took %v: it waited on stdin", took)
				}
				lines := strings.Split(strings.TrimRight(errw, "\n"), "\n")
				if !strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "refused: ") || !strings.Contains(errw, "usage: buddy bye") {
					t.Fatalf("a usage refusal is the usage line, then `refused: <reason>` last (D-098): %q", errw)
				}
				if sessionEnded(t, f, "sess-a") || sessionEnded(t, f, "sess-b") {
					t.Fatal("a refused bye ended a session")
				}
			})
		}
	}
}

// U1 (D-234): a typed bye that ends nothing because the ledger has no such
// row refuses; one whose row already ended stays exit 0; the hook stays
// silent.
func TestATypedByeOfAnUnknownIdRefuses(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)

	errw, code, _ := byeOn(t, f, closedPipe(t, ""), "alpha") // sess-a's LABEL
	if code == 0 || !strings.Contains(errw, "no session alpha in this directory's ledger") || !strings.Contains(errw, "`buddy who <name>`") {
		t.Fatalf("a label is no session id and must refuse: %d %q", code, errw)
	}
	if sessionEnded(t, f, "sess-a") {
		t.Fatal("bye alpha ended sess-a")
	}
	// Ended: idempotent, the fleet skill's "run it again".
	for i := 0; i < 2; i++ {
		if errw, code, _ := byeOn(t, f, closedPipe(t, ""), "sess-a"); code != 0 || errw != "" {
			t.Fatalf("bye #%d of sess-a: %d %q", i+1, code, errw)
		}
	}
	// The read path's typed arm (--force keeps the read) refuses an absent id too.
	if errw, code, _ := byeOn(t, f, closedPipe(t, ""), "nope", "--force"); code == 0 || !strings.Contains(errw, "no session nope") {
		t.Fatalf("a typed --force of an absent id must refuse: %d %q", code, errw)
	}
	// The hook: a SessionEnd for a session this ledger never registered.
	if errw, code, _ := byeOn(t, f, closedPipe(t, hookJSON("never", f.repo, "", ""))); code != 0 || errw != "" {
		t.Fatalf("the hook must stay silent: %d %q", code, errw)
	}
}

// The third state: ByeFrom found no live row, then a hello landed before the
// point query. The live row must be reported as such, never as ended.
func TestByeUnknownSeesARowThatTurnedLive(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	st, err := store.Open(filepath.Join(f.repo, ".git", "buddy.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	err = byeUnknown(st, "sess-a", Env{})
	if err == nil || !strings.Contains(err.Error(), "registered while this bye ran and is live") || strings.Contains(err.Error(), "run it again") {
		t.Fatalf("a live row must refuse, never inviting a second bye: %v", err)
	}
	if err := byeUnknown(st, "nope", Env{}); err == nil || !strings.Contains(err.Error(), "no session nope") {
		t.Fatalf("an absent row must refuse: %v", err)
	}
	if _, errw, code := f.run(t, f.repo, "", "bye", "sess-a"); code != 0 {
		t.Fatal(errw)
	}
	if err := byeUnknown(st, "sess-a", Env{}); err != nil {
		t.Fatalf("an ended row is exit 0: %v", err)
	}
}
