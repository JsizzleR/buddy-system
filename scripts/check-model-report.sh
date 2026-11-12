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
#     refused, and a hand-planted row is DATA: counted, never printed; a
#     relative path that looks like an awk assignment or an option is a path;
#   - a ledger at another schema is refused naming its version, and the same
#     copy at 16 is read (the control);
#   - REVIEW LAYERS (#78), over planted commits: a layer's count group read
#     per kind; a count not written is `- (n=0)`, never 0, beside one that
#     WROTE 0; the scope note after `;` and a subject are never read; the
#     last receipt line wins; a repeated word, a word spelled out and a
#     misspelt one are MALFORMED, read as not recorded; a canary in a layer
#     name and a scope note never reaches the report or the receipts
#     register; --export appends every commit once, leaves the register
#     byte-identical the second time, counts a planted row and refuses a
#     foreign header.
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
SF=ffffffff-0000-4000-8000-00000000000f
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
# Three more fail-then-reclaim pairs, each inside one second as a2's is: the
# claim ids are random, so a tiebreak on them orders one pair right half the
# time and these four together 1 time in 16 (orc-6's review). F has no
# context row and no transcript: docs x unrecorded.
hello "$SF" "$REPO" frank
for i in 1 2 3; do
	as "$SF" claim "f$i-MARKSLUG" --desc "model=claude-opus-5-5 class=docs try" --scope "f$i"
	as "$SF" release "f$i-MARKSLUG" --outcome fail
	as "$SF" claim "f$i-MARKSLUG" --desc "model=claude-opus-5-5 class=docs again" --scope "f$i"
	as "$SF" release "f$i-MARKSLUG" --outcome pass
done

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
# Stdin is /dev/null: an awk left with no file operand reads stdin, so a
# regression there must fail here, not hang (the ./ mutant hung 45 min).
report() { ( cd "$REPO" && PATH="$WORK/shim:$PATH" CLAUDE_PROJECTS_DIR="$P" sh "$1" ${2:+--export "$2"} ) </dev/null 2>&1; }

# audit <file>... — no planted byte, and (for a report) every line a shape the
# report prints. The shapes are spelled here, not copied from the script.
row='^[0-9a-f]{16}	s-[0-9a-f]{8}	[A-Za-z0-9._-]{1,64}	(docs|code|test|research|ops|unknown)	(same|differs|none|unchecked)	(pass|fail|aborted|none)	(released|orphaned)	[0-9]+	[0-9]+	[0-9]+	[0-9]+	([0-9a-f]{16}|-)	([0-9]+|-)	([0-9]+|-)	(chore|lite|full|unknown)	([0-9]{1,3}|-)	(low|medium|high|xhigh|max|unrecorded|other)	[01]$'
hdr14='claim_id	session	model	class	pick	outcome	state	created	ended	nth	slug_claims	prev_claim	requests	tool_calls'
hdr="$hdr14	tier	reds	effort	protected"
audit() {
	for m in $MARKS; do
		if grep -q "$m" "$@"; then echo "planted $m reached the output"; return 1; fi
	done
	return 0
}
shapes() {
	LC_ALL=C grep -Ev \
		-e '^Model report: per class x tier x model, over the (ledger copy|register --export names)$' \
		-e '^Counts and ids only: no description, slug, label or note is printed\. The model is the holder.s recorded one\.$' \
		-e '^export: [0-9]+ new closed item\(s\) appended$' \
		-e '^export: the header gained the tier, reds, effort and protected columns; its older rows read them as missing \(-\)$' \
		-e '^input: (the ledger copy \(not retained: --export <tsv> keeps one\)|[[:print:]]+), [0-9]+ row\(s\), created ([0-9]{4}-[0-9]{2}-[0-9]{2}|-)\.\.([0-9]{4}-[0-9]{2}-[0-9]{2}|-) UTC, sha256 ([0-9a-f]{64}|-)$' \
		-e '^items: [0-9]+ \([0-9]+ open\), [0-9]+ with no class token or a bad one \(`unknown`\)$' \
		-e '^tier= token: [0-9]+ chore, [0-9]+ lite, [0-9]+ full, [0-9]+ missing, repeated or unlisted \(`unknown`\), [0-9]+ older export row\(s\) without it \(`-`\)$' \
		-e '^model= token against the recorded model: [0-9]+ same, [0-9]+ differs, [0-9]+ none or repeated, [0-9]+ with no model recorded$' \
		-e '^effort, the holder.s recorded: [0-9]+ low, [0-9]+ medium, [0-9]+ high, [0-9]+ xhigh, [0-9]+ max, [0-9]+ unrecorded, [0-9]+ other, [0-9]+ older export row\(s\) without it$' \
		-e '^outcomes without reds=: [0-9]+ of [0-9]+$' \
		-e '^protected: [0-9]+ item\(s\) whose claim, or a shared claim beside it, touches the protected list; [0-9]+ older export row\(s\) unknown$' \
		-e '^pick: none printed; this report reads no cap$' \
		-e '^left out: [0-9]+ (row\(s\) that are not in this report.s rendered form|repeated claim id\(s\)|claim\(s\) whose id is not hex)$' \
		-e '^class +tier +model +items +prot +outcome +no-outcome +fail +reworked +reds/item +requests/item +tools/item$' \
		-e '^(docs|code|test|research|ops|unknown) +(chore|lite|full|unknown|-) +[A-Za-z0-9._-]{1,64} +[0-9]+ +[0-9]+( +[0-9]+/[0-9]+ +([0-9]+%|-)){4} +([0-9]+\.[0-9][0-9] \(n=[0-9]+\)|- \(n=0\)) +([0-9]+ \(n=[0-9]+\)|- \(n=0\)) +([0-9]+|-)$' \
		-e '^below the floor: (docs|code|test|research|ops|unknown) x (chore|lite|full|unknown|-) x [A-Za-z0-9._-]{1,64} has [0-9]+ unprotected item\(s\), under [0-9]+; no inference$' \
		-e '^outcome: items released with --outcome; no-outcome: closed items released without one; fail: of the outcomes; reworked: items whose slug was claimed again after them\.$' \
		-e '^prot: items on the protected list, never in a trial.s cell; reds/item: over the items whose note begins reds=<n> \(n\); the floor counts unprotected items\.$' \
		-e '^Review layers: per tier x layer, over the (receipts on HEAD|receipts register beside the export|receipts NOT MEASURED: not a git checkout)$' \
		-e '^export: [0-9]+ new commit receipt\(s\) appended$' \
		-e '^receipts: [0-9]+ commit\(s\), [0-9]+ layer\(s\); counts recorded on [0-9]+, malformed on [0-9]+ \(read as not recorded, never as zero\)$' \
		-e '^left out: [0-9]+ (receipt row\(s\) that are not in this report.s rendered form|repeated receipt layer\(s\))$' \
		-e '^tier +layer +layers +recorded +raised +fixed +answered +reproduced$' \
		-e '^(lite|full|chore|other) +(self|panel|design|code|cross-check|reverify|other) +[0-9]+ +[0-9]+( +([0-9]+ \(n=[0-9]+\)|- \(n=0\))){4}$' \
		-e '^recorded: layers whose receipt wrote a count group; a count is summed over the layers that wrote it \(n\), never read as zero where none was written\.$' \
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
want "$out" '^items: 19 \(1 open\), 4 with no class token' "19 items (no slot, orc- or orchestrator claim among them), 1 open, 4 unknown"
want "$out" '^model= token against the recorded model: 6 same, 2 differs, 3 none or repeated, 8 with no model recorded$' "the model= tokens"
N0='- \(n=0\)'
want "$out" "^code +unknown +claude-opus-5-5 +3 +0 +3/3 +100% +0/3 +0% +1/3 +33% +1/3 +33% +$N0 +1 \\(n=3\\) +1\$" \
	"code x opus: a1, a2 twice; one fail; the first a2 reworked; 2+0+0 requests, 3+0+0 tools"
want "$out" "^docs +unknown +claude-fable-5-1 +2 +0 +2/2 +100% +0/2 +0% +1/2 +50% +0/2 +0% +$N0 +2 \\(n=2\\) +1\$" \
	"docs x fable: b1 (newline, forged row) and b2 (600 bytes), read from the worktree's project dir"
want "$out" "^test +unknown +claude-opus-5-5 +1 +0 +0/1 +0% +0/0 +- +0/0 +- +0/1 +0% +$N0 +0 \\(n=1\\) +0\$" \
	"test x opus: a6, open, its model the RECORDED one and not its token's"
want "$out" "^unknown +unknown +claude-opus-5-5 +4 +0 +0/4 +0% +4/4 +100% +0/0 +- +0/4 +0% +$N0 +0 \\(n=4\\) +0\$" \
	"unknown x opus: a repeated class, an unlisted word, no tokens, a model token after the class"
want "$out" "^ops +unknown +unrecorded +1 +0 +1/1 +100% +0/1 +0% +0/1 +0% +0/1 +0% +$N0 +0 \\(n=1\\) +0\$" "ops x unrecorded: no context row, an empty transcript"
want "$out" "^research +unknown +other +1 +0 +0/1 +0% +1/1 +100% +0/0 +- +0/1 +0% +$N0 +$N0 +-\$" "research x other: a model with a NUL and a tab"
want "$out" "^research +unknown +unrecorded +1 +0 +0/1 +0% +1/1 +100% +0/0 +- +0/1 +0% +$N0 +$N0 +-\$" "research x unrecorded: a revived session's claim"
want "$out" "^docs +unknown +unrecorded +6 +0 +6/6 +100% +0/6 +0% +3/6 +50% +3/6 +50% +$N0 +$N0 +-\$" "docs x unrecorded: three fail-then-reclaim pairs"
[ "$(grep -c '^below the floor: ' "$out")" = 8 ] || fail "want a floor line for each of the 8 cells under 10:
$(cat "$out")"
fl=$( cd "$REPO" && BUDDY_MODEL_FLOOR=3 CLAUDE_PROJECTS_DIR="$P" sh "$ROOT/scripts/model-report.sh" 2>&1 )
printf '%s\n' "$fl" | grep -q '^below the floor: code x' && fail "BUDDY_MODEL_FLOOR=3: a 3-item cell is not under the floor:
$fl"
[ "$(printf '%s\n' "$fl" | grep -c '^below the floor: ')" = 5 ] || fail "BUDDY_MODEL_FLOOR=3: want 5 floor lines:
$fl"

# The audit's control: the same report with the slugs printed must fail it.
cp scripts/model-report.sh "$WORK/leaky.sh"
cp scripts/receipts.awk "$WORK/receipts.awk" # its receipts reader, found beside it (D-143)
echo "echo 'SELECT slug FROM claims;' | q" >>"$WORK/leaky.sh"
report "$WORK/leaky.sh" >"$WORK/leaky.txt" || fail "the leaky control exited non-zero"
audit "$WORK/leaky.txt" >/dev/null && fail "the audit's control failed: a report that prints the slugs passed it"
[ -n "$(shapes "$WORK/leaky.txt")" ] || fail "the shape gate's control failed: a report that prints the slugs passed it"

# --export, twice: the closed items, then nothing.
EXP="$WORK/register.tsv"
report "$ROOT/scripts/model-report.sh" "$EXP" >"$WORK/e1.txt" || fail "the first export exited non-zero:
$(cat "$WORK/e1.txt")"
want "$WORK/e1.txt" '^export: 18 new closed item\(s\) appended$' "the first export: the 18 closed items, not the open one"
want "$WORK/e1.txt" '^Model report: per class x tier x model, over the register --export names$' "an export reports over the register"
want "$WORK/e1.txt" '^items: 18 \(0 open\)' "the register holds no open item"
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
# The rework chain survives the export: in each of the four pairs the failed
# claim comes first and the re-claim names it, once.
chain=$(awk -F'\t' 'NR > 1 && $11 == 2 { if ($6 == "fail") { f++; if ($10 != 1) bad++; first[$1] = 1 } else if ($12 != "-") named[$12]++ }
	END { for (id in first) if (named[id] != 1) bad++; print f + 0, bad + 0 }' "$EXP")
[ "$chain" = "4 0" ] || fail "want 4 failed first claims, each named once by its re-claim; got (fails, faults) $chain"
# A relative --export path is a path, never an awk assignment or an option.
for r in reg=1.tsv -dash.tsv; do
	report "$ROOT/scripts/model-report.sh" "$r" >"$WORK/rel.txt" || fail "--export $r exited non-zero:
$(cat "$WORK/rel.txt")"
	want "$WORK/rel.txt" '^export: 18 new closed item' "--export $r did not append the 18 closed items"
	[ "$(wc -l <"$REPO/$r" | tr -d ' ')" = 19 ] || fail "--export $r did not write $r in the cwd"
	# Read back as a file, not as an assignment: the report covers it and the
	# dedupe sees it (an awk operand `reg=1.tsv` read neither).
	want "$WORK/rel.txt" '^items: 18 \(0 open\)' "--export $r: the report did not read the register back"
	report "$ROOT/scripts/model-report.sh" "$r" >"$WORK/rel.txt" || fail "a second --export $r exited non-zero"
	want "$WORK/rel.txt" '^export: 0 new closed item' "a second --export $r appended again"
done
# A newly closed item is appended once.
as "$SA" release a6-MARKSLUG
report "$ROOT/scripts/model-report.sh" "$EXP" >"$WORK/e3.txt" || fail "the third export exited non-zero"
want "$WORK/e3.txt" '^export: 1 new closed item\(s\) appended$' "the released a6 was not appended once"
dups=$(sed 1d "$EXP" | cut -f1 | sort | uniq -d)
[ -z "$dups" ] || fail "a claim id was exported twice: $dups"
[ "$(sed 1d "$EXP" | wc -l | tr -d ' ')" = 19 ] || fail "want 19 rows in the register"

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

# A ledger at a schema the query was not written for is refused by name.
sqlite3 "$LEDGER" ".backup '$WORK/old.db'"
sqlite3 "$WORK/old.db" 'PRAGMA user_version=18;'
old=$( cd "$REPO" && BUDDY_LEDGER="$WORK/old.db" sh "$ROOT/scripts/model-report.sh" 2>&1 ) &&
	fail "a schema-18 ledger was read:
$old"
printf '%s\n' "$old" | grep -q '^model-report: the ledger is at schema 18; this report reads schema 19 only$' ||
	fail "the schema refusal did not name the version:
$old"
sqlite3 "$WORK/old.db" 'PRAGMA user_version=19;'
( cd "$REPO" && BUDDY_LEDGER="$WORK/old.db" CLAUDE_PROJECTS_DIR="$P" sh "$ROOT/scripts/model-report.sh" ) >"$WORK/v19.txt" 2>&1 ||
	fail "the control failed: the same copy at schema 19 was refused"
want "$WORK/v19.txt" '^items: 19 ' "the control failed: the copy at schema 19 did not report"

# D-133: a parked rider is its producer's work. Bravo (claude-fable-5-1) makes the
# item and parks it with carol, who holds the run and has no model recorded; then
# a plain item carol holds herself is the control that the report still reads the
# holder of any claim nobody parked. In a copy, so the counts above stand.
sqlite3 "$LEDGER" ".backup '$WORK/parked.db'"
PREPO="$WORK/parked-repo"
mkrepo "$PREPO"
git -C "$PREPO" commit -q --allow-empty -m init
cp "$WORK/parked.db" "$PREPO/.git/buddy.db"
PB_() { s=$1; shift; ( cd "$PREPO" && CLAUDE_CODE_SESSION_ID=$s "$WORK/bin/buddy" "$@" ) >/dev/null; }
PB_ "$SC" claim prun --desc x --scope .buddy/slot/prun
PB_ "$SB" claim parked-item --desc "model=claude-fable-5-1 class=code x" --scope parked/x
PB_ "$SB" release parked-item --to "$SC" --in-run prun
PB_ "$SC" release parked-item --outcome pass
PB_ "$SC" claim plain-item --desc "model=claude-fable-5-1 class=code x" --scope plain/x
PB_ "$SC" release plain-item --outcome pass
pid() { sqlite3 "$PREPO/.git/buddy.db" "SELECT claim_id FROM claims WHERE slug='$1'"; }
( cd "$PREPO" && CLAUDE_PROJECTS_DIR="$P" sh "$ROOT/scripts/model-report.sh" --export "$WORK/parked.tsv" ) >"$WORK/parked.txt" 2>&1 </dev/null ||
	fail "the report refused a ledger holding a parked claim: $(cat "$WORK/parked.txt")"
grep -q "^$(pid parked-item)	s-bbbbbbbb	claude-fable-5-1	code	" "$WORK/parked.tsv" ||
	fail "a parked rider was not attributed to its producer:
$(grep "^$(pid parked-item)" "$WORK/parked.tsv")"
grep -q "^$(pid plain-item)	s-cccccccc	" "$WORK/parked.tsv" ||
	fail "the control failed: a plain claim was not attributed to its holder:
$(grep "^$(pid plain-item)" "$WORK/parked.tsv")"


# --- D-175: tier=, reds=, effort and protected, in a ledger of their own ------
# Its own repo, so the counts above stand. Every token case has its positive
# control beside it, and every protected case a near miss that must read 0.
GREPO="$WORK/grammar-repo"
mkrepo "$GREPO"
git -C "$GREPO" commit -q --allow-empty -m init
( cd "$GREPO" && "$WORK/bin/buddy" init ) >/dev/null
G() { s=$1; shift; ( cd "$GREPO" && CLAUDE_CODE_SESSION_ID=$s "$WORK/bin/buddy" "$@" ) >/dev/null; }
GA=a1a1a1a1-0000-4000-8000-0000000000a1
GB=b2b2b2b2-0000-4000-8000-0000000000b2
GC=c3c3c3c3-0000-4000-8000-0000000000c3
GD=d4d4d4d4-0000-4000-8000-0000000000d4
mkdir -p "$WORK/gtx"
# D records an effort outside the list: `other`, and its bytes never printed.
for x in "$GA high" "$GB xhigh" "$GC -" "$GD MARKEFF"; do
	set -- $x
	printf '{"session_id":"%s","cwd":"%s"}' "$1" "$GREPO" | ( cd "$GREPO" && "$WORK/bin/buddy" hello --label "g-$2" ) >/dev/null
	[ "$2" != - ] || continue
	printf '{"type":"assistant","effort":"%s","timestamp":"%s","requestId":"g%s","message":{"model":"claude-opus-5-5","usage":{"input_tokens":3,"cache_read_input_tokens":1,"cache_creation_input_tokens":2,"output_tokens":4}}}\n' \
		"$2" "$(at -7200)" "$2" >"$WORK/gtx/$1.jsonl"
	printf '{"session_id":"%s","cwd":"%s","transcript_path":"%s","tool_name":"Bash","tool_input":{}}' "$1" "$GREPO" "$WORK/gtx/$1.jsonl" |
		( cd "$GREPO" && "$WORK/bin/buddy" beat ) >/dev/null
done
D="model=claude-opus-5-5 class=docs"
# A shared claim on a protected file, released a second before every other
# claim of A's: no later item of A's sits beside it (the window's control).
G "$GA" claim g-early --shared --desc x --scope CLAUDE.md
G "$GA" release g-early
sleep 1
# B holds a shared companion on a protected file beside its item, open while
# A's near misses are claimed: a companion is the SAME session's only.
G "$GB" claim c-comp --shared --desc x --scope skills/buddy/SKILL.md
G "$GB" claim c-item --desc "$D tier=lite c" --scope n/c1
G "$GA" claim g-lite --desc "$D tier=lite ok" --scope n/g1
G "$GA" release g-lite --outcome pass --note "reds=2 abc123"
G "$GA" claim g-chore --desc "$D tier=chore ok" --scope n/g2
G "$GA" release g-chore --outcome pass --note reds=0
G "$GA" claim g-full --desc "$D tier=full ok" --scope n/g3
G "$GA" release g-full --outcome fail --note "reds=1 x"
G "$GA" claim g-reptier --desc "$D tier=lite tier=full" --scope n/g4
G "$GA" release g-reptier --outcome pass --note "reds=01 x"
G "$GA" claim g-badtier --desc "$D tier=MARKTIER x" --scope n/g5
G "$GA" release g-badtier --outcome pass --note "reds=x MARKREDS"
G "$GA" claim g-latetier --desc "$D x tier=lite" --scope n/g6
G "$GA" release g-latetier --outcome pass --note "reds=1 reds=2"
G "$GA" claim g-brokenclass --desc "model=claude-opus-5-5 class=docs class=code tier=lite" --scope n/g7
G "$GA" release g-brokenclass --outcome pass --note REDS=3
# A valid tier= as the third word after a broken pair (no class; no model):
# the anchor is the pair, not the position (a mutation survived without these).
G "$GA" claim g-noclass --desc "model=claude-opus-5-5 foo tier=lite" --scope n/g10
G "$GA" release g-noclass --outcome pass --note reds=0
G "$GA" claim g-nomodel --desc "foo bar tier=lite" --scope n/g11
G "$GA" release g-nomodel --outcome pass --note reds=0
# A LISTED class word makes the pair: an unlisted one admits no tier (Codex).
G "$GA" claim g-badclass --desc "model=claude-opus-5-5 class=MARKCLS tier=lite" --scope n/g12
G "$GA" release g-badclass --outcome pass --note reds=0
G "$GA" claim g-noreds --desc "$D tier=lite" --scope n/g8
G "$GA" release g-noreds --outcome pass
G "$GA" claim g-big --desc "$D tier=lite" --scope n/g9
G "$GA" release g-big --outcome pass --note reds=1000
# The protected list both ways, folded, and its near misses.
for x in p-parent:docs p-child:internal/fence/fence.go p-prefix:docs/review-charter-process.md \
	p-case:Scripts/x.sh p-rules:AGENTS.md p-near:internalx/y p-sib:docs/README.md p-star:'*' p-dash:docs-old; do
	G "$GA" claim "${x%%:*}" --desc "$D tier=lite p" --scope "${x#*:}"
	G "$GA" release "${x%%:*}" --outcome pass --note reds=0
done
G "$GB" release c-item --outcome pass --note reds=0
G "$GB" release c-comp
G "$GC" claim g-c --desc "$D tier=lite unrecorded" --scope n/gc
G "$GC" release g-c --outcome pass --note reds=0
G "$GD" claim g-d --desc "$D tier=lite other effort" --scope n/gd
G "$GD" release g-d --outcome pass --note reds=0
GLED="$GREPO/.git/buddy.db"
for m in MARKTIER MARKREDS MARKEFF MARKCLS; do
	sqlite3 "$GLED" "SELECT descr, outcome_note FROM claims; SELECT effort FROM session_context;" | grep -q "$m" || fail "the planting control failed: $m is not in the grammar fixture"
done
[ "$(sqlite3 "$GLED" "SELECT count(*) FROM claim_scopes WHERE scope = 'Scripts/x.sh' AND folded = 'scripts/x.sh'")" = 1 ] ||
	fail "the planting control failed: Scripts/x.sh was not stored with its folded spelling"
[ "$(sqlite3 "$GLED" "SELECT group_concat(effort, ' ') FROM (SELECT effort FROM session_context ORDER BY effort)")" = "MARKEFF high xhigh" ] ||
	fail "the planting control failed: the efforts MARKEFF, high and xhigh were not recorded"
greport() { ( cd "$GREPO" && PATH="$WORK/shim:$PATH" CLAUDE_PROJECTS_DIR="$P" sh "$ROOT/scripts/model-report.sh" ${1:+--export "$1"} ) </dev/null 2>&1; }
greport >"$WORK/g0.txt" || fail "the grammar report exited non-zero:
$(cat "$WORK/g0.txt")"
GEXP="$WORK/grammar.tsv"
greport "$GEXP" >"$WORK/g1.txt" || fail "the grammar export exited non-zero:
$(cat "$WORK/g1.txt")"
for f in "$WORK/g0.txt" "$WORK/g1.txt" "$GEXP"; do
	for m in MARKTIER MARKREDS MARKEFF MARKCLS; do grep -q "$m" "$f" && fail "planted $m reached $(basename -- "$f")"; done
	[ "$f" = "$GEXP" ] && continue
	odd=$(shapes "$f")
	[ -z "$odd" ] || fail "$(basename -- "$f") printed a line of no known shape:
$odd"
done
[ "$(head -n 1 "$GEXP")" = "$hdr" ] || fail "the grammar export's header is not the 18 columns"
bad=$(sed 1d "$GEXP" | LC_ALL=C grep -Ev "$row" || true)
[ -z "$bad" ] || fail "a grammar export row is not in the rendered form:
$bad"
# gcol <slug> -> tier reds effort protected, read by the row's claim id.
gcol() { id=$(sqlite3 "$GLED" "SELECT claim_id FROM claims WHERE slug = '$1'"); awk -F'\t' -v id="$id" '$1 == id { print $15, $16, $17, $18 }' "$GEXP"; }
for x in 'g-lite:lite 2 high 0' 'g-chore:chore 0 high 0' 'g-full:full 1 high 0' \
	'g-reptier:unknown - high 0' 'g-badtier:unknown - high 0' 'g-latetier:unknown - high 0' \
	'g-brokenclass:unknown - high 0' 'g-noclass:unknown 0 high 0' 'g-nomodel:unknown 0 high 0' 'g-badclass:unknown 0 high 0' 'g-noreds:lite - high 0' 'g-big:lite - high 0' \
	'g-early:unknown - high 1' 'p-parent:lite 0 high 1' 'p-child:lite 0 high 1' 'p-prefix:lite 0 high 1' \
	'p-case:lite 0 high 1' 'p-rules:lite 0 high 1' 'p-near:lite 0 high 0' 'p-sib:lite 0 high 0' \
	'p-star:lite 0 high 0' 'p-dash:lite 0 high 0' 'c-comp:unknown - xhigh 1' 'c-item:lite 0 xhigh 1' \
	'g-c:lite 0 unrecorded 0' 'g-d:lite 0 other 0'; do
	got=$(gcol "${x%%:*}")
	[ "$got" = "${x#*:}" ] || fail "${x%%:*}: want tier reds effort protected '${x#*:}', got '$got'"
done
want "$WORK/g1.txt" '^items: 26 \(0 open\)' "the grammar fixture's 26 items"
want "$WORK/g1.txt" '^tier= token: 1 chore, 15 lite, 1 full, 9 missing, repeated or unlisted \(`unknown`\), 0 older export row\(s\) without it' "the tier tokens"
want "$WORK/g1.txt" '^outcomes without reds=: 6 of 24$' "the outcomes without reds="
want "$WORK/g1.txt" '^protected: 8 item\(s\) ' "the protected count"
want "$WORK/g1.txt" '^effort, the holder.s recorded: 0 low, 0 medium, 22 high, 2 xhigh, 0 max, 1 unrecorded, 1 other, 0 older' "the efforts"
want "$WORK/g1.txt" "^docs +lite +claude-opus-5-5 +14 +6 +14/14 +100% +0/14 +0% +0/14 +0% +0/14 +0% +0\\.17 \\(n=12\\) +$N0 +-\$" \
	"docs x lite x opus: 14 items, 6 protected, reds 2 over the 12 that carry the token"
want "$WORK/g1.txt" "^docs +full +claude-opus-5-5 +1 +0 +1/1 +100% +0/1 +0% +1/1 +100% +0/1 +0% +1\\.00 \\(n=1\\) " "docs x full x opus"
want "$WORK/g1.txt" '^below the floor: docs x lite x claude-opus-5-5 has 8 unprotected item\(s\), under 10; no inference$' \
	"the floor counts the unprotected items only"
# The input line: the path, the rows, the created dates and the file's digest,
# each computed here without the script.
sha() { if command -v sha256sum >/dev/null 2>&1; then sha256sum <"$1"; else shasum -a 256 <"$1"; fi | cut -d' ' -f1; }
span() { sed 1d "$1" | cut -f8 | sort -n | sed -n '1p;$p' | while read -r e; do jq -rn --argjson e "$e" '$e | todate | .[0:10]'; done | paste -sd. - | sed 's/\./../'; }
grep -qxF "input: $GEXP, 26 row(s), created $(span "$GEXP") UTC, sha256 $(sha "$GEXP")" "$WORK/g1.txt" ||
	fail "the input line is not the export's path, rows, dates and sha256 ($(sha "$GEXP")):
$(grep '^input:' "$WORK/g1.txt")"
grep -qxF "input: the ledger copy (not retained: --export <tsv> keeps one), 26 row(s), created $(span "$GEXP") UTC, sha256 -" "$WORK/g0.txt" ||
	fail "the ledger copy's input line:
$(grep '^input:' "$WORK/g0.txt")"
# An OLDER export (14 columns): read back with the four columns missing, its
# header gaining them, its rows untouched, its mode kept, the rest appended.
# Its last row has NO final newline: the first appended row must not be glued
# onto it (Codex code pass; the 3-and-23 field count below is the witness).
OLD="$WORK/old-export.tsv"
{ printf '%s\n' "$hdr14"; sed 1d "$GEXP" | head -n 3 | cut -f1-14; } | perl -pe 'chomp if eof' >"$OLD"
[ -n "$(tail -c 1 "$OLD")" ] || fail "the control failed: the older export still ends in a newline"
chmod 640 "$OLD"
sed 1d "$GEXP" | head -n 3 | cut -f1-14 >"$WORK/old-rows.tsv"
greport "$OLD" >"$WORK/g2.txt" || fail "the export over an older file exited non-zero:
$(cat "$WORK/g2.txt")"
odd=$(shapes "$WORK/g2.txt")
[ -z "$odd" ] || fail "the older-file export printed a line of no known shape:
$odd"
want "$WORK/g2.txt" '^export: the header gained the tier, reds, effort and protected columns' "an older export's header was not upgraded"
want "$WORK/g2.txt" '^export: 23 new closed item\(s\) appended$' "the older export: the 23 items it lacked"
want "$WORK/g2.txt" ', 3 older export row\(s\) without it \(`-`\)$' "an older row's tier reads as missing"
want "$WORK/g2.txt" '; 3 older export row\(s\) unknown$' "an older row's protected reads as missing"
want "$WORK/g2.txt" '^unknown +- +claude-opus-5-5 +2 +0 ' "an older row's cell is tier -, its protected not counted"
want "$WORK/g2.txt" '^docs +- +claude-opus-5-5 +1 +0 ' "an older row's cell is tier -, its protected not counted"
[ "$(head -n 1 "$OLD")" = "$hdr" ] || fail "the older export's header is not the 18 columns now"
[ "$(head -n 1 "$OLD" | cut -f1-14)" = "$hdr14" ] || fail "an older reader's 14 columns moved"
sed -n 2,4p "$OLD" | cmp -s - "$WORK/old-rows.tsv" || fail "the older export's rows were rewritten"
[ "$(awk -F'\t' 'NR > 1 { c[NF]++ } END { print c[14] + 0, c[18] + 0 }' "$OLD")" = "3 23" ] ||
	fail "want 3 rows of 14 fields and 23 of 18 in the upgraded export"
mode=$(stat -c %a "$OLD" 2>/dev/null || stat -f %Lp "$OLD")
[ "$mode" = 640 ] || fail "the header upgrade changed the export's mode to $mode"
grep -qF ", sha256 $(sha "$OLD")" "$WORK/g2.txt" || fail "the printed sha256 is not the upgraded file's"
cp "$OLD" "$WORK/old-first.tsv"
greport "$OLD" >"$WORK/g3.txt" || fail "the second export over the upgraded file exited non-zero"
grep -q '^export: the header gained' "$WORK/g3.txt" && fail "an 18-column header was upgraded again"
cmp -s "$OLD" "$WORK/old-first.tsv" || fail "the second export changed the upgraded file"
# Read back, a row's new columns are DATA too: a bad tier is counted, never shown.
sed -n 2p "$GEXP" | awk -F'\t' -v OFS='\t' '{ $1 = "abcdef0123456789"; $15 = "MARKTIER"; print }' >>"$GEXP"
greport "$GEXP" >"$WORK/g4.txt" || fail "the export over a planted grammar row exited non-zero"
grep -q MARKTIER "$WORK/g4.txt" && fail "a planted tier reached the report"
want "$WORK/g4.txt" '^left out: 1 row\(s\) that are not' "the planted tier row was not counted"
# A NUL voids the tokens of its text (Codex code pass): in a copy, g-lite's
# description and g-full's note gain a NUL and a second token. The control is
# the rows above, where both read lite and 1.
NREPO="$WORK/nul-repo"
mkrepo "$NREPO"
git -C "$NREPO" commit -q --allow-empty -m init
sqlite3 "$GLED" ".backup '$NREPO/.git/buddy.db'"
sqlite3 "$NREPO/.git/buddy.db" "UPDATE claims SET descr = 'model=claude-opus-5-5 class=docs tier=lite ' || char(0) || ' tier=full' WHERE slug = 'g-lite';
	UPDATE claims SET outcome_note = 'reds=1 ' || char(0) || ' reds=2' WHERE slug = 'g-full';"
[ "$(sqlite3 "$NREPO/.git/buddy.db" "SELECT count(*) FROM claims WHERE instr(CAST(descr AS BLOB), x'00') > 0 OR instr(CAST(outcome_note AS BLOB), x'00') > 0")" = 2 ] ||
	fail "the planting control failed: the NULs were not stored"
( cd "$NREPO" && CLAUDE_PROJECTS_DIR="$P" sh "$ROOT/scripts/model-report.sh" --export "$WORK/nul.tsv" ) >"$WORK/nul.txt" 2>&1 </dev/null ||
	fail "the report over a ledger holding NULs exited non-zero: $(cat "$WORK/nul.txt")"
nid() { sqlite3 "$GLED" "SELECT claim_id FROM claims WHERE slug = '$1'"; }
[ "$(awk -F'\t' -v id="$(nid g-lite)" '$1 == id { print $4, $5, $15 }' "$WORK/nul.tsv")" = "unknown none unknown" ] ||
	fail "a NUL in a description hid a second tier= token: $(awk -F'\t' -v id="$(nid g-lite)" '$1 == id' "$WORK/nul.tsv")"
[ "$(awk -F'\t' -v id="$(nid g-full)" '$1 == id { print $16 }' "$WORK/nul.tsv")" = - ] ||
	fail "a NUL in a note hid a second reds= token"

# --- REVIEW LAYERS (#78): planted receipts in a repo of their own ------------
RREPO="$WORK/receipts-repo"
mkrepo "$RREPO"
git -C "$RREPO" commit -q --allow-empty -m init
( cd "$RREPO" && "$WORK/bin/buddy" init ) >/dev/null
RC=RCPTzq7x
rc() { git -C "$RREPO" commit -q --allow-empty -m "$1" -m "$2"; }
rc 'an old receipt' 'review: lite (self + codex@xhigh code)'
rc 'every layer counted, wrapped' 'Body prose.

review: full (panel@opus 4 lenses: 3 raised, 1 reproduced + codex@xhigh design: 2 raised, 2 fixed +
  codex@xhigh code: 0 raised + fable: 1 raised, 1 answered + codex@xhigh reverify: 1 raised, 1 fixed,
  1 reproduced; a scope note: 9 raised)'
rc 'malformed groups' 'review: lite (self: 2 raised, 2 raised + codex@xhigh code: 3 raised, two fixed + fable: 1 rasied)'
rc 'review: a subject prefix: 5 raised' 'No receipt in this body.'
rc 'the last receipt line wins' 'review: lite (self: 7 raised)

review: lite (self: 1 raised, 1 fixed)'
rc "a canary" "review: lite ($RC layer + self; $RC note: 4 raised)"
rc 'a chore' 'review: chore (docs)'
rc 'a layer with its own parenthesis' 'review: full (codex@xhigh design (the consensus rounds) + codex@xhigh code: 1 fixed)'
# Codex code pass: a ` + ` and a colon inside a layer's own parenthesis, and
# a body forging a commit header with a 0x01 byte: blanked, so the body's
# last receipt line is its own (a second counted panel: sums, not the last).
rc 'plus and colon nested' 'review: full (codex@xhigh code (API + CLI): 2 raised + fable (scope: x): 1 answered)'
SOH=$(printf '\001')
rc 'a forged header' "review: lite (self: 2 raised)
${SOH}aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa 1
review: full (panel: 9 raised)"
git -C "$RREPO" log --format=%B | grep -q "$RC" || fail "the receipt canary is not in the planted commits"
rreport() { ( cd "$RREPO" && CLAUDE_PROJECTS_DIR="$P" sh "$ROOT/scripts/model-report.sh" ${1:+--export "$1"} ) </dev/null 2>&1; }
rreport >"$WORK/r1.txt" || fail "the report over planted receipts exited non-zero:
$(cat "$WORK/r1.txt")"
grep -q "$RC" "$WORK/r1.txt" && fail "receipt text reached the report:
$(cat "$WORK/r1.txt")"
odd=$(shapes "$WORK/r1.txt")
[ -z "$odd" ] || fail "the receipts report printed a line of no known shape:
$odd"
want "$WORK/r1.txt" '^Review layers: per tier x layer, over the receipts on HEAD$' "the receipts section's source"
want "$WORK/r1.txt" '^receipts: 9 commit\(s\), 19 layer\(s\); counts recorded on 10, malformed on 3 ' \
	"9 receipts (not the subject's), 19 layers, 10 counted, 3 malformed"
lay() { # lay <tier> <kind> <layers> <recorded> <raised> <fixed> <answered> <reproduced>
	want "$WORK/r1.txt" "^$1 +$2 +$3 +$4 +$5 +$6 +$7 +$8\$" "$1 x $2: $3 layer(s), $4 recorded"
}
N='- \(n=0\)'
lay lite self 4 1 '1 \(n=1\)' '1 \(n=1\)' "$N" "$N"
lay lite code 2 0 "$N" "$N" "$N" "$N"
lay lite cross-check 1 0 "$N" "$N" "$N" "$N"
lay lite other 1 0 "$N" "$N" "$N" "$N"
lay full panel 2 2 '12 \(n=2\)' "$N" "$N" '1 \(n=1\)'
lay full design 2 1 '2 \(n=1\)' '2 \(n=1\)' "$N" "$N"
lay full code 3 3 '2 \(n=2\)' '1 \(n=1\)' "$N" "$N"
lay full cross-check 2 2 '1 \(n=1\)' "$N" '2 \(n=2\)' "$N"
lay full reverify 1 1 '1 \(n=1\)' '1 \(n=1\)' "$N" '1 \(n=1\)'
lay chore other 1 0 "$N" "$N" "$N" "$N"
[ "$(grep -cE '^(lite|full|chore|other) +[a-z-]+ +[0-9]+ +[0-9]+ ' "$WORK/r1.txt")" = 10 ] ||
	fail "want exactly the 10 planted tier x layer rows:
$(cat "$WORK/r1.txt")"

# --export: the register beside the item export, every commit once.
REXP="$WORK/rexp.tsv"
rreport "$REXP" >"$WORK/r2.txt" || fail "the receipts export exited non-zero:
$(cat "$WORK/r2.txt")"
want "$WORK/r2.txt" '^export: 9 new commit receipt\(s\) appended$' "the first export: the 9 receipts"
want "$WORK/r2.txt" '^Review layers: per tier x layer, over the receipts register beside the export$' "an export reports over the register"
want "$WORK/r2.txt" '^full +panel +2 +2 +12 \(n=2\)' "the register read back carries the counts"
[ "$(head -n 1 "$REXP.receipts")" = "commit	committed	tier	layer	kind	counts	raised	fixed	answered	reproduced" ] ||
	fail "the receipts register's header is not the columns"
rrow='^[0-9a-f]{40}	[0-9]+	(lite|full|chore|other)	[0-9]+	(self|panel|design|code|cross-check|reverify|other)	(recorded	([0-9]+|-)	([0-9]+|-)	([0-9]+|-)	([0-9]+|-)|(none|malformed)	-	-	-	-)$'
norow='	recorded	-	-	-	-$'
bad=$(sed 1d "$REXP.receipts" | LC_ALL=C grep -Ev "$rrow" || true)
[ -z "$bad" ] || fail "a receipts register row is not in the rendered form:
$bad"
! grep -q "$norow" "$REXP.receipts" || fail "a receipts register row is recorded with no count"
[ "$(sed 1d "$REXP.receipts" | grep -c .)" = 19 ] || fail "want 19 layer rows in the receipts register"
grep -q "$RC" "$REXP.receipts" && fail "receipt text reached the receipts register"
grep -q "^aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa	" "$REXP.receipts" && fail "a body's 0x01 byte forged a commit's receipt"
cp "$REXP.receipts" "$WORK/rfirst.tsv"
rreport "$REXP" >"$WORK/r3.txt" || fail "the second receipts export exited non-zero"
want "$WORK/r3.txt" '^export: 0 new commit receipt\(s\) appended$' "the second export appended a receipt"
cmp -s "$REXP.receipts" "$WORK/rfirst.tsv" || fail "the second export changed the receipts register"
# A hand-planted row is DATA: counted, never printed; a copied row is a repeat.
printf 'not a row %s\n' "$RC" >>"$REXP.receipts"
printf 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\t1\tlite\t1\tself\trecorded\t-\t-\t-\t-\n' >>"$REXP.receipts"
sed -n 2p "$WORK/rfirst.tsv" >>"$REXP.receipts"
rreport "$REXP" >"$WORK/r4.txt" || fail "the export over a planted receipts register exited non-zero"
grep -q "$RC" "$WORK/r4.txt" && fail "a planted receipts register row reached the report"
want "$WORK/r4.txt" '^left out: 2 receipt row\(s\) that are not' "the planted receipts rows (one recorded with no count) were not counted"
want "$WORK/r4.txt" '^left out: 1 repeated receipt layer' "the copied receipts row was not counted as a repeat"
printf 'some other file\n' >"$WORK/rforeign.tsv.receipts"
if rreport "$WORK/rforeign.tsv" >/dev/null; then fail "a receipts register with a foreign header was appended to"; fi
[ "$(cat "$WORK/rforeign.tsv.receipts")" = "some other file" ] || fail "a refused receipts export changed the file"
[ ! -e "$WORK/rforeign.tsv" ] || fail "a refused receipts export wrote the item register first"

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
