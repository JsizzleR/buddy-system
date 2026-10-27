#!/usr/bin/env python3
"""gate-hook.py -- the ONE spelling of the Claude Code PreToolUse gate entry,
and the install report's reading of a settings file against it (D-107).

    python3 -I scripts/gate-hook.py entry                 # README's PreToolUse entry, one JSON line
    python3 -I scripts/gate-hook.py command               # its command string, one line
    python3 -I scripts/gate-hook.py legacy                # the old fail-open command, one line
    python3 -I scripts/gate-hook.py check-readme <README.md>
    python3 -I scripts/gate-hook.py report <settings.json>

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


def report(path):
    try:
        with open(path, encoding="utf-8") as f:
            settings = json.load(f)
    except FileNotFoundError:
        return "gate: no %s; not checked" % path
    except (OSError, ValueError, RecursionError) as e:
        return "gate: %s is not JSON this report can read (%s); not checked" % (path, type(e).__name__)
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
                "runs twice per call and the old one decides nothing when buddy dies; remove that entry; settings unchanged" % path
            )
        return (
            "gate: LEGACY fail-open command in %s: a buddy that crashes or is killed lets the tool call "
            "through (D-107). Replace that PreToolUse entry with README's; settings unchanged: %s"
            % (path, json.dumps(ENTRY))
        )
    if why:
        return (
            'gate: custom PreToolUse gate in %s (%s); crash and timeout blocking NOT VERIFIED; compare README '
            '"Hook wiring"; settings unchanged' % (path, "; ".join(why))
        )
    return "gate: README's fail-closed gate entry in %s (onFailure block, timeout set); native loading not verified here" % path


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
    elif len(argv) == 2 and argv[0] == "check-readme":
        try:
            got = readme_entries(argv[1])
        except (OSError, ValueError, KeyError, TypeError) as e:
            print("gate-hook: README's hook block cannot be read: %s" % e, file=sys.stderr)
            return 1
        if got != [ENTRY]:
            print("gate-hook: README's PreToolUse is not exactly the gate entry here:\n  README: %s\n  want:   %s"
                  % (json.dumps(got), json.dumps([ENTRY])), file=sys.stderr)
            return 1
    else:
        print((__doc__ or "").split("\n\n")[1], file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
