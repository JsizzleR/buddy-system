package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// `buddy whose` reports the CLAIM register as well as the dirty one (issue #13).
//
// The incident: `whose` reports who has uncommitted CHANGES to a path. Its name
// reads as ownership, so it was used to answer "is this claimed?" — and it is
// silent in exactly the state that matters most, because a session that has
// claimed a path and NOT YET EDITED IT has no dirty row. That is the state every
// session is in immediately after claiming. Two sessions read "unheld" off this
// command in one day; one wrote a shared file on that basis that another session
// had held for hours.

// THE INCIDENT'S EXACT STATE: claimed, untouched, and therefore invisible to
// every dirty scan. If this test is ever "simplified" by dirtying the file
// first, it stops testing the thing that broke.
func TestWhoseReportsAClaimOnAPathNobodyHasTouched(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)

	if _, errw, code := f.run(t, f.repo, "", "claim", "api-work", "--session", "sess-a",
		"--desc", "holding it", "--scope", "internal/api"); code != 0 {
		t.Fatalf("claim: %s", errw)
	}

	out, errw, code := f.run(t, f.repo, "", "whose", "internal/api/server.go")
	if code != 0 {
		t.Fatalf("whose: %s", errw)
	}
	if !strings.Contains(out, "CLAIMED BY") || !strings.Contains(out, "api-work") {
		t.Fatalf("whose did not report the claim that covers the path: %q", out)
	}
	if !strings.Contains(out, "alpha") {
		t.Fatalf("whose did not name the holder: %q", out)
	}
	// The register it always had must still be there, and must be LABELLED, so
	// the two answers cannot be read as one. Without the label, "no session has
	// it recorded" prints directly beneath "CLAIMED BY api-work" and reads as a
	// contradiction of it.
	if !strings.Contains(out, "DIRTY IN") {
		t.Fatalf("whose dropped or unlabelled the dirty register: %q", out)
	}
	if strings.Index(out, "CLAIMED BY") > strings.Index(out, "DIRTY IN") {
		t.Fatalf("the claim register must come first — it is the one that reserves: %q", out)
	}
}

// The other half of the defect: an UNCLAIMED path must say so out loud. An
// absent line and a "none" line are exactly what the incident confused.
func TestWhoseSaysNoneRatherThanStayingSilent(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)

	// CONTROL: with a claim in the ledger on a DIFFERENT scope, so the test
	// proves containment and not an empty table.
	if _, errw, code := f.run(t, f.repo, "", "claim", "docs-work", "--session", "sess-a",
		"--desc", "elsewhere", "--scope", "docs"); code != 0 {
		t.Fatalf("claim: %s", errw)
	}

	out, _, code := f.run(t, f.repo, "", "whose", "internal/api/server.go")
	if code != 0 {
		t.Fatal("whose failed")
	}
	if !strings.Contains(out, "CLAIMED BY   (none)") {
		t.Fatalf("an unclaimed path must say so: %q", out)
	}
	if strings.Contains(out, "docs-work") {
		t.Fatalf("a claim on docs was reported for a path under internal/api: %q", out)
	}
}

// Containment is invariant 14's rule and nothing looser: a claim on a prefix
// covers the paths under it, and a claim on a sibling covers nothing.
func TestWhoseClaimLookupUsesScopeContainment(t *testing.T) {
	boundedParallel(t)
	cases := []struct {
		name, scope, ask string
		want             bool
	}{
		{"exact path", "internal/api/server.go", "internal/api/server.go", true},
		{"prefix covers what is under it", "internal/api", "internal/api/server.go", true},
		{"deeper prefix covers deeper", "internal", "internal/api/server.go", true},
		{"a sibling covers nothing", "internal/apiv2", "internal/api/server.go", false},
		{"a longer name is not a prefix match", "internal/ap", "internal/api/server.go", false},
		{"a file does not cover its siblings", "internal/api/other.go", "internal/api/server.go", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			boundedParallel(t)
			f := newFixture(t)
			f.initAndHello(t)
			if _, errw, code := f.run(t, f.repo, "", "claim", "held", "--session", "sess-a",
				"--desc", "x", "--scope", tc.scope); code != 0 {
				t.Fatalf("claim %s: %s", tc.scope, errw)
			}
			out, _, code := f.run(t, f.repo, "", "whose", tc.ask)
			if code != 0 {
				t.Fatal("whose failed")
			}
			got := strings.Contains(out, "CLAIMED BY   held")
			if got != tc.want {
				t.Fatalf("scope %q vs path %q: reported=%v want=%v\n%s", tc.scope, tc.ask, got, tc.want, out)
			}
		})
	}
}

// A directory argument asks a wider question than containment: "who has
// anything in src/?". A claim held UNDER the directory is reported, and the
// output says it is NOT the same relation — it reserves nothing about the
// directory itself.
func TestWhoseOnADirectoryReportsClaimsHeldUnderIt(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if err := os.MkdirAll(filepath.Join(f.repo, "internal", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, errw, code := f.run(t, f.repo, "", "claim", "deep", "--session", "sess-a",
		"--desc", "x", "--scope", "internal/api"); code != 0 {
		t.Fatalf("claim: %s", errw)
	}
	out, _, code := f.run(t, f.repo, "", "whose", "internal")
	if code != 0 {
		t.Fatal("whose failed")
	}
	if !strings.Contains(out, "deep") {
		t.Fatalf("a claim held under the directory was not reported: %q", out)
	}
	if !strings.Contains(out, "HELD UNDER") {
		t.Fatalf("a claim under the path must not be reported as one that covers it: %q", out)
	}
	// It must NOT be counted as covering: the gate does not read it that way.
	if !strings.Contains(out, "CLAIMED BY   (none)") {
		t.Fatalf("a claim under the directory was reported as claiming the directory: %q", out)
	}
}

// THE DIRECTORY THAT DOES NOT EXIST YET, which is the natural state for a
// package somebody has just claimed IN ORDER TO CREATE IT.
//
// The first version of this feature took an asDir flag computed with os.Stat,
// so the held-under arm fired only when the directory existed locally. A peer
// claiming internal/newpkg/foo.go therefore read back as `CLAIMED BY (none)` —
// this command's own defect wearing the new feature's clothes (Fable review).
// A claim is declared intent and can name a path that exists nowhere yet.
func TestWhoseReportsAClaimUnderADirectoryThatDoesNotExistLocally(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)

	if _, errw, code := f.run(t, f.repo, "", "claim", "newpkg-work", "--session", "sess-a",
		"--desc", "creating it", "--scope", "internal/newpkg/foo.go"); code != 0 {
		t.Fatalf("claim: %s", errw)
	}
	// Deliberately NO MkdirAll. If this test ever grows one, it stops testing
	// the thing that broke.
	if _, err := os.Stat(filepath.Join(f.repo, "internal", "newpkg")); err == nil {
		t.Fatal("fixture precondition: internal/newpkg must not exist on disk")
	}

	out, _, code := f.run(t, f.repo, "", "whose", "internal/newpkg")
	if code != 0 {
		t.Fatal("whose failed")
	}
	if !strings.Contains(out, "newpkg-work") {
		t.Fatalf("a claim under a not-yet-created directory was invisible — "+
			"the exact silence this feature exists to end: %q", out)
	}

	// POSITIVE CONTROL: creating the directory must not CHANGE the answer.
	// Under the old shape it did, which is how the defect stayed hidden.
	if err := os.MkdirAll(filepath.Join(f.repo, "internal", "newpkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	after, _, code := f.run(t, f.repo, "", "whose", "internal/newpkg")
	if code != 0 {
		t.Fatal("whose failed after mkdir")
	}
	if !strings.Contains(after, "newpkg-work") {
		t.Fatalf("claim vanished once the directory existed: %q", after)
	}
}

// BOTH registers at once, which is the common case and the one a mutation
// slipped through: the claim register printed only when there were no dirty
// rows would satisfy every other test here, because they all ask about a path
// nobody has edited yet.
func TestWhoseReportsBothRegistersWhenAPathIsClaimedAndDirty(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.twoSessionsIn(t, f.repo)
	f.edit(t, f.repo, "sess-a", "CHANGELOG.md", "## 0.1.20\n- a bullet\n")

	if _, errw, code := f.run(t, f.repo, "", "claim", "changelog", "--session", "sess-a",
		"--desc", "x", "--scope", "CHANGELOG.md"); code != 0 {
		t.Fatalf("claim: %s", errw)
	}

	out, errw, code := f.run(t, f.repo, "", "whose", "CHANGELOG.md")
	if code != 0 {
		t.Fatalf("whose: %s", errw)
	}
	ci := strings.Index(out, "CLAIMED BY")
	di := strings.Index(out, "DIRTY IN")
	if ci < 0 {
		t.Fatalf("the claim register vanished when the path was also dirty: %q", out)
	}
	if di < 0 {
		t.Fatalf("the dirty register vanished: %q", out)
	}
	if ci > di {
		t.Fatalf("the claim register must come first — it is the one that reserves: %q", out)
	}
	if !strings.Contains(out, "changelog") || !strings.Contains(out, "alpha") {
		t.Fatalf("whose lost the slug or the holder: %q", out)
	}
}

// `whose` is now the FIRST place a session looks to decide whether it may
// write, so a bare "STALE" beside a claim is exactly the word a blocked
// session could read as permission to take the scope. D-022 records that the
// refusal and the dry run must say it STILL REFUSES; this is the third place
// that sentence has to appear, and the one the incident was actually about.
func TestWhoseSaysAStaleClaimStillRefuses(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "claim", "api-work", "--session", "sess-a",
		"--desc", "x", "--scope", "internal/api"); code != 0 {
		t.Fatalf("claim: %s", errw)
	}

	// CONTROL: a fresh holder carries no STALE word at all.
	fresh, _, _ := f.run(t, f.repo, "", "whose", "internal/api/server.go")
	if strings.Contains(fresh, "STALE") {
		t.Fatalf("a fresh claim was marked STALE: %q", fresh)
	}

	f.clock = f.clock.Add(store.StaleAfter + time.Minute)

	out, _, code := f.run(t, f.repo, "", "whose", "internal/api/server.go")
	if code != 0 {
		t.Fatal("whose failed")
	}
	if !strings.Contains(out, "STALE") {
		t.Fatalf("a stale claim was not marked: %q", out)
	}
	if !strings.Contains(out, "still refuses") {
		t.Fatalf("STALE without the qualifier reads as permission to take the scope: %q", out)
	}
}
