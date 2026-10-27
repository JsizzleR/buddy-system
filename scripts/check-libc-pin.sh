#!/bin/sh
# check-libc-pin.sh — done-check: the build selects the modernc.org/libc that
# the selected modernc.org/sqlite declares, exactly.
#
# Usage: sh scripts/check-libc-pin.sh [module-dir]     (default: this repo)
#
# WHY A GATE. modernc.org/sqlite is SQLite translated from C into Go, and the
# translation is generated against ONE modernc.org/libc: upstream says to use
# exactly the libc version sqlite's go.mod declares. Minimal version selection
# does not honour "exactly": it takes the highest version anything requires.
# Measured 2026-10-27 at dd7daff: sqlite v1.40.1 declared libc v1.66.10, and the
# build selected v1.67.1, because github.com/mk6i/open-oscar-server (the TOC
# transport, linked only into `buddylist`) requires it. So the chat half chose
# the C runtime under the claims ledger that the gate reads on every tool call:
# the claims binary dragged behind the chat stack, which the two-binary split
# exists to prevent. Nothing failed; it was a pair upstream never generated.
# Fixed by moving sqlite and libc together to a pair sqlite declares; this
# gate keeps the next oscar bump (or a stray `go get`) from undoing it quietly,
# because `go build` and every test would stay green over it.
#
# WHAT IT CHECKS. The selected libc (`go list -m`), taking a replacement into
# account, against the libc the selected sqlite's own go.mod requires (a
# replaced sqlite's replacement go.mod). Equal, or FAIL naming both. A
# `replace modernc.org/libc => modernc.org/libc vX` pin is honoured by its vX;
# a replacement by a directory or another module path FAILS, because nothing
# here can say which libc that is.
#
# OFFLINE ON PURPOSE. GOPROXY=off: the go command may read only the module
# cache, which the build legs that run before this one in check.sh have already
# filled. GOFLAGS is emptied so an operator's -mod=mod cannot let the check
# rewrite go.mod into agreement, and GOWORK=off so a go.work cannot select a
# libc the committed go.mod would not.
#
# TWO CLAUSES (the convention check.sh and check-fence.sh use). The verdict is
# run on every mismatch shape it must refuse and on the two it must accept, and
# the extraction runs end to end on a fixture whose answer is known (both
# modules replaced by local directories, so it needs no cache), before either is
# trusted with the real module. A parser that silently read nothing would
# otherwise report the same GREEN as a matched pair.
#
# Considered and cut: the opposite lever, `replace modernc.org/libc v1.67.1 =>
# modernc.org/libc v1.66.10`. It keeps SQLite's engine where it was, but its
# left side names one version, so the next oscar bump slips past it (this gate
# would catch that too), and v1.66.10 was not in the module cache to build
# offline. The check accepts that shape if it is ever chosen.
set -eu
. "$(dirname -- "$0")/lib.sh"
# The argument is the caller's path, so it is resolved before the cd below.
mod=
[ -z "${1:-}" ] || mod=$(CDPATH= cd -- "$1" && pwd) || { echo "check-libc-pin: FAIL — no directory '$1'" >&2; exit 1; }
CDPATH= cd -- "$(repo_root "$0")"

fail() { echo "check-libc-pin: FAIL — $*" >&2; exit 1; }

export GOPROXY=off GOFLAGS= GOWORK=off LC_ALL=C

# pair <module-dir> — one line, `selected|replace-path|replace-version|declared`:
# the libc this module's build selects, its replacement if any, and the libc
# the selected sqlite's own go.mod requires. Fails unless that go.mod names
# exactly one.
#
# The declared version is read from sqlite's go.mod FILE (`.GoMod`, which is the
# replacement's when sqlite is replaced), not from `go mod graph`. Measured on a
# module cache filled only by `go vet` and `go test` (what CI has): the graph
# walk wanted golang.org/x/sys@v0.6.0's go.mod, required by go-isatty, which
# predates graph pruning, and failed offline. Each go command runs on its own
# line, so its failure is never a pipe's status.
pair() {
	sel=$(cd -- "$1" && go list -m -f '{{.Version}}|{{with .Replace}}{{.Path}}|{{.Version}}{{else}}|{{end}}' modernc.org/libc) ||
		fail "cannot read the selected modernc.org/libc in $1 (offline: is the module cache filled?)"
	sqmod=$(cd -- "$1" && go list -m -f '{{.GoMod}}' modernc.org/sqlite) ||
		fail "cannot read the selected modernc.org/sqlite in $1"
	[ -n "$sqmod" ] || fail "the selected modernc.org/sqlite in $1 has no go.mod in the module cache"
	sqjson=$(go mod edit -json "$sqmod") || fail "cannot parse $sqmod"
	# The -json layout is go's own MarshalIndent: top-level keys at one tab.
	# Only a "Require" entry counts; a dependency's replace and exclude lines
	# are ignored by the go command, and so here.
	decl=$(printf '%s\n' "$sqjson" | awk '
		/^\t"[A-Za-z]+":/ { sec = $1 }
		sec == "\"Require\":" && $1 == "\"Path\":" { p = $2 }
		sec == "\"Require\":" && $1 == "\"Version\":" && p == "\"modernc.org/libc\"," { v = $2; gsub(/[",]/, "", v); print v; p = "" }
	')
	[ "$(printf '%s\n' "$decl" | grep -c .)" = 1 ] ||
		fail "$sqmod requires '$(printf '%s' "$decl" | tr '\n' ' ')' as its modernc.org/libc; want exactly one version"
	printf '%s|%s\n' "$sel" "$decl"
}

# verdict <selected> <replace-path> <replace-version> <declared> — silent and 0
# when the build's libc is the declared one; one line naming both and 1 when not.
verdict() {
	eff=$1
	if [ -n "$2" ]; then
		[ "$2" = modernc.org/libc ] && [ -n "$3" ] || {
			echo "modernc.org/libc $1 is replaced by $2${3:+ $3}, not by a release of modernc.org/libc; sqlite declares $4"
			return 1
		}
		eff=$3
	fi
	[ "$eff" = "$4" ] || {
		echo "the build selects modernc.org/libc $eff${2:+ (replacing $1)}; the selected modernc.org/sqlite declares $4"
		return 1
	}
}

# Clause 1: the verdict on every shape. Each row: want, then verdict's four args.
while IFS=' ' read -r want s rp rv d; do
	[ -n "$want" ] || continue
	[ "$rp" = - ] && rp=
	[ "$rv" = - ] && rv=
	if out=$(verdict "$s" "$rp" "$rv" "$d"); then got=pass; else got=fail; fi
	[ "$got" = "$want" ] || fail "verdict($s, '$rp', '$rv', $d) = $got, want $want — the gate is broken, not go.mod"
	if [ "$want" = fail ]; then
		case $out in *"$d"*) ;; *) fail "verdict's refusal '$out' does not name the declared $d" ;; esac
	fi
done <<'ROWS'
pass v1.73.4 - - v1.73.4
fail v1.67.1 - - v1.66.10
fail v1.66.9 - - v1.66.10
pass v1.67.1 modernc.org/libc v1.66.10 v1.66.10
fail v1.67.1 modernc.org/libc v1.66.9 v1.66.10
fail v1.67.1 modernc.org/libc v1.67.2 v1.66.10
fail v1.66.10 ./libc - v1.66.10
fail v1.66.10 example.com/libc v1.66.10 v1.66.10
ROWS

# Clause 2: the extraction, end to end, on a fixture whose answer is known.
fx=$(mktemp -d -t libc-pin.XXXXXX)
trap 'rm -rf "$fx"' EXIT INT TERM
mkdir "$fx/s" "$fx/l"
# The fixture sqlite also names libc in an exclude and a replace, which the go
# command ignores in a dependency, so a reader that took any libc line would
# read four versions here and fail the count.
cat > "$fx/s/go.mod" <<'MOD'
module modernc.org/sqlite

go 1.25

require modernc.org/libc v1.66.10 // indirect

exclude modernc.org/libc v1.66.9

replace modernc.org/libc v1.66.11 => modernc.org/libc v1.66.8
MOD
printf 'module modernc.org/libc\n\ngo 1.25\n' > "$fx/l/go.mod"
cat > "$fx/go.mod" <<'MOD'
module fixture

go 1.25

require (
	modernc.org/libc v1.67.1
	modernc.org/sqlite v1.40.1
)

replace modernc.org/sqlite => ./s

replace modernc.org/libc => ./l
MOD
got=$(pair "$fx")
[ "$got" = 'v1.67.1|./l||v1.66.10' ] ||
	fail "the fixture read as '$got', want 'v1.67.1|./l||v1.66.10' — the extraction is broken, not go.mod"

# The real module.
[ -n "$mod" ] || mod=.
got=$(pair "$mod")
IFS='|' read -r sel rp rv decl <<EOF
$got
EOF
why=$(verdict "$sel" "$rp" "$rv" "$decl") || {
	echo "check-libc-pin: FAIL — $why." >&2
	echo "  modernc.org/sqlite is generated against exactly the libc it declares. Another module raised it:" >&2
	echo "  see \`go mod graph | grep ' modernc.org/libc@'\`. Move sqlite to a release that declares the libc" >&2
	echo "  selected, or pin libc with \`replace modernc.org/libc <selected> => modernc.org/libc <declared>\`." >&2
	exit 1
}
echo "check-libc-pin: GREEN (8 verdict shapes and the fixture held; modernc.org/libc $decl is the one modernc.org/sqlite declares)"
