package cli

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// D-033: `buddy wait`, `wait check`, `wait clear`, `wait ls`, and the views.
// Every refusal has a positive control beside it.

// waitFx is the shared shape: alpha (sess-a, in the repo) waits; bravo
// (sess-b, in the second worktree) holds claim api-work on internal/api.
// alpha's transcript is a file the test rewrites to put a turn on record.
type waitFx struct {
	*fixture
	tr string
}

func newWaitFx(t *testing.T) *waitFx {
	t.Helper()
	f := newFixture(t)
	f.initAndHello(t)
	w := &waitFx{fixture: f, tr: filepath.Join(t.TempDir(), "alpha.jsonl")}
	w.ok(t, "sess-b", "claim", "api-work", "--desc", "x", "--scope", "internal/api")
	return w
}

func (w *waitFx) cwdOf(sid string) string {
	if sid == "sess-b" {
		return w.wtB
	}
	return w.repo
}

// as runs a verb as a session's own Bash tool call: the harness's id in the
// environment, from that session's worktree.
func (w *waitFx) as(t *testing.T, sid string, args ...string) (string, string, int) {
	t.Helper()
	var out, errw string
	var code int
	w.asSession(sid, func() { out, errw, code = w.run(t, w.cwdOf(sid), "", args...) })
	return out, errw, code
}

func (w *waitFx) ok(t *testing.T, sid string, args ...string) string {
	t.Helper()
	out, errw, code := w.as(t, sid, args...)
	if code != 0 {
		t.Fatalf("%v as %s: exit %d: %s", args, sid, code, errw)
	}
	return out
}

// turn puts one assistant record on alpha's transcript at `at` and runs the
// beat that reads it — the ledger's observation of alpha's last request.
func (w *waitFx) turn(t *testing.T, at time.Time, read, write, e5m, e1h int64) {
	t.Helper()
	line := turnLineTTL(at.UTC().Format("2006-01-02T15:04:05.000Z"), 2, read, write, e5m, e1h)
	if err := os.WriteFile(w.tr, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.beatCtx(t)
}

// beatCtx runs alpha's PostToolUse beat (with the transcript) and returns
// the additionalContext it injected, "" for none.
func (w *waitFx) beatCtx(t *testing.T) string {
	t.Helper()
	in, _ := json.Marshal(map[string]any{"session_id": "sess-a", "cwd": w.repo, "tool_name": "Bash",
		"transcript_path": w.tr, "tool_input": map[string]any{}})
	out, errw, code := w.run(t, w.repo, string(in), "beat")
	if code != 0 {
		t.Fatalf("beat: %s", errw)
	}
	return additionalContext(t, out)
}

func lines(s string) []string { return strings.Split(strings.TrimRight(s, "\n"), "\n") }

// The whole round trip, with every line it prints held to its text.
func TestWaitDeclareCheckLandOnceThenNoWait(t *testing.T) {
	boundedParallel(t)
	w := newWaitFx(t)
	w.turn(t, w.clock, 214_000, 1_200, 0, 1_200) // a warm request on the 1h tier

	out := w.ok(t, "sess-a", "wait", "--on", "api-work", "--note", "then: rebase")
	got := lines(out)
	if len(got) != 2 {
		t.Fatalf("a first declaration prints two lines:\n%s", out)
	}
	if want := `WAITING on claim "api-work" (held by bravo, seen 0s ago) — deadline in 3h0m; note: then: rebase`; got[0] != want {
		t.Fatalf("declaration line:\n got %q\nwant %q", got[0], want)
	}
	if !strings.HasPrefix(got[1], "keep-alive: cache 1h tier (last written 0s ago). Arm it in THIS session: /loop buddy wait check") ||
		!strings.Contains(got[1], "(50m on this tier)") || !strings.Contains(got[1], "fixed fallback: /loop 30m buddy wait check") {
		t.Fatalf("the arming line:\n%s", got[1])
	}

	w.clock = w.clock.Add(10 * time.Minute)
	out = w.ok(t, "sess-a", "wait", "check")
	got = lines(out)
	if want := `STILL WAITING on claim "api-work" (held by bravo, seen 10m ago) — 10m so far, deadline in 2h50m; next check in 50m (3000s from now)`; got[0] != want {
		t.Fatalf("STILL WAITING:\n got %q\nwant %q", got[0], want)
	}
	if want := "last observed request 10m ago read 214k, wrote 1k (mostly read from the cache)"; len(got) != 2 || got[1] != want {
		t.Fatalf("the observation line:\n%s", out)
	}
	if ctx := w.beatCtx(t); strings.Contains(ctx, "LANDED") {
		t.Fatalf("nothing has landed yet:\n%s", ctx)
	}

	w.clock = w.clock.Add(5 * time.Minute)
	rel := w.ok(t, "sess-b", "release", "api-work")
	if want := `released "api-work" — 1 session(s) had declared a wait on it: alpha 15m; nothing is sent, and a waiter's next ` + "`buddy wait check`" + ` reads this release`; strings.TrimSpace(rel) != want {
		t.Fatalf("release must name the waiter:\n got %q\nwant %q", rel, want)
	}

	// beat says LANDED ONCE, and does not clear the row: the check does.
	w.clock = w.clock.Add(time.Minute)
	ctx := w.beatCtx(t)
	if want := "BUDDY: your wait LANDED — claim \"api-work\" released 1m ago; note: then: rebase. `buddy wait check` clears it; then stop the /loop that runs it.\n"; ctx != want {
		t.Fatalf("the one-shot notice:\n got %q\nwant %q", ctx, want)
	}
	if ctx := w.beatCtx(t); strings.Contains(ctx, "LANDED") {
		t.Fatalf("the notice must not repeat:\n%s", ctx)
	}

	out = w.ok(t, "sess-a", "wait", "check")
	got = lines(out)
	if want := `LANDED: claim "api-work" released 1m ago — the wait is over after 16m; stop the /loop that runs this check (schedule no further check)`; got[0] != want {
		t.Fatalf("LANDED:\n got %q\nwant %q", got[0], want)
	}
	if len(got) != 3 || got[1] != "note: then: rebase" {
		t.Fatalf("LANDED carries the note on its own fenced line:\n%s", out)
	}
	out = w.ok(t, "sess-a", "wait", "check")
	if want := "NO WAIT: nothing is registered for this session (your last wait LANDED 0s ago) — stop the /loop that runs this check (schedule no further check)"; lines(out)[0] != want {
		t.Fatalf("the check after LANDED:\n got %q\nwant %q", lines(out)[0], want)
	}
}

// Fable design pass, 7(b): the plan's formula read the ledger's clock, which
// lags one request. Seeded with an observation fifty minutes old, it would
// print a next check in about two minutes; from-now prints fifty, every time.
func TestWaitCheckPacesFromNowNotFromTheLedgersClock(t *testing.T) {
	boundedParallel(t)
	w := newWaitFx(t)
	w.turn(t, w.clock, 214_000, 1_200, 0, 1_200)
	w.ok(t, "sess-a", "wait", "--on", "api-work", "--until", "12h")
	for i := 0; i < 3; i++ {
		w.clock = w.clock.Add(50 * time.Minute) // no beat in between: the observation only ages
		out := w.ok(t, "sess-a", "wait", "check")
		if !strings.HasSuffix(lines(out)[0], "; next check in 50m (3000s from now)") {
			t.Fatalf("check %d, observation %v old: want the next check 50m from now:\n%s", i+1, time.Duration(i+1)*50*time.Minute, out)
		}
	}
}

func TestWaitCheckCapsTheNextCheckAtTheDeadline(t *testing.T) {
	boundedParallel(t)
	w := newWaitFx(t)
	w.turn(t, w.clock, 214_000, 1_200, 0, 1_200)
	w.ok(t, "sess-a", "wait", "--until", "20m")
	out := w.ok(t, "sess-a", "wait", "check")
	if !strings.HasSuffix(lines(out)[0], "next check in 20m (1200s from now, at the deadline)") {
		t.Fatalf("a deadline inside the period paces to it:\n%s", out)
	}
	// Rounded UP, never to the nearest: with 29 s over a whole minute, the
	// nearest minute schedules a check BEFORE the deadline (Codex code pass).
	w.clock = w.clock.Add(-(29 * time.Second))
	out = w.ok(t, "sess-a", "wait", "check")
	if !strings.HasSuffix(lines(out)[0], "next check in 21m (1260s from now, at the deadline)") {
		t.Fatalf("20m29s left must round up to 21m:\n%s", out)
	}
	w.clock = w.clock.Add(29 * time.Second)
	// 20 s left: still a whole minute, never zero.
	w.clock = w.clock.Add(19*time.Minute + 40*time.Second)
	out = w.ok(t, "sess-a", "wait", "check")
	if !strings.HasSuffix(lines(out)[0], "next check in 1m (60s from now, at the deadline)") {
		t.Fatalf("a sub-minute remainder floors at the scheduler's minute:\n%s", out)
	}
	w.clock = w.clock.Add(time.Minute)
	out = w.ok(t, "sess-a", "wait", "check")
	if !strings.HasPrefix(out, "EXPIRED: the deadline passed 40s ago — the wait is over after 20m; stop the /loop that runs this check (schedule no further check), and ask before waiting longer\n") {
		t.Fatalf("a timer past its deadline:\n%s", out)
	}
}

// The tier decides whether a keep-alive pays: on 5m, and on a turn that
// wrote both tiers (judged by the shorter, as the roster judges it, D-020),
// nothing is armed and the check says to stop. The 1h tier is the control.
func TestWaitArmsNothingOffTheHourTier(t *testing.T) {
	boundedParallel(t)
	for _, tc := range []struct {
		name     string
		e5m, e1h int64
		pays     bool
	}{
		{"1h tier (control)", 0, 1_200, true},
		{"5m tier", 1_200, 0, false},
		{"both tiers", 600, 600, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWaitFx(t)
			w.turn(t, w.clock, 214_000, 1_200, tc.e5m, tc.e1h)
			decl := lines(w.ok(t, "sess-a", "wait", "--on", "api-work"))[1]
			check := lines(w.ok(t, "sess-a", "wait", "check"))[0]
			armed := strings.Contains(decl, "Arm it in THIS session: /loop buddy wait check")
			paced := strings.Contains(check, "next check in 50m")
			if armed != tc.pays || paced != tc.pays {
				t.Fatalf("pays=%v: armed=%v paced=%v\n%s\n%s", tc.pays, armed, paced, decl, check)
			}
			if !tc.pays && (!strings.HasPrefix(decl, "keep-alive: NONE") || !strings.Contains(check, "no next check:")) {
				t.Fatalf("off the hour tier both lines must say why:\n%s\n%s", decl, check)
			}
		})
	}
}

// COLD WRITE is the counts: more written than read. A warm record is the
// control, and neither line calls itself this check's own request.
func TestWaitCheckReportsTheLastObservedRequestByItsCounts(t *testing.T) {
	boundedParallel(t)
	w := newWaitFx(t)
	w.turn(t, w.clock, 21_000, 401_000, 0, 401_000)
	w.ok(t, "sess-a", "wait", "--on", "api-work")
	w.clock = w.clock.Add(50 * time.Minute)
	out := lines(w.ok(t, "sess-a", "wait", "check"))[1]
	if want := "COLD WRITE: last observed request 50m ago read 21k, wrote 401k — more of that prompt was written to the cache than read from it: a lapsed cache (for a check, a keep-alive that missed) or a changed or grown prompt; the counts cannot say which"; out != want {
		t.Fatalf("cold:\n got %q\nwant %q", out, want)
	}
	w.turn(t, w.clock, 401_000, 300, 0, 300)
	if out := lines(w.ok(t, "sess-a", "wait", "check"))[1]; out != "last observed request 0s ago read 401k, wrote 300 (mostly read from the cache)" {
		t.Fatalf("control, warm:\n%s", out)
	}
	// Nothing read and nothing written is not "read from the cache".
	w.turn(t, w.clock, 0, 0, 0, 0)
	if out := lines(w.ok(t, "sess-a", "wait", "check"))[1]; out != "last observed request 0s ago read 0, wrote 0 (nothing read from the cache)" {
		t.Fatalf("an all-zero record:\n%s", out)
	}
	// The boundary is write > read, exactly: one token either side.
	for _, tc := range []struct {
		read, write int64
		cold        bool
	}{{200_000, 200_001, true}, {200_000, 200_000, false}, {200_001, 200_000, false}} {
		w.turn(t, w.clock, tc.read, tc.write, 0, tc.write)
		out := lines(w.ok(t, "sess-a", "wait", "check"))[1]
		if strings.HasPrefix(out, "COLD WRITE") != tc.cold {
			t.Fatalf("read %d write %d: cold=%v, got:\n%s", tc.read, tc.write, tc.cold, out)
		}
	}
}

// Every view reads a wait back only as the CURRENT incarnation's, the rule
// session_context and session_idle already follow. Cleanup closes a
// predecessor's row before any reader can see it, so the guard is armed here
// with a planted row: an OPEN wait carrying an incarnation that is not the
// session's is not the session's wait, on any surface.
func TestViewsIgnoreAnOpenWaitOfAnotherIncarnation(t *testing.T) {
	boundedParallel(t)
	w := newWaitFx(t)
	w.ok(t, "sess-a", "wait", "--on", "api-work")
	if who := w.ok(t, "sess-a", "who", "alpha"); !strings.Contains(who, "WAITING      on claim") {
		t.Fatalf("control: the session's own wait is shown:\n%s", who)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(w.repo, ".git", "buddy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE session_waits SET incarnation='an-earlier-one' WHERE session_id='sess-a'`); err != nil {
		t.Fatal(err)
	}
	if who := w.ok(t, "sess-a", "who", "alpha"); !strings.Contains(who, "WAITING      none declared") {
		t.Fatalf("another incarnation's open row shown as this session's:\n%s", who)
	}
	if out := w.ok(t, "sess-b", "msg", "alpha", "hi"); strings.Contains(out, "declared a wait") {
		t.Fatalf("msg reported another incarnation's wait:\n%s", out)
	}
}

// Fable design pass: the check is the harness's own session's act. From a
// shell with no harness id it refuses and writes nothing; --session is not a
// flag it has; an override that disagrees with the harness is refused.
func TestWaitCheckSpeaksOnlyForTheHarnessSession(t *testing.T) {
	boundedParallel(t)
	w := newWaitFx(t)
	w.ok(t, "sess-a", "wait", "--on", "api-work")
	if _, errw, code := w.run(t, w.repo, "", "wait", "check"); code == 0 || !strings.Contains(errw, "is unset here") {
		t.Fatalf("no harness id: want a refusal, got %d %q", code, errw)
	}
	if _, errw, code := w.as(t, "sess-a", "wait", "check", "--session", "sess-a"); code == 0 || !strings.Contains(errw, "flag provided but not defined") {
		t.Fatalf("--session: want an unknown-flag refusal, got %d %q", code, errw)
	}
	w.env[EnvSession] = "sess-b"
	if _, errw, code := w.as(t, "sess-a", "wait", "check"); code == 0 || !strings.Contains(errw, "a check speaks only for the harness's own session") {
		t.Fatalf("a disagreeing override: want a refusal, got %d %q", code, errw)
	}
	delete(w.env, EnvSession)
	if out, _, _ := w.run(t, w.repo, "", "wait", "ls"); !strings.Contains(out, "no check since declaring") {
		t.Fatalf("the refused checks must have written nothing:\n%s", out)
	}
	// Control: the harness's own session checks.
	if out := w.ok(t, "sess-a", "wait", "check"); !strings.HasPrefix(out, "STILL WAITING") {
		t.Fatalf("control:\n%s", out)
	}
}

func TestWaitDeclarationRefusalsWriteNothing(t *testing.T) {
	boundedParallel(t)
	cases := []struct {
		name    string
		args    []string
		errHas  string
		control []string
	}{
		{"a positional slug", []string{"api-work", "--until", "1h"}, "did you mean `buddy wait --on api-work`", []string{"--on", "api-work", "--until", "1h"}},
		{"over the ceiling", []string{"--on", "api-work", "--until", "20h"}, "over the 12h0m0s ceiling", []string{"--on", "api-work", "--until", "12h"}},
		{"an unknown slug", []string{"--on", "nope"}, `--on "nope": no claim by that slug`, []string{"--on", "api-work"}},
		{"a note over the rendered cap", []string{"--note", strings.Repeat("x", 511) + "\n"}, "--note renders to 514 bytes and the cap is 512, so 2 would be cut", []string{"--note", strings.Repeat("x", 512)}},
		{"an unknown flag", []string{"--on", "api-work", "--for", "1h"}, "flag provided but not defined: -for", []string{"--on", "api-work"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWaitFx(t)
			args := append([]string{"wait"}, tc.args...)
			if _, errw, code := w.as(t, "sess-a", args...); code == 0 || !strings.Contains(errw, tc.errHas) {
				t.Fatalf("want a refusal carrying %q, got %d %q", tc.errHas, code, errw)
			}
			if out := w.ok(t, "sess-a", "wait", "ls"); strings.TrimSpace(out) != "no open waits" {
				t.Fatalf("a refused declaration wrote a row:\n%s", out)
			}
			w.ok(t, "sess-a", append([]string{"wait"}, tc.control...)...)
			if out := w.ok(t, "sess-a", "wait", "ls"); !strings.HasPrefix(out, "wait alpha") {
				t.Fatalf("control: the accepted declaration must be listed:\n%s", out)
			}
		})
	}
}

// --help in any position answers and writes nothing (D-031).
func TestWaitHelpWritesNothing(t *testing.T) {
	boundedParallel(t)
	w := newWaitFx(t)
	for _, args := range [][]string{{"wait", "--help"}, {"wait", "--on", "api-work", "--help"}, {"wait", "check", "--help"}, {"wait", "clear", "-h"}} {
		out, _, code := w.as(t, "sess-a", args...)
		if code != 0 || !strings.HasPrefix(out, "usage: buddy wait") {
			t.Fatalf("%v: want the usage line, got %d %q", args, code, out)
		}
	}
	if out := w.ok(t, "sess-a", "wait", "ls"); strings.TrimSpace(out) != "no open waits" {
		t.Fatalf("--help wrote a row:\n%s", out)
	}
}

// A refused claim suggests the wait and registers nothing (declared or
// nothing); a slug with a space is shell-quoted in the suggestion.
func TestRefusedClaimSuggestsAWaitAndRegistersNothing(t *testing.T) {
	boundedParallel(t)
	w := newWaitFx(t)
	w.ok(t, "sess-b", "claim", "two words", "--desc", "x", "--scope", "docs")
	out, _, code := w.as(t, "sess-a", "claim", "mine", "--desc", "x", "--scope", "internal/api/x.go", "--scope", "docs/y.md")
	if code == 0 {
		t.Fatal("control: the claim must be refused")
	}
	if want := "to be told when it frees: buddy wait --on api-work --on 'two words'\n"; !strings.HasSuffix(out, want) {
		t.Fatalf("the suggestion after the REFUSED lines:\n%s", out)
	}
	out, _, _ = w.as(t, "sess-a", "claim", "mine", "--desc", "x", "--scope", "internal/api", "--dry-run")
	if !strings.Contains(out, "to be told when it frees: buddy wait --on api-work\n") {
		t.Fatalf("the dry run suggests it too:\n%s", out)
	}
	if out := w.ok(t, "sess-a", "wait", "ls"); strings.TrimSpace(out) != "no open waits" {
		t.Fatalf("a refusal registered a wait:\n%s", out)
	}
}

func rosterRow(t *testing.T, w *waitFx, label string) string {
	t.Helper()
	out := w.ok(t, "sess-a", "sessions")
	for _, ln := range lines(out) {
		if fs := strings.Fields(ln); len(fs) > 1 && fs[1] == label {
			return ln
		}
	}
	t.Fatalf("no row for %q in:\n%s", label, out)
	return ""
}

// The roster, `who` both ways, and `wait ls` render the one Verdict: a wait
// that has landed or expired and not been checked says so everywhere.
func TestViewsRenderTheOneVerdict(t *testing.T) {
	boundedParallel(t)
	w := newWaitFx(t)
	w.ok(t, "sess-a", "wait", "--on", "api-work", "--note", "then: rebase")
	w.clock = w.clock.Add(72 * time.Minute)
	w.turn(t, w.clock, 214_000, 1_200, 0, 1_200) // a ping: busy/idle/beat all run, the wait's age must not reset
	if got := rosterRow(t, w, "alpha"); !strings.Contains(got, "  waiting 1h12m  ") {
		t.Fatalf("the roster dates the wait by its declaration:\n%s", got)
	}
	holder := w.ok(t, "sess-a", "who", "bravo")
	if !strings.Contains(holder, "WAITED ON    by 1 session(s): alpha 1h12m\n") {
		t.Fatalf("the holder's report names its waiter:\n%s", holder)
	}
	who := w.ok(t, "sess-a", "who", "alpha")
	if !strings.Contains(who, `WAITING      on claim "api-work" (held by bravo, seen 1h ago) — declared 1h12m ago, deadline in 1h48m, no check since declaring; last observed request 0s ago read 214k, wrote 1k (mostly read from the cache); note: then: rebase`) {
		t.Fatalf("who's WAITING line:\n%s", who)
	}

	w.ok(t, "sess-b", "release", "api-work")
	if got := rosterRow(t, w, "alpha"); !strings.Contains(got, "waiting 1h12m LANDED") {
		t.Fatalf("landed and not yet checked:\n%s", got)
	}
	if who := w.ok(t, "sess-a", "who", "alpha"); !strings.Contains(who, "LANDED: every claim it waits on has closed (a check clears it)") {
		t.Fatalf("who must render the same verdict:\n%s", who)
	}
	if ls := w.ok(t, "sess-a", "wait", "ls"); !strings.Contains(ls, "LANDED: every claim it waits on has closed") {
		t.Fatalf("wait ls must render the same verdict:\n%s", ls)
	}

	// A timer past its deadline, never checked.
	w.ok(t, "sess-a", "wait", "--until", "1h")
	w.clock = w.clock.Add(2 * time.Hour)
	if got := rosterRow(t, w, "alpha"); !strings.Contains(got, "waiting 2h0m EXPIRED") {
		t.Fatalf("expired and not yet checked:\n%s", got)
	}
	// Control: bravo declared nothing and holds nothing.
	if got := w.ok(t, "sess-b", "status"); !strings.Contains(got, "WAITING      none declared") || strings.Contains(got, "WAITED ON") {
		t.Fatalf("a session with no wait and no claims:\n%s", got)
	}
}

// msg to a waiter: D-032's arm first, then what the recipient declared.
func TestMsgToAWaiterSaysWhatItDeclared(t *testing.T) {
	boundedParallel(t)
	w := newWaitFx(t)
	w.ok(t, "sess-a", "wait", "--on", "api-work")
	w.clock = w.clock.Add(10 * time.Minute)
	out := w.ok(t, "sess-b", "msg", "alpha", "free now")
	// Every D-032 arm ends in the mechanism; the wait note comes after it.
	arm, wait := strings.Index(out, "delivery waits for its next tool call"), strings.Index(out, "; it declared a wait 10m ago on claim \"api-work\"")
	if arm < 0 || wait < arm || !strings.Contains(out, "deadline in 2h50m, no check since declaring") {
		t.Fatalf("the waiter note follows the observation arm:\n%s", out)
	}
	if strings.Contains(out, "delivered") || strings.Contains(out, "will") {
		t.Fatalf("an observation, never a forecast:\n%s", out)
	}
	// Control: a recipient with no wait gets no such note.
	if out := w.ok(t, "sess-a", "msg", "bravo", "hi"); strings.Contains(out, "declared a wait") {
		t.Fatalf("no wait, no note:\n%s", out)
	}
}

// hello restates this incarnation's wait and questions the scheduled check;
// after a bye and a revival it names the PREDECESSOR's wait as the earlier
// run's, with the line that declares it again — and not past its deadline.
func TestHelloRestatesAWaitAndNamesAPredecessors(t *testing.T) {
	boundedParallel(t)
	w := newWaitFx(t)
	w.ok(t, "sess-a", "wait", "--on", "api-work", "--note", "then: rebase")
	w.clock = w.clock.Add(5 * time.Minute)
	out, errw, code := w.run(t, w.repo, hookJSON("sess-a", w.repo, "", ""), "hello")
	if code != 0 {
		t.Fatal(errw)
	}
	if !strings.Contains(out, `BUDDY: you are WAITING on claim "api-work" (held by bravo, seen 5m ago) — declared 5m ago, deadline in 2h55m; note: then: rebase. The harness keeps scheduled checks for the session only, not on disk: if no /loop running `+"`buddy wait check`"+` is scheduled here, re-arm it: /loop buddy wait check`) {
		t.Fatalf("a same-incarnation hello:\n%s", out)
	}
	if _, errw, code := w.run(t, w.repo, hookJSON("sess-a", w.repo, "", ""), "bye"); code != 0 {
		t.Fatal(errw)
	}
	w.clock = w.clock.Add(time.Minute)
	out, errw, code = w.run(t, w.repo, hookJSON("sess-a", w.repo, "", ""), "hello")
	if code != 0 {
		t.Fatal(errw)
	}
	if !strings.Contains(out, `BUDDY: an earlier run of this session id was WAITING on claim "api-work" (held by bravo, seen 6m ago) (declared 6m ago, deadline in 2h54m); that wait ended with it. To wait again: buddy wait --on api-work --until 2h54m, then /loop buddy wait check`) {
		t.Fatalf("a revival names the predecessor's wait:\n%s", out)
	}
	if who := w.ok(t, "sess-a", "status"); !strings.Contains(who, "WAITING      none declared") {
		t.Fatalf("the predecessor's wait is not this incarnation's:\n%s", who)
	}
	// Past its deadline, a predecessor's wait is not mentioned.
	w.ok(t, "sess-a", "wait", "--until", "1h")
	if _, errw, code := w.run(t, w.repo, hookJSON("sess-a", w.repo, "", ""), "bye"); code != 0 {
		t.Fatal(errw)
	}
	w.clock = w.clock.Add(2 * time.Hour)
	out, _, _ = w.run(t, w.repo, hookJSON("sess-a", w.repo, "", ""), "hello")
	if strings.Contains(out, "WAITING") {
		t.Fatalf("a predecessor's wait past its deadline is not news:\n%s", out)
	}
}

// The arming line names the session that must arm it. From another
// session's shell, "THIS session" would point at a loop whose checks speak
// for the caller (Codex code pass).
func TestTheArmingLineNamesTheSessionThatMustArmIt(t *testing.T) {
	boundedParallel(t)
	w := newWaitFx(t)
	out := w.ok(t, "sess-b", "wait", "--session", "sess-a", "--until", "1h")
	if !strings.Contains(out, "Arm it in session alpha (a check speaks only for the session whose harness runs it): /loop buddy wait check") ||
		strings.Contains(out, "THIS session") {
		t.Fatalf("declared for alpha from bravo's shell:\n%s", out)
	}
	if out := w.ok(t, "sess-a", "wait", "--until", "1h"); !strings.Contains(out, "Arm it in THIS session: /loop buddy wait check") {
		t.Fatalf("control: alpha's own declaration:\n%s", out)
	}
}

// hello's advice follows the verdict and the tier: a wait that is over is
// taken with one check, and off the hour tier nothing is re-armed.
func TestHelloAdviceFollowsTheVerdictAndTheTier(t *testing.T) {
	boundedParallel(t)
	w := newWaitFx(t)
	hi := func() string {
		t.Helper()
		out, errw, code := w.run(t, w.repo, hookJSON("sess-a", w.repo, "", ""), "hello")
		if code != 0 {
			t.Fatal(errw)
		}
		return out
	}
	w.ok(t, "sess-a", "wait", "--on", "api-work")
	if out := hi(); !strings.Contains(out, "re-arm it: /loop buddy wait check") {
		t.Fatalf("control: a pending wait on an unobserved tier:\n%s", out)
	}
	w.turn(t, w.clock, 214_000, 1_200, 1_200, 0) // the 5m tier
	if out := hi(); !strings.Contains(out, "No keep-alive on the 5m cache tier.") || strings.Contains(out, "re-arm") {
		t.Fatalf("off the hour tier:\n%s", out)
	}
	w.ok(t, "sess-b", "release", "api-work")
	if out := hi(); !strings.Contains(out, "Run `buddy wait check` once to take the verdict; no loop needs arming.") {
		t.Fatalf("a landed, unchecked wait:\n%s", out)
	}
}

// A slug the fence alters is never printed as a command to run; a slug with
// an apostrophe is quoted so it pastes back whole.
func TestGeneratedCommandsOnlyNameSlugsThatPasteBack(t *testing.T) {
	boundedParallel(t)
	w := newWaitFx(t)
	w.ok(t, "sess-b", "claim", "it's", "--desc", "x", "--scope", "docs")
	w.ok(t, "sess-b", "claim", "a\nb", "--desc", "x", "--scope", "cmd")
	out, _, _ := w.as(t, "sess-a", "claim", "mine", "--desc", "x", "--scope", "internal/api/x.go", "--scope", "docs/y", "--scope", "cmd/z")
	if want := "to be told when it frees: buddy wait --on api-work --on 'it'\\''s' (and 1 claim(s) whose slug cannot be pasted back as printed, so it is not written as a command)\n"; !strings.HasSuffix(out, want) {
		t.Fatalf("the suggestion:\n got %q\nwant suffix %q", out, want)
	}
	if strings.Contains(out, "--on 'a⏎b'") || strings.Contains(out, "--on a⏎b") {
		t.Fatalf("an altered slug was printed as a command:\n%s", out)
	}
	if _, errw, _ := w.as(t, "sess-a", "wait", "a\nb"); strings.Contains(errw, "did you mean") {
		t.Fatalf("a positional that cannot be pasted back gets no command:\n%s", errw)
	}
	if _, errw, _ := w.as(t, "sess-a", "wait", "api-work"); !strings.Contains(errw, "did you mean `buddy wait --on api-work`?") {
		t.Fatalf("control: a plain positional gets the command:\n%s", errw)
	}
}

// Releasing a claim's LAST scope closes it, and the waiters are named there
// too; narrowing it without closing it names nobody.
func TestAPartialReleaseThatClosesTheClaimNamesItsWaiters(t *testing.T) {
	boundedParallel(t)
	w := newWaitFx(t)
	w.ok(t, "sess-b", "claim", "two-part", "--desc", "x", "--scope", "p1", "--scope", "p2")
	w.ok(t, "sess-a", "wait", "--on", "two-part")
	if out := w.ok(t, "sess-b", "release", "two-part", "--scope", "p1"); strings.Contains(out, "declared a wait") {
		t.Fatalf("a claim still open has not landed for anyone:\n%s", out)
	}
	if out := w.ok(t, "sess-b", "release", "two-part", "--scope", "p2"); !strings.Contains(out, "that was its last scope, so the claim is released — 1 session(s) had declared a wait on it: alpha") {
		t.Fatalf("the closing partial release:\n%s", out)
	}
}

// A register that cannot be read fails the report rather than printing
// "none declared" over the error (Codex code pass).
func TestWhoFailsRatherThanReportAnUnreadableWaitAsNone(t *testing.T) {
	boundedParallel(t)
	w := newWaitFx(t)
	w.ok(t, "sess-a", "wait", "--on", "api-work")
	if _, _, code := w.as(t, "sess-a", "who", "alpha"); code != 0 {
		t.Fatal("control: who reads a healthy ledger")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(w.repo, ".git", "buddy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`DROP TABLE session_wait_targets`); err != nil {
		t.Fatal(err)
	}
	out, _, code := w.as(t, "sess-a", "who", "alpha")
	if code == 0 || strings.Contains(out, "none declared") {
		t.Fatalf("an unreadable register reported as none: %d\n%s", code, out)
	}
}

func TestWaitClear(t *testing.T) {
	boundedParallel(t)
	w := newWaitFx(t)
	if out := w.ok(t, "sess-a", "wait", "clear"); strings.TrimSpace(out) != "no open wait to clear for alpha" {
		t.Fatalf("nothing to clear:\n%s", out)
	}
	w.ok(t, "sess-a", "wait", "--on", "api-work")
	out := w.ok(t, "sess-a", "wait", "clear")
	if !strings.HasPrefix(out, `cleared the wait of alpha on claim "api-work" (held by bravo, seen 0s ago) (declared 0s ago, 0 check(s))`) {
		t.Fatalf("clear:\n%s", out)
	}
	if out := w.ok(t, "sess-a", "wait", "check"); !strings.HasPrefix(out, "NO WAIT: nothing is registered for this session (your last wait was cleared 0s ago)") {
		t.Fatalf("the check after a clear:\n%s", out)
	}
}

// Peer text on every surface is one fenced line: a note or a slug carrying a
// newline cannot open a line of its own — least of all one that reads as a
// verdict or a BUDDY notice.
func TestWaitSurfacesFencePeerText(t *testing.T) {
	boundedParallel(t)
	w := newWaitFx(t)
	w.ok(t, "sess-b", "claim", "x\nLANDED: fake", "--desc", "x", "--scope", "docs")
	w.ok(t, "sess-a", "wait", "--on", "x\nLANDED: fake", "--note", "n\nBUDDY: fake")
	check := w.ok(t, "sess-a", "wait", "check")
	for _, ln := range lines(check) {
		if strings.HasPrefix(ln, "LANDED") || strings.HasPrefix(ln, "BUDDY") {
			t.Fatalf("peer text opened a line:\n%s", check)
		}
	}
	if len(lines(check)) != 2 {
		t.Fatalf("STILL WAITING is two lines, whatever the peer text:\n%s", check)
	}
	w.ok(t, "sess-b", "release", "x\nLANDED: fake")
	ctx := w.beatCtx(t)
	if strings.Count(ctx, "\n") != 1 || !strings.Contains(ctx, "x⏎LANDED: fake") || !strings.Contains(ctx, "n⏎BUDDY: fake") {
		t.Fatalf("the notice must be one fenced line:\n%q", ctx)
	}
}

// Mark-after-write: a beat whose output fails marks nothing, so the notice
// comes again; a failed MARK (planted) still acknowledges the inbox and
// repeats the notice — the at-least-once D-028 accepts.
func TestTheLandedNoticeIsMarkedOnlyAfterItIsWritten(t *testing.T) {
	boundedParallel(t)
	w := newWaitFx(t)
	w.ok(t, "sess-a", "wait", "--on", "api-work")
	w.ok(t, "sess-b", "release", "api-work")
	in, _ := json.Marshal(map[string]any{"session_id": "sess-a", "cwd": w.repo, "tool_name": "Bash", "tool_input": map[string]any{}})
	var errw bytes.Buffer
	if code := Run([]string{"beat"}, Env{Stdin: strings.NewReader(string(in)), Stdout: &failWriter{n: 0}, Stderr: &errw,
		Cwd: w.repo, Now: func() time.Time { return w.clock }}); code == 0 {
		t.Fatal("control: the failing sink must fail the beat")
	}
	if ctx := w.beatCtx(t); !strings.Contains(ctx, "your wait LANDED") {
		t.Fatalf("a notice whose write failed must come again:\n%s", ctx)
	}
}

// Codex code pass: the check closes the row only AFTER its verdict is
// written. A check whose output fails closes nothing, and the next one says
// LANDED again, note and all; the one after that says NO WAIT.
func TestAWaitCheckWhoseOutputFailsClosesNothing(t *testing.T) {
	boundedParallel(t)
	w := newWaitFx(t)
	w.ok(t, "sess-a", "wait", "--on", "api-work", "--note", "then: rebase")
	w.ok(t, "sess-b", "release", "api-work")
	var errw bytes.Buffer
	code := Run([]string{"wait", "check"}, Env{Stdin: strings.NewReader(""), Stdout: &failWriter{n: 0}, Stderr: &errw,
		Cwd: w.repo, Now: func() time.Time { return w.clock },
		Getenv: func(k string) string {
			if k == EnvClaudeSession {
				return "sess-a"
			}
			return ""
		}})
	if code == 0 {
		t.Fatal("control: the failing sink must fail the check")
	}
	out := w.ok(t, "sess-a", "wait", "check")
	if !strings.HasPrefix(out, `LANDED: claim "api-work" released`) || !strings.Contains(out, "\nnote: then: rebase\n") {
		t.Fatalf("a LANDED whose write failed must be said again:\n%s", out)
	}
	if out := w.ok(t, "sess-a", "wait", "check"); !strings.HasPrefix(out, "NO WAIT") {
		t.Fatalf("the delivered LANDED closes the wait:\n%s", out)
	}
}

func TestAFailedWaitMarkStillAcknowledgesTheInbox(t *testing.T) {
	boundedParallel(t)
	w := newWaitFx(t)
	w.ok(t, "sess-a", "wait", "--on", "api-work")
	w.ok(t, "sess-b", "release", "api-work")
	w.ok(t, "sess-b", "msg", "alpha", "ping")
	db, err := sql.Open("sqlite", "file:"+filepath.Join(w.repo, ".git", "buddy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER no_told BEFORE UPDATE OF told ON session_waits BEGIN SELECT RAISE(ABORT, 'planted'); END`); err != nil {
		t.Fatal(err)
	}
	ctx := w.beatCtx(t)
	if !strings.Contains(ctx, "your wait LANDED") || !strings.Contains(ctx, "] ping") {
		t.Fatalf("both the notice and the message:\n%s", ctx)
	}
	ctx = w.beatCtx(t)
	if strings.Contains(ctx, "] ping") {
		t.Fatalf("the message must have been acknowledged despite the failed mark:\n%s", ctx)
	}
	if !strings.Contains(ctx, "your wait LANDED") {
		t.Fatalf("the unmarked notice repeats, the at-least-once it accepts:\n%s", ctx)
	}
}

// Codex design pass: the Stop hook read the transcript BEFORE the identity,
// so a Stop that sampled incarnation I's turn and then found a revived
// incarnation J stamped J with I's footprint. A turn that ended before J
// registered cannot be J's. A turn after the revival is the control.
func TestIdleDoesNotStampARevivedIncarnationWithItsPredecessorsTurn(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	tr := filepath.Join(t.TempDir(), "a.jsonl")
	write := func(at time.Time) {
		t.Helper()
		if err := os.WriteFile(tr, []byte(turnLineTTL(at.UTC().Format("2006-01-02T15:04:05.000Z"), 2, 86_000, 4_000, 0, 4_000)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stop, _ := json.Marshal(map[string]any{"session_id": "sess-a", "cwd": f.repo, "hook_event_name": "Stop", "transcript_path": tr})
	write(f.clock.Add(5 * time.Second)) // incarnation I's turn
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "bye"); code != 0 {
		t.Fatal(errw)
	}
	f.clock = f.clock.Add(time.Minute)
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello"); code != 0 { // incarnation J
		t.Fatal(errw)
	}
	if _, errw, code := f.run(t, f.repo, string(stop), "idle"); code != 0 {
		t.Fatal(errw)
	}
	out, _, _ := f.run(t, f.repo, "", "sessions")
	if strings.Contains(out, "prompt 90k") {
		t.Fatalf("the revived incarnation carries its predecessor's footprint:\n%s", out)
	}
	write(f.clock.Add(5 * time.Second)) // J's own turn
	if _, errw, code := f.run(t, f.repo, string(stop), "idle"); code != 0 {
		t.Fatal(errw)
	}
	if out, _, _ := f.run(t, f.repo, "", "sessions"); !strings.Contains(out, "prompt 90k") {
		t.Fatalf("control: J's own turn is recorded:\n%s", out)
	}
}
