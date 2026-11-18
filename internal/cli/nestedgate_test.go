package cli

// #121 (D-212): a target inside a nested git repository with NO ledger (a
// `git init`ed or vendored clone, a submodule, a stale .git file) escaped the
// enclosing repository's claims and brake unless the hook cwd was the
// enclosing repository itself. Reproduced at 39c0c92 with a binary built from
// that tree: A holds `vendor`, B's Edit of R/vendor/lib/x.c was DENIED from
// cwd R and ALLOWED from cwd R/vendor/lib, from a relative path there, and
// from a plain directory above R; a paused B's `cd R/vendor/lib && touch f`
// passed from both. Every discovery asked git, which stops at the NEAREST
// repository, and read its "no ledger" as feature-off.
//
// The rule: the first ledger at or above a directory governs it, and the home
// ledger stays additive beside a nested one (D-073 F-03). Each test below
// keeps its positive control beside the refusal: the holder passes, an
// unpaused session passes, a ledgerless repository nested in nothing passes.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func nestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v %s", args, dir, err, out)
	}
}

// nestedFixture is f.repo (governed; alpha holds vendor and mods) holding
// three ledgerless nested repositories: vendor/lib (`git init`), vendor/stale
// (a pruned worktree's .git file, which git calls "not a git repository") and
// mods/sub (a submodule's shape: a `gitdir:` file into R/.git/modules).
func nestedFixture(t *testing.T) (f *fixture, plain string, nested []string) {
	t.Helper()
	f = newFixture(t)
	f.initAndHello(t)
	lib := filepath.Join(f.repo, "vendor/lib")
	nestGit(t, lib, "init", "-q")
	mustWrite(t, filepath.Join(f.repo, "vendor/stale/.git"), "gitdir: "+filepath.Join(f.repo, ".git/worktrees/gone")+"\n")
	sub := filepath.Join(f.repo, "mods/sub")
	if err := os.MkdirAll(filepath.Join(f.repo, ".git/modules/mods"), 0o755); err != nil {
		t.Fatal(err)
	}
	nestGit(t, f.repo, "init", "-q", "--separate-git-dir", filepath.Join(f.repo, ".git/modules/mods/sub"), sub)
	mustClaim(t, f, f.repo, "vend", "sess-a", "vendor", "mods")
	return f, t.TempDir(), []string{lib, filepath.Join(f.repo, "vendor/stale"), sub}
}

func TestGateLedgerlessNestedRepoKeepsTheEnclosingClaim(t *testing.T) {
	boundedParallel(t)
	f, plain, nested := nestedFixture(t)
	for _, n := range nested {
		x := filepath.Join(n, "x.c")
		for _, tc := range []struct {
			name, cwd, input string
			deny             bool
		}{
			{"control: cwd R (D-073 F-03)", f.repo, hookJSON("sess-b", f.repo, "Edit", x), true},
			{"cwd nested", n, hookJSON("sess-b", n, "Edit", x), true},
			{"cwd nested, relative Write", n, hookJSON("sess-b", n, "Write", "new.c"), true},
			{"cwd plain", plain, hookJSON("sess-b", plain, "Edit", x), true},
			{"cwd plain, apply_patch tool", plain, patchHookJSON("sess-b", plain, patchText("*** Add File: "+x+"\n+x\n")), true},
			{"cwd nested, apply_patch tool, relative", n, patchHookJSON("sess-b", n, patchText("*** Add File: y.c\n+x\n")), true},
			// The positive controls: the holder passes from every cwd, and a
			// walked home grants nothing beyond what its claims refuse.
			{"holder, cwd plain", plain, hookJSON("sess-a", plain, "Edit", x), false},
			{"holder, cwd nested", n, hookJSON("sess-a", n, "Edit", x), false},
			{"free path in R, cwd nested", n, hookJSON("sess-b", n, "Edit", filepath.Join(f.repo, "free.go")), false},
		} {
			t.Run(filepath.Base(n)+"/"+tc.name, func(t *testing.T) {
				wantGate(t, f, tc.cwd, tc.input, tc.deny, "vend")
			})
		}
	}
	// The deny from a nested cwd names the repository whose ledger governs,
	// the root a claim is made from: its own repository has no ledger.
	reason, denied := gateDenied(t, f, nested[0], hookJSON("sess-b", nested[0], "Edit", filepath.Join(nested[0], "x.c")))
	if !denied || !strings.Contains(reason, "(in "+canon(f.repo)) {
		t.Fatalf("the nested-cwd deny must name the governing root %s: denied=%v %q", f.repo, denied, reason)
	}
}

// The feature-off boundary: a ledgerless repository nested in NOTHING governed
// is nobody's business, paused session or not, and so is one under a .git git
// cannot open at all (no ledger can sit behind it), or a symlinked .git
// directory with no ledger (proved through the link, not refused).
func TestGateLedgerlessRepoNestedInNothingStaysOff(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "pause", "all"); code != 0 {
		t.Fatal(errw)
	}
	plain := t.TempDir()
	alone := filepath.Join(plain, "alone")
	nestGit(t, alone, "init", "-q")
	junk := filepath.Join(plain, "junk")
	mustWrite(t, filepath.Join(junk, ".git"), "")
	nestGit(t, filepath.Join(junk, "x"), "init", "-q")
	linked := filepath.Join(plain, "linked")
	nestGit(t, filepath.Join(plain, "donor"), "init", "-q")
	if err := os.MkdirAll(linked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(plain, "donor/.git"), filepath.Join(linked, ".git")); err != nil {
		t.Fatal(err)
	}
	nestGit(t, filepath.Join(linked, "x"), "init", "-q")
	for _, dir := range []string{alone, filepath.Join(junk, "x"), filepath.Join(linked, "x")} {
		wantGate(t, f, dir, hookJSON("sess-b", dir, "Edit", filepath.Join(dir, "a.c")), false, "")
		wantGate(t, f, dir, bashHookJSON("sess-b", dir, "touch f"), false, "")
		wantGate(t, f, plain, bashHookJSON("sess-b", plain, "cd "+dir+" && touch f"), false, "")
	}
	// Positive control: the same paused session in the governed repo.
	wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, "touch f"), true, "paused")
}

func TestGateLedgerlessNestedRepoKeepsTheEnclosingBrake(t *testing.T) {
	boundedParallel(t)
	f, plain, nested := nestedFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "pause", "bravo", "--note", "hold the line"); code != 0 {
		t.Fatal(errw)
	}
	for _, n := range nested {
		for _, tc := range []struct {
			name, cwd, input string
			deny             bool
		}{
			{"cd from plain", plain, bashHookJSON("sess-b", plain, "cd "+n+" && touch f"), true},
			{"git -C from plain", plain, bashHookJSON("sess-b", plain, "git -C "+n+" commit -qam x"), true},
			{"path-less Bash from nested cwd", n, bashHookJSON("sess-b", n, "touch f"), true},
			{"Edit of a free file from plain", plain, hookJSON("sess-b", plain, "Edit", filepath.Join(n, "free.c")), true},
			{"control: unpaused session", plain, bashHookJSON("sess-a", plain, "cd "+n+" && touch f"), false},
			{"control: unpaused, nested cwd", n, bashHookJSON("sess-a", n, "touch f"), false},
			{"control: plain cwd, no cd", plain, bashHookJSON("sess-b", plain, "touch f"), false},
		} {
			t.Run(filepath.Base(n)+"/"+tc.name, func(t *testing.T) {
				wantGate(t, f, tc.cwd, tc.input, tc.deny, "hold the line")
			})
		}
	}
}

// The home walk starts PHYSICAL: a cwd reached through a link into the nested
// repository walked lexically climbed to the link's directory and found no
// ledger (panel, Codex design pass P1). A vanished cwd walks from the deepest
// existing directory (D-102), also under a stale .git file, where resolveRepo
// returns no context to start from.
func TestGateNestedHomeWalkIsPhysicalAndSurvivesAVanishedCwd(t *testing.T) {
	boundedParallel(t)
	f, plain, nested := nestedFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "pause", "bravo", "--note", "hold the line"); code != 0 {
		t.Fatal(errw)
	}
	link := filepath.Join(plain, "link")
	if err := os.Symlink(nested[0], link); err != nil {
		t.Fatal(err)
	}
	wantGate(t, f, link, bashHookJSON("sess-b", link, "touch f"), true, "hold the line")
	wantGate(t, f, link, bashHookJSON("sess-a", link, "touch f"), false, "")
	// A vanished cwd BEHIND that link: climbing the dead path as written
	// reaches the link's own directory and finds no ledger; the kernel's walk
	// (D-102) lands in the nested repository, under R.
	behind := filepath.Join(link, "gone")
	wantGate(t, f, behind, bashHookJSON("sess-b", behind, "touch f"), true, "hold the line")
	wantGate(t, f, behind, bashHookJSON("sess-a", behind, "touch f"), false, "")
	for _, n := range nested[:2] {
		gone := filepath.Join(n, "build")
		if err := os.Mkdir(gone, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(gone); err != nil {
			t.Fatal(err)
		}
		wantGate(t, f, gone, bashHookJSON("sess-b", gone, "touch f"), true, "hold the line")
		wantGate(t, f, gone, bashHookJSON("sess-a", gone, "touch f"), false, "")
	}
}

// With R the home, the refusals that hold wherever a ledger governs hold from
// a nested cwd too: an ambiguous identity and a shell-routed patch.
func TestGateNestedCwdTakesTheGoverningRefusals(t *testing.T) {
	boundedParallel(t)
	f, plain, nested := nestedFixture(t)
	lib := nested[0]
	wantGate(t, f, lib, codexBashHookJSON("sess-b", lib, "apply_patch <<'EOF'\n"+patchText("*** Add File: y.c\n+x\n")+"EOF\n"), true, "apply_patch tool")
	alone := filepath.Join(plain, "alone")
	nestGit(t, alone, "init", "-q")
	wantGate(t, f, alone, codexBashHookJSON("sess-b", alone, "apply_patch <<'EOF'\n"+patchText("*** Add File: y.c\n+x\n")+"EOF\n"), false, "")
	f.env = map[string]string{"CLAUDE_CODE_SESSION_ID": "claude-1", "CODEX_THREAD_ID": "codex-parent"}
	wantGate(t, f, lib, bashHookJSON("claude-1", lib, "make"), true, "ambiguous")
	wantGate(t, f, alone, bashHookJSON("claude-1", alone, "make"), false, "")
}

// Invariant 3 across the walk: an enclosing ledger that exists and cannot be
// read DENIES; a ledger Lstat sees behind a .git git calls "not a git
// repository" DENIES (git's fast path cannot overrule it: Codex design pass,
// P2); a ledger with no worktree root (a bare-layout root above a scratch
// repository) places nothing and is walked past (Fable panel lens).
func TestGateNestedWalkKeepsItsThreeStates(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	plain := t.TempDir()

	broken := filepath.Join(plain, "broken")
	nestGit(t, broken, "init", "-q")
	if err := os.Symlink(filepath.Join(plain, "missing.db"), filepath.Join(broken, ".git", ledgerName)); err != nil {
		t.Fatal(err)
	}
	nestGit(t, filepath.Join(broken, "n"), "init", "-q")
	wantGate(t, f, plain, hookJSON("sess-b", plain, "Edit", filepath.Join(broken, "n/x.c")), true, "unavailable")

	lying := filepath.Join(plain, "lying")
	mustWrite(t, filepath.Join(plain, "x-gitdir", ledgerName), "not sqlite")
	mustWrite(t, filepath.Join(lying, ".git"), "gitdir: "+filepath.Join(plain, "x-gitdir")+"\n")
	nestGit(t, filepath.Join(lying, "n"), "init", "-q")
	wantGate(t, f, filepath.Join(lying, "n"), hookJSON("sess-b", filepath.Join(lying, "n"), "Write", "x.c"), true, "cannot tell")
	// Control: the same pointer with NO ledger file behind it is nothing git
	// or Lstat can see a ledger in: feature-off, as git's own discovery reads it.
	if err := os.Remove(filepath.Join(plain, "x-gitdir", ledgerName)); err != nil {
		t.Fatal(err)
	}
	wantGate(t, f, filepath.Join(lying, "n"), hookJSON("sess-b", filepath.Join(lying, "n"), "Write", "x.c"), false, "")
	// Control for the broken row: a readable ledger above the same shape passes.
	healthy := filepath.Join(plain, "healthy")
	nestGit(t, healthy, "init", "-q")
	if _, errw, code := f.run(t, healthy, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	nestGit(t, filepath.Join(healthy, "n"), "init", "-q")
	wantGate(t, f, plain, hookJSON("sess-b", plain, "Edit", filepath.Join(healthy, "n/x.c")), false, "")

	proj := filepath.Join(plain, "proj")
	nestGit(t, filepath.Join(proj, ".git"), "init", "-q", "--bare")
	if _, errw, code := f.run(t, filepath.Join(proj, ".git"), "", "init"); code != 0 {
		t.Fatal(errw)
	}
	if _, err := os.Lstat(filepath.Join(proj, ".git", ledgerName)); err != nil {
		t.Fatalf("the bare-layout root must hold a ledger for this case: %v", err)
	}
	scratch := filepath.Join(proj, "scratch")
	nestGit(t, scratch, "init", "-q")
	wantGate(t, f, scratch, hookJSON("sess-b", scratch, "Edit", filepath.Join(scratch, "x.c")), false, "")
	wantGate(t, f, plain, hookJSON("sess-b", plain, "Edit", filepath.Join(scratch, "x.c")), false, "")
}

// Once a ledgerless cwd's home is the ledger above it, a base under the cwd's
// own .git is no longer the home's: a BARE repository nested there with its
// own paused ledger keys with the cwd's repository, and skipping that key
// passed `cd cache.git && touch f`, which HEAD denied (Codex design pass, P1).
func TestGateNestedBareRepoUnderANestedCwdKeepsItsBrake(t *testing.T) {
	boundedParallel(t)
	f, _, nested := nestedFixture(t)
	cache := filepath.Join(nested[0], "cache.git")
	nestGit(t, cache, "init", "-q", "--bare")
	if _, errw, code := f.run(t, cache, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := f.run(t, cache, "", "pause", "all", "--note", "cache frozen"); code != 0 {
		t.Fatal(errw)
	}
	wantGate(t, f, nested[0], bashHookJSON("sess-b", nested[0], "cd cache.git && touch f"), true, "cache frozen")
	wantGate(t, f, nested[0], bashHookJSON("sess-b", nested[0], "cd "+f.repo+" && touch f"), false, "")
}

// gh stack from a nested cwd: the walked ledger's brake applies, its slot does
// not (the stack catalog is the nested repository's own; R's slot serializes
// nothing there). Control: the same command from R needs R's slot.
func TestGateGHStackFromANestedCwdNeedsNoEnclosingSlot(t *testing.T) {
	boundedParallel(t)
	f, plain, nested := nestedFixture(t)
	lib := nested[0]
	wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, "gh stack rebase"), true, "gh-stack")
	wantGate(t, f, lib, bashHookJSON("sess-b", lib, "gh stack rebase"), false, "")
	wantGate(t, f, plain, bashHookJSON("sess-b", plain, "cd "+lib+" && gh stack rebase"), false, "")
	if _, errw, code := f.run(t, f.repo, "", "pause", "bravo", "--note", "hold the line"); code != 0 {
		t.Fatal(errw)
	}
	wantGate(t, f, lib, bashHookJSON("sess-b", lib, "gh stack rebase"), true, "hold the line")
}

// A nested repository WITH its own ledger stays under the enclosing one's
// claims and brake from every cwd (#145, D-226): D-212 recorded the cwd=gov
// and plain-cwd rows open, and they ALLOWED (A's `vendor` claim in R did not
// reach x.c; R's `pause all` did not stop `touch f` in gov). Positive
// controls: the holder A from every cwd, and gov's own sessions unpaused.
func TestGateLedgerBearingNestedRepoRows(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	plain := t.TempDir()
	gov := filepath.Join(f.repo, "vendor/gov")
	nestGit(t, gov, "init", "-q")
	if _, errw, code := f.run(t, gov, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	mustClaim(t, f, f.repo, "vend", "sess-a", "vendor")
	x := filepath.Join(gov, "x.c")
	wantGate(t, f, f.repo, hookJSON("sess-b", f.repo, "Edit", x), true, "vend")
	wantGate(t, f, f.repo, hookJSON("sess-a", f.repo, "Edit", x), false, "")
	wantGate(t, f, gov, hookJSON("sess-b", gov, "Edit", x), true, "vend")
	wantGate(t, f, plain, hookJSON("sess-b", plain, "Edit", x), true, "vend")
	wantGate(t, f, gov, hookJSON("sess-a", gov, "Edit", x), false, "")
	wantGate(t, f, plain, hookJSON("sess-a", plain, "Edit", x), false, "")
	wantGate(t, f, gov, bashHookJSON("sess-b", gov, "touch f"), false, "")
	if _, errw, code := f.run(t, f.repo, "", "pause", "all"); code != 0 {
		t.Fatal(errw)
	}
	wantGate(t, f, gov, bashHookJSON("sess-b", gov, "touch f"), true, "paused")
	wantGate(t, f, plain, bashHookJSON("sess-b", plain, "cd "+gov+" && touch f"), true, "paused")
	wantGate(t, f, f.repo, bashHookJSON("sess-b", f.repo, "touch f"), true, "paused")
}

// The reached-brake bound counts git discoveries: four reached keys, each a
// ledgerless repository nested in a distinct governed one, cost eight, and
// all four are read (the fourth's brake denies). Control: unpaused, it passes.
func TestGateReachedBrakesReadFourNestedKeys(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	plain := t.TempDir()
	var cmd []string
	for i, name := range []string{"r1", "r2", "r3", "r4"} {
		r := filepath.Join(plain, name)
		nestGit(t, r, "init", "-q")
		if _, errw, code := f.run(t, r, "", "init"); code != 0 {
			t.Fatal(errw)
		}
		n := filepath.Join(r, "n")
		nestGit(t, n, "init", "-q")
		if i == 3 {
			if _, errw, code := f.run(t, r, "", "pause", "all", "--note", "fourth"); code != 0 {
				t.Fatal(errw)
			}
		}
		cmd = append(cmd, "cd "+n+" && true")
	}
	wantGate(t, f, plain, bashHookJSON("sess-b", plain, strings.Join(cmd, "; ")), true, "fourth")
	// Control: the same four, unpaused, pass.
	if _, errw, code := f.run(t, filepath.Join(plain, "r4"), "", "resume", "all"); code != 0 {
		t.Fatal(errw)
	}
	wantGate(t, f, plain, bashHookJSON("sess-b", plain, strings.Join(cmd, "; ")), false, "")
}

// One gate call opens an enclosing ledger once: a target in R, then one in
// R's ledgerless nested repository, costs one more discovery (the nested
// repository's own), and the nested key aliases R's handle, which the
// deferred close then closes once (Codex design pass, Q5; latency lens).
func TestGateCacheOpensTheEnclosingLedgerOnce(t *testing.T) {
	boundedParallel(t)
	f, _, nested := nestedFixture(t)
	c := &gateCache{m: map[string]gateRepo{}}
	defer func() {
		for _, r := range c.m {
			if r.st != nil {
				r.st.Close()
			}
		}
	}()
	env := Env{Getenv: func(string) string { return "" }}
	h := hookInput{SessionID: "sess-b"}
	rdir, rkey := repoKeyForDir(canon(f.repo))
	first := openGateRepo(h, env, rdir, rkey, c)
	if first.st == nil || first.walked {
		t.Fatalf("R must open directly: %+v", first)
	}
	opens := c.opens
	ndir, nkey := repoKeyForDir(canon(nested[0]))
	got := openGateRepo(h, env, ndir, nkey, c)
	if got.st != first.st || !got.walked {
		t.Fatalf("the nested key must alias R's handle: got %p walked=%v, want %p", got.st, got.walked, first.st)
	}
	if c.opens != opens+1 {
		t.Fatalf("one discovery for the nested repository, none for R again: opens %d -> %d", opens, c.opens)
	}
	if c.m[nkey].via != rkey || c.m[nkey].st != nil {
		t.Fatalf("the alias must own nothing and name R's key: %+v", c.m[nkey])
	}
}

// The walk from a target in the home's ledgerless nested repository stops at
// the home's own .git (os.SameFile): the home arms read its claims and brake
// already, and the home ledger is not opened a second time. The verdict is
// the same either way, so only the count can see the stop.
func TestGateWalkStopsAtTheHome(t *testing.T) {
	boundedParallel(t)
	f, _, nested := nestedFixture(t)
	fi, err := os.Stat(filepath.Join(f.repo, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	ti, err := os.Stat(f.repo)
	if err != nil {
		t.Fatal(err)
	}
	c := &gateCache{m: map[string]gateRepo{}, home: fi, homeTop: ti}
	defer func() {
		for _, r := range c.m {
			if r.st != nil {
				r.st.Close()
			}
		}
	}()
	env := Env{Getenv: func(string) string { return "" }}
	ndir, nkey := repoKeyForDir(canon(nested[0]))
	got := openGateRepo(hookInput{SessionID: "sess-b"}, env, ndir, nkey, c)
	if !got.settled || got.st != nil || !got.off() {
		t.Fatalf("the walk must stop at the home, settled and holding nothing: %+v", got)
	}
	if c.opens != 1 {
		t.Fatalf("one discovery (the nested repository's), never the home's again: opens=%d", c.opens)
	}
	// Control: with no home, the same walk opens R and governs.
	c2 := &gateCache{m: map[string]gateRepo{}}
	defer func() {
		for _, r := range c2.m {
			if r.st != nil {
				r.st.Close()
			}
		}
	}()
	if got := openGateRepo(hookInput{SessionID: "sess-b"}, env, ndir, nkey, c2); got.st == nil || !got.walked || c2.opens != 2 {
		t.Fatalf("control: with no home the walk must open R: st=%v walked=%v opens=%d", got.st != nil, got.walked, c2.opens)
	}
}

// The Codex code pass's folds, one test each (#121, D-212).

// A relative `gitdir:` pointer through a link is resolved PHYSICALLY: with
// jump a link elsewhere, `gitdir: jump/../admin` names the link's parent's
// admin, where R's ledger is; a lexical join named <R>/admin, absent, and
// the walk skipped R without asking git (P1).
func TestGateNestedWalkResolvesAPointerPhysically(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	plain := canon(t.TempDir())
	r := filepath.Join(plain, "R")
	g := filepath.Join(plain, "G")
	if err := os.MkdirAll(g, 0o755); err != nil {
		t.Fatal(err)
	}
	nestGit(t, plain, "init", "-q", "--separate-git-dir", filepath.Join(g, "admin"), r)
	if err := os.MkdirAll(filepath.Join(g, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(g, "child"), filepath.Join(r, "jump")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(r, ".git"), "gitdir: jump/../admin\n")
	if _, errw, code := f.run(t, r, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	if _, err := os.Lstat(filepath.Join(g, "admin", ledgerName)); err != nil {
		t.Fatalf("the ledger must live behind the pointer: %v", err)
	}
	if _, errw, code := f.run(t, r, hookJSON("sess-a", r, "", ""), "hello", "--label", "alpha"); code != 0 {
		t.Fatal(errw)
	}
	mustClaim(t, f, r, "vend", "sess-a", "vendor")
	n := filepath.Join(r, "vendor/N")
	nestGit(t, n, "init", "-q")
	wantGate(t, f, n, hookJSON("sess-b", n, "Write", "x.c"), true, "vend")
	wantGate(t, f, n, hookJSON("sess-a", n, "Write", "x.c"), false, "")
}

// The home stop needs the home's worktree directory, not only its .git: S/.git
// a link to R/.git shares R's git directory under another root, and a target
// in S's ledgerless nested repository was settled at S unread (P1).
func TestGateNestedWalkStopsOnlyAtTheHomeRoot(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	mustClaim(t, f, f.repo, "vend", "sess-a", "vendor")
	s := filepath.Join(t.TempDir(), "S")
	if err := os.MkdirAll(s, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(f.repo, ".git"), filepath.Join(s, ".git")); err != nil {
		t.Fatal(err)
	}
	n := filepath.Join(s, "vendor/N")
	nestGit(t, n, "init", "-q")
	x := filepath.Join(n, "x.c")
	wantGate(t, f, f.repo, hookJSON("sess-b", f.repo, "Edit", x), true, "vend")
	wantGate(t, f, f.repo, hookJSON("sess-a", f.repo, "Edit", x), false, "")
}

// A walked governor whose key a bare repository already holds (D-138's key
// collision) gets a key of its own: it was discarded for the bare ledger,
// whose brake is clear, and leaked (P2).
func TestGateNestedWalkKeepsAGovernorItsKeyCollidesWith(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	cache := filepath.Join(f.repo, "cache.git")
	nestGit(t, cache, "init", "-q", "--bare")
	if _, errw, code := f.run(t, cache, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	n := filepath.Join(f.repo, "vendor/N")
	nestGit(t, n, "init", "-q")
	if _, errw, code := f.run(t, f.repo, "", "pause", "bravo", "--note", "hold the line"); code != 0 {
		t.Fatal(errw)
	}
	plain := t.TempDir()
	cmd := "cd " + cache + "; cd " + n + "; touch x"
	wantGate(t, f, plain, bashHookJSON("sess-b", plain, cmd), true, "hold the line")
	wantGate(t, f, plain, bashHookJSON("sess-a", plain, cmd), false, "")
}

// git's discovery that SUCCEEDS and finds no ledger is proof of absence: a
// symlinked .git FILE (which ledgerAbsentAt does not prove) above a ledgerless
// repository was "cannot tell" and denied a call nothing governs (P2).
func TestGateNestedWalkTrustsAnAbsentLedgerGitFound(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	plain := canon(t.TempDir())
	a := filepath.Join(plain, "A")
	nestGit(t, plain, "init", "-q", "--separate-git-dir", filepath.Join(plain, "admin"), a)
	if err := os.Rename(filepath.Join(a, ".git"), filepath.Join(plain, "gitfile")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(plain, "gitfile"), filepath.Join(a, ".git")); err != nil {
		t.Fatal(err)
	}
	n := filepath.Join(a, "N")
	nestGit(t, n, "init", "-q")
	wantGate(t, f, n, hookJSON("sess-b", n, "Write", "x.c"), false, "")
	// Control: with a ledger behind that file, it governs.
	if _, errw, code := f.run(t, a, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := f.run(t, a, "", "pause", "all", "--note", "behind the link"); code != 0 {
		t.Fatal(errw)
	}
	wantGate(t, f, n, hookJSON("sess-b", n, "Write", "x.c"), true, "behind the link")
}

// A relative hook cwd is the process's own: the walk starts from it (P2). Not
// parallel: it changes the process's directory.
func TestGateNestedWalkFromARelativeCwd(t *testing.T) {
	f, _, nested := nestedFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "pause", "bravo", "--note", "hold the line"); code != 0 {
		t.Fatal(errw)
	}
	t.Chdir(nested[0])
	wantGate(t, f, nested[0], bashHookJSON("sess-b", ".", "touch x"), true, "hold the line")
	wantGate(t, f, nested[0], bashHookJSON("sess-a", ".", "touch x"), false, "")
}

// The reached-brake budget binds inside a walk too (P3): a ledgerless N under
// eight bare-layout roots (each a ledger with no worktree) inside a paused R
// spends the eight discoveries before R is reached, and R is not read, like
// any directory past the bound (D-138). Control: under six, R is read.
func TestGateReachedBrakeBudgetBindsInsideAWalk(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "pause", "all", "--note", "deep"); code != 0 {
		t.Fatal(errw)
	}
	chain := func(root string, depth int) string {
		dir := root
		for i := 0; i < depth; i++ {
			dir = filepath.Join(dir, "b")
			nestGit(t, filepath.Join(dir, ".git"), "init", "-q", "--bare")
			if _, errw, code := f.run(t, filepath.Join(dir, ".git"), "", "init"); code != 0 {
				t.Fatal(errw)
			}
		}
		n := filepath.Join(dir, "n")
		nestGit(t, n, "init", "-q")
		return n
	}
	plain := t.TempDir()
	shallow := chain(filepath.Join(f.repo, "s"), 6)
	wantGate(t, f, plain, bashHookJSON("sess-b", plain, "cd "+shallow+" && touch f"), true, "deep")
	deep := chain(filepath.Join(f.repo, "d"), 8)
	wantGate(t, f, plain, bashHookJSON("sess-b", plain, "cd "+deep+" && touch f"), false, "")
	// The same N as a file target has no budget: depth bounds it.
	wantGate(t, f, plain, hookJSON("sess-b", plain, "Edit", filepath.Join(deep, "x.c")), true, "deep")
}

// Re-verify round 1's folds.

// A collision where the walk found an ERROR (R's ledger a dangling link)
// keeps the error, which denies; it aliased the healthy bare ledger and passed.
func TestGateNestedWalkKeepsAnErrorItsKeyCollidesWith(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	cache := filepath.Join(f.repo, "cache.git")
	nestGit(t, cache, "init", "-q", "--bare")
	if _, errw, code := f.run(t, cache, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	n := filepath.Join(f.repo, "vendor/N")
	nestGit(t, n, "init", "-q")
	plain := t.TempDir()
	cmd := "cd " + cache + "; cd " + n + "; touch x"
	// Control: R's ledger healthy and unpaused, the same command passes.
	wantGate(t, f, plain, bashHookJSON("sess-b", plain, cmd), false, "")
	ledger := filepath.Join(f.repo, ".git", ledgerName)
	for _, side := range []string{"", "-wal", "-shm"} {
		os.Remove(ledger + side)
	}
	if err := os.Symlink(filepath.Join(plain, "missing.db"), ledger); err != nil {
		t.Fatal(err)
	}
	wantGate(t, f, plain, bashHookJSON("sess-b", plain, cmd), true, "unavailable")
}

// A relative cwd holding `..` after a link is resolved after the link: a
// lexical clean climbed to the process cwd and missed R. Not parallel.
func TestGateNestedWalkFromARelativeCwdThroughALink(t *testing.T) {
	f, _, nested := nestedFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "pause", "bravo", "--note", "hold the line"); code != 0 {
		t.Fatal(errw)
	}
	plain := canon(t.TempDir())
	sub := filepath.Join(nested[0], "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sub, filepath.Join(plain, "jump")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(plain)
	wantGate(t, f, plain, bashHookJSON("sess-b", "jump/..", "touch x"), true, "hold the line")
	wantGate(t, f, plain, bashHookJSON("sess-a", "jump/..", "touch x"), false, "")
}

// An exhausted shell-brake budget is not a proved absence: an Edit whose hook
// also carries a `command` naming the same directory is adjudicated unbudgeted.
func TestGateExhaustedBudgetIsNotCachedAsAbsence(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "pause", "all", "--note", "deep"); code != 0 {
		t.Fatal(errw)
	}
	dir := filepath.Join(f.repo, "d")
	for i := 0; i < 8; i++ {
		dir = filepath.Join(dir, "b")
		nestGit(t, filepath.Join(dir, ".git"), "init", "-q", "--bare")
		if _, errw, code := f.run(t, filepath.Join(dir, ".git"), "", "init"); code != 0 {
			t.Fatal(errw)
		}
	}
	n := filepath.Join(dir, "n")
	nestGit(t, n, "init", "-q")
	plain := t.TempDir()
	in, _ := json.Marshal(map[string]any{
		"session_id": "sess-b", "cwd": plain, "tool_name": "Edit",
		"tool_input": map[string]any{"file_path": filepath.Join(n, "x.c"), "command": "cd " + n + " && true"},
	})
	wantGate(t, f, plain, string(in), true, "deep")
	if _, errw, code := f.run(t, f.repo, "", "resume", "all"); code != 0 {
		t.Fatal(errw)
	}
	wantGate(t, f, plain, string(in), false, "")
}

// The Fable cross-check's folds.

// A .git nothing can be read behind (a dangling link, a stale pointer behind a
// link, one git refuses) with no ledger file seen is feature-off, as git's own
// discovery reads it: the walk's "cannot tell" refused every call below it,
// in no repository at all. And a symlinked .git directory to a repository git
// refuses (format 99) with no ledger is proved absent through the link.
func TestGateNestedWalkSeesNoLedgerBehindAnUnreadableGit(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	plain := canon(t.TempDir())
	x := filepath.Join(plain, "X")
	if err := os.MkdirAll(filepath.Join(x, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(plain, "no-such-drive", "x.git"), filepath.Join(x, ".git")); err != nil {
		t.Fatal(err)
	}
	nestGit(t, filepath.Join(x, "n"), "init", "-q")
	y := filepath.Join(plain, "Y")
	mustWrite(t, filepath.Join(plain, "stale-gitfile"), "gitdir: "+filepath.Join(plain, "gone")+"\n")
	if err := os.MkdirAll(y, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(plain, "stale-gitfile"), filepath.Join(y, ".git")); err != nil {
		t.Fatal(err)
	}
	nestGit(t, filepath.Join(y, "n"), "init", "-q")
	z := filepath.Join(plain, "Z")
	nestGit(t, filepath.Join(plain, "donor"), "init", "-q")
	nestGit(t, filepath.Join(plain, "donor"), "config", "core.repositoryformatversion", "99")
	if err := os.MkdirAll(z, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(plain, "donor/.git"), filepath.Join(z, ".git")); err != nil {
		t.Fatal(err)
	}
	nestGit(t, filepath.Join(z, "n"), "init", "-q")
	for _, root := range []string{x, y, z} {
		n := filepath.Join(root, "n")
		wantGate(t, f, n, hookJSON("sess-b", n, "Write", "a.c"), false, "")
		wantGate(t, f, plain, hookJSON("sess-b", plain, "Edit", filepath.Join(n, "a.c")), false, "")
		wantGate(t, f, plain, bashHookJSON("sess-b", plain, "cd "+n+" && touch f"), false, "")
	}
	wantGate(t, f, filepath.Join(x, "sub"), bashHookJSON("sess-b", filepath.Join(x, "sub"), "ls"), false, "")
}

// Inside D-138's bare-key collision the walk must not hide a paused bare
// repository: from a nested cwd (the walked home's key is the bare one's too),
// and after a walk from a nested repository stored R under that key.
func TestGateNestedWalkLeavesABareRepositorysBrake(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	cache := filepath.Join(f.repo, "cache.git")
	nestGit(t, cache, "init", "-q", "--bare")
	if _, errw, code := f.run(t, cache, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := f.run(t, cache, "", "pause", "all", "--note", "cache frozen"); code != 0 {
		t.Fatal(errw)
	}
	n := filepath.Join(f.repo, "vendor/N1")
	nestGit(t, n, "init", "-q")
	plain := t.TempDir()
	wantGate(t, f, n, bashHookJSON("sess-b", n, "cd "+cache+" && touch x"), true, "cache frozen")
	wantGate(t, f, plain, bashHookJSON("sess-b", plain, "cd "+n+"; cd "+cache+"; touch x"), true, "cache frozen")
	// Controls: unpaused, both pass.
	if _, errw, code := f.run(t, cache, "", "resume", "all"); code != 0 {
		t.Fatal(errw)
	}
	wantGate(t, f, n, bashHookJSON("sess-b", n, "cd "+cache+" && touch x"), false, "")
	wantGate(t, f, plain, bashHookJSON("sess-b", plain, "cd "+n+"; cd "+cache+"; touch x"), false, "")
}

// commonDirOf appends every relative piece raw, so the kernel resolves `..`
// after a link: a gitdir pointer, the commondir file read beside it, and its
// relative common dir, through `jump`, a link elsewhere. Each lexical join
// (filepath.Join) names a directory with no ledger and proves absence wrongly.
func TestCommonDirOfResolvesEveryPiecePhysically(t *testing.T) {
	t.Parallel()
	plain := canon(t.TempDir())
	r, g := filepath.Join(plain, "R"), filepath.Join(plain, "G")
	mustWrite(t, filepath.Join(g, "wt", "commondir"), "../common\n")
	mustWrite(t, filepath.Join(g, "common", ledgerName), "x")
	if err := os.MkdirAll(filepath.Join(g, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(r, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(g, "child"), filepath.Join(r, "jump")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(r, ".git"), "gitdir: jump/../wt\n")
	if ledgerAbsentAt(filepath.Join(r, ".git")) {
		t.Fatal("ledgerAbsentAt: the ledger behind jump/../wt/../common is there")
	}
	if !ledgerSeenAt(filepath.Join(r, ".git")) {
		t.Fatal("ledgerSeenAt: the ledger behind jump/../wt/../common is there")
	}
	if ledgerProvablyAbsent(filepath.Join(r, "sub")) {
		t.Fatal("ledgerProvablyAbsent: the ledger behind jump/../wt/../common is there")
	}
	// Control: with the ledger gone, each proves absence.
	if err := os.Remove(filepath.Join(g, "common", ledgerName)); err != nil {
		t.Fatal(err)
	}
	if !ledgerAbsentAt(filepath.Join(r, ".git")) || !ledgerProvablyAbsent(filepath.Join(r, "sub")) || ledgerSeenAt(filepath.Join(r, ".git")) {
		t.Fatal("control: with no ledger behind the pointer, absence is proved and nothing is seen")
	}
}

// A .git git fails at, with no ledger seen, is walked PAST, not stopped at:
// under a governed R, R's claim and brake still apply (Codex re-verify, round 2).
func TestGateNestedWalkContinuesPastAnUnreadableGit(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	mustClaim(t, f, f.repo, "vend", "sess-a", "vendor")
	x := filepath.Join(f.repo, "vendor/X")
	if err := os.MkdirAll(x, 0o755); err != nil {
		t.Fatal(err)
	}
	// A stale pointer file behind a link: git FAILS there ("not a git
	// repository") and sees no ledger. (A dangling link git itself skips,
	// finding R, so it cannot reach this arm.)
	stale := filepath.Join(t.TempDir(), "stale-gitfile")
	mustWrite(t, stale, "gitdir: "+filepath.Join(filepath.Dir(stale), "gone")+"\n")
	if err := os.Symlink(stale, filepath.Join(x, ".git")); err != nil {
		t.Fatal(err)
	}
	n := filepath.Join(x, "N")
	nestGit(t, n, "init", "-q")
	plain := t.TempDir()
	wantGate(t, f, plain, hookJSON("sess-b", plain, "Edit", filepath.Join(n, "x.c")), true, "vend")
	wantGate(t, f, n, hookJSON("sess-b", n, "Write", "x.c"), true, "vend")
	wantGate(t, f, plain, hookJSON("sess-a", plain, "Edit", filepath.Join(n, "x.c")), false, "")
	if _, errw, code := f.run(t, f.repo, "", "pause", "bravo", "--note", "hold the line"); code != 0 {
		t.Fatal(errw)
	}
	wantGate(t, f, plain, bashHookJSON("sess-b", plain, "cd "+n+" && touch f"), true, "hold the line")
	wantGate(t, f, plain, bashHookJSON("sess-a", plain, "cd "+n+" && touch f"), false, "")
}

// git 2.56 words a `gitdir:` file pointing at a non-repository "gitfile does
// not point to a valid repository: <.git>", where 2.54 (macOS) and 2.53 said
// "not a git repository: <dir>" (measured in Ubuntu 26.04 containers). Matching
// only the old text, discovery read the new one as a real failure: CI's 2.56.1
// denied the D-212 walk's rows and a cwd under a stale pointer behind a link.
// A shim ahead of git on PATH rewrites the old wording to 2.56's, so this runs
// the CI runner's discovery on any git; the shim is checked to be armed first.
func TestGateReadsGit256sNotARepositoryWording(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	f := newFixture(t)
	f.initAndHello(t)
	mustClaim(t, f, f.repo, "vend", "sess-a", "vendor")
	plain := canon(t.TempDir())
	stale := filepath.Join(plain, "stale-gitfile")
	mustWrite(t, stale, "gitdir: "+filepath.Join(plain, "gone")+"\n")

	// Home arm: a cwd under a stale pointer behind a link, nothing governing.
	y := filepath.Join(plain, "Y")
	if err := os.MkdirAll(filepath.Join(y, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(stale, filepath.Join(y, ".git")); err != nil {
		t.Fatal(err)
	}
	// Walk: the same shape inside governed R, a repository below it.
	x := filepath.Join(f.repo, "vendor/X")
	if err := os.MkdirAll(x, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(stale, filepath.Join(x, ".git")); err != nil {
		t.Fatal(err)
	}
	n := filepath.Join(x, "N")
	nestGit(t, n, "init", "-q")
	// A pointer to a directory holding only a ledger file: "cannot tell".
	lying := filepath.Join(plain, "lying")
	mustWrite(t, filepath.Join(plain, "x-gitdir", ledgerName), "not sqlite")
	mustWrite(t, filepath.Join(lying, ".git"), "gitdir: "+filepath.Join(plain, "x-gitdir")+"\n")
	nestGit(t, filepath.Join(lying, "n"), "init", "-q")

	shimDir := t.TempDir()
	shim := "#!/bin/sh\nerr=$(mktemp)\n" + realGit + " \"$@\" 2>\"$err\"\nrc=$?\n" +
		"sed 's/^fatal: not a git repository: \\(.*\\)$/fatal: gitfile does not point to a valid repository: \\1/' \"$err\" >&2\n" +
		"rm -f \"$err\"\nexit $rc\n"
	if err := os.WriteFile(filepath.Join(shimDir, "git"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Armed: the shim prints 2.56's text for the stale pointer.
	probe := exec.Command("git", "-C", y, "rev-parse", "--git-common-dir")
	probe.Env = append(os.Environ(), "LC_ALL=C") // as revParse pins it (Codex code pass)
	out, _ := probe.CombinedOutput()
	if !strings.Contains(string(out), "gitfile does not point to a valid repository") || strings.Contains(string(out), "not a git repository") {
		t.Fatalf("the shim must print git 2.56's wording, got %q", out)
	}

	wantGate(t, f, filepath.Join(y, "sub"), hookJSON("sess-b", filepath.Join(y, "sub"), "Write", "x.c"), false, "")
	wantGate(t, f, filepath.Join(y, "sub"), bashHookJSON("sess-b", filepath.Join(y, "sub"), "ls"), false, "")
	wantGate(t, f, plain, hookJSON("sess-b", plain, "Edit", filepath.Join(n, "x.c")), true, "vend")
	wantGate(t, f, n, hookJSON("sess-b", n, "Write", "x.c"), true, "vend")
	wantGate(t, f, plain, hookJSON("sess-a", plain, "Edit", filepath.Join(n, "x.c")), false, "")
	wantGate(t, f, filepath.Join(lying, "n"), hookJSON("sess-b", filepath.Join(lying, "n"), "Write", "x.c"), true, "cannot tell")
	// Control: with no ledger file behind the pointer, feature-off. An absence
	// control, not wording coverage: ledgerAbsentAt skips the entry unasked.
	if err := os.Remove(filepath.Join(plain, "x-gitdir", ledgerName)); err != nil {
		t.Fatal(err)
	}
	wantGate(t, f, filepath.Join(lying, "n"), hookJSON("sess-b", filepath.Join(lying, "n"), "Write", "x.c"), false, "")
}

// git 2.56's text is read only as the start of git's first fatal line: git
// echoes paths, and a gitdir under a directory NAMED that text, whose
// commondir cannot be read, with a ledger behind it, fails `fatal: failed to
// read <...>/commondir: Permission denied`. Read anywhere in stderr, that was
// feature-off and a Write from the repository passed (Codex code pass, P1).
// Control: the commondir readable and no ledger, the same layout allows.
func TestGateDoesNotReadGit256sWordingInAnEchoedPath(t *testing.T) {
	boundedParallel(t)
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	f := newFixture(t)
	plain := canon(t.TempDir())
	wt := filepath.Join(plain, "gitfile does not point to a valid repository", "wt")
	common := filepath.Join(plain, "common")
	for _, d := range []string{wt, filepath.Join(common, "objects"), filepath.Join(common, "refs")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, filepath.Join(wt, "HEAD"), "ref: refs/heads/main\n")
	mustWrite(t, filepath.Join(wt, "commondir"), common+"\n")
	mustWrite(t, filepath.Join(common, ledgerName), "x")
	r := filepath.Join(plain, "R")
	mustWrite(t, filepath.Join(r, ".git"), "gitdir: "+wt+"\n")
	if err := os.Chmod(filepath.Join(wt, "commondir"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(wt, "commondir"), 0o644) })
	wantGate(t, f, r, hookJSON("sess-b", r, "Write", "x.c"), true, "unavailable")
	if err := os.Chmod(filepath.Join(wt, "commondir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(common, ledgerName)); err != nil {
		t.Fatal(err)
	}
	wantGate(t, f, r, hookJSON("sess-b", r, "Write", "x.c"), false, "")
}
