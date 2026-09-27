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
	envHelper = "TESTGUARD_HELPER" // "sleep", "wedge" or "leaveholder"
	envDir    = "TESTGUARD_DIR"    // where the helper writes armed and mark
	// envHold makes this process a PIPE HOLDER (#53): it never arms, holds
	// whatever it inherited, and sleeps. It stands in for the child a -race
	// forkExec leaves stuck before exec: the same binary, the watchdog's pipe
	// still open in it, its parent gone.
	envHold = "TESTGUARD_HOLD"
	// envOtherGroup asks leaveholder for a second holder in ANOTHER process
	// group, which the watchdog must never touch.
	envOtherGroup = "TESTGUARD_OTHERGROUP"
	// envMore asks leaveholder for the Codex-pass cases (D-061): a SECOND
	// in-group holder, and two children seen by the watchdog that then exec
	// another binary or leave the group.
	envMore = "TESTGUARD_MORE"
)

func TestMain(m *testing.M) {
	if os.Getenv(envHold) == "1" {
		hold()
	}
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
	case "leaveholder":
		// The #53 shape: a child of this binary keeps the watchdog's pipe
		// open, and this process, the target, exits normally, as the timed-out
		// parent did. The pause lets the watchdog's poll see the children while
		// the target lives (the real stuck child existed for minutes).
		spawnHolder(t, dir, "group", false, true)
		if os.Getenv(envOtherGroup) == "1" {
			spawnHolder(t, dir, "other", true, true)
		}
		if os.Getenv(envMore) == "1" {
			spawnHolder(t, dir, "group2", false, true)
			// Seen by the watchdog as this binary, in this group — then one
			// execs another binary and one leaves the group, both before the
			// holder arm acts. Neither holds the pipe.
			spawnHolder(t, dir, "execlater", false, false)
			spawnHolder(t, dir, "movelater", false, false)
		}
		// A child that EXEC'D another binary (as every git child does), in the
		// same group, orphaned the same way: it must be left alone.
		execd := exec.Command("/bin/sleep", "120")
		if err := execd.Start(); err != nil {
			t.Fatal(err)
		}
		recordHolder(dir, "execd", execd.Process.Pid)
		time.Sleep(4 * pollEvery)
		os.Exit(0)
	case "wedgewithholder":
		// A wedged target that also left a holder: the arm that ends the
		// target must end the holder its death orphans.
		spawnHolder(t, dir, "group", false, true)
		time.Sleep(2 * pollEvery)
		go spinForever()
		time.Sleep(200 * time.Millisecond)
		runtime.GC()
	}
	t.Fatalf("helper %q returned", mode)
}

// hold is a pipe holder's whole life: say where it is, then hold. It never
// arms, so no guard of its own ends it when its parent exits. execlater and
// movelater first wait to be seen, then exec /bin/sleep or leave the group.
func hold() {
	kind := os.Getenv(envHold + "_KIND")
	recordHolder(os.Getenv(envDir), kind, os.Getpid())
	switch kind {
	case "execlater":
		time.Sleep(2*pollEvery + pollEvery/2)
		syscall.Exec("/bin/sleep", []string{"sleep", "120"}, os.Environ())
	case "movelater":
		time.Sleep(2*pollEvery + pollEvery/2)
		syscall.Setpgid(0, 0)
	}
	for {
		time.Sleep(time.Hour)
	}
}

// recordHolder writes "pid born", so a leg can kill exactly that process at
// cleanup whatever state the run ends in.
func recordHolder(dir, kind string, pid int) {
	_, born, _ := procOf(pid)
	os.WriteFile(filepath.Join(dir, "holder-"+kind), []byte(strconv.Itoa(pid)+" "+strconv.FormatInt(born, 10)), 0o644)
}

// spawnHolder starts a child of this binary that never arms; with pipe it
// inherits the watchdog pipe's write end (fd 3), and with other it runs in a
// process group of its own.
func spawnHolder(t *testing.T, dir, kind string, other, pipe bool) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), envHold+"=1", envHold+"_KIND="+kind)
	if pipe {
		cmd.ExtraFiles = []*os.File{pipeW}
	}
	if other {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		if _, err := os.Stat(filepath.Join(dir, "holder-"+kind)); err == nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("holder %s never started", kind)
}

// killHoldersAtCleanup is registered FIRST in a holder leg, before anything
// can fail: at cleanup it kills every holder the run recorded, by pid AND
// start time, so a red leg (or a mutant, or a target that failed half-way)
// never leaks one — the holders deliberately have no guard of their own.
func killHoldersAtCleanup(t *testing.T, dir string) {
	t.Cleanup(func() {
		files, _ := filepath.Glob(filepath.Join(dir, "holder-*"))
		for _, f := range files {
			b, _ := os.ReadFile(f)
			fs := strings.Fields(string(b))
			if len(fs) != 2 {
				continue
			}
			pid, _ := strconv.Atoi(fs[0])
			born, _ := strconv.ParseInt(fs[1], 10, 64)
			if _, b, ok := procOf(pid); ok && b == born {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
}

// holderPID reads a recorded holder's pid.
func holderPID(t *testing.T, dir, kind string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "holder-"+kind))
	fs := strings.Fields(string(b))
	if err != nil || len(fs) != 2 {
		t.Fatalf("no holder %s: %v", kind, err)
	}
	pid, _ := strconv.Atoi(fs[0])
	return pid
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

// #53: a -race forkExec can leave the child stuck BEFORE exec (ThreadSanitizer's
// slot lock, held by a parent thread at fork). Close-on-exec only closes at exec,
// so that child keeps the watchdog's pipe open; the parent times out and EXITS.
// The watchdog saw no EOF, could not identify its gone target, sent nothing, and
// left the child spinning for 44 minutes. Now: target gone and pipe still open
// means a child of the target holds it, and the watchdog samples and ends the
// orphaned same-binary children it saw while the target lived — in its own
// process group only. TWO in-group holders, behind a DEAD stderr, must both go:
// a report written after the first killed the watchdog before the second (Codex,
// D-061). The negatives beside them, all untouched: a holder in ANOTHER group,
// an orphaned child that exec'd /bin/sleep, and two children the watchdog SAW
// as its binary in its group that then exec'd or left the group.
func TestWatchdogEndsTheChildAnExitedTargetLeftHoldingItsPipe(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the watchdog reads another process's parent on darwin only")
	}
	t.Parallel()
	cmd, dir := helper(t, "leaveholder", "10m", envOtherGroup+"=1", envMore+"=1")
	killHoldersAtCleanup(t, dir)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = w
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the target should exit 0 by itself: %v", err)
	}
	r.Close() // the reader of the watchdog's stderr is gone, as go test's is
	for _, kind := range []string{"group", "group2"} {
		pid := holderPID(t, dir, kind)
		if _, gone := goneWithin(pid, 45*time.Second); !gone {
			t.Fatalf("the orphaned child %s (%d) holding the pipe is still running 45s after its target exited", kind, pid)
		}
		samples, _ := filepath.Glob(filepath.Join(dir, "buddy-testguard-"+strconv.Itoa(pid)+"-*.sample.txt"))
		if len(samples) != 1 {
			t.Errorf("want one sample of the holder %s (%d), got %v", kind, pid, samples)
		}
	}
	time.Sleep(3 * pollEvery)
	for _, kind := range []string{"other", "execd", "execlater", "movelater"} {
		if pid := holderPID(t, dir, kind); !pidAlive(pid) {
			t.Errorf("%s (%d) was touched", kind, pid)
		}
	}
}

// The wall and orphan arms end the TARGET; its death orphans a child still
// holding the pipe, and that child must be ended too, not left because the arm
// returned (Codex, D-061).
func TestWatchdogEndsTheHolderOfATargetItEnded(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the watchdog reads another process's parent on darwin only")
	}
	t.Parallel()
	cmd, dir := helper(t, "wedgewithholder", "10m", "GODEBUG=asyncpreemptoff=1")
	killHoldersAtCleanup(t, dir)
	parent := underThrowawayParent(t, cmd)
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	pid, _ := waitArmed(t, dir)
	time.Sleep(2*pollEvery + time.Second) // the holder seen, the target wedged
	parent.Process.Kill()
	parent.Wait()
	if _, gone := goneWithin(pid, 45*time.Second); !gone {
		t.Fatalf("wedged orphan %d still running 45s after its parent was killed", pid)
	}
	holder := holderPID(t, dir, "group")
	if _, gone := goneWithin(holder, 30*time.Second); !gone {
		t.Fatalf("the holder %d its ended target left is still running", holder)
	}
}

// The selection rule, on a fake table: only an ORPHANED process the watchdog saw
// as its target's same-binary child, with the same start time, still that
// binary, still in the group, and never the watchdog itself. On darwin a child
// always reparents to launchd when its parent dies (no subreaper), so a "not
// orphaned" candidate cannot be built with real processes; this table is where
// that guard is exercised.
func TestPickHolders(t *testing.T) {
	const self, exe, pg = 50, "/t/x.test", 7
	seen := []kid{{10, 1}, {11, 1}, {12, 1}, {13, 1}, {14, 1}, {15, 1}, {self, 1}}
	type row struct {
		ppid int
		born int64
		exe  string
		pgid int
	}
	now := map[int]row{
		10:   {1, 1, exe, pg},        // orphaned, same start time: the stuck child
		11:   {77, 1, exe, pg},       // a live parent: not orphaned, untouched
		12:   {1, 9, exe, pg},        // pid reused by another process: untouched
		14:   {1, 1, "/bin/git", pg}, // exec'd since it was seen: untouched
		15:   {1, 1, exe, 99},        // left the group since it was seen: untouched
		self: {1, 1, exe, pg},        // the watchdog itself: never
		// 13 has exited: nothing to do
	}
	look := func(pid int) (int, int64, string, int, bool) {
		r, ok := now[pid]
		return r.ppid, r.born, r.exe, r.pgid, ok
	}
	got := pickHolders(seen, holderScope{exe: exe, pgid: pg, self: self}, look)
	if len(got) != 1 || got[0].pid != 10 {
		t.Fatalf("want only pid 10, got %+v", got)
	}
}

// remember keeps a cap on LIVE children, not a lifetime count: after 64 have
// come and gone, a new one is still recorded (Codex, D-061).
func TestRememberDropsTheGoneBeforeTheCap(t *testing.T) {
	var known []kid
	for i := 0; i < maxKids; i++ {
		known = append(known, kid{pid: 1000 + i, born: 1})
	}
	look := func(pid int) (int, int64, bool) { return 1, 1, pid == 5000 } // only the new one lives
	got := remember(known, []kid{{5000, 1}}, look)
	if len(got) != 1 || got[0].pid != 5000 {
		t.Fatalf("want the one live child recorded, got %d entries (%+v…)", len(got), got[:min(3, len(got))])
	}
}

// Symbolization outlives go test's cleanup (#53): the hand sample of the real
// orphan could not be read, because go test had deleted the binary. The helper
// runs from a COPY that is deleted once it has armed; the watchdog's sample must
// still name the spinning frame, from the executable it opened at start.
func TestWatchdogSymbolizesAfterTheBinaryIsDeleted(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sample and symbolize are darwin only")
	}
	t.Parallel()
	cmd, dir := helper(t, "wedge", "10m", "GODEBUG=asyncpreemptoff=1")
	copied := filepath.Join(t.TempDir(), "copy.test")
	b, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copied, b, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd.Path, cmd.Args[0] = copied, copied
	parent := underThrowawayParent(t, cmd)
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	pid, _ := waitArmed(t, dir)
	if err := os.Remove(copied); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond) // into the wedge
	parent.Process.Kill()
	parent.Wait()
	if _, gone := goneWithin(pid, 45*time.Second); !gone {
		t.Fatalf("wedged orphan %d still running 45s after its parent was killed", pid)
	}
	assertSampleNamesSpin(t, dir)
}
