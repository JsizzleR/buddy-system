package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// runBlocking runs one command as sid with a Sleep seam: each pause advances
// the fixture clock by step and then calls between(n), n counting pauses from
// 1, which plays the world moving on while the block sleeps. A block that
// sleeps past maxPauses fails the test rather than hanging it.
func (b *batchFx) runBlocking(t *testing.T, sid string, step time.Duration, between func(n int), args ...string) (out string, code int) {
	t.Helper()
	const maxPauses = 50
	var stdout, stderr bytes.Buffer
	pauses := 0
	b.asSession(sid, func() {
		code = Run(args, Env{
			Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Cwd: b.cwdOf(sid),
			Now:    func() time.Time { return b.clock },
			Getenv: func(k string) string { return b.env[k] },
			Sleep: func(d time.Duration) {
				if d != waitBlockPoll {
					t.Errorf("slept %s, want the poll %s", d, waitBlockPoll)
				}
				pauses++
				if pauses > maxPauses {
					t.Fatalf("still blocking after %d pauses:\n%s", maxPauses, stdout.String())
				}
				b.clock = b.clock.Add(step)
				prev := b.env[EnvClaudeSession]
				between(pauses)
				b.env[EnvClaudeSession] = prev
			},
		})
	})
	return stdout.String() + stderr.String(), code
}

// D-095: `wait --on <slug> --block` declares, re-reads, and exits with the
// verdict, printing the line `wait check` prints.
func TestWaitBlockExitsWithTheVerdict(t *testing.T) {
	boundedParallel(t)
	for _, tc := range []struct {
		name  string
		args  []string
		act   func(t *testing.T, b *batchFx, n int)
		code  int
		first string // the verdict line begins so
	}{
		{name: "released with an outcome: LANDED, exit 0", args: []string{"--until", "1h"},
			act: func(t *testing.T, b *batchFx, n int) {
				if n == 3 {
					b.ok(t, "sess-a", "release", "herm", "--outcome", "pass")
				}
			}, code: blockLanded, first: `LANDED: claim "herm" released`},
		{name: "handed to the waiter: LANDED, exit 0", args: []string{"--until", "1h"},
			act: func(t *testing.T, b *batchFx, n int) {
				if n == 2 {
					b.ok(t, "sess-a", "release", "herm", "--to", "charlie")
				}
			}, code: blockLanded, first: `LANDED: claim "herm" (handed to charlie`},
		{name: "the holder said bye: settled like a check, LANDED", args: []string{"--until", "1h"},
			act: func(t *testing.T, b *batchFx, n int) {
				if n == 2 {
					if _, errw, code := b.run(t, b.repo, hookJSON("sess-a", b.repo, "", ""), "bye"); code != 0 {
						t.Fatalf("bye: %s", errw)
					}
				}
			}, code: blockLanded, first: `LANDED: claim "herm" orphaned`},
		{name: "nothing happens: EXPIRED, exit 3", args: []string{"--until", "2m"},
			act: func(*testing.T, *batchFx, int) {}, code: blockExpired, first: "EXPIRED: the deadline passed"},
		{name: "cleared: exit 4", args: []string{"--until", "1h"},
			act: func(t *testing.T, b *batchFx, n int) {
				if n == 2 {
					b.ok(t, "sess-c", "wait", "clear")
				}
			}, code: blockCleared, first: "CLEARED: the wait was withdrawn"},
		{name: "replaced by a newer declaration: exit 4", args: []string{"--until", "1h"},
			act: func(t *testing.T, b *batchFx, n int) {
				if n == 2 {
					b.ok(t, "sess-c", "wait", "--on", "herm", "--until", "2h")
				}
			}, code: blockCleared, first: "REPLACED: a newer declaration"},
		{name: "the session ended: exit 5", args: []string{"--until", "1h"},
			act: func(t *testing.T, b *batchFx, n int) {
				if n == 2 {
					if _, errw, code := b.run(t, b.wtC, hookJSON("sess-c", b.wtC, "", ""), "bye"); code != 0 {
						t.Fatalf("bye: %s", errw)
					}
				}
			}, code: blockEnded, first: "ENDED: session charlie ended"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newBatchFx(t)
			b.form(t)
			args := append([]string{"wait", "--on", "herm", "--block"}, tc.args...)
			out, code := b.runBlocking(t, "sess-c", time.Minute, func(n int) { tc.act(t, b, n) }, args...)
			if code != tc.code {
				t.Fatalf("exit %d, want %d:\n%s", code, tc.code, out)
			}
			if !strings.HasPrefix(out, `WAITING on claim "herm"`) || !strings.Contains(out, "\nBLOCKING until it lands") {
				t.Fatalf("the declaration prints as today, then says it blocks:\n%s", out)
			}
			if !strings.Contains(out, "\n"+tc.first) {
				t.Fatalf("want a verdict line beginning %q:\n%s", tc.first, out)
			}
			// Not a keep-alive check: the block never bumped the counters.
			st, _, err := openRepo(b.repo, Env{Getenv: func(string) string { return "" }})
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if w, ok, _ := st.WaitOf("sess-c"); ok && w.Checks != 0 {
				t.Fatalf("a block's reads must not count as checks: %d", w.Checks)
			}
			// A verdict it printed is closed, as a check closes it.
			if tc.code == blockLanded || tc.code == blockExpired {
				if w, _, _ := st.WaitOf("sess-c"); w.IsOpen() {
					t.Fatalf("the verdict was written, so the row closes: %+v", w)
				}
			}
		})
	}
}

// A timer never lands, so --block on one is refused before anything is
// written; the control is the same timer declared without --block.
func TestWaitBlockRefusesATimer(t *testing.T) {
	boundedParallel(t)
	b := newBatchFx(t)
	if errw := b.refused(t, "sess-c", "a blocked timer is a sleep", "wait", "--until", "1h", "--block"); !strings.Contains(errw, "nothing was recorded") {
		t.Fatalf("the refusal says nothing was recorded:\n%s", errw)
	}
	st, _, err := openRepo(b.repo, Env{Getenv: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, ok, _ := st.WaitOf("sess-c"); ok {
		t.Fatal("a refused --block wrote a wait")
	}
	b.ok(t, "sess-c", "wait", "--until", "1h")
	if w, ok, _ := st.WaitOf("sess-c"); !ok || len(w.Targets) != 0 {
		t.Fatalf("control: the timer declares without --block: %+v %v", w, ok)
	}
}
