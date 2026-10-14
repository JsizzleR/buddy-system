#!/usr/bin/env python3
"""Opt-in native custom/absent receiving endpoint qualification on macOS.

The custom case uses documented CLAUDE_CODE_TMPDIR and measures the exported
socket, rather than guessing from a PID or trusting shell TMPDIR. The absent
case adds a restrictive sandbox to ONLY the owned native child: socket binding
fails, while hooks and the loopback provider connection remain available. Bare
mode cannot prove this contract because it disables the Buddy hook as well.

Each case drives the real adapter and receipt-bound hook, checks a conflicting
Write and an allowed control, and leaves the Buddy assignment queued. Nothing
connects to a peer socket. This qualifies endpoint observation and owned-stream
input, not native peer delivery, a real provider, or a mixed fleet.
"""

import argparse
import ctypes
import datetime
import http.server
import importlib.util
import json
import os
import pathlib
import shutil
import stat
import subprocess
import sys
import tempfile
import threading
import traceback


HELPERS = pathlib.Path(__file__).with_name("qualify-claude.py")
sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("claude_fixture", HELPERS)
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)


def process_birth(pid):
    """Independent kernel birth evidence, matching Darwin proc_bsdinfo's ABI.

    The system SDK's sys/proc_info.h defines PROC_PIDTBSDINFO=3 and these
    fields. Only the explicitly owned PID is read; a failed observation is
    unknown and cannot establish either binding or cleanup.
    """
    class BSDInfo(ctypes.Structure):
        _fields_ = [("prefix", ctypes.c_uint32 * 12),
                    ("comm", ctypes.c_char * 16), ("name", ctypes.c_char * 32),
                    ("middle", ctypes.c_uint32 * 6),
                    ("seconds", ctypes.c_uint64), ("microseconds", ctypes.c_uint64)]
    value = BSDInfo()
    lib = ctypes.CDLL("/usr/lib/libproc.dylib", use_errno=True)
    fn = lib.proc_pidinfo
    fn.argtypes = [ctypes.c_int, ctypes.c_int, ctypes.c_uint64, ctypes.c_void_p, ctypes.c_int]
    fn.restype = ctypes.c_int
    count = fn(pid, 3, 0, ctypes.byref(value), ctypes.sizeof(value))
    if count != ctypes.sizeof(value) or value.prefix[3] != pid:
        raise RuntimeError(f"cannot observe birth of owned native PID {pid}: errno {ctypes.get_errno()}")
    return value.seconds * 1_000_000 + value.microseconds


def native_observer(path, native, trace, deny_bind):
    # The sandbox wrapper execs Claude without changing its PID. Stdio and all
    # native frames remain untouched; the observer records only selected keys.
    command = [str(native)]
    if deny_bind:
        command = ["/usr/bin/sandbox-exec", "-p", "(version 1) (allow default) (deny network-bind)",
                   sys.executable, "-c", "import os,sys; os.execv(sys.argv[1], [\"claude\"] + sys.argv[2:])", str(native)]
    path.write_text("#!" + sys.executable + "\n" + '''import json,subprocess,sys
command=COMMAND if sys.argv[1:]!=['--version'] else [NATIVE]
p=subprocess.Popen((["claude"] if command == [NATIVE] else command)+sys.argv[1:],
 executable=NATIVE if command == [NATIVE] else None,stdin=sys.stdin,stdout=subprocess.PIPE,stderr=sys.stderr)
with open(TRACE,'a') as log:
 log.write(json.dumps({'type':'buddy_fixture_native_process','pid':p.pid,'version_probe':sys.argv[1:]==['--version']})+'\\n');log.flush()
 for line in p.stdout:
  try:
   v=json.loads(line)
   keys=('type','subtype','session_id','uuid','isReplay','user_message_uuid','user_message_uuids','queued_turn_count','messaging_socket_path')
   log.write(json.dumps({k:v[k] for k in keys if k in v})+'\\n');log.flush()
  except ValueError:pass
  sys.stdout.buffer.write(line);sys.stdout.buffer.flush()
sys.exit(p.wait())
'''.replace("COMMAND", repr(command)).replace("NATIVE", repr(str(native))).replace("TRACE", repr(str(trace))))
    path.chmod(0o700)


def qualify_case(root, name, deny_bind, buddy, binary, claude, env, version):
    case = root / name
    case.mkdir()
    result = {"case": name, "passed": False, "checks": {},
              "absence_mechanism": "owned native child sandbox denies network-bind" if deny_bind else None}
    checks = result["checks"]
    adapter = server = None
    started = False
    endpoint = {}
    try:
        work, config, native_tmp = (case / part for part in ("work", "config", "native-tmp"))
        for path in (work, config, native_tmp):
            path.mkdir()
        fixture.run(["git", "init", "-q", work], case, env)
        fixture.run([buddy, "init"], work, env)
        fixture.run([buddy, "hello", "--session", "fixture-holder"], work, env)
        fixture.run([buddy, "claim", "fixture-held", "--session", "fixture-holder", "--scope", "guarded.txt",
                     "--desc", "owned native endpoint fixture"], work, env)
        (work / "guarded.txt").write_text("original\n")
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), fixture.MessagesFixture)
        server.requests, server.unexpected = [], []
        server.sequence = [("Write", {"file_path": str(work / file), "content": body})
                           for file, body in (("guarded.txt", "must-not-land\n"), ("allowed.txt", "control\n"))]
        threading.Thread(target=server.serve_forever, daemon=True).start()
        started = True
        native_env = dict(env, CLAUDE_CONFIG_DIR=str(config), CLAUDE_CODE_TMPDIR=str(native_tmp),
                          TMPDIR=str(native_tmp), ANTHROPIC_API_KEY="buddy-loopback-fixture-not-a-provider-key",
                          ANTHROPIC_BASE_URL=f"http://127.0.0.1:{server.server_address[1]}",
                          CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1", DISABLE_AUTOUPDATER="1",
                          DISABLE_TELEMETRY="1", DISABLE_ERROR_REPORTING="1", CLAUDE_CODE_DISABLE_AUTO_MEMORY="1")
        receipt, settings = case / "worker.json", case / "settings.json"
        gate = f"'{binary}' hook --receipt '{receipt}' --buddy '{buddy}'"
        fixture.write_json(settings, {"enabledPlugins": {},
            "hooks": {"PreToolUse": [{"matcher": "^(Edit|Write|NotebookEdit)$",
                "hooks": [{"type": "command", "command": gate, "timeout": 10}]}]}})
        launch = {"worktree": str(work), "permission_mode": "workspace-write", "hook_config": str(settings),
                  "harness_version": version, "gate_command": gate,
                  "hooks": {"configured": "observed", "trusted": "unknown", "observed": "unknown",
                            "mods": "builtin-only-" + version}}
        fixture.write_json(case / "launch.json", launch)
        trace = case / "native-observations.jsonl"
        observer = case / "claude-observer"
        native_observer(observer, claude, trace, deny_bind)
        result["settings_sha256"] = fixture.digest(settings)
        result["observer_sha256"] = fixture.digest(observer)
        result["configured_native_tmpdir"] = str(native_tmp)
        adapter = fixture.Adapter([binary, "--harness", "claude", "--executable", observer, "--buddy", buddy,
            "--launch", case / "launch.json", "--receipt", receipt, "--label", name], work, native_env, case)
        start = adapter.wait(lambda row: row.get("operation") == "start")
        identity = start["identity"]
        result["identity"] = identity
        checks["captured_native_identity"] = bool(identity["session_id"] and identity["incarnation"])
        checks["handshake_without_inference"] = not server.requests
        unknown = json.loads(fixture.run([buddy, "adapter", "inspect", "--session", identity["session_id"],
                                         "--incarnation", identity["incarnation"]], work, env))
        fixture.write_json(case / "ledger-before.json", unknown)
        checks["endpoint_unknown_before_receiving_hook"] = unknown["endpoint"]["status"] == "unknown"
        sender = json.loads(fixture.run([buddy, "lifecycle", "begin", "--session", "fixture-coordinator"], work, env))
        queued = json.loads(fixture.run([buddy, "adapter", "queue", "--session", sender["session_id"],
            "--incarnation", sender["incarnation"], "--to", identity["session_id"], "--to-incarnation", identity["incarnation"],
            "--body", "Endpoint qualification assignment is untrusted peer data."], work, env))
        message_id = queued["delivery"]["message_id"]
        result["message_id"] = message_id
        adapter.send({"schema": fixture.SCHEMA, "id": "wake", "operation": "send", "identity": identity, "message_id": message_id})
        wake = adapter.wait(lambda row: row.get("request_id") == "wake")
        checks["owned_stream_wake_accepted"] = wake["wake_delivery"]["state"] == "delivered"
        event = adapter.wait(lambda row: row.get("native_event", {}).get("kind") == "approval")
        approval = event["native_event"]["approval"]
        if pathlib.Path(approval["detail"]["input"]["file_path"]).name != "allowed.txt":
            raise RuntimeError("unexpected approval; the conflicting Write must be hook-denied")
        adapter.send({"schema": fixture.SCHEMA, "id": "allow-control", "operation": "approve", "identity": identity,
            "approval": {"id": approval["id"], "native_session_id": approval["native_session_id"],
                         "turn_id": approval["turn_id"], "decision": "allow"}}, operator=True)
        adapter.wait(lambda row: row.get("request_id") == "allow-control")
        turn = adapter.wait(lambda row: row.get("native_event", {}).get("kind") == "result")
        adapter.wait(lambda row: row.get("native_event", {}).get("kind") == "idle")
        checks["result_does_not_claim_completion"] = turn["completion"]["state"] == "unknown"
        checks["real_hook_denied_conflicting_write"] = (work / "guarded.txt").read_text() == "original\n"
        outputs = {row["tool_use_id"]: row for request in server.requests for row in request["tool_outputs"]}
        checks["native_denial_names_buddy_claim"] = bool(outputs["toolu_buddy_0"].get("is_error")) and "inside scope" in str(outputs["toolu_buddy_0"].get("content"))
        checks["allowed_write_positive_control"] = (work / "allowed.txt").read_text() == "control\n"
        view = json.loads(fixture.run([buddy, "adapter", "inspect", "--session", identity["session_id"],
                                      "--incarnation", identity["incarnation"]], work, env))
        fixture.write_json(case / "ledger-inspection.json", view)
        endpoint = view["endpoint"]
        result["endpoint"] = endpoint
        observations = [json.loads(line) for line in trace.read_text().splitlines()]
        pids = [row["pid"] for row in observations if row.get("type") == "buddy_fixture_native_process" and not row["version_probe"]]
        if len(pids) != 1:
            raise RuntimeError("observer did not capture exactly one native worker process")
        native_pid = pids[0]
        result["native_process"] = {"pid": native_pid, "born": process_birth(native_pid)}
        checks["receiving_native_identity_matches"] = any(row.get("session_id") == identity["session_id"] for row in observations)
        checks["endpoint_bound_to_exact_native_birth"] = (endpoint.get("pid") == native_pid and
                                                         endpoint.get("born") == result["native_process"]["born"])
        sockets = subprocess.run(["/usr/sbin/lsof", "-a", "-p", str(native_pid), "-U", "-Fn"],
                                 capture_output=True, text=True, timeout=10)
        if sockets.returncode not in (0, 1) or sockets.stderr:
            raise RuntimeError("PID-scoped socket inspection failed: " + sockets.stderr)
        names = [line[1:] for line in sockets.stdout.splitlines() if line.startswith("n")]
        result["native_unix_socket_names"] = names
        result["native_process_birth_after_socket_observation"] = process_birth(native_pid)
        fixture.write_json(case / "socket-observation.json", {"pid": native_pid,
            "born_before": result["native_process"]["born"],
            "born_after": result["native_process_birth_after_socket_observation"],
            "exit": sockets.returncode, "stdout": sockets.stdout, "stderr": sockets.stderr})
        checks["native_process_alive_across_socket_observation"] = result["native_process_birth_after_socket_observation"] == result["native_process"]["born"]
        if deny_bind:
            checks["receiving_endpoint_observed_absent"] = endpoint["status"] == "observed_absent" and not endpoint.get("socket")
            checks["native_socket_actually_absent"] = not names
        else:
            checks["receiving_custom_endpoint_observed"] = (endpoint["status"] == "observed" and
                pathlib.Path(endpoint.get("socket", "")).is_relative_to(native_tmp) and
                endpoint["socket"] != f"/tmp/cc-socks/{native_pid}.sock")
            checks["custom_export_is_native_bound_socket"] = (endpoint.get("socket") in names and
                stat.S_ISSOCK(os.stat(endpoint["socket"]).st_mode))
        inbox = json.loads(fixture.run([buddy, "adapter", "inbox", "--session", identity["session_id"],
                                       "--incarnation", identity["incarnation"], "--message", str(message_id)], work, env))
        fixture.write_json(case / "inbox.json", inbox)
        checks["exact_assignment_remains_queued"] = (inbox["messages"]["count"] == 1 and
            inbox["messages"]["items"][0]["message_id"] == message_id and inbox["messages"]["items"][0]["state"] == "queued")
        checks["fixture_only_expected_requests"] = not server.unexpected
        result["passed"] = all(checks.values())
    except BaseException as exc:
        result["failure"] = str(exc)
        (case / "failure.txt").write_text(traceback.format_exc())
    finally:
        if adapter:
            try:
                adapter.close()
            except BaseException as exc:
                result["cleanup_failure"] = str(exc)
            fixture.write_json(case / "adapter-events.json", adapter.events)
            checks["adapter_exited_successfully"] = adapter.process.returncode == 0
            checks["adapter_stdout_fully_parsed"] = not adapter.unparsed
            result["cleanup_observation"] = fixture.observed_child_exit(case / "native-observations.jsonl")
            checks["owned_native_children_exited"] = result["cleanup_observation"]["status"] == "observed_absent"
            if endpoint.get("pid"):
                # Group TERM may leave a Unix socket file. Its path alone is
                # never a live receiver: retain that observation separately
                # and qualify cleanup against the captured receiver birth.
                sampled = fixture.process_start(endpoint["pid"])
                expected = [endpoint["born"] // 1000000, endpoint["born"] % 1000000]
                result["endpoint_cleanup"] = {"pid": endpoint["pid"], "born": endpoint["born"],
                    "sampled_start": sampled, "eligible_delivery": sampled == expected,
                    "socket_file_present": bool(endpoint.get("socket") and pathlib.Path(endpoint["socket"]).exists())}
                checks["exact_receiver_absent_after_exit"] = sampled != expected
        if server:
            if started:
                server.shutdown()
            server.server_close()
            fixture.write_json(case / "provider-observations.json", server.requests)
        result["passed"] = bool(result["passed"] and all(checks.values()) and not result.get("cleanup_failure"))
        fixture.write_json(case / "result.json", result)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--claude", default=shutil.which("claude"))
    parser.add_argument("--claude-version", choices=fixture.QUALIFICATION_VERSIONS, default=fixture.VERSION)
    parser.add_argument("--buddy", type=pathlib.Path)
    parser.add_argument("--adapter", type=pathlib.Path)
    args = parser.parse_args()
    # D-080: the binaries this run was given are its candidates; every adapter it starts is bound to them.
    sys.modules["buddy_admission_lib"].bind_args(args)
    fixture.exit_on_signals()  # F-35: TERM/HUP still run the finally blocks
    root = pathlib.Path(tempfile.mkdtemp(prefix="buddy-claude-endpoints-", dir="/private/tmp"))
    print("ROOT=" + str(root), flush=True)
    result = {"schema": "buddy.claude-endpoint-qualification.v1", "root": str(root), "passed": False,
              "claude_version": args.claude_version,
              "started_at": datetime.datetime.now(datetime.timezone.utc).isoformat(), "cases": [],
              "provider": "deterministic loopback Messages fixture; no real inference",
              "sources": ["https://code.claude.com/docs/en/env-vars#claude-code-tmpdir",
                          "https://code.claude.com/docs/en/cross-session-messaging#the-sessions-inbox-socket"],
              "unqualified": ["native peer socket send", "real provider workers", "mixed fleet", "native teammates",
                              "resume and compaction", "coordinator succession"]}
    try:
        if sys.platform != "darwin" or not pathlib.Path("/usr/bin/sandbox-exec").is_file():
            raise RuntimeError("this qualification requires macOS child sandbox and native process evidence")
        if not args.claude:
            raise RuntimeError("provide --claude for the pinned native executable")
        claude, env = pathlib.Path(args.claude).absolute(), fixture.clean_env()
        result["fixture_sha256"] = fixture.digest(__file__)
        result["helpers_sha256"] = fixture.digest(HELPERS)
        result["native_version"] = fixture.run([claude, "--version"], root,
            dict(env, CLAUDE_CONFIG_DIR=str(root / "version-config"))).strip()
        if result["native_version"] != args.claude_version + " (Claude Code)":
            raise RuntimeError("native version differs from the qualification contract")
        result["native_sha256"] = fixture.digest(claude.resolve())
        fixture.write_json(root / "native-version.json", {"version": result["native_version"],
            "executable_sha256": result["native_sha256"]})
        result["source_head"] = fixture.run(["git", "--no-optional-locks", "rev-parse", "HEAD"], fixture.REPO, env).strip()
        result["source_status"] = fixture.run(["git", "--no-optional-locks", "status", "--porcelain"], fixture.REPO, env).splitlines()
        manifest = fixture.source_manifest(env)
        fixture.write_json(root / "source-manifest.json", manifest)
        result["source_manifest_sha256"] = fixture.digest(root / "source-manifest.json")
        buddy = args.buddy.resolve() if args.buddy else root / "buddy"
        binary = args.adapter.resolve() if args.adapter else root / "buddy-adapter"
        result["binary_origins"] = {"buddy": "provided artifact" if args.buddy else "built from captured source manifest",
                                    "adapter": "provided artifact" if args.adapter else "built from captured source manifest"}
        buildenv = dict(env, GOCACHE="/private/tmp/buddy-claude-go-cache", GOPROXY="off", GOSUMDB="off", GIT_OPTIONAL_LOCKS="0")
        for supplied, target, package in ((args.buddy, buddy, "./cmd/buddy"), (args.adapter, binary, "./cmd/buddy-adapter")):
            if not supplied:
                fixture.run(["go", "build", "-o", target, package], fixture.REPO, buildenv, timeout=120)
        if fixture.source_manifest(env) != manifest:
            raise RuntimeError("source changed while binaries were prepared; retry from a stable snapshot")
        result["buddy_sha256"], result["adapter_sha256"] = fixture.digest(buddy), fixture.digest(binary)
        result["binary_paths"] = {"buddy": str(buddy), "adapter": str(binary)}
        result["go_version"] = fixture.run(["go", "version"], fixture.REPO, buildenv).strip()
        for name, deny_bind in (("custom", False), ("absent", True)):
            case = qualify_case(root, name, deny_bind, buddy, binary, claude, env, args.claude_version)
            result["cases"].append(case)
            print(json.dumps({"case": name, "passed": case["passed"], "failure": case.get("failure")}), flush=True)
        if fixture.digest(HELPERS) != result["helpers_sha256"] or fixture.digest(__file__) != result["fixture_sha256"]:
            raise RuntimeError("qualification fixture changed during execution; retry from a stable snapshot")
        result["passed"] = all(case["passed"] for case in result["cases"])
    except BaseException as exc:
        result["failure"] = str(exc)
        (root / "failure.txt").write_text(traceback.format_exc())
    finally:
        # Retain original observations for the offline checker; its verdict is
        # derived from these files, never from the fixture's passed booleans.
        result["evidence_sha256"] = {str(path.relative_to(root)): fixture.digest(path)
            for case in ("custom", "absent") for path in (root / case).glob("*")
            if path.is_file() and path.suffix in (".json", ".jsonl")}
        if (root / "native-version.json").exists():
            result["evidence_sha256"]["native-version.json"] = fixture.digest(root / "native-version.json")
        result["finished_at"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        fixture.write_json(root / "result.json", result)
        print("RECEIPT=" + str(root / "result.json"), flush=True)
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
