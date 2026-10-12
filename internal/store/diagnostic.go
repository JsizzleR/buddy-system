package store

// Diagnostics must not call Open: even a read through that door can migrate.
// Nor is mode=ro enough: SQLite may create WAL support files. Read a bounded,
// stable, journal-free image using plain read-only descriptors, then give ONLY
// the private copy to SQLite. Nonempty WAL/rollback journals are uncertainty,
// not permission to ignore committed frames or recover the operator's ledger.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const diagnosticLimit = 64 << 20

type LedgerDiagnostic struct {
	State               string `json:"state"`
	SchemaVersion       *int   `json:"schema_version,omitempty"`
	BinarySchemaVersion int    `json:"binary_schema_version,omitempty"`
	SnapshotSHA256      string `json:"snapshot_sha256,omitempty"`
	Identity            string `json:"identity"`
}

// Diagnose never returns an observed incarnation: a diagnostic is not a way
// for a delayed callback to acquire the current token. The caller supplies its
// captured identity and worktree; only the comparison leaves this function.
func Diagnose(path string, identity AdapterIdentity, worktree string) LedgerDiagnostic {
	r := LedgerDiagnostic{State: "unreadable", BinarySchemaVersion: schemaVersion, Identity: "unknown"}
	data, state := diagnosticImage(path)
	if state != "snapshot" {
		r.State = state
		return r
	}
	hash := sha256.Sum256(data)
	r.SnapshotSHA256 = hex.EncodeToString(hash[:])
	dir, err := os.MkdirTemp("", "buddy-diagnostic-")
	if err != nil {
		return r
	}
	defer os.RemoveAll(dir)
	copyPath := filepath.Join(dir, "snapshot.db")
	if os.WriteFile(copyPath, data, 0600) != nil {
		return r
	}
	uri := url.URL{Scheme: "file", Path: copyPath, RawQuery: "mode=ro&immutable=1"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return r
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var version int
	if db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version) != nil {
		r.State = "invalid"
		return r
	}
	r.SchemaVersion = &version
	switch {
	case version < schemaVersion:
		r.State = "migration_required"
		return r
	case version > schemaVersion:
		r.State = "newer_than_binary"
		return r
	}
	var check string
	if db.QueryRowContext(ctx, "PRAGMA quick_check(1)").Scan(&check) != nil || check != "ok" {
		r.State = "invalid"
		return r
	}
	// A version stamp alone is not evidence even for the small projection this
	// diagnostic reads. Prepare that query even when no identity was supplied.
	stmt, err := db.PrepareContext(ctx, "SELECT incarnation, worktree, ended FROM sessions WHERE session_id = ?")
	if err != nil {
		r.State = "invalid"
		return r
	}
	defer stmt.Close()
	r.State = "current_schema"
	if identity.SessionID == "" || identity.Incarnation == "" {
		return r
	}
	var token, wt string
	var ended sql.NullInt64
	err = stmt.QueryRowContext(ctx, identity.SessionID).Scan(&token, &wt, &ended)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		r.Identity = "absent"
	case err != nil:
		r.State = "invalid"
	case token != identity.Incarnation:
		r.Identity = "incarnation_mismatch"
	case wt != worktree:
		r.Identity = "worktree_mismatch"
	case ended.Valid:
		r.Identity = "ended"
	default:
		r.Identity = "matches_snapshot"
	}
	return r
}

func diagnosticImage(path string) ([]byte, string) {
	return diagnosticImageWith(path, diagnosticRead)
}

// The read seam exercises replacement and concurrent edits deterministically;
// production always uses the read-only descriptor path above.
func diagnosticImageWith(path string, read func(string) ([]byte, os.FileInfo, error)) ([]byte, string) {
	// Lstat distinguishes a dangling symlink from a genuinely absent ledger.
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "absent"
	}
	if err != nil || !info.Mode().IsRegular() {
		return nil, "unreadable"
	}
	if info.Size() > diagnosticLimit {
		return nil, "too_large"
	}
	if state := diagnosticJournals(path); state != "clear" {
		return nil, state
	}
	a, ai, err := read(path)
	if err != nil {
		return nil, "unreadable"
	}
	if !os.SameFile(info, ai) || !info.ModTime().Equal(ai.ModTime()) {
		return nil, "changing"
	}
	if state := diagnosticJournals(path); state != "clear" {
		return nil, state
	}
	b, bi, err := read(path)
	if err != nil {
		return nil, "unreadable"
	}
	if !os.SameFile(ai, bi) || !ai.ModTime().Equal(bi.ModTime()) || !bytes.Equal(a, b) {
		return nil, "changing"
	}
	if state := diagnosticJournals(path); state != "clear" {
		return nil, state
	}
	last, err := os.Lstat(path)
	if err != nil || !os.SameFile(bi, last) || !bi.ModTime().Equal(last.ModTime()) || bi.Size() != last.Size() {
		return nil, "changing"
	}
	if len(a) < 100 || string(a[:16]) != "SQLite format 3\x00" {
		return nil, "invalid"
	}
	return a, "snapshot"
}

func diagnosticJournals(path string) string {
	for _, suffix := range []string{"-wal", "-journal"} {
		fi, err := os.Lstat(path + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !fi.Mode().IsRegular() {
			return "unreadable"
		}
		if fi.Size() != 0 {
			return "journal_present"
		}
	}
	return "clear"
}

func diagnosticRead(path string) ([]byte, os.FileInfo, error) {
	// Nonblocking/no-follow plus fstat also refuse a FIFO/symlink swapped in
	// after Lstat. No descriptor capable of writing the source is ever opened.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > diagnosticLimit {
		return nil, nil, errors.New("not a bounded regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, diagnosticLimit+1))
	if len(b) > diagnosticLimit {
		return nil, nil, errors.New("snapshot too large")
	}
	return b, fi, err
}
