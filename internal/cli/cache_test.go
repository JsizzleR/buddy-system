package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// The prompt cache has two lifetimes, 5 minutes and 1 hour, and which one a
// session is on decides whether handing it the next task is cheap or means
// rewriting its whole prefix. The transcript is the only thing that records
// it (usage.cache_creation), the model string is identical under both, and
// the operator asked for it on the roster (2026-09-20).

// turnLineTTL is turnLine with the per-tier cache_creation object the harness
// writes today. e5m/e1h of -1 omits the object entirely (an older harness).
func turnLineTTL(ts string, in, cacheRead, cacheWrite int64, e5m, e1h int64) string {
	cc := ""
	if e5m >= 0 || e1h >= 0 {
		if e5m < 0 {
			e5m = 0
		}
		if e1h < 0 {
			e1h = 0
		}
		cc = fmt.Sprintf(`,"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":%d}`, e5m, e1h)
	}
	return fmt.Sprintf(
		`{"type":"assistant","isSidechain":false,"timestamp":%q,"effort":"xhigh",`+
			`"message":{"model":"claude-opus-5","role":"assistant","usage":{"input_tokens":%d,`+
			`"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":7%s}}}`,
		ts, in, cacheWrite, cacheRead, cc)
}

func TestLastUsageReportsTheCacheTierTheTurnWrote(t *testing.T) {
	boundedParallel(t)
	const older, newer = "2026-08-14T11:00:00.000Z", "2026-08-14T12:00:00.000Z"
	for _, tc := range []struct {
		name           string
		lines          []string
		want5m, want1h int64
		why            string
	}{
		{"1h tier", []string{turnLineTTL(newer, 2, 100, 50, 0, 50)}, 0, 50, "the measured shape on this box"},
		{"5m tier", []string{turnLineTTL(newer, 2, 100, 50, 50, 0)}, 50, 0, "the tier a session drops to under overage"},
		{"both tiers", []string{turnLineTTL(newer, 2, 100, 80, 30, 50)}, 30, 50, "raw counts, both kept; the reader decides"},
		{"no object at all", []string{turnLineTTL(newer, 2, 100, 50, -1, -1)}, 0, 0, "an older harness: unrecorded is not 5m"},
		{"a pure cache read takes the tier from the turn that built the cache",
			[]string{turnLineTTL(older, 2, 100, 50, 0, 50), turnLineTTL(newer, 2, 150, 0, 0, 0)}, 0, 50,
			"1 of 2371 records measured wrote nothing; the cache it read was built at the earlier tier"},
		{"the newest WRITER wins over an older one",
			[]string{turnLineTTL(older, 2, 100, 50, 0, 50), turnLineTTL(newer, 2, 150, 20, 20, 0)}, 20, 0,
			"a session that dropped to 5m is on 5m now"},
		{"the newest writer wins by TIMESTAMP, not by position",
			[]string{turnLineTTL(newer, 2, 150, 20, 20, 0), turnLineTTL(older, 2, 100, 50, 0, 50)}, 20, 0,
			"issue #8's lesson applied to the tier: append order is not a property"},
		{"a pure read EARLIER in the file than an older writer",
			[]string{turnLineTTL(newer, 2, 150, 0, 0, 0), turnLineTTL(older, 2, 100, 50, 0, 50)}, 0, 50,
			"tier selection must not hide behind the prompt's newest-record skip"},
		{"two writers at the SAME timestamp: the later one in the file wins",
			[]string{turnLineTTL(newer, 2, 100, 50, 0, 50), turnLineTTL(newer, 2, 100, 20, 20, 0)}, 20, 0,
			"a tie goes to append order, stated so the policy is a test and not an accident"},
		{"a sidechain writer does not set the session's tier",
			[]string{turnLineTTL(older, 2, 100, 50, 0, 50),
				strings.Replace(turnLineTTL(newer, 1, 1, 9, 9, 0), `"isSidechain":false`, `"isSidechain":true`, 1)}, 0, 50,
			"a subagent's cache is its own"},
		{"a writer with an unusable timestamp does not set the tier",
			[]string{turnLineTTL(older, 2, 100, 50, 0, 50), turnLineTTL("not-a-time", 2, 100, 20, 20, 0)}, 0, 50,
			"no clock, no provenance"},
		{"a writer rejected for its counts does not set the tier",
			[]string{turnLineTTL(older, 2, 100, 50, 0, 50), turnLineTTL(newer, -1, 100, 20, 20, 0)}, 0, 50,
			"the first shape chose the tier before validating the counts, so a discarded turn drove the timer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "t.jsonl")
			if err := os.WriteFile(path, []byte(strings.Join(tc.lines, "\n")+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			got, ok := lastUsage(path)
			if !ok {
				t.Fatalf("want a sample (%s)", tc.why)
			}
			if got.Cache5m != tc.want5m || got.Cache1h != tc.want1h {
				t.Errorf("tiers 5m=%d 1h=%d, want 5m=%d 1h=%d\n  %s", got.Cache5m, got.Cache1h, tc.want5m, tc.want1h, tc.why)
			}
		})
	}
	// A negative tier count is not a tier: the record sets none, even when
	// its other tier is positive, and it still counts for the prompt size.
	path := filepath.Join(t.TempDir(), "neg.jsonl")
	neg := strings.Replace(turnLineTTL(newer, 2, 100, 50, 20, 50), `"ephemeral_1h_input_tokens":50`, `"ephemeral_1h_input_tokens":-50`, 1)
	if err := os.WriteFile(path, []byte(neg+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok := lastUsage(path)
	if !ok || got.Cache1h != 0 || got.Cache5m != 0 || got.Prompt != 152 || !got.TierAt.IsZero() {
		t.Fatalf("negative tier: want prompt 152 and no tier, got %+v ok=%v", got, ok)
	}
	// And the tier carries its own time: the writer's, not the newest turn's.
	path = filepath.Join(t.TempDir(), "prov.jsonl")
	if err := os.WriteFile(path, []byte(turnLineTTL(older, 2, 100, 50, 0, 50)+"\n"+turnLineTTL(newer, 2, 150, 0, 0, 0)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _ = lastUsage(path)
	if want := time.Date(2026, 8, 14, 11, 0, 0, 0, time.UTC); !got.TierAt.Equal(want) || !got.At.Equal(want.Add(time.Hour)) {
		t.Fatalf("tier time must be the WRITER's (%s), turn time the newest record's: %+v", want, got)
	}
}

func TestCacheNoteSaysHotOrColdWithItsOwnNumber(t *testing.T) {
	boundedParallel(t)
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	at := func(ago time.Duration) store.ContextSample {
		return store.ContextSample{TurnAt: now.Add(-ago), Prompt: 1000}
	}
	for _, tc := range []struct {
		name string
		c    store.ContextSample
		want string
	}{
		{"1h, 12m ago", func() store.ContextSample { c := at(12 * time.Minute); c.Cache1h = 40; return c }(), "cache 1h hot 48m"},
		{"1h, 65m ago", func() store.ContextSample { c := at(65 * time.Minute); c.Cache1h = 40; return c }(), "cache 1h cold 5m"},
		{"1h, exactly 60m ago", func() store.ContextSample { c := at(time.Hour); c.Cache1h = 40; return c }(), "cache 1h cold 0s"},
		{"5m, 2m ago", func() store.ContextSample { c := at(2 * time.Minute); c.Cache5m = 40; return c }(), "cache 5m hot 3m"},
		{"5m, 10m ago", func() store.ContextSample { c := at(10 * time.Minute); c.Cache5m = 40; return c }(), "cache 5m cold 5m"},
		{"both tiers, 3m ago: hot by the SHORTER", func() store.ContextSample { c := at(3 * time.Minute); c.Cache5m, c.Cache1h = 1, 1; return c }(), "cache 1h+5m hot 2m"},
		{"both tiers, 30m ago: cold although the 1h part is not", func() store.ContextSample { c := at(30 * time.Minute); c.Cache5m, c.Cache1h = 1, 1; return c }(), "cache 1h+5m cold 25m"},
		{"no tier recorded: nothing, not 5m", at(time.Minute), ""},
		{"1h, one millisecond before expiry", func() store.ContextSample { c := at(time.Hour - time.Millisecond); c.Cache1h = 1; return c }(), "cache 1h hot 0s"},
		{"1h, one millisecond after expiry", func() store.ContextSample { c := at(time.Hour + time.Millisecond); c.Cache1h = 1; return c }(), "cache 1h cold 0s"},
		{"5m, one millisecond before expiry", func() store.ContextSample { c := at(5*time.Minute - time.Millisecond); c.Cache5m = 1; return c }(), "cache 5m hot 0s"},
		{"observed later than the turn changes nothing: the turn is the clock",
			func() store.ContextSample { c := at(12 * time.Minute); c.Cache1h = 40; c.Observed = now; return c }(), "cache 1h hot 48m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cacheNote(now, tc.c); got != tc.want {
				t.Errorf("cacheNote = %q, want %q", got, tc.want)
			}
			// And the whole context note carries it after the turn age, so
			// the two clocks sit side by side.
			full := contextNote(now, tc.c)
			if tc.want == "" {
				if strings.Contains(full, "cache") {
					t.Errorf("no tier must print no cache word: %q", full)
				}
				return
			}
			if !strings.HasSuffix(full, " turn "+age(now, tc.c.TurnAt)+" "+tc.want) {
				t.Errorf("context note must end with the turn age then the cache note: %q", full)
			}
		})
	}
}

// End to end: beat reads the tier, the ledger keeps it, the roster prints it,
// and it ages by the turn's own clock.
func TestRosterCacheTimerFollowsTheTurn(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	must := func(stdin string, args ...string) {
		t.Helper()
		if _, errw, code := f.run(t, f.repo, stdin, args...); code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, errw)
		}
	}
	// The turn is dated TEN MINUTES before the beat that captures it, so the
	// capture time and the turn time differ and a timer run off the wrong
	// clock cannot pass by coincidence.
	ts := f.clock.Add(-10 * time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
	tr := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(tr, []byte(turnLineTTL(ts, 2, 86_378, 4_119, 0, 4_119)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	beat := func() string {
		in := map[string]any{"session_id": "sess-a", "cwd": f.repo, "tool_name": "Read",
			"transcript_path": tr, "tool_input": map[string]any{}}
		b, _ := json.Marshal(in)
		return string(b)
	}
	must("", "init")
	must(hookJSON("sess-a", f.repo, "", ""), "hello", "--label", "alpha")
	row := func() string {
		out, errw, code := f.run(t, f.repo, "", "sessions")
		if code != 0 {
			t.Fatalf("sessions: %s", errw)
		}
		return out
	}
	must(beat(), "beat")
	if got := row(); !strings.Contains(got, "prompt 90k turn 10m cache 1h hot 50m") {
		t.Fatalf("a 1h write ten minutes old has fifty left, whatever the capture time:\n%s", got)
	}
	f.clock = f.clock.Add(35 * time.Minute)
	must(hookJSON("sess-a", f.repo, "", ""), "beat") // no transcript: the observation stands and ages
	if got := row(); !strings.Contains(got, "turn 45m cache 1h hot 15m") {
		t.Fatalf("the timer runs on the TURN's clock:\n%s", got)
	}
	f.clock = f.clock.Add(20 * time.Minute)
	if got := row(); !strings.Contains(got, "turn 1h cache 1h cold 5m") {
		t.Fatalf("past the hour it is cold, and says by how much:\n%s", got)
	}
	// A later turn that only READ the cache keeps the tier and restarts the
	// clock on the read: the cache lifetime refreshes on use.
	ts2 := f.clock.UTC().Format("2006-01-02T15:04:05.000Z")
	if err := os.WriteFile(tr, []byte(turnLineTTL(ts, 2, 86_378, 4_119, 0, 4_119)+"\n"+turnLineTTL(ts2, 2, 90_497, 0, 0, 0)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	must(beat(), "beat")
	if got := row(); !strings.Contains(got, "turn 0s cache 1h hot 1h") {
		t.Fatalf("a pure read on a 1h cache is a fresh hour (age renders 60m as 1h):\n%s", got)
	}

	// PROVENANCE TRAVELS THROUGH BEAT. The tier is guarded in the ledger by
	// the WRITER's clock, which only works if beat hands that clock over.
	// Sequence: a 5m writer lands at the same turn time as the read above,
	// so the tier becomes 5m; then a re-read of a tail in which that writer
	// is missing (the shape an out-of-order append leaves) offers the OLDER
	// 1h evidence on an EQUAL newest turn. The tier must stay 5m. A beat that
	// dropped the tier's time would let the older evidence win, silently.
	if err := os.WriteFile(tr, []byte(turnLineTTL(ts, 2, 86_378, 4_119, 0, 4_119)+"\n"+turnLineTTL(ts2, 2, 90_000, 500, 500, 0)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	must(beat(), "beat")
	if got := row(); !strings.Contains(got, "cache 5m hot 5m") {
		t.Fatalf("control: a 5m writer at the newest turn puts the session on 5m:\n%s", got)
	}
	if err := os.WriteFile(tr, []byte(turnLineTTL(ts, 2, 86_378, 4_119, 0, 4_119)+"\n"+turnLineTTL(ts2, 2, 90_497, 0, 0, 0)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	must(beat(), "beat")
	if got := row(); !strings.Contains(got, "cache 5m hot 5m") {
		t.Fatalf("older tier evidence on an equal turn must not regress the tier — beat must carry the writer's clock:\n%s", got)
	}
}
