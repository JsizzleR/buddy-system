PASS within scope. No confirmed P1/P2.

- [cmdGate](~/Projects/buddy-system/internal/cli/cli.go:2152) now uses `routeErr` only to change the refusal reason; [governedAny](~/Projects/buddy-system/internal/cli/cli.go:2384) determines jurisdiction.
- [Regression controls](~/Projects/buddy-system/internal/cli/verification_gatetargets_test.go:230) credibly cover >128 KiB ordinary Bash and eight nested shells: ungoverned calls allow, home-ledger controls deny, and a target discovered before depth exhaustion retains enforcement.

Unseen scope beyond parser limits remains the stated cooperative limitation. Source review only; no builds, tests, or native qualification executed.
