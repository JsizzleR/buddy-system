package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// D-099: a Stop with mail queued hands it over instead of resting on it.

// stopPayload is a Stop payload; active is its stop_hook_active.
func stopPayload(session, cwd string, active bool, agentID string) string {
	in := map[string]any{"session_id": session, "cwd": cwd, "hook_event_name": "Stop", "stop_hook_active": active}
	if agentID != "" {
		in["agent_id"] = agentID
	}
	b, _ := json.Marshal(in)
	return string(b)
}

// stopBlock decodes a Stop hook's output: ok is a well-formed block.
func stopBlock(t *testing.T, out string) (reason string, ok bool) {
	t.Helper()
	if strings.TrimSpace(out) == "" {
		return "", false
	}
	var v struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("Stop output must be ONE JSON document: %v\n%q", err, out)
	}
	if v.Decision != "block" || v.Reason == "" {
		t.Fatalf("a Stop that prints anything prints a block with a reason: %q", out)
	}
	return v.Reason, true
}

func TestStopHandsQueuedMailOverInsteadOfResting(t *testing.T) {
	const body = "orchestrator ruling: take the router bundle next"
	cases := []struct {
		name    string
		mail    bool
		active  bool   // stop_hook_active
		agentID string // a subagent's
		block   bool
		idle    bool // the roster reports it idle afterwards
	}{
		// The fleet's case: the reply landed after the last tool call and
		// before the Stop. Held open, mail handed over, not idle.
		{"mail queued at the Stop", true, false, "", true, false},
		// The positive control for every refusal below: no mail, nothing
		// printed, idle recorded as before.
		{"nothing queued", false, false, "", false, true},
		// Never twice in a row: a turn a Stop hook already continued rests,
		// mail and all, and msg's wake covers it from here.
		{"already continued once", true, true, "", false, true},
		// A subagent's end is not the session's: no block, no idle mark.
		{"a subagent", true, false, "a0123456789abcdef", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			boundedParallel(t)
			f := newFixture(t)
			f.initAndHello(t)
			if tc.mail {
				if _, errw, code := f.run(t, f.repo, "", "msg", "bravo", body); code != 0 {
					t.Fatal(errw)
				}
			}
			out, errw, code := f.run(t, f.wtB, stopPayload("sess-b", f.wtB, tc.active, tc.agentID), "idle")
			if code != 0 {
				t.Fatalf("idle: exit %d: %s", code, errw)
			}
			reason, blocked := stopBlock(t, out)
			if blocked != tc.block {
				t.Fatalf("blocked=%v, want %v:\n%s", blocked, tc.block, out)
			}
			if blocked {
				for _, want := range []string{stopLead, "untrusted", body} {
					if !strings.Contains(reason, want) {
						t.Fatalf("the reason must carry %q, fenced like a drain:\n%s", want, reason)
					}
				}
			}
			row := sessionRow(t, f, "bravo")
			if got := strings.Contains(row, " idle "); got != tc.idle {
				t.Fatalf("idle on the roster=%v, want %v:\n  %s", got, tc.idle, row)
			}
			// Delivered exactly when handed over: the next beat carries it
			// only if the Stop did not.
			next, _, _ := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Bash", ""), "beat")
			if tc.mail && strings.Contains(next, body) == tc.block {
				t.Fatalf("handed over at the Stop=%v, so the next beat must %s carry it:\n%s",
					tc.block, map[bool]string{true: "NOT", false: "still"}[tc.block], next)
			}
		})
	}
}

// A Stop whose write fails marks nothing delivered and still records the
// idle mark: the session IS resting with its mail queued, and the next msg
// must name the wake rather than see no idle report at all.
func TestStopWithAFailedWriteRestsWithItsMailQueued(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "msg", "bravo", "check the nightly"); code != 0 {
		t.Fatal(errw)
	}
	var errw strings.Builder
	code := Run([]string{"idle"}, Env{
		Stdin:  strings.NewReader(stopPayload("sess-b", f.wtB, false, "")),
		Stdout: &failWriter{n: 0},
		Stderr: &errw,
		Cwd:    f.wtB,
		Now:    func() time.Time { return f.clock },
	})
	if code == 0 {
		t.Fatal("a Stop whose block could not be written must report it")
	}
	if row := sessionRow(t, f, "bravo"); !strings.Contains(row, " idle ") {
		t.Fatalf("an unwritten hold leaves the session resting, so it says idle:\n  %s", row)
	}
	if out, _, _ := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Bash", ""), "beat"); !strings.Contains(out, "check the nightly") {
		t.Fatalf("nothing was marked, so the next drain delivers it:\n%s", out)
	}
}

// Adapter mail is its adapter's to deliver (D-076): a legacy Stop never
// holds a turn open for it.
func TestStopNeverHoldsATurnForAdapterMail(t *testing.T) {
	boundedParallel(t)
	f, sender, recipient := adapterFixture(t)
	queueAdapter(t, f, sender, recipient, "adapter assignment")
	out, errw, code := f.run(t, f.repo, stopPayload(recipient.SessionID, f.repo, false, ""), "idle")
	if code != 0 {
		t.Fatalf("idle: %d %s", code, errw)
	}
	if _, blocked := stopBlock(t, out); blocked {
		t.Fatalf("adapter mail must not hold a legacy Stop:\n%s", out)
	}
	// Positive control: legacy mail to the same session does.
	if _, errw, code := f.run(t, f.repo, "", "msg", recipient.SessionID, "legacy note"); code != 0 {
		t.Fatal(errw)
	}
	out, _, _ = f.run(t, f.repo, stopPayload(recipient.SessionID, f.repo, false, ""), "idle")
	if reason, blocked := stopBlock(t, out); !blocked || !strings.Contains(reason, "legacy note") || strings.Contains(reason, "adapter assignment") {
		t.Fatalf("legacy mail holds the Stop, and only legacy mail rides it:\n%s", out)
	}
}
