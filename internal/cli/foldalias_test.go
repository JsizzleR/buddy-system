package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// Issue #122, D-222: spellings a case-insensitive APFS volume resolves to the
// same file, which the old fold (ToLower of NFC) keyed apart. A holds an
// exclusive claim on `src` and `docs/strasse`; B reaches the same files as
// `ſrc` (U+017F, long s) and `straße`. Each row went through the real gate,
// claim door and commit gate, and each was ALLOWED or GRANTED.

// TestGateRefusesAnAliasedSpellingOfAClaimedScope is the issue's Edit,
// apply_patch and claim rows, beside the controls the old fold already denied.
func TestGateRefusesAnAliasedSpellingOfAClaimedScope(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "held", "d", "src", "docs/strasse")

	edits := []struct{ name, rel string }{
		{"control: same spelling", "src/a.go"},
		{"control: upper case", "SRC/a.go"},
		{"long s", "\u017frc/a.go"},
		{"sharp s", "docs/stra\u00dfe/plan.md"},
		{"capital sharp s", "docs/STRA\u1e9eE/plan.md"},
	}
	for _, tc := range edits {
		out, errw, _ := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Edit", filepath.Join(f.wtB, tc.rel)), "gate")
		reason, denied := decodeDeny(t, out)
		if !denied || !strings.Contains(reason, `slug "held"`) {
			t.Errorf("Edit %+q inside alpha's claim: want a deny naming it, got out=%q stderr=%q", tc.rel, out, errw)
		}
	}
	// Positive control: the gate allows what nothing claims (with the
	// own-claim arm's line since D-228, an allow: not denied is the test).
	other, _, _ := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Edit", filepath.Join(f.wtB, "docs/other.md")), "gate")
	if _, denied := decodeDeny(t, other); denied {
		t.Fatalf("an unclaimed path must pass: %q", other)
	}

	for _, tc := range []struct{ name, body string }{
		{"control: update", "*** Update File: src/a.go\n@@\n-a\n+b\n"},
		{"update, long s", "*** Update File: \u017frc/a.go\n@@\n-a\n+b\n"},
		{"delete, long s", "*** Delete File: \u017frc/a.go\n"},
		{"add, sharp s", "*** Add File: docs/stra\u00dfe/new.md\n+x\n"},
	} {
		out, errw, _ := f.run(t, f.wtB, patchHookJSON("sess-b", f.wtB, patchText(tc.body)), "gate")
		if reason, denied := decodeDeny(t, out); !denied || !strings.Contains(reason, `slug "held"`) {
			t.Errorf("apply_patch %s: want a deny naming alpha's claim, got out=%q stderr=%q", tc.name, out, errw)
		}
	}
	// Positive control: the same patch shape on an unclaimed path passes, so a
	// deny above is the claim's and not the patch reader's.
	if out, errw, _ := f.run(t, f.wtB, patchHookJSON("sess-b", f.wtB, patchText("*** Update File: docs/other.md\n@@\n-a\n+b\n")), "gate"); strings.TrimSpace(out) != "" {
		t.Fatalf("apply_patch of an unclaimed path must pass: out=%q stderr=%q", out, errw)
	}

	for _, sc := range []string{"SRC", "\u017frc", "docs/stra\u00dfe", "\u017fRC/x.go"} {
		out, errw, code := f.run(t, f.wtB, "", "claim", "dup", "--session", "sess-b", "--desc", "d", "--scope", sc)
		if code != 4 {
			t.Errorf("claim --scope %+q beside alpha's exclusive claim: want exit 4, got %d: %s %s", sc, code, out, errw)
		}
	}
	if out, errw, code := f.run(t, f.wtB, "", "claim", "ok", "--session", "sess-b", "--desc", "d", "--scope", "docs/other.md"); code != 0 {
		t.Fatalf("control: a disjoint claim must be granted: %d %s %s", code, out, errw)
	}
}

// TestGateFoldsTwiceToTheSameKey: the gate folds a path in placeCanonical and
// again in OwnerOf, and the claim door folds a scope once. The old fold was not
// idempotent (ToLower then NFC composes h + U+0331 into U+1E96), so a claim on
// `H̱` did not guard an Edit of the identical spelling `H̱` (panel lens a). The
// third spelling is the one a full fold composed by NFC broke under go1.27's
// tables (its composition truncates code points to 16 bits).
func TestGateFoldsTwiceToTheSameKey(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	for i, sc := range []string{"H\u0331", "J\u030c", "\u13c2\U000113c8"} {
		f.claimFor(t, "sess-a", "c"+string(rune('0'+i)), "d", sc)
		slug := "c" + string(rune('0'+i))
		out, errw, _ := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Edit", filepath.Join(f.wtB, sc, "a.go")), "gate")
		if reason, denied := decodeDeny(t, out); !denied || !strings.Contains(reason, `slug "`+slug+`"`) {
			t.Errorf("Edit of %+q/a.go, the claimed spelling itself: want a deny naming %s, got out=%q stderr=%q", sc, slug, out, errw)
		}
		// Positive control: the holder itself edits it.
		if out, errw, _ := f.run(t, f.repo, hookJSON("sess-a", f.repo, "Edit", filepath.Join(f.repo, sc, "a.go")), "gate"); strings.TrimSpace(out) != "" {
			t.Errorf("the holder's own Edit of %+q/a.go must pass: out=%q stderr=%q", sc, out, errw)
		}
	}
}

// TestCommitGateReportsAnAliasedStagedPath: the commit gate asks the same
// store question about each staged path, so `ſrc/b.go` staged by B is reported
// against alpha's claim on `src`.
func TestCommitGateReportsAnAliasedStagedPath(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "held", "edge cap", "src")
	f.stage(t, f.wtB, "\u017frc/b.go")
	var errw string
	var code int
	f.asSession("sess-b", func() { _, errw, code = f.run(t, f.wtB, "", "commit-gate") })
	if code != commitGateAllow || !strings.Contains(errw, "held") || !strings.Contains(errw, "alpha") {
		t.Fatalf("a staged path under an aliased spelling of alpha's claim must be reported (warn posture): exit %d\n%s", code, errw)
	}
	// Positive control: the same claim, a staged path outside it, nothing named.
	g := newFixture(t)
	g.initAndHello(t)
	g.claimFor(t, "sess-a", "held", "edge cap", "src")
	g.stage(t, g.wtB, "docs/b.go")
	g.asSession("sess-b", func() { _, errw, code = g.run(t, g.wtB, "", "commit-gate") })
	if code != commitGateAllow || strings.Contains(errw, "held") {
		t.Fatalf("a staged path outside alpha's claim must not be reported: exit %d\n%s", code, errw)
	}
}

// TestDottedCapitalIIsNotI is the one pair the old fold merged that the
// volume keeps apart: Go's ToLower maps U+0130 to a plain i, and APFS keys it
// as i + U+0307. Claims on each are two files and both are granted; a claim on
// i + U+0307, which IS the same file, is refused (the positive control).
func TestDottedCapitalIIsNotI(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "dotted", "d", "\u0130")
	if out, errw, code := f.run(t, f.wtB, "", "claim", "plain", "--session", "sess-b", "--desc", "d", "--scope", "i"); code != 0 {
		t.Fatalf("i and U+0130 are two files on APFS; want granted, got %d %s %s", code, out, errw)
	}
	if out, errw, code := f.run(t, f.wtB, "", "claim", "same", "--session", "sess-b", "--desc", "d", "--scope", "i\u0307"); code != 4 {
		t.Fatalf("i + U+0307 is U+0130's own file; want exit 4, got %d %s %s", code, out, errw)
	}
}

// TestChatPostureReadsOnlyAnASCIIWord: BUDDY_CHAT is a word, not a path, and
// install.sh reads it with an ASCII fold. `oﬀ` (o + U+FB00) folds to `off`
// under the full case fold, so hello would have turned chat off and update
// forecast "off" for a value install.sh refuses. A non-ASCII value is neither.
func TestChatPostureReadsOnlyAnASCIIWord(t *testing.T) {
	for _, tc := range []struct {
		val      string
		off, bad bool
		forecast string
	}{
		{"off", true, false, "off"},
		{" OFF ", true, false, "off"},
		{"On", false, false, "on"},
		{"o\ufb00", false, true, ""},
		{"\u00f6ff", false, true, ""},
		{"o\u0301n", false, true, ""},
		{"off\u00a0", false, true, ""},
		{"\u2003on", false, true, ""},
	} {
		env := Env{Getenv: func(k string) string {
			if k == EnvChat {
				return tc.val
			}
			return ""
		}, ChatStamp: func() string { return "" }}
		if got := chatOff(env); got != tc.off {
			t.Errorf("BUDDY_CHAT=%+q: chatOff=%v, want %v", tc.val, got, tc.off)
		}
		posture, bad := chatPosture(chatAsked(env), "")
		if bad != tc.bad || (!bad && posture != tc.forecast) {
			t.Errorf("BUDDY_CHAT=%+q: update forecasts %q bad=%v, want %q bad=%v", tc.val, posture, bad, tc.forecast, tc.bad)
		}
	}
}
