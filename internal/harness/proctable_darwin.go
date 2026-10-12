//go:build darwin

package harness

import "golang.org/x/sys/unix"

// listProcesses is one kern.proc.all read: every process's pid, parent, group
// and kernel start time, the table `ps` reads, without a fork. Measured 110-126
// us median at ~815 rows (2026-10-07). Nothing establishes that the kernel
// copies the table atomically, so attribution treats it as possibly torn
// (descendants.go). The start time is computed exactly as observeProcess
// computes it (kinfoStart), so a snapshot row and a point observation of the
// same process agree.
func listProcesses() ([]procRow, error) {
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	rows := make([]procRow, 0, len(kps))
	for i := range kps {
		kp := &kps[i]
		rows = append(rows, procRow{PID: int(kp.Proc.P_pid), PPID: int(kp.Eproc.Ppid), PGID: int(kp.Eproc.Pgid), Start: kinfoStart(kp)})
	}
	return rows, nil
}

func kinfoStart(kp *unix.KinfoProc) int64 {
	return kp.Proc.P_starttime.Sec*1_000_000 + int64(kp.Proc.P_starttime.Usec)
}
