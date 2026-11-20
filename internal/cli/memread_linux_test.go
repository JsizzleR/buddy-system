//go:build linux && !android

package cli

import (
	"errors"
	"testing"
)

// TestReadMemoryLive is the ONE test that reads a real machine's memory, and
// only on Linux (CI's ubuntu runner), so the operator's darwin runs never do.
// It skips on exactly one error, the named cgroup v1 refusal (a healthy
// v1 host is deliberately unsupported, Codex design finding 5), and fails on
// any other: a reader that cannot read the runner it is gated on is a red
// run. On GitHub's runner it exercises /proc, mountinfo placement and the
// no-limit path; a binding limit is the fixtures' (memlinux_test.go).
func TestReadMemoryLive(t *testing.T) {
	r, err := readMemory()
	if errors.Is(err, errCgroupV1) {
		t.Skipf("this host runs a cgroup v1 memory controller, which the slot refuses: %v", err)
	}
	if err != nil {
		t.Fatalf("readMemory on this Linux host: %v", err)
	}
	if why := r.unusable(); why != "" {
		t.Fatalf("reading %+v is not usable: %s", r, why)
	}
	if r.AvailGB <= 0 || r.Basis == "" || r.SwapGB != 0 || (r.Pressure != 1 && r.Pressure != 4) {
		t.Fatalf("reading %+v: want AvailGB > 0, a Basis, SwapGB 0, pressure 1 or 4", r)
	}
	t.Logf("reading: %s", r.Basis)
}
