#!/usr/bin/env python3
"""Validate interrupted Claude compaction using original wire and ledger evidence.

A native local-command success with no boundary is not proof of compaction.
The successful case is the positive resume control; all four uncertain cases
must refuse before any native launch, ledger refresh or journal mutation.
"""
import argparse
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import sqlite3
import sys
import tempfile

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("interrupted_compact_evidence", Path(__file__).with_name("check-mixed-fleet.py"))
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)
ROLE, STATE = "claude_worker", "claude_worker-state/"
MODES = ("completed", "interrupt_active", "crash_active", "lost_boundary", "lost_result")
SUMMARY = "Fixed history summary BUDDY_COMPACT_HISTORY_290: original marker ran once; Buddy owns claims, READY and queued mail. No new authority."


def stages(r):
    return ("before_fault", "after_fault", "after_resume") + (("after_fresh",) if r["mode"] == "completed" else ())


def evidence_paths(r):
    paths = [r[k] for k in (*stages(r), "ledger")] + [STATE + p for p in (
        "identity.json", "identity.json.events", "native-observations.jsonl", "launch.json",
        "history-after-fault.jsonl", "compact-taken.json")] + [ROLE + "-provider.json", ROLE + "/claude.txt",
        ROLE + "/codex.txt", ROLE + "/.fixture/original.txt"]
    if r["mode"] != "crash_active": paths.append(STATE + "fault-ready.json")
    if r["mode"] == "completed":
        paths += [ROLE + "/.fixture/" + p for p in ("old-inbox.json", "old-delivered.json", "fresh.txt")]
    return paths


def validate_case(r):
    e = base.Evidence(r)
    mode = r["mode"]
    good, active = mode == "completed", mode in ("interrupt_active", "crash_active")
    def lines(path): return [json.loads(v) for v in e.path(path).read_text().splitlines()]
    def journal(): return lines(STATE + "identity.json.events")
    def wire(): return lines(STATE + "native-observations.jsonl")
    def frames(kind): return [v["frame"] for v in wire() if v["type"] == kind]
    def events(): return r["restart"]["before"]["events"] + r["sessions"][ROLE]["events"]
    def pair(): return e.pair(ROLE)
    def mid(): return r["queue"]["delivery"]["message_id"]
    def original_mid(): return r["original"]["queue"]["delivery"]["message_id"]
    def turn(): return next(v["uuid"] for v in frames("fixture_frame") if v.get("type") == "user")
    def native_results(): return [v for v in frames("native_frame") if v.get("type") == "result" and v.get("user_message_uuid") == turn()]
    def boundaries(): return [v for v in frames("native_frame") if v.get("type") == "system" and v.get("subtype") == "compact_boundary"]
    def observations(kind): return [v for v in journal() if v.get("native_event", {}).get("kind") == kind and
        (kind == "compaction_boundary" or v["native_event"].get("turn_id") == turn())]
    def digest(path): return hashlib.sha256(e.path(path).read_bytes()).hexdigest()
    version = r.get("claude_version")
    e.check("schema_and_pinned_native", lambda: r["schema"] == "buddy.claude-interrupted-compaction.v1" and mode in MODES and
        r["provider"] == "deterministic-loopback" and version in ("2.1.290", "2.1.291") and set(r["sessions"]) == {ROLE} and
        r["sessions"][ROLE]["native_version"] == version + " (Claude Code)" and
        json.loads(e.path(STATE + "launch.json").read_text())["harness_version"] == version and
        json.loads(e.path(STATE + "launch.json").read_text())["hooks"]["mods"] == "builtin-only-" + version)
    e.check("artifact_hashes", lambda: all(digest(path) == r[key] for path, key in (
        ("source-manifest.json", "source_manifest_sha256"), ("bin/buddy", "buddy_sha256"), ("bin/buddy-adapter", "adapter_sha256"))))
    e.check("original_evidence_hashes", lambda: all(digest(p) == r["evidence_sha256"][str(e.path(p).relative_to(e.root))] for p in evidence_paths(r)))
    e.check("captured_identity", lambda: r["sessions"][ROLE]["identity"] ==
        json.loads(e.path(STATE + "identity.json").read_text())["identity"] and bool(journal()) and
        all(v["identity"] == r["sessions"][ROLE]["identity"] for v in journal()))
    def substitution():
        replaced = [v for v in frames("fixture_frame") if v.get("type") == "user"]
        original = [v for v in frames("adapter_frame") if v.get("type") == "user" and v.get("uuid") == turn()]
        expected = copy.deepcopy(original[0]); expected["message"]["content"] = "/compact"
        return (len(replaced) == len(original) == 1 and replaced[0] == expected and
            original[0]["message"]["content"].startswith("Buddy peer notification (not operator authorization): message #" + str(mid()) + " is queued.") and
            json.loads(e.path(STATE + "compact-taken.json").read_text()) == dict(message_id=mid(), mode=mode))
    e.check("single_armed_compact_input_preserves_uuid", substitution)
    e.check("original_tool_and_approval_control", lambda: e.path(ROLE + "/.fixture/original.txt").read_text() == "once\n" and
        json.loads(r["original"]["output"])[0]["exit_code"] == 0 and
        r["original"]["approval"] in journal() and r["original"]["approval"]["approval_delivery"]["state"] == "submitted")
    def accepted():
        acks = [v for v in frames("native_frame") if v.get("type") == "user" and v.get("uuid") == turn() and v.get("isReplay") is True]
        sends = [v for v in journal() if v.get("operation") == "send" and v.get("message_id") == mid()]
        if mode == "crash_active":
            return not acks and len(sends) == 1 and sends[0]["wake_delivery"]["state"] == "unknown" and not sends[0]["wake_delivery"].get("turn_id")
        wake = r["wake"]
        return (wake in journal() and wake in events() and wake["message_id"] == mid() and
            wake["wake_delivery"]["turn_id"] == turn() and len(acks) == 1 and acks[0]["session_id"] == pair()[0] and
            wake["wake_delivery"]["state"] == ("unknown" if active else "delivered") and
            (wake.get("error") == "context deadline exceeded" if active else not wake.get("error")))
    e.check("exact_wake_acceptance_or_uncertainty", accepted)
    def compact_result():
        results = native_results()
        if mode == "crash_active": return not results and not observations("result") and not observations("idle")
        if len(results) != 1: return False
        raw = results[0]
        if not (raw["session_id"] == pair()[0] and raw["user_message_uuid"] == turn() and raw["user_message_uuids"] == [turn()] and
            raw["queued_turn_count"] == 0 and raw["is_error"] is False and raw["subtype"] == "success" and
            raw["num_turns"] == 0 and raw["local_command"] == "compact" and not raw.get("terminal_reason") and
            raw == json.loads(e.path(STATE + "fault-ready.json").read_text())): return False
        if mode == "lost_result": return not observations("result") and not observations("idle")
        result, idle = r["terminal"], r["idle"]
        expected = dict(kind="result", native_session_id=pair()[0], turn_id=turn(), result_ref=raw["uuid"],
            result_status="local_compact_completed" if good else "unknown")
        if good: expected["item_id"] = boundaries()[0]["uuid"]
        return (observations("result") == [result] and observations("idle") == [idle] and result["native_event"] == expected and
            result in events() and idle in events() and journal().index(result) < journal().index(idle))
    e.check("native_local_success_is_not_synthetic_completion", compact_result)
    def boundary_evidence():
        observed = observations("compaction_boundary")
        history = [v for v in lines(STATE + "history-after-fault.jsonl") if v.get("type") == "system" and v.get("subtype") == "compact_boundary"]
        if active: return not boundaries() and not observed and not history
        if len(boundaries()) != 1 or len(history) != 1: return False
        raw = boundaries()[0]
        if not (raw["session_id"] == pair()[0] and raw["compact_metadata"]["trigger"] == "manual" and history[0]["uuid"] == raw["uuid"]): return False
        if mode == "lost_boundary": return not observed
        return len(observed) == 1 and observed[0]["native_event"] == dict(kind="compaction_boundary", native_session_id=pair()[0], item_id=raw["uuid"])
    e.check("boundary_observation_and_native_history", boundary_evidence)
    e.check("loss_is_recorded_before_forwarding", lambda: frames("dropped_frame") ==
        (boundaries() if mode == "lost_boundary" else native_results() if mode == "lost_result" else []))
    if active:
        e.check("response_held_at_fault", lambda: r["provider_barrier"] is True and r["response_released_at_fault"] is False and
            not r["fault_script"]["results"] and r["fault_script"]["step_count"] == 0 and
            all(v["error_type"] == "connection_closed" for v in r.get("fault_provider_errors", [])))
    if mode == "interrupt_active":
        e.check("exact_interrupt_after_uncertain_delivery", lambda: bool(r["wrong_interrupt"]["error"]) and
            not r["interrupt"].get("error") and r["interrupt"] in journal() and r["interrupt"]["state"]["state"] == "unknown" and
            [v["request"] for v in frames("adapter_frame") if v.get("type") == "control_request" and v["request"].get("subtype") == "interrupt"] == [{"subtype":"interrupt"}])
    if not good:
        e.check("resume_refused_before_any_side_effect", lambda: "unfinished or uncertain" in r["restart"]["resume"]["error"] and
            r["refusal_exit"] != 0 and r["requests_before_resume"] == r["requests_after_resume"] and
            r["journal_before_resume"] == r["journal_after_refusal"] == digest(STATE + "identity.json.events") and
            r["wire_before_resume"] == r["wire_after_refusal"] == digest(STATE + "native-observations.jsonl") and
            e.rows(r["after_fault"], "SELECT * FROM sessions ORDER BY session_id") == e.rows(r["after_resume"], "SELECT * FROM sessions ORDER BY session_id"))
    else:
        e.check("completed_resume_positive_control", lambda: not r["restart"]["resume"].get("error") and r["restart"]["resume"] in journal() and
            r["restart"]["resume"]["identity"] == r["sessions"][ROLE]["identity"] and r["restart"]["resume"]["state"]["state"] ==
            r["restart"]["resume"]["pending_approval"]["state"] == "unknown")
        e.check("duplicate_wakes_and_old_approval_never_replay", lambda: r["requests_before_resume"] == r["requests_after_resume"] == r["requests_after_replay"] and
            r["duplicate"]["wake_delivery"] == r["wake"]["wake_delivery"] and r["duplicate"]["message_id"] == mid() and
            r["duplicate_original"]["wake_delivery"] == r["original"]["wake"]["wake_delivery"] and r["duplicate_original"]["message_id"] == original_mid() and
            bool(r["replay"].get("error")) and "approval_delivery" not in r["replay"] and not r["inspect"].get("approvals"))
        e.check("summary_used_after_resume", lambda: SUMMARY in e.path(STATE + "history-after-fault.jsonl").read_text() and
            all(v["has_compact_summary"] for v in json.loads(e.path(ROLE + "-provider.json").read_text())["requests"][r["requests_before_resume"]:]) and
            len(json.loads(e.path(ROLE + "-provider.json").read_text())["requests"]) > r["requests_before_resume"])
        e.check("fresh_gate_denial_and_allowed_control", lambda: e.path(ROLE + "/claude.txt").read_text() == "allowed\n" and
            e.path(ROLE + "/codex.txt").read_text() == "original\n" and e.path(ROLE + "/.fixture/fresh.txt").read_text() == "fresh" and
            "guard" in json.dumps(json.loads(r["fresh_output"])[0]) and json.loads(r["fresh_output"])[0]["is_error"] and
            not json.loads(r["fresh_output"])[1]["is_error"] and json.loads(r["fresh_output"])[2]["exit_code"] == 0)
        e.check("fresh_approval_and_native_terminal", lambda: any(v.get("approval_delivery", {}).get("state") == "submitted" and
            v["approval_response"]["turn_id"] == r["fresh_wake"]["wake_delivery"]["turn_id"] for v in journal()) and
            any(v.get("native_event", {}).get("kind") == "result" and v["native_event"].get("result_status") == "completed" and
                v["native_event"].get("turn_id") == r["fresh_wake"]["wake_delivery"]["turn_id"] for v in journal()))
        e.check("original_body_retrieved_explicitly", lambda: r["old_inbox"] == json.loads(e.path(ROLE + "/.fixture/old-inbox.json").read_text()) and
            r["old_delivered"] == json.loads(e.path(ROLE + "/.fixture/old-delivered.json").read_text()) and
            r["old_inbox"]["messages"]["count"] == 1 and r["old_inbox"]["messages"]["items"][0]["message_id"] == original_mid() and
            r["old_delivered"]["delivery"]["state"] == "delivered" and r["old_delivered"]["delivery"]["message_id"] == original_mid() and
            bool(e.rows(r["after_fresh"], "SELECT * FROM inbox_delivery WHERE msg_id=? AND session_id=?", (original_mid(), pair()[0]))))
    def launch_controls():
        launched = [v["args"] for v in wire() if v["type"] == "buddy_fixture_native_process" and "--print" in v["args"]]
        if len(launched) != (2 if good else 1) or launched[0][launched[0].index("--session-id")+1] != pair()[0]: return False
        if not all(v[v.index("--permission-mode")+1] == "manual" for v in launched): return False
        if not good: return True
        args = launched[1]
        if args[args.index("--resume")+1] != pair()[0] or any(v in args for v in ("--continue", "--session-id", "--fork-session")): return False
        last = max(i for i,v in enumerate(wire()) if v["type"] == "buddy_fixture_native_process" and "--resume" in v["args"])
        frames_after = [v["frame"] for v in wire()[last:] if v["type"] == "adapter_frame"]
        first = next(i for i,v in enumerate(frames_after) if v.get("type") == "user")
        return [v["request"]["subtype"] for v in frames_after[:first] if v.get("type") == "control_request"] == ["initialize", "get_settings", "reload_plugins", "get_settings"]
    e.check("native_launch_count_identity_and_controls", launch_controls)
    def exact_inputs():
        inputs = [v["uuid"] for v in frames("adapter_frame") if v.get("type") == "user"]
        answers = [v["response"]["request_id"] for v in frames("adapter_frame") if v.get("type") == "control_response"]
        expected = [r["original"]["wake"]["wake_delivery"]["turn_id"], turn()] + ([r["fresh_wake"]["wake_delivery"]["turn_id"]] if good else [])
        return inputs == expected and answers == [v["approval_response"]["id"] for v in journal() if v.get("approval_delivery", {}).get("state") == "submitted"]
    e.check("native_inputs_and_approval_answers_exact", exact_inputs)
    e.check("logical_identity_not_ended", lambda: all(e.one(r[stage], "SELECT incarnation,ended FROM sessions WHERE session_id=?", (pair()[0],)) ==
        {"incarnation":pair()[1], "ended":None} for stage in stages(r)))
    for name, query in (("claims", "SELECT claim_id,slug,session_id,incarnation,state,outcome FROM claims ORDER BY claim_id"),
        ("READY", "SELECT session_id,incarnation,decl,ready_sha,cleared FROM session_waits ORDER BY session_id"),
        ("READY_targets", "SELECT * FROM session_wait_targets ORDER BY decl,ord")):
        e.check(name + "_preserved", lambda query=query: bool(e.rows(r["before_fault"], query)) and all(
            e.rows(r["before_fault"], query) == e.rows(r[stage], query) for stage in stages(r)[1:]))
    e.check("READY_exact_commit", lambda: base.SHA.fullmatch(r["ready_sha"]) and
        base.git(e.path(r["sessions"][ROLE]["worktree"]), "rev-parse", "HEAD") == r["ready_sha"] and
        e.one(r["after_resume"], "SELECT ready_sha,cleared FROM session_waits WHERE session_id=?", (pair()[0],)) == dict(ready_sha=r["ready_sha"], cleared=None))
    e.check("queued_bodies_survive", lambda: all(not e.rows(r[stage], "SELECT * FROM inbox_delivery WHERE msg_id=? AND session_id=?", (msg, pair()[0]))
        for stage in ("before_fault", "after_fault", "after_resume") for msg in (mid(), original_mid())) and
        all(not e.rows(r[stage], "SELECT * FROM inbox_delivery WHERE msg_id=? AND session_id=?", (mid(), pair()[0])) for stage in stages(r)))
    e.check("work_completion_and_telemetry_remain_unknown", lambda: all(v["completion"]["state"] == v["acknowledgment"]["state"] == "unknown" and
        v["capabilities"]["telemetry"]["support"] == "unknown" for v in events()))
    e.check("shutdown_and_explicit_release", lambda: not r["cleanup_errors"] and not r.get("forced_cleanup_groups") and
        r["restart"]["before"]["exit"] == (-9 if mode in ("crash_active", "lost_boundary", "lost_result") else 0) and
        not r["restart"]["before"]["unparsed"] and r["restart"]["before"]["children"]["status"] == "observed_absent" and
        r["final_child_exit"]["status"] == r["sessions"][ROLE]["native_child_exit"]["status"] == "observed_absent" and
        r["sessions"][ROLE]["adapter_exit"] == (0 if good else 1) and not r["sessions"][ROLE]["unparsed"] and
        not e.rows(r["ledger"], "SELECT claim_id FROM claims WHERE state='open'") and
        not json.loads(e.path(ROLE + "-provider.json").read_text())["errors"])
    return dict(passed=all(e.checks.values()), checks=e.checks, errors=e.errors)


def validate(r):
    if r.get("schema") != "buddy.claude-interrupted-compaction-suite.v1" or set(r.get("cases", {})) != set(MODES):
        return dict(passed=False, error="require successful control and all four fault cases")
    cases = {mode: validate_case(r["cases"][mode]) for mode in MODES}
    # One run, one build: never five individually valid cases from different
    # runs (F-13; check-mixed-fleet.suite_binding says what each half binds).
    bound = base.suite_binding(r, MODES)
    return dict(passed=all(v["passed"] for v in cases.values()) and all(r["cases"][m]["mode"] == m for m in MODES) and
        bound["same_run"] and bound["same_contract"], cases=cases, **bound)


def self_test(r):
    if not validate(r)["passed"]: raise AssertionError("positive receipt failed")
    count = 0
    for mode in MODES:
        changes = [(("claude_version",), "2.1.292"), (("claude_version",), "2.1.289"),
            (("claude_version",), "2.1.291" if r["cases"][mode]["claude_version"] == "2.1.290" else "2.1.290"), (("provider",), "real"), (("adapter_sha256",), "wrong"),
            (("ready_sha",), "wrong"), (("cleanup_errors",), ["failed"]), (("final_child_exit", "status"), "unknown"),
            (("after_resume",), r["cases"][mode]["ledger"]), (("requests_after_resume",), -1),
            (("original", "output"), "[]"), (("restart", "before", "children", "status"), "unknown")]
        if mode != "crash_active": changes += [(("wake", "wake_delivery", "turn_id"), "wrong")]
        if mode in ("completed", "interrupt_active", "lost_boundary"):
            changes += [(("terminal", "native_event", "result_status"), "completed"), (("idle", "native_event", "turn_id"), "wrong")]
        if mode in ("interrupt_active", "crash_active"):
            changes += [(("provider_barrier",), False), (("response_released_at_fault",), True)]
        if mode == "interrupt_active": changes += [(("wrong_interrupt", "error"), ""), (("interrupt", "error"), "refused")]
        if mode != "completed": changes += [(("restart", "resume", "error"), ""), (("journal_after_refusal",), "changed"),
            (("wire_after_refusal",), "changed"), (("refusal_exit",), 0)]
        else: changes += [(("replay", "error"), ""), (("requests_after_replay",), -1), (("fresh_output",), "[]"),
            (("old_inbox", "messages", "count"), 0), (("duplicate", "wake_delivery", "turn_id"), "wrong"),
            (("restart", "resume", "identity", "incarnation"), "wrong")]
        for path, value in changes:
            bad = copy.deepcopy(r); target = bad["cases"][mode]
            for key in path[:-1]: target = target[key]
            target[path[-1]] = value
            if validate(bad)["passed"]: raise AssertionError("accepted damaged receipt: " + mode + "." + ".".join(path))
            count += 1
    for mode in MODES:
        bad = copy.deepcopy(r); del bad["cases"][mode]
        if validate(bad)["passed"]: raise AssertionError("accepted missing fault/control")
        count += 1
    with tempfile.TemporaryDirectory(prefix="buddy-suite-splice-") as scratch:
        count += base.suite_splice_self_test(r, MODES, validate, validate_case, scratch)
    with tempfile.TemporaryDirectory(prefix="buddy-evidence-corrupt-") as scratch:
        count += evidence_self_test(r, Path(scratch))
    return count


# EVIDENCE CORRUPTIONS (review F-14). Every mutation above edits a RECEIPT
# field. Measured: with five evidence predicates forced to return True, the
# self-test still exited 0 with 103 rejections, so it could not fail on the
# checks that read native evidence. These corrupt the evidence itself (the
# wire log, the journal, a ledger snapshot) in a private COPY of each case,
# never the native capture. Each corruption runs twice:
#   bytes   recorded hashes left alone: original_evidence_hashes must fail;
#   forged  the same change with its hash re-recorded, as a consistent forgery
#           or a runner bug would leave it: original_evidence_hashes must PASS
#           (positive control) and the NAMED semantic check must fail, so a
#           hash mismatch can never stand in for the guard it names.
# A byte-only control re-serializes rows without changing their meaning: the
# hash refuses it, and no semantic check is asked to.
def _rows(data):
    return [json.loads(v) for v in data.decode().splitlines()]


def _dump(rows, **kw):
    return "".join(json.dumps(v, **kw) + "\n" for v in rows).encode()


def _jsonl(edit):
    """A change (bytes, scratch) -> bytes that edits decoded JSON-lines rows."""
    def change(data, scratch):
        rows = _rows(data)
        edit(rows)
        return _dump(rows)
    return change


def _is(kind, **frame):
    return lambda v: v["type"] == kind and all(v["frame"].get(k) == x for k, x in frame.items())


def _drop(pred):
    def edit(rows):
        before = len(rows)
        rows[:] = [v for v in rows if not pred(v)]
        if len(rows) == before:
            raise AssertionError("corruption matched no row")
    return edit


def _dup(pred):
    def edit(rows):
        rows.append(copy.deepcopy(next(v for v in rows if pred(v))))
    return edit


def _sql(statement, args=()):
    def change(data, scratch):
        work = scratch / "snapshot.db"
        work.write_bytes(data)
        db = sqlite3.connect(work)
        try:
            if db.execute(statement, args).rowcount < 1:
                raise AssertionError("corruption matched no ledger row")
            db.commit()
        finally:
            db.close()
        return work.read_bytes()
    return change


def corruptions(c):
    """(file, change, named check, label) for one case."""
    mode = c["mode"]
    wire, journal = STATE + "native-observations.jsonl", STATE + "identity.json.events"
    sid = c["sessions"][ROLE]["identity"]["session_id"]
    def rearm(rows):
        hits = [v for v in rows if _is("fixture_frame", type="user")(v)]
        if not hits:
            raise AssertionError("corruption matched no row")
        for v in hits:
            v["frame"]["message"] = dict(v["frame"]["message"], content="/compact now")
    def forge_answer(rows):
        next(v for v in rows if v.get("approval_delivery", {}).get("state") == "submitted")["approval_response"]["id"] = "forged"
    def forge_drop(rows):
        rows.append({"type": "dropped_frame", "frame": next(v["frame"] for v in rows if v["type"] == "native_frame")})
    out = [
        (wire, _jsonl(rearm), "single_armed_compact_input_preserves_uuid", "wire: the armed /compact input rewritten"),
        (wire, _jsonl(_dup(_is("adapter_frame", type="user"))), "native_inputs_and_approval_answers_exact",
            "wire: an adapter input replayed"),
        (journal, _jsonl(forge_answer), "native_inputs_and_approval_answers_exact",
            "journal: an approval answer that was never sent"),
        (wire, _jsonl(_dup(lambda v: v["type"] == "buddy_fixture_native_process" and "--print" in v["args"])),
            "native_launch_count_identity_and_controls", "wire: an extra native launch"),
        (journal, _jsonl(lambda rows: rows[0]["identity"].update(incarnation="stale")), "captured_identity",
            "journal: a row from another incarnation"),
        (c["after_fault"], _sql("UPDATE sessions SET ended=1 WHERE session_id=?", (sid,)), "logical_identity_not_ended",
            "snapshot: the logical session ended"),
        (c["after_fault"], _sql("UPDATE claims SET state='released' WHERE claim_id=(SELECT MIN(claim_id) FROM claims)"),
            "claims_preserved", "snapshot: a claim released across the fault"),
    ]
    if mode in ("interrupt_active", "crash_active"):
        out.append((wire, _jsonl(lambda rows: rows.append({"type": "native_frame", "frame": {
            "type": "system", "subtype": "compact_boundary", "session_id": sid, "uuid": "forged",
            "compact_metadata": {"trigger": "manual"}}})),
            "boundary_observation_and_native_history", "wire: a boundary the interrupted turn never produced"))
    else:
        out.append((wire, _jsonl(_drop(_is("native_frame", type="system", subtype="compact_boundary"))),
            "boundary_observation_and_native_history", "wire: the native compact boundary removed"))
        out.append((STATE + "history-after-fault.jsonl", _jsonl(_drop(lambda v: v.get("type") == "system" and
            v.get("subtype") == "compact_boundary")), "boundary_observation_and_native_history",
            "history: the boundary removed from native history"))
    if mode in ("lost_boundary", "lost_result"):
        out.append((wire, _jsonl(_drop(lambda v: v["type"] == "dropped_frame")), "loss_is_recorded_before_forwarding",
            "wire: the drop record removed"))
    else:
        out.append((wire, _jsonl(forge_drop), "loss_is_recorded_before_forwarding", "wire: a drop the fault never made"))
    if mode == "completed":
        out.append((journal, _jsonl(_drop(lambda v: v.get("native_event", {}).get("kind") == "compaction_boundary")),
            "boundary_observation_and_native_history", "journal: the boundary observation removed"))
    elif mode == "lost_boundary":
        out.append((journal, _jsonl(lambda rows: rows.append(dict(copy.deepcopy(rows[-1]), native_event={
            "kind": "compaction_boundary", "native_session_id": sid, "item_id": "forged"}))),
            "boundary_observation_and_native_history", "journal: an observation of the boundary that was lost"))
    return out


def evidence_self_test(r, scratch):
    count = 0
    reserialize = lambda data, scratch: _dump(_rows(data), separators=(",", ":"))
    for mode in MODES:
        case = base.relocated_case(r["cases"][mode], scratch / "copy" / mode)
        root = Path(case["root"]).resolve()
        control = validate_case(case)
        if not control["passed"]:
            raise AssertionError("evidence control: the copied " + mode + " case failed before corruption: " +
                                 "; ".join(control["errors"]))
        plans = [(f, change, check, label) for f, change, check, label in corruptions(case)] + [
            (STATE + "native-observations.jsonl", reserialize, None, "wire: same rows, other bytes"),
            (STATE + "identity.json.events", reserialize, None, "journal: same rows, other bytes"),
            (case["before_fault"], lambda data, scratch: data + b"\0", None, "snapshot: one trailing byte")]
        for name, change, check, label in plans:
            target = (root / name).resolve()
            if root not in target.parents:
                raise AssertionError("corruption target outside the private copy: " + str(target))
            original = target.read_bytes()
            try:
                changed = change(original, scratch)
                if changed == original:
                    raise AssertionError("corruption changed nothing: " + mode + ": " + label)
                target.write_bytes(changed)
                for forged in ((False, True) if check else (False,)):
                    bad = copy.deepcopy(case)
                    if forged:
                        bad["evidence_sha256"][str(target.relative_to(root))] = hashlib.sha256(changed).hexdigest()
                    checks = validate_case(bad)["checks"]
                    if all(checks.values()):
                        raise AssertionError("accepted corrupted evidence: %s: %s%s" % (
                            mode, label, " (hash re-recorded)" if forged else ""))
                    if checks.get("original_evidence_hashes") is not forged:
                        raise AssertionError("evidence %s (%s): original_evidence_hashes=%r, want %r" % (
                            mode, label, checks.get("original_evidence_hashes"), forged))
                    if forged and checks.get(check) is not False:
                        raise AssertionError("forged evidence did not fail %s: %s: %s" % (check, mode, label))
                    count += 1
            finally:
                target.write_bytes(original)
        control = validate_case(case)
        if not control["passed"]:
            raise AssertionError("evidence control: the copied " + mode + " case did not restore: " +
                                 "; ".join(control["errors"]))
    return count


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("receipt", type=Path)
    p.add_argument("--self-test", action="store_true")
    args = p.parse_args()
    r = json.loads(args.receipt.read_text())
    verdict = validate(r)
    if args.self_test: verdict["mutations_rejected"] = self_test(r)
    print(json.dumps(verdict, indent=2))
    return 0 if verdict["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
