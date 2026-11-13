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
# Every skill destination here is judged by where it lands (D-181): under a
# temp directory inside a work tree, each would be `vcs` and left, and U-4 went
# red on a correct tree (the panel); straight under another checkout's skills/,
# `clone` (Codex code pass). Anything but `ok` is said by name, never a
# confusing red.
. scripts/skill-dest.sh
mkdir "$WORK/none"; skd_judge "$WORK" "$WORK/none" "$WORK/none"; rmdir "$WORK/none"
[ "$SKD_KIND" = ok ] || fail "the temp directory $WORK is judged $SKD_KIND${SKD_MARK:+ ($SKD_MARK)}, where every fixture skill directory is left; set TMPDIR to a plain directory outside any work tree or checkout"
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
cp scripts/install-skill.sh scripts/skill-dest.sh "$SHORT/scripts/"; cp "$SRC" "$SHORT/skills/buddy/"; cp "$FSRC" "$SHORT/skills/buddy-fleet/"
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
cp scripts/install-skill.sh scripts/skill-dest.sh "$CO/scripts/"; cp -R skills "$CO/skills"
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

# QA-15: a destination directory judged before anything is written in it
# (D-181, #94; scripts/skill-dest.sh, the judge uninstall.sh uses too). The
# checkout is a fresh copy, so its skills/ can be watched.
# tsum <dir>: every entry under it, links by their text, files by checksum.
tsum() { (cd "$1" && find . | LC_ALL=C sort | while IFS= read -r f; do if [ -L "$f" ]; then printf 'link %s -> %s\n' "$f" "$(readlink "$f")"; elif [ -f "$f" ]; then printf '%s %s\n' "$(cksum < "$f")" "$f"; else printf 'dir %s\n' "$f"; fi; done); }
C2="$WORK/co2"; mkdir -p "$C2/scripts"
cp scripts/install-skill.sh scripts/skill-dest.sh "$C2/scripts/"; cp -R skills "$C2/skills"
rm -rf "$C2/skills/model-router/__pycache__"
L="$WORK/l"
BUDDY_SKILL_DIR="$L/buddy" sh "$C2/scripts/install-skill.sh" >/dev/null || fail "QA-15: the control install failed"
# QA-15a: the fleet skill's destination a link to the checkout's own
# skills/buddy-fleet (a live-edit setup) is skipped and named: no lock, temp
# file or manifest lands in the checkout (all three did). The core, stale, is
# refreshed: the positive control that the run installed.
rm -rf "$L/buddy-fleet"; ln -s "$C2/skills/buddy-fleet" "$L/buddy-fleet"
b4=$(tsum "$C2/skills")
printf 'stale core\n' > "$L/buddy/SKILL.md"
out=$(BUDDY_SKILL_DIR="$L/buddy" sh "$C2/scripts/install-skill.sh" 2>&1) || fail "QA-15a: a link to the checkout's own skill must not refuse: $out"
echo "$out" | grep -qxF "install-skill: $L/buddy-fleet is this checkout's own skills/buddy-fleet (however it is reached), so it is current; nothing was written there" || fail "QA-15a: the skip is not named: $out"
[ "$(tsum "$C2/skills")" = "$b4" ] || fail "QA-15a: the install wrote into the checkout's skills/:
$(tsum "$C2/skills")"
cmp -s "$C2/skills/buddy/SKILL.md" "$L/buddy/SKILL.md" || fail "QA-15a: the core was not refreshed beside the skipped skill"
# QA-15b: the skills root itself a link to the checkout's skills/: every
# destination is the checkout's own, by -ef (no link at any of them).
ln -s "$C2/skills" "$WORK/r"
out=$(BUDDY_SKILL_DIR="$WORK/r/buddy" sh "$C2/scripts/install-skill.sh" 2>&1) || fail "QA-15b: a root link to the checkout's skills/ must not refuse: $out"
[ "$(echo "$out" | grep -c "is this checkout's own skills/")" -eq "$N" ] || fail "QA-15b: want $N skips: $out"
[ "$(tsum "$C2/skills")" = "$b4" ] || fail "QA-15b: the install wrote into the checkout's skills/"
# QA-15c: a destination linked anywhere else is LEFT and named, nothing
# written through it, and the run goes on (the panel: a refusal there
# stopped install.sh and setup-clone.sh for a second worktree linked to the
# first); so is the same link spelled with a trailing slash (Codex design
# pass: `link/` made `[ -L ]` look past it). The core is the control.
mkdir "$WORK/plain"; cp -R "$L/buddy-lead" "$WORK/plain/buddy-lead"
rm -rf "$L/buddy-lead"; ln -s "$WORK/plain/buddy-lead" "$L/buddy-lead"
printf 'stale\n' > "$WORK/plain/buddy-lead/SKILL.md"
pb4=$(tsum "$WORK/plain")
printf 'stale core\n' > "$L/buddy/SKILL.md"
out=$(BUDDY_SKILL_DIR="$L/buddy" sh "$C2/scripts/install-skill.sh" 2>&1) || fail "QA-15c: a linked destination must not stop the install: $out"
echo "$out" | grep -qxF "install-skill: left: $L/buddy-lead is a symlink to a directory that is not this checkout's skills/buddy-lead, so nothing is written through it" || fail "QA-15c: the link is not named: $out"
[ "$(tsum "$WORK/plain")" = "$pb4" ] || fail "QA-15c: the install wrote through the link"
cmp -s "$C2/skills/buddy/SKILL.md" "$L/buddy/SKILL.md" || fail "QA-15c: the core was not refreshed beside the linked skill"
mv "$L/buddy-fleet" "$WORK/fleet-own"; mkdir "$WORK/fl"; ln -s "$WORK/fl" "$L/fleetlink"
out=$(BUDDY_SKILL_DIR="$L/buddy" BUDDY_FLEET_SKILL_DIR="$L/fleetlink/" sh "$C2/scripts/install-skill.sh" 2>&1) || fail "QA-15c: a linked destination spelled with a trailing slash must not stop the install: $out"
echo "$out" | grep -qxF "install-skill: left: $L/fleetlink is a symlink to a directory that is not this checkout's skills/buddy-fleet, so nothing is written through it" || fail "QA-15c: the trailing-slash spelling was not judged as the link: $out"
[ -z "$(ls -A "$WORK/fl")" ] || fail "QA-15c: the install wrote through the trailing-slash link"
# QA-15d: a destination inside a version-control work tree that is no buddy
# checkout (a dotfiles repo, here through a link above it) installs, but a
# file the checkout no longer ships is moved to .bak there, never deleted
# (Codex design pass: that removal kept no copy). QA-14's removal is the
# control.
mkrepo "$WORK/dots"; mkdir "$WORK/dots/skills"; ln -s "$WORK/dots/skills" "$WORK/v"
C3="$WORK/co3"; mkdir -p "$C3/scripts"
cp scripts/install-skill.sh scripts/skill-dest.sh "$C3/scripts/"; cp -R skills "$C3/skills"
rm -rf "$C3/skills/model-router/__pycache__"
BUDDY_SKILL_DIR="$WORK/v/buddy" sh "$C3/scripts/install-skill.sh" >/dev/null || fail "QA-15d: an install into a work tree failed"
cmp -s "$C3/skills/buddy/SKILL.md" "$WORK/dots/skills/buddy/SKILL.md" || fail "QA-15d: the install into a work tree installed nothing"
rsum=$(cksum < "$WORK/dots/skills/buddy-fleet/restack.md")
rm "$C3/skills/buddy-fleet/restack.md"
out=$(BUDDY_SKILL_DIR="$WORK/v/buddy" sh "$C3/scripts/install-skill.sh" 2>&1) || fail "QA-15d: the refresh into a work tree failed: $out"
echo "$out" | grep -qxF "install-skill: moved $WORK/v/buddy-fleet/restack.md to $WORK/v/buddy-fleet/restack.md.bak (this checkout no longer ships it, and its directory is in a version-control work tree, where nothing is deleted)" || fail "QA-15d: the move is not named: $out"
[ ! -e "$WORK/dots/skills/buddy-fleet/restack.md" ] && [ "$(cksum < "$WORK/dots/skills/buddy-fleet/restack.md.bak")" = "$rsum" ] || fail "QA-15d: the dropped file was not kept as .bak"
# QA-15f: the skills root a link to ANOTHER buddy checkout's skills/ (a
# second clone or worktree, marked by its scripts/install-skill.sh): every
# destination is left, nothing written there, the run goes on (the panel:
# an install from this clone overwrote the other's tracked files), and a
# skill the other checkout lacks is not made there either.
A="$WORK/clone-a"; mkrepo "$A"; mkdir -p "$A/scripts"; : > "$A/scripts/install-skill.sh"
cp -R skills "$A/skills"; rm -rf "$A/skills/model-router"
ln -s "$A/skills" "$WORK/hs"
AP=$(cd "$A" && env pwd -P)
ab4=$(tsum "$A")
out=$(BUDDY_SKILL_DIR="$WORK/hs/buddy" sh "$C2/scripts/install-skill.sh" 2>&1) || fail "QA-15f: another checkout's skills/ must not stop the install: $out"
echo "$out" | grep -qxF "install-skill: left: $WORK/hs/buddy is another checkout's skills/buddy ($AP), so nothing is written there" || fail "QA-15f: the other checkout's skill is not named: $out"
echo "$out" | grep -qxF "install-skill: left: $WORK/hs/model-router would be made in another checkout's skills/ ($AP), so nothing is written there" || fail "QA-15f: the skill it lacks is not named: $out"
[ "$(tsum "$A")" = "$ab4" ] || fail "QA-15f: the install wrote into the other checkout:
$(tsum "$A")"
# QA-15e: a destination inside this checkout that is not the skill's own
# (the core's knob naming the checkout's skills/buddy-fleet) refuses before
# anything is written (the panel: the core's SKILL.md replaced the
# checkout's tracked fleet SKILL.md); so does one made anywhere else in it.
b4=$(tsum "$C2")
if out=$(BUDDY_SKILL_DIR="$C2/skills/buddy-fleet" BUDDY_FLEET_SKILL_DIR="$WORK/f15e" sh "$C2/scripts/install-skill.sh" 2>&1); then fail "QA-15e: a destination inside the checkout must refuse: $out"; fi
echo "$out" | grep -qF "install-skill: $C2/skills/buddy-fleet is inside this checkout and is not its skills/buddy; refusing to write through it (nothing was installed)." || fail "QA-15e: refused, but not naming the checkout: $out"
if out=$(BUDDY_SKILL_DIR="$C2/inst/buddy" sh "$C2/scripts/install-skill.sh" 2>&1); then fail "QA-15e: a destination to be made inside the checkout must refuse: $out"; fi
# Whichever destination the glob lists first is refused (buddy-fleet before
# buddy in C collation: '-' sorts before '/').
echo "$out" | grep -q "^install-skill: $C2/inst/[a-z-]* is inside this checkout and is not its skills/[a-z-]*; refusing" || fail "QA-15e: the absent destination in the checkout is not named: $out"
[ "$(tsum "$C2")" = "$b4" ] && [ ! -e "$WORK/f15e" ] || fail "QA-15e: the refused install wrote something:
$(tsum "$C2")"
# QA-15g: a knob with a . or .. component anywhere refuses before any
# directory is made (the panel: `missing/..` made directories, then refused).
# Above the last name too (Codex code pass): `s94/missing/../buddy` hid
# s94/buddy's symlinked SKILL.md.bak from every check until mkdir -p made
# `missing`, and the backup was then written through onto the victim.
mkdir -p "$WORK/s94/buddy"; printf 'victim\n' > "$WORK/s94/victim"
printf 'changed\n' > "$WORK/s94/buddy/SKILL.md"; ln -s "$WORK/s94/victim" "$WORK/s94/buddy/SKILL.md.bak"
for k in "$WORK/g15/missing/.." "$WORK/g15/buddy/." "$WORK/s94/missing/../buddy" "$WORK/s94/./buddy"; do
	if out=$(BUDDY_SKILL_DIR="$k" BUDDY_FLEET_SKILL_DIR="$WORK/g15f" sh "$C2/scripts/install-skill.sh" 2>&1); then fail "QA-15g: $k must refuse: $out"; fi
	echo "$out" | grep -qF "install-skill: a skill destination has a . or .. component ($k)" || fail "QA-15g: refused, but not for the dot: $out"
	[ ! -e "$WORK/g15" ] && [ ! -e "$WORK/g15f" ] && [ ! -e "$WORK/s94/missing" ] || fail "QA-15g: $k made a directory before refusing"
	[ "$(cat "$WORK/s94/victim")" = victim ] || fail "QA-15g: $k wrote through the linked .bak onto the victim"
done
# QA-15l: `/` as a destination refuses before anything is written (Codex
# re-verify: it was `ok`); the fleet knob beside it is made by nothing.
if out=$(BUDDY_SKILL_DIR=/ BUDDY_FLEET_SKILL_DIR="$WORK/f15l" sh "$C2/scripts/install-skill.sh" 2>&1); then fail "QA-15l: / must refuse: $out"; fi
echo "$out" | grep -qF "install-skill: / is / or has a . or .. component, which can step past a link; refusing to write through it (nothing was installed)." || fail "QA-15l: refused, but not for /: $out"
[ ! -e "$WORK/f15l" ] || fail "QA-15l: the refused install made the fleet destination"
# QA-15h: another buddy checkout with NO skills/ yet (Codex code pass): the
# destination its skills/<n> would be is left before anything is made, and
# the run goes on; the core beside it is the control.
B="$WORK/clone-b"; mkdir -p "$B/scripts"; : > "$B/scripts/install-skill.sh"
BP=$(cd "$B" && env pwd -P)
printf 'stale core\n' > "$L/buddy/SKILL.md"
out=$(BUDDY_SKILL_DIR="$L/buddy" BUDDY_FLEET_SKILL_DIR="$B/skills/buddy-fleet" sh "$C2/scripts/install-skill.sh" 2>&1) || fail "QA-15h: a clone with no skills/ must not stop the install: $out"
echo "$out" | grep -qxF "install-skill: left: $B/skills/buddy-fleet would be made in another checkout's skills/ ($BP), so nothing is written there" || fail "QA-15h: the clone's absent skills/ is not named: $out"
[ ! -e "$B/skills" ] || fail "QA-15h: the install made skills/ in the other checkout"
cmp -s "$C2/skills/buddy/SKILL.md" "$L/buddy/SKILL.md" || fail "QA-15h: the core was not refreshed beside the left skill"
# The same spelled with `//` (Fable cross-check: the empty name before it
# was no `skills`, so skills/buddy-fleet was made there, then refused).
out=$(BUDDY_SKILL_DIR="$L/buddy" BUDDY_FLEET_SKILL_DIR="$B/skills//buddy-fleet" sh "$C2/scripts/install-skill.sh" 2>&1) || fail "QA-15h: the // spelling must not stop the install: $out"
echo "$out" | grep -qxF "install-skill: left: $B/skills/buddy-fleet would be made in another checkout's skills/ ($BP), so nothing is written there" || fail "QA-15h: the // spelling is not named as the clone: $out"
[ ! -e "$B/skills" ] || fail "QA-15h: the // spelling made skills/ in the other checkout"
# QA-15j: a destination to be made through a link to nothing (a removed
# worktree's skills/) is left and named, and the run goes on (Fable
# cross-check: mkdir -p failed under set -e and stopped install.sh); the
# core beside it is the control.
ln -s "$WORK/gone-tree/skills" "$WORK/dl"
printf 'stale core\n' > "$L/buddy/SKILL.md"
out=$(BUDDY_SKILL_DIR="$L/buddy" BUDDY_FLEET_SKILL_DIR="$WORK/dl/buddy-fleet" sh "$C2/scripts/install-skill.sh" 2>&1) || fail "QA-15j: a link to nothing above a destination must not stop the install: $out"
echo "$out" | grep -qxF "install-skill: left: $WORK/dl/buddy-fleet would be made through $WORK/dl, a symlink to nothing, so nothing is made there" || fail "QA-15j: the dangling link is not named: $out"
cmp -s "$C2/skills/buddy/SKILL.md" "$L/buddy/SKILL.md" || fail "QA-15j: the core was not refreshed beside the left skill"
# QA-15k: the checkout spelled in another case, on a case-insensitive volume
# (the macOS default; skipped, said, where the volume is case-sensitive):
# still `checkout`, existing or absent (Fable cross-check: the builtin
# pwd -P of macOS /bin/sh kept the typed case, so the prefix test missed it,
# the core's knob on the checkout's skills/buddy-fleet installed, and an
# absent one was made inside the checkout).
C2U="$WORK/CO2"
if [ "$C2U" -ef "$C2" ]; then
	b4=$(tsum "$C2")
	if out=$(BUDDY_SKILL_DIR="$C2U/skills/buddy-fleet" BUDDY_FLEET_SKILL_DIR="$WORK/f15k" sh "$C2/scripts/install-skill.sh" 2>&1); then fail "QA-15k: the checkout in another case must refuse: $out"; fi
	echo "$out" | grep -qF "install-skill: $C2U/skills/buddy-fleet is inside this checkout and is not its skills/buddy; refusing to write through it (nothing was installed)." || fail "QA-15k: refused, but not naming the checkout: $out"
	if out=$(BUDDY_SKILL_DIR="$C2U/inst/buddy" sh "$C2/scripts/install-skill.sh" 2>&1); then fail "QA-15k: an absent destination in the checkout in another case must refuse: $out"; fi
	echo "$out" | grep -q "^install-skill: $C2U/inst/[a-z-]* is inside this checkout and is not its skills/[a-z-]*; refusing" || fail "QA-15k: the absent destination is not named: $out"
	[ "$(tsum "$C2")" = "$b4" ] && [ ! -e "$WORK/f15k" ] || fail "QA-15k: the refused install wrote something:
$(tsum "$C2")"
	qa15k="the checkout in another case refused"
else
	echo "check-skill: QA-15k NOT RUN: $WORK is on a case-sensitive volume"
	qa15k="QA-15k not run (case-sensitive volume)"
fi
# QA-15i: absent destinations under a link to a directory whose name ends in
# a newline refuse before any directory is made there (Codex code pass: the
# first mkdir -p made a child in "foreign<LF>", and only then refused).
NLD="$WORK/foreign
"
mkdir "$NLD"; ln -s "$NLD" "$WORK/up"
if out=$(BUDDY_SKILL_DIR="$WORK/up/buddy" sh "$C2/scripts/install-skill.sh" 2>&1); then fail "QA-15i: a destination to be made under a newline must refuse: $out"; fi
echo "$out" | grep -q "^install-skill: $WORK/up/[a-z-]* has a physical path holding a newline; refusing" || fail "QA-15i: refused, but not for the newline: $out"
[ -z "$(ls -A "$NLD")" ] || fail "QA-15i: a directory was made under the newline before the refusal"

# QA-4: setup-clone.sh runs it — the step is wired, not merely available.
grep -q 'scripts/install-skill.sh' scripts/setup-clone.sh || fail "QA-4: setup-clone.sh does not run install-skill.sh"

echo "check-skill: GREEN (QA-1 fresh install of all $N, QA-2 current copies untouched, QA-3 stale single-file copy replaced + kept + others added, QA-4 wired into setup-clone, QA-5 symlink + directory refused, QA-6 one installer at a time, QA-7 fleet dir knob + lead beside the core, QA-8 a refused fleet dir installs none, QA-9 one dir for two refused, QA-10 fleet lock, QA-11 core dir named as another skill refused, QA-12 a short checkout refused, QA-13 a spaced destination releases its locks, QA-14 every file of a skill with a manifest: a dropped file removed, an edited one kept, a foreign one left, a bad manifest line and a subdirectory refused, a __pycache__ directory skipped, QA-15 a destination that is the checkout's own skipped, a linked one or another checkout's left, one inside this checkout, a dot knob anywhere, / and a newline above an absent one refused, a clone with no skills/ yet or a link to nothing above left, $qa15k, a stale file in a work tree kept as .bak)"
