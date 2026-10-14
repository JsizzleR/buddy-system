package main

// A-02 (audit, 2026-10-07): SIGKILL the adapter while its Codex worker is
// mid-turn. The worker and its tool stay alive: they are in their own process
// group, and the adapter's death closes only their stdin. Then `--resume` took
// the now-free journal flock, passed every check, and launched a SECOND
// app-server that received thread/resume while the first was still running.
// Neither the receipt nor the journal recorded a native pid, so nothing could
// have refused. Measured against the tree before the fix (resume-exp.log):
// "resume admitted and launched a second native app-server (52349) while the
// first (51299) was alive".
//
// These run the BUILT adapter, as the fixture in main_test.go does, so the
// refusal is the binary's, not a helper's.

import (
	"strings"
	"syscall"
	"testing"
	"time"
)

// resumableFake is the Codex fake with a thread/resume branch, which the
// start-only fake lacks: without it a resumed worker would wedge on the request.
func resumableFake(t *testing.T, f *entryFixture) {
	t.Helper()
	fake := strings.Replace(fakeCodex, "    elif method == 'thread/read':",
		"    elif method == 'thread/resume':\n        record(kind='resume', pid=os.getpid(), thread=m['params'].get('threadId'))\n        emit({'id': m['id'], 'result': {'thread': {'id': m['params']['threadId'], 'status': {'type': 'idle'}}}})\n    elif method == 'thread/read':", 1)
	if fake == fakeCodex {
		t.Fatal("fixture patch did not apply")
	}
	f.writeCodex(t, fake)
}

// firstWorker returns the pid of the fake app-server and of its tool.
func firstWorker(t *testing.T, f *entryFixture) (int, int) {
	t.Helper()
	var first map[string]any
	for _, row := range nativeLog(t, f.marker) {
		if row["kind"] == "app-server" {
			first = row
		}
	}
	if first == nil {
		t.Fatal("native fake did not start")
	}
	return int(first["pid"].(float64)), int(first["tool"].(float64))
}

// secondWorker reports whether a second app-server (not pid worker) started
// and whether any thread/resume reached a fake.
func secondWorker(t *testing.T, f *entryFixture, worker int) (second map[string]any, resumed bool) {
	t.Helper()
	for _, row := range nativeLog(t, f.marker) {
		if row["kind"] == "app-server" && int(row["pid"].(float64)) != worker {
			second = row
		}
		if row["kind"] == "resume" {
			resumed = true
		}
	}
	return second, resumed
}

// killWorkerTree ends the recorded worker's group by hand, as the operator
// would after checking: the fake is mid-turn and never exits on stdin EOF.
func killWorkerTree(t *testing.T, worker, tool int) {
	t.Helper()
	_ = syscall.Kill(-worker, syscall.SIGKILL)
	_ = syscall.Kill(tool, syscall.SIGKILL)
	_ = syscall.Kill(worker, syscall.SIGKILL)
	if !pidGone(worker, 5*time.Second) || !pidGone(tool, 5*time.Second) {
		t.Fatalf("control: could not end the first worker %d / tool %d", worker, tool)
	}
}

func sigkillAdapterMidTurn(t *testing.T, f *entryFixture) (worker, tool int) {
	t.Helper()
	a := f.start(t, nil, nil)
	a.await(t, "start receipt", func(v map[string]any) bool { return v["operation"] == "start" })
	worker, tool = firstWorker(t, f)
	t.Cleanup(func() {
		_ = syscall.Kill(-worker, syscall.SIGKILL)
		_ = syscall.Kill(tool, syscall.SIGKILL)
		_ = syscall.Kill(worker, syscall.SIGKILL)
	})
	// The fake is mid-turn: blocked before reading stdin again (never released).
	if err := a.cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = a.waitExit(t, 10*time.Second)
	time.Sleep(300 * time.Millisecond)
	if syscall.Kill(worker, 0) != nil || syscall.Kill(tool, 0) != nil {
		t.Fatalf("control: after the adapter's SIGKILL the worker %d or its tool %d is not alive; the experiment no longer models A-02", worker, tool)
	}
	return worker, tool
}

func TestAdapterResumeRefusesWhileRecordedWorkerLives(t *testing.T) {
	f := newEntryFixture(t, "")
	resumableFake(t, f)
	worker, tool := sigkillAdapterMidTurn(t, f)

	b := f.start(t, nil, []string{"--resume"})
	got := b.await(t, "resume receipt", func(v map[string]any) bool { return v["operation"] == "resume" })
	second, resumed := secondWorker(t, f, worker)
	if second != nil {
		p2, t2 := int(second["pid"].(float64)), int(second["tool"].(float64))
		t.Cleanup(func() { _ = syscall.Kill(t2, syscall.SIGKILL); _ = syscall.Kill(p2, syscall.SIGKILL) })
	}
	if got["error"] == nil || second != nil || resumed {
		t.Fatalf("A-02: resume admitted while the recorded worker %d lives: error=%v second=%v thread/resume=%v", worker, got["error"], second, resumed)
	}
	reason, _ := got["error"].(string)
	if !strings.Contains(reason, "leader") || !strings.Contains(reason, "--wait-prior") {
		t.Fatalf("refusal does not name the live leader and the wait option: %q", reason)
	}
	_ = b.stdin.Close()
	if err := b.waitExit(t, 10*time.Second); err == nil {
		t.Fatal("refused resume exited 0")
	}
	if syscall.Kill(worker, 0) != nil || syscall.Kill(tool, 0) != nil {
		t.Fatalf("the refusal signalled the prior worker %d or its tool %d", worker, tool)
	}

	// Positive control: once the operator has ended the recorded worker's
	// group, the same receipt resumes, and exactly one new app-server gets
	// thread/resume.
	killWorkerTree(t, worker, tool)
	c := f.start(t, nil, []string{"--resume"})
	got = c.await(t, "resume receipt", func(v map[string]any) bool { return v["operation"] == "resume" })
	second, resumed = secondWorker(t, f, worker)
	if second != nil {
		p2, t2 := int(second["pid"].(float64)), int(second["tool"].(float64))
		t.Cleanup(func() { _ = syscall.Kill(t2, syscall.SIGKILL); _ = syscall.Kill(p2, syscall.SIGKILL) })
	}
	if got["error"] != nil || second == nil || !resumed {
		t.Fatalf("control: resume refused after the prior worker was gone: error=%v second=%v thread/resume=%v; stderr: %s", got["error"], second, resumed, c.stderr)
	}
	_ = c.stdin.Close()
	_ = c.waitExit(t, 10*time.Second)
}

// --wait-prior is passive: the resume waits, signalling nothing, until the
// recorded worker is gone, then admits; it refuses at its bound otherwise.
func TestAdapterResumeWaitPrior(t *testing.T) {
	for _, outcome := range []string{"admits-once-gone", "refuses-at-bound"} {
		t.Run(outcome, func(t *testing.T) {
			f := newEntryFixture(t, "")
			resumableFake(t, f)
			worker, tool := sigkillAdapterMidTurn(t, f)
			bound := "20s"
			if outcome == "refuses-at-bound" {
				bound = "1500ms"
			}
			b := f.start(t, nil, []string{"--resume", "--wait-prior", bound})
			// While the worker lives the adapter waits: no receipt, no signal.
			select {
			case r, ok := <-b.receipts:
				t.Fatalf("resume did not wait for the live worker: receipt=%v open=%v", r, ok)
			case <-time.After(time.Second):
			}
			if syscall.Kill(worker, 0) != nil || syscall.Kill(tool, 0) != nil {
				t.Fatalf("the wait signalled the prior worker %d or its tool %d", worker, tool)
			}
			if outcome == "refuses-at-bound" {
				// The bound refuses before a controller exists, so, like every
				// pre-controller refusal, it is reported on stderr with exit 1
				// and no receipt; the worker is untouched.
				if err := b.waitExit(t, 10*time.Second); err == nil {
					t.Fatal("resume exited 0 at its --wait-prior bound")
				}
				if second, resumed := secondWorker(t, f, worker); second != nil || resumed {
					t.Fatalf("wait launched a worker at its bound: %v %v", second, resumed)
				}
				if msg := b.stderr.String(); !strings.Contains(msg, "waited") || !strings.Contains(msg, "leader is still running") {
					t.Fatalf("timeout refusal does not say it waited for the live leader: %s", msg)
				}
				if syscall.Kill(worker, 0) != nil || syscall.Kill(tool, 0) != nil {
					t.Fatalf("the refusal signalled the prior worker %d or its tool %d", worker, tool)
				}
				return
			}
			killWorkerTree(t, worker, tool)
			got := b.await(t, "resume receipt", func(v map[string]any) bool { return v["operation"] == "resume" })
			second, resumed := secondWorker(t, f, worker)
			if second != nil {
				p2, t2 := int(second["pid"].(float64)), int(second["tool"].(float64))
				t.Cleanup(func() { _ = syscall.Kill(t2, syscall.SIGKILL); _ = syscall.Kill(p2, syscall.SIGKILL) })
			}
			if got["error"] != nil || second == nil || !resumed {
				t.Fatalf("wait did not admit once the worker was gone: error=%v second=%v; stderr: %s", got["error"], second, b.stderr)
			}
			_ = b.stdin.Close()
			_ = b.waitExit(t, 10*time.Second)
		})
	}
}

// The bound is 10 minutes and the flag belongs to --resume. The expected text
// is the validation's own sentence: the fixture's temp path carries the
// subtest name, so merely finding "wait-prior" in stderr proved nothing (a
// mutant raising the cap a hundredfold survived that assertion).
func TestAdapterWaitPriorFlagBounds(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--resume", "--wait-prior", "11m"}, "--wait-prior must be between 0 and 10m0s"},
		{[]string{"--resume", "--wait-prior", "-1s"}, "--wait-prior must be between 0 and 10m0s"},
		{[]string{"--wait-prior", "1s"}, "--wait-prior applies only with --resume"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			f := newEntryFixture(t, "")
			a := f.start(t, nil, tc.args)
			a.stdin.Close()
			if err := a.waitExit(t, 20*time.Second); err == nil {
				t.Fatalf("adapter accepted %v", tc.args)
			}
			if rows := nativeLog(t, f.marker); len(rows) != 0 {
				t.Fatalf("a native child ran before the flag was refused: %v", rows)
			}
			if !strings.Contains(a.stderr.String(), tc.want) {
				t.Fatalf("refusal does not say %q: %s", tc.want, a.stderr)
			}
		})
	}
}
