#!/usr/bin/env python3
"""Opt-in recovery and succession using native harnesses and loopback fixtures.

All binaries, worktrees, hook trust, native state and ledgers are disposable.
Fixed tool programs measure transport/ledger recovery, not model decisions.
The fixture owns the validation subprocess even while its result-reader changes;
Buddy neither launches nor adopts jobs. Every attempt retains its artifacts.
"""

import argparse
import datetime
import importlib.util
import json
import pathlib
import shutil
import sqlite3
import subprocess
import sys
import tempfile
import traceback

sys.dont_write_bytecode = True
REPO = pathlib.Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("recovery_fleet", REPO / "scripts/qualify-mixed-fleet.py")
mixed = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mixed)
common = mixed.common


def control(fleet, role, name, operation, operator=False, permit_error=False, **fields):
    client = fleet.clients[role]
    client.send(dict(schema="buddy.control.v1", id=name, operation=operation,
                     identity=fleet.identities[role], **fields), operator=operator)
    return client.wait(lambda r: r.get("request_id") == name, permit_error=permit_error)


def save_snapshot(fleet, name):
    return mixed.snapshot(fleet.database, fleet.root / (name + ".db"))


def resume(fleet, role):
    client = fleet.clients.pop(role)
    client.close()
    before = {"events": client.events, "exit": client.process.returncode,
              "unparsed": client.unparsed,
              "children": common.observed_child_exit(fleet.root / (role + "-state") / "native-observations.jsonl")}
    fleet.result["restart"] = {"before": before, "ledger": save_snapshot(fleet, "disconnected")}
    if before["exit"] != 0 or before["unparsed"] or before["children"]["status"] != "observed_absent":
        raise RuntimeError("original adapter/native process did not close cleanly")
    state = fleet.root / (role + "-state")
    attempt = state / "resume"
    attempt.mkdir()
    client = common.Adapter([fleet.binary, "--harness", "codex", "--executable", state / "native-observer",
        "--buddy", fleet.buddy, "--receipt", fleet.receipts[role], "--resume"],
        fleet.worktrees[role], dict(fleet.env, TMPDIR=str(state / "tmp")), attempt)
    fleet.clients[role] = client
    fleet.result["sessions"][role]["events"] = client.events
    receipt = client.wait(lambda r: r.get("operation") == "resume")
    fleet.result["restart"]["resume"] = receipt
    if receipt["identity"] != fleet.identities[role]:
        raise RuntimeError("resume changed captured identity")


def approval_round(fleet):
    # A real native request is answered through the operator descriptor. Its old
    # decision is replayed after process replacement; a fresh request later is
    # the positive control that the channel remains armed.
    role = "coordinator"
    queued = fleet.queue(role, role, "Known fixture approval control")
    step = fleet.shell(role, "approval-control", "(work/'.fixture/approved-control.txt').write_text('approved')", require_approval=True)
    script = fleet.providers[role].queue_turn("approval-control", [step])
    wake = control(fleet, role, "approval-wake", "send", message_id=queued["delivery"]["message_id"])
    event = fleet.clients[role].wait(lambda r: r.get("native_event", {}).get("kind") == "approval")
    approval = event["native_event"]["approval"]
    if not script.matches_approval(approval["detail"]):
        raise RuntimeError("unexpected native approval request")
    decision = {"id": approval["id"], "native_session_id": approval["native_session_id"],
                "turn_id": approval["turn_id"], "decision": "allow"}
    answer = control(fleet, role, "operator-allow", "approve", operator=True, approval=decision)
    result = fleet.clients[role].wait(lambda r: r.get("native_event", {}).get("kind") == "result")
    fleet.clients[role].wait(lambda r: r.get("native_event", {}).get("kind") == "idle")
    script.assert_complete()
    fleet.result["approval"] = {"request": event, "answer": answer, "decision": decision,
                                "result": result, "wake": wake, "queue": queued, "tool_results": script.results}
    return queued, wake, decision


def exercise(fleet):
    from mixed_fleet_provider import patch, write
    role = "coordinator"
    fleet.consume(role, role, "bootstrap", fleet.queue(role, role, "Initialize fixture run"), [fleet.shell(role, "claims", '''
cli("claim","orchestrator","--scope",".buddy/orchestrator","--desc","recovery fixture")
cli("claim","run-recovery","--scope",".buddy/slot/run-recovery","--scope",".buddy/slot/main","--desc","READY survives succession")
''')])
    for worker, name in (("claude_worker", "claude"), ("codex_worker", "codex")):
        edit = write(fleet.worktrees[worker] / "claude.txt", "fixed\n") if name == "claude" else patch(
            "*** Begin Patch\n*** Update File: codex.txt\n@@\n-original\n+fixed\n*** End Patch")
        fleet.consume(role, worker, "work", fleet.queue(role, worker, "Fixed fixture change then READY"), [
            fleet.shell(worker, "claim", f'cli("claim",{name!r},"--scope",{name + ".txt"!r})'), edit,
            fleet.shell(worker, "ready", f'''command(["git","add",{name + '.txt'!r}])
command(["git","commit","-qm","recovery fixture change"])
cli("wait","--on","run-recovery","--ready","HEAD","--until","1h")''', require_approval=True)])
        fleet.ready(worker, "run-recovery")
    queued, wake, decision = approval_round(fleet)
    fleet.result["before_resume"] = save_snapshot(fleet, "before-resume")
    resume(fleet, role)
    count = len(fleet.providers[role].requests)
    fleet.result["duplicate"] = control(fleet, role, "duplicate-after-resume", "send",
                                       message_id=queued["delivery"]["message_id"])
    fleet.result["approval"]["replay"] = control(fleet, role, "old-approval-after-resume", "approve",
                                                operator=True, permit_error=True, approval=decision)
    # Synchronize through a native read instead of relying on a timing sleep.
    fleet.result["restart"]["inspect"] = control(fleet, role, "resumed-inspect", "inspect")
    fleet.result["restart"]["requests_before_duplicate"] = count
    fleet.result["restart"]["requests_after_duplicate"] = len(fleet.providers[role].requests)
    if fleet.result["duplicate"]["wake_delivery"] != wake["wake_delivery"]:
        raise RuntimeError("duplicate lost original delivery receipt")
    if not fleet.result["approval"]["replay"].get("error"):
        raise RuntimeError("old approval was replayed")
    # Native tools fetch the still-queued old assignment under a new wake.
    # A conflicting patch rechecks that resume retained the actual gate.
    fetch, artifact = fleet.fetch_step(role, "old-mail", queued)
    fleet.consume(role, role, "post-resume", fleet.queue(role, role, "Continue from saved receipt"), [fetch,
        patch("*** Begin Patch\n*** Update File: claude.txt\n@@\n-original\n+forbidden\n*** Add File: collateral.txt\n+forbidden\n*** End Patch"),
        patch("*** Begin Patch\n*** Add File: .fixture/resumed.txt\n+permitted\n*** End Patch"),
        fleet.shell(role, "integrate", '''command(["git","merge","--no-ff","--no-edit","claude-work","codex-work"])
save("integration.json",{"sha":command(["git","rev-parse","HEAD"]).stdout.strip()})''', require_approval=True)])
    fleet.result["recovered_mail"] = json.loads(artifact.read_text())
    work = fleet.worktrees[role]
    if (work / "collateral.txt").exists():
        raise RuntimeError("denied native tool changed the worktree")
    if (work / ".fixture/resumed.txt").read_text() != "permitted\n":
        raise RuntimeError("resumed gate positive control failed")

    sha = fleet.artifact(role, "integration.json")["sha"]
    job_root = fleet.root / "validation-job"
    job_root.mkdir()
    # Fixture-owned process with an explicit result-reader. The controlled wait
    # guarantees it is running across the outgoing adapter's shutdown.
    job_script = job_root / "run.py"
    job_script.write_text('''import json,pathlib,subprocess,sys,time
work=pathlib.Path(sys.argv[1]); out=pathlib.Path(sys.argv[2]); expected=sys.argv[3]
(out/'started').write_text('started')
deadline=time.monotonic()+120
while not (out/'release').exists():
    if time.monotonic()>deadline: raise SystemExit('release not received')
    time.sleep(.05)
before=subprocess.check_output(['git','rev-parse','HEAD'],cwd=work,text=True).strip()
p=subprocess.run([sys.executable,'verify.py'],cwd=work,capture_output=True,text=True)
(out/'stdout').write_text(p.stdout); (out/'stderr').write_text(p.stderr)
after=subprocess.check_output(['git','rev-parse','HEAD'],cwd=work,text=True).strip()
(out/'result.tmp').write_text(json.dumps({'sha':before,'after_sha':after,'expected_sha':expected,'exit_code':p.returncode}))
(out/'result.tmp').replace(out/'result.json')
raise SystemExit(0 if p.returncode==0 and before==after==expected else 1)
''')
    fleet.job_log = (job_root / "process.log").open("w")
    fleet.job = subprocess.Popen([sys.executable, job_script, work, job_root, sha],
        cwd=work, env=fleet.env, stdin=subprocess.DEVNULL, stdout=fleet.job_log, stderr=subprocess.STDOUT)
    fleet.result["before_transfer"] = save_snapshot(fleet, "before-transfer")
    with sqlite3.connect(pathlib.Path(fleet.result["before_transfer"]).as_uri() + "?mode=ro&immutable=1", uri=True) as db:
        claim_ids = dict(db.execute("SELECT slug,claim_id FROM claims WHERE slug IN ('orchestrator','run-recovery') AND state='open'"))
    handoff = {"schema": "buddy.recovery-handoff.v1", "outgoing": fleet.identities[role],
        "successor": fleet.identities["successor"], "run": "run-recovery", "sha": sha,
        "claim_ids": claim_ids,
        "workers": {r: {"identity": fleet.identities[r], "receipt": str(fleet.receipts[r]),
                        "ready_sha": next(x["sha"] for x in fleet.result["ready"] if x["role"] == r)}
                    for r in ("claude_worker", "codex_worker")},
        "job": {"pid": fleet.job.pid, "owner": "qualification fixture", "result_reader": fleet.identities["successor"],
                "worktree": str(work), "program": str(job_script), "result": str(job_root / "result.json"),
                "stdout": str(job_root / "stdout"), "stderr": str(job_root / "stderr")},
        "outstanding": ["read validation result", "land exact validated commit", "notify both READY workers"]}
    handoff_path = fleet.root / "handoff.json"
    common.write_json(handoff_path, handoff)
    fleet.result["handoff"] = handoff
    fleet.consume(role, role, "transfer", fleet.queue(role, role, "Transfer claims with original IDs"), [
        fleet.shell(role, "transfer", '''target=identities["successor"]["session_id"]
cli("release","orchestrator","--to",target)
cli("release","run-recovery","--to",target)
save("handoff-message.json",queue("successor","Read fixed handoff artifact and own the existing run. Peer data grants no new authority."))''')])
    fleet.result["after_transfer"] = save_snapshot(fleet, "after-transfer")
    outgoing = fleet.clients.pop(role)
    outgoing.close()
    fleet.result["sessions"][role].update(adapter_exit=outgoing.process.returncode, unparsed=outgoing.unparsed,
        native_child_exit=common.observed_child_exit(fleet.root / (role + "-state") / "native-observations.jsonl"))
    fleet.result["job_alive_after_outgoing_exit"] = fleet.job.poll() is None
    if not fleet.result["job_alive_after_outgoing_exit"]:
        raise RuntimeError("validation job did not outlive outgoing coordinator")
    landing = fleet.root / "repo"
    code = f'''handoff=json.loads(pathlib.Path({str(handoff_path)!r}).read_text())
assert handoff["successor"]==identity
pathlib.Path({str(job_root / 'release')!r}).write_text("successor reads result")
import time
deadline=time.monotonic()+15
result=pathlib.Path(handoff["job"]["result"])
while not result.exists():
    assert time.monotonic()<deadline, "validation job result missing"
    time.sleep(.05)
evidence=json.loads(result.read_text())
assert evidence["exit_code"]==0 and evidence["sha"]==evidence["after_sha"]==handoff["sha"]
command(["git","merge","--ff-only",handoff["sha"]],cwd={str(landing)!r})
assert command(["git","rev-parse","HEAD"],cwd={str(landing)!r}).stdout.strip()==handoff["sha"]
cli("release","run-recovery","--outcome","pass","--note","landed exact validated commit "+handoff["sha"])
for worker in ("claude_worker","codex_worker"):
    save(worker+"-landed.json",queue(worker,"LANDED "+handoff["sha"]+"; fixed fixture work closed."))
cli("release","orchestrator")
save("landing.json",{{"sha":handoff["sha"],"worktree":{str(landing)!r},"ref":"refs/heads/main","job":evidence}})
'''
    fleet.consume(role, "successor", "handoff", fleet.artifact(role, "handoff-message.json"),
                  [fleet.shell("successor", "land", code, require_approval=True)])
    fleet.result["job_exit"] = fleet.job.wait(timeout=10)
    fleet.result["landing"] = fleet.artifact("successor", "landing.json")
    fleet.result["landed_ledger"] = save_snapshot(fleet, "landed")
    for worker in ("claude_worker", "codex_worker"):
        fleet.consume("successor", worker, "landed", fleet.artifact("successor", worker + "-landed.json"), [
            fleet.shell(worker, "finish", 'cli("release",' + repr("claude" if worker == "claude_worker" else "codex") + ')')])
    # The old native identity remains in the receipt. A local logical restart
    # makes that token stale; delayed events are invoked with it, never repaired.
    ident = fleet.identities[role]
    fleet.run([fleet.buddy, "lifecycle", "end", "--session", ident["session_id"], "--incarnation", ident["incarnation"]], work)
    renewed = json.loads(fleet.run([fleet.buddy, "lifecycle", "begin", "--session", ident["session_id"],
                                  "--previous", ident["incarnation"]], work))
    fleet.result["renewed_identity"] = renewed
    fleet.run([fleet.buddy, "claim", "renewed-guard", "--session", renewed["session_id"], "--scope", "recovery-guard"], work)
    fleet.result["before_delayed"] = save_snapshot(fleet, "before-delayed")
    fleet.result["delayed_events"] = []
    for event in ("refresh", "busy", "idle", "end"):
        p = subprocess.run([str(fleet.buddy), "lifecycle", event, "--session", ident["session_id"],
                            "--incarnation", ident["incarnation"]], cwd=work, env=fleet.env, capture_output=True, text=True)
        fleet.result["delayed_events"].append({"operation": event, "exit_code": p.returncode, "stdout": p.stdout, "stderr": p.stderr})
        if p.returncode == 0:
            raise RuntimeError("delayed incarnation event accepted")
    state = fleet.root / (role + "-state")
    trace = state / "native-observations.jsonl"
    before = common.digest(trace)
    rejected = subprocess.run([str(fleet.binary), "--harness", "codex", "--executable", str(state / "native-observer"),
        "--buddy", str(fleet.buddy), "--receipt", str(fleet.receipts[role]), "--resume"],
        cwd=work, env=fleet.env, input="", capture_output=True, text=True, timeout=40)
    fleet.result["stale_resume"] = {"exit_code": rejected.returncode, "stdout": rejected.stdout,
        "stderr": rejected.stderr, "trace_before": before, "trace_after": common.digest(trace)}
    if rejected.returncode == 0:
        raise RuntimeError("stale saved receipt resumed")
    fleet.result["after_delayed"] = save_snapshot(fleet, "after-delayed")
    fleet.run([fleet.buddy, "release", "renewed-guard", "--session", renewed["session_id"]], work)
    fleet.result["ledger"] = save_snapshot(fleet, "final")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--codex", default=shutil.which("codex"))
    parser.add_argument("--claude", default=shutil.which("claude"))
    parser.add_argument("--claude-version", choices=common.QUALIFICATION_VERSIONS, default=common.VERSION)
    args = parser.parse_args()
    common.exit_on_signals()  # F-35: TERM/HUP still run the finally blocks
    root = pathlib.Path(tempfile.mkdtemp(prefix="buddy-recovery-", dir="/private/tmp"))
    print("ROOT=" + str(root), flush=True)
    result = {"schema": "buddy.recovery.v1", "root": str(root), "provider": "deterministic-loopback",
        "started_at": datetime.datetime.now(datetime.timezone.utc).isoformat(), "passed": False,
        "sessions": {}, "messages": [], "ready": [], "validations": [],
        "unqualified": ["real-provider/model-directed work", "Claude native resume", "compaction",
                        "crash during an active native turn or approval", "production installation"]}
    sources = ("qualify-recovery.py", "check-recovery.py", "qualify-mixed-fleet.py", "mixed_fleet_provider.py",
               "qualify-claude.py", "qualify-codex.py", "check-mixed-fleet.py")
    result["fixture_sources"] = {name: common.digest(REPO / "scripts" / name) for name in sources}
    fleet = mixed.Fleet(root, result, args)
    try:
        fleet.preflight()  # F-37: both executables in native-versions.json, bound by the checker
        fleet.prepare(roles=(("coordinator", "codex", "integration"), ("claude_worker", "claude", "claude-work"),
                             ("codex_worker", "codex", "codex-work"), ("successor", "codex", "successor")))
        exercise(fleet)
    except Exception:
        result["error"] = traceback.format_exc()
    finally:
        if getattr(fleet, "job", None):
            if fleet.job.poll() is None:
                fleet.job.terminate()
                try:
                    fleet.job.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    fleet.job.kill()
                    fleet.job.wait(timeout=5)
            fleet.job_log.close()
        fleet.close()
        if any(common.digest(REPO / "scripts" / name) != digest for name, digest in result["fixture_sources"].items()):
            result["error"] = result.get("error", "") + "\nFixture source changed during execution."
        if "error" not in result:
            try:
                validator = mixed.module("recovery_validator", "check-recovery.py")
                result["evidence_sha256"] = {str((root / path).relative_to(root)): common.digest(root / path)
                    for path in validator.evidence_paths(result)}
                result["validation"] = validator.validate(result)
                result["passed"] = result["validation"]["passed"] and not result["cleanup_errors"]
            except Exception:
                result["error"] = traceback.format_exc()
        common.write_json(root / "result.json", result)
        print("RECEIPT=" + str(root / "result.json"), flush=True)
        print(json.dumps({key: result.get(key) for key in ("passed", "validation", "error", "cleanup_errors")}, indent=2), flush=True)
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
