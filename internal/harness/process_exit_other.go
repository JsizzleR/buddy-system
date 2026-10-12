//go:build !darwin && !linux

package harness

import "errors"

// Without a way to observe exit without reaping, terminate waits the whole
// grace before SIGKILL rather than reap early and lose the group's identity.
func awaitExit(int) error {
	return errors.New("observing a child's exit without reaping it is unsupported on this platform")
}
