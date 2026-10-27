#!/bin/sh
# model-report.sh — which model finishes which class of item, read as RATES
# per class x model from the ledger's item claims. Counts and ids only: no
# description, slug, label, note, message or transcript text is ever printed.
#
#   sh scripts/model-report.sh [--export <tsv>]
#
# WHY IT EXISTS (docs/BEYOND-CODE-PLAN.md 5.2(a), step 4). The orchestrator
# picks a lane's model by hand, and every orchestrator on 2026-10-26 copied
# the last one's pick. The pick rule (5.2(b)) needs an observed rate to apply,
# and the ledger had the facts scattered: the model on session_context, the
# outcome on the claim, the rework in the slug's history, the turns in a
# transcript. This joins them, and --export keeps them past the sweep.
#
# THE UNIT IS THE ITEM CLAIM, NEVER THE SESSION (Codex finding 2). A session
# holding two items sends two READYs that look exactly like one item reworked
# (5.1), and the split by model across sessions is a split by role: every
# orchestrator ran Opus, every probe Haiku. An item claim is any claim that
# is not coordination: no scope under `.buddy/` (a slot, an `orc-<project>`
# claim) and a slug that is neither `orc-*` nor the legacy `orchestrator`.
# The scope test reads the FOLDED spelling (D-013), so `.BUDDY/slot/x` is a
# slot too (Codex). A `--shared` companion claim is an item by that rule and
# lands, with no tokens, in the `unknown` class.
#
# WHAT A ROW CARRIES, per item claim:
#   class   the `class=<word>` token of 5.2(b)'s grammar: the description
#           begins `model=<id> class=<word> `, two anchored tokens in that
#           order, each at most once: a second ` model=` or ` class=` word
#           anywhere breaks the grammar, and both tokens read as absent
#           (`model=m class=code model=n` is `unknown`, Codex). <word> must be one of
#           docs code test research ops; a missing, repeated or unlisted
#           token is `unknown`, counted and never printed. The word reaches
#           the output as a LITERAL of a SQL CASE, never as the bytes read.
#   model   the holder session's RECORDED model (session_context, the string
#           the roster renders, D-020's register), never the description's
#           token: `unrecorded` with no context row, `other` when it is not
#           1-64 bytes of [A-Za-z0-9._-]. The row is the session's LAST turn
#           (one row per session), so a session that switched model shows
#           its last; and it must be the claim's own INCARNATION's, so a
#           revived session's claim never takes the model of the session it
#           revived (Codex): `unrecorded` until the new one records.
#   pick    the `model=` token against that recorded model: `same`,
#           `differs`, `none` (missing or repeated), or `unchecked` (no
#           model recorded). Compared inside SQL;
#           the token itself never leaves it.
#   outcome the claim's own `--outcome` (D-049): pass fail aborted, or none.
#           An item's outcome is its own release (Codex finding 12); a run
#           claim's outcome is about the run and is not read here.
#   nth, slug_claims, prev_claim
#           the rework count: a re-claim after a RED is a NEW claim id with
#           the same slug. nth is this claim's place among the copy's item
#           claims of its slug (by created, then rowid: a fail and its
#           re-claim can share a second, and the id is random, so ordering
#           by it put the re-claim first in about half the fixture's runs),
#           slug_claims how many
#           there are, prev_claim the id of the one before it (`-` for the
#           first). An item is REWORKED when another row names it as its
#           prev_claim: the rework is charged to the claim whose work was
#           redone, not to the one that redid it. The chain is by claim id,
#           so it joins across exports; a slug is never written. It is cut
#           where the sweep removed the earlier claim before the re-claim was
#           made (a released claim lives 24 hours), so a re-claim more than a
#           day after the previous release starts a new chain (Codex): a
#           slug digest would join it, and was not added, since invariant 9's
#           list of what this may print names no derived value.
#   requests, tool_calls
#           the holder's main-thread requests (deduplicated by requestId,
#           else message.id; a `<synthetic>` record is not one) and tool_use
#           blocks (by id) timed from the claim's creation to its release
#           (now, for an open one), from `<session id>.jsonl` under the
#           Claude projects directory: the main checkout's project dir, its
#           `-…` worktree dirs, and the dir named after the holder's own
#           worktree. `-` where no transcript exists: 68 of 147 sessions had
#           none on 2026-10-26, so the denominator is printed beside the mean.
#           The holder is the claim's CURRENT owner: after `release --to`, the
#           giver's turns are not counted.
#
# INVARIANT 9 IS THE WHOLE DESIGN. Every rendered field is a count, an epoch,
# a claim id (hex, checked in SQL), an `s-<8hex>` session form (checked in
# SQL; `s-unknown` otherwise), the recorded model under the charset above, or
# a literal. A peer-written string never leaves sqlite3, so a newline or a
# tab in it cannot fabricate a row in the report or the export.
# check-model-report.sh plants them and fails on any one reaching either.
#
# --export <tsv> APPENDS every CLOSED item (released or orphaned) whose claim
# id the file does not already hold, then reports over the whole file. Why
# the export exists: a plain `buddy sweep` deletes released claims older than
# 24 hours with their descriptions and outcomes (SweepTTL; Codex finding 3),
# so the ledger never accumulates; the orchestrator runs this before each
# sweep (the skill's duty line). Why closed only: an open claim's outcome and
# counts are not final, a row is never rewritten, and an open claim survives
# the sweep to be exported once released. No default path: the orchestrator
# names the file. Read back, the file is DATA: a row is reported only if
# every field passes the same checks a fresh row does, and the rest are
# counted, never printed. Two exports racing on one file are not serialized.
#
# THE COPY (D-072's rule, the way `buddy diagnose` reads): the ledger and its
# WAL are copied into a private 0700 directory, retried while either one's
# size or mtime moves across the copy or either copy's bytes differ from the
# source after it, and SQLite only ever opens the copy. Not a snapshot: a
# write made and reverted inside that window is not seen, and the copied WAL
# is folded into the copy, never into the source.
# Never a buddy verb (one run under a registered id writes last_seen, D-086,
# and store.Open would migrate an older ledger) and never sqlite3 on the
# source (it creates -shm beside the operator's ledger).
#
# KNOBS
#   BUDDY_LEDGER         the ledger (default: <git common dir>/buddy.db of
#                        the cwd; cost-report.sh's knob)
#   BUDDY_MODEL_FLOOR    items a cell needs before its rates mean anything
#                        (default 10, the operator's number, 5.2(b))
#   CLAUDE_PROJECTS_DIR  where transcripts live (default ~/.claude/projects)
set -eu

usage() { echo "usage: model-report.sh [--export <tsv>]" >&2; exit 2; }
export_to=
case $# in
	0) ;;
	2) [ "$1" = --export ] && [ -n "$2" ] || usage; export_to=$2 ;;
	*) usage ;;
esac

floor=${BUDDY_MODEL_FLOOR:-10}
case "$floor" in
	'' | *[!0-9]* | 0*) echo "BUDDY_MODEL_FLOOR must be a positive integer" >&2; exit 2 ;;
esac
for c in sqlite3 jq awk; do
	command -v "$c" >/dev/null 2>&1 || { echo "model-report: missing required command: $c" >&2; exit 2; }
done
projects=${CLAUDE_PROJECTS_DIR:-"$HOME/.claude/projects"}

if [ -n "${BUDDY_LEDGER:-}" ]; then
	ledger=$BUDDY_LEDGER
else
	# The git COMMON dir, as cost-report.sh: in a linked worktree `.git` is
	# a file, and every worktree shares the one ledger.
	common=$(git -C "$PWD" rev-parse --git-common-dir 2>/dev/null) || {
		echo "model-report: not in a git repository; set BUDDY_LEDGER=<git-common-dir>/buddy.db" >&2
		exit 2
	}
	case "$common" in
		/*) ledger="$common/buddy.db" ;;
		*) ledger="$PWD/$common/buddy.db" ;;
	esac
fi
[ -f "$ledger" ] || { echo "model-report: no ledger at $ledger" >&2; exit 2; }

work=$(mktemp -d "${TMPDIR:-/tmp}/buddy-model.XXXXXX") || exit 1
trap 'rm -rf "$work"' EXIT INT TERM
chmod 700 "$work"

stamp() { # size and mtime of the ledger and its WAL, or "-" for an absent one
	for f in "$ledger" "$ledger-wal"; do
		if [ -e "$f" ]; then stat -f '%z %Fm' "$f" 2>/dev/null || stat -c '%s %.9Y' "$f"; else echo -; fi
	done
}
tries=0
while :; do
	before=$(stamp)
	rm -f "$work/l.db" "$work/l.db-wal"
	cp "$ledger" "$work/l.db"
	[ ! -e "$ledger-wal" ] || cp "$ledger-wal" "$work/l.db-wal"
	# The stamp, then the bytes: a copy that differs from the source as it
	# is now was torn. Size and mtime alone miss a same-size write whose
	# mtime was restored (Codex); a byte compare narrows the window to a
	# change made and reverted between the copy and the compare.
	if [ "$(stamp)" = "$before" ] && cmp -s "$ledger" "$work/l.db" &&
		{ [ ! -e "$ledger-wal" ] || cmp -s "$ledger-wal" "$work/l.db-wal"; }; then
		break
	fi
	tries=$((tries + 1))
	[ "$tries" -lt 5 ] || { echo "model-report: the ledger kept changing across 5 copies; run it again" >&2; exit 1; }
done
# Fold the copied WAL into the copy (this writes the PRIVATE copy only), then
# read it immutable: no lock, no -shm, nothing the read can change.
# Relative to the private dir, so no byte of $TMPDIR has to survive a URI.
(cd "$work" && sqlite3 l.db 'PRAGMA wal_checkpoint(TRUNCATE); PRAGMA journal_mode=DELETE;') >/dev/null
q() { (cd "$work" && sqlite3 -batch -noheader -separator '	' 'file:l.db?mode=ro&immutable=1'); }
[ "$(echo 'PRAGMA quick_check(1);' | q)" = ok ] || { echo "model-report: the ledger copy failed quick_check" >&2; exit 1; }

# One row per item claim, every field already in its rendered form, plus
# the full session id (column 13, charset-checked) and the holder's worktree
# as HEX (column 14) for the transcript lookup, neither ever printed.
cat >"$work/items.sql" <<'SQL'
WITH c AS (
  SELECT rowid AS ord, claim_id, session_id, incarnation, slug, created, renewed, state, outcome,
    ' ' || replace(replace(replace(descr, char(9), ' '), char(10), ' '), char(13), ' ') || ' ' AS d
  FROM claims
  WHERE slug NOT GLOB 'orc-*' AND slug <> 'orchestrator'
    AND NOT EXISTS (SELECT 1 FROM claim_scopes s WHERE s.claim_id = claims.claim_id
      AND (s.folded = '.buddy' OR s.folded GLOB '.buddy/*'))
),
w AS (
  SELECT *,
    substr(d, 2, instr(substr(d, 2), ' ') - 1) AS w1,
    substr(d, 2 + instr(substr(d, 2), ' ')) AS r,
    (length(d) - length(replace(d, ' model=', ''))) / 7 AS nm,
    (length(d) - length(replace(d, ' class=', ''))) / 7 AS nc
  FROM c
),
t AS (
  SELECT *,
    CASE WHEN w1 GLOB 'model=?*' AND nm = 1 AND nc <= 1 THEN substr(w1, 7) END AS mtok,
    CASE WHEN w1 GLOB 'model=?*' AND nm = 1 AND nc = 1 AND substr(r, 1, instr(r, ' ') - 1) GLOB 'class=?*'
      THEN substr(r, 7, instr(r, ' ') - 7) END AS ctok,
    (SELECT model FROM session_context x WHERE x.session_id = w.session_id
      AND x.incarnation = w.incarnation) AS m,
    (SELECT worktree FROM sessions y WHERE y.session_id = w.session_id) AS wt
  FROM w
),
k AS (
  SELECT *,
    ROW_NUMBER() OVER (PARTITION BY slug ORDER BY created, ord) AS nth,
    COUNT(*) OVER (PARTITION BY slug) AS n,
    LAG(claim_id) OVER (PARTITION BY slug ORDER BY created, ord) AS prev
  FROM t
)
SELECT
  claim_id,
  CASE WHEN instr(CAST(session_id AS BLOB), x'00') = 0 AND substr(session_id, 1, 8) GLOB '[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]'
    THEN 's-' || substr(session_id, 1, 8) ELSE 's-unknown' END,
  CASE WHEN m IS NULL OR m = '' THEN 'unrecorded'
    WHEN length(CAST(m AS BLOB)) <= 64 AND instr(CAST(m AS BLOB), x'00') = 0
      AND m NOT GLOB '*[^A-Za-z0-9._-]*' THEN m
    ELSE 'other' END,
  CASE ctok WHEN 'docs' THEN 'docs' WHEN 'code' THEN 'code' WHEN 'test' THEN 'test'
    WHEN 'research' THEN 'research' WHEN 'ops' THEN 'ops' ELSE 'unknown' END,
  CASE WHEN mtok IS NULL THEN 'none' WHEN m IS NULL OR m = '' THEN 'unchecked'
    WHEN mtok = m THEN 'same' ELSE 'differs' END,
  CASE outcome WHEN 'pass' THEN 'pass' WHEN 'fail' THEN 'fail' WHEN 'aborted' THEN 'aborted' ELSE 'none' END,
  CASE state WHEN 'open' THEN 'open' WHEN 'released' THEN 'released' WHEN 'orphaned' THEN 'orphaned' ELSE 'other' END,
  printf('%d', created),
  CASE WHEN state = 'open' THEN '-' ELSE printf('%d', renewed) END,
  nth, n,
  CASE WHEN prev IS NULL THEN '-'
    WHEN length(CAST(prev AS BLOB)) BETWEEN 8 AND 36 AND instr(CAST(prev AS BLOB), x'00') = 0
      AND prev NOT GLOB '*[^0-9a-f-]*' THEN prev ELSE 'malformed' END,
  CASE WHEN length(CAST(session_id AS BLOB)) BETWEEN 8 AND 64 AND instr(CAST(session_id AS BLOB), x'00') = 0
    AND session_id NOT GLOB '*[^0-9a-f-]*' THEN session_id ELSE '-' END,
  CASE WHEN wt GLOB '/?*' THEN hex(CAST(wt AS BLOB)) ELSE '-' END
FROM k
WHERE length(CAST(claim_id AS BLOB)) BETWEEN 8 AND 36 AND instr(CAST(claim_id AS BLOB), x'00') = 0
  AND claim_id NOT GLOB '*[^0-9a-f-]*'
ORDER BY created, ord;
SQL
# Every charset test above also refuses a NUL: SQLite's GLOB and length()
# stop at one and the CLI prints up to it, so `m<NUL><TAB>x` passed as `m`
# (Codex; measured on 3.54.0). The worktree is the one value here that is
# not pre-rendered: it is a path, needed to name the harness's project directory, so it leaves SQL as hex (a
# tab or newline in a raw path would split the row, and a split row is a
# forged one) and is turned into that name by enc() below, byte by byte.
q <"$work/items.sql" >"$work/raw.tsv"
malformed_ids=$(echo "SELECT count(*) FROM claims WHERE NOT (length(CAST(claim_id AS BLOB)) BETWEEN 8 AND 36
  AND instr(CAST(claim_id AS BLOB), x'00') = 0 AND claim_id NOT GLOB '*[^0-9a-f-]*');" | q)

# The harness names a project's directory from its absolute path with every
# UTF-16 code unit outside [A-Za-z0-9] turned into '-' (wake-report.sh).
encode() { jq -rn --arg p "$1" '$p | gsub("[\\x{10000}-\\x{10FFFF}]"; "--") | gsub("[^A-Za-z0-9]"; "-")'; }
main=
case "$ledger" in
	*/.git/buddy.db) main=$(encode "${ledger%/.git/buddy.db}") ;;
esac

# One jq pass per holder transcript, streaming line by line (fromjson?, so a
# torn line costs the line), printing only fixed letters and epochs:
#   R <epoch>   a main-thread request, at its FIRST record's time
#   T <epoch>   a main-thread tool_use block, never one on a <synthetic> record
#   F 0         printed by the loop for every transcript FOUND: a transcript
#               with nothing in it is 0 requests, not "no transcript" (Codex)
# shellcheck disable=SC2016
prog='
def epoch: (sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601);
reduce (inputs | fromjson? | select(type == "object" and .type == "assistant"
    and .isSidechain != true and (.timestamp | type) == "string")) as $e (
  {r: {}, t: {}, out: []};
  ($e.timestamp | try epoch catch null) as $ts
  | if $ts == null or $e.message.model == "<synthetic>" then . else
    ($e.requestId // $e.message.id // null) as $id
    | (if $id == null or $e.message.usage == null or .r[$id | tostring] then .
       else .r[$id | tostring] = true | .out += ["R\t\($ts | floor)"] end)
    | reduce (($e.message.content // []) | if type == "array" then .[] else empty end
        | select(type == "object" and .type == "tool_use" and (.id | type) == "string")) as $u (.;
        if .t[$u.id] then . else .t[$u.id] = true | .out += ["T\t\($ts | floor)"] end)
  end)
| .out[]'
: >"$work/events.tsv"
# enc: the harness's directory name for a path given as hex, by its rule: an
# ASCII letter or digit is kept, any other ASCII byte is one '-', a 2- or
# 3-byte UTF-8 sequence (one UTF-16 unit) one '-', a 4-byte one two.
cut -f13,14 "$work/raw.tsv" | sort -u | awk -F'\t' -v OFS='\t' '
function enc(h,   i, b, o, c) {
	o = ""
	for (i = 1; i < length(h); i += 2) {
		b = index("0123456789ABCDEF", substr(h, i, 1)) * 16 + index("0123456789ABCDEF", substr(h, i + 1, 1)) - 17
		if (b < 128) { c = sprintf("%c", b); o = o (c ~ /[A-Za-z0-9]/ ? c : "-") }
		else if (b >= 240) o = o "--"
		else if (b >= 192) o = o "-"
	}
	return o
}
$1 != "-" { print $1, ($2 == "-" ? "-" : enc($2)) }' | while IFS='	' read -r sid wt; do
	f=
	for dir in ${main:+"$projects/$main"} ${main:+"$projects/$main"-*} "$projects/$wt"; do
		[ "$dir" != "$projects/-" ] || continue
		[ -f "$dir/$sid.jsonl" ] && { f="$dir/$sid.jsonl"; break; }
	done
	[ -n "$f" ] || continue
	printf '%s\tF\t0\n' "$sid"
	jq -n -R -r "$prog" "$f" 2>/dev/null | sed "s/^/$sid	/" || true
done >>"$work/events.tsv"

now=$(date +%s)
# items.tsv: the export's columns. requests/tool_calls in the claim's window.
hdr='claim_id	session	model	class	pick	outcome	state	created	ended	nth	slug_claims	prev_claim	requests	tool_calls'
awk -F'\t' -v OFS='\t' -v now="$now" '
# FILENAME, not NR == FNR: with no transcript at all the events file is
# empty, and NR == FNR then reads every item as an event.
FILENAME == ARGV[1] { has[$1] = 1; ev[$1, ++ne[$1]] = $2 "\t" $3; next }
{
	sid = $13; hi = ($9 == "-") ? now : $9; rq = "-"; tc = "-"
	if (sid != "-" && has[sid]) {
		rq = 0; tc = 0
		for (i = 1; i <= ne[sid]; i++) {
			split(ev[sid, i], e, "\t")
			if (e[1] != "F" && e[2] >= $8 && e[2] <= hi) { if (e[1] == "R") rq++; else tc++ }
		}
	}
	print $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, rq, tc
}' "$work/events.tsv" "$work/raw.tsv" >"$work/items.tsv"

# The row checker, ONE copy for a fresh row and a row read back from a file:
# a row is reported only if every column is in its rendered form.
valid='
function ok(   i) {
	if (NF != 14) return 0
	if ($1 !~ /^[0-9a-f-]+$/ || length($1) < 8 || length($1) > 36) return 0
	if ($2 !~ /^s-([0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]|unknown)$/) return 0
	if ($3 !~ /^[A-Za-z0-9._-]+$/ || length($3) > 64) return 0
	if ($4 !~ /^(docs|code|test|research|ops|unknown)$/) return 0
	if ($5 !~ /^(same|differs|none|unchecked)$/) return 0
	if ($6 !~ /^(pass|fail|aborted|none)$/) return 0
	if ($7 !~ /^(open|released|orphaned|other)$/) return 0
	if ($8 !~ /^[0-9]+$/ || $9 !~ /^([0-9]+|-)$/) return 0
	if ($10 !~ /^[0-9]+$/ || $11 !~ /^[0-9]+$/) return 0
	if ($12 != "-" && $12 != "malformed" && ($12 !~ /^[0-9a-f-]+$/ || length($12) < 8 || length($12) > 36)) return 0
	if ($13 !~ /^([0-9]+|-)$/ || $14 !~ /^([0-9]+|-)$/) return 0
	return 1
}'

source="ledger copy"
rows="$work/items.tsv"
added=0
if [ -n "$export_to" ]; then
	if [ -e "$export_to" ]; then
		[ "$(head -n 1 "$export_to")" = "$hdr" ] || {
			echo "model-report: the --export file exists and its first line is not this report's header; refusing to append to it" >&2
			exit 2
		}
	else
		printf '%s\n' "$hdr" >"$export_to"
	fi
	# Dedupe on the first column of every line that has one, valid or not:
	# a row that fails the checks must still not be appended twice.
	awk -F'\t' -v OFS='\t' "$valid"'
		FILENAME == ARGV[1] { if (FNR > 1) done[$1] = 1; next }
		($7 == "released" || $7 == "orphaned") && !done[$1]++ && ok() { print }
	' "$export_to" "$work/items.tsv" >"$work/new.tsv"
	added=$(awk 'END { print NR }' "$work/new.tsv")
	cat "$work/new.tsv" >>"$export_to"
	source="register --export names"
	rows=$export_to
fi

echo "Model report: per class x model, over the $source"
echo "Counts and ids only: no description, slug, label or note is printed. The model is the holder's recorded one."
[ -z "$export_to" ] || echo "export: $added new closed item(s) appended"
[ "$malformed_ids" = 0 ] || echo "left out: $malformed_ids claim(s) whose id is not hex"

LC_ALL=C awk -F'\t' -v floor="$floor" -v header="$( [ -n "$export_to" ] && echo 1 || echo 0)" "$valid"'
FNR == 1 && header { next }
!ok() { bad++; next }
seen[$1]++ { dup++; next }
{
	n++; k = $4 SUBSEP $3; items[k]++; cls[$4] = 1; mod[$3] = 1
	if ($7 == "open") open++
	if ($6 != "none") { outc[k]++; if ($6 == "fail") fail[k]++ }
	if ($13 != "-") { tr[k]++; req[k] += $13; tool[k] += $14 }
	if ($12 != "-" && $12 != "malformed") redone[$12] = 1
	rowk[$1] = k; pick[$5]++
	if ($4 == "unknown") unk++
}
function pct(a, b) { return b ? sprintf("%d/%d %3d%%", a, b, int(100 * a / b + 0.5)) : sprintf("%d/%d    -", a, b) }
END {
	for (id in rowk) if (id in redone) rw[rowk[id]]++
	printf "items: %d (%d open), %d with no class token or a bad one (`unknown`)\n", n, open, unk
	printf "model= token against the recorded model: %d same, %d differs, %d none or repeated, %d with no model recorded\n", pick["same"], pick["differs"], pick["none"], pick["unchecked"]
	if (bad) printf "left out: %d row(s) that are not in this report'"'"'s rendered form\n", bad
	if (dup) printf "left out: %d repeated claim id(s)\n", dup
	if (!n) exit
	w = 5; for (m in mod) if (length(m) > w) w = length(m)
	printf "\n%-8s  %-*s  %5s  %-14s  %-14s  %-14s  %14s  %10s\n", "class", w, "model", "items", "outcome", "fail", "reworked", "requests/item", "tools/item"
	nc = split("docs code test research ops unknown", order, " ")
	nm = 0; for (m in mod) mods[++nm] = m
	for (i = 1; i < nm; i++) for (j = i + 1; j <= nm; j++) if (mods[j] < mods[i]) { t = mods[i]; mods[i] = mods[j]; mods[j] = t }
	for (c = 1; c <= nc; c++) for (i = 1; i <= nm; i++) {
		k = order[c] SUBSEP mods[i]
		if (!items[k]) continue
		printf "%-8s  %-*s  %5d  %-14s  %-14s  %-14s  %14s  %10s\n", order[c], w, mods[i], items[k],
			pct(outc[k], items[k]), pct(fail[k], outc[k]), pct(rw[k], items[k]),
			tr[k] ? sprintf("%.0f (n=%d)", req[k] / tr[k], tr[k]) : "- (n=0)",
			tr[k] ? sprintf("%.0f", tool[k] / tr[k]) : "-"
		if (items[k] < floor) under[++nu] = sprintf("below the floor: %s x %s has %d item(s), under %d; no inference", order[c], mods[i], items[k], floor)
	}
	if (nu) printf "\n"
	for (i = 1; i <= nu; i++) print under[i]
	printf "\noutcome: items released with --outcome; fail: of those; reworked: items whose slug was claimed again after them.\n"
}' "$rows"
