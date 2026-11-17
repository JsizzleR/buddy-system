package fence

import (
	"strings"
	"testing"
)

// Every class the predicate refuses, each beside an admitted id that differs
// from it by the one thing under test, so a refusal here cannot be the
// predicate refusing everything (the positive controls run in the same table).
func TestValidSessionID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		// Positive controls: the measured native shapes and the suite's ids.
		{"07f63349-294a-489d-8f9b-79a9cb7b37a9", true}, // Claude, UUIDv4
		{"019a7f3e-5b2c-7d41-9e0f-123456789abc", true}, // Codex, UUIDv7
		{"sess-a", true},
		{"X", true},
		{"9", true},
		{"worker.label_2", true},
		{"S-upper", true}, // only the lower-case `s-` prefix is the short form
		{"alls", true},
		{"sx", true},
		{strings.Repeat("a", SessionIDCap), true},

		{"", false},
		{strings.Repeat("a", SessionIDCap+1), false},
		{"all", false},
		{"s-1234abcd", false},
		{"s-", false},
		{"-x", false},
		{".x", false},
		{"_x", false},
		{"sess x", false},
		{"sess\tx", false},
		{"sess\nx", false},
		{"sess\r\nx", false},
		{"sess x", false},
		{"sess\u0085x", false},
		{"sess\x1bx", false},
		{"sess‮x", false},
		{"sess​x", false},
		{"sess x", false},
		{"sess\xc3", false}, // a split rune
		{"café", false},
		{"wt/s-1234abcd", false},
		{"a:b", false},
		{"a@b", false},
		{"it's", false},
		{"a`b", false},
		{"a;b", false},
		{"a|b", false},
		{"a$b", false},
		{"a\x00b", false},
	}
	for _, c := range cases {
		if got := ValidSessionID(c.id); got != c.want {
			t.Errorf("ValidSessionID(%q) = %v, want %v", c.id, got, c.want)
		}
	}
}

// An admitted id survives every fence unchanged, the property the paste rule
// (store and cli) rests on: printed through Line or Field at the id width, it
// is byte-for-byte the key.
func TestValidSessionIDIsFenceStable(t *testing.T) {
	for _, id := range []string{"07f63349-294a-489d-8f9b-79a9cb7b37a9", "sess-a", "a.b_c-d", strings.Repeat("Z", SessionIDCap)} {
		if !ValidSessionID(id) {
			t.Fatalf("control %q refused", id)
		}
		if Line(id, SessionIDCap) != id || Field(id, SessionIDCap) != id {
			t.Fatalf("an admitted id %q is altered by a fence", id)
		}
	}
}
