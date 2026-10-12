#!/usr/bin/env python3
"""Read-only checks of uncertain recovery journals, native frames and effects.

The fixture's success flag is not evidence. Read retained files and immutable
ledger snapshots independently, and reject damaged receipts via --self-test.
"""
import argparse
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import sys

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("uncertain_evidence", Path(__file__).with_name("check-mixed-fleet.py"))
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)
spec = importlib.util.spec_from_file_location("codex_reconcile_boundary", Path(__file__).with_name("check-interrupted.py"))
reconcile_checks = importlib.util.module_from_spec(spec)
spec.loader.exec_module(reconcile_checks)
ROLES = ("lost_wake", "approval_partial", "approval_forwarded")


def validate(r):
    e = base.Evidence(r)
    e.check("schema_roles_provider", lambda: r["schema"] == "buddy.uncertain.v1" and
        r["provider"] == "deterministic-loopback" and set(r["cases"]) == set(r["sessions"]) == set(ROLES))
    e.check("artifact_hashes", lambda: all(hashlib.sha256(e.path(path).read_bytes()).hexdigest() == r[key]
        for path, key in (("source-manifest.json", "source_manifest_sha256"), ("bin/buddy", "buddy_sha256"),
                          ("bin/buddy-adapter", "adapter_sha256"))))
    for role in ROLES:
        c, session = r["cases"].get(role, {}), e.session(role)
        pair = e.pair(role)
        mid = c.get("queue", {}).get("delivery", {}).get("message_id")
        state = role + "-state/"
        def check(name, predicate):
            e.check(role + ":" + name, predicate)
        def lines(name):
            return [json.loads(v) for v in e.path(state + name).read_text().splitlines()]
        def journal_before_resume():
            rows = lines("identity.json.events")
            end = next(i for i, v in enumerate(rows) if v["operation"] == "resume")
            return rows[:end]
        def trace_slice():
            # Derive the recovery interval from the wire, not receipt-supplied
            # offsets which could accidentally skip a replayed input.
            rows = lines("native-observations.jsonl")
            resumes = [i for i, v in enumerate(rows) if v["type"] == "adapter_frame" and v["frame"].get("method") == "thread/resume"]
            if len(resumes) != 1:
                raise ValueError("expected one native resume")
            start = resumes[0]
            request = rows[start]["frame"]["id"]
            reply = next(i for i in range(start + 1, len(rows)) if rows[i]["type"] == "native_frame" and
                         rows[i]["frame"].get("id") == request and "result" in rows[i]["frame"])
            starts = [i for i in range(reply + 1, len(rows)) if rows[i]["type"] == "adapter_frame" and
                      rows[i]["frame"].get("method") in ("turn/start", "turn/steer")]
            replies = [v for v in rows[reply + 1:] if v["type"] == "adapter_frame" and "result" in v["frame"]]
            if len(starts) != 1 or len(replies) != 1:
                raise ValueError("recovery must emit only the fresh input and fresh approval")
            return rows[reply + 1:starts[0]]
        check("captured_identity", lambda: session["identity"] == c["identity"] == c["resume"]["identity"] and
              session["native_version"] == "codex-cli 0.160.0" and session["harness"] == "codex" and
              all(v["identity"] == c["identity"] for v in lines("identity.json.events")))
        check("crash_not_end", lambda: c["crash"]["exit"] == -9 and not c["crash"]["unparsed"] and
              c["crash"]["children"]["status"] == "observed_absent" and
              e.one(c["ledger_after_crash"], "SELECT incarnation,ended FROM sessions WHERE session_id=?", (pair[0],)) ==
              {"incarnation": pair[1], "ended": None})
        check("resume_unknown", lambda: c["resume"]["state"]["state"] == c["resume"]["pending_approval"]["state"] == "unknown" and
              not c["resume"].get("approvals") and not c["resume"].get("error"))
        check("no_replay", lambda: c["requests_before"] == c["requests_after"] and
              0 < c["trace_before"] <= c["trace_after"] <= len(lines("native-observations.jsonl")) and
              c["duplicate"]["message_id"] == mid and not c["duplicate"].get("error") and
              not any(v["type"] == "adapter_frame" and (v["frame"].get("method") in ("turn/start", "turn/steer") or
                  ("result" in v["frame"] and "method" not in v["frame"])) for v in trace_slice()))
        check("no_inferred_effects", lambda: all(c["reconcile"][key]["state"] == "unknown"
              for key in ("completion", "acknowledgment", "body_delivery", "pending_approval")) and
              c["reconcile"]["message_id"] == mid)
        check("reconcile_withdrawal_boundary", lambda: reconcile_checks.reconcile_boundary(
            c["reconcile"], c["identity"], mid, c["duplicate"]["wake_delivery"], lines("native-observations.jsonl")) and
            c["reconcile"] in session["events"])
        check("claim_preserved", lambda: e.one(c["ledger_before"],
              "SELECT claim_id,session_id,incarnation,state,outcome FROM claims WHERE slug=?", (role,)) ==
              e.one(c["ledger_after_reconcile"], "SELECT claim_id,session_id,incarnation,state,outcome FROM claims WHERE slug=?", (role,)) and
              e.one(c["ledger_after_reconcile"], "SELECT state,outcome FROM claims WHERE slug=?", (role,)) == {"state": "open", "outcome": ""})
        check("queued_until_explicit_retrieval", lambda: not e.rows(c["ledger_after_reconcile"],
              "SELECT * FROM inbox_delivery WHERE msg_id=? AND session_id=?", (mid, pair[0])) and
              c["recovered"]["messages"]["count"] == 1 and
              c["recovered"]["messages"]["items"][0]["message_id"] == mid and
              c["delivered"]["delivery"]["message_id"] == mid and c["delivered"]["delivery"]["state"] == "delivered" and
              bool(e.rows(r["ledger"], "SELECT * FROM inbox_delivery WHERE msg_id=? AND session_id=?", (mid, pair[0]))))
        fresh = c.get("fresh_wake", {}).get("wake_delivery", {}).get("turn_id")
        check("fresh_approval_and_turn", lambda: fresh and c["fresh_wake"]["message_id"] != mid and
              e.path(Path(session["worktree"]) / ".fixture/fresh.txt").read_text() == "fresh" and
              any(v.get("approval_delivery", {}).get("state") == "submitted" and v["approval_response"]["turn_id"] == fresh
                  and v["pending_approval"]["state"] == "unknown" and not v.get("approvals") for v in session["events"]) and
              any(v.get("native_event", {}).get("result_status") == "completed" and
                  v["native_event"]["turn_id"] == fresh for v in session["events"]))
        if role == "lost_wake":
            def lost():
                dropped = json.loads(e.path(state + "dropped-wake.json").read_text())
                completed = json.loads(e.path(state + "native-completed.json").read_text())
                turn = dropped["result"]["turn"]["id"]
                intents = [v for v in journal_before_resume() if v.get("message_id") == mid]
                obs = c["reconcile"]["turn_observation"]
                return (c["dropped_wake"] == dropped and c["native_completed"] == completed and bool(turn) and
                    completed["params"]["turn"]["id"] == turn and completed["params"]["threadId"] == pair[0] and
                    completed["params"]["turn"]["status"] == "completed" and len(intents) == 1 and
                    intents[0]["wake_delivery"] == c["duplicate"]["wake_delivery"] == {"state": "unknown"} and
                    not any(v.get("native_event", {}).get("turn_id") == turn for v in journal_before_resume()) and
                    not obs.get("turn_id") and obs["status"]["state"] == "unknown" and
                    obs["native_session_id"] == pair[0] and obs["source"] == "transport_journal" and
                    not any(v["type"] == "adapter_frame" for v in trace_slice()))
            check("lost_reply_stays_unknown_without_native_query", lost)
        else:
            turn = c.get("wake", {}).get("wake_delivery", {}).get("turn_id")
            check("exact_wake_and_approval", lambda: c["wake"]["identity"] == c["identity"] and
                  c["wake"]["message_id"] == mid and c["wake"]["wake_delivery"]["state"] == "delivered" and
                  c["duplicate"]["wake_delivery"] == c["wake"]["wake_delivery"] and
                  c["approval"]["native_event"]["approval"]["turn_id"] == c["decision"]["turn_id"] == turn and
                  c["decision"]["native_session_id"] == pair[0])
            check("spent_prompt_not_actionable", lambda: c["approval_response"]["approval_response"] == c["decision"] and
                  c["approval_response"]["approval_delivery"]["state"] == "submitted" and
                  c["approval_response"]["pending_approval"]["state"] == "unknown" and
                  not c["approval_response"].get("approvals") and bool(c["replay"].get("error")) and
                  "approval_delivery" not in c["replay"])
            def seam():
                proof = json.loads(e.path(state + "approval-delivery.json").read_text())
                attempts = [v for v in journal_before_resume() if v.get("approval_response") == c["decision"]]
                forwarded = role == "approval_forwarded"
                return (proof == c["delivery_seam"] and proof["frame"] == {"id": c["decision"]["id"], "result": {"decision": "accept"}} and
                    proof["complete"] == forwarded and 0 < proof["bytes_written"] <= proof["frame_bytes"] and
                    (proof["bytes_written"] == proof["frame_bytes"]) == forwarded and
                    len(attempts) == 2 and attempts[0]["approval_delivery"]["state"] == "unknown" and
                    attempts[1]["approval_delivery"]["state"] == "submitted" and
                    not any(v.get("native_event", {}).get("kind") in ("result", "approval_resolved") for v in journal_before_resume()))
            check("native_delivery_fault_seam", seam)
            if role == "approval_forwarded":
                check("one_observed_effect", lambda: e.path(Path(session["worktree"]) / ".fixture/effect.txt").read_text() == "once\n" and
                      c["native_completed"] == json.loads(e.path(state + "native-completed.json").read_text()) and
                      c["native_completed"]["params"]["turn"]["id"] == turn and
                      c["native_completed"]["params"]["turn"]["status"] == "completed")
            else:
                check("partial_response_did_not_execute", lambda: not (e.path(session["worktree"]) / ".fixture/effect.txt").exists())
    e.check("completion_remains_unknown", lambda: all(v["completion"]["state"] == v["acknowledgment"]["state"] == "unknown"
        for role in ROLES for v in r["sessions"][role]["events"] + r["cases"][role]["crash"]["events"]))
    e.check("clean_shutdown", lambda: not r["cleanup_errors"] and
        all(s["adapter_exit"] == 0 and not s["unparsed"] and s["native_child_exit"]["status"] == "observed_absent" for s in r["sessions"].values()) and
        not e.rows(r["ledger"], "SELECT claim_id FROM claims WHERE state='open'"))
    return {"passed": all(e.checks.values()), "checks": e.checks, "errors": e.errors}


def self_test(receipt):
    if not validate(receipt)["passed"]:
        raise AssertionError("positive receipt failed")
    changes = [(("provider",), "real"), (("cleanup_errors",), ["failed"]), (("adapter_sha256",), "wrong")]
    for role in ROLES:
        prefix = ("cases", role)
        changes += [(prefix + path, value) for path, value in (
            (("requests_after",), -1), (("crash", "exit"), 0),
            (("resume", "identity", "incarnation"), "wrong"),
            (("reconcile", "completion", "state"), "completed"),
            (("reconcile", "capabilities", "reconcile", "support"), "experimental"),
            (("recovered", "messages", "count"), 0), (("fresh_wake", "wake_delivery", "turn_id"), "wrong"))]
        if role != "lost_wake":
            changes += [(prefix + path, value) for path, value in (
                (("reconcile", "error"), ""),
                (("reconcile", "turn_observation"), {"source": "native_thread_read"}),
                (("approval_response", "pending_approval", "state"), "observed"),
                (("delivery_seam", "bytes_written"), 0), (("replay", "error"), ""),
                (("wake", "wake_delivery", "turn_id"), "wrong"))]
        else:
            changes += [(prefix + ("reconcile", "turn_observation", "turn_id"), "guessed"),
                        (prefix + ("dropped_wake", "result", "turn", "id"), "wrong")]
    for path, value in changes:
        damaged = copy.deepcopy(receipt)
        target = damaged
        for key in path[:-1]:
            target = target[key]
        target[path[-1]] = value
        if validate(damaged)["passed"]:
            raise AssertionError("accepted damaged evidence: " + str(path))
    return {"positive": "passed", "damaged_cases_rejected": len(changes)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("receipt", type=Path)
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()
    receipt = json.loads(args.receipt.read_text())
    result = self_test(receipt) if args.self_test else validate(receipt)
    print(json.dumps(result, indent=2))
    return 0 if args.self_test or result["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
