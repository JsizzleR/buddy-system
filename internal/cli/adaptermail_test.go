package cli

// Regressions for the seams between the legacy mail views and the schema-15
// adapter channel, from the 2026-10-06 review (F-12, F-24, F-25, F-26, F-27 in
// docs/CODEX-ORCHESTRATOR-FINDINGS.md). Each failed against the tree before its
// fix, and each carries the legacy behaviour it must leave alone as a control.

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func queueAdapter(t *testing.T, f *fixture, sender, recipient lifecycleReceiptForTest, body string) int64 {
	t.Helper()
	q := adapterOK(t, f, "queue", "--session", sender.SessionID, "--incarnation", sender.Incarnation,
		"--to", recipient.SessionID, "--to-incarnation", recipient.Incarnation, "--body", body)
	return q.Delivery.MessageID
}

// F-24: `who` and a send's result line used to count only the legacy inbox,
// so three queued adapter messages read as "0 undelivered", and the send
// promised a "next tool call" delivery no adapter worker performs.
func TestLegacyViewsReportAdapterMail(t *testing.T) {
	boundedParallel(t)
	f, sender, recipient := adapterFixture(t)
	// Control first: before any adapter mail, the session reads like any other.
	out, _, _ := f.run(t, f.repo, "", "who", recipient.SessionID)
	if !strings.Contains(out, "ADAPTER      (none)") {
		t.Fatalf("an empty adapter register must say so: %s", out)
	}
	if out, errw, code := f.run(t, f.repo, "", "msg", recipient.SessionID, "--from", "op", "first"); code != 0 ||
		!strings.Contains(out, "delivery waits for its next tool call") {
		t.Fatalf("legacy wording control: %d %q %q", code, out, errw)
	}
	f.clock = f.clock.Add(2 * time.Minute)
	for i := 0; i < 3; i++ {
		queueAdapter(t, f, sender, recipient, fmt.Sprintf("assignment %d", i))
	}
	f.clock = f.clock.Add(5 * time.Minute)
	out, _, _ = f.run(t, f.repo, "", "who", recipient.SessionID)
	for _, want := range []string{"INBOX        1 undelivered", "ADAPTER      3 queued for its adapter, the oldest 5m old"} {
		if !strings.Contains(out, want) {
			t.Fatalf("who must report both channels, separately (%q): %s", want, out)
		}
	}
	out, errw, code := f.run(t, f.repo, "", "msg", recipient.SessionID, "--from", "op", "second")
	if code != 0 {
		t.Fatalf("msg: %d %s", code, errw)
	}
	for _, want := range []string{"3 adapter message(s) queued for its adapter", "reads mail through an adapter", "1 earlier message(s) to it still undelivered"} {
		if !strings.Contains(out, want) {
			t.Fatalf("send note must say %q: %s", want, out)
		}
	}
	if strings.Contains(out, "next tool call") {
		t.Fatalf("no legacy drain runs for an adapter recipient: %s", out)
	}
}

// V-05: delivery consumes the backlog, not the evidence that this incarnation
// receives adapter mail. Codex has no Claude endpoint to keep that fact alive.
func TestDeliveredAdapterMailRetainsRecipientEvidence(t *testing.T) {
	f, sender, recipient := adapterFixture(t)
	plain := func() string {
		out, errw, code := f.run(t, f.repo, "", "msg", recipient.SessionID, "--from", "op", "next")
		if code != 0 {
			t.Fatalf("plain send: %d %s", code, errw)
		}
		return out
	}
	if out := plain(); !strings.Contains(out, "next tool call") {
		t.Fatalf("unclassified positive control: %s", out)
	}
	id := queueAdapter(t, f, sender, recipient, "assignment")
	adapterOK(t, f, "delivered", "--session", recipient.SessionID, "--incarnation", recipient.Incarnation, "--message", fmt.Sprint(id))
	if out := plain(); !strings.Contains(out, "reads mail through an adapter") || strings.Contains(out, "next tool call") || strings.Contains(out, "adapter message(s) queued") {
		t.Fatalf("delivered adapter history lost or counted as backlog: %s", out)
	}
}

// V-10: adapter evidence belongs to its captured incarnation. A later legacy
// recipient must get its socket wake, but an old adapter message never may.
func TestEarlierAdapterIncarnationDoesNotSuppressLegacyWake(t *testing.T) {
	for _, endpoint := range []bool{false, true} {
		t.Run(fmt.Sprint(endpoint), func(t *testing.T) {
			f, sender, recipient := adapterFixture(t)
			f.sockDir = shortSockDir(t)
			var id int64
			if endpoint {
				f.env[EnvClaudeSession] = recipient.SessionID
				f.asProcess(400, 1000, func() {
					adapterOK(t, f, "endpoint", "--session", recipient.SessionID, "--incarnation", recipient.Incarnation)
				})
				delete(f.env, EnvClaudeSession)
				f.kill(400)
			} else {
				id = queueAdapter(t, f, sender, recipient, "old assignment")
			}
			lifecycleOK(t, f, "end", "--session", recipient.SessionID, "--incarnation", recipient.Incarnation)
			f.asProcess(401, 2000, func() {
				if _, errw, code := f.run(t, f.repo, hookJSON(recipient.SessionID, f.repo, "", ""), "hello", "--label", "legacy-successor"); code != 0 {
					t.Fatal(errw)
				}
			})
			listenSock(t, f.sockDir, 401)
			out, errw, code := f.run(t, f.repo, "", "msg", recipient.SessionID, "--from", "op", "plain successor message")
			if code != 0 || !strings.Contains(out, "SendMessage") || strings.Contains(out, "reads mail through an adapter") {
				t.Errorf("earlier incarnation suppressed current legacy wake: %d %s %s", code, out, errw)
			}
			if id != 0 {
				f.env[EnvCodexThread] = sender.SessionID
				for _, args := range [][]string{{"sent"}, {"sent", fmt.Sprint(id)}} {
					out, errw, code := f.run(t, f.repo, "", args...)
					if code != 0 || strings.Contains(out, "SendMessage") || !strings.Contains(out, "an adapter message") {
						t.Errorf("old adapter message offered legacy wake: %v %d %s %s", args, code, out, errw)
					}
				}
			}
		})
	}
}

// F-25: a plain correction of an adapter message reached only the legacy
// inbox, which can never show the original.
func TestLegacyCorrectionOfAdapterMailRefused(t *testing.T) {
	boundedParallel(t)
	f, sender, recipient := adapterFixture(t)
	id := queueAdapter(t, f, sender, recipient, "assignment")
	f.env[EnvCodexThread] = sender.SessionID
	for _, dry := range []bool{true, false} {
		args := []string{"msg", recipient.SessionID, "--supersedes", fmt.Sprint(id), "ignore that"}
		if dry {
			args = append(args, "--dry-run")
		}
		if _, errw, code := f.run(t, f.repo, "", args...); code == 0 || !strings.Contains(errw, "adapter message") {
			t.Fatalf("dry=%v: a legacy correction of adapter mail must refuse: %d %q", dry, code, errw)
		}
	}
	// Control: the same sender corrects its own PLAIN message to the same
	// recipient, so the refusal is about the channel and nothing else.
	out, errw, code := f.run(t, f.repo, "", "msg", recipient.SessionID, "plain")
	if code != 0 {
		t.Fatalf("plain send: %s", errw)
	}
	var plain int64
	if _, err := fmt.Sscanf(out[strings.Index(out, "message #")+len("message #"):], "%d", &plain); err != nil {
		t.Fatalf("no message id in %q", out)
	}
	if _, errw, code := f.run(t, f.repo, "", "msg", recipient.SessionID, "--supersedes", fmt.Sprint(plain), "fixed"); code != 0 {
		t.Fatalf("correcting a plain message must still work: %s", errw)
	}
}

// F-12: an endpoint registration puts the receiving Claude pid in the process
// register, which made the legacy wake rule offer "run buddy inbox" for
// adapter mail that `buddy inbox` cannot show.
func TestNoLegacyWakeForAdapterMailOrRecipients(t *testing.T) {
	boundedParallel(t)
	f, sender, recipient := adapterFixture(t)
	f.sockDir = shortSockDir(t)
	if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "", ""), "hello", "--label", "bravo"); code != 0 {
		t.Fatal(errw)
	}
	id := queueAdapter(t, f, sender, recipient, "assignment")
	f.env[EnvClaudeSession] = recipient.SessionID
	f.proc.PID, f.proc.Born = 400, 1000
	f.alive[400] = 1000
	adapterOK(t, f, "endpoint", "--session", recipient.SessionID, "--incarnation", recipient.Incarnation)
	delete(f.env, EnvClaudeSession)
	f.proc.PID, f.proc.Born = 0, 0
	listenSock(t, f.sockDir, 400)
	// Control: a legacy recipient in the same conditions IS offered a wake.
	beatAs(t, f, 401)
	listenSock(t, f.sockDir, 401)
	idleB(t, f)
	f.clock = f.clock.Add(31 * time.Minute)
	if out, _, _ := f.run(t, f.repo, "", "msg", "bravo", "--from", "op", "legacy"); !strings.Contains(out, "SendMessage") {
		t.Fatalf("legacy wake control: %s", out)
	}
	f.env[EnvCodexThread] = sender.SessionID
	out, _, _ := f.run(t, f.repo, "", "sent", fmt.Sprint(id))
	if strings.Contains(out, "SendMessage") || !strings.Contains(out, "an adapter message") {
		t.Fatalf("sent must name adapter mail and offer no legacy wake: %s", out)
	}
	delete(f.env, EnvCodexThread)
	if out, _, _ := f.run(t, f.repo, "", "msg", recipient.SessionID, "--from", "op", "plain"); strings.Contains(out, "SendMessage") {
		t.Fatalf("an adapter recipient is woken through its adapter, never a socket: %s", out)
	}
	if out, _, _ := f.run(t, f.repo, "", "who", recipient.SessionID); !strings.Contains(out, "a receiving endpoint is registered") {
		t.Fatalf("who must show the endpoint: %s", out)
	}
}

// F-26: lifecycle beats passed no path, so a worker patching its claimed file
// every ten minutes showed that claim STALE.
func TestLifecycleHookRenewsObservedClaimsOnly(t *testing.T) {
	boundedParallel(t)
	f, worker := lifecycleFixture(t)
	f.env[EnvCodexThread] = worker.SessionID
	for _, c := range [][]string{{"work", "README"}, {"other", "docs"}} {
		if _, errw, code := f.run(t, f.repo, "", "claim", c[0], "--desc", "d", "--scope", c[1]); code != 0 {
			t.Fatal(errw)
		}
	}
	delete(f.env, EnvCodexThread)
	hook := func(event, tool, input string) {
		t.Helper()
		payload := fmt.Sprintf(`{"session_id":%q,"cwd":%q,"hook_event_name":%q,"tool_name":%q,"tool_input":%s}`,
			worker.SessionID, f.repo, event, tool, input)
		if out, errw, code := f.run(t, f.repo, payload, "lifecycle", "hook", "--incarnation", worker.Incarnation); code != 0 {
			t.Fatalf("%s: %d %s %s", event, code, out, errw)
		}
	}
	patch := `{"command":"*** Begin Patch\n*** Update File: README\n@@\n-x\n+y\n*** End Patch\n"}`
	for i := 0; i < 4; i++ {
		f.clock = f.clock.Add(10 * time.Minute)
		hook("PostToolUse", "apply_patch", patch)
		// A generic busy event renews nothing, and must not mask that.
		hook("UserPromptSubmit", "", `{}`)
	}
	out, _, _ := f.run(t, f.repo, "", "ls")
	var work, other string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "work "):
			work = line
		case strings.HasPrefix(line, "other "):
			other = line
		}
	}
	// The renewal is ls's age column. STALE no longer marks it: the worker
	// is live, and ls marks a claim STALE only when its HOLDER is quiet
	// (D-090).
	if work == "" || !strings.Contains(work, " 0s  ") {
		t.Fatalf("the patched file's claim must renew: %q\n%s", work, out)
	}
	if other == "" || !strings.Contains(other, " 40m  ") {
		t.Fatalf("control: a claim no observed path covers must still age: %q\n%s", other, out)
	}
}

// F-27: `hello --session` reads non-TTY stdin as a hook's, and refuses what is
// not one (D-069). The refusal now names the hand-run recipe.
func TestHelloSessionStdinRefusalNamesRecipe(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := f.run(t, f.repo, "second-line-of-a-loop\n", "hello", "--session", "manual-x"); code == 0 || !strings.Contains(errw, "</dev/null") {
		t.Fatalf("non-hook bytes must refuse and name the recipe: %d %q", code, errw)
	}
	if _, errw, code := f.run(t, f.repo, "", "hello", "--session", "manual-x"); code != 0 {
		t.Fatalf("empty stdin is the manual registration: %s", errw)
	}
}

// F-27, the half the first regression left out (audit A-10, 2026-10-07): the
// closure text asks that an OPEN pipe be covered as well as malformed bytes.
// A strings.Reader always reaches EOF, so the test above never asked what a
// hook verb does on stdin whose writer stays open — which is what a hand-run
// `hello --session` inherits inside a tool shell or a script loop. Measured:
// decodeHook read to EOF with no bound, so the registration hung until the
// writer closed (3.0 s in the audit's probe; a tool's whole timeout in
// practice), and nothing named the `</dev/null` recipe.
//
// The verdict wanted is a bounded REFUSAL, not a manual registration: F-27
// rejects any fallback from unreadable input to manual authority, and a pipe
// that has not closed may still be carrying a delayed hook's payload (D-069).
// The bound costs a real hook nothing — the select returns the moment the
// harness closes stdin — which the prompt-payload control pins.
func TestHelloSessionOpenPipeIsRefusedWithinTheBound(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	type verdict struct {
		errw string
		code int
	}
	// hello runs the verb on an open pipe (bytes written, writer held open)
	// and waits a bound well past the one the verb is meant to honour, so a
	// read that never returns is a failure, not a hung suite.
	hello := func(t *testing.T, written string) verdict {
		t.Helper()
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		// Closing the writer last frees the reader a refused verb left
		// behind; the pipe outlives the verdict on purpose.
		t.Cleanup(func() { w.Close(); r.Close() })
		if written != "" {
			if _, err := w.WriteString(written); err != nil {
				t.Fatal(err)
			}
		}
		done := make(chan verdict, 1)
		go func() {
			_, errw, code := f.runReader(t, f.repo, r, "hello", "--session", "manual-x")
			done <- verdict{errw, code}
		}()
		select {
		case v := <-done:
			return v
		case <-time.After(15 * time.Second):
			t.Fatalf("hello --session on an open pipe (%q written) did not return within 15 s", written)
			return verdict{}
		}
	}
	for _, written := range []string{"", "second-line-of-a-loop\n"} {
		v := hello(t, written)
		if v.code == 0 || !strings.Contains(v.errw, "</dev/null") {
			t.Fatalf("an open pipe (%q written) must refuse and name the recipe: %d %q", written, v.code, v.errw)
		}
	}
	// A refusal registers nothing: the open pipe is not the manual path.
	if _, _, code := f.run(t, f.repo, "", "who", "manual-x"); code == 0 {
		t.Fatal("a refused hello --session must not have registered the session")
	}
	// Control 1: the same pipe, writer closed with nothing written, is the
	// manual registration — EOF is the recipe.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if _, errw, code := f.runReader(t, f.repo, r, "hello", "--session", "manual-x"); code != 0 {
		t.Fatalf("a closed empty pipe is the manual registration: %s", errw)
	}
	r.Close()
	// Control 2: a hook's payload, written whole and closed the way a harness
	// does it, registers at once — the bound is never charged to a prompt
	// payload.
	r, w, err = os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(hookJSON("hooked-x", f.repo, "", "")); err != nil {
		t.Fatal(err)
	}
	w.Close()
	start := time.Now()
	if _, errw, code := f.runReader(t, f.repo, r, "hello"); code != 0 {
		t.Fatalf("a hook payload on a pipe is a hook-driven hello: %s", errw)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("a complete hook payload must not wait out the open-pipe bound: took %v", took)
	}
	r.Close()
}
