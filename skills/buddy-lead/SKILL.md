---
name: buddy-lead
description: For a FIRST-TIME user of the Buddy System, or the agent helping one. Asks what they are trying to do before anything is installed (which agents, one repo or several, alone or a fleet, chat or not), leads them in short steps that each end in something they can see working, and offers a read-only check of the machine (`scripts/env-check.sh`) for the settings that help many parallel sessions. Use when someone is new to buddy, asks how to start or set it up, or asks whether their machine is ready. Not for a session already working an item (the `buddy` skill) or coordinating lanes (`buddy-fleet`).
---

# Leading a first-time user

You are helping someone use buddy for the first time. Everything you need
already exists: the README, `docs/AGENT-SETUP.md` (the install recipe, a
checkpoint per step, and what an agent must not do unasked), the `buddy`
skill (a session's side) and the `buddy-fleet` skill (a coordinator's). This
page is the order to ask in and where to send each answer; it repeats none
of them.

Three rules before anything else:

- **Ask one question at a time, and stop asking when the answers settle the
  rest.** Never guess the user's path; when two readings remain, show both
  and ask.
- **Nothing happens without the user's yes, one step at a time, the exact
  command shown first.** Installing, a settings file, a shell profile, a
  permission mode, `sudo`, launching a session: each is the user's own act,
  in their own shell. You run only what reads: `buddy status`, `buddy ls`,
  `buddy claim --dry-run`, `sh scripts/env-check.sh`.
- **Every step ends in something the user can see working.** A checkpoint
  that fails is where you stop and read the error together.

## The questions

1. **What are you trying to do?** (a) keep two or three of your own sessions
   from editing the same files; (b) run many sessions on one repo and land
   their work together; (c) look before deciding. All three take the steps
   below; (b) adds the last one.
2. **Which agents: Claude Code, Codex, or both?** Codex anywhere: before the
   exercise, read the `buddy` skill's "Harness boundaries" (every buddy call
   escalated under the sandbox, the native thread id, the patch TOOL) and
   `docs/USAGE.md` "Codex participation".
3. **Do you want the chat half?** An IRC room the sessions and the user can
   read. The default is no: claims only, which coordination never needs. Yes
   needs a server the user runs: README "Presence".
4. **Which repositories?** Each gets `buddy init` (step 2).
5. **A fleet only:** how lanes open (new terminal tabs, background sessions,
   by hand), how many at once, which permission mode and model, at what
   context size a session hands off, how deep a review each item owes. These
   are the user's standing decisions, recorded in THEIR instructions (their
   user-level CLAUDE.md), never in this skill: copy
   `docs/examples/fleet-conventions.md` and fill its angle brackets with
   their answers. The handoff size also goes in their settings' `env` block
   as `BUDDY_HANDOFF_AT`; the install report's `handoff:` line says whether
   it is set.
6. **Is there a review step** (a second model, a script)? It belongs to each
   repository, not the user: that repo's own CLAUDE.md names it (the example
   file's notes say so).
7. **May I check this machine?** Read-only; the last section.

## The steps

1. **Install.** Follow `docs/AGENT-SETUP.md` steps 0 to 3 (requirements,
   claims-only or chat, the install script, PATH, hooks as a merge the user
   approves). Checkpoint: the report ends `install: done`; once the hooks are
   wired its `hooks:` line reads all wired and `gate:` names README's
   fail-closed entry. Quote both lines back.
2. **Turn it on in each repository:** `buddy init`. Checkpoint: its
   `language server:` lines; a `fix:` line there is the user's to apply.
3. **A fresh session** (hooks load at start). Its first lines include
   `BUDDY: you are session …`, and `buddy status` shows its row. Checkpoint:
   `buddy claim demo --dry-run --desc x --scope README.md` answers
   `dry run: no conflicts`. If `buddy status` says the session has not said
   hello, the hooks were not read: AGENT-SETUP step 5. A Codex session has
   no start hook and runs `buddy hello` itself first (USAGE "Codex
   participation").
4. **The exercise: one claim granted, one refused.** In that session,
   `buddy claim demo --desc "trying buddy" --scope README.md`. In a second
   session on the same repo, the same claim: refused, exit 4, naming the
   holder. Then `buddy whose README.md`, `buddy msg demo "done?"` (read its
   result line), `buddy release demo` in the first; the second session's
   claim is granted, and it releases too. `buddy sessions` shows both rows;
   `buddy who <label>` is everything the ledger holds about one. The user has
   now seen the whole mechanism; `/buddy` is what their sessions load from
   here.
5. **Upgrades.** After every pull, `sh scripts/install.sh` again, or `buddy
   update` from anywhere: both keep the mode the last install recorded
   (claims-only stays claims-only; `BUDDY_CHAT=on` or `=off` on one run
   switches it), and say it on their first line. README "Install in two
   minutes" says why a stale binary denies every write. Chat installed by
   accident: `buddy uninstall --chat --dry-run` shows what goes, then without
   `--dry-run` it goes and buddy stays claims-only; `buddy uninstall` removes
   everything (README "Uninstall").
6. **A fleet** (answer (b)). The coordinator loads `/buddy-fleet` and reads
   the user's standing decisions from their instructions (question 5); its
   first fleet is a scratch item on as few lanes as their cap allows,
   watched. Where a decision is missing, that skill asks; nothing fills the
   gap from this one.

## The machine, read-only

With the yes to question 7, from the buddy-system checkout:
`sh scripts/env-check.sh` (with --list it only names the checks and why
each exists).
One line per check, `PASS`, `WARN`, `FAIL` or `CANNOT-ASSESS`, and under
anything not PASS one `fix:` line naming the command, whether it needs sudo
and whether it is reversible. It changes nothing, runs no sudo, exits
non-zero only on a FAIL, and does not repeat what the install report
(hooks, gate, chat, handoff size), `buddy init` (language servers) and
`buddy diagnose` already say. Read it with the user: a WARN is advice, and
CANNOT-ASSESS means the script's own read-only command could not tell, not
that all is well. A fix the user wants is theirs to apply, one at a time:
show the line, wait for the yes, let them run it, re-run the check and show
the line that changed. A fix marked sudo or irreversible is never typed by
you.
