package cli

// #142: outside a repository every hook ran git's discovery twice (the second
// `rev-parse --git-common-dir` re-asked a setup that had already failed), and
// the gate on an Edit there ran it four times (the target's directory, the
// cwd itself, was discovered again). Counted on 615766c with a logging git
// first on PATH: plain-directory Edit 4, governed Edit 1, plain beat 2, a Bash
// with four distinct plain `cd`s 10. The counts are pinned here, never the
// milliseconds: the issue's ratio did not reproduce under load.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func canonDir(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// shExit runs a shell that prints stdout and stderr and exits with code, the
// way cmd.Output() hands a failed git back to discoverRepo.
func shExit(t *testing.T, stdout, stderr string, code int) (string, error) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", `printf '%s' "$1"; printf '%s' "$2" >&2; exit "$3"`,
		"sh", stdout, stderr, strconv.Itoa(code))
	out, err := cmd.Output()
	return string(out), err
}

// Each of the three conditions is needed on its own: every row but the first
// four breaks exactly one, and each has been watched to fail with that one
// condition removed.
func TestGitSetupSaysNoRepository(t *testing.T) {
	boundedParallel(t)
	const plain = "fatal: not a git repository (or any of the parent directories): .git\n"
	for _, tc := range []struct {
		name, stdout, stderr string
		code                 int
		want                 bool
	}{
		{"plain directory", "", plain, 128, true},
		{"filesystem boundary", "", "fatal: not a git repository (or any parent up to mount point /)\nStopping at filesystem boundary (GIT_DISCOVERY_ACROSS_FILESYSTEM not set).\n", 128, true},
		{"bad GIT_DIR", "", "fatal: not a git repository: '/x/nope'\n", 128, true},
		{"git 2.56 stale gitfile", "", "fatal: gitfile does not point to a valid repository: /x/wt/.git\n", 128, true},
		// stdout: setup succeeded and printed the common dir; the second call
		// is the one that answers (a bare repository).
		{"common dir on stdout", "/x/cache.git\n", plain, 128, false},
		// The anchor: a config value forging a fatal line inside a warning
		// (Codex design pass, P1), and a path git echoes in another fatal.
		{"forged line after a warning", "", "warning: ignoring unknown core.fsyncMethod value 'bogus\n" + plain + "'\nfatal: this operation must be run in a work tree\n", 128, false},
		{"text inside an echoed path", "", "fatal: cannot change to '/t/not a git repository/l': Permission denied\n", 128, false},
		// D-219's P1 shape: 2.56's text in a directory name git echoes
		// (Fable cross-check).
		{"2.56 text inside an echoed path", "", "fatal: failed to read /t/fatal: gitfile does not point to a valid repository: /wt/commondir: Permission denied\n", 128, false},
		{"a trace line first", "", "12:00:00.000000 git.c:463 trace: built-in: git rev-parse\n" + plain, 128, false},
		{"bare repository", "", "fatal: this operation must be run in a work tree\n", 128, false},
		{"refused repository", "", "fatal: detected dubious ownership in repository at '/x'\n", 128, false},
		// The exit: only git's die().
		{"exit 1", "", plain, 1, false},
		{"exit 127", "", plain, 127, false},
		{"exit 129", "", plain, 129, false},
	} {
		out, err := shExit(t, tc.stdout, tc.stderr, tc.code)
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != tc.code || out != tc.stdout || string(ee.Stderr) != tc.stderr {
			t.Fatalf("%s: the shell did not produce the case: out=%q err=%v", tc.name, out, err)
		}
		if got := gitSetupSaysNoRepository(out, err); got != tc.want {
			t.Errorf("%s: gitSetupSaysNoRepository = %v, want %v", tc.name, got, tc.want)
		}
	}
	// A git killed by discoveryBudget or a signal (ExitCode -1), with the text
	// already printed, and a git that never ran: neither is setup's verdict.
	killed := exec.Command("/bin/sh", "-c", `printf '%s' "$1" >&2; kill -9 $$`, "sh", plain)
	out, err := killed.Output()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != -1 || string(ee.Stderr) != plain {
		t.Fatalf("the signal case was not produced: %v", err)
	}
	if gitSetupSaysNoRepository(string(out), err) {
		t.Error("a killed git must take the second call")
	}
	missing := exec.Command(filepath.Join(t.TempDir(), "git"))
	out, err = missing.Output()
	if err == nil || gitSetupSaysNoRepository(string(out), err) {
		t.Errorf("a git that never ran must take the second call: %v", err)
	}
	if gitSetupSaysNoRepository("", nil) {
		t.Error("a successful call is no failure")
	}
}

func revParseCount(invs []string) int {
	n := 0
	for _, inv := range invs {
		if strings.Contains(inv, "rev-parse") {
			n++
		}
	}
	return n
}

// The counts after #142, with the base's beside each. The governed row arms
// the counter (1, as TestEachHookRunsOneDiscoveryProcess pins), and the bare
// row is the positive control for the second call: a bare repository fails
// the two-value call AFTER setup, and keeps both further calls.
func TestHookGitChildrenOutsideARepository(t *testing.T) {
	// No t.Parallel(): gitInvocations sets PATH.
	f := newFixture(t)
	f.initAndHello(t)
	repo := canonDir(t, f.repo)
	raw := t.TempDir()
	plain := canonDir(t, raw)
	noledger := filepath.Join(canonDir(t, t.TempDir()), "n")
	nestGit(t, noledger, "init", "-q")
	bare := filepath.Join(canonDir(t, t.TempDir()), "b.git")
	nestGit(t, bare, "init", "-q", "--bare")
	var cds []string
	for i := 0; i < 4; i++ {
		d := filepath.Join(canonDir(t, t.TempDir()), "d"+strconv.Itoa(i))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		cds = append(cds, "cd "+d)
	}
	type row struct {
		name, cwd, verb, stdin string
		want, base             int
	}
	rows := []row{
		{"governed Edit, armed", repo, "gate", hookJSON("sess-b", repo, "Edit", filepath.Join(repo, "x.go")), 1, 1},
		{"plain Edit", plain, "gate", hookJSON("sess-b", plain, "Edit", filepath.Join(plain, "x.txt")), 1, 4},
		{"plain relative Write", plain, "gate", hookJSON("sess-b", plain, "Write", "new.txt"), 1, 4},
		{"no-ledger repository Edit", noledger, "gate", hookJSON("sess-b", noledger, "Edit", filepath.Join(noledger, "x.txt")), 1, 2},
		{"plain Bash", plain, "gate", bashHookJSON("sess-b", plain, "ls"), 1, 2},
		{"plain Bash, four distinct cds", plain, "gate", bashHookJSON("sess-b", plain, strings.Join(cds, " && ")+" && ls"), 5, 10},
		{"plain cwd, governed target", plain, "gate", hookJSON("sess-b", plain, "Edit", filepath.Join(repo, "x.go")), 2, 3},
		{"plain beat", plain, "beat", hookJSON("sess-b", plain, "Bash", ""), 1, 2},
		{"plain idle", plain, "idle", hookJSON("sess-b", plain, "", ""), 1, 2},
		{"bare Bash, second call kept", bare, "gate", bashHookJSON("sess-b", bare, "ls"), 3, 3},
	}
	// The memo is the cwd's exact spelling: a cwd through a link misses it,
	// and the canonical target directory is discovered on its own. A link of
	// the test's own, so the row runs where t.TempDir is canonical too (CI's
	// /tmp; Fable cross-check); macOS's /var spelling besides.
	link := filepath.Join(canonDir(t, t.TempDir()), "link")
	if err := os.Symlink(plain, link); err != nil {
		t.Fatal(err)
	}
	rows = append(rows, row{"plain Edit, cwd spelled through a link", link, "gate", hookJSON("sess-b", link, "Edit", filepath.Join(link, "x.txt")), 2, 4})
	if raw != plain {
		rows = append(rows, row{"plain Edit, cwd spelled through the temp link", raw, "gate", hookJSON("sess-b", raw, "Edit", filepath.Join(raw, "x.txt")), 2, 4})
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			got := gitInvocations(t, f, tc.cwd, tc.stdin, tc.verb)
			if n := revParseCount(got); n != tc.want {
				out, errw, _ := f.run(t, tc.cwd, tc.stdin, tc.verb)
				t.Fatalf("%d rev-parse processes, want %d (base 615766c: %d):\n  %s\nstdout %q stderr %q", n, tc.want, tc.base, strings.Join(got, "\n  "), out, errw)
			}
		})
	}
}

// The forged fatal line, end to end: a bare repository with a ledger and a
// config warning that carries git's "not a git repository" text. The first
// call exits 128 with that text in stderr, and only the second call finds the
// repository. Read anywhere but byte 0 of an empty-stdout failure, discovery
// called it feature-off and the paused ledger's brake passed `touch f`.
func TestDiscoveryKeepsABareLedgerBehindAForgedFatalLine(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	bare := filepath.Join(canonDir(t, t.TempDir()), "cache.git")
	nestGit(t, bare, "init", "-q", "--bare")
	if _, errw, code := f.run(t, bare, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := f.run(t, bare, "", "pause", "all", "--note", "bare frozen"); code != 0 {
		t.Fatal(errw)
	}
	// Control: before the forgery, the brake applies.
	wantGate(t, f, bare, bashHookJSON("sess-b", bare, "touch f"), true, "bare frozen")
	nestGit(t, bare, "config", "core.fsyncMethod", "bogus\nfatal: not a git repository")
	// Armed: git prints the forged line, exits 128, and has the common dir on
	// stdout; the second call succeeds.
	cmd := exec.Command("git", "-C", bare, "rev-parse", "--path-format=absolute", "--git-common-dir", "--show-toplevel")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 128 || !strings.Contains(string(ee.Stderr), "\nfatal: not a git repository") {
		var stderr []byte
		if ee != nil {
			stderr = ee.Stderr
		}
		t.Skipf("this git does not print the forged line (%v, stderr %q): the fixture cannot arm", err, stderr)
	}
	if strings.TrimSpace(string(out)) == "" {
		t.Logf("this git prints no common dir before the work-tree fatal; the anchor alone guards it")
	}
	rc, derr := discoverRepo(bare)
	if derr != nil || rc.ledger != filepath.Join(bare, ledgerName) {
		t.Fatalf("discoverRepo = %+v, %v; want the bare ledger", rc, derr)
	}
	wantGate(t, f, bare, bashHookJSON("sess-b", bare, "touch f"), true, "bare frozen")
}

// The memo is the cwd's OWN discovery, taken before the D-212 walk replaces
// rc with the enclosing ledger's (panel lenses a and b). From a ledgerless
// repository nested in governed R, spelled canonically so the memo is used:
// `gh stack` needs no slot of R's (the walked home's catalog is not R's), and
// R's claim on vendor still refuses the nested file to a non-holder. Recorded
// after the walk, openGateRepo took R's rc for the nested directory as a
// direct discovery, and R's gh-stack slot refused the first row.
func TestGateReusesOnlyTheCwdsOwnDiscovery(t *testing.T) {
	boundedParallel(t)
	f, _, nested := nestedFixture(t)
	lib := canonDir(t, nested[0])
	repo := canonDir(t, f.repo)
	wantGate(t, f, repo, bashHookJSON("sess-b", repo, "gh stack rebase"), true, "gh-stack") // armed
	wantGate(t, f, lib, bashHookJSON("sess-b", lib, "gh stack rebase"), false, "")
	wantGate(t, f, lib, hookJSON("sess-b", lib, "Edit", filepath.Join(lib, "x.c")), true, "vend")
	wantGate(t, f, lib, hookJSON("sess-a", lib, "Edit", filepath.Join(lib, "x.c")), false, "")
	wantGate(t, f, lib, hookJSON("sess-b", lib, "Write", "new.c"), true, "vend")
}
