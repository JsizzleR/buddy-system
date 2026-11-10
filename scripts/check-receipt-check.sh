#!/bin/sh
# check-receipt-check.sh — done-check for scripts/receipt-check.sh (D-143),
# the D-104 receipt check, and for what it added to scripts/receipts.awk.
#
# HERMETIC: sh, git. Every commit is planted in a throwaway repo with a
# chosen COMMITTER epoch, because the check's two cuts (D-104, D-141) are
# epochs. It never reads this checkout's history: over real history the
# check is a report (a red there would refuse every push, for a commit
# nobody can fix), so only planted commits can hold it.
#
# What must hold, each finding beside the control that passes:
#   - each tier's owed layers: a complete full and lite pass, and the same
#     receipt with one layer gone is held for exactly that layer, each of
#     full's five and lite's two; a Codex cross-check is not a code pass;
#   - a skip named in the scope note passes (`; fable cross-check skipped on
#     the operator's cap`), and the same note with no skip word does not;
#   - a full whose code layer wrote `0 raised` owes no re-verify; `1 raised`
#     does;
#   - a bare `codex@xhigh` is the code pass (D-104's first lite receipts);
#   - a chore with a reason passes, a bare `review: chore` is held;
#   - no receipt line, no tier word (with a malformed group: `-` on both
#     lines, never an internal tier), and a full whose prose holds a
#     parenthesis after an em dash (no list straight after the tier word);
#   - the D-104 cut: a commit AT its epoch is not held, the same receipt one
#     second later is; the D-141 cut: a malformed count group after it is
#     held, the same before it is not;
#   - a receipt in the subject's paragraph is read (#87), and a subject line
#     `review: …` alone is not;
#   - a range limits what is read; an option-shaped range is refused before
#     git sees it (exit 2, no file written), and so are two ranges;
#   - no planted text reaches the output, and every line is a known shape.
set -eu
. "$(dirname -- "$0")/lib.sh"
ROOT=$(repo_root "$0")
CDPATH= cd -- "$ROOT"

fail() { echo "check-receipt-check: FAIL — $*" >&2; exit 1; }

WORK=$(mktemp -d) || fail "mktemp failed"
WORK=$(CDPATH= cd -- "$WORK" && pwd -P)
trap 'rm -rf "$WORK"' EXIT INT TERM
R="$WORK/repo"
mkrepo "$R"
D104=1793074046
D141=1794322479
MID=$((D104 + 100000))
NEW=$((D141 + 100000))
CAN=RCPTcnry5q

# c <name> <epoch> <message> — one empty commit at that committer epoch; its
# sha is recorded under <name> for the assertions.
c() {
	GIT_COMMITTER_DATE="@$2 +0000" GIT_AUTHOR_DATE="@$2 +0000" \
		git -C "$R" commit -q --allow-empty -m "$3"
	printf '%s %s\n' "$1" "$(git -C "$R" rev-parse --short=12 HEAD)" >>"$WORK/names"
}
sha() { awk -v n="$1" '$1 == n { print $2 }' "$WORK/names"; }
FULL='review: full (panel@opus + codex@xhigh design + codex@xhigh code + fable + codex@xhigh reverify)'

c base 1790000000 'init'
c old-bad 1793000000 "old

review: full (self)"
c at-d104 $D104 "at the cut

review: full (self)"
c after-d104 $((D104 + 1)) "one second later

review: full (self)"
c full-ok $MID "complete

$FULL"
c full-nofable $MID "no cross-check

review: full (panel@opus + codex@xhigh design + codex@xhigh code + codex@xhigh reverify)"
c full-skip $MID "skipped on the cap

review: full (panel@opus + codex@xhigh design + codex@xhigh code + codex@xhigh reverify; fable cross-check skipped on the operator's cap)"
c full-noskip $MID "a note with no skip word

review: full (panel@opus + codex@xhigh design + codex@xhigh code + codex@xhigh reverify; fable cross-check on the operator's cap)"
c full-zero $NEW "nothing raised, nothing to re-verify

review: full (panel@opus + codex@xhigh design + codex@xhigh code: 0 raised + fable)"
c full-one $NEW "one raised, no re-verify

review: full (panel@opus + codex@xhigh design + codex@xhigh code: 1 raised, 1 fixed + fable)"
c lite-ok $MID "lite

review: lite (self + codex@xhigh code)"
c lite-bare $MID "a bare codex pass

review: lite (codex@xhigh)"
c lite-nocode $MID "no code pass

review: lite (self + fable)"
c chore-ok $MID "chore

review: chore (docs)"
c chore-bare $MID "chore, no reason

review: chore"
c absent $MID "no receipt at all

Body prose only."
c notier $MID "no tier word

review: none (self)"
c emdash $MID "prose, a parenthesis after an em dash

review: full — panel x3 + codex@xhigh design (on drafted code) + codex@xhigh code"
c mal-mid $MID "malformed before D-141

review: lite (self: 2 raised, two fixed + codex@xhigh code)"
# Codex code pass: a Codex cross-check is not the code pass; an unknown tier
# with a malformed group renders `-` on both lines; each full layer alone.
c lite-codexcc $MID "a codex cross-check, no code pass

review: lite (self + codex@xhigh cross-check: 2 raised)"
c notier-mal $NEW "no tier word, a malformed group

review: nope (self: 1 nope)"
c full-nopanel $MID "no panel

review: full (codex@xhigh design + codex@xhigh code + fable + codex@xhigh reverify)"
c full-nodesign $MID "no design

review: full (panel@opus + codex@xhigh code + fable + codex@xhigh reverify)"
c full-nocode $MID "no code

review: full (panel@opus + codex@xhigh design + fable + codex@xhigh reverify)"
c mal-new $NEW "malformed after D-141

review: lite (self: 2 raised, two fixed + codex@xhigh code)"
c ok-new $NEW "counts after D-141, well formed

review: lite (self: 2 raised, 2 fixed + codex@xhigh code: 0 raised)"
c subj-para $MID "receipt in the subject's paragraph
review: lite (self)"
c subj-only $MID "review: lite (self + codex@xhigh code)"
c canary $MID "a canary $CAN

review: lite ($CAN self + codex@xhigh code $CAN; $CAN skipped $CAN)"
git -C "$R" log --format=%B | grep -q "$CAN" || fail "the planting control failed: the canary is not in the planted commits"

run() { ( cd "$R" && sh "$ROOT/scripts/receipt-check.sh" "$@" ) </dev/null 2>&1; }
run >"$WORK/out.txt" || fail "the check exited non-zero over planted history:
$(cat "$WORK/out.txt")"
OUT="$WORK/out.txt"

grep -q "$CAN" "$OUT" && fail "planted text reached the output:
$(cat "$OUT")"
odd=$(grep -vE \
	-e '^Receipt check \(D-104, D-143\): a tier.s owed layers the receipt does not name; a report, it refuses nothing$' \
	-e '^commits: [0-9]+; before D-104, not held: [0-9]+; held: [0-9]+, with a finding: [0-9]+ \([0-9]+ finding\(s\)\)$' \
	-e '^  [0-9a-f]{12}  (full|lite|chore|-)  (owes (self|panel|design|code|cross-check|reverify)|has no layer list|gives no reason|no receipt line|no tier word \(lite, full or chore\)|[0-9]+ malformed count group\(s\))$' \
	"$OUT" || true)
[ -z "$odd" ] || fail "a line of no known shape:
$odd"

# has <name> <finding> — exactly that line; clean <name> — no line at all.
has() {
	s=$(sha "$1"); [ -n "$s" ] || fail "no fixture named $1"
	grep -qE "^  $s  $2\$" "$OUT" || fail "$1: want '$2':
$(cat "$OUT")"
}
clean() {
	s=$(sha "$1"); [ -n "$s" ] || fail "no fixture named $1"
	! grep -q "^  $s  " "$OUT" || fail "$1: want no finding, got:
$(grep "^  $s  " "$OUT")"
}
nfind() { # nfind <name> <n> — exactly n finding lines
	s=$(sha "$1")
	[ "$(grep -c "^  $s  " "$OUT")" = "$2" ] || fail "$1: want $2 finding(s):
$(grep "^  $s  " "$OUT")"
}

want_line() { grep -qE "$1" "$OUT" || fail "want a line matching '$1':
$(cat "$OUT")"; }
clean base; clean old-bad; clean at-d104
has after-d104 'full  owes panel'; nfind after-d104 5
clean full-ok
has full-nofable 'full  owes cross-check'; nfind full-nofable 1
clean full-skip
has full-noskip 'full  owes cross-check'; nfind full-noskip 1
clean full-zero
has full-one 'full  owes reverify'; nfind full-one 1
clean lite-ok
has lite-bare 'lite  owes self'; nfind lite-bare 1
has lite-nocode 'lite  owes code'; nfind lite-nocode 1
clean chore-ok
has chore-bare 'chore  gives no reason'; nfind chore-bare 1
has absent '-  no receipt line'
has notier '-  no tier word \(lite, full or chore\)'
has emdash 'full  has no layer list'; nfind emdash 1
clean mal-mid
has mal-new 'lite  1 malformed count group\(s\)'; nfind mal-new 1
has lite-codexcc 'lite  owes code'; nfind lite-codexcc 1
has notier-mal '-  no tier word \(lite, full or chore\)'; has notier-mal '-  1 malformed count group\(s\)'; nfind notier-mal 2
has full-nopanel 'full  owes panel'; nfind full-nopanel 1
has full-nodesign 'full  owes design'; nfind full-nodesign 1
has full-nocode 'full  owes code'; nfind full-nocode 1
clean ok-new
has subj-para 'lite  owes code'; nfind subj-para 1
has subj-only '-  no receipt line'
clean canary
# The totals last, so a mutant dies on the fixture it breaks, not here.
want_line '^commits: 29; before D-104, not held: 3; held: 26, with a finding: 18 \(23 finding\(s\)\)$'

# A range: only what it names is read.
run "$(sha ok-new)..HEAD" >"$WORK/range.txt" || fail "a range exited non-zero"
grep -qE '^commits: 3; before D-104, not held: 0; held: 3, with a finding: 2 ' "$WORK/range.txt" ||
	fail "the range $(sha ok-new)..HEAD: want 3 commits, 2 with a finding:
$(cat "$WORK/range.txt")"

# Refusals, each beside the control that the same call shape runs.
set +e
run "--output=$WORK/written" >"$WORK/u1.txt"; rc=$?
set -e
[ "$rc" = 2 ] || fail "an option-shaped range: want exit 2, got $rc:
$(cat "$WORK/u1.txt")"
[ ! -e "$WORK/written" ] || fail "an option-shaped range wrote a file"
grep -q '^refused: ' "$WORK/u1.txt" || fail "an option-shaped range: no refused: line"
set +e
run HEAD HEAD >"$WORK/u2.txt"; rc=$?
run no-such-rev >"$WORK/u3.txt"; rc3=$?
run HEAD >"$WORK/u4.txt"; rc4=$?
set -e
[ "$rc" = 2 ] || fail "two ranges: want exit 2, got $rc"
[ "$rc3" = 1 ] || fail "a range git cannot read: want exit 1, got $rc3"
[ "$rc4" = 0 ] || fail "the control: one plain range exited $rc4"

echo "check-receipt-check: ok"
