# Architecture

A map of the Buddy System: what runs, where its state lives, and how one tool
call becomes a verdict. It says where things are and what they do, and stops
there:

- **why** it is built this way, and the assumptions measurement refuted:
  [DESIGN.md](DESIGN.md);
- **how to use** each verb, hook and knob: [USAGE.md](USAGE.md), and
  `buddy <verb> --help`;
- **what it resists** and what it does not: [../SECURITY.md](../SECURITY.md);
- **the record** behind a rule cited here as `D-NNN`: that heading in
  [decisions.md](decisions.md).

The drawing in the [README](../README.md#how-it-holds-together)
(`assets/architecture.svg`) shows the two halves below. It does not draw the
adapter, a fleet ledger, or the ledgers above a nested repository.

## Two halves, and one opt-in exception

Coordination for Claude Code and Codex sessions working in parallel on one
operator's machine. The halves must never be confused (D-004, D-006).

**Claims, the safety half** (`cmd/buddy`). A ledger of who has reserved which
paths, the operator's brake (`pause`) and the message inbox. It is the only
thing that reserves or refuses anything. There is no daemon: a `buddy` process
is spawned per hook call and per typed verb, reads or writes the ledger, and
exits.

- `internal/cli`: every verb, the hook verbs (`hello`, `gate`, `beat`, `idle`,
  `busy`, `bye`), the commit gate (`commitgate.go`), and the one place that
  reads a Claude Code transcript (`transcript.go`: the last turn's token
  counts, never message text).
- `internal/store`: the SQLite ledger. Transactional, WAL, opened with
  `_txlock=immediate`.
- `internal/fence`: untrusted-content fencing. Every value a peer or a wire
  could have written (a claim description, a label, a path, a message body)
  is meant to reach a model's context only through `fence.Line`, on one line,
  or `fence.Field`, in one column; `scripts/check-fence.sh` holds the rule in
  the hermetic tier.

**Chat, the visibility half** (`cmd/buddylist`). A concierge daemon on a chat
server, a durable journal, an MCP server, an alert hook and per-session
presence. It never reserves and never refuses: announced is not locked.

- `internal/buddylist`: the daemon (`chatd.go`), the journal (`journal.go`),
  the unix socket its clients use (`socket.go`), the MCP tools (`mcp.go`), the
  alert hook (`alert.go`) and presence (`presence.go`).
- `internal/ircwire`, `internal/tocwire`: the two transports (IRC; TOC/AIM)
  behind the `Conn` interface in `chatd.go`. The daemon does not know what IRC
  is: protocol detail stays in the wire package.

The dependency runs one way. `buddylist` links the claims packages, so a
session's hook can read its own label and claim slugs for presence; `buddy`
links nothing of chat. A chat failure degrades to "no chat", never to "no
safety".

**The adapter, opt-in** (`cmd/buddy-adapter`, `internal/harness`). A separate
control binary for one worker it launches itself, over a native backend (the
Codex app-server, Claude's stream-json). It is the one recorded exception to
"buddy launches, wakes and ends nothing" (D-070, D-074), and `install.sh`
never installs it. It links no ledger code: it reaches the ledger by running
`buddy adapter ...` and reading versioned JSON. Its `hook` subcommand is the
PreToolUse wrapper a worker's gate configuration names. `buddy diagnose` links
`internal/harness` for its capability tables and launches nothing. See
[USAGE.md](USAGE.md#structured-adapters-experimental).

## Where things are

| Path | What it holds |
| --- | --- |
| `cmd/buddy`, `cmd/buddylist`, `cmd/buddy-adapter` | The three binaries' `main` packages |
| `internal/cli`, `internal/store`, `internal/fence` | The claims half (above) |
| `internal/buddylist`, `internal/ircwire`, `internal/tocwire` | The chat half |
| `internal/harness` | The adapter's backends, controller, receipt journal and admission records (`admissions/`; their documents are in `docs/admissions/`) |
| `internal/testguard` | Test infrastructure only (below) |
| `.githooks/pre-commit` | The commit-time claim gate: hands staged paths to `buddy commit-gate` |
| `.githooks/pre-push` | This repository's own push gate: the hermetic tier on each pushed tip, in a detached tree |
| `scripts/` | Install and uninstall (`install.sh`, `setup-clone.sh`, `uninstall.sh`), the suite (`check.sh` and its `check-*.sh` done-checks), reviews (`codex-review.sh`), reports (`*-report.sh`), the adapter's evidence checkers (`check-*.py`) and qualification runners (`qualify-*.py`) |
| `scripts/hooks/` | Two hooks a user installs by hand: the Agent-model gate and the issue-author gate |
| `skills/` | The skills shipped here and installed under `~/.claude/skills/`: `buddy` (every session), `buddy-fleet` (a coordinator), `buddy-lead` (a first-time user), `model-router` |
| `docs/` | This directory; [README.md](README.md) indexes it |

## Where state lives

| State | Where | Notes |
| --- | --- | --- |
| The ledger | `<git-common-dir>/buddy.db` | One per repository, in the git **common** directory, so every worktree of a checkout shares it and git never sees it. Created by `buddy init`. Claims and their scopes, sessions and their processes, the pause controls, the inbox, waits, dirty-path observations, authority files, the id register |
| A fleet ledger | `~/.buddy/fleets/<name>/buddy.db` | Coordination only, for work outside git (D-191). Created only by `buddy init --fleet <name>` and used when `BUDDY_FLEET` names it. The gate never reads it, so it holds slot claims, messages and waits, and no file claim or pause |
| The chat journal | `~/.buddylist/journal.db` | What the daemon's one connection saw, by sequence number. The daemon's socket is beside it |
| Installed binaries | `~/bin`, or `BUDDY_BIN_DIR` | README's hook lines name `$HOME/bin`. Each `buddy` records the checkout it was built from, which `buddy update` reads (D-110) |
| Hook wiring | the user's Claude Code or Codex settings | Not buddy's files. `install.sh` reports the user-level Claude Code one and edits it only under `--wire-hooks` (D-135) |
| Adapter receipts | paths the operator names at launch | An immutable lifecycle receipt and a transport journal per worker |

The ledger carries a schema version. A binary refuses a ledger newer than
itself (D-037), and an existing ledger is migrated only by the installed
`$HOME/bin/buddy`, or under `BUDDY_MIGRATE=1`, after a verified backup beside
it (D-136). Nothing migrates down. See
[USAGE.md](USAGE.md#upgrading-and-the-ledgers-schema).

A second kind of machine-wide state arrives with the memory slot (`buddy mem`,
D-238): an admission slot for heavy starts, off until the operator runs
`buddy mem init`, kept in `~/.buddy/mem/` and not in any ledger. `buddy gate`
judges a shell call for it after the claims steps below have allowed the
call. It is described in USAGE.md under "Resource slots".

## One tool call, from hook to verdict

The harness runs hook lines the user wired
([README](../README.md#3-hook-wiring)). A session's life is five lifecycle
hooks around the one that decides:

| Hook | Verb | What it does |
| --- | --- | --- |
| `SessionStart` | `buddy hello` | Registers the session, as `(session_id, incarnation)`; prints who holds what and queued mail |
| `PreToolUse` | `buddy gate` | The verdict (below) |
| `PostToolUse` | `buddy beat` | Heartbeat; delivers the inbox; records the path the call named as a dirty-path observation |
| `UserPromptSubmit` | `buddy busy` | Clears idle; carries queued mail into the prompt |
| `Stop` | `buddy idle` | Marks the session idle at its prompt; hands over mail that arrived as the turn ended |
| `SessionEnd` | `buddy bye` | Ends the session once no registered harness process for it is alive |

A Codex session takes part through the gate and its native identity, without
these lifecycle hooks ([USAGE.md](USAGE.md#codex-participation)).

`buddy gate` (`cmdGate` in `internal/cli/cli.go`) reads the hook's JSON from
stdin and decides in this order (steps 5 and 6 interleave in the code). A
step that refuses ends the call.

1. **The input.** JSON it cannot read, or with no tool name, is refused. A
   read-only tool (`Read`, `Glob`, `Grep`, `LS`, `WebFetch`, `WebSearch`)
   passes here, whatever the ledger's state.
2. **The declared paths** (`patch.go`). Every path of an `apply_patch`, or the
   call's `file_path` or `notebook_path`. A Bash command declares none.
3. **The ledger.** Discovery asks git, from the filesystem and not from an
   inherited git environment (D-137), for the working directory's repository
   and its `buddy.db`. Provably no ledger here: the walk goes up, since a
   ledger in a repository above still governs (D-212). A ledger that exists
   and cannot be opened, or a place the gate cannot tell has one, is refused
   (D-231).
4. **Who is calling.** Native session ids that disagree are refused wherever
   a ledger governs (D-085).
5. **The brake.** A paused session is refused, in this ledger, in every
   governed ledger above it (`gateancestors.go`, D-226), and in the ledgers a
   literal `cd` or `git -C` in a shell command reaches, up to four of them
   (D-138).
6. **Shell routes.** A command parsed (never run) as routing `apply_patch`
   through a shell is refused (D-073, D-087); gh stack's rewriting subcommands
   need `.buddy/slot/gh-stack` in the ledger of the repository they run in
   (`ghstack.go`, D-108). A call with no path is allowed here.
7. **Each path, in every spelling a write could land on** (`gatetargets.go`:
   a symlink, `..`, a case alias). Each spelling is placed against the
   ledgers that govern it, the target repository's own included, and refused
   inside another session's claim: inside a shared one, until the caller
   holds a covering claim of its own; inside one parked in a run, unless the
   caller's own exclusive claim declares that run (D-133). Containment is
   exactly `scope == path || strings.HasPrefix(path, scope+"/")` on spellings
   folded by the one rule, `store.Fold`.
8. **The caller's own claim** (`ownclaim.go`, D-228). A path no open claim of
   the caller covers, while another session holds a claim: by default the
   call is allowed with one line added to the model's context under Claude
   Code, and with nothing under Codex; `BUDDY_OWN_CLAIM=deny` refuses it.
   D-228 names the callers and paths this step does not judge.

A refusal is one JSON document on stdout carrying `permissionDecision:
"deny"` and its reason, with exit 0. An allow prints nothing, except step 8's
line, which is one document carrying `additionalContext` and no decision. The
gate decides at the tool-call boundary and not after: the window between its
check and the write is real and documented.

**The second line** is at `git commit`: `.githooks/pre-commit` hands the
staged paths to `buddy commit-gate`, which reports any inside another
session's open claim, and any that no claim of the committer covers. It
warns by default; `BUDDY_COMMIT_GATE=deny` refuses the first kind and never
the second (D-012, D-228). It is the one check that sees what no
tool call declared, where the hook is installed and the commit takes the
ordinary path ([USAGE.md](USAGE.md#the-commit-gate-in-detail)).

## What fails closed, what fails silent, what is off

Three different outcomes, kept apart on purpose (D-005).

| Situation | Outcome |
| --- | --- |
| No `buddy` binary installed | **Off** under Claude Code: each of README's hook lines is guarded by `[ -x ]`. The Codex gate line in USAGE.md names an absolute path with no guard, so a binary missing from that path refuses |
| Not a repository, or one never `buddy init`ed, with no ledger above it | **Off**, silently, while `buddy` runs |
| A ledger exists but cannot be opened or read, is newer than the binary, or cannot be told from an absent one | **Closed.** The gate refuses, naming the cause and the fix it knows |
| An installed `buddy` that crashes, is killed or cannot run | **Closed**, for the gate: README's hook line turns the death into a refusal of every gated call, ledger or none (D-107). **Silent** for the lifecycle lines, which end in `exit 0` |
| The chat daemon down, the socket missing, the chat server gone | **Silent.** Every chat hook line ends in `exit 0`; a chat failure never costs a tool call |
| A fleet ledger missing or unreadable | **Said, never refused**: it guards nothing (D-191) |

Collapsing "no ledger" and "ledger unreadable" into one silent allow is how a
safety feature quietly stops existing; [DESIGN.md](DESIGN.md#what-the-gate-honestly-is)
has the argument.

Enforcement is cooperative, and saying so is the design. The gate adjudicates
the paths a tool call declares; it cannot bind a process that bypasses the
harness, and a shell command's other writes are invisible to it. A seatbelt
for agents, not a sandbox against them.

## The chat half

```
session --MCP stdio----> buddylist mcp ----+
                                           +--> unix socket --> buddylistd --Conn--> ergo (IRC), or a TOC server
session --PostToolUse--> buddylist alert --+                        |
                                                                journal.db
```

- **The daemon** (`buddylist serve`, under launchd) holds the concierge's
  connection to a chat server the operator runs on loopback, and journals
  what that one connection saw.
- **`chat_read` and `chat_send`** are MCP tools a session calls on purpose.
  The read is budgeted and fenced; chat content is hostile input.
- **The alert hook** tells a session that a message named it: the room,
  counts and sequence numbers, never the text.
- **Presence** gives each live session a nick in its project's room, with its
  claim slug as the away message. Those connections never journal, or N
  sessions would store every message N times. Presentation only: it ends no
  session and touches no claim.

No chat message is authoritative, and a message's sender confers nothing.
Everything binds loopback, which is the only reason the chat servers may run
unauthenticated ([USAGE.md](USAGE.md#chat-internals)).

## Tests and checks

- **`sh scripts/check.sh [all|hermetic|live]`** is the suite. `hermetic` is
  gofmt, source-shape gates, vet, the tests, `-race` over every package, the
  per-feature done-checks (`scripts/check-*.sh`) and the scripts' Python
  self-tests; a fresh clone and CI run it. `live` drives the real pinned chat
  server. `all`, the default, exits 2 when the live server binary is absent:
  "not run" is neither pass nor fail.
- **`internal/testguard`** bounds the test binaries. `Arm()`, first in a
  package's `TestMain`, ends a test binary whose `go test` died and one that
  wedged (D-053, D-220); `ScrubGitEnv()` unsets git's local environment in
  every package that runs git, so a test started from a hook cannot write to
  the real repository (D-057).
- **Fixtures** are a real temporary git repository each, never this
  checkout's ledger.
- **Reviews are not tests.** `scripts/codex-review.sh` launches a constrained
  second-model review with the settled facts in
  [review-charter.md](review-charter.md) prepended; its verdict is never test
  evidence. The rules a contributor follows, review tiers included, are in
  [../CLAUDE.md](../CLAUDE.md).
- **The adapter's qualification** is apart: `scripts/check-*.py` read saved
  evidence and launch nothing; `scripts/qualify-*.py` launch a native harness
  and run only on the operator's word.
