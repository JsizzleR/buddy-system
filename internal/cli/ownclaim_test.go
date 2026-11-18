package cli

// Tests for D-228: the gate catches an edit no claim of the calling session
// covers. Rows are the plan's (enforce-own-claim-plan.md 5.2, as Fable's panel
// rulings amended it) and the lane's design note; each silent row sits beside
// a warn or deny from the same fixture, so "silent" cannot mean "never ran".
// Base: A (sess-a, the main checkout) and B (sess-b, linked worktree wtB) are
// registered; A holds internal/router, which arms the ledger for B.

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JsizzleR/buddy-system/internal/store"
)

type ownVerdict struct {
	kind string // "silent", "warn", "deny"
	text string
	keys []string // hookSpecificOutput's keys, sorted as decoded
}

func (v ownVerdict) String() string { return v.kind + ": " + v.text }

// ownGate runs the gate with env overrides and classifies its output.
func ownGate(t *testing.T, f *fixture, cwd, payload string, env map[string]string) ownVerdict {
	t.Helper()
	saved := map[string]string{}
	for k, v := range env {
		saved[k] = f.env[k]
		f.env[k] = v
	}
	defer func() {
		for k, v := range saved {
			f.env[k] = v
		}
	}()
	out, errw, code := f.run(t, cwd, payload, "gate")
	if code != 0 {
		t.Fatalf("gate exit %d: %s %s", code, out, errw)
	}
	if strings.TrimSpace(out) == "" {
		return ownVerdict{kind: "silent"}
	}
	if strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Fatalf("the gate wrote more than one document: %q", out)
	}
	var raw map[string]map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("gate output is not hook JSON: %q", out)
	}
	hs := raw["hookSpecificOutput"]
	var keys []string
	for k := range hs {
		keys = append(keys, k)
	}
	if d, _ := hs["permissionDecision"].(string); d != "" {
		if d != "deny" {
			t.Fatalf("the gate must never write a permissionDecision but deny: %q", out)
		}
		r, _ := hs["permissionDecisionReason"].(string)
		return ownVerdict{kind: "deny", text: r, keys: keys}
	}
	c, _ := hs["additionalContext"].(string)
	return ownVerdict{kind: "warn", text: c, keys: keys}
}

func want(t *testing.T, row string, got ownVerdict, kind string, contains ...string) {
	t.Helper()
	if got.kind != kind {
		t.Fatalf("%s: want %s, got %s", row, kind, got)
	}
	for _, c := range contains {
		if !strings.Contains(got.text, c) {
			t.Fatalf("%s: %s must contain %q: %q", row, kind, c, got.text)
		}
	}
}

func ownFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.initAndHello(t)
	mustClaim(t, f, f.repo, "router", "sess-a", "internal/router")
	return f
}

func edit(session, cwd, p string) string { return hookJSON(session, cwd, "Edit", p) }

// handEdit runs one statement on the ledger of the repository at dir: a state
// nothing in buddy produces, planted the way an operator's sqlite3 would.
func handEdit(t *testing.T, dir, q string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, ".git", "buddy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

var (
	denyEnv = map[string]string{EnvOwnClaim: "deny"}
	offEnv  = map[string]string{EnvOwnClaim: "off"}
)

// Rows 1-4: the warn's shape, deny, off, a bad value.
func TestOwnClaimPostures(t *testing.T) {
	boundedParallel(t)
	f := ownFixture(t)
	p := filepath.Join(f.wtB, "docs/USAGE.md")
	v := ownGate(t, f, f.wtB, edit("sess-b", f.wtB, p), nil)
	want(t, "row 1", v, "warn", ownClaimLead, "docs/USAGE.md", "<a-new-slug>", "--scope docs/USAGE.md",
		"re-claiming a slug you hold replaces its scopes", "--shared", "tell your parent")
	if len(v.keys) != 2 || !(v.keys[0] == "hookEventName" || v.keys[1] == "hookEventName") ||
		!(v.keys[0] == "additionalContext" || v.keys[1] == "additionalContext") {
		t.Fatalf("row 1: the warn's keys must be exactly hookEventName and additionalContext: %v", v.keys)
	}
	if strings.Contains(v.text, "run from") || strings.Contains(v.text, "buddy hello") {
		t.Fatalf("row 1: same ledger, registered caller: no run-from, no hello: %q", v.text)
	}
	want(t, "row 2", ownGate(t, f, f.wtB, edit("sess-b", f.wtB, p), denyEnv), "deny",
		"inside no claim of this session", "BUDDY_OWN_CLAIM=deny", "--scope docs/USAGE.md")
	want(t, "row 3", ownGate(t, f, f.wtB, edit("sess-b", f.wtB, p), offEnv), "silent")
	// Row 4: a typo of deny refuses under Claude and Codex alike, fenced.
	for _, h := range []string{"", "codex"} {
		env := map[string]string{EnvOwnClaim: "dney\nx", EnvHarness: h}
		want(t, "row 4 "+h, ownGate(t, f, f.wtB, edit("sess-b", f.wtB, p), env), "deny",
			"BUDDY_OWN_CLAIM=", "dney", "not warn, deny or off")
	}
	// A bad value says nothing where the arm would not speak: a covered path.
	mustClaim(t, f, f.wtB, "usage", "sess-b", "docs/USAGE.md")
	want(t, "row 4 covered", ownGate(t, f, f.wtB, edit("sess-b", f.wtB, p), map[string]string{EnvOwnClaim: "dney"}), "silent")
}

// Row 3, structurally: off builds no judge, so denyIfHeld runs OwnerOf and
// none of the arm's reads (Codex design pass F5). And a new read's error
// fails closed (invariant 2): a ledger whose facts cannot be read denies under
// warn, the posture that would otherwise have said nothing worse than a line.
func TestOwnClaimOffBuildsNoJudgeAndAReadErrorDenies(t *testing.T) {
	env := Env{Getenv: func(k string) string { return map[string]string{EnvOwnClaim: "off"}[k] }}
	if newOwnJudge(env, hookInput{SessionID: "s1"}) != nil {
		t.Fatal("off must build no judge")
	}
	if newOwnJudge(Env{Getenv: func(string) string { return "" }}, hookInput{}) != nil {
		t.Fatal("a call naming no session builds no judge")
	}
	j := newOwnJudge(Env{Getenv: func(string) string { return "" }}, hookInput{SessionID: "s1"})
	if j == nil {
		t.Fatal("control: warn builds a judge")
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "buddy.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	j.target("/r/docs/a.md")
	j.observe(st, "/r", "docs/a.md", "docs/a.md", false)
	reason, warn := j.verdict(hookInput{SessionID: "s1", ToolName: "Edit"}, Env{})
	if warn != "" || !strings.Contains(reason, "ledger read failed") {
		t.Fatalf("a facts read that fails must deny: reason=%q warn=%q", reason, warn)
	}
}

// Rows 5, 6, 14: arming.
func TestOwnClaimArming(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	p := filepath.Join(f.wtB, "docs/USAGE.md")
	want(t, "row 5 nobody holds anything", ownGate(t, f, f.wtB, edit("sess-b", f.wtB, p), denyEnv), "silent")
	mustClaim(t, f, f.repo, "orc-x", "sess-a", ".buddy/slot/orc-x")
	want(t, "row 5 a slot arms", ownGate(t, f, f.wtB, edit("sess-b", f.wtB, p), nil), "warn", ownClaimLead)
	// Row 14: B's own slot covers only paths under itself, and its own claims
	// never arm: with A's slot gone, B alone is silent again.
	mustClaim(t, f, f.wtB, "x", "sess-b", ".buddy/slot/x")
	want(t, "row 14", ownGate(t, f, f.wtB, edit("sess-b", f.wtB, p), nil), "warn")
	if _, errw, code := f.run(t, f.repo, "", "release", "orc-x", "--session", "sess-a"); code != 0 {
		t.Fatal(errw)
	}
	want(t, "row 5 own claims never arm", ownGate(t, f, f.wtB, edit("sess-b", f.wtB, p), denyEnv), "silent")
	// Row 6: a holder that said bye still refuses its paths and arms nothing.
	mustClaim(t, f, f.repo, "router", "sess-a", "internal/router")
	want(t, "row 6 control", ownGate(t, f, f.wtB, edit("sess-b", f.wtB, p), nil), "warn")
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "bye"); code != 0 {
		t.Fatalf("bye: %s", errw)
	}
	want(t, "row 6 ended holder arms nothing", ownGate(t, f, f.wtB, edit("sess-b", f.wtB, p), denyEnv), "silent")
	want(t, "row 6 ended holder still refuses", ownGate(t, f, f.wtB,
		edit("sess-b", f.wtB, filepath.Join(f.wtB, "internal/router/x.go")), nil), "deny", "router")
}

// Rows 7a-7e: a caller with no row, or an ended one.
func TestOwnClaimUnregisteredCaller(t *testing.T) {
	boundedParallel(t)
	f := ownFixture(t)
	p := filepath.Join(f.wtB, "docs/USAGE.md")
	want(t, "row 7a", ownGate(t, f, f.wtB, edit("sess-c", f.wtB, p), nil), "warn",
		"no live row", "buddy hello </dev/null", "--scope docs/USAGE.md")
	want(t, "row 7a deny", ownGate(t, f, f.wtB, edit("sess-c", f.wtB, p), denyEnv), "deny", "buddy hello </dev/null")
	want(t, "row 7a holder outranks", ownGate(t, f, f.wtB,
		edit("sess-c", f.wtB, filepath.Join(f.wtB, "internal/router/x.go")), nil), "deny", "router")
	// Row 7b: from the main cwd, the target in the linked worktree reaches the
	// cwd's own ledger through the foreign arm: decided by ledger, not arm.
	want(t, "row 7b", ownGate(t, f, f.repo, edit("sess-c", f.repo, p), nil), "warn", "buddy hello </dev/null")
	// Row 7e: an id hello refuses (D-201) is caught, and its line names no hello.
	v := ownGate(t, f, f.wtB, edit("s-bad", f.wtB, p), nil)
	want(t, "row 7e", v, "warn", "not one a ledger registers")
	if strings.Contains(v.text, "buddy hello") {
		t.Fatalf("row 7e: a refused id must not be sent to hello: %q", v.text)
	}
	// Row 7d: B ended, uncovered: hello first in both postures.
	if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "", ""), "bye"); code != 0 {
		t.Fatalf("bye: %s", errw)
	}
	want(t, "row 7d", ownGate(t, f, f.wtB, edit("sess-b", f.wtB, p), nil), "warn", "buddy hello </dev/null")
	want(t, "row 7d deny", ownGate(t, f, f.wtB, edit("sess-b", f.wtB, p), denyEnv), "deny", "buddy hello </dev/null")
	// Row 7f: an ended row whose id an older binary admitted and hello now
	// would not CREATE (D-201 checks only where a row is created): hello
	// revives it, so the line names hello, never "ask the operator" (Codex
	// code pass P3).
	handEdit(t, f.repo, `UPDATE sessions SET session_id='old id' WHERE session_id='sess-b'`)
	v = ownGate(t, f, f.wtB, edit("old id", f.wtB, p), nil)
	want(t, "row 7f", v, "warn", "buddy hello </dev/null")
	if strings.Contains(v.text, "not one a ledger registers") {
		t.Fatalf("row 7f: an existing row is revived by hello: %q", v.text)
	}
	// Row 7a, unarmed leg: A's claim gone, the no-row caller is silent.
	if _, errw, code := f.run(t, f.repo, "", "release", "router", "--session", "sess-a"); code != 0 {
		t.Fatal(errw)
	}
	want(t, "row 7a unarmed", ownGate(t, f, f.wtB, edit("sess-c", f.wtB, p), denyEnv), "silent")
}

// Row 7c: a cwd in a ledgerless nested repository, whose home R is walked to:
// R judges a registered session, never one it has no row for.
func TestOwnClaimWalkedHome(t *testing.T) {
	boundedParallel(t)
	f := ownFixture(t)
	lib := filepath.Join(f.repo, "vendor/lib")
	nestGit(t, lib, "init", "-q")
	p := filepath.Join(f.repo, "docs/x.md")
	want(t, "row 7c registered", ownGate(t, f, lib, edit("sess-b", lib, p), nil), "warn", "run from")
	want(t, "row 7c no row", ownGate(t, f, lib, edit("sess-c", lib, p), denyEnv), "silent")
}

// Rows 8-12: what covers.
func TestOwnClaimCoverage(t *testing.T) {
	boundedParallel(t)
	f := ownFixture(t)
	p := filepath.Join(f.wtB, "docs/USAGE.md")
	call := func() ownVerdict { return ownGate(t, f, f.wtB, edit("sess-b", f.wtB, p), denyEnv) }
	release := func(slug string) {
		t.Helper()
		if _, errw, code := f.run(t, f.wtB, "", "release", slug, "--session", "sess-b"); code != 0 {
			t.Fatal(errw)
		}
	}
	want(t, "control", call(), "deny")
	for _, c := range []struct {
		scope, kind string
	}{
		{"docs/USAGE.md", "silent"}, {"docs", "silent"}, {"docs/USAGE", "deny"}, {"docs/USAGE.md/x", "deny"},
		{"Docs/usage.md", "silent"},
	} {
		mustClaim(t, f, f.wtB, "cov", "sess-b", c.scope)
		want(t, "row 8/9/10 "+c.scope, call(), c.kind)
		release("cov")
	}
	// Row 10, NFD against NFC: a claim on the composed spelling covers the
	// decomposed one.
	mustClaim(t, f, f.wtB, "cafe", "sess-b", "docs/Caf\u00e9.md")
	want(t, "row 10 nfd", ownGate(t, f, f.wtB, edit("sess-b", f.wtB, filepath.Join(f.wtB, "docs/Cafe\u0301.md")), denyEnv), "silent")
	want(t, "row 10 control", ownGate(t, f, f.wtB, edit("sess-b", f.wtB, filepath.Join(f.wtB, "docs/Cafe.md")), denyEnv), "deny")
	release("cafe")
	// Row 11: a --shared claim of the caller covers.
	if _, errw, code := f.run(t, f.wtB, "", "claim", "sh", "--session", "sess-b", "--shared", "--scope", "docs"); code != 0 {
		t.Fatal(errw)
	}
	want(t, "row 11 shared", call(), "silent")
	release("sh")
	// Row 12: A holds docs, so A's holder deny (D7); handed to B, it covers.
	mustClaim(t, f, f.repo, "docs", "sess-a", "docs")
	want(t, "row 12 holder", call(), "deny", "claimed by session")
	if _, errw, code := f.run(t, f.repo, "", "release", "docs", "--session", "sess-a", "--to", "sess-b"); code != 0 {
		t.Fatal(errw)
	}
	want(t, "row 12 handed over", call(), "silent")
}

// Row 15: the .git exemption, at any depth, and what it does not exempt.
func TestOwnClaimGitComponent(t *testing.T) {
	boundedParallel(t)
	f := ownFixture(t)
	for _, rel := range []string{".git/info/exclude", ".GIT/config", "vendor/lib/.git/info/exclude", "Vendor/Lib/.GIT/config", ".claude/worktrees/w/.git"} {
		want(t, "row 15 "+rel, ownGate(t, f, f.repo, edit("sess-b", f.repo, filepath.Join(f.repo, rel)), denyEnv), "silent")
	}
	for _, rel := range []string{".gitignore", "vendor/lib/.gitignore", "mirror.git/config", ".github/x.yml"} {
		want(t, "row 15w "+rel, ownGate(t, f, f.wtB, edit("sess-b", f.wtB, filepath.Join(f.wtB, rel)), nil), "warn")
	}
}

// Row 16: every spelling must be covered.
func TestOwnClaimEverySpelling(t *testing.T) {
	boundedParallel(t)
	f := ownFixture(t)
	if err := os.WriteFile(filepath.Join(f.wtB, "CLAUDE.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("CLAUDE.md", filepath.Join(f.wtB, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	mustClaim(t, f, f.wtB, "agents", "sess-b", "AGENTS.md")
	v := ownGate(t, f, f.wtB, edit("sess-b", f.wtB, filepath.Join(f.wtB, "AGENTS.md")), nil)
	want(t, "row 16", v, "warn", "CLAUDE.md")
	mustClaim(t, f, f.wtB, "claude", "sess-b", "CLAUDE.md")
	want(t, "row 16 both covered", ownGate(t, f, f.wtB, edit("sess-b", f.wtB, filepath.Join(f.wtB, "AGENTS.md")), denyEnv), "silent")
}

// Rows 17, 18, 25: patches, Codex, a pathless call.
func TestOwnClaimPatchesAndCodex(t *testing.T) {
	boundedParallel(t)
	f := ownFixture(t)
	codex := map[string]string{EnvHarness: "codex"}
	codexDeny := map[string]string{EnvHarness: "codex", EnvOwnClaim: "deny"}
	// Row 17: one peer-held and one uncovered path: the holder's deny only.
	v := ownGate(t, f, f.wtB, ancestorPatchJSON("sess-b", f.wtB, "internal/router/x.go", "docs/a.md"), codexDeny)
	want(t, "row 17", v, "deny", "claimed by session")
	if strings.Contains(v.text, "inside no claim") {
		t.Fatalf("row 17: one document, the holder's: %q", v.text)
	}
	// Row 17, the uncovered path FIRST: it is observed before the holder's
	// deny is met, and the holder's deny is still the one document.
	v = ownGate(t, f, f.wtB, ancestorPatchJSON("sess-b", f.wtB, "docs/a.md", "internal/router/x.go"), codexDeny)
	want(t, "row 17 reversed", v, "deny", "claimed by session")
	if strings.Contains(v.text, "inside no claim") {
		t.Fatalf("row 17 reversed: one document, the holder's: %q", v.text)
	}
	// Row 25: Codex hears no warn; deny is honoured.
	patch := ancestorPatchJSON("sess-b", f.wtB, "docs/a.md")
	want(t, "row 25", ownGate(t, f, f.wtB, patch, codex), "silent")
	want(t, "row 25 deny", ownGate(t, f, f.wtB, patch, codexDeny), "deny", "inside no claim")
	// Row 25, a file_path tool under Codex: the harness alone keeps the warn
	// silent (the apply_patch test above cannot tell the two halves apart).
	want(t, "row 25 file_path", ownGate(t, f, f.wtB, edit("sess-b", f.wtB, filepath.Join(f.wtB, "docs/a.md")), codex), "silent")
	want(t, "row 25 file_path control", ownGate(t, f, f.wtB, edit("sess-b", f.wtB, filepath.Join(f.wtB, "docs/a.md")), nil), "warn")
	// Row 25n: a Codex session with no row (its default state).
	want(t, "row 25n", ownGate(t, f, f.wtB, ancestorPatchJSON("thread-c", f.wtB, "docs/a.md"), codex), "silent")
	want(t, "row 25n deny", ownGate(t, f, f.wtB, ancestorPatchJSON("thread-c", f.wtB, "docs/a.md"), codexDeny), "deny", "buddy hello")
	// Row 25b: an unmarked Codex hook reads as Claude; apply_patch alone keeps
	// the warn silent. Its deny leg proves the arm was armed.
	want(t, "row 25b", ownGate(t, f, f.wtB, patch, nil), "silent")
	want(t, "row 25b control", ownGate(t, f, f.wtB, patch, denyEnv), "deny", "inside no claim")
	// Row 18: five uncovered files, a Claude caller: three named, then a count.
	files := []string{"a/1.go", "a/2.go", "a/3.go", "a/4.go", "a/5.go"}
	in, _ := json.Marshal(map[string]any{"session_id": "sess-b", "cwd": f.wtB, "tool_name": "MultiEdit",
		"tool_input": map[string]any{"file_path": filepath.Join(f.wtB, files[0])}})
	want(t, "row 18 single", ownGate(t, f, f.wtB, string(in), nil), "warn", "a/1.go")
	vd := ownGate(t, f, f.wtB, ancestorPatchJSON("sess-b", f.wtB, files...), denyEnv)
	want(t, "row 18 deny lists all", vd, "deny", "a/5.go")
	want(t, "row 18 pathless", ownGate(t, f, f.wtB, bashHookJSON("sess-b", f.wtB, "echo x > docs/a.md"), denyEnv), "silent")
}

// Row 18's count, on the warn: a multi-path Claude call is a patch only under
// Codex, so the renderer is tested directly.
func TestOwnClaimWarnCountsPastThree(t *testing.T) {
	var found []ownFinding
	for _, p := range []string{"a/1", "a/2", "a/3", "a/4", "a/5"} {
		found = append(found, ownFinding{show: p, top: "/r"})
	}
	if got := ownPaths(found, 3); !strings.Contains(got, "a/3") || strings.Contains(got, "a/4") || !strings.Contains(got, "and 2 more") {
		t.Fatalf("three paths, then a count: %q", got)
	}
}

// Rows 19, 20, 24: where the caller stands.
func TestOwnClaimWhereFrom(t *testing.T) {
	boundedParallel(t)
	f := ownFixture(t)
	p := filepath.Join(f.wtB, "docs/a.md")
	v := ownGate(t, f, f.repo, edit("sess-b", f.repo, p), nil)
	want(t, "row 19", v, "warn", "(in "+canon(f.wtB)+")")
	if strings.Contains(v.text, "run from") {
		t.Fatalf("row 19: the same ledger needs no run-from: %q", v.text)
	}
	plain := canon(t.TempDir())
	want(t, "row 20 no row", ownGate(t, f, plain, edit("sess-c", plain, p), denyEnv), "silent")
	want(t, "row 20 known", ownGate(t, f, plain, edit("sess-b", plain, p), nil), "warn", "run from "+canon(f.wtB))
	in, _ := json.Marshal(map[string]any{"session_id": "sess-b", "cwd": f.wtB, "tool_name": "Edit", "agent_id": "sub-1",
		"tool_input": map[string]any{"file_path": p}})
	want(t, "row 24", ownGate(t, f, f.wtB, string(in), nil), "warn", ownClaimLead)
}

// Rows 21-23, 21b: a nested ledger-bearing repository R/sub.
func TestOwnClaimNestedLedgers(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	sub := filepath.Join(f.repo, "sub")
	nestGit(t, sub, "init", "-q")
	initLedger(t, f, sub)
	hi := func(session, label string) {
		t.Helper()
		if _, errw, code := f.run(t, sub, hookJSON(session, sub, "", ""), "hello", "--label", label); code != 0 {
			t.Fatalf("hello %s in sub: %s", session, errw)
		}
	}
	x := filepath.Join(sub, "x")
	call := func(env map[string]string) ownVerdict { return ownGate(t, f, f.repo, edit("sess-a", f.repo, x), env) }
	// B is a peer in sub's ledger; A is in both, covered in neither.
	hi("sess-a", "alpha")
	hi("sess-b", "bravo")
	mustClaim(t, f, sub, "peer", "sess-b", "y")
	// Row 21b first: R/sub armed, R not (nobody else holds anything in R).
	want(t, "row 21", call(nil), "warn", "x (in "+canon(sub)+")", "run from "+canon(sub))
	want(t, "row 21 deny", call(denyEnv), "deny", "inside no claim")
	// Row 22: covered in R/sub only; in R only (sub/x).
	mustClaim(t, f, sub, "mine", "sess-a", "x")
	want(t, "row 22 sub", call(denyEnv), "silent")
	if _, errw, code := f.run(t, sub, "", "release", "mine", "--session", "sess-a"); code != 0 {
		t.Fatal(errw)
	}
	mustClaim(t, f, f.repo, "mine-r", "sess-a", "sub/x")
	want(t, "row 22 R", call(denyEnv), "silent")
	if _, errw, code := f.run(t, f.repo, "", "release", "mine-r", "--session", "sess-a"); code != 0 {
		t.Fatal(errw)
	}
	// Row 21b: R/sub known to the caller and UNARMED, R armed: R catches.
	if _, errw, code := f.run(t, sub, "", "release", "peer", "--session", "sess-b"); code != 0 {
		t.Fatal(errw)
	}
	mustClaim(t, f, f.repo, "r-peer", "sess-b", "elsewhere")
	want(t, "row 21b", call(nil), "warn", "sub/x (in "+canon(f.repo)+")")
	// Row 23: R/sub armed but the caller (sess-c, known only to R) has no row
	// there: R's finding stands.
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-c", f.repo, "", ""), "hello", "--label", "charlie"); code != 0 {
		t.Fatal(errw)
	}
	mustClaim(t, f, sub, "peer2", "sess-b", "y")
	v := ownGate(t, f, f.repo, edit("sess-c", f.repo, x), nil)
	want(t, "row 23", v, "warn", "sub/x (in "+canon(f.repo)+")")
	// Nearest wins: A is known in both, both armed, uncovered in both, so
	// the deeper root, sub, catches and is where to claim (Fable cross-check:
	// no row had both ledgers judging and armed, so a dropped sort survived).
	want(t, "row 21 nearest", call(nil), "warn", "x (in "+canon(sub)+")", "run from "+canon(sub))
	// Row 21c: a covering claim counts only in a ledger that judges the
	// caller. A's claim on x in sub, then A's sessions row there removed by
	// hand: sub no longer judges A, so R (armed, knowing A) catches (Codex
	// code pass P2). Control first: with the row, sub's claim covers.
	mustClaim(t, f, sub, "mine2", "sess-a", "x")
	want(t, "row 21c control", call(denyEnv), "silent")
	handEdit(t, sub, `DELETE FROM sessions WHERE session_id='sess-a'`)
	want(t, "row 21c", call(nil), "warn", "sub/x (in "+canon(f.repo)+")")
}

// Row 26: an unreadable ledger denies as it did; no ledger is 0 bytes.
func TestOwnClaimNoLedgerIsSilent(t *testing.T) {
	boundedParallel(t)
	dir := canon(t.TempDir())
	nestGit(t, dir, "init", "-q")
	f := &fixture{repo: dir, env: map[string]string{}, alive: map[int]int64{}}
	want(t, "row 26 no ledger", ownGate(t, f, dir, edit("sess-b", dir, filepath.Join(dir, "a")), denyEnv), "silent")
	want(t, "row 26 bad value, no ledger", ownGate(t, f, dir, edit("sess-b", dir, filepath.Join(dir, "a")), map[string]string{EnvOwnClaim: "dney"}), "silent")
}

// Row 29: the hazard P1-B moves the handoff note out of worktrees for. A
// claim on `.buddy-state.md` from one worktree refuses every other lane's own
// note: one ledger, worktree-relative scopes. Today's holder deny, pinned.
func TestOwnClaimStateNoteHazard(t *testing.T) {
	boundedParallel(t)
	f := ownFixture(t)
	mustClaim(t, f, f.repo, "note", "sess-a", ".buddy-state.md")
	want(t, "row 29", ownGate(t, f, f.wtB, hookJSON("sess-b", f.wtB, "Write", filepath.Join(f.wtB, ".buddy-state.md")), nil),
		"deny", "claimed by session")
}

// Row 30: a registered linked worktree nested under the home is placed
// against its own root only (#145), so a claim on the home's spelling of it
// does not cover, and the worktree-root spelling does.
func TestOwnClaimNestedLinkedWorktree(t *testing.T) {
	boundedParallel(t)
	f := ownFixture(t)
	w := filepath.Join(f.repo, ".claude/worktrees/w")
	f.git(t, f.repo, "worktree", "add", "-q", w)
	p := filepath.Join(w, "docs/a.md")
	mustClaim(t, f, f.repo, "home-spelling", "sess-b", ".claude/worktrees/w")
	want(t, "row 30", ownGate(t, f, f.repo, edit("sess-b", f.repo, p), nil), "warn", "docs/a.md (in "+canon(w)+")")
	mustClaim(t, f, f.repo, "root-spelling", "sess-b", "docs/a.md")
	want(t, "row 30 covered", ownGate(t, f, f.repo, edit("sess-b", f.repo, p), denyEnv), "silent")
}

// Row 31: a scope is printed as a command only when it pastes back as itself.
func TestOwnClaimPastableScope(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"docs/a.md", "docs/a.md"}, {"docs/a,b.md", "docs"}, {`docs/a\b.md`, "docs"},
		{"docs/a\tb.md", "docs"}, {" a.md", ""}, {"-x", "-x"}, {"--shared/a.md", "--shared/a.md"},
	} {
		got, ok := pastableScope(c.in)
		if got != c.want || ok != (c.want != "") {
			t.Fatalf("pastableScope(%q) = %q %v, want %q", c.in, got, ok, c.want)
		}
	}
	if !gitComponent("a/.git/b") || gitComponent("a/.github/b") || gitComponent("mirror.git/x") || !gitComponent(".GIT") {
		t.Fatal("gitComponent: exactly .git, any depth, folded")
	}
}

// Panel a6: hello's digest says a non-default posture once, and nothing for
// the default (an inherited off otherwise reads exactly like a covered fleet).
func TestOwnClaimHelloSaysANonDefaultPosture(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	for _, c := range []struct{ v, want string }{
		{"", ""}, {"warn", ""}, {"off", "OFF (no line, no refusal)"}, {"deny", "a REFUSAL"},
		{"dney", "not warn, deny or off"},
	} {
		f.env[EnvOwnClaim] = c.v
		out, errw, code := f.run(t, f.repo, hookJSON("sess-h", f.repo, "", ""), "hello", "--label", "hotel")
		if code != 0 {
			t.Fatalf("hello: %s", errw)
		}
		has := strings.Contains(out+errw, EnvOwnClaim+"=")
		if c.want == "" && has || c.want != "" && (!has || !strings.Contains(out+errw, c.want)) {
			t.Fatalf("hello with %s=%q: want %q, got %q %q", EnvOwnClaim, c.v, c.want, out, errw)
		}
	}
}

// Row 7d off the cwd's own ledger: an ENDED row judges too (live or ended),
// and its line says hello first (Fable cross-check: row 7d ran only at the
// cwd's own ledger, so `known && !ended` survived).
func TestOwnClaimEndedCallerElsewhere(t *testing.T) {
	boundedParallel(t)
	f := ownFixture(t)
	p := filepath.Join(f.wtB, "docs/a.md")
	plain := canon(t.TempDir())
	want(t, "control", ownGate(t, f, plain, edit("sess-b", plain, p), nil), "warn", "run from "+canon(f.wtB))
	if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "", ""), "bye"); code != 0 {
		t.Fatalf("bye: %s", errw)
	}
	want(t, "row 7d elsewhere", ownGate(t, f, plain, edit("sess-b", plain, p), nil), "warn",
		"run from "+canon(f.wtB), "buddy hello </dev/null")
}

// Row 31 through the gate: the printed scope pastes back, and where no
// scope can cover the path as named the line says so instead of printing a
// claim that is refused (Fable cross-check).
func TestOwnClaimRemedyPastesBack(t *testing.T) {
	boundedParallel(t)
	f := ownFixture(t)
	for _, c := range []struct{ rel, want, not string }{
		{"docs/a,b.md", "--scope docs`", "--scope docs/a,b.md"},
		{"a,b.md", "rename it, or ask the operator", "--scope"},
		{"--shared", "--scope=--shared`", "rename"},
		{"-x", "--scope=-x`", "rename"},
	} {
		v := ownGate(t, f, f.wtB, edit("sess-b", f.wtB, filepath.Join(f.wtB, c.rel)), nil)
		want(t, "row 31 "+c.rel, v, "warn", c.want)
		if strings.Contains(v.text, c.not) {
			t.Fatalf("row 31 %s: must not print %q: %q", c.rel, c.not, v.text)
		}
	}
	// A leading-dash scope printed inline is one claim accepts, and it covers
	// (Codex re-verify 1: `-x` was told "rename it").
	for i, sc := range []string{"--scope=--shared", "--scope=-x"} {
		if _, errw, code := f.run(t, f.wtB, "", "claim", "dash-"+string(rune('a'+i)), "--session", "sess-b", sc); code != 0 {
			t.Fatalf("claim %s, as the remedy prints it: %s", sc, errw)
		}
	}
	for _, rel := range []string{"--shared", "-x"} {
		want(t, "row 31 pasted "+rel, ownGate(t, f, f.wtB, edit("sess-b", f.wtB, filepath.Join(f.wtB, rel)), denyEnv), "silent")
	}
	// A mixed group: the pastable path is printed, and the unpastable one
	// gets the rename-or-ask fallback, not "a scope covering it".
	vm := ownGate(t, f, f.wtB, ancestorPatchJSON("sess-b", f.wtB, "ok.go", "a,b.md"), denyEnv)
	want(t, "row 31 mixed", vm, "deny", "--scope ok.go", "the other 1 path(s) as printed: rename them, or ask the operator")
	if strings.Contains(vm.text, "a scope covering") {
		t.Fatalf("row 31 mixed: %q", vm.text)
	}
}
