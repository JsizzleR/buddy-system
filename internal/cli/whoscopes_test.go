package cli

import (
	"fmt"
	"strings"
	"testing"
)

// D-094: `who <slug>` prints every scope of that claim, one fenced line each,
// with no 512-byte cut, while every other view keeps its cap. Measured: a
// successor handed a 22-scope claim could not re-claim it with the SAME
// scopes because every view cut the list.
func TestWhoOfAClaimPrintsEveryScopeUncut(t *testing.T) {
	boundedParallel(t)
	b := newBatchFx(t)
	args := []string{"claim", "big", "--desc", "d"}
	var want []string
	for i := 0; i < 30; i++ {
		sc := fmt.Sprintf("internal/some/long/package/path/file_%02d.go", i)
		want = append(want, sc)
		args = append(args, "--scope", sc)
	}
	args = append(args, "--scope", "dir with space/f.go")
	b.ok(t, "sess-a", args...)

	out := b.ok(t, "sess-b", "who", "big")
	if !strings.Contains(out, `claim "big" holds 31 scope(s), every one`) {
		t.Fatalf("a header with the count:\n%s", out)
	}
	for _, sc := range want {
		if strings.Count(out, "\n  scope "+sc+"\n") != 1 {
			t.Fatalf("scope %s must be on its own line exactly once:\n%s", sc, out)
		}
	}
	if !strings.Contains(out, "\n  scope dir␣with␣space/f.go   (rendered:") {
		t.Fatalf("a scope fence.Field altered is printed as one token and said to be altered:\n%s", out)
	}
	// Positive control for the cut the block exists to avoid: the CLAIMS HELD
	// row in the same report still caps the list.
	held := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "  big ") {
			held = l
		}
	}
	if held == "" || strings.Contains(held, want[29]) {
		t.Fatalf("the CLAIMS HELD row keeps its cap (the last scope is not on it):\n%s", held)
	}
	// Only a claim target prints it: `who alpha` is about the session.
	if out := b.ok(t, "sess-b", "who", "alpha"); strings.Contains(out, "every one") {
		t.Fatalf("a label target prints no scope block:\n%s", out)
	}
}
