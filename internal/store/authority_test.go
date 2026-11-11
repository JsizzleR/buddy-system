package store

import (
	"reflect"
	"strings"
	"testing"
)

func TestAuthorityListFoldsAndBounds(t *testing.T) {
	st, _ := openTest(t)
	for _, p := range []string{"docs/Playbook.md", "docs/playbook.md", "CLAUDE.md"} {
		if err := st.AuthorityAdd(p); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.AuthorityPaths()
	if err != nil {
		t.Fatal(err)
	}
	// One folded path, spelled as first added.
	if want := []string{"CLAUDE.md", "docs/Playbook.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if removed, err := st.AuthorityRemove("DOCS/PLAYBOOK.MD"); err != nil || !removed {
		t.Fatalf("remove by folded spelling: %v %v", removed, err)
	}
	if removed, _ := st.AuthorityRemove("docs/playbook.md"); removed {
		t.Fatal("removed twice")
	}
	for i := 0; i < MaxAuthority-1; i++ {
		if err := st.AuthorityAdd("d/" + strings.Repeat("x", i+1)); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if err := st.AuthorityAdd("one-too-many"); err == nil {
		t.Fatal("the bound must refuse")
	}
	// Re-adding an existing path at the bound is a no-op, not a refusal.
	if err := st.AuthorityAdd("claude.md"); err != nil {
		t.Fatalf("re-add at the bound: %v", err)
	}
}

func TestAuthorityLastIsTheNewestRecordPerIncarnationAndPath(t *testing.T) {
	st, _ := openTest(t)
	last := func(inc string) (int64, bool) {
		t.Helper()
		k, ok, err := st.AuthorityLast("sess-a", inc, "CLAUDE.md")
		if err != nil {
			t.Fatal(err)
		}
		return k, ok
	}
	if _, ok := last("inc1"); ok {
		t.Fatal("nothing recorded: no key")
	}
	if err := st.AuthorityRecord("sess-a", "inc1", "claude.md", 100); err != nil {
		t.Fatal(err)
	}
	if k, ok := last("inc1"); !ok || k != 100 {
		t.Fatalf("recorded under the folded spelling: got %d %v", k, ok)
	}
	if _, ok := last("inc2"); ok {
		t.Fatal("another incarnation knows nothing")
	}
	// A, B, A: the newest is A again, which a set of seen keys would get
	// wrong (B was announced after A, so the session's copy is B until A
	// comes back).
	for _, k := range []int64{200, 100} {
		if err := st.AuthorityRecord("sess-a", "inc1", "CLAUDE.md", k); err != nil {
			t.Fatal(err)
		}
		if got, _ := last("inc1"); got != k {
			t.Fatalf("after recording %d the newest is %d", k, got)
		}
	}
}
