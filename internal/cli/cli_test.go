package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixture builds a real git repo with a second worktree, since the ledger
// lives in the git COMMON dir and must be shared across worktrees.
type fixture struct {
	repo, wtB string
	clock     time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "README")
	git("commit", "-q", "-m", "init")
	wtB := filepath.Join(dir, "wtB")
	git("worktree", "add", "-q", wtB)
	return &fixture{repo: repo, wtB: wtB, clock: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)}
}

// run executes a buddy command with stdin JSON (may be empty) from cwd.
func (f *fixture) run(t *testing.T, cwd, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errw bytes.Buffer
	code = Run(args, Env{
		Stdin:  strings.NewReader(stdin),
		Stdout: &out,
		Stderr: &errw,
		Cwd:    cwd,
		Now:    func() time.Time { return f.clock },
	})
	return out.String(), errw.String(), code
}

func hookJSON(session, cwd, tool, filePath string) string {
	in := map[string]any{
		"session_id": session,
		"cwd":        cwd,
		"tool_name":  tool,
		"tool_input": map[string]any{"file_path": filePath},
	}
	b, _ := json.Marshal(in)
	return string(b)
}

func (f *fixture) initAndHello(t *testing.T) {
	t.Helper()
	if out, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatalf("init failed: %s %s", out, errw)
	}
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello", "--label", "alpha"); code != 0 {
		t.Fatalf("hello a: %s", errw)
	}
	if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "", ""), "hello", "--label", "bravo"); code != 0 {
		t.Fatalf("hello b: %s", errw)
	}
}

func decodeDeny(t *testing.T, out string) (reason string, denied bool) {
	t.Helper()
	if strings.TrimSpace(out) == "" {
		return "", false
	}
	var v struct {
		HookSpecificOutput struct {
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("gate output is not hook JSON: %q", out)
	}
	return v.HookSpecificOutput.PermissionDecisionReason, v.HookSpecificOutput.PermissionDecision == "deny"
}

func TestGateDeniesForeignScopeNamingClaimant(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)

	if out, errw, code := f.run(t, f.repo, "", "claim", "router-work", "--desc", "edge cap", "--scope", "internal/router"); code != 0 {
		t.Fatalf("claim: %s %s", out, errw)
	}

	// B edits inside A's scope → deny naming alpha.
	target := filepath.Join(f.wtB, "internal/router/proxy.go")
	out, _, _ := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Edit", target), "gate")
	reason, denied := decodeDeny(t, out)
	if !denied {
		t.Fatalf("want deny, got %q", out)
	}
	if !strings.Contains(reason, "alpha") || !strings.Contains(reason, "router-work") {
		t.Fatalf("denial must name the claimant and slug: %q", reason)
	}

	// B edits elsewhere → pass (no output).
	out, _, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Edit", filepath.Join(f.wtB, "docs/x.md")), "gate")
	if code != 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("disjoint edit must pass silently, got %q", out)
	}

	// A editing its OWN scope → pass.
	out, _, _ = f.run(t, f.repo, hookJSON("sess-a", f.repo, "Edit", filepath.Join(f.repo, "internal/router/proxy.go")), "gate")
	if _, denied := decodeDeny(t, out); denied {
		t.Fatalf("own scope must not be denied: %q", out)
	}
}

func TestGatePauseDeniesAndResumeClears(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)

	if _, errw, code := f.run(t, f.repo, "", "pause", "bravo", "--note", "stop touching the router"); code != 0 {
		t.Fatal(errw)
	}
	// Bash is path-blind but pause still applies.
	out, _, _ := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Bash", ""), "gate")
	reason, denied := decodeDeny(t, out)
	if !denied || !strings.Contains(reason, "stop touching the router") {
		t.Fatalf("paused session's Bash must be denied with the note, got %q", out)
	}
	// The other session is unaffected.
	out, _, _ = f.run(t, f.repo, hookJSON("sess-a", f.repo, "Bash", ""), "gate")
	if _, denied := decodeDeny(t, out); denied {
		t.Fatal("pause of bravo must not deny alpha")
	}
	if _, errw, code := f.run(t, f.repo, "", "resume", "bravo"); code != 0 {
		t.Fatal(errw)
	}
	out, _, _ = f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Bash", ""), "gate")
	if _, denied := decodeDeny(t, out); denied {
		t.Fatal("resume must clear the denial")
	}
}

func TestGateFeatureOffWithoutLedger(t *testing.T) {
	f := newFixture(t)
	// No init. Gate must pass silently: the feature is off.
	out, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "Edit", filepath.Join(f.repo, "x.go")), "gate")
	if code != 0 || out != "" || errw != "" {
		t.Fatalf("uninitialized repo must be a silent no-op: code=%d out=%q err=%q", code, out, errw)
	}
}

func TestGateFailsClosedOnCorruptLedger(t *testing.T) {
	f := newFixture(t)
	common, err := exec.Command("git", "-C", f.repo, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(strings.TrimSpace(string(common)), "buddy.db")
	if err := os.WriteFile(dbPath, []byte("this is not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, _ := f.run(t, f.repo, hookJSON("sess-a", f.repo, "Edit", filepath.Join(f.repo, "x.go")), "gate")
	reason, denied := decodeDeny(t, out)
	if !denied {
		t.Fatalf("corrupt ledger must fail CLOSED for mutating tools, got %q", out)
	}
	if !strings.Contains(reason, "unavailable") {
		t.Fatalf("denial should explain the ledger state: %q", reason)
	}
	// Non-mutating tools still pass.
	out, _, _ = f.run(t, f.repo, hookJSON("sess-a", f.repo, "Read", filepath.Join(f.repo, "x.go")), "gate")
	if _, denied := decodeDeny(t, out); denied {
		t.Fatal("non-mutating tools must not be blocked by a corrupt ledger")
	}
}

// failWriter fails after n bytes, simulating a crash mid-delivery.
type failWriter struct{ n int }

func (w *failWriter) Write(p []byte) (int, error) {
	if len(p) > w.n {
		return 0, errors.New("sink failed")
	}
	w.n -= len(p)
	return len(p), nil
}

func TestBeatDrainsInboxAtLeastOnce(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "msg", "bravo", "--from", "jay", "check", "the", "nightly"); code != 0 {
		t.Fatal(errw)
	}

	// Failing sink: message must NOT be marked delivered.
	var errw bytes.Buffer
	code := Run([]string{"beat"}, Env{
		Stdin:  strings.NewReader(hookJSON("sess-b", f.wtB, "Edit", "")),
		Stdout: &failWriter{n: 0},
		Stderr: &errw,
		Cwd:    f.wtB,
		Now:    func() time.Time { return f.clock },
	})
	if code == 0 {
		t.Fatal("beat with a failing sink should report the failure")
	}

	// Healthy drain: the message arrives, exactly once, fenced as untrusted.
	out, _, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Edit", ""), "beat")
	if code != 0 {
		t.Fatal("healthy beat failed")
	}
	if !strings.Contains(out, "check the nightly") || !strings.Contains(out, "[jay]") {
		t.Fatalf("undelivered message must survive a failed sink and arrive next beat: %q", out)
	}
	if !strings.Contains(out, "untrusted") {
		t.Fatalf("drained messages must be fenced as untrusted input: %q", out)
	}
	var v struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.HookSpecificOutput.AdditionalContext == "" {
		t.Fatalf("beat output must be PostToolUse hook JSON: %q", out)
	}

	// Third beat: nothing left.
	out, _, _ = f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Edit", ""), "beat")
	if strings.TrimSpace(out) != "" {
		t.Fatalf("delivered message must not repeat: %q", out)
	}
}

func TestHelloDigestListsClaimsAndPause(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "claim", "router-work", "--desc", "edge cap", "--scope", "internal/router"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := f.run(t, f.repo, "", "pause", "bravo", "--note", "hold"); code != 0 {
		t.Fatal(errw)
	}
	out, _, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "", ""), "hello", "--label", "bravo")
	if code != 0 {
		t.Fatal("hello failed")
	}
	for _, want := range []string{"router-work", "alpha", "internal/router", "PAUSED", "hold"} {
		if !strings.Contains(out, want) {
			t.Fatalf("hello digest missing %q:\n%s", want, out)
		}
	}
	// The claimant's own hello marks its claim YOU.
	out, _, _ = f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello")
	if !strings.Contains(out, "YOU") {
		t.Fatalf("own claim should be marked YOU:\n%s", out)
	}
}

func TestClaimResolvesSessionByWorktree(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	// Claim from wtB resolves to bravo, so alpha's later overlap is refused naming bravo.
	if out, errw, code := f.run(t, f.wtB, "", "claim", "b-work", "--desc", "x", "--scope", "pkg/b"); code != 0 {
		t.Fatalf("claim from wtB: %s %s", out, errw)
	}
	_, errw, code := f.run(t, f.repo, "", "claim", "a-work", "--desc", "x", "--scope", "pkg/b/sub")
	if code == 0 {
		t.Fatal("overlap should be refused")
	}
	if !strings.Contains(errw, "bravo") {
		t.Fatalf("refusal must name bravo: %s", errw)
	}
}

func TestReleaseAndLs(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "claim", "w", "--desc", "d", "--scope", "pkg"); code != 0 {
		t.Fatal(errw)
	}
	out, _, _ := f.run(t, f.repo, "", "ls")
	if !strings.Contains(out, "w") || !strings.Contains(out, "alpha") {
		t.Fatalf("ls should show the claim: %q", out)
	}
	if _, errw, code := f.run(t, f.repo, "", "release", "w"); code != 0 {
		t.Fatal(errw)
	}
	out, _, _ = f.run(t, f.repo, "", "ls")
	if !strings.Contains(out, "no claims") {
		t.Fatalf("released claim should leave ls empty: %q", out)
	}
	// Releasing someone else's claim is refused.
	if _, errw, code := f.run(t, f.wtB, "", "claim", "bw", "--desc", "d", "--scope", "pkg2"); code != 0 {
		t.Fatal(errw)
	}
	if _, _, code := f.run(t, f.repo, "", "release", "bw"); code == 0 {
		t.Fatal("must not release another session's claim")
	}
}

func TestStaleShownInLs(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "claim", "w", "--desc", "d", "--scope", "pkg"); code != 0 {
		t.Fatal(errw)
	}
	f.clock = f.clock.Add(45 * time.Minute)
	out, _, _ := f.run(t, f.repo, "", "ls")
	if !strings.Contains(out, "STALE") {
		t.Fatalf("unrenewed claim past 30m must show STALE: %q", out)
	}
}

func TestMain(m *testing.M) {
	// Guard: the suite shells out to git; make its absence loud, not flaky.
	if _, err := exec.LookPath("git"); err != nil {
		fmt.Fprintln(os.Stderr, "cli tests require git on PATH")
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestGateDeniesRelativePathInsideForeignScope(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "claim", "router-work", "--desc", "d", "--scope", "internal/router"); code != 0 {
		t.Fatal(errw)
	}
	// Relative file_path resolves against the hook cwd — it must still deny.
	out, _, _ := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Edit", "internal/router/proxy.go"), "gate")
	if _, denied := decodeDeny(t, out); !denied {
		t.Fatalf("relative in-scope path must be denied: %q", out)
	}
}

func TestGateDeniesCaseAliasedRepoRoot(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "claim", "router-work", "--desc", "d", "--scope", "internal/router"); code != 0 {
		t.Fatal(errw)
	}
	// Uppercase the final component of the worktree root: APFS resolves it to
	// the same repo, and a byte-wise containment check would read it as an
	// escape (Codex B1 finding).
	base := filepath.Base(f.wtB)
	aliased := filepath.Join(filepath.Dir(f.wtB), strings.ToUpper(base), "internal/router/proxy.go")
	out, _, _ := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Edit", aliased), "gate")
	if _, denied := decodeDeny(t, out); !denied {
		t.Fatalf("case-aliased repo root must not bypass the gate: %q", out)
	}
}

func TestGateChecksDotDotPrefixedDirName(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	// "..owned" is a legal directory NAME, not an escape.
	if _, errw, code := f.run(t, f.repo, "", "claim", "dots", "--desc", "d", "--scope", "..owned"); code != 0 {
		t.Fatal(errw)
	}
	out, _, _ := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Edit", filepath.Join(f.wtB, "..owned/f.go")), "gate")
	if _, denied := decodeDeny(t, out); !denied {
		t.Fatalf("a ..-prefixed dir NAME must not read as outside the repo: %q", out)
	}
}

func TestGateNotebookEditUsesNotebookPath(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "claim", "nb", "--desc", "d", "--scope", "notebooks"); code != 0 {
		t.Fatal(errw)
	}
	in := map[string]any{
		"session_id": "sess-b", "cwd": f.wtB, "tool_name": "NotebookEdit",
		"tool_input": map[string]any{"notebook_path": filepath.Join(f.wtB, "notebooks/x.ipynb")},
	}
	b, _ := json.Marshal(in)
	out, _, _ := f.run(t, f.wtB, string(b), "gate")
	if _, denied := decodeDeny(t, out); !denied {
		t.Fatalf("NotebookEdit's notebook_path must be claim-checked: %q", out)
	}
	// And a mutating path tool WITHOUT its path field fails closed.
	in["tool_input"] = map[string]any{}
	b, _ = json.Marshal(in)
	out, _, _ = f.run(t, f.wtB, string(b), "gate")
	reason, denied := decodeDeny(t, out)
	if !denied || !strings.Contains(reason, "schema drift") {
		t.Fatalf("missing path field on a mutating tool must fail closed: %q", out)
	}
}

func TestGateDeniesOnDanglingLedgerSymlink(t *testing.T) {
	f := newFixture(t)
	common, err := exec.Command("git", "-C", f.repo, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(strings.TrimSpace(string(common)), "buddy.db")
	if err := os.Symlink(filepath.Join(f.repo, "nowhere.db"), dbPath); err != nil {
		t.Fatal(err)
	}
	out, _, _ := f.run(t, f.repo, hookJSON("sess-a", f.repo, "Edit", filepath.Join(f.repo, "x.go")), "gate")
	if _, denied := decodeDeny(t, out); !denied {
		t.Fatalf("a dangling ledger symlink is not feature-off; must fail closed: %q", out)
	}
}

func TestBeatDrainIsBounded(t *testing.T) {
	f := newFixture(t)
	f.initAndHello(t)
	for i := 0; i < 30; i++ {
		if _, errw, code := f.run(t, f.repo, "", "msg", "bravo", fmt.Sprintf("note %02d", i)); code != 0 {
			t.Fatal(errw)
		}
	}
	out, _, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Edit", ""), "beat")
	if code != 0 {
		t.Fatal("beat failed")
	}
	if strings.Count(out, "note ") != 20 {
		t.Fatalf("one drain must cap at 20 messages, got %d", strings.Count(out, "note "))
	}
	out, _, _ = f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Edit", ""), "beat")
	if strings.Count(out, "note ") != 10 {
		t.Fatalf("the remainder must arrive next beat, got %d", strings.Count(out, "note "))
	}
}

func TestGateConsultsTargetRepoLedgerForCrossRepoEdits(t *testing.T) {
	// Two independent repos, both buddy-governed. A session in repo A editing
	// an absolute path inside repo B's claimed scope must be denied by B's
	// ledger (Codex final-pass finding).
	fa := newFixture(t)
	fa.initAndHello(t)
	fb := newFixture(t)
	fb.initAndHello(t)
	// Real session ids are globally unique; the fixtures reuse sess-a, which
	// would make B's claim look like the editor's OWN. Claim under a distinct
	// session (latest hello for the worktree wins resolution).
	fb.clock = fb.clock.Add(time.Minute) // break the last_seen tie under the pinned clock
	if _, errw, code := fb.run(t, fb.repo, hookJSON("sess-z", fb.repo, "", ""), "hello", "--label", "zulu"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := fb.run(t, fb.repo, "", "claim", "b-owned", "--desc", "d", "--scope", "internal/core"); code != 0 {
		t.Fatal(errw)
	}
	target := filepath.Join(fb.repo, "internal/core/x.go")
	out, errw, _ := fa.run(t, fa.repo, hookJSON("sess-a", fa.repo, "Edit", target), "gate")
	reason, denied := decodeDeny(t, out)
	if !denied {
		t.Fatalf("cross-repo edit into a claimed scope must be denied: %q (stderr: %q, target: %q)", out, errw, target)
	}
	if !strings.Contains(reason, "b-owned") {
		t.Fatalf("denial must cite the target repo's claim: %q", reason)
	}
	// A cross-repo edit into an UNgoverned location still passes.
	plain := t.TempDir()
	out, _, code := fa.run(t, fa.repo, hookJSON("sess-a", fa.repo, "Edit", filepath.Join(plain, "y.go")), "gate")
	if code != 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("a non-buddy-system location must stay silent: %q", out)
	}
}
