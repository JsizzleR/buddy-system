package cli

import (
	"bytes"
	"strings"
	"testing"
)

// The digest's help line and `--help`'s wait row must not tell a Claude
// session to arm `/loop buddy wait check` unconditionally (#139).
//
// D-203 changed the line every declaration prints ("arm nothing if a wake is
// due within 50m ...; else `/loop buddy wait check` in THIS session, no
// interval"), the skills and USAGE, and missed these two: the digest said
// "then arm your own `/loop buddy wait check`" at every session start, and
// the wait row said "arm this session's /loop buddy wait check (~50m on its
// 1h cache tier)", the interval shape D-203 removed. D-203's measured failure
// was a lane that armed a wake it did not need. Both now defer to the
// declaration's own reply, which is the one place that knows the tier.
//
// The old texts are the positive control that the predicate sees the defect.
func TestWaitAdviceDefersToTheDeclarationsReply(t *testing.T) {
	boundedParallel(t)
	var b bytes.Buffer
	usage(&b)
	help := b.String()
	start := strings.Index(help, "wait [--on <slug>]")
	end := strings.Index(help, "A wait reserves nothing.")
	if start < 0 || end < start {
		t.Fatalf("the wait row is not in --help:\n%s", help)
	}
	row := strings.Join(strings.Fields(help[start:end]), " ")

	bad := func(text string) []string {
		var why []string
		if !strings.Contains(text, "/loop buddy wait check") {
			why = append(why, "names no /loop buddy wait check")
		}
		if !strings.Contains(text, "only if its reply says to") {
			why = append(why, "arms the loop unconditionally")
		}
		// A trailing "every <N>" after /loop is loopInterval's, below; a bare
		// "every" is not (the digest says "lists every verb").
		if strings.Contains(text, "50m") {
			why = append(why, "puts an interval beside /loop")
		}
		return why
	}
	for _, old := range []string{
		"`--until 2h` alone, then arm your own `/loop buddy wait check`;",
		"Claude: arm this session's /loop buddy wait check (~50m on its 1h cache tier).",
	} {
		if len(bad(old)) == 0 {
			t.Fatalf("control: the predicate passes the pre-#139 text %q", old)
		}
	}
	for name, text := range map[string]string{"helloHelpLine": helloHelpLine, "wait usage row": row} {
		if why := bad(text); len(why) > 0 {
			t.Errorf("%s %s:\n%s", name, strings.Join(why, "; "), text)
		}
		if i := strings.Index(text, "/loop "); i >= 0 && loopInterval(text[i+len("/loop "):]) != "" {
			t.Errorf("%s: the text after /loop parses to an interval:\n%s", name, text)
		}
	}
}
