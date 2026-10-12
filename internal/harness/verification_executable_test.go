package harness

// V-08: Output waits for inherited stdout holders after its direct child has
// exited. A context deadline that kills only that child never bounds this wait.

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
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
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
				deadline := time.Now().Add(time.Second)
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
			var got result
			select {
			case got = <-finished:
			case <-time.After(2 * time.Second):
				_ = syscall.Kill(holder, syscall.SIGKILL)
				<-finished
				t.Fatal("version probe ignored its deadline while a descendant held stdout")
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
