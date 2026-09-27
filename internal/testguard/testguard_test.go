package testguard

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Every leg runs THIS test binary as a helper (TestHelperProcess, selected by
// envHelper) so the guard under test is the real Arm, in a real process, with
// a real parent that the leg can kill. The helper records what it needs the
// leg to see in files, because an orphan's exit status reaches nobody and its
// stderr is a dead pipe.
const (
	envHelper = "TESTGUARD_HELPER" // "sleep" or "wedge"
	envDir    = "TESTGUARD_DIR"    // where the helper writes armed and mark
)

func TestMain(m *testing.M) {
	if dir := os.Getenv(envDir); dir != "" {
		// Why did the helper exit? Only the in-process guard goes through
		// exit, so a mark says it fired, and its absence says it did not.
		exit = func(code int) {
			os.WriteFile(filepath.Join(dir, "mark"), []byte(strconv.Itoa(code)), 0o644)
			os.Exit(code)
		}
	}
	Arm()
	ScrubGitEnv() // gitenv_test.go runs git (#49)
	os.Exit(m.Run())
}

// spinForever is a loop with no call in it. Under GODEBUG=asyncpreemptoff=1
// nothing can preempt it, so the next stop-the-world waits on it forever and
// every other goroutine, timers included, stops: the #43 wedge. Its name is
// what a leg looks for in the sample and in the SIGQUIT dump.
//
//go:noinline
func spinForever() {
	x := 0
	for {
		x++
	}
}

func TestHelperProcess(t *testing.T) {
	mode := os.Getenv(envHelper)
	if mode == "" {
		t.Skip("helper for the legs below; runs only when a leg execs it")
	}
	dir := os.Getenv(envDir)
	// Two GCs and a pause let the finalizers run, so a watchdog pipe that is
	// not kept reachable would already have been closed by the time a leg
	// checks the watchdog is still there.
	runtime.GC()
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	armed := strconv.Itoa(os.Getpid()) + " " + strconv.Itoa(watchdogPID)
	if err := os.WriteFile(filepath.Join(dir, "armed"), []byte(armed), 0o644); err != nil {
		t.Fatal(err)
	}
	switch mode {
	case "sleep":
		time.Sleep(time.Hour)
	case "wedge":
		go spinForever()
		time.Sleep(200 * time.Millisecond)
		runtime.GC()
	}
	t.Fatalf("helper %q returned", mode)
}

// helper builds the command that runs the helper in mode with -test.timeout.
func helper(t *testing.T, mode, timeout string, env ...string) (*exec.Cmd, string) {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$", "-test.timeout="+timeout)
	cmd.Env = append(os.Environ(), envHelper+"="+mode, envDir+"="+dir, "TMPDIR="+dir)
	cmd.Env = append(cmd.Env, env...)
	return cmd, dir
}

// underThrowawayParent wraps cmd in an sh that exists only to be killed, so
// the helper can be orphaned the way #43's was. The sh is this test's own
// child, so killing it at cleanup is always safe; a helper that never armed is
// then an orphan its own guard ends.
func underThrowawayParent(t *testing.T, cmd *exec.Cmd) *exec.Cmd {
	sh := exec.Command("/bin/sh", append([]string{"-c", `"$@" & wait`, "sh"}, cmd.Args...)...)
	sh.Env = cmd.Env
	t.Cleanup(func() {
		if sh.Process != nil {
			sh.Process.Kill()
			sh.Wait()
		}
	})
	return sh
}

// waitArmed returns the helper's pid and its watchdog's, once it has armed.
func waitArmed(t *testing.T, dir string) (pid, dog int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(filepath.Join(dir, "armed"))
		if f := strings.Fields(string(b)); err == nil && len(f) == 2 {
			pid, _ = strconv.Atoi(f[0])
			dog, _ = strconv.Atoi(f[1])
			// The helper's own kill switch, in case a leg (or a mutant of
			// the guard) leaves it running: this suite must not leak the
			// thing it tests for. It kills only the process it identified
			// here — pid AND start time — never whatever holds the pid by
			// cleanup time.
			if _, born, ok := procOf(pid); ok {
				t.Cleanup(func() {
					if _, b, ok := procOf(pid); ok && b == born {
						syscall.Kill(pid, syscall.SIGKILL)
					}
				})
			}
			return pid, dog
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("helper never armed")
	return 0, 0
}

func pidAlive(pid int) bool {
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

// goneWithin polls until pid no longer exists and reports how long that took.
func goneWithin(pid int, limit time.Duration) (time.Duration, bool) {
	start := time.Now()
	for time.Since(start) < limit {
		if !pidAlive(pid) {
			return time.Since(start), true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return limit, false
}

// The in-process guard: an orphan whose scheduler is healthy exits by itself,
// quickly, with the guard's code. Mutant: drop `go pollParent(ppid)` from Arm
// — the helper then lives until the watchdog's orphan arm (seconds later, and
// with no mark), so this leg goes red.
func TestOrphanExitsWithinFiveSeconds(t *testing.T) {
	t.Parallel()
	cmd, dir := helper(t, "sleep", "1h")
	parent := underThrowawayParent(t, cmd)
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	pid, dog := waitArmed(t, dir)

	// Positive control: armed, with a live parent, several polls go by and
	// nothing fires — and the watchdog is still there after the helper's GCs
	// (an unreachable pipe would have been finalized, EOF, and retired it).
	time.Sleep(3 * pollEvery)
	if !pidAlive(pid) {
		t.Fatal("the guard ended a helper whose parent is alive")
	}
	if dog == 0 || !pidAlive(dog) {
		t.Fatalf("watchdog %d is not running under a live helper", dog)
	}

	parent.Process.Kill()
	parent.Wait()
	took, gone := goneWithin(pid, 5*time.Second)
	if !gone {
		t.Fatalf("orphaned helper %d still running 5s after its parent was killed", pid)
	}
	mark, _ := os.ReadFile(filepath.Join(dir, "mark"))
	if string(mark) != strconv.Itoa(exitOrphaned) {
		t.Fatalf("helper ended after %s but not by the orphan guard: mark %q", took, mark)
	}
	if _, gone := goneWithin(dog, 5*time.Second); !gone {
		t.Fatalf("watchdog %d outlived its target", dog)
	}
	t.Logf("orphan exited %s after its parent was killed", took.Round(10*time.Millisecond))
}

// The watchdog's orphan arm: a WEDGED orphan cannot run the in-process guard,
// so the watchdog samples it and ends it. The missing mark is the control that
// the wedge was real — a healthy helper would have exited through the guard.
// Mutant: drop the orphan arm from watchdog — the helper spins until the
// cleanup kills it, and this leg goes red.
func TestWatchdogEndsAWedgedOrphan(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the watchdog reads another process's parent on darwin only")
	}
	t.Parallel()
	cmd, dir := helper(t, "wedge", "10m", "GODEBUG=asyncpreemptoff=1")
	parent := underThrowawayParent(t, cmd)
	// #43's shape exactly: stdout and stderr are a pipe to the parent's
	// reader, and the reader dies with it. /dev/null here would hide what a
	// dead stderr does to anything that writes to it — the watchdog included.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	parent.Stdout, parent.Stderr = w, w
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	w.Close()
	pid, _ := waitArmed(t, dir)
	time.Sleep(500 * time.Millisecond) // into the wedge

	parent.Process.Kill()
	parent.Wait()
	r.Close()
	took, gone := goneWithin(pid, 45*time.Second)
	if !gone {
		t.Fatalf("wedged orphan %d still running 45s after its parent was killed", pid)
	}
	if mark, err := os.ReadFile(filepath.Join(dir, "mark")); err == nil {
		t.Fatalf("the in-process guard ran (mark %q), so the helper was not wedged and this leg proved nothing", mark)
	}
	assertSampleNamesSpin(t, dir)
	t.Logf("wedged orphan ended %s after its parent was killed", took.Round(10*time.Millisecond))
}

// The watchdog's wall: a wedge under a LIVE parent — not an orphan, so only
// the wall can end it — is SIGQUIT at twice its timeout (10 s, not less: on a
// loaded box startup alone can outlast a short one, and testing's alarm would
// then fire before the wedge and fail the control), and the dump names
// the spinning goroutine. testing's own alarm never fires in the wedge; that
// it did not is the control. Mutant: drop the wall arm — the helper spins
// until this leg's deadline, and it goes red.
func TestWatchdogWallEndsAWedgeUnderALiveParent(t *testing.T) {
	t.Parallel()
	cmd, dir := helper(t, "wedge", "10s", "GODEBUG=asyncpreemptoff=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(90 * time.Second):
		cmd.Process.Kill()
		<-done
		t.Fatalf("wedged helper still running 90s in, against a 20s wall:\n%s", stderr.String())
	}
	out := stderr.String()
	for _, want := range []string{"twice its -test.timeout", "SIGQUIT: quit"} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr lacks %q:\n%.3000s", want, out)
		}
	}
	if strings.Contains(out, "test timed out") {
		t.Errorf("testing's own alarm fired, so the helper was not wedged:\n%.3000s", out)
	}
	if runtime.GOOS == "darwin" {
		assertSampleNamesSpin(t, dir)
	}
}

// assertSampleNamesSpin wants exactly one sample in dir, with the spinning
// frame NAMED: `go test` binaries carry no symbol table, so a sample that
// symbolize did not annotate shows only `???` there.
func assertSampleNamesSpin(t *testing.T, dir string) {
	t.Helper()
	samples, _ := filepath.Glob(filepath.Join(dir, "buddy-testguard-*.sample.txt"))
	if len(samples) != 1 {
		t.Fatalf("want one sample in %s, got %v", dir, samples)
	}
	if b, _ := os.ReadFile(samples[0]); !bytes.Contains(b, []byte("testguard.spinForever testguard_test.go:")) {
		t.Fatalf("the sample does not name the spinning frame:\n%.3000s", b)
	}
}
