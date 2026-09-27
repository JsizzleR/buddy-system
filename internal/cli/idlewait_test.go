package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// userRecord is a prompt or tool result the way the harness writes one: a
// top-level "type":"user" record with its own timestamp (D-060).
func userRecord(at time.Time, sidechain bool) string {
	return promptRecord(at, sidechain, "x")
}

func promptRecord(at time.Time, sidechain bool, promptID string) string {
	return fmt.Sprintf(`{"parentUuid":"p","isSidechain":%t,"promptId":%q,"type":"user","message":{"role":"user","content":"go"},"uuid":"u","timestamp":%q}`,
		sidechain, promptID, stamp(at))
}

// reply is an assistant record carrying usage, prompt = 2 + read + write.
func reply(at time.Time, read int64) string {
	return turnLineTTL(stamp(at), 2, read, 1_000, 0, 1_000)
}

func stamp(at time.Time) string { return at.UTC().Format("2006-01-02T15:04:05.000Z") }

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Error(err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		t.Error(err)
	}
}

// The detector reads "the reply has not landed" from the file: a prompt or
// tool result newer than the newest usage record. Every case that must NOT
// read as pending is a guard on a way the check could be fooled; the plain
// answered turn is the positive control that pending is not simply always on.
func TestReadTurnSeesARecordThatIsStillWaitingForItsReply(t *testing.T) {
	boundedParallel(t)
	t0 := time.Date(2026, 9, 27, 20, 0, 0, 0, time.UTC)
	// A record written after the reply that is not a user record but carries
	// one nested inside it: the prefilter matches, the top-level type must not.
	nested := `{"type":"system","subtype":"x","timestamp":"` + stamp(t0.Add(20*time.Second)) +
		`","content":[{"type":"user","timestamp":"` + stamp(t0.Add(time.Hour)) + `"}]}`
	cases := []struct {
		name    string
		lines   []string
		ok      bool
		read    int64     // the usage record's cache read, when ok
		pending time.Time // zero: nothing waiting
	}{
		{"answered: the reply follows its prompt", []string{userRecord(t0, false), reply(t0.Add(3*time.Second), 88_000)},
			true, 88_000, time.Time{}},
		{"the Stop's case: a new prompt after the previous reply", []string{reply(t0, 77_000), userRecord(t0.Add(time.Minute), false)},
			true, 77_000, t0.Add(time.Minute)},
		{"a tool result waiting for the turn's last reply", []string{userRecord(t0, false), reply(t0.Add(time.Second), 77_000),
			strings.Replace(userRecord(t0.Add(2*time.Second), false), `"content":"go"`, `"content":"`+strings.Repeat("r", 40_000)+`"`, 1)},
			true, 77_000, t0.Add(2 * time.Second)},
		{"a first turn: a prompt and no reply at all", []string{userRecord(t0, false)},
			false, 0, t0},
		{"a subagent's prompt is not the session's", []string{reply(t0, 77_000), userRecord(t0.Add(time.Minute), true)},
			true, 77_000, time.Time{}},
		{"a prompt cut off mid-write is not read", []string{reply(t0, 77_000), userRecord(t0.Add(time.Minute), false)[:60]},
			true, 77_000, time.Time{}},
		{"a nested \"type\":\"user\" in a later record is not a user record", []string{userRecord(t0, false), reply(t0.Add(10*time.Second), 88_000), nested},
			true, 88_000, time.Time{}},
		{"a tie is pending: the prompt in the reply's millisecond", []string{reply(t0, 77_000), userRecord(t0, false)},
			true, 77_000, t0},
		{"no user record at all", []string{reply(t0, 77_000)},
			true, 77_000, time.Time{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "t.jsonl")
			writeLines(t, path, tc.lines...)
			r := readTurn(path, "")
			if r.ok != tc.ok || (tc.ok && r.u.CacheRead != tc.read) {
				t.Fatalf("usage: ok=%v read=%d, want ok=%v read=%d", r.ok, r.u.CacheRead, tc.ok, tc.read)
			}
			if !r.pendingAt.Equal(tc.pending) {
				t.Fatalf("pendingAt = %v, want %v", r.pendingAt, tc.pending)
			}
			if u, ok := lastUsage(path); ok != r.ok || u != r.u {
				t.Fatalf("lastUsage must be readTurn's usage half: %v %v", u, ok)
			}
		})
	}
}

func stopJSON(f *fixture, session, tr string) string {
	b, _ := json.Marshal(map[string]any{"session_id": session, "cwd": f.repo, "hook_event_name": "Stop", "transcript_path": tr})
	return string(b)
}

func idleRow(t *testing.T, f *fixture, label string) string {
	t.Helper()
	out, errw, code := f.run(t, f.repo, "", "sessions")
	if code != 0 {
		t.Fatal(errw)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, label) {
			return l
		}
	}
	t.Fatalf("no row for %s:\n%s", label, out)
	return ""
}

// With the reply not yet written — every measured Stop — the hook records NO
// footprint (the reply it can see is the previous request's) and dates the
// mark by the write, not by the previous reply, which made `idle N` count
// from the turn before. The same transcript with the reply on disk is the
// positive control: the fixture can record a footprint, so its absence is the
// pending path's doing.
func TestIdleBeforeTheReplyRecordsNoFootprintAndDatesTheMarkNow(t *testing.T) {
	boundedParallel(t)
	for _, landed := range []bool{false, true} {
		t.Run(fmt.Sprintf("landed=%v", landed), func(t *testing.T) {
			f := newFixture(t)
			f.initAndHello(t)
			tr := filepath.Join(t.TempDir(), "a.jsonl")
			prev, prompt := f.clock.Add(time.Second), f.clock.Add(2*time.Minute)
			lines := []string{reply(prev, 60_000), userRecord(prompt, false)}
			if landed {
				lines = append(lines, reply(prompt.Add(20*time.Second), 120_000))
			}
			writeLines(t, tr, lines...)
			f.clock = prompt.Add(30 * time.Second)
			if _, errw, code := f.run(t, f.repo, stopJSON(f, "sess-a", tr), "idle"); code != 0 {
				t.Fatal(errw)
			}
			row := idleRow(t, f, "(sess-a)")
			if landed {
				if !strings.Contains(row, "prompt 121k") || !strings.Contains(row, "idle 10s") {
					t.Fatalf("control: the landed reply is the footprint and the mark's date:\n%s", row)
				}
				return
			}
			if strings.Contains(row, "prompt") {
				t.Fatalf("the previous request's footprint must not be recorded as this turn's:\n%s", row)
			}
			if !strings.Contains(row, "idle 0s") {
				t.Fatalf("the mark is dated by the write, not by the previous reply (which reads idle 2m29s):\n%s", row)
			}
			if !strings.Contains(row, "base ") {
				t.Fatalf("the base is this turn's HEAD whatever the reply says, and is recorded:\n%s", row)
			}
		})
	}
}

// A revived session's first Stop, before its reply lands, sees only its
// PREDECESSOR's last reply, and the fence refused the mark on it: the revived
// session reported no idle mark at all after its first turn. Fenced by its
// own pending prompt it is marked. A Stop whose evidence predates the revival
// — the delayed Stop of issue #11, pending or answered — must still be
// refused: those are the fence's positive controls, one per path, and the
// revived session's own answered turn is the found path's.
func TestIdleFencesByThePendingPromptSoARevivedSessionIsMarked(t *testing.T) {
	boundedParallel(t)
	for _, tc := range []struct {
		name   string
		lines  func(predecessor, revived time.Time) []string
		marked bool
	}{
		{"pending: the revived incarnation's own prompt", func(p, r time.Time) []string {
			return []string{reply(p, 60_000), userRecord(r.Add(5*time.Second), false)}
		}, true},
		{"pending: a prompt from before the revival (a delayed Stop)", func(p, r time.Time) []string {
			return []string{reply(p, 60_000), userRecord(r.Add(-5*time.Second), false)}
		}, false},
		{"found: the revived incarnation's own reply", func(p, r time.Time) []string {
			return []string{reply(p, 60_000), userRecord(r.Add(5*time.Second), false), reply(r.Add(8*time.Second), 70_000)}
		}, true},
		{"found: a reply from before the revival (a delayed Stop)", func(p, r time.Time) []string {
			return []string{userRecord(p.Add(-time.Second), false), reply(p, 60_000)}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.initAndHello(t)
			tr := filepath.Join(t.TempDir(), "a.jsonl")
			predecessor := f.clock.Add(5 * time.Second)
			if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "bye"); code != 0 {
				t.Fatal(errw)
			}
			f.clock = f.clock.Add(time.Minute)
			revived := f.clock
			if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello"); code != 0 {
				t.Fatal(errw)
			}
			writeLines(t, tr, tc.lines(predecessor, revived)...)
			f.clock = revived.Add(10 * time.Second)
			if _, errw, code := f.run(t, f.repo, stopJSON(f, "sess-a", tr), "idle"); code != 0 {
				t.Fatal(errw)
			}
			row := idleRow(t, f, "(sess-a)")
			if got := strings.Contains(row, "idle "); got != tc.marked {
				t.Fatalf("marked=%v, want %v:\n%s", got, tc.marked, row)
			}
			if strings.Contains(row, "prompt 61k") {
				t.Fatalf("the predecessor's footprint must never be the revived session's:\n%s", row)
			}
		})
	}
}

// busy re-reads the transcript before it answers the handoff question
// (D-055), so a prose-only session is told on the prompt after the turn that
// crossed the size — not a turn later, and on its first turn too. The same
// hook with the size under the declaration is the control that the note is
// the size's doing, not the re-read's.
func TestBusySeesTheTurnThatJustEndedBeforeItAnswersTheHandoff(t *testing.T) {
	boundedParallel(t)
	for _, tc := range []struct {
		at   string
		note bool
	}{{"100k", true}, {"200k", false}} {
		t.Run(tc.at, func(t *testing.T) {
			f := newFixture(t)
			f.initAndHello(t)
			f.env = map[string]string{EnvHandoffAt: tc.at}
			tr := filepath.Join(t.TempDir(), "a.jsonl")
			// The first turn's reply has landed; no beat or Stop ever read it.
			writeLines(t, tr, userRecord(f.clock.Add(time.Second), false), reply(f.clock.Add(5*time.Second), 120_000),
				userRecord(f.clock.Add(time.Minute), false))
			f.clock = f.clock.Add(time.Minute)
			in, _ := json.Marshal(map[string]any{"session_id": "sess-a", "cwd": f.repo, "hook_event_name": "UserPromptSubmit",
				"transcript_path": tr, "prompt": "next"})
			out, errw, code := f.run(t, f.repo, string(in), "busy")
			if code != 0 {
				t.Fatal(errw)
			}
			if got := strings.Contains(out, "your prompt was"); got != tc.note {
				t.Fatalf("handoff note=%v, want %v:\n%s", got, tc.note, out)
			}
			if tc.note && !strings.Contains(out, "your prompt was 121k") {
				t.Fatalf("the note must carry the turn that just ended:\n%s", out)
			}
			if row := idleRow(t, f, "(sess-a)"); !strings.Contains(row, "prompt 121k") {
				t.Fatalf("the re-read is recorded for the roster too:\n%s", row)
			}
		})
	}
}

// A revived session's first prompt finds its PREDECESSOR's last reply as the
// newest on disk, and D-055 decided a revived session is not told its
// predecessor's size: busy's re-read is fenced by the incarnation's start,
// as idle's is. Its own reply after the revival is the control.
func TestBusyDoesNotTellARevivedSessionItsPredecessorsSize(t *testing.T) {
	boundedParallel(t)
	for _, own := range []bool{false, true} {
		t.Run(fmt.Sprintf("own=%v", own), func(t *testing.T) {
			f := newFixture(t)
			f.initAndHello(t)
			f.env = map[string]string{EnvHandoffAt: "100k"}
			tr := filepath.Join(t.TempDir(), "a.jsonl")
			predecessor := f.clock.Add(5 * time.Second)
			if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "bye"); code != 0 {
				t.Fatal(errw)
			}
			f.clock = f.clock.Add(time.Minute)
			if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello"); code != 0 {
				t.Fatal(errw)
			}
			lines := []string{userRecord(predecessor.Add(-time.Second), false), reply(predecessor, 120_000)}
			if own {
				lines = append(lines, userRecord(f.clock.Add(2*time.Second), false), reply(f.clock.Add(5*time.Second), 130_000))
			}
			writeLines(t, tr, lines...)
			f.clock = f.clock.Add(10 * time.Second)
			in, _ := json.Marshal(map[string]any{"session_id": "sess-a", "cwd": f.repo, "hook_event_name": "UserPromptSubmit",
				"transcript_path": tr, "prompt": "next"})
			out, errw, code := f.run(t, f.repo, string(in), "busy")
			if code != 0 {
				t.Fatal(errw)
			}
			if got := strings.Contains(out, "your prompt was"); got != own {
				t.Fatalf("note=%v, want %v (own reply only):\n%s", got, own, out)
			}
			if own && !strings.Contains(out, "your prompt was 131k") {
				t.Fatalf("the note must carry the revived session's own reply:\n%s", out)
			}
			if strings.Contains(out, "121k") {
				t.Fatalf("the predecessor's size must never be the revived session's:\n%s", out)
			}
		})
	}
}

// Between turns a prose session's roster footprint is the observation busy
// made at the start of the turn that just ended, with THAT request's age —
// never a "now" the pending Stop could have fabricated. The cache clock errs
// the safe way: the last request is newer than the one observed, so the
// roster may call a warm cache colder, never a cold one warm. The control is
// the same turn once its reply is on disk: then the newer request is shown.
func TestARestingProseSessionShowsTheLastObservationsAgeNotNow(t *testing.T) {
	boundedParallel(t)
	for _, landed := range []bool{false, true} {
		t.Run(fmt.Sprintf("landed=%v", landed), func(t *testing.T) {
			f := newFixture(t)
			f.initAndHello(t)
			tr := filepath.Join(t.TempDir(), "a.jsonl")
			t0 := f.clock
			// Turn 1 answered; turn 2's prompt opens with busy, which records turn 1.
			writeLines(t, tr, userRecord(t0.Add(time.Second), false), reply(t0.Add(5*time.Second), 60_000),
				userRecord(t0.Add(10*time.Minute), false))
			f.clock = t0.Add(10 * time.Minute)
			in, _ := json.Marshal(map[string]any{"session_id": "sess-a", "cwd": f.repo, "hook_event_name": "UserPromptSubmit",
				"transcript_path": tr, "prompt": "next"})
			if _, errw, code := f.run(t, f.repo, string(in), "busy"); code != 0 {
				t.Fatal(errw)
			}
			// Turn 2 ends; its reply is written after Stop (or, as the control, before).
			if landed {
				appendLines(t, tr, reply(t0.Add(10*time.Minute+5*time.Second), 70_000))
			}
			f.clock = t0.Add(10*time.Minute + 6*time.Second)
			if _, errw, code := f.run(t, f.repo, stopJSON(f, "sess-a", tr), "idle"); code != 0 {
				t.Fatal(errw)
			}
			f.clock = t0.Add(12 * time.Minute)
			row := idleRow(t, f, "(sess-a)")
			if landed {
				if !strings.Contains(row, "prompt 71k") || !strings.Contains(row, "turn 1m ") {
					t.Fatalf("control: a reply on disk is the footprint, with its own age:\n%s", row)
				}
				return
			}
			if !strings.Contains(row, "prompt 61k") || !strings.Contains(row, "turn 11m ") {
				t.Fatalf("the rest shows the last observation with ITS age, not a fabricated now:\n%s", row)
			}
			if !strings.Contains(row, "cache 1h hot 48m") {
				t.Fatalf("and the cache clock runs from that older request — colder than true, never warmer:\n%s", row)
			}
		})
	}
}

// The Stop names its turn (prompt_id), and so do that turn's user records
// (promptId). A Stop whose turn is on disk BEHIND a newer prompt is late —
// the session has begun another turn: a queued prompt, or a successor
// incarnation after a bye and a hello — and records nothing. The Codex code
// pass found the second: the successor's pending prompt passes the time fence
// alone. Controls: the same transcript with the Stop naming the newest turn
// is marked; a Stop that sends no prompt_id, or names a turn not on disk,
// falls back to the time fences and is marked.
func TestIdleRecordsNothingForATurnANewerPromptHasSuperseded(t *testing.T) {
	boundedParallel(t)
	for _, tc := range []struct {
		name   string
		prompt string
		marked bool
	}{
		{"late: its turn is behind a newer prompt", "p1", false},
		{"control: the newest turn's own Stop", "p2", true},
		{"no prompt_id: the time fences, as before", "", true},
		{"a turn not on disk: the time fences, as before", "p9", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.initAndHello(t)
			tr := filepath.Join(t.TempDir(), "a.jsonl")
			t0 := f.clock
			writeLines(t, tr, promptRecord(t0.Add(time.Second), false, "p1"), reply(t0.Add(5*time.Second), 60_000),
				promptRecord(t0.Add(8*time.Second), false, "p2"))
			f.clock = t0.Add(9 * time.Second)
			b, _ := json.Marshal(map[string]any{"session_id": "sess-a", "cwd": f.repo, "hook_event_name": "Stop",
				"transcript_path": tr, "prompt_id": tc.prompt})
			if _, errw, code := f.run(t, f.repo, string(b), "idle"); code != 0 {
				t.Fatal(errw)
			}
			row := idleRow(t, f, "(sess-a)")
			if got := strings.Contains(row, "idle "); got != tc.marked {
				t.Fatalf("marked=%v, want %v:\n%s", got, tc.marked, row)
			}
			if !tc.marked && strings.Contains(row, "base ") {
				t.Fatalf("a late Stop records no base either:\n%s", row)
			}
		})
	}
}
