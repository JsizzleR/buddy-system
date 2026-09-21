//go:build !darwin

package cli

// procInfo on a platform this project has not measured answers "cannot
// say" for every pid. That leaves every session UNBOUND — a hook-driven
// hello registers no process and bye ends the session as it always did —
// which is the documented degraded state (D-025), never a fence that fires
// on a process it cannot name. Linux is untested here; the /proc walk is
// the obvious implementation when somebody measures it.
func procInfo(pid int) (ppid int, names []string, born int64, ok bool) {
	return 0, nil, 0, false
}

// procGone: this platform cannot say, so nothing is ever "gone" here — a
// registration it cannot inspect reads as alive, the safe direction.
func procGone(err error) bool { return false }
