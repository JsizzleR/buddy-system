package main

// Entry-point regressions: these run the BUILT adapter as the coordinator and
// the operator would, so startup ordering and signal handling are what is
// tested, not a helper. The native harness is a deterministic Python fake of
// the Codex app-server subset the adapter speaks; Buddy is the real binary on
// an isolated ledger. Nothing here launches a real claude or codex.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/JsizzleR/buddy-system/internal/harness"
	"github.com/JsizzleR/buddy-system/internal/store"
)

var built struct {
	once                sync.Once
	dir, buddy, adapter string
	err                 error
}

// builtBinaries builds buddy and buddy-adapter once per test binary.
func builtBinaries(t *testing.T) (string, string) {
	t.Helper()
	built.once.Do(func() {
		dir, err := os.MkdirTemp("", "buddy-adapter-entry-")
		if err == nil {
			dir, err = filepath.EvalSymlinks(dir)
		}
		if err != nil {
			built.err = err
			return
		}
		built.dir, built.buddy, built.adapter = dir, filepath.Join(dir, "buddy"), filepath.Join(dir, "buddy-adapter")
		root, err := filepath.Abs("../..")
		if err != nil {
			built.err = err
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		for _, target := range []struct{ out, pkg string }{{built.buddy, "./cmd/buddy"}, {built.adapter, "./cmd/buddy-adapter"}} {
			cmd := exec.CommandContext(ctx, "go", "build", "-o", target.out, target.pkg)
			cmd.Dir = root
			if out, err := cmd.CombinedOutput(); err != nil {
				built.err = fmt.Errorf("build %s: %v\n%s", target.pkg, err, out)
				return
			}
		}
	})
	if built.err != nil {
		t.Fatal(built.err)
	}
	return built.buddy, built.adapter
}

const fakeCodex = `#!/usr/bin/env python3
import json, os, subprocess, sys, time
env = os.environ
def operator_fd():
    want = env.get('BUDDY_TEST_OPERATOR', '')
    if not want:
        return None
    dev, ino = (int(x) for x in want.split(':'))
    for fd in range(256):
        try:
            st = os.fstat(fd)
        except OSError:
            continue
        if (st.st_dev, st.st_ino) == (dev, ino):
            return fd
    return None
def record(**kw):
    with open(env['BUDDY_TEST_MARKER'], 'a') as f:
        f.write(json.dumps(kw) + '\n')
if '--version' in sys.argv:
    record(kind='version', inherited=operator_fd())
    print('codex-cli 0.160.0')
    sys.exit(0)
tool = subprocess.Popen(['sleep', '300'])
record(kind='app-server', pid=os.getpid(), tool=tool.pid, inherited=operator_fd())
def emit(v):
    sys.stdout.write(json.dumps(v) + '\n')
    sys.stdout.flush()
for line in sys.stdin:
    m = json.loads(line)
    method = m.get('method')
    if method == 'initialize':
        emit({'id': m['id'], 'result': {}})
    elif method == 'hooks/list':
        emit({'id': m['id'], 'result': {'data': [{'cwd': m['params']['cwds'][0], 'errors': [], 'warnings': [], 'hooks': [{'enabled': True, 'eventName': 'preToolUse', 'handlerType': 'command', 'command': env['BUDDY_TEST_GATE'], 'matcher': '^(apply_patch|Bash)$', 'trustStatus': 'trusted', 'currentHash': 'fixture-hash', 'async': False}]}]}})
    elif method == 'thread/start':
        emit({'id': m['id'], 'result': {'thread': {'id': 'thread-1', 'status': {'type': 'idle'}}}})
        # Turn activity waits for the test, which releases it only after the
        # start receipt: until launch has recorded the new thread id, the
        # backend (correctly) ignores events naming it.
        while not os.path.exists(env['BUDDY_TEST_GO']):
            time.sleep(0.02)
        emit({'method': 'turn/started', 'params': {'threadId': 'thread-1', 'turn': {'id': 'turn-1', 'status': 'inProgress'}}})
        for rid in (77, 78):
            emit({'id': rid, 'method': 'item/fileChange/requestApproval', 'params': {'threadId': 'thread-1', 'turnId': 'turn-1', 'itemId': 'item-%d' % rid}})
    elif method == 'thread/read':
        emit({'id': m['id'], 'result': {'thread': {'id': 'thread-1', 'status': {'type': 'active'}}}})
    elif 'method' not in m:
        record(kind='response', id=m.get('id'), result=m.get('result'))
`

type entryFixture struct {
	dir, repo, receipt, launch, codex, marker, gate, buddy, adapter string
	env                                                             []string
}

func newEntryFixture(t *testing.T, buddyOverride string) *entryFixture {
	t.Helper()
	for _, name := range []string{"BUDDY_SESSION", "BUDDY_HARNESS", "CODEX_THREAD_ID", "CODEX_SESSION_ID", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_MESSAGING_TOKEN"} {
		t.Setenv(name, "")
	}
	buddy, adapter := builtBinaries(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &entryFixture{dir: dir, repo: filepath.Join(dir, "repo"), receipt: filepath.Join(dir, "receipt.json"), launch: filepath.Join(dir, "launch.json"),
		codex: filepath.Join(dir, "codex"), marker: filepath.Join(dir, "native.log"), buddy: buddy, adapter: adapter}
	if buddyOverride != "" {
		f.buddy = buddyOverride
	}
	if err := os.Mkdir(f.repo, 0700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", f.repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	st, err := store.Open(filepath.Join(f.repo, ".git", "buddy.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	if err := os.WriteFile(f.codex, []byte(fakeCodex), 0700); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "codex-home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("# isolated fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	f.gate = quote(adapter) + " hook --receipt " + quote(f.receipt) + " --buddy " + quote(f.buddy)
	launch, err := json.Marshal(map[string]any{"gate_command": f.gate, "gate_hash": "fixture-hash", "worktree": f.repo, "permission_mode": "workspace-write",
		"hook_config": filepath.Join(home, "config.toml"), "harness_version": "0.160.0", "hooks": map[string]string{"configured": "observed", "trusted": "observed", "observed": "unknown"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.launch, launch, 0600); err != nil {
		t.Fatal(err)
	}
	f.env = append(os.Environ(), "BUDDY_TEST_MARKER="+f.marker, "BUDDY_TEST_GATE="+f.gate, "BUDDY_TEST_GO="+filepath.Join(dir, "go"))
	return f
}

// release lets the fake start its turn: turn/started, then approvals 77, 78.
func (f *entryFixture) release(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, "go"), nil, 0600); err != nil {
		t.Fatal(err)
	}
}

// lockedBuffer is the adapter's stderr: exec's copy goroutine writes it
// while a failing test may read it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type runningAdapter struct {
	cmd      *exec.Cmd
	stdin    *os.File
	receipts chan map[string]any
	stderr   *lockedBuffer
	exited   chan error
}

func (f *entryFixture) start(t *testing.T, extra []*os.File, args []string, env ...string) *runningAdapter {
	t.Helper()
	cmd := exec.Command(f.adapter, append([]string{"--harness", "codex", "--executable", f.codex, "--buddy", f.buddy, "--launch", f.launch, "--receipt", f.receipt, "--label", "worker"}, args...)...)
	cmd.Env = append(append([]string(nil), f.env...), env...)
	cmd.ExtraFiles = extra
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdin = stdinRead
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	a := &runningAdapter{cmd: cmd, stdin: stdinWrite, receipts: make(chan map[string]any, 64), stderr: &lockedBuffer{}, exited: make(chan error, 1)}
	cmd.Stderr = a.stderr
	// An orphaned native descendant inherits the adapter's stderr. Without
	// this, a regression shows up as "never exited" instead of its cause.
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stdinRead.Close()
	go func() {
		s := bufio.NewScanner(out)
		s.Buffer(make([]byte, 4096), 4<<20)
		for s.Scan() {
			var v map[string]any
			if json.Unmarshal(s.Bytes(), &v) == nil {
				a.receipts <- v
			}
		}
		close(a.receipts)
		a.exited <- cmd.Wait()
	}()
	t.Cleanup(func() {
		_ = stdinWrite.Close()
		_ = cmd.Process.Kill()
	})
	return a
}

func (a *runningAdapter) await(t *testing.T, what string, match func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.After(15 * time.Second)
	var skipped []map[string]any
	for {
		select {
		case r, ok := <-a.receipts:
			if !ok {
				t.Fatalf("adapter exited awaiting %s; skipped %v; stderr: %s", what, skipped, a.stderr)
			}
			if match(r) {
				return r
			}
			skipped = append(skipped, r)
		case <-deadline:
			t.Fatalf("timed out awaiting %s; skipped %v; stderr: %s", what, skipped, a.stderr)
		}
	}
}

// waitExit bounds the WHOLE wait. Draining receipts first without a deadline
// let a wedged adapter pass once it finally exited (measured: 300 s, against
// the unfixed tree's stalled ledger).
func (a *runningAdapter) waitExit(t *testing.T, within time.Duration) error {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case _, open := <-a.receipts:
			if open {
				continue
			}
			select {
			case err := <-a.exited:
				return err
			case <-deadline:
			}
		case <-deadline:
		}
		t.Fatalf("adapter did not exit within %v; stderr: %s", within, a.stderr)
		return nil
	}
}

func nativeLog(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var v map[string]any
		if err := json.Unmarshal(line, &v); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, v)
	}
	return rows
}

func nativeEventKind(r map[string]any) string {
	e, _ := r["native_event"].(map[string]any)
	kind, _ := e["kind"].(string)
	return kind
}

func pidGone(pid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return syscall.Kill(pid, 0) != nil
}

func fileIdentity(t *testing.T, f *os.File) string {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Fstat(int(f.Fd()), &st); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino)
}

// F-02: the operator approval descriptor was wrapped only after Start had
// launched the native worker, and never marked close-on-exec, so the worker
// (and a model's shell beneath it) inherited it: it could read and steal the
// operator's decisions, or, through a FIFO opened read-write, write its own.
func TestAdapterOperatorDescriptorAdmission(t *testing.T) {
	for _, bad := range []string{"read-write fifo", "write-only pipe", "socket", "directory", "not open", "stdin"} {
		t.Run("refused before any child or journal/"+bad, func(t *testing.T) {
			f := newEntryFixture(t, "")
			var extra []*os.File
			fd := "3"
			switch bad {
			case "read-write fifo":
				fifo := filepath.Join(f.dir, "approvals.fifo")
				if err := syscall.Mkfifo(fifo, 0600); err != nil {
					t.Fatal(err)
				}
				h, err := os.OpenFile(fifo, os.O_RDWR, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer h.Close()
				extra = []*os.File{h}
			case "write-only pipe":
				r, w, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				defer r.Close()
				defer w.Close()
				extra = []*os.File{w}
			case "socket":
				pair, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
				if err != nil {
					t.Fatal(err)
				}
				a, b := os.NewFile(uintptr(pair[0]), "a"), os.NewFile(uintptr(pair[1]), "b")
				defer a.Close()
				defer b.Close()
				extra = []*os.File{a}
			case "directory":
				// Read-only, so only the descriptor-type check refuses it.
				h, err := os.Open(f.dir)
				if err != nil {
					t.Fatal(err)
				}
				defer h.Close()
				extra = []*os.File{h}
			case "not open":
				fd = "9"
			case "stdin":
				// The coordinator's control stream, a read-only pipe: the
				// access and type checks pass, so only the descriptor number
				// refuses it. Admitted, peer control text would be read as
				// operator approvals. Nothing tested it (verification audit:
				// the fd < 3 guard was deletable). stdout and stderr arrive
				// as write ends, which the access check already refuses.
				fd = "0"
			}
			a := f.start(t, extra, []string{"--operator-fd", fd})
			a.stdin.Close()
			err := a.waitExit(t, 20*time.Second)
			if err == nil {
				t.Fatalf("adapter accepted %s as the operator descriptor", bad)
			}
			if rows := nativeLog(t, f.marker); len(rows) != 0 {
				t.Fatalf("a native child ran before the descriptor was refused: %v", rows)
			}
			if _, err := os.Stat(f.receipt + ".events"); !os.IsNotExist(err) {
				t.Fatalf("the journal was opened before the descriptor was refused: %v", err)
			}
			want := "operator approval descriptor"
			if bad == "not open" {
				want = "is not open"
			}
			if !strings.Contains(a.stderr.String(), want) {
				t.Fatalf("refusal does not say %q: %s", want, a.stderr)
			}
		})
	}
	t.Run("read-only pipe stays with the operator and approves exactly one request", func(t *testing.T) {
		f := newEntryFixture(t, "")
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		defer w.Close()
		a := f.start(t, []*os.File{r}, []string{"--operator-fd", "3"}, "BUDDY_TEST_OPERATOR="+fileIdentity(t, r))
		r.Close() // the adapter holds its inherited copy at fd 3
		start := a.await(t, "start receipt", func(v map[string]any) bool { return v["operation"] == "start" })
		if start["error"] != nil {
			t.Fatalf("start failed: %v", start["error"])
		}
		identity := start["identity"]
		f.release(t)
		for _, id := range []float64{77, 78} {
			a.await(t, "approval", func(v map[string]any) bool {
				e, _ := v["native_event"].(map[string]any)
				ap, _ := e["approval"].(map[string]any)
				return nativeEventKind(v) == "approval" && ap["id"] == id
			})
		}
		approve, _ := json.Marshal(map[string]any{"schema": "buddy.control.v1", "id": "op-77", "operation": "approve", "identity": identity,
			"approval": map[string]any{"id": 77, "native_session_id": "thread-1", "turn_id": "turn-1", "decision": "allow"}})
		if _, err := w.Write(append(approve, '\n')); err != nil {
			t.Fatal(err)
		}
		done := a.await(t, "operator approval receipt", func(v map[string]any) bool { return v["request_id"] == "op-77" })
		if delivery, _ := done["approval_delivery"].(map[string]any); delivery["state"] != "submitted" {
			t.Fatalf("operator approval did not reach the worker: %v", done)
		}
		inspect, _ := json.Marshal(map[string]any{"schema": "buddy.control.v1", "id": "inspect", "operation": "inspect", "identity": identity})
		if _, err := a.stdin.Write(append(inspect, '\n')); err != nil {
			t.Fatal(err)
		}
		state := a.await(t, "inspect receipt", func(v map[string]any) bool { return v["request_id"] == "inspect" })
		pending, _ := state["approvals"].([]any)
		if len(pending) != 1 || pending[0].(map[string]any)["id"] != float64(78) {
			t.Fatalf("approval did not reach exactly the intended request: pending %v", pending)
		}
		a.stdin.Close()
		if err := a.waitExit(t, 10*time.Second); err != nil {
			t.Fatalf("adapter exit: %v; stderr: %s", err, a.stderr)
		}
		var responses []map[string]any
		for _, row := range nativeLog(t, f.marker) {
			switch row["kind"] {
			case "version", "app-server":
				if row["inherited"] != nil {
					t.Fatalf("native %s inherited the operator descriptor at fd %v", row["kind"], row["inherited"])
				}
			case "response":
				responses = append(responses, row)
			}
		}
		if len(responses) != 1 || responses[0]["id"] != float64(77) || responses[0]["result"].(map[string]any)["decision"] != "accept" {
			t.Fatalf("native responses: %v", responses)
		}
	})
}

// F-07: the adapter had no signal handling, so SIGTERM, SIGINT or SIGHUP
// killed it without Close and left the worker's tools running.
func TestAdapterSignalEndsWorkerTree(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			f := newEntryFixture(t, "")
			a := f.start(t, nil, nil)
			a.await(t, "start receipt", func(v map[string]any) bool { return v["operation"] == "start" })
			var native map[string]any
			for _, row := range nativeLog(t, f.marker) {
				if row["kind"] == "app-server" {
					native = row
				}
			}
			if native == nil {
				t.Fatal("native fake did not start")
			}
			worker, tool := int(native["pid"].(float64)), int(native["tool"].(float64))
			t.Cleanup(func() { _ = syscall.Kill(tool, syscall.SIGKILL); _ = syscall.Kill(worker, syscall.SIGKILL) })
			if syscall.Kill(tool, 0) != nil {
				t.Fatal("control: the worker's tool process is not running")
			}
			if err := a.cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			_ = a.waitExit(t, 10*time.Second)
			if !strings.Contains(a.stderr.String(), "received") {
				t.Fatalf("adapter did not shut down through its handler: %s", a.stderr)
			}
			if !pidGone(worker, 3*time.Second) || !pidGone(tool, 3*time.Second) {
				t.Fatalf("worker %d or its tool %d outlived the adapter", worker, tool)
			}
		})
	}
}

// F-07: SIGPIPE is ignored. A coordinator that stops reading (it exited, or
// closed its end) turns the adapter's next receipt into a write on a broken
// pipe, and a Go program writing to a broken stdout dies by SIGPIPE by
// default: no Close, so the worker, in its own process group, kept running
// with its tools. With the signal ignored the write fails, the adapter shuts
// down through Close and the tree ends. Nothing tested it: the verification's
// mutant deleting signal.Ignore survived every permanent test.
func TestAdapterBrokenControlOutputEndsWorkerTree(t *testing.T) {
	f := newEntryFixture(t, "")
	cmd := exec.Command(f.adapter, "--harness", "codex", "--executable", f.codex, "--buddy", f.buddy, "--launch", f.launch, "--receipt", f.receipt, "--label", "worker")
	cmd.Env = f.env
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := &lockedBuffer{}
	cmd.Stderr = stderr
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	var start map[string]any
	if err := json.NewDecoder(output).Decode(&start); err != nil || start["operation"] != "start" {
		t.Fatalf("control: no start receipt: %v %v; stderr: %s", start, err, stderr)
	}
	var worker, tool int
	for _, row := range nativeLog(t, f.marker) {
		if row["kind"] == "app-server" {
			worker, tool = int(row["pid"].(float64)), int(row["tool"].(float64))
		}
	}
	if worker == 0 || tool == 0 {
		t.Fatal("control: the native fake did not start")
	}
	t.Cleanup(func() { _ = syscall.Kill(tool, syscall.SIGKILL); _ = syscall.Kill(worker, syscall.SIGKILL) })
	// The coordinator goes away; the turn released next needs receipts.
	output.Close()
	f.release(t)
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		t.Fatalf("a broken control output did not stop the adapter; stderr: %s", stderr)
	}
	if !strings.Contains(stderr.String(), "control output unavailable") {
		t.Fatalf("the adapter did not shut down through Close: %s", stderr)
	}
	if !pidGone(worker, 3*time.Second) || !pidGone(tool, 3*time.Second) {
		t.Fatalf("worker %d or its tool %d outlived the adapter", worker, tool)
	}
}

// F-30: a stalled ledger child held the controller lock inside Observe, so on
// stdin EOF Close waited behind it and the adapter never exited. The start
// receipt proves the ledger path worked before the stall.
func TestAdapterStdinEOFWithStalledLedgerExits(t *testing.T) {
	buddy, _ := builtBinaries(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stall, stalled := filepath.Join(dir, "stall"), filepath.Join(dir, "stalled")
	wrapper := filepath.Join(dir, "buddy-stalling")
	// The stalled child also leaves a grandchild holding its stdout, so
	// killing the child alone would still leave the ledger call waiting on
	// the pipe; the bound has to cover that wait too.
	program := fmt.Sprintf("#!/usr/bin/env python3\nimport os,subprocess,sys,time\nif sys.argv[1:3]==['adapter','inspect'] and os.path.exists(%q):\n holder=subprocess.Popen(['sleep','300'])\n open(%q,'w').write('%%d %%d' %% (os.getpid(), holder.pid))\n time.sleep(300)\nos.execv(%q,[%q]+sys.argv[1:])\n", stall, stalled, buddy, buddy)
	if err := os.WriteFile(wrapper, []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	f := newEntryFixture(t, wrapper)
	a := f.start(t, nil, nil)
	a.await(t, "start receipt", func(v map[string]any) bool { return v["operation"] == "start" })
	if err := os.WriteFile(stall, nil, 0600); err != nil {
		t.Fatal(err)
	}
	// The turn's first event (busy) now reaches a ledger call that stalls.
	f.release(t)
	deadline := time.Now().Add(10 * time.Second)
	var stalledPID, holderPID int
	for holderPID == 0 && time.Now().Before(deadline) {
		if raw, err := os.ReadFile(stalled); err == nil {
			fmt.Sscan(string(raw), &stalledPID, &holderPID)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if holderPID == 0 {
		t.Fatal("the native event's observation never reached the stalled ledger")
	}
	t.Cleanup(func() { _ = syscall.Kill(stalledPID, syscall.SIGKILL); _ = syscall.Kill(holderPID, syscall.SIGKILL) })
	a.stdin.Close()
	_ = a.waitExit(t, 10*time.Second)
	if !pidGone(stalledPID, 3*time.Second) {
		t.Fatalf("the stalled ledger child %d outlived the adapter", stalledPID)
	}
}

// V-07: a coordinator may keep stdout open without draining it. Fill the
// pipe BEFORE launch so the very first receipt blocks, then send SIGTERM.
// Startup must remain cancellable and Close must still end the worker tree.
func TestAdapterSignalClosesWorkerWithFullStartupOutput(t *testing.T) {
	f := newEntryFixture(t, "")
	out, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	defer writer.Close()
	fd := int(writer.Fd())
	if err := unix.SetNonblock(fd, true); err != nil {
		t.Fatal(err)
	}
	filled := 0
	for {
		n, err := unix.Write(fd, make([]byte, 4096))
		filled += n
		if err == unix.EAGAIN {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if filled == 0 {
		t.Fatal("control: stdout pipe never accepted data")
	}
	if err := unix.SetNonblock(fd, false); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(f.adapter, "--harness", "codex", "--executable", f.codex, "--buddy", f.buddy,
		"--launch", f.launch, "--receipt", f.receipt, "--label", "worker")
	cmd.Env, cmd.Stdout = f.env, writer
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stderr := &lockedBuffer{}
	cmd.Stderr, cmd.WaitDelay = stderr, 2*time.Second
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(f.receipt); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("startup did not reach receipt publication")
		}
		time.Sleep(20 * time.Millisecond)
	}
	var worker, tool int
	for _, row := range nativeLog(t, f.marker) {
		if row["kind"] == "app-server" {
			worker, tool = int(row["pid"].(float64)), int(row["tool"].(float64))
		}
	}
	if worker == 0 || tool == 0 {
		t.Fatal("control: native worker and tool never started")
	}
	t.Cleanup(func() { _ = syscall.Kill(worker, syscall.SIGKILL); _ = syscall.Kill(tool, syscall.SIGKILL) })
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-exited
		t.Fatal("full startup stdout prevented signal cleanup")
	}
	if !strings.Contains(stderr.String(), "received") || !pidGone(worker, 3*time.Second) || !pidGone(tool, 3*time.Second) {
		t.Fatalf("full stdout bypassed orderly worker cleanup: %s", stderr.String())
	}
}

func TestAdapterExpectedShutdownDoesNotPublishDisconnect(t *testing.T) {
	for _, phase := range []string{"live-disconnect", "already-closing", "cancel-during-observation"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if phase == "already-closing" {
				cancel()
			}
			events := make(chan harness.Event, 1)
			events <- harness.Event{Kind: "disconnected", NativeSessionID: "native"}
			close(events)
			observed, emitted := 0, 0
			observeNativeEvents(ctx, events, func(context.Context, harness.Event) (harness.Receipt, error) {
				observed++
				if phase == "cancel-during-observation" {
					cancel()
				}
				return harness.Receipt{State: harness.Observation{State: "unknown"}}, nil
			}, func(r harness.Receipt, err error) {
				emitted++
				if err != nil || r.State.State != "unknown" {
					t.Fatal("unexpected live disconnect must retain its uncertainty")
				}
			})
			if phase == "live-disconnect" {
				if observed != 1 || emitted != 1 {
					t.Fatal("unexpected transport loss was hidden")
				}
			} else if emitted != 0 || phase == "already-closing" && observed != 0 {
				t.Fatalf("expected shutdown presented as transport failure: observed=%d emitted=%d", observed, emitted)
			}
		})
	}
}

type blockedReceiptWriter struct {
	entered chan struct{}
	release chan struct{}
}

func (w *blockedReceiptWriter) Write(b []byte) (int, error) {
	close(w.entered)
	<-w.release
	return len(b), nil
}

func TestReceiptEmitterCancelsAnEstablishedBlockedWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &blockedReceiptWriter{entered: make(chan struct{}), release: make(chan struct{})}
	defer close(w.release)
	emit := receiptEmitter(ctx, cancel, make(chan error, 1), w)
	returned := make(chan struct{})
	go func() { emit(harness.Receipt{Schema: harness.Schema}, nil); close(returned) }()
	select {
	case <-w.entered: // Positive control: Encode really is inside the blocked Write.
	case <-time.After(time.Second):
		t.Fatal("receipt never reached the output writer")
	}
	cancel()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("cancellation could not interrupt receipt publication")
	}
}
