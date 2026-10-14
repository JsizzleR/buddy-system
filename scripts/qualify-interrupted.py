#!/usr/bin/env python3
"""Opt-in interrupted Codex recovery; fixed loopback responses, no real inference.

The fixture kills only its own process groups and drops one selected native
notification at an explicit proxy seam. Original journals are never edited.
Reconciliation reads native history by the journalled turn, and grants nothing.
"""
import argparse
import datetime
import importlib.util
import json
import os
from pathlib import Path
import shutil
import signal
import sys
import tempfile
import threading
import time
import traceback

sys.dont_write_bytecode = True
REPO = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("interrupted_recovery", REPO / "scripts/qualify-recovery.py")
recovery = importlib.util.module_from_spec(spec)
spec.loader.exec_module(recovery)
mixed, common = recovery.mixed, recovery.common
control = recovery.control


def observer(path, native, trace):
    # All retained protocol traffic belongs to this disposable fixture. No
    # existing session, credential or operator message passes through it.
    path.write_text("#!" + sys.executable + "\n" + '''import json,pathlib,subprocess,sys
p=subprocess.Popen([NATIVE]+sys.argv[1:],stdin=sys.stdin,stdout=subprocess.PIPE,stderr=sys.stderr)
with open(TRACE,'a') as log:
 def record(row):
  log.write(json.dumps(row)+'\\n');log.flush()
 record({'type':'buddy_fixture_native_process','pid':p.pid})
 for line in p.stdout:
  try:
   row=json.loads(line)
   record({'type':'native_frame','frame':row})
   if row.get('method')=='turn/completed' and pathlib.Path(DROP).exists():
    record({'type':'dropped_result','frame':row})
    pathlib.Path(DROPPED).write_text(json.dumps(row))
    continue
  except ValueError:pass
  sys.stdout.buffer.write(line);sys.stdout.buffer.flush()
sys.exit(p.wait())
'''.replace('NATIVE', repr(str(native))).replace('TRACE', repr(str(trace)))
       .replace('DROPPED', repr(str(path.parent / 'dropped.json')))
       .replace('DROP', repr(str(path.parent / 'drop-result'))))
    path.chmod(0o700)


def await_file(path):
    deadline = time.monotonic() + 20
    while not path.exists():
        if time.monotonic() > deadline:
            raise RuntimeError("missing fixture barrier: " + str(path))
        time.sleep(.05)
    return json.loads(path.read_text())


def restart(fleet, role, case):
    client = fleet.clients[role]
    state = fleet.root / (role + "-state")
    natives = common.owned_natives(state / "native-observations.jsonl", [client.owned])
    fleet.owned_natives = getattr(fleet, "owned_natives", []) + natives
    fault_groups = common.crash_owned(client, natives)
    client.close()
    fleet.clients.pop(role)
    case["crash"] = {"exit": client.process.returncode, "fault_groups": fault_groups, "events": client.events,
        "unparsed": client.unparsed, "children": common.observed_child_exit(state / "native-observations.jsonl")}
    if case["crash"]["children"]["status"] != "observed_absent":
        raise RuntimeError("owned native process survived fixture crash")
    case["ledger_after_crash"] = recovery.save_snapshot(fleet, role + "-crashed")
    attempt = state / "resume"
    attempt.mkdir()
    client = common.Adapter([fleet.binary, "--harness", "codex", "--executable", state / "native-observer",
        "--buddy", fleet.buddy, "--receipt", fleet.receipts[role], "--resume"],
        fleet.worktrees[role], dict(fleet.env, TMPDIR=str(state / "tmp")), attempt)
    fleet.clients[role] = client
    fleet.result["sessions"][role]["events"] = client.events
    case["resume"] = client.wait(lambda r: r.get("operation") == "resume")


def abandon_script(provider):
    # No application retry: a killed/interrupted native turn cannot finish its
    # fixed provider program. End that fixture expectation before fresh input.
    with provider._lock:
        provider._active = None
        provider._queued.clear()


def exercise(fleet):
    roles = ("interrupt", "active_crash", "approval_crash", "lost_result")
    for role in roles:
        identity = fleet.identities[role]
        fleet.run([fleet.buddy, "claim", role, "--session", identity["session_id"], "--scope", role + ".txt"], fleet.worktrees[role])
        case = {"identity": identity, "ledger_before": recovery.save_snapshot(fleet, role + "-before")}
        fleet.result["cases"][role] = case
        provider, client = fleet.providers[role], fleet.clients[role]
        case["queue"] = queued = fleet.queue(role, role, "Fixed interrupted recovery fixture; no new authority")
        mid = queued["delivery"]["message_id"]
        state = fleet.root / (role + "-state")
        release, arrived, finished = threading.Event(), threading.Event(), threading.Event()
        if role == "active_crash":
            original = provider._respond
            def held(request, path, original=original, release=release, arrived=arrived, finished=finished):
                response = original(request, path)
                arrived.set()
                if not release.wait(30):
                    raise RuntimeError("fixture response barrier expired")
                finished.set()
                return response
            provider._respond = held
            script = provider.queue_turn(role, [])
        elif role == "lost_result":
            (state / "drop-result").write_text("drop only turn/completed")
            script = provider.queue_turn(role, [fleet.shell(role, "once", "(work/'.fixture/once.txt').open('a').write('once\\n')")])
        else:
            script = provider.queue_turn(role, [fleet.shell(role, "unapproved", "(work/'.fixture/unapproved.txt').write_text('wrong')", require_approval=True)])
        case["wake"] = control(fleet, role, role + "-wake", "send", message_id=mid)
        turn = case["wake"]["wake_delivery"]["turn_id"]
        try:
            case["busy"] = client.wait(lambda r: r.get("native_event", {}).get("kind") == "busy")
            if role in ("interrupt", "approval_crash"):
                case["approval"] = client.wait(lambda r: r.get("native_event", {}).get("kind") == "approval")
                approval = case["approval"]["native_event"]["approval"]
                if not script.matches_approval(approval["detail"]):
                    raise RuntimeError("unexpected native approval")
                case["old_decision"] = dict(id=approval["id"], native_session_id=identity["session_id"], turn_id=turn, decision="allow")
            if role == "interrupt":
                case["interrupt"] = control(fleet, role, "interrupt-exact", "interrupt", turn_id=turn)
                case["result"] = client.wait(lambda r: r.get("native_event", {}).get("kind") == "result")
                case["idle"] = client.wait(lambda r: r.get("native_event", {}).get("kind") == "idle")
            else:
                if role == "active_crash" and not arrived.wait(15):
                    raise RuntimeError("provider request never reached active barrier")
                if role == "active_crash":
                    case["provider_barrier"] = arrived.is_set()
                if role == "lost_result":
                    case["dropped"] = await_file(state / "dropped.json")
                    script.assert_complete()
                    (state / "drop-result").unlink()
                # F-36: where the crash falls in the provider's own timeline. A
                # held request has no answer before this marker.
                provider.mark("fault")
                restart(fleet, role, case)
        finally:
            release.set()
            if role == "active_crash":
                finished.wait(5)
                provider._respond = original
        # Preserve expected connection loss at the fault seam separately; any
        # other provider errors still fail normal fixture cleanup.
        if role == "active_crash":
            deadline = time.monotonic() + 2
            while not provider.errors and time.monotonic() < deadline:
                time.sleep(.05)
            case["fault_provider_errors"] = list(provider.errors)
            provider.errors[:] = [e for e in provider.errors if e.get("error_type") != "connection_closed"]
        abandon_script(provider)
        # F-36: the replay window, as positions in the provider's timeline;
        # the checker counts the requests between them.
        case["requests_before"] = provider.mark("replay-window:open")
        case["reconcile"] = control(fleet, role, role + "-reconcile", "reconcile", permit_error=True, message_id=mid)
        case["duplicate"] = control(fleet, role, role + "-duplicate", "send", message_id=mid)
        if "old_decision" in case:
            case["replay"] = control(fleet, role, role + "-old-approval", "approve", operator=True,
                                     permit_error=True, approval=case["old_decision"])
        case["requests_after"] = provider.mark("replay-window:close")
        case["ledger_after_reconcile"] = recovery.save_snapshot(fleet, role + "-reconciled")
        # Fresh exact approval and inbox retrieval are positive controls after
        # each fault. Retrieve the original body without sending its wake again.
        extra = fleet.shell(role, "recover-body", f'''save("recovered.json",adapter("inbox","--message",{str(mid)!r}))
save("delivered.json",adapter("delivered","--message",{str(mid)!r}))
(work/'.fixture/fresh.txt').write_text('fresh')''', require_approval=True)
        fresh = fleet.queue(role, role, "Fresh fixture control; retrieve the old queued body explicitly")
        case["fresh_wake"], case["fresh_output"] = fleet.turn(role, "fresh-" + role, fresh, [extra])
        case["recovered"] = fleet.artifact(role, "recovered.json")
        case["delivered"] = fleet.artifact(role, "delivered.json")
        fleet.run([fleet.buddy, "release", role, "--session", identity["session_id"]], fleet.worktrees[role])
    fleet.result["ledger"] = recovery.save_snapshot(fleet, "final")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--codex", default=shutil.which("codex"))
    args = parser.parse_args()
    # D-080: the binaries this run was given are its candidates; every adapter it starts is bound to them.
    sys.modules["buddy_admission_lib"].bind_args(args)
    common.exit_on_signals()  # F-35: TERM/HUP still run the finally blocks
    root = Path(tempfile.mkdtemp(prefix="buddy-interrupted-", dir="/private/tmp"))
    print("ROOT=" + str(root), flush=True)
    result = {"schema": "buddy.interrupted.v1", "root": str(root), "provider": "deterministic-loopback",
        "started_at": datetime.datetime.now(datetime.timezone.utc).isoformat(), "passed": False,
        "sessions": {}, "cases": {}, "messages": [], "ready": [], "validations": [],
        "unqualified": ["Claude resume", "compaction", "lost wake reply without turn ID", "crash during approval write",
                        "arbitrary tool effects", "real-provider/model-directed recovery", "production installation"]}
    sources = ("qualify-interrupted.py", "check-interrupted.py", "qualify-recovery.py", "qualify-mixed-fleet.py",
               "mixed_fleet_provider.py", "qualify-claude.py", "qualify-codex.py", "check-mixed-fleet.py")
    result["fixture_sources"] = {name: common.digest(REPO / "scripts" / name) for name in sources}
    fleet = mixed.Fleet(root, result, args)
    try:
        fleet.preflight(("codex",))  # F-37: native-versions.json, bound by the checker
        fleet.prepare(roles=tuple((role, "codex", role) for role in ("interrupt", "active_crash", "approval_crash", "lost_result")), observer=observer)
        exercise(fleet)
    except Exception:
        result["error"] = traceback.format_exc()
    finally:
        fleet.close()
        if any(common.digest(REPO / "scripts" / name) != digest for name, digest in result["fixture_sources"].items()):
            result["error"] = result.get("error", "") + "\nFixture source changed during execution."
        if "error" not in result:
            try:
                validator = mixed.module("interrupted_validator", "check-interrupted.py")
                result["evidence_sha256"] = {str((root / path).relative_to(root)): common.digest(root / path)
                    for path in validator.evidence_paths(result)}
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
