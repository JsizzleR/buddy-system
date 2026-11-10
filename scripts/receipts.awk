# receipts.awk — the ONE reader of commits' review receipts (D-141, D-143),
# shared by scripts/model-report.sh (what each layer found) and
# scripts/receipt-check.sh (what a tier owes and a receipt does not name).
# Two copies of this grammar would drift, and the check would then hold a
# commit to a receipt the report reads differently.
#
# INPUT: `git log --format='%x00%H %ct%n%B'`, a body's own 0x01 blanked and
# git's NUL turned into 0x01 (the caller's `tr`s): so a body cannot forge a
# commit's header line (D-141's Codex code pass).
#
# THE SUBJECT LINE IS NEVER READ, the rest of the message is (D-143). `%b`
# drops the whole subject PARAGRAPH, so a receipt written straight under the
# subject with no blank line was not read at all (#87). `%B` keeps it; only
# its first line is skipped, because `review: …` is also a subject prefix
# here and must never read as a receipt.
#
# The receipt is the LAST line beginning `review:` (either case), with the
# lines after it (it wraps; a trailer may follow). Its tier is the first
# word (lite full chore, else other); its layers are its first parenthesis,
# balanced, cut at the first depth-0 `;` (the scope note), split on a
# depth-0 ` + `. A layer may end in a count group after its last depth-0
# colon, `<n> raised, <n> fixed, <n> answered, <n> reproduced`, any subset,
# each word once; a count not written is NOT RECORDED, never zero; a group
# that starts with a digit and breaks the grammar is MALFORMED. Only
# literals, counts, the commit id and its epoch are printed: no receipt
# text leaves this program.
#
# ROWS: sha, epoch, tier, layer#, kind, state, raised, fixed, answered,
# reproduced (tab-separated). With no parenthesis: one row, layer# 0,
# `other none`. With `-v check=1` (receipt-check.sh only, never the report
# or its register) two more shapes: a commit with no receipt line is one
# row of tier `absent`; and each depth-0 segment of the scope note naming a
# skip (`skip`, `skipped`, `waived`) is a row of layer# 0, the kind its
# words name, state `skipped` (`; fable cross-check skipped on the
# operator's cap`, two commits on main, 57c11b0 and 0deb3a4). The note is
# read for that word only; its reason is not judged.
function kind(n) {
	n = tolower(n)
	if (n ~ /(^|[^a-z])self([^a-z]|$)/) return "self"
	if (n ~ /panel|lens/) return "panel"
	if (n ~ /re-?verify/) return "reverify"
	if (n ~ /design/) return "design"
	if (n ~ /@[^ ]* +code/ || n ~ /(^|[^a-z])code([^a-z]|$)/) return "code"
	# A bare Codex pass is the code pass: D-104's lite tier owed "ONE Codex
	# pass on the finished diff", and the receipts written in its first days
	# say `lite (codex@xhigh)` (88fa5fc, f5e36b7, 26e6003), which read as
	# `other` and would each have been held as a lite owing its code pass.
	# The BARE form only, the whole layer name: `codex@xhigh cross-check`
	# stays a cross-check (Codex code pass: a looser match read it as code).
	if (n ~ /^ *codex(@[a-z0-9._-]*)? *$/) return "code"
	if (n ~ /cross-?check|fable|opus|sonnet|haiku/) return "cross-check"
	return "other"
}
function flush(   s, t, p, i, d, c, inner, nl, L, C, j, nm, g, np, P, w, cnt, st, seen, k, out, nn, N) {
	if (sha == "") return
	if (rc == "") { if (check) print sha, ct, "absent", 0, "other", "none", "-", "-", "-", "-"; return }
	s = substr(rc, index(tolower(rc), "review:") + 7)
	t = ""; if (match(s, /[A-Za-z]+/)) t = tolower(substr(s, RSTART, RLENGTH))
	if (t != "lite" && t != "full" && t != "chore") t = "other"
	# The layer list opens straight after the tier word (D-143): a receipt
	# with no list whose prose holds a parenthesis (`review: full — panel x3
	# + codex@xhigh design (on drafted code) + …`, five on main) read that
	# aside as its layers, a `code` layer from "on drafted code".
	p = match(s, /^[^A-Za-z(]*[A-Za-z]+ *\(/) ? RLENGTH : 0
	if (!p) { print sha, ct, t, 0, "other", "none", "-", "-", "-", "-"; return }
	# The first parenthesis, balanced (a layer can hold its own), cut at
	# a depth-0 `;`; then the layers at a depth-0 ` + `. What follows the
	# cut, to the closing parenthesis, is the scope note, kept in depth-0
	# `;` segments for the skip rows only.
	d = 0; inner = ""; nn = 0
	for (i = p + 1; i <= length(s); i++) {
		c = substr(s, i, 1)
		if (c == "(") d++
		else if (c == ")") { if (!d) break; d-- }
		else if (c == ";" && !d) { N[++nn] = ""; continue }
		if (nn) N[nn] = N[nn] c
		else inner = inner c
	}
	# Depth-0 only, both: a parenthesis inside a layer may hold a ` + ` or
	# a colon (`code (API + CLI): 2 raised`, `code (scope: x): 2 raised`;
	# Codex code pass), so the group starts at the LAST depth-0 colon.
	nl = 1; L[1] = ""; C[1] = 0; d = 0
	for (i = 1; i <= length(inner); i++) {
		c = substr(inner, i, 1)
		if (c == "(") d++
		else if (c == ")" && d) d--
		if (!d && substr(inner, i, 3) == " + ") { nl++; L[nl] = ""; C[nl] = 0; i += 2; continue }
		L[nl] = L[nl] c
		if (!d && c == ":") C[nl] = length(L[nl])
	}
	for (j = 1; j <= nl; j++) {
		c = C[j]; nm = c ? substr(L[j], 1, c - 1) : L[j]
		cnt["raised"] = cnt["fixed"] = cnt["answered"] = cnt["reproduced"] = "-"
		st = "none"
		g = c ? substr(L[j], c + 1) : ""
		if (g ~ /^ *[0-9]/) {
			st = "recorded"; split("", seen)
			np = split(g, P, ",")
			for (k = 1; k <= np; k++) {
				w = P[k]; gsub(/^ +| +$/, "", w)
				if (w !~ /^[0-9]+ +(raised|fixed|answered|reproduced)$/) { st = "malformed"; break }
				sub(/ +/, " ", w); split(w, out, " ")
				if (out[2] in seen) { st = "malformed"; break }
				seen[out[2]] = 1; cnt[out[2]] = out[1] + 0
			}
			if (st == "malformed") cnt["raised"] = cnt["fixed"] = cnt["answered"] = cnt["reproduced"] = "-"
		}
		print sha, ct, t, j, kind(nm), st, cnt["raised"], cnt["fixed"], cnt["answered"], cnt["reproduced"]
	}
	if (check) for (k = 1; k <= nn; k++)
		if (tolower(N[k]) ~ /(^|[^a-z])(skip|skipped|waived)([^a-z]|$)/)
			print sha, ct, t, 0, kind(N[k]), "skipped", "-", "-", "-", "-"
}
/^\001/ { flush(); sha = substr($0, 2, index($0, " ") - 2); ct = substr($0, index($0, " ") + 1); rc = ""; on = 0; subj = 1; next }
subj { subj = 0; next }
tolower($0) ~ /^review:/ { on = 1; rc = "" }
on { rc = rc " " $0 }
END { flush() }
