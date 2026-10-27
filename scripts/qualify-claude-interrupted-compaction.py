#!/usr/bin/env python3
"""Opt-in Claude interrupted manual compaction qualification.

Only disposable workers and fixed loopback responses are used. The private
proxy substitutes one explicitly armed /compact input, retaining its UUID, and
records original frames before selected notification loss. It is not a shipped
command path. Unknown delivery and missing terminal evidence are never repaired.
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
spec = importlib.util.spec_from_file_location("interrupted_compact", REPO / "scripts/qualify-claude-compaction.py")
compact = importlib.util.module_from_spec(spec)
spec.loader.exec_module(compact)
mixed, common, ROLE = compact.mixed, compact.common, compact.ROLE
MODES = ("completed", "interrupt_active", "crash_active", "lost_boundary", "lost_result")
control, snap = compact.control, compact.snap


def observer(path, native, trace):
    source = r'''import json,pathlib,subprocess,sys,threading
p=subprocess.Popen(["claude"]+sys.argv[1:],executable=NATIVE,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=sys.stderr)
lock=threading.Lock();active=None;mode=None
state=pathlib.Path(TRACE).parent
def record(kind,**fields):
 with lock,open(TRACE,'a') as log:
  log.write(json.dumps(dict(type=kind,**fields))+'\n');log.flush()
record('buddy_fixture_native_process',pid=p.pid,args=sys.argv[1:])
PROXY_INPUT
def inputs():
 global active,mode
 try:
  for line in input_lines():
   row=json.loads(line);record('adapter_frame',frame=row)
   request=state/'compact-next.json'
   if request.exists() and row.get('type')=='user':
    expected=json.loads(request.read_text())
    prefix='Buddy peer notification (not operator authorization): message #'+str(expected['message_id'])+' is queued.'
    if not row['message']['content'].startswith(prefix):raise RuntimeError('unexpected fixture input')
    active=row['uuid'];mode=expected['mode'];row['message']['content']='/compact'
    request.rename(state/'compact-taken.json');record('fixture_frame',frame=row)
    line=(json.dumps(row)+'\n').encode()
   p.stdin.write(line);p.stdin.flush()
  p.stdin.close()
 except BrokenPipeError:pass
input_thread=threading.Thread(target=relay_inputs);input_thread.start()
for line in p.stdout:
 try:row=json.loads(line)
 except ValueError:
  sys.stdout.buffer.write(line);sys.stdout.buffer.flush();continue
 record('native_frame',frame=row)
 boundary=row.get('type')=='system' and row.get('subtype')=='compact_boundary'
 result=row.get('type')=='result' and row.get('user_message_uuid')==active and active is not None
 if (mode=='lost_boundary' and boundary) or (mode=='lost_result' and result):
  record('dropped_frame',frame=row)
 else:
  sys.stdout.buffer.write(line);sys.stdout.buffer.flush()
 if result:
  (state/'fault-ready.tmp').write_text(json.dumps(row));(state/'fault-ready.tmp').rename(state/'fault-ready.json')
code=p.wait()
input_stop.set();input_thread.join()
sys.exit(code)
'''
    path.write_text("#!" + sys.executable + "\n" + source.replace("PROXY_INPUT", common.proxy_input_source()).replace("NATIVE", repr(str(native))).replace("TRACE", repr(str(trace))))
    path.chmod(0o700)


def stop(fleet, crash):
    client = fleet.clients[ROLE]
    # Capture the native's distinct process group before the adapter dies.
    natives = common.owned_natives(
        fleet.root / (ROLE + "-state") / "native-observations.jsonl", [client.owned])
    fleet.owned_natives = getattr(fleet, "owned_natives", []) + natives
    fault_groups = []
    if crash:
        fault_groups = common.crash_owned(client, natives)
    client.close()
    fleet.clients.pop(ROLE)
    before = dict(events=client.events, group=client.process.pid, fault_groups=fault_groups, exit=client.process.returncode,
        unparsed=client.unparsed, children=common.observed_child_exit(fleet.root / (ROLE + "-state/native-observations.jsonl")))
    fleet.result["restart"] = {"before": before}
    if before["exit"] != (-signal.SIGKILL if crash else 0) or before["unparsed"] or before["children"]["status"] != "observed_absent":
        raise RuntimeError("owned process shutdown did not match fault seam")


def exercise(fleet, mode):
    from mixed_fleet_provider import write
    r, work, identity = fleet.result, fleet.worktrees[ROLE], fleet.identities[ROLE]
    sid, state = identity["session_id"], fleet.root / (ROLE + "-state")
    provider, client = fleet.providers[ROLE], fleet.clients[ROLE]
    fleet.run([fleet.buddy, "hello", "--session", "fixture-owner"])
    for slug, scope in (("run-compact", ".buddy/slot/compact"), ("guard", "codex.txt")):
        fleet.run([fleet.buddy, "claim", slug, "--session", "fixture-owner", "--scope", scope])
    fleet.run([fleet.buddy, "claim", "worker", "--session", sid, "--scope", "claude.txt"], work)
    fleet.run([fleet.buddy, "wait", "--session", sid, "--on", "run-compact", "--ready", "HEAD"], work)
    r["ready_sha"] = fleet.run(["git", "rev-parse", "HEAD"], work).strip()
    original = fleet.queue(ROLE, ROLE, "Original body stays queued; marker is never replayed.")
    wake, output = fleet.turn(ROLE, "original", original, [fleet.shell(ROLE, "original",
        "(work/'.fixture/original.txt').open('a').write('once\\n')", require_approval=True)])
    answer = next(v for v in client.events if v.get("approval_delivery", {}).get("state") == "submitted")
    r["original"] = dict(queue=original, wake=wake, output=output, approval=answer)
    queued = fleet.queue(ROLE, ROLE, "Fixture-only manual compact; queued body is not operator authority.")
    r["queue"] = queued
    common.write_json(state / "compact-next.json", dict(message_id=queued["delivery"]["message_id"], mode=mode))
    r["before_fault"] = snap(fleet, "before-fault")
    release, arrived, finished = threading.Event(), threading.Event(), threading.Event()
    respond = provider._respond
    active = mode in ("interrupt_active", "crash_active")
    if active:
        def held(request, path):
            response = respond(request, path)
            arrived.set()
            if not release.wait(90):
                raise RuntimeError("summary response barrier expired")
            finished.set()
            return response
        provider._respond = held
    script = provider.queue_turn("compact-summary", [], final_text=compact.SUMMARY_REPLY)
    try:
        client.send(dict(schema="buddy.control.v1", id="compact", operation="send", identity=identity,
            message_id=queued["delivery"]["message_id"]))
        if active:
            if not arrived.wait(15):
                raise RuntimeError("summary request did not reach barrier")
            r["provider_barrier"], r["response_released_at_fault"] = arrived.is_set(), release.is_set()
        if mode != "crash_active":
            r["wake"] = client.wait(lambda v: v.get("request_id") == "compact", permit_error=active)
            turn = r["wake"]["wake_delivery"]["turn_id"]
            if mode == "interrupt_active":
                r["wrong_interrupt"] = control(fleet, "wrong-interrupt", "interrupt", turn_id="wrong", permit_error=True)
                r["interrupt"] = control(fleet, "interrupt", "interrupt", turn_id=turn, permit_error=True)
            if mode in ("completed", "interrupt_active", "lost_boundary"):
                r["terminal"] = client.wait(lambda v: v.get("native_event", {}).get("kind") == "result" and
                    v["native_event"].get("turn_id") == turn, permit_error=True)
                r["idle"] = client.wait(lambda v: v.get("native_event", {}).get("kind") == "idle" and
                    v["native_event"].get("turn_id") == turn, permit_error=True)
            else:
                # Native stdout can outrun the controller's journal writer.
                # Select a boundary-only journal, not an arbitrary earlier
                # crash offset where both observations might still be absent.
                r["boundary"] = client.wait(lambda v: v.get("native_event", {}).get("kind") == "compaction_boundary")
                deadline = time.monotonic() + 10
                while not (state / "fault-ready.json").exists() and time.monotonic() < deadline:
                    time.sleep(.02)
                if not (state / "fault-ready.json").exists():
                    raise RuntimeError("proxy did not capture the lost result")
        stop(fleet, mode in ("crash_active", "lost_boundary", "lost_result"))
    finally:
        release.set()
        if active:
            finished.wait(5)
            provider._respond = respond
    if active:
        deadline = time.monotonic() + 2
        while not provider.errors and time.monotonic() < deadline:
            time.sleep(.02)
        r["fault_provider_errors"] = list(provider.errors)
        provider.errors[:] = [v for v in provider.errors if v.get("error_type") != "connection_closed"]
    r["fault_script"] = script.snapshot()
    with provider._lock:
        provider._active = None
        provider._queued.clear()
    histories = list((state / "native/projects").glob("*/" + sid + ".jsonl"))
    if len(histories) != 1:
        raise RuntimeError("seeded native history missing or ambiguous")
    shutil.copyfile(histories[0], state / "history-after-fault.jsonl")
    r["after_fault"] = snap(fleet, "after-fault")
    r["requests_before_resume"] = len(provider.requests)
    r["journal_before_resume"] = common.digest(state / "identity.json.events")
    r["wire_before_resume"] = common.digest(state / "native-observations.jsonl")
    replacement = compact.resume_client(fleet, "resume")
    fleet.clients[ROLE] = replacement
    r["sessions"][ROLE]["events"] = replacement.events
    r["restart"]["resume"] = replacement.wait(lambda v: v.get("operation") == "resume", permit_error=True)
    r["after_resume"] = snap(fleet, "after-resume")
    r["requests_after_resume"] = len(provider.requests)
    if mode != "completed":
        if not r["restart"]["resume"].get("error"):
            raise RuntimeError("uncertain compaction admitted resume")
        fleet.clients.pop(ROLE).close()
        r["refusal_exit"] = replacement.process.returncode
        r["sessions"][ROLE].update(adapter_exit=replacement.process.returncode, unparsed=replacement.unparsed,
            native_child_exit=common.observed_child_exit(state / "native-observations.jsonl"))
        r["journal_after_refusal"] = common.digest(state / "identity.json.events")
        r["wire_after_refusal"] = common.digest(state / "native-observations.jsonl")
    else:
        if r["restart"]["resume"].get("error"):
            raise RuntimeError(r["restart"]["resume"]["error"])
        r["duplicate"] = control(fleet, "duplicate", "send", message_id=queued["delivery"]["message_id"])
        r["duplicate_original"] = control(fleet, "duplicate-original", "send", message_id=original["delivery"]["message_id"])
        r["replay"] = control(fleet, "old-approval", "approve", operator=True, permit_error=True, approval=answer["approval_response"])
        r["inspect"] = control(fleet, "inspect", "inspect")
        r["requests_after_replay"] = len(provider.requests)
        mid = original["delivery"]["message_id"]
        fresh = fleet.queue(ROLE, ROLE, "Fresh fixed tools after resume; explicitly retrieve original body.")
        fetch = fleet.shell(ROLE, "fresh", f'''save("old-inbox.json",adapter("inbox","--message",{str(mid)!r}))
save("old-delivered.json",adapter("delivered","--message",{str(mid)!r}))
assert (work/'.fixture/original.txt').read_text()=='once\\n'
(work/'.fixture/fresh.txt').write_text('fresh')''')
        r["fresh_wake"], r["fresh_output"] = fleet.turn(ROLE, "fresh", fresh, [
            write(work / "codex.txt", "forbidden\n"), write(work / "claude.txt", "allowed\n"), fetch])
        r["old_inbox"], r["old_delivered"] = fleet.artifact(ROLE, "old-inbox.json"), fleet.artifact(ROLE, "old-delivered.json")
        r["after_fresh"] = snap(fleet, "after-fresh")
    for slug, owner in (("worker", sid), ("guard", "fixture-owner"), ("run-compact", "fixture-owner")):
        fleet.run([fleet.buddy, "release", slug, "--session", owner])
    r["ledger"] = snap(fleet, "final")


def run_case(root, mode, args):
    root.mkdir()
    r = dict(schema="buddy.claude-interrupted-compaction.v1", root=str(root), mode=mode,
        provider="deterministic-loopback", claude_version=args.claude_version,
        started_at=datetime.datetime.now(datetime.timezone.utc).isoformat(), passed=False,
        sessions={}, messages=[], ready=[], validations=[],
        unqualified=["unfinished Claude recovery", "automatic thresholds", "summary quality", "arbitrary effects",
            "real-provider/model-directed recovery", "production installation"])
    sources = ("qualify-claude-interrupted-compaction.py", "check-claude-interrupted-compaction.py",
        "qualify-claude-compaction.py", "qualify-mixed-fleet.py", "mixed_fleet_provider.py", "qualify-claude.py",
        "qualify-codex.py", "check-mixed-fleet.py")
    r["fixture_sources"] = {name: common.digest(REPO / "scripts" / name) for name in sources}
    fleet = mixed.Fleet(root, r, args)
    try:
        fleet.prepare(roles=((ROLE, "claude", "compact-work"),), observer=observer, provider_factory=compact.provider_factory)
        exercise(fleet, mode)
    except Exception:
        r["error"] = traceback.format_exc()
    finally:
        # F-35: natives are identified (pid, kernel start) while their
        # adapter is still alive and verified; never by a pid number later.
        trace = root / (ROLE + "-state/native-observations.jsonl")
        owned = getattr(fleet, "owned_natives", []) + common.owned_natives(
            trace, [c.owned for c in fleet.clients.values()])
        fleet.close()
        # End only those identities; each kill is a cleanup error, and
        # forced_cleanup_groups fails the checker's shutdown check too.
        forced = []
        common.end_owned(owned, r.setdefault("cleanup_errors", []), forced)
        if forced:
            r["forced_cleanup_groups"] = forced
        if trace.exists():
            r["final_child_exit"] = common.observed_child_exit(trace)
        if any(common.digest(REPO / "scripts" / name) != digest for name, digest in r["fixture_sources"].items()):
            r["error"] = r.get("error", "") + "\nFixture source changed during execution."
        if "error" not in r:
            try:
                validator = mixed.module("interrupted_compact_validator", "check-claude-interrupted-compaction.py")
                r["evidence_sha256"] = {str((root / p).relative_to(root)): common.digest(root / p) for p in validator.evidence_paths(r)}
                r["validation"] = validator.validate_case(r)
                r["passed"] = r["validation"]["passed"] and not r["cleanup_errors"]
            except Exception:
                r["error"] = traceback.format_exc()
        common.write_json(root / "result.json", r)
        print(json.dumps({"mode": mode, **{k: r.get(k) for k in ("passed", "validation", "error", "cleanup_errors")}}), flush=True)
    return r


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--claude", default=shutil.which("claude"))
    parser.add_argument("--claude-version", help="defaults to the binary's own version; a typed one must match it")
    args = parser.parse_args()
    # D-080: the binaries this run was given are its candidates; every adapter it starts is bound to them.
    sys.modules["buddy_admission_lib"].bind_args(args)
    common.exit_on_signals()  # F-35: TERM/HUP still run the finally blocks
    root = Path(tempfile.mkdtemp(prefix="buddy-claude-interrupted-compaction-", dir="/private/tmp"))
    print("ROOT=" + str(root), flush=True)
    cases = {mode: run_case(root / mode, mode, args) for mode in MODES}
    r = dict(schema="buddy.claude-interrupted-compaction-suite.v1", root=str(root), cases=cases,
        passed=all(v["passed"] for v in cases.values()))
    common.write_json(root / "result.json", r)
    print("RECEIPT=" + str(root / "result.json"), flush=True)
    return 0 if r["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
