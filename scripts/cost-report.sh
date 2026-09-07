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
	echo "Ledger context fan-out: unavailable (set BUDDY_LEDGER=/path/to/.git/buddy.db)"
fi

if [ -d "$claude_projects" ]; then
	events=$(mktemp "${TMPDIR:-/tmp}/buddy-cost.XXXXXX")
	trap 'rm -f "$events"' EXIT INT TERM
	find "$claude_projects" -type f -name '*.jsonl' -print0 2>/dev/null |
		xargs -0 -n 1 jq -r --arg cutoff "$cutoff_iso" '
			if .type=="assistant" then .timestamp as $ts | .message.content[]? |
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
			else empty end
		' 2>/dev/null >"$events"

	echo
	echo "Claude Buddy MCP usage"
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
