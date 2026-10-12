#!/usr/bin/env python3
"""Validate Claude resume against original wire, journal and ledger captures.

This reads isolated immutable ledger copies, never the Buddy migrating store,
never launches native processes, and does not trust the fixture's passed flag.
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
spec = importlib.util.spec_from_file_location("claude_resume_evidence", Path(__file__).with_name("check-mixed-fleet.py"))
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)
ROLE = "claude_worker"
STATE = ROLE + "-state/"


def evidence_paths(r):
    return [r[k] for k in ("before_resume", "after_resume", "after_fresh", "ledger")] + [
        STATE + p for p in ("identity.json", "identity.json.events", "native-observations.jsonl", "launch.json")] + [
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
    e.check("schema_provider", lambda: r["schema"] == "buddy.claude-resume.v1" and r["provider"] == "deterministic-loopback" and
        version in ("2.1.289", "2.1.290", "2.1.291") and set(r["sessions"]) == {ROLE} and
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
            all(e.rows(r["before_resume"], query) == e.rows(r[stage], query) for stage in ("after_resume", "after_fresh")))
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
        return (len(inputs) == 2 and [v["uuid"] for v in inputs] == [turn(), r["fresh_wake"]["wake_delivery"]["turn_id"]] and
            len(answers) == len(approvals) and all(any(a["response"]["request_id"] == v["approval_response"]["id"] for a in answers) for v in approvals) and
            r["requests_before"] == r["requests_after"] and r["duplicate"]["wake_delivery"] == r["original"]["wake"]["wake_delivery"] and
            r["duplicate"]["message_id"] == mid() and bool(r["replay"].get("error")) and "approval_delivery" not in r["replay"])
    e.check("no_wake_or_approval_replay", no_replay)
    e.check("fresh_approval_is_possible", lambda: any(v.get("approval_delivery", {}).get("state") == "submitted" and
        v["approval_response"]["turn_id"] == r["fresh_wake"]["wake_delivery"]["turn_id"] for v in events()))
    e.check("resumed_gate_denial_and_control", lambda: e.path(ROLE + "/claude.txt").read_text() == "allowed\n" and
        e.path(ROLE + "/codex.txt").read_text() == "original\n" and e.path(ROLE + "/.fixture/fresh.txt").read_text() == "fresh" and
        e.path(ROLE + "/.fixture/original.txt").read_text() == "once" and
        "guard" in json.dumps(json.loads(r["fresh_output"])[0]) and json.loads(r["fresh_output"])[0]["is_error"] and
        not json.loads(r["fresh_output"])[1]["is_error"] and json.loads(r["fresh_output"])[2]["exit_code"] == 0)
    def preserved_history():
        requests = json.loads(e.path(ROLE + "-provider.json").read_text())["requests"]
        boundary = r["requests_at_close"]
        old = requests[boundary-1]["tool_outputs"]
        return (boundary > 0 and bool(old) and requests[boundary]["input_fingerprint"] != requests[boundary-1]["input_fingerprint"] and
            all(v in requests[boundary]["tool_outputs"] for v in old))
    e.check("native_history_in_fresh_provider_request", preserved_history)
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
    changes = [(("claude_version",), "2.1.292"), (("claude_version",), "2.1.290" if r.get("claude_version", "2.1.289") == "2.1.289" else "2.1.289"),
        (("provider",), "real"), (("adapter_sha256",), "wrong"), (("cleanup_errors",), ["failed"]),
        (("ready_sha",), "wrong"), (("requests_after",), -1), (("requests_at_close",), -1),
        (("duplicate", "wake_delivery", "turn_id"), "wrong"), (("replay", "error"), ""),
        (("old_inbox", "messages", "count"), 0), (("after_resume",), r["ledger"]), (("after_fresh",), r["ledger"]),
        (("missing_history", "error"), ""), (("missing_history_exit",), 0),
        (("restart", "resume", "identity", "incarnation"), "wrong"), (("restart", "resume", "state", "state"), "idle"),
        (("restart", "before", "children", "status"), "unknown"), (("fresh_wake", "wake_delivery", "turn_id"), "wrong"),
        (("fresh_output",), "[]"), (("original", "wake", "wake_delivery", "turn_id"), "wrong")]
    for path, value in changes:
        bad = copy.deepcopy(r); target = bad
        for key in path[:-1]: target = target[key]
        target[path[-1]] = value
        if validate(bad)["passed"]: raise AssertionError("accepted damaged receipt: " + ".".join(path))
    return {"positive": "passed", "rejected_mutations": len(changes)}


def synthetic(root):
    """Build a coherent SYNTHETIC resume capture under root; return its receipt.

    Review F-33: this self-test ran only against a native receipt retained in
    /private/tmp, so a fresh clone could not run it and check.sh could not gate
    it. These are real files the checker reads the same way: a JSON-lines wire
    log and journal, SQLite ledger snapshots, worktree files and a provider
    log, hashed like a runner hashes them. They test the CHECKER. They qualify
    no native harness, and a pass here says nothing about Claude Code.
    """
    sid, inc, version = "11111111-2222-4333-8444-555555555555", "synthetic-incarnation", "2.1.291"
    t1, t2, mid = "turn-original", "turn-fresh", 1
    ident = {"schema": "buddy.lifecycle.v1", "harness": "claude", "session_id": sid, "incarnation": inc}
    def event(op, **fields):
        return dict({"schema": "buddy.control.v1", "operation": op, "identity": ident, "state": {"state": "unknown"},
                     "completion": {"state": "unknown"}, "acknowledgment": {"state": "unknown"},
                     "capabilities": {"telemetry": {"support": "unknown"}}}, **fields)
    def native(kind, turn, ref=None):
        return event("event", native_event=dict({"kind": kind, "turn_id": turn, "native_session_id": sid},
                     **({"result_ref": ref, "result_status": "completed"} if ref else {})))
    start, result1, idle1 = event("start"), native("result", t1, "res-1"), native("idle", t1)
    resume = event("resume", pending_approval={"state": "unknown"})
    approval = event("approve", approval_delivery={"state": "submitted"}, approval_response={"id": "req-2", "turn_id": t2})
    result2, idle2 = native("result", t2, "res-2"), native("idle", t2)
    wake = event("send", message_id=mid, wake_delivery={"state": "delivered", "turn_id": t1})
    def frame(kind, value):
        return {"type": kind, "frame": value}
    def raw_result(uuid, turn):
        return frame("native_frame", {"type": "result", "uuid": uuid, "session_id": sid, "user_message_uuid": turn,
            "user_message_uuids": [turn], "queued_turn_count": 0, "terminal_reason": "completed",
            "subtype": "success", "is_error": False})
    resume_args = ["--print", "--resume", sid, "--permission-mode", "manual"]
    wire = [{"type": "buddy_fixture_native_process", "pid": 101, "args": ["--print", "--session-id", sid, "--permission-mode", "manual"]},
            frame("adapter_frame", {"type": "user", "uuid": t1}), raw_result("res-1", t1),
            {"type": "buddy_fixture_native_process", "pid": 102, "args": resume_args},
            {"type": "buddy_fixture_native_process", "pid": 103, "args": resume_args}] + [
            frame("adapter_frame", {"type": "control_request", "request": {"subtype": s}})
            for s in ("initialize", "get_settings", "reload_plugins", "get_settings")] + [
            frame("adapter_frame", {"type": "user", "uuid": t2}),
            frame("adapter_frame", {"type": "control_response", "response": {"request_id": "req-2"}}),
            raw_result("res-2", t2)]
    files = {
        STATE + "identity.json": json.dumps({"identity": ident}),
        STATE + "identity.json.events": "".join(json.dumps(v) + "\n" for v in (result1, idle1, resume, approval, result2, idle2)),
        STATE + "native-observations.jsonl": "".join(json.dumps(v) + "\n" for v in wire),
        STATE + "launch.json": json.dumps({"harness_version": version, "hooks": {"mods": "builtin-only-" + version}}),
        ROLE + "-provider.json": json.dumps({"errors": [], "requests": [
            {"input_fingerprint": "a", "tool_outputs": []}, {"input_fingerprint": "b", "tool_outputs": ["old"]},
            {"input_fingerprint": "c", "tool_outputs": ["old", "fresh"]}]}),
        ROLE + "/claude.txt": "allowed\n", ROLE + "/codex.txt": "original\n",
        ROLE + "/.fixture/original.txt": "once", ROLE + "/.fixture/fresh.txt": "fresh",
        "source-manifest.json": "{}", "bin/buddy": "synthetic buddy", "bin/buddy-adapter": "synthetic adapter"}
    for name, text in files.items():
        (root / name).parent.mkdir(parents=True, exist_ok=True)
        (root / name).write_text(text)
    ready = "a" * 40
    def ledger(name, released=False, delivered=False):
        path = root / (name + ".db")
        db = sqlite3.connect(path)
        db.executescript("""CREATE TABLE sessions(session_id, incarnation, ended);
            CREATE TABLE claims(claim_id, slug, session_id, incarnation, state, outcome);
            CREATE TABLE session_waits(session_id, incarnation, decl, ready_sha, cleared);
            CREATE TABLE session_wait_targets(decl, session_id, incarnation, ord, claim_id, slug);
            CREATE TABLE inbox_delivery(msg_id, session_id, delivered);""")
        db.execute("INSERT INTO sessions VALUES (?,?,NULL)", (sid, inc))
        db.execute("INSERT INTO claims VALUES ('c1','run-resume','owner','owner-inc',?,?)",
                   ("released", "pass") if released else ("open", ""))
        db.execute("INSERT INTO session_waits VALUES (?,?,'d1',?,NULL)", (sid, inc, ready))
        db.execute("INSERT INTO session_wait_targets VALUES ('d1',?,?,0,'c1','run-resume')", (sid, inc))
        if delivered:
            db.execute("INSERT INTO inbox_delivery VALUES (?,?,1)", (mid, sid))
        db.commit()
        db.close()
        return str(path)
    r = {"schema": "buddy.claude-resume.v1", "provider": "deterministic-loopback", "claude_version": version,
         "root": str(root), "ready_sha": ready, "cleanup_errors": [],
         "before_resume": ledger("before-resume"), "after_resume": ledger("after-resume"),
         "after_fresh": ledger("after-fresh", delivered=True), "ledger": ledger("final", released=True, delivered=True),
         "sessions": {ROLE: {"identity": ident, "worktree": str(root / ROLE), "native_version": version + " (Claude Code)",
             "events": [resume, approval, result2, idle2], "adapter_exit": 0, "unparsed": [],
             "native_child_exit": {"status": "observed_absent"}}},
         "restart": {"before": {"events": [start, result1, idle1], "exit": 0, "unparsed": [],
                                "children": {"status": "observed_absent"}}, "resume": resume},
         "original": {"queue": {"delivery": {"message_id": mid}}, "wake": wake},
         "fresh_wake": {"wake_delivery": {"state": "delivered", "turn_id": t2}},
         "duplicate": {"message_id": mid, "wake_delivery": wake["wake_delivery"]},
         "replay": {"error": "approval already answered"}, "missing_history": {"error": "no native history"},
         "missing_history_exit": 1, "requests_before": 2, "requests_after": 2, "requests_at_close": 2,
         "old_inbox": {"messages": {"count": 1, "items": [{"message_id": mid}]}},
         "old_delivered": {"delivery": {"message_id": mid, "state": "delivered"}},
         "fresh_output": json.dumps([{"output": "guard: claimed scope", "is_error": True}, {"is_error": False},
                                     {"exit_code": 0, "is_error": False}])}
    for path, key in (("source-manifest.json", "source_manifest_sha256"), ("bin/buddy", "buddy_sha256"),
                      ("bin/buddy-adapter", "adapter_sha256")):
        r[key] = hashlib.sha256((root / path).read_bytes()).hexdigest()
    e = base.Evidence(r)
    r["evidence_sha256"] = {str(e.path(p).relative_to(e.root)): hashlib.sha256(e.path(p).read_bytes()).hexdigest()
                            for p in evidence_paths(r)}
    return r


def synthetic_self_test():
    """The receipt mutations against synthetic evidence, plus evidence edits."""
    with tempfile.TemporaryDirectory(prefix="buddy-resume-synthetic-") as tmp:
        root = Path(tmp).resolve()
        r = synthetic(root)
        out = self_test(r)
        # The synthetic files are ours to damage: each edit must fail the
        # named check, so the fixture is not merely a receipt that passes.
        for name, edit, check in (
                ("native-observations.jsonl", lambda rows: rows.pop(3), "exact_native_resume_argv"),
                ("native-observations.jsonl", lambda rows: rows.insert(6, rows[5]), "resume_rechecks_native_settings_and_mods"),
                ("identity.json.events", lambda rows: rows.pop(1), "original_native_completed_idle")):
            path = root / STATE / name
            original = path.read_bytes()
            rows = [json.loads(v) for v in original.decode().splitlines()]
            edit(rows)
            path.write_text("".join(json.dumps(v) + "\n" for v in rows))
            bad = copy.deepcopy(r)
            bad["evidence_sha256"][STATE + name] = hashlib.sha256(path.read_bytes()).hexdigest()
            verdict = validate(bad)
            if verdict["passed"] or verdict["checks"].get(check) is not False or not verdict["checks"]["original_evidence_hashes"]:
                raise AssertionError("synthetic evidence edit did not fail " + check)
            path.write_bytes(original)
            out["rejected_mutations"] += 1
        if not validate(r)["passed"]:
            raise AssertionError("synthetic control did not restore")
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
