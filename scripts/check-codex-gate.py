#!/usr/bin/env python3
"""Independently check a qualify-codex.py receipt against the evidence it left.

START-TIER CHECKER (consensus "Harness version tracking", Codex review P1), for
the Codex half of the start tier: `qualify-codex.py --app-server --lifecycle
--edge-cases --shell-patch`. Only that full mode is accepted; a receipt from a
narrower run is missing the shell-routed patch (D-073) or the edge cases, and
is refused rather than read as a smaller pass.

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

CODEX_VERSIONS = ("0.160.0",)
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


def validate(r, receipt_path):
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
    e.check("contract", lambda: version in CODEX_VERSIONS and r["codex_version"] == "codex-cli " + version and
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


def receipt_mutations(r):
    return [(("checks", "unreadable_ledger_denied"), False, "runner_verdict"),
            (("checks", "shell_routed_patch_hook_denied"), g.DELETE, "runner_verdict"),
            (("codex_returncode",), 1, "runner_verdict"),
            (("codex_version",), "codex-cli 0.162.1", "contract"),
            (("requests",), 4, "contract"),
            (("buddy_sha256",), "0" * 64, "artifact_hashes"),
            (("codex_sha256",), "0" * 64, "artifact_hashes"),
            (("mode",), {"app_server": True, "lifecycle": True, "edge_cases": True, "shell_patch": False}, "artifact_hashes"),
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


def synthetic_self_test():
    return g.self_test_suite("codex-gate", validate, synthetic, receipt_mutations, file_mutations, SPLICE)


if __name__ == "__main__":
    sys.exit(g.main_for(__doc__, validate, synthetic_self_test, receipt_mutations))
