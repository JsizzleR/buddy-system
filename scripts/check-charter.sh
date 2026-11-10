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
# THE PROCESS PART (D-147, Codex report A4). docs/review-charter-process.md holds the
# GIVENs only process text rests on, appended under CODEX_CHARTER=process (or `all`),
# its stubs marked `(full text: `CODEX_CHARTER=process`)`. Each part file is paired
# with the charter by the same check under its own mark (PARTS below), and one more
# check runs across them, so every GIVEN is in exactly one part:
#   twoparts N     GIVEN N in full in two part files (the `all` value would send it
#                  twice, and a stub can name only one);
#   unknownpart N  a stub naming a `CODEX_CHARTER=` value with no part file, by exact
#                  membership (`adapter process` is one): the pointer leads to a flag
#                  the script refuses.
# A stub naming one part whose full text is in another is already two failures of the
# pairing: `orphan` in its own part, `both` in the other.
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
PROCESS=docs/review-charter-process.md
# PARTS: one `<value> <file>` per stubbed part; the value is codex-review.sh's
# CODEX_CHARTER word that appends the file.
PARTS="adapter $ADAPTER
process $PROCESS"
mark() { printf '(full text: `CODEX_CHARTER=%s`)' "$1"; }
MARK=$(mark adapter)

fail() { echo "check-charter: FAIL — $*" >&2; exit 1; }

[ -f "$DECISIONS" ] || fail "no $DECISIONS"
[ -f "$CHARTER" ] || fail "no $CHARTER"
[ -f "$ADAPTER" ] || fail "no $ADAPTER"
[ -f "$PROCESS" ] || fail "no $PROCESS"

# uncovered <decisions-file> <charter-file> — one D-number per line, for every
# heading in the decision record the charter never names. grep -F because a
# D-number is a literal, and the whole file because a GIVEN may cite its
# decision anywhere in its sentence.
uncovered() {
	sed -n 's/^## \(D-[0-9][0-9]*\).*$/\1/p' "$1" | while IFS= read -r id; do
		grep -qF -- "$id" "$2" || printf '%s\n' "$id"
	done
}

# givens <file> <whole> [<mark>] — one record per GIVEN: number, lines, stub (0/1), fence
# (0/1), then its D-numbers, tab-separated. A GIVEN starts at `N. **` and runs to the
# next one or a `## ` heading, blank lines inside it included (a second paragraph's
# D-number still counts); its line count is its non-blank lines. <whole>=0 reads only
# the charter's "## Settled decisions" section (the lists elsewhere are not GIVENs).
# <mark> is the stub marker of the part being paired (default the adapter's).
givens() {
	awk -v whole="$2" -v mark="${3-$MARK}" '
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

# pairing <charter> <part-file> [<mark>] — one `<kind> <N> [D-NNN]` line per failure
# above, the part's stubs being the charter GIVENs that carry <mark>.
pairing() {
	givens "$1" 0 "${3-$MARK}" > "$tmpd/core"
	givens "$2" 1 "${3-$MARK}" > "$tmpd/adapter"
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

# crosscheck <charter> <part-file>... — `twoparts N` for a GIVEN in full in two part
# files, `unknownpart N` for a charter stub naming a value no part has (the known
# values are PARTS's). Its own planted fixture arms both below.
crosscheck() {
	_c=$1; shift
	for _f in "$@"; do givens "$_f" 1 | cut -f1 | grep '^[0-9]' | sort -u; done | sort | uniq -d | sed 's/^/twoparts /'
	# Exact membership, not a substring of the joined list: `adapter process` is in
	# " adapter process " and is a value codex-review.sh refuses (Codex code pass).
	awk -v known="$(printf '%s\n' "$PARTS" | cut -d' ' -f1 | tr '\n' ' ')" '
		BEGIN { n = split(known, k, " "); for (i = 1; i <= n; i++) ok[k[i]] = 1 }
		/^## / { on = (index($0, "## Settled decisions") == 1); next }
		on && /^[0-9]+\. \*\*/ {
			line = $0
			while (match(line, /\(full text: `CODEX_CHARTER=[^`]*`\)/)) {
				v = substr(line, RSTART + 27, RLENGTH - 29) # after "(full text: `CODEX_CHARTER=", before "`)"
				if (!(v in ok)) { n = $0; sub(/\..*/, "", n); print "unknownpart " n }
				line = substr(line, RSTART + RLENGTH)
			}
		}' "$_c"
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
# The same planted pair under the PROCESS mark: the pairing is the same check per part,
# so the fixture respelled with the other value must report the same eleven failures.
P=$(mark process)
for f in fx-core fx-adapter; do awk -v a="$MARK" -v b="$P" '{ while ((i = index($0, a)) > 0) $0 = substr($0, 1, i - 1) b substr($0, i + length(a)); print }' "$tmpd/$f" > "$tmpd/p$f"; done
got=$(pairing "$tmpd/pfx-core" "$tmpd/pfx-adapter" "$P" | tr '\n' ';')
[ "$got" = "$want" ] || fail "the pairing reported '$got' on its planted pair under the process mark, want '$want' — the gate is broken, not the docs"

# The cross-part check's planted set: 1 and 2 are correct stubs of each part (its
# positive control), 3 names a part that does not exist, 4 is in full in both parts,
# 5 names two parts joined by a space (each a real part, the value no part).
cat > "$tmpd/x-core" <<MD
## Settled decisions (GIVENs)

1. **An adapter stub (D-901).** Do not propose: x. $(mark adapter)

2. **A process stub (D-902).** Do not propose: x. $(mark process)

3. **A stub to no part (D-903).** Do not propose: x. $(mark bogus)

4. **A stub (D-904).** Do not propose: x. $(mark process)

5. **A stub to two parts (D-905).** Do not propose: x. $(mark 'adapter process')
MD
printf '1. **An adapter stub (D-901).** Do not propose: x.\n\n4. **A stub (D-904).** Do not propose: x.\n' > "$tmpd/x-adapter"
printf '2. **A process stub (D-902).** Do not propose: x.\n\n4. **A stub (D-904).** Do not propose: x.\n' > "$tmpd/x-process"
got=$(crosscheck "$tmpd/x-core" "$tmpd/x-adapter" "$tmpd/x-process" | sort | tr '\n' ';')
[ "$got" = 'twoparts 4;unknownpart 3;unknownpart 5;' ] || fail "the cross-part check reported '$got' on its planted set, want 'twoparts 4;unknownpart 3;unknownpart 5;' — the gate is broken, not the docs"

# The parts come from PARTS, not a second list here, and the count is checked after:
# a loop that skipped a part would report nothing for it and stay green (mutation).
moved= np=0
for _v in $(printf '%s\n' "$PARTS" | cut -d' ' -f1); do
	np=$((np + 1))
	_f=$(printf '%s\n' "$PARTS" | awk -v v="$_v" '$1 == v { print $2 }')
	bad=$(pairing "$CHARTER" "$_f" "$(mark "$_v")")
	if [ -n "$bad" ]; then
		echo "check-charter: $CHARTER and $_f disagree (D-128, D-147):" >&2
		echo "$bad" | sed 's/^/  /' >&2
		echo "  Each GIVEN in $_f keeps a one-line stub in $CHARTER under its number:" >&2
		echo "  its rule, every D-number, its fences, and $(mark "$_v")." >&2
		exit 1
	fi
	moved="$moved${moved:+; }$_v $(cut -f1 "$tmpd/adapter" | tr '\n' ' ' | sed 's/ $//')"
done
[ "$np" = 2 ] && [ "$np" = "$(printf '%s\n' "$PARTS" | grep -c .)" ] || fail "paired $np charter part(s), want the 2 PARTS names (adapter, process) — the gate is broken, not the docs"
bad=$(crosscheck "$CHARTER" "$ADAPTER" "$PROCESS")
if [ -n "$bad" ]; then
	echo "check-charter: the charter's parts overlap or point nowhere (D-147):" >&2
	echo "$bad" | sed 's/^/  /' >&2
	echo "  Each GIVEN is in full in exactly one file, and a stub names a part that exists." >&2
	exit 1
fi

# THE FOLD (Codex report A5). codex-review.sh folds every prompt to ASCII through
# scripts/ascii-fold.py, and a character its table does not name becomes `?`. The
# charter's two GIVENs on `fence.Field` said it renders a space as `␣` and an empty
# result as `∅`; neither was in the table, so every reviewer read that it renders a
# space as `?`. So: fold each charter file through the SAME file the review uses, and
# fail if the folded text has a `?` its source lacks. Two clauses: a planted line of
# mapped characters (`␣`, `∅`, `—`) and a real `?` must NOT be flagged (that line is
# also what fails when a row goes missing), and a planted unmapped character MUST be.
FOLD=scripts/ascii-fold.py
[ -f "$FOLD" ] || fail "no $FOLD (codex-review.sh folds every prompt through it)"
qcount() { LC_ALL=C tr -cd '?' | wc -c | tr -d ' '; }
foldlies() { # <file> — one line naming the file when folding it adds a `?`
	# The fold's own status first: a pipeline into qcount would report qcount's, and a
	# fold that died before its last `?` would count clean.
	python3 -I -B "$FOLD" < "$1" > "$tmpd/folded" || { printf '%s: the fold itself failed\n' "$1"; return 0; }
	_src=$(qcount < "$1")
	_got=$(qcount < "$tmpd/folded")
	[ "$_src" = "$_got" ] || printf '%s: %s `?` in the source, %s after the fold\n' "$1" "$_src" "$_got"
}
printf 'a space renders as ␣, an empty result as ∅ — is that right?\n' > "$tmpd/fold-mapped"
printf 'a star ★ has no row?\n' > "$tmpd/fold-unmapped"
[ -z "$(foldlies "$tmpd/fold-mapped")" ] || fail "the fold turned a mapped character into \`?\`: $(foldlies "$tmpd/fold-mapped") — a row of $FOLD is missing (\`␣\` and \`∅\` are fence.Field's)"
[ -n "$(foldlies "$tmpd/fold-unmapped")" ] || fail "a planted unmapped character was not flagged — the gate is broken, not the docs"
bad=$(foldlies "$CHARTER"; foldlies "$ADAPTER"; foldlies "$PROCESS")
if [ -n "$bad" ]; then
	echo "check-charter: the ASCII fold codex-review.sh applies turns charter text into \`?\`:" >&2
	echo "$bad" | sed 's/^/  /' >&2
	echo "  Add a row for each new non-ASCII character to $FOLD, or write it in ASCII." >&2
	exit 1
fi

missing=$(uncovered "$DECISIONS" "$CHARTER")
if [ -n "$missing" ]; then
	echo "check-charter: decisions with no GIVEN in $CHARTER:" >&2
	echo "$missing" | sed 's/^/  /' >&2
	echo "  Codex sees only the charter. Add a one-line GIVEN naming each D-number," >&2
	echo "  in the voice of the ones already there, or the ruling gets re-proposed." >&2
	exit 1
fi
echo "check-charter: GREEN (planted decision flagged, planted pair's 11 failures found under each part's mark, cross-part pair found, empty charter caught, fold control flagged and charter folds clean; $(sed -n 's/^## \(D-[0-9][0-9]*\).*$/\1/p' "$DECISIONS" | wc -l | tr -d ' ') decisions all charted; GIVENs stubbed: $moved)"
