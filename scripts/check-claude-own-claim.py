#!/usr/bin/env python3
"""Independently check a qualify-claude-own-claim.py receipt against its evidence.

THE own_claim_warn TIER'S CHECKER (D-235). A Claude build earns the tier only
if a worker the adapter launched with `"own_claim": "warn"` gets the gate's
BUDDY UNCLAIMED line where the MODEL reads it, bound to the call it is about,
and the warn leaves the decision with the operator. The runner's `checks` are
required complete and all true, and are never sufficient: every verdict is
decided again from the run's files, which the runner hash-binds
(`evidence_sha256`, required here):

- the launch exactly as the suite writes it, `own_claim: "warn"` included, and
  the captured receipt carrying the same posture (the hook rereads it per call,
  so a receipt that says off would have run off whatever launch.json says);
- the provider log: for each request, every line holding the lead, wherever
  the loopback recorder found it, and every tool_result's position. A line
  counts only inside the messages and bound to ONE tool result (inside it, or
  a later sibling block of its message with no other result between: Claude
  Code's placement in manual stream-json mode is unmeasured, D-229), naming
  that call's own path and this run's worktree. It must appear in the request
  right after each uncovered result, in none before the first, and never bound
  to the peer-held or the covered call (Codex design F2);
- the hook frames in the native trace: one PreToolUse hook per call, exit 0,
  before that call's permission ask, with the gate's own document (holder
  deny, nothing, warn, warn; a warn with no permissionDecision). The adapter's
  stdout carries no hook event, so this is the only record of it;
- three approvals from the adapter's events, bound by approval id, tool id and
  path, in order, answered allow, allow, deny; nothing asked for the peer-held
  path; the files written, written, absent; the ledger's two claims as set.

A same-length substitute for the lead, a line moved out of the message
content, one tied to the wrong result, a skipped approval or an ignored deny is
each refused by a named check (the self-test proves each). What the tier does
NOT show is the receipt's `unqualified` list, required here too.

Run: python3 -I scripts/check-claude-own-claim.py <root>/result.json [--candidate H:V:SHA] [--self-test]
     python3 -I scripts/check-claude-own-claim.py --self-test   (synthetic, hermetic)
"""
import importlib.util
import json
from pathlib import Path
import sys

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("start_tier", Path(__file__).with_name("check-claude-gate.py"))
g = importlib.util.module_from_spec(spec)
spec.loader.exec_module(g)

SCHEMA = "buddy.claude-own-claim.v1"
LEAD = "BUDDY UNCLAIMED:"  # internal/cli/ownclaim.go's ownClaimLead, written down here apart from the runner
SYSTEM_PREFIX = "PreToolUse:Write hook additional context: " + LEAD  # as the native wrote it (D-235, measured)
# (file, content, operator decision); None: the gate denies and nobody is asked.
CALLS = (("peer.txt", "peer-write\n", None), ("covered.txt", "covered-control\n", "allow"),
         ("uncovered-allowed.txt", "uncovered-allowed\n", "allow"), ("uncovered-denied.txt", "must-not-exist\n", "deny"))
IDS = tuple("toolu_buddy_%d" % n for n in range(len(CALLS)))
UNCOVERED = (2, 3)
REPLIES = ("operator-allow-covered", "operator-allow-uncovered", "operator-deny-uncovered")
DENIAL = 'peer.txt is inside scope "peer.txt"'
OPERATOR_DENIAL = "Denied by the operator control interface"
WAITING = {"state": "observed", "detail": "waiting_for_approval"}
SUITE_CHECKS = {
    "captured_native_identity", "handshake_without_inference", "receipt_launch_own_claim_warn", "gate_honours_warn",
    "wake_delivered", "operator_allow_covered_waiting", "operator_allow_covered_submitted",
    "operator_allow_uncovered_waiting", "operator_allow_uncovered_submitted", "operator_deny_uncovered_waiting",
    "operator_deny_uncovered_submitted", "approvals_bound_in_order", "turn_completed", "exactly_three_approvals",
    "peer_write_denied", "covered_written", "uncovered_allowed_written", "uncovered_denied_absent",
    "provider_request_shape", "line_after_uncovered_allowed", "line_after_uncovered_denied", "no_line_before_uncovered",
    "every_line_bound_to_an_uncovered_result", "fixture_only_expected_requests",
    "adapter_exited_successfully", "adapter_stdout_fully_parsed", "observed_native_children_exited",
    "hook_succeeded_before_each_approval", "hook_documents_as_expected"}
# What the tier must say it does not show (a substring of some entry each).
UNQUALIFIED = ("Codex workers", "auto mode", "any Claude build other than", "ACTS on the line",
               "real provider inference", "interactive (non stream-json) manual mode", "subagents",
               "Edit and NotebookEdit", "several uncovered paths")
# The files the runner hashes (its EVIDENCE), written down twice on purpose: a
# file this checker reads that the runner does not hash fails evidence_hashes.
EVIDENCE = ("adapter-events.json", "adapter.stderr", "launch.json", "settings.json", "source-manifest.json",
            "worker.json", "worker.json.events", "worker.json.proc", "provider-observations.json",
            "ledger-inspection.json", "unparsed-output.json", "native-observations.jsonl",
            "bin/claude-observer", "work/peer.txt", "work/covered.txt", "work/uncovered-allowed.txt")


def warn_text(name, work):
    """The gate's warn line for one uncovered path (ownclaim.go's format)."""
    return ("%s no claim of this session covers %s (in %s). Allowed, but a peer can claim it unaware of you. "
            "Claim it: buddy claim <slug> --scope %s (A subagent: tell your parent.)" % (LEAD, name, work, name))


def names(line, n, work):
    """The line holds the lead itself and names call n's path in this run."""
    return LEAD in line and ("covers %s (in %s" % (CALLS[n][0], work)) in line and \
        all(CALLS[m][0] not in line for m in UNCOVERED if m != n)


def binding(occ, request):
    """The tool_use_id one recorded occurrence belongs to, or None.

    Its recorded fields must agree with its JSON path (so an edited
    tool_use_id or block index alone is a disagreement, not a re-binding):
    messages/<i>/content/<j>/..., block j's type, and for a line inside a
    tool_result, a recorded result at exactly that position with that id.
    Outside the messages, in a string content, or before any result: None."""
    path, results = occ["path"], request["tool_results"]
    if occ["role"] == "system":
        return system_binding(occ, path, results)
    if len(path) < 4 or path[0] != "messages" or path[2] != "content":
        return None
    i, j = path[1], path[3]
    # Only where the model reads it (Codex code pass F1: a line in a
    # tool_result's `metadata` bound like one in its content): a
    # tool_result's string content or one of its text blocks, or a sibling
    # text block's text. Anywhere else binds to nothing, so it fails.
    tail = path[4:]
    if not (tail == ["content"] or (len(tail) == 3 and tail[0] == "content" and isinstance(tail[1], int) and tail[2] == "text")
            if occ["block_type"] == "tool_result" else occ["block_type"] == "text" and tail == ["text"]):
        return None
    if occ["message"] != i or occ["block"] != j or occ["in_tool_result"] != (occ["block_type"] == "tool_result"):
        raise ValueError("an occurrence's position disagrees with its path")
    here = [x for x in results if x["message"] == i and x["block"] == j]
    if occ["in_tool_result"]:
        if [x["tool_use_id"] for x in here] != [occ["tool_use_id"]]:
            raise ValueError("an occurrence inside a tool_result the request does not hold there")
        return occ["tool_use_id"]
    if here or occ["tool_use_id"] is not None:
        raise ValueError("a sibling occurrence recorded as a tool result")
    before = [x for x in results if x["message"] == i and x["block"] < j]
    return max(before, key=lambda x: x["block"])["tool_use_id"] if before else None


def system_binding(occ, path, results):
    """D-235's measured placement (n=1, Claude 2.1.312, manual stream-json):
    the native hands the line over as its OWN system-role message right after
    the message holding the tool_result, its content a string (when the
    history is re-sent) or one text block, the line prefixed with the hook's
    event and tool. Only that binds: message m = the result's message + 1,
    role system, content or block 0's text, the Write hook's prefix. Two
    messages after, a later block, another tool's prefix: None (the first
    run failed on the same-message rule alone, so nothing wider is taken)."""
    if len(path) < 3 or path[0] != "messages" or path[2] != "content" or path[3:] not in ([], [0, "text"]):
        return None
    if (occ["message"] != path[1] or occ["block"] != (None if len(path) == 3 else 0) or occ["in_tool_result"] or
            occ["tool_use_id"] is not None or occ["block_type"] != ("string" if len(path) == 3 else "text")):
        raise ValueError("a system occurrence's position disagrees with its path")
    prior = [x for x in results if x["message"] == path[1] - 1]
    if len(prior) != 1 or any(x["message"] == path[1] for x in results) or not occ["line"].startswith(SYSTEM_PREFIX):
        return None
    return prior[0]["tool_use_id"]


def validate(r, receipt_path):
    e = g.Evidence(r, r["root"])
    version = str(r.get("native_version", "")).removesuffix(" (Claude Code)")
    work = e.root / "work"
    ev = lambda: e.json("adapter-events.json")
    launch = lambda: e.json("launch.json")
    gate = lambda: g.parse_gate(launch()["gate_command"])
    requests = lambda: e.json("provider-observations.json")
    trace = lambda: e.lines("native-observations.jsonl")
    def identity(): return e.json("worker.json")["identity"]
    def native(kind): return [v for v in ev() if v.get("native_event", {}).get("kind") == kind]
    def reply(rid):
        rows = [v for v in ev() if v.get("request_id") == rid]
        if len(rows) != 1:
            raise ValueError("want one reply to " + rid)
        return rows[0]
    def outputs():
        rows = {}
        for request in requests():
            for x in request["tool_outputs"]:
                rows.setdefault(x["tool_use_id"], x)
        return rows
    def native_path(): return g.observer_native(e, "bin/claude-observer")[0]

    e.check("receipt_location", lambda: g.located(e, receipt_path))
    e.check("contract", lambda: r["schema"] == SCHEMA and g.admission.need("claude", version, e.root, r["native_sha256"]) and
            r["native_version"] == version + " (Claude Code)" and g.hex64(r["native_sha256"]) and "loopback" in r["provider"] and
            launch()["harness_version"] == version and launch()["hooks"]["mods"] == "builtin-only-" + version and
            all(any(w in u for u in r["unqualified"]) for w in UNQUALIFIED) and
            r["inherited_own_claim"] == "off")
    e.check("runner_verdict", lambda: g.runner_verdict(r, "passed", SUITE_CHECKS))
    e.check("artifact_hashes", lambda: e.sha(gate()[0]) == r["adapter_sha256"] and e.sha(gate()[2]) == r["buddy_sha256"] and
            e.sha("source-manifest.json") == r["source_manifest_sha256"] and e.sha("settings.json") == r["settings_sha256"] and
            e.sha("bin/claude-observer") == r["observer_sha256"] and e.sha(native_path()) == r["native_sha256"] and
            g.observer_native(e, "bin/claude-observer")[1] == [])
    # The posture is the suite: launch.json exactly as the runner writes it.
    e.check("launch", lambda: launch() == {
        "worktree": str(work), "permission_mode": "workspace-write", "hook_config": str(e.root / "settings.json"),
        "harness_version": version, "gate_command": launch()["gate_command"], "own_claim": "warn",
        "hooks": {"configured": "observed", "trusted": "unknown", "observed": "unknown", "mods": "builtin-only-" + version}} and
        gate()[1] == str(e.root / "worker.json") and e.json("settings.json") == g.gate_settings(launch()["gate_command"]))
    # The hook rereads the RECEIPT's posture on every call (hook.go).
    def captured():
        saved = e.json("worker.json")
        start = [v for v in ev() if v.get("operation") == "start"]
        return (g.valid_identity(identity(), "claude") and len(start) == 1 and start[0]["identity"] == identity() and
                not start[0].get("error") and
                all(saved["launch"][k] == launch()[k] for k in
                    ("gate_command", "worktree", "hook_config", "harness_version", "permission_mode", "own_claim")) and
                saved["launch"]["hooks"]["mods"] == launch()["hooks"]["mods"] and
                g.same_identity(identity(), ev()) and g.same_identity(identity(), e.lines("worker.json.events")))
    e.check("captured_identity", captured)
    # The claims themselves are not re-read from the ledger (no raw open of a
    # ledger file): the hook documents prove them in force at each call (the
    # peer deny on peer.txt, silence on the covered call, the line on the two
    # uncovered ones), and the runner's `buddy claim` refuses on failure.
    e.check("ledger", lambda: e.json("ledger-inspection.json")["identity"] == {k: identity()[k] for k in ("session_id", "incarnation")} and
        e.json("ledger-inspection.json")["worktree"] == str(work) and "warn" in e.json("ledger-inspection.json")["gate"]["own_claim"])
    # Ids bound to paths by the native's own record of the calls, and one
    # request per tool result, each holding exactly the results before it.
    # Every traced frame that names a session names the captured worker's
    # (Codex code pass F4: a trace rehashed under another session passed).
    e.check("trace_session", lambda: all(v["session_id"] == identity()["session_id"] for v in trace() if "session_id" in v) and
            any("session_id" in v for v in trace()))
    e.check("tool_calls", lambda: [(b["id"], b["path"]) for v in trace() if v.get("type") == "assistant"
                                   for b in v.get("tool_blocks") or [] if b.get("type") == "tool_use"] ==
            [(IDS[n], str(work / c[0])) for n, c in enumerate(CALLS)] and
            [len(x["tool_outputs"]) for x in requests()] == list(range(len(CALLS) + 1)) and
            all(x["tool_result_ids"] == list(IDS[:k]) and [y["tool_use_id"] for y in x["tool_results"]] == list(IDS[:k])
                for k, x in enumerate(requests())) and r["provider_request_count"] == len(CALLS) + 1)
    e.check("peer_denial", lambda: e.text("work/peer.txt") == "original\n" and outputs()[IDS[0]].get("is_error") is True and
            DENIAL in str(outputs()[IDS[0]]["content"]) and
            not any(v["native_event"]["approval"]["detail"]["input"]["file_path"] == str(work / "peer.txt") for v in native("approval")))
    e.check("writes", lambda: all(e.text("work/" + CALLS[n][0]) == CALLS[n][1] and not outputs()[IDS[n]].get("is_error") and
                                  str(work / CALLS[n][0]) in str(outputs()[IDS[n]]["content"]) for n in (1, 2)))
    e.check("operator_denial", lambda: e.absent("work/" + CALLS[3][0]) and outputs()[IDS[3]].get("is_error") is True and
            OPERATOR_DENIAL in str(outputs()[IDS[3]]["content"]))

    def approvals():
        rows, results = native("approval"), native("result")
        wake = reply("wake-1")["wake_delivery"]
        if len(rows) != 3 or len(results) != 1:
            return False
        events = ev()
        bound = []
        for k, (name, _, decision) in enumerate(CALLS[1:]):
            a = rows[k]["native_event"]["approval"]
            rep = reply(REPLIES[k])
            after = events.index(rows[k + 1]) if k < 2 else events.index(results[0])
            if not (rows[k]["state"] == WAITING and a["item_id"] == IDS[k + 1] and
                    a["detail"]["input"]["file_path"] == str(work / name) and a["turn_id"] == wake["turn_id"] and
                    rep["approval_response"] == {"id": a["id"], "native_session_id": a["native_session_id"],
                                                 "turn_id": a["turn_id"], "decision": decision} and
                    rep["approval_delivery"]["state"] == "submitted" and
                    events.index(rows[k]) < events.index(rep) < after):
                return False
            bound.append({"id": a["id"], "item_id": a["item_id"], "path": str(work / name), "decision": decision})
        return r["approvals"] == bound and len({x["id"] for x in bound}) == 3
    e.check("approvals", approvals)

    def turn():
        wake = reply("wake-1")["wake_delivery"]
        results, idles = native("result"), native("idle")
        return (wake["state"] == "delivered" and bool(wake["turn_id"]) and len(results) == len(idles) == 1 and
                results[0]["native_event"]["turn_id"] == idles[0]["native_event"]["turn_id"] == wake["turn_id"] and
                ev().index(reply("wake-1")) < ev().index(results[0]) < ev().index(idles[0]) and
                idles[0]["state"]["detail"] == "idle" and idles[0]["pending_approval"]["state"] == "observed_absent")
    e.check("turn", turn)

    # The hook, from the trace: one PreToolUse frame per call, exit 0, after
    # the previous call's result and before this call's ask (the denied peer
    # call: before its result, with no ask at all).
    def hook_frames():
        rows = trace()
        def result_at(n):
            at = [i for i, v in enumerate(rows) if v.get("type") == "user" and
                  any(b.get("type") == "tool_result" and b.get("id") == IDS[n] for b in v.get("tool_blocks") or [])]
            if len(at) != 1:
                raise ValueError("want one result frame for " + IDS[n])
            return at[0]
        asks = [(i, v["request"]) for i, v in enumerate(rows) if v.get("type") == "control_request" and
                v["request"].get("subtype") == "can_use_tool"]
        # Bound by tool too (Codex code pass F3: a Read ask on a Write's id passed).
        if [(a["tool_name"], a["tool_use_id"], a["path"]) for _, a in asks] != [("Write", IDS[n], str(work / CALLS[n][0])) for n in (1, 2, 3)]:
            raise ValueError("the asks are not exactly the three approved calls, in order")
        ask_at = {a["tool_use_id"]: i for i, a in asks}
        frames = []
        for n, (_, _, decision) in enumerate(CALLS):
            start = result_at(n - 1) if n else 0
            end = ask_at[IDS[n]] if decision else result_at(n)
            if decision and not end < result_at(n):
                raise ValueError("an ask after its call's result")
            hooks = [v for v in rows[start:end] if v.get("type") == "system" and v.get("subtype") == "hook_response" and
                     v.get("hook_event") == "PreToolUse"]
            if len(hooks) != 1 or hooks[0].get("exit_code") != 0 or hooks[0].get("outcome", "success") != "success":
                raise ValueError("want one successful PreToolUse hook before " + IDS[n] + "'s ask")
            frames.append(hooks[0])
        if len([v for v in rows if v.get("subtype") == "hook_response" and v.get("hook_event") == "PreToolUse"]) != len(CALLS):
            raise ValueError("a PreToolUse hook frame outside the four calls")
        return frames
    e.check("hook_order", lambda: len(hook_frames()) == len(CALLS))

    def documents():
        frames = hook_frames()
        def doc(n): return json.loads(frames[n]["stdout"])["hookSpecificOutput"]
        deny = doc(0)
        return (deny["permissionDecision"] == "deny" and deny["permissionDecisionReason"].startswith(DENIAL) and
                LEAD not in frames[0]["stdout"] and frames[1]["stdout"].strip() == "" and
                all(set(doc(n)) == {"hookEventName", "additionalContext"} and doc(n)["hookEventName"] == "PreToolUse" and
                    doc(n)["additionalContext"].startswith(LEAD) and names(doc(n)["additionalContext"], n, work)
                    for n in UNCOVERED))
    e.check("hook_documents", documents)

    # Where the model was handed the line: decided from the recorder's
    # positions, never from the runner's line checks.
    def bound_lines(k):
        request = requests()[k]
        return [(binding(o, request), o) for o in request["lead_occurrences"]]
    e.check("lines_absent_before_uncovered", lambda: all(not requests()[k]["lead_occurrences"] for k in range(UNCOVERED[0] + 1)))
    # The line must carry the hook's own additionalContext verbatim, not a
    # text that merely starts and names alike (Codex code pass F2: "no claim"
    # respelled "a  claim" in the request passed).
    def emitted(n): return json.loads(hook_frames()[n]["stdout"])["hookSpecificOutput"]["additionalContext"]
    def after(n):
        rows = [o for tid, o in bound_lines(n + 1) if tid == IDS[n]]
        return bool(rows) and all(names(o["line"], n, work) and emitted(n) in o["line"] and o["count"] >= 1 for o in rows)
    e.check("line_after_uncovered_allowed", lambda: after(2))
    e.check("line_after_uncovered_denied", lambda: after(3))
    def every_line():
        total = 0
        for k, request in enumerate(requests()):
            for tid, o in bound_lines(k):
                if (tid not in (IDS[2], IDS[3]) or tid not in request["tool_result_ids"] or not names(o["line"], IDS.index(tid), work) or
                        emitted(IDS.index(tid)) not in o["line"]):
                    return False
                total += 1
        return total >= 2
    e.check("lines_bound", every_line)

    e.check("clean_close", lambda: r["adapter_exit"] == 0 and r["adapter_forced_cleanup"] == [] and
            e.json("unparsed-output.json") == [] and g.close_line(e, "adapter.stderr") and
            g.proc_record(e, "worker.json.proc", identity(), e.root / "worker.json", e.root / "bin/claude-observer") and
            g.child_exit(e, r["cleanup_observation"], "native-observations.jsonl"))
    bound = {}
    e.check("evidence_hashes", lambda: bound.setdefault("v", g.evidence_binding(e, r, True)))
    return e.verdict(evidence=bound.get("v"), read=sorted(e.read_paths))


def receipt_mutations(r):
    return [(("passed",), False, "runner_verdict"),
            (("checks", "line_after_uncovered_denied"), False, "runner_verdict"),
            (("checks", "hook_documents_as_expected"), g.DELETE, "runner_verdict"),
            (("failure",), "boom", "runner_verdict"),
            (("cleanup_failure",), "adapter would not exit", "runner_verdict"),
            (("schema",), "buddy.claude-own-claim.v0", "contract"),
            (("native_version",), "2.1.292 (Claude Code)", "contract"),
            (("unqualified",), [u for u in r["unqualified"] if "Codex" not in u], "contract"),
            (("unqualified",), [u for u in r["unqualified"] if "ACTS" not in u], "contract"),
            (("unqualified",), [u for u in r["unqualified"] if "subagents" not in u], "contract"),
            (("inherited_own_claim",), g.DELETE, "contract"),
            (("inherited_own_claim",), "warn", "contract"),
            (("native_sha256",), "0" * 64, "artifact_hashes"),
            (("adapter_sha256",), "0" * 64, "artifact_hashes"),
            (("buddy_sha256",), "0" * 64, "artifact_hashes"),
            (("settings_sha256",), "0" * 64, "artifact_hashes"),
            (("observer_sha256",), "0" * 64, "artifact_hashes"),
            (("approvals", 2, "decision"), "allow", "approvals"),
            (("provider_request_count",), 6, "tool_calls"),
            # The first native run (D-235): the runner never wrote the key, so every
            # real receipt failed here; the synthetic receipt always carried it.
            (("provider_request_count",), g.DELETE, "tool_calls"),
            (("adapter_exit",), 1, "clean_close"),
            (("evidence_sha256", "provider-observations.json"), "0" * 64, "evidence_hashes"),
            (("evidence_sha256",), g.DELETE, "evidence_hashes"),
            (("root",), str(Path(r["root"]) / "work"), "receipt_location")]


def synthetic(root, tag, form="sibling"):
    """A coherent SYNTHETIC own-claim run under root (F-33): real files read
    the way a native run's are. Tests the CHECKER; qualifies no native harness.
    The uncovered-allowed line sits in a sibling block after its result, the
    uncovered-denied line inside its result: both placements the checker binds."""
    root = Path(root)
    root.mkdir(parents=True)
    version = g.CLAUDE_VERSIONS[-1]
    work = root / "work"
    sid, inc = "%s1111111-2222-4333-8444-555555555555" % tag, "inc-" + tag
    identity = {"schema": "buddy.lifecycle.v1", "harness": "claude", "session_id": sid, "incarnation": inc}
    for name, body in (("bin/buddy", "buddy " + tag), ("bin/buddy-adapter", "adapter " + tag), ("bin/claude", "claude native")):
        g.write(root, name, body)
    receipt = root / "worker.json"
    gate = "'%s' hook --receipt '%s' --buddy '%s'" % (root / "bin/buddy-adapter", receipt, root / "bin/buddy")
    g.write(root, "settings.json", g.gate_settings(gate))
    launch = {"worktree": str(work), "permission_mode": "workspace-write", "hook_config": str(root / "settings.json"),
              "harness_version": version, "gate_command": gate, "own_claim": "warn",
              "hooks": {"configured": "observed", "trusted": "unknown", "observed": "unknown", "mods": "builtin-only-" + version}}
    g.write(root, "launch.json", launch)
    g.write(root, "source-manifest.json", {"go.mod": "0" * 64})
    g.write(root, "bin/claude-observer", g.observer_source(root / "bin/claude"))
    g.write(root, "worker.json", {"schema": "buddy.control.v1", "identity": identity, "launch": launch})
    g.write(root, "work/peer.txt", "original\n")
    for name, body, _ in CALLS[1:3]:
        g.write(root, "work/" + name, body)
    turn = "turn-1-" + tag
    def event(op, **fields):
        return dict({"schema": "buddy.control.v1", "operation": op, "identity": identity, "state": {"state": "unknown"},
                     "completion": {"state": "unknown"}, "pending_approval": {"state": "unknown"},
                     "capabilities": {"telemetry": {"support": "unknown"}}}, **fields)
    def nat(kind, **fields):
        extra = {"approval": {"state": WAITING}, "idle": {"state": {"state": "observed", "detail": "idle"},
                                                          "pending_approval": {"state": "observed_absent"}}}.get(kind, {})
        return event("event", native_event=dict({"kind": kind, "turn_id": turn, "native_session_id": sid}, **fields), **extra)
    def ask(n, aid):
        return nat("approval", approval={"id": aid, "native_session_id": sid, "turn_id": turn, "item_id": IDS[n],
                                         "detail": {"input": {"file_path": str(work / CALLS[n][0])}}})
    events, journal, approvals = [event("start"), dict(event("send"), request_id="wake-1",
                                                       wake_delivery={"state": "delivered", "turn_id": turn}), nat("busy")], [], []
    journal += events
    for k, rid in enumerate(REPLIES):
        n, aid = k + 1, "ap-%d-%s" % (k + 1, tag)
        decision = {"id": aid, "native_session_id": sid, "turn_id": turn, "decision": CALLS[n][2]}
        events += [ask(n, aid), dict(event("approve"), request_id=rid, approval_response=decision,
                                     approval_delivery={"state": "submitted", "detail": "response written; native resolution pending"})]
        journal += [events[-2], dict(events[-1], approval_delivery={"state": "unknown", "detail": "approval response intent"}),
                    events[-1]]
        approvals.append({"id": aid, "item_id": IDS[n], "path": str(work / CALLS[n][0]), "decision": CALLS[n][2]})
    events += [nat("result", result_ref="res-1", result_status="completed"), nat("idle")]
    journal += events[-2:]
    g.write(root, "adapter-events.json", events)
    g.write(root, "worker.json.events", g.jsonl(journal))
    g.write(root, "worker.json.proc", g.proc_source(receipt, identity, root / "bin/claude-observer"))
    g.write(root, "adapter.stderr", g.close_report())
    g.write(root, "unparsed-output.json", [])
    g.write(root, "ledger-inspection.json", {"schema": "buddy.adapter.v1", "operation": "inspect",
                                             "identity": {"session_id": sid, "incarnation": inc}, "worktree": str(work),
                                             "gate": {"own_claim": ["off", "warn", "deny"]}})
    # The provider log: message 2n+2 is the user turn carrying result n.
    out = [{"type": "tool_result", "tool_use_id": IDS[0], "is_error": True,
            "content": "PreToolUse:Write hook error: " + DENIAL + " claimed by session work/s-fixtureh"},
           {"type": "tool_result", "tool_use_id": IDS[1], "content": "File created successfully at: %s" % (work / CALLS[1][0])},
           {"type": "tool_result", "tool_use_id": IDS[2], "content": "File created successfully at: %s" % (work / CALLS[2][0])},
           {"type": "tool_result", "tool_use_id": IDS[3], "is_error": True, "content": OPERATOR_DENIAL}]
    sibling = {"message": 6, "role": "user", "block": 1, "block_type": "text", "in_tool_result": False, "tool_use_id": None,
               "path": ["messages", 6, "content", 1, "text"], "count": 1,
               "line": "PreToolUse:Write hook additional context: " + warn_text(CALLS[2][0], work)}
    inside = {"message": 8, "role": "user", "block": 0, "block_type": "tool_result", "in_tool_result": True,
              "tool_use_id": IDS[3], "path": ["messages", 8, "content", 0, "content", 1, "text"], "count": 1,
              "line": warn_text(CALLS[3][0], work)}
    lines = {3: [sibling], 4: [sibling, inside]}
    at = lambda n: 2 * n + 2
    if form == "measured":
        # D-235's first native run, its numbering kept: result n at message
        # 3n+3, each line its own system message right after its result, the
        # earlier one re-sent as a string in the next request.
        at = lambda n: 3 * n + 3
        def system(n, string):
            path = ["messages", at(n) + 1, "content"] + ([] if string else [0, "text"])
            return {"message": at(n) + 1, "role": "system", "block": None if string else 0,
                    "block_type": "string" if string else "text", "in_tool_result": False, "tool_use_id": None,
                    "path": path, "count": 1, "line": SYSTEM_PREFIX[:-len(LEAD)] + warn_text(CALLS[n][0], work)}
        lines = {3: [system(2, False)], 4: [system(2, True), system(3, False)]}
    requests = []
    for k in range(len(CALLS) + 1):
        results = [{"tool_use_id": IDS[n], "message": at(n), "block": 0} for n in range(k)]
        requests.append({"path": "/v1/messages", "model": "claude-fixture", "stream": True, "tool_outputs": out[:k],
                         "tools": ["Write"], "lead_occurrences": lines.get(k, []), "tool_results": results,
                         "tool_result_ids": list(IDS[:k])})
    g.write(root, "provider-observations.json", requests)
    def doc(**fields): return json.dumps({"hookSpecificOutput": dict({"hookEventName": "PreToolUse"}, **fields)}) + "\n"
    stdouts = [doc(permissionDecision="deny", permissionDecisionReason=DENIAL + " claimed by session work/s-fixtureh"), "",
               doc(additionalContext=warn_text(CALLS[2][0], work)), doc(additionalContext=warn_text(CALLS[3][0], work))]
    rows = [{"type": "buddy_fixture_native_process", "pid": 501}, {"type": "buddy_fixture_native_process", "pid": 503},
            {"type": "system", "subtype": "init", "session_id": sid, "plugins": g.BUILTIN_MODS},
            {"type": "user", "session_id": sid, "isReplay": True, "parent_tool_use_id": None}]
    for n, (name, _, decision) in enumerate(CALLS):
        rows.append({"type": "assistant", "session_id": sid, "parent_tool_use_id": None, "tool_blocks": [
            {"type": "tool_use", "id": IDS[n], "name": "Write", "is_error": None, "path": str(work / name)}]})
        rows.append({"type": "system", "subtype": "hook_started", "hook_event": "PreToolUse", "hook_name": "PreToolUse:Write"})
        rows.append({"type": "system", "subtype": "hook_response", "hook_event": "PreToolUse", "hook_name": "PreToolUse:Write",
                     "outcome": "success", "exit_code": 0, "stdout": stdouts[n], "stdout_chars": len(stdouts[n])})
        if decision:
            rows.append({"type": "control_request", "request": {"subtype": "can_use_tool", "tool_name": "Write",
                                                                "tool_use_id": IDS[n], "path": str(work / name)}})
        rows.append({"type": "user", "session_id": sid, "parent_tool_use_id": None, "tool_blocks": [
            {"type": "tool_result", "id": IDS[n], "name": None, "is_error": True if n in (0, 3) else "<absent>", "path": None}]})
    rows.append({"type": "result", "subtype": "success", "session_id": sid})
    g.write(root, "native-observations.jsonl", g.jsonl(rows))
    r = {"schema": SCHEMA, "root": str(root), "passed": True, "checks": {k: True for k in SUITE_CHECKS},
         "provider": "deterministic loopback Messages fixture; no real inference",
         "unqualified": ["Codex workers (a Codex warn is refused at launch, D-235)",
                         "auto mode and every permission mode but the adapter's manual stdio prompts",
                         "any Claude build other than the one this run bound",
                         "whether the model ACTS on the line (D-228 D10's counts)",
                         "real provider inference", "interactive (non stream-json) manual mode",
                         "subagents (the adapter hook refuses an agent_id payload)",
                         "Edit and NotebookEdit (only Write is exercised)", "a call naming several uncovered paths"],
         "inherited_own_claim": "off",
         "native_version": version + " (Claude Code)",
         "native_sha256": g.admission.synthetic("claude", version, root / "bin/claude", root),
         "source_manifest_sha256": g.sha_file(root / "source-manifest.json"), "buddy_sha256": g.sha_file(root / "bin/buddy"),
         "adapter_sha256": g.sha_file(root / "bin/buddy-adapter"), "settings_sha256": g.sha_file(root / "settings.json"),
         "observer_sha256": g.sha_file(root / "bin/claude-observer"), "approvals": approvals,
         "provider_request_count": len(requests), "adapter_exit": 0, "adapter_forced_cleanup": [],
         "cleanup_observation": {"status": "observed_absent", "observed_pids": [501, 503], "remaining_or_unknown_pids": []}}
    r["evidence_sha256"] = {name: g.sha_file(root / name) for name in EVIDENCE}
    g.write(root, "result.json", r)
    return r


def file_mutations(root, r):
    root = Path(root)
    work = root / "work"

    def edit(name, fn, jsonl=False):
        """Damage one JSON evidence file and re-hash it in the receipt, so the
        semantic guard is what refuses, not the hash."""
        def run():
            path = root / name
            value = [json.loads(x) for x in path.read_text().splitlines() if x.strip()] if jsonl else json.loads(path.read_text())
            fn(value)
            g.write(root, name, g.jsonl(value) if jsonl else value)
            return g.mutate(r, ("evidence_sha256", name), g.sha_file(path))
        return run
    def occ(requests, k, i): return requests[k]["lead_occurrences"][i]
    def to_metadata(requests):
        # The path forged to mimic a message block, its fields left as they
        # were: only the path's root says it is outside the messages.
        for k in (3, 4):
            o = occ(requests, k, 0)
            o["path"] = ["metadata"] + o["path"][1:]
    def substitute(requests):
        for k in (3, 4):
            o = occ(requests, k, 0)
            o["line"] = o["line"].replace(LEAD, "BUDDY UNCLAIMEX:")
    def earlier(requests):
        requests[2]["lead_occurrences"].append(dict(occ(requests, 3, 0), message=4, block=1, path=["messages", 4, "content", 1, "text"]))
    def inside_result(n, line_of):
        def fn(requests):
            requests[4]["lead_occurrences"].append({"message": 2 * n + 2, "role": "user", "block": 0, "block_type": "tool_result",
                "in_tool_result": True, "tool_use_id": IDS[n], "path": ["messages", 2 * n + 2, "content", 0, "content"],
                "count": 1, "line": warn_text(CALLS[line_of][0], work)})
        return fn
    def wrong_result(requests):
        occ(requests, 4, 1).update(message=6, block=0, tool_use_id=IDS[2], path=["messages", 6, "content", 0, "content"])
    def relabel(requests):
        occ(requests, 4, 1)["tool_use_id"] = IDS[2]
    def misplaced(requests):
        occ(requests, 3, 0)["message"] = 4
    def fourth_ask(events):
        at = [i for i, v in enumerate(events) if v.get("request_id") == REPLIES[2]][0]
        a = json.loads(json.dumps(events[at - 1]))
        a["native_event"]["approval"]["id"] = "ap-fourth"
        events.insert(at + 1, a)
    def twice(rows):
        h = hooks(rows)[2]
        rows.insert(rows.index(h), dict(h))
    def drop_ask(n):
        def fn(events):
            at = [i for i, v in enumerate(events) if v.get("native_event", {}).get("approval", {}).get("item_id") == IDS[n]][0]
            del events[at:at + 2]
        return fn
    def peer_ask(events):
        a = json.loads(json.dumps(events[3]))
        a["native_event"]["approval"].update(id="ap-peer", item_id=IDS[0], detail={"input": {"file_path": str(work / "peer.txt")}})
        events.insert(3, a)
    def deny_as_allow(events):
        [v for v in events if v.get("request_id") == REPLIES[2]][0]["approval_response"]["decision"] = "allow"
    def posture(name, word, nested=False):
        def fn(value):
            (value["launch"] if nested else value)["own_claim"] = word
        return edit(name, fn)
    def hooks(rows): return [v for v in rows if v.get("subtype") == "hook_response"]
    def drop_hook(rows):
        rows.remove(hooks(rows)[2])
    def hook_late(rows):
        h = hooks(rows)[2]
        ask = [v for v in rows if v.get("type") == "control_request" and v["request"]["tool_use_id"] == IDS[2]][0]
        rows.remove(h)
        rows.insert(rows.index(ask) + 1, h)
    def decided_warn(rows):
        h = hooks(rows)[2]
        d = json.loads(h["stdout"])
        d["hookSpecificOutput"]["permissionDecision"] = "allow"
        h["stdout"] = json.dumps(d)
    def covered_warned(rows):
        hooks(rows)[1]["stdout"] = json.dumps({"hookSpecificOutput": {"hookEventName": "PreToolUse",
                                                                      "additionalContext": warn_text(CALLS[1][0], work)}})
    def trace_peer_ask(rows):
        at = rows.index(hooks(rows)[0]) + 1
        rows.insert(at, {"type": "control_request", "request": {"subtype": "can_use_tool", "tool_name": "Write",
                                                                "tool_use_id": IDS[0], "path": str(work / "peer.txt")}})
    def peer_written():
        g.write(root, "work/peer.txt", CALLS[0][1])
        return g.mutate(r, ("evidence_sha256", "work/peer.txt"), g.sha_file(work / "peer.txt"))
    def swap_ids(rows):
        uses = [b for v in rows if v.get("type") == "assistant" for b in v["tool_blocks"]]
        uses[1]["id"], uses[2]["id"] = uses[2]["id"], uses[1]["id"]
    # Codex code pass F1-F4, each folded with the guard it names.
    def block_metadata(requests):
        for k in (3, 4):
            o = occ(requests, k, len(requests[k]["lead_occurrences"]) - 1)
            o.update(message=8, block=0, block_type="tool_result", in_tool_result=True, tool_use_id=IDS[3],
                     path=["messages", 8, "content", 0, "metadata"])
    def words_changed(requests):
        for k in (3, 4):
            for o in requests[k]["lead_occurrences"]:
                o["line"] = o["line"].replace("no claim", "a  claim")
    def read_ask(rows):
        [v for v in rows if v.get("type") == "control_request"][1]["request"]["tool_name"] = "Read"
    def other_session(rows):
        for v in rows:
            if "session_id" in v:
                v["session_id"] = "00000000-0000-4000-8000-00000000beef"
    return [
        ("line in a tool_result's metadata", edit("provider-observations.json", block_metadata), "line_after_uncovered_denied"),
        ("the line's words changed, same length", edit("provider-observations.json", words_changed), "line_after_uncovered_allowed"),
        ("a Read ask on a Write's id and path", edit("native-observations.jsonl", read_ask, jsonl=True), "hook_order"),
        ("trace under another session", edit("native-observations.jsonl", other_session, jsonl=True), "trace_session"),
        ("line moved to metadata", edit("provider-observations.json", to_metadata), "line_after_uncovered_allowed"),
        ("same-length substitute for the lead", edit("provider-observations.json", substitute), "line_after_uncovered_allowed"),
        ("line in an earlier request", edit("provider-observations.json", earlier), "lines_absent_before_uncovered"),
        ("line on the covered call", edit("provider-observations.json", inside_result(1, 1)), "lines_bound"),
        ("line on the peer-held call", edit("provider-observations.json", inside_result(0, 0)), "lines_bound"),
        ("line tied to the wrong tool_result", edit("provider-observations.json", wrong_result), "line_after_uncovered_denied"),
        ("line's tool_use_id relabelled, path unchanged", edit("provider-observations.json", relabel), "lines_bound"),
        ("recorded message index disagrees with its path", edit("provider-observations.json", misplaced), "lines_bound"),
        ("no line after the uncovered-denied result", edit("provider-observations.json",
            lambda q: q[4]["lead_occurrences"].pop(1)), "line_after_uncovered_denied"),
        ("an extra provider request", edit("provider-observations.json", lambda q: q.append(dict(q[-1]))), "tool_calls"),
        ("approval skipped", edit("adapter-events.json", drop_ask(2)), "approvals"),
        ("approval asked for the peer-held path", edit("adapter-events.json", peer_ask), "approvals"),
        ("a fourth approval after the deny", edit("adapter-events.json", fourth_ask), "approvals"),
        ("deny answered allow", edit("adapter-events.json", deny_as_allow), "approvals"),
        ("deny ignored (denied file present)", lambda: g.write(root, "work/uncovered-denied.txt", CALLS[3][1]), "operator_denial"),
        ("uncovered-allowed write missing", lambda: (work / "uncovered-allowed.txt").unlink(), "writes"),
        ("peer-held file written", peer_written, "peer_denial"),
        ("own_claim off in launch.json", posture("launch.json", "off"), "launch"),
        ("own_claim deny in launch.json", posture("launch.json", "deny"), "launch"),
        ("own_claim off in the captured receipt", posture("worker.json", "off", nested=True), "captured_identity"),
        ("hook frame missing before an approval", edit("native-observations.jsonl", drop_hook, jsonl=True), "hook_order"),
        ("hook frame twice before one approval", edit("native-observations.jsonl", twice, jsonl=True), "hook_order"),
        ("hook frame after its approval", edit("native-observations.jsonl", hook_late, jsonl=True), "hook_order"),
        ("operator asked on the peer-held call", edit("native-observations.jsonl", trace_peer_ask, jsonl=True), "hook_order"),
        ("warn document carries a permissionDecision", edit("native-observations.jsonl", decided_warn, jsonl=True),
         "hook_documents"),
        ("covered call warned by the hook", edit("native-observations.jsonl", covered_warned, jsonl=True), "hook_documents"),
        ("tool ids swapped in the trace", edit("native-observations.jsonl", swap_ids, jsonl=True), "tool_calls"),
        ("gate does not honour warn", edit("ledger-inspection.json", lambda v: v["gate"].update(own_claim=["off", "deny"])),
         "ledger"),
        ("native child left running", lambda: g.mutate(r, ("cleanup_observation", "observed_pids"), [501]), "clean_close"),
        ("a read file left out of the hashes", lambda: g.mutate(r, ("evidence_sha256", "ledger-inspection.json"), g.DELETE),
         "evidence_hashes"),
    ]


SPLICE = [("worker.json.proc", "clean_close"), ("adapter-events.json", "captured_identity"),
          ("provider-observations.json", "writes")]


def measured_mutations(root, r):
    """D-235's measured placement (a system message right after the result),
    each way out of it that must not bind. The first four are orc-11's list."""
    root = Path(root)
    work = root / "work"
    def edit(fn):
        def run():
            path = root / "provider-observations.json"
            value = json.loads(path.read_text())
            fn(value[4]["lead_occurrences"][1], value)
            g.write(root, "provider-observations.json", value)
            return g.mutate(r, ("evidence_sha256", "provider-observations.json"), g.sha_file(path))
        return run
    def two_after(o, q):
        o.update(message=o["message"] + 1, path=["messages", o["message"] + 1, "content", 0, "text"])
    def user_role(o, q):
        o["role"] = "user"
    def other_file(o, q):
        o["line"] = o["line"].replace(CALLS[3][0], CALLS[2][0])
    def later_block(o, q):
        o.update(block=1, path=o["path"][:3] + [1, "text"])
    def edit_prefix(o, q):
        o["line"] = o["line"].replace("PreToolUse:Write ", "PreToolUse:Edit ")
    def no_prefix(o, q):
        o["line"] = o["line"][len(SYSTEM_PREFIX) - len(LEAD):]
    def resent_two_after(o, q):
        a = q[4]["lead_occurrences"][0]
        a.update(message=a["message"] + 1, path=["messages", a["message"] + 1, "content"])
    def disagrees(o, q):
        o["block"] = None
    return [
        ("system line two messages after its result", edit(two_after), "line_after_uncovered_denied"),
        ("the next message a user's, not a system one", edit(user_role), "line_after_uncovered_denied"),
        ("the system line naming another call's file", edit(other_file), "line_after_uncovered_denied"),
        ("the line in block 1 of the system message", edit(later_block), "line_after_uncovered_denied"),
        ("an Edit hook's prefix on a Write's line", edit(edit_prefix), "line_after_uncovered_denied"),
        ("the line without the hook's prefix", edit(no_prefix), "line_after_uncovered_denied"),
        ("the re-sent string two messages after", edit(resent_two_after), "lines_bound"),
        ("recorded block disagrees with its path", edit(disagrees), "line_after_uncovered_denied"),
    ]


def synthetic_self_test():
    sibling = g.self_test_suite("claude-own-claim", validate, synthetic, receipt_mutations, file_mutations, SPLICE)
    measured = g.self_test_suite("claude-own-claim-measured", validate, lambda root, tag: synthetic(root, tag, "measured"),
                                 lambda r: [], measured_mutations, [])
    return dict(sibling, measured=measured)


if __name__ == "__main__":
    sys.exit(g.main_for(__doc__, validate, synthetic_self_test, receipt_mutations))
