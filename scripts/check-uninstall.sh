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
# XDG_STATE_HOME too: uninstall removes the model router's state under it
# when it is absolute (D-178, D-179), so a caller's own value would move the
# path U-4 and U-15 expect; U-4r (after U-5, which re-runs U-4's case) sets it
# per case.
unset BUDDY_FLEET_SKILL_DIR BUDDY_REPO_BIN BUDDY_CHAT BUDDY_DAEMON CLAUDE_CONFIG_DIR XDG_STATE_HOME
# The umask too: under a caller's 002 the fixture's ~/.local/state is made
# group-writable, and uninstall then leaves the model router's state (its
# parent check), so U-1 went red on a correct tree (Fable).
umask 022

fail() { echo "check-uninstall: FAIL — $*" >&2; exit 1; }
WORK=$(mktemp -d) || fail "mktemp failed"
# The gated install's watchdog (U-13h/i) too: a case that fails while one is
# armed left it sleeping out its bound (measured under a mutant).
# `|| true`: under set -e a failed kill of a watchdog already ended set the
# check's status to 1 after its ok line (measured).
trap '[ -z "${gwd:-}" ] || kill "$gwd" 2>/dev/null || true; rm -rf "$WORK"' EXIT INT TERM
# Every skill destination here is judged by where it lands (D-181): under a
# temp directory inside a work tree, each would be `vcs` and left, and U-4 went
# red on a correct tree (the panel); straight under another checkout's skills/,
# `clone` (Codex code pass). Anything but `ok` is said by name, never a
# confusing red.
. scripts/skill-dest.sh
mkdir "$WORK/none"; skd_judge "$WORK" "$WORK/none" "$WORK/none"; rmdir "$WORK/none"
[ "$SKD_KIND" = ok ] || fail "the temp directory $WORK is judged $SKD_KIND${SKD_MARK:+ ($SKD_MARK)}, where every fixture skill directory is left; set TMPDIR to a plain directory outside any work tree or checkout"
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
# Every shipped skill's SKILL.md, read from skills/ as the installer reads it
# (#92: the list here was written by hand and named three of four).
for f in "$P/bin/buddy" "$P/bin/buddylist" $(for s in skills/*/SKILL.md; do printf '%s ' "$P/$s"; done); do
	[ -f "$f" ] || fail "the prototype install left no $f"
done

# The fakes. launchctl: `print` answers loaded (with the job's program)
# while its marker exists ($FAKE/loaded for the daemon, $FAKE/loaded.<label>
# for any other label), else 113, or FAKE_PRINT_RC; `bootout` removes the
# marker unless FAKE_BOOTOUT_FAIL (every label) or FAKE_BOOTOUT_FAIL_LABEL
# names it. Every call is logged.
FAKE=$WORK/fake GUARD=$WORK/guard
mkdir -p "$FAKE" "$GUARD"
cat > "$FAKE/launchctl" <<'EOF'
#!/bin/sh
echo "$*" >> "$FAKE_DIR/calls"
lbl=${2##*/}
m=$FAKE_DIR/loaded
[ "$lbl" = com.buddy-system.buddylistd ] || m=$FAKE_DIR/loaded.$lbl
case $1 in
print)
	# FAKE_ON_PRINT: a script run once, at the first print, which uninstall
	# asks while it holds the install lock (U-13e: the real overlap).
	if [ -n "${FAKE_ON_PRINT:-}" ] && [ ! -e "$FAKE_DIR/on-print.rc" ]; then
		sh "$FAKE_ON_PRINT" > "$FAKE_DIR/on-print.out" 2>&1 && echo 0 > "$FAKE_DIR/on-print.rc" || echo $? > "$FAKE_DIR/on-print.rc"
	fi
	[ -z "${FAKE_PRINT_RC:-}" ] || exit "$FAKE_PRINT_RC"
	[ -f "$m" ] || exit 113
	[ -z "${FAKE_NO_PROGRAM:-}" ] || { printf 'gui/501/x = {\n\tstate = running\n}\n'; exit 0; }
	printf 'gui/501/x = {\n\tpath = x.plist\n\tprogram = %s\n\tstate = running\n\t\tstate = active\n}\n' "$(cat "$m")" ;;
bootout)
	[ -z "${FAKE_BOOTOUT_FAIL:-}" ] || exit 5
	[ "${FAKE_BOOTOUT_FAIL_LABEL:-}" != "$lbl" ] || exit 5
	# FAKE_ON_BOOTOUT: a script run at each bootout, between uninstall's plan
	# and its act (U-14j: a registration rewritten in that window).
	[ -z "${FAKE_ON_BOOTOUT:-}" ] || sh "$FAKE_ON_BOOTOUT"
	rm -f "$m" ;;
*) echo "fake launchctl: unexpected: $*" >&2; exit 64 ;;
esac
EOF
# claude: `mcp remove buddylist --scope local|user`, logged with its PHYSICAL
# cwd (real claude keys a local registration by it), does what claude does to
# $FAKE_CLAUDE_JSON, and exits 1 when there was nothing to remove, as claude
# does; FAKE_CLAUDE_NOOP exits 0 having removed nothing (the read-back's case),
# and FAKE_TOUCH appends a byte to that file first: claude runs between the
# settings plan and its write, so it is the edit that window can meet.
cat > "$FAKE/claude" <<'EOF'
#!/bin/sh
echo "$(pwd -P)|$*" >> "$FAKE_DIR/claude-calls"
[ "$*" = "mcp remove buddylist --scope local" ] || [ "$*" = "mcp remove buddylist --scope user" ] || { echo "fake claude: unexpected: $*" >&2; exit 64; }
[ -z "${FAKE_TOUCH:-}" ] || printf ' ' >> "$FAKE_TOUCH"
[ -z "${FAKE_CLAUDE_NOOP:-}" ] || exit 0
# FAKE_CLAUDE_HANG: a wedged claude (U-14q). It ignores TERM, and so does the
# child it leaves (claude is node, with children of its own), so only a KILL
# to its process group ends both. Bounded at 60 s, so a mutant without the
# bound fails the check's elapsed test instead of hanging it.
if [ -n "${FAKE_CLAUDE_HANG:-}" ]; then
	trap '' TERM
	sleep 60 &
	echo "$! $$" >> "$FAKE_DIR/hang-pids"
	wait
	exit 0
fi
exec python3 -I -c '
import json, sys
path, scope, cwd = sys.argv[1:]
d = json.load(open(path))
v = d if scope == "user" else d.get("projects", {}).get(cwd, {})
if "buddylist" not in v.get("mcpServers", {}):
    print("No MCP server found with name: buddylist", file=sys.stderr)
    sys.exit(1)
del v["mcpServers"]["buddylist"]
json.dump(d, open(path, "w"))
' "$FAKE_CLAUDE_JSON" "$5" "$(pwd -P)"
EOF
printf '#!/bin/sh\necho "launchctl $*" >> "%s/reached"\nexit 1\n' "$GUARD" > "$GUARD/launchctl"
printf '#!/bin/sh\necho "claude $*" >> "%s/reached"\nexit 1\n' "$GUARD" > "$GUARD/claude"
chmod +x "$FAKE/launchctl" "$FAKE/claude" "$GUARD/launchctl" "$GUARD/claude"

# The settings fixture, from gate-hook.py's own table (the one spelling,
# D-135), so it is README's lines whatever they become: every buddy line, two
# with the keys a hand paste adds (timeout, statusMessage: the developing
# machine's own, D-173), the Agent-model hook's line (never install's, D-120),
# an entry that is not buddy's, and an entry holding a buddy line beside a
# hook that is not (left whole). The cases that edit it read it back.
python3 -I -B - "$ROOT/scripts/gate-hook.py" "$WORK/settings.fixture.json" <<'PY' || fail "the settings fixture did not build"
import importlib.util, json, sys
spec = importlib.util.spec_from_file_location("gh", sys.argv[1]); gh = importlib.util.module_from_spec(spec); spec.loader.exec_module(gh)
h = {t: gh.line_for(t)[1]["hooks"][0] for t, _ in gh.TOKENS if not t.startswith("browser ")}
hello = dict(h["buddy hello"], timeout=10, statusMessage="buddy: hello")
alert = dict(h["buddylist alert"], timeout=10, statusMessage="buddylist: alert")
doc = {"env": {"BUDDY_CHAT": "on"}, "hooks": {
    "SessionStart": [{"hooks": [hello]}],
    "PreToolUse": [gh.ENTRY,
                   {"matcher": "Agent|Task", "hooks": [{"type": "command", "command": "python3 $HOME/bin/agent-model-gate.py"}]},
                   {"matcher": "Bash", "hooks": [{"type": "command", "command": "my-own-hook"}]},
                   gh.BROWSER_HOOKS["PreToolUse"][0]],
    "PostToolUse": [{"matcher": "*", "hooks": [h["buddy beat"], alert]}],
    "SessionEnd": [{"hooks": [h["buddy bye"], h["buddylist presence"]]}],
    "Stop": [{"hooks": [h["buddy idle"], {"type": "command", "command": "say done"}]}],
    "UserPromptSubmit": [{"hooks": [h["buddy busy"]]}]}}
open(sys.argv[2], "w").write(json.dumps(doc, indent=1) + "\n")
PY
# hooks_now <file>: the decoded file, one line, to compare with want_hooks.
hooks_now() { python3 -I -c 'import json,sys; print(json.dumps(json.load(open(sys.argv[1])), sort_keys=True))' "$1"; }
# want_hooks <mode>: the fixture as a run must leave it. full: every buddy
# line gone, the Agent-model and foreign entries kept, the Stop entry holding
# a foreign hook left whole; chat: buddylist's two hooks out of README's
# shared entries, the beat and bye kept where they were.
want_hooks() {
	python3 -I - "$WORK/settings.fixture.json" "$1" <<'PY'
import json, sys
d = json.load(open(sys.argv[1])); hk = d["hooks"]
if sys.argv[2] == "chat":
    hk["PostToolUse"][0]["hooks"].pop(1); hk["SessionEnd"][0]["hooks"].pop(1)
elif sys.argv[2] == "full":
    hk["SessionStart"] = []; hk["PreToolUse"] = hk["PreToolUse"][1:3]; hk["PostToolUse"] = []
    hk["SessionEnd"] = []; hk["UserPromptSubmit"] = []
print(json.dumps(d, sort_keys=True))
PY
}
# mcp_now <file>: "<scope> <project> <command>" per buddylist registration.
mcp_now() {
	python3 -I -c '
import json, sys
d = json.load(open(sys.argv[1]))
if "buddylist" in d.get("mcpServers", {}): print("user - " + d["mcpServers"]["buddylist"].get("command"))
for p, v in sorted(d.get("projects", {}).items()):
    if "buddylist" in v.get("mcpServers", {}): print("local " + p + " " + v["mcpServers"]["buddylist"].get("command"))
' "$1"
}
PIN=$(sed -n 's/^PIN=\([0-9a-f]\{40\}\)$/\1/p' scripts/get-oscar.sh)
[ -n "$PIN" ] || fail "no PIN= line in scripts/get-oscar.sh, so the oscar cases cannot be built"
OSCAR=com.buddy-system.oscar
EPIN=$(sed -n 's/^PIN=\([0-9a-f]\{40\}\)$/\1/p' scripts/get-ergo.sh)
[ "$(echo "$EPIN" | wc -l)" -eq 1 ] && [ -n "$EPIN" ] || fail "want one PIN= line in scripts/get-ergo.sh, so the ergo cases can be built"
ERGO=com.buddy-system.ergo

# rs_plant <dir>: the model router's state as router.py leaves it: the
# directory 0700 (its makedirs), a table opening with its magic, and a
# refusal log of percent-encoded paths, one per line (both 0600).
rs_plant() {
	rm -rf "$1"
	mkdir -p "$1"
	chmod 700 "$1"
	printf '#model-router-table\tmade=2026-11-12T00:00:00Z\nmain\tclaude-opus-5-5\t3\n' > "$1/table.tsv"
	printf '/x/projects/a%%20b.jsonl\n/x/projects/c.jsonl\n' > "$1/refused.txt"
	chmod 600 "$1/table.tsv" "$1/refused.txt"
}
# rs_snap: everything under ~/.local (links by their text, directories by
# their mode, files by checksum; a name holding a newline, which find's lines
# split, by its pieces: the same before and after): the router's state, and the targets U-15
# links it to, so a case that leaves it can say it left it byte for byte.
rs_snap() { (cd "$R/home/.local" 2>/dev/null && find . 2>/dev/null | LC_ALL=C sort | while IFS= read -r f; do if [ -L "$f" ]; then printf 'link %s -> %s\n' "$f" "$(readlink "$f")"; elif [ -d "$f" ]; then printf 'dir %s %s\n' "$f" "$(ls -ldn "$f" | cut -c1-10)"; else cksum "$f" 2>/dev/null || printf 'name %s\n' "$f"; fi; done); }

# setup: a fresh case at $R from the prototype, with the chat daemon's plist
# and program, the checkout's bin/ copies, and every file uninstall must leave
# byte-identical. cp to NEW paths only (never over a running Mach-O).
R=$WORK/case
setup() {
	rm -rf "$R" "$FAKE/calls" "$FAKE/claude-calls" "$FAKE/hang-pids" "$FAKE/loaded" "$FAKE/loaded."* "$FAKE/on-print.rc" "$FAKE/on-print.out"
	cp -Rp "$P" "$R"
	LA=$R/home/Library/LaunchAgents
	mkdir -p "$R/co/scripts" "$R/co/bin" "$R/daemon" "$LA" "$R/home/.buddylist" "$R/home/.codex" "$R/proj" "$R/proj3" "$R/oscar" "$R/ergo"
	# The hook lines run $HOME/bin/...: this bin dir is that one, as on a
	# machine (D-173 edits only lines that run what the run removes).
	ln -s ../bin "$R/home/bin"
	cp scripts/uninstall.sh scripts/install-lock.sh scripts/skill-dest.sh scripts/wire-hooks.py scripts/gate-hook.py scripts/get-oscar.sh scripts/get-ergo.sh "$R/co/scripts/"
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
	# A com.buddy-system.* agent nothing here can prove: left and named.
	printf '<plist><dict><key>Label</key><string>com.buddy-system.other</string></dict></plist>\n' > "$LA/com.buddy-system.other.plist"
	printf '%s' "$R/daemon/buddylist" > "$FAKE/loaded"
	# The AIM server, as get-oscar.sh builds it: bytes carrying the build path
	# and the pinned revision (nothing runs it), loaded, with its plist.
	printf 'elf\npath\tgithub.com/mk6i/open-oscar-server/cmd/server\nbuild\tvcs.revision=%s\nmore\n' "$PIN" > "$R/oscar/oscar-server"
	cat > "$LA/$OSCAR.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
  <key>Label</key><string>$OSCAR</string>
  <key>ProgramArguments</key><array><string>$R/oscar/oscar-server</string></array>
</dict></plist>
EOF
	printf '%s' "$R/oscar/oscar-server" > "$FAKE/loaded.$OSCAR"
	# The IRC server, as get-ergo.sh builds it (D-176): ergo's main package is
	# its module root, so the path line names the module itself. The plist has
	# the hand-written agent's shape: ProgramArguments, the program, its args.
	printf 'mach\npath\tgithub.com/ergochat/ergo\nmod\tgithub.com/ergochat/ergo\t(devel)\t\nbuild\tvcs.revision=%s\nbuild\tvcs.modified=false\n' "$EPIN" > "$R/ergo/ergo-server"
	cat > "$LA/$ERGO.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
  <key>Label</key><string>$ERGO</string>
  <key>ProgramArguments</key><array><string>$R/ergo/ergo-server</string><string>run</string><string>--conf</string><string>$R/home/.buddylist/ergo.yaml</string></array>
</dict></plist>
EOF
	printf '%s' "$R/ergo/ergo-server" > "$FAKE/loaded.$ERGO"
	cp "$WORK/settings.fixture.json" "$R/settings.json"
	# MCP: ours in a project (keyed by its physical path, as claude keys it)
	# and user-wide; ours under a project that is gone; another program's
	# under a real project and under /x. Only the first two go.
	PJ=$(cd "$R/proj" && pwd -P) PJ3=$(cd "$R/proj3" && pwd -P)
	cat > "$R/home/.claude.json" <<EOF
{"mcpServers": {"buddylist": {"type": "stdio", "command": "$R/bin/buddylist", "args": ["mcp"]}},
 "projects": {"/x": {"mcpServers": {"buddylist": {"command": "b"}}},
  "$PJ": {"mcpServers": {"buddylist": {"type": "stdio", "command": "$R/bin/buddylist", "args": ["mcp", "--profile", "core"]}, "other": {"command": "o"}}},
  "$R/gone-proj": {"mcpServers": {"buddylist": {"command": "$R/bin/buddylist"}}},
  "$PJ3": {"mcpServers": {"buddylist": {"command": "/usr/local/bin/not-ours"}}}}}
EOF
	printf 'journal bytes\n' > "$R/home/.buddylist/journal.db"
	printf '{"hooks": {"PreToolUse": [{"hooks": [{"command": "/abs/buddy gate"}]}]}}\n' > "$R/home/.codex/hooks.json"
	printf 'a fixture ledger, never opened\n' > "$R/co/.git/buddy.db"
	printf 'copy\n' > "$R/bin/agent-model-gate.py"
	printf 'an earlier copy\n' > "$R/skills/buddy/SKILL.md.bak"
	# The model router's state, where router.py's state_dir() puts it with
	# XDG_STATE_HOME unset, as its scans leave it: removed on a full run
	# (D-179), kept by --chat; every stop's snapshot holds it.
	RS=$R/home/.local/state/model-router
	rs_plant "$RS"
	# settings.json and .claude.json are edited now (D-173): the cases that
	# edit them read them back, and every stop's snapshot holds them.
	KEEP="$R/home/.buddylist/journal.db $R/home/.codex/hooks.json $R/co/.git/buddy.db $R/bin/agent-model-gate.py $R/skills/buddy/SKILL.md.bak $LA/com.buddy-system.other.plist"
	keep_sum=$(cksum $KEEP)
}
# snap: every file and link under the case, with its checksum.
# Go's own telemetry counters under the scratch HOME are not ours (every
# `go version -m` bumps one; measured on U-3c), so they are not in it.
# A link by its text: the install lock is a link that resolves to nothing.
snap() { (cd "$R" && find . \( -type f -o -type l \) ! -path './home/Library/Application Support/go/*' | LC_ALL=C sort | while IFS= read -r f; do if [ -L "$f" ]; then printf 'link %s -> %s\n' "$f" "$(readlink "$f")"; else cksum "$f"; fi; done); }
kept() { [ "$(cksum $KEEP)" = "$keep_sum" ] || fail "$1: a file uninstall must never touch changed:
$(cksum $KEEP)"; }
# un: uninstall.sh from the scratch checkout, everything pointed into $R.
un() {
	env HOME="$R/home" PATH="$GUARD:$PATH" GOCACHE="$gocache" GOMODCACHE="$gomodcache" \
		BUDDY_BIN_DIR="$R/bin" BUDDY_SKILL_DIR="$R/skills/buddy" BUDDY_SETTINGS="$R/settings.json" \
		BUDDY_PLIST="$R/home/Library/LaunchAgents/$AGENT.plist" BUDDY_LAUNCHCTL="$FAKE/launchctl" \
		BUDDY_CLAUDE="$FAKE/claude" FAKE_CLAUDE_JSON="$R/home/.claude.json" \
		BUDDY_CLAUDE_JSON="$R/home/.claude.json" BUDDY_MCP_JSON="$R/co/.mcp.json" FAKE_DIR="$FAKE" \
		${XENV:-} sh "${CO:-$R/co}/scripts/uninstall.sh" "$@"
}
gone() { for f in "$@"; do [ ! -e "$f" ] && [ ! -L "$f" ] || fail "$label: $f should be removed:
$out"; done; }
there() { for f in "$@"; do [ -e "$f" ] || fail "$label: $f should be left:
$out"; done; }
bins="$R/bin/buddy $R/bin/buddylist $R/co/bin/buddy $R/co/bin/buddylist $R/daemon/buddylist"
# Every file each skill ships, as installed (D-171: the fleet skill's
# reference files beside its SKILL.md), read from skills/ as the installer
# does, so a file added there is held here the day it lands; the floor is the
# control that the reference files were counted.
# A real __pycache__ directory is not a skill file: the installer skips it
# (D-178), so nothing installs it and nothing removes it.
skillmds=""
for f in skills/*/*; do
	[ "${f##*/}" = __pycache__ ] && [ -d "$f" ] && [ ! -L "$f" ] || skillmds="$skillmds $R/$f"
done
skillmds=${skillmds# }
[ "$(set -- $skillmds; echo $#)" -ge 9 ] || fail "found $(set -- $skillmds; echo $#) skill files under skills/, want the three SKILL.md and the fleet skill's six reference files"
# Every other skill's directory goes, read from skills/ (#92: the list was
# written by hand, so the model router's directory, and anything left in it,
# was held by nothing). The core's keeps the SKILL.md.bak setup plants, so it
# stays; the floor is the control that the glob read something.
skilldirs=""
for s in skills/*/SKILL.md; do
	n=${s#skills/}; n=${n%/SKILL.md}
	[ "$n" = buddy ] || skilldirs="$skilldirs $R/skills/$n"
done
skilldirs=${skilldirs# }
[ "$(set -- $skilldirs; echo $#)" -ge 2 ] || fail "found $(set -- $skilldirs; echo $#) non-core skill directories under skills/, want at least buddy-fleet and buddy-lead"
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
for want in "settings: would remove buddy hello: the entry hooks.SessionStart[0], all of it" \
	"settings: would remove buddy beat and buddylist alert: the entry hooks.PostToolUse[0], all of it" \
	"settings: left: hooks.Stop[0] holds buddy idle beside a hook that is not a buddy line" \
	"mcp: would remove buddylist registered for $PJ in $R/home/.claude.json" \
	"mcp: would remove buddylist registered user-wide in $R/home/.claude.json"; do
	echo "$out" | grep -qF "$want" || fail "U-1: the plan does not say '$want':
$out"
done
[ -z "$macos" ] || echo "$out" | grep -qF "chat server: would remove the launchd job $OSCAR" || fail "U-1: the plan does not name the oscar job:
$out"
[ -z "$macos" ] || echo "$out" | grep -qF "chat server: would remove the launchd job $ERGO (launchctl bootout; it runs $R/ergo/ergo-server, the server get-ergo.sh pins)" || fail "U-1: the plan does not name the ergo job:
$out"
[ ! -e "$FAKE/claude-calls" ] || fail "U-1: a dry run ran claude: $(cat "$FAKE/claude-calls")"
# Every line its own path: the full run's report is not a dry run's.
echo "$out" | grep -q 'still wires' && fail "U-1: a dry run whose plan ran also printed the report's grep:
$out"
echo "$out" | tail -1 | grep -qx 'uninstall: dry run: nothing was removed' || fail "U-1: no dry-run last line:
$out"
for want in "data: would remove $RS/refused.txt (the model router's refusal log)" \
	"data: would remove $RS/table.tsv (the model router's table)" \
	"data: would remove $RS, the model router's state directory, once it is empty"; do
	echo "$out" | grep -qxF "uninstall: $want" || fail "U-1: the plan does not say '$want':
$out"
done

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
echo "$out" | grep -qF "settings: would remove buddylist alert from hooks.PostToolUse[0] (its other hooks are buddy's and stay)" || fail "U-2: the plan does not name the chat line:
$out"
echo "$out" | grep -qF "settings: would remove buddy " && fail "U-2: --chat plans to remove a buddy line:
$out"
[ ! -e "$FAKE/claude-calls" ] || fail "U-2: a dry run ran claude"

# U-4: the full run, the control for every STOPPED case below.
label=U-4
setup
[ -f "$R/skills/buddy-fleet/.install-skill.files" ] || fail "U-4: the install wrote no manifest beside the fleet skill, so its removal below is vacuous"
out=$(un 2>&1) || fail "U-4: uninstall failed:
$out"
gone $bins $skillmds $skilldirs
grep -qx "bootout gui/$(id -u)/$AGENT" "$FAKE/calls" || fail "U-4: no bootout:
$(cat "$FAKE/calls")"
[ ! -e "$FAKE/loaded" ] || fail "U-4: the job is still loaded"
if [ -n "$macos" ]; then
	gone "$(plist)"
	ls "$R/home/.buddylist/$AGENT.plist.removed-"* >/dev/null 2>&1 || fail "U-4: the plist was not kept in ~/.buddylist:
$out"
fi
there "$R/skills/buddy/SKILL.md.bak" "$R/co/.git/buddy.db" "$R/home/.local/state"
# The model router's state (D-179): its two files, then its directory; the
# XDG directory above it is not the router's and stays.
gone "$RS/table.tsv" "$RS/refused.txt" "$RS"
# The core's directory stays for its .bak, so its manifest is held by name
# (Codex code pass: skilldirs excludes the core).
gone "$R/skills/buddy/.install-skill.files"
kept U-4
for want in "removed: $RS/refused.txt" "removed: $RS/table.tsv" "removed: $RS (the model router's state directory)"; do
	echo "$out" | grep -qxF "uninstall: $want" || fail "U-4: no line '$want':
$out"
done
# The settings: every buddy line gone, and only those (want_hooks says what
# stays: the Agent-model hook's, a foreign entry, the Stop entry holding a
# foreign hook beside buddy's); the old bytes kept beside it, 0600.
[ "$(hooks_now "$R/settings.json")" = "$(want_hooks full)" ] || fail "U-4: the settings are not the fixture minus buddy's lines:
$(hooks_now "$R/settings.json")
$out"
bk=$(ls "$R/settings.json.buddy-backup-"* 2>/dev/null | head -n 1)
[ -n "$bk" ] && cmp -s "$bk" "$WORK/settings.fixture.json" || fail "U-4: no backup holding the old settings:
$out"
[ "$(ls -l "$bk" | cut -c1-10)" = "-rw-------" ] || fail "U-4: the backup is not 0600: $(ls -l "$bk")"
# MCP: ours removed, by claude, in its project and user-wide; the rest kept.
[ "$(mcp_now "$R/home/.claude.json")" = "$(printf 'local %s %s\nlocal %s %s\nlocal %s %s' /x b "$PJ3" /usr/local/bin/not-ours "$R/gone-proj" "$R/bin/buddylist" | LC_ALL=C sort -k2,2)" ] ||
	fail "U-4: the MCP registrations left are not the foreign ones and the gone project's:
$(mcp_now "$R/home/.claude.json")
$out"
[ "$(cat "$FAKE/claude-calls")" = "$(cd "$R/home" && pwd -P)|mcp remove buddylist --scope user
$PJ|mcp remove buddylist --scope local" ] || fail "U-4: claude was not run exactly twice, in the project and for user scope:
$(cat "$FAKE/claude-calls")"
if [ -n "$macos" ]; then
	grep -qx "bootout gui/$(id -u)/$OSCAR" "$FAKE/calls" || fail "U-4: the oscar job was not booted out:
$(cat "$FAKE/calls")"
	gone "$R/home/Library/LaunchAgents/$OSCAR.plist"
	ls "$R/home/.buddylist/$OSCAR.plist.removed-"* >/dev/null 2>&1 || fail "U-4: the oscar plist was not kept in ~/.buddylist:
$out"
else
	echo "$out" | grep -qF "chat server: left: $OSCAR" || fail "U-4: an oscar plist that cannot be read here is not named left:
$out"
fi
there "$R/oscar/oscar-server"
# ergo, proven by get-ergo.sh's pin (D-176): booted out, its plist kept.
if [ -n "$macos" ]; then
	grep -qx "bootout gui/$(id -u)/$ERGO" "$FAKE/calls" || fail "U-4: the ergo job was not booted out:
$(cat "$FAKE/calls")"
	[ ! -e "$FAKE/loaded.$ERGO" ] || fail "U-4: the ergo job is still loaded"
	gone "$LA/$ERGO.plist"
	echo "$out" | grep -qF "removed: $LA/$ERGO.plist (kept at $R/home/.buddylist/$ERGO.plist.removed-" || fail "U-4: the ergo plist's restore line is missing:
$out"
	ls "$R/home/.buddylist/$ERGO.plist.removed-"* >/dev/null 2>&1 || fail "U-4: the ergo plist was not kept in ~/.buddylist:
$out"
fi
there "$R/ergo/ergo-server"
for want in "settings: $R/settings.json still wires: buddy idle" \
	"settings: removed buddy gate: the entry hooks.PreToolUse[0], all of it in $R/settings.json" \
	"settings: left: hooks.Stop[0] holds buddy idle beside a hook that is not a buddy line" \
	"removed: the buddylist MCP registration for $PJ (cd $PJ && claude mcp remove buddylist --scope local)" \
	"removed: the user-wide buddylist MCP registration (claude mcp remove buddylist --scope user)" \
	"mcp: left: buddylist is registered for /x in $R/home/.claude.json with the command \"b\", which is not a buddylist this run removes; if it is yours to remove: cd /x && claude mcp remove buddylist --scope local" \
	"mcp: left: buddylist is registered for $PJ3 in $R/home/.claude.json with the command \"/usr/local/bin/not-ours\"" \
	"mcp: left: buddylist is registered for $R/gone-proj in $R/home/.claude.json, but that is not a directory's physical path here" \
	"codex: $R/home/.codex/hooks.json names buddy" \
	"left: core.hooksPath = .githooks in $R/co" \
	"left: $R/bin/agent-model-gate.py" \
	"data: left: this checkout's ledger" \
	"left: $R/home/Library/LaunchAgents/com.buddy-system.other.plist, a launchd agent this cannot prove is buddy's"; do
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
[ "$(wc -l < "$FAKE/claude-calls")" -eq 2 ] || fail "U-5: the re-run ran claude again:
$(cat "$FAKE/claude-calls")"
[ "$(hooks_now "$R/settings.json")" = "$(want_hooks full)" ] || fail "U-5: the re-run changed the settings"
echo "$out" | tail -1 | grep -qx 'uninstall: done' || fail "U-5: no done line:
$out"

# U-4r: the model router's state is found where router.py's state_dir() puts
# it (D-178, D-179): under an ABSOLUTE XDG_STATE_HOME, and under
# ~/.local/state when XDG_STATE_HOME is relative (router.py ignores it then).
# The dry run plans it and changes nothing; the real runs remove the one
# found and leave the other byte for byte. A path holding a newline is left,
# unprinted; with none there, no line (U-1/U-4 the control that one is named).
label=U-4r
setup; rs_plant "$R/xdg/model-router"; before=$(snap)
out=$(XENV="XDG_STATE_HOME=$R/xdg" un --dry-run 2>&1) || fail "U-4r: dry run failed:
$out"
[ "$(snap)" = "$before" ] || fail "U-4r: a dry run changed the tree"
echo "$out" | grep -qxF "uninstall: data: would remove $R/xdg/model-router/table.tsv (the model router's table)" || fail "U-4r: an absolute XDG_STATE_HOME's state is not planned:
$out"
echo "$out" | grep -qF "$RS" && fail "U-4r: under an absolute XDG_STATE_HOME the home path was named:
$out"
rb=$(rs_snap)
out=$(XENV="XDG_STATE_HOME=$R/xdg" un 2>&1) || fail "U-4r: the run under an absolute XDG_STATE_HOME failed:
$out"
gone "$R/xdg/model-router"
there "$R/xdg"
[ "$(rs_snap)" = "$rb" ] || fail "U-4r: under an absolute XDG_STATE_HOME the home state changed:
$out"
# A newline in the path is never printed: it would forge a report line
# (D-178's Codex code pass), and it is never judged or removed. Exported in a
# subshell: XENV splits on whitespace.
NLX="$R/nl
uninstall: done"
rs_plant "$NLX/model-router"
nlsum=$(cksum "$NLX/model-router/table.tsv" "$NLX/model-router/refused.txt")
nlline="uninstall: data: left: the model router's state directory (its path holds a newline, so it is not printed here, and it is left)"
out=$(export XDG_STATE_HOME="$NLX"; un --dry-run 2>&1) || fail "U-4r: dry run failed:
$out"
echo "$out" | grep -qxF "$nlline" || fail "U-4r: a state path with a newline is not named without it:
$out"
[ "$(echo "$out" | grep -cx 'uninstall: done')" -eq 0 ] || fail "U-4r: a newline in XDG_STATE_HOME forged a report line:
$out"
out=$(export XDG_STATE_HOME="$NLX"; un 2>&1) || fail "U-4r: the run under a newline XDG_STATE_HOME failed:
$out"
echo "$out" | grep -qxF "$nlline" || fail "U-4r: the real run does not name the newline path left:
$out"
[ "$(cksum "$NLX/model-router/table.tsv" "$NLX/model-router/refused.txt")" = "$nlsum" ] || fail "U-4r: the state on a newline path was touched:
$out"
[ "$(echo "$out" | grep -cx 'uninstall: done')" -eq 1 ] || fail "U-4r: a newline in XDG_STATE_HOME forged a report line:
$out"
out=$(XENV="XDG_STATE_HOME=rel/state" un 2>&1) || fail "U-4r: the run under a relative XDG_STATE_HOME failed:
$out"
echo "$out" | grep -qxF "uninstall: removed: $RS (the model router's state directory)" || fail "U-4r: a relative XDG_STATE_HOME must fall back to ~/.local/state:
$out"
gone "$RS"
out=$(un --dry-run 2>&1) || fail "U-4r: dry run failed:
$out"
echo "$out" | grep -qF "model router" && fail "U-4r: no state directory, yet one was named:
$out"

# The judge's literals are router.py's (Fable): were router.py to change its
# table's magic, a state file's name, its temps or its markers, every real
# state would be left with nothing here red (the safe way, but the feature
# gone). The markers and the magic are compared line for line with RS_PY's
# copies; the names and temps are pinned as router.py spells them (RS_PY's
# side is held by U-1, U-4 and U-15h).
label=U-15-pin
for v in VCS_MARKERS:VCS TABLE_MAGIC:MAGIC; do
	l=$(grep "^${v%%:*} = " skills/model-router/router.py | sed "s/^${v%%:*} /${v#*:} /; s/'/\"/g")
	[ -n "$l" ] && grep -qxF -- "$l" scripts/uninstall.sh || fail "U-15-pin: router.py's ${v%%:*} and uninstall.sh's RS_PY ${v#*:} differ (want the line '$l'): change both"
done
for pin in "mkstemp(prefix='.model-router.', suffix='.probe', dir=d)" "mkstemp(prefix='.model-router.', suffix='.tmp', dir=d)" \
	"os.path.join(sd, 'table.tsv')" "os.path.join(sd, 'refused.txt')"; do
	grep -qF -- "$pin" skills/model-router/router.py || fail "U-15-pin: router.py no longer says $pin, which uninstall.sh's RS_PY copies (OWN, TMP): change both"
done
# U-15: the model router's state LEFT whole (D-179). Each case is a real run
# on an uninstalled fixture (nothing else is left to remove) that leaves the
# state byte for byte, says why, and still finishes; then its positive
# control: the deviation undone, the same run removes it, so the guard is
# what left it. U-15o and U-15p meet the window between plan and act.
label=U-15
setup
out=$(un 2>&1) || fail "U-15: the first run failed:
$out"
gone "$RS"
H="; remove it by hand if you like: rm -rf $RS"
# rs_left <case> <the line's tail after "(the model router's state)">
rs_left() {
	label=$1
	rb=$(rs_snap)
	out=$(un 2>&1) || fail "$label: a run that leaves the model router's state must not fail:
$out"
	[ "$(rs_snap)" = "$rb" ] || fail "$label: the model router's state changed:
$rb
--- now:
$(rs_snap)
$out"
	echo "$out" | grep -qxF "uninstall: data: left: $RS (the model router's state)$2" || fail "$label: the state is not named left with '$2':
$out"
	echo "$out" | grep -qF "removed: $RS" && fail "$label: part of the state was said removed:
$out"
	echo "$out" | tail -1 | grep -qx 'uninstall: done' || fail "$label: the run must finish (the state is never a stop):
$out"
}
rs_goes() {
	out=$(un 2>&1) || fail "$label: the control run failed:
$out"
	gone "$RS"
	there "$R/home/.local/state"
	echo "$out" | grep -qxF "uninstall: removed: $RS (the model router's state directory)" || fail "$label: the control left the state, so the case above proves nothing:
$out"
}
# A dry run with the skill already gone (so only the dry-run gate holds it):
# planned, and nothing changes (the panel: every other dry run had the skill).
label=U-15-dry
rs_plant "$RS"; rb=$(rs_snap)
out=$(un --dry-run 2>&1) || fail "U-15-dry: dry run failed:
$out"
[ "$(rs_snap)" = "$rb" ] || fail "U-15-dry: a dry run changed the state:
$out"
echo "$out" | grep -qxF "uninstall: data: would remove $RS/table.tsv (the model router's table)" || fail "U-15-dry: the dry run did not plan the state:
$out"
# A foreign name is folded to one printable line: a newline, a non-ASCII
# letter and a backslash each print as ? (invariant 9; echo would have
# expanded the backslash, the panel).
fn=$(printf 'a\nb\303\251\\c')
printf 'x\n' > "$RS/$fn"
rs_left U-15-fold ", since it holds a?b??c, a file the router never writes$H"
rm "$RS/$fn"; rs_goes
# U-15a: the directory is a symlink (to a directory holding the router's own
# files): the link and its target are left.
rs_plant "$R/home/.local/target"; ln -s ../target "$RS"
rs_left U-15a ", since it is a symlink$H"
rm "$RS"; mv "$R/home/.local/target" "$RS"; rs_goes
# U-15b: not a directory.
printf 'x\n' > "$RS"
rs_left U-15b ", since it is not a directory$H"
rm "$RS"; rs_plant "$RS"; rs_goes
# U-15c: another user owns it. An EMPTY directory (Codex design pass: a
# populated one also fails each file's owner check, so a mutant dropping this
# one would still be left), owned by another uid as `id -u` says: a fake id
# first on PATH. The control is the same empty directory, the real id.
mkdir "$RS"; chmod 700 "$RS"
printf '#!/bin/sh\n[ "$*" != -u ] || { echo %s; exit 0; }\nexec %s "$@"\n' "$(($(id -u) + 1))" "$(command -v id)" > "$GUARD/id"
chmod +x "$GUARD/id"
rs_left U-15c ", since another user owns it$H"
rm "$GUARD/id"; rs_goes
# U-15d: a file the router never writes, beside its own.
rs_plant "$RS"; printf 'mine\n' > "$RS/notes.txt"
rs_left U-15d ", since it holds notes.txt, a file the router never writes$H"
rm "$RS/notes.txt"; rs_goes
# U-15e: a subdirectory.
rs_plant "$RS"; mkdir "$RS/sub"
rs_left U-15e ", since it holds a subdirectory, sub$H"
rmdir "$RS/sub"; rs_goes
# U-15f: a table.tsv that does not open with the magic (a space for its tab).
rs_plant "$RS"; printf '#model-router-table x\n' > "$RS/table.tsv"
rs_left U-15f ", since its table.tsv is not one the router writes (a table starts with its header)$H"
rs_plant "$RS"; rs_goes
# U-15g: a refused.txt in no shape router.py writes: a space, a byte past
# 0x7e, an empty line (first or later), no final newline, empty.
for v in space high empty-line leading no-newline empty; do
	rs_plant "$RS"
	case $v in
	space) printf '/a b\n' ;;
	high) printf '/a\351\n' ;;
	empty-line) printf '/a\n\n/b\n' ;;
	leading) printf '\n/a\n' ;;
	no-newline) printf '/a' ;;
	empty) : ;;
	esac > "$RS/refused.txt"
	rs_left "U-15g ($v)" ", since its refused.txt is not one the router writes (one percent-encoded path per line)$H"
done
rs_plant "$RS"; rs_goes
# U-15h: a scan's mkstemp temp, either kind: router.py takes no lock, so one
# present may be a scan writing now (D-179's lock answer).
for t in .model-router.abcd_123.tmp .model-router.k9x0q2zz.probe; do
	rs_plant "$RS"; : > "$RS/$t"
	rs_left "U-15h ($t)" ", since it holds $t, the temporary file of a scan (one may be writing now, or a killed one left it)$H"
	rm "$RS/$t"
done
rs_goes
# U-15i: a symlink named table.tsv, to a table: not a file router.py writes.
rs_plant "$RS"; mv "$RS/table.tsv" "$R/home/.local/table-target"; ln -s "$R/home/.local/table-target" "$RS/table.tsv"
[ -f "$RS/table.tsv" ] || fail "U-15i: the link dangles, so it is not a link to a table"
rs_left U-15i ", since it holds table.tsv, which is not a regular file$H"
rm "$RS/table.tsv" "$R/home/.local/table-target"; rs_plant "$RS"; rs_goes
# U-15j: group or others can write to it, either bit.
for m in 770 707; do
	rs_plant "$RS"; chmod "$m" "$RS"
	rs_left "U-15j ($m)" ", since its group or others can write to it$H"
done
chmod 700 "$RS"; rs_goes
# U-15k: a file another user owns, hard-linked in under a name the router
# writes (its bytes are not a table, so the line says which check spoke).
rs_plant "$RS"; rm "$RS/table.tsv"
if [ ! -O /etc/hosts ] && ln /etc/hosts "$RS/table.tsv" 2>/dev/null; then
	rs_left U-15k ", since it holds table.tsv, which another user owns$H"
	rm "$RS/table.tsv"
else
	echo "check-uninstall: U-15k NOT RUN — no file another user owns could be linked in here (Linux protected_hardlinks, or run as root)" >&2
fi
rs_plant "$RS"; rs_goes
# U-15l: a directory this user cannot read.
rs_plant "$RS"; chmod 000 "$RS"
if [ -r "$RS" ]; then
	echo "check-uninstall: U-15l NOT RUN — a mode-000 directory is readable here (root)" >&2
else
	rs_left U-15l ", since it could not be read (Permission denied)$H"
fi
chmod 700 "$RS"
[ -f "$RS/table.tsv" ] && [ -f "$RS/refused.txt" ] || fail "U-15l: the files under the mode-000 directory are gone"
rs_goes
# U-15m: the skill stays (an edited SKILL.md the run leaves), so its data
# does; and a skill directory this user cannot search is not "gone" (Codex
# design pass: `[ -e ]` fails there and read as absent).
rs_plant "$RS"; mkdir -p "$R/skills/model-router"; printf 'an edit\n' > "$R/skills/model-router/SKILL.md"
rs_left U-15m ", since its skill stays ($R/skills/model-router/SKILL.md)"
chmod 000 "$R/skills/model-router"
if [ -x "$R/skills/model-router" ]; then
	echo "check-uninstall: U-15m (unsearchable) NOT RUN — a mode-000 directory is searchable here (root)" >&2
else
	rs_left "U-15m (unsearchable)" ", since its skill stays ($R/skills/model-router/SKILL.md)"
	echo "$out" | grep -qxF "uninstall: skill: left: $R/skills/model-router (whole), since it cannot be searched, so what it holds cannot be judged" || fail "U-15m: the unsearchable skill directory is not named:
$out"
fi
chmod 700 "$R/skills/model-router"; rm -rf "$R/skills/model-router"; rs_goes
# U-15r: in a version-control work tree, where router.py never writes (its
# check_target): a marker at an ancestor (the panel).
rs_plant "$RS"; mkdir "$R/home/.local/.git"
rs_left U-15r ", since it is in a version-control work tree, where router.py never writes$H"
rmdir "$R/home/.local/.git"; rs_goes
# Reached through a link into a work tree: judged at its resolved path, as
# router.py's in_work_tree() judges it; walked as spelled, no marker is met.
rs_plant "$RS"; mkdir -p "$R/home/.local/vcs/.git"; mv "$R/home/.local/state" "$R/home/.local/vcs/state"; ln -s vcs/state "$R/home/.local/state"
rs_left "U-15r (through a link)" ", since it is in a version-control work tree, where router.py never writes$H"
rmdir "$R/home/.local/vcs/.git"; rs_goes
rm "$R/home/.local/state"; mv "$R/home/.local/vcs/state" "$R/home/.local/state"; rmdir "$R/home/.local/vcs"
# U-15s: a relative HOME, so a relative path (it would be read from the
# checkout, uninstall's cwd: the panel). A dry run: its plan is the verdict
# the act obeys. Planted where the relative path lands.
label=U-15s
rs_plant "$R/co/relhome/.local/state/model-router"
out=$(XENV="HOME=relhome" un --dry-run 2>&1) || fail "U-15s: dry run failed:
$out"
echo "$out" | grep -qxF "uninstall: data: left: relhome/.local/state/model-router (the model router's state), since its path is not absolute (HOME is relative); remove it by hand if you like: rm -rf relhome/.local/state/model-router" || fail "U-15s: a relative path is not refused:
$out"
rm -rf "$R/co/relhome"
# U-15w: a backslash in its path prints as itself: say() is printf, where
# echo in dash and macOS sh turns `\n` into a line of its own (the panel).
# The check reads the output with printf for the same reason.
label=U-15w
XS="$R/home/.local/b\\nx"
rs_plant "$XS/model-router"
out=$(XENV="XDG_STATE_HOME=$XS" un --dry-run 2>&1) || fail "U-15w: dry run failed:
$out"
printf '%s\n' "$out" | grep -qxF "uninstall: data: would remove $XS/model-router/table.tsv (the model router's table)" || fail "U-15w: a backslash in the state's path did not print as itself:
$out"
rm -rf "$XS"
# U-15t: its parent lets others rename entries (group or others can write,
# not sticky; either bit alone, Fable): the final rmdir could meet a
# directory swapped in (Codex code pass). The control is the same parent
# sticky (1777), which keeps others from renaming ours.
for m in 777 775 757; do
	rs_plant "$RS"; chmod "$m" "$R/home/.local/state"
	rs_left "U-15t ($m)" ", since others can rename entries in its parent directory$H"
done
chmod 1777 "$R/home/.local/state"; rs_goes
chmod 755 "$R/home/.local/state"
# U-15u: its parent cannot be written, so it could not be removed: left
# whole, not emptied first (Codex code pass).
rs_plant "$RS"; chmod 555 "$R/home/.local/state"
if [ -w "$R/home/.local/state" ]; then
	echo "check-uninstall: U-15u NOT RUN — a mode-555 directory is writable here (root)" >&2
else
	rs_left U-15u ", since its parent directory cannot be written, so it could not be removed$H"
fi
chmod 755 "$R/home/.local/state"; rs_goes
# U-15v: its parent can be written and searched but not read: the act's
# rmdir goes through a descriptor of the parent, which then cannot be
# opened, so the plan opens one too and the state is left whole (the panel:
# it failed only at the act).
rs_plant "$RS"; chmod 311 "$R/home/.local/state"
if [ -r "$R/home/.local/state" ]; then
	echo "check-uninstall: U-15v NOT RUN — a mode-311 directory is readable here (root)" >&2
else
	rs_left U-15v ", since its parent directory could not be read (Permission denied)$H"
fi
chmod 755 "$R/home/.local/state"
[ -f "$RS/table.tsv" ] && [ -f "$RS/refused.txt" ] || fail "U-15v: the files under the unreadable parent are gone"
rs_goes
# U-15x: a file that cannot be unlinked (an immutable flag) met during the
# removal itself: what went is said, the rest is named left, the run goes on
# (Fable: the one part-emptied path no race is needed for). refused.txt goes
# first (the act removes by sorted name). chflags is macOS's; Linux's chattr
# needs root.
label=U-15x
rs_plant "$RS"
if command -v chflags >/dev/null 2>&1 && chflags uchg "$RS/table.tsv" 2>/dev/null; then
	out=$(un 2>&1) && rc=0 || rc=$?
	chflags nouchg "$RS/table.tsv"
	[ "$rc" -eq 0 ] || fail "U-15x: a run that leaves part of the state must not fail:
$out"
	echo "$out" | grep -qxF "uninstall: removed: $RS/refused.txt" || fail "U-15x: the refusal log was not removed:
$out"
	echo "$out" | grep -qxF "uninstall: data: left: $RS with what is still in it, since it could not be read or emptied (Operation not permitted)$H" || fail "U-15x: the part-emptied state is not named left with what is in it:
$out"
	[ -f "$RS/table.tsv" ] && [ ! -e "$RS/refused.txt" ] || fail "U-15x: not exactly the immutable table is left:
$out"
	echo "$out" | tail -1 | grep -qx 'uninstall: done' || fail "U-15x: the run must finish:
$out"
else
	echo "check-uninstall: U-15x NOT RUN — no chflags uchg here (Linux)" >&2
fi
rs_plant "$RS"; rs_goes
# U-15y: a file where ~/.local/state should be: the state cannot exist, so
# no line at all (Fable: it was named "could not be read (Not a directory)",
# with an rm -rf of a path that is not there).
label=U-15y
rmdir "$R/home/.local/state"; printf 'x\n' > "$R/home/.local/state"
out=$(un 2>&1) || fail "U-15y: uninstall failed:
$out"
echo "$out" | grep -qF "model router" && fail "U-15y: a state that cannot exist was named:
$out"
rm "$R/home/.local/state"; mkdir "$R/home/.local/state"; rs_plant "$RS"; rs_goes
# U-15n: no Python to judge it with.
rs_plant "$RS"; XENV="BUDDY_PYTHON=$WORK/no-python"
rs_left U-15n ", since there is no $WORK/no-python here to judge it with$H"
XENV=; rs_goes
# U-15o: a router file the plan did not see, written between plan and act
# (the bootout runs between them; the marker proves it ran): nothing goes.
label=U-15o
setup; rm "$RS/refused.txt"
printf '[ -e %s ] && exit 0\nprintf "/x/late.jsonl\\n" > %s\n: > %s\n' "$WORK/late.done" "$RS/refused.txt" "$WORK/late.done" > "$WORK/late.sh"
rm -f "$WORK/late.done"
tsum=$(cksum < "$RS/table.tsv")
XENV="FAKE_ON_BOOTOUT=$WORK/late.sh"
out=$(un 2>&1) || fail "U-15o: uninstall failed:
$out"
XENV=
[ -e "$WORK/late.done" ] || fail "U-15o: nothing was written between plan and act, so this case tests nothing:
$out"
echo "$out" | grep -qxF "uninstall: data: will remove $RS/table.tsv (the model router's table)" || fail "U-15o: the plan did not see the table:
$out"
echo "$out" | grep -qxF "uninstall: data: left: $RS (the model router's state) as it was, since it changed since the plan read it (it now holds refused.txt)$H" || fail "U-15o: the late file did not leave the state:
$out"
[ "$(cksum < "$RS/table.tsv")" = "$tsum" ] && [ -f "$RS/refused.txt" ] || fail "U-15o: the state was touched:
$out"
rs_goes
# U-15q: the skill back between plan and act (the plan found it absent):
# the act's own gate leaves the state (Codex code pass: only the plan's was
# tested).
label=U-15q
setup; rm -rf "$R/skills/model-router"
printf '[ -e %s ] && exit 0\nmkdir -p %s && printf "an edit\\n" > %s/SKILL.md\n: > %s\n' "$WORK/back.done" "$R/skills/model-router" "$R/skills/model-router" "$WORK/back.done" > "$WORK/back.sh"
rm -f "$WORK/back.done"
rb=$(rs_snap)
XENV="FAKE_ON_BOOTOUT=$WORK/back.sh"
out=$(un 2>&1) || fail "U-15q: uninstall failed:
$out"
XENV=
[ -e "$WORK/back.done" ] || fail "U-15q: nothing put the skill back between plan and act, so this case tests nothing:
$out"
echo "$out" | grep -qxF "uninstall: data: will remove $RS, the model router's state directory, once it is empty" || fail "U-15q: the plan did not plan the state:
$out"
echo "$out" | grep -qxF "uninstall: data: left: $RS (the model router's state), since its skill is still there ($R/skills/model-router/SKILL.md)" || fail "U-15q: the act's skill gate did not leave the state:
$out"
[ "$(rs_snap)" = "$rb" ] || fail "U-15q: the state was touched:
$out"
rm -rf "$R/skills/model-router"; rs_goes
# U-15p: a file the plan named, gone by the act (a clean scan removes its
# refusal log): the rest goes, and the directory.
label=U-15p
setup
printf 'rm -f %s\n' "$RS/refused.txt" > "$WORK/vanish.sh"
XENV="FAKE_ON_BOOTOUT=$WORK/vanish.sh"
out=$(un 2>&1) || fail "U-15p: uninstall failed:
$out"
XENV=
echo "$out" | grep -qxF "uninstall: data: will remove $RS/refused.txt (the model router's refusal log)" || fail "U-15p: the plan did not see the refusal log:
$out"
gone "$RS"
echo "$out" | grep -qxF "uninstall: removed: $RS/table.tsv" || fail "U-15p: the table was not removed:
$out"
echo "$out" | grep -qxF "uninstall: removed: $RS/refused.txt" && fail "U-15p: the refusal log was there at the act, so nothing removed it between plan and act:
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
# The gate line still runs what is at $R/bin/buddy: buddy's lines stay, the
# chat lines (their buddylist is gone) do not.
[ "$(hooks_now "$R/settings.json")" = "$(want_hooks chat)" ] || fail "U-9: with buddy left, only the chat lines may go:
$(hooks_now "$R/settings.json")
$out"
echo "$out" | grep -qF "settings: left: buddy's hook lines, since $R/bin/buddy stays (above): the gate line still runs it" || fail "U-9: the buddy lines left are not named:
$out"

# U-9r: an edited REFERENCE file is left and named, and so is its directory;
# the fleet skill's other files and its manifest go. U-4 is the control: the
# same files unedited all go, directory too.
label=U-9r
setup
printf 'my edit\n' >> "$R/skills/buddy-fleet/restack.md"
out=$(un 2>&1) || fail "U-9r: uninstall failed:
$out"
there "$R/skills/buddy-fleet/restack.md"
gone "$R/skills/buddy-fleet/SKILL.md" "$R/skills/buddy-fleet/handoff.md" "$R/skills/buddy-fleet/.install-skill.files"
echo "$out" | grep -qF "skill: left: $R/skills/buddy-fleet/restack.md differs" || fail "U-9r: the edited reference file is not named:
$out"
echo "$out" | grep -qxF "uninstall: skill: left: $R/skills/buddy-fleet, which still holds files this checkout did not install or that differ" || fail "U-9r: the directory left is not named:
$out"

# U-16: a skill directory whose files are not an installed copy is LEFT
# whole and named, and nothing in what it reaches changes (D-181, #94: a
# link to the checkout's own skills/<n> had its tracked files deleted through
# it). Each case runs the dry run (the same leave planned, nothing changed)
# and then the full run, and names the clause of skill-dest.sh's skd_judge it
# holds alone; U-4 is the control that the same directories, unlinked, go,
# and most cases check an ordinary skill beside them still goes.
# tsum <dir>: every entry under it, links by their text, files by checksum.
tsum() { (cd "$1" && find . | LC_ALL=C sort | while IFS= read -r f; do if [ -L "$f" ]; then printf 'link %s -> %s\n' "$f" "$(readlink "$f")"; elif [ -f "$f" ]; then printf '%s %s\n' "$(cksum < "$f")" "$f"; else printf 'dir %s\n' "$f"; fi; done); }
# u16 <reached dir> <left line>...: both runs, each left line in both, and
# the reached tree unchanged by either. The premise first: the reached
# directory holds a SKILL.md byte-identical to one the checkout ships, so a
# judge that let it through would plan its removal (the panel: an empty
# directory passed the old guard).
u16() {
	u16_reach=$1; shift
	u16_b4=$(tsum "$u16_reach")
	u16_eq=""
	for s in "$R/co/skills"/*/SKILL.md; do cmp -s "$s" "$u16_reach/SKILL.md" 2>/dev/null && u16_eq=1; done
	[ -n "$u16_eq" ] || fail "$label: $u16_reach holds no SKILL.md identical to the checkout's, so this case tests nothing"
	before=$(snap)
	out=$(un --dry-run 2>&1) || fail "$label: the dry run failed:
$out"
	[ "$(snap)" = "$before" ] || fail "$label: the dry run changed the tree:
$out"
	for want in "$@"; do
		echo "$out" | grep -qxF "uninstall: skill: left: $want" || fail "$label: the dry run does not plan the leave '$want':
$out"
	done
	out=$(un 2>&1) || fail "$label: uninstall failed:
$out"
	for want in "$@"; do
		echo "$out" | grep -qxF "uninstall: skill: left: $want" || fail "$label: the run does not name the leave '$want':
$out"
	done
	[ "$(tsum "$u16_reach")" = "$u16_b4" ] || fail "$label: uninstall changed what $u16_reach holds:
$(tsum "$u16_reach")
$out"
}
own_line() { printf '%s (whole), since it is this checkout'"'"'s own skills/%s (however it is reached), so its files are the checkout'"'"'s, not an installed copy' "$1" "$2"; }
ownlink_line() { printf '%s; to remove the link alone: rm %s' "$(own_line "$1" "$2")" "$1"; }
link_line() { printf '%s (whole), since it is a symlink, so its files are not an installed copy; to remove the link alone: rm %s' "$1" "$1"; }
vcs_line() { printf '%s (whole), since it is in a version-control work tree (%s), which may track its files; if they are installed copies you want gone, remove them by hand: rm -r %s' "$1" "$2" "$1"; }
checkout_line() { printf '%s (whole), since it is inside this checkout, so its files are the checkout'"'"'s, not an installed copy' "$1"; }
clone_line() { printf '%s (whole), since it is another checkout'"'"'s skills/%s (%s), so its files are that checkout'"'"'s, not an installed copy' "$1" "$2" "$3"; }

# U-16a: the issue's case. ~/.claude/skills/buddy-fleet a symlink to the
# checkout's own skills/buddy-fleet (a live-edit setup). Clause: the
# directory judged at all (a mutant back to the last-component-only test
# deletes the checkout's files here). The other skills still go: the
# positive control that the run reached the skills.
label=U-16a
setup
rm -rf "$R/skills/buddy-fleet"; ln -s "$R/co/skills/buddy-fleet" "$R/skills/buddy-fleet"
u16 "$R/co/skills/buddy-fleet" "$(ownlink_line "$R/skills/buddy-fleet" buddy-fleet)"
[ -L "$R/skills/buddy-fleet" ] || fail "U-16a: the link itself was removed:
$out"
gone "$R/skills/buddy-lead/SKILL.md" "$R/skills/buddy/SKILL.md" "$R/skills/buddy-lead"
echo "$out" | grep -qF "skill: will remove $R/skills/buddy-fleet/" && fail "U-16a: a file through the link was planned:
$out"

# U-16b: the skills root itself, ~/.claude/skills, a link to the checkout's
# skills/: every destination is a real directory there, so no `-L` sees it;
# -ef does (and the checkout's .git). Every skill is left, and with it the
# model router's state (its skill stays).
label=U-16b
setup
mv "$R/skills" "$R/skills.installed"; ln -s co/skills "$R/skills"
u16 "$R/co/skills/buddy" "$(own_line "$R/skills/buddy" buddy)" "$(own_line "$R/skills/buddy-fleet" buddy-fleet)" "$(own_line "$R/skills/model-router" model-router)"
there "$RS/table.tsv"

# U-16c: the same through a checkout with NO version-control marker (an
# export): -ef names it `own`. A mutant without the -ef clause still leaves
# the files (the checkout clause reads them `checkout`), so it dies here on
# the exact line, not on a deletion (Codex code pass).
label=U-16c
setup
mv "$R/co/.git" "$R/co.git-aside"
mv "$R/skills" "$R/skills.installed"; ln -s co/skills "$R/skills"
u16 "$R/co/skills/buddy-fleet" "$(own_line "$R/skills/buddy-fleet" buddy-fleet)"

# U-16d: a skill directory linked to a plain directory outside any work tree
# (not the checkout's, not tracked): the link alone holds it.
label=U-16d
setup
mkdir "$R/plain"; mv "$R/skills/buddy-fleet" "$R/plain/buddy-fleet"; ln -s ../plain/buddy-fleet "$R/skills/buddy-fleet"
u16 "$R/plain/buddy-fleet" "$(link_line "$R/skills/buddy-fleet")"
gone "$R/skills/buddy-lead/SKILL.md"

# U-16e: a trailing slash makes `[ -L ]` look past that link (Codex design
# pass): dropped first, so the link line names the link.
label=U-16e
setup
mkdir "$R/plain"; mv "$R/skills/buddy-fleet" "$R/plain/buddy-fleet"; ln -s ../plain/buddy-fleet "$R/skills/buddy-fleet"
XENV="BUDDY_FLEET_SKILL_DIR=$R/skills/buddy-fleet/"
u16 "$R/plain/buddy-fleet" "$(link_line "$R/skills/buddy-fleet")"
XENV=
gone "$R/skills/buddy-lead/SKILL.md"

# U-16f: the skills root linked into a work tree that is no buddy checkout
# (a dotfiles repo): the work-tree clause alone holds it, at the physical
# path, and the line names the hand removal.
label=U-16f
setup
mkrepo "$R/dots"; mv "$R/skills" "$R/dots/skills"; ln -s dots/skills "$R/skills"
RP=$(cd "$R" && env pwd -P)
u16 "$R/dots/skills/buddy-fleet" "$(vcs_line "$R/skills/buddy-fleet" "$RP/dots/.git")" "$(vcs_line "$R/skills/buddy" "$RP/dots/.git")"

# U-16l: the skills root linked into ANOTHER buddy checkout's skills/ (a
# second clone or worktree; its scripts/install-skill.sh marks it): its own
# clause, named with that checkout (the panel: an install from this clone
# overwrote the other's tracked files).
label=U-16l
setup
mkrepo "$R/clone"; mkdir "$R/clone/scripts"; : > "$R/clone/scripts/install-skill.sh"
mv "$R/skills" "$R/clone/skills"; ln -s clone/skills "$R/skills"
RP=$(cd "$R" && env pwd -P)
u16 "$R/clone/skills/buddy-fleet" "$(clone_line "$R/skills/buddy-fleet" buddy-fleet "$RP/clone")" "$(clone_line "$R/skills/buddy" buddy "$RP/clone")"

# U-16n: the same into a checkout with NO version-control marker (an
# export), so the clone clause alone holds it (Fable cross-check: U-16l's
# .git still left it as `vcs`), linked in another case on a case-insensitive
# volume (the builtin pwd -P of macOS /bin/sh kept the link's spelling, and
# `Skills` was no `skills`: every identical file was removed).
label=U-16n
setup
mkdir -p "$R/clone2/scripts"; : > "$R/clone2/scripts/install-skill.sh"
mv "$R/skills" "$R/clone2/skills"
cl=clone2/skills
[ ! "$R/CLONE2/Skills" -ef "$R/clone2/skills" ] || cl=CLONE2/Skills
ln -s "$cl" "$R/skills"
RP=$(cd "$R" && env pwd -P)
u16 "$R/clone2/skills/buddy-fleet" "$(clone_line "$R/skills/buddy-fleet" buddy-fleet "$RP/clone2")" "$(clone_line "$R/skills/buddy" buddy "$RP/clone2")"

# U-16g: a RELATIVE knob is read under the checkout, never by `cd`: `-` is
# $OLDPWD to cd, so from a caller's directory outside any work tree the old
# judge looked there while the removal reached <checkout>/- (Codex design
# pass). <checkout>/- holds fleet copies, inside the checkout.
label=U-16g
setup
mkdir "$R/co/-"; cp "$R/skills/buddy-fleet/"*.md "$R/co/-/"
XENV="BUDDY_FLEET_SKILL_DIR=-"
u16_cwd=$(pwd); cd "$WORK"
u16 "$R/co/-" "$(checkout_line "$R/co/-")"
cd "$u16_cwd"; XENV=
gone "$R/skills/buddy-lead/SKILL.md"

# U-16j: a destination inside the checkout that is not its own skills/<n>
# (the core's knob naming the checkout's skills/buddy-fleet, after an
# install that wrote the core's SKILL.md there), in an export with no
# marker: the checkout clause alone holds it (the panel).
label=U-16j
setup
mv "$R/co/.git" "$R/co.git-aside"
cp "$R/co/skills/buddy/SKILL.md" "$R/co/skills/buddy-fleet/SKILL.md"
XENV="BUDDY_SKILL_DIR=$R/co/skills/buddy-fleet BUDDY_FLEET_SKILL_DIR=$R/skills/buddy-fleet"
u16 "$R/co/skills/buddy-fleet" "$(checkout_line "$R/co/skills/buddy-fleet")"
XENV=
gone "$R/skills/buddy-fleet/restack.md"

# U-16m: two knobs naming one directory: each file the plan named is
# removed and reported once (the panel: the second skill's pass re-ran the
# first's unlinks, rm -f succeeded on nothing, and "removed:" printed twice).
label=U-16m
setup
XENV="BUDDY_FLEET_SKILL_DIR=$R/skills/buddy"
out=$(un 2>&1) || fail "U-16m: uninstall failed:
$out"
XENV=
[ "$(echo "$out" | grep -cxF "uninstall: removed: $R/skills/buddy/SKILL.md")" -eq 1 ] || fail "U-16m: want one removed line for the core SKILL.md:
$out"
# The directory is acted on once: its SKILL.md.bak (setup's) keeps it, and
# that is said once (Fable cross-check: twice, one per knob).
[ "$(echo "$out" | grep -cF "skill: left: $R/skills/buddy, which still holds files")" -eq 1 ] || fail "U-16m: want one left line for the shared directory:
$out"

# U-16o: `/` as the core's destination is left, named (Codex re-verify:
# `///` matched no hop pattern, so `/` was `ok` and a /SKILL.md identical to
# the checkout's would have gone). The fleet skill beside it in $R still
# goes: the positive control that the run reached the skills.
label=U-16o
setup
XENV="BUDDY_SKILL_DIR=/ BUDDY_FLEET_SKILL_DIR=$R/skills/buddy-fleet"
out=$(un --dry-run 2>&1) || fail "U-16o: dry run failed:
$out"
XENV=
echo "$out" | grep -qF "skill: left: / (whole), since it is / or has a . or .. component" || fail "U-16o: / is not left and named:
$out"
echo "$out" | grep -qF "$R/skills/buddy-fleet/SKILL.md" || fail "U-16o: the fleet skill beside it was not planned:
$out"

# U-16k: a knob whose last name is . or .. stops the run before anything is
# planned: BUDDY_SKILL_DIR=<skills>/buddy/. put every other skill's
# destination under buddy/, absent, and the model router's state passed as
# its skill gone (the panel).
XENV="BUDDY_SKILL_DIR=$R/skills/buddy/."; stopped U-16k "a skill destination has a . or .. component ($R/skills/buddy/.); nothing was removed"
XENV="BUDDY_FLEET_SKILL_DIR=$R/skills/buddy-fleet/.."; stopped U-16k2 "a skill destination has a . or .. component ($R/skills/buddy-fleet/..); nothing was removed"
# Above the last name too (Codex code pass): install refuses the same knob.
XENV="BUDDY_SKILL_DIR=$R/skills/missing/../buddy"; stopped U-16k3 "a skill destination has a . or .. component ($R/skills/missing/../buddy); nothing was removed"
XENV=

# U-16h: a physical path holding a newline (a link above into "foreign<LF>",
# a work tree) is left and named on ONE line, never printed (invariant 9).
# Outside the case's tree: snap() reads one path per line.
label=U-16h
setup
F="$WORK/foreign
"
rm -rf "$F"
mkdir "$F"; mkrepo "$F"; mv "$R/skills/buddy-fleet" "$F/buddy-fleet"; ln -s "$F" "$R/up"
XENV="BUDDY_FLEET_SKILL_DIR=$R/up/buddy-fleet"
u16 "$F/buddy-fleet" "$R/up/buddy-fleet (whole), since its physical path holds a newline"
XENV=
echo "$out" | grep -qF "which may track its files" && fail "U-16h: the work-tree line printed a path holding a newline:
$out"
gone "$R/skills/buddy-lead/SKILL.md"
rm -rf "$F"

# U-16i: a directory that becomes a link to the checkout's own between the
# plan and the act (the bootout runs between them) is judged again and left:
# nothing in it goes (a mutant without the act's judgement deletes the
# checkout's files through the new link).
label=U-16i
setup
printf '[ -e %s ] && exit 0\nrm -rf %s && ln -s %s %s\n: > %s\n' "$WORK/swap.done" "$R/skills/buddy-lead" "$R/co/skills/buddy-lead" "$R/skills/buddy-lead" "$WORK/swap.done" > "$WORK/swap.sh"
rm -f "$WORK/swap.done"
b4=$(tsum "$R/co/skills/buddy-lead")
XENV="FAKE_ON_BOOTOUT=$WORK/swap.sh"
out=$(un 2>&1) || fail "U-16i: uninstall failed:
$out"
XENV=
[ -e "$WORK/swap.done" ] || fail "U-16i: nothing was swapped between plan and act, so this case tests nothing:
$out"
echo "$out" | grep -qxF "uninstall: skill: will remove $R/skills/buddy-lead/SKILL.md" || fail "U-16i: the plan did not see the installed copy:
$out"
echo "$out" | grep -qxF "uninstall: skill: left: $(ownlink_line "$R/skills/buddy-lead" buddy-lead) (found at the act; nothing in it was removed)" || fail "U-16i: the act did not leave the swapped directory:
$out"
[ "$(tsum "$R/co/skills/buddy-lead")" = "$b4" ] || fail "U-16i: the checkout's skills/buddy-lead changed:
$out"
gone "$R/skills/buddy-fleet/SKILL.md"

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
grep -qsx "bootout gui/$(id -u)/$AGENT" "$FAKE/calls" && fail "U-11: a job that is not ours was booted out"
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
grep -qx "bootout gui/$(id -u)/$AGENT" "$FAKE/calls" || fail "U-11d: a job whose program is gone was not booted out"
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
	grep -qsx "bootout gui/$(id -u)/$AGENT" "$FAKE/calls" && fail "U-11f: a job whose program could not be read was booted out:
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
	grep -qsx "bootout gui/$(id -u)/$AGENT" "$FAKE/calls" && fail "U-11g: a job whose program sits behind an unsearchable symlink was booted out:
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
grep -qsx "bootout gui/$(id -u)/$AGENT" "$FAKE/calls" && fail "U-11c: a job running a foreign buddylist was booted out"
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

# U-14: the D-173 removals' refusals and leave-it cases; U-4 (and U-3b for
# --chat) is the positive control of each, the same fixture removed.
# U-14a: a settings file that is a symlink is refused, named, its target
# byte-identical, and the rest of the run goes on to done.
label=U-14a; setup
mv "$R/settings.json" "$R/settings.real.json"; ln -s settings.real.json "$R/settings.json"
out=$(un 2>&1) || fail "U-14a: a refused settings edit must not fail the run:
$out"
cmp -s "$R/settings.real.json" "$WORK/settings.fixture.json" && [ -L "$R/settings.json" ] || fail "U-14a: a symlinked settings file was edited:
$out"
echo "$out" | grep -qF "settings: left: $R/settings.json unedited, refused: a symlink" || fail "U-14a: the refusal is not named:
$out"
ls "$R/settings.real.json.buddy-backup-"* "$R/settings.json.buddy-backup-"* >/dev/null 2>&1 && fail "U-14a: a refused edit left a backup"
gone $bins
echo "$out" | tail -1 | grep -qx 'uninstall: done' || fail "U-14a: no done line:
$out"
# U-14b: a bin dir that is not ~/bin: its lines run nothing this run removes,
# so the file is left byte-identical, and said.
label=U-14b; setup; rm "$R/home/bin"
out=$(un 2>&1) || fail "U-14b: uninstall failed:
$out"
cmp -s "$R/settings.json" "$WORK/settings.fixture.json" || fail "U-14b: settings were edited for a bin dir that is not ~/bin"
echo "$out" | grep -qF "settings: left: the hook lines in $R/settings.json run \$HOME/bin/..., and this run's bin dir is $R/bin" || fail "U-14b: the reason is not named:
$out"
# U-14c: no claude to run: every registration named with its command, the
# file byte-identical.
label=U-14c; setup; cp "$R/home/.claude.json" "$WORK/cj"
XENV=BUDDY_CLAUDE=$WORK/no-claude
out=$(un 2>&1) || fail "U-14c: uninstall failed:
$out"
XENV=
cmp -s "$R/home/.claude.json" "$WORK/cj" || fail "U-14c: ~/.claude.json changed with no claude to run"
echo "$out" | grep -qF "mcp: left: buddylist is registered for $PJ in $R/home/.claude.json, and there is no $WORK/no-claude here to remove it with: cd $PJ && claude mcp remove buddylist --scope local" || fail "U-14c: the command is not named:
$out"
# U-14d: claude says it removed it and did not: the read-back names it.
label=U-14d; setup; XENV=FAKE_CLAUDE_NOOP=1
out=$(un 2>&1) || fail "U-14d: uninstall failed:
$out"
XENV=
echo "$out" | grep -qF "mcp: left: the buddylist MCP registration for $PJ is still in $R/home/.claude.json (claude exited 0)" || fail "U-14d: the read-back did not catch it:
$out"
echo "$out" | grep -qF "removed: the buddylist MCP registration" && fail "U-14d: a registration still there was said removed:
$out"
# U-14h: the settings change after the plan read them (here, while claude
# runs): the write refuses, names it, and leaves the changed bytes as they are.
label=U-14h; setup; XENV=FAKE_TOUCH=$R/settings.json
out=$(un 2>&1) || fail "U-14h: a refused settings write must not fail the run:
$out"
XENV=
echo "$out" | grep -qF "settings: left: $R/settings.json unedited, refused: changed after the plan read it" || fail "U-14h: the changed file is not refused:
$out"
[ "$(cat "$WORK/settings.fixture.json"; printf '  ')" = "$(cat "$R/settings.json")" ] || fail "U-14h: the changed settings were edited anyway"
# U-14i: a BUDDY_CLAUDE_JSON that is not the file claude writes: claude is
# never run (it would remove from its own file what this one approved).
label=U-14i; setup; cp "$R/home/.claude.json" "$WORK/other.json"; XENV=BUDDY_CLAUDE_JSON=$WORK/other.json
out=$(un 2>&1) || fail "U-14i: uninstall failed:
$out"
XENV=
[ ! -e "$FAKE/claude-calls" ] || fail "U-14i: claude ran for a file it does not write: $(cat "$FAKE/claude-calls")"
echo "$out" | grep -qF "but claude writes $R/home/.claude.json, not that file" || fail "U-14i: the mismatch is not named:
$out"
# U-14j: the user-wide registration rewritten between plan and act (at the
# daemon's bootout): not removed, said; the project's still goes.
label=U-14j; setup
cat > "$WORK/rewrite.sh" <<EOF
python3 -I -c 'import json,sys; d=json.load(open(sys.argv[1])); d["mcpServers"]["buddylist"]["command"]="/opt/foreign"; json.dump(d,open(sys.argv[1],"w"))' "$R/home/.claude.json"
EOF
XENV=FAKE_ON_BOOTOUT=$WORK/rewrite.sh
out=$(un 2>&1) || fail "U-14j: uninstall failed:
$out"
XENV=
[ "$(cat "$FAKE/claude-calls")" = "$PJ|mcp remove buddylist --scope local" ] || fail "U-14j: claude was run for a registration that changed:
$(cat "$FAKE/claude-calls")"
mcp_now "$R/home/.claude.json" | grep -qx 'user - /opt/foreign' || fail "U-14j: the rewritten registration is gone"
echo "$out" | grep -qF "mcp: left: the user-wide buddylist MCP registration, since it changed since the plan read it" || fail "U-14j: the change is not named:
$out"
# U-14k: a registration naming a hard link of a removed buddylist, kept in
# place: the same file once, but a program still there after the run.
label=U-14k; setup; mkdir -p "$R/kept"; ln "$R/bin/buddylist" "$R/kept/buddylist"
python3 -I -c 'import json,sys; d=json.load(open(sys.argv[1])); d["projects"][sys.argv[2]]["mcpServers"]["buddylist"]["command"]=sys.argv[3]; json.dump(d,open(sys.argv[1],"w"))' "$R/home/.claude.json" "$PJ3" "$R/kept/buddylist"
out=$(un 2>&1) || fail "U-14k: uninstall failed:
$out"
grep -qF "$PJ3|" "$FAKE/claude-calls" && fail "U-14k: a registration of a program still there was removed"
mcp_now "$R/home/.claude.json" | grep -qxF "local $PJ3 $R/kept/buddylist" || fail "U-14k: the kept program's registration is gone"
# The plan's own match, not only the act's recheck behind it (mutation round 2).
echo "$out" | grep -qF "mcp: left: buddylist is registered for $PJ3 in $R/home/.claude.json with the command \"$R/kept/buddylist\", which is not a buddylist this run removes" || fail "U-14k: the plan matched the hard link as ours:
$out"
# U-14l: a loaded program whose path holds " = ": the whole path is judged,
# never the part before it (Codex: awk -F' = ' read the pinned one's bytes).
label=U-14l; setup
printf 'not the server\n' > "$R/oscar/oscar-server = foreign"
printf '%s' "$R/oscar/oscar-server = foreign" > "$FAKE/loaded.$OSCAR"
out=$(un 2>&1) || fail "U-14l: uninstall failed:
$out"
grep -q "^bootout gui/$(id -u)/$OSCAR" "$FAKE/calls" && fail "U-14l: a job running '$R/oscar/oscar-server = foreign' was booted out"
echo "$out" | grep -qF "since its loaded job runs $R/oscar/oscar-server = foreign," || fail "U-14l: the whole path is not named:
$out"
if [ -n "$macos" ]; then
	# U-14m: a plist whose Program is another executable, its
	# ProgramArguments[0] the pinned server: launchd runs Program, so left.
	label=U-14m; setup; rm -f "$FAKE/loaded.$OSCAR"
	sed "s#<key>ProgramArguments</key>#<key>Program</key><string>/usr/bin/true</string><key>ProgramArguments</key>#" "$R/home/Library/LaunchAgents/$OSCAR.plist" > "$WORK/op" && cat "$WORK/op" > "$R/home/Library/LaunchAgents/$OSCAR.plist"
	out=$(un 2>&1) || fail "U-14m: uninstall failed:
$out"
	there "$R/home/Library/LaunchAgents/$OSCAR.plist"
	# U-14n: a Label with a trailing newline is another Label.
	label=U-14n; setup; rm -f "$FAKE/loaded.$OSCAR"
	sed "s#<string>$OSCAR</string>#<string>$OSCAR\&\#10;</string>#" "$R/home/Library/LaunchAgents/$OSCAR.plist" > "$WORK/op" && cat "$WORK/op" > "$R/home/Library/LaunchAgents/$OSCAR.plist"
	grep -qF "$OSCAR&#10;" "$R/home/Library/LaunchAgents/$OSCAR.plist" || fail "U-14n: the fixture's Label was not rewritten"
	out=$(un 2>&1) || fail "U-14n: uninstall failed:
$out"
	there "$R/home/Library/LaunchAgents/$OSCAR.plist"
	# U-14e: an oscar whose bytes are another revision is not provably ours:
	# never booted out, its plist kept, named.
	label=U-14e; setup
	printf 'elf\npath\tgithub.com/mk6i/open-oscar-server/cmd/server\nbuild\tvcs.revision=%s\n' 0000000000000000000000000000000000000000 > "$R/oscar/oscar-server"
	out=$(un 2>&1) || fail "U-14e: uninstall failed:
$out"
	grep -q "^bootout gui/$(id -u)/$OSCAR" "$FAKE/calls" && fail "U-14e: an oscar not provably ours was booted out"
	there "$R/home/Library/LaunchAgents/$OSCAR.plist"
	echo "$out" | grep -qF "chat server: left: $OSCAR, since its loaded job runs $R/oscar/oscar-server, whose bytes are not the pinned server" || fail "U-14e: the foreign oscar is not named:
$out"
	# U-14f: ours, loaded, under a plist naming ANOTHER program: left whole.
	label=U-14f; setup
	sed "s#$R/oscar/oscar-server#/usr/bin/true#" "$R/home/Library/LaunchAgents/$OSCAR.plist" > "$WORK/op" && cat "$WORK/op" > "$R/home/Library/LaunchAgents/$OSCAR.plist"
	out=$(un 2>&1) || fail "U-14f: uninstall failed:
$out"
	grep -q "^bootout gui/$(id -u)/$OSCAR" "$FAKE/calls" && fail "U-14f: an oscar job whose plist runs something else was booted out"
	there "$R/home/Library/LaunchAgents/$OSCAR.plist"
	# U-14g: a bootout that fails is named; the plist stays; the run is done.
	label=U-14g; setup; XENV=FAKE_BOOTOUT_FAIL_LABEL=$OSCAR
	out=$(un 2>&1) || fail "U-14g: a failed oscar bootout must not fail the run:
$out"
	XENV=
	there "$R/home/Library/LaunchAgents/$OSCAR.plist"
	echo "$out" | grep -qF "chat server: left: launchctl bootout gui/$(id -u)/$OSCAR did not take" || fail "U-14g: the failed bootout is not named:
$out"
	echo "$out" | tail -1 | grep -qx 'uninstall: done' || fail "U-14g: no done line:
$out"
	# U-14o: the developing machine's own ergo (D-176): built by hand as
	# `command-line-arguments`, at ~/.buddylist/ergo-bin, loaded, under the
	# right Label. A Label and a program under ~/.buddylist prove nothing, so
	# it is left, named with the rebuild, never booted out; oscar in the same
	# run is booted out (the run's own control; U-4 is ergo's).
	label=U-14o; setup
	printf 'mach\npath\tcommand-line-arguments\ndep\tgithub.com/ergochat/irc-go\tv0.5.0\t\n' > "$R/home/.buddylist/ergo-bin"
	sed "s#$R/ergo/ergo-server#$R/home/.buddylist/ergo-bin#" "$LA/$ERGO.plist" > "$WORK/ep" && cat "$WORK/ep" > "$LA/$ERGO.plist"
	printf '%s' "$R/home/.buddylist/ergo-bin" > "$FAKE/loaded.$ERGO"
	out=$(un 2>&1) || fail "U-14o: an unprovable ergo must not fail the run:
$out"
	grep -q "^bootout gui/$(id -u)/$ERGO" "$FAKE/calls" && fail "U-14o: an ergo not provably buddy's was booted out"
	grep -qx "bootout gui/$(id -u)/$OSCAR" "$FAKE/calls" || fail "U-14o: oscar, provable, was not booted out in the same run:
$(cat "$FAKE/calls")"
	there "$LA/$ERGO.plist" "$FAKE/loaded.$ERGO" "$R/home/.buddylist/ergo-bin"
	echo "$out" | grep -qF "chat server: left: $ERGO, since its loaded job runs $R/home/.buddylist/ergo-bin, whose bytes are not the pinned server get-ergo.sh builds (to make it provable: sh scripts/get-ergo.sh" || fail "U-14o: the unprovable ergo is not named with its rebuild:
$out"
	echo "$out" | grep -qF "launchctl bootout gui/$(id -u)/$ERGO, then move" || fail "U-14o: the by-hand bootout is not named:
$out"
	# U-14p: the pin is read from get-ergo.sh: a checkout without one proves
	# no ergo, even one whose bytes carry the pin.
	label=U-14p; setup; rm "$R/co/scripts/get-ergo.sh"
	out=$(un 2>&1) || fail "U-14p: uninstall failed:
$out"
	grep -q "^bootout gui/$(id -u)/$ERGO" "$FAKE/calls" && fail "U-14p: ergo was booted out with no pin to prove it by"
	there "$LA/$ERGO.plist"
	echo "$out" | grep -qF "chat server: left: $ERGO, since its loaded job runs $R/ergo/ergo-server, which cannot be proved here: scripts/get-ergo.sh holds no PIN line" || fail "U-14p: the missing pin is not named:
$out"
	# U-14s: the loaded job is the pinned ergo, its plist names another
	# program: left whole (the plist's half of the proof).
	label=U-14s; setup
	sed "s#<string>$R/ergo/ergo-server</string>#<string>/usr/bin/true</string>#" "$LA/$ERGO.plist" > "$WORK/ep" && cat "$WORK/ep" > "$LA/$ERGO.plist"
	grep -qF /usr/bin/true "$LA/$ERGO.plist" || fail "U-14s: the fixture's program was not rewritten"
	out=$(un 2>&1) || fail "U-14s: uninstall failed:
$out"
	grep -q "^bootout gui/$(id -u)/$ERGO" "$FAKE/calls" && fail "U-14s: an ergo job whose plist runs something else was booted out"
	there "$LA/$ERGO.plist"
	# U-14t: the pinned revision under another build path (a package inside
	# ergo's module, a fork's): the path line is half the proof, so left.
	label=U-14t; setup
	printf 'mach\npath\tgithub.com/ergochat/ergo/cmd/other\nbuild\tvcs.revision=%s\n' "$EPIN" > "$R/ergo/ergo-server"
	out=$(un 2>&1) || fail "U-14t: uninstall failed:
$out"
	grep -q "^bootout gui/$(id -u)/$ERGO" "$FAKE/calls" && fail "U-14t: an ergo whose build path is not the module's was booted out"
	there "$LA/$ERGO.plist"
	echo "$out" | grep -qF "chat server: left: $ERGO, since its loaded job runs $R/ergo/ergo-server, whose bytes are not the pinned server get-ergo.sh builds" || fail "U-14t: the wrong build path is not named:
$out"
	# U-14u: an unloaded plist naming a RELATIVE program, a pinned build at
	# that name under the checkout (uninstall's cwd): launchd resolves it its
	# own way, so it proves nothing and the plist stays (Codex, the code pass).
	label=U-14u; setup; rm -f "$FAKE/loaded.$ERGO"
	cp "$R/ergo/ergo-server" "$R/co/ergo-server"
	sed "s#<string>$R/ergo/ergo-server</string>#<string>ergo-server</string>#" "$LA/$ERGO.plist" > "$WORK/ep" && cat "$WORK/ep" > "$LA/$ERGO.plist"
	grep -qF '<string>ergo-server</string>' "$LA/$ERGO.plist" || fail "U-14u: the fixture's program was not rewritten"
	out=$(un 2>&1) || fail "U-14u: uninstall failed:
$out"
	there "$LA/$ERGO.plist"
	echo "$out" | grep -qF "chat server: left: $ERGO, since its plist $LA/$ERGO.plist is a symlink, cannot be read here, or names ergo-server under the Label $ERGO" || fail "U-14u: the relative program is not named:
$out"
fi
# U-14q: a claude that hangs (ignoring TERM, with a child that does too) is
# stopped at BUDDY_MCP_TIMEOUT, its process group with it, and named left;
# the run goes on to done (D-176). U-4 is the control: the same removals,
# claude exiting, both said removed.
label=U-14q; setup; XENV="FAKE_CLAUDE_HANG=1 BUDDY_MCP_TIMEOUT=1"
t0=$(date +%s)
out=$(un 2>&1) || fail "U-14q: a hung claude must not fail the run:
$out"
t1=$(date +%s)
XENV=
[ $((t1 - t0)) -lt 40 ] || fail "U-14q: the run took $((t1 - t0))s with a 1 s bound on each claude: the bound did not hold"
[ "$(wc -l < "$FAKE/claude-calls")" -eq 2 ] || fail "U-14q: claude was not tried for both registrations:
$(cat "$FAKE/claude-calls")"
for want in "mcp: left: the user-wide buddylist MCP registration, since claude mcp remove did not exit within 1s and was stopped (BUDDY_MCP_TIMEOUT); remove it by hand: claude mcp remove buddylist --scope user" \
	"mcp: left: the buddylist MCP registration for $PJ, since claude mcp remove did not exit within 1s and was stopped"; do
	echo "$out" | grep -qF "$want" || fail "U-14q: no line '$want':
$out"
done
mcp_now "$R/home/.claude.json" | grep -qxF "user - $R/bin/buddylist" || fail "U-14q: the user-wide registration is gone though claude never removed it"
gone $bins
echo "$out" | tail -1 | grep -qx 'uninstall: done' || fail "U-14q: no done line:
$out"
# Every process the fake left is gone: the KILL went to the group.
[ "$(wc -l < "$FAKE/hang-pids")" -eq 2 ] || fail "U-14q: the fake recorded $(wc -l < "$FAKE/hang-pids") hangs, want 2"
for pid in $(cat "$FAKE/hang-pids"); do
	i=0
	while kill -0 "$pid" 2>/dev/null; do
		[ "$i" -lt 20 ] || fail "U-14q: pid $pid of the hung claude's group is still alive"
		sleep 0.1; i=$((i + 1))
	done
done
# U-14r: a BUDDY_MCP_TIMEOUT that is not a positive whole number is refused
# before anything (U-4, unset, is the control).
for v in 0 abc 1.5 07; do
	label="U-14r($v)"; setup; before=$(snap)
	out=$(BUDDY_MCP_TIMEOUT=$v; export BUDDY_MCP_TIMEOUT; un 2>&1) && fail "$label: should have been refused:
$out"
	echo "$out" | grep -qF "refused: BUDDY_MCP_TIMEOUT=$v is not a whole number of seconds above 0" || fail "$label: the refusal does not name it:
$out"
	[ "$(snap)" = "$before" ] || fail "$label: a refused run changed the tree"
done

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

# U-13: the install lock install.sh and uninstall.sh share (D-126). The test
# holds it itself, as a live run would (this shell's pid and start time), so
# the overlap is a fixture, never a race; U-13e is the real one, through the
# fake launchctl. U-4 is the control for every refusal (the same case,
# unlocked), the prototype install and U-13d the install's.
. "$ROOT/scripts/install-lock.sh"
LK=$R/bin/.buddy-install.lock
hold() { # hold <by> <pid> <started>: a lock as install-lock.sh writes one
	ln -sn "$ILOCK_TAG pid=$2 by=$1 started=$3; from=/the/test" "$LK"
}
me_started=$(ilock_started $$)
[ -n "$me_started" ] || fail "U-13: ps gives no start time for this shell, so the lock cases cannot be built"
# A TZ of its own: the lock's start time must not depend on it (Codex: ps
# prints local time, and a live holder read as a reused pid).
inst() { # install.sh, this checkout's, every knob into $R
	env HOME="$R/home" PATH="${IPATH:-}$GUARD:$PATH" TZ=XST+11 GOCACHE="$gocache" GOMODCACHE="$gomodcache" \
		BUDDY_BIN_DIR="$R/bin" BUDDY_SKILL_DIR="$R/skills/buddy" BUDDY_SETTINGS="$R/none.json" \
		BUDDY_DAEMON=off BUDDY_SETUP_CLONE=off BUDDY_REPO_BIN=off BUDDY_MCP_JSON="$R/none-mcp.json" \
		BUDDY_CLAUDE_JSON="$R/home/.claude.json" sh "$ROOT/scripts/install.sh"
}
# U-13a: an uninstall holds the lock, so an install changes nothing, says who
# holds it, and exits 1.
label=U-13a; setup; hold uninstall.sh $$ "$me_started"; before=$(snap)
out=$(inst 2>&1) && fail "U-13a: an install under a live uninstall's lock must stop:
$out"
echo "$out" | grep -qF "install: FAILED — uninstall.sh (pid $$, started $me_started, from /the/test) holds $LK, so a buddy install or uninstall is running; nothing was changed" || fail "U-13a: the stop does not name the holder:
$out"
[ "$(snap)" = "$before" ] || fail "U-13a: a stopped install changed the tree:
$out"
# U-13b: the reverse: an install holds it, so an uninstall removes nothing,
# and asks launchd nothing.
XENV=; label=U-13b; setup; hold install.sh $$ "$me_started"; before=$(snap)
out=$(un 2>&1) && fail "U-13b: an uninstall under a live install's lock must stop:
$out"
echo "$out" | grep -qF "uninstall: STOPPED — install.sh (pid $$, started $me_started, from /the/test) holds $LK" || fail "U-13b: the stop does not name the holder:
$out"
[ "$(snap)" = "$before" ] || fail "U-13b: a stopped uninstall changed the tree:
$out"
[ ! -s "$FAKE/calls" ] || fail "U-13b: launchd was asked before the lock was taken:
$(cat "$FAKE/calls")"
# U-13f: a dry run takes no lock: it says a held one and plans as ever.
label=U-13f; setup; hold install.sh $$ "$me_started"; before=$(snap)
out=$(un --dry-run 2>&1) || fail "U-13f: a dry run must not stop on the lock:
$out"
echo "$out" | grep -qF "uninstall: lock: NOTE — install.sh (pid $$, started $me_started, from /the/test) holds $LK; a real run now would stop" || fail "U-13f: the dry run does not say the lock is held:
$out"
[ "$(snap)" = "$before" ] || fail "U-13f: a dry run changed the tree"
# U-13g: a symlink at the lock's name that neither script wrote, to a
# writable directory, is refused, never taken: `ln -s` without -n linked
# INSIDE the directory and read as taken (measured on /etc and a scratch dir).
label=U-13g; setup; mkdir -p "$R/elsewhere"; ln -s "$R/elsewhere" "$LK"; before=$(snap)
out=$(inst 2>&1) && fail "U-13g: an install over a foreign symlink at the lock must stop:
$out"
echo "$out" | grep -qF "$LK is a symlink neither install.sh nor uninstall.sh wrote (it names $R/elsewhere)" || fail "U-13g: the stop does not name the link:
$out"
[ "$(snap)" = "$before" ] || fail "U-13g: a stopped install changed the tree:
$out"
# U-13c: a lock whose pid is dead (a killed run) is taken over, said, and the
# run finishes and leaves no lock.
label=U-13c; setup
sh -c 'exit 0' & dead=$!; wait "$dead" || true
hold install.sh "$dead" "$me_started"
out=$(un 2>&1) || fail "U-13c: a dead holder's lock must not stop the run:
$out"
echo "$out" | grep -qF "uninstall: lock: TOOK OVER $LK from install.sh (pid $dead," || fail "U-13c: the takeover is not said:
$out"
gone $bins "$LK"
echo "$out" | tail -1 | grep -qx 'uninstall: done' || fail "U-13c: no done line:
$out"
# U-13d: a live pid that started at another time is a reused pid, not the
# holder: an install takes it over, does its whole job and releases.
label=U-13d; setup; hold uninstall.sh $$ "Thu Jan 1 00:00:00 1970"
out=$(inst 2>&1) || fail "U-13d: an install over a reused pid's lock failed:
$out"
echo "$out" | grep -qF "install: lock: TOOK OVER $LK from uninstall.sh (pid $$, started Thu Jan 1 00:00:00 1970" || fail "U-13d: the takeover is not said:
$out"
echo "$out" | tail -1 | grep -qx 'install: done' || fail "U-13d: no done line:
$out"
gone "$LK"
# U-13e: the real overlap: uninstall's first launchctl print runs install.sh
# while uninstall holds the lock. The install stops, changing no binary or
# skill, and the uninstall then removes everything, buddy too, and says done.
label=U-13e; setup
cat > "$WORK/on-print.sh" <<HOOK
cksum "$R/bin/buddy" "$R/bin/buddylist" "$R/skills/buddy/SKILL.md"
env HOME="$R/home" GOCACHE="$gocache" GOMODCACHE="$gomodcache" BUDDY_BIN_DIR="$R/bin" \\
	BUDDY_SKILL_DIR="$R/skills/buddy" BUDDY_SETTINGS="$R/none.json" BUDDY_DAEMON=off \\
	BUDDY_SETUP_CLONE=off BUDDY_REPO_BIN=off BUDDY_MCP_JSON="$R/none-mcp.json" \\
	sh "$ROOT/scripts/install.sh" && rc=0 || rc=\$?
cksum "$R/bin/buddy" "$R/bin/buddylist" "$R/skills/buddy/SKILL.md"
exit \$rc
HOOK
XENV=FAKE_ON_PRINT=$WORK/on-print.sh
out=$(un 2>&1) || fail "U-13e: uninstall failed:
$out"
XENV=
[ "$(cat "$FAKE/on-print.rc" 2>/dev/null)" = 1 ] || fail "U-13e: the install run inside uninstall's lock did not stop (status $(cat "$FAKE/on-print.rc" 2>/dev/null)):
$(cat "$FAKE/on-print.out" 2>/dev/null)"
grep -qF "install: FAILED — uninstall.sh (pid " "$FAKE/on-print.out" || fail "U-13e: the install does not name the uninstall holding the lock:
$(cat "$FAKE/on-print.out")"
grep -q '^install: built' "$FAKE/on-print.out" && fail "U-13e: the install built something under the lock"
[ "$(head -3 "$FAKE/on-print.out")" = "$(grep -v '^install: ' "$FAKE/on-print.out" | tail -3)" ] || fail "U-13e: a binary or skill changed under the lock:
$(cat "$FAKE/on-print.out")"
gone $bins $skillmds "$LK"
echo "$out" | tail -1 | grep -qx 'uninstall: done' || fail "U-13e: no done line:
$out"

# A gated go: `go build` says ready on GATE_READY and blocks until a line
# arrives on GATE_GO, then fails; everything else is the real go. With both
# FIFOs held open read-write here, nothing blocks on an open, and a run that
# ends before its build says so on the ready FIFO instead of hanging the check.
mkdir -p "$WORK/gatego"
realgo=$(command -v go)
cat > "$WORK/gatego/go" <<EOF
#!/bin/sh
if [ "\$1" = build ]; then
	echo ready > "\$GATE_READY"
	read -r x < "\$GATE_GO" || x=eof
	[ "\$x" = go ] || { echo "gated go build: TIMED OUT (\$x)" >&2; exit 1; }
	echo "gated go build: released" >&2; exit 1
fi
exec "$realgo" "\$@"
EOF
chmod +x "$WORK/gatego/go"
mkfifo "$WORK/gate.ready" "$WORK/gate.go"
exec 4<>"$WORK/gate.ready" 5<>"$WORK/gate.go"
# gated_inst: install.sh in the background, stopped inside its first build
# with the lock held; returns once it is there. The group and the install
# hold neither FIFO open (the fake go opens the go FIFO by path), so a case
# that fails while one is gated ends it: this shell's exit closes the last
# writer and the fake build reads EOF. They did hold them, and a failing
# U-13i under a mutant left the install blocked for good, holding the
# caller's stdout (orc-15, measured: 53 min). A watchdog bounds each gated
# run at GATE_LIMIT seconds (default 300) and makes a hang a FAIL, never a
# wait: it says timeout on both FIFOs. Its status is caught, never
# left to set -e: the group inherits it, and an install that failed as the
# fake build makes it ended the group before it said so, hanging the check
# on the FIFO (measured).
gated_inst() {
	IPATH="$WORK/gatego:"
	{
		exec 5>&-
		export GATE_READY="$WORK/gate.ready" GATE_GO="$WORK/gate.go"
		[ -z "$1" ] || export BUDDY_INSTALL_LOCK_HELD="$1"
		inst > "$WORK/gated.out" 2>&1 4>&- && grc=0 || grc=$?
		echo "exited $grc" >&4
	} &
	gated=$!
	IPATH=
	(
		exec 4>&- 5>&- </dev/null >/dev/null 2>&1
		sleep "${GATE_LIMIT:-300}" &
		gs=$!
		trap 'kill "$gs" 2>/dev/null; exit 0' TERM
		wait "$gs"
		echo timeout > "$WORK/gate.ready"
		echo timeout > "$WORK/gate.go"
	) &
	gwd=$!
	read -r r <&4
	[ "$r" = ready ] || { kill "$gwd" 2>/dev/null || true; fail "$label: the install never reached its build ($r):
$(cat "$WORK/gated.out")"; }
}
gate_open() { # let the gated install go on, and see it end
	echo go >&5
	wait "$gated" || true
	read -r r <&4
	kill "$gwd" 2>/dev/null || true
	! grep -q 'gated go build: TIMED OUT' "$WORK/gated.out" || fail "$label: the gated install was held past ${GATE_LIMIT:-300}s, so the case hung:
$(cat "$WORK/gated.out")"
	[ "$r" = "exited 1" ] || fail "$label: the gated install did not end as its fake build made it ($r):
$(cat "$WORK/gated.out")"
}
# U-13h: a REAL install holds its lock for its whole run: stopped inside its
# build, an uninstall stops naming it and changes nothing; once the install
# ends, no lock is left (Codex: U-13a..g never saw a live install's lock).
label=U-13h; setup
gated_inst ""
before=$(snap)
out=$(un 2>&1) && rc=0 || rc=$?
after=$(snap)
gate_open
[ "$rc" -ne 0 ] || fail "U-13h: an uninstall while an install runs must stop:
$out"
echo "$out" | grep -qF "uninstall: STOPPED — install.sh (pid " || fail "U-13h: the stop does not name the running install:
$out"
[ "$after" = "$before" ] || fail "U-13h: a stopped uninstall changed the tree:
$out"
gone "$LK"
# U-13i: the hand-over. The check, as an uninstall holding the lock, runs a
# nested install (BUDDY_INSTALL_LOCK_HELD, as --chat's update does) and ends
# while that install still runs, as when update alone is killed (Codex). The
# lock passes to the install, so a second uninstall stops; when the install
# ends, no lock and no .inner is left.
label=U-13i; setup
say() { printf '%s\n' "$*" >> "$WORK/say.out"; }
die() { fail "U-13i: the check's own lock: $*"; }
shq() { printf "'%s'" "$1"; }
rm -f "$WORK/say.out"
ilock_take uninstall.sh "$LK"
gated_inst "$ILOCK_ME"
case $(readlink "$LK.inner" 2>/dev/null) in *" by=install.sh "*) ;; *) fail "U-13i: the nested install did not register in $LK.inner:
$(cat "$WORK/gated.out")" ;; esac
ilock_release
[ "$(readlink "$LK" 2>/dev/null)" = "$(readlink "$LK.inner" 2>/dev/null)" ] || fail "U-13i: ending, the outer run did not hand the lock to its running install:
$(cat "$WORK/say.out" 2>/dev/null)"
grep -qF "lock: handed $LK to the install.sh this run started" "$WORK/say.out" || fail "U-13i: the hand-over is not said"
out=$(un 2>&1) && rc=0 || rc=$?
gate_open
[ "$rc" -ne 0 ] || fail "U-13i: an uninstall while the handed-to install runs must stop:
$out"
echo "$out" | grep -qF "uninstall: STOPPED — install.sh (pid " || fail "U-13i: the stop does not name the install:
$out"
gone "$LK" "$LK.inner"
exec 4>&- 5>&-

# U-3: --chat. Real path when install.sh stamps the posture (D-124), else the
# stop it must make, and the success path against a stamped STUB buddy.
label=U-3
real=""
# Read from what install.sh BUILT, not from its text: a comment naming the
# stamp would otherwise flip this branch before any build carries one.
go version -m "$P/bin/buddy" 2>/dev/null | grep -qF 'internal/cli.installedChat=' && real=1
if [ -n "$real" ]; then
	setup; rb=$(rs_snap)
	out=$(XENV="BUDDY_DAEMON=off BUDDY_SETUP_CLONE=off BUDDY_REPO_BIN=off" un --chat 2>&1) || fail "U-3: --chat failed:
$out"
	gone "$R/bin/buddylist" "$R/daemon/buddylist"
	there "$R/bin/buddy" $skillmds "$R/co/bin/buddylist"
	[ "$(rs_snap)" = "$rb" ] && [ -f "$RS/table.tsv" ] || fail "U-3: --chat touched the model router's state (D-179):
$out"
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
	grep -qsx "bootout gui/$(id -u)/$AGENT" "$FAKE/calls" && fail "U-3: a stopped --chat booted the daemon out"
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
rb=$(rs_snap)
out=$(XENV="STUB_LOG=$WORK/stub.log STUB_RC=0" un --chat 2>&1) || fail "U-3b: --chat over a buddy stamped off failed:
$out"
# --chat leaves the skills, and so the model router's state (D-179).
[ "$(rs_snap)" = "$rb" ] && [ -f "$RS/table.tsv" ] || fail "U-3b: --chat touched the model router's state:
$out"
grep -qx 'BUDDY_CHAT=off' "$WORK/stub.log" || fail "U-3b: update was not run with BUDDY_CHAT=off"
gone "$R/bin/buddylist" "$R/daemon/buddylist" "$R/co/bin/buddylist"
there "$R/bin/buddy" "$R/co/bin/buddy" $skillmds
grep -qx "bootout gui/$(id -u)/$AGENT" "$FAKE/calls" || fail "U-3b: the daemon was not booted out"
[ -z "$macos" ] || gone "$(plist)"
kept U-3b
[ "$(hooks_now "$R/settings.json")" = "$(want_hooks chat)" ] || fail "U-3b: --chat must remove buddylist's two hooks and nothing else:
$(hooks_now "$R/settings.json")
$out"
echo "$out" | grep -qF "settings: left: buddy's hook lines" && fail "U-3b: --chat judged buddy's own hook lines:
$out"
[ "$(wc -l < "$FAKE/claude-calls")" -eq 2 ] || fail "U-3b: --chat did not remove the two MCP registrations:
$(cat "$FAKE/claude-calls" 2>/dev/null)"
[ -z "$macos" ] || grep -qx "bootout gui/$(id -u)/$OSCAR" "$FAKE/calls" || fail "U-3b: --chat did not boot the oscar job out"
echo "$out" | grep -qF "settings: $R/settings.json declares BUDDY_CHAT=on to every session, which outranks buddy's claims-only stamp" || fail "U-3b: settings declaring on are not named (the fixture's env says on; D-127):
$out"
echo "$out" | grep -qF "model router" && fail "U-3b: --chat leaves the skill, so it must not name the skill's data:
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
		BUDDY_CLAUDE="$FAKE/claude" BUDDY_CLAUDE_JSON="$R/home/.claude.json" BUDDY_MCP_JSON="$R/co/.mcp.json" BUDDY_REPO_BIN=off FAKE_DIR="$FAKE" \
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

[ ! -e "$GUARD/reached" ] || fail "the REAL launchctl or claude was reached: $(cat "$GUARD/reached")"
echo "check-uninstall: ok (dry runs change nothing; full and --chat remove what install put in place; stops remove nothing; foreign and edited files left, every file of a skill with its manifest and directory, an edited reference file and its directory left, a skill directory that is a link, the checkout's own or in a work tree left whole, every shipped skill's directory read from skills/, the model router's own files removed with its skill, its state left whole and named on every refusal, and kept by --chat)"
