package store

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"syscall"
	"testing"
)

// Compare every file, not just rows: a read that creates -wal/-shm or rewrites
// user_version violates the diagnostic contract even if SELECT still agrees.
func diagnosticFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = b
	}
	return out
}

func TestDiagnoseLedgerNoWrites(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version int
		state   string
	}{
		{"current", schemaVersion, "current_schema"}, {"older", schemaVersion - 1, "migration_required"}, {"newer", schemaVersion + 1, "newer_than_binary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "ledger.db")
			st, err := Open(path, nil)
			if err != nil {
				t.Fatal(err)
			}
			si := hello(t, st, "worker", "worker", "/worktree")
			if _, err := st.db.Exec("PRAGMA user_version = " + strconv.Itoa(tc.version)); err != nil {
				t.Fatal(err)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			before := diagnosticFiles(t, dir)
			r := Diagnose(path, AdapterIdentity{SessionID: si.SessionID, Incarnation: si.Incarnation}, "/worktree")
			if r.State != tc.state || r.SchemaVersion == nil || *r.SchemaVersion != tc.version {
				t.Fatalf("diagnostic: %+v", r)
			}
			if tc.version == schemaVersion && r.Identity != "matches_snapshot" {
				t.Fatalf("positive identity: %+v", r)
			}
			if !reflect.DeepEqual(before, diagnosticFiles(t, dir)) {
				t.Fatal("diagnostic changed source files")
			}
		})
	}
}

func TestDiagnoseIdentityDoesNotRecaptureOrRevive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.db")
	st, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	si := hello(t, st, "worker", "worker", "/worktree")
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, token, wt, state string }{
		{"worker", si.Incarnation, "/worktree", "matches_snapshot"},
		{"worker", "old-token", "/worktree", "incarnation_mismatch"},
		{"worker", si.Incarnation, "/other", "worktree_mismatch"},
		{"missing", si.Incarnation, "/worktree", "absent"},
		{"", "", "/worktree", "unknown"},
	} {
		r := Diagnose(path, AdapterIdentity{SessionID: tc.id, Incarnation: tc.token}, tc.wt)
		if r.Identity != tc.state {
			t.Fatalf("%+v: %+v", tc, r)
		}
	}
	st, err = Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.db.Exec("UPDATE sessions SET ended=1 WHERE session_id='worker'"); err != nil {
		t.Fatal(err)
	}
	st.Close()
	before := diagnosticFiles(t, dir)
	r := Diagnose(path, AdapterIdentity{SessionID: si.SessionID, Incarnation: si.Incarnation}, "/worktree")
	if r.Identity != "ended" || !reflect.DeepEqual(before, diagnosticFiles(t, dir)) {
		t.Fatalf("ended identity changed: %+v", r)
	}
}

func TestDiagnoseRefusesLiveWALAndSpecialFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.db")
	r := Diagnose(path, AdapterIdentity{}, "")
	if r.State != "absent" {
		t.Fatalf("absent: %+v", r)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("created an absent ledger")
	}
	st, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	hello(t, st, "live", "live", "/wt")
	before := diagnosticFiles(t, dir)
	r = Diagnose(path, AdapterIdentity{}, "")
	if r.State != "journal_present" {
		t.Fatalf("must not ignore live WAL: %+v", r)
	}
	if !reflect.DeepEqual(before, diagnosticFiles(t, dir)) {
		t.Fatal("changed WAL or SHM")
	}
	st.Close()
	// Positive control shows that closing/checkpointing by the owner, not the
	// diagnostic, makes the same database inspectable.
	if r = Diagnose(path, AdapterIdentity{}, ""); r.State != "current_schema" {
		t.Fatalf("checkpointed: %+v", r)
	}
	for _, suffix := range []string{"-wal", "-journal"} {
		if err := os.WriteFile(path+suffix, []byte("pending"), 0600); err != nil {
			t.Fatal(err)
		}
		if r = Diagnose(path, AdapterIdentity{}, ""); r.State != "journal_present" {
			t.Fatalf("%s: %+v", suffix, r)
		}
		os.Remove(path + suffix)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if r = Diagnose(link, AdapterIdentity{}, ""); r.State != "unreadable" {
		t.Fatalf("symlink: %+v", r)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if r = Diagnose(link, AdapterIdentity{}, ""); r.State != "unreadable" {
		t.Fatalf("dangling symlink must not be absent: %+v", r)
	}
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if r = Diagnose(path, AdapterIdentity{}, ""); r.State != "unreadable" {
		t.Fatalf("FIFO: %+v", r)
	}
}

func TestDiagnoseInvalidAndBounded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.db")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 128), 0600); err != nil {
		t.Fatal(err)
	}
	if r := Diagnose(path, AdapterIdentity{}, ""); r.State != "invalid" {
		t.Fatalf("garbage: %+v", r)
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(diagnosticLimit + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if r := Diagnose(path, AdapterIdentity{}, ""); r.State != "too_large" {
		t.Fatalf("oversized: %+v", r)
	}
}

func TestDiagnoseChangingImage(t *testing.T) {
	for _, replace := range []bool{false, true} {
		dir := t.TempDir()
		path := filepath.Join(dir, "ledger.db")
		st, err := Open(path, nil)
		if err != nil {
			t.Fatal(err)
		}
		st.Close()
		calls := 0
		_, state := diagnosticImageWith(path, func(p string) ([]byte, os.FileInfo, error) {
			calls++
			if calls == 2 {
				if replace {
					b, err := os.ReadFile(p)
					if err != nil {
						t.Fatal(err)
					}
					other := filepath.Join(dir, "replacement")
					if err := os.WriteFile(other, b, 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(other, p); err != nil {
						t.Fatal(err)
					}
				} else {
					fi, err := os.Stat(p)
					if err != nil {
						t.Fatal(err)
					}
					b, err := os.ReadFile(p)
					if err != nil {
						t.Fatal(err)
					}
					b[63]++
					if err := os.WriteFile(p, b, 0600); err != nil {
						t.Fatal(err)
					}
					// Content must still be checked when size and timestamp agree.
					if err := os.Chtimes(p, fi.ModTime(), fi.ModTime()); err != nil {
						t.Fatal(err)
					}
				}
			}
			return diagnosticRead(p)
		})
		if state != "changing" {
			t.Fatalf("replace=%v state=%s", replace, state)
		}
	}
}
