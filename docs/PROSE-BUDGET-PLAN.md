# Prose budget: bounding what every request pays for

Status: PLAN, nothing built (2026-10-28). Written by the `prose-budget` lane on
Fable 5.1, from the operator's question below, the trajectory consult of the
same day (sections 1, 5 and 6), orc-10's cost audit, and counts taken on this
machine the same afternoon. One Codex design pass ran over the first draft
(verdict: REVISE, seven confirmed gaps, two hypotheses); every finding is in
"Taken and refuted" at the end and folded into the sections it names. A
second pass ran over the revision (verdict: REVISE, five confirmed, three
hypotheses); it is folded here and recorded in the same table. Two passes
are the brief's bound; what the second left open is marked as such.
Baseline: main at 8449a87. The `claude-md` (D-112) and `skill-split` (D-114,
D-115) lanes were running when this was written and had not landed: every
size here is today's file, and section 6 says which ceilings are set only
after they land.

Register: every part names the failure it answers, the number it rests on,
the alternative it refuses, and what it cuts. Where a number was not taken,
it says NOT MEASURED. Anything the operator has to decide is marked **OPEN**.

## The operator's question

2026-10-28, in orc-10's pane: "does the above recommendations acceptance take
care of the main risk named with prose growth as the cost driver? If not,
open a fable lane to plan out the fix there in tandem with codex."

orc-10's answer was no: the accepted items (split `CLAUDE.md`, split the
skill, cap review rounds) are one-time cuts. Nothing bounds any file's size
afterwards, every decision still costs four files, and the 100 KB charter is
still prepended to every Codex pass. This plan is the mechanism that holds
after the cuts.

## 0. Summary

The cost is not the size of any file. It is size times the number of requests
that carry it. `CLAUDE.md` is carried by every request of every session; the
skill body by every request after the one tool call that loads it; the
charter by every Codex pass. Measured this week in this repo: 5,254 requests
at a mean prompt of 210,695 tokens and 1.09 billion cache-read tokens, of
which `CLAUDE.md` is about 8% and the skill body about 3%. The splits running
today take those shares down once.

What the first Codex pass made plain, and the arithmetic bears out: **at 8 to
12 decisions a week, any text that must grow by one line per decision
outruns any sane ceiling within weeks.** A one-line index at 200 bytes a
decision is 1.6 to 2.4 KB a week; a 400-byte GIVEN a decision is 3.2 to
4.8 KB a week (20.8 to 31.2 KB and 41.6 to 62.4 KB a quarter). A ceiling over such a text with 10% headroom fires in its first or
second week, and a check that fires every week is the one that gets raised
by reflex, which is the commit-gate trap in a new coat. So the mechanism has
two halves, and the second is not optional:

1. **Ceilings**: a committed table of byte ceilings for every text the
   harness loads unasked, per file AND per load class in aggregate, enforced
   red in the hermetic tier, raised only by the operator's word on the
   ceiling's own line, lowered freely, with a clause that fails when a new
   always-loaded file appears or a budgeted one leaves the table.
2. **Consolidation, in BOTH the index and the charter**: the always-loaded
   copies of a decision are TOPICAL, not per-decision. A new decision amends
   the ruling it belongs to and adds its D-number to that line (6 to 8
   bytes; 0.6 to 1.2 KB a copy a quarter at today's rate) and sometimes a
   clause; it does not add a line. What it adds must fit the headroom or be
   offset by a removal in the same commit: growth is small and positive,
   not zero. 56 of the charter's 112 GIVENs already name two or more
   decisions, so this is half the charter's practice already. The
   per-decision index lives in the record, which no request carries.

The top recommendation, in one line: **ceilings on every auto-loaded text
with mandatory aggregates, and topical rather than per-decision copies in
both the index and the charter, so the ceilings can hold without being
raised.** Everything else
here (the template, retirement, the report section) makes living under the
ceilings cheap.

## 1. Which text rides which context

The multiplier is the thing to target. From the audit, and from how Claude
Code and `codex-review.sh` load text:

| Text | Bytes today | ~Tokens (bytes/4, an estimate) | Loaded | Multiplier this week (this repo) | Share of cache reads (estimate) |
|---|---|---|---|---|---|
| `CLAUDE.md` | 69,690 | 17,400 | every request of every session | 5,254 requests | ~91.5M tokens, ~8.4% |
| `~/.claude/CLAUDE.md` (the operator's; outside the repo and this plan) | 3,657 | 900 | every request, every repo | 5,254 | ~0.4% |
| skill `description` (frontmatter) | ~420 | 105 | every request, in the skills listing | 5,254 | negligible; unbounded today |
| `skills/buddy/SKILL.md` body | 73,144 | 18,300 | once per session (Skill call or auto-trigger), then every later request of that session | body present in 21 of 70 sessions; 1,765 of 5,452 unique requests carried it (32%) | ~32M tokens, ~3.0% |
| hello digest (hook output) | ≤ 9,000 by code (`helloBudget`, D-034) | ≤ 2,250 | once per session, rides after | 70 injections; requests after NOT MEASURED | ~1.2% is the audit's figure, not reconstructed here; already budgeted in code |
| "Read this first" (CLAUDE.md names `README.md`, `docs/README.md`, `docs/DESIGN.md`, `docs/USAGE.md`, `docs/review-charter.md`) | 22,036 + ~3,000 + 11,650 + 168,634 + 99,972 ≈ 305,000 | ~76,000 | once per session that obeys, rides after | NOT MEASURED how many obey; one that does pays four times `CLAUDE.md` | 0 to large; unbounded, unenumerated today |
| `docs/review-charter.md` | 99,972 | 25,000 | prepended to every Codex pass | 47 passes in the receipts of the last 101 commits (14 days): 3.4/day, ~336 KB/day | not in the Anthropic bill; Codex-side caching NOT MEASURED; the script's header names exploration as the dominant cause of watchdog timeouts, which the charter exists to buy back |
| `AGENTS.md` | 4,586 | 1,150 | every Codex run (project instructions) | 3.4/day | small; unbounded |
| `docs/decisions.md` | 602,399 | 150,000 | never auto-loaded | 453 tool calls named it this week: 1 read it whole, 452 read a range or searched it | ~0; the whole-file hazard is NOT realized |
| `docs/USAGE.md`, the plans, `docs/wishlist.md` | 168,634 / 249,000 / 50,399 | — | never auto-loaded (USAGE is read-first, above) | — | 0 by default |

The week's billed context, this repo's 70 transcripts (2026-10-21..28,
unique request ids across files, counts only): cache reads 1,091,524,052;
cache writes 15,409,032; uncached input 63,354; output 4,537,077. Mean prompt
210,695; p10 91,586; p50 188,411; p75 263,769; p90 319,750. Median 68
requests per session, mean 77 over the 68 sessions with usage. Two id rules
gave 5,254 and 5,452 unique requests (the harness's `requestId` with a
fallback, and the message id); the second pass caught the two populations
mixed in the skill share, so both counts are given and step 4's fixture
settles the rule before the report prints one.

What the table says. The audit's fixed prefix (median 56k tokens a request)
is the system prompt, tool and MCP schemas, the operator's file, and this
repo's `CLAUDE.md`; the repo controls about 17k of the 56k today, plus the
skill body on a third of requests, plus whatever read-first reads a session
obeys. After the two splits the repo's every-request share is roughly 6k
tokens (an estimate from the lanes' targets). This plan promises an ABSOLUTE
byte bound on repository-controlled prose; it reports the percentage, and
cannot promise one, because the denominator (conversation length, p50 188k)
is the `handoff-word` lane's and the operator's 300k decision.

Refused: measuring size alone. `docs/decisions.md` is nine times `CLAUDE.md`
and cost one whole read this week; the skill is the same size as `CLAUDE.md`
and costs a third as much. A plan that ranks files by bytes cuts the wrong
one.

## 2. The budget mechanism

**Failure it answers.** No file in this repo has a size bound. `CLAUDE.md`
went 16,252 → 24,572 → 39,732 → 52,237 → 59,275 → 69,690 bytes (09-08, 09-20,
09-29, 10-14, 10-20, 10-28); the skill 0 → 26,035 → 33,825 → 39,696 → 73,144;
the charter 10,497 → 24,859 → 53,885 → 76,323 → 85,789 → 99,972. Each step was
a reasonable commit. The hermetic tier gates the skill's NAMES (every verb
taught exists) and the charter's COVERAGE (every decision has a GIVEN,
`check-charter.sh`), and both gates push text IN; nothing pushes any out.

**The mechanism.** Four parts, the repo's way.

1. **A committed table, `docs/prose-budget.tsv`.** One line per budgeted
   text and one per load class: `key`, `ceiling_bytes`, `set` (date and who:
   the operator, or the lane that lowered it), `why`. A key is a path, a
   section (`CLAUDE.md#Decisions`: from that `## ` heading to the next), a
   frontmatter field (`skills/*/SKILL.md:description`), or a class total.
   The classes, each with a MANDATORY aggregate line (the first Codex pass's
   P1: per-file ceilings alone let a lane add a file under a new ceiling,
   and 1 KB of new rules file per decision is 100 to 150 KB a quarter):

   - `every-request` = `CLAUDE.md` + `CLAUDE.local.md` + `.claude/CLAUDE.md`
     + `.claude/rules/*.md` + every `@` include + every skill description.
   - `per-session` = every `skills/*/SKILL.md` body (core and fleet; a
     coordinator loads both).
   - `read-first` = every file CLAUDE.md's "Read this first" section names.
   - `codex-pass` = `AGENTS.md` + `docs/review-charter.md`.

   A new file in a class consumes that class's headroom; it does not get
   room of its own. Proposed first lines, the numbers **OPEN** until the
   splits land (section 6 gives the formula; the examples below use it on
   guessed landed sizes and are illustrations, not proposals):

   ```
   every-request                 32768  operator  class total; CLAUDE.md + rules + includes + descriptions
   CLAUDE.md                     28672  operator  rules + topical index; set from claude-md's landed size
   CLAUDE.md#Decisions            8192  operator  topical lines naming D-numbers; not one per decision (section 3)
   skills/*/SKILL.md:description  1024  operator  every session's skills listing
   per-session                   81920  operator  class total; core + fleet bodies
   skills/buddy/SKILL.md         32768  operator  core: what a lane needs
   skills/buddy-fleet/SKILL.md   49152  operator  coordinators only
   read-first                   143360  operator  class total; what CLAUDE.md tells every session to read
   codex-pass                   106496  operator  class total; lowered with step 2
   docs/review-charter.md       102400  operator  today's size; lowered when GIVENs consolidate (step 2)
   AGENTS.md                      8192  operator  every Codex run reads it
   ```

2. **`scripts/check-prose-budget.sh`, invoked from `check.sh`** beside
   `check-charter.sh`. For every table line: measure, compare, and on a miss
   FAIL the tier (exit 1, never a warning), printing the key, the bytes, the
   ceiling, the overage, the file's five largest `## ` sections with their
   sizes, and one fixed sentence: *consolidate or retire before raising; to
   raise, edit this key's line in docs/prose-budget.tsv with the date and
   the reason, the operator's word, in the same commit.* On GREEN it prints
   every key's bytes over its ceiling (`CLAUDE.md 23,114/28,672 81%`), so
   the trend is read on every run, not only when it fires.

   Two clauses, as `check-fence.sh` and `check-charter.sh` do: a planted
   fixture (a temp directory with a table and files: one over its ceiling
   that MUST be flagged, one under that must not, one auto-loaded file with
   NO table line that MUST be flagged) proves the measuring, the comparison
   and the enumeration are armed before the real tree is judged.

3. **The enumeration clause, independent of the table.** The check carries
   its OWN fixed list of what the harnesses load unasked and what the repo
   tells a session to read: the `every-request` and `per-session` globs
   above, `AGENTS.md`, `docs/review-charter.md` (the path
   `codex-review.sh` names), and the paths CLAUDE.md's "Read this first"
   section lists. Every hit must have a table line AND belong to a class
   total; a table line whose file is gone is also a FAIL (a lane cannot
   remove coverage by deleting the row, the first pass's second P1). The
   list is in the check, not the table, so the table cannot vote a file off
   it. A loader this list does not know (a new Claude Code auto-load
   location, say) is the recorded residual; the audit's fixed-prefix median
   is the number that would show it.

   Startup reads are declared in BOTH authority files and parsed from
   both: CLAUDE.md's "Read this first" section and AGENTS.md's read list
   (today it names `CLAUDE.md`, `docs/DESIGN.md` and the charter, so cutting
   DESIGN from one list leaves the obligation in the other; the second
   pass's P1). One level of closure: a budgeted startup read's own
   "read first" list, if it has one, joins the class. And a grep-shaped
   clause, two clauses as the repo's others: a line in either authority
   file matching the startup-directive forms (`read .* first`, `before
   working`, `before anything`) OUTSIDE the parsed section FAILS with
   "startup reads belong under Read this first", so a directive moved under
   another heading is caught by its words. What the clause does NOT cover,
   on purpose: a file a session reads because its task needs it ("read
   `docs/USAGE.md` § wait before changing wait" is a task reference). A
   startup directive respelled past the grep is the cooperative residual of
   this clause, and the bound is claimed accordingly: auto-loads and
   declared startup reads, not every sentence that could send a session to
   a file.

4. **The wiring gate, in `check.sh` itself.** A check that is not invoked
   cannot grep for its own invocation (the first pass's P2, which disproves
   the first draft's "cannot be dropped quietly"). So the assertion moves to
   the place that IS always reached: an inline gate in `check.sh`, beside
   the gitenv-list loop, in two parts. An ANCHORED list of required checks
   (today's fifteen plus this one, by name) that must each exist as a file
   and be invoked uncommented, so deleting a check together with its line
   fails too (the second pass's P2: discovery alone finds only the files
   that remain); and DISCOVERY over `scripts/check-*.sh` so a new check
   that is not invoked fails as well. It generalizes the standing rule ("a
   done-check not invoked here is a feature nothing gates") from prose into
   a gate. Mutations at implementation, each watched to fail: delete the
   `check-prose-budget.sh` line; delete a different check's line; delete a
   check's file AND its line; comment the line out. An operator who edits
   the anchor does so in `check.sh`'s diff, which is the visibility this
   plan is after, not tamper-proofing.

**What counts.** Bytes on disk, `wc -c`, the whole file including code
blocks and frontmatter. Not words, not tokens: tokens need a tokenizer the
tier does not have, and bytes/4 is the same estimate for every file, so the
ranking is unchanged. Not "rules" versus "examples": a check that exempts a
kind of line teaches which kind to write.

**How a ceiling is raised.** Only by editing its line, with the date and the
operator's reason in the `why` column, in the commit that needs the room; a
class total likewise. The rule for lanes, one paragraph in the skill's
landing recipe: *a lane never raises a ceiling, as it never raises its own
tier (D-104); it consolidates, retires, or reports the overage in READY with
the bytes, and the operator decides at landing.* The orchestrator sees a
raise as a diff line in a file whose only purpose is to be read at landing.
**Lowering is free**: a lane that trims a text lowers its ceiling to the new
size plus headroom in the same commit, so headroom does not become the next
lane's room. The operator's own `~/.claude/CLAUDE.md` is NOT a relief
destination for repo text: it is outside the budget and outside the repo,
and the plan names the fleet skill, under its ceiling, as where process
rules go.

**Headroom, and the honest runway.** The formula: ceiling = landed size +
10%, rounded up to the next 4,096 bytes (KB here means 1,024 bytes; the
ceilings sit on 4,096-byte boundaries). The runway is NOT a forecast of
calm; it is the arithmetic of the growth rate against the headroom the
formula actually leaves, and both passes corrected the draft's. At 8 to 12
decisions a week, if the copies were per-decision:

| Text | Landed | Ceiling (formula) | Headroom | Bytes per decision | Weekly growth | Fires after |
|---|---|---|---|---|---|---|
| `CLAUDE.md` index, one line each | 10,240 | 12,288 | 2,048 | 200 | 1.6–2.4 KB | 0.9–1.3 weeks |
| charter, one GIVEN each at a 400 B cap | 46,080 | 53,248 | 7,168 | 400 | 3.2–4.8 KB | 1.5–2.2 weeks |
| charter, one GIVEN each at today's mean | 46,080 | 53,248 | 7,168 | 837 | 6.7–10 KB | 0.7–1.1 weeks |
| charter today, one GIVEN each | 99,972 | 110,592 | 10,620 | 837 | 6.7–10 KB | 1.1–1.6 weeks |

So a ceiling over a per-decision text is either raised weekly (the trap) or
set with a quarter's room (21 to 31 KB on the index, 42 to 62 KB on the
charter, which is no ceiling). Neither holds. What holds is the second half
of the mechanism, applied to BOTH copies: the index line and the GIVEN are
topical (section 3), so a decision adds 6 to 8 bytes of identifier (0.6 to
1.2 KB a copy a quarter) and sometimes a clause; what it adds must fit the
headroom or be offset by a removal in the same commit, and the READY line
says which. Growth is small and positive, not zero (the second pass's P1
against the draft's "net to zero by construction", withdrawn). The ceiling
then fires when a lane adds a ruling with no home or a clause with no
offset, which is the conversation it exists to force. How often: NOT
MEASURED; the sweep's report counts bytes added and removed per decision
(topical saturation, the second pass's hypothesis), and the first month's
raises are the measure. The draft's "once a month" and "one commit in
thirty" are withdrawn.

**The "fires on everything" trap, and why this is not it.** The commit gate's
cut warning ("paths you did not claim") fired on nearly every commit, warned,
and would have been disabled. This check fails red, so it cannot be ignored
without a diff; the fix is in its output (the largest sections, the one-line
raise); it is wired by a gate it cannot remove itself; and, with topical
copies, it fires on a new ruling with no home rather than on every decision.
The remaining disable path is a commit that raises every ceiling by a lot
with a reason that says nothing. That is the operator's prerogative and
leaves a dated line saying so; the plan does not try to bind the operator,
only to make the choice visible and dated. The first pass's closing line is
the right register: this can hold repository-controlled prose; it cannot
guarantee a share of spend; and nothing in the evidence says the operator
would disable it.

**Refused alternatives.**
- *A token count instead of bytes.* Needs a tokenizer in the tier; bytes/4
  misranks nothing.
- *Per-file ceilings only* (the first draft). Lets a new file under a new
  ceiling add 100 to 150 KB a quarter (P1, taken).
- *A total ceiling only.* One number hides which file grew and lets the
  index eat the rules; the per-file lines are the attribution.
- *A ceiling in the Go test suite.* It would hold, but the ceilings would
  live in Go source and `cost-report.sh` could not read them; a `.tsv` is
  read by both.
- *A per-commit growth cap.* Split across two commits; a landing squashes.
- *Warn in the tier, deny at the commit gate.* The commit gate reports claim
  collisions only (settled), and a warning is the trap.
- *Generated `CLAUDE.md` from the record.* The size becomes a function of
  the record, which grows without bound; the ceiling then fails the
  generator, not an author.

**What it cuts.** No per-decision byte allowance; no exemption marker; no
automatic trimming; no ceiling on `docs/decisions.md`, the plans or
`docs/wishlist.md`, which cost nothing until read and were read by range 452
times out of 453 this week.

## 3. The four-files-per-decision tax

**Failure it answers.** In the last 101 commits `docs/decisions.md` was
touched 39 times, `CLAUDE.md` 36, the charter 34, the skill 28, `USAGE.md`
27; 29 commits touched all of record + `CLAUDE.md` + charter, and 12 touched
all five. The same decision is AUTHORED four times in four registers: D-111
is 3,455 bytes in the record, 673 in `CLAUDE.md`, 562 in the charter, one
line in the skill. Two of those four are then loaded on every request or
every review, and each copy drifts: the charter has three different
sentences naming D-111 (lines 1027, 1043, 1178). And, per section 2, a copy
that is one line PER DECISION cannot live under a ceiling.

**The shape of the always-loaded copies: topical, naming decisions.** A
ruling is one line (in `CLAUDE.md`'s index section) or one GIVEN (in the
charter), about one subject, ending with the D-numbers that made it:

```
- Scopes are exact paths and directory prefixes; containment is `scope == path || HasPrefix(path, scope+"/")`;
  no globs; a comma in a scope is refused; backslashes fold before the absolute check (D-002, D-066, D-100).
```

A new decision on scopes amends that line and appends its number. The line
grows by 6 to 8 bytes and, sometimes, a clause; it never becomes two lines.
`check-charter.sh` is satisfied by a MENTION of each D-number (its header
says so: "a mention is not a summary"), so a consolidated GIVEN keeps every
decision charted. 56 of today's 112 GIVENs already name two or more
decisions; 4 name none. That is citation overlap, not proof that a new
decision usually fits an existing clause without growing it (the second
pass's hypothesis); the sweep's added-and-removed count is what would show
it. The record keeps the per-decision text and a
per-decision index at its head (or as the file list, if the split below is
taken), so "find D-077" is one `grep` away and costs no request anything.

**OPEN (the operator; it amends D-112's shape).** The `claude-md` brief,
from the operator's acceptance of the trajectory consult's recommendation 1,
specifies ONE line per decision in `CLAUDE.md`. That is the right one-time
cut (43,974 bytes of bullets to about 20 KB of lines) and the wrong durable
shape (section 2's table: it fires a 10%-headroom ceiling in half a week).
This plan recommends the `claude-md` index land as briefed and step 3's lane
consolidate it to topical lines, so the index section's ceiling can be set
at about 8 KB and hold. The alternative, keeping one line per decision and
setting the index ceiling with a quarter's room, is recorded as refused
above; the operator may still take it, and then the ceiling on that section
is a report, not a bound.

**The kind table.** Which texts a decision must touch, by what it changes.
"Record" is `docs/decisions.md`; "index" is the topical line in `CLAUDE.md`;
"GIVEN" the charter line `check-charter.sh` requires; "rule" is prose in
`CLAUDE.md`'s rules sections.

| Kind | Example | Record | Index (amend a topical line, add the number) | GIVEN (same) | Rule text in `CLAUDE.md` | USAGE | Core skill | Fleet skill |
|---|---|---|---|---|---|---|---|---|
| Verb, flag or subcommand added or changed | D-110 `buddy update` | yes | yes | yes | no | yes (the reference) | if a lane uses it (standing rule) | if only a coordinator does |
| Gate verdict, schema, fence, invariant | D-107, D-108 | yes | yes | yes | only if a session must ACT on it daily (the fail-closed hook line) | yes | the refusal's shape, if a lane meets it | no |
| Process rule (how the fleet runs) | D-103, D-104, D-109, D-111 | yes | yes | yes | no | no | the lane-facing clause only (what a lane does at READY, its tier cap) | the coordinator's side |
| Measured fact or gotcha | the `cp` kill, `ISON` shapes | optional, a dated line | no | the charter's facts section | gotchas section, under the ceiling | no | no | no |
| Cut or refutation | glob scopes, the `exit` verb | yes | yes | yes | no | no | no | no |
| Plan accepted | D-116 (this) | yes, when accepted | yes | yes | no | no | no | no |

Three things the table changes. A process rule's coordinator side never
enters USAGE or the core skill (the skill doubled in 8 days on five process
decisions, about 6.5 KB each), while its lane-facing clause stays in the
core so an ordinary lane never has to load the fleet skill to learn what
READY says (the first pass's second hypothesis). A measured fact never gets a
decision number unless something was decided about it. And nothing but the
amended index line and GIVEN is always-loaded for most kinds: the record
carries the why.

**The template.** A record entry gets a fixed tail naming where its copies
live, so a reviewer and the sweep can find them:

```
## D-NNN — <title>
Decided <date>, <by whom>. <The failure. The number. The alternative refused. What it cuts.>
...
### Where it lives
- index: CLAUDE.md § Decisions, the "<topic>" line (amended; number added)
- GIVEN: review-charter.md GIVEN <n> "<topic>" (amended; number added)
- code: <file:line or test name>, or none
- skill: <core|fleet> § <section>, or none
- USAGE: § <section>, or none
- amends: D-MMM <which clauses; whether the predecessor's text is wholly carried here>
```

The first draft proposed a check that the index line and GIVEN appear
verbatim in their files. Withdrawn (the first pass's P2): with topical
copies there is no per-decision verbatim text to check; and the draft's
version exempted any entry without the tail (so a new entry could opt out)
and would have conflicted with retirement (an amended line no longer matches
the predecessor's recorded copy). What remains checkable and cheap is the
D-number mention, which `check-charter.sh` already does for the charter and
a sibling clause can do for `CLAUDE.md`'s index section (every D-number in
the record appears in the index section, or in a `retired to the record:`
line the section may carry). **OPEN**: whether to add that clause in step 3;
it costs one `grep` and a planted fixture.

**The charter.** Three questions from the brief.

*Can the GIVENs be one line each?* Consolidated by topic, yes, and this is
the largest single cut available: 112 GIVENs at a mean 837 bytes are 93,744
of 99,972 bytes; consolidated to perhaps 60 topical GIVENs under a 400-byte
cap they are under 25 KB. The charter's job is anti-relitigation (its header
and `check-charter.sh`'s both say so); the ruling and the refused alternative
do that, the episode does not. The charter gains ONE pointer line: *the
record is `docs/decisions.md`, one `## D-NNN` heading per decision; read a
heading's section when a GIVEN's why matters to a finding; do not read the
file whole.* Codex is read-only but not blind: it can read a repo file, and
the check's header line "it has no way to know that file exists" is cured by
telling it. NOT MEASURED: whether a short GIVEN is re-proposed more often
than a long one, or costs the reviewer more reads and turns (the first pass's
first hypothesis, and D-077 and D-106 carry operative exceptions, not only
episodes). So step 2 PILOTS: 20 GIVENs consolidated first, then ten passes
measured on three counts (findings answered "settled, D-NNN", record reads
in the Codex trace, turns to verdict) against the ten before. The
acceptance criterion is written down BEFORE the counts arrive (proposed: no
count worse by more than one per pass on average; **OPEN**), the inputs are
matched where they can be (the same diff reviewed under both charters for
at least three of the ten), and otherwise the comparison is reported as
observational, since ten different reviews cannot separate the charter's
effect from the diffs'. The rest follow only if the criterion holds, and a
GIVEN whose exceptions are operative keeps them, within the class total.

*Can the charter be scoped per review (GIVENs by area)?* It could: tag each
GIVEN `[gate]`, `[ledger]`, `[chat]`, `[fleet]`, `[adapter]`, `[docs]`, and
let `codex-review.sh` take `CODEX_CHARTER_AREAS=gate,ledger` to include the
always-on sections (what this is, invariants, environment facts, review
conventions) plus the named areas, printing `areas=` in its header line.
Refused for now: consolidation alone takes the charter from 100 KB to about
30 KB with no new knob and no risk of a reviewer missing the GIVEN that would
have settled its finding; scoping saves at most the rest and adds the risk.
Recorded **OPEN**: revisit when the consolidated charter crosses its ceiling.

*Can the code pass take a shorter charter than the design pass?* NOT
MEASURED. The code pass reviews a diff against settled rulings exactly as the
design pass does, and D-106's six re-verify rounds were code passes
re-proposing recognizer shapes. Same answer: consolidate, measure, then
decide. Recorded **OPEN**.

**Refused alternatives.**
- *Dropping the GIVEN requirement for some kinds.* `check-charter.sh`'s
  motivating failure (three decisions re-proposed because they were not
  charted) is exactly what returns.
- *A decision carrying its own full text in `CLAUDE.md`* (today). That is
  the 69 KB.
- *One line per decision forever* (the first draft, and D-112 as briefed).
  Section 2's table.
- *A generated index.* Section 2.
- *The verbatim copy check.* Withdrawn, above.

**What it cuts.** Any "why" in the always-loaded copies; USAGE and core-skill
text for the coordinator's side of process decisions; the copy check.

## 4. Retirement

**Failure it answers.** Text enters the always-loaded files and never leaves.
Twelve `CLAUDE.md` bullets say "amends" or "supersedes" another decision, and
in every case BOTH texts stand (D-104's model clause and D-111's amendment of
it are both in the file). 88 of 111 decisions are cited by D-number in Go
source, so the code and its tests carry them, and the prose repeats them.
And this week's transcripts, counts only: of 111 decisions, 29 were named in
some session's own text; 82 were named by no session in 70, while all 111
rode every request. (A floor on "used": a session obeys a rule without
naming it. It is an instrument for "which text does a session reason with",
not for "which rule may be dropped".)

**The rule.** A decision has three homes and leaves two of them. The RECORD
is append-only and never loses an entry: it is history, and
`check-charter.sh` guarantees every entry keeps its D-number in the charter.
The topical INDEX line and GIVEN keep the number as long as the ruling
stands. Everything else is RULE TEXT and leaves on one of four triggers:

1. **Superseded, clause by clause.** When D-b amends D-a, ONLY the clauses
   D-b changes leave D-a's text; the rest stands. D-111 changes D-104's
   model clause and none of its tiers, passes or receipt, so D-104's tier
   text stays and its model clause goes (the first pass's P1: the draft's
   "D-a's rule text leaves" would have deleted D-104's tiers and D-083's
   relay floor, against GIVENs 105/112 and 84/110). A predecessor becomes a
   bare pointer ONLY when the successor's text carries the whole surviving
   ruling, and the landing lane's READY carries the clause mapping (which
   clause went where). The lane landing D-b does this; today, 12 amendments,
   none did.
2. **Absorbed.** A decision whose refusal or behaviour a test holds and whose
   D-number the code cites needs no rule text beyond its number on a topical
   line. The gotchas section keeps measured facts a test cannot hold (the
   locale traps, the launch-constraint delay). Today: 88 of 111 are cited in
   Go; how many of their rule texts are redundant is NOT MEASURED and is
   step 5's first count.
3. **History-only.** An episode (D-106's seven commits, the A-01 re-review)
   stays in the record; the topical line says what stands; nothing else.
4. **Unused.** Rule text named in no session's own text in 30 days and not
   an invariant or a gotcha is a candidate for the number alone. The 30-day
   instrument is the count above, run by the sweep, never automatic.

**Who decides.** The operator, on two paths. At landing: a lane's READY
carries `retires: <what, from where, bytes>` (and `amends: <clause map>`)
and the orchestrator folds it or the operator declines it in the pane.
Monthly: a `prose-sweep` lane (chore when it only moves text the record
already holds; lite when a rule's wording changes) proposes retirements with
the four triggers' counts, lowers every ceiling it can, reports before and
after bytes, and reports bytes ADDED and REMOVED in the two topical copies
per decision landed since the last sweep (the saturation measure section 2
owes). Nothing is retired on a count alone; the count is the
evidence the operator reads.

**The whole-file read, measured and set aside.** The draft worried that a
lane would `cat` the 602 KB record and carry 150k tokens after. Measured: of
453 tool calls naming `docs/decisions.md` this week, 1 read it whole and 452
read a range or searched it (a `Read` without a range or a `cat` with no
filter counted as whole). Sessions already read it by heading. The hazard is
real in principle and small in practice; the index line's pointer says how
to read one entry and that is enough. **OPEN, no longer recommended on this
ground: one file per decision** (`docs/decisions/D-NNN.md`). Its surviving
benefit is the write side: every lane writes its OWN file, so the `--shared`
claim on `docs/decisions.md` that every brief in today's batch carries, and
the append conflicts orc-10 resolves at landing, both go away;
`check-charter.sh` reads filenames instead of headings (one `sed` line). NOT
MEASURED: how many landings had an append conflict to resolve.

**Refused alternatives.**
- *Automatic retirement* on the 30-day count. A rule nobody names is often a
  rule everybody obeys (invariant 2 is named by no session this week).
- *Deleting record entries* for superseded decisions. The record is the
  history the charter rests on; `check-charter.sh` would fire, correctly.
- *A `deprecated:` marker in the always-loaded text.* It is more text.
- *Whole-ruling retirement on "amends"* (the first draft). P1, taken.

**What it cuts.** Any retirement of record entries; any retirement without a
READY line or a sweep report naming it; the sweep touching code or tests.

## 5. Measurement

**Failure it answers.** `cost-report.sh` prints MCP schema bytes, journal
traffic, ledger fan-out and MCP call counts; it has no line for the files
this plan is about and none for what the week's requests actually carried.
The audit that found the 71% was hand-run. The `claude-md` brief says it
outright: "`scripts/cost-report.sh` has no CLAUDE.md line".

**The instrument.** Two sections added to `cost-report.sh`, counts and bytes
only (invariant 9: nothing from a transcript is printed but numbers). The
report CONSUMES the check's table and classes, so it neither double-counts a
section inside its file nor adds a per-session body to every request.

```
Fixed prose (docs/prose-budget.tsv)
every-request                 bytes=...    ceiling=32768  headroom=..%  est_tokens=...
  CLAUDE.md                   bytes=23114  ceiling=28672  headroom=19%  est_tokens=5778
  CLAUDE.md#Decisions         bytes=7870   ceiling=8192   headroom=4%   est_tokens=1967   (a section: inside CLAUDE.md, not added)
per-session                   bytes=...    (loaded in N of M sessions; rode R of Q requests)
  skills/buddy/SKILL.md       bytes=...
read-first                    bytes=...    (obeyed in N of M sessions, by a Read/cat of a listed file in the first 10 tool calls)
codex-pass                    bytes=...    (passes in window: N, from receipts naming codex@)

Context billed (this repo's transcripts under CLAUDE_PROJECTS_DIR, last 7 days)
sessions=70  requests=5254  cache_read=1091524052  cache_write=15409032  output=4537077
prompt mean=210695 p50=188411 p90=319750
est_share_of_cache_reads (estimates: bytes/4 x requests carrying it / cache_read): every-request=8.4%  per-session=2.9%
docs/decisions.md tool calls: mentions=453 whole_file=1 ranged_or_search=452
```

The transcript section reuses the report's existing `jq` pass (it already
walks `~/.claude/projects` for MCP calls) and dedupes by request id across
files, because one turn writes several assistant records with the same
usage and a resumed session copies its predecessor's lines (21,717 records
this week against 5,254 or 5,452 unique requests by two id rules; the
fixture includes a resumed transcript with copied lines and fixes the rule).
Skill exposure is computed over the SAME deduplicated request set as the
totals, each request flagged by whether the body was in its session's
context when it ran (the first pass's P2: 21 of 70 sessions is not 30% of
requests; the second pass's P2: a per-file count mixes populations;
measured over one deduplicated set, 1,765 of 5,452 requests, 32%).
"Whole file" is a `Read` with no range or a `cat` with no filter; anything
else is a mention, labelled so. A `check-cost-report.sh` with a synthetic
two-request transcript fixture and a planted over-ceiling table line (two
clauses), invoked from `check.sh`, because the report has none today and a
report nothing gates is the one whose numbers drift.

**The before numbers, 2026-10-28, this repo, main at 8449a87** (repeated so
the first weekly run has something to stand against):

| | |
|---|---|
| `CLAUDE.md` | 69,690 B (index section 43,974 of it, 101 bullets, mean 435 B) |
| `skills/buddy/SKILL.md` | 73,144 B; body present in 21 of 70 sessions; carried by 1,765 of 5,452 unique requests (32%) |
| `docs/review-charter.md` | 99,972 B; 112 GIVENs, mean 837 B, 56 naming 2+ decisions, 4 naming none; 47 Codex passes in 101 commits (14 days) |
| `AGENTS.md` | 4,586 B |
| "Read this first" set | ~305,000 B over five files; obedience NOT MEASURED |
| `docs/decisions.md` | 602,399 B; 111 entries; median 4,223 B; last ten mean 8,843 B; read whole 1 time in 453 calls |
| `docs/USAGE.md` | 168,634 B |
| Requests, 7 days | 5,254 (or 5,452 by message id) unique across 68 sessions with usage; median 68 per session |
| Cache reads, 7 days | 1,091,524,052 tokens; mean prompt 210,695; p50 188,411; p90 319,750 |
| Decisions, rate | 102 → 111 between 10-20 and 10-28; 86 → 111 since 10-14 (8 to 12 a week) |
| Decisions named in a session's own text, 7 days | 29 of 111; 4 of them in five or more sessions |
| Files per decision commit | record 39, `CLAUDE.md` 36, charter 34, skill 28, USAGE 27 of 101 commits; all five in 12 |
| Amendments whose predecessor text still stands | 12 of 12 |

**What week-over-week shows.** The number the plan can be held to is the
`every-request` class's bytes (absolute) and its headroom; the number that
says what it bought is `est_share_of_cache_reads`, an estimate, printed as
one. Beside both, `prompt p50`, which this plan does not move and the
`handoff-word` lane does; printing them together is what keeps the two from
being confused in the next audit. For the charter, step 2's pilot counts.

**Refused alternatives.**
- *Reading the ledger's `session_context` instead of transcripts.* One row
  per session (the last turn, by design, D-020); the week's sum needs every
  turn.
- *A dollar figure.* Prices change, the two-repo audit's dollars were a
  hand sum, and bytes are what the plan can be held to.
- *A promised percentage.* A fixed byte allowance cannot hold a fixed share
  of a prompt whose length this plan does not control (P2, taken).
- *A new report script.* `cost-report.sh` is the one the audit and the
  briefs name; a second script is the one nobody runs.

**What it cuts.** Per-session rankings (a session id beside a token count is
a leaderboard, not a measurement); any printing of file or message text.

## 6. Landing order

Each step is one lane, after `claude-md` (D-112) and `skill-split` (D-114,
D-115) land, because they set the sizes the ceilings are cut to. Planned
against today's files; if either lands first, step 1's lane reads the landed
sizes and replaces this section's illustrations with them.

| # | Lane | What lands | Tier and why | Depends on |
|---|---|---|---|---|
| 1 | `prose-budget-check` | `docs/prose-budget.tsv` with per-file lines and the four class totals at landed size + 10% rounded to 4 KB (the charter and `read-first` at today's sizes, lowered by steps 2 and 5); `scripts/check-prose-budget.sh` (measure, enumerate from its own list, planted fixtures for over-ceiling AND unbudgeted AND vanished); the wiring loop in `check.sh` over every `check-*.sh`; one paragraph in the skill's landing recipe (a lane never raises a ceiling; READY names overage, retirements and clause maps); D-116's record entry, topical index line, GIVEN. | **lite**: a new hermetic gate over prose, no `buddy` code path, no verdict. Codex question: name a way a lane grows what a session or a Codex pass loads unasked without this tier failing. Mutation: remove the check's line from `check.sh` and watch the wiring loop fail; remove another check's line and watch it fail too. | claude-md, skill-split landed |
| 2 | `charter-pilot`, then `charter-lines` | Pilot: 20 GIVENs consolidated by topic under 400 B each, numbers kept, the record pointer line added; ten passes measured (settled-findings, record reads, turns) against the ten before. Then the rest if the counts hold; `check-charter.sh` unchanged and green; the charter's and `codex-pass` ceilings lowered. | **lite**: docs; the gate that protects it exists. Codex question: does any consolidated GIVEN lose a ruling, an operative exception, or the alternative it refuses? | 1 |
| 3 | `index-topical` | `CLAUDE.md`'s index consolidated to topical lines naming D-numbers (**OPEN**, amends D-112's shape); the `### Where it lives` tail in the record's header comment; the optional index-mention clause (**OPEN**); the "Read this first" list cut to what every session must read, the rest named where the task needs them (**OPEN**: which); the one-file-per-decision split only if the operator takes it on the write-side ground. | **lite** (CLAUDE.md is the authority file) | 1 |
| 4 | `cost-report-prose` | The two report sections consuming the table; `check-cost-report.sh` from `check.sh`. | **lite**: a report with a done-check. | 1 |
| 5 | `prose-sweep-1` | The first retirement: the 12 amendments folded clause by clause with the maps in READY; the coordinator side of process rules out of the core skill and USAGE; redundant rule text of code-held decisions trimmed (the count first); every ceiling lowered to what it lands at. | **chore** if it moves only text the record holds; **lite** if a rule's wording changes. | 2, 3 |
| 6 | `prose-sweep` (standing) | One line in the fleet skill's recipe: monthly, the sweep lane with the four triggers' counts. | **chore** | 5 |

The cheapest version that holds is step 1 with the aggregates, the
two-file startup-read enumeration and the anchored wiring gate, PLUS the
topical update policy for BOTH the index and the charter from the day step 1
lands (a new decision amends a line and fits the headroom or offsets; it
does not add a line), with the growth accounted for in READY. Without the
policy on the charter, its formula ceiling of 110,592 bytes is crossed by
the 13th GIVEN at today's mean, inside two weeks. The bulk consolidation of
the charter (step 2) and of the index (step 3) are byte cuts that can
follow; the policy cannot. Steps 4 to 6 make the next audit a report instead
of a hand count.

## 7. What this plan does not do

- The operator's own `~/.claude/CLAUDE.md` (3,657 B, every request, every
  repo) is outside the repo and the tier, and is not a destination for repo
  text. The same `.tsv` and check can be copied into any repo, this
  operator's other fleet repo included; the check reads a table, not this
  repo's names.
- The system prompt, tool schemas and the MCP schema (`cost-report.sh`
  already prints the last) are not this repo's prose.
- Conversation length (p50 188k, p90 320k) is the `handoff-word` lane's and
  the operator's 300k decision; this plan promises bytes, reports shares.
- Codex-side prompt caching of the charter is NOT MEASURED; the charter's
  cost here is counted in bytes per pass and in the watchdog budget the
  script's header names, not in dollars.
- A loader this plan's enumeration does not know (a future harness
  auto-load location) is a residual; the audit's fixed-prefix median is the
  number that would show it. So is a startup directive respelled past the
  grep clause in an authority file: cooperative, caught at review, claimed
  as such.
- Nothing here changes a verdict, the ledger, the fence or an invariant.

## Taken and refuted

The first Codex design pass (gpt-6-astra, xhigh, 2026-10-28; verdict REVISE)
over draft 1. "Taken" names where it was folded.

| # | Finding (Codex) | Verdict | Where |
|---|---|---|---|
| 1 | P1: per-file ceilings let a lane add a new budgeted file; 1 KB/decision of new rules files is 104–156 KB a quarter. | **Taken.** Mandatory class totals (`every-request`, `per-session`, `read-first`, `codex-pass`); a new file consumes class headroom. | §2 part 1 |
| 2 | P1: the enumeration list omitted the charter; a deleted table row removes coverage; "read `docs/current-rules.md` first" escapes the budget; the operator's global file was offered as a relief destination. | **Taken**, all four. The check's own list names `docs/review-charter.md` and `AGENTS.md`; a vanished budgeted file fails; the "Read this first" section is parsed as a `read-first` class (measured: it names ~305 KB today); the operator's file is named as NOT a destination. | §2 parts 1, 3; §1 table |
| 3 | P1: "D-a's rule text leaves when D-b amends it" deletes still-standing clauses (D-104's tiers under D-111; D-083's floor under D-109), against GIVENs 105/112 and 84/110. | **Taken.** Clause-level retirement; a bare pointer only when the successor carries the whole surviving ruling; the clause map in READY. | §4 trigger 1 |
| 4 | P2: a check cannot grep for its own invocation once it is not invoked; the draft's "cannot be dropped quietly" was false. | **Taken.** The wiring assertion is an inline loop in `check.sh` over every `check-*.sh`, with a two-target mutation check at implementation. | §2 part 4 |
| 5 | P2: the verbatim copy check exempted any entry without the tail (a new entry could opt out) and conflicted with retirement; defer it. | **Taken.** Withdrawn; replaced by a D-number mention clause (OPEN) and the tail as convention. | §3 template |
| 6 | P2: mandatory per-decision index lines and GIVENs exhaust 10% headroom in 1–2 weeks, not "40 decisions / monthly"; a quarter adds 21–31 KB of index and 42–62 KB of GIVENs; move the exhaustive index out of `CLAUDE.md`. | **Taken, and it reshaped the plan.** The runway table; topical always-loaded copies that net to zero bytes per decision; the per-decision index lives in the record; the one-line-per-decision shape recorded as refused and the change to D-112 marked OPEN; "monthly" and "one in thirty" withdrawn. | §0, §2 headroom, §3 shape, §6 |
| 7 | P2: 21 of 70 sessions is not 30% of requests; a byte bound cannot hold a percentage; 47/14 × 99,972 is 336 KB/day not 590; "Bash calls naming decisions.md" are mentions, not whole reads; "the other 85% is conversation" was not established. | **Taken, all five.** Measured the weighting (1,754 of 5,439 requests, 32%); the plan promises bytes and reports shares; 336 KB/day; whole reads classified and counted (1 of 453); the fixed-prefix sentence rewritten from the audit's 56k. | §1, §5 |
| H1 | Hypothesis: uniform 400 B GIVENs may raise retrieval and review work; D-077/D-106 carry operative exceptions; pilot a subset and measure reads and turns too. | **Taken.** Step 2 is a 20-GIVEN pilot measured on three counts; operative exceptions stay within the class total. | §3 charter; §6 step 2 |
| H2 | Hypothesis: moving every process rule to the fleet skill may make ordinary lanes load it; keep a lane-facing contract in core. | **Taken.** The kind table splits a process rule into its lane-facing clause (core) and its coordinator side (fleet). | §3 kind table |
| — | The closing line: this can hold repository-controlled prose over three months; it cannot guarantee a share of spend; the evidence does not say the operator would disable it. | **Agreed**, and adopted as the plan's own claim. | §2 trap; §7 |

Refuted by measurement after the pass, from the first draft's own claims
rather than Codex's: the whole-file read of `docs/decisions.md` as a live
hazard (1 of 453 calls), so the one-file-per-decision split is no longer
recommended on that ground and stands OPEN on the write-side ground alone.

The second Codex design pass (gpt-6-astra, xhigh, 2026-10-28; verdict REVISE)
over draft 2. It found no contradiction with a charter GIVEN and judged
findings 1, 3 and 5 above adequately folded.

| # | Finding (Codex) | Verdict | Where |
|---|---|---|---|
| 2-1 | P1 (finding 2 incomplete): a startup read under another heading, or in `AGENTS.md` (which already names `CLAUDE.md`, DESIGN and the charter), escapes a parser that reads only "Read this first". | **Taken.** Both authority files parsed; one level of closure; a grep clause for startup-directive forms outside the section; the bound's claim narrowed to auto-loads and declared startup reads, the respelled directive recorded as the cooperative residual. | §2 part 3; §7 |
| 2-2 | P1 (finding 6 incomplete): the stated minimum covered the index only; a charter still taking one GIVEN per decision crosses its 110,592 B formula ceiling at the 13th GIVEN; "net zero by construction" has no basis (identifiers alone add 0.6–1.2 KB a copy a quarter; clauses are unmeasured). | **Taken.** The topical policy is required for both copies from step 1; "net zero" withdrawn for "fits the headroom or is offset, said in READY"; the 110,592 row added to the runway table; the sweep counts bytes added and removed. | §0; §2 headroom; §6 |
| 2-3 | P2 (finding 4's fix): deleting a check's FILE and its line evades a discovery-only loop; the two proposed mutations do not exercise that case. | **Taken.** An anchored list of required checks plus discovery; four mutations including paired deletion and a commented line. | §2 part 4 |
| 2-4 | P2 (finding 7's fix): 1,754 of 5,439 is a per-file count; the totals use 5,254 unique requests; the skill share is 2.6–2.9%, not one value. | **Taken.** Recomputed over one deduplicated set (1,765 of 5,452; two id rules disagree, 5,254 vs 5,452, both reported, the fixture settles the rule); exposure and totals from the same set. | §1; §5 |
| 2-5 | P2: the runway table used bare 10% headroom while the formula rounds to 4 KB; show landed, ceiling, headroom; define KB. | **Taken.** The table carries all three; KB defined as 1,024 bytes on 4,096 boundaries; "half a week" withdrawn for 0.9–1.3 weeks. | §2 headroom |
| 2-a | Arithmetic qualifications: 112 vs 113 GIVENs; label the population behind "mean 77"; §0's ranges should match §2; hello's 1.2% is not reconstructible from the row. | **Taken**, all four. | §0; §1; §3; §5 |
| 2-H1 | Hypothesis: ten different reviews cannot isolate the charter's effect; set the acceptance criterion first; match inputs or report as observational. | **Taken.** Criterion written before the counts (proposed, OPEN); at least three matched diffs; otherwise observational. | §3 charter |
| 2-H2 | Hypothesis: 56 multi-citation GIVENs show overlap, not that new decisions fit without growth. | **Taken** as a measurement: the sweep's bytes added and removed per decision. | §2 headroom; §3 |
| 2-H3 | Hypothesis: a textual wiring check may not establish execution; include a commented invocation among the mutations. | **Taken.** | §2 part 4 |
| — | Closing: the cheapest defensible version is step 1 with corrected startup coverage and wiring plus the forward topical policy for both copies, growth accounted explicitly; whether it stays comfortable for three months is a measurement question, and the bound should not depend on comfort. | **Agreed**; it is §6's closing paragraph. | §6 |

Left open after two passes (the brief's bound): a startup directive
respelled past the grep clause; whether topical clauses saturate or grow
(the sweep measures it); the pilot's acceptance criterion (OPEN for the
operator); and the three OPEN shape decisions in §3 and §4 (the index's
shape against D-112 as briefed, the index-mention clause, one file per
decision).
