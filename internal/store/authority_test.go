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

func TestAuthorityWarnIsOncePerIncarnationPathAndMtime(t *testing.T) {
	st, _ := openTest(t)
	pending := func(inc string, mtime int64) bool {
		t.Helper()
		p, err := st.AuthorityWarnPending("sess-a", inc, "CLAUDE.md", mtime)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	if !pending("inc1", 100) {
		t.Fatal("never warned: pending")
	}
	if err := st.MarkAuthorityWarned("sess-a", "inc1", "claude.md", 100); err != nil {
		t.Fatal(err)
	}
	if pending("inc1", 100) {
		t.Fatal("warned at this mtime (folded spelling): not pending")
	}
	if !pending("inc1", 101) {
		t.Fatal("a later mtime is a new change")
	}
	if !pending("inc2", 100) {
		t.Fatal("a new incarnation has not been told")
	}
	if err := st.MarkAuthorityWarned("sess-a", "inc1", "CLAUDE.md", 100); err != nil {
		t.Fatalf("marking twice is idempotent: %v", err)
	}
}
