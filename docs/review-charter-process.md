# Review charter: process GIVENs (full text)

Appended after `docs/review-charter.md` by `scripts/codex-review.sh` only when
`CODEX_CHARTER=process` (or `all`) is set: the full text of the GIVENs only process text
rests on (the skills, `CLAUDE.md`, `AGENTS.md`, a lane's brief, the review docs and
receipts' wording), which no code change can violate. The main charter keeps a one-line
stub of each, under the same number, naming its rule, its D-numbers and its fences (the
code-level ones included); this text is the GIVEN in full, and it binds exactly as the
main charter does (D-128, D-147). The chosen set and why the rest stay in full: D-147.

104. **The orchestrator uses agent tools itself; a subagent is not a lane (D-103).** While it
    runs a fleet, the orchestrator puts side work (a lane's diff, reports, measurements,
    housekeeping, a second opinion; Fable only for a review, D-111, GIVEN 112) on subagents
    and `codex-review.sh`, because its own context
    is the scarce resource before its handoff size.
    A subagent holds no claim, lands nothing, relays nothing, and runs under the parent's id
    and claims (D-099); its findings are evidence once a test reproduces them. Do not propose
    a ledger row or verb for subagents, or counting their tokens against the handoff size.

110. **A relayed operator decision may carry the item's content and scope (D-109, amends
    D-083).** A brief may say its coordinator also relays the operator's decisions on what the
    change says, adds or drops; each such message marks it said in the coordinator's pane, with
    the time, and is then the operator's, as if typed in the lane's pane. Unmarked, it is the
    coordinator's advice. The sender is still the key and the brief still the authority; the
    floor (install, push, settings, permission, trust, schema, a more permissive mode, work
    beyond the launcher's scope) is unchanged, and no other sender relays.
    Prose only: do not propose a verb, flag or row.

112. **Every lane runs on the default lane model, full tier included; Fable is for reviews only
    (D-111, amends D-104).** The operator's rule; D-104's "a full-tier item runs on the
    strongest model available" was read as Fable. Every lane, panel and fold round runs on the
    operator's default lane model unless the operator names another for the item (or sets a
    run's cap the skill's pick rule works within); the tier changes the review only. Fable runs
    only inside a review: the full tier's cross-check, panel lenses on a security-sensitive
    commit, a judgement-call ruling; the pick rule never lands on it, and a non-review subagent
    is not on it. D-175: an item claim's tokens are `model=` `class=` `tier=`, its release note
    begins `reds=<n> `; `model-report.sh` reads them, effort and a `protected` column (the list
    in its header, overlap either way) and prints no pick. A trial model is reached only by the
    operator's written cap assigning every eligible item; a protected path stays on the default,
    and a trial branch's diff is checked at READY. Do not propose "strongest model" wording, a
    lane on Fable the operator did not name, a verb or check reading a lane's model (the report
    is the existing reader), a cap a lane or coordinator writes, or shrinking the protected list.

116. **A brief names a review-round ceiling (D-115).** Default: lite, one second-model code
    pass folded once; full, design + code + at most two re-verify rounds; the operator raises
    it per item. At the ceiling the lane folds narrowly, answers the rest in the commit, and
    records it open (D-106's folds regressed). Do not propose
    re-verifying "until it accepts" without a named ceiling.
