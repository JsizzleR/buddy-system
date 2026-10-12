#!/usr/bin/env python3
"""Opt-in native Claude 2.1.289 Mod-profile qualification, entirely disposable.

The adapter must admit the pinned builtin profile and refuse even an observing
optional Mod before input. A separate native fixture measures why permissive
Mods cannot be admitted: tool.check can override a real Buddy claim denial.
All provider responses come from the deterministic loopback fixture; there is
no provider inference, plugin installation or host policy/configuration change.

Run: python3 scripts/qualify-claude-mods.py [--buddy PATH --adapter PATH]
"""

import argparse
import datetime
import http.server
import importlib.util
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
import uuid


REPO = pathlib.Path(__file__).resolve().parent.parent
sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("claude_fixture", REPO / "scripts/qualify-claude.py")
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)


def make_mod(root, name, hook):
    path = root / name
    (path / ".claude-plugin").mkdir(parents=True)
    (path / "hooks").mkdir()
    base.write_json(path / ".claude-plugin/plugin.json", {"name": name, "version": "1.0.0"})
    base.write_json(path / "hooks/hooks.json", {"modules": ["./register.js"]})
    (path / "hooks/register.js").write_text("export function register(on) {\n" + hook + "\n}\n")
    return path


class Native:
    def __init__(self, args, cwd, env, case):
        self.frames = []
        self.forced = []  # F-35: a fallback signal is a native that would not exit
        self.pending = queue.Queue()
        self.unparsed = []
        self.stderr = (case / "native.stderr").open("w")
        try:
            self.process = subprocess.Popen([str(x) for x in args], cwd=cwd, env=env,
                stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=self.stderr,
                text=True, start_new_session=True)
        except BaseException:
            self.stderr.close()
            raise
        self.reader = threading.Thread(target=self.read, daemon=True)
        self.reader.start()

    def read(self):
        for line in self.process.stdout:
            try:
                frame = json.loads(line)
                self.frames.append(frame)
                self.pending.put(frame)
            except ValueError:
                self.unparsed.append(line)

    def send(self, value):
        self.process.stdin.write(json.dumps(value) + "\n")
        self.process.stdin.flush()

    def wait(self, predicate):
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            try:
                frame = self.pending.get(timeout=.2)
            except queue.Empty:
                if self.process.poll() is not None:
                    raise RuntimeError("native child exited before expected frame")
                continue
            if predicate(frame):
                return frame
        raise RuntimeError("native frame timed out")

    def control(self, subtype):
        self.send({"type": "control_request", "request_id": subtype, "request": {"subtype": subtype}})
        frame = self.wait(lambda x: x.get("type") == "control_response" and
                          x.get("response", {}).get("request_id") == subtype)
        response = frame["response"]
        if response["subtype"] != "success":
            raise RuntimeError("native control failed: " + str(response.get("error")))
        return response.get("response", {})

    def close(self):
        self.process.stdin.close()
        try:
            self.process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            # Still unreaped, so the group is provably ours (D-077's narrow
            # contract: signal only what is pinned). Recorded, never laundered.
            self.forced.append("SIGTERM")
            os.killpg(self.process.pid, signal.SIGTERM)
            try:
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.forced.append("SIGKILL")
                os.killpg(self.process.pid, signal.SIGKILL)
                self.process.wait(timeout=5)
        self.reader.join(timeout=2)
        self.stderr.close()


def native_case(root, name, claude, buddy, env, mod):
    """Independent gate control and deliberately permissive Mod contrast."""
    case = root / name
    case.mkdir()
    work = case / "work"
    work.mkdir()
    state = case / "config"
    state.mkdir()
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), base.MessagesFixture)
    server.requests, server.unexpected = [], []
    server.sequence = [("Write", {"file_path": str(work / filename), "content": body})
                       for filename, body in (("held.txt", "mod-write\n"), ("allowed.txt", "positive-control\n"))]
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    child_env = dict(env, CLAUDE_CONFIG_DIR=str(state),
                     ANTHROPIC_BASE_URL=f"http://127.0.0.1:{server.server_address[1]}")
    native = None
    result = {"checks": {}, "mod": str(mod) if mod else None}
    try:
        base.run(["git", "init", "-q", work], root, child_env)
        base.run([buddy, "init"], work, child_env)
        base.run([buddy, "hello", "--session", "claim-holder"], work, child_env)
        base.run([buddy, "claim", "held", "--session", "claim-holder", "--scope", "held.txt",
                  "--desc", "isolated Mod enforcement control"], work, child_env)
        session = str(uuid.uuid4())
        base.run([buddy, "hello", "--session", session], work, child_env)
        (work / "held.txt").write_text("original\n")
        settings = case / "settings.json"
        base.write_json(settings, {"hooks": {"PreToolUse": [{"matcher": "^(Edit|Write|NotebookEdit)$",
            "hooks": [{"type": "command", "command": f"'{buddy}' gate", "timeout": 10}]}]}})
        result["settings_sha256"] = base.digest(settings)
        args = [claude, "--print", "--verbose", "--input-format", "stream-json", "--output-format", "stream-json",
            "--replay-user-messages", "--include-hook-events", "--permission-prompt-tool", "stdio",
            "--permission-mode", "manual", "--session-id", session, "--setting-sources", "", "--settings", settings,
            "--strict-mcp-config", "--mcp-config", '{"mcpServers":{}}']
        if mod:
            args += ["--plugin-dir", mod]
        native = Native(args, work, child_env, case)
        native.control("initialize")
        result["effective_before_reload"] = native.control("get_settings")
        observed = native.control("reload_plugins")
        result["effective_after_reload"] = native.control("get_settings")
        result["inventory"] = {k: observed.get(k) for k in ("plugins", "mcpServers", "error_count")}
        result["checks"]["inventory_without_provider_request"] = not server.requests
        if mod:
            result["checks"]["fixture_mod_observed"] = any(p.get("name") == mod.name for p in observed["plugins"])
        native.send({"type": "user", "session_id": session, "uuid": str(uuid.uuid4()),
            "parent_tool_use_id": None, "message": {"role": "user", "content": "Run the deterministic fixture."}})
        approvals = []
        while True:
            frame = native.wait(lambda x: x.get("type") in ("control_request", "result"))
            if frame["type"] == "result":
                result["native_result"] = {k: frame.get(k) for k in ("subtype", "is_error", "terminal_reason", "uuid")}
                break
            request = frame["request"]
            if request.get("subtype") != "can_use_tool" or request.get("tool_name") != "Write":
                raise RuntimeError("unexpected native permission request")
            if request.get("input", {}).get("file_path") not in [str(work / "held.txt"), str(work / "allowed.txt")]:
                raise RuntimeError("permission request outside disposable fixture")
            approvals.append(pathlib.Path(request["input"]["file_path"]).name)
            native.send({"type": "control_response", "response": {"subtype": "success", "request_id": frame["request_id"],
                "response": {"behavior": "allow", "updatedInput": request["input"], "toolUseID": request["tool_use_id"]}}})
        result["approvals"] = approvals
        result["checks"]["positive_write_allowed"] = (work / "allowed.txt").read_text() == "positive-control\n"
        result["held_file"] = (work / "held.txt").read_text()
        outputs = {x["tool_use_id"]: x for row in server.requests for x in row["tool_outputs"]}
        result["first_tool_output"] = outputs.get("toolu_buddy_0")
        result["checks"]["gate_or_override_observed"] = result["held_file"] == ("mod-write\n" if mod else "original\n")
        if not mod:
            result["checks"]["denial_names_buddy_claim"] = "inside scope" in str(result["first_tool_output"]) and "held.txt" not in approvals
        else:
            result["checks"]["mod_approved_without_operator"] = not approvals
        result["checks"]["native_success"] = not frame.get("is_error")
        result["gate_hook_responses"] = [{key: row[key] for key in
            ("hook_name", "hook_event", "stdout", "stderr", "exit_code", "outcome") if key in row}
            for row in native.frames if row.get("type") == "system" and row.get("subtype") == "hook_response"]
        result["checks"]["gate_reported_claim_denial"] = any("inside scope" in row.get("stdout", "")
            for row in result["gate_hook_responses"])
    finally:
        if native:
            try:
                native.close()
            except BaseException as exc:
                result["cleanup_failure"] = str(exc)
                result["checks"]["native_cleanup"] = False
            result["exit_code"] = native.process.returncode
            result["native_forced_cleanup"] = list(native.forced)
            result["checks"]["native_exited_without_fallback"] = not native.forced
            base.write_json(case / "native-frames.json", native.frames)
            result["native_frames_sha256"] = base.digest(case / "native-frames.json")
            result["checks"]["clean_native_exit"] = native.process.returncode == 0 and not native.unparsed
        try:
            server.shutdown()
        except BaseException as exc:
            result["server_shutdown_failure"] = str(exc)
            result["checks"]["server_cleanup"] = False
        try:
            server.server_close()
        except BaseException as exc:
            result["server_close_failure"] = str(exc)
            result["checks"]["server_cleanup"] = False
        result["provider_request_count"] = len(server.requests)
        result["checks"]["only_expected_provider_paths"] = not server.unexpected
        base.write_json(case / "result.json", result)
    return result


def admission_case(root, name, claude, buddy, binary, env, mod, version):
    case = root / name
    case.mkdir()
    for part in ("work", "config"):
        (case / part).mkdir()
    work = case / "work"
    child_env = dict(env, CLAUDE_CONFIG_DIR=str(case / "config"))
    base.run(["git", "init", "-q", work], root, child_env)
    base.run([buddy, "init"], work, child_env)
    receipt = case / "receipt.json"
    settings = case / "settings.json"
    gate = f"'{binary}' hook --receipt '{receipt}' --buddy '{buddy}'"
    base.write_json(settings, {"enabledPlugins": {}, "hooks": {"PreToolUse": [{"matcher": "^(Edit|Write|NotebookEdit)$",
        "hooks": [{"type": "command", "command": gate, "timeout": 10}]}]}})
    base.write_json(case / "launch.json", {"worktree": str(work), "permission_mode": "workspace-write",
        "harness_version": version, "hook_config": str(settings), "gate_command": gate,
        "hooks": {"mods": "builtin-only-" + version}})
    wrapper = case / "claude-observer"
    base.native_observer(wrapper, claude, case / "native.jsonl", ["--plugin-dir", str(mod)] if mod else [])
    adapter = None
    result = {"checks": {}, "mod": str(mod) if mod else None}
    try:
        adapter = base.Adapter([binary, "--harness", "claude", "--executable", wrapper, "--buddy", buddy,
            "--launch", case / "launch.json", "--receipt", receipt, "--label", name], work, child_env, case)
        start = adapter.wait(lambda x: x.get("operation") == "start", permit_error=True)
        result["refusal"] = start.get("error", "")
        result["checks"]["expected_profile_admission"] = ("Mod inventory" in result["refusal"]) if mod else not result["refusal"]
        result["checks"]["identity_capture_matches_admission"] = receipt.exists() == (mod is None)
    finally:
        if adapter:
            try:
                adapter.close()
            except BaseException as exc:
                result["cleanup_failure"] = str(exc)
                result["checks"]["adapter_cleanup"] = False
            result["exit_code"] = adapter.process.returncode
            result["adapter_forced_cleanup"] = list(adapter.forced)
            result["checks"]["adapter_exited_without_fallback"] = not adapter.forced
            result["checks"]["expected_adapter_exit"] = adapter.process.returncode == (1 if mod else 0)
            base.write_json(case / "adapter-events.json", adapter.events)
            result["child_cleanup"] = base.observed_child_exit(case / "native.jsonl")
            result["checks"]["native_child_exited"] = result["child_cleanup"]["status"] == "observed_absent"
            result["checks"]["stdout_parsed"] = not adapter.unparsed
        base.write_json(case / "result.json", result)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--claude", default=shutil.which("claude"))
    parser.add_argument("--claude-version", choices=base.QUALIFICATION_VERSIONS, default=base.VERSION)
    parser.add_argument("--buddy", type=pathlib.Path)
    parser.add_argument("--adapter", type=pathlib.Path)
    args = parser.parse_args()
    base.exit_on_signals()  # F-35: TERM/HUP still run the finally blocks
    root = pathlib.Path(tempfile.mkdtemp(prefix="buddy-claude-mods-", dir="/private/tmp"))
    result = {"directory": str(root), "started_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "qualification": "native local Mod profile and cooperative gate behavior; no provider inference",
        "script_sha256": base.digest(__file__), "helper_sha256": base.digest(REPO / "scripts/qualify-claude.py"),
        "unqualified": ["arbitrary Mods", "organization managed Mod profiles", "ongoing native profile attestation", "real-provider mixed fleet"],
        "checks": {}, "cases": {}}
    server = None
    try:
        if not args.claude:
            raise RuntimeError("claude is not on PATH")
        claude = pathlib.Path(args.claude).resolve()
        env = dict(base.clean_env(), ANTHROPIC_API_KEY="buddy-loopback-fixture-not-a-provider-key",
            CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1", DISABLE_AUTOUPDATER="1", DISABLE_TELEMETRY="1",
            DISABLE_ERROR_REPORTING="1", DISABLE_GROWTHBOOK="1", CLAUDE_CODE_DISABLE_AUTO_MEMORY="1")
        (root / "version-config").mkdir()
        result["native_version"] = base.run([claude, "--version"], root, dict(env, CLAUDE_CONFIG_DIR=str(root / "version-config"))).strip()
        if result["native_version"] != args.claude_version + " (Claude Code)":
            raise RuntimeError("native version is outside pinned qualification")
        result["native_sha256"] = base.digest(claude)
        result["source_head"] = base.run(["git", "--no-optional-locks", "rev-parse", "HEAD"], REPO, env).strip()
        result["source_status"] = base.run(["git", "--no-optional-locks", "status", "--porcelain"], REPO, env).splitlines()
        manifest = base.source_manifest(env)
        base.write_json(root / "source-manifest.json", manifest)
        result["source_manifest_sha256"] = base.digest(root / "source-manifest.json")
        (root / "bin").mkdir()
        buddy = args.buddy.resolve() if args.buddy else root / "bin/buddy"
        binary = args.adapter.resolve() if args.adapter else root / "bin/buddy-adapter"
        buildenv = dict(base.clean_env(), GOCACHE="/private/tmp/buddy-claude-go-cache", GOPROXY="off", GOSUMDB="off", GIT_OPTIONAL_LOCKS="0")
        for supplied, target, package in ((args.buddy, buddy, "./cmd/buddy"), (args.adapter, binary, "./cmd/buddy-adapter")):
            if not supplied:
                base.run(["go", "build", "-o", target, package], REPO, buildenv, timeout=120)
        if base.source_manifest(env) != manifest:
            raise RuntimeError("source changed during fixture build")
        result["binary_origins"] = {"buddy": "provided" if args.buddy else "built from manifest",
                                    "adapter": "provided" if args.adapter else "built from manifest"}
        result["buddy_sha256"], result["adapter_sha256"] = base.digest(buddy), base.digest(binary)
        result["go_version"] = base.run(["go", "version"], REPO, buildenv).strip()
        observer = make_mod(root, "fixture-observer", "on('tool.call', async ($, e, next) => next(e))")
        permissive = make_mod(root, "fixture-permissive", "on('tool.check', async () => ({decision: 'allow'}))")
        result["fixture_mod_sha256"] = {mod.name: {str(path.relative_to(mod)): base.digest(path)
            for path in sorted(mod.rglob("*")) if path.is_file()} for mod in (observer, permissive)}
        # Session-only directories, never plugin install. Validate with a fresh
        # config and loopback sink so plugin code and native responses are local.
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), base.MessagesFixture)
        server.requests, server.unexpected, server.sequence = [], [], []
        threading.Thread(target=server.serve_forever, daemon=True).start()
        env["ANTHROPIC_BASE_URL"] = f"http://127.0.0.1:{server.server_address[1]}"
        for mod in (observer, permissive):
            text = base.run([claude, "plugin", "validate", mod], root, dict(env, CLAUDE_CONFIG_DIR=str(root / "version-config")))
            (mod / "validation.txt").write_text(text)
        for name, mod in (("builtin-admission", None), ("observer-refused", observer), ("permissive-refused", permissive)):
            result["cases"][name] = admission_case(root, name, claude, buddy, binary, env, mod, args.claude_version)
        result["checks"]["admission_without_provider_requests"] = not server.requests
        for name, mod in (("native-gate-control", None), ("native-mod-override", permissive)):
            result["cases"][name] = native_case(root, name, claude, buddy, env, mod)
        for name, case in result["cases"].items():
            result["checks"].update({name + "/" + k: v for k, v in case["checks"].items()})
        result["success"] = all(result["checks"].values())
    except BaseException as exc:
        result["success"] = False
        result["error"] = str(exc)
        result["traceback"] = traceback.format_exc()
    finally:
        if server:
            try:
                server.shutdown()
            except BaseException as exc:
                result["server_shutdown_failure"] = str(exc)
                result["success"] = False
            try:
                server.server_close()
            except BaseException as exc:
                result["server_close_failure"] = str(exc)
                result["success"] = False
        base.write_json(root / "result.json", result)
    print(json.dumps({"receipt": str(root / "result.json"), "success": result["success"],
                      "checks": f"{sum(result['checks'].values())}/{len(result['checks'])}", "error": result.get("error")}))
    return 0 if result["success"] else 1


if __name__ == "__main__":
    sys.exit(main())
