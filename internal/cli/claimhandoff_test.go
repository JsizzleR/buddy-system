package cli

import (
	"strings"
	"testing"
	"time"
)

// Issue #141 (D-224): `buddy claim` says the handoff line when a session past
// its declared size takes a NEW ITEM — the moment new work starts — and says
// nothing on a refresh, a slot, a sibling of a held slug, under the size, or
// undeclared. It is an observation: the claim is made, the exit code is the
// claim's own. Every silent row differs from a speaking one in ONE input, so
// "it said nothing" cannot mean "it never ran".
func TestClaimSaysTheHandoffLineOnANewItemPastTheSize(t *testing.T) {
	boundedParallel(t)
	for _, tc := range []struct {
		name     string
		declared string
		prompt   int64    // 0 = never observed
		held     []string // slugs sess-b claims first (scope pkg/<slug>)
		slug     string
		scope    string
		peerHas  bool // sess-a holds the scope first: the claim is refused
		speaks   bool
		code     int
	}{
		{"exactly at, a new exclusive item", "300k", 300_000, nil, "item", "pkg/item", false, true, 0},
		{"under by one token", "300k", 299_999, nil, "item", "pkg/item", false, false, 0},
		{"undeclared", "", 320_000, nil, "item", "pkg/item", false, false, 0},
		{"never observed", "300k", 0, nil, "item", "pkg/item", false, false, 0},
		{"a slot serves the item in hand", "300k", 320_000, nil, "browser", ".buddy/slot/browser", false, false, 0},
		{"a slot spelled with ./ is a slot (normalized)", "300k", 320_000, nil, "browser", "./.buddy/slot/browser", false, false, 0},
		{"a slot spelling that cleans to a file is an item", "300k", 320_000, nil, "item", ".buddy/slot/../../pkg", false, true, 0},
		{"a sibling: <held>-shared", "300k", 320_000, []string{"item"}, "item-shared", "docs", false, false, 0},
		{"a sibling the other way: held <slug>-x", "300k", 320_000, []string{"item-tests"}, "item", "docs", false, false, 0},
		{"a sibling, folded (invariant 13)", "300k", 320_000, []string{"Item"}, "item-shared", "docs", false, false, 0},
		{"a prefix that is no sibling speaks", "300k", 320_000, []string{"item"}, "items", "docs", false, true, 0},
		{"another item held, a new one taken", "300k", 320_000, []string{"first"}, "second", "docs", false, true, 0},
		{"a re-claim refreshes", "300k", 320_000, []string{"item"}, "item", "pkg/item", false, false, 0},
		{"another letter case is another claim", "300k", 320_000, []string{"Item"}, "item", "docs", false, true, 0},
		{"refused", "300k", 320_000, nil, "item", "pkg/item", true, false, ExitRefused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.initAndHello(t)
			if tc.prompt > 0 {
				f.observeB(t, tc.prompt)
			}
			for _, h := range tc.held {
				if _, errw, code := f.run(t, f.wtB, "", "claim", h, "--session", "sess-b", "--desc", "d", "--scope", "pkg/"+h); code != 0 {
					t.Fatalf("held %s: %s", h, errw)
				}
			}
			if tc.peerHas {
				if _, errw, code := f.run(t, f.repo, "", "claim", "theirs", "--session", "sess-a", "--desc", "d", "--scope", tc.scope); code != 0 {
					t.Fatal(errw)
				}
			}
			f.env[EnvHandoffAt] = tc.declared
			out, errw, code := f.run(t, f.wtB, "", "claim", tc.slug, "--session", "sess-b", "--desc", "d", "--scope", tc.scope)
			if code != tc.code {
				t.Fatalf("exit %d, want %d: %s %s", code, tc.code, out, errw)
			}
			if tc.code == 0 && !strings.HasPrefix(out, `claimed "`+tc.slug+`"`) {
				t.Fatalf("the claim must be made and said first:\n%s", out)
			}
			said := strings.Contains(out+errw, handoffMark)
			if said != tc.speaks {
				t.Fatalf("handoff line said=%v, want %v:\n%s%s", said, tc.speaks, out, errw)
			}
			if tc.speaks {
				line := out[strings.Index(out, "BUDDY: your prompt was "):]
				if strings.Count(line, "\n") != 1 || !strings.Contains(line, "your prompt was "+tokens(tc.prompt)+" at your last observed request (turn 3m ago)") {
					t.Fatalf("want handoffNote's one line, last:\n%s", out)
				}
				return
			}
			if tc.code != 0 {
				return
			}
			// Positive control for the silent claims: declared under the
			// observed size, a NEW slug on a plain scope from the same
			// session speaks, so the guard that kept it quiet was armed.
			f.env[EnvHandoffAt] = "1k"
			if tc.prompt == 0 {
				f.observeB(t, 2_000)
			}
			ctl, errw, code := f.run(t, f.wtB, "", "claim", "zz-control", "--session", "sess-b", "--desc", "d", "--scope", "ctl")
			if code != 0 || !strings.Contains(ctl, handoffMark) {
				t.Fatalf("control: a new item past the size must speak (exit %d): %s %s", code, ctl, errw)
			}
		})
	}
}

// A predecessor incarnation's size is not this session's (sampleOf, D-055):
// revived and unobserved, the claim says nothing; the same session observed
// at that size in its own incarnation speaks (the control).
func TestClaimHandoffIgnoresAPredecessorsObservation(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.observeB(t, 320_000)
	f.env[EnvHandoffAt] = "300k"
	hook := hookJSON("sess-b", f.wtB, "", "")
	if _, errw, code := f.run(t, f.wtB, hook, "bye"); code != 0 {
		t.Fatal(errw)
	}
	f.clock = f.clock.Add(time.Minute)
	if _, errw, code := f.run(t, f.wtB, hook, "hello", "--label", "bravo"); code != 0 {
		t.Fatal(errw)
	}
	out, errw, code := f.run(t, f.wtB, "", "claim", "item", "--session", "sess-b", "--desc", "d", "--scope", "pkg")
	if code != 0 || strings.Contains(out, handoffMark) {
		t.Fatalf("a revived incarnation has no observation of its own (exit %d): %s %s", code, out, errw)
	}
	f.observeB(t, 320_000)
	out, errw, code = f.run(t, f.wtB, "", "claim", "other", "--session", "sess-b", "--desc", "d", "--scope", "pkg2")
	if code != 0 || !strings.Contains(out, handoffMark) {
		t.Fatalf("control: the live incarnation's own 320k speaks (exit %d): %s %s", code, out, errw)
	}
}
