# Buddy on a project that is not git-tracked

Status: PLAN, nothing built (2026-11-14). Written by the `nongit-plan` lane on
Fable 5.2 at high effort, from the operator's request below, the code at main
c41826a, and one fixture script run the same day against throwaway ledgers
(section 10). One Codex pass ran over the finished draft (verdict: revise, 12
findings); each is in section 9 and folded where that section says, once. It
starts from `docs/BEYOND-CODE-PLAN.md` and re-measures nothing that plan
settled.

Register: each part names the failure it answers, the number it rests on and
how to re-run it, the alternative it refuses and what it costs. A number not
taken says NOT MEASURED. What only the operator can decide is in section 8.

## The request

Relayed by the orchestrator, each marked as said in its pane: 2026-11-14 about
06:32Z, "open a lane to plan out how we can use the buddy system/support using
it on a project that isnt git tracked/operator doesnt want git tracking"; about
06:33Z, "the use case btw, is mostly then orchestrating multiple sessions from
one pane". So the weight is on one coordinator and several lanes working in ONE
directory that has no git history and is to get none: claims, waits, READY,
messages, landing and handoff there. The other uses are secondary.

## 1. The straight answer

**It works today, with no buddy code change, if the directory may hold one
small `.git` FILE** (one line, 146 bytes in the fixture) that points at a git
directory kept somewhere else. Nothing is tracked, git is told to ignore
everything, a hook refuses every commit, and the ledger sits outside the
directory. Measured end to end (H0 to H2, section 2): the gate refuses a second lane's write into a claimed file, a
lane parks its item in a run, another declares READY with a path and a version
word, the coordinator releases the run with an outcome and the lane reads
LANDED. No commit exists at any point, and the directory gains no other entry.

**It also works today with nothing added to the directory**, where the
directory can sit inside a dedicated parent that holds the `.git` (F3), at the
price of a scope trap (G1).

**With no git-named entry anywhere it does not work, and that needs code.**
Every verb answers "not a git repository" and the gate allows silently (F0; the
settled feature-off verdict of invariant 3). The code is a second way for the
hooks to find a ledger: BEYOND-CODE-PLAN's option C, changed here in one
respect that matters (the ledger must not live inside the directory, 3.2).

**Does this request meet option C's triggers?** C was cut with two conditions:
"`git init` is one command, the fixture shows it coordinates, and 2 live
sessions are the whole measured demand", and "**OPEN** for the operator: are
there directories you will not `git init`? If yes, C is the next plan." The
request answers the second as worded: there is such a directory, so the next
plan is owed, and this is it. BEYOND-CODE set no further bar for building C.
The bar below is this plan's own recommendation: do not build yet, because
"not git tracked" has two readings that the request does not separate:
no history kept (met today by the pointer file, with zero commits possible),
or no git-named entry in the directory at all (met today only by the parent
layout, else by code). Section 8, question 1, is that choice. Demand was not
re-counted: the figure stays BEYOND-CODE's of 2026-10-26 (8 non-repository
project directories, 24 transcripts, one directory holding 15), plus this one
request.

## 2. What was measured

All on 2026-11-14, macOS 27.2.1, APFS, git 2.54.0, by the one script in
section 10, run in an empty directory under the lane's scratchpad with the
installed `~/bin/buddy`. That binary was rebuilt by a landing while the lane
worked: the table is the script's last run, at revision 9658d75 (`go version
-m`, unmodified); the lane's earlier runs of the same fixtures, at c41826a,
gave the same verdict in every row they shared. Between the two,
`internal/cli` differs by 14 lines in `hello`'s subagent arm (`git diff
--stat c41826a 9658d75`). Throwaway ledgers, three minted session ids (O, A,
B) registered by hand (`hello --session <id> --label <l> </dev/null`), hook
payloads piped to `buddy gate` and `buddy beat`. Nothing was registered in a
real ledger and nothing was installed by the lane.

ALLOW is `buddy gate` exiting 0 with no output. DENY is exit 0 with a JSON
document whose `permissionDecision` is `deny`; the script parses it and
prints anything else raw, and no row did. Every gate DENY has an ALLOW beside
it, and every refused command a control that succeeds with the blocker absent
(K0, or in its own row). A size marked "here" depends on the fixture's own
path or labels (the `.git` file, a DENY's bytes) and is this run's only.

| # | Fixture | Result |
| --- | --- | --- |
| F0 | plain directory, no git | `status`: "not a git repository"; B's write ALLOW |
| F1 | `git init` in the directory (the settled path, and the control) | A claims `notes/plan.md`; B's write there DENY (495 bytes here), B's write to `notes/other.md` ALLOW |
| K0 | F1's repository: controls for the commands refused below | `git add` with nothing excluded: 1 index entry; `git commit` with no hook: exit 0, 1 commit; `wait --on item-a --ready HEAD` with a commit: WAITING |
| F2 | `git init --separate-git-dir=<elsewhere> <dir>` | the directory gains one entry, `.git`, a regular file of 146 bytes here (`gitdir: `, the separate directory's absolute path, a newline); `core.worktree` unset; `buddy init` puts `buddy.db` in the separate directory, 0 files named `buddy*` under the project; B's write to A's file DENY, to `notes/other.md` ALLOW, and DENY again with cwd `notes/`; `BASE` reads "(none recorded…)"; `wait --ready HEAD` refused ("names no commit here"; control K0); the chat line names room `f2`, the separate directory's name less `.git` |
| F2b | a `pre-commit` in that git directory that exits 1 | `git commit --allow-empty` refused with the hook's words; 0 commits (control K0) |
| F2c | the project directory renamed | git names the new path as root; B's write to A's file DENY there, to `notes/other.md` ALLOW |
| F2d | the project directory copied with `cp -R` | both `.git` files name one git directory, so one ledger: B's write to the COPY's `notes/plan.md` DENY, to its `notes/other.md` ALLOW |
| F2e | the separate git directory moved away | `status`: "not a git repository"; B's write to A's file ALLOW |
| F2f | the `.git` file removed, ledger intact elsewhere | B's write to A's file ALLOW; file restored, DENY |
| F3 | `.git` in the PARENT, project in `vault/` below it | nothing added to `vault/`; `buddy init` from `vault/` makes the parent's ledger; the chat line names the parent, `f3` |
| G1 | F3's layout, `claim --scope notes/plan.md` typed from `vault/` | granted with no warning as `notes/plan.md` under the PARENT: B's write to `vault/notes/plan.md` ALLOW, to `<parent>/notes/plan.md` DENY; claimed as `vault/notes/plan.md`, B's write there DENY |
| F4 | no `.git` anywhere, `GIT_DIR` and `GIT_WORK_TREE` exported | the typed verbs follow the variables: `buddy ls` exits 1 without them, `init` and `ls` exit 0 with them. `hello`, a hook verb, exits 0 and registers nothing: the `claim` right after says the session "has not said hello in this repo's ledger". B's write ALLOW |
| F5 | a repository holding a symlink to the project | `git -C` on the wrapper names it (control); through the link: "not a git repository" |
| G2 | an inited repository inside an inited one; A, in the outer, claims the inner directory; one absolute target for both probes | B homed in the INNER: ALLOW; B homed in the outer: DENY |
| G3 | a repository that never commits; A's `beat` names `notes/plan.md` | `whose` lists `alpha … uncommitted` under DIRTY IN; with no commit to make the path clean, no scan retracts it |
| G4 | the dirty scan's own command over a never-committed tree, best of 3 | 1,000 files: 9 ms, 28,000 bytes; 10,000: 17 ms, 280,000 bytes; 50,000: 52 ms, 1,400,000 bytes, past the scan's 1,048,576-byte cap |
| H1 | F2 plus `*` in the git directory's `info/exclude` and F2b's hook | `git add notes/plan.md` refused ("ignored by one of your .gitignore files"), 0 index entries; commit refused, 0 commits (controls K0); the scan's command prints 0 bytes |
| H2 | H1's directory: O holds `orc-vault` and run `r1` (`.buddy/slot/r1`, `.buddy/slot/main`); A and B each claim one file | B's write to A's file DENY, A's own ALLOW; A's `release item-a --to orc-vault --in-run r1`: "handed … parked in run r1"; B's `wait --on r1 --until 4h --note "READY: notes/b.md draft 2"`: WAITING; `who r1` lists bravo by name; `wait ls` quotes the note; O's `release r1 --outcome pass --note …`; B's `wait check`: LANDED, outcome PASS with the note. The project's entries afterwards: `.git`, `notes` |
| H2b | the parked item, run open (`ls`: "PARKED in run r1 produced by alpha") | B's write DENY; A's, who parked it, DENY; O's, who holds it, ALLOW; B's to an unclaimed path ALLOW. After the run's release: B and A still DENY. After O releases the item: B ALLOW |
| H2c | run open, B claims the parked scope: `claim item-c --based-on r1 --scope notes/plan.md` | granted ("BASED ON run r1"); B's write to the parked file ALLOW; that claim released, DENY again |
| H3 | the same directory: A's `beat` names `notes/plan.md`; 17 s later (a scan runs at most once per 15 s) A's `beat` names another file, `notes/other.md` | `whose notes/plan.md` at once: `alpha … uncommitted`; after the second beat: DIRTY IN (none), while `notes/other.md` is now listed. A row lives until the next scan after it |
| H4 | `touch` a stamp, then one claimed file edited and one stray file created | `find . -type f -newer <stamp>` prints exactly those two |
| H6 | G4's 50,000 files with `*` in `info/exclude` | the scan's command: 9 ms, 0 bytes (best of 3); `cp -c -R` of the tree: 5,283 ms (one run; two earlier runs read 7,033 and 8,221) |
| J1 | B's cwd is a plain directory outside the project; it never said hello there | Write by absolute path into A's claimed file DENY; to an unclaimed file there ALLOW; Bash `cd <project> && echo x > f` ALLOW, and DENY after `pause all` in the project; Bash with no directory change, still paused, ALLOW |
| J3 | `buddy gate` wall time, 20 calls each, min / median / max ms, load average 4.3 on 12 cores | project, Write to an unclaimed path 15.7 / 17.1 / 20.2; project, Bash `true` 14.4 / 16.8 / 24.2; plain directory, Write 35.0 / 37.0 / 43.7; plain directory, Bash `true` 20.5 / 21.5 / 22.5; one `git rev-parse` 7.4 / 7.8 / 8.5 in the project and 7.1 / 7.6 / 8.6 failing in the plain directory |

J3 moves with the machine. Three earlier runs of the same loop the same day,
two of them while other lanes' test tiers ran, read for the two Write rows
15.4 against 35.1 ms median (load 5), 15.8 against 47.1 (load 18) and 22.4
against 61.5 (load not recorded). Across the four runs a Write where NO
ledger is found costs two to three times a Write where one is. Why is read
from the code and fits the Bash rows, not separately measured: discovery runs git twice on a failure (D-138 says so of
its own numbers), and a call that names a path then discovers the target's
directory as well (`gateForeignRepo`). Section 3.2 uses it.

Not measured, each named where it matters: any sync tool (the lane writes
nothing into the operator's synced folders); what Claude Code itself does on
finding a `.git` file (its own git context, its worktree features); a real
one-pane run with live lanes in such a directory; Codex lanes there.

## 3. Options

### 3.1 Who this is for, and what serves each (brief item 1)

| The directory | Serves it today | Code |
| --- | --- | --- |
| merely has no commits yet; git is welcome | `git init`, `buddy init` (`not-code.md`; F1) | none |
| is to keep no history, but may hold one pointer file | P: the separate git directory (F2, H0 to H2) | none |
| may hold nothing new, and can sit in a parent made for it | W: the parent wrapper (F3), with G1's trap | none |
| may hold nothing new and its parent is not the operator's to arrange (a sync tool's container), or no git-named entry is acceptable anywhere | nothing | stage 2 |

Two more were tried and are dead. **`GIT_DIR` in the environment** (F4): the
hook verbs unset git's local environment at entry (D-137), so `hello`
registers nothing and the gate reads the directory as no repository while the
typed verbs read a ledger. That split is the bypass D-137 was written against.
**A wrapper repository holding a symlink** (F5): the process's cwd is the
physical path, which is outside the wrapper.

**P, the pointer file (recommended).** The operator runs, once per directory:

```sh
git init --separate-git-dir="$HOME/.buddy/gitdirs/<name>.git" <dir>
echo '*' > "$HOME/.buddy/gitdirs/<name>.git/info/exclude"
printf '#!/bin/sh\necho "not git-tracked: the operator keeps this directory out of git" >&2\nexit 1\n' \
  > "$HOME/.buddy/gitdirs/<name>.git/hooks/pre-commit"
chmod +x "$HOME/.buddy/gitdirs/<name>.git/hooks/pre-commit"
cd <dir> && buddy init
```

Name the git directory after the project: the chat room and so `orc-<project>`
are derived from it (`mainCheckoutName`, F2). The exclude line makes git see
nothing: `git add` is refused, the dirty scan's `git status` prints 0 bytes,
and the scan costs 9 ms on 50,000 files where it otherwise overflows its
cap (H1, H6, G4). The hook refuses a commit a lane makes from habit (F2b). Both
are cooperative, like the gate (D-005): `git add -f` and `commit --no-verify`
are git's documented ways past them (not run here). Where the git directory
goes is the operator's (question 5; the path above is an example); it must be
outside the project and outside anything synced. Cost: five commands, one
file of `gitdir: ` plus that path (about 50 bytes for a path under
`~/.buddy`) that never changes after it is written, 15 to 22 ms per gated
call at the median (J3's four runs), and the residuals of section 5 (the
pointer or the git directory removed is a silent OFF, F2e and F2f). It
follows a rename (F2c). A copy shares the ledger and over-refuses (F2d),
which is how two worktrees of one checkout behave.

**W, the parent wrapper.** `git init` in a parent that holds only the project,
the same exclude and hook, `buddy init`. Nothing is added to the project.
Costs: every scope is relative to the PARENT, and a scope typed as it looks
from the project reserves a path nobody edits, silently (G1), so lanes start
with the parent as cwd or write `vault/…` scopes; the room and `orc-` name are
the parent's (F3); the ledger's brake and roster cover everything under the
parent. Not available where the parent belongs to a sync tool.

**`git init` in place** stays right for the first row. For a directory that is
synced it is the worst option: the ledger (`buddy.db` and its `-wal` and
`-shm`, rewritten on every hook) would sit inside the synced folder. NOT
MEASURED; a tool that copies a live database and its write-ahead log at
different moments is the case SQLite's corruption notes file under backing up
during a transaction. P and W avoid it by construction.

### 3.2 Where the ledger lives and how a hook finds it (brief item 2)

**Today.** One function answers both questions, `discoverRepo`
(`internal/cli/cli.go:910`): `git rev-parse --git-common-dir --show-toplevel`
from the hook's cwd; the ledger is `<common dir>/buddy.db`, the root is the
toplevel. Its two `errNoLedger` returns (git says "not a git repository" or
no `.git` is above; git refused a repository whose ledger is provably absent,
D-044), with `openRepo`'s for a repository never inited, are the feature-off
verdict. Call sites outside tests, counted by the grep in section 10: `discoverRepo` 5,
`resolveRepo` 2, `openRepo` 14, `mustLedger` 21; the `.git`-entry helpers
that key the gate's per-call cache and its nested-repository test,
`nearestGitEntry` 2, `repoKeyForDir` 6, `gateRepoKey` 1, `hasGitAncestor` 1,
`ledgerProvablyAbsent` 1; and the gate's own literal, `homeKey :=
filepath.Join(homeTop, ".git")` (`cli.go:2929`), which a root with no `.git`
never matches (2983). Git-only features behind them: the dirty scan
(`gitDirtyPaths`, 2 call sites), the base sample (`baseGit`, 8), `init`'s
language-server count (`sourceCounts`, `git ls-files`), the commit gate.

**Under P and W nothing changes**: git finds the ledger through the pointer or
the parent, from the filesystem alone, which is why F2 holds with git's
environment scrubbed. The root is the directory holding the `.git` entry.
Invariant 14's containment and invariant 13's folding run on root-relative
paths after `placeCanonical` and never see where the ledger is.

**Stage 2, a ledger found without git.** Three shapes:

- **M, a marker file with the ledger's absolute path** (the `.git` file's own
  shape under a buddy name: `.buddy-project`, one line, a regular file of at
  most 4,096 bytes, read as `commonDirOf` reads a gitfile). Found by a walk up
  from the cwd, so discovery stays on the filesystem (D-137's rule); follows a
  rename; a copy shares the ledger as F2d does.
- **R, a registry outside every project** (one small file mapping a canonical,
  folded root to a ledger directory): nothing is added to the directory at
  all. The gate decides it is inside a project by invariant 14's own test on
  absolute canonical paths, `root == cwd || HasPrefix(cwd, root+"/")`. It
  cannot follow a rename (the new path is unregistered: silent OFF), and it is
  found through the machine, not the directory: looked up through `$HOME` it
  repeats D-137's failure for a session started under another `HOME`, so the
  design would have to find it beside the installed binary (`os.Executable`),
  NOT MEASURED.
- **C as BEYOND-CODE drafted it, a marker directory holding `buddy.db`**: CUT.
  It puts the ledger inside the directory, the synced-folder hazard above, and
  it is the one thing P already avoids.

Whichever shape, the same five rules, which are the design pass's starting
point and not yet a design:

1. **A discovery source, not a new verdict** (BEYOND-CODE's finding 11). Off
   and unreadable stay two verdicts: no marker or registry row on the way up
   and git says no → OFF, as today; a marker or registry that exists and
   cannot be read or parsed → DENY, naming the file and the fix (D-084's
   register); a level of the walk that answers anything but "exists" or "does
   not exist" → DENY, the cannot-tell case; a marker or row whose ledger is
   missing → DENY, because unlike a bare `.git` it is positive evidence of an
   init (this is stricter than F2e and is question 6); a ledger that will not
   open → DENY, as today.
2. **One walk before git runs, and a fixed precedence.** One `Lstat` walk
   from the cwd to the root (the walk `nearestGitEntry` already is) notes the
   nearest `.git` entry and EVERY marker above the cwd. Then, first match:
   - two markers on the path, or a marker and a `.git` entry in one
     directory → DENY, naming both: cannot tell;
   - no marker → today's path, git asked exactly as now. A repository is
     untouched but for one `Lstat` per level;
   - a marker and no `.git` entry → the marker's ledger; git is not run;
   - the marker is the DEEPER (`/outer/.git`, `/outer/sub/.buddy-project`) →
     the marker governs only where the repository above provably has no
     ledger, by `ledgerProvablyAbsent`'s own test, which needs no git; where
     it has one, or the test cannot say → DENY: a governed root inside a
     governed root;
   - the `.git` entry is the deeper (a clone inside the project) → git is
     asked as now. A ledger there is the home, G2's behavior and its residual
     (an outer claim on that directory does not bind a session homed inside
     it); no ledger there → the marker above governs, with the marker's
     directory as root; git refusing or failing → today's verdict.
   Consulting the marker only AFTER git says no was the first draft and is
   refused on J3: across its four runs that path starts at 35 to 62 ms median
   for a Write, against 15 to 22 where a ledger is found, and the hook budget
   is 100 ms. The walk and git can disagree (across a mount, D-138's open
   residual); the design pass owns that. Under shape R a registered root that
   contains the cwd stands where "a marker at that root" stands above.
3. **`init --no-git` refuses to create what rule 2 would deny**: at or under
   a governed root of either kind, above an existing marker, or beside a
   `.git` entry. Nesting is thereby stricter than git's own (G2).
4. **Every site that keys or bypasses discovery is in the review scope**: the
   counts above, the `homeKey` literal, the cross-repository arm
   (`gateForeignRepo`, which J1 exercises), the reached-brake arm (D-138), the
   vanished-cwd walk (D-102), the commit gate's own discovery, and `init`.
5. **No schema change.** The root is already a session column (`worktree`);
   the marker or registry is a file. A schema bump would cost D-037's one-way
   migration for a feature most ledgers never use.

Cost: full tier (what the gate refuses and allows). Recommendation: build
neither until question 1 names a real directory P and W cannot serve; then M
if a buddy-named file is acceptable there, R only if no file is, with R's two
residuals (rename, `HOME`) written into its decision record.

## 4. What each feature becomes (brief item 3)

"Today" means under P or W, measured where a row is cited. "Stage 2" is what a
git-less ledger would owe.

| Feature | Today, under P or W | Stage 2 owes |
| --- | --- | --- |
| Claims and the gate | unchanged (F2, H2, J1) | discovery only; placement after it is the same code |
| Slots | unchanged; `.buddy/slot/<name>` is a scope, and no file appears (H2) | nothing |
| Waits | unchanged (H2) | nothing |
| READY | no commit to name: `wait --on <run> --until 4h --note "READY: <path> <version word>"` (`not-code.md`; H2), read with `wait ls`; a chore or lite lane parks instead, `release <item> --to <coordinator> --in-run <run>` (H2). `--ready HEAD` is refused (F2) | the same; `--ready` with a non-commit token stays unproposed (BEYOND-CODE 1.3) |
| Commit gate | none: nothing is committed, and `setup-clone.sh` is not run. The second line behind the gate (D-012) does not exist here | none possible |
| Dirty paths | with the exclude, a tool-named path is recorded and retracted by the next scan after it (H3). A scan runs only on some session's `beat`, at most once per 15 s per session and directory, so the register holds what was touched since the last scan and does not clear while nobody works. Without the exclude, every touched path stays "uncommitted" for good (G3), and on a tree whose listing passes 1,048,576 bytes each scan is abandoned and retracts nothing (G4; `gitDirtyPaths`) | no `git status`: record nothing and have `whose` say the register is off here (invariant 10: a scan may only retract, and there is none) |
| Base | "(none recorded…)" (F2); the roster's `base` column is empty | skip the sample |
| Chat room, `orc-<project>` | the git directory's name less `.git` under P (F2); the parent's name under W (F3) | the root's directory name, said outright: `mainCheckoutName` reads the ledger's directory, which would name the ledger's home |
| Messages, inbox, wakes, ids, pause, authority files, identity | unchanged; none reads git | nothing |
| Handoff size | unchanged: read from the session's own transcript, declared by `BUDDY_HANDOFF_AT` | nothing |
| Landing and "main" | 4.1 | the same |
| Reports | `cost-`, `model-`, `wake-`, `startup-` and `fleet-report.sh` find the ledger with `git rev-parse --git-common-dir`, which P and W answer; NOT RUN in the fixtures | each needs its ledger named: `cost-report.sh` and `model-report.sh` read `BUDDY_LEDGER` today, the others would gain it (lite) |
| Review receipts | no commit body to carry one: the receipt goes in the READY message and the run's `--note`; `receipt-check.sh` has nothing to read | the same |
| `init`'s language-server report | counts tracked files, so it prints for none | skip |
| Opening lanes | every lane's tab gets the project as `--cwd` and its own `BUDDY_LABEL`; no worktree is made. A Codex lane escalates every buddy call as the skill says (the ledger is outside its workspace). NOT MEASURED with live lanes | the same |

### 4.1 One pane, several lanes, one directory

What changes is not buddy; it is that **there are no worktrees and no
branches**. In a repository each lane edits its own worktree and the
coordinator integrates commits. Here every lane's cwd is the same directory
and an edit is live in it the moment it is written. Four consequences:

- **The gate is the only separation, and it sees only paths a tool names.** A
  lane that writes from a shell passes every claim (D-005), and no commit gate
  catches it later. Each lane's brief says: claim before you edit, write with
  the file tools and never from a shell, never `git add`, `commit` or `stash`
  here, stay in this directory (a foreign command in a subshell).
- **Landing is acceptance, not arrival.** READY says "my paths are in the
  state to read"; the run's outcome says the coordinator read them and what it
  decided. The fleet skill's step 3 loses squash, cherry-pick, the integration
  worktree and the fast-forward, and keeps the run's claim, the answer to
  every READY, the outcome and the words that end a lane's work. What holds the
  state under review is the parked claim instead of a sha, and it is weaker
  than a sha. Once parked, the item refuses every other lane, the one that
  parked it included, and allows the coordinator that holds it, through the
  run's release and until the item itself is released (H2b). One exception,
  D-133's: a lane that claims the same scope exclusively with `--based-on
  <run>` is admitted and may write there (H2c). In a directory with no
  branches nothing is "based on" a run, so every lane's brief says never to
  pass `--based-on`; the rule is cooperative, and the stamp check below is
  what would show such a write. A red goes to the lane the item is handed
  back to and is fixed in place; there is nothing to back out, and nothing
  can be.
- **The project's own checks are owed before READY and before the outcome.**
  A directory without git can still hold scripts, configuration or code.
  Whatever checks that content has (its tests, a linter, a validator) the
  lane runs before READY, the coordinator runs on the assembled state before
  the outcome, and the outcome's note names them with their results. "Read
  and accept" is the gate only for a deliverable whose class has no checks
  (notes, prose), and the outcome's note then says "read, no checks apply".
  An outcome of pass over content with checks that were not run is a false
  receipt; this plan does not license one.
- **"Which files did this run change" has no `git diff`.** The coordinator
  `touch`es a stamp in its own session's scratch directory (the harness's,
  never a path of its choosing in a directory the operator keeps) when it
  forms the run, and before the outcome runs `find . -type f -newer <stamp>`
  and holds the list against the riders' scopes from `buddy ls` (H4: it lists
  the claimed file edited and the stray one). A path outside every rider's
  scopes is a shell write or an unclaimed edit, to be asked about before the
  outcome. An observation, never
  a refusal (invariant 10), and it attributes nothing.

Options for the landing itself, the operator's choice (question 3):

- **In place** (recommended first): as above. Cost: unaccepted work is already
  in the directory, which matters where something consumes it live (a synced
  vault, a published folder, a pipeline).
- **Copy-in**: each lane works on a private copy of its claimed paths outside
  the project and the coordinator copies exactly the parked scopes in at
  landing, holding `.buddy/slot/main`. A red then lands nothing. Cost: the
  lane's edits are outside the governed root, so the gate adjudicates none of
  them, and a lane must be briefed on two trees. Where the copies live is the
  operator's to name, per directory, in the coordinator's pane (question 5);
  no session picks it. NOT MEASURED.

The tier is the workflow's own gate, as `not-code.md` says, under the checks
rule above. A snapshot before a landing (`cp -c -R`, H6) is the only undo
there is. It writes outside the project, so it is taken only where the
operator has said so and named the destination (question 4), never on a
session's own motion.

Handoff is unchanged for a lane (a fresh `<lane>-2` session in the same
directory, claims by `release --to`) and for the coordinator (`orc-<project>`
moves with `release --to`; the state note is a file in its session's scratch
directory, or a claimed path inside the project if the operator wants it
kept there).

## 5. Failure modes (brief item 4)

"P today" is measured. "Stage 2" is what rules 1 to 3 of 3.2 would give, with
nothing built.

| Event | P today | Stage 2, marker M | Stage 2, registry R |
| --- | --- | --- | --- |
| pointer or marker deleted | silent OFF; the ledger and its claims sit unread (F2f) | silent OFF, the same: nothing in the directory says it was governed | cannot happen |
| ledger (or its directory) deleted | silent OFF (F2e) | DENY, naming the marker and the fix (rule 1) | DENY |
| directory renamed or moved | follows (F2c) | follows | silent OFF at the new path; a view can only report the old root MISSING |
| directory copied | one ledger, both copies; claims refuse in each (F2d). Over-refusal, the safe side | the same | the copy is unregistered: OFF, which is right for a copy |
| pointer or marker synced to a second machine | names a git directory that is not there: silent OFF (F2e's shape) | DENY there until that machine's operator runs `init` or removes the marker (question 6) | nothing is synced |
| a sync tool evicts, replaces or conflicts the pointer or marker | NOT MEASURED. If the entry is absent for an instant the gate is OFF for that instant (F2f) | the same | immune |
| ledger inside a synced folder | avoided by construction | avoided | avoided |
| two nested | the deeper wins; an outer claim on the inner directory does not bind a session homed inside (G2) | refused at `init`, DENY at discovery (rules 2 and 3) | refused at `init` |
| a session's cwd leaves the project | its file-tool writes back in are still judged by the project's claims, and a literal `cd` back in reads its brake (J1; D-138). Not re-run, read from the code and BEYOND-CODE 3.4(A): a `cd` inside one command does not move the hooks' cwd; a session that does settle elsewhere beats no ledger there, so mail queued to it is not drained, its row goes stale, and its claims stay held (invariant 11) | the same, once discovery covers targets (rule 4) | the same |
| a session started under another `HOME` | no effect: discovery is from the filesystem | no effect | OFF unless the registry is found beside the binary |

What does NOT fail closed, said plainly: under P, and under M, removing the
one small file turns the feature off without a word, exactly as removing
`.git` does in any repository today. Closing it needs something outside the
directory that remembers the directory was governed, which is the registry.
M and R together (a marker that follows a rename, a registry that notices
the marker gone) close the deletion at the REGISTERED path only. They do not
close every order of events: move a registered `/a` to `/b`, then remove the
marker at `/b`, and `/b` has neither marker nor row, so rule 1 reads OFF. No
shape in this plan is fail-closed against a move and a deletion together.
The pair costs both halves: a registry read on every hook in every directory
git does not claim, and a stale registry row that denies until the operator
retires it. Not recommended before stage 0's sync measurement shows the
pointer can go missing in practice.

## 6. Staged rollout (brief item 5)

| Stage | What | Tier, by class | Owes |
| --- | --- | --- | --- |
| 0 | The operator runs P (or W) in the real directory, and a coordinator the operator starts there runs one batch of lanes. No buddy code, no commit | none: the five commands are the operator's own act, never a lane's and never done on a relayed word (they write into a directory the operator keeps and under the operator's home) | three counts before stage 1 is written, each read without writing: whether the pointer survives the directory's sync tool for a week (`ls -la <dir>/.git` daily, bytes and mtime unchanged); what Claude Code does on meeting the pointer (its start context's git lines, in bytes); refusals, denies and strays (4.1's `find`) over the first batch. The project's own checks are owed as 4.1 says |
| 1a | `skills/buddy-fleet/not-code.md` gains "a directory you keep out of git": the P recipe as the operator's act, W and G1's trap, 4.1's consequences, the `--based-on` rule and the stamp check | chore: skill prose naming no new verb or flag | one decision record (the recipe, F4 and F5 as the refused alternatives, section 5's residuals as accepted), its number on CLAUDE.md's Skills line; the skill tests still pass; `not-code.md` is 2,337 bytes and a lane raises no ceiling in `docs/prose-budget.tsv`: an overage is named at READY for the operator |
| 1b | A done-check, `scripts/check-nongit-recipe.sh`, invoked from `check.sh`: section 10's script cut to the rows that pin the recipe (F2, F2c to F2f, H1, H2, H2b, H2c), so a later discovery change cannot quietly break it. No test names `--separate-git-dir` today (`grep -rl separate-git-dir internal scripts docs` prints nothing at c41826a) | the operator's to say (question 9): it changes nothing the gate does, but its subject is the gate's verdicts, and CLAUDE.md gives a test-only change its subject's tier, which read strictly is full | each DENY beside its ALLOW; each pinned verdict watched to fail under a mutation of discovery in a private copy; never run against a real ledger |
| 2 | Only on question 1: discovery without git, shape M or R (3.2), `buddy init --no-git`, the dirty register and base sample off there, the room name, the reports' ledger knob | full: what the gate refuses and allows. Codex design pass before code, panel, Codex code pass, Fable cross-check, re-verify to the brief's ceiling | a decision record amending D-005 and D-044 and naming D-102, D-137 and D-138 as touched; every row of section 5's column as a test with its positive control; the hook-latency rows of J3 re-taken on the new path; mutants per `errNoLedger` arm; the new flag in `init`'s parser, usage line and a skill in the same commit; `install.sh` after landing (the operator's) |
| 3 | Each only if stage 0 or a month of use shows the count: the stamp check as a script (lite); the marker-and-registry tripwire of section 5 (full); `--ready` carrying a non-commit token (full: schema) | as marked | its own record |

Stages 1 to 3 go through this repository's own run and tier like any item,
with the tests and mutation checks every tier owes. What stage 0 and 4.1 do
change is the non-git project's own landing: an edit is in the directory
before anyone has read it, "landed" means accepted, and the checks that
content owes are run before READY and before the outcome (4.1). That an edit
is live when written is the nature of a directory without branches, not a
step this plan adds; copy-in is the answer where it is not acceptable.

Operator-only, wherever this plan mentions them: stage 0's commands; any
`install.sh`, `buddy update` or `--wire-hooks`; raising a prose ceiling; and
naming every location outside the project that anything is written to (the
git directory, copy-in copies, snapshots, a stage-2 ledger). A session writes
outside the project only into its own scratch directory.

## 7. Not in this plan

Multi-machine claims (out of scope in CLAUDE.md; a synced marker shares an id
at most, never a ledger). Anything that watches the directory (a daemon, an
fsevents reader). Buddy taking snapshots or running git for the operator. A
second landing verb. Authority from chat. Widening the `PreToolUse` matcher so
`pause` reaches MCP-only lanes (BEYOND-CODE 1.2, still open there). Launching
or closing lanes (the coordinator's, with the harness).

## 8. OPEN: only the operator can answer

1. **Which reading of "not git tracked"?** May the directory hold one small
   `.git` pointer file with nothing tracked and commits refused (then stage 0
   is the whole answer), or must it hold no git-named entry (then W, or stage
   2)? If stage 2: is a buddy-named file acceptable there (M), or nothing (R)?
2. **Is the directory synced by another tool, and is its parent yours to
   arrange?** It decides P against W, and whether stage 0's first week must
   prove the pointer survives.
3. **In-place edits, live when written, or copy-in landing where a red lands
   nothing?**
4. **A snapshot before each landing: wanted, and where?**
5. **Where may anything outside the project be written**, each location
   named by you: the git directory (`~/.buddy/gitdirs/` in the recipe),
   copy-in copies and snapshots if you want either, a stage-2 ledger
   (`~/.buddy/projects/`)?
6. **Is a silent OFF acceptable when the pointer is deleted**, as when `.git`
   is deleted in any repository today, or is the stage-3 tripwire worth its
   cost? And for stage 2: a marker whose ledger is missing, DENY (proposed) or
   OFF (parity with F2e)?
7. **What is the workflow's gate in such a project**: the coordinator's read,
   a second model's read, yours?
8. **Is stage 1 wanted now**, before a stage-0 week has produced its counts?
9. **Stage 1b's review tier.** A done-check that pins the gate's verdicts for
   the recipe changes nothing the gate does; read strictly, a test-only change
   takes its subject's tier, which is full. Full, or capped at lite? (Codex
   finding 12, answered here and not folded.)

## 9. Taken and refuted

**The Codex pass** (gpt-6-astra, xhigh, 2026-11-14, through
`scripts/codex-review.sh` in the foreground, `CODEX_DIFF=c41826a..4eaf59e`).
A first attempt with a wider prompt (seven questions, the code read in full)
ran into the script's 540 s watchdog and returned NO VERDICT; nothing from it
is counted. The prompt's scope was cut, as CLAUDE.md says to, and the pass
ran once more: 212 s, header verified, verdict revise, twelve findings.
Finding 1 was reproduced before it was taken (H2c). Folded once; no re-verify
ran, which is the brief's ceiling.

| # | Finding | Taken / answered | Change |
| --- | --- | --- | --- |
| 1 | 4.1: parking does not freeze the reviewed paths; a lane claiming `--based-on <run>` may write there | Taken; reproduced as H2c | 4.1 names the exception and the brief rule against it; "refuses every lane" removed |
| 2 | 4.1, 6: "read and accept" lets content that owes checks get a pass without them | Taken | 4.1: the project's own checks are owed before READY and before the outcome; the no-check gate only for a class with none; stage 0 says so |
| 3 | 4.1, 6: the stamp, the handoff note, copy-in copies and snapshots are writes outside the project that nobody authorized | Taken | a session writes outside the project only into its own scratch directory; every other location is the operator's to name (section 6, question 5) |
| 4 | 2, 10: the gate helper counted bytes and dropped buddy's exit status and decision | Taken; the lane's own scripts did grep the decision, the published helper did not | section 10's helper keeps the exit status and parses the JSON decision; every row re-run with it |
| 5 | 2, 4, 10: replaying H3 with the same path does not give an empty register | Taken; the lane's second beat named another file, which section 10 did not say | H3 says so and shows both paths; "empty within 15 s" replaced in 4 |
| 6 | 10: fixtures lacked their directories and files | Taken | section 10 is now the script that was run |
| 7 | 2, 10: the refused commands had no succeeding control | Taken | K0, and F4's and F5's own controls |
| 8 | 10: G2's two probes named different targets as written | Taken; the run used one absolute path, the text did not | one absolute target, in the script |
| 9 | 2, 10: sizes and timings without reproducible inputs | Taken | the timing program is in the script; path-dependent sizes are marked "here"; the earlier J3 runs are named as such |
| 10 | 3.2: rules 2 and 3 gave two verdicts for `/outer/.git` with `/outer/sub/.buddy-project` | Taken | one precedence list in rule 2, the same-directory tie included; rule 3 is `init`'s side of it |
| 11 | 5: M with R is not fully fail-closed (move, then delete the marker) | Taken | the claim is narrowed to deletion at the registered path, and the sequence is written out |
| 12 | 6: stage 1's lite tier rested on "it is a script", but a test-only change takes its subject's tier | Answered, not folded | stage 1 split into 1a (prose, chore) and 1b (the done-check); 1b's tier is question 9 |

Codex also confirmed section 1's quotation of option C's trigger and noted
that the bar for building is this plan's own; section 1 now says so.

**The lane's own review before the pass** raised thirteen points, all fixed
in the draft Codex read: the `.git` file's size given as if it were fixed;
F4's ledger asserted and not shown; J3's cause stated as measured; a claim
about Claude Code's own `git status`; `git add -f` and `--no-verify` implied
as run; a claim about SQLite's documentation wider than its text; "three"
`errNoLedger` returns where `discoverRepo` has two; rule 2 describing a walk
git does not do today; which reports read `BUDDY_LEDGER`; the stage-2 room
name resting on a fallback that would name the ledger's directory; the
parked-claim refusal quoted from after the run closed (measured mid-run as
H2b); the cwd-leaves row presented as measured; and rule 1 missing the
cannot-tell arm of the walk.

## 10. Re-running the fixtures

Save the block below as a file and run `sh <file> <empty directory>`; the
directory must be outside every repository. It needs `buddy`, `git` and
`python3` on PATH, takes about two minutes, registers three minted session
ids in throwaway ledgers it creates, and touches no other ledger. Each `==`
heading is a row of section 2. This is the text that produced that table.

```sh
#!/bin/sh
# Fixtures for docs/NON-GIT-PLAN.md. usage: sh fixtures.sh <empty directory>
# Throwaway ledgers only: the directory must be outside every repository.
# Session ids are minted. Needs buddy, git, python3 on PATH. About two minutes.
set -u
W=$1
[ -d "$W" ] && [ -z "$(ls -A "$W")" ] || { echo "need an empty directory"; exit 2; }
W=$(cd "$W" && pwd -P)
git -C "$W" rev-parse --git-dir >/dev/null 2>&1 && { echo "inside a repository"; exit 2; }
O=00000000-0000-4000-8000-000000000000
A=11111111-1111-4111-8111-111111111111
B=22222222-2222-4222-8222-222222222222
unset CODEX_THREAD_ID GIT_DIR GIT_WORK_TREE GIT_COMMON_DIR
as() { id=$1; shift; CLAUDE_CODE_SESSION_ID=$id "$@"; }
say() { sed "s|$W|<W>|g" | cut -c1-"${2:-170}" | sed "s|^|  $1: |"; }
hi() { as "$1" buddy hello --session "$1" --label "$2" </dev/null >/dev/null 2>&1 || echo "  hello $2 FAILED"; }
# gate <label> <session> <cwd> <Write|Bash> <absolute path | command>
# ALLOW: buddy exits 0 and prints nothing. DENY: exits 0 and prints a JSON
# document whose permissionDecision is "deny". Anything else is printed raw.
gate() {
	k=file_path; [ "$4" = Bash ] && k=command
	out=$(printf '{"hook_event_name":"PreToolUse","session_id":"%s","cwd":"%s","tool_name":"%s","tool_input":{"%s":"%s"}}' \
		"$2" "$3" "$4" "$k" "$5" | CLAUDE_CODE_SESSION_ID=$2 buddy gate 2>/dev/null)
	rc=$?
	n=$(printf %s "$out" | wc -c | tr -d ' ')
	if [ "$rc" = 0 ] && [ "$n" = 0 ]; then v=ALLOW
	elif [ "$rc" = 0 ] && printf %s "$out" | python3 -I -c 'import json,sys
sys.exit(0 if json.load(sys.stdin)["hookSpecificOutput"]["permissionDecision"]=="deny" else 1)' 2>/dev/null; then v="DENY ($n bytes)"
	else v="OTHER rc=$rc: $out"; fi
	echo "  $1: $v"
}
beat() { printf '{"hook_event_name":"PostToolUse","session_id":"%s","cwd":"%s","tool_name":"Write","tool_input":{"file_path":"%s"}}' \
	"$1" "$2" "$3" | CLAUDE_CODE_SESSION_ID=$1 buddy beat >/dev/null 2>&1; }
SCAN='git -c core.fsmonitor=false --no-optional-locks status --porcelain -z -uall'
lanes() { hi $A alpha; hi $B bravo; as $A buddy claim item-a --desc fixture --scope "$1" 2>&1 | say claim; }

echo "== F0 plain directory"
mkdir -p "$W/plain"; cd "$W/plain"
buddy status 2>&1 | head -1 | say status 60
gate "B writes x.md" $B "$W/plain" Write "$W/plain/x.md"

echo "== F1 git init in place; K0 controls for the refused commands below"
mkdir -p "$W/f1/notes"; git init -q "$W/f1"; cd "$W/f1"
buddy init >/dev/null 2>&1; lanes notes/plan.md
gate "B writes A's notes/plan.md" $B "$W/f1" Write "$W/f1/notes/plan.md"
gate "B writes notes/other.md" $B "$W/f1" Write "$W/f1/notes/other.md"
echo x >notes/plan.md
git add notes/plan.md; echo "  K0 git add, nothing excluded: index entries $(git ls-files | wc -l | tr -d ' ')"
git -c user.name=x -c user.email=x@x commit -qm x; echo "  K0 git commit, no hook: rc=$? commits $(git rev-list --all | wc -l | tr -d ' ')"
as $B buddy wait --on item-a --ready HEAD 2>&1 | head -1 | say "K0 wait --ready HEAD with a commit" 90
as $B buddy wait clear >/dev/null 2>&1

echo "== F2 separate git directory"
mkdir -p "$W/gitdirs"; git init -q --separate-git-dir="$W/gitdirs/f2.git" "$W/f2"; mkdir -p "$W/f2/notes"; cd "$W/f2"
echo "  entries: $(ls -A | tr '\n' ' '); .git: $(stat -f '%HT, %z bytes' .git) = 8 + $(printf %s "$W/gitdirs/f2.git" | wc -c | tr -d ' ') path bytes + 1; core.worktree: $(git config --get core.worktree || echo unset)"
buddy init 2>&1 | head -1 | say init
echo "  files named buddy* under the project: $(find "$W/f2" -name 'buddy*' | wc -l | tr -d ' ')"
lanes notes/plan.md
gate "B writes A's notes/plan.md" $B "$W/f2" Write "$W/f2/notes/plan.md"
gate "B writes notes/other.md" $B "$W/f2" Write "$W/f2/notes/other.md"
gate "B, cwd notes/, writes A's file" $B "$W/f2/notes" Write "$W/f2/notes/plan.md"
as $A buddy hello --session $A --label alpha </dev/null 2>&1 | grep -o 'chat_read [^`]*' | say "chat line"
as $A buddy status 2>&1 | grep '^BASE' | say status 60
as $B buddy wait --on item-a --ready HEAD 2>&1 | head -1 | say "wait --ready HEAD, no commit" 90
as $B buddy wait clear >/dev/null 2>&1
echo "== F2b a pre-commit that exits 1"
printf '#!/bin/sh\necho "not git-tracked: the operator keeps this directory out of git" >&2\nexit 1\n' >"$W/gitdirs/f2.git/hooks/pre-commit"
chmod +x "$W/gitdirs/f2.git/hooks/pre-commit"
git -c user.name=x -c user.email=x@x commit -q --allow-empty -m x 2>&1 | head -1 | say commit
echo "  commits: $(git rev-list --all 2>/dev/null | wc -l | tr -d ' ')"
echo "== F2c renamed"
cd "$W"; mv "$W/f2" "$W/f2-moved"; cd "$W/f2-moved"
git rev-parse --show-toplevel | say toplevel
gate "B writes A's notes/plan.md" $B "$W/f2-moved" Write "$W/f2-moved/notes/plan.md"
gate "B writes notes/other.md" $B "$W/f2-moved" Write "$W/f2-moved/notes/other.md"
echo "== F2d copied"
cp -R "$W/f2-moved" "$W/f2-copy"
gate "B, in the copy, writes its notes/plan.md" $B "$W/f2-copy" Write "$W/f2-copy/notes/plan.md"
gate "B, in the copy, writes its notes/other.md" $B "$W/f2-copy" Write "$W/f2-copy/notes/other.md"
echo "== F2e the git directory moved away"
mv "$W/gitdirs/f2.git" "$W/gitdirs/f2.git.away"
buddy status 2>&1 | head -1 | say status 60
gate "B writes A's notes/plan.md" $B "$W/f2-moved" Write "$W/f2-moved/notes/plan.md"
mv "$W/gitdirs/f2.git.away" "$W/gitdirs/f2.git"
echo "== F2f the .git file removed, then restored"
mv "$W/f2-moved/.git" "$W/dotgit.away"
gate "removed: B writes A's notes/plan.md" $B "$W/f2-moved" Write "$W/f2-moved/notes/plan.md"
mv "$W/dotgit.away" "$W/f2-moved/.git"
gate "restored: B writes A's notes/plan.md" $B "$W/f2-moved" Write "$W/f2-moved/notes/plan.md"

echo "== F3, G1 .git in the parent"
mkdir -p "$W/f3/vault/notes"; git init -q "$W/f3"; cd "$W/f3/vault"
buddy init 2>&1 | head -1 | say "init from vault/"
echo "  entries in vault/: $(ls -A | tr '\n' ' ')"
lanes notes/plan.md
gate "B writes vault/notes/plan.md" $B "$W/f3/vault" Write "$W/f3/vault/notes/plan.md"
gate "B writes <parent>/notes/plan.md" $B "$W/f3/vault" Write "$W/f3/notes/plan.md"
as $A buddy claim item-a2 --desc fixture --scope vault/notes/plan.md 2>&1 | say claim
gate "B writes vault/notes/plan.md" $B "$W/f3/vault" Write "$W/f3/vault/notes/plan.md"
as $A buddy hello --session $A --label alpha </dev/null 2>&1 | grep -o 'chat_read [^`]*' | say "chat line"

echo "== F4 GIT_DIR and GIT_WORK_TREE exported, no .git anywhere"
mkdir -p "$W/f4/notes"; git init -q --bare "$W/gitdirs/f4.git"; git --git-dir="$W/gitdirs/f4.git" config core.bare false
cd "$W/f4"
buddy ls >/dev/null 2>&1; echo "  typed verb, env unset: buddy ls rc=$?"
export GIT_DIR="$W/gitdirs/f4.git" GIT_WORK_TREE="$W/f4"
buddy init >/dev/null 2>&1; echo "  init rc=$?"
buddy ls >/dev/null 2>&1; echo "  typed verb, env set (control): buddy ls rc=$?"
as $A buddy hello --session $A --label alpha </dev/null >/dev/null 2>&1; echo "  hello rc=$?"
as $A buddy claim item-a --desc fixture --scope notes/plan.md 2>&1 | say claim 150
gate "B writes notes/plan.md" $B "$W/f4" Write "$W/f4/notes/plan.md"
unset GIT_DIR GIT_WORK_TREE

echo "== F5 a repository holding a symlink to the project"
mkdir -p "$W/f5real/notes" "$W/f5wrap"; git init -q "$W/f5wrap"; ln -s "$W/f5real" "$W/f5wrap/proj"
git -C "$W/f5wrap" rev-parse --show-toplevel 2>&1 | say "control, the wrapper itself"
git -C "$W/f5wrap/proj" rev-parse --show-toplevel 2>&1 | head -1 | say "through the link" 60

echo "== G2 an inited repository inside an inited one"
mkdir -p "$W/g2/inner/notes"; git init -q "$W/g2"; git init -q "$W/g2/inner"
cd "$W/g2" && buddy init >/dev/null 2>&1; cd "$W/g2/inner" && buddy init >/dev/null 2>&1
echo "  ledgers: outer $(ls "$W/g2/.git" | grep -c '^buddy.db$') inner $(ls "$W/g2/inner/.git" | grep -c '^buddy.db$')"
cd "$W/g2"; hi $A alpha; as $A buddy claim item-a --desc fixture --scope inner 2>&1 | say "outer claim"
cd "$W/g2/inner"; hi $B bravo
gate "B, homed inner, writes <W>/g2/inner/notes/x.md" $B "$W/g2/inner" Write "$W/g2/inner/notes/x.md"
cd "$W/g2"; hi $B bravo
gate "B, homed outer, writes the same path" $B "$W/g2" Write "$W/g2/inner/notes/x.md"

echo "== G3 the dirty register where nothing is ever committed"
mkdir -p "$W/g3/notes"; git init -q "$W/g3"; cd "$W/g3"; buddy init >/dev/null 2>&1; hi $A alpha; hi $B bravo
echo draft >notes/plan.md; beat $A "$W/g3" "$W/g3/notes/plan.md"
whose() { echo "  $1: $(as $B buddy whose "$2" 2>&1 | grep -A1 '^DIRTY' | tr -s ' ' | tr '\n' ' ' | cut -c1-90)"; }
whose "whose notes/plan.md" notes/plan.md

echo "== G4, H6 the scan's command by tree size (best of 3), then excluded, then a clone"
python3 -I - "$W" <<'EOF'
import os, subprocess, sys, time
w = sys.argv[1]
cmd = ["git", "-c", "core.fsmonitor=false", "--no-optional-locks", "status", "--porcelain", "-z", "-uall"]
def scan(d):
    best = None
    for _ in range(3):
        t = time.monotonic()
        p = subprocess.run(cmd, cwd=d, capture_output=True)
        ms = (time.monotonic() - t) * 1000
        best = ms if best is None or ms < best else best
    return "%.0f ms, %d bytes of output, rc %d" % (best, len(p.stdout), p.returncode)
for n in (1000, 10000, 50000):
    d = os.path.join(w, "g4-%d" % n)
    os.makedirs(d)
    subprocess.run(["git", "init", "-q", d], check=True)
    for i in range(n):
        sub = os.path.join(d, "folder-%03d" % (i // 100))
        os.makedirs(sub, exist_ok=True)
        with open(os.path.join(sub, "note-%05d.md" % i), "w") as f:
            f.write("x\n")
    print("  %d files: %s" % (n, scan(d)))
with open(os.path.join(d, ".git", "info", "exclude"), "w") as f:
    f.write("*\n")
print("  50000 files, * in info/exclude: %s" % scan(d))
t = time.monotonic()
r = subprocess.run(["cp", "-c", "-R", d, d + "-snap"])
print("  cp -c -R of that tree, one run: %.0f ms, rc %d" % ((time.monotonic() - t) * 1000, r.returncode))
EOF

echo "== H0 the recipe; H1 git sees nothing"
P="$W/vault"; G="$W/gitdirs/vault.git"
git init -q --separate-git-dir="$G" "$P"; mkdir -p "$P/notes"; cd "$P"
echo '*' >"$G/info/exclude"
printf '#!/bin/sh\necho "not git-tracked: the operator keeps this directory out of git" >&2\nexit 1\n' >"$G/hooks/pre-commit"; chmod +x "$G/hooks/pre-commit"
buddy init 2>&1 | head -1 | say init
echo draft >notes/plan.md; echo b >notes/b.md
git add notes/plan.md 2>&1 | head -1 | say "git add" 80
echo "  index entries: $(git ls-files | wc -l | tr -d ' ')"
git -c user.name=x -c user.email=x@x commit -q --allow-empty -m x 2>&1 | head -1 | say commit
echo "  commits: $(git rev-list --all 2>/dev/null | wc -l | tr -d ' '); scan output: $($SCAN | wc -c | tr -d ' ') bytes"

echo "== H2 one coordinator, two lanes, one directory, no commit"
hi $O orc; hi $A alpha; hi $B bravo
as $O buddy claim orc-vault --desc coordinator --scope .buddy/slot/orc-vault 2>&1 | say O
as $O buddy claim r1 --desc FORMING --scope .buddy/slot/r1 --scope .buddy/slot/main 2>&1 | say O
as $A buddy claim item-a --desc plan --scope notes/plan.md 2>&1 | say A
as $B buddy claim item-b --desc b --scope notes/b.md 2>&1 | say B
gate "before parking: B writes A's file" $B "$P" Write "$P/notes/plan.md"
gate "before parking: A writes its own file" $A "$P" Write "$P/notes/plan.md"
as $A buddy release item-a --to orc-vault --in-run r1 2>&1 | head -1 | say "A parks" 120
as $O buddy ls 2>&1 | grep item-a | tr -s ' ' | say ls 110
echo "== H2b who may write the parked file while the run is open"
gate "B" $B "$P" Write "$P/notes/plan.md"
gate "A, who parked it" $A "$P" Write "$P/notes/plan.md"
gate "O, who holds it" $O "$P" Write "$P/notes/plan.md"
gate "B, an unclaimed path" $B "$P" Write "$P/notes/free.md"
echo "== H2c the exception: an exclusive claim declared --based-on the run"
as $B buddy claim item-c --desc "based on r1" --based-on r1 --scope notes/plan.md 2>&1 | head -1 | say "B claims" 150
gate "B, holding that claim, writes the parked file" $B "$P" Write "$P/notes/plan.md"
as $B buddy release item-c >/dev/null 2>&1
gate "B again, claim released" $B "$P" Write "$P/notes/plan.md"
echo "== H2 READY, the outcome, LANDED"
as $B buddy wait --on r1 --until 4h --note "READY: notes/b.md draft 2" 2>&1 | head -1 | say "B waits" 140
as $O buddy who r1 2>&1 | grep 'WAITED ON' | say "who r1" 90
echo "  wait ls lines quoting the note: $(as $O buddy wait ls 2>&1 | grep -c 'READY: notes/b.md draft 2')"
as $O buddy release r1 --outcome pass --note "accepted: item-a item-b, read by orc" 2>&1 | head -1 | say "O releases r1" 110
as $B buddy wait check 2>&1 | head -1 | say "B checks" 110
gate "run closed, item still parked: B" $B "$P" Write "$P/notes/plan.md"
gate "run closed, item still parked: A" $A "$P" Write "$P/notes/plan.md"
as $O buddy release item-a --outcome pass --note ok 2>&1 | head -1 | say "O releases item-a" 90
gate "item released: B" $B "$P" Write "$P/notes/plan.md"
echo "  project entries after all of it: $(ls -A | tr '\n' ' ')"

echo "== H3 the dirty register with everything excluded"
beat $A "$P" "$P/notes/plan.md"
whose "at once, notes/plan.md" notes/plan.md
sleep 17
echo o >notes/other.md; beat $A "$P" "$P/notes/other.md"
whose "after a beat 17 s later naming notes/other.md: notes/plan.md" notes/plan.md
whose "and notes/other.md" notes/other.md

echo "== H4 files changed since a stamp"
touch "$W/stamp"; sleep 1; echo more >>notes/b.md; mkdir -p stray; echo s >stray/x.md
echo "  find -newer: $(find . -type f -newer "$W/stamp" | sort | tr '\n' ' ')"

echo "== J1 a session whose cwd is outside the project"
as $A buddy claim item-a --desc plan --scope notes/plan.md >/dev/null 2>&1
gate "Write, absolute path into A's claim" $B "$W/plain" Write "$P/notes/plan.md"
gate "Write, an unclaimed path there" $B "$W/plain" Write "$P/notes/free.md"
gate "Bash cd into the project, not paused" $B "$W/plain" Bash "cd $P && echo x > f"
buddy pause all --note fixture >/dev/null 2>&1; echo "  pause all rc=$?"
gate "Bash cd into the project, paused" $B "$W/plain" Bash "cd $P && echo x > f"
gate "Bash, no directory change, paused" $B "$W/plain" Bash "echo x > f"
buddy resume all >/dev/null 2>&1

echo "== J3 buddy gate wall time, 20 calls each: min median max ms; $(uptime | sed 's/.*load/load/'); ncpu $(sysctl -n hw.ncpu)"
python3 -I - "$P" "$W/plain" "$B" <<'EOF'
import json, os, subprocess, sys, time
proj, plain, sid = sys.argv[1:4]
env = dict(os.environ, CLAUDE_CODE_SESSION_ID=sid)
def stats(ts):
    ts.sort()
    return "%.1f %.1f %.1f" % (ts[0], (ts[9] + ts[10]) / 2, ts[-1])
def gate(cwd, tool, inp):
    payload = json.dumps({"hook_event_name": "PreToolUse", "session_id": sid, "cwd": cwd, "tool_name": tool, "tool_input": inp}).encode()
    ts = []
    for _ in range(20):
        t = time.monotonic()
        subprocess.run(["buddy", "gate"], input=payload, capture_output=True, env=env, cwd=cwd)
        ts.append((time.monotonic() - t) * 1000)
    return stats(ts)
def revparse(cwd):
    ts = []
    for _ in range(20):
        t = time.monotonic()
        subprocess.run(["git", "-C", cwd, "rev-parse", "--path-format=absolute", "--git-common-dir", "--show-toplevel"], capture_output=True)
        ts.append((time.monotonic() - t) * 1000)
    return stats(ts)
print("  project, Write to an unclaimed path:", gate(proj, "Write", {"file_path": os.path.join(proj, "notes", "free.md")}))
print("  project, Bash `true`:", gate(proj, "Bash", {"command": "true"}))
print("  plain directory, Write:", gate(plain, "Write", {"file_path": os.path.join(plain, "x.md")}))
print("  plain directory, Bash `true`:", gate(plain, "Bash", {"command": "true"}))
print("  one git rev-parse, project:", revparse(proj))
print("  one git rev-parse, plain (it fails):", revparse(plain))
EOF
echo "== done"
```

The call-site counts of 3.2, at c41826a, for each function name `f`:

```sh
grep -rn --include='*.go' "f(" internal cmd | grep -v _test.go |
  grep -v -E '^[^:]+:[0-9]+:\s*//' | grep -v -E "func f\(" | wc -l
```
