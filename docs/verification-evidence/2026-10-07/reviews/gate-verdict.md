Four CONFIRMED findings from source review:

1. **P1 — A failed spelling suppresses a governed dangling target.** [gatetargets.go:65](~/Projects/buddy-system/internal/cli/gatetargets.go:65)

   Let `/S` be ungoverned, `/G` governed with another session holding `docs`, and `/G/docs/new.go` absent. Create these links, with `/S/physical/deep` existing:

   ```text
   /S/free/link      -> ../physical/deep
   /S/free/entry     -> entry
   /S/physical/entry -> /G/docs/new.go
   ```

   A `Write` from `/S` naming `/S/free/link/../entry` produces lexical `/S/free/entry` and physical `/S/physical/entry` candidates. Resolving the lexical cycle returns immediately, before resolving the physical dangling target. [cmdGate:2197](~/Projects/buddy-system/internal/cli/cli.go:2197) discovers only ungoverned directories and allows, although kernel resolution reaches the held `/G/docs/new.go`.

   **Minimal correction:** retain the resolution error, finish resolving every candidate, then return all discovered targets alongside the error.

2. **P1 — Static ANSI-C quoting bypasses shell-patch recognition.** [patch.go:172](~/Projects/buddy-system/internal/cli/patch.go:172)

   In an unpaused governed repo, `$'apply_\x70atch' < p.patch` invokes `apply_patch`, but `shellLiteral` returns unknown for the escaped quoted word. `shellPatchArgs` consequently returns false, and the pathless Bash call passes without claim checks. The same problem affects `bash -lc $'apply_patch < p.patch\n'`.

   **Minimal correction:** decode static ANSI-C escapes before command-name and shell-script recognition. These words require no variable expansion or execution.

3. **P1 — Recognized shell wrappers lose visible target jurisdiction.** [patch.go:204](~/Projects/buddy-system/internal/cli/patch.go:204)

   With cwd `/S` ungoverned and `/G/held.go` peer-held, this command passes:

   ```sh
   bash -lc 'cd /G && apply_patch <<EOF
   *** Begin Patch
   *** Delete File: held.go
   *** End Patch
   EOF'
   ```

   Recognition descends into `bash -lc`, but `shellPatchDirs` examines only the outer string for its anchored `cd` prefix. It therefore places `held.go` under `/S`, finds no ledger, and allows deletion in `/G`. A quoted leading command, `'cd' /G && apply_patch …`, has the same mismatch.

   **Minimal correction:** carry literal directory context through the same bounded AST traversal used for recognition, including supported wrappers and quoted command words.

4. **P2 — Combined lookup options falsely refuse a nonexecuting command.** [patch.go:123](~/Projects/buddy-system/internal/cli/patch.go:123)

   In an unpaused governed repo, `command -pv apply_patch` only performs command lookup. The implementation recognizes lookup mode only for exact `-v` or `-V`, skips `-pv`, and incorrectly classifies the operand as an invocation.

   **Minimal correction:** recognize `v`/`V` within valid clustered `command` options.

No additional residual questions. No builds, tests, or native qualification were run.
