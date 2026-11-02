#!/bin/sh
# check-charter.sh — done-check: every decision in the record is a GIVEN in the
# review charter.
#
# WHY A GATE. `scripts/codex-review.sh` prepends docs/review-charter.md to every
# prompt, and the charter is the ONLY thing a reviewer is handed for free — it
# does not read docs/decisions.md, and it has no way to know that file exists.
# So a decision that is recorded but not charted is, from the reviewer's side,
# an open question: measured on this tree (review, 2026-09-08), the charter
# carried no D-number at all and its GIVENs stopped three decisions short, so
# D-012, D-013 and D-014 were re-proposable — and re-proposing a settled ruling
# is exactly the review budget the charter exists to buy back. The drift is also
# the silent kind: the charter keeps working, the review keeps returning
# findings, and nothing anywhere says the two files have diverged.
#
# WHAT IT CHECKS. Every `## D-NNN` heading in docs/decisions.md has its D-number
# mentioned SOMEWHERE in docs/review-charter.md. Coarse on purpose — a mention
# is not a summary, and this gate cannot judge whether the GIVEN is faithful. It
# catches the failure that actually happened (a decision with no line in the
# charter at all), and it makes the two files cross-checkable by grep.
#
# TWO CLAUSES (the convention check.sh and check-fence.sh use). Clause 2 is a
# planted decision id the scan MUST flag, next to a real one it must NOT, so a
# respelled heading pattern that silently matches nothing cannot pass as "every
# decision is covered" — a scan that finds no headings reports the same GREEN as
# a scan that found them all covered.
#
# THE ADAPTER SPLIT (D-128). The GIVENs only adapter code rests on live in full in
# docs/review-charter-adapter.md, which codex-review.sh appends only under
# CODEX_CHARTER=adapter; the main charter keeps each as a ONE-LINE stub. So the
# coverage check above stays on the main charter alone (what a pass without the flag
# sees), and a second check pairs the two files, one failure kind each:
#   nostub N       a full text in the adapter file with no GIVEN N in the charter: a
#                  pass without the flag would not know the fence exists;
#   both N         GIVEN N is full text in the charter AND in the adapter file: two
#                  copies drift, and the flag would send it twice;
#   multiline N    a stub longer than one line (the split's saving is the point);
#   dmissing N D   a D-number the full text names that its stub does not;
#   fence N        a full text with a "Do not propose|cite" or "Excluded" whose stub
#                  names no fence: the reviewer must see the fence without the flag;
#   orphan N       a stub whose full text is gone: the pointer leads nowhere;
#   dup N          a GIVEN number twice in one file;
#   outside N      a stub outside the charter's "## Settled decisions" section, which
#                  the pairing would otherwise never see (Codex code pass);
#   malformed L    an adapter-file line at column 0 that is not a GIVEN start (`82a.`
#                  would otherwise fold into GIVEN 82; Codex code pass);
#   empty F        a file that parses to no GIVEN at all: with the core side empty,
#                  awk's FNR == NR would read the ADAPTER file as the core one and
#                  report nothing (Codex code pass).
# The fence kind proves a stub names A fence when its full text has one, not that it
# names every one: which excluded alternatives a stub must keep is a judgement the
# review makes, not a grep.
# A stub is a GIVEN line carrying the literal `(full text: `CODEX_CHARTER=adapter`)`.
# Its planted fixture yields each kind exactly once beside a correct pair that
# yields nothing, so a pairing that stopped parsing cannot pass as "all paired".
#
# DELIBERATELY NOT CHECKED: the reverse direction, a charter citing a D-number
# with no heading behind it. It is a typo, not a safety gap — the charter still
# states the GIVEN — and the clause would need a second fixture to arm it. Add
# it the day a dangling citation actually misleads somebody.
set -eu
. "$(dirname -- "$0")/lib.sh"
CDPATH= cd -- "$(repo_root "$0")"

DECISIONS=docs/decisions.md
CHARTER=docs/review-charter.md
ADAPTER=docs/review-charter-adapter.md
MARK='(full text: `CODEX_CHARTER=adapter`)'

fail() { echo "check-charter: FAIL — $*" >&2; exit 1; }

[ -f "$DECISIONS" ] || fail "no $DECISIONS"
[ -f "$CHARTER" ] || fail "no $CHARTER"
[ -f "$ADAPTER" ] || fail "no $ADAPTER"

# uncovered <decisions-file> <charter-file> — one D-number per line, for every
# heading in the decision record the charter never names. grep -F because a
# D-number is a literal, and the whole file because a GIVEN may cite its
# decision anywhere in its sentence.
uncovered() {
	sed -n 's/^## \(D-[0-9][0-9]*\).*$/\1/p' "$1" | while IFS= read -r id; do
		grep -qF -- "$id" "$2" || printf '%s\n' "$id"
	done
}

# givens <file> <whole> — one record per GIVEN: number, lines, stub (0/1), fence
# (0/1), then its D-numbers, tab-separated. A GIVEN starts at `N. **` and runs to the
# next one or a `## ` heading, blank lines inside it included (a second paragraph's
# D-number still counts); its line count is its non-blank lines. <whole>=0 reads only
# the charter's "## Settled decisions" section (the lists elsewhere are not GIVENs).
givens() {
	awk -v whole="$2" -v mark="$MARK" '
		function flush() {
			if (num == "") return
			printf "%s\t%d\t%d\t%d\t%s\n", num, nl, stub, fence, ds
			num = ""
		}
		/^## / { flush(); if (!whole) on = (index($0, "## Settled decisions") == 1); next }
		!(whole || on) {
			if ($0 ~ /^[0-9]+\. \*\*/ && index($0, mark)) { n = $0; sub(/\..*/, "", n); print "OUTSIDE\t" n }
			next
		}
		whole && seen && /^[^[:space:]]/ && !/^[0-9]+\. \*\*/ { flush(); print "MALFORMED\t" FNR; next }
		/^[0-9]+\. \*\*/ { flush(); num = $0; sub(/\..*/, "", num); nl = 0; stub = 0; fence = 0; ds = ""; seen = 1 }
		/^[[:space:]]*$/ { next }
		num != "" {
			nl++
			if (index($0, mark)) stub = 1
			if ($0 ~ /[Dd]o not (re-)?(propose|cite)|Excluded/) fence = 1
			line = $0
			while (match(line, /D-[0-9][0-9][0-9]/)) {
				d = substr(line, RSTART, RLENGTH)
				if (index(" " ds " ", " " d " ") == 0) ds = (ds == "" ? d : ds " " d)
				line = substr(line, RSTART + RLENGTH)
			}
		}
		END { flush() }' "$1"
}

# pairing <charter> <adapter> — one `<kind> <N> [D-NNN]` line per failure above.
pairing() {
	givens "$1" 0 > "$tmpd/core"
	givens "$2" 1 > "$tmpd/adapter"
	for f in core adapter; do
		grep -q '^[0-9]' "$tmpd/$f" || echo "empty $f"
	done
	# A sentinel line between the two marks the core side's end, so an empty core can never
	# make awk read the adapter file as the first one.
	{ cat "$tmpd/core"; printf 'END-OF-CORE\n'; cat "$tmpd/adapter"; } | awk -F '\t' '
		$1 == "END-OF-CORE" { core_done = 1; next }
		$1 == "OUTSIDE" { print "outside " $2; next }
		$1 == "MALFORMED" { print "malformed " $2; next }
		!core_done { if (cn[$1]++) print "dup " $1; cl[$1] = $2; cs[$1] = $3; cf[$1] = $4; cd[$1] = $5; next }
		{
			if (an[$1]++) { print "dup " $1; next }
			if (!($1 in cl)) { print "nostub " $1; next }
			if (!cs[$1]) { print "both " $1; next }
			n = split($5, ds, " ")
			for (i = 1; i <= n; i++) if (index(" " cd[$1] " ", " " ds[i] " ") == 0) print "dmissing " $1 " " ds[i]
			if ($4 && !cf[$1]) print "fence " $1
		}
		END {
			for (k in cl) {
				if (cs[k] && cl[k] > 1) print "multiline " k
				if (cs[k] && !(k in an)) print "orphan " k
			}
		}' | sort
}

# Clause 2 first: the positive control. Two headings — one the charter really
# does name, one it cannot — so this proves the heading pattern still matches
# AND that a covered decision is not reported. If the scan is wrong about this
# fixture, its verdict on the real record means nothing.
tmpd=$(mktemp -d -t charter-check.XXXXXX)
trap 'rm -rf "$tmpd"' EXIT INT TERM
control=$tmpd/control
cat > "$control" <<'MD'
## D-001 — a decision the charter really does cite
## D-999 — a planted decision the charter cannot possibly cite
MD
got=$(uncovered "$control" "$CHARTER")
case $got in
	D-999) ;;
	*) fail "the scan did not flag its own planted decision (got '$got', want 'D-999') — the gate is broken, not the docs" ;;
esac

# The pairing's own planted pair: GIVEN 1 is a correct stub and full text (the
# positive control: it must yield nothing), and each other number arms one kind.
cat > "$tmpd/fx-core" <<MD
## Settled decisions (GIVENs)

1. **A correct stub (D-901).** Do not propose: x. $MARK

2. **Full text here and there (D-902).**
    a second line.

3. **A stub missing a D-number (D-903).** $MARK

4. **A stub naming no fence (D-904).** $MARK

5. **A stub on two lines (D-905).** Do not propose: x.
    $MARK

6. **A stub whose full text is gone (D-906).** $MARK

8. **A stub (D-908).** $MARK

## Environment facts

9. **Not a GIVEN: outside the settled section.**

10. **A stub outside the settled section (D-910).** $MARK
MD
cat > "$tmpd/fx-adapter" <<'MD'
# planted adapter file

1. **A correct stub (D-901).** Do not propose: x.

2. **Full text here and there (D-902).**

3. **A stub missing a D-number (D-903, D-913).**

4. **A stub naming no fence (D-904).** Excluded, do not re-propose: y.

5. **A stub on two lines (D-905).** Do not propose: x.

7. **No stub anywhere (D-907).**

8. **A stub (D-908).**

8. **A stub (D-908).**

8a. **A respelled number, not a GIVEN start (D-908).**

9. **Not a GIVEN: outside the settled section.**
MD
got=$(pairing "$tmpd/fx-core" "$tmpd/fx-adapter" | tr '\n' ';')
want='both 2;dmissing 3 D-913;dup 8;fence 4;malformed 19;multiline 5;nostub 7;nostub 9;orphan 6;outside 10;'
[ "$got" = "$want" ] || fail "the pairing reported '$got' on its planted pair, want '$want' — the gate is broken, not the docs"
# A charter whose settled section is renamed parses to nothing: that must fail, not
# pass with every adapter GIVEN read as a core one.
sed 's/^## Settled decisions/## Decisions/' "$tmpd/fx-core" > "$tmpd/fx-renamed"
got=$(pairing "$tmpd/fx-renamed" "$tmpd/fx-adapter" | grep -c '^empty core$' || true)
[ "$got" = 1 ] || fail "a charter with no settled section was not reported empty — the gate is broken, not the docs"

bad=$(pairing "$CHARTER" "$ADAPTER")
if [ -n "$bad" ]; then
	echo "check-charter: $CHARTER and $ADAPTER disagree (D-128):" >&2
	echo "$bad" | sed 's/^/  /' >&2
	echo "  Each GIVEN in $ADAPTER keeps a one-line stub in $CHARTER under its number:" >&2
	echo "  its rule, every D-number, its fences, and $MARK." >&2
	exit 1
fi
moved=$(cut -f1 "$tmpd/adapter" | tr '\n' ' ' | sed 's/ $//')

missing=$(uncovered "$DECISIONS" "$CHARTER")
if [ -n "$missing" ]; then
	echo "check-charter: decisions with no GIVEN in $CHARTER:" >&2
	echo "$missing" | sed 's/^/  /' >&2
	echo "  Codex sees only the charter. Add a one-line GIVEN naming each D-number," >&2
	echo "  in the voice of the ones already there, or the ruling gets re-proposed." >&2
	exit 1
fi
echo "check-charter: GREEN (planted decision flagged, planted pair's 11 failures found, empty charter caught; $(sed -n 's/^## \(D-[0-9][0-9]*\).*$/\1/p' "$DECISIONS" | wc -l | tr -d ' ') decisions all charted; adapter GIVENs $moved stubbed)"
