# The Buddy System

[![check](https://github.com/JsizzleR/buddy-system/actions/workflows/check.yml/badge.svg)](https://github.com/JsizzleR/buddy-system/actions/workflows/check.yml)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Go 1.26](https://img.shields.io/badge/go-1.26-00ADD8.svg)](go.mod)

**A claims ledger and retro chat presence for fleets of AI coding agents.**

You run several [Claude Code] sessions in parallel on one machine. Two of them
edit the same file. One of them needs to be told to stop. You have no idea what
any of them are doing without tailing four terminals. The buddy system is the
old safety practice — pair up, so nobody wanders off alone and gets hurt —
applied to agent fleets:

- **`buddy`** — an atomic, per-repo **claims ledger**. A session claims a scope
  of the repo before working it, and a gate at the tool-call boundary denies
  another session's edit inside that claim, naming the holder. It also carries
  the operator's brake (`pause`) and a message inbox. SQLite, transactional,
  conservative about reclaiming. No daemon, no network.
- **`buddylist`** — the **presence layer**, optional. A concierge daemon keeps
  your whole fleet visible in a chat room: a modern IRC server ([ergo]) by
  default, or the real **AIM 5.1 client** against an AIM-compatible server
  ([open-oscar-server]) when nostalgia calls. It keeps a durable journal (the
  scrollback those protocols never had) and exposes MCP tools so agents can read
  the room and talk back.

## What it looks like

Session A claims a directory:

```console
$ buddy claim api-refactor --desc "reworking the handler layer" --scope internal/api
claimed "api-refactor" for myapp/s-aaaaaaaa — scopes: internal/api
```

Session B then tries to edit a file inside it. Its `Edit` never runs; the
`PreToolUse` hook denies it, and the agent is told why:

```text
internal/api/handler.go is inside scope "internal/api" claimed by session
myapp/s-aaaaaaaa (slug "api-refactor": reworking the handler layer). Coordinate
or claim different scopes. …
```

If B tries to claim the file instead, the claim is refused whole, and B is told
how to wait for it:

```console
$ buddy claim fix-handler --desc "fix a bug" --scope internal/api/handler.go
REFUSED: internal/api/handler.go  (overlaps "internal/api" held by myapp/s-aaaaaaaa, claim "api-refactor")
to be told when it frees: buddy wait --on api-refactor
```

`buddy sessions` is the roster, `buddy ls` the board of open claims, and
`buddy msg <session> "…"` puts a note in a session's inbox, delivered on its
next tool call.

## The two rules it stands on

1. **Chat is the view, never the lock.** Coordination and control live in the
   transactional local ledger; a chat message is never authoritative.
   *Announced is not locked.*
2. **Claims work with chat entirely absent.** The chat stack can be down, or
   not installed, without weakening safety. Safety hooks fail closed (in a repo
   that has a ledger, one that cannot be read denies); chat hooks fail silent.

It is built for Claude Code hooks and MCP today. The seams (a hook JSON
contract, a unix-socket daemon API, a `Conn` transport interface) are
deliberately narrow so other harnesses and chat backends can slot in.

## Quick start

Requires Go 1.26+ and `git`. Developed and used on macOS. The hermetic test
suite passes on Linux in CI, but nobody uses it there day to day; the chat
daemon is a plain process, so supervise it with whatever your platform uses.

### 1. Install

```sh
git clone https://github.com/JsizzleR/buddy-system && cd buddy-system
sh scripts/install.sh
```

This builds `buddy` and `buddylist` into `~/bin` (override with
`BUDDY_BIN_DIR`), runs each once to prove it starts, installs the
[skill](#the-skill), restarts the chat daemon if a launchd agent for it is
loaded, and reports which hooks are wired in `~/.claude/settings.json`. It
never edits that file.

**Re-run it after every pull.** It is also the upgrade, and it rebuilds every
copy it knows about: `~/bin`, the program the chat daemon's launchd agent
names, and this checkout's `bin/`. A copy nobody remembered matters: a `buddy`
older than the ledger's schema refuses to open it, and the gate then denies
every write. A copy you put anywhere else, you upgrade yourself.

Put `~/bin` on your `PATH` (`export PATH="$HOME/bin:$PATH"` in `~/.zprofile`).
The hook lines below spell the path out, so they work either way; if you set
`BUDDY_BIN_DIR`, replace `$HOME/bin` in them too.

### 2. Turn it on in a repo

`install.sh` also wires the buddy-system checkout itself. Now, in **each repo
whose sessions you want to coordinate**:

```sh
cd /path/to/your/repo
buddy init
```

That creates `buddy.db` in the repo's git **common** directory
(`$(git rev-parse --git-common-dir)/buddy.db`). Every worktree of the checkout
shares it, and git never sees it. A repo without one is untouched: every hook
is a silent no-op there.

### 3. Hook wiring

Add these to `~/.claude/settings.json` (every repo; the ones never `buddy
init`-ed stay off) or to one repo's `.claude/settings.local.json`. The
install script's report reads only the user-level file. Every line is guarded,
so a missing binary turns the feature off rather than failing a tool call.
Sessions started after this pick it up:

```json
{
  "hooks": {
    "SessionStart": [{"hooks": [{"type": "command",
      "command": "[ -x \"$HOME/bin/buddy\" ] && \"$HOME/bin/buddy\" hello 2>/dev/null; exit 0"}]}],
    "PreToolUse": [{"matcher": "Edit|Write|NotebookEdit|Bash", "hooks": [{"type": "command",
      "command": "[ -x \"$HOME/bin/buddy\" ] && \"$HOME/bin/buddy\" gate; exit 0"}]}],
    "PostToolUse": [{"matcher": "*", "hooks": [
      {"type": "command",
       "command": "[ -x \"$HOME/bin/buddy\" ] && \"$HOME/bin/buddy\" beat 2>/dev/null; exit 0"},
      {"type": "command",
       "command": "[ -x \"$HOME/bin/buddylist\" ] && \"$HOME/bin/buddylist\" alert 2>/dev/null; exit 0"}]}],
    "Stop": [{"hooks": [{"type": "command",
      "command": "[ -x \"$HOME/bin/buddy\" ] && \"$HOME/bin/buddy\" idle 2>/dev/null; exit 0"}]}],
    "UserPromptSubmit": [{"hooks": [{"type": "command",
      "command": "[ -x \"$HOME/bin/buddy\" ] && \"$HOME/bin/buddy\" busy 2>/dev/null; exit 0"}]}],
    "SessionEnd": [{"hooks": [
      {"type": "command",
       "command": "[ -x \"$HOME/bin/buddy\" ] && \"$HOME/bin/buddy\" bye 2>/dev/null; exit 0"},
      {"type": "command",
       "command": "[ -x \"$HOME/bin/buddylist\" ] && \"$HOME/bin/buddylist\" presence --gone 2>/dev/null; exit 0"}]}]
  }
}
```

| Hook | Line | What it does | Needed? |
| --- | --- | --- | --- |
| `SessionStart` | `buddy hello` | registers the session; prints who holds what and any queued mail | yes |
| `PreToolUse` | `buddy gate` | denies an edit inside another session's claim, or while paused | yes — this is the safety |
| `PostToolUse` | `buddy beat` | heartbeat; delivers the inbox | yes |
| `SessionEnd` | `buddy bye` | ends the session once no registered harness process for it is alive, so its claims can be freed | yes |
| `Stop` | `buddy idle` | marks the session idle at its prompt, for the roster | optional |
| `UserPromptSubmit` | `buddy busy` | clears idle, and carries queued mail into the prompt | optional |
| `PostToolUse` | `buddylist alert` | tells a session a chat message named it (counts only, no text) | chat only |
| `SessionEnd` | `buddylist presence --gone` | takes the session's buddy out of the chat room at once | chat only |

The gate line deliberately has no `2>/dev/null`: when it denies, it says why.
Why each hook behaves as it does is in [docs/USAGE.md](docs/USAGE.md#hooks-in-detail).

### 4. The commit gate (optional)

The tool-call gate only sees the path a tool declares. A file written by a code
generator, a shell redirect or a formatter reaches the index without being
asked about. The commit gate catches that at `git commit`: it reports any
staged path inside **another** session's open claim. It warns and lets the
commit through by default.

Git will not let a repository enable its own hooks, so this is always a local
step. In your repo, link the hook shipped here:

```sh
hooks_dir="$(git rev-parse --path-format=absolute --git-path hooks)"
mkdir -p "$hooks_dir"
ln -s /path/to/buddy-system/.githooks/pre-commit "$hooks_dir/pre-commit"
```

`--git-path hooks` honors `core.hooksPath`. The default hooks directory is
shared by every linked worktree; a relative `core.hooksPath` is resolved per
worktree, so link it in each. `ln` refuses if a `pre-commit` is already there;
in that case add `"$HOME/bin/buddy" commit-gate || exit $?` to your existing
hook, so its verdict is not overwritten by a later command. A symlink keeps it
current when you pull buddy-system. `BUDDY_COMMIT_GATE=deny` refuses
instead of warning; `=off` or `BUDDY_COMMIT_GATE_SKIP=1` silences it.

(`sh scripts/setup-clone.sh`, which `install.sh` runs, wires the buddy-system
checkout itself, not your repo.)

### The skill

`install.sh` also copies [`skills/buddy/SKILL.md`](skills/buddy/SKILL.md) to
`~/.claude/skills/buddy/`. It teaches a session how the verbs fit together:
claiming, what to do when refused, waiting on a peer, messaging, correcting a
message. The SessionStart digest points every session at `buddy --help`. Two
tests keep the skill and `--help` in step in both directions, so a renamed flag
fails the test suite. Re-running the install is safe: an unchanged copy is left
alone, and a different one is kept as `SKILL.md.bak` before it is replaced.

## Everyday use

In a session (the agent usually runs these itself, prompted by the skill and
the digest):

```sh
buddy claim api-refactor --desc "reworking the handler layer" --scope internal/api --scope cmd
buddy claim api-refactor --dry-run --desc "…" --scope docs   # forecast a refusal; writes nothing
buddy release api-refactor                                    # or --scope docs to hand back part
buddy ls                                                      # every open claim, STALE flagged
buddy whose internal/api/handler.go                           # who claimed it, who has it dirty
buddy wait --on api-refactor --until 3h                       # declare a wait on a peer's claim
```

A **scope** is a file or directory prefix; there are no globs. A claim is
granted whole or refused whole, and a refusal names every collision. `--shared`
lets several sessions co-hold a file they all append to; each still needs its
own claim, and it does not serialize their writes.

For the operator, from any terminal in the repo:

```sh
buddy sessions                     # the roster: live/idle/paused, claims, context size, cache
buddy who <session|label|slug>     # everything the ledger holds about one session
buddy pause <session> --note "hold off — deciding the design first"
buddy resume <session>
buddy msg all "CI is red, check before pushing"
buddy sweep --dry-run              # what a sweep would free; never reaps a live session
```

A paused session's next mutating tool call is denied. `msg` never claims
delivery: it reports what the ledger knows about the recipient, such as when it
was last seen or that it is idle at its prompt.

`buddy --help` lists every verb, and `buddy <verb> --help` gives its usage.
**[docs/USAGE.md](docs/USAGE.md)** covers each feature in depth: the roster's
columns, messages and corrections, waits that keep a parked session's prompt
cache warm, resource slots, batching several sessions into one long test run,
authority files, an id register, and the commit gate.

## Presence (the fun half)

Run an [ergo] IRC server — a single Go binary. Make the loopback binding
explicit rather than trusting defaults:

```sh
ergo defaultconfig > ergo.yaml
# edit ergo.yaml: under server.listeners keep ONLY "127.0.0.1:6667" (and
# remove the TLS listener unless you provision certs), then:
ergo run --conf ergo.yaml
```

The servers here run unauthenticated *because* they bind loopback. If you ever
bind a real interface, turn on auth first (ergo has SASL). Then:

```sh
buddylist serve --rooms yourproject,ops       # the concierge daemon (buddylistd)
buddylist say yourproject "hello fleet"       # relay as [operator]
buddylist read yourproject --tail 20          # the journal: durable scrollback
buddylist who                                 # live room membership
buddylist dm alice "nightly is RED"           # exits non-zero if alice is not online
```

Point any IRC client at the server ([Halloy] is a maintained, XChat-shaped one)
and watch the fleet. Each live session joins its project's room under its own
name, wears its claim as an away message (`claim: api-refactor · active`), goes
idle after 5 minutes and leaves after 30. The nick list *is* the fleet, and
`/whois` answers "what is that one working on?". Session buddies never speak,
never journal and never touch the ledger: the claim slugs ride the alert hook,
which had already read them, so presence costs no extra hook line. `serve --presence=false` turns them
off. The AIM backend has no per-session buddies: a TOC screen name is an
account, and a second signon boots the first.

Register the compact MCP profile in each repo that uses buddy. It exposes only
`chat_send` and `chat_read`, so unrelated projects don't carry unused tool
schemas:

```sh
claude mcp add-json buddylist "{\"type\":\"stdio\",\"command\":\"$HOME/bin/buddylist\",\"args\":[\"mcp\",\"--profile\",\"core\"]}" --scope local
```

`buddylist mcp --profile full` adds the operator-oriented `chat_status`,
`chat_ack`, `chat_who`, `dm` and `set_status` tools.

For the full 2002 experience instead: run [open-oscar-server] and
`buddylist serve --backend toc`, then sign into real AIM (on macOS,
[im-for-macos] runs AIM 5.1 natively). The daemon speaks the era's wire charset
(CP1252), flattens AIM's HTML message envelopes, and joins the exchange the AIM
client's own Buddy Chat dialog uses. You will hear the door.

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

- One ledger per repo, in the git **common dir**: shared by every worktree,
  invisible to git.
- Sessions are identified by `(session_id, incarnation)`. The harness process
  id decides one thing only: whether a `bye` may end the session. Stale claims are flagged and still refuse; nothing that might be
  alive is reaped automatically.
- The chat journal records what the server saw, and reads page by sequence
  number, so catching up costs only what is new.
- A message that names a session (by its claim slug, label or id) raises an
  alert on that session's next tool call, carrying counts and sequence numbers
  and never the chat text.
- Everything an agent reads back from chat is fenced as untrusted, one line per
  message. Prompt injection through a chat room is assumed, not hoped away.

The reasoning behind each of these, including the assumptions that measurement
refuted, is in [docs/DESIGN.md](docs/DESIGN.md).

## Cost controls

Buddy makes no model calls. Its cost is the text it places into an agent's
context, so the defaults keep that text narrow:

- The proactive alert carries counts and sequence numbers, never chat bodies.
  Room digests are never injected; reading is deliberate. Silent hooks add no
  context.
- MCP `chat_read` defaults to 10 rows and a 4 KiB result; an explicit limit
  above 10 opts into a 16 KiB budget. `chat_send` has a 750-byte routine budget,
  so prefer a compact claim / outcome / blocker / next / reference update;
  `long=true` allows up to 4096 bytes for a deliberate handoff.
- `buddy msg all` goes to the sessions live at send time, and an undelivered
  broadcast expires after 24 hours. A target that resolves to nobody is refused
  rather than queued.
- The SessionStart digest stays under Claude Code's hook-output limit, claims
  first, and counts what did not fit.

For a seven-day baseline that reports counts and byte lengths only, never
message text, run `scripts/cost-report.sh` (`BUDDY_COST_DAYS` changes the
window).

## Security posture

Nothing here listens on the network: the daemon serves a unix socket, and the
chat servers are configured to bind loopback. They run unauthenticated *only*
because of that; if you take them off loopback, turn real auth on first. The claims
ledger trusts the machine's user account: this is a single-operator tool, not a
multi-tenant boundary. Enforcement is **cooperative**: the gate adjudicates the
paths `Edit`, `Write` and `NotebookEdit` declare, and fails closed when the
ledger is unreadable. A `Bash` command is checked only against a pause — what
it writes is caught, if at all, by the commit gate — and nothing binds a
process that bypasses the harness. A seatbelt for agents, not a
sandbox against them. See [SECURITY.md](SECURITY.md) to report a vulnerability.

## Documentation

| Document | What it is |
| --- | --- |
| [docs/USAGE.md](docs/USAGE.md) | Every feature in depth: hooks, claims, messages, the roster, waits, slots, long runs, authority files, ids, the commit gate |
| [docs/DESIGN.md](docs/DESIGN.md) | Why it is built this way, and the assumptions measurement refuted |
| [docs/decisions.md](docs/decisions.md) | The append-only decision record (D-001 onward) behind every rule |
| [docs/README.md](docs/README.md) | Index of everything else in `docs/`, including plans and field notes |

## Status

Working and tested: a hermetic suite (with `-race`) on every push, plus a live
end-to-end tier against the real pinned chat servers (`sh scripts/check.sh`).
It is used to coordinate the agent fleet that builds it. Not yet built: a
chat-command bridge (waits on real auth) and multi-machine claims.

## License

[Apache-2.0](LICENSE).

[ergo]: https://github.com/ergochat/ergo
[open-oscar-server]: https://github.com/mk6i/open-oscar-server
[Claude Code]: https://claude.com/claude-code
[Halloy]: https://github.com/squidowl/halloy
[im-for-macos]: https://github.com/mk6i/im-for-macos
