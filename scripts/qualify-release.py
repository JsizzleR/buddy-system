#!/usr/bin/env python3
"""Qualify one native harness build for admission: retain it, run every tier's
suites from the retained copy through the candidate route, run each suite's
independent checker, and write the admission record for the tiers that earned
it (D-079, D-080, D-081).

  python3 scripts/qualify-release.py --harness claude --executable ~/.local/share/claude/versions/X
  python3 scripts/qualify-release.py --harness codex \\
      --executable ~/.codex/packages/standalone/releases/X-aarch64-apple-darwin/bin/codex --claude-peer PATH
  python3 -I scripts/qualify-release.py --self-test     (hermetic: fake suites, launches nothing)

LAUNCHES claude/codex: operator-authorized only, loopback providers only (every
runner it drives is a deterministic loopback fixture, and the adapter's candidate
route refuses any other provider).

WHY THIS EXISTS. Before it, a new release was qualified by hand: copy the binary
somewhere, export four BUDDY_QUALIFY_* variables, run six runners, run six
checkers, then hand-write a record from their outputs (the 2026-10-13 backfill
was a scratch script). Every step was a place to qualify one set of bytes and
record another. Here one process holds the sha256 from the copy to the record.

THE STEPS, AND THE FAILURE EACH ONE PREVENTS.
 1. Read the executable's `--version` (DISABLE_AUTOUPDATER=1) and hash it.
 2. RETAIN it at ~/.buddy/harness/<harness>/<version>-<sha8>/ (Claude: the one
    Mach-O as `claude`; Codex: the whole release tree, because Codex execs its
    sidecars by relative path and D-079 hashes all of them). Copied with ditto
    into a stage directory beside the target, verified entry by entry against
    the source, then renamed into place: a half-copied package is never at the
    target path. An existing package is NEVER overwritten: if it is byte-identical
    (every entry) it is reused, and otherwise the run refuses. The updater keeps
    three versions and deletes the rest, so the record's bytes must live where
    no updater writes (2.1.289 and 2.1.290 were lost exactly that way).
 3. Run every suite the CODE requires (admissionSuites in internal/harness/
    admission.go, parsed here so there is one list) FROM THE RETAINED COPY. Each
    runner binds what it is given as its candidate (admission_lib.bind), so every
    adapter it starts is typed `--qualify-candidate <retained> --qualify-sha256`.
    A suite counts only when its result names that exact binding and the
    adapter's own control receipts carry the sha256 (the route was taken); Codex's
    direct gate suite runs no adapter and must name the sha256 as codex_sha256.
 4. Run each suite's independent checker with `--candidate harness:version:sha`,
    so a checker cannot accept the run because some record names the version.
 5. Grant a tier only when every one of its suites ran green and its checker
    passed; no record at all without `start`. The record, a summary and the
    checker outputs are written under the repository, with $HOME scrubbed to `~`,
    and every digest the record carries is over the bytes written there (the
    receipts stay in /private/tmp; their digests are named in the summary).
    Runner and checker digests are git blobs at HEAD, so the tree must be clean
    and HEAD unmoved from the first suite to the last, or nothing is written.

What it does not do: decide that a new release's changed hook semantics are
harmless (that is a reading of the release notes and the code's contract, done
before running this), commit, or edit any settings.
"""
import argparse
import datetime
import hashlib
import importlib.util
import json
import os
import pathlib
import platform
import re
import shutil
import stat
import subprocess
import sys
import tempfile

sys.dont_write_bytecode = True
REPO = pathlib.Path(__file__).resolve().parent.parent
SCRIPTS = REPO / "scripts"


def _admission_lib():
    name = "buddy_admission_lib"
    if name not in sys.modules:
        spec = importlib.util.spec_from_file_location(name, SCRIPTS / "admission_lib.py")
        module = importlib.util.module_from_spec(spec)
        sys.modules[name] = module
        spec.loader.exec_module(module)
    return sys.modules[name]


admission = _admission_lib()


class Refusal(Exception):
    """A refusal with nothing granted; the message names why."""


# ------------------------------------------------------------- the code's list

TIER_ORDER = ("start", "resume", "compaction_observation")


def code_suites(source=None):
    """admissionSuites from internal/harness/admission.go: {harness: {tier:
    [(suite, runner, checker)]}}. Parsed rather than copied, so a suite the code
    adds is one this runner runs; the hermetic tier's record validation catches a
    parse that drifted, because a record missing a required suite refuses."""
    text = (source or (REPO / "internal/harness/admission.go").read_text())
    tiers = dict(re.findall(r'\b(Tier\w+)\s*=\s*"([a-z_]+)"', text))
    body = text[text.index("var admissionSuites"):]
    body = body[:body.index("\n}\n")]
    out, harness, tier = {}, None, None
    for token in re.finditer(r'"(claude|codex)":\s*\{|\b(Tier\w+):|\{"([a-z-]+)", "(qualify-[a-z-]+\.py)", "(check-[a-z-]+\.py)"\}', body):
        if token.group(1):
            harness = token.group(1)
            out[harness] = {}
        elif token.group(2):
            tier = tiers[token.group(2)]
            out[harness][tier] = []
        else:
            out[harness][tier].append(token.group(3, 4, 5))
    if set(out) != {"claude", "codex"} or any(set(v) != set(TIER_ORDER) or not all(v.values()) for v in out.values()):
        raise Refusal("could not read admissionSuites from internal/harness/admission.go")
    return out


def code_constants(source=None):
    text = (source or (REPO / "internal/harness/admission.go").read_text())
    contracts = {"claude": re.search(r'claudeAdapterContract\s*=\s*"([^"]+)"', text).group(1),
                 "codex": re.search(r'codexAdapterContract\s*=\s*"([^"]+)"', text).group(1)}
    builtins = re.findall(r'"([^"]+)"', re.search(r'claudeKnownBuiltins\s*=\s*\[\]string\{([^}]*)\}', text).group(1))
    gate = {}
    m = re.search(r'func gateContract\(harness string\) AdmissionGate \{\s*command := "((?:[^"\\]|\\.)*)"(.*?)\n\}', text, re.S)
    command = json.loads('"' + m.group(1) + '"')
    for h, hash_text in re.findall(r'case "(claude|codex)":\s*return AdmissionGate\{Command: command, Hash: "((?:[^"\\]|\\.)*)"\}', m.group(2)):
        gate[h] = {"command": command, "hash": json.loads('"' + hash_text + '"')}
    return contracts, builtins, gate


# --------------------------------------------------------------- the package

def entries(root):
    """A release tree as D-079's record lists it: relative path, type, mode,
    and per file sha256 and size, per link its target. Special files refuse."""
    root = pathlib.Path(root)
    out = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames.sort()
        for name in sorted(dirnames + filenames):
            p = pathlib.Path(dirpath) / name
            rel = p.relative_to(root).as_posix()
            st = os.lstat(p)
            mode = "%04o" % stat.S_IMODE(st.st_mode)
            if stat.S_ISLNK(st.st_mode):
                out.append({"path": rel, "type": "symlink", "target": os.readlink(p)})
            elif stat.S_ISDIR(st.st_mode):
                out.append({"path": rel, "type": "dir", "mode": mode})
            elif stat.S_ISREG(st.st_mode):
                out.append({"path": rel, "type": "file", "mode": mode, "sha256": admission.sha256(p), "size": st.st_size})
            else:
                raise Refusal("special file in the package: " + rel)
    out.sort(key=lambda e: e["path"])
    return out


def source_of(harness, executable):
    """(artifact, package source, path of the executable inside the package)."""
    artifact = pathlib.Path(executable).expanduser().resolve(strict=True)
    if not artifact.is_file():
        raise Refusal(str(artifact) + " is not a regular file")
    if harness == "claude":
        return artifact, artifact, "claude"
    if artifact.name != "codex" or artifact.parent.name != "bin":
        raise Refusal("a Codex executable is bin/codex inside its release tree, not " + str(artifact))
    return artifact, artifact.parent.parent, "bin/codex"


def snapshot(harness, package):
    """What a retained copy must equal: the tree's entries, or for Claude the
    one file's mode, digest and size."""
    if harness == "codex":
        return entries(package)
    st = os.stat(package)
    return [{"path": "claude", "type": "file", "mode": "%04o" % stat.S_IMODE(st.st_mode),
             "sha256": admission.sha256(package), "size": st.st_size}]


def ditto(src, dst):
    subprocess.run(["ditto", str(src), str(dst)], check=True, capture_output=True)


def retain(harness, package, version, sha, home, copier=ditto):
    """Copy package to <home>/.buddy/harness/<harness>/<version>-<sha8>/ and
    return (target, "copied"|"reused"). See step 2 in the module comment."""
    want = snapshot(harness, package)
    base = pathlib.Path(home) / ".buddy" / "harness" / harness
    target = base / (version + "-" + sha[:8])
    if os.path.lexists(target):
        if target.is_symlink() or not target.is_dir() or snapshot(harness, target / "claude" if harness == "claude" else target) != want:
            raise Refusal("retained package " + str(target) + " exists and differs from the source; it is never overwritten")
        return target, "reused"
    base.mkdir(parents=True, exist_ok=True)
    stage = pathlib.Path(tempfile.mkdtemp(prefix=".stage-", dir=base))
    try:
        os.chmod(stage, 0o755)
        copier(package, stage / "claude" if harness == "claude" else stage)
        got = snapshot(harness, stage / "claude" if harness == "claude" else stage)
        if got != want:
            raise Refusal("the staged copy does not match the source entry for entry; nothing retained")
        if snapshot(harness, package) != want:
            raise Refusal("the source changed while it was copied; nothing retained")
        os.rename(stage, target)
    except BaseException:
        shutil.rmtree(stage, ignore_errors=True)
        raise
    if snapshot(harness, target / "claude" if harness == "claude" else target) != want:
        raise Refusal("the retained package changed after the rename: " + str(target))
    return target, "copied"


# ------------------------------------------------------------------ the runs

DIRECT = {"codex-shell-lifecycle-edge"}  # runs codex itself, no adapter
EXTRA = {"codex-shell-lifecycle-edge": ["--app-server", "--lifecycle", "--edge-cases", "--shell-patch"]}


def runner_argv(harness, suite, runner, artifact, peer):
    argv = [sys.executable, "-B", str(SCRIPTS / runner), "--" + harness, str(artifact)] + EXTRA.get(suite, [])
    if suite == "recovery":
        if not peer:
            raise Refusal("the recovery suite runs a Claude worker too; pass --claude-peer <a recorded Claude build>")
        argv += ["--claude", str(peer)]
    return argv


def run_suite(harness, suite, runner, artifact, peer, logs):
    """Run one runner; return (exit code, receipt path or None)."""
    log = logs / (suite + ".runner.log")
    with open(log, "w") as out:
        p = subprocess.run(runner_argv(harness, suite, runner, artifact, peer), cwd=REPO, stdout=out,
                           stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL, timeout=4 * 3600)
    receipt = None
    for line in log.read_text(errors="replace").splitlines():
        if line.startswith("RECEIPT="):
            receipt = pathlib.Path(line.removeprefix("RECEIPT="))
        elif line.startswith("ROOT=") and receipt is None:
            receipt = pathlib.Path(line.removeprefix("ROOT=")) / "result.json"
    return p.returncode, receipt


def run_checker(checker, receipt, candidate, logs, suite):
    p = subprocess.run([sys.executable, "-I", "-B", str(SCRIPTS / checker), str(receipt), "--candidate", candidate],
                       cwd=REPO, capture_output=True, text=True, stdin=subprocess.DEVNULL, timeout=3600)
    return p.returncode, p.stdout, p.stderr


def count_checks(value):
    """(passed, total) over every boolean under a "checks" key, at any depth:
    the runners and checkers nest per-case checks differently."""
    passed = total = 0

    def flat(v):
        nonlocal passed, total
        if isinstance(v, dict):
            for x in v.values():
                flat(x)
        elif isinstance(v, bool):
            total += 1
            passed += v

    def walk(v):
        if isinstance(v, dict):
            for k, x in v.items():
                flat(x) if k == "checks" else walk(x)
        elif isinstance(v, list):
            for x in v:
                walk(x)
    walk(value)
    return passed, total


def first_key(value, key):
    if isinstance(value, dict):
        if isinstance(value.get(key), str):
            return value[key]
        for x in value.values():
            found = first_key(x, key)
            if found:
                return found
    elif isinstance(value, list):
        for x in value:
            found = first_key(x, key)
            if found:
                return found
    return None


def judge_receipt(harness, suite, receipt, binding):
    """Why this receipt is not evidence for the binding, or None. The bytes the
    run bound must be the retained copy's, by the adapter's own word."""
    r = json.loads(receipt.read_text())
    root = receipt.parent
    bindings, shas = admission.scan(root)
    mine = {b for b in bindings if b[0] == harness}
    want = (harness, binding["version"], binding["sha256"])
    if suite in DIRECT:
        if r.get("codex_sha256") != binding["sha256"] or mine - {want}:
            return "the direct run names other bytes than the candidate"
    elif mine != {want} or binding["sha256"] not in shas:
        return "the run's adapter did not run bound to this candidate (bindings %s)" % sorted(map(str, mine))
    if shas - {b[2] for b in bindings}:
        return "a control receipt names a candidate no binding explains"
    for b in r.get("qualification_candidates") or []:
        if b.get("harness") == harness and (b.get("sha256"), b.get("artifact")) != (binding["sha256"], binding["artifact"]):
            return "the runner bound another artifact: " + str(b.get("artifact"))
    return None


# ------------------------------------------------------------------ scrubbing

def scrub(value, home):
    """$HOME as `~` in every string, so a committed file names no account path."""
    home = str(home).rstrip("/")
    if isinstance(value, str):
        return value.replace(home + "/", "~/") if home else value
    if isinstance(value, list):
        return [scrub(v, home) for v in value]
    if isinstance(value, dict):
        return {k: scrub(v, home) for k, v in value.items()}
    return value


def dump(value):
    return (json.dumps(value, indent=1) + "\n").encode()


# ---------------------------------------------------------------- the release

def default_git():
    def git(*args):
        return subprocess.run(["git", "--no-optional-locks", "-C", str(REPO), *args], check=True,
                              capture_output=True).stdout
    return {"head": lambda: git("rev-parse", "HEAD").decode().strip(),
            "clean": lambda: git("status", "--porcelain").decode().strip() == "",
            "blob_sha256": lambda head, name: hashlib.sha256(git("show", head + ":scripts/" + name)).hexdigest()}


def qualify(harness, executable, home, out_root, peer=None, today=None, git=None, suite_runner=run_suite,
            checker_runner=run_checker, copier=ditto, probe=None, logs=None, admission_go=None):
    """The whole release; returns the summary dict. Writes the record only when
    start is granted and the tree stayed clean at one HEAD."""
    git = git or default_git()
    today = today or datetime.date.today().isoformat()
    suites = code_suites(admission_go)[harness]
    contracts, builtins, gate = code_constants(admission_go)
    artifact, package, exe_path = source_of(harness, executable)
    sha = admission.sha256(artifact)
    text = (probe or admission.version_text)(harness, artifact)
    version = admission.parse_version(harness, text)
    tag = version + "-" + sha[:8]
    record_dir = pathlib.Path(out_root) / "internal/harness/admissions" / harness / tag
    if record_dir.exists():
        raise Refusal("a record for " + tag + " already exists; it is never overwritten")
    if peer:
        peer_sha = admission.sha256(pathlib.Path(peer).resolve(strict=True))
        if not any(peer_sha in shas for shas in admission.recorded("claude", pathlib.Path(out_root) / "internal/harness/admissions").values()):
            raise Refusal("--claude-peer must be a recorded Claude build's bytes")
    head, clean = git["head"](), git["clean"]()
    target, how = retain(harness, package, version, sha, home, copier)
    retained = target / exe_path
    if admission.sha256(retained) != sha:
        raise Refusal("the retained executable is not the bytes that were hashed")
    # The candidate as the adapter will bind it: the retained, resolved file.
    binding = {"harness": harness, "version": version, "version_output": text, "artifact": str(retained.resolve()), "sha256": sha}
    candidate = harness + ":" + version + ":" + sha
    logs = pathlib.Path(logs or tempfile.mkdtemp(prefix="buddy-qualify-release-", dir="/private/tmp"))
    evidence_rel = "docs/admissions/%s-%s-%s" % (today, harness, tag)
    evidence_dir = pathlib.Path(out_root) / evidence_rel
    results, outputs = [], {}
    for tier in TIER_ORDER:
        for suite, runner, checker in suites[tier]:
            row = {"tier": tier, "suite": suite, "runner": runner, "checker": checker}
            results.append(row)
            try:
                rc, receipt = suite_runner(harness, suite, runner, retained, peer, logs)
            except Refusal as e:
                row["refused"] = str(e)
                continue
            row.update(runner_exit=rc, receipt=str(receipt) if receipt else None)
            if not receipt or not receipt.is_file():
                row["refused"] = "no receipt"
                continue
            r = json.loads(receipt.read_text())
            row["receipt_sha256"] = admission.sha256(receipt)
            row["checks_passed"], row["checks_total"] = count_checks(r)
            row["buddy_sha256"], row["adapter_sha256"] = first_key(r, "buddy_sha256"), first_key(r, "adapter_sha256")
            why = judge_receipt(harness, suite, receipt, binding)
            if why:
                row["refused"] = why
            crc, cout, cerr = checker_runner(checker, receipt, candidate, logs, suite)
            try:
                verdict = json.loads(cout)
            except ValueError:
                verdict = {"passed": False, "unparsed": True}
            log_bytes = dump(scrub({"checker": checker, "exit": crc, "verdict": verdict, "stderr": cerr[-4000:]}, home))
            outputs[suite + ".checker.json"] = log_bytes
            row["checker_log_sha256"] = hashlib.sha256(log_bytes).hexdigest()
            row["checker_exit"] = crc
            row["checker_checks_passed"], row["checker_checks_total"] = count_checks(verdict)
            row["checker_passed"] = (crc == 0 and verdict.get("passed") is True and row["checker_checks_total"] > 0 and
                                     row["checker_checks_passed"] == row["checker_checks_total"])
            row["green"] = (not row.get("refused") and rc == 0 and r.get("passed", r.get("success", True)) is not False and
                            row["checks_total"] > 0 and row["checks_passed"] == row["checks_total"] and row["checker_passed"] and
                            all(isinstance(row[k], str) and len(row[k]) == 64 for k in ("buddy_sha256", "adapter_sha256")))
    granted = [t for t in TIER_ORDER if all(x.get("green") for x in results if x["tier"] == t)]
    if "start" not in granted:
        granted = []
    stable = clean and git["clean"]() and git["head"]() == head
    summary = {"schema": "buddy.release-qualification.v1", "date": today, "harness": harness, "version": version,
               "version_output": text, "candidate": binding, "retained": {"path": str(target), "how": how},
               "source": {"executable": str(artifact), "package": str(package)},
               "source_head": head, "tree_clean_throughout": stable, "granted": granted,
               "scope": "operator-authorized loopback runs with deterministic providers; fixture evidence, not production proof",
               "suites": results}
    summary = scrub(summary, home)
    if not granted or not stable:
        summary["record"] = ("no record: the tree was dirty or HEAD moved, so runner digests are not of committed bytes"
                             if granted else "no record: start was not earned")
        return summary
    evidence_dir.mkdir(parents=True)
    for name, data in outputs.items():
        (evidence_dir / name).write_bytes(data)
    summary_rel = evidence_rel + "/summary.json"
    record = {"schema": "buddy.admission.v1", "harness": harness, "version": version, "version_output": text,
              "os": {"Darwin": "darwin", "Linux": "linux"}.get(platform.system(), platform.system().lower()),
              "arch": {"x86_64": "amd64", "aarch64": "arm64"}.get(platform.machine(), platform.machine()),
              "adapter_contract": contracts[harness], "tiers": granted,
              "executable": {"path": exe_path, "sha256": sha, "size": os.stat(retained).st_size}}
    if harness == "codex":
        record["package"] = entries(target)
    else:
        record["claude"] = {"mod_profile": "builtin-only-" + version, "builtins": builtins}
    record["gate"] = gate[harness]
    record["suites"] = [{"tier": x["tier"], "suite": x["suite"], "runner": x["runner"],
                         "runner_sha256": git["blob_sha256"](head, x["runner"]), "receipt_sha256": x["receipt_sha256"],
                         "checks_passed": x["checks_passed"], "checks_total": x["checks_total"], "checker": x["checker"],
                         "checker_sha256": git["blob_sha256"](head, x["checker"]), "checker_log_sha256": x["checker_log_sha256"],
                         "checker_passed": True, "checker_checks_passed": x["checker_checks_passed"],
                         "checker_checks_total": x["checker_checks_total"], "buddy_sha256": x["buddy_sha256"],
                         "adapter_sha256": x["adapter_sha256"], "source_head": head, "summary": summary_rel}
                        for x in results if x["tier"] in granted]
    record["evidence"] = scrub({"qualified_on": today,
                                "source": "a retained copy at " + str(target) + " of " + str(package),
                                "note": "scripts/qualify-release.py at " + head + "; every suite run from the retained copy as a "
                                        "D-080 candidate and passed by its checker with --candidate. Tiers not listed were not earned."}, home)
    summary["record"] = str(record_dir.relative_to(out_root) / "admission.json")
    (evidence_dir / "summary.json").write_bytes(dump(summary))
    record_dir.mkdir(parents=True)
    (record_dir / "admission.json").write_bytes(dump(record))
    return summary


# ----------------------------------------------------------------- self-test

def self_test():
    results = {}

    def case(name, ok):
        results[name] = bool(ok)

    go = (REPO / "internal/harness/admission.go").read_text()
    suites = code_suites(go)
    case("code_suites_parsed", suites["claude"]["start"][0] == ("claude", "qualify-claude.py", "check-claude-gate.py") and
         len(suites["codex"]["resume"]) == 3)
    contracts, builtins, gate = code_constants(go)
    case("code_constants_parsed", contracts["claude"] and builtins and gate["codex"]["command"].startswith("'<adapter>'"))
    with tempfile.TemporaryDirectory(prefix="qualify-release-self-") as tmp:
        tmp = pathlib.Path(tmp)
        home = tmp / "home"
        home.mkdir()
        src = home / "versions" / "9.9.9"
        src.parent.mkdir()
        src.write_bytes(b"fake claude 9.9.9\n")
        src.chmod(0o755)
        sha = admission.sha256(src)
        probe = lambda h, p: "9.9.9 (Claude Code)"
        git = {"head": lambda: "e" * 40, "clean": lambda: True, "blob_sha256": lambda head, name: "f" * 64}

        def fake_suites(bad_checker=(), bad_runner=(), wrong_sha=(), unbound=(), stray=()):
            def runner(harness, suite, runner_name, artifact, peer, logs):
                root = pathlib.Path(tempfile.mkdtemp(dir=tmp))
                s = "0" * 64 if suite in wrong_sha else admission.sha256(artifact)
                art = str(pathlib.Path(artifact).resolve())
                (root / "worker.json").write_text(json.dumps({"qualification_candidate": {
                    "harness": harness, "version": "9.9.9", "sha256": s, "artifact": art}}))
                events = [{"operation": "start"}] if suite in unbound else [{"operation": "start", "qualification_candidate": s}]
                if suite in stray:
                    events.append({"operation": "resume", "qualification_candidate": "9" * 64})
                (root / "adapter-events.json").write_text(json.dumps(events))
                (root / "result.json").write_text(json.dumps({
                    "root": str(root), "passed": True, "checks": {"a": suite not in bad_runner, "b": True},
                    "buddy_sha256": "b" * 64, "adapter_sha256": "c" * 64, "where": str(home / "x")}))
                return 0, root / "result.json"

            def checker(checker_name, receipt, candidate, logs, suite):
                seen.append(candidate)
                ok = suite not in bad_checker
                return (0 if ok else 1), json.dumps({"passed": ok, "checks": {"x": ok}, "root": str(home / "y")}), ""
            return runner, checker

        seen = []
        out = tmp / "repo1"
        runner, checker = fake_suites()
        s = qualify("claude", src, home, out, today="2026-10-13", git=git, suite_runner=runner, checker_runner=checker,
                    copier=lambda a, b: shutil.copy2(a, b), probe=probe, logs=tmp, admission_go=go)
        rec_path = out / ("internal/harness/admissions/claude/9.9.9-" + sha[:8] + "/admission.json")
        rec = json.loads(rec_path.read_text()) if rec_path.is_file() else {}
        case("all_green_grants_every_tier", rec.get("tiers") == list(TIER_ORDER) and len(rec.get("suites", [])) == 6)
        case("record_binds_retained_bytes", rec.get("executable", {}).get("sha256") == sha and
             (home / (".buddy/harness/claude/9.9.9-" + sha[:8] + "/claude")).read_bytes() == src.read_bytes())
        case("checkers_given_the_candidate", seen and set(seen) == {"claude:9.9.9:" + sha})
        committed = b"".join(p.read_bytes() for p in out.rglob("*") if p.is_file())
        case("home_scrubbed_from_committed_files", str(home).encode() not in committed and b"~/" in committed)
        logs = list((out / "docs/admissions").rglob("*.checker.json"))
        case("checker_log_digest_is_of_committed_bytes", logs and all(
            hashlib.sha256(p.read_bytes()).hexdigest() in {x["checker_log_sha256"] for x in rec["suites"]} for p in logs))
        case("summary_names_every_receipt", all(x["receipt_sha256"] in (out / rec["suites"][0]["summary"]).read_text() for x in rec["suites"]))

        def attempt(name, **kw):
            o = tmp / name
            r, c = fake_suites(**kw)
            got = qualify("claude", src, home, o, today="2026-10-13", git=git, suite_runner=r,
                          checker_runner=c, copier=lambda a, b: shutil.copy2(a, b), probe=probe, logs=tmp, admission_go=go)
            p = o / ("internal/harness/admissions/claude/9.9.9-" + sha[:8] + "/admission.json")
            return got, (json.loads(p.read_text()) if p.is_file() else None)

        _, r2 = attempt("resume-checker-fails", bad_checker=("claude-interrupted",))
        case("failing_checker_withholds_its_tier_only", r2 and r2["tiers"] == ["start", "compaction_observation"] and
             not any(x["tier"] == "resume" for x in r2["suites"]))
        _, r3 = attempt("start-fails", bad_runner=("claude-mods",))
        case("no_start_no_record", r3 is None)
        _, r4 = attempt("other-bytes", wrong_sha=("claude-compaction",))
        case("run_bound_to_other_bytes_withholds_tier", r4 and "compaction_observation" not in r4["tiers"])
        _, r5 = attempt("unbound", unbound=("claude-resume",))
        _, r7 = attempt("stray", stray=("claude-interrupted-compaction",))
        case("unexplained_candidate_withholds_tier", r7 and "compaction_observation" not in r7["tiers"])
        moving = home / "moving"
        moving.write_bytes(b"moving bytes\n")

        def copy_then_touch(a, b):
            shutil.copy2(a, b)
            pathlib.Path(a).write_bytes(b"moved under the copy\n")
        try:
            retain("claude", moving, "9.9.7", admission.sha256(moving), home, copier=copy_then_touch)
            case("source_changed_during_copy_refused", False)
        except Refusal:
            case("source_changed_during_copy_refused", not list((home / ".buddy/harness/claude").glob("9.9.7-*")))
        case("adapter_not_bound_withholds_tier", r5 and "resume" not in r5["tiers"])
        case("existing_identical_package_reused", retain("claude", src, "9.9.9", sha, home, copier=None)[1] == "reused")
        other = home / "other"
        other.write_bytes(b"different bytes\n")
        try:
            retain("claude", other, "9.9.9", sha, home, copier=None)
            case("differing_package_never_overwritten", False)
        except Refusal:
            case("differing_package_never_overwritten", (home / (".buddy/harness/claude/9.9.9-" + sha[:8] + "/claude")).read_bytes() == src.read_bytes())
        try:
            retain("claude", src, "9.9.8", sha, home, copier=lambda a, b: pathlib.Path(b).write_bytes(b"torn"))
            case("torn_copy_refused_and_unstaged", False)
        except Refusal:
            base = home / ".buddy/harness/claude"
            case("torn_copy_refused_and_unstaged", not (base / ("9.9.8-" + sha[:8])).exists() and not list(base.glob(".stage-*")))
        try:
            qualify("claude", src, home, out, today="2026-10-13", git=git, suite_runner=runner, checker_runner=checker,
                    copier=None, probe=probe, logs=tmp, admission_go=go)
            case("existing_record_never_overwritten", False)
        except Refusal:
            case("existing_record_never_overwritten", True)
        dirty = dict(git, clean=lambda: False)
        r6, c6 = fake_suites()
        s6 = qualify("claude", src, home, tmp / "dirty", today="2026-10-13", git=dirty, suite_runner=r6, checker_runner=c6,
                     copier=None, probe=probe, logs=tmp, admission_go=go)
        case("dirty_tree_writes_no_record", not (tmp / "dirty").exists() and s6["granted"] and "dirty" in s6["record"])
        tree = home / "rel" / "bin"
        tree.mkdir(parents=True)
        (tree / "codex").write_bytes(b"fake codex\n")
        (tree / "codex-code-mode-host").write_bytes(b"sidecar\n")
        (home / "rel" / "codex").symlink_to("bin/codex")
        csha = admission.sha256(tree / "codex")
        target, _ = retain("codex", home / "rel", "0.0.1", csha, home, copier=lambda a, b: shutil.copytree(a, b, symlinks=True, dirs_exist_ok=True))
        case("codex_whole_tree_retained", entries(target) == entries(home / "rel") and len(entries(target)) == 4)
        try:
            source_of("codex", home / "rel" / "bin" / "codex-code-mode-host")
            case("codex_executable_must_be_bin_codex", False)
        except Refusal:
            case("codex_executable_must_be_bin_codex", True)
        case("codex_link_resolves_to_bin_codex", source_of("codex", home / "rel" / "codex")[2] == "bin/codex")
        # The direct gate suite runs no adapter: its own codex_sha256 is the binding.
        cb = {"harness": "codex", "version": "0.0.1", "sha256": csha, "artifact": str(target / "bin/codex")}
        direct = tmp / "direct"
        direct.mkdir()
        (direct / "result.json").write_text(json.dumps({"codex_sha256": csha}))
        ok = judge_receipt("codex", "codex-shell-lifecycle-edge", direct / "result.json", cb) is None
        (direct / "result.json").write_text(json.dumps({"codex_sha256": "0" * 64}))
        case("direct_suite_judged_by_its_own_digest", ok and
             judge_receipt("codex", "codex-shell-lifecycle-edge", direct / "result.json", cb) is not None)
    return {"passed": all(results.values()), "checks": results}


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--harness", choices=("claude", "codex"))
    parser.add_argument("--executable")
    parser.add_argument("--claude-peer", help="a RECORDED Claude build, for the Codex recovery suite's Claude worker")
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()
    if args.self_test:
        out = self_test()
        print(json.dumps(out, indent=2))
        return 0 if out["passed"] else 1
    if not args.harness or not args.executable:
        parser.error("--harness and --executable are required")
    try:
        summary = qualify(args.harness, args.executable, pathlib.Path.home(), REPO, peer=args.claude_peer)
    except Refusal as e:
        print("qualify-release: refused: " + str(e), file=sys.stderr)
        return 2
    print(json.dumps(summary, indent=1))
    return 0 if summary["granted"] and summary.get("record", "").endswith("admission.json") else 1


if __name__ == "__main__":
    sys.exit(main())
