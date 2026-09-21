package cli

import (
	"path/filepath"
	"strings"
	"testing"
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
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "idle"); code != 0 {
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
		"EXIT         ending now would leave 2 claim(s) held",
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
	if code != 0 || !strings.Contains(out, "CLAIMS HELD  0") || !strings.Contains(out, "EXIT         no claims held") {
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
	if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "", ""), "idle"); code != 0 {
		t.Fatalf("idle: %s", errw)
	}
	f.clock = f.clock.Add(2 * 60 * 60 * 1e9)
	out, _, code = f.run(t, f.repo, "", "msg", "bravo", "go")
	if code != 0 || !strings.Contains(out, "queued for bravo") ||
		!strings.Contains(out, "bravo last reported idle 2h ago; a session waiting at its prompt runs no tool, so delivery waits for its next tool call") {
		t.Fatalf("want the idle note on a direct message:\n%s", out)
	}
	out, _, code = f.run(t, f.repo, "", "msg", "all", "go everyone")
	if code != 0 || !strings.Contains(out, "1 of 2 recipients have an outstanding idle report") {
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
