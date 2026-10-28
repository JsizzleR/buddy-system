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
`ISON alice` with `303 me alice` — the name list as an ordinary parameter, no colon —
but `ISON alice SmarterChild` with `303 me :alice SmarterChild`. Reading only the trailing
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

## D-040 — `busy` drains the inbox into the prompt that opens a turn

2026-09-24 · issue #31

**What was wrong** — Only two hooks delivered the inbox: `beat` on a tool call and `hello` at
SessionStart. `busy` (UserPromptSubmit, D-016) only cleared the idle mark. A prompt typed into a
session at rest therefore opened a turn with no mail in it. The mail arrived only if the model
happened to run a tool, and a text-only answer never saw it.

**Measured (a field run's ledger, 2026-09-24)** — `s-ea41a261` went idle at 04:01:28Z with a
background poll running that would not finish for hours. At 04:05:00Z the orchestrator queued an
approval to it (inbox row 1175). At 04:05:41Z the operator typed `ok` into its pane, only because
they knew mail was waiting. The session's first move was `buddy inbox`, the row was marked
delivered at 04:05:44Z, and only then did the work start. Without the keystroke, the approval
would have waited for the poll.

**The rule** — `busy` runs beat's drain: `Undelivered` → `boundDrain` (20 messages / 8 KiB) →
`writeInbox` (the same fence), emitted as ONE hook document with
`hookSpecificOutput.hookEventName: "UserPromptSubmit"` and `additionalContext`. It writes first
and marks delivered only after the write succeeds, so a lost write is redelivered by the next
drain. Nothing queued means nothing is printed. It carries only the inbox. Beat's dirty,
authority and landed-wait notices belong to the tool call that observes them, and that call
follows.

**What it does not do**
- It wakes nothing. Somebody still has to type into the pane, or a peer has to send the D-039
  wake. The change is that whatever opens the turn now carries the mail.
- It does not claim that a harness SendMessage wake fires `UserPromptSubmit`. That was not
  measured. The wake's text says "run buddy inbox", so that path already works either way.

**Found in the same trace, not fixed here** — the wake did not happen at 04:05, although D-039's
conditions all held: `s-ea41a261` was idle, pid 68315 was its one live registered process, and
`/tmp/cc-socks/68315.sock` existed. The orchestrator ran `buddy msg … 2>&1 | head -1`, and the
wake address is `msg`'s second line. It then trusted the harness peer listing, which showed the
session as `busy` because of its background poll, over buddy's `idle 3m`. It sent a SendMessage
to a guessed peer name, and that message never reached `s-ea41a261`. Fixed in D-041.

**Test shape** — `busy_test.go`: delivered with the prompt under the right event name and marked
delivered (and the next beat does not repeat it); an empty inbox prints nothing, with a positive
control that the same hook prints once something is queued; a body's newline is fenced; a failed
write marks nothing and the next beat delivers the message; 25 queued messages deliver 20 and
leave 5 queued.

## D-041 — `msg`'s wake address rides the result line

2026-09-24 · issue #32, found in D-040's trace

**What was wrong** — D-039 printed the wake address as a SECOND line after `queued for X — …`.
At 04:05:00Z on that field run's ledger, every D-039 condition held for `s-ea41a261`: it was idle,
pid 68315 was its one live registered process, and `/tmp/cc-socks/68315.sock` existed. So the
line was printed. But the orchestrator ran `buddy msg … 2>&1 | head -1`, which is the ordinary
way an agent keeps output short, and the address was cut. It then trusted the harness peer
listing, which showed the lane as `busy` while its background poll ran, over buddy's
`idle 3m`. It sent a SendMessage to a guessed peer NAME, and that never arrived. The lane sat
until the operator happened to type into it.

**The rule** — a send answers on exactly ONE line. The wake address is appended to the result
line as `; to wake it now: …`, with the same text, conditions and single observation as D-039.
One line survives `head -1`, `tail -1` and a grep for the recipient.

**What it does not do** — the address text and conditions are unchanged. No wake for a
broadcast. No claim about how the harness peer listing computes `busy`: this trace saw it say
`busy` for a session at its prompt with a background task running, and nothing more.

**Test shape** — `wake_test.go`: every case, with or without the address, asserts that the output
is exactly one newline-terminated line, and the address cases assert the line ends with
`; to wake it now: …`. Mutated back to a second line, the test fails.

## D-042 — A SHARED claim may overlap other shared claims, and nothing else

2026-09-24 · issue #33, wishlist §7

**What was wrong** — The repo under coordination requires mechanism rules to land in one playbook
file. Twice in two landings in the 2026-09-20 fleet run, that file was claimed mid-item by an
unrelated session. Both landing sessions correctly declined to contest a live claim, filed "this
rule has no home", and shipped `DURABLE: none`. Two rules were orphaned with no owner. The
contention on that file is always append-only, and a claim was either exclusive or nothing.

**The rule**
- `claim --shared` stores `claims.shared` (schema 11, an ALTER arm; every older claim reads
  exclusive, which is what it was taken as). The mode belongs to the claim, not to each scope.
- The conflict scan (`scopeConflicts`, shared by the claim and its dry run) has ONE mode term: an
  overlap conflicts unless BOTH sides are shared. Shared and exclusive refuse each other in both
  directions. A slug is still one holder, and a slug conflict never reads as one `--shared`
  would clear.
- The gate (`OwnerOf`, read by the edit gate and the commit gate) returns an exclusive blocker in
  preference to a shared one. A path under only shared claims of others is held unless the caller
  holds its own claim COVERING the path. A shared claim is an invitation to claim alongside, not
  an open door, so the ledger still records everyone who edits. The caller's claim counts in
  either mode, because across sessions an exclusive claim cannot overlap a shared one, and a
  "must be shared" test would be a guard no reachable state arms.
- A refresh takes the mode it is given: re-claiming without `--shared` makes the claim exclusive,
  and a peer sharing the path refuses that.
- `--shared` is refused on any scope that OVERLAPS `.buddy/slot` (D-035's capacity-1 slots),
  `.buddy` included. The check is in `claimScopes`, the one door both the claim and its forecast
  come through.
- Every listing marks a shared claim with the fixed word `SHARED` before its fenced scopes: `ls`,
  the SessionStart digest, `who`/`status`, `whose`, the commit-gate report, the refusal and dry
  run (`held SHARED by`), and the gate deny. That deny names `--shared --scope`. A refusal with
  shared holders in its set prints one `SHARED:` line saying `--shared` clears THOSE conflicts,
  and how many others would still refuse.

**Codex design pass (before code)**, and what each finding became:
- The first draft's "`--shared` would coexist" was false for a slug collision. It now says the
  note covers scope conflicts only, and a slug conflict carries `Shared=false`.
- A shared scope of `.buddy` covers every slot without lying under the prefix, so the check is
  overlap, not containment.
- A shared hold must never hide an exclusive blocker. `OwnerOf` prefers the exclusive one, and a
  planted state proves it.
- A claim on `rules/section` must not admit an edit to `rules`. The caller's claim has to cover
  the path.
- A wait stays bound to the claim CLOSING (D-033). A change to shared is not LANDED; `who`
  shows `SHARED`, and the waiter can reassess.

**What it does not do**
- It does not verify "append-only". The gate sees a tool call naming a path, never a diff, which
  is why the word is "shared". Two holders that read version V and write V+A and V+B can lose A's
  section. The deny, the refusal note and the README all say so.
- No `--standing` owner that "yields to nobody" (the other half of §7's ask). A standing
  exclusive owner is exactly the holder that orphaned the two rules, and a coordinator-assigned
  owner is D-030's orchestrator claim.
- No per-scope mode inside one claim (take two claims), no counted capacity (D-035), and no change
  to staleness (D-022: a stale shared claim still refuses an exclusive request), ended owners
  (D-026), sweep or orphaning.

**Test shape** — `store/shared_test.go`:
- The conflict matrix, run through the claim AND the forecast: eight cases including parent and
  child, and the folded spelling.
- A slug is not shared, with a distinct-slug control.
- OwnerOf with an unjoined caller, a non-covering own claim (child to sibling, child to parent),
  joined callers, and an unidentified caller.
- The exclusive preference, on a planted state with a positive control before the plant.
- A refresh changing mode in both directions.
- Slots: six scopes, the forecast agreeing, and an exclusive control on each.
- Migration from a v10 ledger.

`cli/shared_test.go` runs the same rule through claim, refusal, dry run, gate and `ls`, with an
exclusive-hold control that must print no SHARED word anywhere.

Seventeen mutations were run:
- the mode term dropped, or turned into "either side shared";
- the holder's mode dropped from the conflict, the refusal, or the CLI's rebuild of it;
- `OwnerOf` returning a shared holder first, letting the caller's own claim override an exclusive
  blocker, or counting an own claim that does not cover the path;
- the refresh or the insert dropping the mode;
- the slot check removed, or reduced to containment;
- the listing dropping the mode;
- the SHARED note removed from the refusal or from the dry run;
- the shared deny removed;
- the `--shared` flag ignored.

Each failed a test. `shared-first` first SURVIVED: the planted exclusive-preference test asked
only on behalf of a JOINED caller, whose own claim masked the bug. An unjoined caller facing both
kinds of holder was then added, and the mutation fails it.

**Codex code pass**
- No defect in the `OwnerOf` scan, including an unidentified caller (`excludeSession == ""`
  cannot join) and a caller whose own claim is released.
- Two findings were declined, with reasons:
  - A slug-only refusal does not print the holder as SHARED. A slug is one holder whatever its
    mode, so the mode says nothing about that refusal. Printing SHARED there would invite a
    `--shared` retry that cannot succeed, which is the same false advice the design pass removed.
  - A v10 binary that opened the ledger BEFORE the migration can run a refresh AFTER it. Its
    UPDATE does not write `shared`, so a `shared=1` claim stays shared while it prints "claimed".
    This is the same window every schema bump has had: D-037 checks the version at open and at
    migrate, not in each transaction. It lasts milliseconds, only during an install, and is
    recorded rather than fixed.

## D-043 — A correction names what it corrects, goes to the original's audience, and withholds nothing

2026-09-24 · issue #34, wishlist §4

**What was wrong** — The coordinator in the 2026-09-20 fleet run broadcast measurements, and
seven were wrong. Each was caught by a session, sometimes after peers had acted on it. For
example, nine sessions were told to count with `grep -c`, which counts lines. Nothing linked a
correction to the message it corrected, message ids were invisible on both ends, and nobody
could answer "who has seen the correction?".

**The rule**
- **Ids are visible.** A send's one line gains `; message #N` (after the observation, before
  the D-041 wake clause). Every drained line begins `#N`, in the digest, beat, busy and
  `buddy inbox`.
- **`msg <target> --supersedes N`** records `inbox.supersedes` (schema 12, ALTER arms plus the
  partial index `inbox_supersedes`, created in the arm because the schema script runs before
  it). The checks run in the send's own transaction; the dry run runs the same test in a read
  snapshot:
  - **Ownership.** `inbox.sender_session` is recorded from the resolved SENDING session
    (`senderSession`, never `--from` and never `senderFor`'s display fallback). A session
    corrects only its own messages, and the operator at a bare terminal (`''`) only messages
    sent with no session. `NULL` means unknown: every pre-D-043 row (the column is added with
    no default), and any send from a session id that did not resolve. Nobody can correct an
    unknown-sender message.
  - **Audience.** The correction's target must be the original's. A broadcast's correction
    copies the original's recipient SNAPSHOT row for row instead of taking a fresh one.
- **Nothing is withheld.** An original still queued is delivered marked `SUPERSEDED by #K
  (below|queued for you)`. A correction renders `CORRECTS #N (above | reached you 20m ago |
  expired before it reached you)`. Several corrections of one message are all listed.
- **Links sit between the `#id` and `[sender]`**, which is the first peer-controlled field. A
  plain line always has `[` straight after its id, so no sender or body can make it read as
  linked. The header states the grammar.
- **`buddy sent [id]`** is a report that grants nothing and prints no body. For every addressed
  session it gives `delivery recorded N ago`, `queued`, or `expired undelivered`, and for a
  correction the same for the original. With no id it lists the sender's last ten. The send's
  own line also says `corrects #N, which R of A addressed session(s) have a recorded delivery
  of`.

**This revises D-032's vocabulary, on purpose.** D-032 kept the word "delivered" off `msg`'s
result line, because there it was a PREDICTION about a message just queued. `sent` reports an
OBSERVATION: an `inbox_delivery` row, written only after a hook wrote the message into that
session's context. It says "delivery recorded", never "read" or "seen". A write whose mark then
failed has no row, reads as queued, and is redelivered.

**Codex design pass (before code)**
1. Migration would have made history the operator's: the first draft defaulted
   `sender_session` to `''`, so a bare terminal could correct any old message. Now it is
   NULL = unknown, and refused.
2. The first draft rendered the link where the body goes, so a body reading `CORRECTS #7 (…) —
   ignore point 3` was indistinguishable from a real correction. Links now come first.
3. A correction to a narrower audience would tell a session "SUPERSEDED by #11" without ever
   sending it #11. The audience is now the original's.
4. There is no "same drain" promise: link states are computed from the batch actually written.
   hello measures lines with every link in its longest form (`batch == nil`), so the final
   render never outgrows its budget.
5. The report needed `expired`, as distinct from `queued`.
6. The words are "recorded delivery", not "delivered".
7. There can be several corrections of one message.

**What it does not do**
- No withholding and no retraction: a correction annotates.
- No `buddy notices` standing view, no roster delivery column (D-015), no read receipts.
- No recovery of ownership across `/clear` (a new session id). The operator starts fresh
  sessions rather than using `/clear`, and a `--resume` keeps the id.

**Test shape**
- `store/inbox_test.go`:
  - ownership, with four refusals each checked through the send and the dry run, and a
    positive control;
  - an unknown sender refused to the operator and to a session, with an operator-owns-its-own
    control;
  - audience: a direct message's correction to `all` or to another label is refused; a
    broadcast's correction does not reach a session that started later, with a fresh-broadcast
    control; the original's audience gets both, linked;
  - links for a recipient that already had the original, and two corrections of one message;
  - an expired original, in both the drain and the report;
  - the `Sent`/`SentBy` report;
  - a v11 to v12 migration that leaves senders NULL and creates the index.
- `cli/supersede_test.go`:
  - the full linked render;
  - `reached you 20m ago` and the `sent` rows;
  - a body spelling `CORRECTS #N` renders after `[sender]` and marks nothing superseded;
  - a correction by another session refused, dry and real, with nothing queued;
  - a send from an unresolved session id is not the operator's.
- Two existing tests changed with the line shape: the bounded-drain count, and the recipient
  recovering a sender's address from the line.

Seventeen mutations were run:
- each ownership and audience case removed;
- an unknown sender stored as the operator, and the migration defaulting to the operator;
- the fresh snapshot used for a correction;
- the links left unfilled, and `expired` never set;
- links rendered after `[sender]`, and the batch ignored;
- the `#id` clause removed;
- an unresolved sender treated as the operator;
- the dry run skipping the check;
- `sent` dropping the original's column, and the send's count zeroed.

Two survived the first run, and both were test gaps:
- **The "sender unknown" check.** Ignoring it went unnoticed, because no test had an UNRESOLVED
  sender correct an OPERATOR message. That is the one case where both session ids are `''` and
  only that check refuses. The case is now tested in the store and through the CLI.
- **The dry run's check.** Skipping it went unnoticed, because the dry-run case put `--dry-run`
  after the body. Go's flag parsing stops at the first positional, so the flag became message
  text and the "dry" case was a second real send. The flag now comes before the body.

**Codex code pass**
- No forgery path: a plain line always starts `#N [`, and the `buddy inbox` path strips only
  the indent.
- The nil-batch budget is a true upper bound: a batch only shortens a state, to `(above)` or
  `(below)`.
- No race in the send's transaction.
- Three findings were fixed, each with a test and a mutation that the test kills:
  - **A session labelled `all`** matched every broadcast through `Undelivered`'s direct-target
    arm, past the recipient snapshot and the 24h keep. This predates D-043, but it breaks the
    audience guarantee. That arm now excludes `target='all'`.
    (`TestTheLabelAllIsNotABroadcastAddress`)
  - **A message corrected a thousand times** rendered an annotation no digest could hold. At
    most three corrections are named, then `and N more`. (`TestManyCorrectionsAreCounted`)
  - **`SentBy` scanned all history** for one sender. It now uses the index `inbox_sender`,
    created in the v12 arm.
- Accepted: `links()` runs for every queued message before the drain's bound applies, at one
  indexed query each, plus two for a correction. A session's queue is the bound; an index makes
  each query cheap.

## D-044 — A repository git refuses, with no ledger in it, is feature-off, not unreadable

2026-09-24 · issue #35

**What was wrong** — At 03:01 something ran `git init` in `/private/tmp`, creating an empty
repository owned by the operator inside a root-owned directory. Git then refused every path under
`/tmp` (`detected dubious ownership`, exit 128). `resolveRepo` treats every refusal except "not
a git repository" as ambiguous, which is right for git missing, a deleted cwd or EACCES. So the
gate denied every Edit and Write under `/tmp`, in every session, scratchpads included, for a
repository with no commits and no ledger. Invariant 3 says "never `buddy init`ed" is feature
OFF; git's refusal only hid that it applied.

**The rule** — When git exits with an error, a `.git` exists above the path, and the message is
not "not a git repository", `ledgerProvablyAbsent` looks for the ledger WITHOUT git:
- It takes the nearest `.git` entry, which is where git's discovery stops. For the `.git`
  FILE a linked worktree or submodule uses, it follows the `gitdir:` line, then that
  directory's `commondir`, so a linked worktree finds its main checkout's ledger.
- It then `Lstat`s `buddy.db` there.
- Only a positive "does not exist" returns feature-off. A present ledger, an unreadable path,
  an unparsable or oversized `.git` file, a symlinked `.git`, or `GIT_DIR`/`GIT_COMMON_DIR` in
  the environment (git is then not using the nearest `.git`) all keep the deny.

**What it does not do** — It does not open a refused repository's ledger (that still denies,
as unreadable), does not add `safe.directory`, and does not touch discovery when git itself
fails to run.

**Test shape** — `discovery_test.go`. Git's refusal is reproduced with
`core.repositoryformatversion = 99`, which exits 128 without "not a git repository". The cases:
- a refused repository with no ledger (allowed) and with one (denied, "ledger is
  unavailable");
- a linked worktree of it with no ledger (allowed) and with the ledger in the COMMON dir
  (denied);
- an unparsable `.git` file (denied, with a positive control that git refuses it);
- the function under `GIT_DIR`, asked directly with a positive control first, because through
  the gate `GIT_DIR` also redirects the home repo's discovery and would deny for the wrong
  reason.

Five mutations were run: the fallback removed, the ledger check ignored, `commondir` ignored,
an unparsable pointer accepted, and `GIT_DIR` ignored. Each failed a test.

The stray `/private/tmp/.git` was removed with the operator's approval.

## D-045 — A message says what kind of claim it carries, as the sender's declaration

2026-09-24 · issue #36, wishlist §11 and §15

**What was wrong**
- The coordinator in the 2026-09-20 run forwarded peers' claims it had not measured, four
  times, phrased the way it phrases measured things, and they were acted on. Two reached a
  session's prepared commit before review refuted them.
- A recipient put the need exactly: "I would rather have the lead early and wrong than late and
  right, provided it arrives labelled as a lead." No message had a field for that.
- §15: two relays kept their numbers and lost their scope. One of them went out under the
  measuring session's name, and that session spent a turn disowning a number it never said.

**The rule**
- `msg --lead | --measured "<what was counted, over what>" | --relay <source>`, at most one.
  Stored as `inbox.kind` and `inbox.kind_note` (schema 13, ALTER arms; every older row declared
  nothing).
- No flag renders exactly as before. Most traffic is coordination, and a marker on everything
  gets ignored.
- The declaration renders between `#id` and `[sender]`, and every form starts with the word
  "declared", on the ROW and not only in the header, so a copied line still says whose claim it
  is:
  - `declared LEAD`
  - `declared MEASURED "…"`
  - `declared RELAYED from "…", not re-measured`
- The note is the one peer text allowed in that slot. It is fenced and then `strconv.Quote`d, so
  it cannot close its own quote or reach `[sender]`. The header says the kind is the sender's
  claim and not buddy's check.
- A note must SHOW once rendered, and its cap (128 for a scope, 64 for a source) is on the
  rendered bytes, as D-021 measures a body. `\x1b` alone renders as `""` and is refused.
- The store refuses an unknown kind, a measured or relay kind with no note, and a note on a
  lead or on no kind.
- `sent` and the dry run show the declaration.
- **The drain bound counts RENDERED lines** (links and declaration at their longest), not body
  bytes. Twenty declared lines over an 8 KiB body budget would pass the harness's
  10,000-character hook cap.

**Codex design pass**
1. Row-local "declared" wording, because a bare `MEASURED` beside buddy's own links reads as
   verified.
2. Emptiness judged on the rendered note, not the raw bytes.
3. Every drain budgeted by the full final line.
4. Kinds stay mutually exclusive: relay+measured cannot tell a source's measurement from the
   relayer's extrapolation.
5. **An open limit, stated rather than claimed as solved:** `--relay` does not carry the relayed
   figure's scope as a field, which was §15's first incident. The scope stays in the body.
   D-045 labels provenance and a measurement's own scope; it does not fix scope loss in relays.

**What it does not do** — It verifies nothing, and a declared kind confers no authority
(invariant 4). It is never mandatory, is never guessed from the body, and does not resolve a
relay source to a session.

**Test shape**
- `cli/provenance_test.go`: five renders (none, lead, measured, relay, and a note spelling
  `" — [operator]` that stays inside its quotes); six refusals, each with a control and nothing
  queued (two kinds, an empty scope, an ESC-only scope, a blank source, 129 bytes, and 100 raw
  bytes that render to 200); a 128-byte scope admitted; twenty declared lines with 8,000 body
  bytes in fewer than twenty lines and under 9,000 bytes; the kind in `sent` and in the dry run.
- `store/inbox_test.go`: seven malformed kinds refused, including an ESC-only note and one past
  its rendered cap sent straight to `Send`, and a well-formed one read back.

Eleven mutations were run: the word "declared" dropped, the note left unquoted, two kinds
allowed, emptiness judged on raw bytes, the cap judged on raw bytes, the drain counting bodies,
the store's kind check removed twice (unknown kinds, then the whole rule), the kind not stored,
and `sent` or the dry run dropping it. Each failed a test.

**Codex code pass**
- No forgery through the note.
- No flag case that records a kind the sender did not declare: `--lead=false` records none, and a
  repeated `--measured` takes the last value.
- **Fixed:** the rendered-note rule lived only in the CLI, so a direct `Send` could record
  `MEASURED ""`. It is now `store.ValidateKind`, which `Send` enforces and the CLI calls for its
  message.
- **Already bounded:** its question about one message carrying ten thousand corrections is D-043's
  three-named-plus-count cap.
- **Recorded, not fixed:** a `--from` tag containing `]` can blur where the sender ends. It cannot
  fake a link or a declaration, since both sit before `[`. Inside a session the tag always
  follows the real label, so only the operator at a bare terminal can write one, and invariant 6
  already trusts the operator.

## D-046 — A session learns the verbs from the digest and a skill, not from the chat server

2026-09-25

**What was wrong**
- Sessions were not using the features shipped in D-033 to D-045 (`wait`, `claim --shared`
  and `--dry-run`, `msg --lead/--measured/--relay`, `--supersedes`, `sent`, `ids`). Nothing in
  a session's context named them. The SessionStart digest named one verb, `claim`. The
  buddylist MCP server lists chat tools only and sends no `instructions`. `buddy --help` was
  complete, but a session saw it only if it thought to run it.
- Some verbs announce themselves when they are needed: a refused claim suggests `wait --on`,
  and beat names a changed authority file. The ones above have no such moment.

**The rule**
- `hello` prints one fixed line, `helloHelpLine`, after the identity line. It points at
  `buddy --help` and names the verbs and flags a session would not otherwise learn about.
  It costs about 360 bytes of the 9,000-byte budget at every start.
- `skills/buddy/SKILL.md` is a user-level skill, installed by copying it to
  `~/.claude/skills/buddy/`. It explains how the verbs fit together: claim, dry-run and
  shared; what to do on a refusal (wait and arm your own loop, message the holder, or narrow);
  roster and `who`/`whose`; message kinds, corrections and `sent`; slots, `ids`, the
  orchestrator claim, and authority notices. Until a session uses it, it costs only its
  one-line description.
- Every `buddy <verb> [subcommand] --flag` either one names is read back against that verb's
  usage line in the dispatch table (`checkNamedVerbs`). A renamed flag fails a test instead of
  teaching every session a refusal.

**Found on the way: the digest could exceed its budget (D-036's bound)**
- The line counting undelivered messages is written after the claims list, and the list's room
  never reserved it. The spare bytes hid this. With a long claims list and a message too big to
  ride the digest, it measured 9,004 of 9,000 with the new line removed and 9,025 with it in.
  The list now reserves `helloInboxCountLine` (128 bytes) whenever mail is queued.
- The claims list's own remainder line had a fixed reserve of 160 bytes. The line is 155 bytes
  plus the digits of both counts, so it outgrows that once 100 of the caller's own claims are
  hidden (Codex, arithmetic; the test measured 193 bytes written into a 192-byte room). The
  reserve is now the line's own worst case, rendered by the same function that writes it.

**Considered and cut**
- The MCP `instructions` field. It belongs to the chat half, which is absent whenever the daemon
  is down, and claims must work with chat entirely absent (invariant 1).
- The full verb list in the digest. Every session pays for it at every start, and a copy in
  context goes stale the way a long session's CLAUDE.md does.
- Pointing at the skill from the digest. On a machine without the skill installed, that line
  would send the session looking for something that is not there. A skill surfaces itself.

**Codex code pass**
- The skill overstated nine things; all were corrected: an exclusive vs. shared hold; an
  unclaimed edit being "invisible" (the dirty register observes it, and does not prove
  authorship); the cache kept warm on the 5-minute tier; delivery in one drain; the authority
  notice as proof of staleness; the room name as certain; "the gate is the only thing that
  refuses"; and an orchestrator scope suggestion that would refuse a `.buddy` claim.
- "Park on a peer, never poll" would lead a cold reader to arm no check. The line now says to
  arm your own `/loop buddy wait check`.
- The checker accepted three kinds of drift, each now a test case: a prefix of a subcommand
  (`ids see`), an uppercase subcommand, and a flag with no left boundary (`--co--shared`).

**Open limit** — A verb whose subcommands are all deleted from its usage line stops having
the word after it checked, because the checker then reads that word as a value (a slug). That
is a rewrite of the verb, and the verb's own tests see it.

**Test shape**
- `cli/discover_help_test.go`: the digest carries the line; the line and the skill name only
  what `--help` answers, each with a minimum count read; seven drift shapes refused, one
  usage-side rename refused, and three good shapes accepted.
- `cli/hello_drain_test.go`: the claim-length sweep now queues a 3,000-byte message and requires
  the count line at least once; the remainder line fits its reserve for every room from its
  floor to floor+4000, with a three-digit YOURS count.

Seven mutations were run: removing the inbox reserve (with and without the new line), dropping
the line, `--dryrun` in the line, `--supersede` in the skill, and restoring the 160 constant.
Each failed a test. The first real run also failed on a `--desc` span credited to `ids`,
which was a real error in the skill.

## D-047 — A wait rides hook output inside a bound, and never as a command cut short

2026-09-25 · the open limit from D-046's Codex pass

**What was wrong**
- hello's digest restates a session's wait (D-033), and beat's one-shot LANDED notice names
  what landed. Both rendered EVERY awaited claim at full length.
- Measured with a reproduction test: a wait on 40 claims with 128-byte slugs made the digest
  11,254 bytes for this incarnation's wait and 21,535 for a predecessor's. The predecessor's
  "To wait again: buddy wait --on …" repeats every slug, shell-quoted. Both are over
  helloBudget (9,000) and over the harness's 10,000-character hook cap, past which the whole
  output is replaced by a preview. The wait lines are subtracted from the claims list's room,
  so they crowded the claims out of the digest first.

**The rule**
- At the hook sites only, the targets go through `targetsWithin(…, hookTargetsRoom = 1024, where)`.
  The targets are taken in order, stopping at the first that does not fit (D-036's rule, never
  skipping ahead), then `; and N more claim(s) — <where>`, with room reserved for that tail at
  its worst case.
- `where` names a view that lists them: `buddy status` for this incarnation's wait and for
  LANDED (the wait is still open there), and `buddy ls` for a predecessor's (its wait is closed,
  so `status` says none declared).
- A predecessor's re-declaration whose `--on` flags exceed `hookFlagsRoom` (1024) is not printed
  as a command at all. Pasted, a command cut short would wait on fewer claims than the earlier
  wait, and nothing on the line would say which.
- The views a session runs deliberately (`status`, `who`, `wait ls`, `wait check`, msg's waiter
  note, `wait`'s own output) still list every target. They are a command's own output, not
  injected by a hook.

**Codex code pass** — No defect. Its arithmetic for the worst case, a 128-byte slug of `"` and
`\` quoted to 258 bytes, is 1,636 bytes for the LANDED line, `1,764 + 2S` for this incarnation's
line with a 512-byte note, and `2,338 + 3S` for the predecessor's (S being the widest `span`).
All are under the 3,072-byte test ceiling (`maxHookWaitLine`). It asked for the test to use the
true 128-byte cap and a note at its cap, and both now do.

**Test shape** — `cli/wait_test.go`: 40 claims with 128-byte slugs of `"'\`, and a 512-byte
note. This incarnation's digest stays under budget, still shows a claim, points at
`buddy status`, and has every wait line under the ceiling. After every claim is released, the
LANDED line is also under the ceiling. The predecessor's digest stays under budget, prints no
partial command, and points at `buddy ls`. Each has a control that the wait line is present.
Four mutations were run: each hook site reverted to the full list, and the flags room disabled.
Each failed a test.

## D-048 — A base that carries a commit main dropped says so, found by main's reflog

2026-09-26 · field notes §16, the alert D-038 left as a second decision

**What was wrong** — Field notes §16, measured: a session committed to the shared `main` and
amended that commit twice. A peer had already rebased onto the first version. The rebase
succeeded without a conflict onto a commit main no longer reached, and it was caught only by a
hand-run `git merge-base --is-ancestor`. The cheap check a careful person runs (same parent,
same subject) says "same commit". D-038's base column shows the case as `N ahead, M behind`,
which is also what every healthy session with unlanded work on an older base shows. So the
column recorded the orphaned base and could not tell anyone about it.

**Why not the ancestor test** — "Base is not an ancestor of main" is the alarm polarity of
field notes §20. It fires on every session with unlanded work, and D-038 cut it for exactly that
reason. The question to ask of any probe here is what a HEALTHY tree prints.

**The discriminator** — main's own reflog. Every tip main has had is in it. A commit that main
once reached and the current tip does not is a commit main DROPPED. The dropped set is the
reflog's tips walked back to where they rejoin current main (`rev-list --walk-reflogs
refs/heads/<main>`, then `rev-list --stdin ^<tip>`). A base carries a drop when one of its
commits ahead of main is in that set. A worktree branch's unlanded commits were never reachable
from main, so they are never in it. The healthy tree prints nothing new.

**What shipped** — The same note both views already print gains a tail:
`base 3f2e1d0c (2 ahead, 1 behind main, 12m ago; carries 1 commit main DROPPED: 8ace1954)`,
or `carries N commits main DROPPED, newest <sha8>`. `rev-list --walk-reflogs` is plumbing, so
no user `log.*` setting adds lines to what is parsed. Tips go to `rev-list` on stdin, not argv,
because a long-lived main's reflog can hold thousands.

**Cost** — Read side only, the roster and `who`. No hook path runs it. It adds one reflog walk
per listing, 21.8 ms median (29.3 ms p90) measured on this repo's 40-entry reflog. The per-base
walk runs only when the dropped set is non-empty AND that base is ahead of main.

**What it deliberately does not do**
- It refuses nothing, and it is an observation with an age (invariant 10), like the rest of
  the note.
- It says nothing when the reflog is off (`core.logAllRefUpdates=false`) or expired. gc keeps
  unreachable reflog entries for 30 days by default. So silence means "no drop the reflog still
  records", never "main was not rewritten". The ancestor test would say more and be wrong more
  often.
- No push notice to the session that rewrote main, and none to the peer on `beat`. `beat`
  forks no git by design, and a notice at the rewrite needs a git hook (`reference-transaction`),
  which is a separate decision.

**Test shape** — `cli/base_test.go`, on a real repo with two linked worktrees. charlie has two
commits of its own on the original base. bravo rebases onto a newly landed X and commits.
Control: before any rewrite, neither row says DROPPED. main amends X. bravo's row and `who`
say `carries 1 commit main DROPPED: <X>`. charlie reads the same `2 ahead, 1 behind main` and
says nothing, which is the healthy-tree test. bravo rebuilds on current main and the note clears
while the reflog still remembers X. Then main lands Y1 and Y2 in ONE fast-forward, so Y1 was
never a reflog tip. bravo builds on Y2, and main is reset back past both. bravo carries 2, and
the newest named is Y2. Mutated five ways, and each failed the test: no reflog read, tips
without the walk back, no note rendered, the plural arm unreachable, and every ahead commit
counted as dropped. The walk-back mutation survived until the fast-forward landing was added.
Its first spelling (`--no-walk` beside `^tip`) was a no-op, because git ignores `--no-walk` when
a range is given.

## D-049 — One long run closes out several sessions: `wait --ready`, `release --outcome`

2026-09-26 · operator request; Codex and Fable design passes

**What was wrong** — The fleet's hermetic tier takes about 65 minutes. The operator's words:
"a way for multiple sessions at the same time to all understand how best to work together to do
just ONE herm tier check to close them all out — if someone's like 'I'm done in 10', we wait."
They also said plainly that a mutex was not the ask. A slot (D-035) makes the runs take turns,
and five sessions finishing inside one half hour still pay five tiers. Field practice elsewhere,
run by hand: riders send `READY <branch> <sha>` messages in chat, and one integrator replays them
in a published order, pins a sha once, runs the tier and fast-forwards main. The roster lived in
the integrator's head, the cutoff in chat, and the pass/fail announcement never reached a session
parked on its `/loop`. The measured failures there were land-side: cheap landings invalidated a
~50-minute tier three times to land one change, and a tier graded a tree main never received.
The estimate was one batched tier at about 50 minutes against about 300 serial (arithmetic, not
measured).

**The design passes, and what each changed.** The first candidate had `batch join/go`, a HOLD/GO
verdict, `--hold-max`, OVERDUE, a run table and an ancestry check.
- **Fable:** cut all of it. `--hold-max` is a timer buddy would own, which D-033 cut by name.
  GO-after-cutoff "names who is left behind", which is an auto-eject. `--ready-in` rendered as
  OVERDUE turns a prediction into state beside `idle`, which already observes the same thing.
  The ancestry check fails every correctly integrated rider, because the integrator REBASES
  every rider. "First GO wins" picks the runner by timing, not by who built the integration
  worktree. Its counter-proposal needed no new verbs: `wait --ready` plus a release note.
- **Codex:** a release cannot mean RAN. An integrator that fails to build the worktree and
  releases would tell every rider "RAN" when zero tests ran. So PASS/FAIL/ABORTED is an explicit,
  separate fact. The slot must also hold MAIN through the tier and the land, because otherwise a
  lander moves main and the green result cannot fast-forward. It preferred a batch register with
  a frozen membership and a fixed cutoff.
- **Where they agreed, which shipped:** a designated integrator, a claim on main, no ancestry
  test, an ETA that is advisory only, and an outcome that is reported rather than inferred.
- **Where they split, what the operator chose:** Fable's no-new-verbs shape plus Codex's explicit
  outcome. Membership is the integrator's own `--desc`. No batch table, no hold timer.

**What shipped (schema 14)**
- **`wait --on <run> --ready HEAD|<commit>`** stores the full object name in
  `session_waits.ready_sha`. It must resolve (`rev-parse --verify <rev>^{commit}` in the caller's
  cwd, anything starting with `-` refused), it needs `--on`, and it is otherwise the rider's word.
  Re-declaring replaces the wait, as every declaration does.
- **`who <run>`** switches WAITED ON to one line per waiter as soon as any waiter declared ready:
  `N READY, M not`, READY first, each with its commit, declaration age, last-seen age, deadline
  if passed, and its note quoted. "not ready" means "declared nothing ready". The short form is
  unchanged when nobody declared.
- **`release <slug> --outcome pass|fail|aborted [--note …]`** writes `claims.outcome` and
  `outcome_note` in the release's own transaction. The column carries a CHECK. `--note` needs an
  outcome, an outcome cannot ride a `--scope` narrowing, and every malformed report is refused
  before the ledger opens, so it releases nothing.
- **Every waiter's LANDED** (`wait check`, beat's one-shot notice) renders the released target as
  `released 2m ago, outcome PASS "<note>"`. A waiter that declared `--ready` also gets one caveat
  line when a released target carries NO outcome ("it says the claim closed, not that a run with
  your work passed"), and a different one when the target was ORPHANED (its integrator ended).
  A plain waiter hears nothing extra.
- **The protocol** lives in the README (§1c‴) and the skill: the integrator claims
  `.buddy/slot/<run>` and `.buddy/slot/main` with a FORMING `--desc`, refreshes it at GO (a
  refresh keeps the claim id, so the waits stand), pins, runs, lands and releases with the
  outcome.

**What it deliberately does not do** — No batch or attempt table, no frozen manifest (the `--desc`
is the manifest), no hold cutoff, no OVERDUE, no eject, no ancestry check of `--ready`, no
attribution engine for a red run. Nothing wakes or schedules anyone. A wait still reserves and
refuses nothing. Slots are per checkout, so repositories with separate ledgers do not see each
other's run.

**Skill rule, alongside** — The operator asked that the skill be updated for this and that keeping
it updated be a standing, gated rule. `TestSkillTeachesEveryVerbAndFlag` is the reverse of D-046's
check: every verb, subcommand and flag in the usage table is taught by `skills/buddy/SKILL.md`
or exempted in the test with a written reason. The hooks, `init`, and the operator's `pause`,
`resume` and `authority` are exempt, and so are `--help` and `--session` everywhere. Its first
run found ten names the skill had never taught, all predating this change: `ls`, `sweep --dry-run`,
`sessions --by`, `msg --from` and `--dry-run`, `ids ls`, `ids status` and `ids take --note`, and
`wait clear` and `wait ls`. `TestSkillCoverageGateFires` cuts `--ready`, `--outcome`,
`buddy sent` and `wait check` out of the real skill, and each must be reported.
`scripts/install-skill.sh`, run by `setup-clone.sh`, refreshes the user-level copy and keeps a
differing one as `.bak`. `check-skill.sh` gates it into a temp dir. Measured the same day: the
installed copy on the developer's machine was a release behind the repo.

**Test shape** — `cli/batch_test.go`, three sessions on real worktrees:
- Control: no rider declared, so the short form. Then a rider declares ready and the other only
  a note, and `who` lists both, READY first. Refusals leave the waits as they were: an
  unresolvable commit, `--ready` with no `--on`, and an option as the commit.
- Three malformed releases each leave the claim open. PASS with a note reaches the one-shot
  notice and the check.
- A release with no outcome gives the rider the caveat and the plain waiter none (the control).
  An integrator's bye gives the rider the ORPHANED caveat.
- `store/batch_test.go`: the round trip, the column CHECK with its control, and a schema-13 →
  14 migration.

The migration fixture rebuilds `claims` and `session_waits` from schema 13's EXACT CREATE text.
The first fixture used DROP COLUMN, and SQLite's text edit turned v13's trailing `--` line comment
into "incomplete input". The trailing comment is exactly what a real v13 ledger's ALTER ADD must
survive, so the fixture keeps it. `check-wait.sh` QA-6 drives the same round trip through the
built binary. Thirteen mutations each failed a test:
- the outcome not rendered;
- no caveat, a caveat for every waiter, and no ORPHANED caveat;
- "not ready" listed first;
- the outcome not stored, and the ready commit not stored;
- the timer refusal removed, the `--scope` refusal removed, and the CLI outcome check removed;
- the riders view never chosen;
- the migration arm skipped;
- the CLI discarding the resolved commit.

The first mutation run reported all thirteen killed over a FAILING baseline (the migration
fixture). The script now prints the baseline, and the run was repeated green.

**Codex code pass (fix first, five findings, all taken)**
1. **Beat could pass the harness's hook cap, and this predates D-049.** Beat wrote its notices
   ahead of an inbox drain bounded only by its own 8 KiB, so a LANDED line up to
   `maxHookWaitLine` plus a full drain could exceed 10,000 characters. Every message beside it
   was then marked delivered unread. The drain now takes the room the notices leave under
   `helloBudget` (`beatDrain`), the way hello's digest already did.
2. **`--ready` could record a blob.** `HEAD:x` resolved in one step as `HEAD:x^{commit}` can
   name a tracked FILE called `x^{commit}`. The revision is now resolved first and the full name
   peeled second.
3. **A swept claim took its outcome with it.** A rider now gets a caveat that the outcome is no
   longer on record, instead of silence.
4. and 5. **install-skill.sh:** it now refuses a symlinked or non-regular destination, and takes
   an mkdir lock, because two concurrent refreshes had backed up each other's copies.

Each fix has a test, and each test was watched to fail on its mutation. The first spellings of
two of them passed over their mutations:
- 3,000-byte messages never filled the drain to its edge, so the budget test passed without the
  fix.
- Asserting "row has since been swept" matched the target's own text, not the caveat.

Reverting `--ready` to the one-step resolve SURVIVES, as predicted: the peel alone refuses a
blob, and removing the peel is what fails.

**Fable review of the skill as an agent would act on it.** It found several gaps:
- A rider told "claim again" after LANDED would re-run a PASSed tier.
- "Pin the sha" was undefined.
- Main is only guarded if landers claim `.buddy/slot/main`, which is a convention and was
  written as a guarantee.
- `who` shows notes only once someone is READY.
- The no-outcome caveat reaches `--ready` riders only.
- There was no guidance for a red run, for a rider after FAIL, for work that changes after
  `--ready`, for leaving, or for a 3h default deadline that a hold plus a 65-minute tier can
  outlast.

All of it is now in the skill and the README.

## D-050 — One install/upgrade script rebuilds every binary the machine runs

2026-09-26 · operator request, right after D-049's schema bump

**What was wrong** — An upgrade touched four places by hand, and the one forgotten was the one
nobody could see. Measured on the developer's machine, immediately after D-049 was installed:
- The hooks' `~/bin/buddy` and `~/bin/buddylist` had been rebuilt, and the skill had been copied.
- The chat daemon was still running a `buddylist` built six days earlier. The launchd plist
  names the checkout's `bin/buddylist`, not `~/bin`, and no script built that path.
- The skill copy had been a release behind the day before.

After a schema bump, a stale `buddy` is a DENY (D-037), so "which copies exist" is a safety
question.

**What shipped** — `scripts/install.sh`, re-run after every pull. Each step is verified before
the next:
1. `go build -o` of both binaries into `BUDDY_BIN_DIR` (`~/bin`), plus every other copy found by
   where it is RUN from: the program `com.buddy-system.buddylistd`'s plist names, and the
   checkout's `bin/` copies. Never `cp`, because of the ad-hoc signature trap.
2. Each built binary is run once and must name itself (`buddy help`, and bare `buddylist`,
   whose usage exit is 2). A 137 is reported as a SIGKILL, never silently accepted as it would
   be in a hook.
3. `install-skill.sh`.
4. `launchctl kickstart -k` of the daemon, waiting up to ten seconds for `state = running`.
5. `setup-clone.sh` for this checkout.
6. A report of which of the eight hook lines `~/.claude/settings.json` wires. It never edits
   that file.

Every sub-step's exit status is the install's. The first draft piped the sub-scripts through
`sed`, and `sh` has no pipefail.

**Test shape** — `check-install.sh` (hermetic) redirects every destination into a temp dir and
switches off the daemon, setup-clone and the repo's `bin/`:
- A fresh install builds, runs and installs the skill, and reports EXACTLY the two hooks the
  fixture leaves unwired.
- A re-run is idempotent.
- An all-wired fixture reports all eight (the control).
- A directory where a binary goes fails loud, and never prints `done`.
- A failing skill step fails the install.

The two `buddylist` probe exits were each found by the check failing, not by reading: the missing
`help` verb, and the exit status that `| head` had hidden. `install.sh` was mutated four ways:
- The sub-step status swallowed, the hook report always "wired", and the directory check removed
  each fail the check.
- Removing the check that each binary names itself SURVIVES, as predicted. No fixture can make
  `go build` emit a different program, so it is kept as a belt behind `buddy help`'s
  `commit-gate` check.
- The first spelling of the hook-report mutation did not apply (a perl escape), and was
  re-spelled until it did.

## D-051 — `buddy init` says whether a session here will get a language server

2026-09-26 · operator request: "sessions spend a long time mapping the code on startup"

**What was wrong** — A fresh session maps the code by grep and `sed -n`, one round trip at a
time. `scripts/startup-report.sh`, 30 days to 2026-09-26: 14 sessions here reached an edit, after
a median 22 minutes (an upper bound: it includes the operator's turns) and 29 tool calls returning
89 KB; 0 of 518 calls were LSP; by hand, 277 of the Bash commands named `internal/cli/cli.go`.
Claude Code answers "where is X defined, who calls it" in one call through a language-server
plugin, and none was installed. Setting one up hit a trap on the first try: `go install` put
`gopls` in `~/go/bin`, which PATH did not name. The plugin would have been enabled with nothing to
run (the harness starts a server "by name from the user's PATH"), and sessions would have gone
back to grep with nothing on screen to say so.

**What shipped** —
- `buddy init` (so `setup-clone.sh` and `install.sh`) prints one `language server:` line per
  language that is at least 10% of the repo's recognised TRACKED source files: whether the server
  is on PATH (absolute entries only, executable by the caller), whether
  `<plugin>@claude-plugins-official` is enabled in user (or `$CLAUDE_CONFIG_DIR`), project and
  local settings (merged per full id, as the harness merges them), and `configured` or
  `NOT CONFIGURED` plus a fix line that, followed, fixes it: a server found where `go install` put
  it, or an install that would land there, carries the PATH step; a plugin some file says `false`
  to gets `claude plugin enable --scope <that file's scope>`; the line ends in `/reload-plugins`,
  or in a restart of claude when PATH must change. "Configured", never "ready": settings are the
  harness's first stage, and only the session sees the last. Seven languages: Go, Python,
  TypeScript/JavaScript, Rust, C/C++, Swift, Ruby; any other prints nothing.
- It never changes init's exit status and never blocks: settings are opened non-blocking, must be
  regular files, and are capped at 1 MiB; the file listing is streamed under a 10 s budget that
  closes the pipe itself. Managed settings, `--settings` and `--add-dir` are not read.
- The skill has "Mapping the code": reach for the LSP tool before grep; find references sizes a
  claim; with no LSP tool, tell the operator once (`buddy init` names the fix), never install it.
- `scripts/startup-report.sh [repo]`: from a session's first typed prompt to its first edit of a
  repo file, counts and bytes only; its `lsp` line is the before/after measure. "A repo file" is
  under any worktree, or under a removed sibling named `<repo>-…` (an existing one only when git
  says it shares the common dir), and transcripts are read from every registered worktree's
  directory: its first real run, on a repo whose sessions start in the main checkout and edit
  worktrees beside it that are removed once landed, reported 0 of 29 sessions reaching an edit
  (3 counting registered worktrees alone; 22 with the sibling rule).

**Cut** — a SessionStart digest line (every session pays, forever); installing servers or
enabling plugins (edits the machine and settings, which install.sh never does, D-050); `claude
plugin list --json` (the claims binary would fork the harness CLI); a committed code map (stale
under parallel edits); whole-repo packs; splitting `cli.go` for onboarding's sake; Serena until an
LSP is measured insufficient; `--others` in the file count (an un-ignored virtualenv would be
walked and counted as what the repo is written in).

**Reviews** — four passes, every finding reproduced by a test that failed first or confirmed by a
mutation that a test killed:
- Codex (xhigh), 8 findings, 7 fixed: a FIFO settings file hung init forever; plugin ids keyed by
  name, so another marketplace's `false` disabled the official one; "ready" overstated; any x bit
  counted a mode-0001 file executable; `..` paths and a symlinked root fooled the report's in-repo
  test; C-locale sed encoded é as two hyphens; byte counting was untested. Declined: honouring
  the empty PATH entry. Kept, documented: prompts beginning with `<` are skipped.
- Fable, harness semantics (docs and the installed binary): confirmed the settings layers, PATH
  lookup, the `LSP` tool and its operations, per-UTF-16-unit directory naming and the transcript
  flags; REFUTED "a plugin needs a new session" (`/reload-plugins` activates it); named
  `<pasted_content` as a typed prompt that begins with `<`.
- Fable, adversarial, ~45 inputs: init always exited 0, printed "ledger ready" first, created the
  ledger, never hung (1 s on a 1,000,000-file index). Fixed: relative PATH entries and a relative
  GOBIN were honoured against init's cwd.
- Codex (max) and Sonnet, second pass (8879ce4): the off-PATH `go install` fix ended in
  `/reload-plugins` with the PATH problem in a note below it; a `git` wrapper whose child held
  stdout outlived the listing budget (blocked 10 s in the test, 0.8 s after); `/./` segments were
  rejected with `..`; the check asserted neither medians nor row order, and no fixture called
  Grep, Glob, Task or Agent.

**Test shape** — `internal/cli/codemap_test.go` runs `init` in a real temp repo against a fake
server on a fixture PATH and fixture settings files; every NOT CONFIGURED row has its configured
control. It covers the precedence cases (including another marketplace's id), scope-named fixes,
FIFO / directory / bad-JSON settings, tracked-only counting with a staged control, cwd-relative
PATH and GOBIN (`t.Chdir`), and the git-wrapper timeout (a shim that backgrounds a sleep).
`scripts/check-startup-report.sh` (hermetic; needs jq, says NOT RUN without it) finds the
transcript directory for a repo path with a dot, an é and an emoji (the name written by hand from
the harness's rule), and checks the window's two ends, sidechains, calls after the edit, every
tool-kind column, bytes over string and array results, even and odd medians, row order, the age
window with a control, and that no marker text from the fixture is ever printed. The resolved-root
case bites only where the two spellings differ (macOS `/var`); on Linux CI it passes vacuously.

## D-052 — `msg`'s wake address is also written to stderr, and `sent` names it

2026-09-26 · issue #42

**What was wrong** — A coordinating session sent `buddy msg <target> "…" >/dev/null` to several
workers, treating the result line as a receipt it did not need. Four recipients had been idle at
their prompts for 45–54 minutes by `buddy sessions`. `msg` built the D-039 wake address for each,
on the result line as D-041 requires, and the redirect dropped it. Nothing drained until the
sender noticed the silence and woke each by hand: about 50 idle minutes across four sessions. It
is D-041's failure in its next shape. The wake is the one part of the result line that asks the
sender to act, and the only signal that the message will not arrive by itself.

**The rule** — when the wake clause is non-empty, `msg` ALSO writes one stderr line,
`buddy: message #N to <target> is queued; <the same wake clause>`, after the stdout line. Stdout
is unchanged (D-041), so `| head -1` and a stdout-only redirect still carry it; a `2>&1` reader
sees it twice, which costs nothing. The target is fenced as on stdout. With no wake, stderr stays
empty, so a stderr reader is never told to act on a session that drains on its own.

**Second half: `buddy sent` names the wake too.** The sender in that run found the stuck
messages only by reading `buddy sent` and `buddy sessions` side by side. Now every recipient
row still `queued` whose session the same `wakeClause` says needs a wake gets `; to wake it
now: …` after its standing, from one fresh observation per session per report. In the list (no
id), a direct send carries its wake on its line; a broadcast says how many of its sessions can
be woken and that `buddy sent <id>` names each address, so one send stays one line. The issue
asked for "after N minutes" and "quiet the whole time". Neither was built. `msg` prints the
same clause at age zero, so a threshold would only hide a fact the sender can act on now.
"Not seen since the send" cannot differ from "queued" here, because every hook that marks a
session seen (`beat`, `busy`) also drains its inbox.

**What it does not do** — same conditions, same text as D-039; buddy still wakes nothing, and
the wake still carries no content. `sent` still prints no body.

**Test shape** — `wake_test.go`: the three quiet cases assert one stderr line with the prefix and
the socket address and no body; every other case (seen recently, no socket, a regular file,
process gone, two live processes, ended, broadcast) asserts stderr is empty. Mutant with the
stderr write dropped: the three quiet legs fail, the rest pass.
`TestSentNamesTheWakeOfAQueuedQuietRecipient`: a queued direct send and a queued broadcast to
an idle session with its socket carry the wake (on bravo's row, and on the list line or as a
count); delivered-then-idle-again, a dead process and a missing socket carry none, on either
view.

## D-053 — A test binary that outlives its `go test` is ended from outside, and leaves a sample

2026-09-27 · issue #43

**What was wrong** — An `internal/cli` test binary was found reparented to launchd, spinning
70–86% of a core for 44 minutes against its own `-test.timeout=10m`, twice in one day on a box
at load 16–18. Nobody noticed until somebody read `ps`, and its load slowed every other
session's timing-sensitive tests. The issue's leading candidate was that the timeout panic
blocks writing to a dead stderr.

**What was measured** (go1.26.4, darwin/arm64), and what it refuted:
- An ordinary orphan dies by itself: killed `go test` mid-run, and cli.test (fd 1 and 2 on a
  pipe whose reader was gone) died about 40 s later, of SIGPIPE on its first `os.File` write.
  (The first attempt at this measurement matched the LINKER with `pgrep -f cli.test`, whose argv
  names the output file, and reported every child "gone at once". Match the test binary's
  parent, not a name.)
- **Refuted: the timeout panic does not hang on a dead stderr.** Orphaned toys blocked in
  `Sleep`, and in a preemptible spin, each exited at their 5 s timeout. From source (a Fable pass over GOROOT): the fatal
  panic prints through the runtime's raw write, drops EPIPE (darwin's `sigfwdgo` ignores the
  kernel SIGPIPE), then `exit(2)`.
- So 44 minutes past the timeout means testing's alarm goroutine never ran: the scheduler was
  stopped. A stop-the-world waiting on a goroutine that cannot be preempted (reproduced with
  `GODEBUG=asyncpreemptoff=1`, a call-free loop, and `runtime.GC()`) gives exactly the #43
  shape: orphaned, ppid 1, ~81% of a core, alive at 3× its timeout. The CAUSE in cli.test is
  still unknown; this reproduces the class.
  *Superseded in part by D-061 (#53):* the first live occurrence to be sampled was not the test
  binary at all. It was a `-race` forkExec's child, stuck in ThreadSanitizer before exec, with
  one thread and the parent's argv. #43's orphans had that shape too. The stop-the-world wedge
  above is still guarded, but it was never shown to be what happened.
- **Refuted: RLIMIT_CPU as the wall.** The kernel sends SIGXCPU at the soft limit (a spinning
  `sh` died of it in 2 s), but the Go runtime handles and drops SIGXCPU, and exceeding the hard
  limit sent no SIGKILL: soft 3 s / hard 6 s, still spinning 30 s later.
- A signal from ANOTHER process ends the wedge: SIGQUIT in 1 s, SIGTERM at once. At the default
  traceback level SIGQUIT's dump shows only the thread it landed on (an idle M), not the spinner.
- `go test` binaries carry no symbol table, so `sample` prints `???` for every Go frame. The
  pclntab survives stripping; `debug/gosym` over it named the spinning frame, function and line,
  from `sample`'s load-address offsets.

**What shipped** — `internal/testguard`, and `testguard.Arm()` as the first statement of
`internal/cli`'s `TestMain`:
- **In-process poll**: `os.Getppid()` every second; exit 3 when it differs from the value at
  Arm (not "equals 1", so a binary launchd started is never an orphan). The cheap exit for an
  orphan whose scheduler is healthy — about a second instead of up to the whole timeout. It
  cannot run in the wedge.
- **Watchdog**: the same test binary re-executed in a mode that never runs a test, holding the
  read end of a pipe whose write end only the target holds, so EOF tells it at once that the
  target is gone and it exits. It fires when the target is still orphaned three polls after the
  in-process guard should have ended it (so: wedged), or has run twice its `-test.timeout`
  (`-timeout 0`, or one so long that doubling overflows, means no wall). Then: `sample` to
  `$TMPDIR/buddy-testguard-<pid>-<unix>.sample.txt`, symbolized in place from its own
  executable's pclntab; SIGQUIT; SIGKILL five seconds later if that did not end it; and only
  then one stderr line saying what it did. Every step runs only while the pipe is open AND the
  pid still carries the kernel start time read when the watchdog began (D-025's identity), so
  nothing but the target is sampled or signalled. A watchdog that cannot start is one stderr
  line, and the tests run anyway.
- **A deliberate policy, not a detector**: a test binary whose parent goes away is ended even
  if it was healthy — a `go test -c` binary backgrounded from a shell that then exits, or run
  under a detaching `-exec` wrapper. Nobody is left to read its result.

**Review** — Codex code pass. Its P1 was real and reproduced before it was fixed: in #43's shape
the watchdog inherits the same dead stderr, and it announced itself BEFORE signalling, so its
first write killed it with SIGPIPE and the wedged target lived on. The legs had given the
throwaway parent `/dev/null` and could not see it. Now the wedged-orphan leg gives it a stderr
pipe whose reader dies with it (red at 45 s before the fix), and the watchdog reports last.
Catching SIGPIPE as well was built and cut: with the report last, that mutant left every leg
green, so it was a second fix nothing tested. Also taken: the pipe is not identity (a child between fork and exec holds the
write end for that instant, and check-then-signal has a window), so the start time is; the
target's pid is passed, not read from `Getppid` after the fact; the doubled-timeout overflow;
`sample` cancelled when the target exits; the test's own cleanup kills only a helper it
identified; the wall leg's helper timeout raised from 2 s to 10 s so a loaded box's startup
cannot fire testing's alarm before the wedge. Kept at 5 s against its advice: the plain-orphan
bound, which the issue asked for; measured exits are 0.87–0.92 s.

**Cut** — an in-process `time.AfterFunc(2*timeout, os.Exit)`, which the issue proposed: with a
healthy scheduler it duplicates testing's alarm, and in the wedge it cannot fire. RLIMIT_CPU, as
measured above. `debug.SetTraceback("crash")` so SIGQUIT would dump every thread: it would also
turn every ordinary panicking test in the package into a SIGABRT. Diagnosing the wedge itself:
no live occurrence has been sampled yet, and the watchdog's sample is what will name it.
Only `internal/cli` is armed; it is the only package with a TestMain and the one #43 caught.

**Test shape** — `internal/testguard`: every leg runs the test binary itself as a helper under a
parent the leg can kill.
`TestOrphanExitsWithinFiveSeconds`: a sleeping helper under a throwaway `sh`; control first (three
polls with the parent alive and nothing fires; the watchdog is alive after the helper's two GCs);
kill the parent; the helper is gone within 5 s with the guard's mark `3`, and the watchdog follows.
`TestWatchdogEndsAWedgedOrphan`: the M3 wedge under a throwaway parent; gone within 45 s; NO mark
(the control that the wedge was real — a healthy helper exits through the guard); one sample,
naming `testguard.spinForever testguard_test.go:`.
`TestWatchdogWallEndsAWedgeUnderALiveParent`: the wedge at `-test.timeout=2s` as the leg's own
child; ended by the wall with SIGQUIT on stderr, `test timed out` absent (testing's alarm never
fired), and the sample names the spin.
Mutants, each watched red:
- drop the in-process poll → the plain-orphan leg: ended at 4.96 s by the WATCHDOG, no mark (the
  mark assertion, not the 5 s bound, is what catches it);
- drop the watchdog's orphan arm → the wedged orphan is alive at 45 s;
- drop the wall arm → the live-parent wedge is alive at 90 s;
- leave the pipe's write end unreachable → the helper's GC finalizes it, the watchdog is gone at
  the control check, and all three legs fail;
- drop symbolize → both samples show `???` where the spin is;
- announce on stderr before signalling → the wedged orphan (dead stderr) is alive at 45 s;
- a start time that never matches → nothing is signalled; both wedge legs fail. The positive
  control that the identity check admits the real target.

## D-054 — A park with no claim to name keeps warm on a timer wait, and is told so before it parks

2026-09-27 · issues #44, #45

**What was wrong** — D-033's keep-alive works whenever it is armed, and it was almost never
armed. The operator asked how well idle sessions were staying warm while an orchestrator ran
hour-long hermetic tiers, and the answer took a hand-rolled script over one fleet's
transcripts (token counts and timestamps only) and ledger. Seven days, 2026-09-20 → 09-27,
129 sessions:

- wakes after 50–60 minutes idle: 19, **0** cold. After more than an hour: 149, **145** cold.
  D-033's edge holds exactly;
- **3** sessions declared a wait and **1** armed `/loop buddy wait check`. That one, an
  integrator, read the cache on 20 of 20 checks through its own tier runs;
- the cold wakes within four hours (92), by what woke them: the operator typing 37 (mean idle
  100 min), a peer message 29 (139 min; one land woke 15 parked lanes in 7 minutes, 14 cold),
  the session's own background run finishing 26 (87 min);
- own runs: 66 tier launches in 37 sessions. Of 34 wakes on a task finishing after 50+
  minutes, 27 were cold, every one past the hour. The week before, the same wakes came at
  54–58 minutes and 31 of 39 were warm: the tier crossing the hour is what turned them;
- 54.6M tokens re-written, about $491 at list rates. The 92 within four hours cost about $345,
  and a 50-minute keep-alive priced at the measured cost of a check would have cost about $87.
  Past four hours (mean 7.3 h, mostly overnight) it was a wash, $146 against $144.

Everything that tells a session to wait reaches it through a claim: a refused `claim` prints
the `buddy wait --on …` line, and the D-049 section teaches riders. The parks that cost the
most name no claim. A session on its own run holds nothing another session waits on, and
`wait --on` refuses your own claim, so the integrator of a shared run is in the same position
as a lone session running its own tier. A lane that has handed its work in waits for the
orchestrator, which is not a claim either. The timer form, `buddy wait` with no `--on`, has
covered both since D-033. Nothing taught it.

**The rule** — Mostly taught; three lines of output changed:
- The skill gets **"Before you park"**. Declare a timer sized to the park before ending a turn
  that something else will end: your own run longer than about 50 minutes, or parked for the
  orchestrator or operator. Arm the loop. `buddy wait clear` when something else wakes you
  first. Skip a park with no known end.
- The integrator's line says the tier it runs is such a park.
- An orchestrator tells a parked lane roughly when it will next need it, so the lane can size
  its wait.
- `hello`'s help line (D-046) names `--until` alone beside `--on`.
- A timer's declaration prints a third line: a timer never lands, so when something else wakes
  you first (a run's completion notice, a message, the operator), `buddy wait clear` and stop
  the loop. A reviewer running every step of a timer's life in a scratch repo found that no
  output said this anywhere. The only mention was `wait clear`'s own reply, which comes after
  the fact. A wait on a claim lands by itself and prints no such line. Two variants, both
  from the same review. Off the hour tier no loop was advised, so the line says only to clear:
  an open timer keeps the roster, `who` and `msg` reporting a working session as waiting.
  Declared with `--session` for another session (an orchestrator parking a lane), the line
  names that session and `buddy wait clear --session <id>`, to pass on. A bare `wait clear`
  there cleared the DECLARER's own wait and left the lane's timer open.
- The declaration's keep-alive line and `buddy --help` give a check's real cost (below). The
  12 h ceiling's refusal no longer claims a break-even ("past about sixteen hours"). It says
  only that nobody should wait through a night on a timer, and USAGE carries the per-model
  hours.
- `scripts/wake-report.sh` (#45) re-measures all of the above from the transcripts, counts
  only, so the next "how are we doing" is one command.

**A check is a turn, not a tool call.** D-033 and the skill called a check "a single tool
call". It is, inside a turn of at least three requests (the check, the scheduling call, the
end of the turn), each reading the whole prefix. Over the 20 measured fires: median 3, mean
4.05. USAGE's cost table priced a check as one read, a third of its cost, so its savings were
too high: on Opus 5 a four-hour wait saves 40%, not 80%. Corrected, at the median three reads:
break-even is about 5.5 h on Opus 5, 11 h on Opus 5.5 and 22 h on Fable 5.1, not 16/66 h. At the
mean 4.05 it is about 4, 8 and 16 h. A timer that reaches its own deadline also runs one last
check at it. The 12 h ceiling stands. It bounds a mistake, and whether
the session will be resumed at all is still the question that matters.

**Considered and cut** —
- *`wait --on` your own claim, for the integrator.* Its claim is held from forming to landing,
  not released when the tier ends, so a wait on it would land at the wrong moment. The timer
  says what is true.
- *A process or background-task target ("LANDED when pid N exits").* The harness already wakes
  the session when its task ends. The only thing missing was a request every 50 minutes until
  then, and a timer bounded by the run's length supplies it. A new target kind would also be
  the first that watches something outside the ledger.
- *A Stop-hook nudge when a turn ends with a background run going.* Buddy cannot see the
  harness's background tasks, and inferring a park from idleness is what D-033 ruled out.
- *Pacing.* The one looping session scheduled every 25 minutes against the check's `next check
  in 50m`, using the loop as a heartbeat for its lanes and tier. The harness's scheduler text
  suggests 20–30 minutes for exactly that. A shorter delay is safe, only costlier, so the check
  is unchanged and the skill and USAGE say what it costs.
- *A cold-cache note on `msg` to a quiet lane.* The roster already prints `cache 1h cold`
  (D-020), and a lane that has work must be woken either way.
- *"Always keep warm."* Past four hours the fleet's parks broke even.

**Test shape** — `TestWaitTimerSaysHowItEnds`: a timer's declaration is three lines, the third
the advice held to its text, and the arming line names a check's cost; re-declared on a claim,
the same session gets no such line (the control), and `TestWaitDeclareCheckLandOnceThenNoWait`
still holds a claim wait to two lines. `TestWaitTimerAdviceDeclaredForAnotherSession`: declared
with `--session`, the line names the lane and the flag, and following it verbatim from the
declaring shell clears the lane's timer and leaves the declarer's own. `TestWaitArmsNothingOffTheHourTier`
gains a timer leg: always "clear", "stop the /loop" only on the hour tier. Mutants: the line
never printed; printed for every wait; the `--session` variant replaced by the self one; the
loop clause unguarded. Each fails its test. `TestHelloHelpLineTeachesTheTimerWait` reads the help line through the same
parser as the name gate and requires it to teach `wait --on`, `--until` and `check`. Mutants:
the `--until` clause dropped fails it; the flag renamed fails it and the name gate. The name
gate's count floor (9) could not see the drop, since the line already named 15. The skill's
two gates (every name it teaches exists; every name in the usage table is taught) pass on the
new section. `scripts/check-wake-report.sh`, from `check.sh`, covers the instrument.

## D-055 — A session that declared a handoff size is told, once per turn, when it has grown past it

2026-09-27 · issue #46

**What was wrong** — An orchestrator could not tell that it had grown past the point where its
operator wanted it handed off, and nothing told it. The roster printed `prompt 507k` for anyone who
looked, but the model does not know its own prompt size, and `buddy status` is a place it has no
reason to look. Measured over one fleet repo, 15 days (2026-09-13 → 09-27), 173 sessions, token
counts and timestamps only: 53 sessions crossed 500k, and there were 4 orchestrator successions.
In 3 of the 4 the **operator** noticed the size and started the handoff. The one that went well
was told early (~290k): it finished its batch, wrote a handoff file, woke its successor and
released `orchestrator`, and the successor had re-claimed it and re-woken 8 lanes 43 s later, with
no operator prompt. The one that went badly auto-compacted at 970k and grew back to 919k before
anyone asked for a brief. Its successor's first request was a 96.6k cold write, and re-orienting
took 17 operator prompts over 2 hours.

**The rule** — `BUDDY_HANDOFF_AT` is a size the operator DECLARES in one session's environment
(`BUDDY_HANDOFF_AT=500k claude`), parsed exactly as `BUDDY_CONTEXT_WINDOW` is (`declaredWindow`:
`k`/`M` suffixes, a bare count, the same wrap guard; unset or unparseable is off). When it is
declared and the session's last OBSERVED prompt (the number the roster prints as `prompt N`, for
the current incarnation only) is at or past it, `busy` adds one line to the prompt that opens the
turn:

    BUDDY: your prompt was 507k at your last observed request (turn 3m ago), at or past the 500k
    you hand off at (BUDDY_HANDOFF_AT) — finish the round, write the handoff, and start your
    successor (buddy skill: Running a fleet)

It rides the SAME single `UserPromptSubmit` document as the inbox drain, ahead of the inbox header
(it is buddy's own observation, so it must not sit inside the block marked as peer text), and it
prints even with nothing queued. The drain's bound, fence and write-then-mark (D-040) are
unchanged. It is level-triggered: every turn-opening prompt past the size carries it. An ENDED
session that is handed a prompt is still told. Its observation belongs to its own incarnation
(`bye` does not change it), and a prompt arriving under the id means the process is running, so
the stale fact is the ledger's "ended", not the number. A test pins this, so adding a `Live()`
guard later would be a decision rather than an accident.

- **Declared, not derived.** Nothing in a transcript says which session is the orchestrator, and
  roles were cut (D-015). The operator sets it on the one process it applies to, so lanes are never
  told. A hook is a child of its own session's process, so the declaration reaches no other
  session. Settings can set it for every session if an operator wants that. Measured for #47:
  `herdr tab create … --env BUDDY_HANDOFF_AT=500k` puts it in the lane's `claude` process
  environment (`ps eww`), so that lane's hooks inherit it.
- **"Last observed", never "now".** The number is the one the last `Stop` or `beat` read off the
  session's own transcript, so it lags one request. The line carries the turn's age, as the roster
  does. A prompt hook cannot know the size of the request it is about to open.
- **On `busy` and never on `beat`.** A handoff is decided per turn. `beat` runs on every tool call,
  and fifty copies of the line per turn, on the session that can least afford the context, is a
  line that gets tuned out. `busy` also reaches a turn that runs no tool at all. The cost: `busy`
  is an optional hook, so a machine without it wired never sees the line. D-040 said `busy`
  "carries only the inbox". This is the one addition, for this reason.

**What it does not do** — refuse, reserve or schedule anything (invariant 10: an observation);
name a role or reserve a slug (D-015, D-030); read a transcript itself (it reads the ledger's
observations through `sampleOf`, the same read `wait` and `who` make, which loads every retained
sample; it runs only when the variable is declared, and its cost at fleet size is not measured);
or say what to do beyond pointing at the
skill's recipe, which the skill owns (#47, "Running a fleet"). It is not a compaction trigger:
the harness's own `--autocompact <size>` is the tool when compaction is enough.

**Test shape** — `handoff_test.go`. A table over the pure decision: unset, garbage, zero,
negative, a fraction, a value that would wrap, one token under, exactly at, over, no observation,
`500K`, padded, `1M` under and at, a bare count. Each silent row sits beside a speaking row that
differs in one input, and each speaking row is checked to be one line, to name the recipe and
never to say "now". End to end through the hook: undeclared, unparseable, under and unobserved stay
exactly as before (silent with nothing queued), each followed by a positive control on the same
fixture; at, over and `1M` speak, again on the next prompt. Declared, over and a queued message
give ONE document with the note before the inbox header and the message marked. Every silent case is
also run with mail queued (delivered, marked, no line), and a label-addressed row under a declared
size is still owed. Both documents are checked to name `UserPromptSubmit`. These three came from
the Codex code pass, which found no implementation defect. A failed write
marks nothing. `beat` never carries it (control: `busy` on the same session does). A revived
incarnation is not told its predecessor's size. Mutants, each watched red: `<` to `<=`, the
observation guard dropped, the declaration guard dropped, the wrong environment variable,
both incarnation guards dropped (`ContextSamples`' JOIN and `sampleOf`'s check; either
alone holds, so each single drop is an equivalent mutant, measured), the age dropped, the note after the inbox, the note silent without
mail, the note dropped, delivery marked before the write, the recipe pointer renamed, Codex's two
(a declared-but-silent busy that returns before the drain; the wrong event name when the note
prints), and a `Live()` guard.

## D-056 — A coordinator opens its own lanes with the harness, and buddy stays out of it

2026-09-27 · issue #47 (reopens #22's measurement, not its decline)

**What was wrong** — The operator asked whether an orchestrator could open its own sessions as
work needs them, including a successor orchestrator when its context passes 500k. In one fleet
repo over 15 days (173 sessions), every session had been opened by hand. There were 326 bare
`claude` launches in shell history, 0 with a prompt argument and 0 with `--model` or `--name`.
Briefing was done by typing: 98 first prompts within ten minutes, 9 pasted briefs of 1.2–5.6 KB
(three identical, pasted into three panes), 8 prompts asking the orchestrator to put a brief on
the clipboard, and 37 "I opened another session" notices. That came to about 3.1 minutes per
session opened. #22 declined `buddy spawn` and `buddy exit` (a supervisor is out of scope) and
left open whether the harness surface would do. Nothing had measured it, and the skill said
nothing.

**Measured (2026-09-27, from inside Claude Code sessions in auto mode; every call allowed)** —
- `herdr tab create --workspace <ws> --cwd <repo> --label <l> --env K=V --no-focus`, then
  `herdr agent start <l> --kind claude --pane <id> -- -n <l> --session-id <uuid> "<brief>"`,
  opens an interactive lane in a new tab. It honours `--session-id`, `--env` reaches the lane's
  process (`ps eww`), and its `hello` gives it a roster row with `pane herdr:…`. It started in
  **manual** permission mode. `herdr pane close` ended it, `bye` ran, and the row went `ended`
  within seconds.
- `claude --bg -n <l> "<brief>"` starts a background session. It runs the hooks and prints
  `backgrounded · <8hex> · <l>`, it **ignores** `--session-id`, and `claude stop` ended it through
  `bye`. It refuses an untrusted workspace. `claude stop` cannot end an interactive lane.
- `claude -p --session-id <uuid>` reaches `hello` under exactly that id.
- `claude agents --json` lists every session with `sessionId`, `name`, `kind`, `status`, `pid`,
  `cwd` and `waitingFor`, with no TTY.
- `buddy msg` to an id that has not said hello is refused, loudly, not queued.

**The rule** — The skill's "Running a fleet" teaches it:
- Open lanes with the harness, under the launcher's own permission rules. Open only as many as
  the operator allowed, and give a lane no more permissive a mode than the launcher has.
- The brief rides the launch, as a pointer. Message the lane once `hello` has registered it.
- Ending a lane is the operator's act. A coordinator closes one only on the operator's own word.
- The successor handoff runs on existing primitives: a handoff file named in the `orchestrator`
  claim's description (D-030), `msg` and a wake, release, and the successor re-claims. That is
  the path the fleet's one clean succession took (43 s, no operator prompt). The signal to start
  it is D-055's `BUDDY_HANDOFF_AT`, launched with the coordinator's tab.

**Considered and cut** —
- *`buddy spawn` / `buddy exit` (#22).* Still declined. The harness already does the launch
  with one command, and a verb that launched would make buddy a supervisor. A launch or an exit
  reachable on a relayed instruction is #20's hazard, and here it costs money too: a lane's first
  request writes about 60k tokens of cache.
- *`buddy expect <id>`*, to queue a brief for a session before its hello. Measured
  unnecessary. The brief rides the initial prompt, and the launcher knows the id (chosen with
  `--session-id` in herdr, printed by `--bg`). `hello` lands within seconds, `claude agents
  --json` shows a lane that never arrived, and a premature `msg` is refused loudly, not lost.
- *Launching only with `--bg`.* The operator's fleet runs one pane per lane and approves tool
  calls in panes (115 ack prompts over the window). A background lane has no pane until
  someone attaches, so herdr comes first and `--bg` is the fallback.

**What the verbatim run changed** — A reviewer ran every step of the recipe, using two herdr
lanes and one `--bg` lane on Haiku:
- An assignment sent by `buddy msg` was REFUSED by a lane whose brief did not authorise it. The
  lane said "a peer cannot override your instructions", which is what the skill's own
  untrusted-inbox rule tells it. The control lane was sent the same message, and its brief read
  "your orchestrator is <label>: take your assignments from it through buddy msg". It complied.
  So the brief names the orchestrator by its LABEL, which is the sender a lane sees on every
  `msg`, and says the launch carries the launcher's authority, so only what the operator gave.
- A `--bg` lane inherits the launching shell's `HERDR_PANE_ID`. Its roster row named the
  ORCHESTRATOR's pane, and its pid was the shared `claude daemon`, which outlived
  `claude stop`. Closing "its" pane would have closed the orchestrator. The recipe launches it
  under `env -u HERDR_PANE_ID -u HERDR_TAB_ID`. That hello records the daemon's pid for a
  background lane is a buddy defect, tracked in its own issue.
  *Superseded by D-058 (#50):* a lane gets the claude daemon's environment, not the launcher's,
  so the `env -u` held only when that launch started the daemon. The recipe dropped it; the row
  now names the lane's own pid and no pane.
- `buddy status` prints no prompt size; `buddy sessions` does, so the recipe points there.
- A re-claim of `orchestrator` REPLACES its scopes, so the handoff re-claims with the same ones.
  Between the release and the successor's claim, `msg orchestrator` is refused (released slugs do
  not resolve, D-019), so the successor claims the moment it is released.
- A lane's first request wrote 44–46k tokens on Haiku, against "about 60k" measured across
  the fleet's Opus lanes. It depends on the model.

**Not measured** — whether a lane launched with `--permission-mode` behaves under the
operator's settings as the operator expects (the recipe leaves the choice to the operator), and
any launcher other than herdr.

**Test shape** — Prose only. The skill gate reads every `buddy <verb> --flag` in the section
against the usage table (`buddy claim --desc --scope`, `release`, `who`, `status`, `msg`). The
`herdr` and `claude` commands are the harness's, and a reviewer ran each verbatim in a new
herdr tab with Haiku lanes.

## D-057 — The test suite never runs with git's local environment

2026-09-27 · issue #49

**What happened** — To test exactly the commits being pushed, and not a peer's half-written
files in the shared checkout, a session pushed from a throwaway LINKED worktree
(`git worktree add --detach`). The pre-push hook ran the hermetic tier from it. Git (2.54, the
version measured) runs the pre-push hook with `GIT_DIR` set, and in a linked worktree the value
is absolute (`<repo>/.git/worktrees/<name>`). Only pre-push was measured; the charter records
that pre-commit exported no `GIT_DIR` in its own measured setup. Nothing between the hook and the test binaries removed it.
The fixtures' `git -C <tmp> …` inherited it, and `-C` does not override an absolute `GIT_DIR`, so
every fixture ran against the real repository:
- the refused-repository fixture (`discovery_test.go`) wrote `core.bare = true` and
  `core.repositoryformatversion = 99` into the real `.git/config`, after which every git command
  in the checkout died ("Expected git repo version <= 1, found 99");
- the claim gate then failed closed for every session in the repo, exactly as invariant 3
  requires. It refused Bash and Edit alike, so the session that caused it could not repair it,
  and the operator restored the two values by hand;
- fixtures created two linked worktrees and their branches (`wt`, `wtB`) in the real `.git`
  and committed onto the pushing worktree's detached HEAD. They were pruned and deleted.

The tier went red, so the push was blocked and main was untouched. It had never bitten from the
main checkout, only because there a hook's `GIT_DIR` is the relative `.git`, which resolves
inside each fixture's own temp dir. CLAUDE.md already said `GIT_*` leaks through hooks, in the
other direction: a hook's child git needing it. This is a test suite running inside a hook.

**The rule** — Nothing of git's local environment (`git rev-parse --local-env-vars`, united
with a fixed copy of it) reaches the suite, at four layers, since a guard's scope is every path
that bypasses it:
- `.githooks/pre-push` unsets it after discovery and the local hook, just before the tier.
- `scripts/check.sh` unsets it as its first act, for any caller.
- `scripts/lib.sh` unsets it when sourced, for a done-check run on its own inside some other
  hook.
- `testguard.ScrubGitEnv()` runs inside the `TestMain` of every package that imports `os/exec`
  and names `"git"` (`cli`, `buddylist`, `testguard`), for a `go test` started from inside any
  hook: a machine's own `hooks.local`, a `rebase --exec`, an editor's. A test that needs
  `GIT_DIR` sets it with `t.Setenv` after the scrub.

The fixed copy is there because the one git that cannot answer `--local-env-vars` may be the one
pointed at a broken repository. It is a union, never the only source. A test fails when git
lists a name the copy lacks, and check.sh fails when any of the four copies (Go, pre-push,
check.sh, lib.sh) differs from the others. The first shell copies held six names against Go's
fifteen, so a git that could not answer would have left `GIT_CONFIG` and eight more set (Codex
code pass).

**Considered and cut** —
- *Stripping `GIT_*` wholesale.* That takes `GIT_CONFIG_GLOBAL`, `GIT_CONFIG_NOSYSTEM` and the
  like with it, which a fixture may legitimately depend on. The local list is git's own definition
  of "which repository", and a test holds a non-local name surviving.
- *Only the hook.* It covers the path this took, and nothing else that runs `go test`.
- *Rewriting every fixture's git helper to set `cmd.Env` itself.* Eight files spawn git, and
  production code the tests drive in-process spawns more. The process-level scrub covers all of
  them at once, and the gate stops a ninth file from arriving without it.
- *Forbidding pushes from a linked worktree.* The push was a reasonable act, and a hook that
  cannot run safely from every worktree is the defect.

**What the Codex code pass changed** — the shell copies became the whole list, with the
equality gate. The package gate was keyed on one call spelling and accepted a scrub anywhere in
a `_test.go`: it missed `exec.CommandContext(context.Background(), "git", …)` and passed a scrub
in an unused helper. It is now keyed on the `os/exec` import and the `"git"` literal, and it
reads the scrub from inside `TestMain`'s body. QA-6 checked six names, and QA-7 planted only
`GIT_DIR`. Both now check git's whole list, and QA-7 runs again with no git on `PATH`, where only
the fixed copy can clear anything.

**Test shape** — `testguard`: the scrub clears every listed name and leaves a non-local one;
with no git on `PATH` the fixed copy still clears `GIT_DIR`; the copy covers everything git
lists; and end to end, a fixture-style `git -C <tmp> init` + `config core.bare true` under an
inherited absolute `GIT_DIR` reaches a stand-in "real" repository (the control, proving the leg
is armed on this machine's git), and under the scrub does not. `check-pre-push.sh` QA-6: a REAL
push from a linked worktree of a throwaway repo. First a raw hook records its env and must see
an absolute `GIT_DIR` (the control; if a future git stops leaking it, the leg says to re-derive).
Then the real hook, its tier stubbed to record its env and the repository it finds. A second
local name (`GIT_NO_REPLACE_OBJECTS`) is planted on both pushes, the raw hook must see it, and
none of git's local names (its own list united with the fixed copy) may reach the tier. The tier
must still find the pushed worktree from its cwd. QA-7: sourcing `lib.sh` clears three planted
local names, `GIT_DIR` among them, both with git on `PATH` and with none. `check.sh`: a
source-shape gate fails any package that imports `os/exec` and names `"git"` without a
`ScrubGitEnv()` statement inside its `TestMain`, against a planted five-package control: no
scrub; a scrub only in a comment; a scrub in `TestMain`, the one that passes; a scrub in an
unused helper; a `CommandContext` with another context argument. A second gate holds the four
fixed lists equal. Mutants, each watched red: the hook's unset reduced to `GIT_DIR` (QA-6),
`cli`'s scrub removed (the gate names `internal/cli`), the scrub a no-op (three testguard test
functions), `lib.sh`'s list cut back to the old six (QA-7's no-git run), and check.sh's list
cut back (the equality gate).

## D-058 — A `claude --bg` lane anchors at its own process, and its row names no pane

2026-09-27 · issue #50

**What was wrong** — A lane started with `claude --bg` was registered under the wrong process
and, from a herdr pane, the wrong pane. In D-056's verbatim run the row read `pid 41756 pane
herdr:w18:p9`. The pid was the shared `claude daemon`, which outlived `claude stop`, so the
roster's `pid N` / `GONE` said nothing about the lane. The pane was the ORCHESTRATOR's: the
recipe's close step is `herdr pane close <pane id>`, and an operator reading the row would have
closed the coordinator.

**Measured** (2026-09-27, Claude Code 2.1.283, five Haiku lanes launched from pane `w18:pA`):
- The tree is hook → lane → pty host → daemon → launchd. The hook's DIRECT parent is the lane:
  a one-off `--settings` hook recorded ppid 14509, and `claude agents --json` named 14509.
- The lane RETITLES its argv. procargs2 gives exec path `…/versions/2.1.283` (base `2.1.283`)
  and argv[0] `"claude bg-spare"`, one string with a space in it. The pty host is `"claude
  bg-pty-host"`, the daemon exec `…/bin/claude`. So D-025's walk matched nothing until the
  daemon. An interactive session is exec `…/bin/claude`, argv[0] `claude`, the hook's direct
  parent.
- `bye` after `claude stop` ended the row 5 s later through the OWN-registration arm: its walk
  landed on the same daemon `{pid, born}`, removed its own row and found no other. That was not
  a liveness verdict. Two lanes on one daemon pid did not interfere (separate session ids).
- A lane runs in the DAEMON's environment. A lane launched with `HERDR_PANE_ID` set, under a
  daemon started without it, had no pane. `BUDDY_HANDOFF_AT=123k claude --bg …` did not reach
  the lane (absent from `ps eww`). D-056's `env -u` therefore held only when that launch was
  the one that started the transient daemon.

**The rule** — The walk also matches an argv[0] whose first space-separated word is EXACTLY
`claude` (the harness's retitle, `claude <mode>`). An exact name on any of a process's names
still wins, and the walk still stops at its first match. So an interactive session in its
measured shape (the hook's direct parent, argv[0] `claude`) anchors exactly where it did:
nothing above or below it can newly win. A process that sits BETWEEN a hook and its session and
calls itself `claude <something>` (a wrapper retitled that way) would now win the walk, and its
session would record no pane. That is the name trust D-025 already extended to an argv[0] of
exactly `claude`, not a new boundary. A `--bg` lane now anchors at itself. Its row names the pid `claude
agents` shows and reads `GONE` after `claude stop`, and `bye`'s D-025 fence judges the lane,
not the daemon, which is strictly tighter. An anchor matched ONLY by the retitle records no
terminal handle: a pane read from the daemon's environment names whoever started the daemon,
and no handle is honest where a wrong one is not.

**Cut** — Keeping the daemon as the anchor and flagging it: the pid would still say nothing
about the lane, and two processes on one session id under one daemon would share a
registration, which is D-025's defect by another road. `CLAUDE_JOB_DIR` as the background
signal: it appears only in the lane's environment, but it is as undocumented as the retitle,
and the retitle is what the walk already reads. A looser retitle match (a path before the
space, a prefix such as `claudette`): an exec path with a space in it would anchor a node
process.

**The upgrade residual** (Codex code pass, reproduced as a test that pins it). A lane already
running when this binary is installed registered the DAEMON, and possibly a pane. After the
upgrade its hooks anchor at the lane. Its next `hello` brings no pane, which the store reads as
"keep" (D-025's rule for a refresh that brings nothing), so the old pane stays. Its `bye` is a
stranger to the daemon's registration, which is still alive, so the row stays live and its
claims keep refusing. The remedy is the operator's `buddy bye <id> --force`, or stopping `--bg`
lanes before an install. No lane started after the upgrade can get into that state, and 0
background lanes were live when this was built (`claude agents --json`). A store-level migration
(an "explicitly clear this pane" refresh, and treating a registration that is the caller's own
ancestor as the caller's) was judged not worth a wider store API for a state that ends when the
last pre-upgrade lane does.

**What it does not do** — It passes no environment into a lane (it cannot). The skill now
says so: coordinators and their successors launch in a herdr tab with
`--env BUDDY_HANDOFF_AT=…`, never with `--bg`. Nothing ends on `GONE`, as before. If a
future harness stops retitling, a lane falls back to the daemon anchor, which is the
behaviour before this record, not a new failure.

**Test shape** — `proc_test.go`: the walk is now a pure function over an injected process
lookup (`anchorWalk`), tested on the measured trees. Covered: interactive at depth 1 and
through a wrapper; a retitled descendant of an interactive session; an interactive session
started inside a lane (the interactive one wins); the lane anchoring at itself, with the
daemon-alone control; npm `node` unbound; `claudette x`, `xclaude y`, `/opt/claude x`, an exec
path with a space, and `claude ` with nothing after it all unbound; an exact name beating a
retitle on one process; a failed lookup; the hop bound and its control. `hello` with a retitled
anchor records the pid and no pane, with an interactive control one input away that records
it. The lane's row reads `GONE` when the lane is gone while the daemon above it lives on, with a
live control. Through `beat` and `bye`: a retitled beat binds an unbound lane to itself; a
second live process on the id holds the row open against the lane's `bye`; and the lane's own
`bye`, sent while it is still alive at SessionEnd, ends the row. The upgrade residual is pinned
as above, with `bye --force` as its remedy. The Codex code pass found no defect for
registrations made after the upgrade. It found the two upgrade residuals, the wrapper case
(now qualified above), the two untested call sites and a GONE test that did not keep the daemon
alive; the last three are now tested. Twelve mutants, each watched red: the
retitle rule dropped; a path, a `claude` prefix or an empty remainder accepted as the retitle;
the retitle tried before the exact name; the retitled bit dropped; the walk continuing past a
retitled match; the hop bound off by one; beat's and bye's anchor emptied for a retitled caller
(the two call sites Codex named); and the pane recorded always, or never.

## D-059 — A wake's text names its message and the second it was named, and no content

2026-09-27 · issue #51

**What was wrong** — The wake `msg` names (D-039, D-041, D-052) and `sent` repeats was one fixed
string, `buddy mail is queued for you: run buddy inbox`. Claude Code drops a peer message
IDENTICAL to the previous one from the same sender. Measured 2026-09-27, in a verbatim run of the
skill's "Running a fleet": three wakes to one Haiku lane, each with exactly the text `msg` printed,
at about 4:27, 4:28 and 4:28:40. The first two were delivered. The third showed in the lane's pane
as "Dropped a peer message … identical to the previous message from this sender". The sender's
SendMessage returned "queued there", and `buddy sent` then showed the message undelivered and
suggested the same text again. A retry about 80 s after the last delivered wake went through; the
window was not measured. The sender IS told, but late: minutes after the send, a "[Cross-session
delivery notice]" said the message was dropped and NOT delivered, and to treat it as unsent. An
orchestrator that hands a lane two pieces of work in a row, or re-wakes a lane that did not act,
lost the second wake. The mail stayed queued; the lane was not woken.

**The rule** — The wake's text names the message and the moment the wake was named:

    buddy mail #<id> is queued for you (<HH:MM:SS>): run buddy inbox

`<id>` is the send's id, the `#N` the recipient's drain prints (D-043); on a `sent` row of a
broadcast it is the broadcast's id. `<HH:MM:SS>` is the local time at which `msg` or `sent` named
the wake. Two consecutive sends differ by id even within one second, and `sent` re-suggesting one
message differs from `msg`'s suggestion once the clock has moved a second. The text still ends
"run buddy inbox", and stderr (D-052) carries the same clause. `sent` still observes each session
once per report; what it remembers is the ADDRESS, and each send's line names that send's own id.

- **Still content-free (D-039).** An id and a clock reading, never the body, the sender, or the
  kind. The body stays in the ledger, fenced, and is delivered by the drain.
- **Local time, to the second.** It is what the recipient's pane shows beside the message, and a
  second is finer than the one measured window (a retry 80 s later went through). A nonce was
  considered: it is unique too, but tells a reader nothing, where the id says which mail and the
  time says which suggestion.

**What it does not do** — buddy still wakes nothing, and still names a wake under exactly D-039's
conditions. It does not detect a dropped wake, retry, or read the delivery notice; `sent` is still
the report of what the ledger holds. It is not unique in every case: two suggestions for ONE
message in the same displayed second are identical, and if both are sent in a row the harness drops
the second, named under a second after the first. The clock face also repeats (the next day, a
clock set back), which matters only if that old text was the sender's previous message.

**Test shape** — `wake_test.go`. `TestMsgNamesTheWakeAddressOfAQuietRecipient`'s quiet cases now
spell out the whole clause, `#1` and the fixture clock's local `HH:MM:SS`, and require stderr to be
exactly `buddy: message #1 to <target> is queued; <that clause>`.
`TestWakeTextIsUniquePerSuggestion`: two sends to one idle recipient at the same instant give `#1`
and `#2` texts that differ, stderr matching stdout for each, no body in either; `sent 1` 90 s later
names `#1` with the new time, unequal to `msg`'s; `sent`'s list names each send's own id, at the
time `sent` ran, on that send's line; and a correction's wake (`msg` and `sent 3`) names the
correction's id, not #1. Mutants, each watched red (11 of 11): the old fixed text, time without id,
id without time, UTC for local time, `sent` memoizing the whole clause per session, `msg` naming id
0, stderr rendering a different clause than stdout, and `sent` rendering an earlier time than the
one it names the wake at, the same only on the list line, and a correction naming the id it
corrects (on `msg`, and on its `sent` row). The UTC mutant dies only where local time is not UTC (measured on this box, EDT); on a
UTC machine it is equivalent.

## D-060 — The Stop hook cannot see the turn's reply; it says so, and the next prompt records it

2026-09-27 · issue #52

**What was wrong** — `idle` (the Stop hook) reads the turn's final usage record from the
transcript to date the idle mark, fence it to this incarnation (D-016, #11), and record the
footprint the roster and the handoff note print (D-015, D-020, D-055). The harness has not written
that record when Stop runs. `idle` therefore read the PREVIOUS request, which in a prose-only
session is the previous turn. Found in a verbatim run of #47's recipe: a Haiku lane's roster row had
no `prompt` after its first turn, then showed turn 1's time after turn 2 and turn 2's after turn 3,
and its first `busy` printed no handoff note. Three things followed. The footprint and the note
lagged a whole turn, and a first turn got none. `idle N` counted from the previous request, so a
lane that had just answered read "idle 50s". And after a revival the previous request is the
predecessor's, so the fence refused the revived session's first Stop: it reported no idle mark at
all.

**Measured** (2.1.283, interactive Haiku lanes in a herdr tab, hooks added with a one-off
`--settings`; counts and timestamps only):
- A probe hook with a detached poller: the turn's final record was missing at 11 of 11 Stops
  (prose turns, one- and two-tool turns, long output). The harness writes it WHILE hooks run: a
  probe that held its hook open for 5 s still saw it land. That probe timed from Python's own
  start, 20–30 ms after the spawn, so its "33–83 ms after Stop" understates the spawn-relative
  figure below.
- A Go timer as the Stop hook, exec'ing `buddy idle` 0.3 ms into the hook and alternating
  binaries against a scratch ledger. A debug build polled after its work to find the landing, from
  its own process start: **p50 96.2 ms, p90 98.0, max 108.3 (n=20)**, a tight cluster that looks
  like a write-behind flush. The installed `idle` (3962ef7) ran in **26.4 / 42.7 / 46.0 ms**
  (p50/p90/max, n=20).
- At the next prompt the reply was on disk for 7 of 7 prompts that a human or a separate message
  started (5–74 s after the Stop), and for 3 of 4 that a queued message started 40–151 ms after
  it (missing at 40 ms).
- The Stop payload's `last_assistant_message` is the reply's TEXT, a string with no usage in it.
  Its `prompt_id` is the `promptId` the harness writes on that turn's own user records: a typed
  prompt, its tool results, and a peer message (`isMeta`, `promptSource` system). Checked on 3 of
  3 turns, one of each kind.

**The rule** —
- **Pending is read from the file.** `readTurn` also returns the newest non-sidechain
  `"type":"user"` record (a prompt or a tool result). If that is newer than the newest usage
  record, the reply to it has not landed. This is exact, with no clock threshold. A tie is pending,
  because the two errors differ. Calling a landed reply pending records no footprint for one turn,
  and `busy` records it at the next prompt. Calling a pending reply landed is the original bug. The
  check costs one parse: the last such record in append order, found behind a literal prefilter
  that matched 188 of 188 user records, and nothing else, in this session's own transcript.
- **The Stop's turn is named.** If this Stop's `prompt_id` record is in the tail, and a newer user
  record belongs to ANOTHER prompt, the session has begun another turn. That is a queued prompt, or
  a successor incarnation after a bye and a hello. The Stop is late, and records nothing: no
  footprint, no base, no idle mark. The Codex code pass found the successor case: the successor's
  pending prompt passes the time fence alone, so without this the new pending path would have marked
  a working J idle on I's delayed Stop. If the named turn's record is not found (an unknown kind of
  turn, or a harness that sends no `prompt_id`), the time fences below apply unchanged.
- **`idle` reads once.** If the reply is on disk (rare at Stop), everything is dated and fenced by
  it, as before. If it is pending (the norm), no footprint is recorded: the reply it can see is the
  previous request's, and the ledger already holds it or a newer one. The mark is dated by the
  write. It is fenced by the pending record, this turn's own prompt or tool result, which a revived
  session's first turn passes and a Stop delayed across a bye and a hello still fails. The base
  (D-038) is recorded behind the same fence and dated by the observation. `store.MarkIdle` takes
  `since` and `fenceAt` separately, and a dated mark with no fence is its own fence, so the split
  opens no way around the check.
- **`busy` re-reads before the handoff check.** At the prompt that opens the next turn, the reply
  has usually landed. `busy` records it (beat's order: identity first, then the transcript), then
  answers D-055's question. It is fenced by the incarnation's start, as `idle` is, so a revived
  session is not told its predecessor's size (D-055). In the queued-prompt race it reads the
  previous request, and `RecordContext`'s newer-or-equal-turn rule makes that a no-op.
- **Between turns, a prose session's roster footprint is normally one turn old.** `prompt N`, and
  D-020's `cache 1h hot/cold` clock, come from the observation `busy` made at the START of the turn
  that just ended. It is older when that `busy` lost the queued-prompt race. It always carries its
  own request's time, never a fabricated "now". For the cache clock
  the error is conservative: the real last request is newer than the observed one, so the roster
  can call a warm cache colder, never a cold one warm. A session that runs tools is mostly
  unaffected, because `beat` samples during the turn.

**Considered and cut** —
- *A bounded wait in `idle` for the reply* (the first proposal). Inside the hook's 100 ms budget,
  a wait ending 85 ms after process start caught **1 of 20**. Catching 19 of 20 needs ~110 ms of
  waiting, a ~120 ms hook, on every Stop, to record a number the next prompt records anyway.
- *Waiting in a detached process.* A writer racing the next turn's `beat` and `busy` is a new
  class of bug, for the same number.
- *Reading the Stop payload.* No usage in it, and buddy reads no message text.
- *A freshness threshold on the clock* ("a reply more than N s old is stale"). The file says it
  exactly.

**Residuals** — `sessions.started` is stored in whole seconds, so both fences, `idle`'s and
`busy`'s, admit evidence from earlier in the same second as a revival. A predecessor whose last
reply lands in the second its successor says hello can pass. This predates D-060 (the Codex code
pass), and the `prompt_id` fence closes it for `idle` whenever the named turn is on disk. A user
record the harness might write after a completed reply, with no new turn (a bookkeeping record), would
read as pending. That is the harmless direction, and none appeared in the transcripts measured.

**Latency before/after** — Same lane, same Go timer, one binary per Stop, alternating, 47 Stops.
The installed `idle` (3962ef7): **p50 26.6 ms, p90 29.6, max 32.8 (n=23)**. D-060's (pre-warmed
with one run, since a freshly built binary's first exec measured 244.9 ms once): **26.3 / 27.7 /
28.6 (n=24)**. No measurable change. Every D-060 Stop took the pending path and read `idle 0s`.

**Test shape** — `idlewait_test.go`. The detector, as a table: answered, the Stop's case, a large
tool result waiting, a first turn, and three that must NOT read as pending (a subagent's prompt, a
prompt cut off mid-write, a nested `"type":"user"` inside a reply), plus no user record at all. The
answered turn is the control that pending is not simply always on, and `lastUsage` is pinned to be
`readTurn`'s usage half. `idle` before the reply records no footprint and reads `idle 0s`; the same
transcript with the reply on disk records it and dates the mark by it (control). The fence, per
path: pending, the revived session's own prompt is marked and a prompt from before the revival is
refused; found, its own reply is marked and a reply from before the revival is refused. `busy`
re-reads before the note, at 100k (speaks) and 200k (silent control), and records the roster's
footprint too. `busy` does not tell a revived session its predecessor's size (its own reply after
the revival is the control). A resting prose session shows the last observation with that
observation's age and cache clock, not "now"; a reply on disk is the control. `store`'s `MarkIdle`
tests take the new signature, and pin the split: an earlier `since` with no fence is refused, an
undated mark fenced by earlier evidence is refused, and one fenced by this incarnation's evidence
lands dated by the write. A Stop superseded by a newer prompt records nothing, not even a base.
Its controls, each marked: the newest turn's own Stop, a Stop with no `prompt_id`, and a Stop
naming a turn not on disk. The pending path's base write is asserted. Mutants, each in a private
copy and watched red: pending never set; a first turn not pending; a tie read as landed; a
subagent's prompt counted; a nested `"type":"user"` counted (it first SURVIVED, because the nested
match sat inside the reply, whose own time equals its usage. The fixture now puts it in a later
system record); pending fenced by, dated by, or recording the stale reply; `busy` not re-sampling,
and not fenced; `MarkIdle` ignoring the fence, dating by the fence, or not checking an unfenced
`since`; the superseded check off, never found, or matching any older prompt; the pending base
not written. Equivalent, measured: the found path passing a zero fence, since `MarkIdle` now
fences a dated mark by its own date.

## D-061 — A `-race` forkExec can leave its child stuck before exec; the watchdog ends it

2026-09-27 · issue #53

**What was wrong** — A plain `sh scripts/check.sh` (not a hook) timed out in its `-race` leg:
`internal/cli` took 660 s, with one goroutine in `syscall.forkExec` → `readlen` under a
`git rev-parse`. Afterwards a process with the test binary's argv sat orphaned, at ~96% of a
core, for 44 minutes. D-053's watchdog had exited without sampling or signalling anything.
It was the third forkExec → readlen hang. CLAUDE.md blamed the first two on the pre-push hook
putting CLT's libexec git first on PATH. This one was outside any hook, through `/usr/bin/git`
(a 12-byte exec path in the dump).

**Measured** (go1.26.4, darwin/arm64):
- The orphan had ONE thread. A live Go test binary has many, and a process fresh from `fork` has
  one. A `go test -race -c` build of the exact tree symbolizes its only stack as
  `…revParse` → `os/exec.(*Cmd).Start` → `syscall.forkExec` → **`syscall.forkAndExecInChild`**
  → `rawSyscall` → **`__tsan::TraceSwitchPartImpl`** → `cthread_yield` → `swtch_pri`. It was the
  FORK CHILD, which never exec'd. (A non-race rebuild gave nonsense frames: symbolize against the
  build that ran.)
- Reproduced in a race toy: 8 goroutines forking `/usr/bin/true` while 32 others generate race
  traffic. All 8 forkers hung within 170 s (104,351 forks). The 8 leftovers were orphaned, state
  R, one thread each, with the parent's argv, in `forkAndExecInChild` → `__tsan::SlotLock` → a
  `sched_yield` spin. CONTROL: the same toy without `-race` made 356,378 forks and 0 hung.
- So the child takes ThreadSanitizer's slot lock, which another parent thread held at the
  instant of fork. That thread does not exist in the child, and nothing will ever release the
  lock. The parent waits in `readlen` for an exec that never comes. It happens in `-race` builds
  only, and more often under trace traffic (load). It is not git, PATH, CLT or the hook.
- Why D-053's watchdog did nothing: the child inherited the watchdog pipe's write end, and
  close-on-exec closes it only AT exec. So no EOF came. The parent hit its `-test.timeout`,
  panicked and EXITED normally (the dump printed). The watchdog's orphan arm then read its target
  as gone, and counted nothing. At the 2× wall, `stop` could not identify the target, sent
  nothing and returned. The watchdog that was left, 16 s older than the orphan, was the parent's.

**The rule** — A HOLDER arm, checked before the other two. When the target is gone (no process,
or another start time) and the pipe is still open for `orphanGrace` polls, a child of the target
that never exec'd holds the write end. While the target lives, the watchdog records the
target's children in ITS OWN process group whose exec path is the target's (`groupKids`,
`kern.proc.pgrp` + `kern.proc.procargs2`). A child that exec'd git carries git's path; a
fork-only child carries the parent's. Once the target is gone, it ends those that are still the
process it saw (same start time), are ORPHANED (ppid 1), still carry the target's exec path,
are still in its group, and are not itself (`pickHolders`). It samples each, sends SIGQUIT, then
SIGKILL, re-checking start time, exec path and group before every step. It writes ONE report,
after every process is done. When the wall or orphan arm ends a wedged TARGET, its holders are
ended the same way before that report. Orphanhood is checked once, when the holders are picked.
A re-check before each signal was built and cut: on darwin reparenting only ever goes to launchd,
and a reused pid fails the start time. Its mutant survived every leg. A holder in another process group is never touched. It
cannot be found, and if it keeps the pipe open the watchdog reports and exits.

Why ending a LEGITIMATE same-binary child is safe (a testguard helper the target re-exec'd, not
a stuck fork): it was the target's own child, it is this same test binary, its parent is gone,
and it is still running after the test run that started it ended. A helper that exec'd closed
the pipe at exec, so it is only swept up because something in the group still holds the pipe.
Either way it is a test process that outlived its run, which is the leak this guard exists to
end. A test binary never owns anything a later run needs.

The TARGET opens its executable before it starts the watchdog and hands it over as fd 3, which
the watchdog holds; symbolize reads the pclntab through `/dev/fd/N`. `go test` deletes the binary when it exits, and
an unsymbolized sample names nothing. The target and any child it forked are the same binary.

**What the Codex code pass changed** (five findings, each now a test, watched red against the
first cut):
- A child seen between fork and exec (every git child is, for an instant) execs, or leaves the
  group, before the arm acts. It was still picked. The exec path and group are now re-checked.
- The report was written after EACH holder. A dead stderr (the #53 case) killed the watchdog
  with its first write, before the second holder. There is now one report, last.
- The remembered list was never pruned, so after 64 children had come and gone no new one was
  recorded. Gone entries are now dropped before the cap.
- The wall and orphan arms returned after ending the target, leaving the holder its death
  orphaned. They now end the holders too.
- The watchdog opened the executable itself, racing its deletion. The target now opens it first.
Also from that pass: the target is re-read after enumerating its children, so a pid reused
mid-poll does not lend its children to the list, and the report no longer says each holder
"never exec'd" (a re-exec'd helper can be one).

**Limits, accepted** — A child forked after the last poll before the target died is not seen,
and not ended. A target that could not be identified at start, or whose exec path the kernel
would not give, turns the holder arm off. With no wall, such a watchdog then waits for EOF.
Three failed lookups of a LIVE target read as "gone": nothing is picked (its children are not
orphaned), the watchdog says so and stops guarding it.

**Cut** — Mitigating the ThreadSanitizer bug in this repo. It is upstream, and a retry loop or a
timeout knob hides it (CLAUDE.md). Whether to file it upstream, with the toy as the repro, is the
operator's call. Also cut: listing every process on the box (`kern.proc.all`) to find holders.
It would reach other groups and other runs; the group plus the target's own child list is the
fence.

**Test shape** — `testguard_test.go`, each written to fail first:
- `TestWatchdogEndsTheChildAnExitedTargetLeftHoldingItsPipe`: a target spawns a same-binary
  holder that inherits the pipe, in its own group, and exits normally. Red before the fix (the
  holder was alive 30 s later, the #53 failure). Green: the holder is sampled and gone. The
  negatives beside that control, both untouched: a same-binary holder in ANOTHER group, and an
  orphaned in-group child that exec'd `/bin/sleep`.
- `TestPickHolders`: a table covers the orphaned child (picked), one with a live parent, a
  reused pid, an exited one and the watchdog itself (none picked). On darwin a child always
  reparents to launchd when its parent dies (no subreaper), so a non-orphaned candidate cannot
  be built with real processes. The table is where that guard is exercised.
- `TestWatchdogSymbolizesAfterTheBinaryIsDeleted`: the wedge helper runs from a COPY of the test
  binary, deleted once it arms. Red before the fix (`???` frames). Green: the sample names
  `spinForever`.
Mutants, each pass with an unmutated control run green first. Watched red: the holder arm
dropped; `kern.proc.all` in place of the group; the exec-path filter dropped (the exec'd
`sleep` child was killed); `pickHolders` without its orphan, start-time, self, exec-path or
group rule; the held image unused; the image not handed over by the target; a report written
before the next holder (the second in-group holder survived its dead stderr); the arms not
ending the holders of a target they ended; and `remember` not pruning. `stop`'s per-signal
ORPHAN re-check survived and was cut, as above: it guards a state that cannot occur. Two
guards survive single mutation and are kept, because their windows are real but cannot be
staged deterministically. The per-step exec-path and group re-check covers a child that execs
during the 2 s `sample`: dropped together with the pick-time check, the leg fails, since the
children that exec'd or left the group were killed, and each alone holds the leg. The
target's re-read after enumeration covers a pid reused mid-poll.


**Found upstream (#55, the same day)** — This is golang/go#79804, "runtime: SIGSEGV on child
side of fork pre-exec with -race on darwin/arm64", reported against go1.26.3/1.26.4. From
go1.26.0 darwin's `rawSyscall` is a Go function (CL 699177), so the race detector instruments
it, and `forkAndExecInChild` calls it in the fork child, where the race runtime's thread state
is invalid. The upstream bisect saw about one third SIGSEGVs and two thirds hangs, which are
the orphans measured here. The fix, CL 786620 (`//go:norace` on `rawSyscall`,
`rawSyscall6` and `rawSyscall9`), was backported as golang/go#79806 and released in **Go
1.26.5**. The machine ran go1.26.4. `go.mod` now pins `toolchain go1.26.8`, so under the
default `GOTOOLCHAIN=auto` every build of this repo, CI included (setup-go reads `go.mod`),
uses a fixed release. The race toy that hung 8 of 8 forkers under go1.26.4 was re-run under the
pinned toolchain (see #55). The holder arm stays: it ends ANY same-binary child that never
execs, whatever wedged it. The tsan slot-lock reading above is superseded by the upstream
analysis.
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
- What wedged `internal/cli`'s test binary in #43 is not known. D-053 bounds it and samples the
  next one; it does not explain the first two. Off darwin the watchdog can identify no target,
  so it signals nothing and only the in-process poll bounds an orphan.
- The commit gate covers only the ordinary commit path, and only where the hook is installed.
  `--no-verify` and `commit-tree` skip it, a merge commit runs `pre-merge-commit` instead,
  and the commits that rebase, cherry-pick, revert and `am` create do not run `pre-commit` at
  all. Git will not let a repository configure its own `core.hooksPath` — correctly, since
  that names a directory of programs git executes — so an uninstalled hook is the default
  state of every fresh clone.

## D-062 — Landing through an orchestrator is a recipe in the skill, over existing verbs

2026-09-27 · operator request (the fleet's protocol, graduated; no issue)

**What was wrong** — The orchestration plan of 2026-09-20 proposed a `buddy task` lifecycle
(offered, accepted, reported, closed). The review sent increment 2 back: prove the lifecycle live
before building a table for it. It was not built. Meanwhile one fleet repo wrote the lifecycle
into its OWN skill, as prose, and ran it: a landing protocol under the session holding the
`orchestrator` claim (its record of 2026-09-26), then the orchestrator's own duties (2026-09-27),
after measuring what the first version left unsaid. A docs-only lane sat idle for three hours
with its "landed" message undelivered, and four assignment and red-report messages reached nobody
for about fifty minutes, because the sender discarded `buddy msg`'s result and with it the wake
line. It had landed nine batches by its third day. That protocol was the increment-2 lifecycle,
proven live, and it lived where no other repo's session would read it (D-046: the skill is where
a session learns how the verbs fit). Two of its parts were also weaker than what buddy already
had: READY and LANDED travelled in messages alone, and the measured failure was a LANDED that
never arrived, which is the failure D-049 built `wait --ready` and `release --outcome` to remove,
since the rider's own loop reads the outcome.

The operator's question that opened this (2026-09-27) was where two projects, the fleet repo and
buddy, were going: apart, or together as a "software factory". The answer recorded here: the
product stays where it is, and the protocol is the shareable part, so it is homed in the skill.

**The rule** — A skill section, "Landing through an orchestrator", and a USAGE section of the same
name:
- The orchestrator holds TWO claims. `orchestrator`, durable, its description the published note
  (D-030): how to report, the current run's slug, or the file that says. And the run's claim on
  `.buddy/slot/<run>` and `.buddy/slot/main`, formed per batch and released with its outcome
  (D-049). A lane waits on the RUN, never on `orchestrator`: that claim outlives every run, and
  a wait on it lands only at a handoff (D-056).
- A lane: own worktree at current main; claim with the item's name as the slug; commit, review,
  own checks green; never the shared tier, never main, never a push. READY is
  `wait --on <run> --ready HEAD` in the ledger AND a message with what the ledger does not hold
  (review receipt, files, checks run). Then `/loop buddy wait check` and park. Keep claims and
  worktree until LANDED names it PASS; on FAIL, fix, re-declare, say so.
- The orchestrator owes, every time: an answer to every READY naming its run; every `msg` result
  read and its wake line delivered; the landing reported in the same turn, as the release's
  outcome AND as words that end each landed lane's work; a red routed to the lane it belongs to;
  the new main told to every lane still working; and, before handoff or exit, `who` on every lane
  it assigned. A stale claim with no answer is reported to the operator, never freed.
- Repo-specific parameters (which tier, which review, which checks are a lane's own, worktree
  naming, id spaces, review-slot caps) stay in the repo's own skill or the orchestrator's
  description. Buddy names none of them.
- Buddy keeps no task table. The claim is the responsibility, the `--ready` is the report, the
  outcome is the close, and `who` is the view.

**Considered and cut** —
- *`buddy task` (the plan's increment 2).* Still not built. Every fact it would hold has a row:
  responsibility and scope in the claim, "reported" in `session_waits.ready_sha`, "closed" in
  `claims.outcome`, the brief and the acknowledgement in the inbox, the view in `who`. B1 and B2
  of the review (recovery after a resume; a notice "awaiting retrieval" for 24 hours) would apply
  to a table exactly as they did; the recipe answers them with the wake line (D-052, D-059) and
  the timer wait (D-054) instead.
- *One LANDED that also means "you may exit".* The outcome is one fact about one run; whether a
  lane is done is the orchestrator's word, since a lane may hold follow-ups the run did not carry.
  Two channels, on purpose: the ledger's for the fact, a message for the instruction.
- *Naming the fleet repo.* Not named, as elsewhere in this record.

**Not measured** — A fleet running the recipe with `wait --ready` in place of a message-only
READY. The fleet repo still runs its message-only form; adopting the ledger half is its own
docs-only item, through its orchestrator.

**Test shape** — Prose only. The skill gate reads every `buddy <verb> --flag` in the section
against the usage table (`wait --on --ready --until`, `who`, `msg`, `sent`,
`release --outcome --note`, `sweep --force`); the coverage gate is unchanged, since the section
adds no name.

## D-063 — A holder hands an open claim to another live session, and its waits go with it

2026-09-29 · operator request (an orchestrator handoff, measured; no issue)

**What was wrong** — An orchestrator handing its role to a successor held an in-flight run claim
(`.buddy/slot/<run>` + `.buddy/slot/main`) with two riders parked on it `--ready`. The skill's
handoff recipe covered only `orchestrator`, so the outgoing session worked out "how a run claim is
passed on" for itself: release, and the successor claims again. A new claim is a new claim id,
and a wait is resolved once to a claim id (D-033), so every rider's wait went LANDED with "No
outcome was reported … ask its holder" (the holder that had just left), and each rider's READY sha
went with its wait: `who <run>` showed the successor no riders until both re-declared by hand,
which they did within a minute. One missed check later, the successor would have landed a run
whose riders it could not see. Between the release and the re-claim, `msg <run>` was refused and
main's slot was anybody's.

**The rule** — `buddy release <slug> --to <target>` (`store.TransferClaim`). One immediate
transaction changes the claim's `session_id` and `incarnation` and sets `renewed` to now; the
claim id, scopes, mode, slug, description and `created` stand, so every wait on it keeps waiting,
and the new holder's release (with its outcome) is what lands the riders. It refuses, writing
nothing: a sender not live under its incarnation (release may close a dead incarnation's leftover
claim; a transfer would keep it open under someone else); a recipient that has ended or whose
incarnation changed since it was resolved (`Target.Incarnation`, new); the sender itself and
`all`; a recipient with an open wait on this very claim (it would wait on its own claim, which
only it can land — refused, never repaired, because dropping one target of a multi-target wait
can LAND it); and any scope overlap the recipient could not have claimed itself (a session may
hold overlapping claims of its own, so handing one of two away could leave two sessions on one
path: the ordinary conflict scan runs against the new owner inside the transaction). `--to`
takes no `--outcome`, `--note` or `--scope`: an outcome ends the job the transfer hands on.

The CLI then queues one message to the recipient, signed by the sender, saying what it was handed
and that a re-claim with the SAME scopes refreshes the description; the result line names the
waiters that followed the claim and carries the wake address (D-041, D-052). The notice is a
second write: if it fails, the receipt says the claim moved and the telling failed — never "not
released", which would invite a retry of a move that happened.

**Considered and cut** — Recipient consent (an offer the recipient accepts): the holder already
decides how long the scopes stay reserved, and the skill sequences the move after the successor
answers. A recorded previous holder: no schema for it; the notice names the sender. Clearing the
recipient's self-wait: see above. A skill-only fallback (`release --outcome aborted` with a note
naming the successor): Codex judged "aborted" untrue for a run that carries on, and it still
drops every READY. "Land before you hand off": the trigger is context size, and the round in the
measured case was eight minutes into a gate. An `orphan the ended owners first` step, as `claim`
has: no reachable ledger state gives it anything to do here (a claim cannot already overlap an
ended owner's exclusive claim; `claim` orphaned it when this one was taken), so it was dropped
rather than kept untested.

**The skill** — "Hand off before you are full" step 4 hands every claim over with `--to`, the run
first, and says never to release a run for a successor to claim afresh. "One long run" names it
for an integrator handing a run on mid-flight.

**Test shape** — `store/transfer_test.go`: waits keep waiting and keep READY, the gate follows the
owner, the new holder's refresh keeps the id and its outcome lands the rider, with the positive
control that release-then-claim lands the rider with no outcome; a table of refusals with a live
control, each leaving the claim where it was; ErrNoRelease for a claim not yours.
`cli/release_to_test.go`: the one-line receipt, the recipient's notice, the rider STILL WAITING
then LANDED on the new holder's outcome, and six refusals that leave the claim with its holder
(among them `--to ""`, which must never fall through to a plain release).
Mutated in a private copy: the incarnation fence, the self-wait refusal, the overlap scan, the
sender's liveness check, `renewed=now` and the empty-`--to` guard each killed by a test. Codex's
code pass found the receipt's "told them" overstated a queued send (D-032): it reads "notice
queued".

## D-064 — The handoff size may be declared fleet-wide, so its line speaks to lanes too, and `install.sh` says whether it is set

2026-09-29 · operator request (follows D-063)

**What was wrong** — The orchestrator handoff measured for D-063 began, again, with the operator
noticing the size ("context here is getting a bit large"), as 3 of the 4 measured for D-055 had.
`BUDDY_HANDOFF_AT` is declared per process, and the recipe had the outgoing orchestrator pass it
to its successor with `herdr tab create … --env`; nothing put it on the FIRST orchestrator, which
the operator launches, and nothing said whether it was set anywhere. The same fleet had lanes at
944k and 636k that nobody was told about.

**The rule** —
- Measured 2026-09-29: an entry in a settings file's `env` block is in the environment of that
  session's `UserPromptSubmit` hook (a headless `claude -p --settings` whose hook wrote
  `${VAR:-UNSET}` to a file wrote the settings value, with the variable absent from the parent
  shell). So `"env": {"BUDDY_HANDOFF_AT": "400k"}` in `~/.claude/settings.json` reaches every
  session, the first orchestrator included. USAGE says so.
- `install.sh` reports it in one line after the hook report: the size set there, or not set
  (with the line to add), or set while `buddy busy` is not wired, in which case no session is
  ever told. Reported only; it never edits settings and never chooses a size.
- Declared fleet-wide, the line reaches lanes too, so it no longer says "start your successor":
  it says "finish the round and hand off" and names both arms of the skill — a coordinator's
  "Hand off before you are full", and a lane's "Landing through an orchestrator" step 6, which
  finishes its item, reports to its orchestrator, and takes nothing new (a lane opens no
  successor, D-056). The orchestrator's sweep also reports lanes past the size to the operator
  (the skill, 3a0a450).

**Considered and cut** — A separate lane size variable (`BUDDY_LANE_HANDOFF_AT`): nothing tells a
hook which role its session plays (D-015), and one line naming both arms costs less than a
second declaration. A JSON parser in `install.sh`: a grep for the key as a JSON key is enough for
a report, and a false "set" would only say so. Setting a default size: the operator's call.

**Test shape** — `check-install.sh` QA-1 (not set, with busy unwired), QA-3b (set and named;
the wired file does not warn; the same file without busy says nobody is told, the fixture checked
to have lost busy; the busy-not-wired arm mutated in a private copy and killed by QA-3b).
`handoff_test.go` asserts the two-armed pointer.

## D-065 — `status` and `who` count the shells still running under a session's harness process

2026-09-29 · operator request (follows D-063, D-064)

**What was wrong** — An outgoing orchestrator told its operator "this session holds nothing and
the checkout is clean, so you can close it" while two of its own shells still ran as children of
its claude process: a gate script eight minutes in, writing the summary its successor had been
told to poll, and a 42-minute watcher on a remote nightly whose only output was an echo to the
session about to close (measured 2026-09-29, `ps` by parent pid). `status`'s EXIT line was right:
it speaks for the ledger, and says so. It was read as speaking for the session. The skill now has
the handoff account for its running jobs (3a0a450); nothing could show one that was forgotten.

**The rule** — `status` and `who` print a JOBS line before EXIT, for a live session:
- the shells that are DIRECT children of the session's registered harness process (D-025's
  `session_procs`, alive under its recorded start time), with pids and ages, oldest first;
- or "no shell found directly under … when sampled, besides any running this report", naming
  what it cannot see (a job that exec'd into another program, or runs under a wrapper);
- or "cannot say": no live registered process; this platform's table is not read (only darwin
  reads it); or the table's row for the anchor does not carry the registered start time.

Measured on this machine before writing it: a live claude's children were one `/bin/zsh` (the
Bash tool call running `ps`, in its OWN process group), `npm exec` (an MCP server), `buddylist`,
`gopls` and `caffeinate`. So the line reads the whole table (`kern.proc.all`, one sysctl) and
filters by parent pid: D-061's process-group walk cannot see a child in another group, and D-061
cut the whole-table scan for its own purpose (ending its test binaries), not this one. It counts
shells by the kernel's executable name, which excludes the MCP servers and the rest. The report's
own shell is a Bash tool call too, so the caller's ancestor chain (walked in the same table,
bounded like the anchor walk, cycle-safe) is excluded. A hook or status line the harness is
running when the table is sampled is counted, and the line says "when sampled", nothing more.

**What it prints and does not** — Pids and ages, never argv: a command line is whatever the
session typed, may carry a secret, and would be peer text in another session's context. It refuses,
ends, signals and grants nothing (invariant 10, D-027); nothing ends a session's processes from
buddy (D-056). "Cannot say" is never rendered as "none" (D-016's rule).

**Codex code pass** — CONFIRMED: the anchor was checked alive by `procAlive` and the table read
separately, so a pid reused between the two would lend the session another process's shells. The
table's own row for the anchor must now carry the registered start time (test: a table whose pid
500 was born 2, not the registered 1, prints "cannot say"). The empty arm's first wording ("no
shell running … at this instant") overclaimed a complete inventory; it now names what it cannot
see.

**Considered and cut** — Reporting from `bye` (the pane is already closing, and bye stays silent).
Descendants beyond direct children (a background shell's own children are its business; the shell
is the job). Printing command lines. Linux (`/proc` walk): not measured here, so it says cannot say.

**Test shape** — `jobs_test.go`: the pure filter over the measured tree (the caller's chain
excluded, with the control that counts it without the exclusion), a cycle in the self walk, and
the CLI arms (two shells with ages, none, an unreadable table, an unbound session, a dead anchor,
a reused anchor pid). `jobs_darwin_test.go`: the real sysctl finds this test's own child
`/bin/sh -c "sleep 30; true"` under `os.Getpid()` (two commands, so sh cannot exec sleep in its
place). Run live on this session: no background job → the empty arm; `sleep 90` backgrounded →
"1 shell(s) … pid 81311 0s". Mutated in a private copy: the self-chain skip, the shell filter,
the direct-child rule, the anchor liveness check, the platform arm and the anchor re-identification
each killed by a test.

## D-066 — `claim` refuses a `--scope` that holds a comma, and every listing renders a claim's scopes through one function

2026-10-03 · issue #56

**What was wrong** — `buddy claim fix --scope pkg/a.go,pkg/b.go,pkg/c.go` was granted as ONE scope,
the literal path `pkg/a.go,pkg/b.go,pkg/c.go`. It exited 0 and printed `scopes: pkg/a.go,pkg/b.go,pkg/c.go`.
Containment is exact (invariant 14), so the claim covered one file nobody has and none of the three
its holder believed it held. `whose pkg/a.go` said CLAIMED BY (none). A peer's exclusive claim on
`pkg/a.go` was granted over it. Under a peer's SHARED claim, the gate denied the holder's own edit of
`pkg/a.go`. `ls` joined scopes with a bare `,`, so the literal and a real three-scope claim printed
the same bytes. Every other listing fenced the joined list as one LINE, which keeps spaces, so a
scope with `, ` in it printed the same as two scopes. Measured on one fleet's ledger (474 claims,
99 sessions): 78 claims by 30 sessions had a comma scope. Each was a claim whose ONLY scope was a
comma list of 2 to 29 parts, and 6 were still open. That repo and this one have 0 tracked paths
with a comma. This ledger: 0 of 389 scope rows contain a comma or a space. The defect showed only
when the shared-claim case denied an edit. Every other one of those claims reserved nothing, and
nothing said so.

**The rule** —
- `cmdClaim` refuses any `--scope` holding an ASCII comma, whole, and writes nothing. The check
  runs after flag parsing, BEFORE the ledger is opened, and before the `--dry-run` branch, so the
  forecast refuses in the claim's own words. It applies to every spelling (`--scope=`, `-scope`),
  `--shared`, and a refresh, which leaves the claim it would have refreshed as it was. The message
  names at most three of the offending values, each fenced to 128 bytes. It prints
  `Repeat the flag: --scope a --scope b` only when that command, pasted into sh or zsh, claims
  exactly the paths meant: no part the fence alters (onFlags' rule), no part `NormalizeScope`
  refuses (that refusal is named instead), and not cut short (D-047's rule; over 4 KiB it prints
  the rule and no command). Each part is trimmed. `shellQuote` now quotes a word beginning with
  `=`, because zsh's EQUALS option expands a bare `=sh` to `/bin/sh`. The same fix applies to
  `wait --on` suggestions.
- `scopeList` renders every listing of a claim's scopes. Each scope goes through `fence.Field`
  (one token, a space shown as `␣`), joined by `, `, whole items within 512 bytes, with
  `...and N more not shown` for the rest. The FIRST item always shows, cut by its own fence if it
  must be. Its users: the claim result line, the dry run's `would claim`, the hello digest, both
  arms of the gate's deny, `release --scope`'s result lines and its not-held refusal (re-rendered
  in the CLI, since the store joins with `, `), `ls`, `whose`, `who`/`status` and the commit gate,
  plus both arms of the SLOT note.

**Why refuse, and why in the CLI** — Splitting was cut. A comma is legal in a path, so splitting
would turn a caller who meant the literal into N wrong scopes without saying so. It would also
give one flag a second syntax for help, the skill and their gates to teach. Warning was cut too:
the claim would still exit 0 and protect nothing, which is the measured failure, and D-031 already
refuses input a verb does not understand rather than act on it as something else. The cost is a
path holding a comma, measured at zero. A prefix scope on its directory still covers one. The one
exception is a ROOT-level file, because `.` cannot be claimed (Codex). If a real comma path is
ever measured, an exact-literal escape can be added then. The check is not in `NormalizeScope` or
the store: the ambiguity is the flag's (the store takes a list already), and `NormalizeScope` also
serves `release --scope`, which must still narrow and release a legacy comma row by name.
`release --scope a,b` against a claim on `a` and `b` was already refused, naming what is held.

**`, ` and not a single space** — The issue proposed joining with a single space. With Field, both
joins are unambiguous, since no rendered scope contains a space. `, ` keeps the bytes of the five
listings that already used it for every ordinary claim. Its cost is a reader that splits on
whitespace alone: it gets `a,` with the separator's comma attached. Codex and Fable would both ship
`, `. Whole items with the first always shown: `who` already rendered whole items (D-027), but
applied to the first item that rule printed a claim whose one scope exceeds 512 bytes as
`scopes: ...and 1 more not shown`, and the gate's deny as `inside scope "...and 1 more not shown"`.
Field can also outgrow its cap, because each space becomes a 3-byte `␣` after the cut.

**Reviews** — Codex (design and code, one pass). CONFIRMED: zsh EQUALS on a bare `=word`; the
singular SLOT note and the release refusal still outside the rule; a single over-long scope
rendered as a bare count; the dry run and a refused refresh untested. REFUTED: "the 8192-byte
fallback is unreachable". `fence.Line` appends its marker after the cut, so a truncated result is
always longer than its cap; the fallback was replaced in any case. Fable (an adversarial run of
about 45 inputs against the built binary, and a legacy ledger made by the HEAD binary). It
confirmed every printed fix pastes back as meant, including `$(…)`, an apostrophe and `-x`, that
the dry run refuses byte-identically, and that legacy rows render distinguishably in every
listing. Also CONFIRMED: a 10 KiB comma argument crowded the guidance out of the message (now each
value is fenced to 128 bytes, at most three named). Noted: a claim with more than 512 bytes of
scopes costs a few bytes more in the hello digest (`, ...and N more not shown` against
`…[truncated]`), which can show one claim fewer at the margin. The budget itself is never exceeded
(swept to 8,965 bytes). The second Codex pass, on the revised code, found no production defect. It
CONFIRMED a blind spot in the renderer test: the gate's deny, `whose` and the commit gate also print
the PATH. The literal's path is byte-identical to the pair's rendering, so a list that Field
fenced as one token passed. Now each listing is asserted on its own output, with the list matched
in its listing's own context. It also noted that the long-scope test did not pin the 512-byte cap
per item (now asserted).

**`check-fence`** — The gate counts `.Scopes` in a `fmt` statement against its fence calls, so
`scopeList(c.Scopes)` failed it. `who` had passed only because its `joinCapped(c.Scopes, …)` sat in
an assignment outside the statement. Hoisting the other sites the same way would have made the
gate blind to them. Instead the gate now counts `scopeList(` as a fence call, and a third clause
fails if `scopeList`'s body stops calling `fence.Field`, with a planted copy it must flag first.
Both clauses mutated: an unfenced `scopeList` and a raw `strings.Join(c.Scopes, ", ")` each turn
it red.

**Considered and cut** — Refusing Unicode comma look-alikes (U+FF0C, U+201A, U+060C, U+3001): they
are legal in paths and were not measured as a mistaken list. Refusing a quoted scope with spaces:
measured at 0, and a space is a far commoner path character than a comma. The `␣` on the result
line is the holder's signal. Printing the NORMALIZED scopes on the claim result line (it echoes
argv: `pkg/` against a stored `pkg`): an older difference, and not this defect.

**Test shape** — `comma_scope_test.go`: the issue's remedy-agnostic invariant test (a claim that
exits 0 covers every path and refuses a peer, or the refusal names the repeated form and writes no
row) and its `ls` test. Refusal on every spelling, with a refused refresh leaving the `ls` row
byte-identical, and a positive control that the comma-free refresh applies. Refusal with no
ledger, with the control that a comma-free claim fails on the ledger instead. The dry run's
stderr is byte-identical to the claim's. Each printed fix is run through a real `sh` and `zsh -f`,
its argv compared with the paths meant, then claimed and each path checked with `whose`. The
unpastable, invalid, comma-only, too-long and four-scope cases print no command, or name the rest.
A renderer table (ls, whose, who, status, hello, both gate arms, commit gate, release refusal)
holding a legacy `pkg/c.go, pkg/d.go` row and a real pair must show `pkg/c.go,␣pkg/d.go` AND
`pkg/c.go, pkg/d.go`. The pair is the per-renderer control, and it also catches Field applied to
the whole join. The caller's own listings use a space-holding scope instead (claim, dry run,
narrowing, both SLOT arms). A 608-byte scope shows cut, then the rest counted. A legacy row narrows
exactly and releases by name. `TestCommitGateStagedPathCannotForgeAReportRow` now writes its
comma-holding root-level claim through the store, and first asserts that the CLI refuses it.
Mutated in a private copy: 31 mutants, each killed by a test (the check removed, moved after the
ledger, or moved past the dry run; each renderer reverted one at a time; Field over the whole
join, overall and in each of the five listings that also print the path; the first-item rule;
the per-item cap; shellQuote's `=`; each guard on the fix line; the trim).

## D-067 — `install.sh` waits out the launch constraint a new daemon build meets, and a daemon failure no longer skips the steps after it

2026-10-03 · operator request, after D-066's install

**What was wrong** — The install that landed D-066 died at step 4: `the daemon did not come back
(state: spawn scheduled)`. Steps 5 and 6 (setup-clone, the hook report) never ran. At the time I
put it down to a code-signature kill of the running daemon. The log says otherwise. The old daemon
stopped at the kickstart. The SPAWN of the new build was refused: `xpcproxy exited due to
OS_REASON_CODESIGNING | Launch Constraint Violation (Constraint not matched)`, and launchd marked
the service inactive. Ten seconds later `backgroundtaskmanagementd` logged `invalidateLaunchItem`
for the agent's plist, and the next spawn ran. The job carries `managed LWCR | has LWCR`. The
binary is ad-hoc signed (identifier a.out, no team), so its identity is its code hash, and Go
stamps the commit into every build, so every install after a commit is a new binary. The wait
was ten one-second polls, the recovery took 10.016 s, and the wait lost.

**Measured** — Refused spawn to re-registration: 10.016 s and 10.015 s (two samples), kickstart to
`running` 10.15 s. The control: `kickstart -k` with the binary unchanged ran in 0.14 s, with no
violation logged. Codex's discriminator: a new build (`-buildvcs=false`, a new code hash) left
15 s before the kickstart was refused the same way, with nothing logged during the pause. So the
delay follows the spawn, not the file change, and cannot be waited out in advance. What the
constraint records was not read. Only this was observed: a changed binary fails it and an
unchanged one does not.

**The rule** — Step 4 polls up to `BUDDY_DAEMON_WAIT` seconds (default 30; a whole number of at
most four digits). A return after the first poll says how long it took and the exit reason
launchd RECORDED, worded as a record rather than as a finding about the run. A daemon that does
not come back, or a kickstart that fails, is DEFERRED: steps 5 and 6 still run, then the install
fails with that reason as its last line. `BUDDY_PLIST` and `BUDDY_LAUNCHCTL` let the done-check
drive the step with a fake.

**Considered and cut** (ranked with Codex) — Skipping the restart when the bytes are unchanged:
the commit stamp changes them on every commit, so it saves nothing. `-buildvcs=false` for the
daemon: it gives up provenance, and a real code change still changes the hash. `bootout` plus
`bootstrap`: not shown to avoid the constraint, and a failed bootstrap leaves the agent unloaded,
which is worse than a slow return. A second kickstart once the item is re-registered: launchd
spawned it without one, and an unconditional `-k` can kill a daemon that has just come back.

**Test shape** — `check-install.sh` QA-6 drives step 4 with a fake launchctl that reports `spawn
scheduled` and `OS_REASON_CODESIGNING` until a given poll. Neither real launchd nor the real plist
can be reached: HOME is a throwaway, so the default plist path does not exist, and a guard
launchctl first on PATH records being reached. QA-6a: running at once gives the one-line report,
and on macOS the fixture's own daemon program was rebuilt. QA-6b: running after 11 polls, past
the old window (Codex: a 3 s case passed with the old default), succeeds and says `after 11s` and
the reason. QA-6c: never running within 2 s fails LAST, after setup-clone's arm and the hook
report, with no `done`. A non-numeric or over-long wait is refused. QA-6d: a failed kickstart is
deferred the same way. Eleven mutants in a private copy were each killed: the wait ignored, the
default set back to 10, no deferral on either arm, no final failure, the reason unread, either
knob ignored, no validation, no bound, and a silent slow return. The real daemon kept its pid
throughout.

## D-068 — Codex participation starts with path enforcement and native identity; driver and worker roles are explicit

2026-10-04 · continuation of the accepted `CODEX-ORCHESTRATOR-PLAN.md` work order

**What was wrong** — The gate treated Codex `apply_patch` as a pathless tool,
checking pause but none of the patch's writes. Ordinary CLI identity accepted a
Buddy override, while `wait check` required Claude's environment. Repository
guidance also applied constrained-review restrictions to every Codex role.

**The bounded change** — `AGENTS.md` distinguishes authorized implementation
drivers/workers from read-only constrained reviewers. The latter still use
`codex-review.sh`; a verdict remains separate from executed test evidence.
The gate parses the local patch envelope before adjudication, checks additions,
deletions, updates and both ends of moves, and emits one denial for a conflicting
call. Target-ledger discovery still distinguishes absent from unreadable, even
when the home directory has no ledger. Unknown or malformed patch routing is
refused. No shell-side-effect containment is claimed.

`CODEX_THREAD_ID` supplies native shell identity; ordinary explicit overrides
retain precedence, while `wait check` requires the caller's native identity
and refuses a conflicting override. Hook `session_id` and native identity must
agree when both are present. The environment variable was observed locally
with installed `codex-cli 0.160.0`; it is not a promise about every deployment.
Both `CLAUDE.md` and `AGENTS.md` are always watched, in addition to the eight
configured authority paths. No schema change is needed.

**Lifecycle boundary** — Codex hooks must not borrow a Claude ancestor process
or equate a shared app-server process with one thread. Marked Codex hooks stay
unbound, and unfenced Codex idle/end hooks are refused. The future adapter must
carry the logical incarnation from event production; reading the latest
incarnation on receipt would let a delayed event act on a successor. Automatic
cleanup, idle delivery and independently claiming subagents remain unqualified.
This increment does not adopt a task table, control adapter or structured
delivery/approval schema. The latter must precede the mixed-fleet exercise.

**Evidence** — Regression fixtures first failed against the old behavior for
patch conflicts, native Codex identity and AGENTS change notices. The current
validation receipt and remaining live gates are recorded in
`CODEX-ORCHESTRATOR-PLAN.md`. Hermetic fixtures are not installed-hook or
mixed-fleet qualification. Installation and hook trust remain explicit opt-ins.

## D-069 — Codex lifecycle events carry a captured logical incarnation

2026-10-04. Continued implementation of the accepted Codex orchestrator work order.

**The failure** — a thread id survives resume, while an app-server process can
host several threads. Resolving the current incarnation when an old event arrives
lets that event refresh, mark idle, or end the replacement. Merely fencing `bye`
would leave `hello`, `beat` and `busy` as routes around the guard.

**The bounded change** — `buddy lifecycle begin` returns a `buddy.lifecycle.v1`
JSON receipt with the native session id and a newly minted incarnation. A live
registration refuses begin; restarting an ended registration requires its exact
previous token. Every refresh, beat, busy, idle or end supplies the captured
token, checked in the same SQLite transaction as its effects. A repeated end
for the same incarnation is idempotent; stale or absent tokens refuse. End does
not orphan claims inline. No schema change or task table is needed.

`lifecycle hook --incarnation` maps native top-level Codex events to those
operations. Stop records idle; SessionEnd records end. It emits `{}` on success,
never a continuation request. Unfenced Codex `hello`, `beat` and `busy` hooks now
refuse alongside `idle` and `bye`, including when `hello --session` supplies an
explicit id. Hook input is classified before the manual-registration path; a
conflicting explicit id or malformed payload also refuses before writing. The
no-input manual CLI registration and operator cleanup paths remain explicit
local acts. The PreToolUse gate remains separate.

**Boundaries** — the adapter owns capture, durable receipt storage, per-thread
binding and serialized callback delivery. It must never replace an old event's
token with the current one. A committed begin whose receipt was lost is an
uncertain outcome requiring reconciliation, not an automatic retry. This token
does not order turns within one live incarnation. Lifecycle observation does not
drain inboxes, launch or wake workers, establish approvals, or prove completion.
The minimum delivery/approval and harness-control interfaces remain Step 2.

**Evidence** — store and CLI regressions include predecessor CAS, delayed
callbacks, positive controls, concurrent restarts, rollback, preserved claims
and queued inboxes. Removing token checks or recapturing the current token made
the regressions fail. Independent source review found that `hello --session`
skipped hook classification, letting a delayed callback refresh a live successor
or revive an ended one and orphan its claims. That finding was accepted and
fixed. All six marker/state regressions failed before the fix; manual, Claude,
identity-mismatch and malformed-input controls pass afterward. The current
validation and isolated native-hook receipts
are in `CODEX-ORCHESTRATOR-PLAN.md`; they are distinct from production adapter,
provider and mixed-fleet qualification.

## D-070 — Versioned coordination receipts and separate harness adapters control only workers they launch

2026-10-04. Step 2 of the accepted Codex orchestrator work order.

**Consumer and ownership** — the concrete consumer is a native control adapter,
so the previously cut generic JSON roster is replaced by a bounded
`buddy.adapter.v1` interface. Buddy keeps claims, messages, waits, READY and run
outcomes. A separate `buddy-adapter` binary owns one launched worker's native
transport. No task database, dashboard, attach-to-existing-session, scheduler
or peer-derived operator authority is introduced.

**Identity and delivery** — adapters durably capture the lifecycle begin JSON
before sending input; callbacks carry that same incarnation. Schema 15 adds
sender/recipient incarnation metadata beside existing inbox rows and receiving
Claude endpoint observations bound to a verified process birth. Legacy drains
exclude adapter mail. Fetch and explicit recipient-reported delivery are
separate; native wake acceptance, acknowledgment, results and completion never
stand in for one another. Unsupported, unknown and observed absence remain
different. Native IDs, JSON approval IDs (including integers above 2^53) and
result references retain their values.

**Control** — `buddy.control.v1` supports start, send a queued-message wake,
inspect and interrupt. Codex resume uses the saved native ID and captured live
incarnation; Claude resume is unsupported. Neither transport loss nor a model
turn ends the logical session. The local receipt journal serializes ownership
and records send intent before touching native input, so an uncertain wake is
not blindly replayed. A lost registration receipt needs reconciliation.
Approvals use a separate inherited operator descriptor, disabled by default;
coordinator stdin cannot answer one. Request-scoped allow/deny never grants a
session-wide permission policy. Native resolution remains distinct from a
response being written to the pipe.
*Corrected 2026-10-07 (review F-41, audit A-11), the way GIVEN 71 was:* startup
is argv, not a control request; the coordinator's requests on the control
stream are `inspect`, `reconcile`, `send` and `interrupt`, and approval is the
separate operator-descriptor path. `reconcile` is since WITHDRAWN (D-075,
D-077): the request is still accepted, and answered as unsupported for a wake
with a recorded native turn, or as `unknown` from the transport journal for one
without — never with a native history read.
*Superseded by D-075:* "Claude resume is unsupported" above no longer holds.
Claude resume is admitted, experimentally, when every journalled turn reached a
terminal result for its exact input and the worker went idle; see D-075 for the
exact conditions. The sentence stands here as history.

**Compatibility** — Codex checks the installed version and effective native
hook metadata for the exact trusted synchronous gate command/hash. Claude
checks its installed version and an explicit gate-only configuration against
the inspected SDK streaming contract. Mods that can override hook denial must
be qualified under effective policy; D-071 replaces the original supplied
absence assertion with an exact built-in profile. Unknown refuses launch. No hook
trust or global permission configuration is changed by the adapter. The
receiving Claude environment exports its endpoint; a sender environment and
the historic `/tmp/cc-socks` convention do not identify an adapter recipient.
Endpoint observations do not establish liveness, native delivery or permission.

**Evidence** — see the canonical plan's Step 2 receipt for commands, source
reviews, native local fixtures and outstanding qualification. Fixtures run on
disposable ledgers; this schema change has not migrated the machine's working
ledger or installed binaries. Source review is not test or provider evidence.

## D-071 — Claude admission checks a native built-in Mod profile, not an absence assertion

2026-10-04. Step 3 continuation of the accepted Codex orchestrator work order.

**The finding** — the installed Claude 2.1.289 reports built-in plugins even
with an empty optional plugin map. `cc-plugin-agents-md` contains a Mod;
`cc-plugin-plugin-authoring` provides authoring guidance. The earlier caller
field `no_mods: observed_absent` did not describe the effective native profile.
Mods can override a non-managed PreToolUse denial, so an assertion about empty
configuration cannot qualify the gate.

**Admission** — Claude requires `hooks.mods: "builtin-only-2.1.289"` and rejects
the legacy absence assertion. After native initialization and the existing
effective-settings check, the adapter reloads plugins in its launched child,
requires exactly those two native built-ins with their expected source/path
identities and no reload errors, then checks effective settings again before
capturing the lifecycle identity or accepting input. Unknown, incomplete or
additional inventory refuses; caller labels do not substitute for native data.
The profile stays pinned to the checked native version.

**Boundary** — this is a startup snapshot with a native reload, not a
migration-free/read-only diagnostic, continuous attestation or a claim that no
Mods exist. Native initialization/reload can execute Mod startup code before
inventory admission; this is not plugin isolation. The actual claim-denied edit
and allowed control are separate native
checks under that profile. Optional or managed Mods outside the profile, native
hot changes, real-provider workers and mixed-fleet/recovery behavior are not
qualified by this observation. No global policy, hook trust or installed plugin
configuration is changed.

**Evidence** — the canonical Step 3 continuation receipt in
`CODEX-ORCHESTRATOR-PLAN.md` names the native inventory probe, adapter exercise
and executed regression checks separately. The
[vendor Mod permission contract](https://code.claude.com/docs/en/plugins/mods/overview#decide-whether-to-trust-a-mod)
explains why hook configuration alone is insufficient.

### 2026-10-05 version continuation: 2.1.290

2.1.290 retains the same measured built-in
inventory and effective gate behavior. Admit it only with its own
`builtin-only-2.1.290` profile, while retaining the exact 2.1.289 pair. A launch
must match the executable's exact version, including on resume; neither
cross-profile declarations nor automatic conversation upgrades are supported.
Native gate/approval, Mod, completed-turn resume and cancellation/crash-refusal
fixtures passed for 2.1.290. The permissive-Mod control still overrides a user
hook denial, so the inventory restriction remains necessary. Offline diagnostics
recognize both exact versions without promoting captures to live readiness.
See the Step 4 2.1.290 receipt for counts, hashes and unqualified boundaries.

### 2026-10-06 version continuation: 2.1.291

2.1.291 separately passed native effective
settings, built-in inventory, claim-denial/control, completed-turn resume and
cancellation/crash-refusal qualification. Add only its exact
`2.1.291` / `builtin-only-2.1.291` pair. Every start/resume still checks the
captured version against the executable, and every cross-version/profile pair
refuses. The native permissive-Mod contrast still overrides the user hook;
optional Mods remain outside admission. Offline diagnostics recognize the new
contract without claiming current trust or enforcement. Manual compaction and
its refusal cases remain qualified only on retained 2.1.290; new regressions
prevent ordinary 2.1.291 admission from widening those separate gates. See the
Step 4 2.1.291 receipt for exact evidence and remaining boundaries.
*Superseded in part by D-075 (marker added 2026-10-07, audit A-11):* the
"only on retained 2.1.290" limit on manual compaction no longer holds. A later
2.1.291 compaction receipt measured its boundary and local-command result, and
`claudeManualCompactionQualified` admits both 2.1.290 and 2.1.291; D-075
records the widening. The sentence stands here as history.
*Superseded in part by D-079 (marker added 2026-10-13):* admitted pairs are no
longer "the code's table". `claudeModProfileForVersion` and
`claudeManualCompactionQualified` are gone. A build is admitted only by a
committed exact-artifact record (sha256, exact version text, Mod profile,
built-in list, tiers), and 2.1.289 and 2.1.290 are retired: no binary or hash
survives, so admission refuses them. The exact-version rule and the built-in
profile check above stand; the version lists above are history.

## D-072 — Offline diagnostics inspect a private ledger snapshot and never promote captures to readiness

2026-10-05. Step 4 continuation of the accepted Codex orchestrator work order.

`buddy diagnose` is the concrete need for the migration-free read path deferred
by D-037. `adapter inspect` retains ordinary store semantics; the diagnostic
never calls it or `store.Open`. Even SQLite `mode=ro` can create WAL support
files, so source files are read using plain read-only descriptors. A bounded
stable main image with no nonempty WAL/rollback journal is copied privately;
only that copy is opened by SQLite with `mode=ro&immutable=1`. Active journals,
changing inputs and unreadable files remain uncertainty. Nothing checkpoints or
repairs the source. Missing and unreadable ledgers remain distinct.

`buddy.diagnostic.v1` reports schema version compatibility and compares a
supplied captured receipt to the session snapshot without returning or repairing
tokens. Adapter capabilities come from the same implementation tables as control
receipts. Current installed version, effective policy, trust, Mods and enforcement
are not inferred from supplied launch flags. Optional bounded native captures
are tied to harness/worktree/configuration hash and labeled `supplied_snapshot`;
they are historical claims, not authenticated live observations. The diagnostic
starts no native executable, even for version detection, and cannot run hooks,
reload plugins, register sessions, drain mail or modify permissions.

Exit 0 means report produced, and readiness always remains `not_established`.
This closes the bounded offline report slice; fresh native compatibility,
recovery/resumption and real-provider qualification remain separate gates.

References: [SQLite read-only WAL behavior](https://sqlite.org/wal.html#read_only_databases),
[SQLite immutable URI semantics](https://sqlite.org/uri.html#uriimmutable),
and [Codex hook trust](https://learn.chatgpt.com/docs/hooks#review-and-trust-hooks).

### 2026-10-12 continuation: a hand-wired Codex hook layer is checked for its marker (F-31)

A Codex hook that runs buddy without `BUDDY_HARNESS=codex` is classified as a
Claude hook, unless the hook process happens to inherit `CODEX_THREAD_ID`
(observed in a tool shell, not documented for hooks). A legacy lifecycle verb
so classified changes a logical session without its captured token (D-069). The
docs required the marker; nothing checked a layer for it, and the adapter's own
capture check covers only its own gate command.

`buddy diagnose --codex-hooks <absolute hooks.json>` reads that one file under
the same rules as every diagnostic input (absolute, regular, at most 1 MiB, no
final symlink), except that unknown fields are allowed: it is Codex's file, not
Buddy's schema. Each hook command is parsed with the gate's shell parser (parse
only). A simple command whose fixed-literal command word is `buddy`, optionally
after `env` and `NAME=value` words, is listed by event with its verb and marker.
A command word that is not a fixed literal is `unrecognized`, never guessed; a
wrapper script or `sh -c` is not inspected, and the report says so. The verdict
is `unmarked` if any buddy command lacks the Codex marker (or names another, or
one that is not a fixed literal), else `unrecognized`, `marked`, or
`no_buddy_command_recognized`. A marked lifecycle verb is listed as refused by
design. It describes the file, not what Codex loaded, trusted or ran, and it is
refused with `--harness claude`. Tests: nine layers plus three refusals; 11
mutants in a private copy, each killed (two respelled after their first form did
not compile).

## D-073 — The gate checks every spelling a write can land on; jurisdiction decides before identity; a nested repo adds to the home ledger, never replaces it

2026-10-06. Review of the Codex orchestrator implementation
(`CODEX-ORCHESTRATOR-FINDINGS.md` F-01, F-03, F-04, F-05, F-06, F-28; Codex's
`CODEX-ORCHESTRATOR-RESPONSE.md` accepted each and refined the remedies).
Every item below was a failing gate test against the tree before its fix.

**Shell-routed patches (F-01).** Codex intercepts `apply_patch <<'EOF'`
(also `applypatch`, and `cd <dir> && apply_patch`) inside its shell tool and
applies the patch in-process with the tool's own engine, while the hook says
`tool_name: "Bash"`. The gate saw a pause-only shell call: a peer-held delete
passed in all three spellings while the same patch through the tool was denied.
The gate now refuses `apply_patch`/`applypatch` in shell COMMAND position
wherever a ledger governs the call, and says to use the tool. It does not parse
and adjudicate such a patch, because exec_command's workdir is not in the
payload and a misplaced path is a wrong verdict. Mentions of the word in
argument position (`grep apply_patch`) pass. From a cwd with no ledger, the
refusal applies only when the `cd` directory or a path in a parseable embedded
patch lies in a governed repo. Cooperative, like the rest of the gate: an
obfuscated spelling, a nested `bash -c`, or a workdir passed outside the
command still reaches the engine unseen.

**Jurisdiction before identity (F-04).** `readHook` used to refuse a
conflicting identity before discovery, so a Claude session that inherited a
Codex parent's `CODEX_THREAD_ID` had every Write denied in every directory on
the machine. That is the D-044 fleet-wide-deny class, and it breaks invariant 3.
The gate now decodes the hook, classifies identity, and refuses a conflict only
where a home or TARGET ledger governs the call. An unreadable governing ledger
still denies. Read-only tools pass, as they already do on an unreadable ledger.
Malformed patch input still refuses everywhere (D-068); that is structure, not
identity.

**Every spelling (F-05, F-06).** The gate checked one spelling,
`EvalSymlinks(path)`. Codex resolves `..` lexically, so
`<repo>/docs/link/../core.go` deletes `docs/core.go` while the gate checked
`src/core.go`. The kernel resolves `..` physically. Codex's Delete File, and a
move's source, unlink the link ENTRY, while the gate checked only where the
link points. `gateTargets` now yields the lexical and physical spellings, each
with the final link followed and as the entry itself, plus a dangling link's
would-be target. Any one held refuses. That is at most five per path and
usually one, so the 128-path bound still bounds the work. This is checked for
every operation, rather than threading operation metadata out of the parser:
the deliberately conservative policy Codex's response allowed. The `..` case
predates the Codex work for an absolute Claude `Edit`; which spelling Claude's
tools act on was not measured, and both are checked.

**Nested repositories (F-03).** The Codex work routed a target inside a
nested `.git` to that repo's ledger INSTEAD of the home ledger. A `git init`ed
or submodule directory with no ledger, or a stale `.git` file from a pruned
worktree (git calls it "not a git repository", read as feature-off), then let a
Claude Edit or a patch through a peer's claim on the enclosing directory. HEAD
denied both. The home claim covering the path is now always checked, and the
nested ledger is consulted in addition.

**A target repo's brake (F-28).** A cross-repo edit consulted the target
ledger's claims but not its pause, so `buddy pause all` in repo X did not stop
a session whose cwd was elsewhere. The target ledger's pause now applies. The
label is the one that ledger knows the session by, never one borrowed from
another ledger.

**Evidence.** `internal/cli/gatetargets_test.go`: every test failed against the
pre-fix snapshot (`refs/snapshots/orchestrator-pre-fixes-20261006`). Ten
mutations, one per guard (the shell detector, its governed and ungoverned arms,
nested-replaces-home, the lexical, entry and dangling spellings, identity
refused before discovery, the governed identity arm, and the foreign pause),
each killed its test. These are hermetic fixtures. The shell-routing claim rests
on Codex source at `rust-v0.160.1` and still needs a native case in
`qualify-codex.py` on the admitted version.

### 2026-10-07 independent verification continuation

The [verification report](CODEX-ORCHESTRATOR-VERIFICATION-REPORT.md) supersedes
the native-owed and nested-shell statements above. Retained Codex 0.160.0
reproduced the shell route, including `bash -lc`. Admission must cover `Bash`
and `apply_patch` (V-01). A bounded shell syntax tree now recognizes static
command words, substitutions, common launch wrappers and nested literal shell
scripts, while preserving ordinary quoted arguments (V-04). The same tree
collects literal working directories for jurisdiction. Limits do not create
ledger jurisdiction: an uncertain route refuses where a discovered ledger
governs; unmanaged scope remains feature-off. Dynamic names, undisclosed
workdirs and placements beyond analysis bounds remain cooperative limits.

Dangling links now resolve component by component, with at most 40 link
traversals, preserving physical `..` within link contents (V-03). An error in
one spelling does not prevent discovery of another governed spelling. All
applicable home and target reservations remain additive. Native Codex controls
measured entry deletion, lexical `..`, dangling-target creation and moves;
Claude Write controls measured lexical normalization and refusal of symlinks.
Those measurements do not make general shell side effects claim-enforced.

### 2026-10-07 audit continuation: shell recognition inverted, uncertainty refuses (A-01, A-12)

The [audit](CODEX-ORCHESTRATOR-AUDIT.md) found the continuation above stronger
than the code (A-01, A-13). "Common launch wrappers" was a list of programs
known to run an operand, and every other program passed. In a governed temp
repo, real bash and zsh ran `apply_patch` past the gate through `bash -c --`,
`bash +x -c`, `bash +o posix -c`, `eval`, `trap`, a shell fed a here-document,
here-string or pipe, `bash <(...)`, brace expansion, `csh`/`tcsh -c`, `nice`,
`caffeinate`, `arch`, `stdbuf`, `env -P`, `xargs -S`, zsh's `noglob`,
`nocorrect`, `repeat 1`, `=apply_patch` and anonymous functions, `coproc`,
`find -exec` and `script`. From an ungoverned cwd, `pushd`, `cd ~/...`,
`bash -c -- 'cd ...'` and a here-document holding `cd` were not placed. "Literal
heredoc bodies are not commands" was false for a here-document fed to a shell.

Two constrained Codex design passes reviewed the replacement: a REJECT, then a
PASS WITH CHANGES after the scope was split. Nine rounds of constrained code
review followed (the first two in two passes each), and every round was a REJECT
on exact inputs. Each finding got a regression that was watched to fail and then
fixed: 15, 7, 4, 4, 4, 3, 4, 4 and 4 findings by round. The findings moved from
structural gaps to edges. The structural gaps were a compound statement's
stdin, descriptor duplication and moves, run-time-produced script text, and
nested scripts' inherited descriptors. The edges were `env -S` escapes and
comments, `-vC` clusters, fish's `-C`/`-capply_patch`, `sudo -s` fed a
here-document, 2^63-field brace counts, and quadratic descriptor and
here-document rebuilds (each measured by bytes allocated, up to 4.15 GB, then
bounded). From round five on, Codex ranked findings by realism; most it labelled
crafted. **Open, by decision:** a persistent `exec <<'SH'` redirection that a
LATER statement's shell inherits (round 9, crafted P2). The walker scopes
redirections to their statement. Round 9's own fixes have not been re-reviewed.
The verdicts are source evidence only, kept apart from the test evidence below. The REJECT named forms whose
script is PRODUCED at run time (`printf 'apply_%s' patch | bash`,
`bash -c "$(...)"`, a local function piped to a shell), which no list of
spellings closes. The decision:

- **The default is inverted.** An unknown program's operands are all possible
  command positions. A data program (`echo`, `grep`, `cat`, `which`, `make`, ...)
  stops the scan only at an ESTABLISHED command position: the first word, or
  the one a known wrapper's option grammar lands on. `git` is data except
  `bisect run`, `submodule foreach` and `rebase --exec`. `man` and `sort` stay
  unknown programs, because `-P` and `--compress-program` run one. Each
  `find -exec` action is read on its own.
- **Script text a shell will run is read in turn:** `-c` in any option cluster
  of either sign, `fish --command`, `eval`, `trap`, `env -S`, GNU `script -c`,
  and a here-document or here-string on a stdin-reading shell. A here-document
  on `cat` stays data.
- **Uncertain text refuses where a ledger governs, and only there.** That means
  script text produced at run time (a pipeline, a process substitution, an
  expansion in a `-c`, `eval` or here-document text), a command the bash-mode
  parser rejects, and an exhausted budget. One request-wide budget is shared
  across every script: depth 8, 128 KiB per script, 512 KiB and 64 parses in all,
  and brace fields counted before expansion (256 per word, 1,024 in all).
  `mvdan.cc/sh` has no zsh mode, so zsh-only syntax is uncertain rather than
  guessed at.
- **Speculative text** (an unknown program's operand that looks like shell,
  as with `su -c`, `watch` or `flock -c`) is read at command positions only. A
  parse failure there means it is not readable shell, so it stays cooperative.
  Reading its operands too would refuse prose such as
  `curl -d 'please run apply_patch now'`.
- **Jurisdiction** adds `pushd`, zsh `chdir`, zsh `cd old new`, `sudo -D` and
  `--chdir`, and an unquoted `~` or `~/...`. A tilde is read against the hook's
  `HOME` and against every literal `HOME` the command assigns
  (`env HOME=x zsh -c 'cd ~'`). A script that does not parse still has its
  literal `cd` operands recovered lexically.

**Cost, measured** (2026-10-07, counts only, this machine's Claude Code and
Codex transcripts). The old recognizer would have refused none of 109,990
unique agent commands where a ledger governs. The new one refuses 87 (0.08%),
all but one of the uncertain class: 31 do not parse, 37 run a shell `-c` script
built at run time, 13 run `eval` on run-time text, 4 feed a shell from a
pipeline and 1 from a process substitution. The single direct find was a probe
command from this work, which did run `coproc apply_patch`.

**Cooperative, by decision.** Dynamic command names; script files
(`bash file.sh`, `source file`, `bash < file`); aliases and functions defined
outside the command; programs that run another language's text (awk
`system()`, `sed e`, `python -c`, make recipes, git aliases and config); command
names read from stdin (`xargs` with no command); `CDPATH`, `OLDPWD`, the pushd
stack and `~user`; and a workdir outside the command. These are the same class
as the cooperative boundary the gate already had: text the gate cannot see.
Running an embedded shell interpreter was not considered.

**Dependency (A-12).** `mvdan.cc/sh/v3` v3.12.0, pure Go, is linked into the
claims binary for this parser. Nothing in it is used to expand or execute. It
breaks no stated rule (no cgo); CLAUDE.md now names it.

**Evidence.** `internal/cli/shellroute_test.go`. Every refused form carries a
kernel control where this box can run it: the same command under the named
real shell, with a fake `apply_patch` first on PATH, must run it. Three controls
caught wrong cases before the gate was trusted: zsh keeps `{apply_patch,}`'s
comma, and macOS bash 3.2 has no `coproc` and reads nothing from
`source <(...)`. These forms all failed on the tree before this change. The
false-denial controls are the audit's ten, plus the ones this repo runs daily.
The jurisdiction forms each have a kernel `pwd -P` control and a plain-
directory twin that must pass. The A-14 grammar tests pin the option-value
table, `sudo -D`, `env -S` and the budget.

**Mutations** (private copies, one guard at a time, applied by script, build
failures never counted). 110 mutants are defined against the final code. (This
said 111 until 2026-10-11: the count was taken before the closed-on-move code
below was removed, and its mutant with it.)
Earlier sweeps on intermediate code ran 49 and then 45 of them. They found three
equivalent or untested pieces of code, each resolved: the dead `pipeReader`
(removed, since the parser nests pipelines on the left), a closed-on-move
descriptor (removed, since it was unobservable and only over-refuses), and a
`csh` entry that needed a stdin-fed case. Those sweeps also exposed two
mutants of mine that failed to build, and a wall-clock test that failed its own
control under load (replaced by a resolution count). The final sweep ran 71 of
the 110 before Claude Code stopped it for low system memory: 70 killed, 1
survived. The survivor, `over-budget-command`, showed that no test put an
over-budget brace word at the command position. `TestGateShellRouteOverBudgetCommandWord`
now does.

*2026-10-11, the rest.* The other 39 ran, with `over-budget-command` again, in
two private copies, each behind an unmutated control. `over-budget-command` is
killed by its regression. `dup-stdin` was re-targeted first: the closed-on-move
removal took the `from` variable it matched, so it would have matched nothing.
34 more were killed outright. `brace-budget` is what ran memory out on
2026-10-08. Without the field count, the budget test's `foo
{1..99999999}{1..99999999}` expands without end: 5.4 GB in 100 s in the sweep,
ended by hand. Alone, under `-v -timeout 60s` and an RSS guard, it is killed by
`deny/foo {1..5000}`, which it allows. The expansion then reaches 8.9 GB before
the guard ends it, so a sweep that runs this mutant unbounded will exhaust
memory again. Four survived the whole package. Each was a guard no test
reached. Each now has a regression, which passes on the tree and was watched to
kill its mutant in a copy taken after it landed:

- `data-skip-expansion`. A data program's operands spent the field budget the
  whole command shares, so `touch x{1..1100}; {ls,-l}` refused.
  `TestGateShellRouteDataOperandsSpendNoFieldBudget`, with a kernel-controlled
  refusal proving the later brace word is read.
- `scriptOf-overbudget`. An over-budget word in text a command runs as shell
  was refused as "built at run time", which is false of a static brace word
  and names the wrong fix. `TestGateShellRouteOverBudgetScriptWordSaysBudget`;
  its kernel control is bash running `eval {apply_patch,x{1..300}} < p.patch`.
- `envS-builder-cap`. The `env -S` builder kept a 7.4 MB script built from a
  60 KB command. `TestShellRetainedScriptsAreBounded/env-S-tail`.
- `unparsed-bound`. Scripts refused as over budget were retained past the
  lexing bound: 63 MB, against a 34 MB limit, when four shells read one
  here-document whose `eval` joins 256 fields of 60 KB.
  `TestShellRetainedScriptsAreBounded/eval-fields-read-by-many-shells`. A first
  version fed 8,000 shells an 80 KB here-document and did not kill: a
  here-document read is budgeted before analysis and never reaches this bound.
  Retention costs scan work, not allocation (the strings are shared), which is
  why the allocation checks could not see it.

All 110 have now run on the final `patch.go`. Every one is killed by a test's
assertion.

## D-074 — `buddy-adapter` is the one opt-in exception to "buddy launches, wakes and ends nothing"

2026-10-06. Records a reversal that D-070 made without saying so (review F-15).

D-056 cut `buddy spawn` and `exit` because a verb that launched would make buddy
a supervisor, and because #20 named the hazard of a launch reachable from a
relayed instruction. D-039 and D-033 say buddy wakes nothing. D-070 then added a
binary that launches one worker, resumes it, writes wake input to its owned
stream, interrupts it and kills it on close. No line reconciled the two, and a
constrained reviewer was handed both GIVENs side by side.

**The exception, bounded.**

- `cmd/buddy-adapter` is a SEPARATE, opt-in binary. `buddy`, the claims binary,
  still launches, wakes, schedules and ends nothing, and gains no verb that does.
  Hooks still run `buddy`.
- The adapter acts only on argv typed by the operator, or by a coordinator that
  already holds the operator's authorization for that launch. Argv is a
  transport, not proof of authority. Nothing in the ledger, a message body,
  chat, a claim description or worker output can start, wake or approve
  anything: the queued body stays in the ledger, a wake is the fixed text
  naming an id, and approvals enter only through the separate operator
  descriptor (D-070, as hardened by D-077).
- It controls only the worker it launched, over that worker's own stdio. No
  attach to an existing session, no pane injection, no `/tmp/cc-socks` wake for
  an adapter recipient (D-076), no lane management.

**Why #20 does not apply.** #20 was a launch or exit reachable from peer text.
Here the launch path starts only from typed argv, and the only inputs the running
adapter takes from peers are message ids that it resolves against its own
captured identity.

Ending a lane is still the operator's act, and so is closing an adapter. The
adapter's own kill of its owned process group on close is cleanup of what it
started, not an exit verb.

## D-075 — Recovery admission is exact-version and terminal-evidence-bound; it supersedes D-070's "Claude resume is unsupported"

2026-10-06. Records behaviour decisions whose only record was the plan's
receipts (review F-18).

**What the code admits.**

- **Codex** `resume` resumes the captured native thread id with the captured
  incarnation. `reconcile` is WITHDRAWN (unsupported) until a bounded history
  read is qualified (D-077). It read the full thread history over the live
  control transport, and one reply past the 4 MiB frame bound ended the worker
  mid-turn. While it existed it read only the journalled turn, never replayed a
  wake or approval, and never inferred tool effects; its earlier native fixtures
  are history.
- **Claude** `resume` (experimental) is admitted only when every journalled
  turn reached a terminal result for its exact input and the worker went idle
  after it. The result must be `completed`, `aborted_streaming` (cancelled
  while awaiting a response) or `aborted_tools` (cancelled at an approval). An
  unfinished turn, a missing or unknown `terminal_reason`, a late approval or
  busy event, a disconnect mid-turn, or an uncertain wake refuses before the
  ledger refresh and before native launch. SDK conversation history
  supplies no qualified terminal receipt (Step 4 history receipt), so history
  never substitutes for one. Claude `reconcile` is unsupported.
- **Claude manual compaction** is admitted for resume only on exact versions
  whose boundary and local-command result were separately measured
  (`claudeManualCompactionQualified`: 2.1.290 and 2.1.291). Its boundary UUID
  and its exact-input local result plus idle are both required. A lost boundary
  or result, cancellation, or an active-response crash refuses.
- Every start and resume re-checks the executable's exact version against the
  captured one, so a cross-version resume refuses (D-071).

This supersedes the "Claude resume is unsupported" sentence in D-070. It also
supersedes the sentence in D-071's 2026-10-06 continuation that limits manual
compaction to 2.1.290; the later 2.1.291 compaction receipt widened it. Both
stand as history. Automatic compaction thresholds, arbitrary crash timing, tool
side effects and real-provider recovery are not admitted.

## D-076 — Adapter mail is its own register in every legacy view; no legacy wake or correction crosses channels; a lifecycle beat renews the paths it observed

2026-10-06. Review findings F-12, F-24, F-25, F-26 and F-27; each was a failing
test against the tree before its fix.

Legacy drains exclude adapter rows by design (D-070), because a legacy hook
carries no captured token. The legacy VIEWS did not know that:

- `who`/`status` printed `INBOX 0 undelivered` over a full adapter queue.
- `msg` told the sender delivery "waits for its next tool call", and no adapter
  worker runs a legacy drain.
- `sent` and `msg` offered a D-039 socket wake: the endpoint registration put
  the receiving pid in the process register, and "run buddy inbox" cannot show
  adapter mail.
- A plain `msg --supersedes` "corrected" an adapter message into the legacy
  inbox, which never shows the original.

**Now.**

- `who`/`status` print an `ADAPTER` line beside `INBOX`. It shows the queue for
  the current incarnation with its oldest age, mail captured for an earlier
  incarnation, and whether a receiving endpoint is registered. It prints
  `(none)` rather than being omitted.
- A send's result line counts adapter mail separately. For a session the ledger
  positively records as an adapter recipient, the mechanism clause says that its
  adapter never fetches a plain message.
- `wakeAddr` never offers a socket wake to an adapter recipient. Its adapter's
  journal serializes wakes. `sent` labels an adapter message as such.
- A legacy correction of an adapter message refuses, in the send and its dry
  run alike, until the adapter channel has a correction of its own.

Absence of adapter rows is not proof a session has a legacy drain. Without a
schema change the ledger cannot mark a logical session that has no adapter mail
yet, and none was made.

**Lifecycle beats (F-26).** `lifecycle hook` used to pass no path, so a Codex
worker patching its claimed file every ten minutes showed that claim STALE.
A PostToolUse now passes every path its tool names, extracted with the gate's
bounded parser, and `BeatLogical` renews only claims covering those paths, in
the identity check's transaction. A busy or idle event renews nothing. Workers
launched by `buddy-adapter` install only the PreToolUse gate, and an attempted
write is not an observed one. Their renewal route needs its own PostToolUse
observation; it is not provided here.
*Superseded in part 2026-10-13 by D-078:* that route is now the controller's own
observation of completed native writes, not a PostToolUse in the adapter's
settings.

**Manual `hello --session` (F-27).** It still reads non-TTY stdin as a hook's
and refuses what is not one. Treating stray bytes as "no hook" would reopen the
delayed-hook path D-069 closed. The refusal and the usage line now name the
hand-run recipe, `</dev/null`.

### 2026-10-07 independent verification continuation

Delivered adapter mail remains positive evidence for its captured current
incarnation (V-05). Earlier-incarnation mail and endpoints do not classify a
new legacy recipient (V-10). `sent` independently excludes adapter messages
from both legacy-wake paths, even after a legacy successor registers. A logical
session without such evidence still has no process address to wake. This does
not add the missing adapter PostToolUse renewal route.

## D-077 — The adapter owns its worker's process group, validates the operator descriptor before any child, and records a definite refusal as `not_submitted`

2026-10-06. Review findings F-02, F-07, F-08, F-09, F-10, F-11, F-21, F-22,
F-23, F-29 and F-30. Each was a failing probe or entry test against the tree
before its fix, and each guard was mutated out and watched to fail its test
(35 mutants, all killed).
*Corrected 2026-10-07 by the audit (A-04):* "all killed" was true of the
verifier's working copy, not of the tree. Replayed against the tree's own
tests, fourteen kills in this family — the descriptor lower bound, the group
TERM and KILL, the SIGPIPE ignore, the six Claude and three Codex
`not_submitted` refusals, and the endpoint budget — rested on nine
`TestVerification*` tests that were kept only as `.txt` files in the evidence
directory and never landed. The audit landed tests that kill those fourteen
(its "Landed here" table); the count is true of the tree only since then.

**The operator descriptor (F-02).** `--operator-fd` was wrapped with
`os.NewFile` after the native child had started, and never marked
close-on-exec. A descriptor handed over by a shell redirect or `pass_fds`
arrives inheritable, so the worker held the approval channel: a Codex shell
tool read the operator's line. A FIFO opened read-write let the worker write an
`approve` request that `OperatorApprove` accepted, so the model answered its
own approval. Now, right after flag parsing and before any file is opened or
child started (the version probe included), the descriptor must be open, have
access mode exactly `O_RDONLY`, and be a FIFO, character device or regular
file. It is then marked close-on-exec. An unopened number, a write end, a
read-write FIFO, a socket or a directory refuses with no child started and no
journal written.

**Process ownership (F-07, F-08).**
- **Process group.** The worker runs in its own process group. Close sends
  SIGTERM to the group, waits for the LEADER to exit (2 s at most), sends
  SIGKILL to the group and the leader,
  reaps once, and closes our stdout read end, so a descendant holding stdout
  cannot keep the reader alive. The grace is the leader's, not its tools': a
  leader that exits on TERM at once, or by itself (`watchExit` runs the same
  path), ends the wait, so a tool still running gets TERM and KILL back to
  back. (Wording corrected 2026-10-07, audit A-13; `process.go`'s header had
  it right.) The group is signalled only while its leader is
  unreaped: the zombie reserves the pgid, so the id cannot be reused. Exit is
  detected without reaping (kqueue on darwin, `waitid(WNOWAIT)` on linux).
- **Signals.** INT, TERM and HUP run Close; SIGPIPE is ignored.
- **Limits.** A descendant that re-groups itself escapes. SIGKILL of the
  adapter runs no cleanup, so a free journal lock proves only that the adapter
  exited, never that its worker did.
- **Native denies.** A well-formed Claude subagent or no-active-input permission
  request now gets a native deny instead of a transport teardown.
- **Oversized frames.** A frame past the 4 MiB bound still fails the transport,
  as uncertainty: the pending-approval state becomes unknown, never absent.
  Codex `reconcile` is withdrawn (D-075). The pinned 0.160.0 `thread/read` has no
  bound, and the paged `thread/turns/list` is unqualified.

**Definite refusals (F-10).** A wake refused before any byte reached the native
stream (busy, interrupt uncertain, closed, text bounds) used to be journalled
as `unknown`. Its message could never be woken again, and Claude resume
refused permanently, after the ordinary case of waking a busy worker. Such
refusals are now `ErrNotSubmitted`. The controller journals `not_submitted` and
frees the duplicate-suppression slot. Replay frees it only after an open intent
with no turn, and otherwise refuses the journal. A failed or partial write
stays uncertain. If recording the refusal itself fails, the wake stays unknown
and the journal refuses further effects.

**Observation gaps (F-11, F-30).** An event the ledger could not record used to
leave the receipt reporting `pending_approval: observed_absent` from stale
state. Any failure after the identity check now records a gap and sets state
unknown. Known approvals stay listed as a possibly incomplete set, and only a
recorded idle or end clears the gap. Each ledger call made by `Observe` is
bounded at 10 s, and Close cancels before it takes the lock.

**Admission strictness (F-09, F-21, F-23).**
- **Matchers.** Claude's gate matcher was read as an unanchored Go regexp.
  `Edit|Write` "covered" NotebookEdit, which natively it does not. The matcher
  must now be `""`, `*`, `.*`, an exact `|`/`,` list, or the anchored literal
  alternation `^(A|B|…)$` that the native fixtures use, naming Edit, Write and
  NotebookEdit.
- **Keys.** Settings are decoded with exact, case-sensitive key allowlists that
  refuse duplicates (Go folds key case; the native JSON.parse does not).
  `async` must be absent or false, and `timeout` must be 10-600 s.
- **Executables.** `NewClaude`/`NewCodex` require an absolute path, resolve
  symlinks once, and re-check file identity before exec. The same-user rewrite
  race is documented, not claimed closed.

**Presentation and budgets (F-22, F-29).** Pending approvals in receipts carry
a digest and a fenced 512-byte summary. Exact detail travels once, in
`native_event`, up to 16 KiB, and is marked omitted beyond that. The hook's
advisory endpoint step has its own 250 ms budget (reasoned, not measured
under load), so it can no longer spend the gate's 3 s and turn a slow ledger
into a denial.

**Native qualification still owed.** Each of the following needs a native
check on each admitted version:
- Claude's deny shape for subagent and no-active-input requests;
- whether native tool jobs re-group themselves;
- SIGTERM behaviour inside the grace;
- `thread/turns/list` bounds, before reconcile returns;
- native matcher and timeout semantics;
- the endpoint budget under load.

### 2026-10-07 verification continuation

The independent verification found further counterexamples after the initial
remediation. Its report is `CODEX-ORCHESTRATOR-VERIFICATION-REPORT.md`; the
original receipts above remain historical evidence.

- **Codex hook coverage (V-01):** on retained 0.160.0, a shell `apply_patch`
  changes a peer-held file when the trusted matcher covers only `apply_patch`.
  Native hooks identify that operation as `Bash`. Adapter admission and the
  offline diagnostic now require both, through `^(apply_patch|Bash)$`, its
  reversed literal union, or the already-admitted empty/`.*` wildcard. The
  qualification runner has a shell-patch case and a permitted direct-tool
  control. Updating a temporary fixture's trust remains distinct from editing
  installed hooks.
- **Definite refusal (V-02):** neither the prior intent nor the refusal row may
  contain a native turn ID or result reference. Contradictory rows refuse
  replay; a contradictory live result remains uncertain rather than releasing
  the wake's deduplication reservation.
- **Executable identity (V-06):** resolving Claude's launcher symlink leaves a
  version-number basename, which the hook's ancestry check cannot recognize.
  The adapter executes the same resolved, identity-checked file with `argv[0]`
  set to `claude`. The executable path and process birth fence are unchanged.
- **Reconciliation decision:** retain the withdrawal. This verification slice
  does not introduce `thread/turns/list` or another native history operation.
  Revise dependent qualification cases to require an explicit unsupported
  result for a wake with a known turn, no adapter history request, and preserved
  claims, mail and replay guards. A wake without a recorded turn may still
  project `unknown` from its transport journal without a native query. Earlier
  full-history receipts do not qualify current reconciliation. Reintroducing
  bounded native history requires its own implementation and qualification.
- **Fixture process ownership:** an adapter and its worker now have distinct
  groups. Crash fixtures must capture both through the verified live ancestry
  before the fault and record which groups they deliberately kill. A cleanup
  fallback is still a failure, never evidence that the adapter cleaned up.
  The new native job probe also requires an empty forced-cleanup record for
  PASS, retaining that record on close exceptions (V-13).
- **Startup and shutdown (V-07, V-08, V-11):** receipt output uses a serialized
  writer with cancelable acknowledgment, so a full consumer pipe cannot prevent
  signal cleanup. Version probes have their own bounded output and owned group;
  the leader remains unreaped through final group cleanup. Expected shutdown
  cancels the event pump before closing the controller; unexpected transport
  loss still reports uncertainty.
- **Approval gaps (V-09):** listed approvals after an unrecorded event are last
  observations, possibly stale as well as incomplete. Pending approval state
  stays `unknown`; a historical list cannot establish that approval is still
  required.
- **Native cleanup limit (V-12 / F-07):** retained Claude 2.1.291 actually put a
  tool job in a separate process group. Adapter Close exited successfully while
  that job remained alive. The disposable fixture recorded this as a failure
  and cleaned only its captured, identity-matching group. The group-only runtime
  contract above is unchanged; complete native descendant cleanup is not
  qualified. A leftover endpoint socket was also observed after its receiver
  exited; exact process-birth eligibility, not pathname disappearance, prevents
  reuse. Subagent/no-active-input native deny branches remain unqualified.
- **Measured settings and budget:** native 2.1.291 uppercase literal/anchored
  matchers fired while a lowercase control did not. A ten-second hook timeout
  led to native permission handling rather than an automatic deny. Under a
  700 ms SQLite writer lock the advisory endpoint step yielded around 250 ms;
  held and unheld gate controls still behaved correctly. This does not qualify
  other versions, the 600-second upper bound, or production load.

### 2026-10-07 continuation: ownership record and fail-closed resume (A-02)

The audit's A-02 reproduced the gap D-077 stated and nothing enforced: SIGKILL
the adapter while its Codex worker is mid-turn, and the worker and its tool
stay alive (their own process group; the adapter's death closes only their
stdin). `--resume` then took the now-free journal flock, passed every check,
and launched a SECOND app-server that received `thread/resume` while the first
was still running (measured: 52349 launched while 51299 lived; the entry-level
regression reproduced it on this tree as 17695 while 17655 lived). Neither the
receipt nor the journal named a native pid, so nothing could have refused. A
constrained Codex design review of the proposed fix returned PASS WITH CHANGES
(scratchpad `codex/a02-design-verdict.md`); its changes are applied below.

**The contract.**
- `<receipt>.proc` is the ownership record, schema `buddy.ownership.v1`:
  attempt id, operation (`start`/`resume`), phase (`intent`/`spawned`), the
  bound journal (device and inode of the flocked `.events`) and receipt path,
  the captured identity, the recording adapter's pid and kernel start time,
  and for `spawned` the leader's pid, start time, pgid and sid plus the
  resolved executable's identity. It is published crash-consistently
  (`.proc.tmp`, fsync, rename, fsync dir); a leftover `.tmp` is removed under
  the flock before anything reads the record.
- Every worker spawn goes through ONE path, `startJSONProcess`, which takes an
  explicit, mandatory `*LaunchAuthorization` (never a context value): the
  controller mints it under its lock and the flock; `Spawn` consumes it
  before `cmd.Start` (a second launch on the same authorization is refused
  before any child exists; a nil one is refused), publishes the intent for a
  fresh attempt, starts the child, pins it (`kern.proc.pid` on darwin,
  `/proc/<pid>/stat` field 22 after the last `)` on linux; other platforms
  cannot pin and refuse the launch), and publishes the spawned record before
  the process is returned, so before the adapter's first native byte. If the
  record cannot be published the child is ended while its leader is unreaped
  (the group id is still ours) and the launch fails. The version probe stays
  a separate, unauthorized child. After the backend returns, the controller
  still asserts the authorization was published exactly once and closes a
  backend that returned an unrecorded worker: an extra assertion, not the
  guard.
- Start republishes the spawned record for the same attempt with the captured
  identity once the receipt is published, BEFORE `.start` is removed, so
  admission requires `identity == saved identity` for every record, and a
  crash in that window leaves `.start` (already refused).
- Resume admission is passive and decides once after every check: record
  present, parsed, bound to this journal and receipt, naming the saved
  identity, phase `spawned` with `pgid == leader pid > 1`; then the recorded
  adapter gone, the recorded leader gone (a pid with another start time is
  recycled: not ours, never signalled, counts as gone), and the recorded group
  empty by `kill(-pgid, 0)`, where ONLY `ESRCH` is absence (measured: a group
  whose only member is an unreaped zombie answers `EPERM`). An observation
  that cannot be made refuses and is not waitable. The one exemption: a
  recorded adapter that is THIS process (pid and start time), which holds the
  flock and closed the controller that wrote the record; the leader and group
  checks still decide.
- An intent-only record refuses, naming the receipt, the attempt and the
  recording adapter's pid and start, saying a worker may have started before
  its pid was published and the adapter has verified nothing; the operator's
  recourse, after their own check, is to abandon the receipt and start a fresh
  worker with a NEW receipt; never delete the record or resume the same
  captured session.
- No record refuses, naming the file: a receipt from before this change gets
  no automatic resume; a free journal lock proves nothing.
- `buddy-adapter --resume --wait-prior DURATION` (cap 10m; negative, larger,
  or without `--resume` refused before any child) re-runs the whole admission
  every 250 ms while the only refusals are alive verdicts, waiting through
  `ErrJournalOwned` (a sentinel now) for a prior adapter still holding the
  lock. Timeout, a non-waitable refusal or an observation error refuses with
  the last reason, on stderr like every pre-controller refusal. It waits
  before the 30 s start context; it signals nothing and writes no ledger row.
- The flock is held from `NewController` through admission, refresh, intent,
  spawn and the controller's lifetime; admission and spawn run under `c.mu`.

**The `.start` answer.** `.start` fences only Start, until the receipt is
published. Every spawn, resume included, is fenced by the flock plus durable
intent-before-spawn: a later admission always sees the most recent attempt,
never an older record naming a dead leader. Two resumes cannot both launch.

**Excluded, and why.**
- Rollback of the record (restoring an older `.proc` over a newer one while
  the newer worker lives) admits a resume. That is the machine user's
  deliberate tampering with their own artifact; the ledger trusts the machine
  user (invariant 6), so no journal attempt row is kept to detect it and the
  bindings make no such claim. Said in `ownership.go`.
- Descendants that leave the group (`setsid`, a shell's job control) are not
  visible to the group check; stated as a limit, not claimed covered.
- Killing anything the adapter cannot pin, `--end-prior`, descendant sampling,
  any new signal path, and `Setsid` (kept on `Setpgid`): the operator's open
  choice and native qualification (Q1/Q3) respectively. Resume admits only
  what is observed gone.
- A failed launch does not unpublish its intent: an uncertain intent is never
  deleted as error cleanup, and an older record is never fallen back to.

**Tests (hermetic, darwin; each refusal with a positive control).**
`internal/harness/ownership_test.go`: the record exists, phase spawned, right
pid, durable, before the worker's first byte (the helper child reads `.proc`
before blocking on stdin); single-use authorization and nil authorization
refused before any child; persistence failure of intent (no child) and of the
spawned record (child ended, launch fails, intent on disk); the pure
classifier over fixed observations (18 rows); the resume matrix through real
processes (gone admits; no record, another journal, another identity, unbound
identity, intent-only, a newer intent over an older spawned record, adapter
alive, leader alive, leader exited with an in-group tool alive, recycled pid
whose numeric group exists); recycled pid admitted when its group is gone;
crash at each boundary (after intent, after spawn before the record, after
the record) with the next resume's verdict; unrecorded backend launch refused
on start and resume; start-written record carries the identity and a record
without a receipt refuses a fresh start; `NewControllerAfterPrior` admits once
the leader exits, refuses at its bound without a ledger write, refuses a
non-waitable refusal at once, and waits through flock contention; stale
`.proc.tmp` removal; the zombie-only group's `EPERM`.
`cmd/buddy-adapter/resume_test.go` (built binary): SIGKILL the adapter
mid-turn with the fake worker alive, `--resume` refuses naming the leader and
`--wait-prior`, launches no second app-server, signals nothing, and resumes
once the group is ended by hand; `--wait-prior` admits once the worker is gone
and refuses at its bound; the flag's bounds. Fail-before on the tree before
this change: the entry test observed the second app-server and `thread/resume`
(`scratchpad/a02-logs/entry-fail-before.log`); the harness-level tests could
not compile against the old API, so their fail-before is the mutation table.

**Mutation evidence (private rsync'd copies, python-applied, `go test
-buildvcs=false` over `internal/harness` and `cmd/buddy-adapter`; a build
failure is not a kill; scratchpad `a02-mut/`).** 28 mutants, 27 killed, 1
equivalent.

| Mutant | Guard | Killed by |
| --- | --- | --- |
| M01 consume guard off | second launch on one authorization | TestLaunchAuthorizationIsSingleUse |
| M02 intent not published | durable intent before `cmd.Start` | PersistenceFailure/intent, /spawned; CrashAtEachBoundary/after-intent, /after-spawn |
| M03 spawned record not published | record before the process is returned | RecordPrecedesFirstNativeByte; PersistenceFailure/spawned, /control; CrashAtEachBoundary/after-spawned-record |
| M04 `Published()` always true | post-return assertion | RefusesUnrecordedBackendLaunch/start, /resume; PersistenceFailure/spawned |
| M05 load refusal admits | no record / bad binding refuses | OwnershipMatrix/no-record, /other-journal, /other-identity, /unbound-identity; AfterPrior/non-waitable |
| M06 journal binding off | record bound to this journal and receipt | OwnershipMatrix/other-journal |
| M07 identity binding off | record identity == saved | OwnershipMatrix/other-identity, /unbound-identity |
| M08 intent phase not special | intent-only diagnostic | ClassifyOwnership/intent-only; OwnershipMatrix/intent-only, /newer-intent-supersedes; CrashAtEachBoundary |
| M09 leader alive ignored | leader alive refuses | ClassifyOwnership; OwnershipMatrix/leader-alive; CrashAtEachBoundary/after-spawned-record; AfterPrior/timeout; entry: ResumeRefusesWhileRecordedWorkerLives, ResumeWaitPrior/refuses-at-bound |
| M10 `kill(-pgid,0)` success read as empty | group members refuse | OwnershipMatrix/leader-exited-tool-alive, /recycled-group-id; ObserveProcessPinsByStartTime; ProbeGroupZombieOnlyIsNotAbsence |
| M11 EPERM read as empty | only ESRCH is absence | ProbeGroupZombieOnlyIsNotAbsence |
| M12 pgid validation off | `pgid == leader pid > 1` | ClassifyOwnership/group_not_the_leader's_own, /group_id_1, /group_id_0 |
| M13 adapter alive ignored | adapter alive refuses | ClassifyOwnership/adapter_alive; OwnershipMatrix/adapter-alive |
| M14 leader observation error ignored | observation error refuses | ClassifyOwnership/leader_observation_error, /error_outranks_alive |
| M15 wait ignores non-waitable | `--wait-prior` refuses at once | AfterPrior/non-waitable, 5/5 runs |
| M16 wait ignores its bound | `--wait-prior` refuses at the bound | AfterPrior/timeout; entry ResumeWaitPrior/refuses-at-bound, 5/5 runs |
| M17 start bind skipped | start-written record carries identity | StartWrittenRecordCarriesIdentity; OwnershipMatrix/gone; every resume-after-start test (14) |
| M18 resume post-return check off | resume refuses an unrecorded launch | RefusesUnrecordedBackendLaunch/resume |
| M19 `--wait-prior` cap raised 100x | cap 10m | entry WaitPriorFlagBounds/11m (after its assertion was tightened: the first version matched "wait-prior" in the fixture's temp path and let this survive) |
| M20 stale `.proc.tmp` kept | removed under the flock | OwnershipRecorderCrashConsistency |
| M21 self-exemption for any live adapter | only this exact process is exempt | OwnershipMatrix/adapter-alive |
| M22 reaped pid read as error | ESRCH after EIO is gone | ObserveProcessPinsByStartTime; OwnershipMatrix/gone; and every resume whose recorded worker is gone (22 tests, DuplicateWakeSurvivesRestart among them) |
| M23 changed start time read as alive | recycled is not ours | ControllerResumeAdmitsRecycledLeaderPid; ObserveProcessPinsByStartTime |
| M24 start over a record with no receipt | refused | StartWrittenRecordCarriesIdentity |
| M25 ownership admission skipped | the whole process check (A-02) | entry ResumeRefusesWhileRecordedWorkerLives; OwnershipMatrix; CrashAtEachBoundary; AfterPrior |
| M26 nil authorization replaced by a zero one in `startJSONProcess` | nil refused at the launch path | SURVIVED, then made equivalent: it exposed that a zero-value authorization launched and published `.proc` in the cwd; `Spawn` now refuses an unminted authorization (M27), so this substitution is refused the same way. The launch-path nil guard is belt-and-braces over `Spawn`'s own (M28). |
| M27 unminted guard off | zero-value authorization refused before a child | StartJSONProcessRefusesUnauthorizedLaunch/zero-value |
| M28 `Spawn` nil check off | nil refuses, not panics | LaunchAuthorizationIsSingleUse |

Mutants M10-M16 ran while the coordinator's concurrent `internal/cli` edit did
not build, so their adapter-level kills were discounted and only harness-level
kills counted; all seven were killed by harness tests. The timing-sensitive
tests (`AfterPrior`, entry `ResumeWaitPrior`, `RefusesWhileRecordedWorkerLives`,
`PersistenceFailure`, `ZombieOnly`) were run repeatedly on the real tree under
load average 7-8; one run of ten lost the entry fixture's START receipt to a
15 s fixture deadline before any resume logic ran (the adapter had not yet
emitted its first receipt), the rest passed.

**Codex code review (constrained, gpt-6-astra, xhigh; source evidence only,
not test evidence; scratchpad `codex/a02-code-verdict.md`): PASS WITH
CHANGES.** Q1-Q6 found no interleaving that admits beside a live leader or
in-group tool, judged the self-exemption sound under the flock, the
signal-before-reap order kept on every failure arm, no observation error read
as absence, and no crash-induced false acceptance in publication or binding.
Its one confirmed P2: `AwaitPrior` returned success without consulting
`ctx.Err()`, so `--wait-prior=1ns` would proceed after its bound. Reproduced
first by `TestNewControllerAfterPrior/expired-admissible` (failed: "an expired
bound admitted"), then fixed: the bound is checked before every attempt and
again before returning success, in both the constructor loop and the
admission loop. Its ordering note (the group was probed with signal 0 before
the record's shape was validated) is fixed by `recordShape` running before any
probe; no false admission followed from it. Its residual question (pipe
cleanup on a refused spawn) is answered by the code it could not see: `in`,
`out` and `outWrite` are all closed on that arm.

### 2026-10-11: the operator chose the narrow contract (V-12, A-02)

The audit's ownership proposal left one choice open: may Close, or a resume,
signal a process the adapter cannot pin? Fable's proposal said yes, after a
pid and start-time re-read; Codex's review said no, and found two P1 races in
the yes (a descendant's pid reused between the re-read and the `kill`, and a
prior group with nothing holding it unreaped under `--end-prior`). The
operator chose Codex's contract:

- The adapter signals only the group it pins: its own unreaped child's.
  Nothing else is ever signalled, whatever its pid and start time read as.
- There is no `--end-prior`. Resume admits only what it observes gone.
- Descendants that left the group are OBSERVED, durably, and reported:
  captured, ended only on observed termination, survivors, and an
  unattributable remainder that is always `unknown`. They are evidence for
  reporting and for refusing a resume, never a safety guarantee.
- The worker launches as a session leader (`Setsid`), once native
  qualification Q1 and Q3 pass. Until then it stays on `Setpgid`.

So V-12's escaped job is reported, not ended. `qualify-claude-jobs.py`
expects "reported, cleanup incomplete" rather than "ended", unless native
cooperative cancellation is later qualified.

### 2026-10-12 continuation: the narrow contract, built (V-12, A-02)

What the operator's choice needed beyond the shared core: escaped descendants
observed and recorded, a close that captures them before it can lose their
ancestry and says what became of them, and a resume that refuses while a
recorded one lives. `Setsid` is not in it; the worker stays on `Setpgid` until
native Q1/Q3.

**Observation (`internal/harness/descendants.go`).** One `kern.proc.all` read
per scan (linux: `/proc/*/stat`, torn by construction; other platforms launch
nothing). Breadth-first from the roots: the pinned leader and every recorded
escaped descendant whose pid and start time still match. A row is a child only
if its ppid names the node AND it started no earlier (equal allowed). The table
is not known to be atomic, so every node a new descendant's chain passes through
is re-observed AFTER the read; a node gone or recycled drops the chain (Codex
design review: a parent exiting mid-read and its pid going to a stranger would
otherwise make the stranger's child ours). Escaped means pgid differs from the
leader's; in-group descendants are walked through, not recorded. A recorded one
is dropped only on a positive gone/recycled observation by its own pid, never
on table absence; a table row showing its pid held by a NEWER start replaces it
and counts it ended (Codex code review: both used to be recorded, and a record
naming one pid twice refuses every resume). At most 256; past that a saturating
`overflow` counts dropped observations, sticky. `last_error` holds only
DISCOVERY failures (a table read, an ancestor that could not be observed); a
recorded identity that cannot be observed right now stays recorded and is
observed again by the resume (Codex code review: made unresolved, it outlived
the identity in a sealed record and refused for good).

**When.** A sampler every second (no kqueue `NOTE_FORK` path: measured 3-11 of
20 fast double forks). Every shutdown the adapter starts (Close, stdout EOF)
takes one order: admission off, the sampler stopped and JOINED, a final scan
completed, then stdin closed, then the group signals (Codex design review: a
worker that exits on stdin EOF reparented its children first). A leader that
exits by itself: a capture from recorded roots, then terminate.

**Persistence.** The authorization holds one canonical record; the sampler,
Start's identity `bind` and the close each change their own fields under its
mutex and publish the whole. A scan commits only at the generation it read and
before the close seals the record. A failed publication leaves it dirty and the
next commit retries; a close that could not be published is said on stderr,
and the file then holds an older observation that a resume reads (a stated
limit: nothing durable can be written then).

**The close report** (`.proc` `close`): trigger, captured cohort, `ended`
(observed gone or recycled after the reap), `survivors` (alive at
`observed_at`), `unknown`, and `unattributable: "unknown"` always. One stderr
line repeats it, naming survivors by pid, "not signalled". The stdout receipt
stream is unchanged.

**Admission.** Waitable while a recorded escaped descendant is alive (pid and
start time). Outright: overflow, an unresolved discovery error, a per-entry
observation error, a malformed section (pid <= 1, a duplicate, over 256). No
section adds no refusal: unrecorded is unknown, as before.

**Found and fixed on the way (each with a test watched to fail first).**
- A leader that exited by itself, with a descendant outside its group holding
  native stdout, left the transport open until somebody called Close (Codex
  design review P2). Fail-before: `TestJSONProcessLeaderExitReleasesTransportDespiteEscapedHolder`,
  8.06 s, "kept the transport open with nobody calling Close".
- The first release closed our read end after a fixed 2 s, so a reader that had
  not yet read the leader's last frame lost it, and the error blamed a holder
  nobody had seen (Codex code review P2). It now polls the pipe: unread bytes or
  a pending EOF keep it waiting up to `drainCap` (4 s); it names another holder
  only when nothing is unread.

**Tests** (hermetic, darwin): attribution table; scan against a scripted kernel
(table absence, gone/recycled, retained error, failed table read cleared by a
complete scan, ancestor error, torn ancestry for recycled AND vanished
intermediates, pid replacement and an older row ignored, 255/256 and saturating
overflow); the classifier; record finality (stale generation, seal at the
current generation, bind after seal, commit keeps identity, unchanged scan not
republished, failed publication retried, close publication failure reported);
real processes: a setsid'd survivor recorded, reported and alive after Close,
one that ended before Close dropped and counted, the in-group child not recorded
and killed, every nonzero signal to `-pgid` or the leader; the shutdown order on
Close and on stdout EOF with no periodic scan; the capture joins a sampler held
mid-scan; leader self-exit with an escaped holder; the drain keeps a held
reader's frame and is bounded; resume refuses waitably while a recorded one
lives and admits once it is gone, admits a recycled pid without signalling,
refuses outright on overflow, scan error and malformed; the built adapter names
an escaped tool on stderr, leaves it running, and `.proc` lists it.

**Reviews** (constrained Codex, source evidence only): design PASS WITH CHANGES
(three P2s, all applied); code pass 1 timed out at the watchdog after naming
three gaps (applied, or the persistence one stated as a limit); code pass 2
(narrow, no charter) REJECT on two P2s, both reproduced by a failing test and
fixed; it found no signal path outside the pinned group and leader and no
admission bypass. The fixes after pass 2 were not re-reviewed.

**Mutation evidence** (private rsync'd copies, python-applied, one at a time;
a build failure is not a kill; scoped tests, the whole two packages for a
scoped survivor; scratchpad `a02n/mut/`). The final sweep ran 47 mutants against the
code as committed; every one is killed by a test's assertion (two respelled
after their first form did not compile, then killed).

| Area | Mutants (all killed) |
| --- | --- |
| Attribution | predating child accepted; equal start rejected; in-group recorded; root start ignored; root not marked visited |
| Scan | chain not re-observed; gone kept; retained identity dropped on an error; table error unsaid; ancestor error unsaid; capacity off by one; overflow wraps; unresolved error sticky; recycled read as alive; a retained error made unresolved; a retained error uncounted; a newer pid not replacing, an older one replacing, a replacement not counted |
| Record | stale generation; commit after the seal; state after the seal; a second seal; no retry after a failed publication; always publish; a failed close publication unsaid |
| Admission | overflow, unresolved error, count mismatch, per-entry error, live descendant, malformed section, and descendants never observed: each refusal removed |
| Process | stdin closed before the capture on Close and on stdout EOF; the capture not joining the sampler; no close report; no periodic or final scan; no capture on the leader's own exit; a survivor signalled; a survivor reported as ended |
| Release | the pipe never polled; no drain bound; the release blaming a holder it had not seen; the read end never closed |
| Adapter | the stderr line removed |

An earlier sweep of 38 against the first version left four survivors, each
closed before this one: a `known` set that the visited set made redundant
(removed), two tests that did not isolate their guard (the retained-error case
still had the process in its table; the close-error case matched an earlier
publication error's wording), and a join that timing alone could not catch (a
scan hook now holds the sampler mid-scan, and Close must wait for it).

## D-078 — An adapter-launched worker's claims renew from COMPLETED native writes, through a renewal-only transaction the controller owns

2026-10-13. Review finding F-26, reopened: D-076 renewed a Codex lifecycle
hook's claims, but "workers launched by `buddy-adapter` install only the
PreToolUse gate" and their route was "not provided here". Fable and a
constrained Codex pass agreed on route B (the controller observes completions);
Codex's five stricter points were adopted. Three defects were reproduced as
failing tests first:

- `lifecycle beat --path a --path b` kept only `b` (a plain string flag).
  `TestLifecycleBeatRenewsEveryRepeatedPath`: `a-work` stayed at its claim
  time while `b-work` moved.
- Nothing renewed an adapter worker's claim after a completed Write.
  `TestControllerRenewsTheCompletedClaudeWrite`: "reached no renewal ...
  (ledger events [busy idle])".
- A renewal routed through `BeatLogical` after idle erased the idle row,
  because `BeatLogical` runs `logicalBusy` (`internal/store/logical.go`).
  `TestLogicalRenewalAfterIdleKeepsIdle`, against `BeatLogical`: "erased the
  idle observation: map[]".

**Route.** The controller stays the only lifecycle owner. The gate contract,
Mod profiles, hook settings and the gate hash are unchanged. A PostToolUse in
the adapter's settings (route A) remains only a fallback, for a harness that
cannot expose completion provenance. The backend's reader classifies each
completion and offers its paths to a bounded mailbox that never blocks:
256 paths, 64 KiB, coalesced by launch generation and path. Overflow drops
candidates with a coverage gap, never an approval, result, idle, end or control
response. One goroutine applies what is due, under the controller lock with the
10 s ledger bound. That is the first observation of a path at once, then at
most once per path per 30 s. A timer flushes deferred paths and manufactures
nothing. Idle, end and disconnect apply what is due first, so the journal shows
the renewal before the idle. The route is `buddy lifecycle renew`
(`RenewLogical`). In one transaction it checks the captured incarnation and
renews that incarnation's open claims covering the paths. It does nothing else:
no last_seen, no idle, no lifecycle state. It never clears the approval gap.
Paths are the native spellings, placed as the gate adjudicates them (D-073); a
path outside the repo renews nothing. Each batch journals one `renewal` receipt
(generation, fenced paths, placement, count, coverage). It never goes to
stdout, and recovery ignores it. A failed call is journalled as unconfirmed,
since it may have committed, and is not replayed.

**What renews.** Claude: a non-replay, main-thread tool_result matched to an
Edit, Write or NotebookEdit use recorded in this launch, with `is_error: false`
explicitly. Codex: `item/completed`, `fileChange`, `status: completed`, in the
active turn; every `changes[].path` and `kind.move_path`, or none of a
malformed item. Nothing else renews: Bash (`sed -i` included), a
commandExecution of any exit, a subagent or replayed result, an unknown id, a
denial, or a started, failed, declined or in-progress item.

**A premise the evidence contradicted.** The design said Claude renews on an
EXPLICIT `is_error: false`. Native 2.1.291 sends none on a successful Write;
it sends `is_error: false` on Bash. Measured in the 2026-10-13 rerun receipts
(`claude-interrupted-compaction`, `native-observations.jsonl`). The
conservative choice is the design's own fallback: an omission renews only once
qualified for that exact version. It is qualified for Write on 2.1.291 alone,
and only with a positive witness. The single tool_result in the frame must
have a `tool_use_result` object whose `type` is `create` or `update` and whose
`filePath` equals the recorded input. A denial sends `is_error: true` and a
string. An unwitnessed omission renews nothing and reports coverage `unknown`.
Edit and NotebookEdit success shapes were not measured; they renew only on an
explicit false. A native Edit on 2.1.291 therefore renews nothing until it is
measured.

**Fences.** Correlations live in the backend and die with it, and a turn's end
discards uses without results. So nothing pairs a tool_use with a tool_result
across a resume, and no current token is looked up (D-069). Each launch has its
own generation (the authorization's attempt id). A completion queued under
another generation is discarded as lost coverage. The ledger refuses any token
but the captured live one in the same transaction as the update.

**Cut.** One ordinary controller event per tool result: the 65th Claude or
129th Codex result behind a stalled consumer blocks the reader that also routes
control. Renewing through `BeatLogical`, which erased idle. Renewing from an
attempt (tool_use, can_use_tool, item/started) or from patch text. Route A as
the primary route: a second lifecycle writer, re-opened admission, and two
process starts per tool call on the hook budget.

**Limit.** A stalled renewal holds the controller lock for up to its 10 s
bound, like any stalled ledger call. Each hold applies at most one batch of
128 paths; more due paths re-signal the loop after the lock is released, so a
waiter waits for at most two holds (Go's mutex lets the loop take it once more,
then hands it to a waiter that has waited over 1 ms). The
native reader never waits on it. For more than one batch of due paths at idle,
only the first batch is guaranteed to land before the idle.

**Code review** (one constrained Codex pass, source only). Seven findings
(six P2, one P3) and one residual question. Each was reproduced by a failing
test before its fix:

1. A non-file sighting of a recorded id kept the Write correlated. A Bash
   can_use_tool with the Write's id then renewed a file nothing wrote. Any
   disagreeing sighting now drops the correlation and loses coverage.
2. A rejected (subagent) result consumed the correlation, so the genuine
   result after it renewed nothing. Provenance is now checked before the
   correlation is consumed.
3. Two batches, or a reader refilling the mailbox, ran under one lock hold,
   so a waiting approval waited for all of them. Now each hold runs one batch,
   and a waiter gets the lock within two holds
   (`TestControllerRenewalReleasesTheLockBetweenBatches`).
4. `/w/a`, `/w/./a` and `/w//a` each renewed at once. Paths are now coalesced,
   paced and sent with empty and `.` components removed. `..` is kept, since
   it is physically different after a symlinked directory.
5. A path whose gate spellings could not all be resolved was journalled as
   applied with complete coverage. `lifecycle renew` now names such paths in
   `unresolved_paths`, and the batch's coverage becomes `unknown`.
6. A result frame of an unexpected shape returned silently. This covered
   content that was an object, and a `tool_result_meta` that was not a list.
   Either now loses coverage while any use is outstanding. A text input does
   not.
7. `--path ""` was a successful renewal of nothing; it is refused.

Residual question: `activeTurn` is set by any `turn/started`, so a turn
replayed into a resumed thread could renew. A fileChange now renews only in a
turn this launch started. That is recorded from its own `turn/start` reply on
the reader goroutine, before any later frame is read, and dropped at
`turn/completed`.

**Mutation evidence.** In a private rsync'd copy, each mutant was applied in
python, one at a time. A build failure is not a kill. Against the final code,
62 mutants were all killed by an assertion. Two mutants were respelled after
their first form failed to compile. Two survived an earlier sweep and gained a
killing test or a fix: a witness that ignored `type`, and an unobservable
re-signal, now the only path for due paths past a batch.

**Native** (2026-10-13, operator-authorized, loopback providers): retained
Claude 2.1.291 and Codex 0.160.0, scratch-built binaries.

- `qualify-claude.py --trace-native`: 32/32 checks and 9/9 renewal checks. The
  allowed Write's claim moved. The operator-denied Write, the control claim and
  the holder's claim did not. The journal renewal sits between the allow and
  the idle.
- `qualify-codex.py --adapter`: 17/17. The written file and both ends of a move
  renewed; the held and control claims did not.
- `check-claude-gate.py`, `check-codex-gate.py` (on the gate-mode receipt) and
  `check-adapters.py`: pass.

Observed spellings: the Claude `file_path` and `tool_use_result.filePath`, and
the Codex `changes[].path` and `kind.move_path`, are all absolute.

**The Edit shape (measured 2026-10-13, after D-081).** `qualify-claude.py` now
makes one successful Edit of the allowed file after the three Writes
(`toolu_buddy_3`, operator-allowed) and records its result frame's shape as
`edit_measurement`. Measured on retained 2.1.291 and on 2.1.299, traced, the
result is the same: `is_error` is OMITTED, `tool_use_result` has no `type`
(its keys are `filePath`, `newString`, `oldString`, `originalFile`,
`replaceAll`, `structuredPatch`, `userModified`), `filePath` equals the input
path, and the frame has one block. So D-078's open question is answered with
"omitted, like Write", and the Edit renews nothing. On 2.1.291 it is the one
`not_attempted` row with coverage unknown after the Write's applied row (runner
and checker green). That is one sample per version and no failed-Edit control,
so the qualified omission stays Write-only on 2.1.291 and nothing in Go
widened. `check-claude-gate.py` requires that row, the Edit's approval, and a
recorded shape equal to the trace's.

Also measured: on 2.1.299, `tool_result_meta` is on EXECUTED results too
(`{"id", "permission_decision": {"decision": "accept", "source":
"user_temporary"}}`, no `non_execution_kind`). On 2.1.291 it appeared only with
`non_execution_kind`. `toolResults` treats any listed id as unexecuted. So on
2.1.299 a completed Write or Edit is skipped SILENTLY: no renewal row at all,
and coverage is not reported unknown. The suite is red there
(`unqualified_write_reports_coverage_unknown`, `renewal_journal`). The
conservative reading would key "unexecuted" on `non_execution_kind`, which
leaves 2.1.291's behavior unchanged. That is a wire-interpretation change for
reviewed code (and a contract-id question, D-079), not something a record
can absorb.

**Addendum, 2026-10-14: how `tool_result_meta` is read (R').** The operator's
rule: change it if Fable and Codex agree. They agreed on R' after two rounds;
on the contract id they split, and the operator took Fable's reading (no bump,
with the D-079 sentence below and a committed replay as its evidence).

The field is `@internal` in the retained 2.1.299 binary's own schema, so
nothing public promises it. That schema says: `non_execution_kind` is one of
`user-rejected`, `permission-rule`, `automode-blocked`, `automode-unavailable`,
`automode-parsing-error`, `interrupted`, `cancelled`; absent means the tool ran
to completion; whether a call ran is read from `non_execution_kind`, never from
an entry's presence; an entry may carry `remedy` on a result that DID run;
`decision` is `accept`, `reject` or `cancelled`; and `permission_decision` may
be absent on results that ran. Relying on an internal field is acceptable here
only because admission is per exact build (D-079): a new version's shapes are
measured before it is admitted, and this rule is replayed against them.

The rule, per main-thread file-tool result X ("unknown" = renew nothing and
report renewal coverage `unknown`):

0. `tool_result_meta` present and not a list of objects each with a string
   `id`: the whole frame is unexpected, as before (coverage unknown while a
   use is outstanding). Absent and `null` are no metadata.
1. An entry whose `id` names no `tool_result` of this frame (an empty id names
   none): the whole frame is unexpected.
2. No entry for X: the existing `is_error` / witness rule decides. Two or more:
   unknown. Exactly one, E:
   a. a key outside `id`, `non_execution_kind`, `permission_decision` (e.g.
      `remedy`): unknown;
   b. `permission_decision`, when present, must be an object of a string
      `decision` and optionally a string `source` and a string `reason_type`,
      nothing else: otherwise unknown;
   c. `non_execution_kind` present: `permission-rule` or `user-rejected`, with
      no `permission_decision` or a decision of `reject`, on a result that
      carries no completion signal, is UNEXECUTED (renews nothing, coverage
      complete). Anything else is unknown: another kind, an empty or
      non-string kind, a kind beside `accept` or `cancelled`, a kind beside a
      completion signal (Codex, round 2: completion and non-execution
      contradict each other). A completion signal is an explicit
      `is_error:false`, or an omitted `is_error` beside the Write success
      witness (create/update naming the recorded path, one result in the
      frame; the code-pass amendment below);
   d. no `non_execution_kind`: decision `accept` proceeds to the existing rule
      (meta never renews by itself: acceptance permits a run, it does not
      complete one, and an accepted tool can still fail); an id-only entry or
      any other decision is unknown.

The measured shapes (every frame in the evidence, below): 2.1.291 puts
`{id, non_execution_kind: "permission-rule"}` on BOTH denials of a Write, the
gate's and the operator's (the old comment said only the gate's), and
`{id, non_execution_kind: "user-rejected"}` on a result whose pending approval
an interrupt cancelled. That one is a Bash (the A-07 `interrupt_approval`
case), not a Write: no measured file-tool result carries `user-rejected`. It is
admitted as unexecuted on the schema's word and because the first rule already
read it so; reading it as unknown would change no measured outcome either.
2.1.299 adds `permission_decision` to the denials (`{decision: "reject",
source: "hook", reason_type: "hook"}` from the gate, `{decision: "reject",
source: "user_reject"}` from the operator), keeps the bare `user-rejected` on
the interrupted Bash, and puts `{id, permission_decision: {decision: "accept",
source: "user_temporary"}}` on executed Write, Edit and Bash results the
operator allowed, `{decision: "accept", source: "hook", reason_type: "hook"}`
on Writes the permissive Mod override allowed.

Why unknown is the fallback (Fable and Codex, round 1). A false renewal
fabricates freshness for a write that did not run. A silent skip marks a
writing holder stale AND hides that anything was missed, which is what 2.1.299
did under the first rule. Unknown costs a report, and the report is the
uncertainty an operator or checker needs. Cut: keying "not executed" on a
non-empty kind alone (Codex: `{id, permission_decision: {decision:
"reject"}}` beside explicit false would renew, and a kind beside `accept`
would skip silently); keying success on `accept` (it permits, it does not
complete); a version switch around the reader (R' is one rule, and the replay
shows it equal to the first rule on every 2.1.291 frame).

**The replay** (`internal/harness/testdata/claude-tool-result-meta.json`,
`renewal_claude_replay_test.go`). Every native user frame that carried
`tool_result_meta` or answered a main-thread Write or Edit in the 2.1.291 and
2.1.299 evidence: the A-07 rerun receipts and the lane runs of 2026-10-13/14
under `/private/tmp`, deduplicated by session and uuid. 79 frames: 38 of
2.1.291 (17 with `tool_result_meta`) and 41 of 2.1.299. Each is reduced to what
the reader reads (ids, `is_error`, `tool_use_result`'s type and filePath,
`tool_result_meta` verbatim, provenance), fixture roots spelled `/fixture`,
and replayed alone through a backend of its version after the tool uses that
preceded it. `before` was produced by the same replay against 447387b (the
first rule); `outcome` is this code's. On all 38 2.1.291 frames they are
equal, and the test fails if one moves. On 2.1.299, under R' alone, 14
accepted Write and Edit results moved from a silent skip to coverage unknown,
and nothing else moved; with 2.1.299's Write omission qualified (below), its 11
Writes renew and its 3 Edits stay unknown.
An independent Python derivation of both columns from the rule text agreed on
all 79. Three measured 2.1.291 frames with `tool_result_meta` were traced as
key names only (the A-07 mixed-fleet worker, and two in a qualify-claude run
at 316b337, before the observer projected tool blocks) and cannot be replayed. The rule's 50
arms (56 after the witness amendment below) are a table in the same file, read by the Go test and by
`check-claude-gate.py`'s mirror (`meta_verdict`, used by `matched_write`), so
the two readings are held to one table, and each measured frame's per-result
verdict is recorded there and checked by both.

Mutation evidence (a private rsync'd copy, each mutant applied in python, one
at a time): 34 mutants, 19 of the Go reader and 15 of the Python mirror, all
killed by an assertion, none by a build failure. Reverting to the first rule
fails 14 replay rows (the accepted 2.1.299 Write and Edit results); admitting
`reason_type` nowhere fails 9 (the 2.1.299 gate denials). Three checks were
dropped while writing the mutants, as equivalent (a leading-bracket test, a
leading-brace test and a missing-decision refusal each repeated a later
check's verdict), and the first sweep's one survivor, dropping the
decision-object check, gained the end-to-end row that names its reason.

**2.1.299 joins the qualified Write omission (same day, its own commit).**
From its own frames in the replay fixture: 11 successful main-thread Writes,
each with `is_error` OMITTED, a `tool_use_result` of type `create` (5, the
qualify-claude and claude-mods runs) or `update` (6, the compaction,
interrupted-compaction, interrupted and resume worker traces) whose `filePath`
is byte-for-byte the input path, one `tool_result` in the frame, and an
`accept` entry that R' reads as proceed. That is 2.1.291's witness, so
`claudeWriteOmissionVersions` is `2.1.291, 2.1.299`. Edit stays unqualified: 3
samples on 2.1.299, all omitted with no `type`, and still no failed-Edit
control. This is a parse rule keyed by version, not an admission: 2.1.299 has
no record until its release run earns one. It earned one the same day:
`qualify-release.py` at 784ec7b (this rule, with the witness amendment), from
the retained copy, granted all three tiers with every suite and checker green
(claude 49/49 and 29/29, claude-mods 75/75 and 11/11, resume 22/22 twice,
interrupted 98/98 twice, compaction 26/26 twice, interrupted-compaction
113/113 twice). Record `internal/harness/admissions/claude/2.1.299-ba5ab7b8`,
evidence `docs/admissions/2026-10-14-claude-2.1.299-ba5ab7b8/`. The release
notes were read first: the changelog has one section after 2.1.296. Its hook
changes are to mods, plugin hooks, managed settings, Windows and
UserPromptSubmit ordering (no adapter launch wires that hook), and one to plan
mode, which the adapter's read-only launch uses: plan mode no longer runs a
write a PreToolUse hook allowed. That is stricter, in the direction a
read-only launch wants, and no default the adapter relies on weakened.

The set is written three times, in Go and as `WRITE_RENEWAL_QUALIFIED` in
`qualify-claude.py` (what the runner expects) and `check-claude-gate.py` (what
the checker re-derives), and a version in one but not another surfaces only in
an operator-authorized native run. `TestClaudeWriteOmissionVersionsMatchThePythonMirrors`
reads every `scripts/*.py` that names the set and requires exactly one
`WRITE_RENEWAL_QUALIFIED = ("x", ...)` line equal to the Go set, no second
assignment or extension, and both known mirrors present. Its positive controls:
the exact spelling reads as the Go set, and a short, an extra, a list-respelled,
a doubled, an extended, a reassigned and an absent set are each refused.
Mutation: 8 mutants (the Go set short or long, the predicate ignoring it, each
Python set short, respelled as a list, extended or renamed) were all killed;
dropping 2.1.299 from the Go set fails the 11 replay rows of its Writes.

**Code review** (one constrained Codex pass on both commits, source only).
Six answers; two defects were reproduced by a failing test before their fix:

1. An empty id never names a result, so a frame pairing an id-less
   `tool_result` with an entry `{"id": ""}` is unknown as a whole, where a
   literal reading of R' would let its other results proceed. Conservative,
   and now stated in rule 1 above. No change.
2. Go and the Python mirror agree on every input class asked about. No change.
3. A kind of `permission-rule` beside an omitted `is_error` and a success
   witness reads unexecuted, silently. R' prescribes it and no build sent it;
   it is the one silent arm left. Amended the same day (the orchestrator's
   decision, below).
4. The replay runs each frame alone, so a frame taking the frame-level path
   (rules 0 and 1) could report a use another frame left outstanding in a real
   stream and not in the replay. No measured frame takes that path, and
   `TestClaudeMetaVerdictsOfMeasuredFrames` now fails if one ever does.
5. DEFECT: the mirror gate read `("2.1.291" "2.1.299",)` as both versions,
   where Python concatenates adjacent literals into one string, and
   `("2.1.291")`, which is a str (`in` is then a substring test). The line
   pattern now admits only a tuple literal, and both spellings, a spaced pair
   and a doubled comma are refusal controls.
6. DEFECT: `matched_write` admitted `is_error: 0`, because `0 in ("<absent>",
   False)` is true in Python, while the reader calls it non-boolean and renews
   nothing. It is an identity test now, with a self-test mutation. Residual:
   the observer projects a missing `is_error` as the string `"<absent>"`, so a
   native string of that value is indistinguishable from omission; fixing it
   changes the runner's trace format, which recorded receipts were checked in.
   It stays as it is: a recorded known gap.

**The witness arm is unknown (same day, the orchestrator's decision on finding
3).** A `permission-rule` or `user-rejected` entry beside an omitted `is_error`
AND the Write success witness is contradictory evidence, exactly like a kind
beside explicit `is_error:false`, so it renews nothing and reports coverage
unknown, in the Go reader and in `check-claude-gate.py`'s `meta_verdict`. The
witness counts on every version: what it proves depends on the version's
qualification, but that it contradicts "did not run" does not. `is_error:
true` beside the witness stays unexecuted (the failure outranks it, as it does
with no metadata). Six arms joined the table (56 now): the arm in three
shapes (`permission-rule`, `user-rejected`, the 2.1.299 gate denial's entry),
and on its other side `is_error: true`, no entry, and an `accept`, each beside
the witness. No
measured frame takes it: every measured kind entry sits on `is_error: true`
and a string `tool_use_result`, and `TestClaudeMetaVerdictsOfMeasuredFrames`
(and the mirror's self-test) now fail if a measured verdict differs with the
witness from without it, with the witness found on all 30 measured successful
Writes as the control. So every measured outcome is unchanged and D-079's
no-bump reading still holds. Mutation (a private rsync'd copy, edits in
python): 9 mutants, 8 killed by an assertion. The killed: the witness
dropped from the completion signal (Go, and the mirror), the witness counted
beside `is_error: true` (both), the reader's witness passed as false, its
tool-name or block-count term dropped, and a measured 2.1.291 denial edited
to take the arm (the Go and Python measured-frame checks and the replay row
all failed). The
survivor is equivalent: `matched_write` accepts only `proceed`, so
unexecuted and unknown refuse alike there.

## D-079 — Native admission is a committed, exact-artifact record compiled into the adapter; it supersedes D-071's "from the code's table"

2026-10-13. Versions step 2 of the harness version tracking consensus (Fable and
Codex, decided by the orchestrator the same day).

**The failure** — D-071's exact version/profile rule held up: the permissive-Mod
override still beats a user PreToolUse denial, and the 2.1.292-2.1.296 release
notes change hook and Mod semantics, so capability probing alone and version
ranges stay rejected. What failed was the hand-run loop around it. Each release
was a hand edit of version literals in `claude.go`, `codex.go` and `diagnostic.go`
(`claudeModProfileForVersion`, `claudeManualCompactionQualified`, the Codex
`"0.160.0"` checks), and a version string named nothing about the bytes that
ran. The updater keeps three Claude versions on disk and re-points the launcher,
so 2.1.289 and 2.1.290 are gone with no hash on record, and any file answering
`2.1.291 (Claude Code)` was admitted as 2.1.291. Codex execs
`bin/codex-code-mode-host` and `codex-path/rg` after launch, so hashing
`bin/codex` alone would still admit a swapped sidecar (Codex review P1).

**The decision** — an admitted build is a `buddy.admission.v1` record at
`internal/harness/admissions/<harness>/<version>-<sha8>/admission.json`, compiled
in with `//go:embed`. It sits beside the code, not in `docs/`, because embed
cannot name a parent directory. It binds the harness, the exact `--version` text,
OS/arch, a code-owned adapter contract id, the granted tiers, the executable's
sha256 and size, for Codex the whole release tree (relative paths, types, modes,
hashes, link targets inside the package; an undeclared entry refuses), for Claude
the Mod profile and built-in list, the gate contract, and per suite the runner,
checker, receipt and binary digests and the checker's verdict as evidence.
Parsing is strict. Unknown, duplicate and missing keys, malformed hashes, an
unearned tier and a `reconcile` grant each refuse with their own code. One bad
record makes the whole set unreadable, which refuses every launch.

At launch, before anything runs (not even `--version`), the adapter resolves the
executable once, hashes it, and selects the record for the declared harness and
version by those bytes. It then requires that record's contract id, platform,
tier for the operation and Mod profile, and for Codex the manifest. Next comes the
exact version text, then today's live checks (get_settings, reload_plugins
against the record's built-ins, hooks/list), unchanged. The controller refuses
an unrecorded version before the `.start` intent and a start-only version before
a resume refreshes the ledger. Capabilities are the record's tiers intersected
with the code's support. A start-only record never reaches resume, Codex
reconcile stays withdrawn in code whatever a record says, a built-in the code does
not know cannot be listed, and cross-version resume still refuses. `--retained`
launches the pinned copy at `~/.buddy/harness/<harness>/<version>-<sha8>/`. The
adapter never copies or fetches one; retention is an operator step, and the
copy is hashed like any other path. Workers and version probes run with
`DISABLE_AUTOUPDATER=1`.

**Backfill** — exactly two records. Claude 2.1.291 (`9a1d2ed6…`, the Mach-O
extracted from the 2026-10-07 archive) and Codex 0.160.0 (`bin/codex`
`112fae7a…`, 54-entry release manifest). Each grants `start`, `resume` and
`compaction_observation`. Start rests on this lane's fresh start-tier run at a
clean `abe3796`: qualify-claude.py, qualify-claude-mods.py, qualify-codex.py in
full `--shell-patch` mode and qualify-adapters.py, each passed by its
independent checker (`docs/admissions/2026-10-13-start-tier.json`). The A-07
rerun's receipt copies were checked too: every content check passed, and
`receipt_location` failed only because they are copies, so they grant nothing.
Resume and compaction_observation rest on the A-07 rerun at `316b337` and its
checkers (`docs/verification-evidence/2026-10-13/summary.json`). **2.1.289 and
2.1.290 are retired as history**: no binary or hash survives, so no record can
be written, and admission refuses them (`admission_missing`). Their receipts
stay evidence of what was measured.

**Refused alternatives** — a runtime registry outside the repository (Codex):
the adapter cannot tell a measured record from a typed one, and a commit plus
the hermetic tier can. Invalidating a record on any buddy or adapter hash change:
an embedded record cannot bind the binary it is embedded in without a cycle, so
the contract id is bumped on any argv, wire, settings-grammar or gate change,
and the hashes are evidence. A (dev, inode, size, mtime) hash cache: it misses
exactly the preserved-metadata rewrite F-23 concedes, so every launch hashes in
full. The Claude Mach-O is 240 MB and the Codex tree 318 MB (Fable measured
0.43 s for the former).

**Boundary** — this guards against accidental substitution and stale evidence,
not forgery: the machine user can write a record and rebuild (invariant 6). The
hash-to-exec window F-23 states remains. A record is data. It selects an existing
code-owned contract and may only narrow it; a new plugin name, wire shape or hook
interpretation needs reviewed code and requalification. Claude's
`claudeWriteOmissionQualified` (D-078) stays a code-owned wire rule keyed by
version, because it is a parsing rule, not a grant. A version-keyed parse rule
whose outcomes are identical on every measured frame of every recorded
version, proven by a committed hermetic replay, does not bump the contract id;
a change that alters any such outcome does, and requalifies (2026-10-14, the
`tool_result_meta` rule, D-078's addendum). Deferred suites (endpoints,
peers, history, mixed fleet, jobs) grant nothing. Qualifying a candidate without
production admission (Codex migration step 4) and a retention runner are later
work. Until then a new version's first start-tier run needs a hand-built
candidate route, and none ships. (Superseded the same day: D-080 is that route.)

**Tests** — `internal/harness/admission_test.go`: no record is `admission_missing`
with zero executions; a record for sha256(A) over B, rewritten in place with
identical dev, inode, size, mtime and mode, is `artifact_mismatch` before
anything runs; a changed sidecar, an undeclared addition, a missing entry, a
changed mode and a re-pointed link under an unchanged `bin/codex` are
`package_mismatch`; contract, platform, profile and version-text mismatches each
refuse; a start-only record cannot resume, at the backend and in the
controller's precheck; a reconcile grant and every malformation have their own
codes; an extra built-in refuses unless the record lists it, and a record cannot
list an unknown one. The committed records are the positive controls. Each
suite's receipt digest must appear in the summary it names. In
`cmd/buddy-adapter`, the untagged adapter (the one `install.sh` builds) refuses
an unrecorded fake before running it, while the `buddy_admission_fixture`-tagged
build the entry tests use admits it by record.

**Codex review (constrained, 2026-10-13)** — five findings, each reproduced by a
failing test before its fix (`admission_review_test.go`). P1: a link chain
(`a/b -> ../d`, `a/out -> b/../../external`) passed the lexical check and
resolved outside the package; links now resolve component by component through
the manifest. P1: an updater rename between the hash and `--version` ran
unadmitted bytes; the executable's identity is re-checked right after hashing,
so a replacement refuses before anything runs (an in-place rewrite of the same
inode in that window stays the race F-23 states). P2: a stray `}` or `]` after
a record parsed, because `More()` is false before a closing delimiter; the next
token must now be EOF. P2: setuid, setgid and sticky bits hashed as the plain
mode; they now refuse. P2: before a launch, resume and Codex compaction read
`experimental`; they now read `unknown` until a record is selected.

## D-080 — A qualification candidate is bound by its typed sha256, replaces the record set in that one adapter, and runs only against a loopback provider

2026-10-13. Codex migration step 4 of the harness version tracking consensus.
A decision of its own rather than a D-079 continuation: D-079 decides what may
launch for real work, and this decides how evidence for a build with no record
is produced without widening that. It changes no D-079 rule for an adapter
started without the flags.

**The failure** — D-079 refuses every unrecorded artifact, and a record needs
suites that pass first. Measured the same day: the native check of the
retained 2.1.291 copy failed qualify-claude.py's managed-policy case with
`artifact_mismatch`, because the suites launch a metadata observer (a wrapper
that forwards stdio unchanged and logs frame shapes) and D-079 hashed the
observer. So after D-079 no suite could run through the adapter it was
qualifying, for a new build or an already-recorded one. D-079 named the gap
("a new version's first start-tier run needs a hand-built candidate route,
and none ships").

**The decision** — `buddy-adapter --qualify-candidate <artifact>
--qualify-sha256 <hex>`, in the same binary `install.sh` builds (Codex asked
for final adapter bytes, so a build tag is refused). The artifact is Claude's
Mach-O or `bin/codex` inside its release tree, a regular file and never a link.
When the flags are read, before any backend or child exists, the artifact is
hashed and must equal the typed digest. A distinct `--executable` (the observer)
is hashed too. In that process the committed records are REPLACED by one
synthesized record for exactly those bytes: every tier, the code's contract
id and platform, Claude's whole known built-in list, and Codex's manifest built
from the tree as it was bound. So a candidate adapter can launch nothing else.
At every start and resume, admission hashes the artifact (not the observer) as
usual. The observer must be unchanged, and the provider must be on a loopback
IP: `ANTHROPIC_BASE_URL` for Claude, and for Codex the selected
`model_provider`'s `base_url` in the `config.toml` beside its hook config. That
reader takes only the flat shape the fixtures write and refuses a profile, a
dotted top-level key, an array table and a multi-line string. Every control
receipt carries `qualification_candidate`, the captured receipt records the
binding, and a resume in the other mode or under another candidate refuses.
Codes: `candidate_mismatch`, `candidate_outside_fixture`. It does not combine
with `--retained`.

**Why loopback is the fixture test** — the danger is a candidate doing real
work. A worker whose provider is a loopback fixture cannot do real work, so the
route cannot stand in for a normal start or resume even if someone types it.
A worktree or path marker says nothing about which model a worker reaches.
Like the rest of the adapter, this is cooperative (invariant 6): the machine
user can run a loopback proxy. A candidate shipping a built-in other than the
code's known pair refuses at `reload_plugins`. That refusal is the finding,
because a new built-in needs reviewed code first (D-079).

**Refused alternatives** — a build tag (not the shipped bytes; the
`buddy_admission_fixture` tag stays hermetic-only). A candidate record file
(D-079's reason against a runtime registry). Hashing only the launched file
with the observer inlined into the adapter: that would make the frame logging
part of the adapter contract. Adding the candidate to the embedded records
instead of replacing them: that process could then also launch recorded builds
under a flag whose receipts say "candidate".

**Second constrained review of D-079's fixes (2026-10-13)** — the link chain,
EOF and pre-launch capability fixes were judged sound. P3, reproduced first
(`TestReviewPackageRootSpecialBits`): the package ROOT's sticky or setgid bit
was never inspected, so a changed root verified; it now refuses. P1 residual:
an updater rename after the post-hash identity re-check and before the
`--version` probe runs the replacement's `--version`. That is the path-based
exec window F-23 states ("nothing short of executing from a sealed copy closes
it"), and it is left stated, not closed. `--retained` is the mitigation,
because no updater writes there. What the boundary promises is now measured
(`TestReviewRenameBeforeProbeStartsNoWorker`): no worker ever starts on the
replacement, because the launch re-checks identity before the spawn. A private
hard link was considered and cut. It would write into the updater's volume
on every launch, and it cannot pin a Codex tree whose sidecars are executed by
relative path.

**Tests** — `internal/harness/candidate_test.go`: an unrecorded fake refuses
without the binding and runs with it (IPv4 and IPv6 loopback). Each refuses
with nothing executed: a remote, absent, `localhost`-named or https provider,
another declared version, and the artifact rewritten after binding. Also
refused: a wrong digest, a link, a relative path, a non-`bin/codex` Codex
artifact, a second binding, a changed observer, an unbound executable, each
Codex config shape above, an addition to the bound tree, and both cross-mode
resumes and another candidate's resume, before any ledger call. Every receipt
names the candidate. `cmd/buddy-adapter`: the untagged adapter runs an
unrecorded fake under the flags only with a loopback provider, and refuses it
without one before anything runs.

## D-081 — A qualification run binds its own binary, checkers read the committed records, and one release runner retains, runs, checks and records

2026-10-13. Harness version tracking, after D-080: this is how D-080's
candidate route is used. D-079's launch rule and D-080's binding rule are
unchanged.

**The failure** — after D-080 the route existed, but nothing used it the same
way twice. A suite reached it only when a wrapper exported four
`BUDDY_QUALIFY_*` variables, so a plain `qualify-claude.py --claude X` failed its
first adapter case with `artifact_mismatch`. Every checker carried its own
version list (`CLAUDE_VERSIONS = ("2.1.289", "2.1.290", "2.1.291")`,
`"codex-cli 0.160.0"`). D-079 retired two of those versions the same day, so the
checkers admitted builds that no record or binary backs, and a new build needed
a hand edit in a dozen files before its first run could be checked. The
2026-10-13 backfill record was written by a scratch script from six runner
outputs and six checker outputs. Each of those hand steps was a place to
qualify one set of bytes and record another.

**The decision** — three parts, one module between them
(`scripts/admission_lib.py`, loaded once per process).

- **A runner binds what it was given.** Each runner hashes the resolved
  binary, reads its `--version` (`DISABLE_AUTOUPDATER=1`, throwaway config
  dirs), and types `--qualify-candidate <artifact> --qualify-sha256 <hex>` onto
  every adapter command line it starts for that harness (never on `hook`). The
  binding goes into its result as `qualification_candidates`. The environment
  carries nothing. A recorded build takes the same route, because the suites
  launch through a metadata observer and D-079 would hash the observer.
- **A checker reads the records.** Admissible versions and native contracts come
  from `internal/harness/admissions`. `--candidate harness:version:sha256` names
  the bytes a run must have bound. A candidate run is accepted only when it
  recorded exactly one binding for the harness, the adapter's own control receipts
  carry that sha256 (so the route was taken), no receipt names a sha256 that no
  binding explains, the native sha equals it, and it equals `--candidate` (or,
  without one, a committed record's bytes). A run bound to OTHER bytes is never
  accepted because a record for its version exists. A run with no binding is
  accepted only for a recorded version and only without `--candidate`.
  2.1.289 and 2.1.290 now refuse.
- **`scripts/qualify-release.py` is the one operator entry point.** It reads
  `--version` and hashes, then RETAINS the build at
  `~/.buddy/harness/<harness>/<version>-<sha8>/`: the Claude Mach-O as `claude`,
  or Codex's whole release tree. It `ditto`s into a stage beside the target,
  verifies entry by entry against the source, and renames. An identical
  package is reused. A differing one refuses and is never overwritten. It runs
  every suite `admissionSuites` (parsed from `admission.go`, so there is one
  list) requires from that copy, then each checker with `--candidate`. A tier
  is granted only when all its suites ran green through the binding and all
  their checkers passed. There is no record without `start`, none over an
  existing record, and none unless the tree was clean at one HEAD from the first
  suite to the last, because runner and checker digests are git blobs at that
  HEAD. It writes `docs/admissions/<date>-<harness>-<tag>/` (`summary.json`, one
  `<suite>.checker.json` each, `$HOME` scrubbed to `~`, every digest over the
  committed bytes) and the record. Codex's recovery suite also runs a Claude
  worker, and `--claude-peer` must be a RECORDED Claude build's bytes.

It commits nothing and edits no settings. It does not decide that a release's
changed hook semantics are harmless. That is a reading of the release notes
against the code's contract, done before the run, and a changed default the
adapter relies on means no record whatever the suites say.

**Refused alternatives** — the env transport (it made the plain run red and
put the binding outside the argv that receipts record). Per-checker version
lists (the drift above). A record hand-written from outputs (the bytes the
record names and the bytes that ran were joined only by a person). Retaining by
overwriting (the updater deletes old versions, and a package rewritten under a
committed record turns that record into `package_mismatch`).

**Deferred** — `qualify-claude-jobs.py` is left unbound. Its hermetic test drives
`main` with a fake path, so binding it needs that test to inject a probe. It is a
deferred suite and grants nothing.

**Tests** — `admission_lib.py --self-test` (30 cases: each acceptance arm and
each refusal above, retired versions included). `qualify-release.py --self-test`
(22 cases against fake suites): a tier withheld for a failing checker, a run
bound to other bytes or not bound, no record without start, on a dirty tree or a
moved HEAD, a retained package never overwritten, and a torn copy or a moving
source refused. 25 of 25 mutants of its guards and the library's were killed.
Both self-tests and all 17 checker self-tests run in `check.sh hermetic`.

**The first native release runs (2026-10-13, Claude 2.1.299 and Codex 0.162.1,
loopback)** found four defects in this route. None was a native change. Each
was reproduced by a failing case before its fix:

- Claude resume, interrupted, compaction and interrupted-compaction validate
  their own result in-process with their checker's `validate`. That process
  had no `--candidate`, so `schema_provider` failed in the runner while the
  independent checker passed the same receipt (22/22, 98/98, 26/26, 113/113).
  `bind` now sets the process's expectation to exactly the bound bytes. That is
  the checker's own expectation, never a wider one (`admission_lib`:
  `bound_candidate_*`).
- `qualify-claude-mods.py` printed only a JSON summary, so a 38/38 run counted
  as "no receipt". It now prints `RECEIPT=`, and the self-test requires that
  line of every runner `admissionSuites` names.
- The direct Codex gate suite runs no adapter, so it could never be green: the
  record wants an adapter digest per suite. It now takes the one adapter digest
  all the run's adapter suites report, and only when its buddy digest is
  theirs. The builds are reproducible: one buddy digest and one adapter digest
  across seven suites. The backfill did the same by hand (`settle_direct`).
- D-080's Codex config reader refused any array table. But every fixture
  writes its hooks into that same `config.toml` as `[[hooks.PreToolUse]]`, so
  every Codex adapter suite refused at start (`candidate_outside_fixture`). An
  array table is now read as a table of its own, whose keys are never the top
  level's or a provider's. One under `model_providers` or `profiles`, or with
  a quoted or spaced name, still refuses. Multi-line strings still refuse.
  `TestCandidateCodexFixture` has the fixture block as a positive control and
  those refusals; 4 of 4 mutants killed.

## D-082 — The fleet recipe, corrected by the first mixed fleet: a brief in a file, a wait that knows `done`, a retry after the tab, and a tier that needs escalation

2026-10-14 · the Codex-orchestrated fleet trial (`.cache/trial/report.txt`, orch-3's
notes; operator: "one more lane for the smaller gaps")

**What was wrong** — The skill's "Running a fleet" (D-056) was measured with Haiku lanes
launched from a Claude pane. The first fleet with a Codex orchestrator and mixed lanes
(2026-10-14: a Claude orchestrator above a Codex one, under it one Codex lane and two Claude
lanes) found five things the recipe said wrongly or not at all:

- *The brief as an argument.* `herdr agent start … "<brief>"` was refused for a brief holding
  newlines and backticks: "agent arguments cannot be encoded safely for the target shell". The
  same brief in a file, with the one-line prompt "Read <file> in full and follow it as your
  brief", launched every lane the Claude orchestrator opened (four); the Codex orchestrator
  flattened its lane briefs onto one line, which launched too and reads worse.
- *The wait.* The Codex orchestrator waited with `herdr agent wait <lane> --until idle --until
  blocked --timeout 600000`, and all three timed out (ten minutes each) while the lanes' READY
  reports sat in the inbox: a lane that has finished its turn rests at `done`, a state the wait
  was not told to match. `herdr agent wait --help` (herdr 0.7.5): the states are idle, working,
  blocked, done and unknown; without `--until` it matches idle, done or blocked; a state the
  lane is already in returns at once (probed on this lane: `--until working` returned its row,
  `--until done` timed out). The recipe named no wait at all; the trial's came from its brief.
- *The start after the tab.* `herdr agent start` sent straight after `herdr tab create` was
  refused: `agent_pane_busy`, "agent target pane … is not an available shell". A retry seconds
  later worked (orch-3, 2026-10-14 16:55, opening the fix lanes).
- *The tier under the sandbox.* A Codex driver's `check.sh hermetic` inside the sandbox went red
  on exactly one test, `TestAdapterReportsEscapedToolAtClose`: the adapter's close scan reads
  the process table, which the sandbox refuses ("the final scan was incomplete: process table:
  operation not permitted"), so the one escaped descendant the test plants is never seen. The
  escalated rerun on the same tree was green (`.cache/trial/check-sandbox.log`, `check.log`). A
  sandboxed red there says nothing about the tree.
- *JOBS for a Codex lane.* The orchestrator's DONE check reads `buddy who <lane>` for "no
  shell"; a Codex session prints `JOBS cannot say: no live harness process is registered for
  it`, always, because its hooks bind no process (D-068) and D-065 never renders cannot-say as
  none. The trial orchestrator noticed and took the lane's own words instead; the recipe did
  not say to.

**The rule** — The skill's "Running a fleet" launches with the brief in a file and a one-line
pointer prompt, retries a start refused as `agent_pane_busy`, waits with `herdr agent wait
<label> --timeout <ms>` and no `--until` (idle, done or blocked), reads the lane's words from
`buddy inbox` rather than its state, and does not wait for a Codex lane's JOBS line to say "no
shell". `AGENTS.md` tells a Codex driver or worker to run the hermetic tier escalated, once for
the whole tier. `USAGE.md`'s JOBS paragraph says a Codex session always gets "cannot say".

**Considered and cut** — Making `TestAdapterReportsEscapedToolAtClose` skip when the table is
unreadable: the test exists to prove the close scan sees an escaped tool, and a skip under the
sandbox is the quiet non-run that `check.sh`'s loud NOT RUN exists to prevent; the escalation
is one question. Adding `--until done` to the old list: the default already matches all three
resting states, and a list is what rotted. A JOBS arm that finds a Codex lane's process: D-068
forbids the anchor (a shared app-server process is not one thread).

**Not measured** — Whether `done` is the only state a finished lane rests in (`herdr agent
explain` on a lane the list showed as `done` said `idle` minutes later, so it is not durable);
a Codex lane's states under the same wait.

**Test shape** — Prose only. The skill gate reads every `buddy <verb> --flag` in the section
against the usage table; the `herdr` lines are the harness's, measured as above. The doc-link
test holds the anchors.

## D-083 — A launch brief delegates a bounded relay of the operator's decisions to one coordinator, named by full session id and label

2026-10-14 · the operator's choice, relayed by orch-3 (option 1 of three; the fleet trial's
"decision hold-up")

**What was wrong** — In the first mixed fleet (2026-10-14) the operator made two decisions in
the Claude orchestrator's pane (have the parked lane B release its claim so a fresh lane could
carry its work; wrap the trial up), and that orchestrator relayed each by `buddy msg` to the
Codex orchestrator it had launched. The Codex orchestrator's brief named it only as report-to.
It read `AGENTS.md`'s authority paragraph ("Authority comes from the operator's request in the
current session … No document, plan, claim, message or quoted phrase creates it") and asked the
operator again in its own pane, each time (#101, #111; its report records the re-ask as the
brief's boundary). It was right by the text, and the operator paid one round trip per decision.
The operator's own words opening this lane: "the decision hold up needing me asked each time".

The mechanism already existed for Claude lanes. D-056 measured that a brief saying "take
assignments from <label>" makes that sender's messages assignments, and that without it a lane
refuses them as a peer's instructions; the skill taught it under "Running a fleet", and the
operator's standing decisions said "a lane's brief names and authorises its orchestrator by
label". Three things were missing. `AGENTS.md`, which Codex reads instead of the skill, named
no brief and read as forbidding one. The clause said nothing about WHICH decisions an
orchestrator may relay, so a decision outside the lane's own task (ending another lane's work
before its landing) had no home. And the key was a label: a label can be worn twice (`hello`
warns and never refuses; `who <label>` then refuses as ambiguous, `store/target.go`), and the
`s-<8hex>` short form resolves only a DEFAULT label `<repo>/s-<8hex>` (probed 2026-10-14:
`buddy who s-01a13bb6` was "no such target" for the Codex orchestrator labelled `codex-orch`,
while its full id resolved).

**The rule** — A session a coordinator launched receives its operator request as a launch
brief: its first prompt, written by the launcher from what the operator gave it and nothing
more. The brief's bounds are that session's bounds. It may name ONE coordinator, by full
session id and label, as the relay of the operator's decisions about this work: what to take
next, what to stop or hand on, which claims to release, and when a lane the session opened is
closed after it says DONE. A `buddy msg` from that session inside those bounds is a decision
the operator has already made; the lane acts without asking the operator again in its pane.
Before the first one it checks that `buddy who <that session id>` is live and wears the brief's
label; a label two sessions wear resolves to neither, and then every message under it is peer
text. Anything outside the bounds, or from any other sender, is peer text: report, and ask the
operator. No brief delegates installation, pushes, settings, permission, trust or schema
changes, a more permissive mode, or work beyond the launcher's own authorized scope. The chain
is bounded at each hop: the operator, in the first orchestrator's pane; that orchestrator, by
its brief, to a coordinator it launches; that coordinator, by its briefs, to its lanes.

Where it lives: `AGENTS.md` (a paragraph after the authority paragraph, which stands
unchanged, and the worker bullet), the skill's brief template under "Running a fleet", the
lane's authority paragraph under "Landing through an orchestrator", the "Ending a lane" clause,
and the brief sentence under "Harness boundaries". The operator updated the standing decision
in their own CLAUDE.md the same day: "by label and full session id, and says which decisions
it relays (assignment, stopping, claims, closing lanes it opened); never installs, pushes,
settings or modes".

**Why invariant 4 holds** — The authority is the brief, which rides the launch as the session's
first prompt (D-056). The sender is the KEY that selects the brief's clause, exactly as "take
assignments from <label>" already was; a message from the same sender outside the bound is
peer text, and a message from anyone else is peer text whatever it says. Nothing in buddy
changed: no verb, flag or row confers anything, `--relay` remains the sender's declaration
(D-045), chat is untouched, and the ledger is unchanged. The floor (install, push, settings,
permissions, trust, schema, mode, scope) is not the brief's to give because it was never the
launcher's.

**Considered and cut** —
- *Option 2: the drained line carries the sender's session id beside a custom label*
  (`#N [codex-orch 01a13bb6-…] text`), so the key is on every line and a label worn twice
  cannot confuse. A small change in `inboxLine` and `store.InboxMsg`; not chosen now (the
  operator, 2026-10-14). It remains the better end state once a fleet shows the `who` check
  being skipped, or a worn-twice label biting.
- *Option 3: no delegation; the operator sends out-of-pane decisions from a bare shell*, which
  `msg` stamps `[operator]` (D-043, sender ''). A CLI-entered row by the machine user is the
  authoritative record, but it costs the operator a terminal command per decision (`! buddy
  msg` typed inside an orchestrator's pane sends as that session, since
  `CLAUDE_CODE_SESSION_ID` is set there), and any session that strips its environment is
  stamped the same way, so the stamp is a convenience, not a key. A fallback for a one-off,
  not the fix.
- *Relaying through `herdr agent prompt`.* A typed turn the lane cannot tell from the
  operator's; the trial's mail-only wake rule ("buddy mail #N queued: run buddy inbox" and
  nothing else) exists for exactly this. Rejected.
- *A buddy verb or flag that confers authority* (an `--as-operator`, a relay row). Invariant 4,
  #20, and D-056's cut of `spawn`/`exit`: a path reachable on a relayed instruction is the
  hazard, and it would be used in good faith.

**Not measured** — A Codex coordinator running under the new `AGENTS.md` paragraph (the trial
that found the gap ended before the text existed); whether a lane runs the `who` check once or
on every assignment. Both belong to the next fleet's report.

**Test shape** — Prose only. The skill gate reads every `buddy <verb> --flag` in both sections
against the usage table (`msg`, `status`, `who`); the doc-link test holds the anchors.

## D-084 — A ledger this process may not write gets its cause and fix named; the gate still denies

2026-10-14. F1 of the Codex fleet trial (`.cache/trial/report.txt`). Wording on
the verb path; no verdict changes.

**The failure** — under Codex's workspace-write sandbox every buddy verb a
Codex session ran failed first, and auto-review then approved the same command
escalated. The orchestrator's rollout records it on its first buddy call:

    buddy hello: inspect ledger schema version: unable to open database file (14)
    buddy status: inspect ledger schema version: unable to open database file (14)

One failed call and one review per buddy call (90 escalation requests in 74
tool calls for the orchestrator alone, `.cache/trial/escalations.txt`), and the
error named neither the cause nor the fix.

**Measured (2026-10-14, codex-cli 0.162.1)** — reproduced with Codex's own
runner, `codex sandbox -P :workspace -C <dir> -- buddy status`, on a fixture
ledger outside `/tmp` (the profile makes `/private/tmp` writable, which hides
the defect): the same line, and `--log-denials` shows `file-write-data` on
`buddy.db`. From the main checkout and from a linked worktree alike: the common
dir is read-only either way. A one-line seatbelt profile,
`(allow default)(deny file-write* (subpath "<git-common-dir>"))`, fails the same
fresh fixture byte for byte. The runner exports `CODEX_SANDBOX=seatbelt`. Two
shapes, by whether a peer holds the ledger open:

| `-wal`/`-shm` beside the ledger | read verb | write verb |
|---|---|---|
| absent (no peer has it open) | CANTOPEN (14) at open | CANTOPEN (14) at open |
| present (a peer has it open) | succeeds, read-only | `attempt to write a readonly database (8)` |

**The decision** — `errVerb` passes a failing verb's error through
`explainLedgerAccess` (`internal/cli/ledgeraccess.go`). When the error is
SQLite's CANTOPEN or READONLY family (the chain's code, or SQLite's own fixed
strings, since `fencedErr` flattens some chains) AND an `O_RDWR` open of the
ledger is refused by the OS (EPERM from seatbelt, EACCES from file modes), it
appends two lines: the cause (the ledger path and the errno, that it lives in
the git common directory outside the sandbox's writable roots, naming
`$CODEX_SANDBOX` when set) and the fix (request escalation up front for every
buddy call; every verb needs write access, reads included). Anything else comes
back unchanged. Only the error path pays: one git process and one open, after
the verb already failed.

**What it does not do** — the gate is not routed through `errVerb`, so an
unopenable ledger still DENIES there and never becomes feature-off (invariants
2 and 3; `TestLedgerAccessGateStillDenies`). Nothing opens the ledger read-only
to get a verb through, retries, or decides anything from `CODEX_SANDBOX`. A
read-only open was considered and cut: with no `-shm` present the open fails
anyway, and a sandbox's refusal is the operator's approval to give, which is
what escalation is. AGENTS.md and the skill tell Codex roles to request it up
front; that, not this text, removes the failed first call.

**Tests** — `TestLedgerAccessNamesCauseAndFix` (file modes: CANTOPEN-shaped at
open, READONLY on write, a `fencedErr`-flattened verb, `$CODEX_SANDBOX` named;
each with a writable-ledger positive control), `TestLedgerAccessSaysNothingOtherwise`
(writable ledger, unrelated error, no repo), and on darwin
`TestLedgerAccessUnderSeatbelt`, which re-runs the test binary under
`sandbox-exec` with the profile above from a linked worktree and requires the
trial's `(14)` line, EPERM and the fix. It skips, saying so, only where a
sandbox cannot nest.

## D-085 — Inherited harness identity: refuse with the remedy; a herdr tab does not leak

2026-10-14. F2 of the Codex fleet trial.

**What was reported** — "codex-orch's early `buddy status` (inherited
`CLAUDE_CODE_SESSION_ID`) reported orch-3's session (JOBS pid 29377)".

**What the record shows** — not that. In the orchestrator's rollout
(`~/.codex/sessions/2026/10/14/rollout-…-01a13bb6-….jsonl`) every `buddy status`
printed `* codex-orch (01a13bb6-…)`. pid 29377 appears only in the output of
`buddy who buddy-system/s-51ebb7e8`, an explicit query about orch-3, and in
`msg`'s wake address for orch-3. The `printenv A B C` probes the trial ran do
not show what was inherited: BSD `printenv` reads only its first operand
(measured: `printenv CLAUDE_CODE_SESSION_ID HERDR_PANE_ID` prints one value).
Lane A ran every buddy call as `env -u CLAUDE_SESSION_ID -u CLAUDE_CODE_SESSION_ID …`
because its brief told it to, not because it had seen a leak.

**Measured** — (1) A herdr tab opened from this Claude pane
(`herdr tab create`, herdr 0.7.5) carried `HERDR_*` and no `CLAUDE_*` or `CODEX_*`
variable: a tab's shell takes the herdr SERVER's environment, and this
machine's server was started from Terminal.app's zsh with none. (2) Codex's
runner passes an inherited `CLAUDE_CODE_SESSION_ID` straight through (its
default `shell_environment_policy` inherits all), so a Codex launched as a
CHILD of a Claude shell holds both ids. (3) With both set and different, the
installed binary already refuses every verb that resolves the caller:
`$CLAUDE_CODE_SESSION_ID and $CODEX_THREAD_ID disagree (…); clear the inherited
identity before continuing`.

**The decision** — keep refusing (D-068: the two cannot be ordered from the
environment, and D-025 makes the ancestry walk the identity of one decision,
not a tiebreak). Picking "the harness actually running the caller" by walking
up to the nearest `claude` or `codex` process was considered and not done: it
would overturn D-068's "never guess from ancestry". The refusal now says how:
`env -u CLAUDE_CODE_SESSION_ID buddy …` under Codex, `env -u CODEX_THREAD_ID
buddy …` under Claude Code, and that a herdr tab does not inherit the
launcher's environment. The skill's "Running a fleet" gains the Codex lane
launch and says which launches leak: a herdr tab and `claude --bg` do not (the
latter runs in the claude daemon's environment, already measured there); a lane
started as a child of your own shell does, and is launched with both ids unset.

**Residual** — a caller under one harness that holds ONLY the other harness's
id would be misattributed. It needs a harness that exports no id of its own;
none is observed (Codex 0.160.0 and 0.162.1 export `CODEX_THREAD_ID`, Claude
Code exports `CLAUDE_CODE_SESSION_ID`). A herdr server started from inside a
harness session would hand that session's id to every pane it opens; not
observed here, and named in the skill.

**Tests** — `TestNativeDisagreementNamesTheRemedy` (a read and a write verb,
then the cleared control); the refusal itself stays pinned by
`TestCodexNativeIdentityAmbiguity` and `TestCodexWaitOwnThread`.

## D-086 — A verb run under the harness's native identity is a hearing: it moves `last_seen`, and nothing else

2026-10-14. F3 of the Codex fleet trial.

**The failure** — the roster showed the Codex orchestrator `NOT SEEN FOR 2h` six
minutes after it re-claimed (orch-3's notes), and `msg` told every sender the
same. A Claude session's `last_seen` moves on every tool call because its
PreToolUse hook beats; a Codex session has no buddy lifecycle hooks (unfenced
ones are refused, D-069), so only `hello` ever moved it. An active coordinator
read as stale.

**The decision** — `heardVerb` wraps the non-hook verbs (`claim`, `release`,
`ls`, `sweep`, `pause`, `resume`, `msg`, `sent`, `inbox`, `sessions`, `whose`,
`status`, `who`, `authority`, `ids`, `wait`). Before the verb runs, the session
the harness names (`$CODEX_THREAD_ID` or `$CLAUDE_CODE_SESSION_ID`, refusing
their disagreement) gets `Store.Heard`: `UPDATE sessions SET
last_seen=MAX(last_seen, now) WHERE session_id=? AND ended IS NULL`. Before,
so the verb's own output reads the present under either harness. Failure is
silent: an observation, and a ledger that cannot be written fails the verb
next with its own error.

- **Only native identity.** `--session` and `$BUDDY_SESSION` say whom the
  caller acts FOR; the operator running `buddy status --session X` is not X
  being heard from. The same rule `wait` uses to say "THIS session".
- **Nothing else moves.** No idle row cleared (D-016: idle is reported, busy
  never inferred), no claim renewed (renewal is per observed path, D-078), no
  process registered, no lifecycle state.
- **Invariant 12.** `ended IS NULL`: an ended session stays ended, and an id
  never seen stays unseen. No incarnation is taken. D-069 fences EVENTS that can
  arrive late, a hook queued behind a resume; a verb runs inside its caller's
  tool call and returns before it, so the only state it can describe is now.
- **Not wrapped:** `init` (may create the ledger); the hook verbs `hello`,
  `bye`, `beat`, `idle`, `busy`, `gate`, `commit-gate` (own contracts; a `bye`
  must never look like activity; the gate's 100 ms budget); `lifecycle` and
  `adapter` (captured-incarnation events, D-069); `diagnose` (never opens the
  ledger with SQLite, D-072).

**What it costs** — a read verb now writes one row: one more open and one
short write transaction per verb (not per hook). Under the Codex sandbox that
write fails like the verb's own (D-084). A Codex session that runs no buddy
verb for 30 minutes still reads STALE, which is what the ledger knows; the
skill tells a Codex session to run `buddy inbox` at each milestone, which is
also the only way it reads its mail.

**Refused alternative** — leaving `seen` hook-only and rewording the roster
for sessions with no registered harness process. "No registered process" is
not "no heartbeat": an adapter worker beats through `lifecycle` with pid 0, so
the wording would have been wrong for it.

**Tests** — `TestVerbUnderNativeIdentityIsHeard` (Codex claim, status, inbox,
a refused release, Claude status: heard; override only, `--session` only,
disagreeing natives, `diagnose`: not heard; a bystander never moves),
`TestHeardSessionIsNotReportedStale` (the trial's case end to end through `msg`
and `sessions`, with the stale control first), `TestHeardNeverRevives`.

## D-087 — The gate's "cannot rule out" arm is Codex-only; a literal `apply_patch` still refuses under every harness (narrows D-073)

2026-10-20. Finding I of the 2026-10-14..20 fleet audit. OPERATOR DECIDED.

**The failure** — D-073 (audit A-01) refuses, wherever a ledger governs, a
shell command whose script is produced at run time (`eval "$x"`, `sh -c "$c"`,
a pipe into a shell) or that the bash-mode parser rejects, because Codex can
apply a patch IN PROCESS from its shell and no static reading rules such text
out. The arm fired on Claude Code Bash calls too, which have no such engine and
no `apply_patch` tool to be sent to. On one fleet of 144 Claude sessions: about
10 denials across about 9 sessions, every one `eval`, `sh -c "$c"`, zsh's
`${=p}` (valid zsh; the Bash tool there runs zsh) or a pipe into
`ssh host 'sh -s'`. Each cost a rewrite, the message pointed at a tool Claude
does not have, and writing the same probe to a script file passed: it
protected nothing there.

**The decision** — the uncertain arm applies only when the gate's existing
harness classification (`classifyHook`: `buddy_harness`, `$BUDDY_HARNESS`, or
`$CODEX_THREAD_ID`) says Codex. Under any other harness the walker records no
uncertainty and keeps reading, so `eval "$x"; apply_patch < p` still finds the
literal. A literal `apply_patch`/`applypatch` at a command position refuses
under every harness, as before. An exhausted work bound (depth, bytes, parses,
brace fields) also still refuses under every harness: that is the gate having
stopped reading before it could rule out the literal, not a verdict about the
text. A Claude refusal no longer mentions Codex's tool.

**What it costs** — under Claude, a literal `apply_patch` inside text the gate
cannot read (run-time text, an unparseable script) passes. That is the same
cooperative residue as a script file, which already passed under every
harness, and Claude Code has no in-process engine for it to reach. So does
partly dynamic text: `bash -c "apply_patch < $f"` passes under Claude, because
the script bash runs is built at run time. A word in the command's OWN argv
after a dynamic value is still read: the Codex code pass found `env -S "$opts"
apply_patch < p` passing (an empty `$opts` runs the literal), because the
wrapper scan returned at the dynamic value; it now skips the value and goes on
under any harness but Codex. A Codex hook with no marker and no
`$CODEX_THREAD_ID` is classified Claude (D-073's F-31 continuation); the
installed Codex hook line carries the marker.

**Tests** — `TestGateUncertainRouteIsCodexOnly` (each fleet shape: allowed for
a Claude payload, refused as uncertain for a Codex one, the positive control),
`TestGateLiteralApplyPatchRefusesUnderEveryHarness` (literal forms, including
uncertain text before the literal, refused for both; feature off without a
ledger), `TestGateExhaustedBoundRefusesClaudeToo`. The shell-route suite runs
on Codex payloads, the harness it describes.

## D-088 — A claim refused by a holder exits 4; "not a repository" and "no ledger" say where buddy looked

2026-10-20. Finding K of the 2026-10-14..20 fleet audit.

**The failure** — three errors, one exit status. A driver script on one fleet
read a not-a-repository exit 1 as "slot held" and lost two starts. The
not-a-repository error named no directory: 7 errors in 6 sessions of one
bucket and 7 in 5 of another, all from a notes directory inside `cd notes &&
… && buddy …` chains, one of them a RED report. And a lane in a second clone of
the same repository was told `no ledger at … — run buddy init in this repo
first`: a second ledger coordinates with nobody (one lane built a PATH shim
that cd'd into the right worktree instead).

**The decision** — `claim` exits **4** when a holder refused it (a scope or
the slug held, `store.ErrRefused`), and a `--dry-run` that forecasts such a
refusal exits 4 too. Every other failure keeps 1, and a bare `buddy` keeps 2.
4 rather than 3 because `internal/testguard`'s orphan exit is 3 on this box.
`claim --help` says so. The gate is unaffected: it always exits 0 and answers
in its JSON. The not-a-repository error names the working directory and says to
run buddy from inside the repository whose ledger the peers use; the no-ledger
error says a ledger made here would coordinate with nobody and names `buddy
init` only for a repository meant to be coordinated on its own. The verdicts
(invariant 3, D-044) are unchanged: only the words.

**Also, usage** (findings H and L, no decision of their own): hello's hint and
the skill say `buddy claim <slug> --dry-run`; `claim` with a flag first says
the slug comes first; an argument that begins with `-` and holds whitespace (an
unsplit zsh `$S`) is named as such, pointing at an array; `msg`'s usage states
the `--measured` (128) and `--relay` (64) rendered-byte caps; `ids take
--desc` says ids take uses `--note`.

**Tests** — `TestClaimExitCodesTellHeldFromElsewhere` (scope held, slug held,
dry run: 4; granted: 0; not a repository, no ledger, unknown flag, comma scope:
1; bare: 2), `TestNoRepoAndNoLedgerSayWhere` (and the gate still silent in
both), `TestUsageSaysWhatWentWrong`.

## D-089 — `hello` takes its label from `$BUDDY_LABEL` when no `--label` is given

2026-10-20. Finding O of the 2026-10-14..20 fleet audit.

**The failure** — every lane wore `<repo>/s-<8hex>`, and peers addressed lanes
by the name the launcher gave them (`herdr agent start <label> … -n <label>`):
at least 8 `no such target` refusals on one fleet. `--label` exists only on
the SessionStart hook line, which is one line for every session.

**The decision** — `hello` reads `BUDDY_LABEL` when `--label` was not given;
the flag wins. The value takes exactly the path `--label` takes: stored as
given, fenced wherever it renders, and the label-worn-twice warning fires for
it. The skill's "Running a fleet" sets it on the tab: `herdr tab create …
--env BUDDY_LABEL=<label>` (a tab's `--env` reaches the lane, measured). A tab
does not inherit its launcher's environment (D-085), so a lane's label does not
leak to the lanes it opens. Only `hello`: `lifecycle begin --label` is the
adapter's typed argv.

**Tests** — `TestHelloTakesItsLabelFromBuddyLabel` (msg by the env label
resolves; the flag wins; worn twice warns; unset keeps the default label).

## D-090 — STALE keys on the holder, not only on the claim's renewal

2026-10-20. Finding C of the audit of one fleet of 144 sessions (2026-10-14..20).

**The failure** — a claim renews only when a tool call names a path it covers
(`store.Beat`), so a `.buddy/slot/` claim (a resource no tool call touches) and a
parked lane's claim go unrenewed while their holder works. Every view marked the
claim STALE by renewal alone, and the refusal said "STALE: holder last renewed 34m
ago; it still refuses, so ask the operator" about holders seen seconds before. In
one bucket 6 of 7 claim refusals carried it with every holder live, and three
decisions turned on it: one lane deferred its work; one told the operator "only you
can free a stale claim" about a six-hour slot whose holder was running (the
operator: "6h stale, seems something off?"); one re-claimed "so they aren't swept",
and stale never sweeps. `staleNote`'s own comment named the intent as "the holder
had stopped beating". The commit gate had the same defect from the other side: it
tested the claim's renewal and printed the holder's last-seen age beside it, so a
holder seen a minute ago read "live but SILENT for 1m".

**The decision** — one judgement, `holders.judge` (`internal/cli/stale.go`), and
every view renders it: the claim refusal and its dry run, `ls`, the hello digest,
`who`/`status`, `whose`, and the commit gate's holder phrase.

- Renewal still decides whether there is anything to say: renewed inside
  StaleAfter, or never recorded (issue #13's zero-clock guard), says nothing.
- Past it, the HOLDER decides the words. Ended, every registered process GONE, or
  not seen past StaleAfter: STALE and "ask the operator", issue #18's wording, for
  the case it was written about, now naming which (`holder not seen 2h`,
  `holder's harness process GONE`, `holder ENDED`). Otherwise: "claim unrenewed
  34m; holder seen 5s ago; it still refuses, so ask the holder: buddy msg <slug>".
  The command names the slug only when the slug resolves to the holder, else the
  holder's session id: a slug resolves LAST (D-013), so a claim slugged `all`
  printed a broadcast and one slugged like a peer's label addressed that peer
  (Codex code pass). It is written as a command only when it pastes back as
  printed (onFlags' rule).
- **A slot of a live holder carries no mark at all.** No tool call can renew a
  slot, so "unrenewed" is true of every slot held past thirty minutes and says
  nothing about whether the run it guards is alive; the holder's liveness is the
  whole story. A slot of a quiet holder is STALE like any claim. A claim mixing a
  slot and a file scope is judged as a file claim.
- GONE needs every registered process gone (a second `--resume` registers a
  second one); a session with none registered is "cannot say", never GONE.
- `ls` prints `open STALE` only for a quiet holder (its age column is the
  renewal); hello's bracket is `[STALE: holder not seen 2h]` and appears only then.

**Words only** — D-022 (a stale claim refuses exactly like a fresh one) and D-026
(an ended holder's claim is displaced) are untouched; nothing renews, orphans,
reaps or sweeps differently, and GONE stays diagnostic (D-025). A refusal whose
holding claim cannot be re-read keeps the old renewal-only words and the
operator, the conservative direction; so does one whose holder is not the one the
refusal named (matched by label, which `store.ErrRefused` carries, and by id
where the conflict has it), so a slug released and re-taken between the two
reads never lends the new holder's words to the old one (Codex code pass).

**Tests** — `TestRefusalNoteMessagesTheHolderNotAShadowingName`,
`TestConflictNotesFallBackWhenTheHolderChanged`, `TestStaleJudgementKeysOnTheHolder` (the rule's table, with
staleNote's zero-clock and threshold rows), `TestEveryViewAsksTheHolderWhenTheHolderIsLive`
(all seven views on a live holder with an unrenewed claim, then the same claim
with its process GONE as the armed control), `TestASlotOfALiveHolderCarriesNoMark`,
`TestRefusalNoteWritesOnlyAPastableCommand`; `TestRefusalSaysWhenTheHolderHasGoneQuiet`
carries the quiet-holder refusal and dry run.

## D-091 — The roster folds dead rows into one line; `--all` lists them (amends D-015)

2026-10-20. Finding D (roster half), OPERATOR DECIDED 2026-10-20.

**The failure** — `herdr pane close` ran no SessionEnd for 68 of 119 closed lanes
on one fleet (in all 45 where it did run, `bye` ended the row), so their rows
never ended. The roster reached 906 rows and 168 KB: 758 ended, 147 `live STALE
... pid N GONE`. Sessions grepped it for their own row (`buddy sessions | grep
s-xxxx`, 15+ times) or `head -30`'d it and read dead rows.

**The decision** — `buddy sessions` lists live rows not known GONE, and rows ended
in the last 24 hours; everything else folds into ONE trailing line, e.g.
`(+ 612 ended more than 24h ago, 147 live with every registered process GONE —
not listed: buddy sessions --all)`, printed only when something folded.
`--all` lists every row as before. The caller's own row is always listed, including
an ended one named by `--session` or the harness id, which `whoAmI` refuses (Codex
code pass). A live
row with no registered process (Codex, an adapter worker) is "cannot say" and
stays; GONE needs every registered process gone, the D-090 rule.

**Why 24 hours** — long enough that a lane closed in this working day (the one a
coordinator is confirming it closed, or whose base it is reading) stays listed;
short enough that history does not pile up. Measured on that fleet's ledger, 67
days sampled hourly: a trailing 24h window held 11 ended rows on average and 67
at most, against 758 ended in all (17 at the snapshot). 6h averaged 3 and would
drop lanes closed that morning; 72h averaged 34.

**Nothing changes in the ledger** — no row is ended, reaped or touched (D-025: a
GONE pid is diagnostic, and an auto-end on a misrecorded anchor is the delayed-bye
defect by another road). `msg all`'s recipient count is unchanged.

**Amends D-015** — D-015 cut a header line because the common case is ONE row
quoted into chat, where a header is lost and labels must ride with the numbers.
The fold line labels no column, every row keeps its own words, and it trails the
rows; it is the rule every capped list here follows (D-027: a report that quietly
drops rows is indistinguishable from a smaller one), not a header.

**Tests** — `TestSessionsFoldsDeadRowsIntoOneLine` (old ended and all-GONE fold,
recent ended, live and unbound stay; the exact line; `--all`; the caller's GONE
row stays; the folded row is still live in `who`; no line when nothing folded),
`TestFoldRosterNeedsEveryProcessGone`.

## D-092 — `who` answers the close test on one EXIT line, scopes WAITED ON to a named claim, and reports the caller when bare

2026-10-20. Findings P, R, T and L (`who`) of the same audit.

**EXIT states the wait and the jobs (P)** — closing a lane needs no claims, no
wait and no running job, and EXIT spoke for claims only: coordinators hand-rolled
`buddy who | grep -E 'CLAIMS|WAITING|JOBS'` about 35 times in 20 spellings
before 13 closes. EXIT is now one line of three clauses, then the consequence:
`EXIT         no claims held; no wait declared; no shell found running under it
— the ledger would be left holding no claim (...)`, or `2 claim(s) held; a wait
declared EXPIRED (WAITING above); 1 shell(s) running under it (JOBS above) —
ending now would leave the claim(s) held, ... first: a, b`. The jobs clause keeps
"cannot say" (`its shells: cannot say (JOBS above)`) apart from "none" (D-016,
D-065). Still a report that grants nothing (D-027); an ended session still reads
`already ended`.

**`who <claim>` lists only that claim's waiters (R)** — named by a claim slug,
WAITED ON lists the waits on THAT claim and names it (`WAITED ON    on claim
"run32" by 3 session(s), ...`). It printed the holder's whole set: `who run32` and
`who run33` showed the same three READY riders. A session target is unchanged.

**A bare `who` reports the caller (L)** — exactly `status`. A session typing it
was asking "who am I", and a usage error cost a round trip; a refusal naming
`status` would answer a question it understood with a pointer. D-031 refuses what
a verb does not understand, and an absent target is understood. A target that
contains a slash and resolves to nothing adds "if it is a path, `buddy whose
<path>` reports who claims it": a suggestion, never a redirect, since labels carry
slashes too.

**`whose .buddy/slot/...` consults no git (T)** — a slot is never a file (D-035),
so the dirty register and git's paragraphs had nothing to say (12 calls on one
fleet). The claim register prints as before, then `DIRTY IN     (not applicable
...)`.

**Tests** — `TestExitLineAnswersTheCloseTest` (cannot say, none, everything
named, an expired wait's verdict), `TestWhoOfAClaimListsOnlyThatClaimsWaiters`
(with the session-target control), `TestWhoWithNoArgumentReportsTheCaller`
(byte-identical to status; the slash suggestion and its no-slash control),
`TestWhoseOfASlotConsultsNoGit` (with the file control).

## D-093 — `release` takes several slugs as one transaction; a recipient waiting on a claim it is handed lands, "handed to you" (narrows D-063)

2026-10-20. Finding E of the fleet audit (one fleet of 144 sessions, 2026-10-14..20).

**The failure** — four measured shapes. (1) One slug per call: about 93
`release --to` calls, 41 of them inside for-loops; one loop sent its recipient
8-10 notices and printed 8 stderr wake lines. (2) A sender whose OWN claims
overlapped each other could not hand them on one at a time: each one alone
left its siblings behind under the sender, the overlap scan refused it, and
the refusal read "... and 7 more conflict(s), listed above; nothing was handed
on" over a listing that only `claim` ever printed. (3) D-063 refused a
recipient waiting on the claim, which is exactly what a successor does while it
waits for the handover: 4+ episodes, each a refusal, a round trip asking the
successor to clear and a poll loop; one of those waits had EXPIRED 14 minutes
earlier and still blocked, because the count ignored the deadline. (4) Nothing
told the recipient "a claim was handed to you" in a form a wait could see: 6
`until buddy ls | grep … <its id>` loops, one of which fired on its own
WAITING line.

**The decision** —

- `buddy release <slug>... --to <target>` (`store.TransferClaims`) moves every
  named claim in ONE immediate transaction, all or nothing, in the order named,
  a repeat counted once. Every claim moves before any is scanned, so the scan
  against the new owner excludes exactly the claims moving together; one slug
  that is not the sender's, or one conflict anywhere, rolls every move back.
  ONE notice to the recipient for the set, one result line, one wake.
- A refused transfer PRINTS the whole conflict set, one fenced `REFUSED:` line
  each (`printConflicts`, as `claim` does), so "listed above" is true, plus one
  fixed line saying claims named together move together.
- A recipient with an open wait on a claim it is handed is not refused. Nothing
  is cleared or repaired: a wait target the waiter itself holds, under the
  incarnation that declared, counts as over (`WaitTarget.HandedToWaiter`,
  `Pending`), computed at read time like every verdict input (D-020's rule).
  `release --to` is the only road to that state — `DeclareWait` refuses your own
  claim and a claim id changes hands only by transfer — so the move in that
  transaction IS the resolution. The recipient's `wait check`, beat's one-shot
  LANDED and `--block` say LANDED with `"<slug>" handed to you`; the sender's
  line says the recipient's wait was resolved; the notice says so too. A
  multi-target wait stays pending until its other targets close (D-063's worry,
  that dropping one target lands it early, does not arise: nothing is dropped).
  Handing the claim on again un-does it. A waiter on some OTHER claim, and every
  bystander waiting on the handed one, are unaffected: their waits follow it.
- Plain `buddy release a b c` takes several slugs too, by the same rule (one
  transaction, all or nothing, a line per claim naming its waiters). A lane
  winding down releases two claims every time (`<slug>` and `<slug>-shared`).
  `--scope`, `--outcome` and `--note` stay single-slug, refused with several:
  an outcome reports on one job and a narrowing names one claim's scopes.

D-063's refusal list narrows by one entry (the recipient waiting on this very
claim); everything else it refuses stands.

**Tests** — store: `TestTransferResolvesTheRecipientsOwnWaitOnIt` (lands;
an expired wait lands rather than blocking; a second open target keeps it
pending; a bystander keeps waiting; handing it on again un-does it — each with
the pre-transfer control), `TestTransferLeavesTheRecipientsOtherWaitAlone`,
`TestTransferClaimsMovesASetWhole`, `TestReleaseClaimsIsWholeOrNothing`. CLI:
`TestReleaseToHandsASetWithOneNoticeAndPrintsTheWholeRefusal` (reads the
REFUSED lines the error counts), `TestASuccessorWaitingOnTheClaimIsHandedItAndLands`.

## D-094 — `who <slug>` prints every scope of that claim, uncut

2026-10-20. Finding F of the same audit.

**The failure** — every handoff notice says "re-claim it with the SAME
scopes", and every view of a claim's scopes (`ls`, `who`'s CLAIMS HELD,
`status`, hello) cuts at 512 bytes with a count. One successor handed a
22-scope claim gave up ("the scope lists show truncated"); another typed all
22 by hand after `ls --json` (no such flag), sqlite3 on the live ledger
(CANTOPEN 14) and an eval-built re-claim the gate refused.

**The decision** — a `who` whose target resolved through a claim SLUG prints,
before the holder's report, `claim "<slug>" holds N scope(s), every one`, then
one `  scope <s>` line per scope through `fence.Field` (D-066's item rule: one
token, ␣ for a space), with no total cap; a scope the fence altered says it
does not paste back as shown. Every other view keeps its cap: they list many
claims and land in hook output and tool results that pay per byte. The handoff
notice now points at `who <slug>`. No `--json`, no new verb.

**Tests** — `TestWhoOfAClaimPrintsEveryScopeUncut` (31 scopes, each exactly
once on its own line; the altered-scope note; CLAIMS HELD still capped in the
same report as the control; a label target prints no block).

## D-095 — `buddy wait --on <slug> --block` exits with the verdict (amends D-033)

2026-10-20. Finding Q of the same audit. OPERATOR DECIDED (2026-10-20).

**The failure** — 15+ hand-rolled loops (`for i in $(seq 1 120); do buddy ls
| grep -q <slug> || exit 0; sleep 60; done`), run under the harness's
run_in_background beside a declared `wait --on` that recorded 0 checks. The
background job's completion notice is the wake a parked session reliably gets;
what the loops lacked was a verdict, and grepping `ls` matched the wrong rows.

**The decision** — `--block` on a declaration: the wait is declared exactly as
today and printed the same, then the process re-reads the ledger every 15 s and
exits 0 LANDED (released, an outcome, or handed to you, D-093), 3 EXPIRED, 4
cleared (`wait clear`) or replaced by a newer declaration, 5 the session ended
or re-registered; 1 stays every verb's error and 2 Run's usage. It prints the
line `wait check` prints for that verdict (one renderer, `landedLines` /
`expiredLines`), and closes the row after the write, as a check does.

- **Not a check, and not a keep-alive.** Each read is an autocommit SELECT:
  `last_check` and `checks` stay the scheduled check's (D-033), and the block
  keeps no cache warm, so a park past ~50 minutes on a 1-hour tier still arms
  `/loop buddy wait check`. D-033's "nothing wakes" stands: the harness notices
  its own job ending.
- **No write lock across a sleep.** The one write a poll may make is
  cleanupEnded's, in its own short transaction (`Store.SettleEnded`), and only
  when a read shows an awaited holder that has said bye — the read-only block
  would otherwise sit on that claim until its deadline.
- **It stops when its session ends** as the ledger records it; a harness that
  dies without a bye leaves it running to the deadline, which the 12 h ceiling
  bounds. Watching the parent process was not done: how run_in_background
  parents its shell was not measured.
- **15 s**: under the measured loops' 20 s and 60 s; a completion notice is read
  at the next turn anyway. 240 indexed reads an hour.
- **A timer (no `--on`) with `--block` is refused**, writing nothing: it only
  expires, which the harness's own timer does without a ledger.

**Tests** — `TestWaitBlockExitsWithTheVerdict` (released with an outcome,
handed to the waiter, holder said bye → LANDED 0; EXPIRED 3; cleared 4;
replaced 4; session ended 5; every case: the declaration prints first, the
counters stay 0, a printed verdict closes the row), `TestWaitBlockRefusesATimer`
(with the plain timer as the control). The poll runs through an `Env.Sleep`
seam that advances the fixture clock.

## D-096 — The handoff notice speaks once per 25k step past the size, not on every turn (amends D-055)

2026-10-20. Finding M of the 2026-10-14..20 fleet audit.

**The failure** — D-055 made `busy` say "your prompt was N … at or past the
size you hand off at" on every prompt that opens a turn, level-triggered. On
one fleet of 87 sessions it fired 442 times, up to 27 in one session: on task
notifications, subagent hand-backs and `/loop` keep-alives of lanes already
parked after their handoff, and twice in one session when two queued prompts
opened together. A line said that often is a line tuned out, on the sessions
that can least afford the context. Lanes ran to ~350k against a 300k
fleet-wide size.

**The decision** — the notice is told once per STEP: step 0 when the last
observed prompt reaches `BUDDY_HANDOFF_AT`, step k at the size plus k×25k.
`busyHandoff` computes the step and `store.HandoffTell` claims it: one
conditional UPDATE moves `session_context.handoff_step` (schema 16, the
highest step this incarnation has been told, -1 for none) only when the new
step is higher, and only the call that moved it prints. Under the size the
column goes back to -1, so a session that compacts and grows back is told at
its next crossing; a new incarnation's first observation resets it in the same
upsert. A HIGHEST step and not a set of steps, because the observation is read
outside the claim: the first shape forgot the steps above any lower one, and a
busy that read 300k and stalled deleted the step-1 mark a newer busy had just
printed at 325k, so a third printed it again (Codex code pass). A compaction
that stays over the size is told again only at a new high. The fall under
the size is `RecordContext`'s: every hook's sample carries the declared size
(`sampleOfUsage`), and one under it resets the column inside the upsert,
which applies only a sample at least as new as the row's turn, so the NEWEST
observation decides. The claim also matches the row's `prompt`, the one its
step was computed from, so a claim whose sample was replaced moves nothing.
Three Codex passes on schema 16 shaped this: the design pass found a late
reset re-arming a told step (once per stalled reset) and a late claim
standing over a newer reset (hiding the next crossing for good); the first
fix guarded both on the row's sample, and the re-verify found that a guarded
reset losing its race to a newer sample also lost a real dip under the size,
so the re-crossing went untold. Ordering the reset by the turn's time keeps
both. Not covered: `RecordContext` replaces a row on an EQUAL `turn_ms`, so a
sample of another prompt for the same millisecond could be written back over
a newer one (a higher one then holds the steps below it untold until the
next; an under-size one re-arms one). One `turn_ms` is one assistant record's
usage, so that needs two responses stamped in one millisecond. And a sample
from a process that declared no size resets nothing, so a dip only it saw is
lost; every hook of one session shares its environment, so the size is the
same wherever it is declared in settings or by the launcher.

- **Why 25k.** On this repo's own 662 main-thread turns the prompt grows by a
  median of 1.9k a turn (p75 6k, p90 24k, p95 48k): 25k is about a dozen
  ordinary turns, and one turn in ten crosses it alone. The fleet's lane that
  ran 300k→350k hears three lines instead of one per turn.
- **Claimed before it is written**, the reverse of the inbox's write-then-mark:
  two queued prompts run two `busy` hooks at once, and mark-after let both
  print. One conditional UPDATE (RowsAffected) has one winner. A write that fails after the claim loses that step's line; the next
  step says it again.
- **A schema bump, 15 → 16, by the operator's choice.** The first shape
  avoided it by storing each step as a row of `authority_warned` (D-028) under a
  reserved key. That needed a second key once the Codex pass found
  `authority add '\handoff-at'` normalizing onto the first (a `NormalizeScope` gap, reported separately), and it put
  two meanings in one table. Asked, the operator chose the column
  (2026-10-20). Its cost is D-037's: every buddy binary on the machine must be
  rebuilt by `install.sh` before any of them opens a migrated ledger, or it
  denies. The column sits on the context row because the step is computed from
  that row's prompt and belongs to its incarnation. `session_context` is the
  table D-018 lets the ledger drop and rebuild, and losing it costs at most one
  repeated line.
- **Every kind of turn.** The brief asked to skip turns no person opened if the
  payload can tell them apart. It cannot: the turn's origin is on the
  transcript's user record (`origin.kind`, `turnOrigin`: in this repo's
  transcripts 329 typed, 234 task-notification, 137 peer, 4 scheduled), but
  the `UserPromptSubmit` payload carries only the prompt text, `prompt_id` and
  the session fields (read from the 2.1.304 hook-input builder), and the
  record is NOT on disk when the hook runs: measured 2026-10-20 on 2.1.304, 7
  of 7 turns (typed, task notification, peer message, agent hand-back, `/loop`
  wake), at hook time and 300 ms later. The prompt's own leading tag
  (`<task-notification>`, `<cross-session-message`, `<agent-message`) marks
  three of them, but a `/loop` wake arrives as plain `/loop …` text, the same
  as a person typing it, and a person can type a tag. Deferring the line
  to a typed prompt would also starve a coordinator that goes hours between
  them. Once per step, a turn of any kind carries it into context, and the
  repetition is gone.

**Not measured** — the per-turn growth figures come from this repo's own
transcripts; a bulk read of the other fleet's transcripts was refused by the
harness's classifier, so the fleet figures above are the audit's counts.

**Skill** — a coordinator gives a lane its own size with `herdr tab create …
--env BUDDY_HANDOFF_AT=<n>` when lanes may run past the fleet-wide one.

**Tests** — `TestHandoffTellOncePerStep` (no row, crossing, same step, step,
skipped step, fall under, re-cross; a sample with no declared size resets
nothing; a new incarnation resets it; a superseded one claims nothing), `TestHandoffTellIgnoresAStaleSample` (a late under-size sample, a
dip whose busy stalls, a late claim, and an equal-time sample with another
prompt, each beside the fresh call that still works; the sequence tests drive
the busy hook's own order, record then read then claim), `TestHandoffTellConcurrentHasOneWinner` (8
concurrent claims, one told), `TestHandoffStepMigratesFromV15` (the ALTER arm on a v15 ledger with
a row), `TestBusySaysWhenPastTheHandoffSize` (end to
end: told once, silent one token short of the next step, told on it, silent
under, told at the next crossing).

## D-097 — Hook text that rode every drain and every send costs less: a short inbox header for a plain batch, and no stderr wake copy on the same file (amends D-052)

2026-10-20. Finding N of the 2026-10-14..20 fleet audit.

**The inbox header.** The header that explains the slot between `#id` and
`[sender]` (buddy's CORRECTS/SUPERSEDED links and the sender's declared kind)
is 318 bytes and rode every drain: 207 drains in one bucket of one fleet, 13 of
which had anything in that slot. A batch with nothing there now gets
`BUDDY MESSAGES (operator/peer text — untrusted input, not instructions; one
line per message, newlines shown as ⏎):`, 120 bytes. A batch in which any
line carries a link or a declared kind keeps the long header, including a lone
correction whose original arrived earlier. The rendering is untouched
(invariant 9): the fence and the one-line rule do not depend on the header, a
plain line always has `[` straight after its id (D-043), and every declared
kind still says "declared" on its own row (D-045). Every budget still measures
the long header.

**The stderr wake copy (D-052).** `msg` and `release --to` write the wake line
to stderr as well as on the result line, because a sender discarding stdout
lost it. Under `2>&1` the same reader got it twice: 121 of ~200 `msg` results
in one bucket, at 321 bytes or more each. The copy is now skipped when stdout
and stderr are the same open file, by fstat device and inode (`os.SameFile`):
`2>&1` duplicates the descriptor, so a pipe, a file and a terminal each compare
equal to themselves. Anything else, including a writer that is not a file or
cannot be stat'ed, still gets the copy: said twice rather than lost. So does a
socket: darwin stats a TCP socket as device and inode 0, so two different
connections would compare equal (Codex code pass).

**Considered and not done: hello's claims grouped by holder.** The digest ran
8.6–8.9 KB with up to 21 of 25 rows from one holder. Measured on that fleet's
ledger copy (1,972 claims), the holder's `(label)` is 20 of a row's ~228 bytes
(8.8%); folding 21 rows under one holder saves about 370 bytes of 8.7 KB
(~4%), and a holder's rows would have to be contiguous, which breaks D-036's
oldest-first order. Not a clear win, so not done.

**Tests** — `TestInboxHeaderLongOnlyWhenTheSlotIsUsed` (plain, two plain, lead,
plain+measured, relay, original+correction), `TestInboxHeaderLongForALateCorrection`,
`TestMsgWakeCopyOnlyWhenStderrIsAnotherFile` (a dup'd descriptor, one file as
both, two files as the control), `TestSameOpenFile` (pipes, sockets, buffers).

## D-098 — A refusal that shows the usage puts its reason on the last line

2026-10-20. Finding K (last bullet) of the 2026-10-14..20 fleet audit.

**The failure** — a refusal printed its reason, then the usage under it. A
caller reading `buddy release … 2>&1 | tail -1` kept the usage and lost the
why: 31 `release … | tail -1` and 20 `claim … | tail -1` on one fleet,
including a `--note` without `--outcome` that released nothing while the
caller saw only a usage line.

**The rule** — every refusal that shows a usage goes through `usageRefusal`:
the usage block first, then `  refused: <reason>` as the LAST line. `parseFlags`
(every unknown flag, every verb), `noStray` (a stray positional, every verb),
`init`'s argument check and `release`'s five argument refusals use it. A
refusal with no usage (a conflict set, a cap) is already one line ending in its
reason and is unchanged; a claim conflict's error line already prints after its
REFUSED lines. "refused:" says it was a refusal to a reader who sees nothing
else.

**Left as they are** — `sent`'s two refusals are already one line, reason
first and usage after it on the same line. A bare usage (`buddy claim` with no
slug) has no reason but the usage itself. `commit-gate`'s two flag refusals
are printed by a git hook to git's stderr, never piped by a caller. `wait`'s
stray-positional refusal and two new ones in the handover lane (`wait --block`,
`release` with several slugs) are that lane's files; they convert at
integration.

**Tests** — `TestRefusalReasonIsTheLastLine`: eleven refusals across
`release`, `claim`, `init` and `sweep`, each read as `2>&1` would show it: the
last line starts with its `refused:` reason, the usage is still printed, one
reason line; the positive control is the same claim released cleanly after.

## D-099 — Mail reaches the session it was sent to: subagent hooks drain nothing, a drain holds the write lock, a Stop hands queued mail over, and `inbox <id>` re-reads

2026-10-20. Findings A, B and G of the audit of one fleet of 144 Claude Code
sessions (2026-10-14..20). Amends D-016 (idle is withheld when the Stop hook
continues the turn), D-034 / D-040 (who drains, and under what lock) and D-039
(when the wake is named).

**A. A subagent's tool call drained the parent's inbox.** Claude Code fires
the configured PostToolUse hooks for a tool call made inside a subagent, with
the PARENT's `session_id`. `beat` drained the inbox into that call's output,
which lands in the SUBAGENT's context, and marked it delivered; `buddy sent`
then reported a recorded delivery. Counted on the fleet: 181 distinct messages
reached 186 subagent transcripts, at least 30 of them absent from the main
transcript (an orchestrator's ruling; "your successor is …"). Sessions noticed
("a read-only panel subagent drained my inbox") and worked around it by telling
every subagent to relay mail, or by pasting the whole instruction into the
SendMessage wake, bypassing the ledger and the fence. The one-shot notices rode
the same drain, so a subagent consumed LANDED and authority notices too.

*The marker.* `hookInput.AgentID` was parsed already; `lifecycle.go` refused
it as a subagent identity for Codex events (D-069), a guard, not a
measurement. For Claude Code the field is documented ("the input carries the
`agent_id` and `agent_type` common input fields that identify the subagent")
and the installed 2.1.304 binary's hook-input schema says `agent_id` is
"Present only when the hook fires from within a subagent … Absent for the main
thread, even in --agent sessions". **Not measured on a live subagent
PostToolUse payload**; a hook that logs its stdin under a subagent would
measure it, and needs a settings change that is the operator's.

*The decision.* `hookInput.subagent()` is `agent_id` (or the Codex parent
markers) present. Such a beat is still the session's heartbeat — seen, claim
renewal, the dirty path, the footprint from `transcript_path` (the parent's) —
because a subagent's write is the session's work. It drains nothing and
composes no authority or LANDED notice, so those wait for the main thread's
next tool call. The dirty-path warning still shows to the subagent: it is about
the file that call touched, and the subagent is the one editing it. `busy` and
`idle` return before draining for the same marker (Claude fires SubagentStop,
not Stop, for a subagent, so these are guards for a wiring that names them).

**A'. Concurrent beats each delivered one message.** Three parallel Agent
calls injected one message three times in one millisecond: `Undelivered` (a
read), the write, then `MarkDelivered` (INSERT OR IGNORE), so two readers both
found it unmarked. Reproduced first: eight concurrent beats in-process
delivered one message four times. `store.Drain` now takes the write lock
(BEGIN IMMEDIATE) before the read and holds it across the caller's write and
the mark; a second drain waits in BEGIN and then reads nothing. Write first,
mark after still holds — a failed write rolls back and marks nothing, and a
process that dies between write and commit is redelivered. The read runs on a
second pooled connection (WAL: a reader is never blocked, and with the lock
held no one else can commit), so `Undelivered` stays the one query deciding
what a session is owed. The lock is taken only when a plain read finds mail,
so a beat with nothing queued opens no write transaction, as before. beat,
busy, idle and `inbox` all drain this way; hello's SessionStart drain is
unchanged. *Cut:* mark first and write after (a failed write becomes a lost
message); a lease column (a schema bump for what a transaction gives).
*Residual:* the authority and LANDED notices are composed before the lock, so
two concurrent beats can each carry one once; the finding counted message
duplicates only.

**B. Mail queued between a session's last tool call and its Stop was
stranded.** The lane pattern is "msg the orchestrator, stop", so the reply
lands seconds after the lane's last tool call. `msg` saw "last seen 4s ago" and
named no wake (D-039: a session seen recently drains on its own); the Stop then
marked the session idle with the mail queued, and nothing runs a tool at a
prompt. Six confirmed stalls of 6, 15, 31, 58, 67 and 75 minutes (one: msg at
20:13:20.7, the target's last tool call 20:13:13, its turn ended 20:13:21.9);
at least 30 sends per bucket printed no wake for that reason. Senders
compensated with a hand-typed fixed wake, which the harness drops as an
identical repeat (D-059's collision).

*The decision.* `idle` runs `store.StopTurn`: in ONE transaction, MarkIdle's
incarnation fence, then — unless the payload's `stop_hook_active` is true —
the queued mail. If there is any, it is drained by beat's rules (bound, fence,
write then mark) into a Stop document `{"decision":"block","reason":…}` whose
reason is one buddy line and the fenced inbox block, and the session is NOT
marked idle (an earlier mark is retracted): the turn goes on with its mail.
Otherwise idle is recorded exactly as before. The settings line `buddy idle
2>/dev/null; exit 0` exits 0 with the JSON on stdout, which is the documented
shape ("`decision`: `"block"` prevents Claude from stopping"; `reason`
"Tells Claude why it should continue").

- **Never twice in a row.** A turn a Stop hook already continued says so
  (`stop_hook_active`, "true when Claude Code is already continuing as a result
  of a stop hook"); it is marked idle with its mail queued, so a peer writing
  in a loop cannot keep a session from resting. Mail sent after that idle mark
  is woken by `msg`. **Residual (Codex code pass):** mail queued DURING a
  continued turn, after its last tool call, is stranded as before — `msg` sees
  no idle mark and the second Stop may not hold the turn. Closing it needs a
  second hold (overturning this rule) or the time of the last hold (a schema
  change); neither is taken here.
- **Never for a subagent** (A), **never for adapter mail** (Undelivered
  excludes it; D-076).
- **A failed write** marks nothing and records the idle mark after all: the
  session is resting with its mail queued, and the next `msg` must see that.
- **Not inferring busy (D-016).** The idle mark is withheld because the hook
  itself is continuing the turn, which is an event, not an inference.

*The other end of the race.* `msg` observes its recipient BEFORE its insert,
so the backlog it reports is the earlier messages. A Stop that committed
between the observation and the insert found no mail and marked idle, and the
send named no wake. `msg` now re-reads the idle register AFTER the insert when
the first observation had none (`idleAfterSend`). With the Stop's check and
mark in one transaction, after the insert exactly one is true: the Stop saw
the mail and held the turn, or its idle mark is there to read and the wake is
named. The result line's note still says what was seen before the insert; the
wake may now sit beside "last seen 2s ago". An idle report already won over
recency in the wake rule; that is now said in wake.go's header and pinned by a
case. `release --to`'s notice is not re-read (it is another verb's send path).

*Cut:* the mail as Stop `additionalContext` (documented to continue the turn
too, but whether the next Stop then carries `stop_hook_active` is unmeasured,
and the loop guard rests on it); a wake for any recipient seen recently (it
would fire on every send to a working session).

**G. A delivered message could not be read again.** `buddy inbox --all` was
tried 3 times and `inbox --help` 9 times; two led to a resend round trip.
`buddy inbox [--session <id>] <id>` re-prints one message addressed to the
caller — by session id or current label, or a broadcast whose recipient
snapshot holds it — in any state (delivered, queued, expired), through the same
`inboxLine` fence as a drain, then one line of buddy's own giving that state.
It marks nothing. Another session's message, an unknown id and adapter mail
get ONE refusal with no body, so the difference reveals nothing about mail the
caller was not sent; `buddy sent <id>` stays the sender's view. A word after
the id is refused (the flag parser stops at it).

**Tests** — `TestConcurrentBeatsDeliverAMessageOnce` (8 concurrent beats × 5
rounds; failed on the old drain), `TestSubagentBeatLeavesTheParentsMailQueued`
and `TestSubagentBeatLeavesTheOneShotNoticesForTheMainThread` (each with the
main-thread positive control), `TestStopHandsQueuedMailOverInsteadOfResting`
(mail, none, `stop_hook_active`, subagent), `TestStopWithAFailedWriteRestsWithItsMailQueued`,
`TestStopNeverHoldsATurnForAdapterMail` (legacy-mail positive control),
`TestInboxRereadsOneOfMyMessages`, and the wake case "idle a second after its
last tool call". Each fix was mutated in a private copy and its test watched
fail (eleven mutants, all killed; the subagent LANDED guard is two guards,
killed only with both removed). Review: one lite Codex code pass, one finding
(the residual above), answered in the record. The post-insert re-read has no test that interleaves a Stop
between `msg`'s observation and its insert; there is no seam for it, and it is
covered by the transaction argument above.

## D-100 — A scope's backslashes fold before the absolute check; the removal doors can still name a legacy row

2026-10-20. Found by reading during D-096 (`authority add '\handoff-at'`
normalized onto a reserved key); operator decided full tier.

**The failure** — `store.NormalizeScope` refused an absolute scope with
`strings.HasPrefix(scope, "/")` on the RAW input and folded backslashes to
slashes after it. So `\etc` passed as relative and was stored as "/etc",
`\..\x` as "/x", and a lone `\` as "" (Clean gives "/", and the trailing slash
came off). Containment is exactly `scope == path || HasPrefix(path, scope+"/")`
over repo-relative paths, so each was a claim GRANTED over no file, the D-066
class, at every door the function serves: `claim` and its dry run,
`release --scope`, `authority add`/`rm`, `lifecycle beat --path`, and the
comma check's paste-back line. An authority row "/etc" was stat'ed at
`filepath.Join(top, "/etc")`, so it watched `top/etc` under a name no `rm`
could spell. Measured on this repo's ledger: 0 open claims with an absolute
scope and 0 absolute authority entries; the defect is a door, not a population.

**The decision** — fold first, then refuse: after `ReplaceAll(scope, "\\", "/")`
a leading "/" is refused, saying "(a backslash reads as a slash)" when the
input itself did not start with one. After the fold, `path.Clean` cannot make
a relative input rooted, and its empty result is ".", already refused, so
nothing accepted is absolute or empty. It lives in the store, so every door
gets it at once.

- **Legacy rows, by D-066's precedent.** D-066 refused a comma scope in the
  CLI only, so `release --scope` could still narrow a literal a ledger held.
  Here the refusal must be in the store (every adding door), so the two doors
  that only REMOVE a held row, `release <slug> --scope` and `authority rm`,
  call `NormalizeHeldScope`: `NormalizeScope`, except that where its ONLY
  objection is "absolute" (a sentinel error, not the words) it returns the
  spelling the old code stored. That can only match a row already held:
  release still refuses a scope its claim does not hold (whole request, nothing
  narrowed), and rm still says not-found. Without it, a legacy authority row
  held one of the eight slots for good. `release <slug>` with no `--scope`
  reads no scope and always released a legacy row whole. No adding door may
  call it: the comma check and `lifecycle beat --path` stay strict (Codex
  design pass: a paste-back line must be what a claim would accept, and a
  malformed observation must not get a receipt).
- **Not done.** A migration rewriting legacy rows (the ledger is never
  migrated to edit claim data, and none were measured). A Windows drive
  spelling (`C:\x` is "C:/x", relative, and names nothing on this platform).
  NUL in a scope, which survives normalization but which no argv can carry
  (Codex design pass, P3). Folding a backslash at all: a file name with a
  literal backslash still cannot be claimed, as before.

**Tests** — `TestNormalizeScopeFoldsBeforeRefusingAbsolute` (each input, at
both functions, with relative backslash positive controls and the refusal's
words), `TestClaimRefusesABackslashAbsoluteScope` (nothing written; a
relative backslash scope granted), `TestReleaseScopeNamesALegacyAbsoluteRow`
(planted "/x" and "" rows removed one at a time by the spelling that made them,
`/src` never removing `src`, a request with one scope not held removing
nothing, an escape still refused), `TestBackslashAbsoluteScopeIsRefusedAtEveryAddingDoor`
(claim, dry run and authority add through the CLI beside accepted controls;
rm of a planted legacy row and of a name not held). Mutated in a private copy,
each test watched to fail: the original check order restored, each removal
door made strict, the held function forgiving every refusal, and the hint
dropped (five mutants, all killed). Review: full. The Codex design pass found
nothing blocking and set the shape above (a sentinel, not words; the comma
check and beat strict; NUL deferred). The code pass found no defect, and the
coverage gap it named (the literal "", "." and "../x" at both functions) is
in the table. With no code fix, no re-verify was owed.

## D-101 — The coordination claim is `orc-<project>`, never a bare `orchestrator` (amends D-030, D-062)

2026-10-20. Operator's decision, from a side finding of run2's orchestrator.

**The failure** — D-030 published coordination state in a claim named
`orchestrator`, and D-062's recipe kept the name. The chat alert tells a
session about every unalerted message, in EVERY room the journal holds
(`Journal.Addressed` walks them all), that names one of its claim slugs
(D-009). So every repo's coordinator wore the same token, and a message in
one repo's room saying "orchestrator" alerted the coordinator of every other
repo on the machine. Observed by this repo's orchestrator on 2026-10-20: an
alert naming it for a message in another repo's room. Measured in the
journal the same day: 14 of the 29 retained chat messages (one room,
2026-10-15..20) contain the word, each an alert to any session holding the
claim anywhere.

**The decision** — the claim is named `orc-<project>`, where `<project>` is
the main checkout's directory name (`orc-buddy-system` here). It is unique
per repository on one machine as long as checkouts have distinct names, it
still reads as the role, and peers still address the coordinator by slug
(`buddy msg orc-<project>`), which is what D-009 is for. The skill, the
USAGE reference and every recipe line that named the slug say so; D-030's
mechanism (a claim's `--desc`, no reserved slug, no compel path) is unchanged.

- **Nothing in buddy reads the name.** There was never a reserved slug
  (D-030), so no code changes and no old claim breaks: a coordinator still
  holding `orchestrator` works as before and just keeps the cross-repo
  alerts until it re-claims under the new name. A handoff moves the claim
  under the name it has (`release --to` keeps the claim id, D-063).
- **Not done.** Deriving `<project>` in code (a `buddy` default or a
  reserved prefix): it would make a name meaningful to buddy, which D-030
  cut. Scoping the alert to the session's own room: the room name is derived
  from the label and is wrong in a linked worktree (D-024), and a lane in a
  worktree is exactly who needs the coordinator's mentions. The isolated
  fixtures in the qualification scripts keep `orchestrator`: they run in a
  private ledger and the journal is not involved.

**Tests** — none new: a recipe-text change. The skill tests
(`TestSkillNamesOnlyWhatHelpAnswers`, `TestSkillTeachesEveryVerbAndFlag`)
still read every `buddy <verb> --flag` the skill teaches. Review: chore.

## D-102 — A vanished hook cwd is discovered from its deepest existing ancestor (amends D-044)

2026-10-20. Measured in run1; Fable's rule (c2), built at full tier by the
operator's decision.

**The failure** — In run1 a lane removed its own linked worktree, which lived
INSIDE the main checkout (`<main>/.claude/worktrees/<lane>`). Its hook payload's
`cwd` then named a directory that did not exist. git answered `fatal: cannot
change to '<cwd>': No such file or directory` (exit 128, probed), which is not
"not a git repository", so `resolveRepo` fell to `hasGitAncestor`. That walks UP
lexically from the dead path, found the checkout's `.git`, and D-044's
`ledgerProvablyAbsent` found the ledger PRESENT: "repo discovery failed: exit
status 128", and the gate denied every Bash call the lane made, the `buddy msg
... DONE` it owed included. A worktree removed from BESIDE the checkout had no
`.git` above it and got `errNoLedger`: silently allowed. So the verdict for one
fact, "the cwd is gone", hung on where the directory used to be, and the
comment calling a deleted cwd "ambiguous ... fails closed" was false in the
sibling case. A missing cwd proves nothing about any ledger (it is neither
invariant 3's "provably no repo" nor "exists but unreadable"); it proves that the
session's home is gone.

**The rule** — the verdict is what a session whose cwd were an EMPTY DIRECTORY
at that path gets today. `resolveRepo` (now the hook-facing wrapper over the old
body, `discoverRepo`) does this only when discovery failed AND:
- the cwd is absolute and `Lstat` says exactly `ENOENT`. EACCES, ENOTDIR, ELOOP,
  git missing from PATH and every other failure keep today's verdict.
- none of `GIT_DIR`, `GIT_COMMON_DIR`, `GIT_WORK_TREE`, `GIT_CEILING_DIRECTORIES`,
  `GIT_DISCOVERY_ACROSS_FILESYSTEM` is set (Codex design pass: a RELATIVE
  `GIT_DIR` resolves against a different directory from the ancestor, and git
  exempts the starting directory from a ceiling, so the ancestor's answer would
  differ from the child's). With one set it stands down to today's verdict
  (D-044's: a `.git` above with its ledger present denies, with it provably
  absent is feature-off). Rare.

The ancestor is found the way the kernel resolves the path, one component at a
time from `/`: a symlink's target is spliced in front of the components still
to go (at most 32 links, darwin's ELOOP bound), and a `..` applies to the
PHYSICAL directory reached so far. Once a component is missing, the rest would
be created with it, so later names deepen a count of missing levels and a `..`
climbs back out of one; the answer is the directory they would be created in.
Any error but ENOENT, a non-directory with more path after it, or a link with
an EMPTY target gives up (XNU fails that lookup with ENOENT and no directory
below would repair it; the split read it as `.`, re-verify pass; measured that
APFS creates one).
Two Codex findings set this shape. A DANGLING link must be followed, not
climbed past: an empty directory at `<plain>/link/lane` lives in the link's
target, and climbing to `<plain>` read a cwd under a paused repo as feature-off
(design pass). And a `..` in a link's target must not be collapsed lexically:
with `link -> jump/../gone` and `jump -> <repo>/sub` the kernel lands in
`<repo>`, and `filepath.Clean` landed in `<plain>` (code pass). Discovery then
runs once from that ancestor, and both ends are re-read: a cwd recreated
meanwhile gets its own verdict, and an ancestor that vanished meanwhile denies
(its disappearance is no evidence of feature-off). An ancestor moved away and
back between the reads is not seen (code pass): the TOCTOU residual the gate
already accepts.
`errNoLedger` from the ancestor is feature-off; any other failure, and a ledger
that then fails to open, denies with `hook cwd <x> no longer exists; discovered
from <ancestor>: <cause>; close the pane, or start the session in a directory
that exists` (D-084's register: the cause and the fix).

- **Relative paths are unplaceable.** A relative tool path from a vanished cwd
  (Codex names patch files relative to its cwd) takes the gate's existing
  unresolvable-target arm: refused where the home ledger or any target's
  ledger governs, feature-off where none does. This is a deliberate exception
  to the definition: the lane's claims were on ITS worktree's repo-relative
  files, and `<lane>/x.go` now places as `.claude/worktrees/<lane>/x.go` in the
  checkout and would read unclaimed. With no home ledger (the sibling case) a
  relative `../main/x.go` into a governed repo is refused too (Codex design
  pass; HEAD allowed it). `beat` attributes no dirty path, and a lifecycle
  PostToolUse renews no claim, for such a path; absolute ones are unchanged.
- **Shell-routed patches.** `shellPatchDirs` places every base whose `Stat` is
  ENOENT (a vanished cwd, a `cd` under it, or a `cd ..` that lands on a
  dangling link's own entry) with the same walk, where it used to `canon` it
  and climb lexically past a dangling link into a repository the directory
  would not be in (code pass; the `cd ..` case, re-verify pass). That also
  covers a dangling `cd` target from a LIVE cwd, which the shell could not
  enter anyway; the cwd itself stays a base. Its embedded patch targets keep
  the lexical walk: at worst a refusal of a shell-routed patch, which the tool
  would take.
- **Hooks only.** `buddy init` and `buddy diagnose` call `discoverRepo`: init
  CREATES a ledger where discovery lands, and a vanished cwd must not turn the
  feature on in whatever repository encloses it. Verbs get the cwd from
  `os.Getwd`, which fails (and `main` exits 1) in a process whose own cwd was
  removed, so this repairs a stale PAYLOAD cwd, the measured case.
- **What it costs.** Nothing on the hot path: the `Lstat` runs only after
  discovery has already failed (every edit outside a repo, one syscall). The
  fallback adds one or two git processes in the rare vanished case.
- **Accepted.** A dead cwd under an unrelated repository applies THAT
  repository's brake and claims, as git would for a real subdirectory there; a
  deleted independent repository nested in another registers into the outer
  one. Both are what an empty directory there gets. The re-read narrows the
  TOCTOU window and does not close it (the charter's accepted residual).
- **Not done.** Allowing a vanished cwd wholesale (Fable's (a)): it drops the
  home ledger's brake on a path-less call, so a paused lane's `git push` and a
  Codex `apply_patch <<EOF` with no `cd` would pass. Denying it wholesale ((b)):
  the measured brick. Matching git's "cannot change to" prose (the `Lstat` is
  the test). ENOTDIR (a component that is now a file; an empty directory there
  would need it removed). A pre-existing qualification Codex named and this
  does not change: under a chmod-000 directory `hasGitAncestor` cannot see a
  `.git` inside it, so an EACCES cwd can still read as feature-off when no
  `.git` above is visible.

**Tests** — `deadcwd_test.go`, each watched to FAIL on main 14923ef first:
a removed worktree inside the checkout (path-less Bash allowed; an absolute
write on a peer's claim denied naming it, unclaimed allowed; a relative write
denied naming the cwd); `pause` still stopping it after the cwd is gone (the
positive control that the home ledger was found); a worktree removed beside the
checkout (path-less allowed; absolute governed claim denied; a relative write
into the governed repo denied; outside every repo allowed); an EACCES cwd and an
ENOTDIR cwd still denying, each with the vanished-path control allowing; a
refused (format 99) ancestor feature-off with no ledger and denying, naming the
cwd and the ancestor, with one; a dangling link into a paused repo followed;
a `..` in a dangling link's target (relative, absolute, and out of a missing
component) resolved physically into a paused repo, and one climbing physically
OUT of it allowed while it is paused; a link with an empty target keeping
today's deny; a shell-routed patch (bare, and after `cd ..`) from a vanished
cwd behind a link out of the repo feature-off, with a vanished cwd inside it
refused; a discovery knob (`GIT_CEILING_DIRECTORIES`) standing the
fallback down; `init` creating nothing; `beat` and a lifecycle PostToolUse ignoring a relative path
and keeping an absolute one; the re-read after ancestor discovery (a cwd
recreated as an unparsable repository, an ancestor moved away), through a test
seam; and a dangling ledger symlink found from the ancestor denying with the
cwd named. Mutated in a private copy (python edits, `.git` excluded), each
watched to fail: two of Fable's three (walk the wrong levels, skip the
relative-path refusal) plus the beat and lifecycle skips, init
through the fallback, the env stand-down, a dangling link climbed past, a link
target collapsed lexically, a `..` ignored and a `..` out of a missing level
deepening it, an empty target read as `.`, a file mid-path not giving up (with
the re-read off), shellPatchDirs climbing lexically and keeping its
pre-re-verify condition, each re-read, the openRepo wrap, the ancestor error
unwrapped, and the whole fallback reverted: eighteen, all killed. Fable's third
SURVIVED, and is equivalent, traced on the fixture: dropping the ENOENT check (any error
on the cwd or a step counting as missing) alone, and with the cwd re-read off
too. For EACCES the deepest directory the walk reaches is the unsearchable
directory itself, which git cannot enter either, so discovery from it denies
exactly as today; and the re-read alone already sends every non-ENOENT cwd back
to today's verdict. Review: full (Codex design pass; code pass, two defects,
fixed; re-verify, one partial fix and one new defect, fixed; second re-verify,
both fixed, no new wrong verdict).

## D-103 — The orchestrator uses agent tools itself (a subagent is not a lane)

2026-10-26. Operator's standing decision, given to orc-5 in three steps ("you
yourself can use agent tools as well"; "make that a standing rule as
orchestrator, agent tools can be used"; "it should be durable, or even built
into buddy-system/skills").

**The failure** — an orchestrator's context is the one thing a fleet cannot
replace cheaply. Each handoff past `BUDDY_HANDOFF_AT` (300k here) costs a fresh
~60k-token prompt write and the time to re-read the handoff file, and the
orchestrators on this machine kept reaching it: orc-3 handed off at 322k, orc-4
at 397k, lane co-home-7 at 426k. Much of what filled those contexts was side
work done in the orchestrator's own pane: reading lanes' diffs, running reports,
deleting branches, checking CI logs. A subagent's context is disposable; the
orchestrator's is not. The rule allowing it had stood since 2026-09-07 ("use
agent tools as needed, standing rule"), but only in the operator's private
CLAUDE.md and memory, which no lane and no successor reads, so each new
orchestrator re-derived it or asked.

**The rule** — while it runs a fleet, the orchestrator uses agent tools for its
side work: subagents (the Agent tool, any model), Codex passes through
`scripts/codex-review.sh`, and Fable reads, for reading a lane's diff, reports,
measurements, housekeeping and a second opinion. Its own context is the scarce
resource before its handoff size; a subagent's is not.
- **A subagent is not a lane.** It holds no claim, lands nothing and relays
  nothing; it is never launched as a lane and never messaged as one. It runs
  under the orchestrator's session id, so the gate sees the orchestrator's
  claims (D-099: its hook calls are the parent's), and the orchestrator claims
  the files before handing it an edit.
- **Unchanged.** The never-delegable list (push, install, settings,
  permissions, trust, a more permissive mode; D-083). A Codex pass goes only
  through `codex-review.sh` and is never backgrounded. A subagent's verdict is
  not test evidence: what it finds is evidence once a test reproduces it.
- **Not done.** A buddy verb or ledger row for a subagent: it has no identity
  of its own in the ledger, D-025 and D-099 already treat its hook calls as
  the parent's, and nothing in the ledger should name one. Counting a
  subagent's tokens against the orchestrator's handoff size: the transcript
  reader counts the main thread's last turn only, by design (invariant 10).

**Tests** — none new: skill prose, a CLAUDE.md bullet and this entry; no verb
or flag. The skill tests still read every `buddy <verb> --flag` the skill
teaches. Review: chore.

## D-104 — The review tier follows the change class; a receipt names what ran; the model is pinned per tier

2026-10-26. Operator's decision, given to orc-5: certain items take the full
gate "with a fable/codex review", others the lite one; "lets also make this
pattern part of the skill where possible/generic enough". The pattern is taken
from another repository's review gate on this machine.

**The failure** — the tier text here said only how many Codex passes each tier
owed. Nothing said a full change gets a panel or a third read, nothing made a
receipt say at what effort its second model ran, and `chore` listed "a test-only
change" as an example, so a test of what the gate refuses could ship with no
review at all. Measured over this repo's log (`git log`, the first body line
beginning `review:` per commit; 55 commits carry one, 2026-09-07..10-26): 36
chore, 12 lite, 3 full, 4 untiered; 4 of the 36 chores touched only test files
(7 name a test in their receipt, one of those a records and docs commit); 0 of
the 15 lite and full receipts names an effort; 0 names a panel or a cross-check.
The model was not pinned per tier either: every lane on 2026-10-26 ran on Fable
because each orchestrator copied the last one (the operator's note recording the
default), and two of that day's three items were bounded fixes and a script run.
The source repository measured what the missing layers buy: an independent
second-family review found 3 real issues (2 critical) a same-family review
missed; a second-model pass on a script below its full tier found an
option-injection hole four other layers had missed; re-verify rounds caught two
high-severity defects introduced while folding; and 8 of 14 receipts in one
stretch named the second model with no effort, so a pinned pass and a silent
fallback were the same string (all four read by this lane from that repository's
decision record; it is not named here).

**The rule** — the tier is picked per item by the change's CLASS, never by the
model or the diff's size, named in the lane's brief and in the commit's receipt.
- **full** (here: what the gate refuses or allows, the ledger schema,
  `internal/fence`, an invariant): an adversarial panel of read-only subagents,
  spawned without asking (no edits, builds, tests or writing git in the shared
  checkout; anything that mutates gets its own worktree at a commit); a Codex
  design pass before the code and a code pass after; a Fable cross-check,
  required; and a Codex re-verify of the fixes, until it accepts or the brief's
  bound.
- **lite**: a scoped adversarial self-review and ONE Codex pass on the finished
  diff, findings fixed or answered; one round, no re-verify loop; Fable
  optional. "At most one" became "one": the pass is what makes it lite rather
  than chore, and an operator's cap ("optional") still waives it for a run.
- **chore**: no outside review required, with the reason in the receipt.
- **Every tier.** A second model is allowed at any tier; the tier says when it
  is required. A test-only change takes its class's tier (amends this file's old
  chore example; past receipts are not revisited). The receipt is the last line
  of the commit body and names each layer that ran, each second-model pass with
  its own effort (the cross-check names its model):
  `review: full (panel + codex@xhigh design + codex@xhigh code + fable + codex@xhigh reverify)`.
  A layer the tier owes is skipped only on the operator's word (a cap), and the
  receipt names it with the reason. Tests and the mutation check
  are owed at every tier; a review is never test evidence. The operator caps the
  tier per run and a lane never raises its own above it, nor lowers the brief's
  on its own. A panel subagent is not a lane (D-103).
- **The model per tier**: a full-tier item, its panel and its fold rounds on the
  strongest model available at high effort; lite and chore on the operator's
  default lane model, a standing decision in the operator's instructions (Opus
  5.5 at high effort as of today). The skill names it as that day's default,
  never as a constant, and the fleet recipe's launch line carries the model and
  effort. `docs/BEYOND-CODE-PLAN.md` section 5's pick rule, if it lands,
  replaces the fixed per-tier default with a per-class one.
- **Decided by class** (the operator, 2026-10-27 00:08, relayed by orc-5): a
  full-tier item runs on the strongest model available, with its panel and fold
  rounds, at high effort; lite and chore on the default lane model; the
  operator may still name any item for either. The operator's own instructions
  were aligned the same night. The skill's "unless the operator's instructions
  pin it otherwise" is that naming escape.

The skill carries the pattern generically ("Review depth: the tier follows the
change class"): which classes are full and which script runs the second model
are the repo's own, named in its CLAUDE.md; the source repository is cited only
as "another repo's review gate".

- **Not done.** A receipt CHECK script here (a done-check warning when a full
  receipt lacks the cross-check or a layer its effort): proposed as a follow-up
  at lite, not built now, because this entry is the first to define the receipt's
  shape and a check written before any receipt follows it would be tuned to
  nothing. A tier column in the ledger or any table: this repo keeps the tier in
  the brief and the commit, and nothing in buddy reads or enforces it (the same
  reason D-103 gave a subagent no row). Naming models in the skill as constants:
  the skill is installed user-level for every repo, and the default moves with
  the operator's decisions (it moved on the day this was written).

**Tests** — none new: skill prose, CLAUDE.md, this entry and a charter GIVEN; no
verb or flag. The skill tests (`TestSkillNamesOnlyWhatHelpAnswers`,
`TestSkillTeachesEveryVerbAndFlag`) still read every `buddy <verb> --flag` the
skill teaches, and the first draft failed one: a bare backticked `--model` was
read as a flag of the nearest `buddy` verb. Review: lite (codex@xhigh + fable
cross-check).

## D-105 — `msg` signs an id the ledger does not know as `unregistered s-<8hex>`, never as the operator

2026-10-27. The operator's round of 2026-10-27, relayed by orc-6;
`docs/BEYOND-CODE-PLAN.md` section 4 step 3 (Codex finding 10 on that plan).

**The failure** — a session not registered in this ledger signed as the
operator. Measured 2026-10-26 (the plan's section 2): from another repo's
checkout, a session registered only in this repo's ledger ran `buddy msg <that
fleet's coordinator slug> --dry-run` and was told "would send … as operator",
with its native id set or unset. `senderFor` looked the environment's id up,
found no live row, and fell through to the no-session arm: "operator", or the
`--from` it typed with nothing ahead of it. The same arm took an id whose row
had ENDED. So a peer's text arrived under the one name a recipient treats as
the operator's, and nothing on the line said otherwise. Correction ownership
was already right (D-043's `senderSession` records such a sender as UNKNOWN);
only the words were wrong. The plan's evidence is that a session CAN address a
ledger it is not registered in, which a fleet of fleets makes routine.

**The rule** — a session id in the environment (`BUDDY_SESSION`, then
`CLAUDE_CODE_SESSION_ID` or `CODEX_THREAD_ID`, the order `environmentSession`
already keeps) that is not a LIVE row here, absent or ended, signs
`unregistered s-<8hex>`: the short form a hello under that id would mint
(`defaultLabel`'s suffix). `--from` is a tag after it exactly as after a label:
`unregistered s-66a24e29 (review)`. The bare terminal (no id in the
environment) is unchanged: `operator`, or what `--from` typed. The send's
result line, and the dry run's, says so ON the line, ahead of any wake
(D-041's `| head -1` reader): `signed "unregistered s-66a24e29": your session
id was not found as a live session in this ledger (never said hello here, or
ended); buddy hello --session <id> --label <name> registers you here`
("not found", because a failed read takes the same arm, the safe direction). A hello revives an
ended id (D-032), so the one command serves both cases. The stamp is a
NOTICE, not a reply address: unlike a label (D-019) it resolves to nothing
(`buddy msg "unregistered s-66a24e29"` is no such target, and a bare
`s-<8hex>` resolves only after a default-label hello); a reply goes to the
label the sender registers.
- **Unchanged.** Never refused (`senderFor`'s settled line: a message is never
  refused for want of a signature). `senderSession` and correction ownership
  (D-043), the drain headers (D-097), and the 64-byte `fence.Line` of the
  sender on a drained line and in `sent`: the stamp is one stored sender
  string, rendered by every view exactly as a label is. `pause` and
  `resume` carry no sender stamp, so nothing changes for them.
- **`release --to`'s notice** shares `senderFor`, so it is signed with the
  ENVIRONMENT's identity, not the holder's: `whoAmI` finds the holder by
  `--session` or the cwd, so `release <slug> --session <live holder> --to X`
  run under an unregistered id signs `unregistered s-<8hex>`, and its result
  line now carries the same clause (orc-6's review).
- **Not done.** Refusing the send, or requiring `hello` first: the settled
  line above, and the cross-ledger case is exactly the one where the sender
  has no row to make. Signing with the full session id: the short form is the
  name every other view uses, and it fits the 64-byte column with a tag.
  Stamping the ledger the sender IS registered in: buddy reads one ledger per
  cwd and would have to search for it (the plan's option C, not taken).

**Tests** — `TestUnregisteredSenderIsStampedNotOperator`, one fixture per row:
an absent id under each of the three environment variables, an absent id with
`--from`, an ENDED id with and without `--from`, and an id holding a space
(pasted single-quoted) or a newline (not pasted: `<your session id>`, the
stale note's rule for a name it cannot print as typed); each through the dry
run, a real send and bravo's drained line. Controls in the same table: a live id
(its label, with and without the tag, no clause) and the bare terminal
(`operator`, and `--from` alone). `TestReleaseToUnderAnUnregisteredIdSaysSo`:
the handoff notice's stamp and clause, the holder's own id as control.
`TestUnresolvedSenderIsNotTheOperator` and
the other D-043 correction tests are unchanged and green. Review: lite
(self-review + codex@xhigh code; orc-6's read-only review folded).

## D-106 — The A-01 re-review's open shell-recognizer forms (env -S jurisdiction, script, over-budget option values) are closed with regressions

2026-10-27. The operator's choice for this item (relayed by orc-7): FULL tier,
Opus 5.5 at xhigh with Codex as the second model and an Opus xhigh subagent
cross-check (not Fable). Closes the forms a narrowed constrained Codex re-review
of `wrapper()` and `shell()` reopened on 2026-10-12 (audit A-01; prompts,
verdicts and kernel probes archived read-only at
`~/Archives/buddy-system/2026-10-12-a01-rereview/`, SHA256SUMS beside them).
That round's own fixes (the ninth code pass) had never been re-reviewed; the
re-review found five it confirmed by source and refuted one as stated.

**The failure** — the shell-patch recognizer (`internal/cli/patch.go`, D-073,
D-087) returned neither a found route nor an uncertainty for shell commands a
real shell runs `apply_patch` for, or failed to discover the governed directory
one changes into. Each line below is the one-line reproduction; each ran on this
box against a stub `apply_patch` first on PATH (BSD `env`/`script`; fish not
installed, so F1/G1/G2 have no kernel control — they were FIXED from the stated
behaviour, below):

- **A** `x=; env -S "$x" -C /governed apply_patch`: env still processes the
  options AFTER a dynamic `-S` value (empty or option-only `$x` leaves `-C` in
  force), but the uncertain branch returned before reading `-C`, so from an
  ungoverned cwd `/governed` was never discovered. (`env -S "" -C SUB pwd -P`
  lands in SUB.)
- **B** `env -S '-C /governed #' apply_patch`: BSD/macOS env splits `-C
  /governed` out of the `-S` string and ignores the `#` comment, changing
  directory before the trailing argv runs; the escape/comment branch skipped the
  whole `-S` text, so the `-C` inside it was never discovered. (`env -S '-C SUB
  #' pwd -P` lands in SUB.)
- **C** `script -q /dev/null <<'SH'` … `apply_patch` … `SH`: BSD and util-linux
  `script` with a transcript file (or none) and NO command spawn `$SHELL`, which
  reads its script from the inherited stdin; `wrapper()` consumed the transcript
  operand and reached the end of argv without reading stdin. (Runs only when the
  heredoc is PADDED past `script`'s pty line-discipline buffer — 200 `:` lines
  never ran here, 600 ran 8/8 with zero leaked children; this is why the
  re-review's short-body probe saw it not run.)
- **E** `env -u {X,-S,apply_patch,x{1..300}}`, `bash -o
  {posix,-c,apply_patch,x{1..300}}`, `bash --rcfile {f,-c,apply_patch,x{1..300}}`,
  `env -S {apply_patch,x{1..500}}`: an over-budget brace word consumed as an
  option VALUE was skipped unchecked, but a shell expands it IN PLACE into more
  options and a command (`env -u X -S apply_patch …`, `bash -o posix -c
  apply_patch …`). All four run apply_patch here.
- **D** as stated (`fish -c true -c 'apply_patch'`, read as first-operand-only)
  is **refuted**: `shell()`'s `operands[1:]` already get speculative analysis.
  The real repeated-`-c` gap is F1, below.

**The rule** —

- **A.** When env's `-S` value is dynamic, keep reading env's argv so a later
  `-C`/`--chdir` is discovered and the literal after it is found; `script -c`'s
  value is its whole command line with no further directory-changing argv, so it
  still returns. Harness dimension (D-087): A changes the CODEX verdict only.
  Under Claude a dynamic value is no refusal, so the walker already read past it
  to the literal and discovered `-C`; under Codex it stopped at the uncertainty.
- **B.** In the `-S` escape/comment branch, recover directory changes lexically
  from the `-S` text itself (`w.lexicalDirectories`, honouring quotes), then keep
  reading env's argv. Best-effort BOTH ways: the lexical scan does not model env
  `-S`'s own escapes, so it may add a directory a real env would skip (a `#` that
  BEGINS the split string) AND may miss one `\_` (its separator) or `\c`
  (truncation) would change — it is NOT "only over-refuses" (FULL-review
  cross-check corrected this). The recorded uncertainty still refuses a Codex
  call where a ledger governs. B changes BOTH harnesses: jurisdiction discovery
  is always non-literalOnly, so the skipped `-C` was missed under either.
- **C.** A wrapper that reaches the end of its argv with no command runs a shell
  reading the inherited stdin, so `script` (like `sudo -s`) calls `stdinScript`
  there; a command operand or `-c` breaks the scan earlier, so the end means
  none. C is a literal `apply_patch` read from the heredoc, so it refuses under
  every harness. `exec` is not `script` and sets no shell mode, so the arm does
  not fire for it (the cooperative limit below).
- **E.** An over-budget value word refuses as an exhausted bound — the walk
  stopped before it could rule out a literal — in the generic wrapper value-skip,
  in `shell()`'s `-o`/`-O` and `--rcfile`/`--init-file`, and in the `-S` value
  itself. Exhaustion refuses under every harness, and only where a ledger governs
  (invariant 3).

**Considered and cut** —

- **Modelling `env -S`'s escapes and `${VAR}` expansion** (so B could re-analyze
  the split string precisely instead of lexically): the escapes include `\c`
  (truncate) and `${VAR}` (runtime value), which a static reading cannot resolve;
  the lexical recovery plus the recorded uncertainty is the cheap conservative
  side, as elsewhere in this recognizer.
- **A one-line fix for the `exec <<'SH'` limit** (round 9's remaining crafted
  P2, a persistent redirection a LATER statement's shell reads): the walker
  scopes a redirection to its own statement; carrying it to sibling statements is
  not one line, and it stays the documented cooperative limit (D-073, USAGE). A
  regression asserts the gate ALLOWS it where governed WITH a kernel control
  proving a real shell runs it, so the gap is genuine and accepted, and it guards
  the C fix from over-reaching to `exec`.
- **fish F1/G1/G2 are FIXED (the operator's decision, 2026-10-27, relayed by
  orc-7), from the stated behaviour in verdict3.md, not re-derived from fish's
  source** (a turn re-deriving the parser was stopped by a safety classifier; the
  operator chose to work from the stated behaviour and fish's documented
  options). In `shell()`: fish runs EACH `-c` command, so a repeated `-c` whose
  later value is dynamic is read and refuses under Codex (F1); `--no-execute`
  (G1) and a `-n` before the attached `c` (G2) set no-execute, so fish only
  checks syntax and the command is not analysed — ALLOW. fish is NOT installed
  on this box, so every fish kernel control is NOT RUN, said loudly.

**Second fold (the FULL-tier review's codex@xhigh CODE pass, REJECT 10, and
opus@xhigh cross-check, ACCEPT WITH CHANGES 9).** Five of the ten codex findings
were regressions the first follow-up fold introduced. Folded:

- **Playback precision** (codex 1,2): the first fold read 'T' and the 'p' inside
  an attached `-O/tmp/x.log` as BSD playback, wrongly allowing util-linux `-T`
  (timing — a shell DOES run) and `-O` (log). Playback is now only a bare `-p`/
  `--replay`; a clustered `-qp` over-refuses, the safe side.
- **`script -c` supplies a command** (codex 5): with a dynamic value the scan
  continued under Claude into the new stdin arm — a false denial. A `scriptCmd`
  flag suppresses the arm.
- **`env -S echo x{1..300}`** (codex 6): the first fold's over-budget-tail guard
  false-denied it (echo is a data command whose args decide nothing). The guard
  is removed; the crafted inverse it would have caught (`env -S '-u' {…}`) is
  recorded open below.
- **Over-budget value/action consumers** (codex 8,9,10 + cross-check): the `--`
  lead-operand loop, `findActions`, `gitRoute` (any word), `scriptFile` and
  `trap` each refuse an over-budget brace as exhausted (it expands in place into
  a command). `--log-timing` was added to the script table (codex 3).
  **SUPERSEDED in rounds 1-2:** the any-word `gitRoute` guard (kept for `-C`'s
  value only) and both `findActions` guards were reverted as false-denying data
  braces; those positions are recorded OPEN below.

**Recorded open (respellings, named here and in the audit row; orc-7's
allowance):**

- **codex 4** — `env -S"$x" -C /gov apply_patch` (ATTACHED dynamic `-S`): the
  whole word is unknown, so the wrapper breaks before `-C`; the literal is still
  found (refuses where the cwd/patch is governed) but the ungoverned-jurisdiction
  `-C` is not discovered. Narrow; the attached-dynamic word is the cooperative
  dynamic-text boundary. A shell's attached `-c"$x"` is the same boundary (round
  5 N2, below).
- **codex 7** — util-linux `script -c true -c apply_patch` (repeated `-c` runs
  the last): util-linux only, NOT RUN here, crafted, pre-existing.
- **git action-position over-budget** (codex 10, widened) — `git -C .
  {bisect,run,apply_patch,x{1..300}}` and `git submodule {foreach,apply_patch,…}`:
  a blanket `gitRoute` over-budget guard would catch them but false-denied data
  pathspecs (`git diff -- path{1..300}`, re-verify r1 finding 5), so the guard was
  reverted to `-C`'s value only. Crafted, NOT RUN here.
- **F5 / the static `env -S` option-tail** — `env -S '-u' {X,-S,apply_patch,
  x{1..300}}`: round 1 closed it with a data-command-first-field check, but round
  2 REVERTED that check (it false-denied `env -S 'env echo' …` and quoted/
  option-prefixed data forms, re-verify r2 finding 5), so F5 is OPEN again, with
  the `env -S env {…}` wrapper tail (re-verify r3 finding 4 corrects the earlier
  "F5 is NOT open" note).
- **nested find action-position over-budget** — `find . -exec true
  {x,\;,-exec,apply_patch,x{1..300}} \;`: round 1 closed it with an inner
  separator-scan over-budget check, but round 2 REVERTED that (it false-denied
  `find -exec grep -l foo path{1..300}`, re-verify r2 finding 4), so this nested
  reopening is OPEN, alongside the top-level `find . {-exec,apply_patch,…} \;`
  (re-verify r3 finding 5). This SUPERSEDES the round-1 "closing a nested-action
  bypass" statement below.
- **cross-check** — GNU `env`/`script` abbreviated long options (`--s=`, `--c`;
  fish's `--com`/`--init` and its documentation example's `--init-cmd` too, round 4);
  an unknown wrapper in front (`lockf -k f script …`) hiding C and env forms
  (pre-existing); util-linux `script -t`'s OPTIONAL value. Each verified by the
  cross-check; unbounded or util-linux or pre-existing, NOT RUN here.

**Tests** — in `internal/cli/shellroute_test.go`:
`TestGateShellRouteReReviewForms` (C, four E forms, `exec <<'SH'` cooperative
limit), `TestGateShellRouteReReviewJurisdiction` (A under Codex, B both),
`TestGateShellRouteReReviewFollowUp` (F4 allowed, F1a/F6 refused, finding-6
allowed), and `TestGateShellRouteReviewFolds` (playback precision, `script -c`,
the over-budget consumers, fish F1/G1/G2). Each refused form carries a kernel
control where its shell is on this box (C/F1a/F4 darwin + padded past script's
pty buffer; A/B via `pwd -P`; find/trap over-budget run here); util-linux script
options, `timeout`, fish, an active git bisect and a sourcing-stdin shell are NOT
RUN here, said loudly, with the gate's verdict asserted regardless. 18 guards
were reverted in a private rsync'd copy (python, not sed) and watched to fail
(11 original + 7 fold guards); the over-budget consumer guards reuse the E
pattern's verified over-budget→exhausted and are covered by their deny-budget
tests.

**Re-verify round 1** (codex@xhigh, REJECT, 8; five were side effects of the
second fold). Folded: fish's long `--command` joins `fishCmds` so it is analysed,
not dropped in operands (finding 1); the fish `-c` case guards `len(t) > 1`
before indexing (a `fish -c true ""` PANIC, finding 2); an over-budget `env -S`
tail exhausts UNLESS the split value's first field is a data command, which
denies `env -S env {…}` and re-closes F5 while keeping `env -S echo {…}` allowed
(finding 3 — **SUPERSEDED in round 2: this data-command check was reverted, F5
and `env -S env {…}` are OPEN**); the `-exec` inner separator scan checks
over-budget, closing a nested-action bypass (finding 4 — **SUPERSEDED in round
2: reverted; the nested `-exec … {…}` reopening is OPEN**); the blanket
`gitRoute` over-budget guard was reverted (it false-denied `git diff --
path{1..300}`, finding 5) — the subcommand-position brace is recorded open
above (finding 10). Documented, not
changed: BSD `script -T` is playback, but `-T` is kept OUT of playback
recognition because util-linux `-T` is timing and DOES run a shell, so the
deny over-refuses BSD `-T` playback — the safe side for a security gate
(finding 6). Docs reconciled: F5 removed from the open lists (now fixed —
**SUPERSEDED in round 2: F5 is OPEN again**, above), the
audit A-01 row's fish line (finding 8), and the charter/audit open lists
(finding 7).

**Re-verify round 2** (codex@xhigh, REJECT, 9) then showed round 1's find and
env-`S`-tail over-budget guards false-denied common DATA braces: `find -exec
grep -l foo path{1..300}`, `env -S 'env echo' path{1..300}` / `'"echo"'` /
`'-i echo'` (quotes, leading options and wrapper prefixes defeat any static
first-field test), and the find primary brace `find . -name foo{1..300}`. Per
the operator's explicit direction — a false denial costs every Claude session a
tool call; a crafted brace-budget bypass costs nothing under Claude's allow arm,
and round 3 is the last — this fold is the CONSERVATIVE one: the find
over-budget guards (both the inner separator scan and the outer primary scan)
and the env-`S`-tail data-command check (so finding 3 of round 1 is reverted
too) are REMOVED, the tail always masked. The crafted bypasses they would have
caught are recorded OPEN below. Also fixed: fish init-commands (`-C`/
`--init-command`) are deferred to `fishCmds` so a LATER `--no-execute` suppresses
them (finding 6); `fish --command` with no value sets `fishSawCommand`, so it is
a fish error, not a stdin shell (finding 7); the audit A-01 table row's fish line
(finding 8); and the `env -S echo {…}` allow test gained an executable control
(finding 9). Recorded OPEN (crafted, with reasons; orc-7's allowance): `env -S
'X=/bin/echo' {…}` (assignment then over-budget brace, finding 1), `x=-u; env
-S"$x" echo {…}` (attached-dynamic `-S`, finding 2 — the attached-dynamic residual
is budget, not only jurisdiction), `git submodule {foreach,…}` and the `git -C .
{bisect,run,…}` and `find . {-exec,…}` action-position braces (finding 3; the
git residual is wider than bisect), and the `env -S env {…}` / `env -S '-u' {…}`
wrapper/option split-value tails.

**Re-verify round 3** (codex@xhigh, REJECT, 5) caught a P1 regression the round-2
fold introduced — fish init commands (`-C`/`--init-command`) alone made the early
return skip stdin, so `fish -C true <<'SH'…` was allowed — and a pre-existing
`fish -nC` false denial. Fixed: the fish early return is gated on `fishSawCommand`
ONLY (init commands are analysed but do not suppress stdin, finding 1); the fish
`-C` cluster processes a preceding `n` like the `-c` branch (finding 2); the
`kernelRuns` control fails on a shell LAUNCH error so a negative control cannot
pass vacuously (finding 3); and the F5/nested-find OPEN records and the round-1
note's superseded "closed" statements are corrected (findings 4, 5). The operator
then directed a FOURTH re-verify on this fold before landing (so this is not the
bound after all).

**Re-verify round 4** (codex@xhigh, REJECT, 6; folded by lane a01-2) found round
3's own side effect, a P1: `fish -p/tmp/initCommands.prof -c 'apply_patch <
p.patch'` was ALLOWED under both harnesses — the `-C` branch took the first `c`/`C`
anywhere in the cluster, so it read the C inside `-p`'s attached value as `-C` and
the n before it as `-n` (finding 1; denied on `cbaa253`). The fix is the parse,
not a guard: fish's `-` clusters are read in ONE case as getopt reads them, letters
in order, the FIRST value option (`-c -C -d -o -f -p` per fish's documented
options, and fish 3's `-D`) taking the rest of the cluster or else the next word,
so no letter after it is an option; the long value options (`--profile`,
`--profile-startup`, `--debug`, `--debug-output`, `--features`,
`--debug-stack-frames`) consume their detached value. That also closes three
pre-existing literal bypasses of the same shape: a detached value read as the
script file (`fish -p /tmp/p -c 'apply_patch …'`, `fish --profile /tmp/p -c …`)
and fish's `-o` value read as bash's `-o noexec` (`fish -o noexec -c …`); a
consumed value word over the brace budget still exhausts (finding E). It frees one
crafted form: `fish -pc 'apply_patch …'` gives `-p` the value `c`, so the quoted
text is the script FILE (cooperative), allowed. Two false denials fixed: a value
option ending the line (`fish -C true -C <<<'apply_patch'`, `--init-command`,
any value option) is a fish usage error, so fish exits before reading stdin
(`fishUsage`; the commands before it are still analysed, which can only
over-refuse) (finding 2); and `--version`/`-v` and `--print-debug-categories`
print and exit while fish reads its options (documented), so they suppress
everything like `-n` — one more term in the existing `--no-execute` case and the
cluster's flag letters (finding 3, crafted). Re-checked against the round-3 forms
beside them: init-only stdin still DENIES, `fish -nC apply_patch` still ALLOWS,
`-Cn`/`-cC`/`-Cc` still give the text after the letter as its value. Docs
corrected: the charter's "`findActions` refuses over-budget action values" (those
guards were reverted; finding 4), the audit table's OPEN summary now names F5 and
the round-1 "F5 … (now fixed)" note carries its supersession (finding 5), and the
stale fish "on hold"/"stay open" text (finding 6). Recorded, not fixed: fish's
GNU-style abbreviated long options (`--com`, `--init`) join the abbreviation entry
above, as does `--init-cmd`, the spelling in fish's own documentation example
(unverified: not a prefix of `--init-command`); fish `-h`/`--help` is not
modelled, the gate reading the `-c`/stdin route regardless (fish may run `-h` as
a help command beside a `-c`, so whether that over-refuses is unverified; round 5
N6), and an unknown fish option, on which fish exits, may over-refuse, the safe
side. fish is NOT installed on this box: every fish kernel control is NOT RUN.
Tests: `TestGateShellRouteReVerifyR4` in `internal/cli/shellroute_test.go`.

**Re-verify round 5** (Fable, the operator's choice, run by orc-8: ACCEPT WITH
CHANGES; the six round-4 fixes FIXED, no regression) named pre-existing forms the
fold did not touch. The operator's decision: fold N1 narrowly, record the rest.

- **N1, FIXED** — `fish -c source <<<'apply_patch < p.patch'` (also `source -`,
  `.`) was ALLOWED under both harnesses: fish's `source` with no file, or the file
  `-`, reads its commands from stdin (documented), but `scriptFile` returned on no
  operand and passed over `-`. The `source`/`.` arm in `command()` now reads stdin
  (`stdinScript`) in exactly those two cases, in front of `scriptFile`, so a
  shell's own script-file operand is unchanged. A here-string or heredoc refuses
  under every harness; a pipeline into `source` is uncertain (Codex refuses, Claude
  allows, D-087); `source -` from a FILE is cooperative, like a script file.
  Documented over-refusal, kernel-checked on bash, zsh, ksh and sh: each refuses
  `source` with no file and opens a file named `-`, running nothing, so `source
  <<<'…'` in those shells is now refused though it runs nothing; so is a PROGRAM
  named `source` fed shell text on stdin (`./source <<<'…'`, the command name is a
  basename). Round 6 (F1): fish's `source` ends its options at `--`, so one
  leading `--` is dropped first and `fish -c 'source --'`/`'source -- -'` with a
  here-string refuse too; `source -- f.fish` still names a file.
- **N5, FIXED** — `fish --debug-level 3 -c 'apply_patch …'` (fish 3.0's spelling
  of `-d`) is one more `fishValueLong` entry; on a current fish, which rejects the
  option, consuming its word only over-refuses.
- **N2, OPEN** — the ATTACHED dynamic `-c` (`bash -c"$x"`, `fish -c"$x"`): the
  whole word is unknown, so it is read as the script-file operand and the Codex
  uncertainty the detached `-c "$x"` raises is missed (both harnesses allow). The
  same cooperative dynamic-text boundary as codex 4's `env -S"$x"`, above.
- **N3, N4, documented over-refusals** — fish has no `-s`, but `shell()` reads it
  as bash's stdin flag (`fish -s <<<'apply_patch'` refused; fish exits on the
  invalid option); and a lone `-` ends the options and reads stdin, as in bash,
  where fish would open a file named `-` (`fish - <<<'apply_patch'` refused). Both
  the safe side.
- **N6** — the `-h`/`--help` sentence above is hedged: not modelled, direction
  unverified.

Tests: `TestGateShellRouteReVerifyR5` in `internal/cli/shellroute_test.go` (N1's
three refused forms and N5 beside an ungoverned-cwd control, the two pipeline
forms, two allowing controls, and the bash/zsh over-refusal with its kernel
control RUN here; fish controls NOT RUN).

Review: full — adversarial self-review + panel (3 read-only Opus subagents:
bypass sites, fixture-respelling, false-denial direction) + codex@xhigh design
(on drafted code: the diff preceded the prompt) + codex@xhigh code (REJECT, 10;
folded) + opus@xhigh cross-check (ACCEPT WITH CHANGES, 9; operator's choice, not
fable) + codex@xhigh re-verify round 1 (REJECT, 8) + round 2 (REJECT, 9) + round 3
(REJECT, 5) + round 4 (REJECT, 6), each folded + Fable re-verify round 5 (the
operator's choice, run by the orchestrator; ACCEPT WITH CHANGES, N1/N5 folded,
N2-N4/N6 recorded) + round 6 (Fable, ACCEPT WITH CHANGES, all P3: F1 `source --`
folded, the measurement wording, a test comment and the N1 record corrected).
The operator's decision: land after round 6's fold, no 7th re-verify. Tests:
`TestGateShellRouteReVerifyR6`.

## D-107 — The gate's hook line fails closed when an installed `buddy` dies

2026-10-27. orc-7's dependency review of 2026-10-27 (the operator: "do it");
the operator's choice on the cross-check's first finding, relayed by orc-8.

**The failure** — the documented and installed PreToolUse line was `[ -x
"$HOME/bin/buddy" ] && "$HOME/bin/buddy" gate; exit 0`. `buddy gate` says deny
with one JSON line on stdout and exit 0, and allow with nothing, so a `buddy`
that DIED before printing left stdout empty, and `; exit 0` made the death the
hook's "no decision": Claude Code let the tool call through, the opposite of
invariant 2. Measured at dd7daff: `modernc.org/libc` v1.67.1's netdb init reads
`/etc/services` before `main` and panics on any error but ENOENT; under a
`sandbox-exec` profile denying that read, `buddy` exited 2 silently and the
line exited 0 with nothing on stdout. The libc v1.73.4 pinned since tolerates
a permission error there (measured, same profile: a deciding gate; EIO, EISDIR
and ELOOP still panic, by its source), so that trigger is gone and the class is
not: `cp` over a running Mach-O gets the next run SIGKILLed (CLAUDE.md), any
Go panic exits 2, and a corrupt binary exits 126. Whether a real harness
sandbox denies that read was not established; the kill path needs none.

**The rule** — README's entry, held in one place (`scripts/gate-hook.py`):

    [ -x "$HOME/bin/buddy" ] || exit 0; { "$HOME/bin/buddy" gate; } 2>/dev/null || { echo "buddy gate failed (status $?); refusing the tool call (the gate fails closed). Operator, from a terminal: sh scripts/install.sh in the buddy-system checkout" >&2; exit 2; }

with matcher `Edit|Write|NotebookEdit|Bash`, `"timeout": 10` and `"onFailure":
"block"`. Read from Claude Code's hooks reference (2.1.311): exit 2 blocks a
PreToolUse call whatever stdout holds, with stderr as the reason unless stdout
holds a blocking decision, whose own reason is used (a buddy that printed its
deny and then died is refused with the deny's reason); any other
non-zero exit, a timeout, or a hook that cannot start lets the call through
unless `onFailure` is `"block"` (2.1.295 and later). So:
- **No executable at `$HOME/bin/buddy`:** exit 0, silent. Installation is the
  opt-in; a machine with no binary has no feature to fail.
- **`buddy` exits 0:** its stdout passes through untouched, a deny or nothing.
- **`buddy` exits non-zero, a signal included:** exit 2 with ONE fixed stderr
  line naming the fix. Its own stderr is discarded with the brace group's: a
  panic's text is multi-line and unfenced, and would be the model-visible
  reason when stdout holds no blocking decision (invariant 9; the design
  pass). The gate writes nothing to stderr on any path that decides.
- **A timeout, or a shell that cannot start:** `onFailure: "block"`.
  Documented, not measured: no native run has timed one out.
- **The reach, the operator's choice.** A `buddy` that cannot run cannot tell
  a repository with no ledger from one with, so an installed one that dies
  refuses every matching call in every session on the machine, outside any
  repository and Bash included. The cross-check named it a breach of invariant
  3; the operator accepted it (a broken installed binary is a machine fault,
  and the refusal names the fix) and scoped invariant 3, charter GIVEN 2 and
  `SECURITY.md` to "while `buddy` runs". README names the operator's off
  switch, `chmod -x ~/bin/buddy` (the `[ -x ]` guard), and the refusal does
  not: the model reads it.
- **`install.sh`** reports, never edits: step 6a decodes `~/.claude/settings.json`
  and prints ONE line judging its PreToolUse gates together: README's, LEGACY
  (with README's entry to paste), LEGACY beside README's (runs twice, decides
  nothing when it matters), or custom with "NOT VERIFIED" and the first
  reasons found, in this order: one gate hook's own (a dropped `onFailure` or
  `timeout`, an `if`, `args` or `shell` key), else a matcher not read as an
  exact list or missing a tool, else `disableAllHooks`. `build()` builds
  BESIDE the target, runs `help` and a `gate` probe (a constant Write payload,
  no `cwd`, in a fresh temp directory named under `$TMPDIR`), and renames the
  build in only when both exit 0. The probe's verdict is not read: a TMPDIR
  inside a governed repository may deny, and that is a decision too. A build
  whose gate died used to be in place before its probe ran, refusing every
  call machine-wide, the installing agent's Bash included.
- **Codex.** USAGE's Codex example is wrapped the same way WITHOUT the `[ -x ]`
  guard: wiring an absolute path is Codex's opt-in, so a missing binary
  refuses; Codex blocks on exit 2 with stderr as the reason. An edited Codex
  hook is skipped until re-trusted in `/hooks`, so a wrapped line is fail-open
  until then. `buddy diagnose --codex-hooks` reads it `marked`, as it reads the
  bare line.
- **AGENT-SETUP** replaces the legacy entry on an upgrade rather than keeping
  it beside the new one.

**Considered and cut**
- Capturing stdout and printing a fixed deny JSON with `exit 0` (the
  dependency review's first proposal): it needs `$(...)` capture and JSON
  quoted inside a JSON string, and an exit-0 verdict depends on Claude parsing
  that JSON, where exit 2 depends on the code alone (the design pass).
- A shell-level no-ledger shortcut (look for `buddy.db` before refusing): the
  line cannot discover a ledger the way the gate does (the git common dir of
  the cwd and of every target, linked worktrees' `commondir`, nested
  repositories D-073, a vanished cwd D-102), so it fails open across
  repositories and in linked worktrees.
- Passing a crash's stderr through as the reason: unfenced, multi-line.
- `[ -s ]` against a zero-byte executable: one instance of the class below,
  named instead.

**Not done, accepted**
- A binary that runs and exits 0 having decided nothing: an executable
  holding only `:`, or a ZERO-BYTE executable, which every shell runs as an
  empty script (exit 0, nothing on either stream; measured under sh, bash,
  dash and zsh). `install.sh` renames a probed build into place, so it never
  leaves one.
- A timeout on a Claude Code build before 2.1.295 (no `onFailure`), and what
  such a build makes of the unknown key: not measured. No native run exercises
  README's line (`qualify-claude-mods.py`'s control is a bare gate).
- Codex has no `onFailure`: a Codex gate that times out or cannot start is a
  failed hook, and the call goes ahead (its hooks reference).
- An allow slower than 10 s now blocks (the gate measures 20 ms).
- A session cwd with no search permission: `os.Getwd` fails and `cmd/buddy`
  exits 1, so the call refuses (a Go probe; native not run). A REMOVED cwd
  never reaches it: Claude Code 2.1.311 runs hooks from the original cwd, the
  project root or home then (its hooks reference), and on macOS `buddy` exits
  0 in a removed cwd anyway (measured).
- `[ -x ]` false mid-copy: a hand copy's window; `install.sh` renames.
- `buddy-adapter`'s own process killed: its wrapper denies every error from
  `buddy gate`, not its own death; its strict gate check rejects an
  `onFailure` key, and adapter workers launch with `--setting-sources ""`, so
  README's entry never reaches them. That needs its own qualified contract.
- A future gate deny on stderr with exit 2 would still block, its reason lost
  to the redirect; no such path exists.
- `install.sh` now replaces a non-object file at a target by the rename, where
  `go build -o` refused one.
- `diagnose` reads bare and wrapped Codex lines alike; a "bare gate fails open"
  finding was not added.

**Tests** — `scripts/check-gate-line.sh` (in `check.sh`): README's entry is
`gate-hook.py`'s, whole; the exact command under `sh`, `dash` and `bash`
against the real gate (its deny byte-equal to the bare binary's, two allows)
and stubs, every HOME holding a space: absent and non-executable silent; exits
1, 2, 126, 127, a real Go panic, SIGKILL before and after partial JSON,
deny-then-exit-1, hostile multi-line stderr and a missing interpreter each exit
2 with only the fixed line; the OLD line still lets the killed stub through
(its marker proves the stub ran); the install report on 15 settings shapes and
an over-deep file. `scripts/check-install.sh` QA-9 (the report, the file left
byte-identical), QA-10 (a new buddy whose gate dies is not installed),
QA-11 (the probe from a relative `BUDDY_BIN_DIR`, and with a `"` in TMPDIR),
QA-12 (a killed buddylist names itself, not the gate). A `diagnostic_test.go`
row: the wrapped Codex line reads `marked` with no finding, and
`TestUsageCodexHookExampleDiagnosesMarked` now reads USAGE's wrapped example
(both die when the walk stops at a brace group). Mutation: 28 mutants in
private copies, each killed by its check, that walk and the probe's die line
among them; M21 and M22
(the relative path, the quote in TMPDIR) first SURVIVED because macOS's bare
`mktemp -d` ignores TMPDIR, so the probe's directory is now named under
`$TMPDIR` by template. Review: full (panel x3 opus + codex@xhigh design +
codex@xhigh code + opus@xhigh cross-check, the operator's choice, not fable +
codex@xhigh reverify x3).

## D-108 — The gate refuses gh stack's rewriting subcommands without `.buddy/slot/gh-stack`

2026-10-27. The operator (17:00): a known hazard gets enforcement with the
feature, never "later if it gets used often"; gh-stack v0.3.0 was adopted the
same day, with the skill's recipe (run19). The decisions on the design's open
questions relayed by orc-8 (~17:20).

**The failure** — a restack rewrites lanes' branches IN THEIR OWN WORKTREES
(`gh stack rebase` runs `git -C <lane's worktree> rebase --onto …` for every
stack branch), a plain `gh stack rebase` fetches origin and can fast-forward
trunk, and one conflict holds the stack catalog's lock
(`<git-common-dir>/gh-stack/`) for EVERY stack until `--abort`/`--continue`.
The gate never saw any of it: `gh` sat in `dataCommands`
(`internal/cli/patch.go`), `analyze` returned before `command()`, and every
`gh stack …` was a pathless Bash call, pause-only (the trial: nothing refused
it, nothing consulted a claim). The recipe says only the session holding
`.buddy/slot/gh-stack` runs it; this is the gate's half of that rule.

**The rule** (`internal/cli/ghstack.go`, `cmdGate`):
- **Grammar.** `gh` leaves `dataCommands`; `command()` routes `gh` to `ghRoute`
  and the extension binary `gh-stack` (by any path; `commandName` takes the
  basename and folds) to `ghStackArgs`; an UNKNOWN program's bare operand `gh`
  or `gh-stack` is a command position too (`sandbox-exec -p "$off" gh stack
  init …` is exactly how the recipe runs it; the `--opt=gh` value form is not
  read). `ghRoute` reads only gh's grammar: `stack <sub>` and
  `extension|ext|extensions exec stack <sub>` (measured, all three; `exec
  gh-stack` is "not found"); every other gh command's operands stay data and
  are never scanned, so `gh pr comment -b "run gh stack rebase"` and `gh api
  repos/{owner}/{repo}/pulls` pass as before. Leading dash words are read
  past to reach the command and the subcommand: before gh's command every
  dash word is skipped and the next word is read as the command (`gh
  --help=false stack rebase` RAN rebase; gh refuses `gh --x stack` and `gh --
  stack` itself, so those are over-refused rather than reading gh's unpinned
  root flags, and `gh -R x stack rebase` reads `x` and passes, right while gh
  has no value-taking root flag: the cross-check); after `stack`, the word after a
  value-shaped flag (`--x` or a two-character `-x`, no `=`, not help or
  version) is tested as the subcommand and, when it is not gated, skipped as
  the value, because cobra's stripFlags hands it to the flag and finds the
  subcommand after it: `gh stack --base main init a b` RAN init and `-m wip
  add x` RAN add (the panel's bypass; the first draft read `main` as the
  subcommand and allowed). Both readings are refused, so `--base rebase` stays
  an over-refusal (and `--base rebase init a b`, which runs init, is refused
  naming `rebase`: the wrong constant on a correct refusal, the cross-check)
  and the rule holds if a root flag is added. An empty literal word is
  skipped at every position the route reads: cobra drops it and runs the next
  word (`gh '' stack --version` dispatched, `gh-stack '' rebase` RAN rebase,
  `gh-stack '' init a b` ran init; the code pass asked, measured). CERTAIN text only: the route never
  reads a quoted operand of an unknown program (speculative text, parsed for a
  patch literal), because that reading denied the fleet's own commands:
  `sed -i '' 's/gh stack rebase /…/'` parses as words whose first word's
  basename is `gh`, `awk '/gh stack rebase /…'` the same, and a prompt
  `claude -p "take the slot; gh stack rebase"` or one with the phrase in
  backticks (the panel's false-denial pass). The bare operand `gh` of an
  unknown program is still read.
- **Gated:** rebase, sync, push, submit, merge, link, unstack and its alias
  delete, init, add, modify, checkout (Q1: it fetches and imports), alias (Q2:
  it installs `~/.local/bin/gs`, a spelling the gate cannot see). Allowed:
  view, bottom/top/up/down/trunk/switch (they move the CALLER's HEAD, as `git
  checkout <branch>` does), feedback, completion, help, `-h`/`--help`/
  `-v`/`--version` alone, `gh stack help <x>`, `gh help stack`, an unknown
  subcommand (the root prints help). No flag modelling (Q3): `unstack --local`,
  `rebase --abort/--continue`, `--no-trunk`, `--remote self` all go through the
  slot holder. The slot is the only test.
- **Help forms, measured on the extension binary** (v0.3.0, a non-repository
  directory, network denied): `rebase --help --help=false`, `--help=false
  rebase` and `--help --help=false rebase` all RUN rebase (pflag lets a later
  flag cancel help); `rebase --help`, `rebase -h x`, `help rebase`, `-- rebase`,
  `-- init a b`, `--help init a b`, `-v init a b` and an unknown word print
  help or the version and run nothing; `--base main init a b`, `-m wip add x`
  and `--base -- init a b` RUN their subcommand, `--base rebase` and `--base
  main --remote origin init a b` fail on the unknown flag. On gh: `gh
  --help=false stack rebase` RAN rebase; `gh --help stack` and `gh --version
  stack` reach the extension and run help or fail on the subcommand's flags;
  `gh --x stack`, `gh -R x stack`, `gh -xy stack`, `gh -- stack`, `gh
  extension --help=false exec stack`, `gh extension exec --help=false stack`
  and `gh extension exec -- stack` are refused or "not found" by gh. So help
  passes only as the sole word after the subcommand; `rebase -h x`, `rebase
  --no-trunk --help` (Q4), `--help rebase`, `-- rebase`, `--base rebase`, `-h
  init` and `gh --x stack rebase` are refused: documented over-refusals of
  forms nobody types.
- **The slot check.** The caller is admitted when an OPEN claim owned by the
  payload's `session_id` covers `.buddy/slot/gh-stack` by invariant 14's
  containment on invariant 13's fold (one `ClaimsTouching` read; a claim on
  `.buddy/slot` or `.buddy` admits too, D-035's own note; a claim UNDER the slot
  reserves nothing of it). No incarnation comparison: an open claim always
  carries its owner's current incarnation (Hello over an ended session orphans
  the old one's claims in the same transaction), the same predicate
  `denyIfHeld`'s `OwnerOf` applies to every path. A subagent's hook carries the
  parent's `session_id` (D-099), so a holder's subagents are admitted with it.
  One slot per checkout (Q6): gh-stack's catalog and lock are one per git
  common dir, the directory `buddy.db` lives in, so every linked worktree
  shares both and no mapping is needed.
- **Jurisdiction.** Every governed ledger the command can reach is asked: the
  hook cwd's and each literal directory change the walker discovers
  (`shellPatchDirs`: `cd`, `pushd`, `env -C`, `sudo -D`, `git -C`, `~`). ALL
  must admit; the first refusal wins; an unreadable one DENIES (invariant 3);
  a TARGET repo's pause applies (F-28); none governed is feature-off. A slot
  held in checkout A does not admit `cd B && gh stack rebase` (the design
  pass's P1), and discovery now also feeds the ambiguous-identity arm from an
  ungoverned cwd, which used to run it only for a patch (P1, the second). A
  ledger with no worktree root (bare) refuses rather than placing the slot
  against nothing.
- **Harness split: none.** A dynamic word at the command, `stack` or
  subcommand position is OPEN under EVERY harness (`gh stack "$op"` passes
  under Codex too): D-087's "cannot rule out" arm guards Codex's in-process
  patch engine, and gh stack is a child process under either harness. A
  literal refuses under every harness where a ledger governs. An over-budget
  brace word at a position the route reads (`gh {stack,x{1..300}} rebase`,
  the `exec`, `stack`, subcommand and help positions) exhausts everywhere, as
  `git -C`'s value does; anywhere else in gh's argv it is data (`gh pr list
  --label x{1..300}`, `gh stack rebase --remote x{1..300}` keep their own
  verdicts). D-087's own classes are unchanged, and the one harness split
  there is inherited: script text produced at run time (`echo 'gh stack
  rebase' | bash`) and static text the bash parser rejects but another shell
  runs (`() { gh stack rebase }`, zsh's anonymous function; `env -S 'gh stack
  rebase #'`) are refused under Codex as uncertain and never read under
  Claude (the panel's bypass pass).
- **Precedence.** Pause, then the shell-patch deny (`gh stack rebase &&
  apply_patch < p` is refused as a patch, slot or no slot; `refuses()` ignores
  the gh match and recognition keeps reading past it), then the slot, then the
  paths (a Bash call has none).
- **The deny**, one line (invariant 9; label 64, slug 128, desc 512, repo root
  512 through `fence.Line`; the subcommand is the table's constant): what runs,
  why the slot (the shared catalog and lock, other lanes' branches), then
  `Nobody holds .buddy/slot/gh-stack` and the `buddy claim gh-stack … --scope
  .buddy/slot/gh-stack` fix line, or the holder (`held by session <label> (slug
  "<slug>": <desc>; <state>)`) with D-090's register: ENDED "your own buddy
  claim displaces it" (D-026, no sweep asked for), GONE and SILENT "ask the
  operator: buddy release <slug> --session <holder id>" (D-022: it refuses
  like a fresh one; a bare `buddy release <slug>` releases the CALLER's claims
  and answers "not yours", the panel), live "ask the holder: buddy msg
  <name>" where the name is `msgTargetFor`'s (a slug `all` would broadcast;
  the holder's id then), and the slug and the holder's id are printed as a
  command, both shell-quoted, only when each pastes back as printed (a
  newline or an over-long value names them instead; the code pass: the
  ledger does not restrict a session id's characters); `— in the TARGET repo's buddy ledger (<root>)` and
  `(in <root>, after buddy hello --session <id> there)` for a discovered repo,
  since a claim needs a live sessions row in that ledger (D-088). A claim an
  overlapping one refuses is named by that refusal, not promised here. The
  home ledger is checked by its handle, never by a key match.
- **Cost (Q7).** `buddy gate` on `gh stack rebase --no-trunk b` in a fixture
  ledger with two sessions, n=30 each: before the arm, allow, median 12.6 ms
  (p90 13.0); after it, the holder allowed median 12.7 ms (p90 13.1), a lane
  denied median 12.6 ms (p90 13.3). One claims read on the allow path; the
  process table only to word a deny; nothing on a non-gh Bash call but the
  brace expansion gh's words now get, like git's.

**Considered and cut**
- Flag modelling (`--no-trunk`, `--remote self`, `--local`): pflag's `--remote
  --no-trunk` makes `--no-trunk` a remote NAME; `--remote self` is `git config`
  at hook time, outside the 100 ms budget; each distinction is a fresh false
  allow or false deny in a recognizer where the ten A-01 re-verify rounds each
  introduced a defect. The recipe's discipline, not the gate's.
- A `pre-rebase` git hook: fires only for `git rebase`, only where
  `setup-clone.sh` ran, cannot tell gh-stack's child rebase from a lane's own,
  and sees no session.
- Parsing gh aliases (`~/.config/gh/config.yml`), the `gs` wrapper, `git config
  gh-stack.remote`: the A-01 enumeration failure, a YAML dependency, the budget.
- A Codex-only uncertainty for a dynamic gh word (D-087's count: ~10 denials,
  0 protections). A per-stack slot (two restacks gh-stack itself serializes). An
  exact-match special case beside containment (a second rule). A `buddy who
  gh-stack` READY clause in the deny (READY is the recipe's condition, not gate
  evidence). An incarnation comparison in the gate (the ledger guarantees it).
- Scanning the operands of known data programs for `gh`, or reading the
  `--opt=gh` value form as gh's argv.

**Recorded OPEN** (cooperative, each with its control pinned ALLOW so a fold
is deliberate)
- A dynamic word at the command, `stack` or subcommand position (`$GH stack
  rebase`, `gh "$c" rebase`, `gh stack "$op"`, `gh stack ${X:-rebase}`), script
  text produced at run time under Claude (`echo 'gh stack rebase' | bash`,
  `bash -c "$(…)"`), script files, `make` recipes, git aliases, `python -c`.
- `gs rebase` (the alias `gh stack alias` installs; also Ghostscript), gh's own
  aliases, `GIT_DIR=/other/.git gh stack rebase` (a workdir passed outside the
  command; a literal `GIT_DIR=` could join discovery as HOME does, one fold at a
  time), a bare terminal.
- A word written at run time: `echo rebase | xargs gh stack`, `parallel gh
  stack ::: rebase`, `f() { gh stack "$@"; }; f rebase` (the subcommand
  position is empty or dynamic in the text the gate reads); a dynamic word
  among the leading flags, `gh stack --base="$b" init a b` and `gh stack
  "$flag" init a b`, ends the route before the literal `init` (the code
  pass; pinned ALLOW; closing it needs the walker's word to carry its literal
  dash prefix).
- A value-taking root flag gh does not have today (`gh -X v stack rebase`
  would hand `v` to the command position the route reads; gh is not pinned,
  the cross-check).
- The bare operand `gh` of an unknown program is a command position, so a
  program handed files literally named `gh`, `stack` and `rebase` (`sed -n
  1p gh stack rebase`) is refused: the accepted cost of the recipe's
  `sandbox-exec … gh stack init` shape (the code pass; pinned).
- A chain of more than 128 directory changes from an ungoverned cwd whose
  governed landing is past discovery's cap is not placed (the patch arm's
  boundary, D-073; crafted).
- A workdir the walker does not model (D-073's class, the same gap as a
  shell-routed patch): `uv run --directory /B gh stack rebase`, `pnpm -C /B
  exec gh stack rebase`, `tmux new -d -c /B 'gh stack rebase'`, `cd "$B"`,
  `cd -`, `CDPATH`, the `pushd` stack: the home ledger is asked, B's is not.
- A quoted command line an UNKNOWN program runs: `watch 'gh stack rebase'`,
  `watch -n 60 'flock /tmp/l gh stack rebase'`, `flock -c 'gh stack rebase'
  f`, `su -c '…'`, `ssh host '…'`, `tmux new -d '…'`, `tmux send-keys
  '…'`. Speculative text is not read for gh (above), so no prose, pattern or
  prompt is denied (`claude -p "gh stack rebase next"`, `sed 's/gh stack
  rebase /…/'`, `awk '/gh stack rebase /'` all pass, pinned), and these run
  forms pass with them. The first two run it locally: cooperative, recorded.
- gh's words now count toward the per-hook brace budget (1,024 fields) as
  git's do; a hook with over a thousand brace fields in gh commands followed
  by an unknown program's brace word exhausts where it did not before (the
  panel; incidence about zero).
- The association is by command, not by statement: `gh stack rebase; cd B`
  needs B's slot (over-approximation).
- A DELAYED hook from a dead incarnation of the same session is admitted by the
  live incarnation's claim: `session_id` alone cannot tell them apart. Every
  gate check's limitation (invariant 12), inherited, not introduced.
- "Rewritten without the commit gate" is git's: rebase, cherry-pick, revert
  and `am` never run pre-commit, and plain `git rebase` does the same today.
  This change does not close that and must not be read as closing it. A lane
  with UNCOMMITTED edits is protected by gh-stack itself ("uncommitted changes
  in worktree"); a committed, parked lane is protected by the recipe's READY.
- The recipe's network discipline (`sandbox-exec` with `(deny network*)`;
  `init` and `rebase --no-trunk` send read-only GraphQL queries carrying the
  operator's token) is the recipe's; the gate does not model sandboxing.

**Measured natively** (Q5, orc-9, 2026-10-27, the installed buddy at 579784f,
gh 2.95.0 + gh-stack v0.3.0): a throwaway repo (`git init`, branch `b`,
`buddy init`), every gh call under `sandbox-exec` denying the network and
writes under `$HOME`. (a) A live session's real PreToolUse hook on
`cd <repo> && … gh stack rebase --no-trunk b`, nobody holding: DENIED,
naming the slot ("Nobody holds .buddy/slot/gh-stack. Take it first"). (b) A
stand-in session holding the slot in the target repo's ledger while the
caller held it in its cwd's ledger only: DENIED, naming the holder and the
TARGET repo's ledger. Both ledgers the command reached had to admit. (c) The
stand-in released; the caller `hello`ed in the repo and claimed the slot
there: ALLOWED, and gh-stack ran (exit 2, "branch b is not part of a stack",
its own answer for an empty repo). (d) The stand-in's call while the caller
held the slot: denied, naming the holder ("ask the holder: buddy msg
gh-stack"). Not native: (d) was a PreToolUse payload through the installed
`buddy gate`, not a second live session's hook.

**Tests** (`internal/cli/ghstack_test.go`): 73 refused spellings, each with a
kernel control where the box runs it (recording `gh`/`gh-stack` stubs first on
PATH under `/bin/bash`, `/bin/zsh`, `/bin/sh`; `sandbox-exec` and `caffeinate`
darwin-only), under both hook shapes, from both worktrees, nobody holding and
then the holder admitted while the other session is refused naming it; 76
allows (ordinary gh use, every data program the phrase is typed into, the
dynamic and recorded-open forms pinned so a fold is deliberate); containment;
ENDED/SILENT/GONE holders and the quoted id; jurisdiction (ungoverned cwd,
cross-repo, an unreadable target ledger, the identity arm, a bare repo by cwd
and by `cd`); precedence and the budget positions; a subagent under the
holder; the one-line fence. **Mutation** (an rsync'd private copy, python
edits, each watched to die): 23/23 killed: gh back in dataCommands; checkout not gated; equality for containment; the owner filter dropped; the operands() arm dropped; a Codex-only uncertainty on a dynamic gh word; the identity arm discovering only for a patch; allow at the first ledger that admits; help exempt at any position; leading dash words not read past; a target's pause ignored; a bare repo placed against nothing; an over-budget route word not exhausted; the deny naming the input word; speculative text read for gh (both guards); the release line without --session; a value flag's word not read as the subcommand; gh's leading dash words not skipped; the value word skipped without the gated test; an over-budget value not exhausted; an empty word read as the subcommand; an empty word before gh's command ending the route; the holder id printed unquoted. **Review:** full (panel x3
opus: bypass sites, false denials, deny text and identity; codex@xhigh design,
round 1 timed out at the watchdog, round 2 accepted with two P1s folded;
codex@xhigh code, accepted, two forms recorded open and three questions
folded; fable cross-check, accepted, four folds; codex@xhigh reverify x1,
accepted). Timing per Q7 above.
