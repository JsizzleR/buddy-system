//go:build darwin

package harness

import (
	"errors"
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

// observeProcess reads a process's kernel start time and group from
// kern.proc.pid, the sysctl `ps` reads, without a fork (the same pattern as
// internal/cli and internal/testguard; not imported, since testguard is test
// infrastructure). The start time is identity (D-025): a pid names a process
// only together with it. Measured 2026-10-07 on darwin 27: the sysctl answers
// for a zombie (stat 5, start time intact), so an unreaped leader reads as
// alive; for a reaped or never-existing pid it fails with EIO, not ESRCH, so
// absence is confirmed with kill(pid, 0) and ONLY an ESRCH there is "gone".
// getsid fails on a zombie; the sid is diagnostic and recorded as 0 then.
func observeProcess(pid int) (ProcessIdentity, error) {
	if pid <= 0 {
		return ProcessIdentity{}, fmt.Errorf("pid %d is not observable", pid)
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil || int(kp.Proc.P_pid) != pid {
		if kerr := syscall.Kill(pid, 0); errors.Is(kerr, syscall.ESRCH) {
			return ProcessIdentity{}, errProcessGone
		}
		if err == nil {
			err = errors.New("kern.proc.pid returned another process")
		}
		return ProcessIdentity{}, fmt.Errorf("observe pid %d: %w", pid, err)
	}
	id := ProcessIdentity{PID: pid, Start: kinfoStart(kp), PGID: int(kp.Eproc.Pgid)}
	if sid, err := unix.Getsid(pid); err == nil {
		id.SID = sid
	}
	return id, nil
}
