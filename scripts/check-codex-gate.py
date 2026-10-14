#!/usr/bin/env python3
"""Independently check a qualify-codex.py receipt against the evidence it left.

START-TIER CHECKER (consensus "Harness version tracking", Codex review P1), for
the Codex half of the start tier. Two modes are accepted, each bound by the
receipt's `mode` and checked against its OWN case set:

- the gate mode, `qualify-codex.py --app-server --lifecycle --edge-cases
  --shell-patch`. Only that full mode: a receipt from a narrower run is missing
  the shell-routed patch (D-073) or the edge cases, and is refused rather than
  read as a smaller pass;
- the adapter mode, `qualify-codex.py --adapter <buddy-adapter>` (F-26, D-078):
  the worker launched through the real adapter with its receipt-bound gate,
  and the claims its completed fileChanges renewed. A receipt whose mode is
  exactly {"adapter": true} is validated as that and nothing else; one that
  names `adapter` beside anything else, or carries a gate-mode field, is
  refused. Extended rather than refused because the adapter run is the only
  native evidence that a Codex worker's claims renew, and a verdict nothing
  re-derives is the runner's word.

Every verdict is decided again from the run's files: the hook definitions as
Codex listed them before and after exact-hash trust, the trust entries in the
run's own config, each gate invocation's payload and envelope, the provider's
record of what each tool call returned to the model, the worktree, the broken
nested repository, the lifecycle wrapper's ledger snapshots, and the
app-server transcript. The runner's own checks are only required to be the
full set and all true, so a receipt with one failing case is refused.

Binding to the run (F-13, F-37): the receipt must be <root>/result.json; every
hook payload, transcript path, trusted hook key and app-server thread names
this root and one captured native thread; the buddy binary both wrappers run
hashes to the receipt's value. NOT bound, because the runner does not record
it: the Codex executable's bytes (the receipt carries `codex --version` text
only). The runner .diff this lane proposes records it; until then a version
string is all the receipt can say, and the output says so in `unbound`.

The adapter mode re-derives renewal the way check-claude-gate.py does (its
"claim renewal" section): from the run's two ledger images, the adapter's
journal, and the tool outputs the model was given, never from the receipt's
`renewal` record, which must instead equal what those files hold.

Run: python3 -I scripts/check-codex-gate.py <root>/result.json [--self-test]
     python3 -I scripts/check-codex-gate.py --self-test   (synthetic, hermetic)
"""
import ast
import importlib.util
import json
from pathlib import Path
import shlex
import sys

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("start_tier", Path(__file__).with_name("check-claude-gate.py"))
g = importlib.util.module_from_spec(spec)
spec.loader.exec_module(g)

# The recorded versions (D-079); a run is judged by g.admission.need.
CODEX_VERSIONS = g.admission.recorded_versions("codex")
CHECKS = {"codex_exit_zero", "all_actual_patch_hooks", "guarded_unchanged", "whole_denied_patch_unchanged",
          "allowed_control_written", "deny_envelope_seen", "shell_routed_patch_hook_denied", "malformed_patch_denied",
          "unreadable_ledger_denied", "native_lifecycle_events", "native_lifecycle_hooks_succeeded",
          "lifecycle_observer_succeeded", "same_captured_incarnation", "native_stop_idle_observed", "native_end_observed"}
# What the suite must have exercised, written down independently of the runner.
DENY_PATCH = '*** Begin Patch\n*** Update File: guarded.txt\n@@\n-original\n+conflicting-write\n*** Add File: collateral.txt\n+must-not-exist\n*** End Patch'
MALFORMED = '*** Begin Patch\n*** Add File: malformed.txt\nmissing plus prefix\n*** End Patch'
UNREADABLE = '*** Begin Patch\n*** Add File: broken/should-not-exist.txt\n+must-not-exist\n*** End Patch'
ALLOW_PATCH = '*** Begin Patch\n*** Add File: allowed.txt\n+allowed-control\n*** End Patch'
LIFECYCLE = ["SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "SessionEnd"]
BLOCKED = "Command blocked by PreToolUse hook: "
MATCHER = "^(apply_patch|Bash)$"


def wrapper_buddy(e, name):
    """The buddy binary a wrapper runs: its single subprocess.run([<path>, ...])."""
    rows = [v for v in e.text(name).splitlines() if "subprocess.run([" in v]
    paths = {ast.literal_eval(v.split("subprocess.run([", 1)[1].split(",", 1)[0]) for v in rows}
    if len(paths) != 1:
        raise ValueError(name + " does not name exactly one buddy binary")
    return paths.pop()


def flat(listing):
    return [h for entry in listing["data"] for h in entry["hooks"]]


def deny(stdout):
    out = json.loads(stdout)["hookSpecificOutput"]
    return out["hookEventName"] == "PreToolUse" and out["permissionDecision"] == "deny" and bool(out["permissionDecisionReason"])


def adapter_mode(r):
    """A receipt that names the adapter mode at all is judged as one."""
    return isinstance(r.get("mode"), dict) and "adapter" in r["mode"]


def validate(r, receipt_path):
    return (validate_adapter if adapter_mode(r) else validate_gate)(r, receipt_path)


def receipt_mutations(r):
    return adapter_receipt_mutations(r) if adapter_mode(r) else gate_receipt_mutations(r)


def validate_gate(r, receipt_path):
    e = g.Evidence(r, r["root"])
    work = e.root / "work"
    version = str(r.get("codex_version", "")).removeprefix("codex-cli ")
    def captured(): return e.json("captured-receipt.json")
    def hooks(): return e.lines("hook-invocations.jsonl")
    def life(): return e.lines("lifecycle-invocations.jsonl")
    def outputs():
        rows = {}
        for request in e.json("requests.json"):
            for x in request["tool_outputs"]:
                rows.setdefault(x["call_id"], x["output"])
        return rows
    def record(i, tool, command):
        v = hooks()[i]
        p = v["payload"]
        field = "command" if tool in ("Bash", "apply_patch") else None
        return (p["tool_name"] == tool and p["tool_input"][field] == command and v["returncode"] == 0 and
                p["hook_event_name"] == "PreToolUse")

    e.check("receipt_location", lambda: g.located(e, receipt_path))
    e.check("contract", lambda: g.admission.need("codex", version, e.root, r.get("codex_sha256")) and
            r["codex_version"] == "codex-cli " + version and
            "no real inference" in r["qualification"] and r["requests"] == len(e.json("requests.json")) == 5)
    e.check("runner_verdict", lambda: isinstance(r["checks"], dict) and set(r["checks"]) == CHECKS and
            all(v is True for v in r["checks"].values()) and r["codex_returncode"] == 0)
    # codex_sha256/codex_path and mode are what the proposed runner change
    # records; once present they are checked, and their absence is `unbound`.
    e.check("artifact_hashes", lambda: e.sha(wrapper_buddy(e, "gate-wrapper.py")) == r["buddy_sha256"] and
            wrapper_buddy(e, "lifecycle-wrapper.py") == wrapper_buddy(e, "gate-wrapper.py") and
            ("codex_sha256" not in r or e.sha(r["codex_path"]) == r["codex_sha256"]) and
            ("mode" not in r or r["mode"] == {"app_server": True, "lifecycle": True, "edge_cases": True, "shell_patch": True}))
    def trust():
        before, after = flat(e.json("hooks-untrusted.json")), flat(e.json("hooks-trusted.json"))
        config = e.text("state/config.toml")
        commands = {}
        for h in before:
            # The python half is the runner's interpreter; the wrapper half is ours.
            argv = shlex.split(h["command"])
            if len(argv) != 2 or Path(argv[1]) not in (e.root / "gate-wrapper.py", e.root / "lifecycle-wrapper.py"):
                return False
            commands.setdefault(Path(argv[1]).name, set()).add(h["eventName"])
        view = lambda rows: sorted((h["key"], h["currentHash"], h["command"], h["eventName"], h["matcher"]) for h in rows)
        return (len(before) == len(after) == 6 and all(h["trustStatus"] == "untrusted" for h in before) and
                all(h["trustStatus"] == "trusted" for h in after) and view(before) == view(after) and
                commands == {"gate-wrapper.py": {"preToolUse"}, "lifecycle-wrapper.py":
                             {"sessionStart", "userPromptSubmit", "postToolUse", "stop", "sessionEnd"}} and
                all(h["matcher"] == MATCHER for h in after if h["eventName"] == "preToolUse") and
                all(h["key"].startswith(str(e.root / "state/config.toml") + ":") for h in after) and
                all(('[hooks.state.%s]\ntrusted_hash = %s\n' % (json.dumps(h["key"]), json.dumps(h["currentHash"]))) in config
                    for h in after))
    e.check("hook_trust_reviewed_then_exact", trust)
    def payloads():
        sid = captured()["session_id"]
        return (len(hooks()) == 4 and all(v["payload"]["session_id"] == sid and v["payload"]["cwd"] == str(work) and
                e.inside(v["payload"]["transcript_path"]) for v in hooks() + life()))
    e.check("one_native_thread", lambda: payloads() and captured()["harness"] == "codex" and
            captured()["event"] == "begin" and bool(captured()["incarnation"]))
    # The shell-routed patch (D-073): Codex reported it as Bash, the gate denied
    # it, nothing of the WHOLE patch landed, and the model was told so.
    e.check("shell_routed_denial", lambda: record(0, "Bash", "apply_patch <<'EOF'\n" + DENY_PATCH + "\nEOF") and
            deny(hooks()[0]["stdout"]) and e.text("work/guarded.txt") == "original\n" and e.absent("work/collateral.txt") and
            outputs()["call_buddy_1"].startswith(BLOCKED))
    e.check("malformed_patch_denied", lambda: record(1, "apply_patch", MALFORMED) and deny(hooks()[1]["stdout"]) and
            e.absent("work/malformed.txt") and outputs()["call_buddy_2"].startswith(BLOCKED))
    e.check("unreadable_ledger_denied", lambda: record(2, "apply_patch", UNREADABLE) and deny(hooks()[2]["stdout"]) and
            e.path("work/broken/.git/buddy.db").read_bytes() == b"not a SQLite ledger" and
            e.absent("work/broken/should-not-exist.txt") and outputs()["call_buddy_3"].startswith(BLOCKED))
    e.check("allowed_control", lambda: record(3, "apply_patch", ALLOW_PATCH) and hooks()[3]["stdout"].strip() == "" and
            e.text("work/allowed.txt") == "allowed-control\n" and
            "Success. Updated the following files:\nA allowed.txt" in outputs()["call_buddy_4"])
    def lifecycle():
        inc = captured()["incarnation"]
        rows = life()
        snap = {v["payload"]["hook_event_name"]: v["ledger_snapshot"] for v in rows}
        return ([v["payload"]["hook_event_name"] for v in rows] == LIFECYCLE and
                all(v["returncode"] == 0 and "observer_error" not in v and v["ledger_snapshot"][0] == inc for v in rows) and
                snap["SessionStart"][1] is None and snap["Stop"][1] is None and snap["Stop"][2] == 1 and
                snap["SessionEnd"][1] is not None and rows[2]["payload"]["tool_name"] == "apply_patch")
    e.check("lifecycle_same_incarnation", lifecycle)
    def thread():
        rows = e.lines("codex.stdout.jsonl")
        started = [v for v in rows if v.get("id") == 2]
        done = [v for v in rows if v.get("method") == "turn/completed"]
        return (len(started) == 1 and started[0]["result"]["thread"]["id"] == captured()["session_id"] and
                len(done) == 1 and done[0]["params"]["turn"]["status"] == "completed" and
                done[0]["params"]["threadId"] == captured()["session_id"])
    e.check("app_server_thread", thread)
    binding = {}
    e.check("evidence_hashes", lambda: binding.setdefault("v", g.evidence_binding(e, r, False)))
    unbound = [] if r.get("codex_sha256") else ["codex executable bytes (receipt carries version text only)"]
    return e.verdict(evidence=binding.get("v"), read=sorted(e.read_paths), unbound=unbound)


def gate_receipt_mutations(r):
    return [(("checks", "unreadable_ledger_denied"), False, "runner_verdict"),
            (("checks", "shell_routed_patch_hook_denied"), g.DELETE, "runner_verdict"),
            (("codex_returncode",), 1, "runner_verdict"),
            (("codex_version",), "codex-cli 0.162.1", "contract"),
            (("requests",), 4, "contract"),
            (("buddy_sha256",), "0" * 64, "artifact_hashes"),
            (("codex_sha256",), "0" * 64, "artifact_hashes"),
            (("mode",), {"app_server": True, "lifecycle": True, "edge_cases": True, "shell_patch": False}, "artifact_hashes"),
            (("mode",), {"adapter": True}, "mode_bound"),
            (("root",), str(Path(r["root"]) / "work"), "receipt_location")]


def synthetic(root, tag):
    """A coherent SYNTHETIC full-mode Codex gate run under root (F-33). Tests the checker only."""
    root = Path(root)
    root.mkdir(parents=True)
    work, python = root / "work", "/usr/bin/python3"
    g.write(root, "bin/buddy", "buddy " + tag)
    for name in ("gate-wrapper.py", "lifecycle-wrapper.py"):
        g.write(root, name, "import subprocess\np=subprocess.run([%r,'gate'],input=raw)\n" % str(root / "bin/buddy"))
    sid, inc = "thread-" + tag, "inc-" + tag
    g.write(root, "captured-receipt.json", {"schema": "buddy.lifecycle.v1", "harness": "codex", "session_id": sid,
                                             "incarnation": inc, "event": "begin", "state": "live"})
    key = lambda event: "%s:%s:0:0" % (root / "state/config.toml", event)
    defs = [("preToolUse", "pre_tool_use", "gate-wrapper.py", MATCHER)] + [
        (n, s, "lifecycle-wrapper.py", None) for n, s in (("postToolUse", "post_tool_use"), ("sessionStart", "session_start"),
                                                           ("sessionEnd", "session_end"), ("userPromptSubmit", "user_prompt_submit"),
                                                           ("stop", "stop"))]
    def listing(status):
        return {"data": [{"cwd": str(work), "hooks": [{"key": key(s), "eventName": n, "matcher": m, "trustStatus": status,
                "currentHash": "sha256:" + (s[:4] * 16)[:64], "command": shlex.quote(python) + " " + shlex.quote(str(root / w))}
                for n, s, w, m in defs]}]}
    g.write(root, "hooks-untrusted.json", listing("untrusted"))
    g.write(root, "hooks-trusted.json", listing("trusted"))
    g.write(root, "state/config.toml", "model = \"fixture\"\n" + "".join(
        '\n[hooks.state.%s]\ntrusted_hash = %s\n' % (json.dumps(h["key"]), json.dumps(h["currentHash"]))
        for h in listing("trusted")["data"][0]["hooks"]))
    transcript = str(root / "state/sessions/rollout.jsonl")
    def payload(tool, command):
        return {"session_id": sid, "cwd": str(work), "transcript_path": transcript, "hook_event_name": "PreToolUse",
                "tool_name": tool, "tool_input": {"command": command}}
    def denial(reason):
        return json.dumps({"hookSpecificOutput": {"hookEventName": "PreToolUse", "permissionDecision": "deny",
                                                  "permissionDecisionReason": reason}})
    hooks = [{"payload": payload("Bash", "apply_patch <<'EOF'\n" + DENY_PATCH + "\nEOF"), "stdout": denial("via the shell"), "returncode": 0},
             {"payload": payload("apply_patch", MALFORMED), "stdout": denial("unexpected patch line"), "returncode": 0},
             {"payload": payload("apply_patch", UNREADABLE), "stdout": denial("ledger unreadable"), "returncode": 0},
             {"payload": payload("apply_patch", ALLOW_PATCH), "stdout": "", "returncode": 0}]
    g.write(root, "hook-invocations.jsonl", g.jsonl(hooks))
    snaps = {"SessionStart": [inc, None, 0], "UserPromptSubmit": [inc, None, 0], "PostToolUse": [inc, None, 0],
             "Stop": [inc, None, 1], "SessionEnd": [inc, 1791000000, 1]}
    life = [{"payload": dict({"session_id": sid, "cwd": str(work), "transcript_path": transcript, "hook_event_name": ev},
                             **({"tool_name": "apply_patch"} if ev == "PostToolUse" else {})),
             "returncode": 0, "ledger_snapshot": snaps[ev]} for ev in LIFECYCLE]
    g.write(root, "lifecycle-invocations.jsonl", g.jsonl(life))
    outs = [("call_buddy_1", BLOCKED + "via the shell"), ("call_buddy_2", BLOCKED + "unexpected patch line"),
            ("call_buddy_3", BLOCKED + "ledger unreadable"),
            ("call_buddy_4", "Exit code: 0\nOutput:\nSuccess. Updated the following files:\nA allowed.txt\n")]
    g.write(root, "requests.json", [{"path": "/v1/responses", "tool_outputs": [{"call_id": c, "output": o} for c, o in outs[:n]]}
                                    for n in range(5)])
    g.write(root, "codex.stdout.jsonl", g.jsonl([{"id": 1, "result": {}}, {"id": 2, "result": {"thread": {"id": sid}}},
        {"method": "turn/completed", "params": {"threadId": sid, "turn": {"status": "completed"}}}]))
    g.write(root, "work/guarded.txt", "original\n")
    g.write(root, "work/allowed.txt", "allowed-control\n")
    g.write(root, "work/broken/.git/buddy.db", b"not a SQLite ledger")
    r = {"qualification": "installed native Codex with deterministic local provider fixture; no real inference",
         "codex_version": "codex-cli " + CODEX_VERSIONS[-1], "buddy_sha256": g.sha_file(root / "bin/buddy"),
         "fixture_sha256": "0" * 64, "checks": {k: True for k in CHECKS}, "codex_returncode": 0, "requests": 5,
         "root": str(root)}
    g.write(root, "result.json", r)
    return r


def file_mutations(root, r):
    root = Path(root)
    def edit_lines(name, fn):
        def run():
            rows = [json.loads(v) for v in (root / name).read_text().splitlines()]
            fn(rows)
            g.write(root, name, g.jsonl(rows))
        return run
    def edit_json(name, fn):
        def run():
            value = json.loads((root / name).read_text())
            fn(value)
            g.write(root, name, value)
        return run
    return [
        ("denied edit happened", lambda: g.write(root, "work/guarded.txt", "conflicting-write\n"), "shell_routed_denial"),
        ("half of the denied patch landed", lambda: g.write(root, "work/collateral.txt", "must-not-exist\n"), "shell_routed_denial"),
        ("shell route reported as apply_patch", edit_lines("hook-invocations.jsonl",
            lambda rows: rows[0]["payload"].update(tool_name="apply_patch")), "shell_routed_denial"),
        ("shell route allowed", edit_lines("hook-invocations.jsonl", lambda rows: rows[0].update(stdout="")), "shell_routed_denial"),
        ("missing allowed control", lambda: (root / "work/allowed.txt").unlink(), "allowed_control"),
        ("allowed control denied", edit_lines("hook-invocations.jsonl",
            lambda rows: rows[3].update(stdout=rows[1]["stdout"])), "allowed_control"),
        ("malformed patch landed", lambda: g.write(root, "work/malformed.txt", "x"), "malformed_patch_denied"),
        ("nested ledger was readable", lambda: g.write(root, "work/broken/.git/buddy.db", b"SQLite format 3\0"), "unreadable_ledger_denied"),
        ("hooks trusted before review", edit_json("hooks-untrusted.json",
            lambda v: v["data"][0]["hooks"][0].update(trustStatus="trusted")), "hook_trust_reviewed_then_exact"),
        ("trusted hash differs from the reviewed one", edit_json("hooks-trusted.json",
            lambda v: v["data"][0]["hooks"][0].update(currentHash="sha256:" + "f" * 64)), "hook_trust_reviewed_then_exact"),
        ("gate hook outside the run", edit_json("hooks-trusted.json",
            lambda v: [h.update(command="/usr/bin/python3 /opt/gate.py") for h in v["data"][0]["hooks"]]),
            "hook_trust_reviewed_then_exact"),
        ("lifecycle missing Stop", edit_lines("lifecycle-invocations.jsonl", lambda rows: rows.pop(3)), "lifecycle_same_incarnation"),
        ("lifecycle names another incarnation", edit_lines("lifecycle-invocations.jsonl",
            lambda rows: rows[2]["ledger_snapshot"].__setitem__(0, "other")), "lifecycle_same_incarnation"),
        ("session never ended", edit_lines("lifecycle-invocations.jsonl",
            lambda rows: rows[4]["ledger_snapshot"].__setitem__(1, None)), "lifecycle_same_incarnation"),
        ("turn did not complete", edit_lines("codex.stdout.jsonl",
            lambda rows: rows[2]["params"]["turn"].update(status="failed")), "app_server_thread"),
        ("wrapper runs another buddy", lambda: g.write(root, "gate-wrapper.py",
            "import subprocess\np=subprocess.run(['/opt/buddy','gate'])\n"), "artifact_hashes"),
        # Added after a mutation run found these guards unexercised (2026-10-13).
        ("lifecycle out of order", edit_lines("lifecycle-invocations.jsonl",
            lambda rows: rows.insert(0, rows.pop(1))), "lifecycle_same_incarnation"),
        ("app-server started another thread", edit_lines("codex.stdout.jsonl",
            lambda rows: rows[1]["result"]["thread"].update(id="other")), "app_server_thread"),
    ]


SPLICE = [("hook-invocations.jsonl", "one_native_thread"), ("captured-receipt.json", "one_native_thread"),
          ("codex.stdout.jsonl", "app_server_thread")]


def gate_self_test():
    return g.self_test_suite("codex-gate", validate, synthetic, receipt_mutations, file_mutations, SPLICE)


# ------------------------------------------------- the adapter mode (D-078)

ADAPTER_CHECKS = {"captured_native_identity", "claim_renewal_capability_declared", "native_wake_accepted",
                  "native_idle_translated", "adapter_exit_zero", "conflict_patch_denied", "positive_patch_allowed",
                  "move_patch_applied", "renewal_written_claim_moved", "renewal_move_source_claim_moved",
                  "renewal_move_destination_claim_moved", "renewal_control_claim_unmoved", "renewal_held_claim_unmoved",
                  "renewal_journalled_after_wake_before_idle", "renewal_placed_only_written_paths",
                  "renewal_coverage_complete", "renewal_same_identity"}
# The worker's claims (qualify-codex.py adapter_main): allowed.txt is added and
# source.txt -> moved.txt is moved, so those three renew; control.txt is never
# named; the holder's guarded.txt stands behind the denied patch.
ADAPTER_CLAIMS = {"worker-allowed": ("allowed.txt", True), "worker-move-source": ("source.txt", True),
                  "worker-move-destination": ("moved.txt", True), "worker-control": ("control.txt", True),
                  "guarded": ("guarded.txt", False)}
WRITTEN = ["allowed.txt", "source.txt", "moved.txt"]
MOVE = ("source.txt", "moved.txt")
# Fields only the gate mode's receipt carries: an adapter receipt naming one is
# a gate receipt with its mode rewritten.
GATE_ONLY = ("codex_returncode",)


def validate_adapter(r, receipt_path):
    e = g.Evidence(r, r["root"])
    version = str(r.get("codex_version", "")).removeprefix("codex-cli ")
    launch = lambda: e.json("launch.json")
    gate = lambda: g.parse_gate(launch()["gate_command"])
    journal = lambda: e.lines("worker.json.events")
    def identity(): return e.json("worker.json")["identity"]
    def outputs():
        rows = {}
        for request in e.json("requests.json"):
            for x in request["tool_outputs"]:
                rows.setdefault(x["call_id"], x["output"])
        return rows
    images = lambda: g.renewal_images(e, identity())

    e.check("receipt_location", lambda: g.located(e, receipt_path))
    e.check("mode_bound", lambda: r["mode"] == {"adapter": True} and not any(k in r for k in GATE_ONLY) and
            "through buddy-adapter" in r["qualification"])
    e.check("contract", lambda: g.admission.need("codex", version, e.root, r.get("codex_sha256")) and
            r["codex_version"] == "codex-cli " + version and
            "no real inference" in r["qualification"] and r["requests"] == len(e.json("requests.json")) == 4 and
            launch()["harness_version"] == version and launch()["permission_mode"] == "workspace-write")
    e.check("runner_verdict", lambda: r["passed"] is True and isinstance(r["checks"], dict) and
            set(r["checks"]) == ADAPTER_CHECKS and all(v is True for v in r["checks"].values()))
    e.check("artifact_hashes", lambda: e.sha(gate()[0]) == r["adapter_sha256"] and e.sha(gate()[2]) == r["buddy_sha256"] and
            e.sha(r["codex_path"]) == r["codex_sha256"] and
            e.json("worker.json.proc")["executable"]["path"] == r["codex_path"])
    # The one hook is the adapter's receipt-bound gate, reviewed untrusted, then
    # trusted by its exact hash in this run's own config.
    def trust():
        before, after = flat(e.json("hooks-untrusted.json")), flat(e.json("hooks-trusted.json"))
        view = lambda rows: [(h["key"], h["currentHash"], h["command"], h["eventName"], h["matcher"]) for h in rows]
        h = after[0]
        return (len(before) == len(after) == 1 and before[0]["trustStatus"] == "untrusted" and h["trustStatus"] == "trusted" and
                view(before) == view(after) and h["eventName"] == "preToolUse" and h["matcher"] == MATCHER and
                h["command"] == launch()["gate_command"] and h["currentHash"] == launch()["gate_hash"] and
                h["key"].startswith(str(e.root / "state/config.toml") + ":") and
                ('[hooks.state.%s]\ntrusted_hash = %s\n' % (json.dumps(h["key"]), json.dumps(h["currentHash"]))) in
                e.text("state/config.toml"))
    e.check("gate_configuration", lambda: trust() and gate()[1] == str(e.root / "worker.json") and
            launch()["worktree"] == str(e.root / "work") and launch()["hook_config"] == str(e.root / "state/config.toml") and
            all(e.json("worker.json")["launch"][k] == launch()[k] for k in ("gate_command", "worktree", "hook_config", "harness_version")))
    e.check("captured_identity", lambda: g.valid_identity(identity(), "codex") and
            [v.get("operation") for v in journal()].count("start") == 1 and journal()[0]["operation"] == "start" and
            g.same_identity(identity(), journal()) and g.same_identity(identity(), e.json("events.json")))
    # What the model was told and what is on disk: the patch through the held
    # claim refused whole, the add and the move completed.
    e.check("patches", lambda: e.text("work/guarded.txt") == "original\n" and e.absent("work/collateral.txt") and
            outputs()["call_buddy_1"].startswith(BLOCKED) and 'guarded.txt is inside scope "guarded.txt"' in outputs()["call_buddy_1"] and
            e.text("work/allowed.txt") == "allowed-control\n" and
            "Success. Updated the following files:\nA allowed.txt" in outputs()["call_buddy_2"] and
            e.absent("work/source.txt") and e.text("work/moved.txt") == "changed\n" and
            "Success. Updated the following files:\nM moved.txt" in outputs()["call_buddy_3"])
    def kind(rows, k): return [i for i, v in enumerate(rows) if (v.get("native_event") or {}).get("kind") == k]
    def turn():
        rows = journal()
        wake = [i for i, v in enumerate(rows) if v.get("request_id") == "wake-1" and v["wake_delivery"]["state"] == "delivered"]
        tid = rows[wake[0]]["wake_delivery"]["turn_id"]
        busy, result, idle = kind(rows, "busy"), kind(rows, "result"), kind(rows, "idle")
        return (len(wake) == len(busy) == len(result) == len(idle) == 1 and bool(tid) and
                all(rows[i]["native_event"]["turn_id"] == tid for i in busy + result + idle) and
                wake[0] < busy[0] < result[0] < idle[0] and rows[result[0]]["native_event"]["result_status"] == "completed" and
                rows[idle[0]]["state"]["detail"] == "idle")
    e.check("one_turn", turn)
    e.check("clean_close", lambda: g.close_line(e, "adapter.stderr") and
            g.proc_record(e, "worker.json.proc", identity(), e.root / "worker.json", Path(r["codex_path"])))
    e.check("renewal_receipt_matches_evidence", lambda: g.renewal_receipt_matches(r, *images(), journal()))
    e.check("renewal_capability_declared", lambda: journal()[0]["capabilities"]["claim_renewal"]["support"] == "experimental")
    e.check("renewal_claims_bound", lambda: g.renewal_claims_bound(images(), identity(), ADAPTER_CLAIMS))
    e.check("renewal_written_claims_moved", lambda: g.renewal_moved(*images(), identity(),
                                                                    ["worker-allowed", "worker-move-source", "worker-move-destination"]))
    e.check("renewal_unwritten_claims_unmoved", lambda: g.renewal_unmoved(*images(), ["worker-control", "guarded"]))
    e.check("renewal_journal", lambda: not any("renewal" in v or v.get("operation") == "renewal" for v in e.json("events.json")) and
            g.renewal_applied(e, journal(), identity(), e.json("worker.json.proc")["attempt"], WRITTEN, 3, together=[MOVE]))
    # After the wake was delivered and the turn went busy, before its idle.
    def ordered():
        rows = journal()
        at = [i for i, _ in g.renewal_rows(rows)]
        return bool(at) and kind(rows, "busy")[0] < min(at) and max(at) < kind(rows, "idle")[0]
    e.check("renewal_ordered", ordered)
    binding = {}
    e.check("evidence_hashes", lambda: binding.setdefault("v", g.evidence_binding(e, r, False, optional=g.image_names(e))))
    return e.verdict(evidence=binding.get("v"), read=sorted(e.read_paths), unhashed=g.unhashed_images(e, r), unbound=[])


def adapter_receipt_mutations(r):
    return [(("checks", "renewal_move_destination_claim_moved"), False, "runner_verdict"),
            (("checks", "renewal_same_identity"), g.DELETE, "runner_verdict"),
            (("passed",), False, "runner_verdict"),
            (("mode",), {"adapter": True, "shell_patch": True}, "mode_bound"),
            (("codex_returncode",), 0, "mode_bound"),
            (("codex_version",), "codex-cli 0.162.1", "contract"),
            (("requests",), 5, "contract"),
            (("codex_sha256",), "0" * 64, "artifact_hashes"),
            (("adapter_sha256",), "0" * 64, "artifact_hashes"),
            (("buddy_sha256",), "0" * 64, "artifact_hashes"),
            (("renewal", "after", "worker-move-destination"), 1, "renewal_receipt_matches_evidence"),
            (("qualification",), "installed native Codex with deterministic local provider fixture; no real inference", "mode_bound"),
            (("root",), str(Path(r["root"]) / "work"), "receipt_location")]


BEFORE, AFTER = "renewal-0before.db", "renewal-1after.db"


def synthetic_adapter(root, tag):
    """A coherent SYNTHETIC qualify-codex.py --adapter run under root (F-33). Tests the checker only."""
    root = Path(root)
    root.mkdir(parents=True)
    work = root / "work"
    for name, body in (("bin/buddy", "buddy " + tag), ("bin/buddy-adapter", "adapter " + tag), ("bin/codex", "codex native")):
        g.write(root, name, body)
    sid, inc = "thread-" + tag, "inc-" + tag
    identity = {"schema": "buddy.lifecycle.v1", "harness": "codex", "session_id": sid, "incarnation": inc}
    receipt = root / "worker.json"
    gate = "'%s' hook --receipt '%s' --buddy '%s'" % (root / "bin/buddy-adapter", receipt, root / "bin/buddy")
    key, digest = "%s:pre_tool_use:0:0" % (root / "state/config.toml"), "sha256:" + "6" * 64
    def listing(status):
        return {"data": [{"cwd": str(work), "hooks": [{"key": key, "eventName": "preToolUse", "matcher": MATCHER,
                "trustStatus": status, "currentHash": digest, "command": gate}]}]}
    g.write(root, "hooks-untrusted.json", listing("untrusted"))
    g.write(root, "hooks-trusted.json", listing("trusted"))
    g.write(root, "state/config.toml", 'model = "fixture"\n\n[hooks.state.%s]\ntrusted_hash = %s\n' %
            (json.dumps(key), json.dumps(digest)))
    launch = {"worktree": str(work), "permission_mode": "workspace-write", "hook_config": str(root / "state/config.toml"),
              "harness_version": CODEX_VERSIONS[-1], "gate_command": gate, "gate_hash": digest,
              "hooks": {"configured": "observed", "trusted": "observed", "observed": "unknown", "no_mods": "unknown"}}
    g.write(root, "launch.json", launch)
    g.write(root, "worker.json", {"schema": "buddy.control.v1", "identity": identity, "launch": launch})
    generation, turn = "b" * 32, "turn-" + tag
    def event(op, **fields):
        return dict({"schema": "buddy.control.v1", "operation": op, "identity": identity, "state": {"state": "unknown"}}, **fields)
    def nat(kind, **fields):
        return event("event", native_event=dict({"kind": kind, "native_session_id": sid, "turn_id": turn}, **fields),
                     **({"state": {"state": "observed", "detail": "idle"}} if kind == "idle" else {}))
    def renewal(placed, n):
        return {"launch_generation": generation, "paths": [str(work / p) for p in placed], "placed_paths": placed,
                "renewed_claims": n, "result": {"state": "applied"}, "coverage": {"state": "observed"}}
    start = event("start", capabilities={"claim_renewal": {"support": "experimental"}})
    wake = [event("send", request_id="wake-1", wake_delivery={"state": "unknown"}),
            event("send", request_id="wake-1", wake_delivery={"state": "delivered", "turn_id": turn})]
    batches = [event("renewal", renewal=renewal(["allowed.txt"], 1)), event("renewal", renewal=renewal(list(MOVE), 2))]
    rows = [start] + wake + [nat("busy")] + batches + [nat("result", result_status="completed"), nat("idle")]
    g.write(root, "worker.json.events", g.jsonl(rows))
    g.write(root, "events.json", [start, wake[1], nat("busy"), nat("result", result_status="completed"), nat("idle")])
    outs = [("call_buddy_1", BLOCKED + 'guarded.txt is inside scope "guarded.txt" claimed by session holder'),
            ("call_buddy_2", "Exit code: 0\nOutput:\nSuccess. Updated the following files:\nA allowed.txt\n"),
            ("call_buddy_3", "Exit code: 0\nOutput:\nSuccess. Updated the following files:\nM moved.txt\n")]
    g.write(root, "requests.json", [{"path": "/v1/responses", "tool_outputs": [{"call_id": c, "output": o} for c, o in outs[:n]]}
                                    for n in range(4)])
    g.write(root, "work/guarded.txt", "original\n")
    g.write(root, "work/allowed.txt", "allowed-control\n")
    g.write(root, "work/moved.txt", "changed\n")
    g.write(root, "adapter.stderr", g.close_report())
    g.write(root, "worker.json.proc", dict(g.proc_source(receipt, identity, root / "bin/codex"), attempt=generation))
    holder = ("qualification-holder", "inc-holder-" + tag)
    sessions = [(holder[0], holder[1], str(work)), (sid, inc, str(work))]
    def claims(moved):
        written = 1791000003 if moved else 1791000001
        return [("c0" + tag, "guarded", holder[0], holder[1], 1791000000, 1791000000, "guarded.txt"),
                ("c1" + tag, "worker-allowed", sid, inc, 1791000001, written, "allowed.txt"),
                ("c2" + tag, "worker-move-source", sid, inc, 1791000001, written, "source.txt"),
                ("c3" + tag, "worker-move-destination", sid, inc, 1791000001, written, "moved.txt"),
                ("c4" + tag, "worker-control", sid, inc, 1791000001, 1791000001, "control.txt"),
                ("c5" + tag, "worker-allowed", sid, inc, 1790000000, 1790000000, "allowed.txt", "released")]
    first, second = (BEFORE, AFTER) if tag != "b" else (AFTER, BEFORE)  # never told apart by name
    g.ledger_fixture(root / first, sessions, claims(False), [])
    g.ledger_fixture(root / second, sessions + [("fixture-coordinator", "inc-c", str(work))], claims(True), [(sid, inc, 1791000003)])
    renewed = lambda moved: {c[1]: c[5] for c in claims(moved) if c[7:] != ("released",)}
    r = {"qualification": "installed native Codex through buddy-adapter, deterministic local provider fixture; no real inference",
         "root": str(root), "checks": {k: True for k in ADAPTER_CHECKS},
         "renewal": {"before": renewed(False), "after": renewed(True), "journal": [v["renewal"] for v in batches]},
         "codex_version": "codex-cli " + CODEX_VERSIONS[-1], "buddy_sha256": g.sha_file(root / "bin/buddy"),
         "adapter_sha256": g.sha_file(root / "bin/buddy-adapter"), "codex_path": str(root / "bin/codex"),
         "codex_sha256": g.admission.synthetic("codex", CODEX_VERSIONS[-1], root / "bin/codex"), "mode": {"adapter": True}, "fixture_sha256": "0" * 64,
         "requests": 4, "passed": True}
    g.write(root, "result.json", r)
    return r


def adapter_file_mutations(root, r):
    root = Path(root)
    def edit_json(name, fn):
        def run():
            value = json.loads((root / name).read_text())
            fn(value)
            g.write(root, name, value)
        return run
    def batches(fn):
        return lambda: g.journal_damage(root, r, "worker.json.events", lambda rows: [fn(v["renewal"]) for v in rows if "renewal" in v])
    def drop_destination(v):
        if "moved.txt" in v["placed_paths"]:
            v.update(placed_paths=["source.txt"], paths=[str(root / "work/source.txt")], renewed_claims=1)
    def split_move(rows):
        i = next(i for i, v in enumerate(rows) if "moved.txt" in (v.get("renewal") or {}).get("placed_paths", []))
        half = json.loads(json.dumps(rows[i]))
        rows[i]["renewal"].update(placed_paths=["source.txt"], paths=[str(root / "work/source.txt")], renewed_claims=1)
        half["renewal"].update(placed_paths=["moved.txt"], paths=[str(root / "work/moved.txt")], renewed_claims=1)
        rows.insert(i + 1, half)
    def idle_at(rows): return next(i for i, v in enumerate(rows) if (v.get("native_event") or {}).get("kind") == "idle")
    return [
        ("the move destination's renewal dropped", batches(drop_destination), "renewal_journal"),
        ("the move destination's claim did not move", lambda: g.image_damage(root, r, AFTER, "worker-move-destination", 1791000001),
         "renewal_written_claims_moved"),
        ("the move source's claim did not move", lambda: g.image_damage(root, r, AFTER, "worker-move-source", 1791000001),
         "renewal_written_claims_moved"),
        ("the written claim renewed after the idle", lambda: g.image_damage(root, r, AFTER, "worker-allowed", 1791000004),
         "renewal_written_claims_moved"),
        ("the control claim moved", lambda: g.image_damage(root, r, AFTER, "worker-control", 1791000002),
         "renewal_unwritten_claims_unmoved"),
        ("the held claim moved", lambda: g.image_damage(root, r, AFTER, "guarded", 1791000002), "renewal_unwritten_claims_unmoved"),
        ("the move's two ends in two batches", lambda: g.journal_damage(root, r, "worker.json.events", split_move), "renewal_journal"),
        ("a renewal placed the control path", batches(lambda v: v.update(placed_paths=["control.txt"], paths=[str(root / "work/control.txt")])
                                                      if v["placed_paths"] == ["allowed.txt"] else None), "renewal_journal"),
        ("renewal coverage unknown", batches(lambda v: v.update(coverage={"state": "unknown"})), "renewal_journal"),
        ("renewal from another launch generation", batches(lambda v: v.update(launch_generation="c" * 32)), "renewal_journal"),
        ("a renewal operation on the adapter's stdout", edit_json("events.json",
            lambda rows: rows.insert(3, {"operation": "renewal", "identity": rows[0]["identity"]})), "renewal_journal"),
        ("a renewal record on the adapter's stdout", edit_json("events.json",
            lambda rows: rows[2].update(renewal={})), "renewal_journal"),
        ("renewal journalled after the idle", lambda: g.journal_damage(root, r, "worker.json.events",
            lambda rows: rows.append(rows.pop(next(i for i, _ in g.renewal_rows(rows))))), "renewal_ordered"),
        ("renewal journalled before the turn began", lambda: g.journal_damage(root, r, "worker.json.events",
            lambda rows: rows.insert(1, rows.pop(next(i for i, _ in g.renewal_rows(rows))))), "renewal_ordered"),
        ("the after image is gone", lambda: (root / AFTER).unlink(), "renewal_claims_bound"),
        ("a claim's scope differs", lambda: g.ledger_update(root / AFTER, "UPDATE claim_scopes SET scope = 'moved' "
                                                            "WHERE scope = 'moved.txt'"), "renewal_claims_bound"),
        ("the move did not land", lambda: g.write(root, "work/source.txt", "original\n"), "patches"),
        ("the model was not told of the move", edit_json("requests.json",
            lambda rows: rows[-1]["tool_outputs"].pop()), "patches"),
        ("the held patch landed", lambda: g.write(root, "work/guarded.txt", "conflicting-write\n"), "patches"),
        ("half of the held patch landed", lambda: g.write(root, "work/collateral.txt", "must-not-exist\n"), "patches"),
        ("the hook was trusted before review", edit_json("hooks-untrusted.json",
            lambda v: v["data"][0]["hooks"][0].update(trustStatus="trusted")), "gate_configuration"),
        ("the trusted hook is not the adapter gate", edit_json("hooks-trusted.json",
            lambda v: v["data"][0]["hooks"][0].update(command="/usr/bin/true")), "gate_configuration"),
        ("the reviewed and trusted hook is not the adapter gate", lambda: [edit_json(n,
            lambda v: v["data"][0]["hooks"][0].update(command="/usr/bin/true"))() for n in ("hooks-untrusted.json", "hooks-trusted.json")],
         "gate_configuration"),
        ("the turn never went idle", lambda: g.journal_damage(root, r, "worker.json.events", lambda rows: rows.pop(idle_at(rows))),
         "one_turn"),
        ("the journal names another identity", lambda: g.journal_damage(root, r, "worker.json.events",
            lambda rows: rows[3].update(identity=dict(rows[3]["identity"], incarnation="other"))), "captured_identity"),
        ("an escaped descendant left running", edit_json("worker.json.proc",
            lambda v: v["close"]["survivors"].append({"pid": 9, "start": 9})), "clean_close"),
        ("the owned process is another executable", edit_json("worker.json.proc",
            lambda v: v["executable"].update(path=str(root / "bin/buddy"))), "artifact_hashes"),
        ("renewal capability not declared", lambda: g.journal_damage(root, r, "worker.json.events",
            lambda rows: rows[0]["capabilities"].update(claim_renewal={"support": "unsupported"})), "renewal_capability_declared"),
        ("renewal journalled before the turn went busy", lambda: g.journal_damage(root, r, "worker.json.events",
            lambda rows: rows.insert(2, rows.pop(next(i for i, _ in g.renewal_rows(rows))))), "renewal_ordered"),
        ("the model was told the add failed", edit_json("requests.json",
            lambda rows: [x.update(output="apply_patch verification failed") for q in rows for x in q["tool_outputs"]
                          if x["call_id"] == "call_buddy_2"]), "patches"),
        ("the moved file's content differs", lambda: g.write(root, "work/moved.txt", "original\n"), "patches"),
        ("the denial does not name the held claim", edit_json("requests.json",
            lambda rows: [x.update(output=BLOCKED + "denied") for q in rows for x in q["tool_outputs"] if x["call_id"] == "call_buddy_1"]),
         "patches"),
        ("the trust entry is not in the run's config", lambda: g.write(root, "state/config.toml", 'model = "fixture"\n'),
         "gate_configuration"),
        ("the gate hash is not the trusted one", edit_json("launch.json", lambda v: v.update(gate_hash="sha256:" + "0" * 64)),
         "gate_configuration"),
        ("the gate's receipt is another run's", lambda: regate(root, r), "gate_configuration"),
        ("two native starts", lambda: g.journal_damage(root, r, "worker.json.events", lambda rows: rows.insert(1, rows[0])),
         "captured_identity"),
        ("the turn's result failed", lambda: g.journal_damage(root, r, "worker.json.events",
            lambda rows: [v["native_event"].update(result_status="failed") for v in rows
                          if (v.get("native_event") or {}).get("kind") == "result"]), "one_turn"),
        ("the run is another Codex version", edit_json("launch.json", lambda v: v.update(harness_version="0.161.0")), "contract"),
        ("stdout names another identity", edit_json("events.json",
            lambda rows: rows[1].update(identity=dict(rows[1]["identity"], incarnation="other"))), "captured_identity"),
        ("the result came before busy", lambda: g.journal_damage(root, r, "worker.json.events",
            lambda rows: rows.insert(3, rows.pop(next(i for i, v in enumerate(rows)
                                                      if (v.get("native_event") or {}).get("kind") == "result")))), "one_turn"),
        ("a second idle", lambda: g.journal_damage(root, r, "worker.json.events", lambda rows: rows.append(rows[-1])), "one_turn"),
        ("no close report", lambda: g.write(root, "adapter.stderr", ""), "clean_close"),
        ("the idle names another turn", lambda: g.journal_damage(root, r, "worker.json.events",
            lambda rows: [v["native_event"].update(turn_id="other") for v in rows
                          if (v.get("native_event") or {}).get("kind") == "idle"]), "one_turn"),
    ]


def regate(root, r):
    """The gate command (and its trusted listing) naming another run's receipt."""
    root = Path(root)
    launch = json.loads((root / "launch.json").read_text())
    gate = launch["gate_command"].replace(str(root / "worker.json"), str(root.parent / "run-b/worker.json"))
    launch["gate_command"] = gate
    g.write(root, "launch.json", launch)
    for name in ("hooks-untrusted.json", "hooks-trusted.json"):
        v = json.loads((root / name).read_text())
        v["data"][0]["hooks"][0]["command"] = gate
        g.write(root, name, v)
    worker = json.loads((root / "worker.json").read_text())
    worker["launch"]["gate_command"] = gate
    g.write(root, "worker.json", worker)
    return r


ADAPTER_SPLICE = [("worker.json.events", "captured_identity"), (AFTER, "renewal_claims_bound"), ("worker.json.proc", "clean_close")]


def adapter_self_test():
    return g.self_test_suite("codex-adapter", validate, synthetic_adapter, receipt_mutations, adapter_file_mutations,
                             ADAPTER_SPLICE)


def synthetic_self_test():
    gate_mode, adapter = gate_self_test(), adapter_self_test()
    adapter["rejected_mutations"] += g.unhashed_image_self_test(validate, synthetic_adapter)
    return {"positive": "passed", "rejected_mutations": gate_mode["rejected_mutations"] + adapter["rejected_mutations"],
            "modes": {"gate": gate_mode["rejected_mutations"], "adapter": adapter["rejected_mutations"]},
            "evidence": gate_mode["evidence"]}


if __name__ == "__main__":
    sys.exit(g.main_for(__doc__, validate, synthetic_self_test, receipt_mutations))
