# Review charter

Prepended to every `scripts/codex-review.sh` prompt. Everything here is a **GIVEN**:
already decided, already measured, not open for re-litigation in a review. Its purpose is
to buy back the review budget that would otherwise be spent exploring the repo to
re-derive facts that are already known — exploration is the dominant cause of a review
timing out with nothing to show.

If a review's conclusion depends on overturning something in this file, say so explicitly
and give the evidence. Do not quietly assume the opposite.

## What this project is

The Buddy System coordinates several concurrent Claude Code sessions working in one
operator's repositories. It has two halves that must not be confused, and they ship as two
binaries so the claims ledger is never dragged behind the chat stack (D-004):

- **Claims (`buddy`)** — a SQLite ledger of who has reserved which file scopes, plus the
  operator's brake (`pause`) and message inbox. This is the safety half. It is the only
  thing that reserves anything and the only thing that refuses anything.
- **Chat (`buddylist`)** — an IRC/AIM concierge daemon, a durable journal, an MCP server,
  and per-session presence. This is the visibility half. **Chat is never the lock.**

Chat text is never translated into control. The authoritative record is always a local
ledger row entered through the CLI (D-006).

## Settled decisions (GIVENs)

1. **Enforcement is COOPERATIVE, and saying so is the design.** The PreToolUse gate
   adjudicates paths that tools declare. It cannot bind a process that bypasses the
   harness, and a shell command's side effects are invisible to it. There is a TOCTOU
   window between the gate and the write. This is a seatbelt for agents, not a sandbox
   against them. **Do not propose OS-level containment, filesystem ACLs, or a supervisor
   process** — out of scope for a single-operator tool, and pretending otherwise would be
   the worse design.
2. **"No ledger" and "ledger unreadable" are different verdicts (D-005), and must stay
   different.** Provably not a repo, or never `buddy init`ed, means the feature is OFF
   and every hook is a silent no-op. A ledger that exists but cannot be read means DENY
   (fail closed). Collapsing these two into one silent-allow arm is how a safety feature
   quietly stops existing.
3. **SQLite, not flat files (D-001).** Claims need transactional overlap checks; `O_EXCL`
   files lose to ABA, partial writes, and multi-scope atomicity. The driver is
   `modernc.org/sqlite` — pure Go, **no cgo**, deliberately. Ledger opens with
   `_txlock=immediate`; WAL mode, so readers never block the writer.
4. **The ledger lives at `<git-common-dir>/buddy.db` (D-001).** The common dir, not `.git`,
   so that `git worktree` checkouts of one repo share one ledger. It is machine-local and
   never committed.
5. **Scopes are exact paths, repo-relative, slash-separated (D-002).** Containment is
   `scope == path || strings.HasPrefix(path, scope+"/")`. Glob scopes and arbitrary-glob
   overlap math were explicitly cut. Do not propose them.
6. **One folding rule: `strings.ToLower(norm.NFC.String(s))` (D-002).** macOS aliases a
   repo root several ways and its default volume is case-insensitive. Never
   `strings.EqualFold`, never a second normalization. Accepted residual, already
   documented: this over-merges on a case-SENSITIVE volume mounted under a repo.
7. **Every untrusted value is rendered on exactly ONE line via `fence.Line(s, max)`.**
   Claim descriptions, slugs, session labels, scopes, paths, and all chat content are
   attacker-influenced text that lands in a model's context window. A value containing a
   newline could otherwise fabricate rows in a fenced listing. Byte caps are conventional:
   slug 128, label/room 64, desc 512, scopes 512, path 512, message body 4096.
8. **Room digests are never auto-injected into an agent's context (D-010).** Only operator
   inbox messages are. Visibility means the operator sees the room, not that every agent
   ingests it. Context cost is a first-class constraint here, not an afterthought.
9. **Loopback by default, everywhere.** The chat servers run unauthenticated only under
   that condition. Leaving loopback means turning real auth on first. The ledger trusts
   the machine's user account: this is a single-operator coordination tool, not a tenancy
   boundary.
10. **Sessions are identified by `(session_id, incarnation)` (D-003).** A delayed `bye`
    from a dead incarnation must not orphan a live one; a delayed `beat` must not resurrect
    an ended session. PID is diagnostic only, never authoritative.
11. **Dirty paths are OBSERVATIONS and may never refuse anything.** `dirty_paths` records
    which session's tool call named which file. It exists so a message about an
    uncommitted hunk can be ADDRESSED to someone. Attribution comes only from a tool call
    naming a path; a `git status` scan may only RETRACT rows, never add them, because
    several sessions share one checkout and git attributes nothing.
12. **Stale claims are never auto-reaped (D-001).** Sweeping an open claim because its
    owner went quiet is how a live session loses its reservation. `sweep --force` is the
    operator's explicit act.
13. **The journal records what the SERVER saw — one connection's view (D-008).** Which is
    exactly why per-session presence connections must never journal: N live sessions would
    store every message N times and break that contract.
14. **Per-session presence is presentation only (D-011).** It never reads the ledger from
    the daemon side, never journals and never speaks; it rides the alert hook's
    already-computed identity, and sends stay on the concierge because the `@sent` outbox
    is the discriminator the alert path depends on (D-009).
15. **The commit gate reports claim COLLISIONS only, and warns before it denies (D-012).**
    Reporting "paths you did not claim" was cut — it fires on nearly every commit, and a
    warning that fires on everything gets disabled. Consulting `dirty_paths` was cut too: a
    peer's tool call naming a file is not authorship of the staged hunks. There is no
    pre-push CLAIM gate — it would compare historical commits against CURRENT claims.
    (The `pre-push` hook this repo ships is unrelated: it runs the hermetic check tier.)
16. **`pause`, `resume` and `msg` share ONE target namespace, resolved before it is stored
    (D-013).** `all`, session id, label, `s-<8hex>` short form, or an OPEN claim slug —
    most-specific-first, exact and never folded. Anything unresolvable is REFUSED, rather
    than written as a row the exact read-side queries can never match; they take a resolved
    `store.Target`, so the compiler is the guard.
17. **A DM asks whether the recipient is there, and there is no offline delivery (D-014).**
    It refuses a definite "not online" naming them, and sends on every other outcome,
    because refusing somebody reachable would lose a message. ISON is serialized to ONE
    outstanding query per connection: the reply carries no request tag. Store-and-forward
    was cut — the DM is decoration, and the durable channel is elsewhere.
18. **The roster (`buddy sessions`) is the orchestrator's view, and every number on it
    carries its own word (D-015).** `started` dates THIS incarnation's registration,
    `seen` the last hook that spoke for the session, and the state cell carries its own
    age only when the state is a dated event (`ended 29d`). `--by seen|started`, both
    DESC, live rows above ended ones, ties broken on `session_id`; an unknown key is
    refused. `PAUSED`, `claims N` and the context footprint trail the id, so the fixed
    columns never move. A header line was cut: the common case is ONE row quoted into
    chat, where a header is gone and the labels have to ride with the numbers.
19. **The context footprint is an OBSERVATION read from the session's own transcript
    (D-015), and the window is DECLARED, never derived.** `beat` reads the last 64 KB of
    the file the hook JSON names, takes the newest non-sidechain turn's token counts, and
    stores ONE row per session, read back only through a join on the current incarnation.
    No message text is ever stored. Measured 2026-09-20: a session running the 1M-token
    Opus variant records `"model":"claude-opus-5"`, identical to the 200k variant, so a
    percentage inferred from the model string is a fabrication — the denominator comes
    from `BUDDY_CONTEXT_WINDOW` or no percentage prints. The row says `prompt`, never
    "context left", and the turn's own age prints beside it always. Every failure of the
    capture is silent and costs the beat nothing. Measured cost on a 2.3 MB transcript:
    16.5 ms per beat against 16.4 ms without.

20. **Idle is reported; busy is never inferred (D-016).** A `Stop` hook line
    (`buddy idle`) writes a `session_idle` row keyed to the reporting incarnation, and
    `Beat` deletes it in its own transaction — a tool call IS a turn in progress. NO ROW
    MEANS UNKNOWN: the hook is opt-in like every other one, so a fleet without it wired
    reports nobody idle, and absence must never be read as "mid-turn". Only `Stop` is
    wired; `UserPromptSubmit` was cut because the next beat clears the mark within a
    second of the prompt.

21. **A column is ONE token (D-017).** Peer text in a fixed-width column goes through
    `fence.Field`, which renders spaces as `␣` exactly as `fence.Line` renders line
    breaks as `⏎`. `%-24s` is a minimum width, so a label with a space in it owned the
    state column of its own row (measured). Quoting was tried and cut —
    `strings.Fields` splits inside quotes, so it fools only a human. Refusing a bad
    label at intake was also cut: `hello` runs from a hook line ending in `exit 0`, so a
    refusal there turns the feature off silently and repairs nothing already stored.
    Every roster row carries a gutter (`*` the caller, `-` the rest) so the field count
    never depends on which row is read.
22. **The context capture samples the NEWEST turn in the window, escalates once, and
    reports what the model is (D-018).** `turn_ms` is milliseconds — the only column
    here that is not whole Unix seconds — because at second resolution two turns in one
    second compared equal and the older one won. The scan takes the greatest timestamp,
    not the last record positionally. One escalation, 64 KB then a 512 KB cap, paid only
    after a miss. Model and effort print when the transcript recorded them and are never
    inferred from each other. `session_context` is the one table that may be DROPPED in
    a migration, because every row is re-derived at the next beat.

23. **A claim is still granted whole or refused whole, but the refusal names the WHOLE
    conflict set, `claim --dry-run` forecasts it without writing, and a holder narrows its
    own claim with `release <slug> --scope <path>` (D-019).** Forecast and refusal run ONE
    computation (`allConflicts`: another session's slug first, then every overlapping
    requested/held pair), so they cannot disagree; the dry run checks the caller's
    incarnation as the write does and exits non-zero on any conflict. Partial ACQUISITION
    was cut: a claim that comes back holding three of four paths has changed shape under
    its caller. Release names scopes EXACTLY as claimed — releasing `pkg/sub` from a claim
    on `pkg` is refused, because prefix scopes have no subtraction — and releasing the last
    scope releases the claim. `msg` signs with the sender's LABEL (the one form D-013
    guarantees resolves), read from the environment only, with an explicit `--from` kept as
    a tag after it; measured on a live ledger, 1 of 111 direct messages was signed with a
    label and 104 with a claim slug, 14 of which no longer resolved.

24. **The roster's `cache 1h hot 48m` / `cache 5m cold 3m` is read from the session's own
    transcript and never inferred (D-020).** `session_context` keeps the raw per-tier
    write counts from `usage.cache_creation` (measured 2026-09-20: 2371 records, all
    carrying the object, all on the 1h tier, one pure read in 2371); the roster computes
    hot/cold against the turn's own time, judges a both-tier turn by the SHORTER tier, and
    prints nothing when no tier was recorded — an older harness was silent, not on 5m. The
    turn time is the response's, so the true expiry is slightly earlier; the remaining time
    is printed so the edge is visible. The tier carries its own clock (`tier_ms`, the
    WRITER's time) and its columns are guarded by it, separately from the row's `turn_ms`
    guard. Schema 5 rebuilds `session_context` (D-018 allows it).

25. **`buddy msg` takes its body from stdin when argv carries none, and the body cap is
    measured on the RENDERED form (D-021).** A TTY is never read (`stdinIsTTY`, the same
    guard `readHook` uses). Argv wins when present and the ONE cap applies to whichever
    source won — checking stdin only left the guard reachable around via argv. The cap is
    `renderedLen(body) > 4096`, not `len(body)`, because `fence.Line` expands a line break
    to `⏎` at three bytes: `strings.Repeat("x", 4094) + "\nZ"` is 4096 raw, 4098 rendered,
    and the `Z` was silently lost. Stdin is read to a 64 KiB bound before trimming; a
    heredoc's trailing newline is trimmed before the cap and costs nothing. The usage path
    exits NON-ZERO and always has — the field report's `rc=0` was `sh` having no pipefail.

## Environment facts (measured, do not re-derive)

- macOS (darwin), zsh, Go 1.26. Default volume is case-insensitive but case-preserving.
- `pgrep`/`pkill` abort on non-ASCII patterns ("illegal byte sequence") and report BUSY as
  FREE when they do. `ps | awk` has the same trap from the other side: a non-ASCII argv
  belonging to SOME OTHER process aborts the whole scan (`towc: multibyte conversion
  failure`), so prefix anything that reads `ps` with `LC_ALL=C`.
- The chat daemon runs under a launchd agent with `KeepAlive`, so killing it races a
  respawn and the loser dies on the socket lock. Restart it with `launchctl kickstart -k`,
  not `kill`.
- On macOS, `cp` onto an existing Mach-O invalidates its ad-hoc code signature and the
  kernel SIGKILLs the next run (exit 137). Install with `rm` then `cp`, or `go build -o`
  straight over the target. This bites hooks specifically, because every hook line ends in
  `exit 0` — a killed binary and a binary with nothing to say are the same observation.
- Hook latency budget is 100 ms. Measured: `gate` 20 ms, `beat` 13 ms, chat alert 1.2 ms
  warm / 5.9 ms cold.
- IRC is the daily driver (`ergo`, loopback :6667, IRCv3 `echo-message`); the TOC/AIM
  backend stays behind the `Conn` seam and `--backend toc` (D-007). UTF-8 is native on IRC;
  the CP1252 conversion is a TOC-only concern.
- A relayed room message does **not** know who wrote it: the concierge is the sender and
  the `[label]` attribution is text inside the body, which the wire then CHUNKS — so only
  the first chunk carries it. Measured on a live room: 215 of 2291 rows are attributed.
- Peers address each other by **claim slug**, not by session id or label. A `mentions_me`
  filter built from id and label scored 0 matches on 2313 live messages (D-009) — this is
  the one place the chat half reads the claims half.

## Git facts relevant to hooks (measured on git 2.50.1)

- `git diff --cached --name-only` **quotes** non-ASCII paths by default
  (`"b/caf\303\251.txt"`). `-z` emits raw bytes with NUL separators and no quoting.
- Default rename detection reports **only the destination** of a rename. `--no-renames`
  reports the source as `D` and the destination as `A`. A rename is a write to both paths.
- `diff.relative=true` in a user's config makes `--name-only` output **cwd-relative**,
  silently. Pin `-c diff.relative=false` when repo-relative paths are required.
- `git diff --cached` works with no `HEAD` (first commit); it diffs against the empty tree.
- A `pre-commit` hook runs with cwd at the worktree toplevel. `GIT_DIR` is NOT exported;
  `GIT_INDEX_FILE` and `GIT_PREFIX` are.
- **A partial commit (`git commit -- <path>`) builds a TEMPORARY index and points
  `GIT_INDEX_FILE` at it** (e.g. `.git/next-index-28362.lock`). Stripping `GIT_*` from a
  child git's environment therefore makes it read the real index and report files that are
  NOT being committed. Environment sanitizing is correct for a background scan and WRONG
  for a commit-time gate.

## Review conventions

- **Codex cannot build or test.** A Codex pass is never test evidence. Its findings become
  evidence only once a reproducing test is written and watched to fail.
- Report findings ranked by severity, and separate CONFIRMED defects from residual
  questions. A ranking resting on an adjective rather than a number is a hypothesis.
- Prefer naming the exact failing input and the resulting wrong behavior over describing a
  category of concern.
- A guard's review scope is every site that BYPASSES it, not the diff that adds it.
- If the prompt inlines code, review the inlined code. Do not assume unshown helpers are
  wrong; ask about them explicitly instead.
