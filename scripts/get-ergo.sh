#!/bin/sh
# get-ergo.sh — fetch and build the ergo IRC server at the pinned release into
# .cache/ergo-server, so `buddy uninstall --chat` can prove a launchd agent
# running it is buddy's (D-176, amends D-173).
#
#   sh scripts/get-ergo.sh
#
# WHY A PINNED BUILD. uninstall boots out a chat server's agent only when the
# program's own bytes say it is the build a script here makes: Go's build-info
# lines `path\tgithub.com/ergochat/ergo` and `build\tvcs.revision=<PIN>`, read
# from the file, never by running it (uninstall's header). Before this script
# nothing here built ergo (README said only `ergo run`), and the developing
# machine's ~/.buddylist/ergo-bin, built as `command-line-arguments`, carries
# no module path at all, so D-173 could only leave it, named. A Label plus a
# program path under ~/.buddylist/ was the narrower proof on offer and is not
# sound: anyone can put a file there.
#
# THE PIN IS A COMMIT, THE TAG ITS NAME. A tag can move upstream; a commit id
# cannot. The script fetches TAG, refuses unless TAG names exactly PIN, checks
# PIN out detached, refuses a working tree that is not clean (the build would
# then stamp vcs.modified=true over source that is not the pin's), and builds
# with -buildvcs=true, so a build that cannot stamp its revision fails instead
# of quietly omitting it. ergo vendors its dependencies, so after the clone the
# build fetches nothing. v2.19.1 is the release the developing machine's
# hand-built binary reports (its bytes, 2026-11-12), so a rebuild changes
# nothing a user sees. uninstall.sh reads PIN from this file's `PIN=` line:
# keep that spelling, one 40-hex line.
#
# WHAT IT REFUSES, changing nothing it did not create: an argument; a
# .cache/ergo that is a symlink, not a git work tree, or a clone of another
# origin; a tag that names another commit; a dirty tree; a binary whose build
# info misses either line (checked twice: `go version -m`, and the same byte
# predicate uninstall applies, so what this builds is what uninstall proves).
# The operator's go settings are off for the build (GOENV=off GOFLAGS=
# GOWORK=off, below). The binary is built BESIDE the target and renamed over it: never written or
# copied over a running Mach-O, whose ad-hoc signature that would break (the
# kernel then SIGKILLs the next exec; CLAUDE.md's gotcha, D-107).
#
# NOT HERE: launchd. This builds a file; pointing an agent at it is the
# operator's (docs/USAGE.md, "Uninstall", says how to make an existing agent
# provable). No network in the done-check: check-get-ergo.sh serves a local
# fixture repo through git's url.insteadOf under GIT_ALLOW_PROTOCOL=file.
set -eu
TAG=v2.19.1
PIN=63c743a70644f0f19109508ade8580bf37f7d23d
URL=https://github.com/ergochat/ergo
MOD=github.com/ergochat/ergo

usage="usage: sh scripts/get-ergo.sh   (clone $URL into .cache/ergo, build $TAG ($PIN) into .cache/ergo-server)"
case $# in
0) ;;
*)
	case $1 in -h | --help | help) echo "$usage"; exit 0 ;; esac
	printf '%s\nget-ergo: refused: unknown argument %s\n' "$usage" "$1" >&2
	exit 2
	;;
esac
die() { echo "get-ergo: refused: $*" >&2; exit 1; }

CDPATH= cd -- "$(dirname -- "$0")/.."
# A substitution drops trailing newlines, so a checkout at "/x/co<NL>" read as
# /x/co, a sibling, and built there (Codex, D-176's code pass): the bytes are
# kept, and a path holding a newline is refused.
NL='
'
ROOT=$(pwd -P && echo .)
ROOT=${ROOT%.}
ROOT=${ROOT%"$NL"}
case $ROOT in *"$NL"*) die "this checkout's path holds a newline; move it" ;; esac
# An inherited GIT_DIR (a hook's, a linked worktree's) would make every git -C
# below act on another repository (D-057).
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_COMMON_DIR GIT_OBJECT_DIRECTORY
CACHE=$ROOT/.cache
DIR=$CACHE/ergo
OUT=$CACHE/ergo-server
TAB=$(printf '\t')

mkdir -p "$CACHE"
# mv onto a directory moves INTO it and says nothing (Codex): the binary would
# land at ergo-server/ergo-server.new-<pid> and the run would say built.
[ ! -d "$OUT" ] || die "$OUT is a directory; move it aside"
[ ! -L "$DIR" ] || die "$DIR is a symlink; move it aside"
if [ -e "$DIR" ]; then
	[ -d "$DIR/.git" ] && [ "$(git -C "$DIR" rev-parse --show-toplevel 2>/dev/null)" = "$(CDPATH= cd -- "$DIR" && pwd -P)" ] ||
		die "$DIR exists and is not a git clone of its own; move it aside"
	o=$(git -C "$DIR" config --get remote.origin.url 2>/dev/null || true)
	[ "$o" = "$URL" ] || die "$DIR is a clone of ${o:-no origin}, not $URL; move it aside"
else
	git clone -q --no-checkout "$URL" "$DIR"
fi
# Offline, a clone that already holds the tag still builds: the commit check
# below is what binds the build, not the fetch.
git -C "$DIR" fetch -q origin "refs/tags/$TAG:refs/tags/$TAG" 2>/dev/null ||
	echo "get-ergo: note: could not fetch $TAG from origin; using the clone's own copy" >&2
got=$(git -C "$DIR" rev-parse -q --verify "refs/tags/$TAG^{commit}" 2>/dev/null || true)
[ "$got" = "$PIN" ] || die "the tag $TAG names ${got:-nothing here}, not the pinned commit $PIN"
git -C "$DIR" -c advice.detachedHead=false checkout -q --detach "$PIN"
[ "$(git -C "$DIR" rev-parse HEAD)" = "$PIN" ] || die "$DIR did not check out $PIN"
# --ignored too: a file .git/info/exclude or a global excludes file hides is
# compiled all the same, and Go's own vcs.modified does not see it either
# (Codex: an excluded local.go built under a clean stamp). A fresh clone holds
# none, and the build writes nothing inside the tree.
dirty=$(git -C "$DIR" status --porcelain --ignored --untracked-files=all)
[ -z "$dirty" ] || die "$DIR is not clean at $PIN, so its build would not be the pin's:
$dirty"

NEW=$OUT.new-$$
trap 'rm -f "$NEW"' EXIT INT TERM
# The operator's go settings cannot reach the build: GOFLAGS (env, or `go env
# -w`'s file, which an EMPTY GOFLAGS falls back to, hence GOENV=off) could
# carry -overlay or -modfile, and a go.work above the clone could replace a
# module, each building other source under a stamp that still reads clean at
# PIN (the self-review).
CGO_ENABLED=0 GOENV=off GOFLAGS= GOWORK=off go -C "$DIR" build -buildvcs=true -o "$NEW" .
info=$(go version -m "$NEW")
printf '%s\n' "$info" | grep -qxF "${TAB}path${TAB}$MOD" || die "the build's info names no path $MOD:
$info"
printf '%s\n' "$info" | grep -qxF "${TAB}build${TAB}vcs.revision=$PIN" || die "the build's info names no vcs.revision=$PIN:
$info"
printf '%s\n' "$info" | grep -qxF "${TAB}build${TAB}vcs.modified=false" || die "the build's info does not say vcs.modified=false:
$info"
# uninstall.sh's predicate, on the bytes (its srv_ours).
LC_ALL=C grep -a -q "^.*path${TAB}github\\.com/ergochat/ergo\$" "$NEW" &&
	LC_ALL=C grep -a -q "^.*build${TAB}vcs\\.revision=$PIN\$" "$NEW" ||
	die "the build's bytes do not carry the lines uninstall proves it by"
mv -f -- "$NEW" "$OUT"
trap - EXIT INT TERM
echo "built: $OUT @ $TAG ($PIN)"
