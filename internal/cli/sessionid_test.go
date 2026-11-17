package cli

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

// pasteID admits exactly what the ledger registers, on the RAW id; every
// hostile shape gets the placeholder (the positive controls in the same table
// prove the guard is not refusing everything).
func TestPasteID(t *testing.T) {
	for _, c := range []struct {
		id string
		ok bool
	}{
		{"07f63349-294a-489d-8f9b-79a9cb7b37a9", true},
		{"sess-a", true},
		{"sess\tx", false}, // fences to `sess x`, another session
		{"sess x", false},
		{"x`y", false}, // ends a markdown code span
		{"--help", false},
		{"a;curl x|sh", false},
		{"", false},
		{strings.Repeat("a", 129), false},
	} {
		p, ok := pasteID(c.id)
		if ok != c.ok || (ok && p != c.id) || (!ok && p != "") {
			t.Errorf("pasteID(%q) = %q, %v; want ok=%v", c.id, p, ok, c.ok)
		}
		if got := pasteIDOr(c.id, "<id>"); c.ok && got != c.id || !c.ok && got != "<id>" {
			t.Errorf("pasteIDOr(%q) = %q", c.id, got)
		}
	}
}

// wait.go's timer advice for a wait declared with --session names the id as a
// command only when it pastes back as printed.
func TestTimerEndAdvicePasteRule(t *testing.T) {
	ok := timerEndAdvice(false, store.SessionInfo{SessionID: "sess-a"}, "buddy")
	if !strings.Contains(ok, "`buddy wait clear --session sess-a`") {
		t.Fatalf("control: a valid id must be pasteable: %q", ok)
	}
	for _, id := range []string{"sess\tx", "x`y", "a;b"} {
		got := timerEndAdvice(false, store.SessionInfo{SessionID: id}, "buddy")
		if !strings.Contains(got, "`buddy wait clear --session <its session id>`") {
			t.Fatalf("%q: want the placeholder, got %q", id, got)
		}
	}
	if self := timerEndAdvice(true, store.SessionInfo{SessionID: "sess\tx"}, "buddy"); !strings.Contains(self, "`buddy wait clear`") {
		t.Fatalf("self names no id: %q", self)
	}
}

// sessionRow reads one session row straight from the fixture's ledger.
func ledgerHasSession(t *testing.T, f *fixture, id string) bool {
	t.Helper()
	st, err := store.Open(filepath.Join(f.repo, ".git", "buddy.db"), func() time.Time { return f.clock })
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, ok, err := st.Session(id)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// legacySession seeds a row the way a buddy before D-201 could have written
// it: registered under a valid stand-in, then renamed by SQL to id.
func legacySession(t *testing.T, f *fixture, id, label string) {
	t.Helper()
	seed := "legacy-seed-" + strconv.Itoa(len(id))
	if _, errw, code := f.run(t, f.repo, "", "hello", "--session", seed, "--label", label); code != 0 {
		t.Fatalf("seed hello: %s", errw)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(f.repo, ".git", "buddy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE sessions SET session_id=? WHERE session_id=?`, id, seed); err != nil {
		t.Fatal(err)
	}
}

var hostileIDs = []string{"sess x", "sess\tx", "a\nb", "x‮y", "x​y", "s-1234abcd", "all", "-x", "a`b", "a;b", strings.Repeat("a", 129), "wt/s-1"}

// Every entry path that creates a session row refuses an unregistrable id and
// writes no row; the controls through the same paths register (D-201).
func TestHelloRefusesUnregistrableIDsByEveryPath(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	if out, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatalf("init: %s %s", out, errw)
	}
	for i, id := range append([]string{"07f63349-294a-489d-8f9b-79a9cb7b37a9", "sess-ok"}, hostileIDs...) {
		valid := i < 2
		// (a) the SessionStart hook: payload id, stdout line, exit 0.
		out, errw, code := f.run(t, f.repo, hookJSON(id, f.repo, "", ""), "hello")
		if valid {
			if code != 0 || !ledgerHasSession(t, f, id) {
				t.Fatalf("control %q via hook: %d %s %s", id, code, out, errw)
			}
			continue
		}
		if code != 0 || ledgerHasSession(t, f, id) {
			t.Fatalf("%q via hook: want exit 0 and no row, got %d (%s)", id, code, errw)
		}
		if strings.Count(out, "\n") != 1 || !strings.Contains(out, "cannot be registered") ||
			!strings.Contains(out, "do not say hello under another id") || strings.Contains(out, "A-Z a-z") {
			t.Fatalf("%q via hook: want ONE native refusal line with no charset recipe:\n%s", id, out)
		}
		// (b) hand-run --session: an error that states the rule.
		_, errw, code = f.run(t, f.repo, "", "hello", "--session", id)
		if code == 0 || ledgerHasSession(t, f, id) || !strings.Contains(errw, "A-Z a-z 0-9") || strings.Contains(errw, "\n"+"x") {
			t.Fatalf("%q via --session: want a refusal with the rule, got %d %q", id, code, errw)
		}
		// (c) $BUDDY_SESSION, hand-run: the rule too (a chosen id).
		f.env[EnvSession] = id
		_, errw, code = f.run(t, f.repo, "", "hello")
		delete(f.env, EnvSession)
		if code == 0 || ledgerHasSession(t, f, id) || !strings.Contains(errw, "A-Z a-z 0-9") {
			t.Fatalf("%q via $BUDDY_SESSION: %d %q", id, code, errw)
		}
		// (d) the native variable, hand-run: native wording, no recipe.
		f.env[EnvClaudeSession] = id
		_, errw, code = f.run(t, f.repo, "", "hello")
		delete(f.env, EnvClaudeSession)
		if code == 0 || ledgerHasSession(t, f, id) || strings.Contains(errw, "A-Z a-z") || !strings.Contains(errw, "another id") {
			t.Fatalf("%q via $%s: %d %q", id, EnvClaudeSession, code, errw)
		}
		// (e) lifecycle begin (the adapter's path): certain, no row, no recipe.
		out, errw, code = f.run(t, f.repo, "", "lifecycle", "begin", "--session", id)
		if code == 0 || ledgerHasSession(t, f, id) || !strings.Contains(errw, "do not retry under another id") || strings.Contains(errw, "A-Z a-z") {
			t.Fatalf("%q via lifecycle begin: %d %q %q", id, code, out, errw)
		}
	}
	if out, errw, code := f.run(t, f.repo, "", "lifecycle", "begin", "--session", "019a7f3e-5b2c-7d41-9e0f-123456789abc"); code != 0 {
		t.Fatalf("control lifecycle begin: %s %s", out, errw)
	}
}

// THE ALIAS TRAP (orc-2's ruling, D-201): why a native refusal names no
// recipe. A session whose harness id cannot register, told to pick another
// id, registers the alias and claims as it; the gate still judges the payload
// id, so the session is refused on its own claim. The native refusal must
// therefore say "do not say hello under another id", and does.
func TestNativeRefusalNamesNoAlias(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	if out, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatalf("init: %s %s", out, errw)
	}
	const native = "native id" // a harness id outside the predicate
	out, _, _ := f.run(t, f.repo, hookJSON(native, f.repo, "", ""), "hello")
	if !strings.Contains(out, "do not say hello under another id") || strings.Contains(out, "--session") {
		t.Fatalf("the native refusal must forbid an alias and name no hello command:\n%s", out)
	}
	if !strings.Contains(out, "only `buddy pause all` brakes it") {
		t.Fatalf("the home ledger's refusal says what being unregistered costs there:\n%s", out)
	}
	// What following a recipe would cost: register an alias and claim as it.
	if _, errw, code := f.run(t, f.repo, "", "hello", "--session", "alias-1"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := f.run(t, f.repo, "", "claim", "w", "--session", "alias-1", "--desc", "x", "--scope", "src"); code != 0 {
		t.Fatal(errw)
	}
	gout, _, _ := f.run(t, f.repo, hookJSON(native, f.repo, "Edit", filepath.Join(f.repo, "src/a.go")), "gate")
	if _, denied := decodeDeny(t, gout); !denied {
		t.Fatalf("the trap: the gate judges the payload id, so the alias's own claim refuses it: %q", gout)
	}
}

// The gate for a session whose id could not register (orc-2's (d)): `pause
// all` brakes it (the one brake that reaches it: the positive control that
// the gate is armed for it), an unclaimed path is allowed, a peer's claimed
// path is refused.
func TestGateForAnUnregistrableID(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	const bad = "bad id"
	if out, _, _ := f.run(t, f.repo, hookJSON(bad, f.repo, "", ""), "hello"); !strings.Contains(out, "cannot be registered") {
		t.Fatalf("hello must refuse: %q", out)
	}
	if _, errw, code := f.run(t, f.repo, "", "claim", "peer", "--session", "sess-a", "--desc", "x", "--scope", "claimed"); code != 0 {
		t.Fatal(errw)
	}
	free := filepath.Join(f.repo, "free/a.go")
	if out, _, _ := f.run(t, f.repo, hookJSON(bad, f.repo, "Edit", free), "gate"); func() bool { _, d := decodeDeny(t, out); return d }() {
		t.Fatalf("an unclaimed path must be allowed: %q", out)
	}
	if out, _, _ := f.run(t, f.repo, hookJSON(bad, f.repo, "Edit", filepath.Join(f.repo, "claimed/a.go")), "gate"); func() bool { _, d := decodeDeny(t, out); return d }() == false {
		t.Fatalf("a peer's claimed path must be refused: %q", out)
	}
	if _, errw, code := f.run(t, f.repo, "", "pause", "all", "--note", "stop"); code != 0 {
		t.Fatal(errw)
	}
	if out, _, _ := f.run(t, f.repo, hookJSON(bad, f.repo, "Edit", free), "gate"); func() bool { _, d := decodeDeny(t, out); return d }() == false {
		t.Fatalf("`pause all` must brake a session that could not register: %q", out)
	}
}

// A row an older binary wrote keeps working: the hook hello refreshes it, a
// typed verb finds it, and its id is printed fenced, never as a command.
func TestLegacyHostileRowStillWorks(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	if out, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatalf("init: %s %s", out, errw)
	}
	const legacy = "old\tid"
	legacySession(t, f, legacy, "old")
	if _, errw, code := f.run(t, f.repo, hookJSON(legacy, f.repo, "", ""), "hello"); code != 0 {
		t.Fatalf("a legacy row must refresh: %s", errw)
	}
	if _, errw, code := f.run(t, f.repo, "", "claim", "w", "--session", legacy, "--desc", "x", "--scope", "w"); code != 0 {
		t.Fatalf("a legacy row must be found: %s", errw)
	}
	out, _, _ := f.run(t, f.repo, "", "sessions")
	if strings.Contains(out, "\t") || !strings.Contains(out, "(old␣id)") {
		t.Fatalf("the sessions id column must be one Field token: %q", out)
	}
	// The worktree column too (invariant 9): a path with a space, which an
	// operator's directory can hold, was two tokens before the id column.
	db, err := sql.Open("sqlite", "file:"+filepath.Join(f.repo, ".git", "buddy.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE sessions SET worktree='/w/my tree' WHERE session_id=?`, legacy); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if out, _, _ := f.run(t, f.repo, "", "sessions"); !strings.Contains(out, "/w/my␣tree") {
		t.Fatalf("the sessions worktree column must be one Field token: %q", out)
	}
}

// The render sites of the paste rule, each with a valid control.
func TestPasteRuleSites(t *testing.T) {
	for _, c := range []struct {
		name string
		got  func(id string) string
		cmd  string // what a valid id renders, with %s for the id
	}{
		{"helloRemedy", func(id string) string { return helloRemedy(id, "registers it") }, "`buddy hello --session %s </dev/null` registers it"},
		{"byeRemedy", byeRemedy, "buddy bye %s,"},
		{"unregisteredClause", func(id string) string { return unregisteredClause(Env{Getenv: func(string) string { return "" }}, id) }, "`buddy hello --session %s --label <name>`"},
		{"errUncorroborated", func(id string) string {
			return errUncorroborated{match: store.SessionInfo{SessionID: id, Label: "l"}, liveTotal: 2}.Error()
		}, "`--session %s`"},
		{"helloCountLine", func(id string) string { return helloCountLine(true, true, 3, id) }, "`buddy inbox --session %s`"},
	} {
		const ok = "sess-a"
		if g := c.got(ok); !strings.Contains(g, strings.ReplaceAll(c.cmd, "%s", ok)) {
			t.Errorf("%s control: %q lacks %q", c.name, g, c.cmd)
		}
		for _, id := range []string{"sess\tx", "x`y", "a;b", "--help"} {
			g, f := c.got(id), fence.Line(id, 128)
			for _, bad := range []string{"--session " + f, "--session '" + f, "bye " + f, "bye '" + f} {
				if strings.Contains(g, bad) {
					t.Errorf("%s(%q) printed the id as a command (%q): %q", c.name, id, bad, g)
				}
			}
		}
	}
	for _, c := range []struct{ label, want string }{
		{"alpha", "'alpha'"},
		{"all", "<the holder: see buddy whose>"},
		{"a\tb", "<the holder: see buddy whose>"},
	} {
		if got := reachArg(c.label); got != c.want {
			t.Errorf("reachArg(%q) = %q, want %q", c.label, got, c.want)
		}
	}
}

// In a fleet, a refused id is said as itself, never as the fleet failing
// (panel: the deferred handler and helloBeside both reported it as a
// transient fleet error). Fleet-only hook and hand-run, and the fleet beside a
// home whose older-binary row still refreshes.
func TestFleetSaysAnIDRefusalAsItself(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fe.initFleet(t, "f1")
	fe.env[EnvFleet] = "f1"
	const bad = "bad id"
	out, errw, code := fe.hook(t, "hello", bad, fe.plain)
	if code != 0 || !strings.Contains(out, "cannot be registered in fleet f1") || strings.Contains(out, "fleet ledger") {
		t.Fatalf("fleet-only hook: %d %q %q", code, out, errw)
	}
	// A fleet has no brake (pause refuses there) and claims only slots, so
	// its refusal names the fleet's cost, never the home's (Codex code pass).
	if strings.Contains(out, "pause all") || !strings.Contains(out, "not on the fleet's roster") {
		t.Fatalf("fleet-only hook: the fleet's cost, not the home's: %q", out)
	}
	out, errw, code = fe.run(t, fe.plain, "", "hello", "--session", bad)
	if code == 0 || !strings.Contains(errw, "cannot be registered in fleet f1") || strings.Contains(out+errw, "fleet ledger") {
		t.Fatalf("fleet-only hand-run must refuse as itself: %d %q %q", code, out, errw)
	}
	if out, errw, code := fe.hook(t, "hello", "good-id", fe.plain); code != 0 || strings.Contains(out, "cannot be registered") {
		t.Fatalf("control: %d %q %q", code, out, errw)
	}

	// Beside a home: the home row is an older binary's, so it refreshes;
	// the fleet has none and refuses to create one.
	if o, e, c := fe.run(t, fe.repo, "", "init"); c != 0 {
		t.Fatalf("init: %s %s", o, e)
	}
	legacySession(t, fe.fixture, bad, "old")
	out, errw, code = fe.hook(t, "hello", bad, fe.repo)
	if code != 0 || !strings.Contains(out, "cannot be registered in fleet f1") || strings.Contains(out, "fleet ledger error") {
		t.Fatalf("beside: the fleet's refusal must be said as itself:\n%s\n%s", out, errw)
	}
	// The home row still refreshed, so "can hold no claim" would be false.
	if strings.Contains(out, "can hold no claim here") || strings.Contains(out, "pause all") {
		t.Fatalf("beside: the home half is registered; only the fleet's cost is said:\n%s", out)
	}
}

// sweep names an orphaned claim's holder by its whole id (D-201): it fenced
// the id at 64 while every other renderer fenced it at 128, so an admitted
// id of 65-128 bytes was printed cut, and the printed id named no session.
// A 100-byte id is admitted (the control), holds a claim, ends, and
// `sweep --dry-run` must print it uncut.
func TestSweepPrintsAWholeAdmittedID(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	if out, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatalf("init: %s %s", out, errw)
	}
	long := "a" + strings.Repeat("0123456789", 9) + "bcdefghij" // 100 bytes
	if len(long) != 100 || !fence.ValidSessionID(long) {
		t.Fatalf("control: the id must be admitted and 100 bytes (%d)", len(long))
	}
	if _, errw, code := f.run(t, f.repo, hookJSON(long, f.repo, "", ""), "hello"); code != 0 || !ledgerHasSession(t, f, long) {
		t.Fatalf("hello: %d %s", code, errw)
	}
	f.claimFor(t, long, "long-work", "x", "w")
	if _, errw, code := f.run(t, f.repo, hookJSON(long, f.repo, "", ""), "bye"); code != 0 {
		t.Fatalf("bye: %s", errw)
	}
	out, errw, code := f.run(t, f.repo, "", "sweep", "--dry-run")
	if code != 0 || !strings.Contains(out, "would orphan long-work") {
		t.Fatalf("control: the dry run must name the claim: %d %q %q", code, out, errw)
	}
	if !strings.Contains(out, "("+long+")") {
		t.Fatalf("sweep must print the holder's whole id:\n%s", out)
	}
}

// An ENDED row an older binary wrote under an id the predicate refuses still
// revives under hello (the id is checked only where a row is created), so a
// typed verb's refusal must name the revival, never "cannot be registered"
// (Codex code pass). The hello afterwards is the positive control that the
// remedy is true.
func TestEndedLegacyRowIsNamedRevivable(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	if out, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatalf("init: %s %s", out, errw)
	}
	const legacy = "old:id"
	legacySession(t, f, legacy, "old")
	if _, errw, code := f.run(t, f.repo, hookJSON(legacy, f.repo, "", ""), "bye"); code != 0 {
		t.Fatalf("bye: %s", errw)
	}
	f.env[EnvClaudeSession] = legacy
	_, errw, code := f.run(t, f.repo, "", "status")
	if code == 0 || !strings.Contains(errw, "has ended") || !strings.Contains(errw, "`buddy hello --session <that id>` re-registers it here") || strings.Contains(errw, "cannot be registered") {
		t.Fatalf("an ended legacy row is revivable, and must be named so: %d %q", code, errw)
	}
	_, errw, code = f.run(t, f.repo, "", "hello")
	delete(f.env, EnvClaudeSession)
	if code != 0 {
		t.Fatalf("control: hello under the legacy id revives its row: %d %q", code, errw)
	}
	if si, ok := func() (store.SessionInfo, bool) {
		st, err := store.Open(filepath.Join(f.repo, ".git", "buddy.db"), func() time.Time { return f.clock })
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		si, ok, err := st.Session(legacy)
		if err != nil {
			t.Fatal(err)
		}
		return si, ok
	}(); !ok || !si.Live() {
		t.Fatalf("the legacy row must be live after hello: %+v %v", si, ok)
	}
}

// A --session value EQUAL to the harness's own id is that native id typed
// again, so its refusal is the native one with no recipe (the alias trap);
// a different chosen id is the control and gets the rule (Fable cross-check).
func TestTypedNativeIDGetsTheNativeWording(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	if out, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatalf("init: %s %s", out, errw)
	}
	f.env[EnvClaudeSession] = "bad id"
	defer delete(f.env, EnvClaudeSession)
	_, errw, code := f.run(t, f.repo, "", "hello", "--session", "bad id")
	if code == 0 || strings.Contains(errw, "A-Z a-z") || !strings.Contains(errw, "do not say hello under another id") {
		t.Fatalf("the harness's own id typed as --session is native: %d %q", code, errw)
	}
	_, errw, code = f.run(t, f.repo, "", "hello", "--session", "other bad")
	if code == 0 || !strings.Contains(errw, "A-Z a-z 0-9") {
		t.Fatalf("control: a chosen id gets the rule: %d %q", code, errw)
	}
}

// unregisteredClause is worded by where senderFor read the id: a chosen
// $BUDDY_SESSION gets the rule, the harness's own id no alias (Fable
// cross-check). Neither names a hello command for an id hello refuses.
func TestUnregisteredClauseWordedBySource(t *testing.T) {
	env := func(k, v string) Env {
		return Env{Getenv: func(n string) string {
			if n == k {
				return v
			}
			return ""
		}}
	}
	chosen := unregisteredClause(env(EnvSession, "two words"), "two words")
	if !strings.Contains(chosen, "A-Z a-z 0-9") || strings.Contains(chosen, "another id") || strings.Contains(chosen, "--session") {
		t.Fatalf("a chosen id gets the rule: %q", chosen)
	}
	native := unregisteredClause(env(EnvClaudeSession, "two words"), "two words")
	if strings.Contains(native, "A-Z a-z") || !strings.Contains(native, "do not say hello under another id") || strings.Contains(native, "--session") {
		t.Fatalf("a harness id gets no recipe: %q", native)
	}
}

// sweep's holder id is one column token (invariant 9): an older binary's
// row under `a b` printed two (Fable cross-check).
func TestSweepIDIsOneToken(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	if out, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatalf("init: %s %s", out, errw)
	}
	legacySession(t, f, "a b", "spacey")
	f.claimFor(t, "a b", "spacey-work", "x", "w")
	if _, errw, code := f.run(t, f.repo, hookJSON("a b", f.repo, "", ""), "bye"); code != 0 {
		t.Fatalf("bye: %s", errw)
	}
	out, errw, code := f.run(t, f.repo, "", "sweep", "--dry-run")
	if code != 0 || !strings.Contains(out, "would orphan spacey-work") {
		t.Fatalf("control: %d %q %q", code, out, errw)
	}
	if !strings.Contains(out, "(a␣b)") {
		t.Fatalf("the id must be one token: %q", out)
	}
}

// bye's --force remedy names the id only under the paste rule (D-201): an
// older binary's live row under `sess<TAB>x`, still registered to a running
// process, printed `buddy bye sess x --force`, which ends ANOTHER session.
// TestByeWithNoAnchorIsRefusedByALiveRegistration is the valid-id control.
func TestByeForceRemedyPasteRule(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.asProcess(200, 2, func() {
		if _, errw, code := f.run(t, f.repo, hookJSON("sess-z", f.repo, "", ""), "hello", "--label", "zulu"); code != 0 {
			t.Fatalf("hello: %s", errw)
		}
	})
	db, err := sql.Open("sqlite", "file:"+filepath.Join(f.repo, ".git", "buddy.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`UPDATE sessions SET session_id='sess	x' WHERE session_id='sess-z'`,
		`UPDATE session_procs SET session_id='sess	x' WHERE session_id='sess-z'`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	_, errw, code := f.run(t, f.repo, "", "bye", "sess\tx")
	if code == 0 || !strings.Contains(errw, "stays live") {
		t.Fatalf("control: the live registration must refuse the bye: %d %q", code, errw)
	}
	if !strings.Contains(errw, "`buddy bye <its session id> --force`") || strings.Contains(errw, "buddy bye sess x") {
		t.Fatalf("an unpasteable id gets the placeholder: %q", errw)
	}
}

// assertedNotLive applies the paste rule to the RAW id: `x<ZWSP>y` fences
// to `xy`, which the predicate admits, so a check of the fenced copy printed
// `buddy hello --session xy`, another id (Codex design pass). sess-q is the
// control: an unknown valid id gets its hello command.
func TestAssertedNotLiveChecksTheRawID(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	_, errw, code := f.run(t, f.repo, "", "status", "--session", "sess-q")
	if code == 0 || !strings.Contains(errw, "`buddy hello --session sess-q` registers it here") {
		t.Fatalf("control: %d %q", code, errw)
	}
	_, errw, code = f.run(t, f.repo, "", "status", "--session", "x​y")
	if code == 0 || strings.Contains(errw, "hello --session xy") || !strings.Contains(errw, "cannot be registered") {
		t.Fatalf("the raw id decides: %d %q", code, errw)
	}
}

// A typed verb's refusal for an id that a LOWER source asserts again counts
// that source (D-204): `--session` equal to the harness's own id was worded
// as chosen, so "otherwise re-run without --session" re-ran into the same id
// from the harness variable, and an unregistrable one got the charset recipe
// (the alias trap). One row per wording arm of assertedNotLive, each through
// --session and $BUDDY_SESSION equal to $CLAUDE_CODE_SESSION_ID; the control
// row per arm is a chosen id no other source carries, worded as before.
func TestAssertedNotLiveCountsTheSameIDTwice(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	legacySession(t, f, "old:id", "old")
	if _, errw, code := f.run(t, f.repo, hookJSON("old:id", f.repo, "", ""), "bye"); code != 0 {
		t.Fatalf("bye legacy: %s", errw)
	}
	if _, errw, code := f.run(t, f.repo, "", "hello", "--session", "sess-e", "--label", "e"); code != 0 {
		t.Fatalf("hello e: %s", errw)
	}
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-e", f.repo, "", ""), "bye"); code != 0 {
		t.Fatalf("bye e: %s", errw)
	}
	const rule = "A-Z a-z 0-9"
	const native = "do not say hello under another id"
	cases := []struct {
		name, id, harness, buddy string
		args                     []string
		want, not                []string
	}{
		{"unknown, pastes", "sess-n", "sess-n", "", []string{"--session", "sess-n"},
			[]string{"(from --session, the same id as $CLAUDE_CODE_SESSION_ID) has not said hello",
				"registers it here; otherwise re-run without --session and clear $CLAUDE_CODE_SESSION_ID"}, nil},
		{"unknown, pastes, control", "sess-n", "", "", []string{"--session", "sess-n"},
			[]string{"(from --session) has not said hello", "registers it here; otherwise re-run without --session"},
			[]string{"same id", "and clear"}},
		{"ended, pastes", "sess-e", "sess-e", "", []string{"--session", "sess-e"},
			[]string{"(from --session, the same id as $CLAUDE_CODE_SESSION_ID) has ended",
				"re-registers it here; otherwise re-run without --session and clear $CLAUDE_CODE_SESSION_ID"}, nil},
		{"ended, pastes, via BUDDY_SESSION", "sess-e", "sess-e", "sess-e", nil,
			[]string{"(from BUDDY_SESSION, the same id as $CLAUDE_CODE_SESSION_ID) has ended",
				"otherwise clear $BUDDY_SESSION and $CLAUDE_CODE_SESSION_ID"}, nil},
		{"ended, pastes, control", "sess-e", "", "", []string{"--session", "sess-e"},
			[]string{"(from --session) has ended", "re-registers it here; otherwise re-run without --session"},
			[]string{"same id", "and clear"}},
		{"ended, no paste", "old:id", "old:id", "", []string{"--session", "old:id"},
			[]string{"(from --session, the same id as $CLAUDE_CODE_SESSION_ID) has ended",
				"(its ended row revives; the id does not paste as printed); otherwise re-run without --session and clear $CLAUDE_CODE_SESSION_ID"}, nil},
		{"ended, no paste, control", "old:id", "", "", []string{"--session", "old:id"},
			[]string{"does not paste as printed); otherwise re-run without --session"}, []string{"same id", "and clear"}},
		{"unregistrable", "bad id", "bad id", "", []string{"--session", "bad id"},
			[]string{"(from --session, the same id as $CLAUDE_CODE_SESSION_ID)", "cannot be registered (D-201); it is this session's harness id, so " + native},
			[]string{rule}},
		{"unregistrable, via BUDDY_SESSION", "bad id", "bad id", "bad id", nil,
			[]string{"(from BUDDY_SESSION, the same id as $CLAUDE_CODE_SESSION_ID)", native}, []string{rule}},
		{"unregistrable, control", "bad id", "", "", []string{"--session", "bad id"},
			[]string{"(from --session) has not said hello", rule + " . _ -"}, []string{native, "same id"}},
		// The live lower source is reached only past every non-empty one above
		// it: here $BUDDY_SESSION carries the same dead id.
		{"live lower source", "sess-e", "sess-a", "sess-e", []string{"--session", "sess-e"},
			[]string{"(from --session, the same id as $BUDDY_SESSION)",
				"but CLAUDE_CODE_SESSION_ID names alpha (sess-a), which is live here: re-run without --session and clear $BUDDY_SESSION to use it"}, nil},
		{"live lower source, control", "sess-e", "sess-a", "", []string{"--session", "sess-e"},
			[]string{"which is live here: re-run without --session to use it"}, []string{"same id", "and clear"}},
	}
	for _, c := range cases {
		f.env[EnvClaudeSession], f.env[EnvSession] = c.harness, c.buddy
		_, errw, code := f.run(t, f.repo, "", append([]string{"status"}, c.args...)...)
		delete(f.env, EnvClaudeSession)
		delete(f.env, EnvSession)
		if code == 0 {
			t.Fatalf("%s: status must refuse %q: %q", c.name, c.id, errw)
		}
		for _, w := range c.want {
			if !strings.Contains(errw, w) {
				t.Errorf("%s: want %q in:\n%s", c.name, w, errw)
			}
		}
		for _, n := range c.not {
			if strings.Contains(errw, n) {
				t.Errorf("%s: must not say %q:\n%s", c.name, n, errw)
			}
		}
	}

	// A live harness variable is reached only if the other agrees with it: a
	// later $CODEX_THREAD_ID on the dead id must be cleared too (Codex code
	// pass).
	f.env[EnvClaudeSession], f.env[EnvSession], f.env[EnvCodexThread] = "sess-a", "sess-e", "sess-e"
	_, errw, code := f.run(t, f.repo, "", "status", "--session", "sess-e")
	delete(f.env, EnvSession)
	for _, w := range []string{"(from --session, the same id as $BUDDY_SESSION and $CODEX_THREAD_ID)",
		"which is live here: re-run without --session and clear $BUDDY_SESSION and $CODEX_THREAD_ID to use it"} {
		if code == 0 || !strings.Contains(errw, w) {
			t.Errorf("natives disagree: want %q in: %d %q", w, code, errw)
		}
	}

	// The remedies followed. The old one (drop --session and $BUDDY_SESSION
	// only) leaves the natives disagreeing, and is refused; the new one
	// reaches the live session.
	_, errw, code = f.run(t, f.repo, "", "status")
	if code == 0 || !strings.Contains(errw, "disagree") {
		t.Fatalf("the old remedy, followed, still refuses: %d %q", code, errw)
	}
	delete(f.env, EnvCodexThread)
	if _, errw, code := f.run(t, f.repo, "", "status"); code != 0 {
		t.Fatalf("the new remedy, followed, reaches sess-a: %d %q", code, errw)
	}
	delete(f.env, EnvClaudeSession)

	// wait check reads the natives alone; Codex's answers first, so the
	// Claude variable on the same id is counted (Codex code pass). The
	// control is Codex's alone.
	for _, w := range []struct {
		claude string
		want   string
		not    string
	}{
		{"sess-e", "(from CODEX_THREAD_ID, the same id as $CLAUDE_CODE_SESSION_ID) has ended — if that is really you, `buddy hello --session sess-e` re-registers it here; otherwise clear $CODEX_THREAD_ID and $CLAUDE_CODE_SESSION_ID", ""},
		{"", "(from CODEX_THREAD_ID) has ended — if that is really you, `buddy hello --session sess-e` re-registers it here; otherwise clear $CODEX_THREAD_ID", "same id"},
	} {
		f.env[EnvCodexThread], f.env[EnvClaudeSession] = "sess-e", w.claude
		_, errw, code := f.run(t, f.repo, "", "wait", "check")
		delete(f.env, EnvCodexThread)
		delete(f.env, EnvClaudeSession)
		if code == 0 || !strings.Contains(errw, w.want) || w.not != "" && strings.Contains(errw, w.not) {
			t.Errorf("wait check, claude=%q: want %q in: %d %q", w.claude, w.want, code, errw)
		}
	}
}
