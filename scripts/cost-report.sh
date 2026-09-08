#!/bin/sh
# Privacy-preserving Buddy cost report. It reads aggregate counts and byte
# lengths only: no inbox body, chat text, prompt, or tool result is printed.
set -eu

days=${BUDDY_COST_DAYS:-7}
case "$days" in
	''|*[!0-9]*) echo "BUDDY_COST_DAYS must be a positive integer" >&2; exit 2 ;;
	0) echo "BUDDY_COST_DAYS must be greater than zero" >&2; exit 2 ;;
esac

need() {
	command -v "$1" >/dev/null 2>&1 || { echo "cost-report: missing required command: $1" >&2; exit 2; }
}
need sqlite3
need jq
need awk

# macOS sqlite3 -readonly cannot open some live WAL databases because SQLite
# still needs to coordinate through the shared-memory file. Open normally but
# make every reporting connection query-only before it evaluates SQL.

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
journal=${BUDDYLIST_JOURNAL:-"$HOME/.buddylist/journal.db"}
claude_projects=${CLAUDE_PROJECTS_DIR:-"$HOME/.claude/projects"}
if [ -n "${BUDDYLIST_BIN:-}" ]; then
	buddylist_bin=$BUDDYLIST_BIN
elif [ -x "$HOME/bin/buddylist" ]; then
	buddylist_bin="$HOME/bin/buddylist"
else
	buddylist_bin="$root/bin/buddylist"
fi

if [ -n "${BUDDY_LEDGER:-}" ]; then
	ledger=$BUDDY_LEDGER
else
	common=$(git -C "$PWD" rev-parse --git-common-dir 2>/dev/null || true)
	case "$common" in
		/*) ledger="$common/buddy.db" ;;
		'') ledger= ;;
		*) ledger="$PWD/$common/buddy.db" ;;
	esac
fi

cutoff_iso=$(sqlite3 :memory: "SELECT strftime('%Y-%m-%dT%H:%M:%SZ','now','-$days days');")
window="-$days days"

echo "Buddy cost report: last $days day(s)"
echo "All values are counts/bytes only; message and prompt content is never printed."

if [ -x "$buddylist_bin" ]; then
	echo
	echo "MCP schema"
	for profile in core full; do
		bytes=$(
			printf '%s\n' \
				'{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
				'{"jsonrpc":"2.0","method":"notifications/initialized"}' \
				'{"jsonrpc":"2.0","id":2,"method":"tools/list"}' |
				"$buddylist_bin" mcp --profile "$profile" 2>/dev/null |
				tail -n 1 | awk '{print length($0)}'
		)
		[ -n "$bytes" ] && awk -v p="$profile" -v b="$bytes" 'BEGIN {printf "%s\tbytes=%d\trough_token_equiv=%d\n",p,b,int((b+3)/4)}'
	done
fi

if [ -f "$journal" ]; then
	echo
	echo "Journal traffic"
	if journal_rows=$(sqlite3 -cmd 'PRAGMA query_only=ON;' -separator ' ' "$journal" "
		SELECT 'sent', count(*), coalesce(sum(length(CAST(body AS BLOB))),0),
			coalesce(round(avg(length(CAST(body AS BLOB))),1),0)
		FROM messages WHERE room='@sent' AND at>=strftime('%s','now','$window');
		SELECT 'room_chat', count(*), coalesce(sum(length(CAST(body AS BLOB))),0),
			coalesce(round(avg(length(CAST(body AS BLOB))),1),0)
		FROM messages WHERE kind='chat' AND at>=strftime('%s','now','$window');
	" 2>/dev/null); then
		printf '%s\n' "$journal_rows" | awk '{printf "%s\tcount=%s\tbytes=%s\tavg_bytes=%s\n",$1,$2,$3,$4}'
	else
		echo "Journal traffic: unavailable (could not read $journal)"
	fi
else
	echo
	echo "Journal traffic: unavailable ($journal not found)"
fi

if [ -n "$ledger" ] && [ -f "$ledger" ]; then
	echo
	echo "Ledger context fan-out"
	if ledger_rows=$(sqlite3 -cmd 'PRAGMA query_only=ON;' -separator ' ' "$ledger" "
		SELECT CASE WHEN m.target='all' THEN 'broadcast' ELSE 'targeted' END,
			count(*), coalesce(sum(min(length(CAST(m.body AS BLOB)),4096)),0)
		FROM inbox_delivery d JOIN inbox m ON m.msg_id=d.msg_id
		WHERE d.delivered>=strftime('%s','now','$window') GROUP BY 1 ORDER BY 1;
	" 2>/dev/null); then
		printf '%s\n' "$ledger_rows" | awk 'NF {printf "%s\tdeliveries=%s\tinjected_body_bytes=%s\n",$1,$2,$3}'
		if [ "$(sqlite3 -cmd 'PRAGMA query_only=ON;' "$ledger" "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='inbox_recipient';" 2>/dev/null)" = 1 ]; then
			recipient_rows=$(sqlite3 -cmd 'PRAGMA query_only=ON;' -separator ' ' "$ledger" "
				SELECT 'broadcast_audience', count(*) FROM inbox_recipient r JOIN inbox m ON m.msg_id=r.msg_id
				WHERE m.created>=strftime('%s','now','$window');
			" 2>/dev/null)
			printf '%s\n' "$recipient_rows" | awk '{printf "%s\trecipients=%s\n",$1,$2}'
		else
			echo "broadcast_audience\tlegacy ledger: recipient snapshots not migrated yet"
		fi
	else
		echo "Ledger context fan-out: unavailable (could not read $ledger)"
	fi
else
	echo
	# The git COMMON dir, not `.git`: in a `git worktree` checkout `.git` is a
	# FILE, so the obvious spelling names a path that does not exist and the
	# reader gets this same line back with no hint why.
	echo "Ledger context fan-out: unavailable (set BUDDY_LEDGER=<git-common-dir>/buddy.db; in a worktree the common dir is NOT .git)"
fi

if [ -d "$claude_projects" ]; then
	events=$(mktemp "${TMPDIR:-/tmp}/buddy-cost.XXXXXX")
	transcripts=$(mktemp "${TMPDIR:-/tmp}/buddy-cost-files.XXXXXX")
	jqerr=$(mktemp "${TMPDIR:-/tmp}/buddy-cost-jqerr.XXXXXX")
	trap 'rm -f "$events" "$transcripts" "$jqerr"' EXIT INT TERM
	find "$claude_projects" -type f -name '*.jsonl' -size +0c -print0 2>/dev/null >"$transcripts"
	seen=$(tr -cd '\000' < "$transcripts" | wc -c | tr -d ' ')
	# NO `-n 1`, NO PIPELINE, `-size +0c`, `<= 1` RATHER THAN `== 1`, AND THE FLOOR
	# VERDICT KEYED ON JQ'S OWN STDERR. Five lessons: four measured on one fixture
	# — a handful of transcripts, one holding a line that is not JSON — and the
	# fifth only visible against the real 1448-transcript corpus.
	#
	# `-n 1` was one jq process per transcript, hundreds of them on a working
	# machine. Worse, the old shape was `find … | xargs …`: a transcript jq cannot
	# parse makes xargs exit 123, that is the PIPELINE's status, and `set -e` then
	# killed the whole report — measured, rc=1 with this entire section missing and
	# not one word about why, after the sections above had already printed. One
	# malformed line in one transcript silently deleted the numbers.
	#
	# Batched and un-piped, that failure degrades instead of aborting, but it does
	# cost more: measured on jq 1.7.1, a parse error ABANDONS the rest of that
	# invocation's file list (rc=5, remaining files never opened). The report has
	# to say so when it happens — and the FILE COUNT IS THE WRONG WITNESS for it,
	# wrong in BOTH directions. Refuted by measurement, not by argument:
	#
	#   It misses the loss. The `F` row lands when a file yields its FIRST value,
	#   so every file jq had already opened counts as parsed even if jq then died
	#   partway through the last of them. Measured, three transcripts in find's
	#   order b, c, a with the bad line third inside a: `seen=3 parsed=3`, no note
	#   of any kind, and 4 of the fixture's 5 tool_use records presented as the
	#   whole truth. $jqerr was one line long the entire time and nothing looked
	#   at it.
	#
	#   It invents a loss. A file that yields no first-line value is opened, read
	#   to the end, and still counted unparsed. Measured by dropping one empty
	#   .jsonl into that fixture: `seen=4 parsed=3`, "1 transcript(s) were not
	#   read", "(0 jq error line(s))" — a warning that declares data missing in
	#   the same breath as reporting that jq complained about nothing. Permanent
	#   noise once it starts, which is the failure mode D-012 already rules
	#   against. `-size +0c` drops the empty ones before they are ever counted.
	#
	# So `-s "$jqerr"` — jq's own evidence that it stopped early — decides the
	# FLOOR verdict, and seen/parsed stay what they always were: reported numbers,
	# the ones that distinguish "1200 transcripts, all read" from "1200
	# transcripts, jq opened 40".
	#
	# `<= 1`, NOT `== 1`, and this is the one that says the count could never have
	# been the verdict. input_line_number counts NEWLINES CONSUMED so far, not the
	# line a value sits on, and jq refills from the file 4095 bytes at a time — so
	# when a first line ends exactly on that boundary its closing brace is the
	# last byte of one block and its newline the first byte of the next, and the
	# first value reports 0. Measured on jq 1.7.1, first line sized to the byte:
	# 4094 -> 1, 4095 -> 1, 4096 -> 0, 4097 -> 1, 8191 -> 0.
	#
	# It hides well, which is why it survived the review that found the two above.
	# Whether it costs the file its `F` row depends on the SECOND line too: a
	# 4096-byte first line followed by a ~4095-byte one gives 0, 1, … and the row
	# still lands, while the same first line followed by a SHORT one gives
	# 0, 2, 3, … and `== 1` matches nothing in that file at all. Found by running
	# this report against the real corpus rather than by reading jq: 1447 of 1448,
	# and the odd one out was a 136-line 873 KB subagent transcript with a
	# 4096-byte first line and short lines after it — reported unread forever, on
	# a file jq had read cover to cover, at a rate of about one file in every
	# 4095. `<= 1` restores 1448/1448 and cannot double count: the analysis awk
	# keys the row on the filename.
	#
	# Residual, deliberately left: a transcript whose FIRST line is blank still
	# yields its first value at line 2 and is still counted unparsed. Zero such
	# files exist in the 1448 here, machines write these, and the cost of finding
	# out is emitting a row per line rather than per file. It lands in the second
	# NOTE arm below, which names its evidence instead of blaming jq.
	#
	# The `F` row costs one line per file and the analysis awk below ignores it.
	xargs -0 jq -r --arg cutoff "$cutoff_iso" '
			(if input_line_number <= 1 then (["F", input_filename] | @tsv) else empty end),
			(if .type=="assistant" then .timestamp as $ts | .message.content[]? |
				select(.type=="tool_use" and ((.name // "")|startswith("mcp__buddylist__")) and $ts >= $cutoff) |
				["C",.id,.name,(.input|tojson|utf8bytelength),
					(if .name=="mcp__buddylist__chat_read" then
						(if (.input.since_last // false) then "since_last"
						 elif (.input.mentions_me // false) or (((.input.mentions // [])|length)>0) then "mentions"
						 elif ((.input.tail // 0)>0) then "tail"
						 elif ((.input.after // 0)>0) then "after" else "bare" end)
					 else "-" end)] | @tsv
			elif .type=="user" then .message.content[]? | select(.type=="tool_result") |
				["R",.tool_use_id,(.content|if type=="string" then utf8bytelength else (tojson|utf8bytelength) end)] | @tsv
			else empty end)
		' <"$transcripts" 2>"$jqerr" >"$events" || true
	parsed=$(awk -F '\t' '$1=="F" && !f[$2]++ {n++} END {print n+0}' "$events")

	echo
	echo "Claude Buddy MCP usage"
	printf 'transcripts\tseen=%s\tparsed=%s\n' "$seen" "$parsed"
	if [ -s "$jqerr" ]; then
		echo "  NOTE: jq stopped early on at least one transcript, and a parse error abandons the rest of its file list; every number below is a FLOOR."
		# The COUNT of jq's complaints, never their text: a parse error message
		# can quote the bytes it choked on (jq 1.6 appends "while parsing '…'"),
		# and this report promises in its own header to print no transcript
		# content. Re-run jq by hand on a transcript if the reason matters.
		echo "  ($(wc -l < "$jqerr" | tr -d ' ') jq error line(s), not shown: they can quote transcript bytes.)"
	elif [ "$parsed" -lt "$seen" ]; then
		# Not a jq failure — jq said nothing — so this arm reports only what it
		# can actually see: a transcript that yielded no value on its first line.
		# Still a FLOOR, because from here "read and empty" and "opened and
		# skipped" look the same.
		echo "  NOTE: $((seen - parsed)) transcript(s) yielded no first-line value and jq reported nothing; every number below is a FLOOR."
	fi
	awk -F '\t' '
		$1=="C" && !seen[$2]++ {name[$2]=$3; mode[$2]=$5; calls[$3]++; args[$3]+=$4; if($3=="mcp__buddylist__chat_read") reads[$5]++}
		$1=="R" && name[$2]!="" && !rseen[$2]++ {results[name[$2]]++; bytes[name[$2]]+=$3; if(name[$2]=="mcp__buddylist__chat_read") {rresults[mode[$2]]++; rbytes[mode[$2]]+=$3}}
		END {
			for(k in calls) printf "%s\tcalls=%d\targ_bytes=%d\tresult_bytes=%d\tavg_result_bytes=%.1f\n",k,calls[k],args[k],bytes[k],results[k]?bytes[k]/results[k]:0
			for(k in reads) printf "chat_read_mode:%s\tcalls=%d\tresult_bytes=%d\tavg_result_bytes=%.1f\n",k,reads[k],rbytes[k],rresults[k]?rbytes[k]/rresults[k]:0
		}' "$events" | sort
else
	echo
	echo "Claude Buddy MCP usage: unavailable ($claude_projects not found)"
fi
