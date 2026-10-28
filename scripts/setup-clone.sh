#!/bin/sh
# setup-clone.sh — one-time, per-checkout wiring. Safe to re-run.
#
# WHY THIS IS NOT AUTOMATIC. Git refuses to let a repository configure its own
# core.hooksPath, and it is right to: the setting names a directory of programs
# git will execute, so a clone that could set it would be a clone that could run
# code on checkout. Enabling hooks is therefore always a deliberate local act,
# which means an UNINSTALLED hook is the default state of every fresh clone —
# including yours, after every `git clone`.
set -eu
# `CDPATH= cd --`, not a bare cd: see scripts/lib.sh. Landing in another
# checkout here would wire ITS hooks and init ITS ledger.
CDPATH= cd -- "$(dirname -- "$0")/.."
ROOT=$(pwd)

if [ ! -d "$ROOT/.githooks" ]; then
	echo "setup-clone: no .githooks directory in $ROOT" >&2
	exit 1
fi

BUDDY=$(command -v buddy 2>/dev/null || true)
if [ -z "$BUDDY" ] && [ -x "$HOME/bin/buddy" ]; then
	BUDDY="$HOME/bin/buddy"
fi

# THE BINARY IS CHECKED BEFORE THE HOOKS ARE ENABLED, and this ordering is the
# whole point of the check. The pre-commit hook propagates the gate's exit
# status, deliberately — that is what makes the deny posture and the fail-closed
# ledger arm work at all. So an OLD binary behind a NEW hook does not degrade
# gracefully: `buddy commit-gate` is an unknown command, `buddy` exits 2, and
# every commit in this repo is refused with a message about a verb nobody typed.
# Measured, on exactly that pairing.
#
# AND WHY THE EXIT STATUS IS READ SEPARATELY FROM THE OUTPUT. The first spelling
# of this was `"$BUDDY" help 2>&1 | grep -q commit-gate`, and sh has no pipefail:
# a binary that never ran prints nothing, so the pipeline said exactly what an
# OLD binary says and the operator was told to rebuild — the right remedy for the
# wrong reason, which is the kind of diagnosis that costs an hour. That failure is
# routine on this platform, not hypothetical: `cp` over a running Mach-O
# invalidates its ad-hoc signature and the kernel SIGKILLs the next run (137).
# Capture, then judge the status before reading the text.
if [ -n "$BUDDY" ]; then
	help_out=$("$BUDDY" help 2>&1) && help_rc=0 || help_rc=$?
	if [ "$help_rc" -ne 0 ]; then
		echo "setup-clone: '$BUDDY help' exited $help_rc — that binary is broken, not old." >&2
		if [ "$help_rc" -eq 137 ]; then
			echo "  137 is SIGKILL: on macOS, cp over a running Mach-O invalidates its ad-hoc" >&2
			echo "  code signature and the kernel kills the next run." >&2
		fi
		if [ -n "$help_out" ]; then
			printf '  it said: %s\n' "$(printf '%s' "$help_out" | head -n 1)" >&2
		else
			echo "  it printed nothing at all." >&2
		fi
		echo "  Refusing to enable hooks. Rebuild IN PLACE, then re-run this script:" >&2
		echo "    go build -o \"$BUDDY\" ./cmd/buddy" >&2
		exit 1
	fi
	case $help_out in
	*commit-gate*) ;;
	*)
		echo "setup-clone: the installed buddy ($BUDDY) does not know 'commit-gate'." >&2
		echo "  Refusing to enable hooks: this one propagates the gate's exit status, so an" >&2
		echo "  older binary would refuse EVERY commit here with 'unknown command'." >&2
		echo "  Rebuild first, then re-run this script:" >&2
		echo "    go build -o \"$BUDDY\" ./cmd/buddy" >&2
		exit 1
		;;
	esac
fi

existing=$(git config --get core.hooksPath || true)

# ANOTHER SPELLING OF OUR OWN DIRECTORY IS NOT SOMEBODY ELSE'S PATH. Comparing
# the literal against ".githooks" refused this checkout's own hooks spelled
# absolute ('/Users/.../buddy-system/.githooks', set by hand), and since
# install.sh runs this as step 5, every install on that machine ended "FAILED"
# after rebuilding everything, and the hook and handoff reports never printed
# (measured 2026-10-13). Both sides are resolved physically (`pwd -P`; a relative
# path resolves from $ROOT, where git resolves it for a hook run here) and an
# equal one is LEFT AS SET: an absolute path also covers this checkout's linked
# worktrees, where ".githooks" would name each worktree's own copy, and
# rewriting it would change which hooks they run.
own=$(pwd -P)/.githooks
same=""
if [ -n "$existing" ] && [ -d "$existing" ]; then
	resolved=$(CDPATH= cd -- "$existing" 2>/dev/null && pwd -P || true)
	[ "$resolved" != "$own" ] || same=1
fi
if [ -n "$existing" ] && [ "$existing" != ".githooks" ] && [ -z "$same" ]; then
	# Never silently steal a hooks path somebody else configured — that would
	# disable their hooks and look like this script had simply worked.
	echo "setup-clone: core.hooksPath is already set to '$existing', not .githooks." >&2
	echo "  Refusing to overwrite it. Merge the two by hand, or:" >&2
	echo "    git config core.hooksPath .githooks" >&2
	exit 1
fi
if [ -n "$same" ] && [ "$existing" != ".githooks" ]; then
	echo "setup-clone: core.hooksPath = $existing (this checkout's .githooks; left as set)"
else
	git config core.hooksPath .githooks
	echo "setup-clone: core.hooksPath = .githooks"
fi
chmod +x "$ROOT/.githooks/"* 2>/dev/null || true

# The user-level skills (skills/buddy/SKILL.md, and skills/buddy-fleet for a
# coordinator) are how a session in ANY repo learns how the verbs fit; it is loaded from ~/.claude/skills, so a copy that
# is not refreshed teaches last release's verbs. Before the binary check's
# early exit below: the skill is useful whether or not buddy is built yet.
sh "$ROOT/scripts/install-skill.sh"

if [ -z "$BUDDY" ]; then
	# Loud HERE and silent in the hook, on purpose: this is the moment somebody
	# is watching, whereas a hook that complains on every commit gets removed.
	echo "setup-clone: NOTE — no 'buddy' binary on PATH or in ~/bin." >&2
	echo "  The pre-commit claim gate will do nothing until there is one:" >&2
	echo "    go build -o ~/bin/buddy ./cmd/buddy && go build -o ~/bin/buddylist ./cmd/buddylist" >&2
	echo "  (use 'go build -o', not cp: on macOS, cp over a running Mach-O" >&2
	echo "   invalidates its signature and the kernel kills the next run.)" >&2
	exit 0
fi

# The claims ledger. `buddy init` is idempotent and creates
# <git-common-dir>/buddy.db, which is machine-local and never committed.
"$BUDDY" init
echo "setup-clone: done. Commit gate posture: ${BUDDY_COMMIT_GATE:-warn} (BUDDY_COMMIT_GATE=deny to enforce)."
