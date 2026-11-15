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
- **You are started with `BUDDY_FLEET=<name>`,** in a directory with no ledger
  of its own: your own hooks read the variable from your process environment.
- **Each lane is a herdr tab with the variable** (Opening lanes), in its own
  plain directory:
  `herdr tab create --workspace <ws> --cwd <dir> --label <label> --env BUDDY_LABEL=<label> --env BUDDY_FLEET=<name> --no-focus`,
  then `herdr agent start` as usual. `claude --bg` carries no variable, so a lane
  started that way is in no fleet.
- **The brief's first step is `buddy status`.** In a fleet its first line
  starts `LEDGER`, then `fleet <name>: <path>`. A lane whose `status` fails, or names another
  ledger, says so in its pane and stops: it is on no roster you can read.
- **A lane's directory must have no ledger of its own.** Inside a repository
  that ran `buddy init`, the home ledger wins: its `hello` says
  `BUDDY_FLEET=<name> is not used here`, it is on that repository's roster,
  and the fleet's mail never reaches it. A lane that `cd`s into one later
  leaves its row behind: it stops moving and goes GONE with its slot claim
  open, your wait on it does not land, and its `busy` (if wired) says so at
  each prompt. The repository's gate governs its writes there, which is
  right; joining both ledgers is stage 2.

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

A lane launched without the variable is simply off:
`buddy who <its full session id>` refuses, which is how you see it. `/clear` in a lane mints a new
session id: a new row, the old one's mail stranded (D-034).

## From a governed repository

A session whose own ledger is a repository's can still read a fleet and send
into it by hand, each verb from a plain directory with the variable set, in a subshell:
`cd ~/.buddy/fleets/<name> && BUDDY_FLEET=<name> buddy sessions`. Its own
hooks never drain the fleet's mail. A session on both rosters at once is not
built (stage 2 of `docs/NON-GIT-PLAN.md`).
