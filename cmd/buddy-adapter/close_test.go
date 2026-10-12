package main

// A-02 narrow contract (D-077, 2026-10-11): the adapter ends only its
// worker's process group. A tool the worker put in a session of its own, as
// Claude Code does for every Bash tool shell (V-12), is not the adapter's to
// signal; it is recorded and reported on stderr as still running.

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAdapterReportsEscapedToolAtClose(t *testing.T) {
	f := newEntryFixture(t, "")
	fake := strings.Replace(fakeCodex, "tool = subprocess.Popen(['sleep', '300'])", "tool = subprocess.Popen(['sleep', '300'], start_new_session=True)", 1)
	if fake == fakeCodex {
		t.Fatal("fixture patch did not apply")
	}
	if err := os.WriteFile(f.codex, []byte(fake), 0700); err != nil {
		t.Fatal(err)
	}
	a := f.start(t, nil, nil)
	a.await(t, "start receipt", func(v map[string]any) bool { return v["operation"] == "start" })
	worker, tool := firstWorker(t, f)
	t.Cleanup(func() {
		_ = syscall.Kill(tool, syscall.SIGKILL)
		_ = syscall.Kill(worker, syscall.SIGKILL)
	})
	toolGroup, err1 := syscall.Getpgid(tool)
	workerGroup, err2 := syscall.Getpgid(worker)
	if err1 != nil || err2 != nil || toolGroup == workerGroup {
		t.Fatalf("control: the fixture's tool %d did not leave the worker's group (%d, %d; %v %v)", tool, toolGroup, workerGroup, err1, err2)
	}
	_ = a.stdin.Close()
	_ = a.waitExit(t, 15*time.Second)
	msg := a.stderr.String()
	for _, want := range []string{"worker closed (close): 1 escaped descendant(s) captured", fmt.Sprintf("1 still running (pid %d;", tool), "not signalled", "descendants never observed: unknown"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("stderr does not say %q: %s", want, msg)
		}
	}
	if syscall.Kill(tool, 0) != nil {
		t.Fatalf("the escaped tool %d was signalled", tool)
	}
	if !pidGone(worker, 5*time.Second) {
		t.Fatalf("the worker %d outlived the close", worker)
	}
	b, err := os.ReadFile(f.receipt + ".proc")
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		Close struct {
			Survivors []struct {
				PID int `json:"pid"`
			} `json:"survivors"`
		} `json:"close"`
	}
	if err := json.Unmarshal(b, &rec); err != nil || len(rec.Close.Survivors) != 1 || rec.Close.Survivors[0].PID != tool {
		t.Fatalf("the ownership record does not name the survivor %d: %v %s", tool, err, b)
	}
}
