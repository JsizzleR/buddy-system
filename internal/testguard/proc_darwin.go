//go:build darwin

package testguard

import (
	"debug/gosym"
	"debug/macho"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// procOf reads another process's parent and kernel start time from
// kern.proc.pid, the sysctl `ps` reads, without a fork (internal/cli/
// proc_darwin.go does the same). The start time is identity: a pid names a
// process only together with it (D-025), so the watchdog signals nothing whose
// start time has changed. Any error is "cannot say", which the watchdog reads
// as NOT orphaned and NOT identifiable — a lookup that fails must never kill.
func procOf(pid int) (ppid int, born int64, ok bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, 0, false
	}
	born = kp.Proc.P_starttime.Sec*1_000_000 + int64(kp.Proc.P_starttime.Usec)
	return int(kp.Eproc.Ppid), born, true
}

// frameRE is a frame `sample` could not name: the binary has no symbol table,
// so it prints the offset from the load address instead.
var frameRE = regexp.MustCompile(`load address 0x[0-9a-f]+ \+ (0x[0-9a-f]+)`)

// symbolize names, in place, every frame of a sample that `sample` left as a
// bare offset into exe. It is needed because `go test` links its binaries
// without a symbol table — measured: every Go frame of a sampled testguard.test
// printed as `???` — so the raw sample of a wedged test names nothing. The
// pclntab survives stripping (the runtime needs it for its own tracebacks),
// and the watchdog runs the SAME executable as its target, so it can read the
// target's table from the file. Measured on a stripped wedge: the spinning
// frame came back as the loop's own function and line. Every failure leaves
// the sample as `sample` wrote it.
func symbolize(path, exe string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	f, err := macho.Open(exe)
	if err != nil {
		return
	}
	defer f.Close()
	var base uint64
	for _, l := range f.Loads {
		if s, ok := l.(*macho.Segment); ok && s.Name == "__TEXT" {
			base = s.Addr
		}
	}
	pcln, text := f.Section("__gopclntab"), f.Section("__text")
	if base == 0 || pcln == nil || text == nil {
		return
	}
	data, err := pcln.Data()
	if err != nil {
		return
	}
	tab, err := gosym.NewTable(nil, gosym.NewLineTable(data, text.Addr))
	if err != nil {
		return
	}
	lines := strings.Split(string(b), "\n")
	named := 0
	for i, l := range lines {
		m := frameRE.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		off, err := strconv.ParseUint(m[1], 0, 64)
		if err != nil {
			continue
		}
		file, line, fn := tab.PCToLine(base + off)
		if fn == nil {
			continue
		}
		lines[i] = l + "  " + fn.Name + " " + filepath.Base(file) + ":" + strconv.Itoa(line)
		named++
	}
	if named > 0 {
		os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
	}
}
