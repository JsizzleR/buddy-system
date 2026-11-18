#!/bin/sh
# check-own-claim.sh — done-check for the gate's own-claim arm (D-228): an
# edit that no claim of the CALLING session covers, while a peer holds a
# claim, draws a model-visible line (default) or a refusal
# (BUDDY_OWN_CLAIM=deny).
#
# HERMETIC: git, sh, python3 and a freshly built `buddy`, in a throwaway repo
# under a throwaway HOME. No network, no daemon, no ledger outside $WORK.
#
# WHY THIS EXISTS ALONGSIDE THE GO SUITE. ownclaim_test.go drives cmdGate in
# process; this drives the BUILT binary with a hook payload on stdin, the way
# Claude Code runs it, and judges the bytes: one JSON document, and for a warn
# EXACTLY the keys hookEventName and additionalContext. A permissionDecision on
# the warn is the defect this arm is most exposed to: "allow" there skips the
# permission prompt for every uncovered edit and tells the model nothing.
#
# TWO GREP CLAUSES over every non-test .go file in the module, since -race and
# the table tests see only the paths they drive: (a) the permissionDecision key
# written with any value but "deny"; (b) the key RESPELLED so (a) cannot see it
# (concatenated, in a struct tag, escaped inside a JSON string, in a raw
# string). Syntactic, declared not closed (D-005): a helper that builds the key
# some other way passes both. Each clause is run first against planted
# samples it must catch and a deny line it must pass, so a clause that matches
# nothing reads as broken, not as a clean tree.
#
# ROWS (the plan's 5.4): 1 warn's shape, 2 deny, 3 off, 8 covered, 18's
# pathless Bash call, 26 no ledger and an unreadable ledger, 5 unarmed. Every
# silent row runs beside row 1's warn from the same fixture.
set -eu
. "$(dirname -- "$0")/lib.sh"
ROOT=$(repo_root "$0")
CDPATH= cd -- "$ROOT"

fail() { echo "check-own-claim: FAIL — $*" >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || fail "python3 not found"

WORK=$(mktemp -d) || fail "mktemp failed"
trap 'rm -rf "$WORK"' EXIT INT TERM
# The fixture's payloads name their sessions; the operator's must not leak in.
unset CLAUDE_CODE_SESSION_ID BUDDY_SESSION BUDDY_HARNESS BUDDY_LABEL BUDDY_OWN_CLAIM BUDDY_FLEET CODEX_THREAD_ID || true

# --- The grep clauses. ------------------------------------------------------
# bad_key <dir>: print each non-test .go line naming the key with a value
# other than "deny" (a), or respelling it (b). Empty output is clean.
bad_key() {
	find "$1" -name '*.go' ! -name '*_test.go' -type f -print | while IFS= read -r f; do
		grep -n '"permissionDecision"' "$f" | grep -v '"permissionDecision": *"deny"' | sed "s|^|$f:(a) |" || true
		grep -nE 'json:"permissionDecision|"permission" *\+|\+ *"Decision"|permissionDecision\\"|`[^`]*permissionDecision[^`]*`' "$f" | sed "s|^|$f:(b) |" || true
	done
}
PLANT="$WORK/plant"
mkdir -p "$PLANT"
cat > "$PLANT/a.go" <<'EOF'
package p
var a = map[string]string{"permissionDecision": "allow"}
EOF
cat > "$PLANT/b.go" <<'EOF'
package p
var b = "permission" + "Decision"
type t struct{ D string `json:"permissionDecision"` }
var c = "{\"permissionDecision\":\"allow\"}"
var d = `{"hookSpecificOutput":{"permissionDecision":"ask"}}`
EOF
cat > "$PLANT/ok.go" <<'EOF'
package p
var ok = map[string]string{"permissionDecision": "deny"}
// a comment saying permissionDecision "allow" would be wrong
EOF
cat > "$PLANT/ok_test.go" <<'EOF'
package p
var t = map[string]string{"permissionDecision": "allow"}
EOF
got=$(bad_key "$PLANT")
for want in 'a.go:(a) 2:' 'b.go:(b) 2:' 'b.go:(b) 3:' 'b.go:(b) 4:' 'b.go:(b) 5:'; do
	case $got in *"$want"*) ;; *) fail "the grep clauses missed planted sample $want — the gate is broken, not the tree
$got" ;; esac
done
case $got in *ok.go* | *ok_test.go*) fail "the grep clauses flagged a deny line, a comment or a test file
$got" ;; esac
got=$(bad_key "$ROOT/internal"; bad_key "$ROOT/cmd")
[ -z "$got" ] || fail "a permissionDecision other than \"deny\", or the key respelled, in non-test Go (D-228: an \"allow\" auto-approves every uncovered edit):
$got"

# --- The built binary. ------------------------------------------------------
go build -o "$WORK/bin/buddy" ./cmd/buddy || fail "could not build buddy"
HOME="$WORK/home"
mkdir -p "$HOME"
export HOME

REPO="$WORK/repo"
mkrepo "$REPO"
printf 'x\n' > "$REPO/README.md"
git -C "$REPO" add -A
git -C "$REPO" commit -q -m init
REPO=$(cd "$REPO" && pwd -P)

buddy() { ( cd "$REPO" && "$WORK/bin/buddy" "$@" ); }
hello() { printf '{"session_id":"%s","cwd":"%s"}' "$1" "$REPO" | buddy hello --label "$2" >/dev/null 2>&1 || fail "hello $1"; }
# gate <session> <tool> <file-or-command> [VAR=value...]: the gate's stdout.
gate() {
	s=$1 tool=$2 arg=$3
	shift 3
	if [ "$tool" = Bash ]; then
		in=$(python3 -I -c 'import json,sys; print(json.dumps({"session_id":sys.argv[1],"cwd":sys.argv[2],"tool_name":"Bash","tool_input":{"command":sys.argv[3]}}))' "$s" "$REPO" "$arg")
	else
		in=$(python3 -I -c 'import json,sys; print(json.dumps({"session_id":sys.argv[1],"cwd":sys.argv[2],"tool_name":sys.argv[3],"tool_input":{"file_path":sys.argv[4]}}))' "$s" "$REPO" "$tool" "$REPO/$arg")
	fi
	printf '%s' "$in" | ( cd "$REPO" && env "$@" "$WORK/bin/buddy" gate ) || fail "gate exited non-zero ($s $tool $arg $*)"
}
# kind <stdout>: silent | warn | deny, and the warn's keys checked exactly.
kind() {
	printf '%s' "$1" | python3 -I -c '
import json, sys
raw = sys.stdin.read()
if not raw.strip():
    print("silent"); sys.exit(0)
if raw.strip().count("\n"):
    print("MORE-THAN-ONE-DOCUMENT"); sys.exit(0)
h = json.loads(raw)["hookSpecificOutput"]
if "permissionDecision" in h:
    print("deny" if h["permissionDecision"] == "deny" else "BAD-DECISION:" + h["permissionDecision"])
elif sorted(h) == ["additionalContext", "hookEventName"] and h["hookEventName"] == "PreToolUse":
    print("warn")
else:
    print("BAD-KEYS:" + ",".join(sorted(h)))
'
}
expect() { # <row> <want-kind> <stdout> [substring]
	k=$(kind "$3") || fail "$1: the gate's output is not hook JSON: $3"
	[ "$k" = "$2" ] || fail "$1: want $2, got $k: $3"
	if [ $# -ge 4 ]; then
		case $3 in *"$4"*) ;; *) fail "$1: want \"$4\" in: $3" ;; esac
	fi
}

buddy init >/dev/null 2>&1 || fail "buddy init"
hello sess-a alpha
hello sess-b bravo
buddy claim router --session sess-a --desc d --scope internal/router >/dev/null 2>&1 || fail "claim router"

expect "row 1" warn "$(gate sess-b Edit docs/USAGE.md)" 'BUDDY UNCLAIMED:'
expect "row 1 remedy" warn "$(gate sess-b Edit docs/USAGE.md)" '--scope docs/USAGE.md'
expect "row 2" deny "$(gate sess-b Edit docs/USAGE.md BUDDY_OWN_CLAIM=deny)" 'inside no claim of this session'
expect "row 3" silent "$(gate sess-b Edit docs/USAGE.md BUDDY_OWN_CLAIM=off)"
expect "row 4" deny "$(gate sess-b Edit docs/USAGE.md BUDDY_OWN_CLAIM=dney)" 'not warn, deny or off'
expect "row 7 holder outranks" deny "$(gate sess-b Edit internal/router/x.go BUDDY_OWN_CLAIM=deny)" 'router'
expect "row 18 pathless Bash" silent "$(gate sess-b Bash 'echo x > docs/USAGE.md' BUDDY_OWN_CLAIM=deny)"
buddy claim usage --session sess-b --desc d --scope docs/USAGE.md >/dev/null 2>&1 || fail "claim usage"
expect "row 8 covered" silent "$(gate sess-b Edit docs/USAGE.md BUDDY_OWN_CLAIM=deny)"
expect "row 8 control" deny "$(gate sess-b Edit docs/other.md BUDDY_OWN_CLAIM=deny)" 'docs/other.md'
buddy release router --session sess-a >/dev/null 2>&1 || fail "release router"
expect "row 5 unarmed" silent "$(gate sess-b Edit docs/other.md BUDDY_OWN_CLAIM=deny)"

# Row 26: a repository with no ledger is a silent no-op; an unreadable one
# denies as it did.
NOL="$WORK/noledger"
mkrepo "$NOL"
NOL=$(cd "$NOL" && pwd -P)
in=$(python3 -I -c 'import json,sys; print(json.dumps({"session_id":"sess-b","cwd":sys.argv[1],"tool_name":"Edit","tool_input":{"file_path":sys.argv[1]+"/a"}}))' "$NOL")
out=$(printf '%s' "$in" | ( cd "$NOL" && BUDDY_OWN_CLAIM=deny "$WORK/bin/buddy" gate )) || fail "row 26: gate exited non-zero"
expect "row 26 no ledger" silent "$out"
COMMON=$(git -C "$REPO" rev-parse --path-format=absolute --git-common-dir)
printf 'this is not a database\n' > "$COMMON/buddy.db"
expect "row 26 unreadable" deny "$(gate sess-b Edit docs/USAGE.md)" 'ledger unavailable'

echo "check-own-claim: GREEN (grep clauses + planted controls; rows 1 warn keys exact, 2 deny, 3 off, 4 bad value,"
echo "  7 holder outranks, 18 pathless Bash, 8 covered + control, 5 unarmed, 26 no ledger + unreadable)"
