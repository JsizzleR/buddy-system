package cli

// Tests for #145 (D-226): every distinct governed ancestor of a directory
// applies its claims and brake. Each refusal sits beside its positive control
// (the holder, unpaused, or nothing governing above), and each was watched to
// FAIL on 1bfdf45 first unless its comment says it is a control that base
// already passes.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func ancestorPatchJSON(session, cwd string, files ...string) string {
	var b strings.Builder
	b.WriteString("*** Begin Patch\n")
	for _, f := range files {
		b.WriteString("*** Update File: " + f + "\n@@\n-a\n+b\n")
	}
	b.WriteString("*** End Patch\n")
	out, _ := json.Marshal(map[string]any{"session_id": session, "hook_event_name": "PreToolUse", "cwd": cwd,
		"tool_name": "apply_patch", "tool_input": map[string]any{"command": b.String()}})
	return string(out)
}

func initLedger(t *testing.T, f *fixture, dir string) {
	t.Helper()
	if _, errw, code := f.run(t, dir, "", "init"); code != 0 {
		t.Fatalf("init in %s: %s", dir, errw)
	}
}

// ancestorFixture: governed R (f.repo), A (sess-a) holding `src` and
// `vendor`, and in R/vendor a ledger-bearing nested repository reached each
// way #145 found: `git init` + `buddy init` (gov), a worktree of a governed
// repository outside R (sw), a governed repository copied in (cp), one
// whose core.worktree is R (cw), and gov2 inside gov.
func ancestorFixture(t *testing.T) (f *fixture, plain string, n map[string]string) {
	t.Helper()
	f = newFixture(t)
	f.initAndHello(t)
	mustClaim(t, f, f.repo, "held", "sess-a", "src", "vendor")
	plain = canon(t.TempDir())
	n = map[string]string{}
	gov := filepath.Join(f.repo, "vendor/gov")
	nestGit(t, gov, "init", "-q")
	initLedger(t, f, gov)
	n["gov"] = gov
	gov2 := filepath.Join(gov, "gov2")
	nestGit(t, gov2, "init", "-q")
	initLedger(t, f, gov2)
	n["gov2"] = gov2
	s := filepath.Join(plain, "S")
	nestGit(t, s, "init", "-q")
	nestGit(t, s, "commit", "-q", "--allow-empty", "-m", "s")
	initLedger(t, f, s)
	sw := filepath.Join(f.repo, "vendor/sw")
	nestGit(t, s, "worktree", "add", "-q", sw)
	n["sw"], n["S"] = sw, s
	c := filepath.Join(plain, "C")
	nestGit(t, c, "init", "-q")
	initLedger(t, f, c)
	cp := filepath.Join(f.repo, "vendor/cp")
	if out, err := exec.Command("cp", "-R", c, cp).CombinedOutput(); err != nil {
		t.Fatalf("cp -R: %v %s", err, out)
	}
	n["cp"] = cp
	cw := filepath.Join(f.repo, "vendor/cw")
	nestGit(t, cw, "init", "-q")
	nestGit(t, cw, "config", "core.worktree", "../../..")
	n["cw"] = cw
	return f, plain, n
}

// The issue's rows and every way to a ledger-bearing nested repository: A's
// claim in R refuses B's Edit from the nested cwd, a relative path there, and
// a plain cwd, as it already did from cwd R (D-073 F-03). Controls: the
// holder passes from every cwd, and an untouched file outside every claim.
func TestGateAncestorClaimsReachALedgerBearingNestedRepo(t *testing.T) {
	boundedParallel(t)
	f, plain, n := ancestorFixture(t)
	for _, name := range []string{"gov", "gov2", "sw", "cp"} {
		dir := n[name]
		x := filepath.Join(dir, "x.c")
		for _, cwd := range []string{f.repo, dir, plain} {
			wantGate(t, f, cwd, hookJSON("sess-b", cwd, "Edit", x), true, "held")
			wantGate(t, f, cwd, hookJSON("sess-a", cwd, "Edit", x), false, "")
		}
		wantGate(t, f, dir, hookJSON("sess-b", dir, "Write", "rel.c"), true, "held")
		wantGate(t, f, plain, ancestorPatchJSON("sess-b", plain, x), true, "held")
	}
	// The deny names the root the claim is made from.
	wantGate(t, f, n["gov"], hookJSON("sess-b", n["gov"], "Edit", filepath.Join(n["gov"], "x.c")), true, "(in "+canon(f.repo))
	// Control: a file under no claim, from the nested cwd.
	wantGate(t, f, n["gov"], hookJSON("sess-b", n["gov"], "Edit", filepath.Join(f.repo, "README")), false, "")
}

// A nested ledger whose core.worktree is R makes git name R its root: from
// that cwd, A's claim on `src` in R's own ledger refuses R/src/a.go (panel lens
// a #1: a walk from `Dir(top)` keyed `<top>/.git` never opened R).
func TestGateAncestorWalkStartsAtThePhysicalDirectory(t *testing.T) {
	boundedParallel(t)
	f, _, n := ancestorFixture(t)
	initLedger(t, f, n["cw"])
	a := filepath.Join(f.repo, "src/a.go")
	wantGate(t, f, n["cw"], hookJSON("sess-b", n["cw"], "Edit", a), true, "held")
	wantGate(t, f, n["cw"], hookJSON("sess-a", n["cw"], "Edit", a), false, "")
}

// R's brake reaches a session in a ledger-bearing nested repository and in a
// ledger-bearing BARE repository inside R, path-less, by `cd` from a plain
// cwd, and through a file tool. Control: resumed, each passes.
func TestGateAncestorBrakeReachesLedgerBearingNestedRepos(t *testing.T) {
	boundedParallel(t)
	f, plain, n := ancestorFixture(t)
	cache := filepath.Join(f.repo, "cache.git")
	nestGit(t, cache, "init", "-q", "--bare")
	initLedger(t, f, cache)
	if _, errw, code := f.run(t, f.repo, "", "pause", "all", "--note", "R frozen"); code != 0 {
		t.Fatal(errw)
	}
	check := func(deny bool, want string) {
		t.Helper()
		for _, dir := range []string{n["gov"], n["gov2"], n["sw"], cache} {
			wantGate(t, f, dir, bashHookJSON("sess-b", dir, "touch f"), deny, want)
			wantGate(t, f, plain, bashHookJSON("sess-b", plain, "cd "+dir+" && touch f"), deny, want)
		}
		// The holder A: `pause all` stops it too, and resumed, its claim is no bar.
		wantGate(t, f, plain, hookJSON("sess-a", plain, "Edit", filepath.Join(n["gov"], "free.c")), deny, want)
	}
	check(true, "R frozen")
	if _, errw, code := f.run(t, f.repo, "", "resume", "all"); code != 0 {
		t.Fatal(errw)
	}
	check(false, "")
}

// R's own registered linked worktree inside R (Claude Code's
// `.claude/worktrees/<lane>`) is placed against its own root only (Q2 (i)):
// a claim on `.claude` in R no longer refuses the lane's files from cwd R
// (base: DENY from cwd R only). An ALIAS of R's .git (a symlink, a
// hand-written `gitdir: ../../.git`) is no registration: placed against both
// roots, it refuses from every cwd. Control: R's own `.claude/x` stays held.
func TestGateRegisteredWorktreeIsPlacedAgainstItsOwnRoot(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	plain := canon(t.TempDir())
	mustClaim(t, f, f.repo, "dotc", "sess-a", ".claude", "vendor")
	lane := filepath.Join(f.repo, ".claude/worktrees/lane")
	nestGit(t, f.repo, "worktree", "add", "-q", lane)
	readme := filepath.Join(lane, "README")
	for _, cwd := range []string{f.repo, lane, plain} {
		wantGate(t, f, cwd, hookJSON("sess-b", cwd, "Edit", readme), false, "")
	}
	wantGate(t, f, f.repo, hookJSON("sess-b", f.repo, "Edit", filepath.Join(f.repo, ".claude/x")), true, "dotc")
	// A registered worktree whose discovery names ANOTHER root (a per-worktree
	// core.worktree of R/.claude, which contains it) is not placed by that
	// root alone: the scope `.claude` would never match `worktrees/...`.
	odd := filepath.Join(f.repo, ".claude/worktrees/odd")
	nestGit(t, f.repo, "worktree", "add", "-q", odd)
	nestGit(t, f.repo, "config", "extensions.worktreeConfig", "true")
	nestGit(t, odd, "config", "--worktree", "core.worktree", filepath.Join(f.repo, ".claude"))
	wantGate(t, f, f.repo, hookJSON("sess-b", f.repo, "Edit", filepath.Join(odd, "README")), true, "dotc")
	sg := filepath.Join(f.repo, "vendor/sg")
	if err := os.MkdirAll(sg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(f.repo, ".git"), filepath.Join(sg, ".git")); err != nil {
		t.Fatal(err)
	}
	ptr := filepath.Join(f.repo, "vendor/ptr")
	mustWrite(t, filepath.Join(ptr, ".git"), "gitdir: ../../.git\n")
	for _, d := range []string{sg, ptr} {
		for _, cwd := range []string{f.repo, d, plain} {
			wantGate(t, f, cwd, hookJSON("sess-b", cwd, "Edit", filepath.Join(d, "x.c")), true, "dotc")
		}
	}
}

// The registration test needs a successful discovery and every piece: a
// forged registration with no HEAD keeps R's placement (DENY), as does a lone
// `gitdir:` pointer into R's worktrees directory. A COMPLETE hand-made
// registration is accepted, by the operator's ruling (forgeable by design:
// writing under R/.git can rewrite R's ledger anyway).
func TestGateRegistrationNeedsEveryPiece(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	mustClaim(t, f, f.repo, "vend", "sess-a", "vendor")
	common := canon(filepath.Join(f.repo, ".git"))
	forge := func(name string, head bool) string {
		d := filepath.Join(f.repo, "vendor", name)
		admin := filepath.Join(common, "worktrees", name)
		mustWrite(t, filepath.Join(d, ".git"), "gitdir: "+admin+"\n")
		mustWrite(t, filepath.Join(admin, "commondir"), "../..\n")
		mustWrite(t, filepath.Join(admin, "gitdir"), filepath.Join(canon(d), ".git")+"\n")
		if head {
			mustWrite(t, filepath.Join(admin, "HEAD"), "ref: refs/heads/main\n")
		}
		return d
	}
	stale := forge("stale", false)
	wantGate(t, f, stale, hookJSON("sess-b", stale, "Edit", filepath.Join(stale, "x.c")), true, "")
	wantGate(t, f, f.repo, hookJSON("sess-b", f.repo, "Edit", filepath.Join(stale, "x.c")), true, "vend")
	lone := filepath.Join(f.repo, "vendor/lone")
	mustWrite(t, filepath.Join(lone, ".git"), "gitdir: "+filepath.Join(common, "worktrees", "nobody")+"\n")
	wantGate(t, f, f.repo, hookJSON("sess-b", f.repo, "Edit", filepath.Join(lone, "x.c")), true, "vend")
	full := forge("full", true)
	wantGate(t, f, f.repo, hookJSON("sess-b", f.repo, "Edit", filepath.Join(full, "x.c")), false, "")
	// A COPY of that worktree (`cp -R`): git opens it through the same admin
	// directory, whose back-pointer names the original, so it is no
	// registration of its own and stays held to R's claim.
	cp := filepath.Join(f.repo, "vendor/copy")
	if out, err := exec.Command("cp", "-R", full, cp).CombinedOutput(); err != nil {
		t.Fatalf("cp -R: %v %s", err, out)
	}
	wantGate(t, f, f.repo, hookJSON("sess-b", f.repo, "Edit", filepath.Join(cp, "x.c")), true, "vend")
	// A HARD-LINKED copy of its .git file shares the inode the back-pointer
	// names: compared by directory, it is no registration either.
	hl := filepath.Join(f.repo, "vendor/hl")
	if err := os.MkdirAll(hl, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(full, ".git"), filepath.Join(hl, ".git")); err != nil {
		t.Fatal(err)
	}
	wantGate(t, f, f.repo, hookJSON("sess-b", f.repo, "Edit", filepath.Join(hl, "x.c")), true, "vend")
}

// An ancestor ledger that exists and cannot be read denies below it
// (invariant 3), naming where; one proved absent, or a bare-layout root above
// (no worktree root: it places nothing), is passed. Controls in each shape:
// nothing above, a readable ancestor with no claim.
func TestGateAncestorErrorDeniesAndAbsenceAllows(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	plain := canon(t.TempDir())
	p := filepath.Join(plain, "P")
	nestGit(t, p, "init", "-q")
	q := filepath.Join(p, "Q")
	nestGit(t, q, "init", "-q")
	initLedger(t, f, q)
	x := filepath.Join(q, "x.c")
	wantGate(t, f, q, hookJSON("sess-b", q, "Edit", x), false, "") // P has no ledger
	if err := os.Symlink(filepath.Join(plain, "missing.db"), filepath.Join(p, ".git", ledgerName)); err != nil {
		t.Fatal(err)
	}
	wantGate(t, f, q, hookJSON("sess-b", q, "Edit", x), true, "which governs this call from above, is unavailable")
	wantGate(t, f, plain, hookJSON("sess-b", plain, "Edit", x), true, "unavailable")
	wantGate(t, f, q, bashHookJSON("sess-b", q, "touch f"), true, "unavailable")
	if err := os.Remove(filepath.Join(p, ".git", ledgerName)); err != nil {
		t.Fatal(err)
	}
	initLedger(t, f, p)
	wantGate(t, f, q, hookJSON("sess-b", q, "Edit", x), false, "") // readable, nothing held

	// "Cannot tell": a `gitdir:` pointer git cannot open, a ledger file
	// behind it that Lstat sees. Control: no file behind it, passed.
	lying := filepath.Join(plain, "lying")
	mustWrite(t, filepath.Join(plain, "x-gitdir", ledgerName), "not sqlite")
	mustWrite(t, filepath.Join(lying, ".git"), "gitdir: "+filepath.Join(plain, "x-gitdir")+"\n")
	lq := filepath.Join(lying, "Q")
	nestGit(t, lq, "init", "-q")
	initLedger(t, f, lq)
	wantGate(t, f, lq, hookJSON("sess-b", lq, "Edit", filepath.Join(lq, "x.c")), true, "cannot tell")
	if err := os.Remove(filepath.Join(plain, "x-gitdir", ledgerName)); err != nil {
		t.Fatal(err)
	}
	wantGate(t, f, lq, hookJSON("sess-b", lq, "Edit", filepath.Join(lq, "x.c")), false, "")

	proj := filepath.Join(plain, "proj")
	nestGit(t, filepath.Join(proj, ".git"), "init", "-q", "--bare")
	initLedger(t, f, filepath.Join(proj, ".git"))
	scratch := filepath.Join(proj, "scratch")
	nestGit(t, scratch, "init", "-q")
	initLedger(t, f, scratch)
	wantGate(t, f, scratch, hookJSON("sess-b", scratch, "Edit", filepath.Join(scratch, "x.c")), false, "")
	wantGate(t, f, scratch, bashHookJSON("sess-b", scratch, "touch f"), false, "")
}

// Ancestor sets are cached per discovery context, never per ledger (Codex
// design pass C1): a worktree of an outside governed S placed inside R shares
// S's ledger, not S's ancestors. One patch naming S's file first, then the
// worktree's, is held to R's claim; with R paused, `cd S; cd W` reads R's brake.
func TestGateAncestorSetsAreNotSharedByLedger(t *testing.T) {
	boundedParallel(t)
	f, plain, n := ancestorFixture(t)
	free := filepath.Join(n["S"], "free.c")
	w := filepath.Join(n["sw"], "x.c")
	wantGate(t, f, plain, ancestorPatchJSON("sess-b", plain, free, w), true, "held")
	wantGate(t, f, plain, ancestorPatchJSON("sess-b", plain, free), false, "")
	if _, errw, code := f.run(t, f.repo, "", "pause", "all", "--note", "R frozen"); code != 0 {
		t.Fatal(errw)
	}
	wantGate(t, f, plain, bashHookJSON("sess-b", plain, "cd "+n["S"]+"; cd "+n["sw"]+"; touch x"), true, "R frozen")
	wantGate(t, f, plain, bashHookJSON("sess-b", plain, "cd "+n["S"]+"; touch x"), false, "")
}

// The reached-brake budget reads what base read first (Codex design pass C2):
// four ledgerless N1..N4, each in governed R1..R4, R1 inside governed P, R4
// paused; from a plain cwd `cd N1; cd N2; cd N3; cd N4; touch x` spends base's
// eight discoveries on N1,R1,..,N4,R4 and denies. Control: unpaused, it passes.
func TestGateAncestorsNeverSpendBasesBudget(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	plain := canon(t.TempDir())
	p := filepath.Join(plain, "P")
	nestGit(t, p, "init", "-q")
	initLedger(t, f, p)
	var cmd []string
	for i, parent := range []string{p, plain, plain, plain} {
		r := filepath.Join(parent, "R"+string(rune('1'+i)))
		nestGit(t, r, "init", "-q")
		initLedger(t, f, r)
		nn := filepath.Join(r, "N")
		nestGit(t, nn, "init", "-q")
		if i == 3 {
			if _, errw, code := f.run(t, r, "", "pause", "all", "--note", "fourth"); code != 0 {
				t.Fatal(errw)
			}
		}
		cmd = append(cmd, "cd "+nn)
	}
	cmd = append(cmd, "touch x")
	wantGate(t, f, plain, bashHookJSON("sess-b", plain, strings.Join(cmd, "; ")), true, "fourth")
	if _, errw, code := f.run(t, filepath.Join(plain, "R4"), "", "resume", "all"); code != 0 {
		t.Fatal(errw)
	}
	wantGate(t, f, plain, bashHookJSON("sess-b", plain, strings.Join(cmd, "; ")), false, "")
}

// One ledger is opened once per call however it is reached: R found as gov's
// ancestor, then R as a target's governor, share one handle.
func TestGateAncestorOpensEachLedgerOnce(t *testing.T) {
	boundedParallel(t)
	_, _, n := ancestorFixture(t)
	c := &gateCache{m: map[string]gateRepo{}}
	defer c.closeAll()
	env := Env{Getenv: func(string) string { return "" }}
	h := hookInput{SessionID: "sess-b"}
	gdir, gkey := repoKeyForDir(canon(n["gov"]))
	gov := openGateRepo(h, env, gdir, gkey, c)
	if gov.st == nil {
		t.Fatalf("gov must open: %+v", gov)
	}
	anc, _ := gateAncestors(h, env, gdir, gov, false, c)
	if len(anc) != 1 || anc[0].st == nil {
		t.Fatalf("gov's one ancestor is R: %+v", anc)
	}
	r := filepath.Dir(filepath.Dir(canon(n["gov"])))
	// A ledgerless repository in R, reached next: the D-212 walk finds R,
	// which only the ancestor walk has opened, and takes that handle.
	lib := filepath.Join(r, "vendor/lib")
	nestGit(t, lib, "init", "-q")
	ldir, lkey := repoKeyForDir(lib)
	if lr := openGateRepo(h, env, ldir, lkey, c); lr.st != anc[0].st || !lr.walked {
		t.Fatalf("the walk from a ledgerless repository must reuse R's handle: %+v", lr)
	}
	rdir, rkey := repoKeyForDir(r)
	got := openGateRepo(h, env, rdir, rkey, c)
	if got.st != anc[0].st {
		t.Fatal("R reached as a target after gov's walk must reuse the handle the walk opened")
	}
	// The ancestor set is cached: a second walk asks git nothing.
	opens := c.opens
	if again, _ := gateAncestors(h, env, gdir, gov, false, c); len(again) != 1 || c.opens != opens {
		t.Fatalf("a cached ancestor set: %d ancestors, %d more opens", len(again), c.opens-opens)
	}
}

// A home whose core.worktree names ANOTHER governed repository's root: S's
// ledger, top R. The target walk stopped at R's .git as if the home had read
// R, and from cwd S neither R's claims nor its brake applied (Codex code pass
// P1; base ALLOW too). Controls: the holder, and R resumed.
func TestGateHomeWhoseRootIsAnotherRepository(t *testing.T) {
	boundedParallel(t)
	f, _, n := ancestorFixture(t)
	s := n["S"]
	nestGit(t, s, "config", "core.worktree", canon(f.repo))
	for _, x := range []string{filepath.Join(n["gov"], "x.c"), filepath.Join(f.repo, "src/a.go")} {
		wantGate(t, f, s, hookJSON("sess-b", s, "Edit", x), true, "held")
		wantGate(t, f, s, hookJSON("sess-a", s, "Edit", x), false, "")
	}
	if _, errw, code := f.run(t, f.repo, "", "pause", "all", "--note", "R frozen"); code != 0 {
		t.Fatal(errw)
	}
	wantGate(t, f, s, bashHookJSON("sess-b", s, "touch f"), true, "R frozen")
	if _, errw, code := f.run(t, f.repo, "", "resume", "all"); code != 0 {
		t.Fatal(errw)
	}
	wantGate(t, f, s, bashHookJSON("sess-b", s, "touch f"), false, "")
}

// A home whose cwd is not under its root: a separate git directory H whose
// core.worktree is D inside governed R, `buddy init`ed from D. From cwd H the
// walk never passed R, and the stop at D's entry (the home's own) skipped the
// walk from the root: A's claim and R's brake did not reach an Edit of
// D/x.c or `cd D && touch x` (Fable cross-check; base ALLOW too). Controls:
// the holder, and R resumed.
func TestGateHomeCwdOutsideItsRoot(t *testing.T) {
	boundedParallel(t)
	f, plain, _ := ancestorFixture(t)
	d := filepath.Join(f.repo, "vendor/D")
	hgit := filepath.Join(plain, "H", ".git")
	if err := os.MkdirAll(filepath.Dir(hgit), 0o755); err != nil {
		t.Fatal(err)
	}
	nestGit(t, d, "init", "-q", "--separate-git-dir", hgit)
	nestGit(t, hgit, "config", "core.worktree", canon(d))
	initLedger(t, f, d)
	hcwd := filepath.Join(plain, "H")
	x := filepath.Join(d, "x.c")
	wantGate(t, f, hcwd, hookJSON("sess-b", hcwd, "Edit", x), true, "held")
	wantGate(t, f, hcwd, hookJSON("sess-a", hcwd, "Edit", x), false, "")
	if _, errw, code := f.run(t, f.repo, "", "pause", "all", "--note", "R frozen"); code != 0 {
		t.Fatal(errw)
	}
	wantGate(t, f, hcwd, bashHookJSON("sess-b", hcwd, "cd "+d+" && touch x"), true, "R frozen")
	if _, errw, code := f.run(t, f.repo, "", "resume", "all"); code != 0 {
		t.Fatal(errw)
	}
	wantGate(t, f, hcwd, bashHookJSON("sess-b", hcwd, "cd "+d+" && touch x"), false, "")
}

// The same home nested in R's own registered lane W: S = W/nested, governed,
// core.worktree R. The cwd walk placed R at W only (W is R's registration),
// so R/src/a.go, outside W, met no claim of R's (Codex re-verify, round 1).
// Control: the holder.
func TestGateHomeRootReadEvenUnderARegisteredLane(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	mustClaim(t, f, f.repo, "held", "sess-a", "src")
	w := filepath.Join(f.repo, ".claude/worktrees/w")
	nestGit(t, f.repo, "worktree", "add", "-q", w)
	s := filepath.Join(w, "nested")
	nestGit(t, s, "init", "-q")
	initLedger(t, f, s)
	nestGit(t, s, "config", "core.worktree", canon(f.repo))
	a := filepath.Join(f.repo, "src/a.go")
	wantGate(t, f, s, hookJSON("sess-b", s, "Edit", a), true, "held")
	wantGate(t, f, s, hookJSON("sess-a", s, "Edit", a), false, "")
}

// Two walk stops that save git and placement, counted in process: a target's
// walk ends at the home's own entry (the home arms read the home and its
// ancestors), and R's own registered worktree is placed against its own root
// only, so R's entry above it is neither asked about nor placed.
func TestGateAncestorWalkStopsCostNothing(t *testing.T) {
	boundedParallel(t)
	f, _, n := ancestorFixture(t)
	env := Env{Getenv: func(string) string { return "" }}
	h := hookInput{SessionID: "sess-b"}

	c := &gateCache{m: map[string]gateRepo{}}
	defer c.closeAll()
	r := canon(f.repo)
	c.home, _ = os.Stat(filepath.Join(r, ".git"))
	c.homeTop, _ = os.Stat(r)
	gdir, gkey := repoKeyForDir(canon(n["gov"]))
	gov := openGateRepo(h, env, gdir, gkey, c)
	opens := c.opens
	if anc, _ := gateAncestors(h, env, gdir, gov, false, c); len(anc) != 0 || c.opens != opens {
		t.Fatalf("the walk must stop at the home's entry: %d ancestors, %d opens", len(anc), c.opens-opens)
	}

	lane := filepath.Join(f.repo, ".claude/worktrees/lane")
	nestGit(t, f.repo, "worktree", "add", "-q", lane)
	c2 := &gateCache{m: map[string]gateRepo{}}
	defer c2.closeAll()
	ldir, lkey := repoKeyForDir(canon(lane))
	lg := openGateRepo(h, env, ldir, lkey, c2)
	if lg.st == nil || lg.walked {
		t.Fatalf("the lane opens R's ledger directly: %+v", lg)
	}
	opens = c2.opens
	if anc, _ := gateAncestors(h, env, ldir, lg, false, c2); len(anc) != 0 || c2.opens != opens {
		t.Fatalf("a registered worktree's ledger is not placed again at R: %d ancestors, %d opens", len(anc), c2.opens-opens)
	}
}

// A ledger-bearing nested repository in a directory nothing governs stays its
// own (control: base and tip agree), and gov's own brake still refuses.
func TestGateLedgerBearingRepoNestedInNothingIsItsOwn(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	plain := canon(t.TempDir())
	outer := filepath.Join(plain, "outer")
	if err := os.MkdirAll(outer, 0o755); err != nil {
		t.Fatal(err)
	}
	gov := filepath.Join(outer, "gov")
	nestGit(t, gov, "init", "-q")
	initLedger(t, f, gov)
	x := filepath.Join(gov, "x.c")
	wantGate(t, f, gov, hookJSON("sess-b", gov, "Edit", x), false, "")
	wantGate(t, f, plain, hookJSON("sess-b", plain, "Edit", x), false, "")
	if _, errw, code := f.run(t, gov, "", "pause", "all", "--note", "gov frozen"); code != 0 {
		t.Fatal(errw)
	}
	wantGate(t, f, plain, hookJSON("sess-b", plain, "Edit", x), true, "gov frozen")
}

// A firmlink spelling of a ledgerless nested cwd, `/System/Volumes/Data<R>/
// vendor/lib`, with a relative path: the home is R (walked) and could not
// place the spelling, and the target's walk settled at R's own entry, which
// read as "the home checked it" and passed A's claim unread (D-212's recorded
// firmlink row, panel lens a #6; base ALLOW). Now a placement failure, DENY.
// Control: the canonical cwd with the same relative path is refused by the
// claim itself, and the holder passes there.
func TestGateFirmlinkSpellingSettledAtTheHomeDenies(t *testing.T) {
	boundedParallel(t)
	f, _, nested := nestedFixture(t)
	lib := canon(nested[0])
	fl := "/System/Volumes/Data" + lib
	if _, err := os.Stat(fl); err != nil {
		t.Skipf("no firmlink spelling of the temp dir here: %v", err)
	}
	wantGate(t, f, fl, hookJSON("sess-b", fl, "Edit", "x.c"), true, "could not be placed")
	wantGate(t, f, lib, hookJSON("sess-b", lib, "Edit", "x.c"), true, "vend")
	wantGate(t, f, lib, hookJSON("sess-a", lib, "Edit", "x.c"), false, "")
}
