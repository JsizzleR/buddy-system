package store

import (
	"strings"
	"testing"
	"time"
)

// D-038 (issue #28): a session's base is fenced like its context footprint —
// its own incarnation only, and a newer-or-equal turn only.
func TestBaseBelongsToOneIncarnationAndNeverRollsBack(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	shaA, shaB, shaOld := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("0", 40)

	if err := st.RecordBase(a.SessionID, a.Incarnation, shaA, clk.t); err != nil {
		t.Fatal(err)
	}
	got, err := st.Bases()
	if err != nil {
		t.Fatal(err)
	}
	if got["sess-a"].SHA != shaA || !got["sess-a"].TurnAt.Equal(clk.t) {
		t.Fatalf("control: a live session's own base must read back: %+v", got["sess-a"])
	}

	// A delayed Stop carrying an OLDER turn must not move the base back to a
	// tree the session has since left; a newer one replaces it.
	if err := st.RecordBase(a.SessionID, a.Incarnation, shaOld, clk.t.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Bases(); got["sess-a"].SHA != shaA {
		t.Fatalf("an older turn rolled the base back: %+v", got["sess-a"])
	}
	if err := st.RecordBase(a.SessionID, a.Incarnation, shaB, clk.t.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Bases(); got["sess-a"].SHA != shaB {
		t.Fatalf("control: a newer turn must replace the base: %+v", got["sess-a"])
	}

	// A revived id is a new session: the predecessor's base is hidden, and a
	// late write from the predecessor is dropped.
	clk.advance(time.Hour)
	if err := st.Bye("sess-a", a.Incarnation); err != nil {
		t.Fatal(err)
	}
	b := hello(t, st, "sess-a", "alpha", "/wt/a")
	if got, _ := st.Bases(); got["sess-a"].SHA != "" {
		t.Fatalf("the previous incarnation's base must not be reported as this one's: %+v", got["sess-a"])
	}
	if err := st.RecordBase("sess-a", a.Incarnation, shaOld, clk.t.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Bases(); got["sess-a"].SHA != "" {
		t.Fatalf("a late write from a superseded incarnation landed: %+v", got["sess-a"])
	}
	if err := st.RecordBase("sess-a", b.Incarnation, shaA, clk.t); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Bases(); got["sess-a"].SHA != shaA || got["sess-a"].Incarnation != b.Incarnation {
		t.Fatalf("control: the revived incarnation records its own: %+v", got["sess-a"])
	}
	// And a late write from the predecessor AFTER the successor has a row
	// must not replace it (the case the join alone cannot hide).
	if err := st.RecordBase("sess-a", a.Incarnation, shaOld, clk.t.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Bases(); got["sess-a"].SHA != shaA || got["sess-a"].Incarnation != b.Incarnation {
		t.Fatalf("a superseded incarnation overwrote its successor's base: %+v", got["sess-a"])
	}
}

// A ledger at schema 9 gains session_base and keeps its rows.
func TestSchema10MigratesAnOlderLedger(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.Claim(a.SessionID, a.Incarnation, "keep", "x", []string{"keep"}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DROP TABLE session_base`, `PRAGMA user_version = 9`} {
		if _, err := st.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.RecordBase(a.SessionID, a.Incarnation, strings.Repeat("a", 40), clk.t); err == nil {
		t.Fatal("control: without the table a base cannot be recorded")
	}
	if err := migrate(st.db); err != nil {
		t.Fatal(err)
	}
	var ver int
	if err := st.db.QueryRow(`PRAGMA user_version`).Scan(&ver); err != nil || ver != schemaVersion || schemaVersion < 10 {
		t.Fatalf("want user_version stamped %d (>=10), got %d (err=%v)", schemaVersion, ver, err)
	}
	if err := st.RecordBase(a.SessionID, a.Incarnation, strings.Repeat("a", 40), clk.t); err != nil {
		t.Fatalf("the migrated ledger must accept a base: %v", err)
	}
	if claims, _ := st.Claims(false); len(claims) != 1 {
		t.Fatalf("the migration lost a claim: %d", len(claims))
	}
}
