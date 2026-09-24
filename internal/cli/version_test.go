package cli

import (
	"database/sql"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #29 at the gate: a ledger stamped newer than the binary is "exists but
// cannot be read", which DENIES a mutating tool (invariant 3) — never "no
// ledger", the silent-allow arm. The control is the same call on the same
// ledger at the binary's own version, which is allowed.
func TestGateDeniesOnALedgerNewerThanTheBinary(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	common, err := exec.Command("git", "-C", f.repo, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(strings.TrimSpace(string(common)), "buddy.db")
	var cur int
	setVersion := func(delta int) {
		db, err := sql.Open("sqlite", "file:"+dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if cur == 0 {
			if err := db.QueryRow(`PRAGMA user_version`).Scan(&cur); err != nil || cur == 0 {
				t.Fatalf("read user_version: %d %v", cur, err)
			}
		}
		if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, cur+delta)); err != nil {
			t.Fatal(err)
		}
	}
	edit := hookJSON("sess-a", f.repo, "Edit", filepath.Join(f.repo, "x.go"))

	setVersion(0)
	out, _, _ := f.run(t, f.repo, edit, "gate")
	if _, denied := decodeDeny(t, out); denied {
		t.Fatalf("control: the binary's own version must allow an unclaimed edit, got %q", out)
	}

	setVersion(1)
	out, _, _ = f.run(t, f.repo, edit, "gate")
	reason, denied := decodeDeny(t, out)
	if !denied {
		t.Fatalf("a newer ledger must fail CLOSED, got %q", out)
	}
	if !strings.Contains(reason, "newer than this buddy binary") || !strings.Contains(reason, "rebuild and reinstall buddy") {
		t.Fatalf("the deny must say why and what fixes it: %q", reason)
	}
}
