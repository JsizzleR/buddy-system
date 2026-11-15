# Buddy outside git: one pane over independent investigations

Status: PLAN, nothing built (2026-11-14). **This supersedes the version at
1d36f8b**, which answered another question (file claims in one untracked
directory, through a `.git` pointer file): the operator rejected the pointer
file and re-scoped the use, below. Written by the `nongit-plan-2` lane on Fable
5.2 at high effort, from the code at main 1d36f8b, the installed `buddy` at
9658d75 (`git diff --stat 9658d75 1d36f8b` names this file only) and the
measurements of section 2. One Codex pass ran over the finished draft; section
11 holds its findings and what became of each. It starts from
`docs/BEYOND-CODE-PLAN.md` (convention A, options C and D).

Register: each part names the failure it answers, the number it rests on and
how to re-run it, the alternative it refuses and what it costs. A number not
taken says NOT MEASURED. What only the operator can decide is in section 10.

## The request

Relayed by the orchestrator, each marked as said in its pane on 2026-11-14:

- about 06:32Z: "open a lane to plan out how we can use the buddy system/support
  using it on a project that isnt git tracked/operator doesnt want git tracking"
- about 06:33Z: "the use case btw, is mostly then orchestrating multiple
  sessions from one pane"
- about 17:35Z, on the first plan's questions 1 to 3: "on 1. what is the point
  of a .git pointer file? when we said no git/not a git tracked repo? 2. no
  3. most lanes arent working ont he same project when coordinating for this
  use case. e.g., opening 10 investigations on different items, with an
  orchestrator to help you monitor all 10 at once and pipe questions/answers
  back and forth"
- about 17:38Z: "i think that should help answer the rest, were going in a
  different direction here compared to what i think you imagined"
- about 17:41Z, on the orchestrator's restatement: "yes, open the lane"

The restatement is the orchestrator's (its advice, which the operator approved
by opening this lane): no git at all, and no git-named entry. About ten
INDEPENDENT investigations, each on its own item and mostly in its own
directory. One pane sees who is working, stuck, waiting on a question or done,
and carries questions and answers between the operator and the lanes. File
claims and the edit gate are beside the point; the question is how sessions
that share no repository share ONE coordination ledger, and whether they need
one.

## 1. The straight answer

**Outside git, buddy is off, completely and silently** (N0). All six hook
verbs exit 0 and print nothing; every typed verb exits 1 with "not a git
repository". A lane there has no row, no inbox, no wait, no handoff signal.
`BUDDY_LEDGER` changes none of it: `buddy` never reads it.

**The harness alone already does a usable part of this job** (X1 to X4), with
no buddy and no git: a roster of every session with a busy or idle word, a
message that wakes an idle session and arrives marked as a peer's, herdr's
`blocked` and `done` states with a wait on them. For ten short investigations
and one question at a time, that is enough, and section 4 says how to run it.

**What it does not have, and buddy does:** a queue that outlives the turn it
was sent in, with a record of what was delivered; state the LANE declares
(waiting, and on what; an item's outcome with a note; its context size against
a handoff size); an address that survives a coordinator's handoff
(`orc-<fleet>`); one channel that a Codex lane can use too. Section 4 gives
each with its evidence.

**Recommendation.** Stage 0: run one batch on the harness alone and take five
counts (section 9). Stage 1, if the counts or the operator say buddy is wanted
here: a **fleet ledger**, a coordination-only ledger outside every project,
named by `BUDDY_FLEET=<name>` on the lane's tab at launch and read only where
git finds no ledger. No schema change; no settings change (the hooks already
run in every session, X5); one new flag (`buddy init --fleet <name>`).

**What this plan does not give outside git, said once:** no file claim, no gate
or brake of its own, no landing. (A write that lands in a governed repository
is judged by that repository's ledger, as today.) A fleet ledger holds names,
messages, waits and outcomes. The gate never opens it (section 5.4), so nothing
in it can refuse a tool call or allow one a repository's ledger refuses.

## 2. What was measured

All on 2026-11-14, macOS 27.2.1, Claude Code 2.1.325, herdr 0.9.5, git 2.54.0,
`~/bin/buddy` at 9658d75 (`go version -m`, unmodified).

**N rows:** the one script in section 12, run in an empty directory under the
lane's scratchpad; the table is its last run. Throwaway ledger, eleven minted
session ids registered by hand, hook payloads piped to the hook verbs. Because
today's buddy keeps a ledger nowhere but a git directory, the throwaway ledger
sits in a `git init`ed scratch directory, the "stand-in". That is the
fixture's scaffolding and is recommended to nobody: it shows what the verbs do
once a ledger is open (N1 to N8), and N0, N5, N7 and N9 show what happens
where none is found. Sizes depend on the fixture's own labels, notes and path.

| # | Fixture | Result |
| --- | --- | --- |
| N0 | a plain directory, no git above it | `hello`, `busy`, `beat`, `idle`, `bye` with their hook payloads: each exit 0, 0 bytes on stdout and stderr; `gate` on a Write there: ALLOW. `status`, `sessions`, `inbox`, `wait check`, `msg`, `claim` (a slot scope): each exit 1, "not a git repository". The same `sessions` with `BUDDY_LEDGER` naming the stand-in's ledger: exit 1, the same words. Controls: `sessions` from the stand-in exits 0, and so does a registered session's `status` there (N1) |
| N1 | a coordinator O and ten lanes `i01` to `i10` registered in the stand-in by hand; each holds a claim on `.buddy/slot/<its name>`; O holds `orc-standin` and declares one wait on all ten item claims | 11 claims open; the wait is taken (exit 0) |
| N2 | lanes 3, 7 and 9 each ask: `msg orc-standin "NEED INPUT: ..."` (a 155-byte body), then `wait --until 2h --note "NEED INPUT: ..."` | 3 of 3 sends and 3 of 3 waits exit 0; lane 3's result line is one line of 745 bytes, reciting all ten claims the recipient waits on; its wait reads "WAITING on a timer" with its note |
| N3 | lanes 5 and 6 finish: `release <item> --outcome pass\|fail --note "DONE: ..."` | both exit 0, "outcome PASS" and "outcome FAIL" recorded; the first line of O's next `wait check`, 631 bytes, names all ten claims, the two released ones with outcome and note |
| N4 | what O reads, 11 sessions | `sessions` 2,723 bytes, 11 lines: a lane that asked reads `waiting 2s claims 1` (no note), a finished one reads as a live row with no claim; `wait ls` 1,182 bytes, 4 lines, 3 of them carrying a lane's note (the fourth is O's own wait); `ls` 1,532; `ls --all` 1,876, and of its 2 rows for the released items, 0 carry the outcome or the note; `inbox` 495 bytes, 3 lines, each carrying the question; `who i03` 928 bytes, 12 lines |
| N5 | O answers lane 3 (`msg i03 ...`, message #4); then lane 3's `beat`, run in the plain directory the lane lives in; then the lane reads by hand, `cd <stand-in> && buddy inbox` | the send queues and its line says "the ledger has no live harness process for it" (no wake address); the beat prints 0 bytes and `sent 4` reads "0 of 1 with a recorded delivery"; the hand read prints one line carrying #4 and its body, and `sent 4` reads 1 of 1 |
| N6 | control: the same hooks for lane 4 with its cwd INSIDE the stand-in | `beat` delivers the queued message (202 bytes); `idle` records it: the row reads `idle 0s`, with a pid |
| N7 | which shapes the ledger takes | a relative file scope (`notes.md`): granted, relative to the stand-in, a file no lane edits; an absolute scope: refused ("scopes are repo-relative"); parking a slot-only claim in a run: refused ("coordination is never parked"), a file-scope claim parks (control); `wait --ready HEAD`: refused, no commit (its succeeding control is the first plan's K0, with a commit: WAITING); `wait --on <run> --note "READY: ..."` then the run's release with an outcome: LANDED PASS; a slot-only item claim handed to another session (`release i08 --to i01`): handed. The brake, on one unclaimed file inside the stand-in and lane 10's own plain directory: before `pause i10`, the file inside: ALLOW; paused (the roster row reads PAUSED): a write in the lane's plain directory ALLOW, the file inside DENY; after `resume i10`, the file inside: ALLOW |
| N8 | O blocks on two item claims (`wait --on i07 --on i09 --block`); the releases start once it prints BLOCKING | 20 s after the first release (exit 0): still blocked; after the second: exit 0, one LANDED line |
| N9 | hook wall time, 20 calls each, 0 of 20 failed in every cell, min / median / max ms, load average 14.6 on 12 cores (other lanes' test tiers were running) | no ledger found (plain directory): `beat` 38.5 / 50.8 / 58.7, `busy` 22.7 / 29.6 / 49.3, `idle` 32.0 / 35.9 / 49.9; ledger found: 27.4 / 33.8 / 67.6, 20.3 / 30.1 / 608.1, 24.8 / 29.3 / 36.2. Two earlier runs of the same loop (before it counted failures), medians only: at load 7.3, 21.3, 20.8, 21.1 against 16.1, 15.4, 16.3; at load 21.5, 40.3, 44.6, 33.6 against 30.7, 29.6, 24.8, with no-ledger maxima of 119.0 and 108.9 |

**X rows:** the machine's own live sessions, read once each, read-only.
Counts, key names and sizes only; no name, path or message text was kept.

| # | What | Result |
| --- | --- | --- |
| X1 | `claude agents --json`, one sample at 17:52:37Z | 18 rows, 4,449 bytes (247 a row). Seven keys on every row: `pid`, `cwd`, `kind`, `startedAt`, `sessionId`, `name`, `status`. `status`: 15 busy, 3 idle. `waitingFor` on 0 rows (D-056 records it as set while a session waits for an approval; none was, so its shape is NOT MEASURED here). Of the 18 working directories, 0 are outside git and 0 are in a repository with no `buddy.db` |
| X2 | `herdr agent list`, the same second | 17 rows, 6,683 bytes (393 a row), 14 keys a row, among them `agent_status`, `cwd`, `pane_id`. `agent_status`: 8 working, 7 done, 2 idle. `herdr agent wait <target> --until <state>` takes `idle`, `working`, `blocked`, `done`, `unknown` (its `--help`; not run) |
| X3 | the harness's `ListAgents` tool, seconds later | the caller and 17 peers, one line of about 80 bytes each (name, a ref, kind, state, age): 8 busy, 6 shell, 3 idle. Not the same instant as X1, and the counts still fit one reading: X1's 15 busy are X3's 8 busy, 6 shell and the caller. Three views of the same sessions, three vocabularies |
| X4 | the harness's `SendMessage` tool, its description as this session loaded it; NOT exercised by this lane | to a name from `ListAgents`; queued, drained at the receiver's next tool round; a receiver in another permission mode holds it for its user's approval and may let it expire; a notice comes back when a local session holds or refuses it; `notify_when_idle` asks for one notice when a local session next goes idle or exits. Exercised before: D-039 (2026-09-23: it woke an idle session, enqueued and dequeued 5 ms apart, marked as a peer's, the framing not forgeable from the body) and D-132 (4,898 wakes sent in 14 days of this machine's transcripts) |
| X5 | which events in the user-level settings run a buddy hook verb (a read-only parse of the file) | `hello` on SessionStart, `gate` on PreToolUse (`Edit\|Write\|NotebookEdit\|Bash`), `beat` on PostToolUse (`*`) and, for `mcp__.*`, on PreToolUse and PostToolUseFailure, `busy` on UserPromptSubmit, `idle` on Stop, `bye` on SessionEnd. They are user-level, so they already run in every session whatever its cwd: N0 is what they do there |
| X6 | this checkout's own roster, `buddy sessions` run once here (a read), the byte length of each ` live ` row | 9 rows: 111 to 302 bytes, median 297 (a real row carries a pid, a pane, a context size and a base that the fixture's rows lack) |

Read from the code, not run (file and line at 1d36f8b): the five lifecycle
hooks return silently on `errNoLedger` (`internal/cli/cli.go:1703`, 1954,
2022, 2091, 2200) and the gate goes on with no home ledger (2804); the wake
address needs one live registered process and a quiet recipient
(`wake.go:126`); a drain is at most 20 messages and 8 KiB (`cli.go:2401`); the
handoff line is delivered at the `busy` hook (`handoff.go:115`); a row is
stale 30 minutes after it was last seen (`store/store.go:29`); the typed verbs `msg`,
`inbox`, `sent`, `wait` (but `--ready <commit>`), `ls`, `claim`, `release`,
`sweep`, `ids`, `pause` and `resume` use nothing of git once the ledger is
open, and `who`, `status` and `sessions` use it only for the base sample.

Not measured, each named where it matters: a live fleet of sessions outside
git (every lane here is a minted id); a SendMessage round trip by this lane;
`herdr agent wait`; what an investigation lane in auto mode still stops for;
questions per hour from ten lanes; Codex lanes; anything about stage 1, which
is not built.

## 3. The workflow, step by step (brief item 1)

"In a repository" is today's fleet recipe (the `buddy-fleet` skill). "Outside
git" is the same step today where no ledger is found. "Harness alone" is
stage 0. "Fleet ledger" is stage 1, not built.

| Step | In a repository today | Outside git today | Harness alone | Fleet ledger (stage 1) |
| --- | --- | --- | --- | --- |
| open ten lanes | `herdr tab create --cwd <dir> --label <l> --env BUDDY_LABEL=<l>`, `herdr agent start ... "Read <brief file>"`, in parallel (D-056, D-144) | the same; herdr and the brief do not need git | the same | the same, plus `--env BUDDY_FLEET=<name>` |
| confirm each is up | `buddy who <session id>` | exit 1 (N0): no row | `claude agents --json`, `herdr agent list` (X1, X2) | `buddy who <session id>` |
| the lane works | the gate judges its writes | no home ledger, so nothing of the lane's own is enforced (N0). A write whose target, or whose literal `cd`, lands in a governed repository is still judged by that repository's ledger (D-138; the first plan's J1) | the same | the same: a fleet ledger adds no enforcement and removes none |
| the lane asks a question | `buddy msg orc-<project> "NEED INPUT: ..."`, deliver the wake address, `buddy wait --until 2h --note "..."` (N2) | each exit 1 (N0) | `SendMessage` to the coordinator's name (X4) | as in a repository |
| the coordinator sees it | its own hooks drain it at its next tool call, prompt or Stop, or the wake starts a turn (D-039) | nothing to drain | the message starts a turn | as in a repository |
| the operator answers | in the coordinator's pane, in words | the same | the same | the same |
| the answer goes back | `buddy msg <lane> "the operator, in my pane at <time>: ..."`, deliver the wake | exit 1 | `SendMessage` to the lane's name | as in a repository |
| the lane reports and is done | `release <item> --outcome pass --note "DONE: <path>"` and a message (N3) | exit 1 | `SendMessage`; the session goes idle (`done` in herdr) | as in a repository |
| the coordinator watches | `sessions`, `wait ls`, its own `wait check` (N4) | exit 1 | X1, X2, `herdr agent wait` | as in a repository, minus `base` |
| a lane passes its handoff size | told at its next prompt (D-064) | never told | never told | told |
| a stuck approval prompt | not buddy's: a blocked lane runs no verb | the same | `waitingFor` (X1), herdr `blocked` (X2) | not buddy's; read the harness |
| stop a lane | `buddy pause`, enforced by the gate | nothing (N7) | Esc in its pane, or close its tab | nothing from buddy (5.4) |

Two more shapes exist today and are not recommended. **The hand path** (N1 to
N7): every session registers by hand in a ledger kept in some `git init`ed
directory and runs every verb as `cd <there> && buddy ...`. It needs git
somewhere, and the lane's own hooks never see that ledger: no mail arrives
unless the lane reads by hand (N5), no idle report and so no wake address, a
row that reads stale after 30 minutes of honest work, no handoff signal, and a
`pause` that shows on the roster and stops nothing (N7). **Lanes started
inside one such directory** (BEYOND-CODE 1.4): full function (N6), and it is
git plus one shared directory, both of which the operator has refused.

## 4. What the harness gives without buddy, and what buddy adds (brief item 2)

**The stage-0 loop, no buddy:** the coordinator opens the ten tabs as above,
keeps each lane's name, and briefs each lane: "questions and your final
findings go to <coordinator's name> with SendMessage; then stop". It learns of
a blocked or finished lane from `herdr agent wait <lane> --until blocked
--until done`, one per lane under the harness's background runner, or from
`SendMessage`'s `notify_when_idle`; it reads `claude agents --json` when the
operator asks where things stand. Both waits are NOT MEASURED here (X2, X4).
`herdr agent prompt` and `send-keys` are never the relay: they type into the
lane's pane, where a peer's text would arrive as the operator's turn. Codex
lanes: the fleet skill starts them in herdr with `--kind codex`; they have no
`SendMessage`, and how one would ask is NOT MEASURED.

| Need | The harness alone | What buddy adds |
| --- | --- | --- |
| who is there, busy or idle | yes: X1, X2, X3, at about 80 to 393 bytes a row. Three views, three vocabularies; a session parked on a background shell reads `busy` in X1 (D-041 records a harness listing that read `busy` for a lane at its prompt with a background task running) | one roster with a labelled age on every row and `idle`, `waiting`, `claims`, context size (N4) |
| blocked on an approval | yes: `waitingFor`, herdr `blocked` | nothing; a blocked lane cannot run a verb |
| a message that wakes the other side | yes (X4; D-039) | nothing new: buddy's wake IS this message, with a fixed text |
| the message survives | no: it lives in two transcripts. Held for approval across permission modes, and may expire (X4) | a queue in the ledger until drained; `sent` says delivered or queued per recipient; `inbox <id>` re-reads one; `--supersedes` corrects one (N5) |
| "waiting, and on what" | no: idle is idle | `wait --note`, on the roster as `waiting` and in `wait ls` with the note (N2, N4) |
| "done, and how it went" | partly: herdr `done`, an idle notice; the finding is in a message | `release --outcome` with a note, read by the coordinator's own `wait check` (N3) |
| past the handoff size | no | the lane is told at its prompt; the roster shows the size (D-064) |
| an address that survives a handoff | no: a name is one session's | `orc-<fleet>`, a claim slug that moves with `release --to` |
| Codex lanes on the same channel | no | `buddy msg` and `buddy inbox` work under Codex by hand |
| what a message rests on | no | `--measured`, `--lead`, `--relay` (D-045) |
| cache kept warm while parked | no | `wait check` on its 50-minute pace (D-033, D-054) |

**Is it worth it here?** Not for a batch that is short, single-harness and
quiet: ten lanes that each run under an hour, ask at most one question at a
time and report once need nothing in the right-hand column, and stage 1 is a
full-tier change. It becomes worth it at any of four marks, each a stage-0
count (where the line falls is this plan's judgment; "under an hour" and "one
question at a time" are not measured thresholds): more than one question
pending at once (the coordinator needs "who is waiting on what" without
re-reading its transcript); a lane or the coordinator handing off (names and
mail must survive); a Codex lane; a message held, expired or lost. The fixed
cost on each side is small beside a turn: X1 is 4,449 bytes for 18 sessions,
the buddy roster 2,723 for 11.

A third shape, for the record: the coordinator runs the ten as background
subagents inside its one session. No panes, no cross-session messages, and
nothing for the operator to open, approve in or steer; an investigation dies
with the coordinator's session. NOT MEASURED for this workload. The operator's
word was sessions, and this plan stays with them.

## 5. How a session finds a coordination ledger with no git (brief item 3)

### 5.1 Options

| Option | How a hook finds the ledger | Cost and failure |
| --- | --- | --- |
| **E. A fleet NAME in the environment** (recommended): `BUDDY_FLEET=<name>` on the tab, as `BUDDY_LABEL` and `BUDDY_HANDOFF_AT` ride today (D-089, D-064); the ledger is `<fleet home>/<name>/buddy.db` | one `getenv` after git has said "no ledger" | a launch that forgets the variable gives a lane on no roster (9.1); `claude --bg` carries no variable (the fleet skill says so of `BUDDY_HANDOFF_AT`), so fleet lanes are herdr tabs; a session already running cannot join (stage 2) |
| P. A ledger PATH in the environment (`BUDDY_LEDGER`, or a new name) | the same | refused, 5.3 |
| S. A file per session: `<fleet home>/by-session/<session id>` naming the fleet, written by a typed `join` | one `Lstat` in every hook of every session outside a ledger | works for a running session and for `--bg`; a second kind of state outside the ledger, stale files, and `/clear` mints a new id that has not joined (D-034). Stage 2, on its trigger |
| R. A registry keyed by directory | read on every hook outside a ledger | ten directories to register per batch; any other session started under a registered directory joins a fleet it never heard of; stale rows. Cut: membership belongs to the launch, not the directory |
| U. One default per-user fleet for every session outside a ledger | none | every such session on the machine shares one namespace, pays a ledger write per tool call and gets a digest it did not ask for; defaults are narrow here (D-010). Cut; `BUDDY_FLEET=default` is the opt-in spelling |
| M. A marker in the directory (the first plan's M) | a walk up | writes a buddy-named entry into each of ten directories the operator keeps, to mark one batch. Cut |
| D. A ledger above the repositories' ledgers | | cut in BEYOND-CODE (invariants 1, 4, 6), and still cut: a fleet ledger sits BESIDE them and commands nothing |

`<fleet home>` is the operator's to name (question 2); `~/.buddy/fleets` is
this plan's example. A name is `[a-z0-9][a-z0-9-]{0,63}`: it can spell no
path. The ledger is created only by a typed `buddy init --fleet <name>`, never
by a hook: a hook that created what it failed to find would turn a mistyped
name into a private ledger and a lane that reports to nobody.

The coordinator is a session like any other: its own hooks read the variable
from its process environment, so it is STARTED with it (by the operator, or as
a tab the operator's existing orchestrator opens), in a directory with no
ledger. A session that lives in a governed repository can still read a fleet's
roster and send into it by hand, each verb run from a plain directory with the
variable set (`(cd <fleet home>/<name> && BUDDY_FLEET=<name> buddy sessions)`);
that is section 3's hand path without the git, with the same limits (its own
hooks never drain the fleet's mail). Stage 2 is what removes them.

### 5.2 Resolution, the same for every hook verb and every typed verb

1. Discover from the filesystem exactly as today (`openRepo`). A ledger found
   is the session's ledger, and `BUDDY_FLEET` takes no part in choosing it
   (`hello` alone reads it there, to print the notice in the table below). An
   unreadable one is today's verdict, unchanged.
2. Only on `errNoLedger` (provably not a repository, or a repository never
   `buddy init`ed) and only for the verbs of section 6: read `BUDDY_FLEET`.
3. Unset or empty: today's behavior, byte for byte (N0).
4. Set: open `<fleet home>/<name>/buddy.db`.

| State | `hello` | `beat`, `busy`, `idle`, `bye` | typed verbs | `gate` |
| --- | --- | --- | --- | --- |
| variable unset | silent (N0) | silent | exit 1, as today | as today |
| a home ledger exists here | registers there; one line: "`BUDDY_FLEET=<name>` is not used here: this directory has its own ledger" | as today | the home ledger | as today |
| set, the fleet's ledger opens | registers, drains, prints the digest | heartbeat, drain, idle report, context sample, handoff line | work (section 6) | as today |
| set, the name is malformed | one line on stdout saying so | `busy` prints the same line at each prompt; the rest are silent; all exit 0 | exit 1, `refused: ...` | as today |
| set, no such fleet (no directory, or no `buddy.db` in it) | one line: "names no fleet ledger on this machine (looked in <path>): this session is on no roster and gets no mail; its coordinator creates it with `buddy init --fleet <name>`" | `busy` prints the same line at each prompt; the rest are silent; all exit 0 | exit 1 with the same words | as today |
| set, the ledger exists and cannot be opened or read | one line: "fleet ledger unavailable (<cause>): ...", naming the fix (D-084's register) | `busy` prints the same line on stdout at each prompt and exits 0; the rest exit 1 with the cause on stderr, which the wired hook lines drop | exit 1 with the cause | as today |

**Invariant 3, read for a ledger that is not a safety feature.** "No ledger"
and "unreadable" stay two verdicts with two different sentences, in the two
places something reads them: the SessionStart context and every typed verb's
exit. Neither is a DENY, on purpose: a DENY is the gate's, and the gate has no
part in this (5.4). That is the one departure from "exists but unreadable
denies", and the decision record must state it: the fleet ledger guards
nothing, so there is nothing for a closed failure to protect, and a refusal
keyed on an environment variable is the bug D-137 closed. The record must
say the same of invariant 2's sentence "a ledger that exists but cannot be
read denies": it speaks of the safety hooks, and none reads this ledger. What
must not happen is the silent collapse, which is why `hello` prints on stdout
(the wired lifecycle lines end `2>/dev/null; exit 0`, README, so stderr and
the exit status reach nobody), and why `busy` repeats the line at every prompt
while the failure lasts: a fleet that opened at SessionStart and was removed
or went unreadable an hour later would otherwise stop coordinating with
nothing said, and the stale row of 9.1 cannot be read either while the ledger
cannot. One line a turn, on stdout, never a block.

**The other invariants.** 2: chat is untouched; the digest's chat line is left
out in a fleet (no checkout names a room). 4: membership confers nothing; a
brief still names its coordinator by full session id and label (D-083), and
anything a session of this user can do in a repository's ledger by `cd`, it can
do in a fleet's by setting the variable. 6: the fleet home is the user's own,
mode 0700; not a tenancy boundary. 11: stale rows and claims are marked and
never reaped; `sweep --force` stays the operator's. 12: rows are `(session_id,
incarnation)` and `bye` is fenced by `session_procs` exactly as now; the row's
`worktree` column holds the cwd at `hello` (as for a bare repository today),
shown and never used to place a path. 13 folds names as now; 14 still decides
overlap between slot scopes (D-035) and meets no file path.

### 5.3 `BUDDY_LEDGER` stays what it is

CLAUDE.md's line stands and this plan does NOT overturn it: `BUDDY_LEDGER` is
a knob of two reports, and `buddy` never reads it (N0). A new variable that
carries a NAME is proposed instead, for three reasons. (1) A path can name any
file, a repository's own ledger included: sessions outside that repository
would register in it, and its roster and inbox would stop meaning "the sessions
working here". (2) A path makes whichever binary reads it open, and so
migrate, whatever it names; D-136 lets an existing ledger be migrated only by
`$HOME/bin/buddy` or under `BUDDY_MIGRATE=1`. A name under one fixed home can
reach only ledgers `init --fleet` made. (3) A path variable read by "buddy"
invites the next reader to honor it in the gate, which is D-137's bug under a
buddy name. The reports lose nothing: `BUDDY_LEDGER=<fleet home>/<name>/buddy.db
scripts/cost-report.sh` reads a fleet ledger as it reads any other.

### 5.4 Why a fleet ledger can never turn a refusal on or off

- **The gate never opens one.** `cmdGate` keeps today's code: the ledgers it
  reads are found from the filesystem alone (the call's cwd, each named
  path's directory, each directory a literal `cd` or `git -C` reaches; D-137,
  D-138). On `errNoLedger` it goes on with no home ledger (`cli.go:2804`)
  and never reaches step 2 above. The fleet lookup is a separate function,
  called by the five lifecycle hooks and the typed verbs, and is NOT inside
  `openRepo`, `resolveRepo` or `discoverRepo`, which the gate shares.
- **Nothing a gate could act on can be written into one.** There, `claim`
  refuses every scope outside `.buddy/slot/`, refuses `.buddy/slot/gh-stack`
  (the one slot the gate reads in a repository's ledger, D-108), and `pause`
  and `resume` refuse (section 6). No path claim, no brake row and no slot the
  gate knows exists there for a later bug to find.
- **Nothing crosses.** No verb copies a row from a fleet ledger into a
  repository's, or back. Claims never cross ledgers (BEYOND-CODE, option D).
- **Held by tests, not by this paragraph** (stage 1 owes them): the gate's
  existing verdict table re-run with `BUDDY_FLEET` unset, naming no fleet,
  naming a healthy fleet that holds a slot claim and queued mail, and naming a
  corrupt file, each verdict byte-identical to the unset run and each DENY
  beside its ALLOW; and a source-shape gate in `check.sh`, in the two clauses
  CLAUDE.md asks of such gates, that fails when anything reachable from
  `cmdGate` names the fleet resolver.

Two effects that are NOT refusals and stay: the Stop hook holds a turn open
one step to hand over mail that arrived as it ended (`idlewait.go:186`), in a
fleet as in a repository; and an installed `buddy` that cannot run still
refuses every gated call, ledger or none (D-107).

**The cost, plainly.** A lane in a fleet has no brake. N7 is today's version
of that: `pause` taken, the roster says PAUSED, the write is allowed. Stage 1
refuses `pause` in a fleet ledger so that the roster cannot say what is not
true, and tells the caller where the stop is: the lane's own pane. A brake
there would need the gate to honor a ledger the environment names, which is
the widening this plan was told not to make (question 3).

## 6. Verbs in a fleet ledger (brief item 4)

| Verb | In a fleet ledger | Why |
| --- | --- | --- |
| `hello`, `beat`, `busy`, `idle`, `bye` | work. `beat` runs no dirty scan and `idle` no base sample; the authority watch is off | those three need a root (invariant 10 is kept by recording nothing) |
| `msg`, `inbox`, `sent` | unchanged, the wake address included (the pid comes from `session_procs`) | no use of git (N2, N5, N6) |
| `wait` (a timer, `--on`, `--block`), `wait check`, `ls`, `clear` | unchanged | N2, N3, N8 |
| `wait --ready <commit>` | refused, as today (N7) | no commit exists; the note carries the path |
| `sessions`, `who`, `status` | work; `BASE` reads "not kept in a fleet ledger" | the base sample is git's |
| `claim` on `.buddy/slot/<name>` | works: the item's name, `orc-<fleet>`, a resource one lane may use at a time; `--job <pid>` too (D-131) | a slot is a name, resolved against nothing |
| `claim` on any other scope, on `.buddy/slot/gh-stack`, `--shared`, `--based-on` | `refused:` a fleet ledger reserves no file; no gate reads it, and a path here has no root | N7's `notes.md` is a claim on a file nobody edits: announced, not locked (invariant 1); `gh-stack` is the slot the gate reads (D-108) |
| `release`, `--outcome`, `--note`, `--to <target>` | unchanged; `--to` hands an item or `orc-<fleet>` to a successor | N3, N7 |
| `release --in-run`; READY with no commit | parking is refused already (N7). There is no run to be READY for: a lane's report is its DONE, below. A wait on a claim with a note still works where a coordinator wants one (N7) | nothing is integrated, so nothing parks |
| `sweep`, `ids` | unchanged | a plain sweep deletes a released claim after 24 h (`SweepTTL`, `cli.go:32`), and its outcome with it |
| `pause`, `resume` | `refused:` a fleet ledger carries no brake | 5.4 |
| `whose`, `authority` | `refused:` no paths here | they place paths under a root |
| `init --fleet <name>` | creates `<fleet home>/<name>/buddy.db`, or says it is there; prints the `--env` to launch with | the one new flag |
| `gate`, `commit-gate` | never read it | 5.4 |

**"Needs input"**, three options. (a) Existing verbs, recommended: `buddy msg
orc-<fleet> "NEED INPUT: <question>"`, deliver the wake, then `buddy wait
--until 2h --note "NEED INPUT: <topic>"` (BEYOND-CODE 1.4, N2). The roster
shows `waiting`, `wait ls` the topic, the inbox the question; the lane clears
the wait when answered. Its weakness: the roster word is the same for a lane
waiting on the operator and one waiting out its own long run, and the note is
one read away (N4). (b) The harness's own `waitingFor` and `blocked`, which
catch the case (a) cannot: a lane stopped at an approval prompt runs no verb.
Keep both; they answer different questions. (c) A new state or verb: not
proposed. It is a schema column (full tier, D-037's one-way migration) for a
distinction a convention already carries; its trigger is a stage-0 or stage-1
count of questions the coordinator missed because `waiting` did not say whose
move it was.

**Done**: `buddy release <item> --outcome pass|fail --note "DONE: <path of
the findings>"`, then a message to `orc-<fleet>` with the headline, and the
wake. The coordinator holds one wait on all the item claims; each `wait
check` lists every claim, the released ones with outcome and note (N3: 631
bytes for ten). The blocking form is no "tell me at each finish": it returns
only when all have landed (N8), so the per-finish signal is the lane's
message. `ls --all` does not print the outcome (N4); making it do so is a
small view change (stage 3).

**A DONE here is a report, not a landing.** The outcome is the lane's own
word about its investigation (the schema's comment says so of every release).
Nothing merges anywhere. If an investigation ends in a change to a
repository, that change is a new item in that repository, landed through its
own run, tier, tests and review; a fleet's `pass` licenses none of it.

## 7. Monitoring ten at once from one pane (brief item 5)

**Do not poll.** A cycle is a turn, and a turn is at least three requests that
each read the coordinator's whole prompt from cache (D-054: median 3, mean 4.05
over 20 measured checks); the bytes below are small beside that. The loop is
event-driven:

1. A lane's question or DONE arrives with a wake, so the coordinator's turn
   starts within seconds of the send (D-039), already holding the message.
2. Its standing wait on the ten item claims is checked on the keep-alive pace
   the check itself prints (50 minutes on the hour tier). That one call is the
   whole periodic read: it drains mail and lists all ten claims.
3. `sessions` and `wait ls` are read when the operator asks "where are we", or
   when a check shows a lane neither released nor heard from.

| Read, for a coordinator and ten lanes | Bytes | From |
| --- | --- | --- |
| one question, as drained | 165 on average for a 155-byte body | N4 (495 for three, not one drained alone); a body may be 4,096 (invariant 9's cap) |
| a full drain | at most 20 messages or 8 KiB | `cli.go:2401` |
| `wait check` over ten claims | 631 | N3 |
| `sessions` | 2,723, 248 a row; a real roster's rows run longer (X6) | N4, X6 |
| `wait ls` | 1,182 with three lanes waiting | N4 |
| `who <lane>`, when one lane needs a closer look | 928 | N4 |
| the send's own result line, into the lane's context | 745, most of it the coordinator's ten-claim wait recited | N2 |
| harness alone: `claude agents --json`; `herdr agent list` | 247 a row; 393 a row | X1, X2 |

A whole "where are we" read (`wait check`, `sessions`, `wait ls`) is about
4.5 KB for ten lanes. Questions per hour, the number that decides whether one
pane holds ten lanes, is NOT MEASURED; it is stage 0's first count, as it was
BEYOND-CODE's.

**A question's path.** Lane: `msg orc-<fleet>` + wake + wait. Coordinator: the
drain prints it under the lane's label; it shows the operator the question and
the lane's name, and goes on with other lanes. Operator: answers in that pane.
Coordinator: `buddy msg <lane> "the operator, in my pane at <time>: <answer>"`
and the wake. Lane: wakes, drains, `wait clear`, goes on. Two coordinator turns
and one lane turn per round trip, by construction of the loop; not measured.
The lane takes the answer as the operator's only because its brief carries the
relay clause naming this coordinator by full session id and label (D-083,
D-109); without it the lane asks again in its own pane, and it is right to. A
message is never authority (invariant 4).

**What stays in each lane's pane:** a tool approval, and anything on install,
settings, permissions, trust or a more permissive mode. A reply typed in the
coordinator's pane is not that approval. Which permission mode the lanes run
in is the operator's standing decision, never the coordinator's.

**If a wake is not delivered** (the lane forgot, or the harness held it), the
message waits for the coordinator's next successful hook or check: nominally
about 50 minutes when the hour-tier keep-alive is armed, and with no stated
deadline when none is (a late-firing loop, the 5-minute tier, a coordinator
that armed nothing). The harness's own signals cover
that gap at no cost to the lane (`herdr agent wait --until blocked --until
done`, or `notify_when_idle`), and stage 0 measures how often it happens.

## 8. Refuted by the operator

The first plan's recommendations are withdrawn; `git show
1d36f8b:docs/NON-GIT-PLAN.md` has them. **The pointer file (P)** and **the
parent layout (W)**: a `.git` entry is git, and the operator said no git. **A
marker or registry that lets the GATE find a ledger without git** (its stage
2): it answered "claims with teeth in an untracked directory", which this
workload does not ask for; its trigger is now a named directory where two lanes
must not write the same file and git is refused. Until then, where two lanes
share a directory outside git, nothing refuses a collision, and the briefs keep
them apart (one output file or directory per lane).

Three of its measurements still hold and are used above: outside git every
verb is off (its F0; N0 re-takes it per hook verb); the hook verbs ignore
git's own environment variables (its F4, D-137), which is why a fleet is named
by a buddy variable the gate never reads and not by `GIT_DIR`; a hook's
no-ledger path usually costs more than its found path (its J3 for `gate`; N9
for the lifecycle hooks: in eight of the nine pairs of medians across three
runs, by 5 to 17 ms; `busy` in the last run read 29.6 against 30.1). Under
load single calls already pass the 100 ms hook budget of CLAUDE.md's gotchas
on either path (N9: 119.0, 108.9, 608.1). The lookup of 5.2 is added to the
no-ledger path, so stage 1 re-takes N9 with it in place.

## 9. Failure modes and a staged rollout (brief item 6)

### 9.1 Failure modes of stage 1

| Event | What happens | Seen by |
| --- | --- | --- |
| a lane launched without the variable | off, silently, as today (N0) | the coordinator: it minted the session id, and `buddy who <id>` refuses. The brief's first step is `buddy status`; a lane that sees it fail says so in its pane and stops |
| a mistyped fleet name | `hello`'s line; every typed verb exits 1 | the lane, at its first turn |
| the fleet ledger unreadable or removed, at the start or later | 5.2: `hello`'s line, `busy`'s line at each prompt after it, typed-verb errors; no tool call is refused | each session at its next prompt; the coordinator cannot read the roster either |
| the lane's cwd is, or becomes, a directory with its own ledger (it `cd`s into a governed repository; someone runs `buddy init` in its repository) | its hooks follow the home ledger from then on: its fleet row goes stale, fleet mail is not drained. The repository's gate governs its writes there, which is right | the coordinator, as a stale row; `hello`'s "not used here" line if it started there. Closed only by stage 2 |
| `claude --bg`, or a session opened by hand without the variable | not in the fleet | as the first row |
| `/clear` in a lane | a new session id in the same process and environment: a new row; the old row's mail and wait are stranded (D-034, unchanged) | the coordinator: two rows, one going stale |
| a label worn twice, or reused by a later batch | `msg <label>` refused as ambiguous, for good (BEYOND-CODE 3.3) | the sender. A fleet name per batch (`inv-1114`); the coordinator is addressed by its claim slug |
| another `HOME` (a demo home, `sudo`) | another fleet home: "names no fleet ledger" | the lane |
| the fleet home on a synced or network volume | SQLite under a tool that copies a live database: NOT MEASURED, refused in the recipe | nobody, until it corrupts |
| git's variables in a typed verb's environment | the typed verb may find a ledger through `GIT_DIR` where the scrubbed hooks found none and used the fleet (D-137's recorded open, now with a second ledger) | `status`'s first line names the ledger in use; the design pass decides whether typed verbs scrub too when `BUDDY_FLEET` is set |
| two lanes write one file outside git | nothing refuses it | nobody. Out of this plan (section 8) |
| the coordinator ends without a successor | a lane's next send to `orc-<fleet>` is refused once the claim is orphaned, or queues with a result line that says its holder has ended (D-134) | each lane, at its next send |

### 9.2 Stages

| Stage | What | Tier, by class | Owes |
| --- | --- | --- | --- |
| 0 | One batch on the harness alone (section 4), run by a coordinator the operator starts. No code, no commit, nothing installed | none: nothing changes in this repository | five counts, kept by the coordinator as counts: questions per hour; the most pending at once; messages held, expired or lost; minutes from a lane blocking to the operator seeing it; lanes that ran past an hour or past the handoff size |
| 1a | A decision record and a page in `skills/buddy-fleet/`: outside git buddy is off, the stage-0 loop, and the four marks of section 4 | chore: prose naming no new verb or flag | the record's number on CLAUDE.md's Skills line; the skill tests; a byte ceiling a lane does not raise |
| 1b | The fleet ledger: 5.2's resolution in the five lifecycle hooks and the typed verbs, `buddy init --fleet <name>`, section 6's refusals, `hello`'s lines, the three skips. No schema change, no settings change | **full**: it adds an arm to invariant 3's reading and sits beside the discovery the gate shares. Codex design pass before code, panel, Codex code pass, Fable cross-check, re-verify to the brief's ceiling | a decision record that amends the reading of invariant 3 for a ledger the gate never reads and names D-137, D-044 and D-107 as untouched; 5.4's two tests; every row of 5.2 and 9.1 as a test with its positive control; each refusal of section 6 beside the grant it stands next to; mutants per arm in a private copy; N9 re-taken; the flag in `init`'s parser and usage line and the fleet skill in the same commit; USAGE; then `install.sh`, the operator's |
| 2 | Only on its trigger (question 4, or a stage-1 count of lanes inside governed repositories): a session with BOTH ledgers (the lifecycle hooks beat the home ledger and the fleet; one joint digest budget), and option S's typed `join` for a session already running | full | its own record: two inboxes under one 9,000-byte digest (`helloBudget`, `cli.go:2553`; D-034), which ledger a bare `msg` means, hook latency with two opens |
| 3 | Each only on a count: the outcome on `ls --all` (lite, a view); a "whose move" word on the roster's `waiting` (full if it is a column); a fleet listing verb | as marked | its own record |

Stage 1b is not small and should not be bought on this plan's word: stage 0
costs one batch (an afternoon is this plan's timebox, not a measurement) and
says whether the right-hand column of section 4 is
needed at all. If the operator wants buddy here regardless, 1b can go first;
nothing in it depends on 0 but its priority.

Operator-only, wherever this plan touches them: naming the fleet home;
`install.sh` or `buddy update` after 1b lands; the lanes' permission mode; any
settings change (none is proposed); raising a prose ceiling. A coordinator
creates a fleet (`init --fleet`) and opens lanes only as the operator said it
may run, and a lane does neither.

## 10. OPEN: only the operator can answer

1. **Stage 0 first, or the fleet ledger now?** One batch on the harness alone
   with its five counts, or build 1b regardless?
2. **Where may fleet ledgers be written?** `~/.buddy/fleets/<name>/` is the
   example; the location is yours to name, on a local volume nothing syncs.
3. **No brake and no file protection outside git: accepted?** `pause` is
   refused in a fleet ledger; a lane there stops only in its own pane; two
   lanes writing one file are kept apart by their briefs alone.
4. **Which pane coordinates?** A coordinator started for the fleet in a plain
   directory (stage 1b is enough), or your existing orchestrator pane, which
   lives in a repository with its own ledger (that needs stage 2)? The same
   question for lanes: will some of the ten sit inside a governed repository?
5. **Codex lanes among the ten?** They have no `SendMessage`, so the
   harness-only loop has no message channel for them; that alone would put 1b
   first.
6. **Stage 1b's tier:** full as classed here, or capped for this run?

## 11. Taken and refuted

**The Codex pass** (gpt-6-astra, xhigh, 2026-11-14, through
`scripts/codex-review.sh` in the foreground, `CODEX_DIFF=1d36f8b..2a58789` (the
draft commit, squashed into this one),
one prompt of six questions scoped to this file and five ranges of code): 476
s, header verified, verdict revise, ten findings. On gate jurisdiction (its
question 2), landing (3) and operator-only actions (4) it answered "none";
its finding 3 below is the one place the draft overstated what the gate does.
Folded once; no re-verify ran, which is the brief's ceiling.

| # | Finding | Taken / answered | Change |
| --- | --- | --- | --- |
| 1 | 12: the script tested "outside a repository" BEFORE clearing git's variables, so under `GIT_DIR=/nonexistent` inside a governed repository N0's `hello` and `msg all` could reach that repository's ledger; a failed `init` did not stop it | Taken | git's local variables and the ceiling are unset first; every fixture step that must succeed aborts the run when it does not |
| 2 | 5.2: a fleet that opens at SessionStart and goes missing or unreadable later fails silently (stderr is dropped, and no later `hello` runs); invariant 2's unreadable clause was not addressed | Taken | `busy` repeats the one line at each prompt while the failure lasts; the record must speak to invariant 2's sentence as well as 3's; 9.1's row says so |
| 3 | 3, 1: "the gate allows everything" and "no gate" are too absolute: a write from outside git into a governed repository is still judged there | Taken | both now say no home-ledger enforcement, and that governed targets keep theirs (D-138, the first plan's J1) |
| 4 | N7: the DENY that stood as the pause control targeted a file already claimed and parked, so it did not isolate the brake; N0 showed no succeeding `status`; `--ready HEAD` had no succeeding control | Taken, the last by citation | the brake is now shown on one unclaimed file: ALLOW, paused DENY, resumed ALLOW; a registered session's `status` is in N1; `--ready HEAD` succeeding with a commit is the first plan's K0, cited and not re-run |
| 5 | 2, 12: seven N cells claimed more than the script printed (unprinted exit statuses in N2, N3, N8; `wait ls` notes and the hand-read body not shown; an outcome-absence count that could read 0 on no match; ALLOW inferred from empty output; timings blind to failed calls) | Taken | the script prints or checks each; `gate` parses the decision; N9 counts failures (0 of 20 in every cell); every cell was re-read against the last run |
| 6 | 7: "50 minutes at worst" is a nominal interval, not a bound | Taken | reworded: the next successful hook or check, with no stated deadline where no keep-alive is armed |
| 7 | 5.2: invariant 14 still decides overlap between slot scopes (D-035) | Taken | said so |
| 8 | 2, 12: X1's two directory counts and X5 had no re-run command | Taken | the commands are in section 12; X6 added |
| 9 | 4, 6, 7, 8, 9: figures without a source or a label (the "worth it" thresholds; 24 h; "three requests"; the checkout's row sizes cited to N4; 165 as if drained alone; 4,096; the turns per round trip; 100 ms; 9,000; "one afternoon") | Taken | each now carries its source (`SweepTTL`, D-054, X6, invariant 9, CLAUDE.md, `helloBudget` and D-034) or is labelled a judgment, an average, or by construction |
| 10 | 5.2: step 1 said `BUDDY_FLEET` "is not read" where a ledger is found, while `hello` there prints a notice naming it | Taken | the variable takes no part in choosing the ledger; `hello` alone reads it there, for the notice |

Folding finding 5 changed the script, so every N row is from a run made
after the pass. N9 moved with the machine's load across its three runs, and
section 8 now says only what all three support.

**The lane's own review before the pass** raised thirteen points, all fixed in
the draft Codex read: X3's states miscounted; a send's result-line size quoted
from a run the published script could not reproduce (the script now counts
what the line recites); `release --to` of a slot-only claim asserted and not
measured (now in N7); the provenance flags cited to the wrong decision;
`.buddy/slot/gh-stack`, the one slot the gate reads, left grantable in a fleet
ledger; invariant 13 said to have nothing to act on; the orchestrator's
restatement presented as the operator's words; the coordinator's own need to
be started with the variable left unsaid; `herdr agent wait` and
`notify_when_idle` recommended without saying neither was run, and `herdr
agent prompt` not ruled out as a relay; the macOS version and X5's event list;
a coordinator's end described as queueing only; N9 quoted from a quieter run
than the script's last; READY with no commit left unanswered in section 6.

## 12. Re-running the measurements

Save the block below as a file and run `sh <file> <empty directory>`; the
directory must be outside every repository. It needs `buddy`, `git` and
`python3` on PATH, takes about a minute, registers eleven minted session ids
in one throwaway ledger it creates, and touches no other ledger. Each `==`
heading is an N row of section 2. This is the text that produced that table.
One beat in N6 binds the calling shell's harness process, if there is one, to
a row of the throwaway ledger and to nothing else.

```sh
#!/bin/sh
# Fixtures for docs/NON-GIT-PLAN.md. usage: sh fixtures.sh <empty directory>
# Throwaway ledger only: the directory must be outside every repository.
# Session ids are minted. Needs buddy, git, python3 on PATH. About a minute.
# The stand-in fleet ledger sits in a `git init`ed scratch directory ONLY
# because today's buddy keeps a ledger nowhere else; what is measured is what
# the verbs do once a ledger is open, and what the hooks do where none is found.
set -u
die() { echo "FIXTURE FAILED: $*"; exit 2; }
# Git's own variables go first: with GIT_DIR set, the outside-a-repository test
# below could pass inside one, and the hook verbs (which scrub them) would then
# find that repository's real ledger.
unset $(git rev-parse --local-env-vars) GIT_CEILING_DIRECTORIES
unset CODEX_THREAD_ID BUDDY_SESSION BUDDY_LABEL BUDDY_LEDGER BUDDY_FLEET
W=${1:-}
[ -n "$W" ] && [ -d "$W" ] && [ -z "$(ls -A "$W")" ] || die "need an empty directory"
W=$(cd "$W" && pwd -P)
git -C "$W" rev-parse --git-dir >/dev/null 2>&1 && die "inside a repository"
O=00000000-0000-4000-8000-000000000000
lane() { printf '%08d-1111-4111-8111-111111111111' "$1"; }   # lane 3 -> 00000003-1111-...
as() { id=$1; shift; CLAUDE_CODE_SESSION_ID=$id "$@"; }
say() { sed "s|$W|<W>|g" | cut -c1-"${2:-200}" | sed "s|^|  $1: |"; }
size() { wc -c | tr -d ' '; }
# hook <verb> <event> <session> <cwd> [<tool> <file_path>]: run one hook verb
# with the payload on stdin, FROM <cwd>; leaves RC, $W/.out and $W/.err.
hook() {
	v=$1; ev=$2; id=$3; d=$4; tool=${5:-}; fp=${6:-}
	p=$(printf '{"hook_event_name":"%s","session_id":"%s","cwd":"%s"' "$ev" "$id" "$d")
	[ -n "$tool" ] && p="$p$(printf ',"tool_name":"%s","tool_input":{"file_path":"%s"}' "$tool" "$fp")"
	p="$p}"
	(cd "$d" && printf %s "$p" | CLAUDE_CODE_SESSION_ID=$id buddy "$v" >"$W/.out" 2>"$W/.err"); RC=$?
}
show() { echo "  $1: rc=$RC stdout=$(size <"$W/.out") stderr=$(size <"$W/.err") bytes"; }
# gate <label> <session> <cwd> <absolute path>. ALLOW: buddy exits 0 and prints
# nothing. DENY: exits 0 and prints a JSON document whose permissionDecision is
# "deny". Anything else is printed raw.
gate() {
	hook gate PreToolUse "$2" "$3" Write "$4"
	if [ "$RC" = 0 ] && [ ! -s "$W/.out" ]; then v=ALLOW
	elif [ "$RC" = 0 ] && python3 -I -c 'import json,sys
sys.exit(0 if json.load(sys.stdin)["hookSpecificOutput"]["permissionDecision"]=="deny" else 1)' <"$W/.out" 2>/dev/null; then v="DENY ($(size <"$W/.out") bytes)"
	else v="OTHER rc=$RC: $(cat "$W/.out" "$W/.err")"; fi
	echo "  $1: $v"
}

echo "== N0 a plain directory: the hook verbs, the typed verbs, BUDDY_LEDGER"
F="$W/standin"; mkdir -p "$W/inv/i01"; P="$W/inv/i01"
git init -q "$F" || die "git init"
(cd "$F" && buddy init >/dev/null 2>&1) || die "buddy init"
[ -f "$F/.git/buddy.db" ] || die "no throwaway ledger"
L1=$(lane 1)
hook hello SessionStart "$L1" "$P"; show "hello (SessionStart)"
hook busy UserPromptSubmit "$L1" "$P"; show "busy (UserPromptSubmit)"
hook beat PostToolUse "$L1" "$P" Write "$P/notes.md"; show "beat (PostToolUse)"
hook idle Stop "$L1" "$P"; show "idle (Stop)"
hook bye SessionEnd "$L1" "$P"; show "bye (SessionEnd)"
gate "gate, a Write in the plain directory" "$L1" "$P" "$P/notes.md"
cd "$P"
for v in status sessions inbox "wait check" "msg all hi" "claim x --desc d --scope .buddy/slot/x"; do
	as "$L1" buddy $v >"$W/.out" 2>&1; echo "  buddy $v: rc=$? $(head -1 "$W/.out" | cut -c1-48)"
done
BUDDY_LEDGER="$F/.git/buddy.db" CLAUDE_CODE_SESSION_ID=$L1 buddy sessions >"$W/.out" 2>&1
echo "  BUDDY_LEDGER=<the stand-in's ledger> buddy sessions: rc=$? $(head -1 "$W/.out" | cut -c1-48)"
(cd "$F" && as "$L1" buddy sessions >"$W/.out" 2>&1); echo "  control, the same verb from the stand-in: rc=$? rows $(grep -c . "$W/.out")"

echo "== N1 eleven sessions hand-registered in one ledger (a coordinator, ten lanes)"
cd "$F"
as $O buddy hello --session $O --label orc </dev/null >/dev/null 2>&1 || die "hello orc"
as $O buddy claim orc-standin --desc "coordinator: ten investigations; report with buddy msg" --scope .buddy/slot/orc-standin 2>&1 | say O
ON=""; i=1; while [ $i -le 10 ]; do
	n=$(printf 'i%02d' $i); mkdir -p "$W/inv/$n"; ON="$ON --on $n"
	as "$(lane $i)" buddy hello --session "$(lane $i)" --label $n </dev/null >/dev/null 2>&1 || die "hello $n"
	as "$(lane $i)" buddy claim $n --desc "investigation $n: the question it was given, in a sentence of ordinary length" --scope .buddy/slot/$n >/dev/null 2>&1 || die "claim $n"
	i=$((i+1))
done
echo "  claims open: $(as $O buddy ls 2>/dev/null | grep -c 'slot/')"
as $O buddy status >"$W/.out" 2>&1; echo "  control for N0, a registered session's status here: rc=$?"
as $O buddy wait $ON --until 12h --note "watching ten lanes" >"$W/.out" 2>&1; echo "  O waits on all ten item claims: rc=$? $(head -1 "$W/.out" | cut -c1-60)"

echo "== N2 a lane asks: one message to the coordinator's slug, one declared wait"
Q="NEED INPUT: the vendor's export has two candidate date columns, created_at and posted_at; which one is the audit keyed on? Findings so far are in notes.md."
ms=0; ws=0
for i in 3 7 9; do
	as "$(lane $i)" buddy msg orc-standin "$Q" >"$W/.out" 2>&1 && ms=$((ms+1))
	[ $i = 3 ] && echo "  lane 3's send: result line $(size <"$W/.out") bytes in $(grep -c . "$W/.out") line(s), reciting $(grep -o 'claim "i[0-9]*"' "$W/.out" | wc -l | tr -d ' ') of the claims O waits on: $(cut -c1-60 "$W/.out")"
	as "$(lane $i)" buddy wait --until 2h --note "NEED INPUT: which date column" >"$W/.out" 2>&1 && ws=$((ws+1))
	[ $i = 3 ] && head -1 "$W/.out" | say "lane 3's wait" 120
done
echo "  sends that exited 0: $ms of 3; waits that exited 0: $ws of 3; question body: $(printf %s "$Q" | size) bytes"

echo "== N3 two lanes finish: release with an outcome"
as "$(lane 5)" buddy release i05 --outcome pass --note "DONE: findings in <W>/inv/i05/findings.md" >"$W/.out" 2>&1; echo "  lane 5: rc=$? $(head -1 "$W/.out" | cut -c1-90)"
as "$(lane 6)" buddy release i06 --outcome fail --note "DONE: could not reproduce; notes in findings.md" >"$W/.out" 2>&1; echo "  lane 6: rc=$? $(head -1 "$W/.out" | cut -c1-90)"
as $O buddy wait check >"$W/.out" 2>&1
echo "  O's wait check: rc=$? $(grep -c . "$W/.out") line(s); the first is $(head -1 "$W/.out" | size) bytes and names $(head -1 "$W/.out" | grep -o 'claim "i[0-9]*"' | sort -u | wc -l | tr -d ' ') claims, $(head -1 "$W/.out" | grep -o 'outcome [A-Z]* "[^"]*"' | wc -l | tr -d ' ') with an outcome and its note: $(head -1 "$W/.out" | grep -o 'outcome [A-Z]* "DONE: could[^"]*"')"

echo "== N4 what the coordinator reads in one cycle (bytes, lines)"
for v in sessions "wait ls" ls "ls --all"; do
	as $O buddy $v >"$W/.out" 2>&1; echo "  buddy $v: rc=$? $(size <"$W/.out") bytes, $(grep -c . "$W/.out") lines"
done
as $O buddy sessions 2>&1 | grep -E ' i0[135] ' | sed 's/  */ /g' | say "row" 230
echo "  wait ls lines carrying a lane's NEED INPUT note: $(as $O buddy wait ls 2>&1 | grep -c 'note: NEED INPUT')"
as $O buddy ls --all >"$W/.out" 2>&1
echo "  ls --all: rows for i05 and i06 selected: $(grep -c -E '^i0[56] .* released ' "$W/.out"); of those, rows carrying the outcome word or the note: $(grep -E '^i0[56] .* released ' "$W/.out" | grep -c -E 'PASS|FAIL|pass|fail|DONE')"
as $O buddy inbox >"$W/.out" 2>&1; echo "  buddy inbox: rc=$? $(size <"$W/.out") bytes, $(grep -c . "$W/.out") lines, $(grep -c 'NEED INPUT: the vendor' "$W/.out") carrying the question"
as $O buddy inbox >"$W/.out" 2>&1; echo "  again: $(head -1 "$W/.out" | cut -c1-40)"
as $O buddy who i03 >"$W/.out" 2>&1; echo "  buddy who i03: rc=$? $(size <"$W/.out") bytes, $(grep -c . "$W/.out") lines"

echo "== N5 the answer goes back; the lane's hooks, run where the lane lives, never see it"
as $O buddy msg i03 "posted_at: the operator said so in my pane at 17:50Z" >"$W/.out" 2>&1; echo "  O answers: rc=$? $(cut -c1-150 "$W/.out")"
M=$(grep -o 'message #[0-9]*' "$W/.out" | grep -o '[0-9]*')
hook beat PostToolUse "$(lane 3)" "$W/inv/i03" Write "$W/inv/i03/notes.md"; show "lane 3's beat, from its plain directory"
echo "  buddy sent $M, after that beat: $(as $O buddy sent "$M" 2>&1 | head -1 | grep -o '[0-9] of [0-9] with a recorded delivery')"
(cd "$F" && as "$(lane 3)" buddy inbox >"$W/.out" 2>&1); echo "  the lane, by hand, cd <stand-in> && buddy inbox: rc=$? $(size <"$W/.out") bytes, $(grep -c "#$M .*posted_at: the operator said so" "$W/.out") line(s) carrying message #$M and its body"
echo "  buddy sent $M, after the hand read: $(as $O buddy sent "$M" 2>&1 | head -1 | grep -o '[0-9] of [0-9] with a recorded delivery')"

echo "== N6 the same hooks with the lane's cwd INSIDE the ledger's directory (control)"
mkdir -p "$F/i04"
as $O buddy msg i04 "a second answer" >/dev/null 2>&1 || die "msg i04"
hook beat PostToolUse "$(lane 4)" "$F/i04" Write "$F/i04/notes.md"; show "lane 4's beat"
echo "  beat output lines carrying the body: $(grep -c 'a second answer' "$W/.out")"
hook idle Stop "$(lane 4)" "$F/i04"; show "lane 4's idle"
as $O buddy sessions 2>&1 | grep -E ' i04 ' | sed 's/  */ /g' | sed 's/pid [0-9]*/pid <n>/' | say "row" 230

echo "== N7 which claim shapes the ledger takes and refuses"
as "$(lane 1)" buddy claim i01-file --desc d --scope notes.md 2>&1 | head -1 | say "a relative file scope" 120
as "$(lane 1)" buddy claim i01-abs --desc d --scope "$W/inv/i01/notes.md" >"$W/.out" 2>&1; echo "  an absolute scope: rc=$? $(head -1 "$W/.out" | sed "s|$W|<W>|g" | cut -c1-110)"
as $O buddy claim r1 --desc FORMING --scope .buddy/slot/r1 >/dev/null 2>&1 || die "claim r1"
as "$(lane 2)" buddy release i02 --to orc-standin --in-run r1 >"$W/.out" 2>&1; echo "  park a slot-only claim in a run: rc=$? $(head -1 "$W/.out" | cut -c1-150)"
as "$(lane 1)" buddy release i01-file --to orc-standin --in-run r1 >"$W/.out" 2>&1; echo "  park a file-scope claim (control): rc=$? $(head -1 "$W/.out" | cut -c1-110)"
as "$(lane 8)" buddy wait --on r1 --ready HEAD >"$W/.out" 2>&1; echo "  wait --ready HEAD: rc=$? $(head -1 "$W/.out" | cut -c1-110)"
as "$(lane 8)" buddy wait --on r1 --until 4h --note "READY: <W>/inv/i08/findings.md v1" >"$W/.out" 2>&1; echo "  wait --on r1 --note READY: rc=$? $(head -1 "$W/.out" | cut -c1-90)"
as $O buddy release r1 --outcome pass --note "read, no checks apply" >/dev/null 2>&1 || die "release r1"
as "$(lane 8)" buddy wait check 2>&1 | head -1 | say "wait check" 120
as "$(lane 8)" buddy wait clear >/dev/null 2>&1
as "$(lane 8)" buddy release i08 --to i01 >"$W/.out" 2>&1; echo "  hand a slot-only item claim to another session, release i08 --to i01: rc=$? $(head -1 "$W/.out" | cut -c1-70)"
gate "before the pause: lane 10 writes an unclaimed file INSIDE the stand-in" "$(lane 10)" "$F" "$F/free.md"
as $O buddy pause i10 --note fixture >"$W/.out" 2>&1; echo "  pause i10: rc=$? $(head -1 "$W/.out" | cut -c1-60)"
echo "  roster rows reading PAUSED: $(as $O buddy sessions 2>&1 | grep -c ' i10 .*PAUSED')"
gate "paused: lane 10 writes in the plain directory it lives in" "$(lane 10)" "$W/inv/i10" "$W/inv/i10/notes.md"
gate "paused: the same unclaimed file inside the stand-in (control)" "$(lane 10)" "$F" "$F/free.md"
as $O buddy resume i10 >/dev/null 2>&1; echo "  resume i10: rc=$?"
gate "resumed: the same file again" "$(lane 10)" "$F" "$F/free.md"

echo "== N8 a blocking wait on two claims ends only when BOTH have landed"
as $O buddy wait clear >/dev/null 2>&1
( as $O buddy wait --on i07 --on i09 --until 1h --block >"$W/.blk" 2>&1; echo "rc=$?" >>"$W/.blk" ) &
t=0; until grep -q '^BLOCKING' "$W/.blk" 2>/dev/null; do t=$((t+1)); [ $t -gt 20 ] && die "the wait never blocked"; sleep 1; done
as "$(lane 7)" buddy release i07 --outcome pass --note "DONE" >/dev/null 2>&1; echo "  the wait is blocking; first release: rc=$?"
sleep 20; echo "  20 s later: $(grep -c '^rc=' "$W/.blk") exit(s)"
as "$(lane 9)" buddy release i09 --outcome pass --note "DONE" >/dev/null 2>&1; echo "  second release: rc=$?"; wait
echo "  then: $(tail -1 "$W/.blk"), $(grep -c '^LANDED' "$W/.blk") LANDED line(s)"

echo "== N9 hook wall time, 20 calls each: min median max ms; $(uptime | sed 's/.*load/load/'); ncpu $(sysctl -n hw.ncpu)"
python3 -I - "$W/inv/i02" "$F/i04" "$(lane 2)" "$(lane 4)" <<'EOF'
import json, os, subprocess, sys, time
plain, inside, sp, si = sys.argv[1:5]
def run(verb, ev, cwd, sid):
    payload = json.dumps({"hook_event_name": ev, "session_id": sid, "cwd": cwd, "tool_name": "Write", "tool_input": {"file_path": os.path.join(cwd, "notes.md")}}).encode()
    env = dict(os.environ, CLAUDE_CODE_SESSION_ID=sid)
    ts, bad = [], 0
    for _ in range(20):
        t = time.monotonic()
        r = subprocess.run(["buddy", verb], input=payload, capture_output=True, env=env, cwd=cwd)
        ts.append((time.monotonic() - t) * 1000)
        bad += r.returncode != 0 or bool(r.stderr)
    ts.sort()
    return "%.1f %.1f %.1f (%d of 20 failed)" % (ts[0], (ts[9] + ts[10]) / 2, ts[-1], bad)
for verb, ev in (("beat", "PostToolUse"), ("busy", "UserPromptSubmit"), ("idle", "Stop")):
    print("  %s: no ledger found (plain directory) %s; ledger found %s" % (verb, run(verb, ev, plain, sp), run(verb, ev, inside, si)))
EOF
echo "== done"
```

The X rows, each read-only. X1: `claude agents --json`, its rows counted by
`status`, and the first command below for the two counts of working
directories. X2: `herdr agent list`, rows counted by `agent_status`, and
`herdr agent wait --help`. X3: the harness's `ListAgents` tool. X4: the
`SendMessage` tool's own description. X5 and X6: the second and third
commands below.

```sh
claude agents --json | python3 -I -c 'import json,os,subprocess,sys
out=nol=n=0
for r in json.load(sys.stdin):
    n+=1
    p=subprocess.run(["git","-C",r["cwd"],"rev-parse","--path-format=absolute","--git-common-dir"],capture_output=True,text=True)
    if p.returncode: out+=1
    elif not os.path.exists(os.path.join(p.stdout.strip(),"buddy.db")): nol+=1
print(n,"sessions;",out,"outside git;",nol,"in a repository with no buddy.db")'
python3 -I -c 'import json,os,re
for ev,arr in json.load(open(os.path.expanduser("~/.claude/settings.json"))).get("hooks",{}).items():
    for m in arr:
        for h in m.get("hooks",[]):
            for v in re.findall(r"bin/buddy\W+(hello|beat|busy|idle|bye|gate)\b",h.get("command","")): print(ev,m.get("matcher"),v)'
buddy sessions | LC_ALL=C awk '/ live /{print length($0)}' | sort -n
```
