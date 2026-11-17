# A zero-cost park for a session waiting on a MESSAGE — design note (nothing built yet)

2026-11-17 · run r1117b, lane `msg-wait`. Stage 1 class research; the code stage is **full tier**
(the operator, via orc-3 #1448, about 17:10Z), and **the schema change was accepted by the operator
in this lane's own pane at about 17:11Z** (D-109: a schema change is never taken by relay).
Measured from the code at main a96d9f4, rebased onto 59ccbea (sid-leftovers); nothing launched.
Folded in: one Codex design pass (xhigh, 8 findings) and a three-lens read-only panel (29
findings), each listed with its disposition at the end.

## The gap, measured

D-203's second measurement is this case: a lane waiting for its coordinator's GO message ran
`buddy wait --until 90m`, armed an 1800 s ScheduleWakeup, and was woken 40 s earlier by the
message itself; the stale wake cost 5 requests at ~286k. `wait --block` exists for a claim's
verdict (D-095, D-162): a background job whose exit IS the wake. With no `--on` it refuses
(`internal/cli/wait.go:138`). So a session parked for mail has three ways to wake today, and none
is its own: the sender delivering the harness wake (D-039; named only when the recipient is quiet
and reachable, and sent only if the sender's agent chooses to), a timer plus the keep-alive loop
(D-033; mail read at the next check, up to 50 m late, 2-3 requests per check), or a hand-rolled
background poll of `buddy status` (the failure D-095 was written against).

## How a message is stored and drained

- **Storage.** `inbox` (`msg_id INTEGER PRIMARY KEY AUTOINCREMENT`; `target` = the RESOLVED
  session id, or `all`: `Send` writes `t.ID`, `store/inbox.go:129`; `sender` = display text, the
  sender's `--from` verbatim when it has no session, `cli.go` senderFor; `sender_session` = the
  resolved sending session, '' no session in the environment, NULL unresolved, D-043).
  `inbox_recipient` is a broadcast's snapshot of every live session, the sender included
  (`inbox.go:145`). `inbox_delivery` is the delivery mark, written by the drains and by adapter
  delivery (`store/adapter.go:232`); `adapter_messages` marks an adapter's mail. Rows are kept 24 h.
- **Drains** (each under the write lock, D-099): `hello`, `busy`, `beat`, the Stop hook
  (`StopTurn`), `buddy inbox`. Each hook drain is bounded (20 messages, ~8 KiB), the Stop holds a
  turn open once (`stop_hook_active` skips the second), and under Codex hello, busy, idle and beat
  all refuse (`cli.go:1855, 2340, 2425, 2584`): only `buddy inbox` drains there. So mail can sit
  queued for a long time, below any watermark taken now.
- **`wait --block`** (`waitblock.go:103`) re-reads every 15 s, autocommit SELECTs only, keeps no
  cache warm, exits 0 LANDED / 3 EXPIRED / 4 cleared or replaced / 5 session ended, prints `wait
  check`'s lines, closes the row after the write, and honours a close another path recorded.
- **The register** (`session_waits`) refuses nothing and no gate reads it (invariant 10). Every
  surface renders ONE verdict computed at read time (D-020, GIVEN 36), loaded by `waitsWhere` in a
  single SELECT, one snapshot.

## The design: `buddy wait --for-mail [--until <dur>] [--note <text>] [--block]`

A wait whose LANDED is "you have mail": declared like a timer, blocking in the background like
`--on --block`, its exit the wake. Zero tokens while parked.

### The schema (20): one column

`session_waits.mail_after INTEGER NOT NULL DEFAULT -1`. `-1` = not a mail wait; `>= 0` = a mail
wait whose watermark is that msg_id. One ALTER arm (`addColumnIfMissing`), no index (none is
needed; one would go after the ALTER, as D-043's do). Existing rows read -1 and keep their meaning.
`IsMail() := MailAfter >= 0`, derived once in `waitsWhere`, never `> 0`: an empty inbox's
watermark is 0. The `--from` column of the earlier draft is CUT (below).

### Arrival: what lands a mail wait of session S

A message m counts when it is not adapter mail (`NOT EXISTS adapter_messages`, on every arm), was
not sent by S (`sender_session IS NOT S`: a lane's own broadcast or correction never lands its
own wait), is not a broadcast (`target <> 'all'`, below), and either:

- **queued**: `target = S` and S has no delivery row for it, whatever its id. Mail already waiting
  when the wait is declared lands at the first read: under the drain bounds, a held Stop, or Codex,
  it may never have reached the session, and a wait over unread mail must not sleep; or
- **arrived**: `msg_id > mail_after` and (`target = S` or S has a delivery row for it). A message
  drained by any path after the declaration (a woken session's `buddy inbox`, a beat) still lands
  the wait, so the block never sits to its deadline over mail already read.

The watermark is `COALESCE(MAX(msg_id), 0)` read INSIDE the declaration's write transaction:
AUTOINCREMENT never reuses, sends serialize on the write lock, so every later message has a
larger id. A `--for-mail` re-declaration over S's own open mail row (same incarnation) KEEPS the
old watermark and its `since` (the queued arm's bound: a fresh one dropped mail queued before the
old declaration and drained between the two, Codex re-verify): otherwise a message drained between the old block's last poll and the
re-declaration would be lost to both. The arrival (the first counting msg_id, its
`sender_session`, that sender's ledger label, its created time, whether S has a delivery row, and
the count of counting messages) is a correlated subquery in `waitsWhere`'s one SELECT, `NULL`
unless `mail_after >= 0`, so non-mail rows (and beat's per-call `waitNotice`) pay nothing and a
row and its arrival come from one snapshot. `Wait.Landed()` checks the mail arm before the
"no targets = timer" arm. LANDED wins over EXPIRED, as for claims.

**Broadcasts do not land a mail wait.** One `env -u … buddy msg all x` from any shell reads as the
operator and would end every parked mail wait at once, N turns at full context, re-armable every
15 s (panel, hostile lens). Today that send wakes nobody (no wake clause for a broadcast). A
broadcast still reaches the session by the drains as before; the BLOCKING line says broadcasts do
not wake it. A coordinator that needs parked lanes up sends to each, which is what the fleet skill
already does for assignments.

**`--from` is cut from v1.** Two lenses found the same failure: the main use is a lane waiting for
its coordinator, a coordinator hands off past 300k to a successor with a NEW session id (the
fleet's routine), and the successor's GO would never match: the block sleeps to its deadline. A
sender filter also needs an ended-sender rule, a repeated-flag rule and a NULL rule, all to avoid
a wake that, by the note's own measure, costs one short turn and grants nothing. Without it, any
direct mail to S lands the wait: mail to a parked session is something for it to read. orc-3's
Q4 ("operator mail lands whatever --from") becomes moot. If a filter is wanted later it is a new
column and its own item.

### The lines

- **Declaration:** `WAITING for mail to session <label> (direct messages; broadcasts do not wake
  it) after #<watermark> — deadline in <span>[; N already queued: they land it now]`, then the
  keep-alive line, then with `--block` `BLOCKING until mail arrives: …` and the exit codes. No
  "a timer never lands" line.
- **MAIL verdict** (check, block, beat's one-shot notice, clear): `MAIL: #<id>[ and N more] for
  session <label> from <sender> — <queued: run buddy inbox | delivery recorded: re-read it with
  buddy inbox <id>>; the wait is over after <span>; <stop step>`. `<sender>` comes from the ledger
  only: the `sessions.label` of `sender_session` through `fence.Field(…, 64)` plus its short id
  (D-204's `store.ShortID`), or the fixed words `a sender with no session` ('') or `an unknown
  sender` (NULL). **Never `inbox.sender`**: it is a sender's free text, and on a completion notice
  in buddy's own voice, outside the inbox's untrusted header, it could forge the rest of the line
  (invariant 8's reasoning, invariant 9). No body, no kind. The queued arm names plain `buddy
  inbox` (it drains and marks; `buddy inbox <id>` marks nothing and the next drain would inject the
  body a second time); `<id>` only once a delivery is recorded, since a delivery row proves a
  write to some shell, not that this model saw it. The session is named because a `/clear` in the
  same process inherits the old id's completion notice.
- **Every timer or claim phrase that would describe a mail wait** goes through one
  `waitPhrase(now, w)`: `targetsPhrase`'s call sites (declare, the "replaced your open wait" line,
  `wait ls`, `wait clear`, `who`/`status`, `waiterNote` in msg, which must not tell a sender "a
  timer (never lands)" of the wait its message lands), `deadlinePhrase`'s "every claim it waits on
  has closed", beat's `waitNotice`, hello's head and re-declaration recipe, and the usage line.
- **Hello after a revival** (the predecessor arm, `wait.go:1041-1080`): computes the ended row's
  arrival first; mail since → `MAIL #N arrived since: read it (buddy inbox …); nothing to wait
  for`, the mail form of "its claims have all closed since"; none → the recipe `wait --for-mail
  --until <left> --block`. The new declaration takes a new watermark (a new incarnation's row is
  not "its own open mail row"), which loses nothing: hello found no arrival on the ended row, and
  the queued arm covers anything still undelivered.

### Clear, and the stop step (D-203 amended)

- **`wait clear` on a mail wait that has landed closes it `landed` and prints the MAIL line**,
  computed inside ClearWait's transaction: a session that saw MAIL in `who` and cleared must not
  read CLEARED from its block (two verdicts for one wait). Claim waits keep today's clear.
- **The stop step's mail arm**: `woken by its mail: clear nothing (the block lands on it within
  15s); woken by anything else: leave it running — clear only if you no longer want the message`.
  D-203's "woken first: clear" was for a timer standing in for an outside wake; a mail wait is not
  over until the mail comes, and a lane woken by its own test job that cleared would have no mail
  wake left. The `buddy` skill's generic "woken first" bullet gets the same carve-out (the skill
  is read once, the line every time, D-203). The fixed words carry no "every" (D-203's cron trap).
- **keepAliveAdvice is otherwise unchanged**: "arm nothing if a wake is due within 50m (a job's
  exit, a promised message)" is right for the block. A park past 50 m on a 1 h tier arms the
  self-paced `/loop buddy wait check`; the check lands MAIL too, and whichever reads it first
  closes the row.

### A session woken by the harness wake address instead

The sender saw the wake clause and sent `buddy mail #N is queued for you (T): run buddy inbox`. The
recipient's turn opens on that peer message (D-039 framing); it runs `buddy inbox`, which drains
#N, fenced. Its block is still running; within 15 s it finds #N above the watermark with a delivery
row, prints `MAIL: #N … delivery recorded: re-read it with buddy inbox N`, closes the row, exits 0.
One more short turn inside the hour (cache warm), in which the session recognises #N and does
nothing. **One redundant short turn, accepted.** `msg` keeps naming the wake: the ledger cannot see
whether the block is alive (no pid recorded, D-095), and a wake withheld from a session whose
block died sleeps to its deadline.

## Does it change what anything refuses?

The gate, the commit gate, `claim`, the brake, the drains: nothing. No invariant changes; the
register stays a view (invariant 10). `wait`'s own usage refusals, each writing nothing, each
read inside `DeclareWaitReady`'s transaction where it depends on the ledger:

| Input | Today | Proposed |
|---|---|---|
| `wait --block` with no `--on` | refused | still refused, unless `--for-mail` |
| `--for-mail` with `--on` | n/a | refused: "every claim closed" and "you have mail" are two meanings of LANDED |
| `--for-mail` with `--ready` | `--ready` without `--on` refused (`store/wait.go:230`) | unchanged |
| `--for-mail` for a session that is not the harness's own (`--session`, `$BUDDY_SESSION`, no native id: the declaration's existing `self` test, `wait.go:180`) | n/a | refused: the completion notice would go to another harness or none |
| `--for-mail` while the session has an open `--ready` wait | n/a (it would replace it) | refused, naming `buddy wait clear` as the deliberate way to drop the READY (orc-3, #1421) |
| `--for-mail` without `--block` | n/a | allowed: a keep-alive check lands it (the schema makes that possible) |

## Migration (schema 19 → 20) and invariants 2, 3, 11, 12

- One ALTER arm and a "20 adds session_waits.mail_after (D-NNN)" line in `schemaVersion`'s
  comment. A test from a real 19 shape (an open timer row and a READY row, stamped 19) comes out
  with `mail_after = -1`, its verdict unchanged and `ready_sha` kept, on the model of
  `TestMigrationToSchema19`.
- **The upsert must write the column on every declaration** (`store/wait.go:291-296`):
  `mail_after=excluded.mail_after` in the SET clause, every non-mail caller binding -1. Without it
  a timer or claim wait declared after a mail wait inherits the watermark and lands on mail (panel,
  high). Tested by mutation: drop the SET term and watch the test die.
- Checked sound (migration lens): a 19 hook against a 20 ledger gets `ErrLedgerNewer` and the gate
  denies (D-037, invariants 2-3; chat hooks stay silent); a 20 dev build against a 19 ledger
  without `BUDDY_MIGRATE=1` gets `ErrMigrationRefused` (D-136); buddylist never migrates; a fleet
  ledger migrates on `~/bin/buddy`'s first open with its `.pre-schema20.bak`; a 19 binary's
  already-running `--block` keeps reading named columns harmlessly to its end (no recheck added);
  a mail close is a verdict, not a reap (invariant 11); the block's (session, incarnation, decl)
  fence is unchanged (invariant 12).
- **Reports pinned to 19 move in the same commit**: `scripts/model-report.sh` (accepts `19)`
  only), `scripts/fleet-report.sh` (`known=19`), and their done-checks `check-model-report.sh`,
  `check-fleet-report.sh` (expect "(schema 19)"). `fleet-report.sh:432` counts any open wait past
  its deadline as expired; a mail-landed open row is miscounted as claim-landed ones already are:
  recorded, not changed.

## Residuals, recorded

- The 24 h sweep deletes a message by its `created`: a mail wait landed by a QUEUED message up to
  a day old can read STILL WAITING again after a sweep, inside its deadline (Fable cross-check;
  the earlier "12 h past the deadline" bound predates the queued arm). Recorded, not guarded.
- A broadcast never lands a mail wait (by design, above).
- **Fleet and home ledgers** (orc-3 #1421): the wait watches the ledger it was declared in; its
  lines name `buddy fleet inbox` when that is the fleet's. Watching both ledgers (D-193) is a
  second item.
- **Codex** (orc-3 #1421): whether Codex notices a background job's exit is unmeasured; nothing is
  launched to measure it. The queued arm makes a Codex session's wait land at once on unread mail,
  since its hooks never drain.

## Code stage

`internal/store/store.go` (column, ALTER arm, version), `internal/store/wait.go` (upsert,
watermark, keep-on-re-declare, READY refusal, `waitsWhere`'s arrival subquery, `Landed`,
ClearWait's mail close), `internal/cli/wait.go` (parse, refusals, `waitPhrase`, the lines, the
stop step's mail arm, hello), `internal/cli/waitblock.go` (the MAIL line), `internal/cli/who.go`
and msg's `waiterNote` via `waitPhrase`, the two reports and their checks, `skills/buddy/SKILL.md`
("Before you park" and the woken-first carve-out), `docs/USAGE.md`, a decision entry and its
CLAUDE.md line numbers. Tests, each refusal with a positive control, each fix mutated: lands on a
queued message from before the declaration; on one after it, delivered or not; not on a delivered
one from before; not on S's own send; not on a broadcast; not on adapter mail; an empty inbox
(watermark 0) is a mail wait; a re-declaration keeps the watermark and a timer after a mail wait
does not inherit it; MAIL beats EXPIRED at the deadline on check and block; clear of a landed
mail wait says MAIL; the sender is named from the ledger and never from `inbox.sender`; the 19 →
20 migration.

## Review record

**Codex design pass (xhigh, one round): 8 raised, 8 folded.** (1) a check could close a no-schema
mail wait EXPIRED while mail arrived → the schema; (2) a hook drain does not guarantee the trigger
reaches context → the line names the read; (3) "already delivered" is not "read" → "delivery
recorded"; (4) timer lines and hello mislead → `waitPhrase`, hello's mail arm; (5) a label change
hides an arrival → the label arm is gone (targets are ids); (6) another session's `--session`, the
fleet ledger → refused, fleet-qualified lines; (7) the watermark read before the declaration →
inside its transaction; (8) `--from`'s identity contract → `--from` cut.

**Panel (read-only; hostile sender/drainer on Fable 5.2, verdict machinery and migration on
Opus 5.5): 29 raised (9, 11, 9), 27 folded, 2 answered.**
- Hostile lens: queued-before-watermark mail never lands (high) → the queued arm; broadcast wake
  amplification (high/med) → broadcasts do not land; the sender named from `inbox.sender` → ledger
  label only; own broadcast lands own wait → excluded; an ended `--from` → `--from` cut; the
  delivery arm lands adapter mail → excluded on every arm; the label arm's collision surface → gone;
  `buddy inbox <id>` on a queued message injects twice → plain `buddy inbox`; one-liners (NULL
  sender, the /clear session name, COALESCE, the upsert, the 24 h flip) → folded or recorded.
- Verdict lens: the stop step clears an unlanded mail wait (high) → the mail arm and the skill's
  carve-out; queued mail never lands (high/med) → the queued arm; a re-declaration loses an
  arrival → keeps the watermark; arrival not monotone (label, 24 h) → label arm gone, 24 h
  recorded; own sends → excluded; clear of a landed wait says CLEARED → ClearWait closes landed;
  incomplete renderer list → `waitPhrase`; one SELECT → subquery; empty inbox → COALESCE, `>= 0`;
  `--from` sender ends → cut; refusal table gaps → the `self` test, READY read in the transaction
  (repeated `--from` moot).
- Migration lens: the upsert inherits mail columns (high) → SET clause, mutated; watermark 0 vs
  sentinel → `>= 0`; reports pinned to 19 → moved with their checks; `Landed()` has no database →
  subquery; hello after revival → its mail arm; `--from` across handoff → cut; an index before the
  ALTER → none, and the rule recorded; a running 19 block after migration → **answered**: harmless,
  no recheck; `fleet-report`'s expired count → **answered**: recorded, unchanged.

**Codex code pass (xhigh): 3 raised, 3 fixed.** A block could print `MAIL: #0` after a check closed
the wait and the mail was drained → arrival monotone (a message at or below the watermark is
excluded only by a delivery before `since`) and landedLines' not-found arm; no test fenced the
sender label → a test; the same-second rule → stated.

**Fable cross-check: 12 raised (6 findings, 6 test gaps), 9 fixed, 3 answered.** The named arrival
was the lowest id even when drained → a queued one first; the zero Wait read as a mail wait → the
`mail` flag; wokenStep's no-block wording and hello's recipe → fixed; five test gaps → tests. The
24 h sweep can un-land a wait landed by day-old queued mail → **answered**: recorded in Residuals;
the block's first read waits one 15 s poll → **answered**: as D-095's does; the migrated column's
shape against a fresh one → **answered**: ALTER appends it last with the same declaration.

**Codex re-verify (xhigh), round 1: 2 raised, 2 fixed.** A re-declaration kept the watermark and
reset `since`, so mail queued before the first declaration and drained between the two was lost
→ it keeps `since` too; the no-block step said a bare `wait check` (the shell's builtin; a fleet
wait needs `buddy fleet`) → `wokenStep` takes `buddyCmd`. **Round 2: 0 raised.**

