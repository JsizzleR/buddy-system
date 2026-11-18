#!/bin/sh
# check-env-check.sh — done-check for scripts/env-check.sh (D-121).
#
# HERMETIC: sh, git and the repo. The machine is never judged: every command
# env-check.sh reads (uname, sw_vers, go, df, mdutil, ps, tmutil, pmset, du,
# crontab, sysctl, buddy) is a STUB in a directory that is the whole PATH, beside
# symlinks to the real awk, sed, grep, dirname, basename, cat, id, git and
# cmp (/bin/sh is named absolutely). Each stub answers
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
#   - EVERY VERDICT ARM: a good machine is 18 PASS, exit 0, no fix line; a bad
#     one is FAIL (old Go, a full disk), WARN on every advice check, and exit
#     1; a non-macOS box is CANNOT-ASSESS wherever the tool is missing, never
#     PASS. A subfolder of an indexed volume is CANNOT-ASSESS, not PASS
#     (Spotlight's privacy list is unreadable without sudo).
#   - ONE fix line under every non-PASS line and none under a PASS; the count
#     line agrees with the lines above it.
#   - --list runs no command (an empty log); --help exits 0; an unknown flag
#     or a positional exits 2 with `refused:` last.
#   - THE SETTINGS AND SKILL-COPY ROWS (QA-9; D-188, D-187, D-190, D-186): each arm
#     beside its control. Settings are read through gate-hook.py's decode, and
#     a NaN file, which a plain json.loads would read, is CANNOT-ASSESS; a
#     Codex skill copy is WARN only when readable and different, and absence
#     under a directory that cannot be searched is not proof.
#   - THE MEMORY ROWS (QA-10; D-209, #144): ram-sessions at its 2048 MB a
#     session boundary, counted over the larger of 12 and the claude processes
#     ps shows, CANNOT-ASSESS with no RAM size; user-mcp WARN only on a
#     user-scope server that runs a command, never on a remote or local-scope
#     one, CANNOT-ASSESS on a file it cannot read.
set -eu
. "$(dirname -- "$0")/lib.sh"
ROOT=$(repo_root "$0")
CDPATH= cd -- "$ROOT"
LC_ALL=C; export LC_ALL

fail() { echo "check-env-check: FAIL — $*" >&2; exit 1; }

WORK=$(mktemp -d) || fail "mktemp failed"
# u+rwx first: a case that fails while a mode-000 directory is staged (QA-9)
# would otherwise leave the fixture behind.
trap 'chmod -R u+rwx "$WORK" 2>/dev/null; rm -rf "$WORK"' EXIT INT TERM
WORK=$(CDPATH= cd -- "$WORK" && pwd -P)

# The real tools env-check.sh may use that are not stubbed, by symlink, so
# the PATH can hold nothing else (that is how "python3 missing" is staged).
STD="$WORK/std"; mkdir -p "$STD"
for t in awk sed grep dirname basename cat id git cmp; do
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
		# buddy answers `diagnose --codex-hooks` from the scenario when it
		# has an answer (QA-8), else nothing. The real diagnose's output is
		# check-install.sh QA-18's, over the binary install.sh built.
		buddy) stub "$d" buddy '[ -f "$SC/buddy_out" ] || exit 0
pwd > "$SC/buddy_cwd"
cat "$SC/buddy_out"; exit "$(cat "$SC/buddy_rc" 2>/dev/null || echo 0)"' ;;
		df) stub "$d" df 'p=$2; m=/System/Volumes/Data
[ -n "${STUB_CACHEVOL:-}" ] && case $p in "$STUB_CACHEVOL"|"$STUB_CACHEVOL"/*) m=$STUB_CACHEVOL ;; esac
[ -n "${STUB_OWNVOL:-}" ] && [ "$p" = "$STUB_OWNVOL" ] && m=$p
echo "Filesystem 1024-blocks Used Available Capacity Mounted on"
echo "/dev/disk3s5 1000 500 $(cat "$SC/df_avail") $(cat "$SC/df_used")% $m"' ;;
		mdutil) stub "$d" mdutil '[ "$1" = -s ] || { echo "stub mdutil: only -s is read-only" >&2; exit 1; }
f=$SC/mdutil; [ -n "${STUB_CACHEVOL:-}" ] && [ "$2" = "$STUB_CACHEVOL" ] && f=$SC/mdutil_cache
case $(cat "$f") in on) printf "%s:\n\tIndexing enabled. \n" "$2" ;; off) printf "%s:\n\tIndexing and searching disabled.\n" "$2" ;; *) printf "%s:\n\tError: unknown indexing state.\n" "$2" ;; esac' ;;
		ps) stub "$d" ps '[ -e "$SC/ps_empty" ] && exit 1
echo " 1000  0.1 /usr/libexec/other"
[ -s "$SC/fseventsd" ] && echo " $(cat "$SC/fseventsd") /System/Library/Frameworks/CoreServices.framework/Versions/A/Frameworks/FSEvents.framework/Versions/A/Support/fseventsd"
i=0; while [ "$i" -lt "$(cat "$SC/claude_n" 2>/dev/null || echo 0)" ]; do echo " 250000  1.0 /Users/u/.local/bin/claude"; i=$((i + 1)); done
echo " 3000  0.0 /usr/local/bin/claude-helper"
echo " 2000  0.0 /sbin/launchd"' ;;
		sysctl) stub "$d" sysctl '[ "$*" = "-n hw.memsize" ] || { echo "stub sysctl: $* is not the one read" >&2; exit 1; }
[ -s "$SC/memsize" ] || { echo "sysctl: unknown oid" >&2; exit 1; }
cat "$SC/memsize"' ;;
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
	# FX_HOME (default the fixture HOME; empty is staged as empty), FX_SUMMARIES
	# (the process value of the summaries knob), FX_CODEMODE (Code Mode's, D-190)
	# and FX_CODEX_HOME are the
	# per-case environment; unset, each is absent from the run. FX_HOME_UNSET
	# (any value) runs with no HOME at all. A case sets
	# one, runs, and unsets it: `V=x run` would keep V after a function in sh.
	out=$(cd "$CO" && env -i HOME="${FX_HOME-$HOMEFX}" PATH="$bin:$STD" STUBLOG="$LOG" STUB_SCENARIO="$sc" \
		STUB_CACHEVOL="${STUB_CACHEVOL:-}" STUB_OWNVOL="${STUB_OWNVOL:-}" BUDDY_ENV_CHECK_TMP="$TMPFX" \
		${FX_SUMMARIES+"CLAUDE_CODE_DISABLE_AGENT_PROGRESS_SUMMARIES=$FX_SUMMARIES"} \
		${FX_CODEMODE+"CLAUDE_CODE_EXPERIMENTAL_CODE_MODE=$FX_CODEMODE"} \
		${FX_CODEX_HOME+"CODEX_HOME=$FX_CODEX_HOME"} \
		${FX_HOME_UNSET+/usr/bin/env -u HOME} /bin/sh "$ROOT/scripts/env-check.sh" "$@" 2>&1) && rc=0 || rc=$?
	[ "$_before" = "$(snapshot)" ] || fail "a run ($*) wrote into the fixture:
$out"
	[ -z "$(mutations "$LOG")" ] || fail "a run ($*) made a mutating call: $(mutations "$LOG")"
}

# mutations <log> — every logged call that would change the machine.
mutations() {
	grep -E '^(sysctl -w|sysctl [^ ]*=|go env -w|go env -u|mdutil -i|mdutil -E|mdutil -X|tmutil (add|remove)exclusion|pmset -c|pmset -a|pmset -b|killall|sudo|caffeinate|launchctl|crontab -[er]|git .*worktree (move|add|remove|prune))' "$1" || true
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
# find's complaint about a mode-000 directory (QA-9 stages two) is part of the
# snapshot, not noise on the terminal: the same before and after.
# scripts/__pycache__ is in it too: env-check imports gate-hook.py, and
# without -B Python writes bytecode beside it, outside the fixture (Codex
# code pass).
snapshot() {
	find "$HOMEFX" "$CO" "$TMPFX" "$ROOT/scripts" -path "$ROOT/scripts/*" ! -path "$ROOT/scripts/__pycache__*" -prune -o -print 2>&1 | sort
	find "$HOMEFX" "$CO" "$TMPFX" "$ROOT/scripts/__pycache__" -type f -print 2>/dev/null | sort | while IFS= read -r f; do if [ -r "$f" ]; then cksum < "$f"; else ls -l "$f"; fi; done
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

ALL="uname sw_vers go python3 buddy df mdutil ps tmutil pmset du crontab sysctl sudo killall caffeinate launchctl"

# QA-1: a good machine: every check PASS, exit 0, nothing written, no fix line.
BIN_A="$WORK/bin-a"; mkbin "$BIN_A" $ALL
SC_A="$WORK/sc-a"
scenario "$SC_A" os=Darwin go_version=1.27.1 gocache="$HOMEFX/Library/Caches/go-build" df_avail=104857600 df_used=50 \
	mdutil=off fseventsd="2048  0.3" tm_dest=none tm_excluded=Excluded pm_sleep=0 du_kb=1048576 \
	memsize=68719476736 claude_n=3
# user-mcp's good file: a remote user-scope server and a LOCAL-scope one that
# runs a command, neither of which starts a process in every session.
CJ="$HOMEFX/.claude.json"
GOODCJ='{"mcpServers": {"docs": {"type": "http", "url": "https://example.invalid/mcp"}}, "projects": {"/p": {"mcpServers": {"pw": {"command": "npx"}}}}}'
printf '%s\n' "$GOODCJ" > "$CJ"
printf '<plist><string>%s</string></plist>\n' 'go clean -cache' > "$HOMEFX/Library/LaunchAgents/com.example.trim.plist"
: > "$TMPFX/claude-$(id -u)/one"
SET="$HOMEFX/.claude/settings.json"
GOODSET='{"env": {"CLAUDE_CODE_DISABLE_AGENT_PROGRESS_SUMMARIES": "1"}}'
printf '%s\n' "$GOODSET" > "$SET"
before=$(snapshot)
run "$BIN_A" "$SC_A" --checkout "$CO"
[ "$rc" -eq 0 ] || fail "QA-1: a good machine must exit 0 (rc=$rc):
$out"
[ "$(printf '%s\n' "$out" | grep -c '^\[PASS\]')" -eq 18 ] || fail "QA-1: want 18 PASS lines:
$out"
printf '%s\n' "$out" | grep -q 'fix:' && fail "QA-1: a PASS has a fix line:
$out"
printf '%s\n' "$out" | grep -qx 'env-check: 18 PASS, 0 WARN, 0 FAIL, 0 CANNOT-ASSESS\. A WARN is advice; only a FAIL exits non-zero\.' || fail "QA-1: the count line:
$out"
printf '%s\n' "$out" | grep -q '^env-check: not repeated here: hooks, gate entry, chat and handoff size (sh scripts/install.sh reports them); language servers (buddy init); harness compatibility (buddy diagnose)\.$' || fail "QA-1: the footer must name what install.sh, buddy init and buddy diagnose report:
$out"
[ -z "$(mutations "$LOG")" ] || fail "QA-1: a mutating call was made: $(mutations "$LOG")"
[ "$before" = "$(snapshot)" ] || fail "QA-1: the run wrote into the fixture"
# The stubs were reached (the control that the scenario drove the verdicts):
# each read-only command appears in the log at least once.
for t in uname go df mdutil ps tmutil pmset du sysctl; do
	grep -q "^$t " "$LOG" || fail "QA-1: $t was never called; the PASS lines came from somewhere else"
done

# QA-1b (#135): a buddy on PATH that is NOT the file install builds at
# $HOME/bin/buddy (the one the hooks name) is a WARN naming both: the commit
# gate and every typed verb run the PATH one, and a stale copy is refused on a
# newer ledger (D-037) while this row said PASS. Never a fix applied. The
# control: the same file reached through a link is the PASS of QA-1.
printf '#!/bin/sh\n' > "$HOMEFX/bin/buddy"; chmod +x "$HOMEFX/bin/buddy"
run "$BIN_A" "$SC_A" --checkout "$CO"
[ "$rc" -eq 0 ] || fail "QA-1b: a shadowing buddy is a WARN and must not fail the run (rc=$rc)"
verdicts "$out" | grep -qx 'WARN path-bin' || fail "QA-1b: a buddy on PATH other than \$HOME/bin/buddy must WARN:
$out"
printf '%s\n' "$out" | grep -qF "$BIN_A/buddy" || fail "QA-1b: the WARN names the PATH copy:
$out"
printf '%s\n' "$out" | grep -qF "$HOMEFX/bin/buddy" || fail "QA-1b: the WARN names the installed copy:
$out"
[ -z "$(shape "$out")" ] || fail "QA-1b: shape:$(shape "$out")
$out"
[ -z "$(mutations "$LOG")" ] || fail "QA-1b: a mutating call was made: $(mutations "$LOG")"
rm -f "$HOMEFX/bin/buddy"; ln -s "$BIN_A/buddy" "$HOMEFX/bin/buddy"
run "$BIN_A" "$SC_A" --checkout "$CO"
verdicts "$out" | grep -qx 'PASS path-bin' || fail "QA-1b: the same file through a link is a PASS (the control):
$out"
rm -f "$HOMEFX/bin/buddy"

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
BIN_B3="$WORK/bin-b3"; mkbin "$BIN_B3" uname sw_vers go python3 df mdutil ps tmutil pmset du crontab sysctl sudo killall caffeinate launchctl
SC_B="$WORK/sc-b"
scenario "$SC_B" os=Darwin go_version=1.25.3 gocache="$HOMEFX/Library/Caches/go-build" df_avail=1048576 df_used=99 \
	mdutil=on fseventsd="2097152  3.0" tm_dest=yes tm_excluded=Included pm_sleep=10 du_kb=6291456 \
	memsize=25769803776 claude_n=15
printf '%s\n' '{"mcpServers": {"pw": {"command": "npx", "args": ["-y", "x"]}, "fs": {"type": "stdio", "command": "node"}, "docs": {"type": "http", "url": "https://example.invalid/mcp"}}}' > "$CJ"
rm -f "$HOMEFX/Library/LaunchAgents/com.example.trim.plist"
git -C "$CO" worktree add -q "$TMPFX/wt-under-tmp" HEAD
printf '{"sandbox": {"enabled": true}, "env": {"CLAUDE_CODE_EXPERIMENTAL_CODE_MODE": "1"}}\n' > "$SET"
mkdir -p "$HOMEFX/.agents/skills/buddy"
sed 's/Claude Code/Codex/g; s/CLAUDE\.md/AGENTS.md/g' "$ROOT/skills/buddy/SKILL.md" > "$HOMEFX/.agents/skills/buddy/SKILL.md"
before=$(snapshot)
STUB_OWNVOL="$CO" run "$BIN_B3" "$SC_B" --checkout "$CO"
[ "$rc" -eq 1 ] || fail "QA-3: a FAIL must exit 1 (rc=$rc):
$out"
want='PASS platform
FAIL toolchain
WARN path-bin
FAIL disk-free
WARN go-cache-spotlight
WARN checkout-spotlight
WARN fseventsd
WARN ram-sessions
WARN go-cache-trim
WARN time-machine
WARN ac-sleep
WARN worktrees-tmp
WARN scratch
WARN sandbox
WARN summaries
WARN code-mode
WARN user-mcp
WARN codex-skill-copy'
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
printf '%s\n' "$out" | grep -q 'fix: "env": {"CLAUDE_CODE_DISABLE_AGENT_PROGRESS_SUMMARIES": "1"} in .*(no sudo; reversible' || fail "QA-3: the summaries fix line:
$out"
printf '%s\n' "$out" | grep -q '^\[WARN\] *ram-sessions *24 GB RAM over 15 sessions (the larger of a 12-session fleet and the 15 Claude Code process(es) running now, every repository.s) is 1638 MB a session (advice under 2048' || fail "QA-3: ram-sessions prints RAM, sessions, per session and the threshold:
$out"
printf '%s\n' "$out" | grep -q '^\[WARN\] *user-mcp *2 user-scope MCP server(s) in .*\.claude\.json run a command (and 1 remote): each starts a process in EVERY Claude Code session' || fail "QA-3: user-mcp counts the two that run a command, typed or not:
$out"
printf '%s\n' "$out" | grep -q 'fix: claude mcp list names them; re-add one most sessions never use with claude mcp add --scope project .*(no sudo; reversible' || fail "QA-3: the user-mcp fix line:
$out"
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
# ...and with no python3 to run gate-hook.py's decode, a settings file that
# exists is not read some other way: both settings rows CANNOT-ASSESS
# (QA-3, the same file with python3, is the control: WARN on both).
for r in sandbox summaries code-mode user-mcp; do
	verdicts "$out" | grep -qx "CANNOT-ASSESS $r" || fail "QA-4: no python3 must leave $r CANNOT-ASSESS:
$out"
done
printf '%s\n' "$out" | grep -q 'no python3 to decode it' || fail "QA-4: the CANNOT-ASSESS names the missing python3"
rm -f "$HOMEFX/bin/buddy"

# QA-5: not macOS, the tools missing: CANNOT-ASSESS where a tool is missing,
# never PASS; a trim in crontab is found; exit 0.
BIN_C="$WORK/bin-c"; mkbin "$BIN_C" uname go python3 buddy df ps du crontab
SC_C="$WORK/sc-c"
scenario "$SC_C" os=Linux go_version=1.27.0 gocache="$HOMEFX/.cache/go-build" df_avail=104857600 df_used=40 \
	fseventsd= du_kb=1024 crontab='0 */3 * * * go clean -cache'
git -C "$CO" worktree remove --force "$TMPFX/wt-under-tmp"
printf '%s\n' "$GOODSET" > "$SET"; rm -rf "$HOMEFX/.agents"; printf '%s\n' "$GOODCJ" > "$CJ"
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
CANNOT-ASSESS ram-sessions
PASS go-cache-trim
CANNOT-ASSESS time-machine
CANNOT-ASSESS ac-sleep
PASS worktrees-tmp
PASS scratch
PASS sandbox
PASS summaries
PASS code-mode
PASS user-mcp
PASS codex-skill-copy'
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
printf '{"env": \n' > "$SET"
printf '{"mcpServers": \n' > "$CJ"
mkdir -p "$HOMEFX/.agents/skills/buddy/SKILL.md"
run "$BIN_A" "$SC_D" --checkout "$WORK/missing"
[ "$rc" -eq 0 ] || fail "QA-7: CANNOT-ASSESS must not fail the run (rc=$rc):
$out"
for v in 'CANNOT-ASSESS checkout-spotlight' 'CANNOT-ASSESS worktrees-tmp' 'CANNOT-ASSESS scratch' 'WARN go-cache-trim' \
	'CANNOT-ASSESS sandbox' 'CANNOT-ASSESS summaries' 'CANNOT-ASSESS code-mode' 'CANNOT-ASSESS user-mcp' 'CANNOT-ASSESS codex-skill-copy'; do
	verdicts "$out" | grep -qx "$v" || fail "QA-7: want '$v' when the read fails:
$out"
done
printf '%s\n' "$out" | grep -q 'is not a directory' || fail "QA-7: the missing checkout is named as such"
printf '%s\n' "$out" | grep -q 'du failed or no permission' || fail "QA-7: the failed du is named"
[ -z "$(shape "$out")" ] || fail "QA-7: shape:$(shape "$out")
$out"
printf '%s\n' "$out" | grep -q 'is not JSON this check can read (JSONDecodeError)' || fail "QA-7: the malformed settings file is named as unreadable:
$out"
printf '%s\n' "$out" | grep -q "could not establish absence or equality for: $HOMEFX/.agents/skills/buddy/SKILL.md" || fail "QA-7: a SKILL.md that is a directory is named:
$out"
rm -f "$HOMEFX/Library/LaunchAgents/com.example.env.plist"; rm -rf "$HOMEFX/.agents"; printf '%s\n' "$GOODSET" > "$SET"; printf '%s\n' "$GOODCJ" > "$CJ"
# A go stub that sees the holding env is the control that the script sets it:
# QA-1's toolchain PASS came through this stub, which refuses without it.
grep -q '^go version' "$LOG" || fail "QA-7: go version was not reached"

# QA-6: --list runs nothing and names every check; --help exits 0; an
# unknown flag and a positional are refused, exit 2, `refused:` last.
run "$BIN_A" "$SC_A" --checkout "$CO" --list
[ "$rc" -eq 0 ] || fail "QA-6: --list rc=$rc"
[ ! -s "$LOG" ] || fail "QA-6: --list ran a command: $(cat "$LOG")"
[ "$(printf '%s\n' "$out" | wc -l | tr -d ' ')" -eq 19 ] || fail "QA-6: --list must name the 19 checks:
$out"
for c in platform toolchain path-bin disk-free go-cache-spotlight checkout-spotlight fseventsd ram-sessions go-cache-trim time-machine ac-sleep worktrees-tmp scratch sandbox summaries code-mode user-mcp codex-skill-copy codex-gate; do
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

# QA-8 (Codex report B1): the Codex row. No ~/.codex is no row (QA-1's 15
# PASS are the control: HOMEFX has none). With one: no hooks.json is WARN NOT
# WIRED; a gate diagnose lists clean is PASS (the positive control, and the
# one that proves diagnose was asked: its argv is in the log); a gate drawing
# findings is WARN; an unreadable file, a diagnose refusal, and a config.toml
# hooks table beside no hooks.json are CANNOT-ASSESS; config.toml's
# `[hooks.state]` (trust state) alone is not a hooks table. Each run is held to
# the same no-write rule, Codex's directory included.
CX="$HOMEFX/.codex"; mkdir -p "$CX"
SC_X="$WORK/sc-x"; cp -R "$SC_A" "$SC_X"
codex_row() { printf '%s\n' "$1" | grep 'codex-gate'; }
printf '[hooks.state]\n[hooks.state."%s/hooks.json:pre_tool_use:0:0"]\ntrusted_hash = "x"\n' "$CX" > "$CX/config.toml"
run "$BIN_A" "$SC_X" --checkout "$CO"
codex_row "$out" | grep -q '^\[WARN\] *codex-gate *NOT WIRED: no .*/hooks.json: a Codex session here patches peer-held files unrefused' ||
	fail "QA-8: no hooks.json must be WARN NOT WIRED (hooks.state alone is no hooks table):
$out"
[ -z "$(shape "$out")" ] || fail "QA-8: shape:$(shape "$out")"
grep -q '^buddy diagnose' "$LOG" && fail "QA-8: diagnose was run with no hooks.json to read"
printf '[[hooks.PreToolUse]]\nmatcher = ".*"\n' >> "$CX/config.toml"
run "$BIN_A" "$SC_X" --checkout "$CO"
codex_row "$out" | grep -q '^\[CANNOT-ASSESS\] *codex-gate *no .*hooks.json; .*config.toml declares hooks too' ||
	fail "QA-8: a config.toml hooks table beside no hooks.json must be CANNOT-ASSESS:
$out"
printf '[["hooks".PreToolUse]]\nmatcher = ".*"\n' > "$CX/config.toml"
run "$BIN_A" "$SC_X" --checkout "$CO"
codex_row "$out" | grep -q '^\[CANNOT-ASSESS\] *codex-gate *no .*hooks.json; .*config.toml declares hooks too' ||
	fail "QA-8: a quoted hooks table key must still be CANNOT-ASSESS (Codex code pass):
$out"
printf '[hooks.state]\n' > "$CX/config.toml"
printf '{"hooks":{}}\n' > "$CX/hooks.json"
printf '%s\n' '{"schema":"x","codex_hooks":{"verdict":"marked","commands":[{"event":"PreToolUse","verb":"gate","marker":"codex"}]}}' > "$SC_X/buddy_out"
run "$BIN_A" "$SC_X" --checkout "$CO"
codex_row "$out" | grep -q '^\[PASS\] *codex-gate *gate wired fail-closed in .*/hooks.json' || fail "QA-8: a clean gate must PASS:
$out"
grep -q "^buddy diagnose --codex-hooks $CX/hooks.json\$" "$LOG" || fail "QA-8: the PASS did not come from buddy diagnose --codex-hooks: $(cat "$LOG")"
# diagnose runs from /, so it judges no repository and reads no ledger.
[ "$(cat "$SC_X/buddy_cwd")" = / ] || fail "QA-8: diagnose ran from $(cat "$SC_X/buddy_cwd"), not /"
[ "$rc" -eq 0 ] && [ -z "$(shape "$out")" ] || fail "QA-8: a PASS row (rc=$rc) shape:$(shape "$out")"
printf '%s\n' '{"codex_hooks":{"verdict":"marked","commands":[{"event":"PreToolUse","verb":"gate","marker":"codex","finding":"the gate is not wrapped fail-closed"}]}}' > "$SC_X/buddy_out"
run "$BIN_A" "$SC_X" --checkout "$CO"
codex_row "$out" | grep -q '^\[WARN\] *codex-gate *NOT WIRED: the gate in .* draws findings' || fail "QA-8: a gate with findings must be WARN:
$out"
printf '%s\n' '{"codex_hooks":{"verdict":"no_buddy_command_recognized","commands":[{"event":"PreToolUse","finding":"no PreToolUse buddy gate recognized in this layer"}]}}' > "$SC_X/buddy_out"
run "$BIN_A" "$SC_X" --checkout "$CO"
codex_row "$out" | grep -q '^\[WARN\] *codex-gate *NOT WIRED: .*runs no buddy gate on PreToolUse' || fail "QA-8: a layer with no gate must be WARN:
$out"
printf 'refused\n' > "$SC_X/buddy_out"; printf '1\n' > "$SC_X/buddy_rc"
run "$BIN_A" "$SC_X" --checkout "$CO"
codex_row "$out" | grep -q '^\[CANNOT-ASSESS\] *codex-gate *buddy diagnose --codex-hooks .* refused it' || fail "QA-8: a diagnose refusal must be CANNOT-ASSESS:
$out"
rm -f "$SC_X/buddy_rc"
chmod 000 "$CX/hooks.json"
if [ -r "$CX/hooks.json" ]; then
	echo "check-env-check: QA-8's unreadable case NOT RUN (running as a user who can read a mode-000 file)" >&2
else
	run "$BIN_A" "$SC_X" --checkout "$CO"
	codex_row "$out" | grep -q '^\[CANNOT-ASSESS\] *codex-gate *cannot read .*/hooks.json' || fail "QA-8: an unreadable hooks.json must be CANNOT-ASSESS:
$out"
fi
chmod 600 "$CX/hooks.json"
[ "$(printf '%s\n' "$out" | grep -c '^\[[A-Z-]*\] *codex-gate ')" -eq 1 ] || fail "QA-8: one Codex row, not $(printf '%s\n' "$out" | grep -c codex-gate):
$out"
rm -rf "$CX"

# QA-9 (D-188, D-187, D-190, D-186): the settings and skill-copy rows, arm by arm.
# row <name> — that row's line of the last run, with its fix line.
row() { printf '%s\n' "$out" | awk -v n="$1" '$2 == n { p = 1; print; next } p && /^ *fix: / { print } { p = 0 }'; }
# set9 <json> — the fixture's user settings for the next run.
set9() { printf '%s\n' "$1" > "$SET"; }

# sandbox. Enabled with nothing else: WARN naming the worktree commands AND the
# unattended wait check. autoAllowBashIfSandboxed true, or an allow rule that
# covers `buddy wait check`, drops only the wait-check clause; a rule that
# does not cover it keeps it. {} and no file are PASS. The WARN is the
# positive control that the file was read at all.
set9 '{"sandbox": {"enabled": true}}'
run "$BIN_A" "$SC_A" --checkout "$CO"
r=$(row sandbox)
printf '%s\n' "$r" | grep -q '^\[WARN\] *sandbox *sandbox.enabled is true in .*git worktree add/remove, the integration worktree.s cherry-pick -n and commit, and the restack call with the sandbox off' || fail "QA-9: sandbox enabled must WARN naming the worktree commands:
$r"
printf '%s\n' "$r" | grep -q 'an unattended buddy wait check asks for approval: neither sandbox.autoAllowBashIfSandboxed true nor an allow rule' || fail "QA-9: sandbox alone must name the unattended wait check:
$r"
printf '%s\n' "$r" | grep -q 'fix: .*dangerouslyDisableSandbox.*"autoAllowBashIfSandboxed": true.*(no sudo; reversible' || fail "QA-9: the sandbox fix line:
$r"
[ "$rc" -eq 0 ] && [ -z "$(shape "$out")" ] || fail "QA-9: a sandbox WARN (rc=$rc) shape:$(shape "$out")"
for s in '{"sandbox": {"enabled": true, "autoAllowBashIfSandboxed": true}}' \
	'{"sandbox": {"enabled": true}, "permissions": {"allow": ["Bash(buddy wait:*)"]}}' \
	'{"sandbox": {"enabled": true}, "permissions": {"allow": ["Bash(buddy wait check)"]}}' \
	'{"sandbox": {"enabled": true}, "permissions": {"allow": ["Bash"]}}'; do
	set9 "$s"; run "$BIN_A" "$SC_A" --checkout "$CO"; r=$(row sandbox)
	printf '%s\n' "$r" | grep -q '^\[WARN\] *sandbox .*restack call with the sandbox off' || fail "QA-9: $s must still WARN on the worktree commands:
$r"
	printf '%s\n' "$r" | grep -q 'unattended' && fail "QA-9: $s covers the wait check; the line must not say it asks:
$r"
	[ -z "$(shape "$out")" ] || fail "QA-9: $s shape:$(shape "$out")"
done
for s in '{"sandbox": {"enabled": true}, "permissions": {"allow": ["Bash(git:*)", "Bash(buddy waitress:*)", "Bash(buddy wait check --block)"]}}' \
	'{"sandbox": {"enabled": true, "autoAllowBashIfSandboxed": "true"}}'; do
	set9 "$s"; run "$BIN_A" "$SC_A" --checkout "$CO"
	row sandbox | grep -q 'an unattended buddy wait check asks for approval' || fail "QA-9: $s does not cover the wait check; the line must say it asks:
$(row sandbox)"
done
for s in '{}' '{"sandbox": {"enabled": false}}' '{"sandbox": true}'; do
	set9 "$s"; run "$BIN_A" "$SC_A" --checkout "$CO"
	row sandbox | grep -q '^\[PASS\] *sandbox *sandbox.enabled is not true' || fail "QA-9: $s must PASS sandbox:
$out"
done
# One decode, gate-hook.py's: NaN and a 65-deep nest are files Claude Code
# does not load, and a plain json.loads would read the first (D-183) and the
# interpreter would decide the second (D-182); both are CANNOT-ASSESS in the
# shared decode's words, never PASS. 64 deep reads (the cap's own control).
set9 '{"sandbox": {"enabled": true}, "x": NaN}'
run "$BIN_A" "$SC_A" --checkout "$CO"
row sandbox | grep -q '^\[CANNOT-ASSESS\] *sandbox .*holds a non-JSON constant NaN' || fail "QA-9: a NaN settings file must be CANNOT-ASSESS through gate-hook.py's decode:
$(row sandbox)"
deep=$(awk 'BEGIN { for (i = 0; i < 64; i++) printf "{\"a\":"; printf "1"; for (i = 0; i < 64; i++) printf "}" }')
set9 "{\"sandbox\": {\"enabled\": true}, \"x\": $deep}"
run "$BIN_A" "$SC_A" --checkout "$CO"
row sandbox | grep -q '^\[CANNOT-ASSESS\] *sandbox .*is nested deeper than 64 levels' || fail "QA-9: a 65-deep settings file must be CANNOT-ASSESS:
$(row sandbox)"
deep=$(awk 'BEGIN { for (i = 0; i < 63; i++) printf "{\"a\":"; printf "1"; for (i = 0; i < 63; i++) printf "}" }')
set9 "{\"sandbox\": {\"enabled\": true}, \"x\": $deep}"
run "$BIN_A" "$SC_A" --checkout "$CO"
row sandbox | grep -q '^\[WARN\] *sandbox ' || fail "QA-9: a 64-deep settings file reads (the cap's control):
$(row sandbox)"
set9 '[1]'
run "$BIN_A" "$SC_A" --checkout "$CO"
row sandbox | grep -q '^\[CANNOT-ASSESS\] *sandbox .*is not a JSON object' || fail "QA-9: a settings file that is not an object must be CANNOT-ASSESS:
$(row sandbox)"
rm -f "$SET"
run "$BIN_A" "$SC_A" --checkout "$CO"
row sandbox | grep -q '^\[PASS\] *sandbox *no .*settings.json: the Bash sandbox is not enabled' || fail "QA-9: no settings file must PASS sandbox:
$(row sandbox)"

# summaries (D-187's fixture cases). No settings file and nothing in the
# environment: WARN with the estimate marked as one, and the fix line.
row summaries | grep -q '^\[WARN\] *summaries *subagent progress summaries on: about 20% on subagent spend, 4% of a fleet day.s (estimated, D-187); not set in this environment, and no ' || fail "QA-9: no file and no env must WARN with the estimate:
$(row summaries)"
row summaries | grep -q "fix: \"env\": {\"CLAUDE_CODE_DISABLE_AGENT_PROGRESS_SUMMARIES\": \"1\"} in $SET, if you want it (no sudo; reversible: remove it; loses the background-task panel.s progress line" || fail "QA-9: the summaries fix line:
$(row summaries)"
FX_SUMMARIES=1; run "$BIN_A" "$SC_A" --checkout "$CO"; unset FX_SUMMARIES
row summaries | grep -q '^\[PASS\] *summaries *CLAUDE_CODE_DISABLE_AGENT_PROGRESS_SUMMARIES=1 in this environment' || fail "QA-9: the process env alone must PASS:
$(row summaries)"
set9 '{"env": {"CLAUDE_CODE_DISABLE_AGENT_PROGRESS_SUMMARIES": "0"}}'
run "$BIN_A" "$SC_A" --checkout "$CO"
row summaries | grep -q '^\[WARN\] *summaries .*not set in this environment or in env of ' || fail "QA-9: a 0 in settings must WARN:
$(row summaries)"
set9 '{"env": {"CLAUDE_CODE_DISABLE_AGENT_PROGRESS_SUMMARIES": "true"}}'
run "$BIN_A" "$SC_A" --checkout "$CO"
row summaries | grep -q '^\[PASS\] *summaries .*is 1 or true in env of ' || fail "QA-9: true in settings env must PASS:
$(row summaries)"
FX_SUMMARIES=TRUE; run "$BIN_A" "$SC_A" --checkout "$CO"; unset FX_SUMMARIES
row summaries | grep -q '^\[PASS\] *summaries *CLAUDE_CODE_DISABLE_AGENT_PROGRESS_SUMMARIES=TRUE in this environment' || fail "QA-9: TRUE in the env must PASS:
$(row summaries)"
set9 '{"env": '
FX_SUMMARIES=1; run "$BIN_A" "$SC_A" --checkout "$CO"; unset FX_SUMMARIES
row summaries | grep -q '^\[PASS\] *summaries .*in this environment' || fail "QA-9: the process env beside a malformed file must PASS:
$(row summaries)"
FX_SUMMARIES=0; run "$BIN_A" "$SC_A" --checkout "$CO"; unset FX_SUMMARIES
row summaries | grep -q '^\[CANNOT-ASSESS\] *summaries *not set in this environment, and .*is not JSON this check can read' || fail "QA-9: a malformed file and no process value must be CANNOT-ASSESS:
$(row summaries)"
rm -f "$SET"
chmod 000 "$HOMEFX/.claude"
if [ -x "$HOMEFX/.claude" ]; then
	echo "check-env-check: QA-9's unsearchable-settings case NOT RUN (running as a user who can search a mode-000 directory)" >&2
else
	run "$BIN_A" "$SC_A" --checkout "$CO"
	row summaries | grep -q '^\[CANNOT-ASSESS\] *summaries .*its directory cannot be searched' || fail "QA-9: settings under an unsearchable directory are not proven absent:
$(row summaries)"
fi
chmod 755 "$HOMEFX/.claude"
printf '%s\n' "$GOODSET" > "$SET"

# code-mode (D-190, D-195). Off is PASS; on, in the process environment or in
# env of the user settings, is WARN naming the measured gate and the measured
# mail loss buddy now skips (not re-measured live), with the fix line. The WARN cases are the positive control that
# the variable is read at all (QA-1's GOODSET, without it, is PASS); the
# environment alone is enough for WARN beside a malformed file, which alone is
# CANNOT-ASSESS. A value Code Mode would not take (0) is PASS.
rm -f "$SET"
run "$BIN_A" "$SC_A" --checkout "$CO"
row code-mode | grep -q '^\[PASS\] *code-mode *Code Mode is not on: not set in this environment, and no ' || fail "QA-9: no file and no env must PASS code-mode:
$(row code-mode)"
FX_CODEMODE=1; run "$BIN_A" "$SC_A" --checkout "$CO"; unset FX_CODEMODE
r=$(row code-mode)
printf '%s\n' "$r" | grep -q '^\[WARN\] *code-mode *Code Mode is on (CLAUDE_CODE_EXPERIMENTAL_CODE_MODE=1 in this environment): the gate was measured to refuse inner Edit and Bash calls there (Claude Code 2.1.325, D-190); mail queued after a Stop was measured marked delivered unread on late code output (2 of 2), and buddy now skips those drains (D-195), not re-measured live on an installed build' || fail "QA-9: Code Mode on in the env must WARN:
$r"
printf '%s\n' "$r" | grep -qF "fix: nothing to change for the gate; to rule out the mail loss until that skip is re-measured, start lanes without CLAUDE_CODE_EXPERIMENTAL_CODE_MODE and remove it from env of $SET (no sudo; reversible: set it again; a session started in Code Mode resumes only with it on)" || fail "QA-9: the code-mode fix line:
$r"
[ "$rc" -eq 0 ] && [ -z "$(shape "$out")" ] || fail "QA-9: a code-mode WARN (rc=$rc) shape:$(shape "$out")"
FX_CODEMODE=On; run "$BIN_A" "$SC_A" --checkout "$CO"; unset FX_CODEMODE
row code-mode | grep -q '^\[WARN\] *code-mode .*=On in this environment' || fail "QA-9: On in the env must WARN:
$(row code-mode)"
FX_CODEMODE=0; run "$BIN_A" "$SC_A" --checkout "$CO"; unset FX_CODEMODE
row code-mode | grep -q '^\[PASS\] *code-mode ' || fail "QA-9: 0 in the env must PASS:
$(row code-mode)"
set9 '{"env": {"CLAUDE_CODE_EXPERIMENTAL_CODE_MODE": "true"}}'
run "$BIN_A" "$SC_A" --checkout "$CO"
row code-mode | grep -q '^\[WARN\] *code-mode *Code Mode is on (CLAUDE_CODE_EXPERIMENTAL_CODE_MODE in env of .*): the gate was measured' || fail "QA-9: Code Mode on in settings env must WARN:
$(row code-mode)"
set9 '{"env": {"CLAUDE_CODE_EXPERIMENTAL_CODE_MODE": "0"}}'
run "$BIN_A" "$SC_A" --checkout "$CO"
row code-mode | grep -q '^\[PASS\] *code-mode *Code Mode is not on: not set in this environment or in env of ' || fail "QA-9: 0 in settings env must PASS:
$(row code-mode)"
set9 '{"env": '
run "$BIN_A" "$SC_A" --checkout "$CO"
row code-mode | grep -q '^\[CANNOT-ASSESS\] *code-mode *not on in this environment, and .*is not JSON this check can read' || fail "QA-9: a malformed file and no process value must be CANNOT-ASSESS:
$(row code-mode)"
FX_CODEMODE=yes; run "$BIN_A" "$SC_A" --checkout "$CO"; unset FX_CODEMODE
row code-mode | grep -q '^\[WARN\] *code-mode .*=yes in this environment' || fail "QA-9: the env beside a malformed file must WARN:
$(row code-mode)"
# One value, two sources, one verdict: a padded " true " is judged the same
# in settings as in the environment (Codex code pass). Each run's other
# source is empty, and the settings WARN above is the control that the file
# is read.
set9 '{"env": {"CLAUDE_CODE_EXPERIMENTAL_CODE_MODE": " true "}}'
run "$BIN_A" "$SC_A" --checkout "$CO"
v1=$(verdicts "$out" | grep ' code-mode$')
rm -f "$SET"
FX_CODEMODE=' true '; run "$BIN_A" "$SC_A" --checkout "$CO"; unset FX_CODEMODE
v2=$(verdicts "$out" | grep ' code-mode$')
[ -n "$v1" ] && [ "$v1" = "$v2" ] || fail "QA-9: \" true \" must get one verdict from settings and env, got '$v1' and '$v2'"
printf '%s\n' "$GOODSET" > "$SET"

# codex-skill-copy. A verbatim copy is PASS; the rewrite /import makes is
# WARN naming the path and the refresh with both knobs, never rm; a copy in
# CODEX_HOME's skills is found there and its own command printed; unreadable
# or under an unsearchable directory is CANNOT-ASSESS; HOME empty too. QA-1
# (none at all) is PASS.
AG="$HOMEFX/.agents/skills"
mkdir -p "$AG/buddy" "$AG/buddy-fleet"
cat "$ROOT/skills/buddy/SKILL.md" > "$AG/buddy/SKILL.md"; cat "$ROOT/skills/buddy-fleet/SKILL.md" > "$AG/buddy-fleet/SKILL.md"
run "$BIN_A" "$SC_A" --checkout "$CO"
row codex-skill-copy | grep -q "^\[PASS\] *codex-skill-copy *each shipped SKILL.md under $AG and $HOMEFX/.codex/skills is absent or byte-identical" || fail "QA-9: verbatim copies must PASS:
$(row codex-skill-copy)"
sed 's/Claude Code/Codex/g; s/CLAUDE\.md/AGENTS.md/g' "$ROOT/skills/buddy/SKILL.md" > "$AG/buddy/SKILL.md"
run "$BIN_A" "$SC_A" --checkout "$CO"
r=$(row codex-skill-copy)
printf '%s\n' "$r" | grep -q "^\[WARN\] *codex-skill-copy *differs from this checkout's skills: $AG/buddy/SKILL.md; a Codex session may load it, and a copy Codex's /import took is never refreshed (D-186)" || fail "QA-9: the rewritten copy must WARN naming it:
$r"
printf '%s\n' "$r" | grep -q "fix: BUDDY_SKILL_DIR=\"$AG/buddy\" BUDDY_FLEET_SKILL_DIR=\"$AG/buddy-fleet\" sh scripts/install-skill.sh, from the checkout (no sudo; reversible" || fail "QA-9: the refresh names both knobs:
$r"
printf '%s\n' "$r" | grep -Eq '(fix: |[;&|] *)rm ' && fail "QA-9: the fix must never be an rm:
$r"
[ -z "$(shape "$out")" ] || fail "QA-9: skill-copy WARN shape:$(shape "$out")"
cat "$ROOT/skills/buddy/SKILL.md" > "$AG/buddy/SKILL.md"
CXH="$WORK/codexhome"; mkdir -p "$CXH/skills/buddy-fleet"
printf 'stale\n' > "$CXH/skills/buddy-fleet/SKILL.md"
FX_CODEX_HOME=$CXH; run "$BIN_A" "$SC_A" --checkout "$CO"; unset FX_CODEX_HOME
r=$(row codex-skill-copy)
printf '%s\n' "$r" | grep -q "differs from this checkout's skills: $CXH/skills/buddy-fleet/SKILL.md;" || fail "QA-9: a stale copy under CODEX_HOME must WARN:
$r"
printf '%s\n' "$r" | grep -q "fix: BUDDY_SKILL_DIR=\"$CXH/skills/buddy\" BUDDY_FLEET_SKILL_DIR=\"$CXH/skills/buddy-fleet\" sh scripts/install-skill.sh, from" || fail "QA-9: the CODEX_HOME refresh:
$r"
rm -rf "$CXH"
# A glob character in CODEX_HOME is a name, not a pattern: codex[1] beside an
# empty codex1 is judged as named (Codex code pass: an unquoted loop judged
# codex1 and said PASS).
mkdir -p "$WORK/codex[1]/skills/buddy" "$WORK/codex1/skills"; printf 'stale\n' > "$WORK/codex[1]/skills/buddy/SKILL.md"
FX_CODEX_HOME="$WORK/codex[1]"; run "$BIN_A" "$SC_A" --checkout "$CO"; unset FX_CODEX_HOME
row codex-skill-copy | grep -qF "differs from this checkout's skills: $WORK/codex[1]/skills/buddy/SKILL.md;" || fail "QA-9: CODEX_HOME with a glob character must be judged as named:
$(row codex-skill-copy)"
rm -rf "$WORK/codex[1]" "$WORK/codex1"
chmod 000 "$AG/buddy/SKILL.md"
if [ -r "$AG/buddy/SKILL.md" ]; then
	echo "check-env-check: QA-9's unreadable-copy case NOT RUN (running as a user who can read a mode-000 file)" >&2
else
	run "$BIN_A" "$SC_A" --checkout "$CO"
	row codex-skill-copy | grep -q "^\[CANNOT-ASSESS\] *codex-skill-copy *could not establish absence or equality for: $AG/buddy/SKILL.md" || fail "QA-9: an unreadable copy must be CANNOT-ASSESS:
$(row codex-skill-copy)"
fi
chmod 644 "$AG/buddy/SKILL.md"
rm -rf "$AG/buddy-fleet"; chmod 000 "$AG"
if [ -x "$AG" ]; then
	echo "check-env-check: QA-9's unsearchable-directory case NOT RUN (running as a user who can search a mode-000 directory)" >&2
else
	run "$BIN_A" "$SC_A" --checkout "$CO"
	r=$(row codex-skill-copy)
	printf '%s\n' "$r" | grep -q '^\[CANNOT-ASSESS\] *codex-skill-copy *could not establish absence or equality for: ' &&
		printf '%s\n' "$r" | grep -q " $AG/buddy/SKILL.md" && printf '%s\n' "$r" | grep -q " $AG/buddy-fleet/SKILL.md" ||
		fail "QA-9: under an unsearchable directory, absence is not proven:
$r"
fi
chmod 755 "$AG"; rm -rf "$HOMEFX/.agents"
FX_HOME=; run "$BIN_A" "$SC_A" --checkout "$CO"; unset FX_HOME
for r in sandbox summaries code-mode user-mcp codex-skill-copy; do
	verdicts "$out" | grep -qx "CANNOT-ASSESS $r" || fail "QA-9: an empty HOME must leave $r CANNOT-ASSESS:
$out"
done
row codex-skill-copy | grep -q 'HOME is unset' || fail "QA-9: the empty-HOME line says why:
$(row codex-skill-copy)"
# HOME not set at all: every row still prints (set -u aborted the script at
# the first $HOME before), the HOME-rooted ones CANNOT-ASSESS, scratch too.
FX_HOME_UNSET=1; run "$BIN_A" "$SC_A" --checkout "$CO"; unset FX_HOME_UNSET
for r in sandbox summaries code-mode user-mcp codex-skill-copy scratch; do
	verdicts "$out" | grep -qx "CANNOT-ASSESS $r" || fail "QA-9: an unset HOME must leave $r CANNOT-ASSESS:
$out"
done
printf '%s\n' "$out" | grep -q '^env-check: [0-9]* PASS' || fail "QA-9: an unset HOME must still reach the count line:
$out"
# A symlink that does not resolve (its target under a directory that cannot
# be searched) proves no absence: ~/.agents linked into a locked directory is
# CANNOT-ASSESS, not "absent" (Codex code pass). QA-1's PASS is the control.
mkdir -p "$WORK/locked/agents"; ln -s "$WORK/locked/agents" "$HOMEFX/.agents"; chmod 000 "$WORK/locked"
if [ -x "$WORK/locked" ]; then
	echo "check-env-check: QA-9's locked-symlink case NOT RUN (running as a user who can search a mode-000 directory)" >&2
else
	run "$BIN_A" "$SC_A" --checkout "$CO"
	row codex-skill-copy | grep -q '^\[CANNOT-ASSESS\] *codex-skill-copy *could not establish absence or equality for: ' ||
		fail "QA-9: a skills path through an unresolvable symlink must be CANNOT-ASSESS:
$(row codex-skill-copy)"
fi
chmod 755 "$WORK/locked"; rm -f "$HOMEFX/.agents"
# A newline in a path from the environment stays on its row: no fabricated
# `[PASS]` line, one codex-skill-copy row (Codex code pass).
FX_CODEX_HOME="$WORK/cx
[PASS]          forged"; run "$BIN_A" "$SC_A" --checkout "$CO"; unset FX_CODEX_HOME
printf '%s\n' "$out" | grep -q '^\[PASS\] *forged' && fail "QA-9: a newline in CODEX_HOME fabricated a row:
$out"
[ "$(printf '%s\n' "$out" | grep -c 'codex-skill-copy')" -eq 1 ] && row codex-skill-copy | grep -q "cx?\[PASS\] *forged/skills is absent" ||
	fail "QA-9: the CODEX_HOME path must print on its own row, its newline as ?:
$out"
# QA-10 (D-209, #144): the memory rows, arm by arm. ram-sessions divides RAM
# by the larger of a 12-session fleet and the processes ps names `claude` (a
# `claude-helper` the ps stub always prints is not one): 24 GiB over 12 is
# exactly 2048 MB a session, PASS at the boundary; a 13th running session
# tips it to WARN, so the live count is read (the positive control for the
# processes); 2 running on 16 GiB still divides by 12 (the floor's control);
# no RAM size is CANNOT-ASSESS, never PASS.
SC_M="$WORK/sc-m"; cp -R "$SC_A" "$SC_M"
scenario "$SC_M" memsize=25769803776 claude_n=0
run "$BIN_A" "$SC_M" --checkout "$CO"
row ram-sessions | grep -qx '\[PASS\] *ram-sessions *24 GB RAM over 12 sessions (the larger of a 12-session fleet and the 0 Claude Code process(es) running now, every repository.s) is 2048 MB a session' || fail "QA-10: 24 GiB over 12 sessions is 2048 MB, PASS at the boundary:
$(row ram-sessions)"
scenario "$SC_M" claude_n=13
run "$BIN_A" "$SC_M" --checkout "$CO"
row ram-sessions | grep -q '^\[WARN\] *ram-sessions *24 GB RAM over 13 sessions (.* the 13 Claude Code process(es) running now, .*) is 1890 MB a session (advice under 2048' || fail "QA-10: 13 running sessions must count over the 12 floor:
$(row ram-sessions)"
[ -z "$(shape "$out")" ] || fail "QA-10: ram-sessions WARN shape:$(shape "$out")"
scenario "$SC_M" memsize=17179869184 claude_n=2
run "$BIN_A" "$SC_M" --checkout "$CO"
row ram-sessions | grep -q '^\[WARN\] *ram-sessions *16 GB RAM over 12 sessions (.* the 2 Claude Code process(es) running now, .*) is 1365 MB a session' || fail "QA-10: 2 running sessions still divide by the 12-session fleet:
$(row ram-sessions)"
# A process table ps could not read is a count nobody took: never "0 running".
: > "$SC_M/ps_empty"
run "$BIN_A" "$SC_M" --checkout "$CO"
row ram-sessions | grep -q '^\[WARN\] *ram-sessions *16 GB RAM over 12 sessions (the larger of a 12-session fleet and the Claude Code processes running now, uncounted: ps gave nothing) is 1365 MB' || fail "QA-10: an unreadable process table must say uncounted:
$(row ram-sessions)"
scenario "$SC_M" memsize=25769803776
run "$BIN_A" "$SC_M" --checkout "$CO"
row ram-sessions | grep -q '^\[CANNOT-ASSESS\] *ram-sessions *24 GB RAM over 12 sessions (.*uncounted: ps gave nothing) is 2048 MB a session, a floor: .* so no PASS' || fail "QA-10: 2048 MB on an uncounted floor must be CANNOT-ASSESS, never PASS (Codex code pass):
$(row ram-sessions)"
[ -z "$(shape "$out")" ] || fail "QA-10: ram-sessions floor CANNOT-ASSESS shape:$(shape "$out")"
rm -f "$SC_M/ps_empty" "$SC_M/memsize"
run "$BIN_A" "$SC_M" --checkout "$CO"
row ram-sessions | grep -q '^\[CANNOT-ASSESS\] *ram-sessions *sysctl -n hw.memsize gave no RAM size .*; 2 Claude Code process(es) running now' || fail "QA-10: no RAM size must be CANNOT-ASSESS:
$(row ram-sessions)"
[ -z "$(shape "$out")" ] || fail "QA-10: ram-sessions CANNOT-ASSESS shape:$(shape "$out")"
grep -q '^sysctl -n hw.memsize$' "$LOG" || fail "QA-10: the RAM size did not come from sysctl -n hw.memsize: $(cat "$LOG")"

# user-mcp: WARN only on a user-scope server that runs a command (QA-3: two,
# one typed stdio, one untyped, beside a remote one); here one is enough, and
# no file, {}, an empty table, remote servers (a url wins over a command) and
# a local-scope server that runs one are PASS. A table that is not an object,
# a file that is not one, and NaN (read only by the shared decode's refusal)
# are CANNOT-ASSESS, never PASS.
cj() { printf '%s\n' "$1" > "$CJ"; }
cj '{"mcpServers": {"pw": {"command": "npx"}}}'
run "$BIN_A" "$SC_A" --checkout "$CO"
row user-mcp | grep -q '^\[WARN\] *user-mcp *1 user-scope MCP server(s) in .* run a command (and 0 remote)' || fail "QA-10: one server that runs a command must WARN:
$(row user-mcp)"
[ "$rc" -eq 0 ] && [ -z "$(shape "$out")" ] || fail "QA-10: a user-mcp WARN (rc=$rc) shape:$(shape "$out")"
for c in '{}' '{"mcpServers": {}}' '{"mcpServers": {"r": {"type": "sse", "url": "http://127.0.0.1:1/sse"}, "s": {"command": "x", "url": "u"}}}' "$GOODCJ"; do
	cj "$c"; run "$BIN_A" "$SC_A" --checkout "$CO"
	row user-mcp | grep -q '^\[PASS\] *user-mcp *no user-scope MCP server in .* runs a command' || fail "QA-10: $c must PASS user-mcp:
$(row user-mcp)"
done
for c in '{"mcpServers": []}' '[1]' '{"mcpServers": {"pw": {"command": "npx"}}, "x": NaN}'; do
	cj "$c"; run "$BIN_A" "$SC_A" --checkout "$CO"
	row user-mcp | grep -q '^\[CANNOT-ASSESS\] *user-mcp *.*\.claude\.json: .*; how many user-scope MCP servers start in every session is unknown' || fail "QA-10: $c must be CANNOT-ASSESS:
$(row user-mcp)"
	[ -z "$(shape "$out")" ] || fail "QA-10: $c shape:$(shape "$out")"
done
# Its own reason, not a crash's: a table that is not an object is named so
# (a reader that crashed on it says "could not run", the same verdict).
cj '{"mcpServers": []}'; run "$BIN_A" "$SC_A" --checkout "$CO"
row user-mcp | grep -q 'json: its mcpServers is not a JSON object; how many' || fail "QA-10: a list for mcpServers must be named as such:
$(row user-mcp)"
rm -f "$CJ"
run "$BIN_A" "$SC_A" --checkout "$CO"
row user-mcp | grep -q '^\[PASS\] *user-mcp *no .*\.claude\.json: no user-scope MCP server' || fail "QA-10: no ~/.claude.json must PASS:
$(row user-mcp)"
cj "$GOODCJ"

# Python is passed with -c, never a heredoc: macOS /bin/sh writes a heredoc to
# a temporary file (Codex code pass). Control: the same pattern finds one.
heredoc() { grep -n '<<' "$1" | grep -v '^[0-9]*:[[:space:]]*#' || true; }
printf 'x=$(python3 - <<%sPY%s\n' "'" "'" > "$WORK/heredoc-ctl"
[ -n "$(heredoc "$WORK/heredoc-ctl")" ] || fail "QA-9: the heredoc detector missed its planted control"
[ -z "$(heredoc "$ROOT/scripts/env-check.sh")" ] || fail "QA-9: env-check.sh holds a heredoc (a temporary file under macOS sh): $(heredoc "$ROOT/scripts/env-check.sh")"

echo "check-env-check: GREEN (QA-1 good machine 18 PASS + stubs reached, QA-1b a different buddy on PATH WARN naming both + a link to the installed one PASS, QA-2 indexed subfolder CANNOT-ASSESS + .noindex PASS + CPU WARN, QA-3 bad machine 2 FAIL 15 WARN exit 1 + fix shapes, QA-4 PATH advice + python3 WARN + settings rows CANNOT-ASSESS without it, QA-5 non-macOS CANNOT-ASSESS never PASS, QA-7 failed reads CANNOT-ASSESS + comment/env-only trim not scheduled + go held to the installed toolchain, QA-6 --list runs nothing + --help + refusals, QA-8 the Codex row: none without ~/.codex, NOT WIRED, wired via diagnose, findings, refusal, unreadable, config.toml hooks, QA-9 sandbox WARN with and without the wait-check clause + summaries and code-mode PASS/WARN/CANNOT-ASSESS by env and settings + codex-skill-copy PASS/WARN/CANNOT-ASSESS, through gate-hook.py's decode (NaN, depth) + glob in CODEX_HOME + unset HOME + unresolvable symlink + newline in a path + no heredoc, QA-10 ram-sessions at its 2048 MB boundary over max(12, running) + CANNOT-ASSESS without a RAM size, user-mcp WARN only on a user-scope command, PASS on remote and local scope, CANNOT-ASSESS unread; every run: no write (names + contents), no mutating call; planted mutation and overwrite controls)"
