#!/bin/sh
# lib.sh — the two recipes every done-check re-spells, in one place.
#
# Not a general-purpose shell library, and deliberately small: it holds only the
# things that were written out by hand in three or more scripts, where a copy
# that drifts is a check that quietly measures something else.
#
#   repo_root <$0>   the repo root, discovered the SAFE way (see below).
#   mkrepo <dir>     a throwaway git repo a fixture can commit into.
#
# WHY repo_root EXISTS AT ALL, given it is one line. The obvious spelling,
#   cd "$(dirname "$0")/.."
# resolves its operand through the CALLER's $CDPATH first — a variable an
# operator sets for their own interactive convenience and exports without
# thinking about it. With `CDPATH=$HOME/src` set, `cd scripts/..` can land in
# `$HOME/src/scripts/..`, and then a done-check builds, seeds and greps a
# DIFFERENT checkout while reporting GREEN for this one. Counted at the commit
# before this one: SIX scripts here had the unsafe spelling and three had the
# safe one. This is the safe one, and check.sh now gates it.
# `CDPATH=` empties it for the one command, and `--` stops a path that begins
# with `-` from being read as an option.
#
# Measured, not assumed: two identical trees `decoy/scripts` and `real/scripts`,
# `sh scripts/probe.sh` run from `real` with `CDPATH=…/decoy` exported. The bare
# `cd` printed `…/decoy`; `CDPATH= cd --` printed `…/real`. Note the second tell
# in that run — a cd resolved through CDPATH echoes the directory it landed in on
# STDOUT, so `root=$(cd "$(dirname "$0")/.." && pwd)` captures TWO lines, and
# every path built from it is malformed rather than merely wrong.
#
# WHY mkrepo EXISTS. `git init` alone is not enough to commit in: an operator
# with commit.gpgsign on, or with no usable identity, gets a fixture that fails
# for reasons that have nothing to do with what is being checked. The recipe was
# written out three times before this, and the third copy is the one that would
# have been forgotten when a fourth setting became necessary.
#
# Measured on git 2.50.1 here, because the two settings are NOT equally load
# bearing. Deleting `commit.gpgsign false` and running a done-check under a
# global `gpgsign = true` fails the fixture's commit outright (rc=128, "gpg
# failed to sign the data") — and with a real gpg installed it fails by
# PROMPTING, which under an agent harness is indistinguishable from a hang.
# Deleting the identity lines changes nothing on THIS machine: with no config at
# all, git guesses `user@host` from the OS and commits happily. They are there
# for the machine that cannot guess or sets `user.useConfigOnly`, and a mutation
# of them is expected to survive here.
#
# Sourced, never executed: `. "$(dirname -- "$0")/lib.sh"`. Sourcing is done on
# a path with a slash in it on purpose — `.` searches $PATH for an operand with
# no slash, which is the same class of bug as the $CDPATH one above.

# repo_root <script-path> — absolute path of the repo root, given the calling
# script's "$0". Prints nothing and fails if the directory does not exist.
repo_root() {
	CDPATH= cd -- "$(dirname -- "$1")/.." && pwd
}

# mkrepo <dir> — create <dir> as a git repo that a fixture can commit into,
# independent of the operator's global git config.
mkrepo() {
	git init -q "$1"
	git -C "$1" config user.email t@t
	git -C "$1" config user.name t
	# Otherwise the fixture depends on the developer's signing setup, and a
	# missing key fails by prompting rather than by failing.
	git -C "$1" config commit.gpgsign false
}
