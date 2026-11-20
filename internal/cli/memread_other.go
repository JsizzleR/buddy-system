//go:build !darwin && (!linux || android)

package cli

import "errors"

// readMemory has no reader off macOS and Linux: `buddy mem init` refuses, so
// the slot is never on where it cannot read (memslot.go). The BSDs build buddy
// but nobody runs it there and no CI runner exists, so a reader would be
// untested code in a safety device (FreeBSD's ARC is wired, its free+inactive
// misleads); Windows does not build buddy at all (flock, Setpgid). D-241.
func readMemory() (memReading, error) {
	return memReading{}, errors.New("the memory slot reads memory on macOS and Linux only")
}
