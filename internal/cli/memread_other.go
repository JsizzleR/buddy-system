//go:build !darwin

package cli

import "errors"

// readMemory has no reader off macOS, the platform buddy is developed and
// used on: `buddy mem init` refuses, so the slot is never on where it cannot
// read (memslot.go).
func readMemory() (memReading, error) {
	return memReading{}, errors.New("the memory slot reads memory on macOS only")
}
