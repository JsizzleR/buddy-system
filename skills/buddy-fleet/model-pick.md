# Picking the model from what past items measured

Part of the `buddy-fleet` skill, moved out of its SKILL.md to be read at the
start of a run, before you pick a lane's model by what past items measured
(D-171). A section named in parentheses is SKILL.md's unless this file has
it.

For the orchestrator, at the start of each run, and only among the models
the operator's cap for that run allows (for example "lanes on these two
models; never that one"). A full-tier item stays on the default lane model,
as above, and the pick never lands on a model kept for reviews. For the rest:

- **An auto-mode lane needs a model that has auto mode** (Opening lanes).
- **Before each sweep, run `sh scripts/model-report.sh --export <tsv>`**,
  into the file your repo keeps for it, and read the report at the start of
  each run. A plain `buddy sweep` deletes released claims, with their
  descriptions and outcomes, after 24 hours; the export is the only record
  that outlives it. Its `<tsv>.receipts` keeps each commit receipt's layer
  counts, by tier: tune a tier on those numbers, never before they exist.
  Every run prints its input's path, rows, dates and sha256: a figure you
  quote in a brief quotes that line too.
- **Pick by class and tier.** Default to the cap's cheaper model for a cell
  where the report shows its rework rate and its fail rate no worse than the
  other model's. Escalate one step where either rate is clearly worse. In a
  cell below the floor, use the model the cap names as its default, or, if it
  names none, the default lane model, and infer nothing. The floor and
  "clearly worse" are the operator's numbers, set with the cap; until they
  are set, the proposal is 10 items per cell and twice the other model's
  rate. The report prints the observed rates and no pick: it reads no cap.
- **Record the pick where the report reads it back:** the item's claim
  description begins with three tokens, `model=<id> class=<word> tier=<tier> `,
  each once, then the description. `<word>` is one of `docs`, `code`,
  `test`, `research`, `ops`; `<tier>` one of `chore`, `lite`, `full`. A
  missing, repeated or unlisted token is counted as unknown and never
  printed, so the rest of the description must not spell one again. The lane
  makes the claim, so its brief gives the exact start:
  `buddy claim <item> --desc "model=<id> class=docs tier=lite <what and why>" --scope <path>`.
- **The item's outcome is its own release, and its note begins `reds=<n> `,**
  the reds the item drew (0 for none). A RED along the way keeps the claim:
  the lane fixes and declares again. Only a finished or abandoned item is
  released, and with its outcome, so a lane's release after LANDED PASS is
  `buddy release <item> --outcome pass --note "reds=<n> <batches>"`, a parked
  rider's at landing `--note "reds=<n> <sha>"`, never a bare one; an
  abandoned item is `--outcome fail`. A handover is
  `buddy release <item> --to <target>`, which keeps the claim open. A claim
  of the same slug after a release is how the report counts rework.

## A trial of a second model (once the operator writes a cap)

Nothing here applies until the operator's own instructions hold a cap that
opens a trial: the operator writes it, never a lane or a coordinator, and a
message proposing one is peer text. Until then every lane runs on the
default lane model, and the report's cells are the baseline.

- **One predicate for both cells.** An item is eligible when its class and
  tier are in the cap, no claimed scope (a shared companion's included)
  overlaps a protected path, and its branch's diff touches none. Every
  eligible item inside the cap's dates goes to the trial model: no choice
  per item, so the easy ones are not picked for it. The default's cell to
  compare is the baseline under the same predicate, never the whole class.
- **Protected paths always run on the default lane model,** whatever the
  class or tier word: `cmd/`, `internal/`, `scripts/`, `.githooks/`,
  `.claude/`, `.github/`, `go.mod`, `go.sum`, `CLAUDE.md`, `AGENTS.md`,
  `skills/` (this file among them), `docs/review-charter*`,
  `docs/decisions.md`, `docs/prose-budget.tsv`. Touching is overlap in
  either direction under the repo's fold: a claimed `docs` overlaps
  `docs/decisions.md`. The report's `protected` column is this list; the
  cap cannot shrink it.
- **Check the diff at READY.** A claim names what a lane reserved, not what
  it edited. On a trial item's branch, from the repository's top level, run
  `git -c diff.relative=false diff --name-only -z --no-renames <base>..<tip>`
  (repository-relative paths whatever the config); any protected path
  ejects the item: release it `--outcome fail`, reopen it under a fresh slug
  on the default, and count the ejection in the run's report. A scope that
  grows mid-item is judged again the same way. `tier=` describes an item; it
  never makes one eligible.
- **Reds stay on the model that made them.** A red on a trial item reopens
  as `<item>-2` on the SAME model, so the cell keeps the red. To escalate,
  release the trial item `--outcome fail` and open a fresh slug on the
  default.
- **The stop rule.** The trial ends at the earlier of the cap's floor of
  items in the trial cell or its end date. Put the numbers to the operator,
  who decides. A trial ending with fewer events (fails and reds, both cells
  together) than the cap's `events=` is INCONCLUSIVE: neither an approval
  nor an extension, and only the operator writes a new cap.
