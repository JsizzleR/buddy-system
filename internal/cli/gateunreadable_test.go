package cli

// #146 and #147: invariant 3's "exists but cannot be read", collapsed into
// the silent-allow arm. Reproduced at 615766c with a binary built from that
// tree (the design note's U1-U7 and G1-G5 rows):
//
//   - #146: governed r, `chmod 000 r/.git` (or 400): git says "not a git
//     repository", and the D-212 walk could not look behind the entry
//     either, so a peer's claim on r/x.txt passed an Edit from cwd r and
//     from a plain cwd. Nested (governed P/r in governed P) git PASSES the
//     entry and answers P, so r's claims were never read. A worktree's
//     admin directory mode 000, a never-`buddy init`ed repository behind a
//     mode-000 .git, a `.git` link whose target is mode 000: the same.
//   - git passing an entry with a READABLE ledger behind it (P/r/.git/HEAD
//     mode 000) and its bare twin (cache.git/HEAD mode 000): allowed.
//   - #147: a discovery git the budget killed is no answer, and with no
//     .git above (a governed BARE cache.git) the old probe read it as
//     feature-off; below a ledgerless .git the bare level was never looked
//     at; safe.bareRepository=explicit refused a governed bare repository
//     into the same allow.
//
// Every deny row stands beside its control: the same fixture readable, or
// real git, denies with the claim or the pause text, so the guard was armed.
// The OFF rows are what must stay feature-off.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// skipAsRoot: root reads and searches a mode-000 directory, so no fixture
// here can refuse it anything.
func skipAsRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 path: these fixtures cannot refuse it")
	}
}

// chmodT sets p's mode and restores the one it had when the test ends,
// before t.TempDir's removal runs (Cleanup is LIFO).
func chmodT(t *testing.T, p string, mode os.FileMode) {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(p, fi.Mode().Perm()) })
}

// gitCommonDir is what real git answers from dir, "" when it fails.
func gitCommonDir(dir string) string {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(string(out), "\n")
}

const cannotTellText = "cannot tell whether"

type gateRow struct {
	name, cwd, input string
	deny             bool
	contains         string
}

func runRows(t *testing.T, f *fixture, rows []gateRow) {
	t.Helper()
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			wantGate(t, f, r.cwd, r.input, r.deny, r.contains)
		})
	}
}

// U1, U2 and U3: governed r's own .git. Mode 000 and 400 deny from cwd r and
// from a plain cwd; mode 100 (search only: git works, SQLite cannot open the
// directory to lock) already denied at base and is pinned as it is.
func TestGateUnreadableGitDirDenies(t *testing.T) {
	boundedParallel(t)
	skipAsRoot(t)
	f := newFixture(t)
	f.initAndHello(t)
	mustClaim(t, f, f.repo, "held", "sess-a", "x.txt")
	plain := canon(t.TempDir())
	x := filepath.Join(f.repo, "x.txt")
	edit := func(cwd string) string { return hookJSON("sess-b", cwd, "Edit", x) }
	runRows(t, f, []gateRow{
		{"control: readable, cwd r", f.repo, edit(f.repo), true, "held"},
		{"control: readable, cwd plain", plain, edit(plain), true, "held"},
		{"control: the holder passes", plain, hookJSON("sess-a", plain, "Edit", x), false, ""},
	})
	git := filepath.Join(f.repo, ".git")
	for _, mode := range []os.FileMode{0o000, 0o400} {
		t.Run("mode "+strconv.FormatUint(uint64(mode), 8), func(t *testing.T) {
			chmodT(t, git, mode)
			runRows(t, f, []gateRow{
				{"cwd r", f.repo, edit(f.repo), true, cannotTellText},
				{"cwd plain", plain, edit(plain), true, cannotTellText},
				{"cwd plain, the holder too", plain, hookJSON("sess-a", plain, "Edit", x), true, "permission denied"},
				{"cwd r, path-less Bash", f.repo, bashHookJSON("sess-b", f.repo, "ls"), true, cannotTellText},
			})
			// The deny names the entry, the refused object and its owner.
			reason, _ := gateDenied(t, f, plain, edit(plain))
			for _, want := range []string{git, ".git/commondir: permission denied", "owned by uid " + strconv.Itoa(os.Geteuid()), "never delete a ledger"} {
				if !strings.Contains(reason, want) {
					t.Fatalf("the deny must name %q: %q", want, reason)
				}
			}
		})
	}
	t.Run("mode 100, pinned", func(t *testing.T) {
		chmodT(t, git, 0o100)
		runRows(t, f, []gateRow{
			{"cwd r", f.repo, edit(f.repo), true, "unavailable"},
			{"cwd plain", plain, edit(plain), true, "unavailable"},
		})
	})
}

// U4 and the PASSED check: governed P/r inside governed P. With P/r/.git mode
// 000 git passes the entry and answers P; with only HEAD mode 000 git passes
// it too, and the ledger behind it is readable. Both deny from P/r, P and a
// plain cwd; r's claim denies all three while readable.
func TestGateNestedGitGitPassesDenies(t *testing.T) {
	boundedParallel(t)
	skipAsRoot(t)
	f := newFixture(t)
	f.initAndHello(t)
	p := f.repo
	r := filepath.Join(p, "r")
	nestGit(t, r, "init", "-q")
	initLedger(t, f, r)
	f.helloIn(t, r, "sess-a", "alpha")
	mustClaim(t, f, r, "rheld", "sess-a", "x.txt")
	plain := canon(t.TempDir())
	x := filepath.Join(r, "x.txt")
	rows := func(want string) []gateRow {
		return []gateRow{
			{"cwd P/r", r, hookJSON("sess-b", r, "Edit", x), true, want},
			{"cwd P", p, hookJSON("sess-b", p, "Edit", x), true, want},
			{"cwd plain", plain, hookJSON("sess-b", plain, "Edit", x), true, want},
		}
	}
	t.Run("control: readable", func(t *testing.T) { runRows(t, f, rows("rheld")) })
	t.Run("U4: P/r/.git mode 000", func(t *testing.T) {
		chmodT(t, filepath.Join(r, ".git"), 0)
		if got := gitCommonDir(r); got != canon(filepath.Join(p, ".git")) {
			t.Skipf("this git does not pass a mode-000 .git for the repository above (it answered %q)", got)
		}
		runRows(t, f, rows(cannotTellText))
	})
	t.Run("PASSED: P/r/.git/HEAD mode 000", func(t *testing.T) {
		chmodT(t, filepath.Join(r, ".git", "HEAD"), 0)
		if got := gitCommonDir(r); got != canon(filepath.Join(p, ".git")) {
			t.Skipf("this git does not pass a .git whose HEAD it cannot read (it answered %q)", got)
		}
		runRows(t, f, rows(cannotTellText))
	})
	t.Run("PASSED: P/r/.git/commondir unparsable", func(t *testing.T) {
		// Two lines in commondir: git passes the entry and answers P, and
		// commonDirOf cannot resolve it either, so passedEntry, with no
		// identity to compare, returned nil and r's claim on x.txt passed
		// from P/r, P and a plain cwd (Fable cross-check, F2).
		cdf := filepath.Join(r, ".git", "commondir")
		mustWrite(t, cdf, "a\nb\n")
		t.Cleanup(func() { os.Remove(cdf) })
		if got := gitCommonDir(r); got != canon(filepath.Join(p, ".git")) {
			t.Skipf("this git does not pass a .git with an unparsable commondir (it answered %q)", got)
		}
		runRows(t, f, rows(cannotTellText))
	})
	t.Run("PASSED, met first through an unregistered worktree inside r", func(t *testing.T) {
		// From cwd P/r/wt, a worktree of r whose admin gitdir file names
		// somewhere else (so not registered), the home walk meets r's common
		// directory at wt/.git, then again at P/r/.git, where git passes the
		// damaged entry and answers P: the already-met arm's passedEntry is
		// the only check that sees it, and without it r's ledger was placed
		// at P's root and an Edit in wt passed.
		nestGit(t, r, "commit", "-q", "--allow-empty", "-m", "r")
		wt := filepath.Join(r, "wt")
		nestGit(t, r, "worktree", "add", "-q", wt)
		mustWrite(t, filepath.Join(r, ".git", "worktrees", "wt", "gitdir"), filepath.Join(plain, "elsewhere", ".git")+"\n")
		wa := filepath.Join(wt, "a.txt")
		runRows(t, f, []gateRow{
			{"control: r's claim from cwd wt", wt, hookJSON("sess-b", wt, "Edit", x), true, "rheld"},
			{"control: readable, cwd wt, Edit wt/a.txt", wt, hookJSON("sess-b", wt, "Edit", wa), false, ""},
		})
		chmodT(t, filepath.Join(r, ".git", "HEAD"), 0)
		if got := gitCommonDir(wt); got != canon(filepath.Join(r, ".git")) {
			t.Skipf("this git does not open r's worktree with r's HEAD unreadable (it answered %q)", got)
		}
		if got := gitCommonDir(r); got != canon(filepath.Join(p, ".git")) {
			t.Skipf("this git does not pass a .git whose HEAD it cannot read (it answered %q)", got)
		}
		runRows(t, f, []gateRow{
			{"cwd wt, Edit wt/a.txt", wt, hookJSON("sess-b", wt, "Edit", wa), true, cannotTellText},
		})
	})
}

// The D-212 walk's own PASSED check, asked of enclosingLedger directly: from
// a ledgerless P/r/n, the walk reaches P/r/.git (HEAD mode 000), git answers
// P, and P was taken as the governor with r's ledger unread. Through the gate
// gateAncestors denies as well, so only a direct call shows this arm. And the
// permission check runs before the budget: a walk whose budget is spent must
// still deny at an unreadable entry, not report itself exhausted.
func TestEnclosingLedgerChecksEachEntryFirst(t *testing.T) {
	boundedParallel(t)
	skipAsRoot(t)
	f := newFixture(t)
	f.initAndHello(t)
	r := filepath.Join(f.repo, "r")
	nestGit(t, r, "init", "-q")
	initLedger(t, f, r)
	n := filepath.Join(r, "n")
	nestGit(t, n, "init", "-q")
	env := Env{Getenv: func(string) string { return "" }}
	gov, found := enclosingLedger(env, canon(n), nil)
	if !found || gov.err != nil || canon(gov.rc.top) != canon(r) {
		t.Fatalf("control: the walk from P/r/n finds r: found=%v %+v", found, gov)
	}
	gov.st.Close()
	t.Run("PASSED", func(t *testing.T) {
		chmodT(t, filepath.Join(r, ".git", "HEAD"), 0)
		gov, found := enclosingLedger(env, canon(n), nil)
		if gov.st != nil && !gov.shared {
			gov.st.Close()
		}
		if !found || gov.err == nil || !strings.Contains(gov.err.Error(), cannotTellText) {
			t.Fatalf("git passed P/r/.git for P: want a cannot-tell error, got found=%v err=%v top=%q", found, gov.err, gov.rc.top)
		}
	})
	t.Run("before the budget", func(t *testing.T) {
		chmodT(t, filepath.Join(r, ".git"), 0)
		c := &gateCache{m: map[string]gateRepo{}, limit: 1, opens: 1}
		gov, found := enclosingLedger(env, canon(r), c)
		if !found || gov.exhausted || gov.err == nil || !strings.Contains(gov.err.Error(), "permission denied") {
			t.Fatalf("a spent budget must not walk past an unreadable entry: found=%v %+v", found, gov)
		}
	})
}

// U5, U7 and a .git link whose target is mode 000.
func TestGateUnreadableGitPointerDenies(t *testing.T) {
	boundedParallel(t)
	skipAsRoot(t)
	f := newFixture(t)
	f.initAndHello(t)
	plain := canon(t.TempDir())
	wx := filepath.Join(f.wtB, "x.txt")
	mustClaim(t, f, f.wtB, "wheld", "sess-a", "x.txt")
	t.Run("U5: worktree admin directory mode 000", func(t *testing.T) {
		runRows(t, f, []gateRow{
			{"control: readable, cwd wt", f.wtB, hookJSON("sess-b", f.wtB, "Edit", wx), true, "wheld"},
		})
		chmodT(t, filepath.Join(f.repo, ".git", "worktrees", filepath.Base(f.wtB)), 0)
		runRows(t, f, []gateRow{
			{"cwd wt", f.wtB, hookJSON("sess-b", f.wtB, "Edit", wx), true, cannotTellText},
			{"cwd plain", plain, hookJSON("sess-b", plain, "Edit", wx), true, cannotTellText},
		})
	})
	t.Run("U5 through a .git link to an absolute gitfile", func(t *testing.T) {
		// W/.git a link to a readable gitfile whose ABSOLUTE `gitdir:` names
		// W's admin directory: the entry check read the file and stopped,
		// so with the admin directory mode 000 a peer's claim on W passed
		// an Edit from a plain cwd (Codex code pass, P1).
		w := filepath.Join(plain, "w2")
		nestGit(t, f.repo, "worktree", "add", "-q", w)
		gitfile := filepath.Join(plain, "w2-gitfile")
		if err := os.Rename(filepath.Join(w, ".git"), gitfile); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(gitfile, filepath.Join(w, ".git")); err != nil {
			t.Fatal(err)
		}
		mustClaim(t, f, w, "w2held", "sess-a", "w2.txt")
		w2x := filepath.Join(w, "w2.txt")
		runRows(t, f, []gateRow{
			{"control: readable, cwd plain", plain, hookJSON("sess-b", plain, "Edit", w2x), true, "w2held"},
			{"control: readable, cwd w", w, hookJSON("sess-b", w, "Edit", w2x), true, "w2held"},
		})
		chmodT(t, filepath.Join(f.repo, ".git", "worktrees", "w2"), 0)
		runRows(t, f, []gateRow{
			{"cwd plain", plain, hookJSON("sess-b", plain, "Edit", w2x), true, cannotTellText},
			{"cwd w", w, hookJSON("sess-b", w, "Edit", w2x), true, cannotTellText},
		})
	})
	t.Run("U5 through a .git link to a RELATIVE gitfile", func(t *testing.T) {
		// The same with `gitdir: <relative>`, the gitfile in a directory
		// at another depth: git resolves the pointer from the LINK's
		// directory (measured, Apple Git-157), and the entry check returned
		// nil for any relative pointer, so with the admin directory mode 000
		// a peer's claim on W passed from a plain cwd and from W (Fable
		// cross-check, F3).
		w := filepath.Join(plain, "w3")
		nestGit(t, f.repo, "worktree", "add", "-q", w)
		admin := filepath.Join(canon(f.repo), ".git", "worktrees", "w3")
		rel, err := filepath.Rel(w, admin)
		if err != nil {
			t.Fatal(err)
		}
		gitfile := filepath.Join(plain, "gf", "deep", "er", "w3-gitfile")
		mustWrite(t, gitfile, "gitdir: "+rel+"\n")
		if err := os.Remove(filepath.Join(w, ".git")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(gitfile, filepath.Join(w, ".git")); err != nil {
			t.Fatal(err)
		}
		if got := gitCommonDir(w); got != canon(filepath.Join(f.repo, ".git")) {
			t.Skipf("this git does not resolve a linked relative gitfile from the link's directory (it answered %q)", got)
		}
		mustClaim(t, f, w, "w3held", "sess-a", "w3.txt")
		w3x := filepath.Join(w, "w3.txt")
		runRows(t, f, []gateRow{
			{"control: readable, cwd plain", plain, hookJSON("sess-b", plain, "Edit", w3x), true, "w3held"},
			{"control: readable, cwd w", w, hookJSON("sess-b", w, "Edit", w3x), true, "w3held"},
		})
		chmodT(t, admin, 0)
		runRows(t, f, []gateRow{
			{"cwd plain", plain, hookJSON("sess-b", plain, "Edit", w3x), true, cannotTellText},
			{"cwd w", w, hookJSON("sess-b", w, "Edit", w3x), true, cannotTellText},
		})
	})
	t.Run("U7: never buddy-inited, mode 000", func(t *testing.T) {
		n := filepath.Join(plain, "n")
		nestGit(t, n, "init", "-q")
		nx := filepath.Join(n, "x.txt")
		// Control: readable, it is nobody's business.
		runRows(t, f, []gateRow{{"control: readable", n, hookJSON("sess-b", n, "Edit", nx), false, ""}})
		chmodT(t, filepath.Join(n, ".git"), 0)
		runRows(t, f, []gateRow{
			{"cwd n", n, hookJSON("sess-b", n, "Edit", nx), true, cannotTellText},
			{"cwd plain", plain, hookJSON("sess-b", plain, "Edit", nx), true, cannotTellText},
		})
	})
	t.Run("a .git link whose target is mode 000", func(t *testing.T) {
		s := filepath.Join(plain, "s")
		nestGit(t, s, "init", "-q")
		store := filepath.Join(plain, "s-gitdir")
		if err := os.Rename(filepath.Join(s, ".git"), store); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(store, filepath.Join(s, ".git")); err != nil {
			t.Fatal(err)
		}
		initLedger(t, f, s)
		f.helloIn(t, s, "sess-a", "alpha")
		mustClaim(t, f, s, "sheld", "sess-a", "x.txt")
		sx := filepath.Join(s, "x.txt")
		runRows(t, f, []gateRow{{"control: readable", plain, hookJSON("sess-b", plain, "Edit", sx), true, "sheld"}})
		chmodT(t, store, 0)
		runRows(t, f, []gateRow{
			{"cwd s", s, hookJSON("sess-b", s, "Edit", sx), true, cannotTellText},
			{"cwd plain", plain, hookJSON("sess-b", plain, "Edit", sx), true, cannotTellText},
		})
	})
	t.Run("a .git link whose target's directory is mode 000", func(t *testing.T) {
		// The link hop itself is refused: EvalSymlinks cannot Lstat the
		// target inside an unsearchable directory.
		v := filepath.Join(plain, "v")
		nestGit(t, v, "init", "-q")
		vault := filepath.Join(plain, "vault")
		if err := os.Mkdir(vault, 0o755); err != nil {
			t.Fatal(err)
		}
		store := filepath.Join(vault, "v-gitdir")
		if err := os.Rename(filepath.Join(v, ".git"), store); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(store, filepath.Join(v, ".git")); err != nil {
			t.Fatal(err)
		}
		initLedger(t, f, v)
		f.helloIn(t, v, "sess-a", "alpha")
		mustClaim(t, f, v, "vheld", "sess-a", "x.txt")
		vx := filepath.Join(v, "x.txt")
		runRows(t, f, []gateRow{{"control: readable", plain, hookJSON("sess-b", plain, "Edit", vx), true, "vheld"}})
		chmodT(t, vault, 0)
		runRows(t, f, []gateRow{
			{"cwd v", v, hookJSON("sess-b", v, "Edit", vx), true, cannotTellText},
			{"cwd plain", plain, hookJSON("sess-b", plain, "Edit", vx), true, cannotTellText},
		})
		// The walks' text names the locked directory, not the link the hop
		// began at: the link is mode 0755 and says nothing about the fix
		// (Codex code pass, P3). Asked of entryUnknown: this git reports
		// "error reading" the link, so the gate's own deny here is
		// discovery's, and the walks' text shows where git says "not a git
		// repository" instead.
		err := entryUnknown(filepath.Join(v, ".git"))
		if err == nil {
			t.Fatal("the hop through the locked directory must be cannot-tell")
		}
		if want := vault + " is mode 0000"; !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "(.git is mode") {
			t.Fatalf("the deny must name %q, not the link: %q", want, err)
		}
	})
}

// The bare twins with real git: a governed paused bare repository git will
// not open (HEAD mode 000: "not a git repository"), from its own cwd and by
// a plain cwd's Edit inside it; and in a ledgerless worktree P, where git
// passes the damaged P/cache.git and answers P (Codex design pass, X1).
func TestGateDamagedBareRepositoryDenies(t *testing.T) {
	boundedParallel(t)
	skipAsRoot(t)
	f := newFixture(t)
	plain := canon(t.TempDir())
	bare := func(dir string) string {
		nestGit(t, dir, "init", "-q", "--bare")
		initLedger(t, f, dir)
		if _, errw, code := f.run(t, dir, "", "pause", "all", "--note", "bare frozen"); code != 0 {
			t.Fatal(errw)
		}
		return dir
	}
	cache := bare(filepath.Join(plain, "cache.git"))
	pw := filepath.Join(plain, "P")
	nestGit(t, pw, "init", "-q")
	pcache := bare(filepath.Join(pw, "cache.git"))
	// X1 with core.worktree naming the damaged level: git answers P's common
	// directory with C as the TOPLEVEL, so the walk read the toplevel as the
	// level git used and allowed (Codex code pass, P1).
	qw := filepath.Join(plain, "Q")
	nestGit(t, qw, "init", "-q")
	qcache := bare(filepath.Join(qw, "cache.git"))
	nestGit(t, qw, "config", "core.worktree", qcache)
	hooksX := filepath.Join(cache, "hooks", "x")
	controls := []gateRow{
		{"control: cwd cache.git", cache, bashHookJSON("sess-b", cache, "touch f"), true, "bare frozen"},
		{"control: plain cwd's Edit inside it", plain, hookJSON("sess-b", plain, "Edit", hooksX), true, "bare frozen"},
		{"control: X1 cwd P/cache.git", pcache, bashHookJSON("sess-b", pcache, "touch f"), true, "bare frozen"},
		{"control: X1 core.worktree, cwd Q/cache.git", qcache, bashHookJSON("sess-b", qcache, "touch f"), true, "bare frozen"},
	}
	runRows(t, f, controls)
	chmodT(t, filepath.Join(cache, "HEAD"), 0)
	chmodT(t, filepath.Join(pcache, "HEAD"), 0)
	chmodT(t, filepath.Join(qcache, "HEAD"), 0)
	if got := gitCommonDir(cache); got != "" {
		t.Skipf("this git opens a bare repository whose HEAD it cannot read (%q)", got)
	}
	if got := gitCommonDir(pcache); got != canon(filepath.Join(pw, ".git")) {
		t.Skipf("this git does not pass the damaged P/cache.git for P (%q)", got)
	}
	if got := gitCommonDir(qcache); got != canon(filepath.Join(qw, ".git")) {
		t.Skipf("this git does not answer Q for its core.worktree Q/cache.git (%q)", got)
	}
	runRows(t, f, []gateRow{
		{"cwd cache.git", cache, bashHookJSON("sess-b", cache, "touch f"), true, cannotTellText},
		{"cwd cache.git/refs/heads", filepath.Join(cache, "refs", "heads"), bashHookJSON("sess-b", filepath.Join(cache, "refs", "heads"), "ls"), true, cannotTellText},
		{"plain cwd's Edit inside it", plain, hookJSON("sess-b", plain, "Edit", hooksX), true, cannotTellText},
		{"plain cwd's cd into it", plain, bashHookJSON("sess-b", plain, "cd "+cache+" && touch f"), true, cannotTellText},
		{"X1: cwd P/cache.git", pcache, bashHookJSON("sess-b", pcache, "touch f"), true, cannotTellText},
		{"X1 core.worktree: cwd Q/cache.git", qcache, bashHookJSON("sess-b", qcache, "touch f"), true, cannotTellText},
	})
}

// A level whose .git names the discovered common directory but has no HEAD
// is not the level git used (Codex re-verify, round 1): ledgerless P, governed
// paused bare P/B.git with HEAD mode 000, and P/B.git/X/.git holding only a
// commondir naming P/.git. git passes X and B and answers P; matched by its
// commondir alone, X stopped the bare-level walk before B was read: ALLOW.
func TestGateSeenIgnoresAHeadlessDiscoveredEntry(t *testing.T) {
	boundedParallel(t)
	skipAsRoot(t)
	f := newFixture(t)
	plain := canon(t.TempDir())
	pw := filepath.Join(plain, "P")
	nestGit(t, pw, "init", "-q")
	b := filepath.Join(pw, "B.git")
	nestGit(t, b, "init", "-q", "--bare")
	initLedger(t, f, b)
	if _, errw, code := f.run(t, b, "", "pause", "all", "--note", "bare frozen"); code != 0 {
		t.Fatal(errw)
	}
	x := filepath.Join(b, "X")
	if err := os.MkdirAll(filepath.Join(x, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(x, ".git", "commondir"), []byte(canon(filepath.Join(pw, ".git"))+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runRows(t, f, []gateRow{
		{"control: readable B, cwd B.git/X", x, bashHookJSON("sess-b", x, "touch f"), true, "bare frozen"},
	})
	chmodT(t, filepath.Join(b, "HEAD"), 0)
	if got := gitCommonDir(x); got != canon(filepath.Join(pw, ".git")) {
		t.Skipf("this git does not pass X and the damaged B for P (%q)", got)
	}
	runRows(t, f, []gateRow{
		{"cwd B.git/X", x, bashHookJSON("sess-b", x, "touch f"), true, cannotTellText},
	})
}

// What stays feature-off with real git, each beside a row proving the gate
// armed in the same fixture.
func TestGateUnreadableOffRows(t *testing.T) {
	boundedParallel(t)
	skipAsRoot(t)
	f := newFixture(t)
	f.initAndHello(t)
	mustClaim(t, f, f.repo, "held", "sess-a", "src")
	plain := canon(t.TempDir())
	// A stale pointer (ENOENT behind it) and a dangling .git link.
	stale := filepath.Join(plain, "stale")
	mustWrite(t, filepath.Join(stale, ".git"), "gitdir: "+filepath.Join(plain, "gone")+"\n")
	dangling := filepath.Join(plain, "dangling")
	if err := os.MkdirAll(dangling, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(plain, "nowhere"), filepath.Join(dangling, ".git")); err != nil {
		t.Fatal(err)
	}
	// An unsearchable directory that is NOT .git, in governed and ungoverned
	// repositories: not an entry (the rejected U-alt-2).
	locked := filepath.Join(f.repo, "locked")
	u := filepath.Join(plain, "u")
	nestGit(t, u, "init", "-q")
	ulocked := filepath.Join(u, "locked")
	// And a cwd behind one, in u and outside any repository: git "cannot
	// change to" it, the level proof skips the unsearchable levels.
	plocked := filepath.Join(plain, "plocked")
	for _, d := range []string{locked, ulocked, plocked} {
		if err := os.MkdirAll(filepath.Join(d, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		chmodT(t, d, 0)
	}
	// A regular file as the cwd (git: "cannot change to"), outside any
	// repository and inside governed r.
	file := filepath.Join(plain, "file.txt")
	mustWrite(t, file, "x")
	rfile := filepath.Join(f.repo, "file.txt")
	mustWrite(t, rfile, "x")
	// A ledgerless repository inside a governed paused bare repository is its
	// own: git never looks at the bare level above the repository it found.
	b := filepath.Join(plain, "b.git")
	nestGit(t, b, "init", "-q", "--bare")
	initLedger(t, f, b)
	if _, errw, code := f.run(t, b, "", "pause", "all", "--note", "b frozen"); code != 0 {
		t.Fatal(errw)
	}
	bn := filepath.Join(b, "n")
	nestGit(t, bn, "init", "-q")
	// And a ledgerless BARE repository there: the level git used is the
	// directory itself, not a .git entry (discoveredLevel's second test).
	bub := filepath.Join(b, "ub.git")
	nestGit(t, bub, "init", "-q", "--bare")
	// A commondir that is a LINK (Fable cross-check, F5): typed through the
	// link, a regular file is a pointer and is read; a link to a directory
	// is never opened and stays cannot-tell. A worktree admin directory
	// of ledgerless m whose commondir links to a regular file, and a plain
	// level holding HEAD and such a link, were cannot-tell for the link.
	m := filepath.Join(plain, "m")
	nestGit(t, m, "init", "-q")
	nestGit(t, m, "commit", "-q", "--allow-empty", "-m", "m")
	nestGit(t, m, "worktree", "add", "-q", filepath.Join(plain, "mwt"))
	madmin := filepath.Join(m, ".git", "worktrees", "mwt")
	if err := os.Rename(filepath.Join(madmin, "commondir"), filepath.Join(madmin, "commondir.real")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("commondir.real", filepath.Join(madmin, "commondir")); err != nil {
		t.Fatal(err)
	}
	linkLevel := func(name string, target string) string {
		d := filepath.Join(plain, name)
		mustWrite(t, filepath.Join(d, "HEAD"), "x\n")
		if err := os.MkdirAll(filepath.Join(d, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(d, "commondir")); err != nil {
			t.Fatal(err)
		}
		return filepath.Join(d, "sub")
	}
	mustWrite(t, filepath.Join(plain, "pointer"), "elsewhere\n")
	dfile := linkLevel("D", filepath.Join(plain, "pointer"))
	ddir := linkLevel("D2", plain)
	// A plain directory whose path leaves no room for "/buddy.db" (Fable
	// cross-check, F6): its Lstat is ENAMETOOLONG, which bareLevel called
	// cannot-tell before asking for a marker, so `ls` there denied.
	long := deepDir(t, 1018)
	ls := func(cwd string) string { return bashHookJSON("sess-b", cwd, "ls") }
	runRows(t, f, []gateRow{
		{"armed: a held path in r", f.repo, hookJSON("sess-b", f.repo, "Edit", filepath.Join(f.repo, "src", "a.go")), true, "held"},
		{"plain dir, Edit", plain, hookJSON("sess-b", plain, "Edit", filepath.Join(plain, "a.txt")), false, ""},
		{"plain dir, Bash", plain, bashHookJSON("sess-b", plain, "touch f"), false, ""},
		{"stale .git pointer", stale, hookJSON("sess-b", stale, "Edit", filepath.Join(stale, "a.txt")), false, ""},
		{"stale .git pointer, from plain", plain, hookJSON("sess-b", plain, "Edit", filepath.Join(stale, "a.txt")), false, ""},
		{"dangling .git link", dangling, hookJSON("sess-b", dangling, "Edit", filepath.Join(dangling, "a.txt")), false, ""},
		{"dangling .git link, from plain", plain, hookJSON("sess-b", plain, "Edit", filepath.Join(dangling, "a.txt")), false, ""},
		// Governed r/locked keeps the verdict it had at base, from r's
		// ledger: the target cannot be placed (cwd r), git cannot change
		// into it (cwd plain). Not "cannot tell" about a locked/.git.
		{"governed r/locked mode 000, cwd r (base verdict)", f.repo, hookJSON("sess-b", f.repo, "Edit", filepath.Join(locked, "x.c")), true, "could not resolve the target"},
		{"governed r/locked mode 000, cwd plain (base verdict)", plain, hookJSON("sess-b", plain, "Edit", filepath.Join(locked, "x.c")), true, "repo discovery failed"},
		{"ungoverned u/locked mode 000, cwd u", u, hookJSON("sess-b", u, "Edit", filepath.Join(ulocked, "x.c")), false, ""},
		{"ungoverned u/locked mode 000, cwd plain", plain, hookJSON("sess-b", plain, "Edit", filepath.Join(ulocked, "x.c")), false, ""},
		{"cwd behind u/locked mode 000", filepath.Join(ulocked, "sub"), bashHookJSON("sess-b", filepath.Join(ulocked, "sub"), "ls"), false, ""},
		{"cwd behind plain/plocked mode 000", filepath.Join(plocked, "sub"), bashHookJSON("sess-b", filepath.Join(plocked, "sub"), "ls"), false, ""},
		{"regular-file cwd", file, bashHookJSON("sess-b", file, "ls"), false, ""},
		{"armed: regular-file cwd in governed r", rfile, bashHookJSON("sess-b", rfile, "ls"), true, "unavailable"},
		{"armed: governed paused bare b.git", b, bashHookJSON("sess-b", b, "touch f"), true, "b frozen"},
		{"ledgerless repository inside governed bare b.git", bn, bashHookJSON("sess-b", bn, "touch f"), false, ""},
		{"ledgerless bare repository inside governed bare b.git", bub, bashHookJSON("sess-b", bub, "touch f"), false, ""},
		{"admin dir whose commondir links to a regular file, cwd admin", madmin, ls(madmin), false, ""},
		{"that worktree, cwd mwt", filepath.Join(plain, "mwt"), ls(filepath.Join(plain, "mwt")), false, ""},
		{"a level whose commondir links to a regular file", dfile, ls(dfile), false, ""},
		{"armed: a level whose commondir links to a directory", ddir, ls(ddir), true, cannotTellText},
		{"a plain directory 1018 bytes deep", long, ls(long), false, ""},
		{"a plain directory 1018 bytes deep, from plain", plain, hookJSON("sess-b", plain, "Edit", filepath.Join(long, "a")), false, ""},
	})
}

// deepDir is a new directory under a temp directory whose path is n bytes
// long (n-1 where a last component of one byte would not fit), made of
// components of at most 200 bytes.
func deepDir(t *testing.T, n int) string {
	t.Helper()
	d := canon(t.TempDir())
	for n-len(d) >= 2 {
		d += "/" + strings.Repeat("d", min(n-len(d)-1, 200))
	}
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

// The intended deny for a cwd through a link whose own mode is 000 (`chmod
// -h`): the kernel follows it, readlink is refused, and the level walk
// cannot resolve the path physically, which physicalLevelStart calls cannot
// tell (Fable cross-check, F6; pinned, not folded).
func TestGateCwdThroughALockedLinkDenies(t *testing.T) {
	boundedParallel(t)
	skipAsRoot(t)
	f := newFixture(t)
	plain := canon(t.TempDir())
	sub := filepath.Join(plain, "real", "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	lnk := filepath.Join(plain, "lnk")
	if err := os.Symlink(filepath.Join(plain, "real"), lnk); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(lnk, "sub")
	runRows(t, f, []gateRow{{"control: the link readable", cwd, bashHookJSON("sess-b", cwd, "ls"), false, ""}})
	if out, err := exec.Command("chmod", "-h", "000", lnk).CombinedOutput(); err != nil {
		t.Skipf("chmod -h: %v %s", err, out)
	}
	t.Cleanup(func() { exec.Command("chmod", "-h", "755", lnk).Run() })
	if _, err := os.Readlink(lnk); !errors.Is(err, syscall.EACCES) {
		t.Skipf("this system reads a mode-000 link (%v)", err)
	}
	runRows(t, f, []gateRow{
		{"cwd through it", cwd, bashHookJSON("sess-b", cwd, "ls"), true, "does not resolve"},
		{"plain cwd's Edit through it", plain, hookJSON("sess-b", plain, "Edit", filepath.Join(cwd, "a.txt")), true, "does not resolve"},
	})
}

// A FIFO named commondir at a level B holding HEAD and a buddy.db, above a
// valid ledgerless repository B/P: git stops at B/P/.git and never reads B,
// but the SEEN walk probed B before it recognised the level git used, and
// its ReadFile of the FIFO blocked the hook for good (Codex code pass, P2).
// From B/P it is off; from B itself (git: "not a git repository") the level
// is read, and a pointer that is not a regular file is "cannot tell", never
// opened. Each call runs under a deadline, so a regression fails, not hangs.
func TestGateSeenNeverOpensAFifoPointer(t *testing.T) {
	boundedParallel(t)
	skipAsRoot(t)
	f := newFixture(t)
	plain := canon(t.TempDir())
	b := filepath.Join(plain, "B")
	mustWrite(t, filepath.Join(b, "HEAD"), "ref: refs/heads/main\n")
	mustWrite(t, filepath.Join(b, ledgerName), "")
	fifo := filepath.Join(b, "commondir")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(b, "P")
	nestGit(t, p, "init", "-q")
	within := func(t *testing.T, cwd, input string) (string, bool) {
		t.Helper()
		type res struct{ out, errw string }
		done := make(chan res, 1)
		go func() {
			// runReader calls only t.Helper, safe off the test goroutine.
			out, errw, _ := f.run(t, cwd, input, "gate")
			done <- res{out, errw}
		}()
		select {
		case r := <-done:
			if r.errw != "" {
				t.Fatalf("gate stderr: %q", r.errw)
			}
			return decodeDeny(t, r.out)
		case <-time.After(20 * time.Second):
			// Unblock the reader (a writer's open lets its open return,
			// the close gives it EOF), wait for the gate, then fail.
			if w, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
				w.Close()
			}
			<-done
			t.Fatalf("the gate from %s blocked on the FIFO %s", cwd, fifo)
			return "", false
		}
	}
	t.Run("armed: cwd B", func(t *testing.T) {
		if reason, denied := within(t, b, bashHookJSON("sess-b", b, "touch f")); !denied || !strings.Contains(reason, cannotTellText) {
			t.Fatalf("from B itself the level is read and cannot be told: denied=%v %q", denied, reason)
		}
	})
	t.Run("OFF: cwd B/P", func(t *testing.T) {
		if reason, denied := within(t, p, bashHookJSON("sess-b", p, "touch f")); denied {
			t.Fatalf("B/P is git's own repository and B is never read: %q", reason)
		}
	})
}

// The deny's owner note: the refused path's deepest object this process can
// Lstat, its mode and uid. Another uid's cannot be made without a second
// account, so the note is asked of a path this test owns.
func TestOwnerNoteNamesTheRefusedObject(t *testing.T) {
	boundedParallel(t)
	skipAsRoot(t)
	d := filepath.Join(canon(t.TempDir()), "locked")
	if err := os.Mkdir(d, 0o755); err != nil {
		t.Fatal(err)
	}
	chmodT(t, d, 0)
	got := ownerNote(filepath.Dir(d), filepath.Join(d, "commondir"))
	want := " (locked is mode 0000, owned by uid " + strconv.Itoa(os.Geteuid())
	if !strings.HasPrefix(got, want) {
		t.Fatalf("ownerNote = %q, want the prefix %q", got, want)
	}
}

// wedgeGit puts a `git` that never answers first on PATH and lowers
// discoveryBudget, so every discovery git is killed after 200 ms (#147's G1
// shape, reproduced at base with this wrapper and with real git on a FIFO
// HEAD). The caller must not be parallel: PATH and the budget are process-
// wide, and a non-parallel test finishes before any parallel one runs.
func wedgeGit(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	prev := discoveryBudget
	discoveryBudget = 200 * time.Millisecond
	t.Cleanup(func() { discoveryBudget = prev })
	// Armed: discovery now gets no answer at all.
	if _, err := revParse(t.TempDir(), "--git-common-dir"); err == nil || !strings.Contains(err.Error(), "killed") {
		t.Fatalf("the wedge is not armed: %v", err)
	}
}

// G1, G2 and the level walk's shapes under a git that never answers, beside
// the OFF rows that must survive it. Every fixture is built with real git
// first, and each deny row's control denies with the pause text then.
func TestGateWedgedGitDiscovery(t *testing.T) {
	// No t.Parallel(): wedgeGit sets PATH and discoveryBudget.
	skipAsRoot(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "pause", "bravo", "--note", "r frozen"); code != 0 {
		t.Fatal(errw)
	}
	plain := canon(t.TempDir())
	bare := func(dir string) string {
		nestGit(t, dir, "init", "-q", "--bare")
		initLedger(t, f, dir)
		if _, errw, code := f.run(t, dir, "", "pause", "all", "--note", "bare frozen"); code != 0 {
			t.Fatal(errw)
		}
		return dir
	}
	cache := bare(filepath.Join(plain, "cache.git"))
	heads := filepath.Join(cache, "refs", "heads")
	// G2: a governed bare repository inside a LEDGERLESS worktree L.
	l := filepath.Join(plain, "L")
	nestGit(t, l, "init", "-q")
	lcache := bare(filepath.Join(l, "cache.git"))
	// X2: a governed bare repository holding an empty .git directory; a3:
	// one whose subdirectory holds one (git passes an invalid entry).
	x2 := bare(filepath.Join(plain, "x2.git"))
	if err := os.Mkdir(filepath.Join(x2, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	a3 := bare(filepath.Join(plain, "a3.git"))
	scratch := filepath.Join(a3, "scratch")
	if err := os.MkdirAll(filepath.Join(scratch, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// a4: a cwd reached through a link into the bare repository.
	link := filepath.Join(plain, "link")
	if err := os.Symlink(heads, link); err != nil {
		t.Fatal(err)
	}
	// A level with no buddy.db of its own whose commondir names governed
	// cache.git: git opens it as cache.git's (HEAD here, objects and refs
	// there), and the level walk returned "none" before it read the
	// commondir (Codex code pass, P1).
	cd := filepath.Join(plain, "A")
	head, err := os.ReadFile(filepath.Join(cache, "HEAD"))
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(cd, "HEAD"), string(head))
	mustWrite(t, filepath.Join(cd, "commondir"), cache+"\n")
	// OFF: a fleet-home-like directory (a buddy.db and nothing else), a
	// stray buddy.db directory, an ungoverned worktree admin directory
	// holding a backup buddy.db (its commondir names ungoverned m, X4), and
	// an ungoverned bare repository.
	fleet := filepath.Join(plain, "fleets", "f")
	mustWrite(t, filepath.Join(fleet, ledgerName), "")
	stray := filepath.Join(plain, "stray")
	if err := os.MkdirAll(filepath.Join(stray, ledgerName), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(stray, "notes.txt"), "x")
	m := filepath.Join(plain, "m")
	nestGit(t, m, "init", "-q")
	nestGit(t, m, "commit", "-q", "--allow-empty", "-m", "m")
	nestGit(t, m, "worktree", "add", "-q", filepath.Join(plain, "mwt"))
	admin := filepath.Join(m, ".git", "worktrees", "mwt")
	mustWrite(t, filepath.Join(admin, ledgerName), "backup")
	ub := filepath.Join(plain, "u.git")
	nestGit(t, ub, "init", "-q", "--bare")

	touch := func(cwd string) string { return bashHookJSON("sess-b", cwd, "touch f") }
	runRows(t, f, []gateRow{
		{"control: G1 cwd cache.git", cache, touch(cache), true, "bare frozen"},
		{"control: G1 cwd refs/heads", heads, touch(heads), true, "bare frozen"},
		{"control: G2 cwd L/cache.git", lcache, touch(lcache), true, "bare frozen"},
		{"control: X2", x2, touch(x2), true, "bare frozen"},
		{"control: a3", scratch, touch(scratch), true, "bare frozen"},
		{"control: a4", link, touch(link), true, "bare frozen"},
		{"control: a commondir level A", cd, touch(cd), true, "bare frozen"},
		{"control: governed r", f.repo, touch(f.repo), true, "r frozen"},
		{"control: ungoverned admin dir", admin, touch(admin), false, ""},
	})
	wedgeGit(t)
	runRows(t, f, []gateRow{
		{"G1 cwd cache.git", cache, touch(cache), true, cannotTellText},
		{"G1 cwd cache.git/refs/heads", heads, touch(heads), true, cannotTellText},
		{"G2 cwd L/cache.git", lcache, touch(lcache), true, cannotTellText},
		{"X2 an empty .git in the bare repository", x2, touch(x2), true, cannotTellText},
		{"a3 an empty .git below it", scratch, touch(scratch), true, cannotTellText},
		{"a4 cwd through a link into it", link, touch(link), true, cannotTellText},
		{"a commondir level A naming it", cd, touch(cd), true, cannotTellText},
		{"G4 governed non-bare r", f.repo, touch(f.repo), true, cannotTellText},
		{"G5 plain dir, Bash", plain, touch(plain), false, ""},
		{"G5 plain dir, Edit", plain, hookJSON("sess-b", plain, "Edit", filepath.Join(plain, "a.txt")), false, ""},
		{"fleet-home-like directory", fleet, touch(fleet), false, ""},
		{"stray buddy.db directory", stray, touch(stray), false, ""},
		{"ungoverned worktree admin directory", admin, touch(admin), false, ""},
		{"ungoverned bare repository", ub, touch(ub), false, ""},
	})
	// The proof itself, asked of discoverRepo: through the gate, its SEEN
	// walk (gateResolve) reads the same bare levels and denies these rows
	// too, so only a direct call shows that the proof continues past a
	// proved .git entry (a3) and runs both checks at one level (X2). The
	// typed verbs and the other hooks have no SEEN walk behind them.
	for _, dir := range []string{scratch, x2, lcache, link, cd} {
		if _, err := discoverRepo(dir); err == nil || errors.Is(err, errNoLedger) || !strings.Contains(err.Error(), cannotTellText) {
			t.Errorf("discoverRepo(%s) under the wedge: want a cannot-tell error, got %v", dir, err)
		}
	}
	for _, dir := range []string{plain, fleet, stray, admin, ub} {
		if _, err := discoverRepo(dir); !errors.Is(err, errNoLedger) {
			t.Errorf("discoverRepo(%s) under the wedge: want errNoLedger, got %v", dir, err)
		}
	}
	// The deny names what git gave and the level that could hold a ledger.
	reason, _ := gateDenied(t, f, cache, touch(cache))
	for _, want := range []string{"signal: killed", cache, "never delete a ledger"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("the deny must name %q: %q", want, reason)
		}
	}
}

// G3, D-137's recorded residual: safe.bareRepository=explicit makes git
// REFUSE a governed bare repository (exit 128, "cannot use bare repository"),
// which read as feature-off. Set through a fixture global config file.
func TestGateRefusedBareRepositoryDenies(t *testing.T) {
	// No t.Parallel(): t.Setenv, and revParse inherits the process environment.
	f := newFixture(t)
	plain := canon(t.TempDir())
	cache := filepath.Join(plain, "cache.git")
	nestGit(t, cache, "init", "-q", "--bare")
	initLedger(t, f, cache)
	if _, errw, code := f.run(t, cache, "", "pause", "all", "--note", "bare frozen"); code != 0 {
		t.Fatal(errw)
	}
	ub := filepath.Join(plain, "u.git")
	nestGit(t, ub, "init", "-q", "--bare")
	touch := func(cwd string) string { return bashHookJSON("sess-b", cwd, "touch f") }
	wantGate(t, f, cache, touch(cache), true, "bare frozen") // control
	cfg := filepath.Join(plain, "gitconfig")
	mustWrite(t, cfg, "[safe]\n\tbareRepository = explicit\n")
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	cmd := exec.Command("git", "-C", cache, "rev-parse", "--git-common-dir")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "cannot use bare repository") {
		t.Skipf("this git does not refuse a bare repository under explicit: %v %s", err, out)
	}
	wantGate(t, f, cache, touch(cache), true, cannotTellText)
	wantGate(t, f, plain, bashHookJSON("sess-b", plain, "cd "+cache+" && touch f"), true, cannotTellText)
	wantGate(t, f, ub, touch(ub), false, "") // ungoverned: off
	wantGate(t, f, plain, touch(plain), false, "")
}

// Under GIT_DIR or GIT_COMMON_DIR the level proof proves nothing, even where
// no .git entry is on the way (ledgerAbsentAt's own guard covers only an
// entry): git is not using the directories above at all. The hook verbs
// unset both (D-137); this is commit-gate's and the typed verbs' case.
func TestNoLedgerProofStandsDownUnderGitDir(t *testing.T) {
	// No t.Parallel(): t.Setenv.
	plain := canon(t.TempDir())
	if err := noLedgerProof(plain); err != nil {
		t.Fatalf("control: a plain directory is proved: %v", err)
	}
	for _, k := range []string{"GIT_DIR", "GIT_COMMON_DIR"} {
		t.Run(k, func(t *testing.T) {
			t.Setenv(k, filepath.Join(plain, "elsewhere.git"))
			if err := noLedgerProof(plain); err == nil {
				t.Fatalf("under %s nothing is proved", k)
			}
		})
	}
}

// F1 (Fable cross-check): noLedgerProof judged EVERY .git entry up to "/"
// with entryLedgerAbsent, where the probe it replaced judged only the
// nearest. An entry above the nearest is the D-212 walk's to judge, by
// asking git there; entryLedgerAbsent proves nothing for a symlinked .git or
// a non-pointer .git file, so under a ledgerless top/.git link (1a) and
// under a governed R (1b, whose ledger IS behind R/.git) a ledgerless
// repository git refuses denied every call, where base allowed and R
// adjudicated. The bare levels are still read up to "/" (X2, a3).
func TestNoLedgerProofJudgesTheNearestEntryOnly(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	mustClaim(t, f, f.repo, "yheld", "sess-a", "y.txt")
	plain := canon(t.TempDir())
	refused := func(dir string) string {
		nestGit(t, dir, "init", "-q")
		nestGit(t, dir, "config", "core.repositoryformatversion", "99")
		return dir
	}
	// 1a: no ledger anywhere; top/.git a link to a ledgerless git
	// directory, top2/.git a regular file that is no pointer.
	elsewhere := filepath.Join(plain, "elsewhere")
	nestGit(t, elsewhere, "init", "-q")
	if err := os.MkdirAll(filepath.Join(plain, "top"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(elsewhere, ".git"), filepath.Join(plain, "top", ".git")); err != nil {
		t.Fatal(err)
	}
	x := refused(filepath.Join(plain, "top", "x"))
	mustWrite(t, filepath.Join(plain, "top2", ".git"), "not a gitdir line\n")
	x2 := refused(filepath.Join(plain, "top2", "x"))
	// 1b: in governed R.
	vx := refused(filepath.Join(f.repo, "vendor", "x"))
	if got := gitCommonDir(x); got != "" {
		t.Skipf("this git opens a repository of format 99 (%q)", got)
	}
	// Armed: a refused repository with a ledger file behind its own entry.
	lx := refused(filepath.Join(plain, "lx"))
	mustWrite(t, filepath.Join(lx, ".git", ledgerName), "")
	ls := func(cwd string) string { return bashHookJSON("sess-b", cwd, "ls") }
	edit := func(cwd, p string) string { return hookJSON("sess-b", cwd, "Edit", p) }
	y := filepath.Join(f.repo, "y.txt")
	runRows(t, f, []gateRow{
		{"armed: refused, a ledger file behind it", lx, ls(lx), true, cannotTellText},
		{"armed: R's claim from cwd R/vendor/x", vx, edit(vx, y), true, "yheld"},
		{"armed: R's claim from cwd plain", plain, edit(plain, y), true, "yheld"},
		{"1a: cwd top/x, ls", x, ls(x), false, ""},
		{"1a: cwd top/x, Edit", x, edit(x, filepath.Join(x, "a.txt")), false, ""},
		{"1a: plain, Edit top/x/a.txt", plain, edit(plain, filepath.Join(x, "a.txt")), false, ""},
		{"1a: non-pointer top2/.git, cwd top2/x, ls", x2, ls(x2), false, ""},
		{"1a: non-pointer top2/.git, plain, Edit", plain, edit(plain, filepath.Join(x2, "a.txt")), false, ""},
		{"1b: cwd R/vendor/x, Edit x/a.txt", vx, edit(vx, filepath.Join(vx, "a.txt")), false, ""},
		{"1b: cwd R/vendor/x, ls", vx, ls(vx), false, ""},
		{"1b: cwd R, Edit x/a.txt", f.repo, edit(f.repo, filepath.Join(vx, "a.txt")), false, ""},
		{"1b: cwd plain, Edit x/a.txt", plain, edit(plain, filepath.Join(vx, "a.txt")), false, ""},
	})
}

// F4 (Fable cross-check): the SEEN walk's discoveredLevel did not recognise
// a level whose .git is a LINK to a gitfile (commonDirIdentity leaves that
// to git, for D-226's walk), so inside governed bare B.git, at a worktree of
// ledgerless S whose .git links to its gitfile, the walk read on to B.git
// and denied `ls` and a plain cwd's Edit there, which the parent allowed.
func TestGateLinkedGitfileLevelIsGitsOwn(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	plain := canon(t.TempDir())
	b := filepath.Join(plain, "B.git")
	nestGit(t, b, "init", "-q", "--bare")
	initLedger(t, f, b)
	if _, errw, code := f.run(t, b, "", "pause", "all", "--note", "b frozen"); code != 0 {
		t.Fatal(errw)
	}
	s := filepath.Join(plain, "S")
	nestGit(t, s, "init", "-q")
	nestGit(t, s, "commit", "-q", "--allow-empty", "-m", "s")
	w := filepath.Join(b, "w")
	nestGit(t, s, "worktree", "add", "-q", w)
	ls := func(cwd string) string { return bashHookJSON("sess-b", cwd, "ls") }
	wa := filepath.Join(w, "a.txt")
	runRows(t, f, []gateRow{
		{"armed: cwd B.git", b, bashHookJSON("sess-b", b, "touch f"), true, "b frozen"},
		{"control: a plain gitfile, cwd w", w, ls(w), false, ""},
	})
	gitfile := filepath.Join(plain, "gf", "wgit")
	if err := os.MkdirAll(filepath.Dir(gitfile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(w, ".git"), gitfile); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(gitfile, filepath.Join(w, ".git")); err != nil {
		t.Fatal(err)
	}
	if got := gitCommonDir(w); got != canon(filepath.Join(s, ".git")) {
		t.Skipf("this git does not follow a linked gitfile (it answered %q)", got)
	}
	runRows(t, f, []gateRow{
		{"a linked gitfile, cwd w", w, ls(w), false, ""},
		{"a linked gitfile, plain cwd's Edit", plain, hookJSON("sess-b", plain, "Edit", wa), false, ""},
	})
}

// F7 (Fable cross-check): the SEEN, PROOF and passed-entry texts named their
// directory up to four times, and behind the 512-byte wrappers a fixture
// under a ~148-byte path lost its fix to the truncation; ledgerThereErr
// named none. Each now names a path once, relative to the directory the
// fix names, and ends with that fix.
func TestGateDenyTextKeepsItsFix(t *testing.T) {
	// No t.Parallel(): the PROOF row runs under wedgeGit.
	skipAsRoot(t)
	f := newFixture(t)
	deep := deepDir(t, 150)
	plain := canon(t.TempDir())
	cache := filepath.Join(deep, "cache.git")
	nestGit(t, cache, "init", "-q", "--bare")
	initLedger(t, f, cache)
	if _, errw, code := f.run(t, cache, "", "pause", "all", "--note", "bare frozen"); code != 0 {
		t.Fatal(errw)
	}
	p := filepath.Join(deep, "P")
	nestGit(t, p, "init", "-q")
	initLedger(t, f, p)
	r := filepath.Join(p, "r")
	nestGit(t, r, "init", "-q")
	initLedger(t, f, r)
	f.helloIn(t, r, "sess-a", "alpha")
	mustClaim(t, f, r, "rheld", "sess-a", "x.txt")
	hooksX := filepath.Join(cache, "hooks", "x")
	rx := filepath.Join(r, "x.txt")
	edit := func(p string) string { return hookJSON("sess-b", plain, "Edit", p) }
	runRows(t, f, []gateRow{
		{"control: cache.git", plain, edit(hooksX), true, "bare frozen"},
		{"control: r", plain, edit(rx), true, "rheld"},
	})
	chmodT(t, filepath.Join(cache, "HEAD"), 0)
	chmodT(t, filepath.Join(r, ".git", "HEAD"), 0)
	if gitCommonDir(cache) != "" || gitCommonDir(r) != canon(filepath.Join(p, ".git")) {
		t.Skip("this git opens a repository whose HEAD it cannot read")
	}
	fix := func(t *testing.T, input, want string) {
		t.Helper()
		reason, denied := gateDenied(t, f, plain, input)
		if !denied || !strings.Contains(reason, want) {
			t.Fatalf("the deny must keep its fix %q: denied=%v %q", want, denied, reason)
		}
	}
	readable := "make it readable or work outside "
	t.Run("SEEN", func(t *testing.T) { fix(t, edit(hooksX), readable+cache+"; never delete a ledger") })
	t.Run("PASSED", func(t *testing.T) { fix(t, edit(rx), readable+r+"; never delete a ledger") })
	t.Run("PROOF", func(t *testing.T) {
		wedgeGit(t)
		fix(t, edit(hooksX), "fix the git that hangs or refuses here, or work outside "+cache+"; never delete a ledger")
	})
}
