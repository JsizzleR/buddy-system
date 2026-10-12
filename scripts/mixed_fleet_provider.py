#!/usr/bin/env python3
"""Deterministic loopback providers for the opt-in mixed-fleet qualification.

One provider belongs to one native adapter. Queue a turn before submitting its
native input, then assert its correlated tool results after the native result.
Unique call IDs and hashes of user-input boundaries keep old history and HTTP
retries from advancing a later script. Requests retain tool results and metadata
only: never request headers, prompts, system instructions or tool schemas.

This is a local transport/tool fixture, not inference or work-completion proof.
The caller still verifies native result identity and all expected artifacts.
"""

import base64
import collections
import dataclasses
import hashlib
import http.server
import json
import re
import shlex
import socket
import sys
import threading
import uuid
import zlib


MAX_REQUEST = 8 << 20
MAX_OUTPUT = 1 << 20


@dataclasses.dataclass(frozen=True)
class Step:
    kind: str
    value: object
    cwd: str = ""
    marker: str = ""
    timeout_ms: int = 10000
    require_approval: bool = False


def shell(command, cwd, marker=None, timeout_ms=10000, require_approval=False):
    """Run bounded `sh -eu`; optionally request exact native Codex approval.

    Linked-worktree Git locks are protected even below the disposable workspace.
    Only the caller's marked Git mutations request that exception; launch remains
    workspace-write, with no saved permission rule or broadened launch profile.
    Claude already presents each exact Bash input on its operator descriptor.
    """
    if not isinstance(command, str) or not command or not str(cwd):
        raise ValueError("shell requires a command and working directory")
    if type(timeout_ms) is not int or not 1 <= timeout_ms <= 30000:
        raise ValueError("shell timeout must be between 1 and 30000 milliseconds")
    if marker is not None and (not isinstance(marker, str) or not marker or "\n" in marker or "\r" in marker):
        raise ValueError("shell marker must be one nonempty line")
    if type(require_approval) is not bool:
        raise ValueError("require_approval must be a boolean")
    return Step("shell", command, str(cwd), marker or "", timeout_ms, require_approval)


def patch(text):
    """Codex apply_patch input, including intentionally malformed negative cases."""
    return Step("patch", text)


def write(path, content):
    """Claude Write input; claim denial is checked separately by the caller."""
    return Step("write", {"file_path": str(path), "content": content})


def _text(value):
    if isinstance(value, str):
        return value
    if isinstance(value, list):
        return "\n".join(x.get("text", "") for x in value if isinstance(x, dict) and isinstance(x.get("text"), str))
    return json.dumps(value, sort_keys=True)


class ScriptedTurn:
    def __init__(self, label, steps, final_text, token):
        self.label = label
        self.steps = tuple(steps)
        self.final_text = final_text
        self.token = token
        self.results = []
        self.final_emitted = False
        self.input_fingerprint = None
        self.issued = []
        self._items = {}

    def matches_approval(self, detail):
        """Match only the outstanding exact call, never approve policy changes.

        The parent still binds the native session/turn and owns the operator
        descriptor. Native permission suggestions are metadata: this method
        does not apply them, and the adapter answers only a one-shot decision.
        """
        if not isinstance(detail, dict) or self.final_emitted or len(self.issued) != len(self.results) + 1:
            return False
        call_id = self.issued[-1]
        item = self._items[call_id]
        if item["type"] == "tool_use":
            known = {"subtype", "tool_name", "display_name", "input", "description", "permission_suggestions",
                     "decision_reason", "decision_reason_type", "tool_use_id"}
            return (not (set(detail) - known) and detail.get("subtype") == "can_use_tool"
                    and detail.get("tool_use_id") == call_id and detail.get("tool_name") == item["name"]
                    and detail.get("input") == item["input"])
        if item["type"] != "function_call" or item["name"] != "exec_command":
            return False
        known = {"kind", "startedAtMs", "environmentId", "threadId", "turnId", "itemId", "approvalId", "command", "cwd", "reason", "commandActions",
                 "availableDecisions", "additionalPermissions", "proposedExecpolicyAmendment",
                 "proposedNetworkPolicyAmendments", "networkApprovalContext"}
        if set(detail) - known or any(detail.get(key) for key in (
                "additionalPermissions", "proposedNetworkPolicyAmendments", "networkApprovalContext")):
            return False
        args = json.loads(item["arguments"])
        if args.get("sandbox_permissions") != "require_escalated":
            return False
        if (detail.get("kind") != "command" or detail.get("environmentId") != "local"
                or "accept" not in detail.get("availableDecisions", [])):
            return False
        # The measured native request always suggests a policy amendment even
        # without prefix_rule. It is metadata, never the decision sent: the
        # adapter uses plain `accept`. Admit only this exact command's argv.
        proposal = detail.get("proposedExecpolicyAmendment")
        if proposal is not None and proposal != shlex.split(args["cmd"]):
            return False
        command = detail.get("command")
        exact = command == args["cmd"] or command == ["/bin/sh", "-c", args["cmd"]]
        if not exact and isinstance(command, str):
            try:
                exact = shlex.split(command) == ["/bin/sh", "-c", args["cmd"]]
            except ValueError:
                return False
        return (exact and detail.get("cwd") == args["workdir"]
                and detail.get("itemId") in (call_id, item["id"]))

    def assert_complete(self):
        """Require tool output for every call, not merely a native final reply.

        Shell outputs must include the wrapper's actual exit receipt. Edit steps
        may intentionally be denied; the caller checks their exact refusal and
        positive artifacts. Final emission does not prove native consumption.
        """
        if not self.final_emitted or len(self.results) != len(self.steps):
            raise AssertionError(f"{self.label}: script incomplete ({len(self.results)}/{len(self.steps)} results)")
        for step, result in zip(self.steps, self.results):
            if step.kind == "shell" and result.get("exit_code") != 0:
                raise AssertionError(f"{self.label}: shell execution was not observed successful: {result}")
            if step.marker:
                self.assert_marker(step.marker)
        return True

    def assert_marker(self, marker):
        # An echoed command or permission refusal must never count as execution.
        if not any(result.get("exit_code") == 0 and marker in result["output"].splitlines()
                   for result in self.results):
            raise AssertionError(f"{self.label}: successful shell marker absent: {marker}")
        return True

    def snapshot(self):
        return {"label": self.label, "token": self.token, "step_count": len(self.steps),
                "input_fingerprint": self.input_fingerprint, "results": list(self.results),
                "final_emitted": self.final_emitted}


# The command and receipt are encoded so a native denial that echoes its input
# cannot accidentally satisfy a successful-execution assertion. This program
# runs only within the native shell tool, not inside the HTTP provider process.
_SHELL_RUNNER = '''import base64,json,os,signal,subprocess,sys
v=json.loads(base64.b64decode(sys.argv[1]))
p=subprocess.Popen(['/bin/sh','-eu','-c',v['command']],cwd=v['cwd'],start_new_session=True)
try:
 rc=p.wait(timeout=v['timeout_ms']/1000)
except subprocess.TimeoutExpired:
 os.killpg(p.pid,signal.SIGKILL);p.wait();rc=124
print('\\n'+v['receipt']+'='+str(rc),flush=True)
if rc==0 and v['marker']:print(v['marker'],flush=True)
sys.exit(rc if 0<=rc<=255 else 1)
'''


def _shell_command(step, receipt):
    value = {"command": step.value, "cwd": step.cwd, "timeout_ms": step.timeout_ms,
             "receipt": receipt, "marker": step.marker}
    encoded = base64.b64encode(json.dumps(value).encode()).decode()
    return " ".join(shlex.quote(x) for x in (sys.executable, "-c", _SHELL_RUNNER, encoded))


def _fingerprint(request, harness):
    """Hash only user text boundaries; tool-result user blocks are not turns."""
    messages = request.get("input" if harness == "codex" else "messages", [])
    user_inputs = []
    for message in messages:
        if not isinstance(message, dict) or message.get("role") != "user":
            continue
        content = message.get("content", [])
        if isinstance(content, str):
            user_inputs.append(content)
        elif isinstance(content, list):
            text = [block.get("text", "") for block in content if isinstance(block, dict)
                    and block.get("type") in ("text", "input_text")]
            if text:
                user_inputs.append(text)
    return hashlib.sha256(json.dumps(user_inputs, sort_keys=True).encode()).hexdigest()


class ScriptedProvider:
    def __init__(self, harness, loopback="127.0.0.1"):
        if harness not in ("codex", "claude"):
            raise ValueError("provider harness must be codex or claude")
        if loopback not in ("127.0.0.1", "::1"):
            raise ValueError("fixture provider requires a literal loopback address")
        self.harness = harness
        self.loopback = loopback
        self.requests = []
        self.errors = []
        self.turns = []
        self._queued = collections.deque()
        self._active = None
        self._last = None
        self._calls = {}
        self._tool_names = set()
        self._lock = threading.RLock()
        self._server = None
        self._thread = None

    def start(self):
        if self._server is not None:
            raise RuntimeError("provider already started")
        # The host can exhaust its IPv4 ephemeral ports independently of this
        # fixture. Explicit IPv6 loopback needs no network configuration change
        # and remains local; never accept an arbitrary listening address.
        class LocalServer(http.server.ThreadingHTTPServer):
            address_family = socket.AF_INET6 if self.loopback == "::1" else socket.AF_INET
        server = LocalServer((self.loopback, 0), _Handler)
        server.daemon_threads = True
        server.fixture = self
        self._server = server
        self.port = server.server_address[1]
        host = "[::1]" if self.loopback == "::1" else self.loopback
        self.base_url = f"http://{host}:{self.port}" + ("/v1" if self.harness == "codex" else "")
        self._thread = threading.Thread(target=server.serve_forever, daemon=True)
        self._thread.start()
        return self

    def close(self):
        if self._server:
            self._server.shutdown()
            self._server.server_close()
            self._thread.join(timeout=3)
            self._server = None

    def __enter__(self):
        return self.start()

    def __exit__(self, *args):
        self.close()

    def queue_turn(self, label, steps, final_text="Deterministic Buddy fixture complete."):
        steps = tuple(steps)
        if not label or len(steps) > 64 or not all(isinstance(step, Step) for step in steps):
            raise ValueError("a labeled turn accepts at most 64 Step values")
        allowed = {"shell", "patch" if self.harness == "codex" else "write"}
        if any(step.kind not in allowed for step in steps):
            raise ValueError("tool step does not belong to this provider")
        with self._lock:
            turn = ScriptedTurn(label, steps, final_text, uuid.uuid4().hex)
            self.turns.append(turn)
            self._queued.append(turn)
            return turn

    def _outputs(self, request):
        if self.harness == "codex":
            return [{"call_id": item.get("call_id"), "output": _text(item.get("output", "")),
                     "is_error": None}
                    for item in request.get("input", []) if isinstance(item, dict)
                    and item.get("type") in ("function_call_output", "custom_tool_call_output")]
        outputs = []
        for message in request.get("messages", []):
            content = message.get("content", [])
            if not isinstance(content, list):
                continue
            outputs.extend({"call_id": block.get("tool_use_id"), "output": _text(block.get("content", "")),
                            "is_error": block.get("is_error", False)} for block in content
                           if isinstance(block, dict) and block.get("type") == "tool_result")
        return outputs

    def _item(self, turn, index):
        step = turn.steps[index]
        call_id = ("call_buddy_" if self.harness == "codex" else "toolu_buddy_") + turn.token + "_" + str(index)
        receipt = "BUDDY_FIXTURE_EXIT_" + turn.token + "_" + str(index)
        if step.kind == "shell":
            command = _shell_command(step, receipt)
            if self.harness == "claude":
                name, value = "Bash", {"command": command, "timeout": min(60000, step.timeout_ms + 5000)}
            elif "shell_command" in self._tool_names and "exec_command" not in self._tool_names:
                name, value = "shell_command", {"command": command, "workdir": step.cwd,
                                               "timeout_ms": step.timeout_ms + 5000}
            elif "shell" in self._tool_names and "exec_command" not in self._tool_names:
                name, value = "shell", {"command": ["/bin/sh", "-c", command], "workdir": step.cwd,
                                       "timeout_ms": step.timeout_ms + 5000}
            else:
                name, value = "exec_command", {"cmd": command, "workdir": step.cwd, "login": False,
                                              "shell": "/bin/sh", "yield_time_ms": 30000, "max_output_tokens": 10000}
            if step.require_approval and self.harness == "codex":
                if name != "exec_command":
                    raise ValueError("explicit fixture approval requires Codex exec_command")
                value["sandbox_permissions"] = "require_escalated"
                value["justification"] = ("Allow this exact disposable mixed-fleet Git mutation under /private/tmp? "
                                          "No production repository, credentials or persistent permission change.")
        elif step.kind == "patch":
            name, value = "apply_patch", step.value
        else:
            name, value = "Write", step.value
        if self.harness == "claude":
            item = {"type": "tool_use", "id": call_id, "name": name, "input": value}
        else:
            item = {"type": "custom_tool_call" if step.kind == "patch" else "function_call",
                    "id": "item_" + call_id, "call_id": call_id, "name": name, "status": "completed"}
            item["input" if step.kind == "patch" else "arguments"] = value if step.kind == "patch" else json.dumps(value)
        self._calls[call_id] = {"turn": turn, "index": index, "item": item, "receipt": receipt, "result": None}
        turn.issued.append(call_id)
        turn._items[call_id] = item
        return item

    def _respond(self, request, path):
        with self._lock:
            names = [tool.get("name") for tool in request.get("tools", []) if isinstance(tool, dict)]
            self._tool_names.update(name for name in names if isinstance(name, str))
            outputs = [output for output in self._outputs(request) if output["call_id"] in self._calls]
            for output in outputs:
                if len(output["output"].encode()) > MAX_OUTPUT:
                    raise ValueError("fixture tool output exceeds retention bound")
                call = self._calls[output["call_id"]]
                if call["result"] is not None:
                    if output != call["result"]:
                        raise ValueError("native history changed an existing fixture tool result")
                    continue
                turn, index = call["turn"], call["index"]
                if index != len(turn.results):
                    raise ValueError("fixture tool results arrived out of sequence")
                call["result"] = output
                result = dict(output, step=index, kind=turn.steps[index].kind)
                if turn.steps[index].kind == "shell":
                    exits = re.findall(r"^" + re.escape(call["receipt"]) + r"=(-?\d+)\r?$", output["output"], re.MULTILINE)
                    result["exit_code"] = int(exits[0]) if len(exits) == 1 else None
                turn.results.append(result)
            fingerprint = _fingerprint(request, self.harness)
            self.requests.append({"path": path, "model": request.get("model"), "stream": request.get("stream"),
                                  "input_fingerprint": fingerprint, "tools": names, "tool_outputs": outputs})
            if self._active is None:
                if self._last and self._last.input_fingerprint == fingerprint:
                    return self._final_item(self._last), len(self.requests)
                if not self._queued:
                    raise ValueError("native input arrived without a queued fixture turn")
                self._active = self._queued.popleft()
                self._active.input_fingerprint = fingerprint
            turn = self._active
            if len(turn.results) < len(turn.issued):
                # Retries repeat the identical ID. Request count is not progress.
                return self._calls[turn.issued[-1]]["item"], len(self.requests)
            if len(turn.issued) < len(turn.steps):
                return self._item(turn, len(turn.issued)), len(self.requests)
            turn.final_emitted = True
            self._active = None
            self._last = turn
            return self._final_item(turn), len(self.requests)

    def _final_item(self, turn):
        if self.harness == "claude":
            return {"type": "text", "text": turn.final_text}
        return {"type": "message", "id": "msg_buddy_" + turn.token, "status": "completed", "role": "assistant",
                "content": [{"type": "output_text", "text": turn.final_text, "annotations": []}]}


class _Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def _body(self, status, content_type, body):
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        fixture = self.server.fixture
        try:
            size = int(self.headers.get("Content-Length", "0"))
            if not 0 < size <= MAX_REQUEST:
                raise ValueError("request body size outside fixture bound")
            raw = self.rfile.read(size)
            encoding = self.headers.get("Content-Encoding", "identity")
            if encoding in ("gzip", "deflate"):
                decoder = zlib.decompressobj(16 + zlib.MAX_WBITS if encoding == "gzip" else zlib.MAX_WBITS)
                raw = decoder.decompress(raw, MAX_REQUEST + 1)
                if len(raw) > MAX_REQUEST or not decoder.eof:
                    raise ValueError("compressed request exceeds fixture bound")
            elif encoding != "identity":
                raise ValueError("unsupported request encoding")
            request = json.loads(raw)
            if not isinstance(request, dict):
                raise ValueError("request must be an object")
            path = self.path.split("?")[0]
            if fixture.harness == "claude" and path == "/v1/messages/count_tokens":
                self._body(200, "application/json", b'{"input_tokens":100}')
                return
            expected = "/v1/responses" if fixture.harness == "codex" else "/v1/messages"
            if path != expected:
                raise ValueError("unexpected fixture endpoint")
            item, sequence = fixture._respond(request, path)
            events = _codex_events(item, sequence) if fixture.harness == "codex" else _claude_events(item, sequence, request.get("model"))
            body = "".join("event: " + event["type"] + "\ndata: " + json.dumps(event) + "\n\n" for event in events).encode()
            self._body(200, "text/event-stream", body)
        except (ValueError, TypeError, KeyError, AttributeError) as exc:
            # Fixed diagnostics do not contain the rejected request or headers.
            fixture.errors.append({"error_type": type(exc).__name__, "error": str(exc)})
            self._body(400, "application/json", b'{"error":{"type":"fixture_error","message":"invalid fixture request; inspect provider errors"}}')
        except (BrokenPipeError, ConnectionResetError):
            fixture.errors.append({"error_type": "connection_closed", "error": "native client closed the response"})


def _codex_events(item, sequence):
    rid = f"resp_buddy_{sequence}"
    return [
        {"type": "response.created", "response": {"id": rid, "object": "response", "status": "in_progress", "output": []}},
        {"type": "response.output_item.added", "output_index": 0, "item": dict(item)},
        {"type": "response.output_item.done", "output_index": 0, "item": item},
        {"type": "response.completed", "response": {"id": rid, "object": "response", "status": "completed", "output": [item],
                                                     "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}},
    ]


def _claude_events(item, sequence, model):
    tool = item["type"] == "tool_use"
    block = dict(item, input={}) if tool else {"type": "text", "text": ""}
    delta = {"type": "input_json_delta", "partial_json": json.dumps(item["input"])} if tool else {"type": "text_delta", "text": item["text"]}
    return [
        {"type": "message_start", "message": {"id": f"msg_buddy_{sequence}", "type": "message", "role": "assistant",
          "model": model, "content": [], "stop_reason": None, "stop_sequence": None,
          "usage": {"input_tokens": 100, "output_tokens": 1}}},
        {"type": "content_block_start", "index": 0, "content_block": block},
        {"type": "content_block_delta", "index": 0, "delta": delta},
        {"type": "content_block_stop", "index": 0},
        {"type": "message_delta", "delta": {"stop_reason": "tool_use" if tool else "end_turn", "stop_sequence": None}, "usage": {"output_tokens": 1}},
        {"type": "message_stop"},
    ]


def self_test():
    """Hermetic protocol, history and approval checks; no native/provider calls."""
    import copy
    import pathlib
    import subprocess
    import tempfile
    import unittest

    class ProviderTests(unittest.TestCase):
        def setUp(self):
            self.folder = tempfile.TemporaryDirectory(prefix="buddy-provider-self-test-")
            self.cwd = str(pathlib.Path(self.folder.name).resolve())

        def tearDown(self):
            self.folder.cleanup()

        def test_literal_loopback_only(self):
            for host in ("127.0.0.1", "::1"):
                self.assertEqual(ScriptedProvider("claude", host).loopback, host)
            for host in ("0.0.0.0", "::", "localhost", "127.0.0.2", "example.com", ""):
                with self.assertRaises(ValueError):
                    ScriptedProvider("claude", host)

        def request(self, harness, text, outputs=()):
            if harness == "codex":
                return {"input": [{"role": "user", "content": [{"type": "input_text", "text": text}]}] + list(outputs),
                        "tools": [{"name": "exec_command"}]}
            return {"messages": [{"role": "user", "content": [{"type": "text", "text": text}] + list(outputs)}]}

        def output(self, harness, item, text):
            if harness == "codex":
                return {"type": "function_call_output", "call_id": item["call_id"], "output": text}
            return {"type": "tool_result", "tool_use_id": item["id"], "content": text}

        def command(self, harness, item):
            return json.loads(item["arguments"])["cmd"] if harness == "codex" else item["input"]["command"]

        def execute(self, harness, item):
            return subprocess.run(["/bin/sh", "-c", self.command(harness, item)], text=True,
                                  capture_output=True, timeout=5)

        def test_history_and_retries(self):
            for harness in ("codex", "claude"):
                with self.subTest(harness=harness):
                    provider = ScriptedProvider(harness)
                    first = provider.queue_turn("first", [shell("printf first", self.cwd, marker="FIRST")])
                    second = provider.queue_turn("second", [shell("printf second", self.cwd, marker="SECOND")])
                    one = self.request(harness, "PRIVATE_USER_TEXT")
                    item, _ = provider._respond(one, "fixture")
                    retry, _ = provider._respond(one, "fixture")
                    self.assertEqual(item, retry)
                    executed = self.execute(harness, item)
                    self.assertEqual(executed.returncode, 0)
                    output = self.output(harness, item, executed.stdout)
                    old = self.request(harness, "PRIVATE_USER_TEXT", [output])
                    final, _ = provider._respond(old, "fixture")
                    first.assert_complete()
                    self.assertFalse(second.issued)
                    final_again, _ = provider._respond(old, "fixture")
                    self.assertEqual(final, final_again)
                    self.assertFalse(second.issued)
                    current = self.request(harness, "PRIVATE_SECOND_TEXT", [output])
                    item2, _ = provider._respond(current, "fixture")
                    self.assertNotEqual(item2, item)
                    self.assertFalse(second.results)
                    retry2, _ = provider._respond(current, "fixture")
                    self.assertEqual(retry2, item2)
                    run2 = self.execute(harness, item2)
                    output2 = self.output(harness, item2, run2.stdout)
                    provider._respond(self.request(harness, "PRIVATE_SECOND_TEXT", [output, output2]), "fixture")
                    second.assert_complete()
                    self.assertEqual(len(first.results), 1)
                    self.assertEqual(len(second.results), 1)
                    self.assertNotIn("PRIVATE_USER_TEXT", json.dumps(provider.requests))
                    self.assertNotIn("PRIVATE_SECOND_TEXT", json.dumps(provider.requests))

        def test_denial_echo_does_not_pass(self):
            for harness in ("codex", "claude"):
                provider = ScriptedProvider(harness)
                turn = provider.queue_turn("denied", [shell("true", self.cwd, marker="DONE")])
                item, _ = provider._respond(self.request(harness, "one"), "fixture")
                echo = "Command denied: " + self.command(harness, item)
                provider._respond(self.request(harness, "one", [self.output(harness, item, echo)]), "fixture")
                with self.assertRaises(AssertionError):
                    turn.assert_complete()
                with self.assertRaises(AssertionError):
                    turn.assert_marker("DONE")

        def test_nonzero_and_timeout(self):
            for script, timeout, expected in (("false", 1000, 1), ("sleep 3", 50, 124)):
                provider = ScriptedProvider("codex")
                turn = provider.queue_turn("failure", [shell(script, self.cwd, marker="NEVER", timeout_ms=timeout)])
                item, _ = provider._respond(self.request("codex", "one"), "fixture")
                run = self.execute("codex", item)
                self.assertEqual(run.returncode, expected)
                self.assertNotIn("NEVER", run.stdout)
                provider._respond(self.request("codex", "one", [self.output("codex", item, run.stdout)]), "fixture")
                self.assertEqual(turn.results[0]["exit_code"], expected)
                with self.assertRaises(AssertionError):
                    turn.assert_complete()

        def test_unqueued_changed_and_unknown_result_refuse_progress(self):
            provider = ScriptedProvider("codex")
            with self.assertRaises(ValueError):
                provider._respond(self.request("codex", "one"), "fixture")
            turn = provider.queue_turn("edit", [patch("malformed")])
            item, _ = provider._respond(self.request("codex", "one"), "fixture")
            unknown = self.output("codex", dict(item, call_id="unknown"), "success")
            repeated, _ = provider._respond(self.request("codex", "one", [unknown]), "fixture")
            self.assertEqual(repeated, item)
            self.assertFalse(turn.results)
            output = self.output("codex", item, "denied")
            provider._respond(self.request("codex", "one", [output]), "fixture")
            turn.assert_complete()
            output["output"] = "success"
            with self.assertRaises(ValueError):
                provider._respond(self.request("codex", "one", [output]), "fixture")

        def test_exact_approval_matching(self):
            for harness in ("codex", "claude"):
                cases = [[shell("true", self.cwd, require_approval=True)]]
                if harness == "claude":
                    cases.append([write(self.cwd + "/own.txt", "allowed")])
                for step in cases:
                    provider = ScriptedProvider(harness)
                    turn = provider.queue_turn("approval", step)
                    item, _ = provider._respond(self.request(harness, "one"), "fixture")
                    if harness == "claude":
                        detail = {"subtype": "can_use_tool", "tool_name": item["name"], "tool_use_id": item["id"],
                                  "input": copy.deepcopy(item["input"])}
                        variants = [dict(detail, tool_use_id="stale"), dict(detail, tool_name="Different"),
                                    dict(detail, input=dict(detail["input"], unexpected="side effect"))]
                    else:
                        args = json.loads(item["arguments"])
                        detail = {"itemId": item["call_id"], "command": args["cmd"], "cwd": args["workdir"],
                                  "kind": "command", "environmentId": "local", "availableDecisions": ["accept"]}
                        self.assertTrue(turn.matches_approval(dict(detail, command=["/bin/sh", "-c", args["cmd"]])))
                        native = dict(detail, command=shlex.join(["/bin/sh", "-c", args["cmd"]]), startedAtMs=1,
                                      proposedExecpolicyAmendment=shlex.split(args["cmd"]),
                                      availableDecisions=["accept", {"acceptWithExecpolicyAmendment": {
                                          "execpolicy_amendment": shlex.split(args["cmd"])}}, "cancel"])
                        self.assertTrue(turn.matches_approval(native))
                        variants = [dict(detail, itemId="stale"), dict(detail, cwd="/different"),
                                    dict(detail, command=args["cmd"] + "; echo changed"),
                                    dict(detail, command=["/bin/sh", "-c", args["cmd"], "extra"]),
                                    dict(native, command=native["command"] + "; echo changed"),
                                    dict(native, environmentId="remote"), dict(native, kind="permissions"),
                                    dict(native, availableDecisions=[{"acceptWithExecpolicyAmendment": {}}]),
                                    dict(detail, additionalPermissions={"network": True}),
                                    dict(detail, proposedExecpolicyAmendment=["python3"])]
                    self.assertTrue(turn.matches_approval(detail))
                    for variant in variants + [dict(detail, surprise="side effect")]:
                        self.assertFalse(turn.matches_approval(variant), variant)
                    # A result consumes the call even before final emission.
                    turn.results.append({"output": "finished"})
                    self.assertFalse(turn.matches_approval(detail))

        def test_explicit_approval_is_per_call_and_supported(self):
            for requested in (False, True):
                provider = ScriptedProvider("codex")
                turn = provider.queue_turn("approval", [shell("true", self.cwd, require_approval=requested)])
                item, _ = provider._respond(self.request("codex", "one"), "fixture")
                args = json.loads(item["arguments"])
                self.assertNotIn("prefix_rule", args)
                if requested:
                    self.assertEqual(args["sandbox_permissions"], "require_escalated")
                    self.assertIn("disposable", args["justification"])
                else:
                    self.assertNotIn("sandbox_permissions", args)
                    self.assertNotIn("justification", args)
                detail = {"itemId": item["call_id"], "command": args["cmd"], "cwd": args["workdir"],
                          "kind": "command", "environmentId": "local", "availableDecisions": ["accept"]}
                self.assertEqual(turn.matches_approval(detail), requested)
            for tool in ("shell", "shell_command"):
                provider = ScriptedProvider("codex")
                provider.queue_turn("unsupported", [shell("true", self.cwd, require_approval=True)])
                request = self.request("codex", "one")
                request["tools"] = [{"name": tool}]
                with self.assertRaises(ValueError):
                    provider._respond(request, "fixture")
            with self.assertRaises(ValueError):
                shell("true", self.cwd, require_approval="yes")

    suite = unittest.defaultTestLoader.loadTestsFromTestCase(ProviderTests)
    return unittest.TextTestRunner(verbosity=2).run(suite).wasSuccessful()


if __name__ == "__main__":
    if sys.argv[1:] != ["--self-test"]:
        sys.exit("Usage: mixed_fleet_provider.py --self-test")
    sys.exit(0 if self_test() else 1)
