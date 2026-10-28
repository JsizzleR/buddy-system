#!/bin/sh
# Privacy-preserving Buddy cost report. It reads aggregate counts and byte
# lengths only: no inbox body, chat text, prompt, or tool result is printed.
#
# The last two sections (docs/PROSE-BUDGET-PLAN.md section 5) are about the
# checkout THIS SCRIPT lives in, not the cwd's: its prose budget, and the
# transcripts whose recorded cwd is that checkout or one of its worktrees.
#   Fixed prose      every class of docs/prose-budget.tsv with its members,
#                    bytes, ceiling and headroom. The members come from
#                    `check-prose-budget.sh --members`, the check's own
#                    enumeration: a second list here would drift from it, and
#                    a file the tier budgets that the report never counts is
#                    the miss this exists to prevent. A class total is the sum
#                    of its members, as the check sums it; a SECTION key
#                    (`CLAUDE.md#Decisions`) is printed under its file and
#                    labelled, never added; a member of two classes (CLAUDE.md
#                    is every-request and, through AGENTS.md, read-first) is
#                    labelled in the second. No grand total: one would count
#                    those bytes twice.
#   Context billed   one deduplicated request set: an assistant record with
#                    usage, in the window, never `<synthetic>`, keyed by
#                    message.id else requestId (measured 2026-10-28 over this
#                    repo's 14,076 records: 6,861 unique either way, the two
#                    ids 1:1, usage identical across a message's records, so
#                    the plan's two counts were a fallback rule, not two
#                    populations). Subagent requests count (they are billed);
#                    `sessions` is main-thread session ids.
# Skill exposure is computed over that same set: a request carries a skill
# body when ITS TRANSCRIPT FILE loaded it earlier (a Skill call naming it, a
# `/name` command, or a whole-file Read of its SKILL.md), so a subagent's file
# never inherits its parent's load and a resumed file carries the copied one.
# Residuals, named rather than estimated: a body re-attached after /compact is
# not seen; a request first seen in a file without the load (resume order) is
# not carried. Where a number is not in the transcripts at all (what a
# startup read cost the requests after it) the report says NOT MEASURED.
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

work=$(mktemp -d "${TMPDIR:-/tmp}/buddy-cost.XXXXXX")
trap 'rm -rf "$work"' EXIT INT TERM
events="$work/events"; transcripts="$work/transcripts"; jqerr="$work/jqerr"; stats="$work/stats"
: >"$events"; : >"$stats"
# The prose classes, from the check (header). Empty when this checkout has no
# table or the check cannot run: the section then says unavailable.
: >"$work/members"
prose_ok=
if [ -f "$root/docs/prose-budget.tsv" ] && [ -f "$root/scripts/check-prose-budget.sh" ] &&
	sh "$root/scripts/check-prose-budget.sh" --members >"$work/members" 2>/dev/null; then
	prose_ok=1
fi
# This checkout's worktree paths (the main one first): a transcript record is
# this repo's when its cwd is one of them or under one, which also covers a
# lane's worktree under .claude/worktrees after it was removed.
# NUL-delimited: without -z git quotes a path holding a tab or a newline, and
# the quoted spelling matches no recorded cwd (Codex code pass).
git -C "$root" worktree list --porcelain -z 2>/dev/null >"$work/roots" || : >"$work/roots"
roots_json=$(jq -Rsc 'split("\u0000") | map(select(startswith("worktree ")) | ltrimstr("worktree "))' <"$work/roots")
rf_json=$(awk -F '\t' '$1 == "read-first" { print $2 }' "$work/members" | jq -R . | jq -cs .)
stat_of() { sed -n "s/^$1=//p" "$stats"; }

if [ -d "$claude_projects" ]; then
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
	#
	# The prose sections' rows ride the same pass (U request, T tool call, K
	# skill load), only for a record whose cwd is this checkout's; every field
	# is a number, a fixed flag, or a key the awk below matches and never
	# prints (an id, a file name, a skill name). A tool name is printed by the
	# MCP section, so a name that is not one of the server's tools
	# (internal/buddylist/mcp.go's mcpToolTable) is folded into `(other)`: a
	# model-written name is transcript text like any other, and a SHAPE test
	# still passed `mcp__buddylist__<any lowercase words>` (Codex code pass).
	# The window compares at second precision: `…00.500Z` sorts before
	# `…00Z` as a string (Codex code pass).
	xargs -0 jq -r --arg cutoff "$cutoff_iso" --argjson roots "$roots_json" --argjson rf "$rf_json" '
			def num: if type == "number" then floor else 0 end;
			def inwin: type == "string" and .[0:19] >= ($cutoff | .[0:19]);
			def known: IN("chat_send", "chat_read", "chat_status", "chat_ack", "chat_who", "dm", "set_status");
			def path_is($p): type == "string" and (. == $p or endswith("/" + $p));
			def ranged: (.input.offset // null) != null or (.input.limit // null) != null;
			def reads($p): (.name == "Read" and (.input.file_path | path_is($p)))
				or (.name == "Bash" and (.input.command | type == "string" and
					any(splits("[|;&\n]"); test("^\\s*cat\\s") and (contains(" " + $p) or contains("/" + $p)))));
			def whole($p): (.name == "Read" and (ranged | not) and (.input.file_path | path_is($p)))
				or (.name == "Bash" and (.input.command | type == "string" and (test("[|;&<>\\n]") | not) and
					test("^\\s*cat(\\s+\\S+)*\\s+(\\S*/)?" + ($p | gsub("\\."; "\\.")) + "(\\s+\\S+)*\\s*$")));
			def skill: if .name == "Skill" then (.input.skill | strings)
				elif .name == "Read" and (ranged | not) then
					(.input.file_path | strings | capture("(^|/)skills/(?<s>[^/]+)/SKILL\\.md$")? | .s)
				else empty end;
			(if input_line_number <= 1 then (["F", input_filename] | @tsv) else empty end),
			(if .type=="assistant" then .timestamp as $ts | .message.content[]? |
				select(.type=="tool_use" and ((.name // "")|startswith("mcp__buddylist__")) and ($ts | inwin)) |
				["C",.id,(if (.name | ltrimstr("mcp__buddylist__") | known) then .name else "mcp__buddylist__(other)" end),(.input|tojson|utf8bytelength),
					(if .name=="mcp__buddylist__chat_read" then
						(if (.input.since_last // false) then "since_last"
						 elif (.input.mentions_me // false) or (((.input.mentions // [])|length)>0) then "mentions"
						 elif ((.input.tail // 0)>0) then "tail"
						 elif ((.input.after // 0)>0) then "after" else "bare" end)
					 else "-" end)] | @tsv
			elif .type=="user" then .message.content[]? | select(.type=="tool_result") |
				["R",.tool_use_id,(.content|if type=="string" then utf8bytelength else (tojson|utf8bytelength) end)] | @tsv
			else empty end),
			((.cwd | if type == "string" then . else "" end) as $cwd
			 | if $cwd == "" or (any($roots[]; . as $r | $cwd == $r or ($cwd | startswith($r + "/"))) | not) then empty else
				(.sessionId | if type == "string" then . else "" end) as $sid
				| (if .isSidechain == true then 1 else 0 end) as $sc
				| (.timestamp | inwin) as $in
				| if .type == "assistant" and (.message | type) == "object" and .message.model != "<synthetic>" then
					(if $in and (.message.usage | type) == "object" then
						(.message.usage) as $u
						| ["U", input_filename, ((.message.id // .requestId // "") | tostring), $sid, $sc,
							($u.input_tokens | num), ($u.cache_read_input_tokens | num),
							($u.cache_creation_input_tokens | num), ($u.output_tokens | num)] | @tsv
					 else empty end),
					(.message.content[]? | select(type == "object" and .type == "tool_use" and (.id | type) == "string"
						and (.input | type) == "object") | . as $t
						| (["T", input_filename, .id, $sid, $sc, (if $in then 1 else 0 end),
							(if any($rf[]; . as $p | $t | reads($p)) then 1 else 0 end),
							(if (.input | tojson | contains("decisions.md")) then 1 else 0 end),
							(if whole("docs/decisions.md") then 1 else 0 end)] | @tsv),
						(skill | ["K", input_filename, $sid, $sc, ., $t.id] | @tsv))
				  elif .type == "user" and (.message | type) == "object" then
					((.message.content | strings | capture("<command-name>/?(?<c>[^<]{1,64})</command-name>")? | .c)
					 | ["K", input_filename, $sid, $sc, ., "-"] | @tsv),
					(.message.content[]? | select(type == "object" and .type == "tool_result" and .is_error == true
						and (.tool_use_id | type) == "string") | ["E", .tool_use_id] | @tsv)
				  else empty end
			 end)
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

	# The prose sections' numbers, from the U/T/K rows, in file order (jq keeps
	# it, so a load precedes the requests after it). One request is counted
	# once, at its first sight; a tool call likewise, by its id. A skill load
	# counts only for a per-session member's skill name. Only numbers leave.
	: >"$work/prompts"
	LC_ALL=C awk -F '\t' -v members="$work/members" -v prompts="$work/prompts" '
		BEGIN {
			while ((getline l < members) > 0) {
				split(l, f, "\t")
				if (f[1] == "per-session") { n = f[2]; sub(/\/SKILL\.md$/, "", n); sub(/.*\//, "", n); sp[n] = f[2]; sb[n] = f[3] + 0 }
			}
		}
		# A load is a call; its failed result (is_error) retracts it, before
		# any request can follow, unless the file had already loaded that
		# skill (Codex code pass). A slash command has no result to fail.
		$1 == "K" && ($5 in sp) {
			if (!(($2, $5) in ld)) { ld[$2, $5] = 1; lst[$2] = lst[$2] "\t" $5; if ($6 != "-") kid[$6] = $2 "\t" $5 "\t" $3 "\t" $4 }
			if ($4 == 0) ls[$3]++
			next
		}
		$1 == "E" && ($2 in kid) {
			split(kid[$2], e, "\t"); delete kid[$2]; delete ld[e[1], e[2]]
			lst[e[1]] = ""; for (k2 in ld) { split(k2, f2, SUBSEP); if (f2[1] == e[1]) lst[e[1]] = lst[e[1]] "\t" f2[2] }
			if (e[4] == 0 && --ls[e[3]] <= 0) delete ls[e[3]]
			next
		}
		$1 == "U" && $3 != "" && !useen[$3]++ {
			q++; if ($5 == 0) ss[$4] = 1; else sq++
			ti += $6; cr += $7; cw += $8; out += $9
			print $6 + $7 + $8 > prompts
			nl = split(lst[$2], a, "\t"); any = 0
			for (i = 2; i <= nl; i++) { rode[a[i]]++; any = 1 }
			r += any
			next
		}
		$1 == "T" && !tseen[$3]++ {
			if ($6 == 1 && $8 == 1) { dm++; if ($9 == 1) dw++ }
			if ($5 == 0 && ++ord[$4] <= 10 && $7 == 1) ob[$4] = 1
		}
		END {
			for (s in ss) { m++; if (s in ls) nload++; if (s in ob) nobey++ }
			ps = 0
			for (n in rode) ps += int((sb[n] + 3) / 4) * rode[n]
			printf "sessions=%d\nrequests=%d\nsubagent_requests=%d\ncache_read=%.0f\ncache_write=%.0f\ninput=%.0f\noutput=%.0f\n", m, q, sq, cr, cw, ti, out
			printf "loaded=%d\nrode=%d\nobeyed=%d\nps_tokens=%.0f\n", nload, r, nobey, ps
			printf "dec_mentions=%d\ndec_whole=%d\n", dm, dw
		}' "$events" >"$stats"
else
	echo
	echo "Claude Buddy MCP usage: unavailable ($claude_projects not found)"
fi

# Codex passes in the window: every `codex@` from a commit's review receipt
# (`review:` or `Review:`, last in the body, sometimes wrapped) to the body's
# end, over what HEAD reaches. A pass no receipt names is not counted.
if git -C "$root" rev-parse --git-dir >/dev/null 2>&1; then
	passes=$(git -C "$root" log --since="$cutoff_iso" --format='@@@commit%n%B' 2>/dev/null |
		awk '/^@@@commit$/ { on = 0 } tolower($0) ~ /^review:/ { on = 1 } on { n += gsub(/codex@/, "") } END { print n + 0 }')
	passes="passes in window: $passes, from receipts naming codex@"
else
	passes="passes NOT MEASURED: not a git checkout"
fi
q=$(stat_of requests); m=$(stat_of sessions)
if [ -n "$q" ]; then
	ann_ps="loaded in $(stat_of loaded) of $m sessions; rode $(stat_of rode) of $q requests"
	ann_rf="obeyed in $(stat_of obeyed) of $m sessions, by a Read/cat of a listed file in the first 10 tool calls"
else
	ann_ps="loads NOT MEASURED: no transcripts read"
	ann_rf="obedience NOT MEASURED: no transcripts read"
fi

echo
if [ -n "$prose_ok" ]; then
	echo "Fixed prose (docs/prose-budget.tsv)"
	LC_ALL=C awk -F '\t' -v table="$root/docs/prose-budget.tsv" \
		-v a1="" -v a2="$ann_ps" -v a3="$ann_rf" -v a4="$passes" '
		function row(ind, k, b, note,   s, c) {
			s = sprintf("%s%-" (44 - length(ind)) "s bytes=%-8d", ind, k, b)
			if (k in ceil) {
				c = ceil[k]
				s = s sprintf(" ceiling=%-8d headroom=%d%%", c, int((c - b) * 100 / c))
				if (b > c) s = s " OVER"
			} else s = s " ceiling=none"
			s = s sprintf("  est_tokens=%d", int((b + 3) / 4))
			if (note != "") s = s "  (" note ")"
			print s
		}
		BEGIN {
			while ((getline l < table) > 0) {
				if (l ~ /^#/ || l ~ /^[[:space:]]*$/) continue
				split(l, f, "\t"); ceil[f[1]] = f[2] + 0
			}
		}
		{ cl[NR] = $1; k[NR] = $2; b[NR] = $3; n = NR }
		END {
			split("every-request per-session read-first codex-pass", C, " ")
			ann[1] = a1; ann[2] = a2; ann[3] = a3; ann[4] = a4
			for (i = 1; i <= 4; i++) {
				t = 0
				for (j = 1; j <= n; j++) if (cl[j] == C[i]) t += b[j]
				row("", C[i], t, ann[i])
				for (j = 1; j <= n; j++) {
					if (cl[j] != C[i]) continue
					row("  ", k[j], b[j], (k[j] in first) ? "also in " first[k[j]] ", counted in each" : "")
					if (!(k[j] in first)) first[k[j]] = C[i]
					for (x = 1; x <= n; x++) {
						if (cl[x] != "-" || done[x] || index(k[x], k[j] "#") != 1) continue
						row("    ", k[x], b[x], "a section: inside " k[j] ", not added"); done[x] = 1
					}
				}
			}
			for (x = 1; x <= n; x++) {
				if (cl[x] != "-" || done[x]) continue
				if (!hdr++) print "in no class (budgeted, not added to any total)"
				if (b[x] == "") print "  " k[x] "  GONE"; else row("  ", k[x], b[x], "")
			}
		}' "$work/members"
else
	echo "Fixed prose: unavailable (no docs/prose-budget.tsv beside this script, or its check could not enumerate)"
fi

echo
if [ -n "$q" ] && [ "$roots_json" != "[]" ]; then
	echo "Context billed (transcripts whose cwd is this checkout or a worktree of it, last $days day(s))"
	printf 'sessions=%s  requests=%s  subagent_requests=%s  cache_read=%s  cache_write=%s  input=%s  output=%s\n' \
		"$m" "$q" "$(stat_of subagent_requests)" "$(stat_of cache_read)" "$(stat_of cache_write)" "$(stat_of input)" "$(stat_of output)"
	sort -n "$work/prompts" | awk '{ v[NR] = $1; s += $1 }
		END { if (NR) printf "prompt mean=%.0f p50=%d p90=%d\n", s / NR, v[int((NR * 50 + 99) / 100)], v[int((NR * 90 + 99) / 100)]
			else print "prompt mean=0 p50=0 p90=0" }'
	er=$(awk -F '\t' '$1 == "every-request" { t += $3 } END { print t + 0 }' "$work/members")
	awk -v er="$er" -v q="$q" -v ps="$(stat_of ps_tokens)" -v cr="$(stat_of cache_read)" -v ok="$prose_ok" 'BEGIN {
		printf "est_share_of_cache_reads (estimates: bytes/4 x requests carrying it / cache_read): "
		if (!ok) print "NOT MEASURED: no prose table"
		else if (cr == 0) print "every-request=n/a  per-session=n/a  read-first=NOT MEASURED"
		else printf "every-request=%.1f%%  per-session=%.1f%%  read-first=NOT MEASURED\n", int((er + 3) / 4) * q * 100 / cr, ps * 100 / cr }'
	dm=$(stat_of dec_mentions); dw=$(stat_of dec_whole)
	echo "docs/decisions.md tool calls: mentions=$dm whole_file=$dw ranged_or_search=$((dm - dw))"
elif [ "$roots_json" = "[]" ]; then
	echo "Context billed: unavailable ($root is not a git checkout, so no transcript is this repo's)"
else
	echo "Context billed: unavailable ($claude_projects not found)"
fi
