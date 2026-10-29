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
# and a stale copy is replaced with the old one kept, not lost. Every
# skills/*/SKILL.md ships (D-114, D-121: the core, buddy-fleet and buddy-lead
# today), installed together or not at all; the installer takes the list from
# the directory, with a floor of three, and the count here is read the same
# way so a fourth skill is covered the day it lands.
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
LDIR="$WORK/skills/buddy-lead"
SRC=skills/buddy/SKILL.md
FSRC=skills/buddy-fleet/SKILL.md
LSRC=skills/buddy-lead/SKILL.md
# N is what ships: the same enumeration the installer makes, so the counts
# below follow a fourth skill; the floor is the control that the glob read
# something.
N=$(set -- skills/*/SKILL.md; echo $#)
[ "$N" -ge 3 ] || fail "found $N skills under skills/, want at least 3"

# QA-1: a fresh install creates every directory, each with an identical copy:
# the fleet and lead skills beside the core by default, no knob of theirs set.
out=$(BUDDY_SKILL_DIR="$DIR" sh scripts/install-skill.sh) || fail "QA-1: install failed"
[ "$(echo "$out" | grep -c '^install-skill: installed ')" -eq "$N" ] || fail "QA-1: want $N installed lines: $out"
cmp -s "$SRC" "$DIR/SKILL.md" || fail "QA-1: the installed core differs from the repo's"
cmp -s "$FSRC" "$FDIR/SKILL.md" || fail "QA-1: the fleet skill was not installed beside the core"
cmp -s "$LSRC" "$LDIR/SKILL.md" || fail "QA-1: the lead skill was not installed beside the core"

# QA-2: a re-run over identical copies says current N times and writes no backup.
out=$(BUDDY_SKILL_DIR="$DIR" sh scripts/install-skill.sh) || fail "QA-2: re-run failed"
[ "$(echo "$out" | grep -c ' is current$')" -eq "$N" ] || fail "QA-2: $out"
[ ! -e "$DIR/SKILL.md.bak" ] && [ ! -e "$FDIR/SKILL.md.bak" ] && [ ! -e "$LDIR/SKILL.md.bak" ] || fail "QA-2: a current copy must not be backed up"

# QA-3: a stale copy is replaced, and the stale one is kept as .bak. The stale
# core is the shape every machine had before the split: one whole skill at
# skills/buddy and nothing beside it. It is refreshed and the others added.
printf 'an older skill\n' > "$DIR/SKILL.md"
rm -rf "$FDIR" "$LDIR"
out=$(BUDDY_SKILL_DIR="$DIR" sh scripts/install-skill.sh) || fail "QA-3: refresh failed"
echo "$out" | grep -q '^install-skill: updated .*/buddy/SKILL.md ' || fail "QA-3: $out"
echo "$out" | grep -q '^install-skill: installed .*/buddy-fleet/SKILL.md$' || fail "QA-3: the fleet skill was not added: $out"
echo "$out" | grep -q '^install-skill: installed .*/buddy-lead/SKILL.md$' || fail "QA-3: the lead skill was not added: $out"
cmp -s "$SRC" "$DIR/SKILL.md" || fail "QA-3: the stale copy was not replaced"
[ "$(cat "$DIR/SKILL.md.bak")" = "an older skill" ] || fail "QA-3: the previous copy was not kept"

# QA-7: BUDDY_FLEET_SKILL_DIR places the fleet skill anywhere, and nothing is
# written beside the core then; the lead skill still goes beside the core.
ODIR="$WORK/other/core"; OFDIR="$WORK/elsewhere/fleet"
BUDDY_SKILL_DIR="$ODIR" BUDDY_FLEET_SKILL_DIR="$OFDIR" sh scripts/install-skill.sh >/dev/null || fail "QA-7: install failed"
cmp -s "$FSRC" "$OFDIR/SKILL.md" || fail "QA-7: the fleet skill is not where the knob put it"
[ ! -e "$WORK/other/buddy-fleet" ] || fail "QA-7: the knob was set, yet a copy went beside the core"
cmp -s "$LSRC" "$WORK/other/buddy-lead/SKILL.md" || fail "QA-7: the lead skill does not go beside the core"

# QA-11: a core directory NAMED like another skill's default makes two
# destinations one directory: refused before anything is written, as QA-9
# refuses the two knobs. QA-1 is the control (distinct directories install).
XDIR="$WORK/x/buddy-lead"
if out=$(BUDDY_SKILL_DIR="$XDIR" sh scripts/install-skill.sh 2>&1); then fail "QA-11: a core dir that is the lead skill's dir must be refused"; fi
echo "$out" | grep -q 'name the same directory' || fail "QA-11: refused, but not as one directory for two: $out"
[ ! -e "$XDIR/SKILL.md" ] || fail "QA-11: the refused install wrote a skill"

# QA-12: a checkout with fewer than the three shipped skills is refused whole,
# naming the count, before anything is written. QA-1 is the control.
SHORT="$WORK/short"; mkdir -p "$SHORT/scripts" "$SHORT/skills/buddy" "$SHORT/skills/buddy-fleet"
cp scripts/install-skill.sh "$SHORT/scripts/"; cp "$SRC" "$SHORT/skills/buddy/"; cp "$FSRC" "$SHORT/skills/buddy-fleet/"
if out=$(BUDDY_SKILL_DIR="$WORK/short-dest/buddy" sh "$SHORT/scripts/install-skill.sh" 2>&1); then fail "QA-12: two skills in the checkout must be refused"; fi
echo "$out" | grep -q 'found 2 skill(s)' || fail "QA-12: the refusal does not name the count: $out"
[ ! -e "$WORK/short-dest" ] || fail "QA-12: the refused install wrote something"

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

# QA-13: a destination with a SPACE installs, and its locks are released (the
# trap once split the held list on whitespace and left the lock, so every
# later install refused; Codex code pass). QA-6 is the control that a held
# lock does refuse.
SPD="$WORK/with space/buddy"
BUDDY_SKILL_DIR="$SPD" sh scripts/install-skill.sh >/dev/null || fail "QA-13: a destination with a space must install"
cmp -s "$SRC" "$SPD/SKILL.md" || fail "QA-13: the core was not installed into the spaced path"
[ ! -e "$SPD/.install-skill.lock" ] && [ ! -e "$WORK/with space/buddy-lead/.install-skill.lock" ] || fail "QA-13: a lock was left behind in a spaced destination"
BUDDY_SKILL_DIR="$SPD" sh scripts/install-skill.sh >/dev/null || fail "QA-13: the re-run into the spaced path was refused (a stranded lock)"

# QA-4: setup-clone.sh runs it — the step is wired, not merely available.
grep -q 'scripts/install-skill.sh' scripts/setup-clone.sh || fail "QA-4: setup-clone.sh does not run install-skill.sh"

echo "check-skill: GREEN (QA-1 fresh install of all $N, QA-2 current copies untouched, QA-3 stale single-file copy replaced + kept + others added, QA-4 wired into setup-clone, QA-5 symlink + directory refused, QA-6 one installer at a time, QA-7 fleet dir knob + lead beside the core, QA-8 a refused fleet dir installs none, QA-9 one dir for two refused, QA-10 fleet lock, QA-11 core dir named as another skill refused, QA-12 a short checkout refused, QA-13 a spaced destination releases its locks)"
