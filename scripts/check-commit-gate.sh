#!/bin/sh
# check-commit-gate.sh — done-check for the commit-time claim gate.
#
# HERMETIC: git, sh, and a freshly built `buddy`. No network, no daemon, no
# ledger outside the throwaway repo this creates.
#
# WHY THIS EXISTS ALONGSIDE THE GO SUITE. The Go tests call cmdCommitGate
# directly, so they can prove what the gate decides but not what GIT tells it.
# Two properties are invisible from in-process and are exactly the ones that
# would fail silently in production:
#
#   QA-5  a PARTIAL commit (`git commit -- <path>`) builds a TEMPORARY index and
#         points GIT_INDEX_FILE at it. The gate must inherit that variable, or
#         it reads the real index and warns about files this commit does not
#         touch. Nothing in-process sets that variable, so the Go suite passes
#         with the environment handling mutated to the wrong one — measured.
#   QA-2  the hook must PROPAGATE the gate's exit status. Every Claude Code hook
#         line in this project ends in `exit 0`; copying that idiom here makes
#         the deny posture and the fail-closed arm both inert while still
#         printing everything they would have printed.
set -eu
. "$(dirname -- "$0")/lib.sh"
ROOT=$(repo_root "$0")
CDPATH= cd -- "$ROOT"
HOOK="$ROOT/.githooks/pre-commit"

fail() { echo "check-commit-gate: FAIL — $*" >&2; exit 1; }

[ -f "$HOOK" ] || fail "no .githooks/pre-commit"

# QA-0: source-shape gates on the hook itself.
grep -q 'no-verify' "$HOOK" || fail "QA-0: the hook must document the --no-verify escape hatch"
# The verdict must reach git. `exec buddy commit-gate` is the shape that does;
# a trailing unconditional `exit 0` is the shape that silently does not.
grep -q '^exec .*commit-gate' "$HOOK" || fail "QA-0: the hook must exec the gate so its exit status becomes the hook's"
if grep -qE '^[[:space:]]*exit 0[[:space:]]*$' "$HOOK" && ! grep -qB2 'BUDDY_COMMIT_GATE_SKIP\|-z "\$BUDDY"' "$HOOK"; then
	fail "QA-0: an unconditional trailing 'exit 0' would discard the gate's verdict"
fi

WORK=$(mktemp -d) || fail "mktemp failed"
trap 'rm -rf "$WORK"' EXIT INT TERM

go build -o "$WORK/bin/buddy" ./cmd/buddy || fail "could not build buddy"
PATH="$WORK/bin:$PATH"
export PATH

REPO="$WORK/repo"
mkrepo "$REPO"
# The hook under test, reached the way a real checkout reaches it.
git -C "$REPO" config core.hooksPath "$ROOT/.githooks"

seed() { mkdir -p "$(dirname "$REPO/$1")" && printf '%s\n' "$2" > "$REPO/$1"; }

seed README.md init
git -C "$REPO" add -A
git -C "$REPO" commit -q -m init

buddy() { ( cd "$REPO" && command buddy "$@" ); }
hello() { printf '{"session_id":"%s","cwd":"%s"}' "$1" "$REPO" | buddy hello --label "$2" >/dev/null; }

buddy init >/dev/null
hello sess-a alpha
hello sess-b bravo
buddy claim router-work --session sess-a --desc "edge cap" --scope internal/router >/dev/null

# Every commit below is made AS sess-b, the way an agent's Bash tool call is.
export CLAUDE_CODE_SESSION_ID=sess-b

commit() { # $1=message; remaining args passed to git commit. Captures rc+output.
	msg=$1; shift
	out=$( cd "$REPO" && git commit -m "$msg" "$@" 2>&1 ) && rc=0 || rc=$?
	printf '%s' "$out"
	return $rc
}

# QA-1: a staged path inside another session's claim WARNS and still commits.
seed internal/router/proxy.go one
git -C "$REPO" add -A
out=$(commit "warn case") && rc=0 || rc=$?
[ "$rc" -eq 0 ] || fail "QA-1: warn posture must not block the commit (rc=$rc)
$out"
echo "$out" | grep -q 'router-work' || fail "QA-1: the warning must name the claim slug
$out"
echo "$out" | grep -q 'alpha' || fail "QA-1: the warning must name the claim's owner
$out"
echo "$out" | grep -q 'internal/router/proxy.go' || fail "QA-1: the warning must name the colliding path
$out"

# QA-1b: the CONTROL for QA-1 — a path nobody claimed produces no warning at
# all. Without this, a gate that warned unconditionally would pass QA-1.
seed docs/notes.md two
git -C "$REPO" add -A
out=$(commit "control case") && rc=0 || rc=$?
[ "$rc" -eq 0 ] || fail "QA-1b: an unclaimed path must commit cleanly (rc=$rc)
$out"
echo "$out" | grep -q 'commit-gate' && fail "QA-1b: an unclaimed path must produce NO gate output
$out"

# QA-2: deny posture BLOCKS, and the block reaches git.
seed internal/router/edge.go three
git -C "$REPO" add -A
out=$(BUDDY_COMMIT_GATE=deny commit "deny case") && rc=0 || rc=$?
[ "$rc" -ne 0 ] || fail "QA-2: deny posture must refuse the commit — the hook is discarding the verdict
$out"
echo "$out" | grep -q 'REFUSING' || fail "QA-2: the refusal must say so
$out"
echo "$out" | grep -q 'no-verify' || fail "QA-2: the refusal must name its bypass
$out"
# And the commit really did not happen. The log is CAPTURED rather than piped
# into grep: `git log | grep -q 'deny case' && fail` passes vacuously when the
# git command itself fails (sh has no pipefail), so a fixture repo that had
# stopped being readable would report this leg green forever.
log=$( cd "$REPO" && git log --oneline -1 ) || fail "QA-2: could not read the fixture's log; the assertion would have passed vacuously"
case $log in
*'deny case'*) fail "QA-2: the commit landed despite the refusal" ;;
esac

# QA-3: the documented bypass works.
out=$(BUDDY_COMMIT_GATE=deny commit "bypass case" --no-verify) && rc=0 || rc=$?
[ "$rc" -eq 0 ] || fail "QA-3: --no-verify must bypass the gate (rc=$rc)
$out"

# QA-4: the kill switch works even under deny.
seed internal/router/more.go four
git -C "$REPO" add -A
out=$(BUDDY_COMMIT_GATE=deny BUDDY_COMMIT_GATE_SKIP=1 commit "skip case") && rc=0 || rc=$?
[ "$rc" -eq 0 ] || fail "QA-4: BUDDY_COMMIT_GATE_SKIP=1 must skip the gate (rc=$rc)
$out"

# QA-5: THE PARTIAL-COMMIT LEG. Two files staged, only one of them inside the
# claim; commit ONLY the innocent one, under deny. git builds a temporary index
# for this and names it in GIT_INDEX_FILE. The gate must adjudicate the paths in
# THAT index — one path, no collision — and allow.
#
# With the child environment sanitized the way a background scan sanitizes it,
# the gate reads the real index instead, finds the colliding file that is staged
# but NOT being committed, and refuses. That is the mutant this leg exists to
# kill: it fails as a REFUSAL of a commit that has nothing wrong with it.
seed internal/router/inside.go five
seed docs/outside.md six
git -C "$REPO" add -A
out=$(BUDDY_COMMIT_GATE=deny commit "partial case" -- docs/outside.md) && rc=0 || rc=$?
[ "$rc" -eq 0 ] || fail "QA-5: a partial commit of an unclaimed path must be allowed — the gate is
reading the real index instead of this commit's temporary one (rc=$rc)
$out"
# And the control: the same partial commit OF the colliding path must refuse,
# or QA-5 would pass on a gate that simply never denies partial commits.
out=$(BUDDY_COMMIT_GATE=deny commit "partial control" -- internal/router/inside.go) && rc=0 || rc=$?
[ "$rc" -ne 0 ] || fail "QA-5: a partial commit OF a claimed path must still be refused
$out"

# QA-6: a repo that was never buddy-inited is a silent no-op, not a failure.
BARE="$WORK/nobuddy"
mkrepo "$BARE"
git -C "$BARE" config core.hooksPath "$ROOT/.githooks"
printf 'x\n' > "$BARE/f.txt"
git -C "$BARE" add -A
out=$( cd "$BARE" && git commit -m "no ledger" 2>&1 ) && rc=0 || rc=$?
[ "$rc" -eq 0 ] || fail "QA-6: a repo with no ledger must commit normally (rc=$rc)
$out"
echo "$out" | grep -q 'commit-gate' && fail "QA-6: a repo with no ledger must produce NO gate output
$out"

# QA-7: an existing but unreadable ledger FAILS CLOSED. This is the arm that
# separates "the feature is off" from "the safety mechanism is broken".
COMMON=$(git -C "$REPO" rev-parse --path-format=absolute --git-common-dir)
printf 'this is not a database\n' > "$COMMON/buddy.db"
seed docs/after.md seven
git -C "$REPO" add -A
out=$(commit "corrupt ledger") && rc=0 || rc=$?
[ "$rc" -ne 0 ] || fail "QA-7: an unreadable ledger must fail closed even in warn posture (rc=$rc)
$out"
echo "$out" | grep -q 'ledger unavailable' || fail "QA-7: the failure must name the cause
$out"

echo "check-commit-gate: GREEN (QA-1 warn+named, QA-1b clean control, QA-2 deny blocks via the hook,"
echo "  QA-3 --no-verify bypass, QA-4 kill switch, QA-5 partial-commit temp index + control,"
echo "  QA-6 no-ledger silent, QA-7 unreadable ledger fails closed)"
