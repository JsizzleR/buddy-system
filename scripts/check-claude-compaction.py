#!/usr/bin/env python3
"""Validate manual Claude compaction and resume from original captured evidence.

This reads isolated immutable ledger copies, never the Buddy migrating store,
never launches native processes, and does not trust the fixture's passed flag.
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
spec = importlib.util.spec_from_file_location("claude_compaction_evidence", Path(__file__).with_name("check-mixed-fleet.py"))
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)
ROLE = "claude_worker"
STATE = ROLE + "-state/"


def evidence_paths(r):
    return [r[k] for k in ("before_compact", "before_resume", "after_resume", "after_fresh", "ledger")] + [
        STATE + p for p in ("identity.json", "identity.json.events", "native-observations.jsonl", "launch.json", "history-before-resume.jsonl", "history-after-resume.jsonl", "compact-taken.json")] + [
        ROLE + "-provider.json", ROLE + "/claude.txt", ROLE + "/codex.txt", ROLE + "/.fixture/original.txt", ROLE + "/.fixture/fresh.txt"]


def validate(r):
    e = base.Evidence(r)
    def lines(path): return [json.loads(v) for v in e.path(path).read_text().splitlines()]
    def journal(): return lines(STATE + "identity.json.events")
    def wire(): return lines(STATE + "native-observations.jsonl")
    def frames(kind): return [v["frame"] for v in wire() if v["type"] == kind]
    def events(): return r["restart"]["before"]["events"] + r["sessions"][ROLE]["events"]
    def pair(): return e.pair(ROLE)
    def mid(): return r["original"]["queue"]["delivery"]["message_id"]
    def turn(): return r["original"]["wake"]["wake_delivery"]["turn_id"]
    version = r.get("claude_version", "2.1.289")
    e.check("schema_provider", lambda: r["schema"] == "buddy.claude-compaction.v1" and r["provider"] == "deterministic-loopback" and
        version in ("2.1.290", "2.1.291") and set(r["sessions"]) == {ROLE} and
        r["sessions"][ROLE]["native_version"] == version + " (Claude Code)" and
        json.loads(e.path(STATE + "launch.json").read_text())["harness_version"] == version and
        json.loads(e.path(STATE + "launch.json").read_text())["hooks"]["mods"] == "builtin-only-" + version)
    e.check("artifact_hashes", lambda: all(hashlib.sha256(e.path(path).read_bytes()).hexdigest() == r[key]
        for path, key in (("source-manifest.json", "source_manifest_sha256"), ("bin/buddy", "buddy_sha256"), ("bin/buddy-adapter", "adapter_sha256"))))
    e.check("original_evidence_hashes", lambda: all(hashlib.sha256(e.path(path).read_bytes()).hexdigest() ==
        r["evidence_sha256"][str(e.path(path).relative_to(e.root))] for path in evidence_paths(r)))
    e.check("same_captured_identity", lambda: r["sessions"][ROLE]["identity"] == r["restart"]["resume"]["identity"] ==
        json.loads(e.path(STATE + "identity.json").read_text())["identity"] and
        all(v["identity"] == r["sessions"][ROLE]["identity"] for v in journal()))
    def native_argv():
        launches = [v["args"] for v in wire() if v["type"] == "buddy_fixture_native_process" and "--print" in v["args"]]
        return (len(launches) == 3 and launches[0][launches[0].index("--session-id")+1] == pair()[0] and
            all(a[a.index("--resume")+1] == pair()[0] and "--session-id" not in a and "--continue" not in a and
                "--fork-session" not in a and a[a.index("--permission-mode")+1] == "manual" for a in launches[1:]))
    e.check("exact_native_resume_argv", native_argv)
    def terminal(wake):
        tid = wake["wake_delivery"]["turn_id"]
        results = [v for v in journal() if v.get("native_event", {}).get("kind") == "result" and v["native_event"].get("turn_id") == tid]
        if len(results) != 1: return False
        result = results[0]; obs = result["native_event"]
        raw = [v for v in frames("native_frame") if v.get("type") == "result" and v.get("uuid") == obs["result_ref"]]
        idles = [v for v in journal() if v.get("native_event", {}).get("kind") == "idle" and v["native_event"].get("turn_id") == tid]
        return (len(raw) == len(idles) == 1 and result in events() and idles[0] in events() and
            raw[0]["session_id"] == pair()[0] and raw[0]["user_message_uuid"] == tid and raw[0]["user_message_uuids"] == [tid] and
            raw[0]["queued_turn_count"] == 0 and raw[0]["terminal_reason"] == obs["result_status"] == "completed" and
            raw[0]["subtype"] == "success" and not raw[0]["is_error"] and not obs.get("error") and not obs.get("err") and
            journal().index(result) < journal().index(idles[0]))
    e.check("original_native_completed_idle", lambda: terminal(r["original"]["wake"]))
    e.check("fresh_native_completed_idle", lambda: terminal(r["fresh_wake"]))
    e.check("resume_observation_unknown", lambda: r["restart"]["resume"]["state"]["state"] ==
        r["restart"]["resume"]["pending_approval"]["state"] == "unknown" and not r["restart"]["resume"].get("error") and
        r["restart"]["resume"] in journal())
    e.check("missing_history_refused_without_inference", lambda: bool(r["missing_history"].get("error")) and
        r["missing_history_exit"] != 0 and r["requests_at_close"] == r["requests_before"])
    e.check("transport_exit_is_not_logical_end", lambda: r["restart"]["before"]["exit"] == 0 and not r["restart"]["before"]["unparsed"] and
        r["restart"]["before"]["children"]["status"] == "observed_absent" and
        all(e.one(r[stage], "SELECT incarnation,ended FROM sessions WHERE session_id=?", (pair()[0],)) ==
            {"incarnation": pair()[1], "ended": None} for stage in ("before_resume", "after_resume", "after_fresh")))
    for name, query in (("claims", "SELECT claim_id,slug,session_id,incarnation,state,outcome FROM claims ORDER BY claim_id"),
        ("READY", "SELECT session_id,incarnation,decl,ready_sha,cleared FROM session_waits ORDER BY session_id"),
        ("READY_targets", "SELECT * FROM session_wait_targets ORDER BY decl,ord")):
        e.check(name + "_preserved", lambda query=query: bool(e.rows(r["before_resume"], query)) and
            all(e.rows(r["before_resume"], query) == e.rows(r[stage], query) for stage in ("before_compact", "after_resume", "after_fresh")))
    e.check("READY_exact_commit", lambda: base.SHA.fullmatch(r["ready_sha"]) and
        e.one(r["after_resume"], "SELECT ready_sha,cleared FROM session_waits WHERE session_id=?", (pair()[0],)) ==
        {"ready_sha": r["ready_sha"], "cleared": None})
    e.check("queued_body_requires_explicit_retrieval", lambda: all(not e.rows(r[stage],
        "SELECT * FROM inbox_delivery WHERE msg_id=? AND session_id=?", (mid(), pair()[0])) for stage in ("before_resume", "after_resume")) and
        r["old_inbox"]["messages"]["count"] == 1 and r["old_inbox"]["messages"]["items"][0]["message_id"] == mid() and
        r["old_delivered"]["delivery"]["message_id"] == mid() and r["old_delivered"]["delivery"]["state"] == "delivered" and
        bool(e.rows(r["after_fresh"], "SELECT * FROM inbox_delivery WHERE msg_id=? AND session_id=?", (mid(), pair()[0]))))
    def no_replay():
        inputs = [v for v in frames("adapter_frame") if v.get("type") == "user"]
        answers = [v for v in frames("adapter_frame") if v.get("type") == "control_response"]
        approvals = [v for v in events() if v.get("approval_delivery", {}).get("state") == "submitted"]
        return (len(inputs) == 3 and [v["uuid"] for v in inputs] == [turn(), r["compaction"]["wake"]["wake_delivery"]["turn_id"], r["fresh_wake"]["wake_delivery"]["turn_id"]] and
            len(answers) == len(approvals) and all(any(a["response"]["request_id"] == v["approval_response"]["id"] for a in answers) for v in approvals) and
            r["requests_before"] == r["requests_after"] and r["duplicate"]["wake_delivery"] == r["original"]["wake"]["wake_delivery"] and
            r["duplicate"]["message_id"] == mid() and r["duplicate_compact"]["wake_delivery"] == r["compaction"]["wake"]["wake_delivery"] and bool(r["replay"].get("error")) and "approval_delivery" not in r["replay"])
    e.check("no_wake_or_approval_replay", no_replay)
    e.check("fresh_approval_is_possible", lambda: any(v.get("approval_delivery", {}).get("state") == "submitted" and
        v["approval_response"]["turn_id"] == r["fresh_wake"]["wake_delivery"]["turn_id"] for v in events()))
    e.check("resumed_gate_denial_and_control", lambda: e.path(ROLE + "/claude.txt").read_text() == "allowed\n" and
        e.path(ROLE + "/codex.txt").read_text() == "original\n" and e.path(ROLE + "/.fixture/fresh.txt").read_text() == "fresh" and
        e.path(ROLE + "/.fixture/original.txt").read_text() == "once" and
        "guard" in json.dumps(json.loads(r["fresh_output"])[0]) and json.loads(r["fresh_output"])[0]["is_error"] and
        not json.loads(r["fresh_output"])[1]["is_error"] and json.loads(r["fresh_output"])[2]["exit_code"] == 0)
    def compact_terminal():
        c = r["compaction"]; tid = c["wake"]["wake_delivery"]["turn_id"]
        obs = c["boundary"]["native_event"]; result = c["result"]["native_event"]
        raw = [v for v in frames("native_frame") if v.get("type") == "result" and v.get("uuid") == result["result_ref"]]
        boundaries = [v for v in frames("native_frame") if v.get("type") == "system" and v.get("subtype") == "compact_boundary"]
        return (len(raw) == len(boundaries) == 1 and boundaries[0]["session_id"] == pair()[0] and
            boundaries[0]["compact_metadata"]["trigger"] == "manual" and obs == {"kind":"compaction_boundary",
                "native_session_id":pair()[0], "item_id":boundaries[0]["uuid"]} and
            result == {"kind":"result", "native_session_id":pair()[0], "turn_id":tid,
                "item_id":obs["item_id"], "result_ref":raw[0]["uuid"], "result_status":"local_compact_completed"} and
            raw[0]["session_id"] == pair()[0] and raw[0]["user_message_uuid"] == tid and raw[0]["user_message_uuids"] == [tid] and
            raw[0]["queued_turn_count"] == 0 and raw[0]["subtype"] == "success" and raw[0]["is_error"] is False and
            raw[0]["num_turns"] == 0 and raw[0]["local_command"] == "compact" and not raw[0].get("terminal_reason") and
            c["idle"]["native_event"] == {"kind":"idle", "native_session_id":pair()[0], "turn_id":tid} and
            all(c[k] in journal() and c[k] in events() for k in ("boundary", "result", "idle")) and
            journal().index(c["boundary"]) < journal().index(c["result"]) < journal().index(c["idle"]) and
            frames("native_frame").index(boundaries[0]) < frames("native_frame").index(raw[0]))
    e.check("compact_boundary_and_separate_exact_terminal", compact_terminal)
    def proxy_scope():
        injected = frames("fixture_frame")
        tid = r["compaction"]["wake"]["wake_delivery"]["turn_id"]
        original = [v for v in frames("adapter_frame") if v.get("uuid") == tid]
        taken = json.loads(e.path(STATE + "compact-taken.json").read_text())
        if len(injected) != 1 or len(original) != 1: return False
        expected = copy.deepcopy(original[0]); expected["message"]["content"] = "/compact"
        return (injected[0] == expected and expected["session_id"] == pair()[0] and
            taken == {"message_id":r["compaction"]["queue"]["delivery"]["message_id"]} and
            "message #" + str(taken["message_id"]) + " is queued." in original[0]["message"]["content"])
    e.check("only_explicit_fixture_input_substituted", proxy_scope)
    def preserved_history():
        summary = "BUDDY_COMPACT_HISTORY_290"
        boundary = r["compaction"]["boundary"]["native_event"]["item_id"]
        for name in ("history-before-resume.jsonl", "history-after-resume.jsonl"):
            rows = lines(STATE + name)
            hits = [v for v in rows if v.get("type") == "system" and v.get("subtype") == "compact_boundary"]
            summaries = [v for v in rows if v.get("isCompactSummary") and summary in json.dumps(v.get("message", {}))]
            if len(hits) != 1 or hits[0]["uuid"] != boundary or hits[0]["sessionId"] != pair()[0] or len(summaries) != 1:
                return False
            if summaries[0]["parentUuid"] != boundary or summaries[0]["sessionId"] != pair()[0]: return False
        requests = json.loads(e.path(ROLE + "-provider.json").read_text())["requests"]
        return (r["requests_at_close"] > 0 and requests[r["requests_at_close"]]["has_compact_summary"] is True and
            all(v["has_compact_summary"] for v in requests[r["requests_at_close"]:]) and
            r["compaction"]["script"]["final_emitted"] is True and r["compaction"]["script"]["step_count"] == 0)
    e.check("native_summary_preserved_and_used_after_resume", preserved_history)
    e.check("compact_body_stays_queued", lambda: all(not e.rows(r[stage],
        "SELECT * FROM inbox_delivery WHERE msg_id=? AND session_id=?", (r["compaction"]["queue"]["delivery"]["message_id"], pair()[0]))
        for stage in ("before_resume", "after_resume", "after_fresh")))
    e.check("compaction_stays_an_observation", lambda: r["compaction"]["boundary"]["completion"]["state"] ==
        r["compaction"]["boundary"]["body_delivery"]["state"] == "unknown" and
        r["compaction"]["boundary"]["capabilities"]["compaction_observation"]["support"] == "experimental")
    def renewed_controls():
        trace = wire()
        last = max(i for i, v in enumerate(trace) if v["type"] == "buddy_fixture_native_process" and "--resume" in v["args"])
        rows = [v["frame"] for v in trace[last:] if v["type"] == "adapter_frame"]
        first_input = next(i for i, v in enumerate(rows) if v.get("type") == "user")
        controls = [v["request"]["subtype"] for v in rows[:first_input] if v.get("type") == "control_request"]
        return controls == ["initialize", "get_settings", "reload_plugins", "get_settings"]
    e.check("resume_rechecks_native_settings_and_mods", renewed_controls)
    e.check("unknown_completion_and_telemetry", lambda: all(v["completion"]["state"] == v["acknowledgment"]["state"] == "unknown" and
        v["capabilities"]["telemetry"]["support"] == "unknown" for v in events()))
    e.check("explicit_release_and_cleanup", lambda: not r["cleanup_errors"] and r["sessions"][ROLE]["adapter_exit"] == 0 and
        not r["sessions"][ROLE]["unparsed"] and r["sessions"][ROLE]["native_child_exit"]["status"] == "observed_absent" and
        not e.rows(r["ledger"], "SELECT claim_id FROM claims WHERE state='open'") and
        not json.loads(e.path(ROLE + "-provider.json").read_text())["errors"])
    return {"passed": all(e.checks.values()), "checks": e.checks, "errors": e.errors}


def self_test(r):
    if not validate(r)["passed"]: raise AssertionError("positive receipt failed")
    changes = [(("claude_version",), "2.1.292"), (("claude_version",), "2.1.289"),
        (("claude_version",), "2.1.291" if r["claude_version"] == "2.1.290" else "2.1.290"),
        (("provider",), "real"), (("adapter_sha256",), "wrong"), (("cleanup_errors",), ["failed"]),
        (("ready_sha",), "wrong"), (("requests_after",), -1), (("requests_at_close",), -1),
        (("duplicate", "wake_delivery", "turn_id"), "wrong"), (("replay", "error"), ""),
        (("old_inbox", "messages", "count"), 0), (("after_resume",), r["ledger"]), (("after_fresh",), r["ledger"]),
        (("missing_history", "error"), ""), (("missing_history_exit",), 0),
        (("restart", "resume", "identity", "incarnation"), "wrong"), (("restart", "resume", "state", "state"), "idle"),
        (("restart", "before", "children", "status"), "unknown"), (("fresh_wake", "wake_delivery", "turn_id"), "wrong"),
        (("fresh_output",), "[]"), (("original", "wake", "wake_delivery", "turn_id"), "wrong"),
        (("compaction", "boundary", "native_event", "item_id"), "wrong"),
        (("compaction", "boundary", "native_event", "turn_id"), "invented"),
        (("compaction", "result", "native_event", "result_status"), "completed"),
        (("compaction", "result", "native_event", "item_id"), "wrong"),
        (("compaction", "result", "native_event", "turn_id"), "wrong"),
        (("compaction", "idle", "native_event", "turn_id"), "wrong"),
        (("compaction", "script", "final_emitted"), False),
        (("duplicate_compact", "wake_delivery", "turn_id"), "wrong")]
    for path, value in changes:
        bad = copy.deepcopy(r); target = bad
        for key in path[:-1]: target = target[key]
        target[path[-1]] = value
        if validate(bad)["passed"]: raise AssertionError("accepted damaged receipt: " + ".".join(path))
    return {"positive": "passed", "rejected_mutations": len(changes)}


def synthetic(root):
    """A coherent SYNTHETIC compaction capture under root (base.Synthetic says what that is)."""
    s = base.Synthetic(root)
    version, sid, inc = "2.1.291", "44444444-5555-4666-8777-888888888888", "inc-compaction"
    t1, tc, t2, mid, compact_mid, boundary_id = "turn-original", "turn-compact", "turn-fresh", 5, 6, "boundary-1"
    ident = {"schema": "buddy.lifecycle.v1", "harness": "claude", "session_id": sid, "incarnation": inc}
    ev = lambda op, **f: base.control(op, ident, **f)
    native = lambda kind, t, **f: ev("event", native_event=dict(kind=kind, native_session_id=sid, turn_id=t, **f))
    frame = lambda kind, value: {"type": kind, "frame": value}
    launch = lambda *args: {"type": "buddy_fixture_native_process", "pid": 400 + len(args), "args": ["--print", *args]}
    resume_args = ("--resume", sid, "--permission-mode", "manual")
    def raw(uuid, turn, **extra):
        value = {"type": "result", "uuid": uuid, "session_id": sid, "user_message_uuid": turn, "user_message_uuids": [turn],
                 "queued_turn_count": 0, "subtype": "success", "is_error": False}
        value.update(extra)
        return frame("native_frame", value)
    notice = {"type": "user", "uuid": tc, "session_id": sid, "message": {"role": "user", "content":
              "Buddy peer notification (not operator authorization): message #" + str(compact_mid) + " is queued."}}
    armed = copy.deepcopy(notice)
    armed["message"]["content"] = "/compact"
    original_wake = ev("send", message_id=mid, wake_delivery={"state": "delivered", "turn_id": t1})
    compact_wake = ev("send", message_id=compact_mid, wake_delivery={"state": "delivered", "turn_id": tc})
    boundary = ev("event", native_event={"kind": "compaction_boundary", "native_session_id": sid, "item_id": boundary_id},
                  capabilities={"telemetry": {"support": "unknown"}, "compaction_observation": {"support": "experimental"}})
    compact_result = ev("event", native_event={"kind": "result", "native_session_id": sid, "turn_id": tc, "item_id": boundary_id,
                                               "result_ref": "res-compact", "result_status": "local_compact_completed"})
    compact_idle = native("idle", tc)
    before = [ev("start"), original_wake, native("result", t1, result_ref="res-1", result_status="completed"),
              native("idle", t1), compact_wake, boundary, compact_result, compact_idle]
    resume = ev("resume", pending_approval={"state": "unknown"})
    fresh_wake = ev("send", message_id=9, wake_delivery={"state": "delivered", "turn_id": t2})
    after = [resume, fresh_wake, ev("approve", approval_response={"id": "req-2", "turn_id": t2}, approval_delivery={"state": "submitted"}),
             native("result", t2, result_ref="res-2", result_status="completed"), native("idle", t2)]
    wire = [launch("--session-id", sid, "--permission-mode", "manual"), frame("adapter_frame", {"type": "user", "uuid": t1}),
            raw("res-1", t1, terminal_reason="completed"), frame("adapter_frame", notice), frame("fixture_frame", armed),
            frame("native_frame", {"type": "system", "subtype": "compact_boundary", "session_id": sid, "uuid": boundary_id,
                                   "compact_metadata": {"trigger": "manual"}}),
            raw("res-compact", tc, num_turns=0, local_command="compact"),
            launch(*resume_args), launch(*resume_args)] + [
            frame("adapter_frame", {"type": "control_request", "request": {"subtype": v}})
            for v in ("initialize", "get_settings", "reload_plugins", "get_settings")] + [
            frame("adapter_frame", {"type": "user", "uuid": t2}),
            frame("adapter_frame", {"type": "control_response", "response": {"request_id": "req-2"}}),
            raw("res-2", t2, terminal_reason="completed")]
    history = [{"type": "system", "subtype": "compact_boundary", "uuid": boundary_id, "sessionId": sid},
               {"type": "user", "isCompactSummary": True, "parentUuid": boundary_id, "sessionId": sid,
                "message": {"content": "Summary BUDDY_COMPACT_HISTORY_290: the original marker ran once."}}]
    s.jsonl(STATE + "identity.json.events", before + after)
    s.jsonl(STATE + "native-observations.jsonl", wire)
    s.jsonl(STATE + "history-before-resume.jsonl", history)
    s.jsonl(STATE + "history-after-resume.jsonl", history)
    s.write(STATE + "compact-taken.json", {"message_id": compact_mid})
    s.write(STATE + "identity.json", {"identity": ident})
    s.write(STATE + "launch.json", {"harness_version": version, "hooks": {"mods": "builtin-only-" + version}})
    s.write(ROLE + "-provider.json", {"errors": [], "requests": [{"has_compact_summary": False}, {"has_compact_summary": False},
                                                                 {"has_compact_summary": True}, {"has_compact_summary": True}]})
    ready = s.repo(ROLE)
    for name, text in (("claude.txt", "allowed\n"), ("codex.txt", "original\n"), (".fixture/original.txt", "once"),
                       (".fixture/fresh.txt", "fresh")):
        s.write(ROLE + "/" + name, text)
    claims = [("c1", "run-compact", "owner", "owner-inc", "open", ""), ("c2", "worker", sid, inc, "open", "")]
    snap = lambda name, **kw: s.ledger(name, sessions=[(sid, inc, None)], waits=[(sid, inc, "d1", ready, None)],
                                       targets=[("d1", sid, inc, 0, "c1", "run-compact")], **kw)
    r = {"schema": "buddy.claude-compaction.v1", "root": str(s.root), "provider": "deterministic-loopback",
         "claude_version": version, "ready_sha": ready, "cleanup_errors": [],
         "original": {"queue": {"delivery": {"message_id": mid}}, "wake": original_wake},
         "compaction": {"queue": {"delivery": {"message_id": compact_mid}}, "wake": compact_wake, "boundary": boundary,
                        "result": compact_result, "idle": compact_idle, "script": {"final_emitted": True, "step_count": 0}},
         "restart": {"resume": resume, "before": {"exit": 0, "unparsed": [], "events": before,
                                                  "children": {"status": "observed_absent"}}},
         "fresh_wake": fresh_wake, "missing_history": {"error": "no native history"}, "missing_history_exit": 1,
         "requests_before": 2, "requests_after": 2, "requests_at_close": 2,
         "duplicate": {"message_id": mid, "wake_delivery": original_wake["wake_delivery"]},
         "duplicate_compact": {"message_id": compact_mid, "wake_delivery": compact_wake["wake_delivery"]},
         "replay": {"error": "approval already answered"},
         "old_inbox": {"messages": {"count": 1, "items": [{"message_id": mid}]}},
         "old_delivered": {"delivery": {"message_id": mid, "state": "delivered"}},
         "fresh_output": json.dumps([{"output": "guard: claude.txt is inside scope", "is_error": True},
                                     {"is_error": False}, {"exit_code": 0, "is_error": False}])}
    for stage in ("before_compact", "before_resume", "after_resume", "after_fresh"):
        r[stage] = snap(stage, claims=claims, delivered=[(mid, sid, 1)] if stage == "after_fresh" else [])
    r["ledger"] = snap("final", claims=[c[:4] + ("released", "pass") for c in claims])
    r["sessions"] = {ROLE: {"identity": ident, "worktree": str(s.root / ROLE), "native_version": version + " (Claude Code)",
                            "events": after, "adapter_exit": 0, "unparsed": [], "native_child_exit": {"status": "observed_absent"}}}
    s.built(r)
    return s.seal(r, evidence_paths(r))


def synthetic_self_test():
    """The receipt mutations above, evidence forgeries and the hash binding, against synthetic evidence."""
    with tempfile.TemporaryDirectory(prefix="buddy-claude-compaction-synthetic-") as tmp:
        r = synthetic(Path(tmp) / "case")
        out = self_test(r)
        wire, journal = STATE + "native-observations.jsonl", STATE + "identity.json.events"
        boundary = lambda v: v["type"] == "native_frame" and v["frame"].get("subtype") == "compact_boundary"
        plans = [(wire, lambda rows: rows.append(copy.deepcopy(next(v for v in rows if boundary(v)))),
                  "compact_boundary_and_separate_exact_terminal"),
                 (wire, lambda rows: rows.append(copy.deepcopy(next(v for v in rows if v["type"] == "fixture_frame"))),
                  "only_explicit_fixture_input_substituted"),
                 (wire, lambda rows: rows.insert(1, copy.deepcopy(rows[0])), "exact_native_resume_argv"),
                 (STATE + "history-after-resume.jsonl", lambda rows: rows.pop(), "native_summary_preserved_and_used_after_resume"),
                 (journal, lambda rows: rows[0]["identity"].update(incarnation="stale"), "same_captured_identity")]
        with tempfile.TemporaryDirectory(prefix="buddy-forged-") as scratch:
            out["rejected_mutations"] += base.forged_jsonl_self_test(r, validate, plans, scratch)
        with tempfile.TemporaryDirectory(prefix="buddy-hash-binding-") as scratch:
            out["rejected_mutations"] += base.hash_binding_self_test(r, validate, evidence_paths, scratch)
        out["evidence"] = "synthetic: tests the checker, qualifies no native harness"
        return out


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("receipt", type=Path, nargs="?")
    parser.add_argument("--self-test", action="store_true",
                        help="with no receipt, run against synthetic evidence built in a temp dir (hermetic)")
    args = parser.parse_args()
    if args.receipt is None:
        if not args.self_test:
            parser.error("a receipt is required unless --self-test")
        print(json.dumps(synthetic_self_test(), indent=2))
        return 0
    r = json.loads(args.receipt.read_text())
    out = self_test(r) if args.self_test else validate(r)
    print(json.dumps(out, indent=2))
    return 0 if args.self_test or out["passed"] else 1


if __name__ == "__main__": sys.exit(main())
