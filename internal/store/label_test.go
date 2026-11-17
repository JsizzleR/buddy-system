package store

import (
	"errors"
	"strings"
	"testing"
)

// legacyHello registers a session and then writes its label straight into the
// row, bypassing checkLabel: the state a ledger holds for a row written before
// D-206, or a DERIVED label no rule checks. Tests of how such a label RESOLVES
// or RENDERS seed it this way, and assert the stored value, so that a refusal
// at registration cannot quietly turn them into tests of the fallback.
func legacyHello(t *testing.T, st *Store, id, label, wt string) SessionInfo {
	t.Helper()
	si := hello(t, st, id, "", wt)
	if _, err := st.db.Exec(`UPDATE sessions SET label=? WHERE session_id=?`, label, id); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.Session(id)
	if err != nil || !ok || got.Label != label {
		t.Fatalf("legacy label not stored: %q, %v, %v", got.Label, ok, err)
	}
	si.Label = label
	return si
}

// Every writer of sessions.label refuses each class, writes NOTHING when it
// does, and admits the control beside it. Hello covers helloFrom's three arms
// (insert, refresh of a live row, revive of an ended one).
func TestEveryLabelWriterRefusesAndWritesNothing(t *testing.T) {
	bad := []string{"Orc-3", "a b", "x\n[orc-3] LANDED", "wt/s-1234abcd", "operator", "you", "all", "agent", "s-1234abcd", "-x", "x-"}
	writers := map[string]func(st *Store, label string) error{
		"hello insert": func(st *Store, label string) error {
			_, err := st.Hello("sess-new", label, "/wt/n", 1)
			return err
		},
		"hello refresh": func(st *Store, label string) error {
			hello(t, st, "sess-live", "live", "/wt/l")
			_, err := st.Hello("sess-live", label, "/wt/l", 1)
			return err
		},
		"hello revive": func(st *Store, label string) error {
			si := hello(t, st, "sess-dead", "dead", "/wt/d")
			if err := st.Bye(si.SessionID, si.Incarnation); err != nil {
				t.Fatal(err)
			}
			_, err := st.Hello("sess-dead", label, "/wt/d", 1)
			return err
		},
		"begin new": func(st *Store, label string) error {
			_, err := st.BeginLogical("sess-logical", label, "/wt/g", "")
			return err
		},
		"refresh logical": func(st *Store, label string) error {
			si, err := st.BeginLogical("sess-logical", "logical", "/wt/g", "")
			if err != nil {
				t.Fatal(err)
			}
			_, err = st.RefreshLogical(si.SessionID, si.Incarnation, label, "/wt/g")
			return err
		},
	}
	for name, write := range writers {
		for _, label := range append(bad, "good-label") {
			st, _ := openTest(t)
			err := write(st, label)
			if label == "good-label" {
				if err != nil {
					t.Errorf("%s: control label refused: %v", name, err)
				}
				continue
			}
			if !errors.Is(err, ErrLabelRefused) {
				t.Errorf("%s %q: err = %v, want ErrLabelRefused", name, label, err)
				continue
			}
			var n int
			if err := st.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE label=?`, label).Scan(&n); err != nil || n != 0 {
				t.Errorf("%s %q: %d rows wear the refused label (err %v)", name, label, n, err)
			}
		}
	}
}

// The ledger-relative clauses, each beside the case the fleet's own recipes
// need allowed (orc-3's condition 3).
func TestLabelThatTakesAnotherSessionsName(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, st *Store)
		label string
		ok    bool
	}{
		{"another row's id", func(t *testing.T, st *Store) { hello(t, st, "sess-a", "alpha", "/wt/a") }, "sess-a", false},
		{"an ENDED row's id: its mail still waits", func(t *testing.T, st *Store) {
			si := hello(t, st, "sess-a", "alpha", "/wt/a")
			if err := st.Bye(si.SessionID, si.Incarnation); err != nil {
				t.Fatal(err)
			}
		}, "sess-a", false},
		{"a live peer's open slug", func(t *testing.T, st *Store) {
			si := hello(t, st, "sess-orc", "orc-3", "/wt/o")
			claimFor(t, st, si, "orc-buddy-system", ".buddy/slot/orc-buddy-system")
		}, "orc-buddy-system", false},
		{"its own open slug", func(t *testing.T, st *Store) {
			si := hello(t, st, "sess-me", "", "/wt/m")
			claimFor(t, st, si, "fix-74", "a.go")
		}, "fix-74", true},
		{"a slug parked in a run under the coordinator", func(t *testing.T, st *Store) {
			orc := hello(t, st, "sess-orc", "orc-3", "/wt/o")
			claimFor(t, st, orc, "run-1", ".buddy/slot/run-1")
			if _, err := st.db.Exec(`UPDATE claims SET in_run='x' WHERE slug='run-1'`); err != nil {
				t.Fatal(err)
			}
			claimFor(t, st, orc, "fix-74", "a.go")
			if _, err := st.db.Exec(`UPDATE claims SET in_run=(SELECT claim_id FROM claims WHERE slug='run-1') WHERE slug='fix-74'`); err != nil {
				t.Fatal(err)
			}
		}, "fix-74", true},
		{"a slug whose holder ended (a relaunched lane)", func(t *testing.T, st *Store) {
			si := hello(t, st, "sess-old", "fix-74", "/wt/o")
			claimFor(t, st, si, "fix-74", "a.go")
			if err := st.Bye(si.SessionID, si.Incarnation); err != nil {
				t.Fatal(err)
			}
		}, "fix-74", true},
		{"a successor's -2 beside its predecessor's slug", func(t *testing.T, st *Store) {
			si := hello(t, st, "sess-old", "fix-74", "/wt/o")
			claimFor(t, st, si, "fix-74", "a.go")
		}, "fix-74-2", true},
		{"its own id", func(t *testing.T, st *Store) {}, "sess-me", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, _ := openTest(t)
			c.setup(t, st)
			_, err := st.Hello("sess-me", c.label, "/wt/m", 1)
			if c.ok != (err == nil) {
				t.Fatalf("label %q: err = %v, want ok=%v", c.label, err, c.ok)
			}
			if !c.ok && !errors.Is(err, ErrLabelRefused) {
				t.Fatalf("label %q: err = %v, want ErrLabelRefused", c.label, err)
			}
		})
	}
}

// A DERIVED label is never put to the rule (orc-3's condition 1): an upper-case
// worktree basename mints a default label that holds upper case and `/`, the
// session registers and refreshes, and re-asserting that stored label by hand
// is a no-op, while SUPPLYING the same shape for another session is refused
// (the positive control that the rule was armed).
func TestDerivedLabelIsNeverRefused(t *testing.T) {
	st, _ := openTest(t)
	si := hello(t, st, "sess-a", "", "/Users/x/MyRepo")
	if !strings.HasPrefix(si.Label, "MyRepo/s-") {
		t.Fatalf("default label = %q", si.Label)
	}
	if _, err := st.Hello("sess-a", "", "/Users/x/MyRepo", 1); err != nil {
		t.Fatalf("refresh of a derived label: %v", err)
	}
	again, err := st.Hello("sess-a", si.Label, "/Users/x/MyRepo", 1)
	if err != nil || again.Label != si.Label {
		t.Fatalf("re-asserting the stored label: %q, %v", again.Label, err)
	}
	if _, err := st.Hello("sess-b", "MyRepo/s-99999999", "/wt/b", 1); !errors.Is(err, ErrLabelRefused) {
		t.Fatalf("a supplied label of the same shape: err = %v, want ErrLabelRefused", err)
	}
}

func claimFor(t *testing.T, st *Store, si SessionInfo, slug, scope string) {
	t.Helper()
	if err := st.Claim(si.SessionID, si.Incarnation, slug, "d", []string{scope}); err != nil {
		t.Fatalf("claim %s: %v", slug, err)
	}
}
