package cli

import (
	"strings"
	"testing"
)

// Usage gaps measured on one fleet: `claim --dry-run x ...` (the hello hint's
// old word order) got a bare usage line; an unsplit zsh $S reached claim as
// one argument; --measured notes over the cap were refused with no cap in the
// usage line; `ids take ... --desc` got flag's bare "not defined".
func TestUsageSaysWhatWentWrong(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"flag before the slug", []string{"claim", "--dry-run", "x", "--desc", "d", "--scope", "pkg"},
			[]string{"the slug comes first: buddy claim <slug>", `"--dry-run"`}},
		{"unsplit $S as the slug", []string{"claim", " --scope a --scope b"},
			[]string{"the slug comes first", "S=(--scope a --scope b)"}},
		{"unsplit $S after the flags", []string{"claim", "x", "--session", "sess-b", "--desc", "d", " --scope a --scope b"},
			[]string{"takes no further arguments", "zsh does not split", "S=(--scope a --scope b)"}},
		{"ids take --desc", []string{"ids", "take", "decision", "1", "--desc", "d"},
			[]string{"ids take uses --note, not --desc"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errw, code := f.run(t, f.repo, "", tc.args...)
			if code == 0 {
				t.Fatalf("must refuse: %q", errw)
			}
			for _, w := range tc.want {
				if !strings.Contains(errw, w) {
					t.Fatalf("want %q in %q", w, errw)
				}
			}
		})
	}
	// Positive controls: an ordinary stray word gets no zsh lecture, and a
	// slug-first claim is not mistaken for a flag-first one.
	_, errw, _ := f.run(t, f.repo, "", "claim", "x", "--session", "sess-b", "--desc", "d", "stray")
	if !strings.Contains(errw, "takes no further arguments") || strings.Contains(errw, "zsh") {
		t.Fatalf("a plain stray argument: %q", errw)
	}
	if _, errw, code := f.run(t, f.repo, "", "claim", "x", "--session", "sess-b", "--desc", "d", "--scope", "pkg"); code != 0 {
		t.Fatalf("slug-first claim: %q", errw)
	}

	if !strings.Contains(usageMsg, "--measured <what, over what; at most 128 rendered bytes>") {
		t.Fatalf("msg usage must state the --measured cap: %q", usageMsg)
	}
	if !strings.Contains(helloHelpLead, "`buddy claim <slug> --dry-run`") {
		t.Fatalf("hello's hint must put the slug first: %q", helloHelpLead)
	}
}
