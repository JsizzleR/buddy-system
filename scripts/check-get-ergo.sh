#!/bin/sh
# check-get-ergo.sh — done-check for scripts/get-ergo.sh (D-176).
#
# HERMETIC, and it reaches no network: get-ergo.sh clones
# https://github.com/ergochat/ergo, so every git here runs under a throwaway
# global config whose url.insteadOf maps exactly that URL to a local fixture
# repo, with GIT_ALLOW_PROTOCOL=file (git refuses any other transport before
# it connects; G-0b proves the guard is armed) and GOPROXY=off,
# GOTOOLCHAIN=local (the fixture module has no dependencies, so go needs
# nothing either). The fixture is a module named github.com/ergochat/ergo with
# a main package at its root, as ergo's is, tagged with the shipped script's
# TAG; the script runs as a COPY in a scratch checkout with only its PIN line
# rewritten to the fixture's commit, so the TAG, URL and MOD lines under test
# are the shipped ones.
#
# Each refusal has its control: G-1 builds, from the same fixture, a binary
# uninstall's predicate proves; every refusal after it is a run that reached
# the check it names and left no binary (or the one before it unchanged).
set -eu
. "$(dirname -- "$0")/lib.sh"
ROOT=$(repo_root "$0")
CDPATH= cd -- "$ROOT"
fail() { echo "check-get-ergo: FAIL — $*" >&2; exit 1; }
WORK=$(mktemp -d) || fail "mktemp failed"
trap 'rm -rf "$WORK"' EXIT INT TERM
WORK=$(CDPATH= cd -- "$WORK" && pwd -P)
TAB=$(printf '\t')
# Read outside any module and under GOTOOLCHAIN=local: in this checkout, go.mod's
# toolchain line could make `go env` fetch a toolchain.
gocache=$(CDPATH= cd -- "$WORK" && GOTOOLCHAIN=local go env GOCACHE)

# G-0: the shipped lines, read as uninstall.sh reads PIN.
S=scripts/get-ergo.sh
sh -n "$S" || fail "G-0: $S does not parse"
TAG=$(sed -n 's/^TAG=\(v[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\)$/\1/p' "$S")
PIN=$(sed -n 's/^PIN=\([0-9a-f]\{40\}\)$/\1/p' "$S")
[ "$(printf '%s\n' "$TAG" | wc -l)" -eq 1 ] && [ -n "$TAG" ] || fail "G-0: want one TAG=vX.Y.Z line in $S, found '$TAG'"
[ "$(printf '%s\n' "$PIN" | wc -l)" -eq 1 ] && [ -n "$PIN" ] || fail "G-0: want one PIN=<40 hex> line in $S, found '$PIN'"
[ "$(grep -c '^PIN=' "$S")" -eq 1 ] || fail "G-0: more than one PIN= line in $S (uninstall reads the first)"
grep -qx 'URL=https://github.com/ergochat/ergo' "$S" || fail "G-0: $S does not clone https://github.com/ergochat/ergo"
grep -qx 'MOD=github.com/ergochat/ergo' "$S" || fail "G-0: $S does not check the module path github.com/ergochat/ergo"
# uninstall.sh proves ergo by this script's pin and that module path.
grep -qF 'pin_of get-ergo.sh' scripts/uninstall.sh || fail "G-0: uninstall.sh does not read its ergo pin from get-ergo.sh"
grep -qF "'github\\.com/ergochat/ergo'" scripts/uninstall.sh || fail "G-0: uninstall.sh does not prove ergo by the path github.com/ergochat/ergo"

# The environment every git and go call below runs in.
GIT_CONFIG_GLOBAL=$WORK/gitconfig GIT_CONFIG_NOSYSTEM=1 GIT_ALLOW_PROTOCOL=file
GOPROXY=off GOTOOLCHAIN=local GOFLAGS= GOCACHE=$gocache
export GIT_CONFIG_GLOBAL GIT_CONFIG_NOSYSTEM GIT_ALLOW_PROTOCOL GOPROXY GOTOOLCHAIN GOFLAGS GOCACHE
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_COMMON_DIR GIT_OBJECT_DIRECTORY
: > "$GIT_CONFIG_GLOBAL"
# serve <repo>: the shipped URL now reaches that local repo, and only it.
serve() {
	: > "$GIT_CONFIG_GLOBAL"
	git config --global "url.file://$1.insteadOf" https://github.com/ergochat/ergo
}

# G-0b: the network guard is armed: an https URL nothing maps is refused by
# git itself, before any connection.
if out=$(git ls-remote https://example.invalid/x 2>&1); then fail "G-0b: git reached an https URL under GIT_ALLOW_PROTOCOL=file"; fi
echo "$out" | grep -q "not allowed" || fail "G-0b: the refusal is not git's protocol guard:
$out"

# upstream <dir> <module>: a fixture repo, one commit, tagged $TAG.
upstream() {
	mkrepo "$1"
	printf 'module %s\n\ngo 1.21\n' "$2" > "$1/go.mod"
	printf 'package main\n\nfunc main() {}\n' > "$1/ergo.go"
	git -C "$1" add -A
	git -C "$1" commit -q -m fixture
	git -C "$1" tag -a -m "$TAG" "$TAG"
	git -C "$1" rev-parse HEAD
}
UP=$WORK/up
FPIN=$(upstream "$UP" github.com/ergochat/ergo)
# co <dir> <pin>: a scratch checkout holding the script with that PIN.
co() {
	mkdir -p "$1/scripts"
	sed "s/^PIN=$PIN\$/PIN=$2/" "$S" > "$1/scripts/get-ergo.sh"
	grep -qx "PIN=$2" "$1/scripts/get-ergo.sh" || fail "the scratch copy's PIN was not rewritten"
	[ "$(grep -c "^PIN=" "$1/scripts/get-ergo.sh")" -eq 1 ] || fail "the scratch copy holds more than one PIN line"
}
# provable <binary> <pin>: uninstall.sh's srv_ours, on the bytes.
provable() {
	LC_ALL=C grep -a -q "^.*path${TAB}github\\.com/ergochat/ergo\$" "$1" &&
		LC_ALL=C grep -a -q "^.*build${TAB}vcs\\.revision=$2\$" "$1"
}
nonew() { ! ls "$1/.cache/ergo-server.new-"* >/dev/null 2>&1 || fail "$2: a build left a .new- file: $(ls "$1/.cache")"; }

# G-1: the build, the control for every refusal below.
serve "$UP"
C=$WORK/co1; co "$C" "$FPIN"
out=$(sh "$C/scripts/get-ergo.sh" 2>&1) || fail "G-1: the build failed:
$out"
B=$C/.cache/ergo-server
[ -f "$B" ] || fail "G-1: no $B:
$out"
info=$(go version -m "$B")
for want in "${TAB}path${TAB}github.com/ergochat/ergo" "${TAB}build${TAB}vcs.revision=$FPIN" "${TAB}build${TAB}vcs.modified=false"; do
	printf '%s\n' "$info" | grep -qxF "$want" || fail "G-1: the build info lacks '$want':
$info"
done
provable "$B" "$FPIN" || fail "G-1: uninstall's predicate does not prove the build"
echo "$out" | tail -1 | grep -qxF "built: $B @ $TAG ($FPIN)" || fail "G-1: no built line:
$out"
nonew "$C" G-1
[ "$(git -C "$C/.cache/ergo" config --get remote.origin.url)" = https://github.com/ergochat/ergo ] || fail "G-1: the clone's origin is not the shipped URL"

# G-2: a re-run over the clone builds again (idempotent) and stays provable,
# with the operator's go settings kept off the build: an exported GOFLAGS
# (-trimpath stands for -overlay or -modfile: it leaves a line in the build
# info) and a go.work above the clone naming a module that is not there,
# which fails any build that reads it.
printf 'go 1.21\n\nuse ./nowhere\n' > "$C/go.work"
out=$(GOFLAGS=-trimpath sh "$C/scripts/get-ergo.sh" 2>&1) || fail "G-2: the re-run failed:
$out"
provable "$B" "$FPIN" || fail "G-2: the rebuilt binary is not provable"
go version -m "$B" | grep -q trimpath && fail "G-2: an exported GOFLAGS reached the build:
$(go version -m "$B")"
rm "$C/go.work"

# refused <label> <dir> <text>: get-ergo.sh exits 1 saying text.
refused() {
	if out=$(sh "$2/scripts/get-ergo.sh" 2>&1); then fail "$1: should have been refused:
$out"; fi
	echo "$out" | grep -qF "get-ergo: refused: $3" || fail "$1: the refusal does not say '$3':
$out"
	nonew "$2" "$1"
}

# G-3: a dirty tree is refused, the last good binary unchanged.
sum=$(cksum < "$B")
printf 'x\n' > "$C/.cache/ergo/stray.go"
refused G-3 "$C" "$C/.cache/ergo is not clean at $FPIN"
[ "$(cksum < "$B")" = "$sum" ] || fail "G-3: a refused build changed $B"
rm "$C/.cache/ergo/stray.go"
# G-3b: an IGNORED file is compiled all the same, and neither git's plain
# status nor Go's vcs.modified sees it (Codex, the code pass): refused.
printf 'package main\n\nfunc init() { panic("local") }\n' > "$C/.cache/ergo/local.go"
printf '/local.go\n' >> "$C/.cache/ergo/.git/info/exclude"
refused G-3b "$C" "$C/.cache/ergo is not clean at $FPIN"
[ "$(cksum < "$B")" = "$sum" ] || fail "G-3b: a refused build changed $B"
rm "$C/.cache/ergo/local.go"
# G-9: a directory at the binary's path: mv would move the build INTO it and
# say built (Codex): refused before the build.
mv "$B" "$WORK/b.keep"; mkdir "$B"
refused G-9 "$C" "$B is a directory; move it aside"
[ -z "$(ls -A "$B")" ] || fail "G-9: the build landed inside the directory: $(ls -A "$B")"
rmdir "$B"; mv "$WORK/b.keep" "$B"

# G-4: the tag moved upstream to another commit: refused, no binary.
UP2=$WORK/up2; cp -R "$UP" "$UP2"
printf '// moved\n' >> "$UP2/ergo.go"; git -C "$UP2" commit -q -am moved
git -C "$UP2" tag -f -a -m "$TAG" "$TAG" >/dev/null
moved=$(git -C "$UP2" rev-parse HEAD)
serve "$UP2"; C=$WORK/co4; co "$C" "$FPIN"
refused G-4 "$C" "the tag $TAG names $moved, not the pinned commit $FPIN"
[ ! -e "$C/.cache/ergo-server" ] || fail "G-4: a refused build left a binary"

# G-5: a .cache/ergo that is another origin's clone, a plain directory, or a
# symlink is never touched.
serve "$UP"
C=$WORK/co5; co "$C" "$FPIN"; mkdir -p "$C/.cache"; git clone -q "$UP" "$C/.cache/ergo"
refused G-5 "$C" "$C/.cache/ergo is a clone of $UP, not https://github.com/ergochat/ergo"
[ "$(git -C "$C/.cache/ergo" rev-parse HEAD)" = "$FPIN" ] && [ ! -e "$C/.cache/ergo-server" ] || fail "G-5: the foreign clone was acted on"
C=$WORK/co5b; co "$C" "$FPIN"; mkdir -p "$C/.cache/ergo"; printf 'mine\n' > "$C/.cache/ergo/f"
refused G-5b "$C" "$C/.cache/ergo exists and is not a git clone of its own"
C=$WORK/co5c; co "$C" "$FPIN"; mkdir -p "$C/.cache" "$WORK/elsewhere"; ln -s "$WORK/elsewhere" "$C/.cache/ergo"
refused G-5c "$C" "$C/.cache/ergo is a symlink"
[ -z "$(ls -A "$WORK/elsewhere")" ] || fail "G-5c: the symlink's target was written"

# G-6: the pinned commit builds a module that is not ergo's (a fork renamed):
# the build runs, its info is checked, and nothing is kept.
UP3=$WORK/up3
FORK=$(upstream "$UP3" example.com/fork)
serve "$UP3"; C=$WORK/co6; co "$C" "$FORK"
refused G-6 "$C" "the build's info names no path github.com/ergochat/ergo"
[ ! -e "$C/.cache/ergo-server" ] || fail "G-6: a build of another module was kept"

# G-8: a checkout whose path ENDS in a newline, beside a sibling without it:
# a substitution read it as the sibling and built there (Codex).
NL='
'
C=$WORK/co8$NL; co "$C" "$FPIN"; mkdir -p "$WORK/co8"
refused G-8 "$C" "this checkout's path holds a newline"
[ ! -e "$WORK/co8/.cache" ] || fail "G-8: the sibling checkout was written: $(ls -A "$WORK/co8/.cache")"

# G-7: an argument is refused (exit 2); --help answers.
C=$WORK/co1
rc=0; out=$(sh "$C/scripts/get-ergo.sh" --force 2>&1) || rc=$?
[ "$rc" -eq 2 ] && echo "$out" | grep -qF "get-ergo: refused: unknown argument --force" || fail "G-7: an argument was not refused with exit 2 (rc $rc):
$out"
out=$(sh "$C/scripts/get-ergo.sh" --help 2>&1) || fail "G-7: --help failed"
echo "$out" | grep -qF "usage: sh scripts/get-ergo.sh" || fail "G-7: --help gives no usage"

echo "check-get-ergo: ok (the shipped pin parses; a pinned build carries the path and revision uninstall proves; a dirty tree, a moved tag, a foreign clone, a plain directory, a symlink and another module refused; no network)"
