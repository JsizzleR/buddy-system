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
# What it does to an existing copy: nothing when it is identical; when it
# differs, the old one is kept as SKILL.md.bak (a local edit is never lost
# silently) and the repo's copy replaces it. It never touches anything else in
# the skills directory. An old single-file install (the whole 73 KB skill at
# ~/.claude/skills/buddy) is such a copy: replaced, kept as .bak, and the other
# skills installed beside it.
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
for n in $NAMES; do check "$ROOT/skills/$n/SKILL.md" "$(dest "$n")"; done

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
trap 'printf "%s\n" "$HELD" | while IFS= read -r d; do [ -n "$d" ] || continue; rm -f "$d/.SKILL.md.$$"; rmdir "$d/.install-skill.lock" 2>/dev/null; done; true' EXIT INT TERM
for n in $NAMES; do
	d=$(dest "$n")
	if ! mkdir "$d/.install-skill.lock" 2>/dev/null; then
		echo "install-skill: another install holds $d/.install-skill.lock; if none is running, remove it and re-run." >&2
		exit 1
	fi
	HELD="$HELD$d
"
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
for n in $NAMES; do install_one "$ROOT/skills/$n/SKILL.md" "$(dest "$n")"; done
