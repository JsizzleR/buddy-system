# Verification evidence, 2026-10-07

Read [the report](../../CODEX-ORCHESTRATOR-VERIFICATION-REPORT.md) for disposition
and limitations. This directory preserves compact evidence from the authorized
verification; it is not a release bundle or native admission manifest.

- `checks.json`, build/vet/hermetic logs: executed repository checks.
- `source-before.json`, `source-final.json`: per-file SHA256 manifests. The final
  manifest excludes this evidence directory to avoid recursive hashes.
- `root-*.json`, `root_*.py`, `V*.log`: independent root mutations and watched
  fail-before/pass records.
- `gate/`: summary, exact mutation diffs/drivers, latency samples, snapshot and
  selected regression output. `worker-summary.json` contains final dispositions.
- `adapter/`: ordered old/current probe results, mutation manifests/drivers,
  supplemental test text, fixture settings and selected logs. Its report states
  all surviving mutants and initially ineffective tests.
- `native-summary.json`: exact executable/artifact hashes, commands, receipts,
  check counts, preserved failures and uncompleted cases. `native-inputs/` saves
  definitions of the additional local fixtures as text.
- `reviews/` and `reviews.json`: original prompts, final verdict text and hashes
  of complete source-review logs. A verdict is source evidence only.
- `evidence-sha256.json`: digests of these durable files, excluding itself.

**Home paths replaced, 2026-10-11.** Committing this directory, the repository's
private-terms check refused the machine's home directory, which 22 files here
named 70 times. Each `/Users/<user>` became `~`, and nothing else changed. So
`evidence-sha256.json` no longer matches those 22 files or this README. It
matches the originals, which are kept byte-for-byte in
`~/Archives/buddy-system/2026-10-07-orchestrator-verification/repo-evidence-unscrubbed.tar.zst`
(sha256 `78bad8da…6106`, beside the A-08 archive; a test restore compared equal).
The mutation drivers' `~` paths are records, not runnable paths.

Raw transcripts, complete per-mutant logs and consumed native receipt artifacts
remain in `/private/tmp`; their locations are recorded in the summaries and
**may disappear on reboot**. A hash does not reconstruct a missing artifact.
Selected logs are retained here; the complete native evidence was not copied.

Mutation drivers record exact replacements and test selectors. They were
written for named private source copies and are not a portable one-command
suite: inspect their paths and bootstrap a disposable copy from the appropriate
manifest/snapshot before replaying them. Never point them at the shared tree,
installed binaries or real Buddy ledger. Later fixes may deliberately change
the strings they mutated; a failed replacement/build is not a mutation kill.

Native reproduction requires the named retained versions, disposable state,
loopback permissions and the operator's authorization. The regrouped-job
fixture is expected to remain red until its measured runtime limitation is
resolved; its exact-identity cleanup is not a passing adapter result.
