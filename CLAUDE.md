# CLAUDE.md — the Buddy System

Rules and settled facts for agents working in this repo. Read it before
exploring; almost everything a session re-derives in its first twenty minutes is
already written down here or in the two docs linked below.

## What this is

Coordination for several Claude Code sessions running in parallel on one
operator's machine. Two halves that must never be confused:

- **Claims (`buddy`)** — a transactional SQLite ledger of who has reserved which
  file scopes, plus the operator's brake (`pause`) and the message inbox. This is
  the **safety** half. It is the only thing that reserves anything and the only
  thing that refuses anything. No daemon required.
- **Chat (`buddylist`)** — a concierge daemon over IRC (or TOC/AIM), a durable
  journal, an MCP server, a proactive alert hook, and per-session presence. This
  is the **visibility** half. It never reserves and never refuses.

Go 1.26, pure Go, **no cgo** (`modernc.org/sqlite`). macOS is the developed and
used platform.

## Read this first

- `README.md` — what the thing does, hook wiring, quick start, cost controls.
- `docs/DESIGN.md` — rationale, and the assumptions that got **refuted** by
  measurement. Read the refutations before proposing anything in that area.
- `docs/review-charter.md` — the GIVENs prepended to every Codex review:
  settled decisions, measured environment facts, git facts for hooks. If your
  conclusion requires overturning something there, say so explicitly and bring
  evidence. Do not quietly assume the opposite.

## Repo map

- `cmd/buddy` — the claims binary. No network, no chat, no MCP.
- `cmd/buddylist` — the chat binary. Depends on the store; failure here degrades
  to "no chat", never to "no safety".
- `internal/store` — the SQLite ledger. Claims, sessions, controls, inbox,
  dirty paths. Transactional; WAL; `_txlock=immediate`.
- `internal/cli` — the `buddy` verbs, the Claude Code hooks (`hello`, `gate`,
  `beat`, `idle`, `busy`, `bye`), the git commit gate (`commit-gate`, `commitgate.go`), and the
  ONE place that reads a Claude Code transcript (`transcript.go`: the last
  turn's token counts for the roster, never any message text).
- `internal/fence` — untrusted-content fencing. Every attacker-influenced value
  that reaches a model's context goes through here.
- `internal/buddylist` — the `buddylistd` daemon (`chatd.go`), the journal,
  `alert.go`, `presence.go`, `mcp.go`, `socket.go`.
- `internal/ircwire`, `internal/tocwire` — the two transports, both behind the
  `Conn` seam. **The daemon must not know what IRC is.** Protocol specifics
  (numerics, charset, framing) stay in the wire package; anything the daemon
  needs crosses as a `buddylist`-level type (e.g. `ErrNickInUse`).
- `.githooks/pre-commit` — the commit-time claim gate.
- `docs/`, `scripts/` — as above.

State locations: ledger at `<git-common-dir>/buddy.db` (the **common** dir, so
every worktree of a checkout shares one ledger, and git never sees it); chat
journal at `~/.buddylist/journal.db`.

## Commands

```sh
sh scripts/check.sh [all|hermetic|live]   # default all
sh scripts/setup-clone.sh                 # ONE-TIME PER CHECKOUT (see below)
sh scripts/get-oscar.sh                   # build the pinned AIM-compatible server into .cache/
sh scripts/run-local.sh                   # bring up the local TOC stack + daemon for a trial
scripts/cost-report.sh                    # 7-day context-cost baseline (counts and byte lengths only)
sh scripts/codex-review.sh <prompt-file> <out-file>
```

- **`check.sh` tiers.** `hermetic` = gofmt, source-shape gates, vet, tests,
  `-race`, and the per-feature done-checks; needs only the toolchain and git.
  That is what CI and a fresh clone run. `live` drives the real pinned server
  binary. `all` is the default and runs both.
- **`setup-clone.sh` is not automatic and cannot be.** Git refuses to let a
  repository set its own `core.hooksPath` — correctly, since that names a
  directory of programs git will execute. So an **uninstalled hook is the
  default state of every fresh clone**. The script sets the hooks path and inits
  the ledger; it is idempotent and refuses to steal a hooks path somebody else
  configured.
- **`codex-review.sh` is the ONLY way to invoke Codex.** Never hand-roll
  `codex exec`, and **never background it**: it wedges under an agent harness —
  elapsed climbs, CPU stays ~0, and nothing is ever emitted, so a wedged run and
  a live one are indistinguishable from the output file. If the budget is tight,
  cut the prompt's scope, not the foregrounding. The script also pins model /
  effort / tier and verifies the run header, ASCII-folds the prompt, caps
  concurrency, and prepends the review charter.

Useful knobs: `BUDDY_COMMIT_GATE=warn|deny|off` (default `warn`),
`BUDDY_COMMIT_GATE_SKIP=1`, `BUDDY_COST_DAYS`, `BUDDY_CONTEXT_WINDOW` (a
session's context window, e.g. `1M`/`200k` — DECLARED because it cannot be
derived: the 1M and 200k Opus variants write the same model string into the
transcript, so unset means the roster prints the prompt size with no
percentage), `BUDDY_OSCAR_BIN`. (`BUDDY_LEDGER` is `cost-report.sh`'s knob ONLY — the `buddy`
binary does not read it, and it finds its ledger from the cwd's git common dir.)
Codex: `CODEX_EFFORT` (default `xhigh`; `max` is a second
pass, not a first), `CODEX_TIER`, `CODEX_BUDGET`, `CODEX_NO_CHARTER=1`.

## Invariants — never violate, and flag if a change would

1. **Chat is the view, never the lock.** *Announced is not locked.* Coordination
   and control live in the ledger. Claims must work with chat **entirely
   absent** — not installed, crashed, mid-migration.
2. **Safety hooks fail CLOSED; chat hooks fail SILENT.** A ledger that exists but
   cannot be read denies. A chat failure must never cost a tool call — every
   chat hook line ends in `exit 0`.
3. **"No ledger" and "ledger unreadable" are different verdicts.** Provably not a
   repo, or never `buddy init`ed → feature OFF, silent no-op. Exists but
   unreadable → DENY. Collapsing these into one silent-allow arm is how a safety
   feature quietly stops existing.
4. **A chat message is never authoritative, and a message's `from` never confers
   authority.** Chat text is never translated into control. The authoritative
   record is always a local ledger row entered through the CLI.
5. **Everything binds loopback.** The chat servers are unauthenticated **only**
   because of that. Leaving loopback means turning real auth on first.
6. **The ledger trusts the machine user.** Single-operator tool. It is not a
   tenancy boundary.
7. **The journal records "what the server saw"** — one connection's view. This is
   exactly why per-session presence connections must **never** journal: N live
   sessions would store every message N times and break the contract.
8. **The proactive alert carries room, counts and seqs — never chat text.** The
   byte budget and the untrusted-content fence live in the deliberate
   `chat_read`; an auto-injected body bypasses both.
9. **Every untrusted value read back is rendered on exactly ONE line via
   `internal/fence`, and in a fixed-width listing on exactly ONE COLUMN via
   `fence.Field`.** Claim descriptions, slugs, labels, scopes, paths, chat
   bodies, membership lists. A newline in a value could otherwise fabricate rows
   or a fake cursor line in a fenced listing. Conventional caps: slug 128,
   label/room 64, desc 512, scopes 512, path 512, body 4096.
10. **Dirty paths are OBSERVATIONS and may never refuse anything.** Attribution
    comes only from a tool call naming a path; a `git status` scan may only
    **retract** rows, never add them, because several sessions share one checkout
    and git attributes nothing. The same holds for a session's context
    footprint: an observation of its last turn, read from that session's own
    transcript, stale by construction, and never a denominator it had to guess.
11. **Stale claims are never auto-reaped.** Staleness marks; it never reaps.
    Only positively-ended sessions are cleaned automatically. `sweep --force` is
    the operator's explicit act.
12. **Identity is `(session_id, incarnation)`.** A delayed `bye` from a dead
    incarnation must not orphan a live one; a delayed `beat` must not resurrect
    an ended session. Orphaning happens in `hello`/`sweep`/`claim` (and, the same
    statement, `wait check` — D-033), never inline in `bye`. The hook
    payload names no incarnation, so `bye` is fenced by the harness PROCESS that
    registered the session (`session_procs`, D-025): it ends a session only when
    no registered process is still alive.
13. **One folding rule: `strings.ToLower(norm.NFC.String(s))`.** Never
    `strings.EqualFold`, never a second normalization.
14. **Scope containment is exactly** `scope == path || strings.HasPrefix(path,
    scope+"/")`.

## Style and testing

- **Comments carry the WHY and the failure that motivated the code, including
  refuted alternatives and what was cut.** This is the dominant convention here;
  a new file without it looks foreign. Read the headers of `commitgate.go`,
  `check.sh` and `.githooks/pre-commit` for the register.
- Table-driven tests. Hermetic by default. A real temp git repo per fixture.
- **Mutate every fix and watch the test die.** A test written against a fix is
  not a test of the fix until you have seen it fail without it. Verify a new gate
  also fires on a breakage you did not design it around.
- **A negative/refusal test needs a positive control** proving the guard was
  armed — otherwise "it refused" and "it never ran" look identical.
- Every new `scripts/check-*.sh` must be invoked from `check.sh`. A feature whose
  done-check is not invoked there is a feature nothing gates.
- The `-race` legs are unscoped on purpose: this repo is a concurrency story
  almost everywhere. ~17s hermetic, ~3s live — not worth an honesty problem.
- Some gates are `grep`-shaped because `-race` structurally cannot see the class
  (measured). Those come in two clauses: one for a new occurrence, one for the
  gate being quietly defeated by respelling the line it allows. Keep both.
- **Codex cannot build or test.** A Codex pass is never test evidence. Its
  findings become evidence only once a reproducing test is written and watched
  to fail.
- A guard's review scope is every site that **bypasses** it, not the diff that
  adds it.
- Prefer naming the exact failing input and the resulting wrong behavior over
  describing a category of concern. A ranking resting on an adjective rather
  than a number is a hypothesis.

## Environment gotchas — do not re-derive

- **macOS: `cp` over a running Mach-O invalidates its ad-hoc signature and the
  kernel SIGKILLs the next run (exit 137).** Use `go build -o` straight over the
  target (or `rm` then `cp`). This bites **hooks** specifically, because hook
  lines end in `exit 0` — a killed binary and a silent one are the same
  observation.
- **`sh scripts/check.sh | tail` returns rc=0 over a FAILING run** — `sh` has no
  pipefail. Read the summary line, or drop the pipe.
- **`check.sh` exit 2 means the live leg did not run** (no `.cache/oscar-server`;
  run `scripts/get-oscar.sh`). "Not run" is neither pass nor fail, and it is
  reported loudly on purpose so a leg cannot silently stop running.
- **Restart the daemon when a change adds a journal table** — the running daemon
  migrates the journal at open, so a new table only appears after a restart.
  Hooks spawn a fresh binary per tool call, so *sessions* need no restart.
- **ergo exempts localhost from its own connection limits**, which is why
  presence is bounded at 16 connections in our own code rather than trusting the
  server to bound it.
- **`ISON` answers in two shapes.** `ISON one` → `303 me one` (a plain
  parameter); `ISON one two` → `303 me :one two` (trailing). Reading only the
  trailing form passes every test written against the multi-name shape and then
  reports NOBODY online for the single-name query a DM makes. An over-long ISON
  draws `417 Input line too long` and no 303 at all, on a connection that stays
  up — so the query is length-checked before it reaches the wire.
- `pgrep`/`pkill` abort on non-ASCII patterns ("illegal byte sequence") and
  report BUSY as FREE when they do. Hence the ASCII fold in `codex-review.sh`.
- **The same locale trap bites `ps | awk`**: another process on this box has
  non-ASCII bytes in its argv, and awk aborts the WHOLE scan with `towc:
  multibyte conversion failure` partway through — so a pipeline that looks up
  a pid returns nothing and whatever depended on it is skipped. `LC_ALL=C` in
  front of anything that reads `ps` (measured 2026-09-20, killed a daemon
  restart before it had found the daemon).
- **The chat daemon is supervised by a launchd agent**
  (`~/Library/LaunchAgents/com.buddy-system.buddylistd.plist`, `KeepAlive`),
  so KILLING it starts a race you lose in the confusing direction: launchd
  respawns it within ~10 s, and whichever instance loses the socket lock dies
  with `another daemon is already serving`. If a hand-started copy wins, the
  daemon is alive, unsupervised, and launchd retries against it forever.
  Restart it with `launchctl kickstart -k gui/$UID/com.buddy-system.buddylistd`
  — or kill the stray and let KeepAlive do it.
- **`/clear` mints a NEW session id in the same process** (same pid and pane; the old
  row goes `ended`). `/compact` and `--resume` keep the id. So nothing keyed by
  session id (inbox, wait) crosses a `/clear`, and a message queued to the old id is
  stranded. An id that never took a turn has no transcript and cannot be resumed
  (measured 2026-09-23, D-034).
- Hook latency budget is 100 ms. Measured: `gate` 20 ms, `beat` 13 ms, chat
  alert 1.2 ms warm / 5.9 ms cold.
- **The prompt cache and the harness scheduler, measured 2026-09-23 (D-033).** A
  request up to 3,602 s after the previous one read the cache; 3,633 s and
  later re-wrote it. A self-paced `/loop` wake (ScheduleWakeup) rounds UP to the
  next minute and fires 0-58 s late, so the tool's own "any delay up to 3600
  wakes warm" is false: 9 of 11 one-hour wakes came back cold. A scheduled turn
  runs BOTH `UserPromptSubmit` and `Stop`, so a keep-alive ping resets `idle`.
  Scheduled tasks are session-only (not on disk); surviving `--resume` was not
  measured.
- Git, for anything touching hooks: `--name-only` **quotes** non-ASCII paths (use
  `-z`); rename detection reports only the destination (`--no-renames` gives
  both); `diff.relative=true` in a user config silently makes output
  cwd-relative (pin `-c diff.relative=false`); a **partial commit** points
  `GIT_INDEX_FILE` at a temporary index, so stripping `GIT_*` from a child git is
  correct for a background scan and **wrong** for a commit-time gate.
- macOS default volume is case-insensitive but case-preserving; a repo root can
  be aliased several ways.

## Decisions already made — do not relitigate

- **SQLite with transactions, not flat-file `O_EXCL` locking.** `O_EXCL` reserves
  a filename, not content: partial writes are visible, sweeps have an ABA race,
  and overlap checking needs a transaction anyway.
- **Two binaries**, so the claims ledger is never dragged behind the chat stack.
- **Exact-path / prefix scopes.** Glob scopes and arbitrary-glob overlap math
  were explicitly cut. Do not propose them.
- **Identity is `(session_id, incarnation)`; the pid is never identity (D-025).** It
  is authoritative for ONE decision — whether a hook-driven `bye` may end the session —
  found by walking up to the `claude` ancestor by exec path/argv[0] (never `p_comm`,
  which is the version string), carried with its kernel start time, and diagnostic
  everywhere else (`pid N` / `pid N GONE` on the roster; nothing auto-ends on GONE).
- **IRC is the daily driver.** TOC/AIM stays behind the `Conn` seam for
  nostalgia nights. UTF-8 is native on IRC; the CP1252 conversion is a TOC-only
  concern.
- **Presence is presentation only.** It never reads the ledger from the daemon
  side, never journals, and never speaks. It rides the existing alert hook's
  already-computed identity — no new hook line, no second round-trip. Sends stay
  on the concierge because the concierge's `@sent` outbox is the discriminator
  the alert path depends on.
- **`mentions_me` matches claim SLUGS first**, then label and id. Identity alone
  measured **0** matches on 2313 live messages — peers address each other by
  slug. This is the one place the chat half reads the claims half.
- **The self-alert discriminator is the `@sent` outbox row, not a sender
  comparison.** A relayed message does not know who wrote it: the concierge is
  the sender, `[label]` is text in the body, and the wire chunks long messages
  so only the first chunk carries it (measured: 215 of 2291 rows attributed).
  Every echoed chunk is a substring of the outbox row (13/13 on a 3566-byte
  send).
- **The alert cursor is not the read cursor.** Being told about a message is not
  having seen it.
- **A DM asks whether the recipient is there before sending**, because nothing
  holds mail for a name with no session: `dm` refuses a definite "not online",
  naming them, and sends in every other case (backend cannot answer, probe
  failed, command unsupported). ISON is serialized to ONE outstanding query per
  connection — the reply carries no request tag, so a queue hands one caller
  another's answer — and a query that goes unanswered retires presence on that
  connection rather than leaving a reply owed. Every failure degrades to the old
  unchecked send: refusing somebody reachable would lose a message. Store-and-
  forward for an absent operator was cut; the DM is decoration, and the durable
  channel is elsewhere.
- **No per-session TOC connections**: a screen name there is an account and a
  second signon boots the first, so a collision costs somebody else's
  connection.
- **The commit gate reports claim collisions only.** Reporting "paths you did not
  claim" was considered and cut — it fires on nearly every commit, and a warning
  that fires on everything gets disabled. Consulting `dirty_paths` there was also
  cut: a peer's tool call naming a file is not authorship of the staged hunks.
- **Room digests are never auto-injected into an agent's context**; only operator
  inbox messages are. Context cost is a first-class constraint here.
- **The roster is the orchestrator's view (D-015).** Every age on a
  `buddy sessions` row carries its own word, `PAUSED`/`idle N`/`claims N`/the
  last prompt size trail the id, and the context window is DECLARED
  (`BUDDY_CONTEXT_WINDOW`) or no percentage prints — the transcript cannot tell
  the 1M and 200k variants apart. A header line, a stored branch, a `role`
  field, an undelivered-inbox count and `--json` were all considered and cut;
  see the decision record.
- **Idle is reported; busy is never inferred (D-016).** The `Stop` hook marks a
  session idle, dated by the TURN's end time read from the transcript (a turn
  that ended before this incarnation started is refused), and `beat` clears it
  inside its own transaction. The optional `busy` hook
  (`UserPromptSubmit`) covers the turn that runs no tool at all. NO ROW MEANS
  UNKNOWN: the hooks are opt-in like every other one, so absence of `idle` must
  never be rendered or read as "mid-turn".
- **A column is ONE token (D-017).** Peer text that occupies a fixed-width
  column — a label, a slug — goes through `fence.Field`, which shows spaces as
  `␣` the way `fence.Line` shows newlines as `⏎`. `%-24s` is a minimum width,
  so without it a label owns the columns after it on its own row. Quoting was
  tried and cut: `strings.Fields` splits inside quotes, so it fools only a
  human.
- **`pause`, `resume` and `msg` share ONE target namespace, resolved before it is
  stored**: `all`, session id, label, `s-<8hex>` short form, or an OPEN claim slug —
  most-specific-first, exact (never folded), and anything else is REFUSED rather than
  written as a row that would match nothing. They take a `store.Target`, so the
  compiler is the guard. Slugs resolve because peers address each other by slug
  (D-009); released slugs do not, since the answer would change as history grows.
- **A claim is granted whole or refused whole; the refusal names the WHOLE conflict
  set, `claim --dry-run` forecasts it without writing, and `release <slug> --scope`
  narrows a claim by exact scopes (D-019).** Partial acquisition was cut. `msg` signs
  with the sender's label, which always resolves; `--from` is a tag after it.
- **The roster's cache timer (`cache 1h hot 48m`) is read from the transcript's
  `usage.cache_creation` tiers and never inferred (D-020).** Raw per-tier counts in
  the ledger; hot/cold computed at read time against the turn's own time; a
  both-tier turn is judged by the shorter; no tier recorded prints nothing.
- **`msg` reads its body from stdin when argv has none, never from a TTY, and the
  cap is on the RENDERED body (D-021).** `fence.Line` expands a line break to `⏎`
  at 3 bytes, so a raw-byte cap equal to the render cap still truncates. Argv wins
  when present; the one cap applies to whichever source won.
- **A stale claim REFUSES exactly like a fresh one (D-022); a holder that has SAID
  BYE does not (D-026).** `release`, orphaning of an ended owner (`hello`, `sweep`,
  and first inside `claim`'s own transaction), and `sweep --force` free a scope.
  **`bye` itself still touches no claim row.** The dry run excludes ended owners by
  the same predicate and prints a `note:` for each it would displace; the gates still
  read `state='open'` alone and the deny names the plain `sweep`. Refusals and
  `--dry-run` say when a holder has gone quiet, and a refusal prints the whole set
  even for one conflict.
- **`whose` reports BOTH registers, claim first (D-023).** `CLAIMED BY` then
  `DIRTY IN`, each printing `(none)` rather than being omitted — it was read as
  "unclaimed" when it only ever meant "not dirty", and a session that has just
  claimed a path has no dirty row at all.
- **A `chat_read` of a room the daemon neither SERVES nor REMEMBERS is refused,
  naming the rooms it serves (D-024).** An empty read means a quiet room and
  nothing else. `Say` always refused a room it had not joined; read now matches.
  The SessionStart room name is DERIVED from the label — `path.Base(worktree)`
  while the ledger is in the git COMMON dir — so it is wrong in a linked
  worktree, and the digest says so rather than asserting it.
- **`status` and `who <target>` REPORT and grant nothing (D-027).** Every register the
  ledger holds about one session, resolved through the same target namespace as
  `msg`, and an `EXIT` line describing what the ledger would be left holding — never
  permission. No `exit` verb, no exit-on-peer-request path, ever (issue #20): the
  operator gets the `pid` and `pane` on the row instead. `hello` warns once about a
  label worn twice; `msg` says when the recipient has an outstanding idle report, and
  says nothing when it has none.
- **Authority files are watched by mtime and announced once (D-028).** `buddy
  authority` lists at most 8 repo-relative paths, `CLAUDE.md` always; `beat` tells a
  session ONCE per change, on its next tool call, that a watched file changed on disk
  AFTER it started; `status` shows the same. It is an advisory about the file on disk,
  never a claim about the session's copy; no fingerprint, no git.
- **The id register never parses prose and never reissues (D-029).** `buddy ids`:
  a space is SEEDED with a measured high-water mark before anything is taken (refused
  otherwise; the ceiling is raised, never lowered), `take` hands out the next contiguous
  block above the ceiling in one transaction, there is no `return`, blocks outlive
  their session, and `status` says reserved-here / above-the-ceiling / not-available
  without ever claiming to know what the artifact contains.
- **Coordination state is published as a claim, not messaged (D-030).** An
  `orchestrator` claim's `--desc` (512 bytes, fenced, refreshed by re-claiming) is the
  pull channel every session reads at `hello` and via `ls`/`who`. No key/value store,
  no reserved slug, no compel path: a claim has no `from` and is never an instruction.
- **Every verb answers `--help` before it runs, and refuses what it does not understand
  (D-031).** Dispatch is a table carrying each verb's usage line; `-h`/`-help`/`--help` in
  first position is answered by `Run` for all of them, without a ledger or stdin. An unknown
  flag or a stray positional after the flags is a fenced refusal, never accepted-and-ignored
  (`sweep --help` used to sweep; `sweep --dry-run` swept again and printed a plausible
  zero). `sweep --dry-run` is the real sweep rolled back inside its transaction, and both
  runs name every claim they orphan. Hook verbs keep their argument semantics beyond `--help`.
- **A send reports what the ledger holds about its recipient, never a prediction (D-032).**
  `msg` prints `queued for X — <observation>`, one arm most-alarming-first: ENDED (naming the
  open claims a plain `claim` displaces), harness process GONE, last reported idle, NOT SEEN
  past the stale mark, registered and not seen since, or last seen N ago — then how many
  earlier messages to X are still undelivered and the age of the oldest. It never says
  "delivered", never infers busy, and does not refuse an ended target (`hello` revives the
  id). `who` dates its INBOX line the same way.
- **A parked session keeps its cache warm by DECLARING a wait and arming its OWN
  scheduler (D-033).** `buddy wait --on <slug>... [--until 3h] [--note]` (ceiling
  12h; targets resolved once to claim ids; one row per session with its own
  declaration id) and `/loop buddy wait check` in that session: one tool call, one
  verdict (STILL WAITING / LANDED / EXPIRED / NO WAIT), paced from the check itself —
  `next check in 50m` — NEVER from the ledger's cache clock, which lags one request
  because a check runs before its own beat. The verdict is computed at read time by
  one function every view renders (roster `waiting 1h12m`, `who` WAITING/WAITED ON,
  `release`, `msg`, `hello`, beat's one-shot LANDED). `wait check` speaks only for
  `$CLAUDE_CODE_SESSION_ID`. No keep-alive on the 5m tier. A wait reserves and refuses
  nothing, is never inferred (a refused claim only SUGGESTS `buddy wait --on`), and
  nothing wakes, schedules or types into a pane.
- **`hello` drains the inbox into the SessionStart digest (D-034, #25)** — hook-driven
  only (a hand-run hello counts and marks nothing), beat's fence and write-then-mark,
  bounded by beat's 20 / 8 KiB AND by the room the digest leaves under 9,000 bytes,
  because Claude Code replaces hook output past 10,000 characters with a preview
  (documented, not measured) and that would hide the claims list. The remainder is
  counted and left queued. Waking a session ALREADY at its prompt is still D-027's "no".
- **A resource slot is a claim on `.buddy/slot/<name>` (D-035, #27).** Capacity 1, nothing
  built: the claim refuses, `release` frees, `wait --on` queues, `who` counts. The prefix
  only adds a fenced `SLOT:` line to a refusal and a dry run. No gate reads it, no new scope
  kind, and counted capacity waits until a counted resource is measured contended.
- **The SessionStart claims list fits the budget, ahead of messages (D-036, #30).** Own
  claims first, then oldest first, stop at the first misfit, count the rest. No slug is
  ranked; the gate reads the ledger, so a hidden claim refuses like a shown one.
- **An older binary refuses a newer ledger (D-037, #29).** `user_version > schemaVersion` is
  `ErrLedgerNewer` (Open and migrate's re-check) → the gate DENIES. Never migrate down.
- **Enforcement is cooperative, and saying so is the design.** The gate
  adjudicates declared paths, has a TOCTOU window, and cannot bind a process
  that bypasses the harness. A seatbelt for agents, not a sandbox against them.

## Out of scope

- Multi-machine claims.
- Interrupting an in-flight tool call (harness-level, not ours).
- The chat-command bridge, until real auth is on.
- Any authority derived from chat.
- OS-level containment, filesystem ACLs, or a supervisor process.
