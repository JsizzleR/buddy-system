#!/bin/sh
# check-cost-report.sh — done-check for scripts/cost-report.sh, its two prose
# sections above all (docs/PROSE-BUDGET-PLAN.md section 5, step 4). The report
# had no check before this one, and a report nothing gates is the one whose
# numbers drift: the plan's own measurement found two request counts for one
# week (5,254 and 5,452) because two hand runs keyed requests differently.
#
# HERMETIC: sh, git, jq, sqlite3, awk. A planted checkout holds COPIES of the
# report, check-prose-budget.sh and lib.sh beside a planted prose table, so the
# report's "this checkout" is the fixture's; HOME and CLAUDE_PROJECTS_DIR are
# temp dirs, the journal, ledger and buddylist binary point at nothing. Nothing
# under ~/.claude, ~/.buddylist or this checkout's ledger is read. Without jq or
# sqlite3 this says NOT RUN, as check-wake-report.sh does.
#
# What must hold, each with the control that proves the assertion could fire:
#   - INVARIANT 9: a canary planted in every transcript text a record carries
#     (a prompt, a tool result, assistant text, a skill body, a Bash command,
#     a Read path, a Skill name, an MCP tool name, a model name, a message id
#     outside the repo, a transcript's file name, a malformed line) never
#     reaches the output. The canary is first found in the fixture (it was
#     planted), and a copy of the report that prints the skill names it read
#     must FAIL the same audit (the audit is armed);
#   - Fixed prose: every class total is the sum of its members as the check
#     enumerates them, measured here with wc -c and independently of both
#     scripts; the section key is printed under its file and labelled, never
#     added (the total equals the members' sum EXACTLY, so adding it fails); a
#     member of two classes is labelled in the second; the planted over-ceiling
#     line says OVER and an under one does not (two clauses);
#   - Context billed: one request counted once across the records of one
#     message, a resumed transcript's copied lines, and files; never a
#     <synthetic> record, an out-of-window one, or one whose cwd is another
#     repo or a sibling directory sharing the checkout's name as a prefix; a
#     linked worktree's and a removed worktree's under the checkout counted;
#     subagent requests counted and their session not counted twice;
#   - skill exposure over that set: a request after a Skill call, a /command
#     and a resumed copy of the load is carried, the request that made the
#     call is not, a subagent's file does not inherit its parent's load, and a
#     ranged Read of a SKILL.md loads nothing;
#   - read-first obedience inside the first 10 main-thread tool calls only
#     (one session reads a listed file at its 11th: not obeyed); docs/
#     decisions.md mentions against whole-file reads, a piped cat a mention;
#   - codex passes from receipts only, both spellings, wrapped, in the window;
#   - a malformed transcript line degrades to the FLOOR note, never to text.
set -eu
. "$(dirname -- "$0")/lib.sh"
ROOT=$(repo_root "$0")
CDPATH= cd -- "$ROOT"
LC_ALL=C; export LC_ALL

fail() { echo "check-cost-report: FAIL — $*" >&2; exit 1; }

for c in jq sqlite3; do
	command -v "$c" >/dev/null 2>&1 || { echo "check-cost-report: NOT RUN — no $c on PATH" >&2; exit 0; }
done

# Physical, so the cwd a record names and the path git lists are one spelling
# (macOS /var is a link to /private/var).
WORK=$(mktemp -d) || fail "mktemp failed"
WORK=$(CDPATH= cd -- "$WORK" && pwd -P)
trap 'rm -rf "$WORK"' EXIT INT TERM
CANARY=CANARYzq7x
# All lowercase, so it passes any test of a tool name's SHAPE (Codex code pass).
LCANARY=canaryzqx

# --- the planted checkout -----------------------------------------------------
REPO="$WORK/repo"
mkrepo "$REPO"
git -C "$REPO" config core.excludesFile /dev/null
mkdir -p "$REPO/scripts" "$REPO/docs" "$REPO/skills/buddy" "$REPO/skills/buddy-fleet"
cp scripts/cost-report.sh scripts/check-prose-budget.sh scripts/lib.sh "$REPO/scripts/"
printf 'CHARTER="$(pwd)/docs/review-charter.md"\n' > "$REPO/scripts/codex-review.sh"
cat > "$REPO/CLAUDE.md" <<'MD'
# Rules

## Read this first

- `docs/a.md` and `docs/decisions.md`.

## Decisions

- D-001: one decision, indexed.
MD
printf 'Read `CLAUDE.md` before changing this repository.\n' > "$REPO/AGENTS.md"
printf '# A\n\nplanted, longer than its ceiling\n' > "$REPO/docs/a.md"
printf '# Decisions\n\n## D-001\n\nthe record\n' > "$REPO/docs/decisions.md"
printf '# Charter\n' > "$REPO/docs/review-charter.md"
printf -- '---\nname: buddy\ndescription: the core skill\n---\n\nThe core body, which a Skill call loads.\n' > "$REPO/skills/buddy/SKILL.md"
printf -- '---\nname: buddy-fleet\ndescription: the fleet skill\n---\n\nThe fleet body.\n' > "$REPO/skills/buddy-fleet/SKILL.md"
{
	printf '# planted\n'
	printf 'every-request\t100000\tfixture\tclass\n'
	printf 'CLAUDE.md\t100000\tfixture\tunder\n'
	printf 'CLAUDE.md#Decisions\t1000\tfixture\ta section\n'
	printf 'skills/buddy/SKILL.md:description\t1000\tfixture\tx\n'
	printf 'skills/buddy-fleet/SKILL.md:description\t1000\tfixture\tx\n'
	printf 'per-session\t100000\tfixture\tclass\n'
	printf 'skills/buddy/SKILL.md\t1000\tfixture\tx\n'
	printf 'skills/buddy-fleet/SKILL.md\t1000\tfixture\tx\n'
	printf 'read-first\t100000\tfixture\tclass\n'
	printf 'docs/a.md\t10\tfixture\tOVER, planted\n'
	printf 'docs/decisions.md\t1000\tfixture\tx\n'
	printf 'codex-pass\t100000\tfixture\tclass\n'
	printf 'AGENTS.md\t1000\tfixture\tx\n'
	printf 'docs/review-charter.md\t1000\tfixture\tx\n'
	printf 'docs/gone.md\t1000\tfixture\ta key whose text is gone\n'
} > "$REPO/docs/prose-budget.tsv"
git -C "$REPO" add -A
old=$(jq -rn 'now - 30 * 86400 | strftime("%Y-%m-%dT%H:%M:%SZ")')
GIT_COMMITTER_DATE=$old GIT_AUTHOR_DATE=$old git -C "$REPO" commit -q -m 'old' -m 'review: lite (self + codex@xhigh code)'
git -C "$REPO" commit -q --allow-empty -m 'one' -m 'The codex@xhigh pass in prose is no receipt.' -m 'review: lite (self + codex@xhigh code)'
git -C "$REPO" commit -q --allow-empty -m 'two' -m 'Review: full (panel + codex@xhigh design +
  codex@xhigh code + fable)'
# A newline in the path: git's line-delimited porcelain splits it across two
# lines (a tab it prints raw, measured on 2.54), so only `-z` output names
# the cwd a record holds (Codex code pass).
WT="$WORK/wt
x"
git -C "$REPO" worktree add -q --detach "$WT"

# --- the transcripts ------------------------------------------------------------
P="$WORK/projects"
mkdir -p "$P/-repo/S1/subagents" "$P/-other"
NOW=$(jq -rn 'now - 3600 | strftime("%Y-%m-%dT%H:%M:%S.000Z")')
OLD=$(jq -rn 'now - 30 * 86400 | strftime("%Y-%m-%dT%H:%M:%S.000Z")')
# a <id> <cr> <cw> <in> [<sid> <cwd> <ts> <sidechain> <model>] <content-json>
a() {
	jq -nc --arg id "$1" --argjson cr "$2" --argjson cw "$3" --argjson in "$4" --arg sid "$5" --arg cwd "$6" \
		--arg ts "$7" --argjson sc "$8" --arg model "$9" --argjson c "${10}" \
		'{type: "assistant", sessionId: $sid, cwd: $cwd, timestamp: $ts, isSidechain: $sc, requestId: ("req_" + $id),
		  message: {id: $id, model: $model, content: $c,
		    usage: {input_tokens: $in, cache_read_input_tokens: $cr, cache_creation_input_tokens: $cw, output_tokens: 5}}}'
}
# u <sid> <cwd> <content-json> [isMeta]
u() {
	jq -nc --arg sid "$1" --arg cwd "$2" --arg ts "$NOW" --argjson c "$3" --argjson meta "${4:-false}" \
		'{type: "user", sessionId: $sid, cwd: $cwd, timestamp: $ts, isMeta: $meta, message: {role: "user", content: $c}}'
}
tool() { jq -nc --arg id "$1" --arg name "$2" --argjson input "$3" '[{type: "tool_use", id: $id, name: $name, input: $input}]'; }
txt() { jq -nc --arg t "$1" '[{type: "text", text: $t}]'; }
S1=11111111-0000-4000-8000-000000000001
S2=22222222-0000-4000-8000-000000000002
S3=33333333-0000-4000-8000-000000000003
S4=44444444-0000-4000-8000-000000000004
M=claude-opus-5-5
{
	a m0 99999 0 1 "$S1" "$REPO" "$OLD" false "$M" "$(tool t0 Bash '{"command":"cat docs/decisions.md"}')"
	u "$S1" "$REPO" "$(jq -nc --arg c "$CANARY" '"please " + $c')"
	a m1 1000 100 10 "$S1" "$REPO" "$NOW" false "$M" "$(tool t1 Read "{\"file_path\":\"$REPO/docs/a.md\"}")"
	a m1 1000 100 10 "$S1" "$REPO" "$NOW" false "$M" "$(txt "$CANARY assistant text")"
	u "$S1" "$REPO" "$(jq -nc --arg c "$CANARY" '[{type: "tool_result", tool_use_id: "t1", content: ("result " + $c)}]')"
	a m2 2000 0 20 "$S1" "$REPO" "$NOW" false "$M" "$(tool t2 Skill '{"skill":"buddy"}')"
	u "$S1" "$REPO" "$(jq -nc --arg c "$CANARY" '[{type: "text", text: ("skill body " + $c)}]')" true
	a m3 3000 0 30 "$S1" "$REPO" "$NOW" false "$M" "$(jq -nc --arg r "$REPO" '[
		{type: "tool_use", id: "t3", name: "Read", input: {file_path: ($r + "/docs/decisions.md")}},
		{type: "tool_use", id: "t4", name: "Grep", input: {pattern: "D-1", path: "docs/decisions.md"}}]')"
	a m9 5000 0 1 "$S1" "$REPO" "$NOW" false "<synthetic>" "$(txt "$CANARY")"
	u "$S1" "$REPO" '"<command-name>/buddy-fleet</command-name>"'
	a m4 4000 400 40 "$S1" "$REPO" "$NOW" false "$M" "$(tool t2b Skill "{\"skill\":\"$CANARY\"}")"
} > "$P/-repo/$S1.jsonl"
# The resumed copy: the first seven lines again, then one new request.
{
	sed -n 2,7p "$P/-repo/$S1.jsonl"
	a m5 5000 0 50 "$S1" "$REPO" "$NOW" false "$M" "$(tool t5 Bash '{"command":"cat docs/decisions.md | head -5"}')"
} > "$P/-repo/$CANARY.jsonl"
a m6 6000 0 60 "$S1" "$REPO" "$NOW" true "$M" "$(tool t6 Read "{\"file_path\":\"$REPO/docs/a.md\"}")" > "$P/-repo/S1/subagents/agent-a.jsonl"
{
	for i in 1 2 3 4 5 6 7 8 9 10; do
		a "m7" 7000 700 70 "$S2" "$REPO" "$NOW" false "$M" "$(tool "s2t$i" Bash "{\"command\":\"ls $CANARY\"}")"
	done
	a m7 7000 700 70 "$S2" "$REPO" "$NOW" false "$M" "$(jq -nc --arg r "$REPO" '[
		{type: "tool_use", id: "s2t11", name: "Read", input: {file_path: ($r + "/docs/a.md")}},
		{type: "tool_use", id: "s2t12", name: "Read", input: {file_path: ($r + "/skills/buddy/SKILL.md"), offset: 1}},
		{type: "tool_use", id: "s2t13", name: "mcp__buddylist__chat_read", input: {tail: 3}}]')"
	a m8 300 0 1 "$S2" "$REPO" "$NOW" false "$M" "$(tool s2t14 "mcp__buddylist__$CANARY" '{}')"
	# A Skill call that FAILED loads nothing; a whole-file Read of a SKILL.md does.
	a m13 400 0 1 "$S2" "$REPO" "$NOW" false "$M" "$(tool s2t15 Skill '{"skill":"buddy"}')"
	u "$S2" "$REPO" '[{"type":"tool_result","tool_use_id":"s2t15","is_error":true,"content":"no such skill"}]'
	a m14 500 0 2 "$S2" "$REPO" "$NOW" false "$M" "$(tool s2t16 Read "{\"file_path\":\"$REPO/skills/buddy-fleet/SKILL.md\"}")"
	u "$S2" "$REPO" '[{"type":"tool_result","tool_use_id":"s2t16","content":"the body"}]'
	a m15 600 0 1 "$S2" "$REPO" "$NOW" false "$M" "$(tool s2t17 "mcp__buddylist__$LCANARY" '{}')"
} > "$P/-repo/$S2.jsonl"
a m10 100 0 1 "$S3" "$WT/sub" "$NOW" false "$M" "$(tool s3t1 Bash '{"command":"cat docs/decisions.md README.md"}')" > "$P/-repo/$S3.jsonl"
a m11 200 0 2 "$S4" "$REPO/.claude/worktrees/gone" "$NOW" false "$M" '[]' > "$P/-repo/$S4.jsonl"
{
	a "$CANARY" 8000 0 1 "$S4" /elsewhere/proj "$NOW" false "$CANARY" "$(tool o1 Read "{\"file_path\":\"$CANARY/docs/decisions.md\"}")"
	a m12 9000 0 1 "$S4" "$REPO-old" "$NOW" false "$M" "$(tool o2 Read '{"file_path":"docs/decisions.md"}')"
} > "$P/-other/x.jsonl"
grep -rqF "$CANARY" "$P" || fail "the canary is not in the planted transcripts — the audit below would pass vacuously"

# report <copy> — run a copy of the report from inside the planted checkout.
report() {
	( cd "$REPO" && HOME="$WORK/home" CLAUDE_PROJECTS_DIR="$P" BUDDYLIST_JOURNAL="$WORK/none.db" \
		BUDDY_LEDGER="$WORK/none-ledger.db" BUDDYLIST_BIN="$WORK/no-buddylist" BUDDY_COST_DAYS=7 sh "$1" )
}
out=$(report scripts/cost-report.sh 2>&1) || fail "the report exited non-zero on the planted tree:
$out"
case $out in *"$CANARY"*|*"$LCANARY"*) fail "the report printed planted transcript text (invariant 9)" ;; esac
grep -rqF "$LCANARY" "$P" || fail "the lowercase canary is not in the planted transcripts"

# The control: a copy that prints the skill names it read must fail the audit.
awk '/^# Codex passes in the window/ { print "awk -F \"\\t\" '"'"'$1 == \"K\" { print $5 }'"'"' \"$events\"" } { print }' \
	"$REPO/scripts/cost-report.sh" > "$REPO/scripts/leaky-report.sh"
leak=$(report scripts/leaky-report.sh 2>&1) || true
case $leak in *"$CANARY"*) ;; *) fail "a copy of the report that prints skill names did not print the canary — the audit is not armed" ;; esac

# clause <prefix> <text> — that line carries the text verbatim, so a count is
# bound to its label: "1 of 4 sessions; rode 4 of 13" cannot pass as "4 of 4
# sessions; rode 1 of 13" (Codex code pass: an unordered token test did).
clause() {
	_l=$(line "$1") || exit 1
	case $_l in *"$2"*) ;; *) fail "line '$_l' lacks '$2'" ;; esac
}
# line <prefix> — the one line of $scope (the whole output unless in_class
# narrowed it) that starts with the prefix and a space or a tab.
scope=
line() {
	l=$(printf '%s\n' "${scope:-$out}" | awk -v p="$1" 'index($0, p " ") == 1 || index($0, p "\t") == 1')
	[ "$(printf '%s\n' "$l" | grep -c .)" = 1 ] || fail "want exactly one line starting '$1', got:
$l"
	printf '%s\n' "$l"
}
# has <prefix> <token>... — that line carries every token, space-delimited.
has() {
	_l=$(line "$1") || exit 1
	_l=" $(printf '%s\n' "$_l" | tr '\t' ' ' | tr -s ' ') "; shift
	for t in "$@"; do
		case $_l in *" $t "*) ;; *) fail "line '$_l' lacks '$t'" ;; esac
	done
}
lacks() {
	_l=$(line "$1") || exit 1
	_l=" $(printf '%s\n' "$_l" | tr '\t' ' ' | tr -s ' ') "; shift
	case $_l in *" $1 "*) fail "line '$_l' carries '$1'" ;; esac
}
# in_class <class> — narrow $scope to that class's line and the indented ones
# under it (CLAUDE.md is a member of two classes); in_class "" widens again.
in_class() {
	[ -n "$1" ] || { scope=; return 0; }
	scope=$(printf '%s\n' "$out" | awk -v c="$1" 'index($0, c " ") == 1 { on = 1; print; next } on && /^  / { print; next } { on = 0 }')
	[ -n "$scope" ] || fail "no class '$1' in the report"
}
sz() { wc -c < "$REPO/$1" | tr -d ' '; }
desc_b=$(printf 'description: the core skill\n' | wc -c | tr -d ' ')
fdesc_b=$(printf 'description: the fleet skill\n' | wc -c | tr -d ' ')
sec_b=$(sed -n '/^## Decisions/,$p' "$REPO/CLAUDE.md" | wc -c | tr -d ' ')
core=$(sz skills/buddy/SKILL.md); fleet=$(sz skills/buddy-fleet/SKILL.md)
er=$(( $(sz CLAUDE.md) + desc_b + fdesc_b ))
rf=$(( $(sz CLAUDE.md) + $(sz docs/a.md) + $(sz docs/decisions.md) ))
tok() { echo $(( ($1 + 3) / 4 )); }

# --- Fixed prose ------------------------------------------------------------------
has "Fixed prose" "(docs/prose-budget.tsv)"
in_class every-request
has every-request "bytes=$er" ceiling=100000 "est_tokens=$(tok $er)"
has "  CLAUDE.md" "bytes=$(sz CLAUDE.md)"
lacks "  CLAUDE.md" "(also"
has "    CLAUDE.md#Decisions" "bytes=$sec_b"
clause "    CLAUDE.md#Decisions" "(a section: inside CLAUDE.md, not added)"
has "  skills/buddy/SKILL.md:description" "bytes=$desc_b"
lacks every-request OVER
in_class ""
has per-session "bytes=$((core + fleet))"
clause per-session "(loaded in 2 of 4 sessions; rode 4 of 13 requests)"
has "  skills/buddy/SKILL.md" "bytes=$core"
has read-first "bytes=$rf"
# S1 (a Read of docs/a.md) and S3 (its first call cats two listed files);
# not S2, whose read is its 11th call.
clause read-first "(obeyed in 2 of 4 sessions, by a Read/cat"
in_class read-first
has "  docs/a.md" "bytes=$(sz docs/a.md)" ceiling=10 OVER
lacks "  docs/decisions.md" OVER
clause "  CLAUDE.md" "(also in every-request, counted in each)"
in_class ""
has codex-pass "bytes=$(( $(sz AGENTS.md) + $(sz docs/review-charter.md) ))"
clause codex-pass "(passes in window: 3, from receipts naming codex@)"
has "  docs/gone.md" GONE

# --- Context billed ---------------------------------------------------------------
# Requests: m1 m2 m3 m4 m5 (S1, m5 only in the resumed copy), m6 (S1's
# subagent), m7 m8 m13 m14 m15 (S2), m10 (S3, the linked worktree with a
# newline in its path), m11 (S4, a removed worktree under the checkout). Not m0 (old),
# m9 (<synthetic>), the other repo's, or the sibling `repo-old`'s.
cr=$((1000 + 2000 + 3000 + 4000 + 5000 + 6000 + 7000 + 300 + 400 + 500 + 600 + 100 + 200))
has "Context billed" "(transcripts"
has sessions=4 requests=13 subagent_requests=1 "cache_read=$cr" cache_write=1200 input=288 output=65
# Prompts: 101 202 301 401 502 601 1110 2020 3030 4440 5050 6060 7770; ranks 7 and 12.
has prompt "mean=$(( (101 + 202 + 301 + 401 + 502 + 601 + 1110 + 2020 + 3030 + 4440 + 5050 + 6060 + 7770 + 6) / 13 ))" p50=1110 p90=6060
# Carried: m3 and m5 (core), m4 (core and fleet), m15 (fleet, after S2's
# whole-file Read). Not m2 or m14, which made the call, m14 after a FAILED
# Skill call, m6 (the subagent's own file), or m7 after a ranged Read.
ps=$(( $(tok "$core") * 3 + $(tok "$fleet") * 2 ))
want_er=$(awk -v t="$(tok $er)" -v cr="$cr" 'BEGIN { printf "%.1f%%", t * 13 * 100 / cr }')
want_ps=$(awk -v t="$ps" -v cr="$cr" 'BEGIN { printf "%.1f%%", t * 100 / cr }')
has est_share_of_cache_reads "every-request=$want_er" "per-session=$want_ps" read-first=NOT MEASURED
# t3 (whole Read), s3t1 (an unfiltered cat of two files), t4 (Grep), t5
# (piped cat); not t0 (old), not the other repos'.
has "docs/decisions.md" mentions=4 whole_file=2 ranged_or_search=2

# --- the MCP section keeps a tool name's shape ------------------------------------
has "mcp__buddylist__(other)" calls=2
has mcp__buddylist__chat_read calls=1
# The report's list of the server's tools is mcp.go's, both ways: a tool the
# server gained would fold into (other) unseen.
want=$(grep -oE '^		name: "[a-z_]+"' internal/buddylist/mcp.go | sed 's/.*"\(.*\)"/\1/' | sort | tr '\n' ' ')
got=$(sed -n 's/^[[:space:]]*def known: IN(\(.*\));$/\1/p' scripts/cost-report.sh | tr ',' '\n' | tr -d '" ' | sed '/^$/d' | sort | tr '\n' ' ')
[ -n "$want" ] || fail "found no tool names in internal/buddylist/mcp.go's table — re-point this check"
[ "$got" = "$want" ] || fail "cost-report.sh knows the MCP tools '$got', mcp.go's table has '$want'"

# --- a malformed line: the FLOOR note, never its text ------------------------------
printf '{"type":"user","message":"%s\n' "$CANARY" > "$P/-repo/zz-torn.jsonl"
out=$(report scripts/cost-report.sh 2>&1) || fail "the report exited non-zero on a torn transcript"
case $out in *"$CANARY"*) fail "a torn transcript line reached the output (invariant 9)" ;; esac
case $out in *"every number below is a FLOOR"*) ;; *) fail "a torn transcript did not draw the FLOOR note" ;; esac

echo "check-cost-report: GREEN (prose classes summed from the check's enumeration, a section labelled not added, OVER only over; 13 requests deduplicated across messages, a resume and files; skill exposure, obedience and decisions reads on the planted counts; a planted canary never printed and a leaky copy caught)"
