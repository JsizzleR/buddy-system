# Decision record

An append-only ledger of the rulings this project is built on: what was wrong, what
shipped, what was deliberately cut, and what remains broken. Newest entry last.

`CLAUDE.md` carries the one-line summary of each of these ("Decisions already made");
this file is the record behind it — the measurement, the rejected alternative, and the
reason a cut was a cut. Reviews here are Codex passes; Codex cannot build or test, so a
finding became evidence only once a reproducing test was written and watched to fail.

## D-001 — Claims live in a transactional SQLite ledger, not in lock files

2026-08-14 · Codex design pass on the plan, verdict reject-as-written

**What was wrong** — The first claims design was `O_EXCL` lock files with heartbeat
mtimes. `O_EXCL` reserves a *filename*, not contents: a reader can see a partial write.
Concurrent sweeps have an ABA race — read an old claim, the owner releases, a new claim
lands on the same path, the sweep deletes the wrong one. A claim over several scopes
cannot be acquired atomically across several files, so partial acquisition is reachable.
And scope-overlap checking needs a transaction regardless.

**What shipped** — One ledger at `<git-common-dir>/buddy.db`: WAL, `_txlock=immediate`,
busy_timeout, migrations from day one, pure-Go SQLite driver (no cgo). A claim over N
scopes is one transaction — granted whole or refused whole, naming the claimant. The
*common* dir, not `.git`, so every worktree of a checkout shares one ledger; it is
machine-local and git never sees it.

**What it deliberately does not do** — No TTL reclaim. Staleness *marks*, it never reaps:
a session that thinks for an hour without touching a file is indistinguishable from a dead
one. Only positively-ended sessions are cleaned automatically; anything uncertain requires
an explicit `sweep --force`.

**Residuals** — Claims are single-machine. Multi-machine coordination is out of scope.

## D-002 — Scopes are exact paths and directory prefixes; globs were cut

2026-08-14 · Codex design pass

**What was wrong** — Glob scopes make overlap a decision problem with no cheap answer, and
a wrong answer is a safety bug in both directions: a false grant lets two sessions edit the
same tree, a false refusal blocks work nobody reserved.

**What shipped** — Canonical repo-relative, slash-separated scopes. Containment is exactly
`scope == path || strings.HasPrefix(path, scope+"/")`. One folding rule everywhere —
`strings.ToLower(norm.NFC.String(s))`, never `strings.EqualFold`, never a second
normalization — because the default macOS volume is case-insensitive and a repo root can be
aliased several ways. Traversal, absolute paths and case-aliased roots are rejected or
resolved before comparison.

**What it deliberately does not do** — No glob scopes, no arbitrary-glob overlap math. No
special-casing of repo-shaping git operations (rebase, merge, reset, checkout across
scopes): worktree-per-session already isolates them, and the case for revisiting is a real
conflict, not a hypothetical.

**Residuals** — The folding rule over-merges on a case-*sensitive* volume mounted under a
repo. Separator handling is darwin-only.

## D-003 — Identity is `(session_id, incarnation)`; the PID is diagnostic only

2026-08-14 design pass; Bye ABA fixed 2026-08-14 in a scoped review of store lifecycle

**What was wrong** — A session id alone cannot distinguish this session from a dead earlier
run of the same id, and process ids are recycled lies. Worse, the end-of-session hook
originally orphaned open claims inline: a delayed `bye` from a dead incarnation, racing a
re-hello of the same session id, orphans a live session's work.

**What shipped** — A random incarnation token minted at `hello`. `bye` only *marks* the
session ended, keyed to the incarnation it was issued for; orphaning of open claims moved
to `hello` and `sweep`, where it is idempotent and applies only to rows still ended when
they run. A delayed `beat` cannot resurrect an ended session — `beat` refuses a row with
`ended` set — so `last_seen` is necessarily the earlier of the two writes.

An ended row is therefore dated by `ended`, with the last beat printed beside it. They are
different facts and neither implies the other: `ended` is when the process went away, the
last beat is when its work stopped. Measured across the 151 ended rows in this machine's
ledgers, 71 exceeded the stale threshold, the median gap was 18m and the widest 5.1 days —
so on nearly half of all ended rows the two facts differ by more than the threshold used to
call a session stale. The annotation is unconditional: a note that appears only sometimes
is a fact the reader must already know about to notice it is missing.

**Residuals** — `RESIDUALS: none`

## D-004 — Two binaries, so the claims ledger is never dragged behind the chat stack

2026-08-14 · Codex design pass (accepted finding)

**What was wrong** — One binary puts chat's failure modes — a wedged daemon, a held write
lock, a socket dial that times out — in front of the safety half's hot path.

**What shipped** — `buddy` is claims, control and hooks: no network, no chat, no MCP. It is
small and it is the only thing that reserves or refuses anything. `buddylist` is the
concierge daemon, journal, MCP server, alert hook and presence; it depends on the store
package. A failure there degrades to "no chat", never to "no safety".

**What it deliberately does not do** — The chat half never writes the ledger. The single
crossing is read-only (`cli.ChatIdentity`, which reads this session's claim slugs), and WAL
readers never block the writer. Proactive alerting was deferred once for exactly this
reason and then built without violating it (D-009).

**Residuals** — `RESIDUALS: none`

## D-005 — The gate fails closed on an unreadable ledger and is silent where none exists

2026-08-14 design pass; hardened 2026-08-14 by a scoped Codex pass on gate bypass

**What was wrong** — The tempting single arm — "if discovery fails, allow" — makes the
safety feature stop existing with no sign that it has. Discovery fails for reasons that are
not "feature off": git missing from `PATH`, a dangling ledger symlink, a corrupt or
unreadable database.

**What shipped** — Three verdicts, kept apart on principle. *Provably* not a repo, or a repo
never initialized → feature OFF, silent no-op. Ledger present but unreadable → DENY, naming
the remedy. Otherwise adjudicate: a `pause` control targeting this session denies the next
mutating call with the operator's note; a path inside another live session's scope denies,
naming the claimant. Discovery rests on the git verdict plus an `Lstat`, with `LC_ALL`
pinned; an edit that crosses repos is adjudicated against the *target* repo's ledger.
Measured: `gate` 20 ms, `beat` 13 ms against a 100 ms hook budget.

**What it deliberately does not do** — It adjudicates only what a tool declares. A shell
command's side effects are invisible to it; so is any process that never entered the
harness. That gap is the entire argument for the commit-time second line (D-012).

**Residuals** — Enforcement is cooperative and there is a TOCTOU window between verdict and
write. A seatbelt for agents, not a sandbox against them; saying so is the design.

## D-006 — Chat is never the lock, and chat text is never translated into control

2026-08-14 · Codex design pass

**What was wrong** — The first sketch was "agents coordinate by talking in a room." That
dies on one line: *announced is not locked.* A claim must be atomic, durable across a
session dying mid-task, and enforced where the conflict happens; a message provides none of
those, and an agent that joined late, did not poll, or lost the message to context
compaction does the conflicting thing anyway. The chat server's own management API compounds
it: the `from` field of an injected message is unauthenticated and arbitrary.

**What shipped** — The ledger owns claims and control. The operator's brake (`pause`) and
the message inbox are authoritative *because* they enter through the local CLI; the room
merely mirrors. `from` never confers authority. The inbox is at-least-once: delivery marks
`delivered_at` but undelivered rows replay on the next drain, message ids dedupe, drains are
bounded (20 messages / 8 KiB), and nothing is acked until the notice has actually been
delivered. Every attacker-influenced value that reaches a model's context goes through
`internal/fence`: exactly one line per value, byte-capped, with membership lists marked
peer-controlled. The transports refuse CR/LF/NUL at the final send boundary, so no text
argument can smuggle a protocol command.

**What it deliberately does not do** — Room digests are never auto-injected into an agent's
context; only operator inbox messages are. The chat-command bridge — translating an operator
DM into a control row — stays cut until real auth is on.

**Residuals** — Everything binds loopback, and the chat servers run unauthenticated *only*
because of that. The ledger trusts the machine's user account: a single-operator tool, not
a tenancy boundary.

## D-007 — IRC is the daily driver; the TOC/AIM stack stays behind the `Conn` seam

2026-08-14 (late) · operator verdict after live use; Codex pass on the IRC transport

**What was wrong** — The AIM-compatible path was built first for nostalgia and measured
rough in real use. Three assumptions died on contact: a TOC join does **not** create a
missing room on the public exchange (it errors internally), so rooms have to be created
through the management API; the AIM client's own chat dialog lives on a *different exchange*
than the API-created rooms, so two rooms can share a name and never meet — the first live
operator session was one person alone in a room because of exactly that; and "UTF-8 is text"
is false for a 2002 client, where an em dash sent as UTF-8 is mojibake, so charset is part
of the wire contract.

**What shipped** — A second backend behind the same `Conn` seam, reusing the existing event
vocabulary: injection-guarded, chunked, membership-tracked, with IRCv3 `echo-message`
negotiated so the journal keeps its contract (D-008). It is the default; the whole TOC/AIM
stack stays one flag away. The seam absorbed the swap in an evening and the claims half
never noticed — the independence claimed in D-004 paying out. The Codex pass on the new
transport found a real `SetAway` newline injection, where a status string could smuggle a
protocol command; fixed with regression tests, and the guard's review scope was taken to be
every site that bypasses it, not the diff that adds it.

**What it deliberately does not do** — The daemon must not know what IRC is. Numerics,
charset and framing stay inside the wire package; anything the daemon needs crosses as a
daemon-level type (`ErrNickInUse`).

**Residuals** — UTF-8 is native on IRC, so the CP1252 conversion is exercised only on the
nostalgia path.

## D-008 — The journal records what the server saw: one connection's view

2026-08-14 · Codex pass on the daemon; room-name failure measured 2026-09-07

**What was wrong** — Neither protocol offers scrollback; a client sees only traffic that
passed while it was connected. Journaling *what we sent* would record intent as fact — and
without `echo-message` an IRC server does not reflect your own messages at all, so the
daemon's own relays would silently vanish from the record. (On TOC reflection is always on;
that difference is exactly why the assumption had to be checked.)

**What shipped** — A durable journal with monotonic `(epoch, seq)` cursors that survive
daemon restarts. Outbound intent is journaled to an `@sent` outbox *before* the send, so a
message whose echo never returns is still on the record: submitted and confirmed are
different facts. A cursor older than the retention horizon returns an explicit gap marker
and how to resume — "nothing new" and "the rows you asked for are gone" must never be the
same answer. Reads return oldest-first whatever the selection mode, because every renderer
prints in that order.

The same failure class showed up once more, in the session digest: it told every session to
read a room name the daemon does not serve, and an empty read is indistinguishable from a
quiet room. Measured 2026-09-07 — a session reported "the room is empty, all peer traffic
goes through the message hook instead" while the real room held thousands of messages. The
digest now derives the room from the session label, fenced like every other interpolated
value, and states outright that an empty read means a wrong room name.

**What it deliberately does not do** — No second connection may journal. N live sessions
journaling the same room would store every message N times and destroy the one-connection
contract; that is the binding constraint on presence (D-011).

**Residuals** — `Say` can succeed into a dying TCP connection; the `@sent` row is what keeps
the record straight when it does.

## D-009 — The self-alert discriminator is the `@sent` outbox row, not a sender comparison

2026-08-26 · built and validated against the live journal

**What was wrong** — Two assumptions failed under measurement. *A session answers to its own
name*: a `mentions_me` filter built from session id and label scored **0** matches on 2313
messages of the busiest room — peers address each other by **claim slug**, which lives in
the ledger, not in chat. *A room row knows who wrote it*: every relay has the concierge as
its sender and the `[label]` attribution is text inside the body, which the wire then
CHUNKS — 215 of 2291 rows are attributed at all — so a session's own announcement of its own
slug is indistinguishable from a peer naming it.

**What shipped** — `buddylist alert`, a second PostToolUse hook on the chat binary: on its
next tool call a session is told that a room message named it — room, count, seqs, and the
exact `chat_read` that fetches them. The token set derives live claim slugs and orders slugs
FIRST, because the set truncates its derived tail at the cap and the tail is the half that
has never matched anything; the alert prints the tokens it used, so the read it recommends
cannot disagree with it. Self-alerts are suppressed by testing each echoed chunk against the
`@sent` outbox row written before the send: every echoed chunk of a 3566-byte message was a
literal substring of it (13/13). Replayed across four real sessions: 7 self-alerts
suppressed, 3 genuine ones kept, 0 genuine ones lost. Containment can only suppress, never
invent, so its failure direction is a missed alert.

The alert cursor is a separate table from the read cursor, because being TOLD about a
message is not having SEEN it — advancing the read cursor would bury unseen rows behind
`since_last` forever, and a session that has read a room must still be alerted about a later
message naming it. A room with nothing to report banks its own scan inside the daemon, so an
idle room is never rescanned; a capped scan reports a floor and advances only to where it
stopped. Cost: one socket round-trip per tool call, bounded at 750 ms; measured 5.9 ms cold
and 1.2 ms warm against a 2600-row journal.

**What it deliberately does not do** — It never carries chat text. The byte budget and the
untrusted-content fence live in the deliberate `chat_read`, and an auto-injected body
bypasses both; one read gets it. It does not push into the ledger inbox, which would put the
chat stack in front of the claims hot path (D-004).

**Residuals** — Mention tokens under 3 characters are refused rather than clamped, so a very
short slug is unmatchable — a clamped token would return every row while claiming to be
filtered.

## D-010 — Context cost is a first-class constraint; the defaults were narrowed to fit it

2026-08-26 – 2026-08-27 · audit plus a seven-day rollout baseline

**What was wrong** — This project makes no model calls; its entire spend is text it
deliberately places in a context window, and four sources were measured. The full
seven-tool MCP schema was registered globally — 3470 bytes carried by every turn of every
session, including projects with no stake in it. Catch-up paged FORWARD from the retention
horizon, so the newest rows were reached last: one measured catch-up took ~20 reads over
~1900 rows, making reading a room cost O(history) instead of O(new). Routine reads averaged
about 12 KiB and sends commonly carried multi-KiB prose. And a broadcast matched every
session created after it was sent, so a one-line interjection became standing context for
tomorrow's unrelated work — 532 broadcast deliveries / 808567 injected body bytes against 11
targeted deliveries / 16504 bytes over seven days.

**What shipped** — The `core` MCP profile is the CLI default and advertises only
`chat_send`/`chat_read`: 1958 schema bytes against 3470, about 44% smaller, with the
operator tools one flag away; registration moved from global to per-project. Reads select
forward, backwards, or tail, and which rows survive the budget follows the direction — a
forward page keeps the oldest so its cursor steps over nothing, a tail keeps the newest,
which is the whole reason tail exists. `since_last` owns the entire selection and refuses to
ride on an explicit `after`, a windowed read or a mention filter, each of which would
advance the cursor past rows the caller was never shown. A routine read is 10 rows / 4 KiB;
an explicit limit above 10 opts into the 16 KiB page. A routine send caps at 750 bytes, with
`long=true` required for a deliberate handoff up to the 4096-byte hard limit. Broadcasts
transactionally snapshot the sessions live at send time and expire after 24 hours; the
additive migration reconstructs legacy audiences only from session lifetimes that covered
the original send, committed with its own marker table so a half-finished migration cannot
read as a completed one. Direct messages keep their durable semantics.

**What it deliberately does not do** — The cost report records aggregate counts and byte
lengths only: never a body, a prompt, or a tool result.

**Residuals** — An MCP subprocess keeps the profile it started with until its parent session
exits.

## D-011 — Per-session presence is presentation only, and rides the existing alert hook

2026-09-07 · held since 2026-08-14 for "only after the concierge is boringly stable"

**What was wrong** — The design pass argued for dropping per-session presence entirely. It
was kept on one condition: that it stay presentation and add no coupling from the chat stack
to the safety half. The obvious build breaks that condition three ways — a new hook line, a
second round-trip, and a ledger read on the daemon's side.

**What shipped** — Each live session joins its project's room under its own nick and wears
its claim as an away message; idle at 5 minutes, gone at 30 — the ledger's own STALE
threshold, so the room and `buddy ls` tell the same story. The nick list is the fleet, and a
WHOIS answers "what is that one working on?" (verified live: `301 :claim: <slug> · active`).
The claim slugs ride the PostToolUse alert hook, which had already computed identity for its
own mention tokens: no new hook line, no second round-trip, no daemon-side ledger read.
`note()` is map writes plus a non-blocking wake and every dial happens on the manager loop,
so the hook's measured cost is unchanged even while every dial is timing out. Bounded at 16
connections, because the IRC server exempts loopback from its own limits. `presence [--gone]`
is the explicit door, a SessionEnd hook retires a buddy at once instead of leaving a ghost
for half an hour, and `health` reports online/wanted so a silent feature can still answer
"is it working?".

**What it deliberately does not do** — It never journals: a session connection sees the same
traffic the concierge does, so journaling from one would store every message once per live
session and break D-008. Its events are drained and dropped — drained because an unread
channel stops the client answering PING. It never speaks: sends stay on the concierge, whose
`@sent` outbox is the discriminator the alert path depends on (D-009), and moving sends moves
that discriminator, which is its own change with its own evidence to gather. It never renames
over the wrong failure: only a nick-in-use refusal walks the collision suffix, while a
connection refusal keeps the name and backs off, because a session quietly answering to a
renamed nick would hide the real fault. And it is not on the TOC path at all — a screen name
there is an ACCOUNT and a second signon boots the first, so a collision would cost somebody
else's connection rather than a suffix.

**Residuals** — Without the SessionEnd hook installed, a buddy ages out over 30 minutes
instead of leaving when its session does.

## D-012 — The commit gate reports claim collisions only, and warns before it denies

2026-09-07 · Codex design pass before any code (it rejected the original spec), then a
Codex pass on the implementation (four defects, each fixed against a test watched to fail)

**What was wrong** — The PreToolUse gate adjudicates the path a tool *declares*, so it sees
an Edit and a Write and nothing else. A file written by a code generator, a Bash redirect, a
formatter run over the tree, or any process that never went through the harness reaches the
index without the gate ever being asked. Those writes are invisible at the tool boundary and
visible at the commit boundary.

**What shipped** — `buddy commit-gate` behind a `pre-commit` hook. It hands the staged paths
to the ledger and reports any that sit inside ANOTHER session's OPEN claim — open, not live,
because a claim outlives the session that took it and an ended owner is exactly the case that
wants `sweep --force`. Posture is warn by default with `BUDDY_COMMIT_GATE=deny` as the opt-in:
nobody has yet measured how often a commit in a shared checkout legitimately touches a peer's
claimed scope (a deliberate handoff looks exactly like a mistake from here), and shipping deny
first would enforce against an unmeasured false-positive rate. The ledger is opened BEFORE
identity and before git, so an unresolvable session cannot take an early exit past an
unreadable one; unreadable exits 2 naming the remedy, provably-absent exits 0 in silence
(D-005). `BUDDY_COMMIT_GATE_SKIP=1` is read first of everything, because a kill switch has to
work when the thing it disables is what is broken. Reports cap at 10 claims / 12 paths and
state the counts when they bite, since a report that quietly drops rows is indistinguishable
from a smaller conflict. The hook has no trailing `exit 0`: copying the chat-hook idiom would
discard the verdict and leave both the deny posture and the fail-closed arm inert while still
appearing to work.

Five git facts it rests on, each measured rather than assumed. `-z`, because the default
output C-quotes any path with a space, a quote or a non-ASCII byte, and a quoted path matches
no scope — the gate would go quiet on exactly the filenames nobody tests with.
`--no-renames`, because rename detection reports only a rename's DESTINATION, so moving a
file out of a peer's claimed scope would be invisible; off, the source arrives as a delete
and the destination as an add, and both are adjudicated. `-c diff.relative=false`, because
`diff.relative` in a user's own config silently makes output cwd-relative, and `z.txt`
matches no scope where `deep/dir/z.txt` does. `--no-optional-locks` plus `core.fsmonitor`
pinned off, the first so the listing never takes the index lock the committing git is holding
and the second because fsmonitor is a path to a program git executes, read from a file no
claim protects. And the one that decides the command's correctness: a PARTIAL commit
(`git commit -- <path>`) builds a temporary index and points `GIT_INDEX_FILE` at it, so the
child git's environment must NOT be sanitized the way the background scan's is — measured,
inherited the listing is exactly the committed path, stripped it is two. `GIT_CONFIG*` is
still dropped, being configuration rather than a statement of what is being committed. Also
measured: intent-to-add entries appear in neither the cached diff nor the commit, so they
need no special handling. There is deliberately no `--diff-filter`: an add, a modify, a
delete and a typechange are all writes to the path.

**What it deliberately does not do**

- It does not report paths inside NOBODY's claim. The original spec asked for "writes outside
  your own claims", but that fires on nearly every commit — a README nobody reserved is not a
  collision with anyone — and a warning that fires on everything is read as noise and then
  disabled. It is also not computable from the ledger call used: "no other session holds
  this" does not establish that a path is unclaimed, because it may sit inside the COMMITTING
  session's own claim.
- It does not consult dirty paths. Another session's tool call having named a file is not
  authorship of the staged hunks, and a deliberate handoff produces exactly the same signal.
  That table answers "who do I talk to?", which is `buddy whose`, not this.
- It does not refuse a committer it cannot identify. A human typing `git commit` in their own
  terminal has no session id, and blocking that is how the hook gets uninstalled. The empty
  id is not passed on as a neutral value either — the ledger query excludes on
  `session_id <> ?`, so `""` excludes nothing and every open claim becomes a hit; the safe
  direction, but the report says which of the two cases it is rather than accusing somebody
  of colliding with themselves.
- It does not deduplicate across commits. A per-path cooldown would hide the second
  conflicting edit to a file, which is a real event and not a repeat of the first.
- No pre-push variant. It would compare historical commits against CURRENT claims, which is a
  different question — the claim may have been released before the push or taken after the
  commit — and an endpoint diff over a range cannot see an edit that a later commit reverted.

**Residuals** — It reveals a reservation conflict on the paths of a pending commit. It does
not prevent the write that created them, does not establish who made them, and does not cover
commits whose creation never invokes it.

## D-013 — One target namespace for `pause`, `resume` and `msg`, resolved before it is stored

2026-09-07 · found by a model-diverse roadmap pass (Codex + a Claude pass), then measured
against this machine's live ledgers

**What was wrong** — All three verbs that address ANOTHER session took one argument the help
called "<session|label|all>" and wrote it into the ledger VERBATIM. The matching queries on
the other side are exact — `PausedFor` matches `(session_id, label, 'all')`, `Undelivered`
matches `(session_id, label)` — so a target naming nothing produced a row matching nothing,
and every one of those verbs then printed success and exited 0. Measured on the busiest
ledger on this machine: **31 targeted inbox messages, 16 never delivered, and 8 of those
addressed in forms the delivery query cannot match** — five bare `s-<8hex>` short ids and
three CLAIM SLUGS. All three slugs had an open claim at the instant their message was sent,
so all three were resolvable at the time and simply never resolved.

The same namespace is the operator's brake, which is the part that matters. `buddy pause
<slug>` printed *"paused <slug> — takes effect on their next mutating tool call"* and paused
nobody. It had gone unnoticed because `controls` has **zero rows** in that ledger: the brake
has never been used in anger, so its silent failure had never been observed. This is the one
place where the project's own measurement pointed straight at the gap and nothing consumed
it — D-009 established that peers address each other by claim slug (identity scored 0
matches over 2313 live messages), and no target query consulted the claims table.

**What shipped** — `store.ResolveTarget` maps an argument onto exactly one session or
refuses, in a fixed most-specific-first order: `all` → full session id → label → `s-<8hex>`
short form → open claim slug. Slugs resolve only among OPEN claims, where the
`claims_open_slug` partial unique index makes the answer unique by construction; a released
slug is deliberately unresolvable, because the same slug may have been held by several
sessions over time and the answer would silently change as history accumulated. An ambiguous
short id refuses and names its candidates rather than picking one. Matching is exact, never
folded — a second folding rule here would resolve targets that the exact read-side queries
cannot match, which is the bug being removed.

`Pause`, `Resume` and `Msg` now take a `Target` rather than a string, so **the compiler is
what stops an unresolved value reaching a row**. A guard's review scope is every site that
bypasses it; making the resolved type the only accepted argument means there is no such site
to review, and the change was located by the type checker naming all three call sites.

Resolution happens at the WRITE boundary and stores the canonical session id. The read side
is the gate's hot path — `PausedFor` runs on every mutating tool call against a 100 ms hook
budget — so both matching queries are untouched, this costs the gate nothing, and it cannot
alter what an already-written row matches. `Resume` clears the resolved id **and** the raw
argument, because rows written before this carry whatever was typed; `PausedFor` still
honours those through its label arm, so clearing only the resolved id would strand a live
pause that the gate keeps enforcing and no verb can lift.

**What it deliberately does not do** — It does not resolve a released claim's slug (above).
It does not fold case. It does not refuse a target whose session has ENDED: `Hello` revives a
session under its own id, so the row remains correct and durable — but it warns on stderr,
because nothing will drain it until that happens and silence there is indistinguishable from
delivery. And the resolver's errors and echoes are fenced in `internal/cli`, not in the
store: an ambiguity report names candidate LABELS, which are peer-controlled text reaching an
agent's tool result, while `internal/store` stays free of the fence dependency because it is
the safety core and rendering is not its job.

**Evidence** — Six new store tests and six CLI tests, the four behavioural ones watched to
fail first against unmodified code (`pause s-deadbeef` printing "takes effect", `msg
no-such-slug` printing "queued for", both exiting 0). Six mutations, each killing its own
test and nothing else: refuse→fall-back-to-raw, dropping the slug arm, ambiguity picking
instead of refusing, `Resume` forgetting the raw arm, released slugs resolving, and dropping
the output fence. The fence test's FIRST version passed with the fence removed — it addressed
the session by id, and `Target.String()` returns the raw argument for that form, so no
peer-controlled value was ever rendered; it is written through the slug form for that reason,
and the note is kept in the test. Verified against a copy of the live ledger in a scratch
repo: all four currently-open slugs and a live short id resolve to the right session and echo
which one, while the long-released slugs correctly refuse.

**The Codex code pass then found six defects, each reproduced by a test watched to fail
before it was fixed.** Three were introduced by this change and three were latent in what it
assumed. (1) `sessions.label` has NO unique index and `Hello` never checks one, so a label
held by two sessions resolved by `LIMIT 1` and silently picked one — also a REGRESSION, since
storing the label had made both readers' label arm match every session carrying it. (2) The
short-id arm matched with SQL `LIKE`, and both of that operator's defaults are wrong here:
metacharacters were unescaped, so an open claim slug of the form `s-1111aaa_` was captured by
the SHORT arm — which runs first — and resolved to whichever session's label it happened to
match; and the default collation folds ASCII case, so `s-1111AAAA` resolved though this
change documents matching as exact. Both were verified directly against sqlite before the
fix. Matching now happens in Go over the whole session table, which is small and on the write
path only. (3) Storing the CANONICAL id can widen a row's reach, because the readers still
match on label: where one session's label equals another's id, a pause written against the id
matches the other's label arm too, and the old code — storing the raw argument — hit only
one. Resolution now refuses that collision, and refuses a session whose id is the reserved
word `all`; migrating every legacy row so the readers could drop their label arm is the
alternative, and it is a change with its own evidence to gather. (4) `Resume` matched the
resolved id and the raw argument but not the resolved label, so a legacy row was liftable
only by retyping the exact string it was paused with. (5) is (2). (6) "the compiler is the
guard" was overstated: `Target`'s fields are exported, so a hand-built or zero value was a
legal argument that would insert an unresolved target. The type stops a STRING; an unexported
marker only `ResolveTarget` can set is what stops a forged `Target`, and all three write
boundaries now check it.

**Residuals** — Resolution is exact, so a target differing only in case is refused rather
than matched. A message to an ended session stays queued against its id and is delivered only
if that id ever helloes again. The three slug-addressed messages measured above are not
retroactively deliverable; this stops the next one being lost, it does not recover them. And
the readers still carry their label arm, which is why resolution has to refuse the id/label
collision rather than simply store the id: removing that arm needs a migration of every
legacy `controls` and `inbox` row, which was not attempted here.

## D-014 — A DM asks whether the recipient is there, because there is no offline delivery

2026-09-07 · measured on the live stack, then a Codex code pass and a second-model review

**What was wrong** — `buddylist dm nobody-here-12345 "…"` printed nothing and exited 0.
Nothing was delivered and nothing could have been: a chat server holds no mail for a name
with no session. The refusal did arrive — the journal has it, as a roomless system row
reading `server error 401 No such nick`, naming neither the recipient nor the message, and
written after the CLI process was already gone. That is the anatomy of a silent failure:
the evidence exists, and it reaches nobody who could act on it. It mattered now because
the next roadmap item is a nightly job that DMs the operator when a build goes RED — at
the hour their client is least likely to be running. `DM`'s own doc comment claimed
"same visibility rules as `Say`", and it had none of them.

**What shipped** — A `Presence(names …string) (online []string, known bool, err error)`
method on the daemon's `Conn` seam. IRC answers it with ISON (a lookup with a definite
answer, unlike waiting out the ABSENCE of an error numeric after a send); TOC answers
`known=false`, because there a client learns who is online only by adding a name to its
buddy list and waiting for an `UPDATE_BUDDY` that never comes for a name with no session.
`Daemon.DM` refuses ONLY a definite "not online", naming the recipient and sending
nothing. Every other outcome — a backend that cannot answer, a probe that errors, a
server without the command — sends exactly as before.

**What it deliberately does not do** — It does not confirm delivery: the window between
the answer and the send is the one `Say` already accepts. It does not journal an outbox
row for DMs; a delivered DM already echoes back into `@dm` via `echo-message`, and adding
rows to the `@sent` outbox would widen the substring containment that D-009's self-alert
suppression runs over. And it does not hold a message for an operator who is away —
store-and-forward was considered and cut: the nightly DM is decoration, the durable
channel is elsewhere, and a refusal a script can see is what makes that split honest.

**The measurement that mattered, and it bit before it was believed** — ergo 2.19.1 answers
`ISON jsizl` with `303 me jsizl` — the name list as an ordinary parameter, no colon —
but `ISON jsizl SmarterChild` with `303 me :jsizl SmarterChild`. Reading only the trailing
parameter passed every hermetic test, which had been written against the multi-name shape,
and then reported nobody online for the SINGLE-name query a DM actually makes: it would
have refused every DM on the machine. Caught by driving the real server, not by the suite;
both shapes are now a table.

**Two review findings, both about the same wrong idea** — The first implementation queued
waiters, because RPL_ISON carries no request tag and replies were assumed to come back in
the order the queries went out. (1) They need not: waiters enqueued under one lock and
written under another can reach the wire in the opposite order, so one caller gets
another's answer — reproduced, 2 failures in 100 runs under `-race`, and the wrong answer
refuses a DM to somebody who is right there. (2) A query the server answers with anything
but a 303 leaves a reply owed forever: an over-long line draws `417 Input line too long`
and nothing else, on a connection that stays up, so every later query on it was
misaligned — and the length was caller-supplied, via the DM's own recipient name. What
shipped instead: ONE outstanding query per connection, held across the whole round-trip,
so a reply can only belong to the query that is waiting for it; a query that goes
unanswered retires presence on that connection, and every later caller is told "cannot
tell" rather than being handed a stranger's reply; and an over-long query is refused
before it reaches the wire.

**Residuals** — Every failure of the mechanism degrades to the old unchecked send, which
is the only safe direction: refusing a reachable recipient loses a message the previous
code would have delivered. A retired connection stays retired until the daemon reconnects.
The TOC backend keeps its historical best-effort semantics entirely. And the comparison
uses the daemon's `fold` (`ToLower` + `TrimSpace`, no NFC), which is right on ergo's ASCII
casemapping and would mis-compare the rfc1459 bracket characters on a server configured
for it.

## D-015 — The roster is the orchestrator's view: labelled ages, fitness, and an observed context footprint

2026-09-20 · issue #5, then a model-diverse design pass (Codex + a second-model critique),
measured against this box's ledgers and transcripts

**What was wrong** — `buddy sessions` printed one unlabelled age column beside the word
`live`, and it dated the last TOOL CALL. It reads as uptime and is not: measured on a
334-row ledger, a session that had been running 10.7 hours and had just heartbeated
rendered as `9s`, and the three live rows understated true age by 9.4, 10.7 and 13.3
hours. `sessions.started` was recorded on all 334 rows, 0 of them null and 0 of them
after `last_seen` — and surfaced nowhere, in any command, so "hand the next item to the
session that started after yours" was answerable from the table and from nothing else.
78 of the 334 rows had `started == last_seen`: registered once and never beat again,
indistinguishable at a glance from a session that started this instant.

Two more facts were in the ledger with no output at all. `buddy pause all` leaves every
row reading `live`, so an orchestrator hands out work and the gate DENIES the recipient's
next mutating call with nothing having warned it — the state word lying by omission, the
same defect class as the age column. And "what is this session holding" was answerable
only backwards, through `buddy ls`.

**What shipped** — Every number carries its own word, in fixed columns: `started 6h  seen
5m`, with the state cell carrying its own age only when the state IS a dated event
(`ended 3h`), because that reads correctly in English and `live 4s` is precisely the
misreading. `--by seen|started` chooses the key; both sort DESC (a flag that changed key
and direction together would be two changes under one name, and under DESC "the one that
started just after mine" is the line above, exactly as adjacent), ties break on
`session_id` because both keys are whole seconds and a script spawns four sessions in
one, and an unknown key is REFUSED the way an unresolvable target is. A `*` gutter marks
the caller — a gutter and not a word, since a label is peer free text and a peer labelled
`you` must not be able to wear the marker.

Trailing the id, where the old `last beat` note already lived so the fixed columns never
move: `PAUSED` (asked of `PausedFor`, so the applicability rule stays in one function),
`claims N` (the count, not the slugs — 128 bytes of peer text each, several per session,
into every reader's context), and the context footprint.

**The context footprint** — `beat` reads the TAIL of the session's own transcript, whose
path the hook JSON already carries, and records one row per session: the newest assistant
turn's token accounting, its model, and the turn's own timestamp. Never any message text:
everything here is rendered into other sessions' context windows. Measured on this box's
seven transcripts — files 0.6–2.3 MB, single lines up to 267 KB, and the last usage-bearing
line beginning 2.8–11.0 KB from EOF — so the read is the last 64 KB, six times the measured
worst case, and a record outside that window means "no new observation", never a bigger
window. Cost, measured 2026-09-20 on a 2.3 MB transcript: **16.5 ms per beat with the
capture against 16.4 ms without**, inside a 100 ms budget.

The row says `prompt 90k turn 4s`, never "context left": the number is the last prompt the
model was HANDED (input + cache read + cache write, because a cached token occupies the
window exactly like a fresh one), and the turn's own age prints beside it always, because a
peer that has since compacted from 90k to 20k is exactly the wrong session to pass over.

**What it deliberately does not do** — It does not compute a percentage from the model
name. Measured 2026-09-20: a session running Opus with the 1M-token window writes
`"model":"claude-opus-5"`, byte for byte what the 200k variant writes. 90,499 tokens is 45%
of one and 9% of the other, so a percentage inferred from that string is not an
approximation but a fabrication. The denominator is declared by the operator
(`BUDDY_CONTEXT_WINDOW=1M`) or no percentage prints. It keeps ONE row per session rather
than a history — the routing decision needs the latest observation and its age, and an
append-only table would be one row per tool call across 334 sessions — and that row is
read only through a join on the CURRENT incarnation, so a superseded incarnation's
footprint is never reported as this one's.

**Cut** — A header line (nothing else in buddy prints one, it would print over an empty
ledger, and the common case is one row quoted into chat where the header is gone). A
stored git branch per session (the transcript has one, but it is redundant with the
worktree in a worktree-per-branch flow and identical across sessions sharing a checkout).
A self-declared `role` (it is a second label, and `--label reviewer-1` does it today). An
undelivered-inbox count (beat drains the inbox every tool call, so a non-zero count marks
the session idle at its prompt — which is the session that CAN take work, read as the one
that cannot). `--json` (deferred until the column count forces it; the annotations are
labelled and parseable by eye today).

**What the code review changed** — A Codex pass over the implementation found three
things worth fixing and they shipped with it. (1) `RecordContext` read the current
incarnation itself, which is only "whoever is live now": a beat that read a transcript,
lost its session to a bye and a revival, and then arrived would have stamped the NEW
incarnation with the OLD one's number. The caller now reads the identity BEFORE the
transcript and passes it, and a mismatch drops the sample. (2) The roster reads sessions
and samples in two queries, so a revival between them could attach a sample to a
superseded row; the sample now carries its incarnation and the renderer compares. (3)
`os.Open` on a FIFO with no writer blocks forever — inside a 100 ms hook — so the path is
stat-ed for a regular file first. Also: token counts outside the plausible are refused
rather than summed into a negative prompt, an overflowing `BUDDY_CONTEXT_WINDOW` reads as
undeclared rather than wrapping into a 384-token denominator, and a stray operand
(`buddy sessions stray --by started`, where Go's flag parser stops at `stray` and the
flag is never read) is refused.

**Residuals, and what became of them** — Five were filed as issues the same day and
four are closed by D-017 and D-018: the one-second ordering (#7), the positional scan
(#8), the unsampled record behind a huge tool result (#9), and the column shift (#6,
which turned out to be a forgery and not a cosmetic issue). The fact the ledger could not
tell mid-turn from idle-at-prompt became D-016.

What remains is the one that cannot be fixed and should not be: the footprint is a LAST
OBSERVATION, not a live reading. A session that compacted or grew since its last sample
is reported as it was, which is why the turn age prints unconditionally and why the row
says `prompt` rather than "context left".

## D-016 — Idle is reported; busy is never inferred

2026-09-20 · proposed by a second-model design critique of D-015, built the same day

**What was wrong** — The ledger could not tell a session mid-turn from one that finished
ten minutes ago and is waiting for its operator. Both are live, both are inside
`StaleAfter`, and the busy one looks FRESHER: `last_seen` is the last tool call, so the
roster's default order ranks the session hardest at work first — the exact inverse of
"who can take the next task", which is the question the roster exists to answer.

**What shipped** — A `Stop` hook line (`buddy idle`) and a `session_idle` row per
session, keyed to the incarnation that reported it. `Beat` deletes the row in its own
transaction, because a tool call IS a turn in progress and the heartbeat and the end of
idleness are one fact; splitting them would take a second write lock per tool call for a
row that is usually not there. The roster prints `idle 7m` on a live row whose reporting
incarnation is still the current one.

**The asymmetry is the design.** A row means "reported idle at its prompt". NO ROW MEANS
UNKNOWN, never busy. `Stop` is a line in a settings file that a machine may simply not
have — the same opt-in every other hook here has — so a fleet with it unwired reports
nobody idle, and a reader that took absence for evidence would conclude that every
session on it is mid-turn. One-sided evidence, said one-sidedly.

**Only Stop, not UserPromptSubmit.** The mark is cleared by the next `beat`, and the
first tool call of a turn lands within a second of the prompt that started it. A second
hook line would buy that second and cost every operator another line to install.

**What the code review changed** — A Codex pass found one case and one wording. (1)
`IdleSessions` joined on the incarnation but not on liveness, so a session that ended
between a caller's two queries — the roster reads sessions, then idleness — would print
`idle` off the earlier snapshot: available, and gone. The join is now live-only, which is
the one place the two tables differ on purpose: a context footprint stays true after a
session ends, "waiting for work" does not. The claim count moved to a (session,
incarnation) key for the same reason. (2) The release diagnostic said a claim was open
under an "EARLIER incarnation of this session"; a release delayed across a bye and a
hello arrives with the OLD incarnation while the open claim belongs to the NEW one, so it
named the wrong side. It says DIFFERENT now.

**Residuals, and what became of them** — Both of the fixable ones were filed and closed
the same day. The delayed Stop (#11) is fenced by the TURN's end time, read from the
transcript the payload does name: `since` now dates the turn rather than the scheduling
of the hook, and a turn that ended before this incarnation registered is refused. With no
readable event time the check cannot run and the old behaviour stands, because refusing
on an unreadable transcript would turn the feature off silently. The tool-less turn (#10)
has an optional `busy` verb on `UserPromptSubmit`; the mirror case has always been live
and stays so — a delayed beat from a dead incarnation already updates its successor's
`last_seen`.

What remains is by design: the annotation is double-edged, which the README says out loud:
an idle session is the one that can take work AND the one that will not see a `buddy msg`
until its next tool call, because inbox delivery rides the heartbeat. Routing to it still
needs a human to poke it.

## D-017 — A column is one token: `fence.Field`, and a marker rather than quotes

2026-09-20 · issue #6, from a Codex code pass on the roster work, reproduced on a
throwaway repo

**What was wrong** — Every listing prints peer text in a fixed-width column (`%-24s`),
which is a MINIMUM width and not a maximum. `fence.Line` stops a newline fabricating a
whole row and never claimed to stop anything else, so a label with a space in it owned
the columns after it on its own row:

```
buddy hello --label 'aaaaaaaaaaaaaaaaaaaaaaaa ended 0s'
buddy sessions
#   aaaaaaaaaaaaaaaaaaaaaaaa ended 0s live  started 0s  seen 0s  /tmp/x  (s-forge)
```

A reader splitting that row on whitespace gets state=`ended`, age=`0s`, for a session
that is LIVE, with the real state one field further along. Measured, not hypothesised.

**What shipped** — `fence.Field(s, max)`: `Line`, then every space rendered as `␣`, the
way `Line` renders every line break as `⏎`. Applied to the label and slug columns of
`sessions`, `ls` and `whose`. A literal `␣` in the value is escaped first, for the reason
`Line` escapes a literal `⏎`: so that every marker in the output provably came from the
fence. `check-fence.sh` counts `fence.Field` as fencing, or the gate would have flagged
every call site it was added to.

**Quoting was tried first and cut.** `strconv.Quote` makes the boundary visible to a
human and changes nothing for a reader: `strings.Fields(`"a b"`)` is still two tokens, so
the forged state still lands in field 2. The test for it failed on exactly that, which is
the argument for writing the test as the property ("one value, one token") rather than as
the rendering.

**Also cut: refusing a bad label at intake.** It was the first half of the filed issue and
it is the worse fix. `hello` runs from a hook line that ends in `exit 0` with stderr
swallowed, so a refusal there means a session silently fails to register and the whole
feature turns off for it — and intake validation could never repair the labels already
sitting in ledgers, which the display fix does.

**And the same defect one column left** — the caller's gutter was `"* "` or two spaces,
so the marked row had one MORE whitespace-delimited field than its neighbours. Found by
the test written for #6. Every row now carries a gutter: `*` for the caller, `-` for the
rest.

## D-018 — The capture samples the newest turn, escalates once, and records what the model is

2026-09-20 · issues #7, #8, #9, plus the operator's ask for the model on the row

**What was wrong** — Three properties the capture claimed and did not have. (1)
`turn_at` was whole seconds, so two turns inside one second compared EQUAL and the write
guard's `>=` let the older one overwrite the newer — a guard that held only when the
turns were a second apart. (2) `lastUsage` returned the last usable record POSITIONALLY,
which is the newest only because transcripts are appended to; the comment said so, which
is the definition of an untested assumption. (3) A record behind a single oversized tool
result (267 KB measured, against a 64 KB window) was never sampled at all, and the row
then aged its previous observation instead of saying it had stopped reading.

**What shipped** — `turn_ms`, milliseconds, the only column in the ledger that is not
whole Unix seconds, and commented as such where it is defined. The scan now takes the
greatest timestamp in the window. One escalation, from 64 KB to a 512 KB cap, paid only
when the first window has already missed — a cap and not "read until something turns up",
because that makes a hook's cost a function of how long a session has run.

**Model and effort on the row**, which is what the operator asked for: `claude-opus-5/xhigh
prompt 377k turn 7s`. Both come from the same record as the counts, and both print only
when the transcript recorded them. Measured over this box's seven transcripts, each
discriminates — six `claude-opus-5` to one `claude-fable-5`, six `xhigh` to one `high` —
and two sessions on the same model at different efforts are different instruments to hand
a task to.

**The migration DROPS `session_context`.** It is the one table here that may be thrown
away, and only because of what it holds: every row is re-derived from a session's own
transcript on its next tool call. Claims, pauses and messages are records and none of
them may be dropped to change a column.

## D-019 — The conflict set is reported whole, a claim can be narrowed by its holder, and a message is signed with a name that resolves

2026-09-20 · three items from a field wishlist written after a 14-session orchestrated
run on the reference repo (§5b and §5d there), measured against that run's ledger

**What was wrong** — Three all-or-nothing shapes, each correct and each costing a round
trip or a stuck file. (1) A claim naming four paths was refused WHOLE because one was
held, and the refusal named that one conflict; the three free paths were not taken and
the next collision cost another try. The coordinator had issued an assignment whose
scopes could not all be satisfied, and neither side could see that until the claim was
tried. (2) A holder finished with two of its four scopes narrowed them by SAYING SO — its
description ends "Docs scopes RELEASED." — and the recorded scope was unchanged, because
release was whole too; the gate kept enforcing four paths, a waiter correctly stayed off
two of them, and nothing contradicted either party. Acquisition fails loudly; a prose
release fails silently in both directions. (3) Session A messaged B twice signing
`--from <its claim slug>`, and B's reply to that slug bounced `no such target`: A's claim
had been REFUSED, so it never opened, so the slug resolved to nothing. Measured in that
ledger: of 111 direct messages the sender was a session label ONCE; 104 times it was a
claim slug, 14 of which no longer resolve because the claim closed — and a refused
claim's slug never appears in the ledger at all.

**What shipped** — `claim --dry-run`: the whole conflict set, one `REFUSED:` line per
collision naming the requested scope, the held scope as claimed, the holder and the slug,
then `would claim: …` for the free set; it writes nothing and exits non-zero on any
conflict so a script cannot read "some of it was free" as "go ahead". The real refusal
now carries the rest of the set too (`ErrRefused.More`) and prints it, because both run
the SAME computation (`scopeConflicts`), which is what keeps a forecast from disagreeing
with the refusal it predicts. `release <slug> --scope <path>…`: the holder hands back
named scopes, exactly as claimed; the claim's `renewed` moves; releasing the last scope
releases the claim, so nothing can sit open over nothing; fenced by incarnation as
`release` is and diagnosed by the same `ErrNoRelease`. `msg` signs with the calling
session's LABEL — the one name D-013 guarantees resolves — read from the environment
only (`BUDDY_SESSION`, then `CLAUDE_CODE_SESSION_ID`), and an explicit `--from` that is
not the label is kept as a tag after it: `alpha (r1701-console-quoting)`. Label first
because the inbox fences the sender to 64 bytes and truncation must cost the tag, never
the address. No session in the environment is the operator at a terminal, whose sender
is what they typed or `operator`, exactly as before.

**What it deliberately does not do** — No partial ACQUISITION. It was the first thing
asked for and it is the wrong fix: a claim that comes back holding three of four paths
has changed shape under its caller, whose next edit lands on the fourth as if it were
held. D-001's "granted whole or refused whole" stands; the dry run is how a request gets
narrowed in one round trip instead of three. No containment on release: releasing
`pkg/sub` from a claim holding `pkg` is REFUSED naming what is held, because prefix scopes
have no subtraction (D-002 cut glob math for the same reason) and the only honest result
would be `pkg` still held with success reported — the prose failure with a command in
front of it. The dry run runs on autocommit reads, not in a transaction: the ledger's
`_txlock=immediate` would make a read-only forecast take the WRITE lock. The price is
that a peer's claim can land between its two reads; it is a forecast, and the real
`Claim` re-checks everything under its own lock. `msg` never consults `whoAmI`'s cwd
inference for the signature: that arm can refuse or guess, and a message must never be
refused for want of a name.

**What the code review changed** — A Codex code pass found one defect and one divergence,
and both shipped fixed the same day with a test watched to die under a mutation. (1) The
dry run checked the session id and liveness but not the INCARNATION, so a caller that had
resolved itself before a bye and a hello was forecast "free" and then refused at the write;
it now takes the incarnation and checks it as `Claim` does. (2) `Claim`'s slug check
returned before the scope scan, so a request colliding on slug AND scope was forecast with
two conflicts and refused with one — the exact disagreement the shared computation exists
to prevent. Both paths now call ONE `allConflicts` (slug first, then every overlapping
pair). The pass also named four ways the tests could pass a wrong implementation — partial
acquisition on refusal, a mixed partial release committing the held scope before refusing
the missing one, one requested prefix reporting only its first holder, and deleting the
claim row instead of marking it released — and each got a test and a mutation that died.
Fifteen mutations in all: fourteen caught, and one turned out EQUIVALENT — it rewrote the
claim's description before the conflict check, inside the transaction the refusal rolls
back, so nothing observable changed; the property it aimed at is held by the transaction,
not by a check. A confirmation pass found every finding closed and no new defect.

**Residuals** — A refused claim still leaves no trace, so `whose`/`ls` cannot show that
somebody WANTED a scope; the address now travels on the message instead. The tag after
the label is free text and is fenced like every other sender. The stamp resolves only
while the rendered label equals the stored one: the inbox fences the sender to 64 bytes,
so a hand-chosen label over that, or one carrying a space or a newline, is shown altered
and no longer matches D-013's exact resolution. Default labels are short and plain. The
dry run's two reads can straddle a peer's write; it is a forecast, and `Claim` re-checks
under its own lock.

## D-020 — The roster says whether a peer's prompt cache is hot, from the tier its own transcript recorded

2026-09-20 · operator's ask, measured against this box's transcripts the same day

**What was wrong** — A handoff to a peer whose prompt cache has lapsed rewrites that
peer's whole prefix on its next request; one whose cache is warm costs a fraction of
that. The API offers two lifetimes, 5 minutes and 1 hour, a session is on one or the
other (and drops from 1h to 5m under usage overage), and nothing on the roster said which
— nor could anything infer it: the model string is identical under both.

**What shipped** — Two raw columns on `session_context`, `cache_5m` and `cache_1h`: the
tokens the turn wrote into the cache at each tier, straight from the transcript's
`usage.cache_creation` object (`ephemeral_5m_input_tokens`, `ephemeral_1h_input_tokens`).
Measured: 2371 usage records across this project's transcripts, every one carrying the
object, every write on the 1h tier; one record in 2371 was a pure cache read that wrote
nothing. The reader takes the tier from the NEWEST record that wrote anything, which is
usually but not always the newest record — a pure read hits the cache the last writer
built. The roster renders `cache 1h hot 48m` / `cache 1h cold 3m`: the tier, the verdict,
and the remaining or elapsed time, so every number carries its word (D-015). The clock is
the turn's own time, which already prints beside it: the cache lifetime restarts on each
request that uses the cache, and the newest assistant record is the ledger's closest
observation of the last request. Both tiers in one turn prints `cache 1h+5m` and is judged
hot by the shorter, because the prompt is wholly hot only while every part of it is.
Schema 5 rebuilds `session_context`, the one table a migration may drop (D-018).

**What it deliberately does not do** — No default tier. An older harness records no
`cache_creation` object, and a record without one prints nothing about the cache: it was
not on the 5m tier, it was silent, and a default would be a claim about the session. No
stored verdict: "hot" is a function of the clock and belongs to the reader, so the ledger
keeps counts and the roster computes. No inference from the model or the account.

**What the code review changed** — A Codex code pass found two ordering defects, both fixed
the same day with tests watched to die under mutation. (1) The tier was chosen BEFORE the
record's counts were validated, so a record rejected for an implausible count had already
set the cache tier — the timer described a turn the clock beside it had discarded. Validation
now precedes both selections. (2) The row's write guard orders by `turn_ms` alone, but the
tier comes from a different record (the newest WRITER), so two samples of the same turn can
carry different tier evidence and the later write won whatever it said. The tier now carries
its own clock, `tier_ms`, and its columns are guarded by it: older tier evidence on an equal
turn does not regress the tier, a tierless newer sample keeps the tier it cannot contradict,
and a fresh incarnation takes the new row whole. Also pinned by test: the newest writer wins
by timestamp and not position, a tie goes to append order, a sidechain, a malformed
timestamp or a rejected count sets no tier, and the provenance clock travels through
`beat` (a beat that dropped it let older evidence win on an equal turn — caught by the one
mutation that survived the first round, and closed with an end-to-end test). Twelve
mutations in all, every one caught.

**Residuals** — The turn's timestamp is the RESPONSE's, so the true expiry is earlier by
the length of that response — seconds to a few minutes on a long thinking turn. The
remaining time is printed so a reader at the edge can see the edge; a reader who needs a
margin takes one. A session in overage drops to 5m on its next request, and the row says
so only once that request has been recorded.

## D-021 — A message takes its body from stdin, and the cap is measured on what the recipient will see

2026-09-20 · issue #14 / wishlist §13, measured on this binary the same day

**What was wrong** — A broadcast was sent with a heredoc body. `msg` took its text
from argv only and silently ignored stdin, so what ran was a usage error and the fleet
was never told. The field report named TWO causes and only one was there: the usage
path exits 1 and always has (`Run` returns 1 for any command error), and the `rc=0` in
the report came from a `| tail -5`, because `sh` has no pipefail — the documented trap,
biting something other than a check run. Measured both ways on one binary: unpiped 1,
piped 0. The surviving cause is enough on its own, and no exit code would have fixed
it: a caller who redirects instead of piping still has to notice its text was discarded.
The inconsistency was inside one binary — the hook verbs read stdin through `readHook`
while `msg` threw the same channel away without a word.

**What shipped** — `msgBody`: argv when present, otherwise stdin, with the source chosen
first and ONE cap applied after. A TTY is never read, reusing the existing `stdinIsTTY`
that guards `readHook` for the same reason (`buddy hello` used to hang waiting for hook
JSON that was not coming), so a human who types `buddy msg alpha` gets the usage line and
not a cursor. A heredoc's trailing newline is trimmed BEFORE the cap, so it costs nothing;
interior newlines are kept and fenced. An empty pipe is refused as empty rather than as a
usage error, because "you forgot the text" is the wrong sentence for a caller who did not.
Also `msg --dry-run`, the third ask on the issue: it resolves the target and the sender,
prints the byte count, and sends nothing — the same idiom as `claim --dry-run` (D-019).

**THE CAP IS MEASURED ON THE RENDERED BODY, not the bytes supplied.** `fence.Line`
expands every line break to `⏎`, which is THREE bytes, so a raw-byte cap equal to the
rendering cap does not prevent recipient-side truncation. `renderedLen` measures the body
as the inbox will show it (a `math.MaxInt` max cannot truncate, so it measures the
expansion alone) and the refusal names the rendered size, the cap and the difference.
Reading from stdin is separately bounded at 64 KiB before trimming, because stdin has no
natural end and the fence can shrink a body as well as grow it.

**What it deliberately does not do** — ARGV STILL WINS when it is present, and stdin is
then not read. Refusing that ambiguous case was considered and cut: a script that passes
text and happens to have stdin redirected is doing nothing wrong, and breaking it to catch
a typo trades a live failure for a hypothetical one. The residual is real and stated:
`buddy msg all "note:" <<EOF` still drops the heredoc. No `--check` spelling: the issue
asked for one, and `--dry-run` is what this repo already means by it.

**What the code review changed** — A Codex code pass found three defects, all fixed the
same day with tests watched to die under mutation. (1) THE CAP WAS REACHABLE AROUND: it
checked stdin only, so a 4097-byte argv message walked past the new guard into the ledger
and was shown cut — a guard that exists and can be stepped around is worse than none,
because the refusal now reads as a promise. (2) THE RENDERED-LENGTH DEFECT above, with its
exact input: `strings.Repeat("x", 4094) + "\nZ"` is 4096 bytes, renders to 4098, and the
trailing `Z` vanished from what the recipient read while the sender was told it sent. (3)
`--dry-run` against an ENDED target printed "this is queued against its id" from the
shared resolver and then "nothing was queued" from the preview — one command contradicting
itself, and the false half is the exact shape of assurance this issue is about; the
resolver gained a quiet variant and the preview says what a real send WOULD do. The pass
also predicted the one mutation the first tests would have survived — truncating the body
to 64 bytes, which an assertion looking for only 64 `x`s could not see — and that case now
asserts the whole body. Thirteen mutations over two rounds, every one caught.

**Residuals** — The argv heredoc case above. The cap covers the body, not the sender tag
that renders beside it. `stdinIsTTY` is a character-device test, so `buddy msg alpha <
/dev/null` is treated as a terminal and refused; that loses no message and is the same
test `readHook` already applies, so the two verbs agree.

## D-022 — A stale claim refuses exactly like a fresh one, and now says so

2026-09-20 · issue #18, settled against the code and pinned by test

**What was wrong** — Nothing in the behaviour. The rule was never ambiguous:
`scopeConflicts` tests `state='open'` and nothing else, so staleness has no bearing on
acquisition — staleness marks, it never reaps (invariant 11). It was UNWRITTEN, and on
one afternoon two careful sessions measured OPPOSITE answers ten minutes apart. Session
A broadcast "a stale claim does not block a new one" from a single observation of a
roster row, then retracted it itself: the likelier explanation was the boring one, a
holder releasing around the moment it claimed and a listing rendering a row that was
already dead. Session B had measured the refusal twice across four scopes. Meanwhile a
third session sat blocked ~3h while a fourth insisted the scope was free — **both
reading truthfully, and reading different things.**

**What shipped** — The rule is now written in three places a reader looks (this record,
the charter, `CLAUDE.md`) and pinned by tests that assert it in both directions: a stale
claim refuses, a month-old claim refuses, and the ONLY things that free a scope are the
holder's `release`, `hello` orphaning a dead incarnation, and `sweep --force` — the
operator's explicit act. The refusal and `claim --dry-run` now also SAY when the holder
has gone quiet: `— STALE: holder last renewed 3h ago; it still refuses, so ask the
operator`. The verdict is unchanged and the wording is deliberate — a blocked session
must not read the note as permission to take over. The holder's `renewed` rides
`Conflict` AND `ErrRefused`, because `cmdClaim` rebuilds the set from the error on the
refusal path and a forecast that annotates differently from the refusal it predicts is
the exact defect D-019 exists to prevent.

**A REFUSAL NOW ALWAYS PRINTS THE SET.** D-019 printed it only when there was more than
one conflict, on the reasoning that a single conflict is already stated by the error
line. That held until the line acquired something the error does not carry — the
holder's staleness — and then the one case that needed it most was the one that skipped
it: a session blocked on a SINGLE quiet holder, which is precisely the 3h incident.
Found by a mutation control coming back red, which is the entire reason a negative test
needs one.

**What this turned up on the way, and it belongs to issue #15** — `Bye` stamps
`sessions.ended` and touches no claim row, because orphaning happens in `hello` and
`sweep` and never inline in `bye` (invariant 12: a delayed bye from a dead incarnation
must not orphan a live one's work). So **a session that finishes and exits cleanly
leaves its scopes held, blocking every peer, with the holder gone.** That is the
mechanism behind the incident in issue #15, where a coordinator verified a session's
work was landed and its tree clean, told it to exit, and it still held two claims. The
test for this was written expecting the opposite and was corrected to the code, not the
other way round; the behaviour is deliberate and the gap is that nothing warns at exit.
Not fixed here — it needs a decision about where an exit check belongs.

**What it deliberately does not do** — No takeover, on any timer. A silent takeover of a
scope another session believes it holds is worse than a refusal, and `sweep --force`
stays the only path. No change to what `ls --all` renders: a released claim's row is
already labelled `released`, and the field report's confusion was reading that label as
a live hold rather than the label being absent.

## D-023 — `whose` answers with BOTH registers, and the claim comes first

2026-09-20 · issue #13, two sessions measured wrong on one day

**What was wrong** — `buddy whose <path>` reports who has uncommitted CHANGES to a
path. Its NAME reads as ownership, so it was used to answer "is this claimed?" — and it
is silent in exactly the state that matters most: a session that has claimed a path and
not yet started editing it has no dirty row, and that is the state every session is in
immediately after claiming. The two answers coincide often enough to look reliable and
diverge precisely when the question is load-bearing. Measured: two sessions in one day
read a `whose` result as "unheld" and were wrong; one decided it could write a shared
file on that basis, and the file had been claimed by another session for hours. It
caught the error only because a coordinator happened to hold the claim table and
contradicted the answer. The output was honest about its limits once read; the defect
was that nothing prompted the reader to read them.

**What shipped** — Both registers, labelled, with the CLAIM first because it is the one
that reserves anything and the one the commit gate adjudicates:

```
internal/api/server.go
CLAIMED BY   api-work    alpha    held 2h
             scopes: internal/api
DIRTY IN     (none)
```

`ClaimsTouching` reports both relations and KEEPS THEM APART. `CLAIMED BY` is invariant
14's containment exactly — a claim on `internal/api` covers `internal/api/server.go`, a
claim on `internal/apiv2` covers nothing — and is the relation the gate adjudicates.
`HELD UNDER` is a claim on a path INSIDE the one asked about; it reserves nothing about
that path and says so on its own heading. Merging them would answer a question the
reader did not ask with the authority of the one they did.

**IT CONSULTS NO FILESYSTEM.** The first shape took an `asDir` flag that `whose`
computed with `os.Stat`, and applied the held-under arm only when the directory existed
LOCALLY. A peer claiming `internal/newpkg/foo.go` — the natural state for a package
somebody has just reserved IN ORDER TO CREATE IT — therefore read back as
`CLAIMED BY (none)`: this command's own defect wearing the new feature's clothes. A
claim is declared intent and can name a path that exists nowhere yet. The dirty register
still consults the filesystem, because git only knows files that exist; the claim
register must not. The test for it carries no `MkdirAll` and says why.

**`(none)` IS PRINTED, never omitted.** The entire defect was a silent answer read as
"nobody has this", and an absent line and a "none" line are the two things a reader
confuses. The same reasoning put a label on the no-holder path: its sentence is "no
session has it recorded", which used to be the whole answer and now sits directly under
a CLAIMED BY line, where unlabelled it reads as contradicting the claim two lines above.

**What it deliberately does not do** — No rename to `buddy dirty`. The issue offered it
as an alternative and it is worse: every existing caller, every doc and every habit
names `whose`, and the reader who needed this answer was asking the right question of
the right command. Reporting both is what the name always promised. Neither register
becomes a lock: the dirty rows stay OBSERVATIONS (invariant 10) and the claim block
changes no enforcement.

**A `fence.Field` DECISION WAS OVERTURNED to ship this.** `Field("")` returned `""`, with
the recorded reason "nothing to separate". That reason takes the wrong reading of
"separate": it is about the VALUE (nothing inside it to separate) where D-017's guarantee
is about the ROW (the column must separate from its neighbour). A whitespace-splitting
reader does not see a blank column — it sees the NEXT column's value in this one's
position, which is D-017's own failure (issue #6) arriving from the opposite side: #6 was
a label too WIDE, this is one too NARROW. It is reachable from NON-empty values, so no
caller can prevent it by refusing empties: `Line` strips non-printing runes, so a label of
a single ESC — which `hello` accepts, requiring only non-empty — fences to nothing, and
`CLAIMED BY api-work    held 0s` then reads with `held` in the label column. `Field` now
returns `∅` for an empty result and escapes a literal `∅` first, as it already does for
`␣` and `Line` does for `⏎`. The property now holds for EVERY input by construction, so it
is stated as one: `FuzzFieldIsOneToken`.

**What the reviews changed** — A Codex pass found two defects and predicted one surviving
mutation. (1) `ClaimsTouching` read the matching claim ids and then materialized them in a
SECOND query, so a refresh between the two — which keeps the claim id and REPLACES its
scopes — could print `CLAIMED BY api-work` with `scopes: docs`, a claim reported as
covering a path its recorded scopes do not cover; it now reads ONE snapshot and filters in
Go, the same consistency `buddy ls` already has. (2) `ClaimInfo.Stale` reports an
unrecorded `renewed` as stale, because a zero time is 56 years ago, so `whose` applied
`staleNote`'s zero-clock guard too. A Fable pass then found the `os.Stat` gating above,
the missing "still refuses" qualifier (D-022's own wording, absent from the one command
the incident was about), a README sample still showing the one-register shape under a
comment promising two, and a sentence in `ErrRefused.Error()` — "`claim --dry-run` lists
the whole set" — that became false once the refusal started printing the set two lines
above it. It also corrected the rationale recorded here: the old fence test did not
contradict ITSELF (its loop never included `""`); the stated PROPERTY contradicted the
case, and the overturn rests on the reachable forgery alone.

**Residuals** — The claim block is capped at 20 rows like the dirty block. A path can be
claimed by a session whose worktree is elsewhere; the claim register does not print
worktrees, because a claim is repo-wide while a dirty row is per-worktree. `whose` does
not mark the asker's OWN claim the way `sessions` marks its own row. `buddy claim ""` is
still accepted — `cmdClaim` refuses only a leading `-` and `store.Claim` never validates
the slug — so an empty slug can reach a listing; it renders as `∅` now rather than
collapsing a column, but refusing it at intake is a separate decision.

## D-024 — An unknown room is REFUSED, because an empty read was the failure

2026-09-20 · the 2026-09-07 `lobby` incident, reached again by a different route

**What was wrong** — Sessions were once told to read a room called `lobby`, which does
not exist. `Journal.Read` is `WHERE room=? AND seq>?`, so a wrong name returns zero rows
and renders as `(no messages)` — byte-identical to a room nobody is talking in. Measured
2026-09-07: a session reported "the room is empty, all traffic goes through the message
hook instead" while its actual project room held thousands of messages. That was fixed by
making the digest's room name DERIVED rather than hardcoded — and the derivation is still
a GUESS. It is the label's project half, and `defaultLabel` builds the label from
`path.Base(worktree)` while the ledger lives in the git COMMON dir. **So the boundary the
ledger uses is per-checkout and the boundary the room name uses is per-worktree**, and a
linked worktree session is told to read a room that was never joined. Measured on this box:
5 of 27 live sessions in one project's ledger are in linked worktrees, each told the wrong
room on every SessionStart. Labels are stable by D-013, so changing `defaultLabel` would
not help a single one of them.

**What shipped** — The refusal is on the READ, not on the guess, because the guess is only
one of the ways a wrong name arrives: an explicit `--label`, a typo, and an MCP schema that
until now said `e.g. "lobby"` are the others, and they all converge on `Journal.Read`.
`Daemon.dispatch`'s read arm now refuses a room this daemon **neither serves nor remembers**,
naming the rooms it does serve so the caller recovers in one step. **This closes an
asymmetry rather than inventing a rule:** `Say` has always refused `not joined to room %q`.
Send told the truth and read did not.

**BOTH CLAUSES ARE LOAD-BEARING.** "Not served" alone would refuse a room dropped from the
configured list after accruing rows, and the `@sent`/`@dm` pseudo-rooms, which no config
lists. "No history" alone would refuse a configured room nobody has spoken in yet — which
is precisely the true `(no messages)` this change exists to preserve. Two positive controls
pin them, and the second one FAILED first time round for a fixture reason worth recording:
`kind` is CHECKed against `('chat','im','presence','system')` and `Daemon.append` only LOGS
a failed insert, so a wrong kind in a test is a silent no-op that reads as a code defect.

**The validation already existed, one path over.** `servedRoom` (presence) has always folded
the label's project half against `cfg.Rooms` and returned "" — so a worktree session is
correctly given no presence while being confidently told to read a room that does not exist.
The two halves disagreed and the silent one was right. `servesRoom` is factored out of it so
the read path asks the same question, folded the same way.

**What it deliberately does not do** — The guess is NOT replaced. Deriving the room from the
git common dir's parent basename was considered and cut: it is still an unverified guess
(`filepath.Base(filepath.Dir(rc.ledger))` is `.git`; a checkout cloned into a differently
named directory still guesses wrong), it changes label minting, which is an ADDRESSING
namespace (D-013), and it helps none of the already-minted labels. Storing a room in the
ledger at `init` was cut: two configs that must agree with nothing checking them is the same
failure with a longer fuse, and it would put chat configuration into `cmd/buddy`, which has
no network by design. Querying the daemon at hook time was cut as the wrong binary — the
claims CLI has no socket, and `runAlert`'s header names that coupling as the thing the
design refuses. What the digest does instead is stop ASSERTING: it says the name is derived
from the label, that it can be wrong in a linked worktree, and that a wrong one is refused.

**What the reviews changed** — Two independent reviews (Codex and Fable) were asked
separately how to fix the guess and both concluded the guess is not the defect and the read
path is. Fable supplied the asymmetry argument, the two-clause predicate, and the live
measurement; Codex independently named the same two boundaries and warned against using
"this query returned rows" as the recognition test, because filters, cursors and trimming
legitimately produce zero — which is why the check is a separate `KnowsRoom` and not an
inference from the result. One mutation of six SURVIVED and is EQUIVALENT: removing the
`len(msgs) == 0` guard cannot change behaviour, because `Read`'s `WHERE` begins `room=?` on
the same table `KnowsRoom` queries, so any row returned implies the room is known. The guard
buys a second query on every read of a busy room, not correctness.

**Residuals** — A session can still be pointed at another project's EXISTING room; only an
explicit checkout-to-room binding would stop that, and that is D-024's cut. `servedRoom`
still derives presence from the label, so a linked worktree session remains absent from the
room even now that its reads are honest — the same root, not fixed here.

## D-025 — A `bye` is fenced by the PROCESS that registered the session, so a delayed one cannot end a live incarnation

2026-09-20 · the "Known unfixed" entry below this record until today; operator's ask; a
Codex design pass that REJECTED the first fence with four traces, each now a test

**What was wrong** — `Store.Bye`'s fence was `(incarnation=? OR ?='')` and its only
caller passed `""`, because the SessionEnd hook payload carries `session_id`,
`transcript_path`, `cwd` and `reason` — no incarnation. So a delayed `bye` from a dead
incarnation ended a LIVE one under the same id (`claude --resume` keeps the id): `Beat`
is `WHERE ended IS NULL` and returns nil on no match, so the live session's heartbeats
became silent no-ops it could not clear, and `orphanEnded` runs inside every `Hello`, so
the next peer's startup took its scopes while it was still editing. Two processes on one
session id — a second `--resume` while the first still runs — had the same shape with no
delay at all: `Hello` on a live row refreshes in place, and the first exit ended the row
the second was using. The obvious fix, `beat` clearing `ended`, is forbidden by the other
half of invariant 12. Measured frequency 0 in 358 sessions; latent, and closed anyway.

**What shipped** — The one thing two incarnations provably do not share is the harness
PROCESS that spawns their hooks. Measured 2026-09-20: 17 of 17 sampled hook processes had
the `claude` process as their DIRECT parent, and `kern.procargs2` names it (exec path
`.../bin/claude`, argv[0] `claude`). A hook-driven `hello` and every hook-driven `beat`
register that process — pid AND kernel start time — in `session_procs`; a hook-driven
`bye` removes its own registration and ends the session ONLY when no other registered
process is still alive. Several registrations per session are legal and are the point:
the second `--resume` registers a second process, and whichever exits first leaves the
session open for the other (both orders pinned). The anchor is found BY NAME, walking up
to 16 ancestors until one is the `claude` binary, never by depth: a `timeout` or a script
around the hook line is walked through, and no such ancestor means NO anchor — the
session is unbound and its bye behaves as it always did. A manual `buddy bye <id>` is
refused by any live registration, naming the process to kill, and `--force` is the
operator's act. The roster prints `pid N` on every bound live row, `pid N GONE` when the
process is no longer there, and `pane herdr:w14:pA` from the environment the harness
inherited — the handle for the operator winding a fleet down (issues #20, #22), never
something buddy invokes.

**The four ways the first draft let the defect back in**, each named by the Codex design
pass before any code and each now a test: (1) the LATER registrant exiting first ended the
row under the earlier, still-editing process — a single pid column cannot hold two
processes, hence the table; (2) a "registered process is dead, so end" arm re-created the
defect whenever the anchor was a short-lived wrapper — hence anchoring by name, which
makes the registered process the harness itself, whose death does establish that nothing
is being protected; (3) a hand-run `buddy hello --session X` on a live, hook-registered
session overwrote the pid with its own and, with 0, unbound it — the refresh now keeps
the pid and the terminal when the caller brings none; (4) a bye whose own anchor could
not be resolved fell through to "end" — an unresolved caller now removes nothing and is
refused like a stranger. Also measured before it was believed: `KinfoProc.P_comm` for the
claude process is `2.1.278`, the basename of the versioned file the launcher symlink
points at, so a name test on it would never have anchored.

**What it deliberately does not do** — Nothing auto-ends or auto-reaps on `GONE`; `sweep
--force` stays the operator's act, because a wrongly recorded anchor plus an auto-end
would be this defect by another road. `beat` still never clears `ended`. Chat presence and
the transcript are not consulted: a quiet live process and an exited one can share a
last-turn timestamp. The npm-installed harness (`node`) is not matched — a hook written in
node would anchor to itself — and stays unbound. `proc_other.go` answers "cannot say" on
every non-darwin platform, which leaves every session there unbound: the honest degraded
state, not a fence firing on a process it cannot name.

**Overturned** — Charter GIVEN 10's "PID is diagnostic only, never authoritative": the pid
(with its birth time) is now authoritative for exactly one decision, whether a `bye` may
end a session. Identity is still `(session_id, incarnation)`; the pid is the proof `bye`
carries of WHICH process is saying it, and the pid on the roster is diagnostic only.

**What the code review changed** — A Codex code pass over the implementation found one
confirmed hole and one wording. (1) "Mine" was pid alone: a hook that captured its
anchor, stalled, and outlived its parent could see that pid recycled and re-registered by
a replacement process, and the old bye would then recognise the replacement's row as its
own, delete it without ever asking `alive`, and end the session under a live process —
the defect by yet another road. "Mine" is now pid AND birth time (either side
unrecorded still matches), pinned by a test where `{42, born 100}` says bye over a
registered `{42, born 200}` and is refused as a stranger. (2) `procAlive` read EVERY
kernel error as "dead"; only the "no such process" errnos do now (`EIO` by measurement
on this kernel, `ESRCH`, `ENOENT`), and any other answer is "cannot say", which is alive.
Also: a manual `bye a b` silently ended `a` and dropped `b`; it refuses. The comment
claiming a refused bye writes nothing was wrong — the caller's own row and the pruned
dead ones are deleted and commit — and says so now.

**Residuals** — A session that predates the table, or whose hello was hand-run and that
never beats, is unbound. A registered process killed `-9` leaves its row `live STALE` with
`pid N GONE` until the operator sweeps. The terminal handle is what the environment said
at registration and can go stale if the operator moves the pane.

## D-026 — A session that has said `bye` cannot block a peer's claim: `claim` orphans ended owners first

2026-09-20 · issue #15, the mechanism D-022 turned up and left; Codex design pass Q3

**What was wrong** — `bye` stamps `sessions.ended` and touches no claim row (invariant
12), and orphaning of an ended session's claims ran only in `hello` and `sweep`. So a
session that finished, was verified clean and landed, and exited on request still held
its scopes, and a peer with green work was REFUSED on them — by a holder that had said
goodbye — until some other session happened to start or an operator ran `sweep`. That is
issue #15's incident: the coordinator's release check looked at unlanded work and never at
held claims, and the claims outlived the session with nobody to release them. D-022 wrote
the rule down ("a scope is freed by release, hello, or sweep --force — bye does not") and
pinned it by a test written expecting the opposite; the rule was honest and the gap was
real.

**What shipped** — `Claim` runs `orphanEnded` FIRST, inside its own transaction, before
the conflict scan: the same idempotent statement `hello` and `sweep` run, keyed to rows
still ended when it runs, so it is invariant 12's "orphaning happens in hello/sweep, never
inline in bye" with one more of the former. The conflict computation (`scopeConflicts`
and the slug arm of `allConflicts`) now EXCLUDES claims whose owner has `ended IS NOT
NULL`, so `claim --dry-run` — which cannot write — forecasts what the real claim will do,
and the two cannot disagree. The dry run says which ended holders it would displace
(`note: internal/api is held by ended session alpha (claim api-work); a real claim frees
it`), because "acquirable after cleanup" and "currently unreserved" are different facts and
a forecast that merged them would be read as the second. The PreToolUse gate's deny text
now names the plain `buddy sweep` as the remedy for a holder that has said bye, alongside
`release` and `sweep --force` for one that went silent.

**What it deliberately does not do** — The gate (`OwnerOf`) and the commit gate still
read `state='open'` and nothing else: they are the hot path, denying is the safe
direction, and D-012's "open, not live" stands — an ended owner's claim denies an edit
until any session's `hello`, `claim` or `sweep` orphans it, and the deny says which
command does that. This is a coherent disagreement, not a defect: the dry run forecasts a
transaction that has not happened. `bye` still touches no claim row. Nothing here reaps a
LIVE session's claims on any timer: a session that went silent without `bye` still
refuses exactly as D-022 says, because `ended` is the only evidence consulted and it is
positive evidence — now made trustworthy by D-025, which is why B was not shipped before A.

**Overturned** — Charter GIVEN 26's "the ONLY things that free a scope are release,
hello's orphaning and sweep --force": a peer's `claim` frees the scopes of a session that
has said `bye`, and `scopeConflicts` tests `state='open' AND owner not ended`. Staleness
still has no bearing on acquisition.

**Evidence** — The D-022 table case "the holder says bye and exits" flips from
`wantFreed: false` to `true`, watched to fail first against the old code. New tests pin
that the ended holder's row is ORPHANED and not left open beside the new claim (two open
rows over one scope is what the gate would then adjudicate against the new holder), that
the dry run reports the displacement and its `would claim` line, that `OwnerOf` after the
claim names the new holder to a third session and nobody to the holder itself, and that a
LIVE stale holder still refuses. Mutations: dropping the orphan call (two open rows),
dropping the exclusion from the scan (dry run refuses what the claim grants), and
dropping the dry-run note — each killed by its test.

**What the code review changed** — The dry run ran three autocommit reads and could
assemble a result no single ledger state ever had: a peer's `bye` landing between the
conflict scan and the displacement scan reported the same claim as BOTH blocking and
displaced (Codex code pass). The three reads now run inside ONE read snapshot — `BEGIN
DEFERRED` issued by hand on a dedicated connection, which in WAL mode pins a snapshot
without taking the write lock that D-019 refused to pay — and a test seam lands a peer's
bye exactly between the scans through a second handle on the same file, proving the
scans agree. A forecast may be stale by the time it is read; it must not contradict
itself.

**Residuals** — Between the `bye` and the next `hello`/`claim`/`sweep`, the gate denies
edits under the ended session's scopes to anyone who has not claimed them; the deny names
the remedy. `whose` and `ls` show the ended owner's claim as `open` with an `ended`
owner until then.

## D-027 — One session, every register, one screen: `status` and `who`; a report that grants nothing

2026-09-20 · issues #12, #15, #17, #20 and #22 from one orchestrated run; Codex design
pass Q4, Q6, Q9

**What was wrong** — Four failures with one shape: a fact the ledger held and no command
that put it in front of the person who needed it. (#15) A coordinator released a session
after checking its work was landed and its tree clean, and it still held two claims —
the release check looked at git and never at the ledger, because "what does this session
hold" was answerable only backwards, through `ls`, or as a count on the roster. (#17)
Two live sessions wore one roster name and rulings were attributed to the bare name all
afternoon; a send to a claim slug bounced; a session credited a finding to a third
session because a path read like a name: four identifiers for one actor and no command
that took any one and returned the rest. (#20, #22) Winding the fleet down, the
coordinator could not find a live pid for any session — the harness roster's column read
DEAD for every one — so it asked sessions to CONSENT to being killed, and they correctly
refused: a peer relaying an operator's wish is not the operator. The reportable question
— what would ending this session leave behind — had no verb. (#12) A `msg` to a session
that had reported idle succeeded silently and was never read, because delivery rides the
heartbeat and a session waiting at its prompt makes no tool call; two sessions sat idle
2h and 3h on a resource that was free, with both sides believing the other was working.

**What shipped** — `buddy who <target>` takes any name a session answers to — id,
label, `s-<8hex>`, an OPEN claim slug, through the same `ResolveTarget` that `pause` and
`msg` use — and prints the rest, plus every register: the roster row (state, ages,
`PAUSED`, `idle`, `pid`, `pane`), `CLAIMS HELD` with scopes and staleness, `DIRTY PATHS`
recorded to it (labelled observations), `INBOX` undelivered, and an `EXIT` line saying
what the LEDGER would be left holding if the session ended now. `buddy status` is `who`
on the caller. Both exit 0 on any report produced — "nothing held" is a report — and 1
when no report could be produced: an unresolvable caller, a name that resolves to
nothing, an unreadable ledger. A released slug does not resolve (D-013), so `who`
refuses it as `msg` would. `hello`'s digest warns, at every hello while it is true,
when the caller's `--label` is worn by another live session, because every pause or
message to that label is then refused as ambiguous and the session would otherwise learn
it only when a peer's send bounced. `msg` appends, on a successful send, `— X last
reported idle 2h ago; … delivery waits for its next tool call`, and on a broadcast the
count of live recipients with an outstanding idle report.

**The line this draws (#20)** — The EXIT line is a description of consequences. It is
never permission, never proof that killing is safe, and nothing it could print would
make ending a session anyone's to do but the operator's. There is no `exit` verb and no
"exit on peer request" path, and none will be built: it would be used in good faith by a
coordinator acting on a real instruction, and the sessions that correctly refuse would
look obstructive rather than right. What the operator gets instead is the `pid` and the
`pane` on the row (D-025), so a coordinator is never in the loop for a kill.

**What it deliberately does not do** — The idle note prints only when an idle row
exists for the current incarnation; its absence says nothing, because no row means
UNKNOWN and never busy (D-016). Its wording is the observation and not a prediction —
between the Stop hook and the next beat the operator may already have prompted the
session (Codex). No way to WAKE a session: that is harness-level, and a peer must not be
handed a way to inject a prompt into another session's terminal, which arrives there as
the OPERATOR's turn — the exact laundering #20 is about. `status` takes no argument,
refused rather than ignored, because `status bravo` is `who bravo` and a listing that
quietly answers a different question looks like one that honoured it. The label warning
promises only what D-013 delivers: a full session id resolves BEFORE any label, so
"address by id" is the remedy and "the label is refused" is stated for pause/msg only.

**What the code review changed** — A Codex code pass over the surfaces found three
things here. (1) The scope, dirty-path and EXIT lists were fenced as a JOIN at 512
bytes, so one 512-byte scope hid every scope after it with nothing to say so — a report
that quietly drops rows is indistinguishable from a smaller one; lists now render WHOLE
items and say `...and N more not shown`. (2) Go's flag parser wrote an unknown flag's
text straight to stderr, so an argument carrying a newline fabricated a second line that
could begin `BUDDY:`; parser output is discarded on every verb and the returned error is
fenced. (3) The session id in the report header is now `Field`ed like the label, since
`hello` accepts any non-empty id. The pass also noted that the label warning fires at
every `hello` while the collision holds, not once — which is right, a re-hello is a new
digest — and the record above is worded accordingly.

**Residuals** — The report is the ledger's view: dirty paths are tool-call
observations, the idle mark is self-reported, and `pid GONE` is a liveness probe at
render time. Two sessions can still share a label; buddy warns and does not refuse at
intake, for D-017's reason (a refusal at `hello` turns the feature off silently for that
session).

## D-028 — A watched file that changed after a session started is announced to it once, on its next tool call

2026-09-20 · issue #19 / wishlist §14; Codex design pass Q5

**What was wrong** — A coordinator quoted the project's conventions file to two sessions
as current fact. The sentence it quoted had been corrected on disk at 12:48 that day; its
copy came from a context snapshot taken before that, and a compaction had carried the
stale copy forward. Every check it could plausibly have run said current: main had moved
ZERO commits since its snapshot's tip, because the correction was in a commit the snapshot
already contained — the snapshot of the FILE predated it. The corrected sentence warned
against the very error being made. Fixing a file does not reach the copies already
issued, and the failure is invisible from inside: nothing distinguishes "I read this an
hour ago" from "nine hours ago and it changed twice since". Buddy is the only component
in the workflow that knows when a session started.

**What shipped** — A watch list in the ledger (`buddy authority [add|rm <path>]`, bounded
at eight because every entry is a `Stat` on every tool call; `CLAUDE.md` is always
watched, it being the harness's own authority file and the one the incident was about).
Two surfaces. `status`/`who` print an `AUTHORITY` block: each watched file whose
recorded modification time postdates the session's registration, or one line saying
nothing has. And `beat` carries a ONE-SHOT notice into the session's context on its next
tool call — `BUDDY: CLAUDE.md changed on disk 10m ago, AFTER this session started (2h
ago) — the copy in your context may be stale; re-read it before quoting or acting on it.`
— deduplicated per (session, incarnation, file, mtime in nanoseconds), so a file changed
twice warns twice and one changed once warns once. The notice is the half that would have
caught the incident: the coordinator never ran a status command because it did not know
it was stale, which is the whole shape of the defect. Fail-open like the dirty notice, and
the mark is claimed only after the hook output was written, for the same at-least-once
reason. The list lives in the ledger and not in git config because the check runs on the
hot path, where a git fork is 7-9 ms against a 100 ms budget and the ledger is already
open.

**What it says, exactly** — That the file on disk has a modification time later than the
session's start. Not that the contents differ from what the session read, not that the
session has not re-read them since, and in a linked worktree not that main has moved — the
file there changes only when that worktree pulls. `os.Stat`, not `Lstat`, because what a
session reads is a symlink's target. Wording is the advisory, at both surfaces (Codex).

**What it deliberately does not do** — No content fingerprint: a file rewritten with the
same mtime, or restored to an older one, is not detected, and the record says so rather
than the code pretending otherwise; an ever-seen set of mtimes cannot satisfy that, and a
hash per tool call is a cost this hook does not pay for a case nobody has measured. No
`> last notice` predicate: `> started` is the fact the incident turned on, and distinct
later mtimes already re-warn. No git consulted: the incident is precisely the case git
could not see.

**What the second review changed** — `buddy authority` printed each path at column
zero, so a path NAMED `BUDDY: …` read as a notice in a tool result; rows now begin
`watching `. And the all-clear said "nothing on the watch list has changed", which a
missing watched file made untrue — a stat that fails is skipped, not evidence; it now
says that no watched file carries a later modification time and how many paths could
not be read as a file.

**What the code review changed** — `beat` returned on a failed advisory mark before
acknowledging the inbox, so a hiccup in `authority_warned` (or the pre-existing
`dirty_warned`) after a successful write re-delivered every message printed beside the
notice (Codex code pass). Both advisory marks are now best-effort: the worst a lost mark
can do is repeat its own notice once, which is the at-least-once it already accepts, and
the inbox acknowledgement is the heartbeat's contract. Pinned by a planted trigger that
aborts the mark's INSERT: the message is acknowledged, the notice repeats.

**Residuals** — A session that never runs a tool after the change is not told (the same
residual every beat-borne notice has; `status` is the pull). The mtime clock is the
filesystem's, compared against the ledger's wall clock at registration.


## D-029 — An identifier register that never parses prose: seeded, contiguous above the ceiling, nothing reissued

2026-09-20 · issue #16 / wishlist §3 and §17; Codex design pass Q8 (build it, with a
mandatory seed and honest status strings)

**What was wrong** — The reference repo allocates decision numbers, row ids and bundle
ids from append-only PROSE ledgers, and sessions took max()+1 of what they could see. In
one day: two sessions collided on a bundle id, caught only because a merge driver
refused; a session returned four ids as unused that it had drafted as rows in its own
working document; a session said "filed as <id>" having written to no record file, and a
peer nearly struck its own row as a duplicate over one verb; and the coordinator's own
probe reported an id TAKEN off a raw substring count whose single hit was a range
endpoint in a sentence. Four false occupancy reports, every one made from something
other than a row-shaped read of a register. The eight states an id can be in look
identical to a grep, and a duplicate is not a merge conflict — the driver appends both,
silently. The run's own rules: a substring count is not an occupancy test, and never
reissue a returned id — take from the ceiling.

**What shipped** — `buddy ids`: per space, a CEILING (the highest id known used or
reserved) and the blocks handed out above it, each recorded to the session that took
it with its label and a note. `seed <space> <n>` declares the artifact's measured
high-water mark, creating the space or RAISING the ceiling, and refuses to lower it —
a lower number asserts that reserved blocks are free, which is the reissue this exists
to prevent. `take <space> <count>` is one immediate transaction: the next contiguous
block above the ceiling, and the ceiling moves; refused for an unseeded space (naming
the seed command), a non-positive count, and overflow. `ls` prints each space's ceiling
beside its blocks. `status <space> <n>` answers in exactly three registers: RESERVED
here (by whom, which block, when); above the ceiling — UNRESERVED IN THIS REGISTER,
which is not "free", the artifact may already use it; at or below the ceiling and in no
block — NOT AVAILABLE for allocation, and whether the artifact uses it this register
cannot say. There is NO `return` verb.

**Why the seed is mandatory** — Without it, a repository already carrying ids 1-100
would receive a block starting at 1 (Codex). Seeding is the operator's assertion about
the artifact; the register's promise is uniqueness among cooperating writers on this
machine's ledger above that assertion, and nothing more.

**Why the status strings are worded as they are** — After `seed decisions 100`, an
existing prose entry D-42 has no block; calling it "a hole" would claim knowledge of
the document that only reading the document as a RECORD (anchored, field-shaped) can
give, which is the prose-parsing the register exists to end. "Not available for
allocation" is the fact the register holds; "unreserved in this register" above the
ceiling is likewise the fact and not "free".

**What it deliberately does not do** — No reissue and no return: a returned id is a
claim about intent, and the register cannot see a draft in a document or a citation in
unlanded code; ids are free, holes are not worth mining. No knowledge of the artifact:
it does not read record files, does not grep, and does not claim to. Blocks survive
session end, orphaning and sweep — a reservation is a fact about the number line, not
about a session's life, and a block taken by a session that has since gone is still a
block nobody else may take. It is one register for every space the operator names,
because the run found four id spaces and the one nobody was tracking was the one that
collided.

**What the code review changed** — Three findings. `ids take record 5 junk --session B`
allocated to whoever the environment named and dropped the rest, because Go's parser
stops at the first non-flag: trailing arguments are refused before anything is
allocated. `ids ls` printed the space name at column zero, where a space named
`BUDDY:` read as a notice; rows begin `space `. And a ceiling of `MaxInt64` advertised
a next block at `-9223372036854775808`; it says "exhausted".

**Residuals** — Machine-local, like every ledger here. A session that allocated
outside the register (the lander with numbers reserved in its own uncommitted work,
in the incident) is invisible to it until the ceiling is re-seeded from the artifact;
`seed` raising the ceiling is how the register catches up.

## D-030 — Coordination state is published as a claim, not messaged; no key/value store and no compel path

2026-09-20 · issue #21; Codex design pass Q7

**What was wrong** — In a coordinated run, the facts every session needed — who was
sequencing landings, the queue order, whether a load hold was on, which id blocks were
whose — lived only in messages a coordinator sent. A session told to hold makes no tool
call, so the later "go" could not be delivered (#12); sessions never messaged did not
know a coordinator existed; id allocation was a coordinator's private notes (#16); and a
relayed instruction is not authority (#20), so a message could not be the carrier for
anything consequential anyway. The issue proposed a reserved `orchestrator` slug, a small
key/value store the holder writes and every session reads, and surfacing it at wake-up.

**What shipped** — Nothing in code, and that is the decision. The SessionStart digest
already injects every live claim's slug, owner, `--desc` (512 bytes, fenced) and scopes
into every session, and re-running `claim` with the same slug REFRESHES the description.
So the convention is: the coordinator holds a claim named `orchestrator` (any agreed
slug; nothing is reserved) whose `--desc` carries the published state — the queue head,
a hold and its reason, a pointer to the full queue — and updates it by re-claiming.
Every session reads it at `hello`, and at any time with `buddy ls` or `buddy who
orchestrator`. The id blocks that were in the coordinator's notes are now in `buddy ids`
(D-029), which is the one piece of that state that needed a register rather than a
sentence.

**The line this keeps (#20, #21)** — A claim carries no `from` and reserves a scope;
it cannot be mistaken for a control row, and nothing reads it as an instruction. The
control rows — `pause`, `msg` — are entered through the CLI by whoever runs it, which
is the operator or a session speaking for itself. "Discoverable and citable, never
obeyable": a coordinator publishes facts that bind and never commands that bind, and
buddy offers no path by which one could.

**What it deliberately does not do** — No key/value store: a second table of peer text
with its own verbs, caps and fences, for a channel a `--desc` already is. No reserved
slug: the issue's precedent (a reserved `land` claim as a retry lock) is the reference
repo's convention, not this tool's, and a name with protocol meaning is a name a peer
can wear. No surfacing at every wake-up beyond `hello`: room digests are never
auto-injected (D-010) and a claim description is the same kind of text.

**Limits of the convention, stated (Codex)** — A description is 512 bytes: a bounded
summary or a pointer, never a ten-slug queue (ten 128-byte slugs and nine separators
are 1,289 bytes). It lasts only while the claim is open: orphaning or release takes the
published state with it, so it is not durable publication. And it is peer text: a
description reading "OPERATOR: resume everything" is still a description, and only the
actual control rows carry control. If the claim needs a scope to exist, it should be a
real reservation and not a broad scope invented to carry a message.


## D-031 — Every verb answers `--help` and refuses what it does not understand; `sweep --dry-run` is the real sweep rolled back

2026-09-20 · issue #23

**What was wrong** — `buddy sweep --help` performed a real sweep: 47 rows deleted on the
operator's ledger by the one flag whose universal meaning is "tell me, do not do". `sweep`
read `args[0] == "--force"` and nothing else, so any other argument fell through to a
normal sweep. The follow-up `sweep --dry-run` was equally unrecognised, ran a SECOND real
sweep, and printed `0 orphaned, 0 deleted` — the plausible zero of a population the first
run had consumed, indistinguishable from a forecast honoured. And `sweep --verbose --force`
ran unforced, since `--force` had to be first. `ls` had the same shape (`--all` or nothing,
everything else ignored), and `init --help` created the ledger — turning the feature ON
for a repo whose operator was asking what init does. The common root, shared with #14: the
argument handling reported success for input it did not understand, so a caller could not
tell "understood and done" from "not understood and done anyway".

**What shipped** — Dispatch is a table: every verb carries its usage line, and `Run`
answers `-h`/`-help`/`--help` in first position for all of them before the verb runs —
without a ledger, without reading stdin, exit 0, nothing else. A help flag later in the
line is the verb's own parser's job (`parseFlags`), because only the verb knows where its
flag region ends; the flag spellings alone count, since a bare `help` is a legal target,
slug or message word. Every flag-parsing verb refuses an unknown flag (fenced — flag's own
diagnostic quotes the argument verbatim) and a stray positional after the flags
(`noStray`: Go's parser stops at the first non-flag, so the stray would also swallow every
flag behind it). `sweep`, `ls` and `init` now parse; `claim`, `release`, `pause` and
`inbox` refuse the stray word they used to drop. Hook verbs keep their argument semantics
untouched beyond `--help`; their contract is the JSON on stdin.

`sweep --dry-run` is `store.SweepOpts{DryRun: true}`: the WHOLE sweep runs inside its
transaction and returns a sentinel that rolls it back. It is deliberately not a second
computation of the same predicates. `claim --dry-run` shares its conflict math with the
refusal for the reason this shares the sweep with itself — a forecast that re-derives the
answer differently drifts the first time somebody edits one `WHERE` and forgets the other.
The store test runs two dry runs, the real run, and a dry run after it, and holds all four
to the population. Orphaning is the one act of a sweep that changes what the gate refuses,
so both the forecast and the real run name every claim they orphan, with its holder;
deleting released rows past ttl stays a count.

**What it deliberately does not do** — No `--dry-run` on verbs that have no forecast to
give (`ls`, `resume`, `whose`). No central "help anywhere in argv" scan, for the `msg`
body reason above. No change to how a hook verb treats a flag it does not know: a hook
line is operator configuration, and the failure posture there is the hook's own
(invariant 2), not a usage error's.

**Test shape** — Seeded, as the issue said it must be: `gone` released 25h ago, `left`
whose owner said bye, `held` whose owner is silent 25h. The forecast is taken first,
every variant runs, the forecast is taken again and must not have moved — the assertion
that fails against the old code for `--help`, `--dry-run` and `--verbose --force` alike —
and then the real `--force` sweep is the positive control: it does what the forecasts
said, names both orphans, and leaves a forecast of nothing.


## D-032 — A send reports what the ledger holds about its recipient, never a prediction about it

2026-09-21 · issue #24, including the filer's own narrowing comment

**What was wrong** — `buddy msg` printed `queued for X — delivered after their next tool
call` for every target. That is a statement about the future, and whether it comes true
depends on the one thing the sender cannot see: whether X ever runs another tool. Measured:
two sends 8 s apart to two freshly-started sessions at their first prompt — one delivered in
28 s because that session happened to run a tool, the other in 159 s because a human had to
be asked to type in its pane — and, the same night, 25 undelivered messages across four
sessions that had all gone away, several of them asks to release claims that were blocking a
queue. Every one had reported `queued`. The sender read the silence as "delivered and
ignored" and waited, when the channel was dead. The filer's follow-up narrowed it correctly:
the idle case was already signalled (D-027) and its absence is UNKNOWN by design (D-016);
what was left was the ENDED target, knowable at send time and printed only as a stderr aside
behind the confident stdout line, and the brand-new session that has no idle row because it
has never finished a turn.

**What shipped** — The result line is `queued for X — <what the ledger holds about X>`. One
arm per send, most-alarming-first, every one an observation the ledger already had: it ENDED
N ago, and nothing reads the row unless that id helloes again — naming the open claims it
still holds, because a plain `buddy claim` displaces an ended holder (D-026) and the message
was probably the wrong verb; its registered harness processes are all GONE (D-025's probe,
diagnostic here as everywhere; nothing auto-ends); it last reported idle N ago (D-027's
wording, kept); NOT SEEN FOR N past the 30 m stale mark, with no reason offered because none
is known; registered N ago and not seen since — the first-prompt case, said as exactly that
and not as idle or busy; or last seen N ago. Every arm ends in the mechanism, "delivery
waits for its next tool call", and none in a forecast. Then, on every arm, how many EARLIER messages to that target
are still undelivered and the age of the oldest: the fact that proves a channel is not
draining, which any of the 25 sends after the first could have reported. A broadcast counts
its live recipients not seen past the stale mark next to the D-027 idle count — two counts,
because an idle row says why and a stale row says only that nothing was heard. `who`'s
`INBOX` line dates its oldest row for the same reason. The ENDED fact moved from the shared
resolver's stderr onto the line; the dry run keeps its own note.

**What it deliberately does not do** — It never says "delivered": delivery is the recipient's
act, on its next hook, and this command has returned before it; `who <target>` is the check
after the fact. The WORD is refused by the test, not one spelling of it: the first shape
wrote "delivered on its next tool call" on the two quiet arms, which is the old prediction
respelled, and the Codex code pass caught it. It does not REFUSE an ended target. That was proposed in the issue and
declined for D-013's reason, which still holds: `hello` revives a session under its own id
and reports the queued count, so the row is correct and durable, and a refusal would lose
the one message a resumed session could have read — the line now says, on stdout, that
nothing reads it until then, which is what the sender needed. It does not infer busy: a
session with no idle row and a recent beat is "last seen 4s ago", not "working". It does not
say "no tool call yet" for the fresh case, because the ledger keeps whole seconds and a beat
inside the registration second is indistinguishable from none — "not seen since" is what the
ledger knows. And it does not wake anybody (D-027).

**What the code review changed** — A Codex code pass over the surfaces found the idle row
and the process list each read TWICE inside one send — once in the case guard, once in the
format — so a recipient beating between the reads (clearing its idle row) dereferenced nil
before the write, and one concurrent beat aborted a message without queueing it. Every
register is now read once. The GONE arm said "unless that id helloes again", which overstates
it: a new process can register through a beat (D-025), so it now says only that no hook will
come from a process that is not there. The broadcast counts were measured before the write
and worded as "the recipients Msg just snapshotted", which a session ending in between made
false; they are measured after the write and worded as live recipients. Three negative
controls accepted a failed command with empty stdout and now require the send.

**Test shape** — One table over ten states (the six arms, GONE over idle, a beat
inside the registration second, exactly at and one second past the stale mark), each holding the line to its phrase, to the
absence of the old prediction, and to the words the other states own (`idle`, `busy`,
`STALE`, `GONE`, `ENDED`), with `who`'s INBOX count as the positive control that a send
happened. The backlog test sends four times with the clock moving, counts an undelivered
broadcast, then drains the inbox and holds the note to nothing. The broadcast test's control
is a beat clearing the stale count. The dry-run test's positive control moved from the stderr
aside to the result line. Every test was watched to fail against the old line.


## D-033 — A declared wait, and a check the session's own scheduler runs, keep a parked session's cache warm

2026-09-23 · issue #26; `docs/KEEPALIVE-PLAN.md` (de441b3) is the spec; five measurements
taken before any code; a Codex design pass and a Fable design pass before the code, a Codex
code pass after it

**What was wrong** — A session in a multi-session run parks itself waiting for something
another session is doing (a claim to be released, a serialized ~60-minute test tier, a review
slot) and runs no turn while it waits. Every prompt-cache entry on this box is written on the
1-hour tier (D-020), so a session that waits past the hour comes back cold and its next request
re-writes the whole prefix at twice the base input rate. Over 14 days of this box's transcripts:
187 requests after a gap over an hour re-wrote 54.3M tokens; 108 of them came under four hours,
about $378 at list rates against $28 kept warm. A cache read costs a tenth of base and restarts
the hour, and nothing asked the parked session to make one. The same parking starves the inbox:
a session that runs no tool drains nothing (field notes §5c).

**Measured before any code** (transcript token counts and timestamps, never content):

1. *Positive control.* 22 historical `scheduled_task_fire` records over 30 days: every firing
   whose first request came 3,602 s or less after the previous request READ the cache (9 of 9;
   gaps 265-3,602 s; cache writes of 40-530 tokens on 83k-760k prefixes). Two fresh one-shot
   firings in this session (gaps 147 s, 237 s): warm.
2. *Negative control.* Every firing 3,633-3,660 s after the previous request re-wrote it: 9 of 9
   cold, writes of 60k-743k, reads of 21-30k (the shared system and tool prefix alone). The
   edge on this account lies between 3,602 and 3,633 s, response to response.
3. *Fire-time accuracy.* A self-paced wake (`/loop` with no interval, the harness's
   ScheduleWakeup) fired +0 to +58 s after the delay it was handed, over 14 wakes, consistent
   with rounding UP to the next whole minute. So a 3,600 s wake lands at 3,600-3,660 s, and 9
   of 11 such wakes with no turn in between came back COLD. The harness's own tool text says
   every delay up to 3,600 s "wakes up with your conversation context still cached"; measured,
   false at 3,600. One-shot cron fired +0.4-0.6 s late (4 of 4). The recurring cron behind
   `/loop 30m` was NOT measured (no historical firing; see 5); the harness documents up to 10%
   of the period late, at most 15 minutes, so at most 33 minutes for 30.
4. *Which hooks a scheduled turn runs.* Both. Two one-shot fires in this session: the idle row
   `Stop` wrote at the end of the previous turn was gone at the fired turn's FIRST tool call
   with no beat in between, which only `busy` (UserPromptSubmit) can do; and the fired turn's
   own `stop_hook_summary` lists `buddy idle` (22 ms). So every keep-alive ping resets the
   roster's `idle`, and the wait's own age has to be printed separately.
5. *Survival across `--resume`.* NOT measured. Driving a subject session (typing `/loop` into a
   detached terminal) was refused twice by the harness's permission classifier, and it was not
   worked around. The CronCreate schema says "Session-only. Jobs live only in this Claude
   session — nothing is written to disk, and the job is gone when Claude exits"; `durable` "has
   no effect". The `hello` wording rests on that, and is conditional.

**The plan's pacing formula was wrong, found while taking measurement 4.** The plan paced the
next check from the ledger's cache clock: `tier clock + 1h - now - 8m`. But a check runs BETWEEN
its own PreToolUse gate and its own PostToolUse beat, and only beat and Stop's idle write the
context observation. So during a check the ledger's newest observation is the PREVIOUS request.
At the second of the two fires the ledger said 06:17:03 while the request running the check was
at 06:21:03. In a steady loop that observation is the previous ping, fifty minutes old: the
formula prints about 2m, the ping after it prints about 50m, and the loop pays two pings a
period. The plan's claim that an operator prompt mid-wait "resets the cadence for free" has the
same flaw. By the time any check runs, the request that ran it has already touched the cache, so
the ledger clock can only mis-pace the next ping.
What shipped paces from the check itself: `next = min(50m, deadline - now)`, floored at the
scheduler's minute, printed with its seconds (`next check in 50m (3000s from now)`), because the
self-paced form hands a number to the scheduler. It is safe by construction as well as by
measurement (Fable design pass). Every request after the check touches the cache later still:
the one that reads its result, the one that makes the scheduling call, the one after that call's
result. The scheduler also times the wake from the scheduling call, so the gap to the next ping
is the delay plus the wake's lateness: 3,000 + 58 = 3,058 s against a measured warm edge of
3,602 s. The ledger observation is still used, for two things only: the TIER (1h, 5m, or both,
judged by the shorter as D-020 does), and whether the last OBSERVED request read the prefix or
re-wrote it. Both lag one request, and the line says so ("the last observed request 50m ago").

**What shipped** — Schema 9: `session_waits`, one row per session keyed to the declaring
incarnation, with its own declaration id (`decl`), `since`, a required `deadline`, a fenced
`note`, `last_check`/`checks`, the one-shot `told` mark, and `cleared`/`reason`
(landed|expired|cleared|ended). Alongside it `session_wait_targets`: the awaited claims,
resolved ONCE at declaration to claim ids and keyed to the declaration. No verdict is stored.
`Wait.Verdict` computes LANDED (at least one target and every target claim closed or gone),
else EXPIRED (at or past the deadline), else pending, and every surface renders that one
function. LANDED beats EXPIRED; a wait with no target is a timer that never lands.

Verbs, in the dispatch table with their usage line (D-031):
- `buddy wait [--on <slug>]... [--until <dur>] [--note <text>]` prints the declaration, the
  keep-alive line (the tier, and the exact `/loop buddy wait check` to arm, or NONE with the
  reason on the 5m tier) and what it replaced.
- `buddy wait check` prints one verdict line, the note on its own line for LANDED and EXPIRED,
  and the observation line: STILL WAITING with the next check, LANDED, EXPIRED, or NO WAIT.
  Every verdict that ends the wait says to stop the loop.
- `buddy wait clear`; `buddy wait ls` (every open wait, oldest first, rows begin `wait `).

Refused before any write, each with a positive control beside it in the tests:
- a slug naming no open claim (saying whether it closed, and when, or never existed);
- the caller's own claim;
- a claim whose holder has said bye (nothing will release it; a plain `claim` displaces it,
  D-026);
- `--until` outside 1m..12h;
- a note whose RENDERED length is over 512 (D-021's arithmetic);
- a positional slug (`did you mean --on`);
- an unknown flag.

A refused re-declaration leaves the existing wait untouched.

Views:
- The roster trailer `waiting 1h12m`, dated by the declaration, with `LANDED`/`EXPIRED` when
  that is the verdict and no check has closed it.
- `status`/`who`: `WAITING` (verdict, last check, the observed request, the note) or
  `none declared`; on a holder's report, `WAITED ON`.
- `release` names the sessions waiting on the claim it closed.
- A refused `claim` and its `--dry-run` print `to be told when it frees: buddy wait --on …`
  (shell-quoted), which suggests and never registers.
- `beat` says LANDED once per declaration, marked after the write like D-028.
- `hello` restates this incarnation's wait and asks the session to re-arm only if it finds no
  loop scheduled; after a revival it names the predecessor's still-in-deadline wait as the
  earlier run's, with the line that declares it again.
- `msg` to a waiter adds what it declared and when it last checked, after D-032's arm.
- Done-check `scripts/check-wait.sh`, from `check.sh`.

Cost on the hot path, measured: `beat` against a ledger holding an open wait took a median of
12.8 ms (p90 14.1 ms) against 13.1 ms (p90 14.4 ms) for the previous binary, over 80 beats
each, alternated. The wait notice is one indexed read, well inside the 100 ms budget.

**`wait check` is the harness's own session's act.** It takes its identity from
`$CLAUDE_CODE_SESSION_ID` alone, refuses without it, refuses a disagreeing `$BUDDY_SESSION`, and
has no `--session`. Its meaning (a request was just made on this cache, `last_check` is the
keep-alive's heartbeat, a LANDED verdict is handed to the session that waited) is true only
there. A check typed in another shell would stamp a keep-alive that never happened, and could
clear a waiter's LANDED before the waiter saw it (Fable). `who` and `wait ls` look from outside
without touching anything.

**What it deliberately does not do** —
- No new hook line.
- Nothing wakes, schedules or types into a pane. The operator typing `/loop buddy wait check`
  into a parked pane by hand is the zero-code form.
- Chat is not involved.
- A wait reserves nothing and refuses nothing (invariant 10): no gate or claim reads it, and a
  test claims a freed scope over a declared waiter.
- A live session's wait is never closed early, not even by `sweep --force`; it expires by its
  deadline (invariant 11).
- Waits are never inferred from a refusal, from idleness or from chat.
- No session targets, no `--on inbox`, no git-ref targets (the plan's recommendations, taken).
- No dollar figure in any verb's output; the README carries the cost table.

**Extended** — Two settled texts move, said here rather than assumed:
- Invariant 12 and charter GIVEN 26 listed hello, sweep and claim as where ended holders are
  orphaned. `wait check` now runs the same `orphanEnded` first, inside its own transaction
  (Fable). Without it, a holder's bye with no hello, claim or sweep after it left the waiter at
  STILL WAITING through every check and then EXPIRED "with the claim still open" while the
  ledger had known all along. It is the same idempotent statement D-026 put in `claim`, and
  `bye` still touches nothing. `wait` itself REFUSES a claim whose holder has said bye instead
  (see the code review below).
- Charter GIVEN 20's "only Stop is wired; UserPromptSubmit was cut" was already stale (the
  optional `busy` verb closed issue #10), and measurement 4 shows it running on scheduled turns.

**What the design passes changed** — Codex (design):
- The declaration got its own identity. `since` is whole seconds, so a beat that composed a
  LANDED notice about one wait could have marked a replacement declared in the same second,
  whose own landing would then never be announced; `told` is keyed by `decl`, and the targets
  are too.
- Every view renders ONE verdict function; the roster had said EXPIRED while `who` still said
  WAITING.
- A check from an incarnation that has said bye is refused.
- COLD is the counts and not a diagnosis. A request that wrote more than it read was mostly not
  served from cache, which a lapsed cache, a changed prefix and a huge new tool result all
  produce.
- `release` promises no notice: beat announces a wait only when EVERY target has closed.
- The tier is dated by the record that wrote it.
- Pre-existing, and fixed here: `idle` read the transcript BEFORE the session's identity, so a
  Stop that sampled incarnation I's turn and then found a revived J stamped J with I's
  footprint. The sample is now fenced by the turn's time, as MarkIdle already was.

Fable (design) confirmed the lag argument in code and replaced its hedge with a construction
proof. It added the harness-only check, the LANDED note when the awaited slug is open again
under a new claim, orphaning inside the wait's own transactions, and exact pacing tests seeded
with a fifty-minute-old observation.

**What the code review changed** — Two Codex code passes, the store half and the CLI half,
each finding reproduced by a test that failed first.

Store:
- The declaration orphaned ended holders first, like `claim`. A declaration REFUSED for other
  reasons rolls back, though, so a wait on a bye'd holder's claim was refused with "already
  orphaned (0s ago)" over a claim that was still open. `wait` now refuses such a target by
  saying the holder has said bye and that a plain `claim` displaces it (D-026); only `wait check`
  orphans, and a test holds the claim open after the refusal.
- The check closed a finished wait inside its own transaction, so a check whose output never
  arrived lost its LANDED for good: beat reads only open rows. `WaitCheck` now only evaluates
  and records. The CLI writes the verdict in ONE write and then closes it with
  `CloseWait(decl)`, D-028's mark-after-write. A lost close costs one repeat, and the close is
  silent so a check stays within three lines.
- The target join is on the session as well as the declaration id. A planted collision proves
  it; a real one is a 2⁻⁶⁴ event.

CLI:
- A generated `buddy wait --on …` printed the FENCED slug (`'api⏎work'`), which names a
  different claim. Commands now carry only slugs that survive the fence unchanged, and count the
  rest.
- `wait --session B` from session A's shell said "Arm it in THIS session". It now names the
  session that must arm the loop.
- `hello`'s re-arm line ignored the verdict and the tier. A wait that is over is taken with one
  check, and off the hour tier nothing is re-armed.
- The COLD WRITE line still diagnosed a keep-alive miss; it now names both causes and says the
  counts cannot tell them apart. `read 0, wrote 0` printed "served from cache"; it now prints
  "nothing read from the cache".
- `who` printed `none declared` when the register could not be read; it now fails like every
  other register there.
- The deadline cap rounded to the NEAREST minute, so 20m29s left scheduled a check 29 s before
  the deadline. It now rounds up, and the one-minute floor became unreachable and was deleted.
- `release` read the claim id before releasing, which could name another claim's waiters; it
  now takes the id from the release's own transaction (`ReleaseID`, `ReleaseScopesID`).

**Test shape** — Store: a refusal table where each case has an existing wait that must survive
and a control that must be accepted and must report what it replaced; the verdict across two
targets, one then both released; a timer at one second before its deadline and exactly at it;
LANDED past the deadline; check → LANDED → NO WAIT; the same-second replacement; bye then check;
each cleanup entry point on its own, with `sweep --dry-run` as the rollback control; revival; a
bye'd holder landing at the next check; a re-claimed slug; a swept claim row; a waiter with no
standing. CLI: the whole round trip with every line held to its text; pacing exact against an
observation fifty minutes old, three times; the deadline cap and the minute floor (20 s left
rounds to zero, so the floor is load-bearing); the three tiers; COLD at one token either side of
write = read; the harness-only refusals; every declaration refusal writing no row, each beside
its control; `--help` in three positions; the refused-claim suggestion, shell-quoted; one verdict
across roster, `who`, `wait ls`; `msg`'s ordering; `hello` both ways and past the deadline;
peer text that tries to open a line as LANDED or BUDDY; a failing sink and a planted failing
mark, and one test per code-review finding. 59 mutations over three rounds; every one that
still applies is killed. The survivors along the way each changed something:
- Hello's explicit revival clear was EQUIVALENT to `clearEndedWaits`' incarnation arm. The
  duplicate was deleted, and the arm got its own mutation, which died.
- A COLD criterion loosened to ten times passed, because the test's cold case was nineteen
  times. Cases one token either side of write = read now kill it.
- The views' incarnation comparison was a guard no test could arm, because cleanup always closes
  a predecessor's row first. A planted stale-incarnation row now arms it.
- The session arm of the target join is armed only by a planted declaration-id collision.
Three mutants that did not compile (each left a variable unused) were respelled so that they
did, and then died.

**Residuals** —
- The observation lags one request, so a drop from the 1h to the 5m tier (usage overage) costs
  one wasted ping before a check says to stop, and a COLD WRITE at check N is about check N-1.
- The shared system prefix reads warm even on a cold restart, so a session under about 60k can
  re-write without tripping COLD, which is the cheap direction.
- A LANDED verdict reaches a parked session at its next check, up to fifty minutes late. `beat`
  says it at once only if the session happens to run a tool.
- Measurement 5 and the recurring-cron lateness were not taken. The `hello` line is conditional,
  and the fixed form's 30-minute period leaves 27 minutes of margin against the documented 3.
- `idle`'s context sample on a scheduled turn did not advance to that turn's final record in
  measurement 4 (it kept beat's), where the typed turn before it did. The cause is not
  established.
- The new `idle` fence compares a millisecond turn time against a whole-second `started`, so a
  predecessor's turn and a revival inside the SAME second still pass it. MarkIdle next door has
  the same comparison and the same residual (Codex code pass, question 9).
- A `wait check` whose close fails after its verdict was written says the verdict again at the
  next check, and says nothing about the failed close, by design.

## D-034 — `hello` drains the inbox into the SessionStart digest, inside the context cap

2026-09-23 · issue #25, the half of it that needs no settled decision moved

**What was wrong** — `hello` counted the queued messages and delivered none of them: `BUDDY: N
queued message(s); they will arrive after your next tool call.` The digest it printed was
already going into the session's context, so the one hook that fires before a session's first
prompt knew about the work and withheld it. The measured case (issue #25): an operator opened
six sessions to hand them work, and every assignment needed a human to type into that pane,
because a session at its first prompt runs no tool and so drains nothing. The same holds for
`resume` and `compact`, which run SessionStart again under the SAME session id. `clear` runs
SessionStart too, but under a NEW id (measured below), so it has nothing of the old id's to
drain.

**What shipped** — A hook-driven `hello` renders the inbox after every other digest line with
beat's header, fence and write-then-mark (`boundDrain`, `inboxLine` and `writeInbox`, now
shared by both), and marks delivered only after the write succeeded. Bounded twice: by one
beat's 20 messages / 8 KiB, and by the room the digest leaves under `helloBudget` (9,000 bytes
for the whole digest). Oldest first, stopping at the first message that does not fit rather
than skipping ahead, so order holds. Whatever is not shown is counted — `BUDDY: N queued
message(s) not shown here; they will arrive after your next tool call.` — and stays
undelivered.

**Why the second bound** — Claude Code documents a 10,000-character cap on hook output
injected into context; past it the model gets a ~2 KB preview and a file path. That is
documented, NOT measured on this box. Before this change the digest was a few lines plus the
claims list. A beat's worth of messages on top of a long claims list crosses the cap, and the
preview would hide the claims along with the messages. The claims are the part of the digest a
session must not miss, so messages get the leftover room and no more. The count is in bytes,
never fewer than characters.

**What it deliberately does not do**
- A hand-run `hello` (`--session`, no hook JSON) drains nothing and keeps the count. Its
  output goes to whoever ran it, usually the operator's terminal, and a message marked
  delivered there would never reach the session. Same rule as the process and pane
  registration (D-025).
- It does not wake a session already at its prompt. That is the other half of #25 and still
  meets D-027 ("no way to WAKE a session"). Moving it needs evidence of how a host
  session-to-session send is attributed on the receiving side: the operator's turn or a
  marked peer message. #25's own measurement could not tell those apart.

**Test shape** — `hello_drain_test.go`: delivered once (a beat after it brings nothing); a
hand-run hello counts and marks nothing, then beat delivers; 25 messages show 20, count 5, and
the next beat brings exactly those 5; a body's newline stays inside its line; a failed write
marks nothing; six 500-byte claims plus two 3,000-byte messages give a digest under the budget
with all six claims, exactly one message and a count of one. Mutated six ways (drain off,
drain on a hand run, write error ignored, budget removed, beat bound removed, fence removed),
and each mutation failed exactly the test written for it.

**Measured on the live harness (2026-09-23, two sessions, the installed binary at 7aed0a4)** —
- `compact`: the id stayed the same (`s-2e377b69`, same `started`), the queued message came
  in the `SessionStart:compact` digest inside the fence, and `who` showed 0 undelivered.
- `resume` of an ENDED id (`/exit`, then `msg` printed the ENDED arm, then `claude --resume`):
  the message came in the `SessionStart:resume` digest, the row went back to live under a new
  pid, and a message the compact had already delivered did not come again.
- The cap: 25 queued, then a compact showed m01–m20 plus the `5 queued message(s) not shown
  here` line, `who` showed 5 undelivered, and the next tool call's beat brought m21–m25 in
  order.
- An empty inbox: the digest is unchanged, with no count line and no block.
- `clear` **ends the session and starts a new id in the same harness process**: `cb9b1e1b`
  went to `2e377b69`, same pid 77414, same pane, and the old row went to `ended`. A message
  queued to the old id stays undelivered under an ended session while the pane that should
  have read it keeps running. That id had taken no turn, so Claude Code wrote no transcript,
  and `claude --resume` answers `No conversation found`. The message can never be read, even
  though the ENDED arm says it would be if the id said hello again.

**Residuals** —
- A message queued before a `/clear` is stranded under the ended id (measured above). `msg`
  resolves its target to a session id when it is sent, and a label is `s-<8hex>` of that id,
  so nothing addressed before the clear can reach the session after it. One fix would be for
  `hello` with `source=clear` to adopt the inbox of the session it just ended in the same
  registered harness process. That would be a second decision made on `session_procs`, which
  D-025 trusts for exactly one, so it needs its own record. Not done here, and low priority:
  the operator starts a new session rather than running `/clear`.
- The same holds for a wait (D-033): `WaitOf` is keyed by session id, so a wait declared before
  a `/clear` is neither restated nor offered back to the new id. It is dropped without a word.
- beat's own drain has the byte bound and not the context cap: 8 KiB of body plus notices
  approaches 10,000 characters without passing it. Unchanged here.
- The claims list is unbounded and could cross the cap on its own, before any message is
  added. That predates this change.

## D-035 — A resource slot is a claim on `.buddy/slot/<name>`; the prefix is named, not built

2026-09-23 · issue #27

**What was wrong** — Claims reserve paths, and nothing reserved a shared RESOURCE: the local
test box (one `check-all` at a time), a serialized hour-long tier, `main` during a land. The
slot protocol lived in chat ("ask before you start", "announce when you start", "tell me when
you're off"), so it had no state, no history, and no way to see the holder without asking.
Measured in the 2026-09-20 fleet run: three sessions were each told, individually and
correctly, that a grep-bound doc gate may overlap a tier. The sum was four concurrent suites
against a serialized ~60-minute tier with timing legs, a load nobody authorized, assembled out
of per-session permissions, and only the coordinator could see it.

**What already existed** — For a capacity-1 resource, a claim on a sentinel path does the whole
job. `NormalizeScope` consults no filesystem, so `--scope .buddy/slot/box` is legal and no file
ever needs to exist. The claim is exclusive and a refusal names the holder and its staleness
(D-022). `release` frees it, `wait --on` (D-033) queues on it with a LANDED verdict, `who`
prints `WAITED ON by N session(s)` (the queue depth), and the SessionStart digest shows the
holder to every session (D-030).

**What shipped** — The convention, named:
- `.buddy/slot` is the reserved prefix (`slotPrefix`, `internal/cli/wait.go`). A scope is a
  slot when it lies under the prefix by invariant 14's containment on invariant 13's fold,
  which is the comparison the conflict scan itself makes. `.Buddy/Slot/box` is a slot because
  it collides with `.buddy/slot/box`; `.buddy/slots/x` and `.buddy/slotx` are not.
- A refused claim and a `--dry-run` print one `SLOT:` line, before the `buddy wait --on`
  suggestion, naming every distinct slot in the conflict set (fenced): a shared resource, not
  a file; do not start the job it guards until it frees; `buddy who <slug>` counts who else is
  waiting. Either side of a conflict can be the slot (a held `.buddy` contains every slot; a
  requested `.buddy/slot` asks for all of them), and the held side is named when both are.
- A README recipe (§1c″).

**Why a line and not a verb** — Everything the resource needs already refuses, frees, queues
and shows. What was missing was that a refusal on a sentinel path read like a file collision,
and the natural next move after one (go ahead, it is only a path) is exactly the overlapping
run the slot exists to prevent.

**What it deliberately does not do**
- **No counted capacity.** The review API has 4 slots, and no contention on it has been
  measured. A `slot` verb with capacity, and its own record, waits until a fleet run shows a
  counted resource contended.
- **No new scope kind.** The sentinel is a path. Exact-path/prefix scopes are unchanged, and no
  gate reads the prefix. The SLOT line is presentation on the refusal path only.
- **No change to D-026.** A holder that says `bye` stops refusing, so a job still running after
  its session ended is unguarded from that moment. The ledger records sessions, not jobs. The
  README says to hold the slot in the session that runs the job. This case is predicted, not
  measured. A crashed holder's claim goes stale and still refuses, which is the right
  direction.
- No fairness, ETA expiry, queueing beyond `wait`, or machine-wide authority across independent
  repos. The measured run was worktrees of one checkout, which share one ledger.

**Test shape** — `TestARefusedSlotSaysItIsAResource`, table-driven, every case refused, and the
real claim and the dry run both checked: the same slot; a slot recognizable only after the fold
(`.BUDDY/slot/box` against `.buddy/SLOT/box`); a held parent containing the slot; a request for
every slot (names the held one); one slot reached by two requested scopes (named once); two
slots on one plural line; and three negative cases that are refused and print no SLOT line
(`.buddy/slots/box`, `.buddy/slotbox`, an ordinary file). The refusal and the wait suggestion
are asserted in every case, so "no SLOT line" is never "the refusal path never ran". Mutated
seven ways (fold removed; `/` dropped from the prefix match; requested side preferred over
held; held side only; the line removed from the real claim; removed from the dry run; dedupe
removed), and each mutation failed exactly the cases written for it.

## D-036 — The SessionStart claims list fits the digest budget, ahead of messages

2026-09-23 · issue #30

**What was wrong** — D-034 capped the SessionStart digest at `helloBudget` (9,000 bytes)
because Claude Code documents a 10,000-character cap on injected hook output, past which the
model gets a ~2 KB preview and a file path (documented, not measured). The budget existed to
protect the claims list, and was then applied only to the inbox messages after it. The list
was written unbounded: one line per open claim carrying a fenced slug (128), owner (64), desc
(512) and scopes (512), up to ~1.2 KB each. About eight claims at full length crossed the cap
with no message at all, and every session would start with a preview where the list should be.
No incident; confirmed in source.

**What shipped** — `writeHelloClaims` (`internal/cli/cli.go`). The lines after the list (the
room line and the wait lines) are rendered first, so the list's room is the budget minus
everything around it. A list that fits prints whole, in the ledger's order, unchanged. One that
does not fit picks the session's OWN claims first, then everyone else's oldest first, and stops
at the first that does not fit rather than skipping ahead to a shorter one. It holds back room
for one line: `` BUDDY: N more live claim(s) not shown here, K of them YOURS (the digest is
capped); `buddy ls` lists every one, and they refuse exactly like the ones shown. `` The
"YOURS" clause prints only when K > 0. Shown lines keep the ledger's order. The inbox drain
then takes what is left, as before, so claims come ahead of messages.

**Why own first** — After a resume or compact, a session's own claims are the part of the list
it may not otherwise remember holding, and a claim it forgot is one it never releases.

**What it deliberately does not do**
- **No `orchestrator` priority.** The issue asked for the `orchestrator` claim first. D-030
  reserves no slug ("a name with protocol meaning is a name a peer can wear"), and ranking by
  one would reserve it by the back door. A coordinator claims early, so oldest-first carries
  it in the ordinary case, and `buddy who <slug>` reads it at any time.
- No compact second tier (slug-only lines for the hidden claims): at 100 claims that is
  itself over the cap, and it would need its own bound.
- The gate is unchanged. It reads the ledger, not the digest, so a hidden claim refuses exactly
  like a shown one, and the count line says so.

**Test shape** — `hello_drain_test.go`: twelve ~1.2 KB peer claims, the session's own created
last, a small claim after them, and a 3,000-byte message. The digest stays under budget, the
peer claims shown are a prefix of creation order, the own claim is shown, the small claim is
NOT (no skipping ahead), the count is exact, the room line survives, and the message is counted
and stays queued. Twelve of the session's own claims give the `K of them YOURS` count. A short
list prints whole in creation order with no count line (the control). And a sweep of desc
lengths 0–512 in steps of 16 keeps every digest under budget, with a control that at least one
length cut the list: a fixed line length can land where a room computed without the tail still
fits. That sweep exists because the first mutation run showed it: ignoring the tail survived
every fixed-length test. Mutated six ways (no bound, no own-first, skip ahead, no YOURS count,
own claims hoisted in the rendering, room computed without the tail), and each failed a test.

## D-037 — `Open` refuses a ledger stamped newer than the binary

2026-09-23 · issue #29

**What was wrong** — `store.Open` migrated only upward (`if ver < schemaVersion`) and accepted
anything else. A stale binary (an old `~/bin/buddy`, a build from another worktree, a hook line
naming an old path) opened a ledger a newer binary had migrated and used it as its own shape. A
query against a reshaped table fails loudly, and the gate fails closed on it. A write to a
table whose MEANING changed while its columns did not succeeds, wrongly, and nothing says so.
Low priority, not absent: hooks spawn one installed binary per call, so a mixed fleet needs the
operator to have installed two.

**What shipped** — `Open` returns `ErrLedgerNewer` when `PRAGMA user_version > schemaVersion`,
naming the path, both versions, and the fix (rebuild and reinstall buddy; check the hook
lines). `migrate`'s locked re-check refuses the same way: a newer binary can migrate between
Open's unlocked read and the BEGIN, and the old `ver >= schemaVersion → nil` would then have
handed back a Store on the newer shape. The refused open touches nothing; nothing migrates
down.

**Why an error and not a read-only mode** — To every caller this is "ledger exists but cannot
be read", which the gate already DENIES (invariant 3). It must never collapse into "no ledger",
the silent-allow arm. A read-only path or a `doctor` verb waits until one is needed.

**Test shape** — `internal/store/version_test.go`: a ledger at `schemaVersion` opens (the
control), and at `schemaVersion+1` it is refused with `ErrLedgerNewer`, the refusal names both
versions and the fix, and the stamp is unchanged. `migrate` on a newer stamp refuses, and at
the current stamp it is a no-op. `internal/cli/version_test.go`: an unclaimed Edit is allowed
at the binary's version (the control) and DENIED at +1, and the deny names the cause and the
fix. Mutated: removing Open's check fails the store test, and at the gate the edit is ALLOWED
silently, which is the collapse invariant 3 forbids. Removing migrate's check fails its own
test.

## D-038 — The Stop hook records each session's base; the views print where it stands against main now

2026-09-23 · issue #28

**What was wrong** — Nothing said what commit a session's tree was on. Measured (field notes
§6): a session reported the shared status file at 1999/2000 lines and it went out as a fleet
emergency, with an archive roll assigned. `main` was at 1736. The session's worktree was three
landings behind, before a roll that removed 363 lines. Its number was true about its tree and
meaningless about main. §16: main was amended twice and a peer rebased silently onto an
orphaned commit, caught only by a hand-run `git merge-base --is-ancestor`. With N sessions on
N worktrees at N bases, "how stale am I?" is the unstated assumption in every report.

**What shipped**
- **Recording.** `idle` (the Stop hook, once per turn) runs `git rev-parse --verify -q
  HEAD^{commit}` in the hook's cwd and stores the full object name in `session_base`
  (schema 10). It uses the same fence as the context footprint: current incarnation of a live
  session only, a turn no older than the one recorded, and a turn that ended before this
  incarnation started is refused. A HEAD that cannot be read records nothing, and the previous
  base keeps its own age. Not on `beat`, which forks no git by design. Measured on this box:
  the sample costs 9.1 ms median (10.6 ms p90) in the Stop hook.
- **Reading.** The roster (live rows) and `who` print `base 079dd6a7 (2 ahead, 3 behind main,
  12m ago)`, or `on main`, `3 behind main`, `2 ahead of main`. The lag is computed when the
  view is read, with `git rev-list --left-right --count <sha>...<main>`, one call per distinct
  base per listing (12.8 ms median). Main moves without the session doing anything, so a
  stored lag would be stale in the direction that matters. `main` is the local branch, and
  `master` is the fallback. With neither, the view says `no main branch to compare`. A base
  this repo cannot place says `not in this repo's history`. `who` prints `BASE (none recorded
  …)` instead of omitting the line (D-023).
- **Validation.** Only a full lower-case hex object name (40 or 64) is stored or reaches git's
  argv on the read side, so nothing that parses as an option or a revision expression gets in.
- **Environment.** Git runs with the dirty scan's clean environment. This is a background read,
  so an inherited `GIT_DIR` or `GIT_INDEX_FILE` must not re-point it.

**Changed from the issue: no `NOT ON MAIN`.** The issue proposed flagging a base that is not
an ancestor of main. In a fleet that commits on worktree branches and lands them, that is
every session with unlanded work, and a flag that fires on every busy session gets read past.
Both counts print instead. `ahead` alone is unlanded work on current main. `ahead` and
`behind` together is a tree that needs a rebase before its numbers mean anything about main.
That is also how the §16 orphaned base shows, and an ancestor test cannot tell that case from
unlanded work either.

**What it deliberately does not do**
- It refuses nothing. It is an observation with an age (invariant 10), and D-028's advisory
  wording is the model.
- No stored branch (D-015 cut that). A HEAD sha plus its lag is a different fact.
- No alert when main moves non-fast-forward, and no claim-time warning on an old base. Each is a
  second decision now that the column exists.
- No row for a session without the Stop hook wired. No row means not reported, never
  "current".

**Schema 10 and D-037** — This is the first schema bump since D-037, so an older binary now
refuses the ledger once a new one has opened it, and the gate denies. That is the intended
failure: rebuild every `buddy` the hook lines name.

**Test shape** — `internal/store/base_test.go`: a live session's base reads back; an older turn
does not roll it back; a newer one replaces it; a revived id hides the predecessor's base; a
late write from the predecessor is dropped both before and after the successor records its
own; a schema-9 ledger gains the table and keeps its rows. `internal/cli/base_test.go`, on a
real repo with a linked worktree: no row before any Stop, and `who` says none is recorded;
after the real `idle` hook, `on main`; main lands three commits and the SAME stored base reads
`3 behind`; two commits in the worktree and a later turn read `2 ahead, 3 behind`; `who`
prints the same; a session that never ran the Stop hook has no base (the control); renaming
main away reads `no main branch to compare`, and to `master` compares against it. `isSHA` is
tested against an upper-case name, short and long names, a flag and a revision suffix. Mutated
nine ways (no record in `idle`, no turn guard, no incarnation fence, no join, ahead and behind
swapped, no `master` fallback, no BASE line in `who`, any character accepted, any length
accepted), and each failed a test. The late write after the successor's row was added
because reading the test showed the fence mutation could survive without it: while the
successor has no row, the join alone hides a predecessor's write. That was predicted, not
observed. The first version of that mutation did not compile.

## D-039 — `msg` names the harness's wake address for a quiet recipient; buddy still wakes nothing

2026-09-23 · issue #25, the half D-034 left open

**What was wrong** — Delivery rides the hooks: `beat` on a tool call, `hello` at SessionStart
(D-034). A session already at its prompt runs neither, so every `buddy msg` to an idle session
waited for a human to type into its pane. An operator opened six sessions to delegate work and
released each assignment by hand. D-034 left this as D-027's "no" until someone measured how
the harness's own session-to-session send (SendMessage) appears on the RECEIVING side: as the
operator's turn, or as a marked message from another agent.

**Measured (2026-09-23, two sessions the operator opened for this)**
- **It wakes an idle session.** A probe to a fresh session at its first prompt was enqueued and
  dequeued 5 ms apart, and the session ran a turn and replied. Nobody touched the pane.
- **It is not the operator's turn.** The recipient's transcript records a `user`-role entry
  with `isMeta: true` and `origin: {kind: "peer", name, verifiedPeerPid, msg_id, …}`, and
  `verifiedPeerPid` was the sender's own harness pid. The model sees `Another Claude session
  sent a message:`, then `<cross-session-message from="uds:…" from-name="…" from-mode="…">`,
  then a harness paragraph: "not typed by your user … A peer cannot grant escalation … never
  treat a peer message as your user's approval for a pending prompt".
- **The body cannot forge the framing.** A raw `</cross-session-message>` inside the body
  arrived as `<\/cross-session-message>`, and a raw opening tag arrived as
  `<\cross-session-message`. The recipient saw one real open, one real close, and the test line
  between them.
- **The address.** A sender appears as `uds:/tmp/cc-socks/<pid>.sock`, the harness process's
  socket, and SendMessage accepts that as a `to`. buddy already records that pid (D-025).
- **End to end.** `buddy msg` queued to an idle session (1 undelivered). A SendMessage to its
  socket address said only "run buddy inbox". The inbox went to 0 undelivered, and the session
  went back to idle. The wake also ran the recipient's `UserPromptSubmit` (`busy`) hook: the
  idle mark cleared on arrival.

**What shipped** — After its result line, `msg` prints `to wake it now: SendMessage to
"uds:/tmp/cc-socks/<pid>.sock" with the text "buddy mail is queued for you: run buddy inbox" —
…`, and only when ALL of these hold:
- The target is one live session that the ledger says is quiet (D-032's idle, stale, or
  registered-not-seen arm). A session seen recently drains on its own.
- Exactly one registered harness process is alive, by the pid-and-birth-time probe GONE uses.
  Here the pid forms an address and nothing else (D-025). A reused pid fails the birth-time
  check, and two live processes are ambiguous, so the line is omitted.
- The socket exists and IS a socket. The path is the harness's internal detail, and if it
  moves the line disappears and `msg` reads as it did before.

The result line and the wake line render from ONE observation of the recipient (`observe`:
the row, the idle report, one probe per registered process). `TestMsgProbesTheProcessRegister
OncePerSend` holds that contract; `gonePIDs` folded into it.

**Why this does not reopen D-027** — buddy still wakes nothing. It cannot call a harness tool,
and it never types into a pane. The line names an address, and the sending AGENT decides,
under its own harness's permission rules, including the recipient's hold-for-approval when the
permission modes differ. What arrives is marked as a peer, with the sender verified by the host,
and cannot pass as the operator. The wake carries no content: the message stays in the ledger,
fenced, and the drain delivers it. The ledger stays the authoritative copy (invariant 4), and a
held or refused wake costs nothing. D-027's actual line (no exit verb, no path by which a peer
obtains permission to end a session) is untouched.

**What it deliberately does not do**
- No send from buddy, no pane injection, no scheduler.
- No wake line for a broadcast (`all`): one address per recipient would put up to N lines in
  the sender's context, and a coordinator waking the whole fleet should say so per target.
- No label-to-harness-name mapping (`buddy-system-fd`). The socket address is exact and derived
  from what the ledger already holds; a name is not.
- Not in `who` or the roster. The address is for the moment a message is queued.

**Test shape** — `wake_test.go`, with real unix sockets in a short `/tmp` dir (darwin caps a
socket path at 104 bytes). Printed for an idle recipient, one past the stale mark, and one
registered and not seen since (the measured case). The positive control is then removed one
condition at a time: no socket, a regular file at the path, seen recently, process gone, two
live processes, ended, and a broadcast. Each still queues, and none prints the line. The body
never appears in the output, and the ledger copy stays queued. The fixture's `sockDir` starts
empty, so no test reads the real `/tmp/cc-socks`. Mutated six ways (line removed, socket not
checked, any file accepted, quiet not checked, any number of live processes accepted, register
probed twice), and each failed a test. A seventh, removing a `Live()` test, SURVIVED: `bye`
deletes an ended session's registered processes, so an ended target already fails the
one-live-process condition. The guard was removed as one no test can arm, and the `ended` case
holds the rule. The first probe mutation (`a && a`) survived for the wrong reason: it
short-circuits on "dead", and rewritten as `a || a` it fails the probe-once test.

## Known unfixed

- Enforcement is cooperative, not containment. The gate adjudicates declared paths, has a
  TOCTOU window between verdict and write, and cannot bind a process that bypasses the
  harness.
- The one folding rule over-merges on a case-SENSITIVE volume mounted under a repo. Accepted:
  the alternative is two folding rules and a choice between them at every call site.
- Separator handling is darwin-only.
- Socket connections are bounded by a 30s deadline rather than joined at shutdown.
- `Say` can succeed into a dying TCP connection; the `@sent` outbox row is what keeps the
  record when it does.
- Linux is untested. macOS is the developed and used platform.
- The commit gate covers only the ordinary commit path, and only where the hook is installed.
  `--no-verify` and `commit-tree` skip it, a merge commit runs `pre-merge-commit` instead,
  and the commits that rebase, cherry-pick, revert and `am` create do not run `pre-commit` at
  all. Git will not let a repository configure its own `core.hooksPath` — correctly, since
  that names a directory of programs git executes — so an uninstalled hook is the default
  state of every fresh clone.
