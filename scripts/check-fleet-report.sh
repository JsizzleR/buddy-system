#!/bin/sh
# check-fleet-report.sh — done-check for scripts/fleet-report.sh
# (docs/BEYOND-CODE-PLAN.md, section 4 step 2).
#
# HERMETIC: sh, git, sqlite3, python3 and a freshly built `buddy`. Every
# ledger is a throwaway repo under mktemp; the harness list is a fake
# `claude` (BUDDY_FLEET_CLAUDE), so the machine's own sessions are never
# read. Without sqlite3 (the report's own requirement) or python3 this says
# NOT RUN on stderr, as check-wake-report.sh does without jq.
#
# What must hold, each with the control that proves the assertion could fire:
#   - INVARIANT 9. A label, a slug, a description, a message body and a
#     --from tag, each holding a raw newline and a 600-byte line, and a wait
#     note and a pause note with a newline and a 400-byte line (a wait note
#     is refused past 512 rendered bytes), are planted through the real binary, and the control
#     reads every one of them back out of the fixture ledger first. The gate
#     then fails on ANY of their bytes in the output, and on any line that is
#     not one of the report's own shapes (a raw newline in a printed value
#     makes a line no shape matches). Positive control: a private copy of the
#     report that prints the holder's LABEL where its s-<8hex> goes must FAIL
#     the gate. A gate that passes it is not armed.
#   - IDS ARE NOT TRUSTED EITHER. A session id is the caller's (`hello
#     --session`, a hook payload) and buddy does not validate it: one with a
#     newline and a 600-byte line holds two STALE claims and a pause, through
#     the binary, and one of its claims has a planted claim id (by SQL; buddy
#     mints those). Each must print as "(id not hex)". A pause on a UUID
#     whose session row is gone prints its s-<8hex>.
#   - Every verdict, apart: available; no live session (a ledger with open
#     claims and queued mail and nobody live: Codex finding 4); no ledger, for
#     a non-repository and for a repository never init-ed; ledger UNREADABLE,
#     for a ledger the OS refuses, a -wal the OS refuses (whose shell error
#     must not leak either) and one that is not SQLite; ledger NOT READ, for
#     a link the gate follows (never "DENIES") and an empty file; a SQLite
#     header over a corrupt body is UNREADABLE, the ledger's fault; an
#     unknown schema,
#     said with its number; a directory the OS refuses, and a path UNDER one,
#     whose contents cannot be told (never "no such path"); no such path. Exactly the counts the fixture holds, per D-090 as rows carry
#     it: STALE for an unrenewed claim whose holder is quiet or ended,
#     "unrenewed with the holder seen" for a live holder's, nothing for a
#     live holder's slot or a fresh claim; a drained message is not counted.
#   - The WAL is read. One claim is aged by a connection that is still OPEN
#     with autocheckpoint off while the report runs, so its committed frames
#     exist only in buddy.db-wal: a report that read the main file alone (or
#     opened the copy immutable before folding the WAL) would count one STALE
#     claim fewer.
#   - The source is never written: the set of buddy.db* files and each one's
#     bytes (the -wal and -shm included) are the same after the report as
#     before it.
#   - THE CAP. With 500 more undelivered messages the output stays under
#     9,000 bytes, ends with one "N more" line, and still prints every
#     ledger's verdict and counts, including the ledgers listed AFTER the one
#     that overflowed. Positive control: a copy with the cap raised must FAIL
#     the size gate on the same fixture.
#   - The list: a comment, blank lines and trimmed blanks are skipped; a
#     relative path is read against the list file's directory (not the cwd),
#     so a line `-b` is a project directory and never an option; a tab in a
#     path prints as '?'; a backslash sequence prints as written, never
#     expanded into a line break.
#   - The harness list: counts only, its planted session name never printed;
#     "not available" without the command or when it fails, "not read" for
#     JSON of another shape. A //-prefixed TMPDIR still reads every ledger.
set -eu
. "$(dirname -- "$0")/lib.sh"
ROOT=$(repo_root "$0")
CDPATH= cd -- "$ROOT"

fail() { echo "check-fleet-report: FAIL — $*" >&2; exit 1; }

for c in sqlite3 python3; do
	if ! command -v "$c" >/dev/null 2>&1; then
		echo "check-fleet-report: NOT RUN — no $c on PATH" >&2
		exit 0
	fi
done

WORK=$(mktemp -d) || fail "mktemp failed"
trap 'chmod -R u+rwx "$WORK" 2>/dev/null; rm -rf "$WORK"' EXIT INT TERM
go build -o "$WORK/bin/buddy" ./cmd/buddy || fail "could not build buddy"
PATH="$WORK/bin:$PATH"
export PATH
unset CLAUDE_CODE_SESSION_ID BUDDY_SESSION BUDDY_LABEL || true

# The planted strings: a raw newline, then a 600-byte line, each tagged so a
# leak names its source. FILL never occurs in anything the report prints.
FILL=$(awk 'BEGIN { for (i = 0; i < 300; i++) printf "x7" }')
plant() { printf 'PLANT%s top\nPLANT%s %s' "$1" "$1" "$FILL"; }
# A wait note is capped at 512 rendered bytes and refused past it, so the two
# notes carry the newline and a 400-byte line.
note() { printf 'PLANT%s top\nPLANT%s %s' "$1" "$1" "$(printf '%s' "$FILL" | cut -c1-400)"; }
SA=aaaaaaaa-1111-4111-8111-111111111111
SB=bbbbbbbb-2222-4222-8222-222222222222
SC=cccccccc-3333-4333-8333-333333333333
SD=dddddddd-4444-4444-8444-444444444444

L="$WORK/list"
mkdir -p "$L"
A="$WORK/alpha"
mkrepo "$A"
B="$L/-b" # listed as the relative line "-b"

# as <repo> <session> <args...> — a verb as that session's own Bash call.
as() { r=$1 s=$2; shift 2; (cd "$r" && CLAUDE_CODE_SESSION_ID=$s command buddy "$@" </dev/null); }
# reg <repo> <session> <label> — a hand-run hello: registers no process, so a
# later bye ends the session (an unbound row, D-025).
reg() { (cd "$1" && command buddy hello --session "$2" --label "$3" >/dev/null </dev/null); }
db() { sqlite3 -init /dev/null -batch "$1/.git/buddy.db" "$2" </dev/null; }

(cd "$A" && buddy init >/dev/null)
reg "$A" "$SA" "$(plant LABEL_A)"
reg "$A" "$SB" "$(plant LABEL_B)"
reg "$A" "$SC" "$(plant LABEL_C)"
as "$A" "$SA" claim "$(plant SLUG)" --desc "$(plant DESC)" --scope src >/dev/null
as "$A" "$SA" claim slot-a --desc x --scope .buddy/slot/run-a >/dev/null
as "$A" "$SA" claim fresh-a --desc x --scope fresh >/dev/null
as "$A" "$SB" claim b-work --desc x --scope b >/dev/null
as "$A" "$SC" claim c-work --desc x --scope c >/dev/null
as "$A" "$SA" wait --on b-work --note "$(note WAITNOTE)" >/dev/null
as "$A" "$SB" wait --until 1h --note "$(note WAITNOTE)" >/dev/null
as "$A" "$SB" msg "$SA" "drained before the report" >/dev/null 2>&1
as "$A" "$SA" inbox >/dev/null
as "$A" "$SB" msg "$SA" --from "$(plant FROM)" "$(plant BODY)" >/dev/null 2>&1
as "$A" "$SA" msg "$SB" "to b" >/dev/null 2>&1
as "$A" "$SA" msg all "to all" >/dev/null 2>&1
(cd "$A" && buddy pause all --note "$(note PAUSENOTE)" >/dev/null && buddy pause "$SA" --note "$(note PAUSENOTE)" >/dev/null)
(cd "$A" && printf '{"session_id":"%s","cwd":"%s"}' "$SC" "$A" | buddy bye >/dev/null 2>&1) || true
# Ageing, by hand: a fixture, never a ledger anybody uses. SB goes quiet,
# every claim but fresh-a is two hours unrenewed, SB's wait is past its
# deadline, and a legacy pause row names a label (a row written before
# targets were resolved).
db "$A" "UPDATE sessions SET last_seen = last_seen - 7200 WHERE session_id = '$SB';
	UPDATE claims SET renewed = renewed - 7200 WHERE slug NOT IN ('fresh-a', 'c-work');
	UPDATE session_waits SET deadline = strftime('%s','now') - 60 WHERE session_id = '$SB';
	INSERT INTO controls (kind, target, note, created) VALUES ('pause', 'PLANTLEGACYTARGET', 'n', strftime('%s','now'));"

# The no-live-session ledger: its one session has said bye, and an open claim
# and a queued message are left behind (finding 4).
mkdir -p "$B"
mkrepo "$B"
(cd "$B" && buddy init >/dev/null)
reg "$B" "$SD" "$(plant LABEL_D)"
as "$B" "$SD" claim d-work --desc "$(plant DESC)" --scope d >/dev/null
(cd "$B" && buddy msg "$SD" "$(plant BODY)" >/dev/null 2>&1)
(cd "$B" && printf '{"session_id":"%s","cwd":"%s"}' "$SD" "$B" | buddy bye >/dev/null 2>&1) || true
db "$B" "UPDATE claims SET renewed = renewed - 7200;"

# The planted-ID ledger. A session id is the caller's (`hello --session`, a
# hook payload) and buddy does not validate it, so one carrying a newline and
# a 600-byte line goes in through hello, claim and pause; a claim id is
# minted by buddy, so the planted one goes in by SQL, as the legacy pause row
# does. Both must print as "(id not hex)". A pause on a UUID whose session row
# is gone prints its s-<8hex>; it is an id, not a name.
P="$WORK/planted-ids"
mkrepo "$P"
(cd "$P" && buddy init >/dev/null)
SIDP=$(plant SID)
# Two planted claim ids, one per guard: short with a newline (the hex test
# alone refuses it), and 600 hex characters (the length bound alone does).
HEX600=$(awk 'BEGIN { for (i = 0; i < 300; i++) printf "ab" }')
reg "$P" "$SIDP" p
as "$P" "$SIDP" claim p-work --desc x --scope p >/dev/null
as "$P" "$SIDP" claim p-cid --desc x --scope q >/dev/null
as "$P" "$SIDP" claim p-long --desc x --scope r >/dev/null
(cd "$P" && buddy pause "$SIDP" --note n >/dev/null)
db "$P" "UPDATE sessions SET last_seen = last_seen - 7200;
	UPDATE claims SET renewed = renewed - 7200;
	UPDATE claim_scopes SET claim_id = 'PLANTCID' || char(10) || 'x'
		WHERE claim_id = (SELECT claim_id FROM claims WHERE slug = 'p-cid');
	UPDATE claims SET claim_id = 'PLANTCID' || char(10) || 'x' WHERE slug = 'p-cid';
	UPDATE claim_scopes SET claim_id = '$HEX600' WHERE claim_id = (SELECT claim_id FROM claims WHERE slug = 'p-long');
	UPDATE claims SET claim_id = '$HEX600' WHERE slug = 'p-long';
	INSERT INTO controls (kind, target, note, created)
		VALUES ('pause', 'eeeeeeee-5555-4555-8555-555555555555', 'n', strftime('%s','now'));"
[ "$(db "$P" "SELECT count(*) FROM sessions WHERE instr(session_id, '$SIDP') = 1 AND length(session_id) = length('$SIDP');")" = 1 ] ||
	fail "the plant control failed: the planted session id is not in the ledger whole"
[ "$(db "$P" "SELECT count(*) FROM claims c JOIN controls k ON k.target = c.session_id WHERE c.claim_id = 'PLANTCID' || char(10) || 'x' OR c.claim_id = '$HEX600';")" = 2 ] ||
	fail "the plant control failed: the two planted claim ids, held by the planted session it pauses, are not in the ledger"

# The control for the plant: every planted string IS in a ledger, whole.
for tag in LABEL_A LABEL_B LABEL_C SLUG DESC WAITNOTE FROM BODY PAUSENOTE; do
	case $tag in *NOTE) want=$(note "$tag") ;; *) want=$(plant "$tag") ;; esac
	got=$(db "$A" "SELECT count(*) FROM (SELECT label v FROM sessions UNION ALL SELECT slug FROM claims
		UNION ALL SELECT descr FROM claims UNION ALL SELECT note FROM session_waits UNION ALL SELECT sender FROM inbox
		UNION ALL SELECT body FROM inbox UNION ALL SELECT note FROM controls) WHERE instr(v, '$want') > 0;")
	[ "$got" -ge 1 ] || fail "the plant control failed: $tag is not in the fixture ledger whole"
done
[ "$(db "$A" "SELECT count(*) FROM sessions WHERE session_id = '$SC' AND ended IS NOT NULL;")" = 1 ] ||
	fail "the fixture's bye did not end SC"
[ "$(db "$B" "SELECT count(*) FROM sessions WHERE ended IS NULL;")" = 0 ] || fail "the fixture's bye did not end SD"

# The other list entries.
N="$WORK/plain dir" # not a repository
mkdir -p "$N"
I="$WORK/uninit"
mkrepo "$I"
U="$WORK/unknown-schema"
mkrepo "$U"
cp "$B/.git/buddy.db" "$U/.git/buddy.db"
sqlite3 -init /dev/null -batch "$U/.git/buddy.db" 'PRAGMA user_version = 99;' </dev/null
G="$WORK/garbage"
mkrepo "$G"
printf 'not a database\n' >"$G/.git/buddy.db"
# A good header over a corrupt body is the ledger's fault (store.Open fails
# alike): UNREADABLE. An empty file is SQLite's empty database: not read.
C="$WORK/corrupt-body"
mkrepo "$C"
{ printf 'SQLite format 3\000'; awk 'BEGIN { for (i = 0; i < 4000; i++) printf "%c", 33 + i % 90 }'; } >"$C/.git/buddy.db"
E="$WORK/empty-ledger"
mkrepo "$E"
: >"$E/.git/buddy.db"
R="$WORK/refused-ledger"
mkrepo "$R"
cp "$B/.git/buddy.db" "$R/.git/buddy.db"
chmod 000 "$R/.git/buddy.db"
X="$WORK/locked dir"
mkdir -p "$X/inner"
chmod 000 "$X"
# A -wal the OS refuses: the copy fails, and the shell's own "cannot open"
# error (which names the path) must not reach the output either.
W="$WORK/refused-wal"
mkrepo "$W"
cp "$B/.git/buddy.db" "$W/.git/buddy.db"
printf 'x' >"$W/.git/buddy.db-wal"
chmod 000 "$W/.git/buddy.db-wal"
# A ledger that is a link: store.Open follows it and the gate reads it, so the
# report says it did not read it, and never that the gate DENIES.
K="$WORK/linked-ledger"
mkrepo "$K"
ln -s "$B/.git/buddy.db" "$K/.git/buddy.db"
T=$(printf '%s/tab\tdir' "$WORK")
mkdir -p "$T"
asroot=0
[ -r "$R/.git/buddy.db" ] && asroot=1 # root reads a mode-000 file; those two lines cannot be checked

{
	printf '# the fleet, one project per line\n\n'
	printf '  %s  \n' "$A"
	printf '%s\n' "-b" "$N" "$I" "$U" "$G" "$K" "$R" "$W" "$X" "$X/inner" "$T" "$WORK/no-such" "$WORK/no-such/deeper" "$WORK/back\\nslash" "$P" "$C" "$E"
} >"$L/projects"

# A fake harness list: one session on the list, one in a ledger not on it, one
# in no repository, one whose cwd is not absolute, one in a directory the OS
# refuses (never "no ledger"); a planted name on each.
O="$WORK/other"
mkrepo "$O"
(cd "$O" && buddy init >/dev/null)
cat >"$WORK/bin/fakeclaude" <<EOF
#!/bin/sh
[ "\$1 \$2" = "agents --json" ] || exit 1
cat <<'JSON'
[{"sessionId":"1","cwd":"$A","name":"PLANTNAME one\nPLANTNAME two","status":"busy"},
 {"sessionId":"2","cwd":"$O","name":"PLANTNAME","status":"idle"},
 {"sessionId":"3","cwd":"$N","name":"PLANTNAME","status":"idle"},
 {"sessionId":"4","cwd":"relative/PLANTNAME","name":"PLANTNAME","status":"idle"},
 {"sessionId":"5","cwd":"$X","name":"PLANTNAME","status":"idle"}]
JSON
EOF
chmod +x "$WORK/bin/fakeclaude"
printf '#!/bin/sh\necho '"'"'{"cwd":"/PLANTNAME"}'"'"'\n' >"$WORK/bin/fakeodd"
printf '#!/bin/sh\necho PLANTNAME >&2\nexit 1\n' >"$WORK/bin/fakefail"
chmod +x "$WORK/bin/fakeodd" "$WORK/bin/fakefail"

# run <report-script> <out-file> — the report over the list while a second
# connection holds frames in the main fixture's WAL. Prints the report's exit
# status and whether every buddy.db* file of EVERY fixture ledger (the set of
# names, and each one's bytes, or its size and mtime where the OS refuses a
# read) was the same after the report as before it: the runner's own write
# lands before the first fingerprint, so only the report is judged.
run() {
	python3 -I - "$1" "$L/projects" "$2" "$A/.git/buddy.db" "$B/.git/buddy.db" "$U/.git/buddy.db" \
		"$G/.git/buddy.db" "$R/.git/buddy.db" "$W/.git/buddy.db" "$O/.git/buddy.db" "$P/.git/buddy.db" "$C/.git/buddy.db" "$E/.git/buddy.db" <<'PY'
import glob, hashlib, os, sqlite3, subprocess, sys
report, lst, out, db = sys.argv[1:5]
every = sys.argv[4:]
def one(f):
    st = os.lstat(f)
    try:
        return hashlib.sha256(open(f, "rb").read()).hexdigest()
    except PermissionError:
        return "unreadable %d %d" % (st.st_size, st.st_mtime_ns)
def fingerprint():
    return {f: one(f) for d in every for f in sorted(glob.glob(d + "*"))}
c = sqlite3.connect(db, isolation_level=None)
c.execute("PRAGMA wal_autocheckpoint=0")
# Only in the WAL while the report runs: c-work, two hours unrenewed, its
# holder SC already ended. Read from the main file alone, it is fresh. The
# value always changes (a second earlier when it would not), because a run
# in the same second as the last one writes the same value, and SQLite then
# writes no frame (measured: the second runner found an empty WAL).
c.execute("""UPDATE claims SET renewed = CASE WHEN renewed = strftime('%s','now') - 7200
    THEN renewed - 1 ELSE strftime('%s','now') - 7200 END WHERE slug = 'c-work'""")
if not os.path.exists(db + "-wal") or os.path.getsize(db + "-wal") == 0:
    sys.exit("the runner's write is not in the WAL")
before = fingerprint()
with open(out, "wb") as f:
    r = subprocess.run(["sh", report, lst], stdout=f, stderr=subprocess.STDOUT,
                       env=dict(os.environ, BUDDY_FLEET_CLAUDE="fakeclaude"))
print(r.returncode, "same" if fingerprint() == before else "changed")
PY
}

# gate <out-file> — invariant 9 and the cap. Prints why on failure.
gate() {
	if grep -qE 'PLANT|x7x7x7' "$1"; then
		echo "a planted string reached the output:"; grep -nE 'PLANT|x7x7x7' "$1" | cut -c1-160
		return 1
	fi
	bad=$(LC_ALL=C grep -Evn \
		-e '^Fleet report: [0-9]+ project\(s\) from .*, [0-9]{4}-[0-9]{2}-[0-9]{2} ' \
		-e '^Counts and ids only: a session is s-<8hex>, a claim its id, ' \
		-e '^harness list: (not available \(no claude agents --json here, or no jq\)|not available \(claude agents --json failed\)|not read \(claude agents --json gave JSON of an unexpected shape\)|[0-9]+ active session\(s\): [0-9]+ in ledgers on the list, [0-9]+ in [0-9]+ ledger\(s\) not on the list, [0-9]+ in no ledger, [0-9]+ with no readable cwd)$' \
		-e '^== (available|no live session) \(schema [0-9]+\): ' \
		-e '^== unavailable, (no such path|not a directory|path unreadable|git refuses the repository|no ledger|ledger UNREADABLE|ledger not read|schema [0-9]+,)' \
		-e '^  sessions: [0-9]+ live, [0-9]+ quiet \(not ended, not seen 30m\+\), [0-9]+ ended today$' \
		-e '^  claims: [0-9]+ open, [0-9]+ STALE \(GONE not read\), [0-9]+ unrenewed with the holder seen$' \
		-e '^  waits: [0-9]+ open, [0-9]+ past the deadline$' \
		-e '^  inbox: [0-9]+ undelivered(, oldest [0-9]+(s|m|h[0-9]{2}m|d))?$' \
		-e '^  paused: [0-9]+$' \
		-e '^  STALE claim ([0-9a-fA-F]{1,64}|\(id not hex\))  holder s-([0-9a-fA-F]{8}|\(id not hex\))  unrenewed [0-9]+(s|m|h[0-9]{2}m|d)$' \
		-e '^  undelivered #[0-9]+  [0-9]+(s|m|h[0-9]{2}m|d)$' \
		-e '^  paused #[0-9]+  (all|s-[0-9a-fA-F]{8}|s-\(id not hex\)|\(a name, not shown\))  [0-9]+(s|m|h[0-9]{2}m|d)$' \
		-e '^\.\.\. [0-9]+ more line\(s\) not shown \(the 9,000-byte cap\)' \
		"$1" | head -5) || true
	if [ -n "$bad" ]; then
		echo "a line that is none of the report's shapes (a raw newline in a value makes one):"; printf '%s\n' "$bad" | cut -c1-160
		return 1
	fi
	size=$(wc -c <"$1" | tr -d ' ')
	if [ "$size" -ge 9000 ]; then
		echo "the output is $size bytes, not under 9,000"
		return 1
	fi
}

want() { # want <out-file> <fixed-line> <what>
	grep -qxF -- "$2" "$1" || fail "$3: want the line
  $2
in:
$(cut -c1-200 "$1")"
}

res=$(run scripts/fleet-report.sh "$WORK/out1") || fail "the WAL-holding runner failed"
[ "$res" = "0 same" ] || fail "want exit 0 and the source ledger's files unchanged, got '$res':
$(cat "$WORK/out1")"
why=$(gate "$WORK/out1") || fail "the gate refused the report: $why"

o=$WORK/out1
want "$o" "== available (schema 16): $A" "the fixture is available"
want "$o" "  sessions: 1 live, 1 quiet (not ended, not seen 30m+), 1 ended today" "sessions: SA live, SB quiet, SC ended"
want "$o" "  claims: 5 open, 2 STALE (GONE not read), 1 unrenewed with the holder seen" \
	"claims: SB's and SC's STALE (SC's only in the WAL), SA's slug unrenewed, SA's slot and fresh claim silent"
want "$o" "  waits: 2 open, 1 past the deadline" "waits"
grep -q '^  inbox: 3 undelivered, oldest [0-9]' "$o" || fail "inbox: three undelivered, the drained one not counted:
$(cat "$o")"
want "$o" "  paused: 3" "paused"
for id in $(db "$A" "SELECT claim_id FROM claims WHERE slug IN ('b-work', 'c-work') ORDER BY slug;"); do
	grep -q "^  STALE claim $id  holder s-[bc]\{8\}  unrenewed 2h0[0-9]m$" "$o" || fail "the STALE line for claim $id:
$(cat "$o")"
done
for id in $(db "$A" "SELECT msg_id FROM inbox WHERE NOT EXISTS (SELECT 1 FROM inbox_delivery d WHERE d.msg_id = inbox.msg_id);"); do
	grep -q "^  undelivered #$id  " "$o" || fail "the undelivered line for #$id:
$(cat "$o")"
done
grep -q '^  paused #[0-9]*  all  ' "$o" || fail "the pause on all"
grep -q "^  paused #[0-9]*  s-aaaaaaaa  " "$o" || fail "the pause on SA, by its short id"
grep -q '^  paused #[0-9]*  (a name, not shown)  ' "$o" || fail "the legacy pause, its target not shown"
want "$o" "== no live session (schema 16): -b" "the ledger nobody is live in, listed relative as -b"
grep -A5 -xF -- "== no live session (schema 16): -b" "$o" | grep -qxF '  claims: 1 open, 1 STALE (GONE not read), 0 unrenewed with the holder seen' ||
	fail "-b's claim left behind:
$(cat "$o")"
grep -A5 -xF -- "== no live session (schema 16): -b" "$o" | grep -q '^  inbox: 1 undelivered' || fail "-b's queued mail"
want "$o" "== unavailable, no ledger (not a repository; the gate's verdict there is OFF): $N" "a non-repository"
# The planted ids: never printed, said as "(id not hex)"; the swept UUID by its short id.
pb=$(grep -A12 -xF -- "== no live session (schema 16): $P" "$o" | sed 1d | sed '/^==/,$d')
for l in "  sessions: 0 live, 1 quiet (not ended, not seen 30m+), 0 ended today" \
	"  claims: 3 open, 3 STALE (GONE not read), 0 unrenewed with the holder seen" "  paused: 2"; do
	printf '%s\n' "$pb" | grep -qxF -- "$l" || fail "the planted-id ledger: want '$l' in:
$pb"
done
printf '%s\n' "$pb" | grep -q '^  STALE claim [0-9a-f]\{16\}  holder s-(id not hex)  unrenewed 2h0[0-9]m$' ||
	fail "a planted session id must print as s-(id not hex):
$pb"
[ "$(printf '%s\n' "$pb" | grep -c '^  STALE claim (id not hex)  holder s-(id not hex)  unrenewed 2h0[0-9]m$')" = 2 ] ||
	fail "both planted claim ids (newline, and 600 hex) must print as (id not hex):
$pb"
printf '%s\n' "$pb" | grep -q '^  paused #[0-9]*  s-(id not hex)  ' || fail "a pause on a planted session id:
$pb"
printf '%s\n' "$pb" | grep -q '^  paused #[0-9]*  s-eeeeeeee  ' || fail "a pause on a UUID whose row is gone prints its short id:
$pb"
want "$o" "== unavailable, no ledger (never buddy init-ed; the gate's verdict there is OFF): $I" "a repository never init-ed"
want "$o" "== unavailable, schema 99, which this report was not written against (it reads 16): $U" "an unknown schema, with its number"
want "$o" "== unavailable, ledger UNREADABLE (not a SQLite database; the gate DENIES there): $G" "a ledger that is not SQLite"
want "$o" "== unavailable, ledger UNREADABLE (not a SQLite database; the gate DENIES there): $C" "a SQLite header over a corrupt body"
want "$o" "== unavailable, ledger not read (an empty file; the gate's verdict there is not this report's to say): $E" "an empty ledger file"
want "$o" "== unavailable, ledger not read (buddy.db is a link, which this report does not follow; the gate's verdict there is not this report's to say): $K" \
	"a linked ledger is not read, and no gate verdict is claimed for it"
if [ "$asroot" = 0 ]; then
	want "$o" "== unavailable, ledger UNREADABLE (permission denied; the gate DENIES there): $R" "a ledger the OS refuses"
	want "$o" "== unavailable, path unreadable (whether a ledger is there cannot be told): $X" "a directory the OS refuses"
	want "$o" "== unavailable, path unreadable (whether a ledger is there cannot be told): $X/inner" \
		"a path under a directory the OS refuses is not 'no such path'"
	want "$o" "== unavailable, ledger UNREADABLE (a journal file cannot be read; the gate DENIES there): $W" "a -wal the OS refuses"
else
	echo "check-fleet-report: NOTE — running as root, the two mode-000 verdicts were not checked" >&2
fi
want "$o" "== unavailable, no ledger (not a repository; the gate's verdict there is OFF): $WORK/tab?dir" "a tab in a path prints as ?"
want "$o" "== unavailable, no such path: $WORK/no-such" "no such path"
want "$o" "== unavailable, no such path: $WORK/no-such/deeper" "no such path, under a parent that is missing too"
want "$o" "== unavailable, no such path: $WORK/back\\nslash" "a backslash sequence prints as written"
grep -q '^Fleet report: 18 project(s) from ' "$o" || fail "the comment and the blank line are not projects"
if command -v jq >/dev/null 2>&1 && [ "$asroot" = 0 ]; then
	want "$o" "harness list: 5 active session(s): 1 in ledgers on the list, 1 in 1 ledger(s) not on the list, 1 in no ledger, 2 with no readable cwd" "the harness list's counts"
elif command -v jq >/dev/null 2>&1; then
	want "$o" "harness list: 5 active session(s): 1 in ledgers on the list, 1 in 1 ledger(s) not on the list, 2 in no ledger, 1 with no readable cwd" "the harness list's counts, as root"
else
	want "$o" "harness list: not available (no claude agents --json here, or no jq)" "no jq: the harness list is not available"
fi

# The positive control for invariant 9: the holder's LABEL where s-<8hex> goes.
sed 's/\$(sid8 session_id), \$now - renewed FROM stale/(SELECT label FROM sessions WHERE session_id = stale.session_id), $now - renewed FROM stale/' \
	scripts/fleet-report.sh >"$WORK/leaky.sh"
cmp -s scripts/fleet-report.sh "$WORK/leaky.sh" && fail "the invariant-9 control's mutation did not apply"
res=$(run "$WORK/leaky.sh" "$WORK/leaky.out") || fail "the invariant-9 control's runner failed"
[ "$res" = "0 same" ] || fail "the invariant-9 control: want exit 0 and the source unchanged, got '$res'"
grep -q PLANTLABEL "$WORK/leaky.out" || fail "the invariant-9 control printed no label, so it proves nothing:
$(cut -c1-160 "$WORK/leaky.out")"
gate "$WORK/leaky.out" >/dev/null && fail "the gate PASSED a report that prints a label: it is not armed"

# Environment-free refusals and the missing harness list.
none=$(BUDDY_FLEET_CLAUDE="$WORK/no-such-claude" sh scripts/fleet-report.sh "$L/projects" 2>&1) || fail "a missing harness command is not an error"
printf '%s\n' "$none" | grep -qxF 'harness list: not available (no claude agents --json here, or no jq)' ||
	fail "a missing harness command was not said:
$none"
for f in fakeodd:'harness list: not read (claude agents --json gave JSON of an unexpected shape)' \
	fakefail:'harness list: not available (claude agents --json failed)'; do
	hout=$(BUDDY_FLEET_CLAUDE=${f%%:*} sh scripts/fleet-report.sh "$L/projects" 2>&1) || fail "${f%%:*}: the report failed"
	printf '%s\n' "$hout" | grep -qxF -- "${f#*:}" || fail "${f%%:*}: want '${f#*:}':
$(printf '%s\n' "$hout" | sed -n 3p)"
	printf '%s\n' "$hout" | grep -q PLANT && fail "${f%%:*}: the harness's output reached the report"
done
sh scripts/fleet-report.sh >/dev/null 2>&1 && fail "no list file was accepted"
sh scripts/fleet-report.sh "$WORK/no-list" >/dev/null 2>&1 && fail "an unreadable list file was accepted"
sh scripts/fleet-report.sh --help | grep -q '^usage: fleet-report.sh <list-file>$' || fail "--help"

# A fold that fails for the SCRIPT's reason, not the ledger's: a sqlite3 shim
# that refuses only the fold, as a full TMPDIR would. Not read, never DENY.
mkdir -p "$WORK/shim"
real=$(command -v sqlite3)
printf '#!/bin/sh\ncase "$*" in *journal_mode=DELETE*) echo "Error: unable to open database file" >&2; exit 1 ;; esac\nexec "%s" "$@"\n' "$real" >"$WORK/shim/sqlite3"
chmod +x "$WORK/shim/sqlite3"
printf '%s\n' "$A" >"$L/one"
sh_out=$(PATH="$WORK/shim:$PATH" BUDDY_FLEET_CLAUDE="$WORK/no-such-claude" sh scripts/fleet-report.sh "$L/one" 2>&1)
printf '%s\n' "$sh_out" | grep -qxF -- "== unavailable, ledger not read (its private copy could not be opened; the gate's verdict there is not this report's to say): $A" ||
	fail "a fold failing for the script's own reason must be 'not read':
$(printf '%s\n' "$sh_out" | grep "$A" | cut -c1-200)"

# A TMPDIR spelled with a leading "//": "file://x/..." would name an
# authority, so the copy's URI folds the slashes. The ledger still reads.
mkdir -p "$WORK/tmpd"
dbl=$(TMPDIR="/$WORK/tmpd" BUDDY_FLEET_CLAUDE="$WORK/no-such-claude" sh scripts/fleet-report.sh "$L/projects" 2>&1)
printf '%s\n' "$dbl" | grep -qxF -- "== available (schema 16): $A" || fail "a //-prefixed TMPDIR broke the private copy's URI:
$(printf '%s\n' "$dbl" | grep "$A" | cut -c1-160)"

# A TMPDIR mktemp cannot use, holding a newline: its error quotes the path,
# and must not reach the output. One fixed line, or two with the exit note.
tmpout=$(TMPDIR="$WORK/no such
dir" sh scripts/fleet-report.sh "$L/projects" 2>&1) && fail "a TMPDIR that cannot be used was not refused"
printf '%s\n' "$tmpout" | grep -qxF 'fleet-report: could not make a temporary directory' || fail "the TMPDIR refusal was not said:
$tmpout"
printf '%s\n' "$tmpout" | grep -vxF -e 'fleet-report: could not make a temporary directory' -e 'fleet-report: stopped early (exit 1)' |
	grep -q . && fail "a subprocess's diagnostics reached the output:
$tmpout"

# THE CAP: 500 more undelivered messages in the first ledger listed.
db "$A" "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 500)
	INSERT INTO inbox (target, sender, body, created) SELECT '$SB', 'bulk', 'bulk', strftime('%s','now') - 600 FROM n;"
res=$(run scripts/fleet-report.sh "$WORK/out2") || fail "the capped run's runner failed"
[ "$res" = "0 same" ] || fail "the capped run: want exit 0 and the source unchanged, got '$res'"
why=$(gate "$WORK/out2") || fail "the gate refused the capped report: $why"
grep -q '^  inbox: 503 undelivered, oldest ' "$WORK/out2" || fail "the capped count is the whole count"
grep -q '^\.\.\. [1-9][0-9]* more line(s) not shown (the 9,000-byte cap)' "$WORK/out2" || fail "no 'N more' line:
$(tail -3 "$WORK/out2")"
[ "$(grep -c '^\.\.\. ' "$WORK/out2")" = 1 ] || fail "more than one 'N more' line"
tail -1 "$WORK/out2" | grep -q '^\.\.\. ' || fail "the 'N more' line is not last"
for v in "== no live session (schema 16): -b" "== unavailable, no such path: $WORK/no-such"; do
	want "$WORK/out2" "$v" "a ledger listed after the overflowing one keeps its verdict under the cap"
done
# Every readable ledger keeps its WHOLE count block under the cap, in order.
for h in "== available (schema 16): $A" "== no live session (schema 16): -b" "== no live session (schema 16): $P"; do
	blk=$(grep -A5 -xF -- "$h" "$WORK/out2" | sed 1d)
	got=$(printf '%s\n' "$blk" | sed 's/:.*//' | tr -d ' ' | tr '\n' ,)
	[ "$got" = "sessions,claims,waits,inbox,paused," ] || fail "under the cap, '$h' lost part of its count block (got $got):
$(cat "$WORK/out2" | cut -c1-160)"
done
shown=$(grep -c '^  undelivered #' "$WORK/out2")
more=$(sed -n 's/^\.\.\. \([0-9]*\) more.*/\1/p' "$WORK/out2")
# 503 + 1 (-b's) undelivered, 2 + 1 (-b's) + 3 (P's) STALE, 3 + 2 (P's)
# paused: 515 exception lines.
[ $((shown + more + $(grep -cE '^  (STALE claim |paused #)' "$WORK/out2"))) = 515 ] ||
	fail "shown ($shown) and 'more' ($more) do not add up to the 515 exception lines"

# The positive control for the cap: the same fixture, the cap raised.
sed 's/-v limit=9000 /-v limit=9000000 /' scripts/fleet-report.sh >"$WORK/uncapped.sh"
cmp -s scripts/fleet-report.sh "$WORK/uncapped.sh" && fail "the cap control's mutation did not apply"
res=$(run "$WORK/uncapped.sh" "$WORK/uncapped.out") || fail "the cap control's runner failed"
[ "$res" = "0 same" ] || fail "the cap control: want exit 0 and the source unchanged, got '$res'"
gate "$WORK/uncapped.out" >/dev/null && fail "the gate PASSED an uncapped report of $(wc -c <"$WORK/uncapped.out" | tr -d ' ') bytes: it is not armed"

echo "check-fleet-report: ok"
