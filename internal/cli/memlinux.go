package cli

// The memory slot's Linux reader, as a pure function over an fs.FS so its
// table tests run everywhere the hermetic tier does, the operator's darwin box
// included: this file carries NO build constraint (memread_linux.go is the
// three-line wrapper that hands it os.DirFS("/"); D-241). The figures and why
// each one:
//
//   - Available is /proc/meminfo's MemAvailable, the kernel's own "estimate of
//     how much memory is available for starting new applications, without
//     swapping" (proc(5)). MemFree is the WRONG figure: it excludes page
//     cache, so a healthy box with a warm cache reads near 0 and every start
//     is refused. A kernel without MemAvailable (< 3.14) is an error, never a
//     hand-rolled estimate: MemFree + Cached - ... is the guess the kernel
//     added the field to replace.
//   - Inside a cgroup v2 limit /proc/meminfo is still the HOST's (unless
//     lxcfs is mounted): a 4 GB container on a 64 GB host reads 50 GB
//     available, admits every start, and the cgroup OOM-killer, not swap, ends
//     the lane. That is the trap this reader exists to close. So every level
//     from our own cgroup up to the visible root that has a finite limit
//     (min of memory.max and memory.high: high is where the kernel throttles
//     into reclaim and swap, Codex design finding 2) contributes a headroom,
//     limit - current + the reclaimable cache of OUR path's child at that
//     level (active_file + inactive_file + slab_reclaimable; the level itself
//     at our leaf). A sibling's cache is never credited: it may be protected
//     by memory.min and the kernel skips it (Codex design finding 1, the
//     protected-sibling input that OOMs at 1 GiB of real headroom); our own
//     is, or a build container whose cache fills its limit refuses every
//     start forever (panel lens a). The operator chose this credit, R-3, on
//     2026-11-20 18:13Z. Available is the minimum of the host figure and
//     every headroom; Total the minimum of MemTotal and every finite limit,
//     independent of which term binds, so a machine-relative floor sees the
//     container as the machine and does not flap (Codex design finding 3).
//   - cgroup v1's memory controller is REFUSED (errCgroupV1), never read and
//     never answered with the host figure, which would be the trap again.
//     systemd deprecated v1 in 256 and removed it in 258. The operator's
//     choice, 2026-11-20.
//   - A process placed in a cgroup2 hierarchy we cannot see (no cgroup2
//     mount whose root contains our path, or none whose cgroup.procs lists
//     our pid) is an ERROR, not the host figure: the sandboxed-/sys container
//     with a hidden 4 GB limit is exactly the input a host fallback admits
//     (Codex design discovery case). A level's absent memory.max is "no limit
//     here" only once the directory is PROVED ours by cgroup.procs, never
//     because a wrongly resolved path has no files in it.
//   - Pressure is PSI: `full avg10 >= 10` (every non-idle task stalled on
//     memory for, in an exponentially decaying average, a tenth of the
//     recent past) is critical, 4; anything else 1. Read from the host and
//     from each finite-limit level, never the leaf alone (one task in a scope
//     makes full equal some); the worst wins. It is an UNMEASURED secondary
//     heuristic, not darwin's pressure level qualified (the operator's P-B,
//     2026-11-20). A PSI file that is absent, or reads EOPNOTSUPP (booted
//     psi=0; RHEL 8 ships it off), turns the signal off: MemAvailable is the
//     primary and refusing init for want of the secondary buys nothing. One
//     that exists and does not parse is an error, as any malformed figure.
//   - Swap is NOT judged on Linux (SwapGB 0): the slot's swap rule pairs swap
//     with a percentage that means "70% wired or compressed" on darwin and an
//     ordinary desktop on Linux (9 GB of stale swap beside 29% available
//     would refuse forever; panel lens a, settled with mem-adaptive). The host
//     figure is named in Basis instead.
//
// Every malformed or out-of-range figure is an error, and an error is no
// reading: `mem init` refuses, and with the slot on a heavy call is denied
// (memslot.go). Nothing here guesses.
//
// Recorded open (D-241): a limit set above the cgroup namespace's root (a k8s
// pod cgroup above its container's) is invisible from inside; a memory.min on
// a cousin under our path's child, or protection inherited through
// memory_recursiveprot, is credited; ZFS's ARC is outside
// MemAvailable; mount churn between the reads is an error for that call.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"path"
	"sort"
	"strconv"
	"strings"
)

// errCgroupV1 is the refusal of a v1 (or hybrid) memory controller. Named so
// the live probe (memread_linux_test.go) can skip on exactly this and fail on
// anything else.
var errCgroupV1 = errors.New("cgroup v1 memory controller: the memory slot reads cgroup v2 only")

// linuxMemory is what readLinuxMemory found, in bytes. reading() turns it into
// the slot's memReading.
type linuxMemory struct {
	TotalBytes uint64 // min(MemTotal, every finite limit on our path)
	AvailBytes uint64 // min(MemAvailable, every finite level's headroom)
	HostTotal  uint64 // MemTotal
	HostAvail  uint64 // MemAvailable
	SwapUsed   uint64 // host SwapTotal - SwapFree: named in Basis, never judged
	Pressure   int    // 1, or 4 on PSI full avg10 >= linuxPSIFull anywhere read
	PSI        string // "off", or the worst full avg10 read, as the kernel printed it
	Cgroup     string // our cgroup v2 path, "" when not in one
	Bound      string // what bound AvailBytes: "host MemAvailable" or a level

	psiSeen bool
	psiMax  float64
}

// linuxPSIFull is the `full avg10` at or above which Linux reads critical.
const linuxPSIFull = 10.0

const (
	linuxMaxFile      = 1 << 20 // no /proc or cgroup file the reader opens is near this
	linuxMaxMountinfo = 1 << 26 // but mountinfo grows with the mounts
)

func (m linuxMemory) reading() memReading {
	const g = 1 << 30
	total, avail := float64(m.TotalBytes)/g, float64(m.AvailBytes)/g
	level := 0
	if m.TotalBytes > 0 {
		level = int(math.Floor(100 * float64(m.AvailBytes) / float64(m.TotalBytes)))
	}
	cg := "none"
	if m.Cgroup != "" {
		cg = m.Cgroup
	}
	return memReading{
		TotalGB:  total,
		Level:    level,
		Pressure: m.Pressure,
		AvailGB:  avail,
		// The cgroup path is last: it is the one long, container-chosen value,
		// and fence.Line's cap cuts it rather than a figure.
		Basis: fmt.Sprintf("linux: available %s of %s GB, bound by %s; host MemAvailable %s of %s GB; swap %s GB in use, not judged; PSI full avg10 %s; cgroup %s",
			gb(avail), gb(total), m.Bound, gb(float64(m.HostAvail)/g), gb(float64(m.HostTotal)/g),
			gb(float64(m.SwapUsed)/g), m.PSI, cg),
	}
}

// readLinuxMemory reads the figures above from fsys, a view of "/" (names
// without the leading slash, as io/fs requires), for the process pid.
func readLinuxMemory(fsys fs.FS, pid int) (linuxMemory, error) {
	var m linuxMemory
	host, err := readMeminfo(fsys)
	if err != nil {
		return m, err
	}
	m.HostTotal, m.HostAvail = host.total, host.avail
	m.TotalBytes, m.AvailBytes = host.total, host.avail
	m.SwapUsed = host.swapUsed
	m.Bound = "host MemAvailable"
	m.Pressure, m.PSI = 1, "off"
	if err := m.pressure(fsys, "proc/pressure/memory"); err != nil {
		return m, err
	}

	cgPath, mounts, err := cgroupPlacement(fsys)
	if err != nil || cgPath == "" {
		return m, err
	}
	m.Cgroup = cgPath
	levels, err := provedLevels(fsys, cgPath, mounts, pid)
	if err != nil {
		return m, err
	}
	// levels[0] is our own cgroup, levels[len-1] the visible root.
	for i, dir := range levels {
		limit, finite, err := cgroupLimit(fsys, dir)
		if err != nil {
			return m, err
		}
		if !finite {
			continue
		}
		current, err := readCgroupUint(fsys, path.Join(dir, "memory.current"))
		if err != nil {
			return m, err
		}
		credited := dir
		if i > 0 {
			credited = levels[i-1]
		}
		credit, err := reclaimable(fsys, credited)
		if err != nil {
			return m, err
		}
		// A cgroup's own memory.min does not protect it from reclaim at its
		// OWN limit (the kernel ignores the reclaim target's protection), so
		// the cap applies only to a child credited against an ancestor
		// (Codex re-verify round 1: a full leaf with min = max read 0).
		if i > 0 {
			if credit, err = unprotected(fsys, credited, credit); err != nil {
				return m, err
			}
		}
		// clamp(limit - current + credit, 0, limit) without a signed type:
		// usage over the limit (current can pass memory.high) is paid out of
		// the credit first, never dropped before it is added (Codex code
		// pass, finding 1: 8 - 10 + 6 read 6, not 4).
		var headroom uint64
		if current <= limit {
			headroom = satAdd(limit-current, credit)
		} else if over := current - limit; credit > over {
			headroom = credit - over
		}
		if headroom > limit {
			headroom = limit
		}
		if limit == 0 {
			// A limit of 0 (memory.high's force-reclaim idiom) can start
			// nothing; a Total of 0 is no reading to the seam either, so say
			// so here rather than hand it one it refuses unexplained
			// (Fable cross-check, finding 5).
			return m, fmt.Errorf("linux memory: %s has a limit of 0 bytes", dir)
		}
		if limit < m.TotalBytes {
			m.TotalBytes = limit
		}
		if headroom < m.AvailBytes {
			m.AvailBytes = headroom
			m.Bound = fmt.Sprintf("cgroup level %s, limit %s GB", levelName(dir, levels), gb(float64(limit)/(1<<30)))
		}
		if err := m.pressure(fsys, path.Join(dir, "memory.pressure")); err != nil {
			return m, err
		}
	}
	if m.AvailBytes > m.TotalBytes { // cannot happen: each headroom <= its limit; a guard, not a clamp
		return m, fmt.Errorf("linux memory: available %d exceeds total %d", m.AvailBytes, m.TotalBytes)
	}
	return m, nil
}

// levelName names a level by its distance from our own cgroup: "own",
// "parent", "ancestor 2" and so on. Never the path itself, which is in Basis
// once, last.
func levelName(dir string, levels []string) string {
	for i, l := range levels {
		if l == dir {
			switch i {
			case 0:
				return "own"
			case 1:
				return "parent"
			}
			return "ancestor " + strconv.Itoa(i)
		}
	}
	return "?"
}

func satAdd(a, b uint64) uint64 {
	if a+b < a {
		return math.MaxUint64
	}
	return a + b
}

func readSmall(fsys fs.FS, name string) (string, error) {
	return readCapped(fsys, name, linuxMaxFile)
}

// readCapped is readSmall with its own cap: /proc/self/mountinfo is the one
// file unbounded by design (a busy container host lists hundreds of overlay
// mounts of kilobytes each), and a 1 MiB cap on it refused a healthy host
// (Fable cross-check, finding 6).
func readCapped(fsys fs.FS, name string, max int) (string, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil {
		return "", err
	}
	if len(b) > max {
		return "", fmt.Errorf("%s is over %d bytes", name, max)
	}
	return string(b), nil
}

type meminfo struct{ total, avail, swapUsed uint64 }

// readMeminfo reads MemTotal, MemAvailable, SwapTotal and SwapFree: each
// exactly once, a decimal count of `kB`, else an error.
func readMeminfo(fsys fs.FS) (meminfo, error) {
	var mi meminfo
	text, err := readSmall(fsys, "proc/meminfo")
	if err != nil {
		return mi, fmt.Errorf("linux memory: %w", err)
	}
	want := map[string]*uint64{"MemTotal": new(uint64), "MemAvailable": new(uint64), "SwapTotal": new(uint64), "SwapFree": new(uint64)}
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		key, rest, ok := strings.Cut(line, ":")
		dst := want[key]
		if !ok || dst == nil {
			continue
		}
		if seen[key] {
			return mi, fmt.Errorf("linux memory: /proc/meminfo names %s twice", key)
		}
		seen[key] = true
		f := strings.Fields(rest)
		if len(f) != 2 || f[1] != "kB" {
			return mi, fmt.Errorf("linux memory: /proc/meminfo %s is not a count of kB", key)
		}
		v, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil || v > math.MaxUint64/1024 {
			return mi, fmt.Errorf("linux memory: /proc/meminfo %s is not a count of kB", key)
		}
		*dst = v * 1024
	}
	for _, k := range []string{"MemTotal", "MemAvailable", "SwapTotal", "SwapFree"} {
		if !seen[k] {
			if k == "MemAvailable" {
				return mi, errors.New("linux memory: /proc/meminfo has no MemAvailable (a kernel before 3.14); the slot does not estimate it")
			}
			return mi, fmt.Errorf("linux memory: /proc/meminfo has no %s", k)
		}
	}
	mi.total, mi.avail = *want["MemTotal"], *want["MemAvailable"]
	swapTotal, swapFree := *want["SwapTotal"], *want["SwapFree"]
	switch {
	case mi.total == 0:
		return mi, errors.New("linux memory: /proc/meminfo MemTotal is 0")
	case mi.avail > mi.total:
		return mi, errors.New("linux memory: /proc/meminfo MemAvailable exceeds MemTotal")
	case swapFree > swapTotal:
		return mi, errors.New("linux memory: /proc/meminfo SwapFree exceeds SwapTotal")
	}
	mi.swapUsed = swapTotal - swapFree
	return mi, nil
}

// pressure folds one PSI file into m: absent or unsupported is no signal,
// malformed is an error, `full avg10 >= linuxPSIFull` is critical.
func (m *linuxMemory) pressure(fsys fs.FS, name string) error {
	text, err := readSmall(fsys, name)
	if err != nil {
		// errors.ErrUnsupported matches EOPNOTSUPP/ENOTSUP/ENOSYS on every
		// unix (syscall.Errno.Is): what a psi=0 kernel's read returns.
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, errors.ErrUnsupported) {
			return nil
		}
		return fmt.Errorf("linux memory: %w", err)
	}
	// Exactly one `full` line carrying exactly one avg10: a second of
	// either is malformed, not a choice between them (Codex code pass,
	// finding 4: a quiet first line hid a second reading 50).
	var avg []string
	lines := 0
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || f[0] != "full" {
			continue
		}
		lines++
		for _, kv := range f[1:] {
			if v, ok := strings.CutPrefix(kv, "avg10="); ok {
				avg = append(avg, v)
			}
		}
	}
	switch {
	case lines == 0:
		return fmt.Errorf("linux memory: %s has no full line", name)
	case lines > 1:
		return fmt.Errorf("linux memory: %s has more than one full line", name)
	case len(avg) != 1:
		return fmt.Errorf("linux memory: %s has no full avg10, or more than one", name)
	}
	x, err := strconv.ParseFloat(avg[0], 64)
	if err != nil || math.IsNaN(x) || x < 0 || x > 100 {
		return fmt.Errorf("linux memory: %s full avg10 is not a percentage", name)
	}
	if !m.psiSeen || x > m.psiMax {
		m.psiSeen, m.psiMax, m.PSI = true, x, avg[0]
	}
	if x >= linuxPSIFull {
		m.Pressure = 4
	}
	return nil
}

type cgroupMount struct{ root, point string }

// cgroupPlacement reads our cgroup v2 path from /proc/self/cgroup ("" when the
// process is in none) and the cgroup2 mounts from /proc/self/mountinfo. A v1
// or hybrid memory controller is errCgroupV1.
func cgroupPlacement(fsys fs.FS) (string, []cgroupMount, error) {
	text, err := readSmall(fsys, "proc/self/cgroup")
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil, noUnified(fsys) // a kernel without cgroups: nothing can limit us
	}
	if err != nil {
		return "", nil, fmt.Errorf("linux memory: %w", err)
	}
	var unified string
	found := false
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if line == "" {
			continue
		}
		f := strings.SplitN(line, ":", 3)
		// The shape cgroups(7) documents: a decimal hierarchy id, an empty
		// controller list exactly when it is 0 (the unified hierarchy), an
		// absolute path. Anything else is malformed, never "no memory
		// hierarchy applies" (Codex code pass, finding 2: a line
		// `not-a-hierarchy::/` read the host's 50 GB past a 4 GB limit).
		if len(f) == 3 {
			if _, err := strconv.ParseUint(f[0], 10, 32); err != nil || (f[0] == "0") != (f[1] == "") || !strings.HasPrefix(f[2], "/") {
				f = nil
			}
		}
		if len(f) != 3 {
			return "", nil, errors.New("linux memory: /proc/self/cgroup has a line that is not hierarchy:controllers:path")
		}
		for _, c := range strings.Split(f[1], ",") {
			if c == "memory" {
				return "", nil, errCgroupV1
			}
		}
		if f[0] == "0" {
			if found {
				return "", nil, errors.New("linux memory: /proc/self/cgroup names two cgroup v2 paths")
			}
			unified, found = f[2], true
		}
	}
	if !found {
		return "", nil, noUnified(fsys) // no unified hierarchy and no v1 memory controller: no limit applies
	}
	// path.Clean of an absolute path drops a leading "..", so a path that
	// climbs out of the namespace's root ("/../../x") is not clean: this is
	// the one test of it (a separate ".." scan here was unreachable; the
	// mutation check found it).
	if !path.IsAbs(unified) || path.Clean(unified) != unified {
		return "", nil, errors.New("linux memory: our cgroup v2 path is outside this namespace's root (it names ..), or not clean")
	}
	mounts, err := cgroup2Mounts(fsys)
	if err != nil {
		return "", nil, err
	}
	return unified, mounts, nil
}

// noUnified is the check behind "no 0:: line, so no limit applies": true only
// where no cgroup2 filesystem is mounted either. On a real kernel a mounted
// cgroup2 puts the 0:: line in every process's /proc/self/cgroup, so its
// absence beside one is a masked or faked file (a /dev/null bound over it
// reads empty), and the host figure there is the trap (Fable cross-check,
// finding 2).
func noUnified(fsys fs.FS) error {
	_, listed, err := cgroup2MountsAll(fsys)
	if err != nil {
		return err
	}
	if listed {
		return errors.New("linux memory: a cgroup2 filesystem is mounted but /proc/self/cgroup names no cgroup v2 path, so this process's limit cannot be placed")
	}
	return nil
}

func hasDotDot(p string) bool {
	for _, c := range strings.Split(p, "/") {
		if c == ".." {
			return true
		}
	}
	return false
}

// cgroup2Mounts parses /proc/self/mountinfo: fields before the " - "
// separator (their count varies with the optional fields), the filesystem type
// first after it; root and mount point octal-unescaped (\040 is a space).
func cgroup2Mounts(fsys fs.FS) ([]cgroupMount, error) {
	out, _, err := cgroup2MountsAll(fsys)
	return out, err
}

// cgroup2MountsAll is cgroup2Mounts plus whether mountinfo lists ANY cgroup2
// filesystem, a usable view or not: noUnified asks the second, and counting
// only usable views let a `/..`-rooted mount beside an empty membership read
// the host (Codex re-verify round 1).
func cgroup2MountsAll(fsys fs.FS) ([]cgroupMount, bool, error) {
	text, err := readCapped(fsys, "proc/self/mountinfo", linuxMaxMountinfo)
	if err != nil {
		return nil, false, fmt.Errorf("linux memory: %w", err)
	}
	var out []cgroupMount
	listed := false
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			continue
		}
		pre, post, ok := strings.Cut(line, " - ")
		pf, qf := strings.Fields(pre), strings.Fields(post)
		if !ok || len(pf) < 6 || len(qf) < 1 {
			return nil, false, errors.New("linux memory: /proc/self/mountinfo has a malformed line")
		}
		if qf[0] != "cgroup2" {
			continue
		}
		listed = true
		root, err1 := unescapeMount(pf[3])
		point, err2 := unescapeMount(pf[4])
		if err1 != nil || err2 != nil {
			return nil, false, errors.New("linux memory: /proc/self/mountinfo has a malformed escape")
		}
		if !path.IsAbs(root) || !path.IsAbs(point) || hasDotDot(root) || hasDotDot(point) {
			continue // a view that cannot contain a clean path: not a candidate
		}
		out = append(out, cgroupMount{root: path.Clean(root), point: path.Clean(point)})
	}
	return out, listed, nil
}

func unescapeMount(s string) (string, error) {
	if !strings.Contains(s, `\`) {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		if i+4 > len(s) {
			return "", errors.New("short escape")
		}
		v, err := strconv.ParseUint(s[i+1:i+4], 8, 8)
		if err != nil {
			return "", err
		}
		b.WriteByte(byte(v))
		i += 3
	}
	return b.String(), nil
}

// provedLevels places our cgroup path under a cgroup2 mount whose root is a
// path-component prefix of it (shortest root first: it exposes the most
// ancestors; Codex design discovery case 2), proves the directory ours by our
// pid in its cgroup.procs, and returns it and each ancestor up to the mount
// point, leaf first.
func provedLevels(fsys fs.FS, cg string, mounts []cgroupMount, pid int) ([]string, error) {
	cands := []cgroupMount{}
	for _, mt := range mounts {
		if mt.root == "/" || cg == mt.root || strings.HasPrefix(cg, mt.root+"/") {
			cands = append(cands, mt)
		}
	}
	sort.SliceStable(cands, func(i, j int) bool { return len(cands[i].root) < len(cands[j].root) })
	if len(cands) == 0 {
		return nil, errors.New("linux memory: this process is in a cgroup v2 hierarchy no visible cgroup2 mount contains, so its limit cannot be read")
	}
	// Only the shortest root's mounts are tried (a bind of the same view
	// may appear twice). Every cgroup2 mount views the one hierarchy, so a
	// shortest-root view that does not list us is a shadowed or faked path,
	// not a reason to read a longer root that hides the ancestors only the
	// shorter one shows (Fable cross-check, finding 3: a cgroupns root's own
	// 0-headroom limit went unread).
	for i, mt := range cands {
		if len(mt.root) != len(cands[0].root) {
			cands = cands[:i]
			break
		}
	}
	me := strconv.Itoa(pid)
	var readErr error
	for _, mt := range cands {
		rel := strings.TrimPrefix(strings.TrimPrefix(cg, mt.root), "/")
		top := fsName(mt.point)
		dir := top
		if rel != "" {
			dir = fsName(path.Join(mt.point, rel))
		}
		procs, err := readSmall(fsys, path.Join(dir, "cgroup.procs"))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			// A proof that could not be read is not a disproof. Another
			// alias of the SAME root shows the same ancestors, so it may
			// prove us (Codex re-verify round 1: an EACCES /private/cg beside
			// a readable /sys/fs/cgroup); if none does, this error is the
			// answer, never a longer root (Codex code pass: that dropped /a's
			// 1 GiB headroom).
			if readErr == nil {
				readErr = fmt.Errorf("linux memory: %w", err)
			}
			continue
		}
		if err != nil || !containsLine(procs, me) {
			continue
		}
		levels := []string{dir}
		for d := dir; d != top; {
			d = path.Dir(d)             // path.Dir of a one-component name is "."
			if d == "." && top != "." { // dir is built under top; a guard against a loop, not a case
				return nil, errors.New("linux memory: a cgroup directory is not under its mount point")
			}
			levels = append(levels, d)
		}
		return levels, nil
	}
	if readErr != nil {
		return nil, readErr
	}
	return nil, errors.New("linux memory: no cgroup2 mount's cgroup.procs lists this process, so its cgroup cannot be placed")
}

// fsName is an absolute path as an io/fs name: no leading slash, "." for "/".
func fsName(abs string) string {
	n := strings.TrimPrefix(path.Clean(abs), "/")
	if n == "" {
		return "."
	}
	return n
}

func containsLine(text, want string) bool {
	for _, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) == want {
			return true
		}
	}
	return false
}

// cgroupLimit is min(memory.max, memory.high) at dir, finite when either is.
// An absent file is no limit at this level (dir is proved, or an ancestor of
// a proved one; the controller is not enabled for it); an unreadable one is
// an error.
func cgroupLimit(fsys fs.FS, dir string) (uint64, bool, error) {
	limit, finite := uint64(math.MaxUint64), false
	for _, f := range []string{"memory.max", "memory.high"} {
		text, err := readSmall(fsys, path.Join(dir, f))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, false, fmt.Errorf("linux memory: %w", err)
		}
		s := strings.TrimSpace(text)
		if s == "max" {
			continue
		}
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return 0, false, fmt.Errorf("linux memory: %s is neither max nor a byte count", path.Join(dir, f))
		}
		if v < limit {
			limit = v
		}
		finite = true
	}
	return limit, finite, nil
}

func readCgroupUint(fsys fs.FS, name string) (uint64, error) {
	text, err := readSmall(fsys, name)
	if err != nil {
		return 0, fmt.Errorf("linux memory: %w", err)
	}
	v, err := strconv.ParseUint(strings.TrimSpace(text), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("linux memory: %s is not a byte count", name)
	}
	return v, nil
}

// unprotected caps a credit at what dir's own memory.min leaves reclaimable:
// usage under its min is skipped when an ancestor's limit reclaims, so a
// MemoryMin= on our own path's child credited cache the kernel will not free
// (Fable cross-check, finding 4: 5 GiB of headroom where the next allocation
// OOMs). Only our path's child's own min is read; a cousin's, or one inherited
// through memory_recursiveprot, stays recorded open (D-241). An absent
// memory.min is no protection; an unreadable or malformed one is an error.
func unprotected(fsys fs.FS, dir string, credit uint64) (uint64, error) {
	text, err := readSmall(fsys, path.Join(dir, "memory.min"))
	if errors.Is(err, fs.ErrNotExist) {
		return credit, nil
	}
	if err != nil {
		return 0, fmt.Errorf("linux memory: %w", err)
	}
	s := strings.TrimSpace(text)
	if s == "0" {
		return credit, nil
	}
	prot := uint64(math.MaxUint64) // "max": everything is protected
	if s != "max" {
		if prot, err = strconv.ParseUint(s, 10, 64); err != nil {
			return 0, fmt.Errorf("linux memory: %s is neither max nor a byte count", path.Join(dir, "memory.min"))
		}
	}
	current, err := readCgroupUint(fsys, path.Join(dir, "memory.current"))
	if err != nil {
		return 0, err
	}
	if current <= prot {
		return 0, nil
	}
	return min(credit, current-prot), nil
}

// reclaimable is dir's active_file + inactive_file + slab_reclaimable from its
// memory.stat (not `file`, which includes shmem, which only swap frees). A key
// that is absent, or a memory.stat that is, reads 0: less credit, more
// refusals, the safe direction. An unreadable one is an error.
func reclaimable(fsys fs.FS, dir string) (uint64, error) {
	text, err := readSmall(fsys, path.Join(dir, "memory.stat"))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("linux memory: %w", err)
	}
	var sum uint64
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		switch f[0] {
		case "active_file", "inactive_file", "slab_reclaimable":
			// Each counted key once, as exactly `key value`: a duplicate
			// credited twice and a malformed row read as absent (Codex code
			// pass, finding 3). Other keys are not read.
			if seen[f[0]] {
				return 0, fmt.Errorf("linux memory: %s names %s twice", path.Join(dir, "memory.stat"), f[0])
			}
			seen[f[0]] = true
			var v uint64
			var err error
			if len(f) != 2 {
				err = errors.New("not key value")
			} else {
				v, err = strconv.ParseUint(f[1], 10, 64)
			}
			if err != nil {
				return 0, fmt.Errorf("linux memory: %s %s is not a byte count", path.Join(dir, "memory.stat"), f[0])
			}
			sum = satAdd(sum, v)
		}
	}
	return sum, nil
}
