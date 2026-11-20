package cli

// The Linux reader's fixtures (memlinux.go). No build constraint: the parser
// is a pure function over an fs.FS, so these rows run on the operator's darwin
// tier and on CI's ubuntu runner alike; memread_linux_test.go is the one test
// that reads a real machine. Each refusal row has a positive control beside it
// (the same fixture, one file different, reading cleanly), so "it errored" is
// never "it never got that far" (CLAUDE.md, Style and testing).

import (
	"errors"
	"io/fs"
	"math"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
)

const (
	gib     = uint64(1) << 30
	memPID  = 42
	delFile = "\x00delete"
)

func kb(g float64) string { return strconv.FormatUint(uint64(g*float64(gib))/1024, 10) }

func bytesOf(g float64) string { return strconv.FormatUint(uint64(g*float64(gib)), 10) }

const hostMountinfo = "22 1 0:21 / /proc rw,nosuid shared:12 - proc proc rw\n" +
	"30 25 0:26 / /sys/fs/cgroup rw,nosuid,nodev,noexec,relatime shared:4 - cgroup2 cgroup2 rw,nsdelegate,memory_recursiveprot\n"

// linuxFixture is a desktop: 64 GiB, 50 available, no swap in use, PSI quiet,
// our process in /user.slice/u.scope with no limit anywhere on the path.
// over replaces files; delFile removes one.
func linuxFixture(over map[string]string) fstest.MapFS {
	files := map[string]string{
		"proc/meminfo": "MemTotal:       " + kb(64) + " kB\nMemFree:         1024 kB\nMemAvailable:   " + kb(50) + " kB\n" +
			"SwapTotal:      " + kb(8) + " kB\nSwapFree:       " + kb(7) + " kB\nHugePages_Total:       0\n",
		"proc/pressure/memory":                          "some avg10=0.00 avg60=0.00 avg300=0.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n",
		"proc/self/cgroup":                              "0::/user.slice/u.scope\n",
		"proc/self/mountinfo":                           hostMountinfo,
		"sys/fs/cgroup/user.slice/u.scope/cgroup.procs": "7\n42\n",
	}
	for k, v := range over {
		if v == delFile {
			delete(files, k)
			continue
		}
		files[k] = v
	}
	m := fstest.MapFS{}
	for k, v := range files {
		m[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return m
}

// errFS fails Open of the named files with the given errno, as the kernel
// does for EACCES and for a psi=0 kernel's EOPNOTSUPP.
type errFS struct {
	fs.FS
	errs map[string]error
}

func (e errFS) Open(name string) (fs.File, error) {
	err, ok := e.errs[name]
	if !ok {
		return e.FS.Open(name)
	}
	if r, ok := err.(readErr); ok { // opens, then fails read(2), as older kernels' PSI files do
		f, oerr := e.FS.Open(name)
		if oerr != nil {
			f, oerr = fstest.MapFS{name: &fstest.MapFile{}}.Open(name)
		}
		return readFailFile{f, &fs.PathError{Op: "read", Path: name, Err: r.errno}}, oerr
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: err}
}

// readErr marks an errFS entry that fails on read, not open.
type readErr struct{ errno syscall.Errno }

func (r readErr) Error() string { return r.errno.Error() }

type readFailFile struct {
	fs.File
	err error
}

func (f readFailFile) Read([]byte) (int, error) { return 0, f.err }

// container is the cgroupns docker shape: our cgroup is "/", the namespace
// root, mounted at /sys/fs/cgroup, its limit visible there.
func container(over map[string]string) map[string]string {
	m := map[string]string{
		"proc/self/cgroup":                              "0::/\n",
		"sys/fs/cgroup/cgroup.procs":                    "1\n42\n",
		"sys/fs/cgroup/memory.max":                      bytesOf(4) + "\n",
		"sys/fs/cgroup/memory.high":                     "max\n",
		"sys/fs/cgroup/memory.current":                  bytesOf(3) + "\n",
		"sys/fs/cgroup/memory.stat":                     "anon 2147483648\nfile 1073741824\nactive_file 268435456\ninactive_file 268435456\nshmem 536870912\nslab_reclaimable 0\n",
		"sys/fs/cgroup/memory.pressure":                 "some avg10=0.00 avg60=0.00 avg300=0.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n",
		"sys/fs/cgroup/user.slice/u.scope/cgroup.procs": delFile,
	}
	for k, v := range over {
		m[k] = v
	}
	return m
}

// nested is a limit on the PARENT that binds: /a (8 GiB, 7 used) over /a/b
// (16 GiB, 1 used). a's own memory.stat carries a sibling's 6 GiB of cache,
// which must never be credited.
func nested(over map[string]string) map[string]string {
	m := map[string]string{
		"proc/self/cgroup":                              "0::/a/b\n",
		"sys/fs/cgroup/a/b/cgroup.procs":                "42\n",
		"sys/fs/cgroup/a/memory.max":                    bytesOf(8) + "\n",
		"sys/fs/cgroup/a/memory.current":                bytesOf(7) + "\n",
		"sys/fs/cgroup/a/memory.stat":                   "inactive_file " + bytesOf(6) + "\n",
		"sys/fs/cgroup/a/b/memory.max":                  bytesOf(16) + "\n",
		"sys/fs/cgroup/a/b/memory.current":              bytesOf(1) + "\n",
		"sys/fs/cgroup/a/b/memory.stat":                 "active_file " + bytesOf(0.5) + "\ninactive_file " + bytesOf(0.25) + "\nslab_reclaimable " + bytesOf(0.25) + "\n",
		"sys/fs/cgroup/user.slice/u.scope/cgroup.procs": delFile,
	}
	for k, v := range over {
		m[k] = v
	}
	return m
}

func TestReadLinuxMemory(t *testing.T) {
	psi := func(full string) string {
		return "some avg10=40.00 avg60=1.00 avg300=0.00 total=1\nfull avg10=" + full + " avg60=0.00 avg300=0.00 total=0\n"
	}
	type want struct {
		total, avail float64 // GiB
		pressure     int
		cgroup       string
		bound        string // substring
	}
	host := want{64, 50, 1, "/user.slice/u.scope", "host MemAvailable"}
	cases := []struct {
		name  string
		files map[string]string
		errs  map[string]error
		want  want
		err   string // substring; "" = reads
		isV1  bool
	}{
		{name: "host desktop, no limit on the path: the host figures (positive control)", want: host},
		{name: "no /proc/self/cgroup and no cgroup2 mount: the host figures", files: map[string]string{"proc/self/cgroup": delFile, "proc/self/mountinfo": "22 1 0:21 / /proc rw - proc proc rw\n"},
			want: want{64, 50, 1, "", "host MemAvailable"}},
		{name: "no unified line, a named v1 hierarchy only, no cgroup2 mount: the host figures",
			files: map[string]string{"proc/self/cgroup": "1:name=systemd:/user.slice\n", "proc/self/mountinfo": "22 1 0:21 / /proc rw - proc proc rw\n"}, want: want{64, 50, 1, "", "host MemAvailable"}},
		{name: "no /proc/self/cgroup beside a cgroup2 mount: an error (Fable 2)", files: map[string]string{"proc/self/cgroup": delFile},
			err: "names no cgroup v2 path"},
		{name: "an empty /proc/self/cgroup (a /dev/null bind) beside a cgroup2 mount: an error",
			files: container(map[string]string{"proc/self/cgroup": ""}), err: "names no cgroup v2 path"},
		{name: "an empty membership beside a cgroup2 mount no view can use (root /..): still an error (Codex re-verify 1)",
			files: map[string]string{"proc/self/cgroup": "", "proc/self/mountinfo": "30 25 0:26 /.. /sys/fs/cgroup rw - cgroup2 cgroup2 rw\n"},
			err:   "names no cgroup v2 path"},
		{name: "a named v1 hierarchy only, beside a cgroup2 mount: an error",
			files: map[string]string{"proc/self/cgroup": "1:name=systemd:/user.slice\n"}, err: "names no cgroup v2 path"},
		{name: "a named v1 hierarchy beside the unified line: read as v2",
			files: map[string]string{"proc/self/cgroup": "1:name=systemd:/x\n0::/user.slice/u.scope\n"}, want: host},

		// meminfo
		{name: "MemAvailable absent: no estimate", files: map[string]string{"proc/meminfo": "MemTotal: 1024 kB\nMemFree: 512 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n"},
			err: "no MemAvailable"},
		{name: "MemAvailable over MemTotal", files: map[string]string{"proc/meminfo": "MemTotal: 1024 kB\nMemAvailable: 2048 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n"},
			err: "exceeds MemTotal"},
		{name: "MemTotal 0", files: map[string]string{"proc/meminfo": "MemTotal: 0 kB\nMemAvailable: 0 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n"},
			err: "MemTotal is 0"},
		{name: "a unit other than kB", files: map[string]string{"proc/meminfo": "MemTotal: 1024 MB\nMemAvailable: 512 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n"},
			err: "not a count of kB"},
		{name: "a figure that is not a number", files: map[string]string{"proc/meminfo": "MemTotal: 1e6 kB\nMemAvailable: 512 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n"},
			err: "not a count of kB"},
		{name: "a figure whose bytes overflow", files: map[string]string{"proc/meminfo": "MemTotal: 18014398509481984 kB\nMemAvailable: 512 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n"},
			err: "not a count of kB"},
		{name: "a key twice", files: map[string]string{"proc/meminfo": "MemTotal: 1024 kB\nMemAvailable: 512 kB\nMemAvailable: 1000 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n"},
			err: "twice"},
		{name: "SwapFree absent", files: map[string]string{"proc/meminfo": "MemTotal: 1024 kB\nMemAvailable: 512 kB\nSwapTotal: 0 kB\n"},
			err: "no SwapFree"},
		{name: "SwapFree over SwapTotal", files: map[string]string{"proc/meminfo": "MemTotal: 1024 kB\nMemAvailable: 512 kB\nSwapTotal: 0 kB\nSwapFree: 4 kB\n"},
			err: "SwapFree exceeds"},
		{name: "meminfo unreadable", errs: map[string]error{"proc/meminfo": syscall.EACCES}, err: "permission denied"},

		// PSI
		{name: "PSI absent: the signal off, not an error", files: map[string]string{"proc/pressure/memory": delFile}, want: host},
		{name: "PSI EOPNOTSUPP (booted psi=0): the signal off", errs: map[string]error{"proc/pressure/memory": syscall.EOPNOTSUPP}, want: host},
		{name: "PSI EOPNOTSUPP from read(2), not open: off", errs: map[string]error{"proc/pressure/memory": readErr{syscall.EOPNOTSUPP}}, want: host},
		{name: "PSI EACCES: an error", errs: map[string]error{"proc/pressure/memory": syscall.EACCES}, err: "permission denied"},
		{name: "PSI full avg10 9.99: normal", files: map[string]string{"proc/pressure/memory": psi("9.99")}, want: host},
		{name: "PSI full avg10 10.00: critical", files: map[string]string{"proc/pressure/memory": psi("10.00")},
			want: want{64, 50, 4, "/user.slice/u.scope", "host MemAvailable"}},
		{name: "PSI with no full line: malformed", files: map[string]string{"proc/pressure/memory": "some avg10=0.00 avg60=0.00 avg300=0.00 total=0\n"},
			err: "no full line"},
		{name: "PSI with two full lines (Codex code 4)", files: map[string]string{"proc/pressure/memory": psi("0.00") + "full avg10=50.00 avg60=0.00 avg300=0.00 total=1\n"},
			err: "more than one full line"},
		{name: "PSI full with two avg10", files: map[string]string{"proc/pressure/memory": "full avg10=0.00 avg10=50.00 total=0\n"}, err: "more than one"},
		{name: "PSI full with no avg10", files: map[string]string{"proc/pressure/memory": "full avg60=0.00 total=0\n"}, err: "no full avg10"},
		{name: "PSI full avg10 not a number", files: map[string]string{"proc/pressure/memory": psi("x")}, err: "not a percentage"},
		{name: "PSI full avg10 over 100", files: map[string]string{"proc/pressure/memory": psi("100.01")}, err: "not a percentage"},
		{name: "PSI full avg10 NaN", files: map[string]string{"proc/pressure/memory": psi("NaN")}, err: "not a percentage"},
		{name: "leaf PSI with no limit at the leaf is not read",
			files: map[string]string{"sys/fs/cgroup/user.slice/u.scope/memory.pressure": psi("90.00")}, want: host},

		// cgroup v1
		{name: "v1 memory controller: refused", files: map[string]string{"proc/self/cgroup": "5:memory:/user.slice\n"}, isV1: true},
		{name: "hybrid, memory on v1 beside the unified line: refused",
			files: map[string]string{"proc/self/cgroup": "4:cpu,memory:/\n0::/user.slice/u.scope\n"}, isV1: true},

		// placement
		{name: "a line that is not hierarchy:controllers:path", files: map[string]string{"proc/self/cgroup": "0:/x\n"}, err: "not hierarchy"},
		{name: "two unified paths", files: map[string]string{"proc/self/cgroup": "0::/a\n0::/b\n"}, err: "two cgroup v2"},
		{name: "a path outside the namespace root (..)", files: map[string]string{"proc/self/cgroup": "0::/../../x\n"}, err: "outside"},
		{name: "a relative path", files: map[string]string{"proc/self/cgroup": "0::user.slice\n"}, err: "not hierarchy"},
		{name: "a hierarchy id that is not a number (Codex code 2)", files: container(map[string]string{"proc/self/cgroup": "not-a-hierarchy::/\n"}), err: "not hierarchy"},
		{name: "a nonzero hierarchy with no controllers", files: container(map[string]string{"proc/self/cgroup": "5::/\n"}), err: "not hierarchy"},
		{name: "hierarchy 0 naming a controller", files: container(map[string]string{"proc/self/cgroup": "0:cpu:/\n"}), err: "not hierarchy"},
		{name: "an unreadable cgroup.procs is an error, not a reason to try a longer root (Codex code)",
			files: map[string]string{
				"proc/self/cgroup":               "0::/a/b\n",
				"proc/self/mountinfo":            "30 25 0:26 / /sys/fs/cgroup rw - cgroup2 cgroup2 rw\n31 25 0:26 /a/b /mnt/b rw - cgroup2 cgroup2 rw\n",
				"mnt/b/cgroup.procs":             "42\n",
				"sys/fs/cgroup/a/memory.max":     bytesOf(4) + "\n",
				"sys/fs/cgroup/a/memory.current": bytesOf(3) + "\n",
			}, errs: map[string]error{"sys/fs/cgroup/a/b/cgroup.procs": syscall.EACCES}, err: "permission denied"},
		{name: "in a v2 hierarchy, no cgroup2 mount visible: an error, never the host figure",
			files: map[string]string{"proc/self/mountinfo": "22 1 0:21 / /proc rw - proc proc rw\n"}, err: "no visible cgroup2 mount"},
		{name: "only a mount rooted elsewhere: an error",
			files: map[string]string{"proc/self/mountinfo": "30 25 0:26 /other /sys/fs/cgroup rw - cgroup2 cgroup2 rw\n"}, err: "no visible cgroup2 mount"},
		{name: "an unrelated mount first, a usable one after: placed (Codex discovery case 2)",
			files: map[string]string{"proc/self/mountinfo": "29 25 0:26 /other /mnt/o rw - cgroup2 cgroup2 rw\n" + hostMountinfo}, want: host},
		{name: "a mount whose root names ..: skipped, the usable one places",
			files: map[string]string{"proc/self/mountinfo": "29 25 0:26 /../x /mnt/o rw - cgroup2 cgroup2 rw\n" + hostMountinfo}, want: host},
		{name: "cgroup.procs does not list us: an error",
			files: map[string]string{"sys/fs/cgroup/user.slice/u.scope/cgroup.procs": "7\n420\n"}, err: "cgroup.procs lists this process"},
		{name: "cgroup.procs absent: an error",
			files: map[string]string{"sys/fs/cgroup/user.slice/u.scope/cgroup.procs": delFile}, err: "cgroup.procs lists this process"},
		{name: "the shortest root does not list us, a longer one does: an error, never the longer root (Fable 3)",
			files: map[string]string{
				"proc/self/mountinfo":                           hostMountinfo + "31 25 0:26 /user.slice /mnt/u rw - cgroup2 cgroup2 rw\n",
				"sys/fs/cgroup/user.slice/u.scope/cgroup.procs": "7\n",
				"mnt/u/u.scope/cgroup.procs":                    "42\n",
				"mnt/u/u.scope/memory.max":                      bytesOf(2) + "\n",
				"mnt/u/u.scope/memory.current":                  bytesOf(1) + "\n",
			}, err: "cgroup.procs lists this process"},
		{name: "the same shortest root mounted twice, the second lists us: placed there",
			files: map[string]string{
				"proc/self/mountinfo":                           hostMountinfo + "31 25 0:26 / /mnt/c rw - cgroup2 cgroup2 rw\n",
				"sys/fs/cgroup/user.slice/u.scope/cgroup.procs": delFile,
				"mnt/c/user.slice/u.scope/cgroup.procs":         "42\n",
				"mnt/c/user.slice/u.scope/memory.max":           bytesOf(2) + "\n",
				"mnt/c/user.slice/u.scope/memory.current":       bytesOf(1) + "\n",
			}, want: want{2, 1, 1, "/user.slice/u.scope", "cgroup level own, limit 2.0 GB"}},
		{name: "an EACCES alias of the shortest root beside a readable one: placed by the readable one (Codex re-verify 1)",
			files: map[string]string{"proc/self/mountinfo": "30 25 0:26 / /private/cg rw - cgroup2 cgroup2 rw\n" + hostMountinfo,
				"private/cg/user.slice/u.scope/cgroup.procs": "7\n42\n"},
			errs: map[string]error{"private/cg/user.slice/u.scope/cgroup.procs": syscall.EACCES}, want: host},
		{name: "every alias of the shortest root EACCES: the read error",
			files: map[string]string{"proc/self/mountinfo": "30 25 0:26 / /private/cg rw - cgroup2 cgroup2 rw\n" + hostMountinfo},
			errs:  map[string]error{"private/cg/user.slice/u.scope/cgroup.procs": syscall.EACCES, "sys/fs/cgroup/user.slice/u.scope/cgroup.procs": syscall.EACCES},
			err:   "permission denied"},
		{name: "a mount rooted at /.. (above our namespace) is not read as /",
			files: map[string]string{"proc/self/mountinfo": "30 25 0:26 /.. /sys/fs/cgroup rw - cgroup2 cgroup2 rw\n"}, err: "no visible cgroup2 mount"},
		{name: "a root that prefixes our path's name but not a component (/user vs /user.slice) is not a candidate",
			files: map[string]string{
				"proc/self/mountinfo":               "30 25 0:26 /user /mnt/u rw - cgroup2 cgroup2 rw\n",
				"mnt/u/.slice/u.scope/cgroup.procs": "42\n",
			}, err: "no visible cgroup2 mount"},
		{name: "a mountinfo over 1 MiB reads (Fable 6)",
			files: map[string]string{"proc/self/mountinfo": strings.Repeat("40 25 0:50 / /var/lib/docker/overlay2/"+strings.Repeat("x", 3000)+"/merged rw - overlay overlay rw\n", 400) + hostMountinfo},
			want:  host},
		{name: "an escaped mount point (\\040)",
			files: map[string]string{
				"proc/self/mountinfo":                         "30 25 0:26 / /sys/fs/cg\\040x rw shared:4 - cgroup2 cgroup2 rw\n",
				"sys/fs/cg x/user.slice/u.scope/cgroup.procs": "42\n",
			}, want: host},
		{name: "a malformed escape", files: map[string]string{"proc/self/mountinfo": "30 25 0:26 / /sys/fs/cg\\04 rw - cgroup2 cgroup2 rw\n"}, err: "malformed escape"},
		{name: "a mountinfo line without the separator", files: map[string]string{"proc/self/mountinfo": "30 25 0:26 / /sys/fs/cgroup rw cgroup2 cgroup2 rw\n"}, err: "malformed line"},
		{name: "mountinfo with no optional fields", files: map[string]string{"proc/self/mountinfo": "30 25 0:26 / /sys/fs/cgroup rw - cgroup2 cgroup2 rw\n"}, want: host},

		// a container's limit: THE TRAP
		{name: "cgroupns container 4 GiB, 3 used, 0.5 own cache: 1.5 available of 4 (never the host's 50)",
			files: container(nil), want: want{4, 1.5, 1, "/", "cgroup level own, limit 4.0 GB"}},
		{name: "container, no memory.stat: no credit",
			files: container(map[string]string{"sys/fs/cgroup/memory.stat": delFile}), want: want{4, 1, 1, "/", "cgroup level own"}},
		{name: "container, memory.stat unreadable: an error",
			files: container(nil), errs: map[string]error{"sys/fs/cgroup/memory.stat": syscall.EACCES}, err: "permission denied"},
		{name: "container, memory.stat value not a number",
			files: container(map[string]string{"sys/fs/cgroup/memory.stat": "inactive_file x\n"}), err: "not a byte count"},
		{name: "container over its high mark with credit: the excess is paid from the credit (Codex code 1: 8-10+6 = 4)",
			files: container(map[string]string{
				"sys/fs/cgroup/memory.max": bytesOf(16) + "\n", "sys/fs/cgroup/memory.high": bytesOf(8) + "\n",
				"sys/fs/cgroup/memory.current": bytesOf(10) + "\n", "sys/fs/cgroup/memory.stat": "inactive_file " + bytesOf(6) + "\n",
			}), want: want{8, 4, 1, "/", "limit 8.0 GB"}},
		{name: "over the mark by more than the credit: 0",
			files: container(map[string]string{
				"sys/fs/cgroup/memory.max": bytesOf(16) + "\n", "sys/fs/cgroup/memory.high": bytesOf(8) + "\n",
				"sys/fs/cgroup/memory.current": bytesOf(10) + "\n", "sys/fs/cgroup/memory.stat": "inactive_file " + bytesOf(1) + "\n",
			}), want: want{8, 0, 1, "/", "limit 8.0 GB"}},
		{name: "both finite, high the lower: the minimum",
			files: container(map[string]string{"sys/fs/cgroup/memory.max": bytesOf(6) + "\n", "sys/fs/cgroup/memory.high": bytesOf(4) + "\n"}),
			want:  want{4, 1.5, 1, "/", "limit 4.0 GB"}},
		{name: "both finite, max the lower: the minimum",
			files: container(map[string]string{"sys/fs/cgroup/memory.max": bytesOf(4) + "\n", "sys/fs/cgroup/memory.high": bytesOf(6) + "\n"}),
			want:  want{4, 1.5, 1, "/", "limit 4.0 GB"}},
		{name: "a memory.high of 0: an error naming it (Fable 5)",
			files: container(map[string]string{"sys/fs/cgroup/memory.high": "0\n"}), err: "limit of 0 bytes"},
		{name: "nested, our child's memory.min protects its cache: no credit at the parent (Fable 4)",
			files: nested(map[string]string{"sys/fs/cgroup/a/memory.current": bytesOf(8) + "\n", "sys/fs/cgroup/a/b/memory.min": bytesOf(8) + "\n"}),
			want:  want{8, 0, 1, "/a/b", "parent"}},
		{name: "nested, a memory.min under the child's usage leaves the excess creditable",
			files: nested(map[string]string{
				"sys/fs/cgroup/a/memory.current": bytesOf(8) + "\n", "sys/fs/cgroup/a/b/memory.min": bytesOf(0.5) + "\n",
			}), want: want{8, 0.5, 1, "/a/b", "parent"}},
		{name: "nested, memory.min 0: full credit (positive control)",
			files: nested(map[string]string{"sys/fs/cgroup/a/memory.current": bytesOf(8) + "\n", "sys/fs/cgroup/a/b/memory.min": "0\n"}),
			want:  want{8, 1, 1, "/a/b", "parent"}},
		{name: "nested, memory.min max: no credit",
			files: nested(map[string]string{"sys/fs/cgroup/a/memory.current": bytesOf(8) + "\n", "sys/fs/cgroup/a/b/memory.min": "max\n"}),
			want:  want{8, 0, 1, "/a/b", "parent"}},
		{name: "a full leaf's own memory.min does not protect it from its own limit: its cache is credited (Codex re-verify 1)",
			files: container(map[string]string{
				"sys/fs/cgroup/memory.max": bytesOf(16) + "\n", "sys/fs/cgroup/memory.current": bytesOf(16) + "\n",
				"sys/fs/cgroup/memory.min": bytesOf(16) + "\n", "sys/fs/cgroup/memory.stat": "anon 4294967296\ninactive_file " + bytesOf(12) + "\n",
			}), want: want{16, 12, 1, "/", "cgroup level own"}},
		{name: "nested, memory.min garbage: an error",
			files: nested(map[string]string{"sys/fs/cgroup/a/b/memory.min": "lots\n"}), err: "neither max nor"},
		{name: "container, memory.stat counts a key twice (Codex code 3)",
			files: container(map[string]string{"sys/fs/cgroup/memory.stat": "inactive_file 1073741824\ninactive_file 1073741824\n"}), err: "twice"},
		{name: "container, memory.stat counted row with a trailing field",
			files: container(map[string]string{"sys/fs/cgroup/memory.stat": "inactive_file 1073741824 trailing\n"}), err: "not a byte count"},
		{name: "container, memory.stat other keys malformed are not read",
			files: container(map[string]string{"sys/fs/cgroup/memory.stat": "anon x y\nactive_file 268435456\ninactive_file 268435456\n"}),
			want:  want{4, 1.5, 1, "/", "cgroup level own"}},
		{name: "container, memory.max = max, memory.high 4 GiB: high is the limit",
			files: container(map[string]string{"sys/fs/cgroup/memory.max": "max\n", "sys/fs/cgroup/memory.high": bytesOf(4) + "\n"}),
			want:  want{4, 1.5, 1, "/", "limit 4.0 GB"}},
		{name: "container, both max: no limit, host figures",
			files: container(map[string]string{"sys/fs/cgroup/memory.max": "max\n"}), want: want{64, 50, 1, "/", "host MemAvailable"}},
		{name: "container, memory.max the page-rounded int64 max: never the total, the host figures (mem-adaptive-4's Fable lead)",
			files: container(map[string]string{"sys/fs/cgroup/memory.max": "9223372036854771712\n"}), want: want{64, 50, 1, "/", "host MemAvailable"}},
		{name: "container, memory.high the page-rounded int64 max: the host figures",
			files: container(map[string]string{"sys/fs/cgroup/memory.max": "max\n", "sys/fs/cgroup/memory.high": "9223372036854771712\n"}),
			want:  want{64, 50, 1, "/", "host MemAvailable"}},
		{name: "container, no memory.max or high at the proved root: no limit",
			files: container(map[string]string{"sys/fs/cgroup/memory.max": delFile, "sys/fs/cgroup/memory.high": delFile}),
			want:  want{64, 50, 1, "/", "host MemAvailable"}},
		{name: "container, memory.max unreadable: an error",
			files: container(nil), errs: map[string]error{"sys/fs/cgroup/memory.max": syscall.EACCES}, err: "permission denied"},
		{name: "container, memory.max garbage", files: container(map[string]string{"sys/fs/cgroup/memory.max": "lots\n"}), err: "neither max nor"},
		{name: "container, memory.current absent beside a limit: an error",
			files: container(map[string]string{"sys/fs/cgroup/memory.current": delFile}), err: "memory.current"},
		{name: "container, memory.current garbage", files: container(map[string]string{"sys/fs/cgroup/memory.current": "-1\n"}), err: "not a byte count"},
		{name: "container over its high mark: 0 available",
			files: container(map[string]string{"sys/fs/cgroup/memory.current": bytesOf(5) + "\n", "sys/fs/cgroup/memory.stat": delFile}),
			want:  want{4, 0, 1, "/", "cgroup level own"}},
		{name: "container whose credit exceeds its limit: capped at the limit",
			files: container(map[string]string{"sys/fs/cgroup/memory.current": "0\n", "sys/fs/cgroup/memory.stat": "inactive_file " + bytesOf(9) + "\n"}),
			want:  want{4, 4, 1, "/", "cgroup level own"}},
		{name: "container PSI critical at its limited level: critical",
			files: container(map[string]string{"sys/fs/cgroup/memory.pressure": psi("12.50")}), want: want{4, 1.5, 4, "/", "cgroup level own"}},
		{name: "container PSI absent: off", files: container(map[string]string{"sys/fs/cgroup/memory.pressure": delFile}),
			want: want{4, 1.5, 1, "/", "cgroup level own"}},
		{name: "container PSI malformed: an error", files: container(map[string]string{"sys/fs/cgroup/memory.pressure": "garbage\n"}), err: "no full line"},
		{name: "a host with less available than the container's headroom: the host binds, total still the limit",
			files: container(map[string]string{"proc/meminfo": "MemTotal: " + kb(64) + " kB\nMemAvailable: " + kb(1) + " kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n"}),
			want:  want{4, 1, 1, "/", "host MemAvailable"}},

		// nested: the binding level is the parent; a sibling's cache is never credited
		{name: "a parent's limit binds; only our path's child's cache is credited (Codex design 1)",
			files: nested(nil), want: want{8, 2, 1, "/a/b", "cgroup level parent, limit 8.0 GB"}},
		{name: "nested, the child on our path has no memory.stat: parent credit 0",
			files: nested(map[string]string{"sys/fs/cgroup/a/b/memory.stat": delFile}), want: want{8, 1, 1, "/a/b", "cgroup level parent"}},
		{name: "nested, PSI critical at the parent's limited level",
			files: nested(map[string]string{"sys/fs/cgroup/a/memory.pressure": psi("50.00")}), want: want{8, 2, 4, "/a/b", "parent"}},
		{name: "the binding level is not the total: parent 64/56 binds 8, total is the leaf's 16 (Codex design 3)",
			files: nested(map[string]string{
				"sys/fs/cgroup/a/memory.max": bytesOf(64) + "\n", "sys/fs/cgroup/a/memory.current": bytesOf(56) + "\n",
				"sys/fs/cgroup/a/b/memory.stat": delFile,
			}), want: want{16, 8, 1, "/a/b", "cgroup level parent, limit 64.0 GB"}},
		{name: "two mounts list us: the shortest root is read, exposing the ancestor's limit",
			files: map[string]string{
				"proc/self/mountinfo":                     "31 25 0:26 /user.slice /mnt/u rw - cgroup2 cgroup2 rw\n" + hostMountinfo,
				"mnt/u/u.scope/cgroup.procs":              "42\n",
				"sys/fs/cgroup/user.slice/memory.max":     bytesOf(6) + "\n",
				"sys/fs/cgroup/user.slice/memory.current": bytesOf(2) + "\n",
			}, want: want{6, 4, 1, "/user.slice/u.scope", "cgroup level parent, limit 6.0 GB"}},
		{name: "nested, a sibling's unprotected parent-level cache does not change the answer",
			files: nested(map[string]string{"sys/fs/cgroup/a/memory.stat": "inactive_file 0\n"}), want: want{8, 2, 1, "/a/b", "parent"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var fsys fs.FS = linuxFixture(tc.files)
			if tc.errs != nil {
				fsys = errFS{fsys, tc.errs}
			}
			m, err := readLinuxMemory(fsys, memPID)
			if tc.isV1 {
				if !errors.Is(err, errCgroupV1) {
					t.Fatalf("err = %v, want errCgroupV1", err)
				}
				return
			}
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want one naming %q (reading %+v)", err, tc.err, m)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want a reading", err)
			}
			g := func(b uint64) float64 { return float64(b) / float64(gib) }
			if math.Abs(g(m.TotalBytes)-tc.want.total) > 1e-6 || math.Abs(g(m.AvailBytes)-tc.want.avail) > 1e-6 {
				t.Errorf("total/avail = %g/%g GiB, want %g/%g", g(m.TotalBytes), g(m.AvailBytes), tc.want.total, tc.want.avail)
			}
			if m.Pressure != tc.want.pressure {
				t.Errorf("pressure = %d, want %d (PSI %s)", m.Pressure, tc.want.pressure, m.PSI)
			}
			if m.Cgroup != tc.want.cgroup {
				t.Errorf("cgroup = %q, want %q", m.Cgroup, tc.want.cgroup)
			}
			if !strings.Contains(m.Bound, tc.want.bound) {
				t.Errorf("bound = %q, want it to name %q", m.Bound, tc.want.bound)
			}
			// Every reading the parser returns passes the seam's check, and
			// SwapGB is never judged on Linux.
			r := m.reading()
			if why := r.unusable(); why != "" {
				t.Errorf("reading %+v is unusable: %s", r, why)
			}
			if r.SwapGB != 0 {
				t.Errorf("SwapGB = %g, want 0 (named in Basis, never judged)", r.SwapGB)
			}
		})
	}
}

// TestLinuxReadingFigures is reading()'s arithmetic: Level floors (truncation
// errs toward refusing), AvailGB carries what an integer Level loses on a big
// box (Codex design finding 4: 9 GiB on a 1 TiB host is Level 0 but 9 GB
// free), and Basis names the swap the slot does not judge.
func TestLinuxReadingFigures(t *testing.T) {
	cases := []struct {
		name            string
		m               linuxMemory
		level           int
		avail, free     float64
		basis, notBasis string
	}{
		{"1 TiB host, 9 GiB available: level 0, free 9",
			linuxMemory{TotalBytes: 1024 * gib, AvailBytes: 9 * gib, HostTotal: 1024 * gib, HostAvail: 9 * gib, Pressure: 1, PSI: "off", Bound: "host MemAvailable"},
			0, 9, 9, "available 9.0 of 1024.0 GB, bound by host MemAvailable", "cgroup /"},
		{"64 GiB, 50 available: level 78 (floor of 78.1)",
			linuxMemory{TotalBytes: 64 * gib, AvailBytes: 50 * gib, HostTotal: 64 * gib, HostAvail: 50 * gib, SwapUsed: 3 * gib, Pressure: 1, PSI: "0.00", Bound: "host MemAvailable"},
			78, 50, 50, "swap 3.0 GB in use, not judged; PSI full avg10 0.00; cgroup none", ""},
		{"zero available reads 0 both ways",
			linuxMemory{TotalBytes: 4 * gib, HostTotal: 64 * gib, HostAvail: 50 * gib, Pressure: 1, PSI: "off", Bound: "cgroup level own, limit 4.0 GB", Cgroup: "/"},
			0, 0, 0, "bound by cgroup level own, limit 4.0 GB; host MemAvailable 50.0 of 64.0 GB", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.m.reading()
			if r.Level != tc.level || r.AvailGB != tc.avail || r.freeGB() != tc.free {
				t.Errorf("level/avail/free = %d/%g/%g, want %d/%g/%g", r.Level, r.AvailGB, r.freeGB(), tc.level, tc.avail, tc.free)
			}
			if !strings.Contains(r.Basis, tc.basis) || (tc.notBasis != "" && strings.Contains(r.Basis, tc.notBasis)) {
				t.Errorf("basis = %q, want it to name %q and not %q", r.Basis, tc.basis, tc.notBasis)
			}
		})
	}
}

// TestLinuxReaderThroughSeam runs a fixture's reading through Env.memReading,
// the one wrapper every reading passes, and memVerdict: the container that
// reads 50 GB on the host's meminfo is refused a 6 GB start on its own 4 GB
// limit, and the host-only control is admitted.
func TestLinuxReaderThroughSeam(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   map[string]string
		refused bool
	}{
		{"host desktop admits a 6 GB start (positive control)", nil, false},
		{"4 GiB container refuses it", container(nil), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := Env{MemReading: func() (memReading, error) {
				m, err := readLinuxMemory(linuxFixture(tc.files), memPID)
				return m.reading(), err
			}}
			r, err := env.memReading()
			if err != nil {
				t.Fatal(err)
			}
			why, _ := memVerdict(r, 4, 0, 6)
			if (len(why) > 0) != tc.refused {
				t.Errorf("verdict %q, want refused=%v (reading %+v)", why, tc.refused, r)
			}
		})
	}
}

// TestLinuxPSIRecorded is the PSI figure Basis prints: the worst full avg10
// read, as the kernel printed it, or "off" when no file was read (Fable
// cross-check: a Basis that always printed "off" passed every other row).
func TestLinuxPSIRecorded(t *testing.T) {
	quiet := "full avg10=0.00 avg60=0.00 avg300=0.00 total=0\n"
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"host 10.00", map[string]string{"proc/pressure/memory": "full avg10=10.00 avg60=0.00 avg300=0.00 total=0\n"}, "10.00"},
		{"no PSI file", map[string]string{"proc/pressure/memory": delFile}, "off"},
		{"host quiet, the container's level 12.50: the worst", container(map[string]string{
			"proc/pressure/memory":          quiet,
			"sys/fs/cgroup/memory.pressure": "full avg10=12.50 avg60=0.00 avg300=0.00 total=0\n",
		}), "12.50"},
		{"host 7.00, the container's level 0.00: the worst", container(map[string]string{
			"proc/pressure/memory":          "full avg10=7.00 avg60=0.00 avg300=0.00 total=0\n",
			"sys/fs/cgroup/memory.pressure": quiet,
		}), "7.00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := readLinuxMemory(linuxFixture(tc.files), memPID)
			if err != nil {
				t.Fatal(err)
			}
			if m.PSI != tc.want || !strings.Contains(m.reading().Basis, "PSI full avg10 "+tc.want+";") {
				t.Errorf("PSI = %q (basis %q), want %q", m.PSI, m.reading().Basis, tc.want)
			}
		})
	}
}
