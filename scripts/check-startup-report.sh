#!/bin/sh
# check-startup-report.sh — done-check for scripts/startup-report.sh (D-051).
#
# HERMETIC: sh, git, jq. The transcripts are a fixture in a throwaway
# CLAUDE_PROJECTS_DIR; nothing under ~/.claude is read. jq is the one tool
# beyond the toolchain and git, and macOS and GitHub's Ubuntu runners both ship
# it; without it this check says NOT RUN on stderr rather than failing a fresh
# clone the hermetic tier promises needs nothing else.
#
# What must hold, each with the control that proves the assertion could fire:
#   - the window runs from the first TYPED prompt (not a meta entry, not a
#     compact summary, not a `<command-name>` echo) to the first edit of a
#     file INSIDE the repo, by either spelling of the root (the fixture edits
#     through the RESOLVED one, /private/var on macOS); a write to a
#     scratchpad, or through a `..` out of the repo, is "other" and does not
#     end it;
#   - the transcript directory is found for a repo path with a dot, an é and
#     an emoji: the expected name is written out by hand from the harness's
#     rule (each UTF-16 code unit outside [A-Za-z0-9] -> one '-', so é is one
#     and the emoji two), never computed the way the report computes it, so a
#     shared mistake cannot pass;
#   - a prompt that is PASTED text (`<pasted_content …>`) opens a window,
#     where every other leading `<` is taken for the harness's own;
#   - `KB` is bytes, not characters, over string and array tool results;
#   - worktrees: an edit in a registered linked worktree ends a window (G); a
#     session started IN one is found under its own transcript directory,
#     named by hand from the path the harness saw (H, and git lists the
#     resolved path, so this is the /var-versus-/private/var mapping on macOS);
#     an edit under a REMOVED sibling named `<repo>-…` counts (I); a write into
#     an EXISTING sibling that is another repo, or to a file beside the repo,
#     does not (G's controls);
#   - calls in a subagent sidechain and calls after the first edit (in a later
#     message, or later in the edit's own message) are not counted; an LSP
#     call is counted as lsp;
#   - a session with no edit is counted, not listed; an unparseable transcript
#     is skipped, not fatal; a transcript older than the window is left out;
#   - no prompt, command or tool output text reaches the report (the fixture
#     carries marker strings, and the control greps the fixture for them).
set -eu
. "$(dirname -- "$0")/lib.sh"
ROOT=$(repo_root "$0")
CDPATH= cd -- "$ROOT"

fail() { echo "check-startup-report: FAIL — $*" >&2; exit 1; }

if ! command -v jq >/dev/null 2>&1; then
	echo "check-startup-report: NOT RUN — no jq on PATH" >&2
	exit 0
fi

WORK=$(mktemp -d) || fail "mktemp failed"
trap 'rm -rf "$WORK"' EXIT INT TERM

# mktemp's path is ASCII, so C-locale sed encodes it exactly; the suffix for
# "re.po-café-🙂" is spelled by hand: '.', '-', 'é' and '-' are one '-' each,
# and the emoji, two UTF-16 units, is two.
repo="$WORK/re.po-café-🙂"
mkrepo "$repo"
git -C "$repo" commit -q --allow-empty -m init
wt="$repo-wt"
git -C "$repo" worktree add -q --detach "$wt"
mkrepo "$repo-other"                       # a sibling that is ANOTHER repo
realtop=$(CDPATH= cd -- "$repo" && pwd -P)
proj="$WORK/projects/$(printf '%s' "$WORK" | LC_ALL=C sed 's/[^A-Za-z0-9]/-/g')-re-po-caf----"
mkdir -p "$proj"
big=$(printf '%3000s' '' | tr ' ' x)                  # 3000 bytes
wide=$(printf '%1000s' '' | sed 's/ /é/g')            # 1000 characters, 2000 bytes

# A: the full window. Typed prompt 10:00:00, first in-repo edit 10:12:30.
cat >"$proj/a.jsonl" <<EOF
{"type":"user","isMeta":true,"timestamp":"2026-09-26T09:00:00.000Z","message":{"content":"meta, not a prompt"}}
{"type":"user","timestamp":"2026-09-26T09:30:00.000Z","message":{"content":"<command-name>/clear</command-name>"}}
{"type":"user","isCompactSummary":true,"timestamp":"2026-09-26T09:45:00.000Z","message":{"content":"a summary, not a prompt"}}
{"type":"user","timestamp":"2026-09-26T10:00:00.000Z","message":{"content":"MARKER_PROMPT map the code"}}
{"type":"assistant","timestamp":"2026-09-26T10:01:00.000Z","message":{"content":[{"type":"tool_use","id":"1","name":"Bash","input":{"command":"cat MARKER_COMMAND"}}]}}
{"type":"user","timestamp":"2026-09-26T10:01:05.000Z","message":{"content":[{"type":"tool_result","tool_use_id":"1","content":"MARKER_OUTPUT"}]}}
{"type":"user","timestamp":"2026-09-26T10:01:06.000Z","message":{"content":[{"type":"tool_result","tool_use_id":"1","content":"$big"},{"type":"tool_result","tool_use_id":"1","content":[{"type":"text","text":"$wide"}]}]}}
{"type":"assistant","timestamp":"2026-09-26T10:02:00.000Z","message":{"content":[{"type":"tool_use","id":"2","name":"LSP","input":{"operation":"findReferences"}},{"type":"tool_use","id":"3","name":"Read","input":{"file_path":"$repo/a.go"}}]}}
{"type":"assistant","isSidechain":true,"timestamp":"2026-09-26T10:03:00.000Z","message":{"content":[{"type":"tool_use","id":"4","name":"Bash","input":{"command":"sidechain"}}]}}
{"type":"assistant","timestamp":"2026-09-26T10:05:00.000Z","message":{"content":[{"type":"tool_use","id":"5","name":"Write","input":{"file_path":"/tmp/scratch/prompt.md","content":"x"}}]}}
{"type":"assistant","timestamp":"2026-09-26T10:06:00.000Z","message":{"content":[{"type":"tool_use","id":"5b","name":"Write","input":{"file_path":"$repo/../scratch/note.md","content":"x"}}]}}
{"type":"assistant","timestamp":"2026-09-26T10:12:30.000Z","message":{"content":[{"type":"tool_use","id":"6","name":"Edit","input":{"file_path":"$realtop/a.go"}},{"type":"tool_use","id":"6b","name":"Bash","input":{"command":"same message, after the edit"}}]}}
{"type":"assistant","timestamp":"2026-09-26T10:13:00.000Z","message":{"content":[{"type":"tool_use","id":"7","name":"Bash","input":{"command":"after the edit"}}]}}
EOF
# B: prompted, never edited.
cat >"$proj/b.jsonl" <<'EOF'
{"type":"user","timestamp":"2026-09-26T11:00:00.000Z","message":{"content":"look around"}}
{"type":"assistant","timestamp":"2026-09-26T11:01:00.000Z","message":{"content":[{"type":"tool_use","id":"1","name":"Bash","input":{"command":"ls"}}]}}
EOF
# E: the first prompt is pasted text; a Read, a Grep and a Glob (search), a
# Task and an Agent (agent) — the two columns no other session exercises, so a
# broken mapping there cannot read as a correct zero (Sonnet, second pass) —
# then an in-repo edit spelled through a `.` segment (in the repo all the
# same) three and a half minutes in. Every median stays off a rounding tie.
cat >"$proj/e.jsonl" <<EOF
{"type":"user","timestamp":"2026-09-26T12:00:00.000Z","message":{"content":"<pasted_content id=\"1\">a stack trace</pasted_content> fix this"}}
{"type":"assistant","timestamp":"2026-09-26T12:01:00.000Z","message":{"content":[{"type":"tool_use","id":"1","name":"Read","input":{"file_path":"$repo/a.go"}},{"type":"tool_use","id":"1g","name":"Grep","input":{"pattern":"x"}},{"type":"tool_use","id":"1l","name":"Glob","input":{"pattern":"*.go"}}]}}
{"type":"assistant","timestamp":"2026-09-26T12:02:00.000Z","message":{"content":[{"type":"tool_use","id":"1t","name":"Task","input":{}},{"type":"tool_use","id":"1a","name":"Agent","input":{}}]}}
{"type":"assistant","timestamp":"2026-09-26T12:03:30.000Z","message":{"content":[{"type":"tool_use","id":"2","name":"Edit","input":{"file_path":"$repo/./a.go"}}]}}
EOF
# G: started in the main checkout; a Read, then writes into the sibling that is
# another repo and to a file beside the repo (neither ends the window), then an
# edit in the registered linked worktree two minutes in (it does).
cat >"$proj/g.jsonl" <<EOF
{"type":"user","timestamp":"2026-09-26T13:00:00.000Z","message":{"content":"work in the worktree"}}
{"type":"assistant","timestamp":"2026-09-26T13:00:30.000Z","message":{"content":[{"type":"tool_use","id":"1","name":"Read","input":{"file_path":"$repo/a.go"}}]}}
{"type":"assistant","timestamp":"2026-09-26T13:01:00.000Z","message":{"content":[{"type":"tool_use","id":"2","name":"Write","input":{"file_path":"$repo-other/x.go","content":"x"}},{"type":"tool_use","id":"3","name":"Write","input":{"file_path":"$repo-notes.md","content":"x"}}]}}
{"type":"assistant","timestamp":"2026-09-26T13:02:00.000Z","message":{"content":[{"type":"tool_use","id":"4","name":"Edit","input":{"file_path":"$wt/b.go"}}]}}
EOF
# H: started IN the linked worktree, so filed under ITS directory: "-wt" after
# the repo's own "----", one '-' for the '-' and none for "wt".
wtproj="$WORK/projects/$(printf '%s' "$WORK" | LC_ALL=C sed 's/[^A-Za-z0-9]/-/g')-re-po-caf-----wt"
mkdir -p "$wtproj"
cat >"$wtproj/h.jsonl" <<EOF
{"type":"user","timestamp":"2026-09-26T14:00:00.000Z","message":{"content":"from the worktree"}}
{"type":"assistant","timestamp":"2026-09-26T14:06:30.000Z","message":{"content":[{"type":"tool_use","id":"1","name":"Edit","input":{"file_path":"$wt/c.go"}}]}}
EOF
# I: an edit in a sibling worktree that has since been removed (never created
# here), four and a half minutes in.
cat >"$proj/i.jsonl" <<EOF
{"type":"user","timestamp":"2026-09-26T15:00:00.000Z","message":{"content":"in a worktree removed since"}}
{"type":"assistant","timestamp":"2026-09-26T15:01:00.000Z","message":{"content":[{"type":"tool_use","id":"1","name":"Bash","input":{"command":"true"}}]}}
{"type":"assistant","timestamp":"2026-09-26T15:04:30.000Z","message":{"content":[{"type":"tool_use","id":"2","name":"Edit","input":{"file_path":"$repo-gone/d.go"}}]}}
EOF
# C: not JSON at all.
printf 'not json\n' >"$proj/c.jsonl"
# D: a full window, but older than the default seven days.
sed 's/2026-09-26T1/2026-08-01T1/' "$proj/a.jsonl" >"$proj/d.jsonl"
touch -t 202608010000 "$proj/d.jsonl"

out=$(CLAUDE_PROJECTS_DIR="$WORK/projects" sh scripts/startup-report.sh "$repo" 2>&1) ||
	fail "the report exited non-zero:
$out"

# The one listed row: 12.5 min; read 1, search 0, bash 1, lsp 1, agent 0,
# other 2 (the scratchpad Write and the one through `..`); KB 4 = (13 + 3000 +
# 2027) / 1024, where 2027 is the array result as JSON: 27 bytes of structure
# and 1000 two-byte é. Counting characters instead would give 3.
printf '%s\n' "$out" | grep -q '^2026-09-26 10:00  *12\.5  *1  *0  *1  *1  *0  *2  *4$' ||
	fail "session A's row is wrong or missing:
$out"
printf '%s\n' "$out" | grep -q '^2026-09-26 12:00  *3\.5  *1  *2  *0  *0  *2  *0  *0$' ||
	fail "the pasted-prompt session's row is wrong or missing:
$out"
printf '%s\n' "$out" | grep -q '^2026-09-26 13:00  *2\.0  *1  *0  *0  *0  *0  *2  *0$' ||
	fail "G (edit in a linked worktree, after writes to another repo and a sibling file) is wrong or missing:
$out"
printf '%s\n' "$out" | grep -q '^2026-09-26 14:00  *6\.5  *0  *0  *0  *0  *0  *0  *0$' ||
	fail "H (a session started in the linked worktree) is wrong or missing:
$out"
printf '%s\n' "$out" | grep -q '^2026-09-26 15:00  *4\.5  *0  *0  *1  *0  *0  *0  *0$' ||
	fail "I (an edit in a removed sibling worktree) is wrong or missing:
$out"
printf '%s\n' "$out" | grep -q '^5 session(s) reached an edit, 1 did not\.$' ||
	fail "want five edited sessions and one without (the unparseable and the old one left out):
$out"
# Medians over an ODD count (A E G H I): 2.0 3.5 [4.5] 6.5 12.5 min; calls
# 0 1 [3] 5 5; KB 0 0 [0] 0 4.9. The widened run below adds D for an EVEN count.
printf '%s\n' "$out" | grep -q '^median: 4\.5 min, 3 tool calls, 0 KB returned before the first edit$' ||
	fail "the median line is wrong (odd count):
$out"
printf '%s\n' "$out" | grep -q '^lsp: 1 of 14 calls in the window (7%)$' ||
	fail "the lsp line is wrong:
$out"
if printf '%s\n' "$out" | grep -q '2026-08-01'; then
	fail "a transcript older than the window was listed:
$out"
fi
# The control: the window, widened, does take the old transcript in.
# 36500 days, so the control outlives the fixture's fixed 2026 dates.
widened=$(BUDDY_COST_DAYS=36500 CLAUDE_PROJECTS_DIR="$WORK/projects" sh scripts/startup-report.sh "$repo" 2>&1)
printf '%s\n' "$widened" | grep -q '^2026-08-01 10:00  *12\.5 ' ||
	fail "the control failed: a 36500-day window did not list the old transcript:
$widened"
# EVEN count (D A E G H I): (4.5 + 6.5) / 2 min, (3 + 5) / 2 calls, (0 + 0) / 2 KB.
printf '%s\n' "$widened" | grep -q '^median: 5\.5 min, 4 tool calls, 0 KB returned before the first edit$' ||
	fail "the median line is wrong (even count):
$widened"
# Oldest first: the rows are sorted by start, not by file name or find order.
order=$(printf '%s\n' "$widened" | grep '^20[0-9][0-9]-' | cut -c1-16 | tr '\n' '|')
[ "$order" = "2026-08-01 10:00|2026-09-26 10:00|2026-09-26 12:00|2026-09-26 13:00|2026-09-26 14:00|2026-09-26 15:00|" ] ||
	fail "rows are not oldest first: $order"

# Privacy, with its control: the markers ARE in the fixture.
grep -q MARKER_OUTPUT "$proj/a.jsonl" || fail "the privacy control failed: the fixture lost its markers"
if printf '%s\n%s\n' "$out" "$widened" | grep -q MARKER_; then
	fail "transcript text reached the report:
$out"
fi

# Refusals, each with its reason.
if BUDDY_COST_DAYS=0 sh scripts/startup-report.sh "$repo" >/dev/null 2>&1; then
	fail "BUDDY_COST_DAYS=0 was accepted"
fi
none=$(CLAUDE_PROJECTS_DIR="$WORK/nowhere" sh scripts/startup-report.sh "$repo" 2>&1) ||
	fail "a missing projects directory is not an error, and exited non-zero"
printf '%s\n' "$none" | grep -q '^no transcripts: ' || fail "a missing projects directory was not said:
$none"

echo "check-startup-report: ok"
