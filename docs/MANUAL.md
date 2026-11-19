# The Buddy System — a user manual

This is the guide to follow, start to finish, the first time you set buddy up
and whenever you come back to a part you have not used. It is task by task:
each section says what to type, what you should see, and where the detail
lives. The [README](../README.md) says what buddy is; [USAGE.md](USAGE.md) is
the per-feature reference this manual points into; `buddy --help` and
`buddy <verb> --help` are always current, and every command below answers
`--help` as written.

**Who it is for.** You run two or more Claude Code (or Codex) sessions on one
machine, in one repository or several, and you want them to stop editing the
same files without knowing it. You are the *operator*: the one person whose
machine, repositories and decisions these are. Buddy is single-operator by
design; it trusts your user account and is not a boundary between people.

**What it is, in one paragraph.** Two halves. **Claims** (`buddy`) is a
SQLite ledger inside each repository's git directory recording which session
has reserved which files; a hook on every edit refuses one that lands inside
another session's claim. It also carries your brake (`buddy pause`) and a
message inbox between sessions. **Chat** (`buddylist`) is an optional IRC
(or AIM) room where you can watch the sessions; it is the view, never the
lock, and claims work with it entirely absent. Enforcement is cooperative: the
gate judges the paths a tool call declares, and nothing stops a process that
bypasses the harness. A seatbelt for agents, not a sandbox against them.

**If an agent is helping you,** have it load the `buddy-lead` skill
(`/buddy-lead` once installed): it asks what you want before anything is
installed and walks these same steps one checkpoint at a time. An agent
installing buddy for you follows [AGENT-SETUP.md](AGENT-SETUP.md).

## Contents

1. [Before you start](#1-before-you-start)
2. [Install](#2-install)
3. [Wire the hooks](#3-wire-the-hooks)
4. [Turn it on in a repository](#4-turn-it-on-in-a-repository)
5. [Check it works](#5-check-it-works)
6. [Your first claim, with two sessions side by side](#6-your-first-claim-with-two-sessions-side-by-side)
7. [Refused? Waiting and messages](#7-refused-waiting-and-messages)
8. [Shared files and one-at-a-time resources](#8-shared-files-and-one-at-a-time-resources)
9. [Your controls as the operator](#9-your-controls-as-the-operator)
10. [Chat (optional)](#10-chat-optional)
11. [Codex sessions](#11-codex-sessions)
12. [Running a fleet](#12-running-a-fleet)
13. [Upgrading](#13-upgrading)
14. [Uninstalling](#14-uninstalling)
15. [Troubleshooting: what each refusal means](#15-troubleshooting-what-each-refusal-means)
16. [Where the details are](#16-where-the-details-are)

## 1. Before you start

- **Go 1.26 or later, and `git`.** Nothing else is needed for claims; the
  binaries are pure Go.
- **macOS** is where buddy is developed and used. Linux builds and passes the
  hermetic test tier, but a session's harness process cannot be found there,
  so the roster never marks a process gone, `buddy msg` prints no wake
  address, and `buddy who` says "cannot say" about jobs. Windows is not built.
- **Decide on chat now.** Claims-only (no daemon, nothing listening) is the
  default recommendation; coordination never needs chat. You can add or remove
  chat later (sections [10](#10-chat-optional) and [14](#14-uninstalling)).

## 2. Install

```sh
git clone https://github.com/JsizzleR/buddy-system && cd buddy-system
BUDDY_CHAT=off sh scripts/install.sh   # claims only
# sh scripts/install.sh                # claims and chat
```

The install builds `buddy` (and, with chat, `buddylist`) into `~/bin`,
installs the skills into `~/.claude/skills/` (`buddy`, `buddy-fleet`,
`buddy-lead`, `model-router`), and prints a report. It **reports** hook wiring;
it does not edit your settings unless you ask it to (next section). The
binary records your chat choice, so later installs and `buddy update` keep it
until you set `BUDDY_CHAT=on` or `=off` on one run.

Put `~/bin` on your `PATH` (or set `BUDDY_BIN_DIR` before installing; the hook
lines spell the path out either way). **What you should see:** the report
ends `install: done`. If it says `install: done, but ACTION NEEDED (N below)`,
the lines after it name each thing to do; `install: FAILED` says what failed.

This checkout is your install source from now on: `buddy update` and
`buddy uninstall` run the scripts of the checkout the installed `buddy` was
built from, so keep it.

## 3. Wire the hooks

The hooks are what make a session take part: they register it at start,
refuse an edit inside someone else's claim, deliver its messages and mark it
idle. They go in `~/.claude/settings.json` (every repository) or in one
repository's `.claude/settings.local.json`.

The simplest way, once you have read what it will add:

```sh
sh scripts/install.sh --wire-hooks    # or: buddy update --wire-hooks
```

It appends only the buddy lines its report names missing, each in an entry of
its own, and changes nothing else. To merge by hand, copy the JSON block from
[README "Hook wiring"](../README.md#3-hook-wiring). The `buddy` lines run
`hello`, `gate`, `beat`, `idle`, `busy` and `bye`; the two `buddylist` lines
are chat's, left out of a claims-only install.

Every line is guarded: a missing binary turns the feature off instead of
failing a tool call. The one exception is deliberate: `gate` **fails
closed**, so a `buddy` that is installed but cannot run refuses the edit rather
than letting it through (section [15](#15-troubleshooting-what-each-refusal-means)).

**What you should see:** re-run `sh scripts/install.sh` and its `hooks:` line
reports every line found, and its `gate:` line names the fail-closed entry.
Hooks load when a session starts, so only sessions started after this take
part. Whether Claude Code accepts the whole settings file is for your own
`claude doctor` to say.

## 4. Turn it on in a repository

In each repository whose sessions you want coordinated:

```sh
cd /path/to/your/repo
buddy init
```

That creates `buddy.db` in the repository's git **common** directory: every
worktree of the checkout shares one ledger, and git never sees the file. A
repository you never `buddy init` is untouched; every hook is a silent no-op
there. `init` also says whether each main language of the repository has a
language-server plugin configured, with a `fix:` line if not. Re-running it
is safe.

**Optional: the commit gate.** The edit gate sees only paths a tool call
declares; a code generator or a shell redirect can still write a file inside
someone's claim. The commit gate checks staged paths at `git commit`. Git
will not let a repository turn on its own hooks, so link it yourself, in each
worktree whose hooks path is relative:

```sh
hooks_dir="$(git rev-parse --path-format=absolute --git-path hooks)"
mkdir -p "$hooks_dir"
ln -s /path/to/buddy-system/.githooks/pre-commit "$hooks_dir/pre-commit"
```

It warns by default (`BUDDY_COMMIT_GATE=deny` refuses, `=off` or
`BUDDY_COMMIT_GATE_SKIP=1` silences it), and it refuses only a collision with
another session's claim: a staged path no claim of yours covers is reported,
never refused. Details: [USAGE "The commit gate in detail"](USAGE.md#the-commit-gate-in-detail).

**Outside git** (a folder of documents, an operations workspace), there is no
repository to hold a ledger. A *fleet ledger* covers that:
`buddy init --fleet <name>` makes one under `~/.buddy/fleets/<name>/`, and
sessions launched with `BUDDY_FLEET=<name>` use it wherever no repository
ledger governs their directory. It coordinates claims and
messages; it governs no edit gate. See [USAGE "Outside git"](USAGE.md#outside-git--a-fleet-ledger).

## 5. Check it works

Start a **fresh** Claude Code session in the repository. Its first lines
include:

```text
BUDDY: you are session <label> (<session id>). Claim before taking a bundle: ...
```

Then, in that session (or ask it to run them):

```sh
buddy status
buddy claim demo --dry-run --desc "trying buddy" --scope README.md
```

`buddy status` shows the session's row; the dry run answers
`dry run: no conflicts; nothing was taken`. If `buddy status` says the
session `has not said hello in this repo's ledger`, the hooks were not loaded:
check the settings file, and that the session was started after you wired them.

## 6. Your first claim, with two sessions side by side

Open two sessions in the same repository (two terminal tabs, or two
worktrees of it: they share one ledger). Call them A and B. The commands are
what each session runs; in practice the agent runs them itself, prompted by
the `buddy` skill (`/buddy`) and by the digest it gets at start.

1. **A claims a file.**

   ```sh
   buddy claim demo --desc "trying buddy" --scope README.md
   ```

   A *scope* is a file or a directory prefix (no globs, always relative to the
   repository root); repeat `--scope` for several. A claim is granted whole or
   refused whole.

2. **B tries the same.** It is refused, exit status 4, and nothing is taken:

   ```text
   REFUSED: README.md  (overlaps "README.md" held by <A's label>, claim "demo")
   ```

   Had B skipped the claim and gone straight to an edit, the gate would have
   denied the edit with `README.md is inside scope "README.md" claimed by
   session <A> (slug "demo": trying buddy). Coordinate or claim different
   scopes.` and named how a stuck holder is freed.

3. **B looks and asks.**

   ```sh
   buddy whose README.md      # who claimed it, and who has it dirty right now
   buddy msg demo "are you nearly done with README.md?"
   ```

   A message can be addressed to a session id, a label, an open claim's slug
   or `all`. It is delivered on the recipient's next tool call. The result
   line gives the message's `#id` and, on macOS, says how to wake the
   recipient through its harness: buddy wakes nobody, so if A is idle at its
   prompt, that line is how B (or you) nudges it.

4. **A hands it back.**

   ```sh
   buddy release demo                 # or: buddy release demo --to <B's label>
   ```

   `--to` hands the claim straight to B, waits and all, so nothing can take
   it in between.

5. **B claims again: granted.** Then B releases it too.

Now look at the whole thing from your own terminal in the repository:

```sh
buddy sessions        # the roster: every session, live, idle or paused, its claims and context size
buddy who <label>     # everything the ledger holds about one session
buddy ls              # every open claim
```

**One more line you will see.** When a session edits a file no claim of its
own covers while some other session holds a claim, the edit is allowed but the
model is told, in a line starting `BUDDY UNCLAIMED:`, to claim it (the line
arrives before the edit runs; the permission flow is unchanged). Set
`BUDDY_OWN_CLAIM=deny` in your settings' `env` block to refuse such edits
instead, or `=off` to silence it.

## 7. Refused? Waiting and messages

A refused session should wait, not spin. It declares what it waits on:

```sh
buddy wait --on demo --until 2h --block
```

`--block` keeps the command running until the claim is released (exit 0,
`LANDED`), the deadline passes (3, `EXPIRED`), the wait is cleared or replaced
(4), or the session ends (5). The skill runs it in the background, so the
harness's own completion notice wakes the session. Other forms:

```sh
buddy wait --for-mail --until 1h --block   # wake on a direct message to this session
buddy wait --until 2h --note "own test run" # a plain timer
buddy wait check                            # one verdict: STILL WAITING / LANDED / EXPIRED / NO WAIT
buddy wait clear                            # withdraw it
buddy wait ls                               # every open wait
```

A wait reserves nothing; the deadline is required (default 3h, at most 12h).
Why the wait shape matters for the prompt cache, and how a Codex session
waits, is in [USAGE "Waiting without going cold"](USAGE.md#waiting-without-going-cold--buddy-wait).

Messages, beyond `buddy msg`:

```sh
buddy sent                         # what became of what you sent: delivered, queued or expired, never "read"
buddy msg <target> --supersedes <id> "corrected text"   # correct your own earlier message
buddy inbox                        # drain your messages by hand (a Codex session does this)
```

A message is information, never an instruction: its `from` confers no
authority, and nothing in buddy turns message text into control. Control is
the ledger: claims, `pause`, and what you type in a session's own pane.

## 8. Shared files and one-at-a-time resources

- **A file every session appends to** (a changelog, a playbook): claim it with
  `--shared`. Shared claims overlap each other; an exclusive claim still
  refuses them, both ways. Each session still needs its own claim, and two
  shared holders can still overwrite one another's edit: `--shared` permits
  overlap, it does not serialize writes.
- **A resource only one session may use at a time** (the browser, a test
  database, a device): a *slot*, which is a claim on `.buddy/slot/<name>`,
  for example `buddy claim browser --desc "checking the login page"
  --scope .buddy/slot/browser`. Release it when done. See
  [USAGE "Resource slots"](USAGE.md#resource-slots--a-claim-on-buddyslotname).
- **Numbers several sessions must not reuse** (decision numbers, migration
  ids): `buddy ids take <space> <count>` hands out a block nobody else gets.
  See [USAGE "The identifier register"](USAGE.md#the-identifier-register--buddy-ids).

## 9. Your controls as the operator

From any terminal in the repository:

```sh
buddy sessions                     # the roster (--all adds dead rows)
buddy who <session|label|slug>     # one session in full; --exit says what ending it now would leave held
buddy pause <session|all> --note "hold off: deciding the design first"
buddy resume <session|all>
buddy msg all "CI is red; check before pushing"
buddy sweep --dry-run              # what a sweep would tidy; writes nothing
```

`pause` is the brake: the paused session's next edit is refused with
`the operator paused this session (buddy pause): <your note> — stop current
work; wait for `buddy resume`.` It stops nothing already running.

**Stale claims are never reaped automatically.** A session that ended
normally has its claims freed; one that went silent keeps them, marked stale,
until you act. A plain `buddy sweep` tidies the claims of sessions that have
said bye; `buddy sweep --force` also frees claims of sessions silent more than
24 hours; `buddy release <slug> --session <its id>` frees one claim; and
`buddy bye <session>` ends the row of a session whose process is gone (the
gate's refusal names which of these applies).

## 10. Chat (optional)

The chat half is a room per project where each live session sits under its
own name, wearing its claim as an away message: the nick list is the fleet.
You need a chat server you run, bound to loopback (the servers are
unauthenticated only because nothing off the machine can reach them):

```sh
sh scripts/get-ergo.sh                   # a pinned IRC server into .cache/ergo-server
.cache/ergo-server defaultconfig > ergo.yaml
# edit ergo.yaml: keep only the "127.0.0.1:6667" listener, then
.cache/ergo-server run --conf ergo.yaml
```

Then install with chat (`BUDDY_CHAT=on sh scripts/install.sh` if you started
claims-only), wire the two `buddylist` hook lines, register the MCP server
per repository as [README "Presence"](../README.md#presence-the-fun-half)
shows, and point any IRC client at the server. Sessions get `chat_read` and
`chat_send` tools; an alert tells a session it was named, by counts only,
never text. Everything else: [USAGE "Chat internals"](USAGE.md#chat-internals).

## 11. Codex sessions

Codex takes part through the same ledger: its patch tool goes through the
gate, it is identified by its native thread id, and it reads its own messages
with `buddy inbox`, because no tool call delivers them. Its hook wiring and
limits, including running every buddy call escalated under the Codex sandbox,
are in [USAGE "Codex participation"](USAGE.md#codex-participation), and the
`buddy` skill's "Harness boundaries" section is what a Codex session reads.

## 12. Running a fleet

A *fleet* is one coordinating session (the orchestrator) opening other
sessions (lanes), each working one item, and landing their work together. The
whole recipe is the [buddy-fleet skill](../skills/buddy-fleet/SKILL.md): the
coordinator loads `/buddy-fleet`; you do not need to learn it yourself. What
you decide, and write down once in your own user-level instructions, is your
standing policy: how lanes open (terminal tabs, background sessions), how many
at once, which model and permission mode, the context size at which a session
hands off to a fresh one, and how deep a review each item owes. Start from
[examples/fleet-conventions.md](examples/fleet-conventions.md) and fill in its
angle brackets. Set the handoff size in your settings' `env` block as
`BUDDY_HANDOFF_AT`; the install report says whether it is set.

What buddy does and does not do in a fleet: the orchestrator holds a claim
saying it coordinates, each lane claims its own files, and a lane that is done
hands its item to the orchestrator in a *run* (`buddy release <item> --to
<orchestrator> --in-run <run>`) so one test run covers several lanes' work
([USAGE "One long run closes out several sessions"](USAGE.md#one-long-run-closes-out-several-sessions)
and ["Landing through an orchestrator"](USAGE.md#landing-through-an-orchestrator)).
Buddy launches no session, wakes none and ends none: the harness (or you)
opens and closes lanes, and buddy reports who holds what.

## 13. Upgrading

**Re-run the install after every pull.** Either:

```sh
cd /path/to/buddy-system && git pull && sh scripts/install.sh
buddy update --dry-run    # from anywhere: where the install checkout stands; installs nothing
buddy update              # then run that checkout's install.sh (it never pulls)
```

`buddy update` reads only local git: it says where the checkout stands against
main and the last-fetched origin, names the git commands you may want first,
then runs `scripts/install.sh` there. The install rebuilds every copy of every
buddy binary it knows about, refreshes the skills, restarts the chat daemon if
there is one, and keeps your chat choice. Pass `--refresh-hooks` to replace
a hand-installed hook script that exists, is wired and differs from the
checkout's, once the new one passes its self-test.

**Why it matters.** A new `buddy` may migrate a ledger to a newer schema, and
an older `buddy` refuses to open a newer ledger: every edit is then refused
until every copy is rebuilt. The install rebuilds them all at once, which is
why it is the one upgrade path. Never copy a binary over a running one on
macOS (`cp` invalidates its signature and the next run is killed); the install
does it safely.

## 14. Uninstalling

```sh
buddy uninstall --dry-run   # the plan: what goes, what stays, and why; removes nothing
buddy uninstall --chat      # remove the chat half only; claims-only from then on
buddy uninstall             # everything install.sh put in place
```

It runs `scripts/uninstall.sh` in the install checkout. It removes the
binaries, the skills, the chat daemon and its MCP registrations, and the
README-spelled hook lines (with a backup of the settings file beside it). It
never touches a ledger, the chat journal or your other settings, and it names
everything it leaves. Details:
[USAGE "buddy uninstall"](USAGE.md#buddy-uninstall--remove-it-or-only-the-chat-half-d-125).

## 15. Troubleshooting: what each refusal means

Each entry starts with the words you will see.

- **`REFUSED: <scope>  (overlaps "<their scope>" held by <label>, claim "<slug>")`**
  from `buddy claim`, exit 4. Another session holds an overlapping claim.
  Wait on it (`buddy wait --on <slug> ...`), message it, or claim something
  else. A line `SHARED: ...` under it says whether `--shared` would clear the
  conflict. Never take the file anyway.
- **`<path> is inside scope "<scope>" claimed by session <holder> ...
  Coordinate or claim different scopes.`** from the gate, on an edit. Same
  cause, caught at the edit. The sentence after it is the remedy for the
  holder's state: a holder that said bye is freed by any `buddy claim` or a
  plain `buddy sweep`; one whose harness process is GONE is freed with the
  `buddy bye` command the refusal prints; a silent one needs you.
- **`the operator paused this session (buddy pause) ...`** You (or a script
  of yours) ran `buddy pause`. `buddy resume <session>` lifts it.
- **`BUDDY UNCLAIMED: no claim of this session covers <path> ...`** Not a
  refusal: this check let the call proceed (the permission flow is
  unchanged), and the session should claim the file.
  Under `BUDDY_OWN_CLAIM=deny` it is a refusal reading
  `<path> is inside no claim of this session; refusing <tool>`.
- **`buddy gate failed (status N); refusing the tool call (the gate fails
  closed). Operator, from a terminal: sh scripts/install.sh in the
  buddy-system checkout`** Every edit in every session is refused, in every
  repository, because the installed `buddy` exists but cannot run (killed by
  a bad copy, or failing to start; a missing one turns the feature off
  silently instead). Re-run `sh scripts/install.sh` from a terminal. If the
  checkout cannot build, `chmod -x ~/bin/buddy` turns the feature off until it
  can, through the README wiring's `[ -x ]` guard (a Codex gate line has no
  such guard: USAGE "Codex participation"). To see the gate's own error:
  `echo '{"session_id":"s","tool_name":"Write","tool_input":{"file_path":"f"}}' | ~/bin/buddy gate`.
- **`ledger schema is newer than this buddy binary: <path> is at schema
  version N and this binary understands up to M; rebuild and reinstall
  buddy ...`** Some copy of `buddy` is older than the ledger: a hook line that
  names a different binary, or a checkout you built from long ago. Run
  `sh scripts/install.sh` (or `buddy update`) from a current checkout.
- **`buddy ledger read failed (...); refusing <tool>`** or a refusal naming
  the ledger as unavailable or unwritable: the ledger exists but cannot be
  read or written (permissions, a full disk, a lock). The gate refuses rather
  than guess; the text names the cause and the fix. A repository with no
  ledger at all is different: buddy stays silent there.
- **`session <id> (from <source>) has not said hello in this repo's ledger`**
  from a typed verb. The session never registered: its hooks were not wired
  or not loaded (it started before you wired them). Wire them (section 3) and
  start a fresh session; the refusal itself names the registration command
  for an id it can paste back. A Codex session registers itself as
  [USAGE "Codex participation"](USAGE.md#codex-participation) says.
- **`no ledger at <path> — this repository is not coordinated ...`** from a
  typed verb. This directory's repository was never `buddy init`ed. Run the
  verb from the repository your peers use, or `buddy init` this one if it is
  meant to be coordinated on its own.
- **A refusal ending `refused: <reason>`** is a usage refusal: an unknown
  flag, a stray argument, or a value that looks like a flag. `buddy <verb>
  --help` gives the usage line.
- **A message that never arrives.** A message is delivered on the
  recipient's next tool call; a session idle at its prompt takes none. Check
  `buddy sent`; nudge the session at the wake address the `msg` result
  printed.

## 16. Where the details are

| You want | Read |
| --- | --- |
| What each hook does and why | [USAGE "Hooks in detail"](USAGE.md#hooks-in-detail) |
| Claims: scopes, shared, parked, stale, handing on | [USAGE "Claims in depth"](USAGE.md#claims-in-depth) |
| The brake and sweeps | [USAGE "The operator's brake"](USAGE.md#the-operators-brake--pause-resume-sweep) |
| Messages, corrections, wake addresses | [USAGE "Messages"](USAGE.md#messages--buddy-msg) |
| The roster's columns | [USAGE "The roster"](USAGE.md#the-roster--buddy-sessions) |
| Waits and the prompt cache | [USAGE "Waiting without going cold"](USAGE.md#waiting-without-going-cold--buddy-wait) |
| Files every session is told changed | [USAGE "Authority files"](USAGE.md#authority-files--buddy-authority) |
| Upgrades and the ledger's schema | [USAGE "Upgrading and the ledger's schema"](USAGE.md#upgrading-and-the-ledgers-schema) |
| Measuring what buddy costs in context | [USAGE "Measuring context cost"](USAGE.md#measuring-context-cost) |
| A read-only check of this machine for many sessions | `sh scripts/env-check.sh` (`--list` names the checks) |
| Why it is built this way | [DESIGN.md](DESIGN.md), then [decisions.md](decisions.md) by D-number |
