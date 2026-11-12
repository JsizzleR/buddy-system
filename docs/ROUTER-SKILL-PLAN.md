# A drop-in model router skill: routes from a user's own past sessions, counts only

Status: phase 2 BUILT (2026-11-12, D-177): `skills/model-router/`, measured in
section 16. Phase 3 is still a plan. Written by the `router-skill` lane (Opus
5.5, xhigh) under orc-20's brief, base main at 03da68d; phase 2 by the
`model-router` lanes under orc-21's.

Sources:
- the operator's request;
- the read-only probe in section 0;
- `ROUTING-PLAN.md` and the prior plans;
- one Fable consultation, folded (section 11);
- one Codex design pass, folded by the successor lane `router-skill-2`
  (section 12).

The Fable cross-check of that fold and one Codex re-verify are in section
13, both folded. The round ceiling is reached.
Phase 2 shipped as D-177, on the orchestrator's defaults for section 14's
decisions (section 14 lists them, for the operator to overturn). Phase 3 is
not a decision: it takes its D-number when it lands.

The register is `ROUTING-PLAN.md`'s. Every part names the failure it answers,
the number it rests on, how to re-run that number, the alternative it refuses,
and what it cuts. A figure not taken says NOT MEASURED. Anything the operator
decides is **OPEN**.

## The operator's request

2026-11-12, about 05:40Z, in orc-20's pane:

1. "this routing also needs to be flexible, or a skill really that introspects
   past sessions, for someone to drop in and have an effective router from day
   1, even if they arent using buddy system for coding, or golang, etc."
2. "this is also critical, so use fable and codex, and effort level on fable
   should be at least high, and opus 5.5 effort should be xhigh"

`ROUTING-PLAN.md` designed routing for buddy fleets from buddy's ledger and
found nothing to route yet: 96 of 96 items ran on one model. This plan answers
a wider question. Can a user with no buddy, no Go and no code workflow drop a
skill in and get useful model routing on day 1, from the Claude Code and Codex
sessions already on their disk?

**The answer, in five lines:**

1. History describes the work well. One pass reads 9.4 GB and sorts about
   10,000 tasks into classes by structure alone (M1-M6). No message text is
   read, in any phase.
2. History cannot pick a model. Cells with two models exist (M6), but the user
   chose which model ran what. Comparing them compares the tasks, not the
   models.
3. On a fleet like this one, the outcome signals that tell a good model from a
   bad one barely fire (M8). Interrupts and rejections need a human watching,
   and agents run until the tests pass.
4. So day 1 is a **declared-assignment router**. The scan drafts the user's own
   current routing (a default, and `assign=` lines for where they already
   split work) for the user to confirm. From then on every pick applies it,
   and protected, review and full-tier work stays on the default. A trial is
   the one thing that can move a route on evidence, and it comes second.
   Phase 2 routes sessions, not subagents (section 5). A single-session user
   without buddy gets the scan, the draft, and a pick only when opening a new
   session.
5. One grammar and one parser: buddy's `route-cap` line, plus narrowing-only
   keys. It should be the same parser that ROUTING-PLAN step 4 reads the
   operator's line with.

## 0. What was measured for this plan

A throwaway probe (`router-probe.py`, Python 3 stdlib, 40,402 bytes, sha256
`1cf1b61f594d8cae…`) ran in this lane's scratchpad. It read every retained
Claude Code transcript and Codex session log on this machine, the Codex state
database and buddy's ledger, all read-only. It printed counts only. No message
text, prompt, tool result, command, path, cwd, project name or session id is
printed, stored or passed. Section 3 says exactly what it reads.

Every figure below is from one run, which finished at 2026-11-12 06:25Z.
Three earlier runs fixed probe defects:
- the `test` builtin counted as a test run;
- a denied call counted as a test run;
- a raw effort string could reach output (Fable F14);
- subagent end states overlapped;
- the M13 join (Fable F12).

**On re-running.** The probe stays in the lane's scratchpad (`probe/router-probe.py`,
with its output `probe/full-run4.txt`; the lane's READY names the full
path). The brief kept it out of the repository.
Keeping both as `docs/findings-probes/router-probe.py.txt`, the precedent five
probes follow, is **OPEN** (Fable F22). The figures are feasibility evidence,
and no route rests on them (section 5).

The window moves anyway. Claude Code deletes transcripts older than its
`cleanupPeriodDays` (30 days by default, unset here). A re-run next month reads
a different month (M2).

```sh
python3 -I router-probe.py --buddy-repo <main checkout> \
  --ledger "$(git -C <main checkout> rev-parse --path-format=absolute --git-common-dir)/buddy.db"
```

| # | figure |
|---|---|
| M1 | **Sources.** Claude Code: 276 project directories, 1,098 main transcripts (4.12 GB) and 1,967 subagent transcripts (2.58 GB), 1,619,562 lines, of which 309,338 `api-request` records were skipped unparsed. Codex: 5,742 rollout files (2.74 GB, 392,622 lines); its state database holds 5,744 threads. buddy: one ledger with 135 claims, 95 carrying `model=claude-opus-5-5` and 40 no token |
| M2 | **Retention.** Claude records: 357,008 in 2026-10 and 234,548 in 2026-11. The oldest main transcript is dated 2026-10-13, 30 days back. Codex records run from 2026-06 to 2026-11 |
| M3 | **Read cost.** One full pass took 88 s wall on a quiet machine (run 2) and 197 s with other sessions busy (run 4). CPU was 75-83 s, and peak RSS 327-352 MB |
| M4 | **Requests by model.** Main threads: `claude-opus-5-5` 124,306 of 130,902 (95.0%), `claude-fable-5-1` 3,204, `claude-fable-5-2` 1,243, `claude-opus-4-7` 817, `claude-sonnet-5-5` 696, `claude-opus-4-8` 636. Subagents: `claude-opus-5-5` 42,820, `claude-fable-5-1` 7,638, `claude-sonnet-5-5` 2,844, `claude-fable-5-2` 1,182 |
| M5 | **Tasks.** 1,097 main sessions: 808 with one human prompt, 186 with 2-5, 103 with 6 or more. They hold 2,585 episodes (a human prompt to the next); 291 of those made no model request (a local command). There are 1,967 subagent tasks. Codex has 5,742 sessions: 5,381 from `codex exec` (review passes, mostly) and 361 from the TUI or the desktop app. Prompt origins: human 2,049, task notification 5,052, scheduled 462, field absent 521 |
| M6 | **Class x model cells,** by structure (section 3), with 13 of 28 main cells at 10 or more tasks. Main episodes: code (opus-5-5 678, sonnet-5-5 19, fable-5-2 10); writing (opus-5-5 533, fable-5-1 21, fable-5-2 12); qa (opus-5-5 341, sonnet-5-5 71, fable-5-1 35); ops (opus-5-5 340, fable-5-1 84); research (opus-5-5 78, sonnet-5-5 19). **5 of 8 classes have two models at 10, and 2 at 30.** Subagents: 14 of 28 cells at 10, and 5 classes with two models at 10; research alone has opus-5-5 618, fable-5-1 360, sonnet-5-5 106, fable-5-2 31. Codex: 12 of 21 cells at 10, and 4 classes with two models |
| M7 | **Effort.** Main requests on opus-5-5: high 120,750, xhigh 3,556. Episodes, high vs xhigh: code 652 vs 26, writing 503 vs 30, qa 326 vs 15, ops 327 vs 13. Codex turns: xhigh 4,943, low 1,389, ultra 447, max 328 |
| M8 | **How often each outcome signal fires,** over the 2,241 main episodes in cells of 10 or more. Interrupts 4. User rejections 10. A policy denial in 968. An execution error in 993. A retry after failure in 832, and a second failure in 224. One file edited 5 or more times in 316. Tests ran in 1,262 episodes and **6** ended on a failing run; in 764 a test ran piped into a filter, which reports the filter's exit status. A commit landed in 1,125 (code on opus-5-5: 634 of 678). The last request stopped on `tool_use` in 233 |
| M9 | **Signals that lie, measured.** Subagent transcripts carry no structured interrupt field: it reads 0, while the harness's interrupt marker shows 4. Of 1,927 subagent tasks, the final record stops on `end_turn` in 64, on `tool_use` in 1,379, and with a null stop reason in 484. Policy denials (buddy's gate and the operator's hooks) fall in 573 of 678 opus-5-5 code episodes. Codex sessions with a human attending do show interrupts: code on gpt-5.6-sol 12 of 33, writing on gpt-6-astra 7 of 36, code on gpt-6-astra 6 of 50 |
| M10 | **Re-open.** Of 991 sessions with edits, 79 (8.0%) had a later session edit one of the same files in the same repository within 72 h; 50 (5.0%) did, excluding 9 hub files. Removed worktrees undercount it. The signal is cut (section 4) |
| M11 | **Protected work.** Of the 2,294 main episodes with a request, 172 still resolve to this repository, and 112 of those edit nothing on ROUTING-PLAN's protected list (opus-5-5: code 5 of 38, writing 96 of 119). Across all repositories, 200 episodes edit a generically sensitive name. This answers, in part, ROUTING-PLAN's open "how many items a week" |
| M12 | **Coverage.** 1,074 of 1,097 main sessions carry the harness's `cost-state` record. The dollar figure is left out: it is the operator's profile, and the design needs only the coverage. `permissionDecision` is on 0 of 157 user records before Claude Code 2.1.296, and on 7,380 of 8,299 at 2.1.296 (tool results carry it; prompts do not). `interruptedMessageId` is written only when it fires (0 to 8 records per version), so its coverage cannot be computed (Fable F10) |
| M13 | **Is the structural class right?** The join covers 40 buddy items whose slug is exactly one session's label (a lane is labelled by its item). The class word is matched in SQL, so no description byte reaches the probe. Declared `code`: 24 code, 7 writing, 1 ops. `docs`: 2 writing, 1 ops. `research`: 2 writing. `test`: 1 code. **Agreement: 27 of 38 (71%)**, reading docs as writing and test as code; 2 items carry no class word. The first join, by holder, was wrong. Most items end held by the coordinator they were handed to, and under the producer join 92 of 104 sat on a session holding several items (Fable F12) |

**What these say, before any design:**

1. **The history is large and cheap to read.** 30 days of Claude Code and five
   months of Codex is about 10,000 tasks, read with no dependency (M1-M5).
2. **Model variety exists, but it is confounded.** Five classes hold two models
   at a floor of 10 (M6). Those models ran by role: Fable for reviews and
   Sonnet for side work, both the operator's rules. They also ran by period
   (`fable-5-1` main sessions predate `fable-5-2`) and by project. No pair of
   cells was randomized.
3. **An autonomous fleet silences the human signals.** 4 interrupts and 10
   rejections in 2,241 episodes (M8). Agents end on a green test (6 of 1,262
   end red), and many pipe the test through a filter. The signals that do
   fire measure the gate (M9) or the edit-test loop.
4. **The user's practice is a route already.** qa ran 71 times on a second
   model (M6), and effort is a split too (M7). The day-1 router writes that
   practice down for the user to confirm or change.
5. **The subagent transcript is the weaker instrument** (M9). It has no
   interrupt field and no usable end state.

## 1. What the skill is, and what it is not

A skill directory: `SKILL.md`, one Python 3 stdlib script `router.py`, and a
`reference.md`. The script has these verbs:

- `scan` reads the user's past sessions and writes a counts-only routing
  table;
- `cap-template` drafts a `route-cap` line from the user's own history, for
  the user to edit;
- `pick` prints the model and effort for one task, from the cap and the table;
- `report` (phase 3) compares a trial cell with its baseline;
- `--self-test` runs the fixtures.

**It never launches, wakes, ends or configures anything.** It prints. A
harness, a coordinator or the user acts on what it prints, as in ROUTING-PLAN.

**Refused, in every phase:**

- **reading message text** (section 3);
- **a learner or a fitness score.** BEYOND-CODE 5.3 puts a learner's floor near
  30 items per model per class, and M8 says this machine's signals could not
  train one;
- **a pick across harnesses, and in phase 2 any Codex model id.** Claude vs
  Codex changes the tools, the permission model and the instrument that
  measures the outcome (ROUTING-PLAN F19; `AGENTS.md`'s per-run authorization;
  Fable F20);
- **a pick outside the user's cap, or without one** (section 6);
- **editing settings, the cap, `cleanupPeriodDays`, or any hook.**

## 2. Sources: each optional, each detected

| source | detected by | yields | read cost (M3) |
|---|---|---|---|
| Claude Code main transcripts, `<config>/projects/<p>/<s>.jsonl` | the directory exists. `<config>` is `--claude`, else `CLAUDE_CONFIG_DIR`, else `~/.claude` (Fable F19) | per request: model, effort, token usage, stop reason. Per tool call: tool name, plus a path input reduced to an extension label and a salted in-memory hash. Per result: `is_error`, `permissionDecision`, `toolDenialKind`. Per prompt: `turnOrigin`, `interruptedMessageId` | 4.12 GB, most of the pass |
| Claude Code subagent transcripts, `<s>/subagents/**/agent-*.jsonl` | the same walk | the same, minus interrupts and end state (M9); the Agent call's `model` alias joined to the model the subagent ran on | 2.58 GB |
| Codex rollouts, `<codex>/sessions/YYYY/MM/DD/*.jsonl` | `--codex`, else `CODEX_HOME`, else `~/.codex` | per turn: model, effort, sandbox policy. Per command: exit code, and Codex's own `parsed_cmd` type. Per file change: the extension label. Aborts with their reason. Cumulative tokens. Printed as history only (section 1) | 2.74 GB |
| buddy's export, `model-report.sh --export <tsv>` | `--buddy-export <tsv>` | items with their declared class, tier, outcome, reds and effort, and a protected flag (ROUTING-PLAN step 2) | one small file |

The Codex state database is cut (Fable F24). It was a cross-check only, its
`source` column holds JSON rather than an enum, and three of its columns are
text.

**Rules the scan keeps:**

- **"Absent" and "unreadable" are different verdicts,** in invariant 3's shape.
  - A source directory that does not exist is OFF. The scan says so in its
    print and in the table header: `claude=absent`.
  - A source that exists but cannot be read is a refusal. That covers an
    EACCES, a file that vanishes mid-read, and ANY malformed line before a
    file's last (Codex C7). Malformed means not one complete JSON object. A
    threshold such as "more than 1%" would let two torn lines drop a trial's
    only rejections without a word. A torn LAST line is skipped and counted
    in the header, because a live session is still writing it. (The probe saw
    no torn line in 1.6M, so refusing costs nothing measured.)
  - A refusal needs a way out, because Codex rollouts do not expire (M2): one
    torn interior line would refuse every scan for good (cross-check X5).
    `scan` writes the refused files' paths to `refused.txt`, mode 0600, in its
    state dir. The print names that file and never a path in it. The user
    repairs or moves the files it lists.
  - On a refusal, `scan` names the source and a file count, never a path. A
    path would carry a project name and a session id (Fable F16). It writes
    no table and exits 2.
  - There is no partial table (`--allow-partial` is cut, F24).
- **A pick that needs a source requires it present.** A `roles=main` trial,
  phase 3, needs the Claude main source, and the table header must say
  `present` for it. An absent source can never read as "the trial cell is
  empty, keep going" (F19).
- **Version-dependent fields come with shipped version ranges** (F10). The
  script ships, for each field it reads, the harness versions it has seen
  the field in. A record from a version outside a field's range makes that
  signal "not comparable" in its cell. That holds for event-only fields too,
  whose presence cannot be measured as coverage.
- **The record format is the harness's internal one, not an API.** Every field
  was found by key name on this machine: Claude Code 2.1.280 to 2.1.322, and
  Codex 0.16x. Phase 2's fixtures pin the shapes it reads.
- **Deduplication.** A request counts once by message id. A record counts once
  by `uuid` across files, because a resumed session copies its history (112
  duplicates here). `api-request` records are skipped before parsing.
  - Two copies count as one only when every field the scan reads is equal
    (Codex C7).
  - A CONFLICTING pair (the same id, a field the scan reads different) is
    left out of every cell and counted in the header as `conflicts=<n>`.
    `report` abstains while `conflicts=` is above 0, because the conflicting
    field may be the one that places the task in a cell or a window (X6).
    One number cannot say which window a conflict falls in.
  - How many of the 112 conflict is NOT MEASURED. Phase 2 measures it.
- **Scope.** The default reads the machine's whole history, because routing is
  the user's question, not one repository's. `--cwd-under <dir>` scopes the
  scan in memory, and the header records only `scoped=yes`.

## 3. A task, and its class, without buddy's claim tokens

**The units:**

| unit | from | why |
|---|---|---|
| episode | one main-thread human prompt to the next. A prompt is `turnOrigin=human`, or the first prompt-like record. Task notifications, peer messages and scheduled wakes continue the episode | the user's one request. A lane launched with one brief is one episode (808 of 1,097 sessions) |
| subagent task | one subagent transcript | its model is fixed for its life. It is what a subagent pick would choose, and phase 2 has none (section 5) |
| buddy item | one claim with `class=` (from the export) | the declared class and the ledger's outcome, preferred when present |

**The class, from structure only.** The rule reads the task's tool mix.

- A file edit is counted by its extension's label, from a fixed table: code,
  prose, data, config, media or none.
- A shell command is reduced to fixed program labels: vcs, commit, test,
  build, pkg, net, data, ops, read and script. Codex's own `parsed_cmd` type
  stands in where it has one.

The first rule that matches decides:

1. no tool calls, or at most 2 calls with no edit: **qa**;
2. edits that are mostly to code extensions (any language, plus `Makefile` and
   `Dockerfile`): **code**. Mostly to data files: **data**. Mostly to prose:
   **writing**. Only to config: **ops**;
3. no edits, and one of these shares of calls, tested in this order:
   - Agent calls 30% or more: **delegate**;
   - web or browser calls 30% or more: **research**;
   - data programs or data reads 30% or more: **data**;
   - vcs, ops, package, network or build programs 40% or more: **ops**;
   - reads and searches 40% or more: **research**;
   - a test run: **code**;
4. otherwise **unknown**, counted and never routed.

The scan sets one more class apart. **`review`** is a read-only task on a
model the cap names in `review=`. In session, the model names any review,
security lens or audit as `review` before it runs (section 7). A review is
never routed: `pick` answers `no pick: review`, and the user's instructions
name the reviewer (D-111, D-117; Fable F2).

The taxonomy is domain-agnostic. A novelist's session is `writing`, an
analyst's `data` and a sysadmin's `ops`, whatever the language. Here, 8 of
2,294 main episodes with a request were `unknown`.

**Structure measures the activity, not the intent** (M13: 71% agreement with
the declared class). A `code` item in this repository carries its decision
record and its skill edits, so 7 of 32 read as `writing`. A `research` item
writes a plan, so it reads as `writing`. Three consequences:

- **Where buddy's export exists, the declared class is used.** The two class
  words never share a cell.
- **There is one vocabulary.** The parser reads buddy's `docs` as `writing` and
  `test` as `code`; those are the only synonyms.
- **Without buddy, the activity class is the class.** The report prints every
  non-default task in its window by scanned class, so a task the model named
  `writing` that edited code is visible (Fable F5).

**Message text: none, in any phase.**

The probe compared two harness-written prefixes in memory and kept them only
as booleans:
- the interrupt marker, cross-checking the structured field;
- `<tool_use_error>`, splitting validation errors from execution errors.

The shipped script does neither. Everything it uses is one of three things:
- a structured enum or number field;
- a tool name;
- a tool INPUT that the model wrote (a path or a command), never a result,
  reduced in memory to a label from a fixed table.

**The closed vocabulary holds both ways (F14, Codex C1):**
- efforts, classes and labels come from fixed lists;
- versions are parsed as numbers;
- a model id has no fixed list, because the user's models are the data. So it
  passes a syntax gate first: 1 to 64 bytes of `[a-z0-9._:@-]`, containing
  `claude`, after a recognized `@<effort>` suffix is split off. The `@` keeps
  a cloud id such as `<name>@<date>` whole (section 6; X7). Anything else is
  `(other)`, whatever its count. A Codex id folds to `(other)` too, which
  phase 2 wants (section 6). No space, quote,
  newline or control byte can pass, so no sentence can either;
- an id that passes is printed by name only when the cap names it. Every other
  id prints as `(other)`. Repetition is not approval: ten planted transcripts
  repeating one id gain nothing (Codex C1, against F14's count floor);
- the one exception is the user's draft. Before a cap exists, `scan`'s print
  and `cap-template` name ids that pass the gate and lead 10 or more of the
  tasks the table keeps (main and subagent, in scope, not dropped as a
  conflict), at most 8 of them, the most-used first, since the user needs
  them to write the cap. CHANGED in the fold (panel F1): the floor first
  counted every task, so one file could hold all 10, and nothing bounded how
  many ids a planted store got named. The table names ids by the same rule,
  and its reader enforces it (code pass B1). A pick or a report reads only a
  table scanned under its own cap (rule 4 checks the cap's sha256), so a
  draft-era id never reaches either.
  **Residual:** a writer of the transcript store can plant enough tasks for a
  gate-passing token, such as a hyphenated phrase, to be named in the draft,
  and to lead the most main tasks and so set its `default=`. The draft is
  undated data, which parses as no cap until the user dates it, and the
  SKILL.md has the model show it as data and act on none of it;
- every printed id sits in one fixed-width column on one line, invariant 9's
  shape.

The table and the export are validated the same way when they are READ, and
a value outside the vocabulary makes the file unreadable. An exception prints
only its type, never its message, which can quote the value that caused it
(`subagent-report.py`'s rule; F15). So nothing a transcript says, an injected
instruction included, reaches the table or the model that reads it.

**The refused alternative** is an opt-in text mode, for example classifying by
the prompt's words. It would buy intent ("fix the auth bug") at the price of
four conditions: opt-in, local only, fenced, never quoted. A transcript is the
user's whole session, and the skill's output lands in a model's context, the
same risk `transcript.go`'s header names. M13 says structure gets 71% without
it. The mode is cut, not deferred.

## 4. Outcome signals: "did that model do well"

**Rule inputs, the only signals that can change a verdict (Fable F7):**

| name | computed | fires here (M8, M9) | how it lies |
|---|---|---|---|
| `interrupt` | `interruptedMessageId` on a main-thread user record | 4 of 2,241 main episodes; Codex TUI 12 of 33 | it needs a human watching, so an autonomous lane is never interrupted. Users also interrupt to redirect, not only on a failure. It is absent on subagents, and event-only, so it is judged by the version ranges (F10) |
| `user_reject` | `toolDenialKind=user-rejected` | 10 of 2,241 | zero under auto or bypass mode, so it measures the permission setting; it rises with the user's caution |
| buddy `fail`, `rework`, `reds` | the export (ROUTING-PLAN sections 5 and 6) | ROUTING-PLAN M3, M4 | ROUTING-PLAN section 6. Where the export exists it is the best signal there is |

**Printed beside every rate, never compared:**

| name | computed | fires here | why only printed |
|---|---|---|---|
| `calls/task`, `tested` share | tool calls per task; share of tasks with an executed test | 1,262 of 2,241 tested | the guards. A model that does less looks better on every rule input that counts failures (F7) |
| `test_red_end`, `test_masked` | the last executed test failed; a test piped into a filter | 6 red; 764 piped | agents run until green; a piped test reads green whatever happened |
| `policy_denied` | a hook, rule or classifier rejected a call | 968 of 2,241 | it measures the environment: buddy's gate refuses by design |
| `abandoned` | the last request stopped on `tool_use` | 233 of 2,241 | a coordinator closing a pane looks the same, and the live session is cut off mid-turn (F9) |
| `tokens` | output tokens per task, by model | always | prices differ between models; subagent tokens are billed apart |

**Cut (Fable F8, F24):**
- `retry`. The probe keyed a shell call's target on its first two program
  labels, so a grep with no match followed by an `ls` counted as a failed
  retry, and the thorough model scores worse;
- `churn`, `commit`, `exec_error`;
- `reopen`. It opens `.git` in every recorded cwd and still undercounts;
- the session dollar figure.

**The confound runs against the strong model.** Under every signal, a hard
task looks like a bad model, and the user sends hard work to the stronger
model. History read naively would route hard work AWAY from it, the failure
the brief names first. The design answers it three ways:

- **History never chooses between two models** (section 5).
- **A transcript-only report never prints `no worse`.** It prints `worse:
  <signal>`, `not shown worse` or `inconclusive` (F7). `no worse` needs buddy's
  `fail`/`rework`/`reds`, as ROUTING-PLAN has it.
- **The guards.** A cell whose `calls/task` or `tested` share differs from its
  baseline's by more than half is reported `not comparable`. This is
  Goodhart's case: a session that skips its tests has no red test to show.

## 5. The cold start, and the pick rule

**What history supplies on day 1, and what it does not:**

- the user's classes and their weekly volume;
- the baseline rates on the user's default;
- which signals are live for this user;
- where the user already splits work across models or efforts, printed as
  `confounded: chosen per task`;
- and, from that last item, an undated `assign=` draft for the user to
  confirm (section 6).

None of these is a pick the router made. The confidence it states is the
cell's count beside every rate, and `(below floor)` under 10.

**The shipped priors table is cut** (F24). It only ordered suggestions, and
the cap is already the editable prior: the user opens what they choose.
**Trial suggestions are narrow** (F6). A suggestion names only:
- a `qa` or `writing` class, where a weaker model's miss shows at once;
- a class whose eligible volume reaches the floor within the window;
- the cap's own `trial=` id.

There is no effort suggestion and no invented arm.

**The pick rule.** Precedence runs top-down, and the first matching rule
answers (F17):

1. **No cap.** `--role sub` in phase 2 (below), no cap line for the task's
   role, a broken or expired one, or a cap file inside any git work tree:
   `no pick: <reason>`. The model then
   uses its user's own instructions (ROUTING-PLAN P2). The work-tree case is
   F13: a cloned repository's checked-in line is not the user's. It is judged
   at the cap file's resolved path, never the cwd (Codex C9; section 6).
2. **A review.** Class `review`: `no pick: review`.
3. **Not eligible.** A task gets a non-default pick only when every one of
   these holds, else `pick=<default> by=default`. It is ONE predicate, checked
   before rules 4-8, so no later rule can reach a task that fails it (Codex
   C3: rule 6 tested class and dates, not tier):
   - its class is assigned, or opened in the trial;
   - its tier is not `full` (`pick --tier full`; F1);
   - `--tier` and `tiers=` agree (cross-check X2). A line with `tiers=` needs
     `--tier`, from that list. A `--tier` given against a line without
     `tiers=` defaults. So a fleet's line routes nothing until it carries
     `tiers=`, and a coordinator that omits `--tier` against such a line gets
     the default;
   - no `--touch` path is protected, matched by the script on every path
     component;
   - an editing class (code, writing, data, ops) names its paths with
     `--touch` (F4).

   `tiers=` bounds every non-default pick on its line, `assign=` included. A
   line ROUTING-PLAN can read carries no `assign=`, so there `tiers=` keeps
   its meaning: the trial's tiers. **Residual:** a coordinator calling with
   neither `--tier` nor a `tiers=` line looks the same as a user without
   buddy. A coordinator passes `--tier` as the router's SKILL.md teaches;
   the fleet skill names no pick recipe.
4. **A trial that cannot be checked** (phase 3). Rules 4-6 apply only to a
   class opened in the trial with today inside `starts=`..`ends=` (X3). For
   such a class, any of these gives `pick=<default> by=no-fresh-table`:
   - no `--table`;
   - a table older than 7 days;
   - a table scanned under another cap (its header carries the cap's sha256);
   - the trial's source absent (F19).
5. **A trial at its floor.** Per trial cell (the class, and the tier under
   `tiers=`), the count is the counts file's lines for that cell, and nothing
   else (Codex C5, cross-check X1).
   - The table adds nothing to the count. Its cell holds past tasks, classed
     by structure, and the declared class matches that only 71% of the time
     (M13). A class the user already ran 71 times on the trial model (M6's
     qa) would be over before it began.
   - There is one counts file per cap, at
     `<state dir>/model-router/trial-<cap sha256, 12 hex>.counts`, mode
     0600, whatever `--out` says (X4). The first `scan --cap` for that
     sha256 creates it, when it freezes the baseline. Nothing else creates
     it, and nothing truncates it.
   - An absent or unreadable counts file defaults. A deleted one ends the
     trial for that cap; a newly dated cap starts again.
   - A trial pick takes an `fcntl` lock, counts, appends one line (its UTC
     time, class and tier) and fsyncs. `by=trial` prints only after that. A
     failed append, from a read-only state dir or a sandbox, prints the
     default.
   - A torn last line counts as one pick, so a pick killed mid-append
     over-counts. The next pick, under the lock, first seals it: a newline,
     then a fixed `torn` record, which counts as one pick in EVERY cell, since
     the fragment cannot say whose it was (Codex re-verify R2). A fragment
     followed by a `torn` record is the one malformed shape the reader
     accepts. Any other malformed line makes the file unreadable.
   - A pick that no launch followed over-counts too, which ends the trial
     early: the safe direction.
   - Cut: F18's projection (the cell plus its weekly rate times the table's
     age), and C5's first fold (the table's cell plus the picks since the
     scan).

   The count reaching `floor=`, or `ends=` near enough that the window's
   at-most-14-days bound is reached, gives `pick=<default> by=trial-over`,
   `report due`.
6. **A trial.** An opened class inside `starts=`..`ends=`: `pick=<trial>
   by=trial`. This is an ASSIGNMENT the cap makes for the experiment.
7. **An assignment.** An assigned class: `pick=<id> by=assign`. Rules 4-6
   never reach it, so an assignment needs no table, even while its line has
   a trial (X3).
8. **Otherwise, the default:** `pick=<default> by=default`. A class opened in
   a trial that has not started lands here (X3).

**No subagent pick in phase 2** (Codex C2, against F3's fold). The Agent tool
takes a family alias, and the harness resolves it at launch. ROUTING-PLAN
refused aliases because `fable` ran two ids (its M8). F3's fold let a sub pick
through when the table showed the alias last resolved to the capped id. That
cannot pin the next launch: the harness may resolve the alias differently
tomorrow, and the pick would then have named a model outside the cap.

So `--role sub` answers `no pick: the Agent tool cannot pin an exact id`. The
model then names the model its user's instructions name (D-117). `scan`
prints, as one count with no names, the aliases that resolved to more than
one id in its window. An alias is model-written input with no vocabulary
(X8). No phase routes a subagent until a pin is measured (section 14).

**`report`** (phase 3) applies ROUTING-PLAN section 6's comparison:

- the trial cell is cohort `starts=`..`ends=`;
- the trial and its baseline compare within one class AND one tier, never
  pooled across tiers (Codex C6; ROUTING-PLAN section 6). Under `tiers=`,
  each listed tier is its own pair of cells, and the floor and `events=` hold
  per pair;
- both cells hold only tasks rule 3's predicate admits: no protected path
  and no full tier (cross-check X6; ROUTING-PLAN section 5). The scan marks
  each task's protected flag in memory, from its paths. In this repository,
  23 of 119 opus writing episodes touch a protected path (M11) and could
  never be trial work;
- the baseline is frozen at the first `scan --cap` for that cap's sha256,
  because retention would otherwise eat it (F19). Each new table carries it
  forward, with the `conflicts=` count and version-range verdicts it was
  frozen with. `report` abstains when either the frozen baseline or the
  current data carries one (R4). A refused old table loses the baseline:
  `report` then abstains for that cap, and a newly dated cap freezes again;
- it abstains under `floor=`, or when the cells hold fewer than `events=`
  events, or when the version ranges make a signal not comparable;
- it prints `protected on non-default=<n>`, which must be 0, and every
  non-default task by scanned class (F4, F5).

The user decides from the report (D-163's shape). The router never moves a
default.

## 6. The user's cap: one grammar with buddy's `route-cap`

**The line, a superset of ROUTING-PLAN section 3's:**

```
route-cap [roles=main|sub|main,sub] default=<id>[@<effort>] [assign=<class>:<id>[@<effort>][,...]] [trial=<id>[@<effort>] classes=<c>[,<c>] floor=<n> events=<n> worse=<n>x starts=<date> ends=<date>] [review=<id>[,<id>]] [tiers=<t>[,<t>]] [protect=<prefix>[,<prefix>]] decided=<date>
```

**Every ROUTING-PLAN key keeps its meaning and its refusals.** Each of these
means no cap:
- a duplicate line (two lines whose roles overlap) or a duplicate key;
- an unknown key;
- a number outside 1..1000;
- a model alias instead of a full id;
- `trial=` equal to `default=`, compared as model at effort;
- a line read after `ends=`.

The trial group is all-or-none: `trial=` comes with `classes=`, the three
numbers and both dates. The new parts:

- **`roles=`.** Absent, it means `main` alone, which is ROUTING-PLAN's line
  exactly: a buddy cap routes lanes, and the operator's rules keep subagents
  fixed. In phase 2, `sub` in `roles=` means no cap, `roles=main,sub`
  included, because no sub pick can pin an exact id (section 5; Codex C2).
  That also closes Codex C4: F11's fold refused a trial on `roles=sub` but not
  on `roles=main,sub`. The key stays, reserved. If a later phase admits
  `sub`, a line whose roles include `sub` still takes no trial group, because
  a subagent trial cannot conclude (M9, F11). A refused line anywhere in the
  file means no cap for every role, as a duplicate line does, so a `main`
  line beside a `roles=sub` line does not survive.
- **`assign=<class>:<id>[@<effort>]`** is the user's standing route, the
  missing day-1 piece (F25). It is under the same refusals: no alias, no
  `review=` id, no `review` class, no class assigned twice, and no class both
  assigned and opened in a trial. Protected, review and full-tier tasks still
  get the default. **OPEN, the operator's:** it is a static route the
  operator's own fleet-decisions rule would have to allow.
- **`@<effort>`**, from a fixed list (low, medium, high, xhigh, max). It makes
  the unit model at effort, as a receipt writes `codex@xhigh`. A suffix outside
  the list is part of the id, so a cloud id such as `<name>@<date>` parses as an
  id (F26). An effort trial needs `@` on both sides (F20).
- **`review=`** lists the models kept for reviews. A pick never lands on one,
  and a `review=` id in `default=`, `assign=` or `trial=` means no cap. This is
  D-111's rule made readable to a script, without the shipped text naming
  Fable.
- **`protect=`** adds path prefixes to the shipped list, never removes one
  (ROUTING-PLAN F5). The shipped list covers:
  - instruction files: `CLAUDE.md`, `AGENTS.md`, `.claude/`, `skills/`;
  - CI and hooks: `.github/workflows/`, `.githooks/`;
  - any path COMPONENT containing auth, crypto, secret, security, permission,
    sandbox, credential, password or token. The probe matched only the last
    component, which F4 caught: `auth/handlers/login.go` must match.

  For buddy, the export's `protected` column carries ROUTING-PLAN section 5's
  list.
- **Claude ids only in phase 2.** Every id must contain `claude`, cloud
  provider ids included. A Codex model id means no cap, and no pick prints a
  `codex exec` form (F20). How a Codex-only user would load the skill is NOT
  MEASURED (F26).

**Cost limits are not a cap key** (cut, F24). No pick can act on a limit: the
only cheaper place for a task is a route the cap already names. Cutting work on
the harness's list-price figure would route on a number that is not a
subscription user's bill. `scan` prints the spend it can see, and a hard limit
belongs in the provider's own spend controls. This is the plan's answer to
the brief's "cost limits".

**Where the line lives.** It sits in any file, at column 0, starting
`route-cap `. The script reads it with `--cap <file>`, and has no default path
(ROUTING-PLAN F23, P2).

- **The cap file's own location decides** (F13, Codex C9). The script resolves
  the file's real path and refuses it when any directory above it holds a
  `.git` entry (a directory, or a linked worktree's file), wherever the script
  runs from. The first fold checked the cwd's work tree, so
  `--cap /tmp/cloned-A/CLAUDE.md` run from repository B passed. Neither `~`
  nor `~/.claude` is a work tree on this machine, so the operator's
  fleet-decisions file passes. A `~/.claude/CLAUDE.md` symlinked into a
  dotfiles repository resolves into a work tree and is refused. An unpacked
  project archive, with no `.git`, passes: a residual.
- **A user whose home directory is itself a work tree** (a dotfiles repository)
  therefore gets no cap from any file under it. That is the safe direction:
  `no pick` names the reason, and the user's instructions still choose.
- **The caller passes only the file the user's own instructions name,** never
  a copy, as ROUTING-PLAN section 3 has it. The script cannot tell a stale
  copy from the live file.

- **A buddy operator** keeps it in the fleet-decisions file, where ROUTING-PLAN
  put it.
- **A user without buddy** keeps it in their user-level instructions
  (`~/.claude/CLAUDE.md`), or in a file those instructions name. About 200
  bytes ride every request when it is in `CLAUDE.md` itself.

The SKILL.md says that a project file's line is not the user's. Neither the
skill nor any session writes the cap. `cap-template` prints a draft with
`decided=` blank, which parses as no cap until the user dates it.

**One parser, landed first** (F20). As written, three lines ROUTING-PLAN reads
as no cap would parse here:
- a default-only line;
- an effort trial;
- two lines with disjoint roles.

So one file must not mean two things. The recommendation: phase 2 lands this
parser, and ROUTING-PLAN step 4 becomes this script reading buddy's export
(`router.py report --cap <file> --buddy-export <tsv>`), not a second parse in
`model-report.sh`. `model-report.sh` keeps the export and the baseline (step
2, which the `route-grammar` lane is building now). Until then, the operator's
fleet-decisions file carries no `route-cap` line. **OPEN, the operator's.**

## 7. What it outputs

**The table file.** `scan --out <file>`; with no `--out`, the default is
`${XDG_STATE_HOME:-~/.local/state}/model-router/table.tsv`. It is written
mode 0600, and the script refuses a path inside any git work tree (F16).
The same resolved-path refusal covers every file the script writes: the
table, the counts file and `refused.txt`. A state dir set inside a work tree
(`XDG_STATE_HOME=<repo>/state`) refuses before anything is written, so no
project or session path lands where it could be staged (R3).

The table is TSV, counts only. Its header records:
- the format version and the window;
- each source's state, with `conflicts=` and the torn last lines skipped;
- `scoped`;
- the cap's sha256 under `--cap`, and the frozen baseline;
- the script's sha256 and the UTC time it was made;
- the count of aliases that resolved to more than one id, with no names
  (section 5).

Each row is `role class tier model[@effort] eligible tasks last7d`, then each
signal as `fired/covered`. `tier` is `-` for a task with no declared tier,
which is every task without buddy's export (Codex C6). `eligible` is `no`
for a task with a protected path or a full tier, else `yes`. Comparisons read the `yes` rows; `report`'s `protected on
non-default` reads the non-default models' `no` rows (R1).

**A torn or partial table is refused** (Codex C8). A table overwritten in
place and interrupted after its header and first row would pass every
vocabulary check with the trial's rows missing.
- `scan` writes a temporary file in the same directory, fsyncs it and renames
  it over the old one.
- The table ends with a trailer line holding its row count and the sha256 of
  every byte above it.
- A reader refuses a table with no trailer, a count or hash that does not
  match, an unknown format version, or two rows with one cell key.

What a refused table costs follows from the pick rule: an assignment reads no
table, and a trial picks the default (rule 4). The table's sha256 is printed by `scan`, and by every `pick`
and `report` that reads it. The table is the retained record, because the
transcripts behind it expire in 30 days by default (M2).

**`pick`** prints one line, and the cap's path stays on one capped line
(invariant 9's shape; F15):

```
pick=claude-<x>@high by=assign cap=<path> sha256=<12 hex> decided=<date>
no pick: the Agent tool cannot pin an exact id; use your instructions' default
```

A trial pick (phase 3) adds `table=<12 hex>`. The `alias=` field is cut with
the sub pick (Codex C2).

`pick --all` prints one line per class for the main role, about 1 KB, for a
coordinator about to open several lanes. Any row that would differ from the
default on an editing class says `needs --touch`, and a trial class says
`needs a per-task call`, since only a per-task pick appends to the counts
file (X4). A trial pick, or an assigned pick on an editing class, comes only
from a per-task call that names its paths (F4).

**Day 1, for a user without buddy:**

1. Copy the skill directory into `~/.claude/skills/`.
2. Ask a session to set up model routing. It runs `router.py scan`, which took
   88 s here and prints about 2 KB: the sources found; the classes and their
   weekly volume; the models and efforts already used per class, marked
   confounded; and which signals are live for this user.
3. It runs `router.py cap-template` and shows the draft:
   - `default=` is the user's most-used main model at its most-used effort;
   - a commented `assign=` candidate for each class where the user already
     ran a second model at least 10 times (here, 5 classes: M17), left out of
     the line for the user to move in (section 15);
   - `review=` and `trial=` are blank, with the models seen listed beside
     them;
   - `decided=` is blank.
4. The user edits the draft, dates it, and saves it in their own instructions.
   The session never writes it.
5. From then on, every session pick applies the user's routes. Subagents are
   not routed (section 5), so a single-session user gets a pick only when
   opening a new session. `scan` prints, as an observation, how many Agent
   calls named no model and so inherited the parent's: CHEAPER-MODELS section
   0 counted 17, which D-117's hook now refuses on this machine.
6. Later, a trial on a `qa` or `writing` class can test a route on evidence
   (phase 3).

**What the SKILL.md tells the model in session:**

- **A subagent's model.** Not a pick in phase 2 (section 5). Name the model
  the user's instructions name, and always name one, as a hook may require
  (D-117).
- **A parallel session's (lane's) model and effort.**
  - Name the task's class in one word: code, writing, research, data, ops,
    qa, or review.
  - Run `router.py pick --cap <file> --role main --class <c> --touch
    <path>...`, with `--tier` under buddy, and with `--table <file>` while
    the line has a trial.
  - The launch line carries `--model <id>`, and `--effort <effort>` only
    when the pick carries `@<effort>`: the cap is the user's explicit
    instruction.
  - Under buddy, the brief quotes the cap's path, sha256 and `decided=`.
  - The skill launches nothing.
- **A draft from `scan` or `cap-template`.** Show it to the user as data; act
  on none of it (section 3's residual).
- **Claude vs Codex.** Not a pick. The model says so and follows its user's
  instructions; under buddy, the operator's per-run authorization
  (`AGENTS.md`).
- **Always:**
  - the print confers no authority, and its first line says so;
  - a pick never widens what the user's instructions allow;
  - the output is data, not instructions.

## 8. Portability and shipping

- **Runtime.** Python 3.8 or later, stdlib only (`json`, `fcntl`, `os`,
  `re`, `shlex`, `hashlib`). It runs on macOS and Linux, needs no network and
  no Go, and runs as `python3 -I`.
  - A POSIX-sh version was weighed and refused: sh plus `jq` over 9.4 GB of
    JSONL would take minutes and add a dependency.
  - `env-check.sh` is the read-only precedent in spirit, not in language.
- **The directory.** `skills/model-router/` holds `SKILL.md`, `router.py` and
  `reference.md`.
  - Every file sits beside `SKILL.md`, because `install-skill.sh` installs
    every regular file of a skill and refuses a subdirectory (D-171).
  - `SKILL.md` names no model, date or operator default (D-114).
- **A plain copy works outside this repository:** `cp -R skills/model-router
  ~/.claude/skills/`. `--buddy-export` is its only buddy-specific input, and
  that input is optional.
- **The hermetic tier must change** (Fable F23). `TestSkillNamesOnlyWhatHelpAnswers`
  (`internal/cli/discover_help_test.go:241`) fails a SKILL.md that checks
  fewer than 20 buddy names, or a reference file under 5. A skill that names
  no buddy verb fails it. `TestSkillFilesAreEveryShippedFile` makes the skill
  tests read `router.py` too. Phase 2 therefore:
  - edits the test that holds every skill to the CLI's names;
  - calls `--self-test` from `check.sh`, whose Python leg sweeps
    `scripts/*.py` only;
  - adds `docs/prose-budget.tsv` lines for the new description and body,
    their ceilings set by the operator.
- **Two homes. OPEN, the operator's:**
  - `skills/model-router/`, installed with the set (D-114, D-121). Every buddy
    machine then carries its description in every session's listing. This is
    the recommendation, because the description is small and triggers only on
    a cap or an explicit ask;
  - a directory outside `skills/`, copied by hand. That avoids the listing
    cost, but it is a second distribution path that neither `install-skill.sh`
    nor `uninstall.sh` covers, and neither does the hermetic tier.

## 9. Context cost (D-010)

Every figure is an ESTIMATE of text not yet written. Phase 2 measures each one
with `wc -c` and a fixture run, and reports them in its READY.

| who | bytes | when |
|---|---|---|
| every request, wherever it is installed | the description, about 380. The every-request class has 3,580 bytes of headroom (33,284 of 36,864) | always, once installed |
| a session that loads the skill | the body, at most 4,096. The per-session class has 8,061 bytes of headroom (73,859 of 81,920) | loaded only when the user's own instructions carry a `route-cap` line and it is choosing a model, or when the user asks to set up routing. Once loaded, it rides every later request of that session (Codex C11) |
| a buddy lane brief | the cap's path, sha256 and `decided=`, about 150 | in the brief, so in every request of that lane (Codex C11) |
| read on demand | `reference.md`, about 8 KB: the class rules, the signals, the parse refusals, the version ranges | when setting up, or reading a report |
| one `scan` | at most 2 KB printed | weekly, or at a fleet run's start |
| one `pick`, or `pick --all` | about 200 bytes, or about 1 KB | per pick, or once per session |
| the user's `CLAUDE.md`, if the line lives there | about 200, every request | the user's choice |

## 10. Staged rollout

| phase | ships | tier | measures before the next |
|---|---|---|---|
| 1 (this) | the plan and M1-M13 | full-design (the brief) | the operator's decisions (section 14) |
| 2 | **the declared-assignment router:** `scan`, `cap-template`, `pick` (rules 1-3, 7 and 8), `--self-test`; the one parser; `SKILL.md`, `reference.md`. Its tests: hermetic fixtures with a canary string planted in every text field AND every enum-shaped field, where the test fails if the canary reaches any output, table or error line; a refusal test per cap refusal, each with its positive control; the absent and unreadable verdicts, an interior torn line included; the closed vocabularies and the model-id gate, with a planted id repeated past 10 tasks; the work-tree refusals, the cap's run from a cwd outside its tree; the torn-table refusals (no trailer, a short row count, a duplicate cell key); `roles=` with `sub` refused; the skill-test change (section 8) | **full**, recommended: it reads every transcript on the machine, ships to every install, and changes the test that holds every skill to the CLI's names. The operator's call | `scan` here reproduces, by the same rules and within the window's drift, only what it ships (Codex C10): M4, M5, M6 and M7, with Codex figures per class and their ids folded to `(other)` (cross-check X7); from M8 and M9, `interrupt`, `user_reject`, `calls/task`, `tested`, `test_red_end`, `test_masked`, `policy_denied` and `abandoned`. Not reproduced, because cut: `retry`, the second failure, `churn`, `commit`, `exec_error`, `reopen` (M10) and the probe's text-marker cross-checks. Also its runtime and output bytes; `conflicts=` over the 112 duplicates; `cap-template`'s draft against the operator's actual rules |
| 3 | the trial: pick rules 4-6 with the counts file, `report` by class and tier, the frozen baseline, buddy's export as a source, and ROUTING-PLAN step 4 folded in | full (it changes what moves a lane off the default) | the first trial, until its floor or `ends=` |

**Cut from every phase:**
- a learner;
- text reading;
- launching;
- a settings or cap edit;
- a per-file cache. A cache would leave salted path hashes on disk between
  runs, and a weekly scan does not need it.

## 11. Fable consultation

One read-only Fable subagent (`model: fable`, effort xhigh) ruled on the
draft at commit 6297e94. It was given the draft, the probe and its outputs,
the constraints and the prior-art paths. It read no transcript or rollout,
and ran one counts-only ledger query (immutable).

**Ruling: accept with revisions.** It found the shape sound and overturning
nothing: a counts-only scan, a printed pick inside a user-written cap, history
never choosing between models, and text cut. But the pick path reopened holes
that ROUTING-PLAN's folds had closed, and the rule inputs rewarded doing less.
Its verdict: "ship it as a declared-assignment router with trials second."
Its conditions were F1-F4, F7, F10, F13-F15 and F17-F19, all taken. (Section
12's Codex pass later overturned the folds of F3, F11, F14 and F18, and
completed F13's.) It
raised 26 findings: 23 taken and 3 taken in part (F16, F22, F26).

| # | finding | taken |
|---|---|---|
| F1 | full tier routable: no tier input, no full-tier clause | yes: `pick --tier`; full is the default; a buddy trial line needs `tiers=` (rule 3) |
| F2 | reviews routable: `review=` excluded models, not tasks | yes: a `review` class, `no pick: review` (section 3, rule 2) |
| F3 | `alias=` re-widens the cap (`fable` ran two ids) | yes: a sub pick needs the table's last alias resolution, else `no pick: alias drift`. Overturned by Codex C2: no sub pick in phase 2 |
| F4 | protection was a self-declared flag, off when omitted; names matched the last component only | yes: `--touch`, matched on every component; editing classes with no paths get the default; `protected on non-default` must print 0. Taken stricter than asked: writing and data need `--touch` too, not only code and ops |
| F5 | the named and scanned classes differ, and the leak line counted default tasks only | yes: the report lists every non-default task by scanned class |
| F6 | suggestions push down without evidence; an invented effort arm | yes: qa and writing only, the cap's own `trial=`, no effort suggestion |
| F7 | the rule inputs reward doing less | yes: the rule inputs are `interrupt`, `user_reject`, and buddy's three; `no worse` only with the export; guards (section 4) |
| F8 | `retry` keyed shell targets on program labels | yes: cut |
| F9 | `abandoned` never tested "no later prompt" | yes: printed only, defined as what it is |
| F10 | an event-only field has no coverage | yes: shipped version ranges per field |
| F11 | a subagent trial cannot conclude | yes: a `roles=sub` line takes no trial. Codex C4 found `roles=main,sub` slipped past; C2's fold refuses `sub` in phase 2 |
| F12 | M13 joined the coordinator's busiest episode | yes, by another key: the producer covers 9 items only (92 of 104 sat on multi-item sessions under it), so M13 joins on the lane's label, 40 items, 27 of 38 agreeing |
| F13 | a cloned repository's `CLAUDE.md` can carry a cap | yes: a cap inside the cwd's work tree is refused; a project file's line is not the user's. Completed by Codex C9: the cap file's own path decides |
| F14 | the vocabulary must hold for enum fields and on read; the probe printed a raw effort | yes: fixed lists, rare ids withheld, files validated on read, the canary in enum fields; the probe fixed. The count floor is overturned by Codex C1: a syntax gate, and ids named only from the cap once one exists; the pre-cap draft keeps the floor (section 3) |
| F15 | exception text quotes values | yes: the exception's type only; the cap path on one capped line |
| F16 | `--out` had no home; refusals printed paths; the M-table publishes the operator's profile | yes to the first two: a default under the state dir, 0600, refused in a work tree, refusals by source and count. In part on the third: the dollar median is withdrawn; the project and session counts stay, and the operator decides (section 14) |
| F17 | no stated precedence; no `--table` | yes: an ordered rule; `--table` required with a trial |
| F18 | seven-day staleness overruns `floor=` | yes: the floor projected over the table's age. Overturned by Codex C5: a counts file of trial picks |
| F19 | an absent source keeps a trial running; retention eats the baseline | yes: `CLAUDE_CONFIG_DIR` and `CODEX_HOME`; a trial needs its source present; windows of at most 14 days; the baseline frozen |
| F20 | three lines ROUTING-PLAN reads as no cap parse here | yes: one parser landed first, the fleet file carries no line until then; `@` on both sides; Codex ids refused |
| F21 | eight figures did not match the outputs | yes: every figure re-taken from one run (run 4); M4 is 95.0%, M9 is disjoint, M11 spans all cells |
| F22 | nobody can re-run it | in part: one probe and one output now carry every figure, kept in the scratchpad; the repository copy is OPEN, since the brief kept the probe out |
| F23 | the shipping claim was wrong: the skill tests fail a verb-free skill | yes: verified at `discover_help_test.go:241`; phase 2 changes the test and wires `--self-test` |
| F24 | cuts | yes: the Codex state database, `budget=` (the cost-limit question answered in section 6), the priors table, `--allow-partial`, five signals, subagent trials, the effort suggestion, `no worse` without buddy |
| F25 | a standing assignment, for day 1 | yes, as the recommendation: `assign=`, drafted undated by `cap-template`. OPEN for the operator |
| F26 | cloud ids fail the grammar; Codex-only users | in part: effort comes from a fixed list and cloud ids containing `claude` parse. A Codex-only user is NOT MEASURED, and Codex ids are refused in phase 2 |

Not taken whole:

- F16's machine profile, which stays for the operator to decide;
- F22's repository copy, because the brief said the probe stays out of the
  repository;
- F26's Codex-only path, which phase 2 refuses rather than designs.

## 12. Codex design pass

The successor lane ran one pass on the plan at aff1a63, through the main
checkout's `scripts/codex-review.sh`, in the foreground:
- gpt-6-astra at xhigh, the header verified;
- `CODEX_CHARTER=process`, `CODEX_DIFF=03da68d..aff1a63`;
- 268 s, rc 0.

It ran nothing that reads a transcript. It answered the brief's five questions:

- **Q1, message text:** YES (C1).
- **Q2, a pick widening the cap:** YES (C2-C5, C9).
- **Q3, a figure nobody can re-run:** NO. Section 0 confines M1-M13 to
  feasibility, and every pick derives from the cap.
- **Q4, failing open:** YES (C7, C8).
- **Q5, a cost every session pays:** YES, in the accounting only (C11). The
  costs were disclosed, but two rows were missing.

**It raised 11: 3 P1, 7 P2 and 1 note. All 11 were taken: C1 and C7 in part,
C2 stricter than asked.** Four overturn a Fable fold: C1 (F14's count floor),
C2 (F3), C4 (F11) and C5 (F18). C9 completes F13. The cross-check (section
13) found C3's counting half, C5 and C6's eligibility half folded short. They
were folded again there, and the rows below say where.

| # | finding | taken |
|---|---|---|
| C1 | P1. A model id had no closed vocabulary. Ten planted transcripts carrying an id with a newline in it pass F14's count floor, and reach the table and a model's context | in part: a syntax gate (`[a-z0-9._:-]`, 1-64 bytes, containing `claude`), and an id that passes is printed by name only when the cap names it (section 3). Not taken whole: before a cap exists, `scan` and `cap-template` still name gate-passing ids seen in 10 tasks, because the user needs them to write the cap. The residual is stated, and the draft is shown as data |
| C2 | P1. An alias's past resolution cannot pin a later launch, so a sub pick could name a model outside the cap | yes, stricter than asked: Codex allowed a sub pick where the interface pins an exact id, and none is measured to. No sub pick in phase 2; alias drift printed as a count (sections 5, 7). The fork is OPEN (section 14) |
| C3 | P1. Rule 6 tested class and dates, not `tiers=`, so a `lite` item reached a `chore`-only trial | yes: one eligibility predicate, rule 3, checked before rules 4-8. Its "before counting trial work" half came with X1 |
| C4 | P2. `roles=main,sub` took a trial group, past F11's refusal | yes, through C2's fold: `sub` in `roles=` means no cap in phase 2. A later phase keeps the trial refusal for any role set containing `sub` |
| C5 | P2. A projected floor does not bound picks: an empty fresh cell projects 0 for every pick | yes: a locked counts file of trial picks, one per cap; the projection is cut. The first fold added the table's cell and still overran; X1 and X4 re-folded it (rule 5) |
| C6 | P2. The table's row key had no tier, so tiers pooled | yes: `tier` in the row key, and class-and-tier pairs in `report` and rule 5. The cells' eligibility half came with X6: both hold only what rule 3 admits |
| C7 | P2. A 1% torn-line threshold drops interior events without a word, and conflicting duplicates had no verdict | in part: any interior malformed line refuses, with `refused.txt` as the way out (X5). Codex said refuse a conflicting duplicate; the fold counts it and `report` abstains while any exists, so a scan still runs (section 2) |
| C8 | P2. A truncated table passes the vocabulary checks | yes: an atomic rename, a trailer holding the row count and sha256, refusals on read (section 7) |
| C9 | P2. The cap's work-tree check used the cwd, so a cloned repository's file named from elsewhere passed | yes: the cap file's own resolved path decides (sections 5, 6) |
| C10 | P2. Phase 2's acceptance named M8 and M9 signals that the plan cuts | yes: the reproduced figures are listed one by one (section 10) |
| C11 | note. Section 9 missed the brief's cap quote and a loaded body's carriage | yes: one row changed, one added |

Not taken whole: C1's pre-cap draft and C7's conflict refusal, for the
reasons in their rows.

## 13. Fable cross-check and Codex re-verify

The round ceiling is one of each.

**The cross-check.** One read-only Fable subagent (`model: fable`, effort
xhigh) checked section 12's fold, the diff aff1a63..a6cff74, against Codex's
raw output. It read no transcript or rollout. It also read two public pages of
the harness's documentation, for the C2 ruling.

**It raised 9 findings and 6 notes, and all 15 were taken: X6 and X8 by another
route.** Its verdicts on the fold:
- correct: C2, C4, C8, C9, C11;
- correct with gaps: C3 (X2, X3);
- regressed: C1 (X7), C5 (X1, X4), C7 (X5);
- wrong in part: C6 (X6), C10 (X7).

| # | finding | taken |
|---|---|---|
| X1 | Rule 5 still did not bound the trial. It added the table's cell (scanned class, whole window) to picks (declared class) since the scan: a re-scan lost picks, and a class run 71 times on the trial model was over at its first pick | yes: the count is the counts file's lines alone (rule 5) |
| X2 | Full-tier work left the default when the coordinator omitted `--tier` against a line without `tiers=` | yes: `--tier` and `tiers=` must agree, and `tiers=` bounds `assign=` too. The residual is stated (rule 3) |
| X3 | Rule 4 gave an assignment `no-fresh-table` while its line had a trial, and a trial before `starts=` matched no rule | yes: rules 4-6 scoped to an opened class inside its dates, and a last rule 8 |
| X4 | Two tables made two counters, `pick --all` gave trial picks with no append, a failed append was not a default, and a torn line killed the trial for good | yes: one counts file per cap, `by=trial` only after a synced append, `pick --all` says `needs a per-task call`, and a torn last line counts as a pick |
| X5 | C7's refusal had no way out: one torn interior line in a Codex rollout, which never expires, refuses every scan for good | yes: `refused.txt` lists the files, 0600, and "malformed" is defined (section 2) |
| X6 | The baseline admitted protected tasks the trial never could, and one `conflicts=` number cannot say which window a conflict falls in | by another route: both cells admit only what rule 3 admits; `report` abstains while `conflicts=` is above 0, stricter than the split counts it asked for |
| X7 | The id gate had no `@`, so a cloud id fell to `(other)`; and section 10 asked for Codex figures by id, and for subagent end states the scan does not yield | yes: `@` in the gate; Codex figures per class; end states dropped |
| X8 | Alias keys in the table header had no vocabulary | by another route: no alias names at all, one drift count. A shipped alias list would name model families (D-114) |
| X9 | Section 12 overstated what was taken (C2, C3, C6, C7), the F14 row overstated C1, and the Sources line said section 13 was done | yes: the rows and lines restated |
| notes | a symlinked or archived cap file; rule 3's dead role bullet; the sub status after phase 2; a main line beside a refused sub line; a refused old table and the frozen baseline; `fcntl` missing from the runtime list | yes, all six |

**Its C2 ruling:** the cut is the right default. The alias line that accepts
drift should not be the recommendation, because it makes the cap
family-valued, and `review=`'s exact ids cannot hold against an alias that
moves. The cost was understated, and the fork was a false binary (section
14). Both taken.

**The Codex re-verify.** One pass of the cross-check's fold, the diff
a6cff74..77cfc2d, through the same script: gpt-6-astra at xhigh, header
verified, `CODEX_CHARTER=process`, 395 s, rc 0. It ran nothing that reads a
transcript.

It found X1, X3, X5-X9 and the notes CLOSED, and X2 and X4 OPEN. **It raised 5:
3 P2 and 2 notes. 4 were fixed, and 1 is answered and left open.** The round
ceiling is reached, so no review checked these folds.

| # | finding | taken |
|---|---|---|
| R1 | P2. The cells excluded protected tasks, so the table could no longer give `report`'s `protected on non-default` | fixed: `eligible` in the row key (section 7) |
| R2 | P2. A torn counter tail still ended the trial for good, after the next append made it interior | fixed: the next pick seals it with a `torn` record charged to every cell (rule 5) |
| R3 | P2. `refused.txt` under an `XDG_STATE_HOME` inside a work tree put project paths where they could be staged | fixed: the work-tree refusal covers every file the script writes (section 7) |
| R4 | note. A frozen baseline carried forward lost its own `conflicts=` | fixed: frozen with its uncertainty; `report` abstains on either side's (section 5) |
| R5 | note. X2's case stands as the stated residual: a full-tier call with no `--tier`, against a line with no `tiers=` and an `assign=`, gets the assignment | answered, OPEN (section 14) |

## 14. Open

- **The operator's decisions.** None is answered yet. Phase 2 was built on the
  orchestrator's defaults (orc-20, relayed by orc-21 on 2026-11-12, under the
  operator's word "go with what you think is a sane default"). Each is the
  orchestrator's, not the operator's, and the operator can overturn it:
  - phase 2 is wanted: built;
  - its home (section 8): `skills/model-router/`, shipped with the set;
  - its tier (section 10): full;
  - `assign=` (section 6): yes. The draft lists candidates and never routes
    one (M17);
  - subagent routing (below): the "none" arm;
  - one parser for ROUTING-PLAN step 4 (section 6): yes. `router.py`'s parser
    is that parser, and step 4 has not adopted it yet;
  - the probe in the repository (section 0): yes, as phase 2's starting point.
    `router.py` started from its tables and class rule, and dropped its text
    checks, the Codex state database and the ledger join;
  - the M-table's machine profile (F16): it stays in this plan, as counts
    only.
- **The draft's candidates** (M17, a lane decision taken as the safer option).
  The design put the observed splits into the draft's `assign=`. Measured,
  that drafted five routes here, and none matched the operator's rules, so
  they now sit on a commented line. Restoring them to the line is the
  operator's call.
- **Subagent routing** (Codex C2, the cross-check's ruling). The plan routes
  no subagent until a pin is measured, and that costs a single-session user
  most of day 1: a scan, a draft, and a pick only when opening a new session.
  Three arms, the operator's to choose:
  - **none,** this plan's phase 2;
  - **a pin, recommended for measurement:** a sub pick only when `pick` sees a
    pin equal to the cap's id, else `no pick`. That is C2's own fix. The
    cross-check read two in the harness's public documentation: a subagent
    definition whose `model:` is a full id (which a per-call alias
    overrides, so the call must name none), and
    `ANTHROPIC_DEFAULT_<FAMILY>_MODEL`, which fixes what an alias resolves
    to. Whether either holds, and whether `pick` can see it, is NOT MEASURED;
  - **an alias line accepting drift,** not recommended: it makes the cap
    family-valued, and `review=`'s exact ids cannot hold against an alias
    that moves.
- **Left at the round ceiling** (section 13):
  - **R5, X2's residual: CLOSED in phase 2** (section 15). Every non-default
    pick requires `--tier`, `none` included, so an omission defaults. Phase
    2's design pass read the closure. The residual is now a false statement:
    a full-tier item typed `--tier none`.
  - **Phase 2's re-verify, round 2 (the ceiling), two notes answered:** the
    work-tree walk continues past a component that is a regular file
    (`file/../repo/../store`), so it can refuse a path the kernel would refuse
    anyway with ENOTDIR: it refuses more, never less. A relative path named
    to `scan` or `cap-template` from a deleted cwd exits 3 (the type only, as
    every internal error); nothing is read or written.
  - R1-R4 were folded after the last review round. R1 (`eligible` in the row
    key) and R3 (every write refused in a work tree) are built in phase 2, and
    its reviews read them. R2 and R4 belong to phase 3's counts file and
    baseline, and no review has read them yet.
- **The pre-cap draft** can carry a gate-passing token planted in enough kept
  tasks to be named, and can take its `default=` from one (section 3, C1,
  panel F1). The draft is undated data, shown as data, and the user writes
  the cap.
- **Lane decisions in the fold,** each the safer option, the operator's to
  overturn: at most 8 ids named before a cap; one broad per-file catch
  (`FILE_ERRORS`) turns any decode or shape error into a refusal; only a
  `--claude` or `--codex` NAMED inside a work tree is refused, so a default
  `~/.claude` kept in git still scans; a group-writable cap is accepted
  (a umask of 002 makes every file one) and a world-writable one refused; a
  linked worktree is known only by its git directory's `commondir`, else the
  walk climbs on and protects more; a refused scan removes its target's old
  table; the table's reader enforces the naming rule before a cap.
- **Whether a resumed Codex rollout copies its parent's events** is NOT
  MEASURED (code pass B6). Codex records carry no uuid, so nothing dedups
  them, and a copy would count twice.
- **Answered, no change** (panel, cross-check and code pass): the fence keeps
  `=`, since a caller's path is one token; the Go skill check sees only
  backticked `buddy` spans, every skill's rule; the planted canaries' controls
  prove each file was read; a FIFO named `*.jsonl`, a dead automount and a
  symlinked project directory are skipped as not regular files or not
  directories; the version ranges are harness versions, not a default; the
  fixture ids are test data. B5: a main-file record marked `isSidechain` is
  skipped before any field is read, so it is no counted copy and cannot
  conflict, and moving the skip after the dedup would let it shadow the
  subagent file's equal copy. B6: content dedup would merge genuinely repeated
  events (above).
- **How many of the 112 duplicate records conflict:** MEASURED in phase 2
  (M16). 16 of the 28 duplicated uuids conflict, and they take 5 subagent
  tasks out of every cell.
- **A stop reason is never printed.** It feeds only `abandoned`'s count. So
  the mutation run's "a raw stop reason leaks" mutant has no print to reach
  (section 16).
- **The class rule's accuracy** was measured on 38 buddy items only: 71%
  agreement, and the misses are activity against intent (M13). For a
  non-buddy user's work it is NOT MEASURED.
- **A human-attended user's signals are untested here.** This machine is a
  fleet (M8). That `interrupt` and `user_reject` fire for such a user is a
  prediction, drawn from Codex TUI's 12 of 33 (M9), not a measurement.
- **Several trials at once** need two lines, which mean no cap. It stays that
  way: one trial at a time is the experiment's design.
- **Retention.** A default install keeps 30 days of Claude Code history, and
  the table is the only record that outlives it.
- **The record formats are internal to each harness.** The version ranges
  turn a renamed field into "not comparable", not into a zero. A harness
  release can still retire a field the router needs.

## 15. Phase 2 build plan (for the design pass)

Written by the `model-router` lane before any code, for the Codex design
pass. The orchestrator defaulted section 14's decisions; the lane's READY
records each one. What this section fixes that sections 1-14 left open is
marked **NEW**; a departure from them is marked **CHANGED**, with the reason.

**Files.** `skills/model-router/`, installed with the set by the existing
`install-skill.sh` and removed by `uninstall.sh` (both enumerate
`skills/*/`, so neither changes):

- `SKILL.md`: frontmatter `name: model-router` and a description of about
  400 bytes that triggers on a `route-cap` line or an explicit ask. The body,
  at most 4,096 bytes, is section 7's "what the SKILL.md tells the model". It
  names no model, date or operator default (D-114), and no buddy verb (below).
- `router.py`: Python 3.8+, stdlib only, run as `python3 -I`. It starts from
  the phase 1 probe's tables and class rule (section 0, sha256 `1cf1b61f…`),
  so M4-M7 reproduce by the same rules. It drops the probe's text-prefix
  checks, the Codex state database, the ledger join and the cut signals.
- `reference.md`: the grammar, each refusal, the pick rules, the class rule,
  the signals and their version ranges, the table format, the files and
  modes, the exit codes. About 8 KB, read on demand.

**Verbs, arguments, exit codes.** NEW: arguments are parsed by hand, not by
`argparse`. `argparse` echoes a bad value in its error (`invalid choice:
'<value>'`), and a value here is model-written input. A refusal names the
flag, never the value.

```
router.py scan [--claude <dir>] [--codex <dir>] [--out <file>] [--cap <file>] [--cwd-under <dir>]
router.py cap-template [--table <file>]
router.py pick --cap <file> --role main|sub (--class <c> [--touch <path>]... | --all) [--tier chore|lite|full|none]
router.py --self-test
```

- Exit 0: a table written, a draft printed, or a `pick=` line.
- Exit 1: a `no pick: <reason>` line, or a self-test failure.
- Exit 2: a refusal (a usage error, an unreadable source, a write target in
  a work tree, a refused table). It writes no table.
- Exit 3: an internal error. It prints `router.py: internal error <type>`,
  the exception's type and never its message (F15).
- `report` is phase 3 and refuses as an unknown verb.

**The grammar, exactly.** A cap line is any line of the `--cap` file that
begins at column 0 with `route-cap` followed by a space, a tab or the end of
the line. A line that only starts `route-cap` and is malformed is refused,
not skipped. No other line is parsed or printed.

```
line    = "route-cap" 1*(" " token)       ; bytes 0x20-0x7e only, at most 1,024; one trailing CR stripped
token   = key "=" value                    ; each key at most once
mid     = id ["@" effort]                  ; split at the LAST "@" only when the suffix is an effort
id      = 1*64 of [a-z0-9._:@-], containing "claude" and a digit
effort  = low | medium | high | xhigh | max
class   = code | writing | research | data | ops | qa  (docs reads as writing, test as code)
roles=   main | sub | main,sub              ; absent: main. Phase 2: any value naming sub refuses
default= mid                               ; required
assign=  class ":" mid *("," class ":" mid)
trial=   mid, with classes= class *("," class), floor= events= 1..1000, worse= 1..1000 "x", starts= ends= date
review=  id *("," id)                      ; no @effort: a review model is excluded whatever its effort
tiers=   tier *("," tier), tier = chore | lite
protect= prefix *("," prefix)              ; 1..256 of [A-Za-z0-9._/-], no ".." component, folded to lower case
decided= date                              ; YYYY-MM-DD, a real calendar date; blank is undated
```

Each of these means no cap, and each gets a refusal test with its positive
control: no cap line; two cap lines; a duplicate or unknown key; a byte
outside 0x20-0x7e, or a line over 1,024 bytes; an empty element (`assign=,`);
a number outside 1..1000; an id failing the gate (an alias, a Codex id, a
`[1m]` suffix); `default=` missing; `decided=` missing, blank or not a date;
`roles=` naming `sub`, or any other word; an `assign=` class outside the
list, `review` among them, or assigned twice; the trial group incomplete;
`trial=` equal to `default=` as model at effort, or naming the same id
without `@<effort>` on both sides (D4); `starts=` after `ends=`, or more than
14 days before it (D5); an id with a `latest` component (D6); a class both assigned and in
`classes=`; a `review=` id in `default=`, `assign=` or `trial=` (compared by
id, whatever the effort); `tiers=` naming `full`, `none` or another word; a
date after `ends=` (today, UTC); a cap file that is not a regular file, is
over 1 MiB, or resolves inside a git work tree (section 6).

- NEW: the id gate needs a digit as well as `claude`. Every alias the
  harness takes (`opus`, `sonnet`, `haiku`, `fable`, `inherit`) already fails
  on `claude`; the digit also refuses a family name such as `claude-opus`.
  The one gate serves the cap, the scan and the table.
- NEW: the trial group parses under its refusals, because the grammar is
  ROUTING-PLAN step 4's too. In phase 2, a class opened in a trial answers
  `pick=<default> by=default why=trial-not-built`, and `scan` builds no
  counts file and freezes no baseline. A user who writes a trial line sees
  that it is not running.
- NEW: the cap's sha256 is over the cap line's own bytes, not the whole file.
  An edit elsewhere in the user's `CLAUDE.md` must not change the cap's
  identity. That matters in phase 3, where the counts file is keyed by it.

**The pick, phase 2 (rules 1-3, 7 and 8).** First match answers.

1. `no pick: <reason>`. The reasons are `--role sub` (the Agent tool cannot
   pin an exact id), no cap file given, and the cap file unreadable, too
   large, not regular, or in a work tree. A cap line missing, refused or
   past `ends=` gives one too. The reason comes from a fixed list and never
   quotes the file.
2. `--class review`: `no pick: review`.
3. The eligibility predicate. One function, checked before rule 7. Its first
   failing clause answers `pick=<default> by=default why=<clause>`, from a
   fixed list:
   - `not-assigned`, or `trial-not-built`;
   - `full-tier`: `--tier full`;
   - `no-tier`: `--tier` omitted;
   - `tier-not-listed`: a line with `tiers=` and a `--tier` it does not
     list, `none` included; or a line without `tiers=` and a `--tier` other
     than `none`;
   - `protected`: a `--touch` path matches the shipped list or `protect=`.
     Each path is matched as given and as its resolved real path, on every
     component, after `\` folds to `/` and invariant 13's NFC lower case.
     A `protect=` prefix matches any component suffix in invariant 14's
     shape, so `internal` covers `/x/repo/internal/cli/a.go`;
   - `no-touch`: an editing class (code, writing, data, ops) with no
     `--touch`.
7. `pick=<id>[@<effort>] by=assign`.
8. Otherwise `pick=<default> by=default why=not-assigned`.

**The R5 closure** (section 14). NEW: `--tier` takes a fourth value, `none`,
for a user with no tiers. Every non-default pick requires `--tier`. `none` is
admitted only against a line without `tiers=`, and `chore` or `lite` only
against a line whose `tiers=` lists them. `full` is never admitted. An
omitted `--tier` defaults, whatever the line says. The residual moves from
an omission to a false statement: a coordinator must type `--tier none` for
a full-tier item to reach an assignment. A coordinator passes the item's
tier, as the router's SKILL.md teaches (the fleet skill names no pick
recipe), and its brief carries it.

**Output.** One line per pick:

```
pick=<id>[@<effort>] by=<assign|default> [why=<clause>] cap=<path> sha256=<12 hex> decided=<date>
```

- The cap path goes through one fence. Any byte outside 0x21-0x7e becomes
  `?`, and the path is capped at 256 bytes, in invariant 9's shape. Every
  other value comes from a fixed list, or is a number, a gated id, a hex
  digest or a date.
- `pick --all` prints a first line saying the print is data and confers no
  authority, then one line per class: `class=<c> pick=...`. A row that would
  be an assignment but needs paths prints the default with `why=no-touch`,
  as `needs --touch` in section 7.
- `scan` and `cap-template` open with the same line.

**Sources and refusals** (section 2), with these NEW details:

- Files are read in (mtime, path) order, so a resumed session's original
  file comes before its copy.
- CHANGED (design pass D3): every line is parsed. A record whose `type` is
  exactly `api-request`, `api-request-blob` or `api-request-shape` is then
  skipped. Section 2's "skipped before parsing" let an interior line holding
  only `{"type":"api-request` pass as skipped instead of refusing.
- Malformed means `json.loads` failed or gave a non-object; an empty
  interior line is malformed.
- Subagent transcripts are `<project>/<session>/subagents/**/agent-*.jsonl`.
  The alias-drift count joins an Agent call's `input.model` to the model of
  `agent-<toolUseResult.agentId>.jsonl`. Ids and aliases are held as salted
  in-memory hashes, and only the count is printed.

**Duplicates, NEW detail.** Each record gets an in-memory digest: a salted
hash of the RAW values of every field the scan reads, before any reduction
(design pass D12). `pytest a.py` and `pytest b.py` both reduce to `test`,
but they differ, so they conflict. A second copy with an equal digest is
skipped. A copy with a different digest is a conflict. **Every task that
holds any copy of a conflicting uuid is left out of every cell,** equal copies
included (D11: copies A and C agree and B differs, so all three tasks go).
Dropping one record would change its task's class. `conflicts=` counts the
uuids, and `conflict_tasks=` counts the tasks.

**The table** (section 7) is TSV with:
- a magic and version line;
- one header line of `key=value` fields, each validated on read;
- the column names, then the rows;
- a trailer, `#end rows=<n> sha256=<hex of every byte above>`.

The row key is `role class tier model eligible`:
- `role` is `main`, `sub` or `codex`;
- `tier` is `-` in phase 2;
- `model` is `<id>@<effort>`, or `(other)@<effort>` for an id not named.

Ids are folded BEFORE rows are built, so two folded ids share one row.

- Every Codex id folds to `(other)` (section 3).
- Signals are `fired/covered`, or `-` where a role has no such field.
  Subagents have no interrupt and no end state (M9). Codex has no
  `user_reject` and no `policy_denied`.
- `covered` counts the tasks whose every record falls inside the field's
  shipped version range. The ranges come from section 0's coverage table
  (`interruptedMessageId` 2.1.292 to 2.1.322; `permissionDecision` and
  `toolDenialKind` 2.1.296 to 2.1.322). A newer harness reads as not
  covered until the ranges are re-shipped. Phase 2 only prints signals, so
  that costs a display.

**`cap-template`** reads the table (validated, never re-scanned) and prints:
- a draft line with `decided=` blank, which parses as no cap;
- `default=` set to the main model and effort of the most main tasks;
- CHANGED after measurement (M17): a `# assign= candidates` line, never
  `assign=` in the draft line itself. For each routable class it names the
  most-used non-default main model with at least 10 tasks in that class, and
  the user moves in only the routes they choose. Dated unedited, the draft
  routes nothing off the default;
- a `#` line naming the ids the naming rule names (CHANGED, section 3: 10
  or more kept tasks, at most 8), as candidates for `review=`;
- one `#` line per candidate, with its counts and "confounded: chosen per
  task".

A candidate can name a model the user keeps for reviews. Moved into the line,
it refuses (a `review=` id in `assign=`) once the user fills in `review=`,
which is the safe direction. Here, 2 of 5 candidates did (M17).

**Writes.** The state dir is `${XDG_STATE_HOME:-~/.local/state}/model-router/`.
- It is created 0700, and an `XDG_STATE_HOME` that is not absolute is
  ignored.
- Each target's directory is resolved and refused inside a work tree,
  before any byte is written. That covers the table, `refused.txt` and the
  `--out` file.
- An existing target that is a symlink or not a regular file is refused.
- A write goes to a temp file in the same directory (0600), is fsynced,
  renamed over the target, and the directory is fsynced.

**The skill tests** (section 8). In `internal/cli/discover_help_test.go`:
- `skillFiles` gains the three files.
- Each file gets a kind:
  - a buddy skill keeps its floor: 20 names for a SKILL.md, 5 for a
    reference file;
  - a **no-verb** skill (`model-router`) must report no error AND check
    exactly 0 buddy names. A drop-in that works without buddy names no
    buddy verb, and a bare backticked `--flag` is already an error ("a flag
    with no verb named before it"), so its flags are written inside
    `router.py ...` spans.
- The no-verb rule's positive control: the same checker, run on each of
  those files with a planted `buddy no-such-verb` span and a planted bare
  `--flag` span, must report both. So "0 checked" means the file was read
  under the rule, not skipped.
- The YAML plain-scalar description check covers every SKILL.md, not only
  the first three.
- `check.sh`: the compile sweep and the `assert` scan cover `skills/*/*.py`,
  and the hermetic tier runs `python3 -I -B skills/model-router/router.py
  --self-test`.

**`--self-test`** runs in process, against fixtures it writes under
`tempfile.mkdtemp()`. It refuses to run if that directory is inside a work
tree. It covers every case in section 10's phase 2 row, each refusal with
its positive control. The canary is an upper-case token, so no fixed list
and no id gate can pass it. It is planted in every text and enum-shaped
field: message text, tool inputs, commands, paths, cwd, session ids, uuids,
`model`, `effort`, `stop_reason`, `turnOrigin`, the `permissionDecision`
fields, `toolDenialKind`, `version`, the Agent `model` input, and the Codex
fields. The test fails if the canary reaches stdout, stderr or the table.
`refused.txt` holds paths by design, so the test checks it holds only the
refused files' paths. A lower-case gate-passing id planted in 12 tasks is the
stated residual: it is named in the pre-cap draft, and as `(other)` under a
cap.

**Prose budget.** Two keys at the formula: `skills/model-router/SKILL.md`
and `skills/model-router/SKILL.md:description`. The every-request class has
3,566 bytes of headroom (33,298 of 36,864) and the per-session class 7,984
(73,936 of 81,920), measured at `1f4fc58`.

**Records.**
- D-177, the next free number. The `decision` ids space's ceiling, 145,
  trails the file, and seeding it is the operator's act.
- CLAUDE.md: the skills line gains the number, and the repo map's skills
  paragraph names the fourth skill.
- `docs/README.md`: this plan's row.
- `docs/USAGE.md`: a short section pointing at `reference.md`.
- This plan: status, the measurements (M14 onward), and what stays open.

**The Codex design pass on this section.** One pass on 1f4fc58..2ca9f22,
through `scripts/codex-review.sh`: gpt-6-astra at xhigh, header verified,
`CODEX_CHARTER=process`, 497 s, rc 0. Verdict: "build with the listed
fixes". It answered Q2 (cap widening) and Q6 (the test change) NO defect, Q4
(R5) sound. **It raised 12: 6 P2 and 6 notes. 10 were fixed in the design,
and 2 are answered.** The code follows the fixes.

| # | finding | taken |
|---|---|---|
| D1 | P2. A refusal "names the flag", so an unknown argv token holding a newline could be echoed into a fake output line | fixed: every refusal is a constant string; a flag is named only from the parser's own table |
| D2 | note. The `refused.txt` location could carry a newline from `XDG_STATE_HOME` | fixed: that path goes through the same fence as the cap path |
| D3 | P2. The `api-request` prefix skip let a torn interior line pass | fixed: every line is parsed, then skipped by exact `type` (above) |
| D4 | P2. An effort trial with no `@` on `default=` parsed (section 6) | fixed: when `trial=` and `default=` name one id, both carry `@<effort>` and the efforts differ |
| D5 | P2. `starts=` after `ends=`, or a window past 14 days, parsed (section 5, F19) | fixed: `starts=` <= `ends=`, at most 14 days apart |
| D6 | note. `claude-example-1-latest` passes the gate; a harness alias of that shape would be pinned to nothing | in part: an id with a `latest` component refuses. A moving alias of another shape is a residual (section 14) |
| D7 | note. `route-cap` followed by a tab is recognized, which section 6's literal `route-cap ` was not | fixed, CHANGED: recognized so that it refuses, rather than being silently ignored |
| D8 | note. A single pick printed no "confers no authority" line (section 7) | fixed: every print opens with that line, a pick included |
| D9 | note. `check-skill.sh` and `uninstall.sh` were outside the reads it was allowed | answered: the hermetic tier runs both against the fourth directory |
| D10 | note. Section 10's measurements must be in the acceptance list | answered: they are M14 onward, below |
| D11 | P2. Three copies of one uuid, A and C equal and B different: "both tasks" leaves C counted | fixed: every task holding any copy goes (above) |
| D12 | P2. Reduced-label equality is weaker than section 2's "every field the scan reads" | fixed: the digest is over raw values (above) |

**The fold: panel, cross-check, code pass, re-verify.** The panel read
005c4ed: four read-only lenses, three on Opus at xhigh (the fence and the
canary, the cap's refusals, portability) and one on Fable at xhigh
(injection through transcripts into a model's context, and privacy). The
Fable cross-check read the same at xhigh. The Codex code pass read 35a9212
as two scoped passes (A: refusals, fences and the pick, 348 s; B: the table
reader, the scan and the tests, 319 s). The re-verify read each round's
fixes (203 s and 206 s). Every Codex pass ran gpt-6-astra at xhigh, header
verified, `CODEX_CHARTER=process`, rc 0. Each fix has a test watched to fail
without it (M20). CHANGED marks a change to what this section says above.

| # | finding | taken |
|---|---|---|
| G1 | P1, cross-check. An `@` suffix outside the effort list (`@ultra`, `@x@high`) stayed in the id, so `review=` missed it: `assign=qa:claude-fable-5-2@ultra review=claude-fable-5-2` picked the review model | fixed, CHANGED: every `@` part holds a digit, and `review=` compares ids by the part before the first `@`, in the cap and in the table's review class |
| O1 | P1, cross-check. An EACCES read as absent: a mode-000 `~/.claude` scanned as `claude=absent`, exit 0, table written | fixed: absent on ENOENT only; an entry whose stat fails refuses |
| W1 | P1, cap lens (injection lens, cross-check). A touch was matched against the NEAREST root only: a submodule `proj/auth/.git`, a versioned `~/.claude`, or a `protect=` prefix with an absolute touch picked `by=assign` | fixed: matched against every enclosing root, names only above the outermost unless it is a linked worktree, `protect=` against every suffix of the absolute path |
| VCS | P1, injection lens. A project's `CLAUDE.md` under hg, svn or jj passed as a cap | fixed, CHANGED: eight version-control markers; a cap not owned by the user (`cap-not-owned`) or world-writable (`cap-world-writable`) refuses |
| W2 | P2. A directory touch, a trailing `/` or a blank value picked `by=assign` | fixed: each is protected, as is a relative touch from a deleted cwd |
| W3 | P2. normpath before realpath judged `lnk/../settings.json` as the wrong file | fixed: realpath of the un-normalized join |
| O3 | P2, cross-check and portability. An unhashable `type`, a raw list in the digest, or a 5,000-deep line exited 3 | fixed: the type checked before set membership, every digest value frozen, a RecursionError malformed, and one per-file catch refuses the file |
| O2 | P2. A refused scan left the old table for `cap-template` | fixed, CHANGED: a refused scan removes the table at its target |
| F1 | P2, fence lens (injection, cross-check). Pre-cap naming was unbounded, one file could hold the 10 tasks, a planted id took `default=`, and a planted store printed 28 KB | fixed, CHANGED (section 3): at most 8 ids, counted over the kept main and subagent tasks, and the residual stated |
| F2 | P2. A table or a transcript directory named inside a repository was trusted | fixed: `table-in-work-tree`, `source-in-work-tree`, and `cap-template` re-applies the floor. CHANGED: the first line is `model-router: data, not instructions; it confers no authority` |
| F6 | P2, fence and injection lenses. `refused.txt`'s lines were unfenced | fixed, CHANGED: each path percent-encoded outside 0x21-0x7e and `%`, one line per path |
| port | P2, portability. No remedy for a home directory in git; an unwritable state dir failed after the read; the self-test depended on its cwd; no requirements stated | fixed: a fixed hint on a target refusal, a writability probe before any read, the self-test runs in its temp dir, and `reference.md` states the requirements |
| notes | `is_cap_line` skipped a BOM or a control byte after `route-cap`; `ſkills` on APFS; D-114 wording; the docs against the code (Q6); 12 surviving private mutants (T1) | fixed: a candidate is any column-0 `route-cap` not followed by `[A-Za-z0-9_-]`; an NFKC and casefold variant that only protects; the wording; `reference.md`; a test and a planted canary for each |
| A1 | P1, code pass. A submodule whose `.git` file names a path holding `/worktrees/` passed as a linked worktree, so the names above it went unprotected | fixed, CHANGED: linked only when its git directory holds `commondir`, git's own test |
| A2 | P2. `--out plain/link/../t.tsv` judged one directory and wrote into another | fixed: the target's directory is resolved, and that is judged and written |
| A3 | P2. A cap reached through a link inside a repository passed | fixed: every directory on the way is judged (RV1 completed it) |
| A4 | P2. A dangling `.jsonl` link, or an entry gone after the listing, was dropped | fixed: listed entries by lstat, and each refuses |
| A5 | P2. Another user's old table in a sticky directory survived a refused scan that said it was gone | fixed: `target-not-owned`, and the refusal is truthful when an unlink fails |
| B1 | P2. The table's reader accepted an id below the naming floor | fixed, CHANGED: `table-naming` |
| B2 | P2. A table at the size limit with bytes after it read as its prefix | fixed: one byte past the limit refuses (`table-too-large`) |
| B3 | P2. `requestId` was missing from the digest | fixed |
| B4 | P2. The digest took `1000` and `1000.0` for one value | fixed: scalars tagged by type |
| B5 | P2. A main-file sidechain copy is skipped before the dedup | answered (section 14) |
| B6 | P2. Codex records are never deduped | answered; a resumed rollout's copies NOT MEASURED (section 14) |
| B7 | note. The canary under `item.kind` never reached its read | fixed: planted under `type: Extension` |
| RV1 | P1, re-verify round 1. `plain/entry -> repo/escape -> store`: realpath of each prefix jumps past `repo` | fixed: the path is resolved one component at a time, as the kernel does, every directory it stands in is judged, and past 40 links it refuses |
| RV2 | two notes, re-verify round 2 ("ship as is") | answered (section 14) |

The lenses raised 19, counted from the lane's triage list (some raised by
more than one lens), and the cross-check 9: all fixed. The code pass raised
12: 10 fixed, 2 answered. Round 1 raised 1, fixed, and accepted B5's and
B6's answers. Round 2 raised 2 notes, both answered: the round ceiling.

## 16. Phase 2: what was measured

`router.py` as built, on this machine, 2026-11-12. Two scans ran minutes
apart: the shipped `scan` (its print and table), and a counts-only harness
over the same `Scan` object, for the numbers the print does not carry
(`measure.py` in the lane's scratchpad, as the probe was; F22's repository
copy stays OPEN), and a sum over the table's own rows for the per-cell
figures (`agg.py`, beside it). Both print integers and ids the script itself names, and no
text. The window moves between runs (M2), so a figure drifts by a few tasks.

```sh
XDG_STATE_HOME=<dir> /usr/bin/time -l python3 -I skills/model-router/router.py scan --out <dir>/table.tsv
python3 -I skills/model-router/router.py cap-template --table <dir>/table.tsv
python3 -I skills/model-router/router.py pick --cap <file outside any work tree> --role main --all --tier none
python3 -I -B skills/model-router/router.py --self-test
```

| # | figure |
|---|---|
| M14 | **Version ranges,** measured for the shipped `CLAUDE_RANGES`: `interruptedMessageId` 2.1.292 to 2.1.322; `permissionDecision` and `toolDenialKind` 2.1.296 to 2.1.322; Codex `turn_aborted` interrupts 0.144.3 to 0.166.0, in rollouts 0.136.0 to 0.166.0. Every Claude user record carries a version |
| M15 | **Read cost and print sizes,** at the lane's tip after every fold (b130a28). One scan: 60.0 s wall, 45.1 s user and 3.5 s system, peak RSS 283 MB, at a 1-minute load of 9.6 (12 CPUs). It parses every line, `api-request` records included (D3), and is still faster than the probe's 88-197 s (M3), which also read the Codex state database and the ledger. (The build's run, before the fold: 61.7 s wall at a load of 51.) The print is **1,770 bytes**, a 137-byte table path among them (section 9 estimated at most 2 KB). The table is 10,367 bytes, 135 rows. `cap-template` prints 1,334 bytes, one `pick` 296 and `pick --all` 702, each with a 136-byte cap path. The self-test: 345 checks; 103 runs of it took 13.9 s on 4 workers (M20) |
| M16 | **Duplicates.** 28 uuids carry the **112** extra copies section 2 counted. **16 of the 28 conflict** on raw values (D12), unchanged by the fold's `requestId` and typed values (B3, B4). They take **5 tasks** out of every cell (`conflict_tasks=5`), all of them subagent tasks: 0 of 2,621 main, 5 of 2,007 sub, 0 of 5,807 Codex |
| M17 | **The draft against the operator's actual rules** (`~/.claude/CLAUDE.md`, `~/.claude/fleet-decisions.md`: every lane on the default lane model at high effort unless the operator names one; Fable for reviews only; Sonnet or Opus for side work). `default=claude-opus-5-5@high` matches the lane rule. Section 15's rule drafted **five assignments** into the line: code, research and qa to `claude-sonnet-5-5` (19 of 732, 19 of 101 and 71 of 450 main tasks), and writing and ops to `claude-fable-5-1` (21 of 579, 85 of 427). **None of the five matches the operator's rules.** Two put a review model on a class, so the line refuses once `review=` names it, which is the safe direction. The other three would have routed lanes off the default. So the candidates moved out of the line (section 15, CHANGED): the line now drafts `default=` alone, and the five sit on a `# assign= candidates` line |
| M18 | **M4-M7 reproduced** (the tip's run; the probe's figure in parentheses). M4, main requests: `claude-opus-5-5` 127,864 of 134,472 (95.1%; probe 124,306 of 130,902), `claude-fable-5-1` 3,204 (3,204), `claude-fable-5-2` 1,255 (1,243), `claude-sonnet-5-5` 696 (696), `(other)` 1,453, which is the probe's `opus-4-7` 817 plus `opus-4-8` 636, each under the naming floor. Subagents: 43,401, 7,648, 2,880 and 1,522 (42,820, 7,638, 2,844 and 1,182). M5: 1,125 main files, 2,621 episodes, 2,007 subagent tasks and 5,807 Codex rollouts (1,097, 2,585, 1,967, 5,742). M6, main cells: code opus 698, sonnet 19, fable-5-2 10; writing opus 542, fable-5-1 21, fable-5-2 12; qa opus 342, sonnet 71, fable-5-1 35; ops opus 340, fable-5-1 85; research opus 78, sonnet 19. Each is within a few tasks of the probe. Codex by class (ids `(other)`): qa 5,032, research 501, ops 98, code 85, writing 65. M7: main requests on tasks led by opus-5-5, high 123,875 and xhigh 3,930 (120,750, 3,556). Episodes on opus-5-5, high vs xhigh: code 671 vs 27, writing 508 vs 34, qa 327 vs 15, ops 327 vs 13 |
| M19 | **M8 and M9 signals reproduced** (main tasks, fired/covered; the probe's in parentheses): `interrupt` 4/2,322 (4), `user_reject` 10/2,306 (10), `policy_denied` 1,012/2,306 (968, over cells of 10 or more), `test_red_end` 6/1,313 (6 of 1,262), `test_masked` 813/1,313 (764), `abandoned` 251/2,325 (233). Calls per task: main 60.1, sub 32.1, Codex 4.2. `tested`: main 1,313, sub 606, Codex 207. Subagent interrupts are 0/0, so none are covered and none are read as zero (M9). Codex interrupts: code 18/75, writing 13/59, research 11/487, qa 5/4,833. Protected tasks (not eligible): main 244, sub 23, Codex 30 |
| M20 | **Mutation** (each in a private copy, run from a cwd outside its fixtures; the self-test must fail): **103 of 103 killed** at the tip. At the build, 41 of 41. The first run, of 39, left ONE survivor, and the test was fixed: a `--class` value printed in its refusal. The planted argv value began with `--`, so it stopped at "needs a value" before any value check ran. A value without the dashes now reaches the `--class`, `--role` and `--tier` checks, and the second run adds the `--role` and `--tier` mutants. Before the run, a new `t_fence` test was written for the fence (a newline in the cap path, `--out` or `XDG_STATE_HOME`), and the fence-identity mutant dies on it. The fold's mutants then ran after each fold: 97 after the code pass's, with one survivor (a `--claude` judged at itself only), whose test was then written, 97 of 97; and 6 more for re-verify round 1's fix, 103 of 103. The lists follow the table |
| M21 | **Context bytes** (`check-prose-budget.sh --sizes`, at the tip). Description 395 bytes, on every request once installed. Body 3,694 of its 4,096 ceiling, per session that loads it. `reference.md` about 11 KB, read on demand. The every-request class is 33,904 of 36,864, and per-session 77,630 of 81,920. Neither class is raised |

The mutants, each as mutant -> the first check that failed:
- **leaks:** a raw Claude effort or a raw Codex effort -> "no canary in any
  output, error or table"; a `--class`, `--role` or `--tier` value, or an
  unknown argv token, in its refusal -> "argv refusal is constant"; an
  exception's message printed -> "an exception prints its type only"; the
  fence made the identity -> "a newline in the cap path stays on the pick line".
- **the id gate:** no digit required, or a `latest` component allowed (D6) ->
  "cap refusal bad-id"; the naming floor applied under a cap (C1) -> "under a
  cap, only the cap's ids are named".
- **the table:** the trailer hash, the row count, the duplicate cell key or
  the row vocabulary each left unchecked -> its own "table refusal" check.
- **the cap:** `review-id-routed`, `roles-sub`, `bad-byte`, the 14-day trial
  window (D5), `effort-trial-needs-both` (D4) or `class-assigned-and-trial`
  removed -> its own "cap refusal" check; the file's work-tree refusal removed
  -> "a cap in a work tree, named from outside it, refuses"; that refusal
  judged at the cwd (C9) -> "cap file control picks"; two cap lines accepted ->
  "cap file refusal two-cap-lines".
- **the pick:** `no-tier` moved after the assignment, R5 reopened (an omitted
  tier admitted), `full` admitted, `no-touch` removed, the realpath dropped,
  protection on the last component only (F4), or `protect=` ignored -> the
  pick case or symlink check that names that clause.
- **sources:** an interior torn line accepted, or an unreadable file skipped
  -> "a refused scan prints no canary and writes no table"; `api-request`
  skipped before parsing (D3) -> "a torn api-request line (D3) exits 2"; an
  unreadable source read as absent -> "a projects path that is not a
  directory refuses".
- **duplicates:** the later copies' owners dropped (D11), or the digest taken
  over reduced labels (D12) -> "a raw-value conflict (same label) takes all
  three copies' tasks out".
- **signals and writes:** version coverage always true -> "a version outside a
  range is not covered"; the target's work-tree refusal removed -> "a state
  dir inside a work tree refuses before writing"; a symlinked target accepted
  -> "a symlinked --out refuses"; the temp file at 0644 -> "refused.txt is
  0600".

The fold's 62 mutants, each killed by the fold's own test for it (the
mutation script names the first failing check of each):
- **the cross-check's and the panel's** (G1, O1-O3, W1-W3, F1, F2, F6):
  the `@` rule back to effort words or removed, a punctuation first byte,
  `review=` compared by string in the cap or in the scan; an OSError or a
  dangling link read as absent, `lexists` or `isdir` back, an entry of
  unknown kind skipped; the nearest root only, the names above the outermost
  root unchecked, the linked-worktree exception removed, `protect=` not
  matched on the absolute path or its tail re-applying the names, the climb
  stopping at the first root, `.git` the only marker; a directory, a blank or
  a trailing-slash touch routed; realpath of the normalized join; the NFKC
  variant removed; an uncaught `getcwd`; the cap's lexical directory, owner
  or world-writable check removed; `is_cap_line` narrowed to a space, tab, CR
  or end, or the BOM kept; the `type` tested raw, a raw `sessionId` in the
  digest, RecursionError uncaught, the per-file catch narrowed; a refused
  scan keeping the old table; no bound on pre-cap names, out-of-scope tasks
  counted, the scan's id gate removed; a `--claude`, `--codex` or `--table`
  in a tree accepted, `cap-template` drafting any named id; `refused.txt`'s
  paths raw; a non-table `--out` replaced, no writability probe, no remedy
  hint; the lexical absolute touch dropped; the self-test not in its temp
  dir.
- **the code pass's** (A1-A5, B1-B4): a linked worktree by substring; the
  target not resolved; the cap judged at its target and lexical ancestors,
  or a `--claude` at itself; a dangling `.jsonl` or a vanished entry
  skipped; an unowned target accepted; the reader's naming rule removed, or a
  Codex row allowed to name an id; the size check removed, or the read capped
  at the limit; `requestId` out of the digest; scalars untagged.
- **re-verify round 1's** (RV1): each prefix resolved on its own again; an
  absolute link target taken as relative; links not followed; `..` a lexical
  no-op; the loop guard answering not-in-tree; a visited directory not
  judged.
