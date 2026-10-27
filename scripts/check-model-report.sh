#!/bin/sh
# check-model-report.sh — done-check for scripts/model-report.sh
# (docs/BEYOND-CODE-PLAN.md 5.2(a), step 4).
#
# HERMETIC: sh, git, go (to build buddy), sqlite3, jq. The ledger is a
# throwaway repo's, written by the REAL buddy binary (hello, beat, claim,
# release), so the fixture has the shapes the verbs store and not the ones a
# hand-written INSERT would guess at; the transcripts sit in a throwaway
# CLAUDE_PROJECTS_DIR. Nothing under ~/.claude or this checkout's ledger is
# read. Without sqlite3 or jq this says NOT RUN, as check-wake-report.sh does.
#
# What must hold, each with the control that proves the assertion could fire:
#   - INVARIANT 9, the whole design: a label with a raw newline, a 600-byte
#     label, slugs, a release note, descriptions with a raw newline and tab,
#     a 600-byte one, an unlisted class word, and a recorded model holding a
#     tab are planted, and NOT ONE planted byte reaches the report or the
#     export. The markers are first found in the fixture ledger (the control
#     that they were planted), and a copy of the report that prints the slugs
#     must FAIL the same audit (the control that the audit is armed);
#   - a description cannot forge an export row: one carries a newline and a
#     whole valid-looking row with its own claim id, and that id never
#     appears; every export row is 14 fields in the rendered form, checked
#     here by a pattern spelled independently of the script's own;
#   - every report line has one of the shapes the report prints, so a later
#     "helpful" column fails here even without a planted byte in it;
#   - the grammar: valid, a repeated class token, an unlisted word, no tokens,
#     a repeated model token; the model is the RECORDED one, `unrecorded`
#     with no context row and `other` for one outside the charset;
#   - the unit is the claim: a session holding seven items is seven rows, a
#     re-claim of a slug after a fail is a second item and charges the
#     rework to the first; coordination claims (a slot, an orc- claim) are
#     not items;
#   - requests and tool calls are counted inside the claim's window only,
#     deduplicated, main thread only, never a <synthetic> record, from the
#     main checkout's project dir AND from a linked worktree's own (a path
#     with a space and an e-acute, spelled here by hand);
#   - the rates against the fixture's known counts, and BUDDY_MODEL_FLOOR;
#   - --export: the closed items only, the second run appends nothing and
#     leaves the file byte-identical, a newly closed item is appended once,
#     and no claim id is ever written twice; a file with a foreign header is
#     refused, and a hand-planted row is DATA: counted, never printed.
set -eu
. "$(dirname -- "$0")/lib.sh"
ROOT=$(repo_root "$0")
CDPATH= cd -- "$ROOT"

fail() { echo "check-model-report: FAIL — $*" >&2; exit 1; }

for c in sqlite3 jq; do
	command -v "$c" >/dev/null 2>&1 || { echo "check-model-report: NOT RUN — no $c on PATH" >&2; exit 0; }
done

# Physical, so the path the hooks store and the one the projects dir is named
# from are one spelling (macOS /var is a link to /private/var).
WORK=$(mktemp -d) || fail "mktemp failed"
WORK=$(CDPATH= cd -- "$WORK" && pwd -P)
trap 'rm -rf "$WORK"' EXIT INT TERM
go build -o "$WORK/bin/buddy" ./cmd/buddy || fail "could not build buddy"
unset CLAUDE_CODE_SESSION_ID BUDDY_SESSION BUDDY_LEDGER BUDDY_MODEL_FLOOR || true

REPO="$WORK/repo"
mkrepo "$REPO"
printf 'x\n' >"$REPO/README.md"
git -C "$REPO" add -A
git -C "$REPO" commit -q -m init
WT="$WORK/wt é"
git -C "$REPO" worktree add -q --detach "$WT"
B() { ( cd "$REPO" && "$WORK/bin/buddy" "$@" ); }
as() { s=$1; shift; ( cd "$REPO" && CLAUDE_CODE_SESSION_ID=$s "$WORK/bin/buddy" "$@" ) >/dev/null; }
B init >/dev/null

SA=aaaaaaaa-0000-4000-8000-00000000000a
SB=bbbbbbbb-0000-4000-8000-00000000000b
SC=cccccccc-0000-4000-8000-00000000000c
SD=dddddddd-0000-4000-8000-00000000000d
SE=eeeeeeee-0000-4000-8000-00000000000e
P="$WORK/projects"
# The harness's names, by hand: every byte outside [A-Za-z0-9] of an ASCII
# path is one '-', and " é" is two: the space and the one UTF-16 unit.
PA="$P/$(printf '%s' "$REPO" | LC_ALL=C sed 's/[^A-Za-z0-9]/-/g')"
PB="$P/$(printf '%s' "$WORK" | LC_ALL=C sed 's/[^A-Za-z0-9]/-/g')-wt--"
mkdir -p "$PA" "$PB" "$WORK/tx"
TA="$PA/$SA.jsonl" TB="$PB/$SB.jsonl" TD="$WORK/tx/$SD.jsonl" TE="$WORK/tx/$SE.jsonl"

at() { jq -rn --argjson s "$1" 'now + $s | floor | todate'; }
# req <time> <request-id> <model> [content-json]
req() { printf '{"type":"assistant","timestamp":"%s","requestId":"%s","message":{"model":"%s","usage":{"input_tokens":3,"cache_read_input_tokens":1,"cache_creation_input_tokens":2,"output_tokens":4}%s}}\n' "$1" "$2" "$3" "${4:+,\"content\":$4}"; }
hello() { printf '{"session_id":"%s","cwd":"%s"}' "$1" "$2" | B hello --label "$3" >/dev/null; }
beat() { printf '{"session_id":"%s","cwd":"%s","transcript_path":"%s","tool_name":"Bash","tool_input":{}}' "$1" "$2" "$3" | B beat >/dev/null; }

long=$(printf 'MARKLONG%.0s' $(seq 75)) # 600 bytes
hello "$SA" "$REPO" "$(printf 'MARKLABEL\nfake row')"
hello "$SB" "$WT" "$long"
hello "$SC" "$REPO" carol
hello "$SD" "$REPO" dave
hello "$SE" "$REPO" erin
# Two hours before any claim: the model each beat records, and a request
# that is outside every claim's window.
req "$(at -7200)" rA0 claude-opus-5-5 >"$TA"
req "$(at -7200)" rB0 claude-fable-5-1 >"$TB"
# D's model holds a NUL and then a tab: SQLite's GLOB stops at the NUL.
req "$(at -7200)" rD0 "$(printf 'x\\u0000\\tMARKMODEL')" >"$TD"
req "$(at -7200)" rE0 claude-opus-5-5 >"$TE"
beat "$SA" "$REPO" "$TA"
beat "$SB" "$WT" "$TB"
beat "$SD" "$REPO" "$TD"
beat "$SE" "$REPO" "$TE"
# E ends and is revived: a new incarnation, its context row still the old
# one's, so its claim's model is not recorded (Codex).
printf '{"session_id":"%s","cwd":"%s"}' "$SE" "$REPO" | B bye >/dev/null
hello "$SE" "$REPO" erin
# C has a transcript with nothing in it: 0 requests, never "-".
: >"$PA/$SC.jsonl"

forged="deadbeefdeadbeef	s-deadbeef	claude-opus-5-5	code	same	pass	released	1	2	1	1	-	9	9"
as "$SA" claim orc-fixture --desc "MARKORC" --scope .buddy/orc-fixture
as "$SA" claim run1-MARKSLUG --desc "model=claude-opus-5-5 class=code MARKSLOT" --scope .buddy/slot/main
# Coordination by slug alone, and a slot spelled in another case (Codex).
as "$SA" claim orc-MARKSLUG --desc "model=claude-opus-5-5 class=code" --scope notes/orc
as "$SA" claim orchestrator --desc "model=claude-opus-5-5 class=code" --scope notes/orch
as "$SA" claim run2-MARKSLUG --desc "model=claude-opus-5-5 class=code" --scope .BUDDY/slot/two
as "$SA" claim a1-MARKSLUG --desc "model=claude-opus-5-5 class=code fix MARKDESC" --scope a1
as "$SB" claim b1-MARKSLUG --desc "$(printf 'model=claude-fable-5-1 class=docs MARKNL\n%s\tMARKTAB' "$forged")" --scope b1
# Inside a1's and b1's windows. A: two requests (rA1 on two records), three
# tool calls (t2 twice), and a <synthetic> and a sidechain record, each
# with a tool_use, that are neither. B: four requests, two tool calls.
now=$(at 0)
{
	req "$now" rA1 claude-opus-5-5
	req "$now" rA1 claude-opus-5-5
	req "$now" rA2 claude-opus-5-5 '[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"MARKCMD"}},{"type":"tool_use","id":"t2","name":"Read","input":{}}]'
	req "$now" rA2 claude-opus-5-5 '[{"type":"tool_use","id":"t2","name":"Read","input":{}},{"type":"tool_use","id":"t3","name":"Edit","input":{}}]'
	printf '{"type":"assistant","timestamp":"%s","message":{"id":"syn","model":"<synthetic>","usage":{"input_tokens":0},"content":[{"type":"tool_use","id":"t7","name":"Bash","input":{}}]}}\n' "$now"
	printf '{"type":"assistant","isSidechain":true,"timestamp":"%s","requestId":"side","message":{"model":"m","usage":{"input_tokens":1},"content":[{"type":"tool_use","id":"t9","name":"Bash","input":{}}]}}\n' "$now"
	echo '{"type":"assistant","timest'
	req "$(at 7200)" rA9 claude-opus-5-5 '[{"type":"tool_use","id":"t8","name":"Bash","input":{}}]'
} >>"$TA"
{
	req "$now" rB1 claude-fable-5-1 '[{"type":"tool_use","id":"u1","name":"Bash","input":{}},{"type":"tool_use","id":"u2","name":"Bash","input":{}}]'
	req "$now" rB2 claude-fable-5-1
	req "$now" rB3 claude-fable-5-1
	req "$now" rB4 claude-fable-5-1
	req "$(at 7200)" rB9 claude-fable-5-1
} >>"$TB"
as "$SA" release a1-MARKSLUG --outcome pass --note MARKNOTE
as "$SB" release b1-MARKSLUG --outcome pass
# Every later claim starts a second after those requests, so none counts them.
sleep 1
as "$SA" claim a2-MARKSLUG --desc "model=claude-opus-5-5 class=code first try" --scope a2
as "$SA" release a2-MARKSLUG --outcome fail
as "$SA" claim a2-MARKSLUG --desc "model=claude-opus-5-5 class=code second try" --scope a2
as "$SA" release a2-MARKSLUG --outcome pass
as "$SA" claim a3-MARKSLUG --desc "model=claude-opus-5-5 class=docs class=code MARKREP" --scope a3
as "$SA" claim a4-MARKSLUG --desc "model=claude-opus-5-5 class=MARKWORD rest" --scope a4
as "$SA" claim a5-MARKSLUG --desc "MARKMISS no tokens" --scope a5
as "$SA" claim a6-MARKSLUG --desc "model=claude-fable-5-1 class=test MARKOPEN" --scope a6
as "$SA" claim a7-MARKSLUG --desc "model=claude-opus-5-5 class=ops model=claude-opus-5-5" --scope a7
as "$SA" release a3-MARKSLUG a4-MARKSLUG a5-MARKSLUG a7-MARKSLUG
as "$SB" claim b2-MARKSLUG --desc "model=claude-fable-5-1 class=docs $long" --scope b2
as "$SB" release b2-MARKSLUG --outcome fail
as "$SC" claim c1-MARKSLUG --desc "model=claude-opus-5-5 class=ops" --scope c1
as "$SC" release c1-MARKSLUG --outcome aborted
as "$SD" claim d1-MARKSLUG --desc "model=x class=research" --scope d1
as "$SD" release d1-MARKSLUG
as "$SE" claim e1-MARKSLUG --desc "model=claude-opus-5-5 class=research" --scope e1
as "$SE" release e1-MARKSLUG

LEDGER="$REPO/.git/buddy.db"
MARKS="MARKLABEL MARKLONG MARKSLUG MARKORC MARKSLOT MARKDESC MARKNL MARKTAB MARKNOTE MARKREP MARKWORD MARKMISS MARKOPEN MARKMODEL MARKCMD deadbeefdeadbeef"
# The planting control: every marker IS in the ledger or a transcript, so its
# absence from the output below is the filter's doing, not the fixture's.
for m in $MARKS; do
	sqlite3 "$LEDGER" "SELECT label FROM sessions; SELECT slug, descr, outcome_note FROM claims; SELECT model FROM session_context;" 2>/dev/null |
		cat - "$TA" "$TD" | grep -q "$m" || fail "the planting control failed: $m is not in the fixture"
done
[ "$(sqlite3 "$LEDGER" "SELECT count(*) FROM session_context WHERE instr(CAST(model AS BLOB), x'0009') > 0
	AND instr(CAST(model AS BLOB), CAST('MARKMODEL' AS BLOB)) > 0")" = 1 ] ||
	fail "the planting control failed: the recorded model holding a NUL, a tab and a marker was not stored"
[ "$(sqlite3 "$LEDGER" "SELECT count(*) FROM claims c JOIN session_context x ON x.session_id = c.session_id
	WHERE c.session_id = '$SE' AND c.incarnation <> x.incarnation")" = 1 ] ||
	fail "the planting control failed: E's claim and its context row share an incarnation"
[ "$(sqlite3 "$LEDGER" "SELECT count(*) FROM claim_scopes WHERE scope = '.BUDDY/slot/two'")" = 1 ] ||
	fail "the planting control failed: the slot spelled .BUDDY was not stored as spelled"

# Every report runs with a sqlite3 first on PATH that logs where it ran and
# its arguments, then runs the real one: the witness that SQLite only ever
# opened the private copy (the -shm test it replaced could not fire on an
# immutable open of the source, Codex).
real_sqlite=$(command -v sqlite3)
mkdir -p "$WORK/shim"
printf '#!/bin/sh\nprintf "%%s|%%s\\n" "$PWD" "$*" >>"%s"\nexec "%s" "$@"\n' "$WORK/sqlite.log" "$real_sqlite" >"$WORK/shim/sqlite3"
chmod +x "$WORK/shim/sqlite3"
report() { ( cd "$REPO" && PATH="$WORK/shim:$PATH" CLAUDE_PROJECTS_DIR="$P" sh "$1" ${2:+--export "$2"} ) 2>&1; }

# audit <file>... — no planted byte, and (for a report) every line a shape the
# report prints. The shapes are spelled here, not copied from the script.
row='^[0-9a-f]{16}	s-[0-9a-f]{8}	[A-Za-z0-9._-]{1,64}	(docs|code|test|research|ops|unknown)	(same|differs|none|unchecked)	(pass|fail|aborted|none)	(released|orphaned)	[0-9]+	[0-9]+	[0-9]+	[0-9]+	([0-9a-f]{16}|-)	([0-9]+|-)	([0-9]+|-)$'
hdr='claim_id	session	model	class	pick	outcome	state	created	ended	nth	slug_claims	prev_claim	requests	tool_calls'
audit() {
	for m in $MARKS; do
		if grep -q "$m" "$@"; then echo "planted $m reached the output"; return 1; fi
	done
	return 0
}
shapes() {
	LC_ALL=C grep -Ev \
		-e '^Model report: per class x model, over the (ledger copy|register --export names)$' \
		-e '^Counts and ids only: no description, slug, label or note is printed\. The model is the holder.s recorded one\.$' \
		-e '^export: [0-9]+ new closed item\(s\) appended$' \
		-e '^items: [0-9]+ \([0-9]+ open\), [0-9]+ with no class token or a bad one \(`unknown`\)$' \
		-e '^model= token against the recorded model: [0-9]+ same, [0-9]+ differs, [0-9]+ none or repeated, [0-9]+ with no model recorded$' \
		-e '^left out: [0-9]+ (row\(s\) that are not in this report.s rendered form|repeated claim id\(s\)|claim\(s\) whose id is not hex)$' \
		-e '^class +model +items +outcome +fail +reworked +requests/item +tools/item$' \
		-e '^(docs|code|test|research|ops|unknown) +[A-Za-z0-9._-]{1,64} +[0-9]+ +[0-9]+/[0-9]+ +([0-9]+%|-) +[0-9]+/[0-9]+ +([0-9]+%|-) +[0-9]+/[0-9]+ +([0-9]+%|-) +([0-9]+ \(n=[0-9]+\)|- \(n=0\)) +([0-9]+|-)$' \
		-e '^below the floor: (docs|code|test|research|ops|unknown) x [A-Za-z0-9._-]{1,64} has [0-9]+ item\(s\), under [0-9]+; no inference$' \
		-e '^outcome: items released with --outcome; fail: of those; reworked: items whose slug was claimed again after them\.$' \
		-e '^$' "$1" || true
}

out="$WORK/report.txt"
report "$ROOT/scripts/model-report.sh" >"$out" || fail "the report exited non-zero:
$(cat "$out")"
why=$(audit "$out") || fail "$why in the report:
$(cat "$out")"
odd=$(shapes "$out")
[ -z "$odd" ] || fail "the report printed a line of no known shape:
$odd"

want() { # want <file> <ERE> <what>
	grep -Eq "$2" "$1" || fail "$3:
$(cat "$1")"
}
want "$out" '^items: 13 \(1 open\), 4 with no class token' "13 items (no slot, orc- or orchestrator claim among them), 1 open, 4 unknown"
want "$out" '^model= token against the recorded model: 6 same, 2 differs, 3 none or repeated, 2 with no model recorded$' "the model= tokens"
want "$out" '^code +claude-opus-5-5 +3 +3/3 +100% +1/3 +33% +1/3 +33% +1 \(n=3\) +1$' \
	"code x opus: a1, a2 twice; one fail; the first a2 reworked; 2+0+0 requests, 3+0+0 tools"
want "$out" '^docs +claude-fable-5-1 +2 +2/2 +100% +1/2 +50% +0/2 +0% +2 \(n=2\) +1$' \
	"docs x fable: b1 (newline, forged row) and b2 (600 bytes), read from the worktree's project dir"
want "$out" '^test +claude-opus-5-5 +1 +0/1 +0% +0/0 +- +0/1 +0% +0 \(n=1\) +0$' \
	"test x opus: a6, open, its model the RECORDED one and not its token's"
want "$out" '^unknown +claude-opus-5-5 +4 +0/4 +0% +0/0 +- +0/4 +0% +0 \(n=4\) +0$' \
	"unknown x opus: a repeated class, an unlisted word, no tokens, a model token after the class"
want "$out" '^ops +unrecorded +1 +1/1 +100% +0/1 +0% +0/1 +0% +0 \(n=1\) +0$' "ops x unrecorded: no context row, an empty transcript"
want "$out" '^research +other +1 +0/1 +0% +0/0 +- +0/1 +0% +- \(n=0\) +-$' "research x other: a model with a NUL and a tab"
want "$out" '^research +unrecorded +1 +0/1 +0% +0/0 +- +0/1 +0% +- \(n=0\) +-$' "research x unrecorded: a revived session's claim"
[ "$(grep -c '^below the floor: ' "$out")" = 7 ] || fail "want a floor line for each of the 7 cells under 10:
$(cat "$out")"
fl=$( cd "$REPO" && BUDDY_MODEL_FLOOR=3 CLAUDE_PROJECTS_DIR="$P" sh "$ROOT/scripts/model-report.sh" 2>&1 )
printf '%s\n' "$fl" | grep -q '^below the floor: code x' && fail "BUDDY_MODEL_FLOOR=3: a 3-item cell is not under the floor:
$fl"
[ "$(printf '%s\n' "$fl" | grep -c '^below the floor: ')" = 5 ] || fail "BUDDY_MODEL_FLOOR=3: want 5 floor lines:
$fl"

# The audit's control: the same report with the slugs printed must fail it.
cp scripts/model-report.sh "$WORK/leaky.sh"
echo "echo 'SELECT slug FROM claims;' | q" >>"$WORK/leaky.sh"
report "$WORK/leaky.sh" >"$WORK/leaky.txt" || fail "the leaky control exited non-zero"
audit "$WORK/leaky.txt" >/dev/null && fail "the audit's control failed: a report that prints the slugs passed it"
[ -n "$(shapes "$WORK/leaky.txt")" ] || fail "the shape gate's control failed: a report that prints the slugs passed it"

# --export, twice: the closed items, then nothing.
EXP="$WORK/register.tsv"
report "$ROOT/scripts/model-report.sh" "$EXP" >"$WORK/e1.txt" || fail "the first export exited non-zero:
$(cat "$WORK/e1.txt")"
want "$WORK/e1.txt" '^export: 12 new closed item\(s\) appended$' "the first export: the 12 closed items, not the open one"
want "$WORK/e1.txt" '^Model report: per class x model, over the register --export names$' "an export reports over the register"
want "$WORK/e1.txt" '^items: 12 \(0 open\)' "the register holds no open item"
[ "$(head -n 1 "$EXP")" = "$hdr" ] || fail "the register's header is not the columns"
bad=$(sed 1d "$EXP" | LC_ALL=C grep -Ev "$row" || true)
[ -z "$bad" ] || fail "an export row is not in the rendered form:
$bad"
cp "$EXP" "$WORK/first.tsv"
report "$ROOT/scripts/model-report.sh" "$EXP" >"$WORK/e2.txt" || fail "the second export exited non-zero"
want "$WORK/e2.txt" '^export: 0 new closed item\(s\) appended$' "the second export appended something"
cmp -s "$EXP" "$WORK/first.tsv" || fail "the second export changed the register"
for f in "$EXP" "$WORK/e1.txt" "$WORK/e2.txt"; do
	why=$(audit "$f") || fail "$why in $(basename -- "$f")"
	odd=$(shapes "$f")
	[ "$f" = "$EXP" ] || [ -z "$odd" ] || fail "$(basename -- "$f") printed a line of no known shape:
$odd"
done
# Per item, exactly: the report's mean rounds, so one extra request (a
# <synthetic> record, a sidechain one) moved no cell; the register's row does.
# cell <class> <outcome> <nth> <slug_claims> -> requests and tool calls
cell() { awk -F'\t' -v c="$1" -v o="$2" -v n="$3" -v k="$4" '$4 == c && $6 == o && $10 == n && $11 == k { print $13 "/" $14 }' "$EXP"; }
[ "$(cell code pass 1 1)" = 2/3 ] || fail "a1: want 2 requests and 3 tool calls in its window, got $(cell code pass 1 1)"
[ "$(cell code fail 1 2)" = 0/0 ] || fail "a2: want nothing in its window, got $(cell code fail 1 2)"
[ "$(cell code pass 2 2)" = 0/0 ] || fail "a2 again: want nothing in its window, got $(cell code pass 2 2)"
[ "$(cell docs pass 1 1)" = 4/2 ] || fail "b1: want 4 requests and 2 tool calls, from the worktree's dir, got $(cell docs pass 1 1)"
[ "$(cell ops aborted 1 1)" = 0/0 ] || fail "c1: an empty transcript is 0, never '-', got $(cell ops aborted 1 1)"
[ "$(cell research none 1 1 | sort | tr '\n' ' ')" = "-/- -/- " ] || fail "d1, e1: no transcript is '-', never 0"
# The rework chain survives the export: the second a2 names the first.
first_a2=$(awk -F'\t' '$4 == "code" && $6 == "fail" { print $1 }' "$EXP")
[ "$(awk -F'\t' -v id="$first_a2" '$12 == id' "$EXP" | wc -l | tr -d ' ')" = 1 ] ||
	fail "the re-claim of a2 does not name the first claim as its prev_claim"
# A newly closed item is appended once.
as "$SA" release a6-MARKSLUG
report "$ROOT/scripts/model-report.sh" "$EXP" >"$WORK/e3.txt" || fail "the third export exited non-zero"
want "$WORK/e3.txt" '^export: 1 new closed item\(s\) appended$' "the released a6 was not appended once"
dups=$(sed 1d "$EXP" | cut -f1 | sort | uniq -d)
[ -z "$dups" ] || fail "a claim id was exported twice: $dups"
[ "$(sed 1d "$EXP" | wc -l | tr -d ' ')" = 13 ] || fail "want 13 rows in the register"

# Read back, the register is DATA: a planted line is counted and never shown,
# and a planted copy of an exported row is counted as repeated.
{ printf 'MARKPLANT\tline\n'; sed -n 2p "$EXP"; } >>"$EXP"
report "$ROOT/scripts/model-report.sh" "$EXP" >"$WORK/e4.txt" || fail "the export over a planted register exited non-zero"
grep -q MARKPLANT "$WORK/e4.txt" && fail "a planted register line reached the report"
want "$WORK/e4.txt" '^left out: 1 row\(s\) that are not' "the planted register line was not counted"
want "$WORK/e4.txt" '^left out: 1 repeated claim id' "the planted repeat was not counted"
want "$WORK/e4.txt" '^export: 0 new closed item' "the planted register drew an append"

printf 'some other file\n' >"$WORK/foreign.tsv"
if report "$ROOT/scripts/model-report.sh" "$WORK/foreign.tsv" >/dev/null; then fail "a file with a foreign header was appended to"; fi
[ "$(cat "$WORK/foreign.tsv")" = "some other file" ] || fail "a refused export changed the file"

# Refusals, each with its reason.
for f in 0 x -1 07; do
	if ( cd "$REPO" && BUDDY_MODEL_FLOOR=$f sh "$ROOT/scripts/model-report.sh" ) >/dev/null 2>&1; then
		fail "BUDDY_MODEL_FLOOR='$f' was accepted"
	fi
done
for a in --export "--export x y" --bogus; do
	# shellcheck disable=SC2086
	if ( cd "$REPO" && sh "$ROOT/scripts/model-report.sh" $a ) >/dev/null 2>&1; then fail "arguments '$a' were accepted"; fi
done
# The source is never opened by SQLite: every logged run was inside a private
# buddy-model.* directory and named no path outside it. The control: the log
# saw the runs at all, and the leaky copy's extra query is among them.
[ -s "$WORK/sqlite.log" ] || fail "the sqlite3 witness logged nothing: the shim is not on the report's PATH"
strays=$(grep -v '/buddy-model\.[^/|]*|' "$WORK/sqlite.log" || true)
[ -z "$strays" ] || fail "SQLite ran outside the private copy's directory:
$strays"
named=$(grep -F "$REPO" "$WORK/sqlite.log" || true)
[ -z "$named" ] || fail "a SQLite run named a path in the fixture repo:
$named"
cp "$ROOT/scripts/model-report.sh" "$WORK/opener.sh"
echo 'sqlite3 "file:$ledger?mode=ro&immutable=1" "SELECT 1;" >/dev/null' >>"$WORK/opener.sh"
: >"$WORK/sqlite.log"
report "$WORK/opener.sh" >/dev/null || fail "the opener control exited non-zero"
[ -n "$(grep -v '/buddy-model\.[^/|]*|' "$WORK/sqlite.log")" ] ||
	fail "the witness's control failed: a report that opens the source passed it"

echo "check-model-report: ok"
