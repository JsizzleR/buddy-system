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
		{"unregisteredClause", unregisteredClause, "`buddy hello --session %s --label <name>`"},
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
}
