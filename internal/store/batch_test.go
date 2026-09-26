package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// D-049: a release records the holder's reported outcome, a wait records the
// commit its waiter declared ready, and both read back through the one join
// every wait surface renders.

func TestAReleaseOutcomeAndAReadyCommitReadBackOnTheWait(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.Claim(a.SessionID, a.Incarnation, "herm", "FORMING", []string{".buddy/slot/herm"}); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("c", 40)
	if _, _, err := st.DeclareWaitReady(b.SessionID, b.Incarnation, []string{"herm"}, time.Hour, "", sha); err != nil {
		t.Fatal(err)
	}

	// Refused before anything is written: the claim stays open.
	for _, bad := range [][2]string{{"maybe", ""}, {"", "a note with no outcome"}} {
		if _, err := st.ReleaseOutcome(a.SessionID, a.Incarnation, "herm", bad[0], bad[1]); err == nil {
			t.Fatalf("release with outcome %q note %q must be refused", bad[0], bad[1])
		}
	}
	if w, _, _ := st.WaitOf(b.SessionID); len(w.Targets) != 1 || !w.Targets[0].IsOpen() {
		t.Fatalf("a refused release must leave the claim open: %+v", w.Targets)
	}

	if _, err := st.ReleaseOutcome(a.SessionID, a.Incarnation, "herm", "fail", "bravo ejected"); err != nil {
		t.Fatal(err)
	}
	w, ok, err := st.WaitOf(b.SessionID)
	if err != nil || !ok {
		t.Fatalf("wait: %v %v", ok, err)
	}
	if w.ReadySHA != sha {
		t.Fatalf("ready commit: got %q", w.ReadySHA)
	}
	if tg := w.Targets[0]; tg.State != "released" || tg.Outcome != "fail" || tg.OutcomeNote != "bravo ejected" {
		t.Fatalf("the target carries the reported outcome: %+v", tg)
	}

	// A plain release reports nothing, and a plain wait declares nothing ready.
	if err := st.Claim(a.SessionID, a.Incarnation, "herm", "again", []string{".buddy/slot/herm"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.DeclareWait(b.SessionID, b.Incarnation, []string{"herm"}, time.Hour, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Release(a.SessionID, a.Incarnation, "herm"); err != nil {
		t.Fatal(err)
	}
	w, _, _ = st.WaitOf(b.SessionID)
	if w.ReadySHA != "" || w.Targets[0].Outcome != "" || w.Targets[0].OutcomeNote != "" {
		t.Fatalf("nothing declared, nothing reported: %+v", w)
	}

	// A timer has nobody to hand readiness to.
	if _, _, err := st.DeclareWaitReady(b.SessionID, b.Incarnation, nil, time.Hour, "", sha); err == nil {
		t.Fatal("--ready with no target must be refused")
	}
}

// The column CHECK is the ledger's own guard: a value no CLI path writes is
// refused by SQLite too.
func TestTheOutcomeColumnRefusesAnUnknownWord(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.Claim(a.SessionID, a.Incarnation, "herm", "d", []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE claims SET outcome='maybe' WHERE slug='herm'`); err == nil {
		t.Fatal("the CHECK must refuse an outcome off the list")
	}
	if _, err := st.db.Exec(`UPDATE claims SET outcome='aborted' WHERE slug='herm'`); err != nil {
		t.Fatalf("control: a listed outcome is accepted: %v", err)
	}
}

// A schema-13 ledger gains the three columns with every old row reading
// "nothing reported, nothing declared ready", and the new writes work on it.
func TestSchema14MigratesAnOlderLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	st, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.Claim(a.SessionID, a.Incarnation, "herm", "d", []string{".buddy/slot/herm"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.DeclareWait(b.SessionID, b.Incarnation, []string{"herm"}, time.Hour, "old"); err != nil {
		t.Fatal(err)
	}
	st.Close()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// claims is rebuilt from schema 13's EXACT text, not by DROP COLUMN:
	// SQLite edits the stored CREATE text, and v13's text ends in a `--`
	// line comment just before `)`. That comment is the risk an ALTER ADD
	// COLUMN on a real v13 ledger has to survive, so the fixture keeps it
	// (DROP COLUMN mangled it into "incomplete input" while building this).
	for _, q := range []string{
		`CREATE TABLE claims_v13 (
	claim_id    TEXT PRIMARY KEY,
	session_id  TEXT NOT NULL,
	incarnation TEXT NOT NULL,
	slug        TEXT NOT NULL,
	descr       TEXT NOT NULL,
	created     INTEGER NOT NULL,
	renewed     INTEGER NOT NULL,
	state       TEXT NOT NULL CHECK (state IN ('open','released','orphaned')),
	shared      INTEGER NOT NULL DEFAULT 0 -- D-042: overlaps other SHARED claims; never an exclusive one
)`,
		`INSERT INTO claims_v13 SELECT claim_id, session_id, incarnation, slug, descr, created, renewed, state, shared FROM claims`,
		`DROP TABLE claims`, `ALTER TABLE claims_v13 RENAME TO claims`,
		`CREATE TABLE session_waits_v13 (
	session_id  TEXT PRIMARY KEY,
	incarnation TEXT NOT NULL,
	-- The declaration's own identity. Not since: whole seconds cannot tell a
	-- wait from its replacement declared in the same second (Codex design pass).
	decl        TEXT NOT NULL,
	since       INTEGER NOT NULL,
	deadline    INTEGER NOT NULL,
	note        TEXT NOT NULL,
	last_check  INTEGER NOT NULL DEFAULT 0,
	checks      INTEGER NOT NULL DEFAULT 0,
	told        INTEGER NOT NULL DEFAULT 0,
	cleared     INTEGER,
	reason      TEXT CHECK (reason IS NULL OR reason IN ('landed','expired','cleared','ended'))
)`,
		`INSERT INTO session_waits_v13 SELECT session_id, incarnation, decl, since, deadline, note, last_check, checks, told, cleared, reason FROM session_waits`,
		`DROP TABLE session_waits`, `ALTER TABLE session_waits_v13 RENAME TO session_waits`,
		`PRAGMA user_version = 13`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	db.Close()

	st, err = Open(path, nil)
	if err != nil {
		t.Fatalf("a v13 ledger must migrate: %v", err)
	}
	defer st.Close()
	var ver int
	if err := st.db.QueryRow(`PRAGMA user_version`).Scan(&ver); err != nil || ver != schemaVersion || schemaVersion < 14 {
		t.Fatalf("want user_version stamped %d (>=14), got %d (err=%v)", schemaVersion, ver, err)
	}
	w, ok, err := st.WaitOf(b.SessionID)
	if err != nil || !ok || w.Note != "old" || w.ReadySHA != "" {
		t.Fatalf("the old wait reads back, declaring nothing ready: %+v %v %v", w, ok, err)
	}
	if _, err := st.ReleaseOutcome(a.SessionID, a.Incarnation, "herm", "pass", "n"); err != nil {
		t.Fatalf("a migrated ledger records an outcome: %v", err)
	}
	if w, _, _ = st.WaitOf(b.SessionID); w.Targets[0].Outcome != "pass" {
		t.Fatalf("the outcome reads back: %+v", w.Targets[0])
	}
}
