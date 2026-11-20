# The Buddy System — a user manual

This is the guide to follow, start to finish, the first time you set buddy up
and whenever you come back to a part you have not used. It is task by task:
each section says what to type, what you should see, and where the detail
lives. The [README](../README.md) says what buddy is; [USAGE.md](USAGE.md) is
the per-feature reference this manual points into; `buddy --help` and
`buddy <verb> --help` are always current, and every `buddy` command below
answers `--help` as written.

**Who it is for.** You run two or more Claude Code (or Codex) sessions on one
machine, in one repository or several (or in plain folders outside git,
section [13](#13-work-that-is-not-code-and-work-outside-git)), and you want
them to stop editing the same files without knowing it. You are the *operator*: the one person whose
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
13. [Work that is not code, and work outside git](#13-work-that-is-not-code-and-work-outside-git)
14. [Many sessions on one machine: the memory slot](#14-many-sessions-on-one-machine-the-memory-slot)
15. [Upgrading](#15-upgrading)
16. [Uninstalling](#16-uninstalling)
17. [Troubleshooting: what each refusal means](#17-troubleshooting-what-each-refusal-means)
18. [Where the details are](#18-where-the-details-are)

## 1. Before you start

- **Go 1.26 or later, and `git`.** Nothing else is needed for claims; the
  binaries are pure Go. The install and uninstall scripts read your settings
  file with `python3`: `--wire-hooks` needs it, and without it the install
  report says `gate: not checked` (section [3](#3-wire-the-hooks)).
- **macOS** is where buddy is developed and used. Linux builds and passes the
  hermetic test tier, but a session's harness process cannot be found there,
  so the roster never marks a process gone, `buddy msg` prints no wake
  address, and `buddy who` says "cannot say" about jobs. Windows is not built.
- **Decide on chat now.** Claims-only (no daemon, nothing listening) is the
  default recommendation; coordination never needs chat. You can add or remove
  chat later (sections [10](#10-chat-optional) and [16](#16-uninstalling)).

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

Put `~/bin` on your `PATH`, so that you and your sessions can type `buddy`.
The hook lines of the next section name `$HOME/bin/buddy` in full and do not
depend on `PATH`. (`BUDDY_BIN_DIR=<dir>` installs somewhere else; the hook
lines then have to name that directory, written by hand, because lines that
run `$HOME/bin/buddy` would run nothing, and `--wire-hooks` refuses for that
reason.)

**What you should see:** the report ends `install: done`. If it says
`install: done, but ACTION NEEDED (N below)`, the lines after it name each
thing to do; `install: FAILED` says what failed.

This checkout is your install source from now on: `buddy update` and
`buddy uninstall` run the scripts of the checkout the installed `buddy` was
built from, so keep it.

## 3. Wire the hooks

The hooks are what make a session take part: they register it at start,
refuse an edit inside someone else's claim, deliver its messages and mark it
idle. They go in `~/.claude/settings.json` (every repository) or in one
repository's `.claude/settings.local.json`. The install script reads and
writes only the first: lines you keep in a repository's own file work, and
the install report still calls them not wired.

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
than letting it through (section [17](#17-troubleshooting-what-each-refusal-means)).

**What you should see,** with the lines in the user-level file: re-run
`sh scripts/install.sh` and its `hooks:` line
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

If the repository already has a `pre-commit` there, the link fails: add the
line `"$HOME/bin/buddy" commit-gate || exit $?` to that hook instead. The
gate warns by default (`BUDDY_COMMIT_GATE=deny` refuses, `=off` or
`BUDDY_COMMIT_GATE_SKIP=1` silences it), and it refuses only a collision with
another session's claim: a staged path no claim of yours covers is reported,
never refused. Details: [USAGE "The commit gate in detail"](USAGE.md#the-commit-gate-in-detail).

**A folder that is not a git repository** has nowhere to keep a ledger, and
buddy is off there. Section
[13](#13-work-that-is-not-code-and-work-outside-git) covers both ways on:
making the folder a repository, which gives it everything in this manual, or
a *fleet ledger*, which coordinates sessions and protects no file.

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

Each session has a *label*, the name on its first `BUDDY:` line: by default
the directory's name and a short form of its id (`myrepo/s-1a2b3c4d`). To give
one a name you choose, start it with `BUDDY_LABEL=<name>` in its environment
(`BUDDY_LABEL=alpha claude`; lower-case letters, digits, `.`, `_` and `-`).

1. **A claims a file.**

   ```sh
   buddy claim demo --desc "trying buddy" --scope README.md
   ```

   A *scope* is a file or a directory prefix (no globs, always relative to the
   repository root); repeat `--scope` for several. A claim is granted whole or
   refused whole.

2. **B asks for the same file, under a slug of its own.**

   ```sh
   buddy claim demo-b --desc "also trying buddy" --scope README.md
   ```

   It is refused, exit status 4, and nothing is taken:

   ```text
   REFUSED: README.md  (overlaps "README.md" held by <A's label>, claim "demo")
   ```

   The last line it prints is the wait B can declare, `to be told when it
   frees: buddy wait --on demo` (section 7). A slug names one open claim in
   the ledger: had B reused `demo`, a line `REFUSED: slug "demo" is held by
   <A's label>` would have come first.

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
   or `all`. It is delivered with the recipient's next tool call, or with the
   next prompt typed into it. The result line gives the message's `#id` and
   says what the ledger knows about the recipient. For one that is idle at
   its prompt it also names, on macOS, the address to wake it through its
   harness: buddy wakes nobody, so that line is how B (or you) nudges it.

4. **A hands it back.**

   ```sh
   buddy release demo                 # or: buddy release demo --to <B's label>
   ```

   `--to` hands the claim straight to B, waits and all, so nothing can take
   it in between.

5. **B claims `demo-b` again: granted** (after a `--to`, B already holds
   `demo`). Then B releases what it holds.

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

A refused session should wait, not spin. It declares what it waits on, with
the command the refusal printed and a deadline:

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

A wait reserves nothing, and every wait has a deadline (`--until`: 3h when
you name none, 12h at most).
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
  ids): you seed the space once with the highest number already used,
  `buddy ids seed <space> <n>`; from then on `buddy ids take <space> <count>`
  hands a session a block nobody else gets. A space never seeded is refused:
  the register does not read your files and will not guess where to start.
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
work; wait for `buddy resume`.` It stops nothing already running. A message
typed from a plain terminal, where no session is running, is signed
`operator`.

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
claims-only) and wire the two `buddylist` hook lines. The install builds
`buddylist` and starts nothing, so start the daemon that sits between the
sessions and the server yourself, naming a room after each repository's
directory, and leave it running:

```sh
buddylist serve --rooms yourproject
```

Register the MCP server per repository as
[README "Presence"](../README.md#presence-the-fun-half) shows, and point any
IRC client at the server. Sessions get `chat_read` and `chat_send` tools; an
alert tells a session it was named, by counts only, never text. If you keep
the daemon under a launchd agent of your own named
`com.buddy-system.buddylistd`, each later install restarts it on the new
build. Everything else: [USAGE "Chat internals"](USAGE.md#chat-internals).

## 11. Codex sessions

Codex takes part through the same ledger: its patch tool goes through the
gate, it is identified by its native thread id, and it reads its own messages
with `buddy inbox`, because no tool call delivers them. The `BUDDY UNCLAIMED:`
line of section 6 is not shown under Codex; only `BUDDY_OWN_CLAIM=deny` acts
there. Its hook wiring and
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
`BUDDY_HANDOFF_AT`; the install report's `handoff:` line says whether it is
set. A session is told it has passed that size by the `busy` hook line, so
that line must be wired.

What buddy does and does not do in a fleet: the orchestrator holds a claim
saying it coordinates, each lane claims its own files, and a lane that is done
hands its item to the orchestrator in a *run* (`buddy release <item> --to
<orchestrator> --in-run <run>`) so one test run covers several lanes' work
([USAGE "One long run closes out several sessions"](USAGE.md#one-long-run-closes-out-several-sessions)
and ["Landing through an orchestrator"](USAGE.md#landing-through-an-orchestrator)).
Buddy launches no session, wakes none and ends none: the harness (or you)
opens and closes lanes, and buddy reports who holds what.

## 13. Work that is not code, and work outside git

A claim reserves a path, and nothing in the ledger asks what the path holds.
So several sessions can be coordinated on research notes, a report, a runbook
or configuration as well as on code. There are two cases, and they differ in
what buddy can refuse:

| | Inside a git repository | A plain directory, outside every repository |
| --- | --- | --- |
| How sessions join | `buddy init` once; the hooks do the rest | a *fleet ledger* you create; each session is started with `BUDDY_FLEET=<name>` |
| File claims and the edit gate | yes, as for code | **no**: nothing reserves or refuses a file |
| The brake, `buddy pause` | yes | **no**: refused; you stop a session in its own pane |
| Roster, messages, waits, outcomes | yes | yes |
| Several sessions' work landed in one run | yes, with four words translated (13.1) | **no**: nothing is integrated; a session reports and is done |

In a plain directory with neither, buddy is off: every hook is silent and
every verb that needs a ledger exits 1, saying `not a git repository`.

### 13.1 In a repository: notes, a report, a runbook, configuration

**The folder must be a git repository; commits are optional.** For a folder
that is not one yet:

```sh
cd /path/to/notes
git init
buddy init
```

That puts a `.git` in a folder you keep, so it is your decision and never a
session's. If you never commit, everything works except what names a commit:
`buddy who` has no base commit to show, and `buddy wait --ready HEAD` is
refused.

Sessions then claim the deliverable's paths exactly as in section 6
(`buddy claim q3-report --desc "the Q3 summary" --scope reports/q3`), and an
edit inside another session's claim is refused like any other.

**Where the gate does not reach.** It judges the paths a tool call names. A
call that names none (browsing, a chat tool, a document or spreadsheet
connector) is allowed, so a deliverable written through such a tool is not
gated. Have the session claim it anyway: the claim is the reservation its
peers read. The brake has the same edge: `buddy pause` stops only the tools
the gate is wired to see, and never a web fetch or a web search.

**Run as a fleet,** section 12 applies with four words translated. Your
coordinator reads them in the fleet skill's
[not-code.md](../skills/buddy-fleet/not-code.md); what you will see:

| In a code fleet | When the deliverable is not code |
| --- | --- |
| a commit | a path and a version word, the lane's own name for which state of the files to read: "draft 2", "sections 1-4 final" |
| READY at a commit | the path and the version word in the lane's READY message; a lane that waits for the outcome also carries them in its wait's note, `buddy wait --on <run> --until 4h --note "READY: reports/q3/summary.md draft 2"`, and `buddy wait ls` shows every note |
| the shared test run | whatever check the work has: a review pass over the assembled report, a checklist script, your own read. With none, the run is "read and accept" |
| main | the directory the pieces are assembled into; the coordinator still holds `.buddy/slot/main` while it assembles them |

The run still closes with an outcome that every waiting lane is told, and its
note says which check it was:
`buddy release <run> --outcome pass --note "read and accepted, draft 2"`.

### 13.2 Outside every repository: a fleet ledger

**When it fits.** Several sessions each work an item of their own in a plain
directory (ten investigations, a batch of support tickets), git tracks none
of it, and you want one coordinating session to show you who is working, who
is waiting on an answer from you and who is done, and to carry your answers
back. A *fleet ledger* gives those sessions one roster, one message queue,
waits, and a recorded outcome per item. For a short batch in which each
session asks one question at most and reports once, your harness's own
session list and messages may be all you need; the ledger adds messages that
outlive the turn they were sent in, a wait that says what a session is
waiting on, outcomes you can read back, and one address (`orc-<name>`) that
survives the coordinator handing over to a successor.

**What it does not do.** Read this before relying on it.

- **No edit gate reads a fleet ledger, so it reserves no file.** A claim on a
  file or a directory is refused there. Two sessions writing the same file
  outside git are stopped by nothing: keep them apart in their briefs, one
  directory or one output file each.
- **It carries no brake.** `buddy pause` and `buddy resume` are refused in a
  fleet. To stop a session, interrupt it in its own pane or close the pane.
- **Nothing lands.** There is no run, no READY, no shared test run and no
  main: a session's last act is a report. `buddy release --in-run`,
  `buddy wait --ready`, `buddy claim --shared` and `--based-on` are refused,
  and so are `buddy whose` and `buddy authority` (a fleet has no paths).
- **It replaces no repository's ledger.** An edit that reaches into a
  repository with a ledger of its own is judged by that ledger, as always.

A fleet's refusals read `refused: <what> in fleet <name>: <why>`.

**1. Create the ledger,** once per batch. It is your act, or your
coordinator's on your word; a lane never does it.

```sh
buddy init --fleet inv-a
```

A name is 1 to 64 of `a-z`, `0-9` and `-`, starting with a letter or a digit,
taken exactly as typed. It answers `fleet ledger ready:
<home>/.buddy/fleets/inv-a/buddy.db`, then the variable to start sessions
with. Use a new name for each batch. Keep `~/.buddy/fleets` on a local disk
that nothing syncs, as real directories and not symlinks.

**2. Start every session of the batch with the variable,** the coordinator
included, each in a plain directory of its own:

```sh
cd ~/investigations/ticket-41
BUDDY_FLEET=inv-a BUDDY_LABEL=t-41 claude
```

The variable must be in the session's environment when it starts: a session
already running cannot join. A coordinator that opens the lanes itself puts
both variables on each tab it opens; the fleet skill's
[outside-git.md](../skills/buddy-fleet/outside-git.md) is its recipe, and it
says which ways of starting a session carry no variable.

**What you should see.** The session's first line says where it is:

```text
BUDDY: you are session t-41 (<session id>) in fleet inv-a: a coordination-only ledger (names, messages, waits, outcomes) with no file claim, gate or brake. Claim your item as a slot: buddy claim <item> --desc ... --scope .buddy/slot/<item>
```

and `buddy status` in it opens with `LEDGER       fleet inv-a: <path>
(coordination only: no file claim, gate or brake)`. A session whose
`buddy status` fails is on no roster.

**3. What the sessions then do.** These are the commands you will see them
run, told to by their brief; after a `buddy msg` the sender delivers the
wake address when its result line prints one, as in section 6.

```sh
# the coordinator takes the address every lane reports to
buddy claim orc-inv-a --desc "coordinating inv-a: report with buddy msg orc-inv-a" --scope .buddy/slot/orc-inv-a
# a lane takes its item's name: an address and a status, never a lock on a file
buddy claim t-41 --desc "ticket 41: which date column the export uses" --scope .buddy/slot/t-41

# a lane needs you: it asks the coordinator, then declares what it waits on
buddy msg orc-inv-a "NEED INPUT: which date column is the audit keyed on?"
buddy wait --until 2h --note "NEED INPUT: date column"
# you answer in the coordinator's pane; it relays your words, and the lane goes on
buddy msg t-41 "the operator, in my pane at 14:05: posted_at"
buddy wait clear

# a lane is done: an outcome and where its findings are, then the headline
buddy release t-41 --outcome pass --note "DONE: findings in ticket-41/findings.md"
buddy msg orc-inv-a "t-41 DONE: the audit keys on posted_at"
```

The coordinator watches with one wait on every item
(`buddy wait --on t-41 --on t-42`): each `buddy wait check` lists every
item, the finished ones with their outcome and note. For "where are we" at
any moment, ask it, or run `BUDDY_FLEET=inv-a buddy fleet sessions` and
`BUDDY_FLEET=inv-a buddy fleet wait ls` from any terminal. Read the outcomes
before anyone runs a sweep: `buddy sweep` deletes a released claim, with its
outcome and note, 24 hours after its release.

**What stands in for the words of section 12:**

| In a repository's fleet | In a fleet ledger |
| --- | --- |
| a file claim | a claim on `.buddy/slot/<item>`: who has the item, never who may write a file |
| a commit | the path of the findings, in the release's note |
| READY, and parking the item in a run | nothing: there is no run. `buddy release <item> --outcome pass --note "DONE: <path>"` (or `fail`) and the lane's message are the whole report |
| the shared test run | nothing of buddy's: whoever reads the findings is the check |
| main, and landing | nothing merges. A change an investigation calls for in some repository is a new item there, through that repository's own claims, tests and review |
| `buddy pause` | the session's own pane |

**A coordinator that lives in a repository.** Your usual orchestrator pane,
in a repository with a ledger of its own, can coordinate a fleet too if it
is *started* with `BUDDY_FLEET=<name>`: its hooks keep it on both rosters and
bring it both inboxes. There a bare verb acts in the repository's ledger and
`buddy fleet <verb>` acts in the fleet (`buddy fleet sessions`,
`buddy fleet msg t-41 "..."`, `buddy fleet wait --on t-41`). The same holds
for a lane that works inside such a repository, and its edits there are
judged by that repository's gate.

**When the fleet does not open,** the session says so in one line at its
start, repeated at each prompt while it lasts if the `busy` hook line is
wired, and every fleet verb exits 1 with the same words. No tool call is ever
refused for it: the session is on no fleet roster and gets no fleet mail, and
nothing else changes (in a repository with a ledger of its own, that ledger's
roster and inbox work as before).

- `BUDDY_FLEET=<name> names no fleet ledger on this machine (looked in
  <path>)`: a mistyped name, a different home directory, or a fleet not
  created yet.
- `... is not a fleet name`: start the session again with a well-formed one.
- `fleet ledger unavailable (<cause>)`: fix what the cause names. Never
  delete the ledger to clear it: it holds every session's messages, waits
  and outcomes.
- A session started without the variable is off and says nothing:
  `buddy who <its session id>`, run by the coordinator, refuses, which is how
  it is found.

Details: [USAGE "Outside git"](USAGE.md#outside-git--a-fleet-ledger).

## 14. Many sessions on one machine: the memory slot

Claims keep sessions out of each other's files. They do nothing about a
dozen sessions each starting a test run in the same minute, which can freeze
the machine. The *memory slot* is an admission check on heavy starts, shared
by every session on the machine, in every repository. It is **off until you
turn it on**, and it is macOS only.

```sh
buddy mem init          # turn it on, fitted to this machine; --floor-gb <n>|auto sets the floor
buddy mem status        # free memory, what is booked and pending, the floor and the rule behind it
```

Once it is on, the gate hook that checks claims also judges each shell
command a session starts, whether or not the directory has a ledger. A
*heavy* start (a whole-package or `-race` Go test or build, a
`scripts/check.sh` run, starting a virtual machine, starting another agent
session; USAGE has the list and what each books) books an estimate of the
memory it will take. It is refused when free memory, less what is already
booked, less its own cost, would fall under the floor, or when the system
already reports memory pressure or heavy swapping. A light command never
touches the slot. A booking fades to nothing over 90 seconds: it limits how
much may *start* inside one such window, and it is not a promise that the
start shows in the free-memory reading by then (on macOS it shows there only
as the system compresses memory to make room). A command the session ran and
waited for gives its booking back when it ends, if that is sooner; a session
start, a virtual machine and anything left running in the background only
fade, and so does every booking of a Codex session.

**It fits the machine it is on.** The floor is a sixth of the machine's
memory, never under 1 GB nor over 8 GB (1.3 GB on an 8 GB machine, 2.7 on 16,
4 on 24, 8 from 48 up), worked out at every check and never stored;
`buddy mem init --floor-gb <n>` sets one of your own, and `--floor-gb auto`
goes back. No single start books more than a third of the machine (never
under 1 GB), so on 8 GB the largest start is admitted when half the machine
is free, where a fixed table refused it for ever. Two starts at once still
book two. A call that starts more at once than the machine could hold with
nothing else running is told so at once, by the gate and by `admit` (exit 3,
no place held): waiting will not help, so start them one after another, or
fewer. (If it is a single start, the floor you set leaves it no room on this
machine, and the refusal says so.) If you turned the slot on before it fitted
itself (its config says `"version":1`), it keeps the floor you set and the
full table until you run `buddy mem init --floor-gb auto` or `--floor-gb <n>`;
a bare `buddy mem init` changes nothing and prints what fitting would give.
Every binary on the machine must be the one that knows the fitted config: an
older `buddy` refuses heavy starts under it and names `install.sh`, but its
own `buddy mem init --reset` would write the old fixed floor (4 GB unless you
name one) over it, so run `install.sh` rather than an older binary's init.

**A refusal is a wait** when memory is only short for now (the refusal above
for a call the machine can never hold, and the one below for a slot that
cannot read its own state, say what to do instead). It starts
`MEMORY GATE: nothing in this call ran` and names the command to send
instead, run in the background:

```sh
buddy mem admit <kind> --wait 900 && { <the same command>; }
```

The braces keep every statement of the command behind the admit. `admit`
holds a place in line and is judged again every 15 seconds; later
arrivals count that place in full, so a small start cannot starve a large
one. It exits 0 once admitted (at once when the slot is off), 3 if still
refused when the wait runs out, 1 on an error. The refusal names the
`<kind>`, and `buddy mem --help` lists them. A session must not retry the
bare command in a loop.

**Yours alone:** `buddy mem init`, `buddy mem init --reset` (it clears every
booking), and the override `BUDDY_MEM_OK='<reason>' <command>`, which skips
the verdict, still books, and is logged. Deleting `~/.buddy/mem/config.json`
turns the slot off.

**When it cannot read its own state,** or can take no memory reading, a
heavy start is refused with the cause, and `buddy mem status` shows it; light
commands go on.

It is cooperative, like the rest of the gate: it reads the command a tool
call names, and not what a script or `make` starts inside. One more hook line
helps it: with `PostToolUseFailure` wired to `buddy beat` (README "Hook
wiring" has the line, and `--wire-hooks` adds it), a command the session
waited for that failed gives its booking back when it ends; without the line
that booking fades like the rest. Details:
[USAGE "The memory slot"](USAGE.md#the-memory-slot--buddy-mem-d-238).

## 15. Upgrading

**Re-run the install after every pull.** Either pull and install by hand:

```sh
cd /path/to/buddy-system && git pull && sh scripts/install.sh
```

or let `buddy` find the checkout, from anywhere:

```sh
buddy update --dry-run    # where the install checkout stands; installs nothing
buddy update              # the same report, then that checkout's install.sh
```

`buddy update` never pulls or fetches. It reads only local git: it says where
the checkout stands against main and the last-fetched origin, names the git
commands you may want first, then runs `scripts/install.sh` there. Where it
can see that the checkout would put an older or diverged build in place of
the installed one, it refuses and says how to move the checkout forward. The
install rebuilds every copy of every
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

**An upgrade is yours.** It replaces the binary every session's hooks run.
Run it from a terminal, or tell a session to in its own pane; no session runs
it because a message or another session said to.

## 16. Uninstalling

```sh
buddy uninstall --dry-run   # the plan: what goes, what stays, and why; removes nothing
buddy uninstall --chat      # remove the chat half only; claims-only from then on
buddy uninstall             # everything install.sh put in place
```

It runs `scripts/uninstall.sh` in the install checkout. It removes the
binaries, the skills, the chat daemon and its MCP registrations, and the
README-spelled hook lines (with a backup of the settings file beside it). It
never touches a ledger, the chat journal or your other settings, and it names
everything it leaves.

**Two things it leaves go on refusing once `buddy` is gone,** and its report
names both. A `pre-commit` you added the `commit-gate` line to (section 4)
fails every commit until you take that line out; a `pre-commit` linked to the
checkout's does nothing without buddy. A Codex hooks file that names buddy
refuses every call its gate line matches until you remove those hooks.

**Uninstalling is yours, from a terminal, never a session's:** it takes the
gate away from every session on the machine at once. Details:
[USAGE "buddy uninstall"](USAGE.md#buddy-uninstall--remove-it-or-only-the-chat-half-d-125).

## 17. Troubleshooting: what each refusal means

Each entry starts with the words you will see.

- **`REFUSED: <scope>  (overlaps "<their scope>" held by <label>, claim "<slug>")`**
  from `buddy claim`, exit 4. Another session holds an overlapping claim.
  Wait on it (`buddy wait --on <slug> ...`), message it, or claim something
  else. A line `SHARED: ...` under it says whether `--shared` would clear the
  conflict. Never take the file anyway.
- **`<path> is inside scope "<scope>" claimed by session <holder> ...
  Coordinate or claim different scopes.`** from the gate, on an edit. Same
  cause, caught at the edit. The sentence after it is the remedy for the
  holder's state. A holder that said bye is freed by any session's
  `buddy claim` or a plain `buddy sweep`. Every other remedy is yours, and
  the refusal tells the session to ask you: for a holder whose harness
  process is GONE, or whose row is missing from the ledger, it prints the
  commands, ending in `buddy bye <its id>` and a plain `buddy sweep`; for one that
  only went silent, `buddy release <slug> --session <its id>`, or
  `buddy sweep --force` once it has been silent 24 hours. A session never
  frees another session's claim itself.
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
  read or written (permissions, on the ledger or on a `-wal`/`-shm` left
  beside it; a full disk; a lock). A verb fails when a read or write it needs
  does; the gate refuses a ledger whose open or a read fails, rather than
  guess, and never one merely because it cannot write it. A verb's text names
  the cause and the fix where the system refuses it permission to write the
  ledger file itself (`EACCES` or `EPERM`, a mode or a sandbox; not a
  read-only filesystem, and not where only a `-wal`/`-shm` beside it is
  refused); the gate's names the error and, for a ledger it could not open,
  sends you to `buddy status` for more. A file you can read can still fail to open: SQLite must find the
  `-wal` and `-shm` files beside the ledger or create them, and where it can
  do neither the refusal carries its `attempt to write a readonly database
  (1544)` or `unable to open database file (14)` (that failed create, not a
  write of the gate's; the same words with `(8)`, from a verb, are a refused
  write). A ledger at this buddy's schema that the gate opens but cannot write
  is judged by its claims as usual. So under a sandbox a refusal can come and
  go with those two files, which are there while any buddy process has the
  ledger open. Where one of them has lost its write permission, restore it
  (`chmod u+w`); never delete a `-wal` that is not empty, which can hold
  claims the ledger file does not yet. A repository with no ledger at all is
  different: buddy stays silent there.
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
- **`not a git repository: <directory> — buddy finds its ledger from the
  working directory ...`** from a typed verb. You are outside every
  repository. `cd` into the one you mean; for work that lives outside git,
  section [13](#13-work-that-is-not-code-and-work-outside-git).
- **`refused: <what> in fleet <name>: <why>`**, or a line starting
  **`BUDDY_FLEET=<name> names no fleet ledger on this machine`** or **`fleet
  ledger unavailable`**. The session is in, or was started for, a fleet
  ledger: section 13.2 says what a fleet refuses and what each line means.
- **`MEMORY GATE: nothing in this call ran ...`** The memory slot refused a
  heavy start. It is a wait: send the `buddy mem admit ... --wait 900 &&`
  form the refusal names (section
  [14](#14-many-sessions-on-one-machine-the-memory-slot)).
- **A refusal ending `refused: <reason>`** is a usage refusal: an unknown
  flag, a stray argument, or a value that looks like a flag. `buddy <verb>
  --help` gives the usage line.
- **A message that never arrives.** A message is delivered with the
  recipient's next tool call or its next prompt; a session sitting idle at its
  prompt has neither. The sender's `buddy sent` says whether it is still
  queued and names the wake address again: nudge the session there, or type
  into its pane. A Codex session gets its mail only when it runs
  `buddy inbox`.

## 18. Where the details are

| You want | Read |
| --- | --- |
| What each hook does and why | [USAGE "Hooks in detail"](USAGE.md#hooks-in-detail) |
| Claims: scopes, shared, parked, stale, handing on | [USAGE "Claims in depth"](USAGE.md#claims-in-depth) |
| The brake and sweeps | [USAGE "The operator's brake"](USAGE.md#the-operators-brake--pause-resume-sweep) |
| Messages, corrections, wake addresses | [USAGE "Messages"](USAGE.md#messages--buddy-msg) |
| The roster's columns | [USAGE "The roster"](USAGE.md#the-roster--buddy-sessions) |
| Waits and the prompt cache | [USAGE "Waiting without going cold"](USAGE.md#waiting-without-going-cold--buddy-wait) |
| Files every session is told changed | [USAGE "Authority files"](USAGE.md#authority-files--buddy-authority) |
| A fleet whose deliverable is not code | the fleet skill's [not-code.md](../skills/buddy-fleet/not-code.md) |
| Sessions outside git, and every fleet-ledger refusal | [USAGE "Outside git"](USAGE.md#outside-git--a-fleet-ledger); the coordinator's recipe is [outside-git.md](../skills/buddy-fleet/outside-git.md) |
| One session over several repositories' coordinators | the fleet skill's [fleet-of-fleets.md](../skills/buddy-fleet/fleet-of-fleets.md) |
| The memory slot | [USAGE "The memory slot"](USAGE.md#the-memory-slot--buddy-mem-d-238) |
| Upgrades and the ledger's schema | [USAGE "Upgrading and the ledger's schema"](USAGE.md#upgrading-and-the-ledgers-schema) |
| Measuring what buddy costs in context | [USAGE "Measuring context cost"](USAGE.md#measuring-context-cost) |
| A read-only check of this machine for many sessions | `sh scripts/env-check.sh` (`--list` names the checks) |
| Why it is built this way | [DESIGN.md](DESIGN.md), then [decisions.md](decisions.md) by D-number |
