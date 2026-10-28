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
# TWO skills (D-114): `buddy`, the core every session loads, and `buddy-fleet`,
# which only a coordinator loads. One file had grown to 73 KB and was loaded in
# 115 of 156 sessions, almost all of them lanes that needed none of the fleet
# recipe. Both are installed on every run: an install that refreshed the core
# and then refused the fleet skill would leave a machine whose core points
# coordinators at a skill it does not have, or at a stale one. So both
# destinations are checked before either is written, and a refusal names the
# one refused. (A copy that fails AFTER the checks, a full disk, still exits
# nonzero, and may leave the core refreshed: the pair is checked together, not
# transactional.) The two must be different directories (Codex code pass:
# one directory for both installed the core, overwrote it with the fleet
# skill, and overwrote the core's backup), and each is locked.
#
# What it does to an existing copy: nothing when it is identical; when it
# differs, the old one is kept as SKILL.md.bak (a local edit is never lost
# silently) and the repo's copy replaces it. It never touches anything else in
# the skills directory. An old single-file install (the whole 73 KB skill at
# ~/.claude/skills/buddy) is such a copy: replaced, kept as .bak, and the fleet
# skill installed beside it.
#
# REFUSED, not guessed (Codex code pass): a SKILL.md that is a symlink (the
# refresh would overwrite whatever it points at) or anything but a regular file
# (cp into a directory named SKILL.md "succeeds" and installs nothing). And ONE
# installer at a time, by an mkdir lock: two concurrent refreshes each backed
# up what they saw, the second backing up the first's fresh copy over the local
# edit the first had saved.
#
# KNOBS: BUDDY_SKILL_DIR        where the core goes (default ~/.claude/skills/buddy).
#        BUDDY_FLEET_SKILL_DIR  where the fleet skill goes (default: buddy-fleet
#                               beside BUDDY_SKILL_DIR). The done-check points
#                               both at a temp dir.
set -eu
CDPATH= cd -- "$(dirname -- "$0")/.."
ROOT=$(pwd)
DEST_DIR=${BUDDY_SKILL_DIR:-$HOME/.claude/skills/buddy}
FLEET_DIR=${BUDDY_FLEET_SKILL_DIR:-$(dirname -- "$DEST_DIR")/buddy-fleet}

# check <src> <dest dir>: refuse before anything is written.
check() {
	if [ ! -f "$1" ]; then
		echo "install-skill: no skill at $1" >&2
		exit 1
	fi
	if [ -L "$2/SKILL.md" ] || { [ -e "$2/SKILL.md" ] && [ ! -f "$2/SKILL.md" ]; }; then
		echo "install-skill: $2/SKILL.md is a symlink or not a regular file; refusing to write through it (nothing was installed)." >&2
		echo "  Move it aside and re-run." >&2
		exit 1
	fi
}
check "$ROOT/skills/buddy/SKILL.md" "$DEST_DIR"
check "$ROOT/skills/buddy-fleet/SKILL.md" "$FLEET_DIR"

mkdir -p "$DEST_DIR" "$FLEET_DIR"
# -ef compares device and inode, so an alias of the same directory (a
# symlinked parent, a case-folded spelling on the default macOS volume) is
# caught too, which comparing the spellings would not.
if [ "$DEST_DIR" -ef "$FLEET_DIR" ]; then
	echo "install-skill: BUDDY_SKILL_DIR and BUDDY_FLEET_SKILL_DIR name the same directory ($DEST_DIR); the two skills need two (nothing was installed)." >&2
	exit 1
fi

# A lock in EACH destination: two installs with different core directories
# and one fleet directory would otherwise both write that fleet copy and its
# backup. mkdir never waits, so the order cannot deadlock; when the second is
# held by another install, the exit releases the first.
HELD=0
trap 'rm -f "$DEST_DIR/.SKILL.md.$$" "$FLEET_DIR/.SKILL.md.$$"
	[ "$HELD" -ge 1 ] && rmdir "$DEST_DIR/.install-skill.lock" 2>/dev/null
	[ "$HELD" -ge 2 ] && rmdir "$FLEET_DIR/.install-skill.lock" 2>/dev/null; true' EXIT INT TERM
for d in "$DEST_DIR" "$FLEET_DIR"; do
	if ! mkdir "$d/.install-skill.lock" 2>/dev/null; then
		echo "install-skill: another install holds $d/.install-skill.lock; if none is running, remove it and re-run." >&2
		exit 1
	fi
	HELD=$((HELD + 1))
done

# install <src> <dest dir>: copy to a temp file beside the target and rename
# over it, so a reader never sees half a skill.
install_one() {
	src=$1 dest="$2/SKILL.md" tmp="$2/.SKILL.md.$$"
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
install_one "$ROOT/skills/buddy/SKILL.md" "$DEST_DIR"
install_one "$ROOT/skills/buddy-fleet/SKILL.md" "$FLEET_DIR"
