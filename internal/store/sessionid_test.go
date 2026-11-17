package store

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

// The wrapper agrees with the predicate both ways, and its refusal is one line
// that names the id one token wide whatever the id holds.
func TestStoreValidSessionID(t *testing.T) {
	for _, id := range []string{"07f63349-294a-489d-8f9b-79a9cb7b37a9", "sess-a"} {
		if err := ValidSessionID(id); err != nil {
			t.Fatalf("control %q refused: %v", id, err)
		}
	}
	for _, id := range []string{"", "sess x", "a\nb", "s-1234abcd", "all", strings.Repeat("a", 129), "x‮y"} {
		err := ValidSessionID(id)
		if !errors.Is(err, ErrSessionIDRefused) {
			t.Fatalf("%q: want ErrSessionIDRefused, got %v", id, err)
		}
		if strings.ContainsAny(err.Error(), "\n\r‮") {
			t.Fatalf("%q: the refusal is not one fenced line: %q", id, err)
		}
	}
}

// oldShortCut is the default label's id half before D-201, kept here only to
// prove ShortID equals it for every id the ledger now admits.
func oldShortCut(id string) string {
	id = strings.ReplaceAll(id, "-", "")
	if len(id) > 8 {
		id = id[:8]
	}
	return "s-" + id
}

func TestShortID(t *testing.T) {
	// Byte-identical to the old cut for admitted ids: the measured native
	// shapes, the suite's ids, and one with every kept punctuation mark.
	for _, id := range []string{"07f63349-294a-489d-8f9b-79a9cb7b37a9", "019a7f3e-5b2c-7d41-9e0f-123456789abc",
		"sess-a", "X", "worker-label", "a.b_c-d.e_f", "manual-x"} {
		if got, want := ShortID(id), oldShortCut(id); got != want {
			t.Errorf("ShortID(%q) = %q, the old cut %q", id, got, want)
		}
	}
	for _, c := range []struct{ id, want string }{
		{"ééééabcdefghij", "s-abcdefgh"}, // the old cut split é
		{"ab​cd‮ef gh/ij", "s-abcdefgh"},
		{"a\nb\tc", "s-abc"},
		{"é", "s-?"},
		{"", "s-?"},
		{"----", "s-?"},
	} {
		if got := ShortID(c.id); got != c.want {
			t.Errorf("ShortID(%q) = %q, want %q", c.id, got, c.want)
		}
	}
}

// scripts/leftover-report.py keeps a Python copy of ShortID (it reads a copy
// of the ledger with no buddy binary to ask), so both are held to one vector
// file: this test and that script's --self-test each read it (D-204). A
// vector whose want is not ShortID's fails here; a Python copy that drifts
// fails there.
func TestShortIDSharedVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/shortid.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors [][2]string
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) < 10 {
		t.Fatalf("%d vectors: the file holds at least 10", len(vectors))
	}
	for _, v := range vectors {
		if got := ShortID(v[0]); got != v[1] {
			t.Errorf("ShortID(%q) = %q, the vector says %q", v[0], got, v[1])
		}
	}
}

// Each insert arm refuses an id outside the predicate and writes nothing; the
// controls register. A row an older binary wrote under such an id still
// refreshes, revives and is found (no migration, invariant 12's checks intact).
func TestInsertArmsRefuseUnregistrableIDs(t *testing.T) {
	st, _ := openTest(t)
	for _, id := range []string{"sess x", "a\nb", "x​y", "s-1234abcd", "all", "-x", strings.Repeat("a", 129), "wt/s-1"} {
		if _, err := st.Hello(id, "", "/w", 0); !errors.Is(err, ErrSessionIDRefused) {
			t.Errorf("Hello(%q): want ErrSessionIDRefused, got %v", id, err)
		}
		if _, err := st.BeginLogical(id, "", "/w", ""); !errors.Is(err, ErrSessionIDRefused) {
			t.Errorf("BeginLogical(%q): want ErrSessionIDRefused, got %v", id, err)
		}
		if _, ok, err := st.Session(id); err != nil || ok {
			t.Errorf("%q: a refused id left a row (ok=%v err=%v)", id, ok, err)
		}
	}
	if _, err := st.Hello("sess-a", "", "/w/repo", 0); err != nil {
		t.Fatalf("control hello: %v", err)
	}
	if si, err := st.BeginLogical("019a7f3e-5b2c-7d41-9e0f-123456789abc", "", "/w/repo", ""); err != nil || si.Label != "repo/s-019a7f3e" {
		t.Fatalf("control begin: %+v %v", si, err)
	}

	// An older binary's row: written by SQL as that binary would have.
	const legacy = "old id\twith tab"
	if _, err := st.db.Exec(`INSERT INTO sessions (session_id, incarnation, label, worktree, pid, terminal, started, last_seen) VALUES (?,?,?,?,0,'',1,1)`,
		legacy, "inc0", "legacy", "/w"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Hello(legacy, "", "/w", 0); err != nil {
		t.Fatalf("a legacy row must still refresh: %v", err)
	}
	if err := st.Bye(legacy, ""); err != nil {
		t.Fatal(err)
	}
	if si, err := st.Hello(legacy, "", "/w", 0); err != nil || !si.Live() {
		t.Fatalf("a legacy row must still revive: %+v %v", si, err)
	}
	// A legacy ENDED logical row revived with its exact previous token.
	if _, err := st.db.Exec(`INSERT INTO sessions (session_id, incarnation, label, worktree, pid, terminal, started, last_seen, ended) VALUES (?,?,?,?,0,'',1,1,2)`,
		"old:logical", "prev-inc", "ll", "/w"); err != nil {
		t.Fatal(err)
	}
	if si, err := st.BeginLogical("old:logical", "", "/w", "prev-inc"); err != nil || si.SessionID != "old:logical" {
		t.Fatalf("a legacy ended logical row must revive by its token: %+v %v", si, err)
	}
}

// The ambiguity list renders each label and id one token wide (invariant 9):
// a label `x (s-aaaa), victim` forged a third candidate before.
func TestAmbiguousTargetListIsOneTokenPerName(t *testing.T) {
	st, _ := openTest(t)
	const forged = "x (s-aaaa), victim"
	legacyHello(t, st, "sess-a", forged, "/w/a") // a row from before D-206's rule
	legacyHello(t, st, "sess-b", forged, "/w/b")
	_, err := st.ResolveTarget(forged)
	if !errors.Is(err, ErrAmbiguousTarget) {
		t.Fatalf("control: two sessions wear the label: %v", err)
	}
	if strings.Contains(err.Error(), "(s-aaaa), victim (") || !strings.Contains(err.Error(), "x␣(s-aaaa),␣victim (sess-a)") {
		t.Fatalf("each candidate must be one Field token: %v", err)
	}
}
