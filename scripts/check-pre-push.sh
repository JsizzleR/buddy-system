#!/bin/sh
# check-pre-push.sh — done-check for the pre-push hermetic gate.
#
# HERMETIC and fast: it never runs the real suite. Every leg stubs the hook's
# check command through BUDDY_PREPUSH_CHECK, which is also the reason that knob
# exists — without it, this check would invoke check.sh, which invokes this
# check, forever.
set -eu
cd "$(dirname "$0")/.."
ROOT=$(pwd)
HOOK="$ROOT/.githooks/pre-push"

fail() { echo "check-pre-push: FAIL — $*" >&2; exit 1; }

[ -f "$HOOK" ] || fail "no .githooks/pre-push"
grep -q 'no-verify' "$HOOK" || fail "QA-0: the hook must document the --no-verify escape hatch"

WORK=$(mktemp -d) || fail "mktemp failed"
trap 'rm -rf "$WORK"' EXIT INT TERM

REPO="$WORK/repo"
git init -q "$REPO"
git -C "$REPO" config user.email t@t
git -C "$REPO" config user.name t
git -C "$REPO" config commit.gpgsign false
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

echo "check-pre-push: GREEN (QA-1 green allows, QA-2 red blocks + bypass named,"
echo "  QA-3 deletion-safe, QA-4 kill switch, QA-5 no-repo no-op)"
