package tocwire

import (
	"strings"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
)

// The TOC-era wire charset is windows-1252: AIM 5.x renders bytes as CP1252,
// so UTF-8 multi-byte punctuation arrives as mojibake (measured: an em dash
// sent as UTF-8 displayed as garbage in AIM 5.1). tocwire therefore converts
// at the boundary — Go strings are UTF-8 inside, CP1252 on the wire, both
// directions — so text round-trips through the server's reflection cleanly.
// A modern client sending genuine UTF-8 would be mis-decoded here; the only
// other senders on this fleet are tocwire instances, which all encode.

// asciiFriendly maps typographic characters to ASCII before the lossy CP1252
// step, so the common cases degrade gracefully instead of to '?'.
var asciiFriendly = strings.NewReplacer(
	"—", "-", // em dash
	"–", "-", // en dash
	"‘", "'", "’", "'", // curly single quotes
	"“", `"`, "”", `"`, // curly double quotes
	"…", "...", // ellipsis
	" ", " ", // no-break space
	"→", "->", "←", "<-", // arrows
)

var (
	cp1252Enc = encoding.ReplaceUnsupported(charmap.Windows1252.NewEncoder())
	cp1252Dec = charmap.Windows1252.NewDecoder()
)

// toWire converts a UTF-8 command line to CP1252 wire bytes (best effort:
// unmappable runes become the encoder's substitute).
func toWire(line string) []byte {
	b, err := cp1252Enc.Bytes([]byte(asciiFriendly.Replace(line)))
	if err != nil {
		return []byte(line) // ReplaceUnsupported should never error; be lossless if it does
	}
	// The charmap substitute is 0x1A, which AIM renders as a box; '?' is kinder.
	for i, c := range b {
		if c == 0x1a {
			b[i] = '?'
		}
	}
	return b
}

// fromWire converts CP1252 wire bytes to a UTF-8 string. Every byte maps, so
// this cannot fail.
func fromWire(p []byte) string {
	b, err := cp1252Dec.Bytes(p)
	if err != nil {
		return string(p)
	}
	return string(b)
}
