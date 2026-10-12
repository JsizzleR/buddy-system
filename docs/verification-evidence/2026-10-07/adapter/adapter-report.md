# Adapter verification worker evidence

Scratch: `/private/tmp/buddy-adapter-verify-dxmg7jlz`. No native vendor binary was launched by this worker. Hermetic fake processes and isolated temporary ledgers only.

## Fail-before probes

Command: `env -u CLAUDE_CODE_SESSION_ID -u CODEX_THREAD_ID GOCACHE=<private-cache> GIT_OPTIONAL_LOCKS=0 go test -buildvcs=false -count=1 -timeout=120s ./internal/harness -run "TestReview|TestProbe" -v`.

Snapshot: `git archive refs/snapshots/orchestrator-pre-fixes-20261006` into a new directory; copy `docs/findings-probes/harness_*.go.txt` to `internal/harness/` without `.txt`. Current copy required renaming probe-only `flakyLedger` to `probeFlakyLedger` because the permanent regression defines the same helper. This was a private probe compatibility edit, not a source fix.

| Probe in execution order | Snapshot | Current |
| --- | --- | --- |
| `TestProbeOversizedFrameKillsWorkerAndOrphansChildren` | FAIL | FAIL |
| `TestProbeClaudeBusyWakeRefusalPoisonsJournal` | FAIL | FAIL |
| `TestProbeClaudeMatcherSemantics` | FAIL | PASS |
| `TestProbeClaudeCaseFoldedKeys` | FAIL | PASS |
| `TestReviewOperatorDescriptorNotInheritedByNativeChild` | FAIL | FAIL |
| `TestReviewCloseEndsNativeDescendants` | FAIL | PASS |
| `TestReviewCloseWithDescendantHoldingStdout` | FAIL | PASS |
| `TestReviewDroppedApprovalIsNotObservedAbsent` | FAIL | PASS |
| `TestReviewNativeChildCannotWriteOperatorChannel` | FAIL | FAIL |
| `TestReviewFixOperatorCloexecControl` | PASS | PASS |
| `TestReviewDefiniteWakeRefusalIsNotBurned` | FAIL | PASS |

The four current failures match the announced probe exceptions. The oversized-frame failure is specifically its obsolete expectation that the error contain `too long`; the worker and child now both die. The brief inaccurately describes that probe as asserting worker survival. The busy-wake probe now gets `not_submitted` then `delivered`; it attempts resume while its new turn is open, correctly refused. The two descriptor probes bypass the entry-point ownership fix; the built entry regression covers actual descriptor admission.

Snapshot entry command: `go test -buildvcs=false -count=1 -timeout=150s ./cmd/buddy-adapter -run TestAdapter -v` after copying current `main_test.go`. All three entry tests FAIL: bad descriptors launch native children before refusal; signal cleanup bypasses the handler; stalled-ledger stdin EOF exceeds10s. The descriptor positive subcase fails first on inherited stderr/WaitDelay, so its later descriptor-read assertion is not claimed as separately reached.

## Mutations

Initial sweep:49 valid compiling mutants,31 killed and18 survived. Added independent private tests then killed14 meaningful survivors. Four remain defensive/redundant; all four are explicitly retained in `adapter-summary.json`. Four V02 mutants, four F35 mutants, and five later shutdown/version mutants were added and killed. Final core total:62 distinct,58 killed,4 survivors; zero build-error kills.

The first V07 mutation survived because SIGTERM could beat the startup output call. `TestReceiptEmitterCancelsAnEstablishedBlockedWrite` now waits for the output writer to enter Write before cancelling. That exact mutation then FAILS deterministically. Raw initial and follow-up logs are preserved.

Drivers and manifests are retained beside this report. Each driver expects sibling private source copies as named inside it; bootstrap by copying the desired verified source tree without `.git`, caches or installed binaries. The manifests preserve every exact replacement and test selector; tests build only into private copies. Use `mutation-results.json`, `supplement-results.json`, `final-wrap-results.json`, `f35-mutations.json`, `late-mutations.json` and `late-final-results.json` in that order to audit initial attempts and final dispositions.

## Newly fixed reproductions

- V02: journal `send message_id=42 wake_delivery={state:"unknown"}` followed by `wake_delivery={state:"not_submitted",turn_id:"turn-1"}` incorrectly freed dedupe. Same with refusal `result_ref`, and prior unknown `result_ref`. A live backend response `{state:"unknown",result_ref:"result-1"}` plus `ErrNotSubmitted` was also treated as a safe retry. Permanent `verification_replay_test.go` fails before and passes after; each new predicate was independently removed and killed.
- V07: pre-fill an open stdout pipe to EAGAIN, launch adapter, let it publish startup receipt, send SIGTERM without draining. Before fix, startup Encode prevents Close and worker tree survives. `TestAdapterSignalClosesWorkerWithFullStartupOutput` fails before; the barrier-based emitter test kills the uncancellable-write mutant. One serialized writer preserves framing; caller cancellation can now reach Close.
- V08: native version script `sleep 30 & echo $! > holder.pid; printf "codex-cli 0.160.0\n"; exit 0`, or keep leader waiting until the200ms context deadline. Output formerly hangs beyond2s because the grandchild holds stdout. `TestNativeVersionProbeBoundsInheritedOutput` fails before for leader-exit and cancellation, positive version control passes. Shared probe now owns a group, reads at most4097 bytes, observes exit without reaping, sends its final group kill before the only Wait, and bounds residual stdout drain. Group/leader-only/context mutants all die.
- Expected-close race: feed disconnected event to event observer after lifetime cancellation, or cancel while Observe returns. Before, both become published unknown failures. Permanent event-pump regression fails before, passes after; live unexpected disconnection still reaches Observe+emit. Main cancels context before Close begins.
- F35: fixture adapter -> separate-group observer -> native child. Prior owned_natives returned[] because native pgid differed from adapter pgid. Two new OwnedCleanup tests fail before. Ancestry/birth checks now capture the separate group, intentional crash records/signals both groups, and fallback cleanup still adds an error. Source changes to four fault functions preserve client ownership in fleet until Close succeeds. Four mutations die; full proxy suite10/10 passes.

## Adversarial4 results

-6: inspected sole reap path and executed process group/leader exit/escaped stdout/TERM flush controls. Group signals occur before Wait, and in-group child shutdown on leader exit is intentional. This does not establish whole native tool-tree termination: the native worker separately observed an escaping tool group, which root is dispositioning.
-7: permanent legacy-journal controls preserve unknown, accepted and delivered reservations; valid refusal releases exactly its intent. Inconsistent turn/result-bearing refusals are now rejected (V02).
-9: extracted five actual runner settings dict ASTs, replacing only their dynamic gate string with an equivalent absolute fixture gate. All exact anchored `^(Edit|Write|NotebookEdit)$` and timeout10 settings passed strict admission; payloads retained in fixture-settings.json.
-10: searched all check/qualify scripts and traced every approval detail consumer. All use `native_event.approval`; no checker relies on `approvals[].detail`. Relevant consumers include check-claude-interrupted/history and qualify Claude/history/endpoints/interrupted, Codex uncertain/interrupted/recovery, and mixed-fleet.

## Final checks and provenance

`go test -buildvcs=false -count=1 -timeout=120s ./internal/harness ./cmd/buddy-adapter`:PASS (13.068s,4.985s). `python3 -I -B scripts/test_native_proxy.py`:10 tests PASS (8.634s); ps parent checks require normal host access, obtained through auto-reviewed escalation. The earlier sandbox ps error and an earlier whole-harness stale compaction expectation failure are retained, not counted as behavior failures; root fixed the latter V09 expectation.

Final file hashes are in adapter-summary.json. Other workers may subsequently change shared files; rehash before representing these as the final tree. Memory was used only for prior-context orientation (MEMORY.md305-316), not as current qualification evidence.

## F38 independent supplement

Both injection positive controls were executed in the full proxy suite. Four extra private mutants confirm the stated exception: daemon=True plus no explicit join survives for both compaction injectors; removing each stop signal is killed by the armed child-exit timeout regression. This is not a unique-proof claim for the join/non-daemon guards; the existing serialization and caught BrokenPipe preserve the tested behavior. Final aggregate for this worker is66 distinct mutants,60 killed,6 explicit survivors (4 adapter defensive/redundant and2 F38 daemon-but-caught); no build error is counted as a kill. Exact results: f38-mutations.json.
