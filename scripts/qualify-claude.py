#!/usr/bin/env python3
"""Opt-in Claude adapter qualification against a loopback Messages fixture.

Only disposable repositories, configuration and binaries are written. The actual
buddy-adapter controls the installed native Claude process, including its real
permission callback and receipt-bound Buddy hook. Synthetic provider responses
prove only the measured local contract, never real inference or a mixed fleet.

Run: python3 scripts/qualify-claude.py [--buddy PATH --adapter PATH]
Every attempt leaves result.json plus observations under /private/tmp, including
failed attempts. No installation, global settings changes or provider credentials.
"""

import argparse
import datetime
import hashlib
import http.server
import json
import os
import pathlib
import queue
import shutil
import signal
import subprocess
import sys
import tempfile
import threading
import time
import traceback


# Where a successful native Write's omitted is_error renews (D-078); this
# mirrors claudeWriteOmissionVersions in internal/harness/claude_renewal.go,
# and a Go test refuses any other spelling or set (2.1.299 joined 2026-10-14).
WRITE_RENEWAL_QUALIFIED = ("2.1.291", "2.1.299")
SCHEMA = "buddy.control.v1"
# The files check-claude-gate.py reads (hashed into result.json, F-37).
EVIDENCE = ("adapter-events.json", "adapter.stderr", "launch.json", "settings.json", "source-manifest.json",
            "worker.json", "worker.json.events", "worker.json.proc", "provider-observations.json",
            "ledger-inspection.json", "unparsed-output.json", "work/guarded.txt", "work/allowed.txt",
            "native-observations.jsonl", "bin/claude-observer", "managed-hook-refusal/result.json",
            "managed-hook-refusal/adapter-events.json", "managed-hook-refusal/adapter.stderr",
            "managed-hook-refusal/claude-managed-fixture", "managed-hook-refusal/launch.json",
            "managed-hook-refusal/native-observations.jsonl")
REPO = pathlib.Path(__file__).resolve().parent.parent


def _admission_lib():
    """scripts/admission_lib.py, loaded once per process under one name, so a
    runner and every fixture module it loads share ONE candidate binding."""
    import importlib.util
    name = "buddy_admission_lib"
    if name not in sys.modules:
        spec = importlib.util.spec_from_file_location(name, pathlib.Path(__file__).resolve().with_name("admission_lib.py"))
        module = importlib.util.module_from_spec(spec)
        sys.modules[name] = module
        spec.loader.exec_module(module)
    return sys.modules[name]


admission = _admission_lib()
candidate_flags = admission.candidate_flags
# The recorded versions (D-079), oldest first. A run's own contract is the
# binary it was given (bind_claude), never a pick from this list.
QUALIFICATION_VERSIONS = admission.recorded_versions("claude")
VERSION = QUALIFICATION_VERSIONS[-1]


def bind_claude(args):
    """D-080: bind the --claude binary as this run's candidate, run the
    RESOLVED file from here on (the adapter refuses a link, and an updater
    re-pointing one mid-run would swap the bytes under the binding), and make
    its version the run's contract. A typed --claude-version that disagrees
    with the binary refuses, as the version check always did. Before this a
    plain run needed four BUDDY_QUALIFY_* variables or failed its first case."""
    if not args.claude:
        raise RuntimeError("claude is not on PATH; provide --claude")
    binding = admission.bind("claude", args.claude)
    if getattr(args, "claude_version", None) not in (None, binding["version"]):
        raise RuntimeError("--claude-version " + args.claude_version + " is not the binary's " + binding["version_output"])
    args.claude = binding["artifact"]
    args.claude_version = binding["version"]
    return binding


def add_claude_version(parser):
    parser.add_argument("--claude-version",
                        help="the contract to exercise; defaults to the binary's own version, and a typed one must match it")


def digest(path):
    return hashlib.sha256(pathlib.Path(path).read_bytes()).hexdigest()


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n")


# CLEANUP ON TERM/HUP, AND ONLY OF WHAT WE PROVABLY OWN (review F-35).
# No runner handled SIGTERM or SIGHUP, so a closed pane or a harness timeout
# skipped every `finally: fleet.close()`: adapters (start_new_session) and their
# native children outlived the runner, then hung once its loopback provider
# died with it. Raising SystemExit from the handler runs those finally blocks.
# SIGKILL cannot be caught; nothing here claims to survive it.
#
# The fallback kill used to be `os.killpg(os.getpgid(<native pid from the
# trace>))` when that group id was one of ours. A pid number is not a process:
# after the native exits, its pid and even its group id can be reused, so a
# matching NUMBER could signal a stranger. A process here is (pid, kernel start
# time), read while ownership is provable: the adapter right after Popen (not
# yet reaped, so the pid cannot have been reused), and a native descendant while
# its ancestry still reaches that exact adapter. Setpgid deliberately gives the
# worker a DIFFERENT group; equal group ids no longer establish this ancestry.
# A later signal goes only to a process
# whose start time still matches, and every fallback kill is recorded as a
# cleanup FAILURE: the adapter was supposed to end its own children.
def exit_on_signals():
    def handler(signum, frame):
        raise SystemExit(128 + signum)
    for signum in (signal.SIGTERM, signal.SIGHUP):
        signal.signal(signum, handler)


def process_start(pid):
    """The kernel's start time for pid, or None if no such process.

    macOS: sysctl kern.proc.pid, p_starttime as [sec, usec]. Elsewhere:
    /proc/<pid>/stat field 22, [ticks since boot, 0]. Each is an identity a
    reused pid cannot reproduce, which is all the callers compare, and the two
    are never compared with each other (a process is observed and rechecked on
    one machine). Linux's libc has no `sysctl` symbol at all: CI's ubuntu
    runner raised AttributeError from every owned_process call, which took
    three OwnedCleanup tests down as errors and, because the proxies call it at
    startup, made two injection tests report "never attempted" (measured
    2026-10-24, the first run to get past the Go suite there).
    """
    if sys.platform != "darwin":
        return process_start_proc(pid)
    import ctypes
    import ctypes.util
    import struct
    libc = ctypes.CDLL(ctypes.util.find_library("c"), use_errno=True)
    # CTL_KERN, KERN_PROC, KERN_PROC_PID. kinfo_proc begins with extern_proc,
    # whose first member is p_starttime (struct timeval). Measured: 648 bytes,
    # and a reaped pid answers success with size 0.
    mib = (ctypes.c_int * 4)(1, 14, 1, pid)
    buf, size = ctypes.create_string_buffer(1024), ctypes.c_size_t(1024)
    if libc.sysctl(mib, 4, buf, ctypes.byref(size), None, ctypes.c_size_t(0)) != 0:
        raise OSError(ctypes.get_errno(), "sysctl kern.proc.pid failed")
    if size.value == 0:
        return None
    return list(struct.unpack_from("@qi", buf.raw, 0))


def process_start_proc(pid, text=None):
    """process_start from a Linux /proc/<pid>/stat line (passed in for a test).

    Field 22 is starttime. The comm in field 2 is parenthesised and may itself
    hold spaces and parentheses, so fields are counted from the LAST ')'. A
    process that is gone has no file (ENOENT), and one mid-exit can answer
    ESRCH to the read: both are "no such process", like the sysctl's size 0.
    """
    if text is None:
        try:
            with open(f"/proc/{pid}/stat", encoding="ascii", errors="replace") as fh:
                text = fh.read()
        except (FileNotFoundError, ProcessLookupError):
            return None
    fields = text[text.rindex(")") + 1:].split()
    return [int(fields[19]), 0]  # fields[0] is field 3 (state); field 22 is 19 after it


def owned_process(pid):
    """Identity of a process we provably own right now (see above)."""
    return {"pid": pid, "pgid": os.getpgid(pid), "start": process_start(pid)}


def still_owned(identity):
    start = process_start(identity["pid"])
    return start is not None and start == identity["start"]


def process_parent(pid):
    """Read one process's parent, never enumerate or signal by a process scan."""
    row = subprocess.run(["ps", "-o", "ppid=", "-p", str(pid)], capture_output=True,
                         text=True, timeout=5)
    value = row.stdout.strip()
    return int(value) if row.returncode == 0 and value.isdecimal() else None


def descendant_of(identity, leaders):
    """Bind the current native to an exact live adapter across process groups.

    The trace carries historical pid numbers, so each sampled parent and birth
    is rechecked. A stale trace, unrelated current process, or vanished adapter
    proves nothing. The bounded walk admits a native under an observer proxy.
    """
    chain, seen = [], set()
    current = identity
    for _ in range(32):
        pid = current["pid"]
        if pid in seen or pid <= 1 or current["start"] is None:
            return False
        seen.add(pid)
        for leader in leaders:
            if pid == leader["pid"] and current["start"] == leader["start"]:
                return (still_owned(leader) and still_owned(identity) and
                        all(still_owned(node) and process_parent(node["pid"]) == parent
                            for node, parent in chain))
        parent = process_parent(pid)
        if parent is None or parent <= 1:
            return False
        chain.append((current, parent))
        try:
            current = owned_process(parent)
        except ProcessLookupError:
            return False
    return False


def owned_natives(trace, leaders):
    """Native processes from the trace descended from a verified live adapter.

    Call while the adapters are alive. A native whose leader already exited is
    left out: its ancestry can no longer be proven ours, so it is never signaled
    and observed_child_exit reports it instead.
    """
    leaders = [v for v in leaders if still_owned(v)]
    found = []
    if not trace.exists():
        return found
    for line in trace.read_text().splitlines():
        row = json.loads(line)
        if row.get("type") != "buddy_fixture_native_process" or type(row.get("pid")) is not int:
            continue
        try:
            identity = owned_process(row["pid"])
        except (ProcessLookupError, PermissionError):
            continue
        if identity["start"] is not None and descendant_of(identity, leaders):
            found.append(identity)
    return found


def signal_owned_groups(identities):
    """Signal each captured group once, only through a still-identical member."""
    signaled = []
    for identity in identities:
        try:
            if (identity["pgid"] not in signaled and still_owned(identity) and
                    os.getpgid(identity["pid"]) == identity["pgid"]):
                os.killpg(identity["pgid"], signal.SIGKILL)
                signaled.append(identity["pgid"])
        except ProcessLookupError:
            pass
    return signaled


def crash_owned(client, natives):
    """The intentional fault kills adapter AND its separately captured worker.

    This is the fixture's requested crash seam, not adapter cleanup evidence.
    Capture native identities before calling; after killing the adapter their
    ancestry cannot be reconstructed safely from old pid numbers.
    """
    if not natives or not still_owned(client.owned):
        raise RuntimeError("cannot prove adapter and native ownership before fixture crash")
    groups = signal_owned_groups([client.owned, *natives])
    client.process.wait(timeout=5)
    return groups


def end_owned(identities, errors, forced):
    """Every unexpected fallback kill remains a cleanup FAILURE."""
    for group in signal_owned_groups(identities):
        forced.append(group)
        errors.append("fallback SIGKILL of owned group %d: its adapter did not end it" % group)


def observed_child_exit(trace):
    """Check only PIDs created by this fixture observer; never signal by scan."""
    try:
        pids = []
        for line in trace.read_text().splitlines():
            row = json.loads(line)
            if not isinstance(row, dict):
                raise ValueError("native observation is not an object")
            if row.get("type") == "buddy_fixture_native_process":
                pid = row.get("pid")
                if type(pid) is not int or not 1 < pid < 2**31:
                    raise ValueError("native observation has an invalid process id")
                pids.append(pid)
        if not pids:
            raise ValueError("native trace contains no process observations")
    except (OSError, ValueError, TypeError) as exc:
        # An observer failure must not suppress the qualification's result.json
        # or turn missing evidence into a successful child-exit observation.
        return {"status": "unknown", "observed_pids": [], "remaining_or_unknown_pids": [],
                "observer_error": str(exc)}
    remaining = set(pids)
    deadline = time.monotonic() + 3
    while remaining and time.monotonic() < deadline:
        for pid in tuple(remaining):
            try:
                os.kill(pid, 0)
            except ProcessLookupError:
                remaining.remove(pid)
            except PermissionError:
                pass
        if remaining:
            time.sleep(.05)
    return {"status": "unknown" if remaining else "observed_absent", "observed_pids": pids,
            "remaining_or_unknown_pids": sorted(remaining)}


def clean_env():
    # No inherited API keys, proxies, harness IDs, hook settings or agent state.
    # HOME is preserved, never redirected; native state uses CLAUDE_CONFIG_DIR.
    env = {key: os.environ[key] for key in ("PATH", "HOME", "USER", "LOGNAME", "SHELL") if key in os.environ}
    env.update(LC_ALL="C", TERM="dumb", GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL="/dev/null")
    return env


def run(argv, cwd, env, timeout=60):
    result = subprocess.run([str(x) for x in argv], cwd=cwd, env=env,
                            capture_output=True, text=True, timeout=timeout)
    if result.returncode:
        raise RuntimeError(f"{argv[0]} exited {result.returncode}: {result.stderr}")
    return result.stdout


def source_manifest(env):
    # Every git read of the REAL checkout spells --no-optional-locks (F-34):
    # a plain `git status` refreshes the index and takes index.lock in the
    # operator's shared checkout, which can fail a concurrent session's commit.
    # The flag, not GIT_OPTIONAL_LOCKS in clean_env, because clean_env also
    # becomes the native harness's environment and is part of its contract.
    names = run(["git", "--no-optional-locks", "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--",
                 "cmd", "internal", "go.mod", "go.sum"], REPO, env).split("\0")
    return {name: digest(REPO / name) for name in sorted(set(names)) if name and
            (name.endswith(".go") and not name.endswith("_test.go") or name in ("go.mod", "go.sum"))}


class MessagesFixture(http.server.BaseHTTPRequestHandler):
    """Small SSE contract from the official Messages streaming documentation.

    The script emits only fixture Write calls. Request headers are never retained,
    and fixture tool results (not prompts or ambient environment) are the evidence.
    """

    def log_message(self, *args):
        pass

    def do_POST(self):
        size = int(self.headers.get("Content-Length", "0"))
        if size > 8 << 20:
            self.send_error(413)
            return
        request = json.loads(self.rfile.read(size))
        if self.path.split("?")[0] == "/v1/messages/count_tokens":
            body = b'{"input_tokens":100}'
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        if self.path.split("?")[0] != "/v1/messages":
            self.server.unexpected.append(self.path)
            self.send_error(404)
            return
        outputs = []
        for message in request.get("messages", []):
            content = message.get("content", [])
            if isinstance(content, list):
                outputs.extend(x for x in content if x.get("type") == "tool_result"
                               and x.get("tool_use_id", "").startswith("toolu_buddy_"))
        entry = {"path": self.path, "model": request.get("model"),
                 "stream": request.get("stream"), "tool_outputs": outputs,
                 "tools": [x.get("name") for x in request.get("tools", [])]}
        self.server.requests.append(entry)
        index = len(outputs)
        if index < len(self.server.sequence):
            name, value = self.server.sequence[index]
            block = {"type": "tool_use", "id": f"toolu_buddy_{index}", "name": name, "input": {}}
            delta = {"type": "input_json_delta", "partial_json": json.dumps(value)}
            reason = "tool_use"
        else:
            block = {"type": "text", "text": ""}
            delta = {"type": "text_delta", "text": "Deterministic Buddy fixture complete."}
            reason = "end_turn"
        events = [
            {"type": "message_start", "message": {"id": f"msg_buddy_{len(self.server.requests)}",
             "type": "message", "role": "assistant", "model": request.get("model"),
             "content": [], "stop_reason": None, "stop_sequence": None,
             "usage": {"input_tokens": 100, "output_tokens": 1}}},
            {"type": "content_block_start", "index": 0, "content_block": block},
            {"type": "content_block_delta", "index": 0, "delta": delta},
            {"type": "content_block_stop", "index": 0},
            {"type": "message_delta", "delta": {"stop_reason": reason, "stop_sequence": None},
             "usage": {"output_tokens": 1}},
            {"type": "message_stop"},
        ]
        body = "".join("event: " + e["type"] + "\ndata: " + json.dumps(e) + "\n\n" for e in events).encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


class Adapter:
    def __init__(self, argv, cwd, env, root):
        self.events = []
        self.pending = []
        self.unparsed = []
        self.queue = queue.Queue()
        self.stderr = (root / "adapter.stderr").open("w")
        operator_read, operator_write = os.pipe()
        self.operator = os.fdopen(operator_write, "w")
        try:
            self.process = subprocess.Popen(candidate_flags(argv) + ["--operator-fd", str(operator_read)],
                cwd=cwd, env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                stderr=self.stderr, text=True, pass_fds=(operator_read,), start_new_session=True)
        except BaseException:
            self.operator.close()
            self.stderr.close()
            raise
        finally:
            os.close(operator_read)
        # Not yet reaped, so this pid is ours (F-35).
        self.owned = owned_process(self.process.pid)
        self.forced = []
        self.reader = threading.Thread(target=self.read, daemon=True)
        self.reader.start()

    def read(self):
        for line in self.process.stdout:
            try:
                value = json.loads(line)
            except ValueError:
                self.unparsed.append(line)
                continue
            self.events.append(value)
            self.queue.put(value)

    def wait(self, predicate, timeout=40, permit_error=False):
        # Native events and control replies have independent stdout producers.
        # Keep unmatched observations: a fast result may precede its operator
        # response receipt, and discarding it would fabricate a timeout.
        for index, value in enumerate(self.pending):
            if predicate(value):
                self.pending.pop(index)
                if value.get("error") and not permit_error:
                    raise RuntimeError(value["error"])
                return value
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            try:
                value = self.queue.get(timeout=.2)
            except queue.Empty:
                if self.process.poll() is not None:
                    raise RuntimeError(f"adapter exited {self.process.returncode}")
                continue
            if value.get("error") and not permit_error:
                raise RuntimeError(value["error"])
            if predicate(value):
                return value
            self.pending.append(value)
        raise RuntimeError("timed out waiting for native adapter evidence")

    def send(self, value, operator=False):
        stream = self.operator if operator else self.process.stdin
        stream.write(json.dumps(value) + "\n")
        stream.flush()

    def close(self):
        self.process.stdin.close()
        self.operator.close()
        try:
            self.process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            # Still unreaped, so the group is provably ours; the kill is still
            # an adapter that would not exit, and callers report it (F-35).
            self.forced.append("SIGTERM")
            os.killpg(self.process.pid, signal.SIGTERM)
            try:
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.forced.append("SIGKILL")
                os.killpg(self.process.pid, signal.SIGKILL)
                self.process.wait(timeout=5)
        self.reader.join(timeout=3)
        self.stderr.close()


def proxy_input_source():
    """Inline a stoppable relay in disposable, self-contained native proxies.

    A child can exit while its caller keeps stdin open (missing native history
    does this). A daemon blocked in sys.stdin.buffer then holds CPython's buffer
    lock during finalization and SIGABRTs, causing macOS crash pop-ups. Poll the
    raw descriptor and join the relay after child exit instead of abandoning it.
    """
    return r'''import os,select
input_stop=threading.Event()
def input_lines():
 pending=b''
 while not input_stop.is_set():
  if not select.select([sys.stdin.fileno()],[],[],.05)[0]:continue
  chunk=os.read(sys.stdin.fileno(),65536)
  if not chunk:
   if pending:yield pending
   return
  pending+=chunk
  while b'\n' in pending:
   line,pending=pending.split(b'\n',1)
   yield line+b'\n'
def relay_inputs():
 try:inputs()
 except BrokenPipeError:pass
'''


def native_observer(path, native, trace, extra_args=()):
    """A transparent stdio observer; only an explicit negative case adds argv."""
    path.write_text("#!" + sys.executable + "\n" + '''import json,subprocess,sys
args=sys.argv[1:]
if args!=['--version']:args=EXTRA+args
p=subprocess.Popen(["claude"]+args,executable=NATIVE,stdin=sys.stdin,stdout=subprocess.PIPE,stderr=sys.stderr)
with open(TRACE,'a') as log:
 log.write(json.dumps({'type':'buddy_fixture_native_process','pid':p.pid})+'\\n');log.flush()
 for line in p.stdout:
  try:
   v=json.loads(line)
   if v.get('type') in ('user','result','system','control_response','assistant'):
    keys=('type','subtype','session_id','uuid','isReplay','origin','parent_tool_use_id','terminal_reason','is_error','plugins','state','user_message_uuid','user_message_uuids','result_index','queued_turn_count','messaging_socket_path')
    projection={k:v[k] for k in keys if k in v}
    projection['native_keys']=sorted(v)
    # Claim renewal (D-078) classifies tool results by shape: record ids,
    # whether is_error is present, the tool_use_result type, filePath and key
    # names, and each tool_use's name and path keys. Never content or input bodies.
    content=(v.get('message') or {}).get('content')
    if isinstance(content,list):
     projection['tool_blocks']=[{'type':b.get('type'),'id':b.get('id',b.get('tool_use_id')),'name':b.get('name'),
      'is_error':b.get('is_error','<absent>') if b.get('type')=='tool_result' else None,
      'path':(b.get('input') or {}).get('file_path') if isinstance(b.get('input'),dict) else None}
      for b in content if isinstance(b,dict) and b.get('type') in ('tool_use','tool_result')]
    shape=v.get('tool_use_result')
    if isinstance(shape,dict):projection['tool_use_result']={'type':shape.get('type'),'filePath':shape.get('filePath'),'keys':sorted(shape)}
    elif shape is not None:projection['tool_use_result']='<'+type(shape).__name__+'>'
    if 'tool_result_meta' in v:projection['tool_result_meta']=v['tool_result_meta']
    log.write(json.dumps(projection)+'\\n');log.flush()
  except ValueError:pass
  sys.stdout.buffer.write(line);sys.stdout.buffer.flush()
sys.exit(p.wait())
'''.replace('NATIVE', repr(str(native))).replace('TRACE', repr(str(trace))).replace('EXTRA', repr(list(extra_args))))
    path.chmod(0o700)


def claim_renewals(work, root):
    """{slug: renewed} for open claims, from an immutable private copy of the
    fixture's own ledger, read only once no buddy connection holds a WAL."""
    import sqlite3
    db = work / ".git" / "buddy.db"
    wal = pathlib.Path(str(db) + "-wal")
    for _ in range(50):
        if not wal.exists() or wal.stat().st_size == 0:
            break
        time.sleep(.1)
    else:
        raise RuntimeError("fixture ledger WAL stayed nonempty; refusing an immutable read")
    fd, snapshot = tempfile.mkstemp(prefix="renewal-", suffix=".db", dir=root)
    os.close(fd)
    shutil.copyfile(db, snapshot)
    with sqlite3.connect("file:" + snapshot + "?mode=ro&immutable=1", uri=True) as conn:
        return {slug: renewed for slug, renewed in conn.execute("SELECT slug, renewed FROM claims WHERE state='open'")}


def edit_shape(trace, path):
    """The native shape of the one main-thread Edit of `path` and its result,
    from the metadata observer's trace (D-078's open question): whether the
    tool_result carries is_error (and its value), the frame's tool_use_result
    type and filePath, how many tool blocks share the frame, and whether
    tool_result_meta is present. Shapes only, never content. None untraced."""
    if not trace.is_file():
        return None
    rows = [json.loads(x) for x in trace.read_text().splitlines() if x.strip()]
    uses = [b["id"] for v in rows if v.get("type") == "assistant" and v.get("parent_tool_use_id") is None
            for b in v.get("tool_blocks") or [] if b.get("type") == "tool_use" and b.get("name") == "Edit" and b.get("path") == path]
    frames = [v for v in rows if v.get("type") == "user" and len(uses) == 1
              for b in v.get("tool_blocks") or [] if b.get("type") == "tool_result" and b.get("id") == uses[0]]
    if len(uses) != 1 or len(frames) != 1:
        return {"edit_uses": len(uses), "result_frames": len(frames)}
    v = frames[0]
    block = next(b for b in v["tool_blocks"] if b.get("id") == uses[0])
    return {"edit_uses": 1, "result_frames": 1, "tool_use_id": uses[0], "is_error": block.get("is_error"),
            "tool_use_result": v.get("tool_use_result"), "tool_blocks_in_frame": len(v["tool_blocks"]),
            "tool_result_meta_present": "tool_result_meta" in v, "is_replay": v.get("isReplay"),
            "parent_tool_use_id": v.get("parent_tool_use_id")}


def renewal_evidence(work, root, receipt, before, identity, checks, version):
    """Claim renewal from a completed native Write (F-26, D-078), read from the
    ledger and the adapter's journal after the turn's idle. The journal order
    shows the renewal after the operator's allow was submitted (the Write could
    not complete before it) and before the turn's idle.

    A successful Write OMITS is_error natively; the adapter renews it only on a
    version where that omission was measured with its create/update witness
    (WRITE_RENEWAL_QUALIFIED, mirroring claudeWriteOmissionVersions). On any
    other version the expected outcome is the conservative one: the written
    claim does not move and the journal reports renewal coverage unknown.
    The Edit appended after the Write omits is_error on every measured
    version and renews on none (Edit omission is unqualified)."""
    if version not in WRITE_RENEWAL_QUALIFIED:
        after = claim_renewals(work, root)
        journal = [json.loads(x) for x in pathlib.Path(str(receipt) + ".events").read_text().splitlines()]
        renewals = [r for r in journal if r.get("renewal")]
        checks["unqualified_write_renews_nothing"] = all(after.get(s) == before.get(s) is not None
                                                         for s in ("worker-allowed", "worker-denied", "worker-control", "held"))
        # One row for the Write's result and one for the Edit's, each unknown.
        checks["unqualified_write_reports_coverage_unknown"] = len(renewals) == 2 and all(
            r["renewal"]["coverage"]["state"] == "unknown" and r["renewal"]["result"]["state"] == "not_attempted" for r in renewals)
        return {"before": before, "after": after, "journal": [r["renewal"] for r in renewals],
                "unqualified": "Write omission semantics are measured only on " + ", ".join(WRITE_RENEWAL_QUALIFIED)}
    after = claim_renewals(work, root)
    journal = [json.loads(x) for x in pathlib.Path(str(receipt) + ".events").read_text().splitlines()]
    renewals = [(i, r) for i, r in enumerate(journal) if r.get("renewal")]
    applied = [(i, r) for i, r in renewals if r["renewal"]["result"]["state"] == "applied"]
    placed = sorted({x for _, r in applied for x in r["renewal"].get("placed_paths") or []})
    allow_at = next((i for i, r in enumerate(journal) if (r.get("approval_response") or {}).get("decision") == "allow"
                     and (r.get("approval_delivery") or {}).get("state") == "submitted"), None)
    idle_at = next((i for i, r in enumerate(journal) if (r.get("native_event") or {}).get("kind") == "idle"), None)
    first = next((i for i, r in applied if "allowed.txt" in (r["renewal"].get("placed_paths") or [])), None)
    checks["renewal_written_claim_moved"] = after.get("worker-allowed", 0) > before.get("worker-allowed", 0)
    checks["renewal_denied_write_claim_unmoved"] = after.get("worker-denied") == before.get("worker-denied") is not None
    checks["renewal_control_claim_unmoved"] = after.get("worker-control") == before.get("worker-control") is not None
    checks["renewal_held_claim_unmoved"] = after.get("held") == before.get("held") is not None
    checks["renewal_journalled_after_write_before_idle"] = None not in (allow_at, idle_at, first) and allow_at < first < idle_at
    checks["renewal_placed_only_the_written_path"] = placed == ["allowed.txt"]
    # The Write's renewal is observed coverage. The Edit that follows it
    # OMITS is_error too (measured 2026-10-13 on 2.1.291 and 2.1.299, with a
    # tool_use_result that has no `type`), and Edit omission is not qualified
    # (D-078), so its row must say coverage unknown and renew nothing: one
    # applied row, then one not-attempted row, in that order.
    unknown = [(i, r) for i, r in renewals if r["renewal"]["coverage"]["state"] == "unknown"]
    checks["renewal_write_coverage_observed"] = bool(applied) and all(r["renewal"]["coverage"]["state"] == "observed" for _, r in applied)
    checks["renewal_edit_omission_reports_coverage_unknown"] = (len(applied) == 1 and len(unknown) == 1 and
        len(renewals) == 2 and unknown[0][1]["renewal"]["result"]["state"] == "not_attempted" and
        unknown[0][1]["renewal"]["renewed_claims"] == 0 and idle_at is not None and applied[0][0] < unknown[0][0] < idle_at)
    checks["renewal_same_identity"] = bool(renewals) and all(r["identity"] == identity for _, r in renewals)
    return {"before": before, "after": after, "journal": [r["renewal"] for _, r in renewals],
            "observed_native_spellings": sorted({x for _, r in renewals for x in r["renewal"].get("paths") or []}),
            "placed_paths": placed}


def managed_hook_refusal(root, binary, buddy, claude, work, env, launch, server):
    """Exercise the native parent-policy seam, without changing host policy.

    Configured hooks remain present in get_settings when allowManagedHooksOnly
    disables their source. This proves the adapter checks effective policy before
    admitting an input, rather than confusing configured with enforceable.
    """
    case = root / "managed-hook-refusal"
    case.mkdir()
    (case / "claude").mkdir()
    receipt = case / "worker.json"
    settings = case / "settings.json"
    gate = f"'{binary}' hook --receipt '{receipt}' --buddy '{buddy}'"
    write_json(settings, {"enabledPlugins": {}, "hooks": {"PreToolUse": [{"matcher": "^(Edit|Write|NotebookEdit)$",
        "hooks": [{"type": "command", "command": gate, "timeout": 10}]}]}})
    overridden = dict(launch, gate_command=gate, hook_config=str(settings))
    write_json(case / "launch.json", overridden)
    wrapper = case / "claude-managed-fixture"
    native_observer(wrapper, claude, case / "native-observations.jsonl",
                    ["--managed-settings", '{"allowManagedHooksOnly":true}'])
    result = {"policy_seam": "native --managed-settings parent policy; no machine policy changes",
              "observer_sha256": digest(wrapper), "checks": {}}
    adapter = None
    before = len(server.requests)
    try:
        adapter = Adapter([binary, "--harness", "claude", "--executable", wrapper, "--buddy", buddy,
            "--launch", case / "launch.json", "--receipt", receipt, "--label", "managed-hook-refusal"],
            work, dict(env, CLAUDE_CONFIG_DIR=str(case / "claude")), case)
        start = adapter.wait(lambda x: x.get("operation") == "start", permit_error=True)
        result["refusal"] = start.get("error", "")
        result["checks"]["managed_policy_refused"] = "allowManagedHooksOnly" in result["refusal"]
        result["checks"]["refused_before_identity_capture"] = not receipt.exists()
        result["checks"]["refused_without_provider_call"] = len(server.requests) == before
        return result
    finally:
        if adapter:
            try:
                adapter.close()
            except BaseException as exc:
                result["cleanup_failure"] = str(exc)
                result["checks"]["managed_case_cleanup"] = False
            result["adapter_exit"] = adapter.process.returncode
            write_json(case / "adapter-events.json", adapter.events)
            result["cleanup_observation"] = observed_child_exit(case / "native-observations.jsonl")
            result["checks"]["managed_native_child_exited"] = result["cleanup_observation"]["status"] == "observed_absent"
        write_json(case / "result.json", result)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--claude", default=shutil.which("claude"))
    add_claude_version(parser)
    parser.add_argument("--buddy", type=pathlib.Path)
    parser.add_argument("--adapter", type=pathlib.Path)
    parser.add_argument("--trace-native", action="store_true", help="forward native stdio unchanged through a temporary metadata observer")
    args = parser.parse_args()
    exit_on_signals()  # F-35: TERM/HUP still run the finally blocks
    root = pathlib.Path(tempfile.mkdtemp(prefix="buddy-claude-native-", dir="/private/tmp"))
    print("ROOT=" + str(root), flush=True)
    result = {"schema": "buddy.claude-qualification.v1", "root": str(root), "passed": False,
        "started_at": datetime.datetime.now(datetime.timezone.utc).isoformat(), "checks": {},
        "provider": "deterministic loopback Messages fixture; no real inference",
        "unqualified": ["real provider workers", "mixed fleet", "resume and compaction", "coordinator succession",
            "native teammates", "optional or managed Mods outside the pinned builtin profile", "native peer socket held/refused wake",
            "native custom socket location", "native session with absent inbox endpoint"],
        "sources": ["https://platform.claude.com/docs/en/build-with-claude/streaming"]}
    checks = result["checks"]
    server = None
    server_started = False
    adapter = None
    exit_code = 1
    try:
        result["qualification_candidates"] = [bind_claude(args)]
        claude = pathlib.Path(args.claude)
        env = clean_env()
        work = root / "work"
        state = root / "claude"
        bindir = root / "bin"
        for path in (work, state, bindir, root / "tmp"):
            path.mkdir()
        # An explicit inert key prevents account/keychain credentials being used
        # for inference. Native nonessential traffic and updating are disabled.
        native_env = dict(env, CLAUDE_CONFIG_DIR=str(state), TMPDIR=str(root / "tmp"),
            ANTHROPIC_API_KEY="buddy-loopback-fixture-not-a-provider-key",
            CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1", DISABLE_AUTOUPDATER="1",
            DISABLE_TELEMETRY="1", DISABLE_ERROR_REPORTING="1", CLAUDE_CODE_DISABLE_AUTO_MEMORY="1")
        result["native_version"] = run([claude, "--version"], work, native_env).strip()
        if result["native_version"] != args.claude_version + " (Claude Code)":
            raise RuntimeError("installed Claude version does not match this qualification contract")
        result["native_sha256"] = digest(claude.resolve())
        result["source_head"] = run(["git", "--no-optional-locks", "rev-parse", "HEAD"], REPO, env).strip()
        result["source_status"] = run(["git", "--no-optional-locks", "status", "--porcelain"], REPO, env).splitlines()
        buddy = args.buddy.resolve() if args.buddy else bindir / "buddy"
        binary = args.adapter.resolve() if args.adapter else bindir / "buddy-adapter"
        manifest = source_manifest(env)
        write_json(root / "source-manifest.json", manifest)
        result["source_manifest_sha256"] = digest(root / "source-manifest.json")
        result["binary_origins"] = {"buddy": "provided artifact" if args.buddy else "built from captured source manifest",
                                    "adapter": "provided artifact" if args.adapter else "built from captured source manifest"}
        # go build stamps VCS state by running `git status` in the real
        # checkout, which rewrites its index (measured, F-34): no optional locks.
        buildenv = dict(env, GOCACHE="/private/tmp/buddy-claude-go-cache", GOPROXY="off", GOSUMDB="off", GIT_OPTIONAL_LOCKS="0")
        for supplied, target, package in ((args.buddy, buddy, "./cmd/buddy"), (args.adapter, binary, "./cmd/buddy-adapter")):
            if not supplied:
                run(["go", "build", "-o", target, package], REPO, buildenv, timeout=120)
        if source_manifest(env) != manifest:
            raise RuntimeError("source changed while binaries were prepared; retry from a stable snapshot")
        result["buddy_sha256"] = digest(buddy)
        result["adapter_sha256"] = digest(binary)
        result["go_version"] = run(["go", "version"], REPO, buildenv).strip()
        run(["git", "init", "-q", work], root, env)
        run([buddy, "init"], work, env)
        run([buddy, "hello", "--session", "fixture-holder"], work, env)
        run([buddy, "claim", "held", "--session", "fixture-holder", "--scope", "guarded.txt",
             "--desc", "isolated native qualification"], work, env)
        (work / "guarded.txt").write_text("original\n")
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), MessagesFixture)
        server.requests = []
        server.unexpected = []
        server.sequence = [("Write", {"file_path": str(work / name), "content": body}) for name, body in
            (("guarded.txt", "conflicting-write\n"), ("allowed.txt", "allowed-control\n"),
             ("operator-denied.txt", "must-not-exist\n"))]
        # D-078's open question: what a SUCCESSFUL Edit's tool_result looks like
        # natively (is_error explicitly false, or omitted like Write's), and so
        # whether the adapter renews from it. One Edit of the file the allowed
        # Write created, appended last so toolu_buddy_0..2 keep their meaning.
        server.sequence.append(("Edit", {"file_path": str(work / "allowed.txt"), "old_string": "allowed-control",
                                         "new_string": "allowed-edited"}))
        native_env["ANTHROPIC_BASE_URL"] = f"http://127.0.0.1:{server.server_address[1]}"
        receipt = root / "worker.json"
        # main.go captures POSIX single quoting even for uncomplicated paths.
        gate = f"'{binary}' hook --receipt '{receipt}' --buddy '{buddy}'"
        settings = root / "settings.json"
        write_json(settings, {"enabledPlugins": {}, "hooks": {"PreToolUse": [{"matcher": "^(Edit|Write|NotebookEdit)$",
             "hooks": [{"type": "command", "command": gate, "timeout": 10}]}]}})
        launch = {"worktree": str(work), "permission_mode": "workspace-write", "hook_config": str(settings),
            "harness_version": args.claude_version, "gate_command": gate,
            "hooks": {"configured": "observed", "trusted": "unknown", "observed": "unknown", "mods": "builtin-only-" + args.claude_version}}
        result["configuration_evidence"] = {"settings_sha256": digest(settings), "settings_sources": [],
            "config_dir_initially_empty": not any(state.iterdir()), "enabled_plugins": [],
            "managed_policy": "not changed; effective native settings are checked before input",
            "mods_profile": "builtin-only-" + args.claude_version,
            "mods_evidence": "adapter checks native reload inventory and effective settings before lifecycle capture; actual gate denial is measured separately"}
        write_json(root / "launch.json", launch)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        server_started = True
        result["managed_hook_refusal"] = managed_hook_refusal(root, binary, buddy, claude, work, native_env, launch, server)
        checks.update(result["managed_hook_refusal"]["checks"])
        if not all(checks.values()):
            raise RuntimeError("native managed hook refusal qualification failed")
        executable = claude
        if args.trace_native:
            executable = bindir / "claude-observer"
            native_observer(executable, claude, root / "native-observations.jsonl")
            result["native_metadata_observer"] = {"path": str(executable), "sha256": digest(executable), "transport": "unchanged forwarded stdout; stdin inherited directly"}
        adapter = Adapter([binary, "--harness", "claude", "--executable", executable, "--buddy", buddy,
            "--launch", root / "launch.json", "--receipt", receipt, "--label", "claude-native-fixture"], work, native_env, root)
        start = adapter.wait(lambda x: x.get("operation") == "start")
        identity = start["identity"]
        checks["captured_native_identity"] = bool(identity["session_id"] and identity["incarnation"])
        checks["handshake_without_inference"] = len(server.requests) == 0
        checks["unsupported_telemetry_remains_unknown"] = start["capabilities"]["telemetry"]["support"] == "unknown"
        renewal_capability = start["capabilities"].get("claim_renewal", {}).get("support") == "experimental"
        # F-26 (D-078): the worker's own claims. allowed.txt is written and its
        # claim must renew; operator-denied.txt is attempted and denied, and
        # control.txt is never named, so theirs must not; the holder's
        # guarded.txt claim stands behind the gate-denied write.
        for slug, scope in (("worker-allowed", "allowed.txt"), ("worker-denied", "operator-denied.txt"), ("worker-control", "control.txt")):
            run([buddy, "claim", slug, "--session", identity["session_id"], "--scope", scope, "--desc", "renewal qualification only"], work, env)
        renewal_before = claim_renewals(work, root)
        time.sleep(1.2)  # renewed is whole seconds
        sender = json.loads(run([buddy, "lifecycle", "begin", "--session", "fixture-coordinator"], work, env))
        queued = json.loads(run([buddy, "adapter", "queue", "--session", sender["session_id"], "--incarnation", sender["incarnation"],
            "--to", identity["session_id"], "--to-incarnation", identity["incarnation"], "--body", "Fixture assignment is peer data."], work, env))
        message_id = queued["delivery"]["message_id"]
        request = {"schema": SCHEMA, "id": "wake-1", "operation": "send", "identity": identity, "message_id": message_id}
        adapter.send(request)
        wake = adapter.wait(lambda x: x.get("request_id") == "wake-1")
        checks["native_wake_replay_accepted"] = wake["wake_delivery"]["state"] == "delivered"
        approvals, seen = [], []
        for expected, decision, rid, check in (("allowed.txt", "allow", "operator-allow", "allowed.txt_waiting_approval"),
                                               ("operator-denied.txt", "deny", "operator-deny", "operator-denied.txt_waiting_approval"),
                                               ("allowed.txt", "allow", "operator-allow-edit", "edit_waiting_approval")):
            event = adapter.wait(lambda x: x.get("native_event", {}).get("kind") == "approval" and
                                 x["native_event"]["approval"]["id"] not in seen)
            approval = event["native_event"]["approval"]
            seen.append(approval["id"])
            if pathlib.Path(approval["detail"]["input"]["file_path"]).name != expected:
                raise RuntimeError("unexpected approval tool or path: " + json.dumps(approval))
            approvals.append(approval)
            checks[check] = event["state"] == {"state": "observed", "detail": "waiting_for_approval"}
            operator_request = {"schema": SCHEMA, "id": rid, "operation": "approve", "identity": identity,
                "approval": {"id": approval["id"], "native_session_id": approval["native_session_id"],
                             "turn_id": approval["turn_id"], "decision": decision}}
            if rid == "operator-allow":
                peer_request = dict(operator_request, id="peer-approval-refused")
                adapter.send(peer_request)
                peer = adapter.wait(lambda x: x.get("request_id") == "peer-approval-refused", permit_error=True)
                checks["peer_cannot_approve"] = "unsupported control operation" in peer.get("error", "")
            adapter.send(operator_request, operator=True)
            approved = adapter.wait(lambda x: x.get("request_id") == rid)
            checks[rid.replace("-", "_") + "_submitted"] = approved["approval_delivery"]["state"] == "submitted"
        native_result = adapter.wait(lambda x: x.get("native_event", {}).get("kind") == "result")
        checks["native_result_separate_from_completion"] = native_result["completion"]["state"] == "unknown" and bool(native_result["native_event"]["result_ref"])
        checks["conflicting_write_denied"] = (work / "guarded.txt").read_text() == "original\n"
        tool_outputs = {x["tool_use_id"]: x for request in server.requests for x in request["tool_outputs"]}
        # The Write created the file and the Edit then changed it; an Edit
        # cannot succeed on a file the Write did not create.
        checks["positive_write_allowed"] = (work / "allowed.txt").read_text() == "allowed-edited\n"
        checks["positive_edit_allowed"] = "toolu_buddy_3" in tool_outputs and not tool_outputs["toolu_buddy_3"].get("is_error")
        checks["operator_denied_write_absent"] = not (work / "operator-denied.txt").exists()
        checks["denial_names_buddy_claim"] = "inside scope" in str(tool_outputs["toolu_buddy_0"].get("content")) and bool(tool_outputs["toolu_buddy_0"].get("is_error"))
        checks["operator_denial_reached_native"] = "Denied by the operator control interface" in str(tool_outputs["toolu_buddy_2"].get("content"))
        inspection = json.loads(run([buddy, "adapter", "inspect", "--session", identity["session_id"], "--incarnation", identity["incarnation"]], work, env))
        write_json(root / "ledger-inspection.json", inspection)
        result["endpoint_observation"] = inspection["endpoint"]
        endpoint = result["endpoint_observation"]
        checks["native_receiver_endpoint_captured"] = (endpoint.get("status") == "observed"
            and endpoint.get("pid", 0) > 1 and endpoint.get("born", 0) > 0)
        inbox = json.loads(run([buddy, "adapter", "inbox", "--session", identity["session_id"], "--incarnation", identity["incarnation"]], work, env))
        checks["assignment_still_queued_after_result"] = inbox["messages"]["count"] == 1
        idle = adapter.wait(lambda x: x.get("native_event", {}).get("kind") == "idle", timeout=10)
        checks["native_idle_translated"] = idle["state"]["detail"] == "idle"
        checks["idle_clears_pending_approvals"] = idle["pending_approval"]["state"] == "observed_absent"
        checks["result_bound_to_accepted_input"] = native_result["native_event"].get("turn_id") == wake["wake_delivery"]["turn_id"]
        # Renewal verdicts are kept apart from `checks`, whose exact set
        # check-claude-gate.py requires, and are folded into `passed` below.
        renewal_checks = {"claim_renewal_capability_declared": renewal_capability}
        result["renewal"] = renewal_evidence(work, root, receipt, renewal_before, identity, renewal_checks, args.claude_version)
        result["renewal"]["checks"] = renewal_checks
        result["edit_measurement"] = edit_shape(root / "native-observations.jsonl", str(work / "allowed.txt"))
        before = len(server.requests)
        adapter.send(dict(request, id="wake-duplicate"))
        adapter.wait(lambda x: x.get("request_id") == "wake-duplicate")
        checks["duplicate_wake_not_replayed"] = len(server.requests) == before
        delivery = json.loads(run([buddy, "adapter", "delivered", "--session", identity["session_id"], "--incarnation", identity["incarnation"], "--message", str(message_id)], work, env))["delivery"]
        checks["body_delivery_explicit"] = delivery["state"] == "delivered" and delivery["acknowledged"]["status"] == "unknown" and delivery["completed"]["status"] == "unknown"
        followup = json.loads(run([buddy, "adapter", "queue", "--session", sender["session_id"], "--incarnation", sender["incarnation"],
            "--to", identity["session_id"], "--to-incarnation", identity["incarnation"], "--body", "Fixture second idle assignment."], work, env))
        adapter.send(dict(request, id="wake-2", message_id=followup["delivery"]["message_id"]))
        second_wake = adapter.wait(lambda x: x.get("request_id") == "wake-2")
        second_result = adapter.wait(lambda x: x.get("native_event", {}).get("kind") == "result")
        second_idle = adapter.wait(lambda x: x.get("native_event", {}).get("kind") == "idle", timeout=10)
        checks["second_idle_assignment_accepted"] = (second_wake["wake_delivery"]["state"] == "delivered"
            and second_wake["wake_delivery"]["turn_id"] != wake["wake_delivery"]["turn_id"])
        checks["second_result_has_own_input_identity"] = (second_result["native_event"].get("turn_id") == second_wake["wake_delivery"]["turn_id"]
            and second_result["native_event"]["result_ref"] != native_result["native_event"]["result_ref"]
            and second_idle["state"]["detail"] == "idle")
        checks["fixture_only_expected_requests"] = not server.unexpected
        result["passed"] = all(checks.values()) and all(renewal_checks.values())
        exit_code = 0 if result["passed"] else 1
    except BaseException as exc:
        result["failure"] = str(exc)
        (root / "failure.txt").write_text(traceback.format_exc())
    finally:
        if adapter:
            try:
                adapter.close()
            except BaseException as exc:
                result["cleanup_failure"] = str(exc)
                result["passed"] = False
                exit_code = 1
            write_json(root / "adapter-events.json", adapter.events)
            write_json(root / "unparsed-output.json", adapter.unparsed)
            result["adapter_exit"] = adapter.process.returncode
            checks["adapter_exited_successfully"] = adapter.process.returncode == 0
            checks["adapter_stdout_fully_parsed"] = not adapter.unparsed
            trace = root / "native-observations.jsonl"
            if args.trace_native:
                result["cleanup_observation"] = observed_child_exit(trace)
                checks["observed_native_children_exited"] = result["cleanup_observation"]["status"] == "observed_absent"
        if server:
            # server_close works even if failure happened before serve_forever.
            if server_started:
                server.shutdown()
            server.server_close()
            write_json(root / "provider-observations.json", server.requests)
        result["fixture_sha256"] = digest(__file__)
        # F-37: every file check-claude-gate.py reads, bound to this receipt.
        result["evidence_sha256"] = {name: digest(root / name) for name in EVIDENCE if (root / name).is_file()}
        result["finished_at"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        renewal_ok = bool(result.get("renewal", {}).get("checks")) and all(result["renewal"]["checks"].values())
        result["passed"] = bool(result["passed"] and all(checks.values()) and renewal_ok and
                                not result.get("failure") and not result.get("cleanup_failure"))
        exit_code = 0 if result["passed"] else 1
        write_json(root / "result.json", result)
        print(json.dumps({"passed": result["passed"], "checks": checks, "renewal_checks": result.get("renewal", {}).get("checks"),
                          "failure": result.get("failure")}, indent=2), flush=True)
        print("RECEIPT=" + str(root / "result.json"), flush=True)
    return exit_code


if __name__ == "__main__":
    sys.exit(main())
