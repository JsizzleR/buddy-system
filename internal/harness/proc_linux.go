//go:build linux

package harness

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
)

// observeProcess reads /proc/<pid>/stat. The command name (field 2) is in
// parentheses and may itself hold spaces and parentheses, so the fields are
// parsed after the LAST ')': field 3 is then index 0. pgrp is field 5,
// session field 6, starttime (clock ticks since boot) field 22. Linux is not
// this project's measured platform; this keeps the pin available there.
func observeProcess(pid int) (ProcessIdentity, error) {
	if pid <= 0 {
		return ProcessIdentity{}, fmt.Errorf("pid %d is not observable", pid)
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if errors.Is(err, os.ErrNotExist) {
		return ProcessIdentity{}, errProcessGone
	}
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("observe pid %d: %w", pid, err)
	}
	return parseLinuxStat(pid, b)
}

func parseLinuxStat(pid int, b []byte) (ProcessIdentity, error) {
	id, _, err := parseLinuxStatParent(pid, b)
	return id, err
}

// parseLinuxStatParent also returns the parent (field 4), which the process
// table needs and a point observation does not.
func parseLinuxStatParent(pid int, b []byte) (ProcessIdentity, int, error) {
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return ProcessIdentity{}, 0, fmt.Errorf("observe pid %d: malformed stat", pid)
	}
	fields := bytes.Fields(b[i+1:])
	// fields[0] is field 3 (state); field N is fields[N-3].
	if len(fields) < 20 {
		return ProcessIdentity{}, 0, fmt.Errorf("observe pid %d: stat has %d fields after the command", pid, len(fields))
	}
	ppid, err := strconv.Atoi(string(fields[4-3]))
	if err != nil {
		return ProcessIdentity{}, 0, fmt.Errorf("observe pid %d: ppid: %w", pid, err)
	}
	pgid, err := strconv.Atoi(string(fields[5-3]))
	if err != nil {
		return ProcessIdentity{}, 0, fmt.Errorf("observe pid %d: pgrp: %w", pid, err)
	}
	sid, err := strconv.Atoi(string(fields[6-3]))
	if err != nil {
		return ProcessIdentity{}, 0, fmt.Errorf("observe pid %d: session: %w", pid, err)
	}
	start, err := strconv.ParseInt(string(fields[22-3]), 10, 64)
	if err != nil {
		return ProcessIdentity{}, 0, fmt.Errorf("observe pid %d: starttime: %w", pid, err)
	}
	return ProcessIdentity{PID: pid, Start: start, PGID: pgid, SID: sid}, ppid, nil
}
