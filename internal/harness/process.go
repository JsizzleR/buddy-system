package harness

// Native protocols use framed JSON, not terminal output. A bounded scanner and
// single serialized writer keep a malformed or interrupted frame from becoming
// a plausible control receipt. Stderr stays on stderr and is never parsed.
//
// PROCESS OWNERSHIP (F-07). The worker runs as the leader of its own process
// group, so ending the transport ends the tools the harness started, not just
// the harness. Killing only the direct child left a build, a shell or an MCP
// server that had already passed its PreToolUse gate writing in the worktree
// after Close (TestJSONProcessCloseEndsDescendants). Close sends SIGTERM to the
// group, waits a bounded grace for the leader, then SIGKILLs the group.
//
// NEVER SIGNAL A GROUP WE MAY NOT OWN. -pgid names our group only while the
// group exists, and a group whose last member has been reaped can be reused by
// an unrelated process whose pid happens to match. The leader's zombie keeps
// that id reserved, so the leader is reaped in exactly one place: terminate,
// after its final group signal. Its exit is observed without reaping
// (awaitExit: kqueue on darwin, waitid(WNOWAIT) on linux), never with Wait.
//
// What this does NOT cover, and says so rather than claiming a clean tree:
//   - A descendant that leaves the group (setsid, setpgid, a shell's job
//     control, a daemonizing server) escapes the group signal, and the
//     operator chose (2026-10-11) that it stays unsignalled: it is not
//     pinned, so it is not ours. It is OBSERVED instead (descendants.go),
//     recorded in the ownership record while the worker runs, and reported
//     at close as ended, survivor or unknown. Close still closes our end of
//     its stdout, so the transport finishes and Events closes, but that
//     process keeps running. Measured: Claude Code puts every Bash tool
//     shell in a session of its own, so a background tool job is one.
//   - SIGKILL of the adapter, or a crash, runs no cleanup at all. The worker
//     then sees only stdin EOF, and because it is in its own group a terminal
//     ^C to the adapter's group no longer reaches it either. A released
//     journal flock proves the adapter exited, never that its worker did.
//     What a resume does about that is the ownership record (ownership.go):
//     every spawn here is fenced by a single-use LaunchAuthorization that
//     publishes intent before cmd.Start and the leader's identity before the
//     process is returned, so a later resume can refuse while this worker or
//     its group lives.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// nativeFrameLimit bounds one native frame (one JSON line). A longer frame is
// never skipped: it could be the approval or terminal event the controller is
// waiting for, and a truncated frame has no trustworthy request id. It fails
// the transport, which every backend reports as an UNKNOWN disconnection.
const nativeFrameLimit = 4 << 20

// terminateGrace is how long the leader gets between SIGTERM and SIGKILL. The
// leader has already seen stdin EOF by then. Two seconds lets a Node or Rust
// harness flush its own transcript; it is not a promise that it finished its
// turn, and it keeps adapter shutdown bounded on a signal or stdin EOF.
const terminateGrace = 2 * time.Second

// drainCap bounds how long a leader that exited by itself is given for the
// reader to consume what it wrote, while something keeps native stdout open.
const drainCap = 2 * terminateGrace

type JSONProcess struct {
	cmd    *exec.Cmd
	pgid   int
	stdin  *os.File
	stdout *os.File
	lines  chan json.RawMessage
	// done: the reader finished AND the leader was reaped.
	done chan struct{}
	// stop: write admission is closed. Closed before EOF is exposed.
	stop chan struct{}
	// exited: the leader exited, observed without reaping it.
	exited    chan struct{}
	reaped    chan struct{}
	writeLock chan struct{}
	errMu     sync.Mutex
	err       error
	stopOnce  sync.Once
	stdinOnce sync.Once
	termOnce  sync.Once
	// The narrow contract's observer (descendants.go). auth holds the
	// canonical ownership record the sampler and the close write.
	auth        *LaunchAuthorization
	samplerStop chan struct{}
	samplerDone chan struct{}
	captureOnce sync.Once
	trigger     string
	capturedAt  time.Time
	// signal is syscall.Kill unless a test watches it; trace is a test's
	// record of the shutdown order; scanning a test's hold on a scan. All
	// are fixed at start.
	signal   func(int, syscall.Signal) error
	trace    func(string)
	scanning func()
	reading  func()
}

// StartJSONProcess is the ONE way a worker child is created. auth is the
// controller's single-use launch authorization for this attempt (A-02): it is
// consumed before cmd.Start, publishes the durable intent and then the
// leader's identity before this returns, and refuses a second launch before
// any child exists. There is no launch without one.
func StartJSONProcess(ctx context.Context, auth *LaunchAuthorization, executable string, args []string, worktree string, env []string) (*JSONProcess, error) {
	return startJSONProcess(ctx, auth, executable, args, worktree, env, "")
}

func startJSONProcess(ctx context.Context, auth *LaunchAuthorization, executable string, args []string, worktree string, env []string, argv0 string) (*JSONProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if auth == nil {
		return nil, errors.New("worker launch without a launch authorization is refused; nothing records a worker launched outside the controller")
	}
	// A request deadline bounds startup/RPC, not the owned worker's lifetime.
	// Close explicitly ends only this child transport; it records no Buddy end.
	cmd := exec.Command(executable, args...)
	if argv0 != "" {
		// This changes only the process label, never the file exec resolves.
		cmd.Args[0] = argv0
	}
	cmd.Dir = worktree
	// A child is a new native session, never an alias of the coordinator.
	clean := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "BUDDY_SESSION", "BUDDY_HARNESS", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_MESSAGING_TOKEN", "CODEX_THREAD_ID", "CODEX_SESSION_ID":
			continue
		}
		clean = append(clean, entry)
	}
	cmd.Env = append(clean, env...)
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdin, ok := in.(*os.File)
	if !ok {
		in.Close()
		return nil, errors.New("native input pipe lacks cancellable writes")
	}
	// Our own pipe, not StdoutPipe: Wait closes a StdoutPipe reader, and Close
	// must be able to close it even when Wait cannot run yet.
	out, outWrite, err := os.Pipe()
	if err != nil {
		in.Close()
		return nil, err
	}
	cmd.Stdout = outWrite
	// The spawn runs inside the authorization: intent is durable before
	// cmd.Start, and the leader's pid, start time and group are durable before
	// this function returns the process, so before the backend's first native
	// write. If the record cannot be published, the child is ended while its
	// leader is still unreaped (the group id is still ours) and the launch
	// fails: a worker nobody could later identify is not launched.
	err = auth.Spawn(executable, func() (int, error) {
		if err := cmd.Start(); err != nil {
			return 0, err
		}
		return cmd.Process.Pid, nil
	}, func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Process.Signal(syscall.SIGKILL)
		_ = cmd.Wait()
	})
	if err != nil {
		in.Close()
		out.Close()
		outWrite.Close()
		return nil, err
	}
	// Only the child and its descendants hold the write end now, so EOF means
	// every holder is gone.
	outWrite.Close()
	p := &JSONProcess{cmd: cmd, pgid: cmd.Process.Pid, stdin: stdin, stdout: out, lines: make(chan json.RawMessage, 128),
		done: make(chan struct{}), stop: make(chan struct{}), exited: make(chan struct{}), reaped: make(chan struct{}), writeLock: make(chan struct{}, 1),
		auth: auth, samplerStop: make(chan struct{}), samplerDone: make(chan struct{}), signal: syscall.Kill, trace: func(string) {}, scanning: func() {}, reading: func() {}}
	every := descendantScanInterval
	if hooks := processTestHooks.Load(); hooks != nil {
		if hooks.signal != nil {
			p.signal = hooks.signal
		}
		if hooks.trace != nil {
			p.trace = hooks.trace
		}
		if hooks.scanEvery != 0 {
			every = hooks.scanEvery
		}
		if hooks.scanning != nil {
			p.scanning = hooks.scanning
		}
		if hooks.reading != nil {
			p.reading = hooks.reading
		}
	}
	go p.sample(every)
	go p.watchExit()
	go p.readFrames()
	return p, nil
}

// sample records escaped descendants every interval until a shutdown stops
// it. It never signals anything.
func (p *JSONProcess) sample(every time.Duration) {
	defer close(p.samplerDone)
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-p.samplerStop:
			return
		case <-tick.C:
		}
		p.scanning()
		leader, d, gen, ok := p.auth.descendantState()
		if !ok {
			continue
		}
		p.auth.commitDescendants(gen, scanDescendants(systemObserver, leader, d))
	}
}

// finalCapture stops and JOINS the sampler, then completes one last scan,
// once. Every shutdown runs it before stdin closes (see the file comment in
// descendants.go); the captured cohort is what the close report observes.
func (p *JSONProcess) finalCapture(trigger string) {
	p.captureOnce.Do(func() {
		close(p.samplerStop)
		<-p.samplerDone
		p.trigger, p.capturedAt = trigger, time.Now()
		if leader, d, gen, ok := p.auth.descendantState(); ok {
			p.auth.commitDescendants(gen, scanDescendants(systemObserver, leader, d))
		}
		p.trace("captured")
	})
}

// shutdownInput is the one order an adapter-initiated shutdown takes: write
// admission off, the capture COMPLETED, then stdin closed. Closing stdin first
// let a worker that exits on EOF reparent its children before anything saw
// them (Codex design review, 2026-10-12).
//
// The stdin-closed trace is written BEFORE the close syscall. A worker that
// exits on EOF does so the instant the pipe closes, watchExit's waitid returns,
// and its terminate traces group-term; traced after the syscall, stdin-closed
// could land behind group-term and the order test read a shutdown that was
// correct as "group-term,stdin-closed" (CI's ubuntu runner, 2026-10-24, dash
// exiting faster than this goroutine resumed; never seen on macOS). Nothing
// can observe the close before the syscall, so a trace ahead of it is ahead
// of every consequence. The test hook is the only reader of the trace.
func (p *JSONProcess) shutdownInput(trigger string) {
	p.stopOnce.Do(func() { close(p.stop); p.trace("admission-off") })
	p.finalCapture(trigger)
	p.stdinOnce.Do(func() { p.trace("stdin-closed"); _ = p.stdin.Close() })
}

// Claude's process name is also its hook ancestor marker. The executable
// remains the exact resolved file checked by admission (V-06).
func startClaudeProcess(ctx context.Context, auth *LaunchAuthorization, executable string, args []string, worktree string, env []string) (*JSONProcess, error) {
	return startJSONProcess(ctx, auth, executable, args, worktree, env, "claude")
}

func (p *JSONProcess) readFrames() {
	p.reading()
	scan := bufio.NewScanner(p.stdout)
	scan.Buffer(make([]byte, 4096), nativeFrameLimit)
scanLoop:
	for scan.Scan() {
		line := append(json.RawMessage(nil), scan.Bytes()...)
		if !json.Valid(line) {
			p.setErr(errors.New("native transport emitted invalid JSON"))
			break
		}
		select {
		case p.lines <- line:
		case <-p.stop:
			break scanLoop
		default:
			// Waiting here would hide EOF behind the backend's blocked
			// event publication while the child still accepted approvals.
			// Bound memory and fail the transport visibly; never drop a
			// native frame while leaving control writes enabled.
			p.setErr(errors.New("native output backpressure exceeded the bounded frame queue"))
			break scanLoop
		}
	}
	if err := scan.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			// F-08: name the bound. The frame was not read, so whatever it
			// carried (an approval, a result) is unknown, never absent.
			err = fmt.Errorf("native frame exceeded the %d-byte transport bound and was not read; native state, including any approval it carried, is unknown", nativeFrameLimit)
		}
		p.setErr(err)
	}
	// The transport, not a downstream consumer, must stop native writes.
	// A backend may be blocked publishing an earlier ordinary event and
	// therefore unable to observe Read's EOF yet. Close admission/stdin
	// before exposing EOF; ending the tree may take the grace period, and
	// EOF must not wait for it.
	p.setErr(errors.New("native output stream closed"))
	p.shutdownInput("stdout_eof")
	close(p.lines)
	p.terminate()
	_ = p.stdout.Close()
	close(p.done)
}

// watchExit ends the group as soon as the leader exits. The harness is gone;
// its tool processes are orphans that would keep writing in the worktree. It
// does not close admission at once: the reader still drains what the leader
// wrote, and a closed stop would let it drop a frame it could have delivered.
// But a descendant that LEFT the group survives the group signals, and one
// holding stdout kept the transport open until somebody called Close (Codex
// design review P2, 2026-10-12). That holder is not ours to signal, so the
// reader is released by closing OUR read end (releaseAfterExit).
func (p *JSONProcess) watchExit() {
	select {
	case <-p.reaped:
		return
	default:
	}
	if awaitExit(p.pgid) != nil {
		// Unknown: terminate falls back to the full grace before SIGKILL.
		return
	}
	close(p.exited)
	p.finalCapture("leader_exit")
	p.terminate()
	p.releaseAfterExit()
}

// releaseAfterExit gives the reader the grace to drain what the exited leader
// wrote, then, while the transport has not finished, closes OUR read end only
// once nothing in the pipe is unread, so no frame the leader wrote is lost.
// Past drainCap it stops waiting and says frames may have been lost. It names
// another holder only when the pipe showed nothing unread and no EOF: a fixed
// timeout used to drop a frame a descheduled reader had not read yet, and
// blamed an escaped holder it had not seen (Codex code review P2, 2026-10-12).
// The leader is reaped by now, so p.done waits on the reader alone.
func (p *JSONProcess) releaseAfterExit() {
	deadline := time.Now().Add(drainCap)
	wait := terminateGrace
	for {
		select {
		case <-p.done:
			return
		case <-time.After(wait):
		}
		pending, err := pipePending(p.stdout)
		switch {
		case err != nil:
			p.setErr(fmt.Errorf("the native worker exited and its stdout did not reach EOF; whether unread output remained is unknown (%v), so the adapter released its end: frames may have been lost and native state is unknown", err))
		case pending && time.Now().Before(deadline):
			wait = 100 * time.Millisecond
			continue
		case pending:
			p.setErr(fmt.Errorf("the native worker exited and its stdout still held unread output after %s; the adapter released its end: frames may have been lost and native state is unknown", drainCap))
		default:
			p.setErr(errors.New("the native worker exited, and another process still held its stdout open with nothing unread; the adapter released its own end without signalling that process"))
		}
		_ = p.stdout.Close()
		return
	}
}

// terminate is the ONLY place the leader is reaped, and it signals the group
// strictly before reaping (see the file comment).
//
// These are the only signals the process code sends after the launch: the
// pinned group and the pinned leader, nothing derived from a descendant.
func (p *JSONProcess) terminate() {
	p.termOnce.Do(func() {
		p.finalCapture("terminate")
		p.trace("group-term")
		_ = p.signal(-p.pgid, syscall.SIGTERM)
		select {
		case <-p.exited:
		case <-time.After(terminateGrace):
		}
		// Unconditional: the leader exiting says nothing about its children.
		_ = p.signal(-p.pgid, syscall.SIGKILL)
		// And the leader by pid, which is ours until the Wait below. If the
		// group signal reached nobody (measured by mutation: without Setpgid
		// it does), this still ends the leader, so Close stays bounded
		// instead of waiting in Wait forever.
		_ = p.signal(p.cmd.Process.Pid, syscall.SIGKILL)
		p.setErr(p.cmd.Wait())
		p.reportClose(time.Now())
		close(p.reaped)
	})
}

// reportClose observes the captured cohort once more, after the reap, and
// seals the record with the close report. A survivor is alive at observed_at;
// nothing in `ended` was signalled.
func (p *JSONProcess) reportClose(reaped time.Time) {
	p.auth.mu.Lock()
	var d DescendantRecord
	if p.auth.spawned.Descendants != nil {
		d = *p.auth.spawned.Descendants
	}
	p.auth.mu.Unlock()
	report := CloseReport{Trigger: p.trigger, CapturedAt: p.capturedAt, CaptureError: d.LastError, ReapedAt: reaped,
		Group:     fmt.Sprintf("SIGTERM to group %d, a %s grace for the leader, SIGKILL to the group and the leader, leader reaped", p.pgid, terminateGrace),
		Captured:  append([]ObservedProcess{}, d.Alive...),
		Ended:     []ProcessIdentity{},
		Survivors: []ObservedProcess{},
		Unknown:   []UnknownProcess{},
		// Always: nothing can attribute a descendant that left the tree before
		// a scan saw it, so the report never says the tree is empty.
		Unattributable: "unknown"}
	left := d
	left.Alive = nil
	for _, e := range d.Alive {
		state, err := stateWith(observeProcess, e.identity())
		switch state {
		case procGone, procRecycled:
			report.Ended = append(report.Ended, ProcessIdentity{PID: e.PID, Start: e.Start})
			left.Ended = satAdd(left.Ended, 1)
		case procError:
			report.Unknown = append(report.Unknown, UnknownProcess{PID: e.PID, Start: e.Start, Error: err.Error()})
			left.Alive = append(left.Alive, e)
		default:
			report.Survivors = append(report.Survivors, e)
			left.Alive = append(left.Alive, e)
		}
	}
	report.ObservedAt = time.Now()
	_ = p.auth.sealClose(report, left)
}

func (p *JSONProcess) setErr(err error) {
	if err != nil {
		p.errMu.Lock()
		if p.err == nil {
			p.err = err
		}
		p.errMu.Unlock()
	}
}
func (p *JSONProcess) Err() error                   { p.errMu.Lock(); defer p.errMu.Unlock(); return p.err }
func (p *JSONProcess) PID() int                     { return p.cmd.Process.Pid }
func (p *JSONProcess) Read() <-chan json.RawMessage { return p.lines }
func (p *JSONProcess) Write(v any) error {
	return p.WriteContext(context.Background(), v)
}
func (p *JSONProcess) WriteContext(ctx context.Context, v any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case p.writeLock <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-p.stop:
		return errors.New("native transport closed")
	}
	defer func() { <-p.writeLock }()
	if err := ctx.Err(); err != nil {
		return err
	}
	// Both writer admission and stop can be ready. Never let a selected
	// admission token bypass terminal state observed by the native reader.
	select {
	case <-p.stop:
		return errors.New("native transport closed")
	default:
	}
	f := p.stdin
	if deadline, ok := ctx.Deadline(); ok {
		if err := f.SetWriteDeadline(deadline); err != nil {
			return err
		}
	}
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = f.SetWriteDeadline(time.Now()); close(finished) })
	err := json.NewEncoder(f).Encode(v)
	if !stop() {
		<-finished
	}
	_ = f.SetWriteDeadline(time.Time{})
	if err != nil {
		// A partial JSON frame cannot be reused as a control channel. Close
		// this owned transport; the caller reports uncertainty, never retries.
		p.setErr(fmt.Errorf("native input write failed: %w", err))
		p.Close()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
			return context.DeadlineExceeded
		}
		return err
	}
	return nil
}

// Close ends write admission, then the owned process group, then releases the
// reader. It returns once the leader is reaped (bounded by terminateGrace plus
// SIGKILL delivery), so a caller about to exit has actually ended the group.
func (p *JSONProcess) Close() error {
	p.setErr(errors.New("native transport closed by the adapter"))
	p.shutdownInput("close")
	p.terminate()
	// A descendant that left the group can still hold the write end of
	// stdout; without this the reader, done and Events never finish.
	_ = p.stdout.Close()
	return nil
}
