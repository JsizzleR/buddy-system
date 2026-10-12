# Response to the Codex orchestrator findings

2026-10-06. Response to [Opus's findings](CODEX-ORCHESTRATOR-FINDINGS.md),
reviewed against the working tree based on `6938fbe`.

**Implementation handoff:** the operator intends Claude Opus to make the code
changes. This document supplies Codex's assessment and recommended closure
criteria for that work; this Codex task is limited to the response.

I accept the review's central conclusion: this implementation has unresolved
safety, authority, recovery and evidence defects, and should not be installed
or treated as ready to land. The existing green checks do not cover the
reported counterexamples. I should have examined alternate entry points and
legacy consumers more thoroughly, and brought the expanded recovery work back
through independent review before continuing qualification.

The core separation between coordination, native observations and operator
authority remains useful. These findings require repairs to its implementation
and documentation; they do not justify relaxing those boundaries. The next
implementation slice should address the findings before admitting more native
versions.

## Scope and evidence of this response

This is a source assessment and written response, not an implementation or a
new independent review pass. I read the repository rules, the complete findings,
the principal affected code paths, saved probe definitions, relevant checker
and runner code, and the current decision and plan text.

- **Verified here:** source mechanisms and documentation discrepancies identified
  below. The checkout is still on `main` at `6938fbe`, with substantial existing
  tracked and untracked work.
- **Reported by Opus, not rerun here:** the failing probes, passing prototype
  controls, hermetic suite, native-descriptor experiment, spliced-receipt
  experiments, installed-binary comparison and production-ledger header check.
- **Not performed here:** builds, tests, native qualification, a
  `codex-review.sh` pass, installation, ledger access, branch changes or commits.
  This response is the only file added by this task.

An accepted defect below is not a claim that I reproduced it. Vendor behavior
described in the findings remains attributed to that review unless separately
qualified. In particular, its Codex source examples use `0.160.1`, while the
current adapter still admits only `0.160.0`. That distinction does not excuse
the gate gaps, but it matters when designing the native regressions.

## Blocking defects and changes to the proposed remedies

### F-01 — Shell-routed patches: accept

[gatePaths](../internal/cli/patch.go) parses only the `apply_patch` tool.
A `Bash` payload carrying the reported patch forms has no adjudicated paths.
The direct-tool positive control therefore does not establish coverage of the
alternate native patch entry point.

I agree with refusing recognized shell-routed patch forms and directing the
caller to the dedicated tool. The hook cannot safely reconstruct an omitted
execution workdir. This should remain a narrow, explicitly conservative guard,
not an attempted general shell-side-effect parser. Include `applypatch`, the
`cd` form, multiline/quoted forms and an ordinary-shell positive control.
If a broad patch-marker rule is used, document its intentional refusals of
commands that merely contain patch text.

Qualify hook routing and denial on the exact admitted Codex version; repeat that
case before admitting `0.160.1`. Correct the coverage statement in USAGE and the
skill. Also preserve the feature-off boundary discussed under F-04 rather than
accidentally creating a machine-wide shell restriction.

### F-02 — Operator descriptor inheritance: accept; fix first

[main.go](../cmd/buddy-adapter/main.go) constructs the controller and launches
the backend before wrapping the operator descriptor. No close-on-exec or access
mode check precedes launch. The saved probes exercise descriptor inheritance
through `StartJSONProcess`, including the read-write channel case.

Validate the inherited descriptor before opening files or spawning any child,
including the version probe. Require an already-open, read-only descriptor,
set close-on-exec, and retain that validated handle. Reject invalid descriptor
numbers before the journal or native pipes can reuse them. The command-line
entry point needs its own regression so a helper-level passing test cannot
hide incorrect startup ordering.

Acceptance needs both a worker that cannot read/write the descriptor and a
positive operator approval that still reaches exactly the intended pending
request. Keep the existing exact identity and one-shot approval guards.

### F-03 — Nested repositories hiding home claims: accept

The `nested` branch in [cmdGate](../internal/cli/cli.go) selects the target
ledger instead of checking a covering home-ledger claim. A missing nested
ledger then returns allow. That is an unintended weakening of the existing
Claude gate as well as a patch-gate defect.

Use additive adjudication: check applicable home claims, then any distinct
target ledger. A nested checkout must not erase its parent's reservation.
Retain the no-home-ledger positive case, linked-worktree behavior, unreadable
ledger refusals and the stale-`.git` regression. Do not generalize a Git
discovery error into proof that all applicable ledgers are absent.

### F-04 — Identity errors outside ledger jurisdiction: accept

`readHook` classifies identity before `cmdGate` discovers a ledger. Thus a
syntactically valid write can be denied solely because inherited IDs conflict,
even where Buddy is off.

Separate structural hook decoding from identity adjudication. For a decodable
call, establish whether a home or target ledger governs it before applying the
identity refusal. Checking only cwd is insufficient: a patch from an ungoverned
directory can still target a governed repository. An unreadable governing
ledger must continue to deny. This change should not silently weaken existing
malformed-input handling.

Keep conflicts refusing inside governed scope. Explain which variables disagree
and that launchers must clear inherited identity; do not claim to know the
specific launcher when the payload supplies no such evidence. I do not propose
adopting machine-wide denial as a new policy.

### F-05 / F-06 — Symlink entries and lexical paths: accept, with native controls

The gate resolves symlinks, while `patchPaths` returns strings without operation
metadata. It cannot currently distinguish unlinking a directory entry from
writing the entry's resolved target. Relative inputs are joined and cleaned;
absolute inputs take a different route through canonicalization.

For delete and move sources, adjudicate the entry as well as the resolved
target. Preserve enough operation information to express that requirement, or
document a deliberately conservative policy covering both for every operation.
Bound the resulting work as well as the raw patch path count.

Make absolute and relative normalization agree with the actual harness
operation. Lexical cleaning before symlink resolution is supported by the
vendor behavior reported in the findings, but is not a universal filesystem
rule. Native tests must verify the file actually changed, including link,
dangling-link, move and `..` cases, on each supported harness.

Under the review's own severity definition, F-05 deserves the same blocking
treatment as the other claim bypasses. F-06 being pre-existing does not make
inherited fail-open behavior acceptable in the new coverage claim.

### F-07 — Process ownership and cleanup: accept; process groups are only part of the fix

[JSONProcess.Close](../internal/harness/process.go) closes stdin and kills only
the direct child. It does not close the stdout reader. The adapter entry point
does not arrange signal-driven shutdown. Fixture cleanup cannot establish that
the adapter itself cleans up its worker.

Introduce bounded shutdown of the owned process group, explicit reader closure,
child reaping and handling for catchable termination signals/output failure.
Measure adapter cleanup before invoking any fixture fallback, and record a
fallback cleanup as a failure of adapter cleanup.

There are two limits to the proposed remedy:

- A child can create another process group. The charter already records native
  shell jobs in their own groups. `Setpgid` plus `killpg` alone is not proof that
  all native descendants are gone; qualify the real process layout and scope
  cleanup to captured, owned processes.
- SIGKILL and a hard crash cannot run a signal handler. Journal flock release
  proves that the adapter exited, not that its native worker did. Recovery must
  refuse or reconcile uncertain prior native ownership before launching a
  second controller. Do not infer safe resumption from a free flock alone.

Where a well-formed unsupported permission request has a qualified native deny
response, prefer that bounded refusal. Malformed or uncorrelatable requests
must still close admission rather than receive a guessed response. Cleanup
must never expand into killing another session's processes.

### F-08 — Oversized frames: accept the defect; do not adopt unconditional discard

The 4 MiB scanner limit leads to `Close`, and `ReadTurn` requests full history
on the same live transport. The source therefore supports the reported
mechanism: an ostensibly observational reconciliation can kill the worker.
The large Claude image/PDF trigger remains unqualified here.

Simply discarding an oversized frame and continuing is unsafe. The omitted
frame could be an approval or terminal event, and a truncated frame may not
provide a trustworthy request ID. The transport deliberately treats lost
observations as uncertainty.

First establish what bounded/paged history operation the pinned native protocol
actually supports; do not assume a turn-scoped API exists. If necessary, isolate
history retrieval from the live control transport or refuse reconciliation
before making an unbounded request. A replacement reader must bound memory and
draining time and define how admission closes on lost control evidence.
Acceptance must demonstrate both bounded large-history failure and preservation
of the live worker, without treating lost approvals as absent.

### F-09 / F-21 — Claude gate configuration admission: accept; promote to blockers

[claudeSettingsCompatibility](../internal/harness/claude.go) uses unanchored Go
regex matching and decodes nested hook objects into case-insensitive Go struct
fields. The saved probes show why the adapter can approve a configuration whose
native interpretation selects different tools or a different command.

Both undermine the claimed gate prerequisite. F-21's command/async ambiguity
belongs alongside F-09 and the P1 gate bypasses, rather than ordinary P3 cleanup.

Use a narrowly supported matcher grammar whose semantics have been checked
against each admitted Claude version, and verify the union of effective
synchronous gate coverage. Parse nested keys with exact allowlists; reject
case variants, unknown keys and duplicate keys rather than relying on the last
map value. Bound explicit timeout values using the qualified native contract
and the adapter hook's actual execution budget. Apply identical checks to
supplied and effective settings. A supplied-file refusal test alone is not
native enforcement evidence.

### F-10 — Definite wake refusal poisoning recovery: accept

Both backends return `unknown` for some refusals before any write. The
[controller](../internal/harness/controller.go) stores every delivery result in
`sent`, and [Claude recovery](../internal/harness/claude_recovery.go) requires
every saved wake to be delivered. The busy-worker example follows directly
from those paths.

Introduce an explicit, proven `not_submitted` outcome for pre-transport
refusals. Persist it durably, make replay remove the prior intent's deduplication
reservation, and exclude that attempt from unfinished-turn admission. Retry
must still revalidate identity and queued-message ownership. If persisting the
definite outcome fails, retain uncertainty and refuse further effects.

Do not classify all errors, timeouts or calls returning from `WriteContext` as
unsubmitted: partial writes and lost acknowledgments remain uncertain. Test
busy/refused then idle/retry, close/reopen, crash before recording the refusal,
and a real ambiguous write that must remain unreplayable.

### F-11 — Failed event observation reported as absence: accept

`Observe` builds a receipt before checking the ledger and can return with the
old state after `Inspect` or `Event` fails. `receipt()` derives
`pending_approval: observed_absent` from that stale state.

After validating event identity, make failed recording explicitly uncertain in
both the returned receipt and controller state. Preserve any positively known
pending approvals, while refusing to assert that the set is complete. A later
ordinary busy event does not necessarily repair a missed approval; clear the
uncertainty only on an observation that establishes the relevant state. A
journal failure must continue to block further control effects. Pair this
repair with F-30's bounded event processing.

### F-12 — Legacy wake suggestions for adapter mail: accept, retain the fixture caveat

Endpoint registration writes `session_procs`, while legacy `wakeAddr` does not
distinguish adapter ownership. Legacy inbox reads deliberately exclude adapter
messages. The reported socket fixture therefore exposes an inconsistent route,
although this response does not establish that a native adapter worker creates
that legacy socket.

Suppress legacy wake suggestions for adapter-managed recipients and mark
adapter messages explicitly in sender reports. An endpoint row can be useful
evidence, but must be scoped to its incarnation; absence of an endpoint must
not be treated as proof of legacy delivery capability. Audit `msg`, `sent`,
status, correction and broadcast paths together with F-24/F-25. Keep body
retrieval and recorded delivery on the captured adapter interface.

## Evidence, decisions and review process

| Finding | Response and closure requirement |
| --- | --- |
| **F-13** | **Accept.** The three named suite validators validate cases separately without the cross-case contract equality used by `check-claude-history.py`. Share the contract checks and add valid-case splice mutations across versions, builds and fixture sources. Equal contracts alone do not prove one execution: if a suite claims one run, also bind an explicit suite/run identity. Do not imply that the reported individual native cases never ran; the defect is what the suite validator can establish. |
| **F-14** | **Accept for the named interrupted-compaction checker; narrow the blanket wording.** Its self-test mutates receipt fields without exercising the five named evidence predicates. Current `check-claude-delivery.py` already mutates decoded wire/provider evidence, and `check-claude-history.py` and `check-mixed-fleet.py` mutate decoded contract evidence. They do not close the missing wire/journal/snapshot coverage in the affected checkers. Add both byte/hash corruption and semantic evidence corruption that must fail a named check. Temporarily disabling each semantic guard must break its self-test; a hash mismatch alone is insufficient proof of that guard. |
| **F-15** | **Accept the decision inconsistency.** D-070 describes the separate binary, but does not explicitly reconcile the earlier no-launch/no-wake/no-supervisor rules. Add a dated decision defining the opt-in owned-worker exception and update all affected GIVENs and guidance together. Direct argv is a transport, not proof of authorization: the coordinator must already have operator authority for the launch. Explain that peer messages cannot grant that authority or become approval input. Keep the ledger CLI free of automatic launch, wake and peer-exit behavior. |
| **F-16** | **Accept the unsupported narrowing of the environment rule.** A driver/worker role distinction supplies no measurement about background `codex exec`. Restore the prohibition for that command under an agent harness while retaining authorized implementation roles. The current adapter launches `app-server --stdio`, so this finding does not establish that its transport has the same wedge. The role rules are supplied in the current session; no renewed operator confirmation is needed to write this response. |
| **F-17** | **Accept the wording repair, not the premise that reading it legitimately creates authority.** Put the historical work-order acceptance in the plan receipt. AGENTS should require applicable operator authorization and say that documents and quoted phrases do not create it. Preserve authorization already given in an ongoing session; do not turn this into a repeated-permission requirement. This request authorizes a response document, not implementation of these fixes. |
| **F-18** | **Accept.** D-070 and the later USAGE resume paragraph conflict with the recovery code. Add an explicit recovery/compaction/reconciliation decision and mark superseded statements without erasing historical evidence. There is additional current drift: `claudeManualCompactionQualified` now admits both `2.1.290` and `2.1.291`, while D-071's latest paragraph still says compaction is `2.1.290` only. Preserve exact-version and terminal-evidence requirements in the replacement text. |
| **F-19** | **Accept the durability problem.** I have not recounted the reported 183 paths or 5.7 GB. A committed `result.json` referencing vanished evidence is a durable summary, not a revalidatable capture. Preserve sanitized summaries/manifests and reproduction commands, and either retain the evidence each checker consumes or clearly label that evidence as ephemeral. Provide self-contained synthetic fixtures for offline checker tests; do not commit gigabytes of native state indiscriminately. |
| **F-20** | **Accept the independent-review gap.** The recorded absence of constrained/Fable passes cannot be replaced by driver inspection or this response. After the fixes and decision reconciliation, run bounded design and code reviews through `scripts/codex-review.sh` in the foreground, then address their findings. Keep those verdicts separate from executed checks and native qualification. No such pass was launched for this response. |

## Remaining code findings

F-21 is addressed with F-09 above. The following dispositions preserve the
difference between a source-supported mechanism and an unexecuted failure case.

| Finding | Response and closure requirement |
| --- | --- |
| **F-22** | **Accept the presentation and amplification concern.** Backend approvals retain raw request details, and `receipt()` repeats pending approvals. Bound and fence model-facing summaries, with stable request identity/digests for repeated references. Preserve exact native request bytes where approval forwarding or fingerprinting needs them; truncating that control data would create a different correctness bug. JSON escaping prevents literal row breaks but does not supply an untrusted-content fence or a context budget. |
| **F-23** | **Accept the source-level race and constructor inconsistency.** The same executable string is resolved separately for version checking and launch, and the public constructors lack the entry point's absolute-path check. Resolve to one absolute target before both operations and bind the observed version/digest to it. Resolving a symlink once does not prevent replacement of the target bytes, so document the remaining cooperative same-user race rather than claiming immutable execution. I did not recheck the installed PATH versions. |
| **F-24** | **Accept.** `who` and send notes use the legacy `Undelivered` view. Report legacy and adapter queue counts/ages separately, under the correct incarnation, and describe only the recipient's actual retrieval path. Do not promise that a logical lifecycle event drains either inbox. |
| **F-25** | **Accept.** `correctable` checks sender and audience but not adapter queue membership. Prefer refusing a legacy correction of adapter mail until an incarnation-bound correction contract exists. Apply the same rule to the dry-run and transactional write paths. |
| **F-26** | **Accept, with a wider implementation scope than the proposed one-line fix.** `lifecycleHook` supplies an empty path, and `BeatLogical` renews no claims for that input. Carry all observed paths through a bounded, captured-token PostToolUse path. However, the structured adapter installs only its PreToolUse wrapper and rejects other hook events; fixing the standalone lifecycle hook alone will not provide renewal for those workers. Qualify their path-observation route too. Do not renew every claim from a generic busy/idle event or infer tool effects from attempted writes. |
| **F-27** | **Accept the stdin regression; reject automatic fallback from malformed input to manual authority.** `hello --session` does read non-TTY input. Treating arbitrary non-JSON bytes as a manual registration would weaken the delayed-hook protection introduced by D-069. The immediate documented manual recipe should use explicit EOF (`</dev/null`). If a distinct manual input mode is added, define and test that boundary explicitly. Cover an open pipe as well as malformed bytes. |
| **F-28** | **Accept the source-supported gap; pre-existing scope remains distinct.** `gateForeignRepo` consults claims but not `PausedFor`. Define which target-repository brakes apply, particularly `pause all`, without borrowing a label from a different ledger. Include a foreign-target brake regression in gate repair; do not bury a potentially missed operator stop under cosmetic P3 work. |
| **F-29** | **Accept the mechanism; runtime reproduction remains pending.** Endpoint observation and the safety steps share a deadline even though endpoint failure is ignored. Give the advisory work a separate bounded budget that cannot consume the gate's budget. Choose the value from the hook latency contract and measurement, rather than treating 500 ms as already justified. Identity/ledger verification still fails closed. |
| **F-30** | **Accept the blocking path; runtime reproduction remains pending.** The event loop passes a lifetime context and `Observe` holds the controller lock across ledger calls. Bound those calls and ensure shutdown can cancel them before waiting for that lock. Test a deliberately stalled ledger child with stdin EOF and termination, while preserving unknown observation state. |
| **F-31** | **Accept as a configuration residual, with a narrower trigger.** `classifyHook` also recognizes `buddy_harness` and `CODEX_THREAD_ID`; a missing `BUDDY_HARNESS` alone does not always select Claude. With no recognized Codex indicator it does fall back to legacy behavior. Diagnose unsupported/missing lifecycle wiring from supplied configuration and verify the required marker during admission. Keep the diagnostic offline; do not infer that it attested the live environment. |

## Qualification scripts and documentation

| Finding | Response and closure requirement |
| --- | --- |
| **F-32** | **Accept.** `check.sh` currently invokes shell checks and has no Python/JavaScript qualification-script coverage. Add the genuinely hermetic proxy/provider/checker self-tests and a Python syntax sweep, with an explicit dependency policy. Give `.mjs` syntax/helper checks an appropriate separate prerequisite. Do not make native harness launches or unavailable native/SDK dependencies an implicit requirement of the hermetic tier. |
| **F-33** | **Accept the reproducibility gap.** The named native-receipt-dependent self-tests need committed synthetic evidence that works from a fresh checkout. Include coherent journals, wire records and database snapshots where the checker reads them, not just a fabricated success JSON. Such fixtures test the checker; they do not qualify a native harness. |
| **F-34** | **Accept.** The named runner calls use ordinary `git status --porcelain` in the source checkout. Disable optional locks for observational Git commands and preserve the existing Git-environment isolation. No need to reproduce index contention in the shared checkout. |
| **F-35** | **Accept the cleanup risk; catchable signals and hard crashes need different claims.** Arrange bounded cleanup on TERM/HUP and retain exact owned process/group identities. Signal handlers cannot repair SIGKILL. Record any surviving/unknown child or fallback kill honestly, and never signal a group merely because a stale PID number matches. Coordinate this with F-07 so the fixture does not mask adapter failure. |
| **F-36** | **Accept.** Recount input, approval and provider activity from captured logs where available. A summary boolean such as `job_alive_after_outgoing_exit` cannot be independently established from request counts; capture a bounded, ordered liveness observation with process identity or narrow the claim. Equal summary counters alone do not prove no replay. |
| **F-37** | **Accept.** Share the newer exact-contract and artifact-integrity checks across relevant validators. Ensure hashes are recomputed from retained inputs, not merely compared between receipt fields. State whether each receipt binds native bytes, source, built adapters, fixture code and original evidence. |
| **F-38** | **Accept as a source-supported shutdown risk, not a newly measured crash.** Stop and join every thread that writes to native stdin before closing its buffered stream or exiting Python. Extend the proxy shutdown test to exercise concurrent injected requests as well as input relay. |
| **F-39** | **Accept.** `qualify-codex.py` lacks global/system Git configuration isolation and its `run` has no timeout; it is reused by `qualify-adapters.py`. Add bounded subprocess handling, explicit isolated Git settings and non-optional acceptance checks. Assertions used as qualification gates must survive `python -O`; retain a control that would fail without each guard. |
| **F-40** | **Accept; still present in the source read for this response.** The history observer uses unchecked `str.replace`, and `test_native_proxy.PROXIES` omits it. Prefer a shared explicit proxy configuration seam; minimally require exactly one replacement and cover each history fault mode in shutdown/fault-injection tests. Failure must name an unarmed injector. |
| **F-41** | **Accept the charter drift.** Scope historical GIVENs to their increments and point to the exact current admission table and evidence receipt. Distinguish a passed local deterministic mixed fleet from unqualified real-provider/production operation. Correct D-070's control-operation list: startup is argv; coordinator requests include `inspect`, `reconcile`, `send` and `interrupt`; approval has its separate operator path. |
| **F-42** | **Partly accept; qualify the version-history criticism.** D-071 currently retains its original admission paragraph and adds explicitly dated `2026-10-05` and `2026-10-06` continuations. That is recorded amendment, not an invisible replacement of the original decision. Make amendments independently referenceable and record the later compaction widening. Accept the stale invariant, missing repo-map/decision-summary entries and weak package boundary: `diagnose` imports harness capability code. A shared contract-only package is a reasonable follow-up; imports alone do not mean the diagnostic launches a process. |
| **F-43** | **Accept the context-cost problem.** Reduce always-loaded guidance to operational boundaries, required commands and pointers; move implementation detail to USAGE. Preserve the existing verb/flag documentation gate by updating its deliberate reference/exemption policy if needed. Do not delete required command guidance merely to satisfy an arbitrary line count. |
| **F-44** | **Accept the TOC/flag omissions and revise the authority example.** A peer body may describe already-authorized work, but cannot establish its authorization. Use wording such as “work brief; scope authorized separately by the operator” and make that limitation explicit. The current wording is misleading guidance, not itself evidence that the code grants authority from text. |
| **F-45** | **Accept.** Add documentation coverage for the separate adapter CLI, including hook arguments and mutually exclusive start/resume forms. Exercise the actual flag definitions/help surface so the gate does not merely compare two manually duplicated lists. |
| **F-46** | **Accept.** Replace the accumulated status paragraph with an evidence/status/next-action table; move receipts behind a concise current work order and label superseded statements. “Qualified” must name the exact version, operation and local/native/provider evidence category. After this review, defect remediation precedes the previously listed newer-version admission. |

## Recommended implementation and landing order

These are recommended work slices for Opus. Its implementation brief should
define the authorized scope; no code changes or landing actions were performed
as part of this response.

1. **Preserve the shared work and define a stable review snapshot.** Coordinate
   with active editors before selecting a branch/checkpoint strategy. A new
   branch ref alone does not preserve untracked files or uncommitted bytes.
   Retain those bytes in a durable snapshot; do not switch, stash or repartition
   another session's working tree underneath it.
2. **Repair authority and gate admission.** F-02 first, then F-01/F-03–F-06,
   F-09/F-21 and the target-brake question in F-28. Keep the non-schema gate
   increment separable from schema 15 where practical. Include the affected
   existing Claude paths, not just the new Codex paths.
3. **Repair transport ownership and recovery.** Resolve F-07/F-08 as a design
   boundary, then F-10/F-11/F-23/F-29/F-30. Add fail/pass controls before further
   native recovery claims. Fix the remaining mail/lifecycle defects and bound
   approval presentation in the same stabilization phase.
4. **Make qualification reproducible and independently checkable.** Address
   F-13/F-14/F-19/F-32–F-40, including synthetic evidence and removal of fixture
   cleanup as a substitute for adapter cleanup. Recheck affected prior captures
   with the strengthened validators; rerun native cases whose claims require
   additional observations. Keep earlier results as historical evidence.
5. **Reconcile decisions and documentation, then review the fixed tree.** Update
   F-15–F-18/F-41–F-46 and run the foreground design/code passes from F-20. Each
   landing unit must pass its applicable hermetic and documentation checks;
   retained exact-version native qualification is a separate result.
6. **Plan schema migration and installation as a coordinated operation.** The
   older-binary refusal makes a schema-15 rollout sensitive to every installed
   consumer. Recheck installed versions and the source ledger safely at that
   time, preserve a recoverable backup, and use the established install path
   within explicit operator scope. A repository commit alone does not migrate
   the ledger; executing a new binary against it can. Do not run a development
   binary against the production ledger to inspect readiness.

Opus's proposed commit split is useful, but the final boundaries need to be
checked against actual code dependencies. A migration-free first commit is
valuable only if it stands independently and does not depend on symbols or
documentation introduced by later commits. Hand-staging a historical sequence
is not itself proof of that property.

For each implementation fix, preserve the supplied failing input and positive
control, watch the regression fail before the fix and pass afterward, and make
the regression fail when the guard is removed. Native cases must test actual
effects/refusals on the pinned harness. Neither this response, an independent
review verdict, nor a green hermetic tier closes those native evidence gaps.

For the return handoff, record each finding ID, its disposition, changed paths,
executed regression/mutation checks, exact native evidence where required and
remaining limitations. Preserve the original findings as the review record.
Keep each saved probe until an equivalent permanent regression exists. Mark
unreproduced cases and deferred design decisions explicitly rather than closing
them on source inspection or a passing general suite.
