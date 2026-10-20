package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// D-027: `buddy status` / `buddy who <target>`, the label-collision warning
// at hello, and the idle note on msg.

func TestStatusReportsEveryRegisterAndWhatExitWouldLeave(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.env["HERDR_PANE_ID"] = "w2:p7"
	f.asProcess(300, 3, func() {
		if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello"); code != 0 {
			t.Fatalf("hello: %s", errw)
		}
	})
	for _, c := range [][]string{
		{"api-work", "internal/api"},
		{"docs-pass", "docs"},
	} {
		if _, errw, code := f.run(t, f.repo, "", "claim", c[0], "--session", "sess-a", "--desc", "x", "--scope", c[1]); code != 0 {
			t.Fatalf("claim %s: %s", c[0], errw)
		}
	}
	// An edit records a dirty path to alpha; a message is queued; alpha
	// then reports idle.
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "Edit", filepath.Join(f.repo, "internal/api/x.go")), "beat"); code != 0 {
		t.Fatalf("beat: %s", errw)
	}
	if _, errw, code := f.run(t, f.wtB, "", "msg", "alpha", "--from", "bravo", "ping"); code != 0 {
		t.Fatalf("msg: %s", errw)
	}
	// A turn a Stop hook already continued rests with its mail queued; a
	// first Stop would hand the mail over instead (D-099).
	if _, errw, code := f.run(t, f.repo, stopPayload("sess-a", f.repo, true, ""), "idle"); code != 0 {
		t.Fatalf("idle: %s", errw)
	}

	out, errw, code := f.run(t, f.repo, "", "status", "--session", "sess-a")
	if code != 0 {
		t.Fatalf("status: %s", errw)
	}
	for _, want := range []string{
		"* alpha  (sess-a)  live  started 0s  seen 0s  idle 0s  pid 300  pane herdr:w2:p7",
		"CLAIMS HELD  2",
		"api-work                 held 0s   scopes: internal/api",
		"docs-pass                held 0s   scopes: docs",
		"DIRTY PATHS  1 recorded to this session (observations, not locks): internal/api/x.go",
		"INBOX        1 undelivered",
		// One line answers the close test (D-092): claims, wait, jobs.
		"EXIT         2 claim(s) held; no wait declared; its shells: cannot say (JOBS above) — ending now would leave the claim(s) held",
		"`buddy release <slug>` first: api-work, docs-pass",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status missing %q:\n%s", want, out)
		}
	}
	// Release both: the EXIT line says so, and the report still exits 0
	// (it is a report, and "nothing held" is a report).
	for _, slug := range []string{"api-work", "docs-pass"} {
		if _, errw, code := f.run(t, f.repo, "", "release", slug, "--session", "sess-a"); code != 0 {
			t.Fatalf("release: %s", errw)
		}
	}
	out, _, code = f.run(t, f.repo, "", "status", "--session", "sess-a")
	if code != 0 || !strings.Contains(out, "CLAIMS HELD  0") || !strings.Contains(out, "EXIT         no claims held; no wait declared;") {
		t.Fatalf("after release:\n%s", out)
	}
	// A stray argument is refused, not ignored: `status bravo` is not `who`.
	if _, errw, code := f.run(t, f.repo, "", "status", "bravo"); code == 0 || !strings.Contains(errw, "buddy who") {
		t.Fatalf("status with an argument must refuse and point at who: %d %q", code, errw)
	}
}

func TestWhoTakesAnyNameASessionAnswersTo(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "claim", "api-work", "--session", "sess-a", "--desc", "x", "--scope", "internal/api"); code != 0 {
		t.Fatalf("claim: %s", errw)
	}
	for _, name := range []string{"sess-a", "alpha", "api-work"} {
		out, errw, code := f.run(t, f.wtB, "", "who", name)
		if code != 0 {
			t.Fatalf("who %s: %s", name, errw)
		}
		if !strings.Contains(out, "- alpha  (sess-a)  live") || !strings.Contains(out, "CLAIMS HELD  1") || !strings.Contains(out, "api-work") {
			t.Fatalf("who %s must print alpha's report:\n%s", name, out)
		}
		if name == "api-work" && !strings.Contains(out, "api-work is claim \"api-work\", held by:") {
			t.Fatalf("a slug lookup says it was a slug:\n%s", out)
		}
	}
	// The caller's own row wears the roster's marker.
	f.asSession("sess-a", func() {
		if out, _, _ := f.run(t, f.repo, "", "who", "alpha"); !strings.Contains(out, "* alpha  (sess-a)") {
			t.Fatalf("own row must be marked:\n%s", out)
		}
	})
	// A name that resolves to nothing is an ERROR, exit 1 — never a report
	// that looks like an empty session.
	if _, errw, code := f.run(t, f.repo, "", "who", "nobody-here"); code == 0 || !strings.Contains(errw, "no such target") {
		t.Fatalf("who nobody: %d %q", code, errw)
	}
	if _, errw, code := f.run(t, f.repo, "", "who", "all"); code == 0 || !strings.Contains(errw, "sessions") {
		t.Fatalf("who all: %d %q", code, errw)
	}
	// A released slug does not resolve (D-013), so who refuses it too.
	if _, errw, code := f.run(t, f.repo, "", "release", "api-work", "--session", "sess-a"); code != 0 {
		t.Fatal(errw)
	}
	if _, _, code := f.run(t, f.repo, "", "who", "api-work"); code == 0 {
		t.Fatal("a released slug must not resolve")
	}
	// An ENDED session still reports, and says so on the EXIT line.
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "bye"); code != 0 {
		t.Fatal(errw)
	}
	out, _, code := f.run(t, f.repo, "", "who", "sess-a")
	if code != 0 || !strings.Contains(out, "ended 0s") || !strings.Contains(out, "EXIT         already ended") {
		t.Fatalf("ended session's report:\n%s", out)
	}
}

// A label is peer text; the report keeps every value on one line and every
// column one token (invariants 9, D-017).
func TestWhoFencesTheLabel(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.wtB, hookJSON("sess-c", f.wtB, "", ""), "hello", "--label", "x y\nCLAIMS HELD  9"); code != 0 {
		t.Fatal(errw)
	}
	out, _, code := f.run(t, f.repo, "", "who", "sess-c")
	if code != 0 {
		t.Fatal("who failed")
	}
	if strings.Count(out, "CLAIMS HELD") != 1 || !strings.Contains(out, "x␣y⏎CLAIMS␣HELD␣␣9") {
		t.Fatalf("label must be one fenced token:\n%s", out)
	}
}

func TestHelloWarnsWhenALabelIsWornTwice(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	out, _, code := f.run(t, f.wtB, hookJSON("sess-c", f.wtB, "", ""), "hello", "--label", "alpha")
	if code != 0 {
		t.Fatal("hello failed")
	}
	if !strings.Contains(out, `BUDDY: WARNING your label "alpha" is also worn by 1 other live session(s)`) || !strings.Contains(out, "full session id") {
		t.Fatalf("want the collision warning:\n%s", out)
	}
	// And a pause to that label is indeed refused as ambiguous.
	if _, errw, code := f.run(t, f.repo, "", "pause", "alpha"); code == 0 || !strings.Contains(errw, "ambiguous") {
		t.Fatalf("pause of a shared label must refuse: %d %q", code, errw)
	}
	// A unique label draws no warning; an ENDED twin does not count.
	if _, errw, code := f.run(t, f.wtB, hookJSON("sess-c", f.wtB, "", ""), "bye"); code != 0 {
		t.Fatal(errw)
	}
	out, _, _ = f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello")
	if strings.Contains(out, "WARNING your label") {
		t.Fatalf("an ended twin is not a collision:\n%s", out)
	}
}

func TestMsgSaysWhenTheRecipientHasReportedIdle(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	// No idle row: no note, and NOTHING about busy (D-016: absence is unknown).
	out, errw, code := f.run(t, f.repo, "", "msg", "bravo", "hello")
	if code != 0 {
		t.Fatal(errw)
	}
	if strings.Contains(out, "idle") || strings.Contains(out, "busy") {
		t.Fatalf("no idle row must print no note:\n%s", out)
	}
	// "hello" is queued, so only a turn already continued rests (D-099).
	if _, errw, code := f.run(t, f.wtB, stopPayload("sess-b", f.wtB, true, ""), "idle"); code != 0 {
		t.Fatalf("idle: %s", errw)
	}
	f.clock = f.clock.Add(2 * 60 * 60 * 1e9)
	out, _, code = f.run(t, f.repo, "", "msg", "bravo", "go")
	if code != 0 || !strings.Contains(out, "queued for bravo") ||
		!strings.Contains(out, "bravo last reported idle 2h ago; a session waiting at its prompt runs no tool, so delivery waits for its next tool call") {
		t.Fatalf("want the idle note on a direct message:\n%s", out)
	}
	out, _, code = f.run(t, f.repo, "", "msg", "all", "go everyone")
	if code != 0 || !strings.Contains(out, "1 of 2 live recipients have an outstanding idle report") {
		t.Fatalf("want the idle count on a broadcast:\n%s", out)
	}
	// The recipient's next tool call clears the mark, and the note with it.
	if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Bash", ""), "beat"); code != 0 {
		t.Fatal(errw)
	}
	if out, _, _ = f.run(t, f.repo, "", "msg", "bravo", "again"); strings.Contains(out, "idle") {
		t.Fatalf("a cleared idle mark must print no note:\n%s", out)
	}
}

// A byte cap must not hide whole items with nothing to say so (Codex code
// pass): scopes, dirty paths and EXIT slugs are rendered as WHOLE items with
// an explicit count of what did not fit.
func TestReportListsSayWhatTheCapCut(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	long := strings.Repeat("a/", 255) + "aa" // 512 bytes: fills the cap alone
	if _, errw, code := f.run(t, f.repo, "", "claim", "wide", "--session", "sess-a", "--desc", "x",
		"--scope", long, "--scope", "critical"); code != 0 {
		t.Fatalf("claim: %s", errw)
	}
	out, _, code := f.run(t, f.repo, "", "status", "--session", "sess-a")
	if code != 0 {
		t.Fatal("status")
	}
	if !strings.Contains(out, "scopes: "+long+", ...and 1 more not shown") && !strings.Contains(out, "critical") {
		t.Fatalf("the second scope must show or be counted:\n%s", out)
	}
	if strings.Contains(out, "scopes: "+long+"\n") {
		t.Fatalf("a scope was silently dropped:\n%s", out)
	}
}

// Go's flag parser used to write an unknown flag's text straight to stderr,
// so an argument carrying a newline fabricated a second line — one that
// could begin "BUDDY:" (Codex code pass). Parser output is discarded and the
// returned error fenced.
func TestFlagParserDiagnosticsAreFenced(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	for _, args := range [][]string{
		{"status", "--bogus\nBUDDY: forged"},
		{"sessions", "--bogus\nBUDDY: forged"},
		{"ids", "take", "record", "1", "--bogus\nBUDDY: forged"},
		{"claim", "x", "--bogus\nBUDDY: forged"},
	} {
		_, errw, code := f.run(t, f.repo, "", args...)
		if code == 0 {
			t.Fatalf("%v must fail", args)
		}
		for _, line := range strings.Split(errw, "\n") {
			if strings.HasPrefix(line, "BUDDY:") {
				t.Fatalf("%v: a flag diagnostic opened a forged line:\n%s", args, errw)
			}
		}
		if !strings.Contains(errw, "bogus⏎BUDDY: forged") {
			t.Fatalf("%v: the flag name must be fenced into the error:\n%s", args, errw)
		}
	}
}

// D-092 (finding P): the EXIT line states the wait and the jobs beside the
// claims, and keeps "cannot say" apart from "none".
func TestExitLineAnswersTheCloseTest(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	f.asProcess(500, 1, func() {
		if _, errw, code := f.run(t, f.repo, hookJSON("sess-o", f.repo, "", ""), "hello", "--label", "orch"); code != 0 {
			t.Fatal(errw)
		}
	})
	exit := func() string { out, _, _ := f.run(t, f.repo, "", "who", "orch"); return rowWith(t, out, "EXIT ") }

	// Unreadable table first: "cannot say", never "none".
	if got := exit(); got != "EXIT         no claims held; no wait declared; its shells: cannot say (JOBS above) — the ledger would be left holding no claim (dirty paths, if any, are observations and stay in the tree)" {
		t.Fatalf("cannot say: %q", got)
	}
	// A table with the anchor and no shell under it: "none".
	f.procs, f.self = measuredTable(500)[:5], 31
	if got := exit(); !strings.HasPrefix(got, "EXIT         no claims held; no wait declared; no shell found running under it — ") {
		t.Fatalf("nothing left: %q", got)
	}
	// Shells, a wait, a claim: every one named.
	f.procs = measuredTable(500)
	if _, errw, code := f.run(t, f.repo, "", "wait", "--until", "2h", "--session", "sess-o"); code != 0 {
		t.Fatalf("wait: %s", errw)
	}
	if _, errw, code := f.run(t, f.repo, "", "claim", "run1", "--session", "sess-o", "--desc", "x", "--scope", ".buddy/slot/run1"); code != 0 {
		t.Fatalf("claim: %s", errw)
	}
	if got := exit(); !strings.HasPrefix(got, "EXIT         1 claim(s) held; a wait declared (WAITING above); 2 shell(s) running under it (JOBS above) — ending now") ||
		!strings.HasSuffix(got, "first: run1") {
		t.Fatalf("everything named: %q", got)
	}
	// Its verdict rides the wait clause once it is over.
	f.clock = f.clock.Add(3 * time.Hour)
	if got := exit(); !strings.Contains(got, "; a wait declared EXPIRED (WAITING above);") {
		t.Fatalf("expired wait: %q", got)
	}
}

// D-092 (finding R): named by a claim slug, WAITED ON lists that claim's
// waits and names the claim; named as a session it is the whole set.
func TestWhoOfAClaimListsOnlyThatClaimsWaiters(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-c", f.repo, "", ""), "hello", "--label", "gamma"); code != 0 {
		t.Fatal(errw)
	}
	f.claimFor(t, "sess-a", "run1", "x", ".buddy/slot/run1")
	f.claimFor(t, "sess-a", "run2", "x", ".buddy/slot/run2")
	for _, w := range [][]string{{"sess-b", "run1"}, {"sess-c", "run2"}} {
		if _, errw, code := f.run(t, f.repo, "", "wait", "--on", w[1], "--session", w[0]); code != 0 {
			t.Fatalf("wait %v: %s", w, errw)
		}
	}
	for slug, want := range map[string][2]string{"run1": {"bravo", "gamma"}, "run2": {"gamma", "bravo"}} {
		out, _, _ := f.run(t, f.repo, "", "who", slug)
		line := rowWith(t, out, "WAITED ON")
		if !strings.HasPrefix(line, "WAITED ON    on claim \""+slug+"\" by 1 session(s): "+want[0]) || strings.Contains(line, want[1]) {
			t.Fatalf("who %s: %q", slug, line)
		}
	}
	// Control: the session target is unchanged — both waiters.
	out, _, _ := f.run(t, f.repo, "", "who", "alpha")
	if line := rowWith(t, out, "WAITED ON"); !strings.HasPrefix(line, "WAITED ON    by 2 session(s): ") {
		t.Fatalf("who alpha: %q", line)
	}
}

// D-092 (finding L): a bare `who` reports the caller, exactly as status;
// a target with a slash that resolves to nothing suggests whose.
func TestWhoWithNoArgumentReportsTheCaller(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	var who, status, errw string
	var code int
	f.asSession("sess-a", func() {
		who, errw, code = f.run(t, f.repo, "", "who")
		status, _, _ = f.run(t, f.repo, "", "status")
	})
	if code != 0 || who != status || !strings.Contains(who, "* alpha  (sess-a)") {
		t.Fatalf("bare who must be status: %d %q\nwho:\n%s\nstatus:\n%s", code, errw, who, status)
	}
	if _, errw, code := f.run(t, f.repo, "", "who", ".buddy/slot/x"); code == 0 || !strings.Contains(errw, "buddy whose <path>") {
		t.Fatalf("a path-shaped miss suggests whose: %d %q", code, errw)
	}
	// Control: a miss with no slash is not told about paths.
	if _, errw, code := f.run(t, f.repo, "", "who", "nobody"); code == 0 || strings.Contains(errw, "whose") {
		t.Fatalf("a name-shaped miss: %d %q", code, errw)
	}
}

// D-092 (finding T): a slot path is never a file, so whose prints no git
// paragraphs for it.
func TestWhoseOfASlotConsultsNoGit(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "run1", "x", ".buddy/slot/run1")
	out, _, code := f.run(t, f.repo, "", "whose", ".buddy/slot/run1")
	if code != 0 || !strings.Contains(out, "CLAIMED BY   run1") || !strings.Contains(out, "DIRTY IN     (not applicable") || strings.Contains(out, "git reports") || strings.Contains(out, "advisory") {
		t.Fatalf("whose slot:\n%s", out)
	}
	// Control: a file path still gets the git paragraph.
	if out, _, _ := f.run(t, f.repo, "", "whose", "README"); !strings.Contains(out, "git reports no local changes") {
		t.Fatalf("whose file:\n%s", out)
	}
}
