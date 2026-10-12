#!/usr/bin/env python3
"""Opt-in native SendMessage qualification; all recipients belong to this run.

Run: python3 scripts/qualify-claude-peers.py [--buddy PATH --claude PATH]

Two disposable native processes at a time exercise accept, hold and refuse.
The provider emits only SendMessage addressed to a receiver whose SessionStart
hook exported its own socket. No discovery tool or invented socket wire format
is used. This is native peer transport evidence, not buddy-adapter owned-stream
delivery, body ingestion, work completion, real inference or mixed-fleet proof.
Every attempt preserves result.json under /private/tmp, including failures.
"""

import argparse
import datetime
import http.server
import importlib.util
import json
import os
import pathlib
import queue
import shlex
import shutil
import signal
import stat
import subprocess
import sys
import tempfile
import threading
import time
import traceback
import uuid


sys.dont_write_bytecode = True
HELPER_PATH = pathlib.Path(__file__).with_name("qualify-claude.py")
SPEC = importlib.util.spec_from_file_location("buddy_claude_fixture", HELPER_PATH)
common = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(common)
REPO = pathlib.Path(__file__).resolve().parent.parent


def texts(value):
    """Inspect in memory only; prompts and provider headers are never retained."""
    if isinstance(value, str):
        yield value
    elif isinstance(value, list):
        for item in value:
            yield from texts(item)
    elif isinstance(value, dict):
        for key in ("content", "text", "message"):
            if key in value:
                yield from texts(value[key])


def delivery_outcome(notices, recipient, message_id):
    """A transport success warns about possible refusal; it is not a refusal.

    Require the actual native notice prefix and the exact address/message pair.
    A marker echo, unrelated notice or SendMessage's generic caution proves none
    of held/refused/delivered. Pin this parser to the measured native version.
    """
    suffix = "(recipient: " + recipient + "; msg_id: " + message_id + ")"
    prefix = "[Cross-session delivery notice] Your message to another session was "
    for notice in notices:
        if notice.startswith(prefix + "held by that session " + suffix + ". NOT delivered:"):
            return "held"
        if notice.startswith(prefix + "refused " + suffix + ":"):
            return "refused"
    return "unknown"


def classifier_checks():
    """Negative controls prevent the old generic 'refus' substring false pass."""
    address, message_id = "uds:/fixture.sock", "fixture-message"
    prefix = "[Cross-session delivery notice] Your message to another session was "
    pair = "(recipient: " + address + "; msg_id: " + message_id + ")"
    refused = prefix + "refused " + pair + ": that session is not accepting cross-session messages."
    held = prefix + "held by that session " + pair + ". NOT delivered: its Claude has not seen it."
    return {
        "held_notice_positive": delivery_outcome([held], address, message_id) == "held",
        "refused_notice_positive": delivery_outcome([refused], address, message_id) == "refused",
        "request_echo_not_a_notice": delivery_outcome(["Buddy disposable refuse wake"], address, message_id) == "unknown",
        "tool_warning_not_a_notice": delivery_outcome(["in that session's inbox, not yet read by its Claude - "
            "a [Cross-session delivery notice] follows if that session holds it or refuses it"], address, message_id) == "unknown",
        "wrong_recipient_notice_ignored": delivery_outcome([refused], "uds:/different.sock", message_id) == "unknown",
        "wrong_message_notice_ignored": delivery_outcome([held], address, "different-message") == "unknown",
    }


class PeerProvider(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_POST(self):
        size = int(self.headers.get("Content-Length", "0"))
        if size > 8 << 20:
            self.send_error(413)
            return
        value = json.loads(self.rfile.read(size))
        if self.path.split("?")[0].endswith("/v1/messages/count_tokens"):
            body = b'{"input_tokens":100}'
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        route = self.path.split("/")[1]
        case = self.server.cases.get(route)
        if case is None or self.path.split("?")[0] != "/" + route + "/v1/messages":
            self.server.unexpected.append(self.path)
            self.send_error(404)
            return
        outputs = [block for msg in value.get("messages", [])
                   for block in msg.get("content", []) if isinstance(block, dict)
                   and block.get("type") == "tool_result"
                   and block.get("tool_use_id") == "toolu_buddy_peer"]
        snippets = list(texts(value.get("messages", [])))
        notices = [text for text in snippets if text.startswith("[Cross-session delivery notice]")]
        metadata = {"route": route, "tool_results": outputs,
                    "peer_marker_seen": any(case["marker"] in text for text in snippets),
                    "delivery_notices": notices,
                    "tool_names": [tool.get("name") for tool in value.get("tools", [])]}
        self.server.requests.append(metadata)
        if case["sender"] and not case["sent"]:
            tool = next((tool for tool in value.get("tools", []) if tool.get("name") == "SendMessage"), None)
            if not tool or not {"to", "message"}.issubset(tool.get("input_schema", {}).get("properties", {})):
                self.server.errors.append("Native SendMessage schema is absent or unsupported")
                self.send_error(500)
                return
            # Fail before sending if this exact address was not captured from
            # this fixture's still-owned receiver. No ListAgents fallback.
            recipient = case["recipient"]
            if recipient not in self.server.owned_recipients:
                self.server.errors.append("Recipient was not captured from an owned receiver")
                self.send_error(500)
                return
            metadata["send_schema"] = tool["input_schema"]
            metadata["recipient"] = recipient
            case["sent"] = True
            block = {"type": "tool_use", "id": "toolu_buddy_peer", "name": "SendMessage", "input": {}}
            delta = {"type": "input_json_delta", "partial_json": json.dumps({
                "to": recipient, "message": case["marker"], "summary": "Disposable Buddy native delivery qualification"})}
            reason = "tool_use"
        else:
            block = {"type": "text", "text": ""}
            delta = {"type": "text_delta", "text": "Deterministic native peer fixture complete."}
            reason = "end_turn"
        events = [
            {"type": "message_start", "message": {"id": "msg_buddy_peer", "type": "message",
                "role": "assistant", "model": value.get("model"), "content": [],
                "stop_reason": None, "stop_sequence": None, "usage": {"input_tokens": 100, "output_tokens": 1}}},
            {"type": "content_block_start", "index": 0, "content_block": block},
            {"type": "content_block_delta", "index": 0, "delta": delta},
            {"type": "content_block_stop", "index": 0},
            {"type": "message_delta", "delta": {"stop_reason": reason, "stop_sequence": None}, "usage": {"output_tokens": 1}},
            {"type": "message_stop"}]
        body = "".join("event: " + item["type"] + "\ndata: " + json.dumps(item) + "\n\n" for item in events).encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


class Native:
    def __init__(self, claude, folder, work, env, inbound):
        self.folder = folder
        self.folder.mkdir()
        self.session = str(uuid.uuid4())
        self.events = []
        self.queue = queue.Queue()
        self.pending = []
        self.stderr_bytes = 0
        self.unparsed = 0
        self.closed = False
        self.hook_file = folder / "hook.json"
        config = folder / "config"
        config.mkdir()
        hook = folder / "observe.py"
        hook.write_text("import json,os,pathlib,sys\nv=json.load(sys.stdin)\n"
            "pathlib.Path(" + repr(str(self.hook_file)) + ").write_text(json.dumps({"
            "'session_id':v.get('session_id'),'event':v.get('hook_event_name'),"
            "'socket':os.environ.get('CLAUDE_CODE_MESSAGING_SOCKET'),"
            "'parent_pid':os.getppid()}))\nprint('{}')\n")
        self.settings = {"enabledPlugins": {}, "crossSessionInbound": inbound,
            "hooks": {"SessionStart": [{"hooks": [{"type": "command",
                "command": shlex.join([sys.executable, str(hook)])}]}]}}
        common.write_json(folder / "settings.json", self.settings)
        args = [claude, "--print", "--verbose", "--input-format", "stream-json", "--output-format", "stream-json",
            "--session-id", self.session, "--replay-user-messages", "--permission-prompt-tool", "stdio",
            "--permission-mode", "manual", "--setting-sources", "", "--settings", folder / "settings.json",
            "--strict-mcp-config", "--mcp-config", '{"mcpServers":{}}', "--no-session-persistence", "--no-chrome"]
        self.process = subprocess.Popen(["claude"] + [str(arg) for arg in args[1:]], executable=str(args[0]), cwd=work,
            env=dict(env, CLAUDE_CONFIG_DIR=str(config)), stdin=subprocess.PIPE,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, start_new_session=True)
        self.reader = threading.Thread(target=self.read, daemon=True)
        self.reader.start()
        self.err_reader = threading.Thread(target=self.read_stderr, daemon=True)
        self.err_reader.start()

    def read_stderr(self):
        for line in self.process.stderr:
            self.stderr_bytes += len(line.encode())

    def read(self):
        for line in self.process.stdout:
            try:
                value = json.loads(line)
            except ValueError:
                self.unparsed += 1
                continue
            self.queue.put(value)
            entry = {key: value[key] for key in ("type", "subtype", "session_id", "uuid", "is_error",
                "messaging_socket_path", "user_message_uuid", "queued_turn_count") if key in value}
            # Retain native delivery notices, not prompt text or model context.
            notices = [text for text in texts(value) if text.startswith("[Cross-session delivery notice]")]
            if notices:
                entry["delivery_notices"] = notices
            self.events.append(entry)

    def send(self, value):
        self.process.stdin.write(json.dumps(value) + "\n")
        self.process.stdin.flush()

    def wait(self, predicate, timeout=20):
        for index, value in enumerate(self.pending):
            if predicate(value):
                return self.pending.pop(index)
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            try:
                value = self.queue.get(timeout=.1)
            except queue.Empty:
                if self.process.poll() is not None:
                    raise RuntimeError("native process exited " + str(self.process.returncode))
                continue
            if value.get("type") == "control_request":
                raise RuntimeError("Unexpected native permission request; fixture does not auto-approve")
            if predicate(value):
                return value
            self.pending.append(value)
        raise RuntimeError("native observation timed out: " + self.folder.name)

    def control(self, subtype):
        request_id = str(uuid.uuid4())
        self.send({"type": "control_request", "request_id": request_id, "request": {"subtype": subtype}})
        value = self.wait(lambda item: item.get("response", {}).get("request_id") == request_id)
        response = value["response"]
        if response.get("subtype") != "success":
            raise RuntimeError("Native control refused: " + subtype)
        return response.get("response", {})

    def start(self):
        # Keep fallible process observations after the constructor returns, so
        # the caller already owns the handle and can always close this process.
        self.process_start = subprocess.run(["/bin/ps", "-p", str(self.process.pid), "-o", "lstart="],
            capture_output=True, text=True, timeout=5).stdout.strip()
        self.control("initialize")
        effective = self.control("get_settings").get("effective", {})
        hook = json.loads(self.hook_file.read_text())
        if hook.get("event") != "SessionStart" or hook.get("session_id") != self.session:
            raise RuntimeError("Owned receiver hook identity did not match launch")
        socket = hook.get("socket", "")
        if not socket or not stat.S_ISSOCK(pathlib.Path(socket).stat().st_mode):
            raise RuntimeError("Owned native receiver did not export a socket")
        fd = subprocess.run(["/usr/sbin/lsof", "-a", "-p", str(self.process.pid), "-U", "-Fn"],
            capture_output=True, text=True, timeout=5)
        bound = "n" + socket in fd.stdout.splitlines()
        if not bound:
            raise RuntimeError("Exported socket was not bound by owned native PID")
        self.evidence = {"native_session_id": self.session, "pid": self.process.pid, "socket": socket,
            "process_start_observation": self.process_start,
            "socket_bound_by_owned_pid": bound, "hook": hook,
            "effective_crossSessionInbound": effective.get("crossSessionInbound"),
            "settings_sha256": common.digest(self.folder / "settings.json")}
        common.write_json(self.folder / "endpoint-observation.json", self.evidence)
        common.write_json(self.folder / "socket-observation.json", {"pid": self.process.pid,
            "exit": fd.returncode, "stdout": fd.stdout, "stderr": fd.stderr})
        return self.evidence

    def input(self):
        message_id = str(uuid.uuid4())
        self.send({"type": "user", "session_id": self.session, "uuid": message_id,
            "parent_tool_use_id": None, "message": {"role": "user", "content": "Run the disposable deterministic fixture."}})
        result = self.wait(lambda value: value.get("type") == "result", timeout=25)
        if (result.get("subtype") != "success" or result.get("is_error") is not False
                or result.get("session_id") != self.session or result.get("user_message_uuid") != message_id
                or result.get("queued_turn_count") != 0):
            raise RuntimeError("Native result did not prove a successful completed input")
        return {key: result[key] for key in ("subtype", "is_error", "session_id", "uuid", "user_message_uuid",
            "queued_turn_count") if key in result}

    def close(self):
        if self.closed:
            return
        self.closed = True
        try:
            self.process.stdin.close()
        except BrokenPipeError:
            pass
        try:
            self.process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            os.killpg(self.process.pid, signal.SIGTERM)
            try:
                self.process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                os.killpg(self.process.pid, signal.SIGKILL)
                self.process.wait(timeout=3)
        self.reader.join(timeout=3)
        self.err_reader.join(timeout=3)
        common.write_json(self.folder / "native-observations.json", self.events)
        common.write_json(self.folder / "cleanup.json", {"exit": self.process.returncode,
            "stdout_unparsed_lines": self.unparsed, "stderr_bytes": self.stderr_bytes})


def inspect_mail(buddy, work, env, identity, message_id):
    return json.loads(common.run([buddy, "adapter", "inbox", "--session", identity["session_id"],
        "--incarnation", identity["incarnation"], "--message", str(message_id)], work, env))


def qualify_case(root, mode, claude, buddy, work, env, server, coordinator):
    folder = root / mode
    folder.mkdir()
    marker = "Buddy disposable " + mode + " wake " + str(uuid.uuid4()) + ": assignment remains queued."
    sender_route, receiver_route = mode + "-sender", mode + "-receiver"
    server.cases[receiver_route] = {"sender": False, "marker": marker}
    result = {"mode": mode, "checks": {}, "native_transport": "SendMessage; exact captured uds address"}
    receiver = sender = None
    try:
        receiver = Native(claude, folder / "receiver", work,
            dict(env, ANTHROPIC_BASE_URL=server.base + "/" + receiver_route), mode)
        result["receiver"] = receiver.start()
        identity = json.loads(common.run([buddy, "lifecycle", "begin", "--session", receiver.session,
            "--harness", "claude"], work, env))
        result["buddy_identity"] = identity
        queued = json.loads(common.run([buddy, "adapter", "queue", "--session", coordinator["session_id"],
            "--incarnation", coordinator["incarnation"], "--to", identity["session_id"],
            "--to-incarnation", identity["incarnation"], "--body", "Fixture assignment body stays in Buddy."], work, env))
        message_id = queued["delivery"]["message_id"]
        result["queued"] = queued
        before = inspect_mail(buddy, work, env, identity, message_id)
        result["receiver_initial_result"] = receiver.input()
        address = "uds:" + result["receiver"]["socket"]
        server.owned_recipients.add(address)
        server.cases[sender_route] = {"sender": True, "marker": marker, "recipient": address, "sent": False}
        sender = Native(claude, folder / "sender", work,
            dict(env, ANTHROPIC_BASE_URL=server.base + "/" + sender_route), "accept")
        result["sender"] = sender.start()
        result["sender_initial_result"] = sender.input()
        # A native tool receipt is separate from the receiving model reading
        # the message. Observe both, with a bounded absence window for negatives.
        deadline = time.monotonic() + 3
        while time.monotonic() < deadline:
            time.sleep(.05)
        requests = [item for item in server.requests if item["route"] in (sender_route, receiver_route)]
        outputs = [item for request in requests for item in request["tool_results"]]
        native_notices = [notice for event in sender.events for notice in event.get("delivery_notices", [])]
        notices = native_notices + [notice for request in requests for notice in request["delivery_notices"]]
        receipts = [json.loads(text) for text in texts(outputs)]
        native_message_ids = {item.get("msg_id") for item in receipts}
        if len(native_message_ids) != 1 or not next(iter(native_message_ids)):
            raise RuntimeError("Native SendMessage result did not identify exactly one message")
        native_message_id = next(iter(native_message_ids))
        outcome = delivery_outcome(notices, address, native_message_id)
        result["send_tool_results"] = outputs
        result["native_delivery_notices"] = sorted(set(notices))
        result["post_send_observation_seconds"] = 3
        received = any(item["route"] == receiver_route and item["peer_marker_seen"] for item in requests)
        after = inspect_mail(buddy, work, env, identity, message_id)
        common.write_json(folder / "mail-before.json", before)
        common.write_json(folder / "mail-after.json", after)
        checks = result["checks"]
        checks["effective_inbound_matches"] = result["receiver"]["effective_crossSessionInbound"] == mode
        checks["actual_sendmessage_result"] = bool(outputs)
        checks["owned_socket_used"] = any(item.get("recipient") == address for item in requests)
        checks["native_transport_accepted"] = bool(receipts) and all(item.get("success") is True for item in receipts)
        mail = after["messages"]
        checks["buddy_assignment_unchanged"] = (before == after and mail["count"] == 1
            and mail["items"][0]["state"] == "queued"
            and mail["items"][0]["acknowledged"]["status"] == "unknown"
            and mail["items"][0]["completed"]["status"] == "unknown")
        checks["receiver_marker_delivery"] = received if mode == "accept" else not received
        if mode == "accept":
            checks["native_send_not_error"] = bool(outputs) and not any(item.get("is_error") for item in outputs)
        else:
            checks["native_explicit_delivery_notice"] = outcome == ("held" if mode == "hold" else "refused")
        result["native_message_id"] = native_message_id
        result["native_delivery_outcome"] = "receiver_provider_input_observed" if received else outcome
        result["marker_reached_receiver_provider_input"] = received
        result["body_delivery"] = "not_marked_delivered"
        result["work_completion"] = "unknown"
        checks["case_completed"] = True
        return result
    except BaseException as exc:
        result["failure"] = str(exc)
        result["checks"]["case_completed"] = False
        (folder / "failure.txt").write_text(traceback.format_exc())
        return result
    finally:
        for name, process in (("sender", sender), ("receiver", receiver)):
            if process:
                try:
                    process.close()
                    result["checks"][name + "_exit_zero"] = process.process.returncode == 0
                    result["checks"][name + "_stdout_parsed"] = process.unparsed == 0
                    if hasattr(process, "evidence"):
                        result["checks"][name + "_socket_removed"] = not pathlib.Path(process.evidence["socket"]).exists()
                except BaseException as exc:
                    result.setdefault("cleanup_failures", {})[name] = str(exc)
                    result["checks"][name + "_cleanup_succeeded"] = False
        common.write_json(folder / "result.json", result)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--claude", default=shutil.which("claude"))
    parser.add_argument("--claude-version", choices=common.QUALIFICATION_VERSIONS, default=common.VERSION)
    parser.add_argument("--buddy", type=pathlib.Path)
    args = parser.parse_args()
    common.exit_on_signals()  # F-35: TERM/HUP still run the finally blocks
    root = pathlib.Path(tempfile.mkdtemp(prefix="buddy-claude-peers-", dir="/private/tmp"))
    print("ROOT=" + str(root), flush=True)
    result = {"schema": "buddy.claude-peer-qualification.v1", "passed": False, "cases": [],
        "claude_version": args.claude_version,
        "root": str(root), "started_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "fixture_sha256": common.digest(__file__), "fixture_helper_sha256": common.digest(HELPER_PATH),
        "provider": "deterministic loopback Messages fixture; no real inference",
        "sources": ["https://code.claude.com/docs/en/cross-session-messaging",
                    "https://platform.claude.com/docs/en/build-with-claude/streaming"],
        "unqualified": ["buddy-adapter peer socket transport (adapter uses its owned stream)",
            "Buddy body ingestion", "held-message later release or expiry", "work completion",
            "real-provider or mixed-fleet qualification", "native resume and compaction"]}
    server = None
    try:
        env = common.clean_env()
        work = root / "work"
        work.mkdir()
        (root / "tmp").mkdir()
        (root / "version-config").mkdir()
        if not args.claude:
            raise RuntimeError("Claude is not on PATH; provide --claude")
        claude = pathlib.Path(args.claude).absolute()
        native_env = dict(env, TMPDIR=str(root / "tmp"), ANTHROPIC_API_KEY="buddy-loopback-fixture-not-a-provider-key",
            CLAUDE_CODE_TMPDIR=str(root / "tmp"),
            CLAUDE_CONFIG_DIR=str(root / "version-config"),
            CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1", DISABLE_AUTOUPDATER="1", DISABLE_GROWTHBOOK="1",
            DISABLE_TELEMETRY="1", DISABLE_ERROR_REPORTING="1", CLAUDE_CODE_DISABLE_AUTO_MEMORY="1")
        result["native_version"] = common.run([claude, "--version"], work, native_env).strip()
        if result["native_version"] != args.claude_version + " (Claude Code)":
            raise RuntimeError("Installed native version differs from the pinned fixture contract")
        result["native_sha256"] = common.digest(claude.resolve())
        common.write_json(root / "native-version.json", {"version": result["native_version"],
            "executable_sha256": result["native_sha256"]})
        result["source_head"] = common.run(["git", "--no-optional-locks", "rev-parse", "HEAD"], REPO, env).strip()
        result["source_status"] = common.run(["git", "--no-optional-locks", "status", "--porcelain"], REPO, env).splitlines()
        manifest = common.source_manifest(env)
        common.write_json(root / "source-manifest.json", manifest)
        result["source_manifest_sha256"] = common.digest(root / "source-manifest.json")
        buddy = args.buddy.resolve() if args.buddy else root / "buddy"
        if not args.buddy:
            common.run(["go", "build", "-o", buddy, "./cmd/buddy"], REPO,
                dict(env, GOCACHE="/private/tmp/buddy-claude-go-cache", GOPROXY="off", GOSUMDB="off", GIT_OPTIONAL_LOCKS="0"), timeout=120)
        if common.source_manifest(env) != manifest:
            raise RuntimeError("Source changed while preparing binary; retry from a stable snapshot")
        result["buddy_sha256"] = common.digest(buddy)
        result["binary_paths"] = {"buddy": str(buddy)}
        result["buddy_origin"] = "provided artifact" if args.buddy else "built from source manifest"
        common.run(["git", "init", "-q", work], root, env)
        common.run([buddy, "init"], work, env)
        coordinator = json.loads(common.run([buddy, "lifecycle", "begin", "--session", "fixture-coordinator"], work, env))
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), PeerProvider)
        server.base = f"http://127.0.0.1:{server.server_address[1]}"
        server.cases, server.requests, server.errors, server.unexpected = {}, [], [], []
        server.owned_recipients = set()
        threading.Thread(target=server.serve_forever, daemon=True).start()
        for mode in ("accept", "hold", "refuse"):
            case = qualify_case(root, mode, claude, buddy, work, native_env, server, coordinator)
            result["cases"].append(case)
        result["checks"] = classifier_checks()
        result["checks"].update({mode["mode"] + "_" + key: value for mode in result["cases"] for key, value in mode["checks"].items()})
        result["checks"]["only_expected_provider_requests"] = not server.errors and not server.unexpected
        result["checks"]["unchanged_home"] = native_env.get("HOME") == os.environ.get("HOME")
        result["passed"] = all(result["checks"].values())
    except BaseException as exc:
        result["failure"] = str(exc)
        (root / "failure.txt").write_text(traceback.format_exc())
    finally:
        if server:
            server.shutdown()
            server.server_close()
            common.write_json(root / "provider-observations.json", server.requests)
            result["provider_errors"] = server.errors
        result["evidence_sha256"] = {str(path.relative_to(root)): common.digest(path)
            for mode in ("accept", "hold", "refuse") for path in (root / mode).rglob("*.json")
            if "config" not in path.relative_to(root / mode).parts}
        if (root / "provider-observations.json").exists():
            result["evidence_sha256"]["provider-observations.json"] = common.digest(root / "provider-observations.json")
        if (root / "native-version.json").exists():
            result["evidence_sha256"]["native-version.json"] = common.digest(root / "native-version.json")
        if result["fixture_sha256"] != common.digest(__file__) or result["fixture_helper_sha256"] != common.digest(HELPER_PATH):
            result["failure"] = "Fixture or imported helper changed during this run"
            result["passed"] = False
        result["finished_at"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        common.write_json(root / "result.json", result)
        print(json.dumps({"passed": result["passed"], "checks": result.get("checks"), "failure": result.get("failure")}, indent=2), flush=True)
        print("RECEIPT=" + str(root / "result.json"), flush=True)
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
