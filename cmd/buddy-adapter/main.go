// buddy-adapter controls one native worker over inherited local descriptors.
// It is deliberately a separate binary: buddy remains an offline ledger CLI.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/JsizzleR/buddy-system/internal/harness"
)

// waitPriorCap bounds --wait-prior. Ten minutes outlasts any grace a native
// harness gives itself on stdin EOF; a worker still alive past it is not
// going to exit on its own, and the operator has to look at it.
const waitPriorCap = 10 * time.Minute

func main() {
	if err := run(os.Args[1:]); err != nil {
		if len(os.Args) > 1 && os.Args[1] == "hook" {
			// Claude treats exit 1 as a non-blocking hook diagnostic. Every
			// wrapper failure must deny even when no gate process was reached.
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": "Buddy adapter could not verify captured identity or gate execution"}})
			fmt.Fprintln(os.Stderr, "buddy-adapter hook: captured identity or gate execution refused")
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "buddy-adapter:", err)
		os.Exit(1)
	}
}

// adoptOperatorFD validates the operator approval descriptor and takes it out
// of every future child's reach, BEFORE anything else opens a file or starts a
// process (F-02). It used to be wrapped only after Start had launched the
// native worker, and never marked close-on-exec. A descriptor handed over by a
// shell redirection or Python's pass_fds arrives without close-on-exec, so the
// worker inherited it: measured one level further down, a Codex sandboxed
// shell read the operator's line from it. A read-only pipe let the worker
// steal approvals; a FIFO opened read-write let it WRITE approve requests the
// adapter accepted, so the model could answer its own approval.
//
//   - It must already be open (F_GETFD). A mistyped number used to alias
//     whatever the journal or a native pipe opened at that number later.
//   - Its access mode must be read-only: the adapter never writes approvals,
//     and a writable handle is one more way to forge one.
//   - It must be a pipe, FIFO, terminal or file, not a socket or a runtime fd.
//   - It is marked close-on-exec here, so the version probe (the first child)
//     and the worker never see it.
func adoptOperatorFD(fd int) (*os.File, error) {
	if fd < 3 {
		return nil, errors.New("operator approval descriptor must be separate from stdin/stdout/stderr")
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err != nil {
		return nil, fmt.Errorf("operator approval descriptor %d is not open: %w", fd, err)
	}
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		return nil, fmt.Errorf("operator approval descriptor %d: %w", fd, err)
	}
	if flags&unix.O_ACCMODE != unix.O_RDONLY {
		return nil, fmt.Errorf("operator approval descriptor %d must be open read-only; a writable handle (for example a FIFO opened 3<>) could forge approvals", fd)
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return nil, fmt.Errorf("operator approval descriptor %d: %w", fd, err)
	}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFIFO, unix.S_IFCHR, unix.S_IFREG:
	default:
		return nil, fmt.Errorf("operator approval descriptor %d must be a pipe, FIFO, terminal or file", fd)
	}
	syscall.CloseOnExec(fd)
	f := os.NewFile(uintptr(fd), "operator-approvals")
	if f == nil {
		return nil, errors.New("invalid operator descriptor")
	}
	return f, nil
}

func run(args []string) error {
	if len(args) > 0 && args[0] == "hook" {
		return runHook(args[1:], os.Stdin, os.Stdout, os.Stderr)
	}
	fs := flag.NewFlagSet("buddy-adapter", flag.ContinueOnError)
	kind := fs.String("harness", "", "codex or claude")
	executable := fs.String("executable", "", "absolute native harness binary")
	buddy := fs.String("buddy", "", "absolute Buddy binary")
	config := fs.String("launch", "", "launch JSON file (explicit worktree, permissions and hook compatibility)")
	receipt := fs.String("receipt", "", "absolute immutable lifecycle receipt output")
	label := fs.String("label", "", "Buddy worker label")
	resume := fs.Bool("resume", false, "resume using the existing captured receipt")
	waitPrior := fs.Duration("wait-prior", 0, "with --resume: wait up to this long (at most 10m) for the prior adapter, the recorded worker leader and its process group to be gone, re-checking passively every 250 ms, before the resume begins; signals nothing")
	operatorFD := fs.Int("operator-fd", -1, "optional inherited read-only descriptor >=3 owned by the operator approval UI; never peer/model output")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	// A bounded, passive wait (A-02): the cap keeps an operator from parking a
	// resume indefinitely behind a worker that will never exit by itself, and
	// the flag means nothing without a prior worker to wait for.
	if *waitPrior < 0 || *waitPrior > waitPriorCap {
		return fmt.Errorf("--wait-prior must be between 0 and %v, got %v", waitPriorCap, *waitPrior)
	}
	if *waitPrior > 0 && !*resume {
		return errors.New("--wait-prior applies only with --resume")
	}
	// First, before any file is opened or any child (even --version) starts.
	var operator *os.File
	if *operatorFD >= 0 {
		f, err := adoptOperatorFD(*operatorFD)
		if err != nil {
			return err
		}
		operator = f
		defer operator.Close()
	}
	// A dead coordinator must not kill the adapter mid-write with SIGPIPE; the
	// write fails instead and shutdown runs (F-07).
	signal.Ignore(syscall.SIGPIPE)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	if !filepath.IsAbs(*executable) || !filepath.IsAbs(*buddy) || !filepath.IsAbs(*receipt) {
		return errors.New("executable, buddy and receipt require absolute paths")
	}
	var launch harness.Launch
	if *resume {
		b, err := os.ReadFile(*receipt)
		if err != nil {
			return err
		}
		var saved harness.Saved
		if err = json.Unmarshal(b, &saved); err != nil {
			return err
		}
		launch = saved.Launch
		if *kind != saved.Identity.Harness {
			return errors.New("resume harness differs from captured identity")
		}
	} else {
		b, err := os.ReadFile(*config)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(b, &launch); err != nil {
			return err
		}
	}
	if !filepath.IsAbs(launch.Worktree) {
		return errors.New("launch worktree must be absolute")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	expectedGate := quote(self) + " hook --receipt " + quote(*receipt) + " --buddy " + quote(*buddy)
	if launch.GateCommand != expectedGate {
		return fmt.Errorf("gate_command must exactly match this receipt-bound adapter command: %s", expectedGate)
	}
	var backend harness.Backend
	switch *kind {
	case "codex":
		backend, err = harness.NewCodex(*executable)
	case "claude":
		backend, err = harness.NewClaude(*executable)
	default:
		return errors.New("unsupported harness")
	}
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// shutdown carries the first reason to stop: a signal, or the coordinator
	// gone from our stdout. Either cancels in-flight calls immediately. It is
	// armed before the controller so a signal also ends a --wait-prior wait.
	shutdown := make(chan error, 2)
	go func() {
		select {
		case s := <-signals:
			shutdown <- fmt.Errorf("received %v; closed the worker transport and its process group", s)
			cancel()
		case <-ctx.Done():
		}
	}()
	ledger := harness.CLILedger{Binary: *buddy, Worktree: launch.Worktree}
	var c *harness.Controller
	if *resume && *waitPrior > 0 {
		// The wait precedes the 30 s start context: it is a wait for the prior
		// worker to be gone, not part of this worker's startup budget. It
		// re-runs the whole resume admission and takes the journal only when
		// the prior adapter has released it; Resume below re-checks anyway.
		waitCtx, waitCancel := context.WithTimeout(ctx, *waitPrior)
		c, err = harness.NewControllerAfterPrior(waitCtx, backend, ledger, *receipt)
		waitCancel()
	} else {
		c, err = harness.NewController(backend, ledger, *receipt)
	}
	if err != nil {
		return err
	}
	// Close ends the worker's process group (bounded), then the journal. It
	// runs on every exit path that runs at all; SIGKILL runs none (F-07).
	defer func() {
		// Close intentionally ends the native stream. Stop observing first so
		// this local teardown cannot publish a new unknown disconnection.
		cancel()
		_ = c.Close()
	}()
	emit := receiptEmitter(ctx, cancel, shutdown, os.Stdout)
	startCtx, startCancel := context.WithTimeout(ctx, 30*time.Second)
	var r harness.Receipt
	if *resume {
		r, err = c.Resume(startCtx)
	} else {
		r, err = c.Start(startCtx, *kind, launch, *label)
	}
	startCancel()
	emit(r, err)
	if err != nil {
		return err
	}
	go observeNativeEvents(ctx, backend.Events(), c.Observe, emit)
	read := func(in io.Reader, operator bool) error {
		s := bufio.NewScanner(in)
		s.Buffer(make([]byte, 4096), 1<<20)
		for s.Scan() {
			var q harness.Request
			if err := json.Unmarshal(s.Bytes(), &q); err != nil {
				emit(harness.Receipt{Schema: harness.Schema}, err)
				continue
			}
			callCtx, end := context.WithTimeout(ctx, 30*time.Second)
			var r harness.Receipt
			var err error
			if operator {
				r, err = c.OperatorApprove(callCtx, q)
			} else {
				r, err = c.Handle(callCtx, q)
			}
			end()
			emit(r, err)
		}
		return s.Err()
	}
	if operator != nil {
		go func() {
			if err := read(operator, true); err != nil {
				emit(harness.Receipt{Schema: harness.Schema}, err)
			}
		}()
	}
	control := make(chan error, 1)
	go func() { control <- read(os.Stdin, false) }()
	select {
	case err = <-control:
	case err = <-shutdown:
	}
	return err
}

func observeNativeEvents(ctx context.Context, events <-chan harness.Event,
	observe func(context.Context, harness.Event) (harness.Receipt, error), emit func(harness.Receipt, error)) {
	for {
		var e harness.Event
		select {
		case <-ctx.Done():
			return
		case next, open := <-events:
			if !open || ctx.Err() != nil {
				return
			}
			e = next
		}
		r, err := observe(ctx, e)
		if ctx.Err() != nil {
			return
		}
		emit(r, err)
	}
}

func receiptEmitter(ctx context.Context, cancel context.CancelFunc, shutdown chan<- error, writer io.Writer) func(harness.Receipt, error) {
	// V-07: stdout may be an open, full pipe. One writer preserves framing;
	// callers await its acknowledgment only while the controller is live. A
	// signal can therefore reach deferred Close even during the START receipt.
	// The blocked writer owns no controller lock and cannot hold cleanup open.
	type output struct {
		receipt harness.Receipt
		done    chan struct{}
	}
	writes := make(chan output)
	go func() {
		encoder := json.NewEncoder(writer)
		for {
			select {
			case <-ctx.Done():
				return
			case out := <-writes:
				if e := encoder.Encode(out.receipt); e != nil {
					select {
					case shutdown <- fmt.Errorf("control output unavailable (%v); closed the worker transport", e):
					default:
					}
					cancel()
				}
				close(out.done)
			}
		}
	}()
	return func(r harness.Receipt, err error) {
		if err != nil {
			r.Error = err.Error()
		}
		out := output{receipt: r, done: make(chan struct{})}
		select {
		case writes <- out:
		case <-ctx.Done():
			return
		}
		select {
		case <-out.done:
		case <-ctx.Done():
		}
	}
}
