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
#     repo, an EXISTING sibling `<root>-*` that is another repository or none,
#     or a directory sharing the name without the dash; a linked worktree's,
#     a removed worktree's under the checkout, and a REMOVED sibling
#     `<root>-*`'s (a landed lane's worktree, #77) counted, beside the
#     checkout and beside a linked worktree; subagent requests counted and
#     their session not counted twice;
#   - skill exposure over that set: a request after a Skill call, a /command
#     and a resumed copy of the load is carried, the request that made the
#     call is not, a subagent's file does not inherit its parent's load, and a
#     ranged Read of a SKILL.md loads nothing;
#   - read-first obedience inside the first 10 main-thread tool calls only
#     (one session reads a listed file at its 11th: not obeyed); docs/
#     decisions.md mentions against whole-file reads, a piped cat a mention;
#   - codex passes from receipts only, both spellings, wrapped, in the window;
#   - a line that is not JSON (a transcript's unterminated last line, being
#     written, or a malformed one mid-file) is skipped and counted by kind,
#     never printed, and never costs the lines and files after it; jq's own
#     error (a file it cannot open) still draws the FLOOR note (D-169).
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
	printf 'read-first\treport\tfixture\tclass, report-only (D-169)\n'
	printf 'docs/a.md\t10\tfixture\tOVER, planted\n'
	printf 'docs/decisions.md\treport\tfixture\treport-only, entries capped\n'
	printf 'docs/decisions.md#*\t1000\tfixture\teach entry\n'
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
# An EXISTING sibling named `repo-*` that is another repository: its
# records (m12 below) never count, though a removed one would (#77).
mkrepo "$REPO-old"

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

# --- codex-review.sh's pass record (Codex report A3, D-146) ------------------------
# Rows as that script writes them (14 tab-separated fields), in the planted
# checkout's common dir: two verdicts, a header mismatch, a watchdog kill, a busy
# NOT RUN, a row older than the window, two malformed rows (13 fields; a canary as
# the time), and a well-formed verdict whose model and session fields are the canary:
# counted, never printed.
NOWZ=$(jq -rn 'now - 3600 | strftime("%Y-%m-%dT%H:%M:%SZ")')
OLDZ=$(jq -rn 'now - 30 * 86400 | strftime("%Y-%m-%dT%H:%M:%SZ")')
{
	printf '%s\t0\t0\t200\tgpt-6-astra\txhigh\tok\tcore\t100\t200\t1000\t10\t2\tsid1\n' "$NOWZ"
	printf '%s\t0\t0\t400\tgpt-6-astra\txhigh\tok\tcore\t100\t200\t3000\t20\t0\tsid2\n' "$NOWZ"
	printf '%s\t0\t3\t100\tgpt-6-astra\txhigh\tMISMATCH\tcore\t100\t200\t500\t5\t1\tsid3\n' "$NOWZ"
	printf '%s\t124\t124\t540\tgpt-6-astra\txhigh\tok\tcore\t100\t200\t-\t30\t3\tsid4\n' "$NOWZ"
	printf '%s\t-\t75\t-\tgpt-6-astra\txhigh\t-\t-\t-\t-\t-\t-\t-\t-\n' "$NOWZ"
	printf '%s\t0\t0\t999\tgpt-6-astra\txhigh\tok\tcore\t100\t200\t99999\t99\t9\told\n' "$OLDZ"
	printf '%s\t0\t0\t1\tgpt-6-astra\txhigh\tok\tcore\t1\t1\t1\t1\t1\n' "$NOWZ"
	printf '%s\t0\t0\t1\tgpt-6-astra\txhigh\tok\tcore\t1\t1\t1\t1\t1\tx\n' "$CANARY"
	printf '%s\t0\t0\t300\t%s\txhigh\tok\tcore\t1\t1\t2000\t0\t0\t%s\n' "$NOWZ" "$CANARY" "$CANARY"
	# Malformed by the shape the writer never produces: a ran row with no elapsed,
	# an empty model field. And a codex that itself exited 75: it RAN, a no-verdict.
	printf '%s\t0\t0\t-\tgpt-6-astra\txhigh\tok\tcore\t1\t1\t1\t1\t1\tx\n' "$NOWZ"
	printf '%s\t0\t0\t1\t\txhigh\tok\tcore\t1\t1\t1\t1\t1\tx\n' "$NOWZ"
	printf '%s\t75\t75\t50\tgpt-6-astra\txhigh\tok\tcore\t1\t1\t-\t0\t0\tx\n' "$NOWZ"
} > "$REPO/.git/codex-passes.tsv"

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

# The pass record's section: exact aggregates of the planted rows (elapsed
# 50 100 200 300 400 540; tokens 500 1000 2000 3000; commands 0 0 5 10 20 30;
# re-reads in three passes, 2+1+3; the codex that exited 75 a no-verdict), the canary never printed (the audit above covers this
# output), and a copy whose section prints a row's model field must print it.
printf '%s\n' "$out" | grep -qxF "Codex passes recorded (codex-review.sh's pass record in the git common dir, last 7 day(s))" ||
	fail "no 'Codex passes recorded' heading"
printf '%s\n' "$out" | grep -qxF "passes=7 verdict=3 header_mismatch=1 no_verdict=2 not_run=1 malformed_rows=4" ||
	fail "the pass-record counts are wrong: $(printf '%s\n' "$out" | grep '^passes=')"
printf '%s\n' "$out" | grep -qxF "elapsed_s p50=200 p90=540  tokens sum=6500 p50=1000  commands p50=5 p90=30  charter_rereads passes=3 of 6 total=6" ||
	fail "the pass-record aggregates are wrong: $(printf '%s\n' "$out" | grep '^elapsed_s')"
awk '{ sub(/if \(\$2 == "-"\) \{ notrun\+\+; next \}/, "print $5; &"); print }' "$REPO/scripts/cost-report.sh" > "$REPO/scripts/leaky-passes.sh"
grep -q 'print \$5; if (\$2 == "-")' "$REPO/scripts/leaky-passes.sh" || fail "the leaky pass-record copy was not made — its control tests nothing"
leak=$(report scripts/leaky-passes.sh 2>&1) || true
case $leak in *"$CANARY"*) ;; *) fail "a copy whose pass-record section prints a model field did not print the canary — the audit is not armed" ;; esac

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
has read-first "bytes=$rf" ceiling=report-only
# S1 (a Read of docs/a.md) and S3 (its first call cats two listed files);
# not S2, whose read is its 11th call.
clause read-first "(obeyed in 2 of 4 sessions, by a Read/cat"
in_class read-first
has "  docs/a.md" "bytes=$(sz docs/a.md)" ceiling=10 OVER
has "  docs/decisions.md" "bytes=$(sz docs/decisions.md)" ceiling=report-only
lacks "  docs/decisions.md" OVER
# The entry key: the largest `## ` section's bytes, labelled, never added (D-169).
ent_b=$(sed -n '/^## D-001/,$p' "$REPO/docs/decisions.md" | wc -c | tr -d ' ')
has "    docs/decisions.md#*" "bytes=$ent_b" ceiling=1000
clause "    docs/decisions.md#*" "(its largest entry: each section of docs/decisions.md on its own, not added)"
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

# --- removed siblings count, existing foreign ones do not (#77) -------------------
# A lane's worktree beside the checkout (`<repo>-<label>`) is gone from git's
# list once removed. Counted: m16 under a removed `repo-gone`, m19 under a
# removed sibling of the LINKED worktree (its name holds a newline). Not
# counted: m17 under an existing `repo-plain` that is no repository, m18
# under `repoz`, the name without the dash, m20 through a `..` segment. Phase one above holds the
# existing foreign sibling (m12, `repo-old`) out with the same rule armed.
mkdir -p "$WORK/repo-plain"
SG=55555555-0000-4000-8000-000000000005
mkdir -p "$P/-repo-gone"
a m16 11000 0 1 "$SG" "$REPO-gone/sub" "$NOW" false "$M" '[]' > "$P/-repo-gone/$SG.jsonl"
a m17 12000 0 1 66666666-0000-4000-8000-000000000006 "$WORK/repo-plain" "$NOW" false "$M" '[]' > "$P/-other/plain.jsonl"
a m18 13000 0 1 77777777-0000-4000-8000-000000000007 "$WORK/repoz" "$NOW" false "$M" '[]' > "$P/-other/z.jsonl"
a m19 14000 0 1 88888888-0000-4000-8000-000000000008 "$WT-gone" "$NOW" false "$M" '[]' > "$P/-other/wtgone.jsonl"
# A `..` segment climbs out of the sibling: never counted (startup-report's rule).
a m20 15000 0 1 99999999-0000-4000-8000-000000000009 "$REPO-gone/../../elsewhere" "$NOW" false "$M" '[]' > "$P/-other/dotdot.jsonl"
out=$(report scripts/cost-report.sh 2>&1) || fail "the report exited non-zero with siblings planted:
$out"
has sessions=6 requests=15 "cache_read=$((cr + 11000 + 14000))" input=290

# A removed sibling of a LINKED worktree recorded through a symlinked parent
# (macOS: the harness records /var, git lists /private/var): counted when the
# report runs from that spelling, as git's resolved root is mapped back
# through it (Codex code pass). `alias` is a link to $WORK itself.
ln -s "$WORK" "$WORK/alias"
a m21 16000 0 1 aaaaaaaa-0000-4000-8000-00000000000a "$WORK/alias/wt
x-gone2" "$NOW" false "$M" '[]' > "$P/-other/alias.jsonl"
out=$( cd "$WORK/alias/repo" && HOME="$WORK/home" CLAUDE_PROJECTS_DIR="$P" BUDDYLIST_JOURNAL="$WORK/none.db" \
	BUDDY_LEDGER="$WORK/none-ledger.db" BUDDYLIST_BIN="$WORK/no-buddylist" BUDDY_COST_DAYS=7 sh scripts/cost-report.sh 2>&1 ) ||
	fail "the report exited non-zero through the alias:
$out"
has sessions=7 requests=16 "cache_read=$((cr + 11000 + 14000 + 16000))"

# --- two respellings of an existing foreign sibling (#77's Codex pass) -----------
# A CASE VARIANT names the same directory on macOS's default volume:
# m22 under `repo-OLD` is the existing foreign `repo-old` (phase one) and
# never counts; on a case-sensitive volume the fold over-excludes a removed
# `repo-OLD`, the residual D-002 accepts for scopes. A TRAILING NEWLINE:
# `repo-nl<newline>` exists and is no repository, so it is left out, and
# command substitution's stripped newline must not leave out the removed
# lane `repo-nl` with it (m23 counts). The control is m16, still counted.
mkdir -p "$WORK/repo-nl
"
a m22 18000 0 1 bbbbbbbb-0000-4000-8000-00000000000b "$WORK/repo-OLD/sub" "$NOW" false "$M" '[]' > "$P/-other/case.jsonl"
a m23 19000 0 1 cccccccc-0000-4000-8000-00000000000c "$WORK/repo-nl/sub" "$NOW" false "$M" '[]' > "$P/-other/nl.jsonl"
out=$(report scripts/cost-report.sh 2>&1) || fail "the report exited non-zero with the respellings planted:
$out"
has sessions=7 requests=16 "cache_read=$((cr + 11000 + 14000 + 19000))"

# --- lines that are not JSON: skipped and counted, never text, never the rest ------
# A transcript being WRITTEN ends in an unterminated line; a malformed line can sit
# mid-file. Parsed as JSON, either one was a parse error that ABANDONED the rest of
# jq's file list (measured on jq 1.7.1: rc=5, the files after it never opened), so a
# report run while a fleet is live read 2,534 of 3,024 transcripts and said only
# "FLOOR" (the token-overhead report, D-169). Now each line is parsed on its own:
# the bad ones are skipped and counted by kind, and every other line counts. The
# torn file's own first line (m24) and every file found after either one count,
# whatever order find lists them in, so the full request count is the witness.
S5=dddddddd-0000-4000-8000-00000000000d
{ a m24 21000 0 1 "$S5" "$REPO" "$NOW" false "$M" '[]'; printf '{"type":"user","message":"%s' "$CANARY"; } > "$P/-repo/aa-torn.jsonl"
printf '{"type":"user","message":"%s\n%s\n' "$CANARY" "$(a m25 22000 0 1 "$S5" "$REPO" "$NOW" false "$M" '[]')" > "$P/-repo/zz-mid.jsonl"
# A COMPLETE last record with no final newline counts like any other (m26), here
# and at an xargs boundary alike, and is no skipped line (Codex code pass).
printf '%s' "$(a m26 23000 0 1 "$S5" "$REPO" "$NOW" false "$M" '[]')" > "$P/-repo/ab-tail.jsonl"
out=$(report scripts/cost-report.sh 2>&1) || fail "the report exited non-zero on a torn transcript"
case $out in *"$CANARY"*) fail "a torn transcript line reached the output (invariant 9)" ;; esac
case $out in *"every number below is a FLOOR"*) fail "lines that are not JSON drew the FLOOR note: a parse error still abandons the rest of jq's file list
$out" ;; esac
has sessions=8 requests=19 "cache_read=$((cr + 11000 + 14000 + 19000 + 21000 + 22000 + 23000))"
has transcript_lines_not_json skipped=2 unterminated_last_line=1 elsewhere=1
# A file whose FIRST line is the bad one still counts as parsed: no "no first-line
# value" NOTE either.
has transcripts seen=18 parsed=18
# The control: with nothing torn, no skip line at all (it is not printed always).
mv "$P/-repo/aa-torn.jsonl" "$P/-repo/zz-mid.jsonl" "$P/-repo/ab-tail.jsonl" "$WORK/"
out=$(report scripts/cost-report.sh 2>&1) || fail "the report exited non-zero with the torn transcripts moved out"
case $out in *transcript_lines_not_json*) fail "a skip line printed with nothing to skip" ;; esac
has sessions=7 requests=16 "cache_read=$((cr + 11000 + 14000 + 19000))"
# jq's OWN error (a transcript it cannot open) still draws the FLOOR note, and the
# open files still count.
cp "$WORK/zz-mid.jsonl" "$P/-repo/zz-mid.jsonl"; chmod 000 "$P/-repo/zz-mid.jsonl"
out=$(report scripts/cost-report.sh 2>&1) || fail "the report exited non-zero on an unreadable transcript"
chmod 600 "$P/-repo/zz-mid.jsonl"; rm -f "$P/-repo/zz-mid.jsonl"
case $out in *"every number below is a FLOOR"*) ;; *) fail "a transcript jq could not open did not draw the FLOOR note" ;; esac
has sessions=7 requests=16 "cache_read=$((cr + 11000 + 14000 + 19000))"

# No record yet: one line that says so.
mv "$REPO/.git/codex-passes.tsv" "$WORK/passes.saved"
out=$(report scripts/cost-report.sh 2>&1) || fail "the report exited non-zero with no pass record"
printf '%s\n' "$out" | grep -qxF "Codex passes recorded: none (no codex-passes.tsv in the git common dir yet)" || fail "no pass record did not say none"

echo "check-cost-report: GREEN (codex pass record aggregated exactly, malformed rows counted and skipped, its canary never printed and a leaky copy caught; prose classes summed from the check's enumeration, a section and an entry key labelled not added, a report-only ceiling printed as one, OVER only over; lines that are not JSON skipped and counted, never costing the files after them; 13 requests deduplicated across messages, a resume and files; skill exposure, obedience and decisions reads on the planted counts; a planted canary never printed and a leaky copy caught)"
