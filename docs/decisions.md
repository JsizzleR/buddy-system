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
