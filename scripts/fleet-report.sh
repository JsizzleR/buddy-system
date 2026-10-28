#!/bin/sh
# fleet-report.sh — one screen over every ledger a top session coordinates:
# per ledger, whether it can be read, and counts and ids of what it holds.
# Counts and ids ONLY: no label, slug, note, description or message body is
# ever printed (invariant 9, below).
#
#   sh scripts/fleet-report.sh <list-file>
#
# <list-file> is the fleet repo's list of coordinated projects: one path per
# line, a line whose first non-blank is `#` a comment, blank lines ignored,
# leading and trailing blanks trimmed. A relative path is read against the
# list file's own directory, never the cwd, so every path this script hands
# to git or cat is absolute and none can be read as an option.
#
# WHY IT EXISTS (docs/BEYOND-CODE-PLAN.md, section 4 step 2; option A of 3.4).
# One ledger per repo means one roster per repo: `buddy sessions` answers for
# the cwd's ledger and nothing else, and the harness's own list (`claude
# agents --json`) has status, cwd and pid but no claims, no waits, no queued
# mail, and NOTHING for a ledger with no live session. That last one is the
# ledger the top session most needs to see (Codex design finding 4): open
# claims and queued mail with nobody left to act on them. So the ledgers come
# from the operator's explicit list, not from whoever happens to be running,
# and the harness list only supplements it, as a count of sessions working in
# ledgers the list does not name.
#
# THE VERDICTS, kept apart the way the gate keeps them (invariant 3):
#   available            read; at least one session seen inside 30 minutes
#   no live session      read; nobody seen inside 30 minutes (the case above)
#   unavailable, no ledger
#                        provably nothing to read: not a repository, or a
#                        repository never `buddy init`ed. The gate's verdict
#                        there is OFF.
#   unavailable, ledger UNREADABLE
#                        a buddy.db exists and store.Open would fail on it as
#                        this script did: the OS refuses it or a journal, or
#                        it is not a regular file, or not SQLite. The gate
#                        DENIES there.
#   unavailable, ledger not read
#                        a buddy.db exists and stopped THIS script only: still
#                        changing after three copies, a link (not followed),
#                        a quick_check failure. No claim about the gate.
#   unavailable, schema N
#                        read, at a schema version this script was not
#                        written against: said with its number, never guessed
#                        at, because a column it reads may have moved.
# and three that are neither, said as what they are: no such path, not a
# directory, and a path (or a repository git refuses) where whether a ledger
# exists cannot be told. None of those is "no ledger": collapsing "could not
# look" into "nothing there" is the failure invariant 3 is written against.
#
# HOW A LEDGER IS READ: a PRIVATE IMMUTABLE COPY, never a buddy verb (Codex
# design finding 8). A verb run under a registered session id moves that
# row's last_seen (D-086), and store.Open MIGRATES a ledger older than the
# binary, so "a read-only report" through buddy would write to every ledger
# it looked at. Nor is the source opened with sqlite3: opening a WAL database
# creates its -shm and may checkpoint it. Instead, as `buddy diagnose` does
# (D-072): buddy.db and its -wal (and a -journal, if one is there) are read
# with cat into a mode-700 temporary directory, read again and compared with
# cmp, and the copy is retried (three times) until source and copy agree; a
# ledger still changing after that is reported UNREADABLE (changing), never
# half-read. Unlike diagnose, a nonempty WAL is copied rather than refused:
# a live ledger always has one, and diagnose's refusal would make every
# ledger with a session in it unavailable. The copy alone is then opened
# read-write ONCE, to set journal_mode=DELETE, which folds the WAL's
# committed frames into it (a torn last frame fails its checksum and is
# dropped, as SQLite always does); every query after that opens it
# `mode=ro&immutable=1`. The source is only ever read by cat and cmp.
# `-init /dev/null` keeps the operator's ~/.sqliterc out of every query.
# Residual, left: a link or a FIFO is refused before cat runs, but one
# swapped in between that test and cat is opened (a FIFO would hang the
# report). Diagnose closes that with O_NOFOLLOW|O_NONBLOCK; a shell cannot,
# and the ledger trusts the machine user anyway (invariant 6).
#
# INVARIANT 9, THE WHOLE DESIGN. Every value this script prints is a number,
# a fixed word, or an identifier validated in SQL before it leaves sqlite3:
#   a session    s-<8hex>: its id with '-' removed, first 8 characters, and
#                only if all 8 are hex ("s-(id not hex)" otherwise). An id
#                arrives from the harness or `hello --session`, so it is not
#                trusted to be hex either.
#   a claim      its claim_id, only if it is 1..64 hex characters.
#   a message    its msg_id, an integer.
#   a pause      its control id; its target as `all`, as s-<8hex> when it is
#                a session id the ledger knows or has the shape of one (a
#                UUID: the row may have been swept), else "(a name, not
#                shown)".
#   a project    the path as written in the operator's own list, with control
#                characters shown as '?', so the line structure is the
#                script's and not the file's.
# No label, slug, description, note, message body, sender, cwd or pane is
# ever SELECTED, so none can be printed: the fenced roster is one `cd
# <project> && buddy sessions` away. check-fleet-report.sh plants a raw
# newline and a 600-byte line in each of those through the real binary and
# fails on any of their bytes in the output, and on a mutant that prints one.
#
# THE COUNTS, per readable ledger, with the ledger's own rules:
#   sessions   live = not ended and seen inside 30 minutes (store.StaleAfter);
#              quiet = not ended and not seen for longer; ended today = ended
#              since local midnight.
#   claims     open; STALE by D-090's judgement as far as rows can carry it:
#              open, renewed (a zero renewal is unknown, never stale) more
#              than 30 minutes ago, and the holder ended, missing, or not
#              seen for 30 minutes. D-090's third arm, every registered
#              process GONE, needs the process table and is not read here:
#              a holder whose harness died without a bye reads as seen until
#              its last_seen is 30 minutes old. "unrenewed, holder seen" is
#              D-090's ask-the-holder arm, with a slot claim held by a seen
#              holder left out exactly as D-090 leaves it out. The grain is
#              the ledger's whole second: the judge compares a sub-second
#              clock, so a holder seen exactly 30m ago reads seen here for
#              up to a second after the judge reads it quiet.
#   waits      open (not cleared), and how many of those are past their
#              deadline (EXPIRED at its next check).
#   inbox      undelivered, counted per MESSAGE: a direct message with no
#              recorded delivery to anyone, or a broadcast inside its 24h keep
#              (store.BroadcastKeep) that some recipient in its snapshot has
#              not had; adapter mail is its own register (D-076) and is not
#              counted. And the oldest one's age. store.Undelivered answers
#              per RECIPIENT, so they differ in one case: a message to a
#              label one session drained is queued again for a later session
#              wearing that label, and is not counted here.
#   paused     uncleared pause controls.
#   handoff    sessions not ended whose last observed prompt, for their
#              current incarnation, is at or past the handoff size THIS
#              script's environment declares (BUDDY_HANDOFF_AT, parsed as
#              buddy parses it: "300k", "1M", a bare count; anything else is
#              no size, said as such and nothing counted). D-113: the size
#              was declared fleet-wide and 75 sessions across two repos ran
#              past it in 4 days; busy tells the lane, nothing told the
#              orchestrator. The size is the READER's, because the ledger
#              does not store the one a session's own process declared, and
#              the number is one request stale (invariant 10): a count to
#              look at, never a verdict on anyone.
# Then the exception lines: each STALE claim, each undelivered message, each
# pause, each session at or past the handoff size (its s-<8hex> and prompt).
#
# THE CAP. Output stays under 9,000 bytes, the size a SessionStart digest is
# held under (D-034), so it fits one hook output or one tool result. Every
# ledger's verdict and count lines are printed first in priority; exception
# lines fill what is left, in order, stopping at the first that does not fit
# (D-036's rule), and one line says how many were left out. If the verdict
# lines alone overflow, they too stop at the first misfit and are counted.
#
# KNOBS
#   BUDDY_FLEET_CLAUDE   the `claude` executable for the harness list
#                        (default `claude`); the check points it at a fake.
set -eu

usage='usage: fleet-report.sh <list-file>'
case "${1:-}" in
	-h | -help | --help) echo "$usage"; exit 0 ;;
esac
[ $# -eq 1 ] || { echo "$usage" >&2; exit 2; }
for c in sqlite3 git awk cmp; do
	command -v "$c" >/dev/null 2>&1 || { echo "fleet-report: missing required command: $c" >&2; exit 2; }
done
list=$1
[ -f "$list" ] && [ -r "$list" ] || { echo "fleet-report: cannot read the list file" >&2; exit 2; }

# Git's local environment (an absolute GIT_DIR from a hook in a linked
# worktree, #49) would answer every `git -C <project>` below with the CALLER's
# repository. The same list lib.sh clears.
for _v in $(git rev-parse --local-env-vars 2>/dev/null || true) \
	GIT_ALTERNATE_OBJECT_DIRECTORIES GIT_CONFIG GIT_CONFIG_PARAMETERS GIT_CONFIG_COUNT \
	GIT_OBJECT_DIRECTORY GIT_DIR GIT_WORK_TREE GIT_IMPLICIT_WORK_TREE GIT_GRAFT_FILE \
	GIT_INDEX_FILE GIT_NO_REPLACE_OBJECTS GIT_REPLACE_REF_BASE GIT_PREFIX GIT_SHALLOW_FILE \
	GIT_COMMON_DIR; do
	unset "$_v"
done
unset _v

# The schema versions whose tables and columns the queries below were checked
# against (store.schemaVersion). Add one only after reading its migration.
known=16
stale=1800 # store.StaleAfter
keep=86400 # store.BroadcastKeep

# handoff_of <declaration> — the size in tokens, or 0 for none: declaredWindow's
# rules (internal/cli/transcript.go) in sh. Blanks trimmed around the whole
# and around the number, one k/K or m/M suffix, an optional '+', decimal digits
# only (leading zeros are decimal, never octal), and at most 2^40 tokens so
# nothing downstream can wrap. Zero, negative, a fraction or a stray byte: 0.
# The trim is the WHOLE string's, as strings.TrimSpace's, never a line's: a
# per-line sed kept a leading newline Go drops (Codex code pass). TrimSpace's
# two non-ASCII spaces (U+0085, U+00A0) are not trimmed here: such a value
# reads as no size, said as such.
_ws=$(printf ' \n\t\v\f\r')
trim() {
	_t=$1
	while :; do case $_t in ["$_ws"]*) _t=${_t#?} ;; *) break ;; esac; done
	while :; do case $_t in *["$_ws"]) _t=${_t%?} ;; *) break ;; esac; done
}
handoff_of() {
	trim "$1"
	_s=$_t
	_m=1
	case $_s in
		*[kK]) _m=1000; _s=${_s%?} ;;
		*[mM]) _m=1000000; _s=${_s%?} ;;
	esac
	trim "$_s"
	_s=${_t#+}
	case $_s in '' | *[!0-9]*) echo 0; return ;; esac
	_s=$(printf '%s' "$_s" | sed 's/^0*//')
	case $_s in '' | ??????????????*) echo 0; return ;; esac # zero, or past 13 digits
	awk -v n="$_s" -v m="$_m" 'BEGIN { if (n + 0 > int(1099511627776 / m)) print 0; else printf "%.0f\n", n * m }'
}
handoff=$(handoff_of "${BUDDY_HANDOFF_AT:-}")

# From here on no subprocess's diagnostics reach the output: an error message
# quotes its operands, and an operand can be any path, a TMPDIR holding a
# newline among them (Codex code pass: mktemp printed one). This script's own
# lines go to fd 4, the original stderr, and are fixed text. An unexpected
# exit says so there in one fixed line, rather than dying silently.
exec 4>&2 2>/dev/null
work=
trap 'rc=$?; [ -z "$work" ] || rm -rf "$work"; [ "$rc" = 0 ] || echo "fleet-report: stopped early (exit $rc)" >&4' EXIT
trap 'exit 130' INT TERM
work=$(mktemp -d "${TMPDIR:-/tmp}/fleet-report.XXXXXX") || { echo "fleet-report: could not make a temporary directory" >&4; exit 1; }
chmod 700 "$work"
rows="$work/rows"
ledgers="$work/ledgers"
: >"$rows"
: >"$ledgers"
tab=$(printf '\t')

now=$(date +%s)
midnight=$(sqlite3 -init /dev/null -batch :memory: "SELECT strftime('%s','now','localtime','start of day','utc');" 2>/dev/null </dev/null)
case "$now$midnight" in '' | *[!0-9]*) echo "fleet-report: could not read the clock" >&4; exit 2 ;; esac

listdir=$(CDPATH= cd -- "$(dirname -- "$list")" && pwd)
show() { printf '%s' "$1" | LC_ALL=C tr '\000-\037\177' '?'; }
# P: a verdict or count line, printed first in priority. E: an exception line.
P() { printf 'P\t%s\n' "$1" >>"$rows"; }

# common_of <dir> — the physical path of <dir>'s git common directory, or fail.
common_of() {
	c=$(git -C "$1" rev-parse --path-format=absolute --git-common-dir 2>/dev/null </dev/null) || return 1
	CDPATH= cd -- "$c" 2>/dev/null && pwd -P
}

# dotgit_above <dir> — a .git in <dir> or any ancestor: git refused a repository,
# which is not the same as there being none.
dotgit_above() {
	d=$1
	while :; do
		[ -e "$d/.git" ] && return 0
		[ "$d" = / ] && return 1
		d=$(dirname -- "$d")
	done
}

# absent <path> — PROVABLY not there. `[ ! -e ]` alone is also true when the
# lookup itself is refused (EACCES), and "could not look" said as "nothing
# there" is the collapse invariant 3 forbids (Codex code pass): a project
# under a mode-000 directory read "no such path". So walk up to the nearest
# ancestor that exists: absence is proven when it is a directory that can be
# searched, or not a directory at all (nothing can be under it). The first
# cut tested only the parent, and read /nonexistent/x as "unreadable".
absent() {
	[ ! -e "$1" ] && [ ! -L "$1" ] || return 1
	_a=$(dirname -- "$1")
	while [ ! -e "$_a" ] && [ ! -L "$_a" ]; do
		[ "$_a" = / ] && return 1
		_a=$(dirname -- "$_a")
	done
	[ ! -d "$_a" ] || [ -x "$_a" ]
}

# plain <path> — absent, or a regular file and not a link. A FIFO swapped in for
# a -wal would hang cat; a link is not the ledger the gate reads.
plain() { [ ! -e "$1" ] && [ ! -L "$1" ] || { [ -f "$1" ] && [ ! -L "$1" ]; }; }
# same <a> <b> — both absent, or both present with equal bytes.
same() {
	if [ -e "$1" ]; then [ -e "$2" ] && cmp -s "$1" "$2"; else [ ! -e "$2" ]; fi
}

# snapshot <ledger> <dir> — a consistent private copy of the ledger and its
# journals in <dir>/l.db*. 1: still changing after three tries; 2: a journal
# the OS refuses; 3: a journal that is a link or not a regular file.
snapshot() {
	for _try in 1 2 3; do
		rm -f "$2/l.db" "$2/l.db-wal" "$2/l.db-journal" "$2/l.db-shm"
		for s in '' -wal -journal; do
			plain "$1$s" || return 3
			# 2>/dev/null FIRST: redirections apply left to right, and a failed
			# `<` prints the shell's own error, naming the path, before a later
			# 2>/dev/null exists (Codex code pass). The script-wide stderr close
			# above covers it too; this is the second layer.
			if [ -e "$1$s" ]; then cat 2>/dev/null <"$1$s" >"$2/l.db$s" || return 2; fi
		done
		same "$1" "$2/l.db" && same "$1-wal" "$2/l.db-wal" && same "$1-journal" "$2/l.db-journal" && return 0
	done
	return 1
}

# q <sql> — run against the immutable copy; tab-separated, no header.
q() { sqlite3 -init /dev/null -batch -noheader -separator "$tab" "$uri" "$1" 2>/dev/null </dev/null; }

# The validated identifiers (see the header). Nothing else that is text is
# ever selected.
sid8() { printf "CASE WHEN length(substr(replace(%s,'-',''),1,8))=8 AND substr(replace(%s,'-',''),1,8) NOT GLOB '*[^0-9a-fA-F]*' THEN 's-'||substr(replace(%s,'-',''),1,8) ELSE 's-(id not hex)' END" "$1" "$1" "$1"; }
cid() { printf "CASE WHEN length(%s) BETWEEN 1 AND 64 AND %s NOT GLOB '*[^0-9a-fA-F]*' THEN %s ELSE '(id not hex)' END" "$1" "$1" "$1"; }

# Two ways a ledger that exists goes unread, kept apart because only the first
# is the gate's verdict too. UNREADABLE: what store.Open would also fail on (the
# OS refuses it, it is not a file or not SQLite), so the gate DENIES there. NOT
# READ: what stops this report and not the gate (a ledger busy through three
# copies, a link store.Open follows, a quick_check failure the gate never
# runs); saying "DENIES" there was false (Codex code pass).
unreadable() { P "== unavailable, ledger UNREADABLE ($1; the gate DENIES there): $disp"; }
notread() { P "== unavailable, ledger not read ($1; the gate's verdict there is not this report's to say): $disp"; }

# report <path> <as-written>
report() {
	p=$1
	disp=$(show "$2")
	if absent "$p"; then
		P "== unavailable, no such path: $disp"
		return
	fi
	if [ ! -e "$p" ] && [ ! -L "$p" ]; then
		P "== unavailable, path unreadable (whether a ledger is there cannot be told): $disp"
		return
	fi
	if [ ! -d "$p" ]; then
		P "== unavailable, not a directory: $disp"
		return
	fi
	if [ ! -r "$p" ] || [ ! -x "$p" ]; then
		P "== unavailable, path unreadable (whether a ledger is there cannot be told): $disp"
		return
	fi
	if ! common=$(common_of "$p"); then
		if dotgit_above "$p"; then
			P "== unavailable, git refuses the repository (whether a ledger is there cannot be told): $disp"
		else
			P "== unavailable, no ledger (not a repository; the gate's verdict there is OFF): $disp"
		fi
		return
	fi
	ledger="$common/buddy.db"
	printf '%s\n' "$ledger" >>"$ledgers"
	# common_of has cd'd into the common dir, so it is searchable and absent()
	# is proof here; no "cannot be searched" arm, as none is reachable.
	if absent "$ledger"; then
		P "== unavailable, no ledger (never buddy init-ed; the gate's verdict there is OFF): $disp"
		return
	fi
	if [ -L "$ledger" ]; then notread "buddy.db is a link, which this report does not follow"; return; fi
	if [ ! -f "$ledger" ]; then unreadable "not a regular file"; return; fi
	if [ ! -r "$ledger" ]; then unreadable "permission denied"; return; fi
	snap=$(mktemp -d "$work/l.XXXXXX") || { notread "no room for a private copy"; return; }
	rc=0
	snapshot "$ledger" "$snap" || rc=$?
	case $rc in
		0) ;;
		1) notread "still changing after three copies"; return ;;
		2) unreadable "a journal file cannot be read"; return ;;
		*) notread "a journal file is a link or not a regular file"; return ;;
	esac
	# Whether the bytes are SQLite is the LEDGER's property and the gate's
	# verdict too; a fold failing for any other reason (a full TMPDIR, a copy
	# SQLite will not open) is this script's own and says nothing about the
	# gate. The first cut called every fold failure "not a SQLite database;
	# the gate DENIES" (orc-6's review). An empty file is SQLite's empty
	# database, which store.Open would initialise.
	if [ ! -s "$snap/l.db" ]; then notread "an empty file"; return; fi
	# SQLite's own words decide which side a fold failure is on: a body that
	# is "not a database" or "malformed" behind a good header is the ledger's
	# (store.Open fails the same way), anything else (a full disk, a copy it
	# cannot open) is this script's. The text is matched, never printed.
	if ! why=$(sqlite3 -init /dev/null -batch "$snap/l.db" 'PRAGMA journal_mode=DELETE;' 2>&1 >/dev/null </dev/null); then
		case $why in
			*"not a database"* | *malformed*) unreadable "not a SQLite database" ;;
			*) notread "its private copy could not be opened" ;;
		esac
		return
	fi
	rm -f "$snap/l.db-wal" "$snap/l.db-shm" "$snap/l.db-journal"
	# '%', '?' and '#' end or escape a URI path; nothing else in a path does.
	# And "file://x/..." names an AUTHORITY x, so a TMPDIR spelled with a
	# leading "//" has its slashes folded to one.
	uri="file:$(printf '%s' "$snap/l.db" | sed 's|^//*|/|; s/%/%25/g; s/?/%3f/g; s/#/%23/g')?mode=ro&immutable=1"
	ver=$(q 'PRAGMA user_version;') || { notread "its private copy could not be queried"; return; }
	case $ver in '' | *[!0-9]*) notread "no schema version could be read"; return ;; esac
	case " $known " in
		*" $ver "*) ;;
		*) P "== unavailable, schema $ver, which this report was not written against (it reads $known): $disp"; return ;;
	esac
	[ "$(q 'PRAGMA quick_check(1);')" = ok ] || { notread "fails quick_check"; return; }

	# One invocation over one immutable image, so every row agrees with every
	# other. TEMP views, because a WITH clause scopes to one statement; the
	# temp schema is the connection's own, so the copy stays untouched. Rows:
	# C (the counts), S (a STALE claim: id, holder, unrenewed seconds), M (an
	# undelivered message: id, seconds), Z (a pause: id, target, seconds), H
	# (a session at or past the handoff size: id, prompt). A sample counts
	# only for the incarnation that observed it, as the roster's does, and
	# only when its prompt is an integer: SQLite orders TEXT above every
	# number, so a malformed row would count (Codex code pass).
	out=$(q "
		CREATE TEMP VIEW oc AS
			SELECT c.claim_id, c.session_id, c.renewed, s.session_id AS sid, s.ended, s.last_seen
			FROM claims c LEFT JOIN sessions s ON s.session_id = c.session_id
			WHERE c.state = 'open' AND c.renewed > 0 AND $now - c.renewed > $stale;
		CREATE TEMP VIEW stale AS SELECT * FROM oc WHERE sid IS NULL OR ended IS NOT NULL OR $now - last_seen > $stale;
		CREATE TEMP VIEW unren AS SELECT * FROM oc WHERE sid IS NOT NULL AND ended IS NULL AND $now - last_seen <= $stale
			AND NOT (EXISTS (SELECT 1 FROM claim_scopes x WHERE x.claim_id = oc.claim_id)
				AND NOT EXISTS (SELECT 1 FROM claim_scopes x WHERE x.claim_id = oc.claim_id
					AND NOT (x.folded = '.buddy/slot' OR x.folded GLOB '.buddy/slot/*')));
		CREATE TEMP VIEW und AS
			SELECT m.msg_id, m.created FROM inbox m
			WHERE NOT EXISTS (SELECT 1 FROM adapter_messages a WHERE a.msg_id = m.msg_id)
			AND ((m.target <> 'all' AND NOT EXISTS (SELECT 1 FROM inbox_delivery d WHERE d.msg_id = m.msg_id))
				OR (m.target = 'all' AND m.created >= $now - $keep AND EXISTS (
					SELECT 1 FROM inbox_recipient r WHERE r.msg_id = m.msg_id AND NOT EXISTS (
						SELECT 1 FROM inbox_delivery d WHERE d.msg_id = m.msg_id AND d.session_id = r.session_id))));
		CREATE TEMP VIEW over AS
			SELECT s.session_id, CAST(x.prompt AS INTEGER) AS prompt FROM sessions s
			JOIN session_context x ON x.session_id = s.session_id AND x.incarnation = s.incarnation
			WHERE s.ended IS NULL AND $handoff > 0 AND typeof(x.prompt) = 'integer' AND x.prompt >= $handoff;
		SELECT 'C',
			(SELECT count(*) FROM sessions WHERE ended IS NULL AND $now - last_seen <= $stale),
			(SELECT count(*) FROM sessions WHERE ended IS NULL AND $now - last_seen > $stale),
			(SELECT count(*) FROM sessions WHERE ended IS NOT NULL AND ended >= $midnight),
			(SELECT count(*) FROM claims WHERE state = 'open'),
			(SELECT count(*) FROM stale),
			(SELECT count(*) FROM unren),
			(SELECT count(*) FROM session_waits WHERE cleared IS NULL),
			(SELECT count(*) FROM session_waits WHERE cleared IS NULL AND deadline <= $now),
			(SELECT count(*) FROM und),
			(SELECT coalesce($now - min(created), -1) FROM und),
			(SELECT count(*) FROM controls WHERE kind = 'pause' AND cleared IS NULL),
			(SELECT count(*) FROM over);
		SELECT 'S', $(cid claim_id), $(sid8 session_id), $now - renewed FROM stale ORDER BY renewed, claim_id;
		SELECT 'M', msg_id, $now - created FROM und ORDER BY msg_id;
		SELECT 'Z', id, CASE WHEN target = 'all' THEN 'all'
			WHEN EXISTS (SELECT 1 FROM sessions s WHERE s.session_id = controls.target) THEN $(sid8 target)
			WHEN length(target) = 36 AND length(replace(target, '-', '')) = 32
				AND replace(target, '-', '') NOT GLOB '*[^0-9a-fA-F]*' THEN $(sid8 target)
			ELSE '(a name, not shown)' END, $now - created
			FROM controls WHERE kind = 'pause' AND cleared IS NULL ORDER BY id;
		SELECT 'H', $(sid8 session_id), prompt FROM over ORDER BY session_id;
	") || { notread "a query failed"; return; }

	# The path reaches awk through ENVIRON, never -v: -v expands backslash
	# escapes, and a listed path holding "\n" would print a line break.
	printf '%s\n' "$out" | DISP=$disp LC_ALL=C awk -F '\t' -v ver="$ver" -v at="$handoff" '
		# tokens() in internal/cli: truncating, never rounding up past a size.
		function tok(n) {
			if (n >= 1000000) return int(n / 1000000) "." int(n % 1000000 / 100000) "M"
			if (n >= 1000) return int(n / 1000) "k"
			return n + 0
		}
		function age(s) {
			if (s < 0) s = 0
			if (s < 60) return s "s"
			if (s < 3600) return int(s / 60) "m"
			if (s < 172800) return sprintf("%dh%02dm", int(s / 3600), int(s % 3600 / 60))
			return int(s / 86400) "d"
		}
		$1 == "C" {
			printf "P\t== %s (schema %s): %s\n", ($2 > 0 ? "available" : "no live session"), ver, ENVIRON["DISP"]
			printf "P\t  sessions: %d live, %d quiet (not ended, not seen 30m+), %d ended today\n", $2, $3, $4
			printf "P\t  claims: %d open, %d STALE (GONE not read), %d unrenewed with the holder seen\n", $5, $6, $7
			printf "P\t  waits: %d open, %d past the deadline\n", $8, $9
			printf "P\t  inbox: %d undelivered%s\n", $10, ($10 > 0 ? ", oldest " age($11) : "")
			printf "P\t  paused: %d\n", $12
			if (at > 0) printf "P\t  handoff: %d not ended at or past %s (this report'"'"'s BUDDY_HANDOFF_AT; the last observed prompt)\n", $13, tok(at)
			else printf "P\t  handoff: no size declared here (BUDDY_HANDOFF_AT unset or not a size), nothing counted\n"
		}
		$1 == "S" { printf "E\t  STALE claim %s  holder %s  unrenewed %s\n", $2, $3, age($4) }
		$1 == "M" { printf "E\t  undelivered #%d  %s\n", $2, age($3) }
		$1 == "Z" { printf "E\t  paused #%d  %s  %s\n", $2, $3, age($4) }
		$1 == "H" { printf "E\t  over handoff %s  prompt %s\n", $2, tok($3) }
	' >>"$rows"
}

n=0
while IFS= read -r raw <&3 || [ -n "$raw" ]; do
	line=$(printf '%s' "$raw" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
	case "$line" in '' | '#'*) continue ;; esac
	n=$((n + 1))
	case "$line" in
		/*) report "$line" "$line" ;;
		*) report "$listdir/$line" "$line" ;;
	esac
done 3<"$list"

# The harness list: a count of sessions whose cwd is in a ledger the list does
# not name. A cwd is used to find a ledger and is never printed.
claude=${BUDDY_FLEET_CLAUDE:-claude}
harness="harness list: not available (no claude agents --json here, or no jq)"
if ! command -v jq >/dev/null 2>&1 || ! command -v "$claude" >/dev/null 2>&1; then
	:
elif ! agents=$("$claude" agents --json 2>/dev/null </dev/null); then
	harness="harness list: not available (claude agents --json failed)"
elif ! cwds=$(printf '%s' "$agents" | jq -r 'if type == "array" then .[] | (.cwd // "" | if type == "string" and startswith("/") and (test("[\\n\\r]") | not) then . else "-" end) else error end' 2>/dev/null); then
	harness="harness list: not read (claude agents --json gave JSON of an unexpected shape)"
else
	t=0 on=0 off=0 none=0 unk=0
	: >"$work/others"
	if [ -n "$cwds" ]; then
		while IFS= read -r c; do
			t=$((t + 1))
			# "No ledger" here too only when absence is proven, as above; a cwd
			# that cannot be searched is "no readable cwd" (Codex code pass).
			if [ "$c" = - ] || [ ! -d "$c" ] || [ ! -r "$c" ] || [ ! -x "$c" ]; then unk=$((unk + 1)); continue; fi
			if ! cd_=$(common_of "$c"); then
				if dotgit_above "$c"; then unk=$((unk + 1)); else none=$((none + 1)); fi
				continue
			fi
			if absent "$cd_/buddy.db"; then none=$((none + 1)); continue; fi
			if grep -Fxq -- "$cd_/buddy.db" "$ledgers"; then on=$((on + 1)); else
				off=$((off + 1))
				printf '%s\n' "$cd_" >>"$work/others"
			fi
		done <<EOF
$cwds
EOF
	fi
	nl=$(sort -u "$work/others" | grep -c . || true)
	harness="harness list: $t active session(s): $on in ledgers on the list, $off in $nl ledger(s) not on the list, $none in no ledger, $unk with no readable cwd"
fi

{
	printf 'H\tFleet report: %d project(s) from %s, %s\n' "$n" "$(show "$list")" "$(date '+%Y-%m-%d %H:%M %Z')"
	printf 'H\tCounts and ids only: a session is s-<8hex>, a claim its id, a project the path from the list; no label, slug, note, description or message body. The fenced roster: cd <project> && buddy sessions.\n'
	printf 'H\t%s\n' "$harness"
	cat "$rows"
} | LC_ALL=C awk -F '\t' -v limit=9000 '
	# Under limit bytes in all, the "more" line included: room is kept for it.
	{ kind[NR] = $1; text[NR] = substr($0, length($1) + 2); if ($1 != "E") must += length(text[NR]) + 1 }
	END {
		room = limit - 200
		fit = must <= room
		extra = 0; used = 0; left = 0; stopE = 0; stopP = 0
		for (i = 1; i <= NR; i++) {
			b = length(text[i]) + 1
			if (kind[i] == "E") {
				if (!fit || stopE || must + extra + b > room) { stopE = 1; left++; continue }
				extra += b
			} else if (!fit) {
				if (stopP || used + b > room) { stopP = 1; left++; continue }
				used += b
			}
			print text[i]
		}
		if (left) printf "... %d more line(s) not shown (the 9,000-byte cap); cd <project> && buddy ls, buddy who <id>\n", left
	}'
