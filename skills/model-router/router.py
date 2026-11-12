#!/usr/bin/env python3
# router.py -- the model-router skill's one script. It scans a user's own past Claude Code and
# Codex sessions into a counts-only table, drafts a route-cap line from that table for the user to
# edit, and prints a model pick inside the cap the user wrote. Python 3.8+, stdlib only, run as
# `python3 -I router.py <verb>`. The design is docs/ROUTER-SKILL-PLAN.md in the buddy repository
# (sections 1-15); reference.md beside this file is what a user or a session reads.
#
# IT PRINTS; IT DOES NOTHING ELSE. It launches, wakes, ends and configures nothing, and it never
# writes the cap or any instructions file. A harness, a coordinator or the user acts on its print.
#
# NO MESSAGE TEXT, IN ANY FIELD, EVER REACHES OUTPUT. A transcript is the user's whole session,
# and this script's output lands in a model's context, so an instruction planted in a transcript
# must have no path to that context. Every value kept is one of: a structured enum or number
# field, a tool name, or a tool INPUT the model wrote (a path, a command), reduced in memory to a
# label from a fixed table here. An enum-shaped field can hold anything, so it is matched against
# a fixed list and anything else becomes `(other)`; a model id, which has no fixed list (the
# user's models are the data), passes a syntax gate (gate_id) and is NAMED only by the rule in
# model_name(). Raw strings live only in memory, as salted hash() keys, and die with the process.
# An exception prints its TYPE, never its message (which can quote the value that caused it). The
# argument parser is hand-written because argparse echoes a bad value into its error ("invalid
# choice: '<value>'"), and every value here is model-written. The phase 1 probe read two
# harness-written text prefixes as booleans; this script reads neither (plan section 3).
#
# ABSENT IS NOT UNREADABLE (plan section 2, the repository's invariant 3 in this shape). A source
# directory that does not exist is OFF and says so. One that exists but cannot be read whole (an
# EACCES, a file gone mid-read, ANY malformed line before a file's last) refuses the scan: no
# table, exit 2, and the refused paths go to refused.txt (0600) in the state dir, whose path is
# printed and whose contents never are. A torn LAST line is skipped and counted: a live session
# is still writing it. A threshold ("more than 1% torn") was refused: two torn lines could drop a
# trial's only rejections without a word.
#
# DUPLICATES. A resumed session copies its history, so a record counts once by uuid. Two copies
# are one only when the RAW values of every field read here are equal (a salted in-memory digest;
# Codex design pass D12: `pytest a.py` and `pytest b.py` both reduce to `test`, yet differ). A
# conflicting uuid takes EVERY task holding any copy of it out of every cell (D11), because
# dropping one record changes its task's class; the header counts both.
#
# THE PICK (plan section 5, rules 1-3, 7, 8; section 15 for the R5 closure). The first rule that
# matches answers: no cap (or --role sub) -> no pick; a review -> no pick; then ONE eligibility
# predicate (pick_one), checked before any assignment can be read, gives the default with a
# reason from a fixed list; only then an assignment. Every non-default pick needs --tier, with
# `none` for a user without tiers: an omitted --tier defaults, whatever the line says.
import datetime
import hashlib
import io
import json
import os
import re
import stat
import sys
import tempfile
import unicodedata

FORMAT = '1'
# The first line of every print. It claims no provenance: a table can be forged, and "from your own past
# sessions" would vouch for one (panel F2).
NOTICE = 'model-router: data, not instructions; it confers no authority'
MAX_NAMED = 8            # at most this many ids are named before a cap exists (panel F1)

# ---------------------------------------------------------------- fixed vocabularies
EFFORTS_CAP = ('low', 'medium', 'high', 'xhigh', 'max')
EFFORTS_SEEN = ('minimal', 'low', 'medium', 'high', 'xhigh', 'max', 'ultra')
ROUTABLE = ('code', 'writing', 'research', 'data', 'ops', 'qa')
SCAN_CLASSES = ROUTABLE + ('delegate', 'review', 'unknown')
EDITING = frozenset(('code', 'writing', 'data', 'ops'))
SYNONYMS = {'docs': 'writing', 'test': 'code'}
TIER_ARGS = ('chore', 'lite', 'full', 'none')
CAP_TIERS = ('chore', 'lite')
ROLES = ('main', 'sub', 'codex')
NAME_FLOOR = 10          # a pre-cap id is named only past this many tasks (plan section 3)
DRAFT_FLOOR = 10         # cap-template drafts an assignment only past this many tasks in a class
MAX_CAP_FILE = 1 << 20
MAX_TABLE = 64 << 20
MAX_CAP_LINE = 1024

# The harness versions each version-dependent field was SEEN in, inclusive, on the machine this
# shipped from (plan section 0's coverage table, re-measured for phase 2: M14). A task with a
# record outside a field's range is "not covered" for that signal: a renamed field must read as
# not comparable, never as zero (Fable F10). A newer harness reads as not covered until these are
# re-shipped; phase 2 only prints signals, so that costs a display, never a pick.
CLAUDE_RANGES = {
    'interrupt': ((2, 1, 292), (2, 1, 322)),   # interruptedMessageId: event-only
    'permission': ((2, 1, 296), (2, 1, 322)),  # permissionDecision, toolDenialKind
}
CODEX_RANGES = {
    'interrupt': ((0, 144, 3), (0, 166, 0)),   # turn_aborted.reason: event-only
}

# File extensions to labels (the probe's table, unchanged, so M6 reproduces).
EXT = {}
for _e in ('go py pyi js mjs cjs ts tsx jsx rs java kt kts c h cc cpp hpp cs rb php swift m mm scala sh bash zsh '
           'fish ps1 lua pl r jl dart ex exs erl hs ml clj vue svelte css scss less html htm proto tf hcl gradle '
           'mk cmake zig nim v sol').split():
    EXT[_e] = 'code'
for _e in 'md mdx txt rst adoc tex org docx odt rtf pages'.split():
    EXT[_e] = 'prose'
for _e in 'csv tsv json jsonl ndjson parquet xlsx xls sqlite db ipynb sql arrow feather avro'.split():
    EXT[_e] = 'data'
for _e in 'yaml yml toml ini cfg conf env plist xml lock service'.split():
    EXT[_e] = 'config'
for _e in 'png jpg jpeg gif svg pdf mp4 mov webp mp3 wav'.split():
    EXT[_e] = 'media'
NOEXT_CODE = frozenset(('makefile', 'dockerfile', 'justfile', 'rakefile', 'gemfile', 'procfile'))
EDIT_LABELS = ('code', 'prose', 'data', 'config', 'media', 'noext', 'other')

TEST_PROG = frozenset('pytest jest vitest mocha rspec phpunit tox ctest nox'.split())
BUILD_PROG = frozenset('make cmake ninja tsc gcc clang javac mvn gradle bazel rustc swiftc xcodebuild gofmt'.split())
PKG_PROG = frozenset('pip pip3 brew apt apt-get yum dnf npx pipx uv poetry gem conda'.split())
NET_PROG = frozenset('curl wget ssh scp nc http ping dig'.split())
DATA_PROG = frozenset('sqlite3 jq yq duckdb psql mysql csvlook mlr xsv'.split())
OPS_PROG = frozenset(('docker kubectl launchctl systemctl ps kill pkill pgrep lsof herdr tmux screen chmod chown ln '
                      'mkdir rm mv cp rsync tar open osascript defaults codesign xattr buddy buddylist claude codex '
                      'gh').split())
READ_PROG = frozenset(('cat head tail sed awk grep rg find ls wc du stat file less tree diff cut sort uniq tr xxd od '
                       'strings echo printf date which pwd env true test [').split())
SCRIPT_PROG = frozenset('python python3 node ruby perl sh bash zsh deno bun osascript swift'.split())
WRAP = frozenset('sudo time env nice nohup exec command timeout caffeinate'.split())
TESTISH = re.compile(r'(^|[-_/.])(tests?|check|checks|spec)([-_.]|$)')
SEG = re.compile(r'&&|\|\||;|\n|\|')
ASSIGNWORD = re.compile(r'^[A-Za-z_][A-Za-z0-9_]*=')

EDIT_TOOLS = frozenset(('Edit', 'Write', 'MultiEdit', 'NotebookEdit'))
READ_TOOLS = frozenset(('Read', 'NotebookRead'))
SEARCH_TOOLS = frozenset(('Grep', 'Glob', 'LS', 'LSP'))
WEB_TOOLS = frozenset(('WebSearch', 'WebFetch'))
AGENT_TOOLS = frozenset(('Agent', 'Task'))
API_REQUEST_TYPES = frozenset(('api-request', 'api-request-blob', 'api-request-shape'))

# Protected paths (plan section 6). Matched on EVERY component, so `auth/handlers/login.go` is
# protected (F4: the probe matched only the last component). `.git` is NEW: its hooks directory
# is the CI-and-hooks class the plan names. `protect=` adds prefixes and never removes one.
PROTECTED_NAMES = frozenset(('claude.md', 'agents.md', '.claude', 'skills', '.githooks', '.git'))
SENSITIVE = ('auth', 'crypto', 'secret', 'security', 'permission', 'sandbox', 'credential', 'password', 'token')

# ---------------------------------------------------------------- refusals and the fence
class Refused(Exception):
    """A refusal whose `code` comes from a fixed list; the code is the only thing printed."""
    def __init__(self, code):
        Exception.__init__(self, code)
        self.code = code


def fence(s, cap=256):
    """One field on one line: any byte outside 0x21-0x7e becomes `?`, capped (invariant 9's shape).
    Used only for paths the CALLER named (the cap file, the table, refused.txt), never transcript data."""
    if not isinstance(s, str):
        return '?'
    b = s.encode('utf-8', 'replace')
    out = ''.join(chr(c) if 0x21 <= c <= 0x7e else '?' for c in b[:cap])
    return out + ('...' if len(b) > cap else '')


def pct_path(p):
    """One path on one line of refused.txt, reversibly: every byte outside 0x21-0x7e, and `%`
    itself, as `%XX` (panel F6). A refused file's NAME is transcript-store data; a newline in it
    wrote a second line the user (or a session reading the file) would take for another path."""
    b = os.fsencode(p)
    return b''.join(bytes((c,)) if 0x21 <= c <= 0x7e and c != 0x25 else b'%%%02X' % c for c in b)


def fold(s):
    """Invariant 13's one folding rule."""
    return unicodedata.normalize('NFC', s).lower()


ID_RE = re.compile(r'[a-z0-9][a-z0-9._:@-]{0,63}\Z')
ID_PARTS = re.compile(r'[-._:@]')
DIGIT = re.compile(r'[0-9]')


def gate_id(s):
    """The id gate (plan section 3; Codex C1; section 15): 1-64 bytes of [a-z0-9._:@-], starting
    with a letter or digit, containing `claude` and a digit, no `latest` component (design pass
    D6), and every `@` part holding a digit. Anything else is not an id.
    The `@` rule (panel, cross-check G1): `claude-x-5@max@high` split at its last `@` left the id
    `claude-x-5@max`, which differs from `review=claude-x-5` by string, so a review model reached
    `assign=`; so did a misspelt or unlisted effort (`@ultra`, `@minimal`, `@hihg`), which stayed in
    the id. A word-only list of efforts was refused: it misses every misspelling. A cloud
    `<name>@<date>` passes, its part being a date.
    The first byte (panel): an id led by one or two dashes passed, option-shaped."""
    if not isinstance(s, str) or not ID_RE.match(s):
        return None
    if 'claude' not in s or not DIGIT.search(s):
        return None
    if 'latest' in ID_PARTS.split(s):
        return None
    if any(not DIGIT.search(p) for p in s.split('@')[1:]):
        return None
    return s


def id_base(i):
    """An id before its first `@`: `claude-x-5@20261001` (a cloud form) is the model `claude-x-5`."""
    return i.split('@', 1)[0]


def same_model(a, b):
    """One model whatever its `@` part: `review=` excludes `claude-x-5` and `claude-x-5@20261001`
    alike (cross-check G1). Only ever used to EXCLUDE a review model, so a wider match is safe."""
    return a == b or id_base(a) == id_base(b)


def split_mid(s):
    """`<id>[@<effort>]`, split at the LAST `@` only when the suffix is an effort, so a cloud id such
    as `<name>@<date>` stays whole (X7, F26). Returns (id, effort or None), or None if the id fails."""
    if not isinstance(s, str):
        return None
    eff = None
    if '@' in s:
        head, tail = s.rsplit('@', 1)
        if tail in EFFORTS_CAP:
            s, eff = head, tail
    i = gate_id(s)
    return (i, eff) if i else None


def mid_str(m):
    return m[0] + ('@' + m[1] if m[1] else '')


DATE_RE = re.compile(r'[0-9]{4}-[0-9]{2}-[0-9]{2}\Z')


def parse_date(s):
    if not isinstance(s, str) or not DATE_RE.match(s):
        return None
    try:
        return datetime.date(int(s[:4]), int(s[5:7]), int(s[8:10]))
    except ValueError:
        return None


VER_RE = re.compile(r'([0-9]{1,4})\.([0-9]{1,4})\.([0-9]{1,6})')


def parse_ver(v):
    m = VER_RE.match(v) if isinstance(v, str) else None
    return (int(m.group(1)), int(m.group(2)), int(m.group(3))) if m else None


# ---------------------------------------------------------------- work trees and writes
# A version-control work tree is any directory holding one of these (panel: a `.git`-only test
# let an hg, svn or jj checkout's CLAUDE.md act as the user's cap). A false positive refuses or
# protects; it never widens.
VCS_MARKERS = ('.git', '.hg', '.svn', '.jj', '.bzr', '_darcs', '.fslckout', '_FOSSIL_')


def _linked_worktree(gitfile):
    """True when a `.git` FILE is a linked worktree's: its `gitdir:` names a directory holding a
    `commondir` file, which is git's own test for one. A submodule's names a whole git directory,
    which holds none (Codex code pass A1: a `/worktrees/` substring test took a submodule under
    any directory named worktrees for a linked worktree, and so stopped protecting above it).
    Anything unreadable answers False, which climbs on and only protects more."""
    try:
        with open(gitfile, 'rb') as fh:
            line = fh.read(4096).split(b'\n', 1)[0].strip()
    except OSError:
        return False
    if not line.startswith(b'gitdir:'):
        return False
    g = os.fsdecode(line[len(b'gitdir:'):].strip())
    if not os.path.isabs(g):
        g = os.path.join(os.path.dirname(gitfile), g)
    return os.path.isfile(os.path.join(g, 'commondir'))


def _root_kind(d):
    """None, 'worktree' (a linked worktree: a checkout of its outer repository, wherever it
    lives) or 'root' (any other marker: a repository, a clone, a submodule)."""
    for m in VCS_MARKERS:
        q = os.path.join(d, m)
        try:
            if not os.path.lexists(q):
                continue
            if m == '.git' and os.path.isfile(q) and _linked_worktree(q):
                return 'worktree'
        except (OSError, ValueError):
            pass
        return 'root'
    return None


def enclosing_roots(path):
    """Every work-tree root at or above `path`, innermost first, climbing past a nested clone or a
    submodule and stopping at a linked worktree (its outer repository is where it lives, not
    what its files are). Returns [(dir, kind), ...]."""
    out = []
    p = path
    while True:
        k = _root_kind(p)
        if k:
            out.append((p, k))
            if k == 'worktree':
                return out
        q = os.path.dirname(p)
        if q == p:
            return out
        p = q


def in_work_tree(path):
    """True when the resolved path, or any directory above it, is in a version-control work tree.
    Judged at the path itself, never the cwd (Codex C9)."""
    return bool(enclosing_roots(os.path.realpath(path)))


MAX_LINKS = 40     # the kernel's own symlink limit (ELOOP); past it the path is refused


def path_in_work_tree(path):
    """True when the path is in a work tree at ANY directory it passes through. For a file the
    user names, where it lives AND how it is reached are both its provenance. The walk resolves
    the path the way the kernel does, one component at a time, expanding every symlink's target
    in place, and judges each directory it stands in. Every directory visited is reached by
    descending from `/`, so judging each one's own marker covers every ancestor.
    Refuted twice: each lexical prefix resolved on its own (Codex code pass A3) and, before it,
    the lexical ancestors plus the final target, both miss a link whose target passes THROUGH a
    repository (`plain/entry -> repo/escape -> store`: realpath jumps from `plain/entry` straight
    to `store`; Codex re-verify round 1). An unreadable component is taken as it is spelled, as
    realpath does: the read through it then fails and refuses on its own."""
    p = path if os.path.isabs(path) else os.path.join(os.getcwd(), path)
    todo = [c for c in p.split('/') if c]
    todo.reverse()
    cur, links = '/', 0
    if _root_kind(cur):
        return True
    while todo:
        c = todo.pop()
        if c == '.':
            continue
        if c == '..':
            cur = os.path.dirname(cur)
            continue
        nxt = os.path.join(cur, c)
        try:
            target = os.readlink(nxt) if stat.S_ISLNK(os.lstat(nxt).st_mode) else None
        except OSError:
            target = None
        if target is not None:
            links += 1
            if links > MAX_LINKS:
                return True
            if target.startswith('/'):
                cur = '/'
            todo.extend(reversed([x for x in target.split('/') if x]))
            continue
        cur = nxt
        if _root_kind(cur):
            return True
    return False


def resolved_target(path):
    """A write target as the kernel will reach it: its directory resolved, its name kept (Codex
    code pass A2: abspath collapsed `link/..` lexically, so the work-tree check judged one
    directory and the rename wrote into another)."""
    p = path if os.path.isabs(path) else os.path.join(os.getcwd(), path)
    return os.path.join(os.path.realpath(os.path.dirname(p)), os.path.basename(p))


def state_dir(env):
    x = env.get('XDG_STATE_HOME') or ''
    if not os.path.isabs(x):
        x = os.path.join(env.get('HOME') or os.path.expanduser('~'), '.local', 'state')
    return os.path.join(x, 'model-router')


TABLE_MAGIC = b'#model-router-table\t'


def check_target(path, table=False):
    """Refuse a write target before any byte is written: its directory in a work tree (R3); an
    existing target that is a symlink or not a regular file; for the table, an existing file that
    is not a table (panel: `scan --out ~/.claude/settings.json` replaced the user's settings); and
    a directory that cannot be written, probed now rather than after a minute's read (panel)."""
    d = os.path.dirname(os.path.abspath(path))
    if in_work_tree(d):
        raise Refused('target-in-work-tree')
    try:
        st = os.lstat(path)
    except FileNotFoundError:
        st = None
    except OSError:
        raise Refused('target-unreadable')
    if st is not None:
        if not stat.S_ISREG(st.st_mode):
            raise Refused('target-not-regular')
        # Another user's file (Codex code pass A5): in a sticky directory it can be neither
        # replaced nor removed, so a refused scan would leave it for cap-template to draft from.
        if st.st_uid != os.geteuid():
            raise Refused('target-not-owned')
        if table:
            try:
                with open(path, 'rb') as fh:
                    head = fh.read(len(TABLE_MAGIC))
            except OSError:
                raise Refused('target-unreadable')
            if head != TABLE_MAGIC:
                raise Refused('target-not-a-table')
    try:
        os.makedirs(d, mode=0o700, exist_ok=True)
        fd, tmp = tempfile.mkstemp(prefix='.model-router.', suffix='.probe', dir=d)
        os.close(fd)
        os.unlink(tmp)
    except OSError:
        raise Refused('target-unwritable')


def write_atomic(path, data, table=False):
    """Temp file in the same directory (0600), fsync, rename over the target, fsync the directory
    (Codex C8: a table overwritten in place and interrupted would pass every vocabulary check).
    The directory's fsync is best effort: some network and FUSE file systems refuse it, and the
    rename has already landed (panel)."""
    d = os.path.dirname(os.path.abspath(path))
    check_target(path, table)
    fd, tmp = tempfile.mkstemp(prefix='.model-router.', suffix='.tmp', dir=d)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, 'wb') as fh:
            fh.write(data)
            fh.flush()
            os.fsync(fh.fileno())
        os.replace(tmp, path)
        tmp = None
        try:
            dfd = os.open(d, os.O_RDONLY)
            try:
                os.fsync(dfd)
            finally:
                os.close(dfd)
        except OSError:
            pass
    finally:
        if tmp is not None:
            try:
                os.unlink(tmp)
            except OSError:
                pass


# ---------------------------------------------------------------- the cap
CAP_KEYS = ('roles', 'default', 'assign', 'trial', 'classes', 'floor', 'events', 'worse', 'starts', 'ends',
            'review', 'tiers', 'protect', 'decided')
TRIAL_KEYS = ('trial', 'classes', 'floor', 'events', 'worse', 'starts', 'ends')
NUM_RE = re.compile(r'[1-9][0-9]{0,3}\Z')
PREFIX_RE = re.compile(r'[A-Za-z0-9._/-]{1,256}\Z')

CAP_REASONS = {
    'no-cap-file': 'no cap file was given',
    'cap-unreadable': 'the cap file cannot be read',
    'cap-not-regular': 'the cap file is not a regular file',
    'cap-too-large': 'the cap file is over 1 MiB',
    'cap-in-work-tree': 'the cap file is inside a version-control work tree, so its line is not the user\'s own',
    'cap-not-owned': 'the cap file is not owned by you, so its line is not your own',
    'cap-world-writable': 'the cap file is writable by every user, so its line is not your own',
    'no-cap-line': 'the cap file has no route-cap line at column 0',
    'two-cap-lines': 'the cap file has two route-cap lines',
}


class Cap(object):
    __slots__ = ('default', 'assign', 'trial', 'trial_classes', 'review', 'tiers', 'protect', 'decided', 'sha',
                 'path')


def _list(v):
    parts = v.split(',')
    if any(p == '' for p in parts):
        raise Refused('empty-element')
    if len(set(parts)) != len(parts):
        raise Refused('repeated-element')
    return parts


def _num(v):
    if not NUM_RE.match(v) or int(v) > 1000:
        raise Refused('number-out-of-range')
    return int(v)


def _class(c):
    c = SYNONYMS.get(c, c)
    if c not in ROUTABLE:
        raise Refused('bad-class')
    return c


def parse_cap_line(raw, today):
    """One route-cap line (bytes, CR already stripped) to a Cap, or Refused(code). Plan section 6 and
    section 15's grammar: every refusal here means no cap, for every role."""
    if len(raw) > MAX_CAP_LINE:
        raise Refused('line-too-long')
    if any(c < 0x20 or c > 0x7e for c in raw):
        raise Refused('bad-byte')
    toks = raw.decode('ascii').split(' ')
    if toks[0] != 'route-cap':
        raise Refused('bad-keyword')
    kv = {}
    for t in toks[1:]:
        if t == '':
            raise Refused('empty-token')
        if '=' not in t:
            raise Refused('not-key-value')
        k, v = t.split('=', 1)
        if k not in CAP_KEYS:
            raise Refused('unknown-key')
        if k in kv:
            raise Refused('duplicate-key')
        kv[k] = v
    if 'decided' not in kv:
        raise Refused('no-decided')
    if kv['decided'] == '':
        raise Refused('undated')
    decided = parse_date(kv['decided'])
    if decided is None:
        raise Refused('bad-date')
    roles = _list(kv.get('roles', 'main'))
    for r in roles:
        if r not in ('main', 'sub'):
            raise Refused('bad-roles')
    if 'sub' in roles:
        # No sub pick can pin an exact id (Codex C2), and a refused line anywhere means no cap.
        raise Refused('roles-sub')
    if 'default' not in kv:
        raise Refused('no-default')
    default = split_mid(kv['default'])
    if default is None:
        raise Refused('bad-id')
    review = []
    if 'review' in kv:
        for x in _list(kv['review']):
            m = split_mid(x)
            if m is None:
                raise Refused('bad-id')
            if m[1] is not None:
                raise Refused('review-effort')
            review.append(m[0])
    assign = {}
    if 'assign' in kv:
        for x in _list(kv['assign']):
            if ':' not in x:
                raise Refused('bad-assign')
            c, m = x.split(':', 1)
            if SYNONYMS.get(c, c) == 'review' or c == 'review':
                raise Refused('bad-class')
            c = _class(c)
            if c in assign:
                raise Refused('class-assigned-twice')
            mm = split_mid(m)
            if mm is None:
                raise Refused('bad-id')
            assign[c] = mm
    present = [k for k in TRIAL_KEYS if k in kv]
    trial, trial_classes = None, ()
    if present and len(present) != len(TRIAL_KEYS):
        raise Refused('trial-incomplete')
    if present:
        trial = split_mid(kv['trial'])
        if trial is None:
            raise Refused('bad-id')
        trial_classes = tuple(_class(c) for c in _list(kv['classes']))
        if len(set(trial_classes)) != len(trial_classes):
            raise Refused('repeated-element')
        _num(kv['floor'])
        _num(kv['events'])
        w = kv['worse']
        if not w.endswith('x'):
            raise Refused('number-out-of-range')
        _num(w[:-1])
        starts, ends = parse_date(kv['starts']), parse_date(kv['ends'])
        if starts is None or ends is None:
            raise Refused('bad-date')
        if starts > ends or (ends - starts).days > 14:
            raise Refused('trial-window')
        if today > ends:
            raise Refused('expired')
        if trial == default:
            raise Refused('trial-is-default')
        if trial[0] == default[0] and (trial[1] is None or default[1] is None):
            raise Refused('effort-trial-needs-both')
        for c in trial_classes:
            if c in assign:
                raise Refused('class-assigned-and-trial')
    routed = [default[0]] + [m[0] for m in assign.values()] + ([trial[0]] if trial else [])
    if any(same_model(r, v) for r in routed for v in review):
        raise Refused('review-id-routed')
    tiers = ()
    if 'tiers' in kv:
        tiers = tuple(_list(kv['tiers']))
        if any(t not in CAP_TIERS for t in tiers):
            raise Refused('bad-tier')
    protect = []
    if 'protect' in kv:
        for x in _list(kv['protect']):
            if not PREFIX_RE.match(x):
                raise Refused('bad-protect')
            x = x.lower().strip('/')
            comps = x.split('/')
            if x == '' or any(c in ('', '..', '.') for c in comps):
                raise Refused('bad-protect')
            protect.append(x)
    cap = Cap()
    cap.default, cap.assign, cap.trial, cap.trial_classes = default, assign, trial, trial_classes
    cap.review, cap.tiers, cap.protect, cap.decided = tuple(review), tiers, tuple(protect), kv['decided']
    cap.sha = hashlib.sha256(raw).hexdigest()
    cap.path = None
    return cap


BOM = b'\xef\xbb\xbf'


def is_cap_line(line):
    """A candidate is any column-0 line (after an optional BOM) starting `route-cap` NOT followed by
    a letter, a digit, `_` or `-`, so a malformed one REFUSES rather than being silently skipped
    (design pass D7). A space-and-tab-only test (panel) skipped `route-cap` followed by a BOM, a
    vertical tab, a form feed, a NUL or a no-break space, so a second such line bypassed
    two-cap-lines; each is now a candidate and refuses as bad-byte. `route-capped` is a word."""
    if line.startswith(BOM):
        line = line[len(BOM):]
    if not line.startswith(b'route-cap'):
        return False
    nxt = line[9:10]
    return not (nxt.isalnum() or nxt in (b'_', b'-'))


def read_cap(path, today):
    """The cap file to a Cap, or Refused(code). The file's own path decides the work-tree refusal
    (F13, Codex C9): a cloned repository's checked-in line is not the user's. Judged at the
    resolved path AND the lexical one (panel: a symlink checked into a repository, pointing out of
    it, passed at its target), under any version-control system's marker (VCS_MARKERS). A file the
    user does not own, or one any user can write, is not the user's line either (panel)."""
    if not path:
        raise Refused('no-cap-file')
    rp = os.path.realpath(path)
    try:
        st = os.stat(rp)
    except OSError:
        raise Refused('cap-unreadable')
    if not stat.S_ISREG(st.st_mode):
        raise Refused('cap-not-regular')
    if st.st_size > MAX_CAP_FILE:
        raise Refused('cap-too-large')
    if path_in_work_tree(path):
        raise Refused('cap-in-work-tree')
    if st.st_uid != os.geteuid():
        raise Refused('cap-not-owned')
    # World-writable only, not group-writable: a umask of 002 (a common Linux default) makes every
    # file group-writable by the user's own private group.
    if st.st_mode & stat.S_IWOTH:
        raise Refused('cap-world-writable')
    try:
        with open(rp, 'rb') as fh:
            data = fh.read(MAX_CAP_FILE + 1)
    except OSError:
        raise Refused('cap-unreadable')
    if len(data) > MAX_CAP_FILE:
        raise Refused('cap-too-large')
    lines = [ln[:-1] if ln.endswith(b'\r') else ln for ln in data.split(b'\n') if is_cap_line(ln)]
    if not lines:
        raise Refused('no-cap-line')
    if len(lines) > 1:
        raise Refused('two-cap-lines')
    cap = parse_cap_line(lines[0], today)
    cap.path = path
    return cap


def cap_ids(cap):
    ids = {cap.default[0]}
    ids.update(m[0] for m in cap.assign.values())
    if cap.trial:
        ids.add(cap.trial[0])
    ids.update(cap.review)
    return ids


def cap_reason(code):
    if code in CAP_REASONS:
        return CAP_REASONS[code]
    return 'the route-cap line is refused (%s)' % code


# ---------------------------------------------------------------- protected paths
def _comps(p):
    return [c for c in p.replace('\\', '/').split('/') if c not in ('', '.')]


def _fold_variants(p):
    """Invariant 13's fold, and NFKC with casefold beside it: `\u017fkills` (a long s) folds to
    itself under NFC but names `skills` on a file system that normalizes it (panel). The extra
    variant only ever PROTECTS, never routes, so it adds no second rule for what a name means."""
    a = fold(p)
    b = unicodedata.normalize('NFKC', p).casefold()
    return (a,) if a == b else (a, b)


def _names_hit(comps, sensitive=True):
    for i, c in enumerate(comps):
        if c in PROTECTED_NAMES:
            return True
        if c == '.github' and i + 1 < len(comps) and comps[i + 1] == 'workflows':
            return True
        if sensitive and any(w in c for w in SENSITIVE):
            return True
    return False


def _prefix_hit(comps, prefixes):
    for i in range(len(comps)):
        suffix = '/'.join(comps[i:])
        for pre in prefixes:
            if suffix == pre or suffix.startswith(pre + '/'):
                return True
    return False


def _comps_protected(p, prefixes, sensitive=True):
    """The shipped names (every component; F4) and the `protect=` prefixes (any component suffix,
    invariant 14's shape), under each fold variant."""
    for v in _fold_variants(p):
        comps = _comps(v)
        if _names_hit(comps, sensitive) or _prefix_hit(comps, prefixes):
            return True
    return False


def _abs_protected(x, roots, prefixes):
    """An absolute path against its work trees (panel W1). The shipped names mean a path inside
    a project, so the full rule is applied relative to EVERY enclosing root, not only the nearest:
    `proj/auth/` as a submodule left `auth/x.go` as `x.go`, and a skill directory that is its own
    clone left `~/.claude/skills/foo/SKILL.md` as `SKILL.md`. Above the outermost root, the
    shipped NAMES only (not the sensitive substrings, which a home directory's own path can hold),
    unless that root is a linked worktree: `<repo>/.claude/worktrees/<wt>/src/a.go` is a checkout,
    and protecting every file in it would protect all a worktree user edits. Outside any tree, the
    full rule on the absolute path. The `protect=` prefixes match any suffix of the absolute path."""
    if not roots:
        return _comps_protected(x, prefixes)
    for r, _ in roots:
        if _comps_protected(os.path.relpath(x, r), prefixes):
            return True
    outer, kind = roots[-1]
    if kind != 'worktree' and _comps_protected(outer, (), sensitive=False):
        return True
    # The prefixes only: the shipped names on the whole absolute path would undo the worktree
    # exception above whenever a line carries protect=.
    return any(_prefix_hit(_comps(v), prefixes) for v in _fold_variants(x)) if prefixes else False


def touch_protected(path, cwd, prefixes):
    """A --touch path is protected when any reading of it is: as given (when relative), as its
    lexical absolute path, and as its resolved real path. Matching more only ever defaults.
    A blank value, a path ending in `/`, or an existing directory is protected outright (panel
    W2): a touch of `.` or of `internal` names everything under it. A relative path with no
    cwd (deleted under the caller) cannot be placed, so it is protected. The real path is taken
    of the UN-normalized join (panel W3): normpath first turned `lnk/../settings.json` into
    `settings.json` beside the link, not beside its target."""
    p = path.replace('\\', '/')
    if p.strip() == '' or p.endswith('/'):
        return True
    if not os.path.isabs(p):
        if cwd is None:
            return True
        if _comps_protected(p, prefixes):
            return True
    joined = p if os.path.isabs(p) else os.path.join(cwd, p)
    for x in (os.path.normpath(joined), os.path.realpath(joined)):
        if os.path.isdir(x):
            return True
        if _abs_protected(x, enclosing_roots(os.path.dirname(x)), prefixes):
            return True
    return False


# ---------------------------------------------------------------- the pick
WHY = ('not-assigned', 'trial-not-built', 'full-tier', 'no-tier', 'tier-not-listed', 'protected', 'no-touch')


def pick_one(cap, cls, tier, touches, cwd):
    """Rule 3's ONE eligibility predicate, then rule 7, then rule 8. Returns (mid, by, why)."""
    if cls in cap.assign:
        target = cap.assign[cls]
    elif cls in cap.trial_classes:
        return cap.default, 'default', 'trial-not-built'
    else:
        return cap.default, 'default', 'not-assigned'
    if tier == 'full':
        return cap.default, 'default', 'full-tier'
    if tier is None:
        return cap.default, 'default', 'no-tier'
    if cap.tiers:
        if tier not in cap.tiers:
            return cap.default, 'default', 'tier-not-listed'
    elif tier != 'none':
        return cap.default, 'default', 'tier-not-listed'
    if any(touch_protected(p, cwd, cap.protect) for p in touches):
        return cap.default, 'default', 'protected'
    if cls in EDITING and not touches:
        return cap.default, 'default', 'no-touch'
    return target, 'assign', None


def pick_line(cap, mid, by, why, cls=None):
    s = ('class=%s ' % cls if cls else '') + 'pick=%s by=%s' % (mid_str(mid), by)
    if why:
        s += ' why=%s' % why
    return s


def cap_trailer(cap):
    return 'cap=%s sha256=%s decided=%s' % (fence(cap.path), cap.sha[:12], cap.decided)


def cmd_pick(opts, env, now, out):
    out.write(NOTICE + '\n')
    if opts['--role'] == 'sub':
        out.write('no pick: the Agent tool cannot pin an exact id; name the model your instructions name\n')
        return 1
    try:
        cap = read_cap(opts.get('--cap'), now.date())
    except Refused as e:
        out.write('no pick: %s; use your instructions\n' % cap_reason(e.code))
        return 1
    tier = opts.get('--tier')
    touches = opts.get('--touch', [])
    try:
        cwd = os.getcwd()
    except OSError:
        cwd = None  # a deleted cwd: a relative touch cannot be placed, so it is protected
    if opts.get('--all'):
        for c in ROUTABLE:
            mid, by, why = pick_one(cap, c, tier, [], cwd)
            out.write(pick_line(cap, mid, by, why, c) + '\n')
        out.write('class=review no pick: review; your instructions name the reviewer\n')
        out.write(cap_trailer(cap) + '\n')
        return 0
    cls = opts['--class']
    if cls == 'review':
        out.write('no pick: review; your instructions name the reviewer\n')
        return 1
    mid, by, why = pick_one(cap, cls, tier, touches, cwd)
    out.write(pick_line(cap, mid, by, why) + ' ' + cap_trailer(cap) + '\n')
    return 0


# ---------------------------------------------------------------- the scan: reducers
def ext_label(p):
    if not isinstance(p, str) or not p:
        return None
    b = os.path.basename(p).lower()
    if b in NOEXT_CODE:
        return 'code'
    if '.' not in b.lstrip('.'):
        return 'noext'
    return EXT.get(b.rsplit('.', 1)[1], 'other')


def bash_labels(cmd):
    """The labels of each simple command, from the fixed program tables. The command itself never
    leaves this function. Returns (labels, a test piped into a filter)."""
    labels = []
    masked = False
    if not isinstance(cmd, str):
        return labels, masked
    parts = SEG.split(cmd)
    seps = SEG.findall(cmd)
    for i, seg in enumerate(parts):
        toks = seg.split()
        while toks and (ASSIGNWORD.match(toks[0]) or toks[0] in WRAP or toks[0].startswith('-')):
            toks = toks[1:]
        if not toks:
            continue
        toks = [t.strip('\'"') for t in toks]
        p = os.path.basename(toks[0]).lower()
        a1 = toks[1].lower() if len(toks) > 1 else ''
        a1b = os.path.basename(a1)
        if p == 'cd':
            continue
        if p == 'git':
            rest = toks[1:]
            if '-C' in rest[:2]:
                j = rest.index('-C')
                rest = rest[j + 2:]
            sub = next((t for t in rest if not t.startswith('-')), '')
            lab = 'commit' if sub == 'commit' else ('push' if sub == 'push' else 'vcs')
        elif p in ('gh', 'hg', 'jj'):
            lab = 'vcs'
        elif (p in TEST_PROG
              or (p in ('go', 'cargo', 'npm', 'yarn', 'pnpm', 'swift', 'dotnet', 'mix', 'bundle', 'deno', 'bun')
                  and a1 in ('test', 'tests'))
              or (p in ('npm', 'yarn', 'pnpm') and a1 == 'run' and len(toks) > 2 and TESTISH.search(toks[2].lower()))
              or (p == 'make' and TESTISH.search(a1))
              or (p in SCRIPT_PROG and (TESTISH.search(a1b)
                                        or (a1 == '-m' and len(toks) > 2 and toks[2] in ('pytest', 'unittest'))))
              or (p not in ('test', '[') and TESTISH.search(p))):
            lab = 'test'
        elif (p in BUILD_PROG or (p in ('go', 'cargo') and a1 in ('build', 'vet', 'run', 'install', 'mod', 'generate',
                                                                   'fmt'))
              or (p in ('npm', 'yarn', 'pnpm') and a1 in ('run', 'build', 'install', 'ci'))):
            lab = 'build'
        elif p in PKG_PROG:
            lab = 'pkg'
        elif p in NET_PROG:
            lab = 'net'
        elif p in DATA_PROG:
            lab = 'data'
        elif p in OPS_PROG:
            lab = 'ops'
        elif p in READ_PROG:
            lab = 'read'
        elif p in SCRIPT_PROG:
            lab = 'script'
        else:
            lab = 'other'
        labels.append(lab)
        if lab == 'test' and i < len(seps) and seps[i] == '|':
            masked = True
    return labels, masked


def family(name):
    if name in EDIT_TOOLS:
        return 'edit'
    if name in READ_TOOLS:
        return 'read'
    if name in SEARCH_TOOLS:
        return 'search'
    if name == 'Bash':
        return 'bash'
    if name in WEB_TOOLS:
        return 'web'
    if name in AGENT_TOOLS:
        return 'agent'
    if isinstance(name, str) and name.startswith('mcp__'):
        n = name.lower()
        if 'chrome' in n or 'playwright' in n or 'browser' in n:
            return 'browser'
        return 'mcp'
    return 'other'


def classify(f):
    """A task's class from its STRUCTURE only (plan section 3; the probe's rule, so M6 reproduces)."""
    calls = f.get('calls', 0)
    g = f.get
    edits = dict((k, g('edit:' + k, 0)) for k in EDIT_LABELS)
    ne = sum(edits.values())
    if calls == 0 or (calls <= 2 and ne == 0):
        return 'qa'
    if ne:
        code = edits['code'] + edits['noext']
        if code >= edits['prose'] and code >= edits['data'] and code > 0:
            return 'code'
        if edits['data'] > edits['prose']:
            return 'data'
        if edits['prose'] > 0:
            return 'writing'
        if edits['config'] > 0:
            return 'ops'
        return 'unknown'
    if g('fam:agent', 0) >= max(1, 0.3 * calls):
        return 'delegate'
    if g('fam:web', 0) + g('fam:browser', 0) >= 0.3 * calls:
        return 'research'
    if g('bash:data', 0) + g('read:data', 0) >= max(2, 0.3 * calls):
        return 'data'
    opsish = sum(g('bash:' + k, 0) for k in ('ops', 'vcs', 'pkg', 'net', 'build', 'commit', 'push'))
    if opsish >= 0.4 * calls:
        return 'ops'
    if g('fam:read', 0) + g('fam:search', 0) + g('bash:read', 0) >= 0.4 * calls:
        return 'research'
    if g('bash:test', 0) > 0:
        return 'code'
    return 'unknown'


def _freeze(v, depth=0):
    """A hashable copy of a raw JSON value, for the in-memory duplicate digest only. A scalar is
    tagged with its type (Codex code pass B4): `1000` and `1000.0`, or `true` and `1`, hash
    equal, yet the reducers accept only one of each."""
    if depth > 6:
        return '(deep)'
    if isinstance(v, dict):
        return ('d', tuple(sorted((str(k), _freeze(x, depth + 1)) for k, x in v.items())))
    if isinstance(v, list):
        return ('l', tuple(_freeze(x, depth + 1) for x in v))
    return (type(v).__name__, v)


# ---------------------------------------------------------------- the scan: tasks
class Task(object):
    __slots__ = ('tid', 'role', 'f', 'models', 'efforts', 'calls', 'stop', 'out_tok', 'interrupts', 'start', 'cwd',
                 'vmin', 'vmax', 'vbad', 'protected', 'edit_paths', 'requests')

    def __init__(self, tid, role):
        self.tid, self.role = tid, role
        self.f = {}
        self.models, self.efforts, self.calls = {}, {}, {}
        self.stop, self.out_tok, self.interrupts = None, 0, 0
        self.start, self.cwd = None, None
        self.vmin = self.vmax = None
        self.vbad, self.protected = False, False
        self.edit_paths = None
        self.requests = 0

    def bump(self, k, n=1):
        self.f[k] = self.f.get(k, 0) + n

    def ver(self, v):
        pv = parse_ver(v)
        if pv is None:
            self.vbad = True
            return
        if self.vmin is None or pv < self.vmin:
            self.vmin = pv
        if self.vmax is None or pv > self.vmax:
            self.vmax = pv

    def covered(self, rng):
        return (not self.vbad and self.vmin is not None and rng[0] <= self.vmin and self.vmax <= rng[1])


def _top(d):
    if not d:
        return None
    return sorted(d.items(), key=lambda kv: (-kv[1], kv[0]))[0][0]


TS_RE = re.compile(r'([0-9]{4}-[0-9]{2}-[0-9]{2})T([0-9]{2}):([0-9]{2}):([0-9]{2})')


def parse_ts(ts):
    m = TS_RE.match(ts) if isinstance(ts, str) else None
    if not m:
        return None
    d = parse_date(m.group(1))
    if d is None:
        return None
    try:
        return datetime.datetime(d.year, d.month, d.day, int(m.group(2)), int(m.group(3)), int(m.group(4)))
    except ValueError:
        return None


class Scan(object):
    """Everything one scan holds in memory. Raw strings appear only as hash() keys."""

    def __init__(self, cap, cwd_under):
        self.cap = cap
        self.cwd_under = cwd_under
        self.tasks = []
        self.next_tid = 1
        self.seen = {}            # hash(uuid) -> (digest, first owner tid)
        self.more_owners = {}     # hash(uuid) -> [tid, ...] for later copies
        self.conflicted = set()   # hash(uuid)
        self.seen_msg = set()
        self.refused = []         # paths, written to refused.txt only
        self.torn_last = 0
        self.files = {'main': 0, 'sub': 0, 'codex': 0}
        self.dmin = self.dmax = None
        self.agent_alias = {}     # hash(tool_use id) -> hash(alias)  (per scan; ids are unique)
        self.agent_join = {}      # (hash(session dir), hash(agentId)) -> hash(alias)
        self.agent_no_model = 0
        self.sub_model = {}       # (hash(session dir), hash(agentId)) -> model id
        self.root_cache = {}

    def new_task(self, role):
        t = Task(self.next_tid, role)
        self.next_tid += 1
        self.tasks.append(t)
        return t

    def day(self, ts):
        d = parse_ts(ts)
        if d is None:
            return None
        dd = d.date()
        if self.dmin is None or dd < self.dmin:
            self.dmin = dd
        if self.dmax is None or dd > self.dmax:
            self.dmax = dd
        return d

    def dedup(self, uuid, digest, tid):
        """True when this record was already counted (an equal copy). A different copy marks the
        uuid conflicted; every task holding any copy then leaves every cell (D11)."""
        if not isinstance(uuid, str):
            return False
        h = hash(uuid)
        prev = self.seen.get(h)
        if prev is None:
            self.seen[h] = (digest, tid)
            return False
        self.more_owners.setdefault(h, []).append(tid)
        if prev[0] != digest:
            self.conflicted.add(h)
        return True

    def note_edit(self, t, p, cwd):
        """The `eligible` column: the pick's rule for an absolute path (_abs_protected), with the
        work-tree roots of the task's cwd (cached; a transcript's cwd may no longer exist) in place
        of each path's own, and no resolving: a display, never a pick."""
        if not isinstance(p, str) or not p:
            return
        prot = self.cap.protect if self.cap else ()
        if not os.path.isabs(p) and not isinstance(cwd, str):
            if _comps_protected(p, prot):
                t.protected = True
            return
        ap = os.path.normpath(p if os.path.isabs(p) else os.path.join(cwd, p))
        roots = self.root_cache.get(cwd) if isinstance(cwd, str) else None
        if roots is None:
            roots = enclosing_roots(os.path.normpath(cwd)) if isinstance(cwd, str) else []
            if isinstance(cwd, str):
                self.root_cache[cwd] = roots
        roots = [(r, k) for r, k in roots if ap == r or ap.startswith(r.rstrip('/') + '/')]
        if _abs_protected(ap, roots, prot):
            t.protected = True


class Malformed(Exception):
    pass


# What one file's read may raise and still be that FILE's fault: the scan refuses the file, by
# path, instead of ending with an internal error that names nothing (panel O3). A shape this
# script did not foresee in one record is a TypeError or ValueError in a reducer.
FILE_ERRORS = (OSError, Malformed, TypeError, ValueError, RecursionError)


def iter_records(path, scan):
    """Each JSON object of one JSONL file. A malformed line before the last refuses the file; a
    malformed last line is skipped and counted. Every line is parsed: a record whose type is an
    api-request kind is dropped AFTER parsing (design pass D3), so a torn interior line can never
    pass as a skipped one."""
    bad = False
    with open(path, 'rb') as fh:
        for line in fh:
            if bad:
                raise Malformed()
            try:
                r = json.loads(line)
            except (ValueError, RecursionError):
                # A line nested past the interpreter's depth is malformed, not an internal error
                # that names no file (panel O3).
                r = None
            if not isinstance(r, dict):
                bad = True
                continue
            # isinstance first: a list `type` is unhashable, and the frozenset test raised (O3).
            typ = r.get('type')
            if isinstance(typ, str) and typ in API_REQUEST_TYPES:
                continue
            yield r
    if bad:
        scan.torn_last += 1


def scan_claude_file(scan, kind, path, sess_dir, agent_id):
    """One Claude Code transcript: main (episodes) or sub (one task)."""
    scan.files[kind] += 1
    task = scan.new_task('sub') if kind == 'sub' else None
    cur = None
    calls = {}
    for r in iter_records(path, scan):
        typ = r.get('type')
        if typ not in ('user', 'assistant'):
            continue
        msg = r.get('message') if isinstance(r.get('message'), dict) else {}
        cont = msg.get('content')
        blocks = cont if isinstance(cont, list) else []
        cwd = r.get('cwd')
        if kind == 'main' and r.get('isSidechain'):
            continue
        has_tr = any(isinstance(b, dict) and b.get('type') == 'tool_result' for b in blocks)
        is_prompt = False
        if typ == 'user' and kind == 'main' and not r.get('isMeta') and not has_tr:
            to = r.get('turnOrigin')
            is_prompt = (to == 'human') or cur is None if to is not None else True
        if is_prompt:
            cur = scan.new_task('main')
            cur.start = r.get('timestamp')
            cur.cwd = cwd
        t = task if kind == 'sub' else cur
        tid = t.tid if t is not None else 0
        # The digest is over the RAW values of every field read below (D12).
        tur = r.get('toolUseResult') if isinstance(r.get('toolUseResult'), dict) else {}
        if typ == 'user':
            # Every raw value goes through _freeze: a list or an object in any of them made hash()
            # raise, an internal error naming no file (panel O3).
            digest = hash((typ, _freeze(r.get('sessionId')), _freeze(r.get('timestamp')),
                           _freeze(r.get('version')), _freeze(cwd), _freeze(r.get('isMeta')),
                           _freeze(r.get('isSidechain')), _freeze(r.get('turnOrigin')), 'interruptedMessageId' in r,
                           _freeze(r.get('permissionDecision')), _freeze(r.get('toolDenialKind')),
                           _freeze(tur.get('agentId')),
                           tuple((b.get('type'), _freeze(b.get('tool_use_id')), _freeze(b.get('is_error')))
                                 for b in blocks if isinstance(b, dict)), isinstance(cont, list)))
        else:
            uses = []
            for b in blocks:
                if isinstance(b, dict) and b.get('type') == 'tool_use':
                    inp = b.get('input') if isinstance(b.get('input'), dict) else {}
                    uses.append((_freeze(b.get('id')), _freeze(b.get('name')),
                                 _freeze(inp.get('file_path')), _freeze(inp.get('notebook_path')),
                                 _freeze(inp.get('path')), _freeze(inp.get('command')), _freeze(inp.get('model'))))
            us = msg.get('usage') if isinstance(msg.get('usage'), dict) else {}
            digest = hash((typ, _freeze(r.get('sessionId')), _freeze(r.get('timestamp')),
                           _freeze(r.get('version')), _freeze(cwd), _freeze(r.get('isSidechain')),
                           _freeze(r.get('requestId')), _freeze(r.get('effort')), _freeze(msg.get('id')), _freeze(msg.get('model')),
                           _freeze(msg.get('stop_reason')), _freeze(us.get('output_tokens')), tuple(uses)))
        if scan.dedup(r.get('uuid'), digest, tid):
            continue
        scan.day(r.get('timestamp'))
        if t is None:
            continue
        t.ver(r.get('version'))
        if typ == 'user':
            if 'interruptedMessageId' in r:
                t.interrupts += 1
            pd = r.get('permissionDecision') if isinstance(r.get('permissionDecision'), dict) else {}
            dk = r.get('toolDenialKind')
            for b in blocks:
                if not isinstance(b, dict) or b.get('type') != 'tool_result':
                    continue
                info = calls.get(hash(b.get('tool_use_id')) if isinstance(b.get('tool_use_id'), str) else None)
                denied = pd.get('decision') == 'reject' or dk is not None
                if denied:
                    if dk == 'user-rejected':
                        t.bump('o:denied_user')
                    else:
                        t.bump('o:denied_policy')
                    st2 = 'denied'
                elif b.get('is_error') is True:
                    st2 = 'error'
                else:
                    st2 = 'ok'
                if info is None:
                    continue
                name, labels, masked, alias = info
                if 'test' in labels and st2 != 'denied':
                    t.f['has_test'] = 1
                    t.f['last_test_ok'] = 1 if st2 == 'ok' else 0
                    if masked:
                        t.f['o:test_masked'] = 1
                if kind == 'main' and name in AGENT_TOOLS and isinstance(tur.get('agentId'), str) \
                        and alias is not None and isinstance(r.get('sessionId'), str):
                    scan.agent_join[(hash(r.get('sessionId')), hash(tur.get('agentId')))] = alias
            continue
        # assistant
        model = msg.get('model')
        if model == '<synthetic>':
            continue
        mid = msg.get('id') if isinstance(msg.get('id'), str) else r.get('requestId')
        new_req = True
        if isinstance(mid, str):
            hm = hash(mid)
            new_req = hm not in scan.seen_msg
            scan.seen_msg.add(hm)
        if new_req:
            m = gate_id(model) or '(other)'
            t.models[m] = t.models.get(m, 0) + 1
            eff = r.get('effort')
            e = eff if eff in EFFORTS_SEEN else 'none'
            t.efforts[e] = t.efforts.get(e, 0) + 1
            t.requests += 1
            us = msg.get('usage') if isinstance(msg.get('usage'), dict) else {}
            v = us.get('output_tokens')
            if isinstance(v, int) and not isinstance(v, bool) and 0 <= v < 10 ** 9:
                t.out_tok += v
        if t.start is None:
            t.start = r.get('timestamp')
        if t.cwd is None:
            t.cwd = cwd
        sr = msg.get('stop_reason')
        if sr in ('end_turn', 'tool_use', 'max_tokens', 'stop_sequence', 'pause_turn', 'refusal'):
            t.stop = sr
        elif sr is not None:
            t.stop = '(other)'
        for b in blocks:
            if not isinstance(b, dict) or b.get('type') != 'tool_use':
                continue
            cid = b.get('id')
            hc = hash(cid) if isinstance(cid, str) else None
            if hc is not None and hc in calls:
                continue
            name = b.get('name') if isinstance(b.get('name'), str) else None
            inp = b.get('input') if isinstance(b.get('input'), dict) else {}
            fam = family(name)
            t.bump('calls')
            t.bump('fam:' + fam)
            labels, masked, alias = [], False, None
            if fam in ('edit', 'read'):
                fp = inp.get('file_path') or inp.get('notebook_path') or inp.get('path')
                el = ext_label(fp)
                if el:
                    t.bump(fam + ':' + el)
                if fam == 'edit':
                    scan.note_edit(t, fp, cwd)
            elif fam == 'bash':
                labels, masked = bash_labels(inp.get('command'))
                for lab in labels:
                    t.bump('bash:' + lab)
            elif fam == 'agent':
                am = inp.get('model')
                if am is None:
                    scan.agent_no_model += 1
                else:
                    alias = hash(('alias', am)) if isinstance(am, str) else hash(('alias', '(other)'))
            if hc is not None:
                calls[hc] = (name, labels, masked, alias)
    if kind == 'sub' and sess_dir is not None and agent_id is not None:
        top = _top(task.models)
        if top:
            scan.sub_model[(hash(sess_dir), hash(agent_id))] = top


def scan_codex_file(scan, path):
    """One Codex rollout, one task. Ids fold to `(other)` (plan section 3: no Codex id in phase 2)."""
    scan.files['codex'] += 1
    t = scan.new_task('codex')
    for r in iter_records(path, scan):
        pl = r.get('payload') if isinstance(r.get('payload'), dict) else {}
        typ, ptp = r.get('type'), pl.get('type')
        ts = r.get('timestamp')
        scan.day(ts)
        if t.start is None and isinstance(ts, str):
            t.start = ts
        if typ == 'session_meta':
            t.ver(pl.get('cli_version'))
            if t.cwd is None:
                t.cwd = pl.get('cwd')
        elif typ == 'turn_context':
            t.models['(other)'] = t.models.get('(other)', 0) + 1
            e = pl.get('effort')
            e = e if e in EFFORTS_SEEN else 'none'
            t.efforts[e] = t.efforts.get(e, 0) + 1
            t.requests += 1
            if t.cwd is None:
                t.cwd = pl.get('cwd')
        elif typ == 'event_msg' and ptp == 'token_count':
            info = pl.get('info') if isinstance(pl.get('info'), dict) else {}
            tu = info.get('total_token_usage') if isinstance(info.get('total_token_usage'), dict) else {}
            v = tu.get('output_tokens')
            if isinstance(v, int) and not isinstance(v, bool) and t.out_tok < v < 10 ** 10:
                t.out_tok = v
        elif typ == 'event_msg' and ptp == 'turn_aborted':
            if pl.get('reason') == 'interrupted':
                t.interrupts += 1
        elif typ == 'event_msg' and ptp == 'item_completed':
            it = pl.get('item') if isinstance(pl.get('item'), dict) else {}
            ity = it.get('type')
            if ity == 'CommandExecution':
                t.bump('calls')
                t.bump('fam:bash')
                pcs = it.get('parsed_cmd') if isinstance(it.get('parsed_cmd'), list) else []
                kinds = [e.get('type') for e in pcs if isinstance(e, dict)]
                cmd = it.get('command')
                if isinstance(cmd, list):
                    cmd = cmd[-1] if cmd else ''
                labels, masked = bash_labels(cmd)
                for k3 in kinds:
                    if k3 in ('read', 'search', 'list_files'):
                        t.bump('bash:read')
                if not kinds or 'unknown' in kinds:
                    for lab in labels:
                        t.bump('bash:' + lab)
                ec = it.get('exit_code')
                if 'test' in labels and isinstance(ec, int) and not isinstance(ec, bool):
                    t.f['has_test'] = 1
                    t.f['last_test_ok'] = 1 if ec == 0 else 0
                    if masked:
                        t.f['o:test_masked'] = 1
            elif ity == 'FileChange':
                ch = it.get('changes')
                for k4 in (list(ch.keys()) if isinstance(ch, dict) else []):
                    t.bump('calls')
                    t.bump('fam:edit')
                    el = ext_label(k4)
                    if el:
                        t.bump('edit:' + el)
                    scan.note_edit(t, k4, t.cwd)
            elif ity == 'WebSearch' or (ity == 'Extension' and it.get('kind') == 'web.search'):
                t.bump('calls')
                t.bump('fam:web')
            elif ity == 'SubAgentActivity':
                t.bump('calls')
                t.bump('fam:agent')
            elif ity in ('McpToolCall', 'Mcp'):
                t.bump('calls')
                t.bump('fam:mcp')


def _listdir(d):
    return sorted(os.listdir(d))


# ABSENT ONLY ON ENOENT (cross-check O1). os.path.lexists, isdir and isfile answer False on ANY
# OSError, so `chmod 000 ~/.claude` read as `claude=absent` and a project directory without its
# search bit as no files, each exit 0 with a table: invariant 3's two verdicts collapsed into the
# silent arm. These raise instead, and the caller refuses the path.
def _isdir(p):
    try:
        return stat.S_ISDIR(os.stat(p).st_mode)
    except FileNotFoundError:
        return False


def source_state(d):
    """'absent' only when nothing is there; 'present' for a directory; else 'unreadable' (a
    permission error on the way, a dangling link, a file where a directory belongs)."""
    try:
        st = os.stat(d)
    except FileNotFoundError:
        try:
            os.lstat(d)
        except FileNotFoundError:
            return 'absent'
        except OSError:
            return 'unreadable'
        return 'unreadable'
    except OSError:
        return 'unreadable'
    return 'present' if stat.S_ISDIR(st.st_mode) else 'unreadable'


def _ordered(paths):
    """(mtime, path) order, so a resumed session's original file is read before its copy."""
    keyed = []
    for p in paths:
        try:
            keyed.append((os.stat(p).st_mtime, p))
        except OSError:
            keyed.append((0.0, p))
    return [p for _, p in sorted(keyed)]


def claude_files(projects, scan):
    """Every main and subagent transcript. An entry whose kind cannot be told is refused, never
    skipped (O1). A symlinked project or session directory is skipped, as the harness makes none."""
    mains, subs = [], []
    # A LISTED entry is judged by lstat, so one that vanished after the listing, or a `.jsonl`
    # link with no target, refuses like a file gone mid-read (Codex code pass A4: an ENOENT read
    # as "not a file" dropped it without a word).
    for proj in _listdir(projects):
        pd = os.path.join(projects, proj)
        try:
            if not stat.S_ISDIR(os.lstat(pd).st_mode):
                continue
            names = _listdir(pd)
        except OSError:
            scan.refused.append(pd)
            continue
        for n in names:
            p = os.path.join(pd, n)
            try:
                lst = os.lstat(p)
                if n.endswith('.jsonl'):
                    st = os.stat(p) if stat.S_ISLNK(lst.st_mode) else lst
                    if stat.S_ISREG(st.st_mode):
                        mains.append(p)
                    continue
                if not stat.S_ISDIR(lst.st_mode):
                    continue
                sd = os.path.join(p, 'subagents')
                if not _isdir(sd):
                    continue
            except OSError:
                scan.refused.append(p)
                continue
            for root, dirs, files in os.walk(sd, onerror=lambda e: scan.refused.append(getattr(e, 'filename', sd))):
                dirs.sort()
                for fn in sorted(files):
                    if fn.startswith('agent-') and fn.endswith('.jsonl'):
                        subs.append((os.path.join(root, fn), n, fn[len('agent-'):-len('.jsonl')]))
    return mains, subs


def codex_files(sessions, scan):
    out = []
    for root, dirs, files in os.walk(sessions, onerror=lambda e: scan.refused.append(getattr(e, 'filename', sessions))):
        dirs.sort()
        for fn in sorted(files):
            if fn.endswith('.jsonl'):
                out.append(os.path.join(root, fn))
    return out


def in_scope(scan, cwd):
    if scan.cwd_under is None:
        return True
    if not isinstance(cwd, str):
        return False
    c = fold(os.path.normpath(cwd))
    for d in scan.cwd_under:
        if c == d or c.startswith(d.rstrip('/') + '/'):
            return True
    return False


# ---------------------------------------------------------------- the table
COLUMNS = ('role', 'class', 'tier', 'model', 'eligible', 'tasks', 'last7d', 'calls', 'out_ktok', 'tested',
           'interrupt', 'user_reject', 'policy_denied', 'test_red_end', 'test_masked', 'abandoned')
HEADER_KEYS = ('made', 'window', 'claude', 'codex', 'main_files', 'sub_files', 'codex_files', 'torn_last',
               'conflicts', 'conflict_tasks', 'no_request', 'scoped', 'cap', 'aliases_multi', 'agent_no_model',
               'script')
N = r'(0|[1-9][0-9]{0,11})'
HEADER_RE = {
    'made': r'[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z',
    'window': r'(none|[0-9]{4}-[0-9]{2}-[0-9]{2}\.\.[0-9]{4}-[0-9]{2}-[0-9]{2})',
    'claude': r'(present|absent)', 'codex': r'(present|absent)', 'scoped': r'(yes|no)',
    'cap': r'(none|[0-9a-f]{12})', 'script': r'[0-9a-f]{12}',
}
SIG = r'(-|' + N + '/' + N + ')'


def model_ok(s):
    if '@' not in s:
        return False
    i, e = s.rsplit('@', 1)
    return (i == '(other)' or gate_id(i) is not None) and (e in EFFORTS_SEEN or e == 'none')


def row_ok(cells):
    if len(cells) != len(COLUMNS):
        return False
    role, cls, tier, model, elig = cells[:5]
    if role not in ROLES or cls not in SCAN_CLASSES or tier != '-' or elig not in ('yes', 'no'):
        return False
    if not model_ok(model):
        return False
    if not all(re.match(N + r'\Z', c) for c in cells[5:10]):
        return False
    return all(re.match(SIG + r'\Z', c) for c in cells[10:])


def render_table(header, rows):
    lines = ['#model-router-table\t' + FORMAT,
             '#header\t' + '\t'.join('%s=%s' % (k, header[k]) for k in HEADER_KEYS),
             '\t'.join(COLUMNS)]
    lines += ['\t'.join(r) for r in rows]
    body = ('\n'.join(lines) + '\n').encode('ascii')
    trailer = '#end\trows=%d\tsha256=%s\n' % (len(rows), hashlib.sha256(body).hexdigest())
    return body + trailer.encode('ascii')


def read_table(path):
    """The table, validated as it is READ (plan section 7; Codex C8): a missing trailer, a count or
    hash that does not match, an unknown version, a value outside the vocabulary or two rows with
    one cell key make the file unreadable. Returns (header dict, rows, sha256 hex of the bytes
    read), or raises Refused. The digest is of the bytes validated here, never of a second read
    that could see a file replaced in between."""
    try:
        with open(path, 'rb') as fh:
            data = fh.read(MAX_TABLE + 1)
    except OSError:
        raise Refused('table-unreadable')
    # One byte past the limit refuses (Codex code pass B2): a read capped AT the limit validated
    # a prefix and ignored the rest of the file.
    if len(data) > MAX_TABLE:
        raise Refused('table-too-large')
    if any(c > 0x7e or (c < 0x20 and c not in (0x09, 0x0a)) for c in data):
        raise Refused('table-bad-byte')
    text = data.decode('ascii')
    if not text.endswith('\n'):
        raise Refused('table-no-trailer')
    lines = text[:-1].split('\n')
    if len(lines) < 4 or not lines[-1].startswith('#end\t'):
        raise Refused('table-no-trailer')
    m = re.match(r'#end\trows=' + N + r'\tsha256=([0-9a-f]{64})\Z', lines[-1])
    if not m:
        raise Refused('table-no-trailer')
    body = ('\n'.join(lines[:-1]) + '\n').encode('ascii')
    if hashlib.sha256(body).hexdigest() != m.group(2):
        raise Refused('table-hash')
    if lines[0] != '#model-router-table\t' + FORMAT:
        raise Refused('table-version')
    hp = lines[1].split('\t')
    if hp[0] != '#header' or len(hp) != len(HEADER_KEYS) + 1:
        raise Refused('table-header')
    header = {}
    for k, kv in zip(HEADER_KEYS, hp[1:]):
        if not kv.startswith(k + '='):
            raise Refused('table-header')
        v = kv[len(k) + 1:]
        if not re.match(HEADER_RE.get(k, N) + r'\Z', v):
            raise Refused('table-header')
        header[k] = v
    if lines[2] != '\t'.join(COLUMNS):
        raise Refused('table-columns')
    rows = [ln.split('\t') for ln in lines[3:-1]]
    if len(rows) != int(m.group(1)):
        raise Refused('table-row-count')
    keys = set()
    per_id = {}
    for r in rows:
        if not row_ok(r):
            raise Refused('table-bad-row')
        k = tuple(r[:5])
        if k in keys:
            raise Refused('table-duplicate-cell')
        keys.add(k)
        i = r[3].rsplit('@', 1)[0]
        if r[0] == 'codex' and i != '(other)':
            raise Refused('table-naming')
        if r[0] != 'codex' and i != '(other)':
            per_id[i] = per_id.get(i, 0) + int(r[5])
    # The naming rule, checked where it can be (Codex code pass B1): a table made before any cap
    # names only ids past NAME_FLOOR kept main and subagent tasks, at most MAX_NAMED, exactly as
    # model_names does. Under a cap the cap's ids are named at any count, and the table holds
    # only the cap's hash, so there cap-template applies the floor itself.
    if header['cap'] == 'none' and (len(per_id) > MAX_NAMED or any(n < NAME_FLOOR for n in per_id.values())):
        raise Refused('table-naming')
    return header, rows, hashlib.sha256(data).hexdigest()


# ---------------------------------------------------------------- scan: build and print
def model_names(scan, drop):
    """Which ids are printed by name (plan section 3; Codex C1): under a cap, only the cap's own
    ids; before one exists, an id that passed the gate and leads at least NAME_FLOOR of the tasks
    the table KEEPS (main and subagent, in scope, not dropped as a conflict), since the user needs
    those names to write the cap; and at most MAX_NAMED of them, the most-used first (panel F1:
    unbounded, a planted store named every id it held, and 28 KB printed). Repetition is not
    approval once a cap exists. A writer of the transcript store can still choose the draft's
    default by planting enough tasks; the draft is undated data, which the user dates."""
    if scan.cap is not None:
        return cap_ids(scan.cap)
    n = {}
    for t in scan.tasks:
        if t.tid in drop or t.role == 'codex' or not in_scope(scan, t.cwd):
            continue
        m = _top(t.models)
        if m and m != '(other)':
            n[m] = n.get(m, 0) + 1
    ranked = sorted(((c, m) for m, c in n.items() if c >= NAME_FLOOR), key=lambda x: (-x[0], x[1]))
    return set(m for _, m in ranked[:MAX_NAMED])


def build_rows(scan, now):
    drop = set()
    for h in scan.conflicted:
        drop.add(scan.seen[h][1])
        drop.update(scan.more_owners.get(h, ()))
    drop.discard(0)
    named = model_names(scan, drop)
    cells = {}
    recent = now - datetime.timedelta(days=7)
    no_request = 0
    kept = 0
    for t in scan.tasks:
        if t.tid in drop:
            continue
        if not in_scope(scan, t.cwd):
            continue
        if not t.models:
            no_request += 1
            continue
        kept += 1
        m = _top(t.models)
        e = _top(t.efforts) or 'none'
        label = (m if m in named else '(other)') + '@' + e
        cls = classify(t.f)
        if scan.cap is not None and t.role != 'codex' and cls not in EDITING \
                and any(same_model(m, v) for v in scan.cap.review) \
                and not any(t.f.get('edit:' + k) for k in EDIT_LABELS):
            cls = 'review'
        key = (t.role, cls, '-', label, 'no' if t.protected else 'yes')
        c = cells.get(key)
        if c is None:
            c = cells[key] = dict((k, 0) for k in ('tasks', 'last7d', 'calls', 'out_tok', 'tested', 'int_f', 'int_c',
                                                   'rej_f', 'rej_c', 'pol_f', 'pol_c', 'red', 'mask', 'ab_f'))
        c['tasks'] += 1
        st = parse_ts(t.start)
        if st is not None and st >= recent:
            c['last7d'] += 1
        c['calls'] += t.f.get('calls', 0)
        c['out_tok'] += t.out_tok
        tested = bool(t.f.get('has_test'))
        c['tested'] += tested
        if tested and not t.f.get('last_test_ok'):
            c['red'] += 1
        if tested and t.f.get('o:test_masked'):
            c['mask'] += 1
        if t.role == 'codex':
            if t.covered(CODEX_RANGES['interrupt']):
                c['int_c'] += 1
                c['int_f'] += bool(t.interrupts)
        else:
            if t.role == 'main' and t.covered(CLAUDE_RANGES['interrupt']):
                c['int_c'] += 1
                c['int_f'] += bool(t.interrupts)
            if t.covered(CLAUDE_RANGES['permission']):
                c['rej_c'] += 1
                c['rej_f'] += bool(t.f.get('o:denied_user'))
                c['pol_c'] += 1
                c['pol_f'] += bool(t.f.get('o:denied_policy'))
        if t.role == 'main' and t.stop == 'tool_use':
            c['ab_f'] += 1
    rows = []
    for key in sorted(cells):
        c = cells[key]
        role = key[0]
        fc = lambda f, n: '%d/%d' % (f, n)
        rows.append(list(key) + [str(c['tasks']), str(c['last7d']), str(c['calls']), str(c['out_tok'] // 1000),
                                 str(c['tested']),
                                 fc(c['int_f'], c['int_c']) if role != 'sub' else '-',
                                 fc(c['rej_f'], c['rej_c']) if role != 'codex' else '-',
                                 fc(c['pol_f'], c['pol_c']) if role != 'codex' else '-',
                                 fc(c['red'], c['tested']), fc(c['mask'], c['tested']),
                                 fc(c['ab_f'], c['tasks']) if role == 'main' else '-'])
    aliases = {}
    for k, a in scan.agent_join.items():
        m = scan.sub_model.get(k)
        if m:
            aliases.setdefault(a, set()).add(m)
    multi = sum(1 for s in aliases.values() if len(s) > 1)
    return rows, len(drop), no_request, multi, kept


def script_sha():
    try:
        with open(os.path.abspath(__file__), 'rb') as fh:
            return hashlib.sha256(fh.read()).hexdigest()[:12]
    except (OSError, NameError):
        return '0' * 12


def cmd_scan(opts, env, now, out):
    sd = state_dir(env)
    table = resolved_target(opts.get('--out') or os.path.join(sd, 'table.tsv'))
    refused_txt = resolved_target(os.path.join(sd, 'refused.txt'))
    # Every write target is judged before anything is read or written (R3).
    check_target(table, table=True)
    check_target(refused_txt)
    # A source NAMED inside a work tree is refused (panel F2): a repository's planted transcripts,
    # scanned on its own text's word, would become the default table cap-template drafts from.
    # The defaults (CLAUDE_CONFIG_DIR, ~/.claude) are the harness's own, versioned or not.
    for f in ('--claude', '--codex'):
        if opts.get(f) and path_in_work_tree(opts[f]):
            raise Refused('source-in-work-tree')
    cap = None
    if opts.get('--cap'):
        try:
            cap = read_cap(opts['--cap'], now.date())
        except Refused as e:
            out.write(NOTICE + '\n')
            out.write('scan: refused: the cap given with --cap is not usable: %s\n' % cap_reason(e.code))
            return 2
    cwd_under = None
    if opts.get('--cwd-under'):
        d = opts['--cwd-under']
        cwd_under = sorted(set((fold(os.path.normpath(os.path.abspath(d))),
                                fold(os.path.realpath(d)))))
    scan = Scan(cap, cwd_under)
    home = env.get('HOME') or os.path.expanduser('~')
    cfg = opts.get('--claude') or env.get('CLAUDE_CONFIG_DIR') or os.path.join(home, '.claude')
    cdx = opts.get('--codex') or env.get('CODEX_HOME') or os.path.join(home, '.codex')
    projects = os.path.join(cfg, 'projects')
    sessions = os.path.join(cdx, 'sessions')
    state = {}
    for name, d in (('claude', projects), ('codex', sessions)):
        state[name] = source_state(d)
        if state[name] == 'unreadable':
            scan.refused.append(d)
    mains, subs, cxs = [], [], []
    if state['claude'] == 'present':
        try:
            mains, subs = claude_files(projects, scan)
        except OSError:
            scan.refused.append(projects)
    if state['codex'] == 'present':
        try:
            cxs = codex_files(sessions, scan)
        except OSError:
            scan.refused.append(sessions)
    for p in _ordered(mains):
        try:
            scan_claude_file(scan, 'main', p, None, None)
        except FILE_ERRORS:
            scan.refused.append(p)
    subs_by = dict((p, (sd_, aid)) for p, sd_, aid in subs)
    for p in _ordered(list(subs_by)):
        try:
            scan_claude_file(scan, 'sub', p, subs_by[p][0], subs_by[p][1])
        except FILE_ERRORS:
            scan.refused.append(p)
    for p in _ordered(cxs):
        try:
            scan_codex_file(scan, p)
        except FILE_ERRORS:
            scan.refused.append(p)
    out.write(NOTICE + '\n')
    if scan.refused:
        body = b''.join(pct_path(p) + b'\n' for p in sorted(set(scan.refused)))
        write_atomic(refused_txt, body)
        # A refused scan leaves no table behind it (panel O2): an older one, still in place, was
        # what cap-template drafted from next. Only the script's own file goes: check_target
        # already found it to be a regular file that starts with the table's magic.
        left = False
        try:
            if os.path.isfile(table) and not os.path.islink(table):
                with open(table, 'rb') as fh:
                    if fh.read(len(TABLE_MAGIC)) == TABLE_MAGIC:
                        os.unlink(table)
        except OSError:
            left = True
        srcs = []
        for name, roots in (('claude', (projects,)), ('codex', (sessions,))):
            n = sum(1 for p in set(scan.refused) if any(p == r or p.startswith(r + os.sep) for r in roots))
            if n:
                srcs.append('%s %d' % (name, n))
        out.write('scan: refused: %s path(s) could not be read whole (%s); their paths are in %s; '
                  'repair or move them and scan again. No table was written%s.\n'
                  % (len(set(scan.refused)), ', '.join(srcs) or 'other', fence(refused_txt),
                     '; an earlier table could not be removed: delete it before cap-template' if left
                     else ', and none is left in its place'))
        return 2
    rows, conflict_tasks, no_request, multi, kept = build_rows(scan, now)
    window = 'none' if scan.dmin is None else '%s..%s' % (scan.dmin.isoformat(), scan.dmax.isoformat())
    header = {
        'made': now.strftime('%Y-%m-%dT%H:%M:%SZ'), 'window': window,
        'claude': state['claude'], 'codex': state['codex'],
        'main_files': scan.files['main'], 'sub_files': scan.files['sub'], 'codex_files': scan.files['codex'],
        'torn_last': scan.torn_last, 'conflicts': len(scan.conflicted), 'conflict_tasks': conflict_tasks,
        'no_request': no_request, 'scoped': 'yes' if cwd_under else 'no',
        'cap': cap.sha[:12] if cap else 'none', 'aliases_multi': multi, 'agent_no_model': scan.agent_no_model,
        'script': script_sha(),
    }
    data = render_table(header, rows)
    write_atomic(table, data, True)
    try:
        if os.path.isfile(refused_txt) and not os.path.islink(refused_txt):
            os.unlink(refused_txt)
    except OSError:
        pass
    print_summary(out, header, rows, table, hashlib.sha256(data).hexdigest())
    return 0


def _agg(rows, role, keyf):
    d = {}
    for r in rows:
        if r[0] == role:
            k = keyf(r)
            d[k] = d.get(k, 0) + int(r[5])
    return d


def _fmt(d, limit=None):
    items = sorted(d.items(), key=lambda kv: (-kv[1], kv[0]))
    if limit:
        items = items[:limit]
    return ' '.join('%s=%d' % kv for kv in items)


def print_summary(out, h, rows, table, sha):
    w = out.write
    w('sources: claude=%s main_files=%s sub_files=%s codex=%s codex_files=%s torn_last=%s conflicts=%s '
      'conflict_tasks=%s scoped=%s cap=%s\n' % (h['claude'], h['main_files'], h['sub_files'], h['codex'],
                                                h['codex_files'], h['torn_last'], h['conflicts'],
                                                h['conflict_tasks'], h['scoped'], h['cap']))
    tasks = dict((role, sum(int(r[5]) for r in rows if r[0] == role)) for role in ROLES)
    w('window: %s; tasks: main=%d sub=%d codex=%d; no model request: %s\n'
      % (h['window'], tasks['main'], tasks['sub'], tasks['codex'], h['no_request']))
    w('table: %s sha256=%s\n' % (fence(table), sha[:12]))
    for role in ('main', 'sub', 'codex'):
        if not tasks[role]:
            continue
        w('%s tasks by class: %s\n' % (role, _fmt(_agg(rows, role, lambda r: r[1]))))
    w('main tasks by model: %s\n' % _fmt(_agg(rows, 'main', lambda r: r[3].rsplit('@', 1)[0])))
    w('main tasks by effort: %s\n' % _fmt(_agg(rows, 'main', lambda r: r[3].rsplit('@', 1)[1])))
    for cls in ROUTABLE:
        d = _agg([r for r in rows if r[1] == cls], 'main', lambda r: r[3].rsplit('@', 1)[0])
        if len([v for v in d.values() if v >= DRAFT_FLOOR]) > 1:
            w('split, %s (confounded: chosen per task): %s\n' % (cls, _fmt(d, 3)))
    def sig(col, role):
        f = c = 0
        for r in rows:
            if r[0] == role and r[col] != '-':
                a, b = r[col].split('/')
                f += int(a)
                c += int(b)
        return '%d/%d' % (f, c)
    w('signals, main (tasks fired/covered): interrupt=%s user_reject=%s policy_denied=%s test_red_end=%s '
      'test_masked=%s abandoned=%s\n' % (sig(10, 'main'), sig(11, 'main'), sig(12, 'main'), sig(13, 'main'),
                                         sig(14, 'main'), sig(15, 'main')))
    w('agent calls naming no model: %s; aliases that resolved to more than one id: %s\n'
      % (h['agent_no_model'], h['aliases_multi']))
    w('next: router.py cap-template, which prints a draft for you to edit; nothing is written to your instructions\n')


# ---------------------------------------------------------------- cap-template
def cmd_cap_template(opts, env, now, out):
    out.write(NOTICE + '\n')
    path = opts.get('--table') or os.path.join(state_dir(env), 'table.tsv')
    # A table NAMED inside a work tree is refused (panel F2): a repository can ship a forged one
    # that validates, and its text can ask for it to be drafted from.
    if opts.get('--table') and path_in_work_tree(path):
        out.write('cap-template: refused: the table cannot be used (table-in-work-tree); run router.py scan first\n')
        return 2
    try:
        header, rows, table_sha = read_table(path)
    except Refused as e:
        out.write('cap-template: refused: the table cannot be used (%s); run router.py scan first\n' % e.code)
        return 2
    # The naming rule again, on read (panel F2): a table written by hand can name any gated id on
    # one task. An id is drafted only past NAME_FLOOR main and subagent tasks, and at most
    # MAX_NAMED of them, as the scan names them.
    per_id = {}
    for r in rows:
        if r[0] in ('main', 'sub'):
            i = r[3].rsplit('@', 1)[0]
            per_id[i] = per_id.get(i, 0) + int(r[5])
    ranked = sorted(((n, i) for i, n in per_id.items() if i != '(other)' and n >= NAME_FLOOR),
                    key=lambda x: (-x[0], x[1]))
    nameable = set(i for _, i in ranked[:MAX_NAMED])
    main = [r for r in rows if r[0] == 'main']
    by_mid = {}
    for r in main:
        i, e = r[3].rsplit('@', 1)
        if i in nameable:
            by_mid[(i, e)] = by_mid.get((i, e), 0) + int(r[5])
    if not by_mid:
        out.write('cap-template: no draft: no model is named in the table (none led %d or more tasks)\n'
                  % NAME_FLOOR)
        return 1
    d_id, d_eff = sorted(by_mid.items(), key=lambda kv: (-kv[1], kv[0]))[0][0]
    default = d_id + ('@' + d_eff if d_eff in EFFORTS_CAP else '')
    assign, notes = [], []
    for cls in ROUTABLE:
        ids, effs = {}, {}
        for r in main:
            if r[1] != cls:
                continue
            i, e = r[3].rsplit('@', 1)
            ids[i] = ids.get(i, 0) + int(r[5])
            effs[(i, e)] = effs.get((i, e), 0) + int(r[5])
        others = [(n, i) for i, n in ids.items() if i in nameable and i != d_id and n >= DRAFT_FLOOR]
        if not others:
            continue
        n, i = sorted(others, key=lambda x: (-x[0], x[1]))[0]
        e = sorted(((c, ee) for (ii, ee), c in effs.items() if ii == i), key=lambda x: (-x[0], x[1]))[0][1]
        assign.append('%s:%s' % (cls, i + ('@' + e if e in EFFORTS_CAP else '')))
        notes.append('# %s: %s led %d of %d main tasks; confounded: chosen per task, not shown better'
                     % (cls, i, n, sum(ids.values())))
    seen = sorted(set(i for (i, _), n in by_mid.items()))
    # CHANGED in phase 2 (measured, M17): the observed splits are CANDIDATES on a `#` line, never
    # in the draft line itself. On the machine this shipped from, the floor of 10 drafted five
    # assignments: two put a review model on a class, and three contradicted the user's own
    # rules. Dated unedited, that draft would have routed all five. A user now opts
    # into each route by moving it into the line; dated unedited, the draft routes nothing.
    line = 'route-cap default=%s decided=' % default
    out.write('cap-template: a draft from table sha256=%s (window %s). It is undated, so it parses as no cap.\n'
              % (table_sha[:12], header['window']))
    out.write('Edit it, date decided=, and save it yourself in your own instructions; this script writes nothing.\n')
    out.write(line + '\n')
    if assign:
        out.write('# assign= candidates, left out of the line: add only a route you choose: assign=%s\n'
                  % ','.join(assign))
    for nl in notes:
        out.write(nl + '\n')
    out.write('# review= (blank): list the models you keep for reviews; ids named in the table: %s\n' % ' '.join(seen))
    out.write('# trial= (blank): trials are not built in this version\n')
    out.write('# tiers= (blank): without it, a pick that may leave the default needs --tier none\n')
    return 0


# ---------------------------------------------------------------- arguments
SPEC = {
    'scan': {'--claude': 1, '--codex': 1, '--out': 1, '--cap': 1, '--cwd-under': 1},
    'cap-template': {'--table': 1},
    'pick': {'--cap': 1, '--role': 1, '--class': 1, '--touch': 2, '--tier': 1, '--all': 0},
}
USAGE = '''usage: router.py scan [--claude <dir>] [--codex <dir>] [--out <file>] [--cap <file>] [--cwd-under <dir>]
       router.py cap-template [--table <file>]
       router.py pick --cap <file> --role main|sub (--class <c> [--touch <path>]... | --all) [--tier chore|lite|full|none]
       router.py --self-test
classes: code writing research data ops qa review (docs reads as writing, test as code)
'''


def parse_args(argv):
    """Returns (verb, opts) or raises Refused. Every refusal text is a constant; a flag is named
    only from SPEC, never from argv (design pass D1)."""
    if not argv:
        raise Refused('usage: no verb')
    verb = argv[0]
    if verb in ('-h', '--help'):
        return 'help', {}
    if verb == '--self-test':
        if len(argv) != 1:
            raise Refused('usage: --self-test takes no argument')
        return 'self-test', {}
    if verb not in SPEC:
        raise Refused('usage: unknown verb')
    spec = SPEC[verb]
    opts = {}
    i = 1
    while i < len(argv):
        a = argv[i]
        if a in ('-h', '--help'):
            return 'help', {}
        flag = next((f for f in spec if f == a), None)
        if flag is None:
            raise Refused('usage: unknown argument for %s' % verb)
        if spec[flag] == 0:
            if flag in opts:
                raise Refused('usage: %s given twice' % flag)
            opts[flag] = True
            i += 1
            continue
        if i + 1 >= len(argv) or argv[i + 1].startswith('--') or argv[i + 1] == '':
            raise Refused('usage: %s needs a value' % flag)
        v = argv[i + 1]
        if spec[flag] == 2:
            opts.setdefault(flag, []).append(v)
        else:
            if flag in opts:
                raise Refused('usage: %s given twice' % flag)
            opts[flag] = v
        i += 2
    if verb == 'pick':
        if opts.get('--role') not in ('main', 'sub'):
            raise Refused('usage: pick needs --role main or --role sub')
        if opts.get('--tier') is not None and opts['--tier'] not in TIER_ARGS:
            raise Refused('usage: --tier is one of chore lite full none')
        if opts.get('--all') and ('--class' in opts or '--touch' in opts):
            raise Refused('usage: --all takes no --class or --touch')
        if not opts.get('--all'):
            c = opts.get('--class')
            if c is None:
                raise Refused('usage: pick needs --class or --all')
            c = SYNONYMS.get(c, c)
            if c not in ROUTABLE + ('review',):
                raise Refused('usage: --class is one of code writing research data ops qa review')
            opts['--class'] = c
    return verb, opts


def main(argv, env=None, now=None, out=None, err=None):
    env = os.environ if env is None else env
    # UTC, naive. Not utcnow(): Python 3.12 deprecates it, and the warning would print on stderr
    # at every run of a script that is __main__.
    now = now or datetime.datetime.now(datetime.timezone.utc).replace(tzinfo=None, microsecond=0)
    out = out or sys.stdout
    err = err or sys.stderr
    try:
        verb, opts = parse_args(argv)
    except Refused as e:
        err.write('router.py: refused: %s\n' % e.code)
        err.write(USAGE)
        return 2
    try:
        if verb == 'help':
            out.write(USAGE)
            return 0
        if verb == 'self-test':
            return self_test(out)
        if verb == 'scan':
            return cmd_scan(opts, env, now, out)
        if verb == 'cap-template':
            return cmd_cap_template(opts, env, now, out)
        return cmd_pick(opts, env, now, out)
    except Refused as e:
        err.write('router.py: refused: %s\n' % e.code)
        if e.code.startswith('target-'):
            # A home directory, or the state directory, inside a work tree (a dotfiles checkout)
            # had no remedy in the print (panel, portability).
            err.write('router.py: set XDG_STATE_HOME to an absolute directory outside any work tree, '
                      'or name a table file there with the scan\'s out flag\n')
        elif e.code == 'source-in-work-tree':
            err.write('router.py: a transcript directory named inside a work tree is not your own history\n')
        return 2
    except Exception as e:  # the type only, never the message (F15)
        err.write('router.py: internal error %s\n' % type(e).__name__)
        return 3


# ================================================================ self-test
CANARY = 'CANARY7F3AQ'


class Fx(object):
    """A fixture home: a temp dir with a Claude config, a Codex home and a state dir, outside any
    git work tree (the work-tree refusals would otherwise fire on the fixtures themselves)."""

    def __init__(self, base):
        self.base = tempfile.mkdtemp(prefix='mr-', dir=base)
        self.claude = os.path.join(self.base, 'claude')
        self.codex = os.path.join(self.base, 'codex')
        self.state = os.path.join(self.base, 'state')
        self.env = {'HOME': self.base, 'XDG_STATE_HOME': self.state}

    def proj(self, name='-x-proj'):
        d = os.path.join(self.claude, 'projects', name)
        os.makedirs(d, exist_ok=True)
        return d

    def write(self, path, records, tail=None):
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, 'wb') as fh:
            for r in records:
                fh.write((r if isinstance(r, str) else json.dumps(r)).encode('utf-8') + b'\n')
            if tail is not None:
                fh.write(tail.encode('utf-8'))
        return path

    def run(self, argv, now=None):
        o, e = io.StringIO(), io.StringIO()
        rc = main(argv, env=self.env, now=now or NOW, out=o, err=e)
        return rc, o.getvalue(), e.getvalue()

    def scan(self, *extra):
        return self.run(['scan', '--claude', self.claude, '--codex', self.codex] + list(extra))

    def table(self):
        p = os.path.join(self.state, 'model-router', 'table.tsv')
        if not os.path.exists(p):
            return ''
        with open(p, 'rb') as fh:
            return fh.read().decode('ascii')


NOW = datetime.datetime(2026, 11, 12, 6, 0, 0)
_seq = [0]


def _id(p):
    _seq[0] += 1
    return '%s-%d' % (p, _seq[0])


def episode(sid='s1', model='claude-opus-5-5', effort='high', tools=(), ver='2.1.310', cwd='/x/proj',
            text='a prompt', origin='human', ts='2026-11-10T10:00:00.000Z', results=None, extra_user=None,
            stop='end_turn'):
    """One human prompt, one assistant request with tool calls, and their results."""
    recs = [{'type': 'user', 'uuid': _id('u'), 'sessionId': sid, 'timestamp': ts, 'version': ver, 'cwd': cwd,
             'turnOrigin': origin, 'message': {'role': 'user', 'content': text}}]
    uses = []
    for name, inp in tools:
        uses.append({'type': 'tool_use', 'id': _id('tu'), 'name': name, 'input': inp})
    recs.append({'type': 'assistant', 'uuid': _id('a'), 'sessionId': sid, 'timestamp': ts, 'version': ver,
                 'cwd': cwd, 'effort': effort,
                 'message': {'id': _id('msg'), 'model': model, 'role': 'assistant', 'stop_reason': stop,
                             'usage': {'output_tokens': 1500}, 'content': [{'type': 'text', 'text': text}] + uses}})
    for i, u in enumerate(uses):
        res = (results or {}).get(i, {})
        rec = {'type': 'user', 'uuid': _id('u'), 'sessionId': sid, 'timestamp': ts, 'version': ver, 'cwd': cwd,
               'message': {'role': 'user', 'content': [{'type': 'tool_result', 'tool_use_id': u['id'],
                                                        'is_error': res.get('is_error', False),
                                                        'content': res.get('text', 'result text')}]}}
        for k in ('permissionDecision', 'toolDenialKind', 'toolUseResult'):
            if k in res:
                rec[k] = res[k]
        recs.append(rec)
    if extra_user:
        recs.append(extra_user)
    return recs


def codex_rollout(model='gpt-x', effort='xhigh', cmds=(), files=(), ver='0.166.0', interrupted=False,
                  cwd='/x/proj'):
    recs = [{'timestamp': '2026-11-10T10:00:00Z', 'type': 'session_meta',
             'payload': {'originator': 'codex_exec', 'cli_version': ver, 'cwd': cwd}},
            {'timestamp': '2026-11-10T10:00:01Z', 'type': 'turn_context',
             'payload': {'model': model, 'effort': effort, 'sandbox_policy': {'type': 'read-only'}}}]
    for c, ec in cmds:
        recs.append({'timestamp': '2026-11-10T10:00:02Z', 'type': 'event_msg',
                     'payload': {'type': 'item_completed',
                                 'item': {'type': 'CommandExecution', 'command': ['bash', '-lc', c],
                                          'parsed_cmd': [{'type': 'unknown', 'cmd': c}], 'exit_code': ec,
                                          'aggregated_output': 'output text'}}})
    if files:
        recs.append({'timestamp': '2026-11-10T10:00:03Z', 'type': 'event_msg',
                     'payload': {'type': 'item_completed',
                                 'item': {'type': 'FileChange', 'status': 'completed',
                                          'changes': dict((f, {'diff': 'diff text'}) for f in files)}}})
    if interrupted:
        recs.append({'timestamp': '2026-11-10T10:00:04Z', 'type': 'event_msg',
                     'payload': {'type': 'turn_aborted', 'reason': 'interrupted'}})
    return recs


class T(object):
    def __init__(self, out):
        self.out, self.fails, self.n = out, [], 0

    def ok(self, cond, name):
        self.n += 1
        if not cond:
            self.fails.append(name)
            self.out.write('FAIL %s\n' % name)


def _cap(fx, line, name='cap.md', extra=''):
    d = os.path.join(fx.base, 'home-cfg')
    os.makedirs(d, exist_ok=True)
    p = os.path.join(d, name)
    with open(p, 'w') as fh:
        fh.write('# my instructions\n' + extra + line + '\n')
    return p


GOOD = 'route-cap default=claude-opus-5-5@high assign=qa:claude-sonnet-5-5,code:claude-sonnet-5-5 decided=2026-11-01'
GOOD_TRIAL = (' trial=claude-sonnet-5-5@medium classes=writing floor=20 events=3 worse=2x starts=2026-11-10 '
              'ends=2026-11-20')


def t_cap_refusals(t, fx):
    """Each refusal, with its positive control: the same line with that one defect removed parses."""
    today = NOW.date()
    cases = [
        # (bad line, its control, code)
        (GOOD + ' decided=2026-11-02', GOOD, 'duplicate-key'),
        (GOOD + ' colour=red', GOOD, 'unknown-key'),
        (GOOD.replace('decided=2026-11-01', 'decided='), GOOD, 'undated'),
        (GOOD.replace(' decided=2026-11-01', ''), GOOD, 'no-decided'),
        (GOOD.replace('2026-11-01', '2026-02-30'), GOOD, 'bad-date'),
        (GOOD.replace('default=claude-opus-5-5@high', 'default=opus'), GOOD, 'bad-id'),
        (GOOD.replace('default=claude-opus-5-5@high', 'default=gpt-6-astra'), GOOD, 'bad-id'),
        (GOOD.replace('default=claude-opus-5-5@high', 'default=claude-opus'), GOOD, 'bad-id'),
        (GOOD.replace('default=claude-opus-5-5@high', 'default=claude-opus-5-5[1m]'), GOOD, 'bad-id'),
        (GOOD.replace('decided=2026-11-01', 'decided=2026-11-01\x01'), GOOD, 'bad-byte'),
        (GOOD.replace('default=claude-opus-5-5@high', 'default=claude-3-7-sonnet-latest'), GOOD, 'bad-id'),
        (GOOD.replace('default=claude-opus-5-5@high ', ''), GOOD, 'no-default'),
        ('route-cap roles=sub' + GOOD[9:], GOOD, 'roles-sub'),
        ('route-cap roles=main,sub' + GOOD[9:], 'route-cap roles=main' + GOOD[9:], 'roles-sub'),
        ('route-cap roles=lane' + GOOD[9:], GOOD, 'bad-roles'),
        (GOOD.replace('qa:claude-sonnet-5-5', 'review:claude-sonnet-5-5'), GOOD, 'bad-class'),
        (GOOD.replace('qa:claude-sonnet-5-5', 'poetry:claude-sonnet-5-5'), GOOD, 'bad-class'),
        (GOOD.replace('qa:claude-sonnet-5-5', 'test:claude-sonnet-5-5'), GOOD, 'class-assigned-twice'),
        (GOOD.replace('assign=qa:', 'assign=,qa:'), GOOD, 'empty-element'),
        (GOOD.replace('qa:claude-sonnet-5-5', 'qa:sonnet'), GOOD, 'bad-id'),
        (GOOD + ' review=claude-sonnet-5-5', GOOD + ' review=claude-fable-5-2', 'review-id-routed'),
        (GOOD + ' review=claude-fable-5-2@xhigh', GOOD + ' review=claude-fable-5-2', 'review-effort'),
        (GOOD + ' review=claude-opus-5-5', GOOD + ' review=claude-fable-5-2', 'review-id-routed'),
        (GOOD + ' tiers=full', GOOD + ' tiers=chore', 'bad-tier'),
        (GOOD + ' tiers=none', GOOD + ' tiers=lite', 'bad-tier'),
        (GOOD + ' tiers=chore,chore', GOOD + ' tiers=chore,lite', 'repeated-element'),
        (GOOD + ' protect=../x', GOOD + ' protect=x', 'bad-protect'),
        (GOOD + ' protect=a b', GOOD + ' protect=a', 'not-key-value'),
        (GOOD + ' ', GOOD, 'empty-token'),
        (GOOD + '\t', GOOD, 'bad-byte'),
        (GOOD + ' x' * 600, GOOD, 'line-too-long'),
        (GOOD + GOOD_TRIAL.replace(' floor=20', ''), GOOD + GOOD_TRIAL, 'trial-incomplete'),
        (GOOD + GOOD_TRIAL.replace('floor=20', 'floor=0'), GOOD + GOOD_TRIAL, 'number-out-of-range'),
        (GOOD + GOOD_TRIAL.replace('floor=20', 'floor=1001'), GOOD + GOOD_TRIAL, 'number-out-of-range'),
        (GOOD + GOOD_TRIAL.replace('worse=2x', 'worse=2'), GOOD + GOOD_TRIAL, 'number-out-of-range'),
        (GOOD + GOOD_TRIAL.replace('ends=2026-11-20', 'ends=2026-11-11'), GOOD + GOOD_TRIAL, 'expired'),
        (GOOD + GOOD_TRIAL.replace('starts=2026-11-10', 'starts=2026-11-21'), GOOD + GOOD_TRIAL, 'trial-window'),
        (GOOD + GOOD_TRIAL.replace('starts=2026-11-10', 'starts=2026-11-01'), GOOD + GOOD_TRIAL, 'trial-window'),
        (GOOD + GOOD_TRIAL.replace('claude-sonnet-5-5@medium', 'claude-opus-5-5@high'), GOOD + GOOD_TRIAL,
         'trial-is-default'),
        (GOOD + GOOD_TRIAL.replace('claude-sonnet-5-5@medium', 'claude-opus-5-5'), GOOD + GOOD_TRIAL,
         'effort-trial-needs-both'),
        (GOOD.replace('@high', '') + GOOD_TRIAL.replace('claude-sonnet-5-5@medium', 'claude-opus-5-5@low'),
         GOOD + GOOD_TRIAL.replace('claude-sonnet-5-5@medium', 'claude-opus-5-5@low'), 'effort-trial-needs-both'),
        (GOOD + GOOD_TRIAL.replace('classes=writing', 'classes=qa'), GOOD + GOOD_TRIAL, 'class-assigned-and-trial'),
        (GOOD + GOOD_TRIAL + ' review=claude-sonnet-5-5', GOOD + GOOD_TRIAL + ' review=claude-fable-5-2',
         'review-id-routed'),
        ('route-cap', GOOD, 'no-decided'),
        ('route-cap decided=2026-11-01', GOOD, 'no-default'),
    ]
    for bad, good, code in cases:
        got = None
        try:
            parse_cap_line(bad.encode('latin-1'), today)
        except Refused as e:
            got = e.code
        t.ok(got == code, 'cap refusal %s (got %s)' % (code, got))
        try:
            parse_cap_line(good.encode('ascii'), today)
            ok = True
        except Refused:
            ok = False
        t.ok(ok, 'cap refusal %s: positive control parses' % code)
    # The file-level refusals, each with a control.
    p = _cap(fx, GOOD)
    t.ok(fx.run(['pick', '--cap', p, '--role', 'main', '--class', 'qa', '--tier', 'none'])[0] == 0,
         'cap file control picks')
    for extra, code in (('route-cap default=claude-opus-5-5 decided=2026-11-01\n', 'two-cap-lines'),):
        q = _cap(fx, GOOD, name='two.md', extra=extra)
        rc, o, _ = fx.run(['pick', '--cap', q, '--role', 'main', '--class', 'qa', '--tier', 'none'])
        t.ok(rc == 1 and 'two route-cap lines' in o, 'cap file refusal %s' % code)
    q = _cap(fx, '  ' + GOOD, name='indent.md')
    rc, o, _ = fx.run(['pick', '--cap', q, '--role', 'main', '--class', 'qa', '--tier', 'none'])
    t.ok(rc == 1 and 'no route-cap line' in o, 'cap file refusal: an indented line is not a cap line')
    q = _cap(fx, GOOD.replace('route-cap ', 'route-cap\t'), name='tab.md')
    rc, o, _ = fx.run(['pick', '--cap', q, '--role', 'main', '--class', 'qa', '--tier', 'none'])
    t.ok(rc == 1 and '(bad-byte)' in o, 'cap file refusal: route-cap then a tab refuses (D7)')
    q = _cap(fx, GOOD, name='crlf.md')
    with open(q, 'wb') as fh:
        fh.write(GOOD.encode('ascii') + b'\r\n')
    t.ok(fx.run(['pick', '--cap', q, '--role', 'main', '--class', 'qa', '--tier', 'none'])[0] == 0,
         'a CRLF cap line parses')
    big = _cap(fx, GOOD, name='big.md', extra='x' * (MAX_CAP_FILE + 10) + '\n')
    rc, o, _ = fx.run(['pick', '--cap', big, '--role', 'main', '--class', 'qa', '--tier', 'none'])
    t.ok(rc == 1 and 'over 1 MiB' in o, 'cap file refusal: too large')
    rc, o, _ = fx.run(['pick', '--cap', os.path.join(fx.base, 'nope.md'), '--role', 'main', '--class', 'qa'])
    t.ok(rc == 1 and 'cannot be read' in o, 'cap file refusal: missing')
    rc, o, _ = fx.run(['pick', '--cap', fx.base, '--role', 'main', '--class', 'qa'])
    t.ok(rc == 1 and 'not a regular file' in o, 'cap file refusal: a directory')
    rc, o, _ = fx.run(['pick', '--role', 'main', '--class', 'qa'])
    t.ok(rc == 1 and 'no cap file' in o, 'no --cap: no pick')


def t_work_tree(t, fx):
    """The cap file's own resolved path decides, from any cwd (F13, Codex C9); a write target in a
    work tree refuses before anything is written (R3)."""
    repo = os.path.join(fx.base, 'cloned')
    os.makedirs(os.path.join(repo, '.git'))
    p = os.path.join(repo, 'CLAUDE.md')
    with open(p, 'w') as fh:
        fh.write(GOOD + '\n')
    old = os.getcwd()
    try:
        os.chdir(fx.base)
        rc, o, _ = fx.run(['pick', '--cap', p, '--role', 'main', '--class', 'qa', '--tier', 'none'])
    finally:
        os.chdir(old)
    t.ok(rc == 1 and 'inside a version-control work tree' in o, 'a cap in a work tree, named from outside it, refuses')
    wt = os.path.join(fx.base, 'linked')
    os.makedirs(os.path.join(wt, 'sub'))
    with open(os.path.join(wt, '.git'), 'w') as fh:
        fh.write('gitdir: /elsewhere/.git/worktrees/linked\n')
    p2 = os.path.join(wt, 'sub', 'CLAUDE.md')
    with open(p2, 'w') as fh:
        fh.write(GOOD + '\n')
    rc, o, _ = fx.run(['pick', '--cap', p2, '--role', 'main', '--class', 'qa', '--tier', 'none'])
    t.ok(rc == 1 and 'inside a version-control work tree' in o, 'a cap in a linked worktree (a .git FILE) refuses')
    link = os.path.join(fx.base, 'home-cfg', 'linked-cap.md')
    os.makedirs(os.path.dirname(link), exist_ok=True)
    os.symlink(p, link)
    rc, o, _ = fx.run(['pick', '--cap', link, '--role', 'main', '--class', 'qa', '--tier', 'none'])
    t.ok(rc == 1 and 'inside a version-control work tree' in o, 'a symlink outside a tree to a cap inside one refuses')
    ctl = _cap(fx, GOOD, name='outside.md')
    t.ok(fx.run(['pick', '--cap', ctl, '--role', 'main', '--class', 'qa', '--tier', 'none'])[0] == 0,
         'work-tree control: the same line outside any tree picks')
    # The state dir in a work tree: refused before anything is written.
    fx2 = Fx(fx.base)
    fx2.env['XDG_STATE_HOME'] = os.path.join(repo, 'state')
    fx2.write(os.path.join(fx2.proj(), 's1.jsonl'), episode())
    rc, o, e = fx2.scan()
    t.ok(rc == 2 and 'target-in-work-tree' in e and not os.path.exists(os.path.join(repo, 'state')),
         'a state dir inside a work tree refuses before writing')
    rc, o, e = fx2.scan('--out', os.path.join(repo, 'table.tsv'))
    t.ok(rc == 2 and not os.path.exists(os.path.join(repo, 'table.tsv')), '--out inside a work tree refuses')
    fx2.env['XDG_STATE_HOME'] = fx2.state
    t.ok(fx2.scan()[0] == 0, 'state dir control: outside a tree the scan writes')
    tgt = os.path.join(fx2.base, 'elsewhere.tsv')
    os.symlink(os.path.join(fx2.base, 'victim'), tgt)
    rc, o, e = fx2.scan('--out', tgt)
    t.ok(rc == 2 and 'target-not-regular' in e and not os.path.exists(os.path.join(fx2.base, 'victim')),
         'a symlinked --out refuses')


def t_pick_rules(t, fx):
    p = _cap(fx, GOOD + ' protect=vendor/keep')
    tr = _cap(fx, GOOD + ' tiers=lite' + GOOD_TRIAL, name='tiered.md')

    def pk(cap, cls, tier=None, touch=(), role='main'):
        argv = ['pick', '--cap', cap, '--role', role, '--class', cls]
        if tier:
            argv += ['--tier', tier]
        for x in touch:
            argv += ['--touch', x]
        rc, o, _ = fx.run(argv)
        return rc, o.splitlines()[-1] if o else ''

    cases = [
        (p, 'qa', 'none', (), 'pick=claude-sonnet-5-5 by=assign'),
        (p, 'qa', None, (), 'pick=claude-opus-5-5@high by=default why=no-tier'),
        (p, 'qa', 'full', (), 'pick=claude-opus-5-5@high by=default why=full-tier'),
        (p, 'qa', 'lite', (), 'pick=claude-opus-5-5@high by=default why=tier-not-listed'),
        (p, 'code', 'none', (), 'pick=claude-opus-5-5@high by=default why=no-touch'),
        (p, 'code', 'none', ('src/a.go',), 'pick=claude-sonnet-5-5 by=assign'),
        (p, 'test', 'none', ('src/a.go',), 'pick=claude-sonnet-5-5 by=assign'),
        (p, 'code', 'none', ('src/a.go', 'CLAUDE.md'), 'why=protected'),
        (p, 'code', 'none', ('internal/AUTH/handlers/login.go',), 'why=protected'),
        (p, 'code', 'none', ('src\\.github\\workflows\\ci.yml',), 'why=protected'),
        (p, 'code', 'none', ('src/../skills/x.md',), 'why=protected'),
        (p, 'code', 'none', ('lib/vendor/keep/a.go',), 'why=protected'),
        (p, 'code', 'none', ('lib/vendor/other/a.go',), 'by=assign'),
        (p, 'writing', 'none', ('a.md',), 'why=not-assigned'),
        (tr, 'qa', 'none', (), 'why=tier-not-listed'),
        (tr, 'qa', 'lite', (), 'pick=claude-sonnet-5-5 by=assign'),
        (tr, 'qa', 'chore', (), 'why=tier-not-listed'),
        (tr, 'writing', 'lite', ('a.md',), 'pick=claude-opus-5-5@high by=default why=trial-not-built'),
    ]
    for cap, cls, tier, touch, want in cases:
        rc, line = pk(cap, cls, tier, touch)
        t.ok(rc == 0 and want in line, 'pick %s tier=%s touch=%s -> %s (got %s)' % (cls, tier, touch, want, line))
    rc, line = pk(p, 'review', 'none')
    t.ok(rc == 1 and line.startswith('no pick: review'), 'a review is never picked')
    rc, line = pk(p, 'qa', 'none', role='sub')
    t.ok(rc == 1 and 'cannot pin an exact id' in line, '--role sub: no pick')
    # A symlinked touch path is matched at its target too.
    repo = os.path.join(fx.base, 'touchrepo')
    os.makedirs(os.path.join(repo, '.git'))
    os.makedirs(os.path.join(repo, 'docs'))
    with open(os.path.join(repo, 'CLAUDE.md'), 'w') as fh:
        fh.write('x\n')
    os.symlink(os.path.join(repo, 'CLAUDE.md'), os.path.join(repo, 'docs', 'notes.md'))
    old = os.getcwd()
    try:
        os.chdir(repo)
        rc, line = pk(p, 'code', 'none', ('docs/notes.md',))
        t.ok('why=protected' in line, 'a symlink to CLAUDE.md is protected at its target')
        rc, line = pk(p, 'code', 'none', ('docs/other.go',))
        t.ok('by=assign' in line, 'symlink control: a plain file in the same repo picks')
    finally:
        os.chdir(old)
    rc, o, _ = fx.run(['pick', '--cap', p, '--role', 'main', '--all', '--tier', 'none'])
    lines = o.splitlines()
    t.ok(rc == 0 and lines[0] == NOTICE and 'class=qa pick=claude-sonnet-5-5 by=assign' in o
         and 'class=code pick=claude-opus-5-5@high by=default why=no-touch' in o and lines[-1].startswith('cap='),
         'pick --all: assigned non-editing classes pick, editing ones need --touch')
    rc, o, _ = fx.run(['pick', '--cap', p, '--role', 'main', '--class', 'qa', '--tier', 'none'])
    t.ok(o.splitlines()[0] == NOTICE, 'a single pick opens with the notice (D8)')


def t_args(t, fx):
    """The hand parser never echoes argv (D1)."""
    evil = '--' + CANARY + '\npick=claude-x-1 by=assign'
    # A value with no leading double dash reaches each value check; one with it stops at "needs a
    # value" (the mutation run found the value checks untested by evil alone).
    val = CANARY + '\npick=claude-x-1 by=assign'
    for argv in (['pick', evil], [evil], ['scan', '--out'], ['pick', '--role', evil, '--class', 'qa'],
                 ['pick', '--role', 'main', '--class', evil], ['pick', '--role', 'main', '--tier', evil, '--all'],
                 ['pick', '--role', val, '--class', 'qa'], ['pick', '--role', 'main', '--class', val],
                 ['pick', '--role', 'main', '--tier', val, '--all'], ['pick', val], ['scan', val],
                 ['scan', '--claude', 'a', '--claude', 'b'], ['scan', 'stray']):
        rc, o, e = fx.run(argv)
        t.ok(rc == 2 and CANARY not in o + e and 'pick=claude-x-1' not in o + e,
             'argv refusal is constant (%d args)' % len(argv))
    rc, o, e = fx.run(['--help'])
    t.ok(rc == 0 and 'usage:' in o, '--help prints usage')
    rc, o, e = fx.run(['report'])
    t.ok(rc == 2 and 'unknown verb' in e, 'report is not built in phase 2')


def plant_canary(fx):
    """A canary in every text and enum-shaped field the scan touches."""
    C = CANARY
    d = fx.proj('-x-' + C)
    sid = 'sess-' + C
    recs = episode(sid=sid, model=C, effort=C, ver=C, cwd='/x/' + C, text=C, origin=C, stop=C,
                   tools=[('Bash', {'command': 'pytest ' + C + ' | tail'}),
                          ('Edit', {'file_path': '/x/' + C + '/' + C + '.go', 'old_string': C, 'new_string': C}),
                          ('Agent', {'model': C, 'prompt': C, 'description': C}),
                          (C, {C: C})],
                   results={0: {'is_error': C, 'text': C,
                                'permissionDecision': {'decision': C, 'source': C, 'reasonType': C}},
                            1: {'toolDenialKind': C, 'text': C},
                            2: {'toolUseResult': {'agentId': C, 'agentType': C}, 'text': C}})
    for r in recs:
        r['uuid'] = r['uuid'] + C
        r['requestId'] = C
        if r['type'] == 'assistant':
            r['message']['id'] = r['message']['id'] + C
            r['message']['usage'] = {'output_tokens': C}
    recs += episode(sid=sid, model='claude-opus-5-5@' + C, effort='high', text=C)
    # Past the naming floor (panel F1's fence-lens fix): with the scan's id gate removed, a canary
    # model on 12 tasks would be named in the pre-cap table; on one task the floor alone hid it.
    for _ in range(NAME_FLOOR + 2):
        recs += episode(sid=sid, model=C, effort=C, text=C, tools=[
            ('NotebookEdit', {'notebook_path': '/x/' + C + '.ipynb'}), ('Write', {'path': '/x/' + C + '.md'})])
    # The fields the first plant missed (cross-check T1): timestamps, isMeta, isSidechain, a
    # block's type, tool-use ids.
    for r in recs[-6:]:
        r['timestamp'] = C
        r['isMeta'] = C if r['type'] == 'user' and not isinstance(r['message']['content'], str) else r.get('isMeta')
        if r['type'] == 'assistant':
            for b in r['message']['content']:
                if b.get('type') == 'tool_use':
                    b['id'] = b['id'] + C
            r['message']['content'].append({'type': C, C: C})
        else:
            for b in r['message']['content'] if isinstance(r['message']['content'], list) else []:
                b['tool_use_id'] = b.get('tool_use_id', '') + C
    recs.append(dict(episode(sid=sid, text=C)[0], isSidechain=C, uuid='side-' + C))
    recs.append({'type': C, 'uuid': C, C: C})
    recs.append({'type': 'api-request', 'params': C})
    fx.write(os.path.join(d, sid + '.jsonl'), recs)
    sub = episode(sid=sid, model=C, text=C, tools=[('Read', {'file_path': C})])
    fx.write(os.path.join(d, sid, 'subagents', 'agent-' + C + '.jsonl'), sub)
    cx = codex_rollout(model=C, effort=C, cmds=[(C, C), ('pytest ' + C, 1)], files=[C + '.py'], ver=C, cwd=C)
    cx[0]['payload']['originator'] = C
    cx.append({'timestamp': C, 'type': 'event_msg', 'payload': {'type': 'turn_aborted', 'reason': C}})
    cx.append({'timestamp': C, 'type': 'event_msg', 'payload': {'type': 'item_completed',
                                                                 'item': {'type': C, 'kind': C, C: C}}})
    cx[1]['payload']['cwd'] = C
    cx.append({'timestamp': C, 'type': 'event_msg', 'payload': {'type': 'item_completed',
                                                                 'item': {'type': 'Extension', 'kind': C}}})
    cx.append({'timestamp': C, 'type': C, 'payload': {'type': C, C: C}})
    cx.append({'timestamp': C, 'type': 'event_msg', 'payload': {'type': 'item_completed', 'item': {
        'type': 'CommandExecution', 'command': C, 'parsed_cmd': [{'type': C, 'cmd': C}], 'exit_code': C}}})
    cx.append({'timestamp': C, 'type': 'event_msg', 'payload': {'type': 'token_count', 'info': {
        'total_token_usage': {'output_tokens': C}, C: C}}})
    fx.write(os.path.join(fx.codex, 'sessions', '2026', '11', '10', 'rollout-' + C + '.jsonl'), cx)
    return d


def t_canary(t, fx):
    plant_canary(fx)
    p = _cap(fx, GOOD, extra='# ' + CANARY + ' a note the user wrote\n')
    outs = []
    for argv in (['scan', '--claude', fx.claude, '--codex', fx.codex],
                 ['scan', '--claude', fx.claude, '--codex', fx.codex, '--cap', p],
                 ['cap-template'],
                 ['pick', '--cap', p, '--role', 'main', '--class', 'code', '--touch', 'a.go', '--tier', 'none'],
                 ['pick', '--cap', p, '--role', 'main', '--all', '--tier', 'none']):
        rc, o, e = fx.run(argv)
        # The table after EVERY run (panel F1): the second scan overwrites the first's table.
        outs.append((argv[0], rc, o + e + fx.table()))
    blob = ''.join(x[2] for x in outs)
    t.ok(outs[0][1] == 0 and outs[1][1] == 0, 'canary fixtures scan cleanly')
    t.ok(CANARY not in blob and CANARY.lower() not in blob, 'no canary in any output, error or table')
    t.ok('(other)' in fx.table(), 'canary model ids fold to (other)')


def t_canary_refusal(t, fx):
    """The refusal path carries no canary either; refused.txt holds only the refused files' paths."""
    d = plant_canary(fx)
    bad = fx.write(os.path.join(d, 'torn-' + CANARY + '.jsonl'),
                   [episode(text=CANARY)[0], '{"text": "' + CANARY, episode(text=CANARY)[1]])
    rc, o, e = fx.scan()
    t.ok(rc == 2 and CANARY not in o + e and not fx.table(), 'a refused scan prints no canary and writes no table')
    rt = os.path.join(fx.state, 'model-router', 'refused.txt')
    body = open(rt).read() if os.path.exists(rt) else ''
    t.ok(body == bad + '\n', 'refused.txt holds exactly the refused path')
    t.ok(stat.S_IMODE(os.stat(rt).st_mode) == 0o600, 'refused.txt is 0600')


def t_fence(t, fx):
    """A path the CALLER named is printed through one fence (section 15, D2): a newline in the cap
    path, the --out path or XDG_STATE_HOME must not start a line of its own. The control is the
    same run's own line count: the fenced value stays on the line it belongs to."""
    fake = '\npick=claude-evil-1 by=assign'
    d = os.path.join(fx.base, 'home-cfg', 'a' + fake)
    os.makedirs(d)
    p = os.path.join(d, 'cap.md')
    with open(p, 'w') as fh:
        fh.write(GOOD + '\n')
    rc, o, e = fx.run(['pick', '--cap', p, '--role', 'main', '--class', 'qa', '--tier', 'none'])
    lines = o.splitlines()
    t.ok(rc == 0 and len(lines) == 2 and lines[1].startswith('pick=claude-sonnet-5-5 by=assign')
         and 'cap=' in lines[1] and '?pick=claude-evil-1' in lines[1],
         'a newline in the cap path stays on the pick line, as ?')
    fx.write(os.path.join(fx.proj(), 's.jsonl'), episode())
    out = os.path.join(fx.base, 'o' + fake.replace('pick=', 'table: '))
    rc, o, e = fx.scan('--out', out)
    t.ok(rc == 0 and not any(ln.startswith('table: claude-evil-1') for ln in o.splitlines())
         and os.path.exists(out), 'a newline in --out stays on the table line')
    f = Fx(fx.base)
    f.env['XDG_STATE_HOME'] = os.path.join(f.base, 's' + fake.replace('pick=', 'scan: '))
    f.write(os.path.join(f.proj(), 's.jsonl'), [episode()[0], '{"torn', episode()[1]])
    rc, o, e = f.scan()
    t.ok(rc == 2 and 'repair or move them' in o
         and not any(ln.startswith('scan: claude-evil-1') for ln in o.splitlines()),
         'a newline in XDG_STATE_HOME stays on the refusal line (D2)')
    t.ok(fence('a\nb\x7f\xe9' + 'x' * 300).count('?') == 4 and len(fence('x' * 300)) == 259,
         'fence: control bytes and non-ASCII become ?, capped at 256')


def t_internal_error(t, fx):
    global classify
    keep = classify

    def boom(f):
        raise ValueError('message quoting ' + CANARY)
    classify = boom
    try:
        fx.write(os.path.join(fx.proj(), 's1.jsonl'), episode())
        rc, o, e = fx.scan()
    finally:
        classify = keep
    t.ok(rc == 3 and 'internal error ValueError' in e and CANARY not in o + e, 'an exception prints its type only')


def t_sources(t, fx):
    """Absent and unreadable are different verdicts; a torn interior line refuses, a torn last line
    is counted (plan section 2)."""
    rc, o, e = fx.scan()
    t.ok(rc == 0 and 'claude=absent' in o and 'codex=absent' in o, 'absent sources are OFF, and say so')
    fx.write(os.path.join(fx.proj(), 'ok.jsonl'), episode())
    rc, o, e = fx.scan()
    t.ok(rc == 0 and 'claude=present' in o, 'control: a whole transcript scans')
    cases = [
        ('torn interior line', [episode()[0], '{"type": "user", "uuid"', episode()[1]], None, 2),
        ('empty interior line', [episode()[0], '', episode()[1]], None, 2),
        ('a non-object line', [episode()[0], '[1, 2]', episode()[1]], None, 2),
        ('a torn api-request line (D3)', [episode()[0], '{"type":"api-request', episode()[1]], None, 2),
        ('a whole api-request line', [episode()[0], '{"type":"api-request","params":{"x":1}}', episode()[1]],
         None, 0),
        ('a torn last line', episode(), '{"type": "assist', 0),
    ]
    for name, recs, tail, want in cases:
        f = Fx(fx.base)
        f.write(os.path.join(f.proj(), 's.jsonl'), recs, tail=tail)
        rc, o, e = f.scan()
        t.ok(rc == want, 'source: %s exits %d (got %d)' % (name, want, rc))
        if name == 'a torn last line':
            t.ok('torn_last=1' in o, 'a torn last line is counted')
    if os.geteuid() != 0:
        f = Fx(fx.base)
        p = f.write(os.path.join(f.proj(), 's.jsonl'), episode())
        os.chmod(p, 0)
        rc, o, e = f.scan()
        os.chmod(p, 0o600)
        t.ok(rc == 2 and 'claude 1' in o, 'an unreadable file refuses, named by source and count')
        f = Fx(fx.base)
        os.makedirs(os.path.join(f.claude, 'projects'))
        os.chmod(os.path.join(f.claude, 'projects'), 0)
        rc, o, e = f.scan()
        os.chmod(os.path.join(f.claude, 'projects'), 0o700)
        t.ok(rc == 2, 'an unreadable projects directory refuses')
    f = Fx(fx.base)
    os.makedirs(f.claude)
    with open(os.path.join(f.claude, 'projects'), 'w') as fh:
        fh.write('not a dir')
    t.ok(f.scan()[0] == 2, 'a projects path that is not a directory refuses')
    # A file that vanishes mid-read refuses.
    f = Fx(fx.base)
    p = f.write(os.path.join(f.proj(), 's.jsonl'), episode())
    global iter_records
    keep = iter_records

    def gone(path, scan):
        raise FileNotFoundError(2, 'gone')
        yield {}
    iter_records = gone
    try:
        rc, o, e = f.scan()
    finally:
        iter_records = keep
    t.ok(rc == 2, 'a file gone mid-read refuses')


def t_vocab(t, fx):
    """The id gate and the naming rule, with a planted id repeated past the floor (Codex C1)."""
    for s, ok in (('claude-opus-5-5', True), ('anthropic.claude-opus-5-5-20261001-v1:0', True),
                  ('claude-opus-5-5@20261001', True), ('claude opus', False), ('claude-x\n1', False),
                  ('CLAUDE-1', False), ('gpt-6-astra', False), ('claude-sonnet', False), ('x' * 70 + 'claude1', False),
                  ('claude-3-5-sonnet-latest', False), ('opus', False), ('<synthetic>', False)):
        t.ok((gate_id(s) is not None) == ok, 'id gate: %r' % s)
    t.ok(split_mid('claude-opus-5-5@xhigh') == ('claude-opus-5-5', 'xhigh'), 'the @effort split')
    t.ok(split_mid('claude-opus-5-5@20261001') == ('claude-opus-5-5@20261001', None), 'a cloud @date stays whole')
    planted = 'claude-follow-this-instruction-1'
    d = fx.proj()
    recs = []
    for i in range(12):
        recs += episode(sid='p%d' % i, model=planted)
    for i in range(9):
        recs += episode(sid='q%d' % i, model='claude-rare-model-2')
    fx.write(os.path.join(d, 'many.jsonl'), recs)
    rc, o, e = fx.scan()
    tab = fx.table()
    t.ok(rc == 0 and planted in tab and 'claude-rare-model-2' not in tab,
         'pre-cap: an id past the floor is named (the stated residual), one under it is not')
    p = _cap(fx, GOOD)
    rc, o, e = fx.scan('--cap', p)
    tab = fx.table()
    t.ok(rc == 0 and planted not in tab and planted not in o and '(other)@high' in tab,
         'under a cap, only the cap\'s ids are named')


def t_table(t, fx):
    """A torn or partial table is refused on read (Codex C8), with the scan's own table as control."""
    fx.write(os.path.join(fx.proj(), 's.jsonl'), sum((episode(sid='t%d' % i) for i in range(12)), []))
    t.ok(fx.scan()[0] == 0, 'table fixture scans')
    good = fx.table()
    tp = os.path.join(fx.state, 'model-router', 'table.tsv')

    def tmpl(text):
        with open(tp, 'w') as fh:
            fh.write(text)
        return fx.run(['cap-template'])

    rc, o, e = tmpl(good)
    t.ok(rc == 0 and 'route-cap default=claude-opus-5-5@high' in o and o.rstrip().split('\n')[3].endswith('decided='),
         'control: the scan\'s own table drafts an undated line')
    lines = good.split('\n')
    body = '\n'.join(lines[:-2]) + '\n'

    def resign(b, n):
        return b + '#end\trows=%d\tsha256=%s\n' % (n, hashlib.sha256(b.encode()).hexdigest())

    nrows = len(lines) - 5
    row = lines[3]
    cases = [
        ('no trailer', body, 'table-no-trailer'),
        ('a short row count', resign(body, nrows + 1), 'table-row-count'),
        ('a changed byte', good.replace('\nmain\t', '\nsub\t', 1), 'table-hash'),
        ('a duplicate cell key', resign(body + row + '\n', nrows + 1), 'table-duplicate-cell'),
        ('an unknown version', resign(body.replace('#model-router-table\t1', '#model-router-table\t9'), nrows),
         'table-version'),
        ('a value outside the vocabulary', resign(body.replace(row, row.replace('main', 'boss', 1)), nrows),
         'table-bad-row'),
        ('a canary id', resign(body.replace(row, row.replace('claude-opus-5-5', CANARY, 1)), nrows),
         'table-bad-row'),
        ('a header value outside its pattern', resign(body.replace('scoped=no', 'scoped=' + CANARY), nrows),
         'table-header'),
        ('a truncated table', good[:len(good) // 2], 'table-no-trailer'),
    ]
    for name, text, code in cases:
        rc, o, e = tmpl(text)
        t.ok(rc == 2 and code in o and CANARY not in o + e, 'table refusal: %s (%s)' % (name, code))


def t_dedup(t, fx):
    """Equal copies count once; a conflicting copy takes every task holding any copy out (D11, D12)."""
    base = episode(sid='d1', tools=[('Bash', {'command': 'pytest tests/a.py'})])
    d = fx.proj()
    fx.write(os.path.join(d, 'orig.jsonl'), base)
    fx.write(os.path.join(d, 'copy.jsonl'), [dict(r) for r in base])
    rc, o, e = fx.scan()
    t.ok(rc == 0 and 'conflicts=0' in o and 'tasks: main=1 ' in o, 'an equal copy counts once')
    f = Fx(fx.base)
    d = f.proj()
    a = episode(sid='d2', tools=[('Bash', {'command': 'pytest tests/a.py'})])
    b = json.loads(json.dumps(a))
    b[1]['message']['content'][1]['input']['command'] = 'pytest tests/b.py'
    c = json.loads(json.dumps(a))
    f.write(os.path.join(d, '1.jsonl'), a + episode(sid='d2'))
    f.write(os.path.join(d, '2.jsonl'), b + episode(sid='d3'))
    f.write(os.path.join(d, '3.jsonl'), c + episode(sid='d4'))
    rc, o, e = f.scan()
    t.ok(rc == 0 and 'conflicts=1 ' in o and 'conflict_tasks=3 ' in o and 'tasks: main=3 ' in o,
         'a raw-value conflict (same label) takes all three copies\' tasks out')


def t_signals(t, fx):
    d = fx.proj()
    recs = []
    recs += episode(sid='g1', tools=[('Bash', {'command': 'go test ./... | tail'})], results={0: {'is_error': True}})
    recs += episode(sid='g1', tools=[('Bash', {'command': 'rm -rf x'})],
                    results={0: {'toolDenialKind': 'user-rejected',
                                 'permissionDecision': {'decision': 'reject', 'source': 'user'}}})
    recs += episode(sid='g1', tools=[('Bash', {'command': 'curl x'})],
                    results={0: {'permissionDecision': {'decision': 'reject', 'source': 'hook'}}},
                    stop='tool_use')
    recs += episode(sid='g1', ver='2.1.280', tools=[('Read', {'file_path': 'a.go'})])
    recs += episode(sid='g1', extra_user={'type': 'user', 'uuid': _id('u'), 'sessionId': 'g1',
                                          'timestamp': '2026-11-10T10:00:00.000Z', 'version': '2.1.310',
                                          'interruptedMessageId': 'm', 'message': {'role': 'user', 'content': 'x'},
                                          'turnOrigin': 'peer'})
    fx.write(os.path.join(d, 'g.jsonl'), recs)
    fx.write(os.path.join(fx.codex, 'sessions', '2026', '11', '10', 'r1.jsonl'),
             codex_rollout(cmds=[('pytest', 1)], interrupted=True))
    rc, o, e = fx.scan()
    t.ok(rc == 0, 'signal fixtures scan')
    sig = [ln for ln in o.splitlines() if ln.startswith('signals, main')]
    t.ok(sig and 'interrupt=1/4' in sig[0] and 'user_reject=1/4' in sig[0] and 'policy_denied=1/4' in sig[0]
         and 'test_red_end=1/1' in sig[0] and 'test_masked=1/1' in sig[0] and 'abandoned=1/5' in sig[0],
         'the signals fire, and a version outside a range is not covered (got %s)' % (sig[0] if sig else None))
    cx = [r for r in fx.table().splitlines() if r.startswith('codex\t')]
    t.ok(len(cx) == 1 and cx[0].split('\t')[10] == '1/1' and cx[0].split('\t')[3] == '(other)@xhigh',
         'codex: an interrupt reads, its id folds to (other)')


def t_alias(t, fx):
    d = fx.proj()
    recs = []
    for i, m in enumerate(('claude-fable-5-1', 'claude-fable-5-2')):
        recs += episode(sid='al', tools=[('Agent', {'model': 'fable', 'prompt': 'p'})],
                        results={0: {'toolUseResult': {'agentId': 'ag%d' % i}}})
        fx.write(os.path.join(d, 'al', 'subagents', 'agent-ag%d.jsonl' % i), episode(sid='al', model=m))
    recs += episode(sid='al', tools=[('Agent', {'prompt': 'p'})])
    fx.write(os.path.join(d, 'al.jsonl'), recs)
    rc, o, e = fx.scan()
    t.ok(rc == 0 and 'naming no model: 1;' in o and 'more than one id: 1' in o, 'alias drift and inherit counts')


def t_scope_and_template(t, fx):
    d = fx.proj()
    recs = []
    for i in range(30):
        recs += episode(sid='m%d' % i, tools=[('Edit', {'file_path': '/x/proj/a%d.go' % i})])
    for i in range(12):
        recs += episode(sid='n%d' % i, model='claude-sonnet-5-5', effort='medium')
    for i in range(3):
        recs += episode(sid='o%d' % i, cwd='/elsewhere')
    fx.write(os.path.join(d, 'many.jsonl'), recs)
    rc, o, e = fx.scan()
    rc2, o2, e2 = fx.run(['cap-template'])
    ls2 = o2.splitlines()
    t.ok(rc == 0 and rc2 == 0 and ls2[3] == 'route-cap default=claude-opus-5-5@high decided=',
         'cap-template drafts the default alone in the line (M17)')
    cand = [ln for ln in ls2 if ln.startswith('# assign= candidates')]
    t.ok(len(cand) == 1 and cand[0].endswith(' assign=qa:claude-sonnet-5-5@medium'),
         'a second model used past the floor is a candidate, not a route')
    t.ok('# review= (blank)' in o2 and 'claude-sonnet-5-5' in o2.split('# review=')[1], 'the review hint names ids')
    try:
        parse_cap_line(ls2[3].encode('ascii'), NOW.date())
        undated = False
    except Refused as e:
        undated = e.code == 'undated'
    t.ok(undated, 'the draft parses as no cap until dated')
    dated = ls2[3].replace(' decided=', ' ' + cand[0].rsplit(' ', 1)[1] + ' decided=2026-11-12') if cand else ''
    try:
        c = parse_cap_line(dated.encode('ascii'), NOW.date())
        t.ok(c.assign == {'qa': ('claude-sonnet-5-5', 'medium')}, 'control: the candidate, moved in and dated, routes')
    except Refused:
        t.ok(False, 'control: the candidate, moved in and dated, routes')
    rc, o, e = fx.scan('--cwd-under', '/x/proj')
    t.ok(rc == 0 and 'scoped=yes' in o and 'tasks: main=42 ' in o, '--cwd-under scopes the tasks')


def _pk(fx, cap, cls, touch=(), tier='none', cwd=None):
    """One pick's last line, run from `cwd` when given."""
    argv = ['pick', '--cap', cap, '--role', 'main', '--class', cls, '--tier', tier]
    for x in touch:
        argv += ['--touch', x]
    old = os.getcwd()
    try:
        if cwd:
            os.chdir(cwd)
        rc, o, _ = fx.run(argv)
    finally:
        os.chdir(old)
    return rc, (o.splitlines()[-1] if o else '')


def _mk(path, text='x\n'):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, 'w') as fh:
        fh.write(text)
    return path


def t_gate_at_parts(t, fx):
    """Every `@` part of an id holds a digit (cross-check G1), and review= excludes by model."""
    for s, ok in (('claude-fable-5-2@ultra', False), ('claude-fable-5-2@minimal', False),
                  ('claude-x-5@max', False), ('claude-x-5@hihg', False), ('claude-x-5@high@high', False),
                  ('claude-x-5@', False), ('-claude-1', False), ('claude-x-5@20261001', True),
                  ('claude-x-5@20261001@v2', True)):
        t.ok((gate_id(s) is not None) == ok, 'id gate @ part: %r' % s)
    t.ok(split_mid('claude-x-5@max@high') is None, 'an id with an effort-word part fails before the split')
    today = NOW.date()
    base = 'route-cap default=claude-opus-5-5@high %s decided=2026-11-01'
    for bad, good, code in (
            ('assign=qa:claude-fable-5-2@ultra review=claude-fable-5-2',
             'assign=qa:claude-sonnet-5-5 review=claude-fable-5-2', 'bad-id'),
            ('assign=qa:claude-fable-5-2@20261001 review=claude-fable-5-2',
             'assign=qa:claude-sonnet-5-5@20261001 review=claude-fable-5-2', 'review-id-routed'),
            ('assign=qa:claude-fable-5-2 review=claude-fable-5-2@20261001',
             'assign=qa:claude-sonnet-5-5 review=claude-fable-5-2@20261001', 'review-id-routed'),
            ('review=claude-opus-5-5@20261001', 'review=claude-fable-5-2@20261001', 'review-id-routed'),
            (GOOD_TRIAL[1:] + ' review=claude-sonnet-5-5', GOOD_TRIAL[1:] + ' review=claude-fable-5-2',
             'review-id-routed'),
            ('review=sonnet', 'review=claude-fable-5-2', 'bad-id'),
            (GOOD_TRIAL[1:].replace('claude-sonnet-5-5@medium', 'sonnet'), GOOD_TRIAL[1:], 'bad-id'),
            ('assign=qa-claude-sonnet-5-5', 'assign=qa:claude-sonnet-5-5', 'bad-assign'),
            ('tiers=foo', 'tiers=lite', 'bad-tier'),
            ('protect=a:b', 'protect=a', 'bad-protect'),
            ('protect=a/./b', 'protect=a/b', 'bad-protect'),
            ('protect=caf\xe9', 'protect=cafe', 'bad-byte')):
        got = None
        try:
            parse_cap_line((base % bad).encode('latin-1'), today)
        except Refused as e:
            got = e.code
        t.ok(got == code, 'cap refusal %s: %s (got %s)' % (code, bad[:40], got))
        try:
            parse_cap_line((base % good).encode('ascii'), today)
            ok = True
        except Refused:
            ok = False
        t.ok(ok, 'cap refusal %s: control %s parses' % (code, good[:40]))
    # The review class matches by model: a task on `<review id>@<date>` with no edits is review.
    d = fx.proj()
    recs = []
    for i in range(3):
        recs += episode(sid='rv%d' % i, model='claude-fable-5-2@20261001', tools=[('Read', {'file_path': 'a.go'})])
    fx.write(os.path.join(d, 'rv.jsonl'), recs)
    p = _cap(fx, GOOD + ' review=claude-fable-5-2')
    rc, o, e = fx.scan('--cap', p)
    rows = [r.split('\t') for r in fx.table().splitlines() if r.startswith('main\t')]
    t.ok(rc == 0 and any(r[1] == 'review' for r in rows), 'a review model at a cloud @date reads as review')


def t_sources_eacces(t, fx):
    """Absent only on ENOENT (cross-check O1): a permission error anywhere refuses."""
    if os.geteuid() == 0:
        return
    f = Fx(fx.base)
    f.write(os.path.join(f.proj(), 's.jsonl'), episode())
    t.ok(f.scan()[0] == 0, 'EACCES control: the config scans')
    os.chmod(f.claude, 0)
    try:
        rc, o, e = f.scan()
    finally:
        os.chmod(f.claude, 0o700)
    t.ok(rc == 2 and 'claude=absent' not in o and not f.table(), 'a config dir with mode 000 refuses, not absent')
    f = Fx(fx.base)
    pd = f.proj()
    f.write(os.path.join(pd, 's.jsonl'), episode())
    os.chmod(pd, 0o444)
    try:
        rc, o, e = f.scan()
    finally:
        os.chmod(pd, 0o700)
    t.ok(rc == 2 and not f.table(), 'a project dir without its search bit refuses, not zero files')
    f = Fx(fx.base)
    f.write(os.path.join(f.codex, 'sessions', '2026', '11', '10', 'r.jsonl'), codex_rollout())
    os.chmod(f.codex, 0)
    try:
        rc, o, e = f.scan()
    finally:
        os.chmod(f.codex, 0o700)
    t.ok(rc == 2 and 'codex=absent' not in o, 'a codex home with mode 000 refuses, not absent')
    f = Fx(fx.base)
    os.makedirs(f.claude)
    os.symlink(os.path.join(f.base, 'nowhere'), os.path.join(f.claude, 'projects'))
    t.ok(f.scan()[0] == 2, 'a dangling projects link refuses, not absent')


def t_protect_roots(t, fx):
    """Protection relative to EVERY enclosing root (panel W1), with routable controls."""
    b = fx.base
    p = _cap(fx, GOOD)
    pp = _cap(fx, GOOD + ' protect=proj/internal', name='pp.md')
    os.makedirs(os.path.join(b, 'home', '.claude', 'skills', 'foo', '.git'))
    skill = _mk(os.path.join(b, 'home', '.claude', 'skills', 'foo', 'SKILL.md'))
    os.makedirs(os.path.join(b, 'work', 'foo', '.git'))
    plain = _mk(os.path.join(b, 'work', 'foo', 'SKILL.go'))
    os.makedirs(os.path.join(b, 'proj', '.git'))
    _mk(os.path.join(b, 'proj', 'auth', '.git'), 'gitdir: ../.git/modules/auth\n')
    _mk(os.path.join(b, 'proj', 'lib', '.git'), 'gitdir: ../.git/modules/lib\n')
    os.makedirs(os.path.join(b, 'dot', '.claude', '.git'))
    wt = os.path.join(b, 'proj', '.claude', 'worktrees', 'wt')
    _mk(os.path.join(b, 'proj', '.git', 'worktrees', 'wt', 'commondir'), '../..\n')
    _mk(os.path.join(wt, '.git'), 'gitdir: %s\n' % os.path.join(b, 'proj', '.git', 'worktrees', 'wt'))
    # A submodule under a directory NAMED worktrees is not a linked worktree (Codex code pass A1).
    sm = os.path.join(b, 'worktrees', 'p2')
    os.makedirs(os.path.join(sm, '.git', 'modules', 'mod'))
    _mk(os.path.join(sm, 'skills', 'mod', '.git'), 'gitdir: %s\n' % os.path.join(sm, '.git', 'modules', 'mod'))
    # A linked worktree whose gitdir cannot be read climbs on: it protects more, never less.
    _mk(os.path.join(b, 'proj', '.claude', 'worktrees', 'gone', '.git'), 'gitdir: /nowhere/.git/worktrees/gone\n')
    hg = os.path.join(b, 'tokens', 'hgrepo')
    os.makedirs(os.path.join(hg, '.hg'))
    cases = [
        (p, skill, 'why=protected', 'a skill that is its own clone, under .claude/skills'),
        (p, plain, 'by=assign', 'control: a clone outside any protected name'),
        (p, os.path.join(b, 'proj', 'auth', 'x.go'), 'why=protected', 'a submodule named auth'),
        (p, os.path.join(b, 'proj', 'lib', 'x.go'), 'by=assign', 'control: a submodule named lib'),
        (p, os.path.join(b, 'dot', '.claude', 'settings.json'), 'why=protected', 'a versioned .claude'),
        (pp, os.path.join(b, 'proj', 'internal', 'a.go'), 'why=protected', 'protect= against the absolute path'),
        (pp, os.path.join(b, 'proj', 'other', 'a.go'), 'by=assign', 'control: protect= elsewhere'),
        (p, os.path.join(wt, 'src', 'a.go'), 'by=assign', 'a linked worktree under .claude is routable'),
        (p, os.path.join(wt, 'CLAUDE.md'), 'why=protected', 'a linked worktree still protects its own names'),
        (pp, os.path.join(wt, 'src', 'a.go'), 'by=assign', 'a linked worktree stays routable under protect='),
        (p, os.path.join(sm, 'skills', 'mod', 'a.py'), 'why=protected', 'a submodule under worktrees/ is no worktree'),
        (p, os.path.join(b, 'proj', '.claude', 'worktrees', 'gone', 'a.go'), 'why=protected',
         'a worktree whose gitdir is gone climbs on'),
        (p, os.path.join(hg, 'a.go'), 'by=assign', 'an hg checkout is a work tree: a sensitive name above it is not'),
        (p, os.path.join(hg, 'secrets.go'), 'why=protected', 'control: a sensitive name inside the hg tree'),
    ]
    for cap, touch, want, name in cases:
        rc, line = _pk(fx, cap, 'code', (touch,))
        t.ok(rc == 0 and want in line, 'protect: %s (got %s)' % (name, line[:60]))


def t_protect_shapes(t, fx):
    """A directory, a blank, a trailing slash, a symlinked `..`, a long s, a deleted cwd (W2, W3)."""
    p = _cap(fx, GOOD)
    d = os.path.join(fx.base, 'shapes')
    os.makedirs(os.path.join(d, 'internal'))
    os.makedirs(os.path.join(d, 'deep', '.claude', 'sub'))
    os.makedirs(os.path.join(d, 'deep', 'plain', 'sub'))
    os.symlink(os.path.join(d, 'deep', '.claude', 'sub'), os.path.join(d, 'lnk'))
    os.symlink(os.path.join(d, 'deep', 'plain', 'sub'), os.path.join(d, 'lnk2'))
    os.makedirs(os.path.join(d, 'via'))
    os.symlink(os.path.join(d, 'deep', 'plain'), os.path.join(d, 'via', '.claude'))
    for touch, want, name in (('.', 'why=protected', 'the cwd'), ('internal', 'why=protected', 'a directory'),
                              ('src/', 'why=protected', 'a trailing slash'), (' ', 'why=protected', 'a blank'),
                              ('src/a.go', 'by=assign', 'control: a file'),
                              ('lnk/../settings.json', 'why=protected', 'a symlink then .. (W3)'),
                              ('lnk2/../settings.json', 'by=assign', 'control: the same through a plain dir'),
                              ('ſkills/x.md', 'why=protected', 'a long s folds to skills'),
                              ('internal/a.go', 'by=assign', 'control: a file in that directory'),
                              (os.path.join(d, 'via', '.claude', 'x.go'), 'why=protected',
                               'the lexical path names .claude, its target does not'),
                              (os.path.join(d, 'deep', 'plain', 'x.go'), 'by=assign', 'control: the target itself')):
        rc, line = _pk(fx, p, 'code', (touch,), cwd=d)
        t.ok(rc == 0 and want in line, 'touch shape: %s (got %s)' % (name, line[:60]))
    gone = os.path.join(fx.base, 'gone')
    os.makedirs(gone)
    old = os.getcwd()
    try:
        os.chdir(gone)
        os.rmdir(gone)
        rc, o, e = fx.run(['pick', '--cap', p, '--role', 'main', '--class', 'code', '--touch', 'a.go',
                           '--tier', 'none'])
        rc2, o2, e2 = fx.run(['pick', '--cap', p, '--role', 'main', '--class', 'qa', '--tier', 'none'])
    finally:
        os.chdir(old)
    t.ok(rc == 0 and 'why=protected' in o and rc2 == 0 and 'by=assign' in o2,
         'a deleted cwd: a relative touch is protected, and a pick with none still answers')


def t_cap_file_more(t, fx):
    """The cap file's own refusals added by the panel, each with a control."""
    ctl = _cap(fx, GOOD, name='ctl.md')
    t.ok(_pk(fx, ctl, 'qa')[0] == 0, 'cap file control picks')
    hg = os.path.join(fx.base, 'hgcap')
    os.makedirs(os.path.join(hg, '.hg'))
    rc, line = _pk(fx, _mk(os.path.join(hg, 'CLAUDE.md'), GOOD + '\n'), 'qa')
    t.ok(rc == 1 and 'version-control work tree' in line, 'a cap in an hg checkout refuses')
    repo = os.path.join(fx.base, 'lexrepo')
    os.makedirs(os.path.join(repo, '.git'))
    os.symlink(os.path.dirname(ctl), os.path.join(repo, 'out'))
    rc, line = _pk(fx, os.path.join(repo, 'out', 'ctl.md'), 'qa')
    t.ok(rc == 1 and 'version-control work tree' in line, 'a cap reached through a link in a tree refuses')
    ww = _cap(fx, GOOD, name='ww.md')
    os.chmod(ww, 0o646)
    rc, line = _pk(fx, ww, 'qa')
    t.ok(rc == 1 and 'writable by every user' in line, 'a world-writable cap refuses')
    os.chmod(ww, 0o664)
    t.ok(_pk(fx, ww, 'qa')[0] == 0, 'control: a group-writable cap picks (a umask of 002)')
    keep = os.geteuid
    os.geteuid = lambda: keep() + 1
    try:
        rc, line = _pk(fx, ctl, 'qa')
    finally:
        os.geteuid = keep
    t.ok(rc == 1 and 'not owned by you' in line, 'a cap another user owns refuses')
    for second, name in (('﻿route-cap default=claude-opus-5-5 decided=2026-11-01', 'a BOM'),
                         ('route-cap\x0bdefault=claude-opus-5-5', 'a vertical tab'),
                         ('route-cap\x00', 'a NUL'), ('route-cap\xa0default=claude-opus-5-5', 'a no-break space')):
        q = os.path.join(fx.base, 'home-cfg', 'second.md')
        with open(q, 'wb') as fh:
            fh.write(GOOD.encode('ascii') + b'\n' + second.encode('utf-8') + b'\n')
        rc, line = _pk(fx, q, 'qa')
        t.ok(rc == 1 and 'two route-cap lines' in line, 'a second cap line after %s still counts' % name)
    q = _cap(fx, GOOD, name='word.md', extra='route-capped is a word\nroute-cap-v2 is another\n')
    t.ok(_pk(fx, q, 'qa')[0] == 0, 'control: route-capped and route-cap-v2 are not cap lines')
    q = os.path.join(fx.base, 'home-cfg', 'bom.md')
    with open(q, 'wb') as fh:
        fh.write(b'\xef\xbb\xbf' + GOOD.encode('ascii') + b'\n')
    rc, line = _pk(fx, q, 'qa')
    t.ok(rc == 1 and '(bad-byte)' in line, 'a BOM before the only cap line refuses it')


def t_scan_robust(t, fx):
    """Odd shapes in a record are not internal errors (panel O3); a refused scan leaves no table
    (O2); refused.txt is one line per path (F6); named sources and tables in a tree refuse (F2)."""
    for name, recs, tail, want, extra in (
            ('a list type', [{'type': ['user'], 'uuid': 'l1'}] + episode(), None, 0, None),
            ('a list sessionId', [dict(episode()[0], sessionId=['x'])] + episode()[1:], None, 0, None),
            ('an object timestamp', [dict(episode()[0], timestamp={'a': 1})] + episode()[1:], None, 0, None),
            ('a deep interior line', [episode()[0], '[' * 5000 + ']' * 5000, episode()[1]], None, 2, None),
            ('a deep last line', episode(), '[' * 5000, 0, 'torn_last=1')):
        f = Fx(fx.base)
        f.write(os.path.join(f.proj(), 's.jsonl'), recs, tail=tail)
        rc, o, e = f.scan()
        t.ok(rc == want and 'internal error' not in e and (extra is None or extra in o),
             'record shape: %s exits %d (got %d)' % (name, want, rc))
    f = Fx(fx.base)
    pd = f.proj()
    f.write(os.path.join(pd, 's.jsonl'), episode())
    t.ok(f.scan()[0] == 0 and f.table(), 'stale-table control: the first scan writes a table')
    f.write(os.path.join(pd, 'torn\nscan: x.jsonl'), [episode()[0], '{"torn', episode()[1]])
    rc, o, e = f.scan()
    t.ok(rc == 2 and not f.table(), 'a refused scan removes the earlier table')
    rc2, o2, _ = f.run(['cap-template'])
    t.ok(rc2 == 2 and 'table-unreadable' in o2, 'cap-template then refuses: there is no table to draft from')
    rt = os.path.join(f.state, 'model-router', 'refused.txt')
    body = open(rt, 'rb').read() if os.path.exists(rt) else b''
    t.ok(body.count(b'\n') == 1 and b'torn%0Ascan:%20x.jsonl' in body, 'refused.txt: a newline in a name stays on one line')
    os.unlink(os.path.join(pd, 'torn\nscan: x.jsonl'))
    t.ok(f.scan()[0] == 0 and not os.path.exists(rt), 'a clean scan removes a stale refused.txt')
    repo = os.path.join(fx.base, 'srcrepo')
    os.makedirs(os.path.join(repo, '.git'))
    g = Fx(fx.base)
    g.write(os.path.join(repo, 'cfg', 'projects', '-p', 's.jsonl'), episode())
    rc, o, e = g.run(['scan', '--claude', os.path.join(repo, 'cfg'), '--codex', g.codex])
    t.ok(rc == 2 and 'source-in-work-tree' in e and not g.table(), 'a --claude dir in a work tree refuses')
    rc, o, e = g.run(['scan', '--claude', g.claude, '--codex', os.path.join(repo, 'cdx')])
    t.ok(rc == 2 and 'source-in-work-tree' in e, 'a --codex dir in a work tree refuses')
    tb = os.path.join(repo, 'table.tsv')
    with open(tb, 'w') as fh:
        fh.write(f.table() or 'x')
    rc, o, e = g.run(['cap-template', '--table', tb])
    t.ok(rc == 2 and 'table-in-work-tree' in o, 'a --table in a work tree refuses')
    g.write(os.path.join(g.proj(), 's.jsonl'), sum((episode(sid='g%d' % i) for i in range(12)), []))
    t.ok(g.scan()[0] == 0 and g.run(['cap-template', '--table',
                                     os.path.join(g.state, 'model-router', 'table.tsv')])[0] == 0,
         'control: a --table outside any tree drafts')
    # A hand-written table naming an id on one task: validated, yet not drafted (F2).
    lines = g.table().split('\n')
    row = [ln for ln in lines if ln.startswith('main\t')][0].split('\t')
    row[3], row[5] = 'claude-planted-9@high', '1'
    body = '\n'.join(lines[:3] + ['\t'.join(row)]) + '\n'
    forged = os.path.join(g.base, 'forged.tsv')
    with open(forged, 'w') as fh:
        fh.write(body + '#end\trows=1\tsha256=%s\n' % hashlib.sha256(body.encode()).hexdigest())
    rc, o, e = g.run(['cap-template', '--table', forged])
    t.ok(rc == 2 and 'table-naming' in o and 'claude-planted-9' not in o, 'read_table refuses a pre-cap id under the floor')
    body = body.replace('\tcap=none\t', '\tcap=0123456789ab\t')
    with open(forged, 'w') as fh:
        fh.write(body + '#end\trows=1\tsha256=%s\n' % hashlib.sha256(body.encode()).hexdigest())
    rc, o, e = g.run(['cap-template', '--table', forged])
    t.ok(rc == 1 and 'no draft' in o and 'claude-planted-9' not in o, 'cap-template applies the naming floor itself')
    # The hint for a target in a work tree.
    h = Fx(fx.base)
    h.env['XDG_STATE_HOME'] = os.path.join(repo, 'st')
    rc, o, e = h.scan()
    t.ok(rc == 2 and 'XDG_STATE_HOME' in e and 'outside any work tree' in e, 'a target refusal names its remedy')
    # An existing --out that is not a table is never replaced (panel: --out settings.json).
    k = Fx(fx.base)
    k.write(os.path.join(k.proj(), 's.jsonl'), episode())
    victim = _mk(os.path.join(k.base, 'settings.json'), '{"keep": true}\n')
    rc, o, e = k.scan('--out', victim)
    t.ok(rc == 2 and 'target-not-a-table' in e and open(victim).read() == '{"keep": true}\n',
         'an existing non-table --out refuses and is left as it was')
    out2 = os.path.join(k.base, 't2.tsv')
    t.ok(k.scan('--out', out2)[0] == 0 and k.scan('--out', out2)[0] == 0, 'control: a table replaces its own table')
    # An unwritable state dir refuses BEFORE the read, not with an internal error after it.
    if os.geteuid() != 0:
        ro = os.path.join(k.base, 'ro')
        os.makedirs(os.path.join(ro, 'model-router'))
        os.chmod(os.path.join(ro, 'model-router'), 0o500)
        k.env['XDG_STATE_HOME'] = ro
        try:
            rc, o, e = k.scan()
        finally:
            os.chmod(os.path.join(ro, 'model-router'), 0o700)
        t.ok(rc == 2 and 'target-unwritable' in e, 'an unwritable state dir refuses before reading')
    # A reducer that raises on a shape it did not foresee refuses that file, by path (O3).
    global family
    keep = family

    def odd(name):
        raise TypeError('unforeseen')
    family = odd
    try:
        m = Fx(fx.base)
        m.write(os.path.join(m.proj(), 's.jsonl'), episode(tools=[('Read', {'file_path': 'a.go'})]))
        rc, o, e = m.scan()
    finally:
        family = keep
    t.ok(rc == 2 and 'internal error' not in e and 'claude 1' in o, 'a reducer TypeError refuses its file, by source')


def t_code_pass(t, fx):
    """The Codex code pass's findings, each reproduced and fixed, each with a control."""
    b = fx.base
    os.makedirs(os.path.join(b, 'cprepo', '.git'))
    os.makedirs(os.path.join(b, 'cprepo', 'sub'))
    os.makedirs(os.path.join(b, 'outside', 'sub'))
    os.makedirs(os.path.join(b, 'plain'))
    os.symlink(os.path.join(b, 'cprepo', 'sub'), os.path.join(b, 'plain', 'link'))
    os.symlink(os.path.join(b, 'outside', 'sub'), os.path.join(b, 'plain', 'link2'))
    f = Fx(b)
    f.write(os.path.join(f.proj(), 's.jsonl'), episode())
    rc, o, e = f.scan('--out', os.path.join(b, 'plain', 'link', '..', 't.tsv'))
    t.ok(rc == 2 and 'target-in-work-tree' in e and not os.path.exists(os.path.join(b, 'cprepo', 't.tsv')),
         'A2: an --out reached through link/.. into a tree refuses')
    rc, o, e = f.scan('--out', os.path.join(b, 'plain', 'link2', '..', 't.tsv'))
    t.ok(rc == 0 and os.path.exists(os.path.join(b, 'outside', 't.tsv')), 'A2 control: the same outside a tree writes there')
    os.symlink(os.path.join(b, 'cprepo', 'sub'), os.path.join(b, 'entry'))
    os.symlink(os.path.join(b, 'home-cfg'), os.path.join(b, 'cprepo', 'sub', 'out'))
    ctl = _cap(fx, GOOD, name='a3.md')
    rc, line = _pk(fx, os.path.join(b, 'entry', 'out', 'a3.md'), 'qa')
    t.ok(rc == 1 and 'version-control work tree' in line, 'A3: a cap reached through a link that lives in a tree refuses')
    t.ok(_pk(fx, ctl, 'qa')[0] == 0, 'A3 control: the same file named directly picks')
    rc, o, e = f.run(['scan', '--claude', os.path.join(b, 'entry', 'out'), '--codex', f.codex])
    t.ok(rc == 2 and 'source-in-work-tree' in e, 'A3: a --claude reached through a link that lives in a tree refuses')
    t.ok(f.run(['scan', '--claude', os.path.join(b, 'home-cfg'), '--codex', f.codex])[0] == 0,
         'A3 control: the same directory named directly scans')
    # Re-verify round 1: a link whose target passes THROUGH a tree (plain/chain -> cprepo/escape
    # -> home-cfg); realpath of each prefix jumps from plain/chain straight to home-cfg.
    os.symlink(os.path.join(b, 'home-cfg'), os.path.join(b, 'cprepo', 'escape'))
    os.symlink(os.path.join(b, 'cprepo', 'escape'), os.path.join(b, 'plain', 'chain'))
    os.symlink(os.path.join(b, 'home-cfg'), os.path.join(b, 'plain', 'chain2'))
    os.symlink(os.path.join('..', 'cprepo', 'escape'), os.path.join(b, 'plain', 'relchain'))
    rc, line = _pk(fx, os.path.join(b, 'plain', 'chain', 'a3.md'), 'qa')
    t.ok(rc == 1 and 'version-control work tree' in line, 'RV1: a cap reached through a chain via a tree refuses')
    rc, line = _pk(fx, os.path.join(b, 'plain', 'relchain', 'a3.md'), 'qa')
    t.ok(rc == 1 and 'version-control work tree' in line, 'RV1: the same through a relative link refuses')
    t.ok(_pk(fx, os.path.join(b, 'plain', 'chain2', 'a3.md'), 'qa')[0] == 0,
         'RV1 control: a cap through a link straight outside every tree picks')
    rc, o, e = f.run(['scan', '--claude', os.path.join(b, 'plain', 'chain'), '--codex', f.codex])
    t.ok(rc == 2 and 'source-in-work-tree' in e, 'RV1: a --claude reached through a chain via a tree refuses')
    t.ok(f.run(['scan', '--claude', os.path.join(b, 'plain', 'chain2'), '--codex', f.codex])[0] == 0,
         'RV1 control: a --claude through a link straight outside every tree scans')
    os.symlink('loop2', os.path.join(b, 'plain', 'loop1'))
    os.symlink('loop1', os.path.join(b, 'plain', 'loop2'))
    rc, line = _pk(fx, os.path.join(b, 'plain', 'loop1', 'a3.md'), 'qa')
    t.ok(rc == 1, 'RV1: a symlink loop refuses the cap')
    t.ok(path_in_work_tree(os.path.join(b, 'plain', 'loop1', 'a3.md')) is True,
         'RV1: a symlink loop is judged in a tree (the walk fails closed, not only the read)')
    t.ok(path_in_work_tree(os.path.join(b, 'plain', 'chain2', 'a3.md')) is False,
         'RV1 control: the walk returns False outside every tree')
    g = Fx(b)
    pd = g.proj()
    g.write(os.path.join(pd, 's.jsonl'), episode())
    os.symlink(os.path.join(b, 'no-such-target'), os.path.join(pd, 'dangling.jsonl'))
    rc, o, e = g.scan()
    t.ok(rc == 2 and 'claude 1' in o, 'A4: a dangling .jsonl link refuses')
    os.unlink(os.path.join(pd, 'dangling.jsonl'))
    global _listdir
    keep = _listdir

    def ghost(d):
        return keep(d) + (['ghost.jsonl'] if d == pd else [])
    _listdir = ghost
    try:
        rc, o, e = g.scan()
    finally:
        _listdir = keep
    t.ok(rc == 2 and 'claude 1' in o, 'A4: a listed entry gone before its stat refuses')
    t.ok(g.scan()[0] == 0, 'A4 control: the same project scans')
    tp = os.path.join(g.state, 'model-router', 'table.tsv')
    keep_uid = os.geteuid
    os.geteuid = lambda: keep_uid() + 1
    try:
        rc, o, e = g.scan()
    finally:
        os.geteuid = keep_uid
    t.ok(rc == 2 and 'target-not-owned' in e, 'A5: a target another user owns refuses')
    good = g.table()
    lines = good.split('\n')
    rows = [ln for ln in lines if ln.startswith('main\t')]
    row = rows[0].split('\t')
    row[0] = 'codex'
    row[3] = 'claude-opus-5-5@high'
    body = '\n'.join(lines[:3] + ['\t'.join(row)]) + '\n'
    with open(tp, 'w') as fh:
        fh.write(body + '#end\trows=1\tsha256=%s\n' % hashlib.sha256(body.encode()).hexdigest())
    rc, o, e = g.run(['cap-template'])
    t.ok(rc == 2 and 'table-naming' in o, 'B1: a codex row naming an id refuses')
    global MAX_TABLE
    keep_max = MAX_TABLE
    t.ok(g.scan()[0] == 0, 'B2 fixture: the table rewritten')
    good = g.table()
    try:
        MAX_TABLE = len(good)
        rc1 = g.run(['cap-template'])[0]
        with open(tp, 'a') as fh:
            fh.write('PLANTED\n')
        rc2, o2, _ = g.run(['cap-template'])
    finally:
        MAX_TABLE = keep_max
    t.ok(rc1 == 1 and rc2 == 2 and 'table-too-large' in o2, 'B2: a byte past the table limit refuses; at it, reads')
    for name, change in (('requestId', lambda r: r.__setitem__('requestId', 'r2')),
                         ('a float token count', lambda r: r['message']['usage'].__setitem__('output_tokens', 1500.0))):
        h = Fx(b)
        a = episode(sid='cp')
        del a[1]['message']['id']
        a[1]['requestId'] = 'r1'
        c = json.loads(json.dumps(a))
        change(c[1])
        h.write(os.path.join(h.proj(), '1.jsonl'), a)
        h.write(os.path.join(h.proj(), '2.jsonl'), c + episode(sid='cq'))
        rc, o, e = h.scan()
        t.ok(rc == 0 and 'conflicts=1 ' in o, 'B3/B4: copies differing in %s conflict' % name)
    h = Fx(b)
    a = episode(sid='cp')
    h.write(os.path.join(h.proj(), '1.jsonl'), a)
    h.write(os.path.join(h.proj(), '2.jsonl'), json.loads(json.dumps(a)) + episode(sid='cq'))
    rc, o, e = h.scan()
    t.ok(rc == 0 and 'conflicts=0 ' in o, 'B3/B4 control: equal copies do not conflict')


def t_naming_bound(t, fx):
    """At most MAX_NAMED ids before a cap, counted over kept, in-scope tasks (panel F1)."""
    d = fx.proj()
    recs = []
    for k in range(MAX_NAMED + 1):
        for i in range(NAME_FLOOR + k):
            recs += episode(sid='b%d-%d' % (k, i), model='claude-bound-%d' % k)
    fx.write(os.path.join(d, 'b.jsonl'), recs)
    rc, o, e = fx.scan()
    tab = fx.table()
    t.ok(rc == 0 and 'claude-bound-0@' not in tab and 'claude-bound-1@' in tab
         and 'claude-bound-%d@' % MAX_NAMED in tab, 'only the %d most-used ids are named' % MAX_NAMED)
    f = Fx(fx.base)
    recs = []
    for i in range(NAME_FLOOR):
        recs += episode(sid='c%d' % i, model='claude-scoped-1', cwd='/x/proj' if i else '/elsewhere')
    f.write(os.path.join(f.proj(), 'c.jsonl'), recs)
    t.ok(f.scan()[0] == 0 and 'claude-scoped-1@' in f.table(), 'scope control: unscoped, the id is named')
    t.ok(f.scan('--cwd-under', '/x/proj')[0] == 0 and 'claude-scoped-1@' not in f.table(),
         'an out-of-scope task does not count toward the naming floor')


def t_gaps(t, fx):
    """Paths the cross-check found no test reaching."""
    bad = _cap(fx, GOOD.replace('decided=2026-11-01', 'decided='), name='undated.md')
    fx.write(os.path.join(fx.proj(), 's.jsonl'), episode())
    rc, o, e = fx.scan('--cap', bad)
    t.ok(rc == 2 and 'not usable' in o, 'scan --cap with a refused cap exits 2')
    rc, o, e = fx.run(['pick', '--cap', bad, '--role', 'main', '--all', '--class', 'qa'])
    t.ok(rc == 2 and '--all takes no' in e, '--all with --class is a usage refusal')
    t.ok(fx.scan()[0] == 0, 'gaps: the fixture scans')
    tp = os.path.join(fx.state, 'model-router', 'table.tsv')
    good = fx.table()
    lines = good.split('\n')
    body = '\n'.join(lines[:2] + [lines[2].replace('tasks', 'jobs')] + lines[3:-2]) + '\n'
    with open(tp, 'w') as fh:
        fh.write(body + '#end\trows=%d\tsha256=%s\n' % (len(lines) - 5, hashlib.sha256(body.encode()).hexdigest()))
    rc, o, e = fx.run(['cap-template'])
    t.ok(rc == 2 and 'table-columns' in o, 'table refusal: renamed columns (table-columns)')
    t.ok(fx.scan()[0] == 0, 'gaps: the table rewritten')
    rc, o, e = fx.run(['cap-template'])
    t.ok(rc == 1 and 'no draft' in o, 'cap-template with no named id prints no draft, exit 1')
    # CLAUDE_CONFIG_DIR and CODEX_HOME are read when no flag names a source; a flag wins.
    f = Fx(fx.base)
    alt = os.path.join(f.base, 'alt-claude')
    f.write(os.path.join(alt, 'projects', '-p', 's.jsonl'), episode())
    f.write(os.path.join(f.base, 'alt-codex', 'sessions', '2026', '11', '10', 'r.jsonl'), codex_rollout())
    f.env['CLAUDE_CONFIG_DIR'] = alt
    f.env['CODEX_HOME'] = os.path.join(f.base, 'alt-codex')
    rc, o, e = f.run(['scan'])
    t.ok(rc == 0 and 'main_files=1' in o and 'codex_files=1' in o, 'CLAUDE_CONFIG_DIR and CODEX_HOME are read')
    rc, o, e = f.scan()
    t.ok(rc == 0 and 'main_files=0' in o and 'codex=absent' in o, 'a source flag wins over the environment')
    # isSidechain and isMeta records start no main task; notebook_path and path count as edits.
    g = Fx(fx.base)
    side = dict(episode()[0], isSidechain=True)
    meta = dict(episode()[0], isMeta=True)
    nb = episode(sid='nb', tools=[('NotebookEdit', {'notebook_path': '/x/proj/a.ipynb'}),
                                  ('Write', {'path': '/x/proj/b.csv'}), ('Write', {'path': '/x/proj/c.csv'})])
    g.write(os.path.join(g.proj(), 's.jsonl'), [side, meta] + nb)
    rc, o, e = g.scan()
    t.ok(rc == 0 and 'tasks: main=1 ' in o and 'main tasks by class: data=1' in o,
         'a sidechain or meta record starts no task; notebook_path and path are edits')
    # Codex token_count keeps the largest running total.
    h = Fx(fx.base)
    cx = codex_rollout()
    for v in (2000, 5000, 3000):
        cx.append({'timestamp': '2026-11-10T10:00:05Z', 'type': 'event_msg',
                   'payload': {'type': 'token_count', 'info': {'total_token_usage': {'output_tokens': v}}}})
    h.write(os.path.join(h.codex, 'sessions', '2026', '11', '10', 'r.jsonl'), cx)
    rc, o, e = h.scan()
    cxr = [r.split('\t') for r in h.table().splitlines() if r.startswith('codex\t')]
    t.ok(rc == 0 and len(cxr) == 1 and cxr[0][8] == '5', 'codex token_count: the largest total, in thousands')


def self_test(out):
    tmp = tempfile.mkdtemp(prefix='model-router-selftest-')
    if in_work_tree(tmp):
        os.rmdir(tmp)
        out.write('FAIL the self-test temp dir is inside a work tree; set TMPDIR elsewhere\n')
        return 1
    # Run from inside the temp dir (panel, portability): a relative --touch resolves against the
    # cwd, and from an installed skill's own directory (~/.claude/...) every one read protected.
    try:
        old = os.getcwd()
    except OSError:
        old = None
    os.chdir(tmp)
    t = T(out)
    tests = [t_cap_refusals, t_work_tree, t_pick_rules, t_args, t_canary, t_canary_refusal, t_fence, t_internal_error,
             t_sources, t_vocab, t_table, t_dedup, t_signals, t_alias, t_scope_and_template, t_gate_at_parts,
             t_sources_eacces, t_protect_roots, t_protect_shapes, t_cap_file_more, t_scan_robust, t_naming_bound,
             t_gaps, t_code_pass]
    try:
        for fn in tests:
            try:
                fn(t, Fx(tmp))
            except Exception as e:  # a test that crashes is a failure, named by type only
                t.ok(False, '%s raised %s' % (fn.__name__, type(e).__name__))
    finally:
        if old is not None:
            try:
                os.chdir(old)
            except OSError:
                pass
        _rmtree(tmp)
    if t.fails:
        out.write('router.py self-test: %d of %d checks FAILED\n' % (len(t.fails), t.n))
        return 1
    out.write('router.py self-test: PASS (%d checks)\n' % t.n)
    return 0


def _rmtree(d):
    for root, dirs, files in os.walk(d, topdown=False):
        for n in files:
            p = os.path.join(root, n)
            try:
                os.chmod(p, 0o600)
            except OSError:
                pass
            os.unlink(p)
        for n in dirs:
            p = os.path.join(root, n)
            if os.path.islink(p):
                os.unlink(p)
            else:
                os.chmod(p, 0o700)
                os.rmdir(p)
    os.rmdir(d)


if __name__ == '__main__':
    sys.exit(main(sys.argv[1:]))
