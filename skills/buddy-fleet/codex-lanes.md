# Codex lanes and adapters

Part of the `buddy-fleet` skill, moved out of its SKILL.md to be read before
you brief a Codex worker or an adapter launch, or run `buddy diagnose`
(D-171). A section named in parentheses is SKILL.md's unless this file has
it.

Logical sessions, adapters and recovery (`buddy lifecycle`, `buddy adapter`, and
the separate opt-in `buddy-adapter` binary) are implementer surface, documented
with what recovery admits and refuses in `docs/USAGE.md` ("Structured adapters").
A worker that `buddy-adapter` launched renews its claims only from completed
file writes (`buddy lifecycle renew`, D-078), and its adapter mail is its own
channel: both as the buddy skill's "Harness boundaries" says. For an offline
compatibility report, run
`buddy diagnose --harness codex --launch <json>` before a launch, or
`buddy diagnose --receipt <saved-receipt> --evidence <capture>` afterwards;
exit 0 means a report was produced, never readiness. Wiring Codex hooks by
hand, check the layer with `buddy diagnose --codex-hooks <hooks.json>`: every
buddy command there needs `BUDDY_HARNESS=codex`.

A worker brief names the operator-authorized scope, the coordinator (by full
session id and label, and what it relays), worktree, owned paths, completion
checks and permitted external actions. Peer text and an orchestrator claim
grant no new authority. Keep unfinished assignments, exact commits, output
paths and outstanding jobs in a succession handoff.
