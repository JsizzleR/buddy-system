# Documentation

Start with the [project README](../README.md): what the Buddy System is and how
to install it. The rest, by what you need:

## Using it

| Document | What it is |
| --- | --- |
| [MANUAL.md](MANUAL.md) | The user manual: install, a first claim with two sessions, waits, chat, fleets, work that is not code or is outside git, the memory slot, upgrading, uninstalling and troubleshooting, task by task |
| [USAGE.md](USAGE.md) | Every feature in depth: hooks, claims, messages, the roster, waits, slots, long runs, authority files, ids, the commit gate, chat internals |
| [AGENT-SETUP.md](AGENT-SETUP.md) | The install recipe written for an agent installing buddy for a user, a checkpoint per step |
| [../skills/buddy/SKILL.md](../skills/buddy/SKILL.md) | The skill installed for agents: the order to use the verbs in, written for a session |
| [../skills/buddy-fleet/SKILL.md](../skills/buddy-fleet/SKILL.md) | The coordinator's skill: opening lanes, landing a run, review tiers |
| [../skills/buddy-lead/SKILL.md](../skills/buddy-lead/SKILL.md) | The first-time user's skill: the questions before anything is installed, steps that each end in something working, and `scripts/env-check.sh` |
| [DEMO-RUN.md](DEMO-RUN.md) | Ledger captures from the two-Claude run, beat by beat, and the final ledger of the README's Claude Code/Codex run |
| [PICTURES.md](PICTURES.md) | How the README's pictures were made: sources, what is typeset, the edits to captured text, the takes |

`buddy --help` and `buddy <verb> --help` are always current, and a test keeps
the skills in step with them.

## Why it is built this way

| Document | What it is |
| --- | --- |
| [ARCHITECTURE.md](ARCHITECTURE.md) | The map: what runs, where state lives, how one tool call becomes a verdict, what fails closed |
| [DESIGN.md](DESIGN.md) | The rationale in one read, including the assumptions measurement refuted. Read this before proposing a change. |
| [decisions.md](decisions.md) | The append-only decision record, D-001 onward: for each rule, what was wrong, what shipped, what was cut and why. Search it for the D-number a page cites. |

## Work in progress

| Document | What it is |
| --- | --- |
| [CODEX-ORCHESTRATOR-PLAN.md](CODEX-ORCHESTRATOR-PLAN.md) | Codex orchestration work order and receipts: participation, adapters, a native mixed-fleet exercise and the offline diagnostic built; real-provider and recovery qualification open |
| [BEYOND-CODE-PLAN.md](BEYOND-CODE-PLAN.md) | Plan: buddy for work that is not code, a session over several coordinators, a model pick per item; steps 1 and 5 landed in the skill |
| [NON-GIT-PLAN.md](NON-GIT-PLAN.md) | Plan: sessions outside git on one fleet ledger; stages 1b and 2 built (D-191, D-193) |
| [ROUTING-PLAN.md](ROUTING-PLAN.md) | Plan: a model pick per lane printed inside the operator's cap, never a launcher; step 2 built (D-175) |
| [ROUTER-SKILL-PLAN.md](ROUTER-SKILL-PLAN.md) | Phase 2 built (D-177): a drop-in skill routing models from a user's own past sessions, counts only |

## Working notes, kept as history

Dated, and not descriptions of the current tool; each carries a status line.

| Document | What it is |
| --- | --- |
| [wishlist.md](wishlist.md) | Field notes from orchestrated runs: problem reports, most since answered by a decision |
| [KEEPALIVE-PLAN.md](KEEPALIVE-PLAN.md) | The proposal that became `buddy wait` (D-033) |
| [ORCHESTRATION-PLAN.md](ORCHESTRATION-PLAN.md) | A task-orchestration proposal, not implemented as written |
| [ORCHESTRATION-REVIEW.md](ORCHESTRATION-REVIEW.md) | The design review of that proposal |

## For contributors and reviewers

| Document | What it is |
| --- | --- |
| [../CLAUDE.md](../CLAUDE.md) | Rules and invariants for agents working on this repo |
| [review-charter.md](review-charter.md) | The settled facts prepended to every automated code review (`scripts/codex-review.sh`) |
| [history-rewrite-2026-10-14.md](history-rewrite-2026-10-14.md) | What the 2026-10-14 history rewrite changed, and the old -> new table for a commit hash cited before it |
