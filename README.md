<p align="center">
  <img src="docs/assets/wordmark.svg" width="420" alt="buddy system">
</p>

<p align="center"><b>Several coding agents, one checkout, no stepping on each other: claim a scope, and the gate refuses everyone else's edit by name.</b></p>

<p align="center">
  <a href="https://github.com/JsizzleR/buddy-system/actions/workflows/check.yml"><img src="https://github.com/JsizzleR/buddy-system/actions/workflows/check.yml/badge.svg" alt="check"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="License: Apache-2.0"></a>
  <a href="go.mod"><img src="https://img.shields.io/badge/go-1.26-00ADD8.svg" alt="Go 1.26"></a>
</p>

# The Buddy System

> You run several [Claude Code] (or Codex) sessions in parallel on one machine.
> Two of them edit the same file. One needs to be told to stop. The buddy system
> is the old safety practice, pair up so nobody wanders off alone, applied to
> agent fleets: a transactional **claims ledger** in the repo's own `.git`, a
> hook that **denies** another session's edit inside a claim and says who holds
> it, messages between sessions, a roster for the operator. No server. Chat is
> optional, and it is never the lock.

## What a session does with it

In three lines:

- **Claims that refuse.** `buddy claim <slug> --scope <path>` reserves files or
  directory prefixes, whole or not at all. A `PreToolUse` hook denies any other
  session's `Edit`, `Write` or patch inside them and names the holder; a
  refused claim names the whole conflict set and how to wait for it.
- **Messages between sessions.** `buddy msg <label|slug|id> "…"` queues a note
  delivered on the recipient's next tool call, with an id so a later message
  can correct it; `buddy sent` says what became of it, never "read".
- **A roster for the operator.** `buddy sessions` shows every session live,
  idle or paused with its claims and context size; `buddy who` is everything
  the ledger holds about one of them; `buddy pause` is the brake.

![Two Claude Code sessions side by side. Each claims a scope and edits inside it. One then edits a file in the other's scope and the buddy gate denies it, naming the holder. It messages the holder, the holder releases, its own claim is granted and the edit goes through](docs/assets/demo.svg)

> **Real output, drawn by hand.** Every line in the picture is `buddy`'s own
> output, captured on 2026-10-25 in a scratch repository with its own ledger:
> `git init`, `buddy init`, two session ids, and the hooks fed the JSON Claude
> Code sends them. The picture is a hand-authored animated SVG over that
> capture, not a recording: long lines are soft-wrapped at 58 columns and
> trimmed with an ellipsis, lines starting with `#` are narration, and no
> words were changed. The [ledger's own record of the same
> choreography](#what-the-ledger-did-during-a-run-of-the-choreography), with
> two real sessions, is below.

## What buddy is not

- **Not a sandbox.** The gate adjudicates the paths a tool call *declares*. A
  shell redirect, a code generator or a process that bypasses the harness is
  not stopped; the commit gate catches some of that at `git commit`, and
  nothing catches all of it. Cooperative enforcement is the design, said
  plainly: a seatbelt for agents, not a sandbox against them.
- **Not a daemon.** The safety half is a CLI spawned per hook call over a
  SQLite file in the repo's git common directory. Nothing listens, nothing has
  to be up, and a machine with chat absent loses visibility, never safety.
- **Not a chat bot.** A chat message is never authoritative and a sender's name
  confers nothing. Whatever is said in a room reserves and refuses nothing.
  *Announced is not locked.*
- **Not an orchestrator.** `buddy` launches, wakes and ends nothing. The
  orchestrator is one of your own sessions, opening lanes with the harness
  (`herdr`, `claude --bg`) and coordinating through claims and messages; the
  [skill](#the-skill) carries the recipe.

Codex participation is being added in bounded steps. See the
[Codex hook and identity guide](docs/USAGE.md#codex-participation) for patch
coverage, installation and current lifecycle limits, and the
[orchestrator work order](docs/CODEX-ORCHESTRATOR-PLAN.md) for qualification status.

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

## Install in two minutes

Requires Go 1.26+ and `git`. Developed and used on macOS; the hermetic suite
passes on Linux in CI, where nobody uses it day to day.

```sh
git clone https://github.com/JsizzleR/buddy-system && cd buddy-system
sh scripts/install.sh                   # builds buddy + buddylist into ~/bin, installs the skill, REPORTS hook wiring
# BUDDY_CHAT=off sh scripts/install.sh  # claims only: no daemon, no MCP server, nothing listening
cd /path/to/your/repo && buddy init     # one ledger per repo, inside .git; every worktree shares it
```

Then add the [hook wiring](#3-hook-wiring) to `~/.claude/settings.json` (the
install script reports what is wired and never edits that file), and start a
new session. Here is the whole thing, from an empty directory to the first
claim in another project:

![Two panes of one terminal. Left: git clone, a claims-only run of the install script reporting what it built and that no hooks are wired, the hook-wiring block saved to the settings file, and the re-run reporting all six wired. Right: buddy init in another project, the digest a new session there gets at SessionStart, its first claim, and buddy status showing the claim held](docs/assets/install.svg)

> **Also real, staged against a throwaway HOME.** The install script's own
> knobs (`BUDDY_BIN_DIR`, `BUDDY_SKILL_DIR`, `BUDDY_SETTINGS`, `BUDDY_DAEMON=off`,
> `BUDDY_SETUP_CLONE=off`, `BUDDY_REPO_BIN=off`, the ones its hermetic
> done-check uses) point every write at scratch paths, so the run touched no
> real settings file; `BUDDY_CHAT=off` makes it the claims-only install. The
> clone comes from a local mirror of `main` spelled as the GitHub URL, and the
> build is quick because the machine's Go build cache was warm. Drawn the same
> way as the picture above, from the captured text: the throwaway HOME's path
> is written `~`, the settings file and `buddy status` are shown in part with
> each cut marked `# …`, long lines are wrapped or trimmed with an ellipsis,
> and no words were changed. No session was opened: the new session's
> `SessionStart` hook was run by hand with the payload Claude Code sends, so
> the digest is the one a session there gets. The language-server line is real
> too; the throwaway HOME has no plugins, and `buddy init` says what fixes that.

### It's 2026: let your agent install it

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

This builds `buddy` and `buddylist` into `~/bin` (override with
`BUDDY_BIN_DIR`), runs each once to prove it starts, installs the
[skill](#the-skill), restarts the chat daemon if a launchd agent for it is
loaded, and reports which hooks are wired in `~/.claude/settings.json`. It
never edits that file. `BUDDY_CHAT=off sh scripts/install.sh` installs the
claims half only ([below](#claims-only-no-chat-stack)): it reports any chat
remnant it found and left alone, and uninstalls nothing.

**Re-run it after every pull.** It is also the upgrade, and it rebuilds every
copy it knows about: `~/bin`, the program the chat daemon's launchd agent
names, and this checkout's `bin/`. A copy nobody remembered matters: a `buddy`
older than the ledger's schema refuses to open it, and the gate then denies
every write. A copy you put anywhere else, you upgrade yourself.

Put `~/bin` on your `PATH` (`export PATH="$HOME/bin:$PATH"` in `~/.zprofile`).
The hook lines below spell the path out, so they work either way; if you set
`BUDDY_BIN_DIR`, replace `$HOME/bin` in them too.

### Claims-only: no chat stack

The claims half was built to work with chat entirely absent, and it can be
installed that way: no daemon, no launchd agent, no MCP server, nothing
listening on a port.

```sh
BUDDY_CHAT=off sh scripts/install.sh
```

That builds and verifies `buddy` alone, skips every chat path, and reports
hook wiring against the six `buddy` lines. It is configuration for this run,
not a statement about the machine: a `buddylist` already in `~/bin`, the
daemon's plist or an MCP registration is reported and left where it was.
Then declare it to every session, so their hooks see it and `hello` prints no
chat hint:

```json
{ "env": { "BUDDY_CHAT": "off" } }
```

in `~/.claude/settings.json` (an `env` entry there reaches every hook, the
same way `BUDDY_HANDOFF_AT` travels). Wire the six `buddy` lines from
[step 3](#3-hook-wiring); the two `buddylist` lines may be omitted, and left
in they are no-ops while the `[ -x ]` guard finds no binary. Do **not**
register the buddylist MCP server.

What you lose is the visibility half: room history, mention alerts, presence,
and a chat UI for the operator. What replaces it is the ledger's own views:
`buddy sessions` for the roster, `buddy who <target>` and `buddy ls` for who
holds what, `buddy msg`, `buddy inbox` and `buddy sent` between sessions. A
durable channel *to the operator* does not exist in this mode: `msg all`
reaches peers, and mail a session addresses to itself is consumed by its own
hooks. So a lane with an orchestrator reports to it, and a lane with none
reports in its own pane.

On Linux (or anything but macOS) process identity is "cannot say", so sessions
are never bound to a harness process and nothing is ever marked `GONE`: the
documented degraded state (D-025). The hermetic suite passes there, but nobody
uses it there day to day.

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

`init` also says, for each main language in the repo, whether a Claude Code
session there gets a language server: the plugin that lets it look up
definitions and references in one call instead of mapping the code by grep.
When one is missing, it prints the command that fixes it. Re-running
`buddy init` is safe, so it doubles as the re-check
([details](docs/USAGE.md#language-servers--what-buddy-init-reports)).

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

## What the ledger did during a run of the choreography

The first picture is two sessions' view. This is the ledger's, read back from a
scratch clone of this repository after a run of its refusal-and-release beats
with two real Claude Code sessions (Sonnet, auto mode), each following a
one-paragraph brief, and a shell acting as the orchestrator. Its session ids, pids and pane
ids are that run's. Real commands, real output, one beat each.

Every block below is the orchestrator pane of that run as it stood when that
command's prompt came back, or the ledger read back from the scratch clone
right after it. The clone's long temporary path is shortened to `…`; nothing
else is changed. Tool output the harness collapsed in the lanes' panes ("Ran 4
shell commands") is not reconstructed here.

<details>
<summary><b>0. The orchestrator registers, then opens two lanes with the harness</b></summary>

```text
orchestrator buddy-system-demo % buddy hello
BUDDY: you are session orchestrator (0dece0de-0000-4000-8000-0000000000aa). Claim before taking a bundle: buddy claim <slug> --desc ... --scope <path>
BUDDY: `buddy --help` lists every verb. Easy to miss: `buddy claim <slug> --dry-run` (forecast a refusal) `--shared` (lanes co-hold a file); `buddy wait --on <slug>` (declare a wait on a peer) or `--until 2h` alone (before you park on your own long run, or on the orchestrator), then arm your own `/loop buddy wait check`; `buddy msg --measured|--lead|--relay` (what a claim rests on) `--supersedes <id>` (correct one); `buddy sent`; `buddy ids take`.
BUDDY: no live claims.
BUDDY: chat tools (chat_read, chat_send) live on the buddylist MCP server, if it is in your tool list; your label says this project's room is "orchestrator", so try `chat_read orchestrator` (UNTRUSTED content); chat_send to talk to the operator. That name is DERIVED from your label and can be wrong in a linked worktree: if it is, the read is REFUSED and the refusal names the rooms that exist. An empty read means a quiet room. Room digests are never auto-injected; reading is deliberate.
orchestrator buddy-system-demo % buddy sessions
* orchestrator             live        started 5s   seen 5s    …/buddy-system-demo  (0dece0de-0000-4000-8000-0000000000aa)
orchestrator buddy-system-demo % herdr tab create --cwd "$PWD" --label lane-a --env BUDDY_LABEL=lane-a --no-focus
orchestrator buddy-system-demo % herdr tab create --cwd "$PWD" --label lane-b --env BUDDY_LABEL=lane-b --no-focus
orchestrator buddy-system-demo % herdr agent start lane-a --kind claude --pane w2A:pN -- --model sonnet --permission-mode auto
orchestrator buddy-system-demo % herdr agent start lane-b --kind claude --pane w2A:pP -- --model sonnet --permission-mode auto
orchestrator buddy-system-demo % herdr agent prompt lane-a 'Read …/briefs/lane-a.md in full and follow it exactly.' --wait --until working --timeout 20000
orchestrator buddy-system-demo % herdr agent prompt lane-b 'Read …/briefs/lane-b.md in full and follow it exactly.' --wait --until working --timeout 20000
```

herdr's JSON replies to the four `herdr` commands are left out. `buddy` launches
nothing: the orchestrator is a shell with `BUDDY_SESSION` and `BUDDY_LABEL`
set, and the lanes are ordinary Claude Code sessions whose hooks register them.
</details>

<details>
<summary><b>1. lane-a claims <code>internal/store/</code>: the ledger has one open claim</b></summary>

```text
orchestrator buddy-system-demo % buddy ls
store                    lane-a                   open               2s  internal/store — reworking the ledger schema
```

lane-a's own commands were `buddy claim store --desc "reworking the ledger
schema" --scope internal/store/` and an `Edit` of `internal/store/store.go`,
which its own claim covers.
</details>

<details>
<summary><b>2. lane-b's <code>Edit</code> of the same file is DENIED, naming the holder</b></summary>

What lane-b's pane showed, verbatim (the `PreToolUse` hook's reason, as
Claude Code renders a denial):

```text
⏺ Update(internal/store/store.go)
  ⎿  Error: PreToolUse:Edit hook error: internal/store/store.go is inside scope
     "internal/store" claimed by session lane-a (slug "store": reworking the ledger schema).
     Coordinate or claim different scopes. A holder that has said bye is freed by any session's
     `buddy claim` or a plain `buddy sweep`; one that went silent needs the operator's `buddy
     release` or `buddy sweep --force`.

⏺ The gate denied the edit because lane-a holds internal/store, as the brief said it would. I
  won't retry it. Step 3 is next.
```

The gate read the ledger, not a message: lane-a had announced nothing.
</details>

<details>
<summary><b>3. lane-b forecasts a claim, asks lane-a for the file, and waits</b></summary>

lane-b's brief had it run `buddy claim store-fix --dry-run --desc "fix the
migration note" --scope internal/store/store.go` (a forecast of the same
refusal, written nowhere) and `buddy msg store "need store.go for one migration
note — release when you can?"`, addressing the claim by its slug, then poll its
inbox; its pane reported "Ran 4 shell commands" and moved on. The
ledger's view of lane-b at that moment, from the orchestrator pane:

```text
orchestrator buddy-system-demo % buddy who lane-b
- lane-b  (2509e099-c818-4ff8-8f94-74908cb4ec93)  live  started 1m  seen 11s  pid 82791  pane herdr:w2A:pP
CLAIMS HELD  0
DIRTY PATHS  (none recorded)
BASE         eebca870 (on main, 48s ago) — an observation at the end of its last reported turn
INBOX        0 undelivered
ADAPTER      (none) — no adapter mail queued and no adapter endpoint recorded
WAITING      none declared
AUTHORITY    no watched file carries a modification time later than this session's start (every watched file was read)
JOBS         1 shell(s) running directly under its harness process (pid 82791) when sampled: pid 95290 8s — the ledger does not track them, and closing its pane may end them
EXIT         no claims held; no wait declared; 1 shell(s) running under it (JOBS above) — the ledger would be left holding no claim (dirty paths, if any, are observations and stay in the tree)
```

The one shell under it is its `sleep 20 && buddy inbox` loop. The message
itself sits in the ledger addressed to lane-a and is delivered by lane-a's next
hook; `who` counts a recipient's undelivered mail, it never shows a body to a
third session.
</details>

<details>
<summary><b>4. lane-a releases; lane-b's claim is granted and its edit lands</b></summary>

lane-a's inbox loop delivered the message; its brief then had it run `buddy
release store` and a `buddy msg lane-b` saying so. Between the release
and lane-b's `buddy claim store-fix --desc "fix the migration note" --scope
internal/store/store.go`, the orchestrator pane caught the ledger empty:

```text
orchestrator buddy-system-demo % buddy ls
no claims
```

Six seconds later `store-fix` was open, lane-b re-ran the same `Edit`, and it
was allowed. Read back after the run:

```text
$ buddy ls --all
store                    lane-a                   released           1m  internal/store — reworking the ledger schema
store-fix                lane-b                   orphaned          35s  internal/store/store.go — fix the migration note
```

(`orphaned` is from the close-out's sweep below, not from the release.)
</details>

<details>
<summary><b>5. The close-out: ask the ledger what ending each lane would leave, then end them</b></summary>

```text
orchestrator buddy-system-demo % buddy who lane-a
- lane-a  (c1d1cfe2-e4e7-415f-8ab2-f7f686abb1e2)  live  started 1m  seen 34s  idle 33s  pid 81274  pane herdr:w2A:pN
CLAIMS HELD  0
DIRTY PATHS  1 recorded to this session (observations, not locks): internal/store/store.go
BASE         eebca870 (on main, 33s ago) — an observation at the end of its last reported turn
INBOX        0 undelivered
ADAPTER      (none) — no adapter mail queued and no adapter endpoint recorded
WAITING      none declared
AUTHORITY    no watched file carries a modification time later than this session's start (every watched file was read)
JOBS         no shell found directly under its harness process (pid 81274) when sampled, besides any running this report (a job that exec'd into another program, or runs under a wrapper, is not seen)
EXIT         no claims held; no wait declared; no shell found running under it — the ledger would be left holding no claim (dirty paths, if any, are observations and stay in the tree)
orchestrator buddy-system-demo % buddy who lane-b | tail -3
AUTHORITY    no watched file carries a modification time later than this session's start (every watched file was read)
JOBS         no shell found directly under its harness process (pid 82791) when sampled, besides any running this report (a job that exec'd into another program, or runs under a wrapper, is not seen)
EXIT         1 claim(s) held; no wait declared; no shell found running under it — ending now would leave the claim(s) held, freed only when some session next runs hello, claim or sweep — `buddy release <slug>` first: store-fix
orchestrator buddy-system-demo % herdr pane close w2A:pN
{"id":"cli:pane:close","result":{"type":"ok"}}
orchestrator buddy-system-demo % herdr pane close w2A:pP
{"id":"cli:pane:close","result":{"type":"ok"}}
orchestrator buddy-system-demo % buddy bye c1d1cfe2-e4e7-415f-8ab2-f7f686abb1e2
orchestrator buddy-system-demo % buddy bye 2509e099-c818-4ff8-8f94-74908cb4ec93
orchestrator buddy-system-demo % buddy sweep
  orphaned store-fix held by lane-b (2509e099-c818-4ff8-8f94-74908cb4ec93)
sweep: 1 orphaned, 0 deleted (open claims of live sessions are never touched; --force orphans claims of sessions silent >24h)
orchestrator buddy-system-demo % buddy sessions
* orchestrator             live        started 2m   seen 2m    …/buddy-system-demo  (0dece0de-0000-4000-8000-0000000000aa)
- lane-b                   ended 9s    started 2m   seen 59s   …/buddy-system-demo  (2509e099-c818-4ff8-8f94-74908cb4ec93)  claude-sonnet-5-5/high prompt 68k turn 59s cache 1h hot 59m
- lane-a                   ended 13s   started 2m   seen 1m    …/buddy-system-demo  (c1d1cfe2-e4e7-415f-8ab2-f7f686abb1e2)  claude-sonnet-5-5/high prompt 67k turn 1m cache 1h hot 58m
```

Three things the ledger said plainly: `who` REPORTS what ending a session would
leave and grants nothing (lane-b's EXIT line names the claim it still held);
`bye` ends a session and touches no claim row, so `store-fix` outlived its
holder until a plain `sweep` orphaned it; and closing a pane is not ending a
session, which is why the `bye` is typed.
</details>

<details>
<summary><b>6. Afterwards: who touched the file, and what is in the tree</b></summary>

```text
$ buddy whose internal/store/store.go
internal/store/store.go
CLAIMED BY   (none)
DIRTY IN
  lane-b                   ended            1m  uncommitted     1m  this worktree
  lane-a                   ended            1m  uncommitted     2m  this worktree

advisory: a dirty path is an observation, never a lock — nobody is blocked and
no scope is reserved by it. The CLAIMED BY block above is the register that does
reserve, and the one the commit gate reads. `buddy msg <label> "..."` reaches a
live session after its next tool call.

$ git diff
diff --git a/internal/store/store.go b/internal/store/store.go
index b3ecd4f..63ea39c 100644
--- a/internal/store/store.go
+++ b/internal/store/store.go
@@ -2436,3 +2436,6 @@ func (s *Store) IdleSessions() (map[string]IdleState, error) {
 	}
 	return out, rows.Err()
 }
+
+// lane-a: schema rework in progress
+// lane-b: migration note
```

Both lines landed, in order, with no overlap: the second one waited for the
first one's holder to say so.
</details>

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

A paused session's next mutating tool call is denied. `msg` never claims
delivery: it reports what the ledger knows about the recipient, such as when it
was last seen or that it is idle at its prompt.

`buddy --help` lists every verb, and `buddy <verb> --help` gives its usage.
**[docs/USAGE.md](docs/USAGE.md)** covers each feature in depth: the roster's
columns, messages and corrections, waits that keep a parked session's prompt
cache warm, resource slots, batching several sessions into one long test run,
landing every lane's work through one orchestrator, authority files, an id
register, and the commit gate.

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
schemas. Skip it in a [claims-only install](#claims-only-no-chat-stack): the
tools would name a daemon that is not there.

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
| [docs/USAGE.md](docs/USAGE.md) | Every feature in depth: hooks, claims, messages, the roster, waits, slots, long runs, landing through an orchestrator, authority files, ids, the commit gate |
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
