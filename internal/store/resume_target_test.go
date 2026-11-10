package store

import (
	"errors"
	"fmt"
	"testing"
)

// #86, D-139's open items on the brake's resume side.
//
//	(1) Resume cleared rows on the resolved LABEL unconditionally, a fix for
//	    pre-D-013 rows (raw typed target) resting on "ResolveTarget refuses a
//	    label more than one session wears", which since D-139 holds only among
//	    live sessions. A row on a name brakes every session wearing it
//	    (PausedFor's label arm), so `resume <id>` lifted brakes on sessions the
//	    operator did not name: a live or ended twin's, the fleet-wide pause
//	    for a session labelled `all`, and another session's own pause for one
//	    labelled with that session's id (Codex design pass).
//	(2) `pause fix-74` reaches the live slug holder; once the slug is
//	    released, `resume fix-74` reaches the ended lane by its label and
//	    clears nothing, and the result named nobody.
//	(3) Resolution and write were two transactions.
//
// Each refusal or keep below has a positive control beside it: the same
// resume lifting the row once nothing else wears the name.

// legacyPause writes exactly what the pre-D-013 Pause wrote: the typed word.
func legacyPause(t *testing.T, st *Store, word string) {
	t.Helper()
	if _, err := st.db.Exec(`INSERT INTO controls (kind, target, note, created) VALUES ('pause',?,?,?)`,
		word, "legacy hold", st.now().Unix()); err != nil {
		t.Fatal(err)
	}
}

func resumeWord(t *testing.T, st *Store, word string) ResumeResult {
	t.Helper()
	r, err := st.Resume(mustResolve(t, st, word))
	if err != nil {
		t.Fatalf("Resume(%q): %v", word, err)
	}
	return r
}

func wantPaused(t *testing.T, st *Store, si SessionInfo, want bool, why string) {
	t.Helper()
	_, paused, err := st.PausedFor(si.SessionID, si.Label)
	if err != nil {
		t.Fatal(err)
	}
	if paused != want {
		t.Fatalf("%s (%s): paused=%v, want %v", si.SessionID, why, paused, want)
	}
}

// (1), live twins: `resume <id>` of one wearer must not lift a legacy row
// that still brakes the other.
func TestResumeByIDKeepsALegacyLabelRowATwinShares(t *testing.T) {
	for _, endTwin := range []bool{false, true} {
		t.Run(fmt.Sprintf("twinEnded=%v", endTwin), func(t *testing.T) {
			st, _ := openTest(t)
			a := hello(t, st, "sess-a", "alpha", "/tmp/a")
			b := hello(t, st, "sess-b", "alpha", "/tmp/b")
			if endTwin {
				if err := st.Bye(b.SessionID, b.Incarnation); err != nil {
					t.Fatal(err)
				}
			}
			legacyPause(t, st, "alpha")
			wantPaused(t, st, a, true, "control: the legacy row brakes a by label")
			wantPaused(t, st, b, true, "control: the legacy row brakes b by label")

			r := resumeWord(t, st, a.SessionID)
			if r.Cleared != 0 {
				t.Fatalf("resume %s cleared %d, want 0: the row on alpha also brakes %s", a.SessionID, r.Cleared, b.SessionID)
			}
			wantPaused(t, st, b, true, "the twin's brake must stay on")
			wantPaused(t, st, a, true, "the row on the shared name still brakes a")
			if len(r.Kept) != 1 || r.Kept[0].Target != "alpha" || r.Kept[0].Wearers != 1 || r.Kept[0].Rows != 1 {
				t.Fatalf("Kept = %+v, want the alpha row with 1 other wearer", r.Kept)
			}

			if !endTwin {
				return
			}
			// With the twin ENDED, `alpha` resolves to the live wearer, and
			// typing the row's own text lifts it, for every wearer: the
			// operator named exactly the brake that was set.
			r = resumeWord(t, st, "alpha")
			if r.Cleared != 1 || r.Target.ID != a.SessionID {
				t.Fatalf("resume alpha = %+v, want 1 cleared for %s", r, a.SessionID)
			}
			wantPaused(t, st, a, false, "resume of the row's own text")
			wantPaused(t, st, b, false, "the row was on the name")
		})
	}
}

// Positive control for (1): the label arm still lifts a legacy row through
// any alias while the label is the session's alone (C-4 in target_test.go
// does it by slug; this by id and short form).
func TestResumeByIDLiftsALegacyRowOnALabelNobodyElseWears(t *testing.T) {
	for _, word := range []string{"sess-a", "s-0a1b2c3d"} {
		t.Run(word, func(t *testing.T) {
			st, _ := openTest(t)
			a := hello(t, st, "sess-a", "wt/s-0a1b2c3d", "/tmp/a")
			hello(t, st, "sess-b", "bravo", "/tmp/b")
			legacyPause(t, st, a.Label)
			r := resumeWord(t, st, word)
			if r.Cleared != 1 || len(r.Kept) != 0 {
				t.Fatalf("resume %s = %+v, want 1 cleared and nothing kept", word, r)
			}
			wantPaused(t, st, a, false, "the label was a's alone")
		})
	}
}

// (1), a session labelled `all`: its resume must not lift the fleet brake.
func TestResumeOfASessionLabelledAllKeepsTheFleetPause(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", AllTarget, "/tmp/a")
	b := hello(t, st, "sess-b", "bravo", "/tmp/b")
	if _, err := st.Pause(mustResolve(t, st, AllTarget), "everyone stop"); err != nil {
		t.Fatal(err)
	}
	wantPaused(t, st, b, true, "control: the fleet pause brakes b")

	r := resumeWord(t, st, a.SessionID)
	if r.Cleared != 0 {
		t.Fatalf("resume %s cleared %d, want 0", a.SessionID, r.Cleared)
	}
	wantPaused(t, st, b, true, "the fleet pause must stand")
	if len(r.Kept) != 1 || r.Kept[0].Target != AllTarget {
		t.Fatalf("Kept = %+v, want the all row", r.Kept)
	}

	// Positive control: `resume all` lifts it.
	if r := resumeWord(t, st, AllTarget); r.Cleared != 1 {
		t.Fatalf("resume all = %+v, want 1 cleared", r)
	}
	wantPaused(t, st, b, false, "resume all")
}

// (1), a label equal to another session's id: resuming the wearer must not
// lift that session's own pause.
func TestResumeKeepsAPauseOnALabelThatIsAnotherSessionsID(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/tmp/a")
	b := hello(t, st, "sess-b", "bravo", "/tmp/b")
	if _, err := st.Pause(mustResolve(t, st, b.SessionID), "b stop"); err != nil {
		t.Fatal(err)
	}
	a = hello(t, st, a.SessionID, b.SessionID, "/tmp/a") // a relabels itself as b's id
	if a.Label != b.SessionID {
		t.Fatalf("fixture: relabel did not take, label %q", a.Label)
	}
	wantPaused(t, st, b, true, "control")

	r := resumeWord(t, st, a.SessionID)
	if r.Cleared != 0 {
		t.Fatalf("resume %s cleared %d, want 0: the row is %s's own", a.SessionID, r.Cleared, b.SessionID)
	}
	wantPaused(t, st, b, true, "b's own pause must stand")
	if len(r.Kept) != 1 || r.Kept[0].Target != b.SessionID || r.Kept[0].Wearers != 1 {
		t.Fatalf("Kept = %+v, want b's row, with b as the other wearer", r.Kept)
	}
}

// (2): the word moved between pause and resume. The result names the session
// it reached and the pause still standing elsewhere; the brake stays on.
func TestResumeNamesWhomItReachedAndThePauseStillStanding(t *testing.T) {
	st, _ := openTest(t)
	old, next := handOn(t, st, "fix-74")
	if _, err := st.Pause(mustResolve(t, st, "fix-74"), "stop"); err != nil {
		t.Fatal(err)
	}
	wantPaused(t, st, next, true, "control: pause by slug reached the live holder")
	if err := st.Release(next.SessionID, next.Incarnation, "fix-74"); err != nil {
		t.Fatal(err)
	}

	r := resumeWord(t, st, "fix-74")
	if r.Target.ID != old.SessionID || r.Target.Live || r.Target.Via != "label" {
		t.Fatalf("resume fix-74 reached %+v, want the ended %s by label", r.Target, old.SessionID)
	}
	if r.Cleared != 0 || len(r.Kept) != 0 {
		t.Fatalf("resume fix-74 = %+v, want nothing cleared or kept for the ended lane", r)
	}
	if len(r.Others) != 1 || r.Others[0].Target != next.SessionID || r.Others[0].Label != next.Label || r.OthersMore != 0 {
		t.Fatalf("Others = %+v (+%d), want the pause on %s (%s)", r.Others, r.OthersMore, next.SessionID, next.Label)
	}
	wantPaused(t, st, next, true, "the brake stays on")

	// Positive control: the id the result named lifts it, and a resume that
	// clears something lists no others.
	r = resumeWord(t, st, next.SessionID)
	if r.Cleared != 1 || len(r.Others) != 0 {
		t.Fatalf("resume %s = %+v, want 1 cleared and no others", next.SessionID, r)
	}
	wantPaused(t, st, next, false, "resumed by the named id")
}

func TestResumeOthersIsBounded(t *testing.T) {
	st, _ := openTest(t)
	hello(t, st, "sess-idle", "idle", "/tmp/idle")
	for i := 0; i < ResumeOthersMax+2; i++ {
		si := hello(t, st, fmt.Sprintf("sess-%02d", i), fmt.Sprintf("lane-%02d", i), "/tmp/x")
		if _, err := st.Pause(mustResolve(t, st, si.SessionID), ""); err != nil {
			t.Fatal(err)
		}
	}
	r := resumeWord(t, st, "idle")
	if len(r.Others) != ResumeOthersMax || r.OthersMore != 2 {
		t.Fatalf("Others %d (+%d), want %d (+2)", len(r.Others), r.OthersMore, ResumeOthersMax)
	}
	// A resume that cleared something lists no others, though others stand
	// (mutation check: the clause was untested).
	r = resumeWord(t, st, "sess-00")
	if r.Cleared != 1 || len(r.Others) != 0 || r.OthersMore != 0 {
		t.Fatalf("resume sess-00 = %d cleared, Others %d (+%d), want 1 and none", r.Cleared, len(r.Others), r.OthersMore)
	}
}

// `resume all` lifts the all rows and a legacy row on the empty string (no
// other word reaches it), and nothing targeted.
func TestResumeAllLiftsOnlyTheFleetRows(t *testing.T) {
	st, _ := openTest(t)
	b := hello(t, st, "sess-b", "bravo", "/tmp/b")
	if _, err := st.Pause(mustResolve(t, st, b.SessionID), "b stop"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pause(mustResolve(t, st, AllTarget), "all stop"); err != nil {
		t.Fatal(err)
	}
	legacyPause(t, st, "")
	_, unregistered, err := st.PausedFor("sess-nobody", "")
	if err != nil || !unregistered {
		t.Fatalf("control: the empty-name row should brake an unregistered caller: %v %v", unregistered, err)
	}
	r := resumeWord(t, st, AllTarget)
	if r.Cleared != 2 || len(r.Kept) != 0 {
		t.Fatalf("resume all = %+v, want 2 cleared (all and the empty name)", r)
	}
	wantPaused(t, st, b, true, "b's own pause is not the fleet's")
	if _, p, _ := st.PausedFor("sess-nobody", ""); p {
		t.Fatal("the empty-name row survived resume all")
	}
}

// (3): the word is resolved again inside the write, and a word that now names
// another session refuses and writes nothing.
func TestBrakeRefusesAWordThatMovedBeforeItsWrite(t *testing.T) {
	setup := func(t *testing.T) (*Store, SessionInfo, Target) {
		st, _ := openTest(t)
		c := hello(t, st, "sess-c", "charlie", "/tmp/c")
		if err := st.Claim(c.SessionID, c.Incarnation, "x", "work", []string{"internal/x"}); err != nil {
			t.Fatal(err)
		}
		tgt := mustResolve(t, st, "x")
		if tgt.ID != c.SessionID || tgt.Via != "slug" {
			t.Fatalf("fixture: x resolved to %+v", tgt)
		}
		return st, c, tgt
	}
	controls := func(t *testing.T, st *Store) int {
		var n int
		if err := st.db.QueryRow(`SELECT COUNT(*) FROM controls WHERE cleared IS NULL`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// Each move makes `x` mean something else after it resolved to c.
	moves := map[string]func(t *testing.T, st *Store){
		"a live session now wears x": func(t *testing.T, st *Store) { hello(t, st, "sess-d", "x", "/tmp/d") },
		"two live sessions now wear x": func(t *testing.T, st *Store) {
			hello(t, st, "sess-d", "x", "/tmp/d")
			hello(t, st, "sess-e", "x", "/tmp/e")
		},
		"x was released": func(t *testing.T, st *Store) {
			if err := st.Release("sess-c", mustResolve(t, st, "sess-c").Incarnation, "x"); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, move := range moves {
		t.Run("pause/"+name, func(t *testing.T) {
			st, c, tgt := setup(t)
			move(t, st)
			if _, err := st.Pause(tgt, "stop"); !errors.Is(err, ErrTargetMoved) {
				t.Fatalf("Pause after %s: %v, want ErrTargetMoved", name, err)
			}
			if n := controls(t, st); n != 0 {
				t.Fatalf("a refused pause wrote %d row(s)", n)
			}
			wantPaused(t, st, c, false, "nothing was written")
		})
		t.Run("resume/"+name, func(t *testing.T) {
			st, c, tgt := setup(t)
			if _, err := st.Pause(mustResolve(t, st, c.SessionID), "stop"); err != nil {
				t.Fatal(err)
			}
			move(t, st)
			if _, err := st.Resume(tgt); !errors.Is(err, ErrTargetMoved) {
				t.Fatalf("Resume after %s: %v, want ErrTargetMoved", name, err)
			}
			wantPaused(t, st, c, true, "a refused resume lifts nothing")
		})
	}

	// Positive controls: unmoved, both act; and the same session under a new
	// incarnation is still the same answer.
	t.Run("unmoved", func(t *testing.T) {
		st, c, tgt := setup(t)
		if _, err := st.Pause(tgt, "stop"); err != nil {
			t.Fatal(err)
		}
		wantPaused(t, st, c, true, "pause")
		if r, err := st.Resume(tgt); err != nil || r.Cleared != 1 {
			t.Fatalf("Resume unmoved: %+v %v", r, err)
		}
	})
	// The returned target is the one read inside the write: the session ended
	// between resolution and the write, and both verbs report it ENDED
	// (Codex code pass: a stale return passed every other case).
	t.Run("ended in between", func(t *testing.T) {
		st, c, _ := setup(t)
		tgt := mustResolve(t, st, c.Label)
		if !tgt.Live {
			t.Fatal("fixture: c should resolve live")
		}
		if err := st.Bye(c.SessionID, c.Incarnation); err != nil {
			t.Fatal(err)
		}
		got, err := st.Pause(tgt, "stop")
		if err != nil || got.ID != c.SessionID || got.Live {
			t.Fatalf("Pause after bye = %+v, %v; want %s, not live", got, err, c.SessionID)
		}
		r, err := st.Resume(tgt)
		if err != nil || r.Target.Live || r.Cleared != 1 {
			t.Fatalf("Resume after bye = %+v, %v; want 1 cleared, not live", r, err)
		}
	})
	t.Run("reincarnated", func(t *testing.T) {
		st, c, _ := setup(t)
		tgt := mustResolve(t, st, c.Label)
		if err := st.Bye(c.SessionID, c.Incarnation); err != nil {
			t.Fatal(err)
		}
		c2 := hello(t, st, c.SessionID, c.Label, "/tmp/c")
		if c2.Incarnation == c.Incarnation {
			t.Fatal("fixture: expected a new incarnation")
		}
		if _, err := st.Pause(tgt, "stop"); err != nil {
			t.Fatalf("Pause of the same session, new incarnation: %v", err)
		}
		wantPaused(t, st, c2, true, "same session")
	})
}
