package tocwire

import "strings"

// escapeText backslash-escapes the measured TOC escape set: \ { } ( ) [ ] $ "
// (docs/p0-facts.md; the server unescapes exactly these).
func escapeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\', '{', '}', '(', ')', '[', ']', '$', '"':
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// quoteText escapes and double-quotes a text argument for the server's
// space-separated, LazyQuotes CSV tokenizer.
//
// The ASCII fold runs FIRST (D-221). toWire applies asciiFriendly to the whole
// command line, and it used to be the only place it ran: AFTER this escaped,
// so a curly quote became a bare `"` nothing had escaped, and `ok” x` went out
// as `"\[me\] ok" x"`: the argument ended after the sig (panel lens a). Folded
// here, the `"` it produces is escaped like any other, and toWire's pass finds
// nothing left to fold.
func quoteText(s string) string {
	return `"` + escapeText(asciiFriendly.Replace(s)) + `"`
}
