package cli

import (
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JsizzleR/buddy-system/internal/store"
	_ "modernc.org/sqlite"
)

// THE FLEET LEDGER (D-191, docs/NON-GIT-PLAN.md stage 1b): every row of the
// plan's 5.2 table and of its 9.1, each with its positive control; each refusal
// of its section 6 beside the grant it stands next to; and 5.4's guarantee that
// the gate never reads a fleet ledger, held on the gate's own output.
//
// Every fleet here lives under a throwaway HOME the fixture injects (f.env
// starts empty and never reads the process environment), so no test can reach
// the developer's ~/.buddy/fleets.

// fleetEnv is a fixture with a throwaway HOME and a plain directory outside
// every repository, the place an investigation lane lives.
type fleetEnv struct {
	*fixture
	home, plain string
}

func newFleet(t *testing.T) *fleetEnv {
	t.Helper()
	f := newFixture(t)
	home := t.TempDir()
	plain := filepath.Join(t.TempDir(), "inv", "i01")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if exec.Command("git", "-C", plain, "rev-parse", "--git-dir").Run() == nil {
		t.Fatalf("the test's temp directory %s is inside a git repository; a plain directory is the premise", plain)
	}
	f.env["HOME"] = home
	return &fleetEnv{fixture: f, home: home, plain: plain}
}

// mkdirPlain is another plain directory, for a second lane or a coordinator.
func (fe *fleetEnv) mkdirPlain(t *testing.T, name string) string {
	t.Helper()
	d := filepath.Join(filepath.Dir(fe.plain), name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

// initFleet creates fleet name through the verb and returns its ledger path.
func (fe *fleetEnv) initFleet(t *testing.T, name string) string {
	t.Helper()
	out, errw, code := fe.run(t, fe.plain, "", "init", "--fleet", name)
	if code != 0 {
		t.Fatalf("init --fleet %s: code %d\n%s%s", name, code, out, errw)
	}
	return filepath.Join(fe.home, ".buddy", "fleets", name, "buddy.db")
}

// hook runs one lifecycle hook verb for session id from cwd.
func (fe *fleetEnv) hook(t *testing.T, verb, id, cwd string) (string, string, int) {
	t.Helper()
	tool := ""
	if verb == "beat" {
		tool = "Bash"
	}
	return fe.run(t, cwd, hookJSON(id, cwd, tool, ""), verb)
}

// as runs a typed verb as session id (its native identity), from cwd.
func (fe *fleetEnv) as(t *testing.T, id, cwd string, args ...string) (out, errw string, code int) {
	t.Helper()
	fe.asSession(id, func() { out, errw, code = fe.run(t, cwd, "", args...) })
	return
}

// promptContext is busy's additionalContext, or "" when it wrote nothing.
func promptContext(t *testing.T, out string) string {
	t.Helper()
	if strings.TrimSpace(out) == "" {
		return ""
	}
	var v struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("busy output is not ONE hook JSON document: %q", out)
	}
	return v.HookSpecificOutput.AdditionalContext
}

// 5.2, row "variable unset": silent hooks and today's typed-verb exit, byte
// for byte; an empty value is the same verdict. The positive control is the
// same verbs with the variable naming a healthy fleet.
func TestFleetUnsetIsTodayByteForByte(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fe.initFleet(t, "inv")
	for _, v := range []string{"hello", "busy", "beat", "idle", "bye"} {
		for _, val := range []string{"", "unset"} {
			if val == "unset" {
				delete(fe.env, EnvFleet)
			} else {
				fe.env[EnvFleet] = val
			}
			out, errw, code := fe.hook(t, v, "lane-1", fe.plain)
			if code != 0 || out != "" || errw != "" {
				t.Fatalf("%s with BUDDY_FLEET %q: code %d stdout %q stderr %q, want silence (N0)", v, val, code, out, errw)
			}
		}
	}
	var want string
	for i, val := range []string{"unset", ""} {
		if val == "unset" {
			delete(fe.env, EnvFleet)
		} else {
			fe.env[EnvFleet] = val
		}
		_, errw, code := fe.as(t, "lane-1", fe.plain, "status")
		if code != 1 || !strings.Contains(errw, "not a git repository") {
			t.Fatalf("status with BUDDY_FLEET %q: code %d %q, want today's exit 1", val, code, errw)
		}
		if i == 0 {
			want = errw
		} else if errw != want {
			t.Fatalf("an empty BUDDY_FLEET changed the words:\n%q\nwant\n%q", errw, want)
		}
	}
	// In a repository never `buddy init`ed, the words still name its ledger
	// path (Codex design pass, Q5): the fleet's fallback must not turn "no
	// ledger at" into "not a git repository".
	fe.env[EnvFleet] = ""
	if _, errw, code := fe.as(t, "lane-1", fe.repo, "status"); code != 1 || !strings.Contains(errw, "no ledger at") {
		t.Fatalf("a never-inited repository: code %d %q, want \"no ledger at\"", code, errw)
	}
	// Positive control: the same verbs with a healthy fleet named.
	fe.env[EnvFleet] = "inv"
	if out, _, code := fe.hook(t, "hello", "lane-1", fe.plain); code != 0 || !strings.Contains(out, "in fleet inv") {
		t.Fatalf("control: hello in a healthy fleet: code %d %q", code, out)
	}
	if out, errw, code := fe.as(t, "lane-1", fe.plain, "status"); code != 0 || !strings.Contains(out, "LEDGER       fleet inv") {
		t.Fatalf("control: status in a healthy fleet: code %d %q %q", code, out, errw)
	}
}

// 5.2, row "a home ledger exists here", as stage 2 reads it (D-193): the home
// ledger is still the session's own and the typed verbs' (a file claim is
// granted there), and hello joins the fleet beside it, saying so. The control
// is the same hello with the variable unset: no fleet line, no LEDGER line.
func TestFleetHomeStaysTheVerbsLedger(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fe.initFleet(t, "inv")
	if _, errw, code := fe.run(t, fe.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	out, _, code := fe.hook(t, "hello", "sess-a", fe.repo)
	if code != 0 || strings.Contains(out, "is not used here") {
		t.Fatalf("control: no BUDDY_FLEET, no notice: %q", out)
	}
	// With the variable unset, status and who are today's reports, byte for
	// byte: no LEDGER line at all (5.2, step 3).
	for _, args := range [][]string{{"status"}, {"who", "sess-a"}} {
		if out, errw, code := fe.as(t, "sess-a", fe.repo, args...); code != 0 || strings.Contains(out, "LEDGER") {
			t.Fatalf("control: %v with BUDDY_FLEET unset must print no LEDGER line: code %d %q %q", args, code, out, errw)
		}
	}
	fe.env[EnvFleet] = "inv"
	out, _, code = fe.hook(t, "hello", "sess-b", fe.repo)
	// Stage 2 (D-193): the home ledger is still the session's own, its first
	// line the home's, and the fleet is joined beside it, not instead.
	if code != 0 || !strings.HasPrefix(out, "BUDDY: you are session ") || !strings.Contains(out, "BUDDY: also session ") {
		t.Fatalf("hello in a governed repository with BUDDY_FLEET set: code %d %q", code, out)
	}
	if strings.Contains(out, "is not used here") || strings.Contains(out, ") in fleet inv: a coordination-only") {
		t.Fatalf("a home ledger's digest must not call itself the fleet's, nor say the fleet is unused: %q", out)
	}
	// Registered in the home ledger, and in the fleet's beside it.
	if out, _, _ := fe.as(t, "sess-b", fe.repo, "sessions"); !strings.Contains(out, "sess-b") {
		t.Fatalf("sess-b missing from the home roster: %q", out)
	}
	fleetRoster, _, _ := fe.as(t, "sess-b", fe.plain, "sessions")
	if !strings.Contains(fleetRoster, "sess-b") {
		t.Fatalf("sess-b missing from the fleet's roster: %q", fleetRoster)
	}
	// The typed verbs: the LEDGER line names the home ledger first, and a
	// file claim (refused in a fleet) is granted, because this is the home.
	out, _, code = fe.as(t, "sess-b", fe.repo, "status")
	if code != 0 || !strings.HasPrefix(out, "LEDGER       ") || !strings.Contains(out, "this directory's own (a bare verb acts here); the hooks also report to fleet inv") {
		t.Fatalf("status's first line must name the home ledger: %q", out)
	}
	if _, errw, code := fe.as(t, "sess-b", fe.repo, "claim", "notes", "--desc", "d", "--scope", "notes.md"); code != 0 {
		t.Fatalf("a file claim in the home ledger: %q", errw)
	}
}

// 5.2, row "set, the fleet's ledger opens", and section 6's working verbs: a
// coordinator and a lane in two plain directories, the question and the
// answer, the roster, the idle report, the outcome, bye.
func TestFleetOpensAndCoordinates(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fe.initFleet(t, "inv")
	fe.env[EnvFleet] = "inv"
	orc := fe.mkdirPlain(t, "orc")
	fe.env[EnvLabel] = "orc"
	out, _, code := fe.hook(t, "hello", "orc-1", orc)
	if code != 0 || !strings.Contains(out, "you are session orc (orc-1) in fleet inv") {
		t.Fatalf("coordinator's hello: code %d %q", code, out)
	}
	if strings.Contains(out, "chat_read") {
		t.Fatalf("a fleet digest carries no chat line: %q", out)
	}
	fe.env[EnvLabel] = "i01"
	if _, _, code := fe.hook(t, "hello", "lane-1", fe.plain); code != 0 {
		t.Fatal("lane hello")
	}
	delete(fe.env, EnvLabel)
	if _, errw, code := fe.as(t, "orc-1", orc, "claim", "orc-inv", "--desc", "coordinator", "--scope", ".buddy/slot/orc-inv"); code != 0 {
		t.Fatalf("orc claims its slot: %q", errw)
	}
	if _, errw, code := fe.as(t, "lane-1", fe.plain, "claim", "i01", "--desc", "item", "--scope", ".buddy/slot/i01"); code != 0 {
		t.Fatalf("lane claims its item: %q", errw)
	}
	if _, errw, code := fe.as(t, "orc-1", orc, "wait", "--on", "i01", "--until", "2h", "--note", "watching"); code != 0 {
		t.Fatalf("orc waits on the item: %q", errw)
	}
	// The question: a send to the coordinator's slug, drained by its beat.
	if _, errw, code := fe.as(t, "lane-1", fe.plain, "msg", "orc-inv", "NEED INPUT: which column?"); code != 0 {
		t.Fatalf("lane asks: %q", errw)
	}
	if _, errw, code := fe.as(t, "lane-1", fe.plain, "wait", "--until", "2h", "--note", "NEED INPUT: column"); code != 0 {
		t.Fatalf("lane waits: %q", errw)
	}
	out, _, _ = fe.hook(t, "beat", "orc-1", orc)
	if !strings.Contains(promptContext(t, out), "NEED INPUT: which column?") {
		t.Fatalf("the coordinator's beat must drain the question: %q", out)
	}
	// The answer, drained by the lane's busy (the prompt that opens its turn).
	if _, errw, code := fe.as(t, "orc-1", orc, "msg", "i01", "the operator, in my pane: posted_at"); code != 0 {
		t.Fatalf("orc answers: %q", errw)
	}
	out, _, code = fe.hook(t, "busy", "lane-1", fe.plain)
	if code != 0 || !strings.Contains(promptContext(t, out), "posted_at") {
		t.Fatalf("the lane's busy must drain the answer: code %d %q", code, out)
	}
	// What the coordinator reads.
	out, _, code = fe.as(t, "orc-1", orc, "wait", "ls")
	if code != 0 || !strings.Contains(out, "NEED INPUT: column") {
		t.Fatalf("wait ls: %q", out)
	}
	if _, _, code := fe.hook(t, "idle", "lane-1", fe.plain); code != 0 {
		t.Fatal("idle")
	}
	out, _, _ = fe.as(t, "orc-1", orc, "sessions")
	if !strings.Contains(out, "i01") || !strings.Contains(out, "idle") {
		t.Fatalf("the roster must show the lane idle: %q", out)
	}
	out, _, code = fe.as(t, "orc-1", orc, "who", "i01")
	if code != 0 || !strings.Contains(out, "BASE         not kept in a fleet ledger") || !strings.HasPrefix(out, "LEDGER       fleet inv: ") {
		t.Fatalf("who in a fleet: code %d %q", code, out)
	}
	// Done: the outcome reaches the coordinator's check.
	if _, errw, code := fe.as(t, "lane-1", fe.plain, "release", "i01", "--outcome", "pass", "--note", "DONE: findings.md"); code != 0 {
		t.Fatalf("lane releases with an outcome: %q", errw)
	}
	out, _, _ = fe.as(t, "orc-1", orc, "wait", "check")
	if !strings.Contains(out, "LANDED") || !strings.Contains(out, "DONE: findings.md") {
		t.Fatalf("the coordinator's check must carry the outcome: %q", out)
	}
	if out, _, code := fe.as(t, "orc-1", orc, "ls", "--all"); code != 0 || !strings.Contains(out, "i01") {
		t.Fatalf("ls --all: %q", out)
	}
	if _, errw, code := fe.as(t, "orc-1", orc, "ids", "seed", "finding", "0"); code != 0 {
		t.Fatalf("ids seed: %q", errw)
	}
	if out, errw, code := fe.as(t, "lane-1", fe.plain, "ids", "take", "finding", "1"); code != 0 || !strings.Contains(out, "took finding 1..1") {
		t.Fatalf("ids take: %q %q", out, errw)
	}
	if out, errw, code := fe.as(t, "lane-1", fe.plain, "sent"); code != 0 || !strings.Contains(out, "#") {
		t.Fatalf("sent: %q %q", out, errw)
	}
	if _, errw, code := fe.as(t, "orc-1", orc, "sweep", "--dry-run"); code != 0 {
		t.Fatalf("sweep --dry-run: %q", errw)
	}
	// bye ends the lane's row.
	if _, _, code := fe.hook(t, "bye", "lane-1", fe.plain); code != 0 {
		t.Fatal("bye")
	}
	if out, _, _ := fe.as(t, "orc-1", orc, "who", "lane-1"); !strings.Contains(out, "ended") {
		t.Fatalf("bye must end the lane's row: %q", out)
	}
}

// fleetBreak is one way a named fleet does not open, and the row it lands in.
type fleetBreak struct {
	name  string
	val   string // BUDDY_FLEET
	setup func(t *testing.T, fe *fleetEnv)
	kind  fleetKind
	words string
}

func fleetBreaks() []fleetBreak {
	symlinkTo := func(t *testing.T, target, link string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	return []fleetBreak{
		{"upper case", "Inv", nil, fleetMalformed, "is not a fleet name"},
		{"a slash", "a/b", nil, fleetMalformed, "is not a fleet name"},
		{"a trailing space", "inv ", nil, fleetMalformed, "is not a fleet name"},
		{"dot dot", "..", nil, fleetMalformed, "is not a fleet name"},
		{"65 bytes", strings.Repeat("a", 65), nil, fleetMalformed, "is not a fleet name"},
		{"no fleet home", "inv", nil, fleetAbsent, "names no fleet ledger on this machine"},
		{"no such fleet", "nope", func(t *testing.T, fe *fleetEnv) { fe.initFleet(t, "other") }, fleetAbsent, "names no fleet ledger on this machine"},
		{"a directory with no ledger", "inv", func(t *testing.T, fe *fleetEnv) {
			if err := os.MkdirAll(filepath.Join(fe.home, ".buddy", "fleets", "inv"), 0o700); err != nil {
				t.Fatal(err)
			}
		}, fleetAbsent, "names no fleet ledger on this machine"},
		{"another HOME", "inv", func(t *testing.T, fe *fleetEnv) {
			fe.initFleet(t, "inv")
			fe.env["HOME"] = t.TempDir()
		}, fleetAbsent, "names no fleet ledger on this machine"},
		{"a corrupt file", "inv", func(t *testing.T, fe *fleetEnv) {
			p := fe.initFleet(t, "inv")
			os.Remove(p + "-wal")
			os.Remove(p + "-shm")
			if err := os.WriteFile(p, []byte(strings.Repeat("not a database ", 400)), 0o600); err != nil {
				t.Fatal(err)
			}
		}, fleetUnavailable, "fleet ledger unavailable ("},
		{"the fleet a symlink to a repository's git directory", "inv", func(t *testing.T, fe *fleetEnv) {
			if _, errw, code := fe.run(t, fe.repo, "", "init"); code != 0 {
				t.Fatal(errw)
			}
			symlinkTo(t, filepath.Join(fe.repo, ".git"), filepath.Join(fe.home, ".buddy", "fleets", "inv"))
		}, fleetUnavailable, "is a symlink"},
		{"the fleet home a symlink", "inv", func(t *testing.T, fe *fleetEnv) {
			real := t.TempDir()
			if err := os.MkdirAll(filepath.Join(real, "inv"), 0o700); err != nil {
				t.Fatal(err)
			}
			symlinkTo(t, real, filepath.Join(fe.home, ".buddy", "fleets"))
		}, fleetUnavailable, "is a symlink"},
		{"a dangling fleet symlink", "inv", func(t *testing.T, fe *fleetEnv) {
			symlinkTo(t, filepath.Join(fe.home, "gone"), filepath.Join(fe.home, ".buddy", "fleets", "inv"))
		}, fleetUnavailable, "is a symlink"},
		{"a dangling ~/.buddy", "inv", func(t *testing.T, fe *fleetEnv) {
			symlinkTo(t, filepath.Join(fe.home, "gone"), filepath.Join(fe.home, ".buddy"))
		}, fleetUnavailable, "does not resolve"},
		{"a git directory", "inv", func(t *testing.T, fe *fleetEnv) {
			p := fe.initFleet(t, "inv")
			if err := os.WriteFile(filepath.Join(filepath.Dir(p), "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, fleetUnavailable, "holds HEAD, the mark of a git directory"},
		{"the ledger a symlink", "inv", func(t *testing.T, fe *fleetEnv) {
			p := fe.initFleet(t, "inv")
			if err := os.Rename(p, p+".real"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(p+".real", p); err != nil {
				t.Fatal(err)
			}
		}, fleetUnavailable, "not a regular file"},
		{"the ledger a directory", "inv", func(t *testing.T, fe *fleetEnv) {
			if err := os.MkdirAll(filepath.Join(fe.home, ".buddy", "fleets", "inv", "buddy.db"), 0o700); err != nil {
				t.Fatal(err)
			}
		}, fleetUnavailable, "not a regular file"},
		{"a relative HOME", "inv", func(t *testing.T, fe *fleetEnv) { fe.env["HOME"] = "relative/home" }, fleetUnavailable, "not an absolute path"},
	}
}

// 5.2's three failure rows and 9.1's "mistyped name", "unreadable at the
// start" and "another HOME": hello says it in one line on stdout and exits 0;
// busy says it as its one document and exits 0; beat, idle and bye are silent
// for a malformed or absent fleet and exit 1 with the cause on stderr for an
// unavailable one; a typed verb exits 1 with the words (a malformed name ends
// `refused: ...`). The positive control, per row, is a healthy fleet in the
// same HOME: hello registers there.
func TestFleetLinesWhenItDoesNotOpen(t *testing.T) {
	for _, c := range fleetBreaks() {
		t.Run(c.name, func(t *testing.T) {
			boundedParallel(t)
			fe := newFleet(t)
			if c.setup != nil {
				c.setup(t, fe)
			}
			fe.env[EnvFleet] = c.val
			out, errw, code := fe.hook(t, "hello", "lane-1", fe.plain)
			if code != 0 || errw != "" || strings.Count(out, "\n") != 1 || !strings.HasPrefix(out, "BUDDY: ") || !strings.Contains(out, c.words) {
				t.Fatalf("hello: code %d stdout %q stderr %q, want one line naming %q", code, out, errw, c.words)
			}
			out, errw, code = fe.hook(t, "busy", "lane-1", fe.plain)
			if ctx := promptContext(t, out); code != 0 || errw != "" || !strings.Contains(ctx, c.words) {
				t.Fatalf("busy: code %d context %q stderr %q, want the same line", code, ctx, errw)
			}
			for _, v := range []string{"beat", "idle", "bye"} {
				out, errw, code := fe.hook(t, v, "lane-1", fe.plain)
				if c.kind == fleetUnavailable {
					if code != 1 || out != "" || !strings.Contains(errw, c.words) {
						t.Fatalf("%s on an unavailable fleet: code %d stdout %q stderr %q, want exit 1 with the cause", v, code, out, errw)
					}
				} else if code != 0 || out != "" || errw != "" {
					t.Fatalf("%s on a %s fleet: code %d stdout %q stderr %q, want silence", v, c.name, code, out, errw)
				}
			}
			for _, verb := range [][]string{{"status"}, {"msg", "all", "hi"}, {"claim", "x", "--desc", "d", "--scope", ".buddy/slot/x"}, {"wait", "check"}} {
				_, errw, code := fe.as(t, "lane-1", fe.plain, verb...)
				if code != 1 || !strings.Contains(errw, c.words) {
					t.Fatalf("%v: code %d %q, want exit 1 naming %q", verb, code, errw, c.words)
				}
				if c.kind == fleetMalformed && !strings.Contains(errw, "refused: ") {
					t.Fatalf("%v: a malformed name must read `refused:`: %q", verb, errw)
				}
			}
			// Positive control: a healthy fleet beside it, in this HOME.
			fe.env["HOME"] = fe.home
			if c.name == "a relative HOME" || c.name == "the fleet home a symlink" || c.name == "a dangling ~/.buddy" {
				fe.env["HOME"] = t.TempDir()
			}
			fe.initFleet(t, "ok")
			fe.env[EnvFleet] = "ok"
			if out, _, code := fe.hook(t, "hello", "lane-1", fe.plain); code != 0 || !strings.Contains(out, "in fleet ok") {
				t.Fatalf("control: a healthy fleet must register: code %d %q", code, out)
			}
		})
	}
}

// Codex design pass, finding 1: a fleet name that aliases a repository's own
// ledger through a symlink must not reach it. The control is the same release
// from inside the repository, which does release the claim.
func TestFleetNameCannotReachARepositorysLedger(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fe.initAndHello(t)
	mustClaim(t, fe.fixture, fe.repo, "peer", "sess-a", "dir1")
	if err := os.MkdirAll(filepath.Join(fe.home, ".buddy", "fleets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(fe.repo, ".git"), filepath.Join(fe.home, ".buddy", "fleets", "inv")); err != nil {
		t.Fatal(err)
	}
	fe.env[EnvFleet] = "inv"
	_, errw, code := fe.run(t, fe.plain, "", "release", "peer", "--session", "sess-a")
	if code != 1 || !strings.Contains(errw, "is a symlink") {
		t.Fatalf("release through an aliasing fleet: code %d %q, want refused", code, errw)
	}
	if out, _, _ := fe.run(t, fe.repo, "", "ls"); !strings.Contains(out, "peer") {
		t.Fatalf("the repository's claim must survive: %q", out)
	}
	if _, errw, code := fe.run(t, fe.plain, "", "init", "--fleet", "inv"); code != 1 || !strings.Contains(errw, "refused: init --fleet inv") || !strings.Contains(errw, "is a symlink") {
		t.Fatalf("init --fleet must not adopt the alias: code %d %q", code, errw)
	}
	// Control: from the repository the same release goes through.
	if _, errw, code := fe.run(t, fe.repo, "", "release", "peer", "--session", "sess-a"); code != 0 {
		t.Fatalf("control: %q", errw)
	}
}

// openNoTables makes path a SQLite file at the current schema's user_version
// with no tables: store.Open succeeds and the first query fails (Codex design
// pass, finding 2).
func openNoTables(t *testing.T, path string) {
	t.Helper()
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		os.Remove(p)
	}
	probe := filepath.Join(t.TempDir(), "probe.db")
	st, err := store.Open(probe, nil)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	pdb, err := sql.Open("sqlite", probe)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	if err := pdb.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	pdb.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA user_version = ` + itoa(v)); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// 5.2's unavailable row, reached AFTER the open: hello and busy still say it
// on stdout and exit 0, never the silent collapse. The control is the same
// fleet before it was emptied.
func TestFleetFailureAfterTheOpenIsSaid(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	p := fe.initFleet(t, "inv")
	fe.env[EnvFleet] = "inv"
	if out, _, code := fe.hook(t, "hello", "lane-1", fe.plain); code != 0 || !strings.Contains(out, "in fleet inv") {
		t.Fatalf("control: %q", out)
	}
	openNoTables(t, p)
	out, errw, code := fe.hook(t, "hello", "lane-1", fe.plain)
	if code != 0 || !strings.Contains(out, "BUDDY: fleet ledger error (") || !strings.Contains(out, "opened and then a read or write failed") || strings.Contains(out, "symlinks") || strings.Contains(out, "in fleet inv") {
		t.Fatalf("hello on a fleet that opens and then fails: code %d %q %q", code, out, errw)
	}
	out, errw, code = fe.hook(t, "busy", "lane-1", fe.plain)
	if ctx := promptContext(t, out); code != 0 || !strings.Contains(ctx, "fleet ledger error (") {
		t.Fatalf("busy on a fleet that opens and then fails: code %d %q %q", code, ctx, errw)
	}
}

// 9.1, "the fleet ledger unreadable or removed, later": busy says it at EVERY
// prompt while it lasts, and stops when it is back; no tool call is refused
// meanwhile (the gate's verdict is the unset run's).
func TestFleetBusyRepeatsTheLineWhileItLasts(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	p := fe.initFleet(t, "inv")
	fe.env[EnvFleet] = "inv"
	if _, _, code := fe.hook(t, "hello", "lane-1", fe.plain); code != 0 {
		t.Fatal("hello")
	}
	if out, _, _ := fe.hook(t, "busy", "lane-1", fe.plain); strings.Contains(promptContext(t, out), "BUDDY_FLEET") {
		t.Fatalf("control: a healthy fleet's busy says nothing of it: %q", out)
	}
	dir := filepath.Dir(p)
	if err := os.Rename(dir, dir+".away"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		out, _, code := fe.hook(t, "busy", "lane-1", fe.plain)
		if code != 0 || !strings.Contains(promptContext(t, out), "names no fleet ledger") {
			t.Fatalf("prompt %d after the fleet went away: code %d %q", i+1, code, out)
		}
		gate, _, _ := fe.run(t, fe.plain, hookJSON("lane-1", fe.plain, "Write", filepath.Join(fe.plain, "notes.md")), "gate")
		if gate != "" {
			t.Fatalf("no tool call is refused for a missing fleet: %q", gate)
		}
	}
	if err := os.Rename(dir+".away", dir); err != nil {
		t.Fatal(err)
	}
	if out, _, _ := fe.hook(t, "busy", "lane-1", fe.plain); strings.Contains(promptContext(t, out), "BUDDY_FLEET") {
		t.Fatalf("back again, busy is quiet: %q", out)
	}
	if err := os.WriteFile(p, []byte(strings.Repeat("garbage ", 600)), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Remove(p + "-wal")
	os.Remove(p + "-shm")
	if out, _, code := fe.hook(t, "busy", "lane-1", fe.plain); code != 0 || !strings.Contains(promptContext(t, out), "fleet ledger unavailable") {
		t.Fatalf("a fleet gone unreadable: code %d %q", code, out)
	}
}

// 9.1, "the lane's cwd becomes a directory with its own ledger", closed by
// stage 2 (D-193): its hooks beat the fleet beside the home, so its fleet mail
// is drained from inside the repository. The control is the same beat with
// the variable unset, which does not reach the fleet.
func TestFleetLaneInsideAGovernedRepoKeepsItsFleet(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fe.initFleet(t, "inv")
	if _, errw, code := fe.run(t, fe.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	fe.env[EnvFleet] = "inv"
	orc := fe.mkdirPlain(t, "orc")
	fe.hook(t, "hello", "orc-1", orc)
	fe.hook(t, "hello", "lane-1", fe.plain)
	if _, errw, code := fe.as(t, "orc-1", orc, "msg", "lane-1", "fleet mail"); code != 0 {
		t.Fatal(errw)
	}
	// The control: with the variable unset (as 1b did beside a home), the
	// beat from the repository does not reach the fleet.
	delete(fe.env, EnvFleet)
	out, _, code := fe.hook(t, "beat", "lane-1", fe.repo)
	if code != 0 || strings.Contains(out, "fleet mail") {
		t.Fatalf("control: unset, the fleet's mail is not drained: code %d %q", code, out)
	}
	fe.env[EnvFleet] = "inv"
	// Stage 2 closes the plan's 9.1 row (D-193): the lane that cd'd into a
	// governed repository still beats its fleet and drains its mail there.
	out, _, code = fe.hook(t, "beat", "lane-1", fe.repo)
	if code != 0 || !strings.Contains(promptContext(t, out), "fleet mail") {
		t.Fatalf("inside a governed repository the fleet's mail is drained: code %d %q", code, out)
	}
	if out, _, _ := fe.as(t, "orc-1", orc, "sent"); !strings.Contains(out, "1 of 1 with a recorded delivery") {
		t.Fatalf("the fleet message must be marked delivered: %q", out)
	}
}

// 9.1, "/clear in a lane" and "a label worn twice": a new id in the same
// environment is a new row; the old row's mail is stranded; and the label both
// wear is refused as ambiguous. The control is the full id, which resolves.
func TestFleetClearStrandsTheOldRowAndATwinLabelIsAmbiguous(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fe.initFleet(t, "inv")
	fe.env[EnvFleet] = "inv"
	orc := fe.mkdirPlain(t, "orc")
	fe.hook(t, "hello", "orc-1", orc)
	fe.env[EnvLabel] = "i01"
	fe.hook(t, "hello", "lane-1", fe.plain)
	if _, errw, code := fe.as(t, "orc-1", orc, "msg", "lane-1", "to the first id"); code != 0 {
		t.Fatal(errw)
	}
	out, _, _ := fe.hook(t, "hello", "lane-2", fe.plain) // /clear: a new id, same environment
	delete(fe.env, EnvLabel)
	if !strings.Contains(out, "also worn by 1 other live session") {
		t.Fatalf("the twin label is warned at hello: %q", out)
	}
	if strings.Contains(out, "to the first id") {
		t.Fatalf("the new id must not receive the old id's mail: %q", out)
	}
	// Control (the refusals lens, finding 4): the mail is not lost, only
	// stranded on the old row, which still holds it undelivered; without
	// this, "never delivered" and "never sent" read the same.
	if out, _, _ := fe.as(t, "orc-1", orc, "sent"); !strings.Contains(out, "0 of 1 with a recorded delivery") {
		t.Fatalf("the old id's message must still be held, undelivered: %q", out)
	}
	if _, errw, code := fe.as(t, "orc-1", orc, "msg", "i01", "hi"); code == 0 || !strings.Contains(errw, "ambiguous") {
		t.Fatalf("msg to a twin label: code %d %q, want refused as ambiguous", code, errw)
	}
	if _, errw, code := fe.as(t, "orc-1", orc, "msg", "lane-2", "hi"); code != 0 {
		t.Fatalf("control: the full id resolves: %q", errw)
	}
}

// 9.1, "the coordinator ends without a successor": a lane's next send to its
// slug says the holder has ended, or is refused once the claim is orphaned.
func TestFleetCoordinatorEndedIsSaidToTheLane(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fe.initFleet(t, "inv")
	fe.env[EnvFleet] = "inv"
	orc := fe.mkdirPlain(t, "orc")
	fe.hook(t, "hello", "orc-1", orc)
	fe.hook(t, "hello", "lane-1", fe.plain)
	if _, errw, code := fe.as(t, "orc-1", orc, "claim", "orc-inv", "--desc", "c", "--scope", ".buddy/slot/orc-inv"); code != 0 {
		t.Fatal(errw)
	}
	if out, errw, code := fe.as(t, "lane-1", fe.plain, "msg", "orc-inv", "q"); code != 0 || strings.Contains(out+errw, "ENDED") {
		t.Fatalf("control: a live coordinator: code %d %q %q", code, out, errw)
	}
	if _, _, code := fe.hook(t, "bye", "orc-1", orc); code != 0 {
		t.Fatal("bye")
	}
	out, errw, _ := fe.as(t, "lane-1", fe.plain, "msg", "orc-inv", "q2")
	if !strings.Contains(out+errw, "ENDED") {
		t.Fatalf("a send to an ended coordinator's slug must say so: %q %q", out, errw)
	}
}

// Section 6: claim takes slot scopes only, never gh-stack, never --shared or
// --based-on, each refusal beside its grant, the forecast refusing with the
// claim's words; and a refused claim writes nothing. Codex design pass, Q4's
// table is the scope list. The control for the file scope is the same claim
// in a repository's ledger.
func TestFleetClaimTakesSlotScopesOnly(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fe.initFleet(t, "inv")
	fe.env[EnvFleet] = "inv"
	fe.hook(t, "hello", "lane-1", fe.plain)
	claim := func(slug string, extra ...string) (string, string, int) {
		return fe.as(t, "lane-1", fe.plain, append([]string{"claim", slug, "--desc", "d"}, extra...)...)
	}
	for _, s := range []string{".buddy/slot/i01", ".Buddy/Slot/box"} {
		if _, errw, code := claim("g-"+strings.ToLower(filepath.Base(s)), "--scope", s); code != 0 {
			t.Fatalf("grant %s: %q", s, errw)
		}
	}
	if _, errw, code := claim("g-dry", "--scope", ".buddy/slot/dry", "--dry-run"); code != 0 {
		t.Fatalf("a good forecast: %q", errw)
	}
	for _, s := range []string{"notes.md", "/tmp/x", `\tmp\x`, ".buddy/slot/../x", ".buddy/slot/", ".buddy/slot", ".buddy",
		".Buddy/Slot/GH-STACK", `.buddy\slot\gh-stack`, ".buddy/slot/x/../gh-stack", ".buddy/slot/gh-stack", ".buddy/slot/gh-stack/sub"} {
		for _, dry := range []bool{false, true} {
			args := []string{"--scope", s}
			if dry {
				args = append(args, "--dry-run")
			}
			_, errw, code := claim("r-x", args...)
			if code != 1 || !strings.Contains(errw, "refused: scope") || !strings.Contains(errw, "in fleet inv") {
				t.Fatalf("scope %q (dry %v): code %d %q, want the fleet's refusal", s, dry, code, errw)
			}
		}
	}
	if _, errw, code := claim("r-shared", "--scope", ".buddy/slot/s", "--shared"); code != 1 || !strings.Contains(errw, "refused: --shared in fleet inv") {
		t.Fatalf("--shared: code %d %q", code, errw)
	}
	if _, errw, code := claim("r-based", "--scope", ".buddy/slot/b", "--based-on", "run1"); code != 1 || !strings.Contains(errw, "refused: --based-on in fleet inv") {
		t.Fatalf("--based-on: code %d %q", code, errw)
	}
	out, _, _ := fe.as(t, "lane-1", fe.plain, "ls")
	for _, bad := range []string{"r-x", "r-shared", "r-based", "g-dry"} {
		if strings.Contains(out, bad) {
			t.Fatalf("a refused claim or a forecast wrote a row %q: %q", bad, out)
		}
	}
	if !strings.Contains(out, "g-i01") || !strings.Contains(out, "g-box") {
		t.Fatalf("the grants are listed: %q", out)
	}
	// Control: a file scope in a repository's ledger is granted.
	fe.initAndHello(t)
	if _, errw, code := fe.run(t, fe.repo, "", "claim", "notes", "--desc", "d", "--scope", "notes.md", "--session", "sess-a"); code != 0 {
		t.Fatalf("control: %q", errw)
	}
}

// Section 6: pause and resume refuse in a fleet, and the roster cannot say
// PAUSED; whose and authority refuse; the control is each in a repository.
func TestFleetRefusesTheBrakeAndPaths(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fe.initFleet(t, "inv")
	fe.env[EnvFleet] = "inv"
	fe.env[EnvLabel] = "i01"
	fe.hook(t, "hello", "lane-1", fe.plain)
	delete(fe.env, EnvLabel)
	for _, c := range []struct {
		args  []string
		words string
	}{
		{[]string{"pause", "i01", "--note", "hold"}, "refused: pause in fleet inv: a fleet ledger carries no brake"},
		{[]string{"resume", "i01"}, "refused: resume in fleet inv: a fleet ledger carries no brake"},
		{[]string{"whose", "notes.md"}, "refused: whose in fleet inv: a fleet ledger keeps no paths"},
		{[]string{"authority"}, "refused: authority in fleet inv: a fleet ledger keeps no paths"},
		{[]string{"authority", "add", "notes.md"}, "refused: authority in fleet inv: a fleet ledger keeps no paths"},
	} {
		_, errw, code := fe.as(t, "lane-1", fe.plain, c.args...)
		if code != 1 || !strings.Contains(errw, c.words) {
			t.Fatalf("%v: code %d %q, want %q", c.args, code, errw, c.words)
		}
	}
	if out, _, _ := fe.as(t, "lane-1", fe.plain, "sessions"); strings.Contains(out, "PAUSED") {
		t.Fatalf("the roster must not say PAUSED: %q", out)
	}
	// Controls, in a repository's ledger.
	fe.initAndHello(t)
	for _, args := range [][]string{{"pause", "bravo", "--note", "hold"}, {"resume", "bravo"}, {"whose", "README"}, {"authority"}} {
		if _, errw, code := fe.run(t, fe.repo, "", args...); code != 0 {
			t.Fatalf("control %v in a repository: %q", args, errw)
		}
	}
}

// Section 6: wait --ready refuses in a fleet even where git WOULD resolve the
// commit (the cwd a repository never `buddy init`ed, 5.2's second errNoLedger
// case); the grant beside it is a wait on the run with a READY note, which
// LANDS on the run's outcome.
func TestFleetWaitReadyRefusedNoteGranted(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fe.initFleet(t, "inv")
	fe.env[EnvFleet] = "inv"
	// fe.repo is a repository with a commit and no ledger: the lane lives there.
	fe.hook(t, "hello", "lane-1", fe.repo)
	orc := fe.mkdirPlain(t, "orc")
	fe.hook(t, "hello", "orc-1", orc)
	if _, errw, code := fe.as(t, "orc-1", orc, "claim", "r1", "--desc", "FORMING", "--scope", ".buddy/slot/r1"); code != 0 {
		t.Fatal(errw)
	}
	_, errw, code := fe.as(t, "lane-1", fe.repo, "wait", "--on", "r1", "--ready", "HEAD")
	if code != 1 || !strings.Contains(errw, "refused: --ready in fleet inv") {
		t.Fatalf("wait --ready in a fleet: code %d %q", code, errw)
	}
	if _, errw, code := fe.as(t, "lane-1", fe.repo, "wait", "--on", "r1", "--until", "4h", "--note", "READY: findings.md v1"); code != 0 {
		t.Fatalf("the grant: %q", errw)
	}
	if _, errw, code := fe.as(t, "orc-1", orc, "release", "r1", "--outcome", "pass", "--note", "read"); code != 0 {
		t.Fatal(errw)
	}
	if out, _, _ := fe.as(t, "lane-1", fe.repo, "wait", "check"); !strings.Contains(out, "LANDED") {
		t.Fatalf("the note wait lands: %q", out)
	}
}

// Section 6: parking is refused in a fleet, beside the hand-on and the
// outcome, which work.
func TestFleetReleaseNeverParks(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fe.initFleet(t, "inv")
	fe.env[EnvFleet] = "inv"
	orc := fe.mkdirPlain(t, "orc")
	fe.hook(t, "hello", "orc-1", orc)
	fe.hook(t, "hello", "lane-1", fe.plain)
	fe.as(t, "orc-1", orc, "claim", "r1", "--desc", "run", "--scope", ".buddy/slot/r1")
	fe.as(t, "lane-1", fe.plain, "claim", "i01", "--desc", "item", "--scope", ".buddy/slot/i01")
	_, errw, code := fe.as(t, "lane-1", fe.plain, "release", "i01", "--to", "orc-1", "--in-run", "r1")
	if code != 1 || !strings.Contains(errw, "refused: parking (--in-run, --unpark) in fleet inv") {
		t.Fatalf("park in a fleet: code %d %q", code, errw)
	}
	if _, errw, code := fe.as(t, "lane-1", fe.plain, "release", "i01", "--unpark"); code != 1 || !strings.Contains(errw, "refused: parking") {
		t.Fatalf("--unpark in a fleet: code %d %q", code, errw)
	}
	if out, errw, code := fe.as(t, "lane-1", fe.plain, "release", "i01", "--to", "orc-1"); code != 0 || !strings.Contains(out, "handed \"i01\"") {
		t.Fatalf("hand-on in a fleet: code %d %q %q", code, out, errw)
	}
	if _, errw, code := fe.as(t, "orc-1", orc, "release", "i01", "--outcome", "fail", "--note", "DONE: could not reproduce"); code != 0 {
		t.Fatalf("outcome in a fleet: %q", errw)
	}
}

// `buddy init --fleet <name>`: creates both directories 0700 and the ledger,
// says it again without harm, tightens a wider fleet home and says so, and
// refuses a malformed name, an empty one, a stray argument, an unknown flag and
// a relative HOME, each creating nothing.
func TestInitFleet(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fleets := filepath.Join(fe.home, ".buddy", "fleets")
	if err := os.MkdirAll(fleets, 0o755); err != nil {
		t.Fatal(err)
	}
	out, errw, code := fe.run(t, fe.plain, "", "init", "--fleet", "inv-1115")
	if code != 0 || !strings.Contains(out, "fleet ledger ready: "+filepath.Join(fleets, "inv-1115", "buddy.db")) ||
		!strings.Contains(out, "--env BUDDY_FLEET=inv-1115") || !strings.Contains(out, "tightened "+fleets+" from mode 0755 to 0700") {
		t.Fatalf("init --fleet: code %d %q %q", code, out, errw)
	}
	for _, d := range []string{fleets, filepath.Join(fleets, "inv-1115")} {
		fi, err := os.Stat(d)
		if err != nil || fi.Mode().Perm() != 0o700 {
			t.Fatalf("%s: mode %v err %v, want 0700", d, fi.Mode().Perm(), err)
		}
	}
	if out, _, code := fe.run(t, fe.plain, "", "init", "--fleet", "inv-1115"); code != 0 || !strings.Contains(out, "fleet ledger already there") {
		t.Fatalf("again: code %d %q", code, out)
	}
	if out, _, code := fe.run(t, fe.plain, "", "init", "--fleet=inv-b"); code != 0 || !strings.Contains(out, "fleet ledger ready") {
		t.Fatalf("--fleet=<name>: code %d %q", code, out)
	}
	for _, c := range []struct {
		args  []string
		words string
	}{
		{[]string{"init", "--fleet", "Inv"}, `refused: "Inv" is not a fleet name`},
		{[]string{"init", "--fleet", "../x"}, `refused: "../x" is not a fleet name`},
		{[]string{"init", "--fleet", ""}, "refused: --fleet needs a name"},
		{[]string{"init", "--fleet", "x", "extra"}, "refused: init takes no further arguments"},
		{[]string{"init", "--bogus"}, "refused: flag provided but not defined: -bogus"},
		{[]string{"init", "now"}, `refused: init takes no arguments, got "now"`},
	} {
		_, errw, code := fe.run(t, fe.plain, "", c.args...)
		if code != 1 || !strings.Contains(errw, c.words) {
			t.Fatalf("%v: code %d %q, want %q", c.args, code, errw, c.words)
		}
	}
	entries, _ := os.ReadDir(fleets)
	if len(entries) != 2 {
		t.Fatalf("a refused init created something: %v", entries)
	}
	fe.env["HOME"] = "rel"
	if _, errw, code := fe.run(t, fe.plain, "", "init", "--fleet", "x"); code != 1 || !strings.Contains(errw, "not an absolute path") {
		t.Fatalf("relative HOME: code %d %q", code, errw)
	}
	// init --fleet is not discovery: from inside a governed repository it
	// still makes the fleet, and the repository's ledger is untouched.
	fe.env["HOME"] = fe.home
	if out, _, code := fe.run(t, fe.repo, "", "init", "--fleet", "from-repo"); code != 0 || !strings.Contains(out, "fleet ledger ready") {
		t.Fatalf("init --fleet from a repository: code %d %q", code, out)
	}
	if _, err := os.Lstat(filepath.Join(fe.repo, ".git", "buddy.db")); err == nil {
		t.Fatalf("init --fleet created a repository ledger")
	}
	// init adopts nothing a lookup would refuse: a fleet's directory holding
	// a HEAD (a git directory) and a buddy.db that is a symlink are refused
	// with the lookup's own words, and nothing is created through either.
	// The grant beside them is "from-repo" above.
	headDir := filepath.Join(fleets, "has-head")
	if err := os.MkdirAll(headDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(headDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errw, code := fe.run(t, fe.plain, "", "init", "--fleet", "has-head"); code != 1 || !strings.Contains(errw, "refused: init --fleet has-head") || !strings.Contains(errw, "holds HEAD, the mark of a git directory") {
		t.Fatalf("init over a git directory: code %d %q", code, errw)
	}
	if _, err := os.Lstat(filepath.Join(headDir, "buddy.db")); err == nil {
		t.Fatalf("init created a ledger in a directory holding HEAD")
	}
	linkDir := filepath.Join(fleets, "linked")
	if err := os.MkdirAll(linkDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "elsewhere.db")
	if err := os.Symlink(target, filepath.Join(linkDir, "buddy.db")); err != nil {
		t.Fatal(err)
	}
	if _, errw, code := fe.run(t, fe.plain, "", "init", "--fleet", "linked"); code != 1 || !strings.Contains(errw, "is not a regular file") {
		t.Fatalf("init over a symlinked buddy.db: code %d %q", code, errw)
	}
	if _, err := os.Lstat(target); err == nil {
		t.Fatalf("init created a ledger through a symlink")
	}
}

// 9.1, "git's variables in a typed verb's environment", decided in the Codex
// design pass: with BUDDY_FLEET set, a typed verb discovers as the session's
// hooks do, from the filesystem, so a GIT_DIR naming a governed repository
// cannot make `ls` read that repository while the hooks use the fleet. The
// control is the same verb with BUDDY_FLEET unset, which still inherits
// (D-137's recorded open), and the environment is restored after either.
//
// Not parallel: discovery reads the process environment (t.Setenv).
func TestFleetTypedVerbsDiscoverAsTheHooksDo(t *testing.T) {
	fe := newFleet(t)
	fe.initAndHello(t)
	mustClaim(t, fe.fixture, fe.repo, "repo-claim", "sess-a", "dir1")
	fe.initFleet(t, "inv")
	t.Setenv("GIT_DIR", filepath.Join(fe.repo, ".git"))
	t.Setenv("GIT_WORK_TREE", fe.repo)
	out, _, code := fe.run(t, fe.plain, "", "ls")
	if code != 0 || !strings.Contains(out, "repo-claim") {
		t.Fatalf("control: unset, a typed verb inherits GIT_DIR (D-137's open): code %d %q", code, out)
	}
	fe.env[EnvFleet] = "inv"
	fe.hook(t, "hello", "lane-1", fe.plain)
	fe.as(t, "lane-1", fe.plain, "claim", "i01", "--desc", "d", "--scope", ".buddy/slot/i01")
	out, _, code = fe.run(t, fe.plain, "", "ls")
	if code != 0 || strings.Contains(out, "repo-claim") || !strings.Contains(out, "i01") {
		t.Fatalf("BUDDY_FLEET set: ls must read the fleet the hooks read: code %d %q", code, out)
	}
	if os.Getenv("GIT_DIR") == "" {
		t.Fatalf("the scrub must restore the environment for an in-process caller")
	}
}

// gateCase is one gate call and the verdict it must give with no fleet named.
type gateCase struct {
	name, cwd, stdin string
	deny             bool
}

// 5.4: the gate's verdicts, in a repository and outside one, byte-identical
// with BUDDY_FLEET unset, naming no fleet, naming a healthy fleet that holds a
// slot claim, queued mail and (written past the CLI, as a later bug might) a
// pause and a file claim for the very sessions and paths the cases use, and
// naming a corrupt file; each DENY beside its ALLOW.
func TestGateVerdictsIgnoreTheFleet(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fe.initAndHello(t)
	dir1 := filepath.Join(fe.repo, "dir1")
	if err := os.MkdirAll(dir1, 0o755); err != nil {
		t.Fatal(err)
	}
	mustClaim(t, fe.fixture, fe.repo, "peer", "sess-a", "dir1")
	fe.run(t, fe.repo, hookJSON("sess-c", fe.repo, "", ""), "hello", "--label", "charlie")
	if _, errw, code := fe.run(t, fe.repo, "", "pause", "charlie", "--note", "hold"); code != 0 {
		t.Fatal(errw)
	}
	cases := []gateCase{
		{"repo: a write inside a peer's claim", dir1, hookJSON("sess-b", dir1, "Write", filepath.Join(dir1, "x.go")), true},
		{"repo: a write nobody claims", fe.repo, hookJSON("sess-b", fe.repo, "Write", filepath.Join(fe.repo, "free.md")), false},
		{"repo: a paused session's Bash", fe.repo, bashHookJSON("sess-c", fe.repo, "echo x > y"), true},
		{"repo: an unpaused session's Bash", fe.repo, bashHookJSON("sess-b", fe.repo, "echo x > y"), false},
		{"plain: a write in the lane's own directory", fe.plain, hookJSON("lane-1", fe.plain, "Write", filepath.Join(fe.plain, "notes.md")), false},
		{"plain: a second lane writes the same file", fe.plain, hookJSON("lane-2", fe.plain, "Write", filepath.Join(fe.plain, "notes.md")), false},
		{"plain: a write into a peer's claim in the repository", fe.plain, hookJSON("lane-1", fe.plain, "Write", filepath.Join(dir1, "x.go")), true},
		{"plain: a Bash that cds into the repository and patches", fe.plain, bashHookJSON("lane-1", fe.plain, "cd "+fe.repo+" && apply_patch <<'EOF'\n*** Begin Patch\n*** Add File: x.go\n+x\n*** End Patch\nEOF\n"), true},
	}
	run := func() []string {
		outs := make([]string, len(cases))
		for i, c := range cases {
			out, errw, code := fe.run(t, c.cwd, c.stdin, "gate")
			outs[i] = out + "\x00" + errw + "\x00" + itoa(code)
		}
		return outs
	}
	delete(fe.env, EnvFleet)
	want := run()
	for i, c := range cases {
		out := strings.SplitN(want[i], "\x00", 2)[0]
		if _, denied := decodeDeny(t, out); denied != c.deny {
			t.Fatalf("control, no fleet: %s: denied=%v want %v (%q)", c.name, denied, c.deny, out)
		}
	}
	// A healthy fleet holding everything a gate could act on, were it read.
	p := fe.initFleet(t, "inv")
	st, err := store.Open(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"lane-1", "lane-2", "sess-b"} {
		si, err := st.HelloFrom(id, id, fe.plain, store.ProcRef{}, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Claim(id, si.Incarnation, "slot-"+id, "d", []string{".buddy/slot/" + id}); err != nil {
			t.Fatal(err)
		}
		tgt, err := st.ResolveTarget(id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Pause(tgt, "a pause no gate may read"); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Send(tgt, "x", "queued mail", store.SendOpts{}); err != nil {
			t.Fatal(err)
		}
	}
	si, _, _ := st.SessionByID("lane-1")
	if err := st.Claim("lane-1", si.Incarnation, "file", "d", []string{"free.md", "notes.md", "x.go"}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	if err := os.MkdirAll(filepath.Join(fe.home, ".buddy", "fleets", "bad"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fe.home, ".buddy", "fleets", "bad", "buddy.db"), []byte(strings.Repeat("corrupt ", 600)), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, val := range []string{"", "nope", "inv", "bad", "Bad Name"} {
		fe.env[EnvFleet] = val
		got := run()
		for i, c := range cases {
			if got[i] != want[i] {
				t.Fatalf("BUDDY_FLEET=%q changed the gate's verdict on %s:\n%q\nwant\n%q", val, c.name, got[i], want[i])
			}
		}
	}
}

// The gate is never a fleet verb, and fleetScrub never names it: Run scrubs
// for the gate because it is a hook verb, whatever BUDDY_FLEET says.
func TestFleetVerbSetIsBounded(t *testing.T) {
	boundedParallel(t)
	for _, v := range []string{"gate", "commit-gate", "lifecycle", "adapter", "diagnose", "init", "update", "uninstall", "hello", "beat", "busy", "idle", "bye"} {
		if fleetVerbs[v] {
			t.Fatalf("%s must not be a section-6 fleet verb", v)
		}
	}
	for v := range fleetVerbs {
		if _, ok := verbs[v]; !ok {
			t.Fatalf("fleetVerbs names %q, which is no verb", v)
		}
	}
	env := Env{Getenv: func(k string) string { return map[string]string{EnvFleet: "inv"}[k] }}
	if fleetScrub("commit-gate", env) || !fleetScrub("msg", env) || fleetScrub("msg", Env{Getenv: func(string) string { return "" }}) {
		t.Fatal("fleetScrub: commit-gate never, msg only with BUDDY_FLEET set")
	}
}

// Section 6's skips that leave rows behind if broken: in a fleet, idle samples
// no base and hello takes no authority baseline, even where the lane's cwd is
// a repository never `buddy init`ed whose HEAD resolves and which holds a
// watched CLAUDE.md (5.2's second errNoLedger case: the HEAD would be recorded
// as the base of a session coordinating elsewhere, and the watch is off). The
// positive control is the same steps in the same repository once it has its
// own ledger: both rows are written there, so "none" is not "never ran".
func TestFleetKeepsNoBaseAndNoAuthorityRows(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	ledger := fe.initFleet(t, "inv")
	if err := os.WriteFile(filepath.Join(fe.repo, "CLAUDE.md"), []byte("rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rows := func(t *testing.T, path, id string) (base, auth bool) {
		t.Helper()
		st, err := store.Open(path, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		si, known, err := st.SessionByID(id)
		if err != nil || !known {
			t.Fatalf("session %s is not on %s's roster (known=%v, %v): the steps never ran", id, path, known, err)
		}
		bases, err := st.Bases()
		if err != nil {
			t.Fatal(err)
		}
		_, base = bases[id]
		_, auth, err = st.AuthorityLast(id, si.Incarnation, "CLAUDE.md")
		if err != nil {
			t.Fatal(err)
		}
		return base, auth
	}
	fe.env[EnvFleet] = "inv"
	if out, _, code := fe.hook(t, "hello", "lane-1", fe.repo); code != 0 || !strings.Contains(out, "in fleet inv") {
		t.Fatalf("hello in a fleet from a never-inited repository: code %d %q", code, out)
	}
	stopIn(t, fe.fixture, "lane-1", fe.repo)
	if base, auth := rows(t, ledger, "lane-1"); base || auth {
		t.Fatalf("a fleet session recorded base=%v authority=%v; a fleet keeps neither (D-191)", base, auth)
	}
	// Control: the repository's own ledger, the variable unset.
	delete(fe.env, EnvFleet)
	if _, errw, code := fe.run(t, fe.repo, "", "init"); code != 0 {
		t.Fatalf("init: %s", errw)
	}
	if _, _, code := fe.hook(t, "hello", "lane-2", fe.repo); code != 0 {
		t.Fatal("control hello")
	}
	stopIn(t, fe.fixture, "lane-2", fe.repo)
	common := strings.TrimSpace(gitIn(t, fe.repo, "rev-parse", "--path-format=absolute", "--git-common-dir"))
	if base, auth := rows(t, filepath.Join(common, "buddy.db"), "lane-2"); !base || !auth {
		t.Fatalf("control: the home ledger recorded base=%v authority=%v, want both; the fleet's none proves nothing", base, auth)
	}
}

// 5.2's unavailable row through explainLedgerAccess (Codex design pass,
// finding 2): a fleet ledger this process may not write names its cause and
// fix, and says where it lives (the fleet home, not the git common
// directory), instead of the bare SQLite code. File modes stand in for the
// Codex sandbox, as in ledgeraccess_test.go. The positive control is the same
// verb against the writable fleet ledger: it succeeds and says nothing about
// access, so the text is caused by the lock.
func TestFleetLedgerAccessNamesTheFleetHome(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	ledger := fe.initFleet(t, "inv")
	fe.env[EnvFleet] = "inv"
	fe.hook(t, "hello", "lane-1", fe.plain)
	if _, errw, code := fe.as(t, "lane-1", fe.plain, "status"); code != 0 || strings.Contains(errw, ledgerCause) {
		t.Fatalf("control on a writable fleet ledger: %d %q", code, errw)
	}
	dir := filepath.Dir(ledger)
	if err := os.Chmod(ledger, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(ledger, 0o644) })
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if fh, err := os.OpenFile(ledger, os.O_RDWR, 0); err == nil {
		fh.Close()
		t.Skip("file modes do not refuse this process a write")
	}
	// From a plain directory, and from a repository never `buddy init`ed,
	// where resolveRepo succeeds and the verb still fell back to the fleet
	// (Codex code pass, finding 5: that cwd got the bare SQLite code).
	for _, cwd := range []string{fe.plain, fe.repo} {
		_, errw, code := fe.as(t, "lane-1", cwd, "status")
		if code != 1 {
			t.Fatalf("%s: an unwritable fleet ledger must fail the verb: code %d %q", cwd, code, errw)
		}
		for _, w := range []string{"fleet ledger unavailable", ledgerCause, ledger, "It lives in the fleet home under $HOME", "require_escalated"} {
			if !strings.Contains(errw, w) {
				t.Errorf("%s: stderr lacks %q:\n%s", cwd, w, errw)
			}
		}
		if strings.Contains(errw, "git common directory") {
			t.Errorf("%s: a fleet ledger's explanation must not send the caller to the git common directory:\n%s", cwd, errw)
		}
	}
	// A lifecycle hook opens through openSessionLedger too, so its failure on
	// the fleet ledger names it (stderr: a hand run, or a log, reads it).
	if _, errw, _ := fe.hook(t, "beat", "lane-1", fe.plain); !strings.Contains(errw, "It lives in the fleet home under $HOME") {
		t.Errorf("beat on an unwritable fleet ledger must name the fleet home:\n%s", errw)
	}
	// The Fable cross-check, finding 5: `init` is no fleet verb, so when it
	// fails creating the REPOSITORY's ledger (its git directory unwritable)
	// the explanation must not name the fleet's, which it never opened.
	gitDir := filepath.Join(fe.repo, ".git")
	if err := os.Chmod(gitDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(gitDir, 0o755) })
	_, errw, code := fe.as(t, "lane-1", fe.repo, "init")
	if code == 0 {
		t.Fatalf("premise: init in an unwritable git directory must fail: %q", errw)
	}
	if strings.Contains(errw, ledger) || strings.Contains(errw, "fleet home") {
		t.Fatalf("init's failure names the fleet ledger it never opened:\n%s", errw)
	}
}

// The Fable cross-check, finding 1 (measured there): SQLite opens buddy.db's
// sidecars by name, so a -wal, -shm or -journal with a second name (a hard
// link to a repository's) or a symlink is the alias the ledger's own checks
// stop. Each refuses; the control is the same fleet with the sidecar gone.
func TestFleetSidecarWithASecondNameIsRefused(t *testing.T) {
	boundedParallel(t)
	for _, side := range []string{"-wal", "-shm", "-journal"} {
		t.Run(side, func(t *testing.T) {
			fe := newFleet(t)
			ledger := fe.initFleet(t, "inv")
			fe.env[EnvFleet] = "inv"
			other := filepath.Join(t.TempDir(), "elsewhere")
			if err := os.WriteFile(other, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(other, ledger+side); err != nil {
				t.Skipf("no hard links here: %v", err)
			}
			if out, _, _ := fe.hook(t, "hello", "lane-1", fe.plain); !strings.Contains(out, "fleet ledger unavailable") || !strings.Contains(out, "buddy.db"+side+" has 2 names") {
				t.Fatalf("a hard-linked %s: %q", side, out)
			}
			os.Remove(ledger + side)
			if err := os.Symlink(other, ledger+side); err != nil {
				t.Fatal(err)
			}
			if out, _, _ := fe.hook(t, "hello", "lane-1", fe.plain); !strings.Contains(out, "buddy.db"+side+" is not a regular file") {
				t.Fatalf("a symlinked %s: %q", side, out)
			}
			os.Remove(ledger + side)
			if out, _, _ := fe.hook(t, "hello", "lane-1", fe.plain); !strings.Contains(out, "in fleet inv") {
				t.Fatalf("control: the fleet opens with the sidecar gone: %q", out)
			}
		})
	}
}

// THE FOLD OF THE CODE REVIEW (Codex code pass and the panel, D-191). Each
// alias test below is the reproduction of a finding, seen to fail on the tree
// before its fix.

// Codex code pass, finding 1 (P1), the hard link: `ln R/.git/buddy.db
// <fleet>/buddy.db` is a regular file under a real directory, and every write
// through the fleet's name went to R's own ledger, the one R's gate honours.
func TestFleetHardLinkedLedgerIsRefused(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fe.initAndHello(t)
	mustClaim(t, fe.fixture, fe.repo, "peer", "sess-a", "dir1")
	dir := filepath.Join(fe.home, ".buddy", "fleets", "inv")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(fe.repo, ".git", "buddy.db"), filepath.Join(dir, "buddy.db")); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
	fe.env[EnvFleet] = "inv"
	_, errw, code := fe.run(t, fe.plain, "", "release", "peer", "--session", "sess-a")
	if code != 1 || !strings.Contains(errw, "hard links") {
		t.Fatalf("release through a hard-linked fleet ledger: code %d %q, want refused", code, errw)
	}
	if out, _, _ := fe.run(t, fe.repo, "", "ls"); !strings.Contains(out, "peer") {
		t.Fatalf("the repository's claim must survive: %q", out)
	}
	if out, _, _ := fe.hook(t, "hello", "lane-1", fe.plain); !strings.Contains(out, "fleet ledger unavailable") || !strings.Contains(out, "hard links") {
		t.Fatalf("hello names the hard link: %q", out)
	}
	if _, errw, code := fe.run(t, fe.plain, "", "init", "--fleet", "inv"); code != 1 || !strings.Contains(errw, "refused: init --fleet inv") || !strings.Contains(errw, "hard links") {
		t.Fatalf("init --fleet must not adopt a hard link: code %d %q", code, errw)
	}
	// Control: from the repository the same release goes through.
	if _, errw, code := fe.run(t, fe.repo, "", "release", "peer", "--session", "sess-a"); code != 0 {
		t.Fatalf("control: %q", errw)
	}
}

// Codex code pass, finding 1 (P1), the common directory: a fleet's directory
// with objects/ and refs/ and no HEAD is a repository's COMMON directory once
// a linked worktree's gitdir names it in `commondir`, so buddy in that
// worktree keeps its ledger in the fleet's buddy.db, and a fleet verb from
// outside git freed a claim that worktree's gate honours.
func TestFleetDirectoryAsACommonDirIsRefused(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	ledger := fe.initFleet(t, "inv")
	dir := filepath.Dir(ledger)
	wt := filepath.Join(t.TempDir(), "W")
	gitIn(t, fe.repo, "worktree", "add", "-q", wt)
	for _, d := range []string{"objects", "refs"} {
		if out, err := exec.Command("cp", "-R", filepath.Join(fe.repo, ".git", d), dir).CombinedOutput(); err != nil {
			t.Fatalf("cp %s: %v %s", d, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(fe.repo, ".git", "worktrees", "W", "commondir"), []byte(dir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// git prints the resolved path (/private/var for /var on macOS). A fatal
	// and not a skip: a skipped reproduction proves nothing, and it first
	// skipped here on that spelling alone.
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(gitIn(t, wt, "rev-parse", "--git-common-dir")); got != want {
		t.Fatalf("premise: git must take the fleet's directory as the worktree's common dir; got %q, want %q", got, want)
	}
	// Since D-194 the repository side refuses this one file too: the
	// worktree's ledger sits directly in a fleet's directory, so its hello
	// fails naming D-194 and its gate denies (before, both worked, on the
	// fleet's file). The fleet side still refuses on its own, below.
	if _, errw, code := fe.run(t, wt, hookJSON("sess-w", wt, "", ""), "hello"); code == 0 || !strings.Contains(errw, "a file in the fleet directory") {
		t.Fatalf("the worktree's hello over a fleet's directory: code %d %q, want refused (D-194)", code, errw)
	}
	out, _, _ := fe.run(t, wt, hookJSON("sess-w", wt, "Write", filepath.Join(wt, "x.md")), "gate")
	if reason, denied := decodeDeny(t, out); !denied || !strings.Contains(reason, "D-194") {
		t.Fatalf("the worktree's gate over a fleet's directory: %q", out)
	}
	fe.env[EnvFleet] = "inv"
	_, errw, code := fe.run(t, fe.plain, "", "release", "peer", "--session", "sess-w")
	if code != 1 || !strings.Contains(errw, "the mark of a git directory") {
		t.Fatalf("release through a fleet that is a common dir: code %d %q, want refused", code, errw)
	}
	if _, errw, code := fe.run(t, fe.plain, "", "init", "--fleet", "inv"); code != 1 || !strings.Contains(errw, "refused: init --fleet inv") {
		t.Fatalf("init --fleet must not adopt a common dir: code %d %q", code, errw)
	}
}

// Each git mark alone refuses, and a file a desktop leaves (.DS_Store) does
// not: the deny-list, beside its control. The marks are spelled here and not
// read from gitDirMarks, so a list cut to HEAD alone fails this test (it
// first iterated the list it was checking, and the cut survived).
func TestFleetGitMarksRefuseEachAlone(t *testing.T) {
	boundedParallel(t)
	for _, m := range []string{".DS_Store", "HEAD", "objects", "refs", "commondir", "gitdir"} {
		t.Run(m, func(t *testing.T) {
			fe := newFleet(t)
			ledger := fe.initFleet(t, "inv")
			if err := os.WriteFile(filepath.Join(filepath.Dir(ledger), m), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			fe.env[EnvFleet] = "inv"
			out, _, _ := fe.hook(t, "hello", "lane-1", fe.plain)
			if m == ".DS_Store" {
				if !strings.Contains(out, "in fleet inv") {
					t.Fatalf("control: a stray desktop file must not take the fleet down: %q", out)
				}
				return
			}
			if !strings.Contains(out, "holds "+m+", the mark of a git directory") {
				t.Fatalf("a fleet directory holding %s: %q", m, out)
			}
		})
	}
}

// Codex code pass, finding 2 (P1): a `?` in $HOME ended the SQLite file: URI
// name early, so HOME=R/.git/buddy.db?x=y made init "find" R's ledger and a
// fleet verb from outside git release R's claims.
func TestFleetQuestionMarkInHomeOpensTheFleetsOwnFile(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fe.initAndHello(t)
	mustClaim(t, fe.fixture, fe.repo, "peer", "sess-a", "dir1")
	home := filepath.Join(fe.repo, ".git", "buddy.db?x=y")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	fe.env["HOME"] = home
	out, errw, code := fe.run(t, fe.plain, "", "init", "--fleet", "inv")
	if code != 0 || !strings.Contains(out, "fleet ledger ready") {
		t.Fatalf("init --fleet under a HOME with a '?': code %d %q %q, want a new fleet", code, out, errw)
	}
	if fi, err := os.Stat(filepath.Join(home, ".buddy", "fleets", "inv", "buddy.db")); err != nil || fi.Size() == 0 {
		t.Fatalf("the fleet's own file must exist at the literal path: %v", err)
	}
	fe.env[EnvFleet] = "inv"
	if _, errw, code := fe.run(t, fe.plain, "", "release", "peer", "--session", "sess-a"); code == 0 {
		t.Fatalf("release in the fleet must not find the repository's claim: %q", errw)
	}
	delete(fe.env, EnvFleet)
	if out, _, _ := fe.run(t, fe.repo, "", "ls"); !strings.Contains(out, "peer") {
		t.Fatalf("the repository's claim must survive: %q", out)
	}
}

// Codex code pass, finding 3: hello's replacement line fired after the digest
// was already out (a mark that failed), appending "on no roster" to a digest
// that had just registered the session. Now the digest stands alone, the
// error is today's, and the message is delivered again next time (control).
func TestFleetHelloAddsNoLineAfterItsDigest(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	ledger := fe.initFleet(t, "inv")
	fe.env[EnvFleet] = "inv"
	orc := fe.mkdirPlain(t, "orc")
	fe.hook(t, "hello", "orc-1", orc)
	fe.hook(t, "hello", "lane-1", fe.plain)
	if _, errw, code := fe.as(t, "orc-1", orc, "msg", "lane-1", "the brief is in briefs/i01.md"); code != 0 {
		t.Fatal(errw)
	}
	db, err := sql.Open("sqlite", ledger)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER nomark BEFORE INSERT ON inbox_delivery BEGIN SELECT RAISE(ABORT, 'mark refused by the test'); END`); err != nil {
		t.Fatal(err)
	}
	out, _, code := fe.hook(t, "hello", "lane-1", fe.plain) // a resume: the digest drains the inbox
	if !strings.Contains(out, "in fleet inv") || !strings.Contains(out, "briefs/i01.md") {
		t.Fatalf("the digest with the message: %q", out)
	}
	if strings.Contains(out, "BUDDY: fleet ledger") {
		t.Fatalf("no fleet line may follow a digest that went out: %q", out)
	}
	if code == 0 {
		t.Fatalf("a mark that failed keeps its error (the message is redelivered): code %d", code)
	}
	if _, err := db.Exec(`DROP TRIGGER nomark`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if out, _, _ := fe.hook(t, "hello", "lane-1", fe.plain); !strings.Contains(out, "briefs/i01.md") {
		t.Fatalf("control: the unmarked message is delivered again: %q", out)
	}
}

// Codex code pass, finding 6: a $HOME that is not there gave the walk its
// ENOENT and so "absent" ("its coordinator creates it with init"), where init
// cannot work. Unavailable, beside the control: an existing HOME with no
// fleet is absent.
func TestFleetMissingHomeIsUnavailableNotAbsent(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fe.env[EnvFleet] = "inv"
	if out, _, _ := fe.hook(t, "hello", "lane-1", fe.plain); !strings.Contains(out, "names no fleet ledger on this machine") {
		t.Fatalf("control: an existing HOME with no fleet is absent: %q", out)
	}
	dangling := filepath.Join(t.TempDir(), "home-link")
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone"), dangling); err != nil {
		t.Fatal(err)
	}
	for _, home := range []string{filepath.Join(t.TempDir(), "no-such-home"), dangling} {
		fe.env["HOME"] = home
		out, _, code := fe.hook(t, "hello", "lane-1", fe.plain)
		if code != 0 || !strings.Contains(out, "fleet ledger unavailable") || !strings.Contains(out, "is not an existing directory") {
			t.Fatalf("HOME %s: code %d %q, want unavailable", home, code, out)
		}
	}
}

// Codex code pass, finding 7, and the refusals lens, finding 6: a bare `--`
// and a repeated --fleet are refused and create nothing; the control is the
// plain repository init and one --fleet.
func TestInitFleetRefusesDashDashAndATwiceGivenName(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	if _, errw, code := fe.run(t, fe.repo, "", "init", "--"); code == 0 || !strings.Contains(errw, "bare --") {
		t.Fatalf("init --: code %d %q, want refused", code, errw)
	}
	if _, err := os.Stat(filepath.Join(fe.repo, ".git", "buddy.db")); err == nil {
		t.Fatal("init -- created a repository ledger")
	}
	_, errw, code := fe.run(t, fe.plain, "", "init", "--fleet", "a", "--fleet", "b")
	if code == 0 || !strings.Contains(errw, "--fleet given twice") {
		t.Fatalf("init --fleet a --fleet b: code %d %q, want refused", code, errw)
	}
	for _, n := range []string{"a", "b"} {
		if _, err := os.Stat(filepath.Join(fe.home, ".buddy", "fleets", n)); err == nil {
			t.Fatalf("a refused init created fleet %s", n)
		}
	}
	if _, errw, code := fe.run(t, fe.repo, "", "init"); code != 0 {
		t.Fatalf("control: %q", errw)
	}
	fe.initFleet(t, "b")
}

// The panel's docs lens, finding 3, and refusals lens, finding 7: init over a
// fleet directory it will not adopt refuses in its own words and changes
// nothing, a wide mode included; the control tightens a sound one.
func TestInitFleetChecksBeforeItChanges(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	dir := filepath.Join(fe.home, ".buddy", "fleets", "inv")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	_, errw, code := fe.run(t, fe.plain, "", "init", "--fleet", "inv")
	if code != 1 || !strings.Contains(errw, "refused: init --fleet inv") || !strings.Contains(errw, "nothing was created or changed") || strings.Contains(errw, "on no roster") {
		t.Fatalf("init over a git directory: code %d %q", code, errw)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o770 {
		t.Fatalf("a refused init changed the mode to %#o", fi.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(dir, "buddy.db")); err == nil {
		t.Fatal("a refused init created a ledger")
	}
	os.Remove(filepath.Join(dir, "HEAD"))
	out, errw, code := fe.run(t, fe.plain, "", "init", "--fleet", "inv")
	if code != 0 || !strings.Contains(out, "tightened") {
		t.Fatalf("control: %d %q %q", code, out, errw)
	}
}

// The panel's invariant-3 lens, finding 1: a session the opened fleet does
// not know (its hello ran before init --fleet) is told so at each prompt;
// once registered, nothing.
func TestFleetBusySaysASessionIsNotOnTheRoster(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fe.env[EnvFleet] = "inv"
	fe.hook(t, "hello", "lane-1", fe.plain) // absent: no fleet yet
	fe.initFleet(t, "inv")
	out, _, code := fe.hook(t, "busy", "lane-1", fe.plain)
	ctx := promptContext(t, out)
	if code != 0 || !strings.Contains(ctx, "not on fleet inv's roster") || !strings.Contains(ctx, "buddy hello --session lane-1") {
		t.Fatalf("busy for a session the fleet does not know: code %d %q", code, ctx)
	}
	fe.hook(t, "hello", "lane-1", fe.plain)
	if out, _, _ := fe.hook(t, "busy", "lane-1", fe.plain); strings.Contains(promptContext(t, out), "roster") {
		t.Fatalf("control: a registered session gets no roster line: %q", out)
	}
}

// The panel's lifecycle lens, finding 1 (D-191), as stage 2 reads it (D-193):
// a fleet lane that moves into a governed repository is not registered there;
// its fleet row keeps moving now, and busy says the home's half. Controls:
// BUDDY_FLEET unset says nothing, and a session the repository knows says
// nothing.
func TestFleetBusyInAGovernedRepoSaysTheHomeLacksIt(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fe.initAndHello(t)
	fe.initFleet(t, "inv")
	fe.env[EnvFleet] = "inv"
	fe.hook(t, "hello", "lane-1", fe.plain)
	out, _, _ := fe.hook(t, "busy", "lane-1", fe.repo)
	ctx := promptContext(t, out)
	// Stage 2 (D-193): the fleet row keeps moving, so the line is the home's
	// half: this directory's roster does not have the session.
	if !strings.Contains(ctx, "is not on the roster of this directory's own ledger") || !strings.Contains(ctx, "Its fleet inv row still moves") || strings.Contains(ctx, "goes GONE") {
		t.Fatalf("busy in a governed repo for a fleet lane: %q", ctx)
	}
	if out, _, _ := fe.hook(t, "busy", "sess-a", fe.repo); strings.Contains(promptContext(t, out), "not on the roster") {
		t.Fatalf("control: a session the repository knows: %q", out)
	}
	delete(fe.env, EnvFleet)
	if out, _, _ := fe.hook(t, "busy", "lane-1", fe.repo); strings.Contains(out, "BUDDY_FLEET") {
		t.Fatalf("control: unset says nothing: %q", out)
	}
}

// The refusals lens, finding 1: a slot beside a file is refused in either
// order and claims nothing; the control is the slot alone.
func TestFleetClaimRefusesAFileBesideASlot(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fe.initFleet(t, "inv")
	fe.env[EnvFleet] = "inv"
	fe.hook(t, "hello", "lane-1", fe.plain)
	for _, order := range [][]string{{".buddy/slot/i01", "notes.md"}, {"notes.md", ".buddy/slot/i01"}} {
		_, errw, code := fe.as(t, "lane-1", fe.plain, "claim", "i01", "--desc", "x", "--scope", order[0], "--scope", order[1])
		if code != 1 || !strings.Contains(errw, "reserves no file") {
			t.Fatalf("claim %v: code %d %q", order, code, errw)
		}
	}
	if out, _, _ := fe.as(t, "lane-1", fe.plain, "ls"); strings.Contains(out, "i01") {
		t.Fatalf("a refused claim left a row: %q", out)
	}
	if _, errw, code := fe.as(t, "lane-1", fe.plain, "claim", "i01", "--desc", "x", "--scope", ".buddy/slot/i01"); code != 0 {
		t.Fatalf("control: %q", errw)
	}
}

// The refusals lens, finding 2: `who <a/b>` in a fleet points at ls, not at
// whose (refused there); outside a fleet it still points at whose.
func TestFleetWhoHintPointsAtLs(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fe.initFleet(t, "inv")
	fe.env[EnvFleet] = "inv"
	fe.hook(t, "hello", "lane-1", fe.plain)
	_, errw, code := fe.as(t, "lane-1", fe.plain, "who", ".buddy/slot/x")
	if code != 1 || !strings.Contains(errw, "`buddy ls`") || strings.Contains(errw, "whose") {
		t.Fatalf("who <path> in a fleet: code %d %q", code, errw)
	}
	delete(fe.env, EnvFleet)
	fe.initAndHello(t)
	if _, errw, _ := fe.run(t, fe.repo, "", "who", "a/b"); !strings.Contains(errw, "`buddy whose <path>`") {
		t.Fatalf("control: %q", errw)
	}
}

// The refusals lens, finding 5: a refusal of what a fleet cannot do comes
// before the caller's identity is looked up, so a caller not on the roster
// gets the refusal's words; the control is the same caller's identity error
// on a verb the fleet grants.
func TestFleetRefusalsComeBeforeIdentity(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fe.initFleet(t, "inv")
	fe.env[EnvFleet] = "inv"
	if _, errw, code := fe.as(t, "ghost", fe.plain, "release", "nothere", "--in-run", "r1"); code != 1 || !strings.Contains(errw, "refused: parking") {
		t.Fatalf("release --in-run by an unregistered caller: code %d %q", code, errw)
	}
	if _, errw, code := fe.as(t, "ghost", fe.plain, "wait", "--on", "r1", "--ready", "HEAD"); code != 1 || !strings.Contains(errw, "refused: --ready in fleet inv") {
		t.Fatalf("wait --ready by an unregistered caller: code %d %q", code, errw)
	}
	if _, errw, code := fe.as(t, "ghost", fe.plain, "wait", "--on", "r1", "--until", "1h"); code != 1 || strings.Contains(errw, "refused:") {
		t.Fatalf("control: an unregistered caller's grant fails on identity: code %d %q", code, errw)
	}
}

// Rows a panel found untested, each cheap: a coordinator's `who <id>` for a
// lane not on the roster is an error, and inbox, a timed wait and wait clear
// are granted in a fleet.
func TestFleetGrantsAndAnUnknownLane(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	fe.initFleet(t, "inv")
	fe.env[EnvFleet] = "inv"
	orc := fe.mkdirPlain(t, "orc")
	fe.hook(t, "hello", "orc-1", orc)
	if _, errw, code := fe.as(t, "orc-1", orc, "who", "lane-9"); code != 1 {
		t.Fatalf("who for a lane not on the roster: code %d %q", code, errw)
	}
	fe.hook(t, "hello", "lane-1", fe.plain)
	if out, errw, code := fe.as(t, "orc-1", orc, "who", "lane-1"); code != 0 || !strings.Contains(out, "lane-1") {
		t.Fatalf("control: who for a registered lane: %d %q %q", code, out, errw)
	}
	for _, args := range [][]string{{"inbox"}, {"wait", "--until", "1h"}, {"wait", "clear"}, {"sessions"}} {
		if out, errw, code := fe.as(t, "lane-1", fe.plain, args...); code != 0 {
			t.Fatalf("%v in a fleet: code %d %q %q", args, code, out, errw)
		}
	}
}

// Codex re-verify, round 1, finding 2: a ledger the walk accepts and the open
// refuses (a newer schema) must leave a wide fleet directory as it was; init
// tightened it first and then failed, with the note never printed. Control:
// TestInitFleetChecksBeforeItChanges tightens a sound one.
func TestInitFleetTightensOnlyAfterTheOpen(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	ledger := fe.initFleet(t, "inv")
	db, err := sql.Open("sqlite", ledger)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 9999`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	dir := filepath.Dir(ledger)
	if err := os.Chmod(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	out, errw, code := fe.run(t, fe.plain, "", "init", "--fleet", "inv")
	if code == 0 {
		t.Fatalf("init over a newer-schema ledger: code 0 %q", out)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o770 {
		t.Fatalf("a failed init changed the mode to %#o (stdout %q, stderr %q)", fi.Mode().Perm(), out, errw)
	}
}

// Codex re-verify, round 2, finding 1: with buddy.db absent the walk said
// "absent" before it looked at the sidecars, so init created the ledger and
// SQLite attached a hard-linked -shm, writing through to the other name.
// Refused now, and the other file is untouched; the control is the same
// directory with the link gone.
func TestInitFleetChecksSidecarsWithNoLedger(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	dir := filepath.Join(fe.home, ".buddy", "fleets", "inv")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "repository-shm")
	want := []byte("a repository's shared memory, not the fleet's")
	if err := os.WriteFile(other, want, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(other, filepath.Join(dir, "buddy.db-shm")); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
	_, errw, code := fe.run(t, fe.plain, "", "init", "--fleet", "inv")
	if code != 1 || !strings.Contains(errw, "refused: init --fleet inv") || !strings.Contains(errw, "buddy.db-shm has 2 names") {
		t.Fatalf("init beside a hard-linked -shm and no ledger: code %d %q", code, errw)
	}
	if got, _ := os.ReadFile(other); string(got) != string(want) {
		t.Fatalf("the other name's bytes changed: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "buddy.db")); err == nil {
		t.Fatal("a refused init created the ledger")
	}
	os.Remove(filepath.Join(dir, "buddy.db-shm"))
	fe.initFleet(t, "inv")
}
