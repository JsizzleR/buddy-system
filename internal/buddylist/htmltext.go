package buddylist

import (
	"html"
	"strings"
)

// htmlToText flattens the HTML envelope AIM-era clients wrap messages in
// (measured from AIM 5.1: `<HTML><BODY BGCOLOR="#ffffff"><FONT LANG="0">hi
// </FONT></BODY></HTML>`, entities like &quot; included). It applies ONLY to
// text that arrives enveloped — a plain-text message containing a literal
// '<' (an agent pasting code) passes through untouched.
func htmlToText(s string) string {
	if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(s)), "<HTML") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	inTag := false
	for i := 0; i < len(s); i++ {
		switch {
		case inTag:
			if s[i] == '>' {
				inTag = false
			}
		case s[i] == '<':
			// Line breaks are the one tag carrying content structure. Compare
			// just the tag prefix — upper-casing the whole remainder per '<'
			// was O(n²) on hostile input.
			if i+3 <= len(s) && strings.EqualFold(s[i:i+3], "<BR") {
				b.WriteByte('\n')
			}
			inTag = true
		default:
			b.WriteByte(s[i])
		}
	}
	return strings.TrimSpace(html.UnescapeString(b.String()))
}
