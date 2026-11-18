package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/harness"
	"github.com/JsizzleR/buddy-system/internal/store"
	"github.com/JsizzleR/buddy-system/internal/testguard"
)

func TestMain(m *testing.M) {
	// First, before anything can wedge or be orphaned: this package's tests
	// start child processes, and an orphaned or wedged test binary of theirs
	// had no bound until #130 (D-053). In the watchdog process it never returns.
	testguard.Arm()
	testguard.ScrubGitEnv()
	code := m.Run()
	if built.dir != "" {
		_ = os.RemoveAll(built.dir)
	}
	os.Exit(code)
}

// This fixture invokes the built entrypoint against the built Buddy and a real
// isolated ledger. It proves subprocess/receipt wiring, not native hook trust,
// a Claude process binding, or a provider-backed worker.
func TestReceiptBoundHookExecutable(t *testing.T) {
	for _, name := range []string{"BUDDY_SESSION", "BUDDY_HARNESS", "CODEX_THREAD_ID", "CODEX_SESSION_ID", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_MESSAGING_TOKEN"} {
		t.Setenv(name, "")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	buddy := filepath.Join(dir, "buddy")
	adapter := filepath.Join(dir, "buddy-adapter")
	// The builds have their own bound and every later step liveBound (#90):
	// one 40 s context over both made a starved build eat the hooks' budget.
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), buildBound)
	defer cancelBuild()
	for _, target := range []struct{ out, pkg string }{{buddy, "./cmd/buddy"}, {adapter, "./cmd/buddy-adapter"}} {
		cmd := exec.CommandContext(buildCtx, "go", "build", "-o", target.out, target.pkg)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", target.pkg, err, out)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), liveBound)
	defer cancel()
	repo := filepath.Join(dir, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	repo, err = filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(ctx, "git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	st, err := store.Open(filepath.Join(repo, ".git", "buddy.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	worker, err := st.BeginLogical("worker", "worker", repo, "")
	if err != nil {
		t.Fatal(err)
	}
	peer, err := st.BeginLogical("peer", "peer", repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Claim(worker.SessionID, worker.Incarnation, "worker-path", "", []string{"owned.txt"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Claim(peer.SessionID, peer.Incarnation, "peer-path", "", []string{"held.txt"}); err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(dir, "receipt.json")
	saved := harness.Saved{Schema: harness.Schema, Identity: harness.Identity{Schema: "buddy.lifecycle.v1", Harness: "claude", SessionID: worker.SessionID, Incarnation: worker.Incarnation}, Launch: harness.Launch{Worktree: repo}}
	writeReceipt := func(value harness.Saved) {
		t.Helper()
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(receipt, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeReceipt(saved)
	input := func(path string) map[string]any {
		return map[string]any{"session_id": worker.SessionID, "cwd": repo, "hook_event_name": "PreToolUse", "tool_name": "Write", "tool_input": map[string]any{"file_path": filepath.Join(repo, path), "content": "test"}}
	}
	run := func(payload map[string]any, binary string) ([]byte, string, error) {
		t.Helper()
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, adapter, "hook", "--receipt", receipt, "--buddy", binary)
		cmd.Stdin = bytes.NewReader(data)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		return out, stderr.String(), err
	}
	denied := func(out []byte) bool {
		var v struct {
			Hook struct {
				Decision string `json:"permissionDecision"`
			} `json:"hookSpecificOutput"`
		}
		return json.Unmarshal(out, &v) == nil && v.Hook.Decision == "deny"
	}
	blocked := func(out []byte, err error) bool {
		if denied(out) {
			return true
		}
		failure, ok := err.(*exec.ExitError)
		return ok && failure.ExitCode() == 2
	}
	t.Run("owned positive control", func(t *testing.T) {
		out, stderr, err := run(input("owned.txt"), buddy)
		if err != nil || denied(out) {
			t.Fatalf("owned path refused: %v %s %s", err, out, stderr)
		}
	})
	t.Run("peer conflict", func(t *testing.T) {
		out, stderr, err := run(input("held.txt"), buddy)
		if err != nil || !denied(out) {
			t.Fatalf("peer path was not a native denial: %v %s %s", err, out, stderr)
		}
	})
	// D-228's D9, kept by D-235 for a launch that names no posture: the child
	// gate runs with the own-claim arm OFF, whatever the adapter inherited.
	// allowed.txt is armed (the peer holds held.txt) and no claim of the
	// worker covers it. Positive control first: the same payload straight to
	// `buddy gate` under BUDDY_OWN_CLAIM=deny is refused, so the arm was live
	// for this worker; through the adapter it is 0 bytes.
	t.Run("own-claim arm pinned off", func(t *testing.T) {
		t.Setenv("BUDDY_OWN_CLAIM", "deny")
		data, err := json.Marshal(input("allowed.txt"))
		if err != nil {
			t.Fatal(err)
		}
		direct := exec.CommandContext(ctx, buddy, "gate")
		direct.Dir = repo
		direct.Env = append(os.Environ(), "BUDDY_HARNESS=claude")
		direct.Stdin = bytes.NewReader(data)
		dout, err := direct.Output()
		if err != nil || !denied(dout) || !bytes.Contains(dout, []byte("inside no claim of this session")) {
			t.Fatalf("control: the direct gate under deny must refuse the uncovered path: %v %s", err, dout)
		}
		out, stderr, err := run(input("allowed.txt"), buddy)
		if err != nil || len(bytes.TrimSpace(out)) != 0 {
			t.Fatalf("the adapter's child gate must run with the own-claim arm off (0 bytes): %v %q %s", err, out, stderr)
		}
	})
	// D-235: the posture is the RECEIPT's, appended last, so the inherited
	// value is set to the opposite in every row and never wins. Each row's
	// allowed.txt is armed and uncovered; owned.txt is covered (silent under
	// every posture, the positive control that deny refuses only the
	// uncovered); held.txt is a peer's (denied under every posture, D7).
	withPosture := func(t *testing.T, harnessName, raw string) {
		t.Helper()
		value := saved
		value.Identity.Harness = harnessName
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if raw != "" {
			b = []byte(strings.Replace(string(b), `"launch":{`, `"launch":{"own_claim":`+raw+`,`, 1))
		}
		if err = os.WriteFile(receipt, b, 0600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { writeReceipt(saved) })
	}
	warned := func(out []byte) bool {
		var v struct {
			Hook map[string]any `json:"hookSpecificOutput"`
		}
		if json.Unmarshal(out, &v) != nil {
			return false
		}
		ctx, _ := v.Hook["additionalContext"].(string)
		_, decided := v.Hook["permissionDecision"]
		return v.Hook["hookEventName"] == "PreToolUse" && strings.HasPrefix(ctx, "BUDDY UNCLAIMED:") && !decided
	}
	for _, test := range []struct {
		name, harness, raw, inherited, path string
		want                                string // silent, warn, deny, block
	}{
		{"absent stays off", "claude", "", "deny", "allowed.txt", "silent"},
		{"off stays off", "claude", `"off"`, "deny", "allowed.txt", "silent"},
		{"claude warn", "claude", `"warn"`, "off", "allowed.txt", "warn"},
		{"claude deny", "claude", `"deny"`, "off", "allowed.txt", "deny"},
		{"deny leaves a covered path silent", "claude", `"deny"`, "off", "owned.txt", "silent"},
		{"warn leaves a covered path silent", "claude", `"warn"`, "deny", "owned.txt", "silent"},
		{"peer hold refuses under off", "claude", `"off"`, "", "held.txt", "deny"},
		{"peer hold refuses under warn", "claude", `"warn"`, "", "held.txt", "deny"},
		{"codex deny", "codex", `"deny"`, "off", "allowed.txt", "deny"},
		{"codex warn receipt blocks", "codex", `"warn"`, "", "owned.txt", "block"},
		{"null posture blocks", "claude", `null`, "", "owned.txt", "block"},
		{"empty posture blocks", "claude", `""`, "", "owned.txt", "block"},
		{"another word blocks", "claude", `"Deny"`, "", "owned.txt", "block"},
	} {
		t.Run("posture "+test.name, func(t *testing.T) {
			t.Setenv("BUDDY_OWN_CLAIM", test.inherited)
			withPosture(t, test.harness, test.raw)
			out, stderr, err := run(input(test.path), buddy)
			var ok bool
			switch test.want {
			case "silent":
				ok = err == nil && len(bytes.TrimSpace(out)) == 0
			case "warn":
				ok = err == nil && warned(out) && bytes.Contains(out, []byte(test.path))
			case "deny":
				ok = err == nil && denied(out)
				if test.path == "allowed.txt" {
					ok = ok && bytes.Contains(out, []byte("inside no claim of this session"))
				}
			case "block":
				ok = blocked(out, err) && strings.Contains(stderr+string(out), "own_claim")
			}
			if !ok {
				t.Fatalf("want %s: %v %s %s", test.want, err, out, stderr)
			}
		})
	}
	// The launch file's posture is refused by the adapter's command line
	// before the executable is resolved or any backend exists (D-235): with a
	// nonexistent executable and a gate_command that matches nothing, the
	// refusal names the posture, not either of those. Positive control: the
	// same launch at deny gets past it and is refused for the gate command.
	for _, test := range []struct{ name, harness, raw, want string }{
		{"codex warn", "codex", `"warn"`, "D-228 D9"},
		{"null", "claude", `null`, "own_claim"},
		{"case variant", "claude", `"deny","OWN_CLAIM":"off"`, "own_claim"},
		{"deny control", "codex", `"deny"`, "gate_command must exactly match"},
	} {
		t.Run("launch "+test.name, func(t *testing.T) {
			launchFile := filepath.Join(t.TempDir(), "launch.json")
			body := `{"worktree":` + strconv.Quote(repo) + `,"gate_command":"x","own_claim":` + test.raw + `}`
			if err := os.WriteFile(launchFile, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, adapter, "--harness", test.harness, "--executable", filepath.Join(dir, "no-such-native"),
				"--buddy", buddy, "--launch", launchFile, "--receipt", filepath.Join(t.TempDir(), "worker.json"))
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			err := cmd.Run()
			if err == nil || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("want a refusal naming %q: %v %s", test.want, err, stderr.String())
			}
		})
	}
	// A --buddy whose `adapter inspect` reports no own-claim gate (a build
	// before D-228: the same ledger schema, so nothing else refuses it) BLOCKS
	// every call under a non-off posture, and is fine under off (positive
	// control: the wrapper is otherwise transparent).
	old := filepath.Join(dir, "buddy-before-d228")
	script := "#!/bin/sh\nif [ \"$1\" = adapter ] && [ \"$2\" = inspect ]; then\n  \"" + buddy + "\" \"$@\" | sed 's/,\"gate\":{[^}]*}//'\n  exit $?\nfi\nexec \"" + buddy + "\" \"$@\"\n"
	if err := os.WriteFile(old, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ raw, want string }{{`"off"`, "silent"}, {`"deny"`, "block"}, {`"warn"`, "block"}} {
		t.Run("old buddy under "+test.raw, func(t *testing.T) {
			withPosture(t, "claude", test.raw)
			out, stderr, err := run(input("owned.txt"), old)
			if test.want == "silent" && (err != nil || len(bytes.TrimSpace(out)) != 0) {
				t.Fatalf("positive control through the wrapper: %v %s %s", err, out, stderr)
			}
			if test.want == "block" && (!blocked(out, err) || !strings.Contains(stderr+string(out), "does not report an own-claim gate")) {
				t.Fatalf("an old buddy must block a non-off posture: %v %s %s", err, out, stderr)
			}
		})
	}
	// A --buddy that states an own-claim gate WITHOUT this posture blocks it
	// too: gateHonours checks the posture is listed, not that a list exists
	// (Fable cross-check: "the list is non-empty" stayed green). Deny is
	// listed, so it passes: the positive control.
	partial := filepath.Join(dir, "buddy-without-warn")
	script = "#!/bin/sh\nif [ \"$1\" = adapter ] && [ \"$2\" = inspect ]; then\n  \"" + buddy + "\" \"$@\" | sed 's/\"own_claim\":\\[\"off\",\"warn\",\"deny\"\\]/\"own_claim\":[\"off\",\"deny\"]/'\n  exit $?\nfi\nexec \"" + buddy + "\" \"$@\"\n"
	if err := os.WriteFile(partial, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ raw, want string }{{`"deny"`, "silent"}, {`"warn"`, "block"}} {
		t.Run("buddy without warn under "+test.raw, func(t *testing.T) {
			withPosture(t, "claude", test.raw)
			out, stderr, err := run(input("owned.txt"), partial)
			if test.want == "silent" && (err != nil || len(bytes.TrimSpace(out)) != 0) {
				t.Fatalf("positive control through the wrapper: %v %s %s", err, out, stderr)
			}
			if test.want == "block" && (!blocked(out, err) || !strings.Contains(stderr+string(out), "does not report an own-claim gate")) {
				t.Fatalf("a buddy without warn must block warn: %v %s %s", err, out, stderr)
			}
		})
	}
	for _, test := range []struct{ name, key, value string }{
		{"wrong session", "session_id", "successor"}, {"wrong worktree", "cwd", dir},
		{"subagent", "agent_id", "native-child"}, {"parent session", "parent_session_id", "parent"},
		{"parent thread", "parent_thread_id", "parent"}, {"wrong event", "hook_event_name", "SessionStart"},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload := input("owned.txt")
			payload[test.key] = test.value
			out, stderr, err := run(payload, buddy)
			if !blocked(out, err) {
				t.Fatalf("identity/event mismatch did not BLOCK the native tool (exit 1 is non-blocking): %v %s %s", err, out, stderr)
			}
		})
	}
	// #134: the payload's cwd is the worker's shell directory, and a Claude
	// worker that ran `cd sub` reports <worktree>/sub. Equality refused that
	// call, the `cd ..` that would repair it included, before the gate ran: a
	// BLOCK, so no tool call could ever get the worker out. The check is now
	// PHYSICAL containment (EvalSymlinks both sides, then invariant 14's exact
	// form); the payload reaches the gate unchanged, so a relative path still
	// resolves against the real cwd. Each allow row was watched to BLOCK on the
	// equality; each deny row is a control that kills one weaker check.
	sub := filepath.Join(repo, "sub")
	evil := filepath.Join(dir, "repo-evil")
	// repo/evil exists too: without it a prefix test missing its "/" was
	// still refused, by accident, when the walk below the root looked for
	// `repo/evil` (the sibling's name past the dash) and found nothing.
	for _, d := range []string{sub, evil, filepath.Join(repo, "evil")} {
		if err := os.Mkdir(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(dir, filepath.Join(repo, "out")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(repo, filepath.Join(dir, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := st.Claim(peer.SessionID, peer.Incarnation, "peer-sub", "", []string{"sub/peer.txt"}); err != nil {
		t.Fatal(err)
	}
	// Repositories nested inside the worktree, each a shape where the gate's
	// home is NOT the worktree's ledger at the worktree's root: its own ledger
	// (gov, a nested bare repository, core.worktree pointing back up, a linked
	// worktree of another governed repository, a git directory reached through
	// commondir), or the worktree's ledger placed against a nested root (a
	// linked worktree of the worktree's own repository, a .git symlink to its
	// git directory, a hand-written gitdir pointer). Containment alone let each
	// pass a peer's `vendor` claim or the worktree's brake that a call from the
	// worktree root meets (panel lens b, reproduced); every one is refused.
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	ledger := func(gitDir string) {
		t.Helper()
		nested, err := store.Open(filepath.Join(gitDir, "buddy.db"), nil)
		if err != nil {
			t.Fatal(err)
		}
		nested.Close()
	}
	vendor := filepath.Join(repo, "vendor")
	gov, cw := filepath.Join(vendor, "gov"), filepath.Join(vendor, "cw")
	for _, d := range []string{gov, cw} {
		git("init", "-q", d)
		ledger(filepath.Join(d, ".git"))
	}
	if err := os.Mkdir(filepath.Join(gov, "deeper"), 0700); err != nil {
		t.Fatal(err)
	}
	git("-C", cw, "config", "core.worktree", "../../..")
	bare := filepath.Join(repo, "cache.git")
	git("init", "-q", "--bare", bare)
	ledger(bare)
	git("-C", repo, "commit", "-q", "--allow-empty", "-m", "fixture")
	git("-C", repo, "worktree", "add", "-q", "--detach", filepath.Join(vendor, "same"))
	other := filepath.Join(dir, "other")
	git("init", "-q", other)
	ledger(filepath.Join(other, ".git"))
	git("-C", other, "commit", "-q", "--allow-empty", "-m", "fixture")
	git("-C", other, "worktree", "add", "-q", "--detach", filepath.Join(vendor, "sw"))
	for _, d := range []string{"ln", "hw"} {
		if err := os.Mkdir(filepath.Join(vendor, d), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(repo, ".git"), filepath.Join(vendor, "ln", ".git")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vendor, "hw", ".git"), []byte("gitdir: ../../.git\n"), 0600); err != nil {
		t.Fatal(err)
	}
	admin := filepath.Join(repo, "wtadmin")
	if err := os.Mkdir(admin, 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"HEAD": "ref: refs/heads/main\n", "commondir": filepath.Join(gov, ".git") + "\n"} {
		if err := os.WriteFile(filepath.Join(admin, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// git's necessary test for a git directory, each arm pinned: a dangling
	// symbolic HEAD is still a HEAD to git (Lstat, not Stat; with objects and
	// refs present git stops there, checked by hand: "must be run in a work
	// tree"), and a lone HEAD
	// file with neither objects nor commondir is a plain directory, the
	// positive control that the HEAD arm alone does not refuse.
	dangling, notes := filepath.Join(repo, "dangling"), filepath.Join(repo, "notes")
	for _, d := range []string{"objects", "refs"} {
		if err := os.MkdirAll(filepath.Join(dangling, d), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("refs/heads/main", filepath.Join(dangling, "HEAD")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(notes, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notes, "HEAD"), []byte("not a ref\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := st.Claim(peer.SessionID, peer.Incarnation, "peer-vendor", "", []string{"vendor", "cache.git", "wtadmin", "dangling"}); err != nil {
		t.Fatal(err)
	}
	// Spellings whose `..` comes AFTER a symlink: the kernel applies it to the
	// link's target, a lexical Clean to the link's parent (panel lens a: an
	// EvalSymlinks(Clean(cwd)) mutant passed every other row). And a link from
	// a plain subdirectory into a nested repository's subdirectory: a walk
	// over the payload's spelling finds no .git on its way down (Fable
	// cross-check: what refuses that walk is only the IsDir test at the link).
	outside := filepath.Join(dir, "outside")
	for _, d := range []string{filepath.Join(outside, "deep"), filepath.Join(sub, "deep")} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{
		filepath.Join(repo, "out2"):   filepath.Join(outside, "deep"),
		filepath.Join(outside, "in"):  filepath.Join(sub, "deep"),
		filepath.Join(sub, "linkgov"): filepath.Join(gov, "deeper"),
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	call := func(cwd, session, tool string, args map[string]any) map[string]any {
		return map[string]any{"session_id": session, "cwd": cwd, "hook_event_name": "PreToolUse", "tool_name": tool, "tool_input": args}
	}
	write := func(path string) map[string]any { return map[string]any{"file_path": path, "content": "test"} }
	bash := func(command string) map[string]any { return map[string]any{"command": command} }
	const (
		allow = iota
		nativeDeny
		block
	)
	for _, test := range []struct {
		name    string
		payload map[string]any
		want    int
	}{
		{"subdirectory, owned absolute Write", call(sub, worker.SessionID, "Write", write(filepath.Join(repo, "owned.txt"))), allow},
		{"subdirectory, Bash cd ..", call(sub, worker.SessionID, "Bash", bash("cd ..")), allow},
		{"subdirectory spelled through dot-dot", call(sub+"/../sub/", worker.SessionID, "Write", write(filepath.Join(repo, "owned.txt"))), allow},
		{"subdirectory through a symlink to the worktree", call(filepath.Join(dir, "alias", "sub"), worker.SessionID, "Write", write(filepath.Join(repo, "owned.txt"))), allow},
		{"subdirectory, relative free path", call(sub, worker.SessionID, "Write", write("free.txt")), allow},
		// The gate still decides, from the real cwd: sub/peer.txt is the
		// peer's. Resolved against the worktree root it would be peer.txt,
		// unclaimed, and allowed (the row above is its control).
		{"subdirectory, relative peer path", call(sub, worker.SessionID, "Write", write("peer.txt")), nativeDeny},
		{"sibling sharing the worktree's prefix", call(evil, worker.SessionID, "Write", write(filepath.Join(repo, "owned.txt"))), block},
		{"symlink inside the worktree leading out", call(filepath.Join(repo, "out"), worker.SessionID, "Write", write(filepath.Join(repo, "owned.txt"))), block},
		{"dot-dot out of the worktree", call(sub+"/../..", worker.SessionID, "Write", write(filepath.Join(repo, "owned.txt"))), block},
		{"empty cwd", call("", worker.SessionID, "Write", write(filepath.Join(repo, "owned.txt"))), block},
		{"relative cwd", call("sub", worker.SessionID, "Write", write(filepath.Join(repo, "owned.txt"))), block},
		{"vanished cwd", call(filepath.Join(repo, "gone"), worker.SessionID, "Write", write(filepath.Join(repo, "owned.txt"))), block},
		{"upper-cased alias of the worktree", call(filepath.Join(filepath.Dir(repo), strings.ToUpper(filepath.Base(repo)), "sub"), worker.SessionID, "Write", write(filepath.Join(repo, "owned.txt"))), block},
		{"wrong session in a subdirectory", call(sub, "successor", "Write", write(filepath.Join(repo, "owned.txt"))), block},
		{"dot-dot after a symlink out", call(filepath.Join(repo, "out2")+"/..", worker.SessionID, "Write", write(filepath.Join(repo, "owned.txt"))), block},
		{"dot-dot after a symlink in", call(filepath.Join(outside, "in")+"/..", worker.SessionID, "Write", write(filepath.Join(repo, "owned.txt"))), allow},
		{"dot-dot after a symlink in, relative peer path", call(filepath.Join(outside, "in")+"/..", worker.SessionID, "Write", write("peer.txt")), nativeDeny},
		{"nested repository with its own ledger", call(gov, worker.SessionID, "Write", write("x.c")), block},
		{"below a nested repository with its own ledger", call(filepath.Join(gov, "deeper"), worker.SessionID, "Write", write("x.c")), block},
		{"symlink into a nested repository", call(filepath.Join(sub, "linkgov"), worker.SessionID, "Write", write("x.c")), block},
		{"nested repository whose core.worktree is the worktree", call(cw, worker.SessionID, "Bash", bash("touch f")), block},
		{"nested bare repository with a ledger", call(bare, worker.SessionID, "Bash", bash("touch f")), block},
		{"linked worktree of the worktree's repository", call(filepath.Join(vendor, "same"), worker.SessionID, "Write", write("x.c")), block},
		{"linked worktree of another governed repository", call(filepath.Join(vendor, "sw"), worker.SessionID, "Write", write("x.c")), block},
		{".git symlink to the worktree's git directory", call(filepath.Join(vendor, "ln"), worker.SessionID, "Write", write("x.c")), block},
		{"hand-written gitdir pointer to the worktree's git directory", call(filepath.Join(vendor, "hw"), worker.SessionID, "Write", write("x.c")), block},
		{"git directory reached through commondir", call(admin, worker.SessionID, "Bash", bash("touch f")), block},
		{"git directory with a dangling symbolic HEAD", call(dangling, worker.SessionID, "Bash", bash("touch f")), block},
		{"directory holding a plain HEAD file", call(notes, worker.SessionID, "Write", write("x.c")), allow},
		{"two levels below the worktree", call(filepath.Join(sub, "deep"), worker.SessionID, "Bash", bash("cd ../..")), allow},
	} {
		t.Run("cwd: "+test.name, func(t *testing.T) {
			// Exit 2 is also what an Inspect or gate-process failure
			// gives, so the check's own verdict is asserted too: every
			// block row but the wrong session is refused BY THE CWD.
			cwd, _ := test.payload["cwd"].(string)
			if want := !(test.want == block && test.payload["session_id"] == worker.SessionID); judgedFromWorktree(repo, cwd) != want {
				t.Fatalf("judgedFromWorktree(%q) != %v", cwd, want)
			}
			out, stderr, err := run(test.payload, buddy)
			switch test.want {
			case allow:
				if err != nil || denied(out) {
					t.Fatalf("contained cwd refused: %v %s %s", err, out, stderr)
				}
			case nativeDeny:
				if err != nil || !denied(out) {
					t.Fatalf("the gate did not decide from the real cwd: %v %s %s", err, out, stderr)
				}
			case block:
				// The adapter's OWN refusal (exit 2), not any deny: the gate
				// natively denies some of these cwds anyway (a relative Write
				// in a git directory has no root to place against), so
				// "denied" could not tell the check from its absence.
				if failure, ok := err.(*exec.ExitError); !ok || failure.ExitCode() != 2 {
					t.Fatalf("cwd the gate would not judge from the worktree's ledger at its root was not refused by the adapter: %v %s %s", err, out, stderr)
				}
			}
		})
	}
	// A RELATIVE payload cwd is refused even where it resolves into the
	// worktree: EvalSymlinks resolves it against the HOOK's process
	// directory, through a link there to the worktree, while the gate, run
	// at the worktree, would resolve it against that (Codex code pass: an
	// `IsAbs` mutant passed every row whose relative cwd resolved nowhere).
	t.Run("cwd: relative, resolved through a link beside the hook", func(t *testing.T) {
		hookDir := filepath.Join(dir, "hookcwd")
		if err := os.Mkdir(hookDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(sub, filepath.Join(hookDir, "link")); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(call("link", worker.SessionID, "Write", write(filepath.Join(repo, "owned.txt"))))
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, adapter, "hook", "--receipt", receipt, "--buddy", buddy)
		cmd.Dir = hookDir
		cmd.Stdin = bytes.NewReader(data)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if failure, ok := err.(*exec.ExitError); !ok || failure.ExitCode() != 2 {
			t.Fatalf("relative cwd was not refused by the adapter: %v %s %s", err, out, stderr.String())
		}
	})
	// The receipt's side is resolved too: a worktree captured through a link
	// (here dir/alias -> repo, dir itself under the /var -> /private/var link)
	// admits its own root and a subdirectory (Fable cross-check: every other
	// row's receipt names the resolved path, so a Clean there survived).
	t.Run("cwd: receipt worktree spelled through a link", func(t *testing.T) {
		linked := saved
		linked.Launch.Worktree = filepath.Join(dir, "alias")
		writeReceipt(linked)
		defer writeReceipt(saved)
		for _, cwd := range []string{repo, sub} {
			out, stderr, err := run(call(cwd, worker.SessionID, "Write", write(filepath.Join(repo, "owned.txt"))), buddy)
			if err != nil || denied(out) {
				t.Fatalf("cwd %s under a linked receipt worktree refused: %v %s %s", cwd, err, out, stderr)
			}
		}
	})
	t.Run("endpoint failure remains unknown", func(t *testing.T) {
		// An endpoint failure is advisory; force that exact subprocess to fail
		// regardless of the harness under which this test binary was launched.
		endpointWorker, err := st.BeginLogical("endpoint-worker", "endpoint-worker", repo, "")
		if err != nil {
			t.Fatal(err)
		}
		endpointSaved := saved
		endpointSaved.Identity.SessionID, endpointSaved.Identity.Incarnation = endpointWorker.SessionID, endpointWorker.Incarnation
		writeReceipt(endpointSaved)
		defer writeReceipt(saved)
		proxy := filepath.Join(dir, "buddy-endpoint-failure")
		program := fmt.Sprintf("#!/usr/bin/env python3\nimport os,sys\nif sys.argv[1:3]==['adapter','endpoint']:\n sys.stderr.write('endpoint unavailable in fixture\\n'); sys.exit(1)\nos.execv(%q,[%q]+sys.argv[1:])\n", buddy, buddy)
		if err := os.WriteFile(proxy, []byte(program), 0700); err != nil {
			t.Fatal(err)
		}
		payload := input("endpoint-free.txt")
		payload["session_id"] = endpointWorker.SessionID
		out, stderr, err := run(payload, proxy)
		if err != nil || denied(out) || !strings.Contains(stderr, "endpoint unavailable in fixture") {
			t.Fatalf("endpoint failure changed gate: %v %s %s", err, out, stderr)
		}
		inspection, err := st.InspectAdapter(store.AdapterIdentity{SessionID: endpointWorker.SessionID, Incarnation: endpointWorker.Incarnation})
		if err != nil {
			t.Fatal(err)
		}
		if inspection.Endpoint.Status != "unknown" {
			t.Fatalf("endpoint failure became absence/presence: %+v", inspection.Endpoint)
		}
	})
	// F-29: the advisory endpoint step shared the gate's deadline, so a slow
	// lock-holder on its write transaction spent the gate's time and the tool
	// call was denied. It now has its own short budget.
	t.Run("slow endpoint cannot deny the tool call", func(t *testing.T) {
		slowWorker, err := st.BeginLogical("slow-endpoint-worker", "slow-endpoint-worker", repo, "")
		if err != nil {
			t.Fatal(err)
		}
		slowSaved := saved
		slowSaved.Identity.SessionID, slowSaved.Identity.Incarnation = slowWorker.SessionID, slowWorker.Incarnation
		writeReceipt(slowSaved)
		defer writeReceipt(saved)
		proxy := filepath.Join(dir, "buddy-endpoint-slow")
		program := fmt.Sprintf("#!/usr/bin/env python3\nimport os,sys,time\nif sys.argv[1:3]==['adapter','endpoint']:\n time.sleep(20); sys.exit(0)\nos.execv(%q,[%q]+sys.argv[1:])\n", buddy, buddy)
		if err := os.WriteFile(proxy, []byte(program), 0700); err != nil {
			t.Fatal(err)
		}
		payload := input("slow-endpoint.txt")
		payload["session_id"] = slowWorker.SessionID
		began := time.Now()
		out, stderr, err := run(payload, proxy)
		elapsed := time.Since(began)
		if err != nil || denied(out) {
			t.Fatalf("a slow advisory step denied the tool call after %v: %v %s %s", elapsed, err, out, stderr)
		}
		if elapsed > harness.HookGateBudget+harness.HookEndpointBudget+2*time.Second {
			t.Fatalf("hook took %v; the endpoint step is not bounded by its own budget", elapsed)
		}
		inspection, err := st.InspectAdapter(store.AdapterIdentity{SessionID: slowWorker.SessionID, Incarnation: slowWorker.Incarnation})
		if err != nil {
			t.Fatal(err)
		}
		if inspection.Endpoint.Status != "unknown" {
			t.Fatalf("a timed-out endpoint step became an observation: %+v", inspection.Endpoint)
		}
	})
	t.Run("stale receipt cannot mutate successor", func(t *testing.T) {
		if err := st.EndLogical(worker.SessionID, worker.Incarnation); err != nil {
			t.Fatal(err)
		}
		next, err := st.BeginLogical(worker.SessionID, "", repo, worker.Incarnation)
		if err != nil {
			t.Fatal(err)
		}
		out, stderr, err := run(input("owned.txt"), buddy)
		if !blocked(out, err) {
			t.Fatalf("stale receipt did not BLOCK the native tool (exit 1 is non-blocking): %v %s %s", err, out, stderr)
		}
		current, err := st.InspectAdapter(store.AdapterIdentity{SessionID: next.SessionID, Incarnation: next.Incarnation})
		if err != nil || current.Lifecycle.Value != "live" {
			t.Fatalf("late hook changed successor: %+v %v", current, err)
		}
		fresh := saved
		fresh.Identity.Incarnation = next.Incarnation
		writeReceipt(fresh)
		out, stderr, err = run(input("owned.txt"), buddy)
		if err != nil || denied(out) {
			t.Fatalf("fresh successor positive control failed: %v %s %s", err, out, stderr)
		}
	})
}

// F-29, the half the fixture above cannot see: its gate is fast, so it passed
// whether or not the endpoint's time came out of the gate's budget, and the
// verification's mutant charging it there survived every permanent test. Here
// the gate itself takes most of its budget. The fake endpoint leaves a child
// holding stderr, so its step costs HookEndpointBudget twice (the context,
// then WaitDelay): about 500 ms. The gate takes HookGateBudget minus ONE
// endpoint budget, which fits in the budget it is owed and not in what would
// be left after the endpoint's 500 ms (margins of about a quarter second each
// way). The control is a gate past its whole budget: killed, so the deadline
// is real and the first case is not passing for want of one.
func TestHookEndpointTimeIsNotChargedToTheGate(t *testing.T) {
	for _, test := range []struct {
		name  string
		gate  time.Duration
		allow bool
	}{
		{"gate uses the budget the endpoint did not spend", harness.HookGateBudget - harness.HookEndpointBudget, true},
		{"control: gate past its whole budget is killed", harness.HookGateBudget + 400*time.Millisecond, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			receipt, buddy := filepath.Join(dir, "receipt.json"), filepath.Join(dir, "buddy")
			saved := harness.Saved{Schema: harness.Schema, Identity: harness.Identity{Schema: "buddy.lifecycle.v1", Harness: "claude", SessionID: "native", Incarnation: "captured"}, Launch: harness.Launch{Worktree: dir}}
			data, err := json.Marshal(saved)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(receipt, data, 0600); err != nil {
				t.Fatal(err)
			}
			program := fmt.Sprintf(`#!/bin/sh
case "$1 $2" in
"adapter inspect") printf '%%s\n' '{"schema":"buddy.adapter.v1","identity":{"session_id":"native","incarnation":"captured"},"lifecycle":{"value":"live"}}' ;;
"adapter endpoint") sleep 1 & exec sleep 30 ;;
*) exec sleep %.3f ;;
esac
`, test.gate.Seconds())
			if err := os.WriteFile(buddy, []byte(program), 0700); err != nil {
				t.Fatal(err)
			}
			input, err := json.Marshal(map[string]string{"session_id": "native", "cwd": dir, "hook_event_name": "PreToolUse"})
			if err != nil {
				t.Fatal(err)
			}
			var out, errout bytes.Buffer
			err = runHook([]string{"--receipt", receipt, "--buddy", buddy}, bytes.NewReader(input), &out, &errout)
			if (err == nil) != test.allow {
				t.Fatalf("gate of %v: want allowed=%v, got %v; stderr: %s", test.gate, test.allow, err, errout.String())
			}
		})
	}
}

// The device arm of judgedFromWorktree's walk: a filesystem mounted below the
// worktree is a level git's discovery stops at (panel lens a reproduced the
// gh-stack slot skip from one), and a hermetic test cannot mount. The level
// is a plain directory; only the device it is compared with differs, and the
// same level on its own device is the positive control.
func TestPlainLevelRefusesAnotherDevice(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	dev, ok := deviceOf(fi)
	if !ok {
		t.Fatal("no device number")
	}
	if !plainLevel(dir, dev) {
		t.Fatal("control: a plain directory on the worktree's device was refused")
	}
	if plainLevel(dir, dev+1) {
		t.Fatal("a level on another device passed")
	}
}

// The root's side of the device comparison: a level mounted directly below the
// worktree is refused only if the device it is compared with is the
// WORKTREE's. Statting the cwd instead compared the mount with itself (Codex
// code pass). deviceOf reports another device for any directory named "mnt";
// a plain sibling on the real device is the positive control.
func TestJudgedFromWorktreeComparesWithTheRootsDevice(t *testing.T) {
	w, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"mnt/inner", "plain"} {
		if err := os.MkdirAll(filepath.Join(w, d), 0700); err != nil {
			t.Fatal(err)
		}
	}
	real := deviceOf
	t.Cleanup(func() { deviceOf = real })
	deviceOf = func(fi os.FileInfo) (uint64, bool) {
		dev, ok := real(fi)
		if fi.Name() == "mnt" || fi.Name() == "inner" {
			dev++
		}
		return dev, ok
	}
	for _, test := range []struct {
		cwd  string
		want bool
	}{{"plain", true}, {"mnt", false}, {"mnt/inner", false}} {
		if got := judgedFromWorktree(w, filepath.Join(w, test.cwd)); got != test.want {
			t.Errorf("judgedFromWorktree(<root>/%s) = %v, want %v", test.cwd, got, test.want)
		}
	}
}

// A cwd check that stalls (the worker's cwd on a network mount gone away) is a
// refusal within the gate's budget: a hook that outlives its native timeout is
// neither a deny nor exit 2, and the tool call goes ahead (Fable cross-check).
// The control is the same hook with the real check: the fake gate allows.
func TestHookStalledCwdCheckIsARefusalWithinTheBudget(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	receipt, buddy := filepath.Join(dir, "receipt.json"), filepath.Join(dir, "buddy")
	saved := harness.Saved{Schema: harness.Schema, Identity: harness.Identity{Schema: "buddy.lifecycle.v1", Harness: "codex", SessionID: "native", Incarnation: "captured"}, Launch: harness.Launch{Worktree: dir}}
	data, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receipt, data, 0600); err != nil {
		t.Fatal(err)
	}
	program := `#!/bin/sh
case "$1 $2" in
"adapter inspect") printf '%s\n' '{"schema":"buddy.adapter.v1","identity":{"session_id":"native","incarnation":"captured"},"lifecycle":{"value":"live"}}' ;;
*) exit 0 ;;
esac
`
	if err := os.WriteFile(buddy, []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]string{"session_id": "native", "cwd": dir, "hook_event_name": "PreToolUse"})
	if err != nil {
		t.Fatal(err)
	}
	hook := func() (time.Duration, error) {
		var out, errout bytes.Buffer
		began := time.Now()
		err := runHook([]string{"--receipt", receipt, "--buddy", buddy}, bytes.NewReader(input), &out, &errout)
		return time.Since(began), err
	}
	if _, err := hook(); err != nil {
		t.Fatalf("control: the unstalled hook refused: %v", err)
	}
	release := make(chan struct{})
	defer close(release)
	real := cwdJudge
	// Released after twice the budget at the latest, so a hook that waits on
	// the check unbounded fails here in seconds rather than at go test's
	// timeout: the check then answers "inside", and the hook allows.
	cwdJudge = func(string, string) bool {
		select {
		case <-release:
		case <-time.After(2 * harness.HookGateBudget):
		}
		return true
	}
	defer func() { cwdJudge = real }()
	elapsed, err := hook()
	if err == nil {
		t.Fatal("a stalled cwd check was not a refusal")
	}
	if elapsed > harness.HookGateBudget+time.Second {
		t.Fatalf("the stalled check took %v, past the gate budget", elapsed)
	}
}
