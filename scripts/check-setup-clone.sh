#!/bin/sh
# check-setup-clone.sh — done-check for setup-clone's hooks-path rule.
#
# WHAT IS CHECKED. setup-clone never steals a core.hooksPath somebody else
# configured, and it does not mistake ANOTHER SPELLING of this checkout's own
# .githooks for somebody else's. The second half is the 2026-10-13 failure: the
# path was set absolute by hand, the literal comparison refused it, and every
# install.sh ended "FAILED" at step 5 after rebuilding everything (the hook and
# handoff reports never printed). An equal path is left AS SET, never rewritten
# to ".githooks": absolute also covers linked worktrees, relative does not.
#
# THE FIXTURE is a throwaway clone holding copies of the script, the skill
# installer, the skill and .githooks, with a stub `buddy` that knows
# commit-gate. HOME, PATH and BUDDY_SKILL_DIR are pinned, and
# BUDDY_FLEET_SKILL_DIR unset, so nothing here touches the real skills, binary
# or ledger.
set -eu
. "$(dirname -- "$0")/lib.sh"
ROOT=$(repo_root "$0")
CDPATH= cd -- "$ROOT"

fail() { echo "check-setup-clone: FAIL — $*" >&2; exit 1; }

WORK=$(mktemp -d) || fail "mktemp failed"
trap 'rm -rf "$WORK"' EXIT INT TERM
WORK=$(CDPATH= cd -- "$WORK" && pwd -P)

CLONE="$WORK/clone"
mkrepo "$CLONE"
mkdir -p "$CLONE/scripts" "$CLONE/skills/buddy" "$CLONE/skills/buddy-fleet" "$WORK/bin" "$WORK/home" "$WORK/other-hooks"
cp scripts/setup-clone.sh scripts/install-skill.sh "$CLONE/scripts/"
cp skills/buddy/SKILL.md "$CLONE/skills/buddy/"
cp skills/buddy-fleet/SKILL.md "$CLONE/skills/buddy-fleet/"
cp -R .githooks "$CLONE/.githooks"
printf '#!/bin/sh\ncase "$1" in help) echo "usage: buddy commit-gate ...";; esac\nexit 0\n' > "$WORK/bin/buddy"
chmod +x "$WORK/bin/buddy"
ln -s "$WORK" "$WORK/alias"

# run [hooksPath] — set (or unset) the path, run setup-clone; prints its output
# and leaves its status in $rc.
run() {
	if [ $# -eq 0 ]; then
		git -C "$CLONE" config --unset core.hooksPath 2>/dev/null || true
	else
		git -C "$CLONE" config core.hooksPath "$1"
	fi
	out=$(env -u BUDDY_FLEET_SKILL_DIR HOME="$WORK/home" PATH="$WORK/bin:/usr/bin:/bin" BUDDY_SKILL_DIR="$WORK/skill" \
		sh "$CLONE/scripts/setup-clone.sh" 2>&1) && rc=0 || rc=$?
}
now() { git -C "$CLONE" config --get core.hooksPath || echo "(unset)"; }

# QA-1: unset is the fresh-clone case: wired as ".githooks". Also the control
# that the fixture runs setup-clone to completion at all.
run
[ "$rc" -eq 0 ] || fail "QA-1: a fresh clone must wire (rc=$rc)
$out"
[ "$(now)" = ".githooks" ] || fail "QA-1: hooksPath is '$(now)', not .githooks"

# QA-2: the measured case — this checkout's own .githooks, absolute. Accepted
# and left exactly as set.
run "$CLONE/.githooks"
[ "$rc" -eq 0 ] || fail "QA-2: our own .githooks spelled absolute was refused (rc=$rc)
$out"
[ "$(now)" = "$CLONE/.githooks" ] || fail "QA-2: an equal path was rewritten to '$(now)'"

# QA-3: the same directory through a symlinked parent (/var is /private/var on
# macOS, so this is how a temp or aliased root arrives).
run "$WORK/alias/clone/.githooks"
[ "$rc" -eq 0 ] || fail "QA-3: our own .githooks via a symlinked parent was refused (rc=$rc)
$out"
[ "$(now)" = "$WORK/alias/clone/.githooks" ] || fail "QA-3: rewritten to '$(now)'"

# QA-4: somebody else's existing directory is still REFUSED and left alone.
# This is the guard QA-2 and QA-3 must not have opened.
run "$WORK/other-hooks"
[ "$rc" -ne 0 ] || fail "QA-4: a foreign hooks path was accepted
$out"
[ "$(now)" = "$WORK/other-hooks" ] || fail "QA-4: a foreign hooks path was overwritten with '$(now)'"
echo "$out" | grep -qF "already set to '$WORK/other-hooks'" || fail "QA-4: the refusal does not name the path
$out"

# QA-5: a path that does not exist cannot be ours: refused, not resolved by luck.
run "$WORK/nowhere/.githooks"
[ "$rc" -ne 0 ] || fail "QA-5: a missing hooks path was accepted
$out"

# QA-6: a relative spelling resolves from the checkout root, as git resolves it.
run "./.githooks"
[ "$rc" -eq 0 ] || fail "QA-6: './.githooks' was refused (rc=$rc)
$out"
[ "$(now)" = "./.githooks" ] || fail "QA-6: rewritten to '$(now)'"

echo "check-setup-clone: ok (6 cases)"
