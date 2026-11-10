package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// #76: an inherited git environment made the gate read a repository that HAS a
// ledger as one with none, and allow a write inside a peer's claim, printing
// nothing. Reproduced by the reviewer at main 9c509ec, and here at 7b17cc2:
// a relative GIT_DIR from a subdirectory makes git say "not a git repository"
// (discoverRepo's errNoLedger fast path, before ledgerProvablyAbsent's own
// GIT_DIR guard runs); an absolute one makes git report the cwd as the
// worktree root, so dir1/x.go places as x.go and the dir1 claim misses. A
// dotfiles shell (GIT_DIR and GIT_WORK_TREE aimed at another repository) reads
// every repository on the machine as ledgerless. A ceiling AT the repository
// root stops git's walk below it, the same "not a git repository".
//
// The hook verbs now start from git's own view of the filesystem: git's local
// environment and the ceiling are removed at their entry (hookgitenv.go). The
// commit gate is not a hook verb here and keeps git's environment, which is how
// a pre-commit hook states what is being committed (the charter's git facts).
//
// Not parallel: t.Setenv, and discovery inherits the process environment.

// gitEnvCases are the spellings of the bypass, each a set of variables.
func gitEnvCases(f *fixture, other string) []struct {
	name string
	env  map[string]string
} {
	return []struct {
		name string
		env  map[string]string
	}{
		{"relative GIT_DIR", map[string]string{"GIT_DIR": ".git"}},
		{"absolute GIT_DIR", map[string]string{"GIT_DIR": filepath.Join(f.repo, ".git")}},
		{"dotfiles GIT_DIR and GIT_WORK_TREE", map[string]string{"GIT_DIR": filepath.Join(other, ".git"), "GIT_WORK_TREE": other}},
		{"GIT_WORK_TREE alone", map[string]string{"GIT_WORK_TREE": other}},
		{"GIT_COMMON_DIR", map[string]string{"GIT_COMMON_DIR": filepath.Join(other, ".git")}},
		{"GIT_CEILING_DIRECTORIES at the root", map[string]string{"GIT_CEILING_DIRECTORIES": f.repo}},
	}
}

// otherRepo is a second repository with no ledger, the one a dotfiles
// environment aims git at.
func otherRepo(t *testing.T) string {
	t.Helper()
	other := filepath.Join(t.TempDir(), "dotfiles")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, other, "init", "-q")
	return other
}

func TestHookGitEnvCannotReadAPeersClaimAsUngoverned(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	other := otherRepo(t)
	dir1 := filepath.Join(f.repo, "dir1")
	if err := os.MkdirAll(dir1, 0o755); err != nil {
		t.Fatal(err)
	}
	mustClaim(t, f, f.repo, "peer", "sess-a", "dir1")
	write := hookJSON("sess-b", dir1, "Write", filepath.Join(dir1, "x.go"))

	// The positive control: with no git environment the peer's claim denies.
	out, _, _ := f.run(t, dir1, write, "gate")
	if reason, denied := decodeDeny(t, out); !denied || !strings.Contains(reason, `slug "peer"`) {
		t.Fatalf("control: the peer's dir1 claim must deny: denied=%v %q", denied, reason)
	}
	for _, c := range gitEnvCases(f, other) {
		t.Run(c.name, func(t *testing.T) {
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			out, _, _ := f.run(t, dir1, write, "gate")
			if reason, denied := decodeDeny(t, out); !denied || !strings.Contains(reason, `slug "peer"`) {
				t.Fatalf("under %v the peer's claim must still deny: denied=%v %q", c.env, denied, reason)
			}
		})
	}
}

// The same bypass through a Bash route: a shell-routed patch from a plain cwd
// finds its jurisdiction by discovering the directories it cds into
// (shellPatchDirs, D-073), and that discovery ran under the same environment.
func TestHookGitEnvCannotHideAShellRoutedPatchsRepo(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	other := otherRepo(t)
	plain := t.TempDir()
	patch := "*** Begin Patch\n*** Add File: x.go\n+x\n*** End Patch\n"
	bash := bashHookJSON("sess-b", plain, "cd "+f.repo+" && apply_patch <<'EOF'\n"+patch+"EOF\n")

	out, _, _ := f.run(t, plain, bash, "gate")
	if reason, denied := decodeDeny(t, out); !denied || !strings.Contains(reason, "apply_patch") {
		t.Fatalf("control: a shell-routed patch into a governed repo must deny: denied=%v %q", denied, reason)
	}
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	out, _, _ = f.run(t, plain, bash, "gate")
	if reason, denied := decodeDeny(t, out); !denied || !strings.Contains(reason, "apply_patch") {
		t.Fatalf("under a dotfiles environment the patch's repo must still govern: denied=%v %q", denied, reason)
	}
}

// The brake, on a path-less call from inside the repository: a paused session
// whose shell exports a relative GIT_DIR read its own home as ledgerless.
func TestHookGitEnvKeepsTheBrakeOnAPathlessCall(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	dir1 := filepath.Join(f.repo, "dir1")
	if err := os.MkdirAll(dir1, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, errw, code := f.run(t, f.repo, "", "pause", "bravo", "--note", "hold"); code != 0 {
		t.Fatal(errw)
	}
	bash := bashHookJSON("sess-b", dir1, "echo x > y")
	out, _, _ := f.run(t, dir1, bash, "gate")
	if reason, denied := decodeDeny(t, out); !denied || !strings.Contains(reason, "paused") {
		t.Fatalf("control: the paused session's Bash must deny: denied=%v %q", denied, reason)
	}
	t.Setenv("GIT_DIR", ".git")
	out, _, _ = f.run(t, dir1, bash, "gate")
	if reason, denied := decodeDeny(t, out); !denied || !strings.Contains(reason, "paused") {
		t.Fatalf("under a relative GIT_DIR the brake must still hold: denied=%v %q", denied, reason)
	}
}

// The fixed copy covers git's own list: a git that grows a local variable
// fails here, not silently in a hook.
func TestHookGitEnvCoversGitsLocalList(t *testing.T) {
	boundedParallel(t)
	out, err := exec.Command("git", "rev-parse", "--local-env-vars").Output()
	if err != nil {
		t.Fatalf("git rev-parse --local-env-vars: %v", err)
	}
	have := map[string]bool{}
	for _, k := range hookGitEnv {
		have[k] = true
	}
	names := strings.Fields(string(out))
	if len(names) == 0 {
		t.Fatal("control: git listed no local variables")
	}
	for _, k := range names {
		if !have[k] {
			t.Errorf("git lists local variable %s, which hookGitEnv lacks", k)
		}
	}
}

// The scrub is for the hook verbs only, and leaves an in-process caller's
// environment as it found it. A verb a session types still inherits git's
// environment (recorded open in D-137): `claim` from dir1 under a relative
// GIT_DIR finds no repository, where the gate, scrubbed, finds the ledger.
func TestHookGitEnvScrubsHooksOnlyAndRestores(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	dir1 := filepath.Join(f.repo, "dir1")
	if err := os.MkdirAll(dir1, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", ".git")
	t.Setenv("GIT_INDEX_FILE", "/elsewhere/next-index.lock")
	if _, errw, code := f.run(t, dir1, "", "claim", "x", "--session", "sess-a", "--scope", "dir1"); code == 0 || !strings.Contains(errw, "not a git repository") {
		t.Fatalf("control: a typed verb under a relative GIT_DIR inherits it and finds no repository: %d %q", code, errw)
	}
	if _, err := os.Stat(filepath.Join(f.repo, ".git", "buddy.db")); err != nil {
		t.Fatal(err)
	}
	if _, errw, code := f.run(t, dir1, hookJSON("sess-c", dir1, "", ""), "hello", "--label", "charlie"); code != 0 {
		t.Fatalf("hello under a relative GIT_DIR must register in the repository's ledger: %s", errw)
	}
	out, _, _ := f.run(t, f.repo, "", "sessions")
	if !strings.Contains(out, "charlie") {
		t.Fatalf("hello registered nowhere the repository's ledger shows:\n%s", out)
	}
	// bye ends it in that same ledger: unscrubbed, it found no repository and
	// the session would read live until swept.
	if _, errw, code := f.run(t, dir1, hookJSON("sess-c", dir1, "", ""), "bye"); code != 0 {
		t.Fatalf("bye under a relative GIT_DIR: %s", errw)
	}
	out, _, _ = f.run(t, f.repo, "", "who", "sess-c")
	if !strings.Contains(out, "ended") {
		t.Fatalf("bye under a relative GIT_DIR must end the session in the repository's ledger:\n%s", out)
	}
	if got := os.Getenv("GIT_DIR"); got != ".git" {
		t.Fatalf("the hook verb must restore GIT_DIR for an in-process caller, got %q", got)
	}
	if got := os.Getenv("GIT_INDEX_FILE"); got != "/elsewhere/next-index.lock" {
		t.Fatalf("the hook verb must restore GIT_INDEX_FILE, got %q", got)
	}
}

// Mutation run on the final code: dropping beat, idle, busy or lifecycle
// from hookVerbs survived, because only hello, bye and gate were driven
// under an inherited GIT_DIR. idle and busy are driven here (their effect is
// the roster's state in the repository's ledger); beat's dirty-path row is the
// third. The exact set is pinned below for lifecycle, whose fenced Codex
// path needs a captured incarnation token to observe.
func TestHookGitEnvScrubsEveryHookVerb(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	dir1 := filepath.Join(f.repo, "dir1")
	if err := os.MkdirAll(dir1, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", ".git")
	if _, errw, code := f.run(t, dir1, hookJSON("sess-c", dir1, "", ""), "hello", "--label", "charlie"); code != 0 {
		t.Fatal(errw)
	}
	who := func() string {
		out, _, _ := f.run(t, f.repo, "", "who", "sess-c")
		return out
	}
	before := who()
	if _, errw, code := f.run(t, dir1, hookJSON("sess-c", dir1, "", ""), "idle"); code != 0 {
		t.Fatal(errw)
	}
	idle := who()
	if idle == before || !strings.Contains(idle, "idle") {
		t.Fatalf("idle under a relative GIT_DIR must mark the session in the repository's ledger:\n%s", idle)
	}
	if _, errw, code := f.run(t, dir1, hookJSON("sess-c", dir1, "", ""), "busy"); code != 0 {
		t.Fatal(errw)
	}
	if busy := who(); busy == idle {
		t.Fatalf("busy under a relative GIT_DIR must clear idle in the repository's ledger:\n%s", busy)
	}
	file := filepath.Join(dir1, "note.txt")
	if err := os.WriteFile(file, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errw, code := f.run(t, dir1, hookJSON("sess-c", dir1, "Edit", file), "beat"); code != 0 {
		t.Fatal(errw)
	}
	if out, _, _ := f.run(t, f.repo, "", "whose", "dir1/note.txt"); !strings.Contains(out, "charlie") {
		t.Fatalf("beat under a relative GIT_DIR must record the dirty path in the repository's ledger:\n%s", out)
	}

	want := []string{"beat", "busy", "bye", "gate", "hello", "idle", "lifecycle"}
	var got []string
	for v, on := range hookVerbs {
		if on { // `"lifecycle": false` would skip the scrub (Codex re-verify)
			got = append(got, v)
		}
	}
	sort.Strings(got)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("hookVerbs = %v, want %v: a hook verb left out reads an inherited GIT_DIR (#76)", got, want)
	}
	for _, v := range want {
		if _, ok := verbs[v]; !ok {
			t.Fatalf("hookVerbs names %q, which the dispatch table does not", v)
		}
	}
}
