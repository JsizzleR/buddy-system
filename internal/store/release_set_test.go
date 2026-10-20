package store

import (
	"errors"
	"testing"
)

// D-093: `release a b c` is one transaction, all or nothing.
func TestReleaseClaimsIsWholeOrNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		slugs  []string
		refuse string // the slug the refusal names; "" = released
	}{
		{name: "control: every named claim is released", slugs: []string{"x", "y", "x"}},
		{name: "a slug nobody holds", slugs: []string{"x", "y", "nope"}, refuse: "nope"},
		{name: "a peer's slug", slugs: []string{"x", "theirs", "y"}, refuse: "theirs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := openTest(t)
			a := hello(t, st, "sess-a", "alpha", "/wt/a")
			b := hello(t, st, "sess-b", "bravo", "/wt/b")
			for slug, scope := range map[string]string{"x": "pkg/x", "y": "pkg/y"} {
				if err := st.Claim(a.SessionID, a.Incarnation, slug, "d", []string{scope}); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.Claim(b.SessionID, b.Incarnation, "theirs", "d", []string{"lib"}); err != nil {
				t.Fatal(err)
			}
			ids, err := st.ReleaseClaims(a.SessionID, a.Incarnation, tc.slugs)
			open := map[string]bool{}
			all, _ := st.Claims(false)
			for _, c := range all {
				open[c.Slug] = true
			}
			if tc.refuse == "" {
				if err != nil || len(ids) != 2 {
					t.Fatalf("ids %v err %v; want two, a repeat counted once", ids, err)
				}
				if open["x"] || open["y"] || !open["theirs"] {
					t.Fatalf("open after release: %v", open)
				}
				return
			}
			var nr ErrNoRelease
			if !errors.As(err, &nr) || nr.Slug != tc.refuse {
				t.Fatalf("want ErrNoRelease naming %q, got %v", tc.refuse, err)
			}
			if !open["x"] || !open["y"] {
				t.Fatalf("a refused set released something: open %v", open)
			}
		})
	}
}
