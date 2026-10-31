#!/usr/bin/env python3
# subagent-report.py — the reader behind scripts/subagent-report.sh, which
# carries the report's contract (what each field counts, what it misses, the
# knobs). Run it through that script: `python3 -I`, so neither PYTHON* nor a
# user site-packages decides what this imports.
#
# WHY PYTHON, where the other reports are sh + jq + awk. Two of its fields are
# joins across files: an Agent call in the main transcript names the agent id
# its subagent ran under, and the model that subagent ran on is in another
# file (`<session>/subagents/agent-<id>.jsonl`); a landing read is matched
# against lane names gathered from every other transcript's cwd. And the
# classifier's regex is the plan's measurement draft's (a Python `re`), so the
# report reproduces section 0 of docs/CHEAPER-MODELS-PLAN.md with the engine
# that measured it: Oniguruma's `\b` and case folding are not Python's.
#
# PRIVACY, by construction. Nothing read from a transcript reaches stdout or
# stderr except numbers and values that pass a closed charset check (session
# and tool-use ids, model strings, subagent types) or a fixed list (buddy
# verbs). A value that fails the check prints as `(other)`, never as itself.
# A description, prompt, command, path or tool result is read to decide a word
# or a byte count and is then dropped. No exception message is printed: a
# UnicodeDecodeError names the byte it choked on and a KeyError its key, so
# a file that cannot be read is counted, and an internal error prints only
# its exception's type. check-subagent-report.sh plants canaries to hold it.
import calendar
import glob
import json
import os
import re
import sys
import time

USAGE = "usage: subagent-report.sh [--since YYYY-MM-DD[THH:MM]] [--until YYYY-MM-DD[THH:MM]]"

# Ids, by their exact shape, never a charset alone: a file named
# `SOME_WORDS.jsonl` passes a charset and would print as a session id (Codex).
# A session id is the transcript's file name, a UUID; a tool-use id is the
# API's. The agent id is only joined, never printed, and builds a path: its
# charset is what keeps `../` out of it.
SESSION_ID = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\Z")
TOOL_ID = re.compile(r"toolu_[A-Za-z0-9_-]{1,64}\Z")
SAFE_ID = re.compile(r"[A-Za-z0-9_-]{1,64}\Z")
# Model strings and subagent types: `claude-opus-5-5`, `general-purpose`,
# `claude-code-guide`, a user agent's name.
SAFE_NAME = re.compile(r"[A-Za-z0-9._:-]{1,64}\Z")
# A lane's branch, matched and never printed: `feature/topic` is a branch too.
BRANCH = re.compile(r"[A-Za-z0-9._/-]{1,128}\Z")

# The measurement draft's classifier, plus `ruling`: the plan's text lists it
# and the draft missed one ruling (section 0: hand count 89 reviews, regex 88).
REVIEW = re.compile(
    r"\b(review|panel|lens|cross-?check|verify|adversarial|audit|second opinion"
    r"|critique|reviewer|find (bugs|defects)|read-only|rulings?)\b", re.I)

# A buddy verb in command position: line start, after ; & | ( or $( or a
# backtick, behind `env X=y`. The draft's match, plus a newline as a separator:
# a heredoc'd script's second line is a call too.
BUDDY = re.compile(r"(?:^|[\n;&|(]\s*|\$\(|`)\s*(?:env\s+\S+\s+)?buddy\s+([a-z][a-z-]*)")
# The verbs `buddy` dispatches (internal/cli/cli.go's table; the done-check
# compares this list with it). A word not in it prints as `(other)`: the
# captured word is command text, and printing it would let a command choose
# what the report says.
VERBS = frozenset(
    "init hello bye beat idle busy gate commit-gate claim release ls sweep pause"
    " resume msg sent inbox sessions whose status who authority ids wait lifecycle"
    " adapter diagnose update uninstall".split())

# A lane launch, in command position, as wake-report.sh matches `buddy wait`:
# a commit message or a grep pattern that NAMES the command is not a launch.
# Both are matched with every single-quoted span blanked: nothing inside one
# runs, and `printf '%s' '; herdr agent start x'` was a launch (Codex). A
# double-quoted span is left alone, since `"$(buddy who x)"` does run; a `;`
# inside one still reads as a separator (OPEN, Codex).
UNQUOTE = re.compile(r"'[^']*'")
HERDR = re.compile(
    r"(?:^|[\n;&|(])[ \t]*(?:[A-Za-z_][A-Za-z0-9_]*=[^ \t\n]*[ \t]+)*"
    r"(?:[^ \t\n;&|()]*/)?herdr[ \t]+agent[ \t]+start(?![A-Za-z0-9_-])")

# A harness-made worktree, `<main>/.claude/worktrees/<name>`.
WORKTREE = re.compile(r"/\.claude/worktrees/([A-Za-z0-9._-]+)")
NOT_LANES = frozenset(["main", "master", "HEAD"])
# "Over 10 KB": 10,240 bytes of UTF-8 result text.
BIG = 10 * 1024
TS = re.compile(r"(\d{4})-(\d\d)-(\d\d)T(\d\d):(\d\d):(\d\d)")
WHEN = re.compile(r"(\d{4})-(\d\d)-(\d\d)(?:T(\d\d):(\d\d))?\Z")


def safe(v, pat=SAFE_NAME, none="(none)"):
    if v is None:
        return none
    if isinstance(v, str) and pat.match(v):
        return v
    return "(other)"


def epoch(ts):
    """A transcript timestamp (UTC, `...Z`) to seconds, or None."""
    if not isinstance(ts, str):
        return None
    m = TS.match(ts)
    if not m:
        return None
    try:
        return calendar.timegm(tuple(int(x) for x in m.groups()) + (0, 0, 0))
    except (ValueError, OverflowError):
        return None


def local_when(s):
    """--since/--until: a LOCAL date or date and minute, to seconds."""
    m = WHEN.match(s)
    if not m:
        return None
    y, mo, d, h, mi = (int(x) if x else 0 for x in m.groups())
    try:
        t = time.mktime((y, mo, d, h, mi, 0, 0, 0, -1))
    except (ValueError, OverflowError):
        return None
    # mktime normalizes 2026-02-31 to March; a date that does not round-trip
    # is refused rather than read as some other day.
    if time.localtime(t)[:5] != (y, mo, d, h, mi):
        return None
    return t


def mtime(path):
    """A file's mtime; one removed since it was listed (a session's transcript
    can be deleted mid-run) reads as never modified, not as a crash."""
    try:
        return os.path.getmtime(path)
    except OSError:
        return float("-inf")


def nbytes(content):
    """UTF-8 bytes of a tool result's text (a string, or a list of text blocks)."""
    if isinstance(content, str):
        return len(content.encode("utf-8", "surrogatepass"))
    n = 0
    if isinstance(content, list):
        for x in content:
            if isinstance(x, dict) and isinstance(x.get("text"), str):
                n += len(x["text"].encode("utf-8", "surrogatepass"))
    return n


class Stats:
    def __init__(self):
        self.malformed = 0      # lines that are not a JSON object
        self.unreadable = 0     # files that could not be opened or read


def records(path, st):
    """Every JSON-object line of a transcript; a torn line costs that line."""
    try:
        with open(path, "rb") as f:
            for line in f:
                if not line.strip():
                    continue
                try:
                    o = json.loads(line)
                except ValueError:  # JSONDecodeError and UnicodeDecodeError both
                    st.malformed += 1
                    continue
                if isinstance(o, dict):
                    yield o
                else:
                    st.malformed += 1
    except OSError:
        st.unreadable += 1


def blocks(o, kind):
    m = o.get("message")
    c = m.get("content") if isinstance(m, dict) else None
    if not isinstance(c, list):
        return []
    return [b for b in c if isinstance(b, dict) and b.get("type") == kind]


def lane_of(cwd):
    m = WORKTREE.search(cwd) if isinstance(cwd, str) else None
    return m.group(1) if m else None


class Sub:
    """One subagent transcript: its model and first request's time."""

    def __init__(self, path, st, since, until, seen):
        self.model = "(no request)"
        self.t = None
        self.nested = 0
        got = False
        for o in records(path, st):
            if o.get("type") != "assistant":
                continue
            m = o.get("message")
            if not isinstance(m, dict) or m.get("model") == "<synthetic>":
                continue
            t = epoch(o.get("timestamp"))
            if not got:
                got = True
                self.model = safe(m.get("model"))
                self.t = t
            # An Agent call a subagent made itself: counted apart, never
            # attributed to the main session that spawned the subagent.
            for b in blocks(o, "tool_use"):
                if b.get("name") == "Agent" and t is not None and since <= t < until:
                    i = b.get("id")
                    if isinstance(i, str) and i not in seen:
                        seen.add(i)
                        self.nested += 1


def main(argv):
    since = time.time() - 7 * 86400
    until = time.time()
    until_given = False
    args = list(argv)
    while args:
        a = args.pop(0)
        if a in ("-h", "--help"):
            print(USAGE)
            return 0
        if a in ("--since", "--until") and args:
            v = args.pop(0)
            t = local_when(v)
            if t is None:
                print(USAGE, file=sys.stderr)
                print("refused: %s takes a local YYYY-MM-DD or YYYY-MM-DDTHH:MM" % a, file=sys.stderr)
                return 2
            if a == "--since":
                since = t
            else:
                until, until_given = t, True
            continue
        print(USAGE, file=sys.stderr)
        print("refused: an argument this report does not take", file=sys.stderr)
        return 2
    if until <= since:
        print(USAGE, file=sys.stderr)
        print("refused: --until is not after --since", file=sys.stderr)
        return 2

    root = os.environ.get("CLAUDE_PROJECTS_DIR") or os.path.expanduser("~/.claude/projects")
    st = Stats()
    files = sorted(
        f for f in glob.glob(os.path.join(glob.escape(root), "*", "*.jsonl"))
        if os.path.isfile(f) and mtime(f) >= since)

    # Pass 1: lane names, from the cwd and branch every session first records.
    # A session in a harness worktree is a lane; its worktree name and its
    # branch (when not main) are what an orchestrator's landing reads name.
    # Both map to ONE lane, its worktree's name: a read naming a lane's
    # worktree and its branch is one lane read, not two (Codex).
    lanes = {}          # worktree or branch name -> the lane (worktree name)
    own = {}
    for f in files:
        for o in records(f, Stats()):
            cwd = o.get("cwd")
            if not isinstance(cwd, str):
                continue
            n = lane_of(cwd)
            if n and n not in NOT_LANES:
                own[f] = n
                lanes[n] = n
                b = o.get("gitBranch")
                if isinstance(b, str) and b not in NOT_LANES and BRANCH.match(b):
                    lanes.setdefault(b, n)
            break
    lane_re = None
    if lanes:
        lane_re = re.compile(r"(?<![A-Za-z0-9_-])(" + "|".join(
            re.escape(n) for n in sorted(lanes, key=len, reverse=True)) + r")(?![A-Za-z0-9_-])")

    calls = {}          # tool-use id -> call row (main thread, in the window)
    seen_use = set()    # every tool-use id met, across files: a copy is one call
    seen_res = set()
    sessions = {}       # file -> session id as printed
    orc = {}            # session id -> [launches, prompt at first, reads, bytes, lanes]
    reads = {}          # session id -> [reads, bytes, lanes]
    bash_n = bash_b = 0
    verbs = {}
    subs = {}           # subagent path -> Sub
    nested_seen = set()

    def sub(path):
        if path not in subs:
            subs[path] = Sub(path, st, since, until, nested_seen)
        return subs[path]

    for f in files:
        sid = safe(os.path.basename(f)[:-len(".jsonl")], SESSION_ID, "(other)")
        sessions[f] = sid
        mine = own.get(f)
        uses = {}
        for o in records(f, st):
            if o.get("isSidechain") is True:
                continue
            kind = o.get("type")
            if kind == "assistant":
                m = o.get("message")
                if not isinstance(m, dict) or m.get("model") == "<synthetic>":
                    continue
                t = epoch(o.get("timestamp"))
                inwin = t is not None and since <= t < until
                for b in blocks(o, "tool_use"):
                    i = b.get("id")
                    if not isinstance(i, str):
                        continue
                    name = b.get("name")
                    inp = b.get("input") if isinstance(b.get("input"), dict) else {}
                    # A copy in a later file is not counted again, but is still
                    # known there: the result may be in the copy's file only,
                    # and dropping it read as `(no result)` (Codex).
                    if i not in uses:
                        uses[i] = (name, inp, inwin)
                    if i in seen_use or not inwin:
                        seen_use.add(i)
                        continue
                    seen_use.add(i)
                    if name == "Agent":
                        d = inp.get("description")
                        if not isinstance(d, str) or not d.strip():
                            word = "unclassified"
                        else:
                            p = inp.get("prompt") if isinstance(inp.get("prompt"), str) else ""
                            word = "review" if REVIEW.search(d + " " + p[:400]) else "other"
                        calls[i] = {
                            "sid": sid, "dir": f[:-len(".jsonl")], "id": safe(i, TOOL_ID, "(other)"),
                            "param": safe(inp.get("model")), "type": safe(inp.get("subagent_type")),
                            "word": word, "parent": safe(m.get("model")), "ran": "(no result)"}
                    elif name == "Bash":
                        cmd = inp.get("command")
                        if isinstance(cmd, str) and HERDR.search(UNQUOTE.sub("''", cmd)):
                            r = orc.setdefault(sid, [0, None])
                            r[0] += 1
                            if r[1] is None:
                                u = m.get("usage") if isinstance(m.get("usage"), dict) else {}
                                tok = 0
                                for k in ("input_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"):
                                    v = u.get(k)
                                    if isinstance(v, int) and not isinstance(v, bool):
                                        tok += v
                                r[1] = tok
            elif kind == "user":
                res = blocks(o, "tool_result")
                for b in res:
                    i = b.get("tool_use_id")
                    if not isinstance(i, str) or i not in uses or i in seen_res:
                        continue
                    seen_res.add(i)
                    name, inp, inwin = uses[i]
                    if name == "Agent" and i in calls:
                        # The agent id rides the record's toolUseResult, which is
                        # one per record: with several results on one record it
                        # cannot be told whose it is, and is not guessed.
                        tr = o.get("toolUseResult")
                        a = tr.get("agentId") if isinstance(tr, dict) and len(res) == 1 else None
                        if not isinstance(a, str) or not SAFE_ID.match(a):
                            calls[i]["ran"] = "(no agent id)"
                        else:
                            # Under the session whose file holds the result, else
                            # the one that holds the call (a copied history).
                            ps = [os.path.join(d, "subagents", "agent-%s.jsonl" % a)
                                  for d in (f[:-len(".jsonl")], calls[i]["dir"])]
                            ps = [p for p in ps if os.path.isfile(p)]
                            calls[i]["ran"] = sub(ps[0]).model if ps else "(no transcript)"
                        continue
                    if not inwin or name not in ("Bash", "Read"):
                        continue
                    n = nbytes(b.get("content"))
                    named = set()
                    if name == "Bash":
                        cmd = inp.get("command") if isinstance(inp.get("command"), str) else ""
                        bash_n += 1
                        bash_b += n
                        mt = BUDDY.search(UNQUOTE.sub("''", cmd))
                        if mt:
                            v = mt.group(1) if mt.group(1) in VERBS else "(other)"
                            r = verbs.setdefault(v, [0, 0])
                            r[0] += 1
                            r[1] += n
                        named.update(WORKTREE.findall(cmd))
                        if lane_re:
                            named.update(x.group(1) for x in lane_re.finditer(cmd))
                    else:
                        fp = inp.get("file_path")
                        n0 = lane_of(fp)
                        if n0:
                            named.add(n0)
                    named = {lanes.get(x, x) for x in named} - {mine} - NOT_LANES
                    if named and n > BIG:
                        r = reads.setdefault(sid, [0, 0, set()])
                        r[0] += 1
                        r[1] += n
                        r[2].update(named)

    # Subagent transcripts by session: every agent-*.jsonl under a main
    # transcript read here whose first request is in the window, joined or not.
    by_sess = {}
    for f in files:
        for p in sorted(glob.glob(os.path.join(glob.escape(f[:-len(".jsonl")]), "subagents", "agent-*.jsonl"))):
            if not os.path.isfile(p):
                continue
            s = sub(p)
            if s.t is None or not (since <= s.t < until):
                continue
            row = by_sess.setdefault(sessions[f], {})
            row[s.model] = row.get(s.model, 0) + 1
    nested = sum(s.nested for s in subs.values())

    out = []
    w = out.append
    lo = time.strftime("%Y-%m-%d %H:%M", time.localtime(since))
    hi = "now" if not until_given else time.strftime("%Y-%m-%d %H:%M", time.localtime(until))
    w("Subagent report: %s to %s (local), %d main transcript(s) modified in the window" % (lo, hi, len(files)))
    w("Counts, ids and byte lengths only: no description, prompt, command, path or result text.")
    if st.malformed or st.unreadable:
        w("skipped: %d line(s) not a JSON object, %d file(s) unreadable" % (st.malformed, st.unreadable))

    rows = list(calls.values())
    w("")
    w("AGENT CALLS  %d on main threads in %d session(s); %d made inside subagent transcripts (in no table below)"
      % (len(rows), len({r["sid"] for r in rows}), nested))
    words = ("review", "other", "unclassified")
    w("%-26s %7s %7s %13s %7s" % ("by model param", "review", "other", "unclassified", "total"))

    def line(label, sel):
        c = [sum(1 for r in sel if r["word"] == x) for x in words]
        w("%-26s %7d %7d %13d %7d" % (label, c[0], c[1], c[2], sum(c)))

    for p in sorted({r["param"] for r in rows}, key=lambda p: (p == "(none)", p)):
        line(p, [r for r in rows if r["param"] == p])
    unnamed = [r for r in rows if r["param"] == "(none)"]
    for t in sorted({r["type"] for r in unnamed}):
        line("  (none), " + t, [r for r in unnamed if r["type"] == t])
    line("total", rows)

    w("")
    w("every call, grouped: model param / parent model / subagent type / word / ran on (its subagent transcript's model)")
    g = {}
    for r in rows:
        k = (r["param"], r["parent"], r["type"], r["word"], r["ran"])
        g[k] = g.get(k, 0) + 1
    for k in sorted(g, key=lambda k: (-g[k], k)):
        w("  %4d  %s" % (g[k], "  ".join(k)))

    gp = [r for r in unnamed if r["type"] in ("general-purpose", "(none)")]
    w("")
    w("unnamed general-purpose calls (no model param; type general-purpose or omitted, the tool's default): %d" % len(gp))
    w("  review-shaped: %d, of which ran (a subagent transcript joined): %d"
      % (sum(1 for r in gp if r["word"] == "review"),
         sum(1 for r in gp if r["word"] == "review" and not r["ran"].startswith("("))))
    ran = {}
    for r in gp:
        ran[r["ran"]] = ran.get(r["ran"], 0) + 1
    w("  by what they ran on:")
    for k in sorted(ran, key=lambda k: (-ran[k], k)):
        w("  %4d  %s" % (ran[k], k))

    un = [r for r in rows if r["word"] == "unclassified"]
    w("")
    w("calls with no description (unclassified; listed, not guessed): %d" % len(un))
    for r in sorted(un, key=lambda r: (r["sid"], r["id"])):
        w("  %s  %s" % (r["sid"], r["id"]))

    w("")
    w("SUBAGENT TRANSCRIPTS by session and model (first request in the window): %d in %d session(s)"
      % (sum(sum(v.values()) for v in by_sess.values()), len(by_sess)))
    for s in sorted(by_sess, key=lambda s: (-sum(by_sess[s].values()), s)):
        w("  %s  %s" % (s, " ".join("%s=%d" % (m, by_sess[s][m]) for m in sorted(by_sess[s]))))

    w("")
    w("ORCHESTRATORS (sessions that ran `herdr agent start` in the window): %d; %d lane(s) known"
      % (len(orc), len(set(lanes.values()))))
    w("  %-36s %8s %15s %13s %12s %6s" % ("session", "launches", "prompt@1st", "landing reads", "bytes", "lanes"))
    for s in sorted(orc):
        r = reads.get(s, [0, 0, set()])
        w("  %-36s %8d %15s %13d %12d %6d" % (
            s, orc[s][0], "-" if orc[s][1] is None else "%d" % orc[s][1], r[0], r[1], len(r[2])))
    w("  (prompt@1st: input + cache-read + cache-write tokens of the request carrying its first launch;")
    w("   a landing read: a Read or Bash result over %d bytes whose path or command names another lane's" % BIG)
    w("   worktree or branch; lanes: how many distinct lanes those reads named)")

    w("")
    bb = sum(v[1] for v in verbs.values())
    bn = sum(v[0] for v in verbs.values())
    w("BASH RESULTS  %d, %d bytes; with a buddy verb: %d, %d bytes, %.1f%% of all Bash result bytes"
      % (bash_n, bash_b, bn, bb, 100.0 * bb / bash_b if bash_b else 0.0))
    for v in sorted(verbs, key=lambda v: (-verbs[v][1], v)):
        w("  %-12s %6d calls %12d bytes" % (v, verbs[v][0], verbs[v][1]))

    sys.stdout.write("\n".join(out) + "\n")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except KeyboardInterrupt:
        sys.exit(130)
    except Exception as e:  # never the message: it can carry what was read
        print("subagent-report: internal error (%s)" % type(e).__name__, file=sys.stderr)
        sys.exit(1)
