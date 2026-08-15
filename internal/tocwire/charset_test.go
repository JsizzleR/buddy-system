package tocwire

import "testing"

func TestWireCharsetRoundTrip(t *testing.T) {
	cases := []struct{ in, wire, back string }{
		// Typographic chars degrade to ASCII before the CP1252 step.
		{"live — journaled", "live - journaled", "live - journaled"},
		{"“quoted” … ‘single’", `"quoted" ... 'single'`, `"quoted" ... 'single'`},
		// Latin-1 text survives as real CP1252 and decodes back identically.
		{"café señor", "caf\xe9 se\xf1or", "café señor"},
		// Unmappable runes degrade rather than corrupt the frame.
		{"emoji \U0001F3C3 run", "emoji ? run", "emoji ? run"},
	}
	for _, tc := range cases {
		got := toWire(tc.in)
		if string(got) != tc.wire {
			t.Errorf("toWire(%q) = %q, want %q", tc.in, got, tc.wire)
		}
		if back := fromWire(got); back != tc.back {
			t.Errorf("fromWire(toWire(%q)) = %q, want %q", tc.in, back, tc.back)
		}
	}
	// Inbound from a real AIM client: CP1252 bytes decode to UTF-8.
	if got := fromWire([]byte("caf\xe9 \x97 dash")); got != "café — dash" {
		t.Errorf("CP1252 inbound decode wrong: %q", got)
	}
}
