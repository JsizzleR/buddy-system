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
    "SessionEnd": [{"hooks": [{"type": "command",
      "command": "[ -x \"$HOME/bin/buddy\" ] && \"$HOME/bin/buddy\" bye 2>/dev/null; exit 0"}]}]
  }
}
```

From then on, in any session:

```sh
buddy claim api-refactor --desc "reworking the handler layer" --scope internal/api
buddy ls                          # the board: who holds what, STALE flags
buddy release api-refactor
```

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
buddy msg all "CI is red, check before pushing"   # delivered after each session's next tool call
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
```

Point any IRC client at the server ([Halloy] is the maintained XChat-shaped
one) and watch the fleet. Register the MCP server so agents get
`chat_send` / `chat_read` / `chat_status` / `chat_ack` / `chat_who` / `dm` /
`set_status`:

```sh
claude mcp add-json buddylist "{\"type\":\"stdio\",\"command\":\"$HOME/bin/buddylist\",\"args\":[\"mcp\"]}" --scope local
```

For the full 2002 experience instead: run [open-oscar-server] and
`buddylist serve --backend toc`, then sign into real AIM (on macOS,
[im-for-macos] runs AIM 5.1 natively). The daemon speaks the era's wire
charset (CP1252), flattens AIM's HTML message envelopes, and joins the
exchange the AIM client's own Buddy Chat dialog uses. You will hear the door.

## How it holds together

```
agents (Claude Code sessions)                       operator
  │ hooks: hello/gate/beat/bye     ┌─ IRC client (Halloy) / AIM 5.1
  ▼                                ▼
buddy CLI ──► <repo>/.git/buddy.db      ergo / open-oscar-server
  claims · controls · inbox                   ▲
  (SQLite, transactional)                     │ ircwire / tocwire (Conn seam)
  │                                     buddylistd ──► ~/.buddylist/journal.db
  └─ MCP tools ─────────────────────────► unix socket   (durable room history)
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
[docs/DESIGN.md](docs/DESIGN.md). Roadmap-ish: per-session buddy presence
with away-message statuses, a commit-time claims gate, and multi-machine
coordination.

## License

Apache-2.0.

[ergo]: https://github.com/ergochat/ergo
[open-oscar-server]: https://github.com/mk6i/open-oscar-server
[Claude Code]: https://claude.com/claude-code
[Halloy]: https://github.com/squidowl/halloy
[im-for-macos]: https://github.com/mk6i/im-for-macos
