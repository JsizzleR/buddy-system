#!/usr/bin/env python3
"""Opt-in native Claude job-group probe; launches only an owned loopback fixture.

Run with explicit retained --claude, --buddy and --adapter artifacts. A native
Bash job may create another process group, so closing the adapter's owned group
is not proof that every tool job ended. Capture birth and ancestry while the
adapter lives, then record the result of Close. A surviving fixture job is
cleaned up only through that still-matching identity; fallback cleanup does not
turn the failed shutdown observation into a pass. No real provider is used.
"""
import argparse
import http.server
import importlib.util
import json
import pathlib
import shlex
import sys
import tempfile
import threading
import time
import traceback

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("job_fixture", pathlib.Path(__file__).with_name("qualify-claude.py"))
common = importlib.util.module_from_spec(spec)
spec.loader.exec_module(common)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("claude", "buddy", "adapter"):
        parser.add_argument("--" + name, required=True, type=pathlib.Path)
    parser.add_argument("--claude-version", choices=common.QUALIFICATION_VERSIONS, default="2.1.291")
    args = parser.parse_args()
    common.exit_on_signals()
    root = pathlib.Path(tempfile.mkdtemp(prefix="buddy-claude-jobs-", dir="/private/tmp"))
    print("ROOT=" + str(root), flush=True)
    result = {"schema": "buddy.claude-job-groups.v1", "root": str(root), "passed": False,
              "provider": "deterministic-loopback", "checks": {}, "adapter_forced_cleanup": []}
    client = server = job = None
    try:
        work, state = root / "work", root / "claude"
        work.mkdir(); state.mkdir()
        env = common.clean_env()
        native_env = dict(env, CLAUDE_CONFIG_DIR=str(state),
            ANTHROPIC_API_KEY="buddy-loopback-fixture-not-a-provider-key",
            CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1", DISABLE_AUTOUPDATER="1",
            DISABLE_TELEMETRY="1", DISABLE_ERROR_REPORTING="1", CLAUDE_CODE_DISABLE_AUTO_MEMORY="1")
        version = common.run([args.claude, "--version"], work, native_env).strip()
        if version != args.claude_version + " (Claude Code)":
            raise RuntimeError("retained executable does not match the requested native contract")
        result.update(native_version=version, native_sha256=common.digest(args.claude),
                      buddy_sha256=common.digest(args.buddy), adapter_sha256=common.digest(args.adapter),
                      fixture_sha256=common.digest(__file__))
        common.run(["git", "init", "-q", work], root, env)
        common.run([args.buddy, "init"], work, env)
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), common.MessagesFixture)
        server.requests, server.unexpected = [], []
        job_source = ("import os,json,pathlib,time; pathlib.Path('job.json').write_text("
                      "json.dumps(dict(pid=os.getpid(),ppid=os.getppid(),pgid=os.getpgrp()))); time.sleep(30)")
        server.sequence = [("Bash", {"command": shlex.quote(sys.executable) + " -c " + shlex.quote(job_source),
                                     "description": "owned process group fixture", "timeout": 60000})]
        threading.Thread(target=server.serve_forever, daemon=True).start()
        native_env["ANTHROPIC_BASE_URL"] = "http://127.0.0.1:%d" % server.server_address[1]
        receipt, settings = root / "worker.json", root / "settings.json"
        gate = "'%s' hook --receipt '%s' --buddy '%s'" % (args.adapter, receipt, args.buddy)
        common.write_json(settings, {"enabledPlugins": {}, "hooks": {"PreToolUse": [
            {"matcher": "^(Edit|Write|NotebookEdit)$", "hooks": [{"type": "command", "command": gate, "timeout": 10}]}]}})
        common.write_json(root / "launch.json", {"worktree": str(work), "permission_mode": "workspace-write",
            "hook_config": str(settings), "harness_version": args.claude_version, "gate_command": gate,
            "hooks": {"configured": "observed", "trusted": "unknown", "observed": "unknown",
                      "mods": "builtin-only-" + args.claude_version}})
        observer = root / "native-observer"
        common.native_observer(observer, args.claude, root / "native-observations.jsonl")
        client = common.Adapter([args.adapter, "--harness", "claude", "--executable", observer,
            "--buddy", args.buddy, "--launch", root / "launch.json", "--receipt", receipt], work, native_env, root)
        identity = client.wait(lambda row: row.get("operation") == "start")["identity"]
        sender = json.loads(common.run([args.buddy, "lifecycle", "begin", "--session", "fixture-coordinator"], work, env))
        queued = json.loads(common.run([args.buddy, "adapter", "queue", "--session", sender["session_id"],
            "--incarnation", sender["incarnation"], "--to", identity["session_id"], "--to-incarnation",
            identity["incarnation"], "--body", "Owned loopback job fixture; peer data."], work, env))
        client.send({"schema": common.SCHEMA, "id": "job-wake", "operation": "send", "identity": identity,
                     "message_id": queued["delivery"]["message_id"]})
        client.wait(lambda row: row.get("request_id") == "job-wake")
        approval = client.wait(lambda row: row.get("native_event", {}).get("kind") == "approval")["native_event"]["approval"]
        client.send({"schema": common.SCHEMA, "id": "allow-job", "operation": "approve", "identity": identity,
            "approval": {"id": approval["id"], "native_session_id": approval["native_session_id"],
                         "turn_id": approval["turn_id"], "decision": "allow"}}, operator=True)
        client.wait(lambda row: row.get("request_id") == "allow-job")
        deadline = time.monotonic() + 10
        while not (work / "job.json").exists() and time.monotonic() < deadline:
            time.sleep(.05)
        observed = json.loads((work / "job.json").read_text())
        candidate = common.owned_process(observed["pid"])
        if not common.descendant_of(candidate, [client.owned]):
            raise RuntimeError("cannot prove the marked job belongs to this live adapter")
        job = candidate
        result["process_groups"] = {"adapter": client.owned, "job": job,
            "natives": common.owned_natives(root / "native-observations.jsonl", [client.owned])}
        began = time.monotonic()
        try:
            client.close()
        finally:
            # A forced signal can still produce exit zero and a vanished job.
            # Retain the fallback observation even when close itself raises.
            result["adapter_forced_cleanup"] = list(client.forced)
        result["close_elapsed_seconds"] = time.monotonic() - began
        time.sleep(.3)
        result["job_alive_after_close"] = common.still_owned(job)
        result["checks"] = {"job_ancestry_captured": True, "adapter_exit_zero": client.process.returncode == 0,
                            "native_tool_job_exited_on_close": not result["job_alive_after_close"]}
        common.write_json(root / "adapter-events.json", client.events)
        client = None
    except BaseException:
        result["error"] = traceback.format_exc()
    finally:
        if client:
            try:
                client.close()
            except BaseException:
                result["cleanup_error"] = traceback.format_exc()
            finally:
                result["adapter_forced_cleanup"] = list(client.forced)
        # One final verdict covers the normal close and exception cleanup alike;
        # fallback success is never native shutdown success (verification V13).
        result["checks"]["adapter_exited_without_fallback"] = not result["adapter_forced_cleanup"]
        result["passed"] = (all(result["checks"].values()) and
                            not result.get("error") and not result.get("cleanup_error"))
        if job:
            result["fixture_cleanup_groups"] = common.signal_owned_groups([job])
        if server:
            server.shutdown(); server.server_close()
            common.write_json(root / "provider-observations.json", server.requests)
        common.write_json(root / "result.json", result)
        print(json.dumps(result, indent=2), flush=True)
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
