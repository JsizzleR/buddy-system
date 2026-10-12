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
//     control, a daemonizing server) escapes the group signal. Close still
//     closes our end of its stdout, so the transport finishes and Events
//     closes, but that process keeps running. Whether a native harness puts
//     its tool jobs in their own groups is a native qualification question.
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
	admitOnce sync.Once
	termOnce  sync.Once
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
		done: make(chan struct{}), stop: make(chan struct{}), exited: make(chan struct{}), reaped: make(chan struct{}), writeLock: make(chan struct{}, 1)}
	go p.watchExit()
	go p.readFrames()
	return p, nil
}

// Claude's process name is also its hook ancestor marker. The executable
// remains the exact resolved file checked by admission (V-06).
func startClaudeProcess(ctx context.Context, auth *LaunchAuthorization, executable string, args []string, worktree string, env []string) (*JSONProcess, error) {
	return startJSONProcess(ctx, auth, executable, args, worktree, env, "claude")
}

func (p *JSONProcess) readFrames() {
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
	p.closeAdmission()
	close(p.lines)
	p.terminate()
	_ = p.stdout.Close()
	close(p.done)
}

// watchExit ends the group as soon as the leader exits. The harness is gone;
// its tool processes are orphans that would keep writing in the worktree, and
// one holding stdout would keep the transport open forever. It does not close
// admission or our stdout: the reader still drains what the leader wrote.
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
	p.terminate()
}

func (p *JSONProcess) closeAdmission() {
	p.admitOnce.Do(func() { close(p.stop); _ = p.stdin.Close() })
}

// terminate is the ONLY place the leader is reaped, and it signals the group
// strictly before reaping (see the file comment).
func (p *JSONProcess) terminate() {
	p.termOnce.Do(func() {
		_ = syscall.Kill(-p.pgid, syscall.SIGTERM)
		select {
		case <-p.exited:
		case <-time.After(terminateGrace):
		}
		// Unconditional: the leader exiting says nothing about its children.
		_ = syscall.Kill(-p.pgid, syscall.SIGKILL)
		// And the leader by pid, which is ours until the Wait below. If the
		// group signal reached nobody (measured by mutation: without Setpgid
		// it does), this still ends the leader, so Close stays bounded
		// instead of waiting in Wait forever.
		_ = p.cmd.Process.Signal(syscall.SIGKILL)
		p.setErr(p.cmd.Wait())
		close(p.reaped)
	})
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
	p.closeAdmission()
	p.terminate()
	// A descendant that left the group can still hold the write end of
	// stdout; without this the reader, done and Events never finish.
	_ = p.stdout.Close()
	return nil
}
