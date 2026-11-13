#!/usr/bin/env python3
"""gate-hook.py -- the ONE spelling of the Claude Code PreToolUse gate entry,
and the install report's reading of a settings file against it (D-107).

    python3 -I scripts/gate-hook.py entry                 # README's PreToolUse entry, one JSON line
    python3 -I scripts/gate-hook.py command               # its command string, one line
    python3 -I scripts/gate-hook.py legacy                # the old fail-open command, one line
    python3 -I scripts/gate-hook.py check-readme <README.md> [<USAGE.md>]
    python3 -I scripts/gate-hook.py report <settings.json>
    python3 -I scripts/gate-hook.py browser-report <settings.json>
    python3 -I scripts/gate-hook.py tokens                # install.sh's `wanted`, one line

WHY THE LINE CHANGED. The documented gate was
`[ -x "$HOME/bin/buddy" ] && "$HOME/bin/buddy" gate; exit 0`. `buddy gate` says
deny with one JSON line on stdout and exit 0, and allow with nothing at all, so a
buddy that DIED before printing (a Go panic exits 2, SIGKILL 137, a corrupt
binary 126) left stdout empty, and `; exit 0` turned the death into the hook's
"no decision": Claude Code let the tool call through. Measured 2026-10-27 at
dd7daff: modernc.org/libc v1.67.1's netdb init reads /etc/services before main
and panics on any error but ENOENT; with that read denied by a sandbox-exec
profile, buddy exited 2 silently and the hook exited 0 with empty stdout. The
libc v1.73.4 pinned since tolerates a permission error there (measured: the
same profile, a deciding gate), so that trigger is gone and the class is not:
cp over a running Mach-O makes the kernel SIGKILL the next run (CLAUDE.md), and
any panic exits 2. Invariant 2 says the safety hook fails CLOSED.

WHAT THE LINE DOES NOW, read from Claude Code's hooks reference (2.1.311):
exit 2 BLOCKS a PreToolUse call whatever is on stdout (partial JSON included),
with stderr as the reason unless stdout holds a blocking decision, whose own
reason is used; any other non-zero exit is a non-blocking error, and
the call goes ahead. So:
  - no executable at $HOME/bin/buddy: exit 0, silent. Installation is the
    opt-in; a machine with no binary has no feature to fail.
  - buddy exits 0: its stdout passes through untouched (a deny, or nothing).
  - buddy exits non-zero for any reason, a signal included: exit 2 with ONE
    fixed stderr line naming the fix. buddy's own stderr is discarded with the
    brace group's: a panic's text is multi-line, unfenced, and would become the
    model-visible block reason when stdout holds no blocking decision
    (invariant 9; Codex design pass). The gate writes nothing to stderr on any
    path that decides, so nothing is lost there.
  - `"onFailure": "block"` (Claude Code 2.1.295+) covers what no line can: a
    hook that times out, or a shell that cannot start, otherwise lets the call
    through. Documented, not measured here (no native run has timed one out),
    and older builds are not promised to ignore the key.

REFUTED: capturing stdout and printing a fixed deny JSON with `exit 0` (the
dependency review's first proposal). It needs `$(...)` capture and JSON quoted
inside a JSON string, and an exit-0 verdict depends on Claude parsing that
JSON, where exit 2 depends on the code alone.

NOT COVERED, and said so in D-107: a binary that runs and exits 0 having decided
nothing (an executable holding only `:`, or a ZERO-BYTE executable, which every
shell runs as an empty script: exit 0, measured under sh, bash, dash and zsh;
install.sh renames a probed build into place, so it never leaves one), a
timeout on a build without onFailure, `[ -x ]` reading false mid-copy (a hand
copy's window; install.sh's rename has none), and
the adapter's own process dying (its wrapper denies every error from `buddy
gate`, not its own SIGKILL).

The report is advisory: it reads the operator's settings, never writes them,
and a value it prints goes out json.dumps'd, one line.
"""

import json
import re
import sys

COMMAND = (
    '[ -x "$HOME/bin/buddy" ] || exit 0; '
    '{ "$HOME/bin/buddy" gate; } 2>/dev/null || '
    '{ echo "buddy gate failed (status $?); refusing the tool call (the gate fails closed).'
    ' Operator, from a terminal: sh scripts/install.sh in the buddy-system checkout" >&2; exit 2; }'
)
LEGACY = '[ -x "$HOME/bin/buddy" ] && "$HOME/bin/buddy" gate; exit 0'
MATCHER = "Edit|Write|NotebookEdit|Bash"
TIMEOUT = 10
ENTRY = {
    "matcher": MATCHER,
    "hooks": [{"type": "command", "command": COMMAND, "timeout": TIMEOUT, "onFailure": "block"}],
}

# EVERY buddy hook line, spelled once (D-135). README's "Hook wiring" block and
# USAGE's browser-slot block are held equal to these (check-readme, run by
# check-gate-line.sh), install.sh's report names its tokens (`wanted`, held to
# TOKENS by check-install.sh), and `install.sh --wire-hooks` appends exactly
# these entries, so the line a doc shows, the line the report names and the line
# the writer adds cannot drift apart.
def _guarded(binary, verb):
    return '[ -x "$HOME/bin/%s" ] && "$HOME/bin/%s" %s 2>/dev/null; exit 0' % (binary, binary, verb)


def _cmd(binary, verb):
    return {"type": "command", "command": _guarded(binary, verb)}


README_HOOKS = {
    "SessionStart": [{"hooks": [_cmd("buddy", "hello")]}],
    "PreToolUse": [ENTRY],
    "PostToolUse": [{"matcher": "*", "hooks": [_cmd("buddy", "beat"), _cmd("buddylist", "alert")]}],
    "Stop": [{"hooks": [_cmd("buddy", "idle")]}],
    "UserPromptSubmit": [{"hooks": [_cmd("buddy", "busy")]}],
    "SessionEnd": [{"hooks": [_cmd("buddy", "bye"), _cmd("buddylist", "presence --gone")]}],
}
# The two optional #71 entries (D-134), USAGE "The browser slot".
BROWSER_MATCHER = "mcp__.*"
BROWSER_HOOKS = {ev: [{"matcher": BROWSER_MATCHER, "hooks": [_cmd("buddy", "beat")]}]
                 for ev in ("PreToolUse", "PostToolUseFailure")}
# The report's tokens, in install.sh's order (`wanted`), each with its event; a
# browser token names the event its beat goes on.
TOKENS = [("buddy hello", "SessionStart"), ("buddy gate", "PreToolUse"), ("buddy beat", "PostToolUse"),
          ("buddy idle", "Stop"), ("buddy bye", "SessionEnd"), ("buddy busy", "UserPromptSubmit"),
          ("buddylist alert", "PostToolUse"), ("buddylist presence", "SessionEnd"),
          ("browser PreToolUse", "PreToolUse"), ("browser PostToolUseFailure", "PostToolUseFailure")]


def line_for(token):
    """(event, entry) the writer appends for a report token: README's own hook
    object in a NEW entry of its own, under README's matcher (none where README
    has none). One entry per hook: appending never edits an existing one."""
    ev = dict(TOKENS)[token]
    if token.startswith("browser "):
        return ev, BROWSER_HOOKS[ev][0]
    binary, verb = token.split(" ")
    for e in README_HOOKS[ev]:
        for h in e["hooks"]:
            if ('"$HOME/bin/%s" %s' % (binary, verb)) in h["command"]:
                out = {"matcher": e["matcher"]} if "matcher" in e else {}
                out["hooks"] = [h]
                return ev, out
    raise KeyError(token)


# A PreToolUse command that runs `buddy gate`, however the binary is spelled:
# `"$HOME/bin/buddy" gate`, `/abs/bin/buddy gate`, `BUDDY_HARNESS=codex buddy gate`.
# Not `buddy-adapter hook ... --buddy /abs/buddy`: that wrapper is the adapter's
# own strict contract (internal/harness/claude.go), checked there.
GATE_RE = re.compile(r"buddy[\"']?\s+gate(?![\w-])")
NEEDED = ("Edit", "Write", "NotebookEdit", "Bash")


def gate_hooks(settings):
    """Every (matcher, hook) pair under hooks.PreToolUse whose command runs the gate."""
    hooks = settings.get("hooks") if isinstance(settings, dict) else None
    pre = hooks.get("PreToolUse") if isinstance(hooks, dict) else None
    for entry in pre if isinstance(pre, list) else []:
        if not isinstance(entry, dict) or not isinstance(entry.get("hooks"), list):
            continue
        for h in entry["hooks"]:
            if isinstance(h, dict) and isinstance(h.get("command"), str) and GATE_RE.search(h["command"]):
                yield entry.get("matcher"), h


def matcher_names(m):
    """The tool names a matcher selects as an exact list, "*" for every tool, or
    None when Claude would read it as a regular expression. Per the hooks
    reference: only letters, digits, `_`, `-`, spaces, `,` and `|` is an exact
    list split on `|` or `,` with optional surrounding whitespace; "", "*" and
    an absent matcher select every tool."""
    if m is None or m in ("", "*"):
        return "*"
    if not isinstance(m, str) or not re.fullmatch(r"[A-Za-z0-9_\- ,|]+", m):
        return None
    return {n.strip() for n in re.split(r"[|,]", m)}


def judge(h):
    """'legacy', or a list of reasons this hook object is not README's (empty: it is)."""
    if h["command"] == LEGACY:
        return "legacy"
    why = []
    if h["command"] != COMMAND:
        why.append("command differs from README's")
    if h.get("type") != "command":
        why.append("type is not command")
    if h.get("onFailure") != "block":
        why.append('onFailure is not "block"')
    t = h.get("timeout")
    if isinstance(t, bool) or not isinstance(t, (int, float)) or not 1 <= t <= 600:
        why.append("no timeout of 1-600 s")
    if h.get("async") is True or h.get("asyncRewake") is True:
        why.append("async")
    # Keys that change whether, or how, the command runs (the line panel): an
    # `if` rule narrows the calls it sees, `args` is exec form (no shell to run
    # the line), and `shell` hands it to another interpreter.
    for key, what in (("if", "an if rule narrows when it runs"), ("args", "exec form (args)"), ("shell", "a shell is set")):
        if key in h:
            why.append(what)
    return why


def set_reasons(verdicts, settings):
    """Why a set of non-legacy gate hooks is not README's entry, or [] when it is.
    Coverage is the union over every gate entry: README's entry split in two
    (`Edit|Write` and `NotebookEdit|Bash`) gates the same calls."""
    why = next((v for _, v in verdicts if v), [])
    if why:
        return why
    names, every = set(), False
    for m, _ in verdicts:
        n = matcher_names(m)
        if n is None:
            return ["matcher %s not read as an exact list" % json.dumps(m)]
        if n == "*":
            every = True
        else:
            names |= n
    gaps = [] if every else [n for n in NEEDED if n not in names]
    if gaps:
        return ["the gate's matchers miss %s" % ", ".join(gaps)]
    if settings.get("disableAllHooks") is True:
        return ["disableAllHooks is true in the same file"]
    return []


# THE DEPTH CAP (#96, D-182), the one definition every settings reader uses:
# this report and browser-report, install.sh's Agent-model reader, wire-hooks.py
# (check, wire, unwire, and its re-decode of each splice), uninstall.sh's
# ~/.claude.json reader. Each loads this file and calls decode() below, which
# runs too_deep on the TEXT first, so a nest past the cap reads "not checked" (or is refused)
# on every Python and stack size. Before it, the interpreter decided: up to
# 3.13 json raised RecursionError at a fixed depth that differs by version
# (992 levels on 3.9.6, 9,997 on 3.12.13; the Fable cross-check); 3.14's decoder
# is bounded by the C stack, so a 100000-deep nest decoded with room (Homebrew's
# 3.14.7, whose Python.app main thread has 16 MB whatever `ulimit -s` says;
# uv's 3.14.4 at 64 MB) and not without it (uv's 3.14.4 at 8 MB; #95), and one
# file read two ways.
# 64: the deepest of 44 real settings files on the developing machine
# (~/.claude.json, every settings.json, project settings, .mcp.json; counted
# 2026-11-12) is 7 containers, README's block 6; and every walk after the
# decode (copy.deepcopy, about two frames a level; wire-hooks' span scan; a
# json.dumps of a matcher) stays far inside the 1000-frame limit at 64. The
# RecursionError catches stay. Two are still reached, by a regex matcher's own
# nesting, which is not JSON depth (600 groups in a matcher, a 6-deep file): browser-
# report's re.search, and wire-hooks main's catch around the same reading
# (check-install holds both). The decode
# catches are the second line, reached by no file past the cap.
MAX_DEPTH = 64
DEEP = "nested deeper than %d levels" % MAX_DEPTH
# One linear pass over the characters that can matter. REFUTED: stripping
# strings with a regex first (`"[^"\\]*(?:\\.[^"\\]*)*"`): on an unterminated
# string of escaped quotes it fails and retries at every later quote, quadratic
# on a 1 MiB file (Codex design pass). Outside a string, every `"` opens one; in
# one, a backslash skips the character after it; so on any text json.loads
# accepts, and on the valid prefix of any it rejects, the count is the
# decoder's own nesting. A duplicate key's overwritten value counts too: the
# decoder walks it before dropping it.
_TOKEN = re.compile(r'[\[\]{}"\\]')


def too_deep(text):
    """True when JSON text nests more than MAX_DEPTH containers, `{}` being 1."""
    depth, in_str, skip = 0, False, -1
    for m in _TOKEN.finditer(text):
        i = m.start()
        if i == skip:
            continue
        c = text[i]
        if in_str:
            if c == "\\":
                skip = i + 1
            elif c == '"':
                in_str = False
        elif c == '"':
            in_str = True
        elif c in "[{":
            depth += 1
            if depth > MAX_DEPTH:
                return True
        elif c in "]}":
            depth -= 1
    return False


# THE ONE SETTINGS DECODE (#97, D-183): every reader above calls decode(), never
# too_deep and json.loads apart, so a reader cannot get one check without the
# other. Python's json takes NaN, Infinity and -Infinity by default; JSON does
# not, and neither does Claude Code: a settings.json holding one (README's gate
# entry beside `"x": NaN`) "did not load, so nothing in them is in effect,
# including hooks" (Claude Code 2.1.323, a temp HOME, each of the three), while
# the report called the same file wired and the writer refused it. Fail-open on
# the gate's wiring. parse_constant is called by the C and the pure-Python
# scanner alike, with only those three literal names (so the name printed is
# never file text), at any depth, before an object's duplicate-key hook sees
# the object; a check on the decoded values would miss `"x": NaN, "x": 1`, and
# a finite-number check would refuse 1e400, which both parsers read as
# infinity (Codex design pass). NotChecked is a ValueError, so a reader that
# lacks its own arm still lands in its not-JSON one: closed, words aside.
CONSTANT = "a non-JSON constant %s"


class NotChecked(ValueError):
    """str(e) is the writer's reason; e.said is the reports' predicate."""

    def __init__(self, reason, verb):
        ValueError.__init__(self, reason)
        self.said = "%s %s" % (verb, reason)


def _constant(name):
    raise NotChecked(CONSTANT % name, "holds")


def decode(text, object_pairs_hook=None):
    """The depth cap on the text, then json.loads refusing NaN and Infinity."""
    if too_deep(text):
        raise NotChecked(DEEP, "is")
    return json.loads(text, parse_constant=_constant, object_pairs_hook=object_pairs_hook)


def report(path):
    try:
        with open(path, encoding="utf-8") as f:
            text = f.read()
        settings = decode(text)
    except FileNotFoundError:
        return "gate: no %s; not checked" % path
    except NotChecked as e:
        return "gate: %s %s; not checked" % (path, e.said)
    except (OSError, ValueError, RecursionError) as e:
        return "gate: %s is not JSON this report can read (%s); not checked" % (path, type(e).__name__)
    # Decoded is not walkable (#95). Up to 3.13 json.load raised RecursionError
    # at a fixed depth (992 on 3.9.6, 9,997 on 3.12.13), so a 100000-deep file
    # never got here. From 3.14 the decoder is bounded by the real C stack: a terminated
    # [ * 100000 decodes when the stack has room (Homebrew's 3.14.7, 16 MB,
    # uv's 3.14.4 at 64 MB, measured; the ubuntu-26.04 runner's 3.14.4 read an
    # unterminated one to its end) and not when it has less (uv's 3.14.4 at
    # 8 MB). What decodes can still be too deep to
    # serialize: json.dumps of a 100000-deep matcher raised RecursionError
    # (3.14.7) and the report died in a traceback, which install printed as
    # "could not run scripts/gate-hook.py". Caught here, it says what it is:
    # not checked. Only the crash becomes a verdict; a walk that finishes
    # decides exactly as it did. Since the cap (D-182) no file past 64 levels
    # gets here, so this catch is the second line, not the first.
    try:
        return _judged(path, settings)
    except RecursionError:
        return "gate: %s is nested deeper than this report can walk; not checked" % path


# What every line that names a gate entry as README's or custom ends with (#99,
# #100, D-184); LEGACY alone, a fault verdict, does not. This
# report reads the hook entries and nothing else, and Claude Code unloads the
# WHOLE file, hooks included, for a schema error in an unrelated key (measured on
# 2.1.323: `"model": 5`, `"permissions": {"allow": "x"}`), and per its docs
# (unmeasured) for an array-valued matcher in another PreToolUse entry. A local
# reader can prove a file broken, never that it loads, so the line says what it
# checked (README's entry: judge's keys, nothing else on it, so never "well
# formed": the Fable lens printed that for a hook beside a 5, which the writer
# refuses) and names the one check that can say the rest, as the USER's: a bare
# "run claude doctor" read by an agent running install invites a run that, under
# a temp HOME, put a Keychain dialog on the operator's screen. REFUTED:
# validating the known top-level keys (two measured failing shapes of an
# unbounded set; a false alarm whenever Claude Code loosens a type); the
# schemastore schema (lags the CLI; a network fetch or a fork); install running
# `claude doctor` itself (measured: npm debug logs and a macOS Keychain write
# attempt under a temp HOME, and a skipped entry names the file as a skipped file
# does). install.sh's Agent-model reader prints it from here; its grep line
# spells it again in shell, held to this by check-install.sh.
LOADS = "whether Claude Code loads the file is not checked here; the user's check is claude doctor"


def _judged(path, settings):
    found = list(gate_hooks(settings))
    if not found:
        return 'gate: no PreToolUse buddy gate hook in %s (README "Hook wiring")' % path
    verdicts = [(m, judge(h)) for m, h in found]
    others = [(m, v) for m, v in verdicts if v != "legacy"]
    why = set_reasons(others, settings) if others else ["no gate but the LEGACY one"]
    if len(others) < len(verdicts):
        if not why:
            # Deny wins over no decision, so README's entry beside the old one
            # still refuses for a dead buddy; the old one is a second run per
            # call that decides nothing when it matters (the false-denials panel).
            return (
                "gate: README's fail-closed gate entry in %s, with the LEGACY command beside it (D-107): the gate "
                "runs twice per call and the old one decides nothing when buddy dies; remove that entry; settings unchanged; %s" % (path, LOADS)
            )
        return (
            "gate: LEGACY fail-open command in %s: a buddy that crashes or is killed lets the tool call "
            "through (D-107). Replace that PreToolUse entry with README's; settings unchanged: %s"
            % (path, json.dumps(ENTRY))
        )
    if why:
        return (
            'gate: custom PreToolUse gate in %s (%s); crash and timeout blocking NOT VERIFIED; compare README '
            '"Hook wiring"; settings unchanged; %s' % (path, "; ".join(why), LOADS)
        )
    return "gate: README's fail-closed gate entry in %s (onFailure block, timeout set); %s" % (path, LOADS)


# What `who`'s browser clause needs (D-134): beat on a matcher that selects a
# browser tool. Moved here from install.sh's 6a' heredoc (D-135) so the report
# and `--wire-hooks` read one function. An exact name counts by its prefix; a
# regex is searched unanchored, as the harness does, against one name per
# server, so a regex for other names on them can read as excluding; one Python
# cannot read is not judged. PREFIXES is browser.go's list, held to it by a test.
PREFIXES = ["mcp__claude-in-chrome__", "mcp__playwright__", "mcp__plugin_playwright_playwright__",
            "mcp__Claude_Browser__", "mcp__remote-devices__Claude_Browser__", "mcp__chrome-devtools__",
            "mcp__plugin_chrome-devtools-mcp_chrome-devtools__", "mcp__browser-use__"]
PROBES = [p + "browser_click" for p in PREFIXES]


def selects_browser(m):
    names = matcher_names(m)
    if names == "*":
        return True
    if names is None:
        try:
            return any(re.search(m, p) for p in PROBES)
        except (re.error, TypeError):
            return True
    return any(n.startswith(p) for n in names for p in PREFIXES)


def beat_matchers(hooks, ev):
    # An entry that is not an object, or a command that is not a string, is
    # skipped: one such entry made the whole reading print nothing (panel).
    for e in hooks.get(ev) or []:
        if not isinstance(e, dict):
            continue
        cmds = [h["command"] for h in e.get("hooks") or [] if isinstance(h, dict) and isinstance(h.get("command"), str)]
        if any(re.search(r'buddy\\?"? beat', c) for c in cmds):
            yield e.get("matcher")


def browser_gone(settings):
    """The events of the two optional #71 entries the report names missing: those
    where no buddy beat selects a browser tool. A PreToolUse beat already sees a
    call that fails, so PostToolUseFailure alone is not named (Codex code pass)."""
    hooks = settings.get("hooks") or {}
    # any() stops at the first matcher that selects, as the old loop did: a later
    # regex is never tried (Codex code pass, #71: backtracking could stall it).
    gone = [ev for ev in ("PreToolUse", "PostToolUseFailure") if not any(selects_browser(m) for m in beat_matchers(hooks, ev))]
    return [] if gone == ["PostToolUseFailure"] else gone


UNREADABLE = (ValueError, RecursionError, AttributeError, TypeError)


def browser_report(settings):
    # Each line read on its own: the heredoc this replaced printed the first
    # before a shape the second cannot read ("PreToolUse": 1) ended it, and
    # one catch around both lost it (Codex code pass).
    out = []
    try:
        post = list(beat_matchers(settings.get("hooks") or {}, "PostToolUse"))
        if post and not any(selects_browser(m) for m in post):
            out.append('hooks: PostToolUse runs buddy beat on matcher %s, which selects no browser tool it was tried on, so '
                       '`who` reports no browser tool call; README "Hook wiring" uses "*" (D-134)' % ", ".join(json.dumps(m) for m in post))
    except UNREADABLE:
        pass
    try:
        gone = browser_gone(settings)
    except UNREADABLE:
        gone = []
    if gone:
        out.append('hooks: no buddy beat on %s for browser tools, so `who` misses a browser call %s; docs/USAGE.md "The browser '
                   'slot" has the optional lines (#71)' % (" or ".join(gone), " or ".join(
                       {"PreToolUse": "in flight", "PostToolUseFailure": "that failed"}[ev] for ev in gone)))
    return out


def block_after(path, heading):
    """The first ```json block after a heading line, decoded."""
    with open(path, encoding="utf-8") as f:
        text = f.read()
    at = text.find(heading)
    if at < 0:
        raise ValueError("no %r heading" % heading)
    m = re.compile(r"```json\n(.*?)\n```", re.S).search(text, at)
    if not m:
        raise ValueError("no ```json block after %r" % heading)
    return json.loads(m.group(1))


def readme_entries(path):
    """hooks.PreToolUse of the JSON block under README's "Hook wiring" heading."""
    with open(path, encoding="utf-8") as f:
        text = f.read()
    at = text.find("### 3. Hook wiring")
    if at < 0:
        raise ValueError('no "### 3. Hook wiring" heading')
    m = re.compile(r"```json\n(.*?)\n```", re.S).search(text, at)
    if not m:
        raise ValueError("no ```json block after the heading")
    return json.loads(m.group(1))["hooks"]["PreToolUse"]


def main(argv):
    if argv == ["entry"]:
        print(json.dumps(ENTRY))
    elif argv == ["command"]:
        print(COMMAND)
    elif argv == ["legacy"]:
        print(LEGACY)
    elif len(argv) == 2 and argv[0] == "report":
        print(report(argv[1]))
    elif len(argv) == 2 and argv[0] == "browser-report":
        # Advisory, as the heredoc was: a file it cannot read prints nothing,
        # and one nested past the cap or holding NaN is a file it cannot read
        # (NotChecked is a ValueError, in UNREADABLE; D-182, D-183).
        try:
            with open(argv[1], encoding="utf-8") as f:
                text = f.read()
            lines = browser_report(decode(text))
        except (OSError,) + UNREADABLE:
            lines = []
        for l in lines:
            print(l)
    elif argv == ["tokens"]:
        print(" ".join(t for t, _ in TOKENS if not t.startswith("browser ")))
    elif argv[:1] == ["check-readme"] and len(argv) in (2, 3):
        try:
            got = readme_entries(argv[1])
            whole = block_after(argv[1], "### 3. Hook wiring")["hooks"]
            browser = block_after(argv[2], "### The browser slot") if len(argv) == 3 else BROWSER_HOOKS
        except (OSError, ValueError, KeyError, TypeError) as e:
            print("gate-hook: a hook block cannot be read: %s" % e, file=sys.stderr)
            return 1
        if got != [ENTRY]:
            print("gate-hook: README's PreToolUse is not exactly the gate entry here:\n  README: %s\n  want:   %s"
                  % (json.dumps(got), json.dumps([ENTRY])), file=sys.stderr)
            return 1
        # D-135: the whole block, and USAGE's browser block, are what --wire-hooks appends.
        if whole != README_HOOKS:
            print("gate-hook: README's hook block is not README_HOOKS here:\n  README: %s\n  want:   %s"
                  % (json.dumps(whole), json.dumps(README_HOOKS)), file=sys.stderr)
            return 1
        if browser != BROWSER_HOOKS:
            print("gate-hook: USAGE's browser block is not BROWSER_HOOKS here:\n  USAGE: %s\n  want:  %s"
                  % (json.dumps(browser), json.dumps(BROWSER_HOOKS)), file=sys.stderr)
            return 1
    else:
        print((__doc__ or "").split("\n\n")[1], file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
