//go:build !darwin && !linux

package harness

import "errors"

// No start-time pin here, so no worker launches (proc_other.go) and nothing
// is ever sampled.
func listProcesses() ([]procRow, error) {
	return nil, errors.New("the process table is unreadable on this platform")
}
