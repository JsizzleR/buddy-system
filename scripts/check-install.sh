#!/bin/sh
# check-install.sh — done-check for scripts/install.sh, the machine install
# and upgrade.
#
# HERMETIC: go, git, sh. Every destination is redirected into a throwaway
# directory (BUDDY_BIN_DIR, BUDDY_SKILL_DIR, BUDDY_SETTINGS), the daemon and
# this checkout's setup-clone are switched off, and the checkout's own bin/
# copies are left alone (BUDDY_REPO_BIN=off). Nothing on the real machine is
# touched, so the tier CI runs can run it.
#
# Each refusal has its positive control beside it: a fresh install builds and
# runs both binaries and installs the skill; a re-run is idempotent; an
# unbuildable destination fails LOUD (non-zero, and says so), never green.
set -eu
. "$(dirname -- "$0")/lib.sh"
ROOT=$(repo_root "$0")
CDPATH= cd -- "$ROOT"

fail() { echo "check-install: FAIL — $*" >&2; exit 1; }

WORK=$(mktemp -d) || fail "mktemp failed"
trap 'rm -rf "$WORK"' EXIT INT TERM

inst() {
	BUDDY_BIN_DIR="$WORK/bin" BUDDY_SKILL_DIR="$WORK/skill" BUDDY_SETTINGS="$WORK/settings.json" \
		BUDDY_DAEMON=off BUDDY_SETUP_CLONE=off BUDDY_REPO_BIN=off sh scripts/install.sh "$@"
}

# A settings file with every hook but busy and the presence line, spelled the
# way README's wiring spells them.
cat > "$WORK/settings.json" <<'EOF'
{"hooks": {
 "SessionStart": [{"hooks": [{"command": "[ -x \"$HOME/bin/buddy\" ] && \"$HOME/bin/buddy\" hello 2>/dev/null; exit 0"}]}],
 "PreToolUse": [{"hooks": [{"command": "[ -x \"$HOME/bin/buddy\" ] && \"$HOME/bin/buddy\" gate; exit 0"}]}],
 "PostToolUse": [{"hooks": [{"command": "\"$HOME/bin/buddy\" beat 2>/dev/null; exit 0"},
                            {"command": "\"$HOME/bin/buddylist\" alert 2>/dev/null; exit 0"}]}],
 "Stop": [{"hooks": [{"command": "\"$HOME/bin/buddy\" idle 2>/dev/null; exit 0"}]}],
 "SessionEnd": [{"hooks": [{"command": "\"$HOME/bin/buddy\" bye 2>/dev/null; exit 0"}]}]
}}
EOF

# QA-1: a fresh install builds both binaries, runs them, installs the skill,
# and reports exactly the two hooks that are not wired.
out=$(inst 2>&1) || fail "QA-1: install failed:
$out"
[ -x "$WORK/bin/buddy" ] && [ -x "$WORK/bin/buddylist" ] || fail "QA-1: binaries missing"
"$WORK/bin/buddy" help 2>&1 | grep -q commit-gate || fail "QA-1: the installed buddy is not this checkout's"
cmp -s skills/buddy/SKILL.md "$WORK/skill/SKILL.md" || fail "QA-1: skill not installed"
echo "$out" | grep -qF 'hooks: NOT wired in' || fail "QA-1: the hook report is missing:
$out"
missing=$(echo "$out" | sed -n 's/.*NOT wired in [^:]*: *\(.*\) (README.*/\1/p')
[ "$missing" = "buddy busy buddylist presence" ] || fail "QA-1: hook report named '$missing', want 'buddy busy buddylist presence'"
echo "$out" | tail -1 | grep -qx 'install: done' || fail "QA-1: no done line"

# QA-2: re-run is idempotent — skill current, binaries rebuilt and running.
out=$(inst 2>&1) || fail "QA-2: re-run failed:
$out"
echo "$out" | grep -q 'is current$' || fail "QA-2: the skill should already be current:
$out"

# QA-3: with every hook wired, the report says so (the control for QA-1's list).
sed 's/"hooks": {/"hooks": {"UserPromptSubmit": [{"hooks": [{"command": "\\"$HOME\/bin\/buddy\\" busy"}, {"command": "\\"$HOME\/bin\/buddylist\\" presence --gone"}]}],/' \
	"$WORK/settings.json" > "$WORK/settings2.json"
mv "$WORK/settings2.json" "$WORK/settings.json"
out=$(inst 2>&1) || fail "QA-3: install failed:
$out"
echo "$out" | grep -qF 'hooks: all eight wired' || fail "QA-3: every hook is wired:
$out"

# QA-4: a destination that cannot be built fails LOUD — non-zero, and names it.
rm -rf "$WORK/bin/buddy"
mkdir "$WORK/bin/buddy"
out=$(inst 2>&1) && rc=0 || rc=$?
[ "$rc" -ne 0 ] || fail "QA-4: an unbuildable target must fail the install"
echo "$out" | grep -qF 'is a directory, not a binary' || fail "QA-4: the failure must say why:
$out"
echo "$out" | grep -qx 'install: done' && fail "QA-4: a failed install printed done"

# QA-5: the sub-steps' failures are the install's: a skill step that fails
# (its destination a directory) fails the install, not a sed after it.
rmdir "$WORK/bin/buddy"
rm -f "$WORK/skill/SKILL.md"; mkdir "$WORK/skill/SKILL.md"
out=$(inst 2>&1) && rc=0 || rc=$?
[ "$rc" -ne 0 ] || fail "QA-5: a failed skill step must fail the install:
$out"
echo "$out" | grep -qF 'install-skill exited' || fail "QA-5: the failure must name the step:
$out"

echo "check-install: GREEN (QA-1 fresh install + exact hook report, QA-2 idempotent, QA-3 all-wired control,"
echo "  QA-4 unbuildable target fails loud, QA-5 a sub-step's failure is the install's)"
