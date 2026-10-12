//go:build !darwin && !linux

package harness

import (
	"errors"
	"os"
)

// No worker launches here (proc_other.go); this keeps the package building.
func pipePending(*os.File) (bool, error) {
	return false, errors.New("pipe readiness is unobservable on this platform")
}
