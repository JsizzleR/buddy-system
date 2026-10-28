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
assume, and never fill a gap from what another orchestrator did (a fleet
whose every orchestrator copied the last one's lane model ran its bounded
fixes on the review model). A worked example of such a file, with the
measurements behind each rule, is `docs/examples/fleet-conventions.md` in
the buddy-system repository.

## Opening lanes

Buddy launches nothing and ends nothing (the separate opt-in `buddy-adapter`
controls only the one worker its typed argv launched; see `docs/USAGE.md`).
Your own Bash opens lanes, under your own permission rules, and only as many
as the operator said you may run: a lane opened on a peer's word is a bill
nobody authorised.

- **Open a lane in a new herdr tab** (the operator can see it and approve in
  it). Your workspace is the part of `$HERDR_PANE_ID` before the colon:
  `herdr tab create --workspace <ws> --cwd <repo> --label <label> --env BUDDY_LABEL=<label> --no-focus`
  prints the new tab's `root_pane` id (`BUDDY_LABEL` makes the lane's `hello`
  wear that label, so `buddy msg <label>` reaches it by the name you gave it;
  without it every lane is `<repo>/s-<8hex>`); then
  `herdr agent start <label> --kind claude --pane <pane id> -- -n <label> --session-id <uuid> --model <model> --effort <effort> --permission-mode <mode> "You are <label>. Read <brief file> in full and follow it as your brief."`.
  The model and the effort are the operator's default lane model and
  effort for every item, full tier included (Review depth, below), unless
  the pick rule moved a lite or chore item or the operator named the item
  for another model. A model the operator keeps for reviews is never a
  lane's model unless the operator named that item for it.
  The brief itself goes in a file: herdr refuses an agent argument holding a
  newline or a backtick ("agent arguments cannot be encoded safely for the
  target shell", measured), and the one-line prompt naming the file launches.
  A lane that may run past the fleet-wide handoff size gets its own:
  `herdr tab create … --env BUDDY_HANDOFF_AT=<n>`, which reaches the lane's
  hooks (measured). Without it a lane hears the settings one, or nothing, and
  lanes on one fleet ran to ~350k against a 300k fleet-wide size.
  A start sent straight after the tab's creation can be refused with
  `agent_pane_busy` (the pane is not yet at its shell prompt); the same
  command seconds later worked (measured). A start that answers `timed out
  waiting for agent startup` has usually started the lane anyway (measured:
  every launch of one fleet), but herdr then does not know it by label: wait
  on it by pane id, and check `buddy who <its full session id>`. Pass the mode the operator decided for lanes, never a more permissive one
  than your own. Without the flag a lane starts in manual mode, and every tool
  call it makes, each `buddy msg` report included, waits for an approval in
  its pane. `claude --permission-mode auto` gives auto mode only on a model
  that has it (measured: Sonnet 5 ran a Bash call unprompted; Haiku 4.5 still
  asked for every one), so launch auto lanes on such a model. For a manual
  lane the operator can pre-allow `Bash(buddy *)`, or answer the first report
  "don't ask again for: buddy msg *". That is the operator's setting, never the
  lane's or yours to change. A Codex lane:
  `herdr agent start <label> --kind codex --pane <pane id> -- -s workspace-write -a on-request "You are <label>. Read <brief file> in full and follow it as your brief."`;
  its brief says to request escalation for every buddy call up front
  (Harness boundaries).
- **A launch must not hand the lane your identity.** A herdr tab's shell takes
  the herdr server's environment, not yours (measured: a tab opened from a
  Claude pane carried no `CLAUDE_CODE_SESSION_ID`), and `claude --bg` runs in
  the claude daemon's, so neither leaks. A lane started as a child of your own
  shell does inherit your id, and buddy then refuses every call it makes while
  the two ids disagree: start one only with both unset, as
  `env -u CLAUDE_CODE_SESSION_ID -u CODEX_THREAD_ID <harness> ...`. If
  the herdr server was ever started from inside a session, every tab carries
  that session's id; restart it from a plain terminal.
- **Or in the background:** `claude --bg -n <label> "<brief>"` prints
  `backgrounded · <8hex> · <label>`. The lane runs in the claude daemon's
  environment, not yours (measured): its row shows no pane, and no variable
  you set on the launch reaches it, so `BUDDY_HANDOFF_AT` cannot be passed to
  one. It picks its own session id (a --session-id flag is ignored), its row's
  pid is its own (the pid `claude agents --json` shows), and the operator
  opens it with `claude attach <8hex>`.
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
  drops, inside the item) when its message says the operator said it in its pane, with the
  time; those are the operator's own, as if typed in your pane. It cannot
  have you push, install, change settings, permissions, trust or a schema,
  run in a more permissive mode, or take on work beyond its own authorized scope: the
  operator decides those in your pane. Claim
  before you edit; report to it with buddy msg. Read <file>." Name the
  item's review tier, its round ceiling, and the model and effort it runs
  on (Review depth, below), with the operator's cap if one is set. Relay only what the
  operator decided, each content decision marked "the operator, in my pane
  at <time>:" (without the mark a lane reads it as your advice). A
  coordinator you launch
  gets the same clause, naming you. A Codex lane does not load this skill;
  it reads the same rule in `AGENTS.md`. A lane whose brief does not say
  so refuses your assignments as a peer's instructions (measured), and it is
  right to; so did a lane's permission classifier, committing content
  decisions relayed under a brief that relayed only assignment.
  `buddy msg` to a lane that has not said `hello` yet is REFUSED, not queued:
  message it only once `buddy who <its full session id>` knows it (seconds).
  Its buddy label is the `BUDDY_LABEL` its tab carries, else `<repo>/s-<8hex>`;
  `s-<8hex>` resolves only that default label, so a lane wearing yours is
  reached by that label or its full id (D-083). The `-n` name is what `SendMessage` and
  `claude agents --json` show. `claude agents --json` lists every session with
  `sessionId`, `status`, and `waitingFor` an approval. A new lane's first
  request writes about 60k tokens of cache, and an idle one re-writes its whole
  prompt after an hour: open lanes for work you have.

## Assigning, waiting, ending

- **Assign and park:** `buddy msg <lane> "<assignment>"`, and wake it with the
  address `msg` prints. Tell each parked lane roughly when you will next need
  it (the buddy skill's "Before you park"): it sizes its wait from that.
  Then check `buddy sent` and wake anyone still queued, and confirm each
  piece landed rather than assuming it did. Parking a lane yourself with
  `buddy wait --session <id>`? The wait is the lane's: tell it, since only it
  can stop its loop. From there, your side of the loop is Landing
  several lanes through one run (below).
- **Wait on a lane by state; read it by mail.** `herdr agent wait <pane id>
  --timeout <ms>`, with no state named, returns when the lane is idle, done
  or blocked (its help says so; a state it is already in returns at once).
  A lane that has finished a turn rests at `done`, so
  `herdr agent wait <pane id> --until idle --until blocked` times out on it
  (measured: three lanes, ten minutes each, while their READY reports sat in
  the inbox). The state says the lane stopped,
  not why or what it did: its words are in `buddy inbox`, and `buddy sent`
  says whether yours reached it.
- **Ending a lane is the operator's act.** When its work has landed,
  `buddy who <lane>`'s EXIT line reads "no claims held; no wait declared; no
  shell found running under it", and it names no job of its own still running,
  say so. A Codex lane's
  JOBS line says `cannot say: no live harness process is registered for it`,
  always: its hooks bind no process, so the line can never say "no shell" for
  it. Do not wait for one; its DONE words ("no job running") are its account
  of its jobs, and it owes them. Close a lane yourself
  (`herdr pane close <pane id>`; `claude stop <8hex>` for a
  background one) only if the operator told you that you may (in your pane,
  as a standing decision, or relayed by the coordinator your brief names,
  within what it relays), not a peer, and then only a lane you opened. A lane
  your predecessor opened and handed you with the coordinator claim counts
  as one you opened: do not stop to ask about it (one fleet sat eight hours
  on that question). So does your predecessor's own pane, once it has handed
  you the claim and `buddy who <its id>` shows no claims, no wait and no job.
  Never a CONCURRENT orchestrator's lanes. If you may, do it as soon as the lane
  says DONE and `buddy who` agrees; if you may not, give the operator its
  pane id in the same turn. **Then run `buddy bye <its session id>`.** A pane
  close does not reliably run the lane's SessionEnd hook: measured on one
  fleet, 68 of 119 closed lanes never ran it (no `bye`, no presence), and
  their rows stayed `live STALE … pid N GONE`. A hand-run `bye` ends a row
  whose registered process is dead without `--force`; one that says the
  session "stays live" means its process is still exiting: run it again a
  few seconds later. A finished lane left open still costs: about 55
  minutes into its idle time the harness compacts any session over 200k
  tokens, before its cache expires. Measured on one fleet over five days:
  12 such compactions of 312k to 583k, and 6 were in lanes that had already
  said they were done and never ran again.

## Handing off a coordinator

- **Hand off before you are full.** Launch coordinators, and their successors,
  in a herdr tab with `herdr tab create … --env BUDDY_HANDOFF_AT=<the operator's size>` (or the
  operator sets it for every session in the settings `env` block, which reaches
  the first orchestrator too), never with `claude --bg`: a background lane runs in the
  daemon's environment, and the size never reaches it. A
  prompt that opens a turn tells you once when your last observed prompt
  reaches it, and again at each 25k past it, not every turn (D-096; the
  optional `busy` hook must be wired, and a size must have been observed); your row in `buddy sessions` shows it any time (`prompt 521k`). Then:
  1. Finish the round. Start nothing new, and no background job you will not
     see finish: a job you start is a child of YOUR session, and nothing hands
     it to your successor. A watcher on anything long (a remote run, a nightly)
     writes its result to a file; one that only echoes reports to a session
     about to close.
  2. Write a handoff file, where your repo keeps them. What the ledger cannot
     hold goes first, under these headings, each present even when it says
     "none":
     - **Running jobs:** every background job of yours still running: its
       command and pid, the file its output lands in, the line that says it
       finished, and who reads it next.
     - **Timers and external runs:** whatever finishes or fires with nobody
       here watching (a nightly, a remote grader): when, and how to read it.
     - **Owed:** what you promised the operator or a lane and have not delivered.

     Then the role, the loop you run, the traps you measured, each lane's state
     and what is next, and the ledger as it stands: paste `buddy status`,
     `buddy sent`, and `buddy who <run>` for each run you hold. Point your
     coordination claim at it, re-claiming with the SAME scopes you hold (a re-claim replaces
     them, and `buddy who <slug>` prints every one, uncut), repeating the scope flag once for
     each path you hold, never the handoff file in their place:
     `buddy claim orc-<project> --desc "HANDOFF: read <file>" --scope <each path you hold>`.
  3. Open the successor the same way, in a herdr tab carrying the same size
     (never in the background, where it would not reach it), with the brief "You are the next orchestrator, taking over from <your
     label>. Read <file>, then buddy inbox. Check that you can read the output
     of every running job it lists, then say so. I will hand you my claims."
     The successor does not `buddy wait --on` a claim it is about to be handed
     (a `--until` timer keeps it warm instead): a recipient's wait on the
     claim can refuse the transfer.
  4. `buddy msg` it anything since the file, and wake it. When it answers,
     hand it every claim you hold in ONE call (all or nothing, one notice):
     `buddy release <run> orc-<project> --to <its label>`. Each claim stays open with
     its scopes, and every wait on it follows it: riders keep waiting, stay
     READY on `buddy who <run>`, and land on the successor's outcome. The
     result line tells the successor with one message and ends with the address
     to wake it: deliver it. It refreshes each description by claiming again
     with the SAME scopes. Never release a run and have it claimed afresh: a
     new claim is a new claim id, so every rider's wait lands with no outcome
     and its READY is lost (measured).
     Then re-point your lanes: their briefs name YOU, by label and session id,
     as the coordinator whose relays they take, and nothing in the ledger
     changes that. While you are still live, `buddy msg` each lane you
     launched or inherited: "<successor label> (session <full id>) is your
     coordinator from now; it relays the same decisions I did." The message
     carries authority because the sender is the id the brief names.
  5. **Account for every running job before you say you are done:** finished,
     handed over (its output file is in the handoff and the successor says it
     can read it), or stopped. `buddy status`'s EXIT line speaks for the LEDGER
     only: "holding nothing" says nothing about your processes, and closing your
     pane may end them. Its JOBS line counts the shells still running directly
     under your harness process; it cannot see a job that exec'd into another
     program, so it checks your list, never replaces it. Measured in one handoff: the outgoing orchestrator said
     it held nothing while an 8-minute-old gate script and a 42-minute watcher
     still ran as its children, the watcher reporting only to it. Then tell the
     operator you are done, naming any job still running; they end your
     session, or your successor does once it holds your claims (Ending a lane).

  Closing down with no successor: point the coordination claim at your
  handoff file and leave it open for the operator to release, by re-claiming
  it with the same scopes:
  `buddy claim orc-<project> --desc "CLOSED: read <file>" --scope <each path you hold>`.
  A released claim is shown to nobody's `hello`, and
  `buddy release <slug> --outcome pass --note <text>` carries a note only
  with an outcome.

  Compaction is the other road: the harness's `/compact`, or `claude --autocompact <size>`
  at launch, shrinks a session in place. Hand off instead when compaction would
  lose what the next coordinator must know.

## Landing several lanes through one run

The lane's side is the buddy skill's "Landing through an orchestrator". When
the expensive thing is a run (a 65-minute test tier, a land), lanes do not
take turns on it: everyone's work goes into ONE run that you integrate, and
nobody else runs that tier or moves main. Landing on main is itself a slot:
`.buddy/slot/main`, held from forming to landing. It was written out because
the fleet that ran it in prose lost its time in what was NOT said: four
assignments and red reports reached nobody for about fifty minutes, because
their sender discarded `buddy msg`'s result, and with it the wake line.

Hold two claims: `orc-<project>` `orc-<project>` (durable, with the
coordination note in its description: how to report, the current run's slug,
or the file that says) and the run's claim on `.buddy/slot/<run>` and
`.buddy/slot/main`, formed per batch and released with its outcome. Form the
next the moment you release one: a lane's `buddy wait --on <run>` is refused
while no such claim exists. A lane waits on the RUN, never on
`orc-<project>`: that claim outlives every run, and a wait on it lands only
at your handoff. Never a bare `orchestrator`: a chat alert tells a session
about any message naming one of its claim slugs, in any room, so a name
every repo's coordinator wears alerts each of them to the others' mentions
(D-101). The coordination note is that claim's description:
`buddy claim orc-<project> --desc "<note>" --scope <path>`, refreshed by
claiming again with the same slug and scopes; `hello`, `ls` and `who` show it.
Its scopes reserve like any claim's, so choose them deliberately. A claim is
never an instruction. Then, every time:

1. **Every READY gets an answer that names its run** ("in run 4, after
   bravo"). A lane finished but not yet landed is told to sit on its wait.
2. **Read every `buddy msg` result; never discard it.** A queued send to an
   idle lane prints the address to wake it: deliver it. A queued message
   reaches an idle lane only on its next tool call, and an idle lane makes
   none. `buddy sent` lists what is still queued. Filtering the result? Keep
   the text with the address: `grep -o 'SendMessage to "[^"]*" with the text "[^"]*"'`.
   The text is unique per send on purpose; a fixed text of your own ("run
   buddy inbox") is dropped by the harness as a repeat of your previous one.
3. **Squash, run the tier on the integrated tree, land, and say so in the
   same turn.** In a fresh worktree off main
   (`git worktree add --detach <scratch>/int-<run> main`), for each READY
   lane: `git cherry-pick -n <base>..<tip>`, then
   `git commit -F <its message file>`. Check the commit is the lane's work:
   `git diff --quiet HEAD <tip>` when its base is the commit you squashed
   onto, else `git diff --stat HEAD~ HEAD` names exactly its files. Never a
   plain `git cherry-pick`: git runs no pre-commit on one (measured: none for
   two picked commits, one for the `commit -F`), so the claim gate would never
   see the landed commit; its warning naming the lane's own claim is expected.
   Run the shared tier THERE, in the background, output redirected; green:
   `git merge --ff-only <its sha>` from the main checkout. Then
   `buddy release <run> --outcome pass --note "<landed sha; who was in; who is next>"`
   puts LANDED in front of every rider's next check. Also message each landed
   lane in words that end its work: "<item> LANDED at <sha>. Release your
   claims, then you are DONE; leave your worktree, I remove it." The outcome
   says its run passed; your words end its work. Remove a landed lane's
   worktree yourself once its pane is closed (`git worktree remove <path>`,
   from the main checkout).
4. **A red that is one lane's goes to that lane**, naming the failing check.
   Keep the run's claim, re-claimed "RED, ejecting <item>, one more tier", or
   release it with the outcome fail and the attribution in the note (One long
   run). The lane fixes, re-declares, and rides the next run. The integrated
   tree's tier is the one that counts: a lane's own green does not carry over
   (measured: a lane green on its branch went red squashed, under `-race`, on
   a race older than the lane that its change widened). A red whose cause
   predates the lane still goes to it, as a SEPARATE commit with its own
   message file and tier, landed first: pick the fix's commit alone, then
   the rest of the range.
5. **After each landing, tell every lane still working the new main**, so it
   rebases onto that and not onto the tree it started from. A READY lane
   built on ANOTHER lane's branch, whose fold has since moved it, you can
   restack yourself while it is parked (Restacking a dependent lane, below).
6. **Before you hand off or exit, sweep your assignments**: `buddy who` each
   lane you assigned, and confirm it is working on something named or has
   been told it is done. Everything unfinished goes in the handoff note. Read
   each lane's `prompt` on `buddy sessions` too, and tell the operator which
   lanes are at or past the handoff size.
7. **Pick who does a piece by what it ends in.** A commit that lands: a lane
   (it claims, reports READY, lands through the run). A finding (a lane's diff
   read, a report, a measurement, a CI log, deleting merged branches, a second
   opinion through the repo's review script): a subagent, the Agent tool, on
   a model your operator's decisions allow for side work (a model they keep
   for reviews only for a review, never by inheritance from your own); its
   finding is evidence once a test reproduces it. A handful of tool calls (a
   `buddy who`, a message, the landing itself): you. Your context is the
   scarce resource before your handoff size; a subagent's is not (two
   orchestrators in a row handed off at 322k and 397k, much of it side work
   done in their own panes).
   A subagent is not a lane: it holds no claim, lands nothing, relays nothing,
   and is never messaged as one. It works under YOUR session id and YOUR
   claims, so claim the files before you hand it an edit (D-103).
8. **Name each item's review tier, round ceiling and model in its brief**
   (Review depth, below): the tier by the change's class, under the
   operator's cap for the run, the ceiling on review rounds, and the model and effort the lane launches on, with the claim
   description's two tokens (Picking the model, below). A lane's READY
   carries its receipt; read it against the tier you gave. A code item that
   rests on research names it by path and version word (`<path> as of
   <commit>`; outside git, `as of <time>, shasum <8hex>`), so a successor
   reads the same text; a changed note is a new brief line, never a silent
   update.
9. **A lane at its handoff size with work left goes to a fresh session**,
   never kept going: every turn re-reads its whole prompt (one lane kept on
   went from 402k to about 710k over four review rounds). Open `<item>-2` in
   the same worktree, briefed from the lane's state note; the lane hands it
   its claims with `release --to` and says DONE; then close it (Ending a
   lane).

A lane with a stale claim and no answer stays the operator's to free
(`buddy sweep --force`): report it, never free it yourself.

### The run's claim

You are the run's integrator (any session can be, for a run of its own):

- **Integrator:** hold the run from forming to landing, on its slot AND on main:
  `buddy claim herm --desc "FORMING — join: buddy wait --on herm --ready HEAD" --scope .buddy/slot/herm --scope .buddy/slot/main`.
  Once any rider is READY, `buddy who herm` lists each on its own line: READY at a
  commit, or not ready with its note (before that, only names). Decide when to go.
  Build ONE tree off main, each READY lane squashed onto it in turn (step 3
  above); its sha is the pinned sha. Claim `herm` again with a description naming that sha and who is
  in (re-claiming keeps the claim, so the waits stand). Run the tier on exactly
  that tree, parked on it like any long run of your own: a timer wait
  (`wait --on` refuses your own claim), the buddy skill's "Before you park";
  green: fast-forward main to it. Then always report, never a bare release:
  `buddy release herm --outcome pass --note "<landed sha; who was in; who is next>"`,
  or `--outcome fail` / `--outcome aborted` with what failed and who is out.
  Handing the run to another integrator mid-flight? `buddy release herm --to
  <session>` keeps it open, with its riders' waits; name several claims in one
  call (`buddy release herm orc-<project> --to <session>`) and they move as one
  transaction, with one notice. A successor already waiting on the run is
  handed it and its wait LANDS, "handed to you". A red
  run you will retry: keep the claim, re-claim with "RED — ejecting X, one more
  tier", run again, then release with the outcome.
- **A rider not done yet** declares `buddy wait --on herm --note "done in 10" --until 4h`;
  its ETA is its own words, and nothing turns it into OVERDUE. The default 3h
  can run out during a hold plus a 65-minute tier: EXPIRED with herm still
  open means it declares again. By convention, joining while the
  description says RUNNING puts you on the NEXT run. Buddy keeps no timer and
  ejects nobody: a quiet rider is shown quiet, and going without it is the
  integrator's call.

## Review depth: the tier follows the change class

How much review a change owes is picked per ITEM, by what the change
touches: never by which model wrote it, and never by how small the diff
looks. The orchestrator names the tier in the lane's brief; the lane names
what actually ran in its commit's receipt line. The repo's own CLAUDE.md
says which change classes take which tier (typically full for a security
boundary, what a gate refuses or allows, concurrency, a schema, stored data
mutated or deleted, or an invariant) and names its review script.

- **full**: before the code, the second model's design pass where the repo
  asks for one. Then, on the finished diff, in this order:
  1. An adversarial **panel**: several read-only subagents, each given the
     diff and one lens (bypass, concurrency, the refusal and its positive
     control, the docs' claims). Spawn it without asking: the tier is the
     authorization.
  2. A **second model family**'s code pass, at a named effort, through the
     repo's review script.
  3. A **cross-check** by a third read (another model, in a fresh context),
     given the diff and the same questions, asked for exact line -> issue.
  4. The second model's **re-verify** of your fixes, at most the brief's
     round ceiling (below). Folding findings makes new defects: in another
     repo's review gate, a re-verify round caught two high-severity defects
     introduced while folding the previous round.
- **lite**: a scoped adversarial self-review (re-read your own diff as the
  reviewer would, against the brief's questions), then ONE second-model pass
  at a named effort on the finished diff, its findings fixed or answered in
  the commit. One round, no re-verify loop. A cross-check is optional: yours
  or the brief's call.
- **chore**: no outside review required, and the receipt says why
  (`review: chore (docs)`).

**Every brief names a round ceiling with its tier (D-115).** The default,
which the operator may raise per item and nobody else may:

| tier | rounds |
|---|---|
| lite | one second-model code pass, folded once |
| full | a design pass, a code pass, then at most TWO re-verify rounds |
| chore | none |

At the ceiling the lane stops reviewing: it folds what the last round raised
that it can fold narrowly, answers the rest in the commit message, and
records it open (in the decision record or the note the brief names), then
reports READY. Rounds are where the tokens go, not landing: one gate change
took six re-verify rounds, each re-reading the lane's whole prompt, and on
that work every fold regressed something the previous round had accepted. A
finding left open and written down costs a later item; a seventh round costs
now and may regress again. Brief a lane on gate work to fold narrowly: fix
the exact form found, test it, and record a crafted respelling open rather
than widen a recognizer to catch it.

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

For every tier:

- **The tier says when a second model is REQUIRED, never when one is
  allowed.** Use one beyond what the tier owes (on a chore, or a second pass
  on a lite item) whenever a second reading is worth it, and name it in the
  receipt. Measured in another repo's review
  gate: a second-model pass on a below-full script found an option-injection
  hole four other review layers had missed, and an independent second-family
  review found 3 real issues (2 critical) that a same-family review missed.
  Reviewers of one family can share that family's blind spots.
- **A test-only change takes its class's tier.** A test of a gate is gate
  work: its assertions are what a review reads, and the person who wrote a
  weak assertion is the one least likely to see it.
- **The receipt is the last line of the commit body** and names every layer
  that ran, each second-model pass with its effort (the cross-check names
  its model):
  `review: <tier> (<layer> + <model>@<effort> <pass> + ...; <scope note>)`,
  every pass of a second model with its own effort, for example
  `review: full (panel + <second>@<effort> design + <second>@<effort> code + <third model> + <second>@<effort> reverify)`.
  A bare model name cannot tell a pinned pass from a silent fallback (in that
  other repo, 8 of 14 receipts in one stretch were that ambiguous). A layer
  the tier owes is skipped only on the operator's word (the cap, below), and
  the receipt then names it with the reason; a lane never skips one on its
  own. Report the receipt in your READY.
- **The review script is the repo's own** (its CLAUDE.md names it). Run it in
  the FOREGROUND with a long tool timeout, output redirected to a file you
  then read; never background it and never hand-roll the second model's CLI.
  A second-model CLI backgrounded under an agent harness can wedge with no
  output at all, and an empty output file looks the same as a live run. A
  repo whose CLAUDE.md names no review script: ask the operator before you
  run one.
- **A review is never test evidence.** The tests and the mutation check
  (break the fix, watch the test fail) are owed at every tier, chore
  included. A reviewer's finding is a defect once a test reproduces it.
- **The operator caps the tier per run** (for example "lite at most,
  optional, no second model family"), and the brief carries the cap. A lane
  never raises its own tier above it, and never lowers the brief's tier on
  its own: if the change turns out to touch a full class, say so to your
  orchestrator and wait for its answer.
- **A panel subagent is not a lane.** In the shared checkout it reads,
  greps and reasons; it does not edit, build, test or run a git command that
  writes. Anything that must mutate (a reproducer, a mutation run) gets its
  own worktree at a commit. It holds no claim, lands nothing, relays nothing,
  and runs under your session id and claims (D-103).

**Every lane runs on the operator's default lane model, full-tier items
included**, unless the operator names another model for the item (or sets a
run's cap the pick rule below works within). That default is a standing
decision in the operator's instructions, not a constant of this skill: read
it there. The tier changes the REVIEW, never the model: a full-tier item's
lane, its panel and its fold rounds run on that default too. A model the
operator keeps for reviews (a cross-check, a panel lens on a
security-sensitive commit, a judgement-call ruling) is never a lane's model
unless the operator named that item for it. The orchestrator puts the model
and effort on the launch (Opening lanes) and names them in the brief with
the tier (D-104, D-111: an older "the strongest model available" was read by
one orchestrator as the review model, and its full-tier lanes launched on
it).

### Picking the model from what past items measured

For the orchestrator, at the start of each run, and only among the models
the operator's cap for that run allows (for example "lanes on these two
models; never that one"). A full-tier item stays on the default lane model,
as above, and the pick never lands on a model kept for reviews. For the rest:

- **An auto-mode lane needs a model that has auto mode** (Haiku 4.5 was
  measured asking for every tool call in auto mode).
- **Before each sweep, run `sh scripts/model-report.sh --export <tsv>`**,
  into the file your repo keeps for it, and read the report at the start of
  each run. A plain `buddy sweep` deletes released claims, with their
  descriptions and outcomes, after 24 hours; the export is the only record
  that outlives it.
- **Pick by class.** Default to the cap's cheaper model for a class where
  the report shows its rework rate and its fail rate no worse than the other
  model's. Escalate one step where either rate is clearly worse. In a cell
  below the floor, use the model the cap names as its default, or, if it
  names none, the default lane model, and infer nothing. The floor and "clearly worse" are the
  operator's numbers, set with the cap; until they are set, the proposal is
  10 items per cell and twice the other model's rate. The report prints the
  observed rates, so either number can be revised.
- **Record the pick where the report reads it back:** the item's claim
  description begins with two tokens, `model=<id> class=<word> `, each once,
  then the description. `<word>` is one of `docs`, `code`, `test`,
  `research`, `ops`; a missing, repeated or unlisted token is counted as
  unknown and never printed. The lane makes the claim, so its brief gives the
  exact start:
  `buddy claim <item> --desc "model=<id> class=docs <what and why>" --scope <path>`.
- **The item's outcome is its own release.** A RED along the way keeps the
  claim: the lane fixes and declares again. Only a finished or abandoned item
  is released, and with its outcome, so a lane's release after LANDED PASS is
  `buddy release <item> --outcome pass --note "<reds> <batches>"`, never a bare
  one; an abandoned item is `--outcome fail`. A handover is
  `buddy release <item> --to <target>`, which keeps the claim open. A claim
  of the same slug after a release is how the report counts rework.

## Restacking a dependent lane (gh stack)

For the orchestrator. Lane B was built on lane A's branch; A's fold (an
amend or rebase after its review) moved A, so B still carries A's old
commits. Moving B onto A's new tip is one rebase, in B's worktree, onto A
from A's old tip (the sha of A's earlier READY). `gh stack` (GitHub's CLI
extension, v0.3.0 measured) runs that rebase and finds A's old tip itself.
Either way the restack is yours, done while both lanes are parked, and
landing is unchanged: you still rebase every READY commit onto main in your
own worktree, run the tier and fast-forward main (The run's claim, above).

What it does, measured in throwaway repositories (never this one, never
GitHub):

- `gh stack rebase --no-trunk <B>` checked that B's worktree was clean
  (untracked files included), then ran
  `git -C <B's worktree> -c rebase.updateRefs=false -c rebase.autoStash=false rebase --onto <A> <base> <B>`,
  finding A's old tip from A's reflog (`git merge-base --fork-point`) even
  when the stack was recorded after the fold. It rewrites B's branch IN B's
  worktree: a clean lane's checkout moves under it; one with uncommitted
  changes is refused ("uncommitted changes in worktree") and nothing is
  rewritten.
- **No gate covers a restack.** Git runs no pre-commit hook on a rebase or a
  plain cherry-pick (measured: a commit the gate refused under
  `BUDDY_COMMIT_GATE=deny` was rewritten by `gh stack rebase` with rc=0, and a
  plain `git rebase` and cherry-pick ran no pre-commit either), the commit gate
  only warns by default anyway, and the tool-call gate sees no path in a
  `gh stack` or `git rebase` command line. What it consults is the slot: a
  `gh stack` rewriting subcommand from a session not holding
  `.buddy/slot/gh-stack` is refused (step 1, D-108); nothing checks what a
  restack produced but the lane's own re-run (step 6), or nobody.
- **It reads GitHub even when it pushes nothing.** With origin a GitHub
  repository, `gh stack init` tried 2 read-only API queries for the branches'
  pull requests, `gh stack rebase --no-trunk` 4, `gh stack rebase --remote self`
  2, `gh stack view` 8, each to api.github.com carrying the operator's token;
  `gh stack unstack --local` tried none. (The first trial's origin was a local bare repository, so it
  could not see this.) Under macOS's `sandbox-exec` with the network denied,
  every query failed at the lookup, and each command did the same local work
  and exited 0. The hooks git does run on a rebase (pre-rebase, post-rewrite)
  run inside the same sandbox.
- A plain `gh stack rebase` FETCHES origin and can fast-forward local main or
  move the stack onto origin's main (the first trial). By their own help,
  `gh stack push`, `gh stack submit`, `gh stack sync`, `gh stack merge` and
  `gh stack link` act on GitHub, a bare `gh stack unstack` unstacks there too,
  and `gh stack unstack <number>` goes straight to its API.
- **A conflict stops in B's worktree** (exit 3), detached mid-rebase with
  conflict markers, and locks the stack catalog (`<git-common-dir>/gh-stack`,
  shared by every worktree) for EVERY stack: another restack is refused ("a
  rebase operation is already in progress") until `gh stack rebase --abort`,
  which put B back at its sha, checked out. While it is pending,
  `<git-common-dir>/gh-stack-rebase-state` exists and B's worktree reads
  `## HEAD (no branch)` in `git status --short --branch`; both clear when the
  abort finishes. `gh stack view` does not show it: from the main checkout it
  printed the same stack, conflict pending or not, and from B's worktree it
  failed ("not on any branch").

The recipe:

1. **Claim the slot first**, one restack at a time, because the catalog is one
   per checkout:
   `buddy claim gh-stack --desc "restacking <B> on <A>" --scope .buddy/slot/gh-stack`.
   Hold it until the restack is over: until any abort (or continue) has
   finished, `<git-common-dir>/gh-stack-rebase-state` is gone, and B's
   worktree reads `## <B>` again. Then release it with its outcome (steps 4
   and 5). The tool-call gate refuses, where a ledger governs, gh stack's
   rewriting subcommands (rebase, sync, push, submit, merge, link,
   unstack/delete, init, add, modify, and by the operator's decision
   `checkout` and `alias`; `unstack --local`, `rebase --abort` and `rebase
   --continue` with them: no flag is read) to a Bash call whose session does
   not hold `.buddy/slot/gh-stack` in every ledger the command reaches, and
   names the holder (D-108); `view` and the navigation commands pass. It
   reads the literal command only: a dynamic word (`gh stack "$op"`), a
   `gs` alias and a script file pass it, so the rule above is yours to keep,
   and the network discipline below is the recipe's, not the gate's.
2. Only when A and B have each reported READY in their own words
   (`buddy wait --on <run> --ready`, so `buddy who <run>` lists both at a
   commit) AND each shows `idle` on its first line in `buddy who <lane>`. No
   `idle` there is not idle: the Stop hook may not be wired, and no row means
   unknown (D-016), so treat that lane as busy and wait. A lane mid-turn has
   its tree moved while it works.
3. In ONE Bash call, every command with the network denied and the update
   check off (branch names copied from `git branch`: `init` creates a branch
   for a name that does not exist):

       off='(version 1)(allow default)(deny network*)'
       export GH_STACK_NO_UPDATE_NOTIFIER=1
       (cd <main checkout> &&
         sandbox-exec -p "$off" gh stack init --base main <A> <B> </dev/null &&
         sandbox-exec -p "$off" gh stack rebase --no-trunk <B> </dev/null) &&
       (cd <B's worktree> && sandbox-exec -p "$off" gh stack unstack --local </dev/null)

   Chained, so the first refusal stops it (measured: `init` of a stack
   already recorded exits 5, a conflicting rebase 3), and in subshells, so
   your own cwd stays put. The unstack runs from B's worktree because, run
   from the main checkout with two stacks recorded, it refuses ("branch main
   belongs to multiple stacks"). Check the result:
   `git log --oneline <A>..<B>` lists only B's own commits.
4. **A conflict: abort, report, and stop here.** `gh stack rebase --abort` at
   once, in the same sandbox (an unstack before it is refused, exit 7, while
   the rebase is pending), then the unstack in B's worktree. B is back at its
   own sha. Once step 1's test passes,
   `buddy release gh-stack --outcome aborted --note "<B> conflicted on <A> @ <sha>"`,
   and tell B: "restacking you onto <A> @
   <sha> conflicted and was aborted; your branch is unchanged. Rebase onto it
   yourself, resolve, re-run your checks, and report READY again." Never
   resolve a conflict in a lane's worktree. Skip step 5; step 6 still holds.
5. Clean: `buddy release gh-stack --outcome pass --note "<B> now <new sha>"`,
   then tell B in words: "restacked onto <A>
   @ <sha>; your branch is now <new sha>. Check it out, re-run your checks,
   and report READY again." A is untouched.
6. **Nothing lands until every restacked lane has re-reported.** B checks out
   its new tip (after a clean restack its worktree already holds it: confirm
   with `git log -1`), re-runs its item's checks, and declares READY on it,
   `buddy wait --on <run> --ready HEAD --until 4h`, with a READY message in
   its own words naming the new sha and the checks. Its earlier READY named a
   commit that no longer exists on its branch, and no gate looked at the new
   one.

Never `gh stack` from a lane, and never any of it but the four commands
above. Never `gh stack alias`, and never a `gs` wrapper or any other alias
for it: the coming gate refuses `alias`, and a `gs` command line is
invisible to it. A subagent runs under its parent's session id (D-099), so
one of yours inherits your slot and may run the restack for you; a lane's
subagent holds what the lane holds, which is no slot. Cut: the first trial's `gh stack rebase --remote self` (after
`git remote add self <main checkout>`), which restacks both lanes onto local
main without a fetch. It rewrites A's checkout too, adds a remote to the
config every worktree shares, still reads GitHub, and does what your own
rebase onto main already does when you build the run's tree, without moving
anybody's checkout.

What gh stack saves is looking up A's old tip; it costs three commands and a
slot claim. With gh stack missing or the catalog locked, the plain line does
the same rebase under the same rules, except the slot (it touches no
catalog): B's worktree must print nothing for
`git -C <B's worktree> status --porcelain --untracked-files=all`, then, in
the same sandbox and with the same two settings, so that no other branch
moves and nothing is stashed,
`sandbox-exec -p "$off" git -C <B's worktree> -c rebase.updateRefs=false -c rebase.autoStash=false rebase --onto <A> <A's old tip> <B> </dev/null`.
A conflict there: `git -C <B's worktree> rebase --abort`, then step 4's
report.

## When the deliverable is not code

Research notes, a report, a runbook, configuration: a claim is a reservation
on a path, and nothing in the ledger asks what the path holds. Everything
above carries over, with four words translated (the lane's READY form is in
the buddy skill too):

- **The directory must be a git repository; commits are optional.** Outside
  one, every verb answers "not a git repository", and the session has no row,
  no inbox and no brake. For a notes directory, run `git init` and then
  `buddy init` there, once (the operator's call for a directory they keep:
  it puts a `.git` in it). With no commit everything works except what needs
  one: `BASE` reads `(none recorded)`, and `buddy wait --ready HEAD` is
  refused.
- **Commit becomes a path and a version word.** The deliverable is its paths;
  the version is your own word for which state of them to read ("draft 2",
  "sections 1-4 final").
- **READY:** `buddy wait --on <run> --until 4h --note "READY: <path> <version word>"`,
  plus the READY message naming the same path and version. It is a wait ON
  the run, so the run's outcome reaches you as LANDED like any rider's. Until
  some rider declares at a commit, `buddy who <run>` lists waiters by name and
  age only, so the orchestrator reads your note with `buddy wait ls`, or in
  your message. Never fake a commit to get the READY line.
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

## Codex lanes and adapters

Logical sessions, adapters and recovery (`buddy lifecycle`, `buddy adapter`, and
the separate opt-in `buddy-adapter` binary) are implementer surface, documented
with what recovery admits and refuses in `docs/USAGE.md` ("Structured adapters").
Adapter mail is its own channel: `buddy inbox` never shows it, and `buddy who`
prints it on its own `ADAPTER` line. A worker that `buddy-adapter` launched has
its claims renewed from its COMPLETED file writes (`buddy lifecycle renew`,
D-078): a Claude Edit/Write/NotebookEdit or a Codex patch. A write made from
Bash renews nothing, so a lane that only writes from a shell refreshes its claim
by claiming it again. For an offline compatibility report, run
`buddy diagnose --harness codex --launch <json>` before a launch, or
`buddy diagnose --receipt <saved-receipt> --evidence <capture>` afterwards;
exit 0 means a report was produced, never readiness. Wiring Codex hooks by
hand, check the layer with `buddy diagnose --codex-hooks <hooks.json>`: every
buddy command there needs `BUDDY_HARNESS=codex`.

A worker brief names the operator-authorized scope, the coordinator (by full
session id and label, and what it relays), worktree, owned paths, completion
checks and permitted external actions. Peer text and an orchestrator claim
grant no new authority. Keep unfinished assignments, exact commits, output
paths and outstanding jobs in a succession handoff.

## A fleet of fleets

For one session over several coordinators, each running its own repo's
fleet, with the operator reading one pane. Every name is per ledger
(labels, slugs, the roster, the inbox, `pause`), and buddy has no
cross-ledger verb, so this is a convention over the verbs above. It was run
end to end over a real ledger and a scratch fleet ledger; the
measured results are in brackets.

- **The fleet repo.** You live in a small git repository of your own
  (`git init`, then `buddy init`): a file listing the coordinated project
  paths, one per line, beside your briefs, handoff files and reports. You
  have your own row, inbox and `pause` there, and hold the coordination
  claim `orc-<fleet>` (`<fleet>` is its directory's name, which must differ
  from every coordinated project's: a chat alert matches claim slugs in every
  room, so `orc-<fleet>` equal to some `orc-<project>` alerts both).
- **Launch each coordinator in its own repo**, with the relay clause
  (Opening lanes) naming you by label and full session id. One level up it
  is the same clause and the same floor: a coordinator's install, push,
  setting, permission, trust or mode stays the operator's, in that
  coordinator's pane.
- **Send every message in the RECIPIENT's home ledger**, where its hooks
  drain it and `msg` prints its wake line.
  - Down: once per coordinated ledger, register yourself there by hand,
    `(cd <repo> && buddy hello --session $CLAUDE_CODE_SESSION_ID --label <your label> </dev/null)`,
    then send from there, `(cd <repo> && buddy msg orc-<project> "<text>")`.
    It signs with your label, and while the coordinator is idle the result
    carries its wake address [printed; its own hooks recorded delivery
    within a minute].
  - Up: each coordinator's brief has it register by hand in the fleet ledger,
    `(cd <fleet> && buddy hello --session <its id> --label <its label> </dev/null)`,
    and report to your claim slug there, never your label:
    `(cd <fleet> && buddy msg orc-<fleet> "<text>")`. Your own hooks drain it
    [one tool call drained two messages in one 277-byte notice], and once
    your Stop has reported you idle, its result carries your wake address
    [no address before that].
  - A foreign registration only identifies a SENDER. Send nothing TO one: no
    hook of its session runs in that ledger, so mail queued there is never
    drained, and `msg` takes it without refusing ["registered … and not seen
    since"].
- **Keep your own cwd in the fleet repo**: run each foreign command in a
  subshell, `(cd <repo> && buddy <verb> …)`, so your hooks keep beating your own
  ledger. A row your hooks have beaten is bound to your process, and a `bye`
  of a bound row while you are alive is refused [`stays live`].
- **Read each fleet on a timer wait of your own**
  (`buddy wait --until <dur> --note "<the fleets>"` in the fleet repo, then
  the `/loop`): `(cd <repo> && buddy who orc-<project>)`, and
  `buddy who <run>` there. A read moves only your row's `seen` in that ledger
  [one column of one row, in the fleet ledger], so your foreign rows read STALE only when you have
  not looked for 30 minutes.
- **Wear a label that is yours alone, numbered per successor** (`top-1`,
  `top-2`, as coordinators are `orc-<n>`), and have peers address the claim
  slug. A label is matched against ended rows too, and nothing removes a
  session row, so once a successor wears the same label in a ledger,
  `buddy msg <label>` is ambiguous there for good, unless the old id is
  registered again under another label, whether or not the old row said bye
  first [three times]. A claim slug resolves to its one holder past
  a duplicate label [measured], and moves with `release --to`.
- **Hand off** as Handing off a coordinator says, and in each coordinated ledger, while
  you are live and in this order: `(cd <repo> && buddy msg orc-<project> "<successor label> (session <id>) is your coordinator from now; it relays the same decisions I did.")`,
  then `(cd <repo> && buddy bye $CLAUDE_CODE_SESSION_ID </dev/null)`, which
  ends your unregistered-process row there [no output, exit 0]. After the
  bye a send from you there signs as `operator` [`as operator`]. Your successor's first
  act in each is its own hand `hello`. In the fleet repo, one
  `buddy release orc-<fleet> <run> --to <successor>` hands every claim on.
  That is three commands per coordinated ledger and one in the fleet repo.
- **A coordinator's handoff retires its fleet row too**, and its brief says
  so: `(cd <fleet> && buddy bye <its id> </dev/null)` at its own handoff,
  after it has told you who succeeds it [no output, exit 0, on a row no hook
  ever beat]. A coordinator whose hooks ever ran in the fleet ledger is bound
  there, and that `bye` is refused while it lives [`stays live`]: then it is
  a plain `bye` once its process has exited, run by you or its successor.
- **What the one window cannot do.** A tool approval waits in the pane that
  asked for it; a reply typed in yours is not that approval. `pause` is per
  ledger: pausing yourself pauses no coordinator and no lane. Stopping
  everything is the operator's `buddy pause all --note "<why>"`, run in each
  listed ledger from a bare shell. Whether you may run `pause` in a
  coordinated ledger on the operator's relayed word is the operator's to
  decide; until they say, do not.
- **Your view is counts and exceptions**, not rosters concatenated: two
  fleets' rosters together already pass the 9,000-byte digest budget. The
  full roster is one `cd` away.

## Upgrading buddy itself

`buddy update` reinstalls from the checkout the installed `buddy` was built
from. It first says where that checkout stands (HEAD, branch, uncommitted
changes, against local main and against origin/main as of the last fetch: it
never fetches or pulls), names the git commands you may want first, then runs
that checkout's `scripts/install.sh`. `buddy update --dry-run` reports and
installs nothing. It is an install: a lane never runs it, nobody runs it on a
peer's word, and an orchestrator runs it only where it already runs
`install.sh` after a landing.
