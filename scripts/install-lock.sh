# install-lock.sh — the ONE lock install.sh and uninstall.sh share (D-126).
# Sourced by both, never run. The caller defines say and die and sets ROOT.
#
# WHY. An install STARTED after uninstall's checks republished buddy:
# install.sh renamed buddy.new-<pid> over $BUDDY_BIN_DIR/buddy after
# uninstall.sh had unlinked it, and uninstall then said done or INCOMPLETE
# depending on which won (D-125's Open). Each script now holds this lock for
# its WHOLE run, so while either runs the other stops before it changes
# anything and says who holds it and what to do. Whole run, not the narrow
# window: install's skills and daemon steps race uninstall's as much as the
# rename does, and a lock released early is a window by another name.
#
# WHERE: $BIN_DIR/.buddy-install.lock. The bin dir is the knob both scripts
# share for the file the race is about, whichever checkout runs them, and a
# done-check that points BUDDY_BIN_DIR at a scratch directory gets a scratch
# lock with it. HOME-keyed was rejected: check-install.sh runs install.sh
# under the REAL HOME with a scratch bin dir, so a HOME lock made every
# done-check run contend with a real install on the machine. Two installs into
# two bin dirs are two installs; they do not share a lock (D-126 records it).
#
# WHAT: a SYMLINK whose target text is the holder (`ln -s` is one symlink(2),
# atomic, and fails when the name exists), never a mkdir'd directory with a
# pid file: between that mkdir and that write a killed run leaves a lock that
# names nobody, which no reader can judge stale and only a human can clear.
# `-n`, and text that starts with /dev/null/ (never resolves: ENOTDIR): ln
# given an existing name that resolves to a directory links INSIDE it and
# succeeds, so a foreign symlink to a writable directory read as taken (the
# smoke test's /etc link failed only on permission). A real directory at the
# name is refused before ln.
#
# STALE: the holder is its pid AND that process's start time (`ps -o lstart`).
# A lock whose pid is gone, or whose pid now belongs to a process that started
# at another time (pid reuse), is taken over and said loudly. A start time ps
# cannot give (no ps) falls back to the pid alone, which can only err toward
# refusing. Takeover renames the stale link aside (one rename; of two takers,
# one wins) and checks the renamed link is the one judged stale: a lock taken
# fresh in between is put back.
#
# The start time is read under TZ=UTC0: ps formats it in local time, so an
# install run with one TZ read a live uninstall run with another as a reused
# pid and took its lock over (Codex, code pass).
#
# NESTED: uninstall --chat runs `buddy update`, which runs install.sh, inside
# its own lock. It passes its lock text as BUDDY_INSTALL_LOCK_HELD on that one
# command; an install that finds the lock held by a LIVE holder with exactly
# that text runs inside it, registered as <lock>.inner. Nothing else sets it.
# The outer run, ending while its inner install still runs (update killed
# alone leaves its install child running; Codex), HANDS the lock to it rather
# than removing it, and the inner install releases it when it ends.
#
# SIGNALS: callers exit through their EXIT trap on HUP, INT and TERM
# (ilock_signals). While a takeover has another run's lock moved aside, those
# are ignored, so a run interrupted there cannot strand a live run without its
# lock (Codex); only a SIGKILL in that instant can, which D-126 records.

ILOCK_TAG=/dev/null/buddy-install-lock
ILOCK="" ILOCK_ME="" ILOCK_OWNED="" ILOCK_INNER=""

ilock_signals() {
	trap 'exit 129' HUP
	trap 'exit 130' INT
	trap 'exit 143' TERM
}
# ilock_started <pid>: when that process started, as ps says, on one line.
ilock_started() {
	LC_ALL=C TZ=UTC0 ps -o lstart= -p "$1" 2>/dev/null | head -n 1 | LC_ALL=C tr -s ' \t' '  ' | LC_ALL=C sed 's/^ //; s/ $//'
}
# The holder's fields, read anchored from the start: the text is
# "<tag> pid=<n> by=<script> started=<lstart>; from=<checkout, folded>".
ilock_pid() { printf '%s\n' "$1" | LC_ALL=C sed -n 's#^[^ ]* pid=\([0-9][0-9]*\) .*#\1#p'; }
ilock_by() { printf '%s\n' "$1" | LC_ALL=C sed -n 's#^[^ ]* pid=[0-9]* by=\([^ ]*\) .*#\1#p'; }
ilock_st() { printf '%s\n' "$1" | LC_ALL=C sed -n 's#^[^ ]* pid=[0-9]* by=[^ ]* started=\([^;]*\);.*#\1#p'; }
ilock_from() { printf '%s\n' "$1" | LC_ALL=C sed -n 's#^[^;]*; from=##p'; }
# ilock_show <text>: who, on one line (the text was folded when written, and a
# foreign link's is folded here).
ilock_show() {
	case $1 in
	"$ILOCK_TAG "*) printf '%s (pid %s, started %s, from %s)' "$(ilock_by "$1")" "$(ilock_pid "$1")" "$(ilock_st "$1")" "$(ilock_from "$1")" ;;
	*) printf '%s' "$1" | LC_ALL=C tr -c '[:print:]' '?' | head -c 200 ;;
	esac
}
# ilock_live <text>: its holder is still that process.
ilock_live() {
	ilk_p=$(ilock_pid "$1") ilk_s=$(ilock_st "$1")
	[ -n "$ilk_p" ] || return 1
	kill -0 "$ilk_p" 2>/dev/null || LC_ALL=C ps -p "$ilk_p" >/dev/null 2>&1 || return 1
	[ -n "$ilk_s" ] || return 0
	ilk_now=$(ilock_started "$ilk_p")
	[ -z "$ilk_now" ] || [ "$ilk_now" = "$ilk_s" ]
}

# ilock_take <script name> <lock path>: take the lock or die saying who has it.
ilock_take() {
	ILOCK=$2
	ilk_from=$(printf '%s' "$ROOT" | LC_ALL=C tr -c 'A-Za-z0-9._/ +@%,:-' '?' | head -c 200)
	ILOCK_ME="$ILOCK_TAG pid=$$ by=$1 started=$(ilock_started $$); from=$ilk_from"
	ilk_n=0
	while :; do
		ilk_n=$((ilk_n + 1))
		[ "$ilk_n" -le 5 ] || die "could not take the install lock $ILOCK after 5 tries (is $(dirname -- "$ILOCK") writable?); nothing was changed."
		if [ -e "$ILOCK" ] && [ ! -L "$ILOCK" ]; then
			die "$ILOCK is not a lock install.sh or uninstall.sh wrote (they write a symlink); if neither is running, remove it: rm -rf $(shq "$ILOCK"). Nothing was changed."
		fi
		if ln -sn "$ILOCK_ME" "$ILOCK" 2>/dev/null; then
			ILOCK_OWNED=1
			return 0
		fi
		# Gone between the two calls (its holder finished): try again.
		ilk_held=$(readlink "$ILOCK" 2>/dev/null) || continue
		case $ilk_held in
		"$ILOCK_TAG "*) ;;
		*) die "$ILOCK is a symlink neither install.sh nor uninstall.sh wrote (it names $(ilock_show "$ilk_held")); if neither is running, remove it: rm $(shq "$ILOCK"). Nothing was changed." ;;
		esac
		if ilock_live "$ilk_held"; then
			if [ -n "${BUDDY_INSTALL_LOCK_HELD:-}" ] && [ "$ilk_held" = "$BUDDY_INSTALL_LOCK_HELD" ]; then
				ilk_tmp=$ILOCK.inner-$$
				rm -f "$ilk_tmp"
				ln -sn "$ILOCK_ME" "$ilk_tmp" 2>/dev/null && mv -f "$ilk_tmp" "$ILOCK.inner" ||
					{ rm -f "$ilk_tmp"; die "could not register in $ILOCK.inner inside the $(ilock_by "$ilk_held") that holds $ILOCK; nothing was changed."; }
				ILOCK_INNER=1
				say "lock: running inside the $(ilock_by "$ilk_held") that holds $ILOCK (pid $(ilock_pid "$ilk_held"))"
				return 0
			fi
			die "$(ilock_show "$ilk_held") holds $ILOCK, so a buddy install or uninstall is running; nothing was changed. Let it finish, then run this again. If pid $(ilock_pid "$ilk_held") is not that run (ps -p $(ilock_pid "$ilk_held")), remove the lock: rm $(shq "$ILOCK")"
		fi
		ilk_aside=$ILOCK.stale-$$
		rm -f "$ilk_aside"
		trap '' HUP INT TERM
		if mv -f "$ILOCK" "$ilk_aside" 2>/dev/null; then
			ilk_got=$(readlink "$ilk_aside" 2>/dev/null || true)
			if [ "$ilk_got" = "$ilk_held" ]; then
				rm -f "$ilk_aside"
				say "lock: TOOK OVER $ILOCK from $(ilock_show "$ilk_held"), which is no longer running (killed, or its pid reused); what it was doing may be half done, and this run redoes its own part"
			elif ln -sn "$ilk_got" "$ILOCK" 2>/dev/null; then
				rm -f "$ilk_aside"
			else
				die "two runs raced to take over a stale $ILOCK, and the lock of $(ilock_show "$ilk_got") was moved to $ilk_aside and could not be put back; let every install and uninstall finish, then rm $(shq "$ilk_aside") and run this again."
			fi
		fi
		ilock_signals
	done
}

# ilock_release: remove the lock if this run holds it, or hand it to the
# inner install still running (NESTED, above). Run from the EXIT trap; it
# never changes the exit status.
ilock_release() {
	ilk_cur=$(readlink "$ILOCK" 2>/dev/null || true)
	if [ -n "$ILOCK_INNER" ]; then
		# Handed to this run by an outer one that ended first, or not.
		[ "$ilk_cur" != "$ILOCK_ME" ] || rm -f "$ILOCK"
		[ "$(readlink "$ILOCK.inner" 2>/dev/null || true)" != "$ILOCK_ME" ] || rm -f "$ILOCK.inner"
	elif [ -n "$ILOCK_OWNED" ] && [ "$ilk_cur" = "$ILOCK_ME" ]; then
		ilk_in=$(readlink "$ILOCK.inner" 2>/dev/null || true)
		if [ -n "$ilk_in" ] && ilock_live "$ilk_in"; then
			# One rename over the lock: never a moment without one.
			rm -f "$ILOCK.hand-$$"
			if ln -sn "$ilk_in" "$ILOCK.hand-$$" 2>/dev/null && mv -f "$ILOCK.hand-$$" "$ILOCK"; then
				say "lock: handed $ILOCK to the $(ilock_by "$ilk_in") this run started (pid $(ilock_pid "$ilk_in")), still running; it releases the lock when it ends"
			else
				rm -f "$ILOCK.hand-$$"
				say "lock: NOTE — could not hand $ILOCK to the $(ilock_by "$ilk_in") still running (pid $(ilock_pid "$ilk_in")); left held by this run, so the next run takes it over once this one is gone"
			fi
		else
			rm -f "$ILOCK" "$ILOCK.inner"
		fi
	fi
	ILOCK_OWNED="" ILOCK_INNER=""
}

# ilock_peek <lock path>: for a dry run, which takes no lock: one line when
# the lock is there, saying what a real run would do about it.
ilock_peek() {
	ilk_held=$(readlink "$1" 2>/dev/null) || return 0
	case $ilk_held in
	"$ILOCK_TAG "*) ;;
	*) say "lock: NOTE — $1 is a symlink neither script wrote; a real run would stop on it"; return 0 ;;
	esac
	if ilock_live "$ilk_held"; then
		say "lock: NOTE — $(ilock_show "$ilk_held") holds $1; a real run now would stop until it finishes"
	else
		say "lock: NOTE — $1 was left by $(ilock_show "$ilk_held"), no longer running; a real run would take it over"
	fi
}
