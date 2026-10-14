#!/usr/bin/env python3
"""What the committed admission records admit (D-079), and which qualification
candidate a run bound (D-080): one module for the runners and their checkers.

WHY ONE MODULE. Before this, every checker carried its own literal list
(CLAUDE_VERSIONS = ("2.1.289", "2.1.290", "2.1.291"), "codex-cli 0.160.0") and
the runners another. D-079 retired 2.1.289 and 2.1.290 the same day, so those
lists admitted two versions no record or binary backs, and a new build needed a
hand edit in a dozen files before its first run could even be checked. The
records are the one list; this module reads them.

WHY A RUN BINDS ITS OWN BINARY. Every runner launches the native through a
metadata observer, and D-079 hashes what it is told to launch, so a runner in
record mode fails its first case (artifact_mismatch, measured on the recorded
2.1.291 copy). D-080's candidate route is the only route a suite can take, for a
recorded build too. A runner therefore binds the binary it was given: it hashes
it, reads its `--version` (DISABLE_AUTOUPDATER=1, throwaway config dirs), and
types the binding onto every adapter command line it starts for that harness.
The environment carries nothing (the previous BUDDY_QUALIFY_* transport made a
plain run fail unless a wrapper set four variables).

WHAT A CHECKER ACCEPTS (`admits`). A receipt is admissible evidence for
(harness, version) when either
  - it is a candidate run: exactly one binding for the harness is recorded (the
    adapter's captured receipt or the runner's result names harness, version
    and sha256), the adapter's own control receipts carry that sha256 (so the
    route was really taken), and no control receipt names a sha256 no binding
    explains; and then the binding equals `--candidate` when the checker was
    given one, or else equals a committed record's bytes for that version. A
    receipt that ran as a candidate for OTHER bytes is never accepted because a
    record for the version exists;
  - or it is a legacy run (no binding anywhere): the version has a record, and
    the checker was not told to expect a candidate.
Retired versions (2.1.289, 2.1.290) have no record and are refused: their
receipts stay evidence of what was measured, and grant nothing now.

Run: python3 -I scripts/admission_lib.py --self-test   (hermetic)
"""
import hashlib
import json
import os
import pathlib
import subprocess
import sys
import tempfile

sys.dont_write_bytecode = True

REPO = pathlib.Path(__file__).resolve().parent.parent
RECORDS = REPO / "internal" / "harness" / "admissions"
HARNESSES = ("claude", "codex")
HEX = set("0123456789abcdef")
# The receipt files a scan reads. Anything else under a run's root (native
# transcripts, the worktree) is never where the adapter writes a receipt.
SCANNED = (".json", ".jsonl", ".events")
SCAN_LIMIT = 64 << 20


def version_key(v):
    return tuple(int(x) for x in v.split("."))


def records(harness, root=None):
    """The committed records for harness, parsed. A malformed record makes the
    adapter refuse everything; here it raises, so a checker cannot pass on a
    set the adapter would not even load."""
    base = (pathlib.Path(root) if root else RECORDS) / harness
    out = []
    for p in sorted(base.glob("*/admission.json")):
        r = json.loads(p.read_text())
        if r.get("harness") != harness or not isinstance(r.get("version"), str):
            raise ValueError("record " + str(p) + " is not a " + harness + " record")
        out.append(r)
    return out


def recorded(harness, root=None):
    """{version: {sha256, ...}} from the committed records."""
    found = {}
    for r in records(harness, root):
        found.setdefault(r["version"], set()).add(r["executable"]["sha256"])
    return found


def recorded_versions(harness, extra=(), root=None):
    """The recorded versions, oldest first, plus any extra (a bound candidate)."""
    found = set(recorded(harness, root)) | {v for v in extra if v}
    return tuple(sorted(found, key=version_key))


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def version_text(harness, native):
    """The native's exact `--version` line, read with the updater disabled and
    throwaway config dirs, so probing never writes the operator's state."""
    with tempfile.TemporaryDirectory(prefix="buddy-version-") as tmp:
        env = dict(os.environ, DISABLE_AUTOUPDATER="1", CLAUDE_CONFIG_DIR=os.path.join(tmp, "claude"),
                   CODEX_HOME=os.path.join(tmp, "codex"))
        os.makedirs(env["CODEX_HOME"])
        p = subprocess.run([str(native), "--version"], cwd=tmp, env=env, text=True, capture_output=True, timeout=60)
    if p.returncode:
        raise RuntimeError(str(native) + " --version failed: " + p.stderr.strip()[:200])
    return p.stdout.strip()


def parse_version(harness, text):
    """The declared version inside an exact `--version` line, or an error: the
    adapter compares the whole line (version_mismatch), so a shape this does not
    know is a new contract, not a version to guess at."""
    if harness == "claude" and text.endswith(" (Claude Code)"):
        v = text.removesuffix(" (Claude Code)")
    elif harness == "codex" and text.startswith("codex-cli "):
        v = text.removeprefix("codex-cli ")
    else:
        raise ValueError("unrecognised " + harness + " --version line: " + repr(text))
    try:
        version_key(v)
    except ValueError:
        raise ValueError("unrecognised " + harness + " version: " + repr(v)) from None
    return v


# ------------------------------------------------------------- runner side

CANDIDATES = {}


def bind(harness, native, probe=version_text):
    """Bind the binary a runner was given as its harness's candidate (D-080).

    The artifact is the RESOLVED regular file (the adapter refuses a link), so
    `~/.local/bin/claude` binds the Mach-O it names and a Codex release's `codex`
    link binds `bin/codex`. Binding twice to other bytes in one run raises: one
    run qualifies one build per harness."""
    if harness not in HARNESSES:
        raise RuntimeError("unknown harness " + repr(harness))
    path = pathlib.Path(native).resolve(strict=True)
    if not path.is_file():
        raise RuntimeError(str(path) + " is not a regular file")
    digest = sha256(path)
    text = probe(harness, path)
    binding = {"harness": harness, "version": parse_version(harness, text), "version_output": text,
               "artifact": str(path), "sha256": digest}
    if digest != sha256(path):
        raise RuntimeError(str(path) + " changed while it was being bound")
    prior = CANDIDATES.get(harness)
    if prior is not None and prior != binding:
        raise RuntimeError("a run binds one " + harness + " candidate; already bound " + prior["artifact"])
    CANDIDATES[harness] = binding
    return binding


def bind_args(args):
    """Bind every native a runner's arguments name (--claude, --codex) and
    point the arguments at the resolved artifacts. A typed --claude-version
    must be the binary's own; untyped, it becomes the binary's. Returns the
    bindings, for the runner's result.json."""
    out = []
    for harness in HARNESSES:
        path = getattr(args, harness, None)
        if not path:
            continue
        binding = bind(harness, path)
        setattr(args, harness, binding["artifact"])
        if harness == "claude" and hasattr(args, "claude_version"):
            if args.claude_version not in (None, binding["version"]):
                raise RuntimeError("--claude-version " + args.claude_version + " is not the binary's " + binding["version_output"])
            args.claude_version = binding["version"]
        out.append(dict(binding))
    return out


def candidate_flags(argv):
    """Insert the bound candidate's typed flags after the adapter path, for the
    harness the command line names. A hook invocation never takes them, and a
    harness with no binding runs as typed (record mode)."""
    argv = [str(x) for x in argv]
    if len(argv) > 1 and argv[1] == "hook":
        return argv
    harness = argv[argv.index("--harness") + 1] if "--harness" in argv[:-1] else None
    c = CANDIDATES.get(harness)
    if c is None:
        return argv
    return argv[:1] + ["--qualify-candidate", c["artifact"], "--qualify-sha256", c["sha256"]] + argv[1:]


def summary(harness):
    """The binding as a result.json records it, or None."""
    c = CANDIDATES.get(harness)
    return dict(c) if c else None


# ------------------------------------------------------------ checker side

EXPECTED = {}


def add_argument(parser):
    parser.add_argument("--candidate", action="append", default=[], metavar="HARNESS:VERSION:SHA256",
                        help="the qualification candidate this run must have bound (D-080); "
                             "without it a candidate run is accepted only for a recorded build's bytes")


def expect(values):
    EXPECTED.clear()
    for value in values or ():
        parts = value.split(":")
        if len(parts) != 3 or parts[0] not in HARNESSES or len(parts[2]) != 64 or not set(parts[2]) <= HEX:
            raise SystemExit("--candidate wants harness:version:sha256, got " + repr(value))
        version_key(parts[1])
        if parts[0] in EXPECTED:
            raise SystemExit("--candidate names " + parts[0] + " twice")
        EXPECTED[parts[0]] = (parts[1], parts[2])


def synthetic(harness, version, path):
    """For a checker's synthetic self-test: the fake native's sha256, set as the
    expected candidate for harness, because fake bytes can never be a record's."""
    digest = sha256(path)
    EXPECTED[harness] = (version, digest)
    return digest


def _walk(value, bindings, strays):
    if isinstance(value, dict):
        for k, v in value.items():
            if k == "qualification_candidate":
                if isinstance(v, dict):
                    bindings.add((v.get("harness"), v.get("version"), v.get("sha256")))
                elif isinstance(v, str):
                    strays.add(v)
                elif v is not None:
                    strays.add(repr(v))
            else:
                _walk(v, bindings, strays)
    elif isinstance(value, list):
        for v in value:
            _walk(v, bindings, strays)


def scan(root):
    """Every qualification_candidate under root: the bindings (dicts naming
    harness, version, sha256) and the control receipts' sha256 strings."""
    bindings, shas = set(), set()
    for p in sorted(pathlib.Path(root).rglob("*")):
        if not p.is_file() or p.is_symlink() or not p.name.endswith(SCANNED) or p.stat().st_size > SCAN_LIMIT:
            continue
        text = p.read_text(errors="replace")
        try:
            docs = [json.loads(text)]
        except ValueError:
            docs = []
            for line in text.splitlines():
                try:
                    docs.append(json.loads(line))
                except ValueError:
                    pass
        for d in docs:
            _walk(d, bindings, shas)
    return bindings, shas


def admits(harness, version, root, native_sha=None, records_root=None):
    """True when the run under root is admissible evidence for (harness,
    version); see the module comment for the rule. native_sha is the bytes the
    receipt says ran, when it says: a candidate run must have bound exactly
    those, and a run that never went through the adapter (Codex's direct gate
    suite) is judged by them alone."""
    bindings, shas = scan(root)
    if shas - {b[2] for b in bindings}:
        return False
    mine = {b for b in bindings if b[0] == harness}
    expected = EXPECTED.get(harness)
    if len(mine) > 1:
        return False
    if mine:
        (_, v, sha), = mine
        if v != version or sha not in shas or native_sha not in (None, sha):
            return False
        if expected is not None:
            return expected == (v, sha)
        return sha in recorded(harness, records_root).get(v, ())
    if native_sha is not None:
        if expected is not None:
            return expected == (version, native_sha)
        return native_sha in recorded(harness, records_root).get(version, ())
    if expected is not None:
        return False
    return version in recorded(harness, records_root)


def need(harness, version, root, native_sha=None):
    """admits() for a checker's contract lambda, raising with the reason a
    contract check prints."""
    if not admits(harness, version, root, native_sha):
        raise ValueError(harness + " " + repr(version) + " is not admissible here: no record for it, or the run "
                         "bound a candidate the records and --candidate do not name")
    return True


# --------------------------------------------------------------- self-test

def self_test():
    results = {}

    def case(name, ok):
        results[name] = bool(ok)

    with tempfile.TemporaryDirectory(prefix="admission-lib-") as tmp:
        tmp = pathlib.Path(tmp)
        rec = tmp / "records"
        (rec / "claude/2.1.291-aaaaaaaa").mkdir(parents=True)
        (rec / "claude/2.1.291-aaaaaaaa/admission.json").write_text(json.dumps(
            {"harness": "claude", "version": "2.1.291", "executable": {"sha256": "a" * 64}}))
        (rec / "codex").mkdir()
        case("recorded_versions_read_from_records", recorded_versions("claude", root=rec) == ("2.1.291",))
        case("extra_version_sorted_in", recorded_versions("claude", ("2.1.296",), root=rec) == ("2.1.291", "2.1.296"))

        def run(name, files):
            d = tmp / name
            for rel, value in files.items():
                (d / rel).parent.mkdir(parents=True, exist_ok=True)
                (d / rel).write_text(value if isinstance(value, str) else json.dumps(value))
            return d

        def saved(v, sha, h="claude"):
            return {"identity": {}, "qualification_candidate": {"harness": h, "version": v, "sha256": sha}}

        def events(*shas):
            return [{"operation": "start", "qualification_candidate": s} for s in shas]

        legacy = run("legacy", {"adapter-events.json": [{"operation": "start"}]})
        EXPECTED.clear()
        case("legacy_recorded_version_admitted", admits("claude", "2.1.291", legacy, records_root=rec))
        case("legacy_retired_version_refused", not admits("claude", "2.1.290", legacy, records_root=rec))
        recorded_bytes = run("recorded", {"worker.json": saved("2.1.291", "a" * 64), "adapter-events.json": events("a" * 64)})
        case("candidate_of_recorded_bytes_admitted", admits("claude", "2.1.291", recorded_bytes, records_root=rec))
        other = run("other", {"worker.json": saved("2.1.291", "b" * 64), "adapter-events.json": events("b" * 64)})
        case("candidate_of_other_bytes_refused_despite_record", not admits("claude", "2.1.291", other, records_root=rec))
        new = run("new", {"worker.json": saved("2.1.296", "c" * 64), "a/adapter-events.json": events("c" * 64),
                          "a/worker.json.events": "\n".join(json.dumps(e) for e in events("c" * 64)) + "\n"})
        case("unrecorded_candidate_refused_without_argument", not admits("claude", "2.1.296", new, records_root=rec))
        expect(["claude:2.1.296:" + "c" * 64])
        case("unrecorded_candidate_admitted_with_argument", admits("claude", "2.1.296", new, records_root=rec))
        case("argument_version_must_match_receipt", not admits("claude", "2.1.291", new, records_root=rec))
        case("argument_refuses_legacy_run", not admits("claude", "2.1.291", legacy, records_root=rec))
        expect(["claude:2.1.296:" + "d" * 64])
        case("argument_sha_must_match", not admits("claude", "2.1.296", new, records_root=rec))
        EXPECTED.clear()
        stray = run("stray", {"worker.json": saved("2.1.291", "a" * 64), "adapter-events.json": events("a" * 64, "e" * 64)})
        case("unexplained_receipt_sha_refused", not admits("claude", "2.1.291", stray, records_root=rec))
        unrouted = run("unrouted", {"result.json": {"qualification_candidate": {"harness": "claude", "version": "2.1.291",
                                                                                "sha256": "a" * 64}},
                                    "adapter-events.json": [{"operation": "start"}]})
        case("binding_without_adapter_receipt_refused", not admits("claude", "2.1.291", unrouted, records_root=rec))
        two = run("two", {"worker.json": saved("2.1.291", "a" * 64), "b/worker.json": saved("2.1.291", "f" * 64),
                          "adapter-events.json": events("a" * 64, "f" * 64)})
        case("two_bindings_refused", not admits("claude", "2.1.291", two, records_root=rec))
        case("other_harness_binding_not_mine", not admits("codex", "0.160.0", recorded_bytes, records_root=rec))
        case("candidate_native_sha_must_be_bound_bytes", not admits("claude", "2.1.291", recorded_bytes, "b" * 64, records_root=rec))
        direct = run("direct", {"result.json": {"codex_version": "codex-cli 0.160.0"}})
        (rec / "codex/0.160.0-bbbbbbbb").mkdir()
        (rec / "codex/0.160.0-bbbbbbbb/admission.json").write_text(json.dumps(
            {"harness": "codex", "version": "0.160.0", "executable": {"sha256": "b" * 64}}))
        case("direct_run_recorded_bytes_admitted", admits("codex", "0.160.0", direct, "b" * 64, records_root=rec))
        case("direct_run_other_bytes_refused", not admits("codex", "0.160.0", direct, "9" * 64, records_root=rec))
        expect(["codex:0.162.1:" + "9" * 64])
        case("direct_run_candidate_bytes_admitted", admits("codex", "0.162.1", direct, "9" * 64, records_root=rec))
        case("direct_run_candidate_other_bytes_refused", not admits("codex", "0.162.1", direct, "8" * 64, records_root=rec))
        EXPECTED.clear()
        CANDIDATES.clear()
        exe = tmp / "claude-bin"
        exe.write_bytes(b"#!/bin/sh\n")
        link = tmp / "claude-link"
        link.symlink_to(exe)
        b = bind("claude", link, probe=lambda h, p: "2.1.296 (Claude Code)")
        case("bind_resolves_link_and_hashes", b["artifact"] == str(exe.resolve()) and b["sha256"] == sha256(exe) and
             b["version"] == "2.1.296")
        flags = candidate_flags(["adapter", "--harness", "claude", "--executable", "obs"])
        case("flags_typed_after_adapter", flags[:5] == ["adapter", "--qualify-candidate", str(exe.resolve()),
                                                        "--qualify-sha256", sha256(exe)])
        case("flags_not_on_hook", candidate_flags(["adapter", "hook", "--harness", "claude"]) == ["adapter", "hook", "--harness", "claude"])
        case("flags_only_for_bound_harness", candidate_flags(["adapter", "--harness", "codex"]) == ["adapter", "--harness", "codex"])
        try:
            bind("claude", exe, probe=lambda h, p: "2.1.297 (Claude Code)")
            case("rebind_other_refused", False)
        except RuntimeError:
            case("rebind_other_refused", True)
        for name, text in (("codex_shape_refused_for_claude", "codex-cli 0.162.1"), ("garbage_version_refused", "2.x (Claude Code)")):
            try:
                parse_version("claude", text)
                case(name, False)
            except ValueError:
                case(name, True)
        case("codex_version_parsed", parse_version("codex", "codex-cli 0.162.1") == "0.162.1")
        CANDIDATES.clear()
        EXPECTED.clear()
    return {"passed": all(results.values()), "checks": results}


if __name__ == "__main__":
    if sys.argv[1:] != ["--self-test"]:
        raise SystemExit("usage: python3 -I scripts/admission_lib.py --self-test")
    out = self_test()
    print(json.dumps(out, indent=2))
    sys.exit(0 if out["passed"] else 1)
