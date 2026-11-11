# Review charter: test-infrastructure GIVENs (full text)

The GIVENs only the test watchdog rests on, appended by `scripts/codex-review.sh` unless `CODEX_DIFF` names a
diff with no path in the test-infrastructure area. The main charter's one-line
stub of each binds as written; this is the full text (D-158).

55. **A test binary that outlives its `go test` is ended from outside (D-053, #43).**
    `internal/testguard.Arm()` opens `internal/cli`'s TestMain: an in-process `Getppid` poll
    exits a healthy orphan with 3 in about a second; a watchdog process (the same binary
    re-exec'd, pipe EOF for the target's exit, pid + kernel start time for identity) ends one
    still orphaned three polls later, or at twice `-test.timeout`, with a symbolized `sample`
    to `$TMPDIR`, SIGQUIT, then SIGKILL, and reports on stderr LAST (it may be the dead pipe).
    MEASURED: the timeout panic exits fine on a dead stderr; a stop-the-world waiting on an
    unpreemptible goroutine reproduces #43 and runs no Go code at all (so no in-process wall
    can fire); Go drops SIGXCPU and darwin sent no SIGKILL at the hard RLIMIT_CPU; `go test`
    binaries have no symbol table. Do not propose RLIMIT_CPU, an in-process AfterFunc wall,
    or `SetTraceback("crash")` (it would SIGABRT every panicking test). #43's cause is
    unknown; the watchdog's sample will name it.

63. **A `-race` forkExec can leave its child stuck before exec, and the watchdog ends it (D-061,
    #53).** MEASURED: the child spins in ThreadSanitizer's slot lock, held by a parent thread
    at fork, and never execs. The parent waits in `readlen` to the timeout. It is `-race` only (measured), not
    git, PATH or the hook. The child keeps the watchdog's pipe
    (close-on-exec acts only at exec), so the watchdog has a holder arm. Target gone and pipe held
    means it ends the target's same-binary children, orphaned, same start time, in its own
    process group, seen while the target lived, never itself. It holds its executable's fd so
    the sample symbolizes after go test deletes the binary. Do not propose a retry loop, a
    timeout knob, a box-wide process scan, or treating D-053's stop-the-world toy as the cause.
