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
# way so a fourth skill is covered the day it lands. Every FILE of a skill
# ships with it (D-171: the fleet skill's reference files), counted the same
# way, and QA-14 holds the manifest that removes a file the repo dropped.
# Every comparison below reads the skill list from skills/*/ too (#92: QA-1
# compared only the three skills it named, so a fifth file of the model
# router installed anywhere, or as anything, passed), with skdest() as the
# installer's dest(); only the cases about one skill's knob name it.
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
# F is every file they ship, the installer's files() enumeration; the floor
# is the control that the fleet skill's reference files are counted.
# A real __pycache__ directory is skipped, as the installer skips it (D-178).
F=0
for f in skills/*/*; do
	[ "${f##*/}" = __pycache__ ] && [ -d "$f" ] && [ ! -L "$f" ] || F=$((F + 1))
done
[ "$F" -ge $((N + 6)) ] || fail "found $F skill files under skills/, want the $N SKILL.md and the fleet skill's six reference files"

# skdest <name>: where the default install puts skill <name> (install-skill.sh's
# dest() with no fleet knob): the core at $DIR, every other skill beside it.
skdest() { if [ "$1" = buddy ]; then printf '%s\n' "$DIR"; else printf '%s\n' "$WORK/skills/$1"; fi; }
# skills_each <check-label>: every shipped file is at its skill's destination,
# byte-identical, with the installer's manifest beside it.
skills_each() {
	for s in skills/*/SKILL.md; do
		n=${s#skills/}; n=${n%/SKILL.md}; d=$(skdest "$n")
		for f in skills/"$n"/*; do
			{ [ "${f##*/}" = __pycache__ ] && [ -d "$f" ] && [ ! -L "$f" ]; } && continue
			cmp -s "$f" "$d/${f##*/}" || fail "$1: $f was not installed, identical, at $d"
		done
		[ -f "$d/.install-skill.files" ] || fail "$1: no manifest beside $d/SKILL.md"
	done
}

# QA-1: a fresh install creates every directory, each with an identical copy:
# the fleet and lead skills beside the core by default, no knob of theirs set.
out=$(BUDDY_SKILL_DIR="$DIR" sh scripts/install-skill.sh) || fail "QA-1: install failed"
[ "$(echo "$out" | grep -c '^install-skill: installed ')" -eq "$F" ] || fail "QA-1: want $F installed lines: $out"
cmp -s "$SRC" "$DIR/SKILL.md" || fail "QA-1: the installed core differs from the repo's"
skills_each QA-1
# The reference files land BEFORE their SKILL.md: a SKILL.md refreshed first
# would name files a stop could leave absent.
[ "$(echo "$out" | grep '/buddy-fleet/' | tail -1)" = "install-skill: installed $FDIR/SKILL.md" ] || fail "QA-1: the fleet SKILL.md was not the fleet skill's last file: $out"
cmp -s "$FSRC" "$FDIR/SKILL.md" || fail "QA-1: the fleet skill was not installed beside the core"
cmp -s "$LSRC" "$LDIR/SKILL.md" || fail "QA-1: the lead skill was not installed beside the core"

# QA-2: a re-run over identical copies says current N times and writes no backup.
out=$(BUDDY_SKILL_DIR="$DIR" sh scripts/install-skill.sh) || fail "QA-2: re-run failed"
[ "$(echo "$out" | grep -c ' is current$')" -eq "$F" ] || fail "QA-2: $out"
for s in skills/*/SKILL.md; do
	n=${s#skills/}; n=${n%/SKILL.md}
	! ls "$(skdest "$n")"/*.bak >/dev/null 2>&1 || fail "QA-2: a current copy of $n must not be backed up: $(ls "$(skdest "$n")"/*.bak)"
done

# QA-3: a stale copy is replaced, and the stale one is kept as .bak. The stale
# core is the shape every machine had before the split: one whole skill at
# skills/buddy and nothing beside it. It is refreshed and the others added.
printf 'an older skill\n' > "$DIR/SKILL.md"
for s in skills/*/SKILL.md; do
	n=${s#skills/}; n=${n%/SKILL.md}
	[ "$n" = buddy ] || rm -rf "$(skdest "$n")"
done
out=$(BUDDY_SKILL_DIR="$DIR" sh scripts/install-skill.sh) || fail "QA-3: refresh failed"
echo "$out" | grep -q '^install-skill: updated .*/buddy/SKILL.md ' || fail "QA-3: $out"
for s in skills/*/SKILL.md; do
	n=${s#skills/}; n=${n%/SKILL.md}
	[ "$n" = buddy ] || echo "$out" | grep -qxF "install-skill: installed $(skdest "$n")/SKILL.md" || fail "QA-3: the $n skill was not added: $out"
done
skills_each QA-3
cmp -s "$SRC" "$DIR/SKILL.md" || fail "QA-3: the stale copy was not replaced"
[ "$(cat "$DIR/SKILL.md.bak")" = "an older skill" ] || fail "QA-3: the previous copy was not kept"

# QA-7: BUDDY_FLEET_SKILL_DIR places the fleet skill anywhere, and nothing is
# written beside the core then; the lead skill still goes beside the core.
ODIR="$WORK/other/core"; OFDIR="$WORK/elsewhere/fleet"
BUDDY_SKILL_DIR="$ODIR" BUDDY_FLEET_SKILL_DIR="$OFDIR" sh scripts/install-skill.sh >/dev/null || fail "QA-7: install failed"
cmp -s "$FSRC" "$OFDIR/SKILL.md" || fail "QA-7: the fleet skill is not where the knob put it"
for f in skills/buddy-fleet/*; do
	cmp -s "$f" "$OFDIR/${f##*/}" || fail "QA-7: $f is not where the knob put the fleet skill (Codex code pass: only its SKILL.md was compared)"
done
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

# QA-14: a file the checkout no longer ships is removed from where it was
# installed, by the manifest; an edited one is kept as .bak; a file nobody
# installed is left; a manifest line that is not the installer's (a name with
# a slash) and a subdirectory in a skill refuse the whole install. The
# checkout is a copy, so files can be dropped from it. The control for each
# removal is the file the same run keeps installed.
CO="$WORK/co"; mkdir -p "$CO/scripts"
cp scripts/install-skill.sh "$CO/scripts/"; cp -R skills "$CO/skills"
MD="$WORK/m/buddy"; MF="$WORK/m/buddy-fleet"
BUDDY_SKILL_DIR="$MD" sh "$CO/scripts/install-skill.sh" >/dev/null || fail "QA-14: the copy did not install"
grep -qx '[0-9]* [0-9]* restack.md' "$MF/.install-skill.files" || fail "QA-14: the manifest does not list restack.md"
printf 'my own notes\n' > "$MF/mine.md"
rm "$CO/skills/buddy-fleet/restack.md" "$CO/skills/buddy-fleet/handoff.md"
printf 'my edit\n' >> "$MF/handoff.md"
out=$(BUDDY_SKILL_DIR="$MD" sh "$CO/scripts/install-skill.sh") || fail "QA-14: the refresh failed: $out"
[ ! -e "$MF/restack.md" ] || fail "QA-14: a file the checkout dropped was left installed"
echo "$out" | grep -qxF "install-skill: removed $MF/restack.md (this checkout no longer ships it)" || fail "QA-14: the removal is not reported: $out"
[ ! -e "$MF/handoff.md" ] && grep -q 'my edit' "$MF/handoff.md.bak" || fail "QA-14: an edited dropped file was not kept as .bak"
[ "$(cat "$MF/mine.md")" = "my own notes" ] || fail "QA-14: a file the installer never wrote was touched"
cmp -s "$CO/skills/buddy-fleet/model-pick.md" "$MF/model-pick.md" || fail "QA-14: a file still shipped went with the dropped ones"
! grep -q 'restack.md' "$MF/.install-skill.files" || fail "QA-14: the manifest still lists the dropped file"
printf '1 2 ../buddy/SKILL.md\n' >> "$MF/.install-skill.files"
printf 'stale core\n' > "$MD/SKILL.md"
if out=$(BUDDY_SKILL_DIR="$MD" sh "$CO/scripts/install-skill.sh" 2>&1); then fail "QA-14: a manifest line naming a path outside its directory must refuse"; fi
echo "$out" | grep -q 'holds a line this script did not write' || fail "QA-14: refused, but not for the manifest: $out"
[ "$(cat "$MD/SKILL.md")" = "stale core" ] || fail "QA-14: the refused install refreshed the core"
sed '$d' "$MF/.install-skill.files" > "$WORK/mf" && cat "$WORK/mf" > "$MF/.install-skill.files"
# A dropped file whose .bak is a symlink refuses the install before anything is
# written (the move would write through it). The control: the .bak above was
# a plain path, and the move went there.
cp "$CO/skills/buddy-fleet/not-code.md" "$WORK/not-code.md"
rm "$CO/skills/buddy-fleet/not-code.md"; printf 'edited\n' >> "$MF/not-code.md"
printf 'outside\n' > "$WORK/outside"; ln -s "$WORK/outside" "$MF/not-code.md.bak"
if out=$(BUDDY_SKILL_DIR="$MD" sh "$CO/scripts/install-skill.sh" 2>&1); then fail "QA-14: a symlinked .bak must refuse"; fi
echo "$out" | grep -q 'not-code.md.bak is a symlink or not a regular file' || fail "QA-14: refused, but not naming the .bak: $out"
[ "$(cat "$WORK/outside")" = outside ] && [ "$(cat "$MD/SKILL.md")" = "stale core" ] || fail "QA-14: the refused install wrote something"
rm "$MF/not-code.md.bak"; cp "$WORK/not-code.md" "$CO/skills/buddy-fleet/"
# On a case-insensitive volume (macOS's default) a manifest line spelling a
# shipped file in another case names that same file: it must survive. Where
# the volume tells the spellings apart the case cannot arise, and is skipped.
if [ "$MF/model-pick.md" -ef "$MF/MODEL-PICK.md" ]; then
	sed 's/ model-pick\.md$/ MODEL-PICK.md/' "$MF/.install-skill.files" > "$WORK/mf" && cat "$WORK/mf" > "$MF/.install-skill.files"
	grep -q ' MODEL-PICK.md$' "$MF/.install-skill.files" || fail "QA-14: the case-alias fixture did not plant its line"
	BUDDY_SKILL_DIR="$MD" sh "$CO/scripts/install-skill.sh" >/dev/null || fail "QA-14: the case-alias install failed"
	cmp -s "$CO/skills/buddy-fleet/model-pick.md" "$MF/model-pick.md" || fail "QA-14: a manifest line in another case deleted a shipped file"
	printf 'stale core\n' > "$MD/SKILL.md"
fi
# A __pycache__ DIRECTORY, as Python writes one beside an imported .py, is
# skipped: the install goes through, installs nothing from it and lists none
# of it (D-178; before, it refused every skill, #92). The subdirectory and the
# symlink named __pycache__ just below are the control that the refusal of
# anything else is still armed.
mkdir -p "$CO/skills/model-router/__pycache__" # -p: a checkout that has one copied it (Codex code pass)
printf 'bytecode\n' > "$CO/skills/model-router/__pycache__/router.cpython-313.pyc"
out=$(BUDDY_SKILL_DIR="$MD" sh "$CO/scripts/install-skill.sh" 2>&1) || fail "QA-14: a __pycache__ directory in a skill must not refuse the install: $out"
cmp -s "$CO/skills/buddy/SKILL.md" "$MD/SKILL.md" || fail "QA-14: the install beside a __pycache__ did not refresh the core"
[ ! -e "$WORK/m/model-router/__pycache__" ] || fail "QA-14: the __pycache__ directory was installed"
! grep -q pycache "$WORK/m/model-router/.install-skill.files" || fail "QA-14: the manifest lists the __pycache__"
echo "$out" | grep -q pycache && fail "QA-14: the install named the __pycache__: $out"
rm -r "$CO/skills/model-router/__pycache__"
printf 'stale core\n' > "$MD/SKILL.md"
ln -s "$WORK" "$CO/skills/model-router/__pycache__"
if out=$(BUDDY_SKILL_DIR="$MD" sh "$CO/scripts/install-skill.sh" 2>&1); then fail "QA-14: a symlink named __pycache__ must refuse"; fi
echo "$out" | grep -q '__pycache__ is a symlink or not a regular file' || fail "QA-14: refused, but not naming the __pycache__ symlink: $out"
[ "$(cat "$MD/SKILL.md")" = "stale core" ] || fail "QA-14: the refused install refreshed the core"
rm "$CO/skills/model-router/__pycache__"
mkdir "$CO/skills/buddy-fleet/extra"
if out=$(BUDDY_SKILL_DIR="$MD" sh "$CO/scripts/install-skill.sh" 2>&1); then fail "QA-14: a subdirectory in a skill must refuse"; fi
echo "$out" | grep -q 'extra is a symlink or not a regular file' || fail "QA-14: refused, but not naming the subdirectory: $out"
[ "$(cat "$MD/SKILL.md")" = "stale core" ] || fail "QA-14: the refused install refreshed the core"
rmdir "$CO/skills/buddy-fleet/extra"
BUDDY_SKILL_DIR="$MD" sh "$CO/scripts/install-skill.sh" >/dev/null || fail "QA-14: with the manifest line and the subdirectory gone, the install still refuses"
cmp -s "$CO/skills/buddy/SKILL.md" "$MD/SKILL.md" || fail "QA-14: the control install did not refresh the core"

# QA-4: setup-clone.sh runs it — the step is wired, not merely available.
grep -q 'scripts/install-skill.sh' scripts/setup-clone.sh || fail "QA-4: setup-clone.sh does not run install-skill.sh"

echo "check-skill: GREEN (QA-1 fresh install of all $N, QA-2 current copies untouched, QA-3 stale single-file copy replaced + kept + others added, QA-4 wired into setup-clone, QA-5 symlink + directory refused, QA-6 one installer at a time, QA-7 fleet dir knob + lead beside the core, QA-8 a refused fleet dir installs none, QA-9 one dir for two refused, QA-10 fleet lock, QA-11 core dir named as another skill refused, QA-12 a short checkout refused, QA-13 a spaced destination releases its locks, QA-14 every file of a skill with a manifest: a dropped file removed, an edited one kept, a foreign one left, a bad manifest line and a subdirectory refused, a __pycache__ directory skipped)"
