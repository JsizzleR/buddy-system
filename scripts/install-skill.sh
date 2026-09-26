#!/bin/sh
# install-skill.sh — copy this repo's buddy skill to where Claude Code loads it.
#
# THE SKILL SHIPS IN THE REPO (skills/buddy/SKILL.md) and is LOADED from the
# user's skills directory (~/.claude/skills/buddy/SKILL.md), because it is for
# sessions working in OTHER repositories that use buddy, not only this one. A
# copy step between the two is a copy that goes stale: measured 2026-09-26, the
# installed copy on the developer's own machine was a release behind the repo,
# teaching none of the verbs the release had just added. So setup-clone.sh runs
# this, it is safe to re-run after every pull, and a hermetic done-check
# (check-skill.sh) holds it to installing, refreshing and backing up.
#
# What it does to an existing copy: nothing when it is identical; when it
# differs, the old one is kept as SKILL.md.bak (a local edit is never lost
# silently) and the repo's copy replaces it. It never touches anything else in
# the skills directory.
#
# REFUSED, not guessed (Codex code pass): a SKILL.md that is a symlink (the
# refresh would overwrite whatever it points at) or anything but a regular file
# (cp into a directory named SKILL.md "succeeds" and installs nothing). And ONE
# installer at a time, by an mkdir lock: two concurrent refreshes each backed
# up what they saw, the second backing up the first's fresh copy over the local
# edit the first had saved.
#
# KNOB: BUDDY_SKILL_DIR  the directory to install into (default
#       ~/.claude/skills/buddy). The done-check points it at a temp dir.
set -eu
CDPATH= cd -- "$(dirname -- "$0")/.."
SRC="$(pwd)/skills/buddy/SKILL.md"
DEST_DIR=${BUDDY_SKILL_DIR:-$HOME/.claude/skills/buddy}
DEST="$DEST_DIR/SKILL.md"

if [ ! -f "$SRC" ]; then
	echo "install-skill: no skill at $SRC" >&2
	exit 1
fi
mkdir -p "$DEST_DIR"
if [ -L "$DEST" ] || { [ -e "$DEST" ] && [ ! -f "$DEST" ]; }; then
	echo "install-skill: $DEST is a symlink or not a regular file; refusing to write through it." >&2
	echo "  Move it aside and re-run." >&2
	exit 1
fi
LOCK="$DEST_DIR/.install-skill.lock"
if ! mkdir "$LOCK" 2>/dev/null; then
	echo "install-skill: another install holds $LOCK; if none is running, remove it and re-run." >&2
	exit 1
fi
trap 'rmdir "$LOCK" 2>/dev/null || true' EXIT INT TERM

# Copy to a temp file beside the target and rename over it, so a reader never
# sees half a skill.
TMP="$DEST_DIR/.SKILL.md.$$"
cp "$SRC" "$TMP"
if [ ! -f "$DEST" ]; then
	mv "$TMP" "$DEST"
	echo "install-skill: installed $DEST"
elif cmp -s "$SRC" "$DEST"; then
	rm -f "$TMP"
	echo "install-skill: $DEST is current"
else
	cp "$DEST" "$DEST.bak"
	mv "$TMP" "$DEST"
	echo "install-skill: updated $DEST (the previous copy is at $DEST.bak)"
fi
