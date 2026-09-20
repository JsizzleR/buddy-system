package store

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// ByeFrom, the process-fenced end (D-025), at the store level: the
// registration bookkeeping the CLI tests exercise end to end, pinned here
// where a mutation of the transaction is visible without a git fixture.

// aliveSet answers ByeFrom's seam from a map of pid -> born.
func aliveSet(m map[int]int64) func(ProcRef) bool {
	return func(p ProcRef) bool {
		born, ok := m[p.PID]
		return ok && (p.Born == 0 || born == p.Born)
	}
}

func TestByeFromEndsOnlyWhenNoOtherRegisteredProcessIsAlive(t *testing.T) {
	st, _ := openTest(t)
	if _, err := st.HelloFrom("sess-a", "alpha", "/wt/a", ProcRef{PID: 100, Born: 1}, ""); err != nil {
		t.Fatal(err)
	}
	// A second process joins through its heartbeat.
	if err := st.BeatFrom("sess-a", "", ProcRef{PID: 200, Born: 2}); err != nil {
		t.Fatal(err)
	}
	procs, err := st.SessionProcs()
	if err != nil {
		t.Fatal(err)
	}
	if want := []ProcRef{{100, 1}, {200, 2}}; !reflect.DeepEqual(procs["sess-a"], want) {
		t.Fatalf("registrations: got %v, want %v", procs["sess-a"], want)
	}
	alive := map[int]int64{100: 1, 200: 2}

	// A stranger (a delayed bye from a dead incarnation's process) is refused
	// and names both live processes; nothing is written.
	res, err := st.ByeFrom("sess-a", ProcRef{PID: 50, Born: 9}, aliveSet(alive), false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Known || res.Ended || !res.Stranger || !reflect.DeepEqual(res.Blocking, []ProcRef{{100, 1}, {200, 2}}) {
		t.Fatalf("stranger's bye: %+v", res)
	}
	// 100 exits: its registration goes, 200 keeps the session open.
	res, err = st.ByeFrom("sess-a", ProcRef{PID: 100, Born: 1}, aliveSet(alive), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Ended || res.Stranger || !reflect.DeepEqual(res.Blocking, []ProcRef{{200, 2}}) {
		t.Fatalf("passenger's bye: %+v", res)
	}
	delete(alive, 100)
	procs, _ = st.SessionProcs()
	if !reflect.DeepEqual(procs["sess-a"], []ProcRef{{200, 2}}) {
		t.Fatalf("the exiting process must be deregistered: %v", procs["sess-a"])
	}
	if si, _, _ := st.SessionByID("sess-a"); !si.Live() {
		t.Fatal("session ended while 200 was registered and alive")
	}
	// 200 exits: nothing else alive, so it ends and the table is cleared.
	res, err = st.ByeFrom("sess-a", ProcRef{PID: 200, Born: 2}, aliveSet(alive), false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Ended || len(res.Blocking) != 0 {
		t.Fatalf("last process's bye: %+v", res)
	}
	if si, _, _ := st.SessionByID("sess-a"); si.Live() {
		t.Fatal("not ended")
	}
	if procs, _ = st.SessionProcs(); len(procs["sess-a"]) != 0 {
		t.Fatalf("registrations must be cleared with the end: %v", procs["sess-a"])
	}
	// An ended session: not known, nothing to do.
	if res, err := st.ByeFrom("sess-a", ProcRef{PID: 200, Born: 2}, aliveSet(alive), false); err != nil || res.Known {
		t.Fatalf("bye on an ended session: %+v %v", res, err)
	}
}

func TestByeFromPrunesDeadRegistrationsAndEndsAnUnboundSession(t *testing.T) {
	st, _ := openTest(t)
	if _, err := st.HelloFrom("sess-a", "alpha", "/wt/a", ProcRef{PID: 100, Born: 1}, ""); err != nil {
		t.Fatal(err)
	}
	// 100 died without a bye; a recycled 100 (born 7) is not it.
	res, err := st.ByeFrom("sess-a", ProcRef{}, aliveSet(map[int]int64{100: 7}), false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Ended || len(res.Blocking) != 0 || res.Stranger {
		t.Fatalf("a dead (recycled) registration protects nothing: %+v", res)
	}

	// Unbound: no registrations at all, the old behaviour.
	if _, err := st.Hello("sess-b", "bravo", "/wt/b", 1234); err != nil {
		t.Fatal(err)
	}
	if procs, _ := st.SessionProcs(); len(procs["sess-b"]) != 0 {
		t.Fatalf("Hello (no proc) must register nothing: %v", procs["sess-b"])
	}
	res, err = st.ByeFrom("sess-b", ProcRef{PID: 300, Born: 3}, aliveSet(map[int]int64{}), false)
	if err != nil || !res.Ended || res.Stranger {
		t.Fatalf("unbound session must end on any bye: %+v %v", res, err)
	}

	// --force ends past a live registration; the operator's act.
	if _, err := st.HelloFrom("sess-c", "charlie", "/wt/c", ProcRef{PID: 400, Born: 4}, ""); err != nil {
		t.Fatal(err)
	}
	alive := aliveSet(map[int]int64{400: 4})
	if res, _ := st.ByeFrom("sess-c", ProcRef{}, alive, false); res.Ended {
		t.Fatal("a live registration must refuse an unforced manual bye")
	}
	if res, _ := st.ByeFrom("sess-c", ProcRef{}, alive, true); !res.Ended {
		t.Fatal("--force must end it")
	}
}

func TestHelloFromKeepsRegistrationsAcrossARefreshAndDropsThemOnRevival(t *testing.T) {
	st, _ := openTest(t)
	if _, err := st.HelloFrom("sess-a", "alpha", "/wt/a", ProcRef{PID: 100, Born: 1}, "herdr:w1:p1"); err != nil {
		t.Fatal(err)
	}
	// A refresh with no process and no terminal keeps both.
	si, err := st.HelloFrom("sess-a", "", "/wt/a", ProcRef{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if si.PID != 100 || si.Terminal != "herdr:w1:p1" {
		t.Fatalf("refresh must keep pid and terminal: %+v", si)
	}
	if procs, _ := st.SessionProcs(); !reflect.DeepEqual(procs["sess-a"], []ProcRef{{100, 1}}) {
		t.Fatalf("refresh must keep the registration: %v", procs["sess-a"])
	}
	// Revival replaces both, even with nothing.
	if err := st.Bye("sess-a", ""); err != nil {
		t.Fatal(err)
	}
	if procs, _ := st.SessionProcs(); len(procs["sess-a"]) != 0 {
		t.Fatalf("Bye must clear registrations: %v", procs["sess-a"])
	}
	// A registration that somehow survived the end (both end paths clear
	// them today, so this row is planted) must not survive the revival: the
	// new incarnation would otherwise be refused its own bye by a dead
	// process's row. Pinned so a future end path that forgets is caught here.
	if _, err := st.db.Exec(`INSERT INTO session_procs (session_id, pid, born, registered) VALUES ('sess-a', 555, 5, 1)`); err != nil {
		t.Fatal(err)
	}
	si, err = st.HelloFrom("sess-a", "", "/wt/a", ProcRef{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if si.PID != 0 || si.Terminal != "" {
		t.Fatalf("revival with nothing must report nothing: %+v", si)
	}
	if procs, _ := st.SessionProcs(); len(procs["sess-a"]) != 0 {
		t.Fatalf("revival must drop the dead incarnation's registrations: %v", procs["sess-a"])
	}
}

// A ledger at schema 5 has a sessions table without the terminal column; the
// migration's ALTER arm adds it exactly once. The old ledger is built by
// hand rather than by dropping the column from a new one: DROP COLUMN edits
// the stored CREATE text and trips over the column's own comment, and a
// hand-built table is what an old ledger actually looks like.
func TestMigrationAddsTerminalColumnToAnOldLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE sessions (session_id TEXT PRIMARY KEY, incarnation TEXT NOT NULL, label TEXT NOT NULL,
			worktree TEXT NOT NULL, pid INTEGER NOT NULL DEFAULT 0, started INTEGER NOT NULL, last_seen INTEGER NOT NULL, ended INTEGER)`,
		`INSERT INTO sessions VALUES ('sess-a','inc1','alpha','/wt/a',0,1,1,NULL)`,
		`PRAGMA user_version = 5`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	raw.Close()
	clk := &pinnedClock{t: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)}
	st, err := Open(path, clk.now)
	if err != nil {
		t.Fatalf("Open must migrate: %v", err)
	}
	defer st.Close()
	si, ok, err := st.SessionByID("sess-a")
	if err != nil || !ok || si.Terminal != "" || si.Label != "alpha" {
		t.Fatalf("after migration: %+v %v %v", si, ok, err)
	}
	var ver int
	if err := st.db.QueryRow(`PRAGMA user_version`).Scan(&ver); err != nil || ver != schemaVersion {
		t.Fatalf("stamp: %d %v", ver, err)
	}
	// Idempotent: a second pass over a ledger already carrying the column
	// must not try the ALTER again.
	if _, err := st.db.Exec(`PRAGMA user_version = 5`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(st.db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if _, err := st.HelloFrom("sess-a", "", "/wt/a", ProcRef{PID: 9, Born: 9}, "herdr:w1:p1"); err != nil {
		t.Fatalf("the migrated table must take a terminal: %v", err)
	}
}
