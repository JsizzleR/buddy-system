# Review findings: Codex orchestrator implementation, 2026-10-06

**For:** the Codex driver continuing [CODEX-ORCHESTRATOR-PLAN.md](CODEX-ORCHESTRATOR-PLAN.md).
**From:** a Claude Code session, at the operator's request ("double check the work
already done ... any bugs or oversights?").
**Scope:** the uncommitted working tree on `6938fbe` as it stood on the evening of
2026-10-06. That covers Steps 1-4, from `internal/cli` patch/identity/lifecycle
through `internal/store` schema 15, `internal/harness`, `cmd/buddy-adapter`, the
qualification scripts, and the docs/rules diff. The `history-291` session was
editing the plan, USAGE, the skill and the history scripts during the review, so
line numbers in those files may have moved.

## How to read this

- **CONFIRMED** means a hermetic Go test (or, for F-04, a shell command) fails
  against a fresh `rsync` copy of the *current* tree for exactly the stated reason.
  The reviewer's own copy was not used, because some reviewers had prototype fixes
  applied there. **PLAUSIBLE** means source reading only.
- Native vendor semantics are cited from vendor source or binary inspection
  (Codex `rust-v0.160.1`, Claude 2.1.291) where a finding depends on them. They
  were **not** observed natively in this review. Where a fix relies on one, add a
  native case to the matching `qualify-*.py`.
- Severity: **P1** = fail-open, an approval or authority leak, or a fleet-wide deny.
  **P2** = a real bug, a qualification claim that does not hold, or an unrecorded
  reversal of a settled decision. **P3** = oversight, drift, or a missing gate.
- This is **not** a constrained Codex review (`codex-review.sh`) and not native
  qualification. Report it separately from both, per AGENTS.md.

**Executed on the current tree, all green.** So the suite catches none of the
findings below.

- `go build ./... && go vet ./... && go test -count=1 ./...` passed.
- `sh scripts/check.sh hermetic` passed with **HERMETIC GREEN**; the live leg was not run.

### Probe tests

Every CONFIRMED Go finding has a failing test in [findings-probes/](findings-probes/).
They are saved as `.go.txt` so the toolchain and the hermetic tier ignore them. To run one:

```sh
cp docs/findings-probes/cli_gate_review_test.go.txt internal/cli/zz_review_test.go
env -u CLAUDE_CODE_SESSION_ID -u CODEX_THREAD_ID go test -count=1 ./internal/cli -run TestReview -v
```

| Probe file | Package | Tests |
| --- | --- | --- |
| `cli_gate_review_test.go.txt` | `internal/cli` | F-01, F-03, F-05, F-06 |
| `cli_adapter_mail_probe_test.go.txt` | `internal/cli` | F-12, F-24..F-27 (`TestPW` is F-12) |
| `harness_review_test.go.txt` | `internal/harness` | F-02, F-07, F-10, F-11 |
| `harness_backend_probe*_test.go.txt` | `internal/harness` | F-07, F-08, F-09, F-10, F-21 |

Notes on the probes:

- `TestReviewFixOperatorCloexecControl` is a positive control and passes today.
- The files in one package compile together.
- They are deliberately red. For each fix:
  1. watch its probe fail first;
  2. port it as a real regression, in this repo's naming, with its positive control;
  3. watch it pass;
  4. delete the probe file.

---

## P1

### F-01 An `apply_patch` sent through Codex's shell tool is never checked against claims (fail-open). CONFIRMED gate-side

**Where:** `internal/cli/patch.go` (entry ~L36) and `cmdGate`/`gatePaths` in `cli.go`.

**Codex behaviour** (from `rust-v0.160.1`, not observed natively):

- `exec_command` intercepts a command of the form `apply_patch <<'EOF' … EOF`
  (`core/src/tools/handlers/unified_exec/exec_command.rs:378`). It also accepts
  `applypatch`, and `cd <dir> && apply_patch <<'EOF'`
  (`apply-patch/src/invocation.rs:28,120`).
- Codex then applies that patch **in process**, with the same engine as the tool.
- The PreToolUse payload for it is `tool_name:"Bash"`, with the script in
  `tool_input.command` (`exec_command.rs:520-528`).

**Gate behaviour:** the gate treats `Bash` as pause-only, so no claim is checked.

**Input:** sess-a holds `src`. sess-b (`BUDDY_HARNESS=codex`) sends
`apply_patch <<'EOF'\n*** Begin Patch\n*** Delete File: src/old.go\n*** End Patch\nEOF\n`
through the shell. The gate allows it with exit 0 and empty output.

- The positive control, the same patch through the `apply_patch` tool, is denied.
- All three spellings above are allowed.

**Why it is not a "documented shell side effect":** the patch engine runs the
patch, not a shell. USAGE ("Buddy checks every add, update, delete and both sides
of a move") and SKILL.md:33 ("reads all affected paths from apply_patch")
therefore overclaim.

**Fix:**

- In the gate, deny any `Bash` command that Codex would intercept: an
  `apply_patch`/`applypatch` invocation with a heredoc, or a body containing
  `*** Begin Patch`.
- Make the deny text say to use the `apply_patch` tool.
- Do **not** try to parse the patch and adjudicate it. `exec_command`'s `workdir`
  is not in the hook payload, so relative paths cannot be resolved correctly.
- Add a native case to `qualify-codex.py` that drives the shell form.
- Correct the USAGE and skill coverage text.

**Probe:** `TestReviewShellApplyPatchBypassesClaims`.

### F-02 The native worker inherits the operator approval descriptor. CONFIRMED

**Where:** `cmd/buddy-adapter/main.go:~165`. `os.NewFile(--operator-fd)` never sets
close-on-exec, and it runs only **after** `c.Start` has launched the child
(`main.go:~130`, `process.go:~61`).

**Why the descriptor leaks:**

- A descriptor handed over by a shell redirection, or by Python `pass_fds`
  (`qualify-claude.py:180-185`), arrives without close-on-exec.
- Go's fork/exec passes every such descriptor to the child.
- Measured one level further down: `codex sandbox -- sh -c 'head -n1 <&3'`
  (0.160.1) read the operator's line, so a Codex model's shell tool can read it.

**Consequences:**

- A read-only pipe lets the worker silently consume, and so steal, operator approvals.
- A FIFO opened read-write (`3<>approvals.fifo`, the usual way to avoid blocking on
  open) lets the worker *write* `approve` requests that `OperatorApprove` accepts.
  The model then answers its own approval. This breaks D-070's "coordinator stdin,
  peer text and model output cannot answer approvals".

**Fix:** at the top of `run()`, before `NewController` and `Start`:

1. Check that the descriptor is open with `fcntl(F_GETFD)`. Today a mistyped N
   silently aliases the journal or a native pipe, because those take fds 3 and up.
2. Call `syscall.CloseOnExec(fd)`.
3. Refuse unless the access mode is `O_RDONLY`.

**Probes:** `TestReviewOperatorDescriptorNotInheritedByNativeChild` and
`TestReviewNativeChildCannotWriteOperatorChannel` fail.
`TestReviewFixOperatorCloexecControl` is the passing control.

### F-03 A nested repository or a stale `.git` file hides a home-ledger claim for every tool, including Claude `Edit`. CONFIRMED regression against HEAD

**Where:** `cli.go:~2121-2131`. When `nested`, the code now goes to
`gateForeignRepo` **instead of** `denyIfHeld` on the home ledger.

**Case (a), nested repo without its own ledger:** sess-a claims `vendor`, and
`vendor/lib` is `git init`ed (or is an initialised submodule) with no ledger. A
Claude `Edit` of `vendor/lib/x.go` is now **allowed**. HEAD denies it.

**Case (b), stale `.git` file:** a directory contains a `gitdir:` file pointing at
a pruned worktree. Git says "not a git repository", `resolveRepo` maps that to
`errNoLedger`, and the edit is allowed as feature-off. HEAD denies it. Both `Edit`
and `apply_patch` are affected.

No decision record covers this change to the Claude-side gate.

**Fix:** a home claim that covers the path still refuses. The nested repo's ledger
is consulted **in addition**, never instead. The reviewer's prototype:

```go
path = filepath.Clean(path) // F-06
rel, _, outside := placeInRepo(rc.top, h.Cwd, path)
...
if st != nil && !outside {
    reason = denyIfHeld(st, h, rel, "")
}
if reason == "" && (st == nil || outside || nested) {
    reason = gateForeignRepo(h, env, path, dir, targetKey, targetRepos)
}
```

With that change, F-03 and F-06 go green, and all existing `TestGate*`,
`TestCodex*` and `TestGatePatch*` still pass, including
`TestGatePatchNestedTargetWithoutHomeLedger`.

**Probes:** `TestReviewNestedUnledgeredRepoHidesHomeClaim` and
`TestReviewStaleNestedGitfileHidesHomeClaim`.

### F-04 An inherited `CODEX_THREAD_ID` denies every write on the machine, even outside any repo (invariant 3, the D-044 class). CONFIRMED

**Where:** `readHook` now calls `classifyHook` (`cli.go:~407`), and `cmdGate` denies
on any `readHook` error **before** it looks for a ledger.

**Reproduction** in a scratch directory that is not a git repo, with a Claude
`PreToolUse` `Write` payload whose `session_id` is `claude-sess-1`:

| Binary | Environment | Result |
| --- | --- | --- |
| Working tree | Control, no Codex variables | allow (silent) |
| Working tree | `CLAUDE_CODE_SESSION_ID=claude-sess-1` and `CODEX_THREAD_ID=019a-codex-parent` | **deny** ("$CLAUDE_CODE_SESSION_ID and $CODEX_THREAD_ID disagree … refusing the tool call") |
| Installed `~/bin/buddy` | Same as above | allow |

The reviewer also got a deny for `BUDDY_HARNESS=codex` plus a mismatched `BUDDY_SESSION`.

**Who hits it:** any Claude session started from a Codex shell, such as
`claude --bg` run by a Codex coordinator. Once installed, every Write and Edit
anywhere on the machine is refused. The same applies to a Codex worker started
from another Codex thread's shell, if it inherits the parent's id.

Adapter-launched workers are safe: `process.go` strips the identity variables
from the child.

**Fix:**

- Decide feature-off (no repo, or no ledger at the target) **before** classifying
  identity, so a mismatch denies only where a ledger governs the path.
- Or record the machine-wide deny as an explicit decision, with its reason. It
  currently contradicts invariant 3.
- Have the deny text name the inheriting launcher.
- Add a regression for each environment in the table, outside a repo.

---

## P2: code

### F-05 Deleting or moving a symlink checks the link's target, not the link. CONFIRMED gate-side

**Where:** the gate loop runs `canon` (`EvalSymlinks`) on every patch path.

**Codex behaviour:** `remove` uses `symlink_metadata` and then `remove_file`, so it
unlinks the link itself (`exec-server/src/local_file_system.rs:897-920`). A move
source is removed the same way.

**Input:** `docs/link2 -> ../src/x.go`, sess-a claims `docs`, and the patch is
`*** Delete File: docs/link2`. The gate allows it. The `docs/plain` control is denied.

An `AGENTS.md -> CLAUDE.md` symlink is a common layout, so this is not exotic.

**Fix:** for a Delete or Move source, also check the entry path itself: canonicalise
the parent directory and keep the base name. Do this in addition to the resolved target.

**Probe:** `TestReviewDeleteSymlinkChecksTargetNotEntry`.

### F-06 An absolute path with `..` after a symlinked directory is checked against the wrong file. CONFIRMED (also at HEAD, for `Edit`)

**Where:** `cli.go:~2113`. `canon`'s `EvalSymlinks` resolves `..` *after* following
the link. Codex and Claude resolve `..` lexically.

**Input:** `docs/link -> ../src/deep`, sess-a claims `docs`, and the patch is
`*** Delete File: <repo>/docs/link/../core.go`. Codex deletes `docs/core.go`, but
the gate checks `src/core.go` and allows.

The relative spelling is denied correctly. HEAD fails the same way for an absolute
`Edit`, so this predates the work, but `apply_patch` inherits it.

**Fix:** `path = filepath.Clean(path)` before `placeInRepo`/`gateRepoKey` (in the F-03 snippet).

**Probe:** `TestReviewDotDotThroughSymlinkAbsolute`.

### F-07 Closing the worker, or killing the adapter, leaves the worker's process tree running. CONFIRMED

**Where:**

- `process.go:~38` uses `exec.Command` with no `Setpgid`.
- `process.go:~173` kills only the direct child.
- `main.go` has no signal handling, so SIGTERM, SIGHUP, or SIGPIPE when the
  coordinator's reader goes away kill the adapter without running `Close`.

**What happens:**

- A tool subprocess the worker started (a build, a shell, an MCP server) keeps
  writing in the worktree after `Close`. It already passed its PreToolUse gate.
- After the adapter dies, the journal flock is released while the old native tree
  may still be alive. `--resume` can then attach a second app-server to the same
  thread.

**Why the qualification runs missed it:** their "observed native-child cleanup"
is the fixture's own `os.killpg` of its `start_new_session` group
(`qualify-claude.py:185,241-245`; `qualify-interrupted.py:68`). It is not the adapter's.

**Related paths:**

- `claude.go:787-789,827-829` answers a subagent permission request (`agent_id`),
  or one with no active input, by tearing down the whole transport rather than
  sending a native deny. Every teardown reaches this kill path.
- A descendant that holds native stdout keeps the reader blocked forever after
  `Close`. `done` never closes, the child is never reaped, and Events never closes.

**Fix:**

- Set `Setpgid: true`; on Close, SIGTERM the group, then SIGKILL it.
- `signal.Notify` INT, TERM and HUP into `Close`, and ignore SIGPIPE.
- Close the stdout read end in `Close`.
- Answer unsupported permission requests with a native deny.

**Probes:** `TestReviewCloseEndsNativeDescendants`,
`TestReviewCloseWithDescendantHoldingStdout`, and the child half of
`TestProbeOversizedFrameKillsWorkerAndOrphansChildren`.

### F-08 One native frame over 4 MiB kills the live worker, including during the read-only Codex `reconcile`. CONFIRMED (mechanism)

**Where:** `process.go:~70,91-99`. `bufio.ErrTooLong` leads to `p.Close()`, which runs
`Process.Kill()`.

**Triggers:**

- **Codex:** `codex.go:~359` `ReadTurn` calls `thread/read` with
  `includeTurns:true`, which returns full history (command output, diffs) and
  outgrows 4 MiB on a long thread. The comment at `codex.go:345-349` calls an
  oversized reply "an error"; in fact it kills the worker, possibly mid-turn.
- **Claude (PLAUSIBLE):** a `--verbose` stream-json `user` frame carrying a base64
  image or PDF `tool_result`.

**Fix:** use a bounded line reader that discards an oversized line and fails only
the request it belongs to. For reconcile, use a turn-scoped or paged history read.

**Probe:** `TestProbeOversizedFrameKillsWorkerAndOrphansChildren`.

### F-09 The Claude gate-coverage check reads `matcher` as an unanchored Go regexp. Native Claude does not. CONFIRMED adapter-side

**Where:** `claude.go:~911-934` runs `regexp.Compile(matcher)` and then
`MatchString("NotebookEdit")`.

**Native semantics** (2.1.291 binary): a matcher that matches `^[a-zA-Z0-9_|, -]+$`
is split on `|` and `,` and compared **exactly**.

| Matcher | Adapter | Native |
| --- | --- | --- |
| `Edit\|Write` | admitted, since "Edit" is found inside "NotebookEdit" | NotebookEdit ungated |
| `dit\|rite` | admitted as covering all three tools | matches nothing |
| `(?i)edit\|write\|notebookedit` (not run) | valid in Go | a SyntaxError in JS |

The post-`initialize` `get_settings` recheck runs the same function, so it admits
the same configurations.

**Fix:** accept only `""`, `*`, `.*`, or an exact `|`/`,` list that names all three
tools, and refuse every other pattern. The existing refusal test uses only `^Write$`.

**Probe:** `TestProbeClaudeMatcherSemantics`.

### F-10 A wake refused before anything was written is journalled as `unknown`, so the message can never be woken and Claude resume is refused permanently. CONFIRMED

**Where:**

- `claude.go:~364-379` returns `Delivery{State:"unknown"}` without `ErrNotSubmitted`
  when busy (`c.active != ""`), when `interruptUncertain`, when closed, and for the
  wrong id.
- `codex.go:~276-291` has the same class ("active turn is unknown", text bounds).
- `controller.go:~370-403` journals the result and then replays it from `c.sent`.
- `claude_recovery.go:~94-97` requires every sent wake to be `delivered`.

**Interleaving:**

1. Wake #1 is delivered.
2. Wake #2 is sent while busy. The backend refuses with nothing written.
3. The turn ends and the worker goes idle.
4. Wake #2 is retried. It returns the journalled `unknown` with **no native write**.
5. After reopen, `Resume` is refused: "requires journalled terminal results".

The positive control, without the busy wake, resumes. Waking a busy worker is the
ordinary case.

**Fix:**

- Wrap every refusal before `WriteContext` in `ErrNotSubmitted`.
- Have the controller record `not_submitted` and drop the id from `c.sent`.
- Have journal replay and Claude recovery treat it the way the approval path already does.

**Probes:** `TestProbeClaudeBusyWakeRefusalPoisonsJournal` and
`TestReviewDefiniteWakeRefusalIsNotBurned`.

### F-11 A native event that could not be recorded turns "unknown" into `pending_approval: "observed_absent"`. CONFIRMED

**Where:** `controller.go:~485-505`. `Observe` returns on an `Inspect`/`Event` error
without touching `c.state`, and `receipt()` (`~168-174`) then reports
`observed_absent` from stale state.

**Triggers:** SQLite busy for more than 5 s, `ErrLedgerNewer` after an install
mid-run, or a cancelled context, at the moment an approval event arrives.

**Result:** the approval never enters `c.approvals`, so the operator cannot act on
it, and every receipt says no approval is pending.

**Fix:** after the identity check, any `Observe` failure sets the state to unknown
("native event not recorded") until the next recorded observation.

**Probe:** `TestReviewDroppedApprovalIsNotObservedAbsent`.

### F-12 `buddy sent` and `buddy msg` offer the legacy D-039 wake for adapter mail, and that wake cannot deliver it. CONFIRMED in fixture

**Where:**

- `store/adapter.go:~323` (`RegisterAdapterEndpoint` → `registerProc`) puts the
  receiving Claude pid into `session_procs`, the same table the legacy wake reads.
- `cli/sent.go:~173`, `cli/wake.go:~114-131` and `store/inbox.go:~286` do not tell
  adapter rows apart from ordinary mail.

**Input:** adapter message #1 is queued to a logical Claude worker, the worker
runs `adapter endpoint` (pid 400), and `<sockDir>/400.sock` exists.

**Result:** 31 minutes later, `buddy sent 1` prints
`to wake it now: SendMessage … "run buddy inbox"`. The recipient's `buddy inbox`
says `inbox empty`. Plain `msg` to that worker prints a wake address too.

That wakes an adapter worker outside the adapter's journal-serialized wake, which
D-070 rejects.

**Caveat:** the fixture creates the socket at the legacy path. Whether
adapter-run 2.1.29x workers create `/tmp/cc-socks/<pid>.sock` is not established.

**Fix:** have `wakeAddr` refuse any session with a `session_endpoints` row, and
have `Sent` mark adapter rows. Alternatively, stop registering the endpoint pid
in `session_procs`.

**Probe:** `TestPW`.

## P2: evidence and qualification

### F-13 The suite checkers accept a suite spliced from different runs, versions and builds. CONFIRMED

**Where:**

- `check-claude-interrupted-compaction.py:~202-206`
- `check-claude-interrupted.py:~190-194`
- `check-interrupted-compaction.py:~176-180`, which has the same code shape (not run).

Each case is validated against its own root. `check-claude-history.py` has a
`same_contract` guard written for exactly this ("not five individually plausible
cases silently borrowed from different runs"), and it was never ported to these
three.

**Tests:**

- The 2.1.291 interrupted-compaction suite (`oajhz0dk`, buddy `8a920de5`), with its
  `completed` case swapped for the one from the 2.1.290 run (`p_6983yc`, buddy
  `48167455`): **PASSED**.
- A 2.1.289 case spliced into the 2.1.291 interrupted suite: **PASSED**.

**Fix:** port `same_contract` (claude_version, native_versions, source/buddy/adapter
hashes, fixture_sources) to every suite validator, and add a splice mutation to
each self-test.

### F-14 The "rejects N damaged receipts" self-tests never damage the native evidence files. CONFIRMED

The mutations change receipt JSON fields only. None corrupts the wire log, the
journal (`identity.json.events`) or a ledger snapshot.

**Test:** five evidence checks of `check-claude-interrupted-compaction` were forced
to always return True:

- `boundary_observation_and_native_history`
- `single_armed_compact_input_preserves_uuid`
- `native_inputs_and_approval_answers_exact`
- `loss_is_recorded_before_forwarding`
- `native_launch_count_identity_and_controls`

The self-test on the real receipt still exited 0 with `"mutations_rejected": 103`.
The plan's "103 rejected" therefore cannot fail on the checks that read native
evidence. That breaks "mutate every fix and watch the test die".

**Fix:** add corruptions of `native-observations.jsonl`, the journal events and the
snapshot files, each required to fail a **named** check. `contract_self_test`'s
`refuse(..., check)` already works this way.

## P2: decisions, rules and process

### F-15 D-056, D-039 and D-033 are reversed for `buddy-adapter`, and nothing records it

These still say Buddy launches and wakes nothing:

- CLAUDE.md "Buddy launches and ends nothing; `spawn`/`exit`/`expect` cut."
- CLAUDE.md Out of scope: "a supervisor process".
- Charter GIVEN 58 ("buddy launches and ends nothing … Do not propose `buddy spawn`").
- Charter GIVEN 42 ("buddy still wakes nothing").
- SKILL.md "Buddy launches nothing and ends nothing."

D-070, GIVEN 71, the skill and USAGE now describe a repo binary that launches,
resumes, wakes, interrupts and kills a worker.

None of the added lines mentions D-056, D-039, D-033, #20 or "supervisor". A
constrained reviewer now receives GIVENs 58 and 71 side by side, contradicting
each other.

**Fix:** add a decision (or a D-070 amendment) stating the carve-out:

- it is a separate opt-in binary;
- it launches only from argv the operator or coordinator typed, never from ledger
  or peer text;
- why the #20 hazard (a launch reachable from a relayed instruction) does not apply.

Then amend GIVENs 42 and 58, the CLAUDE.md D-056 bullet and the Out-of-scope line,
and the skill's "Running a fleet".

### F-16 "Never background `codex exec`" was narrowed to reviews only, without a measurement

CLAUDE.md (~L104-111) and the charter now restrict the rule to "a review's `codex exec`".

- The wedge measured on 2026-08-15 is a property of `codex exec` under an agent
  harness. Role has nothing to do with it.
- Nothing now forbids backgrounding a worker's `codex exec`, and AGENTS.md does
  not say how workers are launched.
- D-068 records the role split, citing the operator's acceptance of the plan, but
  gives no evidence for narrowing the wedge rule.

**Fix:** keep "never background `codex exec` under an agent harness" unconditional
in Environment gotchas. The role split can stand separately. **Operator:** your
saved memory note still says Codex runs only through `codex-review.sh`. Confirm the
driver/worker role change is what you intend.

### F-17 AGENTS.md writes a standing, phrase-keyed authorization into an always-read file

The last sentence reads "The request to continue the accepted Codex orchestrator
work order authorizes its bounded implementation…". That turns a one-time operator
act into a grant any future session can claim by quoting the phrase.

**Fix:** move it to the plan receipt. AGENTS.md should say only that authority comes
from the operator's request in the current session.

### F-18 Claude recovery admission has no decision record, and several docs still call it unsupported

**What the code does:**

- Claude `Capabilities` reports `resume: experimental`.
- `claude_recovery.go` admits `completed`, `aborted_streaming` and `aborted_tools`.

**What the docs say:**

- D-070 (decisions.md ~L4594): "Claude resume is unsupported". It has not been amended.
- Plan L268 and L925 say Claude resume is unsupported, and L1122 says compaction
  is unsupported. Neither is marked superseded.
- Plan L262-265 (supplied Mod-absence evidence) was superseded by D-071 and is not marked.
- USAGE.md ~L1456 says resume requires a "successful `completed` result",
  contradicting USAGE ~L1307 and the code.
- No decision covers Claude resume/compaction admission or Codex `reconcile`. Those
  are behaviour decisions whose only record is the plan's receipts.

**Fix:** add a recovery-admission decision, mark the superseded receipt lines, and
fix USAGE ~L1456.

### F-19 All the evidence lives in `/private/tmp`

The plan cites 183 unique `/private/tmp` paths, about 5.7 GB of `buddy-*` data.
The "Evidence" sections of D-068 to D-072 point at the plan, which points at
`/tmp`. Nothing durable is in the repo except hashes, and 11 of 12 checker
self-tests need those receipts to run at all (F-33).

**Fix:** either commit the small `result.json` files and checker outputs (for
example under `docs/receipts/`), or state plainly that the evidence is ephemeral
and that the reproducing command stands in for it.

### F-20 Nothing has been independently reviewed since Step 3

Every receipt from about plan L517 on says no constrained Codex or Fable review
ran. That leaves roughly 15k lines and a schema bump resting on the driver's own
inspection. This document is one input, not a substitute.

**Fix:** before landing, run a design pass and a code pass through
`scripts/codex-review.sh`, in the foreground.

---

## P3: code

| ID | Status | Where | Defect, and fix |
| --- | --- | --- | --- |
| F-21 | CONFIRMED (file check) | `claude.go:~891-903` | Go's `encoding/json` folds key case, so duplicates such as `"Command"`/`"command"`, `Async`, `Matcher` and `Hooks` are admitted while native JS reads the lowercase key. Example: `{"command":"true","Command":"<gate>"}` natively runs `true`. An unbounded `timeout` (e.g. 0.001) is also accepted. **Fix:** decode into `map[string]json.RawMessage` with exact, case-sensitive allowlists, and bound `timeout`. Probe: `TestProbeClaudeCaseFoldedKeys`. |
| F-22 | PLAUSIBLE | `claude.go:~836`, `codex.go:~653`, `controller.receipt()` | Approval `Detail` (the raw tool input, up to 4 MiB of model text) is re-embedded in every receipt and every fsynced journal row while pending. It reaches the coordinator unfenced and uncapped, against invariants 8 and 9 in spirit. **Fix:** cap and fence the presented detail, carry a digest after the first receipt, and keep the raw request on the operator path only. |
| F-23 | PLAUSIBLE | `claude.go:~221` vs `~249`, `codex.go:~119` vs `~146` | The executable is resolved twice, once for `--version` and once for launch. `~/.local/bin/claude` is a symlink the auto-updater re-points (it moved to 2.1.292 today), so the two can differ. `NewClaude`/`NewCodex` also accept relative paths, which are checked against the adapter's cwd but executed relative to the worktree. **Fix:** `EvalSymlinks` once, require an absolute path, then check and exec that same path. |
| F-24 | CONFIRMED | `who.go:~219-229`, `cli.go:~3079-3139` | `who`/`status` print `INBOX 0 undelivered` with 3 adapter messages queued (D-027 says "every register"). `msg` omits them from the backlog and says delivery "waits for its next tool call", but no lifecycle beat or raw Codex hook drains the legacy inbox, so that is a prediction D-032 forbids. **Fix:** count adapter rows separately, with a wording arm for sessions that have no legacy drain. Probe: `TestProbeWhoAndMsgHideAdapterMail`. |
| F-25 | CONFIRMED | `store/inbox.go:~163-187` | Legacy `msg --supersedes 1` accepts a correction of adapter message #1. The correction lands in the legacy inbox, which can never show #1, and the adapter inbox shows #1 with no link. **Fix:** refuse when the target has an `adapter_messages` row, or link the correction in the adapter queue. Probe: `TestProbeLegacyCorrectionOfAdapterMessage`. |
| F-26 | CONFIRMED | `lifecycle.go:~183`, `harness/ledger.go:~45`, `store/logical.go:~107` | Lifecycle beats pass no path, so a logical worker's claims never renew. After 40 minutes of patching its file every 10 minutes, `ls` shows it `STALE`, and refusals tell peers the holder "has gone quiet". **Fix:** extract PostToolUse paths (via `gatePaths`) and pass them to `BeatLogical`. Probe: `TestProbeLifecycleHookNeverRenewsClaims`. |
| F-27 | CONFIRMED regression | `cli.go:~1117` | `hello --session X` now reads any non-TTY stdin. Bytes from a script loop give `parse hook input` exit 1 and are swallowed, and an inherited pipe that never closes blocks forever. This was deliberate in the D-069 fix, but it narrows "no-input manual registration" without saying so. **Fix:** treat non-JSON stdin under an explicit `--session` as manual, or document the behaviour in the usage line. Probe: `TestProbeHelloSessionReadsStdin`. |
| F-28 | PLAUSIBLE, pre-existing class | `gateForeignRepo` | Only claims are checked for a target in another repo. `buddy pause all` in repo X does not stop a session whose cwd is elsewhere from editing X. It is still stricter than HEAD. |
| F-29 | PLAUSIBLE | `cmd/buddy-adapter/hook.go:~65-88` | The advisory endpoint step and the safety gate share one 3 s deadline, and `adapter endpoint` is a write transaction with `busy_timeout(5000)`. A slow lock-holder makes the advisory step deny the tool call. **Fix:** give each step its own context, about 500 ms for the endpoint step. |
| F-30 | PLAUSIBLE | `main.go:~139`, `controller.go:~485` | `Observe` has no timeout and runs while holding the controller lock. A hung `buddy` CLI wedges `Handle`, `OperatorApprove` and `Close`, so the adapter cannot exit on stdin EOF. **Fix:** a bounded per-event context. |
| F-31 | residual | hook wiring | A Codex hook wired without `BUDDY_HARNESS=codex` is classified as Claude, so legacy `bye`/`beat`/`idle` can change a logical session without a token. The docs require the marker. **Fix:** have `diagnose` flag Codex hook definitions that lack it. |

## P3: qualification scripts

| ID | Status | Defect, and fix |
| --- | --- | --- |
| F-32 | CONFIRMED | None of the 31 `.py`/`.mjs` scripts runs automatically, and `check.sh` syntax-checks only `*.sh`. Three self-tests are hermetic, need only `python3`, and pass: `test_native_proxy.py` (2.2 s), `mixed_fleet_provider.py --self-test` (0.24 s) and `check-mixed-fleet.py --self-test` (synthetic). **Fix:** add these three, plus an `ast.parse` sweep of `scripts/*.py`, to the hermetic tier. The "every check-*.sh" rule's spirit covers `check-*.py`. |
| F-33 | CONFIRMED | 11 of 12 checker self-tests run only against a native receipt in `/private/tmp`, so a fresh clone cannot run them. **Fix:** a synthetic receipt per checker, as `check-mixed-fleet.self_test()` already builds. |
| F-34 | CONFIRMED (mechanism) | `qualify-claude.py:394`, `qualify-claude-endpoints.py:268`, `qualify-claude-mods.py:292` and `qualify-claude-peers.py:457` run plain `git status --porcelain` in the **real checkout** without `GIT_OPTIONAL_LOCKS=0`. That refreshes the index and takes `index.lock`, so a concurrent commit in another session can fail. The checker's own `git()` already sets it. **Fix:** `git --no-optional-locks status`, or set it in `clean_env`. |
| F-35 | PLAUSIBLE | No runner handles SIGTERM or SIGHUP, so `finally: fleet.close()` is skipped when a pane closes or a harness times out. Adapters (`start_new_session=True`) and native children outlive the runner, and then hang once the loopback provider dies with it. `observed_child_exit` reports a survivor as "unknown" but never kills it, and it sees only the recorded pid. **Fix:** signal handlers that raise `SystemExit`, then `killpg` any remaining group. |
| F-36 | P3 | Several checks trust values the fixture counted itself: `check-interrupted.py:90` `provider_barrier`, `check-recovery.py:141` `job_alive_after_outgoing_exit`, `check-interrupted-compaction.py:82,92`, and `requests_before == requests_after` everywhere. **Fix:** recount from the provider's request log and the wire log. |
| F-37 | P3 | The newest guards were not back-ported. Only `check-mixed-fleet` and `check-claude-history` bind `native-versions.json` and the executable sha. `check-interrupted`, `check-recovery` and `check-uncertain` lack `original_evidence_hashes`. |
| F-38 | PLAUSIBLE | Un-joined daemon threads that write to `p.stdin` (`qualify-compaction.py:58`, `qualify-interrupted-compaction.py:64`) are the same crash class as the shutdown crash already fixed, with a smaller window. |
| F-39 | P3 | `qualify-codex.py` `clean_env()`, which `qualify-adapters.py` reuses, does not set `GIT_CONFIG_GLOBAL=/dev/null`/`GIT_CONFIG_NOSYSTEM`, so the operator's global git config and templateDir hooks apply. `run()` has no timeout. The "hooks untrusted before trust" check and `qualify-adapters.py:79`'s final pass check are `assert`s, which vanish under `python -O`. |
| F-40 | P3 (file mid-edit) | `qualify-claude-history.py:~39-55` patches the proxy source with `str.replace` and never checks that the target was found, so a refactor silently stops the frame drop. It fails closed, but confusingly. That proxy is also missing from `test_native_proxy`'s `PROXIES`. |

## P3: docs and rules

- **F-41 Charter.**
  - GIVEN 69 still calls the mixed fleet unqualified.
  - GIVEN 70 ("no … worker launcher, automatic wake") reads as current next to
    GIVEN 71, which adds both. Scope it to its increment.
  - GIVEN 72 hard-codes version profiles that go stale with every release. Point
    at the code table instead.
  - D-070 and GIVEN 71 list control operations as start/send/inspect/interrupt.
    The code (`controller.go:~331-404`) has inspect, reconcile, send and interrupt,
    plus operator-fd approve. `start` is argv.
- **F-42 Decision practice.**
  - D-071 was widened in place twice, for 2.1.290 and then 2.1.291. Widening
    admission is a decision, and the record is append-only.
  - CLAUDE.md "Decisions already made" has no bullets for D-068 to D-072.
  - The CLAUDE.md repo map lacks `internal/harness` and `cmd/buddy-adapter`.
  - Invariant 12 still says only the process fences `bye`; D-069 adds the
    captured-token fence.
  - `go list -deps ./cmd/buddy` now includes `internal/harness` (and with it
    `os/exec`) through `diagnose`. "Buddy remains an offline coordination CLI"
    still holds behaviourally, but the separation is weaker than described.
- **F-43 Skill size.** SKILL.md grew from 26.1 KB to 36.3 KB (+39%).
  - The new first section, "Harness boundaries" (10.1 KB), is detail for adapter
    implementers, loaded into every lane, and context cost is a first-class
    constraint here.
  - SKILL.md ~L98 starts a sentence in lowercase.
  - **Fix:** move the detail to USAGE and leave a pointer of 15 lines or fewer.
- **F-44 USAGE.**
  - The table of contents (~L14-34) omits "Structured adapters (experimental)"
    and "Offline compatibility diagnostic".
  - The lifecycle table omits `--harness`.
  - The example `--body 'Authorized work brief; peer provenance'` shows a peer
    body asserting authorization, which invariant 4 and D-030 say a message can
    never carry.
- **F-45 Docs gate.** `buddy-adapter`'s flags (`--operator-fd`, `--resume`, …) are
  covered by no docs gate. `TestSkill*` covers only `buddy`.
- **F-46 The plan as a plan.**
  - The status header (L1-72) is one run-on paragraph of accumulated "now …" sentences.
  - The copy-ready instruction runs to about 75 lines.
  - Stale items: L80 "unqualified mixed fleet", and copy-ready's "for the first
    mixed-fleet integration", which has now passed twice.
  - The actual next step (2.1.292 / 0.160.1 admission) can only be found at the
    end of the newest receipt.
  - **Fix:**
    - Replace the header with a Qualified / Open / Next table.
    - Move the receipts to an appendix or a separate file.
    - Keep the copy-ready text to 10 lines or fewer.

---

## Landing risk

- **Get the work onto a branch now.** Three days of multi-session work sit
  uncommitted on `main`, with rule edits mixed in. One bad checkout or stash loses
  all of it. (A stash named "nemotron comment pass (broken)" is already in
  `git stash list`.)
- **Commit only after concurrent editors stop.** Suggested split, each commit green
  on the hermetic tier and the skill tests on its own:
  1. D-068 + D-069: Codex gate, identity and lifecycle; AGENTS.md; the role edits.
     No schema change, so this commit can be installed alone. Fix F-01, F-03 and
     F-04 first.
  2. D-070 + D-071: schema 15, `internal/harness`, `buddy-adapter`. This is the
     single migration point.
  3. D-072: `diagnose`.
  4. Step 4 recovery, with the F-18 decision.
  5. The plan and the qualification scripts.

  `cli.go`'s dispatch table and usage-text hunks are interleaved across commits 1-3,
  so splitting them needs hand-cut `git apply --cached` patches. At minimum, keep
  the commit with no schema change separate from the schema-15 commit.
- **Schema 15 is a one-way door for this machine** (D-037). Every installed
  hook is a schema-14 binary today. The production ledger still reads
  `user_version 14`; this was checked from the file header, without opening it.
  - The first schema-15 verb that touches it (the gate, `lifecycle begin`,
    `adapter inspect`) migrates it.
  - After that, every schema-14 copy DENIES: `~/bin`, the daemon's `bin/`, and any
    other checkout's binary.
  - So land commit 2 only together with `sh scripts/install.sh`, and never run a
    working-tree binary in this repo before then.

## Checked and found sound

This list is condensed. It is here so the next review knows what was covered.

- **`apply_patch` grammar.** It matches Codex 0.160.1's `StreamingPatchParser` line
  by line: header trimming, content lines beginning `*** `, move only before the
  first chunk, empty chunks refused, CRLF, and a required `*** End Patch`. Where
  they diverge, the gate is the stricter side.
  - The 128-path bound counts raw spellings, allows 128 and denies the 129th.
  - An unreadable target or home ledger denies, and target ledgers are consulted
    even when the home repo has none.
- **Identity.** `wait check`, `msg`, `hello` and `whoAmI` refuse disagreeing native
  ids. Codex-marked hooks never bind an anchor process or read a transcript, and
  raw Codex `hello`/`beat`/`busy`/`idle`/`bye` refuse.
- **Schema 15.** The migration is a single transaction with the newer-version
  recheck under the lock, so a schema-14 binary gets `ErrLedgerNewer` and never
  misreads adapter rows.
  - The legacy drains (beat, busy, hello, `inbox`) exclude adapter rows.
  - `MarkDelivered` refuses a batch that contains an adapter id.
  - Sweep removes adapter rows atomically.
- **Lifecycle store.** Every operation is one `BEGIN IMMEDIATE` with the
  `(session_id, incarnation)` check inside it.
  - A concurrent restart has exactly one winner.
  - `EndLogical` is idempotent and never orphans claims.
  - The CLI never recaptures a token.
- **Adapter mail.** Both incarnations are checked inside the queue transaction.
  Fetch is bounded to 20 and fenced, the point lookup is scoped to the recipient,
  and `delivered` is idempotent.
- **`diagnose`.** It never calls `store.Open`.
  - The source is read with `O_RDONLY|O_NOFOLLOW|O_NONBLOCK`, twice, with
    identity comparison.
  - The private copy is opened `immutable`.
  - Input uses `DisallowUnknownFields` and is capped at 1 MiB.
- **Journal.**
  - Intents are fsynced (`F_FULLFSYNC`) before the native effect.
  - `O_APPEND` with an exclusive flock on a close-on-exec descriptor.
  - A torn tail is refused as uncertain, and mixed identities are refused.
  - The `.start` barrier and the receipt are published exclusively, with the
    directory synced.
- **Approvals.**
  - IDs are compared as typed, decoded values, never float64.
  - They are one-shot: the entry is deleted before the write.
  - A reused id with a different fingerprint closes admission before notification.
  - `Handle` has no approve operation.
- **Transport.**
  - Stop is rechecked after write admission, and a deadline cancels a blocked write.
  - EOF or overflow closes stdin before EOF becomes visible.
  - Lock order is the controller, then the backend.
- **Resume classifier.** It fails closed on a missing or unknown
  `terminal_reason`, a missing turn id, a late approval, or a disconnect mid-turn.
- **Child environment.** It strips `BUDDY_SESSION`, `BUDDY_HARNESS`,
  `CLAUDE_CODE_SESSION_ID`, `CODEX_THREAD_ID`/`CODEX_SESSION_ID`, the messaging
  socket and the token.
- **Codex `hooks/list` check.** It refuses competing, async, untrusted or
  hash-mismatched hooks, and any diagnostics.
- **Qualification fixtures.** Their roots come from mkdtemp. `CLAUDE_CONFIG_DIR`
  and `CODEX_HOME` are redirected, trust is written only into the temp config,
  there is no `shell=True`, there are no `pgrep`/`ps` scans, and the providers are
  loopback-only.
  - The input-relay shutdown fix is present in all six stdin=PIPE proxies.
- **Docs.**
  - D-068 to D-072 and GIVENs 69-73 are contiguous.
  - Every relative link and anchor in the ten changed docs resolves.
  - The schema names in the docs match the code.
  - No private terms, `/Users/` paths or credentials were found in changed files.
