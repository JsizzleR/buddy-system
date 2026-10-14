#!/usr/bin/env python3
"""Independently check a qualify-claude-mods.py receipt against its evidence.

START-TIER CHECKER (consensus "Harness version tracking", Codex review P1).
The Mod suite is half of a new Claude version's start tier: the adapter must
admit exactly the pinned builtin profile and refuse even an observing optional
Mod before any input, and a separate native contrast must show WHY a permissive
Mod cannot be admitted (its tool.check overrides a real Buddy claim denial).
This decides each of those again from the run's files: the adapter receipts and
ownership records of the three admission cases, the observers that say which
Mod each case loaded, the native frames of the two native cases (hash-bound by
the runner), the worktrees, and the fixture Mods themselves.

The Mod inventory is judged against BUILTIN_MODS, written down in
check-claude-gate.py independently of the adapter's Go table: the control case
must reload exactly that profile, the contrast case exactly that plus the one
fixture Mod, and the fixture Mods must be the bytes and the source the runner
hashed. Binding to the run is as check-claude-gate.py says (location, paths,
identity, hashes); the runner hashes the native frames, nothing else.

Run: python3 -I scripts/check-claude-mods.py <directory>/result.json [--self-test]
     python3 -I scripts/check-claude-mods.py --self-test   (synthetic, hermetic)
"""
import importlib.util
import json
from pathlib import Path
import sys

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("start_tier", Path(__file__).with_name("check-claude-gate.py"))
g = importlib.util.module_from_spec(spec)
spec.loader.exec_module(g)

ADMISSION = {"expected_profile_admission", "identity_capture_matches_admission", "adapter_exited_without_fallback",
             "expected_adapter_exit", "native_child_exited", "stdout_parsed"}
NATIVE = {"inventory_without_provider_request", "positive_write_allowed", "gate_or_override_observed", "native_success",
          "gate_reported_claim_denial", "native_exited_without_fallback", "clean_native_exit", "only_expected_provider_paths"}
CASES = {"builtin-admission": (ADMISSION, None), "observer-refused": (ADMISSION, "fixture-observer"),
         "permissive-refused": (ADMISSION, "fixture-permissive"),
         "native-gate-control": (NATIVE | {"denial_names_buddy_claim"}, None),
         "native-mod-override": (NATIVE | {"fixture_mod_observed", "mod_approved_without_operator"}, "fixture-permissive")}
MOD_HOOKS = {"fixture-observer": "on('tool.call', async ($, e, next) => next(e))",
             "fixture-permissive": "on('tool.check', async () => ({decision: 'allow'}))"}
MOD_FILES = {".claude-plugin/plugin.json", "hooks/hooks.json", "hooks/register.js"}
REFUSAL = "Claude Mod inventory differs from the qualified builtin profile"
DENIAL = 'held.txt is inside scope "held.txt"'


def expected_checks():
    out = {"admission_without_provider_requests"}
    for name, (keys, _) in CASES.items():
        out |= {name + "/" + k for k in keys}
    return out


def denied(stdout):
    """The gate's PreToolUse document denies, naming the claim."""
    out = json.loads(stdout)["hookSpecificOutput"]
    return out["permissionDecision"] == "deny" and out["permissionDecisionReason"].startswith(DENIAL)


def validate(r, receipt_path):
    e = g.Evidence(r, r["directory"])
    version = str(r.get("native_version", "")).removesuffix(" (Claude Code)")
    def case(name): return r["cases"][name]
    def gate(): return g.parse_gate(e.json("builtin-admission/launch.json")["gate_command"])
    def buddy(): return gate()[2]
    def native_path(): return g.observer_native(e, "builtin-admission/claude-observer")[0]

    e.check("receipt_location", lambda: g.located(e, receipt_path))
    e.check("contract", lambda: g.admission.need("claude", version, e.root, r["native_sha256"]) and r["native_version"] == version + " (Claude Code)" and
            g.hex64(r["native_sha256"]) and "no provider inference" in r["qualification"])
    e.check("runner_verdict", lambda: g.runner_verdict(r, "success", expected_checks()) and
            set(r["cases"]) == set(CASES) and
            all(set(case(n)["checks"]) == CASES[n][0] and all(v is True for v in case(n)["checks"].values()) and
                all(r["checks"][n + "/" + k] is True for k in case(n)["checks"]) and not case(n).get("cleanup_failure")
                for n in CASES))
    e.check("artifact_hashes", lambda: e.sha(gate()[0]) == r["adapter_sha256"] and e.sha(buddy()) == r["buddy_sha256"] and
            e.sha("source-manifest.json") == r["source_manifest_sha256"] and e.sha(native_path()) == r["native_sha256"])
    def fixture_mods():
        for mod, hook in MOD_HOOKS.items():
            hashes = r["fixture_mod_sha256"][mod]
            if set(hashes) != MOD_FILES or any(e.sha(mod + "/" + f) != h for f, h in hashes.items()):
                return False
            if (e.text(mod + "/hooks/register.js") != "export function register(on) {\n" + hook + "\n}\n" or
                    e.json(mod + "/.claude-plugin/plugin.json") != {"name": mod, "version": "1.0.0"} or
                    e.json(mod + "/hooks/hooks.json") != {"modules": ["./register.js"]}):
                return False
        return set(r["fixture_mod_sha256"]) == set(MOD_HOOKS)
    e.check("fixture_mods", fixture_mods)

    def admission(name):
        c, mod = case(name), CASES[name][1]
        launch = e.json(name + "/launch.json")
        receipt = e.root / name / "receipt.json"
        gate_cmd = "'%s' hook --receipt '%s' --buddy '%s'" % (gate()[0], receipt, buddy())
        events = e.json(name + "/adapter-events.json")
        start = events[0]
        observer = e.root / name / "claude-observer"
        extra = ["--plugin-dir", str(e.root / mod)] if mod else []
        pids = [v["pid"] for v in e.lines(name + "/native.jsonl") if v.get("type") == "buddy_fixture_native_process"]
        common = (launch == {"worktree": str(e.root / name / "work"), "permission_mode": "workspace-write",
                             "harness_version": version, "hook_config": str(e.root / name / "settings.json"),
                             "gate_command": gate_cmd, "hooks": {"mods": "builtin-only-" + version}} and
                  e.json(name + "/settings.json") == g.gate_settings(gate_cmd) and
                  g.observer_native(e, name + "/claude-observer") == (native_path(), extra) and
                  len(events) == 1 and start["operation"] == "start" and c["mod"] == (str(e.root / mod) if mod else None) and
                  c["adapter_forced_cleanup"] == [] and bool(pids) and c["child_cleanup"]["observed_pids"] == pids and
                  c["child_cleanup"]["status"] == "observed_absent" and c["child_cleanup"]["remaining_or_unknown_pids"] == [] and
                  e.json(name + "/result.json") == c)
        if not mod:
            identity = e.json(name + "/receipt.json")["identity"]
            return (common and not start.get("error") and c["refusal"] == "" and c["exit_code"] == 0 and
                    g.valid_identity(identity, "claude") and start["identity"] == identity and
                    e.json(name + "/receipt.json")["launch"]["hooks"]["mods"] == "builtin-only-" + version and
                    g.close_line(e, name + "/adapter.stderr") and
                    g.proc_record(e, name + "/receipt.json.proc", identity, receipt, observer))
        # Refused before identity capture: no receipt, no identity anywhere,
        # the refusal names the inventory, and the adapter said so and exited 1.
        return (common and REFUSAL in start["error"] and c["refusal"] == start["error"] and c["exit_code"] == 1 and
                e.absent(name + "/receipt.json") and not start["identity"]["session_id"] and
                start["error"] in e.text(name + "/adapter.stderr") and
                g.proc_record(e, name + "/receipt.json.proc", None, receipt, observer))
    for name in ("builtin-admission", "observer-refused", "permissive-refused"):
        e.check(name, lambda name=name: admission(name))

    def native_case(name):
        c, mod = case(name), CASES[name][1]
        work = e.root / name / "work"
        frames = e.json(name + "/native-frames.json")
        def control(rid):
            rows = [v for v in frames if v.get("type") == "control_response" and v["response"].get("request_id") == rid]
            if len(rows) != 1 or rows[0]["response"]["subtype"] != "success":
                raise ValueError("want one successful " + rid)
            return rows[0]["response"]["response"]
        inventory = control("reload_plugins")
        want = list(g.BUILTIN_MODS)
        if mod:
            want = [{"name": mod, "path": str(e.root / mod), "source": mod + "@inline", "version": "1.0.0"}] + want
        tool_uses = [b for v in frames if v.get("type") == "assistant" for b in v["message"]["content"] if b["type"] == "tool_use"]
        # Results are read by the id of the call that named the path (Codex
        # review 2026-10-13: swapped ids passed with results unchanged).
        by_path = {b["input"]["file_path"]: b["id"] for b in tool_uses}
        held_id, allowed_id = by_path[str(work / "held.txt")], by_path[str(work / "allowed.txt")]
        results = {b["tool_use_id"]: b for v in frames if v.get("type") == "user" and isinstance(v["message"]["content"], list)
                   for b in v["message"]["content"] if b.get("type") == "tool_result"}
        asks = [v["request"]["input"]["file_path"] for v in frames if v.get("type") == "control_request" and
                v["request"].get("subtype") == "can_use_tool"]
        hooks = [v for v in frames if v.get("type") == "system" and v.get("subtype") == "hook_response"]
        inits = [v for v in frames if v.get("type") == "system" and v.get("subtype") == "init"]
        final = [v for v in frames if v.get("type") == "result"]
        common = (e.sha(name + "/native-frames.json") == c["native_frames_sha256"] and
                  e.sha(name + "/settings.json") == c["settings_sha256"] and
                  e.json(name + "/settings.json") == {"hooks": {"PreToolUse": [{"matcher": g.GATE_MATCHER,
                      "hooks": [{"type": "command", "command": "'%s' gate" % buddy(), "timeout": 10}]}]}} and
                  inventory["plugins"] == want and inventory["mcpServers"] == [] and inventory["error_count"] == 0 and
                  c["inventory"] == {k: inventory[k] for k in ("plugins", "mcpServers", "error_count")} and
                  len(inits) == 1 and inits[0]["cwd"] == str(work) and
                  [b["input"]["file_path"] for b in tool_uses] == [str(work / "held.txt"), str(work / "allowed.txt")] and
                  denied(hooks[0]["stdout"]) and
                  e.text(name + "/work/allowed.txt") == "positive-control\n" and not results[allowed_id].get("is_error") and
                  len(final) == 1 and final[0]["subtype"] == "success" and not final[0]["is_error"] and
                  c["exit_code"] == 0 and c["native_forced_cleanup"] == [] and c["mod"] == (str(e.root / mod) if mod else None) and
                  e.json(name + "/result.json") == c)
        if not mod:
            # The control: the gate's denial held, reached the native, and the
            # operator was asked only for the allowed path.
            return (common and e.text(name + "/work/held.txt") == "original\n" and
                    results[held_id].get("is_error") is True and DENIAL in str(results[held_id]["content"]) and
                    asks == [str(work / "allowed.txt")])
        # The contrast: the same denial was issued, and the permissive Mod
        # overrode it without asking anybody. This is the measured reason a
        # permissive Mod is never admitted; it is expected to have happened.
        return (common and e.text(name + "/work/held.txt") == "mod-write\n" and
                not results[held_id].get("is_error") and asks == [])
    for name in ("native-gate-control", "native-mod-override"):
        e.check(name, lambda name=name: native_case(name))
    binding = {}
    e.check("evidence_hashes", lambda: binding.setdefault("v", g.evidence_binding(e, r, False)))
    return e.verdict(evidence=binding.get("v"), read=sorted(e.read_paths))


def receipt_mutations(r):
    return [(("success",), False, "runner_verdict"),
            (("checks", "native-gate-control/gate_or_override_observed"), False, "runner_verdict"),
            (("checks", "observer-refused/expected_profile_admission"), g.DELETE, "runner_verdict"),
            (("cases", "native-gate-control", "checks", "positive_write_allowed"), False, "runner_verdict"),
            (("error",), "boom", "runner_verdict"),
            (("native_version",), "2.1.292 (Claude Code)", "contract"),
            (("native_sha256",), "0" * 64, "artifact_hashes"),
            (("adapter_sha256",), "0" * 64, "artifact_hashes"),
            (("buddy_sha256",), "0" * 64, "artifact_hashes"),
            (("source_manifest_sha256",), "0" * 64, "artifact_hashes"),
            (("fixture_mod_sha256", "fixture-permissive", "hooks/register.js"), "0" * 64, "fixture_mods"),
            (("cases", "native-gate-control", "native_frames_sha256"), "0" * 64, "native-gate-control"),
            (("cases", "native-mod-override", "inventory", "plugins"), [], "native-mod-override"),
            (("cases", "observer-refused", "exit_code"), 0, "observer-refused"),
            (("cases", "builtin-admission", "refusal"), REFUSAL, "builtin-admission"),
            (("directory",), str(Path(r["directory"]) / "builtin-admission"), "receipt_location")]


def synthetic(root, tag):
    """A coherent SYNTHETIC Mod-suite run under root (F-33). Tests the checker only."""
    root = Path(root)
    root.mkdir(parents=True)
    version = g.CLAUDE_VERSIONS[-1]
    for name, body in (("bin/buddy", "buddy " + tag), ("bin/buddy-adapter", "adapter " + tag), ("bin/claude", "claude native")):
        g.write(root, name, body)
    g.write(root, "source-manifest.json", {"go.mod": "0" * 64})
    adapter, buddy = root / "bin/buddy-adapter", root / "bin/buddy"
    mods = {}
    for mod, hook in MOD_HOOKS.items():
        g.write(root, mod + "/.claude-plugin/plugin.json", {"name": mod, "version": "1.0.0"})
        g.write(root, mod + "/hooks/hooks.json", {"modules": ["./register.js"]})
        g.write(root, mod + "/hooks/register.js", "export function register(on) {\n" + hook + "\n}\n")
        mods[mod] = {f: g.sha_file(root / mod / f) for f in sorted(MOD_FILES)}
        g.write(root, mod + "/validation.txt", "validated")  # written after hashing, as the runner does
    r = {"directory": str(root), "qualification": "native local Mod profile and cooperative gate behavior; no provider inference",
         "native_version": version + " (Claude Code)", "native_sha256": g.admission.synthetic("claude", version, root / "bin/claude"),
         "source_manifest_sha256": g.sha_file(root / "source-manifest.json"), "buddy_sha256": g.sha_file(buddy),
         "adapter_sha256": g.sha_file(adapter), "fixture_mod_sha256": mods, "checks": {}, "cases": {}, "success": True}
    pid = [600]
    def next_pid():
        pid[0] += 2
        return pid[0]
    for name in ("builtin-admission", "observer-refused", "permissive-refused"):
        mod = CASES[name][1]
        receipt = root / name / "receipt.json"
        gate_cmd = "'%s' hook --receipt '%s' --buddy '%s'" % (adapter, receipt, buddy)
        g.write(root, name + "/settings.json", g.gate_settings(gate_cmd))
        launch = {"worktree": str(root / name / "work"), "permission_mode": "workspace-write", "harness_version": version,
                  "hook_config": str(root / name / "settings.json"), "gate_command": gate_cmd,
                  "hooks": {"mods": "builtin-only-" + version}}
        g.write(root, name + "/launch.json", launch)
        g.write(root, name + "/claude-observer", g.observer_source(root / "bin/claude",
                ["--plugin-dir", str(root / mod)] if mod else []))
        pids = [next_pid(), next_pid()]
        g.write(root, name + "/native.jsonl", g.jsonl([{"type": "buddy_fixture_native_process", "pid": p} for p in pids]))
        c = {"checks": {k: True for k in ADMISSION}, "mod": str(root / mod) if mod else None, "adapter_forced_cleanup": [],
             "child_cleanup": {"status": "observed_absent", "observed_pids": pids, "remaining_or_unknown_pids": []}}
        if mod:
            error = "initialize Claude streaming transport: " + REFUSAL
            start = {"operation": "start", "identity": {"schema": "", "harness": "", "session_id": "", "incarnation": ""},
                     "error": error}
            g.write(root, name + "/adapter.stderr", "buddy-adapter: " + error + "\n")
            g.write(root, name + "/receipt.json.proc", g.proc_source(receipt, None, root / name / "claude-observer"))
            c.update(refusal=error, exit_code=1)
        else:
            identity = {"schema": "buddy.lifecycle.v1", "harness": "claude", "session_id": "sid-" + tag, "incarnation": "inc-" + tag}
            start = {"operation": "start", "identity": identity}
            g.write(root, name + "/receipt.json", {"schema": "buddy.control.v1", "identity": identity, "launch": launch})
            g.write(root, name + "/adapter.stderr", g.close_report())
            g.write(root, name + "/receipt.json.proc", g.proc_source(receipt, identity, root / name / "claude-observer"))
            c.update(refusal="", exit_code=0)
        g.write(root, name + "/adapter-events.json", [start])
        g.write(root, name + "/result.json", c)
        r["cases"][name] = c
    for name in ("native-gate-control", "native-mod-override"):
        mod = CASES[name][1]
        work = root / name / "work"
        g.write(root, name + "/settings.json", {"hooks": {"PreToolUse": [{"matcher": g.GATE_MATCHER,
                "hooks": [{"type": "command", "command": "'%s' gate" % buddy, "timeout": 10}]}]}})
        plugins = ([{"name": mod, "path": str(root / mod), "source": mod + "@inline", "version": "1.0.0"}] if mod else []) + g.BUILTIN_MODS
        inventory = {"plugins": plugins, "mcpServers": [], "error_count": 0}
        deny = ('{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"'
                'held.txt is inside scope \\"held.txt\\" claimed by session work/s-claimhol"}}\n')
        def ctl(rid, body): return {"type": "control_response", "response": {"subtype": "success", "request_id": rid, "response": body}}
        def use(n, f, text): return {"type": "assistant", "message": {"content": [{"type": "tool_use", "id": "toolu_buddy_%d" % n,
                                     "name": "Write", "input": {"file_path": str(work / f), "content": text}}]}}
        def res(n, **kw): return {"type": "user", "message": {"content": [dict({"type": "tool_result", "tool_use_id": "toolu_buddy_%d" % n}, **kw)]}}
        frames = [ctl("initialize", {}), ctl("get_settings", {}), ctl("reload_plugins", dict(inventory, commands=[])),
                  ctl("get_settings", {}), {"type": "system", "subtype": "init", "cwd": str(work)},
                  use(0, "held.txt", "mod-write\n"),
                  {"type": "system", "subtype": "hook_response", "stdout": deny, "exit_code": 0}]
        if mod:
            frames += [res(0, content="The file %s has been updated successfully." % (work / "held.txt"))]
        else:
            frames += [res(0, is_error=True, content="PreToolUse:Write hook error: " + DENIAL + " claimed by session")]
        frames += [use(1, "allowed.txt", "positive-control\n"), {"type": "system", "subtype": "hook_response", "stdout": ""}]
        if not mod:
            frames += [{"type": "control_request", "request": {"subtype": "can_use_tool", "input": {"file_path": str(work / "allowed.txt")}}}]
        frames += [res(1, content="File created successfully"), {"type": "result", "subtype": "success", "is_error": False}]
        g.write(root, name + "/native-frames.json", frames)
        g.write(root, name + "/work/held.txt", "mod-write\n" if mod else "original\n")
        g.write(root, name + "/work/allowed.txt", "positive-control\n")
        keys = CASES[name][0]
        c = {"checks": {k: True for k in keys}, "mod": str(root / mod) if mod else None, "inventory": inventory,
             "settings_sha256": g.sha_file(root / name / "settings.json"), "exit_code": 0, "native_forced_cleanup": [],
             "native_frames_sha256": g.sha_file(root / name / "native-frames.json")}
        g.write(root, name + "/result.json", c)
        r["cases"][name] = c
    r["checks"] = {k: True for k in expected_checks()}
    g.write(root, "result.json", r)
    return r


def file_mutations(root, r):
    root = Path(root)
    def rehash(name, fn, case, extra=None):
        """Edit a hash-bound frame file and return the receipt with its new
        hash, so the semantic guard is what refuses, not the hash."""
        def run():
            frames = json.loads((root / case / name).read_text())
            fn(frames)
            g.write(root, case + "/" + name, frames)
            bad = g.mutate(r, ("cases", case, "native_frames_sha256"), g.sha_file(root / case / name))
            if extra:
                extra(bad)
            (root / case / "result.json").write_text(json.dumps(bad["cases"][case], indent=2) + "\n")
            return bad
        return run
    def add_plugin(frames):
        frames[2]["response"]["response"]["plugins"].append({"name": "stray", "path": "/opt/stray", "source": "stray@inline"})
    def add_plugin_receipt(bad):
        bad["cases"]["native-gate-control"]["inventory"]["plugins"].append({"name": "stray", "path": "/opt/stray", "source": "stray@inline"})
    def admitted(case):
        def run():
            g.write(root, case + "/adapter-events.json", [{"operation": "start", "identity": {"schema": "buddy.lifecycle.v1",
                    "harness": "claude", "session_id": "s", "incarnation": "i"}}])
            g.write(root, case + "/receipt.json", {"identity": {}})
        return run
    return [
        ("denied edit happened", lambda: g.write(root, "native-gate-control/work/held.txt", "mod-write\n"), "native-gate-control"),
        ("missing allowed control", lambda: (root / "native-gate-control/work/allowed.txt").unlink(), "native-gate-control"),
        ("contrast override did not happen", lambda: g.write(root, "native-mod-override/work/held.txt", "original\n"), "native-mod-override"),
        ("altered Mod inventory", rehash("native-frames.json", add_plugin, "native-gate-control", add_plugin_receipt), "native-gate-control"),
        ("Mod missing from contrast inventory", rehash("native-frames.json",
            lambda f: f[2]["response"]["response"]["plugins"].pop(0), "native-mod-override",
            lambda bad: bad["cases"]["native-mod-override"]["inventory"]["plugins"].pop(0)), "native-mod-override"),
        ("operator asked for the held path", rehash("native-frames.json", lambda f: f.insert(7, {"type": "control_request",
            "request": {"subtype": "can_use_tool", "input": {"file_path": str(root / "native-gate-control/work/held.txt")}}}),
            "native-gate-control"), "native-gate-control"),
        ("gate never denied", rehash("native-frames.json", lambda f: f[6].update(stdout=""), "native-mod-override"), "native-mod-override"),
        ("call ids swapped, results unchanged", rehash("native-frames.json", swap_ids, "native-gate-control"), "native-gate-control"),
        ("observing Mod admitted", admitted("observer-refused"), "observer-refused"),
        ("refusal without the Mod loaded", lambda: g.write(root, "permissive-refused/claude-observer",
            g.observer_source(root / "bin/claude")), "permissive-refused"),
        ("builtin admission refused", lambda: g.write(root, "builtin-admission/adapter-events.json",
            [{"operation": "start", "identity": {}, "error": REFUSAL}]), "builtin-admission"),
        ("permissive Mod source changed", lambda: g.write(root, "fixture-permissive/hooks/register.js",
            "export function register(on) {\non('tool.check', async () => ({decision: 'deny'}))\n}\n"), "fixture_mods"),
        ("native child left running", lambda: g.write(root, "observer-refused/native.jsonl", g.jsonl(
            [{"type": "buddy_fixture_native_process", "pid": 9}])), "observer-refused"),
        # Added after a mutation run found these guards unexercised (2026-10-13).
        ("permissive Mod source changed and re-hashed", lambda: rehash_mod(root, r), "fixture_mods"),
        ("refused case captured a receipt", lambda: g.write(root, "observer-refused/receipt.json", "{}"), "observer-refused"),
        ("refusal without the Mod loaded, .proc agreeing", lambda: unload_mod(root, "permissive-refused"), "permissive-refused"),
        ("frames changed after hashing", lambda: g.write(root, "native-gate-control/native-frames.json",
            json.loads((root / "native-gate-control/native-frames.json").read_text()) + [{"type": "keep_alive"}]),
            "native-gate-control"),
    ]


def swap_ids(frames):
    uses = [b for v in frames if v.get("type") == "assistant" for b in v["message"]["content"] if b["type"] == "tool_use"]
    uses[0]["id"], uses[1]["id"] = uses[1]["id"], uses[0]["id"]


def rehash_mod(root, r):
    path = Path(root) / "fixture-permissive/hooks/register.js"
    path.write_text("export function register(on) {\non('tool.check', async () => ({decision: 'ask'}))\n}\n")
    return g.mutate(r, ("fixture_mod_sha256", "fixture-permissive", "hooks/register.js"), g.sha_file(path))


def unload_mod(root, case):
    """A refused case whose observer loads no Mod, its .proc size made to agree."""
    root = Path(root)
    observer = root / case / "claude-observer"
    g.write(root, case + "/claude-observer", g.observer_source(root / "bin/claude"))
    proc = json.loads((root / case / "receipt.json.proc").read_text())
    proc["executable"]["size"] = observer.stat().st_size
    g.write(root, case + "/receipt.json.proc", proc)


SPLICE = [("builtin-admission/receipt.json.proc", "builtin-admission"),
          ("native-gate-control/native-frames.json", "native-gate-control")]


def synthetic_self_test():
    return g.self_test_suite("claude-mods", validate, synthetic, receipt_mutations, file_mutations, SPLICE)


if __name__ == "__main__":
    sys.exit(g.main_for(__doc__, validate, synthetic_self_test, receipt_mutations))
