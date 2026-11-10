package cli

// The migration permit (issue #79, D-136): store migrates an EXISTING ledger
// only for the binary the hook lines run, because a ledger migrated by any
// other build is one the installed older hooks refuse (D-037), and the gate
// then denies every gated call in every worktree of that checkout. The rule
// and what was cut are in internal/store/migrate.go; this file holds the half
// that knows how a hook line names buddy: README's lines run $HOME/bin/buddy,
// read from HOME as the hook's shell expands it.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

func init() { store.SetMigrationPermit(hooksBinaryPermit) }

// hooksBinaryPermit: is this process's executable the same file as
// $HOME/bin/buddy? SameFile, so a symlinked ~/bin/buddy and the path a hook
// ran it by agree. Each "no" completes the refusal's "refused because ..."
// clause. HOME and the executable's path are values from the environment, so
// each is fenced here (invariant 9): the gate's home-repo arm renders an
// open's error with %v, unfenced (the refusal lens).
func hooksBinaryPermit() (bool, string) {
	home := os.Getenv("HOME")
	if home == "" || !filepath.IsAbs(home) {
		return false, "HOME is unset or relative, so $HOME/bin/buddy, which the hook lines run, cannot be found"
	}
	hooks := filepath.Join(home, "bin", "buddy")
	hq := fence.Line(hooks, 160)
	hi, err := os.Stat(hooks)
	if err != nil {
		return false, fmt.Sprintf("there is no %s, which the hook lines run", hq)
	}
	exe, err := os.Executable()
	if err != nil {
		return false, fmt.Sprintf("this process cannot name its own executable to compare with %s", hq)
	}
	ei, err := os.Stat(exe)
	if err != nil {
		return false, fmt.Sprintf("this process's executable (%s) cannot be compared with %s", fence.Line(exe, 160), hq)
	}
	if os.SameFile(hi, ei) {
		return true, ""
	}
	// The executable last: the gate cuts an open's error at 512 bytes in
	// some arms, and a `go run` path is long and the least useful part.
	return false, fmt.Sprintf("it is not %s, which the hook lines run (it is %s)", hq, fence.Line(exe, 160))
}
