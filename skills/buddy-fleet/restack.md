# Restacking a dependent lane (gh stack)

Part of the `buddy-fleet` skill, moved out of its SKILL.md to be read before
you restack a lane (D-171). A section named in parentheses is SKILL.md's
unless this file has it.

Start dependent work on the tree it needs now (step 6 of Landing several
lanes), and restack only if the code under it changes: lane B was built on
lane A's branch, and A's fold (an amend or rebase after its review) moved A,
so B still carries A's old commits. Moving B is one rebase, in B's worktree,
onto A from A's old tip (the sha of A's earlier READY); `gh stack` (GitHub's
CLI extension) runs it and finds A's old tip itself. B built on a run's tree
with an item since ejected has no branch A: use the plain line at the end,
once B is READY and idle (step 2) and the rebuilt tree is pinned, its sha
for `<A>` and B's current base for `<A's old tip>`. Either way the restack is
yours, done while both lanes are parked, and landing is unchanged (Landing
several lanes, step 3).

What it does, as measured in throwaway repositories (D-108, D-122):

- `gh stack rebase --no-trunk <B>` rewrites B's branch IN B's worktree: a
  clean lane's checkout moves under it; one with uncommitted changes
  (untracked files included) is refused ("uncommitted changes in worktree")
  and nothing is rewritten.
- **No gate checks what a restack produced.** Git runs no pre-commit hook on
  a rebase or a plain cherry-pick, and the commit gate only warns by default
  anyway. The tool-call gate sees no path in a `gh stack` or `git rebase`
  line; for `gh stack` it consults only the slot (step 1). Nothing checks
  the result but the lane's own re-run (step 6), or nobody.
- **It reads GitHub even when it pushes nothing**: `init` and `rebase` send
  read-only API queries carrying the operator's token. With the network
  denied (step 3's sandbox) each command does the same local work and exits
  0. A plain `gh stack rebase` FETCHES origin and can fast-forward local
  main; `push`, `submit`, `sync`, `merge`, `link` and a bare `unstack` act on
  GitHub.
- **A conflict stops in B's worktree** (exit 3), detached mid-rebase, and
  locks the stack catalog (`<git-common-dir>/gh-stack`, shared by every
  worktree) for EVERY stack ("a rebase operation is already in progress")
  until `gh stack rebase --abort`. While it is pending, `<git-common-dir>/gh-stack-rebase-state` exists and B's worktree
  reads `## HEAD (no branch)` in `git status --short --branch`; `gh stack
  view` does not show it.

The recipe:

1. **Claim the slot first**, one restack at a time, because the catalog is one
   per checkout:
   `buddy claim gh-stack --desc "restacking <B> on <A>" --scope .buddy/slot/gh-stack`.
   Hold it until the restack is over: until any abort (or continue) has
   finished, `<git-common-dir>/gh-stack-rebase-state` is gone, and B's
   worktree reads `## <B>` again. Then release it with its outcome (steps 4
   and 5). The tool-call gate refuses gh stack's rewriting subcommands
   (rebase, sync, push, submit, merge, link, unstack/delete, init, add,
   modify, checkout and alias; no flag is read, so `unstack --local`,
   `rebase --abort` and `rebase --continue` too) to a Bash call whose session
   does not hold `.buddy/slot/gh-stack` in every ledger the command reaches,
   and names the holder (D-108); `view` and the navigation commands pass. It
   reads the literal command only: a dynamic word (`gh stack "$op"`), a `gs`
   alias and a script file pass it, so the rule above is yours to keep, and
   the network discipline below is the recipe's, not the gate's.
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

   Chained, so the first refusal stops it (`init` of a stack already
   recorded exits 5, a conflicting rebase 3), and in subshells, so your own
   cwd stays put. The unstack runs from B's worktree because, run from the
   main checkout with two stacks recorded, it refuses ("branch main belongs
   to multiple stacks"). Check the result:
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
above. Never `gh stack rebase --remote self`: it rewrites A's checkout too,
adds a remote every worktree shares, and still reads GitHub (D-122). Never `gh stack
alias`, and never a `gs` wrapper or any other alias for it: the gate refuses
`alias`, and a `gs` command line is invisible to it. A subagent runs under
its parent's session id (D-099), so one of yours inherits your slot and may
run the restack for you; a lane's subagent holds what the lane holds, which
is no slot.

With gh stack missing or the catalog locked, the plain line does
the same rebase under the same rules, except the slot (it touches no
catalog): B's worktree must print nothing for
`git -C <B's worktree> status --porcelain --untracked-files=all`, then, in
the same sandbox and with the same two settings, so that no other branch
moves and nothing is stashed,
`sandbox-exec -p "$off" git -C <B's worktree> -c rebase.updateRefs=false -c rebase.autoStash=false rebase --onto <A> <A's old tip> <B> </dev/null`.
A conflict there: `git -C <B's worktree> rebase --abort`, then step 4's
report.
