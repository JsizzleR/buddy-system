package store

import (
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// The fold against what a case-insensitive APFS volume calls one file (issue
// #122, D-222). Every pair below was measured on the default macOS volume: the
// volume's mkdir refused the second spelling as existing, and the two names
// shared an inode. The old fold, ToLower(NFC(s)), split each one, so a claim on
// one spelling did not guard an edit under the other: `ſrc/a.go` beside a claim
// on `src` was allowed, and `claim --scope ſrc` was granted beside it.
//
// Only code points Unicode 15 assigns: go.mod's toolchain line named go1.26.8,
// whose tables are Unicode 15, until D-222 moved it to go1.27.1 (Unicode 17);
// a build under an older GOTOOLCHAIN would still split a later pair, and these
// rows hold under both.
var apfsAliases = []struct{ name, a, b string }{
	{"long s", "src", "\u017frc"},
	{"sharp s", "strasse", "stra\u00dfe"},
	{"capital sharp s", "strasse", "stra\u1e9ee"},
	{"final sigma", "\u03c3", "\u03c2"},
	{"ligature fi", "file", "\ufb01le"},
	{"ligature ff", "off", "o\ufb00"},
	{"ligature ffi", "ffi", "\ufb03"},
	{"ligature long st", "st", "\ufb05"},
	{"ligature st", "st", "\ufb06"},
	{"n preceded by apostrophe", "\u02bcn", "\u0149"},
	{"a right half ring", "a\u02be", "\u1e9a"},
	{"micro sign", "\u03bc", "\u00b5"},
	{"Greek beta symbol", "\u03b2", "\u03d0"},
	{"Greek theta symbol", "\u03b8", "\u03d1"},
	{"Greek lunate epsilon", "\u03b5", "\u03f5"},
	{"Greek rho symbol", "\u03c1", "\u03f1"},
	{"Greek pi symbol", "\u03c0", "\u03d6"},
	{"Greek kappa symbol", "\u03ba", "\u03f0"},
	{"Greek phi symbol", "\u03c6", "\u03d5"},
	{"ypogegrammeni", "\u03b9", "\u0345"},
	{"alpha with ypogegrammeni", "\u03b1\u03b9", "\u1fb3"},
	{"long s dot above", "\u1e61", "\u1e9b"},
	{"Cyrillic rounded ve", "\u0432", "\u1c80"},
	{"capital I dot above", "i\u0307", "\u0130"},
	// Already merged by the old fold; here so a key that lost them fails.
	{"upper case", "src", "SRC"},
	{"NFD", "caf\u00e9", "cafe\u0301"},
	// Canonically equivalent orders of marks with ypogegrammeni (ccc 240 after
	// the acute's 230): the case fold turns U+0345 into a base letter \u03b9, so a key
	// that folds before it decomposes keeps each order's own and splits them.
	{"ypogegrammeni reordered", "\u03b1\u0301\u0345", "\u03b1\u0345\u0301"},
	{"alpha acute ypogegrammeni", "\u1fb4", "\u1fb3\u0301"},
	{"Kelvin sign", "k", "\u212a"},
	{"Angstrom sign", "\u00e5", "\u212b"},
	{"Ohm sign", "\u03c9", "\u2126"},
	{"dz digraph title case", "\u01c6", "\u01c5"},
	{"Cherokee", "\uab70", "\u13a0"},
	{"Deseret", "\U00010428", "\U00010400"},
}

// Distinct on the volume (measured) and under the fold. Each would be a false
// refusal if merged; the I-dot-above row is the one pair the old fold merged
// (Go's ToLower maps U+0130 to a plain i) that the volume keeps apart.
var apfsDistinct = []struct{ name, a, b string }{
	{"dotless i", "i", "\u0131"},
	{"capital I dot above vs i", "i", "\u0130"},
	{"capital I dot above vs I", "I", "\u0130"},
	{"full-width", "src", "\uff53\uff52\uff43"},
	{"zero-width space", "s", "s\u200b"},
	{"soft hyphen", "s", "s\u00ad"},
	{"combining grapheme joiner", "x", "x\u034f"},
	{"byte order mark", "x", "x\ufeff"},
	{"combining dot", "s", "s\u0307"},
}

func TestFoldKeysEveryMeasuredAPFSAliasTogether(t *testing.T) {
	for _, tc := range apfsAliases {
		if fold(tc.a) != fold(tc.b) {
			t.Errorf("%s: fold(%+q)=%+q, fold(%+q)=%+q; the volume calls them one file", tc.name, tc.a, fold(tc.a), tc.b, fold(tc.b))
		}
		// Inside a path, at either end: the key is per component.
		pa, pb := "docs/"+tc.a+"/x.go", "docs/"+tc.b+"/x.go"
		if !scopeCovers(fold("docs/"+tc.a), fold(pb)) || !scopeCovers(fold("docs/"+tc.b), fold(pa)) {
			t.Errorf("%s: a scope in one spelling must cover a path in the other", tc.name)
		}
	}
	for _, tc := range apfsDistinct {
		if fold(tc.a) == fold(tc.b) {
			t.Errorf("%s: fold merges %+q and %+q, which the volume keeps apart", tc.name, tc.a, tc.b)
		}
	}
}

// legacyFold is the rule before D-222, which the folded columns still hold.
// It is also the double-fold control: the gate folds a path twice
// (placeCanonical, then OwnerOf), and a key that is not idempotent misses the
// IDENTICAL spelling. ToLower after NFC re-composes: H + U+0331 lowers to
// h + U+0331, which NFC then composes to U+1E96, so a claim on `H̱` did not
// guard an edit of `H̱`.
func TestFoldIsIdempotent(t *testing.T) {
	// Every rune alone, every plane.
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue
		}
		s := string(r)
		if k := fold(s); fold(k) != k {
			t.Fatalf("fold(fold(%+q))=%+q, fold(%+q)=%+q", s, fold(k), s, k)
		}
	}
	// The two-rune inputs that broke the measured alternatives: the old fold's
	// re-composition (124 inputs under go1.27.1, these among them), and an
	// full fold composed by NFC, whose x/text composition truncates code points to
	// 16 bits, so U+13C2 composed with U+113C8 under go1.27's tables.
	for _, s := range []string{
		"H\u0331", "J\u030c", "T\u0308", "T\u0344", "W\u030a", "Y\u030a", "\u0124\u0331", "\u0130\u0340",
		"\u13c2\U000113b8", "\u13c2\U000113c2", "\u13c2\U000113c5", "\u13c2\U000113c7", "\u13c2\U000113c8", "\u13c2\U000113c9",
	} {
		if k := fold(s); fold(k) != k {
			t.Errorf("fold(fold(%+q))=%+q, fold(%+q)=%+q", s, fold(k), s, k)
		}
		if fold(s) != fold(legacyFold(s)) && s == "H\u0331" {
			t.Errorf("H+U+0331 and its old key %+q must share a key", legacyFold(s))
		}
	}
	// A fixed-seed sample of longer names from case-changing runes and marks.
	pool := []rune("aAsSiIkK\u00df\u1e9e\u017f\u03c2\u03c3\u03a3\u0130\u0131\u212a\u01c5\ufb01\ufb03\u0149\u01f0\u0390\u1fb3\u1fbc\u0345\u00b5\u13a0\uab70\u1c80\u1e9b\u0301\u0307\u030c\u0308\u0331\u0342\u0313\u00e9\u13c2\U000113c8")
	rng := rand.New(rand.NewSource(122))
	for i := 0; i < 50000; i++ {
		rs := make([]rune, 1+rng.Intn(5))
		for j := range rs {
			rs[j] = pool[rng.Intn(len(pool))]
		}
		s := string(rs)
		if k := fold(s); fold(k) != k {
			t.Fatalf("fold(fold(%+q))=%+q, fold(%+q)=%+q", s, fold(k), s, k)
		}
	}
}

// The ASCII path skips the Unicode work; it must be the same function.
func TestFoldASCIIPathIsTheUnicodePath(t *testing.T) {
	check := func(s string) {
		if got, want := fold(s), foldUnicode(s); got != want {
			t.Fatalf("fold(%q)=%q, the Unicode path gives %q", s, got, want)
		}
	}
	for b := 0; b < utf8.RuneSelf; b++ {
		check(string(rune(b)))
		check("Dir/" + string(rune(b)) + "/x.GO")
	}
	rng := rand.New(rand.NewSource(222))
	for i := 0; i < 20000; i++ {
		buf := make([]byte, rng.Intn(40))
		for j := range buf {
			buf[j] = byte(rng.Intn(utf8.RuneSelf))
		}
		check(string(buf))
	}
	// And the boundary: one non-ASCII byte anywhere takes the Unicode path.
	if fold("SRC/\u017f") != "src/s" || fold("\u017fRC") != "src" {
		t.Fatalf("a non-ASCII byte must reach the full fold: %q %q", fold("SRC/\u017f"), fold("\u017fRC"))
	}
}

// The table measured on the volume these tests run on. On a case-insensitive
// volume every alias pair must name one file and every distinct pair two; on a
// case-sensitive one there is nothing to measure, and the leg says so rather
// than pass on nothing.
func TestFoldTableHoldsOnThisVolume(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "A")); err != nil {
		t.Skipf("the volume under %s is case-sensitive; nothing to measure the fold against", dir)
	}
	for i, tc := range apfsAliases {
		d := filepath.Join(dir, "alias", strings.Repeat("x", 1+i%3)+string(rune('a'+i%26))+string(rune('0'+i/26)))
		if err := os.MkdirAll(filepath.Join(d, tc.a), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(d, tc.b)); err != nil {
			t.Errorf("%s: this volume keeps %+q and %+q apart (%v); the table is wrong here", tc.name, tc.a, tc.b, err)
		}
	}
	for i, tc := range apfsDistinct {
		d := filepath.Join(dir, "distinct", string(rune('a'+i)))
		if err := os.MkdirAll(filepath.Join(d, tc.a), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(d, tc.b)); err == nil {
			t.Errorf("%s: this volume calls %+q and %+q one file; the table is wrong here", tc.name, tc.a, tc.b)
		}
	}
}

// Every comparison keys the stored SPELLING, never the stored folded column
// (D-222). That column keeps the old key for binaries before D-222, so a row
// written by either binary is judged by today's rule. The test plants a row
// whose folded column says something else entirely: a reader that trusted the
// column would find nothing.
func TestComparisonsKeyTheSpellingNotTheFoldedColumn(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.Claim(a.SessionID, a.Incarnation, "held", "x", []string{"\u017frc", "docs/stra\u00dfe"}); err != nil {
		t.Fatal(err)
	}
	// What the column holds: the old key, for older binaries.
	got := map[string]string{}
	rows, err := st.db.Query(`SELECT scope, folded FROM claim_scopes`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var sc, f string
		if err := rows.Scan(&sc, &f); err != nil {
			t.Fatal(err)
		}
		got[sc] = f
	}
	rows.Close()
	if got["\u017frc"] != "\u017frc" || got["docs/stra\u00dfe"] != "docs/stra\u00dfe" {
		t.Fatalf("the folded column must keep the pre-D-222 key for older binaries, got %q", got)
	}

	check := func(t *testing.T) {
		t.Helper()
		for _, p := range []string{"src/a.go", "\u017frc/a.go", "SRC/a.go", "docs/strasse/plan.md", "docs/stra\u00dfe/plan.md"} {
			if _, held, err := st.OwnerOf(p, b.SessionID); err != nil || !held {
				t.Errorf("OwnerOf(%+q): held=%v err=%v; alpha's claim covers it on this volume", p, held, err)
			}
		}
		// Positive control: the scan answers "free" for a path nothing covers.
		if _, held, err := st.OwnerOf("docs/other.md", b.SessionID); err != nil || held {
			t.Errorf("OwnerOf(docs/other.md): held=%v err=%v; nothing claims it", held, err)
		}
		for _, sc := range []string{"src", "SRC/x", "docs/strasse", "docs"} {
			err := st.Claim(b.SessionID, b.Incarnation, "dup", "x", []string{sc})
			var refused ErrRefused
			if !errors.As(err, &refused) {
				t.Errorf("claim %+q beside alpha's: want ErrRefused, got %v", sc, err)
			}
		}
		if _, cs, _, err := st.ClaimConflicts(b.SessionID, b.Incarnation, "dup", []string{"src"}); err != nil || len(cs) == 0 {
			t.Errorf("the forecast must name the conflict too: %v %v", cs, err)
		}
	}
	t.Run("as written", check)

	// Now corrupt every folded value. Nothing that compares may notice.
	if _, err := st.db.Exec(`UPDATE claim_scopes SET folded='zz-not-a-key/' || rowid`); err != nil {
		t.Fatal(err)
	}
	t.Run("folded column ignored", check)

	// Beat renews by the spelling too.
	if err := st.Beat(a.SessionID, "src/x.go"); err != nil {
		t.Fatal(err)
	}
	// Release narrowing matches the held row by its spelling, in any spelling
	// the fold calls the same, and deletes that row whatever its column holds.
	rem, err := st.ReleaseScopes(a.SessionID, a.Incarnation, "held", []string{"SRC"})
	if err != nil {
		t.Fatalf("release --scope SRC of the held \u017frc: %v", err)
	}
	if len(rem) != 1 || rem[0] != "docs/stra\u00dfe" {
		t.Fatalf("remaining = %q, want only docs/stra\u00dfe", rem)
	}
	if err := st.Claim(b.SessionID, b.Incarnation, "freed", "x", []string{"src"}); err != nil {
		t.Fatalf("src was released and must be claimable: %v", err)
	}
	if err := st.Claim(b.SessionID, b.Incarnation, "still", "x", []string{"docs/strasse"}); err == nil {
		t.Fatal("docs/stra\u00dfe is still held and must refuse docs/strasse")
	}
}

// On a case-SENSITIVE volume one claim can hold two spellings the fold calls
// one key (`Foo` and `foo`, now also `strasse` and `straße`), and they are two
// files there. Release narrowing by one spelling must not drop the other with
// it: that released a file nobody asked to release, and a peer could then
// take it (panel lens c). The exact spelling wins; the fold is the fallback.
func TestReleaseScopeDropsOnlyTheNamedSpellingWhenItIsHeld(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.Claim(a.SessionID, a.Incarnation, "w", "x", []string{"strasse", "stra\u00dfe", "Foo", "foo", "docs"}); err != nil {
		t.Fatal(err)
	}
	rem, err := st.ReleaseScopes(a.SessionID, a.Incarnation, "w", []string{"stra\u00dfe", "Foo"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"strasse": true, "foo": true, "docs": true}
	if len(rem) != len(want) {
		t.Fatalf("remaining = %q, want %v", rem, want)
	}
	for _, r := range rem {
		if !want[r] {
			t.Fatalf("remaining = %q, want %v", rem, want)
		}
	}
	var refused ErrRefused
	if err := st.Claim(b.SessionID, b.Incarnation, "peer", "x", []string{"strasse"}); !errors.As(err, &refused) {
		t.Fatalf("strasse is still held and must refuse a peer with ErrRefused, got %v", err)
	}
	// Positive control: the peer's claim door is open for what nobody holds.
	if err := st.Claim(b.SessionID, b.Incarnation, "peer-ok", "x", []string{"elsewhere"}); err != nil {
		t.Fatalf("a disjoint claim by the peer must be granted: %v", err)
	}
	// Positive control for the fallback: a spelling no row holds verbatim
	// still releases the row the fold matches.
	rem, err = st.ReleaseScopes(a.SessionID, a.Incarnation, "w", []string{"STRASSE"})
	if err != nil || len(rem) != 2 {
		t.Fatalf("release --scope STRASSE: remaining %q, err %v", rem, err)
	}
}

// The authority list keys its rows by the spelling too, so a row written under
// the old key (or by an older binary) can still be removed and is not added
// twice.
func TestAuthorityListKeysTheSpelling(t *testing.T) {
	st, _ := openTest(t)
	if err := st.AuthorityAdd("docs/\u017ftyle.md"); err != nil {
		t.Fatal(err)
	}
	var col string
	if err := st.db.QueryRow(`SELECT folded FROM authority`).Scan(&col); err != nil || col != "docs/\u017ftyle.md" {
		t.Fatalf("the folded column must keep the pre-D-222 key for older binaries, got %q %v", col, err)
	}
	if _, err := st.db.Exec(`UPDATE authority SET folded='zz-not-a-key'`); err != nil {
		t.Fatal(err)
	}
	if err := st.AuthorityAdd("docs/STYLE.md"); err != nil {
		t.Fatal(err)
	}
	if ps, _ := st.AuthorityPaths(); len(ps) != 1 {
		t.Fatalf("one file, one row: %q", ps)
	}
	if ok, err := st.AuthorityRemove("docs/other.md"); err != nil || ok {
		t.Fatalf("removing a path not listed: ok=%v err=%v", ok, err)
	}
	if ok, err := st.AuthorityRemove("docs/style.md"); err != nil || !ok {
		t.Fatalf("removing the listed file by another spelling: ok=%v err=%v", ok, err)
	}
	if ps, _ := st.AuthorityPaths(); len(ps) != 0 {
		t.Fatalf("list not empty: %q", ps)
	}
	// U+0130 and i are two files and two keys, but one old key, which the
	// column's uniqueness holds: refused, naming the entry, never a silent
	// no-op that reports success. The control: alone, it is listed.
	if err := st.AuthorityAdd("i.md"); err != nil {
		t.Fatal(err)
	}
	if err := st.AuthorityAdd("\u0130.md"); err == nil || !strings.Contains(err.Error(), "beside i.md") {
		t.Fatalf("adding \u0130.md beside i.md: want a refusal naming i.md, got %v", err)
	}
	if ok, err := st.AuthorityRemove("i.md"); err != nil || !ok {
		t.Fatalf("rm i.md: ok=%v err=%v", ok, err)
	}
	if err := st.AuthorityAdd("\u0130.md"); err != nil {
		t.Fatalf("\u0130.md alone must be listed: %v", err)
	}
	if ps, _ := st.AuthorityPaths(); len(ps) != 1 || ps[0] != "\u0130.md" {
		t.Fatalf("list = %q, want only \u0130.md", ps)
	}
}

// The remaining doors that read claim_scopes, each asked with every folded
// value corrupted: Beat's and a logical session's renewal, the forecast's
// ended-holder scan, and the run slot (holdsRunSlot and the runOpenSQL every
// claim read carries). Each must still answer by the spelling (D-222).
func TestEveryClaimReadKeysTheSpelling(t *testing.T) {
	corrupt := func(t *testing.T, st *Store) {
		t.Helper()
		if _, err := st.db.Exec(`UPDATE claim_scopes SET folded='zz-not-a-key/' || rowid`); err != nil {
			t.Fatal(err)
		}
	}
	claimID := func(t *testing.T, st *Store, slug string) string {
		t.Helper()
		var id string
		if err := st.db.QueryRow(`SELECT claim_id FROM claims WHERE slug=? AND state='open'`, slug).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	t.Run("beat renews", func(t *testing.T) {
		st, clk := openTest(t)
		a := hello(t, st, "sess-a", "alpha", "/wt/a")
		if err := st.Claim(a.SessionID, a.Incarnation, "held", "x", []string{"ſrc"}); err != nil {
			t.Fatal(err)
		}
		// The control: a claim the path is outside, which must not renew.
		if err := st.Claim(a.SessionID, a.Incarnation, "other", "x", []string{"docs"}); err != nil {
			t.Fatal(err)
		}
		corrupt(t, st)
		clk.advance(20 * time.Minute)
		if err := st.Beat(a.SessionID, "SRC/x.go"); err != nil {
			t.Fatal(err)
		}
		cs, _ := st.Claims(false)
		if len(cs) != 2 {
			t.Fatalf("want 2 claims: %+v", cs)
		}
		for _, c := range cs {
			if renewed := c.Renewed.Equal(clk.now()); renewed != (c.Slug == "held") {
				t.Fatalf("a beat on SRC/x.go renews the claim on ſrc and only it: %s renewed=%v", c.Slug, renewed)
			}
		}
	})
	t.Run("logical renewal", func(t *testing.T) {
		st, _ := openTest(t)
		si := beginLogical(t, st, "logical", "")
		if err := st.Claim(si.SessionID, si.Incarnation, "held", "", []string{"straße"}); err != nil {
			t.Fatal(err)
		}
		// The control: a second claim the path is outside, so n counts only
		// the covering one.
		if err := st.Claim(si.SessionID, si.Incarnation, "other", "", []string{"docs"}); err != nil {
			t.Fatal(err)
		}
		corrupt(t, st)
		if n, err := st.RenewLogical(si.SessionID, si.Incarnation, "", "STRASSE/a.go"); err != nil || n != 1 {
			t.Fatalf("renewal of STRASSE/a.go under a claim on straße: n=%d err=%v", n, err)
		}
	})
	t.Run("ended holder in the forecast", func(t *testing.T) {
		st, _ := openTest(t)
		a := hello(t, st, "sess-a", "alpha", "/wt/a")
		b := hello(t, st, "sess-b", "bravo", "/wt/b")
		if err := st.Claim(a.SessionID, a.Incarnation, "held", "x", []string{"ſrc"}); err != nil {
			t.Fatal(err)
		}
		if err := st.Bye(a.SessionID, a.Incarnation); err != nil {
			t.Fatal(err)
		}
		corrupt(t, st)
		_, _, displaced, err := st.ClaimConflicts(b.SessionID, b.Incarnation, "mine", []string{"src"})
		if err != nil || len(displaced) == 0 {
			t.Fatalf("the forecast must name the ended holder's ſrc over src: %+v %v", displaced, err)
		}
	})
	t.Run("run slot", func(t *testing.T) {
		st, _ := openTest(t)
		a := hello(t, st, "sess-a", "alpha", "/wt/a")
		if err := st.Claim(a.SessionID, a.Incarnation, "run1", "x", []string{SlotPrefix + "/RUN1"}); err != nil {
			t.Fatal(err)
		}
		if err := st.Claim(a.SessionID, a.Incarnation, "rider", "x", []string{"docs"}); err != nil {
			t.Fatal(err)
		}
		run, rider := claimID(t, st, "run1"), claimID(t, st, "rider")
		if _, err := st.db.Exec(`UPDATE claims SET in_run=? WHERE claim_id=?`, run, rider); err != nil {
			t.Fatal(err)
		}
		corrupt(t, st)
		if ok, err := holdsRunSlot(st.db, run, "run1"); err != nil || !ok {
			t.Fatalf("holdsRunSlot: %v %v", ok, err)
		}
		var open bool
		if err := st.db.QueryRow(`SELECT `+runOpenSQL+` FROM claims c WHERE c.claim_id=?`, rider).Scan(&open); err != nil || !open {
			t.Fatalf("runOpenSQL must read the rider's run open: %v %v", open, err)
		}
		// Control: a claim that is not the run reads closed.
		if ok, _ := holdsRunSlot(st.db, rider, "run1"); ok {
			t.Fatal("the rider does not hold the run's slot")
		}
	})
}
