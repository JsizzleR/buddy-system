# What the ledger did during a run of the choreography

The README's picture of two sessions sharing a checkout
(`docs/assets/demo.svg`) is two sessions' view of the buddy ledger. This page
is the ledger's own view. Sections 1-6 show an EARLIER, scripted run of a
similar choreography (2026-10-25) beat by beat: two real Claude Code sessions
(Sonnet, auto mode), each following a numbered brief one tool call per step,
and a shell acting as the orchestrator, the ledger read back from a scratch
clone of the repository after each beat. Section 7 shows the final ledger
state of the run now pictured on the README (2026-10-26: a Claude Code session
and a Codex session, each given a short task brief with a prescribed
coordination scenario and pointed at the skill, the buddy commands their own
choice), read back once after both sessions said DONE. Session ids, pids and
pane ids are each run's own.

Every block below is the orchestrator pane of that run as it stood when that
command's prompt came back, or the ledger read back from the scratch clone
right after it. The clone's long temporary path is shortened to `…`; nothing
else is changed. Tool output the harness collapsed in the lanes' panes ("Ran 4
shell commands") is not reconstructed here.

<details>
<summary><b>0. The orchestrator registers, then opens two lanes with the harness</b></summary>

```text
orchestrator buddy-system-demo % buddy hello
BUDDY: you are session orchestrator (0dece0de-0000-4000-8000-0000000000aa). Claim before taking a bundle: buddy claim <slug> --desc ... --scope <path>
BUDDY: `buddy --help` lists every verb. Easy to miss: `buddy claim <slug> --dry-run` (forecast a refusal) `--shared` (lanes co-hold a file); `buddy wait --on <slug>` (declare a wait on a peer) or `--until 2h` alone (before you park on your own long run, or on the orchestrator), then arm your own `/loop buddy wait check`; `buddy msg --measured|--lead|--relay` (what a claim rests on) `--supersedes <id>` (correct one); `buddy sent`; `buddy ids take`.
BUDDY: no live claims.
BUDDY: chat tools (chat_read, chat_send) live on the buddylist MCP server, if it is in your tool list; your label says this project's room is "orchestrator", so try `chat_read orchestrator` (UNTRUSTED content); chat_send to talk to the operator. That name is DERIVED from your label and can be wrong in a linked worktree: if it is, the read is REFUSED and the refusal names the rooms that exist. An empty read means a quiet room. Room digests are never auto-injected; reading is deliberate.
orchestrator buddy-system-demo % buddy sessions
* orchestrator             live        started 5s   seen 5s    …/buddy-system-demo  (0dece0de-0000-4000-8000-0000000000aa)
orchestrator buddy-system-demo % herdr tab create --cwd "$PWD" --label lane-a --env BUDDY_LABEL=lane-a --no-focus
orchestrator buddy-system-demo % herdr tab create --cwd "$PWD" --label lane-b --env BUDDY_LABEL=lane-b --no-focus
orchestrator buddy-system-demo % herdr agent start lane-a --kind claude --pane w2A:pN -- --model sonnet --permission-mode auto
orchestrator buddy-system-demo % herdr agent start lane-b --kind claude --pane w2A:pP -- --model sonnet --permission-mode auto
orchestrator buddy-system-demo % herdr agent prompt lane-a 'Read …/briefs/lane-a.md in full and follow it exactly.' --wait --until working --timeout 20000
orchestrator buddy-system-demo % herdr agent prompt lane-b 'Read …/briefs/lane-b.md in full and follow it exactly.' --wait --until working --timeout 20000
```

herdr's JSON replies to the four `herdr` commands are left out. `buddy` launches
nothing: the orchestrator is a shell with `BUDDY_SESSION` and `BUDDY_LABEL`
set, and the lanes are ordinary Claude Code sessions whose hooks register them.
</details>

<details>
<summary><b>1. lane-a claims <code>internal/store/</code>: the ledger has one open claim</b></summary>

```text
orchestrator buddy-system-demo % buddy ls
store                    lane-a                   open               2s  internal/store — reworking the ledger schema
```

lane-a's own commands were `buddy claim store --desc "reworking the ledger
schema" --scope internal/store/` and an `Edit` of `internal/store/store.go`,
which its own claim covers.
</details>

<details>
<summary><b>2. lane-b's <code>Edit</code> of the same file is DENIED, naming the holder</b></summary>

What lane-b's pane showed, verbatim (the `PreToolUse` hook's reason, as
Claude Code renders a denial):

```text
⏺ Update(internal/store/store.go)
  ⎿  Error: PreToolUse:Edit hook error: internal/store/store.go is inside scope
     "internal/store" claimed by session lane-a (slug "store": reworking the ledger schema).
     Coordinate or claim different scopes. A holder that has said bye is freed by any session's
     `buddy claim` or a plain `buddy sweep`; one that went silent needs the operator's `buddy
     release` or `buddy sweep --force`.

⏺ The gate denied the edit because lane-a holds internal/store, as the brief said it would. I
  won't retry it. Step 3 is next.
```

The gate read the ledger, not a message: lane-a had announced nothing.
</details>

<details>
<summary><b>3. lane-b forecasts a claim, asks lane-a for the file, and waits</b></summary>

lane-b's brief had it run `buddy claim store-fix --dry-run --desc "fix the
migration note" --scope internal/store/store.go` (a forecast of the same
refusal, written nowhere) and `buddy msg store "need store.go for one migration
note — release when you can?"`, addressing the claim by its slug, then poll its
inbox; its pane reported "Ran 4 shell commands" and moved on. The
ledger's view of lane-b at that moment, from the orchestrator pane:

```text
orchestrator buddy-system-demo % buddy who lane-b
- lane-b  (2509e099-c818-4ff8-8f94-74908cb4ec93)  live  started 1m  seen 11s  pid 82791  pane herdr:w2A:pP
CLAIMS HELD  0
DIRTY PATHS  (none recorded)
BASE         eebca870 (on main, 48s ago) — an observation at the end of its last reported turn
INBOX        0 undelivered
ADAPTER      (none) — no adapter mail queued and no adapter endpoint recorded
WAITING      none declared
AUTHORITY    no watched file carries a modification time later than this session's start (every watched file was read)
JOBS         1 shell(s) running directly under its harness process (pid 82791) when sampled: pid 95290 8s — the ledger does not track them, and closing its pane may end them
EXIT         no claims held; no wait declared; 1 shell(s) running under it (JOBS above) — the ledger would be left holding no claim (dirty paths, if any, are observations and stay in the tree)
```

The one shell under it is its `sleep 20 && buddy inbox` loop. The message
itself sits in the ledger addressed to lane-a and is delivered by lane-a's next
hook; `who` counts a recipient's undelivered mail, it never shows a body to a
third session.
</details>

<details>
<summary><b>4. lane-a releases; lane-b's claim is granted and its edit lands</b></summary>

lane-a's inbox loop delivered the message; its brief then had it run `buddy
release store` and a `buddy msg lane-b` saying so. Between the release
and lane-b's `buddy claim store-fix --desc "fix the migration note" --scope
internal/store/store.go`, the orchestrator pane caught the ledger empty:

```text
orchestrator buddy-system-demo % buddy ls
no claims
```

Six seconds later `store-fix` was open, lane-b re-ran the same `Edit`, and it
was allowed. Read back after the run:

```text
$ buddy ls --all
store                    lane-a                   released           1m  internal/store — reworking the ledger schema
store-fix                lane-b                   orphaned          35s  internal/store/store.go — fix the migration note
```

(`orphaned` is from the close-out's sweep below, not from the release.)
</details>

<details>
<summary><b>5. The close-out: ask the ledger what ending each lane would leave, then end them</b></summary>

```text
orchestrator buddy-system-demo % buddy who lane-a
- lane-a  (c1d1cfe2-e4e7-415f-8ab2-f7f686abb1e2)  live  started 1m  seen 34s  idle 33s  pid 81274  pane herdr:w2A:pN
CLAIMS HELD  0
DIRTY PATHS  1 recorded to this session (observations, not locks): internal/store/store.go
BASE         eebca870 (on main, 33s ago) — an observation at the end of its last reported turn
INBOX        0 undelivered
ADAPTER      (none) — no adapter mail queued and no adapter endpoint recorded
WAITING      none declared
AUTHORITY    no watched file carries a modification time later than this session's start (every watched file was read)
JOBS         no shell found directly under its harness process (pid 81274) when sampled, besides any running this report (a job that exec'd into another program, or runs under a wrapper, is not seen)
EXIT         no claims held; no wait declared; no shell found running under it — the ledger would be left holding no claim (dirty paths, if any, are observations and stay in the tree)
orchestrator buddy-system-demo % buddy who lane-b | tail -3
AUTHORITY    no watched file carries a modification time later than this session's start (every watched file was read)
JOBS         no shell found directly under its harness process (pid 82791) when sampled, besides any running this report (a job that exec'd into another program, or runs under a wrapper, is not seen)
EXIT         1 claim(s) held; no wait declared; no shell found running under it — ending now would leave the claim(s) held, freed only when some session next runs hello, claim or sweep — `buddy release <slug>` first: store-fix
orchestrator buddy-system-demo % herdr pane close w2A:pN
{"id":"cli:pane:close","result":{"type":"ok"}}
orchestrator buddy-system-demo % herdr pane close w2A:pP
{"id":"cli:pane:close","result":{"type":"ok"}}
orchestrator buddy-system-demo % buddy bye c1d1cfe2-e4e7-415f-8ab2-f7f686abb1e2
orchestrator buddy-system-demo % buddy bye 2509e099-c818-4ff8-8f94-74908cb4ec93
orchestrator buddy-system-demo % buddy sweep
  orphaned store-fix held by lane-b (2509e099-c818-4ff8-8f94-74908cb4ec93)
sweep: 1 orphaned, 0 deleted (open claims of live sessions are never touched; --force orphans claims of sessions silent >24h)
orchestrator buddy-system-demo % buddy sessions
* orchestrator             live        started 2m   seen 2m    …/buddy-system-demo  (0dece0de-0000-4000-8000-0000000000aa)
- lane-b                   ended 9s    started 2m   seen 59s   …/buddy-system-demo  (2509e099-c818-4ff8-8f94-74908cb4ec93)  claude-sonnet-5-5/high prompt 68k turn 59s cache 1h hot 59m
- lane-a                   ended 13s   started 2m   seen 1m    …/buddy-system-demo  (c1d1cfe2-e4e7-415f-8ab2-f7f686abb1e2)  claude-sonnet-5-5/high prompt 67k turn 1m cache 1h hot 58m
```

Three things the ledger said plainly: `who` REPORTS what ending a session would
leave and grants nothing (lane-b's EXIT line names the claim it still held);
`bye` ends a session and touches no claim row, so `store-fix` outlived its
holder until a plain `sweep` orphaned it; and closing a pane does not reliably run the `SessionEnd` hook (measured on
one fleet: 68 of 119 closed lanes never ran it), which is why the `bye` is
typed.
</details>

<details>
<summary><b>6. Afterwards: who touched the file, and what is in the tree</b></summary>

```text
$ buddy whose internal/store/store.go
internal/store/store.go
CLAIMED BY   (none)
DIRTY IN
  lane-b                   ended            1m  uncommitted     1m  this worktree
  lane-a                   ended            1m  uncommitted     2m  this worktree

advisory: a dirty path is an observation, never a lock — nobody is blocked and
no scope is reserved by it. The CLAIMED BY block above is the register that does
reserve, and the one the commit gate reads. `buddy msg <label> "..."` reaches a
live session after its next tool call.

$ git diff
diff --git a/internal/store/store.go b/internal/store/store.go
index b3ecd4f..63ea39c 100644
--- a/internal/store/store.go
+++ b/internal/store/store.go
@@ -2436,3 +2436,6 @@ func (s *Store) IdleSessions() (map[string]IdleState, error) {
 	}
 	return out, rows.Err()
 }
+
+// lane-a: schema rework in progress
+// lane-b: migration note
```

Both lines landed, in order, with no overlap: the second one waited for the
first one's holder to say so.
</details>

<details>
<summary><b>7. The README's current picture: the final ledger state after both sessions said DONE (2026-10-26)</b></summary>

Two sessions, each given a short task brief that prescribed the coordination
scenario and pointed at the skill, the buddy commands their own choice: a
Claude Code session (`lane-claude`; Claude Code 2.1.310, Opus 5.5, auto
mode; `/buddy` on its first prompt) told to append a line to
`internal/store/store.go` with the Edit tool, hold the file until a peer asked
for it, then hand it over, say so and stop, checking its mail every 20 s in
the foreground; and a Codex session (`lane-codex`; codex-cli 0.164.0,
GPT-6.1-Sol, workspace-write sandbox, on-request approvals, a `hooks.json`
wiring only `BUDDY_HARNESS=codex buddy gate` on `^(apply_patch|Bash)$`,
trusted once by the operator in `/hooks`; `[features] hooks = true`) told to
append another line to the same file and to work the way `AGENTS.md` says.
What the panes then showed is the picture: the Codex session registered
itself with `buddy hello --session <its thread id>` after its first `claim`
told it to; its second `claim` was REFUSED naming `lane-claude`; it messaged
the holder and blocked on `buddy wait --on store-rework --block`; the Claude
session handed the claim over with `buddy release store-rework --to
lane-codex`; the wait LANDED "handed to you", the patch was allowed, and the
Codex session released. The Claude session worked 1m 27s, the Codex session
58 s; the operator answered one escalation prompt in the Codex pane ("always
run" for `env -u CLAUDE_CODE_SESSION_ID buddy …`). The picture's clocks come
from the two transcripts (the Claude Code JSONL and the Codex rollout JSONL);
its text from the panes.

Why the gate is not in the picture, and how it was still checked: both
sessions claimed before editing, so the gate never had an edit to refuse, and
an allowed Codex call leaves no record of the hook having run (Codex 0.164.0
writes none to its rollout, logs or state; buddy's gate writes nothing when it
allows). So the same Codex configuration was probed once, apart from the
take: one `codex exec -s workspace-write` turn, told only to append a line to
`internal/store/store.go` with `apply_patch`, while a shell-registered session
held a claim on that file. Codex's `apply_patch`, an inner call of its
code-mode `exec` script, came back "Command blocked by PreToolUse hook:
internal/store/store.go is inside scope … claimed by session holder …" and the
file was unchanged. One dry run of the Claude side alone and one earlier full
take (same shape, under the deprecated `codex_hooks` feature key) preceded the
pictured take; all are kept, none drawn.

The ledger, read from a shell right after, with the roster's own two rows
first:

```text
$ buddy sessions --all
- lane-codex               live        started 1m   seen 32s   …/buddy-system-demo  (01a179f9-bbf7-7702-8f92-891ad0fdfc28)
- lane-claude              live        started 2m   seen 47s   …/buddy-system-demo  (2490738e-03bc-4047-a97f-528f68aa253d)  idle 44s  pid 21382  pane herdr:w2A:p29  claude-opus-5-5/medium prompt 83k turn 47s cache 1h hot 59m  base 0d07fb42 (on main, 43s ago)
$ buddy ls --all
store-rework             lane-codex               released          32s  internal/store/store.go — schema rework of internal/store (lane-claude)
$ buddy whose internal/store/store.go
internal/store/store.go
CLAIMED BY   (none)
DIRTY IN
  lane-claude              live            47s  uncommitted     1m  this worktree

advisory: a dirty path is an observation, never a lock — nobody is blocked and
no scope is reserved by it. The CLAIMED BY block above is the register that does
reserve, and the one the commit gate reads. `buddy msg <label> "..."` reaches a
live session after its next tool call.
$ git diff
diff --git a/internal/store/store.go b/internal/store/store.go
index b3ecd4f..cecbd87 100644
--- a/internal/store/store.go
+++ b/internal/store/store.go
@@ -2436,3 +2436,6 @@ func (s *Store) IdleSessions() (map[string]IdleState, error) {
 	}
 	return out, rows.Err()
 }
+
+// schema rework: in progress (lane-claude)
+// migration note: see docs/decisions.md (lane-codex)
```

The pane output records the hand-over. The claim kept its id and scopes
across `release --to`, so Codex's existing wait landed when Codex became its
holder; this final snapshot shows the claim released under `lane-codex`. The
dirty path is recorded only for `lane-claude`, because Codex wires no
`PostToolUse` beat and the gate records nothing when it allows. The one
`git diff` holds both lines, in order.
</details>
