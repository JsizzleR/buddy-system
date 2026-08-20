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
func quoteText(s string) string {
	return `"` + escapeText(s) + `"`
}
