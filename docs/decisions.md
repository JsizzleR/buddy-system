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
