package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// D-042 (issue #33, wishlist §7): a playbook every lane appends to was held
// mid-item by one session, twice, and two rules shipped with no home. A SHARED
// claim may overlap other shared claims and nothing else.

// TestSharedConflictMatrix is the one rule, through BOTH doors that compute it
// (ClaimMode and its forecast), so a dry run cannot disagree with the refusal.
func TestSharedConflictMatrix(t *testing.T) {
	cases := []struct {
		name               string
		held, req          string
		heldShared, shared bool
		refused            bool
	}{
		{"exclusive vs exclusive", "docs/p.md", "docs/p.md", false, false, true},
		{"exclusive held, shared request", "docs/p.md", "docs/p.md", false, true, true},
		{"shared held, exclusive request", "docs/p.md", "docs/p.md", true, false, true},
		{"shared vs shared", "docs/p.md", "docs/p.md", true, true, false},
		{"shared parent vs shared child", "docs", "docs/p.md", true, true, false},
		{"shared parent vs exclusive child", "docs", "docs/p.md", true, false, true},
		{"folded spelling, shared vs shared", "docs/p.md", "Docs/P.md", true, true, false},
		{"no overlap, exclusive", "docs/p.md", "src", false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := openTest(t)
			a := hello(t, st, "sess-a", "alpha", "/wt/a")
			b := hello(t, st, "sess-b", "bravo", "/wt/b")
			if err := st.ClaimMode(a.SessionID, a.Incarnation, "held", "h", []string{tc.held}, tc.heldShared); err != nil {
				t.Fatal(err)
			}
			_, conflicts, _, err := st.ClaimConflictsMode(b.SessionID, b.Incarnation, "req", []string{tc.req}, tc.shared)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(conflicts) > 0; got != tc.refused {
				t.Fatalf("forecast refused=%v, want %v: %+v", got, tc.refused, conflicts)
			}
			err = st.ClaimMode(b.SessionID, b.Incarnation, "req", "r", []string{tc.req}, tc.shared)
			var refused ErrRefused
			if got := errors.As(err, &refused); got != tc.refused {
				t.Fatalf("claim refused=%v, want %v (err %v)", got, tc.refused, err)
			}
			// The refusal carries the HOLDER's mode, so the CLI can say that
			// --shared would clear it.
			if tc.refused && refused.Shared != tc.heldShared {
				t.Fatalf("refusal Shared=%v, want the holder's mode %v", refused.Shared, tc.heldShared)
			}
		})
	}
}

// A slug is one holder whatever the modes: two shared requests for ONE slug
// still collide, and the conflict must not say --shared would clear it.
func TestSharedDoesNotShareASlug(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.ClaimMode(a.SessionID, a.Incarnation, "rules", "r", []string{"rules.md"}, true); err != nil {
		t.Fatal(err)
	}
	err := st.ClaimMode(b.SessionID, b.Incarnation, "rules", "r", []string{"rules.md"}, true)
	var refused ErrRefused
	if !errors.As(err, &refused) || refused.Scope != "" {
		t.Fatalf("a held slug must refuse a shared request for the same slug: %v", err)
	}
	if refused.Shared {
		t.Fatal("a slug conflict must not read as one --shared would clear")
	}
	// Positive control: a distinct slug over the same scope is admitted.
	if err := st.ClaimMode(b.SessionID, b.Incarnation, "rules-b", "r", []string{"rules.md"}, true); err != nil {
		t.Fatalf("control: a distinct slug, shared, must coexist: %v", err)
	}
}

// TestOwnerOfUnderSharedClaims is the gate's reading: a shared hold is an
// invitation to claim alongside, not an open door.
func TestOwnerOfUnderSharedClaims(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.ClaimMode(a.SessionID, a.Incarnation, "playbook-a", "a", []string{"docs"}, true); err != nil {
		t.Fatal(err)
	}
	// B has no claim: held, and the holder is reported shared.
	c, held, err := st.OwnerOf("docs/playbook.md", b.SessionID)
	if err != nil || !held || !c.Shared || c.Slug != "playbook-a" {
		t.Fatalf("an unjoined session must be refused by a shared hold: held=%v %+v %v", held, c, err)
	}
	// B joins on a path that does NOT cover the one it edits: still held.
	if err := st.ClaimMode(b.SessionID, b.Incarnation, "playbook-b", "b", []string{"docs/playbook.md"}, true); err != nil {
		t.Fatal(err)
	}
	if _, held, _ := st.OwnerOf("docs/other.md", b.SessionID); !held {
		t.Fatal("a claim on docs/playbook.md must not admit an edit to docs/other.md")
	}
	if _, held, _ := st.OwnerOf("docs", b.SessionID); !held {
		t.Fatal("a claim on a child must not admit an edit to its parent")
	}
	// The covered path is free for B, and A's own path is free for A.
	if _, held, _ := st.OwnerOf("docs/playbook.md", b.SessionID); held {
		t.Fatal("a session holding a covering shared claim must be admitted")
	}
	if _, held, _ := st.OwnerOf("docs/playbook.md", a.SessionID); held {
		t.Fatal("the other shared holder must be admitted too")
	}
	// An unknown caller (the commit gate with no identity) owns nothing.
	if _, held, _ := st.OwnerOf("docs/playbook.md", ""); !held {
		t.Fatal("an unidentified caller must see the shared hold")
	}
}

// A shared hold must never hide an exclusive blocker. Across sessions the
// conflict scan makes the two unable to coexist, so the state is planted: the
// gate is the last line, and it must not depend on the scan being the only
// writer.
func TestOwnerOfPrefersAnExclusiveBlocker(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	c := hello(t, st, "sess-c", "charlie", "/wt/c")
	if err := st.ClaimMode(a.SessionID, a.Incarnation, "shared-a", "a", []string{"docs/p.md"}, true); err != nil {
		t.Fatal(err)
	}
	if err := st.ClaimMode(b.SessionID, b.Incarnation, "shared-b", "b", []string{"docs/p.md"}, true); err != nil {
		t.Fatal(err)
	}
	// Positive control: B, a shared holder, is admitted before the plant.
	if _, held, _ := st.OwnerOf("docs/p.md", b.SessionID); held {
		t.Fatal("control: B holds a covering shared claim and must be admitted")
	}
	plant(t, st, c.SessionID, c.Incarnation, "excl-c", "docs", false)
	got, held, err := st.OwnerOf("docs/p.md", b.SessionID)
	if err != nil || !held || got.Shared || got.Slug != "excl-c" {
		t.Fatalf("an exclusive blocker must be returned whatever else covers the path: held=%v %+v %v", held, got, err)
	}
	// And to a caller holding NOTHING, where both kinds of holder qualify:
	// the deny must name the exclusive one, or it offers --shared as the
	// way through a path --shared cannot open.
	d := hello(t, st, "sess-d", "delta", "/wt/d")
	got, held, err = st.OwnerOf("docs/p.md", d.SessionID)
	if err != nil || !held || got.Shared || got.Slug != "excl-c" {
		t.Fatalf("an unjoined caller must be shown the exclusive blocker, not a shared holder: held=%v %+v %v", held, got, err)
	}
}

// plant writes an open claim row directly, past the conflict scan.
func plant(t *testing.T, st *Store, session, incarnation, slug, scope string, shared bool) {
	t.Helper()
	id := newToken()
	now := st.now().Unix()
	if _, err := st.db.Exec(`INSERT INTO claims (claim_id, session_id, incarnation, slug, descr, created, renewed, state, shared)
		VALUES (?,?,?,?,?,?,?,'open',?)`, id, session, incarnation, slug, "planted", now, now, shared); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`INSERT INTO claim_scopes (claim_id, scope, folded) VALUES (?,?,?)`, id, scope, fold(scope)); err != nil {
		t.Fatal(err)
	}
}

// A refresh takes the mode it is given: re-claiming a shared slug without
// --shared makes it exclusive, which a peer sharing the path refuses.
func TestSharedRefreshChangesMode(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	claim := func(si SessionInfo, slug string, shared bool) error {
		return st.ClaimMode(si.SessionID, si.Incarnation, slug, "d", []string{"docs/p.md"}, shared)
	}
	if err := claim(a, "pa", true); err != nil {
		t.Fatal(err)
	}
	if err := claim(b, "pb", true); err != nil {
		t.Fatal(err)
	}
	var refused ErrRefused
	if err := claim(a, "pa", false); !errors.As(err, &refused) || !refused.Shared {
		t.Fatalf("an exclusive refresh must be refused by a peer sharing the path: %v", err)
	}
	if err := claim(a, "pa", true); err != nil {
		t.Fatalf("control: the shared refresh is admitted: %v", err)
	}
	if err := st.Release(b.SessionID, b.Incarnation, "pb"); err != nil {
		t.Fatal(err)
	}
	if err := claim(a, "pa", false); err != nil {
		t.Fatalf("alone on the path, the refresh to exclusive is admitted: %v", err)
	}
	cs, err := st.Claims(false)
	if err != nil || len(cs) != 1 || cs[0].Shared {
		t.Fatalf("the refresh must write the mode: %+v %v", cs, err)
	}
	// And the exclusive claim now refuses a shared newcomer.
	if err := claim(b, "pb2", true); !errors.As(err, &refused) {
		t.Fatalf("an exclusive claim must refuse a shared request: %v", err)
	}
}

// A slot is capacity 1 (D-035); a shared claim that reaches one is refused
// before anything is read, by containment in EITHER direction.
func TestSharedRefusedOnASlot(t *testing.T) {
	cases := []struct {
		scope   string
		refused bool
	}{
		{".buddy/slot/box", true},
		{".BUDDY/Slot/box", true},
		{".buddy/slot", true},
		{".buddy", true}, // an ancestor covers every slot without lying under the prefix
		{".buddy/slots/box", false},
		{".buddy/notes", false},
	}
	for _, tc := range cases {
		t.Run(tc.scope, func(t *testing.T) {
			st, _ := openTest(t)
			a := hello(t, st, "sess-a", "alpha", "/wt/a")
			err := st.ClaimMode(a.SessionID, a.Incarnation, "s", "d", []string{"docs", tc.scope}, true)
			if got := err != nil && strings.Contains(err.Error(), "capacity 1"); got != tc.refused {
				t.Fatalf("shared claim on %q: refused=%v, want %v (%v)", tc.scope, got, tc.refused, err)
			}
			_, _, _, ferr := st.ClaimConflictsMode(a.SessionID, a.Incarnation, "s2", []string{tc.scope}, true)
			if got := ferr != nil; got != tc.refused {
				t.Fatalf("the forecast must refuse what the claim refuses: %v", ferr)
			}
			// Control: the same scope, exclusive, is an ordinary claim.
			if err := st.ClaimMode(a.SessionID, a.Incarnation, "x", "d", []string{tc.scope}, false); err != nil {
				t.Fatalf("control: exclusive on %q must be admitted: %v", tc.scope, err)
			}
		})
	}
}

// A ledger from before the column migrates up with every claim exclusive,
// which is what every claim it holds was taken as.
func TestSharedColumnMigratesFromV10(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.db")
	st, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.Claim(a.SessionID, a.Incarnation, "old", "o", []string{"docs"}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE claims DROP COLUMN shared`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 10`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	st, err = Open(path, nil)
	if err != nil {
		t.Fatalf("a v10 ledger must migrate: %v", err)
	}
	defer st.Close()
	cs, err := st.Claims(false)
	if err != nil || len(cs) != 1 || cs[0].Shared {
		t.Fatalf("the old claim must read exclusive: %+v %v", cs, err)
	}
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.ClaimMode(b.SessionID, b.Incarnation, "new", "n", []string{"docs/p.md"}, true); err == nil {
		t.Fatal("the migrated exclusive claim must refuse a shared request")
	}
}
