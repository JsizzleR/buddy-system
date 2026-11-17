#!/bin/sh
# check-check.sh — done-check for check.sh's own EXIT STATUS (#118).
#
# WHY. check.sh reserves exit 2 for "not run": the live leg had no server
# binary, or the tier name was unknown, so nothing was judged (CLAUDE.md,
# "check.sh exit 2 means the live leg did not run"). Every other leg ran
# under `set -e` and passed its own status straight through, and gofmt exits
# 2 on a file it cannot parse: `fmt=$(gofmt -l cmd internal)` carried that 2
# out of check.sh before any summary line, so a broken tree read as a tier
# whose hermetic half passed (#118, reproduced here on 6fb58ac: rc=2). The
# same held for any other leg that exits 2 (a self-test whose argparse
# refuses a renamed flag; a `go vet` that cannot load a package).
#
# HOW IT JUDGES. A copy of check.sh in a fixture tree that holds only what
# the leg under test reaches: `cmd/` and `internal/` for gofmt, and a `go` on
# PATH that exits with the status the case names, so the live tier reaches its
# not-run branch without the toolchain doing anything. Exact exit status per
# case, and the summary line's absence.
#
# CONTROLS. The deliberate exit 2s still exit 2 (an unknown tier; a live leg
# with no .cache/oscar-server): a fix that folded EVERY 2 into 1 would erase
# "not run" and fail them. An unformatted but parseable file still fails as
# "gofmt needed", proving the fixture reaches the gofmt leg at all. And a leg
# other than gofmt (the live leg's `go vet`, shimmed to exit 2) must fail as
# 1 too: a fix spelled on the gofmt line alone passes the first case and
# fails this one.
#
# Every shell check.sh may be run by: /bin/sh, and dash and bash when present
# (Linux CI's /bin/sh is dash; macOS's is bash).
set -u
. "$(dirname -- "$0")/lib.sh"
ROOT=$(repo_root "$0")
CDPATH= cd -- "$ROOT"

fail() { echo "check-check: FAIL — $*" >&2; exit 1; }
command -v gofmt >/dev/null 2>&1 || fail "gofmt not found"

WORK=$(mktemp -d) || fail "mktemp failed"
trap 'rm -rf "$WORK"' EXIT INT TERM

# fixture <name>: a tree with check.sh and empty cmd/ and internal/; prints its root.
fixture() {
	f="$WORK/$1"
	mkdir -p "$f/scripts" "$f/cmd" "$f/internal" "$f/shim" || fail "mkdir $f"
	cp scripts/check.sh "$f/scripts/check.sh" || fail "cp check.sh"
	# A go that exits $GO_RC and does nothing else; the cases that reach it name it.
	printf '#!/bin/sh\nexit "${GO_RC:-0}"\n' > "$f/shim/go" && chmod +x "$f/shim/go" || fail "shim go"
	echo "$f"
}

# expect <shell> <want rc> <label> <fixture> <tier> [GO_RC]: run, compare, and
# require no GREEN line on any non-zero.
expect() {
	sh_=$1 want=$2 label=$3 f=$4 tier=$5 gorc=${6:-0}
	out=$(cd "$f" && GO_RC=$gorc PATH="$f/shim:$PATH" "$sh_" scripts/check.sh "$tier" 2>&1)
	rc=$?
	[ "$rc" = "$want" ] || fail "$sh_: $label: rc=$rc, want $want:
$out"
	case "$out" in *GREEN*) fail "$sh_: $label: printed a GREEN line on rc=$rc:
$out" ;; esac
	printf '%s\n' "$out"
}

shells=/bin/sh
for s in dash bash; do
	p=$(command -v "$s" 2>/dev/null) && [ "$p" != /bin/sh ] && shells="$shells $p"
done

PARSE=$(fixture parse)
printf 'package x\nfunc f( {\n' > "$PARSE/internal/bad.go"
UNFMT=$(fixture unfmt)
printf 'package x\nfunc  f() {}\n' > "$UNFMT/internal/ugly.go"
LIVE=$(fixture live)

for sh_ in $shells; do
	# 1. The defect: gofmt cannot parse a file -> FAIL (1), not "not run" (2).
	out=$(expect "$sh_" 1 "gofmt parse error" "$PARSE" hermetic) || exit 1
	echo "$out" | grep -q "expected ')'" || fail "$sh_: gofmt's own error did not reach the output: $out"
	echo "$out" | grep -q 'exit 2 is reserved' || fail "$sh_: the parse-error FAIL does not say why it is not exit 2: $out"
	# 2. Control: the fixture reaches the gofmt leg (a parseable, unformatted file).
	out=$(expect "$sh_" 1 "gofmt needed" "$UNFMT" hermetic) || exit 1
	echo "$out" | grep -q '^gofmt needed: internal/ugly.go' || fail "$sh_: the unformatted control did not reach the gofmt leg: $out"
	# 3. Control: the deliberate "not run" exits keep their 2.
	out=$(expect "$sh_" 2 "unknown tier" "$LIVE" nosuchtier) || exit 1
	echo "$out" | grep -q "unknown tier 'nosuchtier'" || fail "$sh_: unknown-tier refusal changed: $out"
	out=$(expect "$sh_" 2 "live leg not run" "$LIVE" live 0) || exit 1
	echo "$out" | grep -q 'LIVE LEG NOT RUN' || fail "$sh_: the not-run line is missing: $out"
	# 4. A leg other than gofmt that exits 2 (the live leg's go vet) is a FAIL too.
	out=$(expect "$sh_" 1 "go vet exits 2" "$LIVE" live 2) || exit 1
	case "$out" in *'LIVE LEG NOT RUN'*) fail "$sh_: go vet's 2 went on to the not-run branch: $out" ;; esac
done

echo "check-check: ok ($(echo $shells | wc -w | tr -d ' ') shell(s): a stray exit 2 fails as 1; unknown tier and live-not-run keep 2)"
