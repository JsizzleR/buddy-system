#!/usr/bin/env python3
"""Check mixed-fleet evidence independently of the exercise's success flags.

The runner supplies raw receipts and artifact paths, not a list of assertions.
This checker reads immutable SQLite snapshots and Git objects independently:
native turn completion, explicit body delivery, READY and validated landing are
different facts. A passing deterministic fixture is not real-provider proof.
Run --self-test for hermetic positive and deliberately broken receipt cases.
"""

import argparse
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import sqlite3
import subprocess
import sys
import tempfile


ROLES = {"coordinator": "codex", "claude_worker": "claude", "codex_worker": "codex"}
WORKERS = ("claude_worker", "codex_worker")
SHA = re.compile(r"[0-9a-f]{40}(?:[0-9a-f]{24})?\Z")
VERIFY_SOURCE = (
    "import pathlib\nfor name in ('claude.txt','codex.txt'):\n"
    "    assert pathlib.Path(name).read_text() == 'fixed\\n', name + ' is not fixed'\n"
    "print('both worker changes validated')\n"
)


def identity(value):
    return value["session_id"], value["incarnation"]


def git(worktree, *args, strip=True):
    # The observer must not inherit a caller's index, alternate object store or
    # executable fsmonitor. These reads never run repository hooks.
    env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
    env.update(GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull,
               GIT_OPTIONAL_LOCKS="0")
    result = subprocess.run(["git", "-c", "core.fsmonitor=false", "-c",
                             "core.hooksPath=/dev/null", "-C", str(worktree), *args],
                            env=env, text=True, capture_output=True, timeout=20)
    if result.returncode:
        raise ValueError("git read failed: " + result.stderr.strip())
    return result.stdout.strip() if strip else result.stdout


# SUITE BINDING (review F-13). A suite receipt is N per-case receipts, and each
# suite checker validated every case against its OWN root and nothing else. So
# a valid case lifted from another run passed as part of this one: measured,
# the 2.1.291 interrupted-compaction suite with its `completed` case swapped for
# the 2.1.290 run's PASSED, and so did a 2.1.289 case in the 2.1.291
# interrupted suite. check-claude-history.py had a cross-case contract guard
# written for exactly this; it was never ported. Two facts, kept apart because
# neither implies the other:
#   same_contract  every case names the same native bytes, versions, source
#                  manifest, built binaries and fixture sources. Equal
#                  contracts do NOT prove one execution: two runs of one build
#                  have equal contracts.
#   same_run       every case's root is exactly <suite root>/<mode>. The suite
#                  root is the run's own mkdtemp directory, so this is the only
#                  run identity the existing receipts carry. It binds where the
#                  evidence LIVES, not what wrote it: a whole case directory
#                  copied in from another run of the same build still passes.
#                  An explicit run id written into hashed evidence by the
#                  runner is what would close that; the receipts have none.
_ABSENT = ("absent",)
SUITE_REQUIRED = ("source_manifest_sha256", "buddy_sha256", "adapter_sha256", "fixture_sources")
# Absent from older receipts (2.1.289 wrote no claude_version), so these must be
# absent from EVERY case or equal in every case; one case cannot differ.
SUITE_UNIFORM = ("claude_version", "codex_version", "native_versions", "source_head", "sdk")


def suite_binding(suite, modes, required=SUITE_REQUIRED, uniform=SUITE_UNIFORM):
    """Return {"same_run": bool, "same_contract": bool}; malformed input is False."""
    verdict = {"same_run": False, "same_contract": False}
    try:
        cases = [suite["cases"][mode] for mode in modes]
        root = Path(suite["root"])
        if root.is_absolute():
            top = root.resolve(strict=True)
            verdict["same_run"] = all(
                case["mode"] == mode and Path(case["root"]).is_absolute() and
                Path(case["root"]).resolve(strict=True) == top / mode
                for mode, case in zip(modes, cases))
        def contract(case):
            if not all(case[key] for key in required):
                raise ValueError("empty suite contract field")
            return ([case[key] for key in required], [case.get(key, _ABSENT) for key in uniform],
                    {role: (s["native_version"], s["native_sha256"]) for role, s in case["sessions"].items()})
        first = contract(cases[0])
        verdict["same_contract"] = bool(first[2]) and all(contract(case) == first for case in cases)
    except (KeyError, IndexError, TypeError, ValueError, OSError):
        pass
    return verdict


def _special(directory, names):
    # copytree would block forever opening a FIFO a fixture left behind.
    return [n for n in names if not (os.path.islink(os.path.join(directory, n)) or
            os.path.isfile(os.path.join(directory, n)) or os.path.isdir(os.path.join(directory, n)))]


def relocated_case(case, destination):
    """Copy a case's evidence root to destination; return the case pointing there.

    For self-tests only. The copy is real (never hard links), so a corruption
    written into it can never reach the native capture it came from. Linked
    worktrees keep their absolute gitdir, so Git reads still resolve against
    the original repository, read-only.
    """
    old, new = str(Path(case["root"])), str(Path(destination))
    if Path(new).exists():
        raise ValueError("relocation target exists: " + new)
    shutil.copytree(old, new, symlinks=True, ignore=_special)
    # Rewrite only the fields a checker resolves as evidence LOCATIONS: the
    # case's top-level paths (root, ledger snapshots) and each session's
    # worktree. A path nested anywhere else is captured CONTENT that a checker
    # compares with a file on disk (measured: rewriting every path broke
    # separate_compaction_history, whose receipt copy holds the thread's cwd).
    def swap(value):
        if isinstance(value, str) and (value == old or value.startswith(old + "/")):
            return new + value[len(old):]
        return value
    # Iterate the deep COPY: reading `case` here once handed the copy the
    # original's nested sessions dict, and the worktree rewrite below then
    # moved the caller's receipt too (measured: a later self-test read a
    # deleted scratch worktree).
    moved = copy.deepcopy(case)
    for key, value in list(moved.items()):
        moved[key] = swap(value)
    for session in moved.get("sessions", {}).values():
        session["worktree"] = swap(session["worktree"])
    return moved


def suite_splice_self_test(suite, modes, validate, validate_case, scratch, pinned_by=None):
    """Splice otherwise-valid cases into a suite; both suite guards must refuse.

    Each splice carries a positive control: the altered case still passes
    validate_case on its own, so the refusal can only come from the cross-case
    guard. Returns the number of splices refused.

    pinned_by names the per-case check that already refuses a case MOVED to
    another root, for a checker whose hashed evidence records the absolute
    worktree (check-claude-history: launch.json). There the moved case is
    asserted to fail that check on its own, which is the stronger binding.
    """
    count = 0
    last = modes[-1]
    def refuse(bad, guard, why):
        nonlocal count
        verdict = validate(bad)
        if verdict.get("passed") or verdict.get(guard) is not False:
            raise AssertionError("accepted spliced suite (" + why + "): " + guard + " did not refuse")
        other = "same_run" if guard == "same_contract" else "same_contract"
        if verdict.get(other) is not True:
            raise AssertionError("splice control broke " + other + " as well (" + why + ")")
        count += 1
    for key, value in (("fixture_sources", None), ("source_head", "0" * 40)):
        bad = copy.deepcopy(suite)
        case = bad["cases"][last]
        if key == "fixture_sources":
            name = sorted(case[key])[0]
            case[key][name] = "0" * 64
        else:
            case[key] = value
        if not validate_case(case)["passed"]:
            raise AssertionError("splice control: case with another " + key + " failed on its own")
        refuse(bad, "same_contract", "a case from a different " + key)
    # The suite names a root that is not where its cases live. Every case is
    # untouched, so each still passes alone.
    bad = copy.deepcopy(suite)
    bad["root"] = str(Path(scratch).resolve())
    refuse(bad, "same_run", "suite root names another run")
    bad = copy.deepcopy(suite)
    bad["cases"][last] = relocated_case(suite["cases"][last], Path(scratch) / "other-run" / last)
    alone = validate_case(bad["cases"][last])
    if pinned_by is None:
        if not alone["passed"]:
            raise AssertionError("splice control: the copied case failed on its own: " + str(alone.get("errors")))
        refuse(bad, "same_run", "a valid case copied from another run's root")
    else:
        if alone["passed"] or alone["checks"].get(pinned_by) is not False:
            raise AssertionError("a case moved to another root did not fail " + pinned_by + " on its own")
        if validate(bad).get("passed"):
            raise AssertionError("accepted a suite holding a case moved from another root")
        count += 1
    return count


# SYNTHETIC EVIDENCE (review F-33). Most checkers' self-tests ran only against a
# native receipt retained in /private/tmp, so a fresh clone could not run them,
# check.sh could not gate them, and once a receipt was gone nothing in the tree
# exercised the guards they hold (audit A-06: the suite binding above was
# killed only by a self-test against an ephemeral 93 MB receipt). These write
# REAL files a checker reads the same way it reads a capture: JSON-lines wire
# logs and journals, SQLite ledger snapshots, a git worktree, a provider log,
# each hashed the way a runner hashes it. They test a CHECKER. They qualify no
# native harness, and a pass against them says nothing about Claude or Codex.
SYNTHETIC_LEDGER = """
    CREATE TABLE sessions(session_id, incarnation, ended);
    CREATE TABLE claims(claim_id, slug, session_id, incarnation, state, outcome);
    CREATE TABLE session_waits(session_id, incarnation, decl, ready_sha, cleared);
    CREATE TABLE session_wait_targets(decl, session_id, incarnation, ord, claim_id, slug);
    CREATE TABLE inbox_delivery(msg_id, session_id, delivered);
    CREATE TABLE inbox(msg_id, target, sender_session, body);
    CREATE TABLE adapter_messages(msg_id, sender_incarnation, recipient_incarnation);
"""
# One build's artifacts. Every case of a synthetic suite writes these same
# bytes, so their hashes agree the way one run's do (suite_binding).
SYNTHETIC_BUILT = {"source-manifest.json": "{}\n", "bin/buddy": "synthetic buddy\n",
                   "bin/buddy-adapter": "synthetic adapter\n"}
SYNTHETIC_BUILT_KEYS = {"source-manifest.json": "source_manifest_sha256", "bin/buddy": "buddy_sha256",
                        "bin/buddy-adapter": "adapter_sha256"}


# NATIVE BINDING (review F-37). Only this checker and check-claude-history
# bound the executables a run used; check-interrupted, check-recovery and
# check-uncertain took a session's native_version from the receipt alone, so a
# receipt naming one build could carry a run of another.
def _admission_lib():
    """scripts/admission_lib.py, loaded once per process under one name, so
    every checker module a run loads reads ONE --candidate expectation."""
    name = "buddy_admission_lib"
    if name not in sys.modules:
        import importlib.util
        spec = importlib.util.spec_from_file_location(name, Path(__file__).resolve().with_name("admission_lib.py"))
        module = importlib.util.module_from_spec(spec)
        sys.modules[name] = module
        spec.loader.exec_module(module)
    return sys.modules[name]


admission = _admission_lib()
# The recorded --version lines (D-079), for synthetic runs. A real run is judged
# by admission.admits on its bound candidate or a record's exact bytes.
NATIVE_CONTRACTS = {"codex": tuple("codex-cli " + v for v in admission.recorded_versions("codex")),
                    "claude": tuple(v + " (Claude Code)" for v in admission.recorded_versions("claude"))}


def admitted_native(harness, line, root, sha):
    """The --version line parses for harness and its run is admissible (D-079/D-080)."""
    return admission.admits(harness, admission.parse_version(harness, line), root, sha)


def native_binding(evidence, roles):
    """Each session ran the executable the HASHED native-versions.json names."""
    capture = evidence.read("native-versions.json")
    if capture != evidence.receipt["native_versions"]:
        return False
    for role in roles:
        session = evidence.session(role)
        native = capture[session["harness"]]
        if (not admitted_native(session["harness"], native["version"], evidence.root, native["executable_sha256"]) or
                session["native_version"] != native["version"] or
                session["native_sha256"] != native["executable_sha256"] or
                not re.fullmatch(r"[0-9a-f]{64}", native["executable_sha256"])):
            return False
    return True


def built_artifacts(evidence):
    """The source manifest and both built binaries are the bytes the receipt names."""
    return all(hashlib.sha256(evidence.path(name).read_bytes()).hexdigest() == evidence.receipt[key]
               for name, key in SYNTHETIC_BUILT_KEYS.items())


def synthetic_natives(synthetic, receipt, harnesses):
    """native-versions.json for the harnesses a synthetic run used; sessions name it."""
    versions = {h: {"version": NATIVE_CONTRACTS[h][-1], "executable_sha256": ("a" if h == "claude" else "b") * 64}
                for h in harnesses}
    # A synthetic run's bytes are its own candidate (D-080), never a record's.
    admission.expect([h + ":" + admission.parse_version(h, v["version"]) + ":" + v["executable_sha256"] for h, v in versions.items()])
    synthetic.write("native-versions.json", versions)
    receipt["native_versions"] = versions
    for session in receipt["sessions"].values():
        native = versions[session["harness"]]
        session.update(native_version=native["version"], native_sha256=native["executable_sha256"])
    return receipt


def native_self_test(receipt, validate, paths):
    """F-37 against a synthetic receipt (edited in place, always restored).

    Two forgeries of native-versions.json with its hash RE-RECORDED, so the
    hash guard passes and native_executables must refuse: a capture that
    disagrees with the receipt, and a consistent capture of an unqualified
    build. Then one changed byte in every bound file must fail the hashes.
    """
    target = Path(receipt["root"]) / "native-versions.json"
    original = target.read_bytes()
    count = 0
    def forged(edit_file, edit_receipt):
        nonlocal count
        versions = json.loads(original)
        edit_file(versions)
        target.write_text(json.dumps(versions))
        bad = copy.deepcopy(receipt)
        bad["evidence_sha256"]["native-versions.json"] = hashlib.sha256(target.read_bytes()).hexdigest()
        edit_receipt(bad, versions)
        try:
            checks = validate(bad)["checks"]
            if checks.get("original_evidence_hashes") is not True or checks.get("native_executables") is not False:
                raise AssertionError("forged native-versions.json: hashes=%r native=%r" % (
                    checks.get("original_evidence_hashes"), checks.get("native_executables")))
        finally:
            target.write_bytes(original)
        count += 1
    harness = sorted(json.loads(original))[0]
    def other_build(v):
        v[harness]["executable_sha256"] = "c" * 64
    forged(other_build, lambda bad, v: None)
    def unqualified(v):
        # A version no record can name. Replacing the literal 0.160.0 forged
        # nothing once the synthetic run used the newest record (0.162.1, the
        # first qualify-release.py record, 2026-10-14).
        import re
        v[harness]["version"] = re.sub(r"\d+\.\d+\.\d+", "0.0.1", v[harness]["version"], count=1)
    def follow(bad, v):
        bad["native_versions"] = v
        for session in bad["sessions"].values():
            session["native_version"] = v[session["harness"]]["version"]
    forged(unqualified, follow)
    with tempfile.TemporaryDirectory(prefix="buddy-hash-binding-") as scratch:
        count += hash_binding_self_test(receipt, validate, paths, scratch, relocate=False)
    return count


def control(operation, ident, **fields):
    """A buddy.control.v1 receipt that claims nothing: every effect unknown."""
    return dict({"schema": "buddy.control.v1", "operation": operation, "identity": ident,
                 "state": {"state": "unknown"}, "completion": {"state": "unknown"},
                 "acknowledgment": {"state": "unknown"}, "body_delivery": {"state": "unknown"},
                 "capabilities": {"telemetry": {"support": "unknown"},
                                  "reconcile": {"support": "unsupported"}}}, **fields)


class Synthetic:
    """One synthetic case's evidence root. For self-tests only."""

    def __init__(self, root):
        self.root = Path(root)
        self.root.mkdir(parents=True, exist_ok=True)
        self.root = self.root.resolve(strict=True)

    def write(self, name, value):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        if isinstance(value, bytes):
            path.write_bytes(value)
        elif isinstance(value, str):
            path.write_text(value)
        else:
            path.write_text(json.dumps(value))
        return name

    def jsonl(self, name, rows):
        return self.write(name, "".join(json.dumps(v) + "\n" for v in rows))

    def ledger(self, name, sessions=(), claims=(), waits=(), targets=(), delivered=(), inbox=(), adapter=()):
        """A SQLite snapshot (no WAL) with the tables the checkers read."""
        path = self.root / (name + ".db")
        db = sqlite3.connect(path)
        try:
            db.executescript(SYNTHETIC_LEDGER)
            for table, rows in (("sessions", sessions), ("claims", claims), ("session_waits", waits),
                                ("session_wait_targets", targets), ("inbox_delivery", delivered),
                                ("inbox", inbox), ("adapter_messages", adapter)):
                for row in rows:
                    db.execute("INSERT INTO %s VALUES (%s)" % (table, ",".join("?" * len(row))), row)
            db.commit()
        finally:
            db.close()
        return str(path)

    def repo(self, name):
        """A real git repository at root/name with one commit; returns its HEAD."""
        work = self.root / name
        work.mkdir(parents=True, exist_ok=True)
        git(work, "init", "-q", "-b", "main")
        git(work, "config", "user.name", "Fixture")
        git(work, "config", "user.email", "fixture@example.invalid")
        (work / "README").write_text("synthetic fixture worktree\n")
        git(work, "add", "README")
        git(work, "-c", "commit.gpgsign=false", "commit", "-qm", "fixture")
        return git(work, "rev-parse", "HEAD")

    def built(self, receipt):
        """Write the build's artifacts and record their hashes in receipt."""
        for name, text in SYNTHETIC_BUILT.items():
            self.write(name, text)
            receipt[SYNTHETIC_BUILT_KEYS[name]] = self.digest(name)
        return receipt

    def digest(self, name):
        path = Path(name) if Path(name).is_absolute() else self.root / name
        return hashlib.sha256(path.read_bytes()).hexdigest()

    def seal(self, receipt, paths):
        """Record evidence_sha256 for paths, keyed relative to the root.

        Returns the receipt as a runner's JSON file would load it. A builder
        shares one dict between fields (a wake's delivery, its duplicate and
        its reconcile), deepcopy keeps that sharing, and a self-test mutation
        then damages both sides of the comparison it exists to fail: measured,
        `duplicate.wake_delivery.turn_id` was accepted. A loaded receipt
        shares nothing.
        """
        sealed = receipt.setdefault("evidence_sha256", {})
        for name in paths:
            path = Path(name) if Path(name).is_absolute() else self.root / name
            sealed[str(path.resolve(strict=True).relative_to(self.root))] = self.digest(path)
        return json.loads(json.dumps(receipt))


def synthetic_contract(receipt, sessions):
    """Fill the suite contract fields one run of one build would share."""
    receipt.update(fixture_sources={"fixture.py": "f" * 64}, source_head="e" * 40)
    for session in sessions:
        session.setdefault("native_sha256", "a" * 64)
        # Synthetic bytes are their own candidate (D-080), never a record's.
        line = session.get("native_version", "")
        harness = "claude" if line.endswith(" (Claude Code)") else "codex"
        admission.EXPECTED[harness] = (admission.parse_version(harness, line), session["native_sha256"])
    return receipt


def hash_binding_self_test(case, validate_case, paths, scratch, check="original_evidence_hashes", relocate=True):
    """Every hashed evidence file is bound: one changed byte fails `check`.

    Runs on a private relocated copy, or on case itself when relocate is False
    (a case already built privately, for a checker whose hashed evidence pins
    its absolute root, so no copy can pass). The control (the case passes
    before and after) proves a refusal is the binding, not a broken copy.
    """
    moved = relocated_case(case, Path(scratch) / "hash-binding") if relocate else case
    root = Path(moved["root"]).resolve(strict=True)
    def verdict():
        try:
            return validate_case(moved)
        except ValueError as error:
            # Evidence.read verifies a file's hash before decoding it, and a
            # checker that reads one outside a check (history's report line)
            # lets that refusal escape; validate() turns it into a fail.
            if not str(error).startswith("changed evidence: "):
                raise
            return {"passed": False, "checks": {check: False}}
    if not verdict()["passed"]:
        raise AssertionError("hash-binding control: the copied case failed: " + str(verdict().get("errors")))
    count = 0
    for name in paths(moved):
        target = (Path(name) if Path(name).is_absolute() else root / name).resolve(strict=True)
        if root not in target.parents:
            raise AssertionError("hash-binding target outside the private copy: " + str(target))
        original = target.read_bytes()
        target.write_bytes(original + b"\n")
        try:
            out = verdict()
            if out["passed"] or out["checks"].get(check) is not False:
                raise AssertionError("an edited evidence file was not refused by " + check + ": " + str(name))
        finally:
            target.write_bytes(original)
        count += 1
    if not verdict()["passed"]:
        raise AssertionError("hash-binding control did not restore")
    return count


def forged_jsonl_self_test(case, validate_case, plans, scratch, relocate=True):
    """Forge JSON-lines evidence and RE-RECORD its hash; the named check must fail.

    plans: (name relative to the case root, edit(rows) in place, check). The
    hash guard must PASS on each forgery (its positive control), so the
    refusal can only come from the semantic check named, never from a hash
    standing in for it (the F-14 lesson). Runs on a private relocated copy,
    or on case itself when relocate is False (see hash_binding_self_test).
    """
    moved = relocated_case(case, Path(scratch) / "forged") if relocate else case
    root = Path(moved["root"]).resolve(strict=True)
    if not validate_case(moved)["passed"]:
        raise AssertionError("forgery control: the case failed before any forgery: " + str(validate_case(moved).get("errors")))
    count = 0
    for name, edit, check in plans:
        target = root / name
        original = target.read_bytes()
        rows = [json.loads(v) for v in original.decode().splitlines()]
        edit(rows)
        changed = "".join(json.dumps(v) + "\n" for v in rows).encode()
        if changed == original:
            raise AssertionError("forgery changed nothing: " + name + " for " + check)
        target.write_bytes(changed)
        try:
            bad = copy.deepcopy(moved)
            bad["evidence_sha256"][name] = hashlib.sha256(changed).hexdigest()
            checks = validate_case(bad)["checks"]
            if checks.get("original_evidence_hashes") is not True:
                raise AssertionError("forgery control: the re-recorded hash did not pass (" + check + ")")
            if checks.get(check) is not False:
                raise AssertionError("forged " + name + " did not fail " + check)
        finally:
            target.write_bytes(original)
        count += 1
    if not validate_case(moved)["passed"]:
        raise AssertionError("forgery control did not restore")
    return count


class Evidence:
    def __init__(self, receipt, overrides=None):
        self.receipt = receipt
        self.root = Path(receipt["root"]).resolve(strict=True)
        if self.root == Path(self.root.anchor) or not self.root.is_dir():
            raise ValueError("receipt root must be an artifact directory")
        self.checks = {}
        self.errors = []
        self.overrides = overrides or {}

    def path(self, value):
        path = Path(value)
        if not path.is_absolute():
            path = self.root / path
        path = path.resolve(strict=True)
        if path == self.root or self.root not in path.parents:
            raise ValueError("evidence path leaves artifact root")
        return path

    def check(self, name, predicate):
        try:
            if not predicate():
                raise ValueError("required evidence does not match")
        except (KeyError, IndexError, StopIteration, TypeError, ValueError, OSError,
                sqlite3.Error, subprocess.SubprocessError) as error:
            self.checks[name] = False
            self.errors.append(name + ": " + str(error))
        else:
            self.checks[name] = True

    def read(self, name):
        data = self.path(name).read_bytes()
        if hashlib.sha256(data).hexdigest() != self.receipt["evidence_sha256"][name]:
            raise ValueError("changed evidence: " + name)
        # Self-tests replace decoded copies after checking the saved bytes, so
        # semantic guards are tested separately from artifact integrity.
        return copy.deepcopy(self.overrides[name]) if name in self.overrides else json.loads(data)

    def contract(self, role):
        session = self.session(role)
        harness = ROLES[role]
        version = self.receipt[harness + "_version"]
        expected = version + " (Claude Code)" if harness == "claude" else "codex-cli " + version
        capture = self.read("native-versions.json")[harness]
        if not admission.admits(harness, version, self.root, capture["executable_sha256"]):
            return False
        launch = self.read(role + "-state/launch.json")
        saved = self.read(role + "-state/identity.json")
        if (capture != self.receipt["native_versions"][harness] or
                session["native_version"] != capture["version"] or capture["version"] != expected or
                session["native_sha256"] != capture["executable_sha256"] or
                not re.fullmatch(r"[0-9a-f]{64}", capture["executable_sha256"]) or
                saved["schema"] != "buddy.control.v1" or saved["identity"] != session["identity"] or
                saved["launch"] != launch or launch["worktree"] != session["worktree"] or
                launch["harness_version"] != version or launch["permission_mode"] != "workspace-write" or
                launch["hooks"]["configured"] != "observed" or
                launch["hooks"]["observed"] != "unknown" or not launch["gate_command"]):
            return False
        if harness == "claude":
            return launch["hooks"]["mods"] == "builtin-only-" + version
        return launch["hooks"]["trusted"] == "observed" and bool(launch["gate_hash"])

    def cleanup(self, role):
        session = self.session(role)
        child = session["native_child_exit"]
        return (type(session["adapter_exit"]) is int and session["adapter_exit"] == 0 and
                session["unparsed"] == [] and child["status"] == "observed_absent" and
                bool(child["observed_pids"]) and child["remaining_or_unknown_pids"] == [])

    def rows(self, path, query, args=()):
        path = self.path(path)
        wal = Path(str(path) + "-wal")
        if wal.exists() and wal.stat().st_size:
            raise ValueError("snapshot has a nonempty WAL")
        # immutable avoids creating SHM files or accepting an unfinished WAL
        # copy as a complete snapshot. No Buddy store open/migration is used.
        with sqlite3.connect(path.as_uri() + "?mode=ro&immutable=1", uri=True) as db:
            db.row_factory = sqlite3.Row
            return [dict(row) for row in db.execute(query, args)]

    def one(self, path, query, args=()):
        rows = self.rows(path, query, args)
        if len(rows) != 1:
            raise ValueError("expected exactly one ledger row")
        return rows[0]

    def session(self, role):
        return self.receipt["sessions"][role]

    def pair(self, role):
        pair = identity(self.session(role)["identity"])
        if not all(isinstance(part, str) and part for part in pair):
            raise ValueError("empty captured identity")
        return pair

    def native(self, role):
        session = self.session(role)
        events = session["events"]
        pair = self.pair(role)
        if session["harness"] != ROLES[role] or not events:
            return False
        for event in events:
            if (event["schema"] != "buddy.control.v1" or
                    identity(event["identity"]) != pair or event.get("error") or
                    event["identity"]["harness"] != ROLES[role]):
                return False
            if event["completion"]["state"] != "unknown":
                return False
            if "native_event" in event:
                if (event["native_event"]["native_session_id"] != pair[0] or
                        event["native_event"].get("error")):
                    return False
        starts = [event for event in events if event["operation"] == "start"]
        results = [event for event in events if event.get("native_event", {}).get("kind") == "result"]
        idle = [event for event in events if event.get("native_event", {}).get("kind") == "idle"]
        return (len(starts) == 1 and bool(results) and bool(idle) and
                all(event["native_event"].get("result_ref") for event in results) and
                all(event["native_event"].get("result_status") == "completed" for event in results) and
                all(event["state"] == {"state": "observed", "detail": "idle"} for event in idle))

    def worktrees(self):
        paths = [self.path(self.session(role)["worktree"]) for role in ROLES]
        if len(set(paths)) != 3:
            return False
        common = [Path(git(path, "rev-parse", "--path-format=absolute", "--git-common-dir")).resolve()
                  for path in paths]
        if len(set(common)) != 1 or self.root not in common[0].parents:
            return False
        for role, path in zip(ROLES, paths):
            row = self.one(self.receipt["ledger"],
                           "SELECT worktree FROM sessions WHERE session_id=? AND incarnation=?",
                           self.pair(role))
            if Path(row["worktree"]).resolve() != path:
                return False
        return True

    def message(self, msg):
        sender, recipient = self.pair(msg["sender"]), self.pair(msg["recipient"])
        queue, inbox, delivered = msg["queue"], msg["inbox"], msg["delivered"]
        for value, op in ((queue, "queue"), (inbox, "inbox"), (delivered, "delivered")):
            if value["schema"] != "buddy.adapter.v1" or value["operation"] != op:
                return False
        queued = queue["delivery"]
        mid = queued["message_id"]
        if type(mid) is not int or mid <= 0 or queued["state"] != "queued":
            return False
        if identity(inbox["identity"]) != recipient or inbox["messages"]["status"] != "observed":
            return False
        items = [item for item in inbox["messages"]["items"] if item["message_id"] == mid]
        if len(items) != 1 or inbox["messages"]["count"] < len(inbox["messages"]["items"]):
            return False
        fetched, delivery = items[0], delivered["delivery"]
        for item in (queued, fetched, delivery):
            if (item["message_id"] != mid or identity(item["sender"]) != sender or
                    identity(item["recipient"]) != recipient or item["provenance"] != "peer" or
                    item["acknowledged"]["status"] != "unknown" or
                    item["completed"]["status"] != "unknown"):
                return False
        if fetched["state"] != "queued" or not fetched["body"] or delivery["state"] != "delivered":
            return False
        idle = msg["idle_before"]
        if (idle["schema"] != "buddy.adapter.v1" or idle["operation"] != "inspect" or
                identity(idle["identity"]) != recipient or
                idle["lifecycle"] != {"status": "observed", "value": "live"}):
            return False
        # The bootstrap is deliberately the first native turn. Subsequent
        # delivery in both directions must observe real idle, not infer it
        # from successful process initialization or a missing state sample.
        allowed_states = [{"status": "observed", "value": "idle"}]
        if msg["purpose"] == "bootstrap":
            allowed_states.append({"status": "unknown"})
        if idle["observed_state"] not in allowed_states:
            return False
        wake = msg["wake"]
        if (wake["schema"] != "buddy.control.v1" or wake["operation"] != "send" or
                identity(wake["identity"]) != recipient or wake["message_id"] != mid or
                wake["wake_delivery"]["state"] != "delivered" or wake.get("error") or
                wake["body_delivery"]["state"] != "queued" or
                any(wake[field]["state"] != "unknown" for field in
                    ("acknowledgment", "completion"))):
            return False
        artifact = json.loads(self.path(msg["native_fetch"]).read_text())
        marker = artifact["marker"]
        native_output = json.loads(msg["native_output"])
        # A refusal can echo the command (and its marker), so searching the
        # serialized transcript proves nothing. The provider's correlated
        # shell result must report success and contain an actual output line.
        if (not isinstance(marker, str) or len(marker) < 8 or
                not isinstance(native_output, list) or not any(
                    isinstance(result, dict) and result.get("kind") == "shell" and
                    type(result.get("exit_code")) is int and result["exit_code"] == 0 and
                    result.get("is_error") is not True and isinstance(result.get("output"), str) and
                    marker in result["output"].splitlines() for result in native_output) or
                artifact["inbox"] != inbox or
                artifact["delivered"] != delivered):
            return False
        row = self.one(self.receipt["ledger"],
                       "SELECT m.target,m.sender_session,m.body,a.sender_incarnation,"
                       "a.recipient_incarnation,d.delivered FROM inbox m "
                       "JOIN adapter_messages a ON a.msg_id=m.msg_id "
                       "JOIN inbox_delivery d ON d.msg_id=m.msg_id AND d.session_id=m.target "
                       "WHERE m.msg_id=?", (mid,))
        return ((row["sender_session"], row["sender_incarnation"]) == sender and
                (row["target"], row["recipient_incarnation"]) == recipient and
                row["body"] == fetched["body"] and row["delivered"] == delivery["delivered_at"] and
                row["delivered"] > 0)

    def ready(self, item):
        pair = self.pair(item["role"])
        sha = item["sha"]
        inspection = item["inspect"]
        if not SHA.fullmatch(sha) or item["role"] not in WORKERS:
            return False
        if (inspection["schema"] != "buddy.adapter.v1" or inspection["operation"] != "inspect" or
                identity(inspection["identity"]) != pair or
                inspection["ready_sha"] != {"status": "observed", "value": sha}):
            return False
        if git(self.path(self.session(item["role"])["worktree"]), "rev-parse", sha + "^{commit}") != sha:
            return False
        row = self.one(item["ledger"],
                       "SELECT w.ready_sha,c.claim_id,c.session_id,c.incarnation,c.state FROM session_waits w "
                       "JOIN session_wait_targets t ON t.decl=w.decl AND t.session_id=w.session_id "
                       "JOIN claims c ON c.claim_id=t.claim_id WHERE w.session_id=? AND w.incarnation=? "
                       "AND c.claim_id=?", pair + (item["claim_id"],))
        run = next(run for run in self.receipt["validations"] if run["claim_id"] == item["claim_id"])
        git(self.path(self.session(item["role"])["worktree"]),
            "merge-base", "--is-ancestor", sha, run["sha"])
        return (row["ready_sha"] == sha and row["state"] == "open" and
                (row["session_id"], row["incarnation"]) == self.pair("coordinator"))

    def validation(self, item, outcome):
        sha = item["sha"]
        if (item["outcome"] != outcome or not SHA.fullmatch(sha) or
                type(item["exit_code"]) is not int or item["exit_code"] < 0 or
                (item["exit_code"] == 0) != (outcome == "pass") or
                not isinstance(item["command"], list) or not item["command"] or
                not all(isinstance(arg, str) and arg for arg in item["command"])):
            return False
        stdout = self.path(item["stdout"]).read_text()
        stderr = self.path(item["stderr"]).read_text()
        worktree = self.path(self.session("coordinator")["worktree"])
        if git(worktree, "rev-parse", sha + "^{commit}") != sha:
            return False
        # This checker is for this exact deterministic exercise, not arbitrary
        # jobs. Inspect its immutable inputs without executing receipt-supplied
        # programs: an unrelated shell error is not the intentional red run.
        if (len(item["command"]) != 2 or not Path(item["command"][0]).is_absolute() or
                item["command"][1] != "verify.py" or
                git(worktree, "show", sha + ":verify.py", strip=False) != VERIFY_SOURCE or
                git(worktree, "show", sha + ":codex.txt", strip=False) != "fixed\n" or
                git(worktree, "show", sha + ":claude.txt", strip=False) !=
                ("broken\n" if outcome == "fail" else "fixed\n")):
            return False
        if outcome == "fail":
            if item["exit_code"] != 1 or stdout or not stderr.endswith("AssertionError: claude.txt is not fixed\n"):
                return False
        elif stdout != "both worker changes validated\n" or stderr:
            return False
        row = self.one(item["ledger"],
                       "SELECT session_id,incarnation,state,outcome FROM claims WHERE claim_id=?",
                       (item["claim_id"],))
        return ((row["session_id"], row["incarnation"]) == self.pair("coordinator") and
                row["state"] == "released" and row["outcome"] == outcome)

    def flow(self):
        messages = self.receipt["messages"]
        if not isinstance(messages, list) or len(messages) < 6:
            return False
        ids = [item["queue"]["delivery"]["message_id"] for item in messages]
        if len(set(ids)) != len(ids):
            return False
        for role in WORKERS:
            assignments = [i for i, msg in enumerate(messages) if msg["purpose"] == "assignment" and
                           msg["sender"] == "coordinator" and msg["recipient"] == role]
            reports = [i for i, msg in enumerate(messages) if msg["purpose"] == "report" and
                       msg["sender"] == role and msg["recipient"] == "coordinator"]
            if not assignments or not reports or min(assignments) >= min(reports):
                return False
        fail = self.receipt["validations"][0]
        repairs = [(i, msg) for i, msg in enumerate(messages) if msg["purpose"] == "repair" and
                   msg["sender"] == "coordinator" and msg["recipient"] in WORKERS]
        if not repairs:
            return False
        for index, repair in repairs:
            body = next(item["body"] for item in repair["inbox"]["messages"]["items"]
                        if item["message_id"] == repair["queue"]["delivery"]["message_id"])
            if fail["sha"] not in body or not any(
                    msg["purpose"] == "report" and msg["sender"] == repair["recipient"] and
                    msg["recipient"] == "coordinator" for msg in messages[index + 1:]):
                return False
        return True

    def landed(self):
        failed, passed = self.receipt["validations"]
        landing = self.receipt["landing"]
        worktree = self.path(landing["worktree"])
        if (landing["sha"] != passed["sha"] or landing["ref"] != "refs/heads/main" or
                failed["sha"] == passed["sha"] or failed["claim_id"] == passed["claim_id"]):
            return False
        if git(worktree, "rev-parse", landing["ref"]) != passed["sha"]:
            return False
        if git(worktree, "rev-parse", "HEAD") != passed["sha"]:
            return False
        git(worktree, "merge-base", "--is-ancestor", failed["sha"], passed["sha"])
        row = self.one(landing["ledger"], "SELECT state,outcome FROM claims WHERE claim_id=?",
                       (passed["claim_id"],))
        return row == {"state": "released", "outcome": "pass"}


def validate(receipt, overrides=None):
    """Return a bounded verdict; malformed or missing evidence always fails."""
    try:
        evidence = Evidence(receipt, overrides)
    except (KeyError, TypeError, ValueError, OSError) as error:
        return {"checks": {"receipt": False}, "errors": [str(error)], "passed": False}
    check = evidence.check
    check("schema_and_scope", lambda: receipt["schema"] == "buddy.mixed-fleet.v1" and
          receipt["provider"] == "deterministic-loopback" and not receipt.get("error") and
          not receipt.get("cleanup_errors"))
    check("three_captured_sessions", lambda: set(receipt["sessions"]) == set(ROLES) and
          len({evidence.pair(role)[0] for role in ROLES}) == 3 and
          len({evidence.pair(role)[1] for role in ROLES}) == 3)
    for role in ROLES:
        check("contract_" + role, lambda role=role: evidence.contract(role))
        check("native_" + role, lambda role=role: evidence.native(role))
        check("cleanup_" + role, lambda role=role: evidence.cleanup(role))
    check("separate_worktrees_shared_ledger", evidence.worktrees)
    check("assignment_report_repair_flow", evidence.flow)
    messages = receipt.get("messages", [])
    if isinstance(messages, list):
        for index, msg in enumerate(messages):
            check("message_" + str(index), lambda msg=msg: evidence.message(msg))
    ready = receipt.get("ready", [])
    check("both_workers_ready", lambda: isinstance(ready, list) and len(ready) == 4 and
          {(item["role"], item["claim_id"]) for item in ready} ==
          {(role, run["claim_id"]) for role in WORKERS for run in receipt["validations"]})
    if isinstance(ready, list):
        for index, item in enumerate(ready):
            check("ready_" + str(index), lambda item=item: evidence.ready(item))
    check("failed_then_passed_validation", lambda: len(receipt["validations"]) == 2 and
          evidence.validation(receipt["validations"][0], "fail") and
          evidence.validation(receipt["validations"][1], "pass"))
    check("exact_validated_landing", evidence.landed)
    return {"checks": evidence.checks, "errors": evidence.errors,
            "passed": bool(evidence.checks) and all(evidence.checks.values())}


def self_test():
    # Construct actual linked worktrees, Git commits and SQLite snapshots. No
    # native executable/provider is launched; synthetic control frames test
    # acceptance only and must never be described as harness qualification.
    with tempfile.TemporaryDirectory(prefix="buddy-mixed-check-") as directory:
        root = Path(directory)
        work = root / "coordinator"
        work.mkdir()
        git(work, "init", "-q", "-b", "main")
        git(work, "config", "user.name", "Fixture")
        git(work, "config", "user.email", "fixture@example.invalid")
        (work / "claude.txt").write_text("broken\n")
        (work / "codex.txt").write_text("fixed\n")
        (work / "verify.py").write_text(VERIFY_SOURCE)
        fail_result = subprocess.run([sys.executable, "verify.py"], cwd=work, text=True, capture_output=True)
        git(work, "add", "claude.txt", "codex.txt", "verify.py")
        git(work, "-c", "commit.gpgsign=false", "commit", "-qm", "broken")
        fail_sha = git(work, "rev-parse", "HEAD")
        for role in WORKERS:
            git(work, "worktree", "add", "-q", "--detach", str(root / role), fail_sha)
        (work / "claude.txt").write_text("fixed\n")
        pass_result = subprocess.run([sys.executable, "verify.py"], cwd=work, text=True, capture_output=True)
        git(work, "add", "claude.txt")
        git(work, "-c", "commit.gpgsign=false", "commit", "-qm", "repair")
        pass_sha = git(work, "rev-parse", "HEAD")
        database = root / "ledger.db"
        db = sqlite3.connect(database)
        db.executescript("""
          CREATE TABLE sessions(session_id, incarnation, worktree);
          CREATE TABLE claims(claim_id, session_id, incarnation, state, outcome);
          CREATE TABLE session_waits(session_id, incarnation, decl, ready_sha);
          CREATE TABLE session_wait_targets(decl, session_id, claim_id);
          CREATE TABLE inbox(msg_id, target, sender_session, body);
          CREATE TABLE adapter_messages(msg_id, sender_incarnation, recipient_incarnation);
          CREATE TABLE inbox_delivery(msg_id, session_id, delivered);
        """)
        receipt = {"schema": "buddy.mixed-fleet.v1", "provider": "deterministic-loopback",
                   "root": str(root), "ledger": str(database), "sessions": {}, "messages": [],
                   "ready": [], "validations": [], "landing": {"sha": pass_sha,
                   "ref": "refs/heads/main", "worktree": str(work), "ledger": str(database)}}
        cv, xv = admission.recorded_versions("claude")[-1], admission.recorded_versions("codex")[-1]
        receipt.update(claude_version=cv, codex_version=xv, evidence_sha256={},
                       native_versions={"claude": {"version": cv + " (Claude Code)", "executable_sha256": "a" * 64},
                                        "codex": {"version": "codex-cli " + xv, "executable_sha256": "b" * 64}})
        admission.expect(["claude:" + cv + ":" + "a" * 64, "codex:" + xv + ":" + "b" * 64])
        def artifact(name, value):
            path = root / name
            path.parent.mkdir(exist_ok=True)
            path.write_text(json.dumps(value))
            receipt["evidence_sha256"][name] = hashlib.sha256(path.read_bytes()).hexdigest()
        artifact("native-versions.json", receipt["native_versions"])
        for role, harness in ROLES.items():
            ident = {"schema": "buddy.lifecycle.v1", "harness": harness,
                     "session_id": role, "incarnation": role + "-token"}
            events = [{"schema": "buddy.control.v1", "operation": "start", "identity": ident,
                       "completion": {"state": "unknown"}}]
            for kind in ("result", "idle"):
                events.append({"schema": "buddy.control.v1", "operation": "event", "identity": ident,
                               "completion": {"state": "unknown"},
                               "state": {"state": "observed", "detail": "idle"},
                               "native_event": {"kind": kind, "native_session_id": role,
                                                "result_ref": "native-turn", "result_status": "completed"}})
            receipt["sessions"][role] = {"harness": harness, "identity": ident,
                "worktree": str(root / role), "events": events,
                "native_version": receipt["native_versions"][harness]["version"],
                "native_sha256": receipt["native_versions"][harness]["executable_sha256"],
                "adapter_exit": 0, "unparsed": [], "native_child_exit": {"status": "observed_absent",
                    "observed_pids": [100], "remaining_or_unknown_pids": []}}
            # The profile names the run's own Claude version: a 2.1.291 literal
            # stopped matching once the newest record was 2.1.299 (2026-10-14).
            launch = {"worktree": str(root / role), "harness_version": receipt[harness + "_version"],
                "permission_mode": "workspace-write", "gate_command": "fixture gate", "gate_hash": "fixture-hash",
                "hooks": {"configured": "observed", "trusted": "observed", "observed": "unknown",
                          "mods": "builtin-only-" + receipt["claude_version"]}}
            artifact(role + "-state/launch.json", launch)
            artifact(role + "-state/identity.json", {"schema": "buddy.control.v1", "identity": ident, "launch": launch})
            db.execute("INSERT INTO sessions VALUES (?,?,?)", (role, role + "-token", str(root / role)))
        for index, (purpose, sender, recipient) in enumerate([
                ("assignment", "coordinator", "claude_worker"),
                ("assignment", "coordinator", "codex_worker"),
                ("report", "claude_worker", "coordinator"),
                ("report", "codex_worker", "coordinator"),
                ("repair", "coordinator", "claude_worker"),
                ("report", "claude_worker", "coordinator")], 1):
            ident = lambda role: {"session_id": role, "incarnation": role + "-token"}
            body = purpose + " " + fail_sha
            queued = {"message_id": index, "state": "queued", "sender": ident(sender),
                      "recipient": ident(recipient), "provenance": "peer",
                      "acknowledged": {"status": "unknown"}, "completed": {"status": "unknown"}}
            inbox = {"schema": "buddy.adapter.v1", "operation": "inbox", "identity": ident(recipient),
                     "messages": {"status": "observed", "count": 1, "items": [dict(queued, body=body)]}}
            delivered = {"schema": "buddy.adapter.v1", "operation": "delivered",
                         "delivery": dict(queued, state="delivered", delivered_at=100)}
            fetch = root / (str(index) + ".json")
            marker = "native-fetch-" + str(index)
            fetch.write_text(json.dumps({"inbox": inbox, "delivered": delivered, "marker": marker}))
            receipt["messages"].append({"purpose": purpose, "sender": sender, "recipient": recipient,
                "queue": {"schema": "buddy.adapter.v1", "operation": "queue", "delivery": queued},
                "inbox": inbox, "delivered": delivered, "native_fetch": str(fetch),
                "native_output": json.dumps([{"kind": "shell", "exit_code": 0, "output": marker + "\n"}]),
                "idle_before": {"schema": "buddy.adapter.v1", "operation": "inspect", "identity": ident(recipient),
                    "lifecycle": {"status": "observed", "value": "live"},
                    "observed_state": {"status": "observed", "value": "idle"}},
                "wake": {"schema": "buddy.control.v1", "operation": "send", "identity": ident(recipient),
                    "message_id": index, "wake_delivery": {"state": "delivered"},
                    "body_delivery": {"state": "queued"}, "acknowledgment": {"state": "unknown"},
                    "completion": {"state": "unknown"}}})
            db.execute("INSERT INTO inbox VALUES (?,?,?,?)", (index, recipient, sender, body))
            db.execute("INSERT INTO adapter_messages VALUES (?,?,?)", (index, sender + "-token", recipient + "-token"))
            db.execute("INSERT INTO inbox_delivery VALUES (?,?,?)", (index, recipient, 100))
        for outcome, sha, code in (("fail", fail_sha, 1), ("pass", pass_sha, 0)):
            db.execute("INSERT INTO claims VALUES (?,?,?,?,?)", (outcome, "coordinator", "coordinator-token", "released", outcome))
            stdout, stderr = root / (outcome + ".out"), root / (outcome + ".err")
            executed = fail_result if outcome == "fail" else pass_result
            if executed.returncode != code:
                raise AssertionError("fixture verify.py exited %d, want %d" % (executed.returncode, code))
            stdout.write_text(executed.stdout)
            stderr.write_text(executed.stderr)
            receipt["validations"].append({"outcome": outcome, "sha": sha, "exit_code": code,
                "claim_id": outcome, "ledger": str(database), "command": [sys.executable, "verify.py"],
                "stdout": str(stdout), "stderr": str(stderr)})
        db.commit()
        for outcome, sha in (("fail", fail_sha), ("pass", pass_sha)):
            snapshot = root / (outcome + "-ready.db")
            with sqlite3.connect(snapshot) as ready_db:
                db.backup(ready_db)
                ready_db.execute("UPDATE claims SET state='open',outcome='' WHERE claim_id=?", (outcome,))
                for role in WORKERS:
                    ready_db.execute("INSERT INTO session_waits VALUES (?,?,?,?)", (role, role + "-token", role + "-wait", sha))
                    ready_db.execute("INSERT INTO session_wait_targets VALUES (?,?,?)", (role + "-wait", role, outcome))
                    receipt["ready"].append({"role": role, "sha": sha, "claim_id": outcome, "ledger": str(snapshot),
                        "inspect": {"schema": "buddy.adapter.v1", "operation": "inspect",
                            "identity": {"session_id": role, "incarnation": role + "-token"},
                            "ready_sha": {"status": "observed", "value": sha}}})
        db.close()
        verdict = validate(receipt)
        if not verdict["passed"]:
            raise AssertionError(verdict)
        contract_self_test(receipt)
        mutations = {
            "empty_receipt": lambda value: value.clear(),
            "no_messages": lambda value: value.update(messages=[]),
            "no_ready": lambda value: value.update(ready=[]),
            "missing_native_start": lambda value: value["sessions"]["coordinator"].update(events=[]),
            "wrong_incarnation": lambda value: value["messages"][0]["inbox"]["identity"].update(incarnation="stale"),
            "native_result_as_completion": lambda value: value["sessions"]["claude_worker"]["events"][1]["completion"].update(state="complete"),
            "no_native_fetch": lambda value: value["messages"][0].update(native_output=""),
            "marker_only_in_denied_output": lambda value: value["messages"][0].update(native_output=json.dumps([
                {"kind": "shell", "exit_code": 1, "is_error": True, "output": "native-fetch-1\n"}])),
            "marker_only_in_echoed_command": lambda value: value["messages"][0].update(native_output=json.dumps([
                {"kind": "shell", "exit_code": 0, "output": "echo native-fetch-1\n"}])),
            "wake_as_body_delivery": lambda value: value["messages"][0]["wake"]["body_delivery"].update(state="delivered"),
            "missing_repair": lambda value: value["messages"].pop(4),
            "wrong_landing_sha": lambda value: value["landing"].update(sha=fail_sha),
            "failed_run_claimed_pass": lambda value: value["validations"][0].update(outcome="pass"),
            "outside_artifact_root": lambda value: value.update(ledger="/etc/passwd"),
        }
        for name, mutation in mutations.items():
            altered = copy.deepcopy(receipt)
            mutation(altered)
            if validate(altered)["passed"]:
                raise AssertionError("broken receipt accepted: " + name)
        for filename, replacement in (("pass.out", ""), ("fail.err", "Permission denied\n")):
            path = root / filename
            original = path.read_text()
            path.write_text(replacement)
            if validate(receipt)["passed"]:
                raise AssertionError("unrelated validation output accepted: " + filename)
            path.write_text(original)
        # Keep the receipt's claimed outcome, landing and ancestry consistent
        # with a new commit. Only reading the actual blobs detects that either
        # validation itself was bypassed or the reported repair did not occur.
        for filename, replacement in (("verify.py", "print('validation skipped')\n"),
                                      ("claude.txt", "still broken\n")):
            (work / filename).write_text(replacement)
            git(work, "add", filename)
            git(work, "-c", "commit.gpgsign=false", "commit", "-qm", "unqualified fixture change")
            changed = git(work, "rev-parse", "HEAD")
            altered = copy.deepcopy(receipt)
            altered["validations"][1]["sha"] = changed
            altered["landing"]["sha"] = changed
            if validate(altered)["passed"]:
                raise AssertionError("incorrect validated Git content accepted: " + filename)
            git(work, "reset", "--hard", pass_sha)
        # These mutations alter the independent evidence, not the runner's
        # claims: receipt-only tests would miss both classes of false pass.
        with sqlite3.connect(database) as db:
            db.execute("DELETE FROM inbox_delivery WHERE msg_id=1")
        if validate(receipt)["passed"]:
            raise AssertionError("missing durable delivery accepted")
        with sqlite3.connect(database) as db:
            db.execute("INSERT INTO inbox_delivery VALUES (1,'claude_worker',100)")
        git(work, "update-ref", "refs/heads/main", fail_sha)
        if validate(receipt)["passed"]:
            raise AssertionError("unlanded validated commit accepted")
        print("mixed-fleet acceptance checker: positive control and 20 broken cases passed")


def contract_self_test(receipt):
    """Corrupt copies of real or synthetic evidence; never edit native receipts."""
    if not validate(receipt)["passed"]:
        raise AssertionError("positive receipt failed")
    ignored = copy.deepcopy(receipt)
    ignored.update(passed=False, validation={"passed": False}, checks={"fixture": False})
    if not validate(ignored)["passed"]:
        raise AssertionError("checker depended on fixture success flags")
    e = Evidence(receipt)
    count = 0
    def refuse(value, overrides, check):
        nonlocal count
        verdict = validate(value, overrides)
        if verdict["passed"] or verdict["checks"].get(check) is not False:
            raise AssertionError("damaged evidence did not fail " + check)
        count += 1
    for role, harness in ROLES.items():
        key = "contract_" + role
        for field, value in (("native_version", "unqualified version"), ("native_sha256", "0" * 64)):
            altered = copy.deepcopy(receipt)
            altered["sessions"][role][field] = value
            refuse(altered, {}, key)
        for field, value in (("harness_version", "0.160.1" if harness == "codex" else "2.1.292"),
                             ("permission_mode", "full-access"), ("worktree", "/different-worktree"),
                             ("gate_command", "")):
            launch = e.read(role + "-state/launch.json")
            launch[field] = value
            saved = e.read(role + "-state/identity.json")
            saved["launch"] = launch
            refuse(receipt, {role + "-state/launch.json": launch, role + "-state/identity.json": saved}, key)
        launch = e.read(role + "-state/launch.json")
        launch["hooks"]["mods" if harness == "claude" else "trusted"] = "unqualified"
        saved = e.read(role + "-state/identity.json")
        saved["launch"] = launch
        refuse(receipt, {role + "-state/launch.json": launch, role + "-state/identity.json": saved}, key)
        saved = e.read(role + "-state/identity.json")
        saved["identity"]["incarnation"] = "stale"
        refuse(receipt, {role + "-state/identity.json": saved}, key)
        altered = copy.deepcopy(receipt)
        altered["evidence_sha256"].pop(role + "-state/launch.json")
        refuse(altered, {}, key)
        for field, value in (("adapter_exit", 1), ("unparsed", ["unparsed native output"]),
                             ("native_child_exit", {"status": "unknown"})):
            altered = copy.deepcopy(receipt)
            altered["sessions"][role][field] = value
            refuse(altered, {}, "cleanup_" + role)
    for harness, role in (("claude", "claude_worker"), ("codex", "coordinator")):
        altered = copy.deepcopy(receipt)
        altered[harness + "_version"] = "2.1.292" if harness == "claude" else "0.160.1"
        refuse(altered, {}, "contract_" + role)
        versions = e.read("native-versions.json")
        versions[harness]["version"] = "changed native capture"
        refuse(receipt, {"native-versions.json": versions}, "contract_" + role)
    altered = copy.deepcopy(receipt)
    altered["evidence_sha256"]["native-versions.json"] = "0" * 64
    refuse(altered, {}, "contract_claude_worker")
    print(f"mixed-fleet contract checker: positive control and {count} damaged cases passed")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--receipt", type=Path)
    admission.add_argument(parser)
    args = parser.parse_args()
    admission.expect(args.candidate)
    if args.self_test:
        if args.receipt:
            contract_self_test(json.loads(args.receipt.read_text()))
        else:
            self_test()
        return 0
    if not args.receipt:
        parser.error("--receipt or --self-test is required")
    try:
        result = validate(json.loads(args.receipt.read_text()))
    except (ValueError, OSError) as error:
        result = {"checks": {"receipt": False}, "errors": [str(error)], "passed": False}
    print(json.dumps(result, indent=2))
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
