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
import tempfile

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


HARNESS = {"coordinator": "codex", "claude_worker": "claude", "codex_worker": "codex", "successor": "codex"}


def synthetic(root):
    """A coherent SYNTHETIC recovery capture under root (base.Synthetic says what that is)."""
    s = base.Synthetic(root)
    ident = {role: {"schema": "buddy.lifecycle.v1", "harness": harness, "session_id": "sid-" + role, "incarnation": "inc-" + role}
             for role, harness in HARNESS.items()}
    pair = lambda role: {"session_id": ident[role]["session_id"], "incarnation": ident[role]["incarnation"]}
    ev = lambda role, op, **f: base.control(op, ident[role], **f)
    native = lambda role, kind, turn, **f: ev(role, "event", native_event=dict(kind=kind, native_session_id=ident[role]["session_id"], turn_id=turn, **f))
    def turn_events(role, turn):
        return [native(role, "result", turn, result_status="completed", result_ref="r-" + turn), native(role, "idle", turn)]
    # One repository, four linked worktrees: one common dir, as the fixture runs it.
    work = s.root / "coordinator"
    work.mkdir()
    base.git(work, "init", "-q", "-b", "main")
    base.git(work, "config", "user.name", "Fixture")
    base.git(work, "config", "user.email", "fixture@example.invalid")
    for name in ("claude.txt", "codex.txt"):
        (work / name).write_text("fixed\n")
    base.git(work, "add", "claude.txt", "codex.txt")
    base.git(work, "-c", "commit.gpgsign=false", "commit", "-qm", "both fixed")
    sha = base.git(work, "rev-parse", "HEAD")
    for role in ("claude_worker", "codex_worker", "successor"):
        base.git(work, "worktree", "add", "-q", "--detach", str(s.root / role), sha)
    for name, text in ((".fixture/approved-control.txt", "approved"), (".fixture/resumed.txt", "permitted\n")):
        s.write("coordinator/" + name, text)
    # Eight peer messages, each with its native fetch and its ledger rows.
    plan = [("assignment", "coordinator", "claude_worker"), ("assignment", "coordinator", "codex_worker"),
            ("report", "claude_worker", "coordinator"), ("report", "codex_worker", "coordinator"),
            ("post-resume", "claude_worker", "coordinator"), ("handoff", "coordinator", "successor"),
            ("landed", "successor", "claude_worker"), ("landed", "successor", "codex_worker")]
    messages, inbox_rows, adapter_rows, delivered_rows = [], [], [], []
    for mid, (purpose, sender, recipient) in enumerate(plan, 1):
        body = purpose + " " + sha
        queued = {"message_id": mid, "state": "queued", "sender": pair(sender), "recipient": pair(recipient),
                  "provenance": "peer", "acknowledged": {"status": "unknown"}, "completed": {"status": "unknown"}}
        inbox = {"schema": "buddy.adapter.v1", "operation": "inbox", "identity": pair(recipient),
                 "messages": {"status": "observed", "count": 1, "items": [dict(queued, body=body)]}}
        delivered = {"schema": "buddy.adapter.v1", "operation": "delivered", "delivery": dict(queued, state="delivered", delivered_at=100 + mid)}
        marker = "native-fetch-" + str(mid)
        s.write("fetch-%d.json" % mid, {"inbox": inbox, "delivered": delivered, "marker": marker})
        output = [{"kind": "shell", "exit_code": 0, "output": marker + "\n"}]
        if purpose == "post-resume":
            output += [{"kind": "patch", "output": "Command blocked by PreToolUse hook: claude.txt is inside scope run-recovery"},
                       {"kind": "patch", "output": "Exit code: 0\nA .fixture/resumed.txt"}]
        messages.append({"purpose": purpose, "sender": sender, "recipient": recipient,
            "queue": {"schema": "buddy.adapter.v1", "operation": "queue", "delivery": queued},
            "inbox": inbox, "delivered": delivered, "native_fetch": str(s.root / ("fetch-%d.json" % mid)),
            "native_output": json.dumps(output),
            "idle_before": {"schema": "buddy.adapter.v1", "operation": "inspect", "identity": pair(recipient),
                            "lifecycle": {"status": "observed", "value": "live"}, "observed_state": {"status": "observed", "value": "idle"}},
            "wake": ev(recipient, "send", message_id=mid, wake_delivery={"state": "delivered"}, body_delivery={"state": "queued"})})
        inbox_rows.append((mid, ident[recipient]["session_id"], ident[sender]["session_id"], body))
        adapter_rows.append((mid, ident[sender]["incarnation"], ident[recipient]["incarnation"]))
        delivered_rows.append((mid, ident[recipient]["session_id"], 100 + mid))
    # The coordinator's approval, its crash-free restart, and the handoff.
    approval_mid, decision = 90, {"id": "req-1", "turn_id": "turn-approval", "decision": "allow"}
    approval_wake = ev("coordinator", "send", message_id=approval_mid, wake_delivery={"state": "delivered", "turn_id": "turn-approval"})
    replay = ev("coordinator", "approve", error="approval already answered")
    approval = {"queue": {"delivery": {"message_id": approval_mid}}, "wake": approval_wake, "decision": decision,
                "request": ev("coordinator", "event", state={"state": "observed", "detail": "waiting_for_approval"},
                              pending_approval={"state": "observed"}),
                "answer": ev("coordinator", "approve", approval_response=decision, approval_delivery={"state": "submitted"}),
                "replay": replay}
    resume = ev("coordinator", "resume")
    before = [ev("coordinator", "start"), approval_wake] + turn_events("coordinator", "turn-approval")
    sessions = {}
    for role in HARNESS:
        events = [] if role == "coordinator" else [ev(role, "start")] + turn_events(role, "turn-" + role)
        if role == "coordinator":
            events = [resume, replay, ev(role, "approve", approval_response={"id": "req-2", "turn_id": "turn-fresh"},
                                         approval_delivery={"state": "submitted"})] + turn_events(role, "turn-fresh")
        sessions[role] = {"identity": ident[role], "harness": HARNESS[role], "worktree": str(s.root / role), "events": events,
                          "adapter_exit": 0, "unparsed": [], "native_child_exit": {"status": "observed_absent"}}
    coord, succ = (ident["coordinator"]["session_id"], ident["coordinator"]["incarnation"]), (ident["successor"]["session_id"], ident["successor"]["incarnation"])
    renewed = dict(pair("coordinator"), incarnation="inc-coordinator-renewed")
    claim_rows = lambda owner, state="open", outcome="": [("c-orch", "orchestrator") + owner + (state, outcome),
                                                         ("c-run", "run-recovery") + owner + (state, outcome)]
    waits = [(ident[w]["session_id"], ident[w]["incarnation"], "d-" + w, sha, None) for w in ("claude_worker", "codex_worker")]
    targets = [("d-" + w, ident[w]["session_id"], ident[w]["incarnation"], 0, "c-run", "run-recovery") for w in ("claude_worker", "codex_worker")]
    coordinator_row = [coord + (None,)]
    s.write("job/result.json", {"exit_code": 0, "sha": sha, "after_sha": sha, "expected_sha": sha})
    s.write("job/stdout", "both worker changes validated\n")
    s.write("job/stderr", "")
    job = {"owner": "qualification fixture", "result_reader": ident["successor"], "result": str(s.root / "job/result.json"),
           "stdout": str(s.root / "job/stdout"), "stderr": str(s.root / "job/stderr")}
    guard = ("c-guard", "renewed-guard", renewed["session_id"], renewed["incarnation"], "open", "")
    r = {"schema": "buddy.recovery.v1", "root": str(s.root), "provider": "deterministic-loopback", "cleanup_errors": [],
         "sessions": sessions, "messages": messages, "approval": approval,
         "restart": {"before": {"exit": 0, "unparsed": [], "events": before, "children": {"status": "observed_absent"}},
                     "resume": resume, "inspect": {"state": {"state": "idle"}},
                     "requests_before_duplicate": 4, "requests_after_duplicate": 4,
                     "ledger": s.ledger("restart", sessions=coordinator_row, claims=claim_rows(coord))},
         "before_resume": s.ledger("before-resume", sessions=coordinator_row, claims=claim_rows(coord)),
         "duplicate": {"message_id": approval_mid, "wake_delivery": approval_wake["wake_delivery"]},
         "recovered_mail": {"inbox": {"messages": {"count": 1}},
                            "delivered": {"delivery": {"message_id": approval_mid, "state": "delivered"}}},
         "before_transfer": s.ledger("before-transfer", claims=claim_rows(coord), waits=waits, targets=targets),
         "after_transfer": s.ledger("after-transfer", claims=claim_rows(succ), waits=waits, targets=targets),
         "ready": [{"role": w, "sha": sha, "claim_id": "c-run"} for w in ("claude_worker", "codex_worker")],
         "handoff": {"outgoing": ident["coordinator"], "successor": ident["successor"], "job": job, "sha": sha,
                     "outstanding": ["validate " + sha], "claim_ids": {"orchestrator": "c-orch", "run-recovery": "c-run"},
                     "workers": {w: {"ready_sha": sha} for w in ("claude_worker", "codex_worker")}},
         "job_alive_after_outgoing_exit": True, "job_exit": 0,
         "landing": {"sha": sha, "ref": "refs/heads/main", "worktree": str(work),
                     "job": json.loads((s.root / "job/result.json").read_text())},
         "landed_ledger": s.ledger("landed", claims=[("c-run", "run-recovery") + succ + ("released", "pass")]),
         "delayed_events": [{"operation": op, "exit_code": 1} for op in ("refresh", "busy", "idle", "end")],
         "renewed_identity": renewed,
         "before_delayed": s.ledger("before-delayed", claims=[guard]),
         "after_delayed": s.ledger("after-delayed", claims=[guard]),
         "stale_resume": {"exit_code": 1, "stderr": "buddy-adapter: logical session identity refused\n",
                          "trace_before": "t-7", "trace_after": "t-7"}}
    r["ledger"] = s.ledger("final", sessions=[(renewed["session_id"], renewed["incarnation"], None)],
                           claims=[guard[:4] + ("released", "pass")], delivered=delivered_rows,
                           inbox=inbox_rows, adapter=adapter_rows)
    return s.seal(r, [])


def synthetic_self_test():
    """The receipt mutations above, plus git and evidence edits, against synthetic evidence."""
    with tempfile.TemporaryDirectory(prefix="buddy-recovery-synthetic-") as tmp:
        r = synthetic(Path(tmp) / "case")
        out = self_test(r)
        def refused(why, expect):
            failed = sorted(k for k, v in validate(r)["checks"].items() if not v)
            if expect not in failed:
                raise AssertionError(why + " did not fail " + expect + ": " + str(failed))
            out["rejected_mutations"] += 1
        # The landing is read from git, not from the receipt.
        work = Path(r["landing"]["worktree"])
        (work / "codex.txt").write_text("broken\n")
        base.git(work, "-c", "commit.gpgsign=false", "commit", "-qam", "unvalidated change")
        moved = base.git(work, "rev-parse", "HEAD")
        refused("main moved past the validated commit", "validated_blobs_and_landing")
        base.git(work, "update-ref", "refs/heads/main", r["landing"]["sha"])
        base.git(work, "reset", "-q", "--hard", r["landing"]["sha"])
        # A worker that moved after declaring READY.
        worker = Path(r["sessions"]["claude_worker"]["worktree"])
        base.git(worker, "checkout", "-q", "--detach", moved)
        refused("a worker HEAD past its READY", "ready_declarations_survive_transfer")
        base.git(worker, "checkout", "-q", "--detach", r["landing"]["sha"])
        # The job's own output is read: a skipped validation prints nothing.
        stdout = Path(r["handoff"]["job"]["stdout"])
        stdout.write_text("")
        refused("an empty job stdout", "job_checks_exact_commit")
        stdout.write_text("both worker changes validated\n")
        if not validate(r)["passed"]:
            raise AssertionError("synthetic control did not restore: " + str(validate(r)["errors"]))
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
    receipt = json.loads(args.receipt.read_text())
    result = self_test(receipt) if args.self_test else validate(receipt)
    print(json.dumps(result, indent=2))
    return 0 if args.self_test or result["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
