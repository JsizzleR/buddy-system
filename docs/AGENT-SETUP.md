# Install the Buddy System — a recipe for an agent

You are a coding agent, and a user has pointed you here with words like
"install the buddy system for me". This page is written for you. Work through
it top to bottom. Every step has a checkpoint; do not go past a checkpoint that
fails. Every command on this page exists in this repository or in `buddy --help`.

**Do not, without the user saying yes to that specific step:** edit
`~/.claude/settings.json` or any other settings file; install packages (Go,
`ergo`, anything via a package manager); change a permission mode; use `sudo`;
push to any repository. The install script itself never edits settings, and
neither should you until step 3 asks.

## 0. Requirements

```sh
go version          # Go 1.26 or newer (go.mod pins the toolchain; `GOTOOLCHAIN=auto` fetches it)
git --version
```

If `go` is missing, stop and tell the user: installing it is their decision.
Developed and used on macOS. Linux runs the hermetic test suite in CI; the chat
daemon's supervision differs there and this page does not cover it.

## 1. Decide the mode, then clone and install

Claims-only unless the user asked for chat. The claims half (`buddy`: the
gate, the ledger, messages, the roster) needs no server. The chat half
(`buddylist`: an IRC concierge daemon, MCP tools, a launchd agent) is optional
and needs an IRC server the user runs; the README's "Presence" section has
that recipe when they want it. Keep the mode you pick through every later
step: the re-run in step 3 and the check in step 5 use the same command.

```sh
git clone https://github.com/JsizzleR/buddy-system
cd buddy-system
BUDDY_CHAT=off sh scripts/install.sh    # claims-only: no server of any kind
# sh scripts/install.sh                 # claims + chat, only if the user asked for chat
```

The script builds the binaries into `~/bin` (`BUDDY_BIN_DIR` overrides), runs
each once to prove it starts, installs the skill to `~/.claude/skills/buddy/`,
wires this checkout's own git hooks and ledger, and ends by REPORTING which
Claude Code hooks are wired in `~/.claude/settings.json`. It never edits that
file. Under `BUDDY_CHAT=off` it builds and checks only the claims half and
reports any chat remnants it found and left alone.

**Checkpoint.** The output ends in `install: done`, and:

```sh
"$HOME/bin/buddy" help | head -1       # buddy — the Buddy System: ...
ls ~/.claude/skills/buddy/SKILL.md
```

The report's `hooks:` line will say `NOT wired` on a first install. That is
expected; step 3 fixes it.

## 2. Put `~/bin` on the PATH

The hook lines in step 3 spell out `$HOME/bin/buddy`, so hooks work either
way. The agent inside a session runs `buddy claim …` from its shell, so the
user's shell needs `~/bin` on `PATH`:

```sh
command -v buddy || echo 'export PATH="$HOME/bin:$PATH"' >> ~/.zprofile
```

Ask before appending to a shell profile. **Checkpoint:** a new shell answers
`command -v buddy` with `$HOME/bin/buddy` (or the `BUDDY_BIN_DIR` path).

## 3. Wire the hooks — with the user's approval, as a merge

Show the user the "Hook wiring" JSON block from the README (the exact spelling
matters: the install script's report looks for those lines) and ask to add it
to `~/.claude/settings.json`. Then merge, never overwrite:

- If the file does not exist, write it with that block as its content.
- If it exists and has no `hooks` key, add the block's `hooks` object beside the
  existing keys.
- If it has `hooks`, append our entries to each event's array (`SessionStart`,
  `PreToolUse`, `PostToolUse`, `Stop`, `UserPromptSubmit`, `SessionEnd`),
  keeping every entry already there. Do not drop or reorder what the user has.
- Claims-only: leave out the two `buddylist` lines (`alert`, `presence --gone`)
  and add `"env": {"BUDDY_CHAT": "off"}` at the top level, so every session's
  SessionStart digest stays free of chat hints.

Show the user the diff before saving. **Checkpoint:** the file is still valid
JSON (`python3 -m json.tool ~/.claude/settings.json >/dev/null`), and
re-running the install command from step 1, in the same mode, now reports the
hooks as wired (the six `buddy` lines claims-only, `all eight` with chat).

## 4. Turn it on in each repository

The buddy-system checkout is already wired by step 1. For every OTHER
repository whose sessions should coordinate:

```sh
cd /path/to/the/repo
buddy init
```

This creates `buddy.db` in the repo's git common directory, shared by all of
its worktrees and invisible to git, and prints one line per main language
saying whether a language-server plugin is configured for it (advisory only).
A repository without a ledger is untouched: every hook is a silent no-op there.
Re-running `buddy init` is safe.

Optional, per repository: the commit-time gate, a symlink to
`.githooks/pre-commit`. The README's "commit gate" section has the three lines.

## 5. Verify in a fresh session

Hooks are read when a session starts, so the one you are in now does not have
them. Ask the user to open a new Claude Code session in a repository from step
4. Its context should begin with lines like:

```text
BUDDY: you are session <label> (<session id>). Claim before taking a bundle: buddy claim <slug> --desc ... --scope <path>
```

In that session:

```sh
buddy status                                                # the ledger's view of this session: claims, inbox, EXIT line
buddy claim demo --dry-run --desc x --scope README.md       # would claim: README.md / dry run: no conflicts; nothing was taken
```

`/buddy` loads the skill, which teaches how the verbs fit together. If
`buddy status` says the session `has not said hello in this repo's ledger`, the
SessionStart hook did not run: re-check step 3's spelling and that the session
was started after the file was saved.

## 6. Report

Tell the user, in a few lines: where the binaries are, that the skill is
installed, which hooks are wired (quote the install report's `hooks:` line),
which repositories were `buddy init`-ed, and whether chat was set up or
skipped. Point them at `buddy --help` and the README for everyday use.
