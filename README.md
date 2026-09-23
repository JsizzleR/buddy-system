# The Buddy System

**A claims ledger and retro chat presence for fleets of AI coding agents.**

You run several coding agents in parallel on one machine. Two of them edit the
same file. One of them needs to be told to stop. You have no idea what any of
them are doing without tailing four terminals. The buddy system is the old
safety practice — pair up, so nobody wanders off alone and gets hurt — applied
to agent fleets:

- **`buddy`** — an atomic, per-repo **claims ledger**: agents claim a scope of
  the repo before working it, and a tool-call-boundary gate denies edits inside
  another session's claim, names the claimant, and honors an operator pause.
  SQLite, transactional, conservative about reclaiming. No daemon required.
- **`buddylist`** — the **presence layer**: a concierge daemon keeps your whole
  fleet visible in a chat room — a modern IRC server ([ergo]) by default, or
  the real **AIM 5.1 client** against an AIM-compatible server
  ([open-oscar-server]) when nostalgia calls — with
  a durable journal (the scrollback those protocols never had) and MCP tools so
  agents can read the room and talk back.

The two principles the design stands on:

1. **Chat is the view, never the lock.** Coordination and control live in the
   transactional local ledger; a chat message is never authoritative.
   *Announced is not locked.*
2. **Claims work with chat entirely absent.** The chat stack can be down, or
   not installed, without weakening safety. Safety hooks fail closed; chat
   hooks fail silent.

Built for [Claude Code] hooks and MCP today; the seams (a hook JSON contract, a
unix-socket daemon API, a `Conn` transport interface) are deliberately narrow
so other harnesses and chat backends can slot in.

## Quick start

Requires Go 1.26+ and `git`. Developed and used on macOS; the claims half should work on Linux but is untested there (the chat daemons are plain processes — supervise them with whatever your platform uses).

```sh
git clone https://github.com/JsizzleR/buddy-system && cd buddy-system
mkdir -p ~/bin
go build -o ~/bin/buddy ./cmd/buddy
go build -o ~/bin/buddylist ./cmd/buddylist
```

(Everything below assumes `~/bin` is on your `PATH`; the hook snippets spell
the path out so they work either way.)

**Upgrading on macOS: delete the old binary, do not copy over it.** `cp` onto
an existing Mach-O invalidates its ad-hoc code signature and the kernel
SIGKILLs the result — measured, exit 137 on the very next run. `go build -o`
replaces the file, so the command above is safe; `cp new ~/bin/buddylist` is
not. It matters because the hook lines end in `exit 0`: a killed binary is
indistinguishable from a binary that had nothing to say.

### 1. Claims (the safety half — start here)

```sh
cd /path/to/your/repo
buddy init    # creates buddy.db in the repo's git COMMON directory
              # ($(git rev-parse --git-common-dir)/buddy.db) — shared by every
              # worktree of the checkout, invisible to git
```

Wire the hooks into the repo's `.claude/settings.local.json` (machine-local;
`settings.json` if you want them shared). Every hook is guarded so a missing
binary just turns the feature off:

```json
{
  "hooks": {
    "SessionStart": [{"hooks": [{"type": "command",
      "command": "[ -x \"$HOME/bin/buddy\" ] && \"$HOME/bin/buddy\" hello 2>/dev/null; exit 0"}]}],
    "PreToolUse": [{"matcher": "Edit|Write|NotebookEdit|Bash", "hooks": [{"type": "command",
      "command": "[ -x \"$HOME/bin/buddy\" ] && \"$HOME/bin/buddy\" gate; exit 0"}]}],
    "PostToolUse": [{"matcher": "*", "hooks": [{"type": "command",
      "command": "[ -x \"$HOME/bin/buddy\" ] && \"$HOME/bin/buddy\" beat 2>/dev/null; exit 0"},
      {"type": "command",
      "command": "[ -x \"$HOME/bin/buddylist\" ] && \"$HOME/bin/buddylist\" alert 2>/dev/null; exit 0"}]}],
    "Stop": [{"hooks": [{"type": "command",
      "command": "[ -x \"$HOME/bin/buddy\" ] && \"$HOME/bin/buddy\" idle 2>/dev/null; exit 0"}]}],
    "SessionEnd": [{"hooks": [{"type": "command",
      "command": "[ -x \"$HOME/bin/buddy\" ] && \"$HOME/bin/buddy\" bye 2>/dev/null; exit 0"}]}]
  }
}
```

`Stop` is optional, and so is its companion:

```json
    "UserPromptSubmit": [{"hooks": [{"type": "command",
      "command": "[ -x \"$HOME/bin/buddy\" ] && \"$HOME/bin/buddy\" busy 2>/dev/null; exit 0"}]}],
```

`SessionEnd` (`bye`) is fenced by the **process**, not by the payload: the hook
JSON names a session id and no incarnation, and `claude --resume` keeps the id,
so a `bye` that fired late from a dead incarnation used to end the live one —
its heartbeats went silent and the next peer's `hello` orphaned its claims
mid-edit. Now `hello` and every `beat` register the `claude` process the hook
was spawned by, and `bye` ends the session only when no registered process is
still alive. A hand-run `buddy bye <id>` is refused by a live registration and
names the process; `--force` is the operator's act.

`Stop` marks the session idle at its prompt so the roster can tell "waiting for
a human" from "hard at work"; without it nothing ever prints `idle`, and
nothing else changes. `busy` retracts that mark when a new turn starts — which
matters only for a turn that runs **no tool at all**, because every other
turn's first heartbeat retracts it anyway. Wire `Stop` and skip `busy` if you
want one line instead of two; the cost is that a text-only answer reads as
`idle` while it is being written.

From then on, in any session:

```sh
buddy claim api-refactor --desc "reworking the handler layer" --scope internal/api
buddy ls                          # the board: who holds what, STALE flags
buddy release api-refactor
```

A claim is granted whole or refused whole, and a refusal names **every**
collision, not the first. To see the conflict set before taking anything:

```sh
buddy claim api-refactor --dry-run --desc "…" --scope internal/api --scope docs --scope cmd
# REFUSED: docs  (overlaps "docs" held by repo/s-82bacdd8, claim "docs-pass")
# would claim: internal/api, cmd
# dry run: 1 conflict(s); nothing was taken          (exit 1)
```

It writes nothing and exits non-zero on any conflict, so a scripted caller
cannot read "some of it was free" as "go ahead". A **stale** holder still refuses —
staleness marks, it never reaps — and both the refusal and the dry run say so,
because "somebody is working on this" and "somebody left" need different next
moves and only one of them is `buddy claim` again:

```
# REFUSED: docs  (overlaps "docs" held by repo/s-82bacdd8, claim "docs-pass")
#            — STALE: holder last renewed 3h ago; it still refuses, so ask the operator
```

A scope is freed by `buddy release`, by orphaning of a holder that has **said
`bye`** — which runs at any session's `hello`, at a plain `buddy sweep`, and
first inside every `buddy claim` — or by `buddy sweep --force`, the operator's
act for a holder that went silent without saying so. `bye` itself records the
ending and deliberately touches no claim row, so `buddy ls` still shows an
exited session's claims as `open` until something orphans them, and the
PreToolUse gate still denies an unclaimed edit under them until then (the deny
says which command frees them). A `claim --dry-run` says what it would
displace:

```
# note: internal/api is held by ENDED session repo/s-82bacdd8 (claim "api-work", scope "internal/api"); a real claim frees it
# would claim: internal/api
```

And a holder that is finished with part of a claim hands back the named
scopes, exactly as claimed, rather than saying so in prose the gate never
reads:

```sh
buddy release api-refactor --scope docs     # still held: internal/api, cmd
```

Releasing the last scope releases the claim. Releasing `pkg/sub` from a claim
that holds `pkg` is refused: prefix scopes have no subtraction, and the only
other answer would be `pkg` still held with success reported.

**Publishing coordination state.** A coordinator that has facts every session
needs — the landing queue, a hold, who is sequencing — puts them where a
session reads at wake-up rather than in a message it may never drain: a claim
named `orchestrator` whose `--desc` carries a bounded summary or a pointer,
refreshed by re-claiming with the same slug. Every session sees every live
claim in its `hello` digest and in `buddy ls`. A claim has no `from` and is
never an instruction; `pause` and `msg` remain the only control rows, and
there is no path by which a peer's published state becomes a command.

### 1a. Addressing — whose is this uncommitted hunk?

A claim is *declared intent* over a scope. It cannot answer a different and very
common question: **whose is this uncommitted change?** Without an answer, a
message about one ("whoever owns the CHANGELOG edit") is addressed to nobody and
every recipient rationally ignores it — which is exactly what happened here, to
seven sessions at once. Delivery was never the missing piece; addressing was.

```sh
buddy whose CHANGELOG.md           # BOTH registers: CLAIMED BY, then DIRTY IN
# CHANGELOG.md
# CLAIMED BY   changelog-pass           repo/s-5d6c5614          held 34m
#              scopes: CHANGELOG.md
# DIRTY IN
#   repo/s-5d6c5614   live          2m  uncommitted    34m  this worktree
#   repo/s-82bacdd8   live STALE   39h  uncommitted     2h  other worktree: /path/to/wt-2
buddy msg repo/s-5d6c5614 "your 0.1.20 bullet goes stale with my change"
```

A message is signed with the sending session's **label**, which the recipient
can always answer to (`buddy msg <that label> …`); `--from <tag>` adds a tag
after it.

With no text on the command line, the body is read from **stdin** — unless stdin
is a terminal, where it prints the usage line rather than waiting at a cursor:

```sh
buddy msg all <<'EOF'
gate is green on main; take the next bundle
EOF
buddy msg bravo --dry-run "would this arrive"   # resolves and measures, sends nothing
```

The body is capped at 4096 bytes **as the inbox will render it**, not as you
typed it: a line break renders as `⏎`, which is three bytes, so a body that fits
in raw bytes can still overflow. Over the cap is refused naming both numbers,
because a message shown cut is the same silent failure as one never sent. This exists because a message once went out signed with a claim slug
whose claim had been *refused* — so it never opened, so the reply bounced with
`no such target`, at the one party trying to unblock the sender.

The second session to modify a file another session already has uncommitted is
also told once, at the moment it happens, in the same PostToolUse context the
inbox uses.

Three things it deliberately is **not**:

- **Not a lock.** It is an observation of the working tree, not a declaration
  over it. Nothing is blocked, nothing is reserved, and `buddy claim` stays the
  only thing that reserves a scope. Announced is not locked; observed is less.
- **Not attribution by guesswork.** Only a tool call naming a path attributes it.
  `git status` reports the *union* of every session's edits in a shared checkout
  and attributes none of it, so it is used only to *retract* paths that have
  become clean. A file written by a Bash command is reported as dirty-but-
  unattributed rather than pinned on whoever scanned last.
- **Not silent about what it cannot see.** "No session recorded it" and "the file
  is clean" are different answers, and `whose` distinguishes them.

Holders that have gone quiet are still reported — their edit is still sitting in
the tree, so it is still a collision — but they are labelled `live STALE` or
`ended`, and no message target is offered for someone who cannot answer.

The notice never fires across worktrees: two sessions with the same relative path
dirty in different checkouts are editing different files. `whose` reports them
anyway, labelled, because it answers "who do I talk to?" rather than "am I in
conflict?".

And for the operator:

```sh
buddy pause session-2 --note "hold off — deciding the design first"
buddy msg all "CI is red, check before pushing"   # current live fleet only; expires after 24h
buddy resume session-2
buddy sweep --dry-run             # what a sweep would orphan and delete, by name; writes nothing
buddy sweep                       # tidies released/orphaned claims; never reaps live ones
```

What the gate promises — and what it doesn't: **installation is the opt-in.**
The `[ -x ]` guard means a machine without the binary simply doesn't have the
feature; but once a repo is `buddy init`-ed, the gate fails **closed** — an
unreadable or corrupt ledger denies mutating tools rather than shrugging.
And enforcement is **cooperative**.
It adjudicates the paths tools declare (including cross-repo targets against
the *target* repo's ledger) and fails closed when the ledger is unreadable, but
it cannot bind processes that bypass the harness. It is a seatbelt for agents,
not a sandbox against them.

### 1a′. One session, every register — `status` and `who`

```sh
buddy status                      # everything the ledger holds about YOU
buddy who <id|label|s-id|slug>    # the same report for any name a session answers to
# * repo/s-16c16a94  (16c16a94-…)  live  started 3h  seen 2m  idle 2m  pid 30479  pane herdr:w14:pA
# CLAIMS HELD  2
#   api-work                 held 2h   scopes: internal/api
#   docs-pass                held 20m  scopes: docs   STALE (not renewed 40m; still refuses)
# DIRTY PATHS  3 recorded to this session (observations, not locks): internal/api/x.go, …
# INBOX        1 undelivered
# EXIT         ending now would leave 2 claim(s) held — freed only when some session next runs hello, claim or sweep — `buddy release <slug>` first: api-work, docs-pass
```

A session has four names — its id, its label, the `s-<8hex>` short form, and
whatever claim slug peers address it by — and until this nothing took one and
returned the rest. `who` resolves any of them exactly as `msg` and `pause` do
(an open slug resolves; a released one does not) and prints the whole record.
The `EXIT` line is a **description of what the ledger would be left holding**.
It is not permission and it is not proof that killing the session is safe:
a coordinator can report that a session is safe to release and cannot obtain
consent to end it — there is no `buddy exit`, and there will not be one,
because it would be used in good faith on a relayed instruction and the
sessions that correctly refuse would look obstructive. The operator ends a
session with the `pid` or the `pane` on the row.

Two smaller courtesies ride the same change. `hello` warns, once, when your
`--label` is already worn by another live session — every `pause` or `msg` to
that label is refused as ambiguous, and you would otherwise learn it when a
peer's send bounced. And `msg` reports what the ledger holds about its
recipient, never a prediction: the line used to read `queued for X — delivered
after their next tool call` for every target, and 25 messages to four sessions
that had gone away all reported that (issue #24, D-032). Now one observation
leads it, most-alarming-first — `it ENDED 2h ago; nothing reads this unless that session
id helloes again` (naming the open claims a plain `buddy claim` displaces),
`its registered harness process (pid N) is GONE`, `bravo last reported idle 2h
ago; … delivery waits for its next tool call`, `NOT SEEN FOR 2h, past the 30m
stale mark`, `registered 30s ago and not seen since`, or `last seen 4s ago` —
and then how many earlier messages to it are still undelivered, with the age
of the oldest, which is the fact that proves a channel is not draining. It
never says *delivered*, because delivery is the recipient's act on its next
tool call; `buddy who <target>` is the check afterwards, and its `INBOX` line
dates the oldest undelivered row. No idle row and a recent beat is `last seen
4s ago`, never *busy*.

### 1a″. Authority files — a long session's copy of the rules rots silently

```sh
buddy authority                       # CLAUDE.md  (always)
buddy authority add docs/playbook.md  # at most 8; every entry is a stat per tool call
```

A session's copy of `CLAUDE.md` is a snapshot from session start, and a
compaction carries it forward faithfully. A coordinator nine hours into a run
quoted a sentence that had been corrected on disk seven hours earlier, and
every check it could run said current — main had moved zero commits, because
the correction was in a commit its snapshot already contained. Buddy knows
when each session started, so `beat` now says, **once per change, on the next
tool call**:

```
BUDDY: CLAUDE.md changed on disk 10m ago, AFTER this session started (2h ago) — the copy in your context may be stale; re-read it before quoting or acting on it.
```

`buddy status` carries the same as an `AUTHORITY` line. It is an advisory
about the file on disk — its modification time is later than your start —
and nothing more: not that the contents differ from what you read, not that
you have not re-read it since, and in a linked worktree not that `main` has
moved, since the file there changes only when that worktree pulls.

### 1a‴. The identifier register — `buddy ids`

```sh
buddy ids seed record 1704            # the artifact's MEASURED high-water mark; raise, never lower
buddy ids take record 5 --note "cve"  # took record 1705..1709 for repo/s-16c16a94
buddy ids ls                          # record  ceiling 1709  (next block starts at 1710)
buddy ids status record 1706          # RESERVED here by … / above the ceiling / not available
```

A repo that allocates decision numbers or row ids from append-only prose
files has a register that must be **parsed** to know what is taken, and in
one run that produced four false occupancy reports in a day: ids returned as
unused that were drafted in a document, an id "filed" that reached no file,
and a probe that read a range endpoint in a sentence as a taken number. A
duplicate is not a merge conflict — the driver appends both, silently.

So this register does the one thing prose cannot: `take` is one transaction
handing out the next contiguous block **above the ceiling**, recorded to the
session that took it. It **must be seeded** with the artifact's measured
high-water mark first, because it does not read the artifact and will not
guess the ceiling is zero. There is **no `return` verb**: a returned id is a
claim about intent, and the register cannot see a draft or a citation in
unlanded code — take from the ceiling, ids are free. Blocks outlive their
session. And `status` answers only in the three registers it actually holds:
reserved here (by whom, which block), above the ceiling ("unreserved in this
register", which is not "free"), or at-or-below and in no block ("not
available for allocation" — whether the artifact uses it, only reading the
artifact as a record can say).

### 1b. The commit gate — the second line

The tool-call gate only ever sees the path a tool *declares*. A file written by
a code generator, a shell redirect, or a formatter run across the tree reaches
the index without it ever being asked. That is visible at the commit boundary,
so there is a check there too:

```sh
sh scripts/setup-clone.sh          # sets core.hooksPath, inits the ledger
```

From then on `git commit` reports any staged path sitting inside another
session's open claim — grouped by claim, naming the slug, the owner, and
whether that owner is live, silent, or gone:

```
buddy commit-gate: 2 staged paths in 1 claim inside ANOTHER session's open claim:

  path "internal/router/proxy.go"
  path "internal/router/edge.go"
      claim "router-work" held by repo/s-5d6c5614 (live, last seen 40s)
      scope internal/router — edge cap rework

Warning only — the commit proceeds. Set BUDDY_COMMIT_GATE=deny to refuse instead.
```

It **warns and lets the commit through** by default. Nobody has yet measured how
often a commit in a shared checkout legitimately touches a peer's scope — a
handoff looks exactly like a mistake from here — so enforcing first would be
enforcing against an unmeasured false-positive rate. `BUDDY_COMMIT_GATE=deny`
flips it; `=off` or `BUDDY_COMMIT_GATE_SKIP=1` silences it; `--no-verify`
bypasses it for one commit.

What it deliberately does **not** do, so the promise stays honest:

- It reports **collisions only** — a path inside somebody else's open claim.
  It does not nag about paths nobody claimed, which would fire on nearly every
  commit; and it does not accuse you using the dirty-path table, because a
  peer's tool call naming a file is not authorship of your staged hunks.
- It **never refuses a commit it cannot attribute.** A human typing `git commit`
  has no session id; blocking that is how a hook gets uninstalled. It still
  reports the covering claims, wording them as "these may be yours".
- It covers the **ordinary commit path only**. `--no-verify` and `commit-tree`
  skip it; merge commits run a different hook; the commits that rebase,
  cherry-pick, revert and `git am` create do not run `pre-commit` at all.
- An unreadable ledger still fails **closed** here, exactly as the tool-call
  gate does. A repo that was never `buddy init`-ed stays silent.

### 1c. The roster — which session takes the next task?

```sh
buddy sessions                    # the roster; --by started for arrival order
# * repo/s-16c16a94  live         started 10h  seen 4s   /path/to/repo  (16c16a94-…)  idle 7m  claims 2  claude-opus-5/xhigh prompt 377k turn 7m cache 1h hot 53m
# - repo/s-299a236a  live STALE   started 15d  seen 12d  /path/to/repo  (299a236a-…)  PAUSED
# - repo/s-68a57050  ended 29d    started 36d  seen 34d  /path/to/repo  (68a57050-…)
```

Every age carries its own word, because one unlabelled column next to `live`
reads as uptime and was time-since-last-tool-call: a session ten hours old that
had just heartbeated rendered as `9s`. `started` dates *this incarnation's*
registration, `seen` the last hook that spoke for the session, and the state
word carries its own age when the state is a dated event (`ended 29d`).
`--by started|seen` picks which of the two orders the rows, both newest-first,
live rows always above ended ones. The first column is a gutter: `*` is the row
you are calling from, `-` is everyone else, and every row has one so the field
count never depends on which row you are reading.

Peer text in a column — a label, a slug — is rendered so it stays **one
whitespace-delimited token**: spaces show as `␣`, the way newlines show as `⏎`
everywhere else in this tool. A label is a minimum-width column and free text,
so without that a session could label itself `repo/s-aaaaaaaa ended 9d` and own
the state column of its own row.

The annotations after the id are what an orchestrator picks on:

- `PAUSED` — the operator's brake is on this session. Its next mutating tool
  call will be **denied**, and without this the row just says `live`.
- `idle 7m` — the session finished a turn that long ago and has run no tool
  since: it is waiting for a human, not working. This is the annotation that
  inverts the default order's meaning — `seen` ranks the session hardest at
  work FIRST, which is the opposite of "who can take the next task".

  **Absence is not evidence of busy.** `Stop` is a hook line a machine may not
  have, so a row with no `idle` has simply not reported. And the catch worth
  knowing before you route on it: an idle session is also the one that will
  not *see* a `buddy msg` until its next tool call, because delivery rides the
  heartbeat. It is the session that can take work and the one that needs a
  human to poke it.
- `claims N` — open claims held now. The names are in `buddy ls`; the row
  carries the count, because a slug is 128 bytes of free text and a session may
  hold several.
- `pid 30479` — the harness process this row IS: the `claude` process the
  session's hooks were spawned by, found by walking up from the hook (by exec
  path and `argv[0]`, never by the kernel's `p_comm`, which for the launcher is
  a version string). It is what the operator kills to end the session without
  asking it — a coordinator can report that a session is safe to kill and must
  never be able to obtain permission to kill it, and a pid on the roster keeps
  the coordinator out of that loop. `pid 30479 GONE` means the process is no
  longer there (killed without `bye`): diagnostic only, nothing ends or reaps
  on it. Two pids on one row (`pid 100,200`) is a session id opened twice
  (`--resume` while the first still runs); the row ends only when the last one
  says `bye`. A row with no `pid` is **unbound** — its `hello` was hand-run or
  predates this, and its `bye` ends it as it always did.
- `pane herdr:w14:pA` — the terminal the harness inherited its environment
  from (`HERDR_PANE_ID`, else `TMUX_PANE`), reported at registration: which
  window on the operator's screen this row is. Buddy never acts on it.
- `claude-opus-5/xhigh prompt 90k turn 4s` — what the session is running and
  how much context it was last seen carrying. The model and the effort are read
  from the same record as the counts and printed only when it recorded them:
  across this box's transcripts the model discriminates (six `claude-opus-5`,
  one `claude-fable-5`) and so does the effort (six `xhigh`, one `high`), and
  two sessions on the same model at different efforts are different
  instruments. `beat` reads the tail of the session's own transcript (the
  hook JSON already names the file) and stores the newest turn's token counts:
  input + cache read + cache write, because a cached token occupies the window
  exactly like a fresh one. **No message text is ever stored or printed** —
  counts, a model id and two timestamps.

  It says `prompt`, not "context left": that is the last prompt the model was
  handed, and the session has been working since. The sample is refreshed by
  every tool call and by the `Stop` hook, so a session that has gone quiet at
  its prompt still reports the size it finished on rather than freezing at its
  last tool call. The turn's own age prints
  beside it always, so a reading taken six hours ago cannot be mistaken for one
  taken this second — a peer that has compacted from 90k to 20k since is
  exactly the wrong session to pass over.

  A percentage appears **only** against a window you declare:

  ```sh
  export BUDDY_CONTEXT_WINDOW=1M     # or 200k, or a bare token count
  # repo/s-16c16a94  live  started 10h  seen 4s  …  prompt 90k/1.0M 9%  turn 4s
  ```

  It is declared and not inferred because it cannot be inferred: a session
  running the 1M-token variant writes the same model string into its transcript
  as the 200k one, and 90,499 tokens is 45% of one window and 9% of the other.
  Unset, the row prints the count and no percentage.

- `cache 1h hot 48m` — which prompt-cache lifetime the session's last turn
  wrote (the API offers 5 minutes or 1 hour) and how much of it is left; past
  it, `cache 1h cold 3m` says how long ago it lapsed. A peer whose cache is
  warm is cheap to hand the next task to; one whose cache has lapsed rewrites
  its whole prefix on the next request. The tier comes from the same
  transcript record as the counts (`usage.cache_creation`), the clock is the
  turn's own time printed just before it, and a record that wrote both tiers
  prints `cache 1h+5m` and is judged by the shorter one. No tier recorded
  prints nothing — an older harness that wrote no such object was not on the
  5-minute tier, it was silent.
- `waiting 1h12m` — the session has **declared** a wait (`buddy wait`, below),
  dated by the declaration. It does not reset when the session's keep-alive
  pings, though `idle` does: a scheduled turn runs both the `UserPromptSubmit`
  and the `Stop` hook (measured), so `idle` is the truth about the last turn
  and `waiting` is the number to read. `waiting 1h12m LANDED` / `EXPIRED` means
  the clock and the claims say the wait is over and the session has not
  checked yet.

### 1c′. Waiting without going cold — `buddy wait`

```sh
buddy wait --on api-work --until 3h --note "then: rebase, run check.sh"
# WAITING on claim "api-work" (held by repo/s-4856919d, seen 3m ago) — deadline in 3h0m; note: then: rebase, run check.sh
# keep-alive: cache 1h tier (last written 2m ago). Arm it in THIS session: /loop buddy wait check — each check is one tool call …
/loop buddy wait check               # typed in the WAITING session: its own scheduler, self-paced
# STILL WAITING on claim "api-work" (held by …, seen 3m ago) — 1h12m so far, deadline in 1h48m; next check in 50m (3000s from now)
# last observed request 50m ago read 398k, wrote 1k (mostly read from the cache)
# … and once the holder runs `buddy release api-work`:
# LANDED: claim "api-work" released 20m ago — the wait is over after 2h1m; stop the /loop that runs this check (schedule no further check)
# note: then: rebase, run check.sh
buddy wait ls                        # every open wait, oldest first     buddy wait clear   # withdraw yours
```

A session parked waiting for a peer — a claim to be released, a serialized
hour-long test tier, a review slot — runs no turn, and every prompt-cache
entry here is written on the **1-hour tier**. Past the hour its next request
re-writes the whole prefix at twice the base input rate. Measured over 14
days of this box's transcripts: 187 requests after a gap over an hour re-wrote
54M tokens, 108 of them under four hours. A cache **read** costs a tenth of
base and restarts the hour, so one cheap request inside each hour is all it
takes; and because that request is a tool call, `beat` drains the parked
session's inbox as well, which nothing else was doing.

**The trigger is the session's own.** `buddy wait` records what the session
is waiting on (open claims of other sessions, resolved once to their claim
ids; no `--on` is a plain timer), until when, and a note to itself. The
session then arms its **own** harness scheduler with `/loop buddy wait check`.
Buddy never wakes, schedules or types into anything. The **zero-code form**
of this feature is the operator typing that same `/loop` line into a parked
pane by hand. Each check prints one verdict — `STILL WAITING` (with when the
next is due), `LANDED` (with the note), `EXPIRED`, or `NO WAIT` — and every
verdict that ends the wait says to stop the loop. `beat` also says `LANDED`
once on the session's next tool call, so a session that never armed a timer
learns anyway. A refused `claim` prints the `buddy wait --on …` line that would
wait for it; it never registers one.

**Self-paced is the recommended form; `/loop 30m buddy wait check` is the
fallback.** The check paces from itself: `next check in 50m`, or less when the
deadline is sooner. It does not pace from the ledger's cache clock, which lags
one request, because a check runs before its own `beat` records it; a formula
built on that clock pinged twice per period. Fifty minutes, not fifty-eight,
because the scheduler times the wake from the scheduling call and fires up to
58 s late (measured: it rounds up to the next minute). Scheduled firings
measured warm at up to 3,602 s after the previous request and cold at 3,633 s
and beyond. The harness's own tool text says any delay up to an hour wakes
warm; measured, 9 of 11 one-hour wakes came back cold. The fixed form can't
express a uniform 50-minute cron period, so it pings every 30 minutes: two
reads an hour instead of 1.2, and correct.

**What a keep-alive costs,** per hour of waiting, at list multipliers (1h write
2× base, read 0.1×, Fable 5.1 read 0.025×), for a 400k-token prefix. No verb
prints a dollar figure; pricing is external and changes:

| | Opus 5 | Fable 5.1 |
| --- | --- | --- |
| One cold re-write | $4.00 | $8.00 |
| One warm ping | $0.20 | $0.10 |
| Keep-alive per hour (1.2 pings) | $0.24 | $0.12 |
| Break-even wait length | ~16 h | ~66 h |
| Saved on a 65-minute wait | 95% | 99% |
| Saved on a 4-hour wait | 80% | 95% |

Hence a **required deadline**: default 3h, ceiling 12h (refused above it).
The question that matters is whether the session will be resumed at all, and
the declaration is that knowledge written down by the one party that has it.
No open wait means no ping. A session on the **5-minute tier** gets no
keep-alive: keeping a five-minute entry warm costs 1.5× base per hour against
a 1.25× re-write. The wait is still recorded, and the declaration and the
check both say why.

What it deliberately is not:

- **Not a reservation.** A wait reserves nothing and refuses nothing. No gate
  or claim reads it, and a waiter has no more standing on the scope it waits
  for than anyone else. `release` names the sessions waiting on a claim, and
  `who` shows `WAITING` for a waiter and `WAITED ON` for a holder, as
  information only.
- **Not inferred.** A refused claim, an idle report and silence are not waits.
  No row means the session has not said.
- **Not a check anyone else can run.** `wait check` speaks only for the
  session in `$CLAUDE_CODE_SESSION_ID`: it records a keep-alive and can clear a
  LANDED verdict, and neither is true of a check typed in another shell.
  `buddy who <target>` and `buddy wait ls` look from outside without touching
  anything.
- **Not durable across a restart.** The harness keeps scheduled tasks for the
  session only, not on disk. `hello` restates an open wait and asks the
  session to re-arm the loop only if it finds none scheduled. After a `bye` and
  a resume, `hello` names the earlier run's wait as ended and prints the line
  that would declare it again.

### 2. Presence (the fun half)

Run an [ergo] IRC server — a single Go binary. Make the loopback binding
explicit rather than trusting defaults:

```sh
ergo defaultconfig > ergo.yaml
# edit ergo.yaml: under server.listeners keep ONLY "127.0.0.1:6667" (and
# remove the TLS listener unless you provision certs), then:
ergo run --conf ergo.yaml
```

The servers here run unauthenticated *because* they bind loopback. If you
ever bind a real interface, turn on auth first (ergo has SASL). Then:

```sh
buddylist serve --rooms yourproject,ops       # the concierge daemon (buddylistd)
buddylist say yourproject "hello fleet"       # relay as [operator]
buddylist read yourproject                    # the journal: durable scrollback with seq cursors
buddylist read yourproject --tail 20          # just the newest 20 — no forward walk
buddylist read yourproject --mentions alpha   # only messages naming alpha
buddylist status --session <id>               # per-room counts: newest, unread, addressed
buddylist who                                 # live room membership
buddylist dm jsizl "nightly is RED"           # exits non-zero if nobody is there to receive it
```

There is no offline delivery here, so `dm` asks the server whether the
recipient is online before it sends, and refuses — naming them, having sent
nothing — when they are not. That is what makes it usable from a script:

```sh
buddylist dm "$OPERATOR" "nightly RED: $summary" || notify_some_other_way
```

Only a definite *not online* refuses. A backend that cannot answer the
question (AIM/TOC has no synchronous presence query), a probe that fails, a
server without the command — all still send, exactly as before. A presence
check that breaks must cost a diagnosis, never the message.

Point any IRC client at the server ([Halloy] is the maintained XChat-shaped
one) and watch the fleet. Each live session is its own buddy there: it joins
its project's room under its own name, wears its claim as an away message
(`claim: p4-session-presence · active`), goes idle after 5 minutes, and leaves
after 30 — so the nick list IS the fleet, and `/whois` answers "what is that
one working on?". It is presentation only: session buddies never speak (the
concierge still relays), never journal, and never touch the ledger — the claim
slugs ride along on the alert hook that had already read them. `serve
--presence=false` turns it off; the AIM backend never had it, because a TOC
screen name is an account and a second signon boots the first.

Register the compact MCP profile locally in each
Buddy-enabled repo; it exposes only `chat_send` and `chat_read`, so unrelated
projects and routine model turns do not carry unused tool schemas:

```sh
claude mcp add-json buddylist "{\"type\":\"stdio\",\"command\":\"$HOME/bin/buddylist\",\"args\":[\"mcp\",\"--profile\",\"core\"]}" --scope local
```

The operator-oriented `chat_status` / `chat_ack` / `chat_who` / `dm` /
`set_status` surface remains available with `buddylist mcp --profile full`.

For the full 2002 experience instead: run [open-oscar-server] and
`buddylist serve --backend toc`, then sign into real AIM (on macOS,
[im-for-macos] runs AIM 5.1 natively). The daemon speaks the era's wire
charset (CP1252), flattens AIM's HTML message envelopes, and joins the
exchange the AIM client's own Buddy Chat dialog uses. You will hear the door.

## How it holds together

```
agents (Claude Code sessions)                            operator
  │ hooks: hello/gate/beat/bye          ┌─ IRC client (Halloy) / AIM 5.1
  ▼                                     ▼
buddy CLI ──► <git-common-dir>/buddy.db      ergo / open-oscar-server
  claims · controls · inbox                        ▲
  (SQLite, transactional)                          │ ircwire / tocwire (Conn seam)
  │                                          buddylistd ──► ~/.buddylist/journal.db
  └─ MCP tools ──────────────────────────────► unix socket   (durable room history)
```

- One ledger per repo, in the git **common dir** — shared by every worktree,
  invisible to git.
- Sessions are identified by `(session_id, incarnation)`; PIDs are diagnostic
  only. Delivery is at-least-once; sweeps never reap what might be alive.
- The journal records **what the server saw** (IRC needs IRCv3 `echo-message`
  for that; the daemon negotiates it). Reads paginate by seq cursor and report
  retention gaps explicitly — silence and "nothing" are different answers.
- **Catching up is O(new), not O(history).** `chat_status` answers "is there
  anything, and does any of it name me?" in counts alone — no chat text, so a
  session can decide whether reading is even warranted. Then `tail=N` takes the
  newest messages directly, `mentions_me` narrows to the directed subset, and
  `since_last=true` returns exactly this session's backlog and advances its
  saved cursor.
- The read cursor advances **only over a window that leaves nothing unseen
  behind it**. `since_last` refuses to combine with `tail`, `before`, an
  explicit `after`, or a mention filter, and it saves only what actually fit in
  the result — a cursor that stepped over a dropped row would make that row
  unreachable forever. Which rows a byte budget drops follows the read's
  direction: a forward page keeps the oldest, a tail keeps the newest.
- **A message that names you finds you.** The `buddylist alert` hook tells a
  session, on its next tool call, that a room message named it — which room,
  how many, which seqs, and the exact `chat_read` that fetches them. It carries
  no chat text: the alert says go look, and the deliberate read is where the
  byte budget and the untrusted-content fence live.
  - The names it matches are the session's **claim slugs** first, then its
    label and id. That is the one place the chat half reads the claims half,
    and it is why the feature works at all: measured against a 2313-message
    room, identity alone matched **0** messages, because peers address each
    other by slug.
  - It never alerts a session about its own post. That is harder than it
    sounds — the wire chunks a long message and only the first chunk carries
    the `[label]` attribution, so a session's own announcement of its own slug
    arrives looking exactly like a peer naming it. The discriminator is the
    outbox: every echoed chunk is a substring of the row that recorded the
    submission (measured, 13 of 13 on a 3566-byte send). Across four real
    sessions the filter suppressed 7 self-alerts and kept all 3 genuine ones.
  - The alert cursor is **not** the read cursor. Being told about a message is
    not having seen it, and a session that has read a room must still be told
    about a later one that names it.
- Everything agents read back from chat is fenced as untrusted, one line per
  message, spoof-resistant. Prompt injection through a chat room is assumed,
  not hoped away.

## Cost controls

Buddy itself makes no model calls. Its model cost comes from text deliberately
placed into an agent's context, so the defaults keep that text narrow:

- `buddy msg all` snapshots the sessions live at send time. A session created
  later never inherits the broadcast, and an undelivered broadcast expires
  after 24 hours. Use it for urgent fleet-wide interjections; put routine
  status in chat or target one session with `buddy msg <target>`.
- **A target is resolved before it is stored, and an unresolvable one is
  REFUSED.** `pause`, `resume` and `msg` all take the same target: `all`, a
  session id, a label, an `s-<8hex>` short form, or an **open claim slug** —
  slugs included because that is how peers address each other. Matching is
  exact. Anything else exits non-zero and writes nothing, rather than queueing a
  row that would match nothing and reporting success.
- MCP `chat_read` defaults to 10 rows and a 4 KiB result. `mentions_me=true`
  includes current claim slugs; `tail=N` is the normal catch-up path. An
  explicit limit above 10 opts into the 16 KiB history budget.
- MCP `chat_send` has a 750-byte routine budget. Prefer a compact
  claim/outcome/blocker/next/reference update. A deliberate handoff can pass
  `long=true`, up to the 4096-byte hard cap.
- The proactive alert hook injects counts and sequence numbers, never chat
  bodies. Silent hooks add no model context.

For a privacy-preserving seven-day baseline (counts and byte lengths only):

```sh
scripts/cost-report.sh
# Or point it at another Buddy repo's ledger. That ledger lives in the git
# COMMON dir, which is `.git` only in a plain checkout — in a `git worktree`
# checkout `.git` is a FILE and `.../.git/buddy.db` names nothing:
common=$(cd /path/to/repo && git rev-parse --path-format=absolute --git-common-dir)
BUDDY_LEDGER="$common/buddy.db" scripts/cost-report.sh
```

Set `BUDDY_COST_DAYS` to change the window. The report never prints messages,
prompts, or tool results.

## Security posture

Everything binds loopback by default. The chat servers run unauthenticated
*only* because of that; if you take them off loopback, turn real auth on
(ergo has SASL) and treat the wire accordingly. The claims ledger trusts the
machine's user account — this is a single-operator tool, not a multi-tenant
boundary.

## Status

Working, tested (hermetic suites plus a live end-to-end against the real
pinned servers — `scripts/check.sh`), and used to coordinate the agent fleet
that built it. Design rationale, measured facts, and refuted assumptions:
[docs/DESIGN.md](docs/DESIGN.md); the rulings behind them, and what is known
to be unfixed, are in [docs/decisions.md](docs/decisions.md). Roadmap-ish: a
chat-command bridge once auth is on, and multi-machine coordination.

## License

Apache-2.0.

[ergo]: https://github.com/ergochat/ergo
[open-oscar-server]: https://github.com/mk6i/open-oscar-server
[Claude Code]: https://claude.com/claude-code
[Halloy]: https://github.com/squidowl/halloy
[im-for-macos]: https://github.com/mk6i/im-for-macos
