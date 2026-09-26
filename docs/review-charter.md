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
    both annotate a quiet holder (`— STALE: holder last renewed 3h ago; it still refuses`),
    carried on `Conflict` AND `ErrRefused` so the two paths cannot disagree, and a
    refusal prints the whole set even when there is only one conflict.

27. **`buddy whose <path>` reports the claim registers first, then `DIRTY IN` (D-023).**
    `CLAIMED BY` is invariant 14 containment and the relation the gate reads; `HELD UNDER`
    is a claim on a path inside the one asked about and reserves nothing about it — they
    are kept apart on purpose. The claim lookup consults NO FILESYSTEM (an `os.Stat` gate
    made a claim under a not-yet-created directory read as `(none)`), and reads ONE
    snapshot rather than matching ids and materializing them separately. `(none)` prints
    explicitly rather than being omitted — the defect was a silent answer read as "nobody
    has this" — and a stale claim is marked `STALE (still refuses)`, D-022's wording.
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
    authority`, at most 8; `CLAUDE.md` always), never git config on the hot path. The
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
    exists as a socket, `msg` prints `SendMessage to "uds:…"` with the fixed text "buddy mail is
    queued for you: run buddy inbox". The body stays in the ledger. One observation serves the
    result line and the wake line. No send from buddy, no pane injection, no broadcast wake, no
    name mapping. Do not propose buddy calling the harness, carrying the body in the wake, or
    deriving the address from anything but a live, birth-time-checked registered pid.

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
    it. Do not propose moving the address back onto a line of its own.

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

- **Codex cannot build or test.** A Codex pass is never test evidence. Its findings become
  evidence only once a reproducing test is written and watched to fail.
- Report findings ranked by severity, and separate CONFIRMED defects from residual
  questions. A ranking resting on an adjective rather than a number is a hypothesis.
- Prefer naming the exact failing input and the resulting wrong behavior over describing a
  category of concern.
- A guard's review scope is every site that BYPASSES it, not the diff that adds it.
- If the prompt inlines code, review the inlined code. Do not assume unshown helpers are
  wrong; ask about them explicitly instead.
