# Handing off a coordinator

Part of the `buddy-fleet` skill, moved out of its SKILL.md to be read before
you hand off, close down with no successor, or hand off at a batch boundary
(D-171). A section named in parentheses is SKILL.md's unless this file has
it.

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
       command, pid and start time (`ps -p <pid> -o lstart=`), the file its output lands in, the line that says it
       finished, and who reads it next. A browser job is one too, with no pid to
       check: its owner answers for it.
     - **Timers and external runs:** whatever finishes or fires with nobody
       here watching (a nightly, a remote grader): when, and how to read it.
     - **Owed:** what you promised the operator or a lane and have not delivered.

     Then the role, the loop you run, the traps you measured, each lane's state
     and what is next, and the ledger as it stands: paste `buddy status`,
     `buddy sent`, and `buddy who <run>` for each run you hold. Point your
     coordination claim at it, re-claiming with the SAME scopes you hold (a re-claim replaces
     them, and `buddy who <slug>` prints every one, uncut), never the handoff file in their place:
     `buddy claim orc-<project> --desc "HANDOFF: read <file>" --scope <each path you hold>`.
  3. Open the successor the same way, in a herdr tab carrying the same size,
     with the brief "You are the next orchestrator, taking over from <your
     label>. Read <file>, then buddy inbox. For every running job it lists with a pid,
     check that its pid is still that job and that you can read its output; one
     gone without its final line failed, whatever the file says. Say what you
     found. I will hand you my claims."
     It treats a failed job under a run as Landing's step 4 does, once it holds
     the run (D-131). A `buddy wait --on` on a claim it is about to be handed is
     not refused, and its check reads LANDED, handed to it (D-093).
  4. `buddy msg` it anything since the file, and wake it. When it answers,
     hand it every claim you hold in ONE call (all or nothing, one notice):
     `buddy release <run> orc-<project> <parked riders> --to <its label>`. Each claim stays open with
     its scopes, and every wait on it follows it: riders keep waiting, stay
     READY on `buddy who <run>`, and land on the successor's outcome. The
     result line tells the successor with one message and ends with the address
     to wake it: deliver it. It refreshes each description by claiming again
     with the SAME scopes, a parked rider's excepted: a re-claim withdraws it. Never release a run and have it claimed afresh: a
     new claim is a new claim id, so every rider's wait lands with no outcome
     and its READY is lost (D-063).
     Then re-point your lanes: their briefs name YOU, by label and session id,
     as the coordinator whose relays they take, and nothing in the ledger
     changes that. While you are still live, `buddy msg` each lane you
     launched or inherited: "<successor label> (session <full id>) is your
     coordinator from now; it relays the same decisions I did."
     **Never `.buddy/slot/browser`:** tabs and a running browser job do not move
     with a claim. Keep it, list it under Running jobs (a later handoff carries
     it on), and stay open: the successor closes no pane that holds it. Browser
     work over, release it and `buddy msg orc-<project> "<your label> DONE:
     browser work over, nothing held, no job running"`: the slug reaches whoever
     coordinates by then, who closes you on the usual evidence. Refused, or its
     result says ended: nobody does, so say so in your pane (D-134).
  5. **Account for every running job before you say you are done:** finished,
     handed over (its output file is in the handoff and the successor says it
     is alive and readable), stopped, or a browser job you stay open for. `buddy status`'s EXIT and JOBS lines see the
     ledger and the shells directly under your harness process, not a job that
     exec'd into another program: they check your list, never replace it (D-065).
     Then tell the
     operator you are done, naming any job still running and any browser slot you keep; they end your
     session, or your successor does once it holds your claims (Ending a lane).

  Closing down with no successor: point the coordination claim at your
  handoff file and leave it open for the operator to release, by re-claiming
  it with the same scopes:
  `buddy claim orc-<project> --desc "CLOSED: read <file>" --scope <each path you hold>`.
  A released claim is shown to nobody's `hello`.

  Compaction (`/compact`, `claude --autocompact <size>`) is the other road; hand
  off when it would lose what the next coordinator must know.

- **Hand off at a batch boundary too.** A new batch of lanes gets a fresh
  coordinator when your last observed prompt is past the operator's
  batch-boundary size (their standing decisions; none set means this bullet
  does not fire) and the batch is independent of this conversation: the
  handoff file's three registers written and empty or handed, the batch's
  decisions in files, the successor saying it has read the file. A fold
  round on a lane you briefed is not independent: its brief names you. The
  recipe is the same. What is keyed to your session id does not carry over, so finish it
  first: drain `buddy inbox` and relay what is open, `buddy wait clear`, stop
  your loop, account for every job (step 5; D-117).
