package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// D-131: a job declared on a claim is a set of columns on its row. Schema 17.

func TestClaimWithJobRecordsKeepsAndReplaces(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	scopes := []string{".buddy/slot/run"}

	if err := st.ClaimMode(a.SessionID, a.Incarnation, "plain", "x", []string{"p"}, false); err != nil {
		t.Fatal(err)
	}
	if got := claimBySlug(t, st, "plain").Job; got != (JobDecl{}) {
		t.Fatalf("no declaration reads as zero: %+v", got)
	}

	first := JobDecl{PID: 100, Born: 111, Boot: "boot-A"}
	if err := st.ClaimWithJob(a.SessionID, a.Incarnation, "run", "RUNNING", scopes, false, &first); err != nil {
		t.Fatal(err)
	}
	if got := claimBySlug(t, st, "run").Job; got != first {
		t.Fatalf("a new claim records it: %+v", got)
	}
	// A refresh with no job keeps it, one with a job replaces it.
	if err := st.Claim(a.SessionID, a.Incarnation, "run", "HOLD", scopes); err != nil {
		t.Fatal(err)
	}
	if got := claimBySlug(t, st, "run").Job; got != first {
		t.Fatalf("a refresh naming no job keeps it: %+v", got)
	}
	second := JobDecl{PID: 200, Born: 222, Boot: "boot-A"}
	if err := st.ClaimWithJob(a.SessionID, a.Incarnation, "run", "RUNNING", scopes, false, &second); err != nil {
		t.Fatal(err)
	}
	if got := claimBySlug(t, st, "run").Job; got != second {
		t.Fatalf("a refresh naming a job replaces it: %+v", got)
	}

	// A refused refresh changes nothing: a peer's scope, in one transaction.
	if err := st.Claim(b.SessionID, b.Incarnation, "peer", "x", []string{"q"}); err != nil {
		t.Fatal(err)
	}
	third := JobDecl{PID: 300, Born: 333, Boot: "boot-A"}
	if err := st.ClaimWithJob(a.SessionID, a.Incarnation, "run", "RUNNING", []string{"q"}, false, &third); err == nil {
		t.Fatal("control: an overlapping refresh is refused")
	}
	if got := claimBySlug(t, st, "run").Job; got != second {
		t.Fatalf("a refused claim declared a job: %+v", got)
	}

	// The transfer moves the row, id and declaration with it.
	id := claimBySlug(t, st, "run").ClaimID
	if _, _, err := st.TransferClaim(a.SessionID, a.Incarnation, "run", mustResolve(t, st, "bravo")); err != nil {
		t.Fatal(err)
	}
	moved := claimBySlug(t, st, "run")
	if moved.ClaimID != id || moved.Owner.SessionID != b.SessionID || moved.Job != second {
		t.Fatalf("release --to keeps the declaration on the new holder: %+v", moved)
	}
}

// A ledger at 16 with no job columns (dropped here; schema 13's exact text
// reaches these arms through TestSchema14's fixture): the columns arrive by the
// ALTER arm, every claim on it declares nothing, and the stamp moves.
func TestSchema17AddsTheJobColumnsToALedgerAt16(t *testing.T) {
	path := filepath.Join(t.TempDir(), "l.db")
	st, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.Claim(a.SessionID, a.Incarnation, "old", "x", []string{"p"}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`ALTER TABLE claims DROP COLUMN job_boot`, `ALTER TABLE claims DROP COLUMN job_born`,
		`ALTER TABLE claims DROP COLUMN job_pid`, `PRAGMA user_version = 16`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// Control: this really is a ledger with no such column.
	if _, err := db.Exec(`SELECT job_pid FROM claims`); err == nil {
		t.Fatal("the fixture still has job_pid")
	}
	db.Close()

	st, err = Open(path, nil)
	if err != nil {
		t.Fatalf("migrating a ledger at 16: %v", err)
	}
	defer st.Close()
	var ver int
	if err := st.db.QueryRow(`PRAGMA user_version`).Scan(&ver); err != nil || ver != schemaVersion || schemaVersion < 17 {
		t.Fatalf("want user_version stamped %d (>=17), got %d (err=%v)", schemaVersion, ver, err)
	}
	c := claimBySlug(t, st, "old")
	if c.Job != (JobDecl{}) || c.Slug != "old" {
		t.Fatalf("a claim from before reads as declaring nothing: %+v", c)
	}
	declared := JobDecl{PID: 5, Born: 6, Boot: "b"}
	if err := st.ClaimWithJob(a.SessionID, a.Incarnation, "old", "x", []string{"p"}, false, &declared); err != nil {
		t.Fatal(err)
	}
	if got := claimBySlug(t, st, "old").Job; got != declared {
		t.Fatalf("a migrated row takes a declaration: %+v", got)
	}
}
