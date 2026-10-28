#!/bin/sh
# check-subagent-report.sh — done-check for scripts/subagent-report.sh
# (docs/CHEAPER-MODELS-PLAN.md step 4.3).
#
# HERMETIC: sh and python3, which the hermetic tier already requires. The
# transcripts are a fixture in a throwaway CLAUDE_PROJECTS_DIR; nothing under
# ~/.claude is read.
#
# What must hold, each with the control that proves the assertion could fire:
#   - an Agent call is counted once, by its tool-use id: a copy in an
#     `api-request-blob` record and a second assistant record carrying the
#     same id would each make the Fable row 2; a sidechain call, a
#     `<synthetic>` one, one timed before the window and one stamped after
#     now (no --until given) are not counted; a copy in a later file whose
#     result is only there still joins that result;
#   - the model param is printed as written, `(none)` split by subagent type,
#     so an Explore, a fork and an omitted type each keep their own row and
#     only general-purpose (named or omitted) reaches the unnamed-GP count;
#   - the word: a description matching the regex is `review`, one that does
#     not is `other`, and a call with NO description is `unclassified` and
#     listed by session and tool-use id, although its prompt says "review";
#   - the model a call RAN on is its subagent transcript's, joined by the
#     agent id its result names: a Fable call from an Opus parent reads Fable
#     (the parent's model there would be a misattribution), and `(no agent
#     id)` covers a refused call, an id that fails the charset (a `../` path),
#     and two results on one record; `(no transcript)` and `(no result)` too;
#   - a subagent's own Agent call is counted apart and in no table;
#   - subagent transcripts by session count every `agent-*.jsonl` whose first
#     non-synthetic request is in the window, joined or not;
#   - an orchestrator is a session that ran `herdr agent start` in command
#     position (a commit message naming it, or a single-quoted `; herdr agent
#     start`, is no launch); its prompt at the
#     first launch is that request's input + cache tokens; its landing reads
#     are Read or Bash results over 10,240 bytes naming ANOTHER lane's
#     worktree or branch (not its own worktree, not a small result, not a
#     name that only starts with a lane's name, not a path naming no lane),
#     a worktree and its branch counting as ONE lane, a `feat/c` branch too;
#   - Bash bytes are UTF-8 bytes; a buddy verb counts in command position
#     only, a newline included, never inside single quotes; a word that is
#     not a dispatched verb prints
#     `(other)`; the report's verb list is the dispatch table's;
#   - a torn line, a JSON line that is not an object and a line that is not
#     UTF-8 are counted as skipped and cost nothing else;
#   - a transcript modified before --since is not read (control: an earlier
#     --since reads it);
#   - NO TRANSCRIPT TEXT: canaries are planted in a description, a prompt, a
#     command, a tool result, a path, a model param, a subagent type, an agent
#     id, a branch, a file name that passes an id charset but is no UUID, a
#     tool-use id that is no `toolu_...`, a verb, a torn line and a non-UTF-8 line; the
#     control greps the fixture for each first, then neither stdout nor
#     stderr may hold any;
#   - a bad --since, an unknown argument and an empty window are refused,
#     exit 2, `refused:` last, nothing on stdout; --help exits 0.
set -eu
. "$(dirname -- "$0")/lib.sh"
ROOT=$(repo_root "$0")
CDPATH= cd -- "$ROOT"

fail() { echo "check-subagent-report: FAIL — $*" >&2; exit 1; }

WORK=$(mktemp -d) || fail "mktemp failed"
trap 'rm -rf "$WORK"' EXIT INT TERM
PR="$WORK/projects"
A=aaaaaaaa-0000-4000-8000-000000000001
B=bbbbbbbb-0000-4000-8000-000000000002
C=cccccccc-0000-4000-8000-000000000003
P="$PR/-repo"
SA="$P/$A/subagents"
mkdir -p "$SA" "$PR/-repo--claude-worktrees-lane-a" "$PR/-repo--claude-worktrees-wt-b" \
	"$PR/-repo--claude-worktrees-orc-wt" "$PR/-repo--claude-worktrees-wt-c" "$P/CANARY_SID/subagents" \
	"$P/$B/subagents"

# One helper per record shape. CWD and BR are the session's.
# use <time> <parent-model> <tool-use-id> <tool> <input-json> [usage-json]
use() { printf '{"type":"assistant","timestamp":"2026-10-26T%sZ","cwd":"%s","gitBranch":"%s","message":{"model":"%s","content":[{"type":"tool_use","id":"%s","name":"%s","input":%s}],"usage":%s}}\n' "$1" "$CWD" "$BR" "$2" "$3" "$4" "$5" "${6:-null}"; }
# res <time> <tool-use-id> <content-json> [toolUseResult-json]
res() { printf '{"type":"user","timestamp":"2026-10-26T%sZ","cwd":"%s","message":{"content":[{"type":"tool_result","tool_use_id":"%s","content":%s}]},"toolUseResult":%s}\n' "$1" "$CWD" "$2" "$3" "${4:-null}"; }
# A subagent transcript's one request: sub <file> <model> [date]
sub() { printf '{"type":"assistant","timestamp":"%sT12:00:00Z","message":{"model":"%s","content":[]}}\n' "${3:-2026-10-26}" "$2" >"$1"; }
O=claude-opus-5-5
F=claude-fable-5-1
BIG=$(python3 -I -c 'print("x" * 11000, end="")')
SMALL=$(python3 -I -c 'print("y" * 100, end="")')
E50=$(python3 -I -c 'print("é" * 50, end="")')

# A — the orchestrator in the main checkout: every Agent-call shape, two
# launches, landing reads, buddy verbs, the malformed lines.
CWD=/repo BR=main
{
	use 10:00:00 $O toolu_a1 Agent '{"description":"Cross-check the diff CANARY_DESC","prompt":"CANARY_PROMPT go","model":"fable","subagent_type":"general-purpose"}'
	res 10:00:01 toolu_a1 '"launched CANARY_RESULT"' '{"agentId":"agfable1","status":"async_launched","description":"CANARY_DESC"}'
	# The same call again: a request blob, then an assistant record (a copy).
	echo '{"type":"api-request-blob","timestamp":"2026-10-26T10:00:02Z","message":{"model":"claude-opus-5-5","content":[{"type":"tool_use","id":"toolu_a1","name":"Agent","input":{"description":"Cross-check","model":"fable"}}]}}'
	use 10:00:03 $O toolu_a1 Agent '{"description":"Cross-check","model":"fable","subagent_type":"general-purpose"}'
	use 10:01:00 $F toolu_a2 Agent '{"description":"Run the measurement script","prompt":"x","subagent_type":"general-purpose"}'
	res 10:01:01 toolu_a2 '"ok"' '{"agentId":"aginh1"}'
	use 10:02:00 $O toolu_a3 Agent '{"description":"find the review gate","prompt":"x","subagent_type":"Explore"}'
	res 10:02:01 toolu_a3 '"ok"' '{"agentId":"agexp1"}'
	use 10:03:00 $O toolu_a4 Agent '{"description":"fork side work","prompt":"x","subagent_type":"fork"}'
	res 10:03:01 toolu_a4 '"ok"' '{"agentId":"agfork1"}'
	use 10:04:00 $O toolu_nodesc Agent '{"prompt":"review this CANARY_NODESC","model":"opus","subagent_type":"general-purpose"}'
	res 10:04:01 toolu_nodesc '"ok"' '{"agentId":"agnd1"}'
	# A tool-use id that is not `toolu_...` is listed as (other); no result.
	use 10:04:30 $O CANARY_TID Agent '{"prompt":"x","model":"opus","subagent_type":"general-purpose"}'
	use 10:05:00 $O toolu_a6 Agent '{"description":"panel lens: state","prompt":"x","model":"sonnet","subagent_type":"general-purpose"}'
	res 10:05:01 toolu_a6 '"refused CANARY_RESULT"' '"Error: refused CANARY_RESULT"'
	use 10:06:00 $O toolu_a7 Agent '{"description":"Adversarial review","prompt":"x"}'
	res 10:06:01 toolu_a7 '"ok"' '{"agentId":"agmissing"}'
	use 10:07:00 $O toolu_a8 Agent '{"description":"write the docs","prompt":"x","model":"opus","subagent_type":"general-purpose"}'
	use 10:08:00 $O toolu_a9 Agent '{"description":"plain work","prompt":"x","model":"CANARY_MODEL x","subagent_type":"CANARY_TYPE x"}'
	res 10:08:01 toolu_a9 '"ok"' '{"agentId":"../CANARY_AID"}'
	# Two calls whose results share one record: whose toolUseResult is it?
	use 10:09:00 $O toolu_b1 Agent '{"description":"review b1","prompt":"x","model":"sonnet","subagent_type":"general-purpose"}'
	use 10:09:00 $O toolu_b2 Agent '{"description":"review b2","prompt":"x","model":"sonnet","subagent_type":"general-purpose"}'
	echo '{"type":"user","timestamp":"2026-10-26T10:09:01Z","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_b1","content":"ok"},{"type":"tool_result","tool_use_id":"toolu_b2","content":"ok"}]},"toolUseResult":{"agentId":"agfable1"}}'
	# Not calls: a sidechain's, a synthetic record's, one before the window.
	echo '{"type":"assistant","isSidechain":true,"timestamp":"2026-10-26T10:10:00Z","message":{"model":"claude-opus-5-5","content":[{"type":"tool_use","id":"toolu_side","name":"Agent","input":{"description":"review","model":"fable"}}]}}'
	echo '{"type":"assistant","timestamp":"2026-10-26T10:11:00Z","message":{"model":"<synthetic>","content":[{"type":"tool_use","id":"toolu_syn","name":"Agent","input":{"description":"review","model":"fable"}}]}}'
	echo '{"type":"assistant","timestamp":"2026-09-01T10:00:00Z","message":{"model":"claude-opus-5-5","content":[{"type":"tool_use","id":"toolu_old","name":"Agent","input":{"description":"review","model":"fable"}}]}}'
	# Stamped in the future: out of a window that ends now.
	echo '{"type":"assistant","timestamp":"2099-01-01T10:00:00Z","message":{"model":"claude-opus-5-5","content":[{"type":"tool_use","id":"toolu_future","name":"Agent","input":{"description":"review","model":"fable"}}]}}'
	echo '{"type":"assistant","message":{"content":"CANARY_TORN'
	echo '[1, 2]'
	printf '\377CANARY_BYTES\n'
	# Launches: two in command position, one only named in a commit message.
	use 11:00:00 $O toolu_h1 Bash '{"command":"cd /repo && herdr agent start --pane x -- claude CANARY_CMD"}' '{"input_tokens":10,"cache_read_input_tokens":1000,"cache_creation_input_tokens":200}'
	res 11:00:01 toolu_h1 '"ok"'
	use 11:01:00 $O toolu_h2 Bash '{"command":"herdr agent start y"}' '{"input_tokens":5,"cache_read_input_tokens":9000,"cache_creation_input_tokens":0}'
	res 11:01:01 toolu_h2 '"ok"'
	use 11:02:00 $O toolu_h3 Bash '{"command":"git commit -m \"teach herdr agent start\""}'
	res 11:02:01 toolu_h3 '"ok"'
	# Inside single quotes nothing runs: neither a launch nor a buddy verb.
	use 11:03:00 $O toolu_h4 Bash "{\"command\":\"printf '%s' '; herdr agent start x; buddy who'\"}"
	res 11:03:01 toolu_h4 '"; herdr agent start x; buddy who"'
	# Landing reads: a worktree path, a branch, a Read; then three that are not.
	use 11:10:00 $O toolu_r1 Bash '{"command":"git -C /repo/.claude/worktrees/lane-a diff main"}'
	res 11:10:01 toolu_r1 "\"$BIG\""
	# Names wt-b's worktree AND its branch: one lane, not two.
	use 11:11:00 $O toolu_r2 Bash '{"command":"git -C /repo/.claude/worktrees/wt-b diff main...lane-b"}'
	res 11:11:01 toolu_r2 "[{\"type\":\"text\",\"text\":\"$BIG\"}]"
	use 11:12:00 $O toolu_r3 Read '{"file_path":"/repo/.claude/worktrees/lane-a/CANARY_PATH.go"}'
	res 11:12:01 toolu_r3 "\"$BIG\""
	use 11:13:00 $O toolu_r4 Bash '{"command":"cat /repo/README.md"}'
	res 11:13:01 toolu_r4 "\"$BIG\""
	use 11:14:00 $O toolu_r5 Bash '{"command":"git log lane-a"}'
	res 11:14:01 toolu_r5 "\"$SMALL\""
	use 11:15:00 $O toolu_r6 Bash '{"command":"git log lane-a-2"}'
	res 11:15:01 toolu_r6 "\"$BIG\""
	# A branch with a slash names its lane (wt-c).
	use 11:16:00 $O toolu_r7 Bash '{"command":"git diff main...feat/c"}'
	res 11:16:01 toolu_r7 "\"$BIG\""
	# Buddy verbs: 50 two-byte characters are 100 bytes.
	use 11:20:00 $O toolu_v1 Bash '{"command":"buddy claim x --scope y"}'
	res 11:20:01 toolu_v1 "\"$E50\""
	use 11:21:00 $O toolu_v2 Bash '{"command":"cd /x && buddy msg orc CANARY_CMD"}'
	res 11:21:01 toolu_v2 '"'"$(python3 -I -c 'print("m" * 50, end="")')"'"'
	use 11:22:00 $O toolu_v3 Bash '{"command":"echo hi\nbuddy wait --until 1h"}'
	res 11:22:01 toolu_v3 '"'"$(python3 -I -c 'print("w" * 30, end="")')"'"'
	use 11:23:00 $O toolu_v4 Bash '{"command":"buddy zzcanaryverb"}'
	res 11:23:01 toolu_v4 '"1234567"'
	use 11:24:00 $O toolu_v5 Bash '{"command":"echo buddy claim"}'
	res 11:24:01 toolu_v5 '"123456789"'
} >"$P/$A.jsonl"

# A's subagent transcripts: four joined, one orphan, one that opens with a
# synthetic record, one before the window, and one that made its own call.
sub "$SA/agent-agfable1.jsonl" $F
sub "$SA/agent-aginh1.jsonl" $F
sub "$SA/agent-agexp1.jsonl" $O
sub "$SA/agent-agfork1.jsonl" $O
{
	echo '{"type":"assistant","timestamp":"2026-10-26T12:00:00Z","message":{"model":"claude-opus-5-5","content":[{"type":"tool_use","id":"toolu_nested","name":"Agent","input":{"description":"review","model":"fable"}}]}}'
} >"$SA/agent-agnd1.jsonl"
sub "$SA/agent-orphan.jsonl" claude-sonnet-5-5
{
	echo '{"type":"assistant","timestamp":"2026-10-26T12:00:00Z","message":{"model":"<synthetic>","content":[]}}'
	echo '{"type":"assistant","timestamp":"2026-10-26T12:00:01Z","message":{"model":"claude-haiku-5-5","content":[]}}'
} >"$SA/agent-synth.jsonl"
sub "$SA/agent-oldsub.jsonl" $O 2026-09-01

# A copied history: toolu_a8 again, here WITH its result, the subagent under
# this session. Counted once (in A), and its result still joins it.
CWD=/repo BR=main
{
	use 10:07:00 $O toolu_a8 Agent '{"description":"write the docs","prompt":"x","model":"opus","subagent_type":"general-purpose"}'
	res 10:07:01 toolu_a8 '"ok"' '{"agentId":"agcopy"}'
} >"$P/$B.jsonl"
sub "$P/$B/subagents/agent-agcopy.jsonl" $O

# A transcript whose file name is no UUID (though every character would pass
# an id charset): printed as (other).
echo '{"type":"assistant","timestamp":"2026-10-26T12:00:00Z","cwd":"/repo","message":{"model":"claude-opus-5-5","content":[]}}' >"$P/CANARY_SID.jsonl"
sub "$P/CANARY_SID/subagents/agent-q.jsonl" claude-sonnet-5-5

# The lanes: lane-a (worktree and branch lane-a), wt-b on branch lane-b, one
# whose branch fails the charset (its worktree name still counts).
CWD=/repo/.claude/worktrees/lane-a BR=lane-a
use 09:00:00 $O toolu_la Glob '{}' >"$PR/-repo--claude-worktrees-lane-a/dddddddd-0000-4000-8000-000000000004.jsonl"
CWD=/repo/.claude/worktrees/wt-b BR=lane-b
use 09:00:00 $O toolu_lb Glob '{}' >"$PR/-repo--claude-worktrees-wt-b/eeeeeeee-0000-4000-8000-000000000005.jsonl"
CWD=/repo/.claude/worktrees/wt-b BR="CANARY_BRANCH x"
use 09:00:00 $O toolu_lc Glob '{}' >"$PR/-repo--claude-worktrees-wt-b/ffffffff-0000-4000-8000-000000000006.jsonl"
CWD=/repo/.claude/worktrees/wt-c BR=feat/c
use 09:00:00 $O toolu_ld Glob '{}' >"$PR/-repo--claude-worktrees-wt-c/abababab-0000-4000-8000-000000000007.jsonl"

# C — an orchestrator in a worktree of its own: reading it is no landing read.
CWD=/repo/.claude/worktrees/orc-wt BR=orc-wt
{
	use 13:00:00 $O toolu_c1 Bash '{"command":"herdr agent start z"}' '{"input_tokens":7}'
	res 13:00:01 toolu_c1 '""'
	use 13:01:00 $O toolu_c2 Read '{"file_path":"/repo/.claude/worktrees/orc-wt/x.go"}'
	res 13:01:01 toolu_c2 "\"$BIG\""
	use 13:02:00 $O toolu_c3 Read '{"file_path":"/repo/.claude/worktrees/lane-a/y.go"}'
	res 13:02:01 toolu_c3 "\"$BIG\""
} >"$PR/-repo--claude-worktrees-orc-wt/$C.jsonl"

# Modified before the window: read only when --since reaches its mtime.
{
	echo '{"type":"assistant","timestamp":"2026-10-26T14:00:00Z","cwd":"/repo","message":{"model":"claude-opus-5-5","content":[{"type":"tool_use","id":"toolu_stale","name":"Agent","input":{"description":"review","model":"fable"}}]}}'
} >"$P/99999999-0000-4000-8000-000000000009.jsonl"
touch -t 202610010000 "$P/99999999-0000-4000-8000-000000000009.jsonl"

CANARIES="CANARY_DESC CANARY_PROMPT CANARY_NODESC CANARY_TORN CANARY_BYTES CANARY_CMD CANARY_RESULT CANARY_PATH CANARY_MODEL CANARY_TYPE CANARY_AID CANARY_BRANCH CANARY_SID CANARY_TID zzcanaryverb"
# Control: every canary is in the fixture, or its absence below proves nothing.
# LC_ALL=C: under a UTF-8 locale grep skips the non-UTF-8 line's canary.
for c in $CANARIES; do
	LC_ALL=C grep -rqa -- "$c" "$PR" || find "$PR" -name "*$c*" | grep -q . || fail "control: canary $c is not planted in the fixture"
done

run() { CLAUDE_PROJECTS_DIR="$PR" sh scripts/subagent-report.sh "$@"; }
OUT="$WORK/out"
ERR="$WORK/err"
rc=0
run --since 2026-10-20 --until 2026-11-01 >"$OUT" 2>"$ERR" || rc=$?
[ "$rc" = 0 ] || { cat "$ERR" >&2; fail "the report exited $rc over the fixture"; }

has() { grep -qxF -- "$1" "$OUT" || { cat "$OUT" >&2; fail "missing line: '$1' ($2)"; }; }
row() { # <label> <review> <other> <unclassified> <total> <why>
	awk -v l="$1" -v r="$2" -v o="$3" -v u="$4" -v t="$5" '
		NF >= 5 { n = NF; lab = $1; for (i = 2; i <= n - 4; i++) lab = lab " " $i }
		NF >= 5 && lab == l && $(n-3) == r && $(n-2) == o && $(n-1) == u && $n == t { hit = 1 }
		END { exit !hit }' "$OUT" || { cat "$OUT" >&2; fail "row '$1' is not $2 $3 $4 $5 ($6)"; }
}

# Privacy first: nothing planted reaches either stream.
if LC_ALL=C grep -ia -- canary "$OUT" "$ERR" >&2; then fail "transcript text reached the report (above)"; fi

has "skipped: 3 line(s) not a JSON object, 0 file(s) unreadable" "a torn line, a non-object, a non-UTF-8 line"
has "AGENT CALLS  12 on main threads in 1 session(s); 1 made inside subagent transcripts (in no table below)" \
	"copies, a sidechain, a synthetic, an old and a future call not counted; the nested call apart"
row "(other)" 0 1 0 1 "a model param failing the charset"
row "fable" 1 0 0 1 "counted once across a request blob and a copy"
row "opus" 0 1 2 3 "the call with no description is unclassified, whatever its prompt says"
row "sonnet" 3 0 0 3 "review-shaped descriptions"
row "(none)" 2 2 0 4 "unnamed calls"
row "(none), (none)" 1 0 0 1 "an omitted type keeps its own row"
row "(none), Explore" 1 0 0 1 "an Explore is not general-purpose"
row "(none), fork" 0 1 0 1 "a fork is not general-purpose"
row "(none), general-purpose" 0 1 0 1 "only the named general-purpose call"
row "total" 6 4 2 12 "totals"
has "     1  fable  $O  general-purpose  review  $F" "a Fable call from an Opus parent ran on Fable: its transcript's model, not the parent's"
has "     1  (none)  $F  general-purpose  other  $F" "an unnamed call from a Fable parent"
has "     1  (none)  $O  Explore  review  $O" "Explore"
has "     1  opus  $O  general-purpose  unclassified  $O" "no description"
has "     3  sonnet  $O  general-purpose  review  (no agent id)" "a refused call, and two results on one record"
has "     1  (none)  $O  (none)  review  (no transcript)" "an agent id with no file"
has "     1  opus  $O  general-purpose  other  $O" "a copied call's result, found only in the copy's file, joins it"
has "     1  opus  $O  general-purpose  unclassified  (no result)" "no result"
has "     1  (other)  $O  (other)  other  (no agent id)" "an agent id failing the charset (../) builds no path"
has "unnamed general-purpose calls (no model param; type general-purpose or omitted, the tool's default): 2" "Explore and fork are not counted"
has "  review-shaped: 1, of which ran (a subagent transcript joined): 0" "the omitted-type review has no transcript"
has "     1  $F" "the unnamed general-purpose call that ran on Fable"
has "calls with no description (unclassified; listed, not guessed): 2" "listed"
has "  $A  (other)" "a tool-use id that is not toolu_... is not printed"
has "  $A  toolu_nodesc" "listed by session and tool-use id"
has "SUBAGENT TRANSCRIPTS by session and model (first request in the window): 9 in 3 session(s)" "the old one out, the orphan and the synthetic-first one in"
has "  $A  claude-fable-5-1=2 claude-haiku-5-5=1 claude-opus-5-5=3 claude-sonnet-5-5=1" "by model, synthetic skipped"
has "  (other)  claude-sonnet-5-5=1" "a file name that is no UUID"
has "  $B  claude-opus-5-5=1" "the copy's session"
has "ORCHESTRATORS (sessions that ran \`herdr agent start\` in the window): 2; 4 lane(s) known" \
	"lane-a, wt-b (branch lane-b), orc-wt, wt-c (branch feat/c)"
awk -v s="$A" '$1 == s && $2 == 2 && $3 == 1210 && $4 == 4 && $5 == 44000 && $6 == 3 { hit = 1 } END { exit !hit }' "$OUT" ||
	{ cat "$OUT" >&2; fail "orchestrator A: want 2 launches (a quoted one is none), 1210 at the first, 4 landing reads of 44000 bytes naming 3 lanes (a worktree and its branch are one)"; }
awk -v s="$C" '$1 == s && $2 == 1 && $3 == 7 && $4 == 1 && $5 == 11000 && $6 == 1 { hit = 1 } END { exit !hit }' "$OUT" ||
	{ cat "$OUT" >&2; fail "orchestrator C: want 1 launch, 7 at it, 1 landing read (its own worktree is not one)"; }
# Bash: 16 results; 2+2+2+32 + 5x11000 + 100 + 100+50+30+7+9 bytes, 1 of them empty.
has "BASH RESULTS  16, 55334 bytes; with a buddy verb: 4, 187 bytes, 0.3% of all Bash result bytes" "UTF-8 bytes; command position; a quoted verb is none"
for v in "claim 1 100" "msg 1 50" "wait 1 30" "(other) 1 7"; do
	set -- $v
	awk -v v="$1" -v n="$2" -v b="$3" '$1 == v && $2 == n && $4 == b { hit = 1 } END { exit !hit }' "$OUT" ||
		{ cat "$OUT" >&2; fail "verb $1: want $2 call(s), $3 bytes"; }
done

# The stale transcript: in only when the window reaches its mtime.
run --since 2026-09-30 --until 2026-11-01 >"$WORK/out2" 2>&1 || fail "the report failed with an earlier --since"
grep -qxF "AGENT CALLS  13 on main threads in 2 session(s); 1 made inside subagent transcripts (in no table below)" "$WORK/out2" ||
	{ cat "$WORK/out2" >&2; fail "an earlier --since did not read the transcript modified before the first window"; }

# No --until: the window ends now, so the call stamped 2099 stays out.
run --since 2026-10-20 >"$WORK/out3" 2>&1 || fail "the report failed with no --until"
grep -qxF "AGENT CALLS  12 on main threads in 1 session(s); 1 made inside subagent transcripts (in no table below)" "$WORK/out3" ||
	{ cat "$WORK/out3" >&2; fail "with no --until, a record stamped in the future was counted"; }

# The verb list is the dispatch table's.
want=$(sed -n '/^var verbs = map\[string\]verb{/,/^}/p' internal/cli/cli.go | sed -n 's/^[[:space:]]*"\([a-z-]*\)":.*/\1/p' | sort)
got=$(python3 -I -c '
import re, sys
s = open(sys.argv[1]).read()
m = re.search(r"VERBS = frozenset\((.*?)\.split\(\)\)", s, re.S)
print("\n".join(sorted("".join(re.findall(r"\"([^\"]*)\"", m.group(1))).split())) if m else "")
' scripts/subagent-report.py)
case "$want" in *claim*) ;; *) fail "control: read no verbs from internal/cli/cli.go's table" ;; esac
[ "$want" = "$got" ] || fail "the report's VERBS differs from internal/cli/cli.go's verbs table: $(printf '%s\n' "$want" "$got" | sort | uniq -u | tr '\n' ' ')"

# Refusals, each with nothing on stdout; --help answers.
refuse() {
	rc=0
	run "$@" >"$WORK/r.out" 2>"$WORK/r.err" || rc=$?
	[ "$rc" = 2 ] || fail "'$*' exited $rc, want 2"
	[ ! -s "$WORK/r.out" ] || fail "'$*' printed on stdout"
	tail -n 1 "$WORK/r.err" | grep -q '^refused: ' || fail "'$*' did not end with refused:"
}
refuse --since 2026-13-01
refuse --since 2026-02-31
refuse --bogus
refuse --since
refuse --since 2026-10-30 --until 2026-10-20
run --help >"$WORK/h.out" 2>&1 || fail "--help did not exit 0"
grep -q '^usage: subagent-report.sh' "$WORK/h.out" || fail "--help printed no usage"

echo "check-subagent-report: OK"
