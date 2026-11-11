# When the deliverable is not code

Part of the `buddy-fleet` skill, moved out of its SKILL.md to be read when a
lane's deliverable is not code (D-171). A section named in parentheses is
SKILL.md's unless this file has it.

Research notes, a report, a runbook, configuration: a claim is a reservation
on a path, and nothing in the ledger asks what the path holds. Everything
above carries over, with four words translated (the lane's READY form is the
buddy skill's):

- **The directory must be a git repository; commits are optional.** Outside
  one, every verb answers "not a git repository", and the session has no row,
  no inbox and no brake. For a notes directory, run `git init` and then
  `buddy init` there, once (the operator's call for a directory they keep:
  it puts a `.git` in it). With no commit everything works except what needs
  one: `BASE` reads `(none recorded)`, and `buddy wait --ready HEAD` is
  refused.
- **Commit becomes a path and a version word.** The deliverable is its paths;
  the version is the lane's own word for which state of them to read ("draft
  2", "sections 1-4 final").
- **READY** (a chore or lite lane's: it parks, the path and version word in its
  message) is a wait ON the run with the path and version word in its note,
  `buddy wait --on <run> --until 4h --note "READY: <path> <version word>"`,
  so the run's outcome reaches the lane as LANDED. Until some rider declares
  at a commit, `buddy who <run>` lists waiters by name and age only: read the
  note with `buddy wait ls`, or in the lane's message.
- **The shared tier becomes the workflow's own gate:** a review pass over the
  assembled deliverable, a checklist script, a human read. With none, the run
  is "read and accept", and its outcome still says which.
- **Main becomes the directory the deliverables are integrated into.**
  `.buddy/slot/main` still serializes integration, and the integrator holds
  it from forming to landing.

The gate checks the paths a tool call names. A call that names none
(browsing, chat, an MCP tool) is allowed, so a deliverable written through an
MCP tool (a doc connector, a spreadsheet) is not gated: claim it anyway,
because the claim is the reservation peers read. The brake has the same edge:
`pause` stops only the tools the gate is wired to see, and never `WebFetch`
or `WebSearch`.
