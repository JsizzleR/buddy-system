PASS within scope. No confirmed P1/P2 defects found.

The fixes address cancellation during blocked receipt output, bound version-probe cleanup while preserving process-group ownership, and suppress disconnect reporting caused by normal shutdown. Regression tests credibly exercise the three failures and relevant positive controls.

Source review only; no builds, tests, or native launches performed. The stated limitation for regrouped descendants remains unchanged.
