# Guided orchestration plan — design review

Reviewer: Fable (session buddy-system/s-5d0a0229), with adversarial Codex
passes (gpt-6-astra, xhigh) over the review itself. Date: 2026-09-20.
Reviewed: `docs/ORCHESTRATION-PLAN.md` at source baseline `00ca292`, and
`docs/wishlist.md` read against it (own section below).

Method: the plan, CLAUDE.md, the review charter, D-013 through D-018, and the
store/CLI code the plan anchors on (`store.go` Open/migrate/Hello/Bye/Beat/
Sweep/Msg/Undelivered/MarkDelivered/MarkIdle, `target.go`, `cli.go` hello/beat/
idle/msg/inbox). Every "fact" below is a source fact with its site named. One
positive control was run (wishlist §5c, below); nothing else here is test
evidence. The Codex passes are recorded in the last section with what they
changed and what was rejected.

## Verdict

| Increment | Verdict | Why in one line |
| --- | --- | --- |
| 1: discoverability | **Ready with changes** | Keep `help orchestrate` and `doctor`; fold `inspect` into `sessions <target>`; defer the `orchestrate` overview until tasks exist. `doctor` needs a read-only open path that does not exist yet (B3). |
| 2: tracked work | **Revise** | The lifecycle is right and durable state is earned. But the plan defers every operation that resolves its own likeliest failure (B1), overstates notice durability (B2), cannot be driven by the operator from a terminal (B4), and its concurrency contract needs report-level guards rather than assignment revisions (R1, R2). "Design contract after review" is one place to disagree with. |
| 3: repeated workflows | Not assessed | Correctly gated on demonstrated use. Nothing should start before increment 2 has run the live exercise. |

No settled decision needs overturning. Two GIVENs are applied throughout:
identity is `(session_id, incarnation)` and a re-`hello` of an ENDED session
id mints a new incarnation and orphans the old one's open claims (D-003,
`store.go` Hello, the `ended.Valid` arm); the inbox is delivery-marked per
`session_id` and swept on a TTL (`store.go` Sweep).

## Blockers

Each names the failing input and the wrong behaviour under the plan as written.

### B1. The recovery path for a resumed session is deferred, so the likeliest failure has no exit

**Fact.** `Bye` sets `ended`; the next `Hello` for that id finds `ended.Valid`,
mints a new incarnation and orphans the old incarnation's open claims. Claude
Code keeps the session id across `--resume`, so a clean exit followed by a
resume is exactly this sequence. (A crash or a sleep with no `bye` is NOT: the
`Hello` default arm refreshes the same incarnation in place.)

**Scenario.** Worker W accepts T12 under incarnation i1 and claims
`internal/api`. The operator exits and resumes. W is now `(W, i2)` with its
full conversation, its work in the tree, and no claim. Under the plan T12 shows
*needs recovery*, W's `task report T12` is refused as a stale incarnation, and
the two operations that could resolve it — reassignment and cancellation of
accepted work — are in "Deferred scope". T12 stays open until an operation the
plan has not scheduled exists.

**Required.** Increment 2 ships both of:

1. **Re-accept by the successor incarnation of the same session id.** A task in
   `accepted` whose assignee is `(S, i1)` while the sessions row reads `(S, i2)`
   may be accepted by the caller `(S, i2)`, recorded as a `reaccepted` event.
   It transfers *future responsibility*; it does not re-attribute i1's reports,
   which keep their own author `(S, i1)`. A task still `offered` to `(S, i1)`
   is simply *accepted* by `(S, i2)` — a first acceptance, not a renewal
   (Codex pass 2). A *different* session id may not take either path; that
   stays deferred with reassignment.
2. **Requester or reviewer cancel from any non-terminal state**, with a
   reason, recorded and delivered as a notice. The plan defers it because "it
   cannot claim to stop an in-flight tool call"; neither can `pause`, and
   `pause` exists. Cancel changes a row and sends a notice. It stops nothing
   and releases nothing; the worker's claims stay the worker's.

### B2. "A durable notice is awaiting retrieval" is true for 24 hours

**Fact.** `Sweep` deletes `inbox`, `inbox_delivery` and `inbox_recipient`
rows whose `created` is older than the TTL (`cli.SweepTTL` = 24 h), delivered
or not. `sweep` is a routine verb any session may run.

**Scenario.** T12 is offered Friday evening to a worker idle at its prompt.
Nobody prompts it. Saturday a peer runs `buddy sweep`. Monday the worker's
first tool call drains nothing, and the plan's status table says *Queued*
about a row that no longer exists.

**Required.** **The task row is the durable fact; the notice is a courtesy
with the inbox's lifetime.** Discovery must not depend on the notice: the
`hello` digest carries a one-line count of tasks addressed to the caller's
session id (R9) and `task ls --mine` lists them, both **by session id
regardless of incarnation**, or discovery hides exactly the tasks that need
re-acceptance. `task show` reports the notice as `emitted <age>`, `queued` or
`expired` (R7). Exempting task notices from the TTL is a viable alternative —
the per-beat drain stays bounded at 20 messages / 8 KiB either way — but adds a
second lifetime rule to the inbox for a problem the wording fix already closes.

### B3. Confirmed: every existing store-opening path migrates, so `doctor` needs its own

The plan already requires this check. This is its result, and the fix.

**Fact.** `store.Open` reads `PRAGMA user_version` and, when below
`schemaVersion`, runs `migrate` in a write transaction; the DSN also requests
`journal_mode(WAL)`. `openRepo`, `openLedgerAt` and `mustLedger` all end in
`store.Open`. There is no read-only path.

**Scenario.** Old ledger, new binary, `buddy doctor`: the ledger is migrated
and `session_context` is dropped (the `ver < 4` arm). The diagnostic changed
what it diagnosed, and a downgraded binary now meets a version it does not know.

**Required.** `store.OpenReadOnly`: `mode=ro`, no migration, returns
`user_version`. `doctor` reports `below` ("the first verb you run will migrate
it"), `current`, or `above` ("written by a newer buddy; this binary cannot read
it safely"). On a WAL ledger `mode=ro` needs the `-shm` file or a writable
directory; when it fails, `doctor` reports *unreadable*, distinct from *absent*
(invariant 3). **Qualification covers the auxiliary files**: `buddy.db`,
`buddy.db-wal` and `buddy.db-shm` are byte-identical, or equally absent, after
the run — with a fixture where they are absent beforehand. Note: today's
`Open` accepts `ver > schemaVersion` silently. Pre-existing and out of scope;
`doctor` is where it finally gets said.

### B4. The operator cannot offer work from a terminal

**Fact.** Every proposed task verb needs a requester identity and the plan
assumes a session. `whoAmI` (`cli.go`) resolves from `--session`,
`BUDDY_SESSION`, `CLAUDE_CODE_SESSION_ID`, then the cwd — and the cwd arm
**refuses** when more than one live session exists (`errAmbiguous`,
`errUncorroborated`), and with exactly one it *infers* that session and says
so on stderr. `msg` sidesteps this with `--from operator`.

**Scenario.** The operator, in a terminal, with three live sessions in this
checkout, runs `buddy task offer s-16c16a94 ...`. Refused as ambiguous. With
one live session it is silently filed under that agent.

**Required.** An explicit actor selector, `--as operator`, on every task verb;
without it `whoAmI` applies unchanged. `operator` is a **tagged role kind**
stored in its own column, never a reserved session id or label — a session may
label itself `operator` and must not thereby inherit the role (Codex pass 2).
An operator role has no return address: notices to it are not queued and it
gets no notice status word. An operator reviewer is never adoptable (R12).

## Required changes short of blockers

### R1. Cut assignment revisions; a changed brief is a new task — with two conditions

The brief is immutable from `offer`. Changing it is `cancel` plus `offer
--supersedes T12`, or one `reoffer` verb doing both in one transaction. The
cut survives the strongest case against it (continuity of one task's history)
on two conditions: the `supersedes` link is durable and shown by `show` on
both tasks, and the old worker's outstanding work stays explicit — the
cancelled task keeps its last update and reports, and the new task's `show`
points at them. Revisions are NOT what guards concurrency; report ids are.

### R2. Idempotency and concurrency guards

Two Codex passes broke two drafts of this. The contract that survived:

- **Offer dedup by full content among non-terminal tasks.** A second `offer`
  with the same requester, assignee session id and ALL six brief fields while
  the first is `offered`, `accepted` or `reported` returns the existing id and
  says so, exit 0. Same objective with any other field different is a
  different task and is refused naming the existing one ("T12 has this
  objective with different constraints; `--supersedes T12` or `--key`").
- **`--key <token>` is the retry contract across terminal states.** Unique per
  requester across ALL states; a retry with the same key returns the existing
  task even if it was declined or cancelled in between (transport uncertainty
  must not undo a refusal); the same key with a different payload is refused.
  Without a key, a retry after `declined`/`cancelled` creates a new task — the
  worker and requester both see it, and the guide says to use `--key` for
  scripted offers.
- **Reports are numbered.** Each `report` appends `T12.r<k>`; the task carries
  the latest `k`. `close` and `changes` **require** `--report r<k>` and refuse
  on mismatch, naming the current one. **Arguments and authority are validated
  before any no-op check**: `close --report r999` on a closed task is refused
  on the mismatch, not accepted as "already closed".
- **Repeat-by-content no-op, narrowly.** A `report` identical to the latest
  report by the same author **while the task is still `reported`** is a no-op.
  After `changes`, the task is `accepted` and an identical resubmission is a
  new report (the worker stands by it; the reviewer sees r<k+1> equal to r<k>
  and says so). Residual, accepted: a `report` retry delayed across two review
  cycles lands as a new report equal to a superseded one. It corrupts nothing
  and needs a local CLI call to be delayed across two human review cycles.
  `accept`/`close`/`cancel` finding the task already in the state they would
  produce, by the same actor, succeed without an event or notice.
- **Every transition is one IMMEDIATE transaction** that re-reads the task's
  state and roles AND the caller's own sessions row under the lock: a caller
  resolved as `(R, i1)` whose row now reads `(R, i2)` or `ended` is refused as
  stale, and any target's row (offer, handoff) is revalidated the same way
  (R4). This is what keeps GIVEN 10 whole; the residual that a stale *process*
  freshly resolving itself is identified as the successor is shared with
  `claim` and recorded in the contract.

### R3. The brief is fixed fields, each rendered on one fenced line

Invariant 9 renders every untrusted value on exactly one line. Make the record
the template: `objective`, `actions`, `constraints`, `criteria`, `refs`,
`scopes`, each a column capped at the conventional 512 and printed as
`<name>: <fence.Line>`. Reports: `outcome`, `refs`, `validation`,
`uncertainty`, plus the reporting worktree. Field caps do not replace output
caps: six brief and four report fields at maximum are 5 KiB before labels, so
`show`'s default view (brief + latest report + state + notice status) gets an
8 KiB ceiling and history is the paginated part. Any fixed-column task listing
uses `fence.Field` (GIVEN 21). Blocker text is a separate `update` (R11).

### R4. `Target` gains `Incarnation`; offer and handoff re-check it under the lock

`ResolveTarget` already reads the `SessionInfo` and drops the incarnation.
Carry it. The transaction re-reads the target's row and refuses if the
incarnation moved or `ended` is set. Offering to an ended target is refused
(`Target.Live` false); offering to a `live STALE` one is allowed with the
staleness named, because staleness marks and never decides (D-001).

### R5. "Needs recovery" is derived at read time, by role and by state

Never stored. State `offered` or `accepted` and the assignee's session row is
ended or carries a different incarnation → `needs recovery (worker)`; state
`reported` and the reviewer is a session that has ended → `needs recovery
(reviewer)`; `closed`, `cancelled`, `declined` → never. Sessions rows are never
deleted (`Sweep` touches claims, inbox and dirty paths only), so a role always
has a row to compare against.

### R6. A gate that the safety path never reads tasks — and what a grep can and cannot prove

Task code lives in its own files (`internal/store/task.go`, `internal/cli/
task.go`). A `scripts/check-*.sh` invoked from `check.sh` asserts: no task
table name and no symbol exported from `task.go` appears in `gate`,
`commit-gate`, `Claim`, `Release`, `OwnerOf` or their files; no `claims`/
`claim_scopes` mutation appears in the task files. Two clauses each, as the
existing gates have. **The schema string in `store.go` necessarily contains
the task tables; the gate scopes to the named call sites, not to `Open`.** A
grep proves the absence of direct references, not reachability; the
qualification plants a direct reference AND a helper-mediated one and expects
the second to be caught by the symbol clause, which is as far as a grep-shaped
gate goes. Said out loud so nobody mistakes it for more.

### R7. Notices ride the inbox, in the same transaction, small, fenced, and to the CURRENT role holder

Insert the inbox row inside the task transaction and record its `msg_id` and
recipient session id on the `task_events` row that queued it. Body: generated
text plus TWO fenced values — the sender's label (64) and one field (objective
or outcome, ≤120 B) — `BUDDY TASK T12 offered by <label>: <objective> — buddy
task show T12`, under 256 bytes. Routing: `offer`, `close`, `changes`,
`cancel`, `adopt`, `handoff` → the assignee (the last two say who reviews
now); `accept`, `reaccept`, `decline`, `update`, `report` → the **current
reviewer**; `handoff` also → the new reviewer. `operator` roles get nothing.

Status words are read from that recorded `msg_id`: `queued` (inbox row,
no delivery row for the recipient), `emitted <age>` (delivery row), `expired`
(no inbox row). `inbox_delivery` is keyed by session id, so `emitted` means
"to session W", historically — a drain by `(W, i1)` before a resume is never
repeated for `(W, i2)`, which is acceptable for a courtesy whose discovery
path is independent (B2). No status is printed for a notice never queued.

**GIVEN 8 reconciled explicitly.** "Only operator inbox messages are
auto-injected" describes the inbox versus room digests; peer `buddy msg`
already rides the same drain (README §1a). A task notice is a peer inbox
message whose attacker-influenced content is two fenced values totalling under
200 bytes. That is within the existing inbox contract, and it is the whole
reason the recipient is told to *fetch* the brief rather than being handed it.

### R8. The offer's own output carries the idle fact as an observation

`msg` prints "delivered after their next tool call". `offer` prints that and,
when `session_idle` has a row for the assignee: `— <label> reported idle 8m
ago; if it is still at its prompt, prompt it`. Not "will not see this until
prompted": the row is an observation, and the session may be mid-tool-call by
the time the offer commits.

### R9. The `hello` digest hint is one line, count only, by session id

`BUDDY: 2 task(s) addressed to you (1 offered, 1 accepted): buddy task ls
--mine`. Matched on the caller's session id regardless of incarnation (B2).

### R10. (Dropped.) Self-assignment is allowed

With a distinct reviewer it is a coherent agreement; a task with all three
roles on one session is a todo and harms nobody.

### R11. Blocker updates are a verb, not a report outcome

`task update T12 --blocker <text> --next <text>` stores the latest
blocker/next-action on an `accepted` task and changes no state. Folding it
into `report` would let peer text drive lifecycle semantics. `report` always
moves `accepted → reported`.

### R12. `adopt` and `handoff` have actor, state and concurrency rules

- Both are legal only in `offered`, `accepted`, `reported`.
- `adopt T12`: only when the current reviewer is a session whose row is ended;
  never to a caller whose session id is the assignee's; never when the
  reviewer is `operator`.
- `handoff T12 <target>|operator`: by the live reviewer, to any session except
  the assignee's, with the destination revalidated under the lock (R4).
- Both re-read the current reviewer under the lock; two simultaneous adoptions
  → one wins, the other is told who has it. Prior reviewers stay in events.

Under the single-operator trust boundary (GIVEN 9) this is a coordination
rule, not a security one; it exists so "independently reviewed" on a row
means what it says.

## Design contract after review

**Tables** (new; `schemaVersion` bump; none ever dropped by a migration):

- `tasks`: `task_id` (`INTEGER PRIMARY KEY AUTOINCREMENT`, shown as `T<n>`);
  for each of requester, reviewer, assignee: `<role>_kind` (`session` |
  `operator`), `<role>_sid`, `<role>_inc`; six brief fields; `key`;
  `supersedes`; `state`; `blocker`; `next_action`; `report_seq`; `created`;
  `updated`. Unique index on `(requester_sid, key)` where `key` is non-empty.
- `task_reports`: `(task_id, seq)`, `author_sid`, `author_inc`, `worktree`,
  four report fields, `created`.
- `task_events`: `(task_id, seq)`, `at`, `actor_kind`, `actor_sid`,
  `actor_inc`, `kind`, `from_state`, `to_state`, `note`, `notice_msg_id`,
  `notice_sid` (the last two nullable).

**States**: `offered`, `accepted`, `reported`, `closed`, `declined`,
`cancelled`.

**Verbs**: `offer <target> --objective … [--reviewer <target>|operator]
[--supersedes T<n>] [--key]`, `accept`, `decline [--reason]`, `update
--blocker --next`, `report --outcome --refs --validation --uncertainty`,
`close --report r<k>`, `changes --report r<k> --reason`, `cancel --reason`,
`adopt`, `handoff <target>|operator`, `show`, `ls [--mine]`; all accept
`--as operator`. Plus `help orchestrate`, `doctor`, `sessions <target>`.

**Actor rule**: the caller is `whoAmI`'s current `(session_id, incarnation)`,
as `claim` and `release` resolve theirs, or `operator` by explicit flag. The
transaction revalidates the caller's row under the lock (R2). Shared residual
with `claim`: a stale *process* of an ended-and-revived session that resolves
itself fresh is identified as the successor; the harness gives a hook no way to
export the incarnation into the session's environment.

**Role checks**: `accept`/`update`/`report` require the caller to equal the
assignee `(sid, inc)`, except: `accept` on an `offered` task whose assignee
row has moved to a new incarnation of the same sid binds the caller (first
acceptance); `accept` on an `accepted` task in `needs recovery (worker)` by
the same sid performs `reaccepted`. `close`/`changes`/`handoff` require the
caller's sid to equal the current reviewer's (or `operator` when that is the
role); `cancel` also allows the requester's. The reviewer is matched on session
id with the acting incarnation recorded on the event: review authority is a
role a session holds; acceptance is a commitment one incarnation made and its
successor must re-make. Codex pass 2 judged the asymmetry defensible given the
in-transaction caller revalidation.

**Ceilings**: notice 256 B; `ls` 4 KiB; `show` default 8 KiB; history page
16 KiB with an explicit continuation line. Constants, measured in the live
exercise, not documented as contracts until then.

## Increment 1: what to keep, fold and defer (Q1)

- **Keep `buddy help orchestrate`.** Text in the binary, no ledger read, the
  one thing a fresh session finds with `buddy help`. With R3 the template IS
  the argument list.
- **Fold `inspect <target>` into `buddy sessions <target>`.** One roster row in
  the existing renderer, then its pause note, open claims with scopes, and the
  idle/context observations with ages. `ResolveTarget` provides the argument;
  `all` resolves there by design (GIVEN 16) and **this verb refuses it
  itself**, pointing at the bare roster.
- **Defer `buddy orchestrate`.** Before tasks exist it is `sessions` + `ls` +
  the recipe. Once tasks exist the attention view is `task ls`, exceptions
  first, a footer naming the next command per row. Removal rather than
  deferral waits on the fresh-session exercise showing the guide plus the
  focused view reach the same "next command" a combined overview would.
- **Keep `buddy doctor`**, under B3. Evidence wording: a sessions row proves a
  `hello` ran, **not that the SessionStart hook ran it** (a manual `hello`
  leaves the same row): `hello: configured; registration observed <age>`;
  `beat: last_seen > started; context row <age>`; `idle: row <age>`; `bye:
  ended rows exist`. **`gate` and `commit-gate` leave no ledger trace by
  design** — for them `configured`/`not configured`, never `working`. The
  commit gate is located through the *effective* hooks path (`git rev-parse
  --git-path hooks`, which honours `core.hooksPath`), not by the presence of
  `.githooks/pre-commit` in the tree (Codex pass 2). Hook configuration is
  read from `.claude/settings.json`, `settings.local.json` and
  `~/.claude/settings.json`.

## The eight questions

1. **Front door.** Yes, with `orchestrate` deferred and `inspect` folded.
2. **Does the lifecycle earn durable state?** Yes: *accepted* is a fact the
   coordinator must read without the worker's cooperation at read time, and
   the plan rightly forbids inferring it from messages. Smallest version: the
   contract above — three tables, six states, no assignment revisions, no
   op-id column, blockers as `update`, `--key` for scripted retries.
3. **Identity, revision, idempotency.** Sufficient with R2, R4, R12 and the
   stated residual. Assignment revisions add nothing report ids do not.
4. **Reviewer adoption; deferring reassignment.** `adopt`/`handoff` under R12
   suffice. Deferring reassignment to a different session is workable **only**
   with B1's accept/re-accept and cancel.
5. **Replacement revision.** Cancel-and-reoffer (R1) under its two conditions.
   Atomic unit: one transition, or one paired cancel+insert.
6. **Scope, authorization, claims.** Separated in the model if `scopes` is a
   text field the gate never reads, and R6 gates it as far as a grep can. In
   the guidance, step 4 of the recipe must say *"acceptance is responsibility;
   `buddy claim` is the reservation; the gate reads only claims"*.
7. **Read-only diagnostics and budgets.** Feasible with a new open path (B3)
   qualified against all three files. `show` needed 8 KiB, not 4.
8. **Missing cases.** Next section.

## Acceptance cases to add

| Scenario | Required outcome |
| --- | --- |
| Worker accepts, reports r1, reviewer `changes r1`, worker exits cleanly and resumes, `report` | Refused; `accept` by the same sid → `reaccepted`; then `report` → r2 with author `(S, i2)`; r1's author stays `(S, i1)` |
| Task `offered` to `(S, i1)`; S resumes as i2 and accepts | Plain `accepted`, bound to `(S, i2)`; no `reaccepted` event |
| Requester or reviewer cancels an accepted task | `cancelled`, notice to assignee, claims untouched, last update/report preserved |
| Operator offers with three live sessions, `--as operator` | Accepted with requester kind `operator`; no return notices, no status word; without the flag: refused as ambiguous |
| A session labelled `operator` calls `adopt` on an operator-reviewed task | Refused: the label is not the role |
| Notice swept before delivery | `show` says `expired`; `hello` digest still counts the task; `ls --mine` lists it |
| Notice drained by `(W, i1)`; W resumes as i2 | `show` says `emitted <age>`; i2 receives no second notice; `ls --mine` still lists the task |
| Reviewer handoff after the offer notice was emitted to the old reviewer | The new reviewer's status is not `emitted` off the old row |
| `doctor` on a ledger below / at / above `schemaVersion`, with and without `-wal`/`-shm` present | Three distinct words; all three files byte-identical or equally absent afterwards |
| `doctor` on a ledger it cannot open read-only | `unreadable`, distinct from `absent` |
| `offer` retried after the response was lost, first still `offered`/`accepted`/`reported` | Same `T<n>`; one row, one notice |
| `offer` retried after the first was declined, with and without `--key` | With: same `T<n>`, still declined; without: a new task |
| Same `--key`, different payload | Refused |
| Same objective, different constraints, no key | Refused naming the existing task |
| Reviewer `changes r1`, worker reports r2, reviewer's stale `close --report r1` | Refused naming r2 |
| `close --report r999` on a closed task | Refused on the mismatch, not a no-op |
| `report` identical to the latest while `reported` / after `changes` | No-op / new report r<k+1> |
| `accept` then `cancel`, and `cancel` then `accept` | First: both succeed, ends `cancelled` with both events. Second: `accept` refused naming `cancelled` |
| Two `adopt`s at once | One wins; the loser is told who has it |
| Assignee `adopt`s its own task; `adopt` while the reviewer is live; `adopt` of an operator reviewer; `adopt` on a closed task | All refused |
| Caller resolved as `(R, i1)`, R ends and revives before the write | Refused as stale |
| Offer to an ended target / to a `live STALE` target | Refused / allowed with staleness named |
| Brief field containing `\n` or a literal `⏎`; label with a space in `task ls` | One line per field, one token per column; markers provably from the fence |
| Report refs from worktree A, worker revives in worktree B | `show` prints the report's recorded worktree beside its refs |
| Coordinator and worker in different worktrees of one checkout | Same ledger, same task |
| `task accept` from a session that never said `hello` | Refused, names `buddy hello` |
| `sessions all` | Refused, points at the bare roster |
| Planted direct `tasks` reference in `gate`; planted call to a `task.go` symbol from `Claim` | Both fail the R6 check |
| Closed task whose worker later ends | No recovery flag |

## The wishlist, read against this plan

`docs/wishlist.md` is field notes from a 14-session run on the reference repo.
Most of it is the resource-and-identifier half and does not touch this plan;
the items that do, and what they change here:

- **§8 `grant`.** Not the same thing as `offer`, and the first draft of this
  section said it was. An offer proposes work and carries acceptance, report
  and review; a grant is a completed ruling ("W may use `docs/x.md`", a number
  block, an integration order) with no assignee acceptance and no lifecycle.
  Tasks record the rulings that assign work, which is the audit trail for
  those; a bare-ruling record is a different table and a separate decision.
  Neither reserves anything; claims keep that authority. Nothing in this plan
  needs `grant`, and nothing in it prevents one later.
- **§9 `blocked-on`.** R11's `update --blocker` covers a blocker on an
  accepted task, surfaced in `task ls`. A session waiting on a claim holder
  with no task open cannot say so through it; that, `last-grant` and the
  display-name collision stay unanswered by this plan.
- **§4 corrections that cannot overtake a broadcast.** Deferral stands. The
  delivery rows the wishlist wants to read already exist (`inbox_delivery`);
  what does not exist is supersession, standing corrections or any read state,
  and R7 supplies none of them — a per-notice status word is not an ordering
  guarantee, and with twenty older messages queued an original can drain a
  beat before its correction. Separate work, after increment 2.
- **§10 "the roster should mark message queued, undelivered".** D-015 cut the
  undelivered count with this reason recorded: beat drains the inbox on every
  tool call, so a non-zero count marks the session idle at its prompt — the
  one that can take work — as the one that cannot. Codex pass 3 adds that
  `queued` also results from backlog or delivery failure, so the annotation
  would be a delivery fact, not an availability one. The task-level status
  word (R7) answers the task case; the general case stays cut unless D-015 is
  reopened with evidence.
- **§5c "`buddy msg` never appears in the hook stream".** The *mechanism
  claim* is refuted at this baseline: `cmdBeat` drains `Undelivered` into the
  PostToolUse `additionalContext` under the `BUDDY MESSAGES` banner; three
  tests cover it (`TestBeatDrainsInboxAtLeastOnce`, `TestBeatDrainIsBounded`,
  `TestBeatInboxFencesNewlines`); a positive control run for this review (two
  sessions in a throwaway repo, `msg` by short id, `beat` with Bash hook JSON)
  emitted the message on the recipient's first beat and nothing on the second.
  **The incident's cause is not established by that.** Mechanisms that fit
  "B read every hook line and saw none" and are not excluded: B parked (no
  tool call, no beat — D-016); the `beat` hook unwired or its binary killed on
  launch (the exit-137 signature fact) in that checkout; the sends refused or
  unresolved before insertion; sender and recipient on different ledgers
  (different clones or nested repos, not worktrees); an earlier drain under
  B's session id (a previous incarnation) already marked delivery; twenty
  older messages ahead in the 20/8 KiB drain; the harness dropping the hook's
  `additionalContext`. The probe that separates most of these is a
  read-only look at that repo's ledger, and it was run for this review
  (`sqlite3 "file:…?immutable=1"`, 2026-09-20 13:58): of 264 inbox rows,
  every direct message whose target was live and had a heartbeat AFTER the
  message was queued has a delivery row; the one undelivered message to a
  live session was queued at 13:44:41 to a session whose `session_idle` row
  dates 13:40:11 and whose last heartbeat is 13:40:14 — parked, four minutes
  before the message. Three messages to another live session queued at
  13:56–13:57 were all delivered on its next beat at 13:58:13. The remaining
  two undelivered rows address sessions that had ended. So the mechanism is
  the documented one: the recipient was at its prompt and nothing drains an
  inbox until a tool call. §5c is not a defect in delivery; it is D-016's
  double edge, and the only fix that reaches a parked session is the
  operator prompting it — which is what the plan's offer output (R8) has to
  say. For this
  plan the consequence is unchanged: the live exercise must include a parked
  recipient, and the notice status word (R7) is what would have answered "did
  B ever get it" in one command.
- **§5d a refused claim's slug is not messageable.** A `ResolveTarget` matter
  (D-013) that does not touch tasks: a task addresses a session target and the
  notice names `T<n>`, so no slug is needed to reply. The first draft's fix
  (print the requester's id in *its own* refusal) reaches the wrong party; the
  address has to reach B. The right fix is in `msg`: stamp the sender field
  with the sender's resolved label rather than free `--from` text, so every
  delivered line carries a `[label]` that resolves. **Shipped as part of
  D-019**; the ledger measured 1 of 111 direct messages signed with a label.
- **§2b four names for one actor.** `sessions <target>` answers "any buddy
  form → the row". The harness display name is new data; whether the hook JSON
  carries it is unverified (today's `hookInput` reads `session_id`, `cwd`,
  `tool_name`, `transcript_path`, `tool_input`). Needs a measurement before it
  is promised; duplicate display names need an ambiguity arm regardless.
- **§6 base on the roster.** D-015 cut a stored *branch*; "N behind main" is a
  different fact. Collecting it per beat is a second git process, which
  `cmdBeat` avoided as a measured regression; a SHA sampled at `Stop` with the
  lag computed at roster-read time is one alternative, and any stored lag goes
  stale when main advances. Not part of this plan.
- **§1 slots, §3 alloc, §5/§5b/§7 claim granularity** are outside this plan.
  §5 sub-file scopes and §7 shared-append claims press on D-002 and would need
  their own decision record with the evidence the wishlist already gives. §5b
  (`claim --dry-run`, partial release) conflicts with no settled decision and
  needs no new table — **shipped the same day as D-019**, together with the
  §5d sender fix; partial acquisition was cut, and the record says why.
- **§11 provenance on messages (added while this review was in progress).**
  For task results the plan already has the structural answer: R3's report
  fields separate `outcome` from `validation` and `uncertainty`, so "what was
  measured" and "what is inferred" are different columns and the reviewer
  reads both. For ordinary messages a `--lead|--measured` marker is a `msg`
  change outside this plan; it is consistent with invariant 4 as long as the
  marker is rendered as the sender's own claim about its message and never
  read as authority — the same rule that governs `from`. Worth pairing with
  the §5d sender-field fix, since both touch how a delivered line is stamped.
- **Meta.** The wishlist's closing lesson — make the authority cheap to
  consult — is this plan's thesis for `task show` and `doctor`, stated from
  the field.

Housekeeping: the wishlist names the reference repository by name throughout,
which this project's standing rule says not to do in this repo.

## Unresolved for the author

- Whether closed and cancelled tasks are ever swept. Recommend never in the
  first release; revisit when a ledger measures over a thousand rows.
- Whether `decline` and `cancel` require a reason. Recommend optional for
  `decline`, required for `cancel`.
- Whether `report` requires `refs`. Recommend optional; the reviewer decides
  whether an unreferenced result is closable.
- Whether `handoff` may name the assignee's session id when the requester
  wants self-review. Recommend no; use `operator`.
- Whether an unkeyed retry after `declined` should create a new task or be
  refused naming the declined one. The contract says create; the alternative
  is safer and more annoying.

## Codex passes

### Pass 1 — the first draft of this review, plan and source excerpts inlined

All four blockers stood; three were overstated and corrected (B1's frequency —
only a recorded `bye` rotates the incarnation; B2's case against TTL exemption
— the per-drain bound holds regardless; B3 is a confirmation of the plan's own
requirement and its qualification must cover `-wal`/`-shm`). Changed: R2
rewritten (the "idempotent by nature" cut failed on the accepted-duplicate and
stale-report cases); R3 gained the output ceiling; R5 gained role and state
conditions; R7 routes to the current reviewer; R8 wording; R10 dropped; R11
and R12 added; two increment-1 overclaims fixed (`all` is not refused by the
resolver; a manual `hello` leaves the same evidence as the hook). Four gaps
raised that the draft missed: stale-caller identity, report-specific approval,
adoption authorization/concurrency, and report refs outliving their worktree.
Rejected: none outright; the case for assignment revisions was considered and
the cut kept under two conditions (R1).

### Pass 2 — the revised contract, attacked

Held: report-bound close (b), reviewer-by-session-id asymmetry (e) given
in-transaction caller revalidation, adopt/handoff actor rules (f), recovery
derivation (g). Broke and fixed: offer dedup was not retry-safe across terminal
states and omitted `reported` and the non-objective fields (→ full-content
dedup plus `--key`); repeat-by-content across review cycles (→ narrowed to
"while still `reported`", residual stated; argument validation before no-op);
the operator role had no callable actor path and could be confused with a
label (→ `--as operator`, tagged kind); notice status had no durable
event-to-`msg_id` association and mis-read after handoff (→ recorded on the
event; no word for a never-queued notice); first acceptance after a resume was
mislabelled `reaccepted`; `ls --mine`/`hello` had to match by sid. GIVEN
strains named and reconciled: 8 (R7), 7 and 21 (fence the label; `Field` in
listings), 10 (caller revalidation). Acceptance table: accept/cancel ordering
was wrong as "one wins"; author-preservation and retry traces were
underspecified; R6's grep cannot prove reachability and now says so. Verdicts
unchanged: increment 2 revise, increment 1 ready with changes, with `doctor`
following the effective hooks path.

### Pass 3 — the wishlist reconciliation and the pass-2 dispositions

Dispositions: B4, R2, R7 and the role checks hold as written, with three
specification questions for the implementer (how `--as operator` is exempted
from the sessions-row revalidation; where `decline` and `adopt` authority is
stated — assignee and R12 respectively; how a `handoff` event records two
notice tuples — two event rows). Corrected here: §8 (a grant is not an offer);
§5c (the mechanism is refuted, the cause is not — seven unexcluded mechanisms
and the one ledger probe that separates them are now listed); §5d (the fix
must reach the blocked party, so it belongs in `msg`'s sender field); §4 (R7
is not supersession); §6 (a per-beat git process is not the only design). Two
adjectives ("cheap", "highest-value") replaced with the facts they stood on.
Confirmed: R7's GIVEN 8 reconciliation is explicit, not a silent reversal; no
GIVEN forbids a roster delivery annotation, and D-016 forbids inferring
idleness from one.

### Passes 4 and 5 — code review of what shipped from the wishlist (D-019)

Not part of the plan review, recorded here because the same session did the
work. Pass 4 over the diff found the dry run did not check the caller's
incarnation, that `Claim`'s slug check returned before the scope scan so the
forecast and the refusal could disagree, and four tests a wrong implementation
could pass. All fixed; fifteen mutations run against the tests, fourteen
caught and one equivalent (its write sat inside the transaction the refusal
rolls back). Pass 5 confirmed every finding closed and no new defect, and suggested
four further tests, which were added. The record is D-019.

