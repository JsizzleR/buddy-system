#!/usr/bin/env python3
"""wire-hooks.py -- `install.sh --wire-hooks`: append the buddy hook entries the
install report names missing to the user's Claude Code settings file, and change
nothing else (D-135, amending D-050 and D-120).

    python3 -I -B scripts/wire-hooks.py check <settings.json>
    python3 -I -B scripts/wire-hooks.py wire <settings.json> <snapshot|-> [<token> ...]

`check` is install.sh's preflight, before step 1: every refusal below that the
file can earn now, writing nothing. `wire` is step 6: <snapshot> holds the bytes
install.sh's report grepped (`-`: there was no file), and each <token> is one the
report named missing (`buddy hello` ... `buddylist presence`, as one argument
each). The browser beats (#71) are read here, from the same bytes, by
gate-hook.py's browser_gone, the function the report prints from. Exit 0: wired
or nothing to add; 1: refused, nothing written (or written, durability unsure
or a temp left, said so); 2: usage.

WHY IT EXISTS. install.sh reported hook wiring and never edited settings (D-050):
"a script that rewrote it would be a script that could remove one". After #71
that left the operator pasting JSON by hand for every line the report named. The
operator's decision (2026-11-09) keeps the reason and narrows the rule: an
opt-in flag that can only ADD, and only what the report already names.

WHAT IT NEVER DOES. Remove, reorder or rewrite an existing entry; touch a key but
the hook arrays it appends to (or the `hooks` object / event key it creates when
absent); judge a present-but-different entry (a token the report's grep finds
anywhere, a legacy gate or a permissions rule included, is present: never added,
and only a gate missing as a hook is said, failing the wiring); write the Agent-model hook (D-120: its copy is the operator's opt-in, and
wired without the copy python3 exits 2, blocking every Agent call); read or
write project or managed settings.

HOW IT WRITES. The new text is the old text with insertions, never a
re-serialization: json.dumps of the whole file would respell CRLF, `"\\u0061"`
and `1e0` (Codex design pass), so a span-recording scan finds the insertion
points and each new entry goes in as its own element, after the last one. The
result must decode to the old document with exactly those entries appended,
and removing the insertions must give the old bytes back, or nothing is
written. A private (0600) backup is written and fsynced first; the new file is
a temp in the same directory, the original's mode, fsynced, renamed over it.
All or nothing: one rename, so no failure leaves the gate's entry less wired
than before (invariant 2, D-107).

REFUSED, naming the file, nothing written: a symlink (a rename would replace a
dotfiles link with a file; wire the target, BUDDY_SETTINGS=<target>), not a
regular file, a second hard link (a rename would split it), another user's or
a read-only file, over MAX_BYTES
before or after, not UTF-8, not JSON, a duplicate key, NaN or Infinity, a top
level that is not an object, a `hooks` that is not an object, an event that is
not a list or holds an entry that is not a hook object, `disableAllHooks` true, bytes that differ from the report's
snapshot (the report and the write read one version, Codex design pass), and
bytes that change between the last read and the rename's eve.

NOT COVERED, said in D-135: Claude Code (or anything) writing the file inside
the final read-to-rename window: that edit is lost, and the backup predates it
(no writer lock exists to take); a project's settings that wire a buddy line
spelled another way, which then runs beside the user-level one; a profile under
CLAUDE_CONFIG_DIR (the file written is the one the report reads,
BUDDY_SETTINGS, default ~/.claude/settings.json); whether Claude Code loads
the entries (workspace trust, a managed policy): written is not enforced.
"""

import copy
import importlib.util
import json
import os
import re
import signal
import stat
import sys
import tempfile
import time

MAX_BYTES = 1 << 20
HELD = {signal.SIGHUP, signal.SIGTERM, signal.SIGINT}
HERE = os.path.dirname(os.path.abspath(__file__))


def gate_hook():
    spec = importlib.util.spec_from_file_location("gate_hook", os.path.join(HERE, "gate-hook.py"))
    gh = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(gh)
    return gh


class Refused(Exception):
    pass


def _pairs(pairs):
    # A set, not keys.count per key: 40,000 keys took 9.9 s quadratically (panel).
    seen = set()
    for k, _ in pairs:
        if k in seen:
            raise Refused("a duplicate key %s (which one Claude Code reads is not known here)" % json.dumps(k))
        seen.add(k)
    return dict(pairs)


def _constant(name):
    raise Refused("a non-JSON constant %s" % name)


def decode(text):
    try:
        return json.loads(text, object_pairs_hook=_pairs, parse_constant=_constant)
    except RecursionError:
        raise Refused("nested too deep to read")
    except ValueError as e:
        raise Refused("not JSON (%s)" % e)


def shape(doc):
    if not isinstance(doc, dict):
        raise Refused("the top level is not an object")
    hooks = doc.get("hooks")
    if "hooks" in doc and not isinstance(hooks, dict):
        raise Refused('"hooks" is not an object')
    for ev, v in (hooks or {}).items():
        if not isinstance(v, list):
            raise Refused('"hooks".%s is not a list' % json.dumps(ev))
        # An entry Claude Code could not read either (not an object, its
        # hooks not objects, a command not a string): not appended beside.
        for e in v:
            hs = e.get("hooks") if isinstance(e, dict) else None
            if not isinstance(e, dict) or ("hooks" in e and not isinstance(hs, list)) or any(
                    not isinstance(h, dict) or ("command" in h and not isinstance(h["command"], str)) for h in hs or []):
                raise Refused('an entry of "hooks".%s that is not a hook object' % json.dumps(ev))
    if doc.get("disableAllHooks") is True:
        raise Refused('"disableAllHooks" is true: no hook would run; the operator set that, and it is theirs to undo')


def load(path):
    """(bytes, lstat) of a regular file, or (None, None) when there is none."""
    try:
        st = os.lstat(path)
    except FileNotFoundError:
        return None, None
    if stat.S_ISLNK(st.st_mode):
        raise Refused("a symlink (to %s): a rename would replace the link with a file; wire the target, "
                      "BUDDY_SETTINGS=<target>, or by hand" % json.dumps(os.readlink(path)))
    if not stat.S_ISREG(st.st_mode):
        raise Refused("not a regular file")
    if st.st_nlink > 1:
        raise Refused("one of %d hard links: a rename would split them" % st.st_nlink)
    # A rename replaces the file whatever its own bits say, so a file made
    # read-only on purpose (or another user's) would be rewritten (panel).
    if st.st_uid != os.geteuid():
        raise Refused("owned by uid %d, not this user: a rename would replace it with this user's file" % st.st_uid)
    if not st.st_mode & stat.S_IWUSR:
        raise Refused("not writable by its owner (mode %o): made read-only on purpose, it is left so" % stat.S_IMODE(st.st_mode))
    if st.st_size > MAX_BYTES:
        raise Refused("%d bytes, over %d" % (st.st_size, MAX_BYTES))
    fd = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    try:
        fst = os.fstat(fd)
        if (fst.st_dev, fst.st_ino) != (st.st_dev, st.st_ino):
            raise Refused("replaced while being read")
        chunks = []
        while True:
            b = os.read(fd, 65536)
            if not b:
                break
            chunks.append(b)
            if sum(map(len, chunks)) > MAX_BYTES:
                raise Refused("over %d bytes" % MAX_BYTES)
    finally:
        os.close(fd)
    return b"".join(chunks), st


def parse(raw):
    try:
        text = raw.decode("utf-8")
    except UnicodeDecodeError:
        raise Refused("not UTF-8")
    doc = decode(text)
    shape(doc)
    return text, doc


# --- a span-recording scan of strict JSON (decode() above has already accepted
# the text, so this only locates; it never decides validity) ---
_WS = re.compile(r"[ \t\n\r]*")
_LIT = re.compile(r"-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][-+]?\d+)?|true|false|null")


def _value(s, i):
    """(node, end) where node is ('obj', start, end, [(key, node)]),
    ('arr', start, end, [node]) or ('lit', start, end)."""
    i = _WS.match(s, i).end()
    c = s[i]
    if c == "{":
        members, j = [], _WS.match(s, i + 1).end()
        if s[j] == "}":
            return ("obj", i, j + 1, members), j + 1
        while True:
            key, j = json.decoder.scanstring(s, j + 1)
            j = _WS.match(s, j).end() + 1  # the ':'
            node, j = _value(s, j)
            members.append((key, node))
            j = _WS.match(s, j).end()
            if s[j] == "}":
                return ("obj", i, j + 1, members), j + 1
            j = _WS.match(s, j + 1).end()  # past ','
    if c == "[":
        items, j = [], _WS.match(s, i + 1).end()
        if s[j] == "]":
            return ("arr", i, j + 1, items), j + 1
        while True:
            node, j = _value(s, j)
            items.append(node)
            j = _WS.match(s, j).end()
            if s[j] == "]":
                return ("arr", i, j + 1, items), j + 1
            j += 1
    if c == '"':
        _, j = json.decoder.scanstring(s, i + 1)
        return ("lit", i, j), j
    m = _LIT.match(s, i)
    return ("lit", i, m.end()), m.end()


def _member(node, key):
    return next((n for k, n in node[3] if k == key), None)


def _after_last(node):
    """Where a new member or element goes: after the last one, or just inside
    the opening bracket when there is none; and the separator it needs."""
    if node[3]:
        last = node[3][-1]
        last = last[1] if node[0] == "obj" else last
        return last[2], ", "
    return node[1] + 1, ""


def splice(text, additions):
    """The old text with each (event, entry) appended; [(pos, inserted)]."""
    root, _ = _value(text, 0)
    by_ev = {}
    for ev, entry in additions:
        by_ev.setdefault(ev, []).append(json.dumps(entry))
    hooks = _member(root, "hooks")
    ins = []
    if hooks is None:
        pos, sep = _after_last(root)
        body = ", ".join('"%s": [%s]' % (ev, ", ".join(es)) for ev, es in by_ev.items())
        ins.append((pos, '%s"hooks": {%s}' % (sep, body)))
    else:
        new_keys = []
        for ev, es in by_ev.items():
            arr = _member(hooks, ev)
            if arr is None:
                new_keys.append('"%s": [%s]' % (ev, ", ".join(es)))
            else:
                pos, sep = _after_last(arr)
                ins.append((pos, sep + ", ".join(es)))
        if new_keys:
            pos, sep = _after_last(hooks)
            ins.append((pos, sep + ", ".join(new_keys)))
    ins.sort(key=lambda p: p[0])
    out, at = [], 0
    for pos, piece in ins:
        out.append(text[at:pos])
        out.append(piece)
        at = pos
    out.append(text[at:])
    return "".join(out), ins


def unsplice(new, ins):
    """new with the insertions taken back out, from the right: the old text."""
    shift = 0
    spans = []
    for pos, piece in ins:
        spans.append((pos + shift, len(piece)))
        shift += len(piece)
    for start, n in reversed(spans):
        new = new[:start] + new[start + n:]
    return new


def additions_for(doc, tokens, gh):
    known = {t for t, _ in gh.TOKENS if not t.startswith("browser ")}
    for t in tokens:
        if t not in known:
            raise Refused("token %s is not one the report names" % json.dumps(t))
    toks = list(dict.fromkeys(tokens)) + ["browser " + ev for ev in gh.browser_gone(doc)]
    return [(t,) + gh.line_for(t) for t in toks]


def expected(doc, adds):
    want = copy.deepcopy(doc)
    hooks = want.setdefault("hooks", {})
    for _, ev, entry in adds:
        hooks.setdefault(ev, []).append(entry)
    return want


def _write_new(path, data, mode):
    d = os.path.dirname(os.path.abspath(path))
    fd, tmp = tempfile.mkstemp(dir=d, prefix="." + os.path.basename(path) + ".buddy-wire-")
    try:
        try:
            os.fchmod(fd, mode)
            view = memoryview(data)
            while view:
                view = view[os.write(fd, view):]
            os.fsync(fd)
        finally:
            os.close(fd)
    except BaseException:
        os.unlink(tmp)
        raise
    return tmp


def _backup(path, raw):
    stamp = time.strftime("%Y%m%dT%H%M%SZ", time.gmtime())
    for n in range(1, 100):
        name = "%s.buddy-backup-%s%s" % (path, stamp, "" if n == 1 else "-%d" % n)
        try:
            fd = os.open(name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0), 0o600)
        except FileExistsError:
            continue
        try:
            view = memoryview(raw)
            while view:
                view = view[os.write(fd, view):]
            os.fsync(fd)
        except BaseException:
            os.close(fd)
            os.unlink(name)
            raise
        os.close(fd)
        return name
    raise Refused("no free backup name beside it")


def plan(text, doc, tokens, gh):
    """(additions, new text, its bytes), every check that needs no write done."""
    adds = additions_for(doc, tokens, gh)
    if not adds:
        return adds, text, None
    new, ins = splice(text, [(ev, e) for _, ev, e in adds])
    if decode(new) != expected(doc, adds) or unsplice(new, ins) != text:
        raise Refused("the spliced text did not verify (a bug here); nothing written")
    data = new.encode("utf-8")
    if len(data) > MAX_BYTES:
        raise Refused("it would be %d bytes, over %d" % (len(data), MAX_BYTES))
    return adds, new, data


def wire(path, snapshot, tokens):
    gh = gate_hook()
    raw, st = load(path)
    if snapshot == "-":
        if raw is not None:
            raise Refused("created after the report read it; run again")
        snap = None
    else:
        with open(snapshot, "rb") as f:
            snap = f.read()
        if raw != snap:
            raise Refused("changed after the report read it; run again")
    text, doc = parse(raw) if raw is not None else ("{}", {})
    adds, _, data = plan(text, doc, tokens, gh)
    # The gate's token counted present (the report's grep found it somewhere: a
    # permissions rule, another event, a Read matcher) while no PreToolUse hook
    # runs the gate: never added, as the report names it present, so said, and
    # the wiring fails, since it is the one line that refuses (panel).
    gateless = "buddy gate" not in tokens and not any(gh.gate_hooks(doc)) and re.search(r'buddy\\"* gate|/buddy gate', text)
    if not adds:
        print("hooks: --wire-hooks: nothing to add in %s" % path)
        return _gateless(path) if gateless else 0
    # HUP, TERM and INT are held from the first file made to the last line
    # said: a signal between two statements (the temp's close, a rename and
    # the name forgotten) stranded a file or removed a live backup (Codex code
    # pass). A held one ends the run after, the file already said.
    old_mask = signal.pthread_sigmask(signal.SIG_BLOCK, HELD)
    try:
        return _publish(path, raw, st, data, adds, gateless)
    finally:
        signal.pthread_sigmask(signal.SIG_SETMASK, old_mask)


def _publish(path, raw, st, data, adds, gateless):
    backup = leftover = None
    if raw is None:
        d = os.path.dirname(os.path.abspath(path))
        if not os.path.isdir(d):
            raise Refused("its directory %s does not exist" % d)
        tmp = _write_new(path, data, 0o600)
        try:
            os.link(tmp, path)
        except BaseException as e:
            os.unlink(tmp)
            if isinstance(e, FileExistsError):
                raise Refused("created while wiring; run again")
            raise
        # Published: a failed unlink of the temp is not "nothing written", and
        # left as it is the next run refuses a second hard link (Codex code pass).
        try:
            os.unlink(tmp)
        except OSError as e:
            leftover = (tmp, e)
    else:
        backup = _backup(path, raw)
        tmp = None
        try:
            tmp = _write_new(path, data, stat.S_IMODE(st.st_mode))
            again, st2 = load(path)
            if again != raw or (st2.st_ino, st2.st_mtime_ns) != (st.st_ino, st.st_mtime_ns):
                raise Refused("changed while wiring; run again")
            os.rename(tmp, path)
            tmp = None
        except BaseException:
            if tmp:
                os.unlink(tmp)
            os.unlink(backup)
            raise
    for t, ev, e in adds:
        print("hooks: --wire-hooks added %s on %s%s in %s" % (
            "buddy beat (browser tools, #71)" if t.startswith("browser ") else t,
            ev, " (matcher %s)" % json.dumps(e["matcher"]) if "matcher" in e else "", path))
    if backup:
        print("hooks: --wire-hooks kept the previous file as %s" % backup)
    rc = _gateless(path) if gateless else 0
    if leftover:
        print("hooks: --wire-hooks WROTE %s, but its temp %s, a second link to it, could not be removed (%s): remove it, "
              "or the next run refuses the file" % ((path,) + leftover))
        rc = 1
    try:
        dfd = os.open(os.path.dirname(os.path.abspath(path)), os.O_RDONLY)
        try:
            os.fsync(dfd)
        finally:
            os.close(dfd)
    except OSError as e:
        print("hooks: --wire-hooks WROTE %s, but its directory's fsync failed (%s): durable only once the system "
              "flushes it" % (path, e))
        return 1
    return rc


def _gateless(path):
    print("hooks: --wire-hooks did NOT add the gate: `buddy gate` appears in %s outside any PreToolUse hook, so the "
          "report counts it present; add README's PreToolUse entry by hand" % path)
    return 1


def check(path):
    """The preflight: every refusal the file can earn now, the splice included
    (as if every token were missing: the safe side, it may refuse a file a
    narrower wiring would fit), so a doomed wiring stops before step 1."""
    gh = gate_hook()
    raw, _ = load(path)
    d = os.path.dirname(os.path.abspath(path))
    if raw is not None:
        text, doc = parse(raw)
        plan(text, doc, [t for t, _ in gh.TOKENS if not t.startswith("browser ")], gh)
    elif not os.path.isdir(d):
        raise Refused("its directory %s does not exist" % d)
    if not os.access(d, os.W_OK):
        raise Refused("its directory %s is not writable" % d)
    return 0


def _stop(signum, frame):
    # HUP (a pane closing) or TERM would end Python with its buffered lines
    # unsaid; raised, they are flushed. In the write itself they are held
    # (HELD), so no handler runs between a file made and its name kept.
    raise SystemExit(128 + signum)


def main(argv):
    # INT too: held through the write, then raised as KeyboardInterrupt, it
    # printed a traceback over a completed write (Fable).
    for s in HELD:
        signal.signal(s, _stop)
    try:
        if len(argv) == 2 and argv[0] == "check":
            return check(argv[1])
        if len(argv) >= 3 and argv[0] == "wire":
            return wire(argv[1], argv[2], argv[3:])
    except Refused as e:
        print("hooks: --wire-hooks REFUSED, nothing written: %s: %s" % (argv[1], e))
        return 1
    except OSError as e:
        print("hooks: --wire-hooks REFUSED, nothing written: %s: %s" % (argv[1], e))
        return 1
    except (ValueError, TypeError, AttributeError, KeyError, RecursionError) as e:
        # A shape this reading cannot follow (an entry that is not an object,
        # a command that is not a string, nesting deepcopy cannot copy): one
        # line, not a traceback the install prints line by line (panel).
        print("hooks: --wire-hooks REFUSED, nothing written: %s: a shape it cannot read (%s)" % (argv[1], type(e).__name__))
        return 1
    print((__doc__ or "").split("\n\n")[1], file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
