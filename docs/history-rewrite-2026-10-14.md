# Commit history rewrite, 2026-10-14

Every commit on `main` was rewritten on 2026-10-14, in one pass:

- Its author and committer are now `JsizzleR <27960665+JsizzleR@users.noreply.github.com>`, the
  GitHub no-reply address of the repository owner. The old commits used a machine-local address
  GitHub could not link to the account.
- Two commit messages lost a `Co-Authored-By:` trailer. The repository carries no tool
  attribution.
- A few words that early commits carried in test fixtures, docs and commit messages were replaced
  throughout with neutral ones, case preserved. The tree had been free of them since September;
  this takes them out of history as well.

Parents and dates are unchanged. 71 of the 118 commits have a different tree, and each differs
only at those words. Every tree from `9ea3c77d86` ("minor edits: test fixtures and comments;
hooks run an optional hooks.local hook first") on is byte-identical to the tree it replaced, the
tip's included. git filter-repo remapped hash references inside commit messages, but hashes
cited in tracked files (docs, comments, evidence `source_head` fields) still name the OLD
commits. They are left as written because they are historical records. Translate them with this
table (old -> new, oldest first).

| old | new | subject |
| --- | --- | --- |
| bd8d84fb44 | 4b8bb5ef97 | The Buddy System v0.1 |
| 7b6bbe30c1 | d6839e15c3 | chat: tail reads, per-session cursors, and addressed-message filtering (#3) |
| c69e882753 | 43b0c09f9b | chat: proactive alerting on addressed messages (#3 item 5) |
| b91ee3953f | 0f2a720097 | docs: the macOS cp-over-a-running-binary trap, measured during the item 5 rollout |
| 9498a20f44 | 3e0025aad7 | test: fix the racy conn double, and give check.sh a -race leg (#4) |
| 5626904712 | 66f5e2fc7c | test: gate the raw-send class that -race structurally cannot see (#4) |
| 9fea1482bd | bc7c085c69 | sessions: date an ended row by `ended`, and keep the last beat beside it |
| 76b2bdc04a | 7aac361040 | hello: name the room that exists, derived from the session label |
| f4962a8dfd | 0197573e7d | cost: narrow the context Buddy places into a model's window |
| 7c2bf3e543 | 50fd957c42 | presence: give every live session its own buddy in the room |
| e725e24111 | e448599536 | gate: check the pending commit against claims, at the boundary the tool gate cannot see |
| 5e3ca99726 | 9d8390c97b | test: neutral fixture names, and a settle budget sized for -race on CI |
| f183547fd3 | b22883adc2 | target: resolve pause/resume/msg targets, and refuse what names nothing |
| 78eadbabb5 | 9d3005f3c9 | review: run Codex on the default tier, not the fast one |
| 8e6d40b694 | ff932b051d | chat: ask whether the recipient is there, because nothing holds a DM |
| afe294ef89 | 3b5302949e | gate: fold with the ledger's rule, deny what cannot be placed, fence every peer value |
| f18e95ea59 | 05ff3a8551 | store: open without the write lock, index claim scopes, name the scope as claimed |
| bee6c35b01 | 1e36309d32 | ircwire: bound the reader before the line, not after; nothing outlives the read loop |
| 097fe6af47 | 7cca6d8399 | buddylist: close the dial that lands after its buddy was forgotten; fold like the ledger |
| 411c697069 | 57ca576dad | cli: one resolver for "where am I", and one git process per hook |
| 97f080afbf | d97938d967 | test: run the cli suite in parallel, bounded by what git can actually take |
| 14c93d4557 | 747124bd61 | alert: seek the rooms instead of aggregating the whole journal |
| 5f96721cfb | 101136f586 | test: make presence's negative assertions wait for evidence, not for 50ms |
| 8f2342eb0d | ef5cc15260 | buddylist: one table for the tools, one renderer for the row, one client for the socket |
| a1d4537dd2 | 6f49390be6 | scripts: one repo-root spelling, one throwaway-repo recipe, and pipes that cannot lie |
| ffe7b26919 | 5a32226ae1 | scripts: make check-fence.sh executable like every other done-check |
| c035c4c029 | 82a847bc61 | sessions: every age carries its word, and start order is expressible |
| 6aec687dad | 131e911a3a | sessions: the row says whether a peer can take work |
| c40fdb86e1 | f43c23561e | sessions: report what a peer's context is carrying, from its own transcript |
| 693b946321 | e80655c797 | sessions: report idle at the prompt, and never infer busy |
| 8136fa495d | 87854b8a9e | roster: close the six residuals D-015 and D-016 left behind |
| 00ca2922c8 | fff4e0fa25 | docs: two environment facts a daemon restart cost, one run each |
| 938755d13a | c88aaf7bb0 | claims: the whole conflict set, a partial release, and a sender that resolves |
| 324c846767 | 87a19ec1d8 | sessions: say whether a peer's prompt cache is still hot |
| 80ca1178af | 43fae34958 | docs: a field wishlist, an orchestration plan, and the review that revised it |
| 3882ee6a9e | 3d21188afb | wishlist: §13 named two causes and only one of them was there |
| 2244b5c233 | a213b4ad80 | msg: take the body from stdin, and cap what the recipient will actually see |
| 93ac593d2a | bd21ddac68 | claims: write down that a stale claim still refuses, and say so in the refusal |
| 7098c6d113 | 10482a1964 | whose: answer with the claim register too, and stop a blank column stealing the next one |
| a58c67cdaa | 4fff9aaa12 | chat: refuse a room the daemon neither serves nor remembers |
| de61602875 | 21123a2f99 | bye: fence the end by the process that registered the session, so a delayed one cannot end a live incarnation |
| 670bc31dda | 92592cb959 | claims: a holder that has said bye cannot refuse a peer, because claim orphans ended owners first |
| 98d730384e | 65b0200fdd | status, who: one session, every register, one screen — a report that grants nothing |
| b1cb7b42cd | 27b7d1e9aa | authority: tell a session once, on its next tool call, that a watched file changed after it started |
| f100e152a4 | d275cb967b | ids: an identifier register that never parses prose; and four findings from the safety-half code pass |
| e72bb3b0fb | 122384acb5 | review: peer text cannot open a line as a notice, a cap says what it cut, and take refuses what it would have dropped; D-030 declines the k/v store |
| a7d9d20689 | ebbc0630a4 | sweep: every verb answers --help before it runs and refuses what it does not understand; --dry-run is the real sweep rolled back (D-031, #23) |
| a254f452a4 | 485340142d | msg: the result line reports what the ledger holds about the recipient, never a prediction (D-032, #24) |
| de441b3a4d | 2be8444d5a | docs: keep-alive plan — a declared wait and a self-armed check keep a parked session's cache warm (proposal) |
| 1732a40f1f | 73cc243bd3 | wait: a declared wait and a check the session's own scheduler runs keep a parked session's cache warm (D-033, #26) |
| 7aed0a4b77 | 1e80158cee | hello: the SessionStart digest drains the inbox, inside the context cap (D-034, #25) |
| 3fcc8179c9 | 61b40a42dd | docs: /clear mints a new session id, so the D-034 drain does not cross it (measured) |
| 3c6a3de89a | 082ddc7520 | claim: a refusal under .buddy/slot/ says it refused a shared resource, not a file (D-035, #27) |
| 9fff31cd3f | f22ed2a168 | hello: the SessionStart claims list fits the digest budget, ahead of messages (D-036, #30) |
| 5eca860908 | 8c170e9012 | store: Open refuses a ledger stamped newer than the binary (D-037, #29) |
| 25cc8c0779 | b3f03af89a | roster, who: a session's base and where it stands against main now (D-038, #28) |
| 46dc9fddee | 3397cb2210 | msg: name the harness wake address for a quiet recipient; buddy still wakes nothing (D-039, #25) |
| 133761d337 | e9d8cb3689 | busy: drain the inbox into the prompt that opens a turn (D-040, #31) |
| cf24dc4560 | 2a29a04be4 | msg: the wake address rides the result line, so `\| head -1` keeps it (D-041, #32) |
| a2078fb0b6 | 7f909005b4 | claim --shared: a claim other shared claims may overlap, and nothing else (D-042, #33) |
| 031aeb7a0b | b8b0812a55 | msg --supersedes and buddy sent: a correction names what it corrects (D-043, #34) |
| 768ef5bcfc | 10e26c7333 | gate: a repository git refuses, with no ledger in it, is feature-off (D-044, #35) |
| b588f59098 | fd63615c77 | msg --lead / --measured / --relay: a message says what kind of claim it carries (D-045, #36) |
| b623ced416 | e529497383 | hello points at buddy --help, and a buddy skill says how the verbs fit (D-046) |
| 0bfd30d75f | beff34352f | wait: hello and beat list awaited claims inside a bound (D-047) |
| 3c9b6ab57f | 4430477be5 | base flags a rewritten main; one long run closes out several sessions (D-048, D-049) |
| 2313ef7be6 | becaaf9237 | install.sh: one install/upgrade path that rebuilds every binary the machine runs (D-050) |
| ae3c906639 | aa264714b5 | init says whether a repo's languages have a language server; startup-report measures it (D-051) |
| 8879ce4e9e | b9d6ebaac2 | D-051 second pass: an off-PATH go install's fix carries the PATH step; the listing budget closes the pipe |
| 3a11c30251 | af204ba2f0 | docs: a README a cold reader can act on; the manual moves to docs/USAGE.md |
| c5685c35c4 | 63b1195e80 | docs: D-051, init's language-server report and the startup report |
| c93bb095ba | 9ea3c77d86 | minor edits: test fixtures and comments; hooks run an optional hooks.local hook first |
| e416001b08 | 9b217522cf | startup-report: an edit counts in any worktree of the repo, and in a removed one beside it |
| 00da2e1978 | 476fab4920 | D-052: msg's wake address is also written to stderr (#42) |
| 2714249539 | 23e73f2a77 | D-052 second half: buddy sent names the wake of a queued quiet recipient (#42) |
| 9604481aac | 18f77835b1 | CLAUDE.md: the pre-push forkExec hang is an environment gotcha |
| 76a1f050fc | 37a544abc5 | D-053: a test binary that outlives its go test is ended from outside (#43) |
| 7a7c1d5a29 | 2a6af9cd40 | skill: if you hand out work, close the loop |
| 91f3a6a6b3 | 6c5534ec6d | wake-report.sh: do parked sessions come back warm, and what woke them (#45) |
| a80af43405 | 2a046931f0 | presence test: the positive control waits for the published connection (#48) |
| 09541ff9af | adf0b35673 | D-054: a park with no claim to name keeps warm on a timer wait (#44) |
| a88923ad00 | 994daa37e3 | D-055: a session that declared a handoff size is told when it has grown past it (#46) |
| 79d66eeb05 | 0fc13bf754 | D-056, D-057: coordinators open their own lanes; the suite never sees git's local env (#47, #49) |
| 3962ef79e7 | e3e6b64d59 | D-056, D-057: what the verbatim run and the Codex pass found |
| 94bcf62109 | 3dccbf1c6c | D-058: a claude --bg lane anchors at its own process, and its row names no pane (#50) |
| 9c94299ce1 | cc6674ba7f | D-060: the Stop hook cannot see the turn's reply; it says so, and the next prompt records it (#52) |
| 3a65c04bc8 | c208604a7d | D-059: a wake's text names its message and the second it was named (#51) |
| 711a5945c4 | 3affeccf0e | D-061: a -race forkExec can leave its child stuck before exec; the watchdog ends it (#53) |
| 7388c21f62 | a36bfb0ad1 | CLAUDE.md: the forkExec hang is ThreadSanitizer in the fork child (#53) |
| 473e66f38d | b2752a7ebf | skill: lanes' permission mode and pane-opening are the operator's standing decisions (#54) |
| 3db49cc9eb | 92572c18d1 | go.mod: pin toolchain go1.26.8; the -race fork hang is golang/go#79804 (#55) |
| df69c0ef96 | ff79c811fa | D-062: landing through an orchestrator is a recipe in the skill, over existing verbs |
| 3a0a450a13 | be23301287 | skill: a handoff accounts for its running jobs, and a lane past its size reports rather than hands off |
| 537a3fd5ff | 5c6de78c51 | D-063: a holder hands an open claim to another live session, and its waits go with it |
| ca738dfd80 | 9049f4e9e8 | D-064: the handoff size may be declared fleet-wide; its line speaks to lanes too, and install.sh says whether it is set |
| 04358eb0ec | 18f3f8bf3e | D-065: status and who count the shells still running under a session's harness process |
| f4af75d7d2 | c33e01c8ae | D-066: claim refuses a --scope holding a comma, and every listing renders a claim's scopes through one function |
| 766168abbb | eba66f0df8 | D-067: install.sh waits out the launch constraint a new daemon build meets, and a daemon failure no longer skips the steps after it |
| 6938fbebb5 | 136abee16c | testguard: the watchdog wall test skips off darwin, like its siblings |
| eb5a62f1ad | 1ce19a76e0 | D-068..D-077: Codex joins through the gate and native identity; a separate opt-in adapter launches, owns and recovers one worker |
| b3e88b7cbf | 90b140a566 | docs: the 2026-10-07 verification evidence and the findings probes, home paths replaced |
| 200e21c926 | 546f5c09a2 | D-077: the operator chose the narrow ownership contract (V-12, A-02) |
| 996eeda440 | fb19c28a09 | F-33, F-13: every checker self-tests on synthetic evidence, so check.sh runs them and the suite run binding is exercised by the tree |
| 624e1176e2 | b6aea8b4e8 | F-37: check-interrupted, check-recovery and check-uncertain bind their originals, the build and the native executables |
| cdbb25f6f0 | 0721aa31e5 | F-35: a forced close is recorded by the Mods and peers runners, refused by the delivery checker, and the TERM/HUP test gains its control |
| 2517b1ead6 | 80bf937a71 | F-36: the provider keeps its own timeline, and check-interrupted recounts the replay window and the held request from it |
| a0630f7003 | 12060c7705 | D-077: escaped descendants are observed, recorded and reported, never signalled, and a resume refuses while one lives (narrow contract) |
| 31d39654a3 | bbbf7793b2 | D-072: diagnose checks a hand-wired Codex hook layer for the BUDDY_HARNESS marker (F-31) |
| 701d999004 | 6317d7d851 | docs: the 2026-10-12 results in the verification report and the audit |
| 5dc6f7c17b | bb96a3df20 | skill: a finished lane says DONE, and an authorized orchestrator closes it at once |
| 316b3372bc | d9d231c1a4 | setup-clone: another spelling of this checkout's own .githooks is not somebody else's hooks path |
| 4a9392c234 | ab5e466349 | docs: A-07, the native rerun on the final bytes, and the 2026-10-13 installation |
| 0670151c5c | edaab59206 | start tier: independent checkers for the four suites, and the jobs suite judged by the narrow contract |
| abe37967a9 | d8ec96d6f5 | D-078: an adapter worker's claims renew from completed native writes, through the controller (F-26) |
| 91b0b532bf | ee62acb279 | checkers: claim renewal re-derived from the run's ledger images and journal (F-26, D-078) |
| 2333c972c5 | 51ff10a599 | D-079: native admission is a committed exact-artifact record embedded in the adapter |
| f086e40786 | 3061b8238d | D-080: a qualification candidate is bound by its typed sha256 and runs only against a loopback provider |
| 19ea6195dc | bfe9ad1a89 | qualify: runners bind their own binary as the candidate, checkers read the records, and qualify-release.py retains, runs and records |
