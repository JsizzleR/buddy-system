package store

import (
	"fmt"
	"testing"
	"time"
)

func TestBrowserUseIsFencedByIncarnation(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")

	if _, ok, err := st.BrowserUse(a.SessionID, a.Incarnation); ok || err != nil {
		t.Fatalf("nothing recorded must read as none observed: %v %v", ok, err)
	}
	if err := st.RecordBrowserUse(a.SessionID, a.Incarnation); err != nil {
		t.Fatal(err)
	}
	first := clk.now()
	if got, ok, err := st.BrowserUse(a.SessionID, a.Incarnation); !ok || err != nil || !got.Equal(first.Truncate(time.Second)) {
		t.Fatalf("control: the live incarnation's call is read back: %v %v %v", got, ok, err)
	}

	clk.t = clk.t.Add(5 * time.Minute)
	if err := st.RecordBrowserUse(a.SessionID, a.Incarnation); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := st.BrowserUse(a.SessionID, a.Incarnation); !got.Equal(clk.now().Truncate(time.Second)) {
		t.Fatalf("a later call moves the reading: %v", got)
	}

	// A wall clock stepped backwards does not move it.
	later := clk.now()
	clk.t = clk.t.Add(-2 * time.Minute)
	if err := st.RecordBrowserUse(a.SessionID, a.Incarnation); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := st.BrowserUse(a.SessionID, a.Incarnation); !got.Equal(later.Truncate(time.Second)) {
		t.Fatalf("the reading moved backwards: %v", got)
	}
	clk.t = later

	// A stale token writes nothing, and an empty one is not a wildcard.
	clk.t = clk.t.Add(time.Minute)
	for _, inc := range []string{"not-the-incarnation", ""} {
		if err := st.RecordBrowserUse(a.SessionID, inc); err != nil {
			t.Fatal(err)
		}
		if got, _, _ := st.BrowserUse(a.SessionID, a.Incarnation); !got.Equal(later.Truncate(time.Second)) {
			t.Fatalf("token %q moved the reading: %v", inc, got)
		}
	}

	// A session nobody knows gets no row.
	if err := st.RecordBrowserUse("sess-nobody", "x"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.BrowserUse("sess-nobody", "x"); ok {
		t.Fatal("an unknown session got a row")
	}
}

func TestBrowserUseDoesNotCrossABye(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.RecordBrowserUse(a.SessionID, a.Incarnation); err != nil {
		t.Fatal(err)
	}
	if err := st.Bye(a.SessionID, a.Incarnation); err != nil {
		t.Fatal(err)
	}

	// A beat racing the bye writes nothing under the ended session.
	clk.t = clk.t.Add(time.Minute)
	if err := st.RecordBrowserUse(a.SessionID, a.Incarnation); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := st.BrowserUse(a.SessionID, a.Incarnation); !ok || got.After(clk.now().Add(-time.Minute)) {
		t.Fatalf("a beat after the bye moved the reading: %v %v", got, ok)
	}

	// A revival is a new incarnation: the old reading is not its, and its own
	// first call replaces the row.
	b := hello(t, st, "sess-a", "alpha", "/wt/a")
	if b.Incarnation == a.Incarnation {
		t.Fatal("control: a revival must mint a new incarnation")
	}
	if _, ok, _ := st.BrowserUse(b.SessionID, b.Incarnation); ok {
		t.Fatal("the new incarnation read the old one's browser use")
	}
	// Whatever the clock did in between: the monotonic guard is per incarnation,
	// so a clock behind the old row's reading must still replace it.
	clk.t = clk.t.Add(-time.Hour)
	if err := st.RecordBrowserUse(b.SessionID, b.Incarnation); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.BrowserUse(b.SessionID, b.Incarnation); !ok {
		t.Fatal("the new incarnation's own call must replace the old row")
	}
	if _, ok, _ := st.BrowserUse(a.SessionID, a.Incarnation); ok {
		t.Fatal("the replaced row still answers for the old incarnation")
	}
}

// The reading is one session's: a peer's call is not attributed to anyone else.
func TestBrowserUseIsPerSession(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.RecordBrowserUse(a.SessionID, a.Incarnation); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.BrowserUse(b.SessionID, b.Incarnation); ok {
		t.Fatal("bravo read alpha's browser use")
	}
	if _, ok, _ := st.BrowserUse(a.SessionID, a.Incarnation); !ok {
		t.Fatal("control: alpha reads its own")
	}
}

func TestMigrationToSchema19(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.Claim(a.SessionID, a.Incarnation, "old", "d", []string{"src/a.go"}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DROP TABLE session_browser`, `PRAGMA user_version = 18`} {
		if _, err := st.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, _, err := st.BrowserUse(a.SessionID, a.Incarnation); err == nil {
		t.Fatal("control: the table must be gone before the migration")
	}
	if err := migrate(st.db); err != nil {
		t.Fatal(err)
	}
	var ver int
	if err := st.db.QueryRow(`PRAGMA user_version`).Scan(&ver); err != nil || ver != schemaVersion {
		t.Fatalf("user_version %d %v", ver, err)
	}
	if _, ok, err := st.BrowserUse(a.SessionID, a.Incarnation); ok || err != nil {
		t.Fatalf("a migrated session has observed nothing: %v %v", ok, err)
	}
	if err := st.RecordBrowserUse(a.SessionID, a.Incarnation); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.BrowserUse(a.SessionID, a.Incarnation); !ok {
		t.Fatal("the migrated table does not take a reading")
	}
	if got := claimBySlug(t, st, "old"); got.State != "open" {
		t.Fatalf("the migration changed a claim: %s", fmt.Sprint(got.State))
	}
}
