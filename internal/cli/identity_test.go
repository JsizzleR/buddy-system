package cli

import (
	"strings"
	"testing"
	"time"
)

// The tests in this file cover issue #2: agent verbs inferred the caller from
// the working directory, so sessions sharing a checkout took each other's
// identity. Each one names the symptom it pins.

// helloIn registers a session whose worktree is dir, the way the SessionStart
// hook does.
func (f *fixture) helloIn(t *testing.T, dir, id, label string) {
	t.Helper()
	if _, errw, code := f.run(t, dir, hookJSON(id, dir, "", ""), "hello", "--label", label); code != 0 {
		t.Fatalf("hello %s: %s", id, errw)
	}
}

// ownerOf returns the label `ls` attributes the slug to.
func (f *fixture) ownerOf(t *testing.T, slug string) string {
	t.Helper()
	out, _, _ := f.run(t, f.repo, "", "ls")
	for _, line := range strings.Split(out, "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == slug {
			return fields[1]
		}
	}
	return ""
}

// Symptom 1: a claim was filed under the wrong session, and locked its real
// owner out. Two sessions share one checkout; the one that beat MOST RECENTLY
// is not the one calling.
func TestClaimAttributesToTheCallerNotTheNewestHeartbeat(t *testing.T) {
	f := newFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	f.helloIn(t, f.repo, "sess-a", "alpha")
	f.helloIn(t, f.repo, "sess-b", "bravo")
	// bravo pings last, so directory inference resolves to bravo for everyone.
	f.clock = f.clock.Add(time.Minute)
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-b", f.repo, "Edit", ""), "beat"); code != 0 {
		t.Fatal(errw)
	}

	// alpha claims. It is alpha's claim, whoever pinged last.
	f.asSession("sess-a", func() {
		if out, errw, code := f.run(t, f.repo, "", "claim", "a-work", "--desc", "d", "--scope", "pkg/a"); code != 0 {
			t.Fatalf("claim: %s %s", out, errw)
		}
	})
	if got := f.ownerOf(t, "a-work"); got != "alpha" {
		t.Fatalf("claim must be recorded against its caller; ls says owner is %q, want alpha", got)
	}

	// The inversion the issue describes: alpha must be able to release its own
	// claim, and bravo must NOT be able to release it.
	f.asSession("sess-b", func() {
		if _, _, code := f.run(t, f.repo, "", "release", "a-work"); code == 0 {
			t.Fatal("bravo must not release alpha's claim")
		}
	})
	f.asSession("sess-a", func() {
		if _, errw, code := f.run(t, f.repo, "", "release", "a-work"); code != 0 {
			t.Fatalf("alpha must be able to release its own claim: %s", errw)
		}
	})
}

// Symptom 3: a session whose worktree is the main checkout could neither claim
// nor release from a linked worktree of the same repo — which collides with the
// documented practice of giving each session its own worktree. The ledger is
// shared (git common dir), so identity is all that was missing.
func TestClaimWorksFromAnotherWorktreeOfTheSameRepo(t *testing.T) {
	f := newFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	f.helloIn(t, f.repo, "sess-a", "alpha")

	f.asSession("sess-a", func() {
		if out, errw, code := f.run(t, f.wtB, "", "claim", "a-work", "--desc", "d", "--scope", "pkg/a"); code != 0 {
			t.Fatalf("claim from a sibling worktree: %s %s", out, errw)
		}
		if got := f.ownerOf(t, "a-work"); got != "alpha" {
			t.Fatalf("owner %q, want alpha", got)
		}
		if _, errw, code := f.run(t, f.wtB, "", "release", "a-work"); code != 0 {
			t.Fatalf("release from a sibling worktree: %s", errw)
		}
	})
}

// With nobody to ask, an ambiguous directory is REFUSED and the candidates are
// named. Fail-closed beats a coin flip: the wrong answer silently breaks the
// one invariant the tool exists to enforce.
func TestAmbiguousWorktreeIsRefusedAndNamesCandidates(t *testing.T) {
	f := newFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	f.helloIn(t, f.repo, "sess-a", "alpha")
	f.helloIn(t, f.repo, "sess-b", "bravo")

	_, errw, code := f.run(t, f.repo, "", "claim", "who", "--desc", "d", "--scope", "pkg")
	if code == 0 {
		t.Fatal("an ambiguous worktree must refuse, not pick the newest heartbeat")
	}
	for _, want := range []string{"alpha", "bravo", "sess-a", "sess-b", "--session"} {
		if !strings.Contains(errw, want) {
			t.Fatalf("refusal must name the candidates and the remedy (missing %q): %s", want, errw)
		}
	}
	if out, _, _ := f.run(t, f.repo, "", "ls"); !strings.Contains(out, "no claims") {
		t.Fatalf("a refused claim must not land: %q", out)
	}
	// A single live session in the directory still resolves without ceremony.
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-b", f.repo, "", ""), "bye"); code != 0 {
		t.Fatal(errw)
	}
	if out, errw, code := f.run(t, f.repo, "", "claim", "who", "--desc", "d", "--scope", "pkg"); code != 0 {
		t.Fatalf("one live session must still resolve from the directory: %s %s", out, errw)
	}
}

// Precedence: an explicit --session outranks the environment, and BUDDY_SESSION
// outranks the harness's id.
func TestIdentityPrecedence(t *testing.T) {
	f := newFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	f.helloIn(t, f.repo, "sess-a", "alpha")
	f.helloIn(t, f.repo, "sess-b", "bravo")
	f.helloIn(t, f.repo, "sess-c", "charlie")

	f.env[EnvClaudeSession] = "sess-a"
	f.env[EnvSession] = "sess-b"
	if _, errw, code := f.run(t, f.repo, "", "claim", "w1", "--desc", "d", "--scope", "pkg/1"); code != 0 {
		t.Fatal(errw)
	}
	if got := f.ownerOf(t, "w1"); got != "bravo" {
		t.Fatalf("%s must outrank %s: owner %q, want bravo", EnvSession, EnvClaudeSession, got)
	}
	if _, errw, code := f.run(t, f.repo, "", "claim", "w2", "--session", "sess-c", "--desc", "d", "--scope", "pkg/2"); code != 0 {
		t.Fatal(errw)
	}
	if got := f.ownerOf(t, "w2"); got != "charlie" {
		t.Fatalf("--session must outrank the environment: owner %q, want charlie", got)
	}
}

// An asserted identity that is not registered here is an ERROR naming the
// remedy — never a silent fall back to the directory. Falling back would
// reintroduce the coin flip in the one case we were told the answer.
func TestUnregisteredAssertedIdentityIsRefusedNotGuessed(t *testing.T) {
	f := newFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	f.helloIn(t, f.repo, "sess-a", "alpha") // the ONLY live session: cwd is unambiguous

	f.asSession("sess-elsewhere", func() {
		_, errw, code := f.run(t, f.repo, "", "claim", "w", "--desc", "d", "--scope", "pkg")
		if code == 0 {
			t.Fatal("an unregistered asserted identity must not fall back to the directory")
		}
		if !strings.Contains(errw, "sess-elsewhere") || !strings.Contains(errw, "hello") {
			t.Fatalf("refusal must name the id and the remedy: %s", errw)
		}
	})
	if out, _, _ := f.run(t, f.repo, "", "ls"); !strings.Contains(out, "no claims") {
		t.Fatalf("nothing may be attributed to alpha: %q", out)
	}
}

// A session that has said bye is not live, so its id names nobody rather than
// resurrecting a claim under a dead session.
func TestEndedSessionIsNotAnIdentity(t *testing.T) {
	f := newFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	f.helloIn(t, f.repo, "sess-a", "alpha")
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "bye"); code != 0 {
		t.Fatal(errw)
	}
	f.asSession("sess-a", func() {
		if _, _, code := f.run(t, f.repo, "", "claim", "w", "--desc", "d", "--scope", "pkg"); code == 0 {
			t.Fatal("an ended session must not be able to claim")
		}
	})
}

// A stale exported BUDDY_SESSION outliving the session that set it must NOT be
// told to `buddy hello` itself back to life: that would resurrect the dead
// identity and file the caller's work under it. When a lower-precedence source
// names a live session, the refusal must point there instead. (Codex finding:
// the first draft named a remedy that was actively harmful in this case.)
func TestStaleOverrideIsNotToldToResurrectItself(t *testing.T) {
	f := newFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	f.helloIn(t, f.repo, "sess-a", "alpha")
	f.helloIn(t, f.repo, "sess-b", "bravo")
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "bye"); code != 0 {
		t.Fatal(errw)
	}

	f.env[EnvSession] = "sess-a"       // stale export, left over from alpha
	f.env[EnvClaudeSession] = "sess-b" // the shell we are ACTUALLY in
	_, errw, code := f.run(t, f.repo, "", "claim", "w", "--desc", "d", "--scope", "pkg")
	if code == 0 {
		t.Fatal("a stale override must not silently claim as the dead session")
	}
	if strings.Contains(errw, "hello --session sess-a") {
		t.Fatalf("must not recommend resurrecting the ended session: %s", errw)
	}
	for _, want := range []string{"has ended", "bravo", EnvSession} {
		if !strings.Contains(errw, want) {
			t.Fatalf("refusal must name the live alternative and what to clear (missing %q): %s", want, errw)
		}
	}
	// An id this ledger has never seen may equally be a typo — where `buddy
	// hello` would MINT a session that never existed — so that arm offers hello
	// conditionally and names clearing the source as the other half.
	delete(f.env, EnvClaudeSession)
	f.env[EnvSession] = "sess-typo"
	_, errw, _ = f.run(t, f.repo, "", "claim", "w", "--desc", "d", "--scope", "pkg")
	for _, want := range []string{"hello --session sess-typo", "otherwise", "clear $" + EnvSession} {
		if !strings.Contains(errw, want) {
			t.Fatalf("an unknown id must offer hello CONDITIONALLY (missing %q): %s", want, errw)
		}
	}
	// The remedy is worded for the source it names: a flag is dropped, not cleared.
	delete(f.env, EnvSession)
	_, errw, _ = f.run(t, f.repo, "", "claim", "w", "--session", "sess-typo", "--desc", "d", "--scope", "pkg")
	if !strings.Contains(errw, "re-run without --session") {
		t.Fatalf("a bad --session must be told to drop the flag, not to clear an env var: %s", errw)
	}
}

// An identity taken from the directory is a GUESS, and the defect being fixed
// was not merely that buddy guessed but that it guessed SILENTLY — the first
// symptom was a peer refused by the gate with no idea why. So the one path that
// can still name the wrong session must announce itself.
func TestInferredIdentityAnnouncesItself(t *testing.T) {
	f := newFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	f.helloIn(t, f.repo, "sess-a", "alpha")

	out, errw, code := f.run(t, f.repo, "", "claim", "w", "--desc", "d", "--scope", "pkg")
	if code != 0 {
		t.Fatalf("a sole live session must still resolve: %s %s", out, errw)
	}
	for _, want := range []string{"assuming you are", "alpha", "sess-a", "--session"} {
		if !strings.Contains(errw, want) {
			t.Fatalf("an inferred identity must say so, naming who and how to correct it (missing %q): %q", want, errw)
		}
	}
	// A KNOWN identity is not a guess and must stay quiet.
	f.asSession("sess-a", func() {
		_, errw, code := f.run(t, f.repo, "", "release", "w")
		if code != 0 {
			t.Fatal(errw)
		}
		if strings.Contains(errw, "assuming") {
			t.Fatalf("an asserted identity must not be announced as a guess: %q", errw)
		}
	})
}

// Symptom 2: the MCP server signs chat relays with SessionLabelFor. Five stdio
// servers share one checkout — each spawned by its own session, each carrying
// its own id — so the label must come from identity, and must be empty (→
// "agent") rather than a bystander's name when identity is unknown.
func TestSessionLabelForUsesIdentityNotTheDirectory(t *testing.T) {
	f := newFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	f.helloIn(t, f.repo, "sess-a", "alpha")
	f.helloIn(t, f.repo, "sess-b", "bravo")

	label := func(env map[string]string) string {
		e := Env{Cwd: f.repo, Now: func() time.Time { return f.clock },
			Getenv: func(k string) string { return env[k] }}
		st, err := openLedger(f.repo, e)
		if err != nil {
			t.Fatalf("open ledger: %v", err)
		}
		defer st.Close()
		si, err := whoAmI(st, e, "")
		if err != nil {
			return ""
		}
		return si.Label
	}

	if got := label(map[string]string{EnvClaudeSession: "sess-b"}); got != "bravo" {
		t.Fatalf("label must follow the calling session: %q, want bravo", got)
	}
	if got := label(map[string]string{EnvClaudeSession: "sess-a"}); got != "alpha" {
		t.Fatalf("label must follow the calling session: %q, want alpha", got)
	}
	if got := label(map[string]string{}); got != "" {
		t.Fatalf("an ambiguous directory must sign nothing (caller renders \"agent\"), got %q", got)
	}
}
