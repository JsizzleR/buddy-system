//go:build linux

package harness

import "golang.org/x/sys/unix"

// awaitExit returns once pid has exited, WITHOUT reaping it (WNOWAIT), so the
// zombie keeps the process-group id reserved until terminate's own Wait.
// Linux is not this project's measured platform; this keeps the hermetic tier
// building and behaving there.
func awaitExit(pid int) error {
	var info unix.Siginfo
	for {
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if err == unix.EINTR {
			continue
		}
		return err
	}
}
