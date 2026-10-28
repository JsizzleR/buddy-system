#!/bin/sh
# subagent-report.sh — which model each subagent ran on and who asked for it,
# what orchestrators read when they land lanes, and how much Bash output buddy
# verbs produce. Counts, ids and byte lengths only: no description, prompt,
# command, path or tool result is ever printed.
#
#   sh scripts/subagent-report.sh [--since YYYY-MM-DD[THH:MM]] [--until ...]
#
# The window is local time; --since defaults to 7 days ago, --until to now (a
# record stamped later than now is out, as one after a given --until is).
# It reads every project under ~/.claude/projects (the subagent default is a
# user setting, so the question is the machine's, not one repo's), read-only.
#
# WHY IT EXISTS (docs/CHEAPER-MODELS-PLAN.md, step 4.3). Section 0 of that
# plan was a hand-run script over four days of transcripts: 90 Agent calls on
# Fable, 17 general-purpose calls that named no model and inherited the
# parent's, buddy verbs 10.3% of all Bash result bytes. Its rule and hook
# (4.1), the operator's default-model line (4.2), the deferred landing skill
# (4.4) and the batch-boundary line (4.5) each name a number this report
# re-reads, so the before and after are the same instrument. It does NOT
# verify a commit's review receipt: it shows the subagent models a session
# used, and a reader compares that with the receipt the lane's commit carries.
#
# WHAT IT READS. Main-session transcripts, `<project>/<session>.jsonl`,
# MODIFIED since --since, then records TIMED in the window; sidechain records
# and `<synthetic>` ones (an API error or interrupt the harness writes) are
# not calls. A tool-use id is counted once across every file read: the harness
# also writes `api-request-blob` records carrying earlier assistant content
# (23 Agent tool-use ids repeated within their own file, across 13 files, over
# 2026-10-24..28, measured), and a copied history would otherwise count every
# call again. A torn line costs the line, never the file; skipped lines and
# unreadable files are counted.
#
# THE FIELDS.
#   Agent calls, by model param and word. The param as written (`(none)`: no
#     param, the call inherits its parent's model), split for `(none)` by
#     subagent type, so an Explore or a fork is never folded into
#     general-purpose. The word is the classifier's: `review` when the
#     description plus the prompt's first 400 characters match the plan
#     draft's review regex (review, panel, lens, cross-check, verify,
#     adversarial, audit, ...; plus ruling, which the draft missed), `other`
#     when not, and `unclassified` when the call has NO description: those are
#     listed by session and tool-use id, never guessed from the prompt.
#   Every call, grouped, with the model it RAN on: the agent id the call's
#     result names (`toolUseResult.agentId`), joined to
#     `<session>/subagents/agent-<id>.jsonl` and that file's first request's
#     model. `(no result)`: the call has no result in the file; `(no agent
#     id)`: a result naming none (refused by a hook, failed, or several
#     results on one record, which cannot be told apart); `(no transcript)`:
#     an id with no file; `(no request)`: a file with no request in it.
#   Unnamed general-purpose calls (no param; the type general-purpose or
#     omitted, which the Agent tool runs as general-purpose), with how many
#     were review-shaped and how many of those ran, and what all of them ran
#     on: steps 4.1 and 4.2's targets. A refused call still appears, as
#     `(no agent id)`: the targets count the ones that ran.
#   Agent calls a SUBAGENT made, counted apart and in no table: they are not
#     the main session's calls.
#   Subagent transcripts by session and model, every `agent-*.jsonl` under a
#     main transcript read here whose first request is in the window, whether
#     or not a call joined to it.
#   Orchestrators: a session whose main thread ran `herdr agent start` in
#     command position (as wake-report.sh matches `buddy wait`: a commit
#     message naming it is no launch). Its launches; its prompt at the first
#     launch (input + cache-read + cache-write tokens of the request carrying
#     that call); its landing reads: Read or Bash results over 10,240 bytes of
#     text whose path or command names another lane's worktree
#     (`/.claude/worktrees/<n>`) or branch, the names gathered from the cwd and
#     branch every transcript in the window first records in a harness
#     worktree (a branch may hold a `/`); and how many distinct lanes those
#     reads named, a worktree and its branch being one lane. A lane's name is
#     matched inside the reader and printed only as that count.
#   Bash results: count and UTF-8 bytes, and those whose command runs a buddy
#     verb in command position, by verb. A launch and a buddy verb are matched
#     with single-quoted spans blanked: nothing inside one runs. A word that is not one of the verbs
#     the binary dispatches prints as `(other)`, never as itself.
#
# IDS. A session id prints only when the file name is a UUID and a tool-use id
# only when it is `toolu_...`; anything else prints `(other)`.
#
# WHAT IT MISSES, on purpose. A `;` inside a double-quoted span still reads as
# a separator, so `echo "x; buddy who"` counts as a buddy call (Codex; OPEN: a
# double-quoted `$(buddy ...)` does run, and blanking both would lose it). A lane named with a common word (`docs`) is
# matched wherever that word stands alone in a command. A worktree made
# outside `.claude/worktrees/` is no lane here. A call's model is the
# subagent transcript's first request's; a subagent cannot switch. The window
# filters a call by its own time, so a call made the second before --since in
# a file modified after it is out. Section 0's draft counted every call in a
# file modified in the window, whatever its time.
#
# KNOBS
#   CLAUDE_PROJECTS_DIR  where transcripts live (default ~/.claude/projects;
#                        cost-report's knob)
set -eu
command -v python3 >/dev/null 2>&1 || { echo "subagent-report: missing required command: python3" >&2; exit 2; }
exec python3 -I -B "$(dirname -- "$0")/subagent-report.py" "$@"
