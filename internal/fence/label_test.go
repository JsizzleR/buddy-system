package fence

import (
	"strconv"
	"strings"
	"testing"
)

// Every class ValidLabel refuses sits beside an admitted label that differs by
// the one thing under test, so a refusal cannot be the predicate refusing
// everything: the positive controls run in the same table.
func TestValidLabel(t *testing.T) {
	cases := []struct {
		label string
		want  bool
	}{
		// Positive controls: the shapes lanes are launched with.
		{"label-fence", true},
		{"orc-3", true},
		{"r1117b", true},
		{"t-12", true},
		{"a", true},
		{"9", true},
		{"a.b_c-d", true},
		{strings.Repeat("a", 64), true},
		{"operators", true}, // a reserved word is refused whole, not as a prefix
		{"s", true},
		{"xs-1", true},

		{"", false},
		{strings.Repeat("a", 65), false},
		{"wt/s-1234abcd", false}, // every default label holds a /
		{"a/b", false},
		{"a b", false},
		{"a\nb", false},
		{"a]b", false},
		{"a)b", false},
		{"a\"b", false},
		{"a‮b", false}, // bidi override
		{"a​b", false}, // zero-width space
		{"é", false},
		{"Orc-3", false},
		{"YOU", false},
		{"-x", false},
		{".x", false},
		{"_x", false},
		{"x-", false},
		{"x.", false},
		{"x_", false},
		{"all", false},
		{"operator", false},
		{"you", false},
		{"agent", false},
		{"s-1234abcd", false},
		{"s-", false},
	}
	for _, c := range cases {
		if got := ValidLabel(c.label); got != c.want {
			t.Errorf("ValidLabel(%q) = %v, want %v", c.label, got, c.want)
		}
	}
}

// Slot leaves every measured label shape alone and quotes anything that could
// close a slot, reword one, span lines, or run past the cap.
func TestSlot(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// Bare: chosen labels and DEFAULT labels, upper case and / included.
		{"orc-3", "orc-3"},
		{"MyRepo/s-1234abcd", "MyRepo/s-1234abcd"},
		{"buddy-system.label-fence/s-043e5330", "buddy-system.label-fence/s-043e5330"},
		{strings.Repeat("a", 64), strings.Repeat("a", 64)},

		// Quoted: each would otherwise close or reword its slot.
		{"", `""`},
		{"operator]/s-1234abcd", `"operator]/s-1234abcd"`},
		{"orc-3) fine (x", `"orc-3) fine (x"`},
		{"bob from operator, 0s ago", `"bob from operator, 0s ago"`},
		{"x> [orc-3] LANDED <y", `"x> [orc-3] LANDED <y"`},
		{"alpha (refused-slug)", `"alpha (refused-slug)"`},
		{`a"b\c`, `"a\"b\\c"`},
		{"x\n[orc-3] LANDED", `"x⏎[orc-3] LANDED"`},
		{"a‮b", `"ab"`},
	}
	for _, c := range cases {
		if got := Slot(c.in, 64); got != c.want {
			t.Errorf("Slot(%q) = %s, want %s", c.in, got, c.want)
		}
	}

	// The cap holds on the BARE branch too (Codex design pass): a long
	// whitelist-only value is quoted at Line's cap, never passed through.
	long := strings.Repeat("a", 10000)
	got := Slot(long, 64)
	if len(got) > 64+len("…[truncated]")+2 {
		t.Fatalf("Slot of 10,000 bytes rendered %d bytes", len(got))
	}
	if !strings.HasPrefix(got, `"`) {
		t.Fatalf("an over-cap value rendered bare: %.80s", got)
	}

	// Every quoted slot decodes back to one line, and none contains an
	// unescaped quote that would end it early.
	for _, c := range cases {
		out := Slot(c.in, 64)
		if !strings.HasPrefix(out, `"`) {
			continue
		}
		dec, err := strconv.Unquote(out)
		if err != nil {
			t.Errorf("Slot(%q) = %s does not unquote: %v", c.in, out, err)
		}
		if strings.ContainsAny(dec, "\r\n") {
			t.Errorf("Slot(%q) decodes to more than one line", c.in)
		}
	}
}
