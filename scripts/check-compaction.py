#!/usr/bin/env python3
"""Independently validate retained native compaction and resume evidence.

No native processes or Buddy database opens. The fixture's success flag is not
proof; checks read original wire traffic, journals and immutable ledger copies.
"""
import argparse
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import sys

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("compaction_evidence", Path(__file__).with_name("check-mixed-fleet.py"))
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)
spec = importlib.util.spec_from_file_location("codex_reconcile_boundary", Path(__file__).with_name("check-interrupted.py"))
reconcile_checks = importlib.util.module_from_spec(spec)
spec.loader.exec_module(reconcile_checks)
ROLE = "coordinator"
STATE = ROLE + "-state/"


def evidence_paths(r):
    # Required originals, rather than a receipt-selected subset of artifacts.
    return [r[k] for k in ("before_compact", "after_compact", "after_resume", "after_reconcile", "after_fresh", "ledger")] + [
        STATE + p for p in ("identity.json", "identity.json.events", "native-observations.jsonl", "launch.json",
                            "compact-request-taken.json", "compact-reply.json")] + [ROLE + "-provider.json"]


def validate(r):
    e = base.Evidence(r)
    def lines(path):
        return [json.loads(x) for x in e.path(path).read_text().splitlines()]
    def journal():
        return lines(STATE + "identity.json.events")
    def wire():
        return lines(STATE + "native-observations.jsonl")
    def frames(kind):
        return [v["frame"] for v in wire() if v["type"] == kind]
    def events():
        return r["restart"]["before"]["events"] + r["sessions"][ROLE]["events"]
    def pair():
        return e.pair(ROLE)
    def mid():
        return r["approval"]["queue"]["delivery"]["message_id"]
    def native_compact(phase):
        return [v for v in frames("native_frame") if v.get("method") == "item/" + phase and
                v["params"]["item"]["type"] == "contextCompaction"]
    def compaction(phase):
        return r["compaction"][phase]["native_event"]
    e.check("schema_provider", lambda: r["schema"] == "buddy.compaction.v1" and r["provider"] == "deterministic-loopback" and
            set(r["sessions"]) == {ROLE} and r["sessions"][ROLE]["native_version"] == "codex-cli 0.160.0")
    e.check("artifact_hashes", lambda: all(hashlib.sha256(e.path(path).read_bytes()).hexdigest() == r[key]
        for path, key in (("source-manifest.json", "source_manifest_sha256"), ("bin/buddy", "buddy_sha256"),
                         ("bin/buddy-adapter", "adapter_sha256"))))
    e.check("original_evidence_hashes", lambda: all(hashlib.sha256(e.path(path).read_bytes()).hexdigest() ==
        r["evidence_sha256"][str(e.path(path).relative_to(e.root))] for path in evidence_paths(r)))
    e.check("native_compact_request_reply", lambda: [v for v in frames("fixture_frame")] == [r["compaction"]["request"]] and
            r["compaction"]["request"] == {"id": "fixture-compact", "method": "thread/compact/start", "params": {"threadId": pair()[0]}} and
            r["compaction"]["reply"] == {"id": "fixture-compact", "result": {}} and
            r["compaction"]["reply"] in frames("native_frame"))
    for phase in ("started", "completed"):
        def item(phase=phase):
            native = native_compact(phase)
            obs = compaction(phase)
            return (len(native) == 1 and native[0]["params"]["threadId"] == pair()[0] and
                    native[0]["params"]["turnId"] == obs["turn_id"] and
                    native[0]["params"]["item"] == {"type": "contextCompaction", "id": obs["item_id"]} and
                    obs["kind"] == "compaction_" + phase and obs["native_session_id"] == pair()[0] and
                    bool(obs["item_id"]) and bool(obs["turn_id"]) and not obs.get("result_status") and not obs.get("result_ref") and
                    r["compaction"][phase] in journal() and r["compaction"][phase] in events())
        e.check("native_item_" + phase, item)
    e.check("same_compaction_item", lambda: all(compaction("started")[k] == compaction("completed")[k]
        for k in ("native_session_id", "turn_id", "item_id")))
    e.check("ordered_compaction_journal", lambda: [journal().index(r["compaction"][p])
        for p in ("started", "completed", "result", "idle")] == sorted(set(journal().index(r["compaction"][p])
        for p in ("started", "completed", "result", "idle"))))
    e.check("compaction_is_not_turn_completion", lambda: all(r["compaction"][p][k]["state"] == "unknown"
        for p in ("started", "completed") for k in ("completion", "acknowledgment", "body_delivery")) and
        r["compaction"]["completed"]["state"]["detail"] == "busy" and
        r["compaction"]["result"]["native_event"]["kind"] == "result" and
        r["compaction"]["result"]["native_event"]["result_status"] == "completed" and
        r["compaction"]["result"]["native_event"]["turn_id"] == compaction("started")["turn_id"] and
        r["compaction"]["result"] in journal() and r["compaction"]["idle"] in journal())
    e.check("fixed_summary_ran", lambda: r["compaction"]["script"]["final_emitted"] and
        r["compaction"]["script"]["step_count"] == 0 and not r["compaction"]["script"]["results"] and
        not json.loads(e.path(ROLE + "-provider.json").read_text())["errors"])
    e.check("same_captured_identity", lambda: r["sessions"][ROLE]["identity"] == r["restart"]["resume"]["identity"] ==
        json.loads(e.path(STATE + "identity.json").read_text())["identity"] and
        all(v["identity"] == r["sessions"][ROLE]["identity"] for v in journal()))
    e.check("resume_not_end", lambda: r["restart"]["before"]["exit"] == 0 and not r["restart"]["before"]["unparsed"] and
        r["restart"]["before"]["children"]["status"] == "observed_absent" and
        r["restart"]["resume"]["state"]["state"] == r["restart"]["resume"]["pending_approval"]["state"] == "unknown" and
        all(e.one(r[name], "SELECT incarnation,ended FROM sessions WHERE session_id=?", (pair()[0],)) ==
            {"incarnation": pair()[1], "ended": None} for name in ("before_compact", "after_compact", "after_resume", "after_reconcile", "after_fresh")))
    for name, query in (
        ("claims", "SELECT claim_id,slug,session_id,incarnation,state,outcome FROM claims ORDER BY claim_id"),
        ("READY", "SELECT session_id,incarnation,decl,ready_sha,cleared FROM session_waits ORDER BY session_id"),
        ("READY_targets", "SELECT * FROM session_wait_targets ORDER BY decl,ord")):
        e.check(name + "_preserved", lambda query=query: bool(e.rows(r["before_compact"], query)) and
            all(e.rows(r["before_compact"], query) == e.rows(r[stage], query)
                for stage in ("after_compact", "after_resume", "after_reconcile", "after_fresh")))
    e.check("READY_names_exact_commit", lambda: base.SHA.fullmatch(r["ready_sha"]) and
        e.one(r["after_resume"], "SELECT ready_sha,cleared FROM session_waits WHERE session_id=?", (pair()[0],)) ==
        {"ready_sha": r["ready_sha"], "cleared": None})
    e.check("body_remained_queued", lambda: all(not e.rows(r[stage],
        "SELECT * FROM inbox_delivery WHERE msg_id=? AND session_id=?", (mid(), pair()[0]))
        for stage in ("before_compact", "after_compact", "after_resume", "after_reconcile")) and
        r["old_inbox"]["messages"]["count"] == 1 and r["old_inbox"]["messages"]["items"][0]["message_id"] == mid() and
        r["old_delivered"]["delivery"]["message_id"] == mid() and r["old_delivered"]["delivery"]["state"] == "delivered" and
        bool(e.rows(r["after_fresh"], "SELECT * FROM inbox_delivery WHERE msg_id=? AND session_id=?", (mid(), pair()[0]))))
    def no_replay():
        trace = wire()
        resumes = [i for i, v in enumerate(trace) if v["type"] == "adapter_frame" and v["frame"].get("method") == "thread/resume"]
        if len(resumes) != 1:
            return False
        rest = [v["frame"] for v in trace[resumes[0] + 1:] if v["type"] == "adapter_frame"]
        inputs = [v for v in rest if v.get("method") in ("turn/start", "turn/steer")]
        answers = [v for v in rest if "result" in v and "method" not in v]
        return (len(inputs) == len(answers) == 1 and r["requests_before"] == r["requests_after"] and
            r["duplicate"]["wake_delivery"] == r["approval"]["wake"]["wake_delivery"] and
            r["duplicate"]["message_id"] == mid() and bool(r["replay"]["error"]) and "approval_delivery" not in r["replay"])
    e.check("no_wake_or_approval_replay", no_replay)
    e.check("fresh_turn_and_approval", lambda: r["fresh_wake"]["message_id"] != mid() and
        any(v.get("approval_delivery", {}).get("state") == "submitted" and
            v["approval_response"]["turn_id"] == r["fresh_wake"]["wake_delivery"]["turn_id"] for v in events()) and
        any(v.get("native_event", {}).get("kind") == "result" and v["native_event"]["result_status"] == "completed" and
            v["native_event"]["turn_id"] == r["fresh_wake"]["wake_delivery"]["turn_id"] for v in events()))
    e.check("reconcile_withdrawn_without_native_query", lambda: reconcile_checks.reconcile_boundary(
        r["reconcile"], r["sessions"][ROLE]["identity"], mid(), r["approval"]["wake"]["wake_delivery"], wire()) and
        r["reconcile"] in events())

    e.check("gate_and_positive_control", lambda: e.path(Path(r["sessions"][ROLE]["worktree"]) / "codex.txt").read_text() == "allowed\n" and
        e.path(Path(r["sessions"][ROLE]["worktree"]) / "claude.txt").read_text() == "original\n" and
        e.path(Path(r["sessions"][ROLE]["worktree"]) / ".fixture/fresh.txt").read_text() == "fresh" and
        "guard" in json.loads(r["fresh_output"])[0]["output"] and
        json.loads(r["fresh_output"])[1]["exit_code"] == json.loads(r["fresh_output"])[3]["exit_code"] == 0)
    e.check("unknown_completion_and_telemetry", lambda: all(v["completion"]["state"] == v["acknowledgment"]["state"] == "unknown" and
        v["capabilities"]["telemetry"]["support"] == "unknown" for v in events()))
    e.check("explicit_release_and_cleanup", lambda: not r["cleanup_errors"] and
        r["sessions"][ROLE]["adapter_exit"] == 0 and not r["sessions"][ROLE]["unparsed"] and
        r["sessions"][ROLE]["native_child_exit"]["status"] == "observed_absent" and
        not e.rows(r["ledger"], "SELECT claim_id FROM claims WHERE state='open'"))
    return {"passed": all(e.checks.values()), "checks": e.checks, "errors": e.errors}


def self_test(r):
    if not validate(r)["passed"]:
        raise AssertionError("positive receipt failed")
    changes = [(("provider",), "real"), (("adapter_sha256",), "wrong"), (("cleanup_errors",), ["failed"]),
        (("ready_sha",), "wrong"), (("requests_after",), -1), (("duplicate", "wake_delivery", "turn_id"), "wrong"),
        (("replay", "error"), ""), (("old_inbox", "messages", "count"), 0), (("after_compact",), r["ledger"]),
        (("after_resume",), r["ledger"]), (("after_reconcile",), r["ledger"]), (("after_fresh",), r["ledger"]),
        (("restart", "resume", "identity", "incarnation"), "wrong"),
        (("restart", "before", "children", "status"), "unknown"),
        (("reconcile", "error"), ""),
        (("reconcile", "turn_observation"), {"source": "native_thread_read"}),
        (("reconcile", "capabilities", "reconcile", "support"), "experimental"),
        (("reconcile", "completion", "state"), "completed"),
        (("compaction", "reply", "result"), {"wrong": True}), (("compaction", "script", "final_emitted"), False)]
    for phase in ("started", "completed"):
        for key in ("native_session_id", "turn_id", "item_id", "kind"):
            changes.append((("compaction", phase, "native_event", key), "wrong"))
        for key in ("completion", "acknowledgment", "body_delivery"):
            changes.append((("compaction", phase, key, "state"), "completed"))
    for path, value in changes:
        bad = copy.deepcopy(r)
        target = bad
        for key in path[:-1]: target = target[key]
        target[path[-1]] = value
        if validate(bad)["passed"]:
            raise AssertionError("accepted damaged receipt: " + ".".join(path))
    return {"positive": "passed", "rejected_mutations": len(changes)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("receipt", type=Path)
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()
    r = json.loads(args.receipt.read_text())
    out = self_test(r) if args.self_test else validate(r)
    print(json.dumps(out, indent=2))
    return 0 if args.self_test or out["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
