package store

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Issue #126. claimsWhere read claims and their scopes in two autocommit
// statements, each its own WAL snapshot, and OwnerOf picked a claim in one
// statement and loaded it in another; a peer committing in either gap tore the
// answer. Recovered reproduction: Claims() tore in 18 reads under one writer.
// These tests land the peer's write IN the gap, through a second Store handle on
// the same file (a separate connection pool, as a peer process would be), via
// the per-gap seams, so the tear is deterministic rather than timed.
//
// Every case asserts the WHOLE pre-write state (desc, state, owner, scopes), not
// just "held": a release between OwnerOf's scan and load loads a RELEASED row
// and still answers held=true (the panel measured it), so a `held` assertion
// alone passes on the torn code. Each case also asserts its seam fired exactly
// once (the guard was armed) and that a read after the write sees the write
// (the positive control: the peer's write really landed).

type snapFixture struct {
	st, peer            *Store
	alpha, bravo, charl SessionInfo
}

func newSnapFixture(t *testing.T) snapFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fleet.db")
	clk := &pinnedClock{t: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)}
	st, err := Open(path, clk.now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	peer, err := Open(path, clk.now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { peer.Close() })
	f := snapFixture{st: st, peer: peer,
		alpha: hello(t, st, "sess-a", "alpha", "/wt/a"),
		bravo: hello(t, st, "sess-b", "bravo", "/wt/b"),
		charl: hello(t, st, "sess-c", "charlie", "/wt/c"),
	}
	if err := st.Claim(f.alpha.SessionID, f.alpha.Incarnation, "x", "desc-a", []string{"internal/api"}); err != nil {
		t.Fatal(err)
	}
	return f
}

// The peer's writes. Each keeps the claim id (a refresh replaces scopes; a
// release closes; a hand-on changes the owner), which is what lets a torn read
// pair one moment's row with another's scopes.
var snapWrites = []struct {
	name  string
	write func(t *testing.T, f snapFixture)
}{
	{"refresh", func(t *testing.T, f snapFixture) {
		if err := f.peer.Claim(f.alpha.SessionID, f.alpha.Incarnation, "x", "desc-b", []string{"docs"}); err != nil {
			t.Fatal(err)
		}
	}},
	{"release", func(t *testing.T, f snapFixture) {
		if err := f.peer.Release(f.alpha.SessionID, f.alpha.Incarnation, "x"); err != nil {
			t.Fatal(err)
		}
	}},
	// Codex design pass: hand the claim on, and its new holder re-claims it
	// elsewhere. Statement one reads alpha as the owner, statement two the new
	// holder's scopes.
	{"hand-on then refresh", func(t *testing.T, f snapFixture) {
		if _, _, err := f.peer.TransferClaim(f.alpha.SessionID, f.alpha.Incarnation, "x", mustResolve(t, f.peer, "sess-c")); err != nil {
			t.Fatal(err)
		}
		if err := f.peer.Claim(f.charl.SessionID, f.charl.Incarnation, "x", "desc-c", []string{"docs"}); err != nil {
			t.Fatal(err)
		}
	}},
	// Hand-on to the READER: torn, OwnerOf named the caller as its own blocker.
	{"hand-on to the reader", func(t *testing.T, f snapFixture) {
		if _, _, err := f.peer.TransferClaim(f.alpha.SessionID, f.alpha.Incarnation, "x", mustResolve(t, f.peer, "sess-b")); err != nil {
			t.Fatal(err)
		}
	}},
}

// wantPreWrite is the one claim as it stood before every write above.
func wantPreWrite(t *testing.T, where string, c ClaimInfo) {
	t.Helper()
	if c.Slug != "x" || c.Desc != "desc-a" || c.State != "open" || c.Owner.SessionID != "sess-a" ||
		c.Owner.Label != "alpha" || c.Owner.Missing || !slices.Equal(c.Scopes, []string{"internal/api"}) {
		t.Fatalf("%s: torn read, want the whole pre-write claim (x, desc-a, open, alpha, [internal/api]), got slug=%q desc=%q state=%q owner=%q/%q missing=%v scopes=%q",
			where, c.Slug, c.Desc, c.State, c.Owner.SessionID, c.Owner.Label, c.Owner.Missing, c.Scopes)
	}
}

func TestClaimReadersSeeOneSnapshot(t *testing.T) {
	readers := []struct {
		name string
		arm  func(s *Store, fn func())
		read func(t *testing.T, f snapFixture) ClaimInfo
	}{
		{"Claims", func(s *Store, fn func()) { s.betweenClaimReads = fn }, func(t *testing.T, f snapFixture) ClaimInfo {
			cs, err := f.st.Claims(false)
			if err != nil || len(cs) != 1 {
				t.Fatalf("Claims: %v, %d claims", err, len(cs))
			}
			return cs[0]
		}},
		{"ClaimsTouching", func(s *Store, fn func()) { s.betweenClaimReads = fn }, func(t *testing.T, f snapFixture) ClaimInfo {
			ts, err := f.st.ClaimsTouching("internal/api/server.go")
			if err != nil || len(ts) != 1 || !ts[0].Covers {
				t.Fatalf("ClaimsTouching: %v, %+v", err, ts)
			}
			return ts[0].Claim
		}},
		{"OwnerOf", func(s *Store, fn func()) { s.betweenOwnerLoad = fn }, func(t *testing.T, f snapFixture) ClaimInfo {
			c, held, err := f.st.OwnerOf("internal/api/server.go", f.bravo.SessionID)
			if err != nil || !held {
				t.Fatalf("OwnerOf: held=%v err=%v", held, err)
			}
			return c
		}},
		// PathClaims is OwnerOf's own function (D-228, panel P2-4): the same
		// seam, the same snapshot, so its Owner is pre-write too.
		{"PathClaims", func(s *Store, fn func()) { s.betweenOwnerLoad = fn }, func(t *testing.T, f snapFixture) ClaimInfo {
			v, err := f.st.PathClaims("internal/api/server.go", f.bravo.SessionID)
			if err != nil || !v.Held || v.Covered {
				t.Fatalf("PathClaims: %+v err=%v", v, err)
			}
			return v.Owner
		}},
	}
	for _, r := range readers {
		for _, w := range snapWrites {
			t.Run(r.name+"/"+w.name, func(t *testing.T) {
				f := newSnapFixture(t)
				fired := 0
				r.arm(f.st, func() {
					fired++
					w.write(t, f)
				})
				got := r.read(t, f)
				if fired != 1 {
					t.Fatalf("the seam fired %d times, want 1: the gap was never exercised", fired)
				}
				wantPreWrite(t, r.name, got)
				// Control: the write landed, and the next read sees it.
				r.arm(f.st, nil)
				after, err := f.st.Claims(true)
				if err != nil || len(after) != 1 {
					t.Fatalf("control read: %v, %d", err, len(after))
				}
				if a := after[0]; a.Desc == "desc-a" && a.State == "open" && a.Owner.SessionID == "sess-a" {
					t.Fatalf("control: the peer's %s never landed: %+v", w.name, a)
				}
			})
		}
	}
}

// dropSessionRow plants what nothing in buddy does: a claim whose session row
// is gone. Only a hand-edited ledger reaches it.
func dropSessionRow(t *testing.T, st *Store, id string) {
	t.Helper()
	if _, err := st.db.Exec(`DELETE FROM sessions WHERE session_id=?`, id); err != nil {
		t.Fatal(err)
	}
}

// Issue #126, part 2. OwnerOf loaded the claim it chose through an INNER JOIN on
// sessions, so a claim with no session row loaded zero rows and the gate read
// "not held" while `buddy claim` refused it. Both forms: the dangling claim
// alone, and a dangling shared claim chosen first hiding a live shared one.
func TestADanglingClaimHoldsAtEveryReader(t *testing.T) {
	cases := []struct {
		name   string
		shared bool
		peer   bool // charlie also holds a live shared claim on the same scope
	}{
		{"exclusive", false, false},
		{"shared beside a live shared", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := openTest(t)
			a := hello(t, st, "sess-a", "alpha", "/wt/a")
			b := hello(t, st, "sess-b", "bravo", "/wt/b")
			if err := st.ClaimMode(a.SessionID, a.Incarnation, "api-work", "x", []string{"internal/api"}, tc.shared); err != nil {
				t.Fatal(err)
			}
			if tc.peer {
				c := hello(t, st, "sess-c", "charlie", "/wt/c")
				if err := st.ClaimMode(c.SessionID, c.Incarnation, "c-work", "x", []string{"internal/api"}, true); err != nil {
					t.Fatal(err)
				}
			}
			// Control: held, and by a session with a row.
			if c, held, err := st.OwnerOf("internal/api/server.go", b.SessionID); err != nil || !held || c.Owner.Missing {
				t.Fatalf("control: held=%v missing=%v err=%v", held, c.Owner.Missing, err)
			}
			dropSessionRow(t, st, "sess-a")
			if err := st.Claim(b.SessionID, b.Incarnation, "wants", "x", []string{"internal/api"}); err == nil {
				t.Fatal("the claim door must still refuse")
			}
			c, held, err := st.OwnerOf("internal/api/server.go", b.SessionID)
			if err != nil || !held {
				t.Fatalf("the gate's door: held=%v err=%v; the claim door refuses, so this must hold", held, err)
			}
			if c.Slug == "api-work" && (!c.Owner.Missing || c.Owner.SessionID != "sess-a" || !slices.Equal(c.Scopes, []string{"internal/api"})) {
				t.Fatalf("a dangling claim loads with its own session id, Missing, and its scopes: %+v", c)
			}
			all, err := st.Claims(false)
			if err != nil {
				t.Fatal(err)
			}
			var listed *ClaimInfo
			for i := range all {
				if all[i].Slug == "api-work" {
					listed = &all[i]
				}
			}
			if listed == nil || !listed.Owner.Missing || listed.Owner.Label != "" || !slices.Equal(listed.Scopes, []string{"internal/api"}) {
				t.Fatalf("Claims must list the dangling claim, Missing, with its scopes: %+v", listed)
			}
			ts, err := st.ClaimsTouching("internal/api/server.go")
			if err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(ts, func(x ClaimTouch) bool { return x.Claim.Slug == "api-work" && x.Covers }) {
				t.Fatalf("ClaimsTouching must report the dangling claim as covering: %+v", ts)
			}
		})
	}
}

// RESIDUAL, pinned so a refactor cannot move it silently (issue #126, Fable
// panel): the caller's OWN shared claim clears a peer's shared hold even when
// the caller's session row is gone, because OwnerOf's scan reads ownership from
// the claim row and joins no sessions. The claim door says the caller is not
// live. Recorded open in the decision entry, not changed here: a session with
// no row is reached only by a hand edit, and every edit it makes is refused by
// nothing else either.
func TestOwnDanglingSharedStillClearsASharedHold(t *testing.T) {
	st, _ := openTest(t)
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	c := hello(t, st, "sess-c", "charlie", "/wt/c")
	if err := st.ClaimMode(b.SessionID, b.Incarnation, "b-work", "x", []string{"internal/api"}, true); err != nil {
		t.Fatal(err)
	}
	if err := st.ClaimMode(c.SessionID, c.Incarnation, "c-work", "x", []string{"internal/api"}, true); err != nil {
		t.Fatal(err)
	}
	// Control (Fable cross-check): the path IS under a shared hold for anyone
	// without a claim, so the residual's allow is the caller's own claim acting.
	if _, held, err := st.OwnerOf("internal/api/server.go", "sess-x"); err != nil || !held {
		t.Fatalf("control: a session with no claim is held: held=%v err=%v", held, err)
	}
	dropSessionRow(t, st, "sess-b")
	if _, held, err := st.OwnerOf("internal/api/server.go", "sess-b"); err != nil || held {
		t.Fatalf("residual moved: held=%v err=%v (update the entry if this was meant)", held, err)
	}
}

// A chosen claim that does not load is an ERROR, never "free" (issue #126). It
// is reachable without a dangling owner (Fable panel): a claim_id stored as a
// BLOB joins blob-to-blob in the scan and never equals the TEXT the load binds.
// Control: the same claim with a TEXT id is held.
func TestAChosenClaimThatDoesNotLoadRefuses(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.Claim(a.SessionID, a.Incarnation, "blobby", "x", []string{"internal/api"}); err != nil {
		t.Fatal(err)
	}
	if _, held, err := st.OwnerOf("internal/api/x.go", b.SessionID); err != nil || !held {
		t.Fatalf("control: held=%v err=%v", held, err)
	}
	for _, q := range []string{
		`UPDATE claim_scopes SET claim_id=CAST(claim_id AS BLOB)`,
		`UPDATE claims SET claim_id=CAST(claim_id AS BLOB)`,
	} {
		if _, err := st.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Claim(b.SessionID, b.Incarnation, "wants", "x", []string{"internal/api"}); err == nil {
		t.Fatal("the claim door must still refuse the BLOB-id claim")
	}
	_, held, err := st.OwnerOf("internal/api/x.go", b.SessionID)
	if err == nil || held {
		t.Fatalf("a chosen claim that loads 0 rows must be an error: held=%v err=%v", held, err)
	}
	if !strings.Contains(err.Error(), "loaded 0 rows") {
		t.Fatalf("the error must say what failed: %v", err)
	}
	// PathClaims shares the load and its one-row check (D-228, P2-4): the
	// gate's default posture calls it, so it must refuse the same way.
	if v, err := st.PathClaims("internal/api/x.go", b.SessionID); err == nil || v.Held || !strings.Contains(err.Error(), "loaded 0 rows") {
		t.Fatalf("PathClaims: a chosen claim that loads 0 rows must be an error: %+v err=%v", v, err)
	}
}

// OwnerOf returns readSnapshot's own error (issue #126, Fable panel): the
// nearest example, TwoRemediesAt, drops it with `_ =`, and that shape here reads
// a failed BEGIN as "free". A closed Store is the forcing; control: open, it reads.
func TestOwnerOfReturnsASnapshotFailure(t *testing.T) {
	st, _ := openTest(t)
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if _, held, err := st.OwnerOf("internal/api/x.go", b.SessionID); err != nil || held {
		t.Fatalf("control: held=%v err=%v", held, err)
	}
	if _, err := st.PathClaims("internal/api/x.go", b.SessionID); err != nil {
		t.Fatalf("control: PathClaims reads: %v", err)
	}
	st.Close()
	if _, held, err := st.OwnerOf("internal/api/x.go", b.SessionID); err == nil || held {
		t.Fatalf("a snapshot that cannot open must be an error: held=%v err=%v", held, err)
	}
	if v, err := st.PathClaims("internal/api/x.go", b.SessionID); err == nil || v.Held || v.Covered {
		t.Fatalf("PathClaims: a snapshot that cannot open must be an error: %+v err=%v", v, err)
	}
}

// readSnapshot reports a COMMIT that fails (issue #126): it was a bare deferred
// Exec. The forcing is fn ending the transaction itself, so the deferred COMMIT
// has none to end. Control: an ordinary fn returns nil, and the pool still reads
// afterwards (the failed connection was not handed back pinned).
func TestReadSnapshotReportsItsCommit(t *testing.T) {
	st, _ := openTest(t)
	hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.readSnapshot(func(q querier) error {
		var n int
		return q.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n)
	}); err != nil {
		t.Fatalf("control: %v", err)
	}
	open := -1
	err := st.readSnapshot(func(q querier) error {
		open = st.db.Stats().OpenConnections
		rows, err := q.Query(`COMMIT`)
		if err != nil {
			return err
		}
		return rows.Close()
	})
	if err == nil || !strings.Contains(err.Error(), "ending a read snapshot") {
		t.Fatalf("a failed COMMIT must be the call's error: %v", err)
	}
	// The connection is discarded, never pooled (Fable cross-check: without the
	// discard every assertion above still passed). Control: it was open inside.
	if open < 1 {
		t.Fatalf("control: no connection was open inside the snapshot (%d)", open)
	}
	if now := st.db.Stats().OpenConnections; now != open-1 {
		t.Fatalf("the failed connection must be closed, not pooled: %d open inside, %d after", open, now)
	}
	if _, err := st.Claims(false); err != nil {
		t.Fatalf("the pool must still read after a discarded connection: %v", err)
	}
}
