// Package testguard bounds a test binary that outlives its `go test` (issue
// #43, D-053). TestMain calls Arm first; nothing else here is API.
//
// WHAT HAPPENED. An internal/cli test binary was found reparented to launchd,
// spinning 70-86% of a core for 44 minutes against its own
// -test.timeout=10m, twice in one day on a loaded box. Nobody noticed until
// somebody read `ps`, and its load slowed every other session's timing-
// sensitive tests.
//
// WHAT WAS MEASURED (2026-09-27, go1.26.4 darwin/arm64), because the issue's
// leading candidate turned out to be wrong:
//
//   - An ORDINARY orphan dies by itself. Killing `go test` mid-run leaves
//     cli.test with fd 1 and 2 on a pipe nobody reads; its first write through
//     os.File hits EPIPE, and os's epipecheck kills it with SIGPIPE — about 40 s
//     after orphaning, in the run measured.
//   - The -test.timeout panic does NOT hang on a dead stderr. The runtime
//     prints a fatal panic through its raw writer and discards the error (on
//     darwin the kernel's SIGPIPE is dropped by sigfwdgo), then exit(2).
//     Measured: an orphaned test blocked in Sleep, and one in a preemptible
//     spin, each exited at its 5 s timeout with a dead stderr.
//   - So 44 minutes past the timeout means the alarm's goroutine never RAN:
//     the scheduler was stopped. A stop-the-world (or the GC's suspendG) waiting
//     on a goroutine that cannot be preempted is ONE state that fits, and a toy
//     built that way reproduces the #43 shape: orphaned, ppid 1, ~81% of a core,
//     alive at 3x its timeout, `sample` showing one thread in the test's own
//     loop for every sample and one in preemptall/cond_timedwait. That is a
//     class, not the cause: what wedged cli.test is still unknown.
//
// THE CONSEQUENCE, which is why there are TWO guards. Inside that wedge no Go
// code runs: not testing's alarm, not a time.AfterFunc, and not an in-process
// os.Getppid poll either. The in-process poll is still here, because it is
// the cheap exit for an orphan whose scheduler is healthy — about 1 s instead
// of up to the whole timeout. But the bound on the wedge has to come from
// OUTSIDE the process:
//
//   - RLIMIT_CPU was tried and REFUTED. The kernel does send SIGXCPU at the
//     soft limit (a spinning sh died of it in 2 s), but the Go runtime installs
//     its own SIGXCPU handler and drops the signal, and exceeding the HARD
//     limit sent no SIGKILL: a wedged binary at soft 3 s / hard 6 s was still
//     spinning 30 s later.
//   - A signal from another process DOES end the wedge, because the runtime
//     acts on it inside the signal handler without scheduling anything.
//     Measured: SIGQUIT ended the reproduction in 1 s, and SIGTERM (which #43's
//     orphan also honoured) at once.
//
// So Arm starts a WATCHDOG: this same test binary re-executed in watchdog
// mode, which never runs a test. It holds the read end of a pipe whose write
// end only the target holds (os.Pipe is close-on-exec, so no git child
// inherits it). EOF on that pipe means the target is gone, and the watchdog
// exits. The pipe is how the watchdog learns of the exit at once; it is NOT
// identity (a child caught between fork and exec holds the write end for that
// instant, and a check-then-signal has a window). Identity is the pid AND the
// kernel start time the watchdog read when it began (D-025): it signals
// nothing whose start time has changed. It fires on two
// conditions: the target is still orphaned several polls after the
// in-process guard should have ended it (it must be wedged), or the target
// has run for twice its -test.timeout. It then records `sample` of the target
// to a file, because a wedged orphan's stderr is a dead pipe and this is the
// only diagnosis the next occurrence can leave, names the Go frames in it
// (symbolize: `go test` binaries carry no symbol table), then sends SIGQUIT,
// and SIGKILL if SIGQUIT did not end it. Only THEN does it say so on stderr:
// in #43's shape that stderr is the same dead pipe, and a watchdog that
// announced itself first died of SIGPIPE before it signalled anything (Codex
// code pass; the wedged-orphan leg reproduces it with a stderr pipe whose
// reader dies). Catching SIGPIPE as well was tried and cut: with the report
// last no mutant of it failed a leg, so it was a second fix nothing tested.
// SIGQUIT's own dump is no substitute:
// at the default traceback level it shows only the thread the signal landed
// on — measured, an idle M, not the spinning one.
//
// Cut: an in-process hard wall (time.AfterFunc(2*timeout, os.Exit)), which the
// issue proposed. With a healthy scheduler it duplicates testing's own alarm,
// and in the wedge it cannot fire. Also cut: finding the cause of the wedge. No
// live occurrence has been sampled yet, and the watchdog's sample is what will
// name it.
package testguard

import (
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// exitOrphaned is the in-process guard's exit status. Nobody waits on an
// orphan, so no parent reads it; the leg that kills a throwaway parent reads it
// through the test-only exit hook.
const exitOrphaned = 3

const (
	envWatchdog = "BUDDY_TESTGUARD_WATCHDOG" // "1": this process is a watchdog
	envTarget   = "BUDDY_TESTGUARD_TARGET"   // the target's pid
	envPPID     = "BUDDY_TESTGUARD_PPID"     // the target's parent when it armed
	envWall     = "BUDDY_TESTGUARD_WALL"     // the target's wall, a Duration; 0 = none
)

var (
	// pollEvery paces both guards. Getppid is one syscall and the sysctl is
	// one more, so a second is free.
	pollEvery = time.Second
	// orphanGrace is how many consecutive polls the watchdog sees the target
	// orphaned before it concludes the in-process guard cannot run. The
	// in-process guard needs at most one poll.
	orphanGrace = 3
	// sampleSecs is how long `sample` watches a stuck target.
	sampleSecs = 2
	// exit is os.Exit, a variable only so a test's helper can record why it
	// exited before it does.
	exit = os.Exit
	// pipeW keeps the write end of the watchdog's pipe open for the life of
	// the process. It must stay reachable: an *os.File closes its fd when it
	// is garbage-collected, and that EOF would retire the watchdog at the
	// first GC.
	pipeW *os.File
	// watchdogPID is the watchdog's pid, 0 when none started. A test reads it.
	watchdogPID int
)

// Arm must be the first statement of TestMain. In a watchdog process it never
// returns. In a test process it parses the flags (as TestMain must before
// reading one), starts the in-process orphan poll, and starts the watchdog. A
// watchdog that cannot start is reported on stderr and the tests run anyway:
// this is a bound on a leak, not a precondition of any test.
func Arm() {
	if os.Getenv(envWatchdog) == "1" {
		watchdog()
		// os.Exit, not exit: a helper's exit hook records why the TARGET
		// ended, and the watchdog runs the same TestMain.
		os.Exit(0)
	}
	if !flag.Parsed() {
		flag.Parse()
	}
	ppid := os.Getppid()
	go pollParent(ppid)
	if err := startWatchdog(ppid, wallFor(timeoutFlag())); err != nil {
		fmt.Fprintf(os.Stderr, "testguard: no watchdog for this test binary (%v); an orphan that wedges will not be bounded\n", err)
	}
}

// pollParent exits the process once its parent is no longer the one it
// started under — reparented, on darwin to launchd. It compares with the
// value at start rather than testing for 1, so a binary launchd started
// directly is never taken for an orphan.
func pollParent(ppid int) {
	for {
		time.Sleep(pollEvery)
		if os.Getppid() != ppid {
			exit(exitOrphaned)
		}
	}
}

// timeoutFlag is -test.timeout, or 0 when there is none to read.
func timeoutFlag() time.Duration {
	f := flag.Lookup("test.timeout")
	if f == nil {
		return 0
	}
	g, ok := f.Value.(flag.Getter)
	if !ok {
		return 0
	}
	d, _ := g.Get().(time.Duration)
	return d
}

// wallFor is twice the timeout. `-timeout 0` means somebody turned the
// timeout off on purpose, so there is no wall either; neither is there for a
// timeout so long that doubling it would overflow into a negative Duration
// (centuries — the same thing said differently). The orphan arm is unaffected.
func wallFor(timeout time.Duration) time.Duration {
	if timeout <= 0 || timeout > math.MaxInt64/2 {
		return 0
	}
	return 2 * timeout
}

func startWatchdog(ppid int, wall time.Duration) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(),
		envWatchdog+"=1",
		envTarget+"="+strconv.Itoa(os.Getpid()),
		envPPID+"="+strconv.Itoa(ppid),
		envWall+"="+wall.String(),
	)
	cmd.Stdin = r
	// Stderr is shared so a report reaches `go test` when anybody is still
	// reading it. Stdout stays /dev/null: the test binary's stdout is what
	// `go test` parses.
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		r.Close()
		w.Close()
		return err
	}
	r.Close()
	pipeW = w
	watchdogPID = cmd.Process.Pid
	go cmd.Wait() // reap it if it ever finishes first
	return nil
}

// watchdog runs in the re-executed process, whose parent is the target.
func watchdog() {
	target, _ := strconv.Atoi(os.Getenv(envTarget))
	ppid, _ := strconv.Atoi(os.Getenv(envPPID))
	wall, _ := time.ParseDuration(os.Getenv(envWall))
	gone := make(chan struct{})
	go func() {
		io.Copy(io.Discard, os.Stdin)
		close(gone)
	}()
	// The target is our parent, or it has already exited and this watchdog
	// has nothing to guard. Its start time, read now, is its identity.
	if target == 0 || os.Getppid() != target {
		return
	}
	_, born, identified := procOf(target)
	start := time.Now()
	orphaned := 0
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	for {
		select {
		case <-gone:
			return
		case <-tick.C:
		}
		if wall > 0 && time.Since(start) >= wall {
			stop(target, born, identified, gone, fmt.Sprintf("test binary pid %d has run %s, twice its -test.timeout", target, wall))
			return
		}
		if p, _, ok := procOf(target); ok && p != ppid {
			orphaned++
		} else {
			orphaned = 0
		}
		if orphaned >= orphanGrace {
			stop(target, born, identified, gone, fmt.Sprintf("test binary pid %d is orphaned and still running %d polls after its own guard should have exited it", target, orphaned))
			return
		}
	}
}

// stop records a native sample of the target, SIGQUITs it, and SIGKILLs it if
// that did not end it — each step only while the pipe is open AND the pid still
// carries the start time the watchdog read, so nothing but the target is ever
// sampled or signalled. What it did is reported LAST: stderr may be a dead pipe
// (SIGPIPE kills the watchdog on that write) or a live one nobody drains (a
// blocked write), and neither may stand between a wedged target and its
// signal. After the signals, either costs nothing.
func stop(target int, born int64, identified bool, gone <-chan struct{}, why string) {
	path := filepath.Join(os.TempDir(), fmt.Sprintf("buddy-testguard-%d-%d.sample.txt", target, time.Now().Unix()))
	ours := func() bool {
		if !alive(gone) || !identified {
			return false
		}
		_, b, ok := procOf(target)
		return ok && b == born
	}
	did := "it could not be identified, so nothing was sent"
	if ours() {
		sample(target, path, gone)
		did = "no sample was recorded"
		if _, err := os.Stat(path); err == nil {
			did = "sampled to " + path
		}
	}
	sent := ""
	for _, sig := range []syscall.Signal{syscall.SIGQUIT, syscall.SIGKILL} {
		if !ours() {
			break
		}
		syscall.Kill(target, sig)
		sent += " " + sig.String()
		select {
		case <-gone:
		case <-time.After(5 * time.Second):
		}
	}
	if sent != "" {
		did += "; sent" + sent
	}
	fmt.Fprintf(os.Stderr, "testguard: %s; %s\n", why, did)
}

func alive(gone <-chan struct{}) bool {
	select {
	case <-gone:
		return false
	default:
		return true
	}
}

// sample runs macOS's `sample`, bounded, and ignores every failure: the
// diagnosis is a bonus and the kill must not wait on it. A target that exits
// meanwhile cancels it.
func sample(pid int, path string, gone <-chan struct{}) {
	bin, err := exec.LookPath("sample")
	if err != nil {
		return
	}
	cmd := exec.Command(bin, strconv.Itoa(pid), strconv.Itoa(sampleSecs), "-file", path)
	if err := cmd.Start(); err != nil {
		return
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-gone:
		cmd.Process.Kill()
		return
	case <-time.After(time.Duration(sampleSecs)*time.Second + 20*time.Second):
		cmd.Process.Kill()
		return
	}
	if exe, err := os.Executable(); err == nil {
		symbolize(path, exe)
	}
}
