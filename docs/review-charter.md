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

## Environment facts (measured, do not re-derive)

- macOS (darwin), zsh, Go 1.26. Default volume is case-insensitive but case-preserving.
- `pgrep`/`pkill` abort on non-ASCII patterns ("illegal byte sequence") and report BUSY as
  FREE when they do.
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
