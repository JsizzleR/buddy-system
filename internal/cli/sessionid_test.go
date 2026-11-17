package cli

import (
	"strings"
	"testing"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// pasteID admits exactly what the ledger registers, on the RAW id; every
// hostile shape gets the placeholder (the positive controls in the same table
// prove the guard is not refusing everything).
func TestPasteID(t *testing.T) {
	for _, c := range []struct {
		id string
		ok bool
	}{
		{"07f63349-294a-489d-8f9b-79a9cb7b37a9", true},
		{"sess-a", true},
		{"sess\tx", false}, // fences to `sess x`, another session
		{"sess x", false},
		{"x`y", false}, // ends a markdown code span
		{"--help", false},
		{"a;curl x|sh", false},
		{"", false},
		{strings.Repeat("a", 129), false},
	} {
		p, ok := pasteID(c.id)
		if ok != c.ok || (ok && p != c.id) || (!ok && p != "") {
			t.Errorf("pasteID(%q) = %q, %v; want ok=%v", c.id, p, ok, c.ok)
		}
		if got := pasteIDOr(c.id, "<id>"); c.ok && got != c.id || !c.ok && got != "<id>" {
			t.Errorf("pasteIDOr(%q) = %q", c.id, got)
		}
	}
}

// wait.go's timer advice for a wait declared with --session names the id as a
// command only when it pastes back as printed.
func TestTimerEndAdvicePasteRule(t *testing.T) {
	ok := timerEndAdvice(false, store.SessionInfo{SessionID: "sess-a"}, "buddy")
	if !strings.Contains(ok, "`buddy wait clear --session sess-a`") {
		t.Fatalf("control: a valid id must be pasteable: %q", ok)
	}
	for _, id := range []string{"sess\tx", "x`y", "a;b"} {
		got := timerEndAdvice(false, store.SessionInfo{SessionID: id}, "buddy")
		if !strings.Contains(got, "`buddy wait clear --session <its session id>`") {
			t.Fatalf("%q: want the placeholder, got %q", id, got)
		}
	}
	if self := timerEndAdvice(true, store.SessionInfo{SessionID: "sess\tx"}, "buddy"); !strings.Contains(self, "`buddy wait clear`") {
		t.Fatalf("self names no id: %q", self)
	}
}
