#!/usr/bin/env python3
"""Opt-in Claude cancellation/resume and unfinished-crash refusal qualification.

Each case owns a disposable worktree, native state and loopback provider. Fixed
tools and exact request-scoped approvals exercise the transport, never inference.
Only owned process groups are killed; original journals are never repaired.
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
spec = importlib.util.spec_from_file_location("claude_interrupted_resume", REPO / "scripts/qualify-claude-resume.py")
resume = importlib.util.module_from_spec(spec)
spec.loader.exec_module(resume)
mixed, common, ROLE = resume.mixed, resume.common, resume.ROLE
MODES = ("interrupt_active", "interrupt_approval", "crash_active", "crash_approval")
control, snap = resume.control, resume.snap


def stop(fleet, crash):
    client = fleet.clients[ROLE]
    # F-35: capture descendants while the exact adapter is alive. Its worker
    # owns a separate group, which a deliberate crash must end explicitly.
    natives = common.owned_natives(
        fleet.root / (ROLE + "-state") / "native-observations.jsonl", [client.owned])
    fleet.owned_natives = getattr(fleet, "owned_natives", []) + natives
    fault_groups = []
    if crash:
        fault_groups = common.crash_owned(client, natives)
    client.close()
    fleet.clients.pop(ROLE)
    state = fleet.root / (ROLE + "-state")
    before = dict(events=client.events, group=client.process.pid, fault_groups=fault_groups, exit=client.process.returncode, unparsed=client.unparsed,
        children=common.observed_child_exit(state / "native-observations.jsonl"))
    fleet.result["restart"] = {"before": before}
    if before["exit"] != (-signal.SIGKILL if crash else 0) or before["unparsed"] or before["children"]["status"] != "observed_absent":
        raise RuntimeError("owned process shutdown did not match the fault seam")


def exercise(fleet, mode):
    from mixed_fleet_provider import write
    r, work, identity = fleet.result, fleet.worktrees[ROLE], fleet.identities[ROLE]
    sid = identity["session_id"]
    active, crash = mode.endswith("active"), mode.startswith("crash")
    provider, client = fleet.providers[ROLE], fleet.clients[ROLE]
    state = fleet.root / (ROLE + "-state")
    fleet.run([fleet.buddy, "hello", "--session", "fixture-owner"])
    for slug, scope in (("run-interrupted", ".buddy/slot/interrupted"), ("guard", "codex.txt")):
        fleet.run([fleet.buddy, "claim", slug, "--session", "fixture-owner", "--scope", scope])
    fleet.run([fleet.buddy, "claim", "worker", "--session", sid, "--scope", "claude.txt"], work)
    fleet.run([fleet.buddy, "wait", "--session", sid, "--on", "run-interrupted", "--ready", "HEAD"], work)
    r["ready_sha"] = fleet.run(["git", "rev-parse", "HEAD"], work).strip()
    r["queue"] = queued = fleet.queue(ROLE, ROLE, "Original body stays queued across the fault; never retry uncertain work.")
    r["before_fault"] = snap(fleet, "before-fault")
    release, arrived, finished = threading.Event(), threading.Event(), threading.Event()
    original = provider._respond
    if active:
        # Claude acknowledges input after its first response. Hold the second
        # response, after an explicitly approved marker tool has really run.
        def held(request, path):
            response = original(request, path)
            if len(provider.requests) == 2:
                arrived.set()
                if not release.wait(30):
                    raise RuntimeError("active-response barrier expired")
                finished.set()
            return response
        provider._respond = held
    marker = "before-abort" if active else "unapproved"
    script = provider.queue_turn(mode, [fleet.shell(ROLE, marker,
        "(work/'.fixture/" + marker + ".txt').open('a').write('once\\n')")])
    try:
        r["wake"] = control(fleet, "original-wake", "send", message_id=queued["delivery"]["message_id"])
        turn = r["wake"]["wake_delivery"]["turn_id"]
        r["approval"] = client.wait(lambda x: x.get("native_event", {}).get("kind") == "approval")
        approval = r["approval"]["native_event"]["approval"]
        if not script.matches_approval(approval["detail"]):
            raise RuntimeError("unexpected native approval")
        r["old_decision"] = dict(id=approval["id"], native_session_id=sid, turn_id=turn, decision="allow")
        if active:
            r["approved"] = control(fleet, "allow-marker", "approve", operator=True, approval=r["old_decision"])
            if not arrived.wait(15):
                raise RuntimeError("active request did not reach the response barrier")
            r["provider_barrier"] = arrived.is_set()
            r["response_released_at_fault"] = release.is_set()
        if not crash:
            r["wrong_interrupt"] = control(fleet, "wrong-interrupt", "interrupt", permit_error=True, turn_id="wrong-turn")
            r["interrupt"] = control(fleet, "exact-interrupt", "interrupt", turn_id=turn)
            r["terminal"] = client.wait(lambda x: x.get("native_event", {}).get("kind") == "result")
            r["idle"] = client.wait(lambda x: x.get("native_event", {}).get("kind") == "idle")
        stop(fleet, crash)
    finally:
        release.set()
        if active:
            finished.wait(5)
            provider._respond = original
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
    r["after_fault"] = snap(fleet, "after-fault")
    r["requests_before_resume"] = len(provider.requests)
    r["journal_before_resume"] = common.digest(fleet.receipts[ROLE].with_suffix(".json.events"))
    r["wire_before_resume"] = common.digest(state / "native-observations.jsonl")
    replacement = resume.resume_client(fleet, "resume")
    fleet.clients[ROLE] = replacement
    r["sessions"][ROLE]["events"] = replacement.events
    r["restart"]["resume"] = replacement.wait(lambda v: v.get("operation") == "resume", permit_error=True)
    r["after_resume"] = snap(fleet, "after-resume")
    r["requests_after_resume"] = len(provider.requests)
    if crash:
        if not r["restart"]["resume"].get("error"):
            raise RuntimeError("unfinished crash admitted resume")
        # Refusal must precede ledger refresh, native launch or journal writes.
        fleet.clients.pop(ROLE).close()
        r["refusal_exit"] = replacement.process.returncode
        r["sessions"][ROLE].update(adapter_exit=replacement.process.returncode,
            unparsed=replacement.unparsed, native_child_exit=common.observed_child_exit(state / "native-observations.jsonl"))
        r["journal_after_refusal"] = common.digest(fleet.receipts[ROLE].with_suffix(".json.events"))
        r["wire_after_refusal"] = common.digest(state / "native-observations.jsonl")
    else:
        if r["restart"]["resume"].get("error"):
            raise RuntimeError(r["restart"]["resume"]["error"])
        r["duplicate"] = control(fleet, "duplicate", "send", message_id=queued["delivery"]["message_id"])
        r["replay"] = control(fleet, "old-approval", "approve", operator=True, permit_error=True, approval=r["old_decision"])
        r["inspect"] = control(fleet, "inspect", "inspect")
        r["requests_after_replay"] = len(provider.requests)
        mid = queued["delivery"]["message_id"]
        fetch = fleet.shell(ROLE, "fresh", f'''save("old-inbox.json",adapter("inbox","--message",{str(mid)!r}))
save("old-delivered.json",adapter("delivered","--message",{str(mid)!r}))
assert (work/'claude.txt').read_text()=='allowed\\n'
assert (work/'codex.txt').read_text()=='original\\n'
assert not (work/'.fixture/unapproved.txt').exists()
(work/'.fixture/fresh.txt').write_text('fresh')''')
        fresh = fleet.queue(ROLE, ROLE, "Fresh explicitly authorized fixture input; retrieve the old body without replay.")
        r["fresh_wake"], r["fresh_output"] = fleet.turn(ROLE, "fresh", fresh, [
            write(work / "codex.txt", "forbidden\n"), write(work / "claude.txt", "allowed\n"), fetch])
        r["old_inbox"] = fleet.artifact(ROLE, "old-inbox.json")
        r["old_delivered"] = fleet.artifact(ROLE, "old-delivered.json")
        r["after_fresh"] = snap(fleet, "after-fresh")
    for slug, owner in (("worker", sid), ("guard", "fixture-owner"), ("run-interrupted", "fixture-owner")):
        fleet.run([fleet.buddy, "release", slug, "--session", owner])
    r["ledger"] = snap(fleet, "final")


def run_case(root, mode, args):
    root.mkdir()
    result = dict(schema="buddy.claude-interrupted.v1", root=str(root), mode=mode, provider="deterministic-loopback",
        claude_version=args.claude_version,
        started_at=datetime.datetime.now(datetime.timezone.utc).isoformat(), passed=False,
        sessions={}, messages=[], ready=[], validations=[],
        unqualified=["Claude unfinished/uncertain resume and compaction", "native history reconciliation",
            "arbitrary tool effects", "real-provider/model-directed recovery", "production installation"])
    sources = ("qualify-claude-interrupted.py", "check-claude-interrupted.py", "qualify-claude-resume.py",
        "qualify-mixed-fleet.py", "mixed_fleet_provider.py", "qualify-claude.py", "qualify-codex.py", "check-mixed-fleet.py")
    result["fixture_sources"] = {name: common.digest(REPO / "scripts" / name) for name in sources}
    fleet = mixed.Fleet(root, result, args)
    try:
        from mixed_fleet_provider import ScriptedProvider
        result["loopback"] = "::1" if args.ipv6 else "127.0.0.1"
        fleet.prepare(roles=((ROLE, "claude", "interrupted-work"),), observer=resume.observer,
            provider_factory=lambda harness: ScriptedProvider(harness, loopback=result["loopback"]))
        exercise(fleet, mode)
    except Exception:
        result["error"] = traceback.format_exc()
    finally:
        # F-35: natives are identified (pid, kernel start) while their
        # adapter is still alive and verified; never by a pid number later.
        trace = root / (ROLE + "-state") / "native-observations.jsonl"
        owned = getattr(fleet, "owned_natives", []) + common.owned_natives(
            trace, [c.owned for c in fleet.clients.values()])
        fleet.close()
        # A failed stream may leave the observer's child retrying after the
        # adapter closes the observer. End only those owned identities; each
        # kill is a cleanup error, and forced_cleanup_groups fails the checker.
        forced = []
        common.end_owned(owned, result.setdefault("cleanup_errors", []), forced)
        if forced:
            result["forced_cleanup_groups"] = forced
        if trace.exists():
            result["final_child_exit"] = common.observed_child_exit(trace)
        if any(common.digest(REPO / "scripts" / name) != digest for name, digest in result["fixture_sources"].items()):
            result["error"] = result.get("error", "") + "\nFixture source changed during execution."
        if "error" not in result:
            try:
                validator = mixed.module("claude_interrupted_validator", "check-claude-interrupted.py")
                result["evidence_sha256"] = {str((root / p).relative_to(root)): common.digest(root / p)
                    for p in validator.evidence_paths(result)}
                result["validation"] = validator.validate_case(result)
                result["passed"] = result["validation"]["passed"] and not result["cleanup_errors"]
            except Exception:
                result["error"] = traceback.format_exc()
        common.write_json(root / "result.json", result)
        print(json.dumps({"mode": mode, **{k: result.get(k) for k in ("passed", "validation", "error", "cleanup_errors")}}), flush=True)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--claude", default=shutil.which("claude"))
    parser.add_argument("--claude-version", choices=common.QUALIFICATION_VERSIONS, default=common.VERSION)
    parser.add_argument("--ipv6", action="store_true", help="use IPv6 loopback if IPv4 ephemeral ports are exhausted")
    args = parser.parse_args()
    common.exit_on_signals()  # F-35: TERM/HUP still run the finally blocks
    root = Path(tempfile.mkdtemp(prefix="buddy-claude-interrupted-", dir="/private/tmp"))
    print("ROOT=" + str(root), flush=True)
    cases = {mode: run_case(root / mode, mode, args) for mode in MODES}
    result = {"schema": "buddy.claude-interrupted-suite.v1", "root": str(root), "cases": cases,
        "passed": all(v["passed"] for v in cases.values())}
    common.write_json(root / "result.json", result)
    print("RECEIPT=" + str(root / "result.json"), flush=True)
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
