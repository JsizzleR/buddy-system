<p align="center">
  <img src="docs/assets/wordmark.svg" width="420" alt="buddy system">
</p>

<p align="center"><b>Several coding agents, one checkout, one ledger of who holds which files: claim a scope, and the gate holds that exclusive claim against every other session's edit, naming the holder.</b></p>

<p align="center">
  <a href="https://github.com/JsizzleR/buddy-system/actions/workflows/check.yml"><img src="https://github.com/JsizzleR/buddy-system/actions/workflows/check.yml/badge.svg" alt="check"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="License: Apache-2.0"></a>
  <a href="go.mod"><img src="https://img.shields.io/badge/go-1.26-00ADD8.svg" alt="Go 1.26"></a>
</p>

## What a session does with it

The same ledger, from either harness: the picture below is a Claude Code session
and a Codex session sharing one checkout, each given a short task brief with a
prescribed coordination scenario (append a line to one file; hold it until asked,
hand it over) and pointed at the skill. Which buddy commands to run was each
session's own choice; what buddy refuses comes from the ledger.

![A Claude Code session on the left and a Codex session on the right, sharing one checkout, each given a short task brief with a prescribed coordination scenario and pointed at the buddy skill, the buddy commands their own choice. The Claude session loads the skill with /buddy, claims internal/store/store.go, edits it with the Edit tool and polls its mail. The Codex session reads the repo's agent rules and the skill, runs buddy claim and is told it has not said hello, registers with buddy hello, claims again and is REFUSED by the ledger naming lane-claude, messages the holder, declares a wait and blocks on it. The Claude session hands its claim over with buddy release --to and says DONE. The Codex session's wait lands as handed to it, its inbox carries the hand-over, its patch of the file is allowed, it releases and says DONE](docs/assets/demo.svg)

> **Captured panes, drawn by hand.** Two sessions in a scratch clone,
> 2026-10-26: Claude Code and Codex, each given a task brief with a prescribed
> scenario and pointed at the skill. Titles, `#` and `⏩` lines are typeset,
> long lines wrapped or trimmed, scratch paths shortened to `…`; otherwise no
> word changes. The refusal shown is the ledger's: both claimed before
> editing, so the gate never had an edit to refuse. More:
> [docs/PICTURES.md](docs/PICTURES.md).

- **Claims that refuse.** `buddy claim <slug> --scope <path>` reserves files or
  directory prefixes, whole or not at all. A `PreToolUse` hook denies any other
  session's `Edit`, `Write` or patch inside an exclusive claim and names the
  holder (shared claims overlap each other and nothing else); a refused claim
  names the whole conflict set and how to wait for it.
- **Messages between sessions.** `buddy msg <label|slug|id> "…"` queues a note
  delivered on the recipient's next tool call, with an id so a later message
  can correct it; `buddy sent` says what became of it, never "read".
- **A roster for the operator.** `buddy sessions` is the roster: each session
  live, idle or paused with its claims and context size (dead rows fold into
  one count line; `--all` lists them); `buddy who` is everything the ledger
  holds about one of them; `buddy pause` is the brake.

## A session runs a fleet

One Claude Code session coordinating two others, in a run of the fleet
recipe now in the [buddy-fleet skill](skills/buddy-fleet/SKILL.md). The
`SessionStart` hook registers it and prints what the ledger holds; `/buddy`
loads the skill (the recipe was then part of it); it opens the two lanes in new terminal tabs with the
harness (`herdr tab create`, `herdr agent start`), reads the roster and the
claims from the ledger while they work, waits on each lane with the harness
(`herdr agent wait`: lane-a came back `done`; lane-b came back `blocked`, the
harness's word for the state it read, in the same second lane-b's own
transcript ends with DONE), asks the ledger what closing each would leave
(`buddy who`: no claims held, either lane), and closes the panes it opened.
Buddy launches no process and kills none: the harness opens and closes the
panes, and `buddy bye` ends the ledger row of a session whose process is
already gone.

![One Claude Code session, the coordinator. Its SessionStart digest from buddy hello; the /buddy prompt that loads the skill and points at its brief; buddy sessions; herdr tab create and herdr agent start for lane-a and for lane-b, each start marked with the time herdr's startup wait took; the roster and the claims list while the lanes work; the two herdr agent wait calls, each marked with the real time it took, and their results: lane-a done, lane-b blocked; the claims list and git diff afterwards; buddy who and herdr pane close for each lane, buddy bye for each, buddy sweep, and the final roster with both lanes ended](docs/assets/fleet.svg)

> **Captured from a real run, drawn by hand.** Every output line is from the
> coordinator's transcript: a Claude Code session running the fleet recipe
> with two lanes, scratch clone, 2026-10-26. The prompt, `⏺`, `#` and `⏩`
> lines are typeset, long lines wrapped or trimmed, `# …` marks cuts, scratch
> paths shortened to `…`; otherwise no word changes. `herdr agent wait` said
> `blocked` for lane-b in the second lane-b's own transcript says DONE. More:
> [docs/PICTURES.md](docs/PICTURES.md).

## How it holds together

![Architecture. Left, the claims half: three lanes and an orchestrator that opened them with the harness; their hooks drive one buddy CLI whose gate denies an edit inside another session's claim; the CLI reads and writes a SQLite ledger in the repository's git directory holding claims, the inbox, the operator's pause and the session roster; the operator's terminal and messages between sessions go through the same ledger; the commit gate checks staged paths. Right, dashed, the chat half: an IRC client, ergo on loopback, the buddylistd daemon with its journal and MCP tools, and presence; optional, the view and never the lock](docs/assets/architecture.svg)

> **A drawing, nothing captured** (hand-authored 2026-10-25). Two rules it is
> built on: *chat is the view, never the lock* (control lives in the local
> ledger; a chat message is never authoritative) and *claims work with chat
> entirely absent* (safety hooks fail closed, chat hooks fail silent). The
> safety half is a CLI per hook call over a SQLite file, not a daemon. Why:
> [docs/DESIGN.md](docs/DESIGN.md); how: [docs/PICTURES.md](docs/PICTURES.md).

## Install in two minutes

Requires Go 1.26+ and `git`. **Platforms.** macOS is where it is developed
and used. On Linux the hermetic tier is green in CI (the live tier does not
run there) with process identity degraded, because no harness process is
found for a session: the roster never marks a session's process `GONE`,
`buddy msg` prints no wake address, and `buddy who`'s JOBS line says "cannot
say"; nobody uses it there day to day. Windows is not built or tried. The
whole thing, from an empty directory to the first claim in another project:

![Two panes of one terminal. Left: git clone, a claims-only run of the install script reporting what it built and that no hooks are wired, the hook-wiring block saved to the settings file, and the re-run reporting all six wired. Right: buddy init in another project, the digest a new session there gets at SessionStart, its first claim, and buddy status showing the claim held](docs/assets/install.svg)

> **Captured output, drawn by hand.** A claims-only install against a
> throwaway HOME, 2026-10-25, the install script's own staging knobs pointing
> every write there. Typed commands and `#` lines are typeset, long lines
> wrapped or trimmed, the HOME's path written `~`, `# …` marks cuts; otherwise
> no word changes. No session ran: the `SessionStart` hook was fed Claude
> Code's payload by hand. More: [docs/PICTURES.md](docs/PICTURES.md).

### 1. Clone and install

```sh
git clone https://github.com/JsizzleR/buddy-system && cd buddy-system
sh scripts/install.sh                   # builds buddy + buddylist into ~/bin, installs the skill, REPORTS hook wiring
# BUDDY_CHAT=off sh scripts/install.sh  # claims only: no daemon, no MCP server, nothing listening
cd /path/to/your/repo && buddy init     # one ledger per repo, inside .git; every worktree shares it
```

**Re-run `install.sh` after every pull** (or `buddy update`, from anywhere:
it runs `install.sh` in the checkout the installed `buddy` was built from,
after saying where that checkout stands against main and origin/main as last
fetched, and which git commands you may want first; it never pulls, D-110).
It is also the upgrade, and it
rebuilds every copy it knows about (`~/bin`, the chat daemon's program, this
checkout's `bin/`): a `buddy` older than the ledger's schema refuses to open
it, and the gate then denies every write. It also installs the
[skill](skills/buddy/SKILL.md) to `~/.claude/skills/buddy/`, which teaches a
session how the verbs fit together, and the
[fleet skill](skills/buddy-fleet/SKILL.md) beside it, which a session
coordinating others loads (an example of the operator's own fleet
conventions it reads: [docs/examples/fleet-conventions.md](docs/examples/fleet-conventions.md)),
and the [lead skill](skills/buddy-lead/SKILL.md), which a first-time user (or
the agent helping one) loads with `/buddy-lead`: it asks what you are trying
to do before anything is installed, and its `sh scripts/env-check.sh` is a
read-only look at the machine for settings that help many parallel sessions.
Put `~/bin` on your `PATH`
(`BUDDY_BIN_DIR` overrides it; the hook lines spell the path out either way).

#### Uninstall

```sh
buddy uninstall --dry-run    # the plan: what goes, what stays and why; removes nothing
buddy uninstall --chat       # chat installed by accident: remove the chat half, keep claims, and stay claims-only
buddy uninstall              # everything install.sh put in place, buddy last
```

It runs `scripts/uninstall.sh` in the checkout `buddy` was installed from (or
run that script from any clone). The chat daemon is booted out through launchd
and its plist moved to `~/.buddylist`; settings, MCP registrations, ledgers
and the chat journal are never touched, and it names the hook lines and MCP
entries left for you to remove ([docs/USAGE.md](docs/USAGE.md#buddy-uninstall--remove-it-or-only-the-chat-half-d-125)).

#### Claims-only: no chat stack

```sh
BUDDY_CHAT=off sh scripts/install.sh
```

builds and verifies `buddy` alone, skips every chat path, and reports hook
wiring against the six `buddy` lines; a `buddylist`, daemon plist or MCP
registration already on the machine is reported and left where it was. The
`buddy` it builds records the choice, so every later `sh scripts/install.sh`
or `buddy update` stays claims-only with `BUDDY_CHAT` unset, and says so on
its first line (`chat: off (kept: …)`; `buddy update --dry-run` says it too).
`BUDDY_CHAT=on` on one run turns chat back on, and is kept the same way
(D-124); any other value is refused. Then declare it to every session, so
`hello` prints no chat hint:

```json
{ "env": { "BUDDY_CHAT": "off" } }
```

in `~/.claude/settings.json` (that line is `hello`'s alone: the install never
reads it, and says when it disagrees with the recorded choice). Wire the six `buddy` lines from
[step 3](#3-hook-wiring), leave out the two `buddylist` lines, and do not
register the buddylist MCP server. What you lose is the visibility half; the
ledger's own views (`buddy sessions`, `who`, `ls`, `msg`, `inbox`, `sent`)
remain. A durable channel *to the operator* does not exist in this mode: a
lane reports to its orchestrator, or in its own pane.

### 2. Turn it on in a repo


`install.sh` wires the buddy-system checkout itself. In **each repo whose
sessions you want to coordinate**:

```sh
cd /path/to/your/repo
buddy init
```

That creates `buddy.db` in the repo's git **common** directory, shared by
every worktree and unseen by git. A repo without one is untouched: every hook
is a silent no-op there while `buddy` runs (an installed one that cannot run
refuses, below). `init` also says whether each of the repo's main
languages has a language-server plugin configured, with the command that
fixes it ([details](docs/USAGE.md#language-servers--what-buddy-init-reports));
re-running it is safe.

### 3. Hook wiring

Add these to `~/.claude/settings.json` (every repo; the ones never `buddy
init`-ed stay off while `buddy` runs) or to one repo's `.claude/settings.local.json`. The
install script's report reads only the user-level file. Every line is guarded,
so a missing binary turns the feature off rather than failing a tool call. The
gate is the one line that refuses: a `buddy` that is installed but crashes, is
killed or cannot run refuses the call (exit 2) rather than letting it through.
For a gate that times out or cannot start, Claude Code's hooks reference says
`"onFailure": "block"` (2.1.295 or later) does the same: documented, not
measured here, and what an older build makes of the key is not measured
either. Sessions started after this pick it up:

```json
{
  "hooks": {
    "SessionStart": [{"hooks": [{"type": "command",
      "command": "[ -x \"$HOME/bin/buddy\" ] && \"$HOME/bin/buddy\" hello 2>/dev/null; exit 0"}]}],
    "PreToolUse": [{"matcher": "Edit|Write|NotebookEdit|Bash", "hooks": [{"type": "command",
      "command": "[ -x \"$HOME/bin/buddy\" ] || exit 0; { \"$HOME/bin/buddy\" gate; } 2>/dev/null || { echo \"buddy gate failed (status $?); refusing the tool call (the gate fails closed). Operator, from a terminal: sh scripts/install.sh in the buddy-system checkout\" >&2; exit 2; }",
      "timeout": 10, "onFailure": "block"}]}],
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

The gate's deny reasons travel on stdout as JSON, so its `2>/dev/null` hides
nothing a verdict says; what it discards is a crash's own text, which would
otherwise be the block reason the model reads, unfenced. To see that text, run
the gate by hand from a terminal, as `install.sh` probes it:
`echo '{"session_id":"s","tool_name":"Write","tool_input":{"file_path":"f"}}' | ~/bin/buddy gate`.
While the installed
binary cannot run, every matching call refuses in every session on the
machine, in repositories never `buddy init`-ed and outside any repository too:
a `buddy` that cannot run cannot tell them apart, and Bash is refused with the
rest. Reinstall from a terminal; if the checkout cannot build, `chmod -x
~/bin/buddy` turns the feature off (the `[ -x ]` guard) until it can. That
switch is named here and not in the refusal, which the model reads.
`scripts/install.sh` reports a gate still wired the old way (D-107).
Why each hook behaves as it does is in [docs/USAGE.md](docs/USAGE.md#hooks-in-detail).

### 4. The commit gate (optional)


The tool-call gate sees only the path a tool declares; a file written by a
code generator or a shell redirect reaches the index unasked. The commit gate
reports, at `git commit`, any staged path inside **another** session's open
claim, warning by default. Git will not let a repository enable its own
hooks, so link it in your repo:

```sh
hooks_dir="$(git rev-parse --path-format=absolute --git-path hooks)"
mkdir -p "$hooks_dir"
ln -s /path/to/buddy-system/.githooks/pre-commit "$hooks_dir/pre-commit"
```

`--git-path hooks` honors `core.hooksPath`; a relative one is resolved per
worktree, so link it in each. If a `pre-commit` is already there, add
`"$HOME/bin/buddy" commit-gate || exit $?` to it. `BUDDY_COMMIT_GATE=deny`
refuses instead of warning; `=off` or `BUDDY_COMMIT_GATE_SKIP=1` silences it.
More in [docs/USAGE.md](docs/USAGE.md#the-commit-gate-in-detail).

### Or: let your agent install it

Paste this into a Claude Code (or any capable agent) session on the machine you
want it on. The page it points at is written for an agent, with a checkpoint
after every step, and it says what the agent must not do without asking:

> Install the Buddy System from https://github.com/JsizzleR/buddy-system for me.
> Follow https://raw.githubusercontent.com/JsizzleR/buddy-system/main/docs/AGENT-SETUP.md
> step by step, stopping at each checkpoint. Ask me before editing
> `~/.claude/settings.json` (show me the merge, not an overwrite), before
> installing anything, and before changing any permission mode. Claims only,
> no chat server, unless I say otherwise. Then turn it on in this repository.

The recipe itself is [docs/AGENT-SETUP.md](docs/AGENT-SETUP.md).

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
buddy wait --until 2h --note "own tier run"                   # parked on your own run or the orchestrator: a timer
```

A **scope** is a file or directory prefix; there are no globs. A claim is
granted whole or refused whole, and a refusal names every collision. `--shared`
lets several sessions co-hold a file they all append to; each still needs its
own claim, and it does not serialize their writes.

For the operator, from any terminal in the repo:

```sh
buddy sessions                     # the roster: live/idle/paused, claims, context size, cache (--all: dead rows too)
buddy who <session|label|slug>     # everything the ledger holds about one session
buddy pause <session> --note "hold off — deciding the design first"
buddy resume <session>
buddy msg all "CI is red, check before pushing"
buddy sweep --dry-run              # what a sweep would free; never reaps a live session
```

`buddy --help` lists every verb, and `buddy <verb> --help` gives its usage.
**[docs/USAGE.md](docs/USAGE.md)** covers each feature in depth: the roster's
columns, messages and corrections, waits that keep a parked session's prompt
cache warm, resource slots, batching several sessions into one long test run,
landing every lane's work through one orchestrator, authority files, an id
register, and the commit gate.

## Presence (the fun half)

Run an [ergo] IRC server, a single Go binary, bound to loopback (the servers
here run unauthenticated *because* they bind loopback; off loopback, turn auth
on first), then `buddylist serve` ([docs/USAGE.md](docs/USAGE.md#chat-internals)):

```sh
ergo defaultconfig > ergo.yaml
# edit ergo.yaml: under server.listeners keep ONLY "127.0.0.1:6667" (and
# remove the TLS listener unless you provision certs), then:
ergo run --conf ergo.yaml
```

Point any IRC client at the server ([Halloy] is a maintained, XChat-shaped
one). Each live session joins its project's room under its own name, wears
its claim as an away message, goes idle after 5 minutes and leaves after 30:
the nick list *is* the fleet, and `/whois` answers "what is that one working
on?". Register the compact MCP profile in each repo that uses buddy
(`chat_send` and `chat_read` only; `--profile full` adds the operator's
tools), and skip it in a [claims-only install](#claims-only-no-chat-stack):

```sh
claude mcp add-json buddylist "{\"type\":\"stdio\",\"command\":\"$HOME/bin/buddylist\",\"args\":[\"mcp\",\"--profile\",\"core\"]}" --scope local
```

For the full 2002 experience, run [open-oscar-server] and
`buddylist serve --backend toc`, then sign into real AIM ([im-for-macos] runs
AIM 5.1 natively on macOS). You will hear the door.

## Posture

**Cost.** Buddy makes no model calls; its cost is the text it puts into an
agent's context, so alerts carry counts and sequence numbers, never bodies,
room digests are never injected, `chat_read` and `chat_send` are budgeted,
and the SessionStart digest stays under the harness's hook-output limit
(`scripts/cost-report.sh` prints a seven-day baseline, counts only).
**Security.** Nothing listens off loopback by default: the daemon serves a
unix socket, the chat servers bind loopback, and a claims-only install listens
nowhere at all. The ledger trusts the machine's user
account: a single-operator tool, not a tenancy boundary. Enforcement is
cooperative: the gate adjudicates the paths a tool call *declares*, and
nothing binds a process that bypasses the harness. A seatbelt for agents, not
a sandbox against them. See [SECURITY.md](SECURITY.md) to report a
vulnerability. **Status.** A hermetic suite (with `-race`) on every push, plus
a live end-to-end tier against the real pinned chat servers
(`sh scripts/check.sh`). It is used to coordinate the agent fleet that builds
it. Codex sessions take part through the patch gate and native identity, in
bounded steps ([guide](docs/USAGE.md#codex-participation)). Not yet built: a
chat-command bridge (waits on real auth) and multi-machine claims.

## Documentation


| Document | What it is |
| --- | --- |
| [docs/USAGE.md](docs/USAGE.md) | Every feature in depth: hooks, claims, messages, the roster, waits, slots, long runs, landing through an orchestrator, authority files, ids, the commit gate |
| [docs/DESIGN.md](docs/DESIGN.md) | Why it is built this way, and the assumptions measurement refuted |
| [docs/decisions.md](docs/decisions.md) | The append-only decision record (D-001 onward) behind every rule |
| [docs/DEMO-RUN.md](docs/DEMO-RUN.md) | What the ledger did during a run of the hero's choreography with two real sessions: real commands, real output, one beat each |
| [docs/README.md](docs/README.md) | Index of everything else in `docs/`, including plans and field notes |

## License

[Apache-2.0](LICENSE).

[ergo]: https://github.com/ergochat/ergo
[open-oscar-server]: https://github.com/mk6i/open-oscar-server
[Halloy]: https://github.com/squidowl/halloy
[im-for-macos]: https://github.com/mk6i/im-for-macos
