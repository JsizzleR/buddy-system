package harness

// V-08: Output waits for inherited stdout holders after its direct child has
// exited. A context deadline that kills only that child never bounds this wait.
//
// The probe here is CANCELLED by the test after the hazard is observed, never
// given a deadline. These cases used a 200 ms WithTimeout, and the control
// raced it under load (2026-10-26, hermetic tier at load average 12-14): the
// shell child has to start, fork `sleep` and write holder.pid before that
// deadline kills it, and on a loaded box it lost, so the control failed with
// "did not start its stdout holder" and the hazard was never set up. Now the
// holder is observed FIRST and the cancel follows the observation, so no
// scheduling delay sits between the two. What each case proves is unchanged:
// leader-exit, the direct child exits while a descendant still holds stdout,
// and the probe must return the child's output rather than wait for the holder
// (a probe that waited would take 30 s; the 2 s select is the bound); cancelled,
// the direct child is blocked behind that holder and a cancellation must end
// the probe and the holder. REFUTED: a longer deadline, since any fixed one is
// a bet against the scheduler and 200 ms was already ten times the unloaded
// startup; and writing the marker before the fork, since the slow step is the
// shell's own startup, not its fork.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestNativeVersionProbeBoundsInheritedOutput(t *testing.T) {
	for _, mode := range []string{"positive", "leader-exit", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			path, marker := filepath.Join(dir, "native"), filepath.Join(dir, "holder.pid")
			script := "#!/bin/sh\nprintf 'codex-cli 0.160.0\\n'\n"
			if mode != "positive" {
				script = fmt.Sprintf("#!/bin/sh\nsleep 30 &\necho $! > %q\n", marker)
				if mode == "cancelled" {
					script += "wait\n"
				} else {
					script += "printf 'codex-cli 0.160.0\\n'\nexit 0\n"
				}
			}
			if err := os.WriteFile(path, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type result struct {
				version string
				err     error
			}
			finished := make(chan result, 1)
			go func() {
				version, err := newCodex(path, nil).version(ctx, path)
				finished <- result{version, err}
			}()
			var holder int
			if mode != "positive" {
				// The control: the holder exists before anything is asserted
				// about the probe. Generous on purpose; it is plumbing, and
				// nothing the probe does can shorten it now.
				deadline := time.Now().Add(5 * time.Second)
				for holder == 0 && time.Now().Before(deadline) {
					data, _ := os.ReadFile(marker)
					holder, _ = strconv.Atoi(strings.TrimSpace(string(data)))
					time.Sleep(10 * time.Millisecond)
				}
				if holder <= 1 {
					t.Fatal("control: version probe did not start its stdout holder")
				}
				t.Cleanup(func() { _ = syscall.Kill(holder, syscall.SIGKILL) })
			}
			if mode == "cancelled" {
				// The hazard is in place: the holder is alive and the direct
				// child is blocked in `wait` behind it. Cancel now.
				cancel()
			}
			var got result
			select {
			case got = <-finished:
			case <-time.After(2 * time.Second):
				_ = syscall.Kill(holder, syscall.SIGKILL)
				<-finished
				t.Fatal("version probe waited on a descendant holding stdout instead of finishing")
			}
			if mode == "cancelled" {
				if got.err == nil {
					t.Fatal("cancelled version probe reported success")
				}
			} else if got.err != nil || got.version != "codex-cli 0.160.0" {
				t.Fatalf("version control: %q %v", got.version, got.err)
			}
			if holder > 0 && !waitGone(holder, time.Second) {
				t.Fatal("version probe orphaned its stdout holder")
			}
		})
	}
}
