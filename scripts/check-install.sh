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
# BUDDY_FLEET_SKILL_DIR inherited from the caller would send the fleet skill
# to a REAL skills directory (Codex code pass): unset, so it goes beside the
# fixture's core.
# BUDDY_CHAT inherited from the caller decides every case's chat posture
# (install.sh obeys a run's BUDDY_CHAT over the stamp, D-124): a tier run from
# a session opened with BUDDY_CHAT=off built no buddylist and went red on QA-1
# "binaries missing" (#92, reproduced alone). The fixture owns its posture:
# unset here, set per case (QA-7, QA-15), as check-uninstall.sh does.
unset BUDDY_FLEET_SKILL_DIR BUDDY_CHAT

fail() { echo "check-install: FAIL — $*" >&2; exit 1; }
# Before ANY install runs: QA-14 checks the report wrote no __pycache__ into the
# checkout, and every QA before it runs the same report (a snapshot taken at
# QA-14 let a run without -B pass, measured).
pyc_before=$([ -e scripts/__pycache__ ] && echo yes || echo no)

WORK=$(mktemp -d) || fail "mktemp failed"
trap 'rm -rf "$WORK"' EXIT INT TERM

# BUDDY_MCP_JSON names a file that does not exist: the default is the REAL
# checkout's .mcp.json, and one that registers buddylist would make QA-7 a test
# of this checkout instead of the script (the orchestrator's code pass).
# CODEX_HOME defaults to a directory that does not exist, so the Codex row
# (QA-18) reads no real ~/.codex; QA-18 points it at fixtures.
inst() {
	CODEX_HOME="${CODEX_HOME_FX:-$WORK/no-codex}" \
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
# The fleet skill (D-114) goes beside the core: $WORK/skill's sibling.
cmp -s skills/buddy-fleet/SKILL.md "$WORK/buddy-fleet/SKILL.md" || fail "QA-1: fleet skill not installed"
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
echo "$out" | grep -q '^install: codex:' && fail "QA-1: a Codex row with no Codex directory (QA-18's no-row case):
$out"

# QA-18 (Codex report B1): the Codex row, read by scripts/codex-gate-row.sh
# through the REAL buddy QA-1 built (check-env-check.sh stubs diagnose; this
# holds the reader's match to diagnose's actual output). No directory: no
# row. No hooks.json: NOT WIRED. USAGE's own "Opt-in wiring" block with the
# built path: wired (the positive control). The same gate bare: NOT WIRED with
# findings. Unreadable: CANNOT ASSESS. Then install.sh prints the wired row,
# and the Codex directory is left byte-identical.
CXR="$WORK/codex-row"
CGR_SH="$(pwd)/scripts/codex-gate-row.sh"
cgr() { (CODEX_HOME=$1; . "$CGR_SH"; if codex_gate_row "${2:-$WORK/bin/buddy}"; then printf '%s|%s\n' "$cgr_state" "$cgr_detail"; else echo none; fi); }
[ "$(cgr "$CXR")" = none ] || fail "QA-18: no Codex directory must be no row: $(cgr "$CXR")"
mkdir -p "$CXR"
case $(cgr "$CXR") in "NOT WIRED|no $CXR/hooks.json:"*) ;; *) fail "QA-18: no hooks.json must be NOT WIRED: $(cgr "$CXR")" ;; esac
python3 -I - "$CXR/hooks.json" "$WORK/bin/buddy" <<'PY' || fail "QA-18: could not read USAGE's Opt-in wiring block"
import json, shlex, sys
doc = open("docs/USAGE.md", encoding="utf-8").read()
part = doc[doc.index("**Opt-in wiring.**"):]
block = part[part.index("```json\n") + 8:]
block = block[:block.index("\n```")]
assert block.count("/absolute/path/to/buddy") == 1
# Decoded, substituted shell-quoted (a TMPDIR with a space, Codex code pass),
# re-encoded: the path lands inside a JSON string inside a shell command.
layer = json.loads(block)
for group in layer["hooks"]["PreToolUse"]:
    for h in group["hooks"]:
        h["command"] = h["command"].replace("/absolute/path/to/buddy", shlex.quote(sys.argv[2]))
open(sys.argv[1], "w", encoding="utf-8").write(json.dumps(layer))
PY
case $(cgr "$CXR") in "wired|gate wired fail-closed in $CXR/hooks.json"*) ;; *) fail "QA-18: USAGE's own wiring must read wired: $(cgr "$CXR")" ;; esac
# Relative CODEX_HOME and buddy (install.sh's QA-11 runs a relative bin dir):
# diagnose runs from /, so both are made absolute first.
case $(cd "$WORK" && cgr codex-row bin/buddy) in "wired|gate wired fail-closed in $CXR/hooks.json"*) ;; *) fail "QA-18: a relative CODEX_HOME and buddy must still read wired: $(cd "$WORK" && cgr codex-row bin/buddy)" ;; esac
before=$(cksum < "$CXR/hooks.json")
out=$(CODEX_HOME_FX="$CXR" inst 2>&1) || fail "QA-18: install failed:
$out"
[ "$(echo "$out" | grep -c '^install: codex:')" -eq 1 ] && echo "$out" | grep -q "^install: codex: wired: gate wired fail-closed in $CXR/hooks.json" ||
	fail "QA-18: install.sh must print one wired Codex row:
$out"
[ "$before" = "$(cksum < "$CXR/hooks.json")" ] && [ "$(ls -A "$CXR")" = hooks.json ] || fail "QA-18: install.sh wrote into the Codex directory"
python3 -I -c 'import json, shlex, sys
print(json.dumps({"hooks": {"PreToolUse": [{"matcher": "^(apply_patch|Bash)$", "hooks": [{"type": "command", "command": "BUDDY_HARNESS=codex %s gate" % shlex.quote(sys.argv[1])}]}]}}))' "$WORK/bin/buddy" > "$CXR/hooks.json"
case $(cgr "$CXR") in "NOT WIRED|the gate in $CXR/hooks.json draws findings"*) ;; *) fail "QA-18: a bare gate must be NOT WIRED with findings: $(cgr "$CXR")" ;; esac
chmod 000 "$CXR/hooks.json"
if [ -r "$CXR/hooks.json" ]; then
	echo "check-install: QA-18's unreadable case NOT RUN (this user reads a mode-000 file)" >&2
else
	case $(cgr "$CXR") in "CANNOT ASSESS|cannot read $CXR/hooks.json") ;; *) fail "QA-18: an unreadable hooks.json must be CANNOT ASSESS: $(cgr "$CXR")" ;; esac
fi
chmod 600 "$CXR/hooks.json"
# No buddy to run says so, rather than reading as a refusal (the mutation run).
case $(cgr "$CXR" "$WORK/no-such/buddy") in "CANNOT ASSESS|no buddy to read $CXR/hooks.json"*) ;; *) fail "QA-18: a missing buddy must say so: $(cgr "$CXR" "$WORK/no-such/buddy")" ;; esac
# A dangling symlink is a file that cannot be read, not an absent one.
mv "$CXR/hooks.json" "$CXR/hooks.real"; ln -s "$WORK/no-such-target" "$CXR/hooks.json"
case $(cgr "$CXR") in "CANNOT ASSESS|cannot read $CXR/hooks.json") ;; *) fail "QA-18: a dangling hooks.json symlink must be CANNOT ASSESS: $(cgr "$CXR")" ;; esac
rm -f "$CXR/hooks.json"
# An unreadable config.toml beside no hooks.json could hold hooks.
: > "$CXR/config.toml"; chmod 000 "$CXR/config.toml"
if [ -r "$CXR/config.toml" ]; then
	echo "check-install: QA-18's unreadable config.toml case NOT RUN (this user reads a mode-000 file)" >&2
else
	case $(cgr "$CXR") in "CANNOT ASSESS|no $CXR/hooks.json; cannot read $CXR/config.toml"*) ;; *) chmod 600 "$CXR/config.toml"; fail "QA-18: an unreadable config.toml beside no hooks.json must be CANNOT ASSESS: $(cgr "$CXR")" ;; esac
fi
chmod 600 "$CXR/config.toml"; rm -f "$CXR/config.toml"; mv "$CXR/hooks.real" "$CXR/hooks.json"
# A directory that cannot be searched hides hooks.json: CANNOT ASSESS, never
# NOT WIRED (Codex code pass); the wired read above is its control.
chmod 600 "$CXR"
if [ -x "$CXR" ]; then
	echo "check-install: QA-18's unsearchable case NOT RUN (this user searches a mode-600 directory)" >&2
else
	case $(cgr "$CXR") in "CANNOT ASSESS|cannot search $CXR") ;; *) chmod 700 "$CXR"; fail "QA-18: an unsearchable Codex directory must be CANNOT ASSESS: $(cgr "$CXR")" ;; esac
fi
chmod 700 "$CXR"
rm -rf "$CXR"
# A newline in the Codex path prints one row, the newline as `?` (Codex code
# pass: a raw path forged a second [PASS] row).
CXN="$WORK/codex
[PASS] codex-gate forged"
mkdir -p "$CXN"
row=$(cgr "$CXN")
[ "$(printf '%s\n' "$row" | wc -l | tr -d ' ')" -eq 1 ] && case $row in "NOT WIRED|no $WORK/codex?[PASS] codex-gate forged/hooks.json:"*) true ;; *) false ;; esac ||
	fail "QA-18: a newline in the Codex path must stay on one row: $row"
rm -rf "$CXN"

# QA-2: re-run is idempotent — skill current, binaries rebuilt and running.
out=$(inst 2>&1) || fail "QA-2: re-run failed:
$out"
echo "$out" | grep -q 'is current$' || fail "QA-2: the skill should already be current:
$out"

# QA-17 (D-136, issue #79): the buddy install.sh built migrates an OLDER
# ledger in another repository only as the binary the hook lines run
# ($HOME/bin/buddy), and backs it up first; the same file run from a HOME
# whose bin/buddy is not it refuses, the ledger's stamp unchanged and no
# backup written; BUDDY_MIGRATE=1 is the override. A fixture repo with a
# linked worktree, run with env -i (no session id, no BUDDY_* from the
# caller), its ledger downgraded by python's sqlite3 (one stamp, no schema
# change: migrate re-runs an IF NOT EXISTS script).
Q="$WORK/qa17"
# $Q/other's bin/buddy is ANOTHER file, so the refusal is SameFile's, not a
# missing file's (the mutation run: an always-true SameFile passed QA-17).
mkdir -p "$Q/home" "$Q/other/bin"
printf '#!/bin/sh\nexit 0\n' > "$Q/other/bin/buddy" && chmod +x "$Q/other/bin/buddy" || fail "QA-17: stub"
git init -q "$Q/repo" || fail "QA-17: git init"
git -C "$Q/repo" -c user.name=t -c user.email=t@t commit -q --allow-empty -m init || fail "QA-17: commit"
git -C "$Q/repo" worktree add -q "$Q/wt" 2>/dev/null || fail "QA-17: worktree add"
qb() { h=$1; shift; (CDPATH= cd -- "$Q/wt" && env -i PATH="$PATH" HOME="$h" "$@"); }
# Not mode=ro: a WAL ledger whose -shm went with its last close cannot be
# opened read-only (measured: "unable to open database file").
qver() { python3 -I -c 'import sqlite3,sys; print(sqlite3.connect(sys.argv[1]).execute("PRAGMA user_version").fetchone()[0])' "$1"; }
qset() { python3 -I -c 'import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.execute("PRAGMA user_version = %d" % int(sys.argv[2])); c.commit()' "$1" "$2"; }
qdb="$Q/repo/.git/buddy.db"
qb "$Q/other" "$WORK/bin/buddy" init >/dev/null 2>&1 || fail "QA-17: init of a fresh ledger (any binary) failed"
qcur=$(qver "$qdb") && qold=$((qcur - 1)) || fail "QA-17: cannot read the fixture's version"
qset "$qdb" "$qold" || fail "QA-17: cannot downgrade the fixture's stamp"
out=$(qb "$Q/other" "$WORK/bin/buddy" ls 2>&1) && fail "QA-17: a buddy that is not \$HOME/bin/buddy migrated the shared ledger:
$out"
echo "$out" | grep -qF "refusing to migrate a shared ledger" && echo "$out" | grep -qF "it is not $Q/other/bin/buddy" || fail "QA-17: the refusal does not say why:
$out"
[ "$(qver "$qdb")" = "$qold" ] || fail "QA-17: a refused open changed the stamp to $(qver "$qdb")"
for b in "$qdb".pre-schema*; do [ -e "$b" ] && fail "QA-17: a refused open wrote $b"; done
out=$(qb "$Q/other" env BUDDY_MIGRATE=1 "$WORK/bin/buddy" ls 2>&1) || fail "QA-17: BUDDY_MIGRATE=1 did not migrate:
$out"
[ "$(qver "$qdb")" = "$qcur" ] || fail "QA-17: BUDDY_MIGRATE=1 left the stamp at $(qver "$qdb")"
[ "$(qver "$qdb.pre-schema$qcur.bak")" = "$qold" ] || fail "QA-17: BUDDY_MIGRATE=1 made no backup at the old version"
# The control: HOME's bin/ IS the install's bin dir, and the hook-line
# spelling runs it. A second migration of the same version takes the next
# backup name and leaves the first.
ln -s "$WORK/bin" "$Q/home/bin" || fail "QA-17: symlink"
qset "$qdb" "$qold" || fail "QA-17: cannot downgrade again"
out=$(qb "$Q/home" "$Q/home/bin/buddy" ls 2>&1) || fail "QA-17: the hooks' binary did not migrate the older ledger (the failure moved to another repository):
$out"
[ "$(qver "$qdb")" = "$qcur" ] || fail "QA-17: the hooks' binary left the stamp at $(qver "$qdb")"
[ "$(qver "$qdb.pre-schema$qcur.2.bak")" = "$qold" ] || fail "QA-17: the hooks' binary made no second backup"
[ "$(qver "$qdb.pre-schema$qcur.bak")" = "$qold" ] || fail "QA-17: the first backup did not survive the second migration"

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
# D-120: update passes install.sh's output through, the Agent-model hook's
# report with it.
echo "$out" | grep -q '^install: agent-model hook: ' || fail "QA-13: update's output lacks the agent-model hook report:
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
# D-134: a beat on no matcher sees an MCP tool, and says nothing (the control); on
# a narrow one the same run names it, and the grep's "all wired" does not hide it.
echo "$out" | grep -qF 'reports no browser tool call' && fail "QA-3: a beat on no matcher was called narrow:
$out"
cp "$WORK/settings.json" "$WORK/settings.keep"
sed 's/"PostToolUse": \[{"hooks"/"PostToolUse": [{"matcher": "Edit|Write", "hooks"/' "$WORK/settings.keep" > "$WORK/settings.json"
out=$(inst 2>&1) || fail "QA-3: install on a narrow matcher failed:
$out"
echo "$out" | grep -qF 'matcher "Edit|Write", which selects no browser tool' || fail "QA-3: a narrow beat matcher was not named:
$out"
# Matchers that do select one: unanchored, one server only, an exact name, and a
# regex Python cannot read, which is not judged.
for m in '^mcp__' 'mcp__claude-in-chrome__.*' 'mcp__playwright__browser_navigate' '(?<x>mcp)__'; do
	sed "s/\"PostToolUse\": \\[{\"hooks\"/\"PostToolUse\": [{\"matcher\": \"$m\", \"hooks\"/" "$WORK/settings.keep" > "$WORK/settings.json"
	grep -qF "$m" "$WORK/settings.json" || fail "QA-3: the fixture did not take matcher $m"
	out=$(inst 2>&1) || fail "QA-3: install on matcher $m failed:
$out"
	echo "$out" | grep -qF 'reports no browser tool call' && fail "QA-3: matcher $m selects a browser tool and was called narrow:
$out"
done
# #71: the optional beats on PreToolUse and PostToolUseFailure. Absent, one line
# names both (the file above lacks them); on mcp__.* they are not named; on the
# gate's matcher they still are; with only the first wired, neither (it sees a
# failed call start); with only the second, the first.
cp "$WORK/settings.keep" "$WORK/settings.json"
out=$(inst 2>&1) || fail "#71: install failed:
$out"
echo "$out" | grep -qF 'no buddy beat on PreToolUse or PostToolUseFailure for browser tools, so `who` misses a browser call in flight or that failed' || fail "#71: the missing beats were not named:
$out"
for c in 'mcp__.*,mcp__.*,' 'Edit|Write,Edit|Write,PreToolUse or PostToolUseFailure' 'mcp__.*,,' 'Bash,mcp__.*,PreToolUse for'; do
	python3 -I -c 'import json, sys
p, pre, fail = sys.argv[1], sys.argv[2], sys.argv[3]
s = json.load(open(p))
line = {"command": "[ -x \"$HOME/bin/buddy\" ] \u0026\u0026 \"$HOME/bin/buddy\" beat 2>/dev/null; exit 0"}
s["hooks"]["PreToolUse"].append({"matcher": pre, "hooks": [line]})
if fail:
    s["hooks"]["PostToolUseFailure"] = [{"matcher": fail, "hooks": [line]}]
json.dump(s, open(p, "w"))' "$WORK/settings.json" "$(echo "$c" | cut -d, -f1)" "$(echo "$c" | cut -d, -f2)"
	want=$(echo "$c" | cut -d, -f3)
	out=$(inst 2>&1) || fail "#71: install on $c failed:
$out"
	if [ -z "$want" ]; then
		echo "$out" | grep -qF 'no buddy beat on' && fail "#71: beats on mcp__.* were named missing:
$out"
	else
		echo "$out" | grep -qF "no buddy beat on $want" || fail "#71: case $c did not name $want:
$out"
	fi
	cp "$WORK/settings.keep" "$WORK/settings.json"
done
mv "$WORK/settings.keep" "$WORK/settings.json"

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
echo "$out" | grep -qxF 'install: chat: off (BUDDY_CHAT=off on this run; kept by every later install until BUDDY_CHAT says otherwise)' || fail "QA-7: the chat line is missing:
$out"
echo "$out" | grep -qF 'left alone' && fail "QA-7: nothing was left alone in an empty bin dir:
$out"
echo "$out" | grep -qF 'daemon: skipped (chat off: claims-only install)' || fail "QA-7: the daemon step must say it was skipped for claims-only:
$out"
missing=$(echo "$out" | sed -n 's/.*NOT wired in [^:]*: *\(.*\) (README.*/\1/p')
[ "$missing" = "buddy busy" ] || fail "QA-7: hook report named '$missing', want 'buddy busy'"
echo "$out" | tail -1 | grep -qx 'install: done' || fail "QA-7: no done line"
# The fold: spaces and case around `off` are the same knob; anything else is the
# default, which the all-but-buddylist assertions above would then fail on.
out=$(inst_claims BUDDY_CHAT=' OFF ' 2>&1) || fail "QA-7: ' OFF ' install failed:
$out"
echo "$out" | grep -qF 'install: chat: off (BUDDY_CHAT=off on this run;' || fail "QA-7: ' OFF ' must fold to off:
$out"
[ ! -e "$WORK/bin7/buddylist" ] || fail "QA-7: ' OFF ' built buddylist"
# Anything else is REFUSED before a build (D-124): read as the default, a typo
# built chat on a claims-only machine, the silent failure this closed. The
# ' OFF ' run just above, to its done line, is the control.
out=$(inst_claims BUDDY_CHAT=no 2>&1) && fail "QA-7: BUDDY_CHAT=no must be refused:
$out"
echo "$out" | grep -qxF "install: FAILED — BUDDY_CHAT must be off or on, or unset to keep what $WORK/bin7/buddy records, not 'no'" || fail "QA-7: the refusal must name the two words:
$out"
echo "$out" | grep -q '^install: built ' && fail "QA-7: a refused BUDDY_CHAT built something:
$out"
[ ! -e "$WORK/bin7/buddylist" ] || fail "QA-7: a refused BUDDY_CHAT built buddylist"
# ... on ONE line: a line break in the value is shown as ?, never printed.
out=$(inst_claims BUDDY_CHAT="bad
install: done" 2>&1) && fail "QA-7: a two-line BUDDY_CHAT must be refused:
$out"
echo "$out" | grep -qxF "install: FAILED — BUDDY_CHAT must be off or on, or unset to keep what $WORK/bin7/buddy records, not 'bad?install: done'" ||
	fail "QA-7: the refused value must be shown on one line:
$out"
echo "$out" | grep -qx 'install: done' && fail "QA-7: the refused value printed a forged done line:
$out"

# QA-8: the remnants report. A stale buddylist in the bin dir (a marker file,
# so cmp can prove it was not rebuilt), a plist at BUDDY_PLIST, a .claude.json and a checkout .mcp.json
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
echo "$out" | grep -qF 'daemon: skipped (chat off: claims-only install)' || fail "QA-8: the daemon step must be skipped with a plist present:
$out"
echo "$out" | tail -1 | grep -qx 'install: done' || fail "QA-8: no done line"
[ ! -e "$GUARD/reached" ] || fail "QA-8: the REAL launchctl was reached: $(cat "$GUARD/reached")"

# QA-15 (D-124): the posture is KEPT. A claims-only install, then a re-run and
# a `buddy update` with BUDDY_CHAT unset, build no buddylist: the operator's
# laptop, where update from a terminal (no settings env there) built chat
# again and fetched its modules. "Never built" is read from a recording `go`
# in front of the real one, which logs every build's package, so the proof
# does not rest on the module cache being empty (a warm cache builds
# buddylist without fetching anything); GOPROXY=off besides, so a fetch could
# only fail. The recorder's control is the switch back (BUDDY_CHAT=on), whose
# log names ./cmd/buddylist. The first install runs under GOFLAGS=-trimpath,
# which drops -ldflags from build info unless install.sh overrides it (the
# cross-check; measured): a stamp it cannot read would put chat back.
# Every run is under env -u BUDDY_CHAT, so a caller's own value cannot decide
# a leg. Mutants: the carry-forward dropped (unset is on), -trimpath=false
# dropped, the chat -X moved after the checkout's; each kills a line below.
REALGO=$(command -v go)
mkdir -p "$WORK/recgo" "$WORK/bin15"
printf '#!/bin/sh\n[ "$1" != build ] || echo "$*" >> "%s/builds15"\nexec %s "$@"\n' "$WORK" "$(shq "$REALGO")" > "$WORK/recgo/go"
chmod 755 "$WORK/recgo/go"
cp "$WORK/settings.orig" "$WORK/settings15.json"
inst15() {
	rm -f "$WORK/builds15"
	env -u BUDDY_CHAT PATH="$WORK/recgo:$PATH" GOPROXY=off \
		BUDDY_BIN_DIR="$WORK/bin15" BUDDY_SKILL_DIR="$WORK/skill15" BUDDY_SETTINGS="$WORK/settings15.json" \
		BUDDY_SETUP_CLONE=off BUDDY_REPO_BIN=off BUDDY_LAUNCHCTL="$GUARD/launchctl" \
		BUDDY_PLIST="$WORK/plist15" BUDDY_CLAUDE_JSON="$WORK/claude15.json" BUDDY_MCP_JSON="$WORK/mcp15.json" \
		"$@"
}
built15() { [ -f "$WORK/builds15" ] && grep -q "\./cmd/$1\$" "$WORK/builds15"; }
# (a) Claims-only, explicitly, under GOFLAGS=-trimpath.
out=$(inst15 GOFLAGS=-trimpath BUDDY_CHAT=off sh scripts/install.sh 2>&1) || fail "QA-15a: claims-only install failed:
$out"
built15 buddy || fail "QA-15a: the recorder saw no buddy build, so it records nothing: $(cat "$WORK/builds15" 2>/dev/null)"
built15 buddylist && fail "QA-15a: claims-only built buddylist"
go version -m "$WORK/bin15/buddy" > "$WORK/vm15" 2>&1 || fail "QA-15a: go version -m failed: $(cat "$WORK/vm15")"
grep -qF -e "-ldflags=\"-X 'github.com/JsizzleR/buddy-system/internal/cli.installedChat=off'" "$WORK/vm15" ||
	fail "QA-15a: the installed buddy does not carry the posture first in its flags:
$(cat "$WORK/vm15")"
# Settings that do not say BUDDY_CHAT: hello obeys the stamp (D-127), so no
# note; (b) is the control, settings saying on.
echo "$out" | grep -qF "install: chat: NOTE — this install is claims-only" &&
	fail "QA-15a: a claims-only install beside settings that do not declare BUDDY_CHAT is noted, but hello obeys the stamp (D-127):
$out"
# (b) Re-run, BUDDY_CHAT unset: kept, said before anything is built; settings
# now declaring on, which outranks the stamp, is noted (D-127).
printf '{"env": {"BUDDY_CHAT": " On "}}\n' > "$WORK/settings15.json"
out=$(inst15 sh scripts/install.sh 2>&1) || fail "QA-15b: re-run failed:
$out"
echo "$out" | grep -qxF "install: chat: NOTE — this install is claims-only, but $WORK/settings15.json declares BUDDY_CHAT=on to every session, which outranks the install's stamp, so every session's hello still prints the chat hint; remove it from settings (D-127)" ||
	fail "QA-15b: settings declaring BUDDY_CHAT=on beside a claims-only install are not noted:
$out"
first=$(echo "$out" | sed -n '2p')
[ "$first" = "install: chat: off (kept: $WORK/bin15/buddy was installed with chat off; BUDDY_CHAT=on on a run changes it)" ] ||
	fail "QA-15b: the second line must be the kept posture, before any build:
$out"
built15 buddy || fail "QA-15b: the recorder saw no buddy build, so the next line tests nothing: $(cat "$WORK/builds15" 2>/dev/null)"
built15 buddylist && fail "QA-15b: a re-run with BUDDY_CHAT unset built buddylist: $(cat "$WORK/builds15")"
[ ! -e "$WORK/bin15/buddylist" ] || fail "QA-15b: a buddylist appeared"
echo "$out" | tail -1 | grep -qx 'install: done' || fail "QA-15b: no done line"
case $ROOT in
*\'*\"* | *\"*\'*) echo "check-install: QA-15c NOT RUN — this checkout's path holds both quote characters, which install.sh cannot stamp" >&2 ;;
*)
# (c) buddy update, the operator's path: the dry run names the posture, and the
# real run builds no buddylist.
out=$(cd "$WORK" && inst15 "$WORK/bin15/buddy" update --dry-run </dev/null 2>&1) || fail "QA-15c: update --dry-run failed:
$out"
echo "$out" | grep -qxF "  chat       off (kept: the installed buddy was installed with chat off; BUDDY_CHAT=on on a run changes it)" ||
	fail "QA-15c: the dry run must name the kept posture:
$out"
out=$(cd "$WORK" && inst15 "$WORK/bin15/buddy" update </dev/null 2>&1) || fail "QA-15c: update failed:
$out"
echo "$out" | tail -1 | grep -qx 'install: done' || fail "QA-15c: update did not run install.sh to its done line:
$out"
built15 buddy || fail "QA-15c: update built no buddy, so the leg tested nothing: $(cat "$WORK/builds15" 2>/dev/null)"
built15 buddylist && fail "QA-15c: buddy update on a claims-only install built buddylist: $(cat "$WORK/builds15")"
[ ! -e "$WORK/bin15/buddylist" ] || fail "QA-15c: a buddylist appeared"
;;
esac
# (d) The switch back: said as a change, and buddylist IS built (the
# recorder's control); the settings note now points the other way.
printf '{"env": {"BUDDY_CHAT": "off"}}\n' > "$WORK/settings15.json"
out=$(inst15 BUDDY_CHAT=on sh scripts/install.sh 2>&1) || fail "QA-15d: BUDDY_CHAT=on install failed:
$out"
echo "$out" | grep -qxF "install: chat: CHANGED from off to on by BUDDY_CHAT=on on this run; every later install keeps on until BUDDY_CHAT says otherwise" ||
	fail "QA-15d: the switch must say CHANGED:
$out"
built15 buddylist || fail "QA-15d: chat on built no buddylist: $(cat "$WORK/builds15" 2>/dev/null)"
[ -x "$WORK/bin15/buddylist" ] || fail "QA-15d: buddylist missing"
echo "$out" | grep -qxF "install: chat: NOTE — $WORK/settings15.json declares BUDDY_CHAT=off to every session (hello prints no chat hint), but this install is chat on; to make it claims-only: BUDDY_CHAT=off on a run" ||
	fail "QA-15d: settings that say off beside a chat install are noted:
$out"
# (e) ... and kept on by the next unset run, which rebuilds buddylist.
out=$(inst15 sh scripts/install.sh 2>&1) || fail "QA-15e: re-run failed:
$out"
echo "$out" | grep -qxF "install: chat: on (kept: $WORK/bin15/buddy was installed with chat on; BUDDY_CHAT=off on a run changes it)" ||
	fail "QA-15e: on is kept:
$out"
built15 buddylist || fail "QA-15e: kept on, buddylist must be rebuilt"
[ ! -e "$GUARD/reached" ] || fail "QA-15: the REAL launchctl was reached: $(cat "$GUARD/reached")"

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

# QA-14 (D-120): the Agent-model hook's copy and wiring, reported, never
# installed. Under a throwaway HOME whose bin is the bin dir, so the wiring's
# "$HOME/bin/agent-model-gate.py" names the fixture's copy; the fake go keeps
# each run from compiling. Arm (b), a current copy wired by the operator's
# entry, is the control for every refusal: the copy that DIFFERS by one
# trailing newline, the matchers that do not select Agent, and the rest. Every
# run exits 0 to its done line (drift fails nothing), leaves the settings file
# byte-identical, and writes no __pycache__ into the checkout.
H14="$WORK/home14" A14="scripts/hooks/agent-model-gate.py"
D14="$H14/bin/agent-model-gate.py"
mkdir -p "$H14/bin"
stubs "$WORK/stubs14" 'exit 0' "$OKLIST"
WHY14="optional, the operator's opt-in: docs/CHEAPER-MODELS-PLAN.md 1.3, D-117"
# amg <settings-file> [VAR=value ...]: sets got to the run's agent-model lines, after
# checking it exited 0, said done and left the settings file as it was.
amg() {
	s14=$1; shift
	[ ! -f "$s14" ] || cp "$s14" "$WORK/s14.orig"
	o14=$(inst9 "$s14" HOME="$H14" BUDDY_BIN_DIR="$H14/bin" PATH="$WORK/fakego:$PATH" FAKE_STUBS="$WORK/stubs14" "$@" 2>&1) ||
		fail "QA-14: install failed over $s14:
$o14"
	echo "$o14" | tail -1 | grep -qx 'install: done' || fail "QA-14: no done line:
$o14"
	[ ! -f "$s14" ] || cmp -s "$s14" "$WORK/s14.orig" || fail "QA-14: the settings file was touched"
	got=$(echo "$o14" | grep '^install: agent-model hook: ' || true)
}
# Not called in $(...): fail there would end only the subshell.
wire14() { # wire14 <file> <matcher JSON> <command JSON> [more entries JSON]
	printf '{"hooks": {"PreToolUse": [{"matcher": %s, "hooks": [{"type": "command", "command": %s, "timeout": 10}]}%s]}}\n' "$2" "$3" "${4:+, $4}" > "$1"
}
CMD14='"python3 -I \"$HOME/bin/agent-model-gate.py\""'
wire14 "$WORK/s14wired.json" '"Agent|Task"' "$CMD14"
want_l() { [ "$1" = "$2" ] || fail "QA-14: $3:
got:  $1
want: $2"; }

# (a) Neither installed nor wired, no settings file: ONE line, optional.
amg "$WORK/s14absent.json"
want_l "$got" "install: agent-model hook: not installed at $D14 and not wired in $WORK/s14absent.json ($WHY14)" "missing + settings absent is one line"
[ ! -e "$D14" ] || fail "QA-14a: install.sh installed the hook copy"
[ "$(ls -A "$H14/bin" | sort | tr '\n' ' ')" = "buddy buddylist " ] || fail "QA-14a: install.sh put more than its binaries in the bin dir: $(ls -A "$H14/bin")"
# (b) The control: a current copy under the operator's own entry.
cp "$A14" "$D14"
amg "$WORK/s14wired.json"
want_l "$got" "install: agent-model hook: $D14 is current (byte-identical to this checkout's scripts/hooks/agent-model-gate.py)
install: agent-model hook: wired in $WORK/s14wired.json: matcher \"Agent|Task\" runs $D14 (the copy above)" "current + wired (the control)"
# (c) One trailing newline DIFFERS, naming the remedy; the remedy, run, makes
# it current again (and is the command the line printed, whole).
printf '\n' >> "$D14"
amg "$WORK/s14wired.json"
line=$(echo "$got" | head -1)
want_l "$line" "install: agent-model hook: $D14 DIFFERS from this checkout's scripts/hooks/agent-model-gate.py, and a hook that runs it runs those bytes; update it: cp $(shq "$ROOT/$A14") $(shq "$D14")" "a trailing newline differs"
eval "${line##*update it: }"
cmp -s "$A14" "$D14" || fail "QA-14c: the printed remedy did not make the copy current"
# (d) Not wired: a matcher that does not select Agent, under the same command.
for m in '"Bash"' '"NotAgent"' '"Task"'; do
	wire14 "$WORK/s14m.json" "$m" "$CMD14"
	amg "$WORK/s14m.json"; got=$(echo "$got" | tail -1)
	want_l "$got" "install: agent-model hook: NOT wired in $WORK/s14m.json: the PreToolUse entry naming agent-model-gate.py has matcher $m, which does not select Agent" "matcher $m is not wired"
done
# ... a regular expression, and an entry the whole file disables.
wire14 "$WORK/s14m.json" '"^Agent$"' "$CMD14"
amg "$WORK/s14m.json"; got=$(echo "$got" | tail -1)
want_l "$got" "install: agent-model hook: custom PreToolUse entry naming agent-model-gate.py in $WORK/s14m.json (matcher \"^Agent$\" not read as an exact list); not verified" "a regex matcher is not verified"
sed 's/^{/{"disableAllHooks": true, /' "$WORK/s14wired.json" > "$WORK/s14m.json"
amg "$WORK/s14m.json"; got=$(echo "$got" | tail -1)
want_l "$got" "install: agent-model hook: NOT in effect in $WORK/s14m.json: disableAllHooks is true" "disableAllHooks"
# ... and no entry at all, beside a current copy: two lines, not one.
printf '{"hooks": {}}\n' > "$WORK/s14m.json"
amg "$WORK/s14m.json"
want_l "$got" "install: agent-model hook: $D14 is current (byte-identical to this checkout's scripts/hooks/agent-model-gate.py)
install: agent-model hook: not wired in $WORK/s14m.json" "installed, not wired"
# (e) Unparseable settings: said, and nothing fails.
printf '{"hooks": \n' > "$WORK/s14bad.json"
amg "$WORK/s14bad.json"; got=$(echo "$got" | tail -1)
want_l "$got" "install: agent-model hook: wiring not checked: $WORK/s14bad.json is not JSON this report can read (JSONDecodeError)" "unparseable settings"
amg "$WORK/s14wired.json" BUDDY_PYTHON="$WORK/no-python"; got=$(echo "$got" | tail -1)
want_l "$got" "install: agent-model hook: wiring not checked (no $WORK/no-python to decode $WORK/s14wired.json)" "no python"
# (f) Wired with no copy behind it: python3 exits 2 on a missing script, and
# exit 2 blocks, so this wiring refuses every Agent call; said loudly.
rm -f "$D14"
amg "$WORK/s14wired.json"
want_l "$got" "install: agent-model hook: not installed at $D14 ($WHY14; to install: cp $(shq "$ROOT/$A14") $(shq "$D14"))
install: agent-model hook: wired in $WORK/s14wired.json: matcher \"Agent|Task\" runs $D14 (the copy above; MISSING: python3 exits 2 on a missing script, and exit 2 BLOCKS every Agent call)" "wired, copy missing"
[ ! -e "$D14" ] || fail "QA-14f: install.sh installed the hook copy under a wired entry"
# (g) A symlink: to this checkout's script, current through it; to nothing,
# replace the link (cp would write through it).
ln -s "$ROOT/$A14" "$D14"
amg "$WORK/s14wired.json"; got=$(echo "$got" | head -1)
want_l "$got" "install: agent-model hook: $D14 is current (byte-identical to this checkout's scripts/hooks/agent-model-gate.py), through a symlink to $ROOT/$A14" "a symlink to the script"
rm -f "$D14"; ln -s "$WORK/nowhere.py" "$D14"
amg "$WORK/s14wired.json"; got=$(echo "$got" | head -1)
want_l "$got" "install: agent-model hook: $D14 is a symlink to nothing ($WORK/nowhere.py); replace the link: rm $(shq "$D14") && cp $(shq "$ROOT/$A14") $(shq "$D14")" "a dangling symlink"
printf 'other\n' > "$WORK/nowhere.py"
amg "$WORK/s14wired.json"; got=$(echo "$got" | head -1)
want_l "$got" "install: agent-model hook: $D14 DIFFERS from this checkout's scripts/hooks/agent-model-gate.py; it is a symlink to $WORK/nowhere.py, so replace the link, not what it names: rm $(shq "$D14") && cp $(shq "$ROOT/$A14") $(shq "$D14")" "a symlink to other bytes"
[ "$(cat "$WORK/nowhere.py")" = other ] || fail "QA-14g: install.sh wrote through the symlink"
rm -f "$D14"; cp "$A14" "$D14"
# (h) A second entry running a different path: both named, the other one
# compared too, so a current copy beside a stale wired one is not "current".
printf 'old\n' > "$WORK/other.py"
wire14 "$WORK/s14two.json" '"Agent"' "$CMD14" '{"matcher": "Agent", "hooks": [{"type": "command", "command": "python3 -I '"$WORK"'/agent-model-gate.py"}]}'
amg "$WORK/s14two.json"; got=$(echo "$got" | tail -1)
want_l "$got" "install: agent-model hook: wired in $WORK/s14two.json: matcher \"Agent\" runs $D14 (the copy above); matcher \"Agent\" runs $WORK/agent-model-gate.py (MISSING: python3 exits 2 on a missing script, and exit 2 BLOCKS every Agent call)" "a second entry's path"
printf 'old\n' > "$WORK/agent-model-gate.py"
amg "$WORK/s14two.json"; got=$(echo "$got" | tail -1)
echo "$got" | grep -qF "runs $WORK/agent-model-gate.py (DIFFERS from this checkout's scripts/hooks/agent-model-gate.py)" ||
	fail "QA-14h: a second wired path that differs is named:
$got"
# (i) The code pass's four: only `python3 [flags] <script>` alone has a known
# script. -c runs other code, a single-quoted $HOME is a literal path, and a
# trailing `; exit 0` makes a missing script exit 0, so each is not verified;
# a path through `link/..` is the file the kernel resolves, not the copy; and
# a custom entry beside a wired one is named, not dropped.
for c in '"python3 -I -c pass $HOME/bin/agent-model-gate.py"' '"python3 -I '"'"'$HOME/bin/agent-model-gate.py'"'"'"' \
	'"python3 -I \"$HOME/bin/agent-model-gate.py\"; exit 0"' '"python3 -I \"~/bin/agent-model-gate.py\""'; do
	wire14 "$WORK/s14m.json" '"Agent"' "$c"
	amg "$WORK/s14m.json"; got=$(echo "$got" | tail -1)
	want_l "$got" "install: agent-model hook: custom PreToolUse entry naming agent-model-gate.py in $WORK/s14m.json (its command is not python3 [flags] <script> alone); not verified" "command $c is not verified"
done
mkdir -p "$WORK/old14/sub" "$WORK/old14/bin"; ln -s "$WORK/old14/sub" "$H14/l14"
printf 'old\n' > "$WORK/old14/bin/agent-model-gate.py"
wire14 "$WORK/s14m.json" '"Agent"' '"python3 -I $HOME/l14/../bin/agent-model-gate.py"'
amg "$WORK/s14m.json"; got=$(echo "$got" | tail -1)
want_l "$got" "install: agent-model hook: wired in $WORK/s14m.json: matcher \"Agent\" runs $H14/l14/../bin/agent-model-gate.py (DIFFERS from this checkout's scripts/hooks/agent-model-gate.py)" "a path through link/.. is what the kernel resolves"
wire14 "$WORK/s14m.json" '"Agent"' "$CMD14" '{"matcher": "^Agent$", "hooks": [{"type": "command", "command": "python3 -I /nowhere/agent-model-gate.py"}]}'
amg "$WORK/s14m.json"; got=$(echo "$got" | tail -1)
want_l "$got" "install: agent-model hook: wired in $WORK/s14m.json: matcher \"Agent\" runs $D14 (the copy above); and custom, not verified: matcher \"^Agent$\" not read as an exact list" "a custom entry beside a wired one"
# QA-15f (after QA-14, whose fake go and stubs it uses): an unstamped buddy
# with no buddylist beside it (a claims-only install
# from before D-124) is NOTED, never acted on; one with a buddylist beside it
# is the control. QA-10's fake go builds, and its `version -m` reads nothing,
# so the stub buddy is unstamped. BUDDY_CHAT empty is unset.
note15="NOTE — $WORK/bin15f/buddy records no chat posture (built before D-124, by hand, or under -trimpath) and has no buddylist beside it"
old "$WORK/bin15f" buddy
out=$(inst9 "$WORK/settings.orig" BUDDY_CHAT= BUDDY_BIN_DIR="$WORK/bin15f" PATH="$WORK/fakego:$PATH" FAKE_STUBS="$WORK/stubs14" 2>&1) ||
	fail "QA-15f: install over an unstamped buddy failed:
$out"
echo "$out" | grep -qF "install: chat: $note15" || fail "QA-15f: the unstamped buddy without buddylist must be noted:
$out"
[ -e "$WORK/bin15f/buddylist" ] || fail "QA-15f: the default built no buddylist, so the control below has none beside it"
out=$(inst9 "$WORK/settings.orig" BUDDY_CHAT= BUDDY_BIN_DIR="$WORK/bin15f" PATH="$WORK/fakego:$PATH" FAKE_STUBS="$WORK/stubs14" 2>&1) ||
	fail "QA-15f: re-run failed:
$out"
echo "$out" | grep -qF "records no chat posture (built before" && fail "QA-15f: with buddylist now beside it (the control), no note:
$out"
# QA-16 (D-135): --wire-hooks. (a) The writer alone, on synthetic files: every
# refusal leaves the file byte-identical with nothing beside it, each beside
# its positive control (the same directory, a good file, wired); the splice
# keeps every byte it did not insert (CRLF, an escape, a number spelling) and
# appends after the existing entries, never into them; idempotent. (b)-(e)
# install.sh: unflagged changes nothing (no backup, no temp), flagged adds
# exactly what the report named and the report then says all wired, a legacy
# gate is reported and not "fixed", claims-only adds the six, a stray argument
# a preflight refusal and a bin dir not $HOME/bin install nothing. The fake go keeps each run from
# compiling. First: `wanted` is gate-hook.py's TOKENS, one source.
want16=$(python3 -I scripts/gate-hook.py tokens)
got16="$(sed -n 's/^wanted="\(.*\)"$/\1/p' scripts/install.sh) $(sed -n 's/^	wanted="\$wanted \(.*\)"$/\1/p' scripts/install.sh)"
[ "$got16" = "$want16" ] || fail "QA-16: install.sh's wanted ($got16) is not gate-hook.py's TOKENS ($want16)"
W16="$WORK/w16"
mkdir -p "$W16"
python3 -I -B - "$W16" "$ROOT/scripts/wire-hooks.py" <<'PY' || fail "QA-16a: the writer (above)"
import json, os, subprocess, sys
w, tool = sys.argv[1], sys.argv[2]
bad = []
def run(path, *toks, snap=None):
    """The writer as install.sh runs it: the snapshot is the file's bytes now."""
    if snap is None:
        snap = "-"
        if os.path.lexists(path):
            snap = os.path.join(w, "snapshot")
            with open(path, "rb") as f, open(snap, "wb") as g:
                g.write(f.read())
    return subprocess.run([sys.executable, "-I", "-B", tool, "wire", path, snap] + list(toks), capture_output=True, text=True)
def refused(name, content, why, toks=("buddy hello",), mode=None, setup=None):
    d = os.path.join(w, name)
    os.mkdir(d)
    p = os.path.join(d, "settings.json")
    if setup:
        setup(d, p)
    else:
        with open(p, "wb") as f:
            f.write(content)
    before = sorted(os.listdir(d))
    data = open(p, "rb").read()
    if mode is not None:
        os.chmod(d, mode)
    r = run(p, *toks)
    if mode is not None:
        os.chmod(d, 0o755)
    # One line: a name from the file (an event with a newline) is quoted.
    if r.returncode != 1 or "REFUSED, nothing written" not in r.stdout or why not in r.stdout or r.stdout.count("\n") != 1:
        bad.append("%s: rc=%d out=%r, want refused on one line naming %r" % (name, r.returncode, r.stdout, why))
    if open(p, "rb").read() != data or sorted(os.listdir(d)) != before:
        bad.append("%s: refused, but the directory changed: %r -> %r" % (name, before, sorted(os.listdir(d))))
    # The positive control: the same directory and tokens, a plain file, wired.
    q = os.path.join(d, "ok.json")
    with open(q, "w") as f:
        f.write("{}")
    r = run(q, "buddy hello")
    if r.returncode != 0 or "added buddy hello" not in r.stdout:
        bad.append("%s control: rc=%d out=%r" % (name, r.returncode, r.stdout))
def symlink(d, p):
    with open(os.path.join(d, "real.json"), "w") as f:
        f.write("{}")
    os.symlink("real.json", p)
def hardlink(d, p):
    with open(p, "w") as f:
        f.write("{}")
    os.link(p, os.path.join(d, "other.json"))
refused("dup", b'{"hooks": {}, "hooks": {}}', "duplicate key")
refused("nan", b'{"x": NaN}', "non-JSON constant")
refused("notjson", b'{"hooks": ', "not JSON")
refused("latin1", b'{"x": "\xe9"}', "not UTF-8")
refused("array", b'[]', "top level is not an object")
refused("hooksnull", b'{"hooks": null}', '"hooks" is not an object')
refused("eventobj", b'{"hooks": {"SessionStart": {}}}', "is not a list")
refused("eventnl", b'{"hooks": {"Stop\\ninstall: done\\n": {}}}', '"Stop\\ninstall: done\\n" is not a list')
refused("disabled", b'{"disableAllHooks": true}', "disableAllHooks")
refused("big", b'{}' + b' ' * (1 << 20), "bytes, over")
refused("bigafter", b'{}' + b' ' * ((1 << 20) - 100), "it would be")
refused("symlink", None, "a symlink", setup=symlink)
refused("hardlink", None, "hard links", setup=hardlink)
refused("badtoken", b'{}', "is not one the report names", toks=("buddy rm",))
refused("shape", b'{"hooks": {"PreToolUse": ["x"]}}', "not a hook object")
refused("cmdint", b'{"hooks": {"PreToolUse": [{"hooks": [{"command": 5}]}]}}', "not a hook object")
refused("deep", b'{"x": ' + b'[' * 500 + b']' * 500 + b'}', "a shape it cannot read")
if os.geteuid() != 0:
    refused("readonly", b'{}', "Permission denied", mode=0o555)
    # A file made read-only on purpose: a rename would replace it regardless (panel).
    refused("ro-file", b'{}', "not writable by its owner", setup=lambda d, p: (open(p, "w").write("{}"), os.chmod(p, 0o444)))
# The preflight (check) refuses what step 6 would: the post-splice size and a
# shape, so the install stops before step 1; a plain file is its control.
for name, body, why in (("pre-big", b'{}' + b' ' * ((1 << 20) - 100), "it would be"),
                        ("pre-shape", b'{"hooks": {"PreToolUse": ["x"]}}', "not a hook object"),
                        ("pre-ok", b'{}', None)):
    q = os.path.join(w, name + ".json")
    open(q, "wb").write(body)
    r = subprocess.run([sys.executable, "-I", "-B", tool, "check", q], capture_output=True, text=True)
    if (why is None and r.returncode != 0) or (why is not None and (r.returncode != 1 or why not in r.stdout)):
        bad.append("check %s: rc=%d out=%r" % (name, r.returncode, r.stdout))
# The gate's token outside any PreToolUse hook: counted present, so not added,
# said, and the wiring fails; the other lines are still written.
d = os.path.join(w, "gateless")
os.mkdir(d)
p = os.path.join(d, "settings.json")
open(p, "w").write('{"permissions": {"allow": ["Bash(/u/bin/buddy gate)"]}}')
r = run(p, "buddy hello")
if r.returncode != 1 or "did NOT add the gate" not in r.stdout or "added buddy hello" not in r.stdout:
    bad.append("gate token in a permissions rule: rc=%d out=%r" % (r.returncode, r.stdout))
r = run(p)
if r.returncode != 1 or "did NOT add the gate" not in r.stdout:
    bad.append("gate token in a permissions rule, nothing else to add: rc=%d out=%r" % (r.returncode, r.stdout))
# The browser reading skips an entry it cannot read instead of printing nothing.
q = os.path.join(w, "odd-beat.json")
open(q, "w").write('{"hooks": {"PostToolUse": [{"matcher": "Edit", "hooks": [{"command": "\\"$HOME/bin/buddy\\" beat"}]}], "PreToolUse": [1]}}')
r = subprocess.run([sys.executable, "-I", "-B", os.path.join(os.path.dirname(tool), "gate-hook.py"), "browser-report", q], capture_output=True, text=True)
if 'runs buddy beat on matcher "Edit"' not in r.stdout:
    bad.append("browser-report beside an unreadable entry: %r" % r.stdout)
# ...and keeps its first line when a later event cannot be read at all, as the
# heredoc it replaced printed it before failing (Codex code pass).
open(q, "w").write('{"hooks": {"PostToolUse": [{"matcher": "Edit", "hooks": [{"command": "buddy beat"}]}], "PreToolUse": 1}}')
r = subprocess.run([sys.executable, "-I", "-B", os.path.join(os.path.dirname(tool), "gate-hook.py"), "browser-report", q], capture_output=True, text=True)
if r.stdout.count('runs buddy beat on matcher "Edit"') != 1 or r.stdout.count("\n") != 1:
    bad.append("browser-report beside an unreadable event: %r" % r.stdout)
# The report's snapshot must be the file's bytes now, absent included.
d = os.path.join(w, "moved")
os.mkdir(d)
p = os.path.join(d, "settings.json")
open(p, "w").write("{}")
open(os.path.join(w, "older"), "w").write('{"a": 1}')
r = run(p, "buddy hello", snap=os.path.join(w, "older"))
if r.returncode != 1 or "changed after the report read it" not in r.stdout or open(p).read() != "{}":
    bad.append("snapshot differs: rc=%d out=%r" % (r.returncode, r.stdout))
r = run(p, "buddy hello", snap="-")
if r.returncode != 1 or "created after the report read it" not in r.stdout or open(p).read() != "{}":
    bad.append("snapshot absent, file present: rc=%d out=%r" % (r.returncode, r.stdout))
# Absent file: created 0600 in an existing directory; refused in a missing one.
p = os.path.join(w, "new.json")
r = run(p, "buddy hello")
if r.returncode != 0 or os.stat(p).st_mode & 0o777 != 0o600 or json.load(open(p))["hooks"]["SessionStart"][0]["hooks"][0]["command"] != \
        '[ -x "$HOME/bin/buddy" ] && "$HOME/bin/buddy" hello 2>/dev/null; exit 0':
    bad.append("absent file: rc=%d out=%r" % (r.returncode, r.stdout))
r = run(os.path.join(w, "nodir", "s.json"), "buddy hello")
if r.returncode != 1 or "does not exist" not in r.stdout:
    bad.append("absent directory: rc=%d out=%r" % (r.returncode, r.stdout))
# The splice: every old byte kept, in order; the new entry after the old one;
# the mode kept; a 0600 backup holding the old bytes, named in the output.
d = os.path.join(w, "splice")
os.mkdir(d)
p = os.path.join(d, "settings.json")
old = (b'{\r\n  "env": {"A": "\\u0061", "n": 1e0},\r\n  "hooks": {\r\n'
       b'    "Stop": [ {"hooks": [{"type": "command", "command": "mine"}]} ],\r\n    "PostToolUse": []\r\n  }\r\n}\r\n')
open(p, "wb").write(old)
os.chmod(p, 0o640)
r = run(p, "buddy idle", "buddy beat", "buddy hello")
new = open(p, "rb").read()
it = iter(new)
if r.returncode != 0 or not all(b in it for b in old):
    bad.append("splice lost an old byte: rc=%d out=%r\n%r" % (r.returncode, r.stdout, new))
doc = json.loads(new)
if doc["hooks"]["Stop"][0]["hooks"][0]["command"] != "mine" or len(doc["hooks"]["Stop"]) != 2 \
        or doc["env"] != {"A": "a", "n": 1.0} or os.stat(p).st_mode & 0o777 != 0o640 \
        or len(doc["hooks"]["PostToolUse"]) != 1 or "SessionStart" not in doc["hooks"]:
    bad.append("splice result: %r" % doc)
backups = [n for n in os.listdir(d) if ".buddy-backup-" in n]
if len(backups) != 1 or open(os.path.join(d, backups[0]), "rb").read() != old or \
        os.stat(os.path.join(d, backups[0])).st_mode & 0o777 != 0o600 or backups[0] not in r.stdout:
    bad.append("backup: %r %r" % (backups, r.stdout))
if sorted(os.listdir(d)) != sorted(["settings.json"] + backups):
    bad.append("left beside it: %r" % os.listdir(d))
# Idempotent: no token (the report names none once wired) and the browser pair
# present: nothing added, nothing written, no second backup.
r = run(p)
if r.returncode != 0 or "nothing to add" not in r.stdout or open(p, "rb").read() != new or len(os.listdir(d)) != 2:
    bad.append("second run: rc=%d out=%r" % (r.returncode, r.stdout))
# The splice's verification refuses a wrong splice (mutation check: nothing
# else reaches it), one test per condition: an inserted byte changed (only the
# decode differs) and an old byte changed (only the un-splice differs), each
# refused with the file and its directory as they were; the control is above.
import contextlib, importlib.util, io, signal
spec = importlib.util.spec_from_file_location("wire_hooks", tool)
wh = importlib.util.module_from_spec(spec)
spec.loader.exec_module(wh)
real = wh.splice
def wire_in(name, body="{\"env\": {}}", toks=("buddy hello",)):
    d = os.path.join(w, name)
    os.mkdir(d)
    p = os.path.join(d, "settings.json")
    snap = os.path.join(w, "snap-" + name)
    if body is not None:
        open(p, "w").write(body)
        open(snap, "w").write(body)
    out = io.StringIO()
    with contextlib.redirect_stdout(out):
        try:
            rc = wh.main(["wire", p, snap if body is not None else "-"] + list(toks))
        except SystemExit as e:
            rc = e.code
    return d, p, rc, out.getvalue()
for name, bend in (("bad-inserted", lambda n: n.replace("hello", "hellp", 1)),
                   ("bad-old", lambda n: n.replace('"env": ', '"env":  ', 1))):
    wh.splice = lambda text, adds, bend=bend: (lambda n, ins: (bend(n), ins))(*real(text, adds))
    d, p, rc, out = wire_in(name)
    if rc != 1 or "did not verify" not in out or open(p).read() != '{"env": {}}' or os.listdir(d) != ["settings.json"]:
        bad.append("an injected bad splice (%s): rc=%r out=%r left %r" % (name, rc, out, os.listdir(d)))
wh.splice = real
# A TERM between the temp made and its name kept (once stranding the temp and
# removing the backup, Codex code pass) is held: the file is written, the
# backup kept, no temp left, and the run ends after, with TERM's status.
# INT likewise, ending with its status, not a traceback (Fable).
real_new = wh._write_new
for sig in (signal.SIGTERM, signal.SIGINT):
    def sig_new(*a, sig=sig):
        t = real_new(*a)
        os.kill(os.getpid(), sig)
        return t
    wh._write_new = sig_new
    try:
        d, p, rc, out = wire_in("sig%d" % sig)
    except KeyboardInterrupt:
        rc = "KeyboardInterrupt"
    wh._write_new = real_new
    left = sorted(os.listdir(d))
    if rc != 128 + sig or "SessionStart" not in open(p).read() or len(left) != 2 or not any(".buddy-backup-" in n for n in left):
        bad.append("signal %d mid-write: rc=%r left %r" % (sig, rc, left))
    signal.signal(sig, signal.SIG_DFL if sig != signal.SIGINT else signal.default_int_handler)
# A new file published, then its temp's unlink failing, is said as WROTE and
# fails, never "nothing written" (Codex code pass); the control is "absent
# file" above.
real_unlink = os.unlink
def no_unlink(q, *a, **k):
    if ".buddy-wire-" in os.path.basename(q):
        raise PermissionError(13, "Permission denied", q)
    return real_unlink(q, *a, **k)
os.unlink = no_unlink
try:
    d, p, rc, out = wire_in("leftover", body=None)
finally:
    os.unlink = real_unlink
if rc != 1 or "REFUSED" in out or "could not be removed" not in out or "SessionStart" not in open(p).read():
    bad.append("temp unlink failing after the link: rc=%r out=%r" % (rc, out))
for b in bad:
    print("QA-16a: " + b)
sys.exit(1 if bad else 0)
PY
# (b) install.sh. inst16 <settings> [args...]: the fake go, stubs that pass
# every probe, and $E16's words in the environment (env, not a prefix on the
# function call: unspecified in POSIX sh).
stubs "$WORK/stubs16" 'exit 0' "$OKLIST"
E16="" B16="$W16/bin"
inst16() {
	s16=$1; shift
	env HOME="${B16%/bin}" $E16 BUDDY_BIN_DIR="$B16" BUDDY_SKILL_DIR="$WORK/skill16" BUDDY_SETTINGS="$s16" \
		BUDDY_DAEMON=off BUDDY_SETUP_CLONE=off BUDDY_REPO_BIN=off BUDDY_MCP_JSON="$WORK/mcp.json" \
		PATH="$WORK/fakego:$PATH" FAKE_STUBS="$WORK/stubs16" sh scripts/install.sh "$@"
}
mkdir -p "$W16/b"
cp "$WORK/settings.orig" "$W16/b/settings.json"
# Unflagged: nothing written, nothing beside the file (the opt-in is the flag).
out=$(inst16 "$W16/b/settings.json" 2>&1) || fail "QA-16b: the unflagged install failed:
$out"
cmp -s "$W16/b/settings.json" "$WORK/settings.orig" || fail "QA-16b: the unflagged install changed the settings file"
[ "$(ls -A "$W16/b")" = settings.json ] || fail "QA-16b: the unflagged install left a file beside settings: $(ls -A "$W16/b")"
named=$(echo "$out" | sed -n 's/^install: hooks: NOT wired in [^:]*:\(.*\) (README.*/\1/p')
[ "$named" = " buddy busy buddylist presence" ] || fail "QA-16b: the fixture's report named '$named'"
if echo "$out" | grep -q 'hooks: --wire-hooks'; then fail "QA-16b: the unflagged install mentioned --wire-hooks:
$out"; fi
# Flagged: exactly what the report named, plus the browser pair, then all wired.
out=$(inst16 "$W16/b/settings.json" --wire-hooks 2>&1) || fail "QA-16b: the flagged install failed:
$out"
added=$(echo "$out" | sed -n 's/^install: hooks: --wire-hooks added \(.*\) on \([A-Za-z]*\).*/\1@\2/p' | tr '\n' ' ')
[ "$added" = "buddy busy@UserPromptSubmit buddylist presence@SessionEnd buddy beat (browser tools, #71)@PreToolUse buddy beat (browser tools, #71)@PostToolUseFailure " ] ||
	fail "QA-16b: added '$added':
$out"
echo "$out" | grep -qx "install: hooks: all eight wired in $W16/b/settings.json" || fail "QA-16b: the report after wiring:
$out"
python3 -I - "$WORK/settings.orig" "$W16/b/settings.json" <<'PY' || fail "QA-16b: an existing entry changed (above)"
import json, sys
a, b = (json.load(open(p)) for p in sys.argv[1:])
bad = [ev for ev, es in a["hooks"].items() if b["hooks"][ev][:len(es)] != es]
if bad or set(b) != set(a):
    print("QA-16b: existing entries or top-level keys changed: %r" % bad)
    sys.exit(1)
PY
cp "$W16/b/settings.json" "$W16/b.after"
out=$(inst16 "$W16/b/settings.json" --wire-hooks 2>&1) || fail "QA-16b: the second flagged install failed:
$out"
{ echo "$out" | grep -q 'nothing to add'; } && cmp -s "$W16/b/settings.json" "$W16/b.after" || fail "QA-16b: not idempotent:
$out"
# A legacy gate is present-but-different: reported, never replaced or doubled.
cp "$WORK/s9legacy.json" "$W16/legacy.json"
out=$(inst16 "$W16/legacy.json" --wire-hooks 2>&1) || fail "QA-16c: the legacy install failed:
$out"
if echo "$out" | grep -q 'added buddy gate'; then fail "QA-16c: the legacy gate was doubled:
$out"; fi
echo "$out" | grep -q '^install: gate: LEGACY fail-open command' || fail "QA-16c: the legacy gate is no longer reported:
$out"
python3 -I -c 'import json,sys; sys.exit(0 if json.load(open(sys.argv[1]))["hooks"]["PreToolUse"][0] == json.load(open(sys.argv[2]))["hooks"]["PreToolUse"][0] else 1)' \
	"$W16/legacy.json" "$WORK/s9legacy.orig" || fail "QA-16c: the legacy entry was rewritten"
# A stray argument is refused before anything runs, naming it.
echo '{}' > "$W16/off.json"
if out=$(inst16 "$W16/off.json" --wire-hooks --chat=off 2>&1); then fail "QA-16d: a stray argument ran:
$out"; fi
echo "$out" | grep -q "unknown argument '--chat=off'" || fail "QA-16d: the stray argument was not named:
$out"
cmp -s "$W16/off.json" - <<'EOF' || fail "QA-16d: a refused argument wrote the settings"
{}
EOF
# Claims-only: the six buddy lines and the browser pair, never a buddylist line.
E16="BUDDY_CHAT=off"
out=$(inst16 "$W16/off.json" --wire-hooks 2>&1) || fail "QA-16d: the claims-only install failed:
$out"
E16=""
if echo "$out" | grep -q 'added buddylist'; then fail "QA-16d: claims-only added a buddylist line:
$out"; fi
[ "$(echo "$out" | grep -c '^install: hooks: --wire-hooks added ')" = 8 ] || fail "QA-16d: claims-only did not add the six and the pair:
$out"
# A preflight refusal installs nothing: a fresh bin dir stays empty. Unparseable
# settings, then no python; the unflagged install over the same file is the
# control that something would have been built.
printf '{"hooks": ' > "$W16/broken.json"
cp "$W16/broken.json" "$W16/broken.orig"
B16="$W16/e/bin"
mkdir -p "$W16/e"
if out=$(inst16 "$W16/broken.json" --wire-hooks 2>&1); then fail "QA-16e: wiring an unparseable file passed:
$out"; fi
echo "$out" | grep -q 'REFUSED, nothing written.*not JSON.*nothing installed' || fail "QA-16e: the refusal:
$out"
E16="BUDDY_PYTHON=/nonexistent/python3"
if out=$(inst16 "$W16/off.json" --wire-hooks 2>&1); then fail "QA-16e: no python passed:
$out"; fi
E16=""
echo "$out" | grep -q 'needs /nonexistent/python3' || fail "QA-16e: no python was not named:
$out"
[ ! -e "$B16/buddy" ] && cmp -s "$W16/broken.json" "$W16/broken.orig" || fail "QA-16e: a refused preflight built or wrote"
out=$(inst16 "$W16/broken.json" 2>&1) || fail "QA-16e: control, the unflagged install over the same file failed:
$out"
[ -e "$B16/buddy" ] || fail "QA-16e: control built nothing"
# QA-16f: a bin dir that is not $HOME/bin refuses the flag before step 1, as
# the lines it writes run $HOME/bin/buddy (Fable); every flagged run above,
# HOME its bin dir's parent, is the control.
B16="$W16/f/bin"
mkdir -p "$W16/f"
cp "$W16/b/settings.json" "$W16/f.json" 2>/dev/null || printf '{}' > "$W16/f.json"
cp "$W16/f.json" "$W16/f.orig"
E16="HOME=$W16/elsewhere"
if out=$(inst16 "$W16/f.json" --wire-hooks 2>&1); then fail "QA-16f: wiring beside another bin dir passed:
$out"; fi
E16=""
echo "$out" | grep -q 'BUDDY_BIN_DIR is .*would run nothing.*nothing installed' && [ ! -e "$B16/buddy" ] && cmp -s "$W16/f.json" "$W16/f.orig" ||
	fail "QA-16f: the refusal, or it built or wrote:
$out"
# ...and a "bin<LF>" beside an existing ~/bin, which $( ) once read as ~/bin.
mkdir -p "$W16/g/bin"
B16="$W16/g/bin
"
E16="HOME=$W16/g"
if out=$(inst16 "$W16/f.json" --wire-hooks 2>&1); then fail "QA-16f: wiring beside bin<LF> passed:
$out"; fi
E16=""
echo "$out" | grep -q 'would run nothing' && cmp -s "$W16/f.json" "$W16/f.orig" || fail "QA-16f: bin<LF>, the refusal or it wrote:
$out"
pyc_after=$([ -e scripts/__pycache__ ] && echo yes || echo no)
[ "$pyc_before" = yes ] || [ "$pyc_after" = no ] || fail "QA-14: the report wrote scripts/__pycache__ into the checkout"

echo "check-install: GREEN (QA-1 fresh install + exact hook report, QA-2 idempotent, QA-3 all-wired control, QA-3b handoff size report,"
echo "  QA-17 an older ledger elsewhere migrates only under the hooks' binary (\$HOME/bin/buddy) or BUDDY_MIGRATE=1, backed up first;"
echo "  QA-13 the installed buddy's update names this checkout's install.sh, a dry run runs nothing, and the real run installs,"
echo "  QA-4 unbuildable target fails loud, QA-5 a sub-step's failure is the install's,"
echo "  QA-6 the daemon waits past the old ten-poll window, says what launchd recorded, and fails last (also on a failed"
echo "  kickstart); fake launchctl, real one guarded; QA-7 BUDDY_CHAT=off builds buddy alone, six-line hook report, the fold, another value refused;"
echo "  QA-8 remnants named on one line and left byte-identical; QA-15 the posture is kept by a re-run and by buddy update"
echo "  (no buddylist build recorded, GOPROXY=off, under GOFLAGS=-trimpath), the dry run names it, BUDDY_CHAT=on switches back,"
echo "  an unstamped buddy without buddylist noted (and not with one);"
echo "  QA-9 the gate line judged from decoded JSON: legacy named with"
echo "  README's entry and the file untouched, custom and no-onFailure not verified, another event's line no gate, no python said;"
echo "  QA-10 a new buddy whose gate dies fails the install, naming the probe, and the old one stays; QA-11 the probe passes"
echo "  from a relative BUDDY_BIN_DIR and with a \" in TMPDIR, new builds renamed in; QA-12 a killed buddylist names itself,"
echo "  not the gate, and the old one stays; QA-14 the Agent-model hook's copy current/DIFFERS/missing/symlinked and its"
echo "  wiring decoded: wired, matchers that miss Agent, regex, disableAllHooks, a second path compared, a missing copy"
echo "  that blocks, settings absent/unparseable, no python; nothing installed, settings untouched, no __pycache__;"
echo "  QA-16 --wire-hooks: the writer's refusals each with its control, the splice keeps every old byte, a 0600 backup,"
echo "  idempotent; unflagged writes nothing, flagged adds exactly what the report named, a legacy gate untouched, claims-only"
echo "  adds six and the browser pair, a stray argument, a preflight refusal and a bin dir not \$HOME/bin install nothing)"
