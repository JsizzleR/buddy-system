package cli

import (
	"strings"
	"testing"
)

// A delayed `bye` and the live incarnation it can end (invariant 12).
//
// Invariant 12 says: "A delayed `bye` from a dead incarnation must not orphan a
// live one." `Store.Bye` has a fence for exactly that —
//
//	WHERE session_id=? AND ended IS NULL AND (incarnation=? OR ?='')
//
// — and its only caller, `cmdBye`, passes "" for the incarnation, which
// short-circuits it. It has nothing else to pass: `hookInput` carries
// session_id, cwd, tool_name and transcript_path, and NO incarnation, because
// the harness does not give a hook one.
//
// The consequence is not "a row says ended". `Beat` is `WHERE session_id=? AND
// ended IS NULL` and returns nil when it matches nothing, so the wrongly-ended
// session's own heartbeats become silent no-ops and it CANNOT clear the flag.
// Meanwhile `orphanEnded` runs inside every `Hello`, so the next peer to start
// orphans the live session's open claims while it is still editing.
func TestKnownGap_ADelayedByeEndsALiveIncarnation(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)

	// sess-a ends cleanly. This is incarnation 1.
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "bye"); code != 0 {
		t.Fatalf("bye: %s", errw)
	}
	// It is resumed under the SAME session id: `claude --resume` keeps it.
	// Hello mints incarnation 2 and clears ended.
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello", "--label", "alpha"); code != 0 {
		t.Fatalf("re-hello: %s", errw)
	}
	if _, errw, code := f.run(t, f.repo, "", "claim", "api-work", "--session", "sess-a",
		"--desc", "live work", "--scope", "internal/api"); code != 0 {
		t.Fatalf("claim: %s", errw)
	}

	// THE DELAYED BYE. Incarnation 1's SessionEnd hook fires late — the same
	// payload as the first one, because the payload cannot name an incarnation.
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "bye"); code != 0 {
		t.Fatalf("delayed bye: %s", errw)
	}

	// Incarnation 2 is alive and working. It must still be live.
	out, _, code := f.run(t, f.repo, "", "sessions")
	if code != 0 {
		t.Fatal("sessions failed")
	}
	row := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "alpha") {
			row = line
		}
	}
	if row == "" {
		t.Fatalf("no row for alpha:\n%s", out)
	}
	// THE GAP, PINNED AS IT IS — not as it should be. This asserts the WRONG
	// behaviour on purpose, so the day somebody fixes it this test fails and
	// has to be deleted deliberately, rather than a fix landing unnoticed. It
	// is named KnownGap so it can never be read as approval.
	if !strings.Contains(row, "ended") {
		t.Fatalf("the delayed-bye gap appears to be FIXED — good. Delete this test, "+
			"delete the Known-unfixed entry in docs/decisions.md, and write the "+
			"positive one (TestADelayedByeMustNotEndALiveIncarnation).\n  %s", row)
	}

	// AND IT CANNOT RECOVER. Beat is `WHERE session_id=? AND ended IS NULL`
	// and returns nil when it matches nothing, so the live session's own
	// heartbeats are silent no-ops from here on. The hook still exits 0, which
	// is why nothing anywhere reports this.
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "Edit", "internal/api/x.go"), "beat"); code != 0 {
		t.Fatalf("beat: %s", errw)
	}
	still, _, _ := f.run(t, f.repo, "", "sessions")
	for _, line := range strings.Split(still, "\n") {
		if strings.Contains(line, "alpha") && !strings.Contains(line, "ended") {
			t.Fatalf("beat cleared ended — invariant 12 forbids a beat resurrecting "+
				"an ended session, so if this now passes the fix needs review:\n  %s", line)
		}
	}

	// A PEER starting up runs orphanEnded inside Hello, which orphans the open
	// claims of every session the ledger thinks has ended — including this one,
	// which has not.
	if _, errw, code := f.run(t, f.wtB, hookJSON("sess-c", f.wtB, "", ""), "hello", "--label", "charlie"); code != 0 {
		t.Fatalf("peer hello: %s", errw)
	}
	ls, _, code := f.run(t, f.repo, "", "ls")
	if code != 0 {
		t.Fatal("ls failed")
	}
	if strings.Contains(ls, "api-work") {
		t.Fatalf("the claim SURVIVED a peer's hello — the gap may be fixed; see the "+
			"note at the top of this test before changing it.\n%s", ls)
	}
	// So: a live, working session has silently lost its scope to a peer's
	// startup, and the scope is now free for anyone else to take.
}
