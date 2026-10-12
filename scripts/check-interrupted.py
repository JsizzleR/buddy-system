#!/usr/bin/env python3
"""Read-only independent evidence checks for the interrupted recovery fixture.

No receipt-supplied executable runs and no live Buddy store opens. Damage tests
need an actual passing receipt and change only an in-memory copy of its claims.
"""
import argparse
import copy
import importlib.util
import json
from pathlib import Path
import sys

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("interrupted_evidence", Path(__file__).with_name("check-mixed-fleet.py"))
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)
ROLES = ("interrupt", "active_crash", "approval_crash", "lost_result")


def reconcile_boundary(response, identity, message_id, delivery, wire):
    """D-077: known-turn history is withdrawn; a lost-turn journal stays unknown.

    Inspect every adapter-written native frame, not receipt offsets. Fixture
    history captures are explicitly separate and never become adapter evidence.
    """
    if (response.get("schema") != "buddy.control.v1" or response.get("operation") != "reconcile" or
            response.get("identity") != identity or response.get("message_id") != message_id or
            response.get("wake_delivery") != delivery or
            response.get("capabilities", {}).get("reconcile", {}).get("support") != "unsupported" or
            any(response.get(key, {}).get("state") != "unknown"
                for key in ("completion", "acknowledgment", "body_delivery")) or
            any(row.get("type") == "adapter_frame" and
                row.get("frame", {}).get("method") in ("thread/read", "thread/turns/list") for row in wire)):
        return False
    if delivery.get("turn_id"):
        return ("Codex reconcile is unsupported" in response.get("error", "") and
                "turn_observation" not in response)
    observation = response.get("turn_observation", {})
    return (not response.get("error") and observation.get("source") == "transport_journal" and
            observation.get("native_session_id") == identity["session_id"] and
            not observation.get("turn_id") and observation.get("status", {}).get("state") == "unknown")


def validate(receipt):
    e = base.Evidence(receipt)
    r = receipt
    e.check("schema_roles_provider", lambda: r["schema"] == "buddy.interrupted.v1" and
        r["provider"] == "deterministic-loopback" and set(r["cases"]) == set(r["sessions"]) == set(ROLES))
    for role in ROLES:
        c = r["cases"].get(role, {})
        pair = e.pair(role)
        def check(name, predicate):
            e.check(role + ":" + name, predicate)
        check("bound_wake", lambda: c["wake"]["identity"] == e.session(role)["identity"] == c["identity"] and
              c["wake"]["message_id"] == c["queue"]["delivery"]["message_id"] and
              c["wake"]["wake_delivery"]["state"] == "delivered" and bool(c["wake"]["wake_delivery"]["turn_id"]))
        turn = c.get("wake", {}).get("wake_delivery", {}).get("turn_id")
        mid = c.get("wake", {}).get("message_id")
        check("busy_exact_turn", lambda: c["busy"]["native_event"] ==
              {"kind": "busy", "native_session_id": pair[0], "turn_id": turn})
        check("duplicate_without_inference", lambda: c["requests_before"] == c["requests_after"] and
              c["duplicate"]["wake_delivery"] == c["wake"]["wake_delivery"] and
              c["duplicate"]["message_id"] == mid and not c["duplicate"].get("error"))
        check("reconcile_withdrawn_without_native_query", lambda: reconcile_boundary(
            c["reconcile"], c["identity"], mid, c["wake"]["wake_delivery"],
            [json.loads(line) for line in e.path(role + "-state/native-observations.jsonl").read_text().splitlines()]) and
            c["reconcile"] in e.session(role)["events"])
        check("claim_remains_open", lambda: e.one(c["ledger_after_reconcile"],
              "SELECT session_id,incarnation,state,outcome FROM claims WHERE slug=?", (role,)) ==
              {"session_id": pair[0], "incarnation": pair[1], "state": "open", "outcome": ""})
        check("claim_id_preserved", lambda: e.one(c["ledger_before"],
              "SELECT claim_id,session_id,incarnation,state FROM claims WHERE slug=?", (role,)) ==
              e.one(c["ledger_after_reconcile"], "SELECT claim_id,session_id,incarnation,state FROM claims WHERE slug=?", (role,)))
        check("body_still_queued", lambda: not e.rows(c["ledger_after_reconcile"],
              "SELECT * FROM inbox_delivery WHERE msg_id=? AND session_id=?", (mid, pair[0])))
        check("explicit_recovery_delivery", lambda: c["recovered"]["messages"]["count"] == 1 and
              c["delivered"]["delivery"]["message_id"] == mid and c["delivered"]["delivery"]["state"] == "delivered" and
              bool(e.rows(r["ledger"], "SELECT * FROM inbox_delivery WHERE msg_id=? AND session_id=?", (mid, pair[0]))))
        fresh = c.get("fresh_wake", {}).get("wake_delivery", {}).get("turn_id")
        check("fresh_turn_and_approval", lambda: fresh and fresh != turn and
              e.path(Path(e.session(role)["worktree"]) / ".fixture/fresh.txt").read_text() == "fresh" and
              any(v.get("approval_delivery", {}).get("state") == "submitted" and
                  v["approval_response"]["turn_id"] == fresh for v in e.session(role)["events"]) and
              any(v.get("native_event", {}).get("result_status") == "completed" and
                  v["native_event"]["turn_id"] == fresh for v in e.session(role)["events"]))
        if role != "interrupt":
            check("crash_and_same_identity_resume", lambda: c["crash"]["exit"] == -9 and not c["crash"]["unparsed"] and
                  c["crash"]["children"]["status"] == "observed_absent" and
                  c["resume"]["identity"] == c["identity"] and c["resume"]["state"]["state"] == "unknown" and
                  c["resume"]["pending_approval"]["state"] == "unknown")
            check("crash_not_logical_end", lambda: e.one(c["ledger_after_crash"],
                  "SELECT incarnation,ended FROM sessions WHERE session_id=?", (pair[0],)) ==
                  {"incarnation": pair[1], "ended": None})
        if role in ("active_crash", "approval_crash"):
            check("unfinished_turn_remains_unobserved", lambda:
                  not any(v.get("native_event", {}).get("kind") == "result" and v["native_event"]["turn_id"] == turn
                          for v in c["crash"]["events"]) and "turn_observation" not in c["reconcile"])
        if role == "active_crash":
            check("crash_during_provider_request", lambda: c["provider_barrier"] is True)
        if role in ("interrupt", "approval_crash"):
            check("pending_and_stale_approval_refused", lambda: c["approval"]["pending_approval"]["state"] == "observed" and
                  c["approval"]["native_event"]["approval"]["turn_id"] == turn and
                  bool(c["replay"].get("error")) and "approval_delivery" not in c["replay"] and
                  not (Path(e.session(role)["worktree"]) / ".fixture/unapproved.txt").exists())
        if role == "interrupt":
            check("interrupted_is_not_completed", lambda: c["interrupt"]["state"]["state"] == "unknown" and
                  c["result"]["native_event"]["result_status"] == "interrupted" and
                  c["result"]["native_event"]["turn_id"] == turn and "turn_observation" not in c["reconcile"])
        if role == "lost_result":
            check("dropped_result_is_fixture_evidence_only", lambda: c["dropped"]["method"] == "turn/completed" and
                  c["dropped"]["params"]["threadId"] == pair[0] and c["dropped"]["params"]["turn"]["id"] == turn and
                  c["dropped"]["params"]["turn"]["status"] == "completed" and "turn_observation" not in c["reconcile"] and
                  not any(v.get("native_event", {}).get("kind") == "result" and v["native_event"]["turn_id"] == turn
                          for v in c["crash"]["events"]) and
                  e.path(Path(e.session(role)["worktree"]) / ".fixture/once.txt").read_text() == "once\n")
    e.check("all_receipts_keep_completion_unknown", lambda: all(
        v["identity"] == r["sessions"][role]["identity"] and v["completion"]["state"] == "unknown" and
        v["acknowledgment"]["state"] == "unknown" for role in ROLES
        for v in r["sessions"][role]["events"] + r["cases"][role].get("crash", {}).get("events", [])))
    e.check("closed_children_and_claims", lambda: not r["cleanup_errors"] and
            all(s["adapter_exit"] == 0 and not s["unparsed"] and s["native_child_exit"]["status"] == "observed_absent"
                for s in r["sessions"].values()) and not e.rows(r["ledger"], "SELECT claim_id FROM claims WHERE state='open'"))
    return {"passed": all(e.checks.values()), "checks": e.checks, "errors": e.errors}


def self_test(receipt):
    if not validate(receipt)["passed"]:
        raise AssertionError("positive receipt failed")
    changes = [(("provider",), "real"), (("cleanup_errors",), ["failed"])]
    for role in ROLES:
        prefix = ("cases", role)
        changes += [(prefix + path, value) for path, value in (
            (("wake", "wake_delivery", "turn_id"), "other"),
            (("requests_after",), -1),
            (("reconcile", "error"), ""),
            (("reconcile", "wake_delivery", "turn_id"), "other"),
            (("reconcile", "turn_observation"), {"source": "native_thread_read"}),
            (("reconcile", "capabilities", "reconcile", "support"), "experimental"),
            (("reconcile", "completion", "state"), "completed"),
            (("recovered", "messages", "count"), 0),
            (("fresh_wake", "wake_delivery", "turn_id"), "other"))]
        if role != "interrupt":
            changes += [(prefix + ("crash", "exit"), 0), (prefix + ("resume", "identity", "incarnation"), "wrong")]
        if role in ("interrupt", "approval_crash"):
            changes.append((prefix + ("replay", "error"), ""))
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
