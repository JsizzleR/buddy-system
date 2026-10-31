#!/bin/sh
# check-uninstall.sh — done-check for scripts/uninstall.sh and `buddy uninstall`
# (D-125).
#
# HERMETIC: go, git, sh. install.sh runs ONCE, every destination in a throwaway
# directory (daemon, setup-clone and the checkout's bin/ off), into a prototype
# each case copies afresh. uninstall.sh runs from a scratch COPY of the checkout
# (git init'd), so the checkout's own bin/, its core.hooksPath and its ledger
# are the fixture's, never this one's. HOME is a throwaway, so every default
# path (~/.claude.json, ~/.buddylist, ~/.codex, LaunchAgents) is too. launchctl
# is a FAKE, named by BUDDY_LAUNCHCTL, and a guard launchctl first on PATH
# records being reached: the real one must never be, since the agent's label is
# fixed and the real daemon answers to it.
#
# Each refusal has its control: a full run removes every artifact (U-4), so
# the runs that STOP and remove nothing (uncertain launchd, a failed bootout,
# an install in flight, a posture that does not read off) are not runs that
# never reached the removal. The --chat success path runs against a STUB buddy
# stamped as D-124 stamps until install.sh carries that stamp; U-3 says which.
set -eu
. "$(dirname -- "$0")/lib.sh"
ROOT=$(repo_root "$0")
CDPATH= cd -- "$ROOT"
unset BUDDY_FLEET_SKILL_DIR BUDDY_REPO_BIN BUDDY_CHAT BUDDY_DAEMON

fail() { echo "check-uninstall: FAIL — $*" >&2; exit 1; }
WORK=$(mktemp -d) || fail "mktemp failed"
trap 'rm -rf "$WORK"' EXIT INT TERM
gocache=$(go env GOCACHE) gomodcache=$(go env GOMODCACHE)
AGENT=com.buddy-system.buddylistd
macos=""
[ ! -x /usr/libexec/PlistBuddy ] || macos=1

# The prototype: one real install, chat on, into $WORK/proto.
P=$WORK/proto
mkdir -p "$P/home"
out=$(env HOME="$P/home" GOCACHE="$gocache" GOMODCACHE="$gomodcache" \
	BUDDY_BIN_DIR="$P/bin" BUDDY_SKILL_DIR="$P/skills/buddy" BUDDY_SETTINGS="$P/none.json" \
	BUDDY_DAEMON=off BUDDY_SETUP_CLONE=off BUDDY_REPO_BIN=off BUDDY_MCP_JSON="$P/none-mcp.json" \
	sh scripts/install.sh 2>&1) || fail "the prototype install failed:
$out"
for f in "$P/bin/buddy" "$P/bin/buddylist" "$P/skills/buddy/SKILL.md" "$P/skills/buddy-fleet/SKILL.md" "$P/skills/buddy-lead/SKILL.md"; do
	[ -f "$f" ] || fail "the prototype install left no $f"
done

# The fakes. launchctl: `print` answers loaded (with the daemon's program)
# while $FAKE/loaded exists, else 113, or FAKE_PRINT_RC; `bootout` removes
# the marker unless FAKE_BOOTOUT_FAIL. Every call is logged.
FAKE=$WORK/fake GUARD=$WORK/guard
mkdir -p "$FAKE" "$GUARD"
cat > "$FAKE/launchctl" <<'EOF'
#!/bin/sh
echo "$*" >> "$FAKE_DIR/calls"
case $1 in
print)
	[ -z "${FAKE_PRINT_RC:-}" ] || exit "$FAKE_PRINT_RC"
	[ -f "$FAKE_DIR/loaded" ] || exit 113
	[ -z "${FAKE_NO_PROGRAM:-}" ] || { printf 'gui/501/x = {\n\tstate = running\n}\n'; exit 0; }
	printf 'gui/501/x = {\n\tpath = x.plist\n\tprogram = %s\n\tstate = running\n\t\tstate = active\n}\n' "$(cat "$FAKE_DIR/loaded")" ;;
bootout) [ -z "${FAKE_BOOTOUT_FAIL:-}" ] || exit 5; rm -f "$FAKE_DIR/loaded" ;;
*) echo "fake launchctl: unexpected: $*" >&2; exit 64 ;;
esac
EOF
printf '#!/bin/sh\necho "$*" >> "%s/reached"\nexit 1\n' "$GUARD" > "$GUARD/launchctl"
chmod +x "$FAKE/launchctl" "$GUARD/launchctl"

# setup: a fresh case at $R from the prototype, with the chat daemon's plist
# and program, the checkout's bin/ copies, and every file uninstall must leave
# byte-identical. cp to NEW paths only (never over a running Mach-O).
R=$WORK/case
setup() {
	rm -rf "$R" "$FAKE/calls" "$FAKE/loaded"
	cp -Rp "$P" "$R"
	LA=$R/home/Library/LaunchAgents
	mkdir -p "$R/co/scripts" "$R/co/bin" "$R/daemon" "$LA" "$R/home/.buddylist" "$R/home/.codex"
	cp scripts/uninstall.sh "$R/co/scripts/"
	cp -R skills "$R/co/skills"
	mkrepo "$R/co"
	git -C "$R/co" config core.hooksPath .githooks
	cp "$R/bin/buddy" "$R/co/bin/buddy"
	cp "$R/bin/buddylist" "$R/co/bin/buddylist"
	cp "$R/bin/buddylist" "$R/daemon/buddylist"
	cat > "$LA/$AGENT.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
  <key>Label</key><string>$AGENT</string>
  <key>ProgramArguments</key><array><string>$R/daemon/buddylist</string><string>serve</string></array>
</dict></plist>
EOF
	printf '<plist><dict><key>Label</key><string>com.buddy-system.ergo</string></dict></plist>\n' > "$LA/com.buddy-system.ergo.plist"
	printf '%s' "$R/daemon/buddylist" > "$FAKE/loaded"
	# The never-touched set.
	cat > "$R/settings.json" <<'EOF'
{"env": {"BUDDY_CHAT": "on"}, "hooks": {
 "SessionStart": [{"hooks": [{"command": "[ -x \"$HOME/bin/buddy\" ] && \"$HOME/bin/buddy\" hello 2>/dev/null; exit 0"}]}],
 "PostToolUse": [{"hooks": [{"command": "[ -x \"$HOME/bin/buddylist\" ] && \"$HOME/bin/buddylist\" alert 2>/dev/null; exit 0"}]}]
}}
EOF
	printf '{"projects": {"/x": {"mcpServers": {"buddylist": {"command": "b"}}}}}\n' > "$R/home/.claude.json"
	printf 'journal bytes\n' > "$R/home/.buddylist/journal.db"
	printf '{"hooks": {"PreToolUse": [{"hooks": [{"command": "/abs/buddy gate"}]}]}}\n' > "$R/home/.codex/hooks.json"
	printf 'a fixture ledger, never opened\n' > "$R/co/.git/buddy.db"
	printf 'copy\n' > "$R/bin/agent-model-gate.py"
	printf 'an earlier copy\n' > "$R/skills/buddy/SKILL.md.bak"
	KEEP="$R/settings.json $R/home/.claude.json $R/home/.buddylist/journal.db $R/home/.codex/hooks.json $R/co/.git/buddy.db $R/bin/agent-model-gate.py $R/skills/buddy/SKILL.md.bak $LA/com.buddy-system.ergo.plist"
	keep_sum=$(cksum $KEEP)
}
# snap: every file and link under the case, with its checksum.
# Go's own telemetry counters under the scratch HOME are not ours (every
# `go version -m` bumps one; measured on U-3c), so they are not in it.
snap() { (cd "$R" && find . \( -type f -o -type l \) ! -path './home/Library/Application Support/go/*' | LC_ALL=C sort | while IFS= read -r f; do cksum "$f"; done); }
kept() { [ "$(cksum $KEEP)" = "$keep_sum" ] || fail "$1: a file uninstall must never touch changed:
$(cksum $KEEP)"; }
# un: uninstall.sh from the scratch checkout, everything pointed into $R.
un() {
	env HOME="$R/home" PATH="$GUARD:$PATH" GOCACHE="$gocache" GOMODCACHE="$gomodcache" \
		BUDDY_BIN_DIR="$R/bin" BUDDY_SKILL_DIR="$R/skills/buddy" BUDDY_SETTINGS="$R/settings.json" \
		BUDDY_PLIST="$R/home/Library/LaunchAgents/$AGENT.plist" BUDDY_LAUNCHCTL="$FAKE/launchctl" \
		BUDDY_CLAUDE_JSON="$R/home/.claude.json" BUDDY_MCP_JSON="$R/co/.mcp.json" FAKE_DIR="$FAKE" \
		${XENV:-} sh "${CO:-$R/co}/scripts/uninstall.sh" "$@"
}
gone() { for f in "$@"; do [ ! -e "$f" ] && [ ! -L "$f" ] || fail "$label: $f should be removed:
$out"; done; }
there() { for f in "$@"; do [ -e "$f" ] || fail "$label: $f should be left:
$out"; done; }
bins="$R/bin/buddy $R/bin/buddylist $R/co/bin/buddy $R/co/bin/buddylist $R/daemon/buddylist"
skillmds="$R/skills/buddy/SKILL.md $R/skills/buddy-fleet/SKILL.md $R/skills/buddy-lead/SKILL.md"
plist() { printf '%s' "$R/home/Library/LaunchAgents/$AGENT.plist"; }
# The calls file must exist (the plan always asks print) or "no call but
# print" is vacuous (the panel).
only_prints() {
	[ -s "$FAKE/calls" ] || fail "$label: launchctl was never asked print, so the dry-run check is vacuous"
	! grep -qv '^print ' "$FAKE/calls" || fail "$label: launchctl got more than print:
$(cat "$FAKE/calls")"
}

# U-1: a full dry run plans every removal and changes nothing.
label=U-1
setup; before=$(snap)
out=$(XENV= un --dry-run 2>&1) || fail "U-1: dry run failed:
$out"
[ "$(snap)" = "$before" ] || fail "U-1: a dry run changed the tree:
$out"
only_prints
# Whole lines: "would remove $R/bin/buddy" is a prefix of the buddylist line.
for f in $bins; do
	echo "$out" | grep -qxF "uninstall: $(basename -- "$f"): would remove $f" || fail "U-1: the plan does not name $f:
$out"
done
for f in $skillmds; do
	echo "$out" | grep -qxF "uninstall: skill: would remove $f" || fail "U-1: the plan does not name $f:
$out"
done
echo "$out" | grep -qF "would remove the launchd job $AGENT" || fail "U-1: the plan does not name the job:
$out"
[ -z "$macos" ] || echo "$out" | grep -qF "would remove its plist $(plist)" || fail "U-1: the plan does not name the plist:
$out"
echo "$out" | tail -1 | grep -qx 'uninstall: dry run: nothing was removed' || fail "U-1: no dry-run last line:
$out"

# U-2: a --chat dry run runs no update and changes nothing.
label=U-2
setup; before=$(snap)
out=$(un --chat --dry-run 2>&1) || fail "U-2: dry run failed:
$out"
[ "$(snap)" = "$before" ] || fail "U-2: a --chat dry run changed the tree:
$out"
only_prints
echo "$out" | grep -qF "would run first: BUDDY_CHAT=off $R/bin/buddy update" || fail "U-2: the posture step is not named:
$out"
# update's own dry run gives its verdict now (the Fable lens) and installs
# nothing; this prototype's buddy carries this checkout's stamp.
echo "$out" | grep -qF "dry run: nothing installed; buddy update would run: sh " || fail "U-2: update's dry run did not report:
$out"
echo "$out" | grep -q '^install: ' && fail "U-2: a dry run ran install.sh:
$out"
echo "$out" | grep -qxF "uninstall: buddy: would remove $R/bin/buddy" && fail "U-2: --chat plans to remove buddy:
$out"

# U-4: the full run, the control for every STOPPED case below.
label=U-4
setup
out=$(un 2>&1) || fail "U-4: uninstall failed:
$out"
gone $bins $skillmds
grep -qx "bootout gui/$(id -u)/$AGENT" "$FAKE/calls" || fail "U-4: no bootout:
$(cat "$FAKE/calls")"
[ ! -e "$FAKE/loaded" ] || fail "U-4: the job is still loaded"
if [ -n "$macos" ]; then
	gone "$(plist)"
	ls "$R/home/.buddylist/$AGENT.plist.removed-"* >/dev/null 2>&1 || fail "U-4: the plist was not kept in ~/.buddylist:
$out"
fi
there "$R/skills/buddy/SKILL.md.bak" "$R/co/.git/buddy.db"
kept U-4
for want in "settings: $R/settings.json still wires: buddy hello buddylist alert" \
	"mcp: buddylist is registered for /x in $R/home/.claude.json: cd /x && claude mcp remove buddylist --scope local" \
	"codex: $R/home/.codex/hooks.json names buddy" \
	"left: core.hooksPath = .githooks in $R/co" \
	"left: $R/bin/agent-model-gate.py" \
	"data: left: this checkout's ledger" \
	"left: $R/home/Library/LaunchAgents/com.buddy-system.ergo.plist"; do
	echo "$out" | grep -qF "$want" || fail "U-4: no report line '$want':
$out"
done
echo "$out" | tail -1 | grep -qx 'uninstall: done' || fail "U-4: no done line:
$out"

# U-5: a second run finds nothing and finishes clean (a stopped run is
# finished by running it again).
label=U-5
out=$(un 2>&1) || fail "U-5: the re-run failed:
$out"
echo "$out" | grep -q '^uninstall: removed' && fail "U-5: the re-run removed something:
$out"
echo "$out" | tail -1 | grep -qx 'uninstall: done' || fail "U-5: no done line:
$out"

# U-6..U-8: runs that STOP before removing anything (U-4 is their control).
stopped() { # stopped <label> <expected text> <uninstall args...>
	label=$1 want=$2; shift 2
	setup; before=$(snap)
	out=$(un "$@" 2>&1) && fail "$label: should have stopped:
$out"
	echo "$out" | grep -qF "$want" || fail "$label: the stop does not say '$want':
$out"
	[ "$(snap)" = "$before" ] || fail "$label: a stopped run removed something:
$out"
}
# XENV set and cleared as statements: an assignment in front of a FUNCTION
# call is unspecified in POSIX sh and persisted here (measured: U-7's leaked).
XENV=FAKE_PRINT_RC=5; stopped U-6 "exited 5, so whether the chat daemon is loaded cannot be said"
# U-6b: no launchctl beside a plist is uncertain too, never "no daemon" (Codex).
XENV=BUDDY_LAUNCHCTL=$WORK/no-launchctl; stopped U-6b "no launchctl at $WORK/no-launchctl, so whether the chat daemon is loaded cannot be said"
# U-6c: a path holding a newline is refused before anything is planned
# (Codex: split, it removed two unchecked paths). Exported, not XENV: XENV is
# word-split.
BUDDY_FLEET_SKILL_DIR="$R/skills/buddy-fleet
victim"; export BUDDY_FLEET_SKILL_DIR; XENV=
stopped U-6c "a path to act on holds a newline"
unset BUDDY_FLEET_SKILL_DIR
# U-6d: a checkout whose path ENDS in a newline, beside a sibling without it:
# a substitution read it as the sibling's (Codex, re-verify), whose bin/ copy
# must be left.
label=U-6d; setup
NL='
'
mv "$R/co" "$R/co$NL"; mkdir -p "$R/co/bin"; cp "$R/bin/buddy" "$R/co/bin/buddy"
out=$(CO="$R/co$NL" un 2>&1) && fail "U-6d: a checkout path ending in a newline must stop:
$out"
echo "$out" | grep -qF "a path to act on holds a newline" || fail "U-6d: the stop does not say why:
$out"
there "$R/co/bin/buddy" "$R/bin/buddy"
XENV=FAKE_BOOTOUT_FAIL=1; stopped U-7 "launchctl bootout gui/$(id -u)/$AGENT failed; nothing was removed"; XENV=
grep -q '^bootout' "$FAKE/calls" || fail "U-7: bootout was never tried, so nothing here was tested"
# U-8: a build file whose pid is alive (this shell's) is an install in
# flight; one whose pid is dead was left by a killed install, and is named,
# not a stop (U-8b, its control).
label=U-8; setup; : > "$R/bin/buddy.new-$$"; before=$(snap)
out=$(un 2>&1) && fail "U-8: an install in flight must stop the run:
$out"
echo "$out" | grep -qF "an install is in flight ($R/bin/buddy.new-$$ exists and pid $$ is alive)" || fail "U-8: the stop does not name the build:
$out"
[ "$(snap)" = "$before" ] || fail "U-8: a stopped run removed something"
label=U-8b; setup
sh -c 'exit 0' & dead=$!; wait "$dead" || true
: > "$R/bin/buddy.new-$dead"
out=$(un 2>&1) || fail "U-8b: a dead install's leftover must not stop the run:
$out"
echo "$out" | grep -qF "left: $R/bin/buddy.new-$dead, a build a killed install left behind" || fail "U-8b: the leftover is not named:
$out"
gone "$R/bin/buddy"

# U-9: a foreign buddy and an edited skill are left and named; a buddy whose
# bytes are ours but cannot run (a corrupted tail) IS removed.
label=U-9
setup
rm "$R/bin/buddy"; printf '#!/bin/sh\nexit 127\n' > "$R/bin/buddy"; chmod +x "$R/bin/buddy"
printf 'my edit\n' >> "$R/skills/buddy-fleet/SKILL.md"
rm "$R/co/bin/buddy"
# The checkout's buddy: the real bytes with the last 4096 cut off, so it no
# longer runs but still carries its build-info line.
sz=$(wc -c < "$P/bin/buddy")
head -c $((sz - 4096)) "$P/bin/buddy" > "$R/co/bin/buddy"; chmod +x "$R/co/bin/buddy"
out=$(un 2>&1) && fail "U-9: a run that leaves an executable at the hooks' buddy path must not say done:
$out"
echo "$out" | tail -1 | grep -qF "INCOMPLETE — $R/bin/buddy is still there and executable" || fail "U-9: the last line must say INCOMPLETE:
$out"
there "$R/bin/buddy" "$R/skills/buddy-fleet/SKILL.md"
gone "$R/co/bin/buddy" "$R/skills/buddy/SKILL.md"
echo "$out" | grep -qF "buddy: left: $R/bin/buddy is not this project's buddy" || fail "U-9: the foreign buddy is not named:
$out"
echo "$out" | grep -qF "skill: left: $R/skills/buddy-fleet/SKILL.md differs" || fail "U-9: the edited skill is not named:
$out"

# U-9c: a directory at the hooks' buddy path passes README's `[ -x ]` and then
# fails to exec, so it is INCOMPLETE too, never done (Codex).
label=U-9c
setup
rm "$R/bin/buddy"; mkdir "$R/bin/buddy"
out=$(un 2>&1) && fail "U-9c: a directory at the hooks' buddy path must not say done:
$out"
echo "$out" | tail -1 | grep -qF "INCOMPLETE — $R/bin/buddy is still there and executable" || fail "U-9c: the last line must say INCOMPLETE:
$out"

# U-9b: a buddylist at buddy's path is not this project's buddy (the build-info
# line is $-anchored: cmd/buddy is a prefix of cmd/buddylist).
label=U-9b
setup
rm "$R/co/bin/buddy"; cp "$R/bin/buddylist" "$R/co/bin/buddy"
out=$(un 2>&1) || fail "U-9b: uninstall failed:
$out"
there "$R/co/bin/buddy"
echo "$out" | grep -qF "buddy: left: $R/co/bin/buddy is not this project's buddy" || fail "U-9b: the buddylist at buddy's path is not named:
$out"

# U-11: a loaded job that runs something other than a buddylist is not ours:
# no bootout, its plist stays, and so does every buddylist copy (a wrapper
# may exec one; removing it would leave the job respawn-failing). buddy and
# the skills still go (U-4 is the control: the same case, ours).
label=U-11
setup
printf '/usr/bin/true' > "$FAKE/loaded"
out=$(un 2>&1) || fail "U-11: uninstall failed:
$out"
grep -qs '^bootout' "$FAKE/calls" && fail "U-11: a job that is not ours was booted out"
there "$(plist)" "$R/bin/buddylist" "$R/daemon/buddylist" "$R/co/bin/buddylist"
gone "$R/bin/buddy" $skillmds
echo "$out" | grep -qF "daemon: left: the loaded job $AGENT runs /usr/bin/true" || fail "U-11: the foreign job is not named:
$out"
echo "$out" | tail -1 | grep -qx 'uninstall: done, except the chat pieces named left above' || fail "U-11: the last line must say what is left:
$out"
# U-11d: a job under the agent's label whose buddylist program is already gone
# is respawn-failing, and IS booted out (U-11c's other side).
label=U-11d
setup
printf '%s' "$R/gone/buddylist" > "$FAKE/loaded"
out=$(un 2>&1) || fail "U-11d: uninstall failed:
$out"
grep -q '^bootout' "$FAKE/calls" || fail "U-11d: a job whose program is gone was not booted out"
echo "$out" | tail -1 | grep -qx 'uninstall: done' || fail "U-11d: no done line:
$out"
# U-11f: a foreign program in a directory this user cannot search is not
# "gone" ([ ! -e ] is true on EACCES too; Codex, re-verify): never booted out.
# Root searches every directory, so the case cannot be built under it.
if [ "$(id -u)" = 0 ]; then
	echo "check-uninstall: U-11f NOT RUN — as root every directory is searchable" >&2
else
	label=U-11f
	setup
	mkdir -p "$R/priv"; printf '#!/bin/sh\nexit 0\n' > "$R/priv/buddylist"; chmod 000 "$R/priv"
	printf '%s' "$R/priv/buddylist" > "$FAKE/loaded"
	out=$(un 2>&1) && rc=0 || rc=$?
	chmod 700 "$R/priv"
	[ "$rc" -eq 0 ] || fail "U-11f: uninstall failed:
$out"
	grep -qs '^bootout' "$FAKE/calls" && fail "U-11f: a job whose program could not be read was booted out:
$out"
	echo "$out" | grep -qF "daemon: left: the loaded job $AGENT runs $R/priv/buddylist" || fail "U-11f: the job is not named left:
$out"
	# U-11g: the same program reached through a symlinked ancestor that points
	# into the unsearchable directory (Codex, second re-verify).
	label=U-11g
	setup
	mkdir -p "$R/priv/sub" "$R/pub"; printf '#!/bin/sh\nexit 0\n' > "$R/priv/sub/buddylist"
	ln -s "$R/priv/sub" "$R/pub/link"; chmod 000 "$R/priv"
	printf '%s' "$R/pub/link/buddylist" > "$FAKE/loaded"
	out=$(un 2>&1) && rc=0 || rc=$?
	chmod 700 "$R/priv"
	[ "$rc" -eq 0 ] || fail "U-11g: uninstall failed:
$out"
	grep -qs '^bootout' "$FAKE/calls" && fail "U-11g: a job whose program sits behind an unsearchable symlink was booted out:
$out"
fi
# U-11e: a loaded job whose print names no program is uncertain: a stop.
XENV=FAKE_NO_PROGRAM=1; stopped U-11e "names no program, so whose job it is cannot be said"; XENV=

# U-11c: a job running a program NAMED buddylist that is not ours by its bytes
# is not booted out either (Codex: it was, on the basename).
label=U-11c
setup
mkdir -p "$R/foreign"; printf '#!/bin/sh\nexit 0\n' > "$R/foreign/buddylist"
printf '%s' "$R/foreign/buddylist" > "$FAKE/loaded"
out=$(un 2>&1) || fail "U-11c: uninstall failed:
$out"
grep -qs '^bootout' "$FAKE/calls" && fail "U-11c: a job running a foreign buddylist was booted out"
there "$R/foreign/buddylist" "$R/bin/buddylist"
echo "$out" | grep -qF "daemon: left: the loaded job $AGENT runs $R/foreign/buddylist" || fail "U-11c: the foreign job is not named:
$out"
if [ -n "$macos" ]; then
	label=U-11b
	setup
	rm -f "$FAKE/loaded"
	sed "s#<string>$AGENT</string>#<string>com.example.other</string>#" "$(plist)" > "$WORK/p" && cat "$WORK/p" > "$(plist)"
	out=$(un 2>&1) || fail "U-11b: uninstall failed:
$out"
	there "$(plist)" "$R/daemon/buddylist" "$R/bin/buddylist"
	echo "$out" | grep -qF "daemon: left: $(plist) names $R/daemon/buddylist under the Label com.example.other" || fail "U-11b: the other agent's plist is not named:
$out"
fi

# U-12: a checkout path holding a space and a glob character, beside a clone
# the unquoted glob would match: its own bin/ copies go, the decoy's stay.
label=U-12
setup
mv "$R/co" "$R/my co[x]"
mkdir -p "$R/my cox/bin"
cp "$R/bin/buddy" "$R/my cox/bin/buddy"
out=$(CO="$R/my co[x]" un 2>&1) || fail "U-12: uninstall failed:
$out"
gone "$R/my co[x]/bin/buddy" "$R/my co[x]/bin/buddylist" "$R/bin/buddy"
there "$R/my cox/bin/buddy"

# U-3: --chat. Real path when install.sh stamps the posture (D-124), else the
# stop it must make, and the success path against a stamped STUB buddy.
label=U-3
real=""
# Read from what install.sh BUILT, not from its text: a comment naming the
# stamp would otherwise flip this branch before any build carries one.
go version -m "$P/bin/buddy" 2>/dev/null | grep -qF 'internal/cli.installedChat=' && real=1
if [ -n "$real" ]; then
	setup
	out=$(XENV="BUDDY_DAEMON=off BUDDY_SETUP_CLONE=off BUDDY_REPO_BIN=off" un --chat 2>&1) || fail "U-3: --chat failed:
$out"
	gone "$R/bin/buddylist" "$R/daemon/buddylist"
	there "$R/bin/buddy" $skillmds "$R/co/bin/buddylist"
	"$R/bin/buddy" help >/dev/null 2>&1 || fail "U-3: the buddy left does not run"
	echo "$out" | tail -1 | grep -qx 'uninstall: done: the chat half is removed and buddy stays, claims-only' || fail "U-3: no done line:
$out"
else
	echo "check-uninstall: U-3 real --chat path NOT RUN — install.sh does not stamp the chat posture yet (D-124, chat-sticks); the stub below stands in" >&2
	setup; before_bins=$(cksum "$R/bin/buddylist" "$R/daemon/buddylist" "$R/co/bin/buddylist")
	out=$(XENV="BUDDY_DAEMON=off BUDDY_SETUP_CLONE=off BUDDY_REPO_BIN=off" un --chat 2>&1) && fail "U-3: --chat with no posture stamp must stop:
$out"
	echo "$out" | grep -qF "records no chat posture after the update" || fail "U-3: the stop does not say why:
$out"
	[ "$(cksum "$R/bin/buddylist" "$R/daemon/buddylist" "$R/co/bin/buddylist")" = "$before_bins" ] || fail "U-3: a stopped --chat removed chat"
	grep -qs '^bootout' "$FAKE/calls" && fail "U-3: a stopped --chat booted the daemon out"
fi
# The stub: a Go binary built as this module's cmd/buddy, with D-124's -X
# first in its -ldflags, whose `update` records BUDDY_CHAT and exits STUB_RC.
S=$WORK/stub
mkdir -p "$S/cmd/buddy"
printf 'module github.com/JsizzleR/buddy-system\n\ngo 1.21\n' > "$S/go.mod"
cat > "$S/cmd/buddy/main.go" <<'EOF'
package main

import (
	"os"
	"strconv"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "update" {
		_ = os.WriteFile(os.Getenv("STUB_LOG"), []byte("BUDDY_CHAT="+os.Getenv("BUDDY_CHAT")+"\n"), 0o644)
		rc, _ := strconv.Atoi(os.Getenv("STUB_RC"))
		os.Exit(rc)
	}
}
EOF
for p in on off; do
	(cd "$S" && env GOWORK=off GOFLAGS= GOTOOLCHAIN=local GOCACHE="$gocache" \
		go build -ldflags "-X 'github.com/JsizzleR/buddy-system/internal/cli.installedChat=$p'" -o "$S/buddy-$p" ./cmd/buddy) ||
		fail "the stub buddy did not build"
done
stub() { setup; rm "$R/bin/buddy"; cp "$S/buddy-$1" "$R/bin/buddy"; }
label=U-3b
stub off
out=$(XENV="STUB_LOG=$WORK/stub.log STUB_RC=0" un --chat 2>&1) || fail "U-3b: --chat over a buddy stamped off failed:
$out"
grep -qx 'BUDDY_CHAT=off' "$WORK/stub.log" || fail "U-3b: update was not run with BUDDY_CHAT=off"
gone "$R/bin/buddylist" "$R/daemon/buddylist" "$R/co/bin/buddylist"
there "$R/bin/buddy" "$R/co/bin/buddy" $skillmds
grep -q '^bootout' "$FAKE/calls" || fail "U-3b: the daemon was not booted out"
[ -z "$macos" ] || gone "$(plist)"
kept U-3b
echo "$out" | grep -qF 'add "env": {"BUDDY_CHAT": "off"}' || fail "U-3b: the hello hint is not named (the fixture's env says on):
$out"
echo "$out" | tail -1 | grep -qx 'uninstall: done: the chat half is removed and buddy stays, claims-only' || fail "U-3b: no done line:
$out"
label=U-3c; stub on; before=$(snap)
out=$(XENV="STUB_LOG=$WORK/stub.log STUB_RC=0" un --chat 2>&1) && fail "U-3c: a buddy still stamped on must stop --chat:
$out"
echo "$out" | grep -qF "still records chat on after the update (it reads 'on')" || fail "U-3c: the stop does not say what it read:
$out"
[ "$(snap)" = "$before" ] || fail "U-3c: a stopped --chat removed something:
$(printf '%s\n' "$before" > "$WORK/b"; snap > "$WORK/a"; diff "$WORK/b" "$WORK/a")
$out"
# U-3d: a failed update stops --chat. Over a buddy still stamped on, nothing
# changed; over one stamped off (install.sh renames buddy in before its later
# steps can fail), the stop says chat is still running (the Fable lens).
label=U-3d; stub on; before=$(snap)
out=$(XENV="STUB_LOG=$WORK/stub.log STUB_RC=3" un --chat 2>&1) && fail "U-3d: a failed update must stop --chat:
$out"
echo "$out" | grep -qF "buddy update exited 3; nothing of chat was removed" || fail "U-3d: the stop does not name the update:
$out"
[ "$(snap)" = "$before" ] || fail "U-3d: a stopped --chat removed something"
label=U-3e; stub off; before=$(snap)
out=$(XENV="STUB_LOG=$WORK/stub.log STUB_RC=3" un --chat 2>&1) && fail "U-3e: a failed update must stop --chat:
$out"
echo "$out" | grep -qF "NOTE — $R/bin/buddy is already rebuilt claims-only (chat off), so chat is still installed and RUNNING" || fail "U-3e: the stop does not say chat is still running:
$out"
[ "$(snap)" = "$before" ] || fail "U-3e: a stopped --chat removed something"
# U-3f: an exported BUDDY_CHAT that is not off overrides the stamp on the next
# update, so --chat stops before running one (U-3b, stamped off, is the control).
label=U-3f; stub off; rm -f "$WORK/stub.log"; before=$(snap)
out=$(XENV="STUB_LOG=$WORK/stub.log STUB_RC=0 BUDDY_CHAT=on" un --chat 2>&1) && fail "U-3f: --chat under an exported BUDDY_CHAT=on must stop:
$out"
echo "$out" | grep -qF "BUDDY_CHAT=on is exported here" || fail "U-3f: the stop does not name the export:
$out"
[ ! -e "$WORK/stub.log" ] || fail "U-3f: update ran before the stop"
[ "$(snap)" = "$before" ] || fail "U-3f: a stopped --chat removed something"

# U-10: the verb. The prototype's buddy carries THIS checkout's stamp, so its
# `uninstall --dry-run` runs this checkout's uninstall.sh: every knob into $R,
# the checkout's own bin/ off, and nothing may change.
label=U-10
case $ROOT in
*\'*\"* | *\"*\'*) echo "check-uninstall: U-10 NOT RUN — this checkout's path holds both quote characters, which install.sh cannot stamp" >&2 ;;
*)
	setup; before=$(snap)
	out=$(env HOME="$R/home" PATH="$GUARD:$PATH" BUDDY_BIN_DIR="$R/bin" BUDDY_SKILL_DIR="$R/skills/buddy" \
		BUDDY_SETTINGS="$R/settings.json" BUDDY_PLIST="$(plist)" BUDDY_LAUNCHCTL="$FAKE/launchctl" \
		BUDDY_CLAUDE_JSON="$R/home/.claude.json" BUDDY_MCP_JSON="$R/co/.mcp.json" BUDDY_REPO_BIN=off FAKE_DIR="$FAKE" \
		"$R/bin/buddy" uninstall --dry-run </dev/null 2>&1) || fail "U-10: buddy uninstall --dry-run failed:
$out"
	echo "$out" | head -1 | grep -qF "running: sh $ROOT/scripts/uninstall.sh --dry-run" || fail "U-10: the verb does not run this checkout's script:
$out"
	echo "$out" | grep -qxF "uninstall: buddy: would remove $R/bin/buddy" || fail "U-10: the script did not plan the knobs' buddy:
$out"
	echo "$out" | tail -1 | grep -qx 'uninstall: dry run: nothing was removed' || fail "U-10: no dry-run last line:
$out"
	[ "$(snap)" = "$before" ] || fail "U-10: a dry run through the verb changed the tree"
	only_prints
	;;
esac

[ ! -e "$GUARD/reached" ] || fail "the REAL launchctl was reached: $(cat "$GUARD/reached")"
echo "check-uninstall: ok (dry runs change nothing; full and --chat remove what install put in place; stops remove nothing; foreign and edited files left)"
