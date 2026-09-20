package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// turnLine builds one transcript record the way the harness writes it, with
// only the fields lastUsage reads. The filler makes a record long without
// making it interesting, so a test can push a real one out of the tail.
func turnLine(ts, model string, in, cacheRead, cacheWrite, out int64, sidechain bool, filler int) string {
	return fmt.Sprintf(
		`{"type":"assistant","isSidechain":%t,"timestamp":%q,"gitBranch":"main","pad":%q,`+
			`"message":{"model":%q,"role":"assistant","usage":{"input_tokens":%d,`+
			`"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":%d}}}`,
		sidechain, ts, strings.Repeat("p", filler), model, in, cacheWrite, cacheRead, out)
}

// toolResult is a long record with no usage in it: the shape that makes up
// most of a transcript's bytes and the one the prefilter must skip cheaply.
func toolResult(bytes int) string {
	return fmt.Sprintf(`{"type":"user","message":{"role":"user","content":%q}}`, strings.Repeat("x", bytes))
}

func TestLastUsageReadsTheNewestRealTurn(t *testing.T) {
	boundedParallel(t)
	// The fixtures below are sized in LITERAL bytes against a 64 KB window,
	// not in terms of tailBytes: a fixture that scales with the constant
	// cannot notice the constant changing, and the first version of this test
	// survived a mutation that grew the window a thousandfold — which would
	// have put a 64 MB read inside a 100 ms hook. Changing the budget is
	// allowed; changing it without re-sizing these is not.
	if tailBytes != 64<<10 {
		t.Fatalf("tailBytes is %d, not 64 KB: re-size this test's fixtures deliberately", tailBytes)
	}
	const (
		older = "2026-08-14T11:00:00.000Z"
		newer = "2026-08-14T12:00:00.000Z"
		// Comfortably past a 64 KB window, and the largest single line
		// measured in a real transcript here was 267 KB.
		pastTheWindow = 70 << 10
	)
	for _, tc := range []struct {
		name           string
		lines          []string
		noFinalNewline bool
		want           *usageSample
		why            string
	}{
		{
			name:  "newest turn wins",
			lines: []string{turnLine(older, "m1", 1, 10, 100, 5, false, 0), turnLine(newer, "m2", 2, 20, 200, 6, false, 0)},
			want:  &usageSample{Model: "m2", Prompt: 222, CacheRead: 20, CacheWrite: 200, Output: 6},
			why:   "prompt is everything the model was handed: input + cache read + cache write",
		},
		{
			name: "a subagent's turn is not the session's",
			lines: []string{
				turnLine(older, "m1", 1, 10, 100, 5, false, 0),
				turnLine(newer, "sub", 1, 1, 1, 1, true, 0),
			},
			want: &usageSample{Model: "m1", Prompt: 111, CacheRead: 10, CacheWrite: 100, Output: 5},
			why: "a sidechain record is the subagent's own context; reporting its 3k as the " +
				"session's is how a 90k peer looks free",
		},
		{
			name:  "a record past the tail is not reported",
			lines: []string{turnLine(older, "m1", 1, 10, 100, 5, false, 0), toolResult(pastTheWindow)},
			want:  nil,
			why: "a bounded read is a bounded ATTEMPT: the answer to a record outside the window " +
				"is to keep the last observation, never to grow the window",
		},
		{
			name: "a record inside the tail is reported past a huge one",
			lines: []string{
				toolResult(pastTheWindow),
				turnLine(newer, "m2", 2, 20, 200, 6, false, 0),
			},
			want: &usageSample{Model: "m2", Prompt: 222, CacheRead: 20, CacheWrite: 200, Output: 6},
			why:  "the positive control for the case above: the window itself works",
		},
		{
			name: "a half-written trailing record falls back",
			lines: []string{turnLine(older, "m1", 1, 10, 100, 5, false, 0),
				`{"type":"assistant","timestamp":"2026-08-14T13:00:00.000Z","message":{"usage":{"inp`},
			noFinalNewline: true,
			want:           &usageSample{Model: "m1", Prompt: 111, CacheRead: 10, CacheWrite: 100, Output: 5},
			why:            "beat can read a transcript mid-append; a torn record is skipped, not guessed at",
		},
		{
			name:  "a record with no usable timestamp is skipped",
			lines: []string{turnLine(older, "m1", 1, 10, 100, 5, false, 0), turnLine("not-a-time", "m2", 9, 9, 9, 9, false, 0)},
			want:  &usageSample{Model: "m1", Prompt: 111, CacheRead: 10, CacheWrite: 100, Output: 5},
			why: "substituting now() for a missing turn time would make the stalest possible " +
				"sample read as the freshest",
		},
		{
			name:  "a transcript with no usage anywhere",
			lines: []string{toolResult(16), `{"type":"summary"}`},
			want:  nil,
			why:   "nothing to report is not an error, and must not become a zero",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "t.jsonl")
			body := strings.Join(tc.lines, "\n")
			if !tc.noFinalNewline {
				body += "\n"
			}
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			got, ok := lastUsage(path)
			if tc.want == nil {
				if ok {
					t.Fatalf("want no sample (%s), got %+v", tc.why, got)
				}
				return
			}
			if !ok {
				t.Fatalf("want a sample (%s), got none", tc.why)
			}
			got.At = time.Time{} // compared separately below
			if got != *tc.want {
				t.Errorf("got %+v, want %+v\n  %s", got, *tc.want, tc.why)
			}
		})
	}
}

// TestLastUsageRefusesImplausibleCounts: a negative or absurd count is not a
// smaller problem than a missing record. `prompt -1` is nonsense a reader
// would have to explain, and two near-maxint fields sum to a NEGATIVE prompt
// that renders as a negative percentage.
func TestLastUsageRefusesImplausibleCounts(t *testing.T) {
	boundedParallel(t)
	const ts = "2026-08-14T12:00:00.000Z"
	for _, tc := range []struct {
		name string
		line string
	}{
		{"negative input", turnLine(ts, "m", -1, 0, 0, 0, false, 0)},
		{"negative cache read", turnLine(ts, "m", 1, -10, 0, 0, false, 0)},
		{"a sum that would wrap", turnLine(ts, "m", 1<<62, 1<<62, 0, 0, false, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "t.jsonl")
			if err := os.WriteFile(path, []byte(tc.line+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if got, ok := lastUsage(path); ok {
				t.Errorf("must report nothing rather than %+v", got)
			}
		})
	}
	// Positive control: the same shape with ordinary numbers IS reported, so
	// "it refused" cannot be confused with "the reader never ran".
	path := filepath.Join(t.TempDir(), "ok.jsonl")
	if err := os.WriteFile(path, []byte(turnLine(ts, "m", 1, 10, 100, 5, false, 0)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := lastUsage(path); !ok {
		t.Error("positive control: a plausible record must still be read")
	}
}

// TestLastUsageDoesNotBlockOnAFifo pins the one failure that would cost a
// tool call instead of an annotation: os.Open on a FIFO with no writer blocks
// forever, inside a 100 ms hook.
func TestLastUsageDoesNotBlockOnAFifo(t *testing.T) {
	boundedParallel(t)
	path := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan bool, 1)
	go func() { _, ok := lastUsage(path); done <- ok }()
	select {
	case ok := <-done:
		if ok {
			t.Error("a FIFO is not a transcript")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lastUsage BLOCKED on a FIFO with no writer — a hook that never returns")
	}
}

func TestLastUsageTurnTimeIsTheRecordsOwn(t *testing.T) {
	boundedParallel(t)
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte(turnLine("2026-08-14T11:30:00.000Z", "m", 1, 2, 3, 4, false, 0)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok := lastUsage(path)
	if !ok {
		t.Fatal("no sample")
	}
	want := time.Date(2026, 8, 14, 11, 30, 0, 0, time.UTC)
	if !got.At.Equal(want) {
		t.Errorf("turn time %s, want %s — the age of the NUMBER is the only thing that says "+
			"whether it can still be trusted", got.At, want)
	}
}

func TestLastUsageIsSilentOnEveryFailure(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()
	for _, tc := range []struct{ name, path string }{
		{"no transcript_path in the hook JSON", ""},
		{"a path that does not exist", filepath.Join(dir, "missing.jsonl")},
		{"a directory", dir},
	} {
		if _, ok := lastUsage(tc.path); ok {
			t.Errorf("%s: must report nothing, not a zero sample", tc.name)
		}
	}
}

func TestDeclaredWindowIsDeclaredOrNothing(t *testing.T) {
	boundedParallel(t)
	for _, tc := range []struct {
		in   string
		want int64
		why  string
	}{
		{"", 0, "unset means no percentage, which is the honest output"},
		{"1000000", 1_000_000, "a bare count"},
		{"1M", 1_000_000, "an operator types 1M, not 1000000"},
		{"200k", 200_000, ""},
		{" 200K ", 200_000, "a trailing newline or space in an exported value is the norm"},
		{"claude-opus-5", 0, "a model name is not a window: the transcript cannot tell the variants apart"},
		{"-5", 0, "a negative window would print a negative percentage"},
		{"0", 0, "zero IS the undeclared sentinel"},
		{"1.5M", 0, "unparsed rather than guessed at"},
		{"18446744073709552k", 0,
			"a declaration that would WRAP: parsed it is fine, multiplied it becomes 384, and a " +
				"384-token denominator prints an enormous confident percentage"},
		{"9223372036854775807", 0, "a bare count beyond any plausible window is a typo, not a window"},
	} {
		if got := declaredWindow(tc.in); got != tc.want {
			t.Errorf("declaredWindow(%q) = %d, want %d%s", tc.in, got, tc.want,
				map[bool]string{true: " — " + tc.why}[tc.why != ""])
		}
	}
}
