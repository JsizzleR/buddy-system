#!/usr/bin/env python3
"""Opt-in completed-turn Claude resume, with disposable loopback state only.

Fixed native tools exercise the captured incarnation, replay guards and actual
hook after process replacement. No provider credentials, installation, global
settings or existing sessions are used. Failed attempts retain their receipts.
"""
import argparse
import datetime
import importlib.util
import json
import shutil
import sys
import tempfile
import traceback
from pathlib import Path

sys.dont_write_bytecode = True
REPO = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("claude_resume_fleet", REPO / "scripts/qualify-mixed-fleet.py")
mixed = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mixed)
common = mixed.common
ROLE = "claude_worker"


def observer(path, native, trace):
    # Both directions and exact launch argv are retained. Only the disposable
    # worker's fixed fixture traffic is captured, never an operator session.
    source = r'''import json,subprocess,sys,threading
p=subprocess.Popen(["claude"]+sys.argv[1:],executable=NATIVE,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=sys.stderr)
lock=threading.Lock()
def record(kind,**fields):
 with lock,open(TRACE,'a') as log:
  log.write(json.dumps(dict(type=kind,**fields))+'\n');log.flush()
record('buddy_fixture_native_process',pid=p.pid,args=sys.argv[1:])
PROXY_INPUT
def inputs():
 try:
  for line in input_lines():
   record('adapter_frame',frame=json.loads(line))
   p.stdin.write(line);p.stdin.flush()
  p.stdin.close()
 except BrokenPipeError:pass
input_thread=threading.Thread(target=relay_inputs);input_thread.start()
for line in p.stdout:
 try:record('native_frame',frame=json.loads(line))
 except ValueError:pass
 sys.stdout.buffer.write(line);sys.stdout.buffer.flush()
code=p.wait()
input_stop.set();input_thread.join()
sys.exit(code)
'''
    path.write_text("#!" + sys.executable + "\n" + source.replace("PROXY_INPUT", common.proxy_input_source()).replace("NATIVE", repr(str(native))).replace("TRACE", repr(str(trace))))
    path.chmod(0o700)


def control(fleet, name, operation, operator=False, permit_error=False, **fields):
    client = fleet.clients[ROLE]
    client.send(dict(schema="buddy.control.v1", id=name, operation=operation,
                     identity=fleet.identities[ROLE], **fields), operator=operator)
    return client.wait(lambda r: r.get("request_id") == name, permit_error=permit_error)


def snap(fleet, name):
    return mixed.snapshot(fleet.database, fleet.root / (name + ".db"))


def native_env(fleet, native_state=None):
    state = fleet.root / (ROLE + "-state")
    return dict(fleet.env, TMPDIR=str(state / "tmp"), CLAUDE_CONFIG_DIR=str(native_state or state / "native"),
        ANTHROPIC_API_KEY="buddy-loopback-fixture-not-a-provider-key",
        ANTHROPIC_BASE_URL=fleet.providers[ROLE].base_url, CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1",
        DISABLE_AUTOUPDATER="1", DISABLE_TELEMETRY="1", DISABLE_ERROR_REPORTING="1", CLAUDE_CODE_DISABLE_AUTO_MEMORY="1")


def resume_client(fleet, name, native_state=None):
    state = fleet.root / (ROLE + "-state")
    attempt = state / name
    attempt.mkdir()
    return common.Adapter([fleet.binary, "--harness", "claude", "--executable", state / "native-observer",
        "--buddy", fleet.buddy, "--receipt", fleet.receipts[ROLE], "--resume"],
        fleet.worktrees[ROLE], native_env(fleet, native_state), attempt)


def exercise(fleet):
    from mixed_fleet_provider import write
    ident, work = fleet.identities[ROLE], fleet.worktrees[ROLE]
    sid = ident["session_id"]
    fleet.run([fleet.buddy, "hello", "--session", "fixture-owner"])
    fleet.run([fleet.buddy, "claim", "run-resume", "--session", "fixture-owner", "--scope", ".buddy/slot/resume"])
    fleet.run([fleet.buddy, "claim", "guard", "--session", "fixture-owner", "--scope", "codex.txt"])
    fleet.run([fleet.buddy, "claim", "worker", "--session", sid, "--scope", "claude.txt"], work)
    fleet.run([fleet.buddy, "wait", "--session", sid, "--on", "run-resume", "--ready", "HEAD"], work)
    fleet.result["ready_sha"] = fleet.run(["git", "rev-parse", "HEAD"], work).strip()
    queued = fleet.queue(ROLE, ROLE, "Original body stays queued through completed native turn and resume.")
    wake, output = fleet.turn(ROLE, "before-resume", queued, [fleet.shell(ROLE, "original", "(work/'.fixture/original.txt').write_text('once')", require_approval=True)])
    old_events = fleet.clients[ROLE].events
    answer = next(r for r in old_events if r.get("approval_delivery", {}).get("state") == "submitted")
    fleet.result["original"] = dict(queue=queued, wake=wake, output=output, approval=answer)
    fleet.result["before_resume"] = snap(fleet, "before-resume")
    original = fleet.clients.pop(ROLE)
    original.close()
    state = fleet.root / (ROLE + "-state")
    fleet.result["restart"] = {"before": {"events": original.events, "exit": original.process.returncode,
        "unparsed": original.unparsed, "children": common.observed_child_exit(state / "native-observations.jsonl")}}
    if original.process.returncode or original.unparsed:
        raise RuntimeError("original adapter did not close cleanly")
    fleet.result["requests_at_close"] = len(fleet.providers[ROLE].requests)
    # A saved Buddy receipt cannot substitute for missing native conversation
    # state. Use an empty disposable native directory, then the intact positive.
    empty = state / "empty-native"
    empty.mkdir()
    failed = resume_client(fleet, "missing-history", empty)
    try:
        refusal = failed.wait(lambda r: r.get("operation") == "resume", permit_error=True)
        fleet.result["missing_history"] = refusal
        if not refusal.get("error"):
            raise RuntimeError("missing native history admitted resume")
    finally:
        failed.close()
        fleet.result["missing_history_exit"] = failed.process.returncode
    client = resume_client(fleet, "resume")
    fleet.clients[ROLE] = client
    fleet.result["sessions"][ROLE]["events"] = client.events
    fleet.result["restart"]["resume"] = client.wait(lambda r: r.get("operation") == "resume")
    fleet.result["after_resume"] = snap(fleet, "after-resume")
    fleet.result["requests_before"] = len(fleet.providers[ROLE].requests)
    fleet.result["duplicate"] = control(fleet, "duplicate", "send", message_id=queued["delivery"]["message_id"])
    fleet.result["replay"] = control(fleet, "old-approval", "approve", operator=True, permit_error=True,
        approval=answer["approval_response"])
    fleet.result["inspect"] = control(fleet, "resumed-inspect", "inspect")
    fleet.result["requests_after"] = len(fleet.providers[ROLE].requests)
    mid = queued["delivery"]["message_id"]
    fetch = fleet.shell(ROLE, "fetch-old", f'''save("old-inbox.json",adapter("inbox","--message",{str(mid)!r}))
save("old-delivered.json",adapter("delivered","--message",{str(mid)!r}))
assert (work/'claude.txt').read_text()=='allowed\\n'
assert (work/'codex.txt').read_text()=='original\\n'
assert (work/'.fixture/original.txt').read_text()=='once'
(work/'.fixture/fresh.txt').write_text('fresh')''')
    fresh = fleet.queue(ROLE, ROLE, "Fresh fixed tools after resume, explicitly retrieve old body.")
    fleet.result["fresh_wake"], fleet.result["fresh_output"] = fleet.turn(ROLE, "after-resume", fresh, [
        write(work / "codex.txt", "forbidden\n"), write(work / "claude.txt", "allowed\n"), fetch])
    fleet.result["old_inbox"] = fleet.artifact(ROLE, "old-inbox.json")
    fleet.result["old_delivered"] = fleet.artifact(ROLE, "old-delivered.json")
    fleet.result["after_fresh"] = snap(fleet, "after-fresh")
    for slug, owner in (("worker", sid), ("guard", "fixture-owner"), ("run-resume", "fixture-owner")):
        fleet.run([fleet.buddy, "release", slug, "--session", owner])
    fleet.result["ledger"] = snap(fleet, "final")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--claude", default=shutil.which("claude"))
    parser.add_argument("--claude-version", choices=common.QUALIFICATION_VERSIONS, default=common.VERSION)
    args = parser.parse_args()
    common.exit_on_signals()  # F-35: TERM/HUP still run the finally blocks
    root = Path(tempfile.mkdtemp(prefix="buddy-claude-resume-", dir="/private/tmp"))
    print("ROOT=" + str(root), flush=True)
    result = {"schema": "buddy.claude-resume.v1", "root": str(root), "provider": "deterministic-loopback",
        "claude_version": args.claude_version,
        "started_at": datetime.datetime.now(datetime.timezone.utc).isoformat(), "passed": False,
        "sessions": {}, "messages": [], "ready": [], "validations": [],
        "unqualified": ["Claude interrupted recovery/compaction", "native history reconciliation", "arbitrary effects",
            "real-provider/model-directed recovery", "production installation"]}
    sources = ("qualify-claude-resume.py", "check-claude-resume.py", "qualify-mixed-fleet.py",
               "mixed_fleet_provider.py", "qualify-claude.py", "qualify-codex.py", "check-mixed-fleet.py")
    result["fixture_sources"] = {name: common.digest(REPO / "scripts" / name) for name in sources}
    fleet = mixed.Fleet(root, result, args)
    try:
        fleet.prepare(roles=((ROLE, "claude", "resume-work"),), observer=observer)
        exercise(fleet)
    except Exception:
        result["error"] = traceback.format_exc()
    finally:
        fleet.close()
        if any(common.digest(REPO / "scripts" / name) != digest for name, digest in result["fixture_sources"].items()):
            result["error"] = result.get("error", "") + "\nFixture source changed during execution."
        if "error" not in result:
            try:
                validator = mixed.module("claude_resume_validator", "check-claude-resume.py")
                result["evidence_sha256"] = {str((root / p).relative_to(root)): common.digest(root / p)
                    for p in validator.evidence_paths(result)}
                result["validation"] = validator.validate(result)
                result["passed"] = result["validation"]["passed"] and not result["cleanup_errors"]
            except Exception:
                result["error"] = traceback.format_exc()
        common.write_json(root / "result.json", result)
        print("RECEIPT=" + str(root / "result.json"), flush=True)
        print(json.dumps({k: result.get(k) for k in ("passed", "validation", "error", "cleanup_errors")}, indent=2), flush=True)
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
