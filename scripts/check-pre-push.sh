#!/bin/sh
# check-pre-push.sh — done-check for the pre-push hermetic gate.
#
# HERMETIC and fast: it never runs the real suite. Every leg stubs the hook's
# check command through BUDDY_PREPUSH_CHECK, which is also the reason that knob
# exists — without it, this check would invoke check.sh, which invokes this
# check, forever.
set -eu
. "$(dirname -- "$0")/lib.sh"
ROOT=$(repo_root "$0")
CDPATH= cd -- "$ROOT"
HOOK="$ROOT/.githooks/pre-push"

fail() { echo "check-pre-push: FAIL — $*" >&2; exit 1; }

[ -f "$HOOK" ] || fail "no .githooks/pre-push"
grep -q 'no-verify' "$HOOK" || fail "QA-0: the hook must document the --no-verify escape hatch"

WORK=$(mktemp -d) || fail "mktemp failed"
trap 'rm -rf "$WORK"' EXIT INT TERM

REPO="$WORK/repo"
mkrepo "$REPO"
printf 'x\n' > "$REPO/f.txt"
git -C "$REPO" add -A
git -C "$REPO" commit -q -m one
SHA=$(git -C "$REPO" rev-parse HEAD)
ZERO=0000000000000000000000000000000000000000

# runhook <check-command> <stdin-line> — runs the real hook with a stubbed
# check, from inside the throwaway repo, merging stderr.
runhook() {
	( cd "$REPO" && printf '%s\n' "$2" | env BUDDY_PREPUSH_CHECK="$1" BUDDY_PREPUSH_SKIP= sh "$HOOK" 2>&1 )
}

# QA-1: a green check allows, and says so.
out=$(runhook 'exit 0' "refs/heads/main $SHA refs/heads/main $ZERO") && rc=0 || rc=$?
[ "$rc" -eq 0 ] || fail "QA-1: a green check must allow the push (rc=$rc)
$out"
echo "$out" | grep -q 'GREEN' || fail "QA-1: success must be announced, not silent
$out"

# QA-2: a red check BLOCKS, and names the bypass. This is the control that
# proves QA-1 was not passing because the hook never runs anything.
out=$(runhook 'exit 1' "refs/heads/main $SHA refs/heads/main $ZERO") && rc=0 || rc=$?
[ "$rc" -ne 0 ] || fail "QA-2: a red check must block the push (rc=$rc)
$out"
echo "$out" | grep -q 'BLOCKED' || fail "QA-2: the block must say so
$out"
echo "$out" | grep -q 'no-verify' || fail "QA-2: the block must name its bypass
$out"

# QA-3: deleting a ref pushes no commits, so a red check must not block it —
# there is nothing to have broken.
out=$(runhook 'exit 1' "(delete) $ZERO refs/heads/gone $SHA") && rc=0 || rc=$?
[ "$rc" -eq 0 ] || fail "QA-3: a branch deletion sends no commits and must not be gated (rc=$rc)
$out"

# QA-4: the kill switch, even against a red check.
out=$( cd "$REPO" && printf 'refs/heads/main %s refs/heads/main %s\n' "$SHA" "$ZERO" \
	| env BUDDY_PREPUSH_CHECK='exit 1' BUDDY_PREPUSH_SKIP=1 sh "$HOOK" 2>&1 ) && rc=0 || rc=$?
[ "$rc" -eq 0 ] || fail "QA-4: BUDDY_PREPUSH_SKIP=1 must skip the gate (rc=$rc)
$out"

# QA-5: outside a repo the hook has no opinion and must not block.
out=$( cd "$WORK" && printf 'refs/heads/main %s refs/heads/main %s\n' "$SHA" "$ZERO" \
	| env BUDDY_PREPUSH_CHECK='exit 1' sh "$HOOK" 2>&1 ) && rc=0 || rc=$?
[ "$rc" -eq 0 ] || fail "QA-5: outside a git repo the hook must exit 0 (rc=$rc)
$out"

# QA-6 (#49): a REAL push from a LINKED worktree. Git hands the hook an
# absolute GIT_DIR there, and the tier must see none of git's local
# environment, or every fixture's `git -C <tmp>` writes into the pushing
# repository (measured 2026-09-27: the real one came out bare, format 99).
# The CONTROL first: a raw hook in the same place records its own env, and
# must see an absolute GIT_DIR, or this leg proves nothing on this git. Then
# the real hook, its tier stubbed to record the env the tier gets and the
# repository it finds from its cwd.
REMOTE="$WORK/remote.git"
git init -q --bare "$REMOTE"
git -C "$REPO" remote add origin "$REMOTE"
LWT="$WORK/linked"
git -C "$REPO" worktree add -q --detach "$LWT" HEAD
RAW="$WORK/rawhooks"; mkdir -p "$RAW"
printf '#!/bin/sh\nenv > "%s"\ncat >/dev/null\nexit 0\n' "$WORK/raw-env" > "$RAW/pre-push"
chmod +x "$RAW/pre-push"
git -C "$REPO" config core.hooksPath "$RAW"
# GIT_NO_REPLACE_OBJECTS is planted on both pushes: harmless to a push, one of
# git's local names, and not GIT_DIR — so a hook reduced to `unset GIT_DIR`
# dies on the tier's assertion below (Codex, #49).
( cd "$LWT" && env GIT_NO_REPLACE_OBJECTS=1 git push -q origin HEAD:refs/heads/control ) >/dev/null 2>&1 || fail "QA-6: the control push failed"
rawdir=$(sed -n 's/^GIT_DIR=//p' "$WORK/raw-env")
case "$rawdir" in
/*) ;;
*) fail "QA-6 control: a pre-push hook in a linked worktree saw GIT_DIR='$rawdir', not an absolute path — this git no longer leaks it, re-derive the leg" ;;
esac
grep -q '^GIT_NO_REPLACE_OBJECTS=1$' "$WORK/raw-env" || fail "QA-6 control: the planted GIT_NO_REPLACE_OBJECTS never reached the hook"
REAL="$WORK/realhooks"; mkdir -p "$REAL"
printf '#!/bin/sh\nexec sh "%s" "$@"\n' "$HOOK" > "$REAL/pre-push"
chmod +x "$REAL/pre-push"
git -C "$REPO" config core.hooksPath "$REAL"
TIERENV="$WORK/tier-env"
( cd "$LWT" && env GIT_NO_REPLACE_OBJECTS=1 BUDDY_PREPUSH_CHECK="env > '$TIERENV'; git rev-parse --show-toplevel > '$WORK/tier-top'" BUDDY_PREPUSH_SKIP= \
	git push -q origin HEAD:refs/heads/fixed ) >/dev/null 2>&1 || fail "QA-6: the push through the real hook failed"
[ -s "$TIERENV" ] || fail "QA-6: the stubbed tier never ran"
# Every local name git reports, and testguard's fixed copy of them — not a
# hand list of the likely ones.
localre=$( { git rev-parse --local-env-vars; sed -n '/^var gitLocalEnv/,/^}/p' "$ROOT/internal/testguard/gitenv.go" | grep -o 'GIT_[A-Z_]*'; } | sort -u | paste -sd'|' -)
[ -n "$localre" ] || fail "QA-6: could not list git's local environment"
leak=$(grep -E "^($localre)=" "$TIERENV" || true)
[ -z "$leak" ] || fail "QA-6: the tier inherited git's local environment:
$leak"
top=$(cat "$WORK/tier-top"); want=$(cd "$LWT" && pwd -P)
[ "$(cd "$top" && pwd -P)" = "$want" ] || fail "QA-6: the tier found '$top', not the pushed worktree '$want'"

# QA-7 (#49): every done-check sources lib.sh, and sourcing it clears an
# inherited GIT_DIR, so one run on its own from inside a hook builds its
# fixtures where it thinks it does. Control: the same shell without the source
# still sees it.
# Three local names are planted, GIT_DIR and two that are not; and the leg runs
# twice, the second time with NO git on PATH, which is when only the fixed
# copy in lib.sh can clear them (a six-name copy survived the first run and
# dies on the second — Codex, #49).
show='printf "%s|%s|%s" "${GIT_DIR-unset}" "${GIT_NO_REPLACE_OBJECTS-unset}" "${GIT_CONFIG_PARAMETERS-unset}"'
plant() { env GIT_DIR="$WORK/elsewhere/.git" GIT_NO_REPLACE_OBJECTS=1 GIT_CONFIG_PARAMETERS="'core.bare'='true'" "$@"; }
ctl=$(plant /bin/sh -c "$show")
[ "$ctl" = "$WORK/elsewhere/.git|1|'core.bare'='true'" ] || fail "QA-7 control: the planted names never reached the shell ('$ctl')"
got=$(plant /bin/sh -c ". '$ROOT/scripts/lib.sh'; $show")
[ "$got" = "unset|unset|unset" ] || fail "QA-7: sourcing lib.sh (git on PATH) left '$got'"
NOGIT="$WORK/nogit"; mkdir -p "$NOGIT"
got=$(plant PATH="$NOGIT" /bin/sh -c ". '$ROOT/scripts/lib.sh'; $show")
[ "$got" = "unset|unset|unset" ] || fail "QA-7: sourcing lib.sh with NO git on PATH (the fixed copy alone) left '$got'"

echo "check-pre-push: GREEN (QA-1 green allows, QA-2 red blocks + bypass named,"
echo "  QA-3 deletion-safe, QA-4 kill switch, QA-5 no-repo no-op,"
echo "  QA-6 a linked-worktree push: the hook saw an absolute GIT_DIR, the tier none of git's local env,"
echo "  QA-7 sourcing lib.sh clears git's local env, with and without git on PATH)"
