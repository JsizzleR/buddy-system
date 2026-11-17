# Resuming a fleet after a crash or reboot

Part of the `buddy-fleet` skill, moved out of its SKILL.md to be read before
you resume any lane after a crash, a reboot or a forced power-off (D-209). A
section named in parentheses is SKILL.md's unless this file has it.

Why it is written out: one coordinator resumed its 12 lanes 3 s apart after a
reboot, each told to "re-run whatever was in flight". Several started
whole-package Go test runs, a mutation battery and a `-race` run in the same
minute; free memory went from 6.6 GB to 0.1 GB, swap ran out, and macOS
suspended the terminal that held every lane. The machine froze twice in 30
minutes and needed a forced power-off each time (#144). A resume is a burst
by default: every lane it wakes has heavy work in flight. Every coordinator
on the machine is resuming too, so other repositories' lanes count.

1. **The coordinator first, alone.** The operator resumes you, or starts a
   successor to a coordinator that will not come back. Before any lane, read
   the state: `buddy sessions --all` (without it, a lane whose process died
   is not listed: each lane's row, live, `STALE`, `pid N GONE`, its last
   `prompt` size), `buddy who <id>` for each lane and run (claims,
   parked riders, waits), and the handoff file, if one was written. Nothing
   resumes until you know which lanes hold what.
2. **Taking a dead coordinator's claims**, on the operator's word that you
   are its successor. It cannot hand them on, so you act as it for one
   call: `buddy release <run> orc-<project> <parked riders> --to <your own
   id> --session <dead id>`. It is the same one-call hand-on as a handoff
   (`handoff.md` beside this file): each claim stays open with its scopes,
   riders stay PARKED and READY, and every wait follows (D-063). Then
   `buddy bye <dead id>`, which ends a row whose process is gone; one that
   says the session "stays live" is still running somewhere: stop and tell
   the operator, it is not dead. Then re-point its lanes: `buddy msg` each,
   naming you by label and full session id, and edit each brief file
   wherever it names the dead coordinator (D-192).
3. **Rank the lanes** by what waits on them: an item a run is waiting to
   land, one a peer waits on, then the rest. Resume in that order.
4. **One lane at a time** (Opening lanes): resume it, wait for its first
   report, and only then, if the memory check admits it, the next. A lane is
   resumed with the harness, with its own session id (`claude --resume <its
   session id>` in its tab), and a prompt that says: you were resumed after
   a crash; re-run at most ONE heavy job at a time (a test run, a done-check,
   a build battery), the one you were in, and wait for it before the next;
   say in your RESUMED report what you re-ran and what is still owed.
5. **A lane past its handoff size is not resumed.** Resuming it loads its
   whole context to write one note. Write the successor's note yourself,
   from three sources: its worktree (`git -C <worktree> status --short`, its
   commits since the base, `git -C <worktree> diff --stat`), the tail of its
   transcript (what it was running, what it promised, its last report), and
   its claims (`buddy who <its id>`). Start the successor, `<lane>-2`, from
   that note with the launch recipe (Opening lanes), hand it the old lane's
   claims in one call (`buddy release <slugs> --to <successor id>
   --session <old id>`, as in step 2), then `buddy bye <old id>`.
6. **What is not resumed**: a lane that had sent DONE (close it as
   Assigning, waiting, ending says), and a background job whose session died with it: a job
   gone without its final line is a failed run (D-131), re-run by its owner
   one at a time, never all at once.
