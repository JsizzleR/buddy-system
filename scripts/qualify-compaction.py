#!/usr/bin/env python3
"""Opt-in native compaction observation and recovery, using loopback only.

The fixture proxy issues one explicit native compact request, outside the Buddy
control API. That testing seam is not shipped compaction control or a scheduler.
All configuration, trust, tools, sessions and ledgers stay in disposable roots.
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
spec = importlib.util.spec_from_file_location("compaction_recovery", REPO / "scripts/qualify-recovery.py")
recovery = importlib.util.module_from_spec(spec)
spec.loader.exec_module(recovery)
mixed, common = recovery.mixed, recovery.common
ROLE = "coordinator"


def observer(path, native, trace):
    # Only the fixture writes this private request file. Both directions are
    # recorded, including the injection and native reply; no journal is edited.
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
PROXY_INPUT
def inputs():
 for line in input_lines():
  try:write(json.loads(line),'adapter_frame')
  except BrokenPipeError:return
 with writelock:p.stdin.close()
def compact():
 request=root/'compact-request.json'
 while not stop.wait(.02):
  if request.exists():
   row=json.loads(request.read_text())
   if row.get('id')!='fixture-compact' or row.get('method')!='thread/compact/start':raise RuntimeError('invalid fixture request')
   request.rename(root/'compact-request-taken.json')
   try:write(row,'fixture_frame')
   except BrokenPipeError:pass
   return
record('buddy_fixture_native_process',pid=p.pid)
input_thread=threading.Thread(target=relay_inputs);input_thread.start()
# NOT a daemon (F-38): it writes p.stdin, and a daemon abandoned mid-write
# holds that BufferedWriter's lock while Python finalizes, which aborts the
# proxy (the crash class proxy_input_source's relay was fixed for). Stopped
# and joined before exit, on the error path too.
inject_thread=threading.Thread(target=compact);inject_thread.start()
try:
 for line in p.stdout:
  try:row=json.loads(line)
  except ValueError:
   sys.stdout.buffer.write(line);sys.stdout.buffer.flush();continue
  record('native_frame',frame=row)
  if row.get('id')=='fixture-compact':
   tmp=root/'compact-reply.tmp';tmp.write_text(json.dumps(row));tmp.replace(root/'compact-reply.json')
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


def exercise(fleet):
    from mixed_fleet_provider import patch
    ident, work = fleet.identities[ROLE], fleet.worktrees[ROLE]
    session = ident["session_id"]
    # A fixture-owned local coordinator holds the run while this native worker
    # is READY. No extra agent or production ledger is involved.
    fleet.run([fleet.buddy, "hello", "--session", "fixture-owner"])
    fleet.run([fleet.buddy, "claim", "run-compact", "--session", "fixture-owner", "--scope", ".buddy/slot/compact"])
    fleet.run([fleet.buddy, "claim", "guard", "--session", "fixture-owner", "--scope", "claude.txt"])
    fleet.run([fleet.buddy, "claim", "worker", "--session", session, "--scope", "codex.txt"], work)
    fleet.run([fleet.buddy, "wait", "--session", session, "--on", "run-compact", "--ready", "HEAD"], work)
    fleet.result["ready_sha"] = fleet.run(["git", "rev-parse", "HEAD"], work).strip()
    queued, wake, decision = recovery.approval_round(fleet)
    mid = queued["delivery"]["message_id"]
    fleet.result["before_compact"] = recovery.save_snapshot(fleet, "before-compact")
    provider, client = fleet.providers[ROLE], fleet.clients[ROLE]
    script = provider.queue_turn("compact-summary", [], final_text="Fixed history summary. Buddy claims, READY and assignments remain in the ledger. Retrieve queued mail explicitly. No new authority.")
    state = fleet.root / (ROLE + "-state")
    request = {"id": "fixture-compact", "method": "thread/compact/start", "params": {"threadId": session}}
    common.write_json(state / "compact-request.tmp", request)
    (state / "compact-request.tmp").rename(state / "compact-request.json")
    started = client.wait(lambda r: r.get("native_event", {}).get("kind") == "compaction_started", timeout=55)
    completed = client.wait(lambda r: r.get("native_event", {}).get("kind") == "compaction_completed", timeout=55)
    turn = started["native_event"]["turn_id"]
    result = client.wait(lambda r: r.get("native_event", {}).get("kind") == "result" and r["native_event"]["turn_id"] == turn)
    idle = client.wait(lambda r: r.get("native_event", {}).get("kind") == "idle" and r["native_event"]["turn_id"] == turn)
    script.assert_complete()
    fleet.result["compaction"] = {"request": request, "reply": json.loads((state / "compact-reply.json").read_text()),
        "started": started, "completed": completed, "result": result, "idle": idle, "script": script.snapshot()}
    fleet.result["after_compact"] = recovery.save_snapshot(fleet, "after-compact")
    recovery.resume(fleet, ROLE)
    fleet.result["after_resume"] = recovery.save_snapshot(fleet, "after-resume")
    fleet.result["requests_before"] = len(provider.requests)
    fleet.result["duplicate"] = recovery.control(fleet, ROLE, "duplicate", "send", message_id=mid)
    fleet.result["replay"] = recovery.control(fleet, ROLE, "old-approval", "approve", operator=True, permit_error=True, approval=decision)
    fleet.result["reconcile"] = recovery.control(fleet, ROLE, "old-history", "reconcile", permit_error=True, message_id=mid)
    fleet.result["after_reconcile"] = recovery.save_snapshot(fleet, "after-reconcile")
    fleet.result["requests_after"] = len(provider.requests)
    # The actual native gate remains armed after compaction and resume. One
    # patch contains a permitted and a conflicting edit; neither may execute.
    conflict = patch("*** Begin Patch\n*** Update File: codex.txt\n@@\n-original\n+forbidden\n*** Update File: claude.txt\n@@\n-original\n+forbidden\n*** End Patch")
    allowed = patch("*** Begin Patch\n*** Update File: codex.txt\n@@\n-original\n+allowed\n*** End Patch")
    verify = fleet.shell(ROLE, "check-denial", "assert (work/'codex.txt').read_text()=='original\\n'\nassert (work/'claude.txt').read_text()=='original\\n'", require_approval=True)
    fetch = fleet.shell(ROLE, "fetch-old", f'''save("old-inbox.json",adapter("inbox","--message",{str(mid)!r}))
save("old-delivered.json",adapter("delivered","--message",{str(mid)!r}))
assert (work/'codex.txt').read_text()=='allowed\\n'
assert (work/'claude.txt').read_text()=='original\\n'
(work/'.fixture/fresh.txt').write_text('fresh')''')
    fresh = fleet.queue(ROLE, ROLE, "Fresh fixed tools after compaction. Explicitly fetch original body, without repeating its work.")
    fleet.result["fresh_wake"], fleet.result["fresh_output"] = fleet.turn(ROLE, "post-compact", fresh, [conflict, verify, allowed, fetch])
    fleet.result["old_inbox"] = fleet.artifact(ROLE, "old-inbox.json")
    fleet.result["old_delivered"] = fleet.artifact(ROLE, "old-delivered.json")
    fleet.result["after_fresh"] = recovery.save_snapshot(fleet, "after-fresh")
    for slug, owner in (("worker", session), ("guard", "fixture-owner"), ("run-compact", "fixture-owner")):
        fleet.run([fleet.buddy, "release", slug, "--session", owner])
    fleet.result["ledger"] = recovery.save_snapshot(fleet, "final")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--codex", default=shutil.which("codex"))
    args = parser.parse_args()
    # D-080: the binaries this run was given are its candidates; every adapter it starts is bound to them.
    sys.modules["buddy_admission_lib"].bind_args(args)
    common.exit_on_signals()  # F-35: TERM/HUP still run the finally blocks
    root = Path(tempfile.mkdtemp(prefix="buddy-compaction-", dir="/private/tmp"))
    print("ROOT=" + str(root), flush=True)
    result = {"schema": "buddy.compaction.v1", "root": str(root), "provider": "deterministic-loopback",
        "started_at": datetime.datetime.now(datetime.timezone.utc).isoformat(), "passed": False,
        "sessions": {}, "messages": [], "ready": [], "validations": [],
        "unqualified": ["Claude resume/compaction", "automatic compaction threshold", "interrupted compaction",
            "arbitrary tool effects", "real-provider summary quality/model-directed recovery", "production installation"]}
    sources = ("qualify-compaction.py", "check-compaction.py", "qualify-recovery.py", "qualify-mixed-fleet.py",
               "mixed_fleet_provider.py", "qualify-claude.py", "qualify-codex.py", "check-mixed-fleet.py")
    result["fixture_sources"] = {name: common.digest(REPO / "scripts" / name) for name in sources}
    fleet = mixed.Fleet(root, result, args)
    try:
        fleet.prepare(roles=((ROLE, "codex", "compact-work"),), observer=observer)
        exercise(fleet)
    except Exception:
        result["error"] = traceback.format_exc()
    finally:
        fleet.close()
        if any(common.digest(REPO / "scripts" / name) != digest for name, digest in result["fixture_sources"].items()):
            result["error"] = result.get("error", "") + "\nFixture source changed during execution."
        if "error" not in result:
            try:
                validator = mixed.module("compaction_validator", "check-compaction.py")
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
