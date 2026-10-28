# Cheaper models for subagents and skill work, and a fresh orchestrator per batch

Status: PLAN, nothing built (2026-10-28). Written by the `cheap-models` lane on
Fable 5.1 (the operator named the item for it), from Claude Code's own usage
suggestions to the operator, a docs check, the cost audit orc-10 ran
(2026-10-24..28, two repositories, counts only), three measurements this lane
took the same afternoon, and two Codex design passes over the draft (section 6:
both said revise, the second on one point the third measurement then closed;
every finding of both is folded and listed). Base: main at 01f722e.

Register: every part names the failure it answers, the number it rests on, the
alternative it refuses, and what it cuts. A number not taken says NOT MEASURED;
a fact taken from the Claude Code docs and not reproduced here says DOCUMENTED.
Anything the operator has to decide is marked **OPEN**. Every dollar figure is
an ESTIMATE from the audit's ratio model (cache read 0.1x the input price, a
1-hour cache write 2x, a 5-minute write 1.25x, output 5x; input fitted from
cost-state at about $2.45/M for Opus 5.5 and $5.3/M for Fable 5.1; Sonnet 5.5
ASSUMED at $1.5/M and Opus 4.8 at $5.0/M, as the audit assumed; no Haiku rate),
with its derivation beside it. The audit's per-session dollars (orc-2 to orc-8,
$28 to $42 each) are a different measure, the harness's own cost-state, and
are named as such where used.

## The operator's request

2026-10-28, in orc-10's pane, quoting Claude Code's usage suggestions: "84%
Give general-purpose subagents a cheaper model", "67% Start fresh when you
switch tasks", "give buddy subagents a cheaper model"; then "run /buddy on a
cheaper model"; then "open a fable planning lane as needed for that once
researched". The percentages are the harness's own estimate of how much of the
operator's usage each suggestion touches; nothing here reproduces them, and
section 1 says why the first one does not fit this fleet as stated.

## 0. What was measured for this plan

**Where the subagent calls went** (every main-session transcript modified since
2026-10-24 under `~/.claude/projects`, 197 files, 87 with Agent calls, counted
before this lane's own runs added five more; the script and its output are in
the lane's scratchpad, counts and 70-character descriptions only):

| Agent calls by model param | review-shaped | other | total |
|---|---|---|---|
| `fable` | 88 | 2 | 90 |
| `opus` | 35 | 12 | 47 |
| `sonnet` | 23 | 2 | 25 |
| none (inherits the parent) | 23 | 8 | 31 |
| of which Explore (not changed by the default variable) | 12 | 0 | 12 |
| of which general-purpose | 11 | 6 | 17 |
| of which a fork | 0 | 2 | 2 |

"Review-shaped" is a regex over the call's description and the first 400
characters of its prompt (review, panel, lens, cross-check, re-verify, ruling,
audit); it says what the call was asked for, not what it cost. The 90 Fable
calls sorted by hand from their descriptions: 44 cross-checks of a diff, 30
panel lenses (bypass, state and ordering, hostile input, auth and fence,
secrets), 5 review passes or question sets on a design, 4 rulings, 4 design
reads, 2 re-verify rounds, and 1 piece of non-review work (a trading-tool
capture loop): 89 reviews and 1 other; the regex said 88 and 2 because it
missed one ruling. So of the audit's ESTIMATED $89 of Fable subagent spend
(531 requests, ratio model), the part a description can classify is almost
all review work D-111 sanctions; how the dollars split between those calls was
not measured (one transcript per call, not costed). The 17 inherited
general-purpose calls are where a default would bite: 9 from Opus parents (7
review-shaped: lane panel lenses and design reads spawned without a model
param), 5 from Fable parents, 3 this lane's measurement runs. The five from
Fable parents, by description: one review ("Fable review of homepage hero end
view": a Fable review that FORGOT its param and got Fable by luck), and four
pieces of side work (a Codex pass run through the review script, a Codex
second opinion, a measurement run, a skill edit) that got Fable by inheritance
against the operator's rule.

**Whether a settings `env` block reaches the subagent default** (MEASURED,
2026-10-28 15:49, Claude Code 2.1.312, three `claude -p` runs from a non-repo
scratch directory, main on Sonnet, each asking for one general-purpose
subagent that uses no tools; the subagent's model read from
`~/.claude/projects/<cwd>/<session>/subagents/agent-*.jsonl`):

| arm | where the variable was set | subagent's model string | cost-state |
|---|---|---|---|
| control | nowhere | `claude-sonnet-5-5` (the parent's) | $0.10 |
| shell | `CLAUDE_CODE_SUBAGENT_MODEL=haiku` in the environment | `claude-haiku-5-5` | $0.05 |
| settings | `{"env":{"CLAUDE_CODE_SUBAGENT_MODEL":"haiku"}}` via `--settings <file>`, shell unset | `claude-haiku-5-5` | $0.04 |

What this measured: a settings file's `env` block, read at startup, sets the
default. What it did not: the user file `~/.claude/settings.json` itself (the
operator's; the settings reference applies it and `--settings` by the same
startup rule: DOCUMENTED), a running session picking up a saved change
(DOCUMENTED, NOT MEASURED), a project or local settings file overriding the
user one (DOCUMENTED precedence; neither checkout here has an `env` block in
`.claude/settings.json`, checked 2026-10-28), and a subagent under a parent in
a repository with buddy's hooks live. Section 1.3 names the one cheap check
that closes the first gap after the operator adds the line.

**Whether a PreToolUse hook on the Agent tool can refuse an unnamed model
before any subagent runs** (MEASURED, 2026-10-28 16:18, two `claude -p` runs
as above, a `--settings` file wiring `PreToolUse` matcher `Agent` to a
40-line Python script that exits 2 with a one-sentence reason when
`tool_input.model` is absent on a general-purpose call):

| arm | the Agent call | what happened | subagent transcripts | cost-state |
|---|---|---|---|---|
| no model | `subagent_type: general-purpose`, no `model` | refused; the main session reported `REFUSED` and quoted the hook's sentence | 0 | $0.06 |
| named | the same with `model: sonnet` | ran, answered `done` | 1 | $0.10 |

The script's self-test (three stdin payloads: unnamed, named, Explore) exits
2, 0, 0. So the one thing prose cannot do, stop a forgotten `model` before it
executes, a hook does, and a session that hits it reads the rule at the moment
it matters.

The docs check (a Sonnet subagent over code.claude.com, quotes in its report)
confirmed the precedence the brief relied on, from v2.1.251: the call's
`model` param, then the agent definition's `model`, then
`CLAUDE_CODE_SUBAGENT_MODEL`, then the parent's model; `_FORCE=1` (v2.1.257+)
makes the variable beat the first two and also moves Explore and Plan; without
it the variable does not change Explore or Plan, and a fork always inherits.
Auto mode: Haiku 5.5 has it from v2.1.293 (Haiku 4.5 never did, which is the
measured "asks for every call" in the operator's file); Sonnet 5.5 has it. A
user-level agent that overrides a built-in is documented for Explore only
(`name: Explore`), not for general-purpose. All DOCUMENTED.

**How much Bash output buddy verbs produce** (same 197 files): 3,648 of 17,461
Bash results, 2.93 MB of 28.5 MB, 10.3% of all Bash result bytes. By verb:
`inbox` 588 KB over 277 calls, `who` 420 KB over 374, `claim` 380 KB over 544,
`wait` 328 KB over 569, `msg` 310 KB over 840; nothing else over 190 KB. The
audit put all Bash results' carry at 15% of cache reads, about $160 over the
four days; taking buddy's byte share of that gives about $16 (ESTIMATE: a
proportional attribution assuming equal token density and equal later
exposure, not a measured carry). Section 2 sizes the forked-skill idea
against it.

## 1. The default subagent model

**Failure it answers.** The operator's file says mechanical subagents go on
Sonnet 5.5 or Opus 5.5 and "never Fable by inheritance", and nothing enforces
it: an Agent call with no `model` runs on the parent. In four days five
general-purpose calls inherited Fable that way, four of them side work, and
one review got the model it wanted only because its parent happened to be
Fable. The usage suggestion reads as "set the default to something cheap";
measured, the Fable subagent calls were reviews by description, which a cheap
default must not touch. Both failures have the same cause, an unnamed model,
and the same fix: the call names it, or it does not run.

### 1.1 What each setting would change

Two columns of dollars, because the settings line is proposed AFTER the rule
and hook of 1.3, which pin every review: "history" is what the line would
have done to the last four days as they were; "after 1.3" is what it does once
no review call is unnamed, so only side work is left for a default to move.

| setting | calls it changes (of 193) | reviews it moves silently | history (ESTIMATE, 4 days) | after 1.3 (ESTIMATE, 4 days) |
|---|---|---|---|---|
| nothing (today) | 0 | 0 | $0; the rule stays unenforced | $0 |
| `CLAUDE_CODE_SUBAGENT_MODEL=claude-opus-5-5` | the 5 general-purpose calls inherited from Fable parents | 1 (the forgotten-param Fable review), to Opus | saves about $2.70 (0.15% of the $1,805 main spend) | saves about $2.10: the 4 side-work calls |
| `CLAUDE_CODE_SUBAGENT_MODEL=claude-sonnet-5-5` | the 14 inherited general-purpose calls (17 minus this lane's 3) | 8 (7 lane panel lenses and design reads from Opus parents, the one above) | saves about $3.90 more than the Opus line (0.37% cumulative) | about $1.40 more than the Opus line: 2 Opus-parent and 4 Fable-parent side-work calls |
| either, with `_FORCE=1` | all 193, Explore and Plan too | 88 Fable cross-checks and lenses, 35 Opus, 23 Sonnet | saves about $93 (5.1%), and the full tier's cross-check no longer exists | the same, and the hook of 1.3 is bypassed: the model a call names is ignored |

### 1.2 Derivations

Subagent spend per request, ratio model from the audit: Fable $89 over 531
requests, $0.168; Opus 5.5 $58 over 1,248 requests, $0.0465. Requests per
call: Fable 531 over 90 calls, 5.9; Opus 1,248 over 68 calls that ran on Opus
(47 named, 9 inherited general-purpose, 10 Explore, 2 forks), 18.4.

- Opus line, history: 5 Fable-inherited calls x 5.9 = 29.5 requests x $0.168
  = $4.95 on Fable; on Opus $4.95 x 2.45/5.3 = $2.29; saved $2.66, about
  $2.70. After 1.3 only the 4 side-work calls move: 4/5 of that, $2.13.
- Sonnet line, additional to the Opus line, history: 9 Opus-parent calls x
  18.4 = 165 requests x $0.0465 = $7.68 on Opus, $4.70 on Sonnet (x 1.5/2.45),
  saved $2.98; plus the 5 Fable-inherited calls landing on Sonnet ($1.40)
  rather than Opus ($2.29), $0.89; total $3.87, about $3.90. After 1.3 only
  side work moves: 2 of the 9 Opus-parent calls ($0.66) and 4 of the 5
  Fable-parent ones ($0.71): $1.37, about $1.40.
- FORCE: the audit's subagent total of $162 (Fable $89, Opus 5.5 $58, Opus 4.8
  $9, Sonnet $6) on Sonnet: 89 x 1.5/5.3 + 58 x 1.5/2.45 + 9 x 1.5/5.0 + 6 =
  25.2 + 35.5 + 2.7 + 6 = $69.4; saved $93, 5.1% of $1,805.

Every figure is the ratio model's; the harness's cost-state for the four days
was $1,805 for the main sessions, their subagents' usage included.

### 1.3 Recommendation: name it or it does not run; the settings line is the floor

The cheapest version that holds is one script, one skill sentence, one
settings line, in this order:

1. **The Agent-model hook (MEASURED above): a general-purpose Agent call with
   no `model` is refused before it runs**, with the rule as the reason:
   `claude-opus-5-5` for a panel lens or a lane's review subagent, `fable` only
   for a cross-check or a ruling the tier names, `claude-sonnet-5-5` for side
   work. Explore, Plan, forks and a named custom agent type are exempt (they
   inherit or carry their own model by design, and the data shows no unnamed
   call of those kinds that mattered). The script ships in this repository as
   `scripts/hooks/agent-model-gate.py` with the self-test the Python leg runs
   (step 4.1); the operator copies it beside their other gates and wires it by
   hand, the way `bg-gate.py` and `poll-gate.py` are wired today. The exact
   lines for the operator's `~/.claude/settings.json`, in `hooks.PreToolUse`:

   ```json
   {"matcher": "Agent", "hooks": [{"type": "command", "command": "python3 \"$HOME/bin/agent-model-gate.py\""}]}
   ```

   It fails OPEN on anything it cannot parse (exit 0): it teaches a
   convention, it is not a safety gate (invariant 2 is about buddy's ledger
   hooks), and a broken python must not stop every subagent on the machine.
   Measured cost per call is the Python start, under 50 ms; NOT MEASURED
   precisely. Why a hook and not prose: the second Codex pass's one blocking
   point was that a receipt naming Opus cannot tell a chosen model from an
   inherited one, and prose cannot stop the omission before execution. A
   hook can, and did.
2. **The skill sentence, where lanes and coordinators both read it:** the
   CORE skill's "Your review" (`skills/buddy/SKILL.md`), because a full-tier
   LANE spawns the panel and the core skill is all a lane loads: *every review
   subagent (a panel lens, a cross-check, a re-verify read, a ruling) names
   its model on the Agent call, and the receipt's `panel` names it
   (`panel@claude-opus-5-5`); side work names `claude-sonnet-5-5`.* The fleet
   skill repeats it in "Pick who does a piece by what it ends in". With the
   hook, the sentence is the rule's wording and the receipt's form; without
   the hook it is the rule alone, which is what the four days showed does
   not hold.
3. **The operator's settings line, as the floor beneath the hook** (**OPEN**:
   the operator takes it or not; the trade is stated here). The exact text for
   `~/.claude/settings.json`, beside the `BUDDY_HANDOFF_AT` already there:

   ```json
   "env": {
     "BUDDY_HANDOFF_AT": "300k",
     "CLAUDE_CODE_SUBAGENT_MODEL": "claude-opus-5-5"
   }
   ```

   The full id, not the alias: the operator's conventions name models by id
   so a reader can tell when a line was last right (the fleet-conventions
   example says the same). With the hook wired it changes nothing a
   general-purpose call does (every such call is named) and still catches
   what the hook exempts or misses: a custom agent type without a `model`
   field, a session on a HOME where the hook is not wired, a harness feature
   that spawns agents without the Agent tool (teammates, workflow agents:
   DOCUMENTED to read the variable). Without the hook it enforces "never
   Fable by inheritance" alone and moves a forgotten-param Fable review to
   Opus silently (one in four days); the operator who wires no hook should
   know that is the trade. Whether a running session picks the saved line up
   is DOCUMENTED, NOT MEASURED; the next session certainly does. The one
   cheap check that closes the user-file gap, run by the operator or a lane
   once the line is in: the lane's three-arm script with its `--settings` arm
   removed and the shell unset, from a repository directory, expecting
   `claude-opus-5-5` in the subagent transcript under a Sonnet parent (about
   $0.05, one minute).

**One report shows all three held** (step 4.3): the count of subagent
transcripts on Fable whose Agent call carried no `model` param (5 in the last
four days, target 0), and the count of review-shaped general-purpose calls
with no `model` param (11, target 0; Explore inherits by design and is not
counted), plus the hook's refusals if the operator keeps its stderr (NOT
MEASURED whether the harness records a refused call's hook output in the
transcript; the report counts what is there).

**Refused: `_FORCE=1`.** It makes `model: fable` on a cross-check a no-op,
moves Explore and Plan too, bypasses the hook's whole point (the model a call
names is ignored), and the full tier's receipt would then name a model that
did not run. **Refused: the Sonnet value now.** Eight review-shaped inherited
calls in four days means a lane's panel lens spawned without a model would run
on Sonnet with no trace in the receipt; with the hook wired, that call is
refused instead, and the Sonnet value then moves only side work for about
$1.40 more over four days: **OPEN**, decided from step 4.3's report, not now.
**Cut: a user-level `general-purpose` agent override** pinned to a cheap model:
not documented (only Explore is), and it would change the same calls the
variable changes, with a file to maintain. **Cut: a `side-work` agent
definition** (`~/.claude/agents/side-work.md`, `model: claude-sonnet-5-5`): it
enforces nothing an orchestrator forgot to type, exactly like the `model`
param it replaces; the forked skill in 2.2 is the version of that idea that
pins the model in a file the skill owns, and it is deferred on its own
numbers.

D-111 stands throughout: lanes, their panels and their fold rounds on Opus 5.5;
Fable for the cross-check, security-sensitive panel lenses and rulings, each
named on its call. The hook makes "named" a fact; the variable only decides
the case the hook does not see.

## 2. "/buddy on a cheaper model"

**Failure it answers.** The core skill (73 KB before skill-split, 22.6 KB after
it; the fleet skill 55 KB more for coordinators) loaded in 115 of 156 sessions
and rode every later request of each. The operator asked whether the skill's
work can run on a cheaper model.

### 2.1 The honest answer on the two skills

No, and the mechanism says why. SKILL.md's `model:` frontmatter switches the
MAIN session's model for the rest of the turn in which the skill is invoked
(DOCUMENTED; the session model resumes at the next prompt). The buddy skills
are invoked in a lane's first working turn, the one that claims and starts
editing, so a `model: claude-sonnet-5-5` pin would run that whole turn, edits
included, on Sonnet, and it would be a model switch mid-session, which the
prompt-caching doc says re-reads the whole history with no cache hit. With
`context: fork`, the skill text runs as a subagent's prompt with no
conversation history: a forked `buddy` skill would read its own instructions
in an empty room and teach the lane nothing. The skills are behaviour text,
not a job; their cost is their bytes on every request, and the lever for that
was skill-split (D-114): about 50 KB, roughly 12,500 tokens, off every request
of every lane that loads only the core. ESTIMATE from the audit's shape: 115
sessions averaging about 100 requests each read those bytes about 11,500
times, 145M token-reads, $35 (all Opus) to $76 (all Fable) over four days,
most of which skill-split already takes. A lane on a cheaper model is the
model-pick rule's business (BEYOND-CODE-PLAN section 5, `model-report.sh`),
not a skill setting. **Refused: `model:` on `buddy` or `buddy-fleet`.** **Cut:
a Haiku lane**: Haiku 5.5 has auto mode (DOCUMENTED, v2.1.293+; NOT MEASURED
here), but the pick rule picks within the operator's cap, and the operator's
default is Opus 5.5 (D-111).

### 2.2 What can go cheaper: candidates, sized

Each candidate is a forked skill (`context: fork`, `agent: general-purpose`,
`background: false`, `model: claude-sonnet-5-5`) whose output is a short
report, so the tool output stays out of the orchestrator's context and the
model is pinned by the file, not remembered per call (and a forked skill
carries its model, so the hook of 1.3 never sees an unnamed call from it).
Fork cost, ESTIMATE: the audit's subagents wrote a median first prefix of
about 38k tokens on the 5-minute tier; on Sonnet that is 38k x $1.5/M x 1.25 =
$0.07, plus five more requests reading it, 190k x $0.15/M = $0.03, about $0.10
per fork before its output and before any file it reads; NOT MEASURED.

| candidate | what it replaces in the orchestrator's pane | bytes it keeps out | gross saving per use (ESTIMATE) | modelled margin, before output and file reads |
|---|---|---|---|---|
| A. fleet digest: `buddy sessions`, `buddy who <run>` per run, `buddy ls`, `buddy sent`, one 1,500-byte digest back; one fork per use | 4-6 Bash results of 1-4 KB | about 15 KB, 4k tokens | 4k tokens x 150 later requests = 600k token-reads: $0.15 on an Opus orchestrator, $0.32 on Fable | +$0.05 to +$0.22 per use |
| B. landing evidence: per READY lane, `git diff --stat`, the receipt's text against the brief's tier, the message file's presence, the checks the lane NAMED; one report of what was found and what differs; one fork PER LANE | the lane's diff and files read in the pane | about 20 KB per lane, 80 KB for four | 20k tokens x 150 = 3M token-reads: $0.74 Opus, $1.59 Fable per four-lane batch, and the handoff comes later | four forks, $0.40: +$0.34 Opus to +$1.19 Fable per batch |
| C. `wait check` bookkeeping on the cheaper model | nothing: the check IS the main session's request, and keeping its cache warm is the point | 0 | 0 | not applicable |

A has a small positive modelled margin and an unmeasured total cost; it is
**cut on value**, not on arithmetic: buddy verbs are 10% of Bash bytes
fleet-wide (section 0, about $16 over four days by proportional estimate), the
orchestrator's share of that is smaller, and a skill file is a thing
`install-skill.sh` and the hermetic tier must hold forever for cents a use.
C is a category error: the cheap part of a `/loop buddy wait check` turn is
already the three small requests; the expensive part is that they read the
whole prompt, which only a smaller prompt fixes (section 3). **Cut.** B is
the one with a reason to exist, and the reason is not the dollars (about a
dollar a batch) but the two things it pins: the model, in the skill file, and
the orchestrator's context, which the fleet skill already says to protect
("Pick who does a piece by what it ends in": a finding goes to a subagent).
The skill prose already asks for this; a forked skill makes it one line
(`/buddy-land <lane>`) with a fixed prompt and a fixed model.

**B's boundary, if built.** It collects and compares NAMED evidence and
returns differences; it is not a review and never stands in for one. Its brief
forbids reading the diff for defects, judging the receipt's claims beyond
"present, names the tier the brief named, names each pass's effort", running
or re-running any check, or recommending land or hold. A difference it finds
goes to the reviewer the tier prescribes or to the orchestrator, on the review
model the tier names. Without that boundary a Sonnet fork reading receipts
against tiers becomes a Sonnet cross-check with no name in any receipt, which
is the exact failure section 1 exists to stop.

**OPEN, deferred to step 4.4**: build B only if step 4.3's first two weekly
reports show landing reads still landing in orchestrators' panes (a Read or
Bash result over 10 KB whose path or command names a lane's branch or
worktree, counted by that report). Its listing line costs every session on
the machine about 75 tokens a request (the skill listing is sent on every
request: 263 entries averaging 23 KB in the audit): 16,500 requests in four
days, $0.30 all on Opus or $0.66 all on Fable. Negligible.

### 2.3 What is NOT MEASURED about the fork path

Whether a forked skill's `$ARGUMENTS` reach the subagent prompt as the docs
say, and whether a Sonnet subagent under an auto-mode parent runs its Bash
calls without prompting (the classifier is the parent harness's; the measured
"asks for every call" was a Haiku 4.5 LANE, not a subagent). Step 4.4's first
act is that measurement, one `claude -p` as in section 0.

## 3. "Start fresh when you switch tasks": a fresh orchestrator per batch

**Failure it answers.** For lanes this is already the rule: one item per lane,
a fresh `<item>-2` at the handoff size (D-064, the operator's 2026-10-27
decision). For orchestrators it is not. orc-10 ran 14 hours across five
unrelated tasks (the audit, model-pick, report-gaps, trajectory, this batch)
in one session and sits near 240k: every request of this batch reads about
180k tokens of finished work. orc-2 to orc-8 each ran ONE batch and peaked at
292k to 400k ($28 to $42 each, cost-state, $230 for the seven): a batch of
lanes grows an orchestrator by 240k to 340k, so a second batch in the same
session reaches the 300k handoff mid-batch, which is the expensive moment to
hand off: lanes open and each to be re-pointed by message, runs held and
carried, jobs running (the fleet skill's measured trap: an outgoing
orchestrator that said it held nothing while a watcher still reported only
to it).

### 3.1 The convention

**A new batch of lanes gets a fresh orchestrator when the current one's last
observed prompt is past the operator's batch-boundary size (100k proposed,
OPEN) and the batch is independent of this conversation.** Independence is
not a feeling about relatedness; it is the handoff file's three registers
written and empty or handed ("Running jobs: none", "Timers and external runs:
none", "Owed: none", or each item delivered), the new batch's operator
decisions in files (its briefs, the decisions file), and the successor saying
it has read the file and can read every job it lists. A fold round on a lane
this session briefed is not independent (the lane's brief names this session
as its coordinator). A batch of unrelated items after every lane said DONE
and was closed is, once the registers say so. The handoff is the one the
fleet skill already defines ("Handing off a coordinator"), unchanged: finish
the round, write the file, re-claim `orc-<project>` pointing at it, open the
successor in a herdr tab carrying `BUDDY_HANDOFF_AT`, hand every claim in one
`release --to`, account for every job. A batch boundary is the cheap place to
run it, not a different recipe: no lanes to re-point, no run claims to carry.

What does NOT carry over, and the predecessor finishes before it retires:
mail addressed to its session id (the successor's `buddy inbox` is its own:
the predecessor drains `buddy inbox` and relays what is still open in the
file or by `buddy msg` to the successor), its own waits and `/loop` wakeups
(`buddy wait clear`, stop the loop), scheduled or background jobs (finished,
handed with their output file, or stopped: the recipe's step 5), and any
operator decision still only in its pane (written into the file as the
operator's, with the time). This is the same gap that refused `/clear` as the
fresh start (D-034: a new id strands mail queued to the old one), handled
here by the recipe rather than avoided.

The 100k is a proposal, **OPEN**: it is the fresh prefix (median 56k, p90
68k, the audit) plus one small task's working set, so that an orchestrator
that has only oriented itself and done a report keeps going, and one that has
run a batch does not. The operator's declined 250k handoff is a different
knob and is not touched: the handoff SIZE stays 300k; this is a second
TRIGGER that fires earlier only at a task boundary. It does not conflict with
the size: a session under 100k at a boundary keeps going and hands off at
300k as today.

### 3.2 The cost and the saving (ESTIMATE, both paths over the whole batch)

Both paths hand off once; the question is where. Take orc-10's case: 240k
when a new batch of about 150 requests arrives, the batch growing the prompt
about 1.5k tokens a request (the audit's per-batch growth of 240k-340k over
150-220 requests).

In place: 40 requests at 240k rising to 300k (average 270k: 10.8M
token-reads), then the handoff at 300k mid-batch (the handoff turn, about 5
requests at 300k, 1.5M; a fresh successor: a 56k prefix written at 2x on the
1-hour tier, $0.27 Opus or $0.59 Fable, and about 15 orientation requests at
60-80k, 1M; re-pointing each open lane by message, 4 `buddy msg` and 4 lane
turns reading them at about 200k, 2.4M on Opus lanes), then 110 requests from
56k rising to 221k (average 138k: 15.2M). Reads: 10.8 + 1.5 + 1 + 15.2 = 28.5M
on the orchestrator, 2.4M on the lanes.

At the boundary: the handoff turn at 240k (1.2M), the same fresh successor
(1M orientation, the same prefix write), then all 150 requests from 56k
rising to 281k (average 168k: 25.3M). Reads: 1.2 + 1 + 25.3 = 27.5M on the
orchestrator, nothing on the lanes.

Difference: about 1M token-reads on the orchestrator ($0.25 Opus, $0.53
Fable) plus the lanes' 2.4M ($0.59): roughly $0.85 on an Opus orchestrator,
$1.15 on Fable, per batch. The 40 early requests cost 184k more each in
place, but the in-place path's mid-batch restart then runs its last 110
requests 60k lighter than the boundary path's, and the two nearly cancel. So
the dollar case is about a dollar a batch, and the real case is the moment:
a handoff with no lanes open has nothing to re-point, nothing to carry, and no
job of a lane's to account for, and the successor orients itself on a quiet
ledger instead of four open lanes. Whether a fresh start also makes
mid-batch handoffs rarer is a HYPOTHESIS, not shown: a fresh 56k prefix
reaches 300k after 244k of growth, inside the observed 240k-340k range, and
no distribution of batch growth was taken. NOT MEASURED: a controlled pair of
batches; step 4.5's observation is footprints, not costs, and the plan says
so there.

**Refused: lowering the handoff size** (the operator kept 300k on 2026-10-28).
**Refused: `/clear` as the fresh start**: a new session id in the same
process, mail queued to the old id stranded, and the operator does not use it
(D-034 gotcha). **Cut: a buddy verb or ledger field for "batch"**: the trigger
is a sentence an orchestrator reads, and the ledger already shows the prompt
size (`buddy sessions`, the handoff word from D-113); buddy launches nothing
(D-056).

### 3.3 Where it lives

Three places, one text each, from one lane (step 4.1):

- `skills/buddy-fleet/SKILL.md`, "Handing off a coordinator", a bullet after
  "Hand off before you are full": *"Hand off at a batch boundary too: a new
  batch of lanes gets a fresh coordinator when your last observed prompt is
  past the operator's batch-boundary size (their standing decisions; none set
  means this bullet does not fire) and the batch is independent of this
  conversation: the handoff file's three registers written and empty or
  handed, the batch's decisions in files, the successor saying it has read
  the file. The recipe is the same and cheaper here: no lanes to re-point, no
  runs to carry. Measured: one coordinator that ran five unrelated tasks in
  one session carried 180k of finished work into every request of the fifth;
  the ones that ran one batch each peaked at 292k to 400k."*
- `docs/examples/fleet-conventions.md`, an operator line with its Why, in the
  example's register, the size in angle brackets.
- The operator's `~/.claude/CLAUDE.md`, "Running a fleet", a proposed line
  the operator adds or not (**OPEN**; the date and size are theirs to fill):
  *"**A fresh orchestrator per batch** (decided <date>): an orchestrator
  past <100k> of prompt that is handed a batch of lanes independent of its
  conversation (the fleet skill's test) hands off first, by the fleet skill's
  recipe. (Why: orc-10 ran five tasks in one session and carried 180k of
  finished work into every request of the last; each one-batch orchestrator
  peaked at 292k-400k, so a second batch in place reaches the 300k handoff
  mid-batch, with lanes open.)"*

## 4. Landing order

Each step says whether it SAVES, ENFORCES or MEASURES; only 4.2 saves anything
by itself and only a few dollars, and the plan is honest that the rest is
what makes the operator's rules hold and visible. Each operator decision is
its own prerequisite: a step whose operator line is not taken is skipped, and
nothing downstream assumes it.

| step | who | tier (CLAUDE.md) | does | what | measurement that shows it worked |
|---|---|---|---|---|---|
| 4.1 | one lane, `agent-model-line` | lite (code: a hook script with a self-test; docs: a rule sessions follow): self-review, one Codex code pass asked whether any line lets a review run on an unnamed model, whether the hook can be made to refuse a named call or allow an unnamed general-purpose one, and whether a coordinator may skip a register of the handoff file | ENFORCES | `scripts/hooks/agent-model-gate.py` (the measured script, with its three-payload self-test under `check.sh`'s Python leg) and the exact `hooks.PreToolUse` line for the operator (1.3); core skill "Your review": every review subagent names its model, the receipt's `panel` names it; fleet skill: the same in "Pick who does a piece", and the batch-boundary bullet (3.3); `fleet-conventions.md`: both as operator lines with their Why. D-117 appended recording exactly what this lane lands (the rule, the hook, the bullet); the operator's lines are recorded as amendments when and if taken | 4.3's counts: review-shaped general-purpose calls with no model param 11 to 0; receipts on main naming `panel@<model>` |
| 4.2 | the operator: wire the hook (two lines), then the settings line | none (operator's file) | ENFORCES before execution (the hook); SAVES about $2 over 4 days after it (ESTIMATE; the settings floor) | copy the script to `~/bin`, add the `PreToolUse` line (1.3); then `"CLAUDE_CODE_SUBAGENT_MODEL": "claude-opus-5-5"` in the `env` block, then the one-minute user-file check (1.3) | 4.3's count: subagent transcripts on Fable from a call with no model param, 5 to 0; a refused unnamed call in a real session, once |
| 4.3 | one lane, `subagent-report` | lite (code: a report script) | MEASURES | `scripts/subagent-report.sh [--since D]`, counts and bytes only, with its `check-subagent-report.sh` gate in `check.sh`; the lane's scratchpad script is the draft. Fields: per Agent call, its model param, subagent type, the classifier's word (review, other, unclassified: calls with no description are listed, not guessed), and the transcript model it produced (matched by the agent id the Agent result names); per session, subagent transcripts by model; landing-read observations (Read or Bash results over 10 KB whose path or command names a lane branch or worktree) per orchestrator; each orchestrator's prompt size at its first lane launch (the request carrying its first `herdr agent start` Bash call, from that request's usage); buddy-verb Bash result bytes. It does NOT verify receipts: it shows the subagent models a lane session used, and a reader compares that with the receipt the lane's commit carries | it reproduces section 0's numbers on the same four days (90 Fable calls, 17 inherited general-purpose, 10.3%) |
| 4.4 | **OPEN**: one lane, `buddy-land`, only on 4.3's evidence after two weekly reports | lite (a skill file naming no verb or flag is docs; the fork measurement is owed) | SAVES about a dollar a batch (ESTIMATE); ENFORCES the side-work model by file | `skills/buddy-land/SKILL.md`, `context: fork`, `model: claude-sonnet-5-5`, candidate B with its boundary (2.2); first the `$ARGUMENTS` and auto-mode measurement (2.3) | 4.3's landing-read count per orchestrator, before and after |
| 4.5 | the operator, one CLAUDE.md line | none | moves the handoff to the cheap moment; SAVES about a dollar a batch (ESTIMATE) | the batch-boundary line (3.3), if taken, with its size | 4.3's first-lane-launch footprint per orchestrator: under the chosen size for one opened at a boundary. This observes footprints, not costs; the cost comparison stays NOT MEASURED until a report compares request-weighted spend of two batches |

Not in any step, refused above: `_FORCE=1`; a `model:` pin on `buddy` or
`buddy-fleet`; the fleet-digest fork; a general-purpose override file; a
lower handoff size; a "batch" verb. Lanes on a cheaper model stay with the
pick rule (BEYOND-CODE-PLAN section 5), which is the operator's cap to set.

## 5. Why no D-117 yet

The id is reserved to orc-10 for this item. This lane lands a plan, and the
precedent is BEYOND-CODE-PLAN, which recorded no decision at plan time (D-103
and D-104 were written by the lane that landed its step 1). Step 4.1's lane
writes D-117 recording what IT lands and nothing it does not: the rule that a
review subagent names its model, the hook that refuses an unnamed one, and
the batch-boundary bullet keyed to a size the operator may or may not set;
section 0 of this plan is its record. The operator's two lines (the settings
floor, the batch-boundary size) are theirs, each recorded as an amendment to
D-117 when taken, so that taking one and not the other leaves the record
consistent. `docs/decisions.md` is untouched by this lane.

## 6. Taken and refuted

Two Codex design passes ran over the draft through `scripts/codex-review.sh`
(gpt-6-astra at xhigh, the charter prepended; the question: which of these
changes would lower spend without degrading what a lane or reviewer does;
which would silently move a behaviour-teaching text or a review onto a weaker
model; what is the cheapest version that holds). Both said revise; the brief
allowed exactly two passes, so the second's findings are folded here and the
plan is reported with them folded, not re-verified by a third pass.

**First pass** (eleven findings; verdict: revise, on finding 1):

| # | finding | taken / refuted | where |
|---|---|---|---|
| 1 | blocking: the "name your review model" rule was only in the fleet skill, but a full-tier LANE spawns the panel and loads only the core skill; and the five inherited-Fable calls were not classified, so "moves no review" was unproved for the Opus line | TAKEN: the rule goes in the core skill's "Your review"; the five calls are classified (one forgotten-param Fable review, four side work) and the Opus line's row says it moves that one | 0, 1.1, 1.3 |
| 2 | the measurement covered `--settings` at startup outside a repository, not the user file, hot reload or project settings | TAKEN: labelled DOCUMENTED / NOT MEASURED each; project settings checked for an `env` block (none); the one-minute user-file check named for after the line is added | 0, 1.3 |
| 3 | the Sonnet landing skill could become a cross-check on Sonnet with no name in any receipt | TAKEN: B is "landing evidence" with a boundary: named evidence and differences only, no defect reading, no judging claims, no running checks, no land/hold | 2.2 |
| 4 | the 3.2 saving ignored that the in-place path also hands off (at 300k, after about 40 requests); the fresh path does not guarantee no mid-batch handoff | TAKEN, completed under the second pass's finding 4 | 3.2 |
| 5 | "depends on the conversation" was not decidable; "decided 2026-10-28" was embedded in a line that is OPEN | TAKEN: independence is the handoff file's three registers written and empty or handed, decisions in files, the successor's acknowledgment; the proposed line carries `<date>` and `<100k>` | 3.1, 3.3 |
| 6 | a fresh session leaves session-addressed state behind: the predecessor's mail, waits, wakeups, jobs; the `/clear` objection applies here too | TAKEN: a "what does NOT carry over" paragraph; the predecessor drains and relays mail, clears waits, accounts for jobs; D-034 named as the same gap handled by the recipe | 3.1 |
| 7 | the report's fields could not establish the promised outcomes; 4.4 observed footprints, not costs | TAKEN: 4.3 lists its fields; 4.5 says footprints, not costs; completed under the second pass's finding 7 | 4 |
| 8 | candidate A's cut contradicted its arithmetic; the fork cost omitted the reads; Haiku had no rate; the listing cost was per model | TAKEN: A's margin stated (+$0.05 to +$0.22) and cut on value; fork cost $0.10 with derivation; Haiku comparison dropped; listing $0.30 Opus / $0.66 Fable | 2.2 |
| 9 | "$163 for eight orchestrators" and "a third more" had no derivation and clashed with $28-42 each; "<$3", "$5", "$70" were not reconstructible | TAKEN: the cost-state figure is $230 for orc-2..8; "a third more" dropped; 1.2 derives every dollar from requests per call and spend per request; the two measures named apart in the header | header, 1.1-1.2, 3 |
| 10 | "the $89 is sanctioned review spend" overstated a description classifier; the $16 buddy carry was a proportional estimate presented as measured | TAKEN: "the part a description can classify is almost all review work; the dollar split was not measured"; the $16 labelled a proportional estimate with its assumptions | 0 |
| 11 | the landing table did not separate steps that save from steps that enforce or measure; order: the shared sentence, then the Opus line, then a bounded report; defer the fork; the batch line separate | TAKEN: a `does` column; the order 4.1 rule and hook, 4.2 operator lines, 4.3 report, 4.4 deferred fork, 4.5 the operator's batch line | 4 |

**Second pass** (nine findings; verdict: revise, on finding 1):

| # | finding | taken / refuted | where |
|---|---|---|---|
| 1 | blocking: a receipt naming Opus cannot tell a chosen model from an inherited one; a forgotten-param Fable review still silently becomes Opus under the default, and prose cannot stop the omission before execution; keep the override off unless review launches reject a missing model before running | TAKEN: a PreToolUse hook on the Agent tool refuses an unnamed general-purpose call before it runs, MEASURED the same hour (refused with the rule as its reason; the named call ran); it is step 4.1's script and the first of 4.2's operator lines; the settings line is now the floor beneath it, OPEN, with the trade of wiring no hook stated | 0, 1.3, 4.1-4.2 |
| 2 | 1.2's arithmetic: the Opus line saves $2.74 not $3.50, the Sonnet addition $3.86, FORCE saves $93 (5.1%) not $70 (2.5%); Opus 4.8 had no stated rate | TAKEN: recomputed ($2.70, $3.90, $93 / 5.1%, percentages of $1,805); the Opus 4.8 rate stated as the audit's assumption in the header | header, 1.1, 1.2 |
| 3 | the Sonnet-line saving counted reviews that step 4.1 pins; after it, the Opus line saves about $2.19 and Sonnet about $1.39 more | TAKEN: the table has two dollar columns, history and after 1.3 ($2.10 and $1.40), each derived | 1.1, 1.2 |
| 4 | 3.2 compared only the first 40 requests; the in-place path's restart runs its last 110 requests 60k lighter, so the net is about $0.85 Opus / $1.15 Fable, not $2.50-4.50 | TAKEN: all 150 requests modelled on both paths; the net is about a dollar a batch and the case is the moment of the handoff; 4.5's claim reduced to match | 3.2, 4.5 |
| 5 | candidate B runs per lane, so a four-lane batch pays four forks: a margin of $0.34-1.19, before file reads and output | TAKEN: the table row charges four forks and says what the margin excludes | 2.2 |
| 6 | "the mid-batch handoff becomes the exception" exceeded the evidence: a fresh 56k prefix crosses 300k after 244k of growth, inside the observed range | TAKEN: stated as a HYPOTHESIS with that arithmetic; no distribution of batch growth was taken | 3.2 |
| 7 | 4.3 claimed to let receipts be checked against transcripts but had no receipt input or association | TAKEN: the claim is withdrawn; the report shows subagent models per lane session and a reader compares with the receipt | 1.3, 4.3 |
| 8 | D-117 assumed both operator choices, while 4.5 is optional; adopting one has no consistent recording path | TAKEN: 4.1 records only what it lands; each operator line is an amendment when taken; section 5 and the table's preamble say so | 4, 5 |
| 9 | nit: the Fable description breakdown summed to 90 reviews and left no room for the trading-tool call | TAKEN: re-sorted by hand (44, 30, 5, 4, 4, 2 and 1 other: 89 reviews, 1 other; the regex's 88/2 missed one ruling) | 0 |
