package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buddy update (the operator's request, 2026-10-27): reinstall from the
// checkout install.sh stamped, after a report read from local git only. Every
// test here points the verb at a FIXTURE checkout whose scripts/install.sh is
// a recording stub: nothing in this file may run the real install.sh or
// touch this machine's buddy (CLAUDE.md: never install as part of a fixture).
// The stub writes its physical cwd to a record file OUTSIDE the checkout, so
// "it ran" and "it ran there" are one observation, and "it never ran" is the
// record's absence, each beside a control in which it did run.

type updFix struct {
	c      string // the checkout
	rec    string // the stub's record: absent until it runs
	rcFile string // the status the stub exits with
}

func newUpdFix(t *testing.T) updFix {
	t.Helper()
	root := t.TempDir()
	f := updFix{c: filepath.Join(root, "co"), rec: filepath.Join(root, "ran"), rcFile: filepath.Join(root, "rc")}
	if err := os.MkdirAll(filepath.Join(f.c, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(f.c, "go.mod"), "module "+buddyModule+"\n\ngo 1.26\n")
	write(filepath.Join(f.c, "scripts", "install.sh"), "#!/bin/sh\ncat > '"+f.rec+".stdin'\npwd -P > '"+f.rec+"'\nexit $(cat '"+f.rcFile+"')\n")
	write(f.rcFile, "0\n")
	gitIn(t, f.c, "init", "-q", "-b", "main")
	gitIn(t, f.c, "add", "-A")
	gitIn(t, f.c, "commit", "-q", "-m", "base")
	return f
}

// refAt points ref at sha with its reflog entry dated at, so an age printed
// against updNow is exact.
func refAt(t *testing.T, dir, ref, sha string, at time.Time, logged bool) {
	t.Helper()
	args := []string{"-C", dir}
	if !logged {
		args = append(args, "-c", "core.logAllRefUpdates=false")
	}
	cmd := exec.Command("git", append(args, "update-ref", ref, sha)...)
	cmd.Env = append(os.Environ(), "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", fmt.Sprintf("GIT_COMMITTER_DATE=@%d +0000", at.Unix()))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("update-ref: %v\n%s", err, out)
	}
}

func (f updFix) ran(t *testing.T) bool {
	t.Helper()
	b, err := os.ReadFile(f.rec)
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(f.c)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(b)); got != want {
		t.Fatalf("the stub ran in %q, want the checkout %q", got, want)
	}
	return true
}

var updNow = time.Date(2026, 10, 27, 21, 0, 0, 0, time.UTC)

// updBin is where the tests say install.sh puts buddy (BUDDY_BIN_DIR): never
// the real ~/bin, which no test may even read.
const updBin = "/nonexistent/buddy-update-test/bin"

// builtAt plays an installed build stamped with rev ("" = no commit recorded).
func builtAt(rev string, modified bool) func(string) (builtFrom, bool) {
	return func(string) (builtFrom, bool) { return builtFrom{rev: rev, modified: modified}, true }
}

func runUpd(t *testing.T, src updateSource, args ...string) (int, string, string) {
	t.Helper()
	if src.sh == "" {
		src.sh = "/bin/sh"
	}
	if src.buildOf == nil {
		src.buildOf = builtAt("", false)
	}
	var out, errb bytes.Buffer
	rc := runUpdate(args, Env{
		Stdin: strings.NewReader(`{"hook_event_name":"Stop"}`), Stdout: &out, Stderr: &errb,
		Now: func() time.Time { return updNow }, Getenv: func(k string) string {
			if k == "BUDDY_BIN_DIR" {
				return updBin
			}
			return ""
		},
	}, src)
	return rc, out.String(), errb.String()
}

func TestUpdateRefusesARecordItCannotFollow(t *testing.T) {
	boundedParallel(t)
	cases := []struct {
		name  string
		setup func(t *testing.T, f updFix) string // returns the stamp
		want  string
	}{
		{"no stamp: a plain go build", func(*testing.T, updFix) string { return "" }, "carries no record of the checkout"},
		{"relative stamp", func(*testing.T, updFix) string { return "co" }, "is not a plain absolute path"},
		{"control character", func(_ *testing.T, f updFix) string { return f.c + "\nx" }, "is not a plain absolute path"},
		{"gone", func(_ *testing.T, f updFix) string { return f.c + "-moved" }, "is gone (moved or deleted)"},
		{"no go.mod", func(t *testing.T, f updFix) string {
			if err := os.Remove(filepath.Join(f.c, "go.mod")); err != nil {
				t.Fatal(err)
			}
			return f.c
		}, "has no go.mod"},
		{"another module", func(t *testing.T, f updFix) string {
			if err := os.WriteFile(filepath.Join(f.c, "go.mod"), []byte("module example.com/other\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return f.c
		}, `declares module "example.com/other", not ` + buddyModule},
		{"no install.sh", func(t *testing.T, f updFix) string {
			if err := os.Remove(filepath.Join(f.c, "scripts", "install.sh")); err != nil {
				t.Fatal(err)
			}
			return f.c
		}, "has no scripts/install.sh"},
		{"install.sh a symlink out of the checkout", func(t *testing.T, f updFix) string {
			sh := filepath.Join(f.c, "scripts", "install.sh")
			elsewhere := filepath.Join(filepath.Dir(f.c), "elsewhere.sh")
			if err := os.Rename(sh, elsewhere); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(elsewhere, sh); err != nil {
				t.Fatal(err)
			}
			return f.c
		}, "has no scripts/install.sh as a regular file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newUpdFix(t)
			stamp := tc.setup(t, f)
			for _, args := range [][]string{nil, {"--dry-run"}} {
				rc, out, errs := runUpd(t, updateSource{checkout: stamp}, args...)
				if rc != 1 || !strings.Contains(errs, tc.want) || !strings.Contains(errs, "sh <checkout>/scripts/install.sh") {
					t.Fatalf("%v: rc=%d stderr=%q, want 1 naming %q and the fix", args, rc, errs, tc.want)
				}
				if out != "" || f.ran(t) {
					t.Fatalf("%v: a refused record reported or ran something: %q", args, out)
				}
			}
		})
	}
	// The control: the same fixture, its record intact, runs the stub.
	f := newUpdFix(t)
	if rc, _, errs := runUpd(t, updateSource{checkout: f.c}); rc != 0 || !f.ran(t) {
		t.Fatalf("control: rc=%d ran=%v stderr=%q", rc, f.ran(t), errs)
	}
}

func TestUpdatePassesInstallStatusThrough(t *testing.T) {
	boundedParallel(t)
	f := newUpdFix(t)
	rc, out, _ := runUpd(t, updateSource{checkout: f.c})
	if rc != 0 || !f.ran(t) || !strings.Contains(out, "running: sh "+filepath.Join(f.c, "scripts", "install.sh")) {
		t.Fatalf("rc=%d out=%q", rc, out)
	}
	// The verb's stdin (a hook payload, here) never reaches the script.
	if b, err := os.ReadFile(f.rec + ".stdin"); err != nil || len(b) != 0 {
		t.Fatalf("install.sh read %q from stdin (%v), want nothing", b, err)
	}
	os.Remove(f.rec)
	if err := os.WriteFile(f.rcFile, []byte("7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, _, errs := runUpd(t, updateSource{checkout: f.c})
	if rc != 7 || !f.ran(t) || !strings.Contains(errs, "install.sh exited 7") {
		t.Fatalf("a failing install must be the verb's status: rc=%d stderr=%q", rc, errs)
	}
}

func TestUpdateDryRunRunsNothing(t *testing.T) {
	boundedParallel(t)
	f := newUpdFix(t)
	rc, out, _ := runUpd(t, updateSource{checkout: f.c}, "--dry-run")
	if rc != 0 || f.ran(t) {
		t.Fatalf("dry run: rc=%d ran=%v", rc, f.ran(t))
	}
	if !strings.Contains(out, "dry run: nothing installed; buddy update would run: sh "+filepath.Join(f.c, "scripts", "install.sh")) {
		t.Fatalf("dry run must name what it would run:\n%s", out)
	}
	// The control: the same call without the flag runs it.
	if rc, _, _ := runUpd(t, updateSource{checkout: f.c}); rc != 0 || !f.ran(t) {
		t.Fatalf("control: rc=%d ran=%v", rc, f.ran(t))
	}
}

// The report, state by state. Each row names lines that must appear and lines
// that must not, so a state is told apart from its neighbour, not only seen.
func TestUpdateReportsWhereTheCheckoutStands(t *testing.T) {
	boundedParallel(t)
	type st struct {
		name    string
		setup   func(t *testing.T, f updFix) updateSource
		want    []string
		notWant []string
		refuses bool
	}
	head := func(t *testing.T, f updFix) string { return gitIn(t, f.c, "rev-parse", "HEAD") }
	cases := []st{
		{"equal everywhere, fetched 3h ago", func(t *testing.T, f updFix) updateSource {
			h := head(t, f)
			gitIn(t, f.c, "config", "remote.origin.url", "x")
			refAt(t, f.c, "refs/remotes/origin/main", h, updNow.Add(-50*time.Hour), true)
			fh := filepath.Join(f.c, ".git", "FETCH_HEAD")
			if err := os.WriteFile(fh, []byte(h+"\t\tbranch 'main' of x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(fh, updNow.Add(-3*time.Hour), updNow.Add(-3*time.Hour)); err != nil {
				t.Fatal(err)
			}
			return updateSource{checkout: f.c, buildOf: builtAt(h, false)}
		}, []string{
			"on branch main; clean",
			"HEAD is even with local main",
			"HEAD is even with origin/main, as of the last fetch, 3h ago; it may have moved since",
			"the installed build is HEAD already",
			"fetch, then buddy update --dry-run",
		}, []string{"pull --ff-only", "UNCOMMITTED", "switch main", "git did not record", "last changed here"}, false},
		{"FETCH_HEAD of another remote's branch is not origin/main's", func(t *testing.T, f updFix) updateSource {
			h := head(t, f)
			gitIn(t, f.c, "config", "remote.origin.url", "x")
			refAt(t, f.c, "refs/remotes/origin/main", h, updNow.Add(-5*time.Hour), true)
			fh := filepath.Join(f.c, ".git", "FETCH_HEAD")
			if err := os.WriteFile(fh, []byte(h+"\t\tbranch 'feature' of y\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(fh, updNow, updNow); err != nil {
				t.Fatal(err)
			}
			return updateSource{checkout: f.c}
		}, []string{"as of the last fetch; origin/main last changed here 5h ago"}, []string{"last fetch, 0s ago"}, false},
		{"origin/main with no reflog and no FETCH_HEAD", func(t *testing.T, f updFix) updateSource {
			refAt(t, f.c, "refs/remotes/origin/main", head(t, f), updNow, false)
			return updateSource{checkout: f.c}
		}, []string{"as of the last fetch (when that was, git did not record)"}, nil, false},
		{"no local main: origin/main still compared", func(t *testing.T, f updFix) updateSource {
			h := head(t, f)
			refAt(t, f.c, "refs/remotes/origin/main", h, updNow.Add(-time.Hour), true)
			gitIn(t, f.c, "switch", "-q", "-c", "feature")
			gitIn(t, f.c, "branch", "-q", "-D", "main")
			return updateSource{checkout: f.c}
		}, []string{"no local main or master branch", "HEAD is even with origin/main, as of the last fetch; origin/main last changed here 1h ago"}, []string{"no origin/main"}, false},
		{"shallow history: never refused", func(t *testing.T, f updFix) updateSource {
			a := head(t, f)
			commitN(t, f.c, "e", 1)
			if err := os.WriteFile(filepath.Join(f.c, ".git", "shallow"), []byte(head(t, f)+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return updateSource{checkout: f.c, buildOf: builtAt(a, false)}
		}, []string{"history is shallow (or unreadable), so the installed commit"}, []string{"REFUSE", "carries"}, false},
		{"behind origin/main as last fetched, no FETCH_HEAD", func(t *testing.T, f updFix) updateSource {
			commitN(t, f.c, "a", 2)
			ahead := head(t, f)
			gitIn(t, f.c, "reset", "-q", "--hard", "HEAD~2")
			refAt(t, f.c, "refs/remotes/origin/main", ahead, updNow.Add(-2*time.Hour), true)
			return updateSource{checkout: f.c}
		}, []string{
			"HEAD is 0 ahead, 2 behind origin/main, as of the last fetch; origin/main last changed here 2h ago",
			"2 commit(s) on origin/main are not in HEAD: git -C " + f0 + " pull --ff-only, then buddy update again",
			"records no commit",
		}, []string{"switch main", "the installed build is HEAD"}, false},
		{"on another branch, ahead of main, dirty", func(t *testing.T, f updFix) updateSource {
			gitIn(t, f.c, "switch", "-q", "-c", "feature")
			commitN(t, f.c, "b", 1)
			if err := os.WriteFile(filepath.Join(f.c, "b"), []byte("changed"), 0o644); err != nil {
				t.Fatal(err)
			}
			return updateSource{checkout: f.c}
		}, []string{
			"on branch feature; UNCOMMITTED changes to tracked files",
			"HEAD is 1 ahead, 0 behind local main",
			"the install builds branch feature, not main",
			"no origin/main or origin/master recorded here",
		}, []string{"pull --ff-only", "on branch feature; clean"}, false},
		{"installed is older: the install brings commits", func(t *testing.T, f updFix) updateSource {
			old := head(t, f)
			commitN(t, f.c, "c", 2)
			return updateSource{checkout: f.c, buildOf: builtAt(old, false)}
		}, []string{"brings 2 commit(s) the installed build does not have"}, []string{"REFUSE", "carries"}, false},
		{"installed commit unknown here: said, not refused", func(t *testing.T, f updFix) updateSource {
			return updateSource{checkout: f.c, buildOf: builtAt(strings.Repeat("ab", 20), false)}
		}, []string{"the installed commit abababab is not in this checkout, so it cannot be placed"}, []string{"REFUSE"}, false},
		{"installed is NEWER than HEAD: refused", func(t *testing.T, f updFix) updateSource {
			commitN(t, f.c, "d", 1)
			newer := head(t, f)
			gitIn(t, f.c, "reset", "-q", "--hard", "HEAD~1")
			return updateSource{checkout: f.c, buildOf: builtAt(newer, false)}
		}, []string{"carries 1 commit(s) HEAD does not"}, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newUpdFix(t)
			src := tc.setup(t, f)
			want := make([]string, len(tc.want))
			for i, w := range tc.want {
				want[i] = strings.ReplaceAll(w, f0, f.c)
			}
			for _, dry := range []bool{true, false} {
				var args []string
				if dry {
					args = []string{"--dry-run"}
				}
				os.Remove(f.rec)
				rc, out, errs := runUpd(t, src, args...)
				for _, w := range want {
					if !strings.Contains(out, w) {
						t.Errorf("dry=%v: missing %q in:\n%s", dry, w, out)
					}
				}
				for _, w := range tc.notWant {
					if strings.Contains(out+errs, w) {
						t.Errorf("dry=%v: unexpected %q in:\n%s%s", dry, w, out, errs)
					}
				}
				switch {
				case tc.refuses:
					if rc != 1 || f.ran(t) || !strings.Contains(errs, "an older buddy refuses a newer ledger (D-037)") || !strings.Contains(errs, "run: sh "+filepath.Join(f.c, "scripts", "install.sh")) {
						t.Fatalf("dry=%v: a downgrade must refuse, run nothing and name both ways on: rc=%d ran=%v stderr=%q", dry, rc, f.ran(t), errs)
					}
				case dry:
					if rc != 0 || f.ran(t) {
						t.Fatalf("dry run: rc=%d ran=%v", rc, f.ran(t))
					}
				default:
					if rc != 0 || !f.ran(t) {
						t.Fatalf("rc=%d ran=%v stderr=%q", rc, f.ran(t), errs)
					}
				}
			}
		})
	}
}

// f0 stands for the fixture's checkout in a table written before it exists.
const f0 = "<checkout>"

// The installed commit is read from the file install.sh replaces, not from
// the running binary, and the report says when those differ or when there is
// no build there at all.
func TestUpdateReadsTheInstalledFileNotItself(t *testing.T) {
	boundedParallel(t)
	f := newUpdFix(t)
	h := gitIn(t, f.c, "rev-parse", "HEAD")
	var asked []string
	src := updateSource{checkout: f.c, exe: "/somewhere/else/buddy", buildOf: func(p string) (builtFrom, bool) {
		asked = append(asked, p)
		return builtFrom{rev: h, modified: true}, true
	}}
	_, out, _ := runUpd(t, src, "--dry-run")
	if want := filepath.Join(updBin, "buddy"); len(asked) != 1 || asked[0] != want {
		t.Fatalf("read the build of %q, want only %q", asked, want)
	}
	for _, w := range []string{
		"installed  " + filepath.Join(updBin, "buddy") + ": built from " + h[:8] + " plus uncommitted changes",
		"the installed build is HEAD plus uncommitted changes",
		"note       this buddy, /somewhere/else/buddy, is not that file: install.sh leaves it as it is",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
	// No build there: said, and nothing is compared.
	src.buildOf = func(string) (builtFrom, bool) { return builtFrom{}, false }
	src.exe = filepath.Join(updBin, "buddy")
	_, out, _ = runUpd(t, src, "--dry-run")
	if !strings.Contains(out, "no buddy build at "+filepath.Join(updBin, "buddy")+" to compare with") || strings.Contains(out, "note ") || strings.Contains(out, "  install  ") {
		t.Fatalf("no installed build:\n%s", out)
	}
}

func TestUpdateAnswersHelpAndRefusesWhatItDoesNotKnow(t *testing.T) {
	t.Parallel()
	run := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		rc := Run(append([]string{"update"}, args...), Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb})
		return rc, out.String(), errb.String()
	}
	for _, h := range [][]string{{"--help"}, {"--dry-run", "--help"}} {
		if rc, out, _ := run(h...); rc != 0 || !strings.HasPrefix(out, "usage: buddy update") {
			t.Fatalf("%v: rc=%d out=%q", h, rc, out)
		}
	}
	for _, bad := range [][]string{{"--check"}, {"now"}, {"--dry-run", "x"}} {
		if rc, _, errs := run(bad...); rc != 1 || !strings.Contains(errs, "refused:") {
			t.Fatalf("%v: rc=%d stderr=%q, want a usage refusal", bad, rc, errs)
		}
	}
	// This test binary was not built by install.sh: no stamp, so the real
	// verb refuses before it reads or runs anything.
	if rc, out, errs := run(); rc != 1 || out != "" || !strings.Contains(errs, "carries no record") {
		t.Fatalf("unstamped: rc=%d out=%q stderr=%q", rc, out, errs)
	}
}

func TestBuddyModuleIsTheRepoModule(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if got := modulePath(b); got != buddyModule {
		t.Fatalf("go.mod declares %q, update checks for %q", got, buddyModule)
	}
	for in, want := range map[string]string{
		"module a.b/c\n":           "a.b/c",
		"// x\nmodule \"a.b/c\"\n": "a.b/c",
		"module a.b/c // note\n":   "a.b/c",
		"modules a.b/c\n":          "",
		"go 1.26\n":                "",
	} {
		if got := modulePath([]byte(in)); got != want {
			t.Errorf("modulePath(%q) = %q, want %q", in, got, want)
		}
	}
}

// A clean filter the checkout's config names is a program `git status` runs
// on a changed file. The report, dry run included, must not run it. The
// negative comes FIRST: the control's plain status refreshes the index, after
// which nothing would run the filter and the negative would pass vacuously.
func TestUpdateRunsNoFilterTheCheckoutConfigures(t *testing.T) {
	boundedParallel(t)
	f := newUpdFix(t)
	marker := filepath.Join(filepath.Dir(f.c), "filter-ran")
	gitIn(t, f.c, "config", "filter.probe.clean", "touch '"+marker+"'; cat")
	if err := os.WriteFile(filepath.Join(f.c, ".git", "info", "attributes"), []byte("go.mod filter=probe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gm := filepath.Join(f.c, "go.mod")
	b, err := os.ReadFile(gm)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)-2] = '7' // same size, different content, newer mtime
	if err := os.WriteFile(gm, b, 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(gm, later, later); err != nil {
		t.Fatal(err)
	}
	rc, out, errs := runUpd(t, updateSource{checkout: f.c}, "--dry-run")
	if rc != 0 || !strings.Contains(out, "UNCOMMITTED changes to tracked files") {
		t.Fatalf("rc=%d: dirtiness must still be read with the filter off:\n%s%s", rc, out, errs)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the report ran the checkout's clean filter")
	}
	gitIn(t, f.c, "status", "--porcelain")
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("control: a plain git status did not run the filter, so nothing above was tested")
	}
}

// The running buddy compared by resolved path against the checkout's own
// bin/ copy (which install.sh rebuilds): an alias of the checkout path is not
// "another file" (macOS's /var is /private/var).
func TestUpdateNoteResolvesTheCheckoutCopy(t *testing.T) {
	boundedParallel(t)
	f := newUpdFix(t)
	bin := filepath.Join(f.c, "bin", "buddy")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(f.c, alias); err != nil {
		t.Fatal(err)
	}
	exe, err := filepath.EvalSymlinks(bin)
	if err != nil {
		t.Fatal(err)
	}
	_, out, _ := runUpd(t, updateSource{checkout: alias, exe: exe}, "--dry-run")
	if strings.Contains(out, "note ") {
		t.Fatalf("the checkout's own bin/buddy, reached through an alias, is not another file:\n%s", out)
	}
	// The control: a copy outside the checkout is named.
	_, out, _ = runUpd(t, updateSource{checkout: alias, exe: "/elsewhere/buddy"}, "--dry-run")
	if !strings.Contains(out, "note       this buddy, /elsewhere/buddy, is not that file") {
		t.Fatalf("control: an outside copy must be named:\n%s", out)
	}
}
