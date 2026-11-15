# Investigations outside git: a fleet ledger

Part of the `buddy-fleet` skill, beside its SKILL.md, read when the operator
asks you to coordinate lanes that work in plain directories, outside every
repository (D-191). A section named in parentheses is SKILL.md's unless this
file has it.

Outside git, buddy is off: no row, no inbox, every ledger verb exits 1. A **fleet
ledger** is a coordination-only ledger under the user's home,
`~/.buddy/fleets/<name>/buddy.db`: a roster, messages with their wake
addresses, waits, outcomes with notes, and slot claims. Nothing else. **The
gate never reads it**, so it reserves no file and has no brake: two lanes
writing one file outside git are refused by nothing. Say so in every brief.

## Creating one, and launching into it

- Only on the operator's word, as for opening lanes: `buddy init --fleet <name>`.
  A name is 1 to 64 of `a-z`, `0-9` and `-`, starting with a letter or digit,
  taken exactly (no folding, no trimming). Use one per batch (`inv-a`): a
  label reused by a later batch is ambiguous for good. It prints the ledger's
  path and the variable to launch with, and says so when the ledger is already
  there; it refuses, changing nothing, what a lookup would refuse. A lane
  never runs it.
- Never put the fleet home on a synced or network volume (a `~/.buddy`
  symlinked into one included): SQLite under a tool that copies a live
  database is not measured. `~/.buddy/fleets` and each fleet must be real
  directories, not symlinks, and a fleet holds only its ledger: a git
  directory's mark there (`HEAD`, `objects`, `refs`, `commondir`, `gitdir`) or
  a hard-linked `buddy.db` makes it unavailable.
- **You are started with `BUDDY_FLEET=<name>`:** your own hooks read the
  variable from your process environment, from a plain directory or, on both
  rosters, from a governed repository (below).
- **Each lane is a herdr tab with the variable** (Opening lanes), in its own
  plain directory:
  `herdr tab create --workspace <ws> --cwd <dir> --label <label> --env BUDDY_LABEL=<label> --env BUDDY_FLEET=<name> --no-focus`,
  then `herdr agent start` as usual. `claude --bg` carries no variable, so a lane
  started that way is in no fleet.
- **The brief's first step is `buddy status`.** In a fleet its first line
  starts `LEDGER`, then `fleet <name>: <path>`. Inside a governed repository it
  names that repository's ledger and adds `the hooks also report to fleet
  <name>`; such a lane checks with `buddy fleet status` instead. A lane whose
  `status` fails, or names another ledger without that tail, says so in its
  pane and stops: it is on no roster you can read.
- **A lane inside a governed repository is on both rosters** (stage 2):
  its `hello` says `also session <label> in fleet <name>`, its hooks drain
  both inboxes, and its bare verbs act in the repository's ledger, so its
  brief tells it to report with `buddy fleet msg orc-<name> "..."` and to
  claim its slot with `buddy fleet claim`, in the slot form `buddy claim`
  takes below (slot scopes only). The repository's gate governs its writes there, which is right. A
  lane that `cd`s into one later keeps its fleet row moving.

## What works, and what is refused

Working as in a repository: `buddy msg`, `buddy inbox`, `buddy sent`, every
`buddy wait` form but `--ready`, `buddy wait check`, `buddy sessions`,
`buddy who`, `buddy status` (`BASE` reads "not kept in a fleet ledger"),
`buddy release` with `--outcome`, `--note` and `--to`, `buddy sweep`,
`buddy ids`, and `buddy claim` on a slot: the item's name as
`--scope .buddy/slot/<item>`, and yours as `--scope .buddy/slot/orc-<name>`.

Refused, each `refused: ... in fleet <name>: <why>`: a claim on any other
scope, on `.buddy/slot/gh-stack`, `--shared` or `--based-on`; `buddy pause`
and `buddy resume` (stop a lane in its own pane); `buddy whose` and
`buddy authority` (no paths); `buddy wait --ready` (no commit: the note
carries the path); `buddy release --in-run` and `--unpark` (nothing
integrates, so nothing parks).

## The loop

- **A lane needs input:** `buddy msg orc-<name> "NEED INPUT: <question>"`,
  deliver the wake, then `buddy wait --until 2h --note "NEED INPUT: <topic>"`,
  and `buddy wait clear` once answered. The roster says `waiting` and
  `buddy wait ls` the topic; the harness's own waiting state catches a lane
  stopped at an approval prompt, which runs no verb.
- **A lane is done:** `buddy release <item> --outcome pass|fail --note "DONE: <path of the findings>"`,
  then a message to `orc-<name>` with the headline, and the wake. It is a
  report, not a landing: nothing merges. A change an investigation finds for a
  repository is a new item there, through that repository's run, tier and
  review.
- **You watch** with one wait on every item claim, `buddy wait --on <item> --on <item> ...`:
  each `buddy wait check` lists every claim, the released ones with outcome
  and note. A blocking wait returns only when all have landed; the
  per-finish signal is the lane's message. Read the outcomes before a plain
  sweep deletes them (a released claim, 24 h after its release).
- **Handing off:** `buddy release orc-<name> --to <successor label>`, as in a
  repository (Handing off a coordinator).

## When it does not open

Each is one line in the session's SessionStart context, repeated at every
prompt while it lasts if `busy` is wired, and the exit of every fleet verb; no
tool call is ever refused for it:

- `BUDDY_FLEET=<name> names no fleet ledger on this machine (looked in <path>)`:
  a mistyped name, another `HOME`, or a fleet not yet created.
- `... is not a fleet name`: relaunch with a well-formed one.
- `fleet ledger unavailable (<cause>)`: fix what the cause names, or ask the
  operator. Never delete it: it holds every lane's messages, waits and
  outcomes.
- `fleet ledger error (<cause>)`: it opened and a read or write failed,
  usually a held write lock; the next hook or verb retries. Ask the operator
  if it repeats.
- `this session is not on fleet <name>'s roster` (from `busy`): run the
  `buddy hello --session <id> </dev/null` it names.
- Beside a governed repository the first three end `this session is on no
  fleet roster and gets no fleet mail (this directory's own ledger works as
  before)`, and `busy` may add `this session is not on the roster of this
  directory's own ledger`: run the `buddy hello` it names.

A lane launched without the variable is simply off:
`buddy who <its full session id>` refuses, which is how you see it. `/clear` in a lane mints a new
session id: a new row, the old one's mail stranded (D-034).

## From a governed repository (both ledgers)

A coordinator living in a repository that ran `buddy init` (the operator's
orchestrator pane) coordinates a fleet when it is STARTED with
`BUDDY_FLEET=<name>`: its hooks keep it on both rosters and drain both
inboxes into one digest, the fleet's mail under its own line. A session
already running without the variable cannot join (no `join` was built): at
its next handoff, launch the successor with the variable.

- A bare verb acts in the repository's ledger. Act in the fleet with
  `buddy fleet <verb>`: `buddy fleet sessions`, `buddy fleet msg <lane> "..."`,
  `buddy fleet claim orc-<name>` (slot scopes only), `buddy fleet wait` on the
  lanes' items, `buddy fleet ls`, `buddy fleet inbox`. Never `cd` into the
  fleet's directory for this: with `$HOME` a governed worktree, discovery
  finds that repository's ledger from there.
- A reply to fleet mail goes with `buddy fleet msg`; a bare `buddy msg` to a
  name only the fleet knows is refused as a miss.
- `/loop` keep-alive on a fleet wait: `/loop buddy fleet wait check`.
