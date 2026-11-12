# Model routing: a printed pick inside the operator's cap, never a launcher

Status: PLAN (2026-11-11); **step 2 built as D-175** (2026-11-12, lane
route-grammar, after the operator's "yes trial a second lane model"; its
measured bytes are in section 5's Context cost table). The rest of step 1,
and steps 3 and 4, are not built. Written by the `model-routing` lane
(Opus 5.5, high) under orc-20's brief. Sources: the operator's request, the
read-only reports in section 0, the prior plans, one Fable consultation
(section 7) and one Codex design pass (section 8). Base: main at c23b081.
Nothing here is a decision. What becomes one is the operator's, and it takes
its D-number when a step lands. Section 10 (2026-11-12, lane router-forks)
answers the operator's question about activity across projects: four
ledgers measured, and rulings on pooling them and on the protected list.
None of it is built.

The register is `CHEAPER-MODELS-PLAN.md`'s. Every part names the failure it
answers, the number it rests on and how to re-run that number, the
alternative it refuses, and what it cuts. A figure not taken says NOT
MEASURED. Anything the operator decides is **OPEN**.

## The operator's request

2026-11-11, about 16:20Z, in orc-20's pane: "consult with fable on how we can
add in a model routing layer to help decide what request or lane should go
where with the buddy system, open a new lane. also use codex".

Read against the settled design, "routing" means four choices:

- a lane's model and effort;
- a subagent's model;
- a review layer's model;
- Claude or Codex for an item.

For each one, this plan asks what decides it, from which measured history,
where the pick is written down, and how the pick is checked after the item
lands. buddy launches, wakes and ends nothing (D-027, D-039, D-056), so a
router can only PRINT a pick. The coordinator's own launch line then carries
it.

## 0. What was measured for this plan

A Sonnet subagent took these on 2026-11-11 at about 23:52 local (EST, 04:52Z
on 11-12), from this worktree. Everything is counts and ids only. M4 and M5
were taken again at 2026-11-12 05:10Z with the queries in the appendix, and
they matched.

**On re-running.** The ledger is live and a sweep deletes released claims, so
re-running a ledger row gives that day's numbers, not these. The transcript
rows (M7, M8) are windowed by modification time, so a re-run moves them too.
The plan's export (`model-report.sh --export`, 93 items and 175 receipts)
stayed in the lane's scratchpad and is not retained. M6 is the one figure
fixed by its revision: run the report at `c23b081`.

| # | figure | measured over | re-run |
|---|---|---|---|
| M1 | 97 item claims (4 open), 10 of them with no class token or a bad one. By recorded model: 96 `claude-opus-5-5`, 1 unrecorded. No other model holds an item | the main checkout's ledger copy: item claims created 2026-11-03 07:10Z .. 11-11 18:59Z | `sh scripts/model-report.sh` |
| M2 | Opus 5.5 by class: docs 25 items (22 outcomes, 0 fail, 0 reworked, 57 requests/item); code 59 (51, 0, 1, 73); test 1; research 2; unknown 9 | the same | the same |
| M3 | 0 fails among 81 recorded outcomes; 1 rework (a slug claimed again) among 97 items; 12 of the export's 93 closed items have no outcome | the same; the `--export` tsv | the same; `--export <tsv>` |
| M4 | 81 outcome notes, none in the taught `<reds> <batches>` shape: 3 empty, 20 start with a digit, 44 contain "red", 0 start `reds=` | item claims (`model-report.sh`'s rule) in the live ledger, `outcome_note` | appendix query A (counts only; no script prints a note, invariant 9) |
| M5 | effort is recorded per session (`session_context.effort`, `internal/store/store.go`), and set on 193 of 207 rows: 171 high, 20 xhigh, 1 max, 1 medium | every `session_context` row in the live ledger | appendix query B; no report prints it today |
| M6 | receipts: 175 commits, 333 layers, 12 with counts. Full tier: 100 layers (21 panel, 20 design, 19 code, 17 cross-check, 23 reverify), **0** with counts. Lite code: 80 layers, 10 with counts (45 raised, 38 fixed, 18 reproduced) | HEAD at c23b081, 2026-09-20 .. 11-11 | `sh scripts/model-report.sh` (second table) |
| M7 | 320 Agent calls on main threads, in 97 sessions: `opus` 198, `fable` 73, `sonnet` 49. Unnamed general-purpose calls: **0** | main transcripts modified 2026-11-04 23:52 .. 11-11 23:52 local | `sh scripts/subagent-report.sh` |
| M8 | the `fable` alias ran on two models: 46 calls on `claude-fable-5-2`, 27 on `claude-fable-5-1` | the same, joined to each subagent transcript's model | the same (grouped table, last column) |
| M9 | Codex held no item claim in M1's window. It ran as review passes only: 14 passes, 13 with a verdict, elapsed p50 262 s, 1,326,083 tokens in all | M1's ledger; `codex-review.sh`'s pass record (D-146), last 7 days | `sh scripts/cost-report.sh` (`BUDDY_COST_DAYS`) |
| M10 | cost per item: requests/item and tool calls/item, main thread only (`model-report.sh` header). No report prints tokens or dollars per item or per model | the scripts' headers | read `scripts/model-report.sh`, `scripts/cost-report.sh` |

What these say, before any design:

1. **There is nothing to route between yet.** The operator's standing
   decision puts every lane on Opus 5.5 at high effort, and the ledger agrees
   (M1). The pick rule (`skills/buddy-fleet/model-pick.md`; BEYOND-CODE-PLAN
   5.2(b)) compares two models' rates in one class, and every class holds one
   model. A router run today prints the default for every item.
2. **The failure signal cannot tell two models apart.** There are 0 fails in
   81 outcomes and 1 rework in 97 items (M3). Two things never reach the
   rework column, because neither makes a second claim of the slug:
   - an in-run RED keeps its claim ("A RED along the way keeps the claim",
     `model-pick.md`);
   - a red's `<item>-2` lane is HANDED the parked claim (fleet skill,
     Landing step 4).

   A plain `release --to` also clears the claim's producer
   (`internal/store/transfer.go`), so the report credits the whole item to
   the new holder's model. The red count exists only as free text, in no
   fixed form (M4). The taught lines disagree with each other too:
   - the lane's release says `--note "<reds> <batches>"`;
   - the fleet skill's parked release says `--note <sha>`;
   - its model summary says `--note <what>`.
3. **Effort is recorded, but no report reads it** (M5).
4. **Review yield on the full tier is unknown** (M6: 0 of 100 layers counted).
   D-163 has owed counts since 2026-11-11 14:12Z. It puts the panel-size
   decision to the operator at the later of two points: 2026-12-02, or 10
   recorded full panel layers.
5. **Subagent naming already holds** (M7, with D-117's hook wired). **An alias
   is not a pin** (M8): over one week, a receipt's `fable` named two models.
6. **Claude vs Codex for an item has no history at all** (M9). The Codex plan
   lists a model-directed fleet as "Not qualified" (CODEX-ORCHESTRATOR-PLAN,
   Current status).

## 1. The four choices, and who owns each today

| choice | owner today | rule today | measured basis | what a router can add |
|---|---|---|---|---|
| a lane's model and effort at launch | the coordinator, inside the operator's cap | the default lane model (D-111); the pick rule only within a run's cap, never on a model kept for reviews; a full-tier item stays on the default | M1-M5: one model | a printed pick per class and tier, with the cells it rests on |
| a subagent's model | the session that spawns it | a review names its model (D-117, enforced by a hook); side work runs on Sonnet or Opus (operator) | M7, M8 | nothing to pick: no outcome is recorded for a subagent's work |
| a review layer's model | fixed by the tier | Fable for the full cross-check and the security panel lenses (D-111); Codex through `codex-review.sh`, whose model and effort are pinned (D-104) | M6, M8 | nothing to pick; the yield is D-163's to measure |
| Claude or Codex for an item | the operator, per run (`AGENTS.md` roles; D-071) | none | M9: none | nothing (section 4, not pursued) |

So three of the four are not routing problems. Each is fixed by a decision
and already measured for compliance. The lane pick is the one real choice.
This table is the plan's framing, and no step ships it.

## 2. Constraints the design keeps

- **A printed pick, never a launch** (D-027, D-039, D-056). The output is
  text a coordinator reads and copies into its own launch line (`--model
  <model> --effort <effort>`) and into the brief. Nothing in the gate's path
  reads it. The first line of the output says the print confers no authority.
- **Chat is never control** (invariant 4). The router reads only the export,
  the receipts and the cap. It reads no message, and of a claim description
  only its grammar tokens, whose bytes never leave SQL (as `model-report.sh`
  already does).
- **The operator's cap binds the pick.** The router:
  - picks only among the models the cap names;
  - never picks a model kept for reviews (D-111);
  - gives the cap's default to a full-tier item, a protected path, and a
    class or tier the cap does not open;
  - prints no recommendation, not even a default, when the cap is absent,
    broken or expired. The coordinator then uses the operator's standing
    default from the operator's own instructions, as it does today.

  It never widens the cap. A lane never raises its own model, just as it
  never raises its own tier (D-104).
- **No new harness backends or adapter targets** (the operator's word).
- **Context cost is first-class** (D-010). Section 5 names the bytes.
- **No schema change.** Every field this plan proposes is a grammar token in
  text the ledger already stores, read in SQL the way `class=` is, or a
  column the ledger already has (effort).

## 3. How the operator's cap is declared and read

Today the cap is prose: the operator's standing decision (lanes on Opus 5.5 at
high effort) plus a run's cap in the brief. The pick rule says the floor and
"clearly worse" are "the operator's numbers, set with the cap", and nothing
records them. A router needs the cap in a form it can read, written only by
the operator.

**Where it lives.** One line in the operator's own fleet-decisions file, the
file every coordinator already reads before a fleet. It sits outside every
repo, and it is not a shipped skill: the skill names no operator default
(D-104).

**What the line says:**

```
route-cap default=<id> trial=<id> classes=<word>[,<word>] tiers=<chore|lite>[,...] floor=<n> events=<n> worse=<n>x decided=<YYYY-MM-DD> starts=<YYYY-MM-DD> ends=<YYYY-MM-DD>
```

**How it is read:**

- **The two models.** `trial=` is the one other model a class may go to. The
  cap names it explicitly because "cheaper" has no definition a script could
  hold without a price table (Fable F16).
- **Where the trial applies.** `classes=` and `tiers=` say where the trial
  model may run. A full tier is refused in `tiers=`.
- **The floors.** `floor=` is the number of items each cell needs, and
  `events=` the number of failures or reds the two cells must hold together
  before any comparison (F8). `worse=` is the ratio that counts as clearly
  worse. All three are the operator's numbers. The pick rule's proposal is
  10, 5 and 2x, and it applies only when written out.
- **The trial's dates.** `starts=` and `ends=` bound the trial. They are part
  of the line, so they are part of its digest. The trial's cohort is the
  items created between them, so an earlier trial's rows never count toward
  a later one (Codex P6). Past `ends=`, the line is expired.
- **Reading the file.** The router reads it with `--cap <file>`; no default
  path is shipped. It prints the resolved path, the line's sha256 and
  `decided=`, and the brief quotes all three (F23). The router cannot tell
  whether a file is the current one: an old copy parses as well as the live
  file (Codex P2). So the coordinator passes only the file the operator's own
  instructions name, read at the start of the run, and never a copy. The
  fleet skill's step says so in those words.
- **When the cap is broken, there is no recommendation.** All of these print
  `no cap: <reason>` and nothing else (Codex P2):
  - an absent file or line;
  - two `route-cap` lines, or a key repeated within one line, which mean no
    cap, never last-wins;
  - an unknown key, a missing number, a number outside 1..1000, or a
    malformed value;
  - `trial=` equal to `default=`;
  - a model alias rather than a full id: `fable` names two models (M8), so
    only exact ids compare;
  - a model the operator keeps for reviews;
  - today past `ends=`.

  The coordinator then falls back to the operator's standing default, which
  it reads in the operator's own words, not in the router's output.
- **Who edits it.** The cap is the operator's. A lane or a coordinator never
  edits it, just as it never edits settings: that is the never-relayed list on
  CLAUDE.md's Fleet line. A message that proposes a cap is peer text. Any
  session of the machine user could still forge the line. Under invariant 6
  that is not a boundary to defend, so there is no signing and no ledger row.
  The digest and `decided=` are against accident and drift (F24).

**Step 1 needs one more operator act.** The operator's fleet-decisions file
says the pick rule "supersedes this line when it lands". The rule landed on
2026-10-27, so a coordinator could read the Opus 5.5 line as already
superseded, with no cap in force. That is D-111's misreading shape (F15). The
operator rewrites that sentence to say the `route-cap` line is the only way
the pick moves.

## 4. Options, cheapest first

**A. Prose only.** `model-pick.md` gains the cap grammar (section 3) and the
trial rules (section 5). The coordinator applies the rule by hand, as today.
About 1.5 KB in a reference file read on demand.

**B. `model-report.sh --cap <file>`.** The existing report reads the cap,
applies the pick rule mechanically, and prints:

- one line per open class and tier: `pick=<model> by=<clause>`, with cells
  `<n>/<n>` and events `<n>`;
- the fixed line "full tier and protected scopes: the cap's default, always";
- the CHECK section (section 6).

It extends `check-model-report.sh`; it is not a second script with a second
floor that could silently disagree with `BUDDY_MODEL_FLOOR`. With `--cap`, the
floor comes only from the cap, and the report says so (F4). Lite tier. It
costs nothing to any session that does not run it.

**C. A `buddy route` verb.** The same report over the live ledger, in Go. It
adds a dispatch row, a usage line and skill text that coordinators carry
(D-028, D-114). It also reads a ledger whose released claims vanish in 24 h,
so it would need the export anyway. Not recommended.

**D. Enforce the cap at launch.** A hook that refuses a launch line outside
the cap. buddy judges no launch, because it launches nothing, and launch
lines are the harness's surface. Not pursued. An operator-wired hook in
D-117's shape stays open to the operator.

**Not pursued, in any version:**

- a launcher or a proxy;
- rewriting a call's model (`CLAUDE_CODE_SUBAGENT_MODEL_FORCE`, refused in
  D-117);
- new harness backends or adapter targets;
- a learner or a fitness score: BEYOND-CODE 5.3 puts a learner's floor at
  about 30 items per model per class, and ORCHESTRATION-PLAN's deferred scope
  refuses "opaque capacity scores";
- routing a review layer's model;
- **a Codex lane model in the cap.** A standing line would turn per-run
  operator authorization (`AGENTS.md`, D-071) into something a script reads
  as allowed. Requests are counted from Claude transcripts only, so a Codex
  cell would not compare like with like (F19);
- a schema column, including a transfer-history table for red attribution
  (section 5 attributes reds by keeping the model, not by adding a row).

## 5. Recommendation and staged rollout

**Recommend A, then B, both only if the operator opens a trial.** The routing
layer is the pick rule made checkable, not a new component. If the operator
keeps one lane model, the answer is to land nothing: about 2 KB describing a
no-op is a cost with no use (F2).

| step | does | tier | lands | measures before the next step |
|---|---|---|---|---|
| 1 | **OPEN, the operator:** keep one model (the plan stops here), or say a trial is wanted for named classes on chore or lite items; and rewrite the "supersedes" sentence (section 3) | none | the operator's word | nothing: every later number needs it |
| 2 | option A, plus the grammar the cells need, and a BASELINE on the default only. The tokens: the claim description's third token `tier=<chore\|lite\|full>`; every taught release note begins `reds=<n> ` (the lane's release, the parked release, the model summary: all three change together). `model-report.sh` reads effort from `session_context`, and `tier=` and `reds=` in SQL. It prints cells by class x tier x model, a no-outcome rate and `outcomes without reds=`. The export gains counts-only columns, appended at the end (an older file reads them as missing): `tier`, `reds`, `effort`, and `protected` (0 or 1, computed in SQL from the scopes at export, so a swept row keeps it) | lite (code: report grammar; docs: a rule sessions follow) | `model-pick.md`, the two skills' release lines, `scripts/model-report.sh` and its check | the default's eligible cell (same class and tier, protected excluded) until it holds `floor=` items. Historical rows carry no `tier=` and cannot be the baseline: commits are not joined to claims (D-141) (Codex P3) |
| 3 | **the operator** writes the `route-cap` line, with `starts=` no earlier than the baseline's floor. From then on, every eligible item goes to the trial model: an ASSIGNMENT the cap makes, printed as `by=trial`, never a pick the router learned | none | the operator's file | the trial cell until the stop rule |
| 4 | option B: `model-report.sh --cap` prints a pick past both floors, in the cohort `starts=`..`ends=` | lite (code: a report) | `model-report.sh --cap`, its check, one line in `model-pick.md` | section 6's check, every run |

**The trial's rules (step 2 writes them into `model-pick.md`):**

- **Every eligible item, no choice per item, and one predicate for both
  cells.** An item is eligible when it meets all three conditions:
  - its class and tier are in the cap;
  - no claimed scope overlaps a protected path;
  - its branch's diff touches none.

  Every eligible item inside `starts=`..`ends=` goes to the trial model, so
  the easy ones are not picked for it (F11). The default's comparison cell is
  the baseline: step 2's eligible items under the same predicate. It is never
  the whole class, which would include protected work the trial can never
  receive (Codex P5).
- **Reds stay on the model that made them.** A red on a trial item reopens as
  `<item>-2` on the SAME model, so the cell keeps the red. Escalating to the
  default releases the trial item with `--outcome fail` and opens a fresh slug
  on the default. Without a schema change, this rule is how attribution
  survives `--to` (F7).
- **Protected paths always run on the default.** An item touching any of
  these runs on the default, whatever its class or tier word:
  - all code: `cmd/`, `internal/`, `scripts/`, `.githooks/`, `.claude/`
    (this covers `scripts/gate-hook.py`, which a narrower list missed,
    Codex P1);
  - the rules: `CLAUDE.md`, `AGENTS.md`;
  - `skills/`, including `model-pick.md` itself (F6);
  - `docs/review-charter*`, `docs/decisions.md`, `docs/prose-budget.tsv`.

  In effect, a first trial can open only text that no session or reviewer
  loads unasked. "Touching" means overlap in either direction, under the
  repo's one fold (invariant 13) and its containment rule (invariant 14): a
  claimed parent directory such as `docs/` overlaps `docs/decisions.md`. A
  shared companion claim counts as a claim too. The report holds the list as
  a literal beside a comment that names CLAUDE.md's full-tier classes, and
  the cap cannot shrink it (F5). Ruled since (section 10.2): eligibility is
  also an allow-list of prose files, held at the pick, the export and the
  READY diff alike. Built at the pick (D-180); the export and the READY
  diff: not built.
- **The diff is checked as well as the claim.** A claim names what a lane
  reserved, not what it edited: a lane holding `docs/README.md` can still
  write an unclaimed fence file (Codex P1). At READY, the coordinator runs
  `git diff --name-only -z --no-renames <base>..<tip>` on a trial item's
  branch. If any path is protected, the item is ejected: it is released
  `--outcome fail`, re-opened under a fresh slug on the default, and counted.
  A scope that grows mid-item is re-judged the same way. `tier=` describes
  an item; it never makes one eligible.
- **The stop rule.** The trial ends at the earlier of two points: `floor=`
  items in the trial cell, or `ends=`. At the end, the coordinator puts the
  numbers to the operator, who decides, as D-163's period does (F21). A trial
  that ends with fewer than `events=` events is reported as INCONCLUSIVE. That
  is neither an approval nor an extension, and only the operator writes a new
  line.

**Where the pick is recorded:**

- in the brief: model, effort, tier, the review ceiling, and the cap's path,
  digest and `decided=`;
- in the claim description's tokens (`model=`, `class=`, and `tier=` from
  step 2).

The report re-derives which items ran on the trial model from the recorded
model (M1's column). The brief's "by which clause" is not a measure, because a
scratch file cannot be recounted (F18). Every report run prints the export's
path, row count, date range and sha256. The export is the retained input,
kept where the repo keeps it (`model-pick.md`'s "into the file your repo
keeps for it"), so a figure in a brief can be re-run against the same bytes
(Codex P6).

**Context cost:**

Every figure here is an ESTIMATE of text not yet written. Step 2's lane
measures each with `wc -c` on its diff's added lines and on a fixture run of
the report, and puts the result in its READY (Codex P7).

| who | bytes | measured at step 2 (`wc -c`, D-175) |
|---|---|---|
| every request | 0 auto-loaded: no CLAUDE.md, rule or skill-description text | +7: D-175 on CLAUDE.md's index line, no clause |
| skill bodies, per session that loads them | the release-line wording, about +60 bytes across the core and fleet skills (step 2); a loaded skill rides every later request of that session (D-114) | +77 net: core +2, fleet +75 |
| read on demand | `model-pick.md`, about +2 KB (step 2) and about +300 bytes (step 4) | +2,713 net (2,680 to 5,393) |
| a coordinator, once per run | the report's added output: a line per open class and tier plus the check, about 1 KB | +1,742 on the live ledger (item section 1,454 to 3,196; 6 cells, a floor line each); a fixture run's item section 3,438 (9 cells) and 3,059 (7) |
| a Codex pass | not estimated | +93 on every pass (GIVEN 112's stub); +524 more under `CODEX_CHARTER=process` |
| a trial lane's brief | the cap's path, sha256 and `decided=`, plus the `tier=` token: about 150 bytes, carried on every request of that lane | the token: 10 bytes (`tier=lite `); the cap's part not built |
| any other lane | 0 | 10 bytes, the token in its claim's start |

## 6. How a pick is checked afterwards

Per class x tier, each cell set against the cap's default in the same class
and tier:

- **fail rate:** outcome `fail` among outcomes;
- **no-outcome rate:** closed with no outcome, such as an orphaned or wedged
  lane (F9);
- **rework rate:** the slug claimed again;
- **red rate:** reds PER ITEM, meaning `reds=` summed over a cell's items
  that carry the token, divided by those items. A raw sum would let a
  10-item trial with 2 reds beat a 100-item default with 5, though its rate
  is four times worse (Codex P4). Beside it, the report prints the count of
  outcomes that carry no `reds=`. The coordinator writes both the grade and
  the pick, so an unwritten grade must be visible. D-141 to D-163 is the
  precedent: optional counts were written on 5 of 320 layers (F12).

"Clearly worse" compares only these rates. The router abstains, printing no
pick and the reason, whenever any of these holds:

- either cell is under `floor=`;
- the two cells together hold fewer than `events=` events of the compared
  metric;
- either cell has an outcome without `reds=`.

The pooled event floor is what makes a zero-event baseline license nothing:
two clean cells hold 0 events and abstain. At floor 10, a model whose true
fail rate is 20% shows 0 of 10 about 11% of the time (`0.8^10`, an
illustration, not a measurement; F8).

**Printed, never a rule input:**

- **requests and tool calls per landed item.** They are main-thread only, so
  delegating to subagents looks cheap, and a request costs a different amount
  on each model (F13).
- **review findings by tier, from the receipts.** The receipt joins by tier,
  not class (D-141), and `reproduced` is the count that is evidence.

**Always printed:**

- **the protected-scope count:** closed items on a non-default model whose
  claimed scopes overlap the protected list, read from the export's
  `protected` column. It is a target that must print 0, not a measurement,
  and any other number is a routing defect to report (F5). It sees claims
  only. The READY diff check (section 5) is what sees edits, and each
  ejection it makes is counted in the brief's READY.
- **the `unknown` count:** items outside every cell (10 of 97 today, M1).

## 7. Fable consultation

One read-only Fable subagent (operator-named; `model: fable`) ruled on the
first draft (commit d15b275). It was given the draft, the constraints and the
prior-art paths, and was asked for a ruling, failure modes and cuts. It ran
one counts-only query on a private ledger copy (M4, M5).

**Ruling: accept with revisions.** "A now, B gated, never C or D" stands,
because one model fills every cell. The revisions were conditions on it. It
raised 24 findings: 23 taken, 1 taken in part.

| # | finding | taken |
|---|---|---|
| F1 | accept the shape | yes |
| F2 | land nothing if the operator keeps one model | yes: sections 5, 9 |
| F3 | effort is already recorded (`session_context.effort`, 193 of 207 rows); cut `effort=` and `batches=`, and add `tier=`: class x model puts full-tier gate work in the default's cell only, biasing "no worse" toward the trial model | yes: M5, step 2 |
| F4 | fold B into `model-report.sh --cap`, with one floor source | yes: option B |
| F5 | the router sees neither tier nor paths; a `docs` item on CLAUDE.md invariants or the charter routes cheap; add a protected-scope check that must print 0 | yes: section 5 list, section 6 count |
| F6 | an item editing the rule or the report is routed by the rule it edits | yes: on the list |
| F7 | `release --to` clears the producer, so a cheap lane's red fixed by a default `-2` lane shows as a clean default pass | yes: verified in `transfer.go`; the same-model `-2` rule |
| F8 | a zero-event baseline makes any zero cell "no worse"; add an event floor (proposed 5) | yes: `events=` |
| F9 | `fail` counts only a declared abandonment; 12 of 93 closed items have no outcome | yes: no-outcome rate |
| F10 | "the red count is written" was false as grammar (0 of 81 notes); the taught note lines disagree | yes: section 0 point 2; step 2 changes all three |
| F11 | a coordinator choosing which items go to the trial picks the easy ones | yes: every item of an opened class |
| F12 | the graded party writes the grade | yes: `outcomes without reds=` printed |
| F13 | requests/item is not a cross-model cost | yes: printed, never an input |
| F14 | ten wording chores fill a docs cell in a day | yes, by F3's tier axis; `unknown` printed |
| F15 | the operator's file says the pick rule supersedes the Opus line "when it lands", and it has | yes: step 1's operator act |
| F16 | "cheaper" is undefined | yes: `trial=` |
| F17 | an auto-mode check by name puts a capability list in a script that goes stale | yes: cut; the operator's prose rule stands |
| F18 | the export, the brief's clause and the cap cannot be re-run | yes: path, rows, range and digest printed; the clause measure cut |
| F19 | a Codex cap line turns per-run authorization into a standing one | yes: cut to "not pursued" |
| F20 | cut list | yes, except one part: section 1's table stays in the plan as framing; no step ships it |
| F21 | add `tier=`, `classes=`/`tiers=`, the event floor, the protected check, red attribution, the no-outcome rate, a stop rule, the M1 count fix | yes, all |
| F22 | the cap's home is right | kept |
| F23 | `--cap` with no default lets a scratch file pass as the cap; duplicate lines must mean no cap | yes |
| F24 | forgery is possible and is not a boundary under invariant 6; add `decided=`; the first output line disclaims authority | yes |

Not taken whole: F20's cut of section 1's table. It is the plan's answer to
"what it routes", and the plan keeps it as the reason three of the four
choices need no router. Nothing ships it.

## 8. Codex design pass

One pass through `scripts/codex-review.sh` (gpt-6-astra at xhigh, header
verified; `CODEX_CHARTER=process`, `CODEX_DIFF=c23b081..1b560fa`; 448 s, 90,336
tokens) on the plan with Fable folded. It was asked whether any part lets a
router override an operator decision, weakens a tier, routes on a figure
nobody can re-run, or adds context cost every session pays.

**Verdict: revise.** The printed-report approach keeps "buddy launches
nothing", chat never control, and the subagent and review-model rules. It
raised 7 findings and a reproducibility audit. All 7 were fixed in one fold,
and the audit was answered.

| # | finding | fixed |
|---|---|---|
| P1 high | a `code/lite` item scoped to `scripts/gate-hook.py` (outside `scripts/hooks`) routes to the trial, and the protected count stays 0; a claim on `docs/README.md` can sit beside an unclaimed edit to `internal/fence` | the list covers all code (`cmd/`, `internal/`, `scripts/`, `.githooks/`, `.claude/`); overlap is two-way under the fold; a READY diff check on the branch ejects a trial item that touches a protected path; `tier=` never confers eligibility (section 5) |
| P2 high | a stale `/tmp/old-cap` parses as valid; the end date was outside the line; section 2 said "no pick" while section 3 said "the default" | `starts=`/`ends=` are in the line and its digest; expired means no cap; any broken cap means no recommendation at all, and the coordinator reads the standing default in the operator's own words; only the file the operator's instructions name is passed (sections 2, 3) |
| P3 medium | no historical row carries `tier=`, so the default's comparison cell would be empty when the trial reaches its floors; trial assignment contradicted "below the floor, default" | a baseline phase on the default (step 2) before the operator's `starts=` (step 3); a trial item is an ASSIGNMENT (`by=trial`), not a learned pick (section 5) |
| P4 medium | summed reds let a 10-item trial with 2 reds beat a 100-item default with 5 | reds per item; abstain on a missing grade, a cell under the floor, or pooled events under `events=` (section 6) |
| P5 medium | the default cell holds protected work the trial can never get | one eligibility predicate for both cells; the default's cell is the eligible baseline (section 5) |
| P6 medium | after a sweep nothing retains scopes; an earlier trial's rows could fill a later one's floor; a path, row count and range do not identify an export | the export gains `tier`, `reds`, `effort` and a `protected` column computed at export; the cohort is `starts=`..`ends=`; the export's sha256 is printed and the export is the retained input (sections 3, 5) |
| P7 low | "lanes: 0" was wrong (the brief quotes the cap); the byte figures had no method | the table names a trial lane's ~150 bytes and the skill-body carry; every figure is labelled an ESTIMATE, and step 2 measures it with `wc -c` (section 5) |

**The audit, answered.**

- M4 and M5 were not reproducible as supplied: the query was unstated and its
  copy had been deleted. Both were taken again with the appendix's queries
  and matched.
- M1-M3 and M7-M9 read live state. Section 0 now says that re-running them
  gives that day's numbers, and that the plan's export was not retained.
- The "23:52Z" in section 0 was local time, and is corrected.
- The dollars-per-item line is cut, since it rested on an unspecified price
  table.

**Left as the audit named it, specifications for step 2's lane:**

- duplicate keys within one line (now: no cap);
- numeric bounds (now: 1..1000);
- alias matching (now: refused);
- scope overlap with parent and shared claims (now: both counted).

Each is written into section 3 or 5. Its test is step 2's, at that lane's
tier.

## 9. Open

- **The operator's step 1:** a trial or not. If there is one, which classes
  and tiers it opens, its three numbers (proposed 10, 5, 2x), its end date,
  and the rewritten "supersedes" sentence.
- **Who names reds when the coordinator writes the outcome.** The plan makes
  a missing grade visible. It does not make the grade independent.
- **The `fable` alias spans two models** (M8). D-163's cross-check yield,
  when counted, mixes them, and nothing pins the alias.
- **Claude vs Codex per item** stays the operator's per-run authorization,
  with no measured basis (M9).
- **Not re-verified.** The ceiling was one Fable consultation and one Codex
  pass, each folded once. Codex P1-P7's folds have not been read by a
  second pass.
- **The READY diff check is the coordinator's act, and nothing enforces it.**
  It is cooperative, like the rest of the design (D-005). A coordinator that
  skips it leaves only the claim-based count, which cannot see an unclaimed
  edit.
- **Whether the protected list leaves any class worth a trial.** It excludes
  all code and every loaded-unasked text. What is left is plans, notes,
  `docs/` pages outside the charter and decisions, README and USAGE prose.
  MEASURED since (section 10): over the last 10 days, across four ledgers,
  16 items of docs, research or ops on the default model, about 1.6 a day.

## 10. Several ledgers under one cap (2026-11-12)

**The question.** The operator, 2026-11-12, about 20:04Z, in orc-21's pane,
on route-cap volume: "are you sure we wont have enough activity here betwen
our multiple projects?" Lane router-forks (Opus 5.5 at xhigh) measured and
ruled. The rulings were read by the same Fable consultation and Codex design
pass as the router's forks; `ROUTER-SKILL-PLAN.md` section 14 holds their
counts.

**What was measured.** Four ledgers on this machine: this repository's (A)
and three other projects' (B, C, D). The operator's standing word keeps
their names out of every committed text. orc-21 ran `model-report.sh` on
each (`BUDDY_LEDGER`). A subagent then copied each ledger and read the copy
immutable, as `model-report.sh` reads, with that script's item rule, class
and tier rule, protected list and companion rule. It printed counts only.
The run was at about 20:10Z. The allow-list (10.2) was re-measured at about
20:30Z, when D held one item more.

**On re-running** (Codex design pass 2). The SQL and its outputs stay in
the lane's scratchpad, as the router's probe did, and the lane's READY names
the path. Their sha256 prefixes:

| file | sha256 |
|---|---|
| `ledger-sweep.sql` | `c19d0ba810f2` |
| its output | `93d86bdbf65b` |
| `ledger-sweep2.sql` (the re-measure) | `2e24b371452d` |
| its output | `46f31fb65fa4` |

The ledger copies were not retained, as `model-report.sh` says of its own
copies. A re-run reads that day's ledgers, so it drifts, and a sweep
removes rows for good (section 0). No route rests on these figures: a
trial's numbers come only from retained exports (10.1).

| ledger | items | created | items a day, last 7 full days | protected | unprotected | eligible, allow-list | eligible docs, research or ops on `claude-opus-5-5` |
|---|---|---|---|---|---|---|---|
| A, this one | 115 | 11-03..11-12; complete from about 11-05 (swept) | 13.1 | 109 | 6 | 6 | 5 |
| B | 340 | 10-23..11-12 (no sweep seen) | 18.9 | 261 | 79 | 27 | 9 |
| C | 270 | 11-10..11-12 (swept; its sessions go back to 08-14) | 8.6, truncated | 270 | 0 | 0 | 0 |
| D | 27 | 10-29..11-12 (no sweep) | 1.6 | 2 | 25 | 2 | 2 |
| all four | 752 | | about 42 | 642 | 110 | 35 | 16 |

What the counts say:

- **C's 270 of 270 is real, not an artefact.** The literal list matches C's
  layout. Its code entries protect at least 215 of the 248 items protected
  by their own scopes. `docs/decisions.md` also matches 62 of them,
  overlapping those, and protects 22 more through a shared companion. No instructions file and no single shared
  claim explains it: `CLAUDE.md` and `AGENTS.md` hit none, and the 22
  companion-only items spread over 45 companions, at most 3 items each. It
  is also one burst, not a rate: 210 of the 270 were created on 11-12, and
  102 are still open.
- **The list fails the other way in B and D.** It under-protects. 38 of B's
  79 unprotected items and 19 of D's 25 are declared `code`, in paths the
  list never names. Only the coordinator's class word keeps them out of a
  prose trial, and the READY diff check would pass a mis-declared one.
- **Two of the four were swept** (A and C). A live ledger cannot be re-run
  (Codex P6).
- **`tier=` appears on 11-12 only** (D-175): A 15 of 22 items that day, B 5
  of 63, C 111 of 210, D 1 (on 10-29).
- **No fails, and few reds written.** 0 fails in 531 outcomes across the
  four (A 101, B 276, C 131, D 23). `reds=` is on 10 of A's outcomes, 8 of
  B's and none of D's. C writes it on 88 of 131, and its docs items carry
  0.47 reds each (n=32), all of them protected work.
- **The eligible items are bursty.** A's 5 all fell on 11-12. B's 12 fell
  on 11-03 (3), 11-04 (5), 11-10 (3) and 11-12 (1), and 3 of them ran on
  other ids. D's 2 fell on 11-12.

### 10.1 Pool the ledgers, from retained exports

- **Why pool.** The cap line is global: one line, read by every
  coordinator. The trial assigns every eligible item wherever it runs, so a
  per-ledger report shows four parts of one experiment. And alone, A cannot
  fill a cell inside a trial's 14-day window (10.3).
- **Why exports, never live ledgers.** A sweep has already truncated two of
  the four. The export is the retained input (Codex P6). Each repository's
  coordinator owes its export before every sweep (`model-pick.md`), or that
  history is gone. Whether the other three keep exports: NOT MEASURED.
- **One reader.** Phase 3's `router.py report` takes several exports, read
  only: the one parser (`ROUTER-SKILL-PLAN.md` section 6). `model-report.sh`
  stays the exporter, one ledger per run, and never reads a cap.
- **Labels, never paths** (Fable F2). The caller gives each input a label,
  one capital letter. The operator keeps the map from label to file in
  their own file beside the cap, outside every repository. The report
  prints each input's label, rows, created range and sha256. It prints no
  path for any input, wherever it runs, and its refusals name the label
  (Codex design pass 1: a rule keyed to the cwd's repository would print
  B's path for a run made from inside B). So a figure quoted in a brief or
  a receipt names no repository, and it still re-runs against the same
  bytes. `model-pick.md`'s "quote that line too" becomes "quote the label
  and the sha256" in the commit that lands the labelled reader.
- **A fixed input set** (F3). The baseline's freeze records the labels. A
  run over a different set abstains: an omitted export would otherwise read
  as a smaller experiment. Claim ids dedup within one file only, so a claim
  id found in two inputs refuses, since the same export passed twice would
  count twice.
- **Within each input first, then pooled** (F7). Each input's trial cell is
  compared with its own baseline, then the results are combined. Pooling
  unstratified cells compares repository mixes, not models. The share guard
  the draft proposed is cut: abstaining when an input's share of the two
  cells differs by more than half. At cells of 10 it abstains about 40% of
  the time on sampling noise alone.
- **No choice per item, checked** (F8). Items of an opened class that ran on
  a third model fall out of both cells, and nothing else sees them. The
  report prints their count for each cell's window and abstains above 0.
  Measured: B ran 8 of its 82 docs, research and ops items on other ids.

### 10.2 No per-repository list: one predicate, the same everywhere

- **The literal list stays as it is** (section 5). Nothing shrinks it (F5).
- **An allow-list is added.** An item is eligible only when it has at least
  one scope, and every scope it holds, and every scope of a shared companion
  (section 5's rule), passes all of these:
  - it ends in `.md`, `.markdown`, `.rst`, `.adoc` or `.org`. `.txt` is out,
    since `requirements.txt` and `CMakeLists.txt` are build inputs;
  - no component begins with `.`;
  - its last component, folded, does not begin `claude` or `agents`. That
    catches `CLAUDE.local.md` and `AGENTS.override.md`;
  - section 5's list does not protect it.

  A directory scope is never eligible, since it may hold code.
- **It only removes eligibility.** Measured: the 110 unprotected items fall
  to 35. A and C are unchanged; B falls from 79 to 27, and D from 25 to 2.
- **Why not a list per repository.** C shows the literal list working. B and
  D show it failing, and a list per repository fixes that only where each
  list is written right. A list declared inside a repository is edited by
  that repository's lanes, so the rule would be edited under itself (F6). The
  allow-list needs no layout: an extension it does not list is ineligible.
- **One predicate at three points** (Fable F1): the pick (`router.py pick
  --touch`), the export's flag, and the READY diff. Until D-180 the pick
  did not hold it: `router.py`'s shipped names (`ROUTER-SKILL-PLAN.md`
  section 6) protected neither the code entries nor `docs/decisions.md`, so
  a buddy line with `assign=writing:<id>` and `tiers=lite` would have routed
  `--touch docs/decisions.md` off the default. No `route-cap` line was in
  the operator's file, so nothing routed. The pick holds it now (10.4 step
  1), and is stricter than the predicate above (what step 2, now dropped, would have owed; 10.4).
- **Both cells alike** (F4). The READY diff check runs on every item of an
  opened class, baseline and trial alike; otherwise the baseline is judged
  by claims and the trial by diffs. An ejection leaves both cells and is
  counted apart. It is never `--outcome fail`: taught that way today, each
  ejection is a fail charged to the trial, against a baseline with 0 fails
  in 531, and the trial would read worse because of policy. A claim flag
  cannot see an ejection: an item claiming only `docs/notes.md` that edited
  a code path still exports as eligible (Codex design pass 4). So the
  ejection is recorded as a counts-only token of its release note, read in
  SQL the way `reds=` is. The export carries it, and both cells drop the
  item.
- **Additions** (F5) go through the cap's `protect=`. They are global and
  additive, they sit outside every repository, and they over-protect the
  other repositories, which is the safe side. The export never parses the
  cap (one parser). The route in (Codex design pass 3): `router.py`, the one
  parser, prints the line's additions in canonical form with their digest.
  The coordinator passes that list to the export as its arguments. The
  export applies it to the scopes it still holds, and records the digest on
  every row. The comparison drops, from both cells, a row whose digest is
  missing or differs from the cap's. So changing `protect=` restarts the
  baseline.
- **Residual.** A prose file that another repository loads unasked, under a
  name no list holds, is admitted. Its fix is a `protect=` prefix in the
  operator's own file.

### 10.3 Days to the floor, honestly

The floor is per cell: one class at one tier (section 6). Each estimate
counts from the day 10.4's steps 2 and 3 land. It assumes `tier=` on every
brief and `reds=` on every outcome from then on, and the last 10 days'
rate. That rate counts claims only; eligibility judged on
diffs is NOT MEASURED.

- **Research at lite, pooled: about 8 days per cell.** That is about 1.3
  items a day, if every research item is lite. The baseline cell fills
  before `starts=`, and the trial cell fills after it, so both take about
  16 days. Fable, reading the same counts, put both floors at 12-18 days.
- **Alone:** A, about 20 days per research cell, longer than a trial's
  14-day window (`ROUTER-SKILL-PLAN.md`, D5), so A alone can never fill a
  trial cell. B, about 12 days, inside the window only at its best rate. D,
  about 50 days for a docs cell. C, never.
- **Docs:** about 0.3 a day pooled, so about 33 days per cell. No docs cell
  fills inside the window, even pooled. **Ops:** none in any ledger.
- **The event floor decides, not the item floor.** `events=` counts fails
  and reds over both cells (section 3; `model-pick.md`).
  - Fails: 0 in 531 puts the rate under about 0.6% (the rule of three), so
    five expected fails need about 890 items. Never.
  - Reds: at C's rates (0.30-0.47 an item, the higher on docs), two cells of
    10 hold 6-9, and the floor is met. Written as A and B write them today, there are none.

  So unless every outcome writes `reds=` and prose work reds, a first trial
  ends INCONCLUSIVE, however long it waits.

**The answer to the operator.** Yes, pooled there is enough activity for
the item floor in one cell, research at lite, in about two to three weeks
for both cells. No other cell fills inside a trial's window, and no single
ledger fills one alone except B at its best. Across the four, about 42
items a day were claimed over the last seven full days, and about 1.6 a day
over the last 10 were eligible docs, research or ops on the default model.
Of the 752 items the ledgers still hold, 16 (2%) are. What decides a trial
is not volume but events, and none of the 531 outcomes is a fail.

### 10.4 What a build lane does

Step 1 built (D-180). Steps 2-4 (router-pool) are DROPPED, not deferred:
the operator, in orc-22's pane at about 22:26Z on 2026-11-12, "yes land
router-gate, drop router-pool, run the audit". They stay below as planned;
nothing builds toward them. As planned, in this order, at the full tier,
since each changes what the router may route, as phases 2 and 3 do:

1. `router.py pick` applies section 5's list and the allow-list on any line
   with `tiers=` (10.2, F1). This lands before any buddy `route-cap` line is
   written. **Built (D-180).** On such a line each `--touch` is judged,
   and one failing touch gives the default:
   - no touch at all: `why=no-touch`, for every class;
   - not one path of 1 to 512 bytes of `A-Za-z0-9._/-` (a list, a glob,
     a space, a backslash, a `~`, a non-ASCII name): `why=bad-touch`,
     judged before any file is looked at; so is a path the system cannot
     resolve (a symlink loop, a refused search, a component that is a
     file), and a file with a second hard link;
   - phase 2's shipped names and `protect=`, a directory, the cap file
     itself, then the literal list as `model-report.sh` holds it,
     overlapping in either direction: `why=protected`;
   - the allow-list's clauses: `why=not-prose`, with `..` anywhere in the
     touch, an absolute one included (Codex design pass).

   Every reading of a touch must pass: as given, and its lexical and
   resolved paths relative to every enclosing root (or the whole path when
   none encloses it), each from every component on, so a root not on disk
   yet cannot hide a row. A linked worktree stops the climb only at
   `.claude/worktrees/<name>` or with no repository above it.
   `check-model-report.sh` proves the two copies of the list equal, row
   for row and probe by probe through the real report. Residual: a link
   INTO prose from a protected name (`AGENTS.md -> docs/rules.md`) is not
   followed backwards (D-180). A line without `tiers=` keeps phase 2's rule,
   so a caller who types `--tier none` for a tiered item gets that rule
   (R5's residual: a false tier statement).
2. The export gains appended columns: the allow-list flag, the predicate's
   digest, and the ejection token (10.2). `router.py` prints a line's
   additions in canonical form for it. Dropped (above). It would have owed
   (Codex design pass, D-180): the pick is stricter than 10.2's predicate,
   so the flag would apply the pick's whole predicate to each stored scope,
   not only the literal list and the allow-list: the shipped names (a
   sensitive substring makes `docs/tokens.md` default), `protect=`, the
   charset, the cap file. Otherwise an item the pick defaulted exports as
   eligible and sits on the default inside the trial's window. What the
   pick read from the file system (a directory, a link's target) cannot be
   rebuilt from a stored scope string, so the step would retain that
   evidence or export the item as unknown, dropped from both cells, its
   digest naming these semantics.
3. `model-pick.md` runs the READY diff check on every opened-class item,
   and records an ejection as that token, never as `fail`. Dropped (above).
4. Phase 3's `router.py report` reads several labelled exports. It
   stratifies, prints and abstains as 10.1 says. The label-quoting line in
   `model-pick.md` lands in the same commit, not before the reader exists.
   Dropped (above).

A usable baseline starts only once 2 and 3 have landed (Codex design pass
4). Rows exported before then lack the flag and the ejection token, so they
belong to no cell. With 2 and 3 dropped, none starts.

### 10.5 Left open at the round ceiling

- Whether `events=` must be met in the trial cell itself (Fable F6). A
  baseline larger than the trial cell can meet the pooled floor alone, and
  the stop rule caps the trial cell at `floor=` items. Phase 3's design pass
  decides.
- Eligibility judged on diffs, in any ledger: NOT MEASURED.
- Whether the other three repositories' coordinators keep exports: NOT
  MEASURED.
- Where the measurements' scripts are kept (Codex design pass 2, answered
  in part: the digests are above). A lane scratchpad does not outlive the
  machine's temporary-file cleanup. Keeping counts-only copies under
  `docs/findings-probes/` is the router probe's open question (F22), and the
  same one here. A copy would hold no other repository's name or path: the
  SQL names none, and the ledgers' paths sit only in its uncommitted driver.

## Appendix: the counts-only queries (M4, M5)

Run on the live ledger, read-only and immutable, from any checkout:

```sh
L="$(git rev-parse --path-format=absolute --git-common-dir)/buddy.db"
Q="file:$L?mode=ro&immutable=1"
# A (M4): item claims by model-report.sh's rule; outcome-note shapes, counts only
sqlite3 "$Q" "WITH i AS (SELECT * FROM claims c WHERE slug NOT GLOB 'orc-*'
  AND slug<>'orchestrator' AND NOT EXISTS (SELECT 1 FROM claim_scopes s
  WHERE s.claim_id=c.claim_id AND lower(s.scope) GLOB '.buddy/*'))
  SELECT count(*), sum(outcome<>''), sum(outcome<>'' AND outcome_note=''),
  sum(outcome<>'' AND outcome_note GLOB '[0-9]*'),
  sum(outcome<>'' AND lower(outcome_note) GLOB '*red*'),
  sum(outcome<>'' AND outcome_note GLOB 'reds=*') FROM i;"
# B (M5): effort per session_context row
sqlite3 "$Q" "SELECT effort, count(*) FROM session_context GROUP BY effort ORDER BY 2 DESC;"
```

At 2026-11-12 05:10Z, A printed `97|81|3|20|44|0` and B printed high 171,
xhigh 20, empty 14, medium 1, max 1. `immutable=1` takes no lock and
assumes that nothing changes underneath it, so a write still in the WAL can
be missed. The model report copies the ledger first and reads the copy (its
header). The two reads agree here: Fable's copy gave A's same five numbers.
