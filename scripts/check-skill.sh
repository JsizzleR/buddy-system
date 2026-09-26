#!/bin/sh
# check-skill.sh — done-check for install-skill.sh (D-049's skill rule).
#
# HERMETIC: sh and the repo. It installs into a throwaway directory through
# BUDDY_SKILL_DIR and never touches ~/.claude. The Go suite gates the skill's
# CONTENT (every verb and flag taught, every name it teaches real:
# TestSkillTeachesEveryVerbAndFlag, TestSkillNamesOnlyWhatHelpAnswers); this
# gates the step that gets that content to where sessions load it.
#
# Each case has its positive control: a fresh install lands, a re-run on an
# identical copy changes nothing (control that "current" is not "rewrote"),
# and a stale copy is replaced with the old one kept, not lost.
set -eu
. "$(dirname -- "$0")/lib.sh"
ROOT=$(repo_root "$0")
CDPATH= cd -- "$ROOT"

fail() { echo "check-skill: FAIL — $*" >&2; exit 1; }

WORK=$(mktemp -d) || fail "mktemp failed"
trap 'rm -rf "$WORK"' EXIT INT TERM
DIR="$WORK/skills/buddy"
SRC=skills/buddy/SKILL.md

# QA-1: a fresh install creates the directory and an identical copy.
out=$(BUDDY_SKILL_DIR="$DIR" sh scripts/install-skill.sh) || fail "QA-1: install failed"
echo "$out" | grep -q '^install-skill: installed ' || fail "QA-1: $out"
cmp -s "$SRC" "$DIR/SKILL.md" || fail "QA-1: the installed copy differs from the repo's"

# QA-2: a re-run over an identical copy says current and writes no backup.
out=$(BUDDY_SKILL_DIR="$DIR" sh scripts/install-skill.sh) || fail "QA-2: re-run failed"
echo "$out" | grep -q ' is current$' || fail "QA-2: $out"
[ ! -e "$DIR/SKILL.md.bak" ] || fail "QA-2: a current copy must not be backed up"

# QA-3: a stale copy is replaced, and the stale one is kept as .bak.
printf 'an older skill\n' > "$DIR/SKILL.md"
out=$(BUDDY_SKILL_DIR="$DIR" sh scripts/install-skill.sh) || fail "QA-3: refresh failed"
echo "$out" | grep -q '^install-skill: updated ' || fail "QA-3: $out"
cmp -s "$SRC" "$DIR/SKILL.md" || fail "QA-3: the stale copy was not replaced"
[ "$(cat "$DIR/SKILL.md.bak")" = "an older skill" ] || fail "QA-3: the previous copy was not kept"

# QA-5: a SKILL.md that is a symlink or a directory is refused, and nothing
# is written through it; QA-1..3 are the control that a regular file installs.
LDIR="$WORK/link/buddy"; mkdir -p "$LDIR"
printf 'somebody else\n' > "$WORK/elsewhere.md"
ln -s "$WORK/elsewhere.md" "$LDIR/SKILL.md"
if BUDDY_SKILL_DIR="$LDIR" sh scripts/install-skill.sh >/dev/null 2>&1; then fail "QA-5: a symlinked SKILL.md must be refused"; fi
[ "$(cat "$WORK/elsewhere.md")" = "somebody else" ] || fail "QA-5: the refusal wrote through the symlink"
DDIR="$WORK/dir/buddy"; mkdir -p "$DDIR/SKILL.md"
if BUDDY_SKILL_DIR="$DDIR" sh scripts/install-skill.sh >/dev/null 2>&1; then fail "QA-5: a directory named SKILL.md must be refused"; fi
[ ! -e "$DDIR/SKILL.md/SKILL.md" ] || fail "QA-5: a copy was made inside the directory"

# QA-6: one installer at a time. A held lock refuses and changes nothing; the
# lock is released after a normal run (QA-1..3 ran back to back).
mkdir "$DIR/.install-skill.lock"
printf 'held\n' > "$DIR/SKILL.md"
if BUDDY_SKILL_DIR="$DIR" sh scripts/install-skill.sh >/dev/null 2>&1; then fail "QA-6: a held lock must refuse"; fi
[ "$(cat "$DIR/SKILL.md")" = "held" ] || fail "QA-6: the refused run changed the copy"
rmdir "$DIR/.install-skill.lock"

# QA-4: setup-clone.sh runs it — the step is wired, not merely available.
grep -q 'scripts/install-skill.sh' scripts/setup-clone.sh || fail "QA-4: setup-clone.sh does not run install-skill.sh"

echo "check-skill: GREEN (QA-1 fresh install, QA-2 current copy untouched, QA-3 stale copy replaced + kept, QA-4 wired into setup-clone, QA-5 symlink + directory refused, QA-6 one installer at a time)"
