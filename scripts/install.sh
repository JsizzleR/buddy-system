#!/bin/sh
# install.sh — install or UPGRADE everything buddy puts on this machine, from
# this checkout, and say what it did. Safe to re-run after every pull.
#
#   sh scripts/install.sh
#
# WHY ONE SCRIPT. An upgrade touched four places by hand, and the one that was
# forgotten was the one nobody could see. Measured 2026-09-26, right after a
# schema bump: the hooks' ~/bin/buddy and ~/bin/buddylist had been rebuilt, the
# skill had been copied — and the chat daemon was still running a buddylist
# built six days earlier, from a DIFFERENT path (the launchd plist names the
# checkout's bin/, not ~/bin). The skill copy had been a release behind the
# repo the day before. A schema bump makes the stale-binary case a denial, not
# a cosmetic lag: an older buddy REFUSES a newer ledger and the gate then
# denies every write (D-037). So every binary the machine actually runs is
# rebuilt here, found by where it is RUN from, not by where it ought to be.
#
# WHAT IT DOES, in order, each step verified before the next:
#   1. builds buddy and buddylist into $BUDDY_BIN_DIR (default ~/bin), and
#      rebuilds any OTHER copy the machine runs: the program the launchd
#      daemon agent names, and the checkout's own bin/ copies when present;
#   2. runs each built binary once (`buddy help`, bare `buddylist`, which
#      prints its usage) and checks it says what it is — one that exits 137 was
#      SIGKILLed for a broken ad-hoc signature, and a hook that ran it would
#      have been silent (every hook line ends in `exit 0`);
#   3. installs the user-level skill (scripts/install-skill.sh);
#   4. restarts the chat daemon under launchd (`kickstart -k`, never kill:
#      KeepAlive respawns a killed daemon and the loser of the socket race
#      dies — CLAUDE.md) and checks it is running again, waiting out the
#      launch constraint a NEW build meets first (below);
#   5. wires THIS checkout (scripts/setup-clone.sh: hooks path, ledger);
#   6. REPORTS which Claude Code hooks are wired in ~/.claude/settings.json.
#      It never edits that file: which hooks a user runs is theirs to choose,
#      and a script that rewrote it would be a script that could remove one.
#   A daemon that does not come back fails the install, but at the END: steps
#   5 and 6 do not depend on it, and dying at step 4 skipped them.
#
# A NEW DAEMON BUILD IS REFUSED ITS FIRST SPAWN (measured 2026-10-03, macOS
# 27). The agent carries a launch constraint (`managed LWCR` in `launchctl
# print`), and a CHANGED binary at the program path fails it until macOS
# re-registers the item; what the constraint records was not read, only that
# a changed binary fails it and an unchanged one does not. This binary is
# ad-hoc signed (identifier a.out, no team), so its identity is its code hash,
# and go stamps every build with the commit (-buildvcs), so every install after
# a new commit is a new binary. launchd's first spawn of it fails, `xpcproxy exited
# due to OS_REASON_CODESIGNING | Launch Constraint Violation`; macOS's
# background task manager re-registers the item (`invalidateLaunchItem`) 10.0 s
# after that refusal (two samples: 10.016 s, 10.015 s) and the next spawn runs.
# It follows the SPAWN, not the file: a build left 15 s before the kickstart was
# refused the same way, and nothing was logged in between (Codex's
# discriminator, measured). So it cannot be waited out in advance, only polled
# past. The old ten-second wait lost that race (state "spawn scheduled") and the
# install died before steps 5 and 6. An unchanged binary comes back in 0.14 s
# with no violation (the control). So the wait is 30 s, and a slow return names
# the exit reason launchd recorded.
#
# BUILT WITH `go build -o` STRAIGHT OVER THE TARGET, never cp: on macOS, cp
# over an existing Mach-O invalidates its ad-hoc signature and the kernel
# SIGKILLs the next run (exit 137, measured). That bites hooks specifically.
#
# KNOBS (the done-check, check-install.sh, uses all of them to stay hermetic):
#   BUDDY_BIN_DIR       where the binaries go            (default ~/bin)
#   BUDDY_SKILL_DIR     passed to install-skill.sh       (default ~/.claude/skills/buddy)
#   BUDDY_DAEMON=off    skip step 4 (and the daemon's program in step 1)
#   BUDDY_SETUP_CLONE=off  skip step 5
#   BUDDY_SETTINGS      the settings file step 6 reads   (default ~/.claude/settings.json)
#   BUDDY_REPO_BIN=off  skip the checkout's own bin/ copies in step 1
#   BUDDY_PLIST         the daemon agent's plist          (default ~/Library/LaunchAgents/<agent>.plist)
#   BUDDY_LAUNCHCTL     the launchctl step 4 runs         (default launchctl; the done-check's is a fake)
#   BUDDY_DAEMON_WAIT   seconds step 4 waits for running  (default 30)
set -eu
CDPATH= cd -- "$(dirname -- "$0")/.."
ROOT=$(pwd)

BIN_DIR=${BUDDY_BIN_DIR:-$HOME/bin}
SETTINGS=${BUDDY_SETTINGS:-$HOME/.claude/settings.json}
AGENT=com.buddy-system.buddylistd
PLIST=${BUDDY_PLIST:-$HOME/Library/LaunchAgents/$AGENT.plist}
LAUNCHCTL=${BUDDY_LAUNCHCTL:-launchctl}
DAEMON_WAIT=${BUDDY_DAEMON_WAIT:-30}

# deferred holds a failure that must not stop the steps after it (the daemon,
# step 4); it is reported, and fails the install, at the end.
deferred=""
say() { echo "install: $*"; }
die() { echo "install: FAILED — $*" >&2; exit 1; }

command -v go >/dev/null 2>&1 || die "no go toolchain on PATH"
case $DAEMON_WAIT in
'' | *[!0-9]*) die "BUDDY_DAEMON_WAIT must be a whole number of seconds, not '$DAEMON_WAIT'" ;;
esac
# Bounded too: a value past the shell's integer range breaks the comparison in
# the wait, which then reads as that long a wait having expired (Codex).
[ ${#DAEMON_WAIT} -le 4 ] || die "BUDDY_DAEMON_WAIT must be at most 9999 seconds, not '$DAEMON_WAIT'"

rev=$(git rev-parse --short=8 HEAD 2>/dev/null || echo unknown)
dirty=""
if [ -n "$(git status --porcelain --untracked-files=no 2>/dev/null)" ]; then
	dirty=" + UNCOMMITTED changes (what is installed is this working tree, not $rev)"
fi
say "installing from $ROOT at $rev$dirty"

# build <cmd> <target>: go build -o straight over the target, then run it once
# and check it names itself — output alone is not enough, since a binary that
# never ran and one that ran and said nothing look the same from a hook.
build() {
	cmd=$1 target=$2
	mkdir -p "$(dirname -- "$target")"
	[ ! -d "$target" ] || die "$target is a directory, not a binary"
	go build -o "$target" "./cmd/$cmd" || die "go build ./cmd/$cmd -o $target"
	# buddy answers `help` with 0. buddylist has no help verb: run bare, it
	# prints its usage and exits 2 — a USAGE exit, which is fine here, and
	# distinct from 126/127 (cannot execute) and 137 (SIGKILL). (A first
	# probe of this read `buddylist | head` and saw 0: head's status.)
	if [ "$cmd" = buddy ]; then
		out=$("$target" help 2>&1) && rc=0 || rc=$?
	else
		out=$("$target" 2>&1) && rc=0 || rc=$?
		[ "$rc" -ne 2 ] || rc=0
	fi
	if [ "$rc" -ne 0 ]; then
		why=""
		[ "$rc" -ne 137 ] || why=" (SIGKILL: a broken ad-hoc signature)"
		die "$target exited $rc on its first run$why"
	fi
	case $out in
	*"$cmd "*) ;;
	*) die "$target ran but did not name itself: $(printf '%s' "$out" | head -n 1)" ;;
	esac
	say "built $target"
}

# run_step <label> <cmd...>: a sub-script's output prefixed, its status KEPT.
# Piping into sed would report sed's status (sh has no pipefail), which is how
# a failed setup-clone would have read as a clean install.
run_step() {
	label=$1; shift
	out=$("$@" 2>&1) && rc=0 || rc=$?
	[ -z "$out" ] || printf '%s\n' "$out" | sed "s/^/install: /"
	[ "$rc" -eq 0 ] || die "$label exited $rc"
}

# 1-2. Binaries: the ones the hooks and MCP servers run, then every other copy.
build buddy "$BIN_DIR/buddy"
build buddylist "$BIN_DIR/buddylist"
case $("$BIN_DIR/buddy" help 2>&1) in
*commit-gate*) ;;
*) die "$BIN_DIR/buddy does not know commit-gate; the build is not this checkout's" ;;
esac

daemon_prog=""
if [ "${BUDDY_DAEMON:-}" != off ] && [ -f "$PLIST" ]; then
	daemon_prog=$(/usr/libexec/PlistBuddy -c 'Print :ProgramArguments:0' "$PLIST" 2>/dev/null || true)
	case $daemon_prog in
	*/buddylist)
		if [ "$daemon_prog" != "$BIN_DIR/buddylist" ]; then
			build buddylist "$daemon_prog"
		fi
		;;
	"") say "NOTE — $PLIST names no program; the daemon is not rebuilt" ;;
	*) say "NOTE — the daemon agent runs $daemon_prog, which is not a buddylist; left alone" ;;
	esac
fi
if [ "${BUDDY_REPO_BIN:-}" != off ]; then
	for b in buddy buddylist; do
		t="$ROOT/bin/$b"
		if [ -f "$t" ] && [ "$t" != "$daemon_prog" ] && [ "$t" != "$BIN_DIR/$b" ]; then
			build "$b" "$t"
		fi
	done
fi

# 3. The skill.
run_step install-skill sh "$ROOT/scripts/install-skill.sh"

# 4. The daemon. LC_ALL=C in front of anything that reads process tables
# (CLAUDE.md: a non-ASCII argv elsewhere on the box aborts awk mid-scan).
if [ "${BUDDY_DAEMON:-}" = off ]; then
	say "daemon: skipped (BUDDY_DAEMON=off)"
elif [ ! -f "$PLIST" ]; then
	say "daemon: no launchd agent ($PLIST); nothing to restart"
else
	uid=$(id -u)
	if ! LC_ALL=C "$LAUNCHCTL" print "gui/$uid/$AGENT" >/dev/null 2>&1; then
		say "NOTE — $AGENT is not loaded; load it with: launchctl bootstrap gui/$uid $PLIST"
	elif ! LC_ALL=C "$LAUNCHCTL" kickstart -k "gui/$uid/$AGENT"; then
		# Deferred like a daemon that does not come back: steps 5 and 6 do not
		# depend on it (Codex).
		deferred="launchctl kickstart -k gui/$uid/$AGENT failed; see: launchctl print gui/$uid/$AGENT"
		echo "install: daemon: kickstart FAILED — the install will fail after the remaining steps" >&2
	else
		# Up to DAEMON_WAIT seconds: a new build's first spawn is refused and
		# macOS re-registers it about ten seconds later (see the header). The
		# exit reason is read while waiting, so a slow return says why.
		i=0 state="" reason=""
		while :; do
			job=$(LC_ALL=C "$LAUNCHCTL" print "gui/$uid/$AGENT" 2>/dev/null || true)
			state=$(printf '%s\n' "$job" | LC_ALL=C awk -F' = ' '/^\tstate = /{print $2; exit}')
			r=$(printf '%s\n' "$job" | LC_ALL=C awk -F' = ' '/^\tlast exit reason = /{print $2; exit}')
			[ -z "$r" ] || reason=$r
			[ "$state" = running ] && break
			[ "$i" -lt "$DAEMON_WAIT" ] || break
			sleep 1
			i=$((i + 1))
		done
		# The reason is what launchd RECORDED, said as that: on 2026-10-03 a
		# CODESIGNING reason here was a new build's launch constraint, and the
		# line says so as a pointer, not as a finding about this run (Codex).
		why=""
		case $reason in
		*CODESIGNING*) why="; launchd's last exit reason: $reason (a new build meets a launch constraint until macOS re-registers it; see install.sh's header)" ;;
		?*) why="; launchd's last exit reason: $reason" ;;
		esac
		if [ "$state" != running ]; then
			deferred="the daemon did not come back within ${DAEMON_WAIT}s (state: ${state:-unknown}$why); see ~/.buddylist/buddylistd.log and: launchctl print gui/$uid/$AGENT"
			echo "install: daemon: NOT running — the install will fail after the remaining steps" >&2
		elif [ "$i" -eq 0 ]; then
			say "daemon: restarted on the new build ($daemon_prog)"
		else
			say "daemon: restarted on the new build ($daemon_prog) after ${i}s$why"
		fi
	fi
fi

# 5. This checkout's wiring (hooks path, ledger; it also installs the skill,
# which is then already current).
if [ "${BUDDY_SETUP_CLONE:-}" = off ]; then
	say "setup-clone: skipped (BUDDY_SETUP_CLONE=off)"
else
	# env, not a PATH= prefix: an assignment before a FUNCTION call is
	# unspecified in POSIX sh, and setup-clone must find the binary just built.
	run_step setup-clone env PATH="$BIN_DIR:$PATH" sh "$ROOT/scripts/setup-clone.sh"
fi

# 6. What is wired, reported. Absent is not an error: busy is optional
# (D-016), and the chat lines are the visibility half.
if [ ! -f "$SETTINGS" ]; then
	say "hooks: no $SETTINGS — none are wired; README \"Hook wiring\" has the lines"
else
	missing=""
	for want in "buddy hello" "buddy gate" "buddy beat" "buddy idle" "buddy bye" "buddy busy" "buddylist alert" "buddylist presence"; do
		bin=${want% *} verb=${want#* }
		if ! grep -q "$bin\\\\\"* $verb" "$SETTINGS" && ! grep -q "/$bin $verb" "$SETTINGS"; then
			missing="$missing $want"
		fi
	done
	if [ -z "$missing" ]; then
		say "hooks: all eight wired in $SETTINGS"
	else
		say "hooks: NOT wired in $SETTINGS:$missing (README \"Hook wiring\"; busy is optional)"
	fi
fi

# 7. The handoff size, reported (D-064). BUDDY_HANDOFF_AT in the settings `env`
# block reaches every session's hooks, the operator-launched first
# orchestrator included (measured 2026-09-29: a settings env entry is in the
# UserPromptSubmit hook's environment). In the handoffs measured for D-055 the
# OPERATOR noticed the size in 3 of 4, and the next one measured (2026-09-29)
# was the same, so whether it is set is worth one line. Reported only: the
# size is the operator's to choose and this script never edits settings. A
# grep, not a JSON parse: it matches the key as a JSON key and nothing else,
# and a false "set" would only say so.
handoff=""
[ -f "$SETTINGS" ] && handoff=$(sed -n 's/.*"BUDDY_HANDOFF_AT"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$SETTINGS" | head -1)
if [ -z "$handoff" ]; then
	say "handoff: BUDDY_HANDOFF_AT not set in $SETTINGS; a session is told to hand off only if its own environment declares it (D-055; e.g. \"env\": {\"BUDDY_HANDOFF_AT\": \"400k\"})"
else
	case " ${missing:-} " in
	*" buddy busy "*) say "handoff: BUDDY_HANDOFF_AT=$handoff in $SETTINGS, but buddy busy is NOT wired, so no session is ever told" ;;
	*) say "handoff: BUDDY_HANDOFF_AT=$handoff in $SETTINGS; every session is told as a turn opens once its last prompt is at or past it" ;;
	esac
fi
if [ -n "$deferred" ]; then
	echo "install: FAILED — $deferred" >&2
	exit 1
fi
say "done"
