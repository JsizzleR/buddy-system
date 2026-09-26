#!/bin/sh
# check-wait.sh — done-check for the wait register and its keep-alive check
# (D-033, issue #26).
#
# HERMETIC: git, sh, and a freshly built `buddy`. No network, no daemon, no
# ledger outside the throwaway repo this creates, and no harness: nothing here
# schedules anything, because nothing in buddy does.
#
# WHY THIS EXISTS ALONGSIDE THE GO SUITE. The Go tests call Run in-process
# with an injected environment and clock, so they prove what the verbs decide
# but not what a session's Bash tool call actually meets. Three things are
# only visible from the built binary, and they are exactly the ones a parked
# session's /loop depends on:
#
#   - that `wait` is in the SHIPPED dispatch table at all, and that a wait
#     survives between separate processes the way a declaration and the check
#     fifty minutes later do;
#   - that the harness's id reaches `wait check` through a real environment
#     ($CLAUDE_CODE_SESSION_ID exported, as Claude Code exports it to every Bash
#     tool call), and that the check REFUSES without it — a check from a shell
#     that is not the session would stamp a keep-alive that never happened;
#   - the exit status a /loop sees: 0 for every verdict produced, non-zero for
#     a refusal, so a refused declaration can never read as a recorded one.
#
# Every refusal below has a positive control beside it (the same call, in
# bounds, accepted and listed), because "it refused" and "it never ran" are
# otherwise the same observation.
set -eu
. "$(dirname -- "$0")/lib.sh"
ROOT=$(repo_root "$0")
CDPATH= cd -- "$ROOT"

fail() { echo "check-wait: FAIL — $*" >&2; exit 1; }

WORK=$(mktemp -d) || fail "mktemp failed"
trap 'rm -rf "$WORK"' EXIT INT TERM

go build -o "$WORK/bin/buddy" ./cmd/buddy || fail "could not build buddy"
PATH="$WORK/bin:$PATH"
export PATH
# The operator's own session id must not leak into the fixture: this script
# may itself run under Claude Code, which exports it.
unset CLAUDE_CODE_SESSION_ID BUDDY_SESSION || true

REPO="$WORK/repo"
mkrepo "$REPO"
printf 'x\n' > "$REPO/README.md"
git -C "$REPO" add -A
git -C "$REPO" commit -q -m init

buddy() { ( cd "$REPO" && command buddy "$@" ); }
# as <session> <args...> — a verb as that session's own Bash tool call.
as() { s=$1; shift; ( cd "$REPO" && CLAUDE_CODE_SESSION_ID=$s command buddy "$@" ); }
hello() { printf '{"session_id":"%s","cwd":"%s"}' "$1" "$REPO" | buddy hello --label "$2" >/dev/null; }
beat_a() { printf '{"session_id":"sess-a","cwd":"%s","tool_name":"Bash","tool_input":{}}' "$REPO" | buddy beat; }
nowaits() { [ "$(buddy wait ls)" = "no open waits" ]; }

buddy init >/dev/null
hello sess-a alpha
hello sess-b bravo
as sess-b claim api-work --desc "edge cap" --scope internal/api >/dev/null
as sess-a claim mine --desc "x" --scope cmd >/dev/null

# QA-1: a deadline over the ceiling is refused and writes nothing; the same
# call at the ceiling is accepted and listed.
out=$(as sess-a wait --on api-work --until 20h 2>&1) && rc=0 || rc=$?
[ "$rc" -ne 0 ] || fail "QA-1: --until 20h must be refused (rc=$rc)
$out"
echo "$out" | grep -qF 'over the 12h0m0s ceiling' || fail "QA-1: the refusal must name the ceiling
$out"
nowaits || fail "QA-1: a refused declaration wrote a row"
as sess-a wait --on api-work --until 12h >/dev/null || fail "QA-1 control: --until 12h must be accepted"
buddy wait ls | grep -q '^wait alpha' || fail "QA-1 control: the accepted wait must be listed"
as sess-a wait clear >/dev/null || fail "QA-1: clear"

# QA-2: a slug that names no open claim, and the caller's own claim, are
# refused before any write; an open claim of another session is the control.
for bad in no-such-claim mine; do
	out=$(as sess-a wait --on "$bad" 2>&1) && rc=0 || rc=$?
	[ "$rc" -ne 0 ] || fail "QA-2: --on $bad must be refused
$out"
	nowaits || fail "QA-2: --on $bad wrote a row"
done

# QA-3: the round trip. Declare; the check says STILL WAITING and paces from
# now; the release names the waiter; beat says LANDED exactly once; the check
# says LANDED with the note; the check after that says NO WAIT.
out=$(as sess-a wait --on api-work --note "then: rebase") || fail "QA-3: declaration refused"
echo "$out" | head -1 | grep -qF 'WAITING on claim "api-work" (held by bravo' || fail "QA-3: declaration line
$out"
echo "$out" | grep -qF '/loop buddy wait check' || fail "QA-3: the declaration must print the arming line
$out"
out=$(as sess-a wait check) || fail "QA-3: check failed"
# No transcript is fed to this fixture, so the check also says the tier is
# unobserved; the pacing is the same 50m from now either way.
echo "$out" | head -1 | grep -q '^STILL WAITING on claim "api-work".*next check in 50m (3000s from now' || fail "QA-3: STILL WAITING
$out"
out=$(as sess-b release api-work) || fail "QA-3: release failed"
echo "$out" | grep -qF 'had declared a wait on it: alpha' || fail "QA-3: the release must name the waiter
$out"
out=$(beat_a)
echo "$out" | grep -qF 'your wait LANDED' || fail "QA-3: beat must announce the landing
$out"
out=$(beat_a)
if echo "$out" | grep -qF 'your wait LANDED'; then fail "QA-3: beat must announce it ONCE
$out"; fi
out=$(as sess-a wait check) || fail "QA-3: the LANDED check must exit 0"
echo "$out" | head -1 | grep -q '^LANDED: claim "api-work" released' || fail "QA-3: LANDED
$out"
echo "$out" | grep -qx 'note: then: rebase' || fail "QA-3: LANDED must carry the note on its own line
$out"
out=$(as sess-a wait check) || fail "QA-3: the NO WAIT check must exit 0"
echo "$out" | head -1 | grep -q '^NO WAIT: nothing is registered for this session (your last wait LANDED' || fail "QA-3: NO WAIT
$out"

# QA-4: the check is the harness's own session's act. Without the harness id
# it is refused and writes nothing; QA-3's checks are the control.
as sess-a wait --until 1h >/dev/null || fail "QA-4: a timer"
out=$( cd "$REPO" && command buddy wait check 2>&1 ) && rc=0 || rc=$?
[ "$rc" -ne 0 ] || fail "QA-4: a check with no harness id must be refused
$out"
buddy wait ls | grep -qF 'no check since declaring' || fail "QA-4: the refused check wrote to the row"

# QA-5: --help answers and does nothing, in first position and after flags.
for form in "wait --help" "wait --on api-work --help" "wait check --help"; do
	# shellcheck disable=SC2086
	out=$(as sess-a $form) || fail "QA-5: $form must exit 0"
	echo "$out" | head -1 | grep -q '^usage: buddy wait' || fail "QA-5: $form must print the usage line
$out"
done
buddy wait ls | grep -qF 'on a timer' || fail "QA-5: --help changed the open wait"

# QA-6 (D-049): one long run. bravo integrates on a slot; alpha rides, READY
# at its HEAD; `who` lists it; a malformed outcome releases nothing (the
# control is that who still resolves the open claim after it); the reported
# outcome reaches the rider's LANDED.
as sess-b claim herm --desc "FORMING" --scope .buddy/slot/herm --scope .buddy/slot/main >/dev/null || fail "QA-6: integrator claim"
head=$(git -C "$REPO" rev-parse HEAD)
out=$(as sess-a wait --on herm --ready HEAD) || fail "QA-6: the rider's declaration was refused"
echo "$out" | head -1 | grep -qF "you declared your work ready at $(echo "$head" | cut -c1-8)" || fail "QA-6: declaration line
$out"
out=$(buddy who herm) || fail "QA-6: who herm"
echo "$out" | grep -qF 'WAITED ON    by 1 session(s), 1 READY, 0 not:' || fail "QA-6: who must list the rider
$out"
out=$(as sess-b release herm --outcome maybe 2>&1) && rc=0 || rc=$?
[ "$rc" -ne 0 ] || fail "QA-6: an outcome off the list must be refused
$out"
buddy who herm >/dev/null 2>&1 || fail "QA-6: a refused release must leave herm open"
as sess-b release herm --outcome pass --note "landed $head" >/dev/null || fail "QA-6: release with outcome"
out=$(as sess-a wait check) || fail "QA-6: the rider's check"
echo "$out" | head -1 | grep -qF "outcome PASS \"landed $head\"" || fail "QA-6: LANDED must carry the outcome
$out"

echo "check-wait: GREEN (QA-1 ceiling refused + control, QA-2 unknown/own slug refused, QA-3 declare ->"
echo "  STILL WAITING -> release names waiter -> beat LANDED once -> LANDED + note -> NO WAIT,"
echo "  QA-4 no harness id refused, QA-5 --help writes nothing, QA-6 ready rider -> who -> outcome on LANDED)"
