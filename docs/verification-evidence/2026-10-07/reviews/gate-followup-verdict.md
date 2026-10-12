**P2 — Shell analysis limits deny calls outside Buddy’s jurisdiction.** At [cli.go:2155](~/Projects/buddy-system/internal/cli/cli.go:2155), `routeErr` causes an unconditional denial before `governedAny` checks for a ledger.

Precise failing input: a `Bash` hook whose cwd is an existing directory outside any repository, with command `"#" + strings.Repeat("x", 131072) + "\n"`. This valid shell comment exceeds the budget at [patch.go:58](~/Projects/buddy-system/internal/cli/patch.go:58), making `shellPatchCommand` return true and `shellPatchDirs` return an error. The gate therefore denies a harmless command despite feature-off status. Establish governing jurisdiction before turning analysis errors into refusals.

The four prior repairs appear correct within scope. Source review only; no builds, tests, or native launches performed.
