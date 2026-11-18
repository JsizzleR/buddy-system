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
# VERDICTS. PASS; WARN (advice: the machine works, but a fleet will run slower,
# fill the disk sooner, cost more, or meet a setting or stale copy it must work
# around); FAIL (buddy itself cannot work: no Go or git, a disk
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
# user's LaunchAgents plists, `crontab -l`, `sysctl -n hw.memsize`,
# ~/.claude/settings.json and ~/.claude.json (each through
# scripts/gate-hook.py's one decode, python3 -I -B), two variables of its own
# environment, `cmp` of each shipped SKILL.md against the copies under
# ~/.agents/skills and Codex's skills directory, and, where Codex's directory
# exists, its config.toml and `buddy diagnose --codex-hooks` of its hooks.json.
# `go` runs with GOTOOLCHAIN=local
# and GOTELEMETRY=off: from a checkout pinning a newer toolchain a bare `go
# version` would download it, and go writes telemetry counters at startup. The done-check
# (check-env-check.sh) runs it against stubbed commands that log every call
# and fails on any mutating argument, with a planted mutation as the control.
#
# NOT REPEATED HERE (reported by what already reports it, so two reports
# cannot drift): the hooks, the gate entry, the chat state and the handoff
# size (`sh scripts/install.sh` ends with them); language servers (`buddy
# init`); harness compatibility (`buddy diagnose`). The footer says so. The
# Codex gate row is the exception that keeps the rule: install.sh prints it
# from the same sourced reader (codex-gate-row.sh), so it is one report printed
# twice, for a user who runs only this script.
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

# HOME unset aborted the first $HOME under set -u, so no row printed (Codex
# code pass). Unset is read as empty from here on (not exported to what this
# runs), and every row that reads under HOME says CANNOT-ASSESS for it,
# never PASS on a path rooted at /.
HOME=${HOME:-}

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
ram-sessions|30 sessions on 24 GB, resumed in a burst, ran swap out and froze the machine twice
go-cache-trim|Go trims only entries unused 5 days; one cache passed 100 GB inside that under a fleet
time-machine|a build cache in a Time Machine backup is gigabytes of churn per backup
ac-sleep|a maintenance sleep on AC froze an unattended run; three checks ran 5-16x slow
worktrees-tmp|/tmp is a symlink to /private/tmp on macOS, and path checks on a worktree there disagree
scratch|transcripts and harness scratch grow without bound; 4.7 GB and 6.6 GB on one machine
sandbox|under the Claude Code Bash sandbox, worktree and integration git calls fail unless run with it off
summaries|subagent progress summaries add an estimated 20% to subagent spend, unseen by any transcript
code-mode|with Code Mode on the gate was measured to refuse; mail lost on late code output is now skipped (D-195), not re-measured live
user-mcp|a user-scope MCP server that runs a command starts in every session of every repository; about 220 MB each
codex-skill-copy|a SKILL.md under ~/.agents/skills or the Codex skills directory that is not the shipped one is stale
codex-gate|plain Codex is gated only by a hand-wired hooks.json, and an absent hook is a silent allow; only where ~/.codex exists'

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
	printf '%-15s %-19s %s\n' "[$1]" "$2" "$(oneline "$3")"
}
# fix <line> — the one remedy line under a non-PASS verdict: the command, sudo
# or not, reversible or not.
fix() { printf '%-15s fix: %s\n' "" "$(oneline "$1")"; }
# oneline <text> — <text> on one physical line: every control character,
# newline included, printed as `?`. A path from the environment (HOME,
# CODEX_HOME) holding a newline otherwise printed a second line that began
# `[PASS]` (Codex code pass); the done-check reads the lines by that column.
oneline() { printf '%s' "$1" | awk 'BEGIN { ORS = "" } { gsub(/[[:cntrl:]]/, "?"); if (NR > 1) print "?"; print }'; }

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

# This script's own directory: codex-gate-row.sh ships beside it.
SCRIPTS=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

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

# path-bin. A buddy on PATH that is not the file install.sh builds at
# $HOME/bin/buddy (the one the hooks name) is a WARN naming both (#135, D-217):
# the commit gate and setup-clone take `command -v buddy` first, and so does
# every typed verb, so a stale copy earlier on PATH (one `go install` left) is
# refused on a newer ledger (D-037) while this row said PASS. -ef compares
# device and inode, so a link to the installed copy is the same file. Advice
# only: which copy goes is the user's.
if have buddy; then
	pb=$(command -v buddy)
	if [ -n "$HOME" ] && [ -x "$HOME/bin/buddy" ] && [ ! "$pb" -ef "$HOME/bin/buddy" ]; then
		verdict WARN path-bin "buddy on PATH is $pb, not $HOME/bin/buddy, the copy install.sh builds and the hooks run: the commit gate and every typed verb run $pb, and a stale one is refused on a newer ledger (D-037)"
		fix "remove $pb, or put \$HOME/bin ahead of its directory on PATH (the user's decision; no sudo unless that directory needs it)"
	else
		verdict PASS path-bin "buddy at $pb"
	fi
elif [ -n "$HOME" ] && [ -x "$HOME/bin/buddy" ]; then
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

# fseventsd: resident set and CPU, from one ps sample, which ram-sessions
# below reads too.
PSOUT=$(ps -axo rss=,pcpu=,comm= 2>/dev/null || true)
set -- $(printf '%s\n' "$PSOUT" | awk '$3 ~ /(^|\/)fseventsd$/ { print $1, $2; exit }')
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

# ram-sessions (D-209, #144): RAM over the sessions this machine may run at
# once. Measured on one 24 GB macOS laptop: about 30 Claude Code sessions
# across four repositories' fleets, then 12 lanes resumed 3 s apart running
# tests in one minute, ran swap out; macOS suspended the terminal holding
# every lane and the box froze twice. A session starts at about 250 MB, a
# user-scope MCP server adds about 220 MB to every one (user-mcp, below), and
# one cold Go link peaked at 2.3 GB. So the advice threshold is 2 GB a
# session, a judgement printed with the numbers, as disk-free does. The
# session count is the larger of a 12-session fleet (the largest these checks
# were measured under) and the Claude Code processes running NOW, in every
# repository: a process named `claude` in the ps sample above, an
# observation that misses a session run under another name (node, for an
# npm install). With no ps sample the 12 is a floor: it can still WARN, never
# PASS (Codex code pass). `sysctl -n hw.memsize` is a read; off macOS there is
# none.
memb=""
have sysctl && memb=$(sysctl -n hw.memsize 2>/dev/null || true)
case $memb in *[!0-9]*) memb="" ;; esac
nlive=$(printf '%s\n' "$PSOUT" | awk '$NF ~ /(^|\/)claude$/ { n++ } END { print n + 0 }')
# An unreadable process table is a count nobody took, never "0 running".
livetxt="the $nlive Claude Code process(es) running now, every repository's"
[ -n "$PSOUT" ] || livetxt="the Claude Code processes running now, uncounted: ps gave nothing"
nexp=12
[ "$nlive" -le "$nexp" ] || nexp=$nlive
if [ -z "$memb" ]; then
	verdict CANNOT-ASSESS ram-sessions "sysctl -n hw.memsize gave no RAM size (not macOS, or no sysctl); ${livetxt#the }"
	fix "nothing to apply; size a fleet by hand at about 2 GB of RAM a session, every repository's sessions counted"
else
	memmb=$((memb / 1048576)); per=$((memmb / nexp))
	rwhy="$((memmb / 1024)) GB RAM over $nexp sessions (the larger of a 12-session fleet and $livetxt) is $per MB a session"
	if [ "$per" -lt 2048 ]; then
		verdict WARN ram-sessions "$rwhy (advice under 2048: a session starts near 250 MB, each user-scope MCP server adds about 220 MB to it, one cold Go link peaked at 2.3 GB; 24 GB under about 30 sessions froze twice)"
		fix "run fewer sessions at once across all repositories, and admit each lane only after the last one's first report and a memory check (the fleet skill's Opening lanes; no sudo; reversible)"
	elif [ -z "$PSOUT" ]; then
		verdict CANNOT-ASSESS ram-sessions "$rwhy, a floor: the sessions running now were not counted, so no PASS"
		fix "ps -axo comm= by hand, as the user, and count the Claude Code sessions across every repository"
	else
		verdict PASS ram-sessions "$rwhy"
	fi
fi

# go-cache-trim: a scheduled `go clean -cache`, in a LaunchAgent or a crontab.
# A plist or crontab line that RUNS `go clean` or a program named for trimming
# the cache; a GOCACHE environment setting or a commented-out line is neither
# (Codex code pass). What the program does is not verified; the line says so.
trim=""
for p in "$HOME"/Library/LaunchAgents/*.plist; do
	[ -n "$HOME" ] && [ -f "$p" ] || continue
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
if [ -z "$HOME" ]; then
	verdict CANNOT-ASSESS scratch "HOME is unset, so there are no transcripts to size"
	fix "run it from the user's own shell, with HOME set"
elif [ -n "$unread" ]; then
	verdict CANNOT-ASSESS scratch "could not read $unread (du failed or no permission), so no size to judge"
	fix "du -sk on it by hand, as the user"
elif [ "$pk" -ge 5242880 ] || [ "$sn" -ge 200 ]; then
	verdict WARN scratch "transcripts $HOME/.claude/projects ${pgb} GB (advice at 5), scratch $sdir $sn entries (advice at 200)"
	fix "transcripts: cleanupPeriodDays in ~/.claude/settings.json (no sudo; irreversible: a deleted transcript cannot be resumed and the report scripts read them); scratch: delete entries you no longer need under $sdir (no sudo; irreversible)"
else
	verdict PASS scratch "transcripts ${pgb} GB, scratch $sn entries under $sdir"
fi

# sandbox and summaries (D-188, D-187): two facts from the user's Claude Code
# settings, read ONCE through scripts/gate-hook.py's decode (D-182's depth cap
# on the text, D-183's NaN/Infinity refusal), the decode every settings reader
# here shares, never a second parser: D-187 suggested `plutil -extract`, a
# second reading that takes what Claude Code refuses. python3 runs -I -B, so
# no user site is imported and no __pycache__ is written beside gate-hook.py.
# Only the user layer is read (a project's settings, settings.local.json and a
# managed file are not), and each line says so. The reader prints one line of
# its own words, never a value from the file: `absent`, `unread <reason>`, or
# `ok` and k=v facts (the fourth, codemode, is D-187's #103 sibling, D-190).
# absent_proven <path> — true when <path> provably does not exist: its deepest
# existing ancestor is a non-directory (ENOTDIR) or a searchable directory.
# Under one that cannot be searched, test -e is false for a file that is there;
# so is it for a symlink whose target sits under one, so a link that does not
# resolve proves nothing (Codex code pass: ~/.claude linked into a locked
# directory read as "no settings file", PASS).
absent_proven() {
	_a=$(dirname -- "$1")
	while [ ! -e "$_a" ] && [ ! -L "$_a" ]; do
		_n=$(dirname -- "$_a"); [ "$_n" != "$_a" ] || return 1; _a=$_n
	done
	[ -e "$_a" ] || return 1
	[ ! -d "$_a" ] || [ -x "$_a" ]
}
# The reader, passed with -c, not a heredoc: macOS /bin/sh writes a heredoc to
# a temporary file, a write this script promises not to make (Codex code
# pass). So the program holds no single quote.
# DECODE_PY is the shared head: it decodes argv[1] into s, a JSON object, or
# prints `unread <why>` and exits; CSET_PY and MCP_PY (user-mcp, below) each
# append their own reading of s to it.
DECODE_PY='import importlib.util
import sys

path, gate_hook = sys.argv[1:]
spec = importlib.util.spec_from_file_location("gate_hook", gate_hook)
gh = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gh)
try:
    with open(path, encoding="utf-8") as f:
        s = gh.decode(f.read())
except gh.NotChecked as e:
    print("unread it " + e.said)
    sys.exit(0)
except (OSError, ValueError, RecursionError) as e:
    print("unread it is not JSON this check can read (%s)" % type(e).__name__)
    sys.exit(0)
if not isinstance(s, dict):
    print("unread it is not a JSON object")
    sys.exit(0)
'
CSET_PY=$DECODE_PY'

def get(d, *keys):
    for k in keys:
        d = d.get(k) if isinstance(d, dict) else None
    return d


# An allow rule that covers `buddy wait check`, recognized in the shapes
# Claude Code documents for Bash: the bare tool, an exact command, and a
# prefix rule (`:*`, or a trailing ` *` / `*`). A rule in any other shape is
# not recognized, and the line then says the check is unattended-unsafe: the
# wording of the WARN, never its verdict, rides on this.
CMD = "buddy wait check"


def covers(rule):
    if not isinstance(rule, str):
        return False
    r = rule.strip()
    if r in ("Bash", "Bash(*)"):
        return True
    if not (r.startswith("Bash(") and r.endswith(")")):
        return False
    body = r[5:-1]
    if body == CMD:
        return True
    for tail in (":*", " *", "*"):
        if body.endswith(tail):
            pre = body[: -len(tail)]
            return CMD == pre or CMD.startswith(pre + " ") or (tail == "*" and CMD.startswith(pre))
    return False


# Code Mode is on for a truthy CLAUDE_CODE_EXPERIMENTAL_CODE_MODE: the docs
# name 1; true, yes and on are read as on too, so a spelling Claude Code may
# take is never read as off (the row it feeds is advice either way). Exactly
# the spellings the case pattern of the shell arm takes, any case, nothing
# stripped: " true " in settings and in the environment must agree (Codex
# code pass: a strip here made settings WARN on a value the environment PASSed).
def truthy(v):
    return isinstance(v, str) and v.lower() in ("1", "true", "yes", "on")


allow = get(s, "permissions", "allow")
v = get(s, "env", "CLAUDE_CODE_DISABLE_AGENT_PROGRESS_SUMMARIES")
print("ok sandbox=%d autoallow=%d rule=%d summaries=%d codemode=%d" % (
    get(s, "sandbox", "enabled") is True,
    get(s, "sandbox", "autoAllowBashIfSandboxed") is True,
    any(covers(r) for r in allow) if isinstance(allow, list) else False,
    isinstance(v, str) and v.strip().lower() in ("1", "true"),
    truthy(get(s, "env", "CLAUDE_CODE_EXPERIMENTAL_CODE_MODE"))))
'
CSET=${HOME:-}/.claude/settings.json
cset=""
if [ -z "${HOME:-}" ]; then
	CSET="~/.claude/settings.json"
	cset="unread HOME is unset, so there is no user settings file to read"
elif [ ! -e "$CSET" ] && [ ! -L "$CSET" ]; then
	if absent_proven "$CSET"; then cset=absent; else cset="unread its directory cannot be searched"; fi
elif ! have python3; then
	cset="unread no python3 to decode it"
elif [ ! -r "$SCRIPTS/gate-hook.py" ]; then
	cset="unread no $SCRIPTS/gate-hook.py to decode it with"
else
	cset=$(python3 -I -B -c "$CSET_PY" "$CSET" "$SCRIPTS/gate-hook.py" 2>/dev/null || true)
	[ -n "$cset" ] || cset="unread python3 could not run scripts/gate-hook.py's decode on it"
fi

sb=0; aa=0; ar=0; sm=0; cm=0
case $cset in
ok\ *)
	for kv in ${cset#ok }; do
		case $kv in
		sandbox=1) sb=1 ;; autoallow=1) aa=1 ;; rule=1) ar=1 ;; summaries=1) sm=1 ;; codemode=1) cm=1 ;;
		esac
	done ;;
esac

# sandbox (D-188): Claude Code's Bash sandbox leaves .git/worktrees read-only
# but for the session's own worktree, so a coordinator's worktree add/remove,
# its integration worktree's cherry-pick -n and commit, and the restack's
# sandbox-exec (which cannot nest) fail sandboxed and run with the sandbox off
# for that call; a lane's claim and commits in its own linked worktree ran
# sandboxed. An unattended `buddy wait check` asked for approval outside auto
# mode with neither autoAllowBashIfSandboxed nor an allow rule; a lane's
# permission mode is a launch flag no file holds, so the line names that.
case $cset in
absent) verdict PASS sandbox "no $CSET: the Bash sandbox is not enabled in user settings" ;;
unread\ *)
	verdict CANNOT-ASSESS sandbox "$CSET: ${cset#unread }; whether the Bash sandbox is on is unknown"
	fix "read sandbox.enabled in $CSET by hand, as the user" ;;
*)
	if [ "$sb" = 0 ]; then
		verdict PASS sandbox "sandbox.enabled is not true in $CSET (user layer only; a project's settings are not read)"
	else
		w="a coordinator runs git worktree add/remove, the integration worktree's cherry-pick -n and commit, and the restack call with the sandbox off for that call (D-188)"
		if [ "$aa" = 1 ] || [ "$ar" = 1 ]; then
			verdict WARN sandbox "sandbox.enabled is true in $CSET: $w"
			fix "nothing to change in settings: the coordinator passes dangerouslyDisableSandbox on those calls (the fleet skill's landing step 3); a lane's own claim and commits run sandboxed"
		else
			verdict WARN sandbox "sandbox.enabled is true in $CSET: $w; and outside auto mode an unattended buddy wait check asks for approval: neither sandbox.autoAllowBashIfSandboxed true nor an allow rule covering it is set there"
			fix "the coordinator passes dangerouslyDisableSandbox on those calls (the fleet skill's landing step 3); for unattended waits, a lane in auto mode, or \"autoAllowBashIfSandboxed\": true under \"sandbox\" or \"Bash(buddy wait check)\" in permissions.allow of $CSET (no sudo; reversible: remove it)"
		fi
	fi ;;
esac

# summaries (D-187): subagent progress summaries are forked requests Claude
# Code writes with skipTranscript, so no transcript or report counts them; the
# number is D-187's replay of the summarizer's timer, an ESTIMATE, and the
# line says so. Advice: the operator decides; this row writes nothing. The
# process environment is read first and is enough on its own for PASS.
sv=${CLAUDE_CODE_DISABLE_AGENT_PROGRESS_SUMMARIES:-}
case $sv in
1|[Tt][Rr][Uu][Ee]) verdict PASS summaries "CLAUDE_CODE_DISABLE_AGENT_PROGRESS_SUMMARIES=$sv in this environment" ;;
*)
	swhy="subagent progress summaries on: about 20% on subagent spend, 4% of a fleet day's (estimated, D-187)"
	sfix="\"env\": {\"CLAUDE_CODE_DISABLE_AGENT_PROGRESS_SUMMARIES\": \"1\"} in $CSET, if you want it (no sudo; reversible: remove it; loses the background-task panel's progress line for each running subagent)"
	case $cset in
	absent)
		verdict WARN summaries "$swhy; not set in this environment, and no $CSET"
		fix "$sfix" ;;
	unread\ *)
		verdict CANNOT-ASSESS summaries "not set in this environment, and $CSET: ${cset#unread }"
		fix "read env.CLAUDE_CODE_DISABLE_AGENT_PROGRESS_SUMMARIES in $CSET by hand, as the user" ;;
	*)
		if [ "$sm" = 1 ]; then
			verdict PASS summaries "CLAUDE_CODE_DISABLE_AGENT_PROGRESS_SUMMARIES is 1 or true in env of $CSET"
		else
			verdict WARN summaries "$swhy; not set in this environment or in env of $CSET (a project's settings are not read)"
			fix "$sfix"
		fi ;;
	esac ;;
esac

# code-mode (D-190, amending D-189): Claude Code's experimental Code Mode runs
# Claude's tool calls from JavaScript inside one REPL call. Measured on 2.1.325
# in a scratch repo: the gate DENIED an inner Edit of a peer-held file and an
# inner shell apply_patch with its own reasons, the control edit went through,
# and the inner payloads carry tool_name and file_path as the gate reads them;
# so the gate needs no change. Mail (#107, D-195): measured on 2.1.325, a
# message queued after a Stop was marked delivered unread when background
# code's late output opened a turn (2 of 2), and a hook's context on a poll
# event is discarded; buddy now skips those drains, but that skip has not been
# re-measured live on an installed build. That is why this row is still a WARN
# and not a PASS: advice, so the operator decides. On is the process environment
# (enough on its own) or env of the user settings, the only two places Code
# Mode reads it (a project's settings are ignored for it, per the docs).
cv=${CLAUDE_CODE_EXPERIMENTAL_CODE_MODE:-}
cwhy="the gate was measured to refuse inner Edit and Bash calls there (Claude Code 2.1.325, D-190); mail queued after a Stop was measured marked delivered unread on late code output (2 of 2), and buddy now skips those drains (D-195), not re-measured live on an installed build"
cfix="nothing to change for the gate; to rule out the mail loss until that skip is re-measured, start lanes without CLAUDE_CODE_EXPERIMENTAL_CODE_MODE and remove it from env of $CSET (no sudo; reversible: set it again; a session started in Code Mode resumes only with it on)"
case $cv in
1|[Tt][Rr][Uu][Ee]|[Yy][Ee][Ss]|[Oo][Nn])
	verdict WARN code-mode "Code Mode is on (CLAUDE_CODE_EXPERIMENTAL_CODE_MODE=$cv in this environment): $cwhy"
	fix "$cfix" ;;
*)
	case $cset in
	absent) verdict PASS code-mode "Code Mode is not on: not set in this environment, and no $CSET" ;;
	unread\ *)
		verdict CANNOT-ASSESS code-mode "not on in this environment, and $CSET: ${cset#unread }"
		fix "read env.CLAUDE_CODE_EXPERIMENTAL_CODE_MODE in $CSET by hand, as the user" ;;
	*)
		if [ "$cm" = 1 ]; then
			verdict WARN code-mode "Code Mode is on (CLAUDE_CODE_EXPERIMENTAL_CODE_MODE in env of $CSET): $cwhy"
			fix "$cfix"
		else
			verdict PASS code-mode "Code Mode is not on: not set in this environment or in env of $CSET (a project's settings are ignored for it)"
		fi ;;
	esac ;;
esac

# user-mcp (D-209, #144): an MCP server in the top-level mcpServers of
# ~/.claude.json is user scope, and Claude Code starts every one of them in
# every session of every repository. One that runs a command (stdio: a
# `command`, type absent or "stdio") is a local process per session: one
# `npx -y` server measured about 220 MB a session (npm plus node), 2.4 GB
# across 12 sessions, and 25 of 1,277 sessions in 30 days ever called one of
# its tools. A remote one (a url) starts no process and is only counted. A
# project-scope (.mcp.json) or local-scope (projects.<path>.mcpServers) server
# starts only where it is used, so neither is counted. Read through the same
# decode as the settings rows (DECODE_PY); it prints counts, never a name.
MCP_PY=$DECODE_PY'
m = s.get("mcpServers")
if m is None:
    m = {}
if not isinstance(m, dict):
    print("unread its mcpServers is not a JSON object")
    sys.exit(0)


def local(v):
    if not isinstance(v, dict) or "url" in v:
        return False
    t = v.get("type")
    return t == "stdio" or (t is None and "command" in v)


n = sum(1 for v in m.values() if local(v))
print("ok %d %d" % (n, len(m) - n))
'
CJSON=${HOME:-}/.claude.json
if [ -z "${HOME:-}" ]; then
	mcp="unread HOME is unset, so there is no ~/.claude.json to read"; CJSON="~/.claude.json"
elif [ ! -e "$CJSON" ] && [ ! -L "$CJSON" ]; then
	if absent_proven "$CJSON"; then mcp=absent; else mcp="unread its directory cannot be searched"; fi
elif ! have python3; then
	mcp="unread no python3 to decode it"
elif [ ! -r "$SCRIPTS/gate-hook.py" ]; then
	mcp="unread no $SCRIPTS/gate-hook.py to decode it with"
else
	mcp=$(python3 -I -B -c "$MCP_PY" "$CJSON" "$SCRIPTS/gate-hook.py" 2>/dev/null || true)
	[ -n "$mcp" ] || mcp="unread python3 could not run scripts/gate-hook.py's decode on it"
fi
case $mcp in
absent) verdict PASS user-mcp "no $CJSON: no user-scope MCP server" ;;
unread\ *)
	verdict CANNOT-ASSESS user-mcp "$CJSON: ${mcp#unread }; how many user-scope MCP servers start in every session is unknown"
	fix "claude mcp list by hand, as the user: a user-scope server there starts in every session" ;;
ok\ *)
	set -- ${mcp#ok }
	if [ "$1" -ge 1 ]; then
		verdict WARN user-mcp "$1 user-scope MCP server(s) in $CJSON run a command (and $2 remote): each starts a process in EVERY Claude Code session, in every repository; one npx server measured about 220 MB a session, 2.4 GB across 12"
		fix "claude mcp list names them; re-add one most sessions never use with claude mcp add --scope project (the repository's .mcp.json) or --scope local (one project), then claude mcp remove <name> --scope user (no sudo; reversible: add it back with --scope user)"
	else
		verdict PASS user-mcp "no user-scope MCP server in $CJSON runs a command ($2 remote, which start no process)"
	fi ;;
*)
	verdict CANNOT-ASSESS user-mcp "$CJSON: the reader gave no answer"
	fix "claude mcp list by hand, as the user" ;;
esac

# codex-skill-copy (D-186): Codex 0.169.0 loads user skills from
# $HOME/.agents/skills and $CODEX_HOME/skills, and its /import copies
# ~/.claude/skills there once, never refreshes the copy, and rewrites the core
# skill's harness instructions into wrong ones ("under Codex Code, env -u
# CODEX_THREAD_ID"). So a SKILL.md there that is not the shipped one is a
# stale or rewritten copy a Codex session may load. Read only: test, cmp.
# Absence is proven only from a searchable directory (test -e is false under
# one that cannot be searched); a file that is not regular or not readable is
# CANNOT-ASSESS, never PASS. SKILL.md only: a stale reference file beside an
# identical SKILL.md is not seen. The fix is install-skill.sh with both knobs
# (an inherited BUDDY_FLEET_SKILL_DIR would send the fleet skill elsewhere),
# never rm: its destination judge (D-181) decides whether the directory is
# this install's to write.
SKILLS_SRC=$(dirname -- "$SCRIPTS")/skills
# judge_skill_dir <dir> — compares every shipped SKILL.md with <dir>/<name>/
# SKILL.md, adding to differ, unk, nseen and the fix text. One call per
# directory, the path always quoted: a loop over an unquoted list globbed
# CODEX_HOME=/tmp/codex[1] into /tmp/codex1 and judged a directory it never
# named (Codex code pass).
differ=""; unk=""; sfix=""; nseen=0
judge_skill_dir() {
	_dd=0
	for src in "$SKILLS_SRC"/*/SKILL.md; do
		[ -f "$src" ] || continue
		p="$1/$(basename -- "$(dirname -- "$src")")/SKILL.md"
		nseen=$((nseen + 1))
		if [ ! -e "$p" ] && [ ! -L "$p" ]; then
			absent_proven "$p" || unk="$unk $p"
		elif [ ! -f "$p" ] || [ ! -r "$p" ]; then
			unk="$unk $p"
		else
			# cmp: 0 the same, 1 different, anything else a read that failed.
			cmp -s "$src" "$p" && crc=0 || crc=$?
			case $crc in
			0) ;;
			1) differ="$differ $p"; _dd=1 ;;
			*) unk="$unk $p" ;;
			esac
		fi
	done
	[ "$_dd" = 0 ] || sfix="${sfix:+$sfix; then }BUDDY_SKILL_DIR=\"$1/buddy\" BUDDY_FLEET_SKILL_DIR=\"$1/buddy-fleet\" sh scripts/install-skill.sh"
}
if [ -z "${HOME:-}" ]; then
	verdict CANNOT-ASSESS codex-skill-copy "HOME is unset, so \$HOME/.agents/skills cannot be found"
	fix "run it from the user's own shell, with HOME set"
elif ! have cmp; then
	verdict CANNOT-ASSESS codex-skill-copy "no cmp to compare a Codex skill copy with the shipped one"
	fix "nothing to apply; compare \$HOME/.agents/skills/*/SKILL.md with this checkout's skills/ by hand"
else
	cshow="$HOME/.agents/skills"
	judge_skill_dir "$HOME/.agents/skills"
	cx="${CODEX_HOME:-$HOME/.codex}/skills"
	if [ "$cx" != "$HOME/.agents/skills" ]; then
		judge_skill_dir "$cx"; cshow="$cshow and $cx"
	fi
	if [ "$nseen" -eq 0 ]; then
		verdict CANNOT-ASSESS codex-skill-copy "no shipped skills under $SKILLS_SRC to compare with"
		fix "run this script from a buddy-system checkout"
	elif [ -n "$differ" ]; then
		verdict WARN codex-skill-copy "differs from this checkout's skills:$differ${unk:+ (and not assessed:$unk)}; a Codex session may load it, and a copy Codex's /import took is never refreshed (D-186)"
		fix "$sfix, from the checkout (no sudo; reversible: a replaced SKILL.md is kept as SKILL.md.bak; no delete: install-skill.sh judges whether that directory is this install's to write)"
	elif [ -n "$unk" ]; then
		verdict CANNOT-ASSESS codex-skill-copy "could not establish absence or equality for:$unk (not a regular file, unreadable, or under a directory that cannot be searched)"
		fix "ls -l each by hand, as the user; a copy that is there and differs is refreshed with BUDDY_SKILL_DIR=<dir>/buddy BUDDY_FLEET_SKILL_DIR=<dir>/buddy-fleet sh scripts/install-skill.sh"
	else
		verdict PASS codex-skill-copy "each shipped SKILL.md under $cshow is absent or byte-identical to this checkout's"
	fi
fi

# codex-gate (Codex report B1): one row, only where Codex's directory exists,
# from scripts/codex-gate-row.sh, the reader install.sh's report shares, so the
# two cannot drift. It runs `buddy diagnose --codex-hooks` (a read; cwd /, so
# no repository and no ledger) and writes nothing in Codex's directory.
if [ -r "$SCRIPTS/codex-gate-row.sh" ]; then
	. "$SCRIPTS/codex-gate-row.sh"
	if codex_gate_row "$(command -v buddy 2>/dev/null || true)"; then
		case $cgr_state in
		wired) verdict PASS codex-gate "$cgr_detail" ;;
		"NOT WIRED")
			verdict WARN codex-gate "NOT WIRED: $cgr_detail"
			fix "wire the gate by hand from docs/USAGE.md \"Opt-in wiring\", then trust it in Codex's /hooks (no sudo; reversible: remove the entry)" ;;
		*)
			verdict CANNOT-ASSESS codex-gate "$cgr_detail"
			fix "buddy diagnose --codex-hooks <absolute hooks.json> by hand, as the user" ;;
		esac
	fi
elif [ -d "${CODEX_HOME:-$HOME/.codex}" ]; then
	verdict CANNOT-ASSESS codex-gate "no $SCRIPTS/codex-gate-row.sh to read Codex's hooks.json with"
	fix "run this script from a buddy-system checkout"
fi

echo "env-check: $npass PASS, $nwarn WARN, $nfail FAIL, $ncant CANNOT-ASSESS. A WARN is advice; only a FAIL exits non-zero."
echo "env-check: not repeated here: hooks, gate entry, chat and handoff size (sh scripts/install.sh reports them); language servers (buddy init); harness compatibility (buddy diagnose)."
[ "$nfail" -eq 0 ]
