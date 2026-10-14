# Orchestrator remediation verification

2026-10-07. Verification of [the brief](CODEX-ORCHESTRATOR-VERIFICATION.md),
against the closure requirements in [the response](CODEX-ORCHESTRATOR-RESPONSE.md).
The operator authorized the full verification scope in this session, including
retained native binaries in disposable loopback fixtures and repairs to
reproduced defects.

**Result: verification performed; not cleared for landing, installation or
newer-version admission.** Additional defects were reproduced and repaired.
Retained-version suites passed, but native Claude tool jobs demonstrably escape
the adapter's process group. The subagent/no-active-input permission branches
were not reached by the native fixture. Those gaps are not green qualifications.
Four redundant adapter mutations and two known injector daemon/join mutations
also survived; the brief's literal every-mutant-killed criterion was not met.

*Updated 2026-10-13:* the native suites were rerun on the final bytes (HEAD
`316b337`, A-07); see [the section at the end](#native-rerun-on-the-final-bytes-2026-10-13).
The tree was committed on 2026-10-12 and installed on 2026-10-13 at the operator's
request. Newer-version admission remains on hold.

The checkout remained on `main`, HEAD
`6938fbebb5b5d0d44baf7015fbffa72e2a02606e`, with substantial pre-existing
uncommitted work. No commit, branch switch, install, global hook/trust change or
schema-15 access to the real ledger was performed. Claim bookkeeping used the
installed schema-14 Buddy. Real-provider worker behavior and model-directed
coordination were not qualified.

## Evidence and scope

The fail-before snapshot was
`refs/snapshots/orchestrator-pre-fixes-20261006`, resolving to
`bb2588dd38e2639dde6fe6eaab30f6d419e3fa53`. Tests ran on Go 1.27.1,
darwin/arm64. The initial source manifest and final source hashes, commands,
mutation definitions/results, review verdicts and compact native summaries are
retained in [verification-evidence/2026-10-07](verification-evidence/2026-10-07/README.md).

Raw native transcripts and complete review logs remain at the `/private/tmp`
paths in those summaries. They are **ephemeral**. Their recorded digests and
summaries do not reconstruct missing artifacts or permit a checker to rerun
after those artifacts disappear. Native receipts name the source snapshot they
actually used; earlier suites in this session did not all run final source
bytes. A final mixed-fleet rerun and standalone adapter run exercised the later
startup/shutdown repairs. Final hermetic checks cover the final source.

The categories below are independent: source inspection; executed hermetic
checks; deliberately changed guard tests; source-only constrained reviews;
native deterministic loopback fixtures. No category substitutes for another.

## Finding-by-finding disposition

`VERIFIED` means the stated remediation was independently supported within the
scope in the last column. It does not close an explicitly partial finding.
`REFUTED` records a counterexample to the handed-off implementation, even where
this verification subsequently repaired it. `NOT CHECKED` identifies missing
independent closure evidence. The final state is stated separately.

| ID | Disposition | Evidence and final state |
| --- | --- | --- |
| F-01 | REFUTED; repaired | Patch-only trusted matcher missed native Bash routing (V-01); nested shell and static quoting missed detection (V-04). Fail-before, permanent regressions, mutations and retained Codex native deny/control now pass. Dynamic/unseen shell routing remains cooperative. *Corrected 2026-10-07 by the audit (A-01):* partial, not repaired. The final recognizer still passed static forms bash and zsh run (`bash -c --`, `eval`, a shell fed a here-document or pipe, `nice`, brace expansion, zsh precommand modifiers, `coproc`, `find -exec`), plus four jurisdiction forms (`pushd`, `~`, nested). The native F-01 runs used a buddy built before the final `patch.go`/`cli.go` edits, so the final recognizer had hermetic evidence only. Since repaired in the working tree: the default is inverted and uncertain text refuses where a ledger governs (D-073 audit continuation, `shellroute_test.go`). No native rerun yet (A-07). *Updated 2026-10-13:* rerun on the final bytes: the plain heredoc (within shell/lifecycle/edge 15/15) and `bash -lc` heredoc (7/7) are DENIED through the Bash hook, with direct-patch controls written. A-01 was reopened on 2026-10-12 by a re-review; its open forms (`env -S` jurisdiction, `script` with no command, an over-budget brace word as an option value, fish) and `exec <<'SH'` have no native runner. Still partial. |
| F-02 | VERIFIED | Built entry tests reject unopened, write-only, read-write FIFO, socket and directory descriptors before children/journal; read-only control approves exactly one request. Helper-only historical probes are not entry evidence. |
| F-03 | VERIFIED | Snapshot failure; home reservation retained with nested and linked worktrees; holder control allowed. Mutation replacing home enforcement dies. |
| F-04 | VERIFIED, with repaired regression | Mixed managed/unmanaged patch, pathless unmanaged call, conflicting identity and unreadable/corrupt target controls pass. A new over-budget shell analysis regression was reproduced and repaired under V-04. |
| F-05 | REFUTED; repaired | Multi-link dangling target and early resolution-error counterexamples (V-03). Distinct-spelling regressions kill each placement guard. Native entry deletion, dangling creation and move controls pass on Codex. |
| F-06 | REFUTED; repaired | Physical `..` inside symlink content was lost (V-03). Component traversal repaired. Native Codex and Claude Write lexical controls measured; conservative checking of both spellings retained. |
| F-07 | REFUTED for complete cleanup; partial | Group ownership, signal, stdout-holder and leader-exit hermetic cases pass, including new startup fixes. Native Claude regrouped tool survives Close (V-12). Native subagent/no-active-input deny branch NOT CHECKED: attempt did not arm it. *Corrected 2026-10-07 by the audit (A-02):* the closure requirement "refuse or reconcile uncertain prior native ownership before launching a second controller" was not met. After an adapter SIGKILL, `--resume` launched a second app-server while the first lived. Since repaired in the working tree for the shared core: a durable pre-spawn ownership record, and a resume that refuses while the recorded adapter, leader or group lives (D-077 A-02 continuation). Descendants that leave the group, `--end-prior`, unpinned killing and `Setsid` remain open; no native rerun yet. *Updated 2026-10-13:* the operator chose the narrow contract (D-077, 2026-10-11): descendants that leave the group are recorded and reported, never signalled, and `--end-prior` and unpinned killing are out. Built 2026-10-12; observed natively on the final bytes (V-12 row). `Setsid`, Q1-Q7 and the deny branches remain open. |
| F-08 | VERIFIED by withdrawal | Oversized frames fail visibly as uncertainty. Codex native reconciliation stays unsupported; four dependent runners/checkers revised and rerun. Paged native history is not implemented or qualified. *Corrected 2026-10-07 by the audit (A-09):* verified for the withdrawal of `reconcile` only, which removes the history-read trigger. Any live frame over the 4 MiB transport bound still closes the transport and ends the worker's tree, by design (`process.go`); the response's "preservation of the live worker" is not met for that case, and the Claude image/PDF trigger is unqualified. |
| F-09 | VERIFIED within measured scope | Exact-key/duplicate/matcher/async/timeout admission tests and mutations pass. Native matcher semantics checked on 2.1.291 only. Escaped duplicate test typo corrected to actual `comm\u0061nd`. |
| F-10 | REFUTED; repaired | Contradictory `not_submitted` rows/live results could discard native evidence (V-02). New journal compatibility and contradiction tests pass; ordinary pre-write refusal remains retryable. |
| F-11 | REFUTED; repaired | A missed resolution with known approval still reported pending as observed (V-09). Retained historical list now has unknown state and stale/incomplete wording. |
| F-12 | VERIFIED after V-06/V-10 repairs | Native adapter-run Claude creates endpoint socket; original resolved argv basename prevented registration (V-06). Exact endpoint birth captured after fix. Adapter mail never gains a legacy wake, including old mail after legacy succession. |
| F-13 | VERIFIED partial; open | `same_run`/`same_contract` guards and splicing negatives exercised. No runner-generated run ID binds every case; whole-case substitution from a same-build run remains open. *Corrected 2026-10-07 by the audit (A-06):* the `same_run` and `same_contract` kills came only from `check-claude-interrupted-compaction.py --self-test` run against the ephemeral 93 MB receipt; `check.sh hermetic` never runs that self-test (it runs `check-mixed-fleet.py`, which never calls `suite_binding`), so nothing in the tree exercises those guards once the receipt is gone. The receipt is restorable from the A-08 archive. *Updated 2026-10-12:* the suite binding now runs in `check.sh hermetic` against synthetic suites, and 19 binding mutants are killed by the tree's own tests. Still open: no runner-generated run id binds every case, so a whole case directory from another run of the same build, copied into this run's root, still passes. |
| F-14 | VERIFIED for named checker | Five named evidence predicates independently mutated; each self-test fails. Native interrupted-compaction receipt passes; its checker rejects 222 damaged-evidence cases. Other checkers do not inherit this coverage. *Corrected 2026-10-07 by the audit (A-06):* the five predicate kills depend on the same ephemeral receipt as F-13's, and are not reproduced by the hermetic tier; of the ten script-guard kills only `history_exactly_one`, `still_owned` and the assert gate are killed hermetically. |
| F-15 | VERIFIED, source | D-074, shared rules, charter and skill state the explicit opt-in adapter launch exception. |
| F-16 | VERIFIED, source | Unconditional foreground-only Codex review rule restored. Reviews here used the prescribed script; no saved memory was edited. |
| F-17 | VERIFIED, source | AGENTS role and current-session operator authority wording is explicit. This session's authorization came from the user. |
| F-18 | VERIFIED, source/native boundary | D-075 records recovery widening and reconciliation withdrawal; current decision continuation retains withdrawal. Historical receipts remain history. *Corrected 2026-10-07 by the audit (A-11):* partial as verified. D-070's "Claude resume is unsupported" and D-071's 2026-10-06 continuation (compaction on 2.1.290 only) carried no in-place marker, and D-071's continuations had no anchors of their own. Markers and `###` anchors were added to `decisions.md` on 2026-10-07. |
| F-19 | VERIFIED as documentation only | Ephemeral evidence is explicitly labeled. Compact durable manifests/results added here; complete raw evidence was not archived. |
| F-20 | VERIFIED | Design pass and separate code scopes ran in the foreground. Confirmed defects received failing regressions and follow-up reviews. Verdicts are listed below, separately from tests. |
| F-21 | VERIFIED within measured scope | Ten-second floor exceeds wrapper budgets; native 2.1.291 timeout measured at 10.052 s then entered native permission handling. Other versions and 600 s upper limit NOT CHECKED natively. |
| F-22 | VERIFIED | Bounded fenced summary plus digest; exact native detail remains in event. Tests/mutations pass; checker source reads approval request detail from native events. |
| F-23 | VERIFIED after V-06/V-08 repairs | Absolute resolved identity and changed-file refusal tests pass. Canonical Claude argv label preserves endpoint ancestry; version probe now bounds inherited output holders. Same-user replacement race remains documented. |
| F-24 | REFUTED; repaired | Delivered adapter mail lost recipient evidence (V-05); old incarnation evidence suppressed a legitimate legacy successor (V-10). Current-incarnation evidence and both sent paths repaired. |
| F-25 | VERIFIED | Adapter correction refuses both real send and dry run; snapshot failure and guard mutation reproduced. |
| F-26 | VERIFIED partial; open | Observed lifecycle paths renew only covering claims; both under- and over-renewal mutations die. Adapter-launched workers still lack a PostToolUse renewal route. *Updated 2026-10-13:* route B is decided: the controller renews from observed native completion events (Claude's matched, explicitly successful Edit/Write/NotebookEdit results; Codex's completed `fileChange` items) and stays the only lifecycle owner, with the gate contract unchanged. Implementation in progress; open until it lands with its own native check. |
| F-27 | VERIFIED | Non-hook stdin refuses with `</dev/null` recipe; no manual fallback. Snapshot failure and wording mutation reproduced. *Corrected 2026-10-07 by the audit (A-10):* the response also asked to "cover an open pipe as well as malformed bytes", and the permanent test covered malformed bytes and EOF only; `decodeHook` read stdin to EOF with no bound, so `hello --session` on an inherited open pipe blocked until the writer closed (3.0 s measured). Landed 2026-10-07: hook stdin must arrive whole within 2 s (`hookInputWait`), after which the verb refuses — never the manual path — and `hello --session` names the recipe; a hook payload, written and closed at spawn, is never charged the bound. `TestHelloSessionOpenPipeIsRefusedWithinTheBound` failed on the tree before the fix (blocked past 15 s) and kills both the unbounded read and a fallback to `errNoHookInput`. |
| F-28 | VERIFIED | Target ledger's own pause/label applies across repo boundary; snapshot failure and pause-read mutation reproduced. |
| F-29 | VERIFIED locally | Split endpoint/gate budget regression and mutation pass. Native SQLite writer-lock case passed 31/31: advisory step yielded around 250 ms and actual held/unheld gate controls worked. No production load claim. |
| F-30 | VERIFIED after V-07 repair | Ten-second Observe ledger bound and cancellation-before-lock tests pass. Full startup stdout no longer prevents signal cleanup. |
| F-31 | NOT CHECKED; open | Missing Codex harness-marker diagnostic remains unimplemented; not silently closed by V-01 matcher coverage. *Fixed 2026-10-12 (hermetic):* `buddy diagnose --codex-hooks <hooks.json>` lists every buddy command in a hand-wired Codex layer with its `BUDDY_HARNESS` marker and says which lack it; 11 mutants killed. It reads a file, not the configuration Codex loaded, its trust or enforcement. |
| F-32 | VERIFIED | Hermetic tier registers Python compile/assert rejection, proxy/provider/checker tests and Node syntax checks. Assert-gate mutation killed. Final tier result below. |
| F-33 | VERIFIED partial; open | Claude-resume and mixed synthetic self-tests run; receipt-dependent self-tests also rerun. Synthetic fixtures for remaining checkers are still incomplete. *Fixed 2026-10-12 (hermetic):* all 12 checkers self-test on synthetic evidence, and `check.sh` runs them all. Synthetic evidence tests a checker; it qualifies no harness. |
| F-34 | VERIFIED, source/commands | Qualification Git reads disable optional locks and build environments carry `GIT_OPTIONAL_LOCKS=0`. This does not prove every possible future command is read-only. |
| F-35 | REFUTED original fixture assumption; repaired partial | Native child no longer shares adapter pgid. Capture verified ancestry and both groups before fault, then signal recorded ownership. Two real-group tests and four mutations pass. Sixteen suites reran, but Mods/peers close helpers still lack explicit forced-signal receipts; their passing exit codes do not close that gap. New job-probe fallback reporting repaired as V-13; regrouped-job qualification remains red. *Updated 2026-10-12:* the TERM/HUP test has its no-handler control; the Mods and peers runners record a forced signal, and the delivery checker refuses one. The runner side is unproven until the native rerun (A-07). Under the narrow contract (D-077, 2026-10-11) the regrouped job is reported, not ended. *Updated 2026-10-13:* the runner side ran natively: Mods 38/38 and peers 50/50 on the final bytes, and `check-claude-delivery.py` passes the peers receipt (no forced signal) and rejects 60 damaged cases. The Mods suite has no independent checker yet. Broader cleanup stays open. |
| F-36 | NOT CHECKED; open | No independent recount is possible without provider sequence/stage markers. Not implemented in this verification. *Partial 2026-10-12:* the provider keeps its own timeline, and `check-interrupted` recounts the replay window and the held request from it. Still open: interrupted-compaction's barrier and released flags, the request counts in compaction, interrupted-compaction, uncertain and recovery, the Claude checkers, and recovery's job liveness. The runner side is unproven until A-07. *Updated 2026-10-13:* the runner side ran natively; `check-interrupted` passes 59/59 on a fresh receipt and rejects 54 damaged cases. The other items stay open. |
| F-37 | VERIFIED partial; open | Shared contract/splice checks and updated reconcile boundaries exercised. Complete receipt/evidence/native-executable binding across all checkers remains open. *Fixed 2026-10-12 (hermetic):* `check-interrupted`, `check-recovery` and `check-uncertain` bind their original files, the build and the native executables; the runners write `native-versions.json` and `evidence_sha256`. Receipts written before this fail the new checks. The runner side is unproven until A-07. *Updated 2026-10-13:* on fresh receipts from the final bytes, `check-interrupted` 59/59, `check-recovery` 29/29 and `check-uncertain` 42/42 pass, and every manifest-bearing receipt names the one source manifest. The other checkers' complete binding stays open. |
| F-38 | VERIFIED within test scope | Non-daemon injectors, explicit stop/join, concurrent input relay and child-stops-reading controls exercised. Equivalent join/daemon mutants and their limits are retained in adapter evidence; not every join is independently observable. |
| F-39 | VERIFIED | Fixtures isolate global/system Git config, use bounded commands and `require`; AST assert gate is active and mutated. |
| F-40 | VERIFIED | Exactly-one history rewrite is required; all five proxy modes exercised. Guard removal killed its regression; an initial import-layout error was excluded. |
| F-41 | VERIFIED, source | GIVENs 69–72 and operation list match scoped integration; source charter check passes. *Corrected 2026-10-07 by the audit (A-11):* partial as verified. GIVEN 71's operation list was corrected (startup is argv; requests are inspect, reconcile, send, interrupt; approval is the operator path), but D-070's own list ("start… inspect and interrupt") was not; an in-place correction was added to D-070 on 2026-10-07. |
| F-42 | VERIFIED, source | Shared rules map harness/adapter code, captured-token fence and D-068–D-077. Dated continuations preserve prior context. |
| F-43 | VERIFIED, source/check | Skill is shortened and points to harness documentation; lifecycle/adapter exemptions have reasons. Registered skill check passes. |
| F-44 | VERIFIED, source/check | USAGE includes TOC, lifecycle harness flag and non-authorizing example. Current verification corrects shell coverage and cleanup/approval limits. |
| F-45 | VERIFIED | Source-derived adapter flag documentation test and its positive control pass. |
| F-46 | VERIFIED, source | Plan has evidence categories, status, Next list and historical appendix; updated here with this report and remaining blockers. |

## Commands run and results

Commands below were run from the checkout or an explicitly named disposable
copy. Full argv and result files are in the durable evidence summaries.

```sh
env GIT_OPTIONAL_LOCKS=0 go build ./...
env GIT_OPTIONAL_LOCKS=0 go vet ./...
env GIT_OPTIONAL_LOCKS=0 sh scripts/check.sh hermetic
```

Build and vet succeeded; the final tier reported **HERMETIC GREEN**, including
race tests and registered script self-tests. The live leg was not run. Both
initial and final hermetic logs are retained, with commands in `checks.json`.
Restricted-sandbox socket/cache setup failures were rerun with scoped access
and are not counted as product failures or mutation kills.

Fail-before used `git archive` of the saved snapshot into temporary directories,
then copied only the named regressions/probes and ran `go test -buildvcs=false
-count=1`. The eleven CLI regressions all failed for their intended assertions
and passed on the repaired tree. Of eleven old harness probes, ten failed on
the snapshot; the close-on-exec positive control passed. All three entry-level
regressions failed on the snapshot for the intended defects. No compile error
counts as a reproduction.

Four historical probes still fail on current source for the reasons anticipated
by the brief: two bypass entry-point descriptor admission, one requires an
obsolete `too long` error string, and one attempts resume with a new turn still
open. The oversized-frame worker and child do die; the brief inaccurately calls
that probe a worker-survival expectation. Permanent entry, uncertainty and completed-turn regressions are the
authoritative current tests. A helper-name collision in copied probes was
renamed only in the private test copy before execution.

New V findings were separately watched to fail, repaired, and tested again.
Relevant permanent tests include `verification_gatetargets_test.go`,
`verification_replay_test.go`, `verification_executable_test.go`, and additions
to adapter main, observation, process, Codex admission and adapter-mail tests.

## Mutations

Private copies were mutated one guard at a time; controls had to pass. A build,
import, sandbox or setup failure is not a kill. Initial survivors, supplemental
tests and final outcomes are retained rather than replaced by an all-green
summary.

| Group | Final outcome | Interpretation |
| --- | --- | --- |
| Gate/mail and V-03/V-04 | 37/37 killed | Includes 23 original guards, seven first repairs, six review follow-ups and one budget/jurisdiction regression. Four placement guards initially survived because destinations coincided; a permanent four-distinct-placement fixture killed each. |
| Adapter/controller/process and fault ownership | 58/62 killed; four redundant survivors | Four survivors are listed below. Initial missing tests were supplemented; V-07 gained a deterministic writer-entered barrier after a timing-dependent test let a mutant survive. *Corrected 2026-10-07 by the audit (A-04):* replayed against the tree's own tests, 44/62 — fourteen of the kills (`fd-lower-bound`, `group-term`, `group-kill`, `sigpipe-ignore`, six Claude `ErrNotSubmitted` wraps, three Codex ones, `endpoint-shared-budget`) rested on nine `TestVerification*` tests kept only as `.txt` files in the evidence directory. One mutant (`controller-not-submitted`) no longer applied to the final code; its rewrite is killed. The audit landed tests that kill the fourteen (its "Landed here" table), so 58/62 is true of the tree since 2026-10-07, with the four redundant survivors unchanged. |
| Root V-01/V-05/V-06/V-09/V-10 | 10/10 killed | Admission and offline projection, delivered/current-incarnation evidence, canonical argv label, stale approval state, endpoint incarnation and both sent-wake guards. |
| Script checker guards | 10/10 killed | Same-run/contract, five named evidence predicates, assert gate, exactly-one history injector, owned-process match. *Corrected 2026-10-07 by the audit (A-06):* seven of the ten (the five predicates, `same_run`, `same_contract`) are killed only by `check-claude-interrupted-compaction.py --self-test` against an ephemeral 93 MB receipt that the hermetic tier never runs against; after a reboot nothing exercises them unless the A-08 archive is restored. The other three (`history_exactly_one`, `still_owned`, the assert gate) are killed in the hermetic tier. "Owned-process match" is the same mutant as `f35-birth-comparison`. The ten mutants' definitions existed only under `/private/tmp` and are now in the archive. |
| F-38 injector supplement | 2/4 killed; two equivalent survivors | Removing each stop signal dies. Daemon plus omitted join survives for both injectors under existing serialization and caught broken-pipe behavior; neither is claimed killed. |
| V-13 qualification reporting | 1/1 killed | Forcing the no-fallback predicate true fails the mocked exit-zero/job-absent regression; unforced positive and close-exception controls pass. |
| Reconcile decision boundary | 14 negative controls rejected | Real unsupported receipt plus synthetic no-turn control; kept separate from source-guard mutation totals. |

The adapter survivors remove redundant failure checks after earlier checks have
already proved the property: `F_GETFL` failure after successful `F_GETFD`,
`Fstat` failure after the fcntls, `os.NewFile == nil` for a valid fd at least 3,
and timeout numeric-type checking when the independent numeric floor rejects
the zero fallback. They are not untested reachable acceptance paths, but they
are survivors and prevent an unqualified every-guard-killed claim. Additional
F-38 join/daemon experiments are recorded separately in the adapter summary.

## Adversarial results

- Shell routes: direct command, `bash -lc`, nested known shells, `sudo -u x`,
  `env -i`, command substitution, static split/escaped/ANSI-C words, and literal
  `cd`/`env -C` placement were checked. Ordinary `git commit -m "apply_patch
  fix"`, `make apply_patch`, `tee apply_patch`, quoted regexes, literal heredocs
  and `command -pv/-pV` lookup pass. This is bounded recognition, not general
  shell execution analysis.
- Jurisdiction: managed home and discovered target ledgers refuse uncertain
  routes; ordinary byte/depth exhaustion outside every ledger passes. An unseen
  placement beyond parser bounds remains outside established coverage. A home
  `.claude` claim covers a nested linked worktree; the holder control passes.
  This preserves the explicit additive home reservation policy.
- Path resolution: four genuinely distinct placements prevent conflating
  lexical/physical entry and followed target. Dangling chains and physical
  parent traversal are tested with a kernel write control. Errors in one
  candidate no longer suppress another candidate's governing ledger.
- Recipient evidence: a logical session without mail/endpoint has no process
  address, so it receives no wake offer. Delivered current-incarnation adapter
  mail remains evidence; earlier-incarnation mail/endpoints do not classify the
  successor; an old adapter message never becomes a legacy-wake candidate.
- Journal compatibility: pre-change unknown/accepted/delivered records remain
  valid. A definite refusal with a turn or result is contradictory and cannot
  release the reservation. Missing approval-resolution observations preserve
  uncertainty even when the previous approval list was nonempty.
- Settings: the actual fixture matcher `^(Edit|Write|NotebookEdit)$` with
  timeout 10 is admitted. Duplicate decoded keys, including escaped spelling,
  case variants, asynchronous hooks and out-of-range values are rejected.
- Process ownership: leader remains unreaped through group signaling; stdout
  holders, signal cleanup, startup output blockage and version-probe inherited
  pipes are tested. Fixture fault injection captures native ancestry before
  fault and records exact groups. This does not imply control of regrouped jobs.

Latest 40-sample local latency measurements were Edit median **13.91 ms**,
p95 14.82, max 15.83; 128-path patch median **22.09 ms**, p95 34.31, max 58.21;
four-placement stress median **32.43 ms**, p95 42.75, max 68.89. An earlier
concurrent-load 128-path series included **100.24 ms** (1/40 above the stated
100 ms hook target). Final samples do not erase that miss or establish a hard
latency guarantee. Gate timings are distinct from the adapter wrapper's
250 ms endpoint and 3 s gate budgets.

## Constrained source reviews

Every pass used foreground `sh scripts/codex-review.sh <prompt> <output>` with
the repository charter, model `gpt-6-astra`, effort `xhigh`, tier `default`.
Review prompts and final verdict text are retained. These reviewers did not
build, test or launch native fixtures.

| Pass | Verdict and disposition |
| --- | --- |
| Design | No PASS: dangling chain, quoted shell false denial and delivered-recipient evidence findings; reproduced and repaired as V-03/V-04/V-05. |
| Process/descriptor code | V-07 full-output startup and V-08 inherited version output hangs. Regressions reproduced; process follow-up PASS, including normal-close event race V-11. |
| Controller code | V-09 pending approval overstated after a missed resolution. Reproduced and repaired. |
| Claude settings code | No confirmed P1/P2. Escaped-key fixture typo corrected; actual escaped duplicate passes rejection test. |
| Gate code | Four further cases in V-03/V-04: error short-circuit, ANSI-C literal, nested cwd and command option cluster. Repaired and mutants killed. Follow-up found budget/feature-off regression, also reproduced and repaired. Final focused follow-up PASS. |
| Mail code | V-10 earlier-incarnation evidence. Reproduced and repaired. |
| Approval/mail presentation follow-up | PASS for V-05/V-09/V-10, including both `sent` paths. |
| Native job/fault runner | Found V-13: forced adapter cleanup could be reported as PASS. Reproduced with forced/unforced/exception controls and repaired; focused follow-up PASS. Native descendant-cleanup qualification remains red. |

## Native results

All fixtures used retained **Claude 2.1.291**, **Codex 0.160.0**, and, for
history, retained **Claude Agent SDK 0.3.202**. Exact executable and artifact
hashes and full commands are in `native-summary.json`. Newer PATH versions
were not admitted. Loopback providers emit scripted responses; these counts
measure harness/adapter behavior, not autonomous planning or real providers.

*[2026-10-13: these 2026-10-07 counts are from source that predates the final
bytes (A-07). The rerun on the final bytes is in
[its own section](#native-rerun-on-the-final-bytes-2026-10-13).]*

| Suite | Checks passed | Checker damaged-evidence cases rejected |
| --- | ---: | ---: |
| Claude Mods | 33/33 | — |
| Claude peers | 50/50 | 54 |
| Claude resume | 22/22 | 21 |
| Claude interrupted | 98/98 | 86 |
| Claude compaction | 26/26 | 30 |
| Claude endpoints | 38/38 | 32 |
| Codex recovery | 26/26 | 26 |
| Codex interrupted | 52/52 | 46 |
| Codex uncertain | 40/40 | 38 |
| Codex compaction | 23/23 | 34 |
| Codex interrupted compaction | 75/75 | 90 |
| Claude SDK history | 130/130 | 175 |
| Mixed fleet, final rerun | 37/37 | 41 |
| Claude interrupted compaction | 113/113 | 222 |
| Codex shell/lifecycle/edge | 15/15 | — |
| Standalone Codex adapter | 9/9 | — |

Additional native cases: Codex paths **10/10**, Claude Write paths **6/6**,
nested shell repaired denial **6/6**, loaded endpoint/gate **31/31**.
Claude has no dedicated advertised Delete/Move tool in this interface;
`Bash rm/mv` is outside the documented gate side-effect contract.

The original mixed run failed after successful turns when expected shutdown
queued a disconnect against a closed controller. V-11 repaired this; the
independent final rerun passed. Earlier failed receipts remain recorded.
Endpoint qualification now distinguishes a leftover socket pathname from a
live, exact-birth eligible receiver; it does not unlink the socket to fake
cleanup. Matcher/timeout checks measured 2.1.291 only; timeout expiry led to a
native permission prompt and an explicit denial kept the file absent.

**Separate failed qualification:** `qualify-claude-jobs.py` returned **2/3**
initially, then **3/4** after V-13 added an explicit no-fallback predicate.
A captured native Bash tool job had a group distinct from adapter and native
leader; Close returned zero while the job remained alive. The fixture then
cleaned only its identity-matching recorded group. Fallback cleanup is evidence
of fixture hygiene, not adapter success. The final receipt recorded no forced
adapter cleanup and the exact captured processes were absent after fixture
cleanup. The native unsupported-permission
attempt did not arm the subagent callback; no-active-input was not reached.
Neither branch receives a native PASS.

## Additional findings and exact failing inputs

All repaired cases below have a fail-before record and current regression.
Their full fixture definitions are retained with the source. Temporary path
names in examples stand for fresh fixture directories.

| ID | Failing input and observation | Final state |
| --- | --- | --- |
| V-01 | Trusted Codex matcher `^apply_patch$`; `Bash` command `apply_patch <<'EOF'\n*** Begin Patch\n*** Update File: guarded.txt\n@@\n-original\n+conflicting-write\n*** Add File: collateral.txt\n+must-not-exist\n*** End Patch\nEOF` against peer-held `guarded.txt`. Admission succeeded and native shell modified it and created the collateral file. | Require coverage of both `Bash` and `apply_patch`, including diagnostic projection. Persistent native denial/control case passes. |
| V-02 | Journal send intent `{state:"unknown"}`, then `{state:"not_submitted",turn_id:"turn-1"}` or `result_ref:"result-1"`; also native evidence on prior intent or a live ErrNotSubmitted result. Reservation could be freed despite contradiction. | Prior and refusal rows must lack both fields; contradictory live result remains reserved/uncertain. |
| V-03 | `a -> b`, `b -> held/new.go` with missing leaf; or link contents `alias/../new.go` where `alias` points into a different physical parent. Also `/plain/free/link/../entry` with lexical self-cycle but physical entry pointing into governed `docs/new.go`. | Bounded component resolver, physical traversal and aggregated errors; kernel and distinct-spelling controls pass. |
| V-04 | `bash -lc 'apply_patch <<EOF\n*** Begin Patch\n*** Delete File: held.go\n*** End Patch\nEOF'` bypassed; `rg '(apply_patch\|applypatch)' file` falsely denied. Follow-ups: `$'apply_\x70atch'`, nested `cd /governed`, `command -pv apply_patch`, and unmanaged `printf %s ` plus 131073 `x` bytes or eight nested `sh -c` around `true`. | Bounded static AST recognition plus jurisdiction extraction; ordinary arguments/lookups and unmanaged budgets preserve feature-off. All regression guards killed. |
| V-05 | Deliver the only adapter message, retain no endpoint, then send a plain message. Current recipient reverted to legacy wording after adapter backlog reached zero. | Current-incarnation historical adapter mail preserves positive evidence. |
| V-06 | Start resolved native path ending `/versions/2.1.291`; hook ancestry looked for `claude` in argv and failed endpoint registration, although exact native file ran. | Preserve resolved executable path and set only Claude argv[0] to `claude`; native endpoint captures exact process birth. |
| V-07 | Fill adapter stdout pipe before its first receipt; start owned worker and send SIGTERM while the initial Encode blocks. Cleanup never ran. | Serialized output with cancelable acknowledgment; deterministic writer-entered and built-entry controls pass. |
| V-08 | Version executable script starts `sleep 30 &`, prints `codex-cli 0.160.0`, exits; background child keeps inherited stdout open. `CommandContext(...).Output()` exceeded its deadline. | Bounded owned-group probe, unreaped leader through cleanup, bounded drain/output; exit/cancel/normal controls pass. |
| V-09 | Record approval `"pending"`, then deliver `approval_resolved` while ledger Inspect fails. Pending list still had `state:"observed"`. | Historical approval remains listed but pending state is unknown, with stale/incomplete detail. |
| V-10 | End adapter incarnation after queuing mail or registering endpoint; register same native session as a legacy successor with live socket. Earlier evidence suppressed its wake; merely removing that classifier then offered legacy wake for old adapter `sent` rows. | Current-incarnation evidence only; both `sent` list/row paths explicitly exclude adapter messages. |
| V-11 | Complete native Codex work, close control stdin normally; deferred controller Close produces disconnected event before event-loop cancellation. Mixed checker reports worker error despite completed work. | Cancel event observation before controller Close; ordinary unexpected disconnect still reports uncertainty. Final mixed fixture passes. |
| V-12 | Native Claude Bash starts a long-running local Python job; captured job pgid differs from both adapter and native pgid. Close exits zero and job remains alive. | **Unresolved native cleanup qualification.** Confirms documented regrouping limit; durable red probe and exact-identity cleanup retained. No general descendant supervisor added. *Updated 2026-10-13:* under the narrow contract (D-077) the escaped job is recorded and reported, not ended. On the final bytes the contract held natively twice (recorded in `.proc`, reported on stderr, not signalled, adapter exit 0), and the probe stays RED 3/4 only on `native_tool_job_exited_on_close`, a criterion that predates the contract. Its criteria are being updated. |
| V-13 | New job probe sees EOF timeout, fallback `SIGTERM`, adapter exit 0 and job absent. Ignoring `client.forced` produced a passing receipt; a throwing close could also prevent its receipt. | Record forced cleanup on success and exception paths; final predicate requires none. Mocked failure/positive/exception controls pass, guard mutant dies. Native rerun remains red for the actual regrouped job. |

## Remaining work and plan decision

Codex reconciliation remains withdrawn. Four affected suites now require
explicit unsupported behavior for a known native turn, no adapter history
query, preserved claims/mail/READY and replay guards; no-turn uncertainty can
still be projected from the transport journal. D-077 records this decision.

Do not advance version admission or installation on these results. Resolve or
explicitly accept the measured group-cleanup limitation with an appropriate
runtime contract, and qualify the unsupported native permission branches.
The existing partial/open backlog remains: F-13 run ID; F-26 adapter renewal;
F-31 marker diagnostic; F-33 synthetic checker coverage; F-35 broader cleanup
qualification; F-36 provider stage/sequence evidence; F-37 complete binding.
Retained 2.1.289/2.1.290 native settings semantics and the upper timeout bound
were not refreshed here.

Landing still needs a concrete branch/commit split and coordinated schema-15
installation plan before any real-ledger access. This verification did not
choose or execute that irreversible operation. The [plan](CODEX-ORCHESTRATOR-PLAN.md#next)
now points here for the next bounded work.

*[2026-10-13: the tree landed on `main` on 2026-10-12, and at the operator's
explicit request the schema-15 installation was performed on 2026-10-13 (the plan's
"Production installation" row). This verification still did not choose it.]*

## Native rerun on the final bytes, 2026-10-13

Audit item A-07: the 2026-10-07 suites ran source that predated the final bytes.
This rerun was authorized by the operator on 2026-10-13. Compact evidence is in
[verification-evidence/2026-10-13](verification-evidence/2026-10-13/README.md); the
raw evidence is archived at
`~/Archives/buddy-system/2026-10-13-a07-rerun/evidence.tar.zst` (sha256
`5cb549c7c6bbc99687cb54cd03b13de00317e9a01962246dc9e0b1d4cc1a96fa`, re-checked
against the `.sha256` beside it). The `/private/tmp` paths in its summary are
ephemeral.

**Bytes.** HEAD `316b337`, clean before and after every step; go1.27.1; buddy
`d1cfb880…f290`, buddy-adapter `e6ee0108…41be` (full digests in the summary), the
same digests from the runners that build their own; one source manifest
`ee1a4482…` on all 20 manifest-bearing receipts. Retained inputs, hashes verified:
Claude 2.1.291 (`9a1d2ed6…6690`, restored from the A-08 archive), Codex 0.160.0
(`112fae7a…4b4b`), Claude Agent SDK 0.3.202. Disposable roots, ledgers and
configuration, deterministic loopback providers; no real provider, installation,
settings or trust change, repo edit or commit within the run.

| Suite | Checks | Independent checker | Rejected | 2026-10-07 |
| --- | ---: | --- | ---: | --- |
| Claude Mods | 38/38 | pending | — | 33/33 |
| Claude peers | 50/50 | 30/30 | 60 | 50/50, 54 |
| Claude resume | 22/22 | 22/22 | 21 | 22/22, 21 |
| Claude interrupted | 98/98 | 98/98 | 86 | 98/98, 86 |
| Claude compaction | 26/26 | 26/26 | 30 | 26/26, 30 |
| Claude endpoints | 38/38 | 19/19 | 32 | 38/38, 32 |
| Codex recovery | 29/29 | 29/29 | 31 | 26/26, 26 |
| Codex interrupted | 59/59 | 59/59 | 54 | 52/52, 46 |
| Codex uncertain | 42/42 | 42/42 | 44 | 40/40, 38 |
| Codex compaction | 23/23 | 23/23 | 34 | 23/23, 34 |
| Codex interrupted compaction | 75/75 | 75/75 | 90 | 75/75, 90 |
| Claude SDK history | 130/130 | 130/130 | 175 | 130/130, 175 |
| Mixed fleet | 37/37 | 37/37 | 41 | 37/37, 41 |
| Claude interrupted compaction | 113/113 | 113/113 | 222 | 113/113, 222 |
| Codex shell/lifecycle/edge | 15/15 | pending | — | 15/15 |
| Standalone Codex adapter | 9/9 | pending | — | 9/9 |

- All 16 passed on the first attempt. The 13 independent checkers pass both their
  validation and their receipt self-test, so the 2026-10-12 tightening (F-13/F-33
  suite binding, F-35 forced-signal record, F-36 timeline, F-37 binding) now holds on
  receipts the current runners wrote, not only on synthetic evidence. The higher
  counts are those added checks.
- **Pending:** claude-mods, codex-shell-lifecycle-edge and standalone-adapters have
  no independent checker. Their counts are the runner's own; the checkers are being
  written separately, and nothing here claims them.
- **V-12, `qualify-claude-jobs.py`: RED 3/4 on both attempts**, only on
  `native_tool_job_exited_on_close`, which predates the narrow contract. The contract
  held: Claude 2.1.291 ran the Bash tool shell (4515) as its own group leader under
  the native `claude`, with the python job (4516) as its child; both left the
  worker's group, were captured by the close-time scan, recorded in
  `<receipt>.proc`, reported on one adapter stderr line, and not signalled. The
  adapter exited 0 with no fallback kill; the fixture then ended only the
  identity-matching group. The suite checks neither the record nor the report; its
  criteria are being updated separately.
- **A-01 native cases:** the plain `apply_patch <<'EOF'` through Codex's
  `exec_command` (inside the 15/15) and `bash -lc '<heredoc>'` (7/7; 6/6 on
  2026-10-07 from a stale fork) were DENIED through the Bash hook, held file
  unchanged and collateral absent, with direct-patch controls written. Codex paths
  10/10 (delete-link, both `..` spellings, dangling link, move; each held and
  control) and Claude Write paths 6/6. The forms the 2026-10-12 re-review left open
  and the decided-open `exec <<'SH'` have no native runner: **A-01 stays REOPENED.**
- **Q2, Q4, Q5: not observable** with the current runners: no runner double-forks
  (Q2); all 39 adapter close reports have trigger `close`, none a leader self-exit
  (Q4); every fault seam kills the adapter and its native groups together, so stdin
  EOF is never isolated (Q5). Observation only. `Setsid` is not built; Q1 and Q3 were
  not run. All Codex adapter closes reported 0 escaped descendants, which is not Q3.
- A first pass of the 17 extra cases failed in 0 s on a driver bug (relative paths)
  and is kept in the archive, not counted.

What this changes: A-07 is done for the suites that exist. It does not admit a newer
version, qualify a real provider, close A-01, or qualify `Setsid` or Q1-Q7. Newer
PATH versions are now Claude 2.1.296 and Codex 0.162.1, and the adapter refuses both.
