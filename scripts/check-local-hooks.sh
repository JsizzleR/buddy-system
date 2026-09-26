#!/bin/sh
# check-local-hooks.sh — done-check for the hooks.local extension point.
#
# WHAT IS CHECKED. Both tracked hooks run an executable
# <git-common-dir>/hooks.local/<hook> first, and its failure is theirs. The
# common dir, not the worktree, because a linked worktree has no copy of an
# untracked file in the main tree; QA-5 is that case.
#
# pre-push reads its ref list from stdin, which can be read only once. QA-4
# proves BOTH readers got it: the local hook recorded the line, and the tracked
# hook still announced its own verdict.
#
# HERMETIC: HOME and PATH are pinned so no installed `buddy` runs the claim gate
# here, and the tracked pre-push's check is stubbed (see check-pre-push.sh).
set -eu
. "$(dirname -- "$0")/lib.sh"
ROOT=$(repo_root "$0")
CDPATH= cd -- "$ROOT"

fail() { echo "check-local-hooks: FAIL — $*" >&2; exit 1; }

WORK=$(mktemp -d) || fail "mktemp failed"
trap 'rm -rf "$WORK"' EXIT INT TERM

REPO="$WORK/repo"
mkrepo "$REPO"
git -C "$REPO" config core.hooksPath "$ROOT/.githooks"
LOCAL="$REPO/.git/hooks.local"
ZERO=0000000000000000000000000000000000000000

# commit <dir> <file> — stage and commit one new file; prints git's output.
commit() {
	printf 'x\n' > "$1/$2"
	git -C "$1" add -- "$2"
	env HOME="$WORK" PATH=/usr/bin:/bin git -C "$1" commit -q -m "$2" 2>&1
}
head_of() { git -C "$1" rev-parse -q --verify HEAD || echo none; }

# QA-1: no local hook is the normal case — the commit goes through. This is also
# the control that the fixture can commit at all.
before=$(head_of "$REPO")
out=$(commit "$REPO" one) || fail "QA-1: with no local hook a commit must succeed
$out"
[ "$(head_of "$REPO")" != "$before" ] || fail "QA-1: no commit was made"

# QA-2: a failing local pre-commit refuses the commit, and its words reach the
# committer.
mkdir -p "$LOCAL"
printf '#!/bin/sh\necho "local says no" >&2\nexit 1\n' > "$LOCAL/pre-commit"
chmod +x "$LOCAL/pre-commit"
before=$(head_of "$REPO")
out=$(commit "$REPO" two) && fail "QA-2: a failing local pre-commit must refuse the commit
$out"
[ "$(head_of "$REPO")" = "$before" ] || fail "QA-2: HEAD moved past a refused commit"
echo "$out" | grep -q 'local says no' || fail "QA-2: the local hook's message was lost
$out"

# QA-3: the positive control for QA-2 — the same file passing lets it through,
# so QA-2 refused because of the verdict, not because the hook broke.
printf '#!/bin/sh\nexit 0\n' > "$LOCAL/pre-commit"
before=$(head_of "$REPO")
out=$(commit "$REPO" three) || fail "QA-3: a passing local pre-commit must allow the commit
$out"
[ "$(head_of "$REPO")" != "$before" ] || fail "QA-3: no commit was made"

# QA-4: pre-push hands the same stdin to the local hook AND keeps it for itself.
SHA=$(head_of "$REPO")
LINE="refs/heads/main $SHA refs/heads/main $ZERO"
printf '#!/bin/sh\ncat > "%s"\nexit 0\n' "$WORK/seen" > "$LOCAL/pre-push"
chmod +x "$LOCAL/pre-push"
pushhook() {
	( cd "$REPO" && printf '%s\n' "$LINE" | env BUDDY_PREPUSH_CHECK='exit 0' BUDDY_PREPUSH_SKIP="$1" sh "$ROOT/.githooks/pre-push" origin url 2>&1 )
}
out=$(pushhook "") || fail "QA-4: a passing local pre-push must allow the push
$out"
grep -qxF "$LINE" "$WORK/seen" || fail "QA-4: the local pre-push did not receive the ref line"
echo "$out" | grep -q 'GREEN' || fail "QA-4: the tracked hook lost its stdin after the local one read it
$out"

# QA-5: a failing local pre-push blocks, even with the hermetic tier skipped —
# the knob skips the tier, not the machine's own checks.
printf '#!/bin/sh\necho "local blocks" >&2\nexit 1\n' > "$LOCAL/pre-push"
out=$(pushhook 1) && fail "QA-5: a failing local pre-push must block the push
$out"
echo "$out" | grep -q 'local blocks' || fail "QA-5: the local hook's message was lost
$out"

# QA-6: a linked worktree shares the common dir, so the same local hook guards it.
printf '#!/bin/sh\necho "local says no" >&2\nexit 1\n' > "$LOCAL/pre-commit"
git -C "$REPO" worktree add -q "$WORK/wt" -b side 2>/dev/null
before=$(head_of "$WORK/wt")
out=$(commit "$WORK/wt" four) && fail "QA-6: the local hook must guard a linked worktree too
$out"
[ "$(head_of "$WORK/wt")" = "$before" ] || fail "QA-6: HEAD moved in the worktree"

echo "check-local-hooks: GREEN (QA-1 absent is a no-op, QA-2 local refusal blocks a commit,"
echo "  QA-3 its control, QA-4 pre-push stdin reaches both, QA-5 blocks past the skip knob,"
echo "  QA-6 a linked worktree is covered)"
