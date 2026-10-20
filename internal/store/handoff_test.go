package store

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// The busy hook's sequence, as the tests below drive it: every observation
// is recorded the way a hook records it (carrying the declared size), and a
// tell computes its step from the sample it read, as busyHandoff does.
const testAt = 300_000

func observeAt(t *testing.T, st *Store, a SessionInfo, at time.Time, prompt int64) ContextSample {
	t.Helper()
	c := sample(at, prompt)
	c.HandoffAt = testAt
	if err := st.RecordContext(a.SessionID, a.Incarnation, c); err != nil {
		t.Fatal(err)
	}
	return c
}

// busyTurn is one whole busy: record its own read, read the row back as
// sampleOf does, and claim from what it read.
func busyTurn(t *testing.T, st *Store, a SessionInfo, at time.Time, prompt int64) bool {
	t.Helper()
	observeAt(t, st, a, at, prompt)
	return tellFrom(t, st, a, readRow(t, st, a))
}

func readRow(t *testing.T, st *Store, a SessionInfo) ContextSample {
	t.Helper()
	samples, err := st.ContextSamples()
	if err != nil {
		t.Fatal(err)
	}
	c, ok := samples[a.SessionID]
	if !ok || c.Incarnation != a.Incarnation {
		t.Fatalf("no row for %s's incarnation", a.SessionID)
	}
	return c
}

func tellFrom(t *testing.T, st *Store, a SessionInfo, c ContextSample) bool {
	t.Helper()
	step := int64(-1)
	if c.Prompt >= testAt {
		step = (c.Prompt - testAt) / 25_000
	}
	got, err := st.HandoffTell(a.SessionID, a.Incarnation, c.Prompt, step)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// D-096: the handoff notice is told once per step, the highest step told
// held in session_context.handoff_step. The sequence is one session's life;
// each "false" sits next to a "true" one input away.
func TestHandoffTellOncePerStep(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	// No context row yet: nothing to claim against, and nothing told.
	if tellFrom(t, st, a, sample(clk.t, 300_000)) {
		t.Fatal("no observation, no claim")
	}
	for i, tc := range []struct {
		prompt int64
		want   bool
	}{
		{290_000, false}, // under the size
		{300_000, true},  // the crossing
		{301_000, false}, // the next turn, same step: silent
		{325_000, true},  // grown a step
		{380_000, true},  // grown two at once: told at the new step
		{390_000, false}, // and only once
		{310_000, false}, // a compaction that stays over: a lower step, silent
		{340_000, false}, // growing back below the highest told: silent
		{290_000, false}, // under the size (a compaction): resets
		{300_000, true},  // the next crossing is told
		{310_000, false}, // once
	} {
		clk.advance(time.Minute)
		if got := busyTurn(t, st, a, clk.t, tc.prompt); got != tc.want {
			t.Fatalf("turn %d (%d): told=%v, want %v", i, tc.prompt, got, tc.want)
		}
	}
	// A process that declared no size records a sample under it: no reset,
	// so the told step stands.
	clk.advance(time.Minute)
	undeclared := sample(clk.t, 100_000)
	if err := st.RecordContext(a.SessionID, a.Incarnation, undeclared); err != nil {
		t.Fatal(err)
	}
	clk.advance(time.Minute)
	if busyTurn(t, st, a, clk.t, 300_000) {
		t.Fatal("a sample with no declared size reset a told step")
	}
	// Another incarnation of the same id is told its own crossing: the first
	// RecordContext of the new incarnation resets the column. Before it, the
	// old incarnation's row answers for nobody.
	clk.advance(time.Hour)
	if err := st.Bye(a.SessionID, a.Incarnation); err != nil {
		t.Fatal(err)
	}
	b := hello(t, st, "sess-a", "alpha", "/wt/a")
	if got, _ := st.HandoffTell(b.SessionID, b.Incarnation, 300_000, 5); got {
		t.Fatal("the new incarnation claimed against its predecessor's row")
	}
	if !busyTurn(t, st, b, clk.t, 310_000) {
		t.Fatal("the new incarnation is told its own crossing")
	}
	// And the predecessor, writing late, claims nothing on the new row.
	if got, _ := st.HandoffTell(a.SessionID, a.Incarnation, 310_000, 9); got {
		t.Fatal("a superseded incarnation claimed on its successor's row")
	}
}

// A busy whose sample is stale by the time it records, resets or claims
// moves nothing it should not, and a real dip under the size is never lost
// (Codex design pass and re-verify on schema 16). Each schedule beside the
// fresh call that must still work.
func TestHandoffTellIgnoresAStaleSample(t *testing.T) {
	t.Run("a late under-size sample does not re-arm a told step", func(t *testing.T) {
		st, clk := openTest(t)
		a := hello(t, st, "sess-a", "alpha", "/wt/a")
		t1 := clk.t // A read 290k from its transcript here, then stalled
		clk.advance(time.Minute)
		if !busyTurn(t, st, a, clk.t, 300_000) {
			t.Fatal("B: the crossing")
		}
		late := observeAt(t, st, a, t1, 290_000) // A's record lands late: older turn, not applied
		if tellFrom(t, st, a, late) {
			t.Fatal("A's tell from its stale sample")
		}
		clk.advance(time.Minute)
		if busyTurn(t, st, a, clk.t, 301_000) {
			t.Fatal("C: the same step again, after A's late sample")
		}
	})
	// The busy whose record lands late still reads the row, and the row is
	// newer than what it recorded: it decides from that, and once.
	t.Run("a late record reads the newer row and tells it once", func(t *testing.T) {
		st, clk := openTest(t)
		a := hello(t, st, "sess-a", "alpha", "/wt/a")
		t1 := clk.t
		clk.advance(time.Minute)
		observeAt(t, st, a, clk.t, 300_000) // B recorded, has not claimed yet
		observeAt(t, st, a, t1, 290_000)    // A's record: older, ignored
		if !tellFrom(t, st, a, readRow(t, st, a)) {
			t.Fatal("A read B's untold 300k row: told")
		}
		if tellFrom(t, st, a, readRow(t, st, a)) {
			t.Fatal("B's claim of the same row: once")
		}
	})
	t.Run("a dip whose busy stalls still re-arms the next crossing", func(t *testing.T) {
		st, clk := openTest(t)
		a := hello(t, st, "sess-a", "alpha", "/wt/a")
		if !busyTurn(t, st, a, clk.t, 300_000) {
			t.Fatal("the first crossing")
		}
		clk.advance(time.Minute)
		dip := observeAt(t, st, a, clk.t, 290_000) // A: newest when written; then A stalls
		clk.advance(time.Minute)
		if !busyTurn(t, st, a, clk.t, 300_000) {
			t.Fatal("B: the crossing after the dip must be told")
		}
		if tellFrom(t, st, a, dip) {
			t.Fatal("A's tell from the dip, landing late")
		}
		clk.advance(time.Minute)
		if busyTurn(t, st, a, clk.t, 301_000) {
			t.Fatal("C: told once")
		}
	})
	t.Run("a late claim does not stand over a newer reset", func(t *testing.T) {
		st, clk := openTest(t)
		a := hello(t, st, "sess-a", "alpha", "/wt/a")
		high := observeAt(t, st, a, clk.t, 350_000) // A read step 2 from this, then stalled
		clk.advance(time.Minute)
		observeAt(t, st, a, clk.t, 290_000) // B: compacted
		if tellFrom(t, st, a, high) {
			t.Fatal("A's claim of step 2, landing late")
		}
		clk.advance(time.Minute)
		if !busyTurn(t, st, a, clk.t, 300_000) {
			t.Fatal("the next crossing must be told")
		}
	})
	// RecordContext replaces a row on an EQUAL turn time; the prompt, not the
	// time, is what the step was computed from.
	t.Run("same turn time, another prompt, is another sample", func(t *testing.T) {
		st, clk := openTest(t)
		a := hello(t, st, "sess-a", "alpha", "/wt/a")
		high := observeAt(t, st, a, clk.t, 350_000)
		observeAt(t, st, a, clk.t, 290_000)
		if tellFrom(t, st, a, high) {
			t.Fatal("a claim from the replaced sample")
		}
		clk.advance(time.Minute)
		if !busyTurn(t, st, a, clk.t, 300_000) {
			t.Fatal("the next crossing must be told")
		}
	})
}

// Two prompts queued together run two busy hooks at once; exactly one may
// print. The claim is one conditional UPDATE, so the race has one winner.
func TestHandoffTellConcurrentHasOneWinner(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	cur := sample(clk.t, 300_000)
	if err := st.RecordContext(a.SessionID, a.Incarnation, cur); err != nil {
		t.Fatal(err)
	}
	const n = 8
	var wg sync.WaitGroup
	won := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := st.HandoffTell(a.SessionID, a.Incarnation, cur.Prompt, 0)
			if err != nil {
				t.Error(err)
			}
			won <- got
		}()
	}
	wg.Wait()
	close(won)
	winners := 0
	for w := range won {
		if w {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("%d of %d concurrent claims were told, want exactly 1", winners, n)
	}
}

// Schema 16's ALTER arm: a ledger from 15, with a context row, gains the
// column at -1 (nothing told), keeps the row, and claims against it.
func TestHandoffStepMigratesFromV15(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	st, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	cur := sample(time.Now(), 300_000)
	if err := st.RecordContext(a.SessionID, a.Incarnation, cur); err != nil {
		t.Fatal(err)
	}
	st.Close()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// Rebuilt in its v15 shape rather than by DROP COLUMN, which cannot parse
	// a table whose text carries a comment before the dropped column ("incomplete
	// input", as batch_test.go found for claims).
	const v15 = `CREATE TABLE sc15 (session_id TEXT PRIMARY KEY, incarnation TEXT NOT NULL,
		observed INTEGER NOT NULL, turn_ms INTEGER NOT NULL, model TEXT NOT NULL,
		effort TEXT NOT NULL DEFAULT '', prompt INTEGER NOT NULL, cache_read INTEGER NOT NULL,
		cache_write INTEGER NOT NULL, output INTEGER NOT NULL, declared_window INTEGER NOT NULL DEFAULT 0,
		cache_5m INTEGER NOT NULL DEFAULT 0, cache_1h INTEGER NOT NULL DEFAULT 0, tier_ms INTEGER NOT NULL DEFAULT 0)`
	for _, q := range []string{v15,
		`INSERT INTO sc15 SELECT session_id, incarnation, observed, turn_ms, model, effort, prompt, cache_read, cache_write, output, declared_window, cache_5m, cache_1h, tier_ms FROM session_context`,
		`DROP TABLE session_context`, `ALTER TABLE sc15 RENAME TO session_context`, `PRAGMA user_version = 15`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	st, err = Open(path, nil)
	if err != nil {
		t.Fatalf("a v15 ledger must migrate: %v", err)
	}
	defer st.Close()
	var ver, step int64
	if err := st.db.QueryRow(`PRAGMA user_version`).Scan(&ver); err != nil || ver != schemaVersion {
		t.Fatalf("want user_version %d, got %d (%v)", schemaVersion, ver, err)
	}
	if err := st.db.QueryRow(`SELECT handoff_step FROM session_context WHERE session_id=?`, a.SessionID).Scan(&step); err != nil || step != -1 {
		t.Fatalf("the migrated row must read -1 (nothing told), got %d (%v)", step, err)
	}
	if got, err := st.HandoffTell(a.SessionID, a.Incarnation, cur.Prompt, 0); err != nil || !got {
		t.Fatalf("the migrated row must take a claim: told=%v err=%v", got, err)
	}
}
