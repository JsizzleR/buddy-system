# model-router reference

Read on demand: when setting up, when a `route-cap` line is refused, or when reading a table.
`SKILL.md` beside this file says what a session does with a pick.

## Requirements and install

Python 3.8 or later, standard library only, on macOS or Linux; Windows is not supported. Copy
this directory into `~/.claude/skills/`. Nothing else is installed or configured.

## Verbs and exit codes

    python3 -I router.py scan [--claude <dir>] [--codex <dir>] [--out <file>] [--cap <file>] [--cwd-under <dir>]
    python3 -I router.py cap-template [--table <file>]
    python3 -I router.py pick --cap <file> --role main|sub (--class <c> [--touch <path>]... | --all) [--tier chore|lite|full|none]
    python3 -I router.py --self-test

- 0: a table written, a draft printed, or a `pick=` line.
- 1: a `no pick: <reason>` line, a `cap-template` with no named id to draft from, or a
  self-test failure.
- 2: a refusal: a usage error, an unreadable source, a source or table named inside a work tree,
  a write target that is inside a work tree, unwritable or not a table, or a refused table. No
  table is written. A scan refused over its sources writes `refused.txt` and removes the earlier
  table at its target, so nothing drafts from a stale one.
- 3: an internal error, printed as its exception type only.

A refused write target prints its remedy: set `XDG_STATE_HOME` to an absolute directory outside
any work tree (a home directory kept in git is one).

A refusal never quotes what was passed: model-written input does not reach the print.

## Sources

Each is optional, and each is detected. A source that is absent is OFF and says so
(`claude=absent`). One that exists but cannot be read whole refuses the scan. That covers a
permission error, a file gone mid-read, or any malformed line before a file's last. The
refused paths are written to `refused.txt` in the state directory (mode 0600), one per line,
every byte outside printable ASCII (and `%`) written as `%XX`, so a newline in a file's name
cannot start a second line. The print names that file, never a path in it: repair or move the
files it lists, then scan again. A torn LAST line is skipped and counted (`torn_last=`), because
a live session is still writing it. Absent means nothing is there: a permission error on the way
is unreadable, never absent.

- Claude Code: `<config>/projects/<project>/<session>.jsonl`, and its subagents under
  `<session>/subagents/`. `<config>` is the scan's claude flag, else `CLAUDE_CONFIG_DIR`, else
  `~/.claude`.
- Codex: `<codex>/sessions/YYYY/MM/DD/*.jsonl`. `<codex>` is the codex flag, else `CODEX_HOME`,
  else `~/.codex`. Codex model ids fold to `(other)`: a pick across harnesses is not made.

A directory named by either flag inside a work tree, or reached through one, is refused (the
cap's rule, below): a repository's planted transcripts are not the user's history.

**What is read.** Only these:
- structured enum and number fields: model, effort, stop reason, turn origin, the permission
  decision, the denial kind, versions, timestamps, token counts, exit codes, each tool
  result's error flag, and the `isMeta` and `isSidechain` flags;
- tool names;
- tool INPUTS (a path, a command), reduced in memory to a label from a fixed table;
- the cwd, for scoping and for the protected-path rule;
- session ids, record uuids, tool-use ids and a subagent's id, to join and deduplicate. They
  are held as salted in-memory hashes and never printed.

**What is never read:** message text, a tool's result, or a prompt. A value outside a fixed
list prints as `(other)`.

A record counts once by uuid. Two copies are one only when every raw field read is equal. A
copy that differs is a conflict (`conflicts=`), and every task holding any copy of it leaves
every cell (`conflict_tasks=`).

## The task and its class

- **A main task** is one human prompt to the next. Task notifications, peer messages and
  scheduled wakes continue it.
- **A subagent task** is one subagent transcript.
- **A Codex task** is one rollout.

The class comes from the task's tool mix, and the first rule that matches decides:

1. No tool calls, or at most 2 with no edit: **qa**.
2. Edits, counted by extension. Mostly code (any language, Makefile, Dockerfile): **code**.
   Mostly data: **data**. Mostly prose: **writing**. Only config: **ops**.
3. No edits, by share of calls, in this order:
   - Agent 30%: **delegate**;
   - web or browser 30%: **research**;
   - data programs or data reads 30%: **data**;
   - vcs, ops, package, network or build programs 40%: **ops**;
   - reads and searches 40%: **research**;
   - a test run: **code**.
4. Otherwise **unknown**, counted and never routed.

Under a cap, a task with no edits on a `review=` model is **review**. The class measures the
activity, not the intent: a code task that mostly edits its docs reads as writing.

## The cap line

One line, at column 0, in a file the user's own instructions name. Its bytes are printable
ASCII only, at most 1,024 of them, with keys separated by single spaces.

    route-cap [roles=main] default=<id>[@<effort>] [assign=<class>:<id>[@<effort>][,...]] [review=<id>[,...]] [tiers=<tier>[,...]] [protect=<prefix>[,...]] [trial=... classes=... floor=<n> events=<n> worse=<n>x starts=<date> ends=<date>] decided=<date>

- **id**: 1 to 64 bytes of `a-z 0-9 . _ : @ -`, starting with a letter or digit, containing
  `claude` and a digit, with no `latest` component, and with a digit in every part after an
  `@`. A family alias, a family name and other harnesses' ids fail, and so does a misspelt or
  unlisted effort left in the id (`@ultra`). Cloud ids such as `<name>@<date>` pass whole.
- **@effort**: `low`, `medium`, `high`, `xhigh` or `max`, split at the last `@` only.
- **class**: code, writing, research, data, ops, qa. `docs` reads as writing and `test` as
  code.
- **roles**: `main`, its default. A subagent cannot be pinned to an exact id, so a line naming
  `sub` is refused.
- **review**: the models kept for reviews. A pick never lands on one: an id is compared by its
  part before any `@`, so `review=<id>` also excludes `<id>@<date>`.
- **tiers**: `chore` and/or `lite`, the review tiers a non-default pick may carry. A `full`
  tier never leaves the default.
- **protect**: path prefixes added to the shipped list (below); it never removes one.
- **trial**: the trial group parses, but trials are not built in this version. A trial class
  picks the default (`why=trial-not-built`).
- **decided**: a real `YYYY-MM-DD`. A blank `decided=` (the draft) is no cap.

The cap's identity is the sha256 of the line's own bytes, so an edit elsewhere in the file
does not change it.

**Each of these means no cap,** and the print names the code in brackets:
- `duplicate-key`, `unknown-key`, `not-key-value`, `empty-token` (a double or trailing
  space), `bad-byte`, `line-too-long`, `bad-keyword`;
- `no-default`, `no-decided`, `undated`, `bad-date`;
- `bad-id`, `review-effort`, `bad-roles`, `roles-sub`;
- `bad-class`, `bad-assign`, `class-assigned-twice`, `empty-element`, `repeated-element`;
- `review-id-routed` (a review id in `default=`, `assign=` or `trial=`);
- `bad-tier` (`full`, `none` or another word in `tiers=`), `bad-protect`;
- `trial-incomplete`, `number-out-of-range` (1 to 1000; `worse=` ends in `x`),
  `trial-window` (`starts=` after `ends=`, or more than 14 days before it), `expired` (today
  after `ends=`), `trial-is-default`, `effort-trial-needs-both` (the same id on both sides
  needs `@<effort>` on both), `class-assigned-and-trial`.

The file is refused as a whole when:
- it is missing, is not a regular file, or is over 1 MiB;
- it has no cap line, or two. A cap line is any line starting `route-cap` (after an optional
  byte-order mark) not followed by a letter, digit, `_` or `-`, so `route-cap` followed by a tab
  or a no-break space counts, and refuses as `bad-byte`;
- it is inside a work tree of any version-control system (git, hg, svn, jj, bzr, darcs,
  fossil) at any directory reaching it passes through, every link followed a step at a time,
  so a cloned project's line, or a link a project ships, is never the user's. Keep the cap in a
  file outside any work tree;
- it is owned by another user, or any user can write it (`cap-not-owned`,
  `cap-world-writable`; a group-writable file is accepted).

## The pick

The first rule that matches answers:

1. `no pick`: for a subagent (role sub), with no cap file, or with a cap that is refused.
2. `no pick: review` for class review.
3. One eligibility check. Its first failing clause gives the default, with `why=`:
   - `not-assigned`, `trial-not-built`;
   - `full-tier`;
   - `no-tier`: the tier was omitted. Every non-default pick needs one, `none` for a user
     without tiers;
   - `tier-not-listed`: a line with `tiers=` and a tier it does not list (`none` included),
     or a line without `tiers=` and a tier other than `none`;
   - `protected`: a touched path is protected;
   - `no-touch`: an editing class (code, writing, data, ops) named with no paths.

   On a line with `tiers=`, after `tier-not-listed`, these instead, each judged over all the
   touches at once: `no-touch` for any class; `bad-touch`, a path it cannot judge;
   `protected`, the literal list too; `not-prose`, a path outside the allow-list (below).
4. `pick=<id>[@<effort>] by=assign`.

**Protected paths** are matched on every path component: a relative path as given, and every
path at its absolute form and at its resolved target. Inside a work tree, each is matched
relative to EVERY enclosing root (a submodule's and its repository's), and the shipped names
(not the substrings) are matched on the directories above the outermost root, unless it is a
linked worktree (its `.git` file names a git directory holding `commondir`, as git decides),
which is a checkout wherever it lives. Outside any tree, the absolute path is
matched. A blank touch, one ending in `/`, an existing directory, or a relative one with no cwd
is protected outright. A name is folded as invariant 13 says, and also by NFKC with casefold,
which can only protect more. The shipped list:
- `CLAUDE.md`, `AGENTS.md`;
- `.claude/`, `skills/`, `.githooks/`, `.git/`, `.github/workflows/`;
- any component containing auth, crypto, secret, security, permission, sandbox, credential,
  password or token.

A `protect=` prefix matches any component suffix, of the absolute path too: `internal` covers
`/x/repo/internal/a.go`.

**On a line with `tiers=`** every class needs at least one touch, and every touch must be a
prose file outside a second, literal list, or the pick is the default. A tiered workflow
claims its paths relative to a repository root, and this is its report's list, matched there:
- `cmd/`, `internal/`, `scripts/`, `.githooks/`, `.claude/`, `.github/`, `go.mod`, `go.sum`;
- `CLAUDE.md`, `AGENTS.md`, `skills/`;
- every path starting `docs/review-charter`, `docs/decisions.md`, `docs/prose-budget.tsv`.

A touch overlaps a row in either direction: `docs` overlaps `docs/decisions.md`. A prose file
ends in `.md`, `.markdown`, `.rst`, `.adoc` or `.org`, has no component starting with `.`
(`..` included), and its name does not start `claude` or `agents` (`CLAUDE.local.md`). Every
reading must pass: the touch as given, and its lexical and resolved paths relative to every
enclosing root (or the whole path when no root encloses it), each read from every component
on: `b/docs/decisions.md` is protected, and so is `docs/cmd/a.md`. A linked worktree stops the
climb only at `.claude/worktrees/<name>` or with no repository above it. The cap file itself
is protected. `bad-touch` is a touch that is not one path of 1 to 512 bytes of `A-Za-z0-9._/-`
(a list, a glob, a `$VAR`, a space, a backslash, a `~`, a non-ASCII name), a path the system
cannot resolve (a symlink loop, a directory it may not search, a component that is a file), or
a file with a second hard link; an empty touch value, or one starting with two dashes, is a
usage refusal (exit 2). A path not made yet is judged by its name, so a task can name the file it will write.

## The table

The table lives at `${XDG_STATE_HOME:-~/.local/state}/model-router/table.tsv`, mode 0600,
unless the scan's out flag names another file. Every file the script writes is refused inside
a work tree, before anything is read, judged at its directory resolved; so is an existing
target that is not a regular file or that another user owns, an existing table target that does
not start with the table's magic, and a directory that cannot be written. A write goes to a
temporary file, is synced, and is renamed into place.

Its lines, in order:
1. A magic line, `#model-router-table` and the format version.
2. One `#header` line of key=value fields. They say when and over what window the table was
   made, each source's state and file count, `torn_last`, `conflicts`, `conflict_tasks`,
   whether it was scoped, the cap's short sha256 (or `none`), the count of subagent aliases
   that resolved to more than one id, the count of Agent calls that named no model, and the
   script's short sha256.
3. The column names, then the rows.
4. A trailer, `#end`, holding the row count and the sha256 of every byte above it.

A row is `role class tier model eligible`, then `tasks last7d calls out_ktok tested`, then each
signal as `fired/covered`, or `-` where that role has no such field:
- `tier` is `-` in this version;
- `eligible` is `no` for a task that edited a protected path;
- `model` is `<id>@<effort>`. An id is named only when the cap names it; before a cap exists,
  when it led 10 or more of the main and subagent tasks the table keeps, and only the 8 most
  used. Every other id is `(other)`. Anyone who can write the transcript store can plant enough
  tasks to be named, and to be the draft's `default=`: the draft is data, undated until the
  user dates it.

A table with no trailer, a count or hash that does not match, an unknown version, a value
outside the vocabulary, two rows with one key, a size over 64 MiB, a Codex row naming an id, or
(made before any cap) an id named under the floor or past 8 of them is refused when it is read.

## The draft

`cap-template` reads the table (validated, never re-scanned; one named inside a work tree is
refused), applies the naming rule again (a hand-written table can name any id), and prints:
- a draft line: `default=` is the main model and effort of the most main tasks, and
  `decided=` is blank, so it parses as no cap;
- a `# assign= candidates` line: for each class where another main model led 10 or more tasks,
  the most-used one. They stay out of the line, so a draft dated unedited routes nothing off
  the default. Move in only the routes you choose;
- one `#` line per candidate with its counts, marked confounded: chosen per task;
- the ids the table names, for `review=`.

## Signals, printed only

The signals are `interrupt`, `user_reject`, `policy_denied`, `test_red_end` (the last test run
failed), `test_masked` (a test piped into a filter, which reports the filter's status) and
`abandoned` (the last request stopped on a tool call). None of them chooses a model.

**How they mislead.**
- An interrupt needs a human watching.
- A rejection needs a permission prompt.
- A policy denial measures the hooks, not the model.
- Agents run until their tests pass.
- A task that does less fails less, so `calls` and `tested` sit beside every rate.

`covered` counts the tasks whose every record falls inside the field's shipped harness
version range:

| field | range |
|---|---|
| Claude Code `interruptedMessageId` | 2.1.292 to 2.1.322 |
| `permissionDecision`, `toolDenialKind` | 2.1.296 to 2.1.322 |
| Codex `turn_aborted` | 0.144.3 to 0.166.0 |

A newer harness reads as not covered until the ranges ship again.

## Not in this version

- trials and their counts file;
- `report`;
- a subagent pick;
- a Codex pick;
- reading buddy's export.

The `route-cap` grammar is shared with those later phases, so a line written now keeps its
meaning.
