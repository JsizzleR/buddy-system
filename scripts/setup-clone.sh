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
cd "$(dirname "$0")/.."
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
if [ -n "$BUDDY" ] && ! "$BUDDY" help 2>&1 | grep -q 'commit-gate'; then
	echo "setup-clone: the installed buddy ($BUDDY) does not know 'commit-gate'." >&2
	echo "  Refusing to enable hooks: this one propagates the gate's exit status, so an" >&2
	echo "  older binary would refuse EVERY commit here with 'unknown command'." >&2
	echo "  Rebuild first, then re-run this script:" >&2
	echo "    go build -o \"$BUDDY\" ./cmd/buddy" >&2
	exit 1
fi

existing=$(git config --get core.hooksPath || true)
if [ -n "$existing" ] && [ "$existing" != ".githooks" ]; then
	# Never silently steal a hooks path somebody else configured — that would
	# disable their hooks and look like this script had simply worked.
	echo "setup-clone: core.hooksPath is already set to '$existing', not .githooks." >&2
	echo "  Refusing to overwrite it. Merge the two by hand, or:" >&2
	echo "    git config core.hooksPath .githooks" >&2
	exit 1
fi
git config core.hooksPath .githooks
chmod +x "$ROOT/.githooks/"* 2>/dev/null || true
echo "setup-clone: core.hooksPath = .githooks"

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
