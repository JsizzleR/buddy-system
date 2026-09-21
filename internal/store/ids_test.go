package store

import (
	"errors"
	"testing"
)

// The identifier register (D-029): seeded before anything is taken, blocks
// contiguous above the ceiling, nothing ever reissued, and a status that
// never claims to know the artifact.

func TestIDTakeRefusesAnUnseededSpaceAndAllocatesAboveTheCeiling(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if _, _, err := st.IDTake("record", 5, a.SessionID, a.Incarnation, a.Label, ""); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("an unseeded space must refuse, got %v", err)
	}
	// Seeded with the artifact's measured high-water mark.
	if prev, created, err := st.IDSeed("record", 1704); err != nil || !created || prev != 0 {
		t.Fatalf("seed: %d %v %v", prev, created, err)
	}
	lo, hi, err := st.IDTake("record", 5, a.SessionID, a.Incarnation, a.Label, "cve rows")
	if err != nil || lo != 1705 || hi != 1709 {
		t.Fatalf("first block: %d..%d %v", lo, hi, err)
	}
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	lo, hi, err = st.IDTake("record", 1, b.SessionID, b.Incarnation, b.Label, "")
	if err != nil || lo != 1710 || hi != 1710 {
		t.Fatalf("second block must start above the first: %d..%d %v", lo, hi, err)
	}
	// Non-positive, and overflow, refused.
	if _, _, err := st.IDTake("record", 0, a.SessionID, a.Incarnation, a.Label, ""); err == nil {
		t.Fatal("count 0 must refuse")
	}
	if _, _, err := st.IDSeed("huge", 1<<62); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.IDTake("huge", 1<<62, a.SessionID, a.Incarnation, a.Label, ""); err == nil {
		t.Fatal("overflow must refuse")
	}
	// The ceiling can be raised (the artifact moved under us) and never lowered.
	if prev, created, err := st.IDSeed("record", 1800); err != nil || created || prev != 1710 {
		t.Fatalf("raise: %d %v %v", prev, created, err)
	}
	if _, _, err := st.IDSeed("record", 1000); err == nil {
		t.Fatal("lowering must refuse")
	}
	if lo, _, _ := st.IDTake("record", 1, a.SessionID, a.Incarnation, a.Label, ""); lo != 1801 {
		t.Fatalf("after a raise the next block starts above the new ceiling: %d", lo)
	}
	blocks, err := st.IDBlocks("record")
	if err != nil || len(blocks) != 3 || blocks[0].Label != "alpha" || blocks[0].Note != "cve rows" || blocks[1].Label != "bravo" {
		t.Fatalf("blocks: %+v %v", blocks, err)
	}
}

func TestIDStatusAnswersInThreeRegistersAndOverclaimsNone(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if _, _, _, err := st.IDStatus("record", 5); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("unseeded: %v", err)
	}
	if _, _, err := st.IDSeed("record", 100); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.IDTake("record", 3, a.SessionID, a.Incarnation, a.Label, "x"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		n    int64
		want IDVerdict
	}{
		{101, IDReserved}, {103, IDReserved},
		{104, IDAboveCeiling}, {999, IDAboveCeiling},
		{100, IDBelowCeilingUnreserved}, {42, IDBelowCeilingUnreserved}, {0, IDBelowCeilingUnreserved},
	}
	for _, tc := range cases {
		v, b, ceiling, err := st.IDStatus("record", tc.n)
		if err != nil || v != tc.want || ceiling != 103 {
			t.Fatalf("status %d: %v %v ceiling %d want %v", tc.n, v, err, ceiling, tc.want)
		}
		if v == IDReserved && (b.Label != "alpha" || b.Lo != 101 || b.Hi != 103) {
			t.Fatalf("reserved block: %+v", b)
		}
	}
}

// A block outlives its session's end, orphaning and sweep: it is a fact
// about the artifact's number line, not about a session's life.
func TestIDBlocksSurviveTheirSession(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if _, _, err := st.IDSeed("record", 10); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.IDTake("record", 2, a.SessionID, a.Incarnation, a.Label, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Bye(a.SessionID, a.Incarnation); err != nil {
		t.Fatal(err)
	}
	clk.advance(72 * 3600 * 1e9)
	if _, _, err := st.Sweep(24*3600*1e9, 24*3600*1e9, true); err != nil {
		t.Fatal(err)
	}
	v, b, _, err := st.IDStatus("record", 11)
	if err != nil || v != IDReserved || b.Label != "alpha" {
		t.Fatalf("a block must survive its session: %v %+v %v", v, b, err)
	}
	b2 := hello(t, st, "sess-b", "bravo", "/wt/b")
	if lo, _, _ := st.IDTake("record", 1, b2.SessionID, b2.Incarnation, b2.Label, ""); lo != 13 {
		t.Fatalf("the dead session's block is never reissued: next is %d", lo)
	}
}
