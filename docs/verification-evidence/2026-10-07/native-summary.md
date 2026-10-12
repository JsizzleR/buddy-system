# Native and script verification summary

Authorized verification only. Retained native binaries, inert keys, disposable configurations and loopback providers. No installation, real ledger migration, paid provider or external messaging. Results are local fixture evidence, not production proof.

/private/tmp/buddy-verification-native-4jbaj2p2; refreshed before final mixed-fleet and again before standalone-adapters. Earlier suites retain their captured source-manifest hashes; do not claim all used final tree bytes.

| Suite | Checks | Checker mutations rejected | Receipt |
| --- | --- | --- | --- |
| claude-mods | 33/33 | None | `/private/tmp/buddy-claude-mods-wbb_n7gj/result.json` |
| claude-peers | 50/50 | 54 | `/private/tmp/buddy-claude-peers-yyz80iwc/result.json` |
| claude-resume | 22/22 | 21 | `/private/tmp/buddy-claude-resume-620shi4x/result.json` |
| claude-interrupted | 98/98 | 86 | `/private/tmp/buddy-claude-interrupted-rljgi7q3/result.json` |
| claude-compaction | 26/26 | 30 | `/private/tmp/buddy-claude-compaction-s449mxiq/result.json` |
| claude-endpoints | 38/38 | 32 | `/private/tmp/buddy-claude-endpoints-e83_4aek/result.json` |
| recovery | 26/26 | 26 | `/private/tmp/buddy-recovery-ihxkvd6j/result.json` |
| interrupted | 52/52 | 46 | `/private/tmp/buddy-interrupted-5vibh80e/result.json` |
| uncertain | 40/40 | 38 | `/private/tmp/buddy-uncertain-kriee1yu/result.json` |
| compaction | 23/23 | 34 | `/private/tmp/buddy-compaction-hetr0rln/result.json` |
| interrupted-compaction | 75/75 | 90 | `/private/tmp/buddy-interrupted-compaction-32_w3vmg/result.json` |
| claude-history | 130/130 | 175 | `/private/tmp/buddy-claude-history-x8bf3bx9/result.json` |
| mixed-fleet | 37/37 | 41 | `/private/tmp/buddy-mixed-fleet-2nxrxh7k/result.json` |
| claude-interrupted-compaction | 113/113 | 222 | `/private/tmp/buddy-claude-interrupted-compaction-c_glu0md/result.json` |
| codex-shell-lifecycle-edge | 15/15 | None | `/private/tmp/buddy-codex-contract-qualification-38n2tpxc/result.json` |

| standalone-adapters | 9/9 | None | `/private/tmp/buddy-step2-native-lqi8g020/result.json` |

All 16 tabled suites passed. Earlier failures remain listed in the JSON; final mixed-fleet is37/37 after the close-race fix. Standalone mutant guards10/10 killed; the first history import-layout failure was excluded and re-run correctly.

## Native residuals

- **F01_direct_initial: REFUTED original matcher, fixed and rerun.** ^apply_patch$ does not receive Bash shell-routed patch; held file and collateral changed. Covering union refused; persistent final suite15/15.
- **F01_wrapped: REFUTED original detector, fixed and rerun.** Exact bash -lc heredoc bypass changed held path before; after AST fix6/6 and unchanged.
- **F05_F06_Codex: VERIFIED native cases10/10.** Delete removes symlink entry, leaves target; dotdot uses lexical leaf; Add through dangling symlink creates target; Move deletes source/writes destination. Held spelling cases refused; unheld controls actually executed.
- **F05_F06_Claude: VERIFIED Write cases6/6; dedicated Delete/Move tool cases not applicable.** Write refuses both existing and dangling symlink even with unheld target; dotdot normalized lexically before hook. Held lexical path refused; unheld lexical leaf changed, physical alternate unchanged.
- **F07_regrouped_job: REFUTED broader all-descendant cleanup.** Captured descendant Bash job had pgid42169; adapter42111 and native42114 differed. Close exit0 in0.00156s, job remained alive. Exact captured identity fallback killed only fixture group42169. No fallback promoted this to pass.
- **F07_native_deny_branches: NOT CHECKED.** Attempted actual Agent/Bash branch emitted an async agent and never armed a subagent permission callback; no-active-input branch was not reached. Do not count13/14 as a qualifying pass. Ordinary native permission-deny wire shape did succeed in settings controls.
- **F09_F21_settings: VERIFIED native2.1.291 matcher/timeout floor semantics; other admitted versions and600s bound NOT CHECKED.** Literal and anchored uppercase matchers fired and denied. Lowercase control did not fire. Hook configured timeout10 and sleep11 yielded can_use_tool after10.052s, showing hook cancellation is not native deny; explicit permission denial kept file absent. These were direct native semantic cases, not an admitted slow Buddy hook.
- **F29_loaded_ledger: VERIFIED31/31.** Actual SQLite writer held700ms at endpoint admission; endpoint remained unknown; gate began236/253ms after lock armed, held Write denied and unheld Write allowed. Initial process-local Python3.9 monotonic timestamps excluded; final timestamps used wall clock.

## Commands and artifacts

Exact argv for each suite, native binary/artifact hashes, per-case logs, mutation output, older failures, script ownership and uncompleted checks are in `/private/tmp/buddy-native-verification-summary.json`.

All native commands ran from the private captured source tree using `python3 -B scripts/qualify-<suite>.py`; each validator ran `python3 -I -B scripts/check-<suite>.py <receipt> --self-test` (mixed uses `--receipt`, and delivery checker covers endpoints/peers). Commands need authorized loopback binds outside the restricted sandbox. No raw transcripts are copied into this summary.

## Uncompleted checks

- Native subagent/no-active-input permission branches not armed/observed.
- Matcher/timeout semantics for2.1.289/2.1.290 and600s upper bound not executed.
- No production/provider qualification, installation or schema15 deployment.

## This worker changed

- scripts/qualify-codex.py: persistent --shell-patch and covering Bash/apply_patch matcher
- scripts/qualify-claude.py, qualify-claude-resume.py, qualify-claude-compaction.py, qualify-claude-interrupted-compaction.py: canonical native argv0 in observers (ownership edits belong to adapter worker)
- scripts/qualify-claude-endpoints.py: canonical argv0 including sandbox path, stale socket observation and exact receiver eligibility
- scripts/check-claude-delivery.py: receiver cleanup predicate and two mutations per endpoint case
- scripts/qualify-claude-peers.py: canonical argv0 for direct native launch
- scripts/qualify-recovery.py: explicit --claude-version option for retained291 rerun
- scripts/qualify-claude-jobs.py: new durable native regroup probe; executed but not independently source-reviewed

## Standalone adapter completion

`PATH=~/.codex/packages/standalone/releases/0.160.0-aarch64-apple-darwin/bin:$PATH python3 -B scripts/qualify-adapters.py` ran from the refreshed private source tree, exited0 and passed9/9. Receipt: `/private/tmp/buddy-step2-native-lqi8g020/result.json`. Adapter exit0; hashes are in the JSON summary.

Claude2.1.291 advertises no dedicated Delete/Move tool. Governed path tools are Edit, Write and NotebookEdit. Bash rm/mv side effects are outside the declared-path gate contract; the Codex apply_patch delete/move cases were executed. This is an interface boundary, not an omitted direct-tool test.

## Final root continuation: V13

The new job runner source review found that forced adapter Close could appear
successful. Three permanent mocked controls failed before/passed after; removing
the final no-fallback guard was killed. The focused follow-up review passed.
Final native rerun: `/private/tmp/buddy-claude-jobs-btpg0g3j/result.json`, exit1,
RED3/4; no forced adapter cleanup, but the separate-group tool remained alive.
Only its captured matching group was cleaned, and all recorded process IDs were
then observed absent. `native-job-result.json` preserves this compact receipt.
The prior2/3 receipt remains historical evidence.
