//go:build linux && !android

package cli

import "os"

// readMemory reads /proc and the cgroup v2 filesystem through memlinux.go's
// parser, which carries no build constraint so its fixtures run on every
// platform's tier (D-241). os.DirFS("/") resolves /proc/self and the cgroup
// mount points in this process's own namespaces. Android selects linux files
// but is not a buddy platform: memread_other.go answers there.
func readMemory() (memReading, error) {
	m, err := readLinuxMemory(os.DirFS("/"), os.Getpid())
	if err != nil {
		return memReading{}, err
	}
	return m.reading(), nil
}
