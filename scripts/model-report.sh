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
#   tier    the THIRD anchored token, `tier=<chore|lite|full>` (D-175,
#           docs/ROUTING-PLAN.md step 2): read only after a valid model= and
#           class= pair, once (a second ` tier=` anywhere is `unknown`), a
#           literal of a CASE like the class. Why a tier axis: class x model
#           alone put the full-tier gate work in the default's cell only, so
#           "no worse" leaned toward a trial model given the easy items
#           (Fable F3). A row from an older export has no column: `-`.
#   reds    the item's own release note's FIRST word, `reds=<n>`, n 0..999
#           with no leading zero, once (a second ` reds=` is a bad one): the
#           count, or `-` when missing or bad. Every taught release note
#           begins with it (the lane's, the parked rider's, the model
#           summary's). Before D-175, 0 of 81 outcome notes carried it in
#           any one shape (ROUTING-PLAN M4), and a red never reached the
#           rework column: an in-run red keeps its claim, and a `-2` lane is
#           HANDED the claim. Only the parsed integer leaves SQL.
#   effort  the holder's recorded effort on the same session_context row as
#           its model: low medium high xhigh max as literals, `unrecorded`
#           with none, `other` otherwise. Exported, not a cell axis: the cap
#           names a model, and the report prints it as one summary line.
#   protected
#           1 when a scope of this claim, or of a SHARED claim the same
#           session incarnation held over an overlapping window (a
#           companion), overlaps the protected list below in EITHER
#           direction, under the stored fold (invariant 13) and containment
#           (invariant 14): `docs` overlaps `docs/decisions.md`, and
#           `internal/fence/x.go` overlaps `internal`. Computed at export, so
#           a swept row keeps it. It sees claims only: a lane can edit a path
#           it never claimed, which is why model-pick.md's READY diff check
#           exists (Codex P1). A shared companion handed to another session
#           with a plain `release --to` is no longer the lane's and is missed.
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
#           slug digest would join it and was not added, because a slug is
#           low-entropy and its digest reverses by dictionary, so printing
#           the digest prints the slug.
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
# COLUMNS ARE ONLY EVER APPENDED (D-175: tier, reds, effort, protected after
# the first 14), so a reader by position keeps every column it had. A file
# with the 14-column header has that one line rewritten to the 18-column
# one (through a copy that keeps its mode, then a rename) and its old rows
# kept as they are: read back, a 14-field row reads the four as missing.
# Rewriting the header, not keeping it: an old report refuses the file by
# its first line, loudly, where appending wider rows under the old header
# had it count every new row as "not in this report's rendered form".
# Every run prints its input: the path, the row count, the created dates
# and the file's sha256, so a figure quoted in a brief can be re-run against
# the same bytes (Codex P6); a ledger copy is not retained and says so.
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
#                        the cwd; cost-report.sh's knob), at schema 19
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
# A bare awk operand `reg=1.tsv` is a variable assignment and `-x` an option:
# a relative path is spelled ./ before anything reads it.
case "$export_to" in
	'' | /*) ;;
	*) export_to=./$export_to ;;
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
		# GNU first: GNU `stat -f` is --file-system, which prints the statfs
		# block on stdout AND exits 1, so `-f || -c` stamped free-space counters
		# that move with this script's own cp (orc-6's review, from the option
		# grammar; no GNU stat on the box this was written on). BSD `stat -c`
		# fails with nothing on stdout, so this order cannot mix the two.
		if [ -e "$f" ]; then stat -c '%s %.9Y' "$f" 2>/dev/null || stat -f '%z %Fm' "$f"; else echo -; fi
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
# The schemas this query was written against, refused outside them by name
# (D-037's spirit): an older ledger lacks columns it reads (set -e died on
# "no such column"), and a newer one may mean something else by them.
schema=$(echo 'PRAGMA user_version;' | q)
case "$schema" in
	19) ;;
	*) echo "model-report: the ledger is at schema $schema; this report reads schema 19 only" >&2; exit 2 ;;
esac

# One row per item claim, every field already in its rendered form, plus
# the full session id (column 17, charset-checked) and the holder's worktree
# as HEX (column 18) for the transcript lookup, neither ever printed.
#
# THE PROTECTED LIST (D-175; docs/ROUTING-PLAN.md section 5, "Protected
# paths always run on the default"). CLAUDE.md's full tier is "a change to
# what the gate refuses or allows, the ledger schema, internal/fence or an
# invariant"; an item touching any of these runs on the default lane model
# whatever its class or tier word, and the cap cannot shrink the list (F5):
#   all code      cmd internal scripts .githooks .claude .github go.mod go.sum
#                 (scripts/ whole: scripts/gate-hook.py sat outside a
#                 narrower list, Codex P1; .github and the module files are
#                 code the plan's list did not name, added as the safer side)
#   the rules     CLAUDE.md AGENTS.md
#   the skills    skills (model-pick.md, the rule itself, among them: F6)
#   loaded texts  docs/review-charter* (a PREFIX: every charter part),
#                 docs/decisions.md docs/prose-budget.tsv
# Lower-case literals, compared with the stored folded scope. pre=1 is the
# prefix row. Containment is invariant 14's, spelled with substr, never
# GLOB: a scope is peer-written, and `*` as a GLOB pattern matches all.
cat >"$work/items.sql" <<'SQL'
WITH prot(p, pre) AS (VALUES
  ('cmd', 0), ('internal', 0), ('scripts', 0), ('.githooks', 0), ('.claude', 0),
  ('.github', 0), ('go.mod', 0), ('go.sum', 0),
  ('claude.md', 0), ('agents.md', 0), ('skills', 0),
  ('docs/review-charter', 1), ('docs/decisions.md', 0), ('docs/prose-budget.tsv', 0)
),
hit AS (
  SELECT DISTINCT s.claim_id FROM claim_scopes s JOIN prot ON
    (pre = 0 AND (s.folded = p OR substr(s.folded, 1, length(p) + 1) = p || '/'))
    OR (pre = 1 AND substr(s.folded, 1, length(p)) = p)
    OR substr(p, 1, length(s.folded) + 1) = s.folded || '/'
),
a AS (
  SELECT rowid AS ord, claim_id,
    -- D-133: a parked rider is its producer's work, though a coordinator holds it.
    CASE WHEN producer_session <> '' THEN producer_session ELSE session_id END AS session_id,
    CASE WHEN producer_session <> '' THEN producer_incarnation ELSE incarnation END AS incarnation,
    slug, created, renewed, state, outcome, shared, descr, outcome_note,
    CASE WHEN state = 'open' THEN CAST(strftime('%s', 'now') AS INTEGER) ELSE renewed END AS until
  FROM claims
),
c AS (
  SELECT ord, claim_id, session_id, incarnation, slug, created, renewed, state, outcome,
    ' ' || replace(replace(replace(descr, char(9), ' '), char(10), ' '), char(13), ' ') || ' ' AS d,
    ' ' || replace(replace(replace(outcome_note, char(9), ' '), char(10), ' '), char(13), ' ') || ' ' AS nn,
    -- A NUL anywhere voids every token of that text: SQLite's text length()
    -- and replace() stop at one, so `tier=lite<NUL> tier=full` counted one
    -- ` tier=` and read lite (Codex code pass). argv cannot carry a NUL; a
    -- hand-edited ledger can.
    instr(CAST(descr AS BLOB), x'00') AS dz, instr(CAST(outcome_note AS BLOB), x'00') AS nz,
    CASE WHEN claim_id IN (SELECT claim_id FROM hit) OR EXISTS (SELECT 1 FROM a o
      WHERE o.shared = 1 AND o.claim_id <> a.claim_id AND o.session_id = a.session_id
        AND o.incarnation = a.incarnation AND o.created <= a.until AND a.created <= o.until
        AND o.claim_id IN (SELECT claim_id FROM hit)) THEN 1 ELSE 0 END AS prot
  FROM a
  WHERE slug NOT GLOB 'orc-*' AND slug <> 'orchestrator'
    AND NOT EXISTS (SELECT 1 FROM claim_scopes s WHERE s.claim_id = a.claim_id
      AND (s.folded = '.buddy' OR s.folded GLOB '.buddy/*'))
),
w AS (
  SELECT *,
    substr(d, 2, instr(substr(d, 2), ' ') - 1) AS w1,
    substr(d, 2 + instr(substr(d, 2), ' ')) AS r,
    (length(d) - length(replace(d, ' model=', ''))) / 7 AS nm,
    (length(d) - length(replace(d, ' class=', ''))) / 7 AS nc,
    (length(d) - length(replace(d, ' tier=', ''))) / 6 AS nt,
    substr(nn, 2, instr(substr(nn, 2), ' ') - 1) AS n1,
    (length(nn) - length(replace(nn, ' reds=', ''))) / 6 AS nr
  FROM c
),
t AS (
  SELECT *,
    CASE WHEN dz = 0 AND w1 GLOB 'model=?*' AND nm = 1 AND nc <= 1 THEN substr(w1, 7) END AS mtok,
    CASE WHEN dz = 0 AND w1 GLOB 'model=?*' AND nm = 1 AND nc = 1 AND substr(r, 1, instr(r, ' ') - 1) GLOB 'class=?*'
      THEN substr(r, 7, instr(r, ' ') - 7) END AS ctok,
    substr(r, instr(r, ' ') + 1) AS r2,
    (SELECT model FROM session_context x WHERE x.session_id = w.session_id
      AND x.incarnation = w.incarnation) AS m,
    (SELECT effort FROM session_context x WHERE x.session_id = w.session_id
      AND x.incarnation = w.incarnation) AS ef,
    (SELECT worktree FROM sessions y WHERE y.session_id = w.session_id) AS wt
  FROM w
),
u AS (
  SELECT *,
    -- The third token only after a valid model= and class= pair, and once;
    -- valid means a LISTED class word (`class=bogus tier=lite` is no pair, Codex).
    CASE WHEN ctok IN ('docs', 'code', 'test', 'research', 'ops') AND nt = 1 AND substr(r2, 1, instr(r2, ' ') - 1) GLOB 'tier=?*'
      THEN substr(r2, 6, instr(r2, ' ') - 6) END AS ttok,
    CASE WHEN nr = 1 AND nz = 0
      AND (n1 GLOB 'reds=[0-9]' OR n1 GLOB 'reds=[1-9][0-9]' OR n1 GLOB 'reds=[1-9][0-9][0-9]')
      THEN printf('%d', CAST(substr(n1, 6) AS INTEGER)) ELSE '-' END AS reds
  FROM t
),
k AS (
  SELECT *,
    ROW_NUMBER() OVER (PARTITION BY slug ORDER BY created, ord) AS nth,
    COUNT(*) OVER (PARTITION BY slug) AS n,
    LAG(claim_id) OVER (PARTITION BY slug ORDER BY created, ord) AS prev
  FROM u
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
  CASE ttok WHEN 'chore' THEN 'chore' WHEN 'lite' THEN 'lite' WHEN 'full' THEN 'full' ELSE 'unknown' END,
  reds,
  CASE WHEN ef IS NULL OR ef = '' THEN 'unrecorded' WHEN ef = 'low' THEN 'low' WHEN ef = 'medium' THEN 'medium'
    WHEN ef = 'high' THEN 'high' WHEN ef = 'xhigh' THEN 'xhigh' WHEN ef = 'max' THEN 'max' ELSE 'other' END,
  CASE prot WHEN 1 THEN '1' ELSE '0' END,
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
cut -f17,18 "$work/raw.tsv" | sort -u | awk -F'\t' -v OFS='\t' '
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
hdr14='claim_id	session	model	class	pick	outcome	state	created	ended	nth	slug_claims	prev_claim	requests	tool_calls'
hdr="$hdr14	tier	reds	effort	protected"
awk -F'\t' -v OFS='\t' -v now="$now" '
# FILENAME, not NR == FNR: with no transcript at all the events file is
# empty, and NR == FNR then reads every item as an event.
FILENAME == ARGV[1] { has[$1] = 1; ev[$1, ++ne[$1]] = $2 "\t" $3; next }
{
	sid = $17; hi = ($9 == "-") ? now : $9; rq = "-"; tc = "-"
	if (sid != "-" && has[sid]) {
		rq = 0; tc = 0
		for (i = 1; i <= ne[sid]; i++) {
			split(ev[sid, i], e, "\t")
			if (e[1] != "F" && e[2] >= $8 && e[2] <= hi) { if (e[1] == "R") rq++; else tc++ }
		}
	}
	print $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, rq, tc, $13, $14, $15, $16
}' "$work/events.tsv" "$work/raw.tsv" >"$work/items.tsv"

# The row checker, ONE copy for a fresh row and a row read back from a file:
# a row is reported only if every column is in its rendered form. A 14-field
# row is an older export's (before D-175): its first 14 columns are checked
# the same, and the four it lacks read as missing (`-`) where it is counted.
valid='
function ok(   i) {
	if (NF != 14 && NF != 18) return 0
	if (NF == 18) {
		if ($15 !~ /^(chore|lite|full|unknown)$/) return 0
		if ($16 != "-" && $16 !~ /^(0|[1-9][0-9]?[0-9]?)$/) return 0
		if ($17 !~ /^(low|medium|high|xhigh|max|unrecorded|other)$/) return 0
		if ($18 !~ /^[01]$/) return 0
	}
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
# A register whose last line has no newline (a hand edit, a torn write) would
# have the first appended row glued onto it: one row of 31 fields, both lost
# and the old id still reserved by the dedupe (Codex code pass). End it first.
endline() { [ ! -s "$1" ] || [ -z "$(tail -c 1 "$1")" ] || printf '\n' >>"$1"; }
rows="$work/items.tsv"
added=0
# The receipts register (REVIEW LAYERS, below) is refused HERE, before the
# item register is written: refused after it, a foreign `<tsv>.receipts`
# left the items appended and the run exiting 2 (Codex code pass).
rhdr='commit	committed	tier	layer	kind	counts	raised	fixed	answered	reproduced'
if [ -n "$export_to" ] && [ -e "$export_to.receipts" ] && [ "$(head -n 1 "$export_to.receipts")" != "$rhdr" ]; then
	echo "model-report: $export_to.receipts exists and its first line is not this report's receipts header; refusing to append to it" >&2
	exit 2
fi
upgraded=
if [ -n "$export_to" ]; then
	if [ -e "$export_to" ]; then
		first=$(head -n 1 "$export_to")
		if [ "$first" = "$hdr14" ]; then
			# An export written before D-175: its header gains the four
			# columns, its rows stay as written (the header comment says why).
			# The copy keeps the file's mode; the rename replaces it whole.
			tmp="$export_to.d175.$$"
			cp -p "$export_to" "$tmp"
			{ printf '%s\n' "$hdr"; tail -n +2 "$export_to"; } >"$tmp"
			mv -f "$tmp" "$export_to"
			upgraded=1
		elif [ "$first" != "$hdr" ]; then
			echo "model-report: the --export file exists and its first line is not this report's header; refusing to append to it" >&2
			exit 2
		fi
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
	endline "$export_to"
	cat "$work/new.tsv" >>"$export_to"
	source="register --export names"
	rows=$export_to
fi
hflag=$( [ -n "$export_to" ] && echo 1 || echo 0)

# The input, identified (Codex P6): its rows, the span of their created
# epochs, and the file's sha256 after this run's append. Rows are every line
# under the header, valid or not, because the digest is over all of them.
sha256() {
	if command -v sha256sum >/dev/null 2>&1; then sha256sum <"$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then shasum -a 256 <"$1" | cut -d' ' -f1
	else echo NOT-MEASURED; fi
}
day() { [ -n "$1" ] && jq -rn --argjson e "$1" '$e | todate | .[0:10]' || echo -; }
span=$(LC_ALL=C awk -F'\t' -v header="$hflag" 'FNR == 1 && header { next }
	{ n++ } $8 ~ /^[0-9]+$/ { v = $8 + 0; if (lo == "" || v < lo) lo = v; if (v > hi) hi = v }
	END { print n + 0, lo, hi }' "$rows")
set -- $span
inrows=$1 lo=$(day "${2:-}") hi=$(day "${3:-}")
if [ -n "$export_to" ]; then
	# The path is the operator's argv, printed on one line all the same.
	input="$(printf '%s' "$export_to" | LC_ALL=C tr -c '[:print:]' '?'), $inrows row(s), created $lo..$hi UTC, sha256 $(sha256 "$export_to")"
else
	input="the ledger copy (not retained: --export <tsv> keeps one), $inrows row(s), created $lo..$hi UTC, sha256 -"
fi

echo "Model report: per class x tier x model, over the $source"
echo "Counts and ids only: no description, slug, label or note is printed. The model is the holder's recorded one."
[ -z "$export_to" ] || echo "export: $added new closed item(s) appended"
[ -z "$upgraded" ] || echo "export: the header gained the tier, reds, effort and protected columns; its older rows read them as missing (-)"
echo "input: $input"
[ "$malformed_ids" = 0 ] || echo "left out: $malformed_ids claim(s) whose id is not hex"

LC_ALL=C awk -F'\t' -v floor="$floor" -v header="$hflag" "$valid"'
FNR == 1 && header { next }
!ok() { bad++; next }
seen[$1]++ { dup++; next }
{
	# A 14-field row is an older export'"'"'s: the four D-175 columns are missing.
	T = "-"; R = "-"; E = "-"; X = "-"
	if (NF == 18) { T = $15; R = $16; E = $17; X = $18 }
	n++; k = $4 SUBSEP T SUBSEP $3; items[k]++; mod[$3] = 1; tiers[T]++; eff[E]++
	if ($7 == "open") open++; else { closed[k]++; if ($6 == "none") noout[k]++ }
	if ($6 != "none") { outc[k]++; nout++; if ($6 == "fail") fail[k]++; if (R == "-") nored++ }
	if (R != "-") { rn[k]++; rs[k] += R }
	if (X == "1") { prot[k]++; np++ } else if (X == "0") up[k]++; else npu++
	if ($13 != "-") { tr[k]++; req[k] += $13; tool[k] += $14 }
	if ($12 != "-" && $12 != "malformed") redone[$12] = 1
	rowk[$1] = k; pick[$5]++
	if ($4 == "unknown") unk++
}
function pct(a, b) { return b ? sprintf("%d/%d %3d%%", a, b, int(100 * a / b + 0.5)) : sprintf("%d/%d    -", a, b) }
END {
	for (id in rowk) if (id in redone) rw[rowk[id]]++
	printf "items: %d (%d open), %d with no class token or a bad one (`unknown`)\n", n, open, unk
	printf "tier= token: %d chore, %d lite, %d full, %d missing, repeated or unlisted (`unknown`), %d older export row(s) without it (`-`)\n", tiers["chore"], tiers["lite"], tiers["full"], tiers["unknown"], tiers["-"]
	printf "model= token against the recorded model: %d same, %d differs, %d none or repeated, %d with no model recorded\n", pick["same"], pick["differs"], pick["none"], pick["unchecked"]
	printf "effort, the holder'"'"'s recorded: %d low, %d medium, %d high, %d xhigh, %d max, %d unrecorded, %d other, %d older export row(s) without it\n", eff["low"], eff["medium"], eff["high"], eff["xhigh"], eff["max"], eff["unrecorded"], eff["other"], eff["-"]
	printf "outcomes without reds=: %d of %d\n", nored, nout
	printf "protected: %d item(s) whose claim, or a shared claim beside it, touches the protected list; %d older export row(s) unknown\n", np, npu
	printf "pick: none printed; this report reads no cap\n"
	if (bad) printf "left out: %d row(s) that are not in this report'"'"'s rendered form\n", bad
	if (dup) printf "left out: %d repeated claim id(s)\n", dup
	if (!n) exit
	w = 5; for (m in mod) if (length(m) > w) w = length(m)
	printf "\n%-8s  %-7s  %-*s  %5s  %4s  %-14s  %-14s  %-14s  %-14s  %-14s  %14s  %10s\n", "class", "tier", w, "model", "items", "prot", "outcome", "no-outcome", "fail", "reworked", "reds/item", "requests/item", "tools/item"
	nc = split("docs code test research ops unknown", order, " ")
	nt = split("chore lite full unknown -", tord, " ")
	nm = 0; for (m in mod) mods[++nm] = m
	for (i = 1; i < nm; i++) for (j = i + 1; j <= nm; j++) if (mods[j] < mods[i]) { t = mods[i]; mods[i] = mods[j]; mods[j] = t }
	for (c = 1; c <= nc; c++) for (y = 1; y <= nt; y++) for (i = 1; i <= nm; i++) {
		k = order[c] SUBSEP tord[y] SUBSEP mods[i]
		if (!items[k]) continue
		printf "%-8s  %-7s  %-*s  %5d  %4d  %-14s  %-14s  %-14s  %-14s  %-14s  %14s  %10s\n", order[c], tord[y], w, mods[i], items[k], prot[k],
			pct(outc[k], items[k]), pct(noout[k], closed[k]), pct(fail[k], outc[k]), pct(rw[k], items[k]),
			rn[k] ? sprintf("%.2f (n=%d)", rs[k] / rn[k], rn[k]) : "- (n=0)",
			tr[k] ? sprintf("%.0f (n=%d)", req[k] / tr[k], tr[k]) : "- (n=0)",
			tr[k] ? sprintf("%.0f", tool[k] / tr[k]) : "-"
		if (up[k] < floor) under[++nu] = sprintf("below the floor: %s x %s x %s has %d unprotected item(s), under %d; no inference", order[c], tord[y], mods[i], up[k], floor)
	}
	if (nu) printf "\n"
	for (i = 1; i <= nu; i++) print under[i]
	printf "\noutcome: items released with --outcome; no-outcome: closed items released without one; fail: of the outcomes; reworked: items whose slug was claimed again after them.\n"
	printf "prot: items on the protected list, never in a trial'"'"'s cell; reds/item: over the items whose note begins reds=<n> (n); the floor counts unprotected items.\n"
}' "$rows"

# REVIEW LAYERS (#78, D-141): what each review layer FOUND, from the commits'
# receipts. A receipt said which layers ran and never what one raised, so no
# one could say which layer earns its cost (subagents and Codex passes are the
# largest cost a fleet can turn down: 29% of Claude units and 49 Codex passes
# in one week to 2026-11-09). The grammar, taught in the skills: a layer may
# end in a count group, `<layer>: <n> raised, <n> fixed, <n> answered, <n>
# reproduced`, any subset, each word once; `reproduced` is a finding a test
# was watched to fail on. A count NOT WRITTEN is NOT RECORDED, never zero: no
# receipt before this one carries a group, and reading those as "0 raised"
# would tell the tier tuning every layer found nothing. A group that starts
# with a digit and breaks the grammar is MALFORMED: counted, read as not
# recorded. Prose after a colon (`self-review: the matcher …`, on old
# receipts) is no group.
#
# The reader is scripts/receipts.awk (D-143), shared with receipt-check.sh so
# the report and the check never read one receipt two ways: the last line
# beginning `review:`, the subject line never read, the tier its first word,
# the layers its first parenthesis up to a depth-0 `;`, each layer's kind a
# literal (self, panel, design, code, cross-check, reverify, other). Only
# literals, counts, the commit id and its epoch leave it. The tier, not the
# claim's class=, is the join: a commit names no claim, and the squash at
# landing makes a commit the ledger never saw.
#
# Over HEAD's history, or with --export over `<tsv>.receipts`, appended to
# like the item register (a row per layer, every commit once, read back as
# DATA under the same checks as a fresh row).
rvalid='
function rok(   i) {
	if (NF != 10) return 0
	if ((length($1) != 40 && length($1) != 64) || $1 ~ /[^0-9a-f]/) return 0
	if ($2 !~ /^[0-9]+$/ || $4 !~ /^[0-9]+$/) return 0
	if ($3 !~ /^(lite|full|chore|other)$/) return 0
	if ($5 !~ /^(self|panel|design|code|cross-check|reverify|other)$/) return 0
	if ($6 !~ /^(recorded|none|malformed)$/) return 0
	for (i = 7; i <= 10; i++) if ($i !~ /^([0-9]+|-)$/) return 0
	if ($6 != "recorded" && ($7 != "-" || $8 != "-" || $9 != "-" || $10 != "-")) return 0
	if ($6 == "recorded" && $7 == "-" && $8 == "-" && $9 == "-" && $10 == "-") return 0
	return 1
}'
: >"$work/receipts.tsv"
if git -C "$PWD" rev-parse -q --verify HEAD >/dev/null 2>&1; then
	rsrc="receipts on HEAD"
	# The record separator is a NUL git prints (%x00): a body's own 0x01
	# byte is blanked first, so a body cannot forge a commit's header line
	# (Codex code pass: `\001<40 hex> 1` in a body made a second record).
	git -C "$PWD" log --no-color --format='%x00%H %ct%n%B' HEAD 2>/dev/null | tr '\001' ' ' | tr '\000' '\001' |
		LC_ALL=C awk -v OFS='\t' -f "$(dirname -- "$0")/receipts.awk" >"$work/receipts.tsv"
else
	rsrc="receipts NOT MEASURED: not a git checkout"
fi
rrows="$work/receipts.tsv"
radded=0
if [ -n "$export_to" ]; then
	reg="$export_to.receipts"
	# Its header was checked before the item register was written (above).
	[ -e "$reg" ] || printf '%s\n' "$rhdr" >"$reg"
	# Every commit once: one already in the register adds no layer.
	awk -F'\t' -v OFS='\t' "$rvalid"'
		FILENAME == ARGV[1] { if (FNR > 1) done[$1] = 1; next }
		!done[$1] && rok() { print }
	' "$reg" "$work/receipts.tsv" >"$work/rnew.tsv"
	radded=$(awk -F'\t' '!s[$1]++ { n++ } END { print n + 0 }' "$work/rnew.tsv")
	endline "$reg"
	cat "$work/rnew.tsv" >>"$reg"
	rsrc="receipts register beside the export"
	rrows=$reg
fi

echo
echo "Review layers: per tier x layer, over the $rsrc"
[ -z "$export_to" ] || echo "export: $radded new commit receipt(s) appended"
LC_ALL=C awk -F'\t' -v header="$hflag" "$rvalid"'
FNR == 1 && header { next }
!rok() { bad++; next }
seen[$1, $4]++ { dup++; next }
{
	if (!c[$1]++) nc++
	nl++; k = $3 SUBSEP $5; L[k]++
	if ($6 == "recorded") { rec[k]++; nrec++ } else if ($6 == "malformed") nmal++
	for (i = 7; i <= 10; i++) if ($i != "-") { sum[k, i] += $i; n[k, i]++ }
}
function col(k, i) { return n[k, i] ? sprintf("%d (n=%d)", sum[k, i], n[k, i]) : "- (n=0)" }
END {
	printf "receipts: %d commit(s), %d layer(s); counts recorded on %d, malformed on %d (read as not recorded, never as zero)\n", nc, nl, nrec, nmal
	if (bad) printf "left out: %d receipt row(s) that are not in this report'"'"'s rendered form\n", bad
	if (dup) printf "left out: %d repeated receipt layer(s)\n", dup
	if (!nl) exit
	printf "\n%-6s  %-11s  %6s  %8s  %-12s  %-12s  %-12s  %s\n", "tier", "layer", "layers", "recorded", "raised", "fixed", "answered", "reproduced"
	nt = split("lite full chore other", T, " "); nk = split("self panel design code cross-check reverify other", K, " ")
	for (a = 1; a <= nt; a++) for (b = 1; b <= nk; b++) {
		k = T[a] SUBSEP K[b]
		if (!L[k]) continue
		printf "%-6s  %-11s  %6d  %8d  %-12s  %-12s  %-12s  %s\n", T[a], K[b], L[k], rec[k], col(k, 7), col(k, 8), col(k, 9), col(k, 10)
	}
	printf "\nrecorded: layers whose receipt wrote a count group; a count is summed over the layers that wrote it (n), never read as zero where none was written.\n"
}' "$rrows"
