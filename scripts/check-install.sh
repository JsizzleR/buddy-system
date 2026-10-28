#!/bin/sh
# check-install.sh — done-check for scripts/install.sh, the machine install
# and upgrade.
#
# HERMETIC: go, git, sh, python3 (QA-9's gate report decodes JSON). Every destination is redirected into a throwaway
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

# BUDDY_MCP_JSON names a file that does not exist: the default is the REAL
# checkout's .mcp.json, and one that registers buddylist would make QA-7 a test
# of this checkout instead of the script (the orchestrator's code pass).
inst() {
	BUDDY_BIN_DIR="$WORK/bin" BUDDY_SKILL_DIR="$WORK/skill" BUDDY_SETTINGS="$WORK/settings.json" \
		BUDDY_DAEMON=off BUDDY_SETUP_CLONE=off BUDDY_REPO_BIN=off BUDDY_MCP_JSON="$WORK/mcp.json" \
		sh scripts/install.sh "$@"
}

# A settings file with every hook but busy and the presence line, spelled the
# way README's wiring spells them.
cat > "$WORK/settings.json" <<'EOF'
{"hooks": {
 "SessionStart": [{"hooks": [{"command": "[ -x \"$HOME/bin/buddy\" ] && \"$HOME/bin/buddy\" hello 2>/dev/null; exit 0"}]}],
 "PreToolUse": [{"matcher": "Edit|Write|NotebookEdit|Bash", "hooks": [{"type": "command", "command": "[ -x \"$HOME/bin/buddy\" ] || exit 0; { \"$HOME/bin/buddy\" gate; } 2>/dev/null || { echo \"buddy gate failed (status $?); refusing the tool call (the gate fails closed). Operator, from a terminal: sh scripts/install.sh in the buddy-system checkout\" >&2; exit 2; }", "timeout": 10, "onFailure": "block"}]}],
 "PostToolUse": [{"hooks": [{"command": "\"$HOME/bin/buddy\" beat 2>/dev/null; exit 0"},
                            {"command": "\"$HOME/bin/buddylist\" alert 2>/dev/null; exit 0"}]}],
 "Stop": [{"hooks": [{"command": "\"$HOME/bin/buddy\" idle 2>/dev/null; exit 0"}]}],
 "SessionEnd": [{"hooks": [{"command": "\"$HOME/bin/buddy\" bye 2>/dev/null; exit 0"}]}]
}}
EOF
# Kept as written: QA-3/QA-3b wire busy and presence into settings.json in
# place, and QA-7 needs this file as it is here.
cp "$WORK/settings.json" "$WORK/settings.orig"
# The fixture's gate is README's entry exactly (D-107): scripts/gate-hook.py
# holds it, check-gate-line.sh holds README to it, and this holds the fixture.
python3 -I -c 'import json, sys
s = json.load(open(sys.argv[1]))
sys.exit(0 if s["hooks"]["PreToolUse"] == [json.loads(sys.argv[2])] else 1)' \
	"$WORK/settings.json" "$(python3 -I scripts/gate-hook.py entry)" ||
	fail "the fixture's PreToolUse is not scripts/gate-hook.py's entry"

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
echo "$out" | grep -qxF "install: gate: README's fail-closed gate entry in $WORK/settings.json (onFailure block, timeout set); native loading not verified here" ||
	fail "QA-1: README's gate entry is reported as such (QA-9's control):
$out"
echo "$out" | tail -1 | grep -qx 'install: done' || fail "QA-1: no done line"

# QA-2: re-run is idempotent — skill current, binaries rebuilt and running.
out=$(inst 2>&1) || fail "QA-2: re-run failed:
$out"
echo "$out" | grep -q 'is current$' || fail "QA-2: the skill should already be current:
$out"

# QA-13: the buddy install.sh built carries this checkout, and `buddy update`
# follows it. Run with inst()'s knobs in its environment, which the verb hands
# install.sh, so a dry run that did run it would still install only into
# $WORK. The dry run names the script and runs nothing (no "install:" line);
# the real run is the control: the same call without the flag runs
# install.sh here, to its done line, and its status is the verb's.
upd() {
	BUDDY_BIN_DIR="$WORK/bin" BUDDY_SKILL_DIR="$WORK/skill" BUDDY_SETTINGS="$WORK/settings.json" \
		BUDDY_DAEMON=off BUDDY_SETUP_CLONE=off BUDDY_REPO_BIN=off BUDDY_MCP_JSON="$WORK/mcp.json" \
		"$WORK/bin/buddy" update "$@" </dev/null
}
# shq is internal/cli's shellQuote: bare when every byte is in its safe set
# and the value does not begin with "=", else single-quoted with each ' as
# '\'' (a checkout path with a quote in it, Codex). A path holding BOTH quote
# characters is not stamped (install.sh says so), and update then refuses.
shq() {
	case $1 in
	'' | =* | *[!A-Za-z0-9._/:@%+=,-]*) printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")" ;;
	*) printf '%s' "$1" ;;
	esac
}
case $ROOT in
*\'*\"* | *\"*\'*) echo "check-install: QA-13 NOT RUN — this checkout's path holds both quote characters, which install.sh cannot stamp" >&2 ;;
*)
out=$(cd "$WORK" && upd --dry-run 2>&1) || fail "QA-13: update --dry-run failed:
$out"
echo "$out" | grep -qF "dry run: nothing installed; buddy update would run: sh $(shq "$ROOT/scripts/install.sh")" ||
	fail "QA-13: the installed buddy does not name this checkout's install.sh:
$out"
echo "$out" | grep -q '^install: ' && fail "QA-13: a dry run ran install.sh:
$out"
out=$(cd "$WORK" && upd 2>&1) || fail "QA-13: update (the control) failed:
$out"
echo "$out" | tail -1 | grep -qx 'install: done' || fail "QA-13: update did not run install.sh to its done line:
$out"
;;
esac

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
		BUDDY_PLIST="$WORK/plist7" BUDDY_CLAUDE_JSON="$WORK/claude7.json" BUDDY_MCP_JSON="$WORK/mcp7.json" \
		"$@" sh scripts/install.sh
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
# rebuilt), a plist at BUDDY_PLIST, a .claude.json and a checkout .mcp.json
# each naming buddylist as an MCP server are each NAMED on one `left alone`
# line, and none is touched: the marker is byte-identical afterwards and the
# guard launchctl is never reached, although a plist now exists for step 4 to
# act on.
printf 'stale buddylist, not a binary\n' > "$WORK/bin7/buddylist"
cp "$WORK/bin7/buddylist" "$WORK/stale8"
printf 'not a real plist\n' > "$WORK/plist7"
printf '{"mcpServers": {"buddylist": {"command": "%s/bin7/buddylist", "args": ["mcp"]}}}\n' "$WORK" > "$WORK/claude7.json"
printf '{"mcpServers": {"buddylist": {"command": "buddylist", "args": ["mcp"]}}}\n' > "$WORK/mcp7.json"
out=$(inst_claims BUDDY_CHAT=off 2>&1) || fail "QA-8: claims-only install over remnants failed:
$out"
left=$(echo "$out" | grep '^install: chat: left alone: ' || true)
[ -n "$left" ] || fail "QA-8: no left alone line:
$out"
[ "$(echo "$left" | wc -l | tr -d ' ')" = 1 ] || fail "QA-8: the remnants are ONE line:
$out"
for want in "buddylist at $WORK/bin7/buddylist" "plist at $WORK/plist7" "MCP server registered in $WORK/claude7.json" "MCP server registered in $WORK/mcp7.json" "nothing was removed, stopped or unloaded"; do
	echo "$left" | grep -qF "$want" || fail "QA-8: the left alone line must name '$want':
$left"
done
cmp -s "$WORK/bin7/buddylist" "$WORK/stale8" || fail "QA-8: the stale buddylist was touched"
[ -f "$WORK/plist7" ] && [ -f "$WORK/claude7.json" ] && [ -f "$WORK/mcp7.json" ] || fail "QA-8: a remnant was removed"
echo "$out" | grep -qF 'daemon: skipped (BUDDY_CHAT=off: claims-only install)' || fail "QA-8: the daemon step must be skipped with a plist present:
$out"
echo "$out" | tail -1 | grep -qx 'install: done' || fail "QA-8: no done line"
[ ! -e "$GUARD/reached" ] || fail "QA-8: the REAL launchctl was reached: $(cat "$GUARD/reached")"

# QA-9 (D-107): the gate's own line is judged, not grepped. The old fail-open
# line is named LEGACY with README's entry to paste, and the file is left
# byte-identical; a bare absolute gate (which step 6's second grep counts as
# wired) and README's command without onFailure are CUSTOM, not verified; the
# old line under another event is no PreToolUse gate at all; and without a
# python the line says it did not look. QA-1's fixture is the control: README's
# entry is reported as README's.
# Its own skill dir: an earlier leg leaves a symlink at $WORK/skill/SKILL.md.
inst9() { # inst9 <settings-file> [VAR=value ...]
	s9=$1; shift
	env BUDDY_BIN_DIR="$WORK/bin" BUDDY_SKILL_DIR="$WORK/skill9" BUDDY_SETTINGS="$s9" \
		BUDDY_DAEMON=off BUDDY_SETUP_CLONE=off BUDDY_REPO_BIN=off BUDDY_MCP_JSON="$WORK/mcp.json" \
		"$@" sh scripts/install.sh
}
legacy=$(python3 -I scripts/gate-hook.py legacy | python3 -I -c 'import json, sys; print(json.dumps(sys.stdin.read().rstrip("\n")))')
printf '{"hooks": {"PreToolUse": [{"matcher": "Edit|Write", "hooks": [{"type": "command", "command": %s, "statusMessage": "x"}]}]}}\n' "$legacy" > "$WORK/s9legacy.json"
cp "$WORK/s9legacy.json" "$WORK/s9legacy.orig"
out=$(inst9 "$WORK/s9legacy.json" 2>&1) || fail "QA-9: install over a legacy gate failed:
$out"
line=$(echo "$out" | grep '^install: gate: ' || true)
[ "$(echo "$line" | wc -l | tr -d ' ')" = 1 ] || fail "QA-9: want exactly one gate line:
$out"
echo "$line" | grep -qF "install: gate: LEGACY fail-open command in $WORK/s9legacy.json: a buddy that crashes or is killed lets the tool call through (D-107). Replace that PreToolUse entry with README's; settings unchanged: " ||
	fail "QA-9: the legacy line is named:
$line"
[ "${line##*settings unchanged: }" = "$(python3 -I scripts/gate-hook.py entry)" ] || fail "QA-9: the legacy line must end with README's entry, whole:
$line"
cmp -s "$WORK/s9legacy.json" "$WORK/s9legacy.orig" || fail "QA-9: the settings file was touched"
printf '{"hooks": {"PreToolUse": [{"matcher": "Edit|Write|NotebookEdit|Bash", "hooks": [{"type": "command", "command": "/abs/bin/buddy gate", "timeout": 10, "onFailure": "block"}]}]}}\n' > "$WORK/s9bare.json"
out=$(inst9 "$WORK/s9bare.json" 2>&1) || fail "QA-9: install over a bare gate failed:
$out"
echo "$out" | grep -qxF "install: gate: custom PreToolUse gate in $WORK/s9bare.json (command differs from README's); crash and timeout blocking NOT VERIFIED; compare README \"Hook wiring\"; settings unchanged" ||
	fail "QA-9: a bare absolute gate is custom, named:
$out"
python3 -I -c 'import json, sys
e = json.loads(sys.argv[1]); del e["hooks"][0]["onFailure"]
print(json.dumps({"hooks": {"PreToolUse": [e]}}))' "$(python3 -I scripts/gate-hook.py entry)" > "$WORK/s9nofail.json"
out=$(inst9 "$WORK/s9nofail.json" 2>&1) || fail "QA-9: install over a gate without onFailure failed:
$out"
echo "$out" | grep -qF "install: gate: custom PreToolUse gate in $WORK/s9nofail.json (onFailure is not \"block\"); crash and timeout blocking NOT VERIFIED" ||
	fail "QA-9: README's command without onFailure is custom, named:
$out"
printf '{"hooks": {"Stop": [{"hooks": [{"command": %s}]}]}}\n' "$legacy" > "$WORK/s9stop.json"
out=$(inst9 "$WORK/s9stop.json" 2>&1) || fail "QA-9: install over a Stop-only gate failed:
$out"
echo "$out" | grep -qxF "install: gate: no PreToolUse buddy gate hook in $WORK/s9stop.json (README \"Hook wiring\")" ||
	fail "QA-9: a gate under another event is no PreToolUse gate:
$out"
out=$(inst9 "$WORK/s9legacy.json" BUDDY_PYTHON="$WORK/no-python" 2>&1) || fail "QA-9: install without a python failed:
$out"
echo "$out" | grep -qxF "install: gate: not checked (no $WORK/no-python to decode $WORK/s9legacy.json); README \"Hook wiring\" has the fail-closed entry (D-107)" ||
	fail "QA-9: without a python the gate line says it did not look:
$out"
echo "$out" | tail -1 | grep -qx 'install: done' || fail "QA-9: no done line"

# QA-10..12 build with a fake `go` on PATH: `go build [flags] -o <out> ./cmd/<name>`
# copies $FAKE_STUBS/<name> to <out>, so each leg chooses what a "build"
# produces without compiling anything. QA-1's real build is their control.
# It finds -o and the package wherever they sit: install.sh passes buddy's
# checkout stamp as -ldflags (D-110), and a fake that read fixed positions
# copied the stamp's flag as the output.
mkdir -p "$WORK/fakego"
cat > "$WORK/fakego/go" <<'EOF'
#!/bin/sh
[ "$1" = build ] || exit 0
shift
out=""
while [ $# -gt 1 ]; do
	case $1 in -o) out=$2; shift ;; esac
	shift
done
[ -n "$out" ] || exit 1
cp "$FAKE_STUBS/${1#./cmd/}" "$out" && chmod 755 "$out"
EOF
chmod 755 "$WORK/fakego/go"
stubs() { # stubs <dir> <buddy's gate body> <buddylist's body>
	mkdir -p "$1"
	printf '#!/bin/sh\nif [ "$1" = gate ]; then\n%s\nfi\necho "buddy help commit-gate"\n' "$2" > "$1/buddy"
	printf '#!/bin/sh\n%s\n' "$3" > "$1/buddylist"
}
OKLIST='echo "usage: buddylist serve"; exit 2'
# An installed binary the legs below must leave alone, or replace.
old() { mkdir -p "$1" && printf '#!/bin/sh\necho old %s\n' "$2" > "$1/$2" && chmod 755 "$1/$2" && cp "$1/$2" "$1/$2.orig"; }

# QA-10 (D-107): a new buddy whose GATE dies fails the install loud at build,
# naming the probe, though `help` passed, and is NOT installed: built over
# the target, it was already in place, and README's gate line refused every
# gated call machine-wide from then on, the installing agent's Bash included
# (the cross-check). The old binary stays byte-identical, and no new file is
# left beside it.
stubs "$WORK/stubs10" 'exit 2' "$OKLIST"
old "$WORK/bin10" buddy
out=$(inst9 "$WORK/settings.orig" BUDDY_BIN_DIR="$WORK/bin10" PATH="$WORK/fakego:$PATH" FAKE_STUBS="$WORK/stubs10" 2>&1) && rc=0 || rc=$?
[ "$rc" -ne 0 ] || fail "QA-10: a buddy whose gate exits 2 must fail the install:
$out"
echo "$out" | grep -qxF "install: FAILED — $WORK/bin10/buddy: the new build's gate exited 2 on its probe (a Write in a fresh temp directory); not installed, $WORK/bin10/buddy left as it was" ||
	fail "QA-10: the failure must name the gate probe and say nothing was installed:
$out"
cmp -s "$WORK/bin10/buddy" "$WORK/bin10/buddy.orig" || fail "QA-10: the old buddy must stay in place, byte-identical"
[ "$(ls "$WORK/bin10" | sort | tr '\n' ' ')" = "buddy buddy.orig " ] || fail "QA-10: the new build must not be left beside the old: $(ls "$WORK/bin10")"

# QA-11 (D-107, the code pass): the probe reaches the gate's Write path from
# wherever it is run. A stub gate that exits 0 only on a decodable Write
# payload stands in for the real one, whose parse-error deny exits 0 and so
# could not tell. (a) A RELATIVE BUDDY_BIN_DIR (the checkout's; install.sh
# cd's there): the probe runs from a temp directory, where a relative path
# named nothing (127). (b) A TMPDIR holding a `"`: printed into the JSON, the
# path broke it. Both legs move the probe through TMPDIR, which install.sh's
# explicit mktemp template honours (macOS's bare `mktemp -d` does not, and
# both legs passed the unfixed install until it did: mutants M21, M22).
# Each installs the stubs, renamed over the old binaries: the
# control that QA-10 and QA-12 see a refusal to rename, not a rename that
# never happens.
stubs "$WORK/stubs11" "exec python3 -I -c 'import json, sys
d = json.load(sys.stdin)
sys.exit(0 if d[\"tool_name\"] == \"Write\" and d[\"tool_input\"][\"file_path\"] else 2)' 2>/dev/null" "$OKLIST"
old "$WORK/bin11" buddy
rel=$(python3 -I -c 'import os, sys; print(os.path.relpath(os.path.realpath(sys.argv[1]), os.path.realpath(sys.argv[2])))' "$WORK/bin11" "$ROOT")
case $rel in /* | '') fail "QA-11: no relative path from $ROOT to $WORK/bin11: '$rel'" ;; esac
# That path climbs out of the checkout, and `..` stops at /, so from any
# directory no deeper than the checkout it names the same file: the probe's
# temp directory is put deeper than the checkout, where it names nothing (a
# shallow TMPDIR let the unfixed install pass; mutant M21).
deep=$(python3 -I -c 'import os, sys; print(os.path.join(sys.argv[1], *["d"] * (os.path.realpath(sys.argv[2]).count("/") + 1)))' "$WORK/deep" "$ROOT")
mkdir -p "$deep"
out=$(inst9 "$WORK/settings.orig" BUDDY_BIN_DIR="$rel" TMPDIR="$deep" PATH="$WORK/fakego:$PATH" FAKE_STUBS="$WORK/stubs11" 2>&1) ||
	fail "QA-11a: an install to a relative BUDDY_BIN_DIR failed:
$out"
cmp -s "$WORK/bin11/buddy" "$WORK/stubs11/buddy" || fail "QA-11a: the new buddy must be renamed over the old one"
mkdir -p "$WORK/t\"mp"
old "$WORK/bin11b" buddy
out=$(inst9 "$WORK/settings.orig" BUDDY_BIN_DIR="$WORK/bin11b" TMPDIR="$WORK/t\"mp" PATH="$WORK/fakego:$PATH" FAKE_STUBS="$WORK/stubs11" 2>&1) ||
	fail "QA-11b: an install with a \" in TMPDIR failed:
$out"
cmp -s "$WORK/bin11b/buddy" "$WORK/stubs11/buddy" || fail "QA-11b: the new buddy must be renamed over the old one"
echo "$out" | tail -1 | grep -qx 'install: done' || fail "QA-11b: no done line"

# QA-12 (D-107, the code pass and the cross-check): a new buddylist SIGKILLed
# on its first run fails the install naming ITS failure, and says nothing of
# the gate, which runs buddy (built and installed first, here): the old text
# told the operator README's gate line refused every call. The old buddylist
# stays.
stubs "$WORK/stubs12" 'exit 0' 'kill -9 $$'
old "$WORK/bin12" buddylist
out=$(inst9 "$WORK/settings.orig" BUDDY_BIN_DIR="$WORK/bin12" PATH="$WORK/fakego:$PATH" FAKE_STUBS="$WORK/stubs12" 2>&1) && rc=0 || rc=$?
[ "$rc" -ne 0 ] || fail "QA-12: a killed buddylist must fail the install:
$out"
echo "$out" | grep -qxF "install: FAILED — $WORK/bin12/buddylist: the new build exited 137 on its first run (SIGKILL: a broken ad-hoc signature); not installed, $WORK/bin12/buddylist left as it was" ||
	fail "QA-12: the failure must name buddylist's first run:
$out"
echo "$out" | grep -qiE 'gate line|every gated call' && fail "QA-12: a buddylist failure says nothing of the gate:
$out"
cmp -s "$WORK/bin12/buddylist" "$WORK/bin12/buddylist.orig" || fail "QA-12: the old buddylist must stay in place"
cmp -s "$WORK/bin12/buddy" "$WORK/stubs12/buddy" || fail "QA-12: buddy, built first, is installed"

echo "check-install: GREEN (QA-1 fresh install + exact hook report, QA-2 idempotent, QA-3 all-wired control, QA-3b handoff size report,"
echo "  QA-13 the installed buddy's update names this checkout's install.sh, a dry run runs nothing, and the real run installs,"
echo "  QA-4 unbuildable target fails loud, QA-5 a sub-step's failure is the install's,"
echo "  QA-6 the daemon waits past the old ten-poll window, says what launchd recorded, and fails last (also on a failed"
echo "  kickstart); fake launchctl, real one guarded; QA-7 BUDDY_CHAT=off builds buddy alone, six-line hook report, the fold;"
echo "  QA-8 remnants named on one line and left byte-identical; QA-9 the gate line judged from decoded JSON: legacy named with"
echo "  README's entry and the file untouched, custom and no-onFailure not verified, another event's line no gate, no python said;"
echo "  QA-10 a new buddy whose gate dies fails the install, naming the probe, and the old one stays; QA-11 the probe passes"
echo "  from a relative BUDDY_BIN_DIR and with a \" in TMPDIR, new builds renamed in; QA-12 a killed buddylist names itself,"
echo "  not the gate, and the old one stays)"
