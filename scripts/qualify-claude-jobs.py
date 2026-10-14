#!/usr/bin/env python3
"""Opt-in native Claude job-group probe; launches only an owned loopback fixture.

Run with explicit retained --claude, --buddy and --adapter artifacts. A native
Bash job may create another process group, so closing the adapter's owned group
is not proof that every tool job ended. Capture birth and ancestry while the
adapter lives, then record the result of Close.

THE CRITERIA ARE THE NARROW CONTRACT (D-077, the operator's choice of
2026-10-11, built 2026-10-12). They used to ask that the escaped job had ENDED
on close (`native_tool_job_exited_on_close`), which the contract deliberately
never does: the adapter signals only the group it pins, and a descendant that
left that group is OBSERVED, recorded and reported, never signalled. So the
2026-10-13 rerun was red 3/4 on a criterion the code is built not to meet,
while nothing checked what the contract does promise. Green now means all of:
  - the job escaped the worker's group (its pgid is not the leader's), with an
    ancestry proven to this live adapter;
  - the ownership record (<receipt>.proc) names it, by pid AND kernel start
    time, in descendants.alive, close.captured and close.survivors, and not in
    close.ended or close.unknown;
  - the adapter's stderr carries exactly one close line, naming exactly the
    recorded survivors' pids as "not signalled";
  - the job is still the same live process after Close (a signalled one is not);
  - the native group the adapter DOES pin ended, and the adapter exited 0 with
    no fallback signal from this fixture.
It is not always green: an adapter that signals the job, or one that leaves
the close out of its .proc record, is red here (both measured with built
mutants, 2026-10-13). Its limit, stated (Codex review 2026-10-13): "still the
same live process" is what can be observed, so a signal that does not end
the job (SIGCONT, SIGWINCH, one it handles) is invisible here. Nothing the
kernel offers an observer records signals delivered; the code's own tests
assert every signal it sends (descendants_test.go), and this suite does not
claim more than survival.

Afterwards the fixture ends its own job through the identity captured while
its ancestry was provable (pid AND start time, never a bare number), and must
then observe every recorded survivor gone: a run that leaves a stray process
behind is not a pass either. Every file the checker reads is hashed into the
receipt (evidence_sha256, F-37); check-claude-jobs.py validates it. No real
provider is used.
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

# Every file check-claude-jobs.py reads, hashed into the receipt (F-37).
EVIDENCE = ("worker.json", "worker.json.proc", "worker.json.events", "adapter.stderr", "adapter-events.json",
            "provider-observations.json", "native-observations.jsonl", "native-observer", "launch.json",
            "settings.json", "work/job.json")
CLOSE_PREFIX = "buddy-adapter: worker closed ("


def micros(start):
    """A [sec, usec] kernel start time as the .proc record spells it."""
    return start[0] * 1000000 + start[1]


def contract_checks(root, result, job):
    """The narrow contract's obligations, each read from what the run left.

    An unreadable or malformed piece of evidence is a False check, never an
    exception that skips the rest: result.json must still be written.
    """
    checks = {"job_ancestry_captured": job is not None,
              "adapter_exit_zero": result.get("adapter_exit") == 0,
              "adapter_exited_without_fallback": not result["adapter_forced_cleanup"]}

    def guarded(name, predicate):
        try:
            checks[name] = bool(predicate())
        except Exception as exc:  # noqa: BLE001 - recorded, never raised
            checks[name] = False
            result.setdefault("check_errors", {})[name] = "%s: %s" % (type(exc).__name__, exc)

    def proc():
        return json.loads((root / "worker.json.proc").read_text())

    def pairs(rows):
        return {(v["pid"], v["start"]) for v in rows}

    def ident():
        return (job["pid"], micros(job["start"]))

    guarded("job_escaped_worker_group", lambda: job["pgid"] != proc()["leader"]["pgid"] and
            job["pgid"] != result["process_groups"]["adapter"]["pgid"])
    guarded("escaped_job_recorded", lambda: ident() in pairs(proc()["descendants"]["alive"]) and
            ident() in pairs(proc()["close"]["captured"]) and ident() in pairs(proc()["close"]["survivors"]) and
            ident() not in pairs(proc()["close"]["ended"]) and proc()["close"]["unknown"] == [] and
            proc()["close"]["unattributable"] == "unknown" and proc()["close"]["trigger"] == "close")

    def reported():
        lines = [v for v in (root / "adapter.stderr").read_text().splitlines() if v.startswith(CLOSE_PREFIX)]
        survivors = sorted(v["pid"] for v in proc()["close"]["survivors"])
        named = "(pid %s; not signalled: they left the worker's group)" % ", ".join(str(p) for p in survivors)
        return (len(lines) == 1 and job["pid"] in survivors and named in lines[0] and
                (", %d still running " % len(survivors)) in lines[0])
    guarded("close_report_names_job_not_signalled", reported)
    guarded("job_not_signalled", lambda: result["job_alive_after_close"] is True)
    guarded("pinned_native_group_ended", lambda: bool(result["process_groups"]["natives"]) and
            result["natives_alive_after_close"] == [] and
            all(n["pgid"] == proc()["leader"]["pgid"] for n in result["process_groups"]["natives"]))

    def cleaned():
        # Every recorded survivor, by pid and start time, observed gone or recycled.
        deadline = time.monotonic() + 3
        while True:
            left = []
            for v in proc()["close"]["survivors"]:
                start = common.process_start(v["pid"])
                if start is not None and micros(start) == v["start"]:
                    left.append(v)
            if not left or time.monotonic() > deadline:
                break
            time.sleep(.05)
        result["survivors_after_fixture_cleanup"] = left
        return result.get("fixture_cleanup_groups") == [job["pgid"]] and not left
    guarded("fixture_cleanup_ended_survivors", cleaned)
    return checks


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("claude", "buddy", "adapter"):
        parser.add_argument("--" + name, required=True, type=pathlib.Path)
    parser.add_argument("--claude-version", choices=common.QUALIFICATION_VERSIONS, default="2.1.291")
    args = parser.parse_args()
    common.exit_on_signals()
    root = pathlib.Path(tempfile.mkdtemp(prefix="buddy-claude-jobs-", dir="/private/tmp"))
    print("ROOT=" + str(root), flush=True)
    result = {"schema": "buddy.claude-job-groups.v2", "root": str(root), "passed": False,
              "contract": "D-077 narrow contract: escaped descendants recorded and reported, never signalled",
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
        result["natives_alive_after_close"] = [n for n in result["process_groups"]["natives"] if common.still_owned(n)]
        result["adapter_exit"] = client.process.returncode
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
        if job:
            # The fixture's own cleanup, after every observation of Close: only
            # the group of the identity captured while ancestry was provable.
            result["fixture_cleanup_groups"] = common.signal_owned_groups([job])
        if server:
            server.shutdown(); server.server_close()
            common.write_json(root / "provider-observations.json", server.requests)
        result["checks"] = contract_checks(root, result, job)
        # One final verdict covers the normal close and exception cleanup alike;
        # fallback success is never native shutdown success (verification V13).
        result["passed"] = (all(result["checks"].values()) and
                            not result.get("error") and not result.get("cleanup_error"))
        result["evidence_sha256"] = {name: common.digest(root / name) for name in EVIDENCE if (root / name).is_file()}
        common.write_json(root / "result.json", result)
        print(json.dumps(result, indent=2), flush=True)
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
