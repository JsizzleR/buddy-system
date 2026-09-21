package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// Issue #24: `buddy msg` printed `queued for X — delivered after their next
// tool call` for every target, and that read as a send confirmation. It was a
// prediction, and whether it came true depended on something the sender could
// not see — whether the target ever ran another tool. Measured: two sends 8 s
// apart to two freshly-started idle sessions, one delivered in 28 s because
// the session happened to run a tool, the other in 159 s because a human was
// asked to type in its pane; and 25 undelivered messages across four sessions
// that had all ended, every one of which had reported `queued`. The result
// line now carries the target's OBSERVED state from the ledger — ended, its
// harness process gone, an outstanding idle report, quiet past the stale mark,
// registered and never seen since, or last seen N ago — plus how many earlier
// messages to it are still undelivered, which is the one fact that proves a
// channel is not draining.

// Each case sets up bravo one way and holds the send's result line to the
// phrase that state must produce AND to the absence of the old prediction.
func TestMsgReportsTheTargetsObservedState(t *testing.T) {
	boundedParallel(t)
	const oldPrediction = "delivered after their next tool call"
	cases := []struct {
		name  string
		setup func(t *testing.T, f *fixture)
		want  string
		not   []string
	}{
		{
			name: "registered and never seen since",
			setup: func(t *testing.T, f *fixture) {
				f.clock = f.clock.Add(30 * time.Second)
			},
			want: "queued for bravo — registered 30s ago and not seen since; delivery waits for its next tool call",
			// D-016: no idle row means UNKNOWN, and a fresh session must not
			// be described as either idle or busy.
			not: []string{"idle", "busy", "STALE", "GONE", "ENDED"},
		},
		{
			name: "seen recently",
			setup: func(t *testing.T, f *fixture) {
				// A second later than hello: the ledger keeps whole seconds, and a
				// beat in the registration second reads as none.
				f.clock = f.clock.Add(time.Second)
				if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Bash", ""), "beat"); code != 0 {
					t.Fatalf("beat: %s", errw)
				}
				f.clock = f.clock.Add(4 * time.Second)
			},
			want: "queued for bravo — last seen 4s ago; delivery waits for its next tool call",
			not:  []string{"idle", "busy", "STALE", "GONE", "ENDED", "registered"},
		},
		{
			name: "quiet past the stale mark",
			setup: func(t *testing.T, f *fixture) {
				if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Bash", ""), "beat"); code != 0 {
					t.Fatalf("beat: %s", errw)
				}
				f.clock = f.clock.Add(2 * time.Hour)
			},
			want: "queued for bravo — NOT SEEN FOR 2h, past the 30m stale mark; delivery waits for its next tool call and nothing in the ledger says one is coming",
			not:  []string{"idle", "busy", "GONE", "ENDED"},
		},
		{
			name: "outstanding idle report",
			setup: func(t *testing.T, f *fixture) {
				if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "", ""), "idle"); code != 0 {
					t.Fatalf("idle: %s", errw)
				}
				f.clock = f.clock.Add(2 * time.Hour)
			},
			// The D-027 wording, kept: it is the observation and not a prediction.
			want: "queued for bravo — bravo last reported idle 2h ago; a session waiting at its prompt runs no tool, so delivery waits for its next tool call",
			// Idle outranks stale: the idle row says WHY the session is quiet.
			not: []string{"STALE", "GONE", "ENDED"},
		},
		{
			name: "harness process gone",
			setup: func(t *testing.T, f *fixture) {
				f.asProcess(400, 7, func() {
					if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Bash", ""), "beat"); code != 0 {
						t.Fatalf("beat: %s", errw)
					}
				})
				f.kill(400)
				f.clock = f.clock.Add(5 * time.Minute)
			},
			want: "queued for bravo — its registered harness process (pid 400) is GONE and it was last seen 5m ago; no hook will come from a process that is not there",
			not:  []string{"idle", "busy", "STALE", "ENDED"},
		},
		{
			// GONE outranks an idle report: the report says why the session was
			// quiet, the dead process says it cannot answer.
			name: "gone and idle",
			setup: func(t *testing.T, f *fixture) {
				f.asProcess(400, 7, func() {
					// A beat registers the process (D-025); the idle hook does not.
					if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Bash", ""), "beat"); code != 0 {
						t.Fatalf("beat: %s", errw)
					}
					if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "", ""), "idle"); code != 0 {
						t.Fatalf("idle: %s", errw)
					}
				})
				f.kill(400)
				f.clock = f.clock.Add(5 * time.Minute)
			},
			want: "queued for bravo — its registered harness process (pid 400) is GONE",
			not:  []string{"idle", "busy", "STALE", "ENDED"},
		},
		{
			// The ledger keeps whole seconds: a beat inside the registration
			// second is indistinguishable from none, which is why the arm says
			// "not seen since" and not "no tool call yet".
			name: "beat in the registration second",
			setup: func(t *testing.T, f *fixture) {
				if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Bash", ""), "beat"); code != 0 {
					t.Fatalf("beat: %s", errw)
				}
				f.clock = f.clock.Add(30 * time.Second)
			},
			want: "queued for bravo — registered 30s ago and not seen since; delivery waits for its next tool call",
			not:  []string{"idle", "busy", "STALE", "GONE", "ENDED"},
		},
		{
			name: "seen exactly at the stale mark",
			setup: func(t *testing.T, f *fixture) {
				f.clock = f.clock.Add(time.Second)
				if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Bash", ""), "beat"); code != 0 {
					t.Fatalf("beat: %s", errw)
				}
				f.clock = f.clock.Add(30 * time.Minute)
			},
			want: "queued for bravo — last seen 30m ago; delivery waits for its next tool call",
			not:  []string{"idle", "busy", "STALE", "NOT SEEN", "GONE", "ENDED"},
		},
		{
			name: "one second past the stale mark",
			setup: func(t *testing.T, f *fixture) {
				f.clock = f.clock.Add(time.Second)
				if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Bash", ""), "beat"); code != 0 {
					t.Fatalf("beat: %s", errw)
				}
				f.clock = f.clock.Add(30*time.Minute + time.Second)
			},
			want: "queued for bravo — NOT SEEN FOR 30m, past the 30m stale mark",
			not:  []string{"idle", "busy", "GONE", "ENDED"},
		},
		{
			name: "ended",
			setup: func(t *testing.T, f *fixture) {
				if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "", ""), "bye"); code != 0 {
					t.Fatalf("bye: %s", errw)
				}
				f.clock = f.clock.Add(2 * time.Hour)
			},
			want: "queued for bravo — it ENDED 2h ago; nothing reads this unless that session id helloes again",
			not:  []string{"idle", "busy", "STALE", "GONE"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.initAndHello(t)
			tc.setup(t, f)
			out, errw, code := f.run(t, f.repo, "", "msg", "bravo", "hello there")
			if code != 0 {
				t.Fatalf("send failed: %s", errw)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("want %q in:\n%s", tc.want, out)
			}
			if strings.Contains(out+errw, oldPrediction) {
				t.Fatalf("the old prediction is still printed:\n%s%s", out, errw)
			}
			// And not respelled: a claim of delivery is the one thing this
			// command can never make, so the WORD is refused, not one phrasing
			// of it (Codex: "delivered on its next tool call" passed the check
			// above and was the same prediction).
			if strings.Contains(out, "delivered") {
				t.Fatalf("the line claims delivery:\n%s", out)
			}
			for _, bad := range tc.not {
				if strings.Contains(out, bad) {
					t.Fatalf("result must not say %q for this state:\n%s", bad, out)
				}
			}
			// POSITIVE CONTROL for the whole table: the message was actually
			// queued, so the line describes a send and not a refusal. Read via
			// `who`, which reports an ended session; `inbox` drains as the
			// caller and refuses to be one that has ended.
			if who, _, _ := f.run(t, f.repo, "", "who", "bravo"); !strings.Contains(who, "INBOX        1 undelivered") {
				t.Fatalf("the send was reported but nothing was queued:\n%s", who)
			}
		})
	}
}

// The ended case that produced the 25 undelivered messages: several were
// asks to release claims that were blocking a queue. Since D-026 an ended
// holder's claims are displaced by a plain `buddy claim`, so the send names
// the claims it still holds and says so — the message was the wrong verb.
func TestMsgToAnEndedHolderNamesTheClaimsAPlainClaimDisplaces(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.wtB, "", "claim", "api-work", "--session", "sess-b", "--desc", "x", "--scope", "internal/api"); code != 0 {
		t.Fatalf("claim: %s", errw)
	}
	if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "", ""), "bye"); code != 0 {
		t.Fatalf("bye: %s", errw)
	}
	out, errw, code := f.run(t, f.repo, "", "msg", "bravo", "please release api-work")
	if code != 0 {
		t.Fatal(errw)
	}
	if !strings.Contains(out, "it ENDED 0s ago") || !strings.Contains(out, "; it still holds 1 open claim(s) (api-work) that a plain `buddy claim` displaces (D-026)") {
		t.Fatalf("want the held-claims pointer on a send to an ended holder:\n%s", out)
	}
	// A live holder draws no such pointer: a message IS how you ask a live
	// session for its claim.
	if _, errw, code := f.run(t, f.repo, "", "claim", "docs-pass", "--session", "sess-a", "--desc", "x", "--scope", "docs"); code != 0 {
		t.Fatalf("claim: %s", errw)
	}
	out, errw, code = f.run(t, f.wtB, "", "msg", "alpha", "please release docs-pass")
	if code != 0 || !strings.Contains(out, "queued for alpha") {
		t.Fatalf("control send: %d %s %s", code, out, errw)
	}
	if strings.Contains(out, "displaces") {
		t.Fatalf("a live holder must draw no displacement pointer:\n%s", out)
	}
	// ENDED and the backlog print together: the arm never suppresses the
	// fact that proves the channel is dead.
	f.clock = f.clock.Add(time.Hour)
	out, _, code = f.run(t, f.repo, "", "msg", "bravo", "please, again")
	if code != 0 || !strings.Contains(out, "it ENDED 1h ago") || !strings.Contains(out, "; 1 earlier message(s) to it still undelivered, the oldest 1h old") {
		t.Fatalf("want ENDED and the backlog on one line:\n%s", out)
	}
}

// The backlog is the fact that proves a channel is dead: 25 messages to four
// sessions all reported `queued`, and the sender concluded they had been read
// and ignored. A second send to a target that has not drained the first says
// so, with the age of the oldest; a drained inbox says nothing.
func TestMsgReportsEarlierUndeliveredMessagesToTheTarget(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	out, errw, code := f.run(t, f.repo, "", "msg", "bravo", "first")
	if code != 0 {
		t.Fatal(errw)
	}
	if strings.Contains(out, "undelivered") {
		t.Fatalf("a first send has no backlog to report:\n%s", out)
	}
	f.clock = f.clock.Add(2 * time.Hour)
	out, _, code = f.run(t, f.repo, "", "msg", "bravo", "second")
	if code != 0 || !strings.Contains(out, "; 1 earlier message(s) to it still undelivered, the oldest 2h old") {
		t.Fatalf("want the backlog on the second send:\n%s", out)
	}
	f.clock = f.clock.Add(time.Hour)
	out, _, _ = f.run(t, f.repo, "", "msg", "bravo", "third")
	if !strings.Contains(out, "; 2 earlier message(s) to it still undelivered, the oldest 3h old") {
		t.Fatalf("the backlog must count every earlier message and date the oldest:\n%s", out)
	}
	// A broadcast the target has not drained is part of its backlog too.
	if _, errw, code := f.run(t, f.repo, "", "msg", "all", "fleet note"); code != 0 {
		t.Fatal(errw)
	}
	out, _, _ = f.run(t, f.repo, "", "msg", "bravo", "fourth")
	if !strings.Contains(out, "; 4 earlier message(s) to it still undelivered") {
		t.Fatalf("an undelivered broadcast counts:\n%s", out)
	}
	// POSITIVE CONTROL: draining the inbox clears the note, so the count above
	// was the backlog and not a constant.
	if _, errw, code := f.run(t, f.wtB, "", "inbox", "--session", "sess-b"); code != 0 {
		t.Fatal(errw)
	}
	out, errw, code = f.run(t, f.repo, "", "msg", "bravo", "fifth")
	if code != 0 || !strings.Contains(out, "queued for bravo") {
		t.Fatalf("control send: %d %s %s", code, out, errw)
	}
	if strings.Contains(out, "undelivered") {
		t.Fatalf("a drained inbox must print no backlog:\n%s", out)
	}
}

// A broadcast counts, among its live recipients, the ones the ledger has not
// seen past the stale mark — alongside the D-027 idle count, since the two
// are different observations (an idle row says why; a stale row says only
// that nothing has been heard).
func TestBroadcastCountsRecipientsNotSeenPastTheStaleMark(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.clock = f.clock.Add(time.Hour)
	// alpha beats now; bravo has been silent an hour.
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "Bash", ""), "beat"); code != 0 {
		t.Fatal(errw)
	}
	out, errw, code := f.run(t, f.repo, "", "msg", "all", "fleet note")
	if code != 0 {
		t.Fatal(errw)
	}
	if !strings.Contains(out, "1 of 2 live recipients not seen for over 30m") {
		t.Fatalf("want the stale count on a broadcast:\n%s", out)
	}
	if strings.Contains(out, "idle") {
		t.Fatalf("no idle row must print no idle count:\n%s", out)
	}
	// POSITIVE CONTROL: once bravo is heard from, the count is gone.
	if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Bash", ""), "beat"); code != 0 {
		t.Fatal(errw)
	}
	out, errw, code = f.run(t, f.repo, "", "msg", "all", "again")
	if code != 0 || !strings.Contains(out, "queued for all") {
		t.Fatalf("control send: %d %s %s", code, out, errw)
	}
	if strings.Contains(out, "not seen") {
		t.Fatalf("a fresh beat must clear the stale count:\n%s", out)
	}
}

// `who`'s INBOX line dates the oldest undelivered message, because "3
// undelivered" for a session seen 2 s ago and "3 undelivered, the oldest 2h
// old" for one seen 2 h ago are the difference between a queue draining and a
// channel that is dead.
func TestWhoDatesTheOldestUndeliveredMessage(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "msg", "bravo", "first"); code != 0 {
		t.Fatal(errw)
	}
	f.clock = f.clock.Add(2 * time.Hour)
	if _, errw, code := f.run(t, f.repo, "", "msg", "bravo", "second"); code != 0 {
		t.Fatal(errw)
	}
	out, _, code := f.run(t, f.repo, "", "who", "bravo")
	if code != 0 || !strings.Contains(out, "INBOX        2 undelivered, the oldest 2h old") {
		t.Fatalf("want the dated backlog:\n%s", out)
	}
	if _, errw, code := f.run(t, f.wtB, "", "inbox", "--session", "sess-b"); code != 0 {
		t.Fatal(errw)
	}
	if out, _, _ = f.run(t, f.repo, "", "who", "bravo"); !strings.Contains(out, "INBOX        0 undelivered\n") {
		t.Fatalf("a drained inbox carries no age:\n%s", out)
	}
}

// Codex code pass (P1): the idle row and the process list were each read
// TWICE inside one send — once in the case guard, once in the format — so a
// register that changed between the reads was reported from two states at
// once. An idle row the recipient cleared with a beat in between dereferenced
// nil BEFORE the write, and one concurrent beat aborted a message without
// queueing it; a process whose liveness flipped printed `(pid )`. The idle
// race has no seam to inject at from here; the liveness probe does, and the
// single read that fixes one fixes both. The probe answers "dead" once and
// "alive" ever after: two reads disagree, one read cannot.
func TestMsgProbesTheProcessRegisterOncePerSend(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.asProcess(400, 7, func() {
		if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Bash", ""), "beat"); code != 0 {
			t.Fatalf("beat: %s", errw)
		}
	})
	probes := 0
	var out, errw bytes.Buffer
	code := Run([]string{"msg", "bravo", "hi"}, Env{
		Stdin:  strings.NewReader(""),
		Stdout: &out,
		Stderr: &errw,
		Cwd:    f.repo,
		Now:    func() time.Time { return f.clock },
		Getenv: func(k string) string { return f.env[k] },
		Anchor: func() (store.ProcRef, bool) { return f.proc, f.proc.PID != 0 },
		ProcAlive: func(store.ProcRef) bool {
			probes++
			return probes > 1
		},
	})
	if code != 0 {
		t.Fatalf("send failed: %s", errw.String())
	}
	if !strings.Contains(out.String(), "(pid 400) is GONE") {
		t.Fatalf("a second probe changed the answer mid-line:\n%s", out.String())
	}
	if probes != 1 {
		t.Fatalf("the register was probed %d times in one send; once is the contract", probes)
	}
}
