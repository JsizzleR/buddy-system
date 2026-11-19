//go:build darwin

package cli

// readMemory samples the machine's memory in process, through sysctl(3), for
// the memory slot (memslot.go). No `sysctl` child: the gate's hook budget is
// 100 ms, and the operator's earlier local gate spawned four. The figures are
// the ones that gate measured against the 2026-11-17 freezes:
// kern.memorystatus_level (percent available; about 0 at both crashes) times
// hw.memsize, the kernel's pressure level, and the swap in use.

import (
	"encoding/binary"
	"errors"

	"golang.org/x/sys/unix"
)

func readMemory() (memReading, error) {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return memReading{}, err
	}
	level, err := unix.SysctlUint32("kern.memorystatus_level")
	if err != nil {
		return memReading{}, err
	}
	pressure, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level")
	if err != nil {
		return memReading{}, err
	}
	// vm.swapusage is struct xsw_usage: xsu_total, xsu_avail, xsu_used
	// (uint64 each, host order), then the page size and an encrypted flag.
	raw, err := unix.SysctlRaw("vm.swapusage")
	if err != nil {
		return memReading{}, err
	}
	if len(raw) < 24 {
		return memReading{}, errors.New("vm.swapusage is shorter than struct xsw_usage")
	}
	used := binary.LittleEndian.Uint64(raw[16:24])
	if total == 0 || level > 100 {
		return memReading{}, errors.New("the kernel's memory figures are out of range")
	}
	return memReading{
		TotalGB:  float64(total) / (1 << 30),
		Level:    int(level),
		Pressure: int(pressure),
		SwapGB:   float64(used) / (1 << 30),
	}, nil
}
