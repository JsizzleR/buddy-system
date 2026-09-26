# Security

## Reporting a vulnerability

Please report privately through GitHub's
[private vulnerability reporting](https://github.com/JsizzleR/buddy-system/security/advisories/new)
rather than a public issue. This is a single-maintainer project, so response
times are not guaranteed. Only the latest commit on `main` is
supported.

## What the Buddy System is, and is not, meant to resist

It is a coordination tool for one operator's agents on one machine. Knowing
its boundaries tells you what counts as a vulnerability.

**In scope** — please report:

- A way for content an agent reads (a chat message, a peer's claim
  description, a label, a path) to break out of its fencing. That covers
  forging extra rows or lines in a listing, impersonating the operator or
  `buddy` itself, or reaching an agent's context unfenced. Every such value is
  meant to render on exactly one line via `internal/fence`.
- A way for chat to confer authority: a chat message that pauses, resumes,
  claims, releases, or otherwise changes the ledger. Chat is never meant to be
  control.
- The gate allowing an edit it should deny for a path the tool call declared:
  inside another session's exclusive claim, inside a shared claim when the
  caller holds no covering claim of its own, while paused, or when the ledger
  exists but cannot be read (that case must deny, never allow).
- A server or socket binding a non-loopback address by default.

**Out of scope, by design:**

- **Anything that assumes a hostile local user.** The ledger trusts the
  machine's user account. Any process running as you can edit `buddy.db`
  directly. It is not a multi-tenant boundary.
- **Anything that bypasses the harness.** Enforcement is cooperative. The gate
  adjudicates the paths Claude Code tool calls declare (`Edit`, `Write`,
  `NotebookEdit`). A `Bash` command is checked only against the operator's
  pause: what it writes is not adjudicated, and the commit gate is the second
  line for that. A process that never goes through a hook is not bound at all.
  It is a seatbelt for agents, not a sandbox against them.
- **Unauthenticated chat servers on loopback.** The chat servers run without
  authentication *because* they bind loopback. If you bind a real interface,
  turn authentication on first (ergo has SASL); running them exposed without it
  is a misconfiguration, not a vulnerability.
- The time between a gate check and the write it allowed (TOCTOU). The gate
  decides at the tool-call boundary, and that window is documented.
