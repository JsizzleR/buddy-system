#!/usr/bin/env python3
"""Independently check a qualify-adapters.py receipt against the evidence it left.

START-TIER CHECKER (consensus "Harness version tracking", Codex review P1): the
Codex worker under the separate buddy-adapter (D-070, D-074). The suite trusts
the adapter's own gate command by exact hash, starts one native worker, wakes it
once with a queued assignment, and expects the claim denial to hold, the
positive patch to land, the native result to stay separate from completion,
and a duplicate wake not to be replayed.

Every verdict is decided again from the run's files: the hook listing before
and after exact-hash trust and the run's own config, the launch and the
captured receipt, the adapter's stdout receipts and journal, the ownership
record (.proc) and the close report, the native Codex log on the adapter's
stderr (which carries the hook's denial text) and the worktree. The runner's
checks are only required to be its full set and all true.

Binding to the run (F-13, F-37) as check-claude-gate.py says. NOT bound, because
the runner does not record them: the Codex executable's bytes (the receipt's
`native_version` is a literal the runner writes, not a measurement; the .proc
record's path and size are checked against the file) and the CLI-side checks
whose outputs are not saved (assignment still queued after the result, body
delivery explicit). Those two rest on the runner alone and are listed in
`runner_only`. The runner .diff this lane proposes records all three.

Run: python3 -I scripts/check-adapters.py <root>/result.json [--self-test]
     python3 -I scripts/check-adapters.py --self-test   (synthetic, hermetic)
"""
import importlib.util
import json
from pathlib import Path
import sys

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("start_tier", Path(__file__).with_name("check-claude-gate.py"))
g = importlib.util.module_from_spec(spec)
spec.loader.exec_module(g)

# The recorded versions (D-079); a run is judged by g.admission.need.
CODEX_VERSIONS = g.admission.recorded_versions("codex")
CHECKS = {"captured_native_identity", "native_wake_accepted", "native_result_separate", "native_idle_translated",
          "conflict_patch_denied", "positive_patch_allowed", "assignment_still_queued_after_result",
          "duplicate_wake_not_replayed", "body_delivery_explicit"}
RUNNER_ONLY = ["assignment_still_queued_after_result", "body_delivery_explicit"]
MATCHER = "^(apply_patch|Bash)$"
DENIAL = 'Command blocked by PreToolUse hook: guarded.txt is inside scope "guarded.txt"'


def validate(r, receipt_path):
    e = g.Evidence(r, r["root"])
    def launch(): return e.json("launch.json")
    def gate(): return g.parse_gate(launch()["gate_command"])
    def ev(): return e.json("events.json")
    def identity(): return e.json("worker.json")["identity"]
    def native(kind): return [v for v in ev() if v.get("native_event", {}).get("kind") == kind]
    def reply(rid):
        rows = [v for v in ev() if v.get("request_id") == rid]
        if len(rows) != 1:
            raise ValueError("want one reply to " + rid)
        return rows[0]
    def executable(): return Path(e.json("worker.json.proc")["executable"]["path"])

    e.check("receipt_location", lambda: g.located(e, receipt_path))
    e.check("contract", lambda: g.admission.need("codex", r["native_version"], e.root, r.get("codex_sha256")) and launch()["harness_version"] == r["native_version"] and
            "loopback" in r["provider"] and executable().name == "codex")
    e.check("runner_verdict", lambda: g.runner_verdict(r, "passed", CHECKS) and r["adapter_exit"] == 0)
    e.check("artifact_hashes", lambda: e.sha(gate()[0]) == r["adapter_sha256"] and e.sha(gate()[2]) == r["buddy_sha256"] and
            ("codex_sha256" not in r or e.sha(executable()) == r["codex_sha256"]))
    def trust():
        before, after = e.json("hooks-untrusted.json")["data"], e.json("hooks-trusted.json")["data"]
        hb, ha = [h for x in before for h in x["hooks"]], [h for x in after for h in x["hooks"]]
        key = str(e.root / "codex/config.toml") + ":pre_tool_use:0:0"
        return (len(hb) == len(ha) == 1 and hb[0]["trustStatus"] == "untrusted" and ha[0]["trustStatus"] == "trusted" and
                all(h["key"] == key and h["command"] == launch()["gate_command"] and h["matcher"] == MATCHER and
                    h["eventName"] == "preToolUse" for h in hb + ha) and
                hb[0]["currentHash"] == ha[0]["currentHash"] == launch()["gate_hash"] and
                ('[hooks.state.%s]\ntrusted_hash = %s\n' % (json.dumps(key), json.dumps(ha[0]["currentHash"]))) in
                e.text("codex/config.toml"))
    e.check("hook_trust_reviewed_then_exact", trust)
    e.check("gate_configuration", lambda: gate()[1] == str(e.root / "worker.json") and
            launch()["worktree"] == str(e.root / "work") and launch()["hook_config"] == str(e.root / "codex/config.toml") and
            launch()["permission_mode"] == "workspace-write" and launch()["hooks"]["trusted"] == "observed")
    e.check("captured_identity", lambda: g.valid_identity(identity(), "codex") and
            [v["operation"] for v in ev()][:1] == ["start"] and ev()[0]["identity"] == identity() and
            not any(v.get("error") for v in ev()) and e.json("worker.json")["launch"] == launch() and
            g.same_identity(identity(), ev()) and g.same_identity(identity(), e.lines("worker.json.events")))
    e.check("claim_denial_held", lambda: e.text("work/guarded.txt") == "original\n" and e.absent("work/collateral.txt") and
            DENIAL in e.text("adapter.stderr"))
    e.check("allowed_control", lambda: e.text("work/allowed.txt") == "allowed-control\n")
    def turn():
        wake = reply("wake-1")
        tid = wake["wake_delivery"]["turn_id"]
        busy, results, idles = native("busy"), native("result"), native("idle")
        return (wake["wake_delivery"]["state"] == "delivered" and bool(tid) and
                len(busy) == len(results) == len(idles) == 1 and
                all(v["native_event"]["turn_id"] == tid and v["native_event"]["native_session_id"] == identity()["session_id"]
                    for v in busy + results + idles) and
                results[0]["completion"]["state"] == "unknown" and bool(results[0]["native_event"]["result_ref"]) and
                results[0]["native_event"]["result_status"] == "completed" and idles[0]["state"]["detail"] == "idle" and
                ev().index(wake) < ev().index(busy[0]) < ev().index(results[0]) < ev().index(idles[0]))
    e.check("one_turn", turn)
    # Not replayed: the duplicate was answered with the original delivery and
    # no second native turn followed it.
    e.check("duplicate_not_replayed", lambda: reply("wake-duplicate")["wake_delivery"] == reply("wake-1")["wake_delivery"] and
            ev().index(reply("wake-duplicate")) > ev().index(native("idle")[0]) and
            ev().index(reply("wake-duplicate")) == len(ev()) - 1)
    e.check("clean_close", lambda: g.close_line(e, "adapter.stderr") and
            g.proc_record(e, "worker.json.proc", identity(), e.root / "worker.json", executable()))
    binding = {}
    e.check("evidence_hashes", lambda: binding.setdefault("v", g.evidence_binding(e, r, False)))
    unbound = [] if r.get("codex_sha256") else ["codex executable bytes (native_version is the runner's literal)"]
    return e.verdict(evidence=binding.get("v"), read=sorted(e.read_paths), unbound=unbound, runner_only=RUNNER_ONLY)


def receipt_mutations(r):
    return [(("passed",), False, "runner_verdict"),
            (("checks", "conflict_patch_denied"), False, "runner_verdict"),
            (("checks", "duplicate_wake_not_replayed"), g.DELETE, "runner_verdict"),
            (("adapter_exit",), 1, "runner_verdict"),
            (("native_version",), "0.0.1", "contract"),
            (("adapter_sha256",), "0" * 64, "artifact_hashes"),
            (("buddy_sha256",), "0" * 64, "artifact_hashes"),
            (("codex_sha256",), "0" * 64, "artifact_hashes"),
            (("root",), str(Path(r["root"]) / "work"), "receipt_location")]


def synthetic(root, tag):
    """A coherent SYNTHETIC adapter-driven Codex run under root (F-33). Tests the checker only."""
    root = Path(root)
    root.mkdir(parents=True)
    for name, body in (("bin/buddy", "buddy " + tag), ("bin/buddy-adapter", "adapter " + tag), ("bin/codex", "codex native")):
        g.write(root, name, body)
    receipt = root / "worker.json"
    gate = "'%s' hook --receipt '%s' --buddy '%s'" % (root / "bin/buddy-adapter", receipt, root / "bin/buddy")
    key = str(root / "codex/config.toml") + ":pre_tool_use:0:0"
    digest = "sha256:" + "9" * 64
    def listing(status):
        return {"data": [{"cwd": str(root / "work"), "hooks": [{"key": key, "eventName": "preToolUse", "matcher": MATCHER,
                "command": gate, "currentHash": digest, "trustStatus": status}]}]}
    g.write(root, "hooks-untrusted.json", listing("untrusted"))
    g.write(root, "hooks-trusted.json", listing("trusted"))
    g.write(root, "codex/config.toml", 'model = "fixture"\n\n[hooks.state.%s]\ntrusted_hash = %s\n' % (json.dumps(key), json.dumps(digest)))
    launch = {"worktree": str(root / "work"), "permission_mode": "workspace-write", "hook_config": str(root / "codex/config.toml"),
              "harness_version": CODEX_VERSIONS[-1], "gate_command": gate, "gate_hash": digest,
              "hooks": {"configured": "observed", "trusted": "observed", "observed": "unknown", "no_mods": "unknown"}}
    g.write(root, "launch.json", launch)
    sid, turn = "thread-" + tag, "turn-" + tag
    identity = {"schema": "buddy.lifecycle.v1", "harness": "codex", "session_id": sid, "incarnation": "inc-" + tag}
    g.write(root, "worker.json", {"schema": "buddy.control.v1", "identity": identity, "launch": launch})
    def event(op, **kw):
        return dict({"schema": "buddy.control.v1", "operation": op, "identity": identity, "state": {"state": "unknown"},
                     "completion": {"state": "unknown"}}, **kw)
    def nat(kind, **kw):
        return event("event", native_event=dict({"kind": kind, "native_session_id": sid, "turn_id": turn}, **kw),
                     state={"state": "observed", "detail": "idle" if kind == "idle" else "busy"})
    wake = {"state": "delivered", "turn_id": turn, "result_ref": turn}
    events = [event("start"), event("send", request_id="wake-1", wake_delivery=wake, message_id=1), nat("busy"),
              nat("result", result_status="completed", result_ref=turn), nat("idle"),
              event("send", request_id="wake-duplicate", wake_delivery=wake, message_id=1)]
    g.write(root, "events.json", events)
    g.write(root, "worker.json.events", g.jsonl(events))
    g.write(root, "worker.json.proc", g.proc_source(receipt, identity, root / "bin/codex"))
    g.write(root, "adapter.stderr", "ERROR codex_core::tools::router: error=" + DENIAL + " claimed by session work/s-holder\n" +
            g.close_report())
    g.write(root, "work/guarded.txt", "original\n")
    g.write(root, "work/allowed.txt", "allowed-control\n")
    r = {"root": str(root), "checks": {k: True for k in CHECKS}, "passed": True, "adapter_exit": 0,
         "buddy_sha256": g.sha_file(root / "bin/buddy"), "adapter_sha256": g.sha_file(root / "bin/buddy-adapter"),
         "fixture_sha256": "0" * 64, "native_version": CODEX_VERSIONS[-1], "provider": "deterministic loopback fixture; no real inference"}
    g.write(root, "result.json", r)
    return r


def file_mutations(root, r):
    root = Path(root)
    def edit_json(name, fn):
        def run():
            value = json.loads((root / name).read_text())
            fn(value)
            g.write(root, name, value)
        return run
    return [
        ("denied edit happened", lambda: g.write(root, "work/guarded.txt", "conflicting-write\n"), "claim_denial_held"),
        ("half of the denied patch landed", lambda: g.write(root, "work/collateral.txt", "must-not-exist\n"), "claim_denial_held"),
        ("denial never reported", lambda: g.write(root, "adapter.stderr", g.close_report()), "claim_denial_held"),
        ("missing allowed control", lambda: (root / "work/allowed.txt").unlink(), "allowed_control"),
        ("duplicate wake replayed", edit_json("events.json", lambda rows: rows.extend(rows[2:5])), "one_turn"),
        ("result before its wake", edit_json("events.json", lambda rows: rows.insert(1, rows.pop(3))), "one_turn"),
        ("result marked complete", edit_json("events.json", lambda rows: rows[3]["completion"].update(state="completed")), "one_turn"),
        ("hook trusted before review", edit_json("hooks-untrusted.json",
            lambda v: v["data"][0]["hooks"][0].update(trustStatus="trusted")), "hook_trust_reviewed_then_exact"),
        ("trusted a different hash", edit_json("hooks-trusted.json",
            lambda v: v["data"][0]["hooks"][0].update(currentHash="sha256:" + "8" * 64)), "hook_trust_reviewed_then_exact"),
        ("escaped descendant left running", edit_json("worker.json.proc",
            lambda v: v["close"]["survivors"].append({"pid": 9, "start": 9})), "clean_close"),
        ("journal names another identity", lambda: g.write(root, "worker.json.events",
            g.jsonl([dict(json.loads((root / "events.json").read_text())[0], identity={"incarnation": "x"})])), "captured_identity"),
        ("adapter binary replaced", lambda: g.write(root, "bin/buddy-adapter", "other"), "artifact_hashes"),
        # Added after a mutation run found these guards unexercised (2026-10-13).
        ("duplicate answered with a new delivery", edit_json("events.json",
            lambda rows: rows[-1]["wake_delivery"].update(turn_id="turn-new")), "duplicate_not_replayed"),
        ("worker launched with a wider sandbox", edit_json("launch.json",
            lambda v: v.update(permission_mode="danger-full-access")), "gate_configuration"),
        ("reviewed hash differs from the trusted one", edit_json("hooks-untrusted.json",
            lambda v: v["data"][0]["hooks"][0].update(currentHash="sha256:" + "7" * 64)), "hook_trust_reviewed_then_exact"),
    ]


SPLICE = [("worker.json.proc", "clean_close"), ("events.json", "captured_identity"),
          ("hooks-trusted.json", "hook_trust_reviewed_then_exact")]


def synthetic_self_test():
    return g.self_test_suite("adapters", validate, synthetic, receipt_mutations, file_mutations, SPLICE)


if __name__ == "__main__":
    sys.exit(g.main_for(__doc__, validate, synthetic_self_test, receipt_mutations))
