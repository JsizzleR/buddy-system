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
# DELIBERATELY NOT CHECKED: the reverse direction, a charter citing a D-number
# with no heading behind it. It is a typo, not a safety gap — the charter still
# states the GIVEN — and the clause would need a second fixture to arm it. Add
# it the day a dangling citation actually misleads somebody.
set -eu
. "$(dirname -- "$0")/lib.sh"
CDPATH= cd -- "$(repo_root "$0")"

DECISIONS=docs/decisions.md
CHARTER=docs/review-charter.md

fail() { echo "check-charter: FAIL — $*" >&2; exit 1; }

[ -f "$DECISIONS" ] || fail "no $DECISIONS"
[ -f "$CHARTER" ] || fail "no $CHARTER"

# uncovered <decisions-file> <charter-file> — one D-number per line, for every
# heading in the decision record the charter never names. grep -F because a
# D-number is a literal, and the whole file because a GIVEN may cite its
# decision anywhere in its sentence.
uncovered() {
	sed -n 's/^## \(D-[0-9][0-9]*\).*$/\1/p' "$1" | while IFS= read -r id; do
		grep -qF -- "$id" "$2" || printf '%s\n' "$id"
	done
}

# Clause 2 first: the positive control. Two headings — one the charter really
# does name, one it cannot — so this proves the heading pattern still matches
# AND that a covered decision is not reported. If the scan is wrong about this
# fixture, its verdict on the real record means nothing.
control=$(mktemp -t charter-control.XXXXXX)
trap 'rm -f "$control"' EXIT INT TERM
cat > "$control" <<'MD'
## D-001 — a decision the charter really does cite
## D-999 — a planted decision the charter cannot possibly cite
MD
got=$(uncovered "$control" "$CHARTER")
case $got in
	D-999) ;;
	*) fail "the scan did not flag its own planted decision (got '$got', want 'D-999') — the gate is broken, not the docs" ;;
esac

missing=$(uncovered "$DECISIONS" "$CHARTER")
if [ -n "$missing" ]; then
	echo "check-charter: decisions with no GIVEN in $CHARTER:" >&2
	echo "$missing" | sed 's/^/  /' >&2
	echo "  Codex sees only the charter. Add a one-line GIVEN naming each D-number," >&2
	echo "  in the voice of the ones already there, or the ruling gets re-proposed." >&2
	exit 1
fi
echo "check-charter: GREEN (planted decision flagged; $(sed -n 's/^## \(D-[0-9][0-9]*\).*$/\1/p' "$DECISIONS" | wc -l | tr -d ' ') decisions all charted)"
