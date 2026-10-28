# Security

## Reporting a vulnerability

Please report privately through GitHub's
[private vulnerability reporting](https://github.com/JsizzleR/buddy-system/security/advisories/new)
rather than a public issue. This is a single-maintainer project, so response
times are not guaranteed. Only the latest commit on `main` is
supported.

## What the Buddy System is, and is not, meant to resist

It is a coordination tool for one operator's agents on one machine. Knowing
its boundaries tells you what counts as a vulnerability.

**In scope** — please report:

- A way for content an agent reads (a `buddy msg` body, a chat message, a
  peer's claim description, a label, a path) to break out of its fencing. That
  covers forging extra rows or lines in a listing or spilling into another
  column, impersonating the operator or `buddy` itself, or reaching an agent's
  context unfenced. Every such value is meant to render on exactly one line via
  `internal/fence`. The chat alert hook should carry counts and sequence
  numbers only; chat text in it is a bug.
- A way for chat to confer authority: a chat message that pauses, resumes,
  claims, releases, or otherwise changes the ledger. Chat is never meant to be
  control.
- The gate allowing an edit it should deny for a path the tool call declared:
  inside another session's exclusive claim, inside a shared claim when the
  caller holds no covering claim of its own, while paused, through another
  spelling of the path (a symlink, `..`, a case alias) or a nested governed
  repository, or when the ledger exists but cannot be read or is newer than
  the binary (those must deny, never allow).
- README's gate line letting a tool call through when the installed `buddy`
  crashes, is killed or cannot run: it must refuse (D-107). D-107 names what
  it does not cover (a binary that exits 0 having decided nothing, a timeout
  on a Claude Code build without `onFailure`).
- Any path by which buddy copies transcript text, rather than the last turn's
  token counts, into the ledger or another session's context.
- `buddy-adapter` (opt-in, never installed by `install.sh`) letting its worker
  read or forge an operator approval, starting a native executable whose exact
  hash has no committed admission record, or running a qualification
  candidate against a provider off loopback.
- A server or socket binding a non-loopback address by default.

**Out of scope, by design:**

- **Anything that assumes a hostile local user.** The ledger trusts the
  machine's user account. Any process running as you can edit `buddy.db`
  directly. It is not a multi-tenant boundary.
- **Anything that bypasses the harness.** Enforcement is cooperative. The gate
  adjudicates the paths Claude Code tool calls declare (`Edit`, `Write`,
  `NotebookEdit`) and every path of a Codex `apply_patch`. A `Bash` command is
  checked against the operator's pause, and parsed (never run) for a route by
  which a shell would run `apply_patch`, which is refused where a ledger
  governs. A command that only might run it (a name built at run time, a
  script that will not parse) is refused under Codex only. Anything else it
  writes is not adjudicated. The commit gate checks staged paths against
  claims, but by default it only warns (`BUDDY_COMMIT_GATE=deny` makes it
  refuse), and only in a checkout whose hooks path `setup-clone.sh` set. A
  process that never goes through a hook is not bound at all. It is a seatbelt
  for agents, not a sandbox against them.
- **A repository with no ledger.** While `buddy` runs, the gate is off there,
  including in a repository git refuses that holds no `buddy.db`. A ledger
  that exists is the switch; one that exists and cannot be read denies. An
  installed `buddy` that cannot run refuses every gated call in every session,
  ledger or none: it cannot tell them apart, and a refusal naming the fix was
  chosen over a gate that fails open (D-107).
- **Who a message says it is from.** `buddy msg` signs with the sender's
  registered label, but a process with no session signs as `operator`, or as
  its `--from`. A sender's name confers no authority, and message text is
  delivered marked untrusted.
- **Unauthenticated chat servers on loopback.** The chat servers run without
  authentication *because* they bind loopback. If you bind a real interface,
  turn authentication on first (ergo has SASL; `scripts/run-local.sh` runs the
  TOC server with `DISABLE_AUTH=true`, on loopback only); running them exposed
  without it is a misconfiguration, not a vulnerability.
- The time between a gate check and the write it allowed (TOCTOU). The gate
  decides at the tool-call boundary, and that window is documented.

## What it installs, links and listens on

For anyone checking it against a machine policy.

- **Claims only** (`BUDDY_CHAT=off sh scripts/install.sh`): binaries in `~/bin`
  (and the checkout's `bin/` copies, if present), the agent skills at
  `~/.claude/skills/buddy/SKILL.md` and `~/.claude/skills/buddy-fleet/SKILL.md`,
  a git hooks path per checkout, and one
  SQLite file in each repository's git directory. You add the Claude Code hook
  lines to your settings yourself; the installer reports them and never edits
  settings. No MCP server, no daemon, nothing listening, no network
  connection.
- **With chat** (the default): also `buddylist` in `~/bin`. You register its
  local stdio MCP server yourself (`claude mcp add-json`; Claude Code starts it
  as a child process, and it is not a remote MCP) and supply the launchd agent
  that runs the daemon; the installer only rebuilds and restarts one that
  exists. The daemon serves a unix socket in `~/.buddylist` (mode 0700), where
  it also keeps its journal, and connects to an IRC server you run yourself
  (ergo, which the README configures for loopback only); the installer
  installs no server. Chat is the view and never the lock: the claims and the
  gate work the same without it.

## Third-party Go modules

Pure Go, no cgo. `go version -m ~/bin/buddy` lists the modules a binary was
built from; `go tool nm` says which left code in it. Measured 2026-10-27: the
claims binary `buddy` (the gate, which runs on every tool call) is built from
every module below except `open-oscar-server`, which only `buddylist` uses.
`bigfft`, `go-isatty` and `go-strftime` are in the build but leave no code in
either binary: the linker drops them as unreachable.

| Module | Why it is here |
| --- | --- |
| `modernc.org/sqlite` | The ledger. A C-to-Go translation of SQLite, chosen so the build needs no cgo. |
| `modernc.org/libc`, `mathutil`, `memory` | That translation's C runtime. |
| `github.com/dustin/go-humanize`, `github.com/google/uuid`, `github.com/mattn/go-isatty`, `github.com/ncruces/go-strftime` | Imported by `modernc.org/libc`, not by our code. |
| `github.com/remyoudompheng/bigfft` | Imported by `modernc.org/mathutil`, not by our code. Its own README calls it "a toy proof-of-concept" not "expected to be used in any production system". Only `mathutil.BinaryLog` calls it, and nothing we link calls that: no code of it is in either binary. |
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
