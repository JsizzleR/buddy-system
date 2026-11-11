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
- **Pick by class.** Default to the cap's cheaper model for a class where
  the report shows its rework rate and its fail rate no worse than the other
  model's. Escalate one step where either rate is clearly worse. In a cell
  below the floor, use the model the cap names as its default, or, if it
  names none, the default lane model, and infer nothing. The floor and "clearly worse" are the
  operator's numbers, set with the cap; until they are set, the proposal is
  10 items per cell and twice the other model's rate. The report prints the
  observed rates, so either number can be revised.
- **Record the pick where the report reads it back:** the item's claim
  description begins with two tokens, `model=<id> class=<word> `, each once,
  then the description. `<word>` is one of `docs`, `code`, `test`,
  `research`, `ops`; a missing, repeated or unlisted token is counted as
  unknown and never printed. The lane makes the claim, so its brief gives the
  exact start:
  `buddy claim <item> --desc "model=<id> class=docs <what and why>" --scope <path>`.
- **The item's outcome is its own release.** A RED along the way keeps the
  claim: the lane fixes and declares again. Only a finished or abandoned item
  is released, and with its outcome, so a lane's release after LANDED PASS is
  `buddy release <item> --outcome pass --note "<reds> <batches>"`, never a bare
  one; an abandoned item is `--outcome fail`. A handover is
  `buddy release <item> --to <target>`, which keeps the claim open. A claim
  of the same slug after a release is how the report counts rework.
