# Audit of the verification report

2026-10-07. An audit of [the verification report](CODEX-ORCHESTRATOR-VERIFICATION-REPORT.md)
against its retained evidence and the working tree, written for the session that
picks this work up next. Claude Code session `s-c953a5cd`, at the operator's
request. No native `claude` or `codex` was launched, no binary built from this
tree touched the real ledger, and nothing was committed. Experiments ran in
private copies of the tree; their raw files sit in this session's scratch
directory and are **ephemeral** (listed at the end).

**Result.** The report's coverage claim holds: F-01 to F-46 each have a row,
every section the brief asked for is present except adversarial item 11, the
retained evidence is intact, and every number the report states matches the
retained JSON. Its conclusion (not cleared for landing, installation or
newer-version admission) stands, and is milder than the tree warrants:

- two safety gaps are missing from its open list (A-01, A-02);
- several dispositions are stronger than the tree supports (A-04 to A-11);
- documented sentences the code does not bear out are listed under A-13.

This session landed the missing regressions behind A-03 to A-05 ("Landed
here"). Everything else below is open.

## Status, 2026-10-08

Claude Code session `s-3c6aeb2d` worked items 1 to 4 of the work order, at the
operator's request. Nothing was committed or installed, no tree-built binary
touched the real ledger, and no native `claude` or `codex` ran. All evidence
below is hermetic tests, mutations in private copies, and constrained Codex
reviews (source evidence only). None of it is native.

On 2026-10-11 session `s-0f653d6a` ran the A-01 mutants left unrun and the
final hermetic check, and landed regressions for the four survivors (test code
only; `patch.go` is unchanged). The bullets below carry those results.

On 2026-10-12 the same session committed the tree at the operator's request. It
was NOT installed, also at the operator's request: another project on this machine
could not take the schema-15 risk. After the operator chose Codex's narrower
ownership contract, the session built it (D-077, 2026-10-12 continuation), closed
F-31 (D-072 continuation), and integrated a lane's F-33, F-13, F-37, F-35 and part
of F-36. A re-review of A-01 reopened it; see the A-01 bullet below. Nothing native
ran.

On 2026-10-13, at the operator's request, the native suites were rerun on the final
bytes (A-07, HEAD `316b337`): 16/16 green on the first attempt, the 13 independent
checkers passing on fresh receipts, V-12's job probe RED 3/4 on a criterion that
predates the narrow contract while the contract itself held natively. See the
[report section](CODEX-ORCHESTRATOR-VERIFICATION-REPORT.md#native-rerun-on-the-final-bytes-2026-10-13)
and [its evidence](verification-evidence/2026-10-13/README.md). Separately, also at
the operator's explicit request, `install.sh` installed `316b337`, migrating this
checkout's ledger to schema 15.

| Item | State | Where |
| --- | --- | --- |
| A-01 static shell forms | **Closed, hermetic.** Every static form listed under A-01 is refused where a ledger governs, each with a kernel control that the real shell runs it. `pushd`, `~` and the nested forms are placed. The ten false-denial controls pass. The borderline forms are decided: covered, or recorded as the cooperative limit. One crafted case stays open by decision: a persistent `exec <<'SH'` redirection read by a later statement's shell. Native rerun still owed (A-07). **REOPENED 2026-10-12** by a re-review (bullet below). *2026-10-13, native on the final bytes:* the plain heredoc and `bash -lc` heredoc DENIED (15/15, 7/7), Codex paths 10/10, Claude Write paths 6/6. The re-review's open forms and `exec <<'SH'` have no native runner, so it stays reopened. **RE-CLOSED hermetically 2026-10-27 (D-106)** for the re-review's confirmed forms: A (`env -S "$x" -C /gov`), B (`env -S '-C /gov #'`), C (`script` no-command stdin) and E (over-budget option values) are now refused where governed, each with a kernel control (C darwin-only, padded past script's pty buffer; A/B via `pwd -P`), and each fix reverted in a private copy with its test watched to die (7/7 mutants). D was refuted as stated. fish F1/G1/G2 were then FIXED in `shell()` at the operator's decision (from verdict3.md's stated behaviour, not fish's source; fish not on box, controls NOT RUN). A FULL review (codex design + code + opus cross-check) and three codex re-verify rounds folded further; **what stays OPEN** (recorded respellings, D-106): the attached/assignment-dynamic `env -S` jurisdiction/budget forms, util-linux `script -c` repeated and `-t` optional value, GNU abbreviated long options, a front unknown-wrapper (`lockf -k`), the `git` action-position brace (`-C .`/`submodule`/`bisect`), the find action-position brace, and the `env -S <wrapper> {…}` / `exec <<'SH'` cooperative limits. **Documented over-refusal:** BSD `script -T` playback (util-linux `-T` runs a shell). The native rerun of all these forms is still owed (A-07). | `internal/cli/patch.go`, `shellroute_test.go`; D-106; D-073 audit continuation; USAGE "Shell-routed patches" |
| A-02 shared core | **Closed, hermetic; the narrow contract built 2026-10-12** (escaped descendants observed, recorded, reported at close and on stderr, never signalled; resume refuses while a recorded one lives; D-077 2026-10-12 continuation). **Shared core, 2026-10-08:** A durable pre-spawn ownership record, and a resume that refuses while the recorded adapter, leader or group lives. Also: `--wait-prior` and a single-use launch authorization. `.start` fences only Start; every spawn is fenced by the flock plus intent-before-spawn. Out by the operator's choice (2026-10-11, Codex's narrower contract): unpinned killing and `--end-prior`. Still owed: `Setsid` once Q1/Q3 pass, and the native rerun. *2026-10-13:* the native rerun is done, and the narrow contract held in V-12's job probe on both attempts (escaped shell and job recorded in `.proc`, reported on stderr, not signalled, adapter exit 0); the probe stays RED 3/4 on its pre-contract criterion, which is being updated. Still owed: `Setsid` and Q1-Q7; Q2, Q4 and Q5 are not observable with the current runners. |
| A-07 native rerun | **Done 2026-10-13, operator-authorized.** All 16 suites on the final bytes, green on the first attempt; 13 independent checkers pass on the fresh receipts. Three suites (claude-mods, codex-shell-lifecycle-edge, standalone-adapters) have no independent checker yet: pending. | [Report section](CODEX-ORCHESTRATOR-VERIFICATION-REPORT.md#native-rerun-on-the-final-bytes-2026-10-13); `verification-evidence/2026-10-13/` | `internal/harness/ownership.go`, `ownership_test.go`, `cmd/buddy-adapter/resume_test.go`; D-077 A-02 continuation |
| 3. Correct the record | **Done.** Report rows F-01, F-07, F-08, F-13, F-14, F-18, F-27 and F-41, and the mutation table. A-11 markers and anchors. A-13 sentences. A-10 test, with a 2 s stdin bound found necessary. A-14: tests for the target ledger's own label, and new guards for each patch.go guard the rewrite kept. | Report; `decisions.md` D-070, D-071, D-077; `review-charter.md` GIVEN 78; USAGE; CLAUDE.md |
| A-12 dependency | **Done.** | D-073 audit continuation; CLAUDE.md |

Reviews. A-01: two design passes (REJECT, then PASS WITH CHANGES) and nine
code rounds, each a REJECT on exact inputs. Every finding was reproduced by a
failing test before its fix. The last round's fixes were not re-reviewed. A-02:
design PASS WITH CHANGES, code PASS WITH CHANGES; its one P2 was reproduced
and fixed. Mutation and check results:

- A-01: all 110 mutants have run on the final code (the count said 111 until
  2026-10-11; see D-073). 71 ran on 2026-10-08, before the sweep was stopped
  for low system memory. The other 39 ran on 2026-10-11, with the 2026-10-08
  survivor again. Every one is now killed by a test's assertion. Five guards
  had no test until a sweep found them: `over-budget-command` (2026-10-08), and
  `data-skip-expansion`, `scriptOf-overbudget`, `envS-builder-cap` and
  `unparsed-bound` (2026-10-11). Each now has a regression watched to kill its
  mutant. The memory went to `brace-budget`: without the field count, the
  budget test expands without end (8.9 GB inside 60 s). An assertion kills it
  first, but run it alone and bounded. Details are in the D-073 audit
  continuation.
- A-01, reopened 2026-10-12. The ninth round's fixes had never been re-reviewed. A
  narrowed constrained Codex pass over `wrapper()` and `shell()` returned REJECT:
  four gaps it confirmed by source review (two `env -S` jurisdiction paths, `script`
  with no command, an over-budget brace word taken as an option's value), one
  refuted as stated, and on fish one more fail-open and two false denials. Kernel
  probes showed a real shell runs the fixture's apply_patch for two of them; for
  `script` none ran here. None is reproduced by a test yet: the lane doing it was
  stopped mid-turn by a safety classifier, declined to resume, and this session did
  not take it over. Open for the operator. The prompts, verdicts and probes are in
  `~/Archives/buddy-system/2026-10-12-a01-rereview/` (SHA256SUMS beside them).
  *Closed 2026-10-27 (D-106).* The four confirmed gaps (A, B, C, E) each got a
  regression in `shellroute_test.go` (`TestGateShellRouteReReview*`) with a kernel
  control where its shell is on this box — C darwin-only and padded past script's
  pty line-discipline buffer (200 `:` lines never ran, 600 ran 8/8), A/B via a
  `pwd -P` landing check — and the fix in `patch.go`, each reverted in a private
  rsync'd copy and its test watched to fail (7 guards, 7 killed). D stays refuted.
  The `exec <<'SH'` limit is tested as an accepted gap, not fixed. fish's F1/G1/G2
  were later FIXED in `shell()` at the operator's decision (from verdict3.md's
  stated behaviour, not fish's source; fish not on this box, kernel controls NOT
  RUN). An earlier turn drafting their fixtures was stopped by a safety classifier
  and not rephrased around; the operator then chose the stated-behaviour route.
  The FULL-tier review (Codex design pass + a 3-subagent panel) then surfaced
  adjacent gaps, folded the same way (`TestGateShellRouteReReviewFollowUp`, 11
  guards mutation-covered): a `script -p` playback false denial the first C fix
  introduced, a `script --` no-command miss, util-linux `script` log-options added
  to the value table (NOT RUN here), and two over-budget value consumers the first
  E pass missed (`env -S` clean-value-then-brace-tail, and `git -C <brace>`).
  A second fold then folded the FULL review's codex@xhigh CODE pass (REJECT, 10 —
  five of them regressions the first fold introduced: playback precision, `script
  -c` with a command, the `env -S echo` over-budget false denial) and opus@xhigh
  cross-check (ACCEPT WITH CHANGES, 9: more over-budget consumers in `findActions`/
  `gitRoute`/`scriptFile`/`trap`, and the corrected "not only over-refuses" for B).
  The operator then decided fish F1/G1/G2 be FIXED in this item (from verdict3.md's
  stated behaviour, not fish's source; fish not on box, controls NOT RUN). 18 guards
  are mutation-covered. codex@xhigh re-verify rounds 1 and 2 (REJECT, 8 then 9)
  folded further fold side effects — fish long `--command`, a `fish -c true ""`
  panic, fish init-commands under a later `--no-execute`, fish `--command` with no
  value, and over-budget guards that false-denied common DATA braces (`find -exec
  grep -l foo path{1..300}`, `env -S 'env echo' path{1..300}`, `git diff --
  path{1..300}`), which per the operator's direction (a false denial costs every
  Claude session a tool call; a crafted brace bypass is free under Claude's allow
  arm) were REVERTED, the crafted forms recorded open. Recorded OPEN as
  respellings (D-106): the attached/assignment-dynamic `env -S` jurisdiction/budget
  and `env -S <wrapper> {…}` forms, util-linux `script -c` repeated and `-t`
  optional value, GNU abbreviated long options, a front unknown-wrapper
  (`lockf -k`), and the `git` and `find` action-position braces
  (`git -C .`/`submodule`/`bisect {…}`, `find . {-exec,…}`). Documented
  over-refusal: BSD `script -T` playback (util-linux `-T` runs a shell). Hermetic
  only; the native rerun of these forms is still owed (A-07). Re-verify round 3
  (the bound) run by the orchestrator; the item lands at the bound with the last
  verdict named in the commit.
- A-02: 27 of 28 mutants killed, one equivalent (D-077 A-02 continuation). The narrow
  contract built on 2026-10-12 has its own sweep (D-077 2026-10-12 continuation).
- `sh scripts/check.sh hermetic` over the finished tree, the 2026-10-11
  regressions included, ran on 2026-10-11: exit 0, **HERMETIC GREEN** (vet,
  tests, unscoped race, done-checks, script self-tests; the live leg is not
  part of that tier). Until then, only each worker's own slice had reported
  green.

## How to use this

1. The plan's Next list now points here and names A-01 and A-02. They are the
   blockers this audit adds, beside V-12.
2. "Landed here" lists the tests this session added and the mutations each one
   was watched to kill.
3. "Ownership proposal" (A-02, V-12) is a proposal: it needs an operator
   decision and native qualification before any code.
4. "Work order" is the suggested sequence for the next session.

## What was checked

The evidence categories stay separate, as in the report.

| Category | What | Result |
| --- | --- | --- |
| Evidence integrity | `evidence-sha256.json` digests; the tree against `source-final.json` | 155/155 digests match. The tree was byte-identical to the final manifest (255 files) before this session's edits: nothing changed after the report. |
| Executed checks | `go build ./...`, `go vet ./...`, `sh scripts/check.sh hermetic` | Clean, clean, HERMETIC GREEN; the live leg was not run. |
| Report numbers | Native suite counts, mutation totals, latency figures and review verdicts against the retained JSON and the raw review logs | All match. The raw review logs match their recorded digests, and the final gate and process reviews began after the last edits to the files they reviewed. |
| Mutation replay | Every retained mutant re-applied to the FINAL tree, each run against its whole package | Adapter 58 (one rewritten for the final code), gate/mail 43 (five rewritten), root 10, scripts 19. |
| Adversarial | 84 shell forms through the gate in a governed temp repo: 30 the recognizer is built for, 34 more, 10 false-denial controls, 10 jurisdiction cases. Also descriptor, budget, resume and process-group experiments. | A-01 to A-05 |
| Native binding | Each native receipt's `source-manifest.json` against the final tree | A-07 |
| Docs against code | D-073 to D-077, GIVENs 74–78, USAGE "Codex participation" and "Structured adapters", CLAUDE.md | A-13 |

## Findings

### Safety: missing from the report's open list

**A-01: the shell-patch recognizer passes static forms it is documented to
cover (F-01, V-04; report lines 55 and 286).**

- Setup: a governed temp repo where a peer holds `held.go`, `p = apply_patch < p.patch`.
  - Denied: `p` and `bash -c 'p'`.
  - Allowed: `bash -c -- 'p'`, `eval 'p'`, `bash <<'EOF'` and `bash -s <<'EOF'` with `p`
    in the body, `echo 'p' | bash`, `timeout 60 p`.
  - Real bash runs each allowed form.
- The recognizer is built for 30 forms, and denies 29 of them:
  - the forms: `exec`, `time`, `nohup`, `command`, `builtin`, `xargs`, `sudo --`, `env`,
    `/usr/bin/env`, `env -S`, groups, subshells, `if`/`for`/`case`, functions, negation,
    pipelines, here-strings, quoting and escapes, line continuation, and
    `bash`/`zsh`/`dash -c` (also with `-O`, `--norc` and an absolute path);
  - the one allowed is `coproc apply_patch`, which zsh runs.
- Other static forms the D-073 continuation and USAGE claim are also allowed:
  - `sh -c --` and `zsh -c --`;
  - `bash +x -c` and `bash +o posix -c`;
  - `trap '…' EXIT`;
  - brace expansion `bash -c '{apply_patch,} < p'`;
  - `env -P /usr/bin apply_patch`;
  - `xargs -I % -S 512 apply_patch %`.
- Borderline, also allowed. These matter only if the worker's shell or PATH has them:
  - `csh -c` and `tcsh -c`;
  - the wrappers `nice`, `caffeinate`, `arch` and `stdbuf`;
  - zsh's `noglob`, `nocorrect`, `repeat 1`, `=apply_patch` and an anonymous function.
- Allowed because USAGE excludes heredocs: `bash <<<` and `bash <(…)`, besides the heredoc
  and pipe forms above.
- Documented as cooperative, and allowed as expected: `find -exec`, `script`,
  `bash <file>`, `source`, `$x` and `${U:-apply_patch}`.
- Jurisdiction from an ungoverned cwd:
  - `cd`, `cd -P` and `env -C` are placed.
  - These are not: `pushd <repo> && apply_patch`, `cd ~/<path-to-repo> && …` (the gate
    places `~` under the cwd; bash and zsh land in the repo), `bash -c -- 'cd <repo> && …'`,
    and `bash <<'OUTER'` containing `cd <repo> && …`.
- False-denial controls: 9 of 10 pass. `bash -n -c` is denied; that is harmless.
- Disposition: repaired for the tested forms, partial overall.
- USAGE says "literal heredoc bodies are not commands". That is false when the heredoc
  feeds a shell.
- The native F-01 evidence (shell/lifecycle/edge 15/15, wrapped shell 6/6) used a buddy
  built at 01:09:55. That is before the final edits to `patch.go` (01:18:07, +102/−47 lines)
  and `cli.go` (01:27:53). The final recognizer has hermetic evidence only.

**A-02: a resume after an adapter SIGKILL launches a second worker while the
first is alive (F-07 closure requirement).**

- The response requires: "Recovery must refuse or reconcile uncertain prior native
  ownership before launching a second controller. Do not infer safe resumption from a
  free flock alone."
- D-077 and GIVEN 78 state the limit; nothing enforces it.
- What resume checks today:
  - `Controller.Resume` (`controller.go:337`) checks the receipt, the `.start` intent, the
    journal identity, and, for Claude only, journalled terminal results plus idle.
  - `codexBackend.Resume` (`codex.go:108`) has no admission check.
  - Neither the receipt nor the journal records a native pid.
- Experiment (fake Codex worker mid-turn, `cmd/buddy-adapter` entry fixture):
  - SIGKILL the adapter. The worker (pid 51299) and its tool stay alive.
  - `--resume` returns no error and launches a second app-server (52349). It receives
    `thread/resume` while 51299 is still alive.
- Unmeasured: whether a real worker is still running at that point depends on its
  behaviour on stdin EOF.
- D-075 says arbitrary crash timing is not admitted; the Codex resume code admits it.
- Open. See the ownership proposal.

### Fixed in this session

**A-03: the `fd < 3` operator-descriptor guard had no permanent test (F-02).**

- `TestAdapterOperatorDescriptorAdmission` only ever passed descriptor 3 or 9.
- `--operator-fd 0`, with stdin a read-only pipe, passes every other check. Admitted, the
  coordinator's control stream would be read as operator approvals.
- Landed as that test's `stdin` case.

**A-04: fourteen adapter mutation kills rested on tests that never landed
(report line 150, "58/62 killed").**

- Replayed against the tree's own tests, 18 of the 58 Go mutants survived:
  - the four the report accepts as redundant;
  - fourteen the verifier killed with nine `TestVerification*` tests kept only as `.txt`
    files in the evidence directory:
    - `fd-lower-bound`, `group-term`, `group-kill`, `sigpipe-ignore`;
    - six Claude approval `ErrNotSubmitted` wraps;
    - three Codex ones (`ReadTurn` twice, `call` once);
    - `endpoint-shared-budget`.
- One mutant (`controller-not-submitted`) no longer applies to the final code. Its rewrite
  for the final send-dedupe condition is killed.
- The four F-35 Python mutants are killed by `scripts/test_native_proxy.py`.
- So the tree killed 44 of the 62. D-077's "35 mutants, all killed" is not borne out either.
- The ninth private test, on the legacy journal and contradictory refusals, was already in
  the tree as `TestControllerJournalRefusalRejectsNativeEvidence`, so it was not copied.

**A-05: the descendant-cleanup test caught its mutant by chance.**

- `TestJSONProcessCloseEndsDescendants` announced the tool before the tool's
  `trap '' TERM` had run. Close's TERM usually ended the tool by itself, so the test passed
  with the group SIGKILL deleted: it caught the deletion in 3 of 20 runs.
- The tool now signals once its trap is set.
- Result: the deletion is caught every time (20/20 in the first trial, 10/10 for the landed
  test), and the test passes every time with the KILL present (20/20 and 10/10).

### Evidence and dispositions: open

**A-06: seven of the ten "killed" script guards depend on an ephemeral capture
(F-13, F-14; report lines 67, 68 and 152).**

- These seven: the five named evidence predicates of
  `check-claude-interrupted-compaction.py`, plus `same_run` and `same_contract`.
- What kills them: only that checker's `--self-test` run against the 93 MB receipt
  `/private/tmp/buddy-claude-interrupted-compaction-c_glu0md/result.json`.
- `check.sh hermetic` never runs it. It runs `check-mixed-fleet.py --self-test`, which
  never calls `suite_binding`.
- After a reboot nothing exercises those guards.
- The other three (`history_exactly_one`, `still_owned`, the assert gate) are killed in the
  hermetic tier.
- "Owned-process match" in the 10/10 row is the same mutant as `f35-birth-comparison`.
- This is F-33's open item seen from the mutation side.
- *Closed hermetically 2026-10-12:* every checker now self-tests on synthetic evidence in
  `check.sh hermetic`, so the suite binding is exercised by the tree, and 19 binding mutants
  are killed by its own tests.

**A-07: none of the 16 native suites ran the final product code (report lines 36–39).**

- Fourteen suites ran source whose manifest predates the final version of 10 product files:
  `cmd/buddy-adapter/main.go`, `internal/cli/{cli,gatetargets,patch,sent}.go`,
  `internal/harness/{claude,codex,controller,executable}.go` and `internal/store/adapter.go`.
- The changes after those runs are the V-03/V-04 follow-ups and V-07 to V-11, including the
  V-08 version probe in both backends.
- The final mixed-fleet rerun differs from final only in `cli.go`, so it did exercise the
  V-07, V-08 and V-11 repairs on both harnesses.
- `codex-shell-lifecycle-edge` and `standalone-adapters` recorded no source manifest.
- The report's "did not all run final source bytes" understates this: none did.
- Any native claim for the final code needs a rerun on the final bytes.
- *Done 2026-10-13:* all 16 suites, V-12's probe and the 17 extra path cases reran on
  HEAD `316b337`, a clean tree, and all 20 manifest-bearing receipts name one source
  manifest (`ee1a4482…`). Results in the report's 2026-10-13 section.

**A-08: the durable evidence does not identify its receipts (F-19; report lines 33–35).**

- Missing from `native-summary.json`:
  - 15 of its 16 suite rows carry no receipt digest; only the V-12 jobs receipt has
    `receipt_sha256`.
  - Five rows have no `checker_log`.
- The definitions of the ten script mutants exist only under
  `/private/tmp/buddy-verification-script-mutants/`, despite the evidence README's "drivers
  record exact replacements".
- As of this audit, all 65 `/private/tmp` directories the summaries name were still
  present: about 2.6 GB without the Go caches, 4.7 GB with them.
- **Archived 2026-10-07, at the operator's request,** to
  `~/Archives/buddy-system/2026-10-07-orchestrator-verification/`:
  - `evidence.tar.zst` (515 MB, sha256 `79be5158…3991`) holds the 62 verification roots
    and this audit's scratch directory, with paths relative to `/private/tmp`;
  - 14,159 files, each listed with its SHA-256 in `MANIFEST.sha256`;
  - a test restore re-checked every digest.
- Left out: Go build caches, and three endpoint sockets (a tar archive cannot hold a
  socket).
- `README.txt` there gives the restore command, which recreates the original
  `/private/tmp` paths the receipts name.

**A-09: F-08 "VERIFIED by withdrawal" overstates (report line 62).**

- Withdrawing reconcile removes only the history-read trigger.
- Any live frame over `nativeFrameLimit` still closes the transport and ends the worker's
  tree (`process.go:148,170`). The Claude image/PDF trigger is unqualified.
- The response asked for "preservation of the live worker".
- It should read: verified for reconcile; a live oversized frame still ends the worker,
  by design.

**A-10: F-27 dropped a requirement (report line 81).**

- The response asked to "Cover an open pipe as well as malformed bytes". The permanent
  test covers malformed bytes and EOF only.
- `decodeHook` (`cli.go:398–411`) reads stdin to EOF with no deadline.
- Measured with a private build against a temp ledger: `hello --session` on an inherited
  open pipe blocked 3.0 s, until the writer closed.

**A-11: F-18 and F-41 are partial (report lines 72 and 95).**

- D-075 records both supersessions, but D-070 (`decisions.md:4593–4594`, "Claude resume is
  unsupported") and D-071's 2026-10-06 continuation (`:4673–4676`, compaction on 2.1.290
  only) carry no in-place marker.
- D-070's control-operation list ("start… inspect and interrupt") was not corrected; only
  GIVEN 71 was.
- D-071's continuations are still unanchored bold paragraphs (F-42's "independently
  referenceable").

**A-12: an undeclared dependency.**

- The verification added `mvdan.cc/sh/v3 v3.12.0` (`go.mod`, imported by
  `internal/cli/patch.go`) to the safety binary.
- The report, CLAUDE.md ("pure Go, no cgo"), the decisions, README and USAGE do not
  mention it. It is pure Go and breaks no stated rule.
- It belongs in D-073 and CLAUDE.md.

**A-13: sentences the code does not bear out (the brief's adversarial item 11).**

- **D-073 continuation (`decisions.md:4783`) and USAGE (`:114–119`):** the shell claims
  are narrower in code than in text. See A-01. "Literal heredoc bodies are not commands"
  is wrong for a heredoc fed to a shell.
- **USAGE `:122–125`:** says any disagreement among `session_id`, `CODEX_THREAD_ID`,
  `CLAUDE_CODE_SESSION_ID` and `BUDDY_SESSION` refuses where a ledger governs.
  `harness_identity.go:70–71` returns nil with no marker and no `CODEX_THREAD_ID`, so a
  plain Claude hook whose ids disagree is not refused.
- **The "2 s grace":** `decisions.md:4952`, GIVEN at `review-charter.md:759`, USAGE `:1542`.
  The grace ends when the LEADER exits (`process.go:217–222`), and `watchExit` runs the
  same path on a leader that exits by itself. Tools can therefore get TERM and KILL back
  to back. The file header of `process.go` states this correctly.
- **D-077's header (`decisions.md:4933–4935`):** "35 mutants, all killed". See A-04.
- **GIVEN 78 (`review-charter.md:764`):** "a literal matcher naming all three tools". The
  code also admits `""`, `*` and `.*` (`claude.go:1087–1090`).
- **USAGE `:138`:** the Codex wiring example uses `"matcher": "*"`. The Codex coverage
  check (`codex.go:264–268`, shared by `diagnose`) admits only `""`, `.*` and the anchored
  `^(apply_patch|Bash)$` (or its reverse).
- **USAGE `:222`:** the qualification command omits `--shell-patch` (the V-01 case) and
  does not pin `--codex`. The default is `shutil.which('codex')`, which is 0.160.1 here and
  not admitted; the runner records the version but does not refuse it.
- **CLAUDE.md `:59–60`:** "`buddy diagnose` imports only its capability tables".
  `internal/cli/diagnostic.go` calls `harness.Diagnose`, so the claims binary links the
  package. "Launches nothing" does hold.

**A-14: gate guards no mutant covered.** Each of these survives the whole
`./internal/cli` package when deleted:
- `cli.go:2323`: the target ledger's own label in `PausedFor` (F-28's test covers only
  `pause all`);
- `patch.go:156`: the `sudo -D` directory;
- `patch.go:68`: the parse-error fallback;
- `patch.go:139`: `env -S` recursion;
- `patch.go:154`: the `xargs` option-value table.

**A-15: minor.**
- The F-04 refusal names the disagreeing variables but does not say that launchers must
  clear inherited identity, as the response asked.
- `msg all` has no adapter-specific test. It rides the shared `wakeAddr` guard, which
  mutation covers.

### Confirmed

- **Gate:**
  - the 37/37 gate/mail mutation row holds on the final tree: 38 killed, five rewritten for
    restructured code and killed, none surviving;
  - socket-fixture kills caught their mutants 10/10;
  - F-03, F-04, F-05/F-06, F-12, F-24, F-25, F-26 (both renewal directions) and F-28's
    `pause all` have permanent tests.
- **Adapter and transport:**
  - F-10 has a permanent test for each requirement: busy/refused then retried, close and
    reopen, a refusal not made durable, and an ambiguous write;
  - F-11's gap clears only on idle or end, and is pinned;
  - F-21's 10 s floor is pinned, with the 600 s bound honestly not checked;
  - F-22, F-23, F-29 and F-30 are as reported;
  - V-01, V-06 and V-09 mutants are killed.
- **Scripts:**
  - F-32, F-34, F-35, F-39 and F-40 are as reported;
  - the rejection counts reproduce on the current tree against the retained receipts:
    222 for claude-interrupted-compaction, 41 for the mixed fleet, 32 for delivery.
- **Report and docs:**
  - the report's correction of the brief is right: the oversized-frame probe asserts that
    the worker dies;
  - F-15, F-16, F-17, F-20, F-43, F-44 and F-45 are as reported.

## Landed here

Each test was run on the real tree. Each was then watched, in a private copy, to
fail with its guard removed, repeated where timing is involved. The four
mutants accepted as redundant were not targeted.

| Test | File | Guard removed | Caught |
| --- | --- | --- | --- |
| `TestJSONProcessCloseEndsDescendants`, race fixed | `internal/harness/process_test.go` | The group SIGKILL after the grace (`group-kill`) | 10/10, was 3/20 |
| `TestJSONProcessCloseTermsTheGroupFirst`, new | same | The group SIGTERM (`group-term`); a new mutant sending TERM to the leader only | 3/3; 3/3 |
| `TestClaudeApprovalRefusalsBeforeTransportAreNotSubmitted`, new | `internal/harness/wake_refusal_test.go` | Each of `Approve`'s six `ErrNotSubmitted` refusals | 6 of 6 |
| `TestCodexRefusalsBeforeTransportAreNotSubmitted`, new | same | `ReadTurn` naming a foreign thread, `ReadTurn` with no turn, `call` with no transport | 3 of 3 |
| `TestClaudeSettingsAdmitWhatTheRunnersWrite`, new | `internal/harness/claude_settings_test.go` | Two new mutants: a runner's hook timeout changed to 5; a runner respelled out of the scan | 1/1; 1/1 |
| `TestAdapterOperatorDescriptorAdmission`, new `stdin` case | `cmd/buddy-adapter/main_test.go` | The `fd < 3` refusal (`fd-lower-bound`) | 1/1 |
| `TestAdapterBrokenControlOutputEndsWorkerTree`, new | same | `signal.Ignore(SIGPIPE)` (`sigpipe-ignore`) | 2/2 |
| `TestHookEndpointTimeIsNotChargedToTheGate`, new | `cmd/buddy-adapter/hook_test.go` | The gate budget charged with the endpoint's time (`endpoint-shared-budget`) | 3/3 |

With these, the tree kills the fourteen mutants of A-04, so the report's
58 of 62 is now true of the tree itself. The four survivors are the redundant
ones the report accepts.

All eight tests pass on the real tree. Repeated with their guards present:
- the descendant test passed 10/10;
- group TERM 10/10;
- the endpoint budget 5/5;
- broken output 3/3.

## Ownership proposal (V-12, A-02)

Measured in hermetic experiments this session: darwin 27.0.1 arm64, go1.27.1, no
cgo, no root. No real `claude` or `codex` was run. This is a proposal for an
operator decision, not a decision.

**The cause of V-12.** Claude Code runs every Bash tool shell in a session of
its own. This session's own shell, pid 13118, showed pgid = sid = 13118 with
parent `claude`. So the V-12 job sat in a session no group or session signal on
the worker reaches. Only parentage reaches it, and only while the leader lives.

| Mechanism (unprivileged) | Catches | Misses | Cost |
| --- | --- | --- | --- |
| Group signal (today) | In-group processes, including double-forked children that kept the group | Anything regrouped or setsid'd | about 0 |
| Process-table walk by ppid from the leader, BEFORE the first signal (`kern.proc.all`) | Regrouped and setsid'd descendants whose parent chain is alive | Anything already reparented to launchd | 110–126 µs median, ≤ 508 µs at 815 rows; pid + start time re-read 2–7 µs before each kill |
| Session sweep (worker launched with `Setsid`, `getsid` per row) | ppid-1 processes that kept the session. A post-signal `sid == leader` sweep closed the capture-to-signal race: 3–4 regrouped children per trial escaped without it, 0/10 with it. | Anything that called `setsid`, as Claude's tool shells do | +136–142 µs per table. Under today's `Setpgid` the sid is the ADAPTER's session, and two experiments killed their own shell, so the rule is unusable without `Setsid`. |
| Periodic sampling (1–100 ms) | Nothing fast: 0/20 double forks | Fast double forks (the intermediate lives about 1.2 ms) | 126 µs per sample |
| kqueue `NOTE_FORK`-triggered scan | Long-lived children 20/20; double forks 3/20 (python), 11/20 (sh) | Most fast double forks | 0.2–0.5 ms per scan |
| `NOTE_TRACK` | n/a | ENOTSUP (errno 45) | n/a |
| `NOTE_EXIT` on a non-child | Exit of a prior leader the adapter did not spawn, 169 µs after SIGKILL | n/a | about 0 |
| Coalition id, `coalition_create` | Nothing: one id for the whole login session; create is EPERM | n/a | n/a |
| Responsible pid (private API) | Everything while the root lives | Not reachable without cgo | n/a |

A descendant that double-forked and left its session before capture cannot be
attributed by any supported unprivileged mechanism. The contract has to say so.

**Contract.**
- **Launch:**
  - The worker runs as leader of its own session (`Setsid`, which implies the group).
  - Before the worker's first native byte, the adapter writes and fsyncs `<receipt>.proc`
    with adapter pid and start, leader pid, start time, pgid, sid and the resolved
    executable identity.
  - While the worker runs, the adapter keeps a bounded set of descendant identities
    (256), refreshed every second and on `NOTE_FORK`.
- **Close:** ends, in order:
  - write admission;
  - the group: TERM, the grace, then KILL, with the leader unreaped throughout;
  - every process captured BEFORE the first signal by parentage or session membership,
    each signalled only after its pid and start time re-read as a match;
  - a second `sid == leader` sweep;
  - then it reaps the leader.
- **Receipt:** reports `captured`, `ended`, `refused` (start time changed, so not ours and
  not signalled), `survivors`, and always `unattributable: "unknown"`. It never says the
  tree is empty.

**Resume admission.** Passive checks first; side effects last.
1. **Existing checks.** Journal and receipt identity, as today.
2. **Ownership record.** `<receipt>.proc` must exist. If it is missing, refuse and name
   it: a free flock proves nothing.
3. **Prior adapter.** If the recorded adapter is alive (by pid and start time), refuse.
4. **Prior leader.**
   - Gone or recycled: admit and signal nothing.
   - Alive: refuse by default, printing pid, start and sid.
   - `--wait-prior` waits on `NOTE_EXIT`.
   - `--end-prior` ends only the identified tree and records what it ended.
5. **Recorded descendants alive after the leader is gone.** Refuse unless `--end-prior`.

The same rule applies to Claude and Codex, idle or mid-turn.

**Tests the proposal needs.** Each has a positive control and a mutation it must kill:
- the ownership record exists before the first native byte;
- Close ends a `setpgid` child, a `setsid` child, and a job under a setsid'd shell whose
  parent exited;
- a recycled pid with a changed start time is never signalled;
- a zero or one pgid is never signalled;
- a leader that exits by itself still has its recorded set ended;
- the session rule is refused under `Setpgid`;
- the resume matrix: refuse while alive, `--end-prior`, admit gone, admit recycled without
  signalling, refuse without `.proc`, `--wait-prior`;
- a pure classifier over a fixed process table.

**Native qualification, operator-authorized.**
- Q1, on Claude 2.1.291 under `Setsid`: init, endpoint socket, hook ancestry and gate deny
  are unchanged. Then `qualify-claude-jobs.py` goes green.
- Q2: a double-forking tool must appear as survivor or unattributable.
- Q3: the Codex 0.160.0 sandboxed shell layout and Close.
- Q4: a leader that exits by itself with a tool running.
- Q5: the leader's behaviour on stdin EOF after an adapter SIGKILL, for both harnesses,
  idle and mid-turn. This decides `--wait-prior`'s default.
- Q6: `--resume` refuses while the old worker lives, and `--end-prior` then resumes with
  no second app-server.
- Q7: a leftover endpoint socket after `--end-prior`.

**Risks.**
- `Setsid` changes native behaviour until Q1 and Q3 pass.
- The identity-to-kill window is 2–7 µs. It has no hard bound (pid wrap at 99,999).
- After a leader exits by itself, only the recorded set finds Claude's setsid'd tools.
- `--end-prior` on a mid-turn Codex loses the turn.

### Codex's review of the proposal

How it was run:
- A constrained design review through `scripts/codex-review.sh` (gpt-6-astra, xhigh,
  default tier, charter prepended), run in the foreground, 2026-10-07.
- The proposal, the measurements and the code were inlined, and Codex was told not to use
  tools.
- Its verdict is source evidence only. Its P1/P2 items are not yet reproduced by a test.
- The full prompt and verdict are in the archive (A-08), as
  `claude-501/…/scratchpad/codex-ownership-{prompt,verdict}.md`.

**Verdict: REJECT as written.** In its words: "use Setsid with pinned-group signalling,
durable descendant observations and fail-closed resume; defer unpinned descendant
killing and --end-prior."

**Agreed:**
- `Setsid` as the launch boundary, subject to Q1/Q3. Those must observe, under the real
  launch argv:
  - pid, birth time, ppid, pgid and sid for the worker, its hooks and its tool shells;
  - initialization, I/O, endpoint discovery and admission;
  - a tool run and a gate denial;
  - native interrupt, and which groups it signals;
  - controlling terminal and `/dev/tty` behaviour;
  - Close and leader self-exit while an unrelated neighbouring session stays alive.
- Hook ancestry should survive, because `Setsid` changes no ppid. That still has to be
  observed.
- Resume refuses while the recorded leader lives. Admission makes one decision after
  every check, so "leader gone" must not admit before the descendant check runs.
- `--wait-prior` is a bounded, passive wait that reruns the whole admission. A timeout or
  an observation error refuses.
- Receipts written before `.proc` existed get no automatic resume. Ownership is never
  reconstructed from names, a free flock or a convenient pid.
- The sampler is evidence for reporting and refusal, not a safety guarantee.

**Findings:**
- **P1: descendant pid reuse.**
  - The sequence: the adapter verifies (P, start A); it is descheduled; P exits, is
    reaped and the id is reused; `kill(P)` then reaches an unrelated process.
  - The 2–7 µs is the cost of the lookup, not a bound on the gap before the kill.
  - Only the adapter's own unreaped child pins its group.
- **P1: `--end-prior` has no pin.**
  - The prior leader and its group can exit, be reaped and have their id reused between
    the check and `kill(-pgid)`.
  - A new adapter cannot keep the old leader unreaped.
- **P2: self-exit escapes more than the proposal excludes.**
  - The sequence: just after a scan, the worker starts one child that calls `setsid`, then
    exits before the next scan.
  - The child is unrecorded, reparented and in its own session, so the recorded set and
    the session sweep both miss it. No double fork is needed.
  - The fix is in the ordering: stop write admission, capture, then close stdin.
  - The exclusion widens to "any unrecorded descendant that lost its observable ancestry
    and left the root session".
- **Open questions:**
  - A session "led by a captured descendant" must require a protected leader identity,
    never a saved numeric sid.
  - Does `.start` fence every resume spawn until the new `.proc` is durable? If not, an
    old `.proc` that names a dead leader lets two resumes each launch a worker.
  - "Before the first native byte" needs a durable pre-spawn intent, because the worker
    can run before its pid is published.

**Receipt semantics it asks for:**
- `captured`: with observation time and ownership evidence.
- `ended`: only on observed termination; a successful `kill` is not termination.
- `refused`: with the expected and the observed identity.
- `survivors`: alive at a stated time after the deadline.
- A separate unknown for unresolved status.
- Scan times, a local scan sequence (not a kernel generation), errors, truncation and
  sampler gaps.
- An explicit overflow policy for the 256 bound.
- `.proc` bound to the exact journal and launch attempt, published crash-consistently.

**Tests it adds:**
- a pause between verify and signal, with a recycled pid or pgid substituted;
- sid reuse before the sweep;
- a single-fork setsid child at an immediate leader exit, and at exit on EOF;
- a crash at every spawn and publication boundary, including resume with an older `.proc`;
- overflow, scan failure and persistence failure;
- a full TERM grace for every admitted target, independent of the leader's exit;
- signal submission kept distinct from observed termination.

**Alternative to qualify:** native cooperative cancellation. The worker's own process is
the parent of its tool shells and has not reaped them, so it can end them safely before
the adapter ends it. For Claude, whether an interrupt ends running and background tool
processes is a native question.

**Where Fable and Codex agree, and the one difference.**
- Both adopt `Setsid`, the durable ownership record, and a resume that refuses while the
  old worker lives. That much closes A-02.
- They differ on whether Close and `--end-prior` may signal processes the adapter cannot
  pin:
  - Fable: yes, after a pid + start re-read. On darwin a reused pid needs a full wrap
    (99,999 ids) inside that window.
  - Codex: no. Report them instead, keeping the absolute rule.
- Under Codex's contract, V-12's escaped job is reported, not ended.
  `qualify-claude-jobs.py` then expects "reported, cleanup incomplete" rather than "ended",
  unless native cooperative cancellation is qualified.
- **Decided 2026-10-11: Codex's narrower contract.** Recorded in D-077's "2026-10-11:
  the operator chose the narrow contract".

## Work order for the next session

1. **A-01.** *Closed 2026-10-08 (hermetic; see Status).*
   - Close the static forms.
   - Write the regressions first and watch each one fail.
   - Then correct USAGE `:114–125` and the D-073 continuation.
2. **A-02 and V-12.** *Shared core closed 2026-10-08 (hermetic). The operator chose Codex's narrower contract on 2026-10-11 (D-077); built 2026-10-12, hermetic. `Setsid` waits on Q1/Q3.*
   - The operator chooses between Fable's proposal and Codex's narrower contract (see
     "Codex's review of the proposal").
   - Both start the same way: a durable pre-spawn ownership record, and resume that
     refuses while the recorded worker lives. That closes A-02, does not depend on
     `Setsid`, and can come first.
   - The tests for it come from Codex's list.
3. **Correct the record.** *Done 2026-10-08.*
   - In the report: the F-01, F-07, F-08, F-13, F-14, F-18, F-27 and F-41 rows, and the
     mutation table (A-04, A-06).
   - The A-11 markers and the A-13 sentences.
   - Add tests for A-10 (open pipe) and A-14.
4. **A-12.** Record the dependency in D-073 and CLAUDE.md. *Done 2026-10-08.*
5. **A-08.** Done: archived 2026-10-07. Restore it before re-running a checker against
   the native receipts.
6. **Native rerun.** After 1 and 2, an operator-authorized native rerun on the final
   bytes (A-07). *Done 2026-10-13 (see Status). A-01's reopened forms still have no
   native runner.*

The open backlog, as of 2026-10-12:
- F-13: no runner-generated run id binds every case.
- F-26: workers the adapter launches still have no claim-renewal route. It is a
  design choice: a PostToolUse hook in the adapter's gate contract (which changes the
  strict hook settings both harnesses are admitted on), or renewal from the paths in
  observed native events. Either needs the native rerun.
- F-36: the provider timeline is used by `check-interrupted` only (see the report row).
- F-35, F-36, F-37: the runner side is compiled, not run, until A-07.
- F-31, F-33 and F-37 are closed hermetically (report rows).

*Updated 2026-10-13:*
- F-26: route B is decided, renewal from observed native completion events, with the
  controller the only lifecycle owner and the gate contract unchanged. Implementation
  is in progress; it needs its own native check on the retained versions.
- F-35, F-36, F-37: the runner side ran natively in A-07, and the checkers pass on the
  fresh receipts. F-36's remaining recounts, F-35's broader cleanup and F-13's run id
  stay open.

## Where the raw results are

The 2026-10-13 A-07 rerun is archived at
`~/Archives/buddy-system/2026-10-13-a07-rerun/`: `evidence.tar.zst` (sha256
`5cb549c7c6bbc99687cb54cd03b13de00317e9a01962246dc9e0b1d4cc1a96fa`, in
`evidence.tar.zst.sha256` beside it) and an uncompressed `compact/` copy of its
summary, README, `runs.jsonl` and drivers. The compact repo copy, with both digests
of its summary, is `docs/verification-evidence/2026-10-13/`.

The 2026-10-12 work is archived outside the repo, each directory with a
`SHA256SUMS`:
- `~/Archives/buddy-system/2026-10-12-a02-narrow-contract/`: the A-02 narrow
  contract's Codex prompts and verdicts, mutant lists and per-mutant logs
  (`a02n-results.tar.zst`), and the F-31 mutants (`f31-mutants.tar.zst`).
- `~/Archives/buddy-system/2026-10-12-a01-rereview/`: the A-01 re-review's
  prompts, verdicts and kernel probes.
- The originals of the 22 evidence files whose home paths were replaced are in
  `~/Archives/buddy-system/2026-10-07-orchestrator-verification/repo-evidence-unscrubbed.tar.zst`.

The 2026-10-11 A-01 mutation run (session `s-0f653d6a`) is under
`/private/tmp/claude-501/…/0f653d6a-0a6f-4658-997d-2dbacd0a9f8b/scratchpad/a01-mut2/`:
`wE.log` and `wF.log` (the 40 runs), `kill2.log` (the four regressions killing
their mutants), `braceb.out` (`brace-budget` alone, bounded) and
`../hermetic.log`. It does not survive a reboot and is not archived.

The audit's own results are under the auditing session's scratch directory,
`/private/tmp/claude-501/…/c953a5cd-e58f-48c7-8cf2-6ace82255f90/scratchpad/`,
which does not survive a reboot. It is also in the A-08 archive at the same path
relative to `/private/tmp`, together with Codex's review files:
`codex-ownership-prompt.md`, `codex-ownership-verdict.md`, and the raw
`codex-ownership-out.txt`.

- `mut/`: adapter mutant replay (`adapter-replay.json`, `replay.out`), the
  group-kill catch-rate runs and `trap-armed.diff`.
- `mut3/`: the mutation check of the tests landed here (`results.json`, `logs/`).
- `gate-fork/`: gate/mail replay, the 84-form shell probe (`probe-summary.txt`,
  `probe-tree/internal/cli/zz_probe_test.go`), the open-pipe measurement.
- `adapter-fork/`: root-mutant replay and the resume-after-SIGKILL experiment
  (`exp/cmd/buddy-adapter/zz_fork_resume_test.go`, `resume-exp.log`).
- `scripts-fork/`: script mutant replay and reconstructed definitions.
- `fable-ownership/`: the ownership experiments (`INDEX.txt` maps them).
