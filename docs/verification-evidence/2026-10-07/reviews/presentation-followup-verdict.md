PASS — no remaining P1/P2 found in the scoped source review.

- Receipt gaps retain listed approvals while marking pending state `unknown` and explicitly warning they may be stale.
- Adapter recipient evidence is incarnation-filtered and survives delivery; `Earlier` does not classify the current recipient.
- Both `printSent()` wake paths explicitly exclude adapter messages, including historical mail after a legacy successor registers.

The named regression tests cover these cases, including both `sent` modes. Tests were inspected, not executed.
