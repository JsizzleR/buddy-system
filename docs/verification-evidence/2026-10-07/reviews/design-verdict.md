**Three CONFIRMED source defects; no PASS within this scope.**

1. **P1 — Dangling symlink chains bypass target claims.**  
   [gatetargets.go:64](~/Projects/buddy-system/internal/cli/gatetargets.go:64) expands dangling targets only from the original slice. With `free/a -> b`, `free/b -> ../docs/new.go`, nonexistent `docs/new.go`, and another session claiming `docs`, a `Write` targeting `/R/free/a` checks only `free/a` and `free/b` and allows. The final creation target is never adjudicated.  
   **Minimal correction:** resolve dangling chains with bounded, cycle-aware traversal through the final target; refuse unresolved placement instead of returning an incomplete target set.

2. **P2 — Quoted arguments are mistaken for shell commands.**  
   [patch.go:48](~/Projects/buddy-system/internal/cli/patch.go:48) treats shell punctuation inside quotes as command boundaries. In a governed repository, the read-only command `rg '(apply_patch|applypatch)' internal/cli/patch.go` matches `(apply_patch|` and is refused as a shell-routed patch. This contradicts D-073’s command-position boundary.  
   **Minimal correction:** identify command words with quote/comment/heredoc-aware shell tokenization. Keep patch payload adjudication out of this path.

3. **P2 — Draining adapter mail restores false legacy-delivery wording.**  
   [adapter.go:365](~/Projects/buddy-system/internal/store/adapter.go:365) excludes delivered messages, while [cli.go:3410](~/Projects/buddy-system/internal/cli/cli.go:3410) uses that backlog to classify the recipient. Queue and mark delivered the last adapter message for a live Codex worker without an endpoint, then run `buddy msg <worker> next`. The result says delivery waits for its next tool call, although its adapter never fetches that plain message. Existing incarnation-bound adapter evidence remains stored but is ignored.  
   **Minimal correction:** separate adapter-recipient evidence from undelivered counts, retaining classification from adapter messages for the current incarnation regardless of delivery status.

**Residual/unverified boundaries:** No additional confirmed P1/P2 emerged in descriptor admission, process-group ownership, observation-gap handling, approval presentation, or strict settings admission against the inspected fixture configurations. Native enforcement, tool path semantics, process regrouping, signal behavior, and matcher/timeout behavior remain unverified here.

This verdict is source evidence only. No builds, tests, native harnesses, web searches, edits, or reviewer launches were performed.

