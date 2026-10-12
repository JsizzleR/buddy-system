package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/harness"
	"github.com/JsizzleR/buddy-system/internal/store"
	"github.com/JsizzleR/buddy-system/internal/testguard"
)

func TestMain(m *testing.M) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	for _, target := range []struct{ out, pkg string }{{buddy, "./cmd/buddy"}, {adapter, "./cmd/buddy-adapter"}} {
		cmd := exec.CommandContext(ctx, "go", "build", "-o", target.out, target.pkg)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", target.pkg, err, out)
		}
	}
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
