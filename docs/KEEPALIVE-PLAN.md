# Keeping a waiting session's prompt cache warm

Status: proposal for review; nothing here is implemented, installed or wired.
Date: 2026-09-23. Source baseline: `a254f45`.
Scope: planning only. A design for one increment, the measurements that must
precede it, and what was considered and cut.

## The failure, measured

A session in a multi-session run parks itself waiting for something another
session is doing: a claim to be released, a serialized ~60-minute test tier
(field notes §1), a review slot, an operator ruling. While it waits it runs no
turn. The harness writes every prompt-cache entry on the **1h tier** (D-020;
re-measured below: 261M tokens written at 1h, 0 at 5m, over 14 days), so a
session that waits longer than an hour comes back to a cold cache and its next
request **re-writes the whole prefix at the 1h write price**, twice the base
input rate. A cache read costs a tenth of base (a fortieth on Fable 5.1), and
a read refreshes the entry's timer at no extra charge. The waiting session did
nothing wrong; nothing asked it to make one cheap request before the hour ran
out.

Counts only, from this box's transcripts for the last 14 days (135 transcripts,
48,775 assistant records; the script reads token counts and timestamps, never
content — the same rule `cost-report.sh` follows):

| Observation | Value |
| --- | --- |
| Requests that followed a gap of more than 60 minutes | 187 |
| Tokens those requests wrote into cache (cold restarts) | 54.3M |
| Cold restarts that re-wrote more than 100k tokens | 155 |
| Largest single cold re-write | 875k tokens |
| Gaps of 1–2h / 2–4h / 4–12h / over 12h | 74 / 34 / 51 / 28 |
| Gaps of 30–60 minutes (still hot, no re-write) | 98 |
| Cold re-writes by model: Opus 5 / Fable 5.1 / Sonnet 5 / Opus 5.5 | 50.2M / 2.7M / 1.0M / 0.3M |

At API list rates for the 1h write tier ($10/MTok Opus 5, $20 Fable 5.1, $4
Sonnet 5, $8 Opus 5.5) those 187 re-writes are about **$560 in 14 days**, most
of it Opus 5. On a subscription the same tokens draw down plan usage instead;
the ratio is what matters and it does not change.

Not all of it is recoverable, and the same transcripts say which part is. For
every cold restart, the reads a 50-minute keep-alive would have made instead
are `floor(gap / 50m)` reads of the same prefix (read rates: $0.50/MTok Opus 5,
$0.25 Fable 5.1, $0.20 Sonnet 5 and Opus 5.5):

| Gap bucket (all models) | Restarts | Cold write, tokens → list $ | Keep-alive reads, tokens → list $ | Saved |
| --- | --- | --- | --- | --- |
| 1–2h | 74 | 24.7M → $266 | 28.8M → $14 | 95% |
| 2–4h | 34 | 10.9M → $112 | 30.7M → $14 | 87% |
| 4–12h | 51 | 12.7M → $126 | 103.3M → $51 | 60% |
| over 12h | 28 | 5.9M → $59 | 171.5M → $85 | **loses** |

The 108 restarts under four hours are the target and the shape the operator
described: a session that will certainly be resumed, parked past the hour.
They cost about $378 in 14 days and would have cost $28 kept warm. The
over-12h row is the overnight case, and it is the evidence for the deadline
ceiling below: on Opus a keep-alive that runs past about sixteen hours has
spent more in reads than the re-write it prevents.

The second cost of the same parking is already on record. A parked session
runs no tool, so nothing drains its inbox (D-016, field notes §5c: "it cost two
sessions an hour"). The same cheap request that refreshes the cache is a tool
call, and a tool call is a `beat`, and a `beat` delivers the inbox. One
mechanism closes both.

## What the ledger already knows, and what is missing

Already there, per session: the cache tier and the clock of the turn that
wrote it (`session_context.cache_1h`, `tier_ms`), rendered as `cache 1h hot
48m`; the idle report (`session_idle`); the harness process and pane; the
undelivered inbox count. Nothing has to be observed anew.

Missing, and the whole of this proposal:

1. **A declared wait.** Which session is waiting, on what, since when, until
   when. Declared, never inferred (D-016): a refused claim is not a wait, an
   idle report is not a wait, silence is not a wait.
2. **A cheap check** that answers "has it landed, and is my cache still warm"
   from the ledger alone, in one tool call, on one to three fenced lines.
3. **A trigger** that makes an idle session issue one request before the hour
   is up. This is the only part buddy cannot own, and the design turns on
   that.

## The mechanism

```
session parks:   buddy wait --on <slug> [--on <slug>] [--until 3h] --note "then: rebase, run check.sh"
                 → row in session_waits; prints the deadline, the current cache clock,
                   and the exact keep-alive line to arm
session arms:    /loop buddy wait check          (self-paced; the harness's own scheduler)
each firing:     one tool call: buddy wait check
                 → refreshes the cache by existing (the request is the refresh)
                 → beat drains the inbox, prints dirty/authority notices as on any tool call
                 → verdict on one line: STILL WAITING (stand down, next check in 47m)
                                        LANDED (cleared; delete the scheduled check; your note)
                                        EXPIRED (cleared; delete the scheduled check; ask before waiting longer)
                                        NO WAIT (nothing registered; delete the scheduled check)
```

The wait is a row the session entered through the CLI, so it is authoritative
in the only sense this ledger has (invariant 4). The scheduled prompt is the
session's own act through its harness. Buddy never schedules, never wakes,
never types into a pane, and the charter's "do not propose a wake" (item 35)
stands: what is proposed is that a session which has declared it is waiting
arms its own timer, and that the ledger gives that timer a cheap, honest thing
to do when it fires.

### Why the trigger is the session's own scheduled prompt

Considered and cut, each for a named reason:

| Trigger | Why not |
| --- | --- |
| A daemon or launchd timer typing into the session's tmux pane (`pane` is on the roster row) | Terminal prompt injection is excluded by name in the anticipation plan's boundaries; it races the operator's half-typed prompt and permission dialogs; and it is a wake from outside the session, which the charter rules out. |
| A headless `claude -p --resume <id> "ping"` from a timer | Documented to continue the same session id and transcript, but whether the `-p` system prompt and tool list are byte-identical to the interactive request is UNCONFIRMED — and a prefix that differs in one byte makes every ping a full 2× cold write of exactly the tokens it was meant to protect, invisible from inside. It also registers a second harness process on the session id (D-025) and fires `hello`. A measurement-only experiment at most, never the design. |
| A `Stop` hook that refuses to let the turn end (exit 2) and sleeps | Makes a parked session report busy, which is the lie D-016 exists to prevent; the harness caps a foreground command at ten minutes, so it is six tool calls an hour; and this box's hooks block foreground sleeps. |
| The chat daemon pinging over IRC | Chat never controls and never reads the ledger from the daemon side (D-006, D-011). A room message does not make a request happen. |
| Buddy inferring a wait from a refused claim | A refusal is a fact about a claim, not about what the session does next. The refusal will *suggest* the command; it never registers the row. |

What remains is the harness's own scheduled-task feature, documented to fire
only while the session is idle, in the same conversation, with a one-minute
floor. That is exactly the shape needed, and it costs no new hook line.

### The period is 50 minutes, not 58, and it is paced from the ledger's clock

The 1h lifetime is measured from the **start** of the request that wrote or
read the entry; the ledger's clock is the **response** time (D-020 residual), so
the true expiry is earlier by the length of that turn. The harness's recurring
scheduler fires up to 10% of its period late, capped at fifteen minutes. A
58-minute period therefore fires anywhere up to 63.8 minutes after the last
one and pays the cold write it was armed to prevent — and from inside the
session the ping looks identical either way. 50 minutes fires by 55, with the
response-length margin on top.

Two ways to arm it; the first is recommended:

- **Self-paced** (`/loop buddy wait check`, no interval). Each check prints
  `next check in 47m`, computed as `tier clock + 1h − now − 8m`; the session
  schedules its next wakeup from that. This paces from the last request that
  actually happened, so an operator prompt in the middle of a wait resets the
  cadence for free instead of wasting a ping. An 8-minute margin covers a
  long thinking turn plus wake latency. If the harness's self-pacing clamp
  (one hour) or jitter turn out to differ from what is documented, the printed
  number absorbs it: the check reads the clock, it never assumes the period.
- **Fixed** (`/loop 30m buddy wait check`). Five-field cron cannot express a
  uniform 50-minute period (`*/50` fires at :00 and :50 — gaps of 50 and 10),
  so the uniform choice inside the window is 30 minutes: two reads an hour
  instead of 1.2, worst case 33 minutes. Correct and a little dearer; the
  fallback if self-pacing proves unreliable under measurement.

The check also reports whether the last ping **worked**: the ledger already
holds the turn's `cache_read` and `cache_write`, so `last turn read 398k
wrote 1k` is a warm ping and `wrote 401k` is a cold one, printed as
**COLD — the keep-alive missed**. A keep-alive that is silently failing (a
harness restart, a system-prompt change at a date rollover, an overage that
dropped the account to the 5m tier) is visible on its next check, on the
roster, and to `who`.

A session on the **5m tier** gets no keep-alive: `buddy wait` says so and
prints no arming line. At 0.1× per read, keeping a 5-minute entry warm costs
1.5× base per hour against a 1.25× re-write; it never pays.

## The wait register

`session_waits`, one open row per session, keyed to the incarnation that
declared it:

| Column | Meaning |
| --- | --- |
| `session_id`, `incarnation` | The declarer (D-003/D-025). A row from an earlier incarnation is not this session's wait, exactly as for `session_idle`. |
| `since`, `deadline` | Whole Unix seconds. `deadline` is required: default `--until 3h`, ceiling 12h, refused above it with the reason (the cost model's break-even on Opus is ~16h; nobody should wait through a night on a timer). Re-declaring replaces the row and prints what it replaced. |
| `note` | The session's own reminder of what to do when it lands, 512 bytes, fenced on read like every other value (invariant 9). |
| `last_check` | When `wait check` last ran — the keep-alive's own heartbeat, so `who` can date it and a dead keep-alive shows as `waiting 2h, last check 1h40m ago`. |
| `cleared`, `reason` | `landed`, `expired`, `cleared` (by `wait clear`), `ended` (session said bye; swept like any positively-ended session's rows, invariant 11). |

`session_wait_targets`: `(session_id, incarnation, claim_id)`, one row per
`--on`. Targets are resolved **once, at declaration**, to a claim id (D-013:
resolved before it is stored, refused when it names nothing). Storing the id
and not the slug is what makes "landed" stable: a released slug stops
resolving in the target namespace on purpose, and the answer would otherwise
change as history grows.

**Landed** is `every target claim has state != 'open'` — released, orphaned by
`hello`/`claim`/`sweep`, or force-swept. The release **is** the landing signal
in this system (D-030: coordination state is published as a claim). A wait
with no `--on` is a pure timer: it never lands, it expires, and it exists so a
session parked on an external ~60-minute tier keeps its cache and its inbox
without inventing a target.

Refused at declaration: a slug that names no open claim; the caller's own
claim; a deadline over the ceiling; a note over the cap (says what it cut,
D-030 review). Never refused by a wait: anything. A wait reserves nothing and
blocks nothing (invariant 10 applies word for word).

## What every side sees

- **The waiter**: `buddy wait` prints the row back, the cache clock, and the
  arming line. `wait check` prints the verdict. `hello` on a resumed session
  prints the open wait and says to re-arm if the scheduled check did not
  survive the restart (documented to survive `--resume`; the tool schema says
  session-only — measure, and word the line on the result).
- **The roster**: `waiting 1h12m` trails the row beside `idle N`, and it does
  not reset on a ping (every ping resets `idle`, which is the truth about the
  turn, so the wait's own age is the number an orchestrator reads).
- **`status` / `who`**: a `WAITING` line — `on claim api-refactor (held by
  s-4856919d, seen 3m ago) since 1h12m ago, deadline in 2h48m, last check 3m
  ago, last turn read 398k wrote 1k`. And on the **holder's** report: `WAITED
  ON   by 2 session(s): s-abc 1h12m, s-def 20m` — the `blocked-on` gap from
  field notes §9, closed from both ends.
- **`release`**: names the sessions waiting on the slug, so the releaser
  knows the release mattered and to whom. Informational; no message is sent.
- **A refused `claim`**: after the REFUSED lines, one line: `to be told when it
  frees: buddy wait --on <slug>`. Suggests; never registers.
- **`beat`**: when a wait has LANDED, one one-shot line on the next tool call,
  marked after the write exactly like the authority notice (D-028). A session
  that never armed a timer but happens to run a tool learns at once.
- **`msg` to a waiter** (D-032 arm, most-alarming-first stays): after the
  observation, `X is waiting on <slug>; last keep-alive check 3m ago` — an
  observation of the ledger, never "your message will arrive at the next ping".

## Cost model

Per hour of waiting, N = the prefix in tokens, at list multipliers (1h write
2×, read 0.1×, Fable 5.1 read 0.025×). One ping is one read of N plus a few
hundred tokens written; the ping turn itself adds about 250 tokens to the
context, permanently, which is under 3k over a twelve-hour wait.

| | Opus 5, N = 400k | Fable 5.1, N = 400k |
| --- | --- | --- |
| One cold re-write | $4.00 | $8.00 |
| One warm ping | $0.20 | $0.10 |
| Keep-alive per hour (1.2 pings) | $0.24 | $0.12 |
| Break-even wait length | ~16 h | ~66 h |
| Saved on a 65-minute wait | 95% | 99% |
| Saved on a 4-hour wait | 80% | 95% |

The predicate that matters is not the interval; it is **whether the session
will be resumed at all**. A session that is finished, abandoned, or asleep
until morning must not be pinged, and no timer can know that. The declared
wait with a required deadline is that knowledge, written down by the one
party that has it.

The harness's own tool text says that scheduling wakeups to keep a cache warm
is waste. For a session that would wake anyway, it is. For a session that
would otherwise sit past the hour and certainly return, the table above is
the arithmetic, and the wait row is the bound that keeps the exception from
becoming a habit: no open wait, no ping.

## Boundaries that must survive

1. Chat is the view; the wait is a ledger row entered through the CLI. Works
   with chat entirely absent (invariant 1).
2. No new hook line. `wait check` is a tool call like any other; the hooks it
   triggers are the ones already wired. Safety hooks unchanged.
3. A wait reserves nothing, refuses nothing, and is never a claim about the
   awaited session's intent. `WAITED ON` on a holder's report is information.
4. Nothing wakes anyone. The session arms its own scheduler; the operator can
   type the same `/loop` line into a parked pane by hand, which is the
   zero-code form of this feature and should be documented as such.
5. No row means unknown. A session with no wait row is not "not waiting"; it
   has not said.
6. Every value read back is fenced on one line (note, slugs, labels); every
   number carries its word (D-015); no stored verdict — `landed`, `hot`,
   `expired` are computed against the clock at read time, and only the
   one-shot marks are written.
7. Context cost stays first-class: the verdict is one to three lines, the
   ping turn is one tool call, and nothing about a wait is auto-injected into
   any *other* session's context beyond the roster trailer it already reads.

## Measurements that precede implementation

Each is a transcript read of token counts and timestamps, or a ledger read;
none needs new code, and the last three decide wording in the design.

1. **Positive control.** Park a session with a large prefix, arm `/loop 30m
   buddy status` by hand, wait two firings. The two ping turns' usage records
   must show `cache_read ≈ prompt` and `cache_creation` in the hundreds. If a
   ping shows `cache_creation ≈ prompt`, the prefix changed under it (find
   out what: date in the system prompt, tool list, settings) and the design
   needs a "why it missed" line before anything else.
2. **Negative control.** The same session left 65 minutes: the next record's
   `cache_creation ≈ prompt`. Without this, "it stayed warm" and "it was
   never going to go cold" look identical.
3. **Fire-time accuracy.** Timestamps of the ping turns against the schedule,
   for both the self-paced and the fixed form, over at least four firings.
   This sets the margin (8m proposed) and decides which form is recommended.
4. **Which hooks a scheduled prompt runs.** Whether `UserPromptSubmit` (busy)
   and `Stop` (idle) fire on a scheduled turn, read from `session_idle` after
   a ping. Decides whether the roster's `idle` resets on pings (it will if
   `Stop` fires) and whether the doc says so.
5. **Survival across `--resume`.** Arm, quit, resume, wait a period. Decides
   the `hello` wording.

## Delivery

One increment, in this order, each step with its tests watched to die under
mutation and a positive control beside every refusal:

1. **Store**: schema 9 adds `session_waits` and `session_wait_targets`;
   `DeclareWait`, `ClearWait`, `OpenWaits`, `WaitCheck` (evaluates targets,
   deadline, records `last_check`), `WaitersOn(claimID)`. Ended sessions'
   rows are swept with the rest.
2. **CLI**: `buddy wait [--on <slug>]... [--until <dur>] [--note <text>]`,
   `buddy wait check`, `buddy wait clear`, `buddy wait ls` (operator view of
   every open wait, oldest first). Dispatch-table entries with usage lines;
   `--help` answered before anything runs; unknown flags refused (D-031).
3. **Views**: roster trailer, `status`/`who` lines both ways, `release` names
   waiters, refused `claim` suggests the command, `beat` one-shot LANDED,
   `hello` prints the open wait, `msg` observation arm.
4. **Done-check**: `scripts/check-wait.sh`, invoked from `check.sh`: declares
   a wait against a fixture claim, releases it, proves `wait check` says
   LANDED once and NO WAIT after; proves an over-ceiling deadline is refused
   and that the same call under the ceiling succeeds; proves a wait on an
   unknown slug writes no row.
5. **Docs**: D-033 in `decisions.md`; a charter item; a CLAUDE.md decisions
   bullet; a README subsection under the roster with the cost table and the
   by-hand `/loop` form; the anticipation plan's resource-slot proposal gains
   `--on slot:<name>` as the obvious extension once slots exist.

## Qualification scenarios

| Scenario | Required outcome |
| --- | --- |
| Wait declared on an open claim, claim released 20 minutes later | The next `wait check` says LANDED with the note, clears the row; the check after that says NO WAIT; `beat` on any intervening tool call said LANDED exactly once |
| Wait on two claims, one released | STILL WAITING, naming the one still held and its holder's last-seen age |
| Wait declared with no `--on` | Never lands; expires at the deadline; each check prints the deadline |
| Deadline passes between checks | EXPIRED on the next check, cleared with reason `expired`, timer told to disarm |
| `--until 20h` | Refused, naming the ceiling; the same call with `--until 12h` succeeds |
| `--on` names a released slug, a slug that never existed, or the caller's own claim | Refused before any write; no row; the refusal names which |
| Awaited claim orphaned by `sweep --force` or by `hello` on an ended owner | Counts as landed (state is not open); the verdict says how it closed |
| Session says `bye` with an open wait, then a peer runs `hello` | The wait row is cleared as `ended`; `WAITED ON` no longer lists it |
| Session resumes under a new incarnation | The old incarnation's wait is not shown as this session's; `hello` says so and prints the re-declare line |
| Ping turn arrives while the session is PAUSED | The gate denies the Bash call as it should; the request still refreshed the cache; the next check after `resume` reports normally |
| Ping's usage shows a cold write | Check prints COLD, roster and `who` show `wrote 401k` on the last turn; nothing is auto-corrected |
| Session on the 5m tier declares a wait | Accepted as a wait (the inbox delivery is still worth it); no arming line; the check says why |
| Hostile note, slug or label | One fenced line per value, caps stated; no fabricated verdict line |
| Ledger unreadable at check time | The check fails loudly (it is not a safety hook, but it must not print STILL WAITING over an error) |
| Chat absent | Identical behaviour |

## Decisions to settle before implementation

1. **Self-paced or fixed as the recommended form.** Recommended: self-paced,
   subject to measurement 3. The fixed 30-minute form stays documented as the
   fallback.
2. **Deadline default and ceiling.** Recommended: 3h default, 12h ceiling.
   The ceiling is below the Opus break-even and above any single tier or
   review slot seen in the field notes.
3. **Whether a wait on a session target (not a claim) exists.** Recommended:
   no. "Waiting on a session" has no landing event the ledger can see; idle is
   transient, ended is the wrong event. Claims are where work is published.
4. **Whether `--on inbox` (land when anyone messages me) is in v1.** Recommended:
   cut. Every ping drains the inbox anyway, and the stand-down line already
   counts what arrived with it.
5. **Whether a wait on a git ref (`--on commit <ref>`) is in v1.** Recommended:
   deferred. The release is the landing signal here; a git probe on a
   50-minute cadence is cheap and could come later without changing the row.

## Cut, and why

- A buddy-side scheduler, daemon timer or launchd agent for pings: outside the
  session's authority over itself, and a wake.
- Pane injection, headless resume: see the trigger table.
- Auto-arming: buddy cannot call the harness's scheduler and should not want
  to; it prints the line.
- Inferring waits from refusals, idleness or chat: declared or nothing.
- A stored `hot`/`landed` verdict: computed at read time, as D-020 did.
- Estimated dollar figures in the verb's output: pricing is external and
  changes; the verb prints tokens and tiers, the README prints the table.

## Appendix: how the numbers were taken

Counts and timestamps only, no content, over `~/.claude/projects`. Run it
again before implementing so the baseline is current; the 14-day window is
the argument.

```sh
#!/bin/sh
# Counts only. Per model and gap bucket: cold restarts after a gap > 60 min,
# the tokens they re-wrote, and the READ tokens a 50-minute keep-alive would
# have spent instead (pings = floor((gap-1)/3000s) reads of the same prefix).
set -eu
days=${1:-14}
find "$HOME/.claude/projects" -type f -name '*.jsonl' -size +0c -mtime -"$days" -print0 \
| xargs -0 jq -r '
  select(.type=="assistant" and ((.isSidechain//false)|not) and .message.usage!=null)
  | [input_filename,
     (.timestamp | sub("\\.[0-9]+Z$";"Z") | fromdateiso8601),
     (.message.usage.cache_creation_input_tokens//0),
     (.message.model//"")] | @tsv' 2>/dev/null \
| sort -t"$(printf '\t')" -k1,1 -k2,2n \
| awk -F'\t' '
  function bucket(g) { if (g<=7200) return "1-2h"; if (g<=14400) return "2-4h"; if (g<=43200) return "4-12h"; return ">12h" }
  {
    f=$1; t=$2+0; cw=$3+0; m=$4
    if (f==pf && pt>0) {
      gap=t-pt
      if (gap>3600) {
        b=bucket(gap); k=m "\t" b
        n[k]++; tok[k]+=cw; pings=int((gap-1)/3000); ping[k]+=pings*cw
        if (gap<=14400) { n4[m]++; tok4[m]+=cw; ping4[m]+=pings*cw }
      }
    }
    pf=f; pt=t
  }
  END {
    for (k in n) printf "%s\trestarts=%d\tcold_write_tok=%d\tkeepalive_read_tok_at_50m=%d\n", k, n[k], tok[k], ping[k]
    for (m in n4) printf "%s\t<=4h\trestarts=%d\tcold_write_tok=%d\tkeepalive_read_tok_at_50m=%d\n", m, n4[m], tok4[m], ping4[m]
  }' | sort
```
