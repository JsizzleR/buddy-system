#!/bin/sh
# uninstall.sh — remove what install.sh put on this machine, or only its chat
# half, and say what it leaves (D-125).
#
#   sh scripts/uninstall.sh [--chat] [--dry-run]
#   buddy uninstall [--chat] [--dry-run]   (from anywhere: runs this script in
#                                           the checkout buddy was built from)
#
# WHY A VERB AND NOT A KNOB. install.sh's claims-only knob describes one
# invocation and removes nothing, because an install that uninstalled on a
# knob could remove the chat half by a typo in a settings env block. Removal
# is this script, asked for by name.
#
# WHY A SCRIPT. install.sh finds what it installs in shell, from knobs: the
# daemon's program from its plist, the checkout's own bin/, the skill names
# from skills/*. The inverse finds them the same way from the same knobs, so
# the done-check (check-uninstall.sh) installs and uninstalls under one
# environment, and it runs when the installed buddy cannot (D-107: a dead
# buddy refuses every gated call, and removing it is one of the fixes).
#
# TWO MODES.
#   full     the chat half (1, 2), the skills (3), then the claims half (4).
#   --chat   the chat half only, and the install stays claims-only from then on.
#            The posture is chat-sticks' stamp in the installed buddy (D-124:
#            -X …/internal/cli.installedChat=off, read back by `go version -m`),
#            and only a build can change a stamp, so this runs the installed
#            buddy's own `buddy update` with BUDDY_CHAT=off FIRST: update's
#            downgrade refusal applies (D-110), install.sh rebuilds buddy alone
#            beside-then-rename and leaves every chat remnant alone, and the
#            stamp is read back. Chat is removed only once it reads off. The
#            other order would leave chat removed and a buddy stamped on, whose
#            next update rebuilds buddylist. A claims-only reinstall refreshes
#            the skills as every install does (install-skill.sh's .bak rule).
#   --dry-run  prints the plan and the reports, removes nothing, asks
#            launchctl only `print`, and under --chat runs `buddy update
#            --dry-run` (read-only by its own contract) for update's verdict.
#
# THE ORDER IS THE SAFETY ARGUMENT. Every step skips what is absent, so a run
# that stops anywhere is finished by running it again. The claims half goes
# LAST: until then a stop leaves buddy installed and working, and chat half
# gone is harmless (chat hooks fail silent, invariant 2). Each buddy is
# removed by one unlink, never truncated or copied over, so there is no
# instant at which a PRESENT buddy cannot run (D-107); the checkout's bin/
# copy goes before the hooks' copy, so a stop between them leaves the hooks'
# buddy working. What a session sees during the unlink: a hook that passed
# README's `[ -x "$HOME/bin/buddy" ]` just before and execs just after gets
# ENOENT, and the gate's line refuses that ONE call; every later call finds no
# buddy and the feature is off. Claims stay in every ledger (invariant 11).
#
# NOTHING IS EXECUTED TO IDENTIFY IT (the design pass). A file is this
# project's when its bytes hold Go's build-info line `path\t<module>/cmd/<name>`
# as a whole line: an exit status or a usage string proves nothing about who
# wrote a program, and running a foreign `buddylist` could start a daemon. A
# buddy whose ad-hoc signature a cp broke keeps those bytes and is removed; a
# zero-byte or foreign file is left and named with the command that removes it.
#
# THE DAEMON GOES THROUGH LAUNCHD ONLY (D-122: never kill it; KeepAlive
# respawns, and the loser of the socket race dies). The service is asked for by
# its label whether or not a plist exists, since a loaded job outlives its
# deleted plist: `print` 0 is loaded (its program must be a buddylist, else it
# is not ours and is left), 113 is launchd's "not found", anything else is
# UNCERTAIN and the run stops before removing anything: a loaded KeepAlive job
# whose program vanished respawn-fails for good. After `bootout` the label must
# read 113 within ten seconds. The plist was written by hand, not by install.sh,
# so it is MOVED out of LaunchAgents (to ~/.buddylist), never deleted: left in
# place it is RunAtLoad at the next login on a program that is gone.
#
# WHAT IT ALSO UNDOES, BY DEFAULT (D-173: the operator, 2026-11-11, "yes open
# the lane, default-on", over a --chat dry run that left five steps by hand):
#   - the buddy hook lines for what this run removes, in the settings file:
#     buddylist's two on --chat, every buddy line on a full run, and only when
#     the binary a line runs ($HOME/bin/<it>) is gone after the run. The edit
#     is wire-hooks.py's `unwire`: gate-hook.py's ONE table read in reverse
#     (D-135), a verified text splice, a 0600 backup, a same-directory temp
#     renamed over the file, --wire-hooks' refusals. It runs LAST, after buddy's
#     unlink, so a stop anywhere before leaves buddy installed AND its gate
#     wired. A refused edit is named and the run goes on: every README line is
#     guarded ([ -x ] ... exit 0), so a line left over a binary that is gone
#     runs nothing, while stopping would leave the binaries a refused edit had
#     nothing to do with. An entry holding a buddy line beside anything else is
#     left whole and named; so is a line in any other spelling. Never the
#     Agent-model hook's line (D-120): it is not install's.
#   - the buddylist MCP registrations whose command is a buddylist this run
#     removes: `claude mcp remove buddylist --scope local` run inside each
#     project ~/.claude.json names (only where that path is the directory's
#     physical one, which is what claude keys it by), `--scope user` for the
#     user-wide one. ~/.claude.json is read, never written: claude writes it.
#     Each removal is read back. A registration running anything else is left
#     and named; with no claude to run, every command is named as before.
#   - com.buddy-system.oscar, the AIM server README's TOC backend runs, when it
#     is provably buddy's: the Label, AND a program whose bytes carry
#     open-oscar-server's cmd/server build path and get-oscar.sh's pinned
#     vcs.revision (the job's program and the plist's both, when present).
#     Booted out as the daemon is (print 0, bootout, print 113 within 10 s) and
#     its plist MOVED to ~/.buddylist with the restore command. Anything less
#     is left and named, and never stops the run: nothing removed here runs it.
#     com.buddy-system.ergo is never provable: its plist was written by hand
#     (2026-08-14, before the public tree) for an ergo build no doc of ours
#     names (README says only `ergo run`), so it is left and named.
#
# NEVER TOUCHED, REPORTED: settings outside those lines (BUDDY_CHAT in env
# included), the checkout's .mcp.json, Codex's hooks.json (Codex's file, its
# trust keyed to its contents by Codex's /hooks review: an edit here would be a
# change of trust this never makes), a pre-commit holding README's commit-gate
# line (not enumerable: any repo's .git/hooks), core.hooksPath (who set it
# cannot be known here), every ledger and the chat journal (data; a --purge is
# out of scope), SKILL.md.bak and any installed skill file (a SKILL.md or,
# D-171, a reference file beside it) that is not byte-identical to this
# checkout's (an edit, or another release's), a file in a skill directory this
# checkout does not ship (the directory is then left too), the Agent-model
# hook copy (D-120), other com.buddy-system.* launchd agents, a buddylist
# process (never signalled: after the bootout they are live sessions' MCP
# servers, D-122), and any file not identified as ours.
#
# ONE LOCK WITH install.sh (D-126): an install STARTED after this one's checks
# republished what it removed (D-125's Open), so both hold
# $BUDDY_BIN_DIR/.buddy-install.lock for their whole run, and either stops,
# changing nothing, while the other holds it (scripts/install-lock.sh). --chat's
# own `buddy update` runs inside this run's lock, named to it by
# BUDDY_INSTALL_LOCK_HELD, and is handed the lock if this run ends first. A
# dry run takes no lock and says when one is held.
# An install from a checkout older than D-126 takes none, so what was refused
# before still is: an install-skill lock or a `<target>.new-<pid>` build
# beside a target, checked first and again just before buddy goes.
#
# KNOBS: install.sh's, with the same defaults: BUDDY_BIN_DIR, BUDDY_SKILL_DIR,
# BUDDY_FLEET_SKILL_DIR, BUDDY_PLIST, BUDDY_LAUNCHCTL, BUDDY_REPO_BIN=off,
# BUDDY_SETTINGS, BUDDY_CLAUDE_JSON, BUDDY_MCP_JSON, BUDDY_PYTHON; and
# BUDDY_CLAUDE, the claude it runs `mcp remove` with (default: claude on PATH),
# so the done-check can name a fake as it names launchctl's. Every other path
# it reports is under $HOME (~/.buddylist, ~/.codex) or beside $BUDDY_PLIST
# (the oscar agent), which the done-check moves.
set -eu
CDPATH= cd -- "$(dirname -- "$0")/.."
NL='
'
# A substitution drops trailing newlines, so a checkout at "/x/co<NL>" read as
# /x/co, a sibling's, before the newline guard below saw it (Codex, re-verify).
ROOT=$(pwd && echo .)
ROOT=${ROOT%.}
ROOT=${ROOT%"$NL"}
# The reports read this checkout's git; an inherited GIT_DIR (a hook's, a
# linked worktree's) would name another (D-057).
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_COMMON_DIR GIT_OBJECT_DIRECTORY

usage="usage: sh scripts/uninstall.sh [--chat] [--dry-run]   (remove what install.sh installed; --chat the chat half only, keeping claims; --dry-run prints the plan and removes nothing)"
chat_only="" dry=""
for a in "$@"; do
	case $a in
	--chat) chat_only=1 ;;
	--dry-run) dry=1 ;;
	-h | --help | help) echo "$usage"; exit 0 ;;
	*) printf '%s\nuninstall: refused: unknown argument %s\n' "$usage" "$a" >&2; exit 2 ;;
	esac
done

BIN_DIR=${BUDDY_BIN_DIR:-$HOME/bin}
case $BIN_DIR in /*) ;; *) BIN_DIR=$ROOT/$BIN_DIR ;; esac
SETTINGS=${BUDDY_SETTINGS:-$HOME/.claude/settings.json}
AGENT=com.buddy-system.buddylistd
PLIST=${BUDDY_PLIST:-$HOME/Library/LaunchAgents/$AGENT.plist}
LAUNCHCTL=${BUDDY_LAUNCHCTL:-launchctl}
CLAUDE=${BUDDY_CLAUDE:-claude}
PY=${BUDDY_PYTHON:-python3}
OSCAR=com.buddy-system.oscar
CLAUDE_JSON=${BUDDY_CLAUDE_JSON:-$HOME/.claude.json}
MCP_JSON=${BUDDY_MCP_JSON:-$ROOT/.mcp.json}
SKILL_DIR=${BUDDY_SKILL_DIR:-$HOME/.claude/skills/buddy}
FLEET_DIR=${BUDDY_FLEET_SKILL_DIR:-$(dirname -- "$SKILL_DIR")/buddy-fleet}
STATE_DIR=$HOME/.buddylist
MODULE=github.com/JsizzleR/buddy-system
TAB=$(printf '\t')

say() { echo "uninstall: $*"; }
die() { echo "uninstall: STOPPED — $*" >&2; exit 1; } # redefined below, once the lists are set up
shq() {
	case $1 in
	'' | *[!A-Za-z0-9._/:@%+=,-]*) printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")" ;;
	*) printf '%s' "$1" ;;
	esac
}
mode=full
[ -z "$chat_only" ] || mode="chat half only"
[ -z "$dry" ] || mode="$mode, DRY RUN: nothing is removed"
say "from $ROOT ($mode)"

# ours <file> <cmd>: the file's bytes carry cmd/<cmd>'s build-info path line.
# Dots escaped; the trailing $ keeps cmd/buddy from matching cmd/buddylist.
ours() {
	[ -f "$1" ] && LC_ALL=C grep -a -q "^.*path${TAB}github\\.com/JsizzleR/buddy-system/cmd/$2\$" "$1"
}

# Skill destinations, install-skill.sh's dest().
skill_names=""
for s in "$ROOT"/skills/*/SKILL.md; do
	[ -f "$s" ] || continue
	# By expansion, not basename: a substitution drops a trailing newline.
	n=${s%/SKILL.md}
	n=${n##*/}
	case $n in *"$NL"*) die "a skill directory's name holds a newline ($(printf '%s' "$n" | tr '\n' '?')); nothing was removed." ;; esac
	skill_names="$skill_names$NL$n"
done

# FROM HERE every list is one path per LINE, split on newlines only and never
# globbed: a checkout under "My Code" split on its space planned none of its
# bin/ copies, and one under "bs[12]" globbed into OTHER clones' bin/buddy
# (the panel; install-skill.sh met the same bug). A glob below turns globbing
# back on for its one loop.
set -f
IFS=$NL
# So a path holding a newline is refused before anything is planned: split
# here, BUDDY_FLEET_SKILL_DIR="/x/fleet<NL>victim" planned one checked path
# and removed two unchecked ones (Codex, code pass). Skill names are checked
# as they are read, above.
for p in "$ROOT" "$BIN_DIR" "$SKILL_DIR" "$FLEET_DIR" "$PLIST"; do
	case $p in *"$NL"*) die "a path to act on holds a newline ($(printf '%s' "$p" | tr '\n' '?')); nothing was removed. Set the BUDDY_* knobs to paths without one." ;; esac
done

# flipped: --chat has rebuilt buddy claims-only. Every stop after that says so,
# since chat is then still running and no later install rebuilds it (the panel).
flipped=""
die() {
	echo "uninstall: STOPPED — $*" >&2
	[ -z "$flipped" ] || echo "uninstall: NOTE — $BIN_DIR/buddy is already rebuilt claims-only (chat off), so chat is still installed and RUNNING and no later install rebuilds its buddylist: run this again once that is fixed, or put chat back: BUDDY_CHAT=on sh <checkout>/scripts/install.sh" >&2
	exit 1
}

# The install lock (D-126; see the header), taken before launchd is asked or
# anything is planned, and held to the end. A missing bin dir is made for it
# and removed again at exit if still empty: with no directory there is no
# lock, and an install creates the directory before it takes one.
. "$ROOT/scripts/install-lock.sh"
LOCK=$BIN_DIR/.buddy-install.lock
made_bin=""
if [ -n "$dry" ]; then
	ilock_peek "$LOCK"
else
	if [ ! -d "$BIN_DIR" ]; then
		mkdir -p -- "$BIN_DIR" || die "could not create $BIN_DIR to hold the install lock in; nothing was removed."
		made_bin=1
	fi
	trap 'ilock_release; [ -z "$made_bin" ] || rmdir -- "$BIN_DIR" 2>/dev/null || true' EXIT
	ilock_signals
	ilock_take uninstall.sh "$LOCK"
fi

dest() {
	case $1 in
	buddy) printf '%s\n' "$SKILL_DIR" ;;
	buddy-fleet) printf '%s\n' "$FLEET_DIR" ;;
	*) printf '%s/%s\n' "$(dirname -- "$SKILL_DIR")" "$1" ;;
	esac
}
skill_dests=""
for n in $skill_names; do skill_dests="$skill_dests$NL$(dest "$n")"; done

# in_flight <target>...: refuse while an install is mid-way (see the header).
in_flight() {
	for t in "$@"; do
		set +f
		for n in "$t".new-*; do
			[ -e "$n" ] || continue
			# install.sh names its build <target>.new-<its pid>: a live pid is an
			# install in flight; a dead one left it behind when it was killed
			# (its cleanup runs only on its own failures), and refusing on it
			# would lock uninstall out for good (the Fable lens).
			pid=${n##*.new-}
			case $pid in '' | *[!0-9]*) pid="" ;; esac
			if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
				die "an install is in flight ($n exists and pid $pid is alive); let it finish, then run this again. Nothing more was removed."
			fi
			say "left: $n, a build a killed install left behind; remove it by hand: rm $(shq "$n")"
		done
		set -f
	done
	for d in $skill_dests; do
		[ ! -d "$d/.install-skill.lock" ] || die "an install holds $d/.install-skill.lock; if none is running, remove it and run this again. Nothing more was removed."
	done
}

repo_bin=1
[ "${BUDDY_REPO_BIN:-}" != off ] || repo_bin=""

# 1. The daemon's service, by label, read BEFORE --chat changes anything, so
# an uncertain launchd stops a run that has done nothing (the panel). A
# missing launchctl is no launchd to ask only off macOS with no plist: on
# macOS, or beside a plist, it is UNCERTAIN, since skipping the bootout there
# removed the program under a loaded KeepAlive job (Codex, code pass).
# gone_for_sure <path>: absent, and not merely unreadable. `[ ! -e ]` is also
# true on EACCES, so a foreign program in a directory this user cannot search
# read as gone and its job was booted out (Codex, re-verify): the deepest
# ancestor that exists must be a directory this user can search. The walk
# stops at a symlink too, which must then resolve: one into an unsearchable
# directory fails `-e` like a missing entry, and climbing past it read a
# foreign program as gone (Codex, second re-verify).
gone_for_sure() {
	[ ! -e "$1" ] && [ ! -L "$1" ] || return 1
	g=$(dirname -- "$1")
	while [ ! -e "$g" ] && [ ! -L "$g" ]; do g=$(dirname -- "$g"); done
	[ -e "$g" ] && [ -d "$g" ] && [ -x "$g" ]
}
uid=$(id -u)
svc=absent svc_prog=""
if ! command -v "$LAUNCHCTL" >/dev/null 2>&1; then
	if [ "$(uname -s)" = Darwin ] || [ -f "$PLIST" ]; then
		die "no launchctl at $LAUNCHCTL, so whether the chat daemon is loaded cannot be said; nothing was removed."
	fi
else
	job=$(LC_ALL=C "$LAUNCHCTL" print "gui/$uid/$AGENT" 2>/dev/null) && prc=0 || prc=$?
	case $prc in
	0)
		svc_prog=$(printf '%s\n' "$job" | LC_ALL=C awk '/^\tprogram = /{sub(/^\tprogram = /, ""); print; exit}')
		# No program line is a print format this cannot read: UNCERTAIN, not a
		# foreign job (the Fable cross-check).
		[ -n "$svc_prog" ] || die "launchctl print gui/$uid/$AGENT names no program, so whose job it is cannot be said; nothing was removed."
		# Ours by the program's bytes, as every file here: a foreign
		# /x/buddylist under this label was booted out on its basename alone
		# (Codex, code pass). A program already gone is a job respawn-failing
		# under this project's label, and booting it out is the fix.
		case $svc_prog in
		*/buddylist) if ours "$svc_prog" buddylist || gone_for_sure "$svc_prog"; then svc=loaded; else svc=foreign; fi ;;
		*) svc=foreign ;;
		esac
		;;
	113) ;;
	*) die "launchctl print gui/$uid/$AGENT exited $prc, so whether the chat daemon is loaded cannot be said; nothing was removed (a loaded KeepAlive job whose program is removed respawn-fails for good)." ;;
	esac
fi
# pb <key> <plist>: PlistBuddy's value into pb_val, its own newline off and
# every other byte kept: a substitution dropped a value's trailing newline,
# so a Label "<label><NL>" read as the label (Codex, D-173's code pass).
pb() {
	pb_val=$(/usr/libexec/PlistBuddy -c "Print :$1" "$2" 2>/dev/null && echo x) || { pb_val=""; return 0; }
	pb_val=${pb_val%x}
	pb_val=${pb_val%"$NL"}
}
# pb_exec <plist>: the program launchd runs: `Program` when the plist sets
# one (ProgramArguments[0] is then only argv[0]; Codex, D-173), else
# ProgramArguments[0].
pb_exec() {
	pb Program "$1"
	[ -n "$pb_val" ] || pb ProgramArguments:0 "$1"
}
plist_prog="" plist_ok="" plist_other=""
if [ -f "$PLIST" ] && [ -x /usr/libexec/PlistBuddy ]; then
	pb Label "$PLIST"
	plabel=$pb_val
	pb_exec "$PLIST"
	plist_prog=$pb_val
	case $plist_prog in
	*"$NL"*) ;; # a program path with a newline is no install's (see above)
	*/buddylist) if [ "$plabel" = "$AGENT" ]; then plist_ok=1; else plist_other=${plabel:-(none)}; fi ;;
	esac
fi
# NOT OURS TO JUDGE: a job under the agent's label that runs something else
# (a wrapper may exec a buddylist), or a plist naming a buddylist under
# ANOTHER label (a job launchd knows by a name this never asks). Removing a
# buddylist could then leave a loaded KeepAlive job respawn-failing, so every
# buddylist copy is left with the job and the plist (the panel).
chat_left=""
if [ "$svc" = foreign ] || [ -n "$plist_other" ]; then
	chat_left=1 plist_ok=""
fi

# The buddylist candidates: the hooks', the daemon's (from its plist or the
# loaded job, only when they are ours), the checkout's. Deduplicated.
lists=""
add_list() { case "$NL$lists$NL" in *"$NL$1$NL"*) ;; *) lists="$lists$NL$1" ;; esac; }
if [ -z "$chat_left" ]; then
	add_list "$BIN_DIR/buddylist"
	[ -z "$plist_ok" ] || add_list "$plist_prog"
	[ "$svc" != loaded ] || add_list "$svc_prog"
	[ -z "$repo_bin" ] || add_list "$ROOT/bin/buddylist"
fi
buddies=""
[ -n "$chat_only" ] || buddies="${repo_bin:+$ROOT/bin/buddy$NL}$BIN_DIR/buddy"
in_flight "$BIN_DIR/buddy" $lists $buddies

# 0. --chat: the posture, before anything is removed (see the header).
if [ -n "$chat_only" ]; then
	# Env beats the stamp (D-124): with BUDDY_CHAT exported and not off, the
	# next install or update here rebuilds chat, so a --chat that ended
	# "done" would be false (the Fable cross-check). Stopped before anything.
	c=$(printf '%s' "${BUDDY_CHAT:-}" | LC_ALL=C tr '[:upper:]' '[:lower:]' | LC_ALL=C sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
	[ -z "$c" ] || [ "$c" = off ] ||
		die "BUDDY_CHAT=${BUDDY_CHAT} is exported here, and an exported BUDDY_CHAT overrides the posture stamp: the next install or buddy update run with it builds chat again. Unset it (and any settings env that sets it), then run this again. Nothing was removed."
	ours "$BIN_DIR/buddy" buddy ||
		die "no buddy of this project at $BIN_DIR/buddy, so nothing records a claims-only posture for buddy update to keep. Install claims-only first (BUDDY_CHAT=off sh $(shq "$ROOT/scripts/install.sh")), then run this again. Nothing was removed."
	# The stamp is read the way install.sh reads it (D-124): from the FILE's
	# build info, never by running it, at the start of -ldflags.
	posture_of() {
		command -v go >/dev/null 2>&1 || return 0
		go version -m "$1" 2>/dev/null |
			LC_ALL=C sed -n -E "s#^[[:space:]]*build[[:space:]]+-ldflags=\"-X '$MODULE/internal/cli\\.installedChat=(on|off)'.*#\\1#p" | head -n 1
	}
	if [ -n "$dry" ]; then
		# update's own dry run installs nothing by contract, and says now the
		# likeliest stop: its downgrade refusal, or a stamped checkout that is
		# gone (the Fable lens).
		say "would run first: BUDDY_CHAT=off $(shq "$BIN_DIR/buddy") update (a claims-only reinstall, so chat stays off through every later install and update), and remove chat only once $(shq "$BIN_DIR/buddy") records chat off. Its dry run says:"
		env BUDDY_CHAT=off "$BIN_DIR/buddy" update --dry-run </dev/null && rc=0 || rc=$?
		[ "$rc" -eq 0 ] || die "buddy update --dry-run exited $rc, so --chat would stop there; nothing was removed."
	else
		say "running: BUDDY_CHAT=off $(shq "$BIN_DIR/buddy") update"
		# Inside this run's lock: the install it runs would otherwise stop on it.
		env BUDDY_CHAT=off BUDDY_INSTALL_LOCK_HELD="$ILOCK_ME" "$BIN_DIR/buddy" update </dev/null && rc=0 || rc=$?
		# install.sh renames the new buddy in before its later steps (skills,
		# setup-clone), so a failed update can already have stamped it off
		# while chat still runs (the Fable lens): read, never assumed.
		[ "$(posture_of "$BIN_DIR/buddy")" != off ] || flipped=1
		[ "$rc" -eq 0 ] || die "buddy update exited $rc; nothing of chat was removed. Fix what it said, then run this again."
		case $(posture_of "$BIN_DIR/buddy") in
		off) ;;
		on) die "$BIN_DIR/buddy still records chat on after the update (it reads 'on'), so a later buddy update would rebuild chat; nothing of chat was removed." ;;
		*) die "$BIN_DIR/buddy records no chat posture after the update (it reads 'nothing': a checkout older than D-124 stamps none, a build under GOFLAGS=-trimpath records no -ldflags (measured), or no go toolchain reads it), so a later buddy update would rebuild chat; nothing of chat was removed." ;;
		esac
		say "posture: $BIN_DIR/buddy records chat off; every later install and buddy update keeps it until a run says BUDDY_CHAT=on"
	fi
fi

# The plan. Every line says remove, absent or left, and why; rm_lists,
# rm_skills and rm_buddies are exactly what the act removes.
w="will remove"
[ -z "$dry" ] || w="would remove"
case $svc in
loaded) say "daemon: $w the launchd job $AGENT (launchctl bootout; it runs $svc_prog)" ;;
foreign) say "daemon: left: the loaded job $AGENT runs $svc_prog, which is not a buddylist; booting it out is yours: launchctl bootout gui/$uid/$AGENT" ;;
*) say "daemon: no launchd job $AGENT loaded" ;;
esac
[ -z "$plist_other" ] || say "daemon: left: $PLIST names $plist_prog under the Label $plist_other, a job this does not ask launchd about; booting it out is yours: launchctl bootout gui/$uid/$plist_other"
[ -z "$chat_left" ] || say "buddylist: left: every buddylist copy, since a launchd job not judged here may run one (see above); remove them by hand once it is booted out"
if [ -n "$plist_ok" ]; then
	say "daemon: $w its plist $PLIST from LaunchAgents (moved to $STATE_DIR, not deleted: it was written by hand)"
elif [ -f "$PLIST" ] && [ -z "$plist_other" ]; then
	say "daemon: left: $PLIST (its program is not a buddylist, or it cannot be read here)"
fi
rm_lists="" rm_skills="" rm_skill_dirs="" rm_buddies=""
plan_bin() { # plan_bin <path> <cmd>: prints the line; status 0 when it goes
	if [ -L "$1" ] && [ ! -e "$1" ]; then
		say "$2: left: $1 is a symlink to nothing; remove it by hand: rm $(shq "$1")"
	elif [ ! -e "$1" ]; then
		:
	elif ours "$1" "$2"; then
		say "$2: $w $1"
		return 0
	else
		say "$2: left: $1 is not this project's $2 (its bytes carry no build-info path $MODULE/cmd/$2); if it is yours to remove: rm $(shq "$1")"
		[ "$2" != buddylist ] || chat_left=1
	fi
	return 1
}
for b in $lists; do ! plan_bin "$b" buddylist || rm_lists="$rm_lists$NL$b"; done
if [ -z "$chat_only" ]; then
	# Every file the checkout ships for a skill (install-skill.sh's files():
	# its regular, non-dot files; D-171), SKILL.md first so the skill stops
	# loading before its reference files go. Names come from the checkout,
	# never from the destination, so nothing this checkout does not ship is
	# planned; the manifest install-skill.sh keeps goes too, and then the
	# directory if it is empty.
	for n in $skill_names; do
		d=$(dest "$n")
		fl=SKILL.md
		set +f
		for s in "$ROOT/skills/$n"/*; do
			b=${s##*/}
			case $b in SKILL.md | *[!A-Za-z0-9._-]*) continue ;; esac
			[ -f "$s" ] && [ ! -L "$s" ] || continue
			fl="$fl$NL$b"
		done
		set -f
		for b in $fl; do
			if [ -L "$d/$b" ] || { [ -e "$d/$b" ] && [ ! -f "$d/$b" ]; }; then
				say "skill: left: $d/$b is a symlink or not a regular file"
			elif [ ! -e "$d/$b" ]; then
				:
			elif cmp -s "$ROOT/skills/$n/$b" "$d/$b"; then
				say "skill: $w $d/$b"
				rm_skills="$rm_skills$NL$d/$b"
			else
				say "skill: left: $d/$b differs from this checkout's skills/$n/$b (an edit, or another release's copy); if it is yours to remove: rm $(shq "$d/$b")"
			fi
			[ ! -e "$d/$b.bak" ] || say "skill: left: $d/$b.bak (an earlier copy install-skill.sh kept, perhaps your edit)"
		done
		if [ -f "$d/.install-skill.files" ] && [ ! -L "$d/.install-skill.files" ]; then
			say "skill: $w $d/.install-skill.files (install-skill.sh's list of what it installed there)"
			rm_skills="$rm_skills$NL$d/.install-skill.files"
		fi
		rm_skill_dirs="$rm_skill_dirs$NL$d"
	done
	# The checkout's copy before the hooks' (the order $buddies holds): a stop
	# between them leaves the hooks' buddy working, and a ~/bin/buddy that is a
	# symlink to the checkout's is unlinked after its target, as planned.
	for b in $buddies; do ! plan_bin "$b" buddy || rm_buddies="$rm_buddies$NL$b"; done
fi

# 1b. The AIM server's agent (see the header). Asked by label, as the daemon
# is, but nothing here runs its program, so an answer this cannot read leaves
# it, named, and never stops the run.
OSCAR_PLIST=$(dirname -- "$PLIST")/$OSCAR.plist
pin=$(LC_ALL=C sed -n 's/^PIN=\([0-9a-f]\{40\}\)$/\1/p' "$ROOT/scripts/get-oscar.sh" 2>/dev/null | head -n 1)
oscar_ours() {
	[ -n "$pin" ] && [ -f "$1" ] &&
		LC_ALL=C grep -a -q "^.*path${TAB}github\\.com/mk6i/open-oscar-server/cmd/server\$" "$1" &&
		LC_ALL=C grep -a -q "^.*build${TAB}vcs\\.revision=$pin\$" "$1"
}
osc=absent osc_prog="" osc_why=""
if command -v "$LAUNCHCTL" >/dev/null 2>&1; then
	job=$(LC_ALL=C "$LAUNCHCTL" print "gui/$uid/$OSCAR" 2>/dev/null) && prc=0 || prc=$?
	case $prc in
	0)
		osc_prog=$(printf '%s\n' "$job" | LC_ALL=C awk '/^\tprogram = /{sub(/^\tprogram = /, ""); print; exit}')
		if [ -n "$osc_prog" ] && oscar_ours "$osc_prog"; then
			osc=loaded
		else
			osc=left osc_why="its loaded job runs ${osc_prog:-no program print names}, whose bytes are not the pinned server get-oscar.sh builds"
		fi
		;;
	113) ;;
	*) osc=left osc_why="launchctl print gui/$uid/$OSCAR exited $prc, so whether it is loaded cannot be said" ;;
	esac
fi
osc_plist=""
if [ -f "$OSCAR_PLIST" ] || [ -L "$OSCAR_PLIST" ]; then
	op_label="" op_prog=""
	if [ ! -L "$OSCAR_PLIST" ] && [ -x /usr/libexec/PlistBuddy ]; then
		pb Label "$OSCAR_PLIST"
		op_label=$pb_val
		pb_exec "$OSCAR_PLIST"
		op_prog=$pb_val
	fi
	case $op_prog in *"$NL"*) op_prog="" ;; esac
	if [ "$op_label" = "$OSCAR" ] && [ -n "$op_prog" ] && oscar_ours "$op_prog"; then
		osc_plist=1
	elif [ "$osc" != left ]; then
		osc=left osc_why="its plist $OSCAR_PLIST is a symlink, cannot be read here, or names ${op_prog:-no program} under the Label ${op_label:-(none)}, not the pinned server under $OSCAR"
	fi
fi
if [ "$osc" = left ]; then
	say "chat server: left: $OSCAR, since $osc_why; to stop it: launchctl bootout gui/$uid/$OSCAR, then move $(shq "$OSCAR_PLIST") out of LaunchAgents"
else
	[ "$osc" != loaded ] || say "chat server: $w the launchd job $OSCAR (launchctl bootout; it runs $osc_prog, the server get-oscar.sh pins)"
	[ -z "$osc_plist" ] || say "chat server: $w its plist $OSCAR_PLIST from LaunchAgents (moved to $STATE_DIR, not deleted)"
fi

# 1c. MCP registrations in ~/.claude.json, decoded (the Fable lens, D-125):
# one line per registration, its scope, its project, and whether its command
# is a buddylist this run removes. A path with a control character is never
# printed raw (invariant 9) and never acted on.
MCP_PY='
import json, os, sys
mode, path, rest = sys.argv[1], sys.argv[2], sys.argv[3:]
try:
    d = json.load(open(path, encoding="utf-8"))
except (OSError, ValueError, RecursionError):
    sys.exit(1)
if not isinstance(d, dict):
    sys.exit(1)
def reg(v):
    m = v.get("mcpServers") if isinstance(v, dict) else None
    return m.get("buddylist") if isinstance(m, dict) else None
def at(scope, p):
    pr = d.get("projects")
    return d if scope == "user" else pr.get(p) if isinstance(pr, dict) else None
if mode == "has":
    sys.exit(0 if reg(at(rest[0], rest[1])) is not None else 3)
if mode == "still":
    # 0: still the registration the plan approved, and its program gone; 4: it
    # changed since; 5: its program is still there.
    r = reg(at(rest[0], rest[1]))
    cmd = r.get("command") if isinstance(r, dict) else None
    if json.dumps(cmd) != rest[2]:
        sys.exit(4)
    sys.exit(5 if os.path.lexists(cmd) else 0)
regs = [("user", "-", reg(d))] if reg(d) is not None else []
pr = d.get("projects")
for p, v in (pr.items() if isinstance(pr, dict) else []):
    if reg(v) is not None:
        regs.append(("local", p, reg(v)))
for scope, p, r in regs:
    if any(ord(c) < 32 or ord(c) == 127 for c in p):
        print("odd\t%s\t-\t-" % scope)
        continue
    cmd = r.get("command") if isinstance(r, dict) else None
    ours = isinstance(cmd, str) and os.path.isabs(cmd) and cmd in [x for x in rest if x]
    print("%s\t%s\t%s\t%s" % ("ours" if ours else "other", scope, p, json.dumps(cmd)))
'
mcp_plan="" mrc=0
if [ -f "$CLAUDE_JSON" ] && grep -q '"buddylist"[[:space:]]*:' "$CLAUDE_JSON"; then
	if command -v "$PY" >/dev/null 2>&1; then
		# The buddylists this run removes, and a candidate already gone (a
		# re-run finishing what a stopped one left: every step skips what is
		# absent). Split on newlines (IFS), one argument per path.
		mcp_ours=$rm_lists
		for b in $lists; do ! gone_for_sure "$b" || mcp_ours="$mcp_ours$NL$b"; done
		mcp_plan=$("$PY" -I -B -c "$MCP_PY" list "$CLAUDE_JSON" $mcp_ours 2>/dev/null) && mrc=0 || mrc=$?
	else
		mrc=127
	fi
	# Decoded with no registration (the grep met the name elsewhere): nothing.
	[ "$mrc" -eq 0 ] || say "mcp: left: a buddylist MCP server is registered in $CLAUDE_JSON, which $PY cannot decode here; remove it with claude mcp remove buddylist, in each project it was added to"
fi
have_claude=""
command -v "$CLAUDE" >/dev/null 2>&1 && have_claude=1
# claude edits ITS file, ${CLAUDE_CONFIG_DIR:-$HOME}/.claude.json, whatever
# this read: a BUDDY_CLAUDE_JSON elsewhere approved one file's registration
# and claude removed another's (Codex, D-173's code pass).
claude_file=${CLAUDE_CONFIG_DIR:-$HOME}/.claude.json
mcp_mine=""
{ [ "$CLAUDE_JSON" = "$claude_file" ] || [ "$CLAUDE_JSON" -ef "$claude_file" ]; } && mcp_mine=1
mcp_go=""
for l in $mcp_plan; do
	kind=${l%%"$TAB"*} r=${l#*"$TAB"}
	scope=${r%%"$TAB"*} r=${r#*"$TAB"}
	pj=${r%%"$TAB"*} cmd=${r#*"$TAB"}
	if [ "$scope" = user ]; then
		where="user-wide in $CLAUDE_JSON" how="claude mcp remove buddylist --scope user"
	else
		where="for $pj in $CLAUDE_JSON" how="cd $(shq "$pj") && claude mcp remove buddylist --scope local"
	fi
	case $kind in
	odd) say "mcp: left: buddylist is registered for a project whose path holds a control character in $CLAUDE_JSON; remove it from inside that project: claude mcp remove buddylist --scope local" ;;
	other) say "mcp: left: buddylist is registered $where with the command $cmd, which is not a buddylist this run removes; if it is yours to remove: $how" ;;
	ours)
		# claude keys a local registration by its own cwd, the PHYSICAL path:
		# run from a directory whose physical path is another key, it would
		# act on that other project's registration.
		if [ "$scope" = local ] && [ "$( (CDPATH= cd -- "$pj" 2>/dev/null && pwd -P) && echo x)" != "$pj${NL}x" ]; then
			say "mcp: left: buddylist is registered $where, but that is not a directory's physical path here (gone, or reached through a link), so claude run there could act on another project's; if it is yours to remove, from that project: claude mcp remove buddylist --scope local"
		elif [ -z "$have_claude" ]; then
			say "mcp: left: buddylist is registered $where, and there is no $CLAUDE here to remove it with: $how"
		elif [ -z "$mcp_mine" ]; then
			say "mcp: left: buddylist is registered $where, but claude writes $claude_file, not that file, so claude run here would act on another file's registration: $how, with claude pointed at it"
		else
			say "mcp: $w buddylist registered $where (its command $cmd is removed above): $how"
			mcp_go="$mcp_go$NL$scope$TAB$pj$TAB$cmd"
		fi
		;;
	esac
done

# 1d. The hook lines for what this run removes (see the header). A line runs
# $HOME/bin/<binary>, so it is this run's only when the bin dir is that one
# (install.sh's --wire-hooks test, D-135) and the binary is gone after it.
real_dir() { (CDPATH= cd -- "$1" 2>/dev/null && pwd -P) || printf '%s\n' "$1"; printf x; }
bin_home=""
[ "$(real_dir "$BIN_DIR")" != "$(real_dir "$HOME/bin")" ] || bin_home=1
goes() { # goes <path> <planned list>: absent now, or planned to be removed
	{ [ ! -e "$1" ] && [ ! -L "$1" ]; } || case "$NL$2$NL" in *"$NL$1$NL"*) return 0 ;; *) return 1 ;; esac
}
CHAT_TOKENS="buddylist alert${NL}buddylist presence"
BUDDY_TOKENS="buddy hello${NL}buddy gate${NL}buddy beat${NL}buddy idle${NL}buddy bye${NL}buddy busy"
hook_toks="" hook_snap=""
if [ -f "$SETTINGS" ] || [ -L "$SETTINGS" ]; then
	if [ -z "$bin_home" ]; then
		say "settings: left: the hook lines in $SETTINGS run \$HOME/bin/..., and this run's bin dir is $BIN_DIR, so they run nothing it removes; remove them by hand if they are yours"
	else
		if goes "$BIN_DIR/buddylist" "$rm_lists"; then
			hook_toks=$CHAT_TOKENS
		else
			say "settings: left: buddylist's hook lines, since $BIN_DIR/buddylist stays (above)"
		fi
		if [ -z "$chat_only" ]; then
			if goes "$BIN_DIR/buddy" "$rm_buddies"; then
				hook_toks="$BUDDY_TOKENS${hook_toks:+$NL$hook_toks}"
			else
				say "settings: left: buddy's hook lines, since $BIN_DIR/buddy stays (above): the gate line still runs it"
			fi
		fi
	fi
	if [ -n "$hook_toks" ] && ! command -v "$PY" >/dev/null 2>&1; then
		say "settings: left: no $PY here to decode $SETTINGS, so its hook lines are left as they are (named below)"
		hook_toks=""
	elif [ -n "$hook_toks" ]; then
		pm=plan
		[ -z "$dry" ] || pm=dry
		# One argument per token: IFS is a newline here.
		hp_out=$("$PY" -I -B "$ROOT/scripts/wire-hooks.py" unwire "$pm" "$SETTINGS" - $hook_toks 2>&1) && hp_rc=0 || hp_rc=$?
		for l in $hp_out; do
			case $l in "snapshot "*) hook_snap=${l#snapshot } ;; *) say "$l" ;; esac
		done
		# A refusal (named above) leaves the file to the report below.
		[ "$hp_rc" -eq 0 ] && [ -n "$hook_snap" ] || hook_toks=""
		[ "$hook_snap" != - ] || hook_toks=""
	fi
fi

# 2. Act. Nothing above has changed anything but --chat's posture.
if [ -z "$dry" ]; then
	if [ "$svc" = loaded ]; then
		LC_ALL=C "$LAUNCHCTL" bootout "gui/$uid/$AGENT" ||
			die "launchctl bootout gui/$uid/$AGENT failed; nothing was removed. See: launchctl print gui/$uid/$AGENT"
		i=0
		while :; do
			LC_ALL=C "$LAUNCHCTL" print "gui/$uid/$AGENT" >/dev/null 2>&1 && prc=0 || prc=$?
			[ "$prc" -ne 113 ] || break
			[ "$i" -lt 10 ] || die "after bootout, gui/$uid/$AGENT still answers print (status $prc) after 10s; nothing more was removed."
			sleep 1
			i=$((i + 1))
		done
		say "removed: the launchd job $AGENT (booted out)"
	fi
	if [ -n "$plist_ok" ]; then
		mkdir -p "$STATE_DIR" || die "could not create $STATE_DIR to keep the plist in; the plist and every binary are left"
		kept=$STATE_DIR/$AGENT.plist.removed-$(date -u +%Y%m%dT%H%M%SZ)
		mv -- "$PLIST" "$kept" || die "could not move $PLIST to $kept; every binary is left"
		say "removed: $PLIST (kept at $kept; to restore: mv $(shq "$kept") $(shq "$PLIST") && launchctl bootstrap gui/$uid $(shq "$PLIST"))"
	fi
	# One unlink each, of exactly what the plan named: a symlink loses the
	# link only, and one whose target went first is still unlinked.
	unlink_one() { rm -f -- "$1" || die "could not remove $1; what follows it in the plan is left"; say "removed: $1"; }
	for b in $rm_lists; do unlink_one "$b"; done
	# The AIM server (1b): never a stop, since nothing removed here runs it.
	if [ "$osc" = loaded ]; then
		if LC_ALL=C "$LAUNCHCTL" bootout "gui/$uid/$OSCAR"; then
			i=0
			while :; do
				LC_ALL=C "$LAUNCHCTL" print "gui/$uid/$OSCAR" >/dev/null 2>&1 && prc=0 || prc=$?
				[ "$prc" -ne 113 ] || break
				if [ "$i" -ge 10 ]; then osc=left; break; fi
				sleep 1
				i=$((i + 1))
			done
		else
			prc=bootout osc=left
		fi
		if [ "$osc" = left ]; then
			say "chat server: left: launchctl bootout gui/$uid/$OSCAR did not take ($prc after 10s), so its plist stays too; see: launchctl print gui/$uid/$OSCAR"
		else
			say "removed: the launchd job $OSCAR (booted out)"
		fi
	fi
	if [ -n "$osc_plist" ] && [ "$osc" != left ]; then
		if mkdir -p "$STATE_DIR" && kept=$STATE_DIR/$OSCAR.plist.removed-$(date -u +%Y%m%dT%H%M%SZ) && mv -- "$OSCAR_PLIST" "$kept"; then
			say "removed: $OSCAR_PLIST (kept at $kept; to restore: mv $(shq "$kept") $(shq "$OSCAR_PLIST") && launchctl bootstrap gui/$uid $(shq "$OSCAR_PLIST"))"
		else
			say "chat server: left: $OSCAR_PLIST could not be moved to $STATE_DIR; move it out of LaunchAgents by hand"
		fi
	fi
	# The MCP registrations (1c), each read back: claude's exit status says
	# what it tried, the file says what is there.
	for l in $mcp_go; do
		scope=${l%%"$TAB"*} r=${l#*"$TAB"}
		pj=${r%%"$TAB"*} cmd=${r#*"$TAB"}
		# Read again just before claude runs: the plan read it before the
		# bootout, and a registration rewritten since was removed unchecked
		# (Codex, D-173's code pass). Left open: claude's own read after this.
		"$PY" -I -B -c "$MCP_PY" still "$CLAUDE_JSON" "$scope" "$pj" "$cmd" 2>/dev/null && src=0 || src=$?
		if [ "$src" -ne 0 ]; then
			[ "$scope" = user ] && what="the user-wide buddylist MCP registration" || what="the buddylist MCP registration for $pj"
			case $src in
			5) why="its command $cmd is still there" ;;
			*) why="it changed since the plan read it (or could not be read)" ;;
			esac
			say "mcp: left: $what, since $why; nothing was run for it"
			continue
		fi
		if [ "$scope" = user ]; then
			how="claude mcp remove buddylist --scope user"
			(CDPATH= cd -- "$HOME" && "$CLAUDE" mcp remove buddylist --scope user </dev/null >/dev/null 2>&1) && crc=0 || crc=$?
			what="the user-wide buddylist MCP registration"
		else
			how="cd $(shq "$pj") && claude mcp remove buddylist --scope local"
			(CDPATH= cd -- "$pj" && "$CLAUDE" mcp remove buddylist --scope local </dev/null >/dev/null 2>&1) && crc=0 || crc=$?
			what="the buddylist MCP registration for $pj"
		fi
		"$PY" -I -B -c "$MCP_PY" has "$CLAUDE_JSON" "$scope" "$pj" 2>/dev/null && hrc=0 || hrc=$?
		if [ "$hrc" -eq 3 ]; then
			say "removed: $what ($how)"
		else
			say "mcp: left: $what is still in $CLAUDE_JSON (claude exited $crc); remove it by hand: $how"
		fi
	done
	for f in $rm_skills; do unlink_one "$f"; done
	for d in $rm_skill_dirs; do
		[ -d "$d" ] || continue
		rmdir -- "$d" 2>/dev/null || say "skill: left: $d, which still holds files this checkout did not install or that differ"
	done
	if [ -n "$rm_buddies" ]; then
		in_flight $rm_buddies
		for b in $rm_buddies; do unlink_one "$b"; done
	fi
	# The hook lines (1d), LAST: a stop above left buddy installed and its gate
	# wired. Only the tokens whose binary is now gone, from the bytes the plan
	# read (the write refuses others). A refusal is named and the run goes on.
	if [ -n "$hook_toks" ]; then
		toks=""
		for t in $hook_toks; do
			b=$BIN_DIR/${t%% *}
			[ -e "$b" ] || [ -L "$b" ] || toks="$toks$NL$t"
		done
		if [ -n "$toks" ]; then
			hw_out=$("$PY" -I -B "$ROOT/scripts/wire-hooks.py" unwire write "$SETTINGS" "$hook_snap" $toks 2>&1) || true
			for l in $hw_out; do say "$l"; done
		fi
	fi
fi

# 3. What is left, and where. Reports only.
if command -v ps >/dev/null 2>&1; then
	pids=$(LC_ALL=C ps -Ao pid=,comm= 2>/dev/null | LC_ALL=C awk '$2 ~ /(^|\/)buddylist$/ {printf "%s ", $1}')
	# After a bootout these are live sessions' MCP servers and presence, or a
	# daemon started by hand: each keeps its old binary until it ends.
	[ -z "$pids" ] || say "left: buddylist processes still run (pid ${pids% }): live sessions' MCP servers or presence, or a daemon launchd does not own; never signalled here (D-122): they end with their sessions, or end them yourself"
fi
# The settings report: the report's grep (install.sh's), after the edit, so
# it names what is still there for whatever reason (a line left above, a
# refusal, a spelling not README's). A dry run's plan already said what goes
# and stays, so it greps only where no plan ran.
if [ -f "$SETTINGS" ] && { [ -z "$dry" ] || [ -z "$hook_snap" ]; }; then
	wanted="buddylist alert buddylist presence"
	[ -n "$chat_only" ] || wanted="buddy hello buddy gate buddy beat buddy idle buddy bye buddy busy $wanted"
	found=""
	IFS=' '
	set -- $wanted
	IFS=$NL
	while [ $# -ge 2 ]; do
		bin=$1 verb=$2; shift 2
		if grep -q "$bin\\\\\"* $verb" "$SETTINGS" || grep -q "/$bin $verb" "$SETTINGS" || grep -q "[\"; ]$bin $verb" "$SETTINGS"; then
			found="$found $bin $verb"
		fi
	done
	if [ -n "$found" ]; then
		say "settings: $SETTINGS still wires:$found; remove those hook lines by hand (why each is left is said above). README's lines are guarded ([ -x ] ... exit 0), so over a binary that is gone they do nothing"
	fi
	if grep -q '"BUDDY_CHAT"' "$SETTINGS" && [ -z "$chat_only" ]; then
		say "settings: $SETTINGS sets BUDDY_CHAT in env; it does nothing without buddy, remove it by hand if you like"
	elif [ -n "$chat_only" ]; then
		# Since D-127 hello obeys buddy's off stamp unless BUDDY_CHAT says off
		# or on, so only an `on` in settings keeps the hint; read as install.sh
		# reads it (trimmed, lower-cased). A grep: a false note only says so.
		sc=$(sed -n 's/.*"BUDDY_CHAT"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$SETTINGS" | head -n 1 |
			LC_ALL=C tr '[:upper:]' '[:lower:]' | LC_ALL=C sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
		[ "$sc" != on ] || say "settings: $SETTINGS declares BUDDY_CHAT=on to every session, which outranks buddy's claims-only stamp, so each session's hello still prints the chat hint; remove it from settings (D-127)"
	fi
fi
# The checkout's own .mcp.json is a file of the project (often checked in),
# not a registration claude removes: named, never edited. ~/.claude.json's
# registrations are 1c's.
if [ -f "$MCP_JSON" ] && grep -q '"buddylist"[[:space:]]*:' "$MCP_JSON"; then
	say "mcp: $MCP_JSON registers a buddylist MCP server (project scope); remove its \"buddylist\" entry by hand"
fi
if [ -z "$chat_only" ]; then
	for f in "$HOME/.codex/hooks.json" "$ROOT/.codex/hooks.json"; do
		if [ -f "$f" ] && grep -q 'buddy' "$f"; then
			say "codex: $f names buddy; Codex's gate wiring has no [ -x ] guard (docs/USAGE.md), so it now REFUSES every matching call: remove its buddy hooks by hand"
		fi
	done
	say "commit gate: a pre-commit you added README's \"\$HOME/bin/buddy\" commit-gate || exit \$? line to now fails every commit: remove that line (a pre-commit LINKED to $ROOT/.githooks/pre-commit exits 0 without buddy)"
	hp=$(git config --get core.hooksPath 2>/dev/null || true)
	[ -z "$hp" ] || say "left: core.hooksPath = $hp in $ROOT (setup-clone's or yours; its pre-commit exits 0 without buddy): git -C $(shq "$ROOT") config --unset core.hooksPath"
	[ ! -e "$BIN_DIR/agent-model-gate.py" ] || say "left: $BIN_DIR/agent-model-gate.py, the Agent-model hook's copy (D-120); remove it and its settings line by hand if you wired it"
	common=$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null || true)
	[ -z "$common" ] || [ ! -e "$common/buddy.db" ] || say "data: left: this checkout's ledger $common/buddy.db"
	say "data: left: every other repo's ledger (<git-common-dir>/buddy.db, wherever buddy init ran; not findable from here); each is inert without buddy"
fi
[ ! -d "$STATE_DIR" ] || say "data: left: $STATE_DIR (the chat journal, log and any plist kept above)"
set +f
for p in "$(dirname -- "$PLIST")"/com.buddy-system.*.plist; do
	[ -f "$p" ] || continue
	[ "$p" != "$PLIST" ] || continue
	[ "$p" != "$OSCAR_PLIST" ] || continue # 1b says what became of it
	l=$(basename -- "$p" .plist)
	say "left: $p, a launchd agent this cannot prove is buddy's (only $AGENT and the pinned $OSCAR are); to stop it: launchctl bootout gui/$uid/$l, then move the plist out of LaunchAgents"
done
set -f
if [ -n "$dry" ]; then
	say "dry run: nothing was removed"
elif [ -n "$chat_only" ] && [ -n "$chat_left" ]; then
	say "done, except the chat pieces named left above; buddy stays, claims-only"
elif [ -n "$chat_only" ]; then
	say "done: the chat half is removed and buddy stays, claims-only"
elif [ -x "$BIN_DIR/buddy" ]; then
	# README's gate line runs whatever executable is at that path: a foreign
	# or zero-byte one left there refuses every gated call on the machine
	# (D-107), the case uninstall is often run for (the Fable lens). The same
	# predicate as the line's `[ -x ]`: a searchable directory passes it too
	# and then fails to exec (Codex, code pass).
	echo "uninstall: INCOMPLETE — $BIN_DIR/buddy is still there and executable, and README's gate line runs it on every gated call (one that cannot run refuses them all): rm $(shq "$BIN_DIR/buddy")  (or chmod -x it)" >&2
	exit 1
elif [ -n "$chat_left" ]; then
	say "done, except the chat pieces named left above"
else
	say "done"
fi
