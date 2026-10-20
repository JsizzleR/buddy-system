package cli

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// D-100 through the verbs: claim and its dry run refuse a backslash-absolute
// scope and say why, authority add refuses it, and authority rm can still
// name a legacy absolute row to free its slot. Each refusal sits beside the
// relative backslash spelling that is accepted.
func TestBackslashAbsoluteScopeIsRefusedAtEveryAddingDoor(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)

	for _, args := range [][]string{
		{"claim", "bad", "--session", "sess-a", "--desc", "d", "--scope", `\etc`},
		{"claim", "bad", "--session", "sess-a", "--desc", "d", "--dry-run", "--scope", `\etc`},
		{"authority", "add", `\etc`},
	} {
		_, errw, code := f.run(t, f.repo, "", args...)
		if code == 0 || !strings.Contains(errw, "is absolute (a backslash reads as a slash)") {
			t.Errorf("%v: want the absolute refusal, got %d %q", args, code, errw)
		}
	}
	if ls, _, _ := f.run(t, f.repo, "", "ls"); strings.Contains(ls, "bad") {
		t.Fatalf("a refused claim is listed: %q", ls)
	}
	// Positive controls: the same doors take a relative backslash spelling.
	if _, errw, code := f.run(t, f.repo, "", "claim", "ok", "--session", "sess-a", "--desc", "d", "--scope", `pkg\a.go`); code != 0 {
		t.Fatalf("a relative backslash scope must be granted: %d %q", code, errw)
	}
	if w, _, _ := f.run(t, f.repo, "", "whose", "pkg/a.go"); !strings.Contains(w, "ok") {
		t.Fatalf("the granted claim must cover pkg/a.go: %q", w)
	}
	if out, errw, code := f.run(t, f.repo, "", "authority", "add", `docs\play.md`); code != 0 || !strings.Contains(out, "watching docs/play.md") {
		t.Fatalf("a relative backslash authority path must be added: %d %q %q", code, out, errw)
	}

	// A legacy absolute authority row, as the bug stored `\etc`.
	db, err := sql.Open("sqlite", "file:"+filepath.Join(f.repo, ".git", "buddy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO authority (path, folded, added) VALUES ('/etc', '/etc', ?)`, f.clock.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, errw, code := f.run(t, f.repo, "", "authority", "rm", `\nothere`); code == 0 || !strings.Contains(errw, "was not on the list") {
		t.Fatalf("rm of an absolute name not held must say not-found: %d %q", code, errw)
	}
	if out, errw, code := f.run(t, f.repo, "", "authority", "rm", `\etc`); code != 0 || !strings.Contains(out, "no longer watching /etc") {
		t.Fatalf("rm must free the legacy row by the spelling that stored it: %d %q %q", code, out, errw)
	}
	if out, _, _ := f.run(t, f.repo, "", "authority"); strings.Contains(out, "/etc") || !strings.Contains(out, "docs/play.md") {
		t.Fatalf("only the legacy row goes: %q", out)
	}
}
