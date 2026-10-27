# Beyond code: buddy for any workflow, and an orchestrator of orchestrators

Status: PLAN, nothing built (2026-10-26). Written by the `beyond-code` lane on
Fable, from the operator's question below, the docs this repo already holds,
and counts taken on this machine the same evening. One Codex design pass ran
over the draft (verdict: revise); every finding is in "Taken and refuted" at
the end and folded into the sections it names. Section 1.4 was added after
the pass, from the operator's own scenario. Baseline: main at 9b6980b when
the lane opened. Steps 1 and 5 landed in the skill on 2026-10-27 (the
`plan-skill` lane), after 3.4(A)'s routing rule was measured end to end;
the numbers are under A, and what the measurement refuted is in section 6.

Register: every part names the failure it answers, the number it rests on,
the alternative it refuses, and what it cuts. Where a number was not taken,
it says NOT MEASURED. Anything the operator has to decide is marked **OPEN**.

## The operator's question

2026-10-26 23:05, in the orchestrator's pane: "Can buddy-system be adapted to
any workflow? Let's say I want an orchestrator to help me manage several
orchestrators performing different tasks. This way I can scale my reach, while
still viewing one window primarily." And: "I'm talking about buddy-system for
not just coding, when I say adapted to any workflow." At 23:20: "the
orchestrator capable of deciding what model to use for the task automatically,
with some self learning from past fails or excess turns due to the model
choice."

Three asks, each sized on its own below: (1) buddy as the coordination layer
for non-code work; (2) one top-level session over several coordinators, the
operator watching one pane; (3) the coordinator picking a model per item from
what past items measured.

## 1. The straight answer

**Yes for any workflow whose deliverables are files in a directory that is a
git repository on this machine, today, with no code change.** Research notes,
writing, runbooks, trading-tool configuration, a decision memo: a claim is a
reservation on a path, and nothing in the ledger asks what the path holds.
Measured in a scratch fixture (2026-10-26, two throwaway ledgers, a fake
session id, nothing registered anywhere else): a directory with `git init`
and **no commit at all** takes `buddy init`, `hello`, `claim`, `status`, a
wait, and `who`; its `BASE` line reads `(none recorded)` and refuses nothing.
The only verb that wants a commit is `wait --ready HEAD`, refused there
("names no commit here; nothing was recorded"); section 1.3 says what a
READY is without one.

**No for a session whose working directory is not a git repository**, and
that is the live gap: every verb answers "not a git repository … run it from
inside the repository whose ledger your peers use" (measured in two such
directories tonight). Invariant 3 makes "provably not a repo" the feature-OFF
verdict on purpose, so a non-repo session has no row, no claims, no inbox, no
pause, and no roster line. Two of the ten sessions live on this machine right
now sit in such directories (section 2). The fix that needs no code is `git
init` in that directory, then `buddy init`: the fixture above is exactly that
state. Option C in section 3 is the code path if the operator refuses to put
a `.git` in a notes folder; it is not recommended first.

### 1.1 Which primitives are domain-free

| Primitive | Domain-free? | What a non-code workflow does with it |
| --- | --- | --- |
| Identity `(session_id, incarnation)`, `hello`/`beat`/`bye` | the semantics, yes; the implementation finds its ledger with git on every hook | A research or writing lane is a session like any other; `beat` runs on every tool (the PostToolUse matcher is `*`), so mail and heartbeats work for a session that only browses or calls MCP tools |
| Claims on any nameable path (D-035 proved `NormalizeScope` consults no filesystem) | yes | The deliverable: `notes/plan.md`, `reports/q3/`, a directory of captures |
| Resource slots `.buddy/slot/<name>` | yes, already path-free | A broker connection, a browser profile, a rate-limited API, "the one live account" |
| Messages with declared provenance (`--measured`, `--lead`, `--relay`, `--supersedes`) | yes | Research is mostly relayed figures; the kinds were built for exactly that register |
| Waits on a claim or a timer, `wait check` keep-alive | yes | Unchanged |
| `ids` | yes | Numbering memos, tickets, decision records in any repo |
| The `orc-<project>` claim's description as the pull channel (D-030) | yes | Unchanged |
| Authority files, `pause` | yes | `authority add` a runbook or a trading policy file; `pause` is the brake for any tool the gate sees (1.2) |
| The roster, `who`, `status`, `whose` | yes | `BASE` prints "(none recorded)" when there is no HEAD |

### 1.2 Which pieces are code-shaped, and what a non-code workflow needs instead

- **The ledger lives in the git common dir, found from the hook's cwd**
  (`discoverRepo`, nine call sites in `internal/cli`; every hook verb runs it,
  `beat` may run the dirty scan, `idle` the base sample). Needs: a git
  repository, commits optional. A non-repo directory is feature-off (above).
  Instead: `git init` there. The ledger is unseen by git either way.
- **The gate adjudicates tool-call PATHS**, and the shipped `PreToolUse`
  matcher routes four tools to it (`Edit|Write|NotebookEdit|Bash`). For a lane
  whose work is browsing, chat or an API, the path check is meaningless and
  harmless: a call with no path is allowed (`len(paths) == 0`). Two things
  still matter. The **brake**: the gate applies `pause` to every tool it sees
  except its read-only list (`Read`, `Glob`, `Grep`, `LS`, `WebFetch`,
  `WebSearch`, which return before the pause check), so with the shipped
  matcher a paused browsing lane keeps browsing, and even with the matcher
  widened to `*` a paused lane can still `WebFetch` and `WebSearch`. Widening
  the matcher is a settings change (the operator's, never a lane's) that
  costs the gate's measured 20 ms per tool call; what that does to a
  tool-heavy session's wall time is NOT MEASURED. The **deliverable**: a lane
  that writes its report with `Write` is gated like any code lane; one that
  writes it through an MCP tool (a doc connector, a spreadsheet) names no path
  the gate can place, exactly like a shell redirect does today ("cooperative
  enforcement", DESIGN.md). Claim it anyway; the claim is still the
  reservation peers read.
- **The landing recipe** (a run, a shared tier, `.buddy/slot/main`, READY at a
  commit, LANDED by an outcome). The run claim, the waits and the outcome are
  domain-free. Only two words are code-shaped: "commit" and "tier". 1.3 gives
  their replacements. `--ready HEAD` is refused with no commit; a repo that
  commits its notes keeps the full recipe.
- **`base`, dirty paths and the commit gate** in a repo with no commit
  (Codex finding 9 corrected the draft, which had written all three off):
  only the base sample is absent, since HEAD does not resolve. The dirty scan
  is `git status` and records a `Write` to `notes.md` as before; the commit
  gate reads the index against an unborn HEAD on purpose (`stagedPaths`) and
  warns or denies a claim collision on the first commit like any other. They
  work; a workflow that never commits simply never exercises the gate.
- **The skill's prose** is written for code ("commit; finish your review; run
  your item's own checks; never push"). A non-code lane reading it has to
  translate. Instead: one short section in the skill, "When the deliverable is
  not code", that maps each word (step 4.1 below), not a second skill.

### 1.3 A unit of work and "done" when there is no commit

- **A unit of work is a claim** whose slug is the item's name and whose scopes
  are the deliverable's paths. Exactly as now; no table.
- **READY** = the lane's `buddy wait --on <run> --until 4h --note "READY:
  <path> <version word>"` plus the `buddy msg orc-<project> "<item> READY: …"`
  the recipe already requires, naming the path and what the orchestrator
  should read. That is a wait ON the run: it rides the run's LANDED like any
  rider's (it is not a timer). What the orchestrator sees: `who <run>` prints
  its SHORT waiter listing (labels and ages, no notes) until some waiter has
  declared `--ready`, because the per-rider lines are keyed on `ReadySHA`
  (`who.go`, Codex finding 6); the notes are read with `buddy wait ls`, which
  quotes every declaration's note, or from the READY message. `ready_sha`
  means "a commit", and faking one is worse than a note. **OPEN**: whether to
  let `--ready` carry a non-commit token (`--ready-at <path>`, a schema
  column, full tier) so `who <run>` lists non-code riders the same way. Not
  proposed now: 0 non-code runs have been formed, and `wait ls` is enough for
  the first ten.
- **LANDED** = the orchestrator's `release <run> --outcome pass|fail|aborted
  --note`, exactly as now, read by every rider's `wait check`.
- **The shared tier** = whatever gate the workflow has: a review pass over
  the assembled report, a checklist script, a human read. With none, the
  integrator's run is "read and accept", and the outcome still says which.
- **Main** = the directory the deliverables are integrated into. With no
  branches, `.buddy/slot/main` is still the slot that serializes integration,
  and the integrator still holds it from forming to landing.

### 1.4 A worked example: two repositories and eight tickets, one window

Added after the Codex pass from the operator's scenario (2026-10-26 23:30):
two coding projects, each git-tracked, and eight troubleshooting tickets
that are not, each a session working one ticket with the tools in the
session (MCP and the like); one orchestrator relays everything from all ten
to one window, so the operator answers and directs in one place and no
session's cache goes cold because a notice went unseen. Everything below is
existing verbs and measured pieces; what is new is writing them as one loop.

**Where the ten sessions live.** The eight tickets go in ONE repository,
`tickets/`, made with `git init` and `buddy init` and never committed to;
each session's cwd is its own `tickets/T-<n>/`, so all eight share one
ledger (a cwd inside the repo finds it). Each wears its ticket as its label
(`BUDDY_LABEL=T-<n>` on the tab, D-089) and claims its own directory, for
addressing rather than protection. MCP-only work is untouched by the gate (a
call with no path is allowed) and `beat` runs on every tool, so mail
reaches each one. The orchestrator of the tickets lives in that repo too:
it is the fleet repo of 3.4(A), and its pane is the one window. The two
coding projects keep their own ledgers and their own `orc-<project>`
coordinators, launched by the window session with the relay clause (D-083)
and reporting upward by hand registration in `tickets/` (the routing rule).

**The attention loop**, the part the rest of the plan left unsaid:

1. A lane that needs the operator never asks in its own pane. Its brief
   says: `buddy msg orc-tickets "NEED INPUT: <the question>"`, deliver the
   wake line the result prints (D-041: the orchestrator is woken in seconds
   as a peer message, D-039, instead of at its next tool call), then
   `buddy wait --until 2h --note "waiting on operator: <topic>"` and arm
   `/loop buddy wait check`. Measured: a lane follows a brief that names its
   coordinator (D-056); a parked lane on the hour tier stays warm through
   the check, and without it 145 of 149 wakes past an hour came back cold
   (D-054).
2. The orchestrator's drain prints the question in the one window with the
   lane's label on it. The operator answers there, in words.
3. The orchestrator sends `buddy msg T-<n> "<the answer>"` and delivers that
   wake line too; the lane wakes, clears its wait, and goes on. A coding
   lane's question travels the same way one hop longer: lane → its
   `orc-<project>` → the window, and back.
4. Between questions the window reads state, not text: `buddy sessions` in
   `tickets/` (eight rows: `idle 12m`, `waiting 1h12m` with its note, claims,
   prompt size), `cd <repo> && buddy who orc-<project>` for each coding
   project, and `claude agents --json` for `waitingFor` across all ten.
   Later, `fleet-report.sh` (4.2) prints that in one call.

**What stays in the lanes' panes, by the relay floor (D-083).** A tool
approval: a reply typed in the window is not that approval, and the lane's
prompt sits until the operator answers it where it is. Lanes in auto mode on
a model that has it (the operator's standing decision) remove most of those;
what an MCP-tool lane in auto mode still stops for is NOT MEASURED and is the
first measurement this scenario owes. `pause` is per ledger: stopping
everything is three `pause all`, one per ledger, from a bare shell.

**Cost.** Every relayed question and answer is context in the window session
(a body up to 4,096 bytes, drains of 20 / 8 KiB), and its handoff size
(D-064) is the ceiling; a round trip is one orchestrator turn each way. How
many questions per hour ten lanes raise at one window is NOT MEASURED and
is the second measurement owed: it decides whether one window session
coordinates ten lanes or hands the tickets to a coordinator of their own.

## 2. Evidence from this machine (counts only, 2026-10-26)

**Project directories the harness keeps** (`~/.claude/projects`, each mapped
to its path by its transcripts' `cwd` field, then tested with `git rev-parse
--git-common-dir` and the presence of `buddy.db` there):

| Class | Directories | Transcripts |
| --- | --- | --- |
| git repo with a ledger (11 directories over 6 distinct ledgers; worktrees share one) | 11 | 649 |
| git repo, no ledger | 5 | 18 |
| not a repository | 8 | 24 |
| directory gone (removed worktrees, scratch fixtures) | 32 | 42 |
| total | 56 | 733 |

So 42 of 733 transcripts, under 6%, were sessions buddy could not have seen.
Of the 8 non-repo directories one holds 15 transcripts: a standing non-code
workflow, not a stray. The 68 of this ledger's 147 sessions whose transcripts
are no longer under any project directory (their worktrees were removed) are
the other number this section carries forward: a report that needs
transcripts loses almost half its sessions once lanes are cleaned up (5.1).

**Live right now** (`claude agents --json`, 10 rows; `herdr pane list`, 10
panes in 4 workspaces): this repo's fleet in one workspace (5 panes, this lane
among them); another repo's fleet in a second (2 panes: an orchestrator and a
lane); a third workspace with a personal-decision session in a non-repo
directory and a session in a trading-tool checkout that HAS a ledger; a fourth
with one session in a non-repo directory. Three of ten are not coding; two of
ten are invisible to buddy. Two fleets, two ledgers, two coordination claims,
concurrently.

**What one session sees of both fleets from one place, measured from each
repo's cwd** (never `hello`, never a send):

- `cd <other repo> && buddy sessions`, `buddy ls`, `buddy who <slug>`,
  `buddy wait ls` all print that ledger's registers to any caller. With this
  session's native id set, `buddy status` there says "has not said hello in
  this repo's ledger — if that is really you, `buddy hello --session <id>`
  registers it here". These reads wrote nothing tonight because this id has
  no row there: `heardVerb` (D-086) moves `last_seen` only for a row it
  finds. They are NOT read-only in general: for an id registered in that
  ledger every non-hook verb is a write, `inbox` marks deliveries, and
  `store.Open` migrates a ledger older than the binary (D-037 is one-way).
  3.4(A) and 4.2 take that into account.
- Bytes: this ledger's roster 5,371 bytes / 28 lines and claims 1,839 bytes;
  the other fleet's 11,104 / 59 and 9,658 / 32. Both rosters together exceed
  the 9,000-byte digest budget (D-034), and the other fleet's claims list
  alone is over 8 KiB. A "one window" view cannot be the two rosters
  concatenated; it is counts and exceptions (3.4).
- **Addressing across ledgers.** From this cwd, the other fleet's labels and
  slugs are "no such target" (measured). From the other cwd, `buddy msg
  <its coordinator slug> --dry-run` resolves it and reports "would send …
  **as operator**", with this session's id set or unset. `senderFor` falls
  back to the word "operator" for any id the ledger does not know (and for a
  known id that has ended), by design ("a message must never be refused for
  want of a signature"); the correction owner is recorded as unknown
  (`senderSession`, D-043). So a session CAN address a peer in a ledger it is
  not registered in, and its message arrives stamped as the operator's.
  Nothing was sent.
- **Hand registration works** (fixture, 2026-10-26): `buddy hello --session
  <id> --label top` run in a second ledger gives that id a row there
  (worktree = that repo; no process registered, so it is "unbound"), and a
  `msg` from it in that cwd signs as `top`. A hand `buddy bye <id>` there ends
  that row without `--force` (measured). A second session wearing `top` there
  makes `msg top` "ambiguous … use a full session id" (measured). Its hooks
  beat the cwd's ledger only, so the row's `seen` moves only when it runs a
  verb there (D-086); mail queued to it there is drained only by a hand-run
  `buddy inbox` in that cwd, and `msg` prints no wake address for it, since
  the wake needs a registered live process (`wakeAddr`).
- **The chat journal is machine-wide and already crosses ledgers.** The
  daemon serves four rooms: three repos' and one named for no repo (`ops`,
  16 messages). Rows per room: 1,296 / 58 / 16 / 16. It is the one register on
  this machine that every fleet writes into, and it is a view (invariant 1).
- **The harness's own cross-fleet registers.** `claude agents --json` lists
  every LIVE session on the machine with `cwd`, `pid`, `status` and
  `sessionId`; `/tmp/cc-socks/` holds 217 socket files for 10 live sessions
  (stale ones linger), addressed by pid, machine-wide (D-039). Between them a
  top session can find and WAKE any live session on the box without buddy;
  it cannot read what that session holds without that session's ledger, and
  a ledger with open claims and queued mail but no live session is invisible
  to both.

## 3. The orchestrator of orchestrators

### 3.1 What the skill already allows

- **The relay chain is already bounded at each hop** (D-083): the operator in
  the first pane; that session, by its brief, to a coordinator it launches;
  that coordinator, by its briefs, to its lanes. A top session launching two
  coordinators is the same clause one level up, and the floor is the same at
  every hop: never install, push, settings, permission, trust, schema, a more
  permissive mode, or work beyond the launcher's scope. Nothing new to decide.
- **Panes across workspaces**: `herdr tab create --workspace <ws>` opens a
  coordinator's pane in whichever workspace the operator reads that project
  in; the top session's own pane is the "one window".
- **Wake is machine-wide**: the socket address `msg` prints is
  `uds:/tmp/cc-socks/<pid>.sock`, and `SendMessage` to it works from any
  session (D-039).
- **Views of every ledger** from one shell (section 2), with the write
  caveat there.

### 3.2 What breaks

- **One ledger per repo, so every name is per ledger**: labels, slugs,
  `orc-<project>`, the roster, the inbox, `pause`. The top session is
  registered where its cwd's ledger is and nowhere else. A `buddy msg` to a
  coordinator in another repo arrives stamped `[operator]` (section 2): not a
  security hole (invariant 4: `from` confers nothing; D-083: the brief is the
  authority, and the sender must be the id the brief names, which "operator"
  is not, so the coordinator correctly treats it as peer text), but a
  **misattribution the operator reads**. The first fix is in 4.3.
- **No cross-ledger roster.** `buddy sessions` is one ledger. The one window
  has `claude agents --json` (status, cwd, pid; no claims, no waits, no
  READY, and nothing for a ledger with no live session) and chat.
- **The alert crosses rooms** (D-101): a message naming a slug in any room
  alerts every session holding that slug anywhere. Two fleets both holding a
  bare `orchestrator` tonight (the other repo's still does) is the measured
  case: 14 of the 29 retained journal messages (one room, 2026-10-15..20)
  carried the word. Coordinators
  under a top session must wear `orc-<project>`, and the top session's own
  slug must be unique on the machine too.
- **The coordination claim's description is 512 bytes** (D-030): a top
  session's "state of every fleet" does not fit; it is a pointer to a file.
- **The one window is not one control** (Codex finding 13). The relay
  chain carries decisions inside D-083's bound and nothing else: a
  coordinator's request to install, change a permission or a mode is
  answered in ITS pane by the operator, and a reply typed in the top pane
  does not become that approval. `pause` is per ledger: pausing the top
  session pauses no coordinator and no lane. The authorized stop-everything
  is the operator's, one `buddy pause all --note <why>` per coordinated
  ledger from a bare shell (the fleet repo keeps that loop as a script, the
  list of ledgers being 4.2's). **OPEN**: whether the top session may run
  `pause` in a coordinated ledger on the operator's relayed word. `pause` is
  a control row the machine user enters (invariant 6) and D-083 lists "stop"
  among the relayable decisions, but the brake has only ever been the
  operator's own act; the plan leaves it so until the operator says.

### 3.3 The cost

- A coordinator is a session: about 60k tokens of cache on its first request
  (D-056), a handoff at `BUDDY_HANDOFF_AT` (500k on this fleet, D-064), and an
  hour-tier cache to keep warm while parked (D-033). Two coordinators and a
  top session are three such sessions before any lane; each handoff chain
  (D-063) is a coordinator's own.
- **The top session's handoff is more than its pointer file** (Codex finding
  1): its coordinators' briefs name its FULL session id, and its hand
  registrations sit in every coordinated ledger. The successor is announced
  to each coordinator by `msg` from the outgoing id ("<successor> (session
  <id>) is your coordinator from now", the skill's existing step), any
  continuing claim in the fleet repo moves with `release --to`, and the
  outgoing session retires each foreign row with `cd <repo> && buddy bye
  <its own id>`. Retiring first does NOT free the label (measured
  2026-10-27, section 6): label resolution matches ended rows too, and
  nothing deletes a session row, so a successor under the same label makes
  `msg top` ambiguous in that ledger for good. Hence each top session wears
  its own numbered label, and peers address the claim slug (3.4(A)).
  Measured count: three commands per coordinated ledger (the announcement,
  the `bye`, the successor's `hello`) and one `release --to` in the fleet
  repo, in the handoff file's "Owed" section.
- The top session's window is bounded like any session's: a hello digest
  under 9,000 bytes (D-034), drains of 20 messages / 8 KiB (D-036). Measured
  above: two rosters already exceed that. So the top view is per-fleet
  counts (live, idle, READY, waiting, STALE, at handoff size) and the
  exceptions, with the full roster one `cd` away.
- Each level of relay adds a round trip to every decision the operator
  makes; D-083 measured one round trip per decision cost when the clause was
  missing. With the clause, the top session decides inside its bound and
  asks for the rest. The operator's "one window" is also where every
  question from two fleets lands: NOT MEASURED how many per hour that is.

### 3.4 Options

**(A) Convention only, plus a read-only rollup script.** The skill gets "A
fleet of fleets":

- The top session lives in a small git repository of its own ("the fleet
  repo": the list of coordinated project paths, briefs, handoff files, the
  rollup's output) with its own ledger, so it has a row, an inbox, `pause`,
  and claims on the briefs it writes.
- It launches each coordinator in that coordinator's repo with the relay
  clause naming the top session by label and full id.
- **Routing rule** (Codex finding 7): every message is sent in the
  RECIPIENT's home ledger, where the recipient's hooks drain it and `msg`
  prints its wake address. Downward: the top session hand-registers in each
  coordinated ledger (`cd <repo> && buddy hello --session
  $CLAUDE_CODE_SESSION_ID --label top-<n>`, a label numbered per successor)
  so its sends there sign as `top-<n>`,
  and sends from that cwd. Upward: each coordinator hand-registers in the
  fleet repo's ledger and reports there to the top session's claim slug
  (`cd <fleet> && buddy msg orc-<fleet> …`; the draft had `msg top`, which a
  successor under the same label makes ambiguous for good, section 6),
  where the top session's own hooks drain it and the wake line prints. A
  foreign registration identifies a SENDER; nothing is ever sent TO a
  foreign row. Chat (the `ops` room) stays optional visibility.
- The top session reads each ledger with `cd <repo> && buddy who
  orc-<project>` and `who <run>` on a timer wait of its own; each such read
  moves its own `seen` in that ledger (D-086), which is harmless and means
  its foreign rows read STALE only when it has not looked for 30 minutes.
- Succession as in 3.3.

`scripts/fleet-report.sh` (4.2) prints, per coordinated ledger, the counts
above and the exception lines, under 9,000 bytes. Holds every invariant:
nothing new reserves or refuses; chat stays the view; every row is
CLI-entered by the machine user. Bends nothing; costs one hand `hello` per
ledger per session and the retirement commands at handoff (3.3).

**Measured end to end, 2026-10-27** (the `plan-skill` lane, before step 1's
prose landed). Two ledgers: this checkout's (W) and a scratch fleet repo (F:
`git init`, `buddy init`, `notes/projects` naming this checkout, no commit).
The lane's own session stood in for the top session, hand-registered in F as
`top`; a minted id was the coordinator `orc-x`, hand-registered in F and
never in W. Counts and verdict words only; no body is quoted.

| # | Command | Ledger | Printed | In A, carried by |
| --- | --- | --- | --- | --- |
| 1 | `buddy hello --session <top> --label top </dev/null` | F | 4 lines, 1,090 bytes, exit 0 | hand |
| 2 | `buddy hello --session <x> --label orc-x </dev/null` | F | 4 lines, 1,096 bytes; row with pid 0 and no process | hand |
| 3 | `buddy msg orc-buddy-system "…"` from the worktree | W | one line, 549 bytes: `queued for orc-6 … last reported idle 40s ago … message #418; to wake it now: SendMessage to "uds:…"` | hand; the recipient's hooks recorded delivery inside 53 s, the wake not delivered |
| 4 | `buddy msg top "…"` as orc-x, twice | F | `queued … registered 17s ago and not seen since …`; no wake address | hand |
| 5 | `buddy beat`, PostToolUse payload on stdin | F | one `additionalContext` document, 277 bytes, both messages (#1 #2); `buddy sent` as orc-x: delivery recorded for each. The same beat bound the lane's own claude process to the `top` row (`BeatFrom`) | the top's PostToolUse |
| 6 | `buddy inbox` after a third send | F | `#3`, one line; a second run `inbox empty` | hand, on demand |
| 7 | `buddy msg top "…"` as orc-x, top bound, no idle report | F | `last seen 1m ago`; no wake address | hand |
| 8 | `buddy idle`, Stop payload (`stop_hook_active`) | F | nothing; one `session_idle` row | the top's Stop |
| 9 | `buddy msg top "…"` as orc-x, top bound and idle | F | `top last reported idle 1s ago … to wake it now: SendMessage to "uds:/tmp/cc-socks/<pid>.sock"` | hand |
| 10 | `buddy who orc-x` as top | F | 10 lines, 755 bytes; ledger diff before/after: ONE column of ONE row, `last_seen` of top (D-086) | hand, on the timer |
| 11 | `buddy who orc-buddy-system` from the worktree | W | 22 lines, 2,116 bytes | hand, on the timer |
| 12 | `buddy bye <x> </dev/null` | F | nothing, exit 0; row ended | hand, at handoff |
| 13 | `buddy bye <top> </dev/null`, top bound to a live process | F | `stays live: still registered to running process(es) <pid>`, exit 1 | — |
| 14 | a successor `hello` under the label, before or after the old row's `bye` | F | either order: `buddy msg <label>` refused `ambiguous target … names 2 sessions as a label` (three pairs) | — |
| 15 | `buddy msg orc-fleet --dry-run` past two `top` rows, top holding `orc-fleet` | F | `would send … to top (claim orc-fleet)`; the real send printed the wake line | hand |
| 16 | `buddy msg orc-fleet --dry-run` from the ended orc-x | F | `… as operator` | — |

Counts, per session per ledger, as A runs them. **Top session:** in each
coordinated ledger, one hand `hello` at the start, one command per send
(its result carries the recipient's wake line, row 3), one per timer read
(row 11; each moves one column, row 10), and at handoff three (announce,
`bye`, the successor's `hello`); in the fleet ledger, nothing by hand for
mail: its PostToolUse drains (row 5), its Stop records idle (row 8), and
only that makes an upward send print its wake (rows 7 and 9); `inbox` is
there on demand. **Coordinator:** in the fleet ledger, one hand `hello`, one
command per report, one `bye` at its own handoff; nothing new in its home
ledger. The fixture's totals: F 6 session rows, 6 messages, all delivered;
W one message (#418, delivered by its recipient's own hooks) and two reads
on the lane's own live row. Cleanup in W: none owed (no row was made there;
#418 ages out with the sweep TTL).

What the fixture could not show. (1) A live top session whose cwd IS the
fleet repo: its hooks were stood in for by hook payloads piped from the
lane's shell (rows 5 and 8), which bound the lane's own process to the
fleet row; the one command that measures it is the orchestrator's (D-056):
`herdr tab create --workspace <ws> --cwd <fleet> --label top-1 --env BUDDY_LABEL=top-1`,
then `herdr agent start top-1 …`. (2) A coordinator's own hooks: orc-x was a
minted id with no process, so its home-ledger side was not run; its fleet
side is exactly the hand path rows 2, 4 and 12 measured. (3) Neither wake
was delivered (the upward one addresses this lane itself). (4) Approvals and
`pause` were not exercised; 3.2 stands as written. (5) One read each, not an
hour of timer reads. The lane's hooks did not follow its `cd <fleet> && …`
calls (the harness reset the cwd after each, and F's `last_seen` held between
calls), so a top session keeps its cwd in the fleet repo by running each
foreign command in a subshell; a coordinated ledger its hooks beat would
bind its process there, and row 13 is what `bye` then says.

**(B) Small buddy additions.** A ledger registry written at `init`
(`~/.buddy/ledgers`), `buddy sessions --every` / `ls --every` over it, `-C
<repo>` on every verb, and a cross-ledger `msg <project>/<target>`. Holds
invariants 1, 4 and 6 (today nothing requires a sender row: section 2; a
cross-ledger verb would have to add that rule or inherit the stamp problem).
Cut from the first slice: the registry is a new state location outside both
git and `~/.buddylist`, and the fleet repo's list (4.2) is the same fact in a
file the operator already edits; `-C` is `cd`; the `--every` views are the
rollup script; and cross-ledger `msg` is the hand registration with a verb in
front of it. B's one piece worth taking early is not a verb at all: the
sender stamp (4.3).

**(C) A ledger outside git for non-repo work.** A marker directory
(`.buddy-ledger/` holding `buddy.db`) found by walking up from the cwd when
git finds nothing, before "provably not a repo". Answers the non-code case
without `git init`. It adds a discovery SOURCE, not a third verdict (Codex
finding 11): an unreadable marker ledger is DENY and an absent marker is OFF,
as today; what changes is that "not a repository" stops meaning "off". That
is a change to what the gate refuses, at all nine discovery sites and in
`buddy init`, with `top` for path placement (the marker's directory): full
tier. Not recommended now: `git init` is one command, the fixture shows it
coordinates, and 2 live sessions are the whole measured demand. **OPEN** for
the operator: are there directories you will not `git init`? If yes, C is
the next plan.

**(D) A meta-ledger, or any authority above the per-repo ledgers.** CUT.
Invariants 1, 4 and 6: the authoritative record is a local row in the ledger
whose repo the work is in; a row ABOVE it would either duplicate (two
answers) or command (a compel path, D-030). Claims never cross ledgers. The
one thing D would buy, a machine-wide slot (the box's CPU during two fleets'
tiers; D-035 measured four concurrent suites once), is a claim on
`.buddy/slot/box` in the fleet repo's ledger that every coordinator
hand-registers in and honours: another ledger with a convention over it, not
an authority, and it bends nothing (Codex finding 11). **OPEN**: whether the
operator wants that slot; nothing contends it today that was measured.

Which invariants each holds: A all; B all, with the sender-row rule a
cross-ledger `msg` would have to add; C changes the gate's "non-repo is off"
verdict and so is full tier, bending no invariant's wording; D bends 1, 4
and 6.

## 4. Recommendation and landing order

Take A now, with B's stamp fix, and the model report of section 5. Each step
is one lane; the tier is per CLAUDE.md.

1. **Skill: "When the deliverable is not code" and "A fleet of fleets"**
   (chore: prose naming no new verb or flag; the skill tests still read every
   `buddy <verb> --flag` it teaches). The first maps commit → path and version
   word, tier → the workflow's own gate, main → the integration directory,
   READY → `wait --on <run> --note` read by `wait ls`, and says `git init` +
   `buddy init` for a notes directory. The second is option A's recipe: the
   fleet repo and its project list, the relay clause one level up, the
   routing rule, the hand registration and its retirement at handoff, and
   3.2's limits (approvals in the child's pane; pause per ledger). Measured
   before it landed (2026-10-27): the routing rule end to end over this
   checkout's ledger and a scratch fleet ledger, 16 steps, every downward and
   upward send delivered, the wake line printed both ways (upward only once
   the top's Stop had reported idle), each timer read moving one column; one
   gap found (a reused label is ambiguous for good, section 6), so the top
   session wears a numbered label and is addressed by its claim slug. The
   table and what the fixture could not show are under 3.4(A).
2. **`scripts/fleet-report.sh`** (lite): per coordinated ledger, counts and
   exception lines, output capped under 9,000 bytes with a "N more" line; a
   `check-fleet-report.sh` self-test invoked from `check.sh` like
   `check-wake-report.sh`. The ledgers come from an explicit list in the
   fleet repo (one path per line), each reported as available, unavailable,
   or with no live session (Codex finding 4: a ledger with open claims and
   queued mail but no live session is exactly the one the window must show,
   and `claude agents --json` cannot list it); the harness list supplements
   it with "sessions in ledgers not on the list". It reads each ledger
   through a private immutable copy with `sqlite3`, the way `buddy diagnose`
   reads (D-072) and `cost-report.sh` does, never through buddy verbs: a
   verb run under a registered id writes `last_seen`, and `store.Open` would
   migrate an older ledger (Codex finding 8). **Invariant 9**: the script
   prints counts and ids only, never a peer-written string: a session is
   `s-<8hex>`, a claim its claim id, a project the path from the operator's
   own list; no label, slug, note or description is ever rendered by it (the
   full roster, fenced by buddy, is one `cd` away). The self-test plants a
   label, a slug and a description carrying a newline and a 600-byte line in
   a fixture ledger and fails on any raw newline or any of those bytes in
   the output, so the rule cannot be defeated by a later "helpful" column.
3. **`msg`'s stamp for an id the ledger does not know** (lite; verb path, not
   the gate). Contract (Codex finding 10): a session id in the environment
   that is not a LIVE row here (absent, or ended) signs `unregistered
   s-<8hex>`, with `--from` appended as a tag the way a label's is
   (`unregistered s-66a24e29 (review)`); the bare terminal (no id) stays
   `operator`, with or without `--from`; the result line says the sender is
   not registered here and how to be (`hello --session`). Never refused
   (`senderFor`'s settled line). Correction ownership (`senderSession`), the
   drain headers and the 64-byte `fence.Line` of the sender column are
   untouched; `unregistered s-…` is one fenced sender, not a roster column.
   Tests: the dry run and a real send in a fixture, the stamp on the drained
   line for the absent and the ended case, the `--from` tag, the
   bare-terminal control, and the existing unknown-sender correction tests
   kept; mutate the arm and watch each die. `pause` and `resume` carry no
   sender stamp today, so nothing is proposed for them.
4. **`scripts/model-report.sh` and its export** (lite), section 5.2(a),
   under the same invariant-9 rule as step 2: counts, ids, the model string
   as the roster already renders it, and a class word only when it is one
   of the skill's fixed vocabulary (5.2(b)); anything else is counted as
   `other` and never printed. The same planted-newline self-test.
5. **The pick rule in the skill** (chore), section 5.2(b).
6. **Only then, if the report is read**: the item class column (full:
   schema), section 5.2(c).
7. **Not in this plan**: option C, option D, a ledger registry outside the
   fleet repo, `-C`, a cross-ledger `msg` verb, counted slots, `--ready`
   without a commit, any learner, any wake or launch by buddy, any change to
   what the gate refuses, any change to the sweep TTL. Each has its trigger
   named above or in section 5.

## 5. Model choice by the orchestrator, with learning from past lanes

### 5.1 What the ledger already holds

This checkout's ledger, read from a private immutable copy on 2026-10-26,
counts only. Schema 16; 147 sessions, 125 ended; 87 with a context row
(model, effort, prompt, cache tiers, the LAST turn only: one row per session,
so a session that changed model mid-way shows its last); 222 claims (27 slot
claims); 380 inbox rows; 33 wait rows (one per session, replaced on
re-declaration; 19 declared READY); 28 claims released with an outcome,
every one `pass`.

Per ended session, what a model report could join today:

| Fact | Ended sessions with it (of 125) |
| --- | --- |
| a model string | 77 |
| an item claim (not a slot, not `orc-*`) | 73 |
| a READY declaration surviving (`ready_sha`) | 16 |
| a run outcome reachable from that wait | 16 |
| all four | 13 |

By model, among the 77 with one: Opus 5.5 40 (32 with an item, 7 READY, 7
outcomes), Fable 5.1 20 (14, 9, 9), Haiku 4.5 14 (0, 0, 0), Opus 5 2, Sonnet
5.5 1. Effort: 56 `high`, 15 `xhigh`, 1 `max`, 1 `medium`, 14 none (Haiku).
Items per session: 40 sessions held one, 33 held two or more (max 8).

Requests and tool calls come from transcripts, not the ledger (the
`wake-report.sh` idiom: main thread, deduplicated by request id, counts
only). For the 79 ledger sessions with a transcript still on disk: Opus 5.5
n=30, 171 requests on average (max 395), 167 tool calls; Fable n=15, 89
(max 161), 113; Haiku n=14, 15 requests and **0 tool calls** (the D-056
verbatim runs and probes: not lanes). Narrowed to lanes that held an item
and declared READY: Fable n=7, 73 requests (33–133); Opus n=6, 135 (110–164).

What that sample is and is not. Opus rows include every orchestrator (average
lifetime 1,039 minutes, max 4,416); Fable rows are lanes (821, max 3,164);
Haiku rows are probes. **The split by model is a split by role.** Thirteen
lanes with all four facts, on different items with no recorded class, say
nothing about which model finishes an item in fewer turns. Every recorded
outcome is `pass`: a run's outcome is one fact about one run, never about an
item, and no item has ever been released with `--outcome fail`. The ledger's
only "fail" signal is words in mail: 17 rows with RED, 31 with FAIL, 12 with
"handoff size", 125 with READY, 66 with LANDED, 99 with DONE; these are
message statistics (`LIKE` matches: "no RED" counts), never failure
measurements. Of 39 sessions that sent a READY, 25 sent more than one (max
13), and a session holding two items sends two READYs that look exactly like
one item reworked: rework is not recoverable today. 68 of 147 sessions have
no transcript under any project directory: turn counts die with the
worktree. And **a plain `buddy sweep` deletes released claims (with their
descriptions and outcomes) and inbox rows older than 24 hours** (`SweepTTL`;
Codex finding 3), so nothing here accumulates: a month of waiting is a month
of 24-hour windows.

### 5.2 The smallest slice

(a) **`scripts/model-report.sh` and its export** (lite), counts only like
`wake-report.sh`, over a private immutable copy of the ledger
(`BUDDY_LEDGER` as `cost-report.sh` takes it). **The unit is the item claim,
never the session** (Codex finding 2): per item, the model and class tokens
from its description (b), the holder's session, the item's own outcome
(`claims.outcome`, b), how many times its slug was claimed (a re-claim after
a RED is a new claim id with the same slug: the rework count), and, where a
transcript exists, the holder's requests and tool calls between the claim
and its release. Printed per class × model as RATES with their denominators
(items, items with an outcome, reworked items, requests per item), and a
line for every cell with fewer than 10 items saying so. Because the sweep
deletes its inputs after 24 hours, the script has `--export <tsv>`: append
every item not yet exported (by claim id) to a file in the repo the
orchestrator keeps, so the register outlives the ledger's housekeeping; the
orchestrator runs it before each sweep (a line in the skill's duties).
**OPEN**: a sweep TTL knob instead; not proposed, since the TTL is also what
keeps the roster small. Invariant 9: the report and the export render counts,
claim ids, `s-<8hex>` session forms, the model string, and a class word only
from the fixed vocabulary in (b); a description's other bytes, a slug, a
label or a note are never printed, so no peer-written string reaches a line.
A `check-model-report.sh` self-test in `check.sh` plants a newline and an
over-long line in a fixture's descriptions and labels and fails on either
appearing in the output or the export.

(b) **The pick rule in the skill** (chore), applied by the orchestrator at the
start of each run by reading that report, under the operator's cap per run
(as review tiers are capped: "lanes on Opus or Fable; no Haiku lanes"):
- the standing rule stands: a lane needs a model with auto mode (Haiku 4.5
  measured asking for every call; and 0 tool calls across 14 sessions here);
- default to the cap's cheaper model for a class where the report shows its
  rework RATE and fail RATE no worse than the other model's; escalate one
  step where its rework or fail rate is clearly worse; below the floor, the
  cap's default and no inference. The floor (10 items per cell) and "clearly
  worse" (twice the other's rate) are PROPOSALS, not derived: nothing sets
  them yet, since 0 item fails are recorded and a power calculation needs an
  observed rate. The operator sets both numbers with the cap when the rule
  lands, and the report prints the observed rates so they can be revised;
- record the pick where the report can read it back: the item's claim
  description begins with two anchored tokens, `model=<id> class=<word> `,
  each at most once, `<word>` from a fixed vocabulary the skill lists (for
  example `docs`, `code`, `test`, `research`, `ops`), a missing, repeated or
  unlisted token read as "unknown" and counted as such, never printed
  (D-030's channel, 512 bytes; a grammar the orchestrator
  writes for the report, not prose; D-029's rule is about the id allocator
  and is not bent, Codex finding 11);
- and the item's outcome is its own release (Codex finding 12): an
  intermediate RED KEEPS the claim (the lane fixes and re-declares, as the
  recipe says); only a finished or abandoned item is released, `buddy
  release <item> --outcome pass|fail --note "<reds> <batches>"`; a handover
  uses `--to`. That verb exists for any claim today and has only ever been
  used on runs; D-049's semantics ("what became of the job the claim
  guarded") hold because the job has ended when it is used. A waiter on an
  item claim gets LANDED with that outcome, which is right.

(c) **Only if (a) is read for a month of exports**: `claim --class <word>`
and a `claims.class` column (full: schema), so class is typed rather than
parsed. Rows: class on `claims`; nothing else, since the outcome and the
rework count are already rows. Invariant 10 holds by construction: a class, a
pick and a count are observations the orchestrator acts on; no verb reads
them to refuse, and buddy never enforces a model.

### 5.3 What "self-learning" can and cannot be

At this sample (13 lanes with every fact, one model per role, zero recorded
fails, 24-hour retention) it is nothing: any rule fitted to it fits the role
split. With (a) and (b) running, the orchestrator learns the way the recipe
already works: reading a report and applying a written rule, with the
operator's cap above it. For a learner that adjusts its own picks, each class
needs both models tried on comparable items deliberately (otherwise the
stronger model gets the harder items and shows more turns), with outcomes
released per item. "About 30 items per model per class" is a PROPOSAL for
that floor, the operator's to set or a power calculation's once an item fail
rate exists; at this ledger's measured rate (73 item claims in about five
weeks) across two models and a handful of classes, that proposal is
arithmetic for two to three months of the export running, and it still
reads counts, never transcript text. Nothing in this plan proposes a
learner, and buddy never picks a model: the pick is the orchestrator's,
inside the operator's cap, visible in the claim.

## 6. Taken and refuted

One Codex design pass (gpt-6-astra, xhigh, 2026-10-26; the raw output is
kept in the lane's scratchpad as `codex-design-out.txt`, the prompt beside
it as `codex-design-prompt.txt`). Verdict: revise. Thirteen
findings; every one changed the text. Where a finding rested on a claim about
the code, the claim was re-read or re-measured before it was taken.

| # | Finding | Taken / refuted | Change |
| --- | --- | --- | --- |
| 1 | Top-session succession leaves foreign `top` rows and briefs naming the old id; sweep ends no session row | Taken; measured: a second `top` makes `msg top` ambiguous, and a hand `bye` ends an unbound row without `--force` | 3.3: successor announced by `msg` from the old id, claims by `--to`, foreign rows retired with `bye` before the successor registers |
| 2 | A per-session report cannot give class × model, item outcomes or item rework | Taken | 5.2(a): the unit is the item claim; tokens and outcome per item; rework = claims per slug |
| 3 | A plain sweep deletes released claims and old inbox rows after 24 h | Taken; `SweepTTL` re-read | 5.1 says so; 5.2(a) adds `--export`, run before each sweep; TTL knob marked OPEN and not proposed |
| 4 | Discovery from `claude agents --json` omits a ledger with no live session | Taken | 4.2: an explicit project list in the fleet repo, each ledger reported available / unavailable / no live session; harness list supplements |
| 5 | Counts without denominators; `LIKE '%RED%'` matches "no RED"; READYs conflate items | Taken | 5.1 labels them message statistics; 5.2(b) uses rates over items with a 10-item floor and no inference below it |
| 6 | `who <run>` shows no notes until a waiter has `ReadySHA`; the `--on` wait is not a timer | Taken; `who.go` re-read | 1.3: the notes are read with `wait ls` or the READY message; the wait rides LANDED |
| 7 | A reply TO a hand-registered row has no wake and no drain | Taken; `wakeAddr` re-read | 3.4(A): every message goes to the recipient's home ledger; a foreign row identifies a sender only |
| 8 | "Read-only" views write `last_seen` (D-086) and `inbox` marks; `Open` migrates | Taken in part: tonight's reads wrote nothing because this id has no row there (`noteHeard` finds none), which the text now says; the finding holds for a registered id | 2 and 4.2: the report reads a private immutable copy with `sqlite3`, never buddy verbs |
| 9 | The dirty scan and the commit gate work with an unborn HEAD; only `base` is absent; matcher `*` still never pauses `WebFetch`/`WebSearch` | Taken; `stagedPaths` and `readOnlyTools` re-read | 1.2 rewritten |
| 10 | The stamp change needs the ended-id and `--from` cases; `pause`/`resume` have no stamp | Taken | 4.3: full contract (absent or ended id → `unregistered s-<8hex>`, tag appended, bare terminal unchanged); the pause/resume item dropped |
| 11 | C is a discovery source, not a third verdict; B's sender-row rule does not exist today; a fleet-ledger slot bends no invariant; D-029 is about the allocator | Taken | 3.4 accounting rewritten; D stays cut on the authority-above ground; 5.2(b) gives the token grammar |
| 12 | `release <item> --outcome fail` on an intermediate RED ends the reservation and lands its waiters | Taken | 5.2(b): intermediate RED keeps the claim; only a finished or abandoned item is released; handover by `--to` |
| 13 | "One window" implies controls it does not have: child approvals, a machine-wide pause | Taken | 3.2: limits stated, stop-everything is the operator's per-ledger `pause all`, the top session's use of `pause` marked OPEN |

Nothing was refuted outright. The one place the draft's measurement stood
against a finding (8) is recorded above with its reason.

**Refuted by the 3.4(A) measurement (2026-10-27, `plan-skill` lane).** One
gap; the rows are 3.4(A)'s table.

| # | The plan said | Measured | Change |
| --- | --- | --- | --- |
| M1 | 3.3: retire the foreign `top` row with `bye` BEFORE the successor hand-registers, or `msg top` is ambiguous "until the operator sweeps" | Ambiguous in either order, and for good: label resolution (`store/target.go`, the exact-label arm) matches ended rows, and no code deletes a session row. Three pairs (rows 12 and 14): bye-then-hello and hello-then-bye both left `msg <label>` refused `ambiguous target … names 2 sessions as a label`; `hello` printed no label-worn-twice warning for an ended twin. A claim slug resolved past the duplicate (row 15) | 3.3 and 3.4(A): each top session wears its own numbered label (`top-<n>`, as coordinators wear `orc-<n>`), upward reports go to the claim slug `orc-<fleet>`, which `release --to` moves; the `bye` stays, so the foreign row reads ended rather than STALE. Not proposed: a label arm that skips ended rows (a resolver change, its own lite item if wanted) |

Two smaller corrections, not gaps. The "four commands" at handoff count as
three per coordinated ledger plus one `release --to` in the fleet repo (3.3).
And section 2's "no process registered, so it is unbound" holds only while
none of that session's hooks runs in that ledger: the first hook-shaped
`beat` there binds its process (row 5), and a bound row's hand `bye` from
the live session is refused (row 13). Hence the subshell rule in 3.4(A).
