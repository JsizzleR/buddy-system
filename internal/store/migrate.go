package store

// THE MIGRATION GUARD AND THE BACKUP (issue #79, D-136).
//
// THE FAILURE. Open migrated any ledger below schemaVersion, from any binary,
// with no backup. The ledger is <git-common-dir>/buddy.db, shared by every
// worktree of a checkout, so one `go run ./cmd/buddy ls` on a schema-bump
// branch, in any linked worktree, migrated it in place; the installed older
// ~/bin/buddy every hook runs then refused it (ErrLedgerNewer, D-037) and the
// gate denied every gated call, Bash included, in every worktree of that
// checkout. Nothing migrates down, and the only backup ever made was by hand.
//
// THE RULE. The damage is a MISMATCH between the binary that migrates and the
// binary the hooks run, so an EXISTING ledger is migrated only by:
//   - the binary the hook lines run: README's lines run $HOME/bin/buddy, and
//     internal/cli (migrate.go) sets migrationPermit to "this executable is
//     that file". After install.sh, the new ~/bin/buddy's hooks in every OTHER
//     repository migrate that repository's older ledger themselves, so the
//     failure is not moved to those repositories (the question the issue
//     filed); or
//   - BUDDY_MIGRATE=1, the operator's override, for hooks wired to another
//     path (a Codex hook naming one, a non-default BUDDY_BIN_DIR). It makes
//     the same backup, and makes no older hook read the result.
// A FRESH ledger (user_version exactly 0 and no row in sqlite_master: a new
// `buddy init`, every test fixture) is created by any binary, as before.
//
// Considered and cut, both from the Codex design pass:
//   - install.sh's provenance stamp (cli.installedFrom) as the permit: it says
//     where a build came from, not that hooks run it (a stamped build in a
//     fixture bin dir, an old stamped copy).
//   - $BUDDY_BIN_DIR/buddy as a second hooks path: a dev binary run with
//     BUDDY_BIN_DIR=/tmp/dev would pass, and check-install.sh sets exactly
//     that around builds of the tree under test.
//   - BUDDY_MIGRATE alone (the issue's first shape): install.sh migrates only
//     its own checkout, so the new installed binary's hooks would refuse, and
//     the gate deny, in every other repository with an older ledger.
//
// THE BACKUP. Under migrateTx's write lock (BEGIN IMMEDIATE), on a SECOND
// connection, `VACUUM INTO` a temporary name nobody else uses, then fsync,
// verify, and hard-link it to <db>.pre-schema<N>.bak (N the version migrated
// TO, as the hand-made pre-schema15 copy is named), or .pre-schema<N>.<k>.bak
// when that exists. VACUUM INTO cannot run inside a transaction, so the lock
// is held by the first connection while the second reads; measured
// (2026-11-10, modernc sqlite): it succeeds in WAL while the lock is held,
// and its output holds only committed state. Because the lock is held from
// before the snapshot to the commit, no write lands between them, and a
// second opener blocked on BEGIN finds the version current and makes no
// backup. The temporary name is unique because VACUUM INTO writes into an
// existing EMPTY file (measured): two processes sharing a name could both
// write it. The link is exclusive (a rename would overwrite): an earlier
// backup, from an attempt killed before its commit or made by hand, may be
// the only pre-migration copy.
//
// THE GUARANTEE, as narrowed by the design pass: before an existing ledger's
// migration commits, a verified, synced snapshot of its committed
// pre-migration state is published beside it; a failure anywhere in the
// backup commits no migration, and Open fails (the gate denies, invariant 3).
// It is a logical copy, not the ledger's bytes. A process killed before the
// link can leave a temporary file; one killed after it leaves a backup and no
// migration, and the next attempt publishes another.
//
// NOT DONE, accepted:
//   - A FRESH ledger is created by any build, so a dev build's `buddy init` in
//     a repository never inited stamps its own schema there and older hooks
//     then deny (D-037). Every fixture harness creates ledgers with builds
//     that are not ~/bin/buddy; the issue's failure is an EXISTING ledger.
//   - Open's DSN sets journal_mode=WAL when a connection opens, before the
//     version is read, so a refused open can still leave -wal/-shm files. Every
//     buddy since D-001 opens that way, so every ledger is already WAL and the
//     pragma changes nothing in it; ErrLedgerNewer's refusal is the same.
//   - A fixture HOME whose bin/buddy IS a dev build passes the permit: it is
//     the binary those hooks would run.
//   - os.Executable names a path: a process whose file install.sh renamed a
//     new build over is judged by the new file. A downgrade install racing a
//     running newer buddy is the one input that turns that into a migration
//     older hooks refuse; `buddy update` refuses a downgrade (D-110).
//   - A backup slower than busy_timeout (5 s) under the lock makes a
//     concurrent opener of the same binary fail once (it denies, and the next
//     call finds the ledger current). The ledger was 1.4 MB when measured.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"

	"github.com/JsizzleR/buddy-system/internal/fence"
)

// ErrMigrationRefused is Open's refusal to migrate an existing ledger from a
// binary that is not the one the hooks run (D-136). To every caller it is
// "ledger exists but cannot be opened": the gate DENIES (invariant 3), with
// the cause and the fix in the words, as D-084 does for an unwritable ledger.
var ErrMigrationRefused = errors.New("refusing to migrate a shared ledger")

// migrationPermit says whether this process is the binary the hook lines run,
// and if not, why not, in a clause that completes "and ...". Set once at
// process start by internal/cli (migrate.go), which knows how a hook line
// names buddy; nil refuses. Consulted only when an existing ledger needs a
// migration, so a current ledger's open (every gate call) pays nothing.
var migrationPermit func() (ok bool, why string)

// SetMigrationPermit installs the permit, once: internal/cli's init calls it.
// A second call panics rather than let a later caller replace the permit
// with a permissive one (the bypass lens).
func SetMigrationPermit(f func() (ok bool, why string)) {
	if migrationPermit != nil {
		panic("store: the migration permit is set once, at init")
	}
	migrationPermit = f
}

// verifyCopy is verifyBackup, a variable only so a test can make the check
// fail and prove backupLedger calls it and obeys it (VACUUM INTO always
// yields a good copy, so nothing else can).
var verifyCopy = verifyBackup

// afterUnlockedCheck is a TEST SEAM: called between migrateLedger's unlocked
// check and its BEGIN, so a test can have a peer create the ledger there and
// prove the locked re-check, not the unlocked one, decides. nil in production.
var afterUnlockedCheck func()

// maxBackups bounds the numbered backup names one schema version can take.
const maxBackups = 20

func migrationAllowed() (bool, string) {
	// Exactly "1": a value the operator typed on purpose, not any non-empty
	// string a settings block or a stray export happens to hold.
	if os.Getenv("BUDDY_MIGRATE") == "1" {
		return true, ""
	}
	if migrationPermit == nil {
		return false, "this program names no binary the hooks run"
	}
	return migrationPermit()
}

// ledgerIsFresh: a ledger nothing has written. Version EXACTLY 0 (a negative
// stamp is an existing ledger: the design pass's input, user_version=-1 with
// tables, read neither "> 0" nor "0 and tables" under the first shape) and no
// schema object at all. A pre-stamp ledger reads 0 WITH tables: existing.
func ledgerIsFresh(q rowQuerier, ver int) (bool, error) {
	if ver != 0 {
		return false, nil
	}
	var n int
	if err := q.QueryRow(`SELECT count(*) FROM sqlite_master`).Scan(&n); err != nil {
		return false, fmt.Errorf("inspect ledger before migrating it: %w", err)
	}
	return n == 0, nil
}

func migrationRefused(dbPath string, ver int, why string) error {
	// The gate fences an open's error at 512 bytes, so the fix comes before
	// anything that can be cut. BUDDY_MIGRATE is not offered as a repair:
	// with older hooks it backs up and still strands them (the design pass).
	// The fix, then the cause, the permit's clause (whose executable path is
	// long and least useful) last of all: with a realistic `go run` path the
	// first order put the BUDDY_MIGRATE caveat past the cut (the refusal lens).
	// The path is fenced here (invariant 9, the code pass and the cross-
	// check): the gate's home-repo arm renders an open's error unfenced.
	return fmt.Errorf("%w: %s is at schema %d, this buddy at %d. Fix: install this build as $HOME/bin/buddy (sh scripts/install.sh) and run that, or try it on a fixture ledger; BUDDY_MIGRATE=1 migrates anyway, after a backup, and makes no older hook read it. Hooks on an older buddy would refuse the migrated ledger and the gate deny every gated call here (D-037); refused because %s",
		ErrMigrationRefused, fence.Line(dbPath, 200), ver, schemaVersion, why)
}

// migrateLedger is Open's ONLY way to migrate. The permit is checked first
// from Open's unlocked read, so a refused binary takes no lock and never
// queues a hook behind busy_timeout; then again under the lock, where the
// version and freshness are re-read, because a peer may have created or
// migrated the ledger in between.
func migrateLedger(db *sql.DB, dbPath string) error {
	// ONE statement, so one snapshot (the concurrency lens): Open's version
	// read and a separate table count could straddle a peer's fresh create,
	// which commits the tables and the stamp together, and read "version 0
	// with tables", a state that never existed, refused in wrong words.
	var ver, n int
	if err := db.QueryRow(`SELECT (SELECT user_version FROM pragma_user_version), (SELECT count(*) FROM sqlite_master)`).Scan(&ver, &n); err != nil {
		return fmt.Errorf("inspect ledger before migrating it: %w", err)
	}
	if fresh := ver == 0 && n == 0; !fresh && ver < schemaVersion {
		if ok, why := migrationAllowed(); !ok {
			return migrationRefused(dbPath, ver, why)
		}
	}
	if afterUnlockedCheck != nil {
		afterUnlockedCheck()
	}
	return migrateTx(db, func(tx *sql.Tx, lockedVer int) error {
		fresh, err := ledgerIsFresh(tx, lockedVer)
		if err != nil || fresh {
			return err
		}
		if ok, why := migrationAllowed(); !ok {
			return migrationRefused(dbPath, lockedVer, why)
		}
		return backupLedger(db, dbPath, lockedVer)
	})
}

// backupLedger publishes a verified copy of the committed ledger at version
// ver. The caller holds the write lock on another connection of db; the
// default pool opens a second one (a one-connection pool would deadlock here,
// and Open sets no limit).
func backupLedger(db *sql.DB, dbPath string, ver int) (err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("back up the ledger before migrating it (nothing was migrated): %w", err)
		}
	}()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	// Some name must be free BEFORE the copy: with every name taken, each
	// open would otherwise pay a whole VACUUM, two fsyncs and a quick_check
	// under the write lock before failing (the concurrency lens). Only a
	// check: the link loop below still starts at the first name and is the
	// exclusive step (the cross-check: a probe that chose the name left a
	// rename in place of the link untested).
	free := false
	for k := 1; k <= maxBackups && !free; k++ {
		_, err := os.Lstat(backupName(dbPath, k))
		free = errors.Is(err, fs.ErrNotExist)
	}
	if !free {
		return fmt.Errorf("%d backups named %s.pre-schema%d*.bak exist already; move the old ones aside", maxBackups, fence.Line(dbPath, 200), schemaVersion)
	}
	tmp := fmt.Sprintf("%s.pre-schema%d.tmp-%d-%s", dbPath, schemaVersion, os.Getpid(), newToken())
	// Removed on every path: after the link it is a second name for the
	// published file, and before it a partial copy.
	defer os.Remove(tmp)
	if _, err := conn.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		return err
	}
	if err := syncPath(tmp); err != nil {
		return err
	}
	if err := verifyCopy(tmp, ver); err != nil {
		return err
	}
	for k := 1; k <= maxBackups; k++ {
		err := os.Link(tmp, backupName(dbPath, k))
		if err == nil {
			return syncPath(filepath.Dir(dbPath))
		}
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	return fmt.Errorf("%d backups named %s.pre-schema%d*.bak exist already; move the old ones aside", maxBackups, fence.Line(dbPath, 200), schemaVersion)
}

// backupName is the k-th backup name: .pre-schema<N>.bak, then .<k>.bak.
func backupName(dbPath string, k int) string {
	if k == 1 {
		return fmt.Sprintf("%s.pre-schema%d.bak", dbPath, schemaVersion)
	}
	return fmt.Sprintf("%s.pre-schema%d.%d.bak", dbPath, schemaVersion, k)
}

// verifyBackup opens the copy read-only and requires the version the lock
// read and a clean quick_check: a copy that cannot be read back is no
// backup. immutable=1: nobody else knows the temporary name, and the copy
// is in rollback-journal mode (measured), so no -wal or -shm appears.
//
// The path is percent-escaped into the URI (the code pass): SQLite decodes
// %XX in a file: URI and ends the name at ? or #, so a raw `good%2edb`
// opened `good.db` and verified the wrong file. EscapedPath escapes %, ?
// and # and keeps /. Open's own "file:"+dbPath has the same shape, older
// than this change, and is recorded open.
func verifyBackup(path string, ver int) error {
	u := url.URL{Path: path}
	b, err := sql.Open("sqlite", "file:"+u.EscapedPath()+"?mode=ro&immutable=1")
	if err != nil {
		return err
	}
	defer b.Close()
	var got int
	if err := b.QueryRow(`PRAGMA user_version`).Scan(&got); err != nil {
		return fmt.Errorf("read the backup back: %w", err)
	}
	if got != ver {
		return fmt.Errorf("the backup reads schema %d, the ledger %d", got, ver)
	}
	var check string
	if err := b.QueryRow(`PRAGMA quick_check`).Scan(&check); err != nil {
		return fmt.Errorf("check the backup: %w", err)
	}
	if check != "ok" {
		return fmt.Errorf("the backup fails quick_check: %s", check)
	}
	return nil
}

func syncPath(p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
