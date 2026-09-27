#!/bin/sh
# wake-report.sh — whether this repo's sessions come back WARM after an idle
# gap, and what woke the ones that came back cold. Counts, token totals and
# timestamps only: no prompt, message, command or tool output is ever printed.
#
#   sh scripts/wake-report.sh [project-dir]
#
# project-dir is a directory under ~/.claude/projects by name
# (`-Users-me-src-repo`) or by path. Default: the one the harness keeps for
# the repo the cwd is in.
#
# WHY IT EXISTS (#45). "Are parked sessions coming back warm?" had no
# instrument. D-033 took its five measurements of the cache edge by hand, and
# #44's case for teaching the timer wait (`buddy wait --until`, armed with
# `/loop buddy wait check`) rested on a hand-rolled script over raw
# transcripts: 149 wakes past 60 minutes idle in one week, 145 of them cold,
# 54.6M tokens re-written; 3 of 129 sessions declared a wait and 1 armed the
# loop. Nothing let the operator re-run that after the skill changed. This is
# that script, re-runnable, so the before and after are the same instrument.
#
# WHAT A ROW IS. A REQUEST is an assistant record carrying .message.usage,
# deduplicated by .requestId (else .message.id): the harness writes one record
# per content block, each carrying the same usage, so counting records counts
# one request three times. Its time is its FIRST record's .timestamp. A
# `<synthetic>` record (an API error or an interrupt, written by the harness)
# carries zeroed usage and is not a request: counted, it would be a warm wake
# that never read anything. Main thread only — sidechain records and the
# subagents/ directory are left out, since a subagent's requests are its own
# cache and its idle is the parent's busy. The GAP is a request's time minus
# the previous request's in the same transcript; every request after a gap of
# 5 minutes or more is a WAKE. COLD means cache_creation_input_tokens >
# cache_read_input_tokens — the request wrote more than it read — which is
# D-033's definition and a count, not a diagnosis. "Re-written" sums
# cache_creation_input_tokens over the cold wakes.
#
# WHAT WOKE IT is the latest record since the previous request that is not a
# tool result, read from the harness's own `origin` stamp, never from text:
#   operator         origin.kind "human"
#   peer-session     origin.kind "peer" from a "uds:" socket (SendMessage)
#   subagent         origin.kind "peer" with handback set
#   background-task  origin.kind "task-notification"
#   scheduled:wait-check / scheduled:other
#                    a `scheduled_task_fire` system record, split on whether
#                    its prompt names `buddy wait check`
#   tool             only tool results since the previous request: a tool
#                    that ran long, or a permission prompt that sat unanswered
#   other            anything else: a slash-command echo, an interrupt, an
#                    origin kind this script has not met
# An originless isMeta user record is harness context riding with something
# else (the prompt a scheduled fire injects right after the fire, a skill body,
# a hook's output) and never the waker: taken as one, it hid every scheduled
# fire in the first cut behind "other".
#
# KEEP-ALIVE LINES. A session DECLARED a wait if its main thread ran a Bash
# command with `buddy wait` in COMMAND position (line start, after `;` `&` `|`
# `(`, behind `VAR=x` or a directory) followed by a flag other than help, or
# by nothing (bare `buddy wait` is a 3h timer); `check`, `ls` and `clear` are
# not declarations. Command position because the first cut matched the words
# anywhere and, in this repo's own transcripts, counted 10 of 25 sessions as
# declaring: commit messages, grep patterns and heredocs that NAME the verb.
# The tightened match left 4: three real declarations and one command that
# was testing this very regex. It ARMED the check if a
# ScheduleWakeup or CronCreate prompt names `buddy wait check`. A wait-check
# FIRE is a scheduled_task_fire whose prompt names it; "warm" there is the
# request that fire woke, at any gap, since a healthy loop fires well inside
# the 5-minute floor the wake tables use.
#
# WHAT IT MISSES, on purpose. A request's time is when its first record was
# written, so a gap after a long-streaming request is overstated by that
# request's own duration. The gap is measured within one transcript: a
# `--resume` that starts a new file, or a `/clear` (a new session id), opens
# with no previous request and is not a wake. Only the one project directory
# is read: a session started in a linked worktree is filed under that
# worktree's own directory, so run the report once per directory.
#
# KNOBS
#   BUDDY_WAKE_DAYS      look back this many days (default 7): transcripts
#                        MODIFIED in the window, then requests and tool calls
#                        TIMED in it (a wake's previous request may be older)
#   CLAUDE_PROJECTS_DIR  where transcripts live (default ~/.claude/projects;
#                        cost-report's knob)
set -eu

days=${BUDDY_WAKE_DAYS:-7}
case "$days" in
	'' | *[!0-9]*) echo "BUDDY_WAKE_DAYS must be a positive integer" >&2; exit 2 ;;
	0) echo "BUDDY_WAKE_DAYS must be greater than zero" >&2; exit 2 ;;
esac
command -v jq >/dev/null 2>&1 || { echo "wake-report: missing required command: jq" >&2; exit 2; }
[ $# -le 1 ] || { echo "usage: wake-report.sh [project-dir]" >&2; exit 2; }

projects=${CLAUDE_PROJECTS_DIR:-"$HOME/.claude/projects"}
# The harness names a project's directory from its absolute path with every
# UTF-16 code unit outside [A-Za-z0-9] turned into '-' (startup-report.sh has
# the measurement; a character outside the BMP is two units, so two hyphens).
# Not just '/' and '.': a repo under "My Drive" or "café" would be missed.
encode() { jq -rn --arg p "$1" '$p | gsub("[\\x{10000}-\\x{10FFFF}]"; "--") | gsub("[^A-Za-z0-9]"; "-")'; }
if [ $# -eq 1 ]; then
	case "$1" in
		*/*) proj=$1 ;;
		*) proj="$projects/$1" ;;
	esac
else
	# The root as the operator spells it (pwd -L), which is what the harness
	# names its directory from; --show-toplevel resolves symlinks.
	cdup=$(git rev-parse --show-cdup 2>/dev/null) || {
		echo "wake-report: not in a git repository; name a project directory" >&2
		exit 2
	}
	top=$(CDPATH= cd -- "./${cdup:-.}" && pwd -L)
	proj="$projects/$(encode "$top")"
fi

echo "Wake report: $(basename -- "$proj"), last $days day(s)"
echo "Every request after 5+ min idle in the same session (main thread). Counts, tokens and times only."
if [ ! -d "$proj" ]; then
	echo "no transcripts: $proj does not exist"
	exit 0
fi

# One pass per transcript, streaming (never slurping: a long session runs to
# tens of megabytes), line by line with fromjson? so one torn line from a
# crashed session costs that line, not the session. It prints only numbers and
# the fixed labels above — never a value read from the transcript — so the
# privacy line holds by construction, and check-wake-report.sh plants markers
# to prove it anyway. Rows:
#   W <gap-seconds> <cold 0|1> <cache-creation> <trigger>   one per wake
#   F                                                        one per wait-check fire
#   K <warm 0|1>                                             one per request a fire woke
#   S <requests-in-window> <declared 0|1> <armed 0|1>        one per transcript
# shellcheck disable=SC2016
prog='
def epoch: (sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601)
  + ((capture("(?<f>\\.[0-9]+)Z$") | "0" + .f | tonumber) // 0);
def waitcheck: tostring | contains("buddy wait check");
def results: (.message.content | type) == "array" and (.message.content | length) > 0
  and all(.message.content[]; type == "object" and .type == "tool_result");
def waker: (.origin // {}) as $o
  | if $o.kind == "human" then "operator"
    elif $o.kind == "peer" then
      (if ($o.from // "" | tostring | startswith("uds:")) then "peer-session"
       elif $o.handback then "subagent" else "other" end)
    elif $o.kind == "task-notification" then "background-task"
    else "other" end;
def declares: [match("(?:^|[\\n;&|(])[ \\t]*(?:[A-Za-z_][A-Za-z0-9_]*=[^ \\t\\n]* +)*(?:[^ \\t\\n;&|()\"'"'"'`]*/)?buddy[ \\t]+wait(?=$|[ \\t\\n;&|)])[ \\t]*([^ \\t\\n;&|)]*)"; "g") | .captures[0].string]
  | any(. == "" or (startswith("-") and (IN("-h", "-help", "--help") | not)));
reduce (inputs | fromjson? | select(type == "object" and .isSidechain != true and (.timestamp | type) == "string")) as $e (
  {seen: {}, prev: null, trig: null, tool: false, fire: false, out: [], n: 0, decl: 0, armed: 0};
  ($e.timestamp | epoch) as $t
  | if $e.type == "system" and $e.subtype == "scheduled_task_fire" then
      if ($e.prompt // "" | waitcheck) then
        .trig = "scheduled:wait-check" | .fire = ($t >= $cut)
        | (if .fire then .out += ["F"] else . end)
      else .trig = "scheduled:other" | .fire = false end
    elif $e.type == "user" then
      if $e | results then .tool = true
      elif $e.origin == null and $e.isMeta == true then .
      else .trig = ($e | waker) | .fire = false end
    elif $e.type == "assistant" then
      (if $t >= $cut then
        reduce (($e.message.content // []) | if type == "array" then .[] else empty end
                | select(type == "object" and .type == "tool_use")) as $u (.;
          if $u.name == "Bash" and ($u.input.command // "" | tostring | declares) then .decl = 1
          elif ($u.name == "ScheduleWakeup" or $u.name == "CronCreate") and ($u.input.prompt // "" | waitcheck) then .armed = 1
          else . end)
      else . end)
      | ($e.message.usage // null) as $use
      | ($e.requestId // $e.message.id // null) as $id
      | if $use == null or $id == null or $e.message.model == "<synthetic>" or .seen[$id] then .
        else
          (($use.cache_read_input_tokens // 0)) as $cr
          | (($use.cache_creation_input_tokens // 0)) as $cw
          | (if $cw > $cr then 1 else 0 end) as $cold
          | (.trig // (if .tool then "tool" else "other" end)) as $why
          | if $t >= $cut then
              .n += 1
              | (if .prev != null and $t - .prev >= 300 then
                  .out += ["W\t\(($t - .prev) | floor)\t\($cold)\t\($cw)\t\($why)"] else . end)
              | (if .fire then .out += ["K\t\(1 - $cold)"] else . end)
            else . end
          | .seen[$id] = true | .prev = $t | .trig = null | .tool = false | .fire = false
        end
    else . end)
| (.out[]),
  "S\t\(.n)\t\(.decl)\t\(.armed)"'

# A fire is counted when it fires, not when it wakes something: the first cut
# counted it at the request it woke, and so a fire the operator's typing beat to
# the prompt, or one that landed after the session's last request, vanished
# from the count instead of reading as a fire that woke nothing.
cut=$(jq -n --argjson d "$days" 'now - $d * 86400 | floor')
rows=$(mktemp) || exit 1
trap 'rm -f "$rows"' EXIT INT TERM
# -mtime is whole days, the grain BUDDY_WAKE_DAYS is in; -maxdepth 1 keeps
# <session>/subagents/*.jsonl out.
find "$proj" -maxdepth 1 -name '*.jsonl' -mtime "-$days" | while IFS= read -r f; do
	jq -n -R -r --argjson cut "$cut" "$prog" "$f" 2>/dev/null || true
done >"$rows"

LC_ALL=C awk -F'\t' '
BEGIN {
	nb = split("5-30m 30-50m 50-60m 60-65m 65m-2h 2-4h 4h+", bucket, " ")
	split("1800 3000 3600 3900 7200 14400", edge, " ")
	nt = split("operator peer-session subagent background-task scheduled:wait-check scheduled:other tool other", trig, " ")
}
$1 == "S" { files++; if ($2 > 0) { active++; decl += $3; armed += $4 } }
$1 == "F" { fires++ }
$1 == "K" { woke++; fwarm += $2 }
$1 == "W" {
	for (b = 1; b < nb && $2 >= edge[b]; b++) ;
	if ($3) { cold[b]++; rw[b] += $4 } else warm[b]++
	if ($3 && $2 >= 3600) { tc[$5]++; tidle[$5] += $2; trw[$5] += $4 }
}
END {
	printf "sessions: %d transcript(s) modified in the window, %d with a request in it\n\n", files, active
	printf "%-8s %6s %6s %14s\n", "idle", "warm", "cold", "re-written"
	for (b = 1; b <= nb; b++) {
		printf "%-8s %6d %6d %14.0f\n", bucket[b], warm[b], cold[b], rw[b]
		W += warm[b]; C += cold[b]; R += rw[b]
	}
	printf "%-8s %6d %6d %14.0f\n", "total", W, C, R
	printf "\ncold wakes past 60 min, by what woke them\n"
	printf "%-21s %6s %10s %14s\n", "woken by", "cold", "mean idle", "re-written"
	for (i = 1; i <= nt; i++) {
		k = trig[i]
		printf "%-21s %6d %10s %14.0f\n", k, tc[k], tc[k] ? sprintf("%.0fm", tidle[k] / tc[k] / 60) : "-", trw[k]
	}
	printf "\nkeep-alive\n"
	printf "declared a wait (buddy wait ...):     %d of %d session(s)\n", decl, active
	printf "armed buddy wait check (loop/cron):   %d of %d session(s)\n", armed, active
	printf "wait-check fires: %d, %d woke a request, %d came back warm\n", fires, woke, fwarm
}' "$rows"
