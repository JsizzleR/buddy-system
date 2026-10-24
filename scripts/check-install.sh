#!/bin/sh
# check-install.sh — done-check for scripts/install.sh, the machine install
# and upgrade.
#
# HERMETIC: go, git, sh. Every destination is redirected into a throwaway
# directory (BUDDY_BIN_DIR, BUDDY_SKILL_DIR, BUDDY_SETTINGS), the daemon and
# this checkout's setup-clone are switched off, and the checkout's own bin/
# copies are left alone (BUDDY_REPO_BIN=off). Nothing on the real machine is
# touched, so the tier CI runs can run it.
#
# Each refusal has its positive control beside it: a fresh install builds and
# runs both binaries and installs the skill; a re-run is idempotent; an
# unbuildable destination fails LOUD (non-zero, and says so), never green; a
# claims-only install (BUDDY_CHAT=off) builds buddy alone and reports the chat
# remnants it left alone, with QA-1's default run as the control that both
# buddylist hook lines are still counted when the knob is unset.
set -eu
. "$(dirname -- "$0")/lib.sh"
ROOT=$(repo_root "$0")
CDPATH= cd -- "$ROOT"

fail() { echo "check-install: FAIL — $*" >&2; exit 1; }

WORK=$(mktemp -d) || fail "mktemp failed"
trap 'rm -rf "$WORK"' EXIT INT TERM

inst() {
	BUDDY_BIN_DIR="$WORK/bin" BUDDY_SKILL_DIR="$WORK/skill" BUDDY_SETTINGS="$WORK/settings.json" \
		BUDDY_DAEMON=off BUDDY_SETUP_CLONE=off BUDDY_REPO_BIN=off sh scripts/install.sh "$@"
}

# A settings file with every hook but busy and the presence line, spelled the
# way README's wiring spells them.
cat > "$WORK/settings.json" <<'EOF'
{"hooks": {
 "SessionStart": [{"hooks": [{"command": "[ -x \"$HOME/bin/buddy\" ] && \"$HOME/bin/buddy\" hello 2>/dev/null; exit 0"}]}],
 "PreToolUse": [{"hooks": [{"command": "[ -x \"$HOME/bin/buddy\" ] && \"$HOME/bin/buddy\" gate; exit 0"}]}],
 "PostToolUse": [{"hooks": [{"command": "\"$HOME/bin/buddy\" beat 2>/dev/null; exit 0"},
                            {"command": "\"$HOME/bin/buddylist\" alert 2>/dev/null; exit 0"}]}],
 "Stop": [{"hooks": [{"command": "\"$HOME/bin/buddy\" idle 2>/dev/null; exit 0"}]}],
 "SessionEnd": [{"hooks": [{"command": "\"$HOME/bin/buddy\" bye 2>/dev/null; exit 0"}]}]
}}
EOF
# Kept as written: QA-3/QA-3b wire busy and presence into settings.json in
# place, and QA-7 needs this file as it is here.
cp "$WORK/settings.json" "$WORK/settings.orig"

# QA-1: a fresh install builds both binaries, runs them, installs the skill,
# and reports exactly the two hooks that are not wired.
out=$(inst 2>&1) || fail "QA-1: install failed:
$out"
[ -x "$WORK/bin/buddy" ] && [ -x "$WORK/bin/buddylist" ] || fail "QA-1: binaries missing"
"$WORK/bin/buddy" help 2>&1 | grep -q commit-gate || fail "QA-1: the installed buddy is not this checkout's"
cmp -s skills/buddy/SKILL.md "$WORK/skill/SKILL.md" || fail "QA-1: skill not installed"
echo "$out" | grep -qF 'hooks: NOT wired in' || fail "QA-1: the hook report is missing:
$out"
missing=$(echo "$out" | sed -n 's/.*NOT wired in [^:]*: *\(.*\) (README.*/\1/p')
[ "$missing" = "buddy busy buddylist presence" ] || fail "QA-1: hook report named '$missing', want 'buddy busy buddylist presence'"
echo "$out" | grep -qF 'handoff: BUDDY_HANDOFF_AT not set in' || fail "QA-1: an unset handoff size is reported:
$out"
echo "$out" | tail -1 | grep -qx 'install: done' || fail "QA-1: no done line"

# QA-2: re-run is idempotent — skill current, binaries rebuilt and running.
out=$(inst 2>&1) || fail "QA-2: re-run failed:
$out"
echo "$out" | grep -q 'is current$' || fail "QA-2: the skill should already be current:
$out"

# QA-3: with every hook wired, the report says so (the control for QA-1's list).
sed 's/"hooks": {/"hooks": {"UserPromptSubmit": [{"hooks": [{"command": "\\"$HOME\/bin\/buddy\\" busy"}, {"command": "\\"$HOME\/bin\/buddylist\\" presence --gone"}]}],/' \
	"$WORK/settings.json" > "$WORK/settings2.json"
mv "$WORK/settings2.json" "$WORK/settings.json"
out=$(inst 2>&1) || fail "QA-3: install failed:
$out"
echo "$out" | grep -qF 'hooks: all eight wired' || fail "QA-3: every hook is wired:
$out"

# QA-3b (D-064): a handoff size in the settings env block is named, and a
# settings file whose busy hook is missing says nobody will be told (its
# control is the wired file just above, which must not say so).
sed 's/^{"hooks": {/{"env": {"BUDDY_HANDOFF_AT": "400k"}, "hooks": {/' "$WORK/settings.json" > "$WORK/settings2.json"
mv "$WORK/settings2.json" "$WORK/settings.json"
out=$(inst 2>&1) || fail "QA-3b: install failed:
$out"
echo "$out" | grep -qF 'handoff: BUDDY_HANDOFF_AT=400k in' || fail "QA-3b: the declared size is named:
$out"
echo "$out" | grep -qF 'NOT wired, so no session' && fail "QA-3b: busy is wired here:
$out"
sed 's/{"command": "\\"$HOME\/bin\/buddy\\" busy"}, //' "$WORK/settings.json" > "$WORK/settings2.json"
grep -q 'buddy\\" busy' "$WORK/settings2.json" && fail "QA-3b: the fixture still wires busy"
# inst() names the settings file itself, so the fixture goes in its place.
cp "$WORK/settings.json" "$WORK/settings.keep"
mv "$WORK/settings2.json" "$WORK/settings.json"
out=$(inst 2>&1) || fail "QA-3b: install failed:
$out"
mv "$WORK/settings.keep" "$WORK/settings.json"
echo "$out" | grep -qF 'handoff: BUDDY_HANDOFF_AT=400k in' || fail "QA-3b: the size is named without busy too:
$out"
echo "$out" | grep -qF 'but buddy busy is NOT wired, so no session is ever told' || fail "QA-3b: a size with no busy hook says nobody is told:
$out"

# QA-4: a destination that cannot be built fails LOUD — non-zero, and names it.
rm -rf "$WORK/bin/buddy"
mkdir "$WORK/bin/buddy"
out=$(inst 2>&1) && rc=0 || rc=$?
[ "$rc" -ne 0 ] || fail "QA-4: an unbuildable target must fail the install"
echo "$out" | grep -qF 'is a directory, not a binary' || fail "QA-4: the failure must say why:
$out"
echo "$out" | grep -qx 'install: done' && fail "QA-4: a failed install printed done"

# QA-5: the sub-steps' failures are the install's: a skill step that fails
# (its destination a directory) fails the install, not a sed after it.
rmdir "$WORK/bin/buddy"
rm -f "$WORK/skill/SKILL.md"; mkdir "$WORK/skill/SKILL.md"
out=$(inst 2>&1) && rc=0 || rc=$?
[ "$rc" -ne 0 ] || fail "QA-5: a failed skill step must fail the install:
$out"
echo "$out" | grep -qF 'install-skill exited' || fail "QA-5: the failure must name the step:
$out"

# QA-6: the daemon step (4), against a FAKE launchctl. A new daemon build is
# refused its first spawn by a launch constraint and macOS re-registers it about
# ten seconds later (measured, install.sh's header); the old ten-second wait
# lost that race and died before setup-clone and the hook report. The fake
# answers `print` with "spawn scheduled" and that exit reason until it has been
# asked FAKE_RUN_AFTER times, then "running".
#
# NOT THE REAL DAEMON under the mutants measured (BUDDY_LAUNCHCTL ignored,
# BUDDY_PLIST ignored, each killed by QA-6a): HOME is a throwaway, so the
# default plist path does not exist, and a guard launchctl first on PATH
# records being reached. An absolute /bin/launchctl or a ~user lookup would
# get past both; nothing in install.sh spells either. Go's caches stay the real ones, or every
# build here is cold. Its own skill dir: QA-5 leaves $WORK/skill unwritable.
FAKE="$WORK/fake" GUARD="$WORK/guard"
mkdir -p "$FAKE" "$GUARD" "$WORK/home" "$WORK/daemon"
cat > "$FAKE/launchctl" <<'EOF2'
#!/bin/sh
case $1 in
print)
	n=$(cat "$FAKE_DIR/prints" 2>/dev/null || echo 0); n=$((n + 1)); echo "$n" > "$FAKE_DIR/prints"
	if [ "$n" -gt "$FAKE_RUN_AFTER" ]; then
		printf 'gui/501/agent = {\n\tstate = running\n\tpid = 4242\n}\n'
	else
		printf 'gui/501/agent = {\n\tstate = spawn scheduled\n\tlast exit reason = OS_REASON_CODESIGNING\n}\n'
	fi ;;
kickstart) echo "$*" >> "$FAKE_DIR/kicks"; [ -z "${FAKE_KICK_FAIL:-}" ] || exit 1 ;;
*) echo "fake launchctl: unexpected: $*" >&2; exit 64 ;;
esac
EOF2
printf '#!/bin/sh\necho "$*" >> "%s/reached"\nexit 1\n' "$GUARD" > "$GUARD/launchctl"
chmod +x "$FAKE/launchctl" "$GUARD/launchctl"
cat > "$WORK/agent.plist" <<EOF2
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
  <key>Label</key><string>com.buddy-system.buddylistd</string>
  <key>ProgramArguments</key><array><string>$WORK/daemon/buddylist</string><string>serve</string></array>
</dict></plist>
EOF2
gocache=$(go env GOCACHE) gomodcache=$(go env GOMODCACHE)
# inst_daemon <polls until running> [wait seconds] [FAIL: kickstart fails]. Through env, not as
# assignments in front of the call: those are not exported from a function
# call in every sh (install.sh's step 5 says the same).
inst_daemon() {
	rm -f "$FAKE/prints" "$FAKE/kicks"
	env HOME="$WORK/home" GOCACHE="$gocache" GOMODCACHE="$gomodcache" PATH="$GUARD:$PATH" \
		BUDDY_BIN_DIR="$WORK/bin" BUDDY_SKILL_DIR="$WORK/skill6" BUDDY_SETTINGS="$WORK/settings.json" \
		BUDDY_SETUP_CLONE=off BUDDY_REPO_BIN=off \
		BUDDY_PLIST="$WORK/agent.plist" BUDDY_LAUNCHCTL="$FAKE/launchctl" FAKE_DIR="$FAKE" \
		FAKE_RUN_AFTER="$1" ${2:+"BUDDY_DAEMON_WAIT=$2"} ${3:+"FAKE_KICK_FAIL=$3"} sh scripts/install.sh
}

# QA-6a, the control: running on the first poll is the old one-line report.
out=$(inst_daemon 1 2>&1) || fail "QA-6a: install failed:
$out"
[ -s "$FAKE/kicks" ] || fail "QA-6a: the daemon was never kickstarted, so nothing here is being tested:
$out"
echo "$out" | grep -q 'daemon: restarted on the new build ([^)]*)$' || fail "QA-6a: a prompt restart is one plain line:
$out"
echo "$out" | tail -1 | grep -qx 'install: done' || fail "QA-6a: no done line"
# Where the plist can be read (macOS), the fixture's daemon program was the one
# found and rebuilt; ubuntu has no PlistBuddy and the step names no program.
if [ -x /usr/libexec/PlistBuddy ]; then
	echo "$out" | grep -qF "built $WORK/daemon/buddylist" || fail "QA-6a: the fixture's daemon program was not rebuilt:
$out"
fi

# QA-6b: refused first, running after 11 polls: PAST the old ten-poll wait,
# which is what lost the measured 10.04 s return (Codex: a 3 s case passed
# with the old default). Succeeds, and says how long and what launchd recorded.
out=$(inst_daemon 12 2>&1) || fail "QA-6b: a daemon that comes back late must not fail the install:
$out"
echo "$out" | grep -qF "after 11s; launchd's last exit reason: OS_REASON_CODESIGNING (a new build meets a launch constraint" || fail "QA-6b: a slow return names its wait and the recorded reason:
$out"
echo "$out" | tail -1 | grep -qx 'install: done' || fail "QA-6b: no done line"

# QA-6c: never running within BUDDY_DAEMON_WAIT. Fails non-zero and says why,
# but only AFTER the steps that do not depend on the daemon have run.
out=$(inst_daemon 1000 2 2>&1) && fail "QA-6c: a daemon that never comes back must fail the install:
$out"
echo "$out" | grep -qF "FAILED — the daemon did not come back within 2s (state: spawn scheduled; launchd's last exit reason: OS_REASON_CODESIGNING" || fail "QA-6c: the failure names the wait, the state and the reason:
$out"
# Steps 5 and 6 still ran: setup-clone's arm (off here, so its skip line) and
# the hook report.
echo "$out" | grep -qF 'install: setup-clone: skipped' || fail "QA-6c: step 5 must still run after a daemon failure:
$out"
echo "$out" | grep -q '^install: hooks: ' || fail "QA-6c: the hook report must still run after a daemon failure:
$out"
echo "$out" | tail -1 | grep -qF 'FAILED — the daemon did not come back' || fail "QA-6c: the failure must be the LAST line:
$out"
echo "$out" | grep -qx 'install: done' && fail "QA-6c: a failed install printed done"
out=$(inst_daemon 1 soon 2>&1) && fail "QA-6c: a non-numeric wait must be refused:
$out"
echo "$out" | grep -qF "BUDDY_DAEMON_WAIT must be a whole number of seconds, not 'soon'" || fail "QA-6c: the refusal must say why:
$out"
out=$(inst_daemon 1 99999999999999999999 2>&1) && fail "QA-6c: a wait past the shell's integer range must be refused:
$out"
echo "$out" | grep -qF 'BUDDY_DAEMON_WAIT must be at most 9999 seconds' || fail "QA-6c: the bound must say why:
$out"

# QA-6d: a kickstart that fails is deferred like a daemon that never returns.
out=$(inst_daemon 1 2 FAIL 2>&1) && fail "QA-6d: a failed kickstart must fail the install:
$out"
echo "$out" | grep -q '^install: hooks: ' || fail "QA-6d: the hook report must still run after a failed kickstart:
$out"
echo "$out" | tail -1 | grep -qF 'FAILED — launchctl kickstart -k gui/' || fail "QA-6d: the failed kickstart must be the LAST line:
$out"
[ ! -e "$GUARD/reached" ] || fail "QA-6: the REAL launchctl was reached: $(cat "$GUARD/reached")"

# QA-7: a claims-only install (BUDDY_CHAT=off) into an EMPTY bin dir builds
# buddy, builds no buddylist, installs the skill, says `chat: off`, reports no
# remnant (there is none to find: every path the remnants report reads is
# pointed into $WORK, since the real ~/.claude.json and plist would otherwise
# make this a test of the operator's machine), and counts the SIX buddy hook
# lines, so the fixture settings file (no busy, no presence) names exactly
# `buddy busy`. The daemon step is NOT switched off here: skipping it is the
# knob's own promise, and the guard launchctl catches a real one being reached.
# QA-1 is the control: the same fixture under the default names both buddylist
# lines too.
mkdir -p "$WORK/bin7"
inst_claims() {
	env BUDDY_BIN_DIR="$WORK/bin7" BUDDY_SKILL_DIR="$WORK/skill7" BUDDY_SETTINGS="$WORK/settings7.json" \
		BUDDY_SETUP_CLONE=off BUDDY_REPO_BIN=off BUDDY_LAUNCHCTL="$GUARD/launchctl" \
		BUDDY_PLIST="$WORK/plist7" BUDDY_CLAUDE_JSON="$WORK/claude7.json" "$@" sh scripts/install.sh
}
cp "$WORK/settings.orig" "$WORK/settings7.json"
grep -q ' busy' "$WORK/settings7.json" && fail "QA-7: the fixture must not wire busy"
out=$(inst_claims BUDDY_CHAT=off 2>&1) || fail "QA-7: claims-only install failed:
$out"
[ -x "$WORK/bin7/buddy" ] || fail "QA-7: buddy missing"
[ ! -e "$WORK/bin7/buddylist" ] || fail "QA-7: a claims-only install built buddylist"
echo "$out" | grep -q 'built .*buddylist' && fail "QA-7: a claims-only install reports building buddylist:
$out"
cmp -s skills/buddy/SKILL.md "$WORK/skill7/SKILL.md" || fail "QA-7: skill not installed"
echo "$out" | grep -qxF 'install: chat: off (claims-only install)' || fail "QA-7: the chat line is missing:
$out"
echo "$out" | grep -qF 'left alone' && fail "QA-7: nothing was left alone in an empty bin dir:
$out"
echo "$out" | grep -qF 'daemon: skipped (BUDDY_CHAT=off: claims-only install)' || fail "QA-7: the daemon step must say it was skipped for claims-only:
$out"
missing=$(echo "$out" | sed -n 's/.*NOT wired in [^:]*: *\(.*\) (README.*/\1/p')
[ "$missing" = "buddy busy" ] || fail "QA-7: hook report named '$missing', want 'buddy busy'"
echo "$out" | tail -1 | grep -qx 'install: done' || fail "QA-7: no done line"
# The fold: spaces and case around `off` are the same knob; anything else is the
# default, which the all-but-buddylist assertions above would then fail on.
out=$(inst_claims BUDDY_CHAT=' OFF ' 2>&1) || fail "QA-7: ' OFF ' install failed:
$out"
echo "$out" | grep -qxF 'install: chat: off (claims-only install)' || fail "QA-7: ' OFF ' must fold to off:
$out"
[ ! -e "$WORK/bin7/buddylist" ] || fail "QA-7: ' OFF ' built buddylist"
out=$(inst_claims BUDDY_CHAT=no 2>&1) || fail "QA-7: BUDDY_CHAT=no install failed:
$out"
echo "$out" | grep -qF 'chat: off' && fail "QA-7: BUDDY_CHAT=no is the default, not off:
$out"
[ -x "$WORK/bin7/buddylist" ] || fail "QA-7: the default under BUDDY_CHAT=no must build buddylist"

# QA-8: the remnants report. A stale buddylist in the bin dir (QA-7's last run
# left a real one; it is replaced by a marker file so cmp can prove it was not
# rebuilt), a plist at BUDDY_PLIST and a .claude.json naming buddylist as an
# MCP server are each NAMED on one `left alone` line, and none is touched: the
# marker is byte-identical afterwards and the guard launchctl is never reached,
# although a plist now exists for step 4 to act on.
printf 'stale buddylist, not a binary\n' > "$WORK/bin7/buddylist"
cp "$WORK/bin7/buddylist" "$WORK/stale8"
printf 'not a real plist\n' > "$WORK/plist7"
printf '{"mcpServers": {"buddylist": {"command": "%s/bin7/buddylist", "args": ["mcp"]}}}\n' "$WORK" > "$WORK/claude7.json"
out=$(inst_claims BUDDY_CHAT=off 2>&1) || fail "QA-8: claims-only install over remnants failed:
$out"
left=$(echo "$out" | grep '^install: chat: left alone: ' || true)
[ -n "$left" ] || fail "QA-8: no left alone line:
$out"
[ "$(echo "$left" | wc -l | tr -d ' ')" = 1 ] || fail "QA-8: the remnants are ONE line:
$out"
for want in "buddylist at $WORK/bin7/buddylist" "plist at $WORK/plist7" "MCP server registered in $WORK/claude7.json" "nothing was removed, stopped or unloaded"; do
	echo "$left" | grep -qF "$want" || fail "QA-8: the left alone line must name '$want':
$left"
done
cmp -s "$WORK/bin7/buddylist" "$WORK/stale8" || fail "QA-8: the stale buddylist was touched"
[ -f "$WORK/plist7" ] && [ -f "$WORK/claude7.json" ] || fail "QA-8: a remnant was removed"
echo "$out" | grep -qF 'daemon: skipped (BUDDY_CHAT=off: claims-only install)' || fail "QA-8: the daemon step must be skipped with a plist present:
$out"
echo "$out" | tail -1 | grep -qx 'install: done' || fail "QA-8: no done line"
[ ! -e "$GUARD/reached" ] || fail "QA-8: the REAL launchctl was reached: $(cat "$GUARD/reached")"

echo "check-install: GREEN (QA-1 fresh install + exact hook report, QA-2 idempotent, QA-3 all-wired control, QA-3b handoff size report,"
echo "  QA-4 unbuildable target fails loud, QA-5 a sub-step's failure is the install's,"
echo "  QA-6 the daemon waits past the old ten-poll window, says what launchd recorded, and fails last (also on a failed"
echo "  kickstart); fake launchctl, real one guarded; QA-7 BUDDY_CHAT=off builds buddy alone, six-line hook report, the fold;"
echo "  QA-8 remnants named on one line and left byte-identical)"
