#!/usr/bin/env python3
"""Opt-in Codex lost-wake and partial/forwarded approval recovery qualification.

Only disposable native sessions and deterministic loopback responses are used.
The proxy records actual wire traffic and stops selected delivery, never edits
an adapter journal. A native response written to the proxy is not proof that
the complete response reached Codex; both sides of that boundary are exercised.
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
spec = importlib.util.spec_from_file_location("uncertain_interrupted", REPO / "scripts/qualify-interrupted.py")
interrupted = importlib.util.module_from_spec(spec)
spec.loader.exec_module(interrupted)
recovery, mixed, common = interrupted.recovery, interrupted.mixed, interrupted.common
control = recovery.control
ROLES = ("lost_wake", "approval_partial", "approval_forwarded")


def observer(path, native, trace):
    # stdin and stdout are independently relayed: observing a native approval
    # response cannot accidentally require reading the still-blocked output.
    source = r'''import json,pathlib,subprocess,sys,threading
p=subprocess.Popen([NATIVE]+sys.argv[1:],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=sys.stderr)
root=pathlib.Path(TRACE).parent
lock=threading.Lock()
blocked=threading.Event()
def record(kind,**fields):
 with lock,open(TRACE,'a') as log:
  log.write(json.dumps(dict(type=kind,**fields))+'\n');log.flush()
def mark(name,row):
 tmp=root/(name+'.tmp')
 tmp.write_text(json.dumps(row));tmp.replace(root/(name+'.json'))
def mode():
 try:return (root/'fault-mode').read_text()
 except FileNotFoundError:return ''
record('buddy_fixture_native_process',pid=p.pid)
PROXY_INPUT
def inputs():
 for line in input_lines():
  row=json.loads(line)
  record('adapter_frame',frame=row)
  if mode().startswith('approval_') and 'method' not in row and 'result' in row:
   blocked.set()
   partial=mode()=='approval_partial'
   data=line[:len(line)//2] if partial else line
   p.stdin.write(data);p.stdin.flush()
   proof=dict(frame=row,bytes_written=len(data),frame_bytes=len(line),complete=not partial)
   record('approval_delivery',**proof);mark('approval-delivery',proof)
   if partial:
    input_stop.wait();return
   continue
  p.stdin.write(line);p.stdin.flush()
 p.stdin.close()
input_thread=threading.Thread(target=relay_inputs);input_thread.start()
for line in p.stdout:
 try:row=json.loads(line)
 except ValueError:
  sys.stdout.buffer.write(line);sys.stdout.buffer.flush();continue
 record('native_frame',frame=row)
 lost=mode()=='lost_wake'
 if lost and isinstance(row.get('result'),dict) and isinstance(row['result'].get('turn'),dict):
  blocked.set();record('dropped_wake',frame=row);mark('dropped-wake',row)
 if (lost or blocked.is_set()) and row.get('method')=='turn/completed':
  mark('native-completed',row)
 if lost or blocked.is_set():
  record('suppressed_frame',frame=row);continue
 sys.stdout.buffer.write(line);sys.stdout.buffer.flush()
code=p.wait()
input_stop.set();input_thread.join()
sys.exit(code)
'''
    path.write_text("#!" + sys.executable + "\n" + source.replace("PROXY_INPUT", common.proxy_input_source()).replace("NATIVE", repr(str(native))).replace("TRACE", repr(str(trace))))
    path.chmod(0o700)


def exercise(fleet):
    for role in ROLES:
        identity, client, provider = fleet.identities[role], fleet.clients[role], fleet.providers[role]
        state = fleet.root / (role + "-state")
        fleet.run([fleet.buddy, "claim", role, "--session", identity["session_id"], "--scope", role + ".txt"], fleet.worktrees[role])
        case = {"identity": identity, "ledger_before": recovery.save_snapshot(fleet, role + "-before")}
        fleet.result["cases"][role] = case
        case["queue"] = queued = fleet.queue(role, role, "Fixed uncertain recovery fixture; no new authority")
        mid = queued["delivery"]["message_id"]
        steps = [] if role == "lost_wake" else [fleet.shell(role, "effect",
            "(work/'.fixture/effect.txt').open('a').write('once\\n')", require_approval=True)]
        script = provider.queue_turn(role, steps)
        if role == "lost_wake":
            (state / "fault-mode").write_text(role)
            client.send(dict(schema="buddy.control.v1", id="lost-wake", operation="send", identity=identity, message_id=mid))
            case["dropped_wake"] = interrupted.await_file(state / "dropped-wake.json")
            case["native_completed"] = interrupted.await_file(state / "native-completed.json")
            script.assert_complete()
        else:
            case["wake"] = control(fleet, role, role + "-wake", "send", message_id=mid)
            case["approval"] = client.wait(lambda r: r.get("native_event", {}).get("kind") == "approval")
            approval = case["approval"]["native_event"]["approval"]
            if not script.matches_approval(approval["detail"]):
                raise RuntimeError("unexpected native approval")
            case["decision"] = dict(id=approval["id"], native_session_id=identity["session_id"],
                                    turn_id=approval["turn_id"], decision="allow")
            (state / "fault-mode").write_text(role)
            case["approval_response"] = control(fleet, role, role + "-approve", "approve", operator=True, approval=case["decision"])
            case["delivery_seam"] = interrupted.await_file(state / "approval-delivery.json")
            if role == "approval_forwarded":
                case["native_completed"] = interrupted.await_file(state / "native-completed.json")
                script.assert_complete()
        # Restart only our process group. Resume appends to the original journal;
        # its prefix before the resume record remains independently inspectable.
        (state / "fault-mode").unlink()
        interrupted.restart(fleet, role, case)
        interrupted.abandon_script(provider)
        case["requests_before"] = len(provider.requests)
        case["trace_before"] = len((state / "native-observations.jsonl").read_text().splitlines())
        case["reconcile"] = control(fleet, role, role + "-reconcile", "reconcile", permit_error=True, message_id=mid)
        case["duplicate"] = control(fleet, role, role + "-duplicate", "send", message_id=mid)
        if "decision" in case:
            case["replay"] = control(fleet, role, role + "-replay", "approve", operator=True, permit_error=True, approval=case["decision"])
        case["trace_after"] = len((state / "native-observations.jsonl").read_text().splitlines())
        case["requests_after"] = len(provider.requests)
        case["ledger_after_reconcile"] = recovery.save_snapshot(fleet, role + "-reconciled")
        # New work is a positive control, not a retry of uncertain effects.
        extra = fleet.shell(role, "fresh", f'''save("recovered.json",adapter("inbox","--message",{str(mid)!r}))
save("delivered.json",adapter("delivered","--message",{str(mid)!r}))
(work/'.fixture/fresh.txt').write_text('fresh')''', require_approval=True)
        fresh = fleet.queue(role, role, "Fresh control: explicitly retrieve original body; do not repeat its effects")
        case["fresh_wake"], case["fresh_output"] = fleet.turn(role, "fresh-" + role, fresh, [extra])
        case["recovered"], case["delivered"] = fleet.artifact(role, "recovered.json"), fleet.artifact(role, "delivered.json")
        fleet.run([fleet.buddy, "release", role, "--session", identity["session_id"]], fleet.worktrees[role])
    fleet.result["ledger"] = recovery.save_snapshot(fleet, "final")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--codex", default=shutil.which("codex"))
    args = parser.parse_args()
    # D-080: the binaries this run was given are its candidates; every adapter it starts is bound to them.
    sys.modules["buddy_admission_lib"].bind_args(args)
    common.exit_on_signals()  # F-35: TERM/HUP still run the finally blocks
    root = Path(tempfile.mkdtemp(prefix="buddy-uncertain-", dir="/private/tmp"))
    print("ROOT=" + str(root), flush=True)
    result = {"schema": "buddy.uncertain.v1", "root": str(root), "provider": "deterministic-loopback",
        "started_at": datetime.datetime.now(datetime.timezone.utc).isoformat(), "passed": False,
        "sessions": {}, "cases": {}, "messages": [], "ready": [], "validations": [],
        "unqualified": ["Claude resume", "compaction", "arbitrary crash offsets/tool effects",
                        "native crash before approval submission receipt", "real-provider/model-directed recovery", "production installation"]}
    sources = ("qualify-uncertain.py", "check-uncertain.py", "qualify-interrupted.py", "qualify-recovery.py",
               "qualify-mixed-fleet.py", "mixed_fleet_provider.py", "qualify-claude.py", "qualify-codex.py", "check-mixed-fleet.py")
    result["fixture_sources"] = {name: common.digest(REPO / "scripts" / name) for name in sources}
    fleet = mixed.Fleet(root, result, args)
    try:
        fleet.preflight(("codex",))  # F-37: native-versions.json, bound by the checker
        fleet.prepare(roles=tuple((role, "codex", role) for role in ROLES), observer=observer)
        exercise(fleet)
    except Exception:
        result["error"] = traceback.format_exc()
    finally:
        fleet.close()
        if any(common.digest(REPO / "scripts" / name) != digest for name, digest in result["fixture_sources"].items()):
            result["error"] = result.get("error", "") + "\nFixture source changed during execution."
        if "error" not in result:
            try:
                validator = mixed.module("uncertain_validator", "check-uncertain.py")
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
