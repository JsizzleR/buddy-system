#!/usr/bin/env python3
"""Independently check a qualify-claude-jobs.py receipt (schema v2) against its evidence.

The jobs suite asks whether the adapter keeps the narrow contract (D-077, the
operator's choice of 2026-10-11, built 2026-10-12) for a native Bash job that
left the worker's process group: OBSERVED, recorded in <receipt>.proc, reported
on one stderr line, and NEVER signalled, while the group the adapter does pin
ends. qualify-claude-jobs.py decides that live; this decides it again from the
files the run left, and binds them to the receipt by hash (the runner writes
evidence_sha256 over every file read here, and a receipt without it, or with a
file it does not cover, is refused).

The live facts (the job alive after Close, the native group gone, the fixture's
own cleanup) can only be observed during the run; the receipt's record of them
is checked for consistency with the .proc record and the stderr report, not
re-observed. A receipt from the old v1 criteria (`native_tool_job_exited_on_close`)
is refused: it asked for the opposite of the contract.

Run: python3 -I scripts/check-claude-jobs.py <root>/result.json [--self-test]
     python3 -I scripts/check-claude-jobs.py --self-test   (synthetic, hermetic)
"""
import importlib.util
import json
from pathlib import Path
import sys

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("start_tier", Path(__file__).with_name("check-claude-gate.py"))
g = importlib.util.module_from_spec(spec)
spec.loader.exec_module(g)

CHECKS = {"job_ancestry_captured", "adapter_exit_zero", "adapter_exited_without_fallback", "job_escaped_worker_group",
          "escaped_job_recorded", "close_report_names_job_not_signalled", "job_not_signalled",
          "pinned_native_group_ended", "fixture_cleanup_ended_survivors"}
EVIDENCE = {"worker.json", "worker.json.proc", "worker.json.events", "adapter.stderr", "adapter-events.json",
            "provider-observations.json", "native-observations.jsonl", "native-observer", "launch.json",
            "settings.json", "work/job.json"}


def micros(start):
    return start[0] * 1000000 + start[1]


def validate(r, receipt_path):
    e = g.Evidence(r, r["root"])
    version = str(r.get("native_version", "")).removesuffix(" (Claude Code)")
    def launch(): return e.json("launch.json")
    def gate(): return g.parse_gate(launch()["gate_command"])
    def proc(): return e.json("worker.json.proc")
    def identity(): return e.json("worker.json")["identity"]
    def job(): return r["process_groups"]["job"]
    def ident(): return (job()["pid"], micros(job()["start"]))
    def pairs(rows): return {(v["pid"], v["start"]) for v in rows}
    def reply(rid):
        rows = [v for v in e.json("adapter-events.json") if v.get("request_id") == rid]
        if len(rows) != 1:
            raise ValueError("want one reply to " + rid)
        return rows[0]

    e.check("receipt_location", lambda: g.located(e, receipt_path))
    e.check("contract", lambda: r["schema"] == "buddy.claude-job-groups.v2" and g.admission.need("claude", version, e.root, r["native_sha256"]) and
            r["native_version"] == version + " (Claude Code)" and g.hex64(r["native_sha256"]) and
            r["provider"] == "deterministic-loopback" and launch()["harness_version"] == version and
            launch()["hooks"]["mods"] == "builtin-only-" + version)
    e.check("runner_verdict", lambda: g.runner_verdict(r, "passed", CHECKS))
    e.check("artifact_hashes", lambda: e.sha(gate()[0]) == r["adapter_sha256"] and e.sha(gate()[2]) == r["buddy_sha256"] and
            e.sha(g.observer_native(e, "native-observer")[0]) == r["native_sha256"] and
            g.observer_native(e, "native-observer")[1] == [])
    e.check("gate_configuration", lambda: e.json("settings.json") == g.gate_settings(launch()["gate_command"]) and
            gate()[1] == str(e.root / "worker.json") and launch()["worktree"] == str(e.root / "work") and
            launch()["hook_config"] == str(e.root / "settings.json"))
    e.check("captured_identity", lambda: g.valid_identity(identity(), "claude") and proc()["identity"] == identity() and
            proc()["receipt"] == str(e.root / "worker.json") and
            proc()["executable"]["path"] == str(e.root / "native-observer") and
            g.same_identity(identity(), e.json("adapter-events.json")) and
            g.same_identity(identity(), e.lines("worker.json.events")))
    e.check("job_approved_by_operator", lambda: reply("job-wake")["wake_delivery"]["state"] == "delivered" and
            reply("allow-job")["approval_delivery"]["state"] == "submitted" and
            e.json("provider-observations.json")[0]["tool_outputs"] == [])
    # The job the run marked is the job the record names: same pid and group as
    # it wrote from inside itself, outside the leader's group.
    e.check("job_escaped_worker_group", lambda: e.json("work/job.json")["pid"] == job()["pid"] and
            e.json("work/job.json")["pgid"] == job()["pgid"] and job()["pgid"] != proc()["leader"]["pgid"] and
            job()["pgid"] != r["process_groups"]["adapter"]["pgid"])
    e.check("escaped_job_recorded", lambda: ident() in pairs(proc()["descendants"]["alive"]) and
            ident() in pairs(proc()["close"]["captured"]) and ident() in pairs(proc()["close"]["survivors"]) and
            ident() not in pairs(proc()["close"]["ended"]) and proc()["close"]["unknown"] == [] and
            proc()["close"]["ended"] == [] and proc()["close"]["unattributable"] == "unknown" and
            proc()["close"]["trigger"] == "close" and
            all(v["pgid"] != proc()["leader"]["pgid"] for v in proc()["close"]["captured"]))
    e.check("close_report_names_job_not_signalled", lambda: g.close_line(e, "adapter.stderr",
            [v["pid"] for v in proc()["close"]["survivors"]]))
    e.check("job_not_signalled", lambda: r["job_alive_after_close"] is True and r["adapter_forced_cleanup"] == [])
    e.check("pinned_native_group_ended", lambda: bool(r["process_groups"]["natives"]) and
            r["natives_alive_after_close"] == [] and r["adapter_exit"] == 0 and
            all(n["pgid"] == proc()["leader"]["pgid"] for n in r["process_groups"]["natives"]) and
            {n["pid"] for n in r["process_groups"]["natives"]} <=
            {v["pid"] for v in e.lines("native-observations.jsonl") if v.get("type") == "buddy_fixture_native_process"})
    e.check("fixture_cleanup", lambda: r["fixture_cleanup_groups"] == [job()["pgid"]] and
            r["survivors_after_fixture_cleanup"] == [])
    binding = {}
    e.check("evidence_hashes", lambda: set(r["evidence_sha256"]) == EVIDENCE and
            binding.setdefault("v", g.evidence_binding(e, r, True)) == "hashed" and
            all(e.sha(name) == r["evidence_sha256"][name] for name in EVIDENCE))
    return e.verdict(evidence=binding.get("v"), read=sorted(e.read_paths))


def receipt_mutations(r):
    return [(("passed",), False, "runner_verdict"),
            (("checks", "job_not_signalled"), False, "runner_verdict"),
            (("checks", "escaped_job_recorded"), g.DELETE, "runner_verdict"),
            (("checks", "native_tool_job_exited_on_close"), True, "runner_verdict"),
            (("schema",), "buddy.claude-job-groups.v1", "contract"),
            (("native_version",), "2.1.292 (Claude Code)", "contract"),
            (("native_sha256",), "0" * 64, "artifact_hashes"),
            (("adapter_sha256",), "0" * 64, "artifact_hashes"),
            (("job_alive_after_close",), False, "job_not_signalled"),
            (("adapter_forced_cleanup",), ["SIGTERM"], "job_not_signalled"),
            (("natives_alive_after_close",), [{"pid": 2}], "pinned_native_group_ended"),
            (("process_groups", "job", "pid"), 3, "job_escaped_worker_group"),
            (("process_groups", "job", "start"), [1, 1], "escaped_job_recorded"),
            (("survivors_after_fixture_cleanup",), [{"pid": 3}], "fixture_cleanup"),
            (("evidence_sha256",), g.DELETE, "evidence_hashes"),
            (("evidence_sha256", "worker.json.proc"), "0" * 64, "evidence_hashes"),
            (("root",), str(Path(r["root"]) / "work"), "receipt_location")]


def synthetic(root, tag):
    """A coherent SYNTHETIC jobs run under root (F-33). Tests the checker only."""
    root = Path(root)
    root.mkdir(parents=True)
    version = g.CLAUDE_VERSIONS[-1]
    for name, body in (("bin/buddy", "buddy " + tag), ("bin/buddy-adapter", "adapter " + tag), ("bin/claude", "claude native")):
        g.write(root, name, body)
    receipt = root / "worker.json"
    gate = "'%s' hook --receipt '%s' --buddy '%s'" % (root / "bin/buddy-adapter", receipt, root / "bin/buddy")
    g.write(root, "settings.json", g.gate_settings(gate))
    launch = {"worktree": str(root / "work"), "permission_mode": "workspace-write", "hook_config": str(root / "settings.json"),
              "harness_version": version, "gate_command": gate,
              "hooks": {"configured": "observed", "trusted": "unknown", "observed": "unknown", "mods": "builtin-only-" + version}}
    g.write(root, "launch.json", launch)
    g.write(root, "native-observer", g.observer_source(root / "bin/claude"))
    identity = {"schema": "buddy.lifecycle.v1", "harness": "claude", "session_id": "sid-" + tag, "incarnation": "inc-" + tag}
    g.write(root, "worker.json", {"schema": "buddy.control.v1", "identity": identity, "launch": launch})
    shell, job = (4515, 1791937077208782), (4516, 1791937077211500)
    g.write(root, "worker.json.proc", g.proc_source(receipt, identity, root / "native-observer", leader=4460,
                                                     survivors=(shell, job)))
    g.write(root, "adapter.stderr", g.close_report((shell, job)))
    events = [{"operation": "start", "identity": identity},
              {"operation": "send", "request_id": "job-wake", "identity": identity, "wake_delivery": {"state": "delivered"}},
              {"operation": "event", "identity": identity, "native_event": {"kind": "approval"}},
              {"operation": "approve", "request_id": "allow-job", "identity": identity, "approval_delivery": {"state": "submitted"}}]
    g.write(root, "adapter-events.json", events)
    g.write(root, "worker.json.events", g.jsonl(events))
    g.write(root, "provider-observations.json", [{"path": "/v1/messages", "tool_outputs": []}])
    g.write(root, "native-observations.jsonl", g.jsonl([{"type": "buddy_fixture_native_process", "pid": 4461}]))
    g.write(root, "work/job.json", {"pid": job[0], "ppid": shell[0], "pgid": shell[0]})
    r = {"schema": "buddy.claude-job-groups.v2", "root": str(root), "passed": True, "provider": "deterministic-loopback",
         "checks": {k: True for k in CHECKS}, "adapter_forced_cleanup": [], "native_version": version + " (Claude Code)",
         "native_sha256": g.admission.synthetic("claude", version, root / "bin/claude"), "buddy_sha256": g.sha_file(root / "bin/buddy"),
         "adapter_sha256": g.sha_file(root / "bin/buddy-adapter"), "fixture_sha256": "0" * 64,
         "process_groups": {"adapter": {"pid": 4457, "pgid": 4457, "start": [1791937076, 571263]},
                            "job": {"pid": job[0], "pgid": shell[0], "start": [job[1] // 1000000, job[1] % 1000000]},
                            "natives": [{"pid": 4461, "pgid": 4460, "start": [1791937076, 778720]}]},
         "job_alive_after_close": True, "natives_alive_after_close": [], "adapter_exit": 0,
         "fixture_cleanup_groups": [shell[0]], "survivors_after_fixture_cleanup": []}
    r["evidence_sha256"] = {name: g.sha_file(root / name) for name in sorted(EVIDENCE)}
    g.write(root, "result.json", r)
    return r


def file_mutations(root, r):
    root = Path(root)
    def rehashed(name, fn):
        """Damage one hashed file AND re-hash it, as a runner writing the
        damage would: the semantic guard must refuse, not the hash."""
        def run():
            path = root / name
            value = json.loads(path.read_text())
            fn(value)
            g.write(root, name, value)
            return g.mutate(r, ("evidence_sha256", name), g.sha_file(path))
        return run
    def signalled(v):
        # What an adapter that signals its escaped job leaves: ended, not survivors.
        v["close"]["ended"] = [{"pid": x["pid"], "start": x["start"]} for x in v["close"]["survivors"]]
        v["close"]["survivors"] = []
    return [
        ("adapter signalled the job", rehashed("worker.json.proc", signalled), "escaped_job_recorded"),
        ("close left out of the .proc record", rehashed("worker.json.proc", lambda v: v.pop("close")), "escaped_job_recorded"),
        ("descendants left out of the .proc record", rehashed("worker.json.proc",
            lambda v: v["descendants"].update(alive=None)), "escaped_job_recorded"),
        ("job never escaped the group", rehashed("worker.json.proc",
            lambda v: v["leader"].update(pgid=4515)), "job_escaped_worker_group"),
        ("job.json names another process", rehashed("work/job.json", lambda v: v.update(pid=1234)), "job_escaped_worker_group"),
        ("operator never approved", rehashed("adapter-events.json", lambda v: v.pop()), "job_approved_by_operator"),
        ("spliced .proc identity", rehashed("worker.json.proc", lambda v: v["identity"].update(incarnation="other")),
         "captured_identity"),
        ("hashed file changed after the run", lambda: g.write(root, "adapter-events.json", "[]"), "evidence_hashes"),
        ("stderr does not name the job", lambda: _stderr(root, r, g.close_report()), "close_report_names_job_not_signalled"),
        ("stderr says signalled", lambda: _stderr(root, r, g.close_report(((4515, 1), (4516, 2))).replace(
            "not signalled", "signalled")), "close_report_names_job_not_signalled"),
        # Added after a mutation run found these guards unexercised (2026-10-13).
        ("job dropped from survivors", rehashed("worker.json.proc",
            lambda v: v["close"].update(survivors=v["close"]["survivors"][:1])), "escaped_job_recorded"),
        ("another capture ended", rehashed("worker.json.proc",
            lambda v: v["close"].update(ended=[{"pid": 4515, "start": 1791937077208782}])), "escaped_job_recorded"),
        ("gate widened", rehashed("settings.json", lambda v: v["hooks"]["PreToolUse"][0].update(matcher="Write")),
         "gate_configuration"),
        ("receipt hashes a file the contract does not name", lambda: g.mutate(r, ("evidence_sha256", "stray.txt"),
            g.sha_file(root / "launch.json")), "evidence_hashes"),
    ]


def _stderr(root, r, text):
    g.write(root, "adapter.stderr", text)
    return g.mutate(r, ("evidence_sha256", "adapter.stderr"), g.sha_file(Path(root) / "adapter.stderr"))


SPLICE = [("worker.json.proc", "evidence_hashes"), ("adapter-events.json", "evidence_hashes")]


def synthetic_self_test():
    return g.self_test_suite("claude-jobs", validate, synthetic, receipt_mutations, file_mutations, SPLICE)


if __name__ == "__main__":
    sys.exit(g.main_for(__doc__, validate, synthetic_self_test, receipt_mutations))
