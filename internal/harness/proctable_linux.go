//go:build linux

package harness

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

// listProcesses reads /proc/<pid>/stat for every numeric entry. The reads
// span time, so the table is torn by construction; attribution re-observes
// every parent it relies on (descendants.go). A process that exits mid-scan is
// simply not in the table, and absence from a table is never read as "gone".
// Linux is not this project's measured platform.
func listProcesses() ([]procRow, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	rows := make([]procRow, 0, len(entries))
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read /proc/%d/stat: %w", pid, err)
		}
		id, ppid, err := parseLinuxStatParent(pid, b)
		if err != nil {
			return nil, err
		}
		rows = append(rows, procRow{PID: pid, PPID: ppid, PGID: id.PGID, Start: id.Start})
	}
	return rows, nil
}
