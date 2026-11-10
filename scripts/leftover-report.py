#!/usr/bin/env python3
"""leftover-report.py: the census behind scripts/leftover-report.sh (#84).

Lists the operator's processes that outlived the process that started them
and were adopted by launchd, ties each to a buddy session where the kernel
lets it, and says whether that session is live, ended, or unknown. It reads;
it never signals, ends or grants anything. Run it through the .sh wrapper:

    sh scripts/leftover-report.sh [--all] [<repo> ...]   # default: the cwd's repo
    python3 -I scripts/leftover-report.py --self-test

WHY IT EXISTS (wishlist section 22, issue #84). After one fleet of about 25
lanes over about 10 hours, a hand census found 202 processes of the operator's
user reparented to launchd, about 306 MB RSS: 182 gpg-agents a test suite left
in temporary GNUPGHOMEs, tail/ugrep watchers on old tier logs, a node fixture
server, fake and demo `codex app-server`s. No buddy verb could see one: JOBS
(D-065) counts the direct children of a LIVE session's registered harness
process, and a leftover is a child of pid 1 whose session row has ended.

WHAT WAS MEASURED (darwin 27, same user, 2026-11-10), and what it decided:
  - Orphan or launchd's own. ppid 1 alone cannot tell them apart: of 452
    ppid-1 processes of this user, most were launchd agents, apps and XPC
    services. The kernel keeps the ORIGINAL parent's id version
    (proc_pidinfo PROC_PIDUNIQIDENTIFIERINFO, p_orig_ppidversion) across the
    reparent, while pbi_ppid and p_puniqueid both move to launchd. launchd's
    own p_idversion (pid 1, readable by any user through that flavor) was
    1017, and 431 of the 452 carried 1017 as their original parent: started
    by launchd. The other 21 carried another version, and every process
    planted with `( cmd & )` was among them. That is the orphan test here.
  - cwd: proc_pidinfo PROC_PIDVNODEPATHINFO returned it for all 452 (lsof -d
    cwd agrees). A cwd in a Claude Code scratchpad
    (/private/tmp/claude-<uid>/<project>/<session-id>/...) names the session.
  - Environment: sysctl KERN_PROCARGS2 returned it for a Go binary, node, and
    the command-line-tools Python, each carrying the CLAUDE_CODE_SESSION_ID a
    Bash-tool shell inherits (D-085). For Apple platform binaries (/bin/sh,
    /bin/zsh, /bin/sleep) the same call SUCCEEDED and returned argv with NO
    environment, and for some system processes it failed with EINVAL. So a
    `tail -F` under a zsh wrapper is tied by its cwd or not at all, and the
    report says "env withheld" rather than "no session".
  - Cut: a controlling tty (a Bash-tool shell has none, `??`) and ps's sess
    column (0 on darwin). session_procs anchors the harness process, whose
    child shell, the leftover's original parent, is gone too, so it ties
    nothing to a leftover; it does say whether an OPEN session is still
    running. Start-time overlap with an ended session's life, the issue's
    first proposal: with lanes running side by side, one leftover's start
    falls inside many sessions' lives, so it names nobody; env and cwd name
    the session directly.

WHAT IT PRINTS. One line per orphan ROOT (an orphan whose parent is launchd;
its descendants are counted into TREE and TREE_RSS, not listed): pid, ppid,
age, RSS, tree size and RSS, the kernel's 16-byte executable name (p_comm,
folded to a safe set), a verdict, the evidence and the session; at most 50
rows (ROW_CAP, D-010) and a count of the rest unless --all. NEVER argv
or environment text: a command line may carry a secret and is peer text in
another session's context (D-065). Labels are folded to one token
(invariant 9). The name is not argv; without it an operator cannot tell 182
gpg-agents from a VM helper without running `ps` on each pid, which prints
the command lines this report keeps out.

VERDICTS. OWNED: a session whose ledger row is open (leave it; tell that
session, `buddy msg`). STALE: every session tied to it has ended. UNKNOWN: no
tie, or a session id in none of the ledgers read: the operator's. A verdict
is a description, never permission (D-027): reaping is the orchestrator's
act, by pid, re-checked just before; whether it may reap without asking is
the operator's standing decision. Not listed, and said so in a count:
launchd's own processes (the chat daemon among them, D-122), other users'.

HOW A LEDGER IS READ: a private copy, as fleet-report.sh does. A buddy verb
under a registered session id moves that row's last_seen (D-086) and
store.Open migrates an older ledger, and sqlite3 on a WAL source creates its
-shm; so buddy.db and its -wal are read twice, compared, copied into a
mode-700 temporary directory and opened there. Three tries, then the ledger
is reported "not read (changing)".

Darwin only: elsewhere it prints CANNOT SAY and exits 2 (not run is neither
pass nor fail). Exit 0: a report was produced. Exit 1: a usage refusal.
"""

import ctypes
import os
import re
import shutil
import sqlite3
import struct
import subprocess
import sys
import tempfile
import time

# --- what the kernel says ------------------------------------------------------

PROC_PIDTASKINFO = 4
PROC_PIDTBSDINFO = 3
PROC_PIDVNODEPATHINFO = 9
PROC_PIDUNIQIDENTIFIERINFO = 17
SZOMB = 5
BSDINFO_SIZE = 136
TASKINFO_SIZE = 96
UNIQINFO_SIZE = 56
VNODEPATHINFO_SIZE = 2352
VIP_PATH_OFFSET = 152          # sizeof(struct vnode_info), cdir first
CTL_KERN, KERN_ARGMAX, KERN_PROCARGS2 = 1, 8, 49

SESSION_KEYS = (b"CLAUDE_CODE_SESSION_ID=", b"CODEX_THREAD_ID=")
ID_SHAPE = re.compile(r"[0-9A-Za-z][0-9A-Za-z_-]{0,127}\Z")
# A Claude Code scratchpad: /private/tmp/claude-<uid>/<project>/<session>/...
# (/tmp is the /private/tmp symlink; the kernel answers the resolved path).
SCRATCH = re.compile(r"(?:/private)?/tmp/claude-[0-9]+/[^/]+/([0-9a-fA-F-]{36})(?:/|\Z)")


class Kernel:
    """libproc and sysctl through ctypes: no ps, no pgrep, no awk, so no argv
    is ever read as text and no non-ASCII command line can abort the scan
    (D-122)."""

    def __init__(self):
        self.libc = ctypes.CDLL(None, use_errno=True)
        self.lp = ctypes.CDLL("/usr/lib/libproc.dylib", use_errno=True)
        self.lp.proc_pidinfo.argtypes = [ctypes.c_int, ctypes.c_int, ctypes.c_uint64,
                                         ctypes.c_void_p, ctypes.c_int]
        raw = self._sysctl([CTL_KERN, KERN_ARGMAX], 4)
        self.argmax = struct.unpack("i", raw)[0] if raw else 1 << 20

    def _sysctl(self, mib, size):
        arr = (ctypes.c_int * len(mib))(*mib)
        buf = ctypes.create_string_buffer(size)
        sz = ctypes.c_size_t(size)
        if self.libc.sysctl(arr, len(mib), buf, ctypes.byref(sz), None, ctypes.c_size_t(0)) != 0:
            return None
        return buf.raw[:sz.value]

    def _info(self, pid, flavor, size):
        buf = ctypes.create_string_buffer(size)
        n = self.lp.proc_pidinfo(pid, flavor, 0, buf, size)
        return buf.raw[:n] if n == size else None

    def pids(self):
        n = self.lp.proc_listallpids(None, 0)
        if n <= 0:
            return None
        arr = (ctypes.c_int * (n + 256))()
        got = self.lp.proc_listallpids(arr, ctypes.sizeof(arr))
        return None if got <= 0 else [p for p in arr[:got] if p > 0]

    def bsd(self, pid):
        b = self._info(pid, PROC_PIDTBSDINFO, BSDINFO_SIZE)
        if b is None:
            return None
        status, = struct.unpack_from("I", b, 4)
        ppid, uid = struct.unpack_from("II", b, 16)
        sec, usec = struct.unpack_from("QQ", b, 120)
        return {"status": status, "ppid": ppid, "uid": uid,
                "born": sec * 1000000 + usec, "comm": b[48:64].split(b"\0")[0]}

    def rss(self, pid):
        b = self._info(pid, PROC_PIDTASKINFO, TASKINFO_SIZE)
        return struct.unpack_from("Q", b, 8)[0] if b else 0

    def idversions(self, pid):
        """(own id version, original parent's id version), or None."""
        b = self._info(pid, PROC_PIDUNIQIDENTIFIERINFO, UNIQINFO_SIZE)
        return struct.unpack_from("iI", b, 32) if b else None

    def cwd(self, pid):
        b = self._info(pid, PROC_PIDVNODEPATHINFO, VNODEPATHINFO_SIZE)
        if b is None:
            return None
        return b[VIP_PATH_OFFSET:VIP_PATH_OFFSET + 1024].split(b"\0")[0].decode("utf-8", "replace")

    def env_ids(self, pid):
        raw = self._sysctl([CTL_KERN, KERN_PROCARGS2, pid], self.argmax)
        return session_ids_from_procargs(raw)


def session_ids_from_procargs(raw):
    """The session ids in a KERN_PROCARGS2 blob's ENVIRONMENT, never its argv.
    Returns (state, ids): state "read", "withheld" (argv and no environment:
    what darwin returns for a platform binary), or "unreadable".

    Layout: int argc; the exec path, NUL-terminated; NUL padding; argc
    NUL-terminated argv strings; the environment, each NUL-terminated, ended
    by an empty string (the apple[] strings follow it and are not read)."""
    if not raw or len(raw) < 4:
        return "unreadable", []
    argc, = struct.unpack_from("i", raw, 0)
    i = raw.find(b"\0", 4)
    if argc < 0 or i < 0:
        return "unreadable", []
    while i < len(raw) and raw[i] == 0:
        i += 1
    for _ in range(argc):
        j = raw.find(b"\0", i)
        if j < 0:
            return "unreadable", []
        i = j + 1
    ids, seen_env = [], False
    while i < len(raw):
        j = raw.find(b"\0", i)
        if j < 0:
            j = len(raw)
        s = raw[i:j]
        if not s:
            break
        seen_env = True
        for key in SESSION_KEYS:
            if s.startswith(key):
                v = s[len(key):].decode("ascii", "replace")
                if ID_SHAPE.match(v) and v not in ids:
                    ids.append(v)
        i = j + 1
    return ("read" if seen_env else "withheld"), ids


# --- what the ledgers say ------------------------------------------------------

def git_common_dir(repo):
    env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
    env["LC_ALL"] = "C"
    try:
        out = subprocess.run(["git", "-C", repo, "rev-parse", "--path-format=absolute", "--git-common-dir"],
                             capture_output=True, env=env, stdin=subprocess.DEVNULL, timeout=20)
    except (OSError, subprocess.SubprocessError):
        return None
    return out.stdout.decode("utf-8", "replace").strip() if out.returncode == 0 else None


def read_ledger(repo):
    """(verdict words, sessions dict or None). Sessions: id -> {label,
    worktree, ended, procs[(pid, born)]}."""
    if not os.path.isdir(repo):
        return "not read (no such directory)", None
    common = git_common_dir(repo)
    if not common:
        return "no ledger (not a repository)", None
    src = os.path.join(common, "buddy.db")
    if not os.path.lexists(src):
        return "no ledger (never buddy init-ed)", None
    if os.path.islink(src) or not os.path.isfile(src):
        return "not read (not a regular file)", None
    if os.path.lexists(src + "-journal"):
        # buddy opens WAL; a rollback journal beside it may be hot, and a copy
        # without it reads uncommitted pages as committed (Codex code pass).
        return "not read (a rollback journal is present)", None
    tmp = tempfile.mkdtemp(prefix="leftover-report.")
    try:
        os.chmod(tmp, 0o700)
        for _ in range(3):
            try:
                first = [_slurp(src + sfx) for sfx in ("", "-wal")]
                again = [_slurp(src + sfx) for sfx in ("", "-wal")]
            except OSError:
                return "UNREADABLE (the OS refused it)", None
            if first == again:
                break
        else:
            return "not read (changing)", None
        for sfx, data in zip(("", "-wal"), first):
            if data is not None:
                with open(os.path.join(tmp, "buddy.db" + sfx), "wb") as f:
                    f.write(data)
        try:
            c = sqlite3.connect(os.path.join(tmp, "buddy.db"))
            sessions = {}
            for sid, label, wt, ended in c.execute("SELECT session_id, label, worktree, ended FROM sessions"):
                sessions[sid] = {"label": label, "worktree": wt, "ended": ended, "procs": []}
            for sid, pid, born in c.execute("SELECT session_id, pid, born FROM session_procs"):
                if sid in sessions:
                    sessions[sid]["procs"].append((pid, born))
            c.close()
        except sqlite3.Error:
            return "UNREADABLE (not a ledger this report can read)", None
        return "read, %d session(s)" % len(sessions), sessions
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def merge_sessions(dst, src):
    """A session registered in two ledgers (one coordinator, two repos) is
    open if any row is, ended at the latest end otherwise, with every
    registered process: the arguments' order decides nothing (Codex code
    pass; the first shape kept whichever ledger came first)."""
    for sid, s in src.items():
        d = dst.get(sid)
        if d is None:
            dst[sid] = {"label": s["label"], "worktree": s["worktree"], "ended": s["ended"],
                        "procs": list(s["procs"]), "worktrees": [s["worktree"]]}
            continue
        d["ended"] = None if d["ended"] is None or s["ended"] is None else max(d["ended"], s["ended"])
        d["procs"].extend(s["procs"])
        d["worktrees"].append(s["worktree"])


def _slurp(path):
    try:
        with open(path, "rb") as f:
            return f.read()
    except FileNotFoundError:
        return None


# --- attribution (pure: the self-test drives it) -------------------------------

def session_state(s, table):
    """OWNED states and STALE: what the ledger row says, with D-025's process
    fence for an open row (a registered pid alive under its recorded start)."""
    if s["ended"] is not None:
        return "STALE", "ended"
    if not s["procs"]:
        return "OWNED", "open, no process registered"
    for pid, born in s["procs"]:
        row = table.get(pid)
        if row is not None and (born == 0 or row["born"] == born):
            return "OWNED", "live"
    # Open row, every registered process gone: a crash nobody said bye for.
    # Not STALE: ending a session that never ended is the operator's (D-090).
    return "UNKNOWN", "open, its process gone"


def contains(scope, path):
    return scope == path or path.startswith(scope.rstrip("/") + "/")  # invariant 14's rule


def attribute(proc, sessions, table, now):
    """(verdict, evidence, session words) for one orphan root.
    Evidence order: env (the session whose shell ran it), then a scratchpad
    cwd (the session that owns the directory), then a worktree cwd (every
    session registered in the deepest checkout containing it)."""
    ids, evidence = [], ""
    if proc["env_ids"]:
        ids, evidence = proc["env_ids"], "env"
    elif proc["cwd"]:
        m = SCRATCH.match(proc["cwd"])
        if m:
            ids, evidence = [m.group(1).lower()], "scratchpad"
    if ids:
        known = [i for i in ids if i in sessions]
        if not known:
            return "UNKNOWN", evidence, "%s not in the ledgers read" % short(ids[0])
        states = [(session_state(sessions[i], table), i) for i in known]
        verdict, ev, who = pick(states, sessions, now, evidence)
        unknown = [i for i in ids if i not in sessions]
        if verdict == "STALE" and unknown:
            # An id in no ledger read is a session nobody here can say ended
            # (Codex code pass): not STALE.
            return "UNKNOWN", evidence, "%s not in the ledgers read +%d" % (short(unknown[0]), len(ids) - 1)
        return verdict, ev, who
    if proc["cwd"]:
        best, hits = -1, []
        for sid, s in sessions.items():
            for wt in s.get("worktrees") or [s["worktree"]]:
                if wt and contains(wt, proc["cwd"]):
                    if len(wt) > best:
                        best, hits = len(wt), [sid]
                    elif len(wt) == best and sid not in hits:
                        hits.append(sid)
        if hits:
            states = [(session_state(sessions[i], table), i) for i in hits]
            return pick(states, sessions, now, "worktree(%d)" % len(hits))
    why = {"withheld": "env withheld", "unreadable": "env unreadable"}.get(proc["env_state"], "")
    if proc["cwd"] is None:
        why = (why + ", " if why else "") + "cwd unreadable"
    return "UNKNOWN", "-", "no tie" + (" (%s)" % why if why else "")


def pick(states, sessions, now, evidence):
    """One verdict for every session tied: OWNED if any is, then UNKNOWN,
    STALE only when all are; the session words name the one that decided."""
    for want in ("OWNED", "UNKNOWN", "STALE"):
        for (verdict, words), sid in states:
            if verdict == want:
                s = sessions[sid]
                if s["ended"] is not None:
                    words = "ended %s ago" % age(now - s["ended"] * 1000000)
                more = " +%d" % (len(states) - 1) if len(states) > 1 else ""
                return verdict, evidence, "%s %s %s%s" % (fold(s["label"], 64), short(sid), words, more)
    return "UNKNOWN", evidence, "no tie"


def short(sid):
    # Folded: a ledger's session id is the caller's (`hello --session`), and
    # one carrying a newline printed a second row (Codex code pass, P1).
    return fold("s-" + sid.replace("-", "")[:8], 16)


SAFE = re.compile(r"[^A-Za-z0-9._/@:+-]")


def fold(v, cap):
    """One token, one column (invariant 9): anything outside a safe set shows
    as '?', so no label or name can add a line or a column."""
    if isinstance(v, bytes):
        v = v.decode("utf-8", "replace")
    v = SAFE.sub("?", v or "") or "?"
    return v if len(v) <= cap else v[:cap - 3] + "..."


def age(us):
    s = max(0, int(us // 1000000))
    if s < 60:
        return "%ds" % s
    if s < 3600:
        return "%dm" % (s // 60)
    if s < 86400:
        return "%dh%02dm" % (s // 3600, s % 3600 // 60)
    return "%dd%02dh" % (s // 86400, s % 86400 // 3600)


def mb(b):
    return "%.1fM" % (b / 1048576.0)


# --- the census ----------------------------------------------------------------

def census(k, me):
    """(table, roots, counts). table: pid -> row for every process of `me`
    the kernel answered for; roots: the orphan roots."""
    pids = k.pids()
    if pids is None:
        return None
    launchd = k.idversions(1)
    if launchd is None:
        return None
    launchd_version = launchd[0]
    table, unread = {}, 0
    for pid in pids:
        row = k.bsd(pid)
        if row is None:
            unread += 1
            continue
        if row["uid"] != me:
            continue
        row["pid"] = pid
        table[pid] = row
    counts = {"unread": unread, "mine": len(table), "ppid1": 0, "launchd": 0, "noversion": 0}
    roots = []
    for pid, row in table.items():
        if row["ppid"] != 1 or row["status"] == SZOMB:
            continue
        counts["ppid1"] += 1
        v = k.idversions(pid)
        if v is None:
            counts["noversion"] += 1
            continue
        if v[1] == launchd_version:
            counts["launchd"] += 1
            continue
        state, ids = k.env_ids(pid)
        cwd = k.cwd(pid)
        # Re-identify: the pid must still be the process first read (D-065's
        # anchor rule), or its env and cwd describe some other process.
        again = k.bsd(pid)
        if again is None or again["born"] != row["born"]:
            continue
        row.update(env_state=state, env_ids=ids, cwd=cwd, rss=k.rss(pid))
        roots.append(row)
    kids = {}
    for pid, row in table.items():
        kids.setdefault(row["ppid"], []).append(pid)
    for r in roots:
        seen, todo, n, tree_rss = {r["pid"]}, list(kids.get(r["pid"], [])), 1, r["rss"]
        while todo:
            p = todo.pop()
            if p in seen:
                continue
            seen.add(p)
            n += 1
            tree_rss += k.rss(p)
            todo.extend(kids.get(p, []))
        r["tree"], r["tree_rss"] = n, tree_rss
    return table, roots, counts


# A row is about 110 bytes and the report lands in a coordinator's context
# (D-010): the incident's 202 leftovers would be 22 KB. Past the cap the rows
# are counted, never dropped silently, and the summary still totals them all.
ROW_CAP = 50


def render(roots, counts, ledgers, sessions, table, now, me, cap=ROW_CAP):
    out = ["leftover-report: uid %d, %d process(es) of this user read; %d of the table not read "
           "(another user's, or the kernel refused)" % (me, counts["mine"], counts["unread"])]
    out.append("  ppid 1: %d; %d started by launchd (left alone: launchd's services, apps and agents); "
               "%d reparented to launchd (orphan roots, below)%s"
               % (counts["ppid1"], counts["launchd"], len(roots),
                  "; %d whose origin the kernel did not say" % counts["noversion"] if counts["noversion"] else ""))
    for repo, words in ledgers:
        out.append("  ledger %s: %s" % (fold(repo, 120), words))
    rows = []
    for r in roots:
        verdict, ev, who = attribute(r, sessions, table, now)
        rows.append((("OWNED", "STALE", "UNKNOWN").index(verdict), -(now - r["born"]), r, verdict, ev, who))
    rows.sort(key=lambda x: (x[0], x[1], x[2]["pid"]))
    if rows:
        out.append("%-7s %-5s %-7s %-7s %-5s %-8s %-16s %-8s %-13s %s"
                   % ("PID", "PPID", "AGE", "RSS", "TREE", "TREE_RSS", "NAME", "VERDICT", "EVIDENCE", "SESSION"))
    tot = {"OWNED": [0, 0], "STALE": [0, 0], "UNKNOWN": [0, 0]}
    for n, (_, _, r, verdict, ev, who) in enumerate(rows):
        tot[verdict][0] += 1
        tot[verdict][1] += r["tree_rss"]
        if cap is not None and n >= cap:
            continue
        out.append("%-7d %-5d %-7s %-7s %-5d %-8s %-16s %-8s %-13s %s"
                   % (r["pid"], r["ppid"], age(now - r["born"]), mb(r["rss"]), r["tree"], mb(r["tree_rss"]),
                      fold(r["comm"], 16), verdict, ev, who))
    if cap is not None and len(rows) > cap:
        out.append("...and %d more row(s) not shown (--all lists every one)" % (len(rows) - cap))
    out.append("summary: OWNED %d (%s), STALE %d (%s), UNKNOWN %d (%s), trees included. "
               "This report ends nothing; a verdict is not permission."
               % (tot["OWNED"][0], mb(tot["OWNED"][1]), tot["STALE"][0], mb(tot["STALE"][1]),
                  tot["UNKNOWN"][0], mb(tot["UNKNOWN"][1])))
    return out


USAGE = "usage: sh scripts/leftover-report.sh [--all] [<repo> ...]   (python3 -I scripts/leftover-report.py --self-test)"


def main(argv):
    if argv[:1] in (["--help"], ["-h"]):
        print(USAGE)
        print("Lists this user's processes reparented to launchd, each tied to a buddy session where the")
        print("kernel lets it (env, scratchpad cwd, worktree cwd): pid, age, RSS, never argv. Ends nothing.")
        return 0
    if argv[:1] == ["--self-test"]:
        if len(argv) != 1:
            print(USAGE + "\nrefused: --self-test takes nothing else", file=sys.stderr)
            return 1
        return self_test()
    show_all = "--all" in argv
    argv = [a for a in argv if a != "--all"]
    for a in argv:
        if a.startswith("-"):
            print(USAGE + "\nrefused: unknown flag %s" % fold(a, 64), file=sys.stderr)
            return 1
    if sys.platform != "darwin":
        print("leftover-report: CANNOT SAY: only darwin's process table is read (not run here)")
        return 2
    repos = argv or ["."]
    ledgers, sessions = [], {}
    for repo in repos:
        words, got = read_ledger(os.path.abspath(repo))
        ledgers.append((os.path.abspath(repo), words))
        merge_sessions(sessions, got or {})
    k = Kernel()
    me = os.getuid()
    res = census(k, me)
    if res is None:
        print("leftover-report: CANNOT SAY: the kernel did not list processes or launchd's id version")
        return 2
    table, roots, counts = res
    now = int(time.time() * 1000000)
    print("\n".join(render(roots, counts, ledgers, sessions, table, now, me, None if show_all else ROW_CAP)))
    return 0


# --- self-test: no native binary, any platform ---------------------------------

def self_test():
    fails = []

    def want(name, got, exp):
        if got != exp:
            fails.append("%s: got %r, want %r" % (name, got, exp))

    def blob(argv, env, apple=(b"executable_path=/x",)):
        b = struct.pack("i", len(argv)) + b"/bin/x\0\0\0\0"
        b += b"".join(a + b"\0" for a in argv) + b"".join(e + b"\0" for e in env) + b"\0"
        return b + b"".join(a + b"\0" for a in apple)

    sid_a = "0a4422d1-1db6-4d3a-a7e4-268c4fce9d5a"
    # argv carries a lookalike: it must never be read as the environment.
    want("argv lookalike", session_ids_from_procargs(
        blob([b"x", b"CLAUDE_CODE_SESSION_ID=argvonly"], [b"HOME=/h", b"CLAUDE_CODE_SESSION_ID=" + sid_a.encode()])),
        ("read", [sid_a]))
    # control: the same blob with argc off by one reads the lookalike, so the
    # argc walk is what kept it out.
    b = blob([b"x", b"CLAUDE_CODE_SESSION_ID=argvonly"], [b"HOME=/h"])
    want("argc control", session_ids_from_procargs(struct.pack("i", 1) + b[4:]), ("read", ["argvonly"]))
    want("withheld", session_ids_from_procargs(blob([b"sh", b"-c", b"sleep 1"], [])), ("withheld", []))
    want("apple strings not env", session_ids_from_procargs(
        blob([b"x"], [b"A=1"], apple=(b"CODEX_THREAD_ID=apple",))), ("read", []))
    want("codex id", session_ids_from_procargs(blob([b"x"], [b"CODEX_THREAD_ID=t-1"])), ("read", ["t-1"]))
    want("bad id shape", session_ids_from_procargs(blob([b"x"], [b"CLAUDE_CODE_SESSION_ID=a\nb"])), ("read", []))
    want("empty", session_ids_from_procargs(b""), ("unreadable", []))
    want("truncated", session_ids_from_procargs(struct.pack("i", 3) + b"/bin/x\0a\0"), ("unreadable", []))

    now = 100 * 86400 * 1000000
    hr = 3600 * 1000000
    sessions = {
        sid_a: {"label": "lane-a", "worktree": "/w/repo-wt-a", "ended": (now - 2 * hr) // 1000000, "procs": [(50, 5)]},
        "live-1": {"label": "lane\nlive", "worktree": "/w/repo", "ended": None, "procs": [(60, 6)]},
        "gone-1": {"label": "lane-gone", "worktree": "/w/repo-wt-g", "ended": None, "procs": [(70, 7)]},
        "end-2": {"label": "old", "worktree": "/w/repo", "ended": (now - hr) // 1000000, "procs": []},
        "codex-1": {"label": "cx", "worktree": "/w/repo-wt-c", "ended": None, "procs": []},
    }
    table = {60: {"born": 6}, 70: {"born": 999}}   # 70 reused: not the registered process

    def proc(env_ids=(), env_state="read", cwd="/"):
        return {"env_ids": list(env_ids), "env_state": env_state, "cwd": cwd}

    def att(p):
        v, e, w = attribute(p, sessions, table, now)
        return v, e, w.split()[0] if w else ""

    want("env ended", att(proc([sid_a])), ("STALE", "env", "lane-a"))
    want("env ended words", attribute(proc([sid_a]), sessions, table, now)[2], "lane-a s-0a4422d1 ended 2h00m ago")
    want("env live", att(proc(["live-1"])), ("OWNED", "env", "lane?live"))
    want("env gone pid reused", att(proc(["gone-1"])), ("UNKNOWN", "env", "lane-gone"))
    want("env unregistered proc", att(proc(["codex-1"])), ("OWNED", "env", "cx"))
    want("env not in ledger", attribute(proc(["feedface-0000"]), sessions, table, now),
         ("UNKNOWN", "env", "s-feedface not in the ledgers read"))
    want("env beats cwd", att(proc(["live-1"], cwd="/private/tmp/claude-501/p/%s/scratchpad" % sid_a)),
         ("OWNED", "env", "lane?live"))
    want("scratchpad", att(proc(env_state="withheld", cwd="/private/tmp/claude-501/p/%s/scratchpad/m" % sid_a.upper())),
         ("STALE", "scratchpad", "lane-a"))
    want("scratchpad /tmp", att(proc(env_state="withheld", cwd="/tmp/claude-501/p/%s" % sid_a)),
         ("STALE", "scratchpad", "lane-a"))
    want("not a scratchpad", att(proc(env_state="withheld", cwd="/private/tmp/claude-501/p/%sx" % sid_a)),
         ("UNKNOWN", "-", "no"))
    want("worktree one ended", att(proc(env_state="withheld", cwd="/w/repo-wt-a/sub")), ("STALE", "worktree(1)", "lane-a"))
    want("worktree shared, one live", att(proc(cwd="/w/repo/x")), ("OWNED", "worktree(2)", "lane?live"))
    want("worktree prefix is not containment", att(proc(cwd="/w/repo-wt-ab")), ("UNKNOWN", "-", "no"))
    want("worktree deepest wins", att(proc(cwd="/w/repo-wt-g")), ("UNKNOWN", "worktree(1)", "lane-gone"))
    want("no tie words", attribute(proc(env_state="withheld", cwd=None), sessions, table, now)[2],
         "no tie (env withheld, cwd unreadable)")

    want("ended beside unknown", attribute(proc([sid_a, "feedface-1"]), sessions, table, now),
         ("UNKNOWN", "env", "s-feedface not in the ledgers read +1"))
    want("ended beside ended", att(proc([sid_a, "end-2"])), ("STALE", "env", "lane-a"))
    nl = {"x\nSTALE": {"label": "lane", "worktree": "/n", "ended": 1, "procs": []}}
    want("ledger id folded", attribute(proc(cwd="/n/a"), nl, table, now)[2].split(" ")[1], "s-x?STALE")
    m1 = {}
    merge_sessions(m1, {"s": {"label": "a", "worktree": "/a", "ended": 5, "procs": []}})
    merge_sessions(m1, {"s": {"label": "b", "worktree": "/b", "ended": None, "procs": [(60, 6)]}})
    m2 = {}
    merge_sessions(m2, {"s": {"label": "b", "worktree": "/b", "ended": None, "procs": [(60, 6)]}})
    merge_sessions(m2, {"s": {"label": "a", "worktree": "/a", "ended": 5, "procs": []}})
    want("merge order", [attribute(proc(["s"]), m, table, now)[0] for m in (m1, m2)], ["OWNED", "OWNED"])
    want("merge worktrees", attribute(proc(cwd="/a/x"), m2, table, now)[:2], ("OWNED", "worktree(1)"))
    m3 = {}
    for e in (5, 9):
        merge_sessions(m3, {"s": {"label": "a", "worktree": "/a", "ended": e, "procs": []}})
    want("merge ended latest", m3["s"]["ended"], 9)

    want("fold newline", fold("a\nb c", 64), "a?b?c")
    want("fold cap", fold("x" * 70, 64), "x" * 61 + "...")
    want("fold empty", fold(b"", 16), "?")
    want("age", [age(5e6), age(125e6), age(3 * hr + 60e6), age(26 * hr)], ["5s", "2m", "3h01m", "1d02h"])
    want("contains", [contains("/a", "/a"), contains("/a", "/a/b"), contains("/a", "/ab"), contains("/a/", "/a/b")],
         [True, True, False, True])

    roots = [dict(pid=11, ppid=1, born=now - hr, comm=b"gpg-agent", rss=1048576, tree=2, tree_rss=2097152,
                  **proc([sid_a])),
             dict(pid=12, ppid=1, born=now - 2 * hr, comm=b"evil\nname", rss=0, tree=1, tree_rss=0,
                  **proc(["live-1"]))]
    lines = render(roots, {"unread": 3, "mine": 9, "ppid1": 4, "launchd": 2, "noversion": 0},
                   [("/w/repo", "read, 5 session(s)")], sessions, table, now, 501)
    body = [line for line in lines if line[:2] in ("11", "12")]
    want("render rows", [line.split()[:9] for line in body],
         [["12", "1", "2h00m", "0.0M", "1", "0.0M", "evil?name", "OWNED", "env"],
          ["11", "1", "1h00m", "1.0M", "2", "2.0M", "gpg-agent", "STALE", "env"]])
    want("render one line each", len(lines), 7)
    want("render summary", lines[-1].split(". ")[0],
         "summary: OWNED 1 (0.0M), STALE 1 (2.0M), UNKNOWN 0 (0.0M), trees included")
    capped = render(roots, {"unread": 3, "mine": 9, "ppid1": 4, "launchd": 2, "noversion": 0},
                    [], sessions, table, now, 501, cap=1)
    want("cap rows", [line.split()[0] for line in capped[3:5]], ["12", "...and"])
    want("cap counts", capped[4], "...and 1 more row(s) not shown (--all lists every one)")
    want("cap summary totals all", capped[-1].split(". ")[0], lines[-1].split(". ")[0])

    for f in fails:
        print("leftover-report self-test FAIL: " + f, file=sys.stderr)
    if fails:
        return 1
    print("leftover-report self-test: ok")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
