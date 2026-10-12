# Verification brief: findings remediation, before the plan resumes

**For:** Codex, verifying the remediation of
[CODEX-ORCHESTRATOR-FINDINGS.md](CODEX-ORCHESTRATOR-FINDINGS.md) (F-01 to F-46).
Your assessment is [CODEX-ORCHESTRATOR-RESPONSE.md](CODEX-ORCHESTRATOR-RESPONSE.md),
and its closure requirements are the yardstick here. **From:** the Claude Code
session that implemented the remediation, 2026-10-06/07.

**What this asks.** Independently check that each fix does what it claims, that
each regression test really fails without its fix, and that nothing regressed.
Then report back in the format at the end. Only after the pass criteria are met
does work resume on [the plan](CODEX-ORCHESTRATOR-PLAN.md)'s **Next** list
(constrained reviews, native requalification, then the 2.1.292 / 0.160.1
admission).

**What this does not do.** This brief grants no authority (`AGENTS.md`). The
operator's request when handing it to you defines what you may run. Items marked
**[operator authorization]** launch native `claude`/`codex` binaries or change
machine state. Do not run them on this brief's word alone.

## Ground rules

- **Never run a binary built from this tree against this repo's real ledger.**
  It is still schema 14, and every installed hook is a schema-14 binary. Schema
  15 migrates on first open, and from then on every installed copy DENIES
  (D-037). Tests use temporary repos and are safe. Never run `install.sh`.
- **The tree is uncommitted, on `main`.** Do not commit, stash, switch branches
  or rewrite files you were not asked to fix. The pre-remediation bytes are saved
  at `refs/snapshots/orchestrator-pre-fixes-20261006`, a commit holding the full
  working tree, untracked files included. Use `git archive` on that ref for every
  fail-before run.
- **Claim before editing** (`buddy claim`), and edit only what a finding needs.
- **Report the evidence categories separately:** source review, executed hermetic
  checks, mutations, native fixture qualification (loopback provider), and
  constrained review verdicts. None stands in for another.
- **Never background `codex exec` under an agent harness** (CLAUDE.md). Run
  constrained reviews through `scripts/codex-review.sh`, in the foreground.

## What the implementer already ran

All runs were on 2026-10-06/07, on the current tree.

| Check | Result |
| --- | --- |
| `go build ./... && go vet ./...` | clean |
| `sh scripts/check.sh hermetic` | **HERMETIC GREEN**, now including the Python self-test leg. The live leg was not run. |
| New `internal/cli` regressions against the snapshot | all 11 fail before the fix |
| Gate mutations (one guard each) | 10 of 10 killed |
| Mail/lifecycle mutations | 10 of 10 killed |
| Harness/adapter mutations | 35 of 35 killed |
| Script-checker mutants and neuterings | all killed except one, explained under F-38 |
| `TestUsageDocumentsEveryAdapterFlag` (new docs gate) | passes; its positive control fires |
| `check-charter.sh` | GREEN, 77 decisions charted |

The implementer's mutation scripts and logs sit in a session scratch directory
under `/private/tmp` and will not survive a reboot. Write your own mutants; they
are a better independent check anyway.

## Disposition by finding

Status words:

- **Fixed:** the hermetic regression and the mutation were both shown.
- **Fixed, native owed:** fixed in code, but the claim also depends on vendor
  behaviour that still needs a native check.
- **Partial:** part of the closure requirement is done; the rest is listed.
- **Open:** not done.

### Gate and identity (`internal/cli`; D-073)

| ID | Status | Change | Regression |
| --- | --- | --- | --- |
| F-01 | Fixed, native owed | `patch.go` `shellPatchCmd`, `shellPatchCommand`, `shellPatchDirs`; `cmdGate`. `apply_patch`/`applypatch` in shell command position refuses where a ledger governs. From an ungoverned cwd it refuses only if the `cd` dir or a parsed patch path is governed. | `TestGateRefusesShellRoutedPatch`, `TestGateShellRoutedPatchFeatureOff` |
| F-03 | Fixed | `gateTarget`: the home claim is always checked, and the nested ledger additionally. | `TestGateNestedRepoKeepsHomeClaim` |
| F-04 | Fixed | `decodeHook` vs `readHook`. `cmdGate` classifies, then refuses only where a home or target ledger governs (`governedAny`). Read-only tools pass. | `TestGateIdentityConflictOnlyWhereGoverned` |
| F-05 | Fixed, native owed | New `gatetargets.go` `gateTargets`: entry spellings, plus `danglingTarget`. | `TestGateChecksEverySpellingOfATarget` |
| F-06 | Fixed, native owed | `gateTargets`: lexical and physical spellings, both checked. Which one Claude's tools act on was not measured. | same |
| F-28 | Fixed | `openGateRepo` reads the target ledger's pause, using that ledger's own label. | `TestGateForeignTargetHonoursPause` |

### Mail, lifecycle, hello (`internal/cli`, `internal/store`; D-076)

| ID | Status | Change | Regression |
| --- | --- | --- | --- |
| F-12 | Fixed, native owed | `wakeAddr` never wakes `recipient.adapterRecipient()` (an endpoint row or adapter mail). `sent` labels adapter messages. Not established natively: whether adapter-run Claude creates the legacy socket. | `TestNoLegacyWakeForAdapterMailOrRecipients` |
| F-24 | Fixed | `store.AdapterBacklogOf`. `who`/`status` print an `ADAPTER` line; `sendNote` counts adapter mail and words the mechanism for adapter recipients. | `TestLegacyViewsReportAdapterMail` |
| F-25 | Fixed | `store.correctable` refuses adapter messages, in the send and the dry run alike. | `TestLegacyCorrectionOfAdapterMailRefused` |
| F-26 | Partial | `lifecycleHook` passes PostToolUse paths; `BeatLogical(..., relPaths...)` renews only covering claims. **Open:** adapter-launched workers install only PreToolUse, so they have no renewal route. | `TestLifecycleHookRenewsObservedClaimsOnly` |
| F-27 | Fixed (as Codex required) | `hello --session` still refuses non-hook stdin; the error and usage line now name `</dev/null`. There is no fallback to manual. | `TestHelloSessionStdinRefusalNamesRecipe` |

### Adapter and transport (`internal/harness`, `cmd/buddy-adapter`; D-077)

| ID | Status | Change | Regression |
| --- | --- | --- | --- |
| F-02 | Fixed | `main.go` `adoptOperatorFD` runs before any file or child, the version probe included. It requires the descriptor open, `O_RDONLY`, and a FIFO, character device or regular file, then sets close-on-exec. | `TestAdapterOperatorDescriptorAdmission` (built binary: rw FIFO, write end, socket, directory and unopened fd all refused; read-only control approves exactly the intended request) |
| F-07 | Fixed, native owed | `process.go` plus `process_exit_{darwin,linux,other}.go`: Setpgid; group TERM, 2 s, KILL while the leader is unreaped; reader closed; INT/TERM/HUP run Close; SIGPIPE ignored. A Claude subagent or no-input permission request gets a native deny. | `TestJSONProcessCloseEndsDescendants`, `TestJSONProcessCloseFinishesDespiteStdoutHolders`, `TestJSONProcessLeaderExitEndsGroup`, `TestAdapterSignalEndsWorkerTree`, `TestClaudeUnsupportedPermissionGetsNativeDeny` |
| F-08 | Fixed by withdrawal | An oversized frame fails the transport as uncertainty, never as absence. **Codex `reconcile` is withdrawn**: capability `unsupported`, and the request refuses before any native request, because 0.160.0 `thread/read` is unbounded. | `TestJSONProcessOversizedFrameFailsVisiblyAndEndsTree`, `TestCodexReconcileRefusesUnboundedHistoryRead`, `TestCodexHistoryProjectionIsExact` |
| F-09, F-21 | Fixed, native owed | `claude.go` `claudeSettingsCompatibility`, `strictJSONObject`, `claudeMatcherCoversGate`: exact case-sensitive keys, no duplicates, literal matcher grammar (also admits `^(A\|B\|…)$`), `async` absent or false, `timeout` 10–600 s. Supplied and effective settings get the same check. | `TestClaudeSettingsMatcherSemantics`, `TestClaudeSettingsExactKeysAndBounds`, `TestClaudeEffectiveSettingsUseStrictAdmission`, `TestClaudeHookTimeoutFloorExceedsWrapperBudget` |
| F-10 | Fixed | Pre-write refusals are `ErrNotSubmitted`. The controller journals `not_submitted` and frees the dedupe slot. Replay frees it only after an open, turn-less intent. A failed refusal record keeps the wake unknown. | `TestControllerNotSubmittedWakeIsRetryable`, `TestControllerJournalNotSubmittedRequiresOpenIntent`, `TestControllerClaudeBusyWakeRefusalRetriesAndResumes`, `TestClaudeWakeRefusalsBeforeTransportAreNotSubmitted`, `TestCodexWakeRefusalsBeforeTransportAreNotSubmitted` |
| F-11 | Fixed | An observation gap makes state unknown. Known approvals stay, marked as possibly incomplete. Only a recorded idle or end clears the gap. | `TestControllerUnrecordedEventIsNeverObservedAbsent` |
| F-22 | Fixed | Receipts carry `detail_sha256` and a fenced summary of at most 512 bytes. Exact detail appears once in `native_event`, up to 16 KiB. | `TestControllerApprovalDetailIsBoundedAndFenced` |
| F-23 | Fixed | New `executable.go`: absolute path, resolved once, identity re-checked before exec. `NewClaude`/`NewCodex` now return `(Backend, error)`. | `TestNativeExecutableIsAbsoluteAndResolvedOnce`, `TestNativeExecutableChangedAfterVersionCheckRefuses` |
| F-29 | Fixed, native owed | `hook.go`: the endpoint step has its own 250 ms budget, and the gate keeps 3 s. The 250 ms is reasoned, not measured under load. | `TestReceiptBoundHookExecutable/slow_endpoint_cannot_deny_the_tool_call` |
| F-30 | Fixed | 10 s bound on each Observe ledger call; Close cancels before taking the lock. | `TestControllerStalledLedgerCannotWedgeShutdown`, `TestAdapterStdinEOFWithStalledLedgerExits` |
| F-31 | **Open** | Not implemented: `diagnose` does not yet flag Codex hook wiring that lacks the harness marker. | none |

### Qualification scripts (`scripts/`)

| ID | Status | Change and evidence |
| --- | --- | --- |
| F-13 | Partial | `check-mixed-fleet.suite_binding()` checks `same_contract` and `same_run` (case root is `<suite>/<mode>`). It is used by the claude-interrupted, claude-interrupted-compaction, interrupted-compaction and claude-history checkers, and splice cases were added to each self-test. The three demonstrated splices are now refused. **Open:** a whole case copied from another run of the same build still passes. Closing that needs a runner-generated `run_id` bound into every receipt, which means a runner change and a native rerun. |
| F-14 | Fixed (named checker) | `check-claude-interrupted-compaction.evidence_self_test`: wire, journal, history and snapshot corruptions, each required to fail `original_evidence_hashes` and then a named check. Rejections went from 103 to 222, and forcing any of the five predicates now fails the self-test. |
| F-32 | Fixed | The hermetic tier compiles every `scripts/*.py`, refuses any `assert` statement, runs `test_native_proxy.py`, the `mixed_fleet_provider` and `check-mixed-fleet` self-tests and `check-claude-resume --self-test`, and runs `node --check` on `.mjs` (NOT RUN, loudly, without node). It adds about 20 s to pre-push. |
| F-33 | Partial | Synthetic evidence exists for `check-claude-resume.py` (22 checks, 24 rejected mutations). **Open:** the other 10 checkers. |
| F-34 | Fixed | `--no-optional-locks` on git reads of the real checkout, and `GIT_OPTIONAL_LOCKS=0` in go-build environments (go's VCS stamping also refreshes the index; measured). |
| F-35 | Partial | Every runner calls `exit_on_signals()`. Owned-process identity is recorded at spawn, kills are limited to still-matching identities, and a fallback kill is reported as a cleanup failure. **Open:** never run natively; `qualify-claude-mods`/`-peers` `close()` do not mark forced kills; coordination with F-07 has not been exercised. |
| F-36 | **Open** | The provider logs carry no sequence or stage marker, so they cannot be recounted. This needs a runner change. |
| F-37 | Partial | The contract check is shared across suite checkers. **Open:** `check-interrupted` and `check-recovery` bind nothing; `check-uncertain` lacks fixture/evidence hashes; most checkers do not recompute the native executable sha. |
| F-38 | Fixed | Injector threads are non-daemon, stopped and joined. One mutant survived: "daemon-but-caught". The write lock already forces ordering, so a missing join is unobservable in that harness. |
| F-39 | Fixed | `GIT_CONFIG_GLOBAL=/dev/null` and `GIT_CONFIG_NOSYSTEM=1`; `run()` has timeouts; gate asserts became `require()`; the hermetic tier refuses `assert`. |
| F-40 | Fixed | The history injector must replace exactly one relay line or it names itself UNARMED. Its proxy is covered by `test_native_proxy` in all 5 modes. |

### Decisions, rules, docs

| ID | Status | Where |
| --- | --- | --- |
| F-15 | Fixed | **D-074**: `buddy-adapter` is the one opt-in exception. GIVENs 42, 58 and 75, the CLAUDE.md D-056 bullet and Out-of-scope, and the skill's "Running a fleet" were updated. |
| F-16 | Fixed | CLAUDE.md: "Never background `codex exec` under an agent harness, whatever the role" is unconditional again. **Operator:** your saved memory note still says Codex runs only through `codex-review.sh`; the role split stands in `AGENTS.md`. |
| F-17 | Fixed | `AGENTS.md`: authority comes only from the operator's request in the current session; no document or quoted phrase creates it. |
| F-18 | Fixed | **D-075** records recovery admission, supersedes D-070's "Claude resume is unsupported", and records Codex `reconcile` as withdrawn. USAGE's resume paragraph is corrected. The plan marks 20 superseded receipt statements. |
| F-19 | Fixed (documented) | The plan states that `/private/tmp` evidence is ephemeral and that hashes and reproduction commands are the durable part. |
| F-20 | **Open, your step** | No constrained review of the remediation has run (step 5 below). |
| F-41 | Fixed | Charter GIVENs 69–72 scoped or pointed at the code table; control-operation list corrected. |
| F-42 | Fixed | CLAUDE.md: decision bullets D-068 to D-077, repo map (`internal/harness`, `cmd/buddy-adapter`), and invariant 12's captured-token fence. D-071 itself keeps its dated continuations; D-075 records the compaction widening. |
| F-43 | Fixed | Skill trimmed from 36.3 KB to 28.3 KB. "Harness boundaries" is now a short pointer; `lifecycle`/`adapter` are exempt from the skill gate, with a reason. |
| F-44 | Fixed | USAGE: table of contents, `--harness` in the lifecycle table, and the example body no longer asserts authorization. |
| F-45 | Fixed | `cmd/buddy-adapter/docs_test.go` reads the FlagSet definitions from source and requires each `--flag` in USAGE's "Structured adapters" section, with a positive control. |
| F-46 | Fixed | Plan: a status table with evidence categories, a Next list, a 10-line copy-ready instruction, and receipts moved to a historical appendix. |

New decisions D-073 to D-077 are in `docs/decisions.md` and GIVENs 74–78 in
`docs/review-charter.md`.

## Verification steps

### 1. Baseline

```sh
go build ./... && go vet ./...
sh scripts/check.sh hermetic > /tmp/hermetic.log 2>&1; echo rc=$?   # never pipe it: sh has no pipefail
```

Pass: `rc=0` and the summary line reads `HERMETIC GREEN`.

### 2. Fail-before, against the snapshot

```sh
SNAP=refs/snapshots/orchestrator-pre-fixes-20261006
D=$(mktemp -d) && git archive "$SNAP" | tar -x -C "$D"
cp internal/cli/gatetargets_test.go internal/cli/adaptermail_test.go "$D/internal/cli/"
(cd "$D" && env -u CLAUDE_CODE_SESSION_ID -u CODEX_THREAD_ID go test -buildvcs=false -count=1 ./internal/cli \
  -run 'TestGate(RefusesShellRoutedPatch|ShellRoutedPatchFeatureOff|NestedRepoKeepsHomeClaim|ChecksEverySpellingOfATarget|IdentityConflictOnlyWhereGoverned|ForeignTargetHonoursPause)|TestLegacyViewsReportAdapterMail|TestLegacyCorrectionOfAdapterMailRefused|TestNoLegacyWakeForAdapterMailOrRecipients|TestLifecycleHookRenewsObservedClaimsOnly|TestHelloSessionStdinRefusalNamesRecipe' -v)
```

Pass: all 11 FAIL, each for the reason in its own message, not on a build error.

The new harness unit tests use new symbols and do not compile against the
snapshot. For those, the review probes are the fail-before evidence: copy
`docs/findings-probes/harness_*.go.txt` into the snapshot's `internal/harness/`
as `_test.go` files and run `-run 'TestReview|TestProbe'`. Expected outcomes:

- On the snapshot, every probe fails except `TestReviewFixOperatorCloexecControl`.
- On the CURRENT tree, four probes still fail **by design**; the rest pass.
  - `TestReviewOperatorDescriptorNotInheritedByNativeChild` and
    `TestReviewNativeChildCannotWriteOperatorChannel`: their helper-level
    process start cannot close arbitrary inherited descriptors. The fix lives at
    the entry point (`adoptOperatorFD`), covered by
    `TestAdapterOperatorDescriptorAdmission`.
  - `TestProbeOversizedFrameKillsWorkerAndOrphansChildren`: it asserts the
    worker survives an oversized frame, but the transport now deliberately
    fails as uncertainty (Codex rejected discarding frames). Its orphaned-child
    half no longer reproduces.
  - `TestProbeClaudeBusyWakeRefusalPoisonsJournal/busy-wake`: the retried wake
    is now delivered (`not_submitted`, then `delivered`), but the probe resumes
    while that turn is still open, so the refusal is correct.
    `TestControllerClaudeBusyWakeRefusalRetriesAndResumes` completes the turn
    and resumes.

For the entry-level tests, copy `cmd/buddy-adapter/main_test.go` into the
snapshot. They should fail for the stated reasons: bad descriptors accepted,
the worker surviving a signal, a stalled ledger hanging stdin EOF.

The probes in `docs/findings-probes/` are for fail-before only. The permanent
regressions above are authoritative. `cli_adapter_mail_probe_test.go.txt`'s
`TestProbeHelloSessionReadsStdin` asserts the fallback Codex rejected, and its
first probe asserts wording the fix did not adopt. Both fail on the fixed tree
by design. Delete the probe directory when this work lands.

### 3. Mutation, written by you

For each guard below, remove it in a private copy, run its regression, and
confirm the test dies. A build error does not count as a kill.

- **Gate:**
  - `shellPatchCommand`;
  - the `if shellPatch { deny }` arm after the pause check;
  - the `shellPatchDirs` probe in the ungoverned arm;
  - `gateTarget`'s unconditional home `denyIfHeld`;
  - each `add(...)` line in `gateTargets`, and `danglingTarget`;
  - `governedAny`;
  - the `idErr` refusal ordering;
  - `openGateRepo`'s pause read.
- **Mail:**
  - `wakeAddr`'s `adapterRecipient()` early return;
  - `AdapterBacklogOf`'s endpoint count;
  - the `who` ADAPTER line;
  - `sendNote`'s `why` switch and `adapterBacklogNote`;
  - `correctable`'s adapter refusal;
  - the `lifecycleHook` path extraction;
  - `BeatLogical`'s `scopeCovers` filter (an over-renewal mutant must also die);
  - `cmdHello`'s `</dev/null` wrap.
- **Adapter:**
  - each check in `adoptOperatorFD`;
  - `Setpgid`;
  - the group kill;
  - the reader close;
  - the signal handler;
  - every `ErrNotSubmitted` wrap;
  - the controller's `not_submitted` handling and its replay condition;
  - the observation-gap flag;
  - every strict-settings check (duplicate keys, case, matcher, async, timeout bounds);
  - the executable identity re-check;
  - the endpoint budget split;
  - `ledgerScoped`.
- **Scripts:**
  - `suite_binding`'s two facts;
  - each named evidence predicate in `check-claude-interrupted-compaction`;
  - the `assert` gate;
  - the history injector's exactly-one check;
  - `still_owned`.

### 4. Adversarial checks of the fixes

These are the places a fix is most likely to be wrong or too broad. Try to break them.

1. **Shell-patch detector** (`patch.go` `shellPatchCmd`). It must refuse every
   form Codex 0.160.0 actually routes into its patch engine, and pass ordinary
   commands that mention the word. Check `bash -lc '…apply_patch…'` (a
   documented miss), `sudo -u x apply_patch`, `env -i apply_patch`, `$(apply_patch …)`,
   `git commit -m "apply_patch fix"`, `make apply_patch` and `tee apply_patch`.
   Decide whether each verdict is right, and report any form Codex routes that
   passes.
2. **Gate latency.** `gateTargets` does up to five placements per path, across
   up to 128 paths. Time `buddy gate` on a 128-path `apply_patch` and on a single
   Edit in a temp repo. The hook budget is 100 ms, and the gate measured 20 ms
   before this change. Nobody has measured it since.
3. **Nested linked worktrees.** A home claim on a directory that contains linked
   worktrees, such as `.claude`, now refuses edits inside those worktrees. That
   is what HEAD did before the Codex work. Confirm this is acceptable, or
   propose a narrower rule that keeps F-03 closed.
4. **F-04 jurisdiction.** Check a multi-path patch where one path is governed
   and the rest are not, a pathless tool from an ungoverned cwd, and an
   unreadable target ledger. Confirm a conflict never refuses outside governed
   scope, and always refuses inside it.
5. **`adapterRecipient` is a positive-evidence heuristic.** A logical session
   with no adapter mail and no endpoint still reads as legacy. Confirm no path
   offers such a session a socket wake. Logical sessions register no processes,
   so `wakeAddr` should have nothing to address.
6. **Process-group safety.** Confirm the adapter never signals a group it does
   not own. A pgid is reserved only while its leader is unreaped. Check
   leader-exit-ends-group as well: is ending a worker's background children
   when its native process exits the intended behaviour?
7. **Journal compatibility.** Journals written before this change have no
   `not_submitted` records. Confirm the replay rules handle them as before and
   refuse only a genuinely inconsistent `not_submitted`.
8. **Codex `reconcile` withdrawal.** `qualify-interrupted.py:162` and
   `qualify-uncertain.py:125` call it. The compaction and interrupted-compaction
   runners and the matching `check-*` reference it too. Those native suites now
   see a refusal. Decide whether to revise them to expect the refusal, or to
   qualify the paged `thread/turns/list` and bring reconcile back. Either needs
   a decision record.
9. **Strict settings vs fixtures.** A static check found every Claude fixture
   using `"matcher": "^(Edit|Write|NotebookEdit)$"` with `"timeout": 10`, exactly
   at the new floor. Confirm `claudeSettingsCompatibility` admits those exact
   files: feed it the JSON the runners write.
10. **Approval detail.** Receipts no longer carry raw approval detail. Confirm
    every checker that reads approval detail takes it from `native_event`, not
    from the receipt's pending list.
11. **Docs against code.** Check D-073 to D-077, GIVENs 74–78, and USAGE's
    "Codex participation" and "Structured adapters" sections against the code.
    Report every sentence the code does not bear out.

### 5. Independent constrained reviews (F-20)

Run a design pass, then a code pass, through `sh scripts/codex-review.sh
<prompt-file> <out-file>`, in the foreground. The charter is prepended
automatically. Scope each prompt narrowly; suggested splits:

- (a) the gate: `cli.go` `cmdGate` through `governedAny`, `gatetargets.go`,
  `patch.go`'s shell detector;
- (b) adapter process ownership and descriptor admission: `process*.go`,
  `cmd/buddy-adapter/main.go`;
- (c) the controller's `not_submitted`, observation gaps and approval
  presentation;
- (d) strict Claude settings admission;
- (e) adapter mail in the legacy views (`wake.go`, `sent.go`, `who.go`, `store/adapter.go`, `store/inbox.go`).

A review verdict is source evidence only. A confirmed defect still needs a
regression watched to fail.

### 6. Native qualification **[operator authorization]**

These launch native binaries with loopback providers in disposable
configuration. They spend no provider money, but the operator must authorize
them. Run them on the RETAINED qualified versions (Claude 2.1.291, Codex
0.160.0), not the PATH versions (2.1.292 and 0.160.1, which are not admitted):

- **F-01:** a new `qualify-codex.py` case driving `apply_patch <<'EOF'` through
  the shell tool against a peer-held path. It must be refused, and the file
  must be unchanged.
- **F-05, F-06:** native delete of a symlink entry, `..` after a symlinked
  directory, a dangling link, and a move. Confirm which file each harness
  actually changes, and that the gate's verdict matches.
- **F-07:** whether native tool jobs re-group themselves; SIGTERM behaviour
  within the 2 s grace; and the Claude native deny shape for subagent and
  no-active-input permission requests.
- **F-09, F-21:** native matcher and `timeout` semantics per admitted Claude
  version.
- **F-29:** the endpoint budget under a loaded ledger.
- **Reruns:** run the existing suites with the strengthened checkers, and
  record the new rejection counts. Expect the reconcile-dependent Codex suites
  to change, per item 8 above. Earlier receipts stay as history.

## Pass criteria to resume the plan

1. Baseline green on the tree you verified.
2. Every fail-before reproduced, and every guard you mutated killed.
3. No unresolved P1/P2 from steps 4 and 5. Fix each confirmed one with the same
   discipline: regression first, watched to fail.
4. A recorded decision on Codex `reconcile` (item 8).
5. The operator has decided on native requalification (step 6), and on landing:
   a branch and commit split, plus the schema-15 install as one coordinated
   operation with `install.sh`.

Then continue with the plan's **Next** list. Remediation-open items do not block
resumption unless the operator says so: F-13 `run_id`, F-26 adapter renewal,
F-31, F-33 remaining checkers, F-35 native, F-36, F-37. Each must stay listed
as open in the plan until closed.

## Report back

Write `docs/CODEX-ORCHESTRATOR-VERIFICATION-REPORT.md`. For each finding ID,
record VERIFIED, REFUTED (with the failing input) or NOT CHECKED. Then add, in
separate sections:

- commands run and their results;
- mutations and whether each was killed;
- adversarial results;
- constrained review verdicts;
- native results, only if authorized;
- new findings, numbered V-01 onward, each with its exact failing input.
