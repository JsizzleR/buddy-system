#!/usr/bin/env python3
"""Validate interrupted-compaction evidence without launching native processes.

Read original wire frames, durable journals and immutable ledger copies. The
runner's success flags and a missing completion event are never sufficient.
"""
import argparse
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import sys
import tempfile

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("interrupted_compaction_evidence", Path(__file__).with_name("check-mixed-fleet.py"))
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)
spec = importlib.util.spec_from_file_location("codex_reconcile_boundary", Path(__file__).with_name("check-interrupted.py"))
reconcile_checks = importlib.util.module_from_spec(spec)
spec.loader.exec_module(reconcile_checks)
ROLE, STATE = "coordinator", "coordinator-state/"
MODES = ("cancel", "active_crash", "lost_completion")
STAGES = ("before_compact", "after_fault", "after_resume", "after_reconcile", "after_fresh")


def evidence_paths(r):
    return [r[k] for k in (*STAGES, "ledger")] + [STATE + p for p in (
        "identity.json", "identity.json.events", "native-observations.jsonl", "launch.json",
        "fixture-compact-taken.json", "fixture-compact-reply.json", "fixture-history-taken.json", "fixture-history-reply.json"
    )] + [ROLE + "-provider.json", ROLE + "/.fixture/old-inbox.json", ROLE + "/.fixture/old-delivered.json"]


def validate_case(r):
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
    def turn():
        return r["compaction"]["started"]["native_event"]["turn_id"]
    def native_items(phase):
        return [v for v in frames("native_frame") if v.get("method") == "item/" + phase and
                v["params"].get("item", {}).get("type") == "contextCompaction"]
    def native_results():
        return [v for v in frames("native_frame") if v.get("method") == "turn/completed" and v["params"]["turn"]["id"] == turn()]
    e.check("schema_provider", lambda: r["schema"] == "buddy.interrupted-compaction.v1" and r["mode"] in MODES and
        r["provider"] == "deterministic-loopback" and set(r["sessions"]) == {ROLE} and
        r["sessions"][ROLE]["native_version"] == "codex-cli 0.160.0")
    e.check("artifact_hashes", lambda: all(hashlib.sha256(e.path(path).read_bytes()).hexdigest() == r[key]
        for path, key in (("source-manifest.json", "source_manifest_sha256"), ("bin/buddy", "buddy_sha256"), ("bin/buddy-adapter", "adapter_sha256"))))
    e.check("original_evidence_hashes", lambda: all(hashlib.sha256(e.path(path).read_bytes()).hexdigest() ==
        r["evidence_sha256"][str(e.path(path).relative_to(e.root))] for path in evidence_paths(r)))
    e.check("one_compact_no_retry", lambda: [v for v in frames("fixture_frame") if v.get("method") == "thread/compact/start"] ==
        [r["compaction"]["request"]] and r["compaction"]["request"] ==
        {"id": "fixture-compact", "method": "thread/compact/start", "params": {"threadId": pair()[0]}} and
        r["compaction"]["reply"] == {"id": "fixture-compact", "result": {}} and r["compaction"]["reply"] in frames("native_frame") and
        not any(v.get("method") == "thread/compact/start" for v in frames("adapter_frame")))
    def started():
        obs = r["compaction"]["started"]["native_event"]
        native = native_items("started")
        return (len(native) == 1 and native[0]["params"]["threadId"] == pair()[0] == obs["native_session_id"] and
            native[0]["params"]["turnId"] == turn() and bool(turn()) and bool(obs["item_id"]) and
            native[0]["params"]["item"] == {"type": "contextCompaction", "id": obs["item_id"]} and
            obs["kind"] == "compaction_started" and not obs.get("result_status") and not obs.get("result_ref") and
            r["compaction"]["started"] in journal() and r["compaction"]["started"] in events())
    e.check("native_item_started", started)
    e.check("observation_not_completion", lambda: all(r["compaction"]["started"][k]["state"] == "unknown"
        for k in ("completion", "acknowledgment", "body_delivery")) and
        not any(v.get("native_event", {}).get("kind") == "compaction_completed" for v in journal()))
    def fault():
        results, completed = native_results(), native_items("completed")
        if r["mode"] == "cancel":
            request = r["fault"]["interrupt"]
            return (r["fault"]["provider_barrier"] is True and r["fault"]["response_released_at_fault"] is False and
                bool(r["fault"]["wrong_interrupt"]["error"]) and request["state"]["state"] == "unknown" and
                not request.get("error") and len(results) == 1 and not completed and
                results[0]["params"]["threadId"] == pair()[0] and results[0]["params"]["turn"]["status"] == "interrupted" and
                r["fault"]["result"]["native_event"]["result_status"] == "interrupted" and
                r["fault"]["result"]["native_event"]["turn_id"] == r["fault"]["idle"]["native_event"]["turn_id"] == turn() and
                all(r["fault"][k] in journal() and r["fault"][k] in events() for k in ("interrupt", "result", "idle")) and
                [v["params"] for v in frames("adapter_frame") if v.get("method") == "turn/interrupt"] ==
                [{"threadId": pair()[0], "turnId": turn()}])
        if r["mode"] == "active_crash":
            return (r["fault"]["provider_barrier"] is True and r["fault"]["response_released_at_fault"] is False and not results and not completed)
        return (len(results) == len(completed) == 1 and results[0] == r["fault"]["dropped_result"] and
            completed[0] == r["fault"]["dropped_item"] and results[0]["params"]["threadId"] == pair()[0] and
            results[0]["params"]["turn"]["status"] == "completed" and
            completed[0]["params"]["threadId"] == pair()[0] and completed[0]["params"]["turnId"] == turn() and
            completed[0]["params"]["item"] == native_items("started")[0]["params"]["item"] and
            frames("dropped_frame") == [completed[0], results[0]])
    e.check("exact_fault_observed", fault)
    e.check("fault_not_provider_failure", lambda: not json.loads(e.path(ROLE + "-provider.json").read_text())["errors"] and
        all(v["error_type"] == "connection_closed" for v in r["fault"].get("provider_errors", [])) and
        r["compaction"]["script"]["step_count"] == 0 and not r["compaction"]["script"]["results"])
    e.check("no_invented_compaction_result", lambda: r["mode"] == "cancel" or not any(
        v.get("native_event", {}).get("turn_id") == turn() and v["native_event"]["kind"] in ("result", "idle") for v in journal()))
    e.check("same_captured_identity", lambda: r["sessions"][ROLE]["identity"] == r["restart"]["resume"]["identity"] ==
        json.loads(e.path(STATE + "identity.json").read_text())["identity"] and
        all(v["identity"] == r["sessions"][ROLE]["identity"] for v in journal()))
    e.check("shutdown_and_resume", lambda: r["restart"]["before"]["exit"] == (0 if r["mode"] == "cancel" else -9) and
        not r["restart"]["before"]["unparsed"] and r["restart"]["before"]["children"]["status"] == "observed_absent" and
        r["restart"]["resume"]["state"]["state"] == r["restart"]["resume"]["pending_approval"]["state"] == "unknown")
    e.check("logical_session_not_ended", lambda: all(e.one(r[stage], "SELECT incarnation,ended FROM sessions WHERE session_id=?", (pair()[0],)) ==
        {"incarnation": pair()[1], "ended": None} for stage in STAGES))
    for name, query in (
        ("claims", "SELECT claim_id,slug,session_id,incarnation,state,outcome FROM claims ORDER BY claim_id"),
        ("READY", "SELECT session_id,incarnation,decl,ready_sha,cleared FROM session_waits ORDER BY session_id"),
        ("READY_targets", "SELECT * FROM session_wait_targets ORDER BY decl,ord")):
        e.check(name + "_preserved", lambda query=query: bool(e.rows(r["before_compact"], query)) and
            all(e.rows(r["before_compact"], query) == e.rows(r[stage], query) for stage in STAGES[1:]))
    e.check("READY_exact_commit", lambda: base.SHA.fullmatch(r["ready_sha"]) and
        base.git(e.path(r["sessions"][ROLE]["worktree"]), "rev-parse", "HEAD") == r["ready_sha"] and
        e.one(r["after_resume"], "SELECT ready_sha,cleared FROM session_waits WHERE session_id=?", (pair()[0],)) == {"ready_sha": r["ready_sha"], "cleared": None})
    e.check("body_queued_until_explicit_fetch", lambda: all(not e.rows(r[stage],
        "SELECT * FROM inbox_delivery WHERE msg_id=? AND session_id=?", (mid(), pair()[0])) for stage in STAGES[:-1]) and
        r["old_inbox"] == json.loads(e.path(ROLE + "/.fixture/old-inbox.json").read_text()) and
        r["old_delivered"] == json.loads(e.path(ROLE + "/.fixture/old-delivered.json").read_text()) and
        r["old_inbox"]["messages"]["count"] == 1 and r["old_inbox"]["messages"]["items"][0]["message_id"] == mid() and
        r["old_delivered"]["delivery"]["message_id"] == mid() and r["old_delivered"]["delivery"]["state"] == "delivered" and
        bool(e.rows(r["after_fresh"], "SELECT * FROM inbox_delivery WHERE msg_id=? AND session_id=?", (mid(), pair()[0]))))
    def replay():
        trace = wire()
        resumes = [i for i, v in enumerate(trace) if v["type"] == "adapter_frame" and v["frame"].get("method") == "thread/resume"]
        if len(resumes) != 1:
            return False
        rest = [v["frame"] for v in trace[resumes[0] + 1:] if v["type"] == "adapter_frame"]
        return (len([v for v in rest if v.get("method") in ("turn/start", "turn/steer")]) == 1 and
            len([v for v in rest if "result" in v and "method" not in v]) == 1 and
            r["requests_before"] == r["requests_after"] and
            r["duplicate"]["wake_delivery"] == r["approval"]["wake"]["wake_delivery"] and r["duplicate"]["message_id"] == mid() and
            bool(r["replay"]["error"]) and "approval_delivery" not in r["replay"])
    e.check("no_wake_or_approval_replay", replay)
    e.check("reconcile_withdrawn_without_native_query", lambda: reconcile_checks.reconcile_boundary(
        r["reconcile"], r["sessions"][ROLE]["identity"], mid(), r["approval"]["wake"]["wake_delivery"], wire()) and
        r["reconcile"] in events())

    e.check("separate_compaction_history", lambda: r["compaction_history"] == json.loads(e.path(STATE + "fixture-history-reply.json").read_text()) and
        r["compaction_history"] in frames("native_frame") and r["compaction_history"]["result"]["thread"]["id"] == pair()[0] and
        [v for v in frames("fixture_frame") if v.get("method") == "thread/read"] ==
        [{"id": "fixture-history", "method": "thread/read", "params": {"threadId": pair()[0], "includeTurns": True}}])
    e.check("exact_compaction_history_status", lambda: [v["status"] for v in r["compaction_history"]["result"]["thread"]["turns"]
        if v["id"] == turn()] == ["completed" if r["mode"] == "lost_completion" else "interrupted"])
    e.check("fresh_native_turn_and_approval", lambda: r["fresh_wake"]["message_id"] != mid() and
        any(v.get("approval_delivery", {}).get("state") == "submitted" and
            v["approval_response"]["turn_id"] == r["fresh_wake"]["wake_delivery"]["turn_id"] for v in events()) and
        any(v.get("native_event", {}).get("kind") == "result" and v["native_event"]["result_status"] == "completed" and
            v["native_event"]["turn_id"] == r["fresh_wake"]["wake_delivery"]["turn_id"] for v in events()))
    e.check("gate_and_positive_control", lambda: e.path(ROLE + "/codex.txt").read_text() == "allowed\n" and
        e.path(ROLE + "/claude.txt").read_text() == "original\n" and e.path(ROLE + "/.fixture/fresh.txt").read_text() == "fresh" and
        "guard" in json.loads(r["fresh_output"])[0]["output"] and
        json.loads(r["fresh_output"])[1]["exit_code"] == json.loads(r["fresh_output"])[3]["exit_code"] == 0)
    e.check("unknown_completion_and_telemetry", lambda: all(v["completion"]["state"] == v["acknowledgment"]["state"] == "unknown" and
        v["capabilities"]["telemetry"]["support"] == "unknown" for v in events()))
    e.check("explicit_release_and_cleanup", lambda: not r["cleanup_errors"] and r["sessions"][ROLE]["adapter_exit"] == 0 and
        not r["sessions"][ROLE]["unparsed"] and r["sessions"][ROLE]["native_child_exit"]["status"] == "observed_absent" and
        not e.rows(r["ledger"], "SELECT claim_id FROM claims WHERE state='open'"))
    return {"passed": all(e.checks.values()), "checks": e.checks, "errors": e.errors}


def validate(r):
    if r.get("schema") != "buddy.interrupted-compaction-suite.v1" or set(r.get("cases", {})) != set(MODES):
        return {"passed": False, "error": "required suite cases absent"}
    cases = {mode: validate_case(r["cases"][mode]) for mode in MODES}
    # One run, one build: never three individually valid cases from different
    # runs (F-13; check-mixed-fleet.suite_binding says what each half binds).
    bound = base.suite_binding(r, MODES)
    return {"passed": all(v["passed"] and r["cases"][mode]["mode"] == mode for mode, v in cases.items()) and
        bound["same_run"] and bound["same_contract"], "cases": cases, **bound}


def self_test(r):
    if not validate(r)["passed"]:
        raise AssertionError("positive receipt failed")
    count = 0
    for mode in MODES:
        original = r["cases"][mode]
        changes = [(("provider",), "real"), (("adapter_sha256",), "wrong"), (("ready_sha",), "wrong"),
            (("after_fault",), original["ledger"]), (("after_resume",), original["ledger"]), (("after_reconcile",), original["ledger"]), (("after_fresh",), original["ledger"]),
            (("requests_after",), -1), (("replay", "error"), ""), (("old_inbox", "messages", "count"), 0),
            (("duplicate", "wake_delivery", "turn_id"), "wrong"), (("restart", "resume", "identity", "incarnation"), "wrong"),
            (("restart", "before", "children", "status"), "unknown"), (("compaction", "reply", "result"), {"wrong": True}),
            (("reconcile", "error"), ""),
        (("reconcile", "turn_observation"), {"source": "native_thread_read"}),
        (("reconcile", "capabilities", "reconcile", "support"), "experimental"), (("reconcile", "completion", "state"), "completed"),
            (("cleanup_errors",), ["failed"]), (("compaction_history", "result", "thread", "id"), "wrong")]
        for key in ("kind", "native_session_id", "turn_id", "item_id"):
            changes.append((("compaction", "started", "native_event", key), "wrong"))
        for key in ("completion", "body_delivery", "acknowledgment"):
            changes.append((("compaction", "started", key, "state"), "completed"))
        if mode == "cancel":
            changes.extend([(("fault", "wrong_interrupt", "error"), ""), (("fault", "result", "native_event", "result_status"), "completed")])
        if mode == "lost_completion":
            changes.append((("fault", "dropped_result", "params", "turn", "status"), "interrupted"))
        else:
            changes.append((("fault", "response_released_at_fault"), True))
        for path, value in changes:
            bad = copy.deepcopy(original)
            target = bad
            for key in path[:-1]:
                target = target[key]
            target[path[-1]] = value
            if validate_case(bad)["passed"]:
                raise AssertionError("accepted damaged receipt: " + mode + "." + ".".join(path))
            count += 1
    with tempfile.TemporaryDirectory(prefix="buddy-suite-splice-") as scratch:
        count += base.suite_splice_self_test(r, MODES, validate, validate_case, scratch)
    return {"positive": "passed", "rejected_mutations": count}


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
