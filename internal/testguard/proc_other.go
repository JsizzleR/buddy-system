//go:build !darwin

package testguard

// procOf on a platform this project has not measured cannot say, so the
// watchdog can identify no target there and signals nothing: only the
// in-process poll bounds an orphan. Linux is untested; /proc/<pid>/stat is the
// obvious implementation.
func procOf(int) (ppid int, born int64, ok bool) { return 0, 0, false }

// symbolize: there is no `sample` to annotate off darwin.
func symbolize(path, exe string) {}

// groupKids and execPath cannot say off darwin, so the holder arm finds nobody
// and sends nothing.
func groupKids(pgid, parent int, exe string) []kid { return nil }
func execPath(int) string                          { return "" }
func procAll(int) (int, int64, string, int, bool)  { return 0, 0, "", 0, false }
