package cli

// Regressions for the 2026-10-06 review of the Codex gate work (findings F-01,
// F-03, F-04, F-05, F-06, F-28 in docs/CODEX-ORCHESTRATOR-FINDINGS.md). Each
// was a failing probe against the tree before its fix; each keeps a positive
// control, because "it refused" and "it never ran" look identical otherwise.

import (
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func bashHookJSON(session, cwd, command string) string {
	in := map[string]any{
		"session_id": session, "cwd": cwd, "tool_name": "Bash",
		"tool_input": map[string]any{"command": command},
	}
	b, _ := json.Marshal(in)
	return string(b)
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustClaim(t *testing.T, f *fixture, cwd, slug, session string, scopes ...string) {
	t.Helper()
	args := []string{"claim", slug, "--session", session}
	for _, s := range scopes {
		args = append(args, "--scope", s)
	}
	if _, errw, code := f.run(t, cwd, "", args...); code != 0 {
		t.Fatalf("claim %s: %s", slug, errw)
	}
}

func wantGate(t *testing.T, f *fixture, cwd, input string, deny bool, contains string) {
	t.Helper()
	out, errw, code := f.run(t, cwd, input, "gate")
	reason, denied := decodeDeny(t, out)
	if code != 0 || errw != "" {
		t.Fatalf("gate must answer on stdout with exit 0: code=%d stderr=%q", code, errw)
	}
	if denied != deny || (deny && !strings.Contains(reason, contains)) {
		t.Fatalf("want deny=%v (containing %q), got %q\ninput: %s", deny, contains, out, input)
	}
}

// F-01: Codex applies `apply_patch <<'EOF'` from its SHELL tool in-process, and
// the hook says tool_name "Bash". The gate used to treat that as pause-only.
func TestGateRefusesShellRoutedPatch(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	mustClaim(t, f, f.repo, "core", "sess-a", "src")
	patch := patchText("*** Delete File: src/old.go\n")
	// Control: the same patch through the TOOL is adjudicated and denied.
	wantGate(t, f, f.wtB, patchHookJSON("sess-b", f.wtB, patch), true, "core")
	for _, cmd := range []string{
		"apply_patch <<'EOF'\n" + patch + "EOF\n",
		"applypatch <<'EOF'\n" + patch + "EOF\n",
		"cd " + f.wtB + " && apply_patch <<'EOF'\n" + patch + "EOF\n",
		"set -e\napply_patch <<\"EOF\"\n" + patch + "EOF\n",
		"cat p.patch | apply_patch",
		"FOO=1 apply_patch < p.patch",
		"'apply_patch' <<'EOF'\n" + patch + "EOF\n",
		"/opt/codex/bin/apply_patch \"$(cat p.patch)\"",
		"if true; then apply_patch <<'EOF'\n" + patch + "EOF\nfi",
		// Not even a claimed path: the refusal is about the ROUTE, which the
		// gate cannot place, not about this patch's paths.
		"apply_patch <<'EOF'\n" + patchText("*** Add File: free.go\n+x\n") + "EOF\n",
	} {
		wantGate(t, f, f.wtB, codexBashHookJSON("sess-b", f.wtB, cmd), true, "apply_patch tool")
	}
	// Ordinary shell, including commands that merely MENTION the word, passes.
	// This repo greps for it constantly.
	for _, cmd := range []string{
		"grep -n apply_patch internal/cli/*.go",
		"rg 'apply_patch <<' docs",
		"git log --grep=apply_patch",
		"ls scripts/apply_patch.py && python3 apply_patch.py",
		"go test -run TestApplyPatch ./...",
		"echo hello",
	} {
		wantGate(t, f, f.wtB, codexBashHookJSON("sess-b", f.wtB, cmd), false, "")
	}
}

// F-01 keeps the feature-off boundary: outside any ledger the shell route is
// nobody's business, unless the patch visibly lands in a governed repo.
func TestGateShellRoutedPatchFeatureOff(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	plain := t.TempDir()
	wantGate(t, f, plain, codexBashHookJSON("sess-b", plain,
		"apply_patch <<'EOF'\n"+patchText("*** Add File: x.go\n+x\n")+"EOF\n"), false, "")
	wantGate(t, f, plain, codexBashHookJSON("sess-b", plain,
		"apply_patch <<'EOF'\n"+patchText("*** Add File: "+filepath.Join(f.repo, "x.go")+"\n+x\n")+"EOF\n"), true, "apply_patch tool")
	wantGate(t, f, plain, codexBashHookJSON("sess-b", plain,
		"cd "+f.repo+" && apply_patch <<'EOF'\n"+patchText("*** Add File: x.go\n+x\n")+"EOF\n"), true, "apply_patch tool")
}

// F-03: a nested repository is consulted IN ADDITION to the home ledger. It
// used to replace it, so a claim on the enclosing directory stopped applying.
func TestGateNestedRepoKeepsHomeClaim(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	mustClaim(t, f, f.repo, "vendor", "sess-a", "vendor")
	gitInit := filepath.Join(f.repo, "vendor/inited")
	if err := os.MkdirAll(gitInit, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", gitInit, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	// A pruned linked worktree leaves a .git FILE git calls "not a git
	// repository", which discovery reads as feature-off for that directory.
	mustWrite(t, filepath.Join(f.repo, "vendor/stale/.git"), "gitdir: "+filepath.Join(f.repo, ".git/worktrees/gone")+"\n")
	// Control: a sibling with no nested .git is denied.
	wantGate(t, f, f.repo, hookJSON("sess-b", f.repo, "Edit", filepath.Join(f.repo, "vendor/x.go")), true, "vendor")
	for _, p := range []string{"vendor/inited/x.go", "vendor/stale/x.go"} {
		wantGate(t, f, f.repo, hookJSON("sess-b", f.repo, "Edit", filepath.Join(f.repo, p)), true, "vendor")
		wantGate(t, f, f.repo, patchHookJSON("sess-b", f.repo, patchText("*** Add File: "+p+"\n+x\n")), true, "vendor")
	}
	// The holder itself still passes, through both arms.
	wantGate(t, f, f.repo, hookJSON("sess-a", f.repo, "Edit", filepath.Join(f.repo, "vendor/inited/x.go")), false, "")
}

// F-05 and F-06: every spelling a write can land on is checked.
func TestGateChecksEverySpellingOfATarget(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	mustWrite(t, filepath.Join(f.repo, "docs/core.go"), "x\n")
	mustWrite(t, filepath.Join(f.repo, "src/deep/core.go"), "x\n")
	mustWrite(t, filepath.Join(f.repo, "src/core.go"), "x\n")
	mustWrite(t, filepath.Join(f.repo, "src/x.go"), "x\n")
	for link, to := range map[string]string{
		"docs/link":     "../src/deep",     // a directory link, for `..` after it
		"docs/filelink": "../src/x.go",     // an entry in a claimed dir, pointing out
		"free/intodocs": "../docs/core.go", // an unclaimed entry, pointing in
		"docs/dangling": "../src/new.go",   // creates outside the claim when written
		"free/dangling": "../docs/new.go",  // creates INSIDE the claim when written
	} {
		if err := os.MkdirAll(filepath.Join(f.repo, filepath.Dir(link)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(to, filepath.Join(f.repo, link)); err != nil {
			t.Fatal(err)
		}
	}
	mustClaim(t, f, f.repo, "docs", "sess-a", "docs")
	abs := func(p string) string { return f.repo + "/" + p }
	for _, tc := range []struct {
		name, input string
		deny        bool
	}{
		{"control: claimed file", patchHookJSON("sess-b", f.repo, patchText("*** Delete File: "+abs("docs/core.go")+"\n")), true},
		{"control: free file", patchHookJSON("sess-b", f.repo, patchText("*** Delete File: src/core.go\n")), false},
		// F-06: Codex cleans `..` lexically (docs/core.go); EvalSymlinks
		// followed docs/link first (src/core.go). Relative was always fine.
		{"relative link/..", patchHookJSON("sess-b", f.repo, patchText("*** Delete File: docs/link/../core.go\n")), true},
		{"absolute link/.. patch", patchHookJSON("sess-b", f.repo, patchText("*** Delete File: "+abs("docs/link/../core.go")+"\n")), true},
		{"absolute link/.. Edit", hookJSON("sess-b", f.repo, "Edit", abs("docs/link/../core.go")), true},
		// F-05: Delete and a move's source unlink the ENTRY.
		{"delete claimed entry", patchHookJSON("sess-b", f.repo, patchText("*** Delete File: docs/filelink\n")), true},
		{"move claimed entry", patchHookJSON("sess-b", f.repo, patchText("*** Update File: docs/filelink\n*** Move to: free/moved.go\n@@\n-x\n+y\n")), true},
		{"write through a free entry into the claim", hookJSON("sess-b", f.repo, "Write", abs("free/intodocs")), true},
		{"dangling link into the claim", hookJSON("sess-b", f.repo, "Write", abs("free/dangling")), true},
		{"dangling link entry in the claim", hookJSON("sess-b", f.repo, "Write", abs("docs/dangling")), true},
		// The holder passes every spelling.
		{"holder", patchHookJSON("sess-a", f.repo, patchText("*** Delete File: docs/filelink\n*** Delete File: "+abs("docs/link/../core.go")+"\n")), false},
	} {
		t.Run(tc.name, func(t *testing.T) { wantGate(t, f, f.repo, tc.input, tc.deny, "docs") })
	}
}

// F-04: an identity conflict is a verdict only where a ledger governs. It used
// to deny every Write on the machine for a Claude session that inherited a
// Codex parent's CODEX_THREAD_ID.
func TestGateIdentityConflictOnlyWhereGoverned(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	broken := newFixture(t)
	mustWrite(t, filepath.Join(broken.repo, ".git", "buddy.db"), "corrupt")
	plain := t.TempDir()
	for _, env := range []map[string]string{
		{"CLAUDE_CODE_SESSION_ID": "claude-1", "CODEX_THREAD_ID": "codex-parent"},
		{"BUDDY_HARNESS": "codex", "BUDDY_SESSION": "someone-else"},
	} {
		f.env = env
		write := func(cwd, path string) string { return hookJSON("claude-1", cwd, "Write", path) }
		// Feature-off: no ledger anywhere near the call.
		wantGate(t, f, plain, write(plain, filepath.Join(plain, "x.txt")), false, "")
		wantGate(t, f, plain, bashHookJSON("claude-1", plain, "make"), false, "")
		// Governed: the home ledger, a TARGET ledger from an ungoverned cwd,
		// and an unreadable target ledger (invariant 3: deny, never off).
		wantGate(t, f, f.repo, write(f.repo, filepath.Join(f.repo, "x.txt")), true, "ambiguous")
		wantGate(t, f, f.repo, bashHookJSON("claude-1", f.repo, "make"), true, "ambiguous")
		wantGate(t, f, plain, write(plain, filepath.Join(f.repo, "x.txt")), true, "ambiguous")
		wantGate(t, f, plain, write(plain, filepath.Join(broken.repo, "x.txt")), true, "unavailable")
		// Read-only tools are never adjudicated, ledger or not.
		wantGate(t, f, f.repo, hookJSON("claude-1", f.repo, "Read", filepath.Join(f.repo, "x.txt")), false, "")
	}
	// Control: the same calls with a consistent identity are allowed.
	f.env = map[string]string{"CLAUDE_CODE_SESSION_ID": "claude-1"}
	wantGate(t, f, f.repo, hookJSON("claude-1", f.repo, "Write", filepath.Join(f.repo, "x.txt")), false, "")
}

// F-28: a cross-repo edit honours the TARGET ledger's brake, not just its
// claims. `buddy pause all` there used to stop nobody whose cwd was elsewhere.
func TestGateForeignTargetHonoursPause(t *testing.T) {
	boundedParallel(t)
	home := newFixture(t)
	home.initAndHello(t)
	target := newFixture(t)
	target.initAndHello(t)
	plain := t.TempDir()
	path := filepath.Join(target.repo, "x.go")
	// Control: no claim and no brake, so the foreign edit passes.
	wantGate(t, home, home.repo, hookJSON("sess-a", home.repo, "Edit", path), false, "")
	if _, errw, code := target.run(t, target.repo, "", "pause", "all"); code != 0 {
		t.Fatal(errw)
	}
	for _, cwd := range []string{home.repo, plain} {
		wantGate(t, home, cwd, hookJSON("sess-a", cwd, "Edit", path), true, "paused edits in")
		wantGate(t, home, cwd, patchHookJSON("sess-a", cwd, patchText("*** Add File: "+path+"\n+x\n")), true, "paused edits in")
	}
	// The home repo's own files are not under the target's brake.
	wantGate(t, home, home.repo, hookJSON("sess-a", home.repo, "Edit", filepath.Join(home.repo, "x.go")), false, "")
	if _, errw, code := target.run(t, target.repo, "", "resume", "all"); code != 0 {
		t.Fatal(errw)
	}
	wantGate(t, home, home.repo, hookJSON("sess-a", home.repo, "Edit", path), false, "")
}

// F-28's other half (audit A-14, 2026-10-07): the label openGateRepo hands the
// TARGET ledger's PausedFor is the one THAT ledger knows the session by. The
// test above pauses `all`, which needs no label, so deleting the label argument
// survived the whole package. `pause <label>` resolves to the session id
// before it is stored, so the label arm is reached only by a row written
// before targets were resolved (store.Resume says why those are still
// honoured); the test plants that row directly, as the ledger holds it.
func TestGateForeignTargetHonoursPauseByItsOwnLabel(t *testing.T) {
	boundedParallel(t)
	home := newFixture(t)
	home.initAndHello(t) // sess-a is "alpha" at home
	target := newFixture(t)
	if _, errw, code := target.run(t, target.repo, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	// The same session wears a DIFFERENT label in the target ledger.
	if _, errw, code := target.run(t, target.repo, hookJSON("sess-a", target.repo, "", ""), "hello", "--label", "zulu"); code != 0 {
		t.Fatal(errw)
	}
	path := filepath.Join(target.repo, "x.go")
	edit := hookJSON("sess-a", home.repo, "Edit", path)
	// Control: no brake, so the foreign edit passes.
	wantGate(t, home, home.repo, edit, false, "")
	db, err := sql.Open("sqlite", "file:"+filepath.Join(target.repo, ".git", "buddy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	plant := func(label string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO controls (kind, target, note, created) VALUES ('pause', ?, 'legacy row', ?)`,
			label, target.clock.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	// Control: the session's HOME label names nobody in the target ledger. A
	// label borrowed from another ledger could name somebody else here, so it
	// is never the one consulted.
	plant("alpha")
	wantGate(t, home, home.repo, edit, false, "")
	// The target ledger's own label for the session stops it, from either cwd.
	plant("zulu")
	for _, cwd := range []string{home.repo, t.TempDir()} {
		wantGate(t, home, cwd, hookJSON("sess-a", cwd, "Edit", path), true, "paused edits in")
	}
	// And `resume <label>` there lifts it (store.Resume clears by label for
	// exactly these rows), so the pause is not one no verb can clear.
	if _, errw, code := target.run(t, target.repo, "", "resume", "zulu"); code != 0 {
		t.Fatal(errw)
	}
	wantGate(t, home, home.repo, edit, false, "")
}
