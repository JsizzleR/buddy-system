#!/bin/sh
# install-skill.sh — copy this repo's buddy skills to where Claude Code loads them.
#
# THE SKILLS SHIP IN THE REPO (skills/<name>/SKILL.md) and are LOADED from the
# user's skills directory (~/.claude/skills/<name>/SKILL.md), because they are
# for sessions working in OTHER repositories that use buddy, not only this one.
# A copy step between the two is a copy that goes stale: measured 2026-09-26,
# the installed copy on the developer's own machine was a release behind the
# repo, teaching none of the verbs the release had just added. So setup-clone.sh
# runs this, it is safe to re-run after every pull, and a hermetic done-check
# (check-skill.sh) holds it to installing, refreshing and backing up.
#
# EVERY skills/*/SKILL.md IS INSTALLED, together or not at all (D-114, D-121).
# `buddy` is the core every session loads, `buddy-fleet` only a coordinator
# loads, `buddy-lead` a first-time user's helper. One file had grown to 73 KB
# and was loaded in 115 of 156 sessions, almost all of them lanes that needed
# none of the fleet recipe; the split is why there are several. All are
# installed on every run: an install that refreshed the core and then refused
# another would leave a machine whose core points at a skill it does not have,
# or at a stale one. So every destination is checked before any is written,
# and a refusal names the one refused. (A copy that fails AFTER the checks, a
# full disk, still exits nonzero, and may leave the core refreshed: the set is
# checked together, not transactional.) The destinations must be pairwise
# different directories (Codex code pass: one directory for both installed the
# core, overwrote it with the fleet skill, and overwrote the core's backup),
# and each is locked. The list is the repo's `skills/` directory, not a count
# written here (a third skill would otherwise be a third edit of this script),
# with a floor of the three that ship today: fewer means a checkout missing a
# skill directory, refused rather than installed short.
#
# EVERY FILE OF A SKILL, NOT ONLY ITS SKILL.md (D-171). The fleet skill's
# read-once sections (restacking, handing off a coordinator, a fleet of fleets,
# the model pick, non-code deliverables, Codex lanes) moved into reference
# files beside its SKILL.md, which names each and says when to read it; a
# SKILL.md installed without them points at files the machine does not have.
# So a skill is every regular file in skills/<name>/ (a dotfile, a Finder
# .DS_Store, is skipped), each refused before anything is written if its name
# is not a plain [A-Za-z0-9._-] word or its destination is a symlink or not a
# regular file, and anything else in the directory (a subdirectory) is
# refused too, never silently left out. The reference files go BEFORE their
# SKILL.md, so a refreshed SKILL.md never names a file still absent, and a
# copy that fails between them leaves the old SKILL.md over a superset.
#
# ONE NAMED EXCEPTION: a real DIRECTORY named exactly __pycache__ is skipped,
# never installed and never refused (D-178). Python writes one beside any .py
# that something IMPORTS (a developer's `import router`, a test runner, an
# editor), and the model router ships router.py: one stray cache refused
# every skill, the core included, so a pull's install left every skill stale
# (#92). Making nothing write it (-B, PYTHONDONTWRITEBYTECODE) binds only the
# runs this repo makes, never a user's own import, so skipping is the fix
# that holds. It is bytecode Python rebuilds, so nothing a skill needs is
# left out. A symlink named __pycache__, and every other subdirectory, still
# refuses the whole install, as above; a regular FILE of that name is a file
# like any other (Python never writes one), installed and listed.
#
# NO STALE COPY OF A FILE THE REPO DROPPED. Each destination keeps a manifest,
# .install-skill.files, one `<cksum> <bytes> <name>` line per file this
# script installed there. A name in it that the repo no longer ships is
# removed when its bytes still match the line (the copy we wrote), and kept as
# <name>.bak when they do not (an edit is never lost silently), once the new
# SKILL.md is in. Nothing the manifest does not name is touched: a file you
# put there yourself is yours. A manifest line that is not that shape (a name
# with a slash would reach outside the directory) refuses the whole install.
# The manifest is written last, by rename: a run stopped before it leaves the
# old one, whose stale names the next run still removes.
#
# What it does to an existing copy: nothing when it is identical; when it
# differs, the old one is kept as SKILL.md.bak (a local edit is never lost
# silently) and the repo's copy replaces it. It never touches anything else in
# the skills directory. An old single-file install (the whole 73 KB skill at
# ~/.claude/skills/buddy) is such a copy: replaced, kept as .bak, and the other
# skills installed beside it.
#
# REFUSED, not guessed (Codex code pass): a SKILL.md (or any file installed) that is a symlink (the
# refresh would overwrite whatever it points at) or anything but a regular file
# (cp into a directory named SKILL.md "succeeds" and installs nothing). And ONE
# installer at a time, by an mkdir lock: two concurrent refreshes each backed
# up what they saw, the second backing up the first's fresh copy over the local
# edit the first had saved.
#
# KNOBS: BUDDY_SKILL_DIR        where the core goes (default ~/.claude/skills/buddy).
#        BUDDY_FLEET_SKILL_DIR  where the fleet skill goes (default: buddy-fleet
#                               beside BUDDY_SKILL_DIR). Every other skill goes
#                               beside BUDDY_SKILL_DIR under its own name. The
#                               done-check points them at a temp dir.
set -eu
CDPATH= cd -- "$(dirname -- "$0")/.."
ROOT=$(pwd)
DEST_DIR=${BUDDY_SKILL_DIR:-$HOME/.claude/skills/buddy}
FLEET_DIR=${BUDDY_FLEET_SKILL_DIR:-$(dirname -- "$DEST_DIR")/buddy-fleet}

# dest <name> — the directory skill <name> is installed into.
dest() {
	case $1 in
	buddy) printf '%s\n' "$DEST_DIR" ;;
	buddy-fleet) printf '%s\n' "$FLEET_DIR" ;;
	*) printf '%s/%s\n' "$(dirname -- "$DEST_DIR")" "$1" ;;
	esac
}

NAMES=""
for s in "$ROOT"/skills/*/SKILL.md; do
	[ -f "$s" ] || continue
	n=$(basename -- "$(dirname -- "$s")")
	NAMES="$NAMES $n"
done
NAMES=${NAMES# }
count=$(set -- $NAMES; echo $#)
if [ "$count" -lt 3 ]; then
	echo "install-skill: found $count skill(s) under $ROOT/skills (${NAMES:-none}); the repo ships at least three (buddy, buddy-fleet, buddy-lead), so this checkout is incomplete; nothing was installed." >&2
	exit 1
fi

MANIFEST=.install-skill.files

# files <name>: the files skill <name> ships, one per line, its reference
# files first and SKILL.md last (the install order; see the header). Refuses
# an entry that is not a regular file or whose name is not a plain word.
files() {
	for f in "$ROOT/skills/$1"/*; do
		[ -e "$f" ] || [ -L "$f" ] || continue
		b=$(basename -- "$f")
		# The one exception (D-178; the header): a real __pycache__ directory.
		if [ "$b" = __pycache__ ] && [ -d "$f" ] && [ ! -L "$f" ]; then continue; fi
		case $b in
		*[!A-Za-z0-9._-]*)
			echo "install-skill: $f: a skill file's name must be a plain [A-Za-z0-9._-] word; nothing was installed." >&2
			exit 1
			;;
		esac
		if [ -L "$f" ] || [ ! -f "$f" ]; then
			echo "install-skill: $f is a symlink or not a regular file; a skill is regular files only (nothing was installed)." >&2
			exit 1
		fi
		[ "$b" = SKILL.md ] || printf '%s\n' "$b"
	done
	echo SKILL.md
}

# refuse_through <path>: refuse before anything is written.
refuse_through() {
	if [ -L "$1" ] || { [ -e "$1" ] && [ ! -f "$1" ]; }; then
		echo "install-skill: $1 is a symlink or not a regular file; refusing to write through it (nothing was installed)." >&2
		echo "  Move it aside and re-run." >&2
		exit 1
	fi
}

# sums <file>: its manifest fields, `<cksum> <bytes>`.
sums() { cksum < "$1" | awk '{print $1" "$2}'; }

# check_manifest <name>: refuse a manifest line not of the shape this script
# writes, before anything is written. Called directly, never inside $(...),
# where its exit would end only the subshell.
check_manifest() {
	m="$(dest "$1")/$MANIFEST"
	[ -f "$m" ] || return 0
	if LC_ALL=C grep -vEq '^[0-9]+ [0-9]+ [A-Za-z0-9_-][A-Za-z0-9._-]*$' "$m"; then
		echo "install-skill: $m holds a line this script did not write; move it aside and re-run (nothing was installed)." >&2
		exit 1
	fi
}

# stale <name>: the names skill <name>'s manifest lists that the repo no
# longer ships, one per line (check_manifest has vetted every line).
stale() {
	m="$(dest "$1")/$MANIFEST"
	[ -f "$m" ] || return 0
	want=$(files "$1")
	awk '{print $3}' "$m" | while IFS= read -r f; do
		printf '%s\n' "$want" | grep -qxF -- "$f" || printf '%s\n' "$f"
	done
}

for n in $NAMES; do
	[ -f "$ROOT/skills/$n/SKILL.md" ] || { echo "install-skill: no skill at $ROOT/skills/$n/SKILL.md" >&2; exit 1; }
	d=$(dest "$n")
	files "$n" > /dev/null
	refuse_through "$d/$MANIFEST"
	check_manifest "$n"
	# Each file's .bak too: a changed or dropped file is moved or copied there,
	# and a symlinked .bak would be written through (Codex code pass).
	for f in $(files "$n") $(stale "$n"); do refuse_through "$d/$f"; refuse_through "$d/$f.bak"; done
done

for n in $NAMES; do mkdir -p "$(dest "$n")"; done
# -ef compares device and inode, so an alias of the same directory (a
# symlinked parent, a case-folded spelling on the default macOS volume) is
# caught too, which comparing the spellings would not. Every pair.
for a in $NAMES; do
	for b in $NAMES; do
		[ "$a" \< "$b" ] || continue
		if [ "$(dest "$a")" -ef "$(dest "$b")" ]; then
			echo "install-skill: the destinations of $a and $b name the same directory ($(dest "$a")); each skill needs its own (nothing was installed)." >&2
			exit 1
		fi
	done
done

# A lock in EACH destination: two installs with different core directories
# and one fleet directory would otherwise both write that fleet copy and its
# backup. mkdir never waits, so the order cannot deadlock; when a later one is
# held by another install, the exit releases those taken.
# HELD is one directory per LINE: a destination with a space in it, split on
# whitespace, left its lock behind and every later install refused (Codex
# code pass).
HELD=""
trap 'printf "%s\n" "$HELD" | while IFS= read -r d; do [ -n "$d" ] || continue; rm -f "$d/.install-skill.tmp.$$" "$d/.install-skill.files.$$"; rmdir "$d/.install-skill.lock" 2>/dev/null; done; true' EXIT INT TERM
for n in $NAMES; do
	d=$(dest "$n")
	if ! mkdir "$d/.install-skill.lock" 2>/dev/null; then
		echo "install-skill: another install holds $d/.install-skill.lock; if none is running, remove it and re-run." >&2
		exit 1
	fi
	HELD="$HELD$d
"
done

# install_one <src> <dest dir>: copy to a temp file beside the target and
# rename over it, so a reader never sees half a file.
install_one() {
	src=$1 dest="$2/$(basename -- "$1")" tmp="$2/.install-skill.tmp.$$"
	rm -f "$tmp" # never cp through one left behind (a symlink is followed)
	cp "$src" "$tmp"
	if [ ! -f "$dest" ]; then
		mv "$tmp" "$dest"
		echo "install-skill: installed $dest"
	elif cmp -s "$src" "$dest"; then
		rm -f "$tmp"
		echo "install-skill: $dest is current"
	else
		cp "$dest" "$dest.bak"
		mv "$tmp" "$dest"
		echo "install-skill: updated $dest (the previous copy is at $dest.bak)"
	fi
}
# The stale list is read BEFORE anything is written (the checks above
# refused on it), then each skill: its files, its stale names, its manifest.
for n in $NAMES; do
	d=$(dest "$n")
	gone=$(stale "$n")
	for f in $(files "$n"); do install_one "$ROOT/skills/$n/$f" "$d"; done
	for f in $gone; do
		[ -e "$d/$f" ] || continue
		# On a case-insensitive volume RESTACK.md in the manifest IS the shipped
		# restack.md: removing it would delete the file just installed (Codex
		# code pass). -ef compares inodes, so any alias is caught.
		alias=""
		for g in $(files "$n"); do [ ! "$d/$f" -ef "$d/$g" ] || alias=1; done
		[ -z "$alias" ] || continue
		was=$(awk -v f="$f" '$3 == f {l = $1" "$2} END {print l}' "$d/$MANIFEST")
		if [ "$(sums "$d/$f")" = "$was" ]; then
			rm -f "$d/$f"
			echo "install-skill: removed $d/$f (this checkout no longer ships it)"
		else
			mv "$d/$f" "$d/$f.bak"
			echo "install-skill: moved $d/$f to $d/$f.bak (this checkout no longer ships it, and it differs from the copy installed)"
		fi
	done
	tmp="$d/.install-skill.files.$$"
	rm -f "$tmp"
	: > "$tmp"
	for f in $(files "$n"); do
		printf '%s %s\n' "$(sums "$d/$f")" "$f" >> "$tmp"
	done
	mv "$tmp" "$d/$MANIFEST"
done
