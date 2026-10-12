//go:build !darwin && !linux

package harness

import "errors"

// Without a kernel start time there is no pin: a pid alone cannot identify
// the prior worker, so neither a spawned record nor an admission can be made.
// The launch refuses (Spawn cannot publish), and so does every resume.
func observeProcess(int) (ProcessIdentity, error) {
	return ProcessIdentity{}, errors.New("process start time is unobservable on this platform; a worker cannot be pinned here")
}
