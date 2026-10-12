//go:build darwin || linux

package harness

import (
	"os"

	"golang.org/x/sys/unix"
)

// pipePending reports whether the read end of a pipe has something the reader
// has not consumed yet: unread bytes, or the EOF it has not seen. poll with a
// zero timeout, through SyscallConn so the descriptor keeps its non-blocking
// mode (os.File.Fd would switch it to blocking and stop Close from waking the
// reader). Not pending means: nothing is lost by closing our end now.
func pipePending(f *os.File) (bool, error) {
	rc, err := f.SyscallConn()
	if err != nil {
		return false, err
	}
	var ready bool
	var perr error
	if err := rc.Control(func(fd uintptr) {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		for {
			var n int
			n, perr = unix.Poll(fds, 0)
			if perr == unix.EINTR {
				continue
			}
			ready = perr == nil && n > 0 && fds[0].Revents&(unix.POLLIN|unix.POLLHUP) != 0
			return
		}
	}); err != nil {
		return false, err
	}
	return ready, perr
}
