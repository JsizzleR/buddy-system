//go:build darwin

package cli

import (
	"bytes"

	"golang.org/x/sys/unix"
)

// procInfo answers from two sysctls, no fork: kern.proc.pid for the parent
// and the start time, kern.procargs2 for the name. Both are what `ps` reads.
//
// The name comes from procargs2 and not from KinfoProc.Proc.P_comm, because
// P_comm is the basename of the file the kernel exec'd, and the claude
// launcher is a symlink into a versions directory — measured 2026-09-20:
// P_comm `2.1.278`, exec path `/Users/.../.local/bin/claude`, argv[0]
// `claude`. The exec path is preferred over argv[0] because argv[0] is the
// caller's to set; both are tried because a name the exec path cannot give
// (a hard link, a renamed copy) argv[0] usually can.
//
// A pid that does not exist draws EIO from kern.proc.pid and EINVAL from
// procargs2 (measured); either is "not ok". The start time is the process's
// own, in microseconds — compared for equality only, never as a clock.
func procInfo(pid int) (ppid int, names []string, born int64, ok bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, nil, 0, false
	}
	born = kp.Proc.P_starttime.Sec*1_000_000 + int64(kp.Proc.P_starttime.Usec)
	ppid = int(kp.Eproc.Ppid)
	if raw, err := unix.SysctlRaw("kern.procargs2", pid); err == nil && len(raw) > 4 {
		names = execNames(raw[4:])
	}
	if len(names) == 0 {
		// procargs2 is refused for a process this user may not inspect;
		// kern.proc.pid still answers. P_comm is a last resort and, for the
		// reason above, will not match the harness — which leaves the walk to
		// continue past this process, never to anchor to it wrongly.
		comm := kp.Proc.P_comm[:]
		if i := bytes.IndexByte(comm, 0); i >= 0 {
			comm = comm[:i]
		}
		names = []string{string(comm)}
	}
	return ppid, names, born, true
}

// execNames parses the procargs2 block after its 4-byte argc: the exec path,
// NUL-terminated and NUL-padded, then argv[0]. Both are returned, because a
// harness started by its versioned path has a version string for one and
// `claude` for the other.
func execNames(rest []byte) []string {
	i := bytes.IndexByte(rest, 0)
	if i < 0 {
		return nil
	}
	var out []string
	if exe := string(rest[:i]); exe != "" {
		out = append(out, exe)
	}
	rest = rest[i:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	if j := bytes.IndexByte(rest, 0); j > 0 {
		out = append(out, string(rest[:j]))
	}
	return out
}
