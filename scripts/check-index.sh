#!/bin/sh
# check-index.sh — done-check: every decision in the record is on a line of
# CLAUDE.md's "Decisions already made" index, and every number that index
# names is a decision in the record.
#
# WHY A GATE. CLAUDE.md is loaded into every session; docs/decisions.md is
# opened only on purpose. So the index is the one place a session learns that a
# subject is settled, and a decision missing from it reads as OPEN to a session
# that never opens the record (D-112's reason, kept by D-118's rule 2: "Every
# number stays on some line"). Since D-118 the index is topical, one line per
# subject carrying several numbers, and a new decision AMENDS a line instead of
# appending one: the step that silently drops a number is now an edit in the
# middle of a sentence, not a missing last bullet. D-118 recorded this check
# as open ("Nothing gates it today; this lane checked it with a scratch
# script"); it is that scratch check, armed.
#
# WHAT IT CHECKS. Both directions, by exact D-number:
#   forward  every `## D-NNN` heading in docs/decisions.md has its number on
#            some line INSIDE CLAUDE.md's "## Decisions already made" section,
#            bounded from that heading to the next `## ` heading or EOF. A
#            number elsewhere in CLAUDE.md does not count: "Operating rules"
#            and the invariants cite D-numbers as REASONS for a rule, and a
#            citation there is not the index saying the subject is settled.
#   reverse  every D-number on a line of that section has a `## D-NNN`
#            heading. A dangling number in the index points a session at a
#            record that does not exist (a typo'd D-121 for D-112 hides D-112).
# A number is a whole token: `D-` plus its maximal run of digits, not preceded
# by a letter, digit, `_` or `-`. So D-12 is never satisfied by D-123's line,
# and D-123 is never read as D-12 (clause 4 plants the prefix; clause 1 the
# near-misses XD-999 and D-9990 beside a D-999 that must still be missing).
# Coarse on purpose, as check-charter.sh is: a number on a line is not a
# faithful summary, and this cannot judge the line. It catches the failure
# D-118's rule names (a number on no line) and its mirror.
#
# MALFORMED HEADINGS FAIL. The forward scan sees only headings it can parse; a
# decision headed `### D-124` or `## D–124` (en dash) would be invisible to it,
# and invisible means GREEN. So any heading line that looks like a decision
# heading but is not spelled `## D-<digits>` fails by itself.
#
# DELIBERATELY NOT CHECKED: Markdown context. A `## D-130` line inside a code
# fence or an HTML comment in the record reads as a heading here (Codex code
# pass), so it could stand behind a dangling index number. The record has no
# comments and its four fences hold command output, no heading; telling them
# apart needs a Markdown reader, not a line scan. Add it the day an example
# heading lands in a fence.
#
# TWO CLAUSES (the convention check.sh and check-charter.sh use). Each planted
# fault runs against temp copies of the REAL files and must be flagged with
# exactly its planted number, so the same comparison proves the real files
# pass apart from the plant (the positive control): a scan that finds no
# headings, or no section, would report the same GREEN as one that found them
# all indexed, and these fixtures are what tell the two apart.
set -eu
. "$(dirname -- "$0")/lib.sh"
CDPATH= cd -- "$(repo_root "$0")"

DECISIONS=docs/decisions.md
INDEX=CLAUDE.md
SECTION='## Decisions already made'

fail() { echo "check-index: FAIL — $*" >&2; exit 1; }

[ -f "$DECISIONS" ] || fail "no $DECISIONS"
[ -f "$INDEX" ] || fail "no $INDEX"

# headings <decisions-file> — the record's numbers, one per line, sorted.
headings() { sed -n 's/^## \(D-[0-9][0-9]*\).*$/\1/p' "$1" | sort -u; }

# malformed <decisions-file> — heading lines that name a decision but are not
# `## D-<digits>`: wrong depth, up to three leading spaces (still a heading to
# Markdown), emphasis or a link around the number (`## **D-130**`), a
# lower-case d, a space or dash variant, a missing hyphen. C locale so an en
# dash is two non-letter bytes, never a letter of some locale's.
malformed() {
	LC_ALL=C grep -nE '^ {0,3}#+[^A-Za-z0-9]*[Dd]([^A-Za-z]|$)' "$1" | grep -vE '^[0-9]+:## D-[0-9]+([^0-9]|$)' || true
}

# section <index-file> — the lines of the decisions section, heading excluded.
# The heading must appear exactly once: a respelled heading would otherwise
# leave an empty section, and two would leave a question which one counts.
# Matched as the whole heading, or the heading and its " — " tail ("do not
# relitigate"): `## Decisions already madeup` and `## Decisions already made
# elsewhere` are other sections, neither counted nor confused with it.
ISHEAD='$0 == h || index($0, h " — ") == 1'
section() {
	n=$(awk -v h="$SECTION" "$ISHEAD { n++ } END { print n + 0 }" "$1")
	[ "$n" = 1 ] || fail "$1 has $n '$SECTION' headings, want 1"
	awk -v h="$SECTION" "$ISHEAD { on = 1; next } on && /^## / { on = 0 } on" "$1"
}

# indexed <index-file> — the numbers on the section's lines, one per line,
# sorted. The leading character class is what makes a token whole on the left;
# grep -o's maximal digit run makes it whole on the right.
indexed() {
	section "$1" > "$tmp/section" || exit 1
	grep -oE '(^|[^A-Za-z0-9_-])D-[0-9]+' "$tmp/section" | sed 's/^[^D]*//' | sort -u
}

# unindexed <decisions> <index> — record numbers no index line names.
# dangling <decisions> <index> — index numbers with no record heading.
unindexed() { headings "$1" > "$tmp/h"; indexed "$2" > "$tmp/i" || exit 1; comm -23 "$tmp/h" "$tmp/i"; }
dangling() { headings "$1" > "$tmp/h"; indexed "$2" > "$tmp/i" || exit 1; comm -13 "$tmp/h" "$tmp/i"; }

tmp=$(mktemp -d -t check-index.XXXXXX)
trap 'rm -rf "$tmp"' EXIT INT TERM

# expect <clause> <real> <planted> <want> — a planted fault must ADD exactly its
# number to what the same scan reports on the real files; anything else means
# the scan, not the docs, is broken. Judged as a difference, not as "planted
# reports only <want>", so a real gap in the docs reaches the real-file verdict
# below and is named as one, instead of tripping a clause and blaming the gate.
# It costs no strength: GREEN needs the real report EMPTY, and against an empty
# real report the difference is the planted report itself.
expect() {
	printf '%s\n' "$2" | sed '/^$/d' > "$tmp/real"
	printf '%s\n' "$3" | sed '/^$/d' > "$tmp/planted"
	got=$(comm -13 "$tmp/real" "$tmp/planted")
	[ "$got" = "$4" ] || fail "clause '$1': the plant added '$got' to the scan's report, want '$4' — the gate is broken, not the docs"
}

real_un=$(unindexed "$DECISIONS" "$INDEX")
real_dang=$(dangling "$DECISIONS" "$INDEX")

# Clause 1: a planted heading no index line names is flagged, though the
# section carries near-misses that are not its number: a letter on the left
# (XD-999, the left boundary) and a longer digit run (D-9990, the right).
cp "$DECISIONS" "$tmp/d1"
printf '\n## D-999 — a planted decision no index line names\n' >> "$tmp/d1"
awk -v h="$SECTION" -v p='- near-misses of the planted number: XD-999, D-9990' "{ print } $ISHEAD { print \"\"; print p }" "$INDEX" > "$tmp/c1"
grep -q 'XD-999' "$tmp/c1" || fail "clause 'planted heading': the near-misses did not land"
expect "planted heading" "$real_un" "$(unindexed "$tmp/d1" "$tmp/c1")" "D-999"

# Clause 2: the same number cited OUTSIDE the section is still flagged: it is
# not counted. Planted twice, just above the heading (where "Operating rules"
# would cite it) and in a section appended after it, so both bounds are armed.
awk -v h="$SECTION" -v p='- a rule citing D-999 above the index' "$ISHEAD { print p; print \"\" } { print }" "$INDEX" > "$tmp/c2"
printf '\n## A later section\n\n- a rule citing D-999 below the index\n' >> "$tmp/c2"
[ "$(grep -c 'citing D-999' "$tmp/c2")" = 2 ] || fail "clause 'outside the section': the plants did not land"
expect "outside the section" "$real_un" "$(unindexed "$tmp/d1" "$tmp/c2")" "D-999"

# Clause 3: a heading removed from the record while its number stays on an
# index line is flagged by the reverse direction. D-001 is on the "Ledger"
# line today; if that ever changes, this clause says so rather than pass.
grep -vE '^## D-001([^0-9]|$)' "$DECISIONS" > "$tmp/d3" || true
cmp -s "$DECISIONS" "$tmp/d3" && fail "clause 'removed heading': the plant did not land (no '## D-001' heading)"
expect "removed heading" "$real_dang" "$(dangling "$tmp/d3" "$INDEX")" "D-001"

# Clause 4: a planted heading whose number is a PREFIX of an indexed one
# (D-12 of D-120..D-129) is flagged: D-120's line does not satisfy D-12.
cp "$DECISIONS" "$tmp/d4"
printf '\n## D-12 — a planted prefix of an indexed number\n' >> "$tmp/d4"
expect "prefix" "$real_un" "$(unindexed "$tmp/d4" "$INDEX")" "D-12"

# Clause 5: a decision heading the scan cannot parse fails by itself.
cp "$DECISIONS" "$tmp/d5"
printf '\n### D-998 — wrong depth\n## **D-997** — emphasis\n## d-996 — lower case\n' >> "$tmp/d5"
got=$(malformed "$tmp/d5")
for plant in '### D-998' '## **D-997**' '## d-996'; do
	case $got in
		*"$plant"*) ;;
		*) fail "clause 'malformed heading': the planted '$plant' was not reported — the gate is broken, not the docs" ;;
	esac
done

# The real files.
bad=$(malformed "$DECISIONS")
[ -z "$bad" ] || { echo "check-index: $DECISIONS has decision headings not spelled '## D-<digits>':" >&2; echo "$bad" | sed 's/^/  /' >&2; exit 1; }
missing=$real_un
extra=$real_dang
if [ -n "$missing" ] || [ -n "$extra" ]; then
	if [ -n "$missing" ]; then
		echo "check-index: decisions on no line of $INDEX's '$SECTION' section:" >&2
		echo "$missing" | sed 's/^/  /' >&2
		echo "  Add each number to its subject's line (D-118: amend the line, a new line only for a new subject)." >&2
	fi
	if [ -n "$extra" ]; then
		echo "check-index: numbers in $INDEX's '$SECTION' section with no '## D-NNN' heading in $DECISIONS:" >&2
		echo "$extra" | sed 's/^/  /' >&2
	fi
	exit 1
fi
echo "check-index: GREEN (5 planted faults flagged; $(headings "$DECISIONS" | wc -l | tr -d ' ') decisions all indexed, none dangling)"
