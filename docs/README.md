# Documentation

Start with the [project README](../README.md): what the Buddy System is, how
to install it, and how to wire it into a repo. The rest, by what you need:

## Using it

| Document | What it is |
| --- | --- |
| [USAGE.md](USAGE.md) | Every feature in depth: hooks, claims, messages, the roster, waits, resource slots, long runs, authority files, the id register, the commit gate, chat internals |
| [../skills/buddy/SKILL.md](../skills/buddy/SKILL.md) | The skill installed for agents: the order to use the verbs in, written for a session rather than the operator |
| [DEMO-RUN.md](DEMO-RUN.md) | Ledger captures from the earlier two-Claude run, beat by beat, plus the final ledger snapshot from the README's Claude Code/Codex run (short task briefs, a prescribed scenario, the buddy commands the sessions' own) |
| [PICTURES.md](PICTURES.md) | How the README's pictures were made: what each is captured from, what is typeset, the edits to captured text, the takes, the versions and the oddities the pictures keep |

`buddy --help` and `buddy <verb> --help` are always current, and a test keeps
the skill in step with them.

## Why it is built this way

| Document | What it is |
| --- | --- |
| [DESIGN.md](DESIGN.md) | The rationale in one read, including the assumptions measurement refuted. Read this before proposing a change. |
| [decisions.md](decisions.md) | The append-only decision record, D-001 onward: for each rule, what was wrong, what shipped, what was cut and why. Long; search it for the D-number a page cites. |

## Work in progress

| Document | What it is |
| --- | --- |
| [CODEX-ORCHESTRATOR-PLAN.md](CODEX-ORCHESTRATOR-PLAN.md) | Codex orchestration work order and receipts: participation, adapters, isolated native mixed-fleet exercise and offline compatibility diagnostic implemented; real-provider and recovery qualification remain open |

## Working notes, kept as history

These are dated and are not descriptions of the current tool. Each carries a
status line saying what became of it.

| Document | What it is |
| --- | --- |
| [wishlist.md](wishlist.md) | Field notes from orchestrated multi-session runs: problem reports, most since answered by a decision |
| [KEEPALIVE-PLAN.md](KEEPALIVE-PLAN.md) | The proposal that became `buddy wait` (D-033) |
| [ORCHESTRATION-PLAN.md](ORCHESTRATION-PLAN.md) | A task-orchestration proposal, not implemented as written |
| [ORCHESTRATION-REVIEW.md](ORCHESTRATION-REVIEW.md) | The design review of that proposal |

## For contributors and reviewers

| Document | What it is |
| --- | --- |
| [../CLAUDE.md](../CLAUDE.md) | Rules and invariants for agents working on this repo |
| [review-charter.md](review-charter.md) | The settled facts prepended to every automated code review (`scripts/codex-review.sh`), so a review spends its budget on the change and not on re-deriving the project |
| [history-rewrite-2026-10-14.md](history-rewrite-2026-10-14.md) | What the 2026-10-14 history rewrite changed, and the old -> new table for a commit hash cited before it |
