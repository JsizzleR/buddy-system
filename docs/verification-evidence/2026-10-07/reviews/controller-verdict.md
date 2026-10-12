1. **CONFIRMED P2 — A missed cancellation is reported as a pending approval.** [controller.go:225](~/Projects/buddy-system/internal/harness/controller.go:225)

   Exact sequence: successfully observe approval `"a"` for turn `"t"`, then observe its matching `approval_resolved` event while `ledger.Inspect` fails once. The failure returns before removing `"a"` ([line 615](~/Projects/buddy-system/internal/harness/controller.go:615)). Although `Observe` sets an observation gap, lines 225–228 override `pending_approval: unknown` with `observed` and assert “the listed approvals are pending.”

   Native cancellation has already removed the request ([claude.go:701](~/Projects/buddy-system/internal/harness/claude.go:701)). Subsequent receipts nevertheless retain that false pending assertion; attempted replies are refused by the backend. Retained approvals must be presented as potentially stale when an observation gap could contain their cancellation.

No other confirmed P1/P2 found in the scoped V-02 checks or approval-detail presentation. Residual verification: tests were inspected but not executed; native behavior was not qualified. Codex `ReadTurn` remains deliberately unsupported.
