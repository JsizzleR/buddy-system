package cli

import (
	"strings"
	"testing"
)

// #72: a coordinator's handoff naming the browser slot among its claims is
// refused whole, in one line that says what to do instead, and exits 1 as the
// verb's other transfer refusals do; the same handoff without it goes through.
func TestReleaseToRefusesTheBrowserSlotInAHandoff(t *testing.T) {
	boundedParallel(t)
	b := newBatchFx(t)
	b.form(t)
	b.ok(t, "sess-a", "claim", "orc-p", "--desc", "coordinates", "--scope", ".buddy/slot/orc-p")
	b.ok(t, "sess-a", "claim", "tabs", "--desc", "two tabs open", "--scope", ".buddy/slot/browser", "--scope", "notes/b.md")

	out, errw, code := b.as(t, "sess-a", "release", "orc-p", "herm", "tabs", "--to", "bravo")
	if code != 1 {
		t.Fatalf("exit %d, want 1 like every transfer refusal:\n%s\n%s", code, out, errw)
	}
	line := strings.TrimSpace(errw)
	if strings.Count(line, "\n") != 0 ||
		!strings.Contains(line, `claim "tabs" reaches .buddy/slot/browser by ".buddy/slot/browser", and the browser slot stays with its holder`) ||
		!strings.Contains(line, "claim every scope of it reaching the slot alone under another slug, then release them from this one") ||
		!strings.Contains(line, "release it plainly once the browser work is over") ||
		!strings.HasSuffix(line, "nothing was handed on") {
		t.Fatalf("one line naming the claim, the remedy and that nothing moved:\n%s", errw)
	}
	ls := b.ok(t, "sess-a", "ls")
	for _, slug := range []string{"orc-p", "herm", "tabs"} {
		if !strings.Contains(ls, slug) || strings.Contains(b.ok(t, "sess-a", "who", slug), "held by:\n- bravo") {
			t.Fatalf("refused, so %q stays alpha's:\n%s", slug, ls)
		}
	}

	// Positive control: the handoff without the browser claim moves; tabs stays.
	if got := b.ok(t, "sess-a", "release", "orc-p", "herm", "--to", "bravo"); !strings.HasPrefix(got, "handed 2 claims to bravo") {
		t.Fatalf("control: the handoff without the slot must move:\n%s", got)
	}
	if w := b.ok(t, "sess-a", "who", "tabs"); !strings.Contains(w, "alpha") {
		t.Fatalf("tabs stays with alpha:\n%s", w)
	}
	// And released plainly once the browser work is over.
	b.ok(t, "sess-a", "release", "tabs")
}
