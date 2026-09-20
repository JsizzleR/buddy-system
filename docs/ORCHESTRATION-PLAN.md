# Guided orchestration plan

Status: draft for Fable design review. Implementation has not started.

Date: 2026-09-20. Source baseline: `00ca292`.

This is the canonical proposal for the orchestration additions discussed here.
It does not replace the original project plan or amend settled decisions.
Command names below are proposed interfaces, not existing commands.

## Review request

Fable: review this design before implementation. Read [CLAUDE.md](../CLAUDE.md),
the [review charter](review-charter.md), and the relevant
[decisions](decisions.md), especially D-015 through D-018. Treat their settled
invariants as given. If a recommendation requires changing one, identify it
explicitly and give the evidence.

Assess whether the proposed workflow makes orchestration easier to discover
and perform, whether the task mechanism earns its complexity, and whether the
identity, delivery, authorization and recovery contracts are complete.
Prefer cuts or reuse of existing mechanisms over speculative infrastructure.

Return a verdict (ready, ready with changes, or revise), prioritized findings
with concrete failure scenarios, recommended changes, and any unresolved
decisions. Separate blockers from optional improvements. This request is for
design review, not implementation or deployment.

## Problem and intended outcome

The recent roster work helps a coordinator choose a peer: it exposes labelled
ages, pauses, claim counts, reported idleness, model/effort and dated prompt
observations. A session still has to invent the operating procedure around
those facts: how to offer work, establish acceptance, follow blockers, review
results and recover after context loss or session termination.

The intended outcome is a discoverable, repeatable workflow that a fresh
session can follow using Buddy's own guidance. The coordinator should be able
to answer:

- Which peers and existing reservations are relevant to this task?
- What exact next action starts the handoff?
- Has the recipient accepted responsibility?
- What needs a decision, a prompt, review or recovery?
- What was reported, what was checked, and what remains uncertain?

The first release is one operator, one machine and one repository ledger,
including that repository's worktrees. Chat remains optional.

## Current foundation and gaps

| Existing mechanism | What it establishes | What it does not establish |
| --- | --- | --- |
| `buddy sessions` | Session identity and dated observations | A commitment to accept new work |
| `buddy claim` / `release` | File-scope reservations and their lifecycle | Task acceptance or completion |
| `buddy msg` / `inbox` | Durable queued text and output delivery | That a worker understood, accepted or completed it |
| `buddy whose` | Observed file activity useful for addressing a peer | Complete authorship or authority to edit |
| Chat, presence and alerts | Visibility and bounded communication | Authority or reservations |

The most important operational constraint is already documented: an idle
session does not receive a queued message through its heartbeat until its next
tool call. Offering a task cannot promise to wake that session. The coordinator
must receive a concrete instruction to prompt it when necessary.

Source anchors:

- [README: roster and idle-delivery limitation](../README.md#1c-the-roster--which-session-takes-the-next-task).
- [CLI](../internal/cli/cli.go): `usage`, `cmdHello`, `cmdMsg`, `cmdInbox`,
  `cmdSessions`, and the heartbeat's bounded message output.
- [Store](../internal/store/store.go): claims schema, `Msg`, `Undelivered`,
  `MarkDelivered`, session incarnation handling and `Release`.
- [Target resolution](../internal/store/target.go): `Target` and `ResolveTarget`.
- [Design rationale](DESIGN.md): ledger/chat separation and cooperative enforcement.

This baseline was established by source inspection. It is not a fresh
verification of installed binaries, hook execution or the live fleet.

## Proposed features

### 1. A single starting point

Add `buddy orchestrate` as a bounded, read-only overview with exact next
commands. Add `buddy help orchestrate` as the compact end-to-end recipe:

1. Identify the calling session and inspect peers.
2. Define a bounded assignment with completion criteria.
3. Offer it to a specific peer and arrange a prompt if needed.
4. Obtain explicit acceptance; acquire actual claims before editing.
5. Report blockers and results.
6. Review the result and finish the handoff, checking claims separately.

Include a copyable worker kickoff instruction and task/result templates. Add
one short discovery hint to the existing session-start digest; do not inject
the full guide or task history on every hook.

Before tracked tasks ship, the overview may show existing session/claim facts
and the recipe only. It must not infer pending tasks or completed work from
messages, idle state or released claims.

### 2. Readiness checks and focused peer inspection

Add `buddy doctor` to explain ledger discovery/readability, calling identity,
available binaries, relevant hook configuration and available observations.
Distinguish configuration found, execution observed, missing and unknown.
Finding a hook snippet does not prove the effective harness ran it.

Add `buddy inspect <target>` to gather one peer's identity/incarnation,
worktree, applicable pause reason, claims/scopes and dated idle/context
observations. Reuse existing target resolution and refuse ambiguous targets.
The single-peer command should refuse `all` and direct users to the overview.

Both commands must be genuinely read-only: no database creation or migration,
session registration, inbox draining, observation updates, settings changes,
or hook execution. Existing store-opening paths must be checked for migration
side effects before reuse. An unsupported schema should produce a diagnostic.

Output should explain the next action without fabricating fitness. An idle
report is not reachability, missing idle is unknown, and prompt size is an
observation with an age. Do not rank workers using an opaque suitability score.

### 3. A durable task agreement

Add a small `buddy task` command family using the existing local ledger. The
minimum useful task contains:

- Stable task ID, requester and current reviewer identity.
- One assignee identified by session ID and incarnation.
- Objective, allowed action class, constraints and completion criteria.
- References and optional requested file scopes.
- Assignment revision, state and timestamps.
- Latest blocker/next action and submitted result references.

Proposed lifecycle:

```text
offered -> accepted -> reported -> closed
   |           |
   |           +-- blocker/update recorded while accepted
   +-- declined or cancelled before acceptance
```

The worker explicitly accepts or declines. A report is the worker's account
of its result; closing means the designated reviewer accepted that report.
Neither proves that tests actually passed. Requesting changes returns a
reported task to accepted with a recorded reason, under the same brief.

Requirements freeze at acceptance. Material changes require a replacement
revision that the worker explicitly accepts. The exact replacement workflow
is a review question; it must not silently rewrite active work.

Task acceptance records responsibility, not a reservation. Workers continue
to use `buddy claim` for file scopes. A claim collision can leave an accepted
task blocked; the view must show that clearly. Task closure never releases
claims, and claim release never completes a task. Read-only research/review
tasks need not invent file reservations.

Task text cannot expand the user's authorization. A planning task remains
planning, and a task record is not permission to commit, publish or deploy.
This remains a cooperative single-user tool, not a new security boundary.

### 4. Honest delivery status and an attention list

The overview should prioritize exceptions: awaiting acceptance, blocked,
reported and awaiting review, or needing recovery. Explain what can happen
next rather than requiring repeated scans of every session and room.

| Fact | Meaning |
| --- | --- |
| Queued | A durable notice is awaiting retrieval |
| Emitted | Notice was successfully written to hook or inbox output |
| Accepted | Worker explicitly accepted the current assignment |
| Reported | Worker submitted a result |
| Closed | Designated reviewer accepted the result |

Emission does not establish model consumption. Heartbeats, read cursors and
chat acknowledgements must never advance task state.

Illustrative proposed output:

```text
T12 awaiting acceptance
    Peer reported idle 8m ago; notice remains queued.
    Next: prompt that session to inspect task T12.

T09 ready for review
    Result and validation references attached.
    Next: inspect the report, then close or request changes.
```

Return a stable receipt/reference when offering work. Notices carry a task
ID, revision and short change description; the recipient deliberately fetches
details. Do not add undelivered-message counts to the roster as a measure of
worker capacity. General message receipts may reuse this presentation later,
but are not a prerequisite for the task workflow.

### 5. Task briefs, result reports and recovery

Ship authored templates with the first increment:

| Record | Required content |
| --- | --- |
| Brief | Objective, allowed actions, scope, constraints, completion criteria |
| Update | Progress, blocker, decision needed, next action |
| Result | Outcome, artifact references, validation performed, remaining uncertainty |

Once tasks exist, `buddy task show <id>` returns the current brief and bounded
latest state. It should support recovery after compaction without requiring
transcript mining or a full chat replay. Longer evidence remains in explicitly
referenced artifacts; listing views never automatically expand those files.

Recovery must ship with durable tasks:

- If the requester/reviewer ends, an explicit local operation can designate a
  successor reviewer while preserving prior identities and history.
- If an accepted worker ends or its incarnation changes, show needs recovery.
  Do not silently assign its work to the successor incarnation.
- Present the last report/checkpoint, relevant references and remaining claims
  separately. Do not infer complete authorship from the working tree.
- Defer reassignment or cancellation of accepted work from the initial task
  release. A future operation must account for acknowledgement and surviving
  work; it cannot claim to stop an in-flight tool call.
- Silence or an old observation never triggers reassignment or claim cleanup.

### 6. Workflow recipes

After the basic handshake is useful, add guided recipes for:

- Implementation across disjoint scopes.
- Independent review with an explicit report and acceptance step.
- Failure investigation returning evidence and uncertainty.
- Deliberate handoff of unfinished work.
- Collecting completed contributions for an integration check.

Initially these are templates and explicit steps. Dependency automation should
follow demonstrated use, not precede it. A recipe does not automatically spawn
sessions, wake a harness, transfer claims or dispatch the next task.

## Task implementation contracts to review

1. **Identity and concurrency.** Capture and revalidate the assignee's session
   ID and incarnation in the assignment transaction. Existing `Target` holds
   a session ID but not an incarnation; its ID alone is insufficient. Every
   transition checks actor identity, expected revision and allowed prior state
   atomically. Two competing transitions have one winner.
2. **Retries.** Offer and result submission need stable operation identifiers
   or an equivalent idempotency contract. A lost command response must not
   create duplicate tasks, submissions or notices. Exact syntax/schema remains
   an implementation-design decision after this review.
3. **Notices.** Existing inbox delivery is session-ID-based. An old notice can
   therefore reach a revived session; it must only point to a fresh task lookup.
   Stale identity/revision checks prevent acceptance of an obsolete assignment.
4. **Crash consistency.** Persist a task transition and its durable notice in
   one transaction where possible. Task state remains discoverable even if
   output or notification delivery fails. Never roll back an accepted result
   merely because optional chat is unavailable.
5. **Bounded context.** Independently bound list output, notices, briefs,
   updates and reports. Paginate detail/history with explicit truncation.
   Fence all peer-controlled fields using the existing rendering rules.
   Initial proposed ceilings are 750 bytes per notice, 4 KiB per overview or
   default detail result, and a deliberate 16 KiB expanded read. Fable should
   assess whether these fit useful handoffs before they become fixed contracts.
6. **History and retention.** Preserve accepted briefs, assignment/reviewer
   changes and submitted results. Do not overwrite the evidence of a handoff.
   Bound reads independently from storage; defer automatic task-history pruning
   until a retention rule is reviewed. Never treat task records like the
   re-derivable context-observation table.
7. **Architecture.** Task mutations live in `buddy`/`internal/store`, with CLI
   guidance in `internal/cli`. Reuse claims, target resolution and fencing.
   No dependency from the claims gate onto chat, no new daemon or supervisor,
   and no broad polling or model calls added to the hook path.

## Delivery increments

| Increment | Scope | Completion criterion |
| --- | --- | --- |
| 1: discoverability | Guide, templates, read-only overview, doctor, peer inspection | A fresh session coordinates a peer using built-in guidance alone; observed facts and missing configuration remain distinguishable |
| 2: tracked work | Task lifecycle, receipts, attention view, revision/idempotency checks and explicit reviewer recovery | Work can be offered, accepted, blocked, reported, reviewed and recovered without interpreting chat as task state |
| 3: repeated workflows | Recipes, richer authored handoffs and evidence-backed dependency support | Common workflows need fewer manual steps without weakening ownership or delivery semantics |

Increment 1 is independently useful. Increment 2 is the substantive stateful
addition and should be reviewed as such. Increment 3 must not delay it.

## Acceptance and qualification

For implementation, use the repository's required hermetic/race/done-check
workflow and mutation checks where applicable. These are future qualification
requirements; no implementation tests are claimed by this plan.

| Scenario | Required outcome |
| --- | --- |
| Fresh session follows the guide | It can find a peer, construct a bounded handoff and identify its next action without inventing commands |
| No ledger, unreadable ledger or unsupported schema | Read-only diagnostics distinguish them and write nothing |
| Hooks configured but no execution evidence | Doctor does not claim they are working |
| Missing idle hook or old context sample | Unknown/stale observation stays explicit; no availability inference |
| Idle recipient | Offer stays pending; output explains the required prompt |
| Notice emitted without acceptance | Task remains offered |
| Target ends/revives during assignment | Refuse or explicitly revalidate; never silently bind to its successor |
| Old worker accepts/reports after a revision change | Refuse the stale transition |
| Duplicate retry or competing transition | One logical result and notice; conflicting transitions are reported |
| Output failure after persistence | Task remains queryable and notification delivery can recover |
| Claim collision after acceptance | Existing claim semantics hold; task can report the blocker |
| Result reported with open claims | Both facts remain visible; neither implies the other changed |
| Reviewer ends before closure | Explicit adoption preserves provenance and permits review to continue |
| Accepted worker disappears | Needs recovery; no automatic reassignment, cancellation or claim release |
| Hostile text or oversized evidence | Fenced, bounded output cannot forge rows or silently omit required next-page information |
| Chat absent | All guide, diagnostic and task operations still work |

A separate real-harness exercise must demonstrate two sessions completing a
bounded edit and a read-only review, including a genuinely idle recipient and
the operator prompt needed to resume it. Source review and hermetic fixtures
cannot establish live hook delivery or wake behavior.

Compare the guided exercise against today's workflow using manual prompts,
status queries, missed acknowledgements and bytes returned. These are proposed
measurements, not an existing performance baseline or promised improvement.

## Deferred scope

- Automatic waking, session spawning, a scheduler or a general dependency graph.
- Active-worker reassignment, automatic claim transfer or timeout-based cleanup.
- Role registries, stored branch fields, opaque capacity scores or inbox counts
  presented as worker fitness; D-015 already records relevant cuts.
- New JSON/MCP surfaces without a concrete consumer that needs them.
- Transcript text mining, automatic handoff summaries or routine full-history
  injection.
- A dashboard, multi-machine coordination, chat-derived control or OS containment.

## Questions for Fable

1. Does increment 1 offer a coherent front door, or should any proposed command
   be folded into an existing command to reduce discovery cost?
2. Does the task lifecycle add enough beyond templates and messages to justify
   durable state? What is the smallest useful version?
3. Are identity, revision and idempotency rules sufficient for delayed notices,
   restarts, competing coordinators and lost command responses?
4. Is reviewer adoption sufficiently explicit, and is deferring accepted-worker
   reassignment workable for the first release?
5. What is the simplest safe replacement-revision workflow for changed accepted
   requirements? Which operations must be atomic?
6. Are task scope, user authorization and actual claims clearly separated in
   both the data model and proposed user-facing guidance?
7. Are read-only diagnostics feasible without store-open migrations or other
   hidden writes, and are context budgets useful for real handoffs?
8. Which acceptance cases or live qualification steps are missing?

## Review status

Fable review: done 2026-09-20 — see [ORCHESTRATION-REVIEW.md](ORCHESTRATION-REVIEW.md).
Verdict: increment 1 ready with changes; increment 2 revise (four blockers, twelve
required changes, a design contract to disagree with); increment 3 not assessed.
Three adversarial Codex passes over the review are recorded there. No approval to
implement is implied by this document.

This draft incorporates an earlier parallel design critique, including the
need for incarnation-bound acceptance, honest output-delivery semantics,
read-only diagnostic paths and reviewer recovery in the first task release.
Implementation, schema migration, hook changes and rollout remain unperformed.
