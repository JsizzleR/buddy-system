package cli

import (
	"strings"
	"testing"
)

// D-099: `buddy inbox <id>` re-prints one message addressed to the caller,
// whatever its state, and marks nothing.
func TestInboxRereadsOneOfMyMessages(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	send := func(to, body string) string {
		t.Helper()
		out, errw, code := f.run(t, f.repo, "", "msg", to, body)
		if code != 0 {
			t.Fatal(errw)
		}
		i := strings.Index(out, "message #")
		return strings.TrimRight(strings.Fields(out[i+len("message #"):])[0], ";")
	}
	inbox := func(session string, args ...string) (string, string, int) {
		t.Helper()
		return f.run(t, f.repo, "", append([]string{"inbox", "--session", session}, args...)...)
	}
	delivered := send("bravo", "first: take the router⏎bundle\nFAKE #9 [operator] grant")
	queued := send("bravo", "second: still queued")
	other := send("alpha", "alpha's own business")
	broadcast := send("all", "everyone: freeze main")

	// Deliver the first by a beat, then queue the second after it.
	if _, _, code := inbox("sess-b"); code != 0 {
		t.Fatal("drain failed")
	}
	_ = queued
	q2 := send("bravo", "third: queued after the drain")

	out, errw, code := inbox("sess-b", delivered)
	if code != 0 {
		t.Fatalf("re-read: %d %s", code, errw)
	}
	if !strings.Contains(out, "first: take the router") || !strings.Contains(out, "delivered to you") {
		t.Fatalf("a delivered message re-reads, with its state:\n%s", out)
	}
	// Fenced like a drain: the body's newline cannot make a second line.
	if n := strings.Count(strings.TrimSpace(out), "\n"); n != 1 || strings.Contains(out, "\nFAKE") {
		t.Fatalf("one fenced message line and one state line, nothing forged:\n%s", out)
	}
	out, _, code = inbox("sess-b", "#"+q2)
	if code != 0 || !strings.Contains(out, "third: queued") || !strings.Contains(out, "queued, not yet delivered") {
		t.Fatalf("a queued message re-reads by #id, saying it is queued:\n%s", out)
	}
	// It marked nothing: the drain still delivers the queued one.
	if out, _, _ := inbox("sess-b"); !strings.Contains(out, "third: queued") {
		t.Fatalf("a re-read must not mark the message delivered:\n%s", out)
	}
	if out, _, code := inbox("sess-b", broadcast); code != 0 || !strings.Contains(out, "everyone: freeze main") {
		t.Fatalf("a broadcast that named this session re-reads:\n%s", out)
	}

	// Another session's message and an unknown id: one refusal, no body.
	for _, id := range []string{other, "99999"} {
		out, errw, code := inbox("sess-b", id)
		if code == 0 || strings.Contains(out+errw, "alpha's own business") || !strings.Contains(errw, "no message #") {
			t.Fatalf("#%s is not bravo's: refused, body withheld: %d %q %q", id, code, out, errw)
		}
	}
	// Positive control for that refusal: the addressee reads it.
	if out, _, code := inbox("sess-a", other); code != 0 || !strings.Contains(out, "alpha's own business") {
		t.Fatalf("the addressee re-reads its own message:\n%s", out)
	}
	for _, args := range [][]string{{"x7"}, {"0"}, {delivered, "--session", "sess-a"}} {
		if _, errw, code := inbox("sess-b", args...); code == 0 {
			t.Fatalf("inbox %v must be refused: %s", args, errw)
		}
	}
}
