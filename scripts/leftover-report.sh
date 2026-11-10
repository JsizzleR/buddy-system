#!/bin/sh
# leftover-report.sh — the operator's processes that outlived the process that
# started them and were adopted by launchd, each tied to a buddy session where
# the kernel lets it: pid, ppid, age, RSS, tree, verdict. Never argv. It ends,
# signals and grants nothing (D-027): reaping stays the orchestrator's act, by
# pid, and whether it may reap without asking is the operator's decision.
#
#   sh scripts/leftover-report.sh [--all] [<repo> ...]   # ledgers to read; default the cwd's
#
# The census is scripts/leftover-report.py (its header carries the darwin
# measurements it rests on, what it prints and what was cut). This wrapper
# exists so the report sits in the *-report.sh family the docs list, and so
# LC_ALL=C is set before anything runs: the python reads the process table
# through libproc, never `ps`, `pgrep` or `pkill`, but git and anything it
# spawns inherit the locale (D-122).
#
# Exit 0 a report; 1 a usage refusal; 2 CANNOT SAY (not darwin, or the kernel
# did not list its processes): not run is neither pass nor fail.
LC_ALL=C
export LC_ALL
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd) || exit 1
exec python3 -I "$here/leftover-report.py" "$@"
