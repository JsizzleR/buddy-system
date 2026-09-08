package buddylist

import (
	"testing"
	"time"
)

// Client's whole job is that the socket path and the round-trip bound are
// spelled once. The path is compile-checked by having one spelling; the bound
// is not, because its dangerous value is the ZERO one — net.DialTimeout reads
// 0 as "no deadline", so a caller that omits the field would block forever on
// a wedged daemon rather than costing one notice. This pins the defaulting.
//
// The negative direction has a positive control beside it: without the "an
// explicit bound is kept" row, a timeout() that returned defaultCallTimeout
// unconditionally would pass every other row in the table.
func TestClientTimeoutDefaultsRatherThanBlockingForever(t *testing.T) {
	cases := []struct {
		name string
		set  time.Duration
		want time.Duration
	}{
		{"unset means the default, never zero", 0, defaultCallTimeout},
		{"an explicit bound is kept", 2 * time.Second, 2 * time.Second},
		// A negative duration is a caller bug, and net.DialTimeout would treat
		// it as an already-expired deadline: every call fails, and the report
		// is "daemon not reachable", which is a lie about the daemon.
		{"a negative bound is not honoured", -time.Second, defaultCallTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := (Client{Socket: "/nope.sock", Timeout: tc.set}).timeout(); got != tc.want {
				t.Fatalf("timeout() = %v, want %v", got, tc.want)
			}
		})
	}
}
