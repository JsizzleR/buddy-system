package cli

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// Issue #46 (D-055): an orchestrator is never told it has grown past the size
// its operator hands off at. BUDDY_HANDOFF_AT declares that size; busy
// (UserPromptSubmit) says so, once per turn, when the last OBSERVED prompt is
// at or past it.

const handoffMark = "you hand off at (" + EnvHandoffAt + ")"

// hookEvent is the hookEventName of busy's one document. The harness ignores
// a hookSpecificOutput that names another event, so right text under the
// wrong name is a line nobody sees — and additionalContext does not look.
func hookEvent(t *testing.T, out string) string {
	t.Helper()
	var v struct {
		HookSpecificOutput struct {
			HookEventName string `json:"hookEventName"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("not one hook JSON document: %q", out)
	}
	return v.HookSpecificOutput.HookEventName
}

// The whole decision, as a table: the declaration's spellings (parsed as
// BUDDY_CONTEXT_WINDOW is), the threshold's edge, and the missing
// observation. Every silent row has a speaking row beside it that differs in
// ONE input, so "it said nothing" cannot mean "it never ran".
func TestHandoffNoteDecides(t *testing.T) {
	boundedParallel(t)
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	turn := now.Add(-3 * time.Minute)
	for _, tc := range []struct {
		name     string
		declared string
		prompt   int64
		observed bool
		want     string // "" = silent; else a substring the line must carry
	}{
		{"unset", "", 900_000, true, ""},
		{"garbage", "lots", 900_000, true, ""},
		{"zero", "0", 900_000, true, ""},
		{"negative", "-5k", 900_000, true, ""},
		{"a fraction is not a number the parser takes", "0.5M", 900_000, true, ""},
		{"wraps when multiplied: undeclared, not 384", "18446744073709552k", 900_000, true, ""},
		{"under by one token", "500k", 499_999, true, ""},
		{"exactly at", "500k", 500_000, true, "your prompt was 500k at your last observed request (turn 3m ago), at or past the 500k " + handoffMark},
		{"over", "500k", 507_312, true, "your prompt was 507k"},
		{"no observation yet", "500k", 900_000, false, ""},
		{"no observation, control: the same size observed speaks", "500k", 900_000, true, "your prompt was 900k"},
		{"upper-case suffix", "500K", 500_000, true, "past the 500k"},
		{"spaces around it", " 500k ", 500_000, true, "past the 500k"},
		{"the M form, under", "1M", 999_999, true, ""},
		{"the M form, at", "1M", 1_000_000, true, "your prompt was 1.0M at your last observed request (turn 3m ago), at or past the 1.0M"},
		{"a bare count", "250000", 250_000, true, "past the 250k"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at := declaredWindow(tc.declared)
			got := handoffNote(now, at, store.ContextSample{Prompt: tc.prompt, TurnAt: turn}, tc.observed)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("want silence, got %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("want %q in:\n%s", tc.want, got)
			}
			// ONE line, pointing at the recipe, and never claiming to know
			// the size of the request the prompt is about to open.
			if strings.Count(got, "\n") != 1 || !strings.HasSuffix(got, "\n") {
				t.Errorf("the note must be exactly one line: %q", got)
			}
			if !strings.Contains(got, `(buddy skill: a coordinator, "Hand off before you are full"; a lane, "Landing through an orchestrator" step 6)`) {
				t.Errorf("the note must name the skill's recipe: %q", got)
			}
			if strings.Contains(got, " now") {
				t.Errorf("the observation lags a request; the note must never say now: %q", got)
			}
		})
	}
}

// observeB records a context observation for sess-b the way a live session
// gets one: a beat reads the prompt size off its own transcript. The turn is
// dated three minutes before the clock, so the note's age is a fact about the
// record.
func (f *fixture) observeB(t *testing.T, prompt int64) {
	t.Helper()
	ts := f.clock.Add(-3 * time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
	tr := filepath.Join(t.TempDir(), "b.jsonl")
	// input 2 + cache read + cache write 1000 = prompt.
	line := turnLine(ts, "claude-opus-5", "xhigh", 2, prompt-1_002, 1_000, 10, false, 0)
	if err := os.WriteFile(tr, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in, _ := json.Marshal(map[string]any{"session_id": "sess-b", "cwd": f.wtB, "tool_name": "Read",
		"transcript_path": tr, "tool_input": map[string]any{}})
	if _, errw, code := f.run(t, f.wtB, string(in), "beat"); code != 0 {
		t.Fatalf("beat: %s", errw)
	}
}

// End to end through the hook: declared and over speaks, and each silent case
// is the same fixture one input away from speaking.
func TestBusySaysWhenPastTheHandoffSize(t *testing.T) {
	boundedParallel(t)
	for _, tc := range []struct {
		name     string
		declared string
		prompt   int64 // 0 = never observed
		speaks   bool
	}{
		{"undeclared", "", 507_000, false},
		{"unparseable", "half a million", 507_000, false},
		{"under", "500k", 499_000, false},
		{"exactly at", "500k", 500_000, true},
		{"over", "500k", 507_000, true},
		{"no observation yet", "500k", 0, false},
		{"the 1M form", "1M", 1_200_000, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.initAndHello(t)
			if tc.prompt > 0 {
				f.observeB(t, tc.prompt)
			}
			f.env[EnvHandoffAt] = tc.declared
			out := f.busyB(t)
			if !tc.speaks {
				if out != "" {
					t.Fatalf("busy must stay exactly as it was (silent with nothing queued), got %q", out)
				}
				// And with mail queued, exactly as it was too: delivered,
				// marked, and no line (Codex, D-055 code pass — every silent
				// case had been tested with an empty inbox only).
				if _, errw, code := f.run(t, f.repo, "", "msg", "bravo", "ping"); code != 0 {
					t.Fatal(errw)
				}
				mail := f.busyB(t)
				if ctx := additionalContext(t, mail); !strings.Contains(ctx, "] ping\n") || strings.Contains(ctx, handoffMark) {
					t.Fatalf("a silent case must deliver the mail and nothing else:\n%s", ctx)
				}
				if ev := hookEvent(t, mail); ev != "UserPromptSubmit" {
					t.Errorf("hookEventName = %q, want UserPromptSubmit", ev)
				}
				if n := f.undeliveredTo(t, "sess-b", "bravo"); n != 0 {
					t.Fatalf("delivered, so marked: %d still queued", n)
				}
				// Positive control: one input away, the same session is told.
				f.env[EnvHandoffAt] = "1k"
				if tc.prompt == 0 {
					f.observeB(t, 2_000)
				}
				if ctx := additionalContext(t, f.busyB(t)); !strings.Contains(ctx, handoffMark) {
					t.Fatalf("control: declared under the observed size, busy must speak:\n%s", ctx)
				}
				return
			}
			if ev := hookEvent(t, out); ev != "UserPromptSubmit" {
				t.Errorf("the note-only document names event %q, want UserPromptSubmit", ev)
			}
			ctx := additionalContext(t, out)
			if !strings.HasPrefix(ctx, "BUDDY: your prompt was "+tokens(tc.prompt)+" at your last observed request (turn 3m ago)") ||
				!strings.Contains(ctx, handoffMark) {
				t.Fatalf("busy must name the observed size, its age and the threshold:\n%s", ctx)
			}
			// Level-triggered: the next prompt is told again, not once ever.
			if again := additionalContext(t, f.busyB(t)); again != ctx {
				t.Fatalf("every turn-opening prompt past the size carries the line; the second said:\n%s", again)
			}
		})
	}
}

// Declared, over, AND mail queued: ONE document with both parts, the note
// ahead of the inbox header (it is buddy's observation, not peer text), and
// the drain unchanged — the message delivered and marked.
func TestBusyHandoffRidesTheSameDocumentAsTheInbox(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.observeB(t, 612_000)
	f.env[EnvHandoffAt] = "500k"
	if _, errw, code := f.run(t, f.repo, "", "msg", "bravo", "--from", "orchestrator", "take R-7"); code != 0 {
		t.Fatal(errw)
	}
	out := f.busyB(t)
	dec := json.NewDecoder(strings.NewReader(out))
	var first, second json.RawMessage
	if err := dec.Decode(&first); err != nil {
		t.Fatalf("not hook JSON: %q", out)
	}
	if dec.Decode(&second) == nil {
		t.Fatalf("a hook event may write ONE document; busy wrote two:\n%s", out)
	}
	if ev := hookEvent(t, out); ev != "UserPromptSubmit" {
		t.Errorf("the note-plus-mail document names event %q, want UserPromptSubmit", ev)
	}
	ctx := additionalContext(t, out)
	note, inbox := strings.Index(ctx, handoffMark), strings.Index(ctx, "BUDDY MESSAGES (operator/peer text")
	if note < 0 || inbox < 0 || !strings.Contains(ctx, "] take R-7\n") {
		t.Fatalf("want the handoff line AND the message in the one document:\n%s", ctx)
	}
	if note > inbox {
		t.Errorf("the handoff line must come before the inbox header, not inside the peer-text block:\n%s", ctx)
	}
	if n := f.undeliveredTo(t, "sess-b", "bravo"); n != 0 {
		t.Fatalf("written, so delivered: %d still queued", n)
	}
}

// Write first, mark after (D-040) survives the new line: a document that
// never reached the session delivered nothing.
func TestBusyHandoffFailedWriteMarksNothing(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.observeB(t, 612_000)
	f.env[EnvHandoffAt] = "500k"
	if _, errw, code := f.run(t, f.repo, "", "msg", "bravo", "ping"); code != 0 {
		t.Fatal(errw)
	}
	code := Run([]string{"busy"}, Env{
		Stdin:  strings.NewReader(hookJSON("sess-b", f.wtB, "", "")),
		Stdout: &failWriter{},
		Stderr: &strings.Builder{},
		Cwd:    f.wtB,
		Now:    func() time.Time { return f.clock },
		Getenv: func(k string) string { return f.env[k] },
	})
	if code == 0 {
		t.Fatal("control: a busy whose write failed must say so")
	}
	if n := f.undeliveredTo(t, "sess-b", "bravo"); n != 1 {
		t.Fatalf("a failed write marked the message delivered (%d still queued)", n)
	}
}

// Never on beat: fifty tool calls a turn would be fifty copies of the line, on
// the session that can least afford the context. The control is the same
// session's busy saying it.
func TestBeatNeverCarriesTheHandoffLine(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.observeB(t, 612_000)
	f.env[EnvHandoffAt] = "500k"
	if ctx := f.beatB(t); strings.Contains(ctx, handoffMark) {
		t.Fatalf("beat must not carry the handoff line:\n%s", ctx)
	}
	if ctx := additionalContext(t, f.busyB(t)); !strings.Contains(ctx, handoffMark) {
		t.Fatalf("control: busy on the same session must:\n%s", ctx)
	}
}

// A revived session is not told its predecessor's size: the observation
// belongs to the incarnation that made it, and the new one has taken no turn.
// Two guards hold it, each enough alone: ContextSamples' JOIN on the live
// incarnation, and sampleOf's own check. Measured: dropping either one
// leaves this green, and dropping both turns it red.
func TestBusyHandoffIgnoresAPredecessorsObservation(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.observeB(t, 612_000)
	f.env[EnvHandoffAt] = "500k"
	if ctx := additionalContext(t, f.busyB(t)); !strings.Contains(ctx, handoffMark) {
		t.Fatalf("control: the live incarnation's own observation speaks:\n%s", ctx)
	}
	hook := hookJSON("sess-b", f.wtB, "", "")
	if _, errw, code := f.run(t, f.wtB, hook, "bye"); code != 0 {
		t.Fatal(errw)
	}
	f.clock = f.clock.Add(time.Minute)
	if _, errw, code := f.run(t, f.wtB, hook, "hello", "--label", "bravo"); code != 0 {
		t.Fatal(errw)
	}
	if out := f.busyB(t); out != "" {
		t.Fatalf("a revived incarnation has no observation of its own, so busy stays silent; got %q", out)
	}
}

// Declared and UNDER, with a row addressed by LABEL (the legacy shape
// TestBusyDeliversALabelAddressedRow covers undeclared): still delivered,
// still no line. The declaration must not change which rows are owed.
func TestBusyHandoffUnderStillDeliversALabelAddressedRow(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.observeB(t, 499_000)
	f.env[EnvHandoffAt] = "500k"
	db, err := sql.Open("sqlite", "file:"+filepath.Join(f.repo, ".git", "buddy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO inbox (target, sender, body, created) VALUES ('bravo','ana','legacy by label',?)`, f.clock.Unix()); err != nil {
		t.Fatal(err)
	}
	ctx := additionalContext(t, f.busyB(t))
	if !strings.Contains(ctx, "] legacy by label\n") || strings.Contains(ctx, handoffMark) {
		t.Fatalf("under the size: the label-addressed row, and no line:\n%s", ctx)
	}
	if n := f.undeliveredTo(t, "sess-b", "bravo"); n != 0 {
		t.Fatalf("delivered, so marked; %d still queued", n)
	}
}

// An ENDED session that is handed a prompt is still told. The observation is
// its own incarnation's (bye does not change the incarnation), and a prompt
// arriving under the id is the harness saying the process is running — the
// ledger's "ended" is the stale fact here, not the number. Pinned so a later
// Live() guard is a decision, not an accident (Codex asked; D-055 records it).
func TestBusyHandoffStillSpeaksToAnEndedSession(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.observeB(t, 612_000)
	f.env[EnvHandoffAt] = "500k"
	if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "", ""), "bye"); code != 0 {
		t.Fatal(errw)
	}
	if ctx := additionalContext(t, f.busyB(t)); !strings.Contains(ctx, "your prompt was 612k") {
		t.Fatalf("an ended session handed a prompt is still told its own observed size:\n%s", ctx)
	}
}
