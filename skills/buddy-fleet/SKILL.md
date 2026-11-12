---
name: buddy-fleet
description: For the ORCHESTRATOR — the one session that coordinates others with `buddy` (holds the `orc-<project>` claim, opens lanes in herdr tabs, writes their briefs, lands their READY work in one run, closes finished lanes, hands off to a successor orchestrator, or runs a fleet of fleets). Covers the launch recipe, the relay clause, review tiers and the round cap a brief names, picking a lane's model, restacking a dependent lane, and handing off a coordinator. Not for a lane working one item; a lane needs only the `buddy` skill.
---

# Running a fleet with buddy

For the session that coordinates others. This assumes the `buddy` skill
(claims, waits, messages, landing as a lane), which every session loads; it
is the lane's side of everything here.

## Your operator's standing decisions

How many lanes run at once, which model and effort they run on, which
permission mode they start in, whether you open and close their panes
yourself, the handoff size, and the review caps are the OPERATOR's standing
decisions, not constants of this skill. Find them in your instructions (the
operator's CLAUDE.md, or your brief); if you find neither, ask rather than
assume, and never fill a gap from what another orchestrator did (D-104,
D-111). A worked example of such a file, with the measurements behind each
rule, is `docs/examples/fleet-conventions.md` in the buddy-system repository.

## Opening lanes

Buddy launches nothing and ends nothing (the separate opt-in `buddy-adapter`
controls only the one worker its typed argv launched; see `docs/USAGE.md`).
Your own Bash opens lanes, under your own permission rules, and only as many
as the operator said you may run: a lane opened on a peer's word is a bill
nobody authorised.

- **Open a lane in a new herdr tab** (the operator can see it and approve in
  it). Your workspace is the part of `$HERDR_PANE_ID` before the colon:
  `herdr tab create --workspace <ws> --cwd <repo> --label <label> --env BUDDY_LABEL=<label> [--env BUDDY_CHAT=off] --no-focus`
  prints the new tab's `root_pane` id (`BUDDY_LABEL` makes the lane's `hello`
  wear that label, so `buddy msg <label>` reaches it by the name you gave it;
  without it every lane is `<repo>/s-<8hex>`; `BUDDY_CHAT=off` drops the
  digest's chat line, for a lane that never reads chat); then
  `herdr agent start <label> --kind claude --pane <pane id> -- -n <label> --session-id <uuid> --model <model> --effort <effort> --permission-mode <mode> "You are <label>. Read <brief file> in full and follow it as your brief."`.
  The model and effort are the operator's default lane model and effort,
  full tier included, unless the pick rule moved a lite or chore item or the
  operator named the item for another model (Review depth, below).
  The brief goes in a file: herdr refuses an agent argument holding a
  newline or a backtick (D-082). A lane that may run past the fleet-wide
  handoff size gets its own, `herdr tab create … --env BUDDY_HANDOFF_AT=<n>`,
  which reaches the lane's hooks; without it a lane hears the settings one,
  or nothing (D-064, D-096). A start refused with `agent_pane_busy` (the pane is not
  yet at its shell prompt) works when retried seconds later (D-082). A start
  that answers `timed out waiting for agent startup` has usually started the
  lane anyway, but herdr then does not know it by label: wait on it by pane
  id, and check `buddy who <its full session id>` (D-122). On a Claude lane
  the start runs to its timeout every time, so a launcher that starts lanes
  one after another pays N x the timeout: start them in parallel (one
  background start per lane, a few seconds apart, then wait for all) or pass
  a short timeout (`herdr agent start … --timeout 10000`). Never retry a
  timed-out start: confirm each lane by its first `buddy msg` or by
  `buddy who <its full session id>`; retry only on `agent_pane_busy` (D-144).
  Pass the mode the operator decided for lanes, never a more permissive one
  than your own. Without the flag a lane starts in manual mode (D-056), and
  every tool call it makes, each `buddy msg` report included, waits for an
  approval in its pane. `claude --permission-mode auto` gives auto mode only on a
  model that has it (a model without it still asks for every call; D-114),
  so launch auto lanes on such a model. For a manual lane the operator can
  pre-allow `Bash(buddy *)`, or answer the first report "don't ask again
  for: buddy msg *": the operator's setting, never the lane's or yours to
  change. A Codex lane:
  `herdr agent start <label> --kind codex --pane <pane id> -- -s workspace-write -a on-request "You are <label>. Read <brief file> in full and follow it as your brief."`;
  its brief says to request escalation for every buddy call up front
  (Harness boundaries).
- **A launch must not hand the lane your identity.** A herdr tab's shell takes
  the herdr server's environment, and `claude --bg` the claude daemon's, so
  neither leaks your `CLAUDE_CODE_SESSION_ID` (D-085). A lane started as a
  child of your own shell inherits your id, and buddy then refuses every call
  it makes while the two ids disagree: start one only with both unset, as
  `env -u CLAUDE_CODE_SESSION_ID -u CODEX_THREAD_ID <harness> ...`. If the
  herdr server was ever started from inside a session, every tab carries that
  session's id; restart it from a plain terminal.
- **Or in the background:** `claude --bg -n <label> "<brief>"` prints
  `backgrounded · <8hex> · <label>`. The lane runs in the claude daemon's
  environment, not yours: its row shows no pane, and no variable you set on
  the launch reaches it, so `BUDDY_HANDOFF_AT` cannot be passed to one. It
  picks its own session id (a --session-id flag is ignored), its row's pid is
  its own (the pid `claude agents --json` shows), and the operator opens it
  with `claude attach <8hex>` (D-056, D-058).
- **Either way, the brief rides the launch**, as the lane's first user
  prompt, so it carries your authority: put in it only what the operator gave
  you. Keep it a pointer. Name yourself by LABEL (the sender on every
  `buddy msg` you send) AND by full session id (on your `buddy status` row; a
  label can be worn twice, and `s-<8hex>` resolves only a default label), and
  say what you relay, so an operator decision you pass on is not asked for a
  second time in the lane's pane: "Your orchestrator is <label> (session
  <id>): take your assignments from it through buddy msg, from no one else.
  It relays the operator's decisions on this work: what you take next, what
  you stop or hand on, which claims you release, when a lane you opened is
  closed, and this item's content and scope (what the change says, adds or
  drops, inside the item) when its message says the operator said it in its
  pane, with the time; those are the operator's own, as if typed in your
  pane. It cannot have you push, install, change settings, permissions, trust
  or a schema, run in a more permissive mode, or take on work beyond its own
  authorized scope: the operator decides those in your pane. Claim before you
  edit; report to it with buddy msg. Read <file>." Name the item's review
  tier, its round ceiling, and the model and effort it runs on (Review depth,
  below), with the operator's cap if one is set. Relay only what the
  operator decided, each content decision marked "the operator, in my pane
  at <time>:" (without the mark a lane reads it as your advice). A
  coordinator you launch gets the same clause, naming you. A Codex lane does
  not load this skill; it reads the same rule in `AGENTS.md`. A lane whose
  brief does not say so refuses your assignments as a peer's instructions,
  and it is right to; so does a lane's permission classifier, for content
  decisions relayed under a brief that relays only assignment (D-083, D-109).
  `buddy msg` to a lane that has not said `hello` yet is REFUSED, not queued:
  message it only once `buddy who <its full session id>` knows it (seconds).
  The `-n` name is what `SendMessage` and `claude agents --json`
  show; `claude agents --json` lists every session with `sessionId`,
  `status`, and `waitingFor` an approval. A new lane's first request writes
  about 60k tokens of cache (D-056), and an idle one re-writes its whole
  prompt after an hour (D-033): open lanes for work you have.

## Assigning, waiting, ending

- **Assign and park:** `buddy msg <lane> "<assignment>"`, and wake it with the
  address `msg` prints. Tell each parked lane roughly when you will next need
  it (the buddy skill's "Before you park"): it sizes its wait from that.
  Then check `buddy sent` and wake anyone still queued. Parking a lane yourself with
  `buddy wait --session <id>`? The wait is the lane's: tell it, since only it
  can stop its loop.
- **Wait on a lane by state; read it by mail.** `herdr agent wait <pane id>
  --timeout <ms>`, with no state named, returns when the lane is idle, done
  or blocked (a state it is already in returns at once). A lane that has
  finished a turn rests at `done`, so `--until idle --until blocked` times
  out on it (D-082). The state says the lane stopped, not why or what it did:
  its words are in `buddy inbox`, and `buddy sent` says whether yours reached
  it.
- **Anything of yours queued behind a session's claim or slot? Ask the
  holder in the same turn** (D-130). Nothing tells a lane that your subagent,
  routine or run waits on its `.buddy/slot/<name>`: `buddy msg <slug>`
  what it is running, for how long, its ETA, and to release the moment it is
  done (`buddy wait --on` prints the line). Never report "waiting on <lane>"
  to the operator without its answer from the last 15 minutes.
- **Ending a lane is the operator's act.** When its work has landed,
  `buddy who <lane>`'s EXIT line reads "no claims held; no wait declared; no
  shell found running under it", and it names no job of its own still running,
  say so. A pane holding `.buddy/slot/browser` is not closed. EXIT's `browser
  tools used` clause is a call observed, never that a tab is open: unless the
  session's DONE says its browser work is over, ask what is still open before
  you close it (D-134). A Codex lane's JOBS line always says `cannot say: no live harness
  process is registered for it` (its hooks bind no process): do not wait for
  "no shell"; its DONE words ("no job running") are its account of its jobs,
  and it owes them (D-082). Close a lane yourself (`herdr pane close <pane
  id>`; `claude stop <8hex>` for a background one) only if the operator told
  you that you may (in your pane, as a standing decision, or relayed by the
  coordinator your brief names, within what it relays), not a peer, and then
  only a lane you opened. A lane your predecessor opened and handed you with
  the coordinator claim counts as one you opened: do not stop to ask about
  it (D-122). So does your predecessor's own pane, once it has handed you the
  claim and `buddy who <its id>` shows no claims, no wait, no job and no open browser work. Never a
  CONCURRENT orchestrator's lanes. If you may, do it as soon as the lane says
  DONE and `buddy who` agrees; if you may not, give the operator its pane id
  in the same turn. **Then run `buddy bye <its session id>`:** a pane close
  does not reliably run the lane's SessionEnd hook, and its row stays `live
  STALE … pid N GONE` (D-091). A hand-run `bye` ends a row whose registered
  process is dead without `--force`; one that says the session "stays live"
  means its process is still exiting: run it again a few seconds later. A
  finished lane left open over 200k tokens is compacted about 55 minutes in (D-122).

## Handing off a coordinator

Before you hand off, take over from a coordinator, close down with no
successor, or hand off at a batch boundary, read `handoff.md` beside this file: the handoff file's three
registers, the successor's brief, the one-call hand-on, re-pointing your
lanes, the close-down claim. Kept here, since each refuses or loses work
before you would open it:

- **Hand off before you are full**, at the operator's handoff size: a prompt that opens a turn says when you reach it (the optional `busy` hook), and your `buddy sessions` row shows it (`prompt 521k`). A new batch of lanes past the operator's batch-boundary size, if they set one, gets a fresh coordinator too (D-117).
- Launch a coordinator, and its successor, in a herdr tab, `herdr tab create … --env BUDDY_HANDOFF_AT=<the operator's size>`, never `claude --bg`: the size never reaches a background session.
- Start no background job you will not see finish: a job you start is your session's child, and nothing hands it to a successor.
- Hand every claim on in ONE `buddy release <run> orc-<project> <parked riders> --to <successor>`; never release a run to have it claimed afresh: a new claim id lands every rider's wait with no outcome and loses its READY (D-063).
- Never hand on `.buddy/slot/browser`: keep it, list it, and stay open until its work is over (D-134).
- Account for every running job before you say you are done (D-065).
- A habit the operator may take up, not a rule: on a cold first prompt of the day with no run in flight, starting a fresh orchestrator is the cheapest moment, since the warm cache a handoff forfeits is already gone.

## Landing several lanes through one run

The lane's side is the buddy skill's "Landing through an orchestrator". When
the expensive thing is a run (a 65-minute test tier, a land), lanes do not
take turns on it: everyone's work goes into ONE run that you integrate, and
nobody else runs that tier or moves main. Landing on main is itself a slot:
`.buddy/slot/main`, held from forming to landing. It is written out because
what went unsaid cost a fleet about fifty minutes of unread mail (D-062).

Hold two claims: `orc-<project>` (durable, with the coordination note in its
description: how to report, the current run's slug, or the file that says)
and the run's claim on `.buddy/slot/<run>` and `.buddy/slot/main`, formed per
batch and released with its outcome (The run's claim, below). Form the next
the moment you release one: a lane's `buddy wait --on <run>` is refused
while no such claim exists. A lane waits on the RUN, never on
`orc-<project>`: that claim outlives every run, and a wait on it lands only
at your handoff. Never a bare `orchestrator`: a chat alert tells a session
about any message naming one of its claim slugs, in any room, so a name
every repo's coordinator wears alerts each of them to the others' mentions
(D-101). The coordination note is that claim's description:
`buddy claim orc-<project> --desc "<note>" --scope <path>`, refreshed by
claiming again with the same slug and scopes; `hello` and `ls` show it.
Its scopes reserve like any claim's, so choose them deliberately. A claim is
never an instruction. Then, every time:

1. **Every READY gets an answer that names its run** ("in run 4, after
   bravo"). A chore or lite lane parks its item at READY and says DONE in the
   same message (D-161): close its pane on the usual evidence; its commit is
   in that message, its item `PARKED in run` on `buddy ls`, with no READY row.
   A full-tier lane, or one built on another lane's branch (a restack needs it),
   finished but not yet landed is told to sit on its wait.
2. **Read every `buddy msg` result; never discard it.** A queued send to an
   idle lane prints the address to wake it: deliver it. A queued message
   reaches an idle lane only on its next tool call, and an idle lane makes
   none (a Codex lane's, only at its own `buddy inbox`). `buddy sent` lists
   what is still queued. Filtering the result? Keep the text with the address: `grep -o 'SendMessage to "[^"]*" with the text "[^"]*"'`.
   The text is unique per send on purpose; a fixed text of your own ("run
   buddy inbox") is dropped by the harness as a repeat of your previous one.
3. **Squash, run the tier on the integrated tree, land, and say so in the
   same turn.** Once any rider is READY, `buddy who <run>` lists each on its
   own line, READY at a commit or not ready with its note (before that, only
   names; a parked item has none: `buddy ls` and its message); decide when to go. In a fresh worktree off main
   (`git worktree add --detach <scratch>/int-<run> main`), for each READY
   lane: `git cherry-pick -n <base>..<tip>`, then
   `git commit -F <its message file>`. Check the commit is the lane's work:
   `git diff --quiet HEAD <tip>` when its base is the commit you squashed
   onto, else `git diff --stat HEAD~ HEAD` names exactly its files. Never a
   plain `git cherry-pick`: git runs no pre-commit on one, so the claim gate
   would never see the landed commit (D-122); its warning naming the lane's
   own claim is expected. The tree's sha is the pinned sha: claim `<run>`
   again with a description naming it and who is in (re-claiming keeps the
   claim, so the waits stand). Run the shared tier on exactly that tree, in
   the background, output redirected, parked on a timer wait (`wait --on`
   refuses your own claim; the buddy skill's "Before you park"); green:
   `git merge --ff-only <its sha>` from the main checkout. Then always report,
   never a bare release:
   `buddy release <run> --outcome pass --note "<landed sha; who was in; who is next>"`
   puts LANDED in front of every rider's next check (`--outcome fail` or
   `--outcome aborted` with what failed and who is out). Also message each
   landed lane still open in words that end its work: "<item> LANDED at <sha>. Release
   your claims, then you are DONE; leave your worktree, I remove it." The
   outcome says its run passed; your words end its work. Remove a landed
   lane's worktree yourself once its pane is closed (`git worktree remove
   <path>`, from the main checkout).
4. **A red that is one lane's goes to that lane**, naming the failing check;
   its pane closed, to a fresh `<item>-2` lane briefed from its READY
   message, handed the parked item (A rider's claims at READY, below).
   Keep the run's claim, re-claimed "RED, ejecting <item>, one more tier", run
   again and then release with the outcome; or release it now with the
   outcome fail and the attribution in the note (riders: A rider's claims at READY, below). The lane fixes, re-declares,
   and rides the next run. The integrated tree's tier is the one that counts:
   a lane's own green does not carry over (D-122). A red whose cause predates
   the lane still goes to it, as a SEPARATE commit with its own message file
   and tier, landed first: pick the fix's commit alone, then the rest of the
   range.

   **A job killed under a run is a red nobody reported (D-131).** Declare the
   tier as it starts, re-claiming the run:
   `buddy claim <run> --desc <its note> --scope <every scope it holds> --job <pid>`
   (a re-claim replaces the note and the scopes; `buddy who <run>` prints the
   scopes). Name the tier's own process and never a wrapper's: a wrapper that
   outlives its tier and keeps printing fools the declaration. Under a Bash tool
   call, JOBS's pid is the wrapping shell: `pgrep -P <that pid>` is the tier, and
   `ps -p <pid> -o lstart= -o command=` confirms it. `buddy who <run>` then prints
   a JOB line: alive; GONE (the process ended, result unknown; a finished job
   reads this too); or CANNOT SAY (no identity readable, as on a platform that
   reads none: check by hand with that `ps`, and note the start). A re-claim
   naming a job replaces the declaration, one naming none keeps it, and handing
   the claim on carries it. A pid not running when declared is granted and reads
   GONE: the result line says so, so check the pid you typed. No reading releases
   or refuses anything, riders keep waiting, and JOBS shows what is present when
   sampled, never what died. So check on a schedule, whatever your cache tier, as
   nothing else looks. Only two things tell you anything: a tier's final status
   line, the one it prints last, only on reaching its end (its GREEN summary, or
   the like), which is the result, only for the invocation you declared; and a
   declared job reading GONE, which says only that it ended, so look for that
   line. With it: it finished. Without it: a FAILED run, not a slow one. Output
   growing or flat says neither, since a test runner may print only as each
   package ends. Riders hear a failed run in that same turn.
   Rerunning now with the same riders: keep the claim, re-claimed "RED, job
   killed, one more tier" naming the new job (waits and READYs stand),
   and `buddy msg` each rider, since a re-claim tells nobody; look first at what
   else is running, as whatever killed it may still be there. Otherwise (no
   rerun starting this turn, or the riders change):
   `buddy release <run> --outcome aborted --note "<what happened, who reruns>"`,
   and form the next run.
5. **After each landing, tell every lane still working the new main**, so
   any rebase it needs goes onto that, not onto the tree it started from. A
   READY lane built on ANOTHER lane's branch, whose fold has since moved it,
   you can restack yourself while it is parked (Restacking a dependent
   lane, below).
6. **Never hold new lanes for a run in flight.** The next round may branch
   from the run's pinned tree, or a READY lane's tip, instead of main; the
   brief names that base, and the lane's READY repeats it. Step 3 squashes
   each lane's own `<base>..<tip>`, so a commit on the run's tip that touches
   only docs or ledgers (its batch record) never forces a restack; that
   lane's check is then `git diff --stat HEAD~ HEAD`, its base not being
   the commit you squashed onto. A code change under a lane does: an item ejected on a red,
   or a fold. Then restack its dependents (tell a lane still working to
   rebase itself) and land none of them before each has re-reported on its
   new tip. A restacked lane's base is then the tree it now sits on: its
   READY names that, and the next restack or squash starts from it (the
   brief's base would carry the fold's commits along) (D-119). A rider's claim
   on the path must not hold them either: have that lane park it (A rider's
   claims at READY, below).
7. **Before you hand off or exit, sweep your assignments**: `buddy who` each
   lane you assigned, and confirm it is working on something named or has
   been told it is done. Everything unfinished goes in the handoff note. Read
   each lane's `prompt` on `buddy sessions` too, and tell the operator which
   lanes are at or past the handoff size. Where the repo ships a census of
   processes outliving their sessions, run it here and after each batch lands;
   a STALE one goes in the note or to the operator, by pid, unless your
   operator's decisions say you reap.
8. **Pick who does a piece by what it ends in.** A commit that lands: a lane
   (it claims, reports READY, lands through the run). A finding (a lane's diff
   read, a report, a measurement, a CI log, deleting merged branches, a second
   opinion through the repo's review script): a subagent, the Agent tool, on
   a model your operator's decisions allow for side work (a model they keep
   for reviews only for a review, never by inheritance from your own); its
   finding is evidence once a test reproduces it. Name the model on every
   such call, review or side work: the call's `model` takes a family alias
   (`sonnet`, `opus`, `haiku`, `fable`), a general-purpose call with none
   runs on YOUR model (a hook the operator may wire refuses it), and a
   lane's receipt names its panel's (`panel@<model>`) (D-117). A handful of
   tool calls (a `buddy who`, a message, the landing itself): you. Your
   context is the scarce resource before your handoff size; a subagent's is
   not. A subagent is not a lane: it holds no claim, lands nothing, relays nothing,
   and is never messaged as one. It works under YOUR session id and YOUR
   claims, so claim the files before you hand it an edit (D-103).
9. **Name each item's review tier, round ceiling and model in its brief**
   (Review depth, below): the tier by the change's class, under the
   operator's cap for the run, the ceiling on review rounds, and the model and effort the lane launches on, with the claim
   description's two tokens (Picking the model, below). A lane's READY
   carries its receipt; read it against the tier you gave. A code item that
   rests on research names it by path and version word (`<path> as of
   <commit>`; outside git, `as of <time>, shasum <8hex>`), so a successor
   reads the same text; a changed note is a new brief line, never a silent
   update.
10. **A lane at its handoff size with work left goes to a fresh session**,
    never kept going: every turn re-reads its whole prompt (D-114, D-122).
    Open `<item>-2` in the same worktree, briefed from the lane's state note;
    the lane hands it its claims with `release --to` and says DONE; then
    close it (Ending a lane).

A lane with a stale claim and no answer stays the operator's to free
(the refusal names how): report it, never free it yourself.

### The run's claim

You are the run's integrator (any session can be, for a run of its own). Form
it with `buddy claim herm --desc "FORMING — join: buddy wait --on herm --ready HEAD" --scope .buddy/slot/herm --scope .buddy/slot/main`,
hold it from forming to landing, read its riders with `buddy who herm` (a
parked item has no READY row there: it shows on `buddy ls`), and run it as step
3 says.

- **Handing the run to another integrator mid-flight?** `buddy release herm --to
  <session>` keeps it open, with its riders' waits; name several claims in one
  call (`buddy release herm orc-<project> <parked riders> --to <session>`) and they
  move as one transaction, with one notice, the riders still parked. A successor already waiting on the run is
  handed it and its wait LANDS, "handed to you".
- **A rider not done yet** declares `buddy wait --on herm --note "done in 10" --until 4h`
  (its side is the buddy skill's); its ETA is its own words, and nothing turns
  it into OVERDUE. By convention, joining while the description says RUNNING
  puts it on the NEXT run. Buddy keeps no timer and ejects nobody: a quiet
  rider is shown quiet, and going without it is the integrator's call.

### A rider's claims at READY

After READY a rider's claim refuses like any reservation, though its work is in
the run. It does two jobs, the path and the outcome; parked, it keeps both while a
lane on the run's tree works beside it (D-133):

- **A full-tier lane waiting at READY, pane open,** keeps its claims and wait, and its claim
  does not yield.
- **A chore or lite lane parks the item at READY** (the default, D-161) and commits
  nothing after:
  `buddy release <item> --to orc-<project> --in-run <run>`, where the recipient
  holds that open run (a run is a claim holding `.buddy/slot/<its slug>`). Its other
  claims, with no outcome on them, go the way you say: parked too if the run holds
  those paths, else released plain. Never have it release the item's own claim at
  READY: a red then finds no holder and no claim for the outcome.
- **A new lane branched from the run's tree** claims, not shared, with
  `buddy claim <slug> --based-on <run> --scope ...`, and only a parked rider of
  that run yields to it: an active peer, a slot, a slug and a rider of another
  run still refuse. Its gate and commit gate read that claim, so it needs one
  covering each path it edits, and a re-claim says `--based-on` again. Put the
  flag in the brief of a lane you branched there, and no other.
- **Nothing checks a declaration.** It names the run, not a commit. A lane at an
  older tree that copies the flag, or one you branched from a READY lane's tip or
  from a tree without this rider's commit, is admitted over work its tree lacks, and `git
  cherry-pick -n` merges disjoint hunks without complaint (measured on one file), so
  only the tier can catch it. The base you name in a brief is the one guard: restack
  such a lane (step 6). A rider you leave out of the run: `buddy release <rider>
  --unpark`, and `--in-run <next run>` once that run's tree holds its commit.
- **`buddy ls` shows it:** `PARKED in run <run>` (`in CLOSED run` once it closed,
  refusing like any claim), `produced by <label>` (who the model report credits),
  `BASED ON run <run>`.
- **At landing** release each parked item with its own outcome, BEFORE the run:
  `buddy release <rider> --outcome pass --note "reds=<n> <sha>"`. A plain release, or the
  run's alone, reports nothing for it, and a run closed first denies its admitted
  lanes even on a green. Your own `Edit` on a path such a lane holds is denied too,
  and your commit there meets the commit gate: have it narrow or hand over first.
- **On a red, or ejecting one rider while the run is open, you act before anyone
  repairs.** `buddy release <rider> --unpark` withdraws one in place, and the run's
  release all of them; a plain `buddy release <rider> --to <item>-2` also withdraws
  it, and is refused while a lane holds the path. The ledger revokes nothing: an
  admitted lane's edits on the path are DENIED at the gate, and your repair is
  refused by its claim. So: `buddy ls` for each `BASED ON` claim on the rider's
  paths, tell that lane to narrow off the path or hand you the claim, then assign
  the repair (a `buddy pause` would deny it its own `buddy release` too). Narrowing
  names the exact scope it holds (`buddy release <slug> --scope <scope>`), so a
  directory over the rider's file hands over instead, and a last scope closes the
  claim with no outcome. A claim handed to you still overlaps the rider: narrow or
  release it before a `--to` of the rider to a fixer. A lane that needs the fix
  waits and restacks (step 6). A closed run's riders refuse again: once each
  commit is in your next run's tree, `buddy release <riders> --in-run <run>`
  re-parks them; a claim `overlap stands` names gets no yield until re-declared.
- **Handing the run on:** name the run and its riders in ONE call (above). A plain
  `--to` keeps a rider parked only if the recipient holds its run once the set has
  moved, so one sent ahead of its run, or to a fixer, is withdrawn.

## Review depth: the tier follows the change class

What each tier owes a lane, the review script's foreground rule, the round
ceiling's stop, the receipt and "a review is never test evidence" are the
buddy skill's "Your review"; this section is what the orchestrator adds. The
tier is picked per ITEM by what the change touches: never by which model
wrote it, never by how small the diff looks. You name it in the lane's brief;
the lane names what actually ran in its commit's receipt. The repo's own
CLAUDE.md says which change classes take which tier (typically full for a
security boundary, what a gate refuses or allows, concurrency, a schema,
stored data mutated or deleted, or an invariant) and names its review
script; a repo whose CLAUDE.md names none: ask the operator before you run
one.

- **full**, beyond "Your review": the design pass runs where the repo asks
  for one; the panel needs no asking, since the tier is the authorization;
  the second model's code pass runs at a named effort through the repo's
  review script; the cross-check is given the diff and the same questions
  and asked for exact line -> issue.
- **lite**: a cross-check is optional, the lane's or the brief's call.
- **The tier says when a second model is REQUIRED, never when one is
  allowed.** Use one beyond what the tier owes (on a chore, or a second
  pass on a lite item) whenever a second reading is worth it, and name it in
  the receipt: reviewers of one family can share its blind spots (D-104).
- **A test-only change takes its class's tier.** A test of a gate is gate
  work: its assertions are what a review reads, and the person who wrote a
  weak assertion is the one least likely to see it.
- **The receipt** names every layer that ran, each second-model pass with its
  own effort and the cross-check with its model,
  `review: <tier> (<layer> + <model>@<effort> <pass> + ...; <scope note>)`, for example
  `review: full (panel + <second>@<effort> design + <second>@<effort> code + <third model> + <second>@<effort> reverify)`.
  A layer the tier owes is skipped only on the operator's word (the cap), and
  the receipt then names it with the reason; a lane never skips one on its
  own (D-104). On a lite or full receipt, each panel, cross-check and
  second-model layer owes its counts, as the core skill teaches
  (`: 3 raised, 1 reproduced`; D-163): a brief says so, and the repo's
  receipt check reports a layer without them. One not written is not
  recorded, never zero.
- **The operator caps the tier per run** (for example "lite at most,
  optional, no second model family"), and the brief carries the cap. A lane
  never raises its own tier above it, and never lowers the brief's.

**Every brief names a round ceiling with its tier (D-115).** The default,
which the operator may raise per item and nobody else may:

| tier | rounds |
|---|---|
| lite | one second-model code pass, folded once |
| full | a design pass, a code pass, then at most TWO re-verify rounds |
| chore | none |

At the ceiling the lane folds what it can fold narrowly, answers the rest
and records it open (in the decision record or the note the brief names),
and reports READY. Rounds are where the tokens go, not landing, and on gate
work every fold can regress what an earlier round
accepted (D-115). Brief a lane on gate work to fold narrowly: fix the exact
form found, test it, and record a crafted respelling open rather than widen
a recognizer to catch it.

An item that is not code takes the same tiers, by what it changes (its
`class=` word, Picking the model, below). The receipt ends the commit, or
the note when nothing is committed:

| what changes | tier | the review |
|---|---|---|
| docs: wording, a fact restated | chore | none; the repo's doc tests still run |
| docs: a rule or recipe sessions follow | lite | the second model is asked whether any rule lets a session land untested work, rewrite another's commits or take an operator-only action |
| research a code item will rest on | lite; for a full item, its design pass | the second model reads the method and every figure, each naming what it was measured over and how to re-run it |
| research nothing rests on yet | chore | none: a lead until a test or a re-measurement backs it |
| ops: a script run, a report, cleanup | chore | none; the report names the command, its exit status and the line that says it worked |

**Every lane runs on the operator's default lane model** (a standing
decision in their instructions), unless the operator names another model for
the item or sets a run's cap the pick rule below works within. The tier
changes the REVIEW, never the model: a full-tier item's lane, its panel and
its fold rounds run on that default too. A model the operator keeps for
reviews (a cross-check, a panel lens on a security-sensitive commit, a
judgement-call ruling) is never a lane's model unless the operator named
that item for it (D-104, D-111).

### Picking the model from what past items measured

At the start of each run, before you pick any item's model other than the
default by what past items measured, read `model-pick.md` beside this file.
Kept here:

- A full-tier item stays on the default lane model, and the pick never lands on a model kept for reviews.
- Every item's claim description begins `model=<id> class=<word> tier=<tier> `, `<word>` one of `docs`, `code`, `test`, `research`, `ops`, `<tier>` one of `chore`, `lite`, `full`: the brief gives the lane that exact start.
- Before each `buddy sweep`, run `sh scripts/model-report.sh --export <tsv>`: a plain sweep deletes released claims, with their outcomes, after 24 hours.
- A finished or abandoned item is released with its outcome, `buddy release <item> --outcome pass|fail --note "reds=<n> <what>"`, never bare; a handover is `buddy release <item> --to <target>`.

## Restacking a dependent lane (gh stack)

Before you restack a lane (B built on lane A's branch, and A's fold moved
A), read `restack.md` beside this file: what gh stack does as measured, the
six-step recipe, the plain-git line. Kept here:

- Never `gh stack` from a lane: the coordinator restacks, one at a time, holding `.buddy/slot/gh-stack` (the gate refuses gh stack's rewriting subcommands without it; D-108), and only the recipe's four commands, network denied: a plain `gh stack rebase` fetches origin and can move local main.
- Never `gh stack rebase --remote self`, never `gh stack alias`, never a `gs` wrapper.
- Restack only while both lanes are READY and `idle`; never resolve a conflict in a lane's worktree: abort and tell the lane.
- Nothing lands until every restacked lane has re-reported READY on its new tip.

## When the deliverable is not code

When a lane's deliverable is notes, a report, a runbook or configuration,
read `not-code.md` beside this file: how commit, READY, the shared tier and
main translate. Kept here:

- The directory must be a git repository with `buddy init` run: outside one every verb answers "not a git repository".
- A deliverable written through an MCP tool is not gated: claim it anyway.

## Codex lanes and adapters

Before you brief a Codex worker or an adapter launch, or run
`buddy diagnose`, read `codex-lanes.md` beside this file. Kept here:
`buddy diagnose` exit 0 means a report was produced, never readiness.

## A fleet of fleets

Before you coordinate other coordinators (one session over several repos'
fleets), read `fleet-of-fleets.md` beside this file. Kept here:

- Send every message in the RECIPIENT's home ledger, and nothing TO a foreign registration: no hook of its session drains mail there.
- `pause` in a coordinated ledger only on the operator's word.

## Upgrading buddy itself

`buddy update` (an install: the buddy skill's rule) reinstalls from the checkout the installed `buddy` was built
from. It first says where that checkout stands (HEAD, branch, uncommitted
changes, against main and origin/main as of the last fetch: it never fetches),
then runs
that checkout's `scripts/install.sh`; `buddy update --dry-run` reports and
installs nothing. Its `chat` line is the posture the install keeps (D-124):
a claims-only machine stays one unless a run says `BUDDY_CHAT=on`. An
orchestrator runs it only where it already runs `install.sh` after a landing
(D-110).

`buddy uninstall` runs that checkout's `scripts/uninstall.sh`: the chat
daemon booted out through launchd, the binaries, the skills, buddy last,
then the hook lines, MCP registrations and oscar agent provably for what it
removed (D-173); it deletes no ledger or journal, signals no process, and
names what it leaves.
`buddy uninstall --chat` removes the chat half only, rebuilding buddy
claims-only first (`BUDDY_CHAT=off buddy update`) so a later update keeps
chat off; `--dry-run` prints the plan. The operator's act, never relayed
(D-125).
