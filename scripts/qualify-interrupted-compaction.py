#!/usr/bin/env python3
"""Opt-in interrupted Codex compaction with deterministic loopback responses.

Cancellation, an owned process-group crash and lost completion notifications
are distinct fault seams. No receipt or native history is edited. The proxy's
manual compact/history requests are fixture controls, not new adapter verbs.
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
spec = importlib.util.spec_from_file_location("interrupted_compaction", REPO / "scripts/qualify-compaction.py")
compact = importlib.util.module_from_spec(spec)
spec.loader.exec_module(compact)
recovery, mixed, common = compact.recovery, compact.mixed, compact.common
ROLE = "coordinator"
MODES = ("cancel", "active_crash", "lost_completion")


def observer(path, native, trace):
    # Only private fixture commands enter here. Preserve both original frames
    # and explicit drop records, so missing journal entries alone prove nothing.
    source = r'''import json,pathlib,subprocess,sys,threading
p=subprocess.Popen([NATIVE]+sys.argv[1:],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=sys.stderr)
root=pathlib.Path(TRACE).parent
loglock=threading.Lock();writelock=threading.Lock();stop=threading.Event()
def record(kind,**fields):
 with loglock,open(TRACE,'a') as log:
  log.write(json.dumps(dict(type=kind,**fields))+'\n');log.flush()
def write(row,kind):
 with writelock:
  # inputs() closes p.stdin at the adapter's EOF; an injection after that
  # is a frame the native can never receive, not a crash (F-38).
  if p.stdin.closed:raise BrokenPipeError('native stdin already closed')
  record(kind,frame=row)
  p.stdin.write((json.dumps(row)+'\n').encode());p.stdin.flush()
def save(name,row):
 tmp=root/(name+'.tmp');tmp.write_text(json.dumps(row));tmp.replace(root/(name+'.json'))
PROXY_INPUT
def inputs():
 for line in input_lines():
  try:write(json.loads(line),'adapter_frame')
  except BrokenPipeError:return
 with writelock:p.stdin.close()
def requests():
 request=root/'fixture-request.json'
 while not stop.wait(.02):
  if request.exists():
   row=json.loads(request.read_text())
   if (row.get('id'),row.get('method')) not in (('fixture-compact','thread/compact/start'),('fixture-history','thread/read')):raise RuntimeError('invalid fixture request')
   request.rename(root/(row['id']+'-taken.json'))
   try:write(row,'fixture_frame')
   except BrokenPipeError:return
record('buddy_fixture_native_process',pid=p.pid)
input_thread=threading.Thread(target=relay_inputs);input_thread.start()
# NOT a daemon (F-38): it writes p.stdin, and a daemon abandoned mid-write
# holds that BufferedWriter's lock while Python finalizes, which aborts the
# proxy (the crash class proxy_input_source's relay was fixed for). Stopped
# and joined before exit, on the error path too.
inject_thread=threading.Thread(target=requests);inject_thread.start()
try:
 for line in p.stdout:
  try:row=json.loads(line)
  except ValueError:
   sys.stdout.buffer.write(line);sys.stdout.buffer.flush();continue
  record('native_frame',frame=row)
  if row.get('id') in ('fixture-compact','fixture-history'):
   save(row['id']+'-reply',row);continue
  params=row.get('params',{})
  drop=root/'drop-completion'
  if drop.exists() and (row.get('method')=='turn/completed' or (row.get('method')=='item/completed' and params.get('item',{}).get('type')=='contextCompaction')):
   record('dropped_frame',frame=row)
   save('dropped-result' if row['method']=='turn/completed' else 'dropped-item',row)
   continue
  sys.stdout.buffer.write(line);sys.stdout.buffer.flush()
except BaseException:
 stop.set();input_stop.set();raise
stop.set()
code=p.wait()
input_stop.set();input_thread.join()
inject_thread.join()
sys.exit(code)
'''
    path.write_text("#!" + sys.executable + "\n" + source.replace("PROXY_INPUT", common.proxy_input_source()).replace("NATIVE", repr(str(native))).replace("TRACE", repr(str(trace))))
    path.chmod(0o700)


def await_file(path):
    deadline = time.monotonic() + 20
    while not path.exists():
        if time.monotonic() > deadline:
            raise RuntimeError("missing fixture barrier: " + str(path))
        time.sleep(.02)
    return json.loads(path.read_text())


def fixture_request(state, request):
    common.write_json(state / "fixture-request.tmp", request)
    (state / "fixture-request.tmp").rename(state / "fixture-request.json")


def resume(fleet, mode):
    client = fleet.clients[ROLE]
    state = fleet.root / (ROLE + "-state")
    natives = common.owned_natives(state / "native-observations.jsonl", [client.owned])
    fleet.owned_natives = getattr(fleet, "owned_natives", []) + natives
    fault_groups = []
    if mode != "cancel":
        fault_groups = common.crash_owned(client, natives)
    client.close()
    fleet.clients.pop(ROLE)
    before = {"events": client.events, "fault_groups": fault_groups, "exit": client.process.returncode, "unparsed": client.unparsed,
              "children": common.observed_child_exit(state / "native-observations.jsonl")}
    fleet.result["restart"] = {"before": before}
    if before["exit"] != (0 if mode == "cancel" else -signal.SIGKILL) or before["unparsed"] or before["children"]["status"] != "observed_absent":
        raise RuntimeError("old native process shutdown differs from fault seam")
    fleet.result["after_fault"] = recovery.save_snapshot(fleet, "after-fault")
    attempt = state / "resume"
    attempt.mkdir()
    client = common.Adapter([fleet.binary, "--harness", "codex", "--executable", state / "native-observer",
        "--buddy", fleet.buddy, "--receipt", fleet.receipts[ROLE], "--resume"],
        fleet.worktrees[ROLE], dict(fleet.env, TMPDIR=str(state / "tmp")), attempt)
    fleet.clients[ROLE] = client
    fleet.result["sessions"][ROLE]["events"] = client.events
    fleet.result["restart"]["resume"] = client.wait(lambda r: r.get("operation") == "resume")


def exercise(fleet, mode):
    from mixed_fleet_provider import patch
    r, work = fleet.result, fleet.worktrees[ROLE]
    session = fleet.identities[ROLE]["session_id"]
    fleet.run([fleet.buddy, "hello", "--session", "fixture-owner"])
    for slug, scope, owner in (("run", ".buddy/slot/compact", "fixture-owner"),
                               ("guard", "claude.txt", "fixture-owner"), ("worker", "codex.txt", session)):
        fleet.run([fleet.buddy, "claim", slug, "--session", owner, "--scope", scope], work)
    fleet.run([fleet.buddy, "wait", "--session", session, "--on", "run", "--ready", "HEAD"], work)
    r["ready_sha"] = fleet.run(["git", "rev-parse", "HEAD"], work).strip()
    queued, wake, decision = recovery.approval_round(fleet)
    mid = queued["delivery"]["message_id"]
    r["before_compact"] = recovery.save_snapshot(fleet, "before-compact")
    provider, client = fleet.providers[ROLE], fleet.clients[ROLE]
    state = fleet.root / (ROLE + "-state")
    release, arrived, finished = threading.Event(), threading.Event(), threading.Event()
    original = provider._respond
    def held(request, path):
        response = original(request, path)
        arrived.set()
        if not release.wait(45):
            raise RuntimeError("fixture compaction barrier expired")
        finished.set()
        return response
    if mode == "lost_completion":
        (state / "drop-completion").write_text("drop compact item and turn completion only")
    else:
        provider._respond = held
    script = provider.queue_turn("compact-summary", [], final_text="Fixed fixture summary. Retrieve Buddy state explicitly; no new authority.")
    request = {"id": "fixture-compact", "method": "thread/compact/start", "params": {"threadId": session}}
    fixture_request(state, request)
    r["compaction"] = {"request": request}
    try:
        started = client.wait(lambda v: v.get("native_event", {}).get("kind") == "compaction_started", timeout=55)
        r["compaction"].update(started=started, reply=await_file(state / "fixture-compact-reply.json"))
        turn = started["native_event"]["turn_id"]
        if mode == "lost_completion":
            r["fault"] = {"dropped_result": await_file(state / "dropped-result.json"),
                          "dropped_item": await_file(state / "dropped-item.json")}
            script.assert_complete()
            (state / "drop-completion").unlink()
        else:
            if not arrived.wait(15):
                raise RuntimeError("compaction provider request did not reach barrier")
            r["fault"] = {"provider_barrier": arrived.is_set(), "response_released_at_fault": release.is_set()}
            if mode == "cancel":
                r["fault"]["wrong_interrupt"] = recovery.control(fleet, ROLE, "wrong-interrupt", "interrupt", turn_id=turn + "-wrong", permit_error=True)
                r["fault"]["interrupt"] = recovery.control(fleet, ROLE, "compact-interrupt", "interrupt", turn_id=turn)
                r["fault"]["result"] = client.wait(lambda v: v.get("native_event", {}).get("kind") == "result" and v["native_event"]["turn_id"] == turn)
                r["fault"]["idle"] = client.wait(lambda v: v.get("native_event", {}).get("kind") == "idle" and v["native_event"]["turn_id"] == turn)
        resume(fleet, mode)
    finally:
        release.set()
        if mode != "lost_completion":
            finished.wait(5)
            provider._respond = original
    if mode != "lost_completion":
        # The severed fixture HTTP stream is the expected fault; retain it and
        # fail every other provider error rather than suppressing all errors.
        deadline = time.monotonic() + 2
        while not provider.errors and time.monotonic() < deadline:
            time.sleep(.02)
        r["fault"]["provider_errors"] = list(provider.errors)
        provider.errors[:] = [e for e in provider.errors if e.get("error_type") != "connection_closed"]
    r["compaction"]["script"] = script.snapshot()
    r["after_resume"] = recovery.save_snapshot(fleet, "after-resume")
    r["requests_before"] = len(provider.requests)
    r["duplicate"] = recovery.control(fleet, ROLE, "old-wake", "send", message_id=mid)
    r["replay"] = recovery.control(fleet, ROLE, "old-approval", "approve", operator=True, permit_error=True, approval=decision)
    r["reconcile"] = recovery.control(fleet, ROLE, "old-history", "reconcile", permit_error=True, message_id=mid)
    r["after_reconcile"] = recovery.save_snapshot(fleet, "after-reconcile")
    # A separate exact native history capture describes the compact turn. It
    # must not be confused with adapter reconciliation of the original wake.
    fixture_request(state, {"id": "fixture-history", "method": "thread/read", "params": {"threadId": session, "includeTurns": True}})
    r["compaction_history"] = await_file(state / "fixture-history-reply.json")
    r["requests_after"] = len(provider.requests)
    conflict = patch("*** Begin Patch\n*** Update File: codex.txt\n@@\n-original\n+forbidden\n*** Update File: claude.txt\n@@\n-original\n+forbidden\n*** End Patch")
    allowed = patch("*** Begin Patch\n*** Update File: codex.txt\n@@\n-original\n+allowed\n*** End Patch")
    verify = fleet.shell(ROLE, "check-denial", "assert (work/'codex.txt').read_text()=='original\\n'\nassert (work/'claude.txt').read_text()=='original\\n'", require_approval=True)
    fetch = fleet.shell(ROLE, "fetch-old", f'''save("old-inbox.json",adapter("inbox","--message",{str(mid)!r}))
save("old-delivered.json",adapter("delivered","--message",{str(mid)!r}))
assert (work/'codex.txt').read_text()=='allowed\\n'
assert (work/'claude.txt').read_text()=='original\\n'
(work/'.fixture/fresh.txt').write_text('fresh')''')
    fresh = fleet.queue(ROLE, ROLE, "Fresh fixed tools after interrupted compaction; retrieve old body explicitly without repeating work.")
    r["fresh_wake"], r["fresh_output"] = fleet.turn(ROLE, "post-fault", fresh, [conflict, verify, allowed, fetch])
    r["old_inbox"], r["old_delivered"] = fleet.artifact(ROLE, "old-inbox.json"), fleet.artifact(ROLE, "old-delivered.json")
    r["after_fresh"] = recovery.save_snapshot(fleet, "after-fresh")
    for slug, owner in (("worker", session), ("guard", "fixture-owner"), ("run", "fixture-owner")):
        fleet.run([fleet.buddy, "release", slug, "--session", owner])
    r["ledger"] = recovery.save_snapshot(fleet, "final")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--codex", default=shutil.which("codex"))
    args = parser.parse_args()
    common.exit_on_signals()  # F-35: TERM/HUP still run the finally blocks
    root = Path(tempfile.mkdtemp(prefix="buddy-interrupted-compaction-", dir="/private/tmp"))
    print("ROOT=" + str(root), flush=True)
    report = {"schema": "buddy.interrupted-compaction-suite.v1", "root": str(root), "cases": {}, "passed": False}
    sources = ("qualify-interrupted-compaction.py", "check-interrupted-compaction.py", "qualify-compaction.py",
        "qualify-recovery.py", "qualify-mixed-fleet.py", "mixed_fleet_provider.py", "qualify-claude.py", "qualify-codex.py", "check-mixed-fleet.py")
    validator = mixed.module("interrupted_compaction_validator", "check-interrupted-compaction.py")
    for mode in MODES:
        case_root = root / mode
        case_root.mkdir()
        r = {"schema": "buddy.interrupted-compaction.v1", "root": str(case_root), "mode": mode,
            "provider": "deterministic-loopback", "started_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
            "sessions": {}, "messages": [], "ready": [], "validations": [], "passed": False,
            "unqualified": ["automatic thresholds", "arbitrary crash offsets/tool effects", "Claude resume/compaction",
                            "real-provider summary quality/model-directed recovery", "production installation"],
            "fixture_sources": {name: common.digest(REPO / "scripts" / name) for name in sources}}
        report["cases"][mode] = r
        fleet = mixed.Fleet(case_root, r, args)
        try:
            fleet.prepare(roles=((ROLE, "codex", "compact-work"),), observer=observer)
            exercise(fleet, mode)
        except Exception:
            r["error"] = traceback.format_exc()
        finally:
            fleet.close()
            if any(common.digest(REPO / "scripts" / name) != digest for name, digest in r["fixture_sources"].items()):
                r["error"] = r.get("error", "") + "\nFixture source changed during execution."
            if "error" not in r:
                try:
                    r["evidence_sha256"] = {str((case_root / p).relative_to(case_root)): common.digest(case_root / p)
                        for p in validator.evidence_paths(r)}
                    r["validation"] = validator.validate_case(r)
                    r["passed"] = r["validation"]["passed"] and not r["cleanup_errors"]
                except Exception:
                    r["error"] = traceback.format_exc()
            common.write_json(case_root / "result.json", r)
            print(json.dumps({"case": mode, **{k: r.get(k) for k in ("passed", "validation", "error", "cleanup_errors")}}, indent=2), flush=True)
    report["passed"] = all(r["passed"] for r in report["cases"].values())
    common.write_json(root / "result.json", report)
    print("RECEIPT=" + str(root / "result.json"), flush=True)
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
