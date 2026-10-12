# Gate/mail worker verification

Evidence root: /private/tmp/buddy-gate-mail-verification-20261007

## Fail-before

All 11 requested snapshot regressions failed their intended assertions; all 11 passed current. Initial GOCACHE/socket sandbox failures were setup errors and excluded.

## Mutations

| Mutation | Result |
| --- | --- |
| gate-shellPatchCommand | KILLED |
| gate-governed-shell-deny | KILLED |
| gate-ungoverned-shellPatchDirs | KILLED |
| gate-home-denyIfHeld | KILLED |
| gate-target-add-1 | KILLED |
| gate-target-add-2 | KILLED |
| gate-target-add-3 | KILLED |
| gate-target-add-4 | KILLED |
| gate-target-add-5 | KILLED |
| gate-danglingTarget | KILLED |
| gate-governedAny | KILLED |
| gate-idErr-early-refusal | KILLED |
| gate-pause-read | KILLED |
| mail-wakeAddr-early-return | KILLED |
| mail-endpoint-count | KILLED |
| mail-who-line | KILLED |
| mail-sendNote-why | KILLED |
| mail-backlog-note | KILLED |
| mail-correctable | KILLED |
| mail-lifecycle-extraction | KILLED |
| mail-BeatLogical-under-renew | KILLED |
| mail-BeatLogical-over-renew | KILLED |
| mail-hello-wrap | KILLED |

Four individual target-add removals initially survived. A permanent four-distinct-target fixture killed each independently. No build-error kills counted.

## Repair mutations

| Mutation | Result |
| --- | --- |
| V03-resolver-omitted | KILLED |
| V03-readlink-cleaned | KILLED |
| V03-resolution-error-ignored | KILLED |
| V03-budget-allowed | KILLED |
| V04-shell-recursion-omitted | KILLED |
| V04-command-position-ignored | KILLED |
| V04-quoted-word-ignored | KILLED |

## Adversarial results

Nested linked worktree home claim refused a peer and allowed its holder. Mixed governed/unmanaged path identity conflict refused; unmanaged-only and pathless unmanaged calls passed; corrupt target ledger refused. An unclassified logical session registered no processes and offered no socket wake.

V03 failing inputs: free/first -> second -> ../docs/new.go; and free/first -> ../other/redirect/../new.go with other/redirect -> ../docs/deep. Peer owns docs; kernel writes reach docs/new.go but original gate allowed. Both now pass permanent tests.

V04 failing input: bash -lc wrapping apply_patch heredoc. Native sibling proved held-file mutation. Literal printf/rg strings also falsely denied. Parser repair and controls now pass.

## Final latency

| Input | Median ms | p95 ms | Max ms | >100 ms |
| --- | ---: | ---: | ---: | ---: |
| edit | 13.46 | 14.27 | 14.92 | 0/40 |
| patch128 | 21.83 | 81.12 | 100.24 | 1/40 |
| patch128-four-distinct-candidates | 37.55 | 41.26 | 41.54 | 0/40 |

## Reproduction

`mutations.py` and `repair-mutations.py` retain exact edits and each .diff/.log. `latency.py` and `latency-distinct.py` retain generated hook input. `V03-before.log`, `V04-before.log`, `final-gate.log` separate failures and corrected regression evidence. `reconcile-boundary-check.py` reproduces real-refusal/synthetic-unknown boundary checks and14 rejected mutations.

## Constrained-review corrections

All TestGate pass after: accumulate resolution errors while completing every spelling; decode static ANSI-C quotes; share literal cwd discovery with shell wrapper AST traversal; preserve command -pv/-pV lookup behavior.

| Follow-up mutation | Result |
| --- | --- |
| V03-error-returns-before-other-spellings | KILLED |
| V04-ansi-literal-unknown | KILLED |
| V04-nested-cwd-context-omitted | KILLED |
| V04-all-directory-context-omitted | KILLED |
| V04-combined-lookup-not-recognized | KILLED |
| V04-raw-word-prefilter | KILLED |

The corrected nested-heredoc test uses shellQuote, preserving newlines; it fails both context-removal mutants. Exact logs are review-followups-after.log and V04-nested-cwd-context-omitted.log.

## Remeasurement after constrained-review repairs

| Input | Median ms | p95 ms | Max ms | >100 ms |
| --- | ---: | ---: | ---: | ---: |
| edit | 13.91 | 14.82 | 15.83 | 0/40 |
| patch128 | 22.09 | 34.31 | 58.21 | 0/40 |
| patch128-four-distinct-candidates | 32.43 | 42.75 | 68.89 | 0/40 |

Earlier timing including the 100.24 ms outlier remains in latency-before-review-followups.json. These are local samples under concurrent verification load, not a worst-case deadline guarantee.

Budget followup: TestGateShellBudgetRequiresJurisdiction watched fail then pass; ordinary unmanaged long/deep commands allow, known home/target ledger refuses. Blanket-refusal mutant killed without build error; all TestGate passed4.378s. Unseen placements beyond parser bounds remain unobserved cooperative scope.
Native sibling reports all four revised reconcile suites/self-tests passed; see /private/tmp/buddy-native-verification-summary.json.
