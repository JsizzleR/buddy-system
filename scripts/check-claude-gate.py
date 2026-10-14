#!/usr/bin/env python3
"""Independently check a qualify-claude.py receipt against the evidence it left.

START-TIER CHECKER (consensus "Harness version tracking", Codex review P1). An
admission record for a new Claude version will rest on this suite, and until
now nothing but the runner's own `passed: true` said it passed. This reads the
run's files (adapter receipts, the provider log, the worktree, the ownership
record, the managed-policy case) and decides every verdict again from them. It
never launches a native process, opens no ledger and trusts no runner check:
the runner's `checks` are only required to be complete and all true, so a
receipt that says passed while one of its own cases failed is refused too.

What binds a receipt to ITS run (F-13, F-37). The runner records no hash of
the evidence it leaves, so a run is bound by where it lives and by what its
files name: the receipt must be <root>/result.json; every path the launch, the
gate command, the ownership record and the approvals name is under that root;
one captured identity runs through the receipt, the journal, the ownership
record and every adapter event; and the built artifacts, settings, source
manifest, observers and the native executable they name hash to the receipt's
values. A case directory copied in whole from another run of the same build
still names that run's root and its identity, and is refused. If a runner adds
`evidence_sha256` (the runner .diff this lane proposes), every file read here
must be listed and match; until then the output says `unhashed`.

This file also holds the helpers the other start-tier checkers load
(check-claude-mods.py, check-codex-gate.py, check-adapters.py,
check-claude-jobs.py), the way the resume checkers load check-mixed-fleet.py.

Run: python3 -I scripts/check-claude-gate.py <root>/result.json [--self-test]
     python3 -I scripts/check-claude-gate.py --self-test   (synthetic, hermetic)
"""
import argparse
import copy
import hashlib
import json
import os
from pathlib import Path
import shlex
import shutil
import sys
import tempfile

sys.dont_write_bytecode = True

# The exact native contracts the adapter's own table admits today
# (internal/harness/claude.go). A new version is added here as part of its
# admission, never by a range.
CLAUDE_VERSIONS = ("2.1.289", "2.1.290", "2.1.291")
# The pinned builtin Mod profile, written down here independently of the Go
# admission code so a change to either is a disagreement somebody sees.
BUILTIN_MODS = [{"name": "cc-plugin-agents-md", "path": "builtin", "source": "cc-plugin-agents-md@builtin"},
                {"name": "cc-plugin-plugin-authoring", "path": "builtin", "source": "cc-plugin-plugin-authoring@builtin"}]
GATE_MATCHER = "^(Edit|Write|NotebookEdit)$"
HEX64 = set("0123456789abcdef")
CLOSE_PREFIX = "buddy-adapter: worker closed ("


class Evidence:
    """One run's evidence root, path containment and named verdicts.

    Like check-mixed-fleet.Evidence, but any exception in a predicate is a
    failed check rather than a crash: a damaged receipt must be REFUSED, and
    an AttributeError on a field of the wrong type is just another damage.
    """

    def __init__(self, receipt, root):
        self.receipt = receipt
        self.root = Path(root).resolve(strict=True)
        if self.root == Path(self.root.anchor) or not self.root.is_dir():
            raise ValueError("receipt root must be an artifact directory")
        self.checks, self.errors, self.read_paths, self._sha = {}, [], set(), {}

    def path(self, value):
        path = Path(value)
        if not path.is_absolute():
            path = self.root / path
        if path.is_symlink():
            raise ValueError("evidence path is a link: " + str(value))
        path = path.resolve(strict=True)
        if path == self.root or self.root not in path.parents:
            raise ValueError("evidence path leaves artifact root: " + str(value))
        self.read_paths.add(os.path.relpath(path, self.root))
        return path

    def inside(self, value):
        """A path string names a file under this root."""
        path = Path(value)
        return path.is_absolute() and self.root in path.resolve().parents

    def text(self, name):
        return self.path(name).read_text()

    def json(self, name):
        return json.loads(self.path(name).read_bytes())

    def lines(self, name):
        return [json.loads(v) for v in self.text(name).splitlines() if v.strip()]

    def absent(self, name):
        path = self.root / name
        return not path.exists() and not path.is_symlink()

    def sha(self, path):
        """sha256 of any file a receipt names (artifacts may live outside root)."""
        path = Path(path)
        if not path.is_absolute():
            path = self.path(path)
        key = str(path)
        if key not in self._sha:
            h = hashlib.sha256()
            with open(path, "rb") as f:
                for chunk in iter(lambda: f.read(1 << 20), b""):
                    h.update(chunk)
            self._sha[key] = h.hexdigest()
        return self._sha[key]

    def check(self, name, predicate):
        try:
            ok = bool(predicate())
            if not ok:
                self.errors.append(name + ": required evidence does not match")
        except Exception as error:  # noqa: BLE001 - every damage is a refusal
            ok = False
            self.errors.append(name + ": " + type(error).__name__ + ": " + str(error))
        self.checks[name] = ok
        return ok

    def verdict(self, **extra):
        out = {"passed": bool(self.checks) and all(self.checks.values()), "checks": self.checks, "errors": self.errors}
        out.update(extra)
        return out


def hex64(value):
    return isinstance(value, str) and len(value) == 64 and set(value) <= HEX64


def located(e, receipt_path):
    """The receipt is <root>/result.json: a receipt lifted into another run's
    directory names its own root, not the one it was found in. The directory
    it was FOUND in is compared as well as the one it resolves into (Codex
    review 2026-10-13: run-b/result.json linked to run A's receipt resolved
    into run A and passed as run B's)."""
    path = Path(receipt_path)
    return (path.name == "result.json" and path.parent.resolve(strict=True) == e.root and
            path.resolve(strict=True).parent == e.root)


def runner_verdict(r, flag, expected):
    """The runner said passed, said it for the whole expected case set, and none
    of its own cases failed. Never sufficient; always necessary."""
    checks = r["checks"]
    return (r[flag] is True and isinstance(checks, dict) and set(checks) == set(expected) and
            all(v is True for v in checks.values()) and not r.get("failure") and not r.get("error") and
            not r.get("cleanup_failure") and not r.get("cleanup_error"))


def parse_gate(command):
    """'<adapter>' hook --receipt '<receipt>' --buddy '<buddy>' -> (adapter, receipt, buddy)."""
    argv = shlex.split(command)
    if len(argv) != 6 or argv[1:3] != ["hook", "--receipt"] or argv[4] != "--buddy":
        raise ValueError("gate command is not the adapter hook form")
    return argv[0], argv[3], argv[5]


def gate_settings(gate):
    return {"enabledPlugins": {}, "hooks": {"PreToolUse": [{"matcher": GATE_MATCHER,
            "hooks": [{"type": "command", "command": gate, "timeout": 10}]}]}}


# What a native_observer may call to start a process: exactly one Popen.
LAUNCHERS = {"Popen", "run", "call", "check_call", "check_output", "getoutput", "getstatusoutput", "system",
             "popen", "fork", "forkpty", "posix_spawn", "posix_spawnp", "exec", "eval", "compile", "__import__",
             "getattr", "execl", "execle", "execlp", "execlpe", "execv", "execve", "execvp", "execvpe",
             "spawnl", "spawnle", "spawnlp", "spawnlpe", "spawnv", "spawnve", "spawnvp", "spawnvpe"}


def observer_native(e, name):
    """The native executable a native_observer script execs, and its extra argv.

    Read from the PROGRAM, not its text (Codex review 2026-10-13: the two
    recognised lines inside a string literal, and a different launch below
    them, passed): imports limited to json, subprocess and sys; exactly one
    process launch anywhere, a module-level `p=subprocess.Popen(["claude"]+args,
    executable=<literal>, ...)`; and the extra argv a module-level
    `if args!=['--version']:args=<literal list>+args`.
    """
    import ast
    tree = ast.parse(e.text(name))
    for node in ast.walk(tree):
        if isinstance(node, ast.ImportFrom) or (isinstance(node, ast.Import) and
                any(a.name not in ("json", "subprocess", "sys") or a.asname for a in node.names)):
            raise ValueError("observer imports beyond json, subprocess and sys")
    calls = [n for n in ast.walk(tree) if isinstance(n, ast.Call) and
             (getattr(n.func, "attr", None) or getattr(n.func, "id", None)) in LAUNCHERS]
    launch = [n for n in tree.body if isinstance(n, ast.Assign) and isinstance(n.value, ast.Call) and
              ast.unparse(n.value.func) == "subprocess.Popen" and [ast.unparse(t) for t in n.targets] == ["p"]]
    if len(calls) != 1 or len(launch) != 1 or launch[0].value is not calls[0]:
        raise ValueError("observer must launch exactly one native, from one module-level Popen")
    call = launch[0].value
    if [ast.unparse(a) for a in call.args] != ["['claude'] + args"]:
        raise ValueError("observer launches something other than the native with its argv")
    native = [k.value for k in call.keywords if k.arg == "executable"]
    if len(native) != 1 or not isinstance(native[0], ast.Constant) or not isinstance(native[0].value, str):
        raise ValueError("observer's executable is not a literal path")
    extra = [n for n in tree.body if isinstance(n, ast.If) and ast.unparse(n.test) == "args != ['--version']" and
             len(n.body) == 1 and isinstance(n.body[0], ast.Assign) and ast.unparse(n.body[0].targets[0]) == "args" and
             isinstance(n.body[0].value, ast.BinOp) and ast.unparse(n.body[0].value.right) == "args"]
    if len(extra) != 1 or extra[0].orelse:
        raise ValueError("observer's extra argv is not the runner's one literal prefix")
    return native[0].value, ast.literal_eval(extra[0].body[0].value.left)


def same_identity(identity, rows):
    return all(v.get("identity") == identity for v in rows)


def valid_identity(identity, harness):
    return (identity["schema"] == "buddy.lifecycle.v1" and identity["harness"] == harness and
            bool(identity["session_id"]) and bool(identity["incarnation"]))


def proc_record(e, name, identity, receipt, executable, survivors=()):
    """The ownership record (.proc) names this run and closed as expected.

    survivors: the (pid, start) pairs the close must report as escaped and
    still running. Empty means nothing escaped. Never a kill: the narrow
    contract (D-077) records and reports them.
    """
    proc = e.json(name)
    close = proc["close"]
    want = sorted(survivors)
    got = sorted((v["pid"], v["start"]) for v in close["survivors"])
    captured = sorted((v["pid"], v["start"]) for v in close["captured"])
    # identity None: an admission refused before capture writes no identity.
    return (proc["schema"] == "buddy.ownership.v1" and proc.get("identity") == identity and
            proc["receipt"] == str(receipt) and e.inside(proc["receipt"]) and
            proc["executable"]["path"] == str(executable) and
            proc["executable"]["size"] == Path(executable).stat().st_size and
            proc["leader"]["pid"] > 1 and proc["leader"]["pgid"] == proc["leader"]["pid"] and
            close["trigger"] == "close" and close["unattributable"] == "unknown" and close["unknown"] == [] and
            got == want and captured == want and close["ended"] == [] and
            close["group"].startswith("SIGTERM to group %d," % proc["leader"]["pgid"]))


def close_line(e, name, survivors=()):
    """Exactly one close report on the adapter's stderr, consistent with .proc."""
    rows = [v for v in e.text(name).splitlines() if v.startswith(CLOSE_PREFIX)]
    if len(rows) != 1:
        return False
    line = rows[0]
    n = len(survivors)
    if not n:
        return "0 escaped descendant(s) captured, 0 ended, 0 still running, 0 unknown" in line
    pids = ", ".join(str(p) for p in sorted(survivors))
    return ("%d escaped descendant(s) captured, 0 ended, %d still running (pid %s; not signalled" % (n, n, pids)) in line


def evidence_binding(e, r, required):
    """F-37: when the runner hashes its evidence, every file read must be listed.

    Returns "hashed" or "unhashed". A receipt that carries evidence_sha256 with a
    missing or wrong entry raises (and the check fails)."""
    hashes = r.get("evidence_sha256")
    if hashes is None:
        if required:
            raise ValueError("receipt carries no evidence_sha256")
        return "unhashed"
    if not isinstance(hashes, dict):
        raise ValueError("evidence_sha256 is not a map")
    for name in sorted(e.read_paths):
        if name == "result.json":
            continue
        if hashes.get(name) != e.sha(e.root / name):
            raise ValueError("evidence not bound by hash: " + name)
    return "hashed"


def mutate(r, path, value):
    bad = copy.deepcopy(r)
    target = bad
    for key in path[:-1]:
        target = target[key]
    if value is DELETE:
        del target[path[-1]]
    else:
        target[path[-1]] = value
    return bad


DELETE = object()


def write(root, name, value):
    path = Path(root) / name
    path.parent.mkdir(parents=True, exist_ok=True)
    if isinstance(value, bytes):
        path.write_bytes(value)
    elif isinstance(value, str):
        path.write_text(value)
    else:
        path.write_text(json.dumps(value, indent=2) + "\n")
    return path


def jsonl(rows):
    return "".join(json.dumps(v) + "\n" for v in rows)


def sha_file(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def observer_source(native, extra=()):
    """The shape of qualify-claude.native_observer, for synthetic runs."""
    return ("#!/usr/bin/python3\nimport json,subprocess,sys\nargs=sys.argv[1:]\n"
            "if args!=['--version']:args=" + repr(list(extra)) + "+args\n"
            "p=subprocess.Popen([\"claude\"]+args,executable=" + repr(str(native)) +
            ",stdin=sys.stdin,stdout=subprocess.PIPE,stderr=sys.stderr)\nsys.exit(p.wait())\n")


def proc_source(receipt, identity, executable, leader=4100, survivors=()):
    rows = [{"pid": p, "start": s, "ppid": leader + 1, "pgid": survivors[0][0], "first_seen": "t"} for p, s in survivors]
    proc = {"schema": "buddy.ownership.v1", "attempt": "a" * 32, "operation": "start", "phase": "spawned",
            "receipt": str(receipt), "identity": identity, "adapter": {"pid": leader - 2, "start": 1, "pgid": leader - 2},
            "leader": {"pid": leader, "start": 2, "pgid": leader, "sid": leader - 2},
            "executable": {"path": str(executable), "size": Path(executable).stat().st_size},
            "descendants": {"scan_seq": 1, "scan_errors": 0, "ended": 0, "alive": rows or None},
            "close": {"trigger": "close", "group": "SIGTERM to group %d, a 2s grace for the leader, SIGKILL to the group "
                      "and the leader, leader reaped" % leader, "captured": rows, "ended": [], "survivors": rows,
                      "unknown": [], "unattributable": "unknown"}}
    if identity is None:
        del proc["identity"]
    return proc


def close_report(survivors=()):
    if not survivors:
        return (CLOSE_PREFIX + "close): 0 escaped descendant(s) captured, 0 ended, 0 still running, 0 unknown; "
                "descendants never observed: unknown\n")
    pids = ", ".join(str(p) for p, _ in sorted(survivors))
    n = len(survivors)
    return (CLOSE_PREFIX + "close): %d escaped descendant(s) captured, 0 ended, %d still running (pid %s; not "
            "signalled: they left the worker's group), 0 unknown; descendants never observed: unknown\n" % (n, n, pids))


def self_test_suite(name, validate, synthetic, receipt_mutations, file_mutations, splice):
    """The shared hermetic self-test: a synthetic positive control, receipt
    damage, file damage (each must fail its named check while the rest of the
    run stays intact), a splice from a second run, and a hash-bound variant."""
    with tempfile.TemporaryDirectory(prefix="buddy-" + name + "-synthetic-") as tmp:
        base_dir = Path(tmp).resolve()
        a = synthetic(base_dir / "run-a", "a")
        b = synthetic(base_dir / "run-b", "b")
        receipt_a, receipt_b = base_dir / "run-a/result.json", base_dir / "run-b/result.json"
        for receipt, r in ((receipt_a, a), (receipt_b, b)):
            verdict = validate(r, receipt)
            if not verdict["passed"]:
                raise AssertionError("synthetic positive control failed: " + json.dumps(verdict["errors"]))
        rejected = 0
        for path, value, check in receipt_mutations(a):
            verdict = validate(mutate(a, path, value), receipt_a)
            if verdict["passed"] or verdict["checks"].get(check) is not False:
                raise AssertionError("accepted damaged receipt %s (want %s to fail)" % (".".join(map(str, path)), check))
            rejected += 1
        for label, damage, check in file_mutations(base_dir / "run-a", a):
            snapshot = base_dir / "pristine"
            shutil.copytree(base_dir / "run-a", snapshot, symlinks=True)
            try:
                # A damage may return the receipt that goes with it (a file
                # re-hashed by the runner), so a semantic guard is tested apart
                # from the hash that would otherwise catch it first.
                bad = damage()
                verdict = validate(bad if isinstance(bad, dict) else a, receipt_a)
                if verdict["passed"] or verdict["checks"].get(check) is not False:
                    raise AssertionError("accepted damaged evidence %s (want %s to fail): %s" %
                                         (label, check, json.dumps(verdict["checks"])))
                rejected += 1
            finally:
                shutil.rmtree(base_dir / "run-a")
                shutil.move(str(snapshot), str(base_dir / "run-a"))
        # A receipt from another run, found in this run's directory, and one
        # evidence file of that other run spliced into this one.
        if validate(b, receipt_a)["checks"].get("receipt_location") is not False:
            raise AssertionError("accepted a receipt naming another run's root")
        rejected += 1
        for label, check in splice:
            snapshot = base_dir / "pristine"
            shutil.copytree(base_dir / "run-a", snapshot, symlinks=True)
            try:
                shutil.copyfile(base_dir / "run-b" / label, base_dir / "run-a" / label)
                verdict = validate(a, receipt_a)
                if verdict["passed"] or verdict["checks"].get(check) is not False:
                    raise AssertionError("accepted %s spliced from another run (want %s to fail)" % (label, check))
                rejected += 1
            finally:
                shutil.rmtree(base_dir / "run-a")
                shutil.move(str(snapshot), str(base_dir / "run-a"))
        # A link in run B's place to run A's receipt is still run A's receipt.
        linked = base_dir / "linked"
        linked.mkdir()
        (linked / "result.json").symlink_to(receipt_a)
        if validate(a, linked / "result.json")["checks"].get("receipt_location") is not False:
            raise AssertionError("accepted a receipt through a link from another directory")
        (base_dir / "run-b/result.json").unlink()
        (base_dir / "run-b/result.json").symlink_to(receipt_a)
        if validate(a, base_dir / "run-b/result.json")["checks"].get("receipt_location") is not False:
            raise AssertionError("accepted run A's receipt linked into run B")
        rejected += 2
        # Hash-bound evidence (the proposed runner change): correct hashes pass,
        # one wrong hash is refused.
        probe = validate(a, receipt_a)
        hashed = copy.deepcopy(a)
        e = Evidence(a, base_dir / "run-a")
        hashed["evidence_sha256"] = {p: e.sha(e.root / p) for p in probe["read"] if p != "result.json"}
        if not validate(hashed, receipt_a)["passed"] or validate(hashed, receipt_a)["evidence"] != "hashed":
            raise AssertionError("hash-bound synthetic receipt failed")
        first = sorted(hashed["evidence_sha256"])[0]
        hashed["evidence_sha256"][first] = "0" * 64
        if validate(hashed, receipt_a)["checks"].get("evidence_hashes") is not False:
            raise AssertionError("accepted a wrong evidence hash")
        rejected += 1
        if not validate(a, receipt_a)["passed"]:
            raise AssertionError("synthetic control did not restore")
        return {"positive": "passed", "rejected_mutations": rejected,
                "evidence": "synthetic: tests the checker, qualifies no native harness"}


def receipt_self_test(validate, r, receipt, receipt_mutations):
    """Against a REAL receipt: the receipt-level mutations only; files untouched."""
    verdict = validate(r, receipt)
    if not verdict["passed"]:
        raise AssertionError("positive receipt failed: " + json.dumps(verdict["errors"]))
    rejected = 0
    for path, value, check in receipt_mutations(r):
        verdict = validate(mutate(r, path, value), receipt)
        if verdict["passed"] or verdict["checks"].get(check) is not False:
            raise AssertionError("accepted damaged receipt %s (want %s to fail)" % (".".join(map(str, path)), check))
        rejected += 1
    return {"positive": "passed", "rejected_mutations": rejected}


def main_for(doc, validate, synthetic_self_test, receipt_mutations):
    parser = argparse.ArgumentParser(description=doc)
    parser.add_argument("receipt", type=Path, nargs="?")
    parser.add_argument("--self-test", action="store_true",
                        help="with no receipt, run against synthetic evidence built in a temp dir (hermetic)")
    args = parser.parse_args()
    if args.receipt is None:
        if not args.self_test:
            parser.error("a receipt is required unless --self-test")
        print(json.dumps(synthetic_self_test(), indent=2))
        return 0
    r = json.loads(args.receipt.read_text())
    out = receipt_self_test(validate, r, args.receipt, receipt_mutations) if args.self_test else validate(r, args.receipt)
    print(json.dumps(out, indent=2))
    return 0 if args.self_test or out["passed"] else 1


# ---------------------------------------------------------------- the suite

SUITE_CHECKS = {
    "managed_policy_refused", "refused_before_identity_capture", "refused_without_provider_call",
    "managed_native_child_exited", "captured_native_identity", "handshake_without_inference",
    "unsupported_telemetry_remains_unknown", "native_wake_replay_accepted", "allowed.txt_waiting_approval",
    "peer_cannot_approve", "operator_allow_submitted", "operator-denied.txt_waiting_approval",
    "operator_deny_submitted", "native_result_separate_from_completion", "conflicting_write_denied",
    "positive_write_allowed", "operator_denied_write_absent", "denial_names_buddy_claim",
    "operator_denial_reached_native", "native_receiver_endpoint_captured", "assignment_still_queued_after_result",
    "native_idle_translated", "idle_clears_pending_approvals", "result_bound_to_accepted_input",
    "duplicate_wake_not_replayed", "body_delivery_explicit", "second_idle_assignment_accepted",
    "second_result_has_own_input_identity", "fixture_only_expected_requests", "adapter_exited_successfully",
    "adapter_stdout_fully_parsed"}
TRACE_CHECKS = {"observed_native_children_exited"}
MANAGED = "managed-hook-refusal/"


def validate(r, receipt_path):
    e = Evidence(r, r["root"])
    traced = "native_metadata_observer" in r
    version = str(r.get("native_version", "")).removesuffix(" (Claude Code)")
    ev = lambda: e.json("adapter-events.json")
    launch = lambda: e.json("launch.json")
    gate = lambda: parse_gate(launch()["gate_command"])
    def ops(op): return [v for v in ev() if v.get("operation") == op]
    def reply(rid):
        rows = [v for v in ev() if v.get("request_id") == rid]
        if len(rows) != 1:
            raise ValueError("want one reply to " + rid)
        return rows[0]
    def native(kind): return [v for v in ev() if v.get("native_event", {}).get("kind") == kind]
    def outputs():
        rows = {}
        for request in e.json("provider-observations.json"):
            for x in request["tool_outputs"]:
                rows.setdefault(x["tool_use_id"], x)
        return rows
    def identity(): return e.json("worker.json")["identity"]
    def approval_file(event): return Path(event["native_event"]["approval"]["detail"]["input"]["file_path"])
    def executable():
        return (e.root / "bin/claude-observer") if traced else Path(e.json("worker.json.proc")["executable"]["path"])
    def native_path():
        return Path(observer_native(e, "bin/claude-observer")[0]) if traced else executable()

    e.check("receipt_location", lambda: located(e, receipt_path))
    e.check("contract", lambda: r["schema"] == "buddy.claude-qualification.v1" and version in CLAUDE_VERSIONS and
            r["native_version"] == version + " (Claude Code)" and hex64(r["native_sha256"]) and
            "loopback" in r["provider"] and launch()["harness_version"] == version and
            launch()["hooks"]["mods"] == "builtin-only-" + version and
            r["configuration_evidence"]["mods_profile"] == "builtin-only-" + version)
    e.check("runner_verdict", lambda: runner_verdict(r, "passed", SUITE_CHECKS | (TRACE_CHECKS if traced else set())))
    e.check("artifact_hashes", lambda: e.sha(gate()[0]) == r["adapter_sha256"] and e.sha(gate()[2]) == r["buddy_sha256"] and
            e.sha("source-manifest.json") == r["source_manifest_sha256"] and
            e.sha("settings.json") == r["configuration_evidence"]["settings_sha256"] and
            e.sha(native_path()) == r["native_sha256"] and
            (not traced or (e.sha(executable()) == r["native_metadata_observer"]["sha256"] and
                            observer_native(e, "bin/claude-observer")[1] == [])))
    e.check("gate_configuration", lambda: e.json("settings.json") == gate_settings(launch()["gate_command"]) and
            gate()[1] == str(e.root / "worker.json") and launch()["worktree"] == str(e.root / "work") and
            launch()["hook_config"] == str(e.root / "settings.json") and launch()["permission_mode"] == "workspace-write")
    e.check("captured_identity", lambda: valid_identity(identity(), "claude") and len(ops("start")) == 1 and
            ops("start")[0]["identity"] == identity() and not ops("start")[0].get("error") and
            all(e.json("worker.json")["launch"][k] == launch()[k] for k in ("gate_command", "worktree", "hook_config", "harness_version")) and
            e.json("worker.json")["launch"]["hooks"]["mods"] == launch()["hooks"]["mods"] and
            same_identity(identity(), ev()) and same_identity(identity(), e.lines("worker.json.events")) and
            ops("start")[0]["capabilities"]["telemetry"]["support"] == "unknown")
    # The claimed path: the edit did not happen, the gate's denial reached the
    # native, and it never got as far as asking the operator.
    e.check("claim_denial_held", lambda: e.text("work/guarded.txt") == "original\n" and
            outputs()["toolu_buddy_0"].get("is_error") is True and
            'guarded.txt is inside scope "guarded.txt"' in str(outputs()["toolu_buddy_0"]["content"]) and
            not any(approval_file(v).name == "guarded.txt" for v in native("approval")))
    e.check("allowed_control", lambda: e.text("work/allowed.txt") == "allowed-control\n" and
            not outputs()["toolu_buddy_1"].get("is_error") and
            str(e.root / "work/allowed.txt") in str(outputs()["toolu_buddy_1"]["content"]) and approval_file(native("approval")[0]) == e.root / "work/allowed.txt" and
            reply("operator-allow")["approval_delivery"]["state"] == "submitted")
    e.check("operator_denial", lambda: e.absent("work/operator-denied.txt") and
            outputs()["toolu_buddy_2"].get("is_error") is True and
            "Denied by the operator control interface" in str(outputs()["toolu_buddy_2"]["content"]) and
            approval_file(native("approval")[1]) == e.root / "work/operator-denied.txt" and len(native("approval")) == 2 and
            reply("operator-deny")["approval_delivery"]["state"] == "submitted")
    e.check("peer_cannot_approve", lambda: "unsupported control operation" in reply("peer-approval-refused")["error"] and
            "approval_delivery" not in reply("peer-approval-refused") and
            ev().index(reply("peer-approval-refused")) < ev().index(reply("operator-allow")))
    def turn(rid):
        wake = reply(rid)
        tid = wake["wake_delivery"]["turn_id"]
        results = [v for v in native("result") if v["native_event"]["turn_id"] == tid]
        idles = [v for v in native("idle") if v["native_event"]["turn_id"] == tid]
        return (wake["wake_delivery"]["state"] == "delivered" and bool(tid) and len(results) == len(idles) == 1 and
                results[0]["completion"]["state"] == "unknown" and bool(results[0]["native_event"]["result_ref"]) and
                results[0]["native_event"]["result_status"] == "completed" and
                ev().index(wake) < ev().index(results[0]) < ev().index(idles[0]) and
                idles[0]["state"]["detail"] == "idle" and idles[0]["pending_approval"]["state"] == "observed_absent")
    e.check("first_turn", lambda: turn("wake-1") and len(native("approval")) == 2 and
            all(v["native_event"]["approval"]["turn_id"] == reply("wake-1")["wake_delivery"]["turn_id"] for v in native("approval")))
    e.check("second_turn", lambda: turn("wake-2") and
            reply("wake-2")["wake_delivery"]["turn_id"] != reply("wake-1")["wake_delivery"]["turn_id"] and
            len({v["native_event"]["result_ref"] for v in native("result")}) == 2 == len(native("result")))
    # Four requests for the first turn (0..3 tool results), one for the second;
    # a replayed duplicate wake or a managed case that reached the provider adds one.
    e.check("duplicate_not_replayed", lambda: reply("wake-duplicate")["wake_delivery"] == reply("wake-1")["wake_delivery"] and
            [len(v["tool_outputs"]) for v in e.json("provider-observations.json")] == [0, 1, 2, 3, 3])
    e.check("managed_refusal", lambda: "allowManagedHooksOnly" in e.json(MANAGED + "result.json")["refusal"] and
            e.json(MANAGED + "result.json")["adapter_exit"] == 1 and e.absent(MANAGED + "worker.json") and
            [v.get("error") for v in e.json(MANAGED + "adapter-events.json")] == [e.json(MANAGED + "result.json")["refusal"]] and
            e.json(MANAGED + "result.json")["refusal"] in e.text(MANAGED + "adapter.stderr") and
            e.sha(MANAGED + "claude-managed-fixture") == r["managed_hook_refusal"]["observer_sha256"] and
            observer_native(e, MANAGED + "claude-managed-fixture") == (str(native_path()), ["--managed-settings", '{"allowManagedHooksOnly":true}']) and
            e.json(MANAGED + "launch.json")["gate_command"] == parse_gate_case(e, MANAGED) and
            child_exit(e, e.json(MANAGED + "result.json")["cleanup_observation"], MANAGED + "native-observations.jsonl"))
    e.check("endpoint_observed", lambda: e.json("ledger-inspection.json")["identity"] ==
            {k: identity()[k] for k in ("session_id", "incarnation")} and
            e.json("ledger-inspection.json")["endpoint"]["status"] == "observed" and
            e.json("ledger-inspection.json")["endpoint"]["pid"] > 1 and e.json("ledger-inspection.json")["endpoint"]["born"] > 0 and
            e.json("ledger-inspection.json")["worktree"] == str(e.root / "work"))
    e.check("clean_close", lambda: r["adapter_exit"] == 0 and e.json("unparsed-output.json") == [] and
            close_line(e, "adapter.stderr") and
            proc_record(e, "worker.json.proc", identity(), e.root / "worker.json", executable()) and
            (not traced or child_exit(e, r["cleanup_observation"], "native-observations.jsonl")))
    if traced:
        e.check("mods_inventory_at_init", lambda: inits(e, "native-observations.jsonl", identity()["session_id"]) == 2)
    binding = {}
    e.check("evidence_hashes", lambda: binding.setdefault("v", evidence_binding(e, r, False)))
    return e.verdict(evidence=binding.get("v"), read=sorted(e.read_paths), traced=traced)


def parse_gate_case(e, case):
    receipt = e.root / case / "worker.json"
    adapter, _, buddy = parse_gate(e.json("launch.json")["gate_command"])
    return "'%s' hook --receipt '%s' --buddy '%s'" % (adapter, receipt, buddy)


def child_exit(e, observation, trace):
    pids = [v["pid"] for v in e.lines(trace) if v.get("type") == "buddy_fixture_native_process"]
    return (bool(pids) and observation["status"] == "observed_absent" and observation["observed_pids"] == pids and
            observation["remaining_or_unknown_pids"] == [])


def inits(e, trace, session):
    """Count native init frames; each must carry exactly the builtin profile."""
    rows = [v for v in e.lines(trace) if v.get("type") == "system" and v.get("subtype") == "init"]
    if not rows or any(v["session_id"] != session or v["plugins"] != BUILTIN_MODS for v in rows):
        raise ValueError("native init Mod inventory is not the pinned builtin profile")
    return len(rows)


def receipt_mutations(r):
    return [(("passed",), False, "runner_verdict"),
            (("checks", "conflicting_write_denied"), False, "runner_verdict"),
            (("checks", "positive_write_allowed"), DELETE, "runner_verdict"),
            (("failure",), "boom", "runner_verdict"),
            (("native_version",), "2.1.292 (Claude Code)", "contract"),
            (("schema",), "buddy.claude-qualification.v0", "contract"),
            (("native_sha256",), "0" * 64, "artifact_hashes"),
            (("adapter_sha256",), "0" * 64, "artifact_hashes"),
            (("buddy_sha256",), "0" * 64, "artifact_hashes"),
            (("source_manifest_sha256",), "0" * 64, "artifact_hashes"),
            (("configuration_evidence", "settings_sha256"), "0" * 64, "artifact_hashes"),
            (("managed_hook_refusal", "observer_sha256"), "0" * 64, "managed_refusal"),
            (("adapter_exit",), 1, "clean_close"),
            (("root",), str(Path(r["root"]) / "work"), "receipt_location")]


# ------------------------------------------------------------ synthetic run

def synthetic(root, tag):
    """A coherent SYNTHETIC qualify-claude.py --trace-native run under root.

    Review F-33: a self-test that needs a native receipt in /private/tmp stops
    running after a reboot. These are real files read the same way the native
    run's are. They test the CHECKER and qualify no native harness.
    """
    root = Path(root)
    root.mkdir(parents=True)
    version = CLAUDE_VERSIONS[-1]
    sid, inc = "%s1111111-2222-4333-8444-555555555555" % tag, "inc-" + tag
    identity = {"schema": "buddy.lifecycle.v1", "harness": "claude", "session_id": sid, "incarnation": inc}
    for name, body in (("bin/buddy", "buddy " + tag), ("bin/buddy-adapter", "adapter " + tag), ("bin/claude", "claude native")):
        write(root, name, body)
    receipt = root / "worker.json"
    gate = "'%s' hook --receipt '%s' --buddy '%s'" % (root / "bin/buddy-adapter", receipt, root / "bin/buddy")
    write(root, "settings.json", gate_settings(gate))
    launch = {"worktree": str(root / "work"), "permission_mode": "workspace-write", "hook_config": str(root / "settings.json"),
              "harness_version": version, "gate_command": gate,
              "hooks": {"configured": "observed", "trusted": "unknown", "observed": "unknown", "mods": "builtin-only-" + version}}
    write(root, "launch.json", launch)
    write(root, "source-manifest.json", {"go.mod": "0" * 64})
    write(root, "bin/claude-observer", observer_source(root / "bin/claude"))
    write(root, "worker.json", {"schema": "buddy.control.v1", "identity": identity, "launch": launch})
    write(root, "work/guarded.txt", "original\n")
    write(root, "work/allowed.txt", "allowed-control\n")
    t1, t2 = "turn-1-" + tag, "turn-2-" + tag
    def event(op, **fields):
        return dict({"schema": "buddy.control.v1", "operation": op, "identity": identity, "state": {"state": "unknown"},
                     "completion": {"state": "unknown"}, "pending_approval": {"state": "unknown"},
                     "capabilities": {"telemetry": {"support": "unknown"}}}, **fields)
    def nat(kind, turn, **fields):
        return event("event", native_event=dict({"kind": kind, "turn_id": turn, "native_session_id": sid}, **fields),
                     **({"state": {"state": "observed", "detail": "idle"}, "pending_approval": {"state": "observed_absent"}}
                        if kind == "idle" else {}))
    def approval(name, n):
        return nat("approval", t1, approval={"id": "ap-%d" % n, "native_session_id": sid, "turn_id": t1,
                   "detail": {"input": {"file_path": str(root / "work" / name)}}})
    events = [event("start"), dict(event("send"), request_id="wake-1", wake_delivery={"state": "delivered", "turn_id": t1}),
              nat("busy", t1), approval("allowed.txt", 1),
              dict(event("approve"), request_id="peer-approval-refused", error="unsupported control operation \"approve\""),
              dict(event("approve"), request_id="operator-allow", approval_delivery={"state": "submitted"}),
              approval("operator-denied.txt", 2),
              dict(event("approve"), request_id="operator-deny", approval_delivery={"state": "submitted"}),
              nat("result", t1, result_ref="res-1", result_status="completed"), nat("idle", t1),
              dict(event("send"), request_id="wake-duplicate", wake_delivery={"state": "delivered", "turn_id": t1}),
              dict(event("send"), request_id="wake-2", wake_delivery={"state": "delivered", "turn_id": t2}),
              nat("result", t2, result_ref="res-2", result_status="completed"), nat("idle", t2)]
    write(root, "adapter-events.json", events)
    write(root, "worker.json.events", jsonl(events))
    out = [{"type": "tool_result", "tool_use_id": "toolu_buddy_0", "is_error": True,
            "content": 'PreToolUse:Write hook error: guarded.txt is inside scope "guarded.txt" claimed by session work/s-fixtureh'},
           {"type": "tool_result", "tool_use_id": "toolu_buddy_1", "content": "File created successfully at: %s" % (root / "work/allowed.txt")},
           {"type": "tool_result", "tool_use_id": "toolu_buddy_2", "is_error": True, "content": "Denied by the operator control interface"}]
    write(root, "provider-observations.json", [{"path": "/v1/messages", "tool_outputs": out[:n]} for n in (0, 1, 2, 3, 3)])
    write(root, "ledger-inspection.json", {"identity": {"session_id": sid, "incarnation": inc}, "worktree": str(root / "work"),
                                           "endpoint": {"status": "observed", "pid": 4242, "born": 1}})
    write(root, "unparsed-output.json", [])
    write(root, "adapter.stderr", close_report())
    write(root, "worker.json.proc", proc_source(receipt, identity, root / "bin/claude-observer"))
    init = {"type": "system", "subtype": "init", "session_id": sid, "plugins": BUILTIN_MODS}
    write(root, "native-observations.jsonl", jsonl([{"type": "buddy_fixture_native_process", "pid": 501},
                                                    {"type": "buddy_fixture_native_process", "pid": 503}, init, init]))
    refusal = "initialize Claude streaming transport: Claude hook compatibility refuses allowManagedHooksOnly"
    write(root, MANAGED + "claude-managed-fixture",
          observer_source(root / "bin/claude", ["--managed-settings", '{"allowManagedHooksOnly":true}']))
    write(root, MANAGED + "launch.json", dict(launch, gate_command="'%s' hook --receipt '%s' --buddy '%s'" %
                                               (root / "bin/buddy-adapter", root / MANAGED / "worker.json", root / "bin/buddy")))
    write(root, MANAGED + "adapter-events.json", [dict(event("start"), error=refusal)])
    write(root, MANAGED + "adapter.stderr", "buddy-adapter: " + refusal + "\n")
    write(root, MANAGED + "native-observations.jsonl", jsonl([{"type": "buddy_fixture_native_process", "pid": 401}]))
    managed = {"refusal": refusal, "adapter_exit": 1, "observer_sha256": sha_file(root / MANAGED / "claude-managed-fixture"),
               "cleanup_observation": {"status": "observed_absent", "observed_pids": [401], "remaining_or_unknown_pids": []}}
    write(root, MANAGED + "result.json", managed)
    r = {"schema": "buddy.claude-qualification.v1", "root": str(root), "passed": True,
         "checks": {k: True for k in SUITE_CHECKS | TRACE_CHECKS}, "provider": "deterministic loopback Messages fixture",
         "native_version": version + " (Claude Code)", "native_sha256": sha_file(root / "bin/claude"),
         "source_manifest_sha256": sha_file(root / "source-manifest.json"),
         "buddy_sha256": sha_file(root / "bin/buddy"), "adapter_sha256": sha_file(root / "bin/buddy-adapter"),
         "configuration_evidence": {"settings_sha256": sha_file(root / "settings.json"), "mods_profile": "builtin-only-" + version},
         "managed_hook_refusal": managed, "native_metadata_observer": {"sha256": sha_file(root / "bin/claude-observer")},
         "cleanup_observation": {"status": "observed_absent", "observed_pids": [501, 503], "remaining_or_unknown_pids": []},
         "adapter_exit": 0}
    write(root, "result.json", r)
    return r


def file_mutations(root, r):
    root = Path(root)
    def edit_json(name, fn):
        def run():
            value = json.loads((root / name).read_text())
            fn(value)
            write(root, name, value)
        return run
    def edit_lines(name, fn):
        def run():
            rows = [json.loads(v) for v in (root / name).read_text().splitlines()]
            fn(rows)
            write(root, name, jsonl(rows))
        return run
    def swap_outputs(rows):
        rows[-1]["tool_outputs"][0]["is_error"] = False
    return [
        ("denied edit happened", lambda: write(root, "work/guarded.txt", "conflicting-write\n"), "claim_denial_held"),
        ("denial never reached native", edit_json("provider-observations.json",
            lambda rows: [x.update(is_error=False) for v in rows for x in v["tool_outputs"] if x["tool_use_id"] == "toolu_buddy_0"]),
            "claim_denial_held"),
        ("missing allowed control", lambda: (root / "work/allowed.txt").unlink(), "allowed_control"),
        ("operator-denied write happened", lambda: write(root, "work/operator-denied.txt", "must-not-exist\n"), "operator_denial"),
        ("duplicate wake replayed", edit_json("provider-observations.json", lambda rows: rows.append(rows[-1])), "duplicate_not_replayed"),
        ("second turn reuses the first result", edit_json("adapter-events.json",
            lambda rows: rows[-2]["native_event"].update(result_ref="res-1")), "second_turn"),
        ("peer approval accepted", edit_json("adapter-events.json",
            lambda rows: [v.pop("error") for v in rows if v.get("request_id") == "peer-approval-refused"]), "peer_cannot_approve"),
        ("managed refusal captured identity", lambda: write(root, MANAGED + "worker.json", "{}"), "managed_refusal"),
        ("journal names another identity", edit_lines("worker.json.events",
            lambda rows: rows[1]["identity"].update(incarnation="other")), "captured_identity"),
        ("escaped descendant left running", edit_json("worker.json.proc",
            lambda v: v["close"]["survivors"].append({"pid": 9, "start": 9})), "clean_close"),
        ("native child still alive", edit_lines("native-observations.jsonl",
            lambda rows: rows.insert(0, {"type": "buddy_fixture_native_process", "pid": 777})), "clean_close"),
        ("altered Mod inventory at init", edit_lines("native-observations.jsonl",
            lambda rows: rows[2]["plugins"].append({"name": "x", "path": "/tmp/x", "source": "x@inline"})), "mods_inventory_at_init"),
        ("adapter binary replaced", lambda: write(root, "bin/buddy-adapter", "other adapter"), "artifact_hashes"),
        ("settings widened", edit_json("settings.json", lambda v: v["hooks"]["PreToolUse"][0].update(matcher="Write")), "artifact_hashes"),
        ("observer adds argv", lambda: write(root, "bin/claude-observer", observer_source(root / "bin/claude", ["--x"])), "artifact_hashes"),
        # Each of these was added because a mutation run found the guard it
        # targets unexercised (2026-10-13): the guard could be removed and the
        # self-test still passed.
        ("settings widened and re-hashed", lambda: rehash_settings(root, r), "gate_configuration"),
        ("endpoint never observed", edit_json("ledger-inspection.json",
            lambda v: v["endpoint"].update(status="unknown")), "endpoint_observed"),
        ("approval from another turn", edit_json("adapter-events.json",
            lambda rows: [v["native_event"]["approval"].update(turn_id="other") for v in rows
                          if v.get("native_event", {}).get("kind") == "approval"][:1]), "first_turn"),
        ("no close report", lambda: write(root, "adapter.stderr", ""), "clean_close"),
        ("two close reports", lambda: write(root, "adapter.stderr", close_report() + close_report()), "clean_close"),
        ("worktree outside the run", lambda: relink_outside(root, "work"), "claim_denial_held"),
        ("observer's recognised lines are a decoy string", lambda: decoy_observer(root, r), "artifact_hashes"),
        ("observer launches a second process", lambda: decoy_observer(root, r, second=True), "artifact_hashes"),
    ]


def rehash_settings(root, r):
    value = json.loads((Path(root) / "settings.json").read_text())
    value["hooks"]["PreToolUse"][0]["matcher"] = "Write"
    write(root, "settings.json", value)
    return mutate(r, ("configuration_evidence", "settings_sha256"), sha_file(Path(root) / "settings.json"))


def decoy_observer(root, r, second=False):
    """The observer's two recognised lines inside a string and a different
    native actually launched, or (second) the real observer plus one more
    launch first; its hash and .proc size made to agree."""
    root = Path(root)
    path = root / "bin/claude-observer"
    if second:
        path.write_text(path.read_text().replace("args=sys.argv[1:]\n", "args=sys.argv[1:]\nsubprocess.call(['/bin/sh'])\n"))
    else:
        path.write_text('DECOY = """\n' + path.read_text().replace("#!/usr/bin/python3\n", "") + '"""\n'
                        'import subprocess,sys\nsys.exit(subprocess.call(["/bin/sh"]+sys.argv[1:]))\n')
    proc = json.loads((root / "worker.json.proc").read_text())
    proc["executable"]["size"] = path.stat().st_size
    write(root, "worker.json.proc", proc)
    return mutate(r, ("native_metadata_observer", "sha256"), sha_file(path))


def relink_outside(root, name):
    """Move a directory out of the run and leave a link in its place: every
    path under it still spells the run's root, and must not be read as one."""
    root = Path(root)
    outside = root.parent / ("outside-" + root.name + "-" + name)
    if outside.exists():
        shutil.rmtree(outside)
    shutil.move(str(root / name), str(outside))
    (root / name).symlink_to(outside)


SPLICE = [("worker.json.proc", "clean_close"), ("adapter-events.json", "captured_identity"),
          ("provider-observations.json", "allowed_control")]


def synthetic_self_test():
    return self_test_suite("claude-gate", validate, synthetic, receipt_mutations, file_mutations, SPLICE)


if __name__ == "__main__":
    sys.exit(main_for(__doc__, validate, synthetic_self_test, receipt_mutations))
