<p align="center">
  <img src="docs/assets/wordmark.svg" width="420" alt="buddy system">
</p>

<p align="center"><b>Several coding agents, one checkout, one ledger of who holds which files: claim a scope, and the gate holds that exclusive claim against every other session's edit, naming the holder.</b></p>

<p align="center">
  <a href="https://github.com/JsizzleR/buddy-system/actions/workflows/check.yml"><img src="https://github.com/JsizzleR/buddy-system/actions/workflows/check.yml/badge.svg" alt="check"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="License: Apache-2.0"></a>
  <a href="go.mod"><img src="https://img.shields.io/badge/go-1.26-00ADD8.svg" alt="Go 1.26"></a>
</p>

## A session runs a fleet

One Claude Code session coordinating two others, in a run of the
[skill](skills/buddy/SKILL.md)'s "Running a fleet" recipe. The
`SessionStart` hook registers it and prints what the ledger holds; `/buddy`
loads the skill; it opens the two lanes in new terminal tabs with the
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

> **Captured from a real run, drawn by hand.** Every output line is the
> coordinator session's own, read from its transcript: the `SessionStart`
> output of the `buddy hello` hook (no other hook printed), each tool call's
> result as the harness recorded it (so "Bash completed with no output" is
> the harness's wording, not buddy's), and the session's own words, shown as
> the pane renders them. Captured on 2026-10-26 in a scratch clone with its
> own ledger, under a throwaway home directory whose settings wire buddy's
> hooks and nothing else: one Claude Code session (Opus, auto mode) following
> a brief of 21 numbered steps, and two lanes (Sonnet, auto mode) following
> the briefs of the picture below. The typed prompt, the `⏺` tool-call lines
> (those calls, written as Claude Code renders them) and the `#` narration
> are typeset.
> `…` stands for the scratch directory holding the briefs and the clone;
> long lines are soft-wrapped at 112 columns or trimmed at a word with an
> ellipsis; a `# …` row marks lines cut from a result (the `git diff` keeps
> its first line and the two added lines; `buddy who` keeps its first line,
> `CLAIMS HELD` and `EXIT`); and each `⏩` line is the real time between the
> call above it and its result below, read from the transcript's timestamps,
> with shorter gaps unmarked. `herdr agent start` answers with a timeout:
> its 30-second wait to detect the agent ran out while the lane was already
> running. `herdr agent wait` answers with the state it read from the lane's
> pane: `done` for lane-a, `blocked` for lane-b, in the second lane-b's own
> transcript ends with DONE and after it had released its claim (the
> `buddy ls --all` and `buddy who lane-b` results that follow show that).
> Nothing else in a captured line is changed. The pane is drawn as a 30-row
> window that scrolls; no frame was recorded.

## What a session does with it

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

![Two Claude Code sessions side by side. Each claims a scope and edits inside it. One then edits a file in the other's scope and the buddy gate denies it, naming the holder. It messages the holder, the holder releases, its own claim is granted and the same edit is allowed](docs/assets/demo.svg)

> **Captured output, drawn by hand.** Every output line in the picture is
> `buddy`'s own, captured on 2026-10-25 in a scratch repository with its own
> ledger: `git init`, `buddy init`, two session ids. No session ran: the hooks
> were fed the JSON Claude Code sends them. The prompts, the `⏺ Edit(…)` tool
> lines (those calls, written as Claude Code renders them) and the `#`
> narration are typeset. The picture is a hand-authored animated SVG over that
> capture, not a recording: long lines are soft-wrapped at 58 columns and
> trimmed with an ellipsis, and the output is preserved except for that
> wrapping and trimming. A separate run of the same choreography with two
> real sessions, read from the ledger's side, is
> [docs/DEMO-RUN.md](docs/DEMO-RUN.md).

## How it holds together

![Architecture. Left, the claims half: three lanes and an orchestrator that opened them with the harness; their hooks drive one buddy CLI whose gate denies an edit inside another session's claim; the CLI reads and writes a SQLite ledger in the repository's git directory holding claims, the inbox, the operator's pause and the session roster; the operator's terminal and messages between sessions go through the same ledger; the commit gate checks staged paths. Right, dashed, the chat half: an IRC client, ergo on loopback, the buddylistd daemon with its journal and MCP tools, and presence; optional, the view and never the lock](docs/assets/architecture.svg)

> **Two rules the drawing is built on.** *Chat is the view, never the lock:*
> coordination and control live in the transactional local ledger, and a chat
> message is never authoritative. *Claims work with chat entirely absent:*
> safety hooks fail closed (a ledger that exists but cannot be read denies)
> and chat hooks fail silent. **Not a daemon:** the safety half is a CLI
> spawned per hook call over a SQLite file in the repo's git common directory;
> nothing listens, nothing has to be up, and a machine with chat absent loses
> visibility, never safety. The reasoning behind each box, including the
> assumptions measurement refuted, is in [docs/DESIGN.md](docs/DESIGN.md).

## Install in two minutes

Requires Go 1.26+ and `git`. Developed and used on macOS. The hermetic suite
passes on Linux in CI, where process identity is "cannot say", nothing is
ever marked `GONE`, and nobody uses it day to day. The whole thing, from an
empty directory to the first claim in another project:

![Two panes of one terminal. Left: git clone, a claims-only run of the install script reporting what it built and that no hooks are wired, the hook-wiring block saved to the settings file, and the re-run reporting all six wired. Right: buddy init in another project, the digest a new session there gets at SessionStart, its first claim, and buddy status showing the claim held](docs/assets/install.svg)

> **Captured the same way, against a throwaway HOME** (the install script's
> own staging knobs, listed in its header, point every write there; the clone
> came from a local mirror of `main` spelled as the GitHub URL, with a warm Go
> build cache, so the two minutes are the script's, not the download's). No
> session ran: the `SessionStart` hook was fed Claude Code's payload by hand.
> The HOME's path is written `~`, the settings file and `buddy status` are cut
> where `# …` says so, and the output is preserved except for that
> substitution, the wrapping and trimming, and those marked omissions.

### 1. Clone and install

```sh
git clone https://github.com/JsizzleR/buddy-system && cd buddy-system
sh scripts/install.sh                   # builds buddy + buddylist into ~/bin, installs the skill, REPORTS hook wiring
# BUDDY_CHAT=off sh scripts/install.sh  # claims only: no daemon, no MCP server, nothing listening
cd /path/to/your/repo && buddy init     # one ledger per repo, inside .git; every worktree shares it
```

**Re-run `install.sh` after every pull.** It is also the upgrade, and it
rebuilds every copy it knows about (`~/bin`, the chat daemon's program, this
checkout's `bin/`): a `buddy` older than the ledger's schema refuses to open
it, and the gate then denies every write. It also installs the
[skill](skills/buddy/SKILL.md) to `~/.claude/skills/buddy/`, which teaches a
session how the verbs fit together. Put `~/bin` on your `PATH`
(`BUDDY_BIN_DIR` overrides it; the hook lines spell the path out either way).

#### Claims-only: no chat stack

```sh
BUDDY_CHAT=off sh scripts/install.sh
```

builds and verifies `buddy` alone, skips every chat path, and reports hook
wiring against the six `buddy` lines; a `buddylist`, daemon plist or MCP
registration already on the machine is reported and left where it was. Then
declare it to every session, so `hello` prints no chat hint:

```json
{ "env": { "BUDDY_CHAT": "off" } }
```

in `~/.claude/settings.json`. Wire the six `buddy` lines from
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
is a silent no-op there. `init` also says whether each of the repo's main
languages has a language-server plugin configured, with the command that
fixes it ([details](docs/USAGE.md#language-servers--what-buddy-init-reports));
re-running it is safe.

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
