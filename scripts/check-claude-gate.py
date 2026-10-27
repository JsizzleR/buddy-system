#!/usr/bin/env python3
"""Independently check a qualify-claude.py receipt against the evidence it left.

START-TIER CHECKER (consensus "Harness version tracking", Codex review P1). An
admission record for a new Claude version will rest on this suite, and until
now nothing but the runner's own `passed: true` said it passed. This reads the
run's files (adapter receipts, the provider log, the worktree, the ownership
record, the managed-policy case) and decides every verdict again from them. It
never launches a native process, opens no live ledger (only the two private
copies a run left, read-only and immutable, for claim renewal) and trusts no
runner check: the runner's `checks` are only required to be complete and all
true, so a receipt that says passed while one of its own cases failed is
refused too. The same holds for the renewal verdicts the runner keeps in
`renewal.checks` (F-26, D-078): each is decided again from the ledger images,
the journal and the native trace; see "claim renewal" below.

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
# The recorded versions (D-079). A run is judged by admission.need: its own
# bound candidate or a record's exact bytes, never a version range or a list
# typed here (this one named two versions D-079 retired).
CLAUDE_VERSIONS = admission.recorded_versions("claude")
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


def evidence_binding(e, r, required, optional=()):
    """F-37: when the runner hashes its evidence, every file read must be listed.

    Returns "hashed" or "unhashed". A receipt that carries evidence_sha256 with a
    missing or wrong entry raises (and the check fails). `optional` names files
    the runner does not hash yet (the renewal images): left out, they are
    reported by the caller; listed, they must match like any other."""
    hashes = r.get("evidence_sha256")
    if hashes is None:
        if required:
            raise ValueError("receipt carries no evidence_sha256")
        return "unhashed"
    if not isinstance(hashes, dict):
        raise ValueError("evidence_sha256 is not a map")
    for name in sorted(e.read_paths):
        if name == "result.json" or (name in optional and name not in hashes):
            continue
        if hashes.get(name) != e.sha(e.root / name):
            raise ValueError("evidence not bound by hash: " + name)
    return "hashed"


# ------------------------------------------------------- claim renewal (D-078)
#
# F-26 made a worker the adapter launched renew its claims from COMPLETED native
# writes. The runners record their renewal verdicts beside `checks`; until this
# section, those verdicts rested on the runner's word. They are re-derived here
# from three registers the run left, none of which is the runner's summary:
#
# - the two private ledger IMAGES the runner copied (renewal-*.db), read with
#   SQLite mode=ro&immutable=1, never the fixture's live ledger and never a real
#   one. Which is which is decided by the ledger, not by file name or mtime:
#   the AFTER image holds the worker's idle observation and the BEFORE image
#   none (the before copy is taken before the wake is even queued);
# - the adapter's journal (worker.json.events), where each renewal batch is one
#   `renewal` row and its order against the wake, the approval and the idle is
#   the order the controller wrote them;
# - the native's own record of the completion (Claude: the trace's tool_result
#   and its structured witness; Codex: the tool output the model was given).
#
# The images are NOT in the runners' evidence_sha256 (their names are random
# and the runners hash a fixed list), so they are bound by content instead:
# both name this run's worktree, its captured session and incarnation, and the
# receipt's before/after maps must equal them. The output lists them under
# `unhashed`; hashing them is the runner change that would close it.

RENEWAL_IMAGE_PREFIX, RENEWAL_IMAGE_SUFFIX = "renewal-", ".db"


def ledger_image(e, name):
    """One runner-made ledger copy, read without writing: open claims with their
    owner, times and scopes; sessions; idle observations."""
    import contextlib
    import sqlite3
    import urllib.parse
    path = e.path(name)
    with contextlib.closing(sqlite3.connect("file:" + urllib.parse.quote(str(path)) + "?mode=ro&immutable=1",
                                            uri=True)) as conn:
        claims = {}
        for cid, slug, sid, inc, created, renewed, state in conn.execute(
                "SELECT claim_id, slug, session_id, incarnation, created, renewed, state FROM claims"):
            if state != "open":
                continue
            if slug in claims:
                raise ValueError("two open claims named " + slug)
            scopes = sorted(s for (s,) in conn.execute("SELECT scope FROM claim_scopes WHERE claim_id = ?", (cid,)))
            claims[slug] = {"session": sid, "incarnation": inc, "created": created, "renewed": renewed, "scopes": scopes}
        sessions = {sid: (inc, wt) for sid, inc, wt in conn.execute("SELECT session_id, incarnation, worktree FROM sessions")}
        idle = {(sid, inc): since for sid, inc, since in conn.execute("SELECT session_id, incarnation, since FROM session_idle")}
    return {"claims": claims, "sessions": sessions, "idle": idle}


def renewal_images(e, identity):
    """(before, after): exactly two images in the run's root, told apart by the
    worker's idle row, both naming this run's worktree and captured identity.
    "Exactly one of each" is the whole count: a third image of either kind, or
    a missing one, is refused there."""
    names = sorted(p.name for p in e.root.iterdir()
                   if p.name.startswith(RENEWAL_IMAGE_PREFIX) and p.name.endswith(RENEWAL_IMAGE_SUFFIX))
    images = [ledger_image(e, n) for n in names]
    key = (identity["session_id"], identity["incarnation"])
    before = [v for v in images if key not in v["idle"]]
    after = [v for v in images if key in v["idle"]]
    if len(before) != 1 or len(after) != 1:
        raise ValueError("the images are not one before the worker's idle and one after it")
    for v in images:
        if v["sessions"].get(identity["session_id"]) != (identity["incarnation"], str(e.root / "work")):
            raise ValueError("a renewal image does not name this run's worker in this run's worktree")
    return before[0], after[0]


def renewed_map(image):
    return {slug: v["renewed"] for slug, v in image["claims"].items()}


def renewal_claims_bound(images, identity, expected):
    """expected: {slug: (scope, owned_by_worker)}. Both images hold exactly those
    open claims, with those scopes, the worker's under its captured incarnation
    and the holder's under some other session, and each claim is the same row
    (same created time) in both."""
    for image in images:
        claims = image["claims"]
        if set(claims) != set(expected):
            return False
        for slug, (scope, mine) in expected.items():
            v = claims[slug]
            owned = (v["session"], v["incarnation"]) == (identity["session_id"], identity["incarnation"])
            if v["scopes"] != [scope] or owned != mine or (not mine and v["session"] == identity["session_id"]):
                return False
    return all(images[0]["claims"][s]["created"] == images[1]["claims"][s]["created"] for s in expected)


def renewal_moved(before, after, identity, moved):
    """Each claim in `moved` renewed strictly later in the after image, and no
    later than the worker's idle there. Which claim covers a written path is
    settled by renewal_claims_bound: it pins every claim to its ONE expected
    scope, and no other claim's scope contains that path (Invariant 14), so a
    covering computation here could only agree with it (a mutation run found
    one unkillable for that reason, and it was cut)."""
    since = after["idle"][(identity["session_id"], identity["incarnation"])]
    return all(before["claims"][s]["renewed"] < after["claims"][s]["renewed"] <= since for s in moved)


def renewal_unmoved(before, after, slugs):
    return all(before["claims"][s]["renewed"] == after["claims"][s]["renewed"] for s in slugs)


def renewal_rows(rows):
    """(index, renewal) for every renewal row in a journal."""
    return [(i, v["renewal"]) for i, v in enumerate(rows) if "renewal" in v]


def renewal_applied(e, journal, identity, generation, placed, renewed, together=()):
    """Every renewal row is this launch's, under the captured identity, applied
    with complete coverage and nothing unresolved; across the rows the placed
    paths are exactly `placed`, each once, each native spelling the run's own
    worktree path for its placed path; every group in `together` (both ends of
    one move) is in ONE batch, since a completion offers all its paths or none;
    and the renewed counts sum to `renewed`. How completions batch is the
    controller's timing, so the number of rows is not fixed."""
    rows = renewal_rows(journal)
    if any(journal[i].get("identity") != identity for i, _ in rows):
        return False
    for _, v in rows:
        if (v.get("launch_generation") != generation or v["coverage"] != {"state": "observed"} or
                v["result"] != {"state": "applied"} or v.get("unresolved_paths") or
                v["paths"] != [str(e.root / "work" / p) for p in v["placed_paths"]]):
            return False
    flat = [p for _, v in rows for p in v["placed_paths"]]
    return (sorted(flat) == sorted(placed) and
            all(any(set(group) <= set(v["placed_paths"]) for _, v in rows) for group in together) and
            sum(v["renewed_claims"] for _, v in rows) == renewed)


def renewal_not_applied(journal, identity, generation):
    """The conservative outcome (an unqualified omission): every row reports
    coverage unknown, attempted nothing and renewed nothing."""
    rows = renewal_rows(journal)
    return (bool(rows) and all(journal[i].get("identity") == identity for i, _ in rows) and
            all(v.get("launch_generation") == generation and v["coverage"]["state"] == "unknown" and
                v["result"]["state"] == "not_attempted" and v["renewed_claims"] == 0 for _, v in rows))


def renewal_receipt_matches(r, before, after, journal):
    """The receipt's renewal record is the images' and the journal's, not its own story."""
    rec = r["renewal"]
    return (rec["before"] == renewed_map(before) and rec["after"] == renewed_map(after) and
            rec["journal"] == [v for _, v in renewal_rows(journal)])


def image_names(e):
    """Every renewal image this validation read: the files evidence_binding may
    find unlisted (and must still match when listed)."""
    return sorted(n for n in e.read_paths if n.startswith(RENEWAL_IMAGE_PREFIX))


def unhashed_images(e, r):
    hashes = r.get("evidence_sha256") or {}
    return [n for n in image_names(e) if n not in hashes]


def ledger_fixture(path, sessions, claims, idle):
    """A minimal ledger image for the synthetic runs: the four tables the
    renewal checks read, in the shape internal/store gives them.
    claims: [(claim_id, slug, session, incarnation, created, renewed, scope[, state])]."""
    import contextlib
    import sqlite3
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    with contextlib.closing(sqlite3.connect(str(path))) as conn:
        conn.executescript(
            "CREATE TABLE sessions (session_id TEXT, incarnation TEXT, label TEXT, worktree TEXT, pid INTEGER, started INTEGER,"
            " last_seen INTEGER, ended INTEGER, terminal TEXT);"
            "CREATE TABLE claims (claim_id TEXT, session_id TEXT, incarnation TEXT, slug TEXT, descr TEXT, created INTEGER,"
            " renewed INTEGER, state TEXT, shared INTEGER, outcome TEXT, outcome_note TEXT);"
            "CREATE TABLE claim_scopes (claim_id TEXT, scope TEXT, folded TEXT);"
            "CREATE TABLE session_idle (session_id TEXT, incarnation TEXT, since INTEGER);")
        conn.executemany("INSERT INTO sessions VALUES (?, ?, 'x', ?, 0, 1, 1, NULL, '')", sessions)
        for cid, slug, sid, inc, created, renewed, scope, *state in claims:
            conn.execute("INSERT INTO claims VALUES (?, ?, ?, ?, 'q', ?, ?, ?, 0, NULL, NULL)",
                         (cid, sid, inc, slug, created, renewed, (state or ["open"])[0]))
            conn.execute("INSERT INTO claim_scopes VALUES (?, ?, ?)", (cid, scope, scope))
        conn.executemany("INSERT INTO session_idle VALUES (?, ?, ?)", idle)
        conn.commit()
    return path


def ledger_update(path, sql, args=()):
    """Damage a SYNTHETIC image (the self-tests' own files, never a real ledger)."""
    import contextlib
    import sqlite3
    with contextlib.closing(sqlite3.connect(str(path))) as conn:
        conn.execute(sql, args)
        conn.commit()


def image_damage(root, r, image, slug, renewed):
    """Move one claim's renewed time in a synthetic image, and return the receipt
    a runner would have written over it, so the semantic guard is what refuses,
    not the receipt's disagreement with the image."""
    root = Path(root)
    ledger_update(root / image, "UPDATE claims SET renewed = ? WHERE slug = ?", (renewed, slug))
    side = "after" if "after" in image else "before"
    return mutate(r, ("renewal", side, slug), renewed)


def journal_damage(root, r, name, fn):
    """Edit a journal's rows and return the receipt with its copy of the
    renewal rows kept equal to them."""
    root = Path(root)
    rows = [json.loads(v) for v in (root / name).read_text().splitlines()]
    fn(rows)
    write(root, name, jsonl(rows))
    return mutate(r, ("renewal", "journal"), [v for _, v in renewal_rows(rows)])


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
    admission.add_argument(parser)
    args = parser.parse_args()
    admission.expect(args.candidate)
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
    "adapter_stdout_fully_parsed", "edit_waiting_approval", "operator_allow_edit_submitted", "positive_edit_allowed"}
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
    e.check("contract", lambda: r["schema"] == "buddy.claude-qualification.v1" and
            admission.need("claude", version, e.root, r["native_sha256"]) and
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
    # The allowed Write creates the file and the allowed Edit (toolu_buddy_3,
    # the last tool use) changes it, so the final text is the Edit's.
    e.check("allowed_control", lambda: e.text("work/allowed.txt") == "allowed-edited\n" and
            not outputs()["toolu_buddy_1"].get("is_error") and
            str(e.root / "work/allowed.txt") in str(outputs()["toolu_buddy_1"]["content"]) and approval_file(native("approval")[0]) == e.root / "work/allowed.txt" and
            reply("operator-allow")["approval_delivery"]["state"] == "submitted")
    e.check("operator_denial", lambda: e.absent("work/operator-denied.txt") and
            outputs()["toolu_buddy_2"].get("is_error") is True and
            "Denied by the operator control interface" in str(outputs()["toolu_buddy_2"]["content"]) and
            approval_file(native("approval")[1]) == e.root / "work/operator-denied.txt" and len(native("approval")) == 3 and
            reply("operator-deny")["approval_delivery"]["state"] == "submitted")
    e.check("allowed_edit", lambda: "toolu_buddy_3" in outputs() and not outputs()["toolu_buddy_3"].get("is_error") and
            approval_file(native("approval")[2]) == e.root / "work/allowed.txt" and
            native("approval")[2]["native_event"]["approval"]["item_id"] == "toolu_buddy_3" and
            reply("operator-allow-edit")["approval_delivery"]["state"] == "submitted" and
            ev().index(reply("operator-deny")) < ev().index(reply("operator-allow-edit")))
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
    e.check("first_turn", lambda: turn("wake-1") and len(native("approval")) == 3 and
            all(v["native_event"]["approval"]["turn_id"] == reply("wake-1")["wake_delivery"]["turn_id"] for v in native("approval")))
    e.check("second_turn", lambda: turn("wake-2") and
            reply("wake-2")["wake_delivery"]["turn_id"] != reply("wake-1")["wake_delivery"]["turn_id"] and
            len({v["native_event"]["result_ref"] for v in native("result")}) == 2 == len(native("result")))
    # Five requests for the first turn (0..4 tool results), one for the second;
    # a replayed duplicate wake or a managed case that reached the provider adds one.
    e.check("duplicate_not_replayed", lambda: reply("wake-duplicate")["wake_delivery"] == reply("wake-1")["wake_delivery"] and
            [len(v["tool_outputs"]) for v in e.json("provider-observations.json")] == [0, 1, 2, 3, 4, 4])
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
    unbound = claude_renewal(e, r, version, traced, identity, ev)
    binding = {}
    e.check("evidence_hashes", lambda: binding.setdefault("v", evidence_binding(e, r, False, optional=image_names(e))))
    return e.verdict(evidence=binding.get("v"), read=sorted(e.read_paths), traced=traced,
                     unhashed=unhashed_images(e, r), unbound=unbound)


# The worker's own claims (qualify-claude.py): allowed.txt is written and its
# claim must renew; operator-denied.txt is attempted and denied by the operator,
# and control.txt is never named, so theirs must not; the holder's guarded.txt
# claim stands behind the gate-denied write. {slug: (scope, owned by worker)}.
CLAUDE_RENEWAL_CLAIMS = {"worker-allowed": ("allowed.txt", True), "worker-denied": ("operator-denied.txt", True),
                         "worker-control": ("control.txt", True), "held": ("guarded.txt", False)}
# Where a successful Write's OMITTED is_error renews (D-078), written down here
# apart from claudeWriteOmissionVersions and the runner's copy of it; a Go
# test (TestClaudeWriteOmissionVersionsMatchThePythonMirrors) refuses any
# disagreement, and any spelling of this line but this one.
WRITE_RENEWAL_QUALIFIED = ("2.1.291", "2.1.299")
RENEWAL_CHECKS = {"claim_renewal_capability_declared", "renewal_written_claim_moved", "renewal_denied_write_claim_unmoved",
                  "renewal_control_claim_unmoved", "renewal_held_claim_unmoved", "renewal_journalled_after_write_before_idle",
                  "renewal_placed_only_the_written_path", "renewal_write_coverage_observed",
                  "renewal_edit_omission_reports_coverage_unknown", "renewal_same_identity"}
# A successful Edit OMITS is_error and its tool_use_result has no `type`
# (measured 2026-10-13 on 2.1.291 and 2.1.299: keys filePath, newString,
# oldString, originalFile, replaceAll, structuredPatch, userModified). Edit
# omission is not qualified (D-078), so the Edit renews nothing and its result
# is one coverage-unknown row, on every version.
EDIT_OMISSION_NOTE = "a file tool's result omitted is_error without a qualified success witness"
UNQUALIFIED_RENEWAL_CHECKS = {"claim_renewal_capability_declared", "unqualified_write_renews_nothing",
                              "unqualified_write_reports_coverage_unknown"}
# tool_result_meta (D-078 addendum, 2026-10-14), written down here apart from
# the Go reader (claude_renewal.go, readClaudeResultMeta and verdict) so the
# two are two readings of one rule. 2.1.299 puts an entry on EXECUTED results
# too ({id, permission_decision: {decision: "accept", source:
# "user_temporary"}}), so an entry's presence no longer means "did not run".
# Only an entry of a measured non-execution shape is UNEXECUTED; an accepted
# permission with no kind PROCEEDS to the is_error / witness rule (meta never
# renews by itself); every other shape is UNKNOWN: renew nothing and report
# coverage unknown. A non-execution kind beside a completion signal (explicit
# is_error false, or an omitted is_error beside the Write success witness) is
# contradictory evidence and UNKNOWN too; the witness arm was R's last silent
# one, read unexecuted until the orchestrator's 2026-10-14 decision, and no
# measured frame takes it. The field is @internal in the native schema, and
# relying on it is acceptable only because admission is per exact build.
META_ENTRY_KEYS = frozenset(("id", "non_execution_kind", "permission_decision"))
META_DECISION_KEYS = frozenset(("decision", "source", "reason_type"))
META_UNEXECUTED_KINDS = ("permission-rule", "user-rejected")
ABSENT = "<absent>"


def meta_verdict(meta, result_ids, tid, is_error=ABSENT, witness=False):
    """'proceed', 'unexecuted' or 'unknown' for result `tid` of a frame whose
    tool_result_meta is `meta` (None when absent or null, as the Go reader)
    and whose tool_result ids are `result_ids`. `is_error` is the result's
    value, ABSENT when the key is missing (the observer's own spelling).
    `witness` is True when the frame carries the Write success witness for
    `tid` (create/update naming the recorded path, one result block)."""
    if meta is None:
        return "proceed"
    # R' 0 and 1, for the whole frame: a list of objects with string ids,
    # each naming a tool_result of this frame.
    if not isinstance(meta, list) or not all(isinstance(m, dict) and isinstance(m.get("id"), str) for m in meta):
        return "unknown"
    if any(m["id"] not in result_ids for m in meta):
        return "unknown"
    mine = [m for m in meta if m["id"] == tid]
    if not mine:
        return "proceed"
    if len(mine) > 1:
        return "unknown"
    entry = mine[0]
    if set(entry) - META_ENTRY_KEYS:
        return "unknown"
    decided = "permission_decision" in entry
    if decided:
        pd = entry["permission_decision"]
        if (not isinstance(pd, dict) or set(pd) - META_DECISION_KEYS or "decision" not in pd or
                not all(isinstance(v, str) for v in pd.values())):
            return "unknown"
    if "non_execution_kind" in entry:
        kind = entry["non_execution_kind"]
        completed = is_error is False or (is_error == ABSENT and witness)
        if (kind in META_UNEXECUTED_KINDS and
                (not decided or entry["permission_decision"]["decision"] == "reject") and not completed):
            return "unexecuted"
        return "unknown"
    if decided and entry["permission_decision"]["decision"] == "accept":
        return "proceed"
    return "unknown"


def frame_result_ids(v):
    """The tool_result ids of one observer-projected user frame."""
    return {b.get("id") for b in v.get("tool_blocks") or [] if b.get("type") == "tool_result" and isinstance(b.get("id"), str) and b.get("id")}


def matched_write(e, path):
    """The tool_use id of the one main-thread Write of `path` in the native
    trace, if its single tool_result is the success D-078 renews on: not a
    replay, main thread, the only block in its frame, is_error absent or false,
    no tool_result_meta reading other than 'proceed' (meta_verdict), and the
    structured create/update witness naming the recorded path. The result must
    come after its use and before the turn's result frame."""
    rows = e.lines("native-observations.jsonl")
    uses = [(i, b["id"]) for i, v in enumerate(rows) if v.get("type") == "assistant" and v.get("parent_tool_use_id") is None
            for b in v.get("tool_blocks") or [] if b.get("type") == "tool_use" and b.get("name") == "Write" and b.get("path") == path]
    if len(uses) != 1:
        raise ValueError("want one main-thread Write of the allowed path in the trace")
    at, tid = uses[0]
    results = [(i, v) for i, v in enumerate(rows) if v.get("type") == "user"
               for b in v.get("tool_blocks") or [] if b.get("type") == "tool_result" and b.get("id") == tid]
    if len(results) != 1:
        raise ValueError("want one tool_result for " + str(tid))
    i, v = results[0]
    end = next(j for j, x in enumerate(rows) if j > at and x.get("type") == "result")
    witness = v.get("tool_use_result")
    witnessed = (len(v["tool_blocks"]) == 1 and isinstance(witness, dict) and witness.get("type") in ("create", "update") and
                 witness.get("filePath") == path)
    if not (at < i < end and v.get("isReplay") is not True and v.get("parent_tool_use_id") is None and
            len(v["tool_blocks"]) == 1 and (v["tool_blocks"][0]["is_error"] is False or v["tool_blocks"][0]["is_error"] == ABSENT) and
            meta_verdict(v.get("tool_result_meta"), frame_result_ids(v), tid, v["tool_blocks"][0]["is_error"], witnessed) == "proceed" and
            witnessed):
        raise ValueError("the allowed Write's result is not a renewable success")
    return tid


def edit_shape(e, path):
    """qualify-claude.edit_shape, re-derived from the native trace: the one
    main-thread Edit of `path`, its one result frame, and that frame's shape."""
    rows = e.lines("native-observations.jsonl")
    uses = [b["id"] for v in rows if v.get("type") == "assistant" and v.get("parent_tool_use_id") is None
            for b in v.get("tool_blocks") or [] if b.get("type") == "tool_use" and b.get("name") == "Edit" and b.get("path") == path]
    frames = [v for v in rows if v.get("type") == "user" and len(uses) == 1
              for b in v.get("tool_blocks") or [] if b.get("type") == "tool_result" and b.get("id") == uses[0]]
    if len(uses) != 1 or len(frames) != 1:
        return {"edit_uses": len(uses), "result_frames": len(frames)}
    v = frames[0]
    block = next(b for b in v["tool_blocks"] if b.get("id") == uses[0])
    return {"edit_uses": 1, "result_frames": 1, "tool_use_id": uses[0], "is_error": block.get("is_error"),
            "tool_use_result": v.get("tool_use_result"), "tool_blocks_in_frame": len(v["tool_blocks"]),
            "tool_result_meta_present": "tool_result_meta" in v, "is_replay": v.get("isReplay"),
            "parent_tool_use_id": v.get("parent_tool_use_id")}


def claude_renewal(e, r, version, traced, identity, ev):
    """Re-derive the receipt's renewal verdicts (F-26, D-078). Returns what the
    files could not show."""
    journal = lambda: e.lines("worker.json.events")
    images = lambda: renewal_images(e, identity())
    generation = lambda: e.json("worker.json.proc")["attempt"]
    allowed = lambda: str(e.root / "work/allowed.txt")
    qualified = version in WRITE_RENEWAL_QUALIFIED
    e.check("renewal_runner_verdict", lambda: isinstance(r["renewal"]["checks"], dict) and
            set(r["renewal"]["checks"]) == (RENEWAL_CHECKS if qualified else UNQUALIFIED_RENEWAL_CHECKS) and
            all(v is True for v in r["renewal"]["checks"].values()))
    e.check("renewal_receipt_matches_evidence", lambda: renewal_receipt_matches(r, *images(), journal()))
    e.check("renewal_capability_declared", lambda: [v["capabilities"]["claim_renewal"]["support"]
                                                    for v in journal() if v.get("operation") == "start"] == ["experimental"])
    e.check("renewal_claims_bound", lambda: renewal_claims_bound(images(), identity(), CLAUDE_RENEWAL_CLAIMS))
    # Nothing outside the written path moved: the operator-denied write, the
    # never-named control, the holder's claim behind the gate's denial.
    still = ["worker-denied", "worker-control", "held"] + ([] if qualified else ["worker-allowed"])
    e.check("renewal_unwritten_claims_unmoved", lambda: renewal_unmoved(*images(), still))
    # Never on the adapter's stdout (D-078): the journal is its only record.
    # The Edit's result is the LAST renewal row, coverage unknown with the
    # adapter's omission note, renewing nothing; every row before it is the
    # Write's (applied when qualified, unknown otherwise).
    def edit_row_last():
        rows = renewal_rows(journal())
        if not rows:
            return False
        i, last = rows[-1]
        return (journal()[i].get("identity") == identity() and last.get("launch_generation") == generation() and
                last["coverage"] == {"state": "unknown", "detail": EDIT_OMISSION_NOTE} and
                last["result"] == {"state": "not_attempted"} and last["renewed_claims"] == 0)
    def write_rows():
        last = renewal_rows(journal())[-1][0]
        return [v for i, v in enumerate(journal()) if i != last]
    e.check("renewal_journal", lambda: not any("renewal" in v or v.get("operation") == "renewal" for v in ev()) and
            edit_row_last() and (
            renewal_applied(e, write_rows(), identity(), generation(), ["allowed.txt"], 1) if qualified else
            renewal_not_applied(journal(), identity(), generation()) and len(renewal_rows(journal())) == 2))
    # The runner's recorded Edit shape is the trace's, and it is the omission
    # this suite expects; an untraced run records none.
    e.check("edit_measurement", lambda: r.get("edit_measurement") == (edit_shape(e, allowed()) if traced else None) and
            (not traced or r["edit_measurement"]["is_error"] == "<absent>"))
    if not qualified:
        return ["renewal: Write omission unqualified on " + version + "; the conservative outcome was checked"]
    e.check("renewal_written_claim_moved", lambda: renewal_moved(*images(), identity(), ["worker-allowed"]))
    def tool_id():
        approvals = [v["native_event"]["approval"] for v in ev() if v.get("native_event", {}).get("kind") == "approval" and
                     v["native_event"]["approval"]["detail"]["input"]["file_path"] == allowed()]
        # Two for the allowed path: the Write's, then the Edit's.
        if len(approvals) != 2 or approvals[1]["item_id"] != "toolu_buddy_3":
            raise ValueError("want the Write's and then the Edit's approval for the allowed path")
        return approvals[0]
    # The witness D-078 renews an omitted is_error on is in the native trace
    # alone, so an untraced receipt cannot show why its Write renewed: refused.
    e.check("renewal_matched_result", lambda: traced and matched_write(e, allowed()) == tool_id()["item_id"])
    # After the operator's allow for THAT tool use (its result cannot exist
    # before it) and before the turn's idle. The journal has no row for the
    # tool_result itself, so the allow submission is the latest anchor it holds.
    def ordered():
        rows = journal()
        aid = tool_id()["id"]
        allow = [i for i, v in enumerate(rows) if (v.get("approval_response") or {}).get("id") == aid and
                 v["approval_response"]["decision"] == "allow" and (v.get("approval_delivery") or {}).get("state") == "submitted"]
        idle = [i for i, v in enumerate(rows) if (v.get("native_event") or {}).get("kind") == "idle"]
        at = [i for i, _ in renewal_rows(rows)]
        return len(allow) == 1 and bool(idle) and bool(at) and allow[0] < min(at) and max(at) < idle[0]
    e.check("renewal_ordered", ordered)
    return []


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
            (("checks", "positive_edit_allowed"), DELETE, "runner_verdict"),
            (("edit_measurement", "is_error"), False, "edit_measurement"),
            (("edit_measurement",), None, "edit_measurement"),
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
            (("root",), str(Path(r["root"]) / "work"), "receipt_location"),
            (("renewal", "checks", "claim_renewal_capability_declared"), False, "renewal_runner_verdict"),
            (("renewal", "checks", "renewal_same_identity"), DELETE, "renewal_runner_verdict"),
            (("renewal", "after", "worker-control"), 1, "renewal_receipt_matches_evidence"),
            (("renewal", "before", "held"), 1, "renewal_receipt_matches_evidence"),
            (("renewal", "journal"), [], "renewal_receipt_matches_evidence")]


# ------------------------------------------------------------ synthetic run

# The positive control and its damage cases (renewal_same_identity among them)
# need a version whose Write omission renews, so the default is the newest
# RECORDED version that is also qualified for it. It was the newest recorded
# version outright until 2.1.311 was recorded (2026-10-27) outside
# WRITE_RENEWAL_QUALIFIED, the only thing that matters here: every synthetic took
# the unqualified arm, and the self-test died deleting a check that arm never
# writes. The unqualified arm keeps its own control (unqualified_self_test).
SYNTHETIC_VERSION = [v for v in CLAUDE_VERSIONS if v in WRITE_RENEWAL_QUALIFIED][-1]


def synthetic(root, tag, version=SYNTHETIC_VERSION):
    """A coherent SYNTHETIC qualify-claude.py --trace-native run under root.

    Review F-33: a self-test that needs a native receipt in /private/tmp stops
    running after a reboot. These are real files read the same way the native
    run's are. They test the CHECKER and qualify no native harness.
    """
    root = Path(root)
    root.mkdir(parents=True)
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
    write(root, "work/allowed.txt", "allowed-edited\n")
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
                   "item_id": "toolu_buddy_%d" % n, "detail": {"input": {"file_path": str(root / "work" / name)}}})
    qualified = version in WRITE_RENEWAL_QUALIFIED
    caps = {"telemetry": {"support": "unknown"}, "claim_renewal": {"support": "experimental"}}
    events = [dict(event("start"), capabilities=caps),
              dict(event("send"), request_id="wake-1", wake_delivery={"state": "delivered", "turn_id": t1}),
              nat("busy", t1), approval("allowed.txt", 1),
              dict(event("approve"), request_id="peer-approval-refused", error="unsupported control operation \"approve\""),
              dict(event("approve"), request_id="operator-allow", approval_delivery={"state": "submitted"},
                   approval_response={"id": "ap-1", "decision": "allow"}),
              approval("operator-denied.txt", 2),
              dict(event("approve"), request_id="operator-deny", approval_delivery={"state": "submitted"},
                   approval_response={"id": "ap-2", "decision": "deny"}),
              approval("allowed.txt", 3),
              dict(event("approve"), request_id="operator-allow-edit", approval_delivery={"state": "submitted"},
                   approval_response={"id": "ap-3", "decision": "allow"}),
              nat("result", t1, result_ref="res-1", result_status="completed"), nat("idle", t1),
              dict(event("send"), request_id="wake-duplicate", wake_delivery={"state": "delivered", "turn_id": t1}),
              dict(event("send"), request_id="wake-2", wake_delivery={"state": "delivered", "turn_id": t2}),
              nat("result", t2, result_ref="res-2", result_status="completed"), nat("idle", t2)]
    write(root, "adapter-events.json", events)
    # The journal also holds the renewal batch, after the allow is submitted and
    # before the idle (D-078); stdout never carries it.
    generation = "a" * 32
    renewal = ({"launch_generation": generation, "paths": [str(root / "work/allowed.txt")], "placed_paths": ["allowed.txt"],
                "renewed_claims": 1, "result": {"state": "applied"}, "coverage": {"state": "observed"}} if qualified else
               {"launch_generation": generation, "renewed_claims": 0, "result": {"state": "not_attempted"},
                "coverage": {"state": "unknown"}})
    intent = dict(events[5], approval_delivery={"state": "unknown", "detail": "approval response intent"})
    # The Edit's result renews nothing on any version: one unknown row after
    # its allow and before the idle (EDIT_OMISSION_NOTE).
    edit_renewal = {"launch_generation": generation, "renewed_claims": 0, "result": {"state": "not_attempted"},
                    "coverage": {"state": "unknown", "detail": EDIT_OMISSION_NOTE}}
    journal = (events[:5] + [intent, events[5], dict(event("renewal"), renewal=renewal)] + events[6:10] +
               [dict(event("renewal"), renewal=edit_renewal)] + events[10:])
    write(root, "worker.json.events", jsonl(journal))
    out = [{"type": "tool_result", "tool_use_id": "toolu_buddy_0", "is_error": True,
            "content": 'PreToolUse:Write hook error: guarded.txt is inside scope "guarded.txt" claimed by session work/s-fixtureh'},
           {"type": "tool_result", "tool_use_id": "toolu_buddy_1", "content": "File created successfully at: %s" % (root / "work/allowed.txt")},
           {"type": "tool_result", "tool_use_id": "toolu_buddy_2", "is_error": True, "content": "Denied by the operator control interface"},
           {"type": "tool_result", "tool_use_id": "toolu_buddy_3",
            "content": "The file %s has been updated successfully." % (root / "work/allowed.txt")}]
    write(root, "provider-observations.json", [{"path": "/v1/messages", "tool_outputs": out[:n]} for n in (0, 1, 2, 3, 4, 4)])
    write(root, "ledger-inspection.json", {"identity": {"session_id": sid, "incarnation": inc}, "worktree": str(root / "work"),
                                           "endpoint": {"status": "observed", "pid": 4242, "born": 1}})
    write(root, "unparsed-output.json", [])
    write(root, "adapter.stderr", close_report())
    write(root, "worker.json.proc", dict(proc_source(receipt, identity, root / "bin/claude-observer"), attempt=generation))
    init = {"type": "system", "subtype": "init", "session_id": sid, "plugins": BUILTIN_MODS}
    edit_witness = {"type": None, "filePath": str(root / "work/allowed.txt"), "keys": ["filePath", "newString", "oldString",
                    "originalFile", "replaceAll", "structuredPatch", "userModified"]}
    def use(n, name, tool="Write"):
        return {"type": "assistant", "session_id": sid, "parent_tool_use_id": None, "tool_blocks": [
                {"type": "tool_use", "id": "toolu_buddy_%d" % n, "name": tool, "is_error": None, "path": str(root / "work" / name)}]}
    def result(n, error, witness):
        return {"type": "user", "session_id": sid, "parent_tool_use_id": None, "tool_use_result": witness, "tool_blocks": [
                {"type": "tool_result", "id": "toolu_buddy_%d" % n, "name": None, "is_error": error, "path": None}]}
    write(root, "native-observations.jsonl", jsonl([
        {"type": "buddy_fixture_native_process", "pid": 501}, {"type": "buddy_fixture_native_process", "pid": 503}, init,
        {"type": "user", "session_id": sid, "isReplay": True, "parent_tool_use_id": None},
        use(0, "guarded.txt"), result(0, True, "<str>"), use(1, "allowed.txt"),
        result(1, "<absent>", {"type": "create", "filePath": str(root / "work/allowed.txt")}),
        use(2, "operator-denied.txt"), result(2, True, "<str>"), use(3, "allowed.txt", "Edit"),
        result(3, "<absent>", edit_witness), {"type": "result", "subtype": "success", "session_id": sid},
        init, {"type": "result", "subtype": "success", "session_id": sid}]))
    # The runner's two ledger copies: before the wake (no idle row) and after
    # the turn's idle; only the written claim moved, at or before the idle.
    holder = ("fixture-holder", "inc-holder-" + tag)
    moved = 1791000003 if qualified else 1791000001
    sessions = [(holder[0], holder[1], str(root / "work")), (sid, inc, str(root / "work"))]
    def claims(allowed):
        return [("c0" + tag, "held", holder[0], holder[1], 1791000000, 1791000000, "guarded.txt"),
                ("c1" + tag, "worker-allowed", sid, inc, 1791000001, allowed, "allowed.txt"),
                ("c2" + tag, "worker-denied", sid, inc, 1791000001, 1791000001, "operator-denied.txt"),
                ("c3" + tag, "worker-control", sid, inc, 1791000001, 1791000001, "control.txt"),
                ("c4" + tag, "worker-control", sid, inc, 1790000000, 1790000000, "control.txt", "released")]
    first, second = (BEFORE, AFTER) if tag != "b" else (AFTER, BEFORE)
    ledger_fixture(root / first, sessions, claims(1791000001), [])
    ledger_fixture(root / second, sessions + [("fixture-coordinator", "inc-c", str(root / "work"))],
                   claims(moved), [(sid, inc, 1791000003)])
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
         "native_version": version + " (Claude Code)", "native_sha256": admission.synthetic("claude", version, root / "bin/claude"),
         "source_manifest_sha256": sha_file(root / "source-manifest.json"),
         "buddy_sha256": sha_file(root / "bin/buddy"), "adapter_sha256": sha_file(root / "bin/buddy-adapter"),
         "configuration_evidence": {"settings_sha256": sha_file(root / "settings.json"), "mods_profile": "builtin-only-" + version},
         "managed_hook_refusal": managed, "native_metadata_observer": {"sha256": sha_file(root / "bin/claude-observer")},
         "cleanup_observation": {"status": "observed_absent", "observed_pids": [501, 503], "remaining_or_unknown_pids": []},
         "adapter_exit": 0,
         "edit_measurement": {"edit_uses": 1, "result_frames": 1, "tool_use_id": "toolu_buddy_3", "is_error": "<absent>",
                              "tool_use_result": edit_witness, "tool_blocks_in_frame": 1, "tool_result_meta_present": False,
                              "is_replay": None, "parent_tool_use_id": None},
         "renewal": {"before": {"held": 1791000000, "worker-allowed": 1791000001, "worker-control": 1791000001,
                                "worker-denied": 1791000001},
                     "after": {"held": 1791000000, "worker-allowed": moved, "worker-control": 1791000001,
                               "worker-denied": 1791000001},
                     "journal": [renewal, edit_renewal],
                     "checks": {k: True for k in (RENEWAL_CHECKS if qualified else UNQUALIFIED_RENEWAL_CHECKS)}}}
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
        # The Edit (D-078's open question): its approval, its renewal row, and
        # the shape the runner says it measured.
        ("edit approval not submitted", edit_json("adapter-events.json",
            lambda rows: [v.update(approval_delivery={"state": "unknown"}) for v in rows if v.get("request_id") == "operator-allow-edit"]),
            "allowed_edit"),
        ("edit result renewed its claim", edit_lines("worker.json.events",
            lambda rows: [v["renewal"].update(result={"state": "applied"}, renewed_claims=1, coverage={"state": "observed"})
                          for v in rows if v.get("renewal", {}).get("coverage", {}).get("detail") == EDIT_OMISSION_NOTE]),
            "renewal_journal"),
        ("edit result carried explicit is_error false", edit_lines("native-observations.jsonl",
            lambda rows: [b.update(is_error=False) for v in rows if v.get("type") == "user"
                          for b in v.get("tool_blocks") or [] if b.get("id") == "toolu_buddy_3"]),
            "edit_measurement"),
        # Receipt and trace AGREE on an explicit false: consistent, but not the
        # omission this suite's renewal expectations rest on.
        ("edit measured with explicit is_error false", lambda: (
            edit_lines("native-observations.jsonl", lambda rows: [b.update(is_error=False) for v in rows if v.get("type") == "user"
                       for b in v.get("tool_blocks") or [] if b.get("id") == "toolu_buddy_3"])(),
            mutate(r, ("edit_measurement", "is_error"), False))[-1],
            "edit_measurement"),
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
        # Claim renewal (F-26, D-078): each re-derived verdict, damaged alone.
        ("the denied write's claim renewed", lambda: image_damage(root, r, AFTER, "worker-denied", 1791000003),
         "renewal_unwritten_claims_unmoved"),
        ("the control claim moved", lambda: image_damage(root, r, AFTER, "worker-control", 1791000002),
         "renewal_unwritten_claims_unmoved"),
        ("the holder's claim moved", lambda: image_damage(root, r, AFTER, "held", 1791000002),
         "renewal_unwritten_claims_unmoved"),
        ("the written claim's renewal did not move", lambda: image_damage(root, r, AFTER, "worker-allowed", 1791000001),
         "renewal_written_claim_moved"),
        ("the written claim renewed after the idle", lambda: image_damage(root, r, AFTER, "worker-allowed", 1791000004),
         "renewal_written_claim_moved"),
        ("renewal journalled after the idle", lambda: journal_damage(root, r, "worker.json.events",
            lambda rows: rows.insert(next(i for i, v in enumerate(rows) if v.get("native_event", {}).get("kind") == "idle"),
                                     rows.pop(next(i for i, _ in renewal_rows(rows))))), "renewal_ordered"),
        ("renewal journalled before the allow", lambda: journal_damage(root, r, "worker.json.events",
            lambda rows: rows.insert(1, rows.pop(next(i for i, _ in renewal_rows(rows))))), "renewal_ordered"),
        ("a renewal operation on the adapter's stdout", edit_json("adapter-events.json",
            lambda rows: rows.insert(6, {"operation": "renewal", "identity": rows[0]["identity"]})), "renewal_journal"),
        ("a renewal record on the adapter's stdout", edit_json("adapter-events.json",
            lambda rows: rows[6].update(renewal={})), "renewal_journal"),
        ("two open claims of one slug", lambda: ledger_update(root / AFTER, "INSERT INTO claims SELECT 'cy', session_id, "
            "incarnation, slug, descr, created, renewed, state, shared, outcome, outcome_note FROM claims "
            "WHERE slug = 'worker-control' AND state = 'open'") or ledger_update(root / AFTER, "INSERT INTO claim_scopes "
            "VALUES ('cy', 'control.txt', 'control.txt')"), "renewal_claims_bound"),
        ("a worker claim held by the holder", lambda: [ledger_update(root / n, "UPDATE claims SET session_id = 'fixture-holder', "
            "incarnation = 'inc-holder-a' WHERE slug = 'worker-control'") for n in (BEFORE, AFTER)], "renewal_claims_bound"),
        ("renewal placed the denied path", lambda: journal_damage(root, r, "worker.json.events",
            lambda rows: [v["renewal"].update(placed_paths=["operator-denied.txt"], paths=[str(root / "work/operator-denied.txt")])
                          for v in rows if "renewal" in v]), "renewal_journal"),
        ("renewal's native spelling outside the worktree", lambda: journal_damage(root, r, "worker.json.events",
            lambda rows: [v["renewal"].update(paths=["/elsewhere/allowed.txt"]) for v in rows if "renewal" in v]),
         "renewal_journal"),
        ("renewal coverage unknown", lambda: journal_damage(root, r, "worker.json.events",
            lambda rows: [v["renewal"].update(coverage={"state": "unknown"}) for v in rows if "renewal" in v]), "renewal_journal"),
        ("renewal from another launch generation", lambda: journal_damage(root, r, "worker.json.events",
            lambda rows: [v["renewal"].update(launch_generation="b" * 32) for v in rows if "renewal" in v]), "renewal_journal"),
        ("renewal counted a second claim", lambda: journal_damage(root, r, "worker.json.events",
            lambda rows: [v["renewal"].update(renewed_claims=2) for v in rows if "renewal" in v]), "renewal_journal"),
        ("renewal under another incarnation", lambda: journal_damage(root, r, "worker.json.events",
            lambda rows: [v.update(identity=dict(v["identity"], incarnation="other")) for v in rows if "renewal" in v]),
         "renewal_journal"),
        ("no renewal journalled", lambda: journal_damage(root, r, "worker.json.events",
            lambda rows: rows.pop(next(i for i, _ in renewal_rows(rows)))), "renewal_journal"),
        ("the allowed write's result was an error", edit_lines("native-observations.jsonl",
            lambda rows: allowed_result(rows)["tool_blocks"][0].update(is_error=True)), "renewal_matched_result"),
        # 0 == False in Python, so a membership test let a numeric is_error
        # through; the adapter's reader calls it non-boolean and renews
        # nothing (Codex code review of the tool_result_meta rule, finding 6).
        ("the allowed write's is_error was the number 0", edit_lines("native-observations.jsonl",
            lambda rows: allowed_result(rows)["tool_blocks"][0].update(is_error=0)), "renewal_matched_result"),
        ("the omitted is_error has no witness", edit_lines("native-observations.jsonl",
            lambda rows: allowed_result(rows).update(tool_use_result="<str>")), "renewal_matched_result"),
        ("the witness names another path", edit_lines("native-observations.jsonl",
            lambda rows: allowed_result(rows)["tool_use_result"].update(filePath=str(root / "work/operator-denied.txt"))),
         "renewal_matched_result"),
        ("the result was a replay", edit_lines("native-observations.jsonl",
            lambda rows: allowed_result(rows).update(isReplay=True)), "renewal_matched_result"),
        ("the result was a subagent's", edit_lines("native-observations.jsonl",
            lambda rows: allowed_result(rows).update(parent_tool_use_id="toolu_task")), "renewal_matched_result"),
        ("the result came after the turn ended", edit_lines("native-observations.jsonl",
            lambda rows: rows.insert(next(i for i, v in enumerate(rows) if v.get("type") == "result"),
                                     rows.pop(rows.index(allowed_result(rows))))), "renewal_matched_result"),
        ("the after image is gone", lambda: (root / AFTER).unlink(), "renewal_claims_bound"),
        ("the after image holds no idle", lambda: ledger_update(root / AFTER, "DELETE FROM session_idle"), "renewal_claims_bound"),
        ("the holder's claim is the worker's", lambda: [ledger_update(root / n, "UPDATE claims SET session_id = ?, "
            "incarnation = ? WHERE slug = 'held'", (json.loads((root / "worker.json").read_text())["identity"]["session_id"],
                                                    json.loads((root / "worker.json").read_text())["identity"]["incarnation"]))
            for n in (BEFORE, AFTER)], "renewal_claims_bound"),
        ("a claim was re-created between the images", lambda: ledger_update(root / AFTER,
            "UPDATE claims SET created = 1791000002 WHERE slug = 'worker-control'"), "renewal_claims_bound"),
        ("the images name another worktree", lambda: [ledger_update(root / n, "UPDATE sessions SET worktree = '/elsewhere'")
                                                      for n in (BEFORE, AFTER)], "renewal_claims_bound"),
        ("an extra claim of the worker's", lambda: [extra_claim(root / n, root) for n in (BEFORE, AFTER)], "renewal_claims_bound"),
        ("the holder's claim under the worker's session id", lambda: [ledger_update(root / n, "UPDATE claims SET session_id = ? "
            "WHERE slug = 'held'", (json.loads((root / "worker.json").read_text())["identity"]["session_id"],))
            for n in (BEFORE, AFTER)], "renewal_claims_bound"),
        ("renewal result unconfirmed", lambda: journal_damage(root, r, "worker.json.events",
            lambda rows: [v["renewal"].update(result={"state": "unconfirmed"}) for v in rows if "renewal" in v]), "renewal_journal"),
        ("renewal left a path unresolved", lambda: journal_damage(root, r, "worker.json.events",
            lambda rows: [v["renewal"].update(unresolved_paths=["allowed.txt"]) for v in rows if "renewal" in v]), "renewal_journal"),
        ("renewal capability not declared", lambda: journal_damage(root, r, "worker.json.events",
            lambda rows: rows[0]["capabilities"].update(claim_renewal={"support": "unsupported"})), "renewal_capability_declared"),
        ("a second Write of the allowed path", edit_lines("native-observations.jsonl",
            lambda rows: rows.insert(-2, dict(rows[6], tool_blocks=[dict(rows[6]["tool_blocks"][0], id="toolu_buddy_9")]))),
         "renewal_matched_result"),
        ("a second result for the allowed Write", edit_lines("native-observations.jsonl",
            lambda rows: rows.insert(-2, allowed_result(rows))), "renewal_matched_result"),
        ("the result frame carries a second block", edit_lines("native-observations.jsonl",
            lambda rows: allowed_result(rows)["tool_blocks"].append({"type": "tool_result", "id": "toolu_x", "is_error": "<absent>"})),
         "renewal_matched_result"),
        ("the witness is a delete", edit_lines("native-observations.jsonl",
            lambda rows: allowed_result(rows)["tool_use_result"].update(type="delete")), "renewal_matched_result"),
        # tool_result_meta on the allowed Write (D-078 addendum): every reading
        # but 'proceed' leaves the omission unwitnessed. meta_self_test holds
        # the positive control, the measured 2.1.299 accept, which passes.
        ("the allowed write's result is marked not executed", edit_lines("native-observations.jsonl",
            lambda rows: allowed_result(rows).update(tool_result_meta=[{"id": "toolu_buddy_1", "non_execution_kind": "permission-rule"}])),
         "renewal_matched_result"),
        ("the allowed write's meta entry is id-only", edit_lines("native-observations.jsonl",
            lambda rows: allowed_result(rows).update(tool_result_meta=[{"id": "toolu_buddy_1"}])), "renewal_matched_result"),
        ("the allowed write's accept carries remedy", edit_lines("native-observations.jsonl",
            lambda rows: allowed_result(rows).update(tool_result_meta=[{"id": "toolu_buddy_1", "remedy": "retry",
                                                                       "permission_decision": {"decision": "accept"}}])),
         "renewal_matched_result"),
        ("the allowed write's meta names a result outside its frame", edit_lines("native-observations.jsonl",
            lambda rows: allowed_result(rows).update(tool_result_meta=[{"id": "toolu_buddy_9", "permission_decision": {"decision": "accept"}}])),
         "renewal_matched_result"),
        ("the approval names another tool use", edit_json("adapter-events.json",
            lambda rows: [v["native_event"]["approval"].update(item_id="toolu_buddy_7") for v in rows
                          if v.get("native_event", {}).get("kind") == "approval"][:1]), "renewal_matched_result"),
        ("the trace was not kept", lambda: drop_trace(root, r), "renewal_matched_result"),
        ("a third ledger image", lambda: shutil.copyfile(root / BEFORE, root / "renewal-2extra.db"), "renewal_claims_bound"),
        ("the result came before its use", edit_lines("native-observations.jsonl",
            lambda rows: rows.insert(rows.index(next(v for v in rows if v.get("type") == "assistant" and
                                                     v["tool_blocks"][0]["id"] == "toolu_buddy_1")),
                                     rows.pop(rows.index(allowed_result(rows))))), "renewal_matched_result"),
        ("the allowed write was an Edit", edit_lines("native-observations.jsonl",
            lambda rows: [b.update(name="Edit") for v in rows if v.get("type") == "assistant"
                          for b in v["tool_blocks"] if b["id"] == "toolu_buddy_1"]), "renewal_matched_result"),
        ("a second approval for the allowed path", edit_json("adapter-events.json",
            lambda rows: rows.insert(4, dict(rows[3], native_event=dict(rows[3]["native_event"],
                approval=dict(rows[3]["native_event"]["approval"], id="ap-9"))))), "renewal_matched_result"),
        ("the approval was answered deny", lambda: journal_damage(root, r, "worker.json.events",
            lambda rows: [v["approval_response"].update(decision="deny") for v in rows
                          if (v.get("approval_response") or {}).get("id") == "ap-1"]), "renewal_ordered"),
        ("the allow was submitted twice", lambda: journal_damage(root, r, "worker.json.events",
            lambda rows: rows.insert(-1, next(v for v in rows if (v.get("approval_response") or {}).get("decision") == "allow" and
                                              v["approval_delivery"]["state"] == "submitted"))),
         "renewal_ordered"),
    ]


def extra_claim(path, root):
    """A claim of the worker's beyond the expected four, covering the written path."""
    identity = json.loads((Path(root) / "worker.json").read_text())["identity"]
    ledger_update(path, "INSERT INTO claims VALUES ('cx', ?, ?, 'worker-extra', 'q', 1791000001, 1791000001, 'open', 0, NULL, NULL)",
                  (identity["session_id"], identity["incarnation"]))
    ledger_update(path, "INSERT INTO claim_scopes VALUES ('cx', 'allowed.txt', 'allowed.txt')")


def drop_trace(root, r):
    """The receipt and the trace without the traced mode: everything else keeps
    passing, and a qualified renewal can no longer show its witness."""
    root = Path(root)
    bad = copy.deepcopy(r)
    del bad["native_metadata_observer"]
    proc = json.loads((root / "worker.json.proc").read_text())
    proc["executable"] = {"path": str(root / "bin/claude"), "size": (root / "bin/claude").stat().st_size}
    write(root, "worker.json.proc", proc)
    return bad


BEFORE, AFTER = "renewal-0before.db", "renewal-1after.db"


def allowed_result(rows):
    """The synthetic trace's tool_result frame for the allowed Write."""
    return next(v for v in rows if v.get("type") == "user" and
                any(b.get("id") == "toolu_buddy_1" for b in v.get("tool_blocks") or []))


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
          ("provider-observations.json", "allowed_control"), (AFTER, "renewal_claims_bound")]


def unqualified_self_test():
    """A version where a Write's omitted is_error is NOT qualified: the expected
    outcome is the conservative one, so a moved claim or an applied batch there
    is refused."""
    with tempfile.TemporaryDirectory(prefix="buddy-claude-gate-unqualified-") as tmp:
        root = Path(tmp).resolve() / "run"
        r = synthetic(root, "u", version="2.1.290")
        receipt = root / "result.json"
        verdict = validate(r, receipt)
        if not verdict["passed"]:
            raise AssertionError("unqualified positive control failed: " + json.dumps(verdict["errors"]))
        rejected = 0
        for label, damage, check in (
                ("unqualified write renewed", lambda: image_damage(root, r, AFTER, "worker-allowed", 1791000003),
                 "renewal_unwritten_claims_unmoved"),
                ("unqualified batch applied", lambda: journal_damage(root, r, "worker.json.events",
                    lambda rows: [v["renewal"].update(result={"state": "applied"}) for v in rows if "renewal" in v]),
                 "renewal_journal"),
                ("unqualified coverage observed", lambda: journal_damage(root, r, "worker.json.events",
                    lambda rows: [v["renewal"].update(coverage={"state": "observed"}) for v in rows if "renewal" in v]),
                 "renewal_journal"),
                ("unqualified batch counted a claim", lambda: journal_damage(root, r, "worker.json.events",
                    lambda rows: [v["renewal"].update(renewed_claims=1) for v in rows if "renewal" in v]), "renewal_journal"),
                ("unqualified batch from another generation", lambda: journal_damage(root, r, "worker.json.events",
                    lambda rows: [v["renewal"].update(launch_generation="c" * 32) for v in rows if "renewal" in v]),
                 "renewal_journal"),
                ("unqualified batch under another incarnation", lambda: journal_damage(root, r, "worker.json.events",
                    lambda rows: [v.update(identity=dict(v["identity"], incarnation="other")) for v in rows if "renewal" in v]),
                 "renewal_journal"),
                ("unqualified run journalled no batch", lambda: journal_damage(root, r, "worker.json.events",
                    lambda rows: rows.pop(next(i for i, _ in renewal_rows(rows)))), "renewal_journal")):
            snapshot = root.parent / "pristine"
            shutil.copytree(root, snapshot)
            try:
                verdict = validate(damage(), receipt)
                if verdict["passed"] or verdict["checks"].get(check) is not False:
                    raise AssertionError("accepted damaged unqualified evidence %s (want %s to fail)" % (label, check))
                rejected += 1
            finally:
                shutil.rmtree(root)
                shutil.move(str(snapshot), str(root))
        return rejected


def unhashed_image_self_test(validate, synthetic):
    """The runners hash a fixed list that does not name the renewal images: a
    hashed receipt without them passes and reports them unhashed; one that
    lists an image with the wrong hash is refused like any other file."""
    with tempfile.TemporaryDirectory(prefix="buddy-renewal-unhashed-") as tmp:
        root = Path(tmp).resolve() / "run"
        r = synthetic(root, "h")
        e = Evidence(r, root)
        read = validate(r, root / "result.json")["read"]
        hashed = mutate(r, ("evidence_sha256",), {p: e.sha(root / p) for p in read
                                                  if p != "result.json" and not p.startswith(RENEWAL_IMAGE_PREFIX)})
        verdict = validate(hashed, root / "result.json")
        if not verdict["passed"] or verdict["evidence"] != "hashed" or verdict["unhashed"] != [BEFORE, AFTER]:
            raise AssertionError("a hashed receipt that leaves the images out was not passed as unhashed: " +
                                 json.dumps(verdict["errors"]))
        hashed["evidence_sha256"][AFTER] = "0" * 64
        if validate(hashed, root / "result.json")["checks"].get("evidence_hashes") is not False:
            raise AssertionError("accepted a wrong hash for a listed renewal image")
        return 1


META_FIXTURE = Path(__file__).resolve().parent.parent / "internal/harness/testdata/claude-tool-result-meta.json"
# The measured 2.1.299 entries, by tool use of the synthetic run (one gate
# denial, one operator allow, one operator denial, one Edit allow).
META_2_1_299 = {"toolu_buddy_0": {"non_execution_kind": "permission-rule",
                                  "permission_decision": {"decision": "reject", "source": "hook", "reason_type": "hook"}},
                "toolu_buddy_1": {"permission_decision": {"decision": "accept", "source": "user_temporary"}},
                "toolu_buddy_2": {"non_execution_kind": "permission-rule",
                                  "permission_decision": {"decision": "reject", "source": "user_reject"}},
                "toolu_buddy_3": {"permission_decision": {"decision": "accept", "source": "user_temporary"}}}


def meta_self_test():
    """meta_verdict against the table claude_renewal.go is tested with (the
    fixture's arms and its measured frames' verdicts), then a 2.1.299 run whose
    trace carries that version's measured entries: it passes (the accepted
    Write still witnesses its omission), and the same run with the allowed
    Write's entry reduced to its id is refused. Returns the refusals counted."""
    doc = json.loads(META_FIXTURE.read_text())
    for arm in doc["arms"]:
        target = next(b for b in arm["results"] if b["id"] == arm["id"])
        ids = frame_result_ids({"tool_blocks": [{"type": "tool_result", "id": b["id"]} for b in arm["results"]]})
        got = meta_verdict(arm.get("meta"), ids, arm["id"], target.get("is_error", ABSENT), arm.get("witness", False))
        if got != arm["verdict"]:
            raise AssertionError("meta_verdict(%s) = %s, the Go table says %s" % (arm["name"], got, arm["verdict"]))
    witnessed = 0
    for row in doc["measured"]:
        frame = row["frame"]
        blocks = [b for b in frame["message"]["content"] if b.get("type") == "tool_result"]
        ids = {b["tool_use_id"] for b in blocks if b.get("tool_use_id")}
        tur = frame.get("tool_use_result")
        for b in blocks:
            uses = [u for u in row["uses"] if u["id"] == b["tool_use_id"] and u["name"] == "Write"]
            witness = (len(uses) == 1 and len(blocks) == 1 and isinstance(tur, dict) and
                       tur.get("type") in ("create", "update") and tur.get("filePath") == uses[0]["path"])
            witnessed += witness
            got = meta_verdict(frame.get("tool_result_meta"), ids, b["tool_use_id"], b.get("is_error", ABSENT), witness)
            if got != row["verdicts"][b["tool_use_id"]]:
                raise AssertionError("meta_verdict of measured %s %s = %s" % (row["source"], b["tool_use_id"], got))
            # No measured result takes the witness arm (the Go test asserts the same).
            if meta_verdict(frame.get("tool_result_meta"), ids, b["tool_use_id"], b.get("is_error", ABSENT)) != got:
                raise AssertionError("measured %s %s takes the witness arm" % (row["source"], b["tool_use_id"]))
    if witnessed < 30:
        raise AssertionError("the success witness was found on %d measured results, want at least 30" % witnessed)
    if len(doc["arms"]) < 40 or len(doc["measured"]) < 79:
        raise AssertionError("the tool_result_meta fixture lost rows")
    with tempfile.TemporaryDirectory(prefix="buddy-claude-gate-meta-") as tmp:
        root = Path(tmp).resolve() / "run"
        r = synthetic(root, "m", version="2.1.299")
        trace = root / "native-observations.jsonl"
        rows = [json.loads(v) for v in trace.read_text().splitlines()]
        for v in rows:
            if v.get("type") == "user" and v.get("tool_blocks"):
                tid = v["tool_blocks"][0]["id"]
                v["tool_result_meta"] = [dict({"id": tid}, **META_2_1_299[tid])]
        write(root, "native-observations.jsonl", jsonl(rows))
        r["edit_measurement"]["tool_result_meta_present"] = True
        write(root, "result.json", r)
        verdict = validate(r, root / "result.json")
        if not verdict["passed"]:
            raise AssertionError("the measured 2.1.299 entries were refused: " + json.dumps(verdict["errors"]))
        allowed_result(rows)["tool_result_meta"] = [{"id": "toolu_buddy_1"}]
        write(root, "native-observations.jsonl", jsonl(rows))
        if validate(r, root / "result.json")["checks"].get("renewal_matched_result") is not False:
            raise AssertionError("an id-only entry on the allowed Write was accepted as a witnessed success")
    return 1


def synthetic_self_test():
    out = self_test_suite("claude-gate", validate, synthetic, receipt_mutations, file_mutations, SPLICE)
    out["rejected_mutations"] += unqualified_self_test() + unhashed_image_self_test(validate, synthetic) + meta_self_test()
    return out


if __name__ == "__main__":
    sys.exit(main_for(__doc__, validate, synthetic_self_test, receipt_mutations))
