#!/bin/sh
# check-wake-report.sh — done-check for scripts/wake-report.sh (#45).
#
# HERMETIC: sh, git, jq. The transcripts are a fixture in a throwaway
# CLAUDE_PROJECTS_DIR; nothing under ~/.claude is read. Without jq this check
# says NOT RUN on stderr, as check-startup-report.sh does, rather than failing
# a fresh clone the hermetic tier promises needs nothing else.
#
# What must hold, each with the control that proves the assertion could fire:
#   - a request is counted ONCE: a requestId written on three records, and a
#     message.id standing in for a missing requestId written on two, each
#     place a duplicate far enough after the first that counting it would add
#     a wake the table does not have; its time is its FIRST record's;
#   - a sidechain record and a `<synthetic>` record carry usage and are not
#     requests: either one, counted, would split a gap and move a wake into a
#     lower bucket; a subagents/ transcript is not read at all;
#   - a 4-minute gap is not a wake, and the bucket edges are half-open (a gap
#     of exactly 50m, 65m and 2h lands in the bucket it opens);
#   - warm and cold are cache_creation against cache_read, per bucket, with
#     the re-written tokens summed over the cold ones only;
#   - every trigger kind is attributed, and only cold wakes past 60 minutes
#     reach that table (a cold wake at 15 minutes does not); an isMeta record
#     riding after the waker (hook output, a scheduled fire's own prompt)
#     never takes the attribution from it;
#   - declared and armed: a real `buddy wait --until`, never a commit message
#     or grep pattern naming the verb, never `buddy wait check`; armed by a
#     ScheduleWakeup prompt, never by a CronCreate for something else;
#   - a wait-check fire is counted when it fires — one superseded by the
#     operator's prompt, one after the last request — and "warm" is the
#     request each woke, at any gap; the next request of the same turn was
#     not woken by it; the fire's own prompt never takes the attribution in
#     any of the three shapes measured after a fire, the plain user record
#     stamped turnOrigin "scheduled" included;
#   - a torn line costs the line, not the transcript; a file that is not JSON
#     is scanned and yields nothing;
#   - BUDDY_WAKE_DAYS: a transcript modified before the window is left out
#     (control: a window that reaches its mtime takes it in), and a request
#     timed before the window is left out even in a file modified inside it,
#     while its successor's gap is still measured from it;
#   - the knob is named (#98): the header says BUDDY_WAKE_DAYS and follows its
#     value; BUDDY_COST_DAYS alone draws ONE line saying it is not this
#     report's and leaves the window at 7, its value never echoed, and
#     neither BUDDY_WAKE_DAYS beside it nor no knob at all draws that line;
#     USAGE names BUDDY_WAKE_DAYS beside wake-report.sh, not BUDDY_COST_DAYS;
#   - the default project directory is the cwd repo's, named by the harness's
#     rule and spelled here by hand, never computed the way the report does;
#     its linked worktrees' directories join it, from the main checkout and
#     from inside a worktree, and nothing else does: not a decoy sharing the
#     encoded prefix, not a FILE with the worktree prefix (nor does one drop
#     the real directory after it), not the transcripts of another repository
#     a worktree is parked under; a named directory is read alone;
#   - a named directory holding no transcript at all (missing, empty, or a
#     repo checkout's path, said to be one) refuses, exit 2, naming it and
#     what the argument is, with nothing on stdout; one whose transcripts are
#     all older than the window prints the zero report, exit 0 (and a window
#     reaching them reads them); a repo with no transcript directory, run
#     with no argument, still says so, exit 0;
#   - no body, tool result, command or prompt text reaches the report: markers
#     are planted in each, and the control greps the fixture for them first.
set -eu
. "$(dirname -- "$0")/lib.sh"
ROOT=$(repo_root "$0")
CDPATH= cd -- "$ROOT"

fail() { echo "check-wake-report: FAIL — $*" >&2; exit 1; }

if ! command -v jq >/dev/null 2>&1; then
	echo "check-wake-report: NOT RUN — no jq on PATH" >&2
	exit 0
fi

WORK=$(mktemp -d) || fail "mktemp failed"
trap 'rm -rf "$WORK"' EXIT INT TERM
P="$WORK/projects/-fixture"
mkdir -p "$P/sess-a/subagents"

# One helper per record shape, so a fixture line reads as what it is.
# req <time> <id> <cache_read> <cache_creation> [extra-fields]
req() { printf '{"type":"assistant","timestamp":"%s","requestId":"%s","message":{"model":"m","usage":{"input_tokens":3,"cache_read_input_tokens":%s,"cache_creation_input_tokens":%s}%s}}\n' "$1" "$2" "$3" "$4" "${5:-}"; }
human() { printf '{"type":"user","timestamp":"%s","origin":{"kind":"human"},"message":{"content":"%s"}}\n' "$1" "$2"; }

# A — operator wakes, the dedup, the sidechain, the synthetic, a 4m gap.
{
	human 2026-09-26T10:00:00.000Z "MARKER_BODY start"
	req 2026-09-26T10:00:05.000Z a1 0 1000
	human 2026-09-26T10:04:00.000Z "four minutes later"
	req 2026-09-26T10:04:05.000Z a2 1000 10 ',"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"grep -n \"buddy wait\" MARKER_CMD; buddy wait check; buddy wait --help"}}]'
	echo '{"type":"user","timestamp":"2026-09-26T10:04:06.000Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"MARKER_RESULT"}]}}'
	# Counted, the sidechain would put a2 -> a3 at 40m instead of 66m.
	echo '{"type":"assistant","isSidechain":true,"timestamp":"2026-09-26T10:30:05.000Z","requestId":"side","message":{"usage":{"cache_read_input_tokens":1,"cache_creation_input_tokens":0}}}'
	human 2026-09-26T11:10:04.000Z "MARKER_BODY back"
	echo '{"type":"user","isMeta":true,"timestamp":"2026-09-26T11:10:04.500Z","message":{"content":"hook context MARKER_META"}}'
	# a3: 66m cold. Its duplicates at +2s and +10m must not count.
	req 2026-09-26T11:10:05.000Z a3 100 5000
	req 2026-09-26T11:10:07.000Z a3 100 5000
	req 2026-09-26T11:20:05.000Z a3 100 5000
	human 2026-09-26T12:05:00.000Z "warm one"
	# a4: 55m warm, no requestId — message.id stands in; its duplicate at +10m.
	echo '{"type":"assistant","timestamp":"2026-09-26T12:05:05.000Z","message":{"id":"msg_a4","usage":{"cache_read_input_tokens":5000,"cache_creation_input_tokens":10}}}'
	echo '{"type":"assistant","timestamp":"2026-09-26T12:15:05.000Z","message":{"id":"msg_a4","usage":{"cache_read_input_tokens":5000,"cache_creation_input_tokens":10}}}'
	# Counted, the synthetic would put a4 -> a5 at 40m instead of 65m.
	echo '{"type":"assistant","timestamp":"2026-09-26T12:30:05.000Z","message":{"id":"syn","model":"<synthetic>","usage":{"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}'
	# a5: exactly 65m, woken by a slash-command echo -> other.
	echo '{"type":"user","timestamp":"2026-09-26T13:10:04.000Z","message":{"content":"<command-name>/compact</command-name>"}}'
	req 2026-09-26T13:10:05.000Z a5 50 700
} >"$P/sess-a.jsonl"
# A subagent's transcript: never read. Counted, it adds a session and wakes.
{
	req 2026-09-26T09:00:00.000Z s1 0 100
	human 2026-09-26T15:00:00.000Z "x"
	req 2026-09-26T15:00:05.000Z s2 0 99999
} >"$P/sess-a/subagents/agent-1.jsonl"

# B — every other waker, and the warm and short buckets.
{
	human 2026-09-26T09:00:00.000Z "go"
	req 2026-09-26T09:00:05.000Z b1 0 900 ',"content":[{"type":"tool_use","id":"c1","name":"Bash","input":{"command":"git commit -m '"'"'teach `buddy wait --on x`'"'"'"}},{"type":"tool_use","id":"c2","name":"CronCreate","input":{"cron":"* * * * *","prompt":"MARKER_PROMPT tidy"}}]'
	echo '{"type":"user","isMeta":true,"timestamp":"2026-09-26T10:30:04.000Z","origin":{"kind":"peer","from":"uds:/fixture.sock"},"message":{"content":"MARKER_BODY from a peer"}}'
	req 2026-09-26T10:30:05.000Z b2 10 2000
	echo '{"type":"user","timestamp":"2026-09-26T12:30:04.000Z","origin":{"kind":"peer","from":"a1b2","handback":true},"message":{"content":"MARKER_BODY handback"}}'
	req 2026-09-26T12:30:05.000Z b3 10 3000
	echo '{"type":"user","timestamp":"2026-09-26T17:00:04.000Z","origin":{"kind":"task-notification"},"message":{"content":"<task-notification>MARKER_BODY</task-notification>"}}'
	req 2026-09-26T17:00:05.000Z b4 10 4000 ',"content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"sleep 4200"}}]'
	echo '{"type":"user","timestamp":"2026-09-26T18:10:04.000Z","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"MARKER_RESULT"}]}}'
	echo '{"type":"user","timest'
	req 2026-09-26T18:10:05.000Z b5 10 500
	human 2026-09-26T18:20:04.000Z "ten minutes"
	req 2026-09-26T18:20:05.000Z b6 9000 5
	echo '{"type":"user","timestamp":"2026-09-26T18:55:04.000Z","origin":{"kind":"task-notification"},"message":{"content":"x"}}'
	# Wrote exactly what it read: warm. Cold is wrote MORE than read (D-033).
	req 2026-09-26T18:55:05.000Z b7 500 500
	human 2026-09-26T19:57:04.000Z "62 minutes"
	req 2026-09-26T19:57:05.000Z b8 10 800
	human 2026-09-26T20:12:04.000Z "15 minutes, cold"
	req 2026-09-26T20:12:05.000Z b9 10 300
} >"$P/sess-b.jsonl"

# C — the keep-alive: declared, armed, and five wait-check fires.
fire() { printf '{"type":"system","subtype":"scheduled_task_fire","timestamp":"%s","prompt":"%s"}\n{"type":"user","isMeta":true,"timestamp":"%s","message":{"content":"%s"}}\n' "$1" "$2" "$1" "$2"; }
# The other two shapes the record after a fire takes (measured 2026-10-27, all
# three in every version from 2.1.283 to 2.1.311): stamped turnOrigin
# "scheduled" with a scheduledFireId, isMeta or NOT. The plain one, read as a
# waker, takes the attribution from the fire and drops its K row.
fire_sched() { printf '{"type":"system","subtype":"scheduled_task_fire","timestamp":"%s","prompt":"%s"}\n{"type":"user","timestamp":"%s","turnOrigin":"scheduled","scheduledFireId":"f1","wakeupSource":"loop_wakeup","message":{"content":"%s"}}\n{"type":"user","isMeta":true,"timestamp":"%s","wakeupSource":"loop_wakeup","message":{"content":[{"type":"text","text":"MARKER_META"}]}}\n' "$1" "$2" "$1" "$2" "$1"; }
fire_sched_meta() { printf '{"type":"system","subtype":"scheduled_task_fire","timestamp":"%s","prompt":"%s"}\n{"type":"user","isMeta":true,"timestamp":"%s","turnOrigin":"scheduled","scheduledFireId":"f2","message":{"content":"%s"}}\n' "$1" "$2" "$1" "$2"; }
{
	human 2026-09-26T08:00:00.000Z "park"
	req 2026-09-26T08:00:03.000Z c1 0 800 ',"content":[{"type":"tool_use","id":"w1","name":"Bash","input":{"command":"cd x && ~/bin/buddy wait --until 2h --note MARKER_CMD"}},{"type":"tool_use","id":"w2","name":"ScheduleWakeup","input":{"delaySeconds":3000,"prompt":"buddy wait check MARKER_PROMPT"}}]'
	fire_sched_meta 2026-09-26T08:50:00.000Z "buddy wait check MARKER_PROMPT"
	req 2026-09-26T08:50:03.000Z c2 8000 20
	fire_sched 2026-09-26T10:00:00.000Z "/loop buddy wait check"
	req 2026-09-26T10:00:03.000Z c3 10 6000
	fire 2026-09-26T11:10:00.000Z "MARKER_PROMPT something else"
	req 2026-09-26T11:10:03.000Z c4 10 1500
	fire_sched 2026-09-26T11:14:00.000Z "buddy wait check"
	req 2026-09-26T11:14:03.000Z c5 9000 5 ',"content":[{"type":"tool_use","id":"t3","name":"Bash","input":{"command":"true"}}]'
	# The same turn's next request: the fire woke c5, not this one.
	echo '{"type":"user","timestamp":"2026-09-26T11:14:05.000Z","message":{"content":[{"type":"tool_result","tool_use_id":"t3","content":"MARKER_RESULT"}]}}'
	req 2026-09-26T11:14:06.000Z c5b 9000 5
	# Superseded by the operator: a fire that woke nothing, and an operator wake.
	# The operator's record also carries turnOrigin "scheduled" (none measured
	# so): a record with an origin is the waker whatever else it is stamped.
	fire 2026-09-26T12:00:00.000Z "buddy wait check"
	echo '{"type":"user","timestamp":"2026-09-26T12:00:01.000Z","origin":{"kind":"human"},"turnOrigin":"scheduled","message":{"content":"MARKER_BODY I am back"}}'
	req 2026-09-26T12:00:05.000Z c6 9000 5
	# After the last request: a fire that woke nothing.
	fire 2026-09-26T14:00:00.000Z "buddy wait check"
} >"$P/sess-c.jsonl"
printf 'not json\n' >"$P/sess-g.jsonl"

out=$(BUDDY_WAKE_DAYS=36500 CLAUDE_PROJECTS_DIR="$WORK/projects" sh scripts/wake-report.sh -fixture 2>&1) ||
	fail "the report exited non-zero:
$out"

want() { # want <regex> <what>
	printf '%s\n' "$out" | grep -q "$1" || fail "$2:
$out"
}
want '^sessions: 4 transcript(s) modified in the window, 3 with a request in it$' \
	"want 4 transcripts (the subagent's not among them), 3 with a request"
want '^5-30m  *1  *1  *300$' "5-30m: one warm (10m), one cold (15m), and never the 4m gap"
want '^30-50m  *2  *0  *0$' "30-50m: the 35m and 46m warm wakes"
want '^50-60m  *2  *0  *0$' "50-60m: the 55m warm wake, and the one at exactly 50m"
want '^60-65m  *0  *1  *800$' "60-65m: the 62m cold wake"
want '^65m-2h  *0  *6  *15700$' "65m-2h: six cold, one of them at exactly 65m"
want '^2-4h  *0  *1  *3000$' "2-4h: the wake at exactly 2h"
want '^4h+  *0  *1  *4000$' "4h+: the background task"
want '^total  *5  *10  *23800$' "the total row"
want '^operator  *2  *64m  *5800$' "operator: the 66m and 62m wakes (not the 15m one, not the 46m warm one)"
want '^peer-session  *1  *90m  *2000$' "peer-session"
want '^subagent  *1  *120m  *3000$' "subagent (handback)"
want '^background-task  *1  *270m  *4000$' "background-task"
want '^scheduled:wait-check  *1  *70m  *6000$' "scheduled:wait-check"
want '^scheduled:other  *1  *70m  *1500$' "scheduled:other"
want '^tool  *1  *70m  *500$' "tool: only a tool result since the last request"
want '^other  *1  *65m  *700$' "other: a slash-command echo"
want '^declared a wait (buddy wait \.\.\.): *1 of 3 session(s)$' "declared: C alone"
want '^armed buddy wait check (loop/cron): *1 of 3 session(s)$' "armed: C alone"
want '^wait-check fires: 5, 3 woke a request, 2 came back warm$' "the wait-check fires"

# Privacy, with its control: the markers ARE in the fixture, in a body, a tool
# result, a command, an isMeta record and a scheduled prompt.
for m in MARKER_BODY MARKER_RESULT MARKER_CMD MARKER_META MARKER_PROMPT; do
	grep -q "$m" "$P/sess-a.jsonl" "$P/sess-b.jsonl" "$P/sess-c.jsonl" ||
		fail "the privacy control failed: the fixture lost $m"
done
if printf '%s\n' "$out" | grep -q MARKER_; then
	fail "transcript text reached the report:
$out"
fi

# BUDDY_WAKE_DAYS, over its own project so the fixed-date fixture above never
# moves with the calendar. W1: requests at -80h, -50h and -47h, so -50h is a
# 30h wake and -47h a 3h one. W2: modified 10 days ago, a 90m wake an hour ago.
W="$WORK/projects/-window"
mkdir -p "$W"
at() { jq -rn --argjson h "$1" 'now - $h * 3600 | floor | todate'; }
{
	req "$(at 80)" w1 0 100
	req "$(at 50)" w2 0 200
	req "$(at 47)" w3 0 300
} >"$W/w1.jsonl"
{
	req "$(at 2.5)" x1 0 100
	req "$(at 1)" x2 0 400
} >"$W/w2.jsonl"
touch -t "$(jq -rn 'now - 10 * 86400 | strftime("%Y%m%d%H%M")')" "$W/w2.jsonl"
win() { BUDDY_WAKE_DAYS=$1 CLAUDE_PROJECTS_DIR="$WORK/projects" sh scripts/wake-report.sh -window 2>&1; }
w7=$(win 7)
printf '%s\n' "$w7" | grep -q '^total  *0  *2  *500$' ||
	fail "7 days: want w1's two wakes and not the transcript modified 10 days ago:
$w7"
w2=$(win 2)
printf '%s\n' "$w2" | grep -q '^total  *0  *1  *300$' ||
	fail "2 days: want only the -47h wake, its gap still measured from -50h:
$w2"
printf '%s\n' "$w2" | grep -q '^2-4h  *0  *1  *300$' ||
	fail "2 days: the -47h wake's gap was not measured from the request before the window:
$w2"
w11=$(win 11)
printf '%s\n' "$w11" | grep -q '^total  *0  *3  *900$' ||
	fail "the control failed: an 11-day window did not take in the 10-day-old transcript:
$w11"

# The knob is named (#98): a cost audit ran BUDDY_COST_DAYS=10, got 7 days, and
# read it as the window ignored. The header names BUDDY_WAKE_DAYS, and its
# value moves with it (the 11-day run says 11: the header is not a constant).
printf '%s\n' "$w7" | grep -qx 'Wake report: -window, last 7 day(s) (BUDDY_WAKE_DAYS)' ||
	fail "the header does not name BUDDY_WAKE_DAYS:
$w7"
printf '%s\n' "$w11" | grep -qx 'Wake report: -window, last 11 day(s) (BUDDY_WAKE_DAYS)' ||
	fail "the header's window did not follow BUDDY_WAKE_DAYS=11:
$w11"
# BUDDY_COST_DAYS alone: ONE line says it is not this report's knob, and the
# window stays 7 (w1's two wakes, not the 10-day-old transcript an 11-day
# window takes in). The value is never echoed: one planted after a newline
# would be a second line.
costnote='^BUDDY_COST_DAYS is set and is not this report.s knob'
cw() { env -u BUDDY_WAKE_DAYS -u BUDDY_COST_DAYS "$@" CLAUDE_PROJECTS_DIR="$WORK/projects" sh scripts/wake-report.sh -window 2>&1; }
c=$(cw BUDDY_COST_DAYS=11)
[ "$(printf '%s\n' "$c" | grep -c "$costnote")" -eq 1 ] ||
	fail "BUDDY_COST_DAYS set, BUDDY_WAKE_DAYS unset: want one line saying the cost knob is not this report's:
$c"
printf '%s\n' "$c" | grep -q '^total  *0  *2  *500$' ||
	fail "BUDDY_COST_DAYS=11 moved the window: want the 7-day default's two wakes:
$c"
c=$(cw BUDDY_COST_DAYS="$(printf '11\nMARKER_COSTVAL')")
if printf '%s\n' "$c" | grep -q MARKER_COSTVAL; then
	fail "BUDDY_COST_DAYS's value reached the report:
$c"
fi
[ "$(printf '%s\n' "$c" | grep -c "$costnote")" -eq 1 ] ||
	fail "a BUDDY_COST_DAYS holding a newline: want the one note line still:
$c"
# Controls, so "no line" is the knob's doing and not a line never printed:
# BUDDY_WAKE_DAYS set beside it, or neither set, says nothing.
for knobs in "BUDDY_WAKE_DAYS=11 BUDDY_COST_DAYS=11" "BUDDY_WAKE_DAYS=7" ""; do
	# shellcheck disable=SC2086 # split on purpose: zero, one or two assignments
	c=$(cw $knobs)
	if printf '%s\n' "$c" | grep -q "$costnote"; then
		fail "with '$knobs' the cost-knob line printed:
$c"
	fi
	printf '%s\n' "$c" | grep -q '^Wake report: -window, last [0-9]* day(s) (BUDDY_WAKE_DAYS)$' ||
		fail "with '$knobs' the report did not run:
$c"
done
# USAGE names the knob beside wake-report.sh and says it is not the cost knob:
# the text from `scripts/wake-report.sh` to the next report it lists, joined.
# Control: that span was found at all (it carries the report's own summary).
u=$(awk '/^## Measuring context cost$/ {on=1; next} on && /^## / {exit} on' docs/USAGE.md |
	tr '\n' ' ' | sed -n 's/.*`sh scripts\/wake-report\.sh \(.*\)`sh scripts\/fleet-report\.sh.*/\1/p')
case "$u" in *'tokens re-written'*) ;; *) fail "the USAGE control failed: no wake-report.sh entry in \"Measuring context cost\": '$u'" ;; esac
case "$u" in *'`BUDDY_WAKE_DAYS`'*) ;; *) fail "USAGE does not name BUDDY_WAKE_DAYS beside wake-report.sh: '$u'" ;; esac
case "$u" in *'not `BUDDY_COST_DAYS`'*) ;; *) fail "USAGE does not say wake-report.sh's knob is not BUDDY_COST_DAYS: '$u'" ;; esac

# The default: the cwd repo's directory, spelled by hand — '/', '.', ' ' and
# 'é' are one '-' each.
repo="$WORK/re.po é"
mkrepo "$repo"
mkdir -p "$repo/sub"
enc="$(printf '%s' "$WORK" | LC_ALL=C sed 's/[^A-Za-z0-9]/-/g')-re-po--"
mkdir -p "$WORK/projects/$enc"
cp "$W/w1.jsonl" "$WORK/projects/$enc/"
def=$(cd "$repo/sub" && CLAUDE_PROJECTS_DIR="$WORK/projects" sh "$ROOT/scripts/wake-report.sh" 2>&1) ||
	fail "the default run exited non-zero:
$def"
printf '%s\n' "$def" | grep -q '^total  *0  *2  *500$' ||
	fail "the default did not find the cwd repo's transcripts:
$def"
# Its linked worktrees' directories join the default, from the main checkout
# and from inside a worktree alike: a lane's 2h cold wake of 4000. A decoy
# sharing the encoded prefix but not `--claude-worktrees-` is never read (its
# 9h wake would land in 4h+), and a named directory is still read alone.
git -C "$repo" commit -q --allow-empty -m init
git -C "$repo" worktree add -q --detach "$repo/.claude/worktrees/lane"
mkdir -p "$WORK/projects/$enc--claude-worktrees-lane" "$WORK/projects/$enc-other--claude-worktrees-z"
# A FILE with the worktree prefix, sorting first: never a directory read, and
# never a reason to drop the real one after it.
: >"$WORK/projects/$enc--claude-worktrees-a"
{
	req "$(at 3)" l1 0 100
	req "$(at 1)" l2 0 4000
} >"$WORK/projects/$enc--claude-worktrees-lane/lane.jsonl"
{
	req "$(at 10)" z1 0 100
	req "$(at 1)" z2 0 77777
} >"$WORK/projects/$enc-other--claude-worktrees-z/z.jsonl"
for from in "$repo/sub" "$repo/.claude/worktrees/lane"; do
	both=$(cd "$from" && CLAUDE_PROJECTS_DIR="$WORK/projects" sh "$ROOT/scripts/wake-report.sh" 2>&1) ||
		fail "the default run from $from exited non-zero:
$both"
	printf '%s\n' "$both" | grep -q '^total  *0  *3  *4500$' ||
		fail "from $from: want the main checkout's two wakes and the lane's one, never the decoy's:
$both"
	printf '%s\n' "$both" | grep -q '^plus 1 linked worktree dir(s): ' ||
		fail "from $from: the worktree directory read was not said:
$both"
done
# A SIBLING worktree (`<repo>-wt-<label>`, where this repo's lanes live) and one
# of this repository parked under ANOTHER repository's .claude/worktrees: from
# either, and from the main checkout, the report reads the main checkout's
# directory and every worktree's (D-169; the first cut read the worktree's
# own alone, an empty table that looked like "no wakes"), and never the other
# repository's (whose 9h wake of 77777 would land in 4h+). The sibling's 3h
# wake is 6000, lane2's two are w1's 500: 0 cold-warm, 6 wakes, 11000 in all.
arch="$WORK/arch"
mkrepo "$arch"
git -C "$repo" worktree add -q --detach "$arch/.claude/worktrees/lane2"
git -C "$repo" worktree add -q --detach "$repo-wt-a"
wenc=$(printf '%s' "$WORK" | LC_ALL=C sed 's/[^A-Za-z0-9]/-/g')
mkdir -p "$WORK/projects/$wenc-arch--claude-worktrees-lane2" "$WORK/projects/$wenc-arch" "$WORK/projects/$enc-wt-a"
cp "$W/w1.jsonl" "$WORK/projects/$wenc-arch--claude-worktrees-lane2/"
cp "$WORK/projects/$enc-other--claude-worktrees-z/z.jsonl" "$WORK/projects/$wenc-arch/"
{
	req "$(at 4)" s1 0 100
	req "$(at 1)" s2 0 6000
} >"$WORK/projects/$enc-wt-a/sib.jsonl"
for from in "$repo-wt-a" "$arch/.claude/worktrees/lane2" "$repo/sub"; do
	all=$(cd "$from" && CLAUDE_PROJECTS_DIR="$WORK/projects" sh "$ROOT/scripts/wake-report.sh" 2>&1) ||
		fail "the default run from $from exited non-zero:
$all"
	printf '%s\n' "$all" | grep -q '^total  *0  *6  *11000$' ||
		fail "from $from: want the main checkout's, the lanes' and the sibling's wakes, never the other repository's:
$all"
	printf '%s\n' "$all" | grep -q "^Wake report: $enc, " ||
		fail "from $from: the report did not name the main checkout:
$all"
	printf '%s\n' "$all" | grep -q '^plus 3 linked worktree dir(s): ' ||
		fail "from $from: want 3 worktree directories said:
$all"
done
# A lane under a symlinked parent (`al` -> `real2/al`) of a repository at
# real2/r3, beside ANOTHER repository's transcripts at `r3`: the prefix pair
# the two spellings leave maps real2/r3 onto r3, and only a mapping naming the
# same directory is kept (Codex code pass). The lane's own two wakes, never
# r3's 77777.
mkdir -p "$WORK/real2/al"
ln -s "$WORK/real2/al" "$WORK/al"
mkrepo "$WORK/real2/r3"
git -C "$WORK/real2/r3" commit -q --allow-empty -m init
git -C "$WORK/real2/r3" worktree add -q --detach "$WORK/real2/al/lane"
mkrepo "$WORK/r3"
mkdir -p "$WORK/projects/$wenc-al-lane" "$WORK/projects/$wenc-r3"
cp "$W/w1.jsonl" "$WORK/projects/$wenc-al-lane/"
cp "$WORK/projects/$enc-other--claude-worktrees-z/z.jsonl" "$WORK/projects/$wenc-r3/"
fromal=$(cd "$WORK/al/lane" && CLAUDE_PROJECTS_DIR="$WORK/projects" sh "$ROOT/scripts/wake-report.sh" 2>&1) ||
	fail "the run from a lane under a symlinked parent exited non-zero:
$fromal"
printf '%s\n' "$fromal" | grep -q '^total  *0  *2  *500$' ||
	fail "a lane under a symlinked parent took in another repository's transcripts:
$fromal"
alone=$(CLAUDE_PROJECTS_DIR="$WORK/projects" sh scripts/wake-report.sh "$enc" 2>&1)
printf '%s\n' "$alone" | grep -q '^total  *0  *2  *500$' ||
	fail "a named project directory took in its worktrees' directories:
$alone"
# And by path, the other spelling of the argument.
bypath=$(BUDDY_WAKE_DAYS=36500 sh scripts/wake-report.sh "$P" 2>&1)
printf '%s\n' "$bypath" | grep -q '^total  *5  *10  *23800$' || fail "a path argument did not read the fixture:
$bypath"

# Refusals, each with its reason.
for d in 0 x -1 07x; do
	if BUDDY_WAKE_DAYS=$d CLAUDE_PROJECTS_DIR="$WORK/projects" sh scripts/wake-report.sh -fixture >/dev/null 2>&1; then
		fail "BUDDY_WAKE_DAYS='$d' was accepted"
	fi
done
# A named directory holding no transcript at all is refused, never reported as
# a zero: one that does not exist, an empty one (by name and by path), and a
# repo checkout's path, which says it is one. stdout stays empty, so nothing
# reading it takes a header for a report.
mkdir -p "$WORK/projects/-empty/a.jsonl"     # a directory, no transcript (Codex)
: >"$WORK/projects/-empty/notes.txt"
for arg in -nowhere -empty "$WORK/projects/-empty" "$repo"; do
	rc=0
	CLAUDE_PROJECTS_DIR="$WORK/projects" sh scripts/wake-report.sh "$arg" >"$WORK/o" 2>"$WORK/e" || rc=$?
	[ "$rc" -eq 2 ] || fail "a named directory with no transcript ($arg) exited $rc, not 2:
$(cat "$WORK/o" "$WORK/e")"
	[ ! -s "$WORK/o" ] || fail "the refusal of $arg printed on stdout:
$(cat "$WORK/o")"
	grep -qF -- "$arg" "$WORK/e" || fail "the refusal did not name $arg:
$(cat "$WORK/e")"
	grep -q 'under .*, by name .* or by path' "$WORK/e" || fail "the refusal of $arg did not say what the argument is:
$(cat "$WORK/e")"
	grep -q 'With no argument, run from inside the repo' "$WORK/e" || fail "the refusal of $arg did not name the default:
$(cat "$WORK/e")"
done
grep -q 'It is a git checkout' "$WORK/e" || fail "a repo checkout's path was not said to be one:
$(cat "$WORK/e")"
# The control for that note: a directory that is no checkout is not called one.
CLAUDE_PROJECTS_DIR="$WORK/projects" sh scripts/wake-report.sh -empty >/dev/null 2>"$WORK/e" || true
if grep -q 'git checkout' "$WORK/e"; then fail "an empty project directory was called a git checkout"; fi
# The positive control: transcripts all older than the window are no refusal,
# and print the honest zero (w2 alone, modified 10 days ago, in a 7-day window).
# A directory named a.jsonl sorts first there: no reason to stop looking.
mkdir -p "$WORK/projects/-old/a.jsonl"
cp -p "$W/w2.jsonl" "$WORK/projects/-old/"
old=$(BUDDY_WAKE_DAYS=7 CLAUDE_PROJECTS_DIR="$WORK/projects" sh scripts/wake-report.sh -old 2>&1) ||
	fail "a directory whose transcripts are all older than the window was refused:
$old"
printf '%s\n' "$old" | grep -q '^sessions: 0 transcript(s) modified in the window, 0 with a request in it$' ||
	fail "the out-of-window directory did not print the zero report:
$old"
printf '%s\n' "$old" | grep -q '^total  *0  *0  *0$' || fail "the out-of-window directory's total is not zero:
$old"
# And its control: the same directory, a window that reaches the file, reads it.
BUDDY_WAKE_DAYS=11 CLAUDE_PROJECTS_DIR="$WORK/projects" sh scripts/wake-report.sh -old 2>&1 |
	grep -q '^total  *0  *1  *400$' || fail "the control failed: an 11-day window did not read -old's transcript"
# The default with no transcript directory for the repo still says so, exit 0:
# a repo whose sessions have none yet is a true zero, and the line names it.
mkrepo "$WORK/fresh"
none=$(cd "$WORK/fresh" && CLAUDE_PROJECTS_DIR="$WORK/projects" sh "$ROOT/scripts/wake-report.sh" 2>&1) ||
	fail "a repo with no transcript directory is not an error, and exited non-zero:
$none"
printf '%s\n' "$none" | grep -q '^no transcripts: .* does not exist$' || fail "a missing default directory was not said:
$none"

echo "check-wake-report: ok"
