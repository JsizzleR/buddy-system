#!/bin/sh
# check-env-check.sh — done-check for scripts/env-check.sh (D-121).
#
# HERMETIC: sh, git and the repo. The machine is never judged: every command
# env-check.sh reads (uname, sw_vers, go, df, mdutil, ps, tmutil, pmset, du,
# crontab, buddy) is a STUB in a directory that is the whole PATH, beside
# symlinks to the real awk, sed, grep, dirname, basename, cat, id and git
# (/bin/sh is named absolutely). Each stub answers
# from a scenario directory and appends its argv to a log. HOME is a fixture;
# the checkout is a throwaway repo; the harness scratch root and the /tmp
# rule are pointed at the fixture by BUDDY_ENV_CHECK_TMP.
#
# WHAT MUST HOLD, each beside the control that proves the assertion could fire:
#   - READ-ONLY: after every run the log holds no mutating call (`go env -w`,
#     `mdutil -i`, `tmutil addexclusion`, `pmset -c`, `killall`, `sudo`, a
#     worktree move, ...) and the fixture tree lists the same files as before.
#     Control: a planted script that makes two such calls through the same
#     stubs, which the same detector MUST report.
#   - EVERY VERDICT ARM: a good machine is 12 PASS, exit 0, no fix line; a bad
#     one is FAIL (old Go, a full disk), WARN on every advice check, and exit
#     1; a non-macOS box is CANNOT-ASSESS wherever the tool is missing, never
#     PASS. A subfolder of an indexed volume is CANNOT-ASSESS, not PASS
#     (Spotlight's privacy list is unreadable without sudo).
#   - ONE fix line under every non-PASS line and none under a PASS; the count
#     line agrees with the lines above it.
#   - --list runs no command (an empty log); --help exits 0; an unknown flag
#     or a positional exits 2 with `refused:` last.
set -eu
. "$(dirname -- "$0")/lib.sh"
ROOT=$(repo_root "$0")
CDPATH= cd -- "$ROOT"
LC_ALL=C; export LC_ALL

fail() { echo "check-env-check: FAIL — $*" >&2; exit 1; }

WORK=$(mktemp -d) || fail "mktemp failed"
trap 'rm -rf "$WORK"' EXIT INT TERM
WORK=$(CDPATH= cd -- "$WORK" && pwd -P)

# The real tools env-check.sh may use that are not stubbed, by symlink, so
# the PATH can hold nothing else (that is how "python3 missing" is staged).
STD="$WORK/std"; mkdir -p "$STD"
for t in awk sed grep dirname basename cat id git; do
	p=$(command -v "$t") || fail "no $t on this machine's PATH"
	ln -s "$p" "$STD/$t"
done
PY=$(command -v python3) || fail "no python3 (the hermetic tier needs one)"

HOMEFX="$WORK/home"; mkdir -p "$HOMEFX/Library/LaunchAgents" "$HOMEFX/.claude/projects" "$HOMEFX/bin"
TMPFX="$WORK/tmp"; mkdir -p "$TMPFX/claude-$(id -u)"
CO="$WORK/checkout"; mkrepo "$CO"
# core.hooksPath pinned to an empty directory: a global hooksPath would run the
# user's own hooks on this commit, outside the fixture (Codex code pass).
mkdir -p "$WORK/nohooks"; git -C "$CO" config core.hooksPath "$WORK/nohooks"
printf 'x\n' > "$CO/f"; git -C "$CO" add f; git -C "$CO" -c commit.gpgsign=false commit -qm init
LOG="$WORK/log"

# stub <bin dir> <name> <body> — a stub that logs its call, then runs <body>
# with the scenario directory in $SC.
stub() {
	{
		printf '#!/bin/sh\nprintf "%%s %%s\\n" "$(basename -- "$0")" "$*" >> "$STUBLOG"\nSC=$STUB_SCENARIO\n'
		printf '%s\n' "$3"
	} > "$1/$2"
	chmod +x "$1/$2"
}
# mkbin <dir> <tools...> — the stub set for a scenario; a tool left off the
# list is missing on that machine.
mkbin() {
	d=$1; shift; mkdir -p "$d"
	for t in "$@"; do
		case $t in
		uname) stub "$d" uname 'cat "$SC/os"' ;;
		sw_vers) stub "$d" sw_vers 'echo 27.0.1' ;;
		go) stub "$d" go '[ "${GOTOOLCHAIN:-}" = local ] && [ "${GOTELEMETRY:-}" = off ] || { echo "stub go: not held to the installed toolchain (GOTOOLCHAIN=${GOTOOLCHAIN:-unset}, GOTELEMETRY=${GOTELEMETRY:-unset}); a bare go can download a toolchain" >&2; exit 1; }
case "$1 ${2:-}" in
"version ") echo "go version go$(cat "$SC/go_version") darwin/arm64" ;;
"env GOCACHE") cat "$SC/gocache" ;;
*) echo "stub go: unexpected $*" >&2; exit 1 ;;
esac' ;;
		python3) ln -s "$PY" "$d/python3" ;;
		buddy) stub "$d" buddy 'exit 0' ;;
		df) stub "$d" df 'p=$2; m=/System/Volumes/Data
[ -n "${STUB_CACHEVOL:-}" ] && case $p in "$STUB_CACHEVOL"|"$STUB_CACHEVOL"/*) m=$STUB_CACHEVOL ;; esac
[ -n "${STUB_OWNVOL:-}" ] && [ "$p" = "$STUB_OWNVOL" ] && m=$p
echo "Filesystem 1024-blocks Used Available Capacity Mounted on"
echo "/dev/disk3s5 1000 500 $(cat "$SC/df_avail") $(cat "$SC/df_used")% $m"' ;;
		mdutil) stub "$d" mdutil '[ "$1" = -s ] || { echo "stub mdutil: only -s is read-only" >&2; exit 1; }
f=$SC/mdutil; [ -n "${STUB_CACHEVOL:-}" ] && [ "$2" = "$STUB_CACHEVOL" ] && f=$SC/mdutil_cache
case $(cat "$f") in on) printf "%s:\n\tIndexing enabled. \n" "$2" ;; off) printf "%s:\n\tIndexing and searching disabled.\n" "$2" ;; *) printf "%s:\n\tError: unknown indexing state.\n" "$2" ;; esac' ;;
		ps) stub "$d" ps 'echo " 1000  0.1 /usr/libexec/other"
[ -s "$SC/fseventsd" ] && echo " $(cat "$SC/fseventsd") /System/Library/Frameworks/CoreServices.framework/Versions/A/Frameworks/FSEvents.framework/Versions/A/Support/fseventsd"
echo " 2000  0.0 /sbin/launchd"' ;;
		tmutil) stub "$d" tmutil 'case $1 in
destinationinfo) if [ "$(cat "$SC/tm_dest")" = none ]; then echo "tmutil: No destinations configured."; exit 1; else echo "Name : Backup"; fi ;;
isexcluded) echo "[$(cat "$SC/tm_excluded")]  $2" ;;
*) echo "stub tmutil: $1 is not read-only" >&2; exit 1 ;;
esac' ;;
		pmset) stub "$d" pmset '[ "$1 $2" = "-g custom" ] || { echo "stub pmset: $* is not a read" >&2; exit 1; }
printf "Battery Power:\n sleep                1\nAC Power:\n displaysleep         0\n"
[ "$(cat "$SC/pm_sleep")" = absent ] || printf " sleep                %s\n" "$(cat "$SC/pm_sleep")"
printf " disksleep            10\n"' ;;
		du) stub "$d" du '[ -e "$SC/du_fail" ] && { echo "du: $3: Permission denied" >&2; exit 1; }
printf "%s\t%s\n" "$(cat "$SC/du_kb")" "$3"' ;;
		crontab) stub "$d" crontab 'if [ -s "$SC/crontab" ]; then cat "$SC/crontab"; else echo "crontab: no crontab" >&2; exit 1; fi' ;;
		# Mutators a careless edit might reach for: stubbed so the log, not the
		# machine, sees them.
		sudo|killall|caffeinate|launchctl) stub "$d" "$t" 'exit 0' ;;
		*) fail "mkbin: no stub for $t" ;;
		esac
	done
}

# scenario <dir> key=value... — the answers the stubs read.
scenario() {
	d=$1; shift; mkdir -p "$d"
	for kv in "$@"; do printf '%s\n' "${kv#*=}" > "$d/${kv%%=*}"; done
}

# run <bin> <scenario> [args...] — env-check under the fixture; output in
# $out, status in $rc, the log cleared first.
# Every run, not only the ones a case thinks of (Codex code pass: a write in
# one verdict arm escapes a case that checks another), is held to: nothing
# written or changed under the fixture (names AND contents), no mutating call
# in the log.
run() {
	bin=$1; sc=$2; shift 2
	: > "$LOG"
	_before=$(snapshot)
	out=$(cd "$CO" && env -i HOME="$HOMEFX" PATH="$bin:$STD" STUBLOG="$LOG" STUB_SCENARIO="$sc" \
		STUB_CACHEVOL="${STUB_CACHEVOL:-}" STUB_OWNVOL="${STUB_OWNVOL:-}" BUDDY_ENV_CHECK_TMP="$TMPFX" \
		/bin/sh "$ROOT/scripts/env-check.sh" "$@" 2>&1) && rc=0 || rc=$?
	[ "$_before" = "$(snapshot)" ] || fail "a run ($*) wrote into the fixture:
$out"
	[ -z "$(mutations "$LOG")" ] || fail "a run ($*) made a mutating call: $(mutations "$LOG")"
}

# mutations <log> — every logged call that would change the machine.
mutations() {
	grep -E '^(go env -w|go env -u|mdutil -i|mdutil -E|mdutil -X|tmutil (add|remove)exclusion|pmset -c|pmset -a|pmset -b|killall|sudo|caffeinate|launchctl|crontab -[er]|git .*worktree (move|add|remove|prune))' "$1" || true
}
# The control: a planted pair of mutating calls, through the same stubs, that
# the detector MUST report, beside a read it must NOT.
CTL="$WORK/ctl"; mkbin "$CTL" go mdutil sudo
: > "$LOG"
env PATH="$CTL:$STD" STUBLOG="$LOG" STUB_SCENARIO="$WORK/none" /bin/sh -c 'go env -w GOCACHE=/x >/dev/null 2>&1; sudo mdutil -i off / >/dev/null 2>&1; mdutil -s / >/dev/null 2>&1' || true
got=$(mutations "$LOG" | sed 's/ .*//' | tr '\n' ' ')
[ "$got" = "go sudo " ] || fail "the mutation detector reported '$got' of its planted control, want 'go sudo ' — the gate is broken, not the script"

# snapshot — the fixture tree's names and every regular file's checksum, so an
# overwrite of an existing file shows as well as a new one (Codex code pass).
snapshot() {
	find "$HOMEFX" "$CO" "$TMPFX" -print | sort
	find "$HOMEFX" "$CO" "$TMPFX" -type f -print | sort | while IFS= read -r f; do cksum < "$f"; done
}
# The control: a changed byte in an existing file moves the snapshot.
_s0=$(snapshot); printf 'y\n' > "$CO/f"; _s1=$(snapshot); printf 'x\n' > "$CO/f"
[ "$_s0" != "$_s1" ] || fail "the snapshot did not see an overwrite of an existing file — the gate is broken, not the script"
[ "$_s0" = "$(snapshot)" ] || fail "the snapshot control did not restore"

# verdicts <out> — the verdict column of every check line, in order.
verdicts() { printf '%s\n' "$1" | sed -n 's/^\[\([A-Z-]*\)\] *\([a-z-]*\) .*/\1 \2/p'; }
# shape <out> — one fix line under every non-PASS, none under a PASS, and the
# count line equal to the lines.
shape() {
	printf '%s\n' "$1" | awk '
		/^\[/ { if (pending) { bad = bad " no-fix:" pname } v = $1; pname = $2; pending = (v != "[PASS]"); n[v]++; next }
		/^ *fix: / { if (!pending) bad = bad " fix-under-pass:" pname; pending = 0; next }
		/^env-check: [0-9]+ PASS/ {
			if (pending) bad = bad " no-fix:" pname
			pending = 0
			split($0, a, /[ ,]+/)
			if (a[2] != n["[PASS]"] + 0 || a[4] != n["[WARN]"] + 0 || a[6] != n["[FAIL]"] + 0 || a[8] != n["[CANNOT-ASSESS]"] + 0) bad = bad " count-line"
		}
		END { print bad }'
}

ALL="uname sw_vers go python3 buddy df mdutil ps tmutil pmset du crontab sudo killall caffeinate launchctl"

# QA-1: a good machine: every check PASS, exit 0, nothing written, no fix line.
BIN_A="$WORK/bin-a"; mkbin "$BIN_A" $ALL
SC_A="$WORK/sc-a"
scenario "$SC_A" os=Darwin go_version=1.27.1 gocache="$HOMEFX/Library/Caches/go-build" df_avail=104857600 df_used=50 \
	mdutil=off fseventsd="2048  0.3" tm_dest=none tm_excluded=Excluded pm_sleep=0 du_kb=1048576
printf '<plist><string>%s</string></plist>\n' 'go clean -cache' > "$HOMEFX/Library/LaunchAgents/com.example.trim.plist"
: > "$TMPFX/claude-$(id -u)/one"
before=$(snapshot)
run "$BIN_A" "$SC_A" --checkout "$CO"
[ "$rc" -eq 0 ] || fail "QA-1: a good machine must exit 0 (rc=$rc):
$out"
[ "$(printf '%s\n' "$out" | grep -c '^\[PASS\]')" -eq 12 ] || fail "QA-1: want 12 PASS lines:
$out"
printf '%s\n' "$out" | grep -q 'fix:' && fail "QA-1: a PASS has a fix line:
$out"
printf '%s\n' "$out" | grep -qx 'env-check: 12 PASS, 0 WARN, 0 FAIL, 0 CANNOT-ASSESS\. A WARN is advice; only a FAIL exits non-zero\.' || fail "QA-1: the count line:
$out"
printf '%s\n' "$out" | grep -q '^env-check: not repeated here: hooks, gate entry, chat and handoff size (sh scripts/install.sh reports them); language servers (buddy init); harness compatibility (buddy diagnose)\.$' || fail "QA-1: the footer must name what install.sh, buddy init and buddy diagnose report:
$out"
[ -z "$(mutations "$LOG")" ] || fail "QA-1: a mutating call was made: $(mutations "$LOG")"
[ "$before" = "$(snapshot)" ] || fail "QA-1: the run wrote into the fixture"
# The stubs were reached (the control that the scenario drove the verdicts):
# each read-only command appears in the log at least once.
for t in uname go df mdutil ps tmutil pmset du; do
	grep -q "^$t " "$LOG" || fail "QA-1: $t was never called; the PASS lines came from somewhere else"
done

# QA-2: a subfolder of an INDEXED volume is CANNOT-ASSESS, never PASS, and a
# .noindex cache is PASS on the same volume by its name; fseventsd busy on CPU
# alone is a WARN pointing at the spotlight lines.
SC_A2="$WORK/sc-a2"; cp -R "$SC_A" "$SC_A2"
scenario "$SC_A2" mdutil=on gocache="$HOMEFX/Library/Caches/go-build.noindex" fseventsd="1232 99.7"
run "$BIN_A" "$SC_A2" --checkout "$CO"
[ "$rc" -eq 0 ] || fail "QA-2: CANNOT-ASSESS and WARN must not fail the run (rc=$rc)"
verdicts "$out" | grep -qx 'CANNOT-ASSESS checkout-spotlight' || fail "QA-2: a subfolder of an indexed volume must be CANNOT-ASSESS:
$out"
printf '%s\n' "$out" | grep -q 'privacy list is unreadable without sudo' || fail "QA-2: the CANNOT-ASSESS must say why it could not tell"
verdicts "$out" | grep -qx 'PASS go-cache-spotlight' || fail "QA-2: a .noindex cache on an indexed volume is PASS by its name:
$out"
verdicts "$out" | grep -qx 'WARN fseventsd' || fail "QA-2: fseventsd at 99.7% CPU is a WARN:
$out"
printf '%s\n' "$out" | grep -q 'fix: the two spotlight lines above' || fail "QA-2: the CPU WARN's fix points at the spotlight lines"
[ -z "$(shape "$out")" ] || fail "QA-2: shape:$(shape "$out")
$out"

# QA-3: a bad machine. Old Go and a near-full disk FAIL; every advice check
# WARNs; the checkout is its own indexed volume (the sudo mdutil fix); exit
# 1; one fix line under each; still nothing written and nothing mutated.
BIN_B="$WORK/bin-b"; mkbin "$BIN_B" uname sw_vers go df mdutil ps tmutil pmset du crontab sudo killall caffeinate launchctl
SC_B="$WORK/sc-b"
scenario "$SC_B" os=Darwin go_version=1.25.3 gocache="$HOMEFX/Library/Caches/go-build" df_avail=1048576 df_used=99 \
	mdutil=on fseventsd="2097152  3.0" tm_dest=yes tm_excluded=Included pm_sleep=10 du_kb=6291456
rm -f "$HOMEFX/Library/LaunchAgents/com.example.trim.plist"
git -C "$CO" worktree add -q "$TMPFX/wt-under-tmp" HEAD
before=$(snapshot)
STUB_OWNVOL="$CO" run "$BIN_B" "$SC_B" --checkout "$CO"
[ "$rc" -eq 1 ] || fail "QA-3: a FAIL must exit 1 (rc=$rc):
$out"
want='PASS platform
FAIL toolchain
WARN path-bin
FAIL disk-free
WARN go-cache-spotlight
WARN checkout-spotlight
WARN fseventsd
WARN go-cache-trim
WARN time-machine
WARN ac-sleep
WARN worktrees-tmp
WARN scratch'
[ "$(verdicts "$out")" = "$want" ] || fail "QA-3: verdicts:
$(verdicts "$out")
want:
$want
$out"
[ -z "$(shape "$out")" ] || fail "QA-3: shape:$(shape "$out")
$out"
printf '%s\n' "$out" | grep -q 'fix: sudo mdutil -i off ' || fail "QA-3: the own-volume fix names sudo mdutil -i off"
printf '%s\n' "$out" | grep -q 'go env -w GOCACHE=.*\.noindex.*no sudo; reversible' || fail "QA-3: the cache fix is the no-sudo reversible one"
printf '%s\n' "$out" | grep -q 'fix: sudo tmutil addexclusion -p .*sudo; reversible' || fail "QA-3: the Time Machine fix says sudo and reversible"
printf '%s\n' "$out" | grep -q 'sudo killall fseventsd (sudo;' || fail "QA-3: the fseventsd fix says sudo"
printf '%s\n' "$out" | grep -q 'worktree(s) under '"$TMPFX"':.*wt-under-tmp' || fail "QA-3: the /tmp worktree is named:
$out"
printf '%s\n' "$out" | grep -q 'not installed yet' || fail "QA-3: no buddy anywhere reads as not installed"
printf '%s\n' "$out" | grep -q 'irreversible' || fail "QA-3: the scratch fix says irreversible"
[ -z "$(mutations "$LOG")" ] || fail "QA-3: a mutating call was made: $(mutations "$LOG")"
[ "$before" = "$(snapshot)" ] || fail "QA-3: the run wrote into the fixture"

# QA-4: a buddy built but not on PATH is the PATH advice, not "not installed";
# a missing python3 is a WARN on toolchain with go and git fine.
SC_B2="$WORK/sc-b2"; cp -R "$SC_B" "$SC_B2"; scenario "$SC_B2" go_version=1.26.8
printf '#!/bin/sh\n' > "$HOMEFX/bin/buddy"; chmod +x "$HOMEFX/bin/buddy"
STUB_OWNVOL="$CO" run "$BIN_B" "$SC_B2" --checkout "$CO"
printf '%s\n' "$out" | grep -q 'is built but \$HOME/bin is not on this shell'"'"'s PATH' || fail "QA-4: a built buddy off PATH:
$out"
verdicts "$out" | grep -qx 'WARN toolchain' || fail "QA-4: no python3 with go 1.26.8 is a WARN, not a FAIL:
$out"
printf '%s\n' "$out" | grep -q 'python3 MISSING' || fail "QA-4: the WARN names python3"
rm -f "$HOMEFX/bin/buddy"

# QA-5: not macOS, the tools missing: CANNOT-ASSESS where a tool is missing,
# never PASS; a trim in crontab is found; exit 0.
BIN_C="$WORK/bin-c"; mkbin "$BIN_C" uname go python3 buddy df ps du crontab
SC_C="$WORK/sc-c"
scenario "$SC_C" os=Linux go_version=1.27.0 gocache="$HOMEFX/.cache/go-build" df_avail=104857600 df_used=40 \
	fseventsd= du_kb=1024 crontab='0 */3 * * * go clean -cache'
git -C "$CO" worktree remove --force "$TMPFX/wt-under-tmp"
run "$BIN_C" "$SC_C" --checkout "$CO"
[ "$rc" -eq 0 ] || fail "QA-5: CANNOT-ASSESS must not fail the run (rc=$rc):
$out"
want='WARN platform
PASS toolchain
PASS path-bin
PASS disk-free
CANNOT-ASSESS go-cache-spotlight
CANNOT-ASSESS checkout-spotlight
CANNOT-ASSESS fseventsd
PASS go-cache-trim
CANNOT-ASSESS time-machine
CANNOT-ASSESS ac-sleep
PASS worktrees-tmp
PASS scratch'
[ "$(verdicts "$out")" = "$want" ] || fail "QA-5: verdicts:
$(verdicts "$out")
want:
$want
$out"
[ -z "$(shape "$out")" ] || fail "QA-5: shape:$(shape "$out")
$out"
printf '%s\n' "$out" | grep -q 'scheduled: crontab' || fail "QA-5: the crontab trim was not found"
[ -z "$(mutations "$LOG")" ] || fail "QA-5: a mutating call was made: $(mutations "$LOG")"

# QA-7: a read that failed is CANNOT-ASSESS, never PASS (Codex code pass): a
# checkout that is not a directory (no mount to judge, no worktrees to list),
# a du that fails on the transcripts, a crontab whose only trim line is a
# comment and a plist that only SETS GOCACHE (neither schedules anything).
# QA-1 is the control: the same checks PASS when the reads succeed.
SC_D="$WORK/sc-d"; cp -R "$SC_A" "$SC_D"
scenario "$SC_D" crontab='# 0 */3 * * * go clean -cache'
: > "$SC_D/du_fail"
printf '<plist><key>EnvironmentVariables</key><dict><key>GOCACHE</key><string>/x</string></dict></plist>\n' > "$HOMEFX/Library/LaunchAgents/com.example.env.plist"
run "$BIN_A" "$SC_D" --checkout "$WORK/missing"
[ "$rc" -eq 0 ] || fail "QA-7: CANNOT-ASSESS must not fail the run (rc=$rc):
$out"
for v in 'CANNOT-ASSESS checkout-spotlight' 'CANNOT-ASSESS worktrees-tmp' 'CANNOT-ASSESS scratch' 'WARN go-cache-trim'; do
	verdicts "$out" | grep -qx "$v" || fail "QA-7: want '$v' when the read fails:
$out"
done
printf '%s\n' "$out" | grep -q 'is not a directory' || fail "QA-7: the missing checkout is named as such"
printf '%s\n' "$out" | grep -q 'du failed or no permission' || fail "QA-7: the failed du is named"
[ -z "$(shape "$out")" ] || fail "QA-7: shape:$(shape "$out")
$out"
rm -f "$HOMEFX/Library/LaunchAgents/com.example.env.plist"
# A go stub that sees the holding env is the control that the script sets it:
# QA-1's toolchain PASS came through this stub, which refuses without it.
grep -q '^go version' "$LOG" || fail "QA-7: go version was not reached"

# QA-6: --list runs nothing and names every check; --help exits 0; an
# unknown flag and a positional are refused, exit 2, `refused:` last.
run "$BIN_A" "$SC_A" --checkout "$CO" --list
[ "$rc" -eq 0 ] || fail "QA-6: --list rc=$rc"
[ ! -s "$LOG" ] || fail "QA-6: --list ran a command: $(cat "$LOG")"
[ "$(printf '%s\n' "$out" | wc -l | tr -d ' ')" -eq 12 ] || fail "QA-6: --list must name the 12 checks:
$out"
for c in platform toolchain path-bin disk-free go-cache-spotlight checkout-spotlight fseventsd go-cache-trim time-machine ac-sleep worktrees-tmp scratch; do
	printf '%s\n' "$out" | grep -q "^$c " || fail "QA-6: --list does not name $c"
done
run "$BIN_A" "$SC_A" --checkout "$CO" --help
[ "$rc" -eq 0 ] && printf '%s\n' "$out" | grep -q '^usage: sh scripts/env-check.sh' || fail "QA-6: --help (rc=$rc):
$out"
run "$BIN_A" "$SC_A" --checkout "$CO" --bogus
[ "$rc" -eq 2 ] || fail "QA-6: an unknown flag must exit 2 (rc=$rc)"
[ "$(printf '%s\n' "$out" | tail -1)" = "refused: unknown flag --bogus" ] || fail "QA-6: the refusal line:
$out"
run "$BIN_A" "$SC_A" --checkout "$CO" stray
[ "$rc" -eq 2 ] && printf '%s\n' "$out" | tail -1 | grep -q '^refused: ' || fail "QA-6: a positional must be refused (rc=$rc):
$out"
[ ! -s "$LOG" ] || fail "QA-6: a refused call ran a command"

echo "check-env-check: GREEN (QA-1 good machine 12 PASS + stubs reached, QA-2 indexed subfolder CANNOT-ASSESS + .noindex PASS + CPU WARN, QA-3 bad machine 2 FAIL 9 WARN exit 1 + fix shapes, QA-4 PATH advice + python3 WARN, QA-5 non-macOS CANNOT-ASSESS never PASS, QA-7 failed reads CANNOT-ASSESS + comment/env-only trim not scheduled + go held to the installed toolchain, QA-6 --list runs nothing + --help + refusals; every run: no write (names + contents), no mutating call; planted mutation and overwrite controls)"
