# A drop-in model router skill: routes from a user's own past sessions, counts only

Status: PLAN, nothing built (2026-11-12). Written by the `router-skill` lane
(Opus 5.5, xhigh) under orc-20's brief. Base: main at 03da68d.

Sources:
- the operator's request;
- the read-only probe in section 0;
- `ROUTING-PLAN.md` and the prior plans;
- one Fable consultation, folded (section 11);
- one Codex design pass, folded by the successor lane `router-skill-2`
  (section 12).

The Fable cross-check of that fold and one Codex re-verify are in section
13, both folded. The round ceiling is reached.
Nothing here is a decision and no skill ships with it. The operator decides
from this plan, and what becomes a decision takes its D-number when a phase
lands.

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
  and `cap-template` name ids that pass the gate and are seen in at least 10
  tasks, since the user needs them to write the cap. The table names ids by
  the same rule. A pick or a report reads only a table scanned under its own
  cap (rule 4 checks the cap's sha256), so a draft-era id never reaches either.
  **Residual:** a gate-passing token, such as a hyphenated phrase, planted in
  10 transcripts could reach a draft the user reads. The SKILL.md has the
  model show the draft as data and act on none of it;
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
   buddy. The fleet skill's pick recipe always passes `--tier`.
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
   - `assign=` lists each class where the user already ran a second model at
     least 10 times (here, qa: 71);
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

- **The operator's decisions:**
  - whether phase 2 is wanted;
  - its home (section 8);
  - its tier (section 10);
  - `assign=` (section 6);
  - subagent routing (below);
  - one parser for ROUTING-PLAN step 4 (section 6);
  - whether the probe is kept in the repository (section 0);
  - whether the M-table's machine profile stays in a public plan (F16).
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
  - **R5, X2's residual.** A full-tier call that omits `--tier`, against a
    line with no `tiers=` and with an `assign=`, gets the assignment. The
    closure, for phase 2's own design pass: every non-default pick requires
    `--tier`, with a `none` value for a user without tiers, so an omission
    defaults. It is not folded here, because no review round is left to check
    a new value in the grammar.
  - R1-R4 were folded after the last review round, so no review has read
    them.
- **The pre-cap draft** can carry a gate-passing token planted in 10
  transcripts (section 3, C1). The draft is shown as data, and the user writes
  the cap.
- **How many of the 112 duplicate records conflict** is NOT MEASURED (section
  2). Phase 2 measures it.
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
