#!/usr/bin/env python3
r"""agent-model-gate.py -- a Claude Code PreToolUse hook on the Agent tool: a
general-purpose (or untyped) subagent call names its model, or it does not run
(D-117).

    python3 -I "$HOME/bin/agent-model-gate.py"            # the hook: payload on stdin
    python3 -I -B scripts/hooks/agent-model-gate.py --self-test

WHY. A subagent spawned with no `model` runs on its PARENT's model. Counted over
every main-session transcript of 2026-10-24..28 (197 files, 87 with Agent calls;
docs/CHEAPER-MODELS-PLAN.md section 0): 31 Agent calls named no model, 17 of
them general-purpose. Nine came from Opus parents, seven of those review-shaped
(panel lenses and design reads that the review tier wanted on a NAMED model);
five came from Fable parents: one Fable review that forgot its param and got
Fable by luck, and four pieces of side work (a Codex pass, a second opinion, a
measurement run, a skill edit) that ran on Fable by inheritance, against the
operator's rule that side work never does. The skill already said to name the
model. Prose did not hold, and a receipt naming a model cannot tell a chosen one
from an inherited one (the plan's second Codex design pass, its one blocking
point). Only something that runs BEFORE the subagent can stop the omission.

MEASURED, 2026-10-28 16:18, Claude Code 2.1.312: two `claude -p` runs on a
Sonnet parent, a --settings file wiring this script (its draft) to matcher
`Agent`. A general-purpose call with no `model` was refused before any subagent
ran (0 subagent transcripts); the parent read the refusal and quoted it. The
same call with `model: sonnet` ran (1 transcript). That run's result record
called the refused call `Task`, the tool's older name, while the hook's payload
said `Agent` (this script refused it on that name); both names are taken here.

WHAT IT REFUSES. An Agent call whose type is missing, empty, `general-purpose`
or `claude` (the catch-all the tool lists as "FleetView's default when no
agent name is typed": general-purpose under another name, inheriting the same
way), compared after
NFC, strip and lower-case, whose `model` is not a non-empty string. Exit 2 with
one line on stderr, which the harness hands the model as the reason. The line
names the values the call's `model` parameter takes, family aliases, never a
full id: the tool's schema accepts `sonnet`, `opus`, `haiku` and `fable` only,
and a refusal that told the model to type `claude-opus-5-5` (the draft's) named
a value the next try could not use. The line echoes nothing from the payload.

WHAT IT LETS THROUGH, exit 0 and silent. Explore and Plan (they inherit by
design, and the variable that moves the default does not move them), a fork
(it always inherits: it is the parent, continued), and every other NAMED type,
built-in or the user's own (`claude-code-guide`, `statusline-setup`, a custom
agent): a named type carries its own `model:` or is a narrow helper, and none
of the 31 unnamed calls was one. A custom type with no `model:` field inherits;
that is what the operator's settings floor (CLAUDE_CODE_SUBAGENT_MODEL, the
plan's 1.3 point 3, OPEN) catches, not this hook. Any other tool.

FAILS OPEN. Stdin that is not a JSON object, a tool input that is not an
object, a type that is neither a string nor null (no call the tool's schema
admits; a null type is read as an omitted one, which refuses), or any
exception: exit 0. This teaches a convention; it is not a safety gate.
Invariant 2's fail-CLOSED is about buddy's ledger hooks, where a silent allow
loses a claim; here a broken python3 or an unforeseen payload must not stop
every subagent on the machine, and a missing python3 is a non-blocking hook
error the harness lets through anyway.

INSTALLED BY HAND. The operator copies it beside their other hook scripts and
wires it in `~/.claude/settings.json`, `hooks.PreToolUse`:
  {"matcher": "Agent", "hooks": [{"type": "command", "command": "python3 -I \"$HOME/bin/agent-model-gate.py\""}]}
install.sh does not install it and nothing here edits settings: hook wiring is
the operator's (CLAUDE.md).

CUT. Checking that the named model is a valid alias: the tool's schema refuses
anything else before a hook sees it. Reading agent definitions to see whether a
named type carries `model:`: a file walk per call for a case the settings floor
covers. CLAUDE_CODE_SUBAGENT_MODEL_FORCE=1 instead of a hook: it overrides the
model a call names, so a `fable` cross-check would silently not run on Fable.
NOT COVERED: a HOME where the hook is not wired, and harness features that
spawn agents without the Agent tool (teammates, workflow agents); the settings
floor reads for both.
"""
import json
import sys
import unicodedata

# The catch-all types: what a call gets when it names none.
CATCH_ALL = frozenset(("", "general-purpose", "claude"))
TOOL_NAMES = frozenset(("Agent", "Task"))

REFUSAL = (
    "AGENT-MODEL gate: name the model on this Agent call: model takes a family "
    "alias (sonnet, opus, haiku, fable), not a full id. opus for a panel lens or "
    "a lane's review subagent, fable only for a cross-check or a ruling the "
    "review tier names, sonnet for side work (a report, a diff read, "
    "housekeeping). With no model a general-purpose subagent runs on this "
    "session's own model, which the operator's rules forbid.\n"
)


def fold(s):
    return unicodedata.normalize("NFC", s).strip().lower()


def verdict(raw):
    """Return True to refuse. Anything this cannot read is an allow."""
    d = json.loads(raw)
    if not isinstance(d, dict) or d.get("tool_name") not in TOOL_NAMES:
        return False
    i = d.get("tool_input")
    if not isinstance(i, dict):
        return False
    t = i.get("subagent_type")
    if t is None:
        t = ""
    if not isinstance(t, str) or fold(t) not in CATCH_ALL:
        return False
    m = i.get("model")
    return not (isinstance(m, str) and m.strip())


def hook():
    try:
        refuse = verdict(sys.stdin.buffer.read())
    except Exception:
        refuse = False
    if refuse:
        sys.stderr.write(REFUSAL)
        sys.exit(2)
    sys.exit(0)


def self_test():
    """Run THIS file as the harness does, one child per payload, and check its
    exit code and stderr. Every refused payload is paired with its positive
    control, the same payload naming a model, which must be allowed: a refusal
    that also fired on the control would be refusing something other than the
    missing model."""
    import subprocess

    def agent(**ti):
        return json.dumps({"tool_name": "Agent", "tool_input": dict({"description": "d", "prompt": "p"}, **ti)})

    def run(payload):
        p = subprocess.run([sys.executable, "-I", "-B", __file__], input=payload.encode("utf-8"),
                           stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=30)
        return p.returncode, p.stdout.decode("utf-8", "replace"), p.stderr.decode("utf-8", "replace")

    refused = {
        "general-purpose, no model": agent(subagent_type="general-purpose"),
        "untyped, no model": agent(),
        "type null, no model": agent(subagent_type=None),
        "type empty, no model": agent(subagent_type=""),
        "type blank, no model": agent(subagent_type="  "),
        "type General-Purpose": agent(subagent_type="General-Purpose"),
        "type padded": agent(subagent_type=" general-purpose\n"),
        "type claude (catch-all)": agent(subagent_type="claude"),
        "model empty": agent(subagent_type="general-purpose", model=""),
        "model blank": agent(subagent_type="general-purpose", model=" \t"),
        "model null": agent(subagent_type="general-purpose", model=None),
        "model not a string": agent(subagent_type="general-purpose", model=0),
        "tool named Task": json.dumps({"tool_name": "Task", "tool_input": {"subagent_type": "general-purpose", "prompt": "p"}}),
    }
    allowed = {
        "Explore, no model": agent(subagent_type="Explore"),
        "Plan, no model": agent(subagent_type="Plan"),
        "fork, no model": agent(subagent_type="fork"),
        "claude-code-guide, no model": agent(subagent_type="claude-code-guide"),
        "statusline-setup, no model": agent(subagent_type="statusline-setup"),
        "custom type, no model": agent(subagent_type="my-reviewer"),
        "non-Agent tool": json.dumps({"tool_name": "Bash", "tool_input": {"command": "true"}}),
        "garbage stdin": "not json {",
        "empty stdin": "",
        "a JSON list": "[]",
        "tool_input a list": json.dumps({"tool_name": "Agent", "tool_input": ["general-purpose"]}),
        "type not a string": agent(subagent_type=["general-purpose"]),
    }
    # Every alias the model parameter takes, not only the controls' one.
    for alias in ("sonnet", "opus", "haiku", "fable"):
        allowed["general-purpose, model %s" % alias] = agent(subagent_type="general-purpose", model=alias)
    # The positive control of each refusal: the same call, model named.
    for name, payload in list(refused.items()):
        d = json.loads(payload)
        d["tool_input"]["model"] = "opus"
        allowed["control of '%s'" % name] = json.dumps(d)

    bad = []
    for name, payload in refused.items():
        rc, out, err = run(payload)
        if rc != 2 or out or err != REFUSAL:
            bad.append("refused case '%s': exit %d, stdout %d bytes, stderr %r" % (name, rc, len(out), err[:80]))
    for name, payload in allowed.items():
        rc, out, err = run(payload)
        if rc != 0 or out or err:
            bad.append("allowed case '%s': exit %d, stdout %d bytes, stderr %r" % (name, rc, len(out), err[:80]))
    # The refusal names only values the call's model parameter takes.
    if "claude-" in REFUSAL:
        bad.append("the refusal names a full model id; the Agent tool's model takes a family alias")
    # The settings line in the header is what the operator copies: it must parse.
    lines = [l.strip() for l in (__doc__ or "").splitlines() if l.strip().startswith('{"matcher"')]
    try:
        entry = json.loads(lines[0]) if len(lines) == 1 else None
        ok = entry is not None and entry["hooks"][0]["command"] == 'python3 -I "$HOME/bin/agent-model-gate.py"'
    except Exception:
        ok = False
    if not ok:
        bad.append("the header's settings line is not one JSON entry running the installed script")
    for b in bad:
        print("agent-model-gate self-test: FAIL %s" % b, file=sys.stderr)
    if bad:
        sys.exit(1)
    print("agent-model-gate self-test: %d refused, %d allowed (%d of them positive controls)"
          % (len(refused), len(allowed), len(refused)))


if __name__ == "__main__":
    if sys.argv[1:] == ["--self-test"]:
        self_test()
    else:
        hook()
