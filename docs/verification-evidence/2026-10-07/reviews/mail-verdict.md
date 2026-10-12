**CONFIRMED P2 — Earlier-incarnation backlog misclassifies a current legacy recipient.**

Trigger: session `S`, now legacy incarnation `B`, has one live registered process/socket and one undelivered adapter message captured for incarnation `A`. [AdapterBacklogOf](~/Projects/buddy-system/internal/store/adapter.go:382) correctly counts that message as `Earlier`, but [adapterRecipient](~/Projects/buddy-system/internal/cli/cli.go:3431) treats it as evidence about `B`.

Consequently, `buddy msg S plain` falsely says this recipient reads through an adapter, and [wakeAddr](~/Projects/buddy-system/internal/cli/wake.go:127) suppresses its valid legacy wake. Legacy `sent` rows and transfer notices inherit that suppression.

Minimal fix: exclude `Earlier` from current-recipient classification. Preserve suppression for historical adapter messages by explicitly checking `si.Adapter` at both [printSent wake paths](~/Projects/buddy-system/internal/cli/sent.go:133).

Source evidence only; no builds or tests run.
