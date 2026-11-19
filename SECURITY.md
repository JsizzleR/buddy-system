# Security

## Reporting a vulnerability

Please report privately through GitHub's
[private vulnerability reporting](https://github.com/JsizzleR/buddy-system/security/advisories/new)
rather than a public issue. This is a single-maintainer project, so response
times are not guaranteed. Only the latest commit on `main` is
supported.

## What the Buddy System is, and is not, meant to resist

It is a coordination tool for one operator's agents on one machine. Knowing
its boundaries tells you what counts as a vulnerability. How the parts named
here fit together is in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

**In scope** — please report:

- A way for content an agent reads (a `buddy msg` body, a chat message, a
  peer's claim description, a label, a path) to break out of its fencing. That
  covers forging extra rows or lines in a listing or spilling into another
  column, impersonating the operator or `buddy` itself, or reaching an agent's
  context unfenced. Every such value is meant to render on exactly one line via
  `internal/fence`. The ledger creating a session row for an id outside the
  plain form D-201 admits, or taking a newly supplied label that is
  `operator`, `all` or another session's id (D-206), is the same bug; a row
  or a label stored before those rules is kept as it is. The chat alert hook
  should carry a room, counts and sequence numbers only; chat text in it is a
  bug.
- `scripts/issue-read.py` printing the text of a GitHub issue or comment
  whose recorded author is not the authenticated `gh` user (D-215). It checks
  the author GitHub records, not who last edited the text: D-215 records that
  open. The hook that refuses other ways of reading an issue is cooperative:
  see below.
- A way for chat to confer authority: a chat message that pauses, resumes,
  claims, releases, or otherwise changes the ledger. Chat is never meant to be
  control.
- The gate allowing an edit it should deny for a path the tool call declared:
  inside another session's exclusive claim, a stale one included (D-022; one
  exception is by design: a claim parked in a run yields to a caller whose own
  exclusive claim declares that run, D-133); inside a shared claim when the
  caller holds no covering claim of its own; under `BUDDY_OWN_CLAIM=deny`, a
  path no open claim of the caller covers while another session holds a claim
  in that ledger, for the callers and paths D-228 judges (it names those it
  does not: a caller with no session row calling from another directory, a
  path with a `.git` component); while paused; through another spelling of
  the path (a symlink, `..`, a case alias); from another working directory or
  inside a nested repository, with or without a ledger of its own, when a
  ledger at or above the path holds the claim or the pause (D-212; D-226,
  which names the layouts it leaves open); or when a ledger exists but cannot
  be opened or read, is newer than the binary, or cannot be told from an
  absent one (those must deny, never allow: D-037, D-084, D-231).
- Where a ledger governs, the gate letting through a `Bash` command it parses
  as running `apply_patch`; one of gh stack's rewriting subcommands while the
  caller does not hold `.buddy/slot/gh-stack` in the ledger of the repository
  the command runs in (D-108; a ledger above that repository applies its
  pause, not its slot, D-226); or any command while a ledger that its working
  directory, or a literal `cd` or `git -C` in it, reaches has the session
  paused (D-138, which bounds how many such directories one command is read
  for: four ledgers); or adjudicating a hook call whose native session ids
  disagree as either session, where it must refuse (D-085).
- A delayed `bye` from a dead incarnation ending a live session and so freeing
  its claims, or a delayed `beat` reviving an ended one (CLAUDE.md's
  invariant 12; D-025, D-069).
- README's gate line letting a tool call through when the installed `buddy`
  crashes, is killed or cannot run: it must refuse (D-107). D-107 names what
  it does not cover (a binary that exits 0 having decided nothing, a timeout
  on a Claude Code build without `onFailure`).
- Any path by which buddy copies transcript text, rather than the last turn's
  token counts, into the ledger or another session's context; or a report
  script or the model router's `router.py` printing message text from a
  transcript it read (they print counts, ids and byte lengths only: D-177).
- `buddy-adapter` (opt-in, never installed by `install.sh`) letting its worker
  read or forge an operator approval, starting, outside a qualification run,
  a native executable whose exact hash has no committed admission record
  (D-079), running a qualification candidate, which its typed sha256 binds,
  against a provider off loopback (D-080), controlling a worker it did not
  launch (D-070), letting a worker's tool call through when its hook cannot check the
  captured identity or run the gate, letting an inherited `BUDDY_OWN_CLAIM`
  decide a worker's gate (D-235), or passing a native server's error text or a
  child's stderr on unfenced (D-198, D-200).
- A server or socket binding a non-loopback address by default.

**Out of scope, by design:**

- **Anything that assumes a hostile local user.** The ledger trusts the
  machine's user account. Any process running as you can edit `buddy.db`
  directly, or run any `buddy` verb, the operator's included (`pause`,
  `resume`, `sweep --force`): no verb authenticates its caller. It is not a
  multi-tenant boundary.
- **Anything that bypasses the harness.** Enforcement is cooperative. The gate
  adjudicates the paths Claude Code tool calls declare (`Edit`, `Write`,
  `NotebookEdit`, any other tool the hook's matcher sends it that names a
  `file_path`) and every path of a Codex `apply_patch`. A `Bash` command is
  checked against the operator's pause, and parsed (never run) for a route by
  which a shell would run `apply_patch`, which is refused where a ledger
  governs. A command that only might run it (a name built at run time, a
  script that will not parse) is refused under Codex only. Anything else it
  writes is not adjudicated. A tool the matcher does not name (an MCP tool, a
  document connector) never reaches the gate. The commit gate checks staged
  paths against claims, but by default it only warns (`BUDDY_COMMIT_GATE=deny`
  makes it refuse a collision with another session's claim; a staged path no
  claim of the committer covers is reported and never refused, D-228), and
  only where its `pre-commit` hook is installed and runs (git lets no
  repository enable its own hooks; `--no-verify`, a rebase and a cherry-pick
  skip it). A process that never goes through a hook is not bound at all. It
  is a seatbelt for agents, not a sandbox against them.
- **An edit nobody has claimed, by default.** A path that nobody holds and no
  claim of the caller covers draws one line in the model's context while
  another session holds a claim in that ledger, and the edit proceeds; only
  `BUDDY_OWN_CLAIM=deny` refuses it. Under Codex the default prints no line
  at all, a session alone in a ledger is never told, and a `Bash` write is
  never seen (D-228).
- **A gate the harness does not run.** The hook lines are yours to wire
  (`install.sh` reports them, and adds missing ones only under
  `--wire-hooks`). A settings file the harness will not load, a session started
  before the lines were there, and a Codex gate wired only in a project layer
  whose directory is not trusted (D-185) each leave the gate not running, and
  buddy cannot see that from inside.
- **A directory no ledger governs.** While `buddy` runs, the gate is off for
  a path when no `buddy.db` exists in its repository or in any repository
  above it, including in a repository git refuses that holds none. A ledger that
  exists is the switch; one that exists and cannot be read denies, and so
  does a `.git` the gate may not look inside (D-231). An installed `buddy`
  that cannot run refuses every gated call in every session, ledger or none:
  it cannot tell them apart, and a refusal naming the fix was chosen over a
  gate that fails open (D-107).
- **A fleet ledger guards no file.** The coordination-only ledger that
  `BUDDY_FLEET` names for work outside git
  (`~/.buddy/fleets/<name>/buddy.db`) holds slot claims, messages and waits;
  the gate never reads it, so it refuses no edit and carries no pause
  (D-191).
- **Who a message says it is from.** `buddy msg` stamps the sender's
  registered label, and `--from` only adds a tag after it. A process with no
  session id in its environment is taken for the operator's terminal and
  signs `operator`; one whose id this ledger does not know signs
  `unregistered s-<8hex>` (D-105). Any process can unset its ids, so a
  sender's name confers no authority, and message text is delivered marked
  untrusted.
- **The issue-author hook's reach.** `scripts/hooks/issue-author-gate.py` is
  installed by hand and refuses the reads of issue text it can recognize in a
  Bash, WebFetch or Monitor call. D-215 lists what it does not (a command
  behind a variable, an alias or a wrapper script, an MCP GitHub tool, a
  browser, a Codex session). Only the reader's own output is in scope above.
- **Unauthenticated chat servers on loopback.** The chat servers run without
  authentication *because* they bind loopback. If you bind a real interface,
  turn authentication on first (ergo has SASL; `scripts/run-local.sh` runs the
  TOC server with `DISABLE_AUTH=true`, on loopback only); running them exposed
  without it is a misconfiguration, not a vulnerability.
- The time between a gate check and the write it allowed (TOCTOU). The gate
  decides at the tool-call boundary, and that window is documented.

## What it installs, links and listens on

For anyone checking it against a machine policy.

- **Claims only** (`BUDDY_CHAT=off sh scripts/install.sh`): `buddy` in `~/bin`
  (and the checkout's `bin/` copy, if present), built with `go build`, which
  downloads the modules below when the module cache lacks them; the agent
  skills under `~/.claude/skills/` (`buddy`, `buddy-fleet` with its reference
  files, `buddy-lead`, and `model-router`, which carries one Python script,
  `router.py`, run only when a session or you invoke it); this checkout's git
  hooks path; and one SQLite ledger in the git common directory of each
  repository you `buddy init`. The installer reports the Claude Code hook
  lines and edits your settings only when you pass `--wire-hooks`, which
  appends the buddy lines its report names missing and nothing else (D-135).
  It reports two more hooks, the Agent-model and issue-author ones, which you
  copy and wire by hand; `--refresh-hooks` replaces a copy of one that is
  already there, wired and out of date (D-232). No MCP server, no daemon,
  nothing listening, and `buddy` itself opens no network connection.
  `buddy init --fleet <name>` adds a ledger under `~/.buddy/fleets/`; the
  model router, when run, keeps its table and refusal log in
  `~/.local/state/model-router`.
  `buddy uninstall` removes what the installer put in place and leaves ledgers
  and anything it cannot prove is buddy's (D-125, D-173).
- **With chat** (the default): also `buddylist` in `~/bin`. You register its
  local stdio MCP server yourself (`claude mcp add-json`; Claude Code starts it
  as a child process, and it is not a remote MCP) and supply the launchd agent
  that runs the daemon; the installer only rebuilds and restarts one that
  exists. The daemon serves a unix socket in `~/.buddylist` (mode 0700), where
  it also keeps its journal, and connects to an IRC server you run yourself
  (ergo, which the README configures for loopback only; `scripts/get-ergo.sh`
  fetches and builds a pinned release into the checkout's `.cache/` when you
  run it); the installer installs no server. Chat is the view and never the lock: the
  claims and the gate work the same without it.
- **`buddy-adapter`** is built only when you ask for it; no script here
  installs it.

## Third-party Go modules

Pure Go, no cgo. `go version -m ~/bin/buddy` lists the modules a binary was
built from; `go tool nm` says which left code in it. Measured 2026-11-19 at
5dcfe1c: the claims binary `buddy` (the gate, which runs on every tool call)
is built from every module below except `open-oscar-server`, which only
`buddylist` uses. `bigfft`, `go-isatty` and `go-strftime` are in the build of
both but leave no code in either: the linker drops them as unreachable.
`buddy-adapter` is built from `golang.org/x/sys` alone.

| Module | Why it is here |
| --- | --- |
| `modernc.org/sqlite` | The ledger. A C-to-Go translation of SQLite, chosen so the build needs no cgo. |
| `modernc.org/libc`, `mathutil`, `memory` | That translation's C runtime. |
| `github.com/dustin/go-humanize`, `github.com/google/uuid`, `github.com/mattn/go-isatty`, `github.com/ncruces/go-strftime` | Imported by `modernc.org/libc`, not by our code. |
| `github.com/remyoudompheng/bigfft` | Imported by `modernc.org/mathutil`, not by our code. Its own README calls it "a toy proof-of-concept" not "expected to be used in any production system". Only `mathutil.BinaryLog` calls it, and nothing we link calls that: no code of it is in any of the three binaries. |
| `mvdan.cc/sh/v3` | The gate parses a Bash command with it to find a shell-routed `apply_patch`. Parse only: nothing is expanded or run. |
| `golang.org/x/sys`, `golang.org/x/text` | Process-table reads; Unicode normalization (the one folding rule) and TOC's charset. |
| `github.com/mk6i/open-oscar-server` | The TOC/AIM wire format (`internal/tocwire`). `buddylist` only. |

Two of these are pinned to commits rather than releases: `open-oscar-server`
and `bigfft`, whose pin is a 2023-01 commit and which has no tagged release.
The build's `modernc.org/libc` is the v1.73.4 that `modernc.org/sqlite` v1.53.0
declares, and `scripts/check-libc-pin.sh` fails the hermetic tier if anything,
`open-oscar-server` included, moves it off the version sqlite declares (it
once raised it to v1.67.1 over sqlite v1.40.1's v1.66.10). A vulnerability in
any module whose code `buddy` links is in scope: report it here as well as
upstream.
