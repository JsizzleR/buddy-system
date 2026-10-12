package store

// These fixtures keep the adapter's OLD token across end/begin. Re-reading the
// live row before replaying an event would test the lookup bug, not its fence.
// Positive controls assert each event's effects before refusing delayed copies.

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func beginLogical(t *testing.T, st *Store, id, previous string) SessionInfo {
	t.Helper()
	si, err := st.BeginLogical(id, "", "/wt/logical", previous)
	if err != nil {
		t.Fatal(err)
	}
	return si
}

func logicalInfo(t *testing.T, st *Store, id string) SessionInfo {
	t.Helper()
	si, found, err := st.SessionByID(id)
	if err != nil || !found {
		t.Fatalf("session %q: found=%v err=%v", id, found, err)
	}
	return si
}

func TestBeginLogicalRequiresPreviousIncarnation(t *testing.T) {
	st, clk := openTest(t)
	for _, tc := range []struct{ id, previous string }{{"", ""}, {"missing", "old"}} {
		if _, err := st.BeginLogical(tc.id, "", "/wt/logical", tc.previous); !errors.Is(err, ErrLogicalIdentity) {
			t.Fatalf("begin(%q,%q): want identity refusal, got %v", tc.id, tc.previous, err)
		}
	}
	first := beginLogical(t, st, "logical", "")
	if first.Incarnation == "" || !first.Live() || first.PID != 0 || first.Terminal != "" {
		t.Fatalf("new logical registration: %+v", first)
	}
	if first.Label != "logical/s-logical" {
		t.Fatalf("default label = %q", first.Label)
	}
	if err := st.Claim(first.SessionID, first.Incarnation, "old-claim", "", []string{"pkg"}); err != nil {
		t.Fatal(err)
	}
	clk.advance(time.Minute)
	for _, token := range []string{"", first.Incarnation, "wrong"} {
		if _, err := st.BeginLogical(first.SessionID, "takeover", "/wt/other", token); !errors.Is(err, ErrLogicalIdentity) {
			t.Fatalf("live begin(%q): want refusal, got %v", token, err)
		}
		if got := logicalInfo(t, st, first.SessionID); got != first {
			t.Fatalf("refused live begin changed session: %+v", got)
		}
	}
	if err := st.EndLogical(first.SessionID, first.Incarnation); err != nil {
		t.Fatal(err)
	}
	ended := logicalInfo(t, st, first.SessionID)
	for _, token := range []string{"", "wrong"} {
		if _, err := st.BeginLogical(first.SessionID, "takeover", "/wt/other", token); !errors.Is(err, ErrLogicalIdentity) {
			t.Fatalf("ended begin(%q): want refusal, got %v", token, err)
		}
		if got := logicalInfo(t, st, first.SessionID); got != ended {
			t.Fatalf("refused restart changed session: %+v", got)
		}
		claims, err := st.Claims(false)
		if err != nil || len(claims) != 1 || claims[0].State != "open" {
			t.Fatalf("refused restart performed cleanup: %+v, %v", claims, err)
		}
	}
	second := beginLogical(t, st, first.SessionID, first.Incarnation)
	if second.Incarnation == first.Incarnation || !second.Live() {
		t.Fatalf("restart reused identity: %+v", second)
	}
	claims, err := st.Claims(true)
	if err != nil || len(claims) != 1 || claims[0].State != "orphaned" {
		t.Fatalf("restart did not orphan old claims: %+v, %v", claims, err)
	}
	if err := st.EndLogical(second.SessionID, second.Incarnation); err != nil {
		t.Fatal(err)
	}
	// A begin delayed past TWO generations must not revive the newest ended
	// one, even though both old and new rows are now ended.
	if _, err := st.BeginLogical(first.SessionID, "", "/wt/logical", first.Incarnation); !errors.Is(err, ErrLogicalIdentity) {
		t.Fatalf("delayed restart accepted old previous token: %v", err)
	}
	third := beginLogical(t, st, second.SessionID, second.Incarnation)
	if third.Incarnation == second.Incarnation {
		t.Fatal("positive restart control did not mint a third incarnation")
	}
}

func TestRefreshLogicalKeepsIdentityAndIdle(t *testing.T) {
	st, clk := openTest(t)
	first := beginLogical(t, st, "logical", "")
	if err := st.IdleLogical(first.SessionID, first.Incarnation, time.Time{}); err != nil {
		t.Fatal(err)
	}
	clk.advance(time.Minute)
	refreshed, err := st.RefreshLogical(first.SessionID, first.Incarnation, "adapter", "/wt/new")
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Incarnation != first.Incarnation || refreshed.Started != first.Started || refreshed.Label != "adapter" || refreshed.Worktree != "/wt/new" || !refreshed.LastSeen.Equal(clk.now()) {
		t.Fatalf("refresh: %+v", refreshed)
	}
	idle, err := st.IdleSessions()
	if err != nil || idle[first.SessionID].Incarnation != first.Incarnation || !idle[first.SessionID].Since.Equal(first.Started) {
		t.Fatalf("refresh cleared idle: %+v, %v", idle, err)
	}
	retry, err := st.RefreshLogical(first.SessionID, first.Incarnation, "", "/wt/new")
	if err != nil || retry != refreshed {
		t.Fatalf("same-token retry changed identity or label: %+v, %v", retry, err)
	}
	procs, err := st.SessionProcs()
	if err != nil || len(procs) != 0 {
		t.Fatalf("logical session registered a process: %+v, %v", procs, err)
	}
}

func logicalActions(st *Store) []struct {
	name string
	run  func(string, string) error
} {
	return []struct {
		name string
		run  func(string, string) error
	}{
		{"refresh", func(id, inc string) error { _, err := st.RefreshLogical(id, inc, "late", "/late"); return err }},
		{"beat", func(id, inc string) error { return st.BeatLogical(id, inc, "pkg/a.go") }},
		{"busy", st.BusyLogical},
		{"idle", func(id, inc string) error { return st.IdleLogical(id, inc, time.Time{}) }},
		{"end", st.EndLogical},
	}
}

func TestLogicalEventsRefuseStaleIdentity(t *testing.T) {
	for _, action := range []string{"refresh", "beat", "busy", "idle", "end"} {
		for _, tokenKind := range []string{"missing", "old", "foreign", "unknown-session"} {
			t.Run(action+"/"+tokenKind, func(t *testing.T) {
				st, clk := openTest(t)
				old := beginLogical(t, st, "logical", "")
				if err := st.EndLogical(old.SessionID, old.Incarnation); err != nil {
					t.Fatal(err)
				}
				current := beginLogical(t, st, old.SessionID, old.Incarnation)
				foreign := beginLogical(t, st, "foreign", "")
				if err := st.Claim(current.SessionID, current.Incarnation, "held", "", []string{"pkg"}); err != nil {
					t.Fatal(err)
				}
				if err := st.IdleLogical(current.SessionID, current.Incarnation, time.Time{}); err != nil {
					t.Fatal(err)
				}
				beforeClaims, err := st.Claims(false)
				if err != nil {
					t.Fatal(err)
				}
				beforeIdle, err := st.IdleSessions()
				if err != nil {
					t.Fatal(err)
				}
				clk.advance(time.Minute)
				id, token := current.SessionID, ""
				switch tokenKind {
				case "old":
					token = old.Incarnation
				case "foreign":
					token = foreign.Incarnation
				case "unknown-session":
					id, token = "unknown", current.Incarnation
				}
				for _, candidate := range logicalActions(st) {
					if candidate.name == action {
						if err := candidate.run(id, token); !errors.Is(err, ErrLogicalIdentity) {
							t.Fatalf("delayed %s: want identity refusal, got %v", action, err)
						}
					}
				}
				if got := logicalInfo(t, st, current.SessionID); got != current {
					t.Fatalf("delayed %s changed successor: %+v", action, got)
				}
				claims, err := st.Claims(false)
				if err != nil || !reflect.DeepEqual(claims, beforeClaims) {
					t.Fatalf("delayed %s changed successor claims: %+v, %v", action, claims, err)
				}
				idle, err := st.IdleSessions()
				if err != nil || !reflect.DeepEqual(idle, beforeIdle) {
					t.Fatalf("delayed %s changed successor idle: %+v, %v", action, idle, err)
				}
			})
		}
	}
}

func TestLogicalEndedEventsDoNotRevive(t *testing.T) {
	st, clk := openTest(t)
	first := beginLogical(t, st, "logical", "")
	if err := st.EndLogical(first.SessionID, first.Incarnation); err != nil {
		t.Fatal(err)
	}
	ended := logicalInfo(t, st, first.SessionID)
	clk.advance(time.Minute)
	for _, action := range logicalActions(st) {
		err := action.run(first.SessionID, first.Incarnation)
		if action.name == "end" {
			if err != nil {
				t.Fatalf("repeated matching end: %v", err)
			}
		} else if !errors.Is(err, ErrLogicalIdentity) {
			t.Fatalf("ended %s: want refusal, got %v", action.name, err)
		}
		if got := logicalInfo(t, st, first.SessionID); got != ended {
			t.Fatalf("ended %s changed ended row: %+v", action.name, got)
		}
	}
}

func TestLogicalEventsPositiveControls(t *testing.T) {
	st, clk := openTest(t)
	current := beginLogical(t, st, "logical", "")
	for _, scope := range []string{"pkg", "docs"} {
		if err := st.Claim(current.SessionID, current.Incarnation, scope, "", []string{scope}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.IdleLogical(current.SessionID, current.Incarnation, time.Time{}); err != nil {
		t.Fatal(err)
	}
	clk.advance(time.Minute)
	if err := st.BusyLogical(current.SessionID, current.Incarnation); err != nil {
		t.Fatal(err)
	}
	idle, err := st.IdleSessions()
	if err != nil || len(idle) != 0 || !logicalInfo(t, st, current.SessionID).LastSeen.Equal(clk.now()) {
		t.Fatalf("busy did not clear idle and refresh seen: %+v, %v", idle, err)
	}
	claims, err := st.Claims(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, claim := range claims {
		if !claim.Renewed.Equal(current.Started) {
			t.Fatalf("busy renewed claim %q", claim.Slug)
		}
	}
	if err := st.IdleLogical(current.SessionID, current.Incarnation, clk.now().Add(-10*time.Second)); err != nil {
		t.Fatal(err)
	}
	idle, err = st.IdleSessions()
	if err != nil || !idle[current.SessionID].Since.Equal(clk.now().Add(-10*time.Second)) {
		t.Fatalf("idle discarded report date: %+v, %v", idle, err)
	}
	clk.advance(time.Minute)
	if err := st.BeatLogical(current.SessionID, current.Incarnation, "PKG/a.go"); err != nil {
		t.Fatal(err)
	}
	idle, err = st.IdleSessions()
	if err != nil || len(idle) != 0 || !logicalInfo(t, st, current.SessionID).LastSeen.Equal(clk.now()) {
		t.Fatalf("beat did not clear idle and refresh seen: %+v, %v", idle, err)
	}
	claims, err = st.Claims(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, claim := range claims {
		want := current.Started
		if claim.Slug == "pkg" {
			want = clk.now()
		}
		if !claim.Renewed.Equal(want) {
			t.Fatalf("claim %q renewed at %s; want %s", claim.Slug, claim.Renewed, want)
		}
	}
	if err := st.EndLogical(current.SessionID, current.Incarnation); err != nil {
		t.Fatal(err)
	}
	if logicalInfo(t, st, current.SessionID).Live() {
		t.Fatal("matching end did not end session")
	}
	claims, err = st.Claims(false)
	if err != nil || len(claims) != 2 {
		t.Fatalf("end removed claims: %+v, %v", claims, err)
	}
	for _, claim := range claims {
		if claim.State != "open" {
			t.Fatalf("end orphaned claim inline: %+v", claim)
		}
	}
}

func TestBeatLogicalRollsBackEveryEffect(t *testing.T) {
	st, clk := openTest(t)
	current := beginLogical(t, st, "logical", "")
	if err := st.Claim(current.SessionID, current.Incarnation, "held", "", []string{"pkg"}); err != nil {
		t.Fatal(err)
	}
	if err := st.IdleLogical(current.SessionID, current.Incarnation, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`CREATE TRIGGER refuse_renew BEFORE UPDATE OF renewed ON claims BEGIN SELECT RAISE(ABORT, 'injected renewal failure'); END`); err != nil {
		t.Fatal(err)
	}
	clk.advance(time.Minute)
	if err := st.BeatLogical(current.SessionID, current.Incarnation, "pkg/a.go"); err == nil {
		t.Fatal("injected claim renewal failure did not fail beat")
	}
	if got := logicalInfo(t, st, current.SessionID); got != current {
		t.Fatalf("failed beat committed last_seen: %+v", got)
	}
	idle, err := st.IdleSessions()
	if err != nil || idle[current.SessionID].Incarnation != current.Incarnation {
		t.Fatalf("failed beat cleared idle: %+v, %v", idle, err)
	}
	if _, err := st.db.Exec(`DROP TRIGGER refuse_renew`); err != nil {
		t.Fatal(err)
	}
	if err := st.BeatLogical(current.SessionID, current.Incarnation, "pkg/a.go"); err != nil {
		t.Fatalf("renewal positive control: %v", err)
	}
}

func TestBeginLogicalConcurrentRestartHasOneWinner(t *testing.T) {
	st, _ := openTest(t)
	old := beginLogical(t, st, "logical", "")
	if err := st.EndLogical(old.SessionID, old.Incarnation); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			<-start
			_, err := st.BeginLogical(old.SessionID, "", "/wt/logical", old.Incarnation)
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrLogicalIdentity) {
			t.Fatalf("concurrent restart: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent restart winners = %d, want 1", winners)
	}
}
