#!/bin/sh
# check-prose-budget.sh — done-check (D-116): every text a session or a Codex
# pass loads UNASKED stays under its byte ceiling in docs/prose-budget.tsv.
#
# Usage: sh scripts/check-prose-budget.sh            judge this tree
#        sh scripts/check-prose-budget.sh --sizes    print every budgeted key's
#                                                    bytes and its formula ceiling
#        sh scripts/check-prose-budget.sh --members  print `class<TAB>key<TAB>bytes`
#                                                    for every enumerated member,
#                                                    then `-<TAB>key<TAB>bytes` for
#                                                    every other table key (a
#                                                    section); judges nothing
#
# WHY A GATE. The cost of a text is its size times the requests that carry it.
# CLAUDE.md rides every request of every session, a skill body every request
# after it loads, the charter every Codex pass. Nothing bounded any of them:
# CLAUDE.md went 16,252 -> 69,690 bytes in seven weeks, the skill 0 -> 73,144,
# the charter 10,497 -> 99,972, each step a reasonable commit, and the two gates
# over them (the skill's names, the charter's coverage) only push text IN.
# Measured 2026-10-28 (docs/PROSE-BUDGET-PLAN.md): 5,254 requests a week in this
# repo at a mean prompt of 210,695 tokens, CLAUDE.md about 8% of the cache reads.
# The claude-md and skill-split lanes cut those once; this is what holds after.
#
# WHAT IT CHECKS, per line of the table (key, ceiling_bytes, set, why):
#   - a key is a path, a section (`CLAUDE.md#Decisions`: from the `## ` heading
#     that starts with that text to the next `## `), a frontmatter field
#     (`skills/buddy/SKILL.md:description`), or one of the four class totals;
#   - bytes on disk (`wc -c`, LC_ALL=C), never tokens: a tokenizer is not in
#     the tier, and bytes/4 is the same estimate for every file, so the ranking
#     is unchanged. Not "rules" against "examples" either: a check that exempts
#     a kind of line teaches which kind to write;
#   - over its ceiling FAILS the tier (exit 1, never a warning: the commit
#     gate's cut warning fired on every commit and was ignored), printing the
#     overage, the file's five largest sections and the one way to raise it.
#
# THE ENUMERATION IS THE CHECK'S, NOT THE TABLE'S (the plan's first Codex pass,
# P1 twice). Per-file ceilings alone let a lane add a NEW always-loaded file
# under a new line (1 KB of rules a decision is 100-150 KB a quarter), and a
# table that lists what it budgets lets a lane delete the row. So this script
# carries its own fixed list of what the harnesses load unasked, sorts every hit
# into a class, and requires a table line for every hit AND every class total:
#   every-request  every CLAUDE.md / CLAUDE.local.md at any depth, .claude/rules,
#                  .claude/agents and .claude/commands, every `@` include they
#                  name, and every skill's `description` (the skills listing);
#   per-session    every skills/*/SKILL.md and .claude/skills/*/SKILL.md, whole;
#   read-first     every path CLAUDE.md's "## Read this first" section names and
#                  every path AGENTS.md's "Read ... before" paragraph names, plus
#                  ONE level of closure (a named file's own "read ... first"
#                  section joins; that file's list does not recurse);
#   codex-pass     AGENTS.md and the charter codex-review.sh prepends (the path
#                  is read from its CHARTER= line, not assumed).
# A class total is the sum of its members as ENUMERATED, so a new file spends
# its class's headroom and gets none of its own. A table line whose file or
# section is gone FAILS too: coverage cannot be removed by deleting the text.
# The files come from git's view (tracked, plus untracked-but-not-ignored), so
# a fresh clone, CI and the main checkout judge the same tree: the operator's
# ignored local files are outside the budget, as ~/.claude/CLAUDE.md is.
#
# A STARTUP DIRECTIVE OUTSIDE THE PARSED SECTION FAILS (the second pass's P1):
# a line of CLAUDE.md or AGENTS.md that says "read ... first" or "before
# working/anything/changing/exploring/editing/starting" AND names a path
# (backticked, linked or a bare *.md) must sit in the parsed section, or a list
# moved under another heading escapes the class. The path is required because
# CLAUDE.md says "read the release notes first" of a task, naming no file. A
# directive respelled past these forms is the cooperative residual (D-005's
# register): auto-loads and DECLARED startup reads are bounded, not every
# sentence that could send a session to a file. Also residual: a loader this
# list does not know (a new harness auto-load location); the audit's
# fixed-prefix median is the number that would show it.
#
# TWO CLAUSES (check.sh's convention). The judge runs first on a planted tree
# whose table must yield EXACTLY one over-ceiling file, one over-ceiling class
# whose member is under, one description over only past its blank line, one
# unbudgeted auto-load, one unbudgeted closure read, one unbudgeted import at
# the second hop, one unbudgeted titled link, one unbudgeted read in a SECOND
# "Read this first" section, one vanished line and one misplaced directive, and
# nothing else: not the file under its ceiling, not the path-less directive,
# not the second level of closure. A judge that stopped measuring, summing, enumerating or parsing
# reports a different set, and the real tree is never judged by it.
#
# HOW A CEILING MOVES. Raised only by editing its line, `set` and `why` dated
# and naming the operator's word, in the commit that needs the room; a lane
# never raises one (the core skill's landing recipe), it consolidates, retires
# or names the overage in READY. Lowered freely: a lane that trims a text
# lowers its line to the new size + 10% rounded up to 4,096 in the same commit
# (`--sizes` prints that formula's value), so headroom is not the next lane's
# room. A raise is a dated diff line in a file whose only purpose is to be read
# at landing; this does not try to bind the operator, only to make it visible.
#
# Not budgeted, on purpose: docs/decisions.md and the plans when no startup list
# names them (read by range: 452 of 453 calls in the measured week), and the
# operator's own ~/.claude/CLAUDE.md, which is outside the repo and is never a
# destination for repo text.
set -eu
. "$(dirname -- "$0")/lib.sh"
CDPATH= cd -- "$(repo_root "$0")"
# Byte counts and ASCII patterns. Also CLAUDE.md's locale trap: awk under a
# UTF-8 locale counts characters, and aborts on a byte it cannot convert.
LC_ALL=C; export LC_ALL

TABLE=docs/prose-budget.tsv
CLASSES="every-request per-session read-first codex-pass"
RAISE='consolidate or retire before raising; to raise, edit this key'"'"'s line in docs/prose-budget.tsv with the date and the reason, the operator'"'"'s word, in the same commit.'

fail() { echo "check-prose-budget: FAIL — $*" >&2; exit 1; }

# files <root> — the tree as git sees it, one path per line, existing files only
# (a deleted file stays in the index until staged).
files() {
	git -C "$1" -c core.quotePath=false ls-files -co --exclude-standard | sort -u |
		while IFS= read -r p; do [ -f "$1/$p" ] && printf '%s\n' "$p"; done
	return 0
}

# section <file> <heading-prefix> — the lines of every `## ` section whose
# heading starts with the prefix (followed by the end of the line or a space),
# each to the next `## `. EVERY one, not the first: a second "## Read this
# first" further down was a list nothing parsed (Codex code pass). Prints
# nothing when there is no such heading.
section() {
	awk -v h="## $2" '
		/^## / { on = (index($0, h) == 1 && (length($0) == length(h) || substr($0, length(h) + 1, 1) == " ")) }
		on { print }' "$1"
}

# readpara <file> — AGENTS.md's startup list: the first paragraph whose first
# line starts with "Read ", to the next blank line.
readpara() {
	awk 'on && /^[[:space:]]*$/ { exit } !on && prev_blank && /^Read / { on = 1 } on { print } { prev_blank = ($0 ~ /^[[:space:]]*$/) } BEGIN { prev_blank = 1 }' "$1"
}

# readsub <file> — a startup read's OWN "read ... first" section (any heading
# level, to the next heading), for the one level of closure.
readsub() {
	awk 'on && /^#/ { exit } /^#+ / && tolower($0) ~ /read .*first/ { on = 1; next } on { print }' "$1"
}

# pathtokens — the path-shaped names in text on stdin: backticked spans, link
# targets and bare *.md words, an anchor stripped, URLs and placeholders out.
# A link target ends at its first space, so a titled link (`[x](f.md "t")`)
# yields f.md and is not dropped whole (Codex code pass).
pathtokens() {
	{ grep -oE '`[^`]+`|\]\([^) ]+|[A-Za-z0-9_][A-Za-z0-9_./-]*\.md' || true; } |
		sed -e 's/^`//' -e 's/`$//' -e 's/^](//' -e 's/#.*$//' |
		grep -E '^[A-Za-z0-9_.][A-Za-z0-9_./-]*\.[A-Za-z0-9]+$' | grep -v '://' | sort -u || true
}

# resolve <root> <from-file> <token> — the token as a repo path: beside the
# file that names it, else from the root. Prints nothing when neither exists.
resolve() {
	_d=$(dirname -- "$2")
	if [ "$_d" != . ] && [ -f "$1/$_d/$3" ]; then printf '%s/%s\n' "$_d" "$3"
	elif [ -f "$1/$3" ]; then printf '%s\n' "$3"
	fi
}

# members <root> <out-dir> — enumerate every class into <out-dir>/<class>, one
# key per line, and the startup reads that name no file into <out-dir>/dangling.
# Directive lines outside a parsed section go to <out-dir>/directive.
members() {
	R=$1; O=$2
	: > "$O/every-request"; : > "$O/per-session"; : > "$O/read-first"; : > "$O/codex-pass"
	: > "$O/dangling"; : > "$O/directive"; : > "$O/unparsed"
	files "$R" > "$O/files"

	grep -E '(^|/)CLAUDE(\.local)?\.md$|^\.claude/(rules|agents|commands)/.*\.md$' "$O/files" >> "$O/every-request" || true
	# `@path` includes, resolved beside the including file; one that names no
	# file is prose (an `@name`), not an include. Claude Code follows an import
	# inside an import, up to five hops, so five passes over the growing list.
	for _hop in 1 2 3 4 5; do
		for f in $(cat "$O/every-request"); do
			{ grep -oE '(^|[[:space:]])@[A-Za-z0-9_.][A-Za-z0-9_./-]*' "$R/$f" || true; } | sed 's/^[[:space:]]*@//' |
				while IFS= read -r t; do resolve "$R" "$f" "$t"; done
		done > "$O/inc"
		cat "$O/every-request" "$O/inc" | sort -u > "$O/er"
		mv "$O/er" "$O/every-request"
	done
	for s in $(grep -E '^(\.claude/)?skills/[^/]+/SKILL\.md$' "$O/files" || true); do
		printf '%s:description\n' "$s" >> "$O/every-request"
		printf '%s\n' "$s" >> "$O/per-session"
	done

	# read-first: the two authority files' declared startup lists.
	: > "$O/rf0"
	if [ -f "$R/CLAUDE.md" ]; then
		section "$R/CLAUDE.md" "Read this first" > "$O/sec.CLAUDE.md"
		[ -s "$O/sec.CLAUDE.md" ] || echo "CLAUDE.md#Read this first" >> "$O/unparsed"
		pathtokens < "$O/sec.CLAUDE.md" | while IFS= read -r t; do printf 'CLAUDE.md\t%s\n' "$t"; done >> "$O/rf0"
	fi
	if [ -f "$R/AGENTS.md" ]; then
		readpara "$R/AGENTS.md" > "$O/sec.AGENTS.md"
		[ -s "$O/sec.AGENTS.md" ] || echo "AGENTS.md#Read ... before" >> "$O/unparsed"
		pathtokens < "$O/sec.AGENTS.md" | while IFS= read -r t; do printf 'AGENTS.md\t%s\n' "$t"; done >> "$O/rf0"
	fi
	: > "$O/rf1"
	while IFS="	" read -r from t; do
		p=$(resolve "$R" "$from" "$t")
		if [ -z "$p" ]; then printf '%s->%s\n' "$from" "$t" >> "$O/dangling"; continue; fi
		printf '%s\n' "$p" >> "$O/rf1"
	done < "$O/rf0"
	sort -u "$O/rf1" -o "$O/rf1"
	cp "$O/rf1" "$O/read-first"
	while IFS= read -r p; do
		readsub "$R/$p" | pathtokens | while IFS= read -r t; do
			q=$(resolve "$R" "$p" "$t")
			if [ -n "$q" ]; then printf '%s\n' "$q"; else printf '%s->%s\n' "$p" "$t" >> "$O/dangling"; fi
		done >> "$O/read-first"
	done < "$O/rf1"

	# A startup directive naming a file, outside the section that is parsed.
	for a in CLAUDE.md AGENTS.md; do
		[ -f "$R/$a" ] || continue
		awk -v sec="$O/sec.$a" '
			BEGIN { while ((getline l < sec) > 0) inside[l] = 1 }
			{ low = tolower($0) }
			low ~ /read .*first|before (working|anything|changing|exploring|editing|starting)/ && !($0 in inside) { print FILENAME ":" FNR "\t" $0 }' "$R/$a" |
			while IFS="	" read -r loc line; do
				if [ -n "$(printf '%s\n' "$line" | pathtokens)" ]; then printf '%s\n' "${loc#"$R"/}" >> "$O/directive"; fi
			done
	done

	[ -f "$R/AGENTS.md" ] && echo AGENTS.md >> "$O/codex-pass"
	if [ -f "$R/scripts/codex-review.sh" ]; then
		c=$(sed -n 's#^CHARTER=.*/\(docs/[A-Za-z0-9_./-]*\.md\)".*$#\1#p' "$R/scripts/codex-review.sh" | head -1)
		if [ -z "$c" ]; then echo "scripts/codex-review.sh#CHARTER=" >> "$O/unparsed"
		elif [ -f "$R/$c" ]; then echo "$c" >> "$O/codex-pass"
		else echo "scripts/codex-review.sh->$c" >> "$O/dangling"
		fi
	fi
	for c in $CLASSES; do sort -u "$O/$c" -o "$O/$c"; done
}

# keysize <root> <out-dir> <key> — the key's bytes, or nothing when it is gone.
keysize() {
	case " $CLASSES " in
	*" $3 "*)
		t=0
		while IFS= read -r m; do
			b=$(keysize "$1" "$2" "$m"); t=$((t + ${b:-0}))
		done < "$2/$3"
		echo "$t"; return ;;
	esac
	case $3 in
	*"#"*)
		f=${3%%#*}; [ -f "$1/$f" ] || return 0
		section "$1/$f" "${3#*#}" | awk '{ n += length($0) + 1; any = 1 } END { if (any) print n }' ;;
	*:description)
		f=${3%:description}; [ -f "$1/$f" ] || return 0
		awk 'NR == 1 { if ($0 != "---") exit; fm = 1; next }
			fm && $0 == "---" { exit }
			on && (/^[ \t]/ || /^$/) { n += length($0) + 1; next }
			{ on = 0 }
			/^description:/ { on = 1; any = 1; n += length($0) + 1 }
			END { if (any) print n }' "$1/$f" ;;
	*)
		[ -f "$1/$3" ] || return 0
		wc -c < "$1/$3" | tr -d ' ' ;;
	esac
}

# sections <file> — its five largest `## ` sections, bytes first.
sections() {
	awk '/^## / { h = $0 } { n[h] += length($0) + 1 } END { for (k in n) printf "%8d  %s\n", n[k], (k == "" ? "(before the first ## )" : k) }' "$1" |
		sort -rn | head -5
}

# judge <root> <table> — one `FAIL <kind> <key>` line per failure (details
# indented under it) and one `OK` line per key, on stdout. Exit 1 on a FAIL.
judge() {
	R=$1; T=$2
	W=$(mktemp -d -t prose-budget.XXXXXX)
	members "$R" "$W"
	bad=0
	[ -f "$T" ] || { echo "FAIL missing-table $T"; rm -rf "$W"; return 1; }
	# The table: key<TAB>ceiling<TAB>set<TAB>why; `#` comments and blank lines.
	awk -F '\t' '/^#/ || /^[[:space:]]*$/ { next }
		NF != 4 || $2 !~ /^[1-9][0-9]*$/ || $1 == "" { print "BAD\t" NR; next }
		seen[$1]++ { print "DUP\t" $1; next }
		{ print "KEY\t" $1 "\t" $2 }' "$T" > "$W/table"
	while IFS="	" read -r kind a b; do
		case $kind in
		BAD) echo "FAIL malformed $T:$a"; echo "  want 4 tab-separated fields: key, ceiling_bytes (a positive integer), set, why"; bad=1 ;;
		DUP) echo "FAIL duplicate $a"; echo "  one line per key"; bad=1 ;;
		esac
	done < "$W/table"
	sed -n 's/^KEY	//p' "$W/table" | cut -f1 | sort -u > "$W/keys"

	# Every enumerated key and every class total has a line.
	for c in $CLASSES; do
		grep -qxF -- "$c" "$W/keys" || { echo "FAIL unbudgeted $c"; echo "  a class total line is MANDATORY: a new file spends its class's headroom"; bad=1; }
		while IFS= read -r m; do
			grep -qxF -- "$m" "$W/keys" || { echo "FAIL unbudgeted $m"; echo "  loaded unasked ($c) with no line in $T"; bad=1; }
		done < "$W/$c"
	done
	while IFS= read -r d; do echo "FAIL dangling $d"; echo "  a startup list names a file that is not in the tree"; bad=1; done < "$W/dangling"
	while IFS= read -r d; do echo "FAIL unparsed $d"; echo "  the text this check parses is gone or respelled: re-point the check in the same commit"; bad=1; done < "$W/unparsed"
	while IFS= read -r d; do echo "FAIL directive $d"; echo "  startup reads belong under CLAUDE.md's \"Read this first\" or AGENTS.md's \"Read ... before\" paragraph"; bad=1; done < "$W/directive"

	# Every line: measured against its ceiling.
	while IFS="	" read -r kind key ceil; do
		[ "$kind" = KEY ] || continue
		n=$(keysize "$R" "$W" "$key")
		if [ -z "$n" ]; then echo "FAIL vanished $key"; echo "  a budgeted text is gone: delete its line in the same commit as the text, never the line alone"; bad=1; continue; fi
		pct=$((n * 100 / ceil))
		if [ "$n" -gt "$ceil" ]; then
			echo "FAIL over $key"
			echo "  $n bytes, ceiling $ceil, over by $((n - ceil))"
			case " $CLASSES " in
			*" $key "*) while IFS= read -r m; do printf '  %8s  %s\n' "$(keysize "$R" "$W" "$m")" "$m"; done < "$W/$key" ;;
			*) f=${key%%#*}; f=${f%:description}; sections "$R/$f" | sed 's/^/  /' ;;
			esac
			echo "  $RAISE"
			bad=1
		else
			echo "OK $key $n/$ceil $pct%"
		fi
	done < "$W/table"
	rm -rf "$W"
	return "$bad"
}

# formula <bytes> — landed size + 10%, rounded up to the next 4,096.
# One ceiling division over the exact product: flooring the 110% first gave
# 4,096 for 3,724 bytes, whose 110% is 4,096.4 (Codex code pass).
formula() { echo $(( ($1 * 11 + 40959) / 40960 * 4096 )); }

if [ "${1:-}" = --sizes ]; then
	W=$(mktemp -d -t prose-budget.XXXXXX)
	members . "$W"
	{ echo $CLASSES | tr ' ' '\n'; cat "$W/every-request" "$W/per-session" "$W/read-first" "$W/codex-pass";
	  [ -f "$TABLE" ] && sed -n 's/^\([^#	][^	]*\)	.*/\1/p' "$TABLE"; } | awk '!seen[$0]++' |
		while IFS= read -r k; do
			n=$(keysize . "$W" "$k"); [ -n "$n" ] && printf '%s\t%s\t%s\n' "$k" "$n" "$(formula "$n")" || printf '%s\tGONE\n' "$k"
		done
	rm -rf "$W"
	exit 0
fi
# --members: the enumeration, for scripts/cost-report.sh. The report prints
# each class beside the requests that carried it, and a second list of what a
# class contains would drift from this one (a file the check budgets and the
# report never counts), so it reads it from here. A member of two classes
# (the charter: read-first and codex-pass) is printed under each; a table key
# that is no member (a section, a key whose text is gone) is printed once
# under `-`, its bytes empty when gone.
if [ "${1:-}" = --members ] && [ $# -eq 1 ]; then
	W=$(mktemp -d -t prose-budget.XXXXXX)
	members . "$W"
	for c in $CLASSES; do
		while IFS= read -r m; do printf '%s\t%s\t%s\n' "$c" "$m" "$(keysize . "$W" "$m")"; done < "$W/$c"
	done
	cat "$W/every-request" "$W/per-session" "$W/read-first" "$W/codex-pass" > "$W/all"
	if [ -f "$TABLE" ]; then
		sed -n 's/^\([^#	][^	]*\)	.*/\1/p' "$TABLE" | while IFS= read -r k; do
			case " $CLASSES " in *" $k "*) continue ;; esac
			grep -qxF -- "$k" "$W/all" || printf -- '-\t%s\t%s\n' "$k" "$(keysize . "$W" "$k")"
		done
	fi
	rm -rf "$W"
	exit 0
fi
[ $# -eq 0 ] || fail "usage: sh scripts/check-prose-budget.sh [--sizes|--members]"

# Clause 2 first: the planted tree. See the header for what each file arms.
FX=$(mktemp -d -t prose-budget-fixture.XXXXXX)
trap 'rm -rf "$FX"' EXIT INT TERM
mkrepo "$FX"
# The operator's global excludes (they may ignore .claude/) must not decide
# which planted files git sees.
git -C "$FX" config core.excludesFile /dev/null
mkdir -p "$FX/docs" "$FX/skills/s" "$FX/.claude/rules" "$FX/scripts"
cat > "$FX/CLAUDE.md" <<'MD'
# Rules

Read it before exploring; the docs below.

## Read this first

- `docs/a.md`, [the record](docs/b.md#top) and [rules](docs/f.md "Startup policy").

## Commands

Run the qualifier; read the release notes first.
Read `docs/sneak.md` first, before anything.
@docs/i1.md

## Read this first

- `docs/e.md`, a second section of the same name
MD
printf 'Read [CLAUDE.md](CLAUDE.md) and `docs/b.md` before changing this repository.\n\nOther text.\n' > "$FX/AGENTS.md"
printf '# A\n\n## Read these first\n\n- `c.md`\n' > "$FX/docs/a.md"
printf '# B\n' > "$FX/docs/b.md"
printf '# C\n\n## Read first\n\n- `docs/d.md`\n' > "$FX/docs/c.md"
printf '# D\n' > "$FX/docs/d.md"
printf '# E\n' > "$FX/docs/e.md"
printf '# F\n' > "$FX/docs/f.md"
printf 'imported, and imports\n@i2.md\n' > "$FX/docs/i1.md"
printf 'imported at the second hop\n' > "$FX/docs/i2.md"
printf '# sneak\n' > "$FX/docs/sneak.md"
printf '# charter\n' > "$FX/docs/review-charter.md"
printf 'CHARTER="$(pwd)/docs/review-charter.md"\n' > "$FX/scripts/codex-review.sh"
printf -- '---\nname: s\ndescription: |\n  a planted skill\n\n  past the blank line, a continuation long enough to cross its ceiling\n---\n\nBody of the planted skill, longer than its class allows.\n' > "$FX/skills/s/SKILL.md"
printf 'a new always-loaded rules file nobody budgeted\n' > "$FX/.claude/rules/new.md"
{
	printf '# a planted table\n'
	printf 'every-request\t100000\tfixture\tclass\n'
	printf 'per-session\t10\tfixture\tclass over, its one member under\n'
	printf 'read-first\t100000\tfixture\tclass\n'
	printf 'codex-pass\t100000\tfixture\tclass\n'
	printf 'CLAUDE.md\t20\tfixture\tover\n'
	printf 'CLAUDE.md#Read this first\t1000\tfixture\ta section, under\n'
	printf 'AGENTS.md\t1000\tfixture\tunder\n'
	printf 'docs/a.md\t1000\tfixture\tunder\n'
	printf 'docs/i1.md\t1000\tfixture\tunder\n'
	printf 'docs/b.md\t1000\tfixture\tunder\n'
	printf 'docs/review-charter.md\t1000\tfixture\tunder\n'
	printf 'skills/s/SKILL.md\t1000\tfixture\tunder\n'
	printf 'skills/s/SKILL.md:description\t60\tfixture\tover only past its blank line\n'
	printf 'docs/gone.md\t1000\tfixture\tvanished\n'
} > "$FX/table.tsv"
got=$(judge "$FX" "$FX/table.tsv" | sed -n 's/^FAIL //p' | sort | tr '\n' ';' || true)
want='directive CLAUDE.md:12;over CLAUDE.md;over per-session;over skills/s/SKILL.md:description;unbudgeted .claude/rules/new.md;unbudgeted docs/c.md;unbudgeted docs/e.md;unbudgeted docs/f.md;unbudgeted docs/i2.md;vanished docs/gone.md;'
[ "$got" = "$want" ] || fail "the judge reported '$got' on its planted tree, want '$want' — the gate is broken, not the tree"
[ "$(formula 3723)" = 4096 ] && [ "$(formula 3724)" = 8192 ] || fail "the formula gave $(formula 3723) and $(formula 3724) for 3,723 and 3,724 bytes, want 4096 and 8192"

if out=$(judge . "$TABLE"); then
	echo "$out" | sed 's/^OK /  /'
	echo "check-prose-budget: GREEN (planted tree: 10 failures found, 0 false; $(echo "$out" | grep -c '^OK ') keys under their ceilings)"
else
	echo "$out" | grep -v '^OK ' >&2
	echo "check-prose-budget: FAIL — $(echo "$out" | grep -c '^FAIL ') line(s) above; nothing here is a warning" >&2
	exit 1
fi
