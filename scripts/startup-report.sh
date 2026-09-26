#!/bin/sh
# startup-report.sh — how long this repo's sessions spend finding their way
# around before their first edit, and how. Counts and byte lengths only: no
# prompt, command, path or tool output is ever printed.
#
#   sh scripts/startup-report.sh [repo-dir]      # default: this checkout
#
# WHY IT EXISTS (D-051). Its first run here, 30 days to 2026-09-26: 14
# sessions reached an edit, a median 22 minutes and 29 tool calls (89 KB
# returned) before it, 0 of 518 calls LSP — mapping by grep and `sed -n`
# through Bash, 277 of those commands naming internal/cli/cli.go (counted by
# hand). The fix was a language server (`buddy init` now says whether one is
# set up). Codex's first caveat on that fix was "measure whether it helped":
# installing a tool is not sessions using it. This is the measurement,
# re-runnable, so the before and after are the same instrument — and its `lsp`
# line says whether sessions picked the tool up at all.
#
# WHAT A ROW IS. One transcript (a session's main thread; subagent sidechains
# are left out), from its first TYPED prompt to its first Edit, Write,
# MultiEdit or NotebookEdit tool call on a file INSIDE the repo — a note written
# to a scratchpad is not the session starting work, and the first draft of this
# report, counting it, put this very session's "first edit" at 1.8 minutes, on
# a Codex prompt file. "Inside" is either spelling of the root — the path the
# harness was started in (its transcripts' directory is named from it) and the
# resolved one (a symlinked parent; /var is /private/var on macOS) — and never
# a path through a `..` segment. `min` is wall-clock between the two, so
# it is an UPPER bound: it includes the operator's own turns and any time the
# session sat waiting. The tool columns count calls made in that window, and
# `KB` is the bytes those calls returned. A session that never edited has no
# window and is counted, not listed.
#
# WHAT IT MISSES, on purpose. A typed prompt that itself begins with `<` is
# taken for one the harness generated (`<command-name>`, `<local-command-…>`,
# `<system-reminder>`, `<task-notification>`, … fourteen in the 2.1.283
# binary) and skipped, so such a session's window starts at its next prompt:
# listing every tag the harness generates would fail the other way, silently,
# on the next tag it adds. The one exception is `<pasted_content`, which is
# how the harness wraps text the operator PASTED — a typed prompt. A compacted session's summary entry
# (isCompactSummary) is never the typed prompt. "Last N days" is transcripts
# MODIFIED in that window, so a session resumed today reports its original
# startup. A repo path over 200 characters is not found: the harness shortens
# and hashes those directory names.
#
# KNOBS
#   BUDDY_COST_DAYS      look back this many days (default 7; cost-report's knob)
#   CLAUDE_PROJECTS_DIR  where transcripts live (default ~/.claude/projects;
#                        cost-report's knob). The repo's own directory in it is
#                        its absolute path with every character outside
#                        [A-Za-z0-9] turned into '-'. A linked worktree is a
#                        different directory, so name it as the argument.
set -eu

days=${BUDDY_COST_DAYS:-7}
case "$days" in
	'' | *[!0-9]*) echo "BUDDY_COST_DAYS must be a positive integer" >&2; exit 2 ;;
	0) echo "BUDDY_COST_DAYS must be greater than zero" >&2; exit 2 ;;
esac
command -v jq >/dev/null 2>&1 || { echo "startup-report: missing required command: jq" >&2; exit 2; }

dir=${1:-$PWD}
[ $# -le 1 ] || { echo "usage: startup-report.sh [repo-dir]" >&2; exit 2; }
# The root as the operator spells it (pwd -L), which is what the harness names
# its directory from, and resolved (pwd -P). --show-toplevel alone resolves
# symlinks, and a repo under a symlinked parent would then find no transcripts.
cdup=$(git -C "$dir" rev-parse --show-cdup 2>/dev/null) || cdup=
top=$(CDPATH= cd -- "$dir/${cdup:-.}" && pwd -L)
real=$(CDPATH= cd -- "$top" && pwd -P)
projects=${CLAUDE_PROJECTS_DIR:-"$HOME/.claude/projects"}
# The harness replaces every UTF-16 code unit outside [A-Za-z0-9] with '-'
# (`replace(/[^a-zA-Z0-9]/g, "-")`, read in its binary, D-051). jq, not
# `LC_ALL=C sed`: sed in the C locale works on bytes, and turns the two bytes
# of an é into two hyphens where the harness writes one (Codex). jq works on
# code points, so a character outside the BMP (two UTF-16 units: an emoji) is
# given its two hyphens first.
proj="$projects/$(jq -rn --arg p "$top" '$p | gsub("[\\x{10000}-\\x{10FFFF}]"; "--") | gsub("[^A-Za-z0-9]"; "-")')"

echo "Startup report: $top, last $days day(s)"
echo "From each session's first typed prompt to its first edit. Counts and bytes only."
if [ ! -d "$proj" ]; then
	echo "no transcripts: $proj does not exist"
	exit 0
fi

# One TSV row per transcript: start-epoch, start as text (BSD awk has no
# strftime), minutes (or "-" with no edit),
# then read, search, bash, lsp, agent, other, bytes. The jq program streams
# (never slurps): a long session's transcript runs to tens of megabytes.
# shellcheck disable=SC2016
prog='
def epoch: sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601;
def human: (startswith("<") | not) or startswith("<pasted_content");
def typed($c):
  if ($c | type) == "string" then ($c | human)
  elif ($c | type) == "array" then
    ([$c[] | select(.type == "tool_result")] | length) == 0
    and ([$c[] | select(.type == "text") | .text | human] | any)
  else false end;
def kind: if . == "Read" then "read"
  elif . == "Grep" or . == "Glob" then "search"
  elif . == "Bash" then "bash"
  elif . == "LSP" then "lsp"
  elif . == "Agent" or . == "Task" then "agent"
  else "other" end;
def size: if type == "string" then utf8bytelength else (tojson | utf8bytelength) end;
def inrepo: (startswith($top + "/") or startswith($real + "/")) and (test("/\\.\\.?(/|$)") | not);
reduce (inputs | select(.isSidechain != true and .isMeta != true and .isCompactSummary != true)) as $e (
  {t0: null, t1: null, n: {read: 0, search: 0, bash: 0, lsp: 0, agent: 0, other: 0}, bytes: 0};
  ($e.message.content // null) as $c
  | if .t1 != null then .
    elif .t0 == null then
      (if $e.type == "user" and typed($c) then .t0 = $e.timestamp else . end)
    elif $e.type == "assistant" and ($c | type) == "array" then
      reduce ($c[] | select(.type == "tool_use")) as $u (.;
        if .t1 != null then .
        elif ($u.name | IN("Edit", "Write", "MultiEdit", "NotebookEdit"))
          and (($u.input.file_path // $u.input.notebook_path // "") | inrepo)
        then .t1 = $e.timestamp
        else .n[$u.name | kind] += 1 end)
    elif $e.type == "user" and ($c | type) == "array" then
      .bytes += ([$c[] | select(.type == "tool_result") | .content | size] | add // 0)
    else . end)
| select(.t0 != null)
| [(.t0 | epoch), (.t0 | epoch | strftime("%Y-%m-%d %H:%M")),
   (if .t1 then (((.t1 | epoch) - (.t0 | epoch)) / 60 * 10 | round / 10) else "-" end),
   .n.read, .n.search, .n.bash, .n.lsp, .n.agent, .n.other, .bytes]
| @tsv'

rows=$(mktemp) || exit 1
trap 'rm -f "$rows"' EXIT INT TERM
# -mtime is whole days, which is the grain BUDDY_COST_DAYS is in.
find "$proj" -maxdepth 1 -name '*.jsonl' -mtime "-$days" | while IFS= read -r f; do
	# A transcript jq cannot parse is skipped, not fatal: one bad line from a
	# crashed session must not blank the whole report.
	jq -n -r --arg top "$top" --arg real "$real" "$prog" "$f" 2>/dev/null || true
done | sort -n >"$rows"

LC_ALL=C awk -F'\t' '
function median(a, n,   i, j, t) {
	for (i = 2; i <= n; i++) { t = a[i]; for (j = i - 1; j >= 1 && a[j] > t; j--) a[j + 1] = a[j]; a[j + 1] = t }
	return n % 2 ? a[(n + 1) / 2] : (a[n / 2] + a[n / 2 + 1]) / 2
}
BEGIN { printf "%-16s %7s %5s %6s %5s %4s %5s %5s %7s\n", "started (UTC)", "min", "read", "search", "bash", "lsp", "agent", "other", "KB" }
$3 == "-" { noedit++; next }
{
	n++
	calls = $4 + $5 + $6 + $7 + $8 + $9
	m[n] = $3; c[n] = calls; k[n] = $10 / 1024; lsp += $7; all += calls
	printf "%-16s %7.1f %5d %6d %5d %4d %5d %5d %7d\n", $2, $3, $4, $5, $6, $7, $8, $9, $10 / 1024
}
END {
	if (n == 0) { printf "no session reached an edit (%d without one)\n", noedit; exit }
	printf "\n%d session(s) reached an edit, %d did not.\n", n, noedit
	printf "median: %.1f min, %.0f tool calls, %.0f KB returned before the first edit\n", median(m, n), median(c, n), median(k, n)
	printf "lsp: %d of %d calls in the window (%.0f%%)\n", lsp, all, all ? 100 * lsp / all : 0
}' "$rows"
