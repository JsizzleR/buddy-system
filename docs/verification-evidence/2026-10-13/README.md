# Native rerun on the final bytes, 2026-10-13 (A-07)

Read [the report](../../CODEX-ORCHESTRATOR-VERIFICATION-REPORT.md#native-rerun-on-the-final-bytes-2026-10-13)
and [the audit](../../CODEX-ORCHESTRATOR-AUDIT.md) (A-07) for what this does and does
not settle. This directory is compact evidence from one operator-authorized run; it is
not a release bundle or a native admission record, and it holds no raw receipt.

**What ran.** The 16 native suites of
[2026-10-07](../2026-10-07/native-summary.json), `qualify-claude-jobs.py` (V-12, twice),
and the 17 extra native path cases the report lists, regenerated from the current
runners. Source: HEAD `316b337`, clean tree before and after every step. Built with
go1.27.1 at `vcs.revision` 316b337, `vcs.modified=false`: buddy
`d1cfb880baa728816dd633d86dcf2e9bb776f36ce72918f6ef482b9c6de3f290`, buddy-adapter
`e6ee01081155883951d0865ceb4e7ad98c4804e60441ea80f7c4db6e7ce341be`. Runners that build
their own binaries from the captured manifest produced the same two digests, and all 20
manifest-bearing receipts carry the one source manifest `ee1a4482caeb3c74…`.

Retained inputs, each hash re-checked: Claude 2.1.291
(`9a1d2ed6bb4421e8…a056690`, extracted from the 2026-10-07 A-08 archive after that
archive's own sha256 verified), Codex 0.160.0 (`112fae7a5a1223e6…f1b4b`), Claude Agent
SDK 0.3.202 (package.json `c4f86b54930b68cf…f2c7e9`). Every fixture used its own temp
git repo, ledger and configuration and a deterministic loopback provider. No real
provider, installation, global settings or hook-trust change, repo edit or commit was
part of the run. Fixture evidence only, not production proof.

## Results

Every suite passed on its first attempt. "Checker" is the independent `check-*.py`
validating the fresh receipt; "rejected" is its receipt self-test's count of
damaged-evidence cases refused, with its positive control passing.

| Suite | Checks | Checker | Rejected | 2026-10-07 checks | 2026-10-07 rejected |
| --- | ---: | --- | ---: | ---: | ---: |
| claude-mods | 38/38 | pending | — | 33/33 | — |
| claude-peers | 50/50 | `check-claude-delivery.py` 30/30 | 60 | 50/50 | 54 |
| claude-resume | 22/22 | `check-claude-resume.py` 22/22 | 21 | 22/22 | 21 |
| claude-interrupted | 98/98 | `check-claude-interrupted.py` 98/98 | 86 | 98/98 | 86 |
| claude-compaction | 26/26 | `check-claude-compaction.py` 26/26 | 30 | 26/26 | 30 |
| claude-endpoints | 38/38 | `check-claude-delivery.py` 19/19 | 32 | 38/38 | 32 |
| recovery | 29/29 | `check-recovery.py` 29/29 | 31 | 26/26 | 26 |
| interrupted | 59/59 | `check-interrupted.py` 59/59 | 54 | 52/52 | 46 |
| uncertain | 42/42 | `check-uncertain.py` 42/42 | 44 | 40/40 | 38 |
| compaction | 23/23 | `check-compaction.py` 23/23 | 34 | 23/23 | 34 |
| interrupted-compaction | 75/75 | `check-interrupted-compaction.py` 75/75 | 90 | 75/75 | 90 |
| claude-history | 130/130 | `check-claude-history.py` 130/130 | 175 | 130/130 | 175 |
| mixed-fleet | 37/37 | `check-mixed-fleet.py` 37/37 | 41 | 37/37 | 41 |
| claude-interrupted-compaction | 113/113 | `check-claude-interrupted-compaction.py` 113/113 | 222 | 113/113 | 222 |
| codex-shell-lifecycle-edge | 15/15 | pending | — | 15/15 | — |
| standalone-adapters | 9/9 | pending | — | 9/9 | — |

- The higher counts (claude-mods, recovery, interrupted, uncertain, peers' rejections)
  follow the 2026-10-12 runner and checker changes: F-35's forced-signal record in the
  Mods and peers runners, F-36's provider timeline, F-37's file, build and executable
  binding. With F-13's and F-33's suite binding, those tightened checkers pass on
  receipts the current runners wrote; until now they had run on synthetic evidence only.
- **Pending:** claude-mods, codex-shell-lifecycle-edge and standalone-adapters have no
  independent checker yet. Their counts are the runner's own, and nothing here claims
  more for them.
- claude-history's receipt nests its checks twice (260/260 counted recursively); the
  checker's recount, 130, is used, as on 2026-10-07.

**claude-jobs (V-12): RED 3/4 on both attempts**, failing only
`native_tool_job_exited_on_close`. That criterion predates the narrow contract of
2026-10-12 (D-077), which deliberately does not end an escaped job. The contract itself
held natively both times: the Bash tool shell (4515, its own group, parent the native
`claude`) and its python job (4516) left the worker's group, were captured by the
close-time scan, recorded in `<receipt>.proc`, reported on one adapter stderr line
(`2 escaped descendant(s) captured, 0 ended, 2 still running … not signalled`) and
were not signalled. The adapter exited 0 with no fallback kill; the fixture then ended
only the identity-matching job group. The suite checks neither the `.proc` record nor
the report; its criteria are being updated separately.

**A-01 native cases, all passing:** `apply_patch <<'EOF'` through Codex's
`exec_command` arrived as a Bash hook and was DENIED (inside shell/lifecycle/edge
15/15); `bash -lc '<heredoc>'` DENIED, 7/7 (6/6 on 2026-10-07, from a stale fork);
Codex paths 10/10; Claude Write paths 6/6. The forms the 2026-10-12 A-01 re-review
left open (`env -S` jurisdiction paths, `script` with no command, an over-budget brace
word as an option value, fish) and the decided-open `exec <<'SH'` have no native runner,
and none was run.

**Q2, Q4, Q5: not observable** with the existing runners (observation only). `Setsid`
is not built, and Q1/Q3 were not run. Details in `summary.json` → `q_observations`.

## Files and digests

- `summary.json`: the run's summary (the 2026-10-07 `native-summary.json` shape, plus
  a sha256 for every input, log and receipt, and each row's 2026-10-07 counts). The
  only change from the run's file is that each `/Users/<user>` became `~`, so the
  private-terms check passes. Both digests, so neither is mistaken for the other:
  - **as committed here (scrubbed):**
    `7e96e9a3380296a6f962854c0516e3df9765ad3ca3921b6059b045eff86a1b9c`
  - **as the run wrote it (unscrubbed):**
    `42cbb7fde2e20e09b96f7d50632b9e95c7ad96fab4d52f7be319ff83147f3a95`, the digest in
    the archive's `compact/summary.json.sha256`; the archived copy matches it.
- The receipt, log and fixture paths `summary.json` names under `/private/tmp` are
  **ephemeral**. The raw evidence (receipts, logs, drivers, generated cases, the
  extracted Claude binary, `runs.jsonl`) is archived at
  `~/Archives/buddy-system/2026-10-13-a07-rerun/evidence.tar.zst`, sha256
  `5cb549c7c6bbc99687cb54cd03b13de00317e9a01962246dc9e0b1d4cc1a96fa` (recorded beside
  it in `evidence.tar.zst.sha256` and re-checked), with an uncompressed `compact/` copy
  of the summary, README, `runs.jsonl` and drivers.
- A first pass of the 17 extra cases failed in 0 s from a driver bug (relative paths in
  its variants file). It is kept in the archive and not counted; the regenerated rerun
  is what is reported.
