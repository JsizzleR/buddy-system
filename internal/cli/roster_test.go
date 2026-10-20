package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// D-091: the default roster folds ended rows older than 24h and live rows
// whose every registered process is GONE into one line naming --all, and
// changes nothing in the ledger.
func TestSessionsFoldsDeadRowsIntoOneLine(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	hello := func(id, label string) {
		t.Helper()
		if _, errw, code := f.run(t, f.repo, hookJSON(id, f.repo, "", ""), "hello", "--label", label); code != 0 {
			t.Fatalf("hello %s: %s", id, errw)
		}
	}
	bye := func(id string) {
		t.Helper()
		if _, errw, code := f.run(t, f.repo, hookJSON(id, f.repo, "", ""), "bye"); code != 0 {
			t.Fatalf("bye %s: %s", id, errw)
		}
	}
	hello("sess-old", "ended-old")
	bye("sess-old")
	f.clock = f.clock.Add(25 * time.Hour)
	hello("sess-new", "ended-new")
	bye("sess-new")
	f.asProcess(301, 1, func() { hello("sess-up", "live-up") })
	f.asProcess(302, 1, func() { hello("sess-gone", "live-gone") })
	f.kill(302)
	hello("sess-unbound", "live-unbound") // no registered process: "cannot say", never GONE

	out, errw, code := f.run(t, f.repo, "", "sessions")
	if code != 0 {
		t.Fatalf("sessions: %s", errw)
	}
	for _, want := range []string{"ended-new", "live-up", "live-unbound"} {
		if !strings.Contains(out, want) {
			t.Fatalf("default listing dropped %s:\n%s", want, out)
		}
	}
	for _, folded := range []string{"ended-old", "live-gone"} {
		if strings.Contains(out, folded) {
			t.Fatalf("default listing printed %s:\n%s", folded, out)
		}
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if last := lines[len(lines)-1]; last != "(+ 1 ended more than 24h ago, 1 live with every registered process GONE — not listed: buddy sessions --all)" {
		t.Fatalf("the fold line is the last line and counts both: %q", last)
	}

	all, _, _ := f.run(t, f.repo, "", "sessions", "--all")
	for _, want := range []string{"ended-old", "ended-new", "live-up", "live-gone", "live-unbound"} {
		if !strings.Contains(all, want) {
			t.Fatalf("--all dropped %s:\n%s", want, all)
		}
	}
	if strings.Contains(all, "(+ ") {
		t.Fatalf("--all folds nothing, so it says nothing was folded:\n%s", all)
	}

	// The caller's own row stays, whatever it is: "which row is mine".
	mine, _, _ := f.run(t, f.repo, "", "sessions", "--session", "sess-gone")
	if !strings.Contains(mine, "* live-gone") || !strings.Contains(mine, "(+ 1 ended more than 24h ago — not listed") {
		t.Fatalf("the caller's GONE row must stay, and only the other row fold:\n%s", mine)
	}

	// An ENDED caller named by --session stays too: whoAmI refuses an ended
	// session, and the fold must not hide the row it was asked about (Codex
	// code pass).
	if out, _, _ := f.run(t, f.repo, "", "sessions", "--session", "sess-old"); !strings.Contains(out, "ended-old") {
		t.Fatalf("the named ended caller was folded:\n%s", out)
	}

	// Nothing ended in the ledger (D-025): the GONE row is still live.
	w, _, code := f.run(t, f.repo, "", "who", "live-gone")
	if code != 0 || !strings.Contains(w, "live") || strings.Contains(w, "ended") {
		t.Fatalf("a folded row is untouched in the ledger:\n%s", w)
	}

	// Control: with nothing to fold there is no fold line.
	g := newFixture(t)
	if _, errw, code := g.run(t, g.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := g.run(t, g.repo, hookJSON("s1", g.repo, "", ""), "hello", "--label", "one"); code != 0 {
		t.Fatal(errw)
	}
	if out, _, _ := g.run(t, g.repo, "", "sessions"); strings.Contains(out, "(+ ") || !strings.Contains(out, "one") {
		t.Fatalf("nothing folded, nothing said:\n%s", out)
	}
}

// One of two registered processes gone is a session still there (a second
// --resume on one id): it is listed.
func TestFoldRosterNeedsEveryProcessGone(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	h := holders{
		env:   Env{ProcAlive: func(p store.ProcRef) bool { return p.PID == 1 }},
		procs: map[string][]store.ProcRef{"half": {{PID: 1}, {PID: 2}}, "gone": {{PID: 2}, {PID: 3}}},
	}
	rows := []store.SessionInfo{
		{SessionID: "half", LastSeen: now},
		{SessionID: "gone", LastSeen: now},
		{SessionID: "edge", LastSeen: now, Ended: now.Add(-rosterEndedWindow)}, // exactly at the window: kept
	}
	kept, line := foldRoster(h, rows, "", now)
	if len(kept) != 2 || kept[0].SessionID != "half" || kept[1].SessionID != "edge" {
		t.Fatalf("kept %v", kept)
	}
	if line != "(+ 1 live with every registered process GONE — not listed: buddy sessions --all)" {
		t.Fatalf("line %q", line)
	}
}
