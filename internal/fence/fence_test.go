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

// The marker must be injective, or "newlines shown as ⏎" is unfalsifiable: a
// value that already contains ⏎ would be indistinguishable from a collapsed
// line break, and a reader could not tell a fabricated record from a real one.
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
