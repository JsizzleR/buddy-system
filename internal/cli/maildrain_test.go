package cli

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// Who drains a session's mail, and how many times (drain.go, D-099).

// subagentHook is hookJSON for a tool call made inside a subagent: the
// PARENT's session id, plus the agent_id Claude Code adds for such a call
// ("Present only when the hook fires from within a subagent", the 2.1.304
// hook-input schema and the hooks documentation; not measured on a live
// payload).
func subagentHook(session, cwd, tool, filePath string) string {
	var in map[string]any
	_ = json.Unmarshal([]byte(hookJSON(session, cwd, tool, filePath)), &in)
	in["agent_id"] = "a40917c065881f990"
	in["agent_type"] = "general-purpose"
	b, _ := json.Marshal(in)
	return string(b)
}

// One fleet: three parallel Agent calls fired three PostToolUse hooks in one
// session, and each beat read, wrote and marked the same message — injected
// three times in one millisecond. The drain now holds the ledger's write
// lock from its read to its mark, so exactly one of N concurrent beats
// carries it.
func TestConcurrentBeatsDeliverAMessageOnce(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	const rounds, beats = 5, 8
	for r := 0; r < rounds; r++ {
		body := "round " + string(rune('A'+r)) + " ruling"
		if _, errw, code := f.run(t, f.repo, "", "msg", "bravo", body); code != 0 {
			t.Fatal(errw)
		}
		var wg sync.WaitGroup
		outs := make([]string, beats)
		codes := make([]int, beats)
		start := make(chan struct{})
		for i := 0; i < beats; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				outs[i], _, codes[i] = f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Bash", ""), "beat")
			}(i)
		}
		close(start)
		wg.Wait()
		got := 0
		for i, out := range outs {
			if codes[i] != 0 {
				t.Fatalf("round %d beat %d: exit %d", r, i, codes[i])
			}
			got += strings.Count(out, body)
		}
		if got != 1 {
			t.Fatalf("round %d: %d concurrent beats delivered one message %d times, want exactly once:\n%s",
				r, beats, got, strings.Join(outs, "\n---\n"))
		}
	}
}

// A subagent's tool call fires PostToolUse with the PARENT's session id. The
// beat it runs drained the parent's inbox into the subagent's context and
// marked it delivered: 181 distinct messages reached 186 subagent
// transcripts on one fleet, at least 30 of them never in the main thread.
// It must still be the session's heartbeat (a subagent's work is the
// session's), and deliver nothing: the main thread's next tool call does.
func TestSubagentBeatLeavesTheParentsMailQueued(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "msg", "bravo", "your successor is charlie"); code != 0 {
		t.Fatal(errw)
	}
	seenBefore := sessionRow(t, f, "bravo")
	f.clock = f.clock.Add(10 * 60e9)
	out, errw, code := f.run(t, f.wtB, subagentHook("sess-b", f.wtB, "Bash", ""), "beat")
	if code != 0 {
		t.Fatalf("subagent beat: %s", errw)
	}
	if strings.Contains(out, "successor") {
		t.Fatalf("a subagent's beat must not carry the parent's mail:\n%s", out)
	}
	if row := sessionRow(t, f, "bravo"); row == seenBefore || !strings.Contains(row, "seen 0s") {
		t.Fatalf("a subagent's tool call is still the session's heartbeat:\nbefore %s\nafter  %s", seenBefore, row)
	}
	// busy has no subagent event today; the same guard holds for it anyway.
	if out, _, _ := f.run(t, f.wtB, subagentHook("sess-b", f.wtB, "", ""), "busy"); strings.Contains(out, "successor") {
		t.Fatalf("busy under a subagent id must not drain:\n%s", out)
	}
	// Positive control: the same beat from the main thread delivers it, so
	// the refusal above was the guard and not an empty inbox.
	out, _, _ = f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Bash", ""), "beat")
	if !strings.Contains(out, "your successor is charlie") {
		t.Fatalf("the main thread's beat must deliver what the subagent left queued:\n%s", out)
	}
}

// The one-shot notices ride the same drain, and a subagent ate those too:
// a LANDED wait seen by a subagent is a LANDED the main thread never sees.
func TestSubagentBeatLeavesTheOneShotNoticesForTheMainThread(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.asSession("sess-a", func() {
		if _, errw, code := f.run(t, f.repo, "", "claim", "router", "--desc", "x", "--scope", "src/router"); code != 0 {
			t.Fatal(errw)
		}
	})
	f.asSession("sess-b", func() {
		if _, errw, code := f.run(t, f.wtB, "", "wait", "--on", "router"); code != 0 {
			t.Fatal(errw)
		}
	})
	f.asSession("sess-a", func() {
		if _, errw, code := f.run(t, f.repo, "", "release", "router"); code != 0 {
			t.Fatal(errw)
		}
	})
	out, errw, code := f.run(t, f.wtB, subagentHook("sess-b", f.wtB, "Bash", ""), "beat")
	if code != 0 {
		t.Fatalf("subagent beat: %s", errw)
	}
	if strings.Contains(out, "LANDED") {
		t.Fatalf("a subagent must not consume the LANDED notice:\n%s", out)
	}
	out, _, _ = f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Bash", ""), "beat")
	if !strings.Contains(out, "LANDED") {
		t.Fatalf("positive control: the main thread's beat carries the LANDED notice:\n%s", out)
	}
}

// sessionRow is the roster row whose label column is label.
func sessionRow(t *testing.T, f *fixture, label string) string {
	t.Helper()
	out, errw, code := f.run(t, f.repo, "", "sessions")
	if code != 0 {
		t.Fatalf("sessions: %s", errw)
	}
	for _, ln := range strings.Split(out, "\n") {
		if fs := strings.Fields(ln); len(fs) > 1 && fs[1] == label {
			return ln
		}
	}
	t.Fatalf("no row for %q in:\n%s", label, out)
	return ""
}
