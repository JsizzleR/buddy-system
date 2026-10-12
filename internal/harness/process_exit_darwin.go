//go:build darwin

package harness

import "syscall"

// awaitExit returns once pid has exited, WITHOUT reaping it, so the zombie
// keeps the process-group id reserved until terminate's own Wait. kill(pid, 0)
// cannot answer this on darwin: it succeeds on a zombie (measured 2026-10-06),
// and Wait4 would reap. A NOTE_EXIT registration on an already-exited (zombie)
// child fails with ESRCH (measured); that is an exit, because we never reap
// before this returns.
func awaitExit(pid int) error {
	kq, err := syscall.Kqueue()
	if err != nil {
		return err
	}
	defer syscall.Close(kq)
	syscall.CloseOnExec(kq)
	change := []syscall.Kevent_t{{Ident: uint64(pid), Filter: syscall.EVFILT_PROC, Flags: syscall.EV_ADD | syscall.EV_ONESHOT, Fflags: syscall.NOTE_EXIT}}
	events := make([]syscall.Kevent_t, 1)
	for {
		n, err := syscall.Kevent(kq, change, events, nil)
		if err == syscall.EINTR {
			continue // EV_ADD is idempotent; resubmitting cannot double-register
		}
		if err != nil {
			return err
		}
		if n != 1 {
			continue
		}
		if events[0].Flags&syscall.EV_ERROR != 0 {
			if syscall.Errno(events[0].Data) == syscall.ESRCH {
				return nil
			}
			return syscall.Errno(events[0].Data)
		}
		if events[0].Fflags&syscall.NOTE_EXIT != 0 {
			return nil
		}
	}
}
