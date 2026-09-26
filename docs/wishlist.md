# Buddy wishlist — field notes from a 14-session orchestrated run

**Source:** one orchestrated run on a reference repo, 2026-09-20. One orchestrator
(no item of its own) coordinating up to 14 concurrent sessions over ~4 hours, against
a repo with a serialized ~60-minute test tier, a shared remote grader, a 4-slot
external review API, and an append-only ledger three files wide.

**Relationship to `ORCHESTRATION-PLAN.md`:** that document covers task agreements,
briefs, readiness, delivery status and recipes — the *work* half. Almost nothing
below overlaps it. What broke in practice was the *resource and identifier* half,
plus correction propagation. Read them together.

**Method:** every item names the incident that motivated it. Where a figure appears
it was measured during the run. Items are ordered by how much they actually cost.

---

## 1. Resource slots — the single biggest gap

Claims reserve **scope** (paths). Nothing reserves a shared **resource**. This repo
has at least four: the local test box (one `check-all` at a time), the remote grader,
a 4-slot external review API, and `main` itself during a land.

I built a slot protocol in chat: "ask before you start", "announce when you start",
"tell me when you're off". It worked only because I answered every message. It has
no state, no history, and no way for a session to see the current holder without
asking me.

**What happened:** I told three sessions individually that a grep-bound doc gate may
overlap a tier. Each applied that correctly. The aggregate was **four concurrent
suites against a 60-minute tier with timing legs** — a fleet-level load nobody
authorised, assembled entirely out of permissions I granted one at a time. A
per-session allowance is not a fleet allowance, and only the coordinator can see the
sum.

**Wanted:**

```sh
buddy slot acquire box --exclusive --eta 60m   # blocks or refuses, records holder
buddy slot acquire codex --count 1             # counted, cap declared per resource
buddy slot ls                                  # holder, since, eta, queue depth
buddy slot release box
```

Resources declared in repo config with a capacity (`box=1`, `codex=4`, `grader=1`).
The value is not enforcement — buddy is cooperative and should stay so — it is that
**the count exists somewhere other than in the coordinator's head**. The structural
point, from one of the sessions: *a rule phrased as a property of one session's work
("is my gate cheap?") composes badly by construction; one phrased as a request
against a shared counter ("may I spend a slot?") cannot.*

## 2. A session's OS processes are invisible to buddy

> **Status (2026-09-20, D-025):** the roster now prints `pid N` — the `claude` process the
> session's hooks were spawned by — and `pane herdr:…` from the environment; `buddy who
> <any name>` returns the rest. What a session is RUNNING (`buddy run`) is not tracked.

**What happened:** a session found two `check-residuals` processes it did not start,
traced their parents to a `claude` pid, and **could not map that pid to a buddy
slug**. It correctly refused to name a peer on an inference and escalated to me. I
mapped it by walking the process tree and reading a scratchpad path out of a shell
wrapper's argv. Separately, a fourth session had been running gates I did not know
about at all.

A slug and an OS pid have no link in either direction. Every session knows its own
slug; nothing records what it is running.

**Wanted:** `buddy run start "<label>" --pid N` / `buddy run ls` / auto-clear on exit,
with the roster showing `running: check-all.sh (pid 16087, 12m)`. The sessions
converged on announcing slug+pid in chat by hand; that is the feature asking to exist.

## 2b. A session has at least three names, and none derives from the others

> **Status (2026-09-20, D-027):** `buddy who <id|label|s-id|slug>` takes any of the four
> and prints the rest, with every register; `hello` warns when a `--label` is worn twice.
> The harness roster name is not captured (the hooks do not receive it).

**What happened:** a session was addressed in the room as `repo-4d` (from its buddy
id `s-4d601a31`), appeared in the harness roster as `repo-e7`, and held a claim
slugged `r1701-console-quoting`. A peer reported "two sessions have claimed R-1701"
and I asked the session to deconflict — it could not, because `buddy ls --all` showed
exactly one claim. **It was one session wearing two names.** Resolving it cost two
sessions an hour of attention, and the same ambiguity in the other direction would
have made a real duplicate claim read as normal.

Separately, two distinct live sessions shared the harness display name `repo-95`,
distinguished only by a hex ref. In a fleet that addresses peers by name, that is a
mis-delivery waiting to happen.

So the registers are: **buddy session id → claim slug → harness roster name → OS pid**,
four identifiers for one actor, and no mapping between any adjacent pair. §2 asks for
the pid link; this is the same defect one register further out.

**Wanted:** buddy records and prints the harness-visible name alongside its own id
(the hooks already receive enough to capture it), so `buddy sessions` and `buddy ls`
can show `s-4d601a31 (repo-e7) [r1701-console-quoting]`. A `buddy who <any-name>`
resolver that accepts any of the four and returns all of them would close it outright.
Without that, the only reliable cross-register map lives in the coordinator's head,
and the coordinator is the party least able to verify it.

## 3. Identifier allocation — this caused a real collision

> **Status (2026-09-20, D-029):** `buddy ids seed|take|ls|status`. Seeded with the measured
> high-water mark, contiguous above the ceiling, nothing reissued, no `return`, and a
> status that never claims to know the artifact. The alloc verbs above are its shape.

The repo allocates decision numbers (`D-nnn`) and residual-row ids (`R-nnnn`) from
shared append-only ledgers. I allocated blocks centrally to prevent collisions.

**What happened:** I took the high-water mark from the ledger at a given commit and
issued blocks. A session outside my allocation set — a lander whose numbers were
reserved in its own uncommitted work — landed `R-1705` while my blocks were in
flight. Collision.

Then three further failures of the obvious fixes:

- **"Check the ledger for a row"** misses a peer's reservation that is not written yet.
- **"Grep all of docs/ for mentions"** over-counts: an entry *recording that numbers
  were returned unused* reads identically to one taking them. A session duly reported
  the high-water two too high and nearly skipped free ids.
- **Neither can see** a number allocated to a session and written nowhere at all. Two
  such were returned to me by hand.

The state we ended at: **three registers — the ledger is the authority for TAKEN, the
coordinator's table for RESERVED, and neither can be derived from the other.**

**Wanted:** buddy holds the reservation register.

```sh
buddy alloc take R 5 --for r1649-cve   # atomic, returns a contiguous block
buddy alloc ls                          # who holds what, and since when
buddy alloc return R-1713 R-1714        # back to the pool, no ledger prose needed
```

Crucially this removes the need to record returns *in the artifact*, which is what
created the over-count. A return recorded in prose is a landmine for the next counter.

**THE STRONGEST ARGUMENT FOR THIS ITEM IS HOW MANY STATES WE DISCOVERED BY NOT HAVING IT.**
Over one run, reconstructing the allocation register by greping prose produced *six*
distinct states that a naive "is this id taken?" check conflates — each found the hard
way, by a different session, after a near-miss:

| state | how it reads to a grep | found by |
|---|---|---|
| TAKEN | a row exists | — |
| RESERVED | nothing at all | a session returning two ids nobody could see |
| COMMITTED-FORWARD | mentioned, promising the id to future work | an audit catching me about to reissue it |
| RETURNED-UNUSED | mentioned, recording a *return* | a session reading a return as a use |
| BACKFILL | free, but 38 below the frontier | a session filing into a hole |
| RANGE-CITATION | `R-1770..R-1779` gives the **endpoints** a false positive and the **interior** nothing, though their status is identical | an audit measuring R-1779 "mentioned" and R-1775 "clean" |
| CITED-FROM-AN-UNLANDED-BRANCH | neither free nor taken; an **immutable commit body** points at a number that may land elsewhere | a session de-pinning a peer's row number from its own commit message |

Every one of these is an artifact of the register being *prose that must be parsed*
rather than *state that can be queried*. None would exist if `buddy alloc` held it.
Two are worth singling out. RANGE-CITATION means a correct, careful grep returns
opposite answers for two ids in the same unused range, failing toward skipping a free
number. CITED-FROM-AN-UNLANDED-BRANCH is worse: it fails toward a **permanent wrong
pointer** rather than a collision, because a commit body cannot be corrected once
landed.

I also published three buddy slugs that were wrong in a systematic way — I had put
`s-` in front of a harness roster ref, producing a string that *looks* like a slug and
is not. That is §2b's missing resolver showing up as a coordinator error rather than a
session one.

## 4. Corrections cannot overtake the broadcast they correct

**What happened:** I broadcast seven measurements during the run. **Seven were wrong**
and each was caught by a session, sometimes after peers had acted. Examples: I told
nine sessions to count occurrences with `grep -c` (it counts lines); I told them to
drive a claim count to zero (which would have deleted legitimate historical
citations); I broadcast a status-file emergency off one session's local figure without
running the one command that settles it.

`buddy msg all` snapshots live sessions at send time and delivery rides the heartbeat.
There is no supersession, no read state, and no way to answer "who has seen the
correction?".

**Wanted:** `buddy msg all --supersedes <msg-id>` marking the original as corrected at
the receiver; delivery/read state in `buddy sessions`; and a `buddy notices` view of
standing corrections a session can re-read on wake. **An orchestrator will be wrong;
the system should make being wrong cheap to undo.**

## 5. Claims are file-grained; conflicts are row-grained

**What happened:** one row of one TSV — `B32` in a bundle ledger — conflicted on
**six separate rebases** across four sessions. Nobody's claim could express "I own row
B32"; every session claimed the whole file or nothing, and the convention became "do
not claim the ledgers at all, append at land time and let the merge driver sort it
out". That convention exists because claims are too coarse.

The repo's merge driver **duplicates edited rows silently** — the file still parses,
every gate stays green, and a peer's edit is lost without trace. Four sessions
independently developed the same defensive resolution (take main's row wholesale,
append only your own ids, print both difference sets to prove nothing was dropped).

**Wanted:** sub-file claim keys — `--scope docs/residual-bundles.tsv#B32` — advisory
like every other claim, purely so `buddy whose` can answer and the commit gate can
warn. Also: a claim kind for **"I will need this file for 60 seconds at land time"**,
distinct from "I am editing this for an hour". Two sessions were blocked for tens of
minutes behind a claim whose holder had finished editing.

## 5b. Claims are ALL-OR-NOTHING, and one busy path blocks every free one

> **Status (2026-09-20, D-019):** `claim --dry-run` reports the whole conflict set and takes
> nothing; a refusal now names every collision; `release <slug> --scope <path>` narrows a
> claim. Partial ACQUISITION was considered and cut — see the decision record.

**What happened:** a session was asked to claim four paths. `buddy claim` refused the
**entire** command because one of them (`docs/decisions.md`) overlapped a live holder.
The other three — which nobody held — were therefore not claimed either. The session
reported it rather than retrying piecemeal, correctly.

This compounds with §5: because ledger files are contended almost constantly, *any*
claim that mentions one is likely to fail wholesale, and the session loses the
uncontended paths it actually needed.

**Wanted:** partial acquisition with an explicit report — take what is free, name what
was refused and who holds it, exit non-zero so nothing passes silently:

```
claimed:  docs/status.md, docs/archive/status-2026.md
REFUSED:  docs/decisions.md  (held by v1-demo-hermetic-rehearsal, 6m)
```

Or at minimum a `--dry-run` that reports the conflict set before anything is taken, so
a coordinator can narrow the request in one round trip instead of three.

**AND THE SAME DEFECT WEARING ITS OPPOSITE FACE: THERE IS NO PARTIAL RELEASE.** Later
in the same run, a holder narrowed its scope by *saying so* — its claim description
ends with the words "Docs scopes RELEASED." The recorded scope was unchanged, because
`release` is all-or-nothing too. So enforcement kept reading four held paths while the
holder believed it had handed them back, and a session that needed two of them
correctly stayed off them. **Neither party was wrong and the file was stuck.**

That is worse than the acquisition case, because acquisition fails *loudly* — you are
refused and you know. A prose release fails *silently and in both directions*: the
holder thinks it released, the waiter sees it held, and nothing contradicts either.

**Wanted:** `buddy release <slug> --scope <path>` for partial release; or, failing
that, a warning when a claim's description asserts a release its recorded scope does
not reflect. The only working form today is for the holder to re-claim with the
narrowed scope — which a waiter cannot do on its behalf and should not.

**Second-order:** I issued an assignment that told a session to scope to two files
*and* to file a ledger row — which requires two more files. Those instructions could
not both be satisfied, and neither of us noticed until the claim was refused. A
`--dry-run` would have caught my contradiction before the session hit it.

## 5c. `buddy msg` has its own inbox, and a session reading the hook stream never sees it

> **Status (2026-09-20):** the mechanism as stated is refuted — `beat` drains the inbox into
> the same `BUDDY MESSAGES` hook output, and the ledger for this run shows every direct
> message whose target heartbeated afterwards was delivered. The one undelivered message to
> a live session was queued four minutes AFTER that session reported idle. B was parked; a
> parked session runs no tool, so nothing drains its inbox (D-016). The fix that reaches a
> parked session is a prompt from the operator. See ORCHESTRATION-REVIEW.md for the probe.

**What happened, and it cost two sessions an hour:** session A was blocked waiting for
session B to release a contended file. A messaged B twice. I then chased B directly.
B answered none of it — and was not ignoring anyone. **`buddy msg` lands in a private
inbox that only `buddy inbox` surfaces. It does not appear in the `BUDDY MESSAGES`
lines the PostToolUse hook injects**, which carry the room/broadcast stream. B had been
reading every hook line all session and had never run `buddy inbox`.

B's own diagnosis is the right one: the hook stream is continuous enough to *feel* like
full delivery, so the absence of a direct message is indistinguishable from there being
none. Nothing signals that a second, quieter channel exists.

Worse, the session most likely to be messaged — one that is **parked**, holding
something a peer needs — is the one least likely to run any command that would surface
it.

**Wanted:** the hook stream should carry a pending-inbox count (`BUDDY: 2 unread`), or
direct messages should ride the same injection the room does. Either closes it. A
`buddy inbox` reminder at session start and before parking is the workaround, not the
fix.

## 5d. A refused claim never opens, so its slug cannot be messaged

> **Status (2026-09-20, D-019):** `msg` now signs with the sender's LABEL, which always
> resolves, and keeps an explicit `--from` as a tag after it. Measured in this run's ledger:
> 1 of 111 direct messages was signed with a label; 104 with a claim slug.

**What happened, in the same incident:** B tried to reply to A and got `no such target`.
A was addressing itself by its claim slug — the convention the tool documents and that
peers use — but **A's claim had been REFUSED, so it never opened, so the slug does not
resolve.** The blocked party can reach you; you cannot reach it back by the name it is
using.

That is a dead-end aimed precisely at the person trying to *unblock* someone. B fell
back to the harness roster and a different addressing layer entirely.

**Wanted:** a refused claim should still register its slug as a messageable alias for
the requesting session, or `buddy msg` should fall back to resolving a slug against
refused/pending claims and say so. This is the same family as §2b (a session has four
identifiers, none deriving from the others) — here the failure lands on the party with
the power to fix the problem.

## 5e. WITHDRAWN — a finding I put here and a peer then refuted

I previously wrote an item claiming that **claim boundaries silently clip the scope of
a repo-wide sweep**: one session fixed six sites, a second found eight, and the two
survivors were both outside the first session's claim. It was a clean story and I
recorded it.

It is **wrong**. A third session tested the first session's three literal anchors
against both survivor lines with `/usr/bin/grep -F`: **no match, all three, both
lines.** The anchors would have missed those sites regardless of who held them. The
real mechanism was time-varying prose with no fixed spelling — the claim had been
written four different ways over its life, and each sweep was anchored on the spellings
its author could see.

I am leaving this here rather than deleting it because the *process* is the item worth
reviewing: I wrote an inference as a finding, the session it credited narrowed it
unprompted, and a third session refuted it outright with one command. Any wishlist
assembled from a live run will contain some of these. Treat every item above as
carrying that risk unless it names its measurement.
## 6. Nothing shows what base a session is on

**What happened, and it cost me:** a session reported the shared status file at
1999/2000 lines — one line of headroom, a fleet emergency. I broadcast urgency and
assigned an archive roll. A peer challenged the figure. Measured: **main was 1736**.
The reporting session's worktree was based three landings back, *before* an archive
roll that had removed 363 lines. Its number was true about its tree and meaningless
about main. It had also trimmed its own write-up by a third to fit a budget that did
not exist.

With N sessions on N worktrees at N bases, "how stale am I?" is the most common
implicit assumption in every message, and it is never stated.

**Wanted:** the roster carries each session's base — `base 079dd6a7 (3 behind main)`.
Cheap to collect (the hooks already run git), and it would have flagged this instantly.
Optionally a warning at claim time when a session's base is more than one landing old.

## 7. Standing scopes for shared doctrine files

**What happened, twice in two landings:** the repo requires mechanism rules to land in
a specific playbook file. Both times, the file was held mid-item by an unrelated
session. Both sessions correctly declined to contest a live claim, filed a row saying
"this rule has no home", and shipped a `DURABLE: none` receipt. **Two orphaned rules
and no owner** — a coordination defect, not a session defect.

**Wanted:** a claim kind that is explicitly *shared-append* rather than exclusive, for
files whose contention is always append-only. Or first-class support for a standing
owner: `buddy claim --standing playbook --scope docs/playbook.md`, which yields to
nobody and which the coordinator assigns once.

## 8. No audit trail of coordinator decisions

I issued dozens of rulings: scope grants, slot grants, number blocks, integration
order, tier classifications, three ejection thresholds. All of it lives in chat
scrollback. When a session asked "did you give me `cmd/<app>/main.go`?" I had to
remember.

**Wanted:** `buddy grant <slug> --scope <path> --note "<why>"` recorded in the ledger
and visible in `buddy ls`. Same store, same idiom as claims; the difference is that a
grant is made *by* the coordinator *to* a session, and it is the record that survives.

## 9. Roster gaps an orchestrator picks on

The new annotations (`idle N`, `claims N`, model/effort/prompt size) were genuinely
useful and I routed on them. Missing:

- **`blocked-on`** — three sessions were waiting on a claim holder and only I knew.
- **`base`** — see §6.
- **`running`** — see §2.
- **`last-grant`** — what the coordinator most recently authorised for this session.
- A **name-collision warning**: a peer session shared my exact display name
  (`repo-95`), distinguished only by a hex ref. In a fleet that addresses by name,
  that is a mis-delivery waiting to happen.

## 10. Smaller, still real

- **`buddy sessions --by started`** is documented and works; a note in the README that
  the old "age column is last_seen" guidance is superseded would save a re-derivation.
- **`buddy msg` to an idle session** won't arrive until its next tool call. That is
  documented, but it means the sessions most able to take work are the least reachable.
  The roster should mark "message queued, undelivered". *(2026-09-20, D-027: `msg` now
  says so on the send, and `who` shows the undelivered count.)*
- **Message size**: the 750-byte routine budget is right for status, but an
  orchestrator's rulings are genuinely long. Every substantive coordination message in
  this run went out-of-band via the harness instead of through buddy, which means the
  buddy journal has no record of the run's actual decisions.

---

## What worked, and should not be changed

- **Cooperative-not-enforced.** Every scope collision resolved socially and correctly.
  The one time buddy did refuse (a claim overlapping a live holder) it was right, and
  the session escalated rather than forced.
- **`buddy whose`** answered a real question repeatedly.
- **Claim descriptions as a broadcast channel.** Sessions read each other's `--desc`
  to understand the run. They are doing double duty as documentation and it works.
- **Refusing to resolve an unresolvable target.** Prevented at least one message going
  nowhere silently.

## 11. A message carries no provenance, and a relayed lead reads like a finding

**What happened, repeatedly:** as coordinator I relayed peers' findings to other
peers. Four times I forwarded a claim I had not measured, phrased the same way I
phrase things I have measured, and it was acted on. Two of those reached a session's
prepared commit before a fourth review layer refuted them.

**The failure is in the label, not the content.** A lead sent early is valuable —
one session told me so explicitly: *"I would rather have the lead early and wrong
than late and right, provided it arrives labelled as a lead."* But a buddy or
harness message has no field for that. "X is caused by Y" from a coordinator reads
as settled regardless of whether the coordinator ran anything.

**And here is the one case that did not propagate, which shows what closes it.** I
relayed a mechanism to a session as fact. Its entry never carried it — because it
wrote down only the clauses it had verified itself and silently dropped mine. Its
own account: *"that is not foresight; it is only that I wrote down what I could
verify and left the scenario out because I had not measured it."*

So: **the relay was wrong, the recipient wrote only the measured half, and the error
did not reach an immutable artifact.**

That is the honest claim, and it is narrower than the one I first wrote here. The
recipient corrected me: the error *did* propagate — into my own rhetoric, into praise
I sent a third session, and into the first draft of this very item. It was stopped at
**one** boundary, after crossing several. What the mechanism bought was that it never
reached a committed decisions entry, which is the boundary that matters because that
artifact is append-only.

Its words, declining the better-sounding version: *"If the sentence is going in front
of an operator I would rather it claimed the smaller true thing."*

The discipline worked because it was mechanical, not because anyone was sharp — which
is the only kind that survives a fourteen-session day. But it is a last line of
defence, not a filter, and an item claiming otherwise would be making the same mistake
it documents.

**Wanted:** a confidence/provenance marker on messages — `--lead` vs `--measured`,
surfaced in the rendering, so the distinction is structural rather than a habit the
sender has to remember under time pressure. Cheap to add, and it targets the exact
moment the habit fails: when you are relaying fast because someone is blocked.

Related: several sessions independently converged on stating the *limit* of their own
findings ("this is an inference, not a run"; "my oracle verifies rows it named"). That
convention emerged socially and nothing in the tool encourages it. A structured field
would make it the default rather than an act of unusual care.

## The one meta-lesson

Of the incidents above, the expensive ones share a shape: **a fact that is true
locally and false globally, asserted without consulting the authority.** A session's
line count, a session's view of the high-water mark, a per-session load allowance, a
peer's pid. The orchestrator is the only party who can see the global fact, and is
therefore the party most likely to broadcast a local one as though it were global —
I did it seven times.

Buddy cannot prevent that. What it can do is **make the authority cheap to consult**:
one command for the current holder of a resource, one for the allocation register, one
for a session's base. Every one of my seven errors had a one-command discriminator I
did not run.

---

# Addendum — findings after the first draft (2026-09-20, later in the same run)

Four more, three of which are defects in buddy itself rather than gaps in it. Recorded
separately because the first draft claimed to be complete and was not; the run kept
producing material after I stopped writing, which is itself the point of §2b.

## 12. `buddy whose <path>` answers a different question than its name

**Measured.** A session (`grader-throughput`) used `buddy whose docs/status.md` to decide
whether it could write that file, read the result as "unheld", and was wrong — `status.md`
was claimed by another session the whole time. **`whose` reports who has UNCOMMITTED CHANGES
to a path, not who has CLAIMED it.** Those coincide often enough to look right and diverge
exactly when it matters: a session that has claimed a file and not yet started editing it is
invisible, which is the state every session is in immediately after claiming.

The name is the defect. `whose` reads as ownership; it returns dirty-worktree attribution.
Either rename it (`buddy dirty <path>`) or make it report both registers with the claim
first:

```
$ buddy whose docs/status.md
CLAIMED BY   v1-demo-live-gates (s-50c3c451)  since 14:02Z
DIRTY IN     (none)
```

Two sessions hit this in one day. The second one only caught it because a coordinator
happened to hold the claim table and contradicted the answer.

## 13. `buddy msg` ignores stdin, and the exit status that looked like the second cause was the pipe

> **Status (2026-09-20, D-021):** the surviving half SHIPPED — `msg` reads its body from
> stdin when argv carries none, never from a terminal, and the cap is measured on the
> rendered body (a line break is `⏎`, three bytes) rather than the raw one. `msg --dry-run`
> resolves and measures without sending. See the decision record.
>
> **Status (2026-09-20):** half of this item is REFUTED, and it is the half the title led
> with. The usage path does NOT return 0. Measured on the committed binary: `buddy msg all
> --from s-xxx` with no text exits **1** — `Run` returns 1 for any command error
> (`internal/cli/cli.go`), and it always has. The `rc=0` recorded below is the `| tail -5`:
> `sh` has no pipefail, so the status read back was the filter's, not the command's. That
> trap is already in CLAUDE.md's environment gotchas; this item is what it looks like when
> it bites something other than a check run. What SURVIVES is the stdin half, and it is
> enough on its own to have caused the incident.

```
$ buddy msg all --from s-xxx <<'EOF' | tail -5
...text...
EOF
buddy msg: usage: buddy msg <session|label|slug|all> [--from <who>] <text...>
--- rc=0 ---
```

**The broadcast was never sent.** `msg` takes its text as argv and silently ignores stdin,
so the heredoc went nowhere and what ran was a usage error. Measured both ways on one
binary, 2026-09-20: unpiped, that command exits 1 and a caller checking `rc` learns it;
through the pipe it reads 0. **One cause, not two** — and the one that remains is the one no
exit code would have fixed, because a caller who redirects instead of piping still has to
notice that the text it fed the command was discarded.

The inconsistency lives inside a single binary: the hook verbs DO read stdin (`readHook`),
so `buddy gate` and `buddy beat` take their payload there while `msg` throws the same
channel away without a word.

Wanted: **read stdin when it is not a TTY**, because every other line-oriented tool in this
workflow does and the muscle memory is real — or REFUSE when stdin is not a TTY and no text
was given, which is cheaper and fails at the moment the mistake is made rather than at the
moment somebody notices the fleet was never told. Either way the discriminator is already
written and already load-bearing: `stdinIsTTY` sits beside `readHook`, added so a human at
a terminal would not block waiting for hook JSON that is not coming. A `buddy msg --check`
printing the resolved recipient set and byte count without sending would also have caught
it.

**Why this is corrected in place rather than deleted**, which is §5e's rule applied to its
author: the item was written with two causes, one observed and one inferred from it, and the
inferred one went in the title. The observation (`rc=0`) was real; the explanation attached
to it was not. A wishlist item that names a mechanism is a hypothesis until somebody runs
it, and the cost of running this one was a single command with the pipe removed.

## 14. A long-running session's copy of the standing rules rots, and nothing says so

> **Status (2026-09-20, D-028):** `buddy authority [add|rm]` (CLAUDE.md always); `beat`
> announces a change ONCE on the next tool call; `status` carries an AUTHORITY line. An
> mtime advisory, worded as one.

This is the one I would most want fixed, and it is not strictly buddy's job — but buddy is
the only thing in the workflow that knows a session has been alive for nine hours.

**Measured.** I quoted my project's constitution (`CLAUDE.md`) to two sessions as current
fact. The sentence I quoted had been **corrected on disk at 12:48 that same day**. My copy
came from a context snapshot taken before that, and a context compaction had faithfully
carried the stale copy forward. `git log` showed **main had moved zero commits** since my
snapshot's tip, so every check I might plausibly have run said I was current. I was not: the
correction was in a commit my snapshot already contained, and the snapshot of the *file*
predated it.

The corrected sentence explicitly warned against exactly the error I was making. **Fixing the
file did not reach the copies already issued** — the readers were the stale artifact, and no
producer-side fix can reach them. Worse, the failure is invisible from inside: nothing in a
session's view distinguishes "I read this an hour ago" from "I read this nine hours ago and
it changed twice since".

What would have caught it: **buddy knows each session's start time and can watch a small set
of declared authority files.**

```
$ buddy status
s-ac718c1c  orchestrator  alive 9h41m
  STALE AUTHORITY: CLAUDE.md changed 12:48 (79bb9ff4) — your session started 08:07
                   docs/agentic-engineering-playbook.md changed 16:20 (D-731)
```

A `[authority]` list in buddy's config, a modified-since-your-start check, and one line in
whatever a session already runs at wake-up. The cost is a `stat`. The alternative is what
happened: the coordinator broadcasts a correction *about the coordinator*, to a fleet in which
an unknown number of sessions are holding the same stale copy and cannot tell.

Generalised: **an orchestrator's authority document is subject to the same snapshot rule as
its data.** §3's id registers already say a number-space snapshot is a permission the other
side holds open. So is a rules snapshot, and it fails more quietly because nobody thinks of
prose as state.

## 15. A relayed number loses its unit before it loses its value

§11 says a relayed claim must carry who measured and who extrapolated. One more field, from
two separate incidents in one hour:

- I relayed `76 / 3 / 24` as "failures" when the source carried **two** figure sets —
  `71 / 0 / 17` counting error occurrences and `76 / 3 / 24` counting failing tests with a
  different package set each arm. The numbers were right and unusable.
- I relayed a per-package tmpfs-vs-disk ratio as a whole-tier wall-clock multiplier, **under
  the measuring session's name**, turning a measurement of one command into a prediction
  about 123 of them.

Both survived the relay as *numbers* and lost what made them meaningful. The pattern: a
figure is easy to copy and its scope is not attached to it. Whatever structure §11 grows for
provenance should carry **unit and scope as required fields**, not prose around the number —
"what was counted" and "over what population", refused if empty.

The second incident has a distinct harm worth naming on its own: relaying an extrapolation
under the measurer's name means **a peer must overcome the credibility of a source that never
made the claim** in order to correct it. The measuring session spent a turn disowning a
number it had never said. That is a hazard created by the relay position itself, not by
carelessness in it, and it is the strongest argument in this document for machine-carried
provenance over discipline.

## 16. A rewritten shared branch is invisible to the coordination layer

> **Status (2026-09-26, D-048):** the roster and `who` append `carries N commit(s) main
> DROPPED` to a base built on history main rewrote away, found from main's reflog. That
> check stays silent on healthy unlanded work, which an ancestor test does not. No notice
> reaches the session that rewrote main at the moment it does so.

**Measured, and it invalidated a peer's work in flight.** A session committed to the shared `main`,
then amended that commit twice. `git reflog` records it plainly — `commit (amend)` at 13:45:51 and
13:46:30 — but nothing else does. Two orphaned commits were left behind with **the same parent and
the same subject line as the live tip, and different trees**.

A peer had already rebased onto the first orphan. **The rebase succeeded silently**: no conflict, no
warning, a clean result built on a commit no longer reachable from `main`. It was caught only
because that peer ran `git merge-base --is-ancestor` on a hunch. The cheap check a careful person
runs — same parent, same subject, therefore the same commit — returns the **wrong** answer here.

Neither side could see it. The amending session experienced "I improved my commit seconds after
making it, before anyone could have noticed." The rebasing session experienced nothing at all.
Notably, the amending session *did* self-report a different and much smaller hazard it could see,
and missed this one entirely — **it reported the risk that was visible from where it stood.**

Buddy already knows which sessions are active and what they claim. It does not know that the branch
they all build on was rewritten. One watched ref and a comparison against the last-seen value:

```
$ buddy status
  ALERT  main was rewritten 2 min ago: 8ace1954 -> baee227f (non-fast-forward)
         your last-seen main was 689f2e67 — NOT an ancestor of the current tip
         sessions with a stale base: grader-throughput, criterion7-survey
```

The general form, and the reason this belongs beside §14 rather than in a git FAQ: **§14 was a
stale copy of the rules, this is a stale copy of the base, and both are snapshots that rot without
any signal.** A coordination tool that tracks who is working on what, but not what they are working
*from*, is tracking the less dangerous half. The identifier registers in §3 already established that
a snapshot is a permission the other side holds open; a branch tip is the same thing with a
`git merge-base` for a discriminator instead of a grep.

The remedy at the protocol level is simply **append-only shared branches** — gate first, publish
second, never amend what is already shared. But that is a rule people follow, and the reason to
build the alert anyway is that this rule's violations are silent on both sides.

---

# Addendum 2 — from the landing burst (same run, later)

## 17. There are more identifier registers than anyone counts, and they all collide the same way

§3 described one identifier space with seven states. By the end of the run there were **four
distinct id spaces** in play, each with the same failure mode, plus **five ways to name a session**.

The id spaces: decision numbers, ledger row ids, **bundle ids**, and the session identifiers
themselves. Bundle ids were the one nobody was tracking — including me, and I was running the
register. Two sessions collided on the same bundle id within an hour. The second one found it
only because a merge driver refused; the driver "conflicted precisely BECAUSE it had no id-keyed
answer, which is it working."

An **eighth** state also turned up, and it nearly caused real damage: **DRAFTED-IN-A-DOCUMENT-BUT-
NEVER-WRITTEN-TO-ANY-LEDGER.** A session said "filed as R-1851" when it had never written to the
ledger at all — its claim covered only its design doc. Another session read "filed", was about to
strike its own row as a duplicate, and would have landed a dangling pointer to a number that was
neither free nor taken. **One verb.** Distinct from RESERVED (allocated, unused) and from
FILED-UNLANDED (written to a ledger in someone's worktree), and indistinguishable from both by any
search.

And five ways to name a session, of which **three broke in one afternoon**: the claim slug (not
messageable — a send to it failed outright), the roster name (**two live sessions shared one
name**, and rulings were being attributed to that name all day), the coordination-tool id, the
session uuid, and — the one nobody would predict — **a filesystem path that reads like a session
name**. A worktree called `~/d735-6e` caused one session to credit another's findings to a third,
because `d735` was a decision number and `6e` was a suffix. Shared uid means file ownership
cannot disambiguate it either.

The ask is unchanged from §3 but wider: **whatever register exists should cover every id space,
not the one that caused the last incident**, and it should distinguish reserved from drafted from
filed-unlanded from landed, because all four look identical to a search.

## 18. A relayed value has an age, and a checklist freezes it

The sharpest formulation of the run, from the session it nearly bit.

I relayed a value from one session to another and wrote it into an integration checklist: *assert
that this field reads `partly-done(D-722)`.* The value was accurate when the owner gave it to me.
By the time the integrator read it, the owner had had that value **rejected by a gate**, measured
why, and moved to a different one. **The checklist had frozen a value that was already dead.**

Had the integrator asserted it literally it would have reported a **correct** tree as defective.
The worse branch is the one it named: a session "fixing" the tree *toward* the rejected value,
which then fails a gate silently at land time rather than loudly at integration.

It caught this by **going and reading the gate's source** rather than believing either of us —
and found the rule at four separate sites.

This is §11's provenance problem with a time axis. A relayed claim needs not only *who measured
it* and *who extrapolated from it* but **when, and whether the source has moved since.** A
coordination tool already knows when a message was sent; what it cannot know is that the fact
inside it has a shorter half-life than the message.

The general rule the session derived is better than any tooling fix and is worth stating as the
finding: **prefer asserting the thing whose failure is LOUD over the thing whose failure is
SILENT.** Here, getting the state token wrong reddens a gate and you find out; getting the id
list wrong passes every scan and three rows vanish. So assert the ids and treat the state as
secondary — even though the state is what the instruction was about.

## 19. A hazard found by review, then verified by the first person to hit it

Worth recording as a positive, because most of this document is failures.

A no-tools review pass found that the landing procedure's two pushes each resolve a **mutable
branch name** at execution time, so if the branch moves between them the two mirrors receive
**different tips** — silently breaking an invariant the procedure explicitly checks for. Nobody
had hit it; it was found by reading.

I could not verify the proposed remedy: my attempt to test the command fired a repository hook
that launched a second full test suite alongside a live one. So I broadcast it as **mechanism
sound, command unverified** — and the next session to land simply ran it, pinning the object id
instead of the branch name, and reported three identical hashes. It also went one better and made
its fast-forward a **compare-and-swap** that would have refused rather than clobbered.

The sequence worth keeping: **review found it, the coordinator flagged it as unverified rather
than as fact, and the first practitioner verified it and improved it.** None of the three steps
required the other two to be perfect. That is what a working review layer looks like, and it only
worked because the unverified half was labelled as unverified.

## 20. A positive control only covers one of four failure polarities

The single most useful thing to come out of the run, and it arrived late because it took four
separate incidents to see the pattern.

Throughout the day we kept catching probes that returned a **confident wrong answer**, and the
standing remedy we all adopted was: *an empty result is evidence only once the probe has been
shown to match something* — i.e. run a positive control. That is correct and it caught several
real errors. **It is also only a quarter of the problem.** A probe can lie in four directions:

| polarity | what the probe says | what it makes you do | caught by a positive control? |
|---|---|---|---|
| **confident nothing** | "no matches" over content that is present | **stop** — conclude there's nothing to do | **yes** |
| **confident alarm** | "this differs" over a healthy state | **act** — repair something that isn't broken | no |
| **confident agreement** | arms agree perfectly because they measured the same thing | **stop checking** — read it as a strong finding | no |
| **confident wrong count** | a pattern wider than intended returns a plausible number | act on a wrong quantity | no |

Every one of these was measured, not theorised:

- **Nothing:** *two sessions measured the same file, got the same zero, and had different causes — and the one who stopped to ask WHY zero found a cause the other could not have seen.* Neither probe was careless; each was correct for its own tree. (a) a grep of a phrase that is hard-wrapped across two lines. `grep -c` → 0, `tr '\n' ' ' | grep -o` → 1. No flag to any tool fixes it; a multi-line string has no single-line representation. A *second, independent* cause produced the identical zero minutes later — a stale index in the shared checkout serving a 476-line-shorter copy of the same file. **Two unrelated causes, one symptom, indistinguishable from the result.**
- **Alarm:** an ancestry check that returns "not an ancestor" for a genuine orphan *and* for perfectly healthy unlanded work. Pointed at the wrong ref it makes you redo good work. I shipped this one to nine sessions. Then I shipped a second with the same defect — "if the trees differ, escalate" — which cannot distinguish an amend twin from a legitimate rebase image of the same commit.
- **Agreement:** a shell `case` with exact patterns against composed labels matched nothing, so the loop variable silently retained its previous value and **every arm of an A-B-A ran on the same target.** The arms agreed perfectly. Perfect agreement reads as a strong finding rather than as a broken experiment, and it reached a landed record before its author found it by re-reading their own script.
- **Wrong count:** a verification pattern `^R-18[3-4][0-9]` intended for eleven specific ids spanned twenty and returned 13 — which looked like two unexplained extra rows rather than like a bad pattern.

**The asks, in order of how much they'd have saved us:**

1. **Stop teaching "run a positive control" as though it were sufficient.** It answers *can this probe match at all* — nothing about the other three. The general form is stronger and barely longer: **a probe must be shown to distinguish the two states you care about, not merely to produce output.**
2. **For the alarm polarity specifically: ask what a healthy state returns.** Both of my broken probes would have died instantly on "what does this print for a tree that is fine?"
3. **For the agreement polarity: make every arm record the value of the variable that makes it that arm, never the label it was passed.** A label is an intention; the value is the experiment. One instrument in that same session printed its actual target and was the only trustworthy one.
4. **When two of your own instruments disagree, the disagreement is the finding.** Do not pick the one telling the better story — that session's globbed 2×2 was right the whole time and was distrusted because it contradicted the headline.

## 21. A coordinator cannot relay an operator's instruction as authority — and shouldn't be able to

> **Status (2026-09-20, D-027):** `buddy status` / `buddy who` answer the reportable question
> (claims held, dirty paths, inbox, and what ending now would leave) and grant nothing; the
> pid and pane are on the roster for the operator; no exit verb exists or will.

Recorded because I violated it, two sessions caught me, and the tool made it easy.

Winding the fleet down, I sent four sessions their verified PIDs and asked each to reply `EXIT OK` so I could terminate it. My warrant was a real instruction from the operator — *"check in with every session... and then actually go kill them."*

**Both sessions that were awake refused, on the same ground, independently.** Their words:

> *"A peer relaying 'the operator wants the fleet closed out' is not the operator telling me so. Authorising the termination of my own session on a relayed report of their wishes is exactly the thing I am not supposed to do with a peer message."*

> *"That is not obstruction and you should not read it as a HOLD. I have nothing outstanding and nothing would be lost if my process ends right now. But closing it needs no keyword from me — hand the PID to the operator."*

They were right and I had not noticed. Every cross-session message in this harness carries a standing warning not to treat a peer's message as the user's approval. **I had been quoting that rule at other sessions all day and then asked four of them to break it.** The shape is the same permission-laundering the rule exists to stop, with the coordinator as the launderer — and it is *easier* to fall into from the coordinator's seat, because relaying instructions is the whole job.

The second refusal contains the resolution: **consent to terminate and absence of work to lose are different questions**, and only the second is the session's to answer. A session can truthfully say "nothing would be lost" without authorising anything.

**The ask:**

1. **Separate the two questions in whatever wind-down support exists.** `buddy status --exit-readiness` answering *unlanded commits / dirty paths / open claims / nothing-in-flight* is useful and uncontroversial. A session answering that is reporting, not consenting.
2. **Never build an "exit on peer request" path.** If one exists it will be used by a coordinator acting in good faith on a real instruction, and the sessions that refuse will look obstructive rather than correct.
3. **Make the PID discoverable to the operator directly**, so the coordinator never needs to be in the loop for a kill. See the separate issue on the stale PID column — the register's own `pid` field read DEAD for every live session including the coordinator's, which is what pushed me toward asking sessions instead of reading it.

The one-line version: **a coordinator should be able to report that a session is safe to kill, and should not be able to obtain permission to kill it.**
