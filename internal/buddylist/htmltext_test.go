package buddylist

import "testing"

func TestHTMLToText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"aim-envelope", `<HTML><BODY BGCOLOR="#ffffff"><FONT LANG="0">hi</FONT></BODY></HTML>`, "hi"},
		{"entities", `<HTML><BODY><FONT>` + "` &quot; hi ..." + `</FONT></BODY></HTML>`, "` \" hi ..."},
		{"lowercase-envelope", `<html><body>ok</body></html>`, "ok"},
		{"line-breaks", `<HTML><BODY>one<BR>two<br/>three</BODY></HTML>`, "one\ntwo\nthree"},
		{"plain-untouched", "keep <T> and a < b intact", "keep <T> and a < b intact"},
		{"plain-entity-untouched", "literal &quot; stays escaped in plain text", "literal &quot; stays escaped in plain text"},
		{"unclosed-tag", `<HTML><BODY>trailing <FONT`, "trailing"},
	}
	for _, tc := range cases {
		if got := htmlToText(tc.in); got != tc.want {
			t.Errorf("%s: htmlToText(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}
