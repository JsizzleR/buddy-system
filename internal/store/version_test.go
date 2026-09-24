package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #29: Open migrated only upward and accepted anything else, so a stale
// binary used a ledger a newer one had migrated as if it were its own shape.

// stamp sets a ledger's user_version behind Open's back.
func stamp(t *testing.T, path string, ver int) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, ver)); err != nil {
		t.Fatal(err)
	}
}

func versionOf(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var ver int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&ver); err != nil {
		t.Fatal(err)
	}
	return ver
}

func TestOpenRefusesALedgerNewerThanTheBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "buddy.db")
	st, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	// Positive control: the binary's own version opens, so a refusal below is
	// about the version and not about this fixture.
	stamp(t, path, schemaVersion)
	st, err = Open(path, nil)
	if err != nil {
		t.Fatalf("control: a ledger at schemaVersion must open: %v", err)
	}
	st.Close()

	stamp(t, path, schemaVersion+1)
	st, err = Open(path, nil)
	if err == nil {
		st.Close()
		t.Fatal("a ledger stamped newer than the binary opened")
	}
	if !errors.Is(err, ErrLedgerNewer) {
		t.Fatalf("want ErrLedgerNewer, got %v", err)
	}
	for _, want := range []string{fmt.Sprintf("schema version %d", schemaVersion+1), fmt.Sprintf("up to %d", schemaVersion), "rebuild and reinstall buddy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q: %v", want, err)
		}
	}
	// Refused means untouched: nothing migrates DOWN.
	if v := versionOf(t, path); v != schemaVersion+1 {
		t.Fatalf("a refused open changed the stamp to %d", v)
	}
}

// Open's version read is unlocked; migrate re-reads under the lock, and a newer
// binary can have migrated in between. That re-check must refuse too, not
// return nil and let Open hand back a Store on the newer shape.
func TestMigrateRefusesANewerStampSeenUnderTheLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "buddy.db")
	st, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	stamp(t, path, schemaVersion+1)
	db, err := sql.Open("sqlite", "file:"+path+"?_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrate(db); !errors.Is(err, ErrLedgerNewer) {
		t.Fatalf("want ErrLedgerNewer from the locked re-check, got %v", err)
	}
	// Control: at the binary's own version the same call is a no-op, not an error.
	stamp(t, path, schemaVersion)
	if err := migrate(db); err != nil {
		t.Fatalf("control: migrate at schemaVersion must be a no-op: %v", err)
	}
}
