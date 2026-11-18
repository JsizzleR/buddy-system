#!/bin/sh
# check-pre-push.sh — done-check for the pre-push hermetic gate.
#
# HERMETIC and fast: it never runs the real suite. Every leg stubs the hook's
# check command through BUDDY_PREPUSH_CHECK, which is also the reason that knob
# exists — without it, this check would invoke check.sh, which invokes this
# check, forever. BUDDY_PREPUSH_MEMGATE is stubbed the same way (QA-15): the
# real memory gate is never asked from here.
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
# A throwaway HOME for everything below, so no hook run here, nor a mutant of
# the hook, can reach the machine's own $HOME/bin/mem-gate.py (measured: a
# mutant asking it for a stubbed tier booked 8 GB on the real gate). The
# fixtures set their own identity (mkrepo).
HOME="$WORK/home"
export HOME
mkdir -p "$HOME"
# And no inherited gate override, which the hook would ask even for a stubbed
# tier (Codex); QA-15 names its stubs explicitly.
unset BUDDY_PREPUSH_MEMGATE

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
( cd "$LWT" && env GIT_NO_REPLACE_OBJECTS=1 BUDDY_PREPUSH_CHECK="env > '$TIERENV'; git rev-parse --show-toplevel > '$WORK/tier-top'; git rev-parse HEAD > '$WORK/tier-head'" BUDDY_PREPUSH_SKIP= \
	git push -q origin HEAD:refs/heads/fixed ) >/dev/null 2>&1 || fail "QA-6: the push through the real hook failed"
[ -s "$TIERENV" ] || fail "QA-6: the stubbed tier never ran"
# Every local name git reports, and testguard's fixed copy of them — not a
# hand list of the likely ones.
localre=$( { git rev-parse --local-env-vars; sed -n '/^var gitLocalEnv/,/^}/p' "$ROOT/internal/testguard/gitenv.go" | grep -o 'GIT_[A-Z_]*'; } | sort -u | paste -sd'|' -)
[ -n "$localre" ] || fail "QA-6: could not list git's local environment"
leak=$(grep -E "^($localre)=" "$TIERENV" || true)
[ -z "$leak" ] || fail "QA-6: the tier inherited git's local environment:
$leak"
# Amended (#123, D-223): the tier runs on the pushed commit in the detached
# tree, so its HEAD is the pushed sha and its top is NOT the pushing worktree.
head=$(cat "$WORK/tier-head"); pushed=$(git -C "$LWT" rev-parse HEAD)
[ "$head" = "$pushed" ] || fail "QA-6: the tier's HEAD is '$head', not the pushed sha '$pushed'"
top=$(cat "$WORK/tier-top"); lwtop=$(cd "$LWT" && pwd -P)
[ "$(cd "$top" && pwd -P)" != "$lwtop" ] || fail "QA-6: the tier ran in the pushing worktree '$lwtop', not in a detached tree"

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

# THE TIER RUNS ON WHAT IS PUSHED (#123, D-223). Each leg stubs the tier to
# report what it sees. On the hook before #123, which ran the tier in the
# pushing checkout, QA-8..11 and QA-13..15 are red; QA-12 is green there by
# construction and is the leg that kills a stash-and-run-in-place mutant. A
# fresh repository, so nothing above (the linked worktree, its hooksPath) is in
# its state.
R8="$WORK/r8"
mkrepo "$R8"
STATE8="$(cd "$R8/.git" && pwd -P)/buddy-prepush"
# hook8 <check> <stdin> [VAR=value...] — the real hook from inside R8. An empty
# BUDDY_PREPUSH_MEMGATE leaves the hook's own default, which consults no gate
# for a stubbed tier.
hook8() {
	_c=$1 _in=$2
	shift 2
	( cd "$R8" && printf '%s\n' "$_in" | env BUDDY_PREPUSH_CHECK="$_c" BUDDY_PREPUSH_SKIP= BUDDY_PREPUSH_MEMGATE= "$@" sh "$HOOK" origin url 2>&1 )
}

# QA-8: the recorded reproduction. The pushed commit needs helper.go, which
# exists only untracked in the working tree. The CONTROL first: the stub
# passes there, so a block below is the tree it ran in, not a broken stub.
printf 'needs helper.go\n' > "$R8/main.txt"
git -C "$R8" add main.txt
git -C "$R8" commit -q -m 'uses helper.go (forgot to add it)'
printf 'helper\n' > "$R8/helper.go"
S8=$(git -C "$R8" rev-parse HEAD)
( cd "$R8" && test -f helper.go ) || fail "QA-8 control: the stub must pass in the working tree"
out=$(hook8 'test -f helper.go' "refs/heads/main $S8 refs/heads/main $ZERO") && rc=0 || rc=$?
[ "$rc" -ne 0 ] || fail "QA-8: a pushed commit without helper.go went GREEN, the tier saw the untracked copy (rc=$rc)
$out"
echo "$out" | grep -q 'BLOCKED' || fail "QA-8: the block must say so
$out"
rm -f "$R8/helper.go"

# QA-9: f.txt and g.txt are committed "broken"; the working tree has f.txt
# fixed and staged, g.txt fixed and unstaged, and a peer's untracked junk.txt.
# The tier must read the commit: "broken" twice, and no junk.
printf 'broken\n' > "$R8/f.txt"
printf 'broken\n' > "$R8/g.txt"
git -C "$R8" add f.txt g.txt
git -C "$R8" commit -q -m broken
S9=$(git -C "$R8" rev-parse HEAD)
printf 'fixed\n' > "$R8/f.txt"
git -C "$R8" add f.txt
printf 'fixed\n' > "$R8/g.txt"
printf 'junk\n' > "$R8/junk.txt"
out=$(hook8 "cat f.txt g.txt > '$WORK/q9'; if test -e junk.txt; then echo JUNK >> '$WORK/q9'; fi" "refs/heads/main $S9 refs/heads/main $ZERO") \
	|| fail "QA-9: the stub tier exits 0, so the push must be allowed
$out"
[ "$(cat "$WORK/q9")" = "$(printf 'broken\nbroken')" ] || fail "QA-9: the tier read the working tree, not the pushed commit:
$(cat "$WORK/q9")"

# QA-10: `git push origin other` while HEAD is elsewhere. The tier's HEAD is
# the pushed sha. commit-tree, so the dirty working tree stays as it is.
S10=$(git -C "$R8" commit-tree -p "$S9" -m other "$S9^{tree}")
git -C "$R8" update-ref refs/heads/other "$S10"
out=$(hook8 "git rev-parse HEAD > '$WORK/q10'" "refs/heads/other $S10 refs/heads/other $ZERO") || fail "QA-10: a green stub must allow
$out"
[ "$(cat "$WORK/q10")" = "$S10" ] || fail "QA-10: pushing other ($S10) tested '$(cat "$WORK/q10")', not the pushed tip"

# QA-11: a deletion, tip B on two refs, and tip C, C red. Each distinct tip
# is tested once, in stdin order, and the red one blocks.
SB=$(git -C "$R8" commit-tree -p "$S10" -m b "$S9^{tree}")
SC=$(git -C "$R8" commit-tree -p "$SB" -m c "$S9^{tree}")
git -C "$R8" update-ref refs/heads/b "$SB"
git -C "$R8" update-ref refs/heads/c "$SC"
out=$(hook8 "h=\$(git rev-parse HEAD); echo \"\$h\" >> '$WORK/q11'; test \"\$h\" != $SC" "(delete) $ZERO refs/heads/gone $S9
refs/heads/b $SB refs/heads/b $ZERO
refs/tags/tb $SB refs/tags/tb $ZERO
refs/heads/c $SC refs/heads/c $ZERO") && rc=0 || rc=$?
[ "$rc" -ne 0 ] || fail "QA-11: tip C is red, so the push must be blocked (rc=$rc)
$out"
echo "$out" | grep -q 'BLOCKED' || fail "QA-11: the block must say so
$out"
[ "$(cat "$WORK/q11")" = "$(printf '%s\n%s' "$SB" "$SC")" ] || fail "QA-11: want B then C, each once; the tier saw:
$(cat "$WORK/q11")"

# QA-12: a green and a red run leave the checkout's status, worktrees, refs
# and stash exactly as they were. The CONTROL: the snapshot holds the dirt, so
# a mutant that stashes it and runs in place shows here.
snap8() {
	git -C "$R8" status --porcelain
	echo --
	git -C "$R8" worktree list --porcelain
	echo --
	git -C "$R8" for-each-ref
	echo --
	git -C "$R8" stash list
}
before=$(snap8)
case "$before" in
*junk.txt*) ;;
*) fail "QA-12 control: the snapshot does not show the dirty working tree:
$before" ;;
esac
hook8 'exit 0' "refs/heads/main $S9 refs/heads/main $ZERO" >/dev/null || fail "QA-12: the green run must allow"
hook8 'exit 1' "refs/heads/main $S9 refs/heads/main $ZERO" >/dev/null && fail "QA-12: the red run must block"
after=$(snap8)
[ "$before" = "$after" ] || fail "QA-12: the runs changed the checkout:
--- before
$before
--- after
$after"

# QA-13: ONE stable tree per checkout (cmd/go keys a main module's compiles
# by its directory), and a previous tier's leftovers are gone before the next.
hook8 "pwd -P > '$WORK/q13a'; echo x > leftover.txt" "refs/heads/main $S9 refs/heads/main $ZERO" >/dev/null || fail "QA-13: the first run must allow"
[ "$(cat "$WORK/q13a")" = "$STATE8/tree" ] || fail "QA-13: the tier ran in '$(cat "$WORK/q13a")', not the stable '$STATE8/tree'"
out=$(hook8 "pwd -P > '$WORK/q13b'; test ! -e leftover.txt" "refs/heads/other $S10 refs/heads/other $ZERO") || fail "QA-13: a previous tier's untracked file survived into the next run
$out"
[ "$(cat "$WORK/q13b")" = "$STATE8/tree" ] || fail "QA-13: the second run moved to '$(cat "$WORK/q13b")'"

# QA-14: the tree is LOCKED. While another process holds the lock, the hook
# says it waits and the tier does not start; on release it runs.
rm -f "$WORK/q14-held" "$WORK/q14-ran"
mkdir -p "$STATE8"
python3 -I -c 'import fcntl, os, sys, time
f = open(sys.argv[1], "a")
fcntl.flock(f, fcntl.LOCK_EX)
open(sys.argv[2], "w").close()
t0 = time.time()
while not os.path.exists(sys.argv[3]) and time.time() - t0 < 30:
    time.sleep(0.05)' "$STATE8/lock" "$WORK/q14-held" "$WORK/q14-release" &
holder=$!
i=0
until [ -e "$WORK/q14-held" ]; do
	i=$((i + 1))
	[ "$i" -le 100 ] || { fail "QA-14 control: the lock holder never took the lock"; break; }
	sleep 0.1
done
hook8 "touch '$WORK/q14-ran'" "refs/heads/main $S9 refs/heads/main $ZERO" > "$WORK/q14-out" 2>&1 &
pusher=$!
# Wait for the hook to SAY it waits (not a fixed sleep: on a loaded machine
# the hook may not reach the lock within any fixed time), then look.
i=0
announced=yes
until grep -q 'waiting' "$WORK/q14-out" 2>/dev/null; do
	i=$((i + 1))
	if [ "$i" -gt 100 ] || [ -e "$WORK/q14-ran" ]; then
		announced=no
		break
	fi
	sleep 0.1
done
# Announced is not proof of waiting: a hook that announces and then runs
# anyway (a non-blocking take) gets a second to show it.
[ "$announced" = no ] || sleep 1
early=no
[ ! -e "$WORK/q14-ran" ] || early=yes
touch "$WORK/q14-release"
wait "$holder" || true
wait "$pusher" && rc=0 || rc=$?
[ "$announced" = yes ] || fail "QA-14: a held lock must be announced
$(cat "$WORK/q14-out")"
[ "$early" = no ] || fail "QA-14: the tier ran while another push held the tree's lock"
[ "$rc" -eq 0 ] && [ -e "$WORK/q14-ran" ] || fail "QA-14: after the release the tier must run and allow (rc=$rc)
$(cat "$WORK/q14-out")"

# QA-15: the memory gate. A stub gate records its arguments and exits
# $Q15_RC. (a) named, it is asked `admit tier --wait 1800` before the tier;
# (b) refusing, the push is blocked and the tier never starts; (c) the
# DEFAULT tier asks $HOME/bin/mem-gate.py (here a refusing stub, so check.sh
# never runs; without the ask, the fixture's missing check.sh blocks with no
# record); (d) a stubbed tier asks no default gate, the nesting guard: the
# real tier runs this check and must not book itself twice; (e) a gate that
# does not exist is skipped silently.
MG="$WORK/mg"
printf '#!/bin/sh\necho "gate $*" >> "$Q15_LOG"\nexit "${Q15_RC:-0}"\n' > "$MG"
chmod +x "$MG"
FAKEHOME="$WORK/fakehome"
mkdir -p "$FAKEHOME/bin"
cp "$MG" "$FAKEHOME/bin/mem-gate.py"
L="$WORK/q15a"
hook8 "echo tier >> '$L'" "refs/heads/main $S9 refs/heads/main $ZERO" BUDDY_PREPUSH_MEMGATE="$MG" Q15_LOG="$L" >/dev/null \
	|| fail "QA-15a: an admitting gate and a green tier must allow"
[ "$(cat "$L")" = "$(printf 'gate admit tier --wait 1800\ntier')" ] || fail "QA-15a: want the gate's ask, then the tier:
$(cat "$L")"
L="$WORK/q15b"
out=$(hook8 "echo tier >> '$L'" "refs/heads/main $S9 refs/heads/main $ZERO" BUDDY_PREPUSH_MEMGATE="$MG" Q15_LOG="$L" Q15_RC=3) && rc=0 || rc=$?
[ "$rc" -ne 0 ] && echo "$out" | grep -q 'BLOCKED' || fail "QA-15b: a refusing gate must block (rc=$rc)
$out"
[ "$(cat "$L")" = "gate admit tier --wait 1800" ] || fail "QA-15b: the tier ran past a refusing gate:
$(cat "$L")"
L="$WORK/q15c"
out=$( cd "$R8" && printf 'refs/heads/main %s refs/heads/main %s\n' "$S9" "$ZERO" \
	| env -u BUDDY_PREPUSH_CHECK -u BUDDY_PREPUSH_MEMGATE BUDDY_PREPUSH_SKIP= HOME="$FAKEHOME" Q15_LOG="$L" Q15_RC=3 sh "$HOOK" origin url 2>&1 ) && rc=0 || rc=$?
[ "$rc" -ne 0 ] || fail "QA-15c: the default tier past a refusing default gate must block (rc=$rc)
$out"
[ "$(cat "$L" 2>/dev/null)" = "gate admit tier --wait 1800" ] || fail "QA-15c: the default tier never asked \$HOME/bin/mem-gate.py
$out"
L="$WORK/q15d"
hook8 "echo tier >> '$L'" "refs/heads/main $S9 refs/heads/main $ZERO" HOME="$FAKEHOME" Q15_LOG="$L" Q15_RC=3 >/dev/null \
	|| fail "QA-15d: a stubbed tier must not ask the default gate"
[ "$(cat "$L")" = "tier" ] || fail "QA-15d: a stubbed tier asked the default gate:
$(cat "$L")"
out=$(hook8 'exit 0' "refs/heads/main $S9 refs/heads/main $ZERO" BUDDY_PREPUSH_MEMGATE="$WORK/no-such-gate") || fail "QA-15e: a missing gate must be skipped
$out"
case "$out" in
*memory*) fail "QA-15e: a missing gate must be skipped SILENTLY:
$out" ;;
esac

# QA-16: a tree a killed run left broken is rebuilt, not trusted: (a) its
# .git gone, (b) a stale index.lock that fails the checkout.
[ -d "$STATE8/tree/.git" ] || fail "QA-16 control: no tree from the runs above"
rm -rf "$STATE8/tree/.git"
out=$(hook8 "git rev-parse HEAD > '$WORK/q16a'" "refs/heads/main $S9 refs/heads/main $ZERO") || fail "QA-16a: a tree with no .git must be rebuilt
$out"
[ "$(cat "$WORK/q16a")" = "$S9" ] || fail "QA-16a: the rebuilt tree tested '$(cat "$WORK/q16a")', not $S9"
: > "$STATE8/tree/.git/index.lock"
out=$(hook8 "git rev-parse HEAD > '$WORK/q16b'" "refs/heads/other $S10 refs/heads/other $ZERO") || fail "QA-16b: a tree with a stale index.lock must be rebuilt
$out"
[ "$(cat "$WORK/q16b")" = "$S10" ] || fail "QA-16b: the rebuilt tree tested '$(cat "$WORK/q16b")', not $S10"

# QA-17: (a) a pushed sha this repository cannot read BLOCKS (QA-1 is the
# readable control); (b) a tag of a tree names no commit and has no tree to
# test, so even a red tier is not run for it.
out=$(hook8 'exit 0' "refs/heads/main 1111111111111111111111111111111111111111 refs/heads/main $ZERO") && rc=0 || rc=$?
[ "$rc" -ne 0 ] && echo "$out" | grep -q 'BLOCKED' || fail "QA-17a: an unreadable pushed sha must block (rc=$rc)
$out"
git -C "$R8" tag -a treetag -m 'a tree' "$S9^{tree}"
ST=$(git -C "$R8" rev-parse treetag)
out=$(hook8 'exit 1' "refs/tags/treetag $ST refs/tags/treetag $ZERO") || fail "QA-17b: a tag of a tree has no tree to test and must not be gated
$out"
echo "$out" | grep -q 'names no commit' || fail "QA-17b: the skipped tag must be named
$out"
git -C "$R8" tag -d treetag >/dev/null
# (c) readable but unpeelable: a tag object whose commit is missing. Neither a
# commit nor positively a tree or a blob, so it blocks (Codex).
SM=$(printf 'object 2222222222222222222222222222222222222222\ntype commit\ntag broken\ntagger t <t@t> 0 +0000\n\nbroken\n' | git -C "$R8" hash-object -t tag -w --literally --stdin)
git -C "$R8" cat-file -e "$SM" || fail "QA-17c control: the broken tag object was not written"
out=$(hook8 'exit 0' "refs/tags/broken $SM refs/tags/broken $ZERO") && rc=0 || rc=$?
[ "$rc" -ne 0 ] && echo "$out" | grep -q 'BLOCKED' || fail "QA-17c: a tag naming a missing commit must block, not skip (rc=$rc)
$out"

# QA-18: a tier that leaves a child running (a daemon, a stray sleep) must not
# keep the tree's lock past the hook: fd 9 is closed for the tier, so the next
# push takes the lock at once and says nothing of waiting.
out=$(hook8 "sleep 30 </dev/null >/dev/null 2>&1 & echo \$! > '$WORK/q18-pid'" "refs/heads/main $S9 refs/heads/main $ZERO") || fail "QA-18: the first run must allow
$out"
out=$(hook8 'exit 0' "refs/heads/other $S10 refs/heads/other $ZERO") && rc=0 || rc=$?
kill "$(cat "$WORK/q18-pid")" 2>/dev/null || true
[ "$rc" -eq 0 ] || fail "QA-18: the second run must allow (rc=$rc)
$out"
case "$out" in
*waiting*) fail "QA-18: the tier's leftover child held the tree's lock:
$out" ;;
esac

# QA-19: a replacement ref never chooses the commit tested. Pushed: tag tc,
# naming C (red). refs/replace/<tc> points at tag td, naming B (green). Git
# sends tc as stored, so the tier must test C and block.
git -C "$R8" tag -a tc -m c "$SC"
git -C "$R8" tag -a td -m d "$SB"
TC=$(git -C "$R8" rev-parse tc)
git -C "$R8" replace "$TC" "$(git -C "$R8" rev-parse td)"
[ "$(git -C "$R8" rev-parse "$TC^{commit}")" = "$SB" ] || fail "QA-19 control: the replacement does not redirect the peel on this git"
out=$(hook8 "h=\$(git rev-parse HEAD); echo \"\$h\" > '$WORK/q19'; test \"\$h\" != $SC" "refs/tags/tc $TC refs/tags/tc $ZERO") && rc=0 || rc=$?
[ "$rc" -ne 0 ] && [ "$(cat "$WORK/q19")" = "$SC" ] || fail "QA-19: a replaced tag had the tier test '$(cat "$WORK/q19")', not the pushed tag's $SC (rc=$rc)
$out"
git -C "$R8" replace -d "$TC" >/dev/null
git -C "$R8" tag -d tc td >/dev/null

# QA-20: the lock is held for the WHOLE run, not only while it is taken. Push A's
# tier holds until released; push B, started meanwhile, waits and runs only
# after A ends (a take followed by an unlock passes QA-14 and fails here).
rm -f "$WORK/q20-a" "$WORK/q20-arel" "$WORK/q20-b"
hook8 "touch '$WORK/q20-a'; i=0; while [ ! -e '$WORK/q20-arel' ] && [ \$i -lt 300 ]; do sleep 0.1; i=\$((i+1)); done" "refs/heads/main $S9 refs/heads/main $ZERO" > "$WORK/q20-aout" 2>&1 &
pa=$!
i=0
until [ -e "$WORK/q20-a" ]; do
	i=$((i + 1))
	[ "$i" -le 100 ] || { fail "QA-20 control: push A's tier never started"; break; }
	sleep 0.1
done
hook8 "touch '$WORK/q20-b'" "refs/heads/other $S10 refs/heads/other $ZERO" > "$WORK/q20-bout" 2>&1 &
pb=$!
i=0
until grep -q 'waiting' "$WORK/q20-bout" 2>/dev/null || [ -e "$WORK/q20-b" ] || [ "$i" -gt 100 ]; do
	i=$((i + 1))
	sleep 0.1
done
sleep 1
early=no
[ ! -e "$WORK/q20-b" ] || early=yes
touch "$WORK/q20-arel"
wait "$pa" || true
wait "$pb" && rc=0 || rc=$?
[ "$early" = no ] || fail "QA-20: push B's tier ran while push A's held the tree"
[ "$rc" -eq 0 ] && [ -e "$WORK/q20-b" ] || fail "QA-20: push B must run after A (rc=$rc)
$(cat "$WORK/q20-bout")"

echo "check-pre-push: GREEN (QA-1 green allows, QA-2 red blocks + bypass named,"
echo "  QA-3 deletion-safe, QA-4 kill switch, QA-5 no-repo no-op,"
echo "  QA-6 a linked-worktree push: the hook saw an absolute GIT_DIR, the tier none of git's local env,"
echo "  QA-7 sourcing lib.sh clears git's local env, with and without git on PATH,"
echo "  QA-8..11 the tier runs on each pushed tip, not the working tree: an untracked helper blocks,"
echo "  a fixed working tree reads the broken commit, another branch is its own sha, distinct tips once,"
echo "  QA-12 the checkout's status, worktrees, refs and stash unchanged, QA-13 one stable clean tree,"
echo "  QA-14 the tree is locked, QA-15 the memory gate asked, obeyed, defaulted and skipped,"
echo "  QA-16 a broken tree is rebuilt, QA-17 an unreadable or unpeelable sha blocks, a tree's tag is skipped,"
echo "  QA-18 a tier's leftover child does not hold the lock, QA-19 a replace ref picks no commit,"
echo "  QA-20 the lock is held for the whole run)"
