package cli

import (
	"strings"
	"testing"
)

// D-089: BUDDY_LABEL names the session at hello when no --label is given, so a
// launcher's tab env (`herdr tab create --env BUDDY_LABEL=<lane>`) makes
// `buddy msg <lane>` resolve. The flag wins; the worn-twice warning fires.
func TestHelloTakesItsLabelFromBuddyLabel(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t) // sess-a "alpha", sess-b "bravo"

	f.env[EnvLabel] = "lane-misc"
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-c", f.repo, "", ""), "hello"); code != 0 {
		t.Fatal(errw)
	}
	out, errw, code := f.run(t, f.repo, "", "msg", "lane-misc", "hello")
	if code != 0 || !strings.Contains(out, "queued for lane-misc") {
		t.Fatalf("msg by the launcher's name must resolve: %d %q %q", code, out, errw)
	}

	// The flag wins over the environment.
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-d", f.repo, "", ""), "hello", "--label", "flagged"); code != 0 {
		t.Fatal(errw)
	}
	if out, _, _ := f.run(t, f.repo, "", "who", "sess-d"); !strings.Contains(out, "flagged") || strings.Contains(out, "lane-misc") {
		t.Fatalf("--label must win over %s: %q", EnvLabel, out)
	}

	// Worn twice: the same warning a hand-chosen --label gets.
	f.env[EnvLabel] = "alpha"
	out, errw, code = f.run(t, f.repo, hookJSON("sess-e", f.repo, "", ""), "hello")
	if code != 0 || !strings.Contains(out, `your label "alpha" is also worn by 1 other live session(s)`) {
		t.Fatalf("a label from the environment worn twice must warn: %d %q %q", code, out, errw)
	}

	// The positive control: unset, the default label still applies.
	delete(f.env, EnvLabel)
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-f", f.repo, "", ""), "hello"); code != 0 {
		t.Fatal(errw)
	}
	if out, _, _ := f.run(t, f.repo, "", "who", "sess-f"); !strings.Contains(out, "/s-") {
		t.Fatalf("no %s: the default label: %q", EnvLabel, out)
	}
}
