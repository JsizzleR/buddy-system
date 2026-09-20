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
cannot read "some of it was free" as "go ahead". And a holder that is finished
with part of a claim hands back the named scopes, exactly as claimed, rather
than saying so in prose the gate never reads:

```sh
buddy release api-refactor --scope docs     # still held: internal/api, cmd
```

Releasing the last scope releases the claim. Releasing `pkg/sub` from a claim
that holds `pkg` is refused: prefix scopes have no subtraction, and the only
other answer would be `pkg` still held with success reported.

### 1a. Addressing — whose is this uncommitted hunk?

A claim is *declared intent* over a scope. It cannot answer a different and very
common question: **whose is this uncommitted change?** Without an answer, a
message about one ("whoever owns the CHANGELOG edit") is addressed to nobody and
every recipient rationally ignores it — which is exactly what happened here, to
seven sessions at once. Delivery was never the missing piece; addressing was.

```sh
buddy whose CHANGELOG.md
# CHANGELOG.md
#   repo/s-5d6c5614   live          2m  uncommitted    34m  this worktree
#   repo/s-82bacdd8   live STALE   39h  uncommitted     2h  other worktree: /path/to/wt-2
buddy msg repo/s-5d6c5614 "your 0.1.20 bullet goes stale with my change"
```

A message is signed with the sending session's **label**, which the recipient
can always answer to (`buddy msg <that label> …`); `--from <tag>` adds a tag
after it. This exists because a message once went out signed with a claim slug
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
