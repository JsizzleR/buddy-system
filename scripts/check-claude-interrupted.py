#!/usr/bin/env python3
"""Validate Claude interruption evidence from original captures, without launch.

Terminal errors are not successful work. Crash refusals must leave the ledger,
journal and native wire untouched. Runner flags alone satisfy no check here.
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
spec = importlib.util.spec_from_file_location("claude_interrupted_evidence", Path(__file__).with_name("check-mixed-fleet.py"))
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)
ROLE, STATE = "claude_worker", "claude_worker-state/"
MODES = ("interrupt_active", "interrupt_approval", "crash_active", "crash_approval")


def stages(r):
    return ("before_fault", "after_fault", "after_resume") + (() if r["mode"].startswith("crash") else ("after_fresh",))


def evidence_paths(r):
    paths = [r[k] for k in (*stages(r), "ledger")] + [STATE + p for p in (
        "identity.json", "identity.json.events", "native-observations.jsonl", "launch.json")] + [
        ROLE + "-provider.json", ROLE + "/claude.txt", ROLE + "/codex.txt"]
    if r["mode"].endswith("active"):
        paths += [ROLE + "/.fixture/before-abort.txt"]
    if r["mode"].startswith("interrupt"):
        paths += [ROLE + "/.fixture/" + p for p in ("old-inbox.json", "old-delivered.json", "fresh.txt")]
    return paths


def validate_case(r):
    e = base.Evidence(r)
    crash, active = r["mode"].startswith("crash"), r["mode"].endswith("active")
    def lines(path): return [json.loads(v) for v in e.path(path).read_text().splitlines()]
    def journal(): return lines(STATE + "identity.json.events")
    def wire(): return lines(STATE + "native-observations.jsonl")
    def frames(kind): return [v["frame"] for v in wire() if v["type"] == kind]
    def events(): return r["restart"]["before"]["events"] + r["sessions"][ROLE]["events"]
    def pair(): return e.pair(ROLE)
    def turn(): return r["wake"]["wake_delivery"]["turn_id"]
    def mid(): return r["queue"]["delivery"]["message_id"]
    def digest(path): return hashlib.sha256(e.path(path).read_bytes()).hexdigest()
    version = r.get("claude_version", "2.1.289")
    e.check("schema_provider", lambda: r["schema"] == "buddy.claude-interrupted.v1" and r["mode"] in MODES and
        r["provider"] == "deterministic-loopback" and r["loopback"] in ("127.0.0.1", "::1") and set(r["sessions"]) == {ROLE} and
        version in ("2.1.289", "2.1.290", "2.1.291") and r["sessions"][ROLE]["native_version"] == version + " (Claude Code)" and
        json.loads(e.path(STATE + "launch.json").read_text())["harness_version"] == version and
        json.loads(e.path(STATE + "launch.json").read_text())["hooks"]["mods"] == "builtin-only-" + version)
    e.check("artifact_hashes", lambda: all(digest(path) == r[key] for path, key in (
        ("source-manifest.json", "source_manifest_sha256"), ("bin/buddy", "buddy_sha256"), ("bin/buddy-adapter", "adapter_sha256"))))
    e.check("original_evidence_hashes", lambda: all(digest(p) == r["evidence_sha256"][str(e.path(p).relative_to(e.root))]
        for p in evidence_paths(r)))
    e.check("captured_identity", lambda: r["sessions"][ROLE]["identity"] ==
        json.loads(e.path(STATE + "identity.json").read_text())["identity"] and
        bool(journal()) and all(v["identity"] == r["sessions"][ROLE]["identity"] for v in journal()))
    e.check("native_wake_accepted", lambda: r["wake"]["wake_delivery"]["state"] == "delivered" and bool(turn()) and
        r["wake"]["message_id"] == mid() and r["wake"] in journal() and r["wake"] in events() and
        any(v.get("type") == "user" and v.get("isReplay") is True and v.get("uuid") == turn() and
            v.get("session_id") == pair()[0] for v in frames("native_frame")))
    def approval():
        obs = r["approval"]["native_event"]["approval"]
        return (r["approval"] in journal() and r["approval"] in events() and obs["native_session_id"] == pair()[0] and
            obs["turn_id"] == turn() and r["approval"]["state"] == {"state": "observed", "detail": "waiting_for_approval"} and
            r["old_decision"] == dict(id=obs["id"], native_session_id=pair()[0], turn_id=turn(), decision="allow") and
            any(v.get("type") == "control_request" and v.get("request_id") == obs["id"] and
                v.get("request") == obs["detail"] for v in frames("native_frame")))
    e.check("pending_approval_observed", approval)
    def fault():
        if active:
            return (r["provider_barrier"] is True and r["response_released_at_fault"] is False and
                r["approved"]["approval_response"] == r["old_decision"] and r["approved"] in journal() and
                r["approved"]["approval_delivery"]["state"] == "submitted" and
                e.path(ROLE + "/.fixture/before-abort.txt").read_text() == "once\n" and
                len(r["fault_script"]["results"]) == 1 and r["fault_script"]["results"][0]["exit_code"] == 0)
        return not r["fault_script"]["results"] and not r["fault_script"]["final_emitted"] and "approved" not in r
    e.check("fault_seam_and_tool_control", fault)
    e.check("unapproved_tool_never_runs", lambda: not (e.root / ROLE / ".fixture/unapproved.txt").exists())
    def terminal(wake, status):
        tid = wake["wake_delivery"]["turn_id"]
        results = [v for v in journal() if v.get("native_event", {}).get("kind") == "result" and v["native_event"].get("turn_id") == tid]
        idles = [v for v in journal() if v.get("native_event", {}).get("kind") == "idle" and v["native_event"].get("turn_id") == tid]
        if len(results) != 1 or len(idles) != 1: return False
        result, idle = results[0], idles[0]
        obs = result["native_event"]
        native = [v for v in frames("native_frame") if v.get("type") == "result" and v.get("uuid") == obs["result_ref"]]
        if len(native) != 1: return False
        raw = native[0]
        expected_error = "native result: error_during_execution/" + status if status != "completed" else ""
        return (raw["session_id"] == pair()[0] and raw["user_message_uuid"] == tid and raw["user_message_uuids"] == [tid] and
            raw["queued_turn_count"] == 0 and raw["terminal_reason"] == obs["result_status"] == status and
            raw["is_error"] == (status != "completed") and raw["subtype"] == ("success" if status == "completed" else "error_during_execution") and
            obs.get("error", "") == expected_error and result in events() and idle in events() and journal().index(result) < journal().index(idle))
    if crash:
        e.check("unfinished_turn_has_no_terminal", lambda: not any(v.get("type") == "result" for v in frames("native_frame")) and
            not any(v.get("native_event", {}).get("kind") in ("result", "idle") for v in journal()))
        e.check("resume_refused_before_side_effects", lambda: "unfinished or uncertain" in r["restart"]["resume"]["error"] and
            r["refusal_exit"] != 0 and r["requests_before_resume"] == r["requests_after_resume"] and
            r["journal_before_resume"] == r["journal_after_refusal"] == digest(STATE + "identity.json.events") and
            r["wire_before_resume"] == r["wire_after_refusal"] == digest(STATE + "native-observations.jsonl") and
            e.rows(r["after_fault"], "SELECT * FROM sessions ORDER BY session_id") ==
            e.rows(r["after_resume"], "SELECT * FROM sessions ORDER BY session_id"))
    else:
        e.check("exact_interrupt_only", lambda: bool(r["wrong_interrupt"]["error"]) and not r["interrupt"].get("error") and
            r["interrupt"]["state"]["state"] == "unknown" and r["interrupt"] in journal() and
            [v["request"] for v in frames("adapter_frame") if v.get("type") == "control_request" and
                v["request"].get("subtype") == "interrupt"] == [{"subtype": "interrupt"}])
        e.check("cancelled_result_and_idle", lambda: terminal(r["wake"], "aborted_streaming" if active else "aborted_tools") and
            r["terminal"] in journal() and r["idle"] in journal() and r["terminal"]["native_event"]["turn_id"] == turn())
        e.check("resumed_identity_unknown_state", lambda: not r["restart"]["resume"].get("error") and
            r["restart"]["resume"]["identity"] == r["sessions"][ROLE]["identity"] and r["restart"]["resume"] in journal() and
            r["restart"]["resume"]["state"]["state"] == r["restart"]["resume"]["pending_approval"]["state"] == "unknown")
        e.check("no_wake_or_old_approval_replay", lambda: r["requests_before_resume"] == r["requests_after_resume"] == r["requests_after_replay"] and
            r["duplicate"]["wake_delivery"] == r["wake"]["wake_delivery"] and r["duplicate"]["message_id"] == mid() and
            bool(r["replay"]["error"]) and "approval_delivery" not in r["replay"] and not r["inspect"].get("approvals"))
        e.check("fresh_native_result", lambda: terminal(r["fresh_wake"], "completed"))
        e.check("fresh_approval", lambda: any(v.get("approval_delivery", {}).get("state") == "submitted" and
            v["approval_response"]["turn_id"] == r["fresh_wake"]["wake_delivery"]["turn_id"] for v in journal()))
        def history():
            requests = json.loads(e.path(ROLE + "-provider.json").read_text())["requests"]
            boundary = r["requests_before_resume"]
            outputs = requests[boundary]["tool_outputs"]
            item = r["approval"]["native_event"]["approval"]["item_id"]
            return (boundary > 0 and requests[boundary]["input_fingerprint"] != requests[boundary-1]["input_fingerprint"] and
                any(v["call_id"] == item and v["is_error"] == (not active) for v in outputs) and
                all(v in outputs for v in requests[boundary-1]["tool_outputs"]))
        e.check("restored_native_tool_history", history)
        e.check("resumed_gate_denial_and_control", lambda: e.path(ROLE + "/claude.txt").read_text() == "allowed\n" and
            e.path(ROLE + "/codex.txt").read_text() == "original\n" and e.path(ROLE + "/.fixture/fresh.txt").read_text() == "fresh" and
            "guard" in json.dumps(json.loads(r["fresh_output"])[0]) and json.loads(r["fresh_output"])[0]["is_error"] and
            not json.loads(r["fresh_output"])[1]["is_error"] and json.loads(r["fresh_output"])[2]["exit_code"] == 0)
        e.check("explicit_original_body_retrieval", lambda: r["old_inbox"] == json.loads(e.path(ROLE + "/.fixture/old-inbox.json").read_text()) and
            r["old_delivered"] == json.loads(e.path(ROLE + "/.fixture/old-delivered.json").read_text()) and
            r["old_inbox"]["messages"]["count"] == 1 and r["old_inbox"]["messages"]["items"][0]["message_id"] == mid() and
            r["old_delivered"]["delivery"]["message_id"] == mid() and r["old_delivered"]["delivery"]["state"] == "delivered" and
            bool(e.rows(r["after_fresh"], "SELECT * FROM inbox_delivery WHERE msg_id=? AND session_id=?", (mid(), pair()[0]))))
    def native_inputs():
        inputs = [v for v in frames("adapter_frame") if v.get("type") == "user"]
        answers = [v["response"]["request_id"] for v in frames("adapter_frame") if v.get("type") == "control_response"]
        approved = [v["approval_response"]["id"] for v in journal() if v.get("approval_delivery", {}).get("state") == "submitted"]
        return [v["uuid"] for v in inputs] == [turn()] + ([] if crash else [r["fresh_wake"]["wake_delivery"]["turn_id"]]) and answers == approved
    e.check("native_inputs_and_approval_answers_exact", native_inputs)
    def launches():
        rows = wire()
        launched = [v["args"] for v in rows if v["type"] == "buddy_fixture_native_process" and "--print" in v["args"]]
        if len(launched) != (1 if crash else 2) or launched[0][launched[0].index("--session-id")+1] != pair()[0]: return False
        if crash: return True
        args = launched[1]
        if args[args.index("--resume")+1] != pair()[0] or any(flag in args for flag in ("--continue", "--session-id", "--fork-session")): return False
        if args[args.index("--permission-mode")+1] != "manual": return False
        last = max(i for i, v in enumerate(rows) if v["type"] == "buddy_fixture_native_process" and "--resume" in v["args"])
        stream = [v["frame"] for v in rows[last:] if v["type"] == "adapter_frame"]
        boundary = next(i for i, v in enumerate(stream) if v.get("type") == "user")
        return [v["request"]["subtype"] for v in stream[:boundary] if v.get("type") == "control_request"] == [
            "initialize", "get_settings", "reload_plugins", "get_settings"]
    e.check("native_launch_and_renewed_controls", launches)
    e.check("shutdown_observed", lambda: r["restart"]["before"]["exit"] == (-9 if crash else 0) and
        not r["restart"]["before"]["unparsed"] and r["restart"]["before"]["children"]["status"] == "observed_absent")
    e.check("logical_identity_not_ended", lambda: all(e.one(r[stage], "SELECT incarnation,ended FROM sessions WHERE session_id=?", (pair()[0],)) ==
        {"incarnation": pair()[1], "ended": None} for stage in stages(r)))
    for name, query in (("claims", "SELECT claim_id,slug,session_id,incarnation,state,outcome FROM claims ORDER BY claim_id"),
        ("READY", "SELECT session_id,incarnation,decl,ready_sha,cleared FROM session_waits ORDER BY session_id"),
        ("READY_targets", "SELECT * FROM session_wait_targets ORDER BY decl,ord")):
        e.check(name + "_preserved", lambda query=query: bool(e.rows(r["before_fault"], query)) and
            all(e.rows(r["before_fault"], query) == e.rows(r[stage], query) for stage in stages(r)[1:]))
    e.check("READY_exact_commit", lambda: base.SHA.fullmatch(r["ready_sha"]) and
        base.git(e.path(r["sessions"][ROLE]["worktree"]), "rev-parse", "HEAD") == r["ready_sha"] and
        e.one(r["after_resume"], "SELECT ready_sha,cleared FROM session_waits WHERE session_id=?", (pair()[0],)) ==
        {"ready_sha": r["ready_sha"], "cleared": None})
    e.check("original_body_stays_queued", lambda: all(not e.rows(r[stage],
        "SELECT * FROM inbox_delivery WHERE msg_id=? AND session_id=?", (mid(), pair()[0])) for stage in ("before_fault", "after_fault", "after_resume")))
    e.check("work_completion_and_telemetry_unknown", lambda: all(v["completion"]["state"] == v["acknowledgment"]["state"] == "unknown" and
        v["capabilities"]["telemetry"]["support"] == "unknown" for v in events()))
    e.check("explicit_release_and_cleanup", lambda: not r["cleanup_errors"] and not r.get("forced_cleanup_groups") and
        r["final_child_exit"]["status"] == "observed_absent" and
        r["sessions"][ROLE]["adapter_exit"] == (1 if crash else 0) and not r["sessions"][ROLE]["unparsed"] and
        r["sessions"][ROLE]["native_child_exit"]["status"] == "observed_absent" and
        not e.rows(r["ledger"], "SELECT claim_id FROM claims WHERE state='open'") and
        not json.loads(e.path(ROLE + "-provider.json").read_text())["errors"] and
        all(v["error_type"] == "connection_closed" for v in r.get("fault_provider_errors", [])))
    return {"passed": all(e.checks.values()), "checks": e.checks, "errors": e.errors}


def validate(r):
    if r.get("schema") != "buddy.claude-interrupted-suite.v1" or set(r.get("cases", {})) != set(MODES):
        return {"passed": False, "error": "require all four fault cases"}
    cases = {mode: validate_case(r["cases"][mode]) for mode in MODES}
    # One run, one build: never four individually valid cases from different
    # runs (F-13; check-mixed-fleet.suite_binding says what each half binds).
    bound = base.suite_binding(r, MODES)
    return {"passed": all(v["passed"] for v in cases.values()) and all(r["cases"][m]["mode"] == m for m in MODES) and
        bound["same_run"] and bound["same_contract"], "cases": cases, **bound}


def self_test(r):
    if not validate(r)["passed"]: raise AssertionError("positive receipt failed")
    count = 0
    for mode in MODES:
        changes = [(("claude_version",), "2.1.292"),
            (("claude_version",), "2.1.290" if r["cases"][mode].get("claude_version", "2.1.289") == "2.1.289" else "2.1.289"),
            (("adapter_sha256",), "wrong"), (("ready_sha",), "wrong"), (("cleanup_errors",), ["failed"]),
            (("loopback",), "0.0.0.0"), (("final_child_exit", "status"), "unknown"),
            (("wake", "wake_delivery", "turn_id"), "wrong"), (("after_resume",), r["cases"][mode]["ledger"]),
            (("restart", "before", "children", "status"), "unknown"), (("requests_after_resume",), -1),
            (("old_decision", "turn_id"), "wrong"), (("fault_script", "results"), [{"exit_code": 1}])]
        if mode.endswith("active"): changes += [(("provider_barrier",), False), (("response_released_at_fault",), True)]
        if mode.startswith("crash"):
            changes += [(("restart", "resume", "error"), ""), (("journal_after_refusal",), "changed"),
                (("wire_after_refusal",), "changed"), (("refusal_exit",), 0)]
        else:
            changes += [(("terminal", "native_event", "result_status"), "completed"), (("wrong_interrupt", "error"), ""),
                (("replay", "error"), ""), (("requests_after_replay",), -1), (("fresh_output",), "[]"),
                (("old_inbox", "messages", "count"), 0), (("restart", "resume", "identity", "incarnation"), "wrong")]
        for path, value in changes:
            bad = copy.deepcopy(r); target = bad["cases"][mode]
            for key in path[:-1]: target = target[key]
            target[path[-1]] = value
            if validate(bad)["passed"]: raise AssertionError("accepted damaged receipt: " + mode + "." + ".".join(path))
            count += 1
    for mode in MODES:
        bad = copy.deepcopy(r); del bad["cases"][mode]
        if validate(bad)["passed"]: raise AssertionError("accepted missing case")
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


if __name__ == "__main__": sys.exit(main())
