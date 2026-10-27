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
    an ended session. The pid is never identity — but since D-025 it is AUTHORITATIVE for
    exactly one decision: a hook-driven `bye` ends a session only when no registered
    harness process (pid + kernel start time, `session_procs`) is still alive. On the
    roster it is diagnostic only.
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
    wired by default; the optional `busy` verb on `UserPromptSubmit` retracts the mark for a
    turn that runs no tool (issue #10), and measured on 2026-09-23 BOTH hooks run on a
    scheduled (`/loop`) turn, so a keep-alive ping resets `idle` (D-033).

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

26. **A STALE claim refuses a new overlapping claim exactly as a fresh one does (D-022),
    and a holder that has SAID BYE does not (D-026).** `scopeConflicts` tests
    `state='open'` and `owner not ended` — staleness has no bearing, positive `ended`
    does. What frees a scope: the holder's `release`, orphaning of an ended owner (which
    runs in `hello`, `sweep` AND, since D-026, first inside `claim`'s own transaction —
    and since D-033 inside `wait check`'s, the same statement), and `sweep --force`. **`bye` itself still touches no claim row** (invariant 12). The
    dry run excludes ended owners by the same predicate so it forecasts the claim, and
    prints a `note:` line for each ended holder it would displace — "acquirable after
    cleanup" is not "unreserved". The PreToolUse gate and the commit gate still read
    `state='open'` alone (hot path, deny is the safe direction); the deny names the plain
    `buddy sweep` as the remedy for a holder that has said bye. A refusal and the dry run
    both annotate an unrenewed claim by what the ledger knows about its HOLDER (D-090:
    `— STALE: claim unrenewed 3h, holder not seen 3h; it still refuses, so ask the
    operator` for a quiet, GONE or ended holder; `— claim unrenewed 34m; holder seen 5s
    ago; ... ask the holder: buddy msg <slug>` for a live one; nothing for a live
    holder's slot), one judgement every claim view renders, and a refusal prints the
    whole set even when there is only one conflict.

27. **`buddy whose <path>` reports the claim registers first, then `DIRTY IN` (D-023).**
    `CLAIMED BY` is invariant 14 containment and the relation the gate reads; `HELD UNDER`
    is a claim on a path inside the one asked about and reserves nothing about it — they
    are kept apart on purpose. The claim lookup consults NO FILESYSTEM (an `os.Stat` gate
    made a claim under a not-yet-created directory read as `(none)`), and reads ONE
    snapshot rather than matching ids and materializing them separately. `(none)` prints
    explicitly rather than being omitted — the defect was a silent answer read as "nobody
    has this" — and an unrenewed claim carries D-090's holder words and "still refuses",
    D-022's qualifier. A `.buddy/slot/` path consults no git (D-092).
    Neither register is a lock: dirty rows stay observations (invariant 10). It was NOT
    renamed to `buddy dirty`.

27a. **`fence.Field` returns `∅` for an empty result, overturning "an empty value stays
    empty" (D-023).** A blank column is ZERO tokens and D-017 guarantees ONE, so the next
    column slides into its position. Reachable from non-empty values, because `Line`
    strips non-printing runes: a label of one ESC fences to nothing. A literal `∅` is
    escaped first, as `␣` and `⏎` already are. The property holds for every input by
    construction and is asserted as one (`FuzzFieldIsOneToken`).

28. **A `chat_read` of a room the daemon NEITHER SERVES NOR REMEMBERS is refused, naming
    the rooms it serves (D-024).** An empty read now means a quiet room and nothing else.
    Both clauses are load-bearing: "not served" alone would refuse an archived room and the
    `@sent`/`@dm` pseudo-rooms; "no history" alone would refuse a configured room nobody has
    spoken in. The check runs only on an empty result (an equivalent-mutation guard: it buys
    cost, not behaviour). The SessionStart room name is still DERIVED from the label and the
    digest now SAYS so — it is `path.Base(worktree)` while the ledger is in the git COMMON
    dir, so a linked worktree's name is wrong; deriving it differently was cut because labels
    are a stable addressing namespace (D-013) and already-minted ones would not change.
    `servesRoom` is shared with the presence path, which has always done this check.

29. **A `bye` is fenced by the registered PROCESS, found by NAME (D-025).** Hook-driven
    `hello` and `beat` register the `claude` ancestor of the hook (walked up to 16 hops by
    exec path / argv[0], never by depth, never `p_comm` — which is the version string);
    `bye` removes its own registration and ends the session only when no other
    registered process is alive, judged by pid AND start time. Several registrations per
    session are legal (a second `--resume`), a session with none is UNBOUND and ends on
    any bye as before, a manual `bye` needs `--force` past a live registration, nothing
    auto-ends on `GONE`, and non-darwin platforms record no anchor.

30. **`buddy status` / `buddy who <target>` REPORT and grant nothing (D-027).** One
    renderer, every register the ledger holds about one session (roster row, claims held
    with scopes, dirty paths as observations, inbox, pid, pane) and an EXIT line that
    describes what the LEDGER would be left holding — never permission, never proof that
    killing is safe. `who` resolves through `ResolveTarget` (id, label, `s-<8hex>`, OPEN
    slug). Exit 0 for any report produced, 1 when none could be. There is NO `exit` verb
    and no exit-on-peer-request path, and none will be built (issue #20). `hello` warns
    once when a `--label` is worn by another live session; `msg` appends the recipient's
    outstanding idle report on a send, only when a current-incarnation idle row exists
    (D-016: absence says nothing), worded as the observation and not a prediction.

31. **Authority files are watched by mtime, announced ONCE per change on the next tool
    call, and the wording is an advisory (D-028).** The list is in the ledger (`buddy
    authority`, at most 8 configured paths; `CLAUDE.md` and `AGENTS.md` always), never git config on the hot path. The
    check is `os.Stat` mtime > session `started`; dedup key is (session, incarnation,
    folded path, mtime ns). It says the file on disk changed after the session started —
    not that contents differ, not that the session has not re-read them, not that main
    moved. No fingerprint, no `> last notice`, no git.

32. **The id register never parses prose, never reissues, and does not know the artifact
    (D-029).** `buddy ids seed <space> <n>` is mandatory before `take` (create or RAISE the
    ceiling, never lower); `take` is one transaction handing out the next contiguous block
    above the ceiling; there is no `return` verb; blocks outlive their session and are
    never swept. `status` answers in three registers only — reserved here, above the
    ceiling ("unreserved in this register", not "free"), at-or-below and unreserved ("not
    available for allocation", never "a hole").

33. **Coordination state is published as a claim's `--desc`, never a message, and there is
    no key/value store and no compel path (D-030).** The digest already injects every live
    claim into every session and re-claiming refreshes the description; the convention is
    an `orchestrator` claim (nothing reserved) carrying a bounded summary or a pointer. A
    claim has no `from` and is never read as an instruction; `pause` and `msg` remain the
    only control rows. Do not propose a store, a reserved slug, or auto-injection.

34. **Every verb answers `--help` in first position before it runs, and refuses an argument it
    does not understand (D-031).** Dispatch is a table carrying each verb's usage line; the
    flag spellings alone count as help (a bare `help` is a legal target or message word); an
    unknown flag or a stray positional after the flags is a fenced refusal, never ignored.
    `sweep --dry-run` is the real sweep rolled back inside its transaction, not a second
    computation of its predicates, and both runs name every claim they orphan. Do not propose
    a help scan across argv, or a forecast that re-derives the sweep.

35. **A send reports what the ledger holds about its recipient, never a prediction (D-032).**
    `msg` prints `queued for X — <observation>`: ENDED N ago (naming the open claims a plain
    `buddy claim` displaces, D-026), harness process GONE, last reported idle N ago (D-027),
    NOT SEEN past the stale mark, registered and not seen since, or last seen N ago — one
    arm, most-alarming-first — then how many earlier messages to X are still undelivered and
    the age of the oldest. `who` dates its INBOX line the same way. It never says "delivered",
    never infers busy, and does not refuse an ended target (D-013: `hello` revives the id).
    Do not propose a delivered receipt, a refusal on ENDED, or a wake.

36. **A parked session keeps its cache warm by DECLARING a wait and arming its OWN scheduler
    (D-033).** `buddy wait [--on <slug>]... [--until <dur>] [--note]` records one row per session:
    the awaited claims resolved ONCE to claim ids, a required deadline (default 3h, ceiling
    12h), and its own declaration id. `buddy wait check` is one tool call run by the session's
    own `/loop`. Its request refreshes the cache, its beat drains the inbox, and it prints one
    verdict: STILL WAITING with `next check in 50m (3000s from now)`, LANDED, EXPIRED, or NO WAIT.
    LANDED means every target is closed; EXPIRED means past the deadline; LANDED beats EXPIRED; a
    wait with no target is a timer that never lands. All are computed from the clock and the
    claims table at read time by ONE function every view renders. Nothing is stored but what an
    act did (`reason`) and beat's one-shot `told`, keyed by the declaration and marked after the
    write.
    **Pacing is from the check itself, not the ledger's cache clock.** A check runs before its own
    beat, so the ledger's newest observation is the previous request; pacing from it pinged twice
    a period. Measured: warm at <= 3,602 s, cold at >= 3,633 s; self-paced wakes fire 0-58 s late
    (they round up to the minute); 9 of 11 one-hour wakes were cold. The observation is used only
    for the tier (no keep-alive on 5m or both tiers) and for the lagged read/COLD WRITE counts line.
    `wait check` speaks ONLY for `$CLAUDE_CODE_SESSION_ID` (no `--session`). `wait check`
    orphans ended holders first, as `claim` does; `wait` REFUSES a claim whose holder said bye
    (a refused declaration rolls back, so it must not explain itself with an orphaning). A
    check closes a finished wait only AFTER its verdict is written, and a generated `buddy wait
    --on` command names only slugs that survive the fence unchanged.
    A wait reserves nothing and refuses nothing, is never inferred, and is never closed early for
    a live session. No new hook line, no wake, no chat, no dollar figures in any output. Do not
    propose a buddy-side timer, pane injection, a headless `--resume` ping, a Stop hook that
    refuses to end the turn, inferring a wait from a refusal or idleness, or a stored verdict.

37. **`hello` drains the inbox into the SessionStart digest, inside the context cap (D-034).**
    Only a hook-driven `hello` drains; a hand-run one (`--session`, no hook JSON) prints the
    count and marks nothing, because its output reaches whoever ran it and not the session. It
    uses beat's header, `fence.Line` and write-then-mark, and it is bounded twice: by one beat's
    20 messages / 8 KiB, and by the room the rest of the digest leaves under `helloBudget` (9,000
    bytes for the whole digest). Claude Code documents a 10,000-character cap on hook output
    injected into context, with a preview and a file path past it (documented, not measured),
    and an over-cap digest would hide the claims list. Oldest first, stopping at the first
    message that does not fit; the rest are counted (`N queued message(s) not shown here`) and
    stay queued. It does not wake a session already at its prompt; that is still D-027's "no".
    Do not propose dropping either bound, a drain on a hand-run hello, or skipping past a
    message that does not fit.

38. **A resource slot is a claim on `.buddy/slot/<name>` (D-035).** For a capacity-1 shared
    resource (the test box, a serialized tier, `main` during a land), a claim on a path under
    the reserved `.buddy/slot` prefix IS the reservation: exclusive, freed by `release`,
    queued on by `wait --on`, counted by `who`. No file exists there. A scope is a slot by
    invariant 14's containment on invariant 13's fold. The only code is presentation: a
    refused claim and a `--dry-run` print one fenced `SLOT:` line before the wait suggestion.
    No gate reads the prefix, no new scope kind, no counted capacity until a counted resource
    is measured contended, and a holder that says `bye` still stops refusing (D-026). Do not
    propose a `slot` verb, a capacity column, or a job-lifetime binding without that
    measurement.

39. **The SessionStart claims list fits the digest budget, ahead of messages (D-036).** The
    lines after the list are rendered first and the list gets the rest of `helloBudget`. A list
    that fits prints whole, in ledger order. One that does not shows the session's own claims,
    then others oldest first, stopping at the first that does not fit (never skipping ahead),
    and counts the rest (`N more live claim(s) not shown here, K of them YOURS`). Messages take
    what the list leaves. No slug is ranked (D-030 reserves none, so no `orchestrator`
    priority), and the gate reads the ledger, so a hidden claim refuses like a shown one. Do
    not propose ranking by slug or a hidden-claims tier without its own bound.

40. **`Open` refuses a ledger stamped newer than the binary (D-037).** `user_version >
    schemaVersion` is `ErrLedgerNewer` in `Open` and in `migrate`'s locked re-check, naming
    both versions and the fix. It is "exists but unreadable", so the gate DENIES (invariant 3),
    never "no ledger". Nothing migrates down, and no read-only open or `doctor` verb until one
    is needed.

41. **The Stop hook records each session's base; the views print where it stands against main
    now (D-038).** `idle` stores `HEAD` (a full hex object name only) in `session_base`
    (schema 10) under the context footprint's fence: incarnation, newer-or-equal turn. Never on
    `beat`. The roster (live rows) and `who` print `base <sha8> (N ahead, M behind main, age)`.
    The lag is computed at read time against local `main`, else `master`, one `rev-list` per
    distinct base. An observation that refuses nothing. There is no `NOT ON MAIN` flag: an
    ancestor test fires on every session with unlanded work, and the two counts carry the
    orphaned-base case. Do not propose storing the lag, sampling on `beat`, or a flag that
    fires on unlanded work.

42. **`msg` names the harness's wake address for a quiet recipient; buddy still wakes nothing
    (D-039).** Measured: a harness SendMessage wakes an idle session and is recorded as a
    marked peer message (`isMeta`, `origin.kind="peer"`, host-verified sender pid, plus a "not
    typed by your user" note), never as the operator's turn. A body cannot forge the wrapper,
    because the host escapes the tag. For a quiet target (idle, stale, or registered and not
    seen since) with exactly one live registered process whose `/tmp/cc-socks/<pid>.sock`
    exists as a socket, `msg` prints `SendMessage to "uds:…"` with a text ending "run buddy
    inbox" that names the message id and the second (D-059, see 61). The body stays in the
    ledger. One observation serves the
    result line and the wake line. No send from buddy, no pane injection, no broadcast wake, no
    name mapping. Do not propose buddy calling the harness, carrying the body in the wake, or
    deriving the address from anything but a live, birth-time-checked registered pid. The one
    exception is the separate opt-in `buddy-adapter`, which writes wake input only to the worker
    it launched (D-074, see 75), and no socket wake is ever offered for an adapter recipient (D-076).

43. **`busy` drains the inbox into the prompt that opens a turn (D-040).** UserPromptSubmit
    runs beat's drain (the same bound, fence, and order of write first, mark after) as ONE
    `hookEventName: "UserPromptSubmit"` document. It prints nothing when nothing is queued, and
    it carries only the inbox. Measured: an operator's `ok` into an idle lane opened a turn
    without the approval queued for it, and the lane saw the approval only because it chose to
    run `buddy inbox`. It wakes nothing. Do not propose moving beat's notices onto the prompt,
    or claiming a SendMessage wake fires UserPromptSubmit (unmeasured).

44. **`msg`'s wake address rides its result line (D-041).** One send answers on ONE line. The
    D-039 address used to be a second line, and an orchestrator that read `msg … | head -1`
    (measured, 2026-09-24) lost it, so an idle lane sat unwoken until the operator typed into
    it. Do not propose moving the address back onto a line of its own. It is ALSO written to
    stderr as one line (D-052): a sender that discarded stdout as a receipt (`>/dev/null`) lost
    it, measured. Stderr is empty when there is no wake; a `2>&1` reader sees it twice.
    `buddy sent` names the same wake on each still-queued recipient that the same
    `wakeClause` says needs one. No "after N minutes" threshold: `msg` prints it at age zero.

45. **A SHARED claim may overlap other shared claims, and nothing else (D-042).** One mode term
    in the one conflict scan: an overlap conflicts unless both sides are shared. The gate returns
    an exclusive blocker first, and admits an edit under only shared holds when the caller has
    its own claim covering the path (claim-first). A refresh takes the mode it is given.
    `--shared` is refused on any scope overlapping `.buddy/slot`. "Shared" is not "append-only":
    the gate sees paths, not diffs, and a concurrent read-modify-write can lose an edit, which
    is stated rather than solved. Do not propose an open-door shared hold, a standing owner that
    yields to nobody, or per-scope modes.

46. **A correction names what it corrects, goes to the original's audience, and withholds
    nothing (D-043).** `msg --supersedes N`: only the sending SESSION that sent N (recorded
    `sender_session`; `''` = operator, NULL = unknown and uncorrectable) may correct it, and
    only to N's own target or broadcast snapshot. Both messages are delivered; the links sit
    between `#id` and `[sender]`, so no text can forge one. `buddy sent` reports "delivery
    recorded" / "queued" / "expired undelivered" per addressed session. This deliberately
    revises D-032's no-"delivered" rule, for a report of an OBSERVED delivery row. Do not
    propose withholding, retraction, read receipts, or ownership by label or `--from`.

47. **A repository git refuses, with no ledger in it, is feature-off (D-044).** On a git
    refusal (not "not a git repository") with a `.git` above the path, the nearest `.git`
    (through `gitdir:` and `commondir` for a linked worktree) is checked for `buddy.db` without
    git. Only a positive "does not exist" allows. A present ledger, an unreadable path, an
    unparsable pointer, or `GIT_DIR`/`GIT_COMMON_DIR` still deny. Measured: an empty `git init`
    in `/private/tmp` denied every write under `/tmp` fleet-wide via "dubious ownership".
    A cwd that does not exist at all (exact ENOENT) is not a refusal: since D-102 it is
    discovered from its deepest existing ancestor first, and this check applies there.

48. **A message says what kind of claim it carries, as the SENDER's declaration (D-045).**
    `msg --lead | --measured "<scope>" | --relay <source>`, at most one, rendered between `#id`
    and `[sender]` as `declared …` on the row itself. The note is fenced and then quoted, must
    show once rendered, and is capped on its rendered bytes. No flag renders as before. The drain
    bound counts rendered lines. It verifies nothing and confers no authority. Open limit:
    `--relay` does not carry the relayed figure's scope as a field. Do not propose a mandatory
    kind, a kind inferred from the body, or combined kinds.

49. **A session learns the verbs from the digest and a skill, never the chat server (D-046).**
    `hello` prints one fixed line pointing at `buddy --help` and naming the easy-to-miss verbs.
    `skills/buddy/SKILL.md` (user-level, copied into `~/.claude/skills/`) says how they fit.
    A test checks every verb and flag either one names against the usage table. The claims list
    reserves the queued-message count line and its own remainder at their rendered worst case.
    Do not propose the MCP `instructions` field (chat can be absent) or a full verb list in the
    digest (every start pays for it).

50. **A wait rides hook output inside a bound (D-047).** hello and beat's LANDED notice render the
    awaited claims through `targetsWithin` (1,024 bytes, in order, stopping at the first misfit,
    the rest counted with `buddy status` or `buddy ls`). A predecessor's re-declaration too long
    to print whole is not printed as a command. The deliberate views still list every target.
    Do not propose a cap on `wait --on` targets for this; the bound is on the rendering.

51. **A base that carries a commit main DROPPED says so, found by main's reflog (D-048).** Dropped
    means reachable from a former tip of main (the reflog walk plus the files backend's old
    column) and not from the current tip, which is read AFTER the reflog. Only a base's commits
    ahead of main are checked, so healthy unlanded work never prints it. Read side only (roster
    and `who`); silence means no drop the reflog records. Do not propose the ancestor test
    (every busy session fails it) or a notice on `beat` (it forks no git).

52. **One long run closes out several sessions on existing primitives (D-049).** One integrator
    holds `.buddy/slot/<run>` and `.buddy/slot/main` from forming to landing. Riders declare
    `wait --on <run> --ready <commit>` (the rider's word, resolved to a commit, never checked for
    ancestry, because the integrator rebases). `who <run>` lists READY and not-ready riders with
    ages and notes. `release --outcome pass|fail|aborted [--note]` rides every waiter's LANDED, and
    a rider is told when a release carried no outcome or the claim was orphaned. Membership is the
    integrator's `--desc`. Do not propose a batch table, a hold timer, OVERDUE, auto-eject, or
    "first GO wins"; all were designed and cut by Codex and Fable passes. The skill must teach
    every verb and flag in the usage table, or exempt it with a reason (a hermetic test).

53. **`scripts/install.sh` is the one install and upgrade path (D-050).** It rebuilds every buddy
    binary the machine runs, found by where it is run from (`~/bin`, the launchd daemon's plist
    program, the checkout's `bin/`), runs each once, installs the skill, `kickstart -k`s the
    daemon, runs setup-clone, and REPORTS hook wiring without editing settings. Every sub-step's
    status is the install's. Do not propose editing `~/.claude/settings.json` from it, or `cp`
    for the binaries.

54. **`buddy init` says whether this repo's main languages have a language server (D-051).** Per
    language that is at least 10% of tracked source: the server on PATH (absolute entries,
    executable by the caller) and `<plugin>@claude-plugins-official` enabled in user, project and
    local settings, merged per full id. It prints `configured`, or `NOT CONFIGURED` with a fix
    line that fixes it (PATH steps included, `--scope` named, `/reload-plugins` or a restart).
    Advisory: it never fails init, never blocks (FIFO settings, a git wrapper holding the pipe),
    never prints before "ledger ready", never runs in a hook, installs and edits nothing.
    `scripts/startup-report.sh` measures the window to the first edit, counts only. Do not
    propose a SessionStart line, auto-install, `claude plugin list`, a committed code map, or
    counting untracked files.

55. **A test binary that outlives its `go test` is ended from outside (D-053, #43).**
    `internal/testguard.Arm()` opens `internal/cli`'s TestMain: an in-process `Getppid` poll
    exits a healthy orphan with 3 in about a second; a watchdog process (the same binary
    re-exec'd, pipe EOF for the target's exit, pid + kernel start time for identity) ends one
    still orphaned three polls later, or at twice `-test.timeout`, with a symbolized `sample`
    to `$TMPDIR`, SIGQUIT, then SIGKILL, and reports on stderr LAST (it may be the dead pipe).
    MEASURED: the timeout panic exits fine on a dead stderr; a stop-the-world waiting on an
    unpreemptible goroutine reproduces #43 and runs no Go code at all (so no in-process wall
    can fire); Go drops SIGXCPU and darwin sent no SIGKILL at the hard RLIMIT_CPU; `go test`
    binaries have no symbol table. Do not propose RLIMIT_CPU, an in-process AfterFunc wall,
    or `SetTraceback("crash")` (it would SIGABRT every panicking test). The cause of #43's
    wedge is not known; the watchdog's sample is what will name it.

56. **A park with no claim to name keeps warm on a TIMER wait, and is told so before it parks
    (D-054, #44, #45).** MEASURED over one fleet's week: 3 of 129 sessions declared a wait,
    145 of 149 wakes past an hour were cold and 0 of 19 at 50–60 minutes; the costly parks
    were a session's own hour-plus run (the integrator of a shared run included: `wait --on`
    refuses your own claim) and a lane waiting for its orchestrator. The skill's "Before you
    park", `hello`'s help line naming `--until` alone, and a timer declaration's third line
    (clear it when something else wakes you first; `--session <id>` named when declared for
    another session; no "stop the loop" off the hour tier) teach it. A check is one tool call
    inside a turn of about three requests (median 3, mean 4.05), and USAGE's cost table is
    priced that way. `scripts/wake-report.sh` re-measures, counts only. Do not propose a
    pid/background-task wait target, `wait --on` your own claim, a Stop-hook nudge, inferring a
    park from idleness, or "always keep warm" (past four hours the fleet's parks broke even).

57. **A session that DECLARED a handoff size is told, once per turn, when it has grown past it
    (D-055, #46).** MEASURED over one fleet repo, 15 days: 4 orchestrator successions, 3 begun
    by the operator noticing the size. `BUDDY_HANDOFF_AT` (parsed as `BUDDY_CONTEXT_WINDOW` is;
    unset or unparseable is off) is set on the one process it applies to. When the LAST
    OBSERVED prompt for the current incarnation is at or past it, `busy` (UserPromptSubmit)
    puts one line ahead of the inbox in its single document, on every turn-opening prompt,
    even with nothing queued. The line names the size, the turn's age and the threshold, and
    points at the skill's recipe for BOTH arms: a coordinator's "Hand off before you are full",
    a lane's "Landing through an orchestrator" step 6 (D-064: the size may be declared
    fleet-wide in the settings `env` block, measured to reach the hook, so lanes hear it too,
    and a lane opens no successor). `install.sh` reports whether it is set there. The drain (D-040) is unchanged. It is an
    observation (invariant 10): it refuses, reserves and schedules nothing, and infers no role.
    Do not propose firing it on `beat`, a role or reserved slug, a stored "told" flag, or
    reading the transcript from the prompt hook.

58. **A coordinator opens its own lanes with the harness; buddy launches and ends nothing
    (D-056, #47; #22's decline stands).** MEASURED from sessions in auto mode: `herdr tab create`
    + `herdr agent start … -- -n <l> --session-id <uuid> "<brief>"` opens an interactive lane
    (it honours `--session-id`, `--env` reaches the process, it starts in MANUAL permission mode,
    and `herdr pane close` runs its `bye`); `claude --bg -n <l>` starts a background lane but
    ignores `--session-id`; `claude agents --json` lists every session. The skill's "Running a
    fleet" teaches it: open only as many lanes as the operator allowed, and never with a more
    permissive mode than the launcher's own. The brief rides the launch; `msg` waits for the
    lane's hello (a premature one is refused, loudly); ending a lane is the operator's act; the
    successor handoff runs on a file named in the `orchestrator` claim, `msg` + wake, release.
    Do not propose `buddy spawn`/`exit`, or `buddy expect` (a brief queued before hello). The
    separate opt-in `buddy-adapter` is the one recorded exception, for the worker it launches
    from typed argv (D-074, see 75); `buddy` itself still launches and ends nothing.

59. **The test suite never runs with git's local environment (D-057, #49).** MEASURED (git
    2.54): a push from a linked worktree hands the pre-push hook an ABSOLUTE `GIT_DIR`, and the fixtures'
    `git -C <tmp>` inherited it and made the real repository bare with
    repositoryformatversion=99. `git rev-parse --local-env-vars` (united with a fixed copy) is
    unset by pre-push before the tier, by check.sh first thing, by sourcing lib.sh, and by
    `testguard.ScrubGitEnv()` inside the TestMain of every package that imports os/exec and
    names "git" (a gate enforces it, and a second keeps the four fixed copies of the list
    equal). Do not propose stripping `GIT_*` wholesale, or relying on the hook alone.

60. **A `claude --bg` lane anchors at its own process, and its row names no pane (D-058,
    #50).** MEASURED (2.1.283): the lane retitles its argv[0] to the single string
    `claude bg-spare` (exec path is the versioned file), so the walk passed it and anchored at
    the shared `claude daemon`. The lane is the hook's direct parent, and it runs in the
    DAEMON's environment, so its pane (and any variable set on `claude --bg`, including
    BUDDY_HANDOFF_AT) is whoever started the daemon's. The walk also matches an argv[0] whose
    first word is exactly `claude`; an exact name still wins, and the walk stops at the first
    match, so an interactive session in its measured shape (the hook's direct parent) anchors
    where it always did. A retitle-only anchor records no pane. A lane live ACROSS the upgrade
    keeps its daemon registration and pane until the operator's `bye <id> --force` (pinned by a
    test; accepted, not fixed). Do not propose anchoring at the daemon with a flag, reading
    CLAUDE_JOB_DIR, or a looser (path or prefix) retitle match.

61. **A wake's text names its message and the second it was named (D-059, #51).** MEASURED:
    Claude Code drops a peer message identical to the previous one from the same sender
    (SendMessage still reports success; a delivery notice says so minutes later), so one fixed
    wake text lost a second wake to the same lane. The text is `buddy mail #<id> is queued for you (<HH:MM:SS>): run
    buddy inbox`: the send's id (a broadcast's own on a `sent` row) and the local time the wake
    was named. Two suggestions for one message in one displayed second are still identical;
    that residual is accepted. No body, sender or kind. Do not propose a fixed text, carrying
    the body, or buddy detecting or retrying a dropped wake.

62. **The Stop hook cannot see its turn's reply; it names its turn, and the next prompt records
    the footprint (D-060, #52).** MEASURED (2.1.283, interactive): the turn's final usage record
    lands ~96 ms after the Stop hook's process starts (p50 96.2, p90 98.0, max 108.3), and `idle`
    is done by ~26 ms. It was missing at every Stop measured. `idle` reads once. If a user record
    is at or after the newest usage record, the reply is pending: no footprint, the mark dated now
    and fenced by that record. A Stop whose `prompt_id` turn is on disk behind a newer prompt
    records nothing. `busy` re-reads, fenced by incarnation start, before D-055's note, so a
    resting prose session's footprint is normally one turn old and errs colder, never warmer. Do
    not propose waiting in `idle` for the reply (1 of 20 within the 100 ms budget), a detached
    writer, reading `last_assistant_message` (text, no usage), or a clock threshold for freshness.

63. **A `-race` forkExec can leave its child stuck before exec, and the watchdog ends it (D-061,
    #53).** MEASURED: the child spins in ThreadSanitizer's slot lock (`forkAndExecInChild` →
    `__tsan::SlotLock` / `TraceSwitchPartImpl`), held by a parent thread at fork, and never
    execs. The parent waits in `readlen` to the timeout. It is `-race` only (toy: 8/8 forkers hung;
    0 of 356k forks without -race), not git, PATH or the hook. The child keeps the watchdog's pipe
    (close-on-exec acts only at exec), so the watchdog has a holder arm. Target gone and pipe held
    means it ends the target's same-binary children, orphaned, same start time, in its own
    process group, seen while the target lived, never itself. It holds its executable's fd so
    the sample symbolizes after go test deletes the binary. Do not propose a retry loop, a
    timeout knob, a box-wide process scan, or treating D-053's stop-the-world toy as the cause.

64. **The `-race` forkExec hang is golang/go#79804, fixed in Go 1.26.5 (D-061, #55).** `go.mod`
    pins `toolchain go1.26.8`; under `GOTOOLCHAIN=auto` every build, CI included, uses it.
    Do not propose a retry loop, a timeout knob, or dropping the `-race` legs for it; if it
    recurs, the toolchain in use is the first thing to check.

65. **Landing through an orchestrator is a recipe in the skill, over existing verbs (D-062).**
    One fleet repo ran the orchestration plan's increment-2 lifecycle live, in prose, in its own
    skill, and MEASURED what the first version left unsaid: a lane sat idle three hours for a
    "landed" message that never came, and four assignments and red reports reached nobody for
    about fifty minutes because the sender discarded `msg`'s result and its wake line. The
    skill's "Landing through an orchestrator" teaches both sides: the orchestrator holds a
    durable `orchestrator` claim (its description the published note, D-030) AND a per-run
    claim on `.buddy/slot/<run>` and `.buddy/slot/main` (D-049), formed again the moment one is
    released; a lane waits on the RUN, never on `orchestrator`. READY is `wait --on <run>
    --ready HEAD` plus a message carrying what the ledger does not hold; LANDED is the release's
    outcome plus words that end the lane's work. The orchestrator owes an answer to every READY,
    every `msg` result read for its wake line, the landing said in the same turn, a red routed to
    its lane, the new main to every lane still working, and `who` on every lane before handoff.
    Repo-specific parameters (tier, review, own checks, worktree names, id spaces) stay in the
    repo's own skill. Do not propose `buddy task` or any task table (every fact has a row:
    claim, `ready_sha`, `outcome`, inbox), a LANDED that also means "you may exit", or naming
    the fleet repo.

66. **A holder hands an open claim on with `release <slug> --to <target>`, keeping its id (D-063).**
    Release-then-claim mints a new claim id, and a wait is resolved once to a claim id, so a
    successor re-claiming an in-flight run landed every rider with no outcome and dropped their
    READY (measured, 2026-09-29). The transfer moves owner and incarnation in one transaction and
    keeps everything else; it refuses a sender not live under its incarnation, an ended or
    re-registered recipient, a recipient waiting on the claim, and any overlap the recipient could
    not have claimed. No consent step, no recorded previous holder: cut, see the record. Do not
    propose release-then-claim or `--outcome aborted` as the handoff of a run that carries on.

67. **`status` and `who` print a JOBS line: the shells still running directly under the
    session's registered harness process (D-065).** Measured 2026-09-29: an outgoing
    orchestrator said it "holds nothing, close me" with a gate script and a 42-minute watcher
    still running as its children, and EXIT (the ledger only) was read as the session. A Bash
    tool call is `/bin/zsh`, a direct child of claude in its OWN process group, so the line reads
    `kern.proc.all` by parent (D-061's cut of that scan was for a different purpose), counts
    shells only (MCP servers, gopls, caffeinate are not), excludes the caller's ancestor chain,
    and re-identifies the anchor by its registered start time in the same table (a reused pid
    must not lend its shells). Pids and ages only, never argv. "Cannot say" (no live registered
    process, a platform whose table is not read, the anchor not the registered one) is never
    "none". It refuses, ends and grants nothing. Do not propose ending, signalling or
    reparenting a session's processes from buddy (D-027, D-056).

68. **`claim` refuses a `--scope` that holds a comma; every listing of a claim's scopes goes through
    `scopeList` (D-066, issue #56).** Measured: 78 claims by 30 of 99 sessions on one fleet were a
    comma list granted as one literal path, covering none of the files; 0 tracked paths there or
    here contain a comma. Refused whole in `cmdClaim`, before the ledger and the dry-run branch.
    The fix command is printed only when it pastes back as meant: no part the fence alters, none
    `NormalizeScope` refuses, and not cut short. `scopeList` renders each scope with `fence.Field`,
    joined by `, `, whole items, and always shows the first. `NormalizeScope`, the store and
    `release --scope` still accept a comma, so legacy rows can be narrowed and released by name.
    Do not propose splitting on the comma, a warning that exits 0, or moving the refusal into
    `NormalizeScope`. Comma look-alikes (U+FF0C and the rest) and spaces are accepted: neither was
    measured as a mistaken list.

69. **Codex participation begins with the patch gate and native thread identity
    (D-068).** Authorized driver/worker roles are in `AGENTS.md`; constrained
    reviews remain read-only through `codex-review.sh`. Local `apply_patch` checks
    every affected path, including both ends of moves, before one allow/deny
    result. Malformed or unsupported routing refuses. `CODEX_THREAD_ID` is the
    locally observed native identity; ordinary explicit overrides retain
    precedence, but `wait check` requires native identity and rejects a conflicting
    override. Codex hooks must not borrow a Claude ancestor or identify a logical
    session by app-server pid. Unfenced Codex lifecycle hooks refuse (D-069 below).
    As of D-068, production lifecycle integration and a mixed fleet were unqualified, and it
    introduced no task table or structured adapter schema; D-073 (see 74) since refuses
    shell-routed patches where a ledger governs and checks every spelling of a target, and the
    plan's status table says what is qualified now. `AGENTS.md` joins `CLAUDE.md`
    as always watched, with eight additional configured paths.

70. **Codex lifecycle observations require the adapter's captured incarnation
    (D-069).** `lifecycle begin` returns `buddy.lifecycle.v1` JSON. Live registrations
    refuse begin; an ended registration requires the exact previous token before
    a new one is minted. Every event checks the captured token transactionally;
    no handler may look up the current token to repair a late event. Native
    `lifecycle hook --incarnation` maps Stop to idle and SessionEnd to end, emits
    `{}`, drains no inbox, and rejects subagents. An explicit `hello --session`
    must still classify supplied hook input before writing; it cannot turn a
    delayed hook into a manual registration. Unfenced Codex hello/beat/busy
    refuse alongside idle/bye. Manual local/operator paths retain their authority.
    The adapter must durably capture receipts and serialize events within an
    incarnation; failed receipt publication is an uncertain outcome, not a reason
    to retry begin blindly. This increment added no schema change, process binding, worker
    launcher, automatic wake or inferred completion (D-070 and D-074 later add the opt-in
    adapter; D-076 lets a PostToolUse renew the claims covering the paths it observed). Installed-hook qualification with a
    local deterministic provider is distinct from production/provider/mixed-fleet
    evidence; consult the current plan receipt for what actually ran.

71. **Minimal structured adapters preserve captured identity and separate
    delivery stages (D-070).** `buddy.adapter.v1` exposes bounded ledger
    projections and incarnation-bound peer inbox operations. Schema 15 adds
    message identity and receiving endpoint metadata, not tasks. Legacy drains
    cannot consume adapter mail. `buddy-adapter` is a separate control binary
    for one worker it launches (startup is argv; coordinator requests are inspect,
    reconcile, send and interrupt; approval is the separate operator path); native IDs,
    raw JSON approval IDs and result references stay exact. Its journal records intent before wake input,
    refuses concurrent ownership and never repairs a missing receipt by looking
    up the current token. Native turn result, body delivery, acknowledgment,
    completion, validation and landing remain distinct. Only a separate
    operator input descriptor may answer pending approvals; peer/coordinator
    input cannot. Codex checks effective gate hash/trust; Claude checks bounded
    gate settings and the pinned built-in Mod profile (D-071). No install, trust
    mutation, existing-session attach or task table. See the plan receipt for
    actual executed and native qualification rather than inferring it here.

72. **Claude admission uses a pinned native built-in Mod profile (D-071).**
    `hooks.mods: "builtin-only-<version>"` selects the separately checked profile
    matching the exact declared and observed native version. The admitted pairs are
    the committed admission records (D-079), not this GIVEN; each new build joins
    only after its own qualification. Start and resume reject a cross-version/profile pair;
    no semver range or automatic conversation upgrade is admitted. Legacy
    `no_mods` evidence refuses. The adapter checks effective settings, reloads
    plugins in its owned child before input, requires exactly the two expected
    native built-ins with their source/path identities and no reload errors,
    then checks effective settings again. Missing/additional inventory refuses.
    The agents-md built-in contains a Mod, so empty optional configuration is
    not evidence of Mod absence. This is a startup snapshot, not continuous
    attestation, plugin startup isolation or a read-only diagnostic. Native
    initialization/reload precedes inventory admission. Actual native claim denial is a
    separate check; optional/managed Mods outside the profile and later native
    configuration changes remain unqualified.

73. **Offline compatibility diagnostics do not open the source ledger with
    SQLite (D-072).** `buddy diagnose` reads a bounded stable main-file image
    through read-only descriptors and opens only a private immutable copy.
    Nonempty WAL/rollback journals, unreadable inputs and changing files report
    uncertainty; they never trigger checkpointing, recovery or migration.
    Receipt comparisons never return or repair an incarnation token. Native
    commands, hooks, plugin reloads and inbox drains are not run. Supplied native
    version/trust captures are historical snapshots, even when their input hash
    matches; current installed version and enforcement remain unknown. Exit 0
    means report produced, not readiness or authority. This is the concrete
    read-only consumer deferred by D-037, separate from `adapter inspect`.

74. **The gate checks every spelling a write can land on, decides jurisdiction before
    identity, and adds a nested repo's ledger to the home one (D-073).** Shell-routed
    `apply_patch`/`applypatch` (command position, Codex's in-process engine) refuses wherever
    a ledger governs, naming the tool; it is never parsed and adjudicated, because the
    payload omits the workdir. An identity conflict refuses only where a home or target
    ledger governs; read-only tools pass. Lexical and physical spellings, each followed and as
    the link entry, plus a dangling link's target, are all checked; any held refuses. A home
    claim covering a path inside a nested repo still refuses. A target repo's pause applies to
    a cross-repo edit. Do not propose parsing shell-routed patches, refusing identity
    conflicts machine-wide, or checking one spelling.

75. **`buddy-adapter` is the one opt-in exception to "buddy launches, wakes and ends nothing"
    (D-074).** A separate binary, acting only on typed argv from the operator or an
    already-authorized coordinator, on the one worker it launched, over that worker's own
    stdio. Ledger rows, message bodies, chat, claim descriptions and worker output start,
    wake and approve nothing. `buddy` gains no launch, wake or exit verb. Do not cite D-056
    or D-039 against the adapter, or the adapter as precedent for a `buddy` verb.

76. **Recovery admission is exact-version and terminal-evidence-bound (D-075).** Codex
    resumes the captured thread; Codex `reconcile` is withdrawn until a bounded history
    read is qualified (D-077). Claude resume
    needs every journalled turn's terminal result for its exact input (`completed`,
    `aborted_streaming`, `aborted_tools`) and idle; anything unfinished or unknown refuses
    before ledger refresh and launch, and SDK history never substitutes. Manual compaction
    is admitted only on versions where its boundary and local result were measured. D-075
    supersedes D-070's "Claude resume is unsupported".

77. **Adapter mail is its own register in the legacy views (D-076).** `who`/`status` print
    an `ADAPTER` line beside `INBOX`; a send counts adapter mail separately and never claims
    a legacy drain for an adapter recipient; no socket wake is offered to one; a legacy
    correction of adapter mail refuses. A lifecycle PostToolUse renews only claims covering
    the paths it names. `hello --session` still refuses non-hook stdin and names `</dev/null`.

78. **The adapter owns its worker's process group and validates the operator descriptor
    before any child (D-077).** `--operator-fd` must be open, `O_RDONLY`, a FIFO/char
    device/regular file, and is marked close-on-exec before the version probe; anything else
    refuses with no child and no journal. Close ends the worker's process group (TERM, then
    KILL as soon as the leader exits or after 2 s at most — the grace is the leader's, so a
    tool can get TERM and KILL back to back) while its leader is unreaped, and closes the
    stdout reader; INT/TERM/HUP run
    Close. A refusal before any native byte is `not_submitted` and retryable; a write that
    may have happened stays uncertain. An unrecorded event makes state unknown, never
    `observed_absent`. Claude gate settings use exact case-sensitive keys, no duplicates, a
    matcher that is `""`, `*` or `.*`, or a literal list (plain or `^(…)$`-anchored) naming
    all three tools, `timeout` 10-600 s. Executables are absolute,
    resolved once, identity-checked before exec. Do not propose discarding oversized
    frames, reading full Codex history on the live transport, or treating a free journal
    lock as proof the worker exited. **Resume is fail-closed on an ownership record
    (D-077, 2026-10-07 continuation, audit A-02):** every spawn publishes `<receipt>.proc`
    (intent before the child, the leader's pid/start/pgid/sid before the first native
    byte) through a single-use launch authorization consumed before `cmd.Start`; resume
    refuses with no record, a record bound to another journal/receipt/identity, an
    intent-only record, or while the recorded adapter, leader or group lives (`kill(-pgid,
    0)`, ESRCH alone is absence); `--wait-prior` is a bounded passive re-check. `.start`
    fences only Start; the flock plus intent-before-spawn fence every spawn. **Narrow
    contract (D-077, the operator's choice 2026-10-11):** descendants that left the group
    are sampled every second (one `kern.proc.all` read; parentage only, every parent
    re-checked by pid and start time after the read), recorded in `.proc`, captured again
    before stdin closes on every adapter-initiated shutdown, reported at close (`ended`,
    `survivors`, `unknown`; `unattributable` is always "unknown") and on one stderr line,
    and NEVER signalled; resume refuses waitably while a recorded one lives, and outright
    on overflow (256), an unresolved discovery error or a malformed section. Excluded, do
    not re-propose: record rollback detection (invariant 6), killing what the adapter
    cannot pin (a pid and start-time re-read is not a pin), `--end-prior`. Setsid waits on
    native qualification Q1/Q3.

79. **An adapter-launched worker's claims renew from COMPLETED native writes, through the
    controller (D-078).** No PostToolUse joins the adapter's gate contract (that route is a
    fallback only). The backend's reader offers paths to a bounded mailbox that never
    blocks (256 paths / 64 KiB; overflow drops renewal candidates with a coverage gap, never
    control traffic). The controller applies them via `buddy lifecycle renew`, which checks
    the captured incarnation in the same transaction and renews only covering claims: no
    last_seen, no idle, no lifecycle state, never the approval gap. First observation at
    once, then at most once per path per 30 s; idle/end apply what is due first. Claude
    renews only a matched non-replay main-thread tool_result with explicit `is_error:false`,
    or on 2.1.291 and 2.1.299 a Write whose omitted `is_error` carries the measured
    create/update witness (Edit omission stays unqualified); Codex only a completed `fileChange` in the active turn (every path and move
    destination). Bash, attempts, denials, subagents, replays and other launch generations
    renew nothing; correlations never cross a resume. Do not propose renewing through
    `BeatLogical` (it erased idle), one event per tool result (it blocks the reader), or
    renewal from attempts or patch text. `lifecycle beat --path` is repeatable.
    `tool_result_meta` (D-078 addendum, R', agreed by Fable and Codex): a frame whose list is
    not objects with string ids each naming a tool_result of that frame is unexpected; for a
    result, two entries are unknown; one entry with only `id`, `non_execution_kind`,
    `permission_decision` (string `decision`, optional string `source`, `reason_type`) is
    UNEXECUTED when its kind is `permission-rule` or `user-rejected` with no decision or
    `reject` and the result carries no completion signal (explicit `is_error:false`, or an
    omitted `is_error` beside the Write success witness), PROCEEDS to the is_error/witness rule when
    it has no kind and decision `accept`, and is otherwise unknown (renew nothing, coverage
    unknown). Do not propose reading an entry's presence as "not executed" (2.1.299 puts one
    on executed results) or treating `accept` as completion. The field is `@internal`;
    exact-build admission is why relying on it is acceptable.

80. **Native admission is a committed exact-artifact record, embedded in the adapter
    (D-079).** `internal/harness/admissions/<harness>/<version>-<sha8>/admission.json`
    (`buddy.admission.v1`), strict parse (unknown, duplicate or missing keys, a malformed
    hash, an unearned tier, a `reconcile` grant: each its own refusal; one bad record
    refuses every launch). Before anything runs, the executable is hashed and matched by
    bytes, and Codex's whole release tree is matched to its manifest (an undeclared entry
    refuses). Then contract id, platform, tier and profile, the exact version text, and
    the live checks, unchanged. Tiers are `start`, `resume`, `compaction_observation`;
    each needs the code's suites with an independent checker's pass. Capabilities are
    tiers intersected with code support, and reconcile is never granted. 2.1.289 and
    2.1.290 are retired (no binary or hash). Do not propose a runtime registry, ranges or
    probing alone, a metadata hash cache, or invalidation by adapter hash (an embedded
    record cannot bind its own binary; the contract id is bumped instead). A version-keyed
    parse rule whose outcomes are identical on every measured frame of every recorded
    version, proven by a committed hermetic replay, does not bump the contract id; a change
    that alters any such outcome does, and requalifies (operator, 2026-10-14).
81. **A qualification candidate (D-080)** is `buddy-adapter --qualify-candidate <artifact>
    --qualify-sha256 <hex>` in the shipped binary: the typed digest binds the artifact, the
    committed records are REPLACED by one synthesized record for those bytes, an observer
    `--executable` is hashed and must stay unchanged, and every start and resume needs a
    loopback-IP provider (Claude `ANTHROPIC_BASE_URL`; Codex `config.toml` `model_provider`
    `base_url`). Every receipt carries `qualification_candidate`; cross-mode resume refuses.
    The rename window between the post-hash re-check and `--version` is F-23's stated
    path-exec window (no worker starts on a replacement); `--retained` is the mitigation.
82. **A qualification run binds its own binary; checkers read the records (D-081).** Each
    runner hashes the binary it was given, reads `--version`, and types the candidate flags
    onto every adapter it starts (no `BUDDY_QUALIFY_*` env transport). Each checker takes its
    versions from the committed records and `--candidate harness:version:sha256`, and accepts a
    candidate run only for exactly those bound bytes with the route visible in the adapter's
    receipts. A run bound to other bytes is never accepted because the version has a record.
    `scripts/qualify-release.py` retains the build (staged, verified, renamed, never
    overwritten), runs the code's suites from the copy, and writes a record only for tiers
    whose suites and checkers all passed, only with `start`, and only on a clean tree at one
    HEAD. Do not propose per-checker version lists or hand-written records.
83. **The fleet recipe launches with the brief in a file, waits with no state named, retries a
    start the new tab refuses, and reads a Codex lane's JOBS line as "cannot say" (D-082).**
    Measured on the first mixed fleet (2026-10-14): `herdr agent start` refuses an agent
    argument holding a newline or a backtick, so the prompt is one line naming the brief
    file; `herdr agent wait` with no `--until` matches idle, done or blocked, and a finished
    lane rests at `done` (the trial's `--until idle --until blocked` timed out on all three
    lanes); a start straight after `herdr tab create` can be refused as `agent_pane_busy`
    and a retry seconds later works; under the Codex sandbox the hermetic tier runs
    escalated, once for the whole tier (`TestAdapterReportsEscapedToolAtClose` reads the
    process table, which the sandbox refuses, so a sandboxed red says nothing about the
    tree); a Codex session's JOBS line always says "cannot say" (its hooks bind no process,
    D-068), so its DONE words are its account of its jobs. Do not propose a sandbox skip for
    that test, a JOBS arm that anchors a Codex process, or a state list in place of the
    default wait.
84. **A launch brief delegates a bounded relay of the operator's decisions to ONE
    coordinator, named by full session id and label (D-083).** The brief is the launched
    session's operator request (its first prompt, D-056). Inside the decisions it says that
    coordinator relays (what to take next, stop or hand on; claims to release; closing lanes
    the session opened after DONE), a `buddy msg` from that session is decided, with no
    second ask in the lane's pane; the lane checks once that `buddy who <that id>` is live
    and wears the label (a label worn twice resolves to neither; `s-<8hex>` resolves only a
    default label). Outside the bound, or from any other sender, it is peer text. The sender
    is the key and the brief the authority; nothing in buddy confers (invariant 4 stands,
    `--relay` stays a declaration). Never delegable: install, push, settings, permission,
    trust, schema, a more permissive mode, work beyond the launcher's scope. Not chosen now:
    the sender's id on every drained line; a bare-shell `[operator]` row as the channel (a
    convenience, not a key); relaying through `herdr agent prompt` (a typed turn the lane
    cannot tell from the operator's); any authority-conferring verb or flag.

85. **A ledger this process may not write gets its cause and fix named; the gate still
    denies (D-084).** Codex's workspace-write sandbox keeps the git common dir read-only
    (measured: CANTOPEN 14 at open, READONLY 8 on a write while a peer holds the ledger open).
    A failing VERB whose error is that family, over a ledger the OS refuses `O_RDWR`
    (EPERM/EACCES), appends the cause and "request escalation up front"; anything else is
    unchanged. The gate is not routed through it. Do not propose opening read-only to get a
    verb through, or deciding anything from `CODEX_SANDBOX`.

86. **Disagreeing native ids refuse, naming the remedy; nothing orders them (D-085).**
    `env -u` the other harness's variable. Measured: a herdr tab takes the herdr server's
    environment, not the launcher's; a child of a harness's shell inherits its id. Do not
    propose picking the id by the nearest harness ancestor (D-068, D-025).

87. **A non-hook verb run under the native harness id moves `last_seen`, and nothing else
    (D-086).** Before the verb, `ended IS NULL`, never for `--session`/`BUDDY_SESSION`; no
    idle, claim renewal, process or lifecycle change; not init, the hook verbs, lifecycle,
    adapter or diagnose. A verb is not a late event, so it takes no incarnation (D-069 fences
    events). Do not propose clearing idle or renewing claims from it.

88. **The shell-patch gate's "cannot rule out" arm is Codex-only (D-087, narrows D-073).**
    Run-time script text and a command the bash-mode parser rejects refuse only when
    `classifyHook` says Codex; a literal `apply_patch`/`applypatch` at a command position,
    including in a wrapper's argv after a dynamic value, and an exhausted work bound refuse
    under every harness. Measured: ~10 Claude denials in ~9 of 144 sessions on one fleet,
    none protecting anything. Do not propose restoring the arm for Claude, or a lexical
    search of text the parser rejects.

89. **`claim` refused by a holder exits 4, and so does a dry run that forecasts it (D-088).**
    Other failures exit 1, a bare `buddy` 2 (3 is testguard's orphan exit); the gate always
    exits 0. Not-a-repository names the cwd; no-ledger says a second ledger coordinates with
    nobody. The verdicts (invariant 3, D-044) are unchanged: only the words.

90. **`hello` takes `$BUDDY_LABEL` when no `--label` is given (D-089).** The flag wins; the
    value takes `--label`'s path (stored as given, fenced on render, the worn-twice warning).
    Only `hello`: `lifecycle begin --label` is the adapter's typed argv.

91. **STALE keys on the holder, not only on the claim's renewal (D-090).** One judgement,
    `holders.judge`, renders in every claim view (refusal, dry run, `ls`, hello, `who`/`status`,
    `whose`, commit gate). Renewal fresh or unrecorded: nothing. Unrenewed past StaleAfter with
    the holder ended, every registered process GONE, or not seen past StaleAfter: STALE, ask
    the operator. Holder seen: "claim unrenewed; holder seen ... ask the holder: buddy msg
    <slug>". A live holder's all-slot claim: no mark. Words only: D-022/D-026 verdicts, renewal,
    orphaning and sweeping are unchanged. Do not propose renewing slots or parked claims to
    clear the mark, or reaping on GONE.

92. **The roster folds dead rows into one trailing line; `sessions --all` lists them (D-091,
    amends D-015).** Folded: ended over 24h ago, and live rows whose EVERY registered process is
    GONE (none registered is "cannot say", kept). The caller's row always stays. No ledger row
    changes (D-025). The line labels no column, so it is not the header D-015 cut. Do not
    propose auto-ending GONE rows.

93. **`who` answers the close test on one EXIT line (D-092).** Claims held; wait declared (with
    its verdict); jobs (none / N shells / "cannot say", never collapsed). Still grants nothing
    (D-027). `who <slug>` lists only that claim's waiters and names it; a bare `who` is `status`;
    a slash-shaped miss suggests `whose`; `whose` of a `.buddy/slot` path consults no git.

94. **`release` takes several slugs as ONE transaction, all or nothing; a recipient waiting on
    a claim it is handed lands (D-093, narrowing D-063).** `--to` moves every named claim before
    any is scanned, so overlap is checked against everyone but the set; one notice for the set;
    a refusal prints its whole conflict set. A wait target the waiter itself holds (only
    `release --to` gets it there) is over, computed at read time; nothing is cleared, so a
    multi-target wait stays pending until its other targets close. `--scope`/`--outcome`/`--note`
    stay single-slug. Do not propose clearing the recipient's wait, or refusing it again.

95. **`who <slug>` prints every scope of that claim, one `fence.Field` line each, uncut
    (D-094).** Every other view keeps its 512-byte cap. Do not propose lifting the cap
    elsewhere or adding `--json`.

96. **`wait --on <slug> --block` exits with the verdict (D-095, amending D-033).** 0 LANDED, 3
    EXPIRED, 4 cleared or replaced, 5 session ended; the check's own line; the row closed after
    the write. Autocommit reads every 15 s: never a check, never a keep-alive, no lock across a
    sleep (the one write is cleanupEnded's, only for an ended holder). Buddy still wakes nothing:
    the harness's completion notice is the wake. Do not propose having it wake, schedule, or
    bump the check counters.

97. **The handoff notice speaks once per 25k step past `BUDDY_HANDOFF_AT`, on a turn of any
    kind (D-096, amends D-055).** The highest step told is `session_context.handoff_step`
    (schema 16), claimed by one conditional UPDATE before the line is written, so two queued
    prompts print it once; the NEWEST sample under the size (in RecordContext's ordered
    upsert, every hook's sample carrying the declared size) or a new incarnation resets it
    to -1, and the claim matches the row's prompt, so a stale busy moves nothing. The
    `UserPromptSubmit` payload carries no turn origin (2.1.304's builder) and the turn's
    transcript record is not on disk at hook time (measured, 7 of 7 turns), so turns are not
    told apart. Do not propose per-turn repetition, a set of told steps a stale sample can
    undo, storing it in another table, or reading the turn's origin from the transcript.

98. **A drain with no CORRECTS/SUPERSEDED link or declared kind gets the short inbox header;
    `msg`'s stderr wake copy is skipped when stderr is stdout's open file (D-097, amends
    D-052).** Same file is fstat device and inode; a socket, a non-file or a failed stat gets
    the copy. The rendering under either header is unchanged (invariant 9), and budgets measure
    the long header. Grouping hello's claims by holder was measured (~4%) and not done.

99. **A refusal that shows the usage ends with `  refused: <reason>`, usage first (D-098).**
    `usageRefusal`: `parseFlags`, `noStray`, `init`, `release`'s argument refusals. A refusal
    with no usage is one line already. Do not propose moving the reason back above the usage.

100. **Mail reaches the session it was sent to (D-099).** A hook payload with `agent_id` (a
    subagent's tool call, under the PARENT's session id; documented and in the 2.1.304
    hook-input schema, not measured on a live payload) beats for the session but drains no
    mail and consumes no authority or LANDED notice; only the dirty-path warning shows.
    Every drain (beat, busy, idle, `inbox`) holds the ledger's write lock from its read to
    its mark (`store.Drain`), write first and mark after. `idle` checks for queued legacy
    mail and records idle in ONE transaction (`store.StopTurn`): mail becomes a Stop
    `{"decision":"block","reason":…}` and no idle mark, never when `stop_hook_active` is
    true, never for a subagent or adapter mail; a failed write rests idle. `msg` re-reads the
    idle register after its insert for the wake. `inbox <id>` re-reads one message addressed
    to the caller in any state, marks nothing, and refuses every other id alike, with no
    body. Do not propose marking before writing, a lease column, or a wake for every
    recently seen recipient.

101. **A scope's backslashes fold BEFORE the absolute check (D-100).** `\etc`, `\..\x` and a
    lone `\` were stored as "/etc", "/x" and "" (granted, covering no file) at every door
    `NormalizeScope` serves; now each is refused there, naming the backslash. The two doors
    that only REMOVE a held row, `release <slug> --scope` and `authority rm`, use
    `NormalizeHeldScope`, which maps an absolute input to the spelling the old code stored,
    so it can match a legacy row and nothing else (D-066's precedent). Do not propose a
    migration rewriting legacy rows, Windows drive spellings, or letting an adding door
    use the held function.

102. **The coordination claim is `orc-<project>` (D-101).** `<project>` is the main checkout's
    directory name. A bare `orchestrator` slug, worn by every repo's coordinator, made the
    chat alert (which matches claim slugs in every room, D-009) tell each coordinator about
    the others' mentions. Nothing in buddy reads the name. Do not propose a reserved prefix,
    a derived default in code, or scoping the alert to a session's derived room.

103. **A vanished hook cwd is discovered from its deepest existing ancestor (D-102).** The
    verdict is what an empty directory at that path gets. Only an absolute cwd whose `Lstat` is
    exactly ENOENT, with no `GIT_DIR`/`GIT_COMMON_DIR`/`GIT_WORK_TREE`/`GIT_CEILING_DIRECTORIES`/
    `GIT_DISCOVERY_ACROSS_FILESYSTEM` set. The ancestor is found as the kernel resolves the path
    (links spliced in, `..` applied physically, never `filepath.Clean` first; a dangling link
    followed, not climbed past; an empty link target gives up); both ends re-read after.
    `shellPatchDirs` places any base whose `Stat` is ENOENT the same way. A relative tool path from it is unplaceable and refused
    where a ledger governs; beat and lifecycle renewal ignore it. `init`/`diagnose` stay strict.
    Measured: a lane that removed its worktree inside the checkout was denied every call, its
    DONE report included, while one beside the checkout was allowed. Do not propose allowing a
    vanished cwd wholesale (it drops the home ledger's brake) or denying it wholesale.

104. **The orchestrator uses agent tools itself; a subagent is not a lane (D-103).** While it
    runs a fleet, the orchestrator puts side work (a lane's diff, reports, measurements,
    housekeeping, a second opinion: subagents on any model, a Codex pass through
    `codex-review.sh`, a Fable read) on subagents, because its own context is the scarce
    resource before its handoff size (orc-3 handed off at 322k, orc-4 at 397k). A subagent
    holds no claim, lands nothing, relays nothing, and runs under the parent's id and claims
    (D-099); its findings are evidence once a test reproduces them. Do not propose a ledger
    row or verb for subagents, or counting their tokens against the handoff size.

105. **The review tier follows the change class; a receipt names what ran; the model is pinned
    per tier (D-104).** Full (what the gate refuses or allows, the ledger schema,
    `internal/fence`, an invariant): a read-only subagent panel, Codex design and code passes,
    a required Fable cross-check, and a Codex re-verify of the fixes until it accepts or the
    brief's bound. Lite: a scoped adversarial self-review and one Codex pass, one round, Fable
    optional. Chore: no outside review required, with the reason. A test-only change takes its
    class's tier; a second model is allowed at any tier. The receipt names each layer, each
    second-model pass with its own effort, the cross-check by its model
    (`review: full (panel + codex@xhigh design + codex@xhigh code + fable + codex@xhigh reverify)`;
    measured, 0 of 15 lite/full receipts here named one). Lanes run on the
    operator's default model; a full item, its panel and fold rounds on the strongest at high
    effort; the skill states the default as that day's, never a constant. Do not propose a
    tier column in the ledger, a receipt check before receipts follow the shape, or model
    names as constants in the skill.

106. **`msg` signs an id the ledger does not know as `unregistered s-<8hex>` (D-105).** A
    session id in the environment with no LIVE row here (absent, or ended) used to sign as
    `operator`, or as its `--from` alone; measured 2026-10-26, a session registered in
    another ledger addressing this one was told "would send … as operator". It now signs
    the short form a hello would mint, `--from` a tag after it
    (`unregistered s-66a24e29 (review)`), and the result line (and the dry run's) says it is
    not registered here and names `buddy hello --session <id> --label <name>`, ahead of any
    wake (D-041). The bare terminal is unchanged (`operator`, or its `--from`). Never
    refused; `senderSession`'s UNKNOWN owner (D-043), the drain headers and the 64-byte
    sender fence are untouched. The stamp is a notice, not a reply address (it resolves to
    no target); a reply goes to the label the sender registers. `release --to`'s notice is
    signed the same way, from the environment, not the holder. Do not propose refusing the
    send, requiring hello first, or signing with the full id.

107. **The A-01 re-review's open shell-recognizer forms are closed (D-106).** The
    shell-patch recognizer (`internal/cli/patch.go`, D-073/D-087) now: keeps reading env's argv
    past a DYNAMIC `env -S` value so a later `-C` is discovered and the literal found (A,
    Codex-verdict only — Claude already read past it); recovers a directory change lexically
    from `env -S` text carrying a `#` comment or escape, e.g. `env -S '-C /gov #' apply_patch`
    (B, both harnesses; best-effort BOTH ways — it can also MISS one env's `\_`/`\c` escapes
    change, NOT "only over-refuses"); treats `script` with a transcript file and no command as a
    shell reading stdin, so `script -q /dev/null <<'SH' … apply_patch … SH` is caught (C, both;
    playback `-p` excluded; the kernel control needs the heredoc padded past script's pty
    buffer); refuses an over-budget brace consumed as an option/action value in the wrapper
    grammar, `shell()`'s `-o`/`--rcfile`/`-S`, the `--`/lead loop, `findActions`, `gitRoute`,
    `scriptFile` and `trap` (E, both); and FIXES fish F1/G1/G2 in `shell()` (repeated `-c`
    refuses; `--no-execute`/`-n` allow). Finding D was refuted. `exec <<'SH'` read by a later
    statement stays the documented cooperative limit, tested as an accepted gap. Recorded OPEN
    (respellings, D-106): attached dynamic `env -S"$x"` jurisdiction, util-linux `script -c`
    repeated, GNU abbreviated long options, a front unknown-wrapper (`lockf -k`), util-linux
    `script -t` optional value. These forms and B/C/F4 may be re-examined — this GIVEN records
    what was done, and does not forbid re-deriving them.

## Environment facts (measured, do not re-derive)

- A NEW buddylist build is refused its first launchd spawn: `OS_REASON_CODESIGNING | Launch
  Constraint Violation`, the agent carrying `managed LWCR`. backgroundtaskmanagementd
  re-registers it 10.0 s later (two samples), and the next spawn runs. It follows the spawn,
  not the file: a 15 s pause between the build and the kickstart did not avoid it. An
  unchanged binary restarts in 0.14 s. Measured 2026-10-03, macOS 27 (D-067).

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
- `/clear` **ends the session and starts a NEW session id in the same harness process** (same
  pid and pane; the old row goes to `ended`). `/compact` and `claude --resume` keep the id.
  An id that never took a turn has no transcript and cannot be resumed (`No conversation
  found`). Anything keyed by session id (inbox, wait) does not cross a `/clear` (D-034,
  measured 2026-09-23).

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

- **A constrained Codex reviewer cannot build or test.** This charter is prepended to
  that read-only workflow by `scripts/codex-review.sh`. Authorized drivers and workers
  follow `AGENTS.md` and may build and test within their assigned scope. A review
  verdict is never test evidence; report executed checks separately, and watch a
  defect's reproducing test fail without the fix.
- Report findings ranked by severity, and separate CONFIRMED defects from residual
  questions. A ranking resting on an adjective rather than a number is a hypothesis.
- Prefer naming the exact failing input and the resulting wrong behavior over describing a
  category of concern.
- A guard's review scope is every site that BYPASSES it, not the diff that adds it.
- If the prompt inlines code, review the inlined code. Do not assume unshown helpers are
  wrong; ask about them explicitly instead.
