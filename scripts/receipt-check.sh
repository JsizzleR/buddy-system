#!/bin/sh
# receipt-check.sh — which landed commits' review receipts do not name a layer
# their tier owes (D-104's receipt check, D-143). A REPORT: it prints, and it
# refuses nothing. Commit ids, tier and layer-kind literals and counts only:
# no message text is ever printed.
#
#   sh scripts/receipt-check.sh [<revision range>]     (default HEAD)
#
# WHY IT EXISTS. D-104 said what each tier owes and that the receipt names
# every layer that ran, and deferred the check until receipts had a shape to
# hold; D-141 built the parser and deferred it again. Nothing held a commit to
# its tier, so a `full` with no cross-check and no stated reason read the same
# as a complete one unless somebody read the log by eye.
#
# WHY A REPORT AND NOT A GATE. `check.sh hermetic` is the pre-push hook's
# tier, and a landed commit's message cannot be changed without rewriting
# main: a check.sh leg red over history would refuse every push from then on,
# for a commit nobody can fix. A refusal at commit time would be a new gate,
# the full tier (D-104), not asked for. So this runs on demand, over a range:
# the integrator's `main..<run tip>` before landing, or all of HEAD; its
# done-check (check-receipt-check.sh) holds it over PLANTED commits only.
#
# WHAT A TIER OWES (CLAUDE.md "Review depth", D-104, D-115):
#   full   panel, design, code, cross-check, reverify. The re-verify is of the
#          fixes, so it is not owed when a code layer wrote `0 raised` (D-141's
#          count: nothing raised, nothing to re-verify).
#   lite   self and code. A cross-check is optional.
#   chore  a reason: a parenthesis (`review: chore (docs)`).
# A layer the tier owes is skipped only on the operator's word, named with
# its reason in the scope note (`; fable cross-check skipped on the operator's
# cap`): a note segment carrying `skip`, `skipped` or `waived` stands for the
# kind its words name. The reason's wording is not judged.
# Also reported: a commit with no receipt line, one whose tier word is none
# of the three, a lite or full with no layer list, and, after D-141, a layer
# whose count group is MALFORMED.
#
# COUNTS OWED (D-163, amends D-141). D-141 made a layer's count group
# optional and deferred any tier change until counts existed; three weeks on,
# 5 of 320 layers at 1b0e87f carried one and no panel layer did, so a panel's yield was
# unknown by construction. From D-163's cut on, a lite or full receipt's
# panel, design, code, cross-check and reverify layers each owe a count group
# (`0 raised` when the layer found nothing): one with none is reported as
# `owes counts on <kind>`, once per kind. A `self` layer, a kind the reader
# cannot place (`other`) and a chore's parenthesis (its reason, not layers;
# a reason such as "no code" would read as a code layer) owe none. A MALFORMED
# group is already its own finding, so it is not reported twice. A skip-note
# row names no layer that ran, so it owes nothing either.
#
# WHAT IS NEVER HELD. A commit committed at or before D-104 (0e1a5d9, epoch
# 1793074046) is counted and not read: its receipt predates the rule. The
# count grammar is held only after D-141 (ffaf5bf, epoch 1794322479); one
# older receipt reads MALFORMED by that grammar (dc1b37a) and is not a
# finding. Counts are owed only after D-163's cut, the operator's "do them
# all" on the token-overhead report (2026-11-11 14:12:00Z, epoch 1794406320):
# main's last commit before it is 1b0e87f (05:48Z that day), and a lane's
# commit or a landing squash is committed after it, so history is never held
# for counts. Each cut is the COMMITTER epoch, not an ancestor test, so it
# survives a rewritten main; a landing squash commits at landing time.
#
# The reader is scripts/receipts.awk, the one model-report.sh uses, so the
# check and the report never read a receipt two ways.
#
# EXIT: 0 when it ran (findings or none); 2 a usage refusal; 1 not a git
# checkout or a range git cannot read.
set -eu
usage() { echo "usage: receipt-check.sh [<revision range>]" >&2; echo "refused: $1" >&2; exit 2; }
case $# in
0) range=HEAD ;;
1)	case $1 in
	-h | --help) echo "usage: receipt-check.sh [<revision range>]"; exit 0 ;;
	# A range is passed to git log; one spelled as an option would be read
	# as one (`--output=<file>` writes a file), so it is refused here and
	# `--end-of-options` stands behind that.
	-*) usage "a range does not begin with '-'" ;;
	'') usage "an empty range" ;;
	esac
	range=$1 ;;
*) usage "one range at most" ;;
esac
awkf=$(dirname -- "$0")/receipts.awk
[ -r "$awkf" ] || { echo "receipt-check: cannot read $awkf" >&2; exit 1; }
git rev-parse -q --git-dir >/dev/null 2>&1 || { echo "receipt-check: not a git checkout" >&2; exit 1; }
n=$(git rev-list --count --end-of-options "$range" 2>/dev/null) || {
	echo "receipt-check: git cannot read that range" >&2; exit 1; }

echo "Receipt check (D-104, D-143, D-163): a tier's owed layers, and owed counts, the receipt does not name; a report, it refuses nothing"
git log --no-color --format='%x00%H %ct%n%B' --end-of-options "$range" | tr '\001' ' ' | tr '\000' '\001' |
	LC_ALL=C awk -v OFS='\t' -v check=1 -f "$awkf" |
	LC_ALL=C awk -F'\t' -v n="$n" -v d104=1793074046 -v d141=1794322479 -v d163=1794406320 '
function owe(s, t, k) {
	if (k == "reverify" && zero[s]) return
	if (!((s, k) in has)) find(s, t "  owes " k)
}
function find(s, w) { F[s, ++nf[s]] = w; nfind++ }
{
	s = $1
	if (!(s in seen)) { seen[s] = 1; order[++ns] = s; tier[s] = $3; ct[s] = $2 + 0 }
	if ($6 == "skipped") { has[s, $5] = 1; next }
	if ($4 == 0) { nolist[s] = 1; next }
	has[s, $5] = 1
	if ($5 == "code" && $6 == "recorded" && $7 == "0") zero[s] = 1
	if ($6 == "malformed") mal[s]++
	# D-163: a layer that ran and wrote no count group. Rows of layer# 0
	# (no list, a skip note) returned above, so this is a listed layer.
	if ($6 == "none" && ($5 == "panel" || $5 == "design" || $5 == "code" || $5 == "cross-check" || $5 == "reverify")) bare[s, $5] = 1
}
function counts(s, t, k) { if ((s, k) in bare) find(s, t "  owes counts on " k) }
END {
	for (i = 1; i <= ns; i++) {
		s = order[i]; t = tier[s]
		# Rendered once: `other` and `absent` are internal words of the
		# reader and print as `-`, on every line (Codex code pass: a
		# malformed group under an unknown tier printed `other`).
		r = (t == "lite" || t == "full" || t == "chore") ? t : "-"
		if (ct[s] <= d104) { old++; continue }
		held++
		if (t == "absent") find(s, "-  no receipt line")
		else if (t == "other") find(s, "-  no tier word (lite, full or chore)")
		else if (nolist[s]) find(s, t "  " (t == "chore" ? "gives no reason" : "has no layer list"))
		else if (t == "full") { owe(s, t, "panel"); owe(s, t, "design"); owe(s, t, "code"); owe(s, t, "cross-check"); owe(s, t, "reverify") }
		else if (t == "lite") { owe(s, t, "self"); owe(s, t, "code") }
		if (mal[s] && ct[s] > d141) find(s, r "  " mal[s] " malformed count group(s)")
		if (ct[s] > d163 && (t == "lite" || t == "full") && !nolist[s]) {
			counts(s, t, "panel"); counts(s, t, "design"); counts(s, t, "code"); counts(s, t, "cross-check"); counts(s, t, "reverify")
		}
		if (nf[s]) nc++
	}
	printf "commits: %d; before D-104, not held: %d; held: %d, with a finding: %d (%d finding(s))\n", n, old, held, nc, nfind
	for (i = 1; i <= ns; i++) { s = order[i]; for (j = 1; j <= nf[s]; j++) printf "  %s  %s\n", substr(s, 1, 12), F[s, j] }
	if (ns != n) printf "read: %d commit(s) of %d; the rest were not read\n", ns, n
}'
