package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestJSONProcessHelper(t *testing.T) {
	if os.Getenv("BUDDY_PROCESS_HELPER") != "1" {
		return
	}
	if os.Getenv("BUDDY_PROCESS_MODE") == "argv0" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"argv0": os.Args[0]})
		var input any
		_ = json.NewDecoder(os.Stdin).Decode(&input)
		os.Exit(0)
	}
	if os.Getenv("BUDDY_PROCESS_MODE") == "ownership" {
		// Read the ownership record BEFORE the first byte of input arrives,
		// then report it once the byte has come: what the record said before
		// the adapter wrote anything.
		record, _ := os.ReadFile(os.Getenv("BUDDY_PROCESS_RECORD"))
		var input any
		_ = json.NewDecoder(os.Stdin).Decode(&input)
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"record": string(record), "pid": os.Getpid()})
		os.Exit(0)
	}
	if os.Getenv("BUDDY_PROCESS_MODE") == "eof" {
		os.Stdout.Close()
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
	if mode := os.Getenv("BUDDY_PROCESS_MODE"); mode == "approval-eof" || mode == "flood-eof" {
		enc, dec := json.NewEncoder(os.Stdout), json.NewDecoder(os.Stdin)
		if mode == "approval-eof" {
			_ = enc.Encode(map[string]string{"native": "ready"})
			var start any
			if dec.Decode(&start) != nil {
				os.Exit(2)
			}
		} else {
			for i := 0; i < 512; i++ {
				if enc.Encode(map[string]int{"native_event": i}) != nil {
					os.Exit(3)
				}
			}
		}
		// This models a broken native output stream whose process still
		// accepts input. An approval must not reach it after stdout EOF.
		os.Stdout.Close()
		var approval any
		if dec.Decode(&approval) == nil {
			_ = os.WriteFile(os.Getenv("BUDDY_PROCESS_EFFECT"), []byte("approval received"), 0600)
		}
		os.Exit(0)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"thread": os.Getenv("CODEX_THREAD_ID"), "token": os.Getenv("CLAUDE_CODE_MESSAGING_TOKEN")})
	if os.Getenv("BUDDY_PROCESS_MODE") == "blocked" {
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
	var v any
	if json.NewDecoder(os.Stdin).Decode(&v) == nil {
		_ = json.NewEncoder(os.Stdout).Encode(v)
	}
	os.Exit(0)
}

func TestClaudeProcessKeepsExactExecutableAndRecognizableArgv(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, claude := range []bool{true, false} {
		start, want := StartJSONProcess, exe
		if claude {
			start, want = startClaudeProcess, "claude"
		}
		p, err := start(context.Background(), testAuthorization(t), exe, []string{"-test.run=^TestJSONProcessHelper$"}, t.TempDir(), []string{"BUDDY_PROCESS_HELPER=1", "BUDDY_PROCESS_MODE=argv0"})
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		if p.cmd.Path != exe {
			t.Fatalf("changed exact executable: %q", p.cmd.Path)
		}
		select {
		case raw := <-p.Read():
			var got map[string]string
			if json.Unmarshal(raw, &got) != nil || got["argv0"] != want {
				t.Errorf("Claude=%v: argv0=%s, want %q", claude, raw, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("helper did not report its process name")
		}
	}
}

func TestJSONProcessEOFStopsWritesWhileChildWaitsForInput(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	effect := filepath.Join(root, "approval-effect")
	p, err := StartJSONProcess(context.Background(), testAuthorization(t), exe, []string{"-test.run=^TestJSONProcessHelper$"}, root, []string{"BUDDY_PROCESS_HELPER=1", "BUDDY_PROCESS_MODE=approval-eof", "BUDDY_PROCESS_EFFECT=" + effect})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	select {
	case <-p.Read():
	case <-time.After(time.Second):
		t.Fatal("helper did not become ready")
	}
	if err := p.Write(map[string]string{"control": "close stdout"}); err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-p.Read():
		if ok {
			t.Fatal("unexpected native frame")
		}
	case <-time.After(time.Second):
		t.Fatal("EOF not observed")
	}
	if err := p.WriteContext(context.Background(), map[string]string{"approval": "allow"}); err == nil {
		t.Error("stdout EOF left approval writes enabled")
	}
	select {
	case <-p.done:
	case <-time.After(time.Second):
		t.Error("EOF left owned process unreaped")
	}
	if _, err := os.Stat(effect); !os.IsNotExist(err) {
		t.Errorf("native process received approval after EOF: %v", err)
	}
}

func TestJSONProcessOutputOverflowClosesWithoutConsumer(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	effect := filepath.Join(root, "approval-effect")
	p, err := StartJSONProcess(context.Background(), testAuthorization(t), exe, []string{"-test.run=^TestJSONProcessHelper$"}, root, []string{"BUDDY_PROCESS_HELPER=1", "BUDDY_PROCESS_MODE=flood-eof", "BUDDY_PROCESS_EFFECT=" + effect})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	// Deliberately never consume Read. A saturated backend cannot be allowed
	// to hide terminal transport state behind a second bounded queue.
	select {
	case <-p.stop:
	case <-time.After(time.Second):
		t.Error("native frame backpressure left write admission enabled")
	}
	if err := p.WriteContext(context.Background(), map[string]string{"approval": "allow"}); err == nil {
		t.Error("approval written through overflowing native output")
	}
	if err := p.Err(); err == nil || !strings.Contains(err.Error(), "backpressure") {
		t.Errorf("overflow lacks explicit transport error: %v", err)
	}
	_ = p.Close()
	select {
	case <-p.done:
	case <-time.After(time.Second):
		t.Error("overflowed owned process did not stop")
	}
	if _, err := os.Stat(effect); !os.IsNotExist(err) {
		t.Errorf("native process received approval after output saturation: %v", err)
	}
}

func TestJSONProcessEOFPrecedesProcessExit(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p, err := StartJSONProcess(context.Background(), testAuthorization(t), exe, []string{"-test.run=^TestJSONProcessHelper$"}, t.TempDir(), []string{"BUDDY_PROCESS_HELPER=1", "BUDDY_PROCESS_MODE=eof"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	select {
	case _, ok := <-p.Read():
		if ok {
			t.Fatal("unexpected native output")
		}
	case <-time.After(time.Second):
		t.Fatal("native stdout EOF hidden until process exit")
	}
}
func TestJSONProcessBlockedWriteHonorsContextAndCloses(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p, err := StartJSONProcess(context.Background(), testAuthorization(t), exe, []string{"-test.run=^TestJSONProcessHelper$"}, t.TempDir(), []string{"BUDDY_PROCESS_HELPER=1", "BUDDY_PROCESS_MODE=blocked"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	<-p.Read()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err = p.WriteContext(ctx, strings.Repeat("x", 2<<20))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked pipe did not respect deadline: %v", err)
	}
	select {
	case <-p.done:
	case <-time.After(time.Second):
		t.Fatal("uncertain partial write left process alive")
	}
}
func TestJSONProcessOwnsLifetimeAndScrubsParentIdentity(t *testing.T) {
	t.Setenv("CODEX_THREAD_ID", "coordinator")
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "parent-token")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p, err := StartJSONProcess(ctx, testAuthorization(t), exe, []string{"-test.run=^TestJSONProcessHelper$"}, t.TempDir(), []string{"BUDDY_PROCESS_HELPER=1"})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	cancel() // cancelling the completed startup call must not kill the worker.
	select {
	case raw := <-p.Read():
		var v map[string]string
		if err = json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		if v["thread"] != "" || v["token"] != "" {
			t.Fatal("parent identity inherited")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no helper output")
	}
	if err = p.Write(map[string]string{"still": "live"}); err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-p.Read():
		if string(raw) != `{"still":"live"}` {
			t.Fatalf("worker lost: %s", raw)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker disappeared")
	}
}

// readJSONLine waits for one native frame and decodes it.
func readJSONLine(t *testing.T, p *JSONProcess, v any) {
	t.Helper()
	select {
	case line, ok := <-p.Read():
		if !ok {
			t.Fatal("native stream closed before the expected frame")
		}
		if err := json.Unmarshal(line, v); err != nil {
			t.Fatalf("frame %s: %v", line, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no native frame")
	}
}

func alive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }

// waitGone polls until pid no longer exists (or is a zombie we do not own:
// a reparented zombie is reaped by launchd within milliseconds).
func waitGone(pid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return !alive(pid)
}

// F-07: Close killed only the direct child, so a tool process the worker had
// started (already past its PreToolUse gate) kept writing in the worktree.
func TestJSONProcessCloseEndsDescendants(t *testing.T) {
	dir := t.TempDir()
	marker, armed := filepath.Join(dir, "late-write"), filepath.Join(dir, "trap-armed")
	// The tool ignores SIGTERM, as a build that traps it would: only the
	// group SIGKILL after the grace ends it. It says so only once the trap is
	// set. The worker used to announce the tool as soon as it forked, so
	// Close's TERM usually landed before the trap and ended the tool on its
	// own; with the group SIGKILL deleted this test then still passed, and
	// caught the deletion in 3 of 20 runs (verification audit, 2026-10-07).
	script := fmt.Sprintf(`(trap '' TERM; : > %q; sleep 2; echo late > %q) </dev/null >/dev/null 2>&1 & echo "{\"tool_pid\":$!}"; exec sleep 300`, armed, marker)
	p, err := StartJSONProcess(context.Background(), testAuthorization(t), "/bin/sh", []string{"-c", script}, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		PID int `json:"tool_pid"`
	}
	readJSONLine(t, p, &v)
	t.Cleanup(func() { _ = syscall.Kill(v.PID, syscall.SIGKILL) })
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(armed); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("control: the descendant never set its TERM trap")
		}
	}
	// Positive control: the descendant exists, so "gone" below is a result.
	if !alive(v.PID) {
		t.Fatalf("control: descendant %d not running before Close", v.PID)
	}
	closed := make(chan struct{})
	go func() { _ = p.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(terminateGrace + 5*time.Second):
		t.Fatal("Close did not return")
	}
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("transport did not finish")
	}
	if !waitGone(v.PID, 2*time.Second) {
		_ = syscall.Kill(v.PID, syscall.SIGKILL)
		t.Fatalf("descendant %d of the closed worker survived Close", v.PID)
	}
	time.Sleep(2500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("descendant of the closed native worker wrote %s after Close", marker)
	}
}

// F-07: Close is TERM to the whole group, then the grace, then KILL. The TERM
// is what lets the worker and its tools finish their own writes and exit;
// without it every Close is a KILL two seconds later. Nothing pinned it: the
// verification's mutant deleting the group TERM survived every permanent
// test, because each one passes with KILL alone. The leader and a tool in its
// group each record the TERM they handle, and the leader announces only once
// both handlers are set, so Close cannot race them. The grace runs until the
// LEADER exits, so this leader, like a worker that lets its tools finish,
// waits for the tool before it exits. A TERM sent to the leader alone fails
// here too: the tool then never records one.
func TestJSONProcessCloseTermsTheGroupFirst(t *testing.T) {
	dir := t.TempDir()
	native := filepath.Join(dir, "native")
	program := `#!/usr/bin/env python3
import os, signal, sys
out = sys.argv[1]
def record(role):
    with open(os.path.join(out, role), "w") as f:
        f.write("TERM")
armed_r, armed_w = os.pipe()
tool = os.fork()
if tool == 0:
    def tool_term(sig, frame):
        record("tool")
        os._exit(0)
    signal.signal(signal.SIGTERM, tool_term)
    os.write(armed_w, b"x")
    while True:
        signal.pause()
def leader_term(sig, frame):
    record("leader")
    os.waitpid(tool, 0)
    os._exit(0)
signal.signal(signal.SIGTERM, leader_term)
os.read(armed_r, 1)
sys.stdout.write('{"tool": %d}\n' % tool)
sys.stdout.flush()
while True:
    signal.pause()
`
	if err := os.WriteFile(native, []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	p, err := StartJSONProcess(context.Background(), testAuthorization(t), native, []string{dir}, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Tool int `json:"tool"`
	}
	readJSONLine(t, p, &v)
	t.Cleanup(func() { _ = syscall.Kill(v.Tool, syscall.SIGKILL) })
	if !alive(v.Tool) {
		t.Fatalf("control: tool %d not running before Close", v.Tool)
	}
	_ = p.Close()
	for _, role := range []string{"leader", "tool"} {
		if got, err := os.ReadFile(filepath.Join(dir, role)); err != nil || string(got) != "TERM" {
			t.Errorf("Close ended the %s without a TERM it could handle: %q %v", role, got, err)
		}
	}
}

// F-07: a descendant holding native stdout kept the reader in Scan forever
// after Close, so done never closed and the child was never reaped. One holder
// stays in the group (the group kill releases it); the other leaves the group,
// which nothing here can kill, so Close must release our end of the pipe.
func TestJSONProcessCloseFinishesDespiteStdoutHolders(t *testing.T) {
	for _, holder := range []string{"in-group", "escaped"} {
		t.Run(holder, func(t *testing.T) {
			dir := t.TempDir()
			// The holder announces itself only after it has (or has not) left
			// the group, so Close cannot race its setpgid.
			leave := ""
			if holder == "escaped" {
				// setpgid(0,0) is what job control or a daemonizing server does.
				leave = "os.setpgid(0,0); "
			}
			start := `python3 -c 'import os,sys,time; ` + leave + `sys.stdout.write("{\"holder\":%d}\n" % os.getpid()); sys.stdout.flush(); time.sleep(30)' & exec sleep 300`
			p, err := StartJSONProcess(context.Background(), testAuthorization(t), "/bin/sh", []string{"-c", start}, dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			var v struct {
				Holder int `json:"holder"`
			}
			readJSONLine(t, p, &v)
			t.Cleanup(func() { _ = syscall.Kill(v.Holder, syscall.SIGKILL) })
			if !alive(v.Holder) {
				t.Fatalf("control: stdout holder %d not running", v.Holder)
			}
			_ = p.Close()
			select {
			case <-p.done:
			case <-time.After(5 * time.Second):
				t.Fatal("after Close, a descendant holding native stdout kept the transport reader (and the unreaped child) alive")
			}
			if holder == "in-group" && !waitGone(v.Holder, 2*time.Second) {
				t.Fatalf("in-group stdout holder %d survived the group kill", v.Holder)
			}
			// The documented limit, observed rather than assumed: a process
			// that left the group is not ours to signal and keeps running.
			if holder == "escaped" && !alive(v.Holder) {
				t.Fatalf("escaped holder %d is gone; the test no longer exercises the pipe release", v.Holder)
			}
		})
	}
}

// F-07: when the harness itself exits, its tool processes are orphans. One
// holding stdout used to keep the transport open (no EOF, no Events close)
// until a consumer happened to call Close.
func TestJSONProcessLeaderExitEndsGroup(t *testing.T) {
	p, err := StartJSONProcess(context.Background(), testAuthorization(t), "/bin/sh", []string{"-c", `sleep 30 & echo "{\"tool\":$!}"; exit 0`}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var v struct {
		Tool int `json:"tool"`
	}
	readJSONLine(t, p, &v)
	t.Cleanup(func() { _ = syscall.Kill(v.Tool, syscall.SIGKILL) })
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("leader exit left its tool process holding the transport open")
	}
	if !waitGone(v.Tool, 2*time.Second) {
		t.Fatalf("tool process %d outlived its exited harness", v.Tool)
	}
}

// F-08: one frame over the bound is never skipped (it could be the approval
// or result being waited for). It fails the transport with an error naming
// the bound, write admission closes, and the worker's whole group ends.
func TestJSONProcessOversizedFrameFailsVisiblyAndEndsTree(t *testing.T) {
	for _, size := range []int{nativeFrameLimit - 64, nativeFrameLimit + 1024} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			dir := t.TempDir()
			script := fmt.Sprintf(`sleep 60 & echo "{\"tool\":$!}"; python3 -c 'import sys; sys.stdout.write("{\"x\":\"" + "a"*%d + "\"}\n"); sys.stdout.flush()'; exec sleep 60`, size-10)
			p, err := StartJSONProcess(context.Background(), testAuthorization(t), "/bin/sh", []string{"-c", script}, dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			var v struct {
				Tool int `json:"tool"`
			}
			readJSONLine(t, p, &v)
			t.Cleanup(func() { _ = syscall.Kill(v.Tool, syscall.SIGKILL) })
			if size < nativeFrameLimit {
				// Positive control: a frame under the bound is delivered
				// whole and the transport stays writable.
				var big map[string]string
				readJSONLine(t, p, &big)
				if len(big["x"]) != size-10 {
					t.Fatalf("frame under the bound was altered: %d bytes", len(big["x"]))
				}
				if err := p.WriteContext(context.Background(), map[string]string{"still": "open"}); err != nil {
					t.Fatalf("frame under the bound closed the transport: %v", err)
				}
				return
			}
			select {
			case _, ok := <-p.Read():
				if ok {
					t.Fatal("an oversized frame was delivered or replaced")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("transport did not close on an oversized frame")
			}
			if err := p.Err(); err == nil || !strings.Contains(err.Error(), "transport bound") || !strings.Contains(err.Error(), "unknown") {
				t.Fatalf("oversized frame error does not name the bound and the uncertainty: %v", err)
			}
			if err := p.WriteContext(context.Background(), map[string]string{"after": "close"}); err == nil {
				t.Fatal("write accepted after an oversized frame")
			}
			select {
			case <-p.done:
			case <-time.After(5 * time.Second):
				t.Fatal("worker not reaped after an oversized frame")
			}
			if !waitGone(v.Tool, 2*time.Second) {
				t.Fatalf("worker's child %d survived the transport failure", v.Tool)
			}
		})
	}
}
