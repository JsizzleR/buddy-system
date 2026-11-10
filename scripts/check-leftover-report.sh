#!/bin/sh
# check-leftover-report.sh — done-check for scripts/leftover-report.sh (#84).
#
# HERMETIC: sh, git, go (to build buddy and a sleeper), python3. The ledger is
# a fixture in a throwaway repository; the planted processes are this check's
# own, and it ends them (by the pids it recorded) on the way out. The census
# reads the machine's whole process table, so every assertion names a planted
# pid: what else is running decides nothing here.
#
# What must hold, each with the control that proves the assertion could fire:
#   - the pure arms (procargs parsing, attribution, folding, the rendered
#     rows) hold on ANY platform: the script's own --self-test, whose argc
#     control proves the argv walk is what keeps an argv lookalike out;
#   - a usage refusal exits 1 and ends `refused: <reason>` (D-098), --help
#     exits 0;
#   - the report signals nothing: no kill/signal call in either file, with a
#     planted copy that carries one as the control the scan fires on;
#   - off darwin: CANNOT SAY and exit 2, never an empty census;
#   on darwin, five orphans planted with `( cmd & )` and one child that is
#   not one:
#   - env, ended session: STALE, evidence env, the ended lane's label, TREE 2
#     (the plant's own child is counted, not listed);
#   - env, open session: OWNED; env naming an id in no ledger: UNKNOWN;
#   - a platform /bin/sh (darwin withholds its environment) whose cwd is in
#     the ended lane's linked worktree: STALE by worktree(1);
#   - no tie at all: UNKNOWN;
#   - the not-orphaned child of this shell is NOT listed (control: it is
#     alive when the report runs);
#   - never argv: a token in a plant's argv is absent from the report
#     (control: `ps` on that pid shows it);
#   - the ledger decides: the same census with no ledger turns the env STALE
#     into UNKNOWN "not in the ledgers read";
#   - --all, because past 50 rows the young UNKNOWN plants would sort out of
#     the capped listing (the cap itself is the self-test's);
#   - a ledger with a rollback journal beside it is "not read" (control:
#     the same ledger read without one);
#   - the source ledger is never written: the buddy.db* file set and bytes
#     are the same after the report as before.
set -eu
. "$(dirname -- "$0")/lib.sh"
ROOT=$(repo_root "$0")
CDPATH= cd -- "$ROOT"

fail() { echo "check-leftover-report: FAIL — $*" >&2; exit 1; }

if ! command -v python3 >/dev/null 2>&1; then
	echo "check-leftover-report: NOT RUN — no python3 on PATH" >&2
	exit 0
fi

python3 -I scripts/leftover-report.py --self-test >/dev/null || fail "the self-test failed (run it for the reason)"

rc=0
err=$(sh scripts/leftover-report.sh --bogus 2>&1 >/dev/null) || rc=$?
[ "$rc" = 1 ] && [ "$(printf '%s\n' "$err" | tail -n 1)" = "refused: unknown flag --bogus" ] ||
	fail "an unknown flag gave rc=$rc and '$err', want rc=1 ending 'refused: unknown flag --bogus'"
sh scripts/leftover-report.sh --help >/dev/null || fail "--help did not exit 0"

# THE NO-SIGNAL SCAN. A report that ends nothing (D-027) must not hold the
# means to. In the python, by token, so the docstrings that say what it does
# NOT do are not hits and a call respelled across lines still is: any name
# kill, killpg, signal, pkill or pgrep. In the wrapper, any of those words
# outside a comment.
signals() { # <file.py> — prints line:name for each hit
	python3 -I - "$1" <<'PY'
import sys, tokenize
with open(sys.argv[1], "rb") as f:
    for t in tokenize.tokenize(f.readline):
        if t.type == tokenize.NAME and t.string in ("kill", "killpg", "signal", "pkill", "pgrep"):
            print("%d:%s" % (t.start[0], t.string))
PY
}
hits=$(signals scripts/leftover-report.py)$(grep -nE '^[^#]*(kill|signal|pgrep)' scripts/leftover-report.sh || true)
[ -z "$hits" ] || fail "the report holds a signal call: $hits"

WORK=$(mktemp -d) || fail "mktemp failed"
PLANTED=""
kids() { LC_ALL=C ps -axo pid=,ppid= | awk -v p="$1" '$2 == p {print $1}'; }
cleanup() { # children first: a plant's child already up when this runs is ended
	# too; one forked after the scan survives, at most its 300 s (Codex code pass)
	for p in $PLANTED; do
		for k in $(kids "$p"); do kill "$k" 2>/dev/null || true; done
		kill "$p" 2>/dev/null || true
	done
	rm -rf "$WORK"
}
trap cleanup EXIT INT TERM
WORK=$(CDPATH= cd -- "$WORK" && pwd -P) # the kernel answers the resolved cwd

# Control: a respelled call fires, and a docstring naming kill does not.
printf '"""kill nothing"""\nimport os\nos . kill(\n  1, 15)  # planted\n' >"$WORK/control.py"
[ "$(signals "$WORK/control.py")" = "3:kill" ] || fail "the no-signal scan gave '$(signals "$WORK/control.py")' on its control, want '3:kill'"

if [ "$(uname -s)" != Darwin ]; then
	rc=0
	out=$(sh scripts/leftover-report.sh 2>&1) || rc=$?
	[ "$rc" = 2 ] && case $out in *"CANNOT SAY"*) true ;; *) false ;; esac ||
		fail "off darwin the report gave rc=$rc '$out', want rc=2 and CANNOT SAY"
	echo "check-leftover-report: darwin census NOT RUN on $(uname -s) (the self-test and the CANNOT SAY arm ran)" >&2
	exit 0
fi

go build -o "$WORK/bin/buddy" ./cmd/buddy || fail "could not build buddy"
mkdir -p "$WORK/sleeper"
cat >"$WORK/sleeper/main.go" <<'EOF'
// A non-platform binary: darwin returns its environment (a platform /bin/sh's
// it withholds). "spawn" starts one child of itself, so the tree is 2.
package main

import (
	"os"
	"time"
)

func main() {
	if len(os.Args) > 2 && os.Args[2] == "spawn" {
		_, _ = os.StartProcess(os.Args[0], []string{os.Args[0], os.Args[1], "child"},
			&os.ProcAttr{Files: []*os.File{nil, nil, nil}})
	}
	d, _ := time.ParseDuration(os.Args[1])
	time.Sleep(d)
}
EOF
(cd "$WORK/sleeper" && GOFLAGS= go mod init sleeper >/dev/null 2>&1 && go build -o "$WORK/bin/sleeper" .) ||
	fail "could not build the sleeper"
unset CLAUDE_CODE_SESSION_ID CODEX_THREAD_ID BUDDY_SESSION BUDDY_LABEL || true

SID_LIVE=11111111-aaaa-4aaa-8aaa-aaaaaaaaaaaa
SID_END=22222222-bbbb-4bbb-8bbb-bbbbbbbbbbbb
SID_FOREIGN=feedface-cccc-4ccc-8ccc-cccccccccccc
TOKEN=PLANTARGVTOKEN7f3a
R="$WORK/r"
E="$WORK/r-wt-end"
mkrepo "$R"
git -C "$R" commit -q --allow-empty -m init
git -C "$R" worktree add -q "$E" -b end
mkdir -p "$E/sub" "$WORK/elsewhere"
(cd "$R" && "$WORK/bin/buddy" init >/dev/null)
(cd "$R" && "$WORK/bin/buddy" hello --session "$SID_LIVE" --label live-lane >/dev/null </dev/null)
(cd "$E" && "$WORK/bin/buddy" hello --session "$SID_END" --label end-lane >/dev/null </dev/null)
(cd "$E" && printf '{"session_id":"%s","cwd":"%s"}' "$SID_END" "$E" | "$WORK/bin/buddy" bye >/dev/null 2>&1) || true

# orphan <name> <dir> <cmd...> — start cmd in dir from a subshell that exits,
# so launchd adopts it; its pid goes to $WORK/<name>.pid.
orphan() {
	n=$1 d=$2
	shift 2
	# The braces matter: `cd && cmd &` backgrounds the whole list, and $! is
	# then a subshell that forks cmd (measured: the first run recorded those).
	(cd "$d" && { "$@" </dev/null >/dev/null 2>&1 & echo $! >"$WORK/$n.pid"; })
	p=$(cat "$WORK/$n.pid")
	PLANTED="$PLANTED $p"
}
S="$WORK/bin/sleeper"
orphan envend "$WORK/elsewhere" env -i PATH=/usr/bin:/bin CLAUDE_CODE_SESSION_ID=$SID_END "$S" 300s spawn $TOKEN
orphan envlive "$WORK/elsewhere" env -i PATH=/usr/bin:/bin CLAUDE_CODE_SESSION_ID=$SID_LIVE "$S" 300s
orphan foreign "$WORK/elsewhere" env -i PATH=/usr/bin:/bin CLAUDE_CODE_SESSION_ID=$SID_FOREIGN "$S" 300s
orphan wt "$E/sub" env -i PATH=/usr/bin:/bin /bin/sh -c 'sleep 300; true'
orphan none "$WORK/elsewhere" env -i PATH=/usr/bin:/bin "$S" 300s
"$S" 300s </dev/null >/dev/null 2>&1 &
CHILD=$!
PLANTED="$PLANTED $CHILD"

pid() { cat "$WORK/$1.pid"; }
ppid() { LC_ALL=C ps -o ppid= -p "$1" 2>/dev/null | tr -d ' '; }
for n in envend envlive foreign wt none; do
	i=0
	while [ "$(ppid "$(pid $n)")" != 1 ]; do
		i=$((i + 1))
		[ $i -le 50 ] || fail "plant $n (pid $(pid $n)) was never adopted by launchd"
		sleep 0.1
	done
done
# The plants' own children are up (TREE 2 needs them), and recorded so the
# exit ends them too; the plain child is alive.
for n in envend wt; do
	i=0
	while [ -z "$(kids "$(pid $n)")" ]; do
		i=$((i + 1))
		[ $i -le 50 ] || fail "plant $n's child never started"
		sleep 0.1
	done
	PLANTED="$PLANTED $(kids "$(pid $n)")"
done
kill -0 "$CHILD" || fail "the control child is not alive"
[ "$(ppid "$CHILD")" = $$ ] || fail "the control child's parent is not this shell"
LC_ALL=C ps -ww -o command= -p "$(pid envend)" | grep -q "$TOKEN" ||
	fail "the argv control failed: the token is not in the plant's command line"

ledger_state() { (cd "$R/.git" && for f in buddy.db*; do printf '%s ' "$f"; cksum <"$f"; done); }
before=$(ledger_state)
out="$WORK/out"
sh scripts/leftover-report.sh --all "$R" >"$out" || fail "the report exited $? (output: $(cat "$out"))"
[ "$(ledger_state)" = "$before" ] || fail "the report changed the source ledger's files"

# row <pid> — the report's line for that pid, columns: PID PPID AGE RSS TREE
# TREE_RSS NAME VERDICT EVIDENCE SESSION...
row() { awk -v p="$1" '$1 == p' "$out"; }
expect() { # <name> <verdict> <evidence> <session's first word> [tree]
	r=$(row "$(pid "$1")")
	[ -n "$r" ] || fail "plant $1 (pid $(pid "$1")) is not in the report"
	got=$(printf '%s\n' "$r" | awk '{print $8, $9, $10}')
	[ "$got" = "$2 $3 $4" ] || fail "plant $1: got '$got', want '$2 $3 $4' (line: $r)"
	[ -z "${5:-}" ] || [ "$(printf '%s\n' "$r" | awk '{print $5}')" = "$5" ] || fail "plant $1: TREE is not $5 (line: $r)"
}
expect envend STALE env end-lane 2
expect envlive OWNED env live-lane
expect foreign UNKNOWN env s-feedface
expect wt STALE "worktree(1)" end-lane 2
expect none UNKNOWN - no
[ -z "$(row "$CHILD")" ] || fail "a process that is not an orphan is listed: $(row "$CHILD")"
! grep -q "$TOKEN" "$out" || fail "argv text reached the report"
grep -q "^  ledger .*: read, 2 session(s)$" "$out" || fail "the ledger line is not 'read, 2 session(s)': $(grep '^  ledger' "$out")"
tail -n 1 "$out" | grep -q "^summary: .* This report ends nothing" || fail "no summary line"
# launchd's own are counted and left out. A logged-in darwin user always has
# some (431 of 452 ppid-1 processes, measured; a service UID with none would
# fail here, an accepted dependence on the host): none counted means the
# original-parent test stopped telling them apart, and they are being listed.
nl=$(sed -n 's/^  ppid 1: [0-9]*; \([0-9]*\) started by launchd.*/\1/p' "$out")
[ "${nl:-0}" -ge 1 ] || fail "no process counted as launchd's own (a UID launchd started nothing for cannot run this assertion): $(sed -n 2p "$out")"

# Without the ledger the same orphan is nobody's: the ledger decided STALE.
(cd "$WORK/elsewhere" && sh "$ROOT/scripts/leftover-report.sh" --all) >"$out" || fail "the no-ledger report failed"
expect envend UNKNOWN env s-22222222
grep -q "no ledger (not a repository)" "$out" || fail "the no-ledger run did not say so"

# A rollback journal beside the ledger is not read around (it may be hot):
# the same ledger read above ("read, 2 session(s)") is its control.
: >"$R/.git/buddy.db-journal"
sh scripts/leftover-report.sh --all "$R" >"$out" || fail "the journal run failed"
rm -f "$R/.git/buddy.db-journal"
grep -q "^  ledger .*: not read (a rollback journal is present)$" "$out" || fail "a ledger with a journal beside it was read: $(grep '^  ledger' "$out")"
expect envend UNKNOWN env s-22222222

echo "check-leftover-report: ok"
