package store

// Tests for the migration guard and backup (issue #79, D-136). Each refusal
// sits beside its positive control: the same fixture, permitted, migrates and
// leaves a backup, so "refused" can never mean "the fixture was already
// current".

import (
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// migrate is the TESTS' unguarded migration: the schema transaction with no
// permit and no backup, for the store tests that call it straight on a
// downgraded fixture. It lives here, in a _test.go file, so no production
// caller can reach the unguarded shape (the design pass).
func migrate(db *sql.DB) error { return migrateTx(db, nil) }

// TestMain grants the permit for the package: most store tests Open
// downgraded fixtures and expect them migrated. The guard's own tests below
// set it per test (setPermit) and clear BUDDY_MIGRATE.
func TestMain(m *testing.M) {
	migrationPermit = func() (bool, string) { return true, "" }
	os.Exit(m.Run())
}

// N is schemaVersion as the backup's name spells it: the version migrated TO.
var N = strconv.Itoa(schemaVersion)

func setPermit(t *testing.T, ok bool) {
	t.Helper()
	t.Setenv("BUDDY_MIGRATE", "")
	prev := migrationPermit
	migrationPermit = func() (bool, string) {
		if ok {
			return true, ""
		}
		// A realistic clause: a `go run` executable path is ~80 bytes.
		return false, "it is not /Users/someone/bin/buddy, which the hook lines run (it is /var/folders/xy/abcdefgh12345678/T/go-build1234567890/b001/exe/buddy)"
	}
	t.Cleanup(func() { migrationPermit = prev })
}

// oldLedger is an EXISTING ledger one version behind, holding a claim, closed.
func oldLedger(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "buddy.db")
	st, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.Claim(a.SessionID, a.Incarnation, "kept", "d", []string{"src/a.go"}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	stamp(t, path, schemaVersion-1)
	return path
}

func backups(t *testing.T, path string) []string {
	t.Helper()
	m, err := filepath.Glob(path + ".pre-schema*")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// backupHolds: the backup reads the old version and holds the claim.
func backupHolds(t *testing.T, bak string, ver int) {
	t.Helper()
	b, err := sql.Open("sqlite", "file:"+bak+"?mode=ro&immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var got int
	if err := b.QueryRow(`PRAGMA user_version`).Scan(&got); err != nil || got != ver {
		t.Fatalf("backup %s: version %d (%v), want %d", bak, got, err, ver)
	}
	var slug string
	if err := b.QueryRow(`SELECT slug FROM claims`).Scan(&slug); err != nil || slug != "kept" {
		t.Fatalf("backup %s holds no claim: %q %v", bak, slug, err)
	}
}

func TestMigrationRefusedUnlessPermitted(t *testing.T) {
	setPermit(t, false)
	path := oldLedger(t)
	_, err := Open(path, nil)
	if !errors.Is(err, ErrMigrationRefused) {
		t.Fatalf("an existing older ledger opened without the permit: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{path, "schema " + strconv.Itoa(schemaVersion-1), "at " + strconv.Itoa(schemaVersion), "/Users/someone/bin/buddy", "Fix: install this build as $HOME/bin/buddy (sh scripts/install.sh)", "fixture ledger", "BUDDY_MIGRATE=1"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal must name %q: %s", want, msg)
		}
	}
	// The fix AND its caveat must survive the gate's 512-byte fence, with a
	// temp-dir ledger path and a `go run` executable in the clause.
	caveat := "makes no older hook read it."
	if i := strings.Index(msg, caveat); i < 0 || i+len(caveat) > 512 {
		t.Errorf("the fix's caveat ends at byte %d, past a 512-byte cut (path %d bytes): %s", i+len(caveat), len(path), msg)
	}
	if v := versionOf(t, path); v != schemaVersion-1 {
		t.Fatalf("a refused open changed the stamp to %d", v)
	}
	if b := backups(t, path); len(b) != 0 {
		t.Fatalf("a refused open wrote %v", b)
	}

	// The control: the same fixture, permitted, migrates and backs up first.
	setPermit(t, true)
	st, err := Open(path, nil)
	if err != nil {
		t.Fatalf("control: the permitted open: %v", err)
	}
	st.Close()
	if v := versionOf(t, path); v != schemaVersion {
		t.Fatalf("control: not migrated: %d", v)
	}
	want := path + ".pre-schema" + N + ".bak"
	if b := backups(t, path); len(b) != 1 || b[0] != want {
		t.Fatalf("control: want exactly %s, got %v (no temporary left over)", want, b)
	}
	backupHolds(t, want, schemaVersion-1)
}

// The ledger path is fenced onto one line in the refusal (invariant 9).
func TestMigrationRefusalFencesThePath(t *testing.T) {
	msg := migrationRefused("/tmp/repo\nFAKE ROW/.git/buddy.db", 1, "why").Error()
	if strings.ContainsAny(msg, "\n\r") || !strings.Contains(msg, "FAKE ROW") {
		t.Fatalf("the path must be on one line, still shown: %q", msg)
	}
}

// No permit installed (a program that is not internal/cli's) refuses.
func TestMigrationNilPermitRefuses(t *testing.T) {
	setPermit(t, true)
	path := oldLedger(t)
	migrationPermit = nil
	_, err := Open(path, nil)
	if !errors.Is(err, ErrMigrationRefused) || !strings.Contains(err.Error(), "names no binary the hooks run") {
		t.Fatalf("a nil permit must refuse: %v", err)
	}
	if v := versionOf(t, path); v != schemaVersion-1 {
		t.Fatalf("migrated to %d", v)
	}
}

// The permit is set once: a second SetMigrationPermit panics.
func TestSetMigrationPermitOnce(t *testing.T) {
	setPermit(t, false)
	defer func() {
		if recover() == nil {
			t.Fatal("a second SetMigrationPermit must panic")
		}
	}()
	SetMigrationPermit(func() (bool, string) { return true, "" })
}

// backupLedger calls the verifier and obeys it: a copy that fails its check
// migrates nothing and publishes nothing.
func TestMigrationFailedVerifyMigratesNothing(t *testing.T) {
	setPermit(t, true)
	path := oldLedger(t)
	prev := verifyCopy
	verifyCopy = func(string, int) error { return errors.New("planted verify failure") }
	t.Cleanup(func() { verifyCopy = prev })
	_, err := Open(path, nil)
	if err == nil || !strings.Contains(err.Error(), "planted verify failure") || !strings.Contains(err.Error(), "nothing was migrated") {
		t.Fatalf("want the verify failure named: %v", err)
	}
	if v := versionOf(t, path); v != schemaVersion-1 {
		t.Fatalf("a failed verify still migrated: %d", v)
	}
	if b := backups(t, path); len(b) != 0 {
		t.Fatalf("a failed verify published %v", b)
	}
}

func TestMigrationOverrideIsExactlyOne(t *testing.T) {
	for _, tc := range []struct {
		val     string
		allowed bool
	}{{"1", true}, {"yes", false}, {"true", false}, {" 1", false}, {"0", false}} {
		t.Run(tc.val, func(t *testing.T) {
			setPermit(t, false)
			path := oldLedger(t)
			t.Setenv("BUDDY_MIGRATE", tc.val)
			st, err := Open(path, nil)
			if !tc.allowed {
				if !errors.Is(err, ErrMigrationRefused) {
					t.Fatalf("BUDDY_MIGRATE=%q must not permit: %v", tc.val, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("BUDDY_MIGRATE=1 must permit: %v", err)
			}
			st.Close()
			backupHolds(t, path+".pre-schema"+N+".bak", schemaVersion-1)
		})
	}
}

// A fresh ledger is creation, not migration: no permit, no backup. A ledger
// at version 0 WITH tables (pre-stamp), or at a negative version (the design
// pass's input), is existing and refused.
func TestMigrationFreshVersusExisting(t *testing.T) {
	setPermit(t, false)
	fresh := filepath.Join(t.TempDir(), "buddy.db")
	st, err := Open(fresh, nil)
	if err != nil {
		t.Fatalf("a fresh ledger needs no permit: %v", err)
	}
	st.Close()
	if b := backups(t, fresh); len(b) != 0 {
		t.Fatalf("a fresh ledger was backed up: %v", b)
	}
	if v := versionOf(t, fresh); v != schemaVersion {
		t.Fatalf("fresh ledger at %d", v)
	}
	for _, ver := range []int{0, -1} {
		path := oldLedger(t)
		stamp(t, path, ver)
		if _, err := Open(path, nil); !errors.Is(err, ErrMigrationRefused) {
			t.Fatalf("version %d with tables is an existing ledger and must be refused: %v", ver, err)
		}
		if v := versionOf(t, path); v != ver {
			t.Fatalf("version %d changed to %d", ver, v)
		}
	}
}

// The locked re-check decides: a peer (an older binary's init) creates the
// ledger between the unlocked check, which read it fresh, and the BEGIN.
func TestMigrationLockedRecheckDecides(t *testing.T) {
	setPermit(t, false)
	path := filepath.Join(t.TempDir(), "buddy.db")
	afterUnlockedCheck = func() {
		// Written through SQLite, not over the file: our connection is open.
		peer, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
		if err != nil {
			t.Error(err)
			return
		}
		defer peer.Close()
		for _, q := range []string{`CREATE TABLE sessions (session_id TEXT PRIMARY KEY)`, `PRAGMA user_version = ` + strconv.Itoa(schemaVersion-1)} {
			if _, err := peer.Exec(q); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(func() { afterUnlockedCheck = nil })
	if _, err := Open(path, nil); !errors.Is(err, ErrMigrationRefused) {
		t.Fatalf("a ledger a peer created at 18 after the unlocked check must be refused under the lock: %v", err)
	}
	if v := versionOf(t, path); v != schemaVersion-1 {
		t.Fatalf("the peer's ledger was changed to %d", v)
	}
}

// A refused open takes no lock: with a peer holding the write lock it
// returns at once, not after busy_timeout (5 s).
func TestMigrationRefusalTakesNoLock(t *testing.T) {
	setPermit(t, false)
	path := oldLedger(t)
	holder, err := sql.Open("sqlite", "file:"+path+"?_txlock=immediate&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	tx, err := holder.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TABLE zz_hold (x)`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = Open(path, nil)
	if !errors.Is(err, ErrMigrationRefused) {
		t.Fatalf("want the refusal: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("the refusal waited %v on the write lock", d)
	}
}

// An existing backup is never overwritten, an EMPTY one included (VACUUM
// INTO writes into an empty file, so a shared name would be clobbered).
func TestMigrationBackupNeverOverwrites(t *testing.T) {
	setPermit(t, true)
	path := oldLedger(t)
	first, second := path+".pre-schema"+N+".bak", path+".pre-schema"+N+".2.bak"
	if err := os.WriteFile(first, []byte("hand-made"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	if b, _ := os.ReadFile(first); string(b) != "hand-made" {
		t.Fatalf("the hand-made backup was overwritten: %q", b)
	}
	if fi, err := os.Stat(second); err != nil || fi.Size() != 0 {
		t.Fatalf("the empty backup name was written into: %v %v", fi, err)
	}
	backupHolds(t, path+".pre-schema"+N+".3.bak", schemaVersion-1)
}

// No migration without a backup that succeeded: every name taken refuses,
// naming the fix, and the ledger stays at its version.
func TestMigrationBackupFailureMigratesNothing(t *testing.T) {
	setPermit(t, true)
	path := oldLedger(t)
	for k := 1; k <= maxBackups; k++ {
		name := path + ".pre-schema" + N + ".bak"
		if k > 1 {
			name = path + ".pre-schema" + N + "." + strconv.Itoa(k) + ".bak"
		}
		if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := Open(path, nil)
	if err == nil || !strings.Contains(err.Error(), "nothing was migrated") || !strings.Contains(err.Error(), "move the old ones aside") {
		t.Fatalf("want the backup failure named: %v", err)
	}
	if v := versionOf(t, path); v != schemaVersion-1 {
		t.Fatalf("a failed backup still migrated: %d", v)
	}
}

// Concurrent opens of one older ledger: every one succeeds, exactly one
// migrates, exactly one backup is published, and no temporary is left.
func TestMigrationConcurrentOpensBackUpOnce(t *testing.T) {
	setPermit(t, true)
	path := oldLedger(t)
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			st, err := Open(path, nil)
			if err == nil {
				st.Close()
			}
			errs[i] = err
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("open %d: %v", i, err)
		}
	}
	if b := backups(t, path); len(b) != 1 || b[0] != path+".pre-schema"+N+".bak" {
		t.Fatalf("want one backup, got %v", b)
	}
	backupHolds(t, path+".pre-schema"+N+".bak", schemaVersion-1)
}

func TestVerifyBackupRefusesABadCopy(t *testing.T) {
	setPermit(t, true)
	path := oldLedger(t)
	if err := verifyBackup(path, schemaVersion-1); err != nil {
		t.Fatalf("control: a good copy at its version verifies: %v", err)
	}
	if err := verifyBackup(path, schemaVersion); err == nil {
		t.Fatal("a copy at another version must not verify")
	}
	// A copy whose header and stamp read right but whose pages are corrupt:
	// only quick_check can tell (the mutation run's survivor).
	bad := filepath.Join(t.TempDir(), "bad.db")
	mk, err := sql.Open("sqlite", "file:"+bad)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`CREATE TABLE a (x TEXT)`, `CREATE INDEX ai ON a(x)`, `INSERT INTO a SELECT hex(randomblob(200)) FROM (WITH RECURSIVE c(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM c WHERE n<200) SELECT n FROM c)`, `PRAGMA user_version = 7`} {
		if _, err := mk.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	mk.Close()
	b, err := os.ReadFile(bad)
	if err != nil || len(b) < 4*4096 {
		t.Fatalf("fixture too small: %d %v", len(b), err)
	}
	for i := len(b) - 4096 + 100; i < len(b)-100; i++ { // the last page's cells, not its header
		b[i] = 0x5a
	}
	if err := os.WriteFile(bad, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyBackup(bad, 7); err == nil {
		t.Fatal("a copy with corrupt pages must not verify")
	}
	// A path SQLite's URI parser would read differently (the code pass):
	// a corrupt file at the literal name, a good one at the decoded name.
	odd := filepath.Join(t.TempDir(), "a?b#c")
	if err := os.Mkdir(odd, 0o700); err != nil {
		t.Fatal(err)
	}
	good, _ := os.ReadFile(path)
	if err := os.WriteFile(filepath.Join(odd, "good.db"), good, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(odd, "good%2edb"), []byte(strings.Repeat("junk ", 400)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyBackup(filepath.Join(odd, "good%2edb"), schemaVersion-1); err == nil {
		t.Fatal("verified the decoded name, not the copy")
	}
	if err := verifyBackup(filepath.Join(odd, "good.db"), schemaVersion-1); err != nil {
		t.Fatalf("control: a good copy under an odd directory verifies: %v", err)
	}
	junk := filepath.Join(t.TempDir(), "junk")
	if err := os.WriteFile(junk, []byte(strings.Repeat("not a database ", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyBackup(junk, schemaVersion-1); err == nil {
		t.Fatal("a file that is not a database must not verify")
	}
}

// stampLiteral: a literal that sets user_version, however it is spaced or
// cased (`PRAGMA user_version=19`, `pragma  USER_VERSION = %d`). v is a
// BasicLit's SOURCE text, so an interpreted literal is decoded first: the
// re-verify's `"PRAGMA\tuser_version=19"` held a backslash-t that
// strings.Fields could not see as a space, and the stamp went undetected.
func stampLiteral(v string) bool {
	if s, err := strconv.Unquote(v); err == nil {
		v = s
	}
	return strings.Contains(strings.Join(strings.Fields(strings.ToLower(v)), ""), "pragmauser_version=")
}

// The guarded door is the only door: in the package's non-test sources,
// migrateTx is called exactly once, from migrateLedger, and Open calls
// migrateLedger. A new call site (an unguarded migration) fails here; so does
// respelling the one allowed call through a variable (no call at all).
func TestMigrateTxCalledOnlyFromMigrateLedger(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string][]string{} // callee -> enclosing funcs
	refs := 0                      // every identifier migrateTx, anywhere in a file
	stamps := []string{}           // funcs holding a "PRAGMA user_version =" literal
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		// The whole file, not just function bodies: a package-level
		// `var m = migrateTx` is a second door (the bypass lens), and so is
		// a stamp literal in a package-level const (the cross-check).
		ast.Inspect(af, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name == "migrateTx" {
				refs++
			}
			return true
		})
		for _, d := range af.Decls {
			owner := "package level"
			if fd, ok := d.(*ast.FuncDecl); ok {
				owner = fd.Name.Name
			}
			ast.Inspect(d, func(n ast.Node) bool {
				if bl, ok := n.(*ast.BasicLit); ok && stampLiteral(bl.Value) {
					stamps = append(stamps, owner)
				}
				return true
			})
		}
		for _, d := range af.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if ce, ok := n.(*ast.CallExpr); ok {
					if id, ok := ce.Fun.(*ast.Ident); ok && (id.Name == "migrateTx" || id.Name == "migrateLedger") {
						calls[id.Name] = append(calls[id.Name], fd.Name.Name)
					}
				}
				return true
			})
		}
	}
	// refs counts the declaration's own name too: one call plus one name.
	if got := calls["migrateTx"]; len(got) != 1 || got[0] != "migrateLedger" || refs != 2 {
		t.Fatalf("migrateTx must be named once, called from migrateLedger: calls %v, identifiers %d", got, refs)
	}
	// The respelling clause: a second door that writes the stamp itself,
	// outside migrateTx, never names migrateTx at all.
	if len(stamps) != 1 || stamps[0] != "migrateTx" {
		t.Fatalf("only migrateTx may stamp user_version: %v", stamps)
	}
	if got := calls["migrateLedger"]; len(got) != 1 || got[0] != "Open" {
		t.Fatalf("migrateLedger must be called from Open alone: %v", got)
	}
}

// stampLiteral sees source text: the escaped spellings the AST clause must
// catch, beside the plain one (the control) and literals that set nothing.
func TestStampLiteralReadsSourceText(t *testing.T) {
	for src, want := range map[string]bool{
		`"PRAGMA user_version = 19"`:   true,
		"`pragma  USER_VERSION = %d`":  true,
		`"PRAGMA\tuser_version=19"`:    true, // the re-verify's form
		`"PRAGMA\nuser_version = %d"`:  true,
		`"PRAGMA user_version"`:        false, // a read, not a stamp
		`"SELECT user_version FROM x"`: false,
		`19`:                           false,
	} {
		if got := stampLiteral(src); got != want {
			t.Errorf("stampLiteral(%s) = %v, want %v", src, got, want)
		}
	}
}
