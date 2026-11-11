package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// Codex report B4. Under Codex, hello is never hook-driven (a Codex
// SessionStart into `buddy hello` is refused, D-069), so it delivers nothing
// and counts every queued message. The count line used to promise the rest
// "will arrive after your next tool call", which is Claude's beat; under
// Codex beat is refused too, so nothing ever drained them and the session was
// told to wait for a delivery that never comes. Under Codex the line names
// `buddy inbox`, the verb that does drain them; the Claude line is unchanged.
// Named by --session, the caller may be anyone, and a bare `buddy inbox` with
// two live sessions resolves no one, so that line names the id; each arm runs
// exactly the command its line names.
func TestCodexHelloCountNamesInbox(t *testing.T) {
	boundedParallel(t)
	const claudeLine = "BUDDY: 1 queued message(s) not shown here; they will arrive after your next tool call.\n"
	const codexLine = "BUDDY: 1 queued message(s) not shown here; run `buddy inbox` to read them (no tool call delivers them under Codex).\n"
	const namedLine = "BUDDY: 1 queued message(s) not shown here; no tool call delivers them under Codex: that session runs `buddy inbox --session sess-b`.\n"
	for _, tc := range []struct {
		name, want string
		env        map[string]string
		args       []string
		named      []string // the command the line names, as argv
	}{
		// The positive control: a hand-run hello with no Codex marker keeps the
		// Claude line, whose promise holds there (beat drains on the next call).
		{"claude", claudeLine, nil, []string{"--session", "sess-b"}, nil},
		{"marked", namedLine, map[string]string{EnvHarness: "codex"}, []string{"--session", "sess-b"}, []string{"inbox", "--session", "sess-b"}},
		{"native", codexLine, map[string]string{EnvCodexThread: "sess-b"}, nil, []string{"inbox"}},
		{"native-named", namedLine, map[string]string{EnvCodexThread: "sess-b"}, []string{"--session", "sess-b"}, []string{"inbox", "--session", "sess-b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.initAndHello(t)
			if _, errw, code := f.run(t, f.repo, "", "msg", "bravo", "--from", "ana", "ping"); code != 0 {
				t.Fatal(errw)
			}
			for k, v := range tc.env {
				f.env[k] = v
			}
			out, errw, code := f.run(t, f.wtB, "", append([]string{"hello", "--label", "bravo"}, tc.args...)...)
			if code != 0 {
				t.Fatal(errw)
			}
			if !strings.HasSuffix(out, tc.want) {
				t.Fatalf("the count line:\n got %q\nwant suffix %q", out, tc.want)
			}
			if n := f.undeliveredTo(t, "sess-b", "bravo"); n != 1 {
				t.Fatalf("a hand-run hello marked %d message(s) delivered", 1-n)
			}
			if tc.env == nil {
				return
			}
			// The premise of the Codex line: a Codex tool call's beat drains
			// nothing, so only the verb the line names delivers the message.
			if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Bash", ""), "beat"); code == 0 || !strings.Contains(errw, "unsupported") {
				t.Fatalf("beat under Codex must be refused: %d %s", code, errw)
			}
			if n := f.undeliveredTo(t, "sess-b", "bravo"); n != 1 {
				t.Fatalf("a refused beat delivered %d message(s)", 1-n)
			}
			// Exactly what the line names. Bare, with no id in the environment
			// and two live sessions, it refuses (the review's input): why the
			// --session line names the id.
			if tc.name == "marked" {
				if _, _, code := f.run(t, f.wtB, "", "inbox"); code == 0 {
					t.Fatal("a bare inbox with no id and two live sessions resolved someone; the named line would be unneeded")
				}
			}
			if out, errw, code := f.run(t, f.wtB, "", tc.named...); code != 0 || !strings.Contains(out, "] ping\n") {
				t.Fatalf("buddy inbox must deliver what the line names: %d %q %s", code, out, errw)
			}
			if n := f.undeliveredTo(t, "sess-b", "bravo"); n != 0 {
				t.Fatalf("inbox left %d queued", n)
			}
		})
	}
}

// waitNotice's Codex wording ("schedule no further check") was unreachable:
// its one caller is cmdBeat, which refuses any Codex hook (JSON marker,
// BUDDY_HARNESS, or CODEX_THREAD_ID: classifyHook sets h.Harness from each)
// before it reaches the notice, so the flag it passed was always false. The
// arm is gone; this pins the premise. Under every Codex marker a beat with a
// LANDED wait writes nothing and consumes nothing: the notice is still unread
// when a Claude beat (the positive control) reaches it, and `buddy wait check`
// is where a Codex session reads the verdict (TestCodexHookWaitAdvice).
func TestCodexBeatNeverReachesWaitNotice(t *testing.T) {
	boundedParallel(t)
	beat := func(w *waitFx, extra map[string]any) (string, string, int) {
		in := map[string]any{"session_id": "sess-a", "cwd": w.repo, "tool_name": "Bash", "tool_input": map[string]any{}}
		for k, v := range extra {
			in[k] = v
		}
		b, _ := json.Marshal(in)
		return w.run(t, w.repo, string(b), "beat")
	}
	for _, marker := range []string{"json", "environment", "native"} {
		t.Run(marker, func(t *testing.T) {
			w := newWaitFx(t)
			w.ok(t, "sess-a", "wait", "--on", "api-work")
			w.ok(t, "sess-b", "release", "api-work")
			var extra map[string]any
			switch marker {
			case "json":
				extra = map[string]any{"buddy_harness": "codex"}
			case "environment":
				w.env[EnvHarness] = "codex"
			case "native":
				w.env[EnvCodexThread] = "sess-a"
			}
			out, errw, code := beat(w, extra)
			if code == 0 || !strings.Contains(errw, "unsupported") || strings.Contains(out, "LANDED") {
				t.Fatalf("a Codex beat must be refused before the notice: %d %q %s", code, out, errw)
			}
			delete(w.env, EnvHarness)
			delete(w.env, EnvCodexThread)
			out, errw, code = beat(w, nil)
			if code != 0 {
				t.Fatal(errw)
			}
			if ctx := additionalContext(t, out); !strings.Contains(ctx, "your wait LANDED") || !strings.Contains(ctx, "then stop the /loop that runs it") {
				t.Fatalf("the Claude beat must still carry the unconsumed notice:\n%s", ctx)
			}
		})
	}
}
