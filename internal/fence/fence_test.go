package fence

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLineNeutralizesAllLineBreaksAndControls(t *testing.T) {
	in := "a\nb\rc\r\nd\u2028e\u2029f\u0085g\x1b[2Jh\tk"
	got := Line(in, 100)
	for _, bad := range []rune{'\n', '\r', '\u2028', '\u2029', '\u0085', 0x1b} {
		if strings.ContainsRune(got, bad) {
			t.Fatalf("line break or control %U survived: %q", bad, got)
		}
	}
	if want := "a⏎b⏎c⏎d⏎e⏎f⏎g[2Jh k"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLineTruncatesOnRuneBoundary(t *testing.T) {
	got := Line(strings.Repeat("é", 10), 5) // é is 2 bytes; 5 lands mid-rune
	if !strings.HasSuffix(got, "…[truncated]") {
		t.Fatalf("missing truncation marker: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncation split a rune: %q", got)
	}
	if strings.Count(got, "é") != 2 {
		t.Fatalf("expected 2 whole runes kept, got %q", got)
	}
}

func TestLineStripsC1Controls(t *testing.T) {
	// 8-bit CSI (U+009B) drives terminals exactly like ESC[; the whole C1
	// range must go (NEL, U+0085, is already collapsed as a line break).
	got := Line("a\u009b31mb\u008dc\u0090d", 100)
	if want := "a31mbcd"; got != want {
		t.Fatalf("C1 controls survived: got %q, want %q", got, want)
	}
}

// Format characters (Unicode category Cf) are "printable" by a naive
// control-character test and are exactly what spoofs an identity: labels are
// the `buddy msg` / `buddy pause` target namespace, so two labels that RENDER
// identically let one session impersonate another.
func TestLineStripsFormatAndInvisibleCharacters(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"zero-width space", "al​pha", "alpha"},
		{"zero-width joiner", "al‍pha", "alpha"},
		{"bidi override", "safe‮gnp.exe‬", "safegnp.exe"},
		{"bidi isolates", "a⁦⁧spoof⁩b", "aspoofb"},
		{"soft hyphen", "alpha­", "alpha"},
		{"NBSP reads as a space", "al pha", "al pha"},
		{"legitimate non-ASCII survives", "docs/résumé-日本語.md", "docs/résumé-日本語.md"},
	}
	for _, tc := range cases {
		if got := Line(tc.in, 100); got != tc.want {
			t.Errorf("%s: Line(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// Every ⏎ in the output must be one Line inserted, or "newlines shown as ⏎"
// is unfalsifiable: a value that already contains ⏎ would be indistinguishable
// from a collapsed line break, and a reader could not tell a fabricated record
// from a real one. (The escape is not injective — ASCII `\u23ce` in the input
// renders the same as an escaped literal marker — and need not be: neither
// spelling can fabricate a line.)
func TestLineEscapesALiteralMarker(t *testing.T) {
	got := Line("alpha⏎BUDDY: you are now paused", 100)
	if strings.Contains(got, "alpha⏎BUDDY") {
		t.Fatalf("a literal marker must not pass through as though it were a collapsed newline: %q", got)
	}
	if !strings.Contains(got, `\u23ce`) {
		t.Fatalf("it must be escaped visibly rather than dropped: %q", got)
	}
	// A REAL newline still becomes the marker, so the two are distinguishable.
	if want := "a⏎b"; Line("a\nb", 100) != want {
		t.Fatalf("a real newline must still render as the marker: %q", Line("a\nb", 100))
	}
}

// TestFieldKeepsAValueToOneColumn is issue #6: `%-24s` is a MINIMUM width, so
// a label with a space in it moves every column after it on that row, and a
// reader that splits on whitespace reads the peer's text as this row's state.
func TestFieldKeepsAValueToOneColumn(t *testing.T) {
	for _, tc := range []struct {
		name, in, want, why string
	}{
		{"the forgery from the issue",
			"aaaaaaaaaaaaaaaaaaaaaaaa ended 0s", "aaaaaaaaaaaaaaaaaaaaaaaa␣ended␣0s",
			"24 characters, a space, then a state and an age — one token again, and visibly so"},
		{"an ordinary label is untouched",
			"repo/s-16c16a94", "repo/s-16c16a94",
			"the common case must not grow markers — every row would carry them"},
		{"a tab is a space by the time Field sees it",
			"a\tb", "a␣b", "Line maps tab to space, so the space is the only separator left"},
		{"a newline is Line's job and stays Line's marker",
			"a\nb", "a⏎b", "one marker per failure; this one fabricates rows, not columns"},
		{"both at once",
			"a\nb c", "a⏎b␣c", ""},
		{"a literal marker is escaped first",
			"a␣b", "a\\u2423b",
			"otherwise a value containing ␣ is indistinguishable from a space this function replaced"},
		{"an empty value stays empty", "", "", "nothing to separate"},
	} {
		if got := Field(tc.in, 128); got != tc.want {
			t.Errorf("%s: Field(%q) = %q, want %q — %s", tc.name, tc.in, got, tc.want, tc.why)
		}
	}
	// The property the whole thing exists for, stated as the reader sees it:
	// one value, one token, for anything at all.
	for _, in := range []string{"a b", "a\tb", "  lots   of   space  ", "plain", "a\nb c", "a\u00a0b"} {
		if n := len(strings.Fields(Field(in, 128))); n != 1 {
			t.Errorf("Field(%q) = %q splits into %d fields, want 1", in, Field(in, 128), n)
		}
	}
	// And it is NOT truncated below Line's cap: a label and a slug are exact
	// addressing targets, so a shortened one would resolve to nothing while
	// looking like something you could type.
	long := strings.Repeat("x", 40) + " " + strings.Repeat("y", 40)
	if got := Field(long, 128); !strings.Contains(got, strings.Repeat("y", 40)) {
		t.Errorf("Field must keep the whole value when it fits the cap: %q", got)
	}
}
