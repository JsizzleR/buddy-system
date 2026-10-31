#!/bin/sh
# install.sh — install or UPGRADE everything buddy puts on this machine, from
# this checkout, and say what it did. Safe to re-run after every pull.
#
#   sh scripts/install.sh
#   buddy update          (once installed, from anywhere: runs this script in
#                          the checkout the installed buddy was built from)
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
#      every buddy carries this checkout's path, which `buddy update` reads;
#   2. runs each built binary once (`buddy help`, bare `buddylist`, which
#      prints its usage) and checks it says what it is — one that exits 137 was
#      SIGKILLed for a broken ad-hoc signature, and a hook that ran it would
#      have been silent (every chat hook line ends in `exit 0`) or, through the
#      gate's line, refused every matching tool call (D-107); and buddy runs
#      `gate` once in a fresh temp directory, which must exit 0. Each is
#      built BESIDE its target and renamed over it only once it passed, so a
#      build that fails here leaves the old binary in place;
#   3. installs the user-level skills, buddy and buddy-fleet (scripts/install-skill.sh);
#   4. restarts the chat daemon under launchd (`kickstart -k`, never kill:
#      KeepAlive respawns a killed daemon and the loser of the socket race
#      dies — CLAUDE.md) and checks it is running again, waiting out the
#      launch constraint a NEW build meets first (below);
#   5. wires THIS checkout (scripts/setup-clone.sh: hooks path, ledger);
#   6. REPORTS which Claude Code hooks are wired in ~/.claude/settings.json,
#      and whether the gate's is README's fail-closed entry or the old line
#      that let a crashed buddy's tool call through (D-107).
#      It never edits that file: which hooks a user runs is theirs to choose,
#      and a script that rewrote it would be a script that could remove one.
#      It also REPORTS whether the Agent-model hook's copy in the bin dir is
#      this checkout's script, and whether settings wire it (D-120); it
#      installs neither.
#   A daemon that does not come back fails the install, but at the END: steps
#   5 and 6 do not depend on it, and dying at step 4 skipped them.
#
# CLAIMS-ONLY (BUDDY_CHAT=off). The architecture promises that claims work with
# chat entirely absent (CLAUDE.md, invariant 1), but this script always built
# buddylist and its hook report nagged "NOT wired: buddylist alert buddylist
# presence" on every run of a deployment that allows no server of any kind. With
# chat off (BUDDY_CHAT trimmed and lower-cased, exactly `off`, or that posture
# kept; below) steps 1, 2 and 4 touch only the claims half: buddy is built and
# verified, no buddylist is built anywhere, the daemon step is skipped, and the
# hook report counts the six buddy lines. It is CONFIGURATION, never a probe:
# a socket file, a buddylist beside buddy or an MCP registration each lie in
# both directions (a stale socket; a daemon mid-restart under launchd; a
# different HOME). And "off" describes THIS INSTALL, not the machine: a
# buddylist already in the bin dir, the daemon's plist and an MCP registration
# are REPORTED as left alone, and nothing is removed, stopped or unloaded. An
# install that uninstalled on a knob would be an install that could remove the
# chat half by a typo in a settings env block.
#
# THE POSTURE IS KEPT (D-124). Read from this invocation alone, off lasted one
# run: README puts BUDDY_CHAT=off in settings' env block, which reaches
# sessions and not a terminal, so the operator's `buddy update` from a
# terminal built buddylist again and fetched its modules (measured
# 2026-10-31, a claims-only laptop). So every buddy built here is stamped with
# the posture it was built under (-X …installedChat=on|off, like D-110's
# checkout), and a run whose BUDDY_CHAT is unset or empty keeps what
# $BUDDY_BIN_DIR/buddy records. BUDDY_CHAT=off or =on on a run is the switch,
# said as CHANGED when it flips the stamp; any other value is refused, since a
# typo that read as the default was the silent failure; no stamp (a fresh bin
# dir, a buddy built before D-124 or by hand) is on, said as the default. The
# stamp travels with the bytes it describes, so a bin dir carries its own and a
# hand `go build -o` over it inherits nothing it did not earn. Settings' env
# value is NOT read for the posture: it is hello's knob, and an operator who
# muted the hint there would lose the daemon's rebuild (6b' says when the two
# disagree).
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
#   BUDDY_FLEET_SKILL_DIR  likewise                       (default buddy-fleet beside it)
#   BUDDY_DAEMON=off    skip step 4 (and the daemon's program in step 1)
#   BUDDY_SETUP_CLONE=off  skip step 5
#   BUDDY_SETTINGS      the settings file step 6 reads   (default ~/.claude/settings.json)
#   BUDDY_REPO_BIN=off  skip the checkout's own bin/ copies in step 1
#   BUDDY_PLIST         the daemon agent's plist          (default ~/Library/LaunchAgents/<agent>.plist)
#   BUDDY_LAUNCHCTL     the launchctl step 4 runs         (default launchctl; the done-check's is a fake)
#   BUDDY_DAEMON_WAIT   seconds step 4 waits for running  (default 30)
#   BUDDY_CHAT=off|on   claims-only, or chat, from this run on (unset: keep
#                       what $BUDDY_BIN_DIR/buddy records; none: on)
#   BUDDY_CLAUDE_JSON   the user-level MCP registrations file the claims-only
#                       remnants report reads             (default ~/.claude.json)
#   BUDDY_MCP_JSON      the checkout's MCP registrations file it reads
#                                                         (default <checkout>/.mcp.json)
#   BUDDY_PYTHON        the python step 6a decodes settings with (default python3)
set -eu
CDPATH= cd -- "$(dirname -- "$0")/.."
ROOT=$(pwd)

BIN_DIR=${BUDDY_BIN_DIR:-$HOME/bin}
SETTINGS=${BUDDY_SETTINGS:-$HOME/.claude/settings.json}
AGENT=com.buddy-system.buddylistd
PLIST=${BUDDY_PLIST:-$HOME/Library/LaunchAgents/$AGENT.plist}
LAUNCHCTL=${BUDDY_LAUNCHCTL:-launchctl}
DAEMON_WAIT=${BUDDY_DAEMON_WAIT:-30}
CLAUDE_JSON=${BUDDY_CLAUDE_JSON:-$HOME/.claude.json}
# A knob too, not only $ROOT/.mcp.json: the done-check runs from the real
# checkout, and a checkout that registers buddylist there would otherwise fail
# its "nothing left alone" assertion, so the check's hermetic contract is
# this variable's (the orchestrator's code pass).
MCP_JSON=${BUDDY_MCP_JSON:-$ROOT/.mcp.json}
# The repo's one folding rule is ToLower(NFC(s)) after trimming; `off` is ASCII,
# so NFC changes nothing on a value that IS off, and tr's ASCII fold under
# LC_ALL=C is the test the hooks apply to the same variable. NOT byte parity
# with hello's reading, which sh cannot offer: Go's TrimSpace also strips
# Unicode spaces, so an NBSP around `off` is off for hello and the default
# here (sed's [[:space:]] is ASCII), and sh cannot NFC-fold at all. Both
# divergences fall the safe way: this script installs MORE than hello
# believes is there, and it refuses nothing. chat_off is set or empty.
chat=$(printf '%s' "${BUDDY_CHAT:-}" | LC_ALL=C tr '[:upper:]' '[:lower:]' | LC_ALL=C sed 's/^[[:space:]]*//; s/[[:space:]]*$//')

# deferred holds a failure that must not stop the steps after it (the daemon,
# step 4); it is reported, and fails the install, at the end.
deferred=""
say() { echo "install: $*"; }
die() { echo "install: FAILED — $*" >&2; exit 1; }
# shq: one word a shell reads back as the same path, for a remedy line meant
# to be pasted (bare when every byte is safe, else single-quoted).
shq() {
	case $1 in
	'' | *[!A-Za-z0-9._/:@%+=,-]*) printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")" ;;
	*) printf '%s' "$1" ;;
	esac
}

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

# The chat posture (D-124): this run's BUDDY_CHAT when it says off or on, else
# what the installed buddy records, else on. Read from the FILE's build info,
# never by running it: a buddy that is SIGKILLed or panics would otherwise read
# as "no posture" and put chat back. Its own -X is FIRST in the flags (build
# below), so the match is anchored at the start of the value and a checkout
# path spelling the same words further on cannot be read as it. go prints the
# value quoted (it always holds a space); escapes, if any, come after the
# prefix. A file that is no Go binary, or none at all, records nothing. The
# same reading as update.go's chatOf: the dots are literal there, so here too.
# Only a line that BEGINS with go's tab, "build", tab is a build setting: the
# first line go prints is the file's own path, and a bin dir spelled
# `build -ldflags="-X '…installedChat=on'` was read as the stamp (Codex). A
# path holding a line break and that tab is the one crafted form left open.
CHAT_X=github.com/JsizzleR/buddy-system/internal/cli.installedChat
chat_re=$(printf '%s' "$CHAT_X" | sed 's/[.]/[.]/g')
tab=$(printf '\t')
installed_chat=""
if [ -f "$BIN_DIR/buddy" ]; then
	installed_chat=$(go version -m "$BIN_DIR/buddy" 2>/dev/null |
		LC_ALL=C sed -n -E "s#^${tab}build${tab}-ldflags=\"-X '$chat_re=(on|off)'.*#\\1#p" | head -n 1)
fi
case $chat in
off | on) posture=$chat ;;
'') posture=${installed_chat:-on} ;;
*)
	# One line, bounded: a line break in the value would print a forged
	# `install:` line after the refusal (Codex; invariant 9's register).
	shown=$(printf '%s' "$BUDDY_CHAT" | LC_ALL=C tr -c '[:print:]' '?' | head -c 64)
	die "BUDDY_CHAT must be off or on, or unset to keep what $BIN_DIR/buddy records, not '$shown'" ;;
esac
chat_off=""
[ "$posture" != off ] || chat_off=1
# One line, said BEFORE anything is built: building buddylist fetches its
# modules, and a posture said at the end was said too late (D-124).
if [ -n "$chat" ] && [ -n "$installed_chat" ] && [ "$chat" != "$installed_chat" ]; then
	say "chat: CHANGED from $installed_chat to $posture by BUDDY_CHAT=$chat on this run; every later install keeps $posture until BUDDY_CHAT says otherwise"
elif [ -n "$chat" ]; then
	say "chat: $posture (BUDDY_CHAT=$chat on this run; kept by every later install until BUDDY_CHAT says otherwise)"
elif [ -n "$installed_chat" ]; then
	say "chat: $posture (kept: $BIN_DIR/buddy was installed with chat $installed_chat; BUDDY_CHAT=$([ "$posture" = on ] && echo off || echo on) on a run changes it)"
else
	say "chat: on (the default: no buddy at $BIN_DIR/buddy records a chat posture; BUDDY_CHAT=off on a run makes this install claims-only, and later installs keep it)"
	# Said, never acted on (orc-14's ask, D-124): a buddy with no readable
	# posture and no buddylist beside it is what a claims-only install from
	# before D-124 (or under -trimpath, or by hand) leaves. Deciding off from
	# it would be the probe the header rejects (a buddylist removed by hand
	# reads the same), so it is a NOTE before buddylist is built, and the
	# dry run of buddy update says it before anything runs.
	if [ -f "$BIN_DIR/buddy" ] && [ ! -e "$BIN_DIR/buddylist" ]; then
		say "chat: NOTE — $BIN_DIR/buddy records no chat posture (built before D-124, by hand, or under -trimpath) and has no buddylist beside it; if this install was claims-only, interrupt now and re-run with BUDDY_CHAT=off"
	fi
fi

# build <cmd> <target>: go build -o a NEW file beside the target, run it once
# and check it names itself — output alone is not enough, since a binary that
# never ran and one that ran and said nothing look the same from a hook — and
# only then rename it over the target.
#
# BESIDE, NOT OVER (D-107). Built straight over the target, a build whose gate
# died was already in place when its probe found it, and README's gate line
# refuses every gated call while the installed buddy cannot run: every session
# on the machine, the Bash of an agent running this install included (the
# cross-check). Renamed in only after every probe passed, a failed build leaves
# the old binary where it was. A rename is one directory entry on one volume,
# never a cp over a running Mach-O (CLAUDE.md), and a process still running the
# old binary keeps it. The new file's path is absolute because the gate probe
# runs it from a temporary directory (a relative BUDDY_BIN_DIR is the
# checkout's, where this script cd'd first; the code pass's relative-path
# finding).
build() {
	cmd=$1 target=$2
	mkdir -p "$(dirname -- "$target")"
	[ ! -d "$target" ] || die "$target is a directory, not a binary"
	new=$target.new-$$
	case $new in /*) ;; *) new=$ROOT/$new ;; esac
	# buddy carries this checkout's path, which `buddy update` reinstalls
	# from (internal/cli/update.go: a stamp travels with the bytes built and
	# probed here; a record file beside them would not). go splits -ldflags
	# on spaces and honours ' and " quotes with no escapes, so a path holding
	# both cannot be passed: that build carries no stamp, and update refuses
	# it naming this script, never a guess. The chat posture is stamped on
	# every buddy, FIRST and always single-quoted (its value is a fixed word),
	# so the next install finds it at the start of the flags (D-124). Built
	# -trimpath=false on the command line, which wins over GOFLAGS: under
	# -trimpath go leaves -ldflags out of the build info (measured, go 1.26),
	# so a GOFLAGS=-trimpath shell would install a buddy whose posture the
	# next install cannot read, and chat would come back (the cross-check).
	ldflags="" trim=""
	if [ "$cmd" = buddy ]; then
		trim=-trimpath=false
		ldflags="-X '$CHAT_X=$posture'"
		x="github.com/JsizzleR/buddy-system/internal/cli.installedFrom=$ROOT"
		case $ROOT in
		*\'*\"* | *\"*\'*) say "NOTE — $ROOT holds both quote characters, so buddy cannot record it; buddy update will refuse and name this script" ;;
		*\'*) ldflags="$ldflags -X \"$x\"" ;;
		*) ldflags="$ldflags -X '$x'" ;;
		esac
	fi
	go build $trim -ldflags "$ldflags" -o "$new" "./cmd/$cmd" || { rm -f "$new"; die "go build ./cmd/$cmd -o $target"; }
	# buddy answers `help` with 0. buddylist has no help verb: run bare, it
	# prints its usage and exits 2 — a USAGE exit, which is fine here, and
	# distinct from 126/127 (cannot execute) and 137 (SIGKILL). (A first
	# probe of this read `buddylist | head` and saw 0: head's status.)
	if [ "$cmd" = buddy ]; then
		out=$("$new" help 2>&1) && rc=0 || rc=$?
	else
		out=$("$new" 2>&1) && rc=0 || rc=$?
		[ "$rc" -ne 2 ] || rc=0
	fi
	if [ "$rc" -ne 0 ]; then
		why=""
		[ "$rc" -ne 137 ] || why=" (SIGKILL: a broken ad-hoc signature)"
		rm -f "$new"
		die "$target: the new build exited $rc on its first run$why; not installed, $target left as it was"
	fi
	case $out in
	*"$cmd "*) ;;
	*) rm -f "$new"; die "$target: the new build ran but did not name itself: $(printf '%s' "$out" | head -n 1); not installed, $target left as it was" ;;
	esac
	# `help` proves the binary starts, not that the gate does: a gate path that
	# dies is a refusal of every gated call machine-wide once installed
	# (D-107), so it is run once here on a Write inside a fresh temp
	# directory and must exit 0. Its verdict is not read: a TMPDIR inside a
	# paused repository would deny, and that is a decision too. The payload
	# is a constant with no `cwd` and a relative path, which the gate
	# resolves against its own working directory: a temp path printed into
	# the JSON broke it when TMPDIR held a `"`, and the gate's parse-error
	# deny exits 0, so the probe passed without reaching the Write (the code
	# pass). The directory is named under $TMPDIR by an
	# explicit template: macOS's bare `mktemp -d` ignores TMPDIR (it takes the
	# per-user directory confstr names; measured), and the done-check moves
	# the probe through TMPDIR.
	if [ "$cmd" = buddy ]; then
		probe=$(mktemp -d "${TMPDIR:-/tmp}/buddy-gate-probe.XXXXXX") || { rm -f "$new"; die "mktemp failed"; }
		gout=$(cd "$probe" && printf '%s' '{"session_id":"install-probe","hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":"probe"}}' |
			"$new" gate 2>&1) && grc=0 || grc=$?
		rm -rf "$probe"
		if [ "$grc" -ne 0 ]; then
			rm -f "$new"
			first=$(printf '%s' "$gout" | head -n 1)
			die "$target: the new build's gate exited $grc on its probe (a Write in a fresh temp directory)${first:+: $first}; not installed, $target left as it was"
		fi
	fi
	mv -f "$new" "$target" || { rm -f "$new"; die "could not rename the new build over $target"; }
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
# Claims-only builds buddy alone: bins lists what this invocation installs, and
# the daemon's program is a buddylist, so that arm is skipped with it.
bins="buddy buddylist"
[ -z "$chat_off" ] || bins=buddy
build buddy "$BIN_DIR/buddy"
[ -n "$chat_off" ] || build buddylist "$BIN_DIR/buddylist"
case $("$BIN_DIR/buddy" help 2>&1) in
*commit-gate*) ;;
*) die "$BIN_DIR/buddy does not know commit-gate; the build is not this checkout's" ;;
esac

daemon_prog=""
if [ -z "$chat_off" ] && [ "${BUDDY_DAEMON:-}" != off ] && [ -f "$PLIST" ]; then
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
	for b in $bins; do
		t="$ROOT/bin/$b"
		if [ -f "$t" ] && [ "$t" != "$daemon_prog" ] && [ "$t" != "$BIN_DIR/$b" ]; then
			build "$b" "$t"
		fi
	done
fi

# 3. The skills.
run_step install-skill sh "$ROOT/scripts/install-skill.sh"

# 4. The daemon. LC_ALL=C in front of anything that reads process tables
# (CLAUDE.md: a non-ASCII argv elsewhere on the box aborts awk mid-scan).
# Claims-only is named first: it is the broader statement, and with no buddylist
# built there is nothing a restart could put into service.
if [ -n "$chat_off" ]; then
	say "daemon: skipped (chat off: claims-only install)"
elif [ "${BUDDY_DAEMON:-}" = off ]; then
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
# (D-016), and the chat lines are the visibility half. Claims-only counts the
# six buddy lines: a report that nagged about two chat hooks a deployment
# forbids would be a report nobody read.
wanted="buddy hello buddy gate buddy beat buddy idle buddy bye buddy busy"
count=six
if [ -z "$chat_off" ]; then
	wanted="$wanted buddylist alert buddylist presence"
	count=eight
fi
if [ ! -f "$SETTINGS" ]; then
	say "hooks: no $SETTINGS — none are wired; README \"Hook wiring\" has the lines"
else
	missing=""
	set -- $wanted
	while [ $# -ge 2 ]; do
		bin=$1 verb=$2; shift 2
		if ! grep -q "$bin\\\\\"* $verb" "$SETTINGS" && ! grep -q "/$bin $verb" "$SETTINGS"; then
			missing="$missing $bin $verb"
		fi
	done
	if [ -z "$missing" ]; then
		say "hooks: all $count wired in $SETTINGS"
	else
		say "hooks: NOT wired in $SETTINGS:$missing (README \"Hook wiring\"; busy is optional)"
	fi
	# 6a. The gate's own line, judged (D-107). The grep above finds `buddy\" gate`
	# in the old fail-open line and the new one alike, and anywhere in the file;
	# this decodes the JSON and reads each PreToolUse command that runs the gate
	# against README's entry (scripts/gate-hook.py holds it). Only a python3 can
	# decode it here, and install needs none for anything else, so without one
	# the line says it did not look. Advisory like the rest: settings unchanged.
	py=${BUDDY_PYTHON:-python3}
	if ! command -v "$py" >/dev/null 2>&1; then
		say "gate: not checked (no $py to decode $SETTINGS); README \"Hook wiring\" has the fail-closed entry (D-107)"
	elif gate=$("$py" -I "$ROOT/scripts/gate-hook.py" report "$SETTINGS" 2>/dev/null) && [ -n "$gate" ]; then
		say "$gate"
	else
		say "gate: not checked ($py could not run scripts/gate-hook.py); README \"Hook wiring\" has the fail-closed entry (D-107)"
	fi
fi

# 6b. Claims-only names what it found of the chat half and left alone (its
# posture line came first, before step 1). The posture describes this install,
# so a buddylist, a daemon plist or
# an MCP registration already on the machine is neither an error nor removed:
# a stale buddylist beside a NEW buddy is exactly the D-037 denial this script
# exists to prevent, and the operator should know it is there. The MCP check is
# a grep for the key, as step 7's is: a false "left alone" only says so.
if [ -n "$chat_off" ]; then
	left=""
	[ ! -e "$BIN_DIR/buddylist" ] || left="$left; buddylist at $BIN_DIR/buddylist"
	[ ! -e "$PLIST" ] || left="$left; the daemon agent's plist at $PLIST"
	for f in "$MCP_JSON" "$CLAUDE_JSON"; do
		if [ -f "$f" ] && grep -q '"buddylist"[[:space:]]*:' "$f"; then
			left="$left; a buddylist MCP server registered in $f"
		fi
	done
	[ -z "$left" ] || say "chat: left alone:${left#;} (chat off makes THIS install claims-only and says nothing about the rest of the machine; nothing was removed, stopped or unloaded)"
fi
# 6b'. Settings' BUDDY_CHAT is hello's knob (cli.go, EnvChat), never this
# script's: a chat machine whose operator wrote off there only to mute the hint
# would otherwise lose its daemon rebuild at the next schema bump, and the old
# buddylist then refuses the migrated ledger (D-037; the cross-check, D-124).
# A disagreement either way is said and acted on by neither. A grep, as step
# 7's is: a false note only says so.
if [ -f "$SETTINGS" ]; then
	sc=$(sed -n 's/.*"BUDDY_CHAT"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$SETTINGS" | head -n 1 |
		LC_ALL=C tr '[:upper:]' '[:lower:]' | LC_ALL=C sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
	if [ "$sc" = off ] && [ -z "$chat_off" ]; then
		say "chat: NOTE — $SETTINGS declares BUDDY_CHAT=off to every session (hello prints no chat hint), but this install is chat on; to make it claims-only: BUDDY_CHAT=off on a run"
	elif [ "$sc" != off ] && [ -n "$chat_off" ]; then
		say "chat: NOTE — this install is claims-only, but $SETTINGS does not declare BUDDY_CHAT=off, so every session's hello still prints the chat hint (README \"Claims-only\")"
	fi
fi

# 6c. The Agent-model hook (D-117), reported (D-120). scripts/hooks/
# agent-model-gate.py runs from a COPY the operator put in the bin dir and
# wired by hand, so a commit that changes the repo's script changed nothing any
# session runs, and nothing said so: the gap `buddy update` (D-110) closed for
# the binaries. Two lines: is the copy this checkout's bytes, and does settings
# wire a PreToolUse command naming it under a matcher that selects Agent.
# REPORT ONLY, never a copy and never an edit: the hook is the operator's opt-in
# (docs/CHEAPER-MODELS-PLAN.md 1.3), an operator who never took it is a valid
# state, and drift fails nothing. Byte-identical is the test (cmp), so a
# trailing-newline edit DIFFERS: the report cannot tell a harmless difference
# from a harmful one, and saying which file differs is all it promises.
#
# The wiring is decoded, not grepped: a matcher of `Bash` or `NotAgent` beside
# the command reads as wired to a grep. matcher_names is gate-hook.py's (D-107),
# loaded from its path, so one reading of a matcher serves both reports; -B,
# because importing it would otherwise write scripts/__pycache__ into the
# checkout, a file install.sh never wrote before. A command's script is known
# only in one shape, `python3 [flags] <script>` alone (CMD below); every other
# command is "not verified", never guessed. A wired path that does not exist is said loudly: python3 exits 2 on
# a missing script (measured, 2026-10-29), and exit 2 BLOCKS a PreToolUse call,
# so that wiring refuses every Agent call on the machine.
amg_wiring() { # amg_wiring <python> <settings> <copy> <checkout script> <gate-hook.py> <home>
	amg_p=$1; shift
	"$amg_p" -I -B - "$@" 2>/dev/null <<'PY'
import importlib.util
import json
import os
import re
import sys

settings_path, dst, src, gate_hook, home = sys.argv[1:]
NAME = "agent-model-gate.py"
spec = importlib.util.spec_from_file_location("gate_hook", gate_hook)
gh = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gh)


# Files that exist are compared by what the kernel resolves (samefile); a
# lexical normpath first collapsed `link/..` and called a different file the
# copy (Codex). Paths that do not both exist compare as spelled.
def same(a, b):
    try:
        return os.path.samefile(a, b)
    except OSError:
        return a == b


# The ONE command shape whose script is known: python3 (or an absolute
# python3), single-letter flags that take no argument, and the script's path,
# and nothing else. A token scan read `python3 -c pass <path>` and a
# single-quoted '$HOME/...' (a literal path) as running the copy, and a
# trailing `; exit 0` makes a missing script exit 0 (Codex): every other shape
# is "not verified", never guessed. $HOME may be bare or double-quoted, ~/
# only bare (the shell expands neither inside single quotes, nor ~ in double).
CMD = re.compile(
    r"\s*(?:python3?|/[A-Za-z0-9._/+-]*/python3(?:\.[0-9]+)?)(?:\s+-[IBEsSuOq]+)*\s+"
    r"(?P<q>[\"']?)(?P<pre>\$HOME/|\$\{HOME\}/|~/|/)(?P<rest>[A-Za-z0-9._/+@%,:=-]*)(?P=q)\s*")


def script_path(command):
    m = CMD.fullmatch(command)
    if not m:
        return None
    q, pre, rest = m.group("q", "pre", "rest")
    if not (rest == NAME or rest.endswith("/" + NAME)):
        return None
    if pre == "/":
        return "/" + rest
    if not home or q == "'" or (pre == "~/" and q):
        return None
    return home + "/" + rest


def state(p):
    if same(p, dst):
        what = "the copy above"
    elif same(p, src):
        what = "this checkout's own script: no copy to drift"
    elif not os.path.isfile(p):
        what = ""
    else:
        try:
            with open(p, "rb") as a, open(src, "rb") as b:
                what = "current" if a.read() == b.read() else "DIFFERS from this checkout's scripts/hooks/" + NAME
        except OSError:
            what = "not readable"
    if not os.path.isfile(p):
        what = (what + "; " if what else "") + (
            "MISSING: python3 exits 2 on a missing script, and exit 2 BLOCKS every Agent call")
    return what


def verdict():
    try:
        with open(settings_path, encoding="utf-8") as f:
            s = json.load(f)
    except (OSError, ValueError, RecursionError) as e:
        return "unread", "wiring not checked: %s is not JSON this report can read (%s)" % (settings_path, type(e).__name__)
    hooks = s.get("hooks") if isinstance(s, dict) else None
    pre = hooks.get("PreToolUse") if isinstance(hooks, dict) else None
    found = []
    for entry in pre if isinstance(pre, list) else []:
        if not isinstance(entry, dict) or not isinstance(entry.get("hooks"), list):
            continue
        for h in entry["hooks"]:
            if isinstance(h, dict) and isinstance(h.get("command"), str) and NAME in h["command"]:
                found.append((entry.get("matcher"), h))
    if not found:
        return "none", "not wired in %s" % settings_path
    if s.get("disableAllHooks") is True:
        return "off", "NOT in effect in %s: disableAllHooks is true" % settings_path
    live, custom, missed = [], [], []
    for m, h in found:
        names = gh.matcher_names(m)
        if names is None:
            custom.append("matcher %s not read as an exact list" % json.dumps(m))
            continue
        if names != "*" and "Agent" not in names:
            missed.append(json.dumps(m))
            continue
        why = [w for k, w in (("if", "an if rule narrows when it runs"), ("args", "exec form (args)"),
                              ("shell", "a shell is set")) if k in h]
        if h.get("type") != "command":
            why.append("type is not command")
        if h.get("async") is True or h.get("asyncRewake") is True:
            why.append("async")
        p = script_path(h["command"])
        if p is None:
            why.append("its command is not python3 [flags] <script> alone")
        if why:
            custom.extend(why)
        else:
            live.append("matcher %s runs %s (%s)" % (json.dumps(m), p, state(p)))
    if live:
        # A custom entry beside a wired one is named too: it may also select
        # Agent and run something else (Codex).
        also = "; and custom, not verified: %s" % "; ".join(custom) if custom else ""
        return "wired", "wired in %s: %s%s" % (settings_path, "; ".join(live), also)
    if custom:
        return "custom", "custom PreToolUse entry naming %s in %s (%s); not verified" % (NAME, settings_path, "; ".join(custom))
    return "off", "NOT wired in %s: the PreToolUse entry naming %s has matcher %s, which does not select Agent" % (
        settings_path, NAME, ", ".join(missed))


kind, text = verdict()
print(kind + " " + " ".join(text.split()))
PY
}
amg_src=$ROOT/scripts/hooks/agent-model-gate.py
amg_dst=$BIN_DIR/agent-model-gate.py
case $amg_dst in /*) ;; *) amg_dst=$ROOT/$amg_dst ;; esac
amg_cp="cp $(shq "$amg_src") $(shq "$amg_dst")"
amg_why="optional, the operator's opt-in: docs/CHEAPER-MODELS-PLAN.md 1.3, D-117"
amg_copy=""
if [ ! -e "$amg_dst" ] && [ ! -L "$amg_dst" ]; then
	:
elif [ -L "$amg_dst" ] && [ ! -e "$amg_dst" ]; then
	# cp writes THROUGH a symlink into whatever it names, so every remedy for
	# a link replaces the link.
	amg_copy="$amg_dst is a symlink to nothing ($(readlink "$amg_dst" || true)); replace the link: rm $(shq "$amg_dst") && $amg_cp"
elif [ ! -f "$amg_dst" ]; then
	amg_copy="$amg_dst is not a regular file; move it aside, then: $amg_cp"
elif cmp -s "$amg_src" "$amg_dst"; then
	amg_copy="$amg_dst is current (byte-identical to this checkout's scripts/hooks/agent-model-gate.py)"
	[ ! -L "$amg_dst" ] || amg_copy="$amg_copy, through a symlink to $(readlink "$amg_dst" || true)"
elif [ -L "$amg_dst" ]; then
	amg_copy="$amg_dst DIFFERS from this checkout's scripts/hooks/agent-model-gate.py; it is a symlink to $(readlink "$amg_dst" || true), so replace the link, not what it names: rm $(shq "$amg_dst") && $amg_cp"
else
	amg_copy="$amg_dst DIFFERS from this checkout's scripts/hooks/agent-model-gate.py, and a hook that runs it runs those bytes; update it: $amg_cp"
fi
amg_py=${BUDDY_PYTHON:-python3}
if [ ! -f "$SETTINGS" ]; then
	amg_kind=none amg_wire="not wired in $SETTINGS"
elif ! command -v "$amg_py" >/dev/null 2>&1; then
	amg_kind=unread amg_wire="wiring not checked (no $amg_py to decode $SETTINGS)"
elif amg_v=$(amg_wiring "$amg_py" "$SETTINGS" "$amg_dst" "$amg_src" "$ROOT/scripts/gate-hook.py" "${HOME:-}") && [ -n "$amg_v" ]; then
	amg_kind=${amg_v%% *} amg_wire=${amg_v#* }
else
	amg_kind=unread amg_wire="wiring not checked ($amg_py could not read $SETTINGS)"
fi
if [ -z "$amg_copy" ] && [ "$amg_kind" = none ]; then
	say "agent-model hook: not installed at $amg_dst and $amg_wire ($amg_why)"
else
	say "agent-model hook: ${amg_copy:-not installed at $amg_dst ($amg_why; to install: $amg_cp)}"
	say "agent-model hook: $amg_wire"
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
