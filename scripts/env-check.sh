#!/bin/sh
# env-check.sh — a READ-ONLY look at this machine for the settings that help
# many agent sessions run in parallel (D-121).
#
# Usage: sh scripts/env-check.sh [--checkout <dir>]   judge this machine
#        sh scripts/env-check.sh --list                name every check and its
#                                                      reason; runs none
#        sh scripts/env-check.sh --help
#
# WHY. The first-time user's skill (skills/buddy-lead) ends with "may I check
# your machine?". What it checks for came from running fleets of four to
# twelve sessions on one macOS laptop for two months: each check below is a
# measured stall, not a preference. A Go build cache on an indexed volume kept
# fseventsd at 77-96% CPU and ran a test tier twice as slow; the cache grew
# to over 100 GB in days, the disk hit 93%, and the first symptom was
# `link: mapping output file failed: no space left on device`, read as a
# compile error; a maintenance sleep on AC power froze an unattended run so
# that three checks ran 5-16x slow; fseventsd reached 10 GB resident after 24
# days up and only a restart cleared it; worktrees under /tmp failed path
# checks because /tmp is a symlink to /private/tmp. None of these is buddy's
# to fix, so this script FIXES NOTHING. It prints one line per check and, for
# anything not PASS, one `fix:` line: the command, whether it needs sudo, and
# whether it is reversible. The user applies a fix, one at a time, in their
# own shell; the skill never types a sudo or an irreversible one.
#
# VERDICTS. PASS; WARN (advice: the machine works, a fleet will run slower or
# fill the disk sooner); FAIL (buddy itself cannot work: no Go or git, a disk
# about to be full); CANNOT-ASSESS (this script's own read-only command could
# not tell: not macOS, a tool missing, Spotlight's privacy list, which is
# unreadable without sudo). A check that could not tell never says PASS. Exit
# 0 unless a FAIL; 2 on a usage error. Nothing here is a hypothesis dressed
# as a verdict: where a threshold is a judgement (88% disk, 5 GB of
# transcripts) the line prints the number and the threshold.
#
# WHAT IT NEVER DOES. No write anywhere (not `go env -w`, not a settings
# file, not a temp file of its own), no sudo, no process signalled, no
# network, nothing launched. It reads: `uname`, `sw_vers`, `go version`,
# `go env GOCACHE`, `git --version`, `git worktree list`, `df`, `mdutil -s`
# (the volume's indexing state; `-i` would change it and is never run),
# `ps`, `tmutil destinationinfo|isexcluded`, `pmset -g custom`, `du` of
# ~/.claude/projects, a directory listing of the harness's scratch root, the
# user's LaunchAgents plists and `crontab -l`. `go` runs with GOTOOLCHAIN=local
# and GOTELEMETRY=off: from a checkout pinning a newer toolchain a bare `go
# version` would download it, and go writes telemetry counters at startup. The done-check
# (check-env-check.sh) runs it against stubbed commands that log every call
# and fails on any mutating argument, with a planted mutation as the control.
#
# NOT REPEATED HERE (reported by what already reports it, so two reports
# cannot drift): the hooks, the gate entry, the chat state and the handoff
# size (`sh scripts/install.sh` ends with them); language servers (`buddy
# init`); harness compatibility (`buddy diagnose`). The footer says so.
#
# KNOBS, for the done-check only: BUDDY_ENV_CHECK_TMP (default /tmp) is where
# the harness's scratch root `claude-<uid>` and the /tmp worktree rule are
# looked for, so a fixture can plant both without touching /tmp.
set -eu
# Byte-exact parsing and the ps/awk locale trap: a non-ASCII argv anywhere on
# the box aborts awk under a UTF-8 locale partway through a process scan.
LC_ALL=C; export LC_ALL

usage() {
	echo "usage: sh scripts/env-check.sh [--checkout <dir>] | --list | --help"
	echo "       read-only: one PASS/WARN/FAIL/CANNOT-ASSESS line per check, a fix: line for anything not PASS;"
	echo "       changes nothing, runs no sudo, exits non-zero only on a FAIL"
}
refuse() { usage >&2; echo "refused: $*" >&2; exit 2; }

CHECKOUT=""
LIST=0
while [ $# -gt 0 ]; do
	case $1 in
	--help|-h) usage; exit 0 ;;
	--list) LIST=1 ;;
	--checkout)
		[ $# -ge 2 ] || refuse "--checkout needs a directory"
		CHECKOUT=$2; shift ;;
	--checkout=*) CHECKOUT=${1#--checkout=} ;;
	-*) refuse "unknown flag $1" ;;
	*) refuse "no positional arguments; $1 is one" ;;
	esac
	shift
done

TMP=${BUDDY_ENV_CHECK_TMP:-/tmp}
TMP=${TMP%/}

# The check list, one line each: name, then the one-sentence reason. --list
# prints it; the run below walks it in this order.
CHECKS='platform|macOS is where buddy is developed and measured; elsewhere process identity is degraded
toolchain|buddy needs Go 1.26+ and git to build and run; install.sh and the test tier need python3
path-bin|a session runs buddy from its shell, so the install directory must be on PATH
disk-free|a full disk fails builds with a message read as a compile error, and SQLite writes with it
go-cache-spotlight|a Go build cache indexed by Spotlight keeps fseventsd busy under parallel builds
checkout-spotlight|a tree of worktrees indexed by Spotlight does the same, measured at 77-96% CPU
fseventsd|fseventsd leaks under long uptimes; 10 GB resident after 24 days on one machine
go-cache-trim|Go trims only entries unused 5 days; one cache passed 100 GB inside that under a fleet
time-machine|a build cache in a Time Machine backup is gigabytes of churn per backup
ac-sleep|a maintenance sleep on AC froze an unattended run; three checks ran 5-16x slow
worktrees-tmp|/tmp is a symlink to /private/tmp on macOS, and path checks on a worktree there disagree
scratch|transcripts and harness scratch grow without bound; 4.7 GB and 6.6 GB on one machine'

if [ "$LIST" = 1 ]; then
	printf '%s\n' "$CHECKS" | while IFS='|' read -r name why; do
		printf '%-19s %s\n' "$name" "$why"
	done
	exit 0
fi

npass=0; nwarn=0; nfail=0; ncant=0
# verdict <PASS|WARN|FAIL|CANNOT-ASSESS> <name> <detail>
verdict() {
	case $1 in
	PASS) npass=$((npass + 1)) ;;
	WARN) nwarn=$((nwarn + 1)) ;;
	FAIL) nfail=$((nfail + 1)) ;;
	CANNOT-ASSESS) ncant=$((ncant + 1)) ;;
	esac
	printf '%-15s %-19s %s\n' "[$1]" "$2" "$3"
}
# fix <line> — the one remedy line under a non-PASS verdict: the command, sudo
# or not, reversible or not.
fix() { printf '%-15s fix: %s\n' "" "$1"; }

have() { command -v "$1" >/dev/null 2>&1; }
# gocmd — `go` held to the installed toolchain and no telemetry: from a checkout
# whose go.mod pins a newer toolchain, a bare `go version` under GOTOOLCHAIN=auto
# downloads and unpacks it, and go opens telemetry counter files at startup
# (Codex code pass). Neither is a read.
gocmd() { GOTOOLCHAIN=local GOTELEMETRY=off go "$@"; }

# mount_of <path> — the mount point of the volume holding <path> (df's last
# column, which may hold spaces), or nothing.
mount_of() {
	df -Pk "$1" 2>/dev/null | awk 'NR == 2 { m = $6; for (i = 7; i <= NF; i++) m = m " " $i; print m }'
}
# indexing <mount> — on|off|unknown, from `mdutil -s` (read-only; `-i` is the
# switch and is never run here).
indexing() {
	have mdutil || { echo unknown; return; }
	s=$(mdutil -s "$1" 2>/dev/null || true)
	case $s in
	*disabled*) echo off ;;
	*enabled*) echo on ;;
	*) echo unknown ;;
	esac
}

echo "env-check: read-only; it changes nothing, runs no sudo, and reads only what each line names"

# The checkout: --checkout, else the main checkout of the repository around
# the cwd (worktrees share it), else the one this script ships in.
if [ -z "$CHECKOUT" ]; then
	common=$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null || true)
	if [ -n "$common" ]; then
		CHECKOUT=$(dirname -- "$common")
	else
		CHECKOUT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
	fi
fi

# platform
os=$(uname -s 2>/dev/null || echo unknown)
case $os in
Darwin)
	ver=$(sw_vers -productVersion 2>/dev/null || echo "version unknown")
	verdict PASS platform "macOS $ver"
	mac=1 ;;
*)
	verdict WARN platform "$os: buddy is developed and measured on macOS; the hermetic tier runs here, process identity is degraded (no harness process fence, no wake address)"
	fix "nothing to apply; the macOS-only checks below say CANNOT-ASSESS"
	mac=0 ;;
esac

# toolchain
gov=""; gitv=""; pyv=""
have go && gov=$(gocmd version 2>/dev/null | sed -n 's/^go version go\([0-9][0-9.]*\).*/\1/p')
have git && gitv=$(git --version 2>/dev/null | sed -n 's/^git version \([0-9][0-9.]*\).*/\1/p')
have python3 && pyv=yes
gomaj=${gov%%.*}; gomin=${gov#*.}; gomin=${gomin%%.*}
goold=0
if [ -n "$gov" ]; then
	case $gomaj in *[!0-9]*|'') goold=1 ;; *) [ "$gomaj" -gt 1 ] || { case $gomin in *[!0-9]*|'') goold=1 ;; *) [ "$gomin" -ge 26 ] || goold=1 ;; esac; } ;; esac
fi
if [ -z "$gov" ] || [ -z "$gitv" ]; then
	verdict FAIL toolchain "go ${gov:-MISSING}, git ${gitv:-MISSING}: buddy cannot be built or run without both"
	fix "install Go 1.26+ (go.dev/dl; a package manager) and git; the user's decision, each its own yes"
elif [ "$goold" = 1 ]; then
	verdict FAIL toolchain "go $gov is older than the 1.26 go.mod requires (git $gitv)"
	fix "install Go 1.26+, or let GOTOOLCHAIN=auto (Go 1.21+) fetch the pinned toolchain on the first build (no sudo; reversible)"
elif [ -z "$pyv" ]; then
	verdict WARN toolchain "go $gov, git $gitv, python3 MISSING: install.sh cannot judge the gate entry and the hermetic tier will not run"
	fix "install python3 (on macOS it ships with the Command Line Tools: xcode-select --install; the user's yes)"
else
	verdict PASS toolchain "go $gov, git $gitv, python3 present"
fi

# path-bin
if have buddy; then
	verdict PASS path-bin "buddy at $(command -v buddy)"
elif [ -x "$HOME/bin/buddy" ]; then
	verdict WARN path-bin "$HOME/bin/buddy is built but \$HOME/bin is not on this shell's PATH"
	fix "echo 'export PATH=\"\$HOME/bin:\$PATH\"' >> ~/.zprofile, then a new shell (no sudo; reversible: delete the line)"
else
	verdict WARN path-bin "no buddy on PATH and none at $HOME/bin/buddy: not installed yet"
	fix "sh scripts/install.sh from the checkout (README \"Install in two minutes\"; no sudo; it reports, never edits settings)"
fi

# disk-free: the volume holding HOME, in KB; thresholds printed with the number.
set -- $(df -Pk "$HOME" 2>/dev/null | awk 'NR == 2 { sub(/%/, "", $5); print $4, $5 }')
if [ $# -ne 2 ]; then
	verdict CANNOT-ASSESS disk-free "df could not report the volume holding $HOME"
	fix "nothing to apply; check free space by hand before a long run"
else
	avail=$1; used=$2
	gb=$((avail / 1048576))
	if [ "$avail" -lt 2097152 ]; then
		verdict FAIL disk-free "${gb} GB free, ${used}% used: a build fails with 'link: mapping output file failed' and the ledger's writes fail with it"
		fix "free space now: go clean -cache (no sudo; reversible, the next build recompiles), then the scratch and transcript lines below"
	elif [ "$used" -ge 88 ] || [ "$avail" -lt 31457280 ]; then
		verdict WARN disk-free "${gb} GB free, ${used}% used (advice at 88% or under 30 GB): a fleet's builds and swap share this space"
		fix "go clean -cache (no sudo; reversible: the next build recompiles); never a private GOCACHE per session, three of them once filled a disk"
	else
		verdict PASS disk-free "${gb} GB free, ${used}% used"
	fi
fi

# go-cache-spotlight
gocache=""
have go && gocache=$(gocmd env GOCACHE 2>/dev/null || true)
if [ -z "$gocache" ]; then
	verdict CANNOT-ASSESS go-cache-spotlight "no go on PATH, so no cache path to judge"
	fix "nothing to apply until Go is installed (toolchain above)"
else
	gm=$(mount_of "$gocache")
	[ -n "$gm" ] || gm=$(mount_of "$(dirname -- "$gocache")")
	# No mount found is "unknown", never the root volume's state (Codex code pass).
	case $([ -n "$gm" ] && indexing "$gm" || echo unknown) in
	off) verdict PASS go-cache-spotlight "$gocache is on ${gm:-a volume} with indexing off" ;;
	on)
		case $gocache in
		*.noindex) verdict PASS go-cache-spotlight "$gocache: a .noindex directory is skipped by Spotlight (its volume ${gm} is indexed)" ;;
		*)
			verdict WARN go-cache-spotlight "$gocache is on an indexed volume (${gm}) and not named .noindex"
			fix "go env -w GOCACHE=\"$gocache.noindex\" (no sudo; reversible: go env -u GOCACHE; the old directory stays until you delete it). Stronger: the cache on its own volume with sudo mdutil -i off <volume>" ;;
		esac ;;
	*)
		case $gocache in
		*.noindex) verdict PASS go-cache-spotlight "$gocache: a .noindex directory is skipped by Spotlight" ;;
		*)
			verdict CANNOT-ASSESS go-cache-spotlight "$gocache: the volume's indexing state is not readable here (${gm:-no mount}; mdutil $(have mdutil && echo present || echo missing))"
			fix "on macOS, go env -w GOCACHE=\"$gocache.noindex\" skips indexing either way (no sudo; reversible: go env -u GOCACHE)" ;;
		esac ;;
	esac
fi

# checkout-spotlight: the volume's state is readable; a folder's place in
# Spotlight's privacy list is not without sudo, so a subfolder of an indexed
# volume is CANNOT-ASSESS, never PASS.
cm=""
[ -d "$CHECKOUT" ] && cm=$(mount_of "$CHECKOUT")
case $([ -n "$cm" ] && indexing "$cm" || echo unknown) in
off) verdict PASS checkout-spotlight "$CHECKOUT is on ${cm} with indexing off" ;;
on)
	if [ "$cm" = "$CHECKOUT" ]; then
		verdict WARN checkout-spotlight "$CHECKOUT is its own volume, indexed"
		fix "sudo mdutil -i off \"$CHECKOUT\" (sudo; reversible: -i on)"
	else
		verdict CANNOT-ASSESS checkout-spotlight "$CHECKOUT is on indexed volume ${cm}; whether it is in Spotlight's privacy list is unreadable without sudo"
		fix "if it is not there: System Settings > Spotlight > Search Privacy, add $CHECKOUT (no sudo; reversible); or keep worktrees on their own volume with indexing off"
	fi ;;
*)
	if [ ! -d "$CHECKOUT" ]; then
		verdict CANNOT-ASSESS checkout-spotlight "$CHECKOUT is not a directory"
		fix "pass --checkout <the checkout>"
	else
		verdict CANNOT-ASSESS checkout-spotlight "the indexing state of ${cm:-the volume} holding $CHECKOUT is not readable here"
		fix "nothing to apply off macOS; on macOS this needs mdutil"
	fi ;;
esac

# fseventsd: resident set and CPU, from one ps sample.
set -- $(ps -axo rss=,pcpu=,comm= 2>/dev/null | awk '$3 ~ /(^|\/)fseventsd$/ { print $1, $2; exit }')
if [ $# -ne 2 ]; then
	verdict CANNOT-ASSESS fseventsd "no fseventsd in the process table (not macOS, or ps unreadable)"
	fix "nothing to apply"
else
	rss=$1; cpu=${2%%.*}
	rssmb=$((rss / 1024))
	if [ "$rss" -ge 1048576 ]; then
		verdict WARN fseventsd "${rssmb} MB resident, ${cpu}% CPU: a leak (10 GB after 24 days up on one machine)"
		fix "sudo killall fseventsd (sudo; launchd restarts it at once; a one-off, nothing persists)"
	elif [ "${cpu:-0}" -ge 50 ]; then
		verdict WARN fseventsd "${cpu}% CPU in this sample (${rssmb} MB resident): file churn it is logging, usually a build cache or worktrees on an indexed volume"
		fix "the two spotlight lines above; a sample at a quiet moment says whether this was the build you just ran"
	else
		verdict PASS fseventsd "${rssmb} MB resident, ${cpu}% CPU"
	fi
fi

# go-cache-trim: a scheduled `go clean -cache`, in a LaunchAgent or a crontab.
# A plist or crontab line that RUNS `go clean` or a program named for trimming
# the cache; a GOCACHE environment setting or a commented-out line is neither
# (Codex code pass). What the program does is not verified; the line says so.
trim=""
for p in "$HOME"/Library/LaunchAgents/*.plist; do
	[ -f "$p" ] || continue
	if grep -Eq 'go clean|cache[-_]?trim|trim[-_]?cache' "$p" 2>/dev/null; then trim=$p; break; fi
done
[ -n "$trim" ] || { crontab -l 2>/dev/null | grep -Ev '^[[:space:]]*#' | grep -Eq 'go clean|cache[-_]?trim|trim[-_]?cache' && trim=crontab; } || true
if [ -z "$gocache" ]; then
	verdict CANNOT-ASSESS go-cache-trim "no go on PATH"
	fix "nothing to apply until Go is installed"
elif [ -n "$trim" ]; then
	verdict PASS go-cache-trim "scheduled: $trim (what it runs is not verified here)"
else
	verdict WARN go-cache-trim "nothing schedules a trim of $gocache; Go drops only entries unused for 5 days"
	fix "a LaunchAgent or cron line running go clean -cache at low priority, on a schedule you choose (no sudo; reversible; the cost is recompiling what it dropped)"
fi

# time-machine: only the build cache; code is what a backup is for.
if ! have tmutil; then
	verdict CANNOT-ASSESS time-machine "no tmutil (not macOS)"
	fix "nothing to apply"
else
	dest=$(tmutil destinationinfo 2>&1 || true)
	case $dest in
	*"No destinations configured"*) verdict PASS time-machine "no Time Machine destination: nothing to exclude" ;;
	*)
		if [ -z "$gocache" ]; then
			verdict CANNOT-ASSESS time-machine "a destination exists; no go on PATH, so no cache path to judge"
			fix "nothing to apply until Go is installed"
		else
			case $(tmutil isexcluded "$gocache" 2>/dev/null || true) in
			*"[Excluded]"*) verdict PASS time-machine "$gocache is excluded from Time Machine" ;;
			*"[Included]"*)
				verdict WARN time-machine "$gocache is backed up by Time Machine: gigabytes of churn per backup"
				fix "sudo tmutil addexclusion -p \"$gocache\" (sudo; reversible: sudo tmutil removeexclusion -p)" ;;
			*)
				verdict CANNOT-ASSESS time-machine "tmutil isexcluded gave no verdict for $gocache"
				fix "tmutil isexcluded \"$gocache\" by hand" ;;
			esac
		fi ;;
	esac
fi

# ac-sleep: the AC power section's `sleep` minutes; 0 is never.
if ! have pmset; then
	verdict CANNOT-ASSESS ac-sleep "no pmset (not macOS)"
	fix "nothing to apply; keep a long unattended run awake by the platform's own means"
else
	acsleep=$(pmset -g custom 2>/dev/null | awk '/^AC Power:/ { on = 1; next } /^[A-Za-z].*:$/ { on = 0 } on && $1 == "sleep" { print $2; exit }')
	case $acsleep in
	'') verdict CANNOT-ASSESS ac-sleep "pmset -g custom shows no AC Power sleep setting"
	    fix "pmset -g custom by hand" ;;
	0) verdict PASS ac-sleep "never sleeps on AC power" ;;
	*)
		verdict WARN ac-sleep "sleeps after $acsleep min on AC: a maintenance sleep froze an unattended run, three checks ran 5-16x slow"
		fix "caffeinate -i -s -m -w <pid> & around a long run (no sudo; releases itself), or sudo pmset -c sleep 0 (sudo; reversible: sudo pmset -c sleep $acsleep)" ;;
	esac
fi

# worktrees-tmp
wts=$(git -C "$CHECKOUT" worktree list --porcelain 2>/dev/null | sed -n 's/^worktree //p' || true)
if [ -z "$wts" ]; then
	verdict CANNOT-ASSESS worktrees-tmp "$CHECKOUT is not a git checkout this user can list"
	fix "nothing to apply; pass --checkout <dir>"
else
	bad=""
	n=0
	oifs=$IFS; IFS='
'
	for w in $wts; do
		n=$((n + 1))
		case $w in "$TMP"/*|/private"$TMP"/*) bad="$bad $w" ;; esac
	done
	IFS=$oifs
	if [ -n "$bad" ]; then
		verdict WARN worktrees-tmp "worktree(s) under $TMP:$bad (a symlink to /private$TMP on macOS; path checks disagree across the two spellings)"
		fix "git worktree move <path> <somewhere under HOME> for each (no sudo; reversible)"
	else
		verdict PASS worktrees-tmp "$n worktree(s), none under $TMP"
	fi
fi

# scratch: transcripts (du, fast: few large files) and the harness's scratch
# root (an entry count: a du there took 12 s on one machine).
pk=0; unread=""
if [ -d "$HOME/.claude/projects" ]; then
	# du's status is kept: a partial total over an unreadable tree is not a size.
	if dur=$(du -skx "$HOME/.claude/projects" 2>/dev/null); then pk=${dur%%[[:space:]]*}; else unread="$HOME/.claude/projects"; fi
fi
case $pk in ''|*[!0-9]*) unread="${unread:-$HOME/.claude/projects}"; pk=0 ;; esac
sdir="$TMP/claude-$(id -u)"
sn=0
if [ -d "$sdir" ] && [ ! -r "$sdir" ]; then
	unread="${unread:+$unread, }$sdir"
elif [ -d "$sdir" ]; then
	for e in "$sdir"/* "$sdir"/.[!.]*; do [ -e "$e" ] && sn=$((sn + 1)); done
fi
pgb=$((pk / 1048576))
if [ -n "$unread" ]; then
	verdict CANNOT-ASSESS scratch "could not read $unread (du failed or no permission), so no size to judge"
	fix "du -sk on it by hand, as the user"
elif [ "$pk" -ge 5242880 ] || [ "$sn" -ge 200 ]; then
	verdict WARN scratch "transcripts $HOME/.claude/projects ${pgb} GB (advice at 5), scratch $sdir $sn entries (advice at 200)"
	fix "transcripts: cleanupPeriodDays in ~/.claude/settings.json (no sudo; irreversible: a deleted transcript cannot be resumed and the report scripts read them); scratch: delete entries you no longer need under $sdir (no sudo; irreversible)"
else
	verdict PASS scratch "transcripts ${pgb} GB, scratch $sn entries under $sdir"
fi

echo "env-check: $npass PASS, $nwarn WARN, $nfail FAIL, $ncant CANNOT-ASSESS. A WARN is advice; only a FAIL exits non-zero."
echo "env-check: not repeated here: hooks, gate entry, chat and handoff size (sh scripts/install.sh reports them); language servers (buddy init); harness compatibility (buddy diagnose)."
[ "$nfail" -eq 0 ]
