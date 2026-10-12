#!/usr/bin/env python3
"""Independently inspect recovery receipts, immutable ledger copies and Git.

No Buddy store is opened; no receipt-supplied program is executed. --self-test
requires an actual passing receipt as the positive control, then corrupts each
evidence leg in memory. It leaves the original artifact directory unchanged.
"""

import argparse
import copy
import importlib.util
import json
from pathlib import Path
import sys

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("recovery_evidence", Path(__file__).with_name("check-mixed-fleet.py"))
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)


def validate(receipt):
    e = base.Evidence(receipt)
    r = receipt
    coordinator = e.pair("coordinator")
    successor = e.pair("successor")
    workers = ("claude_worker", "codex_worker")
    e.check("schema_and_provider", lambda: r["schema"] == "buddy.recovery.v1" and r["provider"] == "deterministic-loopback")
    e.check("separate_worktrees", lambda: len({str(e.path(s["worktree"])) for s in r["sessions"].values()}) == 4 and
            len({base.git(e.path(s["worktree"]), "rev-parse", "--path-format=absolute", "--git-common-dir")
                 for s in r["sessions"].values()}) == 1)
    restart = r["restart"]
    def native_results():
        roles = {"coordinator": "codex", "claude_worker": "claude", "codex_worker": "codex", "successor": "codex"}
        if set(r["sessions"]) != set(roles):
            return False
        for role, harness in roles.items():
            events = list(e.session(role)["events"])
            if role == "coordinator":
                events += restart["before"]["events"]
            results, idle, starts = set(), set(), 0
            for event in events:
                if (event["schema"] != "buddy.control.v1" or event["identity"] != e.session(role)["identity"] or
                        event["identity"]["harness"] != harness):
                    return False
                if event.get("error") and not (role == "coordinator" and event == r["approval"]["replay"]):
                    return False
                starts += event["operation"] == "start"
                native = event.get("native_event", {})
                if native and (native["native_session_id"] != e.pair(role)[0] or native.get("error")):
                    return False
                if native.get("kind") == "result":
                    if native.get("result_status") != "completed" or not native.get("result_ref"):
                        return False
                    results.add(native["turn_id"])
                if native.get("kind") == "idle":
                    idle.add(native["turn_id"])
            if starts != 1 or not results or results != idle:
                return False
        return True

    e.check("native_result_identity_and_idle", native_results)
    # This fixture does not assert that every initial assignment found an idle
    # worker. Allow an unknown initial observation, retaining the rest of the
    # existing independent delivery/ledger/native-output checks unchanged.
    e.check("native_inbox_delivery_rows", lambda: len(r["messages"]) == 8 and
            all(e.message(dict(message, purpose="bootstrap")) for message in r["messages"]))
    e.check("closed_before_resume", lambda: restart["before"]["exit"] == 0 and not restart["before"]["unparsed"] and
            restart["before"]["children"]["status"] == "observed_absent")
    e.check("same_captured_identity", lambda: restart["resume"]["operation"] == "resume" and
            restart["resume"]["identity"] == r["sessions"]["coordinator"]["identity"] and
            restart["resume"]["state"]["state"] == "unknown" and restart["inspect"]["state"]["state"] == "idle")
    e.check("transport_exit_not_logical_end", lambda: all(e.one(r[path],
            "SELECT incarnation,ended FROM sessions WHERE session_id=?", (coordinator[0],)) ==
            {"incarnation": coordinator[1], "ended": None} for path in ("before_resume",)) and
            e.one(restart["ledger"], "SELECT incarnation,ended FROM sessions WHERE session_id=?", (coordinator[0],)) ==
            {"incarnation": coordinator[1], "ended": None})
    e.check("duplicate_keeps_native_turn", lambda: r["duplicate"]["wake_delivery"] == r["approval"]["wake"]["wake_delivery"] and
            r["duplicate"]["message_id"] == r["approval"]["queue"]["delivery"]["message_id"] and
            restart["requests_before_duplicate"] == restart["requests_after_duplicate"])
    e.check("native_approval_was_pending", lambda: r["approval"]["request"]["state"] ==
            {"state": "observed", "detail": "waiting_for_approval"} and
            r["approval"]["request"]["pending_approval"]["state"] == "observed" and
            r["approval"]["answer"]["approval_response"] == r["approval"]["decision"] and
            r["approval"]["decision"]["decision"] == "allow" and
            r["approval"]["answer"]["approval_delivery"]["state"] == "submitted")
    e.check("old_approval_refused_after_resume", lambda: bool(r["approval"]["replay"].get("error")) and
            "approval_delivery" not in r["approval"]["replay"])
    e.check("fresh_approval_control", lambda: any(x.get("approval_delivery", {}).get("state") == "submitted" and
            x["approval_response"]["turn_id"] != r["approval"]["decision"]["turn_id"]
            for x in e.session("coordinator")["events"]))
    e.check("queued_body_retrieved_after_resume", lambda: r["recovered_mail"]["inbox"]["messages"]["count"] == 1 and
            r["recovered_mail"]["delivered"]["delivery"]["message_id"] == r["approval"]["queue"]["delivery"]["message_id"] and
            r["recovered_mail"]["delivered"]["delivery"]["state"] == "delivered")
    resumed_outputs = json.loads(next(m["native_output"] for m in r["messages"] if m["purpose"] == "post-resume"))
    e.check("resumed_gate_denial_and_control", lambda:
            any(x["kind"] == "patch" and "Command blocked by PreToolUse hook: claude.txt is inside scope" in x["output"]
                for x in resumed_outputs) and
            any(x["kind"] == "patch" and "Exit code: 0" in x["output"] and "A .fixture/resumed.txt" in x["output"]
                for x in resumed_outputs) and
            not (e.path(e.session("coordinator")["worktree"]) / "collateral.txt").exists() and
            (e.path(e.session("coordinator")["worktree"]) / ".fixture/approved-control.txt").read_text() == "approved" and
            (e.path(e.session("coordinator")["worktree"]) / ".fixture/resumed.txt").read_text() == "permitted\n")

    def transfers():
        for slug in ("orchestrator", "run-recovery"):
            query = "SELECT claim_id,session_id,incarnation,state,outcome FROM claims WHERE slug=? AND state='open'"
            before = e.one(r["before_transfer"], query, (slug,))
            after = e.one(r["after_transfer"], query, (slug,))
            if (before["session_id"], before["incarnation"]) != coordinator or (after["session_id"], after["incarnation"]) != successor:
                return False
            if before["claim_id"] != after["claim_id"] or before["outcome"] != after["outcome"] or r["handoff"]["claim_ids"][slug] != before["claim_id"]:
                return False
        return True

    e.check("claim_ids_survive_transfer", transfers)

    def ready_preserved():
        for worker in workers:
            identity = e.pair(worker)
            query = ("SELECT w.decl,w.incarnation,w.ready_sha,w.cleared,t.claim_id FROM session_waits w "
                     "JOIN session_wait_targets t ON w.decl=t.decl WHERE w.session_id=?")
            before = e.one(r["before_transfer"], query, (identity[0],))
            after = e.one(r["after_transfer"], query, (identity[0],))
            declared = next(row for row in r["ready"] if row["role"] == worker)
            if before != after or after["cleared"] is not None or after["incarnation"] != identity[1]:
                return False
            if after["ready_sha"] != declared["sha"] or after["claim_id"] != declared["claim_id"]:
                return False
            if r["handoff"]["workers"][worker]["ready_sha"] != after["ready_sha"]:
                return False
            if base.git(e.path(e.session(worker)["worktree"]), "rev-parse", "HEAD") != after["ready_sha"]:
                return False
        return True

    e.check("ready_declarations_survive_transfer", ready_preserved)
    handoff = r["handoff"]
    e.check("handoff_names_result_reader", lambda: handoff["outgoing"] == e.session("coordinator")["identity"] and
            handoff["successor"] == handoff["job"]["result_reader"] == e.session("successor")["identity"] and
            handoff["job"]["owner"] == "qualification fixture" and bool(handoff["outstanding"]))
    e.check("job_outlives_coordinator", lambda: r["job_alive_after_outgoing_exit"] is True and r["job_exit"] == 0)
    job = json.loads(e.path(handoff["job"]["result"]).read_text())
    e.check("job_checks_exact_commit", lambda: job == r["landing"]["job"] and job["exit_code"] == 0 and
            job["sha"] == job["after_sha"] == job["expected_sha"] == handoff["sha"] and
            e.path(handoff["job"]["stdout"]).read_text().strip() == "both worker changes validated" and
            not e.path(handoff["job"]["stderr"]).read_text())
    e.check("validated_blobs_and_landing", lambda: r["landing"]["sha"] == handoff["sha"] and
            r["landing"]["ref"] == "refs/heads/main" and
            base.git(e.path(r["landing"]["worktree"]), "rev-parse", r["landing"]["ref"]) == handoff["sha"] and
            all(base.git(e.path(r["landing"]["worktree"]), "show", handoff["sha"] + ":" + name + ".txt", strip=False) == "fixed\n"
                for name in ("claude", "codex")))
    e.check("run_released_by_successor", lambda: e.one(r["landed_ledger"],
            "SELECT session_id,incarnation,state,outcome FROM claims WHERE slug='run-recovery'") ==
            {"session_id": successor[0], "incarnation": successor[1], "state": "released", "outcome": "pass"})
    e.check("landing_notices_delivered", lambda: {x["recipient"] for x in r["messages"] if x["purpose"] == "landed" and
            x["sender"] == "successor" and x["delivered"]["delivery"]["state"] == "delivered"} == set(workers))
    e.check("delayed_old_incarnation_refused", lambda: {x["operation"] for x in r["delayed_events"] if x["exit_code"] != 0} ==
            {"refresh", "busy", "idle", "end"} and r["renewed_identity"]["incarnation"] != coordinator[1] and
            e.one(r["ledger"], "SELECT incarnation,ended FROM sessions WHERE session_id=?", (coordinator[0],)) ==
            {"incarnation": r["renewed_identity"]["incarnation"], "ended": None})
    e.check("delayed_events_preserve_new_claim", lambda: e.one(r["before_delayed"],
            "SELECT claim_id,session_id,incarnation,state FROM claims WHERE slug='renewed-guard'") ==
            e.one(r["after_delayed"], "SELECT claim_id,session_id,incarnation,state FROM claims WHERE slug='renewed-guard'") and
            e.one(r["after_delayed"], "SELECT incarnation,state FROM claims WHERE slug='renewed-guard'") ==
            {"incarnation": r["renewed_identity"]["incarnation"], "state": "open"})
    e.check("stale_resume_before_native_launch", lambda: r["stale_resume"]["exit_code"] != 0 and
            "logical session identity refused" in r["stale_resume"]["stderr"] and
            r["stale_resume"]["trace_before"] == r["stale_resume"]["trace_after"])
    e.check("closed_claims_and_children", lambda: not e.rows(r["ledger"], "SELECT claim_id FROM claims WHERE state='open'") and
            all(s["adapter_exit"] == 0 and not s["unparsed"] and s["native_child_exit"]["status"] == "observed_absent"
                for s in r["sessions"].values()) and not r["cleanup_errors"])
    e.check("native_results_not_completion", lambda: all(x["completion"]["state"] == "unknown" and
            x["acknowledgment"]["state"] == "unknown" for s in r["sessions"].values() for x in s["events"]))
    return {"passed": bool(e.checks) and all(e.checks.values()), "checks": e.checks, "errors": e.errors}


def self_test(receipt):
    positive = validate(receipt)
    if not positive["passed"]:
        raise AssertionError(positive)
    changes = [
        (("provider",), "real"),
        (("restart", "resume", "identity", "incarnation"), "wrong"),
        (("restart", "before", "exit"), 1),
        (("restart", "requests_after_duplicate"), -1),
        (("sessions", "codex_worker", "events"), []),
        (("messages", 0, "native_output"), "[]"),
        (("duplicate", "wake_delivery", "turn_id"), "wrong"),
        (("approval", "request", "state", "detail"), "idle"),
        (("approval", "answer", "approval_delivery", "state"), "unknown"),
        (("approval", "replay", "error"), ""),
        (("recovered_mail", "delivered", "delivery", "state"), "queued"),
        (("after_transfer",), receipt["before_transfer"]),
        (("handoff", "claim_ids", "run-recovery"), "wrong"),
        (("ready", 0, "claim_id"), "wrong"),
        (("handoff", "workers", "claude_worker", "ready_sha"), "wrong"),
        (("handoff", "job", "result_reader", "incarnation"), "wrong"),
        (("job_alive_after_outgoing_exit",), False),
        (("job_exit",), 1),
        (("landing", "job", "sha"), "wrong"),
        (("landing", "sha"), "wrong"),
        (("renewed_identity", "incarnation"), receipt["sessions"]["coordinator"]["identity"]["incarnation"]),
        (("delayed_events", 0, "exit_code"), 0),
        (("after_delayed",), receipt["ledger"]),
        (("stale_resume", "exit_code"), 0),
        (("stale_resume", "trace_after"), "wrong"),
        (("sessions", "successor", "native_child_exit", "status"), "unknown"),
    ]
    for path, value in changes:
        broken = copy.deepcopy(receipt)
        node = broken
        for key in path[:-1]:
            node = node[key]
        node[path[-1]] = value
        if validate(broken)["passed"]:
            raise AssertionError("validator accepted broken evidence: " + repr(path))
    return {"positive": True, "rejected_mutations": len(changes)}


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
