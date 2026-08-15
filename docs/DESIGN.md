# Design notes

The distilled rationale behind the Buddy System — including the assumptions
that got refuted along the way, because those shaped it more than the plans
did.

## Why a ledger and not just the chat room

The first sketch was "agents coordinate by talking in a room." That dies on a
one-line observation: **announced is not locked.** A claim needs to be atomic
(two sessions can't both win), durable (survives a session dying mid-task),
and enforced where the conflict happens (the tool call), none of which a chat
message provides. An agent that joined late, didn't poll, or lost the message
to context compaction does the conflicting thing anyway. So the split became
the architecture: a transactional SQLite ledger owns claims and control; chat
is presence, visibility, and a place for the operator to talk — never
authority.

The reverse also holds: the chat stack can be entirely absent — not installed,
crashed, mid-migration between backends — without weakening safety. That
independence was load-bearing in practice: the chat backend was swapped from
AIM to IRC in one evening and the claims half never noticed.

## Why SQLite and not clever files

The first claims design was `O_EXCL` lock files with heartbeat mtimes. Review
killed it, correctly: `O_EXCL` reserves a *filename*, not content — readers
can see partial writes; concurrent sweeps have an ABA race (read old claim,
owner releases, a new claim lands on the same path, sweep deletes the wrong
one); and scope-overlap checking needs a transaction anyway. One SQLite file
in the repo's git common directory gives atomic publication, overlap
rejection, and generation tokens for free, and is shared by every worktree of
a checkout without ever appearing in `git status`.

Identity is `(session_id, incarnation)` — a random token minted at session
hello — because a session id alone can't distinguish "this session" from "a
dead earlier run of the same session," and process ids are recycled lies. The
end-of-session hook deliberately only *marks* a session ended and never
orphans its claims inline: a delayed `bye` racing a re-hello of the same
session id would otherwise orphan a live session's work. Orphaning happens in
`hello` and `sweep`, where it's idempotent and keyed to rows still ended when
they run.

Sweeps are conservative on principle: staleness *marks*, it never reaps. A
session that thinks for an hour without touching a file looks identical to a
dead one; only positively-ended sessions get cleaned automatically, and
anything uncertain requires an explicit `--force` from a human.

## What the gate honestly is

Enforcement is **cooperative**. The PreToolUse gate adjudicates the paths
tools declare — canonicalized, case-folded (case-insensitive filesystems will
happily alias a repo root two ways), resolved against the *target* repo's
ledger when an edit crosses repos — and it fails closed when a ledger exists
but can't be read. It cannot bind a process that bypasses the harness, and a
shell command's side effects are invisible to it. It's a seatbelt for agents,
not a sandbox against them. Pretending otherwise would be the worse design.

A related boundary that took discipline to hold: the gate distinguishes
"provably no ledger here" (feature off, stay silent) from "discovery failed"
(git missing from PATH, a dangling ledger symlink, unreadable database —
deny). Collapsing those two into one silent-allow arm is how safety features
quietly stop existing.

## What the journal is for

Neither OSCAR nor IRC gives you scrollback: a client sees only what was said
while it was connected. The concierge daemon therefore keeps a durable
journal — the room's memory — with monotonic sequence cursors that survive
daemon restarts. Two contracts matter:

- **It records what the server saw**, not what we hoped we sent. On IRC that
  required negotiating IRCv3 `echo-message`, because IRC doesn't reflect your
  own messages by default — without the echo, the daemon's own relays would
  silently vanish from the record. Outbound intent is additionally journaled
  to an `@sent` outbox *before* sending, so a send whose echo never returns is
  still on the record: submitted and confirmed are different facts.
- **A gap is reported, never smoothed over.** A cursor older than the
  retention horizon returns an explicit gap marker plus how to resume.
  "Nothing new" and "the messages you're asking about were deleted" must never
  be the same answer.

## Chat content is hostile input

Anything an agent reads back from a room is a prompt-injection vector — that's
not a hypothetical, it's the design assumption. Tool results fence chat
content as untrusted, render exactly one line per message (embedded newlines
become a visible marker, so a message can't fabricate extra records or a fake
cursor line), bound every field, and mark even membership lists as
peer-controlled. The transport layers hold their own line: the IRC client
refuses CR/LF/NUL at the final send boundary (so no text argument can smuggle
a protocol command), and the TOC backend converts the era's charset (CP1252)
at the wire so 2002 clients and modern UTF-8 both see clean text.

## Things measured that refuted the plan

A few assumptions died on contact with reality, and the design is better for
recording them:

- *"Joining a chat room creates it."* On the AIM-compatible server, a TOC
  join does **not** create a missing room on the public exchange (it errors
  internally); rooms had to be created via the management API. And the AIM
  client's own Buddy Chat dialog lives on a *different exchange* than the
  API-created public rooms — two rooms can share a name and never meet. The
  first live operator session was one person alone in a room because of
  exactly this.
- *"UTF-8 is text."* AIM 5.1 renders CP1252; an em dash sent as UTF-8 is
  mojibake on the operator's screen. Charset is part of the wire contract.
- *"Our own messages come back."* True on TOC (reflection is always on),
  false on IRC without `echo-message`. The journal's honesty depended on
  noticing.

## Security posture

Loopback by default, everywhere. The chat servers run unauthenticated only
under that condition; leaving loopback means turning on real auth first. The
ledger trusts the machine's user account: this is a single-operator
coordination tool, not a tenancy boundary. The operator's brake (`pause`) and
messages are authoritative *because* they enter through the local CLI — chat
text is never translated into control.
