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
# and a stale copy is replaced with the old one kept, not lost. Two skills
# ship (D-114), the core and buddy-fleet, installed together or not at all.
set -eu
. "$(dirname -- "$0")/lib.sh"
ROOT=$(repo_root "$0")
CDPATH= cd -- "$ROOT"
# BUDDY_FLEET_SKILL_DIR inherited from the caller would send the fleet skill
# to a REAL skills directory (Codex code pass): unset, so it goes beside the
# fixture's core.
unset BUDDY_FLEET_SKILL_DIR

fail() { echo "check-skill: FAIL — $*" >&2; exit 1; }

WORK=$(mktemp -d) || fail "mktemp failed"
trap 'rm -rf "$WORK"' EXIT INT TERM
DIR="$WORK/skills/buddy"
FDIR="$WORK/skills/buddy-fleet"
SRC=skills/buddy/SKILL.md
FSRC=skills/buddy-fleet/SKILL.md

# QA-1: a fresh install creates both directories, each with an identical copy:
# the fleet skill beside the core by default, with no knob of its own set.
out=$(BUDDY_SKILL_DIR="$DIR" sh scripts/install-skill.sh) || fail "QA-1: install failed"
[ "$(echo "$out" | grep -c '^install-skill: installed ')" -eq 2 ] || fail "QA-1: want two installed lines: $out"
cmp -s "$SRC" "$DIR/SKILL.md" || fail "QA-1: the installed core differs from the repo's"
cmp -s "$FSRC" "$FDIR/SKILL.md" || fail "QA-1: the fleet skill was not installed beside the core"

# QA-2: a re-run over identical copies says current twice and writes no backup.
out=$(BUDDY_SKILL_DIR="$DIR" sh scripts/install-skill.sh) || fail "QA-2: re-run failed"
[ "$(echo "$out" | grep -c ' is current$')" -eq 2 ] || fail "QA-2: $out"
[ ! -e "$DIR/SKILL.md.bak" ] && [ ! -e "$FDIR/SKILL.md.bak" ] || fail "QA-2: a current copy must not be backed up"

# QA-3: a stale copy is replaced, and the stale one is kept as .bak. The stale
# core is the shape every machine had before the split: one whole skill at
# skills/buddy and no buddy-fleet. It is refreshed and the fleet skill added.
printf 'an older skill\n' > "$DIR/SKILL.md"
rm -rf "$FDIR"
out=$(BUDDY_SKILL_DIR="$DIR" sh scripts/install-skill.sh) || fail "QA-3: refresh failed"
echo "$out" | grep -q '^install-skill: updated .*/buddy/SKILL.md ' || fail "QA-3: $out"
echo "$out" | grep -q '^install-skill: installed .*/buddy-fleet/SKILL.md$' || fail "QA-3: the fleet skill was not added: $out"
cmp -s "$SRC" "$DIR/SKILL.md" || fail "QA-3: the stale copy was not replaced"
[ "$(cat "$DIR/SKILL.md.bak")" = "an older skill" ] || fail "QA-3: the previous copy was not kept"

# QA-7: BUDDY_FLEET_SKILL_DIR places the fleet skill anywhere, and nothing is
# written beside the core then.
ODIR="$WORK/other/core"; OFDIR="$WORK/elsewhere/fleet"
BUDDY_SKILL_DIR="$ODIR" BUDDY_FLEET_SKILL_DIR="$OFDIR" sh scripts/install-skill.sh >/dev/null || fail "QA-7: install failed"
cmp -s "$FSRC" "$OFDIR/SKILL.md" || fail "QA-7: the fleet skill is not where the knob put it"
[ ! -e "$WORK/other/buddy-fleet" ] || fail "QA-7: the knob was set, yet a copy went beside the core"

# QA-8: a fleet destination it must refuse refuses the WHOLE install: the
# core is not refreshed either, and the refusal names the fleet path. The
# control is QA-3: the same stale core with a writable fleet dir is refreshed.
PDIR="$WORK/pair/buddy"; mkdir -p "$PDIR" "$WORK/pair/buddy-fleet/SKILL.md"
printf 'stale core\n' > "$PDIR/SKILL.md"
if out=$(BUDDY_SKILL_DIR="$PDIR" sh scripts/install-skill.sh 2>&1); then fail "QA-8: a directory named buddy-fleet/SKILL.md must be refused"; fi
echo "$out" | grep -q 'buddy-fleet/SKILL.md is a symlink or not a regular file' || fail "QA-8: the refusal does not name the fleet path: $out"
[ "$(cat "$PDIR/SKILL.md")" = "stale core" ] || fail "QA-8: the core was refreshed although the fleet skill was refused"

# QA-9: one directory for both is refused before anything is written, spelled
# the same or through a symlinked alias (one install there wrote the core,
# then the fleet skill over it, then the fresh core over the core's backup).
# QA-7 is the control: two different directories install. The refusal must
# be the same-directory one: the second lock (in the directory this run has
# just locked) would refuse too, with a message that blames another install.
SDIR="$WORK/same/dir"; mkdir -p "$SDIR"; ln -s "$WORK/same" "$WORK/alias"
for f in "$SDIR" "$WORK/alias/dir"; do
	if out=$(BUDDY_SKILL_DIR="$SDIR" BUDDY_FLEET_SKILL_DIR="$f" sh scripts/install-skill.sh 2>&1); then
		fail "QA-9: one directory for both skills ($f) must be refused"
	fi
	echo "$out" | grep -q 'name the same directory' || fail "QA-9: refused, but not as one directory for both ($f): $out"
done
[ ! -e "$SDIR/SKILL.md" ] || fail "QA-9: the refused install wrote a skill"

# QA-10: a lock held in the FLEET directory refuses too, changes nothing, and
# leaves no core lock behind (the exit releases the one it took).
mkdir "$FDIR/.install-skill.lock"
printf 'held\n' > "$DIR/SKILL.md"
if BUDDY_SKILL_DIR="$DIR" sh scripts/install-skill.sh >/dev/null 2>&1; then fail "QA-10: a held fleet lock must refuse"; fi
[ "$(cat "$DIR/SKILL.md")" = "held" ] || fail "QA-10: the refused run changed the core"
[ ! -e "$DIR/.install-skill.lock" ] || fail "QA-10: the core lock was left behind"
rmdir "$FDIR/.install-skill.lock"

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

echo "check-skill: GREEN (QA-1 fresh install of both, QA-2 current copies untouched, QA-3 stale single-file copy replaced + kept + fleet added, QA-4 wired into setup-clone, QA-5 symlink + directory refused, QA-6 one installer at a time, QA-7 fleet dir knob, QA-8 a refused fleet dir installs neither, QA-9 one dir for both refused, QA-10 fleet lock)"
