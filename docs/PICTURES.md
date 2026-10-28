# How the README's pictures were made

The README's captions say, in two to four sentences each, what a picture is
drawn from, what is typeset, and the one thing the picture needs the reader
to know. This page holds the rest: the provenance of each picture in full,
the takes, the versions, what the operator did by hand, and the oddities a
harness showed that the pictures keep rather than hide. Nothing here is a
description of the current tool; `docs/USAGE.md` is that.

Every picture is a hand-authored SVG, not a recording. The animated ones
reveal captured lines on SMIL begin times and hold a still final frame under
`prefers-reduced-motion`; no screen was captured, no frame recorded, no OCR
run. Each terminal picture was built by a generator script from a capture
(pane text, or a transcript) and a hand-written story naming which captured
lines to show and when; the script wrapped and trimmed them, and the lane
that made the picture checked it rendered in a headless browser before it
shipped. The repository carries only the SVGs, which are plain text and can
be edited by hand. The generators, the stories and the pane captures lived
in each lane's session scratch directory, which is temporary and not kept,
so a picture is not re-renderable from the repository; what survives of each
run, and where, is said under its picture. Before each picture landed, the
repository's private-terms scan and a grep of the asset checked that no
account name, host name or scratch path is in it. The register every caption
keeps: every output line is a captured line; prompts, tool-call lines and
`#` narration are typeset; wrapping, trimming and omissions are disclosed;
scratch paths are shortened to `…`; otherwise no word in a captured line
changes.

## The Claude Code session and the Codex session side by side (`docs/assets/demo.svg`)

**What was captured.** Two real sessions in a scratch clone of this
repository with its own ledger, 2026-10-26, under a throwaway HOME whose
settings wire buddy's hooks and nothing else:

- `lane-claude`: Claude Code 2.1.310, Opus 5.5, auto mode, `/buddy` on its
  first prompt; opened with `herdr tab create` and `herdr agent start`.
- `lane-codex`: codex-cli 0.164.0, GPT-6.1-Sol, the workspace-write sandbox,
  on-request approvals, a throwaway Codex config directory holding a
  `hooks.json` that wires only the gate (`BUDDY_HARNESS=codex buddy gate` on
  `^(apply_patch|Bash)$`) under `[features] hooks = true`, trusted once by
  the operator in the session's `/hooks` review.

Each got a short task brief with a prescribed coordination scenario: append
a line to the same file, `internal/store/store.go`. The Claude session was
told to append with the Edit tool, hold the file until a peer asked for it,
hand it over, say so and stop, checking its mail every 20 s in the
foreground; the Codex session, told a peer was working in `internal/store`,
to append another line and work the way `AGENTS.md` says. Which buddy verbs
to use was in neither brief: that was each session's own choice. The
`env -u CLAUDE_CODE_SESSION_ID` in front of every buddy call in the Codex
pane is what `AGENTS.md` tells a Codex worker to do (D-085).

**How.** The pane output is transcribed from the text each session's user
saw, read back with `herdr pane read` at each beat and after the run, the
harnesses' collapsed summaries ("Ran 3 shell commands", "+ 2058 lines")
included. The two transcripts (the Claude Code JSONL and the Codex rollout
JSONL) were used to check the timestamps and that nothing was reworded,
never as the drawn text. The transcripts' clocks sort the two panes' beats
into one timeline; they also split one Codex cell, the blocking wait (its
call and `BLOCKING` line at the call's time, its `LANDED` lines when the
chunk carrying them arrived), and place the `# …` marker for the skill read
after that cell, where the pane lists the read before it. The Claude session
worked 1m 27s, the Codex session 58 s.

**What is typeset.** The pane titles and the `#` lines, that is the one `⏩`
line (the real time between the beats around it) and the `# …` lines, which
mark cells not drawn: Codex's reads of `store.go` and of the skill, and its
`wait clear`, `inbox` and `status` wind-down.

**Edits to captured text.** The scratch directory is written `…`; long lines
are soft-wrapped at 58 columns with continuations indented as the panes
indent them; a `…` ending a Codex line is Codex's own trim at its pane
width. No word changes. The panes are drawn as two 30-row windows that
scroll, 960 px wide, on a 36-second loop.

**The operator's interactions.** One escalation prompt in the Codex pane,
answered "always run" for `env -u CLAUDE_CODE_SESSION_ID buddy …`; the
one-time hook trust above; and the copy of Codex's credentials into the
throwaway config directory, the operator's act and never a lane's. An "LSP plugin
recommendation" dialog overlaid the Claude pane mid-run in both full takes;
it did not block, and it is gone from the final pane text.

**The takes, none edited.** A dry run of the Claude side alone
(it appended with a shell `printf >>` instead of Edit and said so; the task
wording then gained "with the Edit tool"). Take 1, both sessions, under the
deprecated `codex_hooks` feature key: the same shape as the shipped take,
not shipped, because nothing Codex 0.164.0 writes records an allowed hook
run, so an allowed patch could not be told from a skipped hook. A separate
`codex exec` probe under the same `hooks.json`, with a shell-registered
holder on the file, in which the gate denied Codex's inner `apply_patch`
and the file was unchanged: the evidence that the hook runs under this
configuration. Take 2, the shipped one. What survives: the sessions'
transcripts, where the harnesses wrote them on the machine the run was made
on (Claude Code's JSONL under the throwaway HOME, the Codex rollout JSONL
under the throwaway Codex config directory); the pane reads at each beat,
the final pane reads, the ledger read back and the generator were in the
lane's session scratch directory, and the ledger read back is the part
carried into [DEMO-RUN.md](DEMO-RUN.md), section 7.

**What the picture does not show, and why.** The gate's refusal: both
sessions claimed before editing, so the gate that denies an edit inside a
peer's claim never had one to refuse. The refusal shown is the ledger's
refusal of the second claim.

**Known oddity.** At the end of the run the Claude pane's input line held
unsubmitted text, `buddy who lane-codex`: typed by neither session, absent
from both transcripts and from the throwaway HOME's prompt history, present
from the first pane read after both said DONE, not drawn; who typed it is
not recorded.

The versions, the hook probe's text, and this run's ledger read back
(roster, claims, `whose`, `git diff`) are in
[DEMO-RUN.md](DEMO-RUN.md), section 7; sections 1-6 there are an earlier,
scripted run of a similar choreography, read from the ledger beat by beat.

## The session running a fleet (`docs/assets/fleet.svg`)

**What was captured.** One Claude Code session (Opus, auto mode), the
coordinator, following a brief of 21 numbered steps in a run of the
[skill](../skills/buddy/SKILL.md)'s "Running a fleet" recipe (now in the
[fleet skill](../skills/buddy-fleet/SKILL.md)), 2026-10-26, in
a scratch clone with its own ledger under a throwaway home directory whose
settings wire buddy's hooks and nothing else. The two lanes it opened
(Sonnet, auto mode) followed the numbered briefs of the scripted run in
[DEMO-RUN.md](DEMO-RUN.md), sections 1-6.

**Source.** Every output line is the coordinator session's own, read from
its transcript: the `SessionStart` output of the `buddy hello` hook (no
other hook printed; the generator refuses a capture in which any other hook
did), each tool call's result as the harness recorded it (so "Bash completed
with no output" is the harness's wording, not buddy's), and the session's
own words, shown as the pane renders them.

**What is typeset.** The typed prompt, the `⏺` tool-call lines (those calls,
written as Claude Code renders them), the `#` narration, and each `⏩` line,
which is the real time between the call above it and its result below, read
from the transcript's timestamps, with shorter gaps unmarked; the four drawn
are 32 s, 32 s, 42 s and 22 s.

**Edits to captured text.** `…` stands for the scratch directory holding the
briefs and the clone; long lines are soft-wrapped at 112 columns or trimmed
at a word with an ellipsis; a `# …` row marks lines cut from a result (the
`git diff` keeps its first line and the two added lines; `buddy who` keeps
its first line, `CLAIMS HELD` and `EXIT`). Nothing else in a captured line
is changed. The pane is drawn as a 30-row window that scrolls.

**What the harness showed.** `herdr agent start` answers with a timeout: its
30-second wait to detect the agent ran out while the lane was already
running. `herdr agent wait` answers with the state it read from the lane's
pane: `done` for lane-a, `blocked` for lane-b, in the second lane-b's own
transcript ends with DONE and after it had released its claim (the
`buddy ls --all` and `buddy who lane-b` results that follow show that).
Neither is retouched.

**The takes.** Take 1, a rehearsal under the real HOME, was the first
version of the picture and is not the shipped source. Take 2, under the
throwaway HOME, aborted at step 5: each lane's first Read of its brief hit
Claude Code 2.1.310's "Read outside the working directories" dialog, a
one-time notice per HOME, which the operator answered once. Take 3, with
nothing in the briefs or settings changed, is the shipped picture. What
survives: the three sessions' transcripts under the throwaway HOME (take 1's
under the real one), on the machine the run was made on; the extracted
events and the generator were in the lane's session scratch directory.

## The architecture drawing (`docs/assets/architecture.svg`)

A static drawing, hand-authored on 2026-10-25 (the date of the commit that
added it) in the register of the terminal pictures
(same palette, same monospace stack), with its own dark background so it
reads on a light or a dark page, no external fonts or scripts, name-free.
It replaced the README's ASCII "How it holds together" block on the
operator's direction (cut more, and replace prose with a drawing). Its generator has a width guard: a line that would overflow its
box is cut to fit, never shrunk.

The caption's two rules, in full: *chat is the view, never the lock*
(coordination and control live in the transactional local ledger, and a
chat message is never authoritative) and *claims work with chat entirely
absent* (safety hooks fail closed, so a ledger that exists but cannot be
read denies; chat hooks fail silent). Not a daemon: the safety half is a CLI
spawned per hook call over a SQLite file in the repo's git common directory;
nothing listens, nothing has to be up, and a machine with chat absent loses
visibility, never safety. The reasoning behind each box, including the
assumptions measurement refuted, is in [DESIGN.md](DESIGN.md).

## The install (`docs/assets/install.svg`)

**What was captured.** Real output, 2026-10-25, of a claims-only install
(`BUDDY_CHAT=off`) run by a script against the throwaway HOME, reset first.
The install script's own staging knobs, listed in its header, point every
write there (the binaries, the skill, the settings file); HOME itself is that
directory; and git's global config is the one file in it that maps the GitHub
URL onto a local mirror of `main`, so the clone is spelled as the README
spells it and came from disk. The Go caches stayed the real ones, so the
build was not a module download: the two minutes are the script's, not the
download's. Then, in a second project under the same HOME: `buddy init`, the
digest a session there gets, a first claim and `buddy status`. No session
ran: the `SessionStart` hook was fed the JSON payload Claude Code sends, by
hand.

**What is typeset.** The typed commands (after `$`) and the `#` lines.

**Edits to captured text.** The HOME's path is written `~`, as the shell
prompt would. Long lines are soft-wrapped at 58 columns or trimmed with an
ellipsis. The settings file is cut where `# …` says so (its 18 lines are the
README's hook-wiring block, verbatim), and `buddy status` is cut where `# …`
names the eight lines elided. The output is preserved except for that
substitution, the wrapping and trimming, and those marked omissions. Two
panes because the story has two acts, and because one 100-column pane ran
to about 900 px.
