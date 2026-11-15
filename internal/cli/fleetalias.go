package cli

// A REPOSITORY LEDGER THAT IS A FLEET'S FILE (D-194, amending D-191's Open
// paragraph, on the operator's word: "add the gate-side refusal").
//
// A fleet ledger (D-191) is coordination-only and the gate never reads it. A
// REPOSITORY ledger is the file the gate's every verdict rests on. If one file
// is both, a `buddy fleet <verb>` or a `BUDDY_FLEET` verb run outside git
// writes the file a repository's gate reads, and can release that repository's
// claims. The fleet's own walk (fleetLedgerPath) refuses every alias it can see
// from its side: a symlinked fleet home or fleet, a fleet directory holding a
// git directory's mark, a fleet ledger or sidecar that is not one regular file
// with one name (so a HARD link from either side makes the fleet unavailable).
// What it cannot see is a link pointing IN from the repository's side (Codex
// re-verify of D-191, round 1, finding 1, P1):
//
//	R/.git/buddy.db -> $HOME/.buddy/fleets/f/buddy.db
//
// SQLite opens the link's target, and puts its -wal and -shm beside the
// target, so R's gate and fleet f's verbs share one file. A plain buddy.db
// whose -wal, -shm or -journal is a symlink into a fleet's directory would be
// the same alias by its sidecar. On this pin SQLite opens a sidecar with
// O_NOFOLLOW: the Fable cross-check measured a symlinked -wal or -shm
// (target existing or dangling) failing CANTOPEN with nothing written to the
// target, and a -journal link never opened (an earlier note that a write
// through a symlinked -wal returned no error did not reproduce). The sidecar
// arms are refused anyway, as defence in depth: one rule for every file
// SQLite opens by name costs nothing, names the cause where CANTOPEN names
// none, and outlives a SQLite that follows them. The %XX
// spelling of the same alias (D-191's Fable cross-check, finding 2) is closed
// in store.Open itself, which now escapes the path; this guard is the links.
//
// THE RULE: a repository ledger whose resolved file, or the resolved file of a
// sidecar SQLite would open beside it, sits DIRECTLY in a directory of the fleet
// home (a fleet's directory, `<HOME>/.buddy/fleets/<entry>/`) is a ledger that
// exists but cannot be used: invariant 3's DENY, with its cause and its fix,
// never feature-off and never errNoLedger (which would hand the session to the
// fleet under openSessionLedger). A fleet's directory belongs to that fleet
// whole, so a link to any file in it is refused, not only to buddy.db: an
// exact-file rule would need the fleet's filename spelled the case-insensitive
// volume's way (D-194).
// The depth is exact (the Codex design pass, Q3): a repository that merely
// lives under the fleet home, `<HOME>/.buddy/fleets/x/repo/.git/buddy.db`, is
// not a fleet's file and opens as before.
//
// COMPARED BY os.SameFile, not by a path prefix: the fleet home spelled in
// another case, or NFD, on the case-insensitive default volume, or reached
// through a symlinked ~/.buddy (which D-191 allows), is the same directory.
//
// BEFORE the open, not by the file SQLite opened (Store.MainFile) after it:
// openDSN reads the schema stamp and may MIGRATE a below-schema file (D-136)
// and asks for WAL mode, so a check after the open would have touched the
// fleet's file before refusing it (the design pass, Q1). The window between
// this check and the open is the TOCTOU the ledger already accepts (D-005,
// invariant 6); D-193's MainFile comparison beside a home stays as the
// backstop for two opens of one file.
//
// THE SIDECARS ARE DERIVED FROM THE RESOLVED DATABASE (the design pass,
// finding 1): `R/.git/buddy.db -> /data/repo.db` with `/data/repo.db-wal`
// linked into a fleet has no sidecar beside the link at all; SQLite opens the
// one beside /data/repo.db. And a stray link beside the LINK, which SQLite
// never opens, refuses nothing.
//
// WHEN THERE IS NO CHECK. $HOME empty or relative, or the fleet home not
// stat-able as a directory (absent, not a directory, ~/.buddy not
// searchable): no fleet verb
// under this HOME can reach a fleet there (fleetLedgerPath refuses every one
// of those), so there is no alias to refuse, and the repository opens as it
// did. A fleet-home error is NOT a denial (the design pass, finding 2): an
// unreadable ~/.buddy would otherwise refuse every gated call in every
// healthy repository on the machine, the fleet-wide-deny class D-137 closed
// and D-191 kept the repository independent of. What does deny is failing to
// resolve the REPOSITORY's own file once a fleet home exists: that is the
// file the gate is about to open, and "cannot tell" is not "safe". Recorded
// open: a hook and a fleet verb with DIFFERENT HOMEs (`R/.git/buddy.db ->
// /H1/.buddy/fleets/f/buddy.db`, the hook under HOME=/H2) pass this check;
// the ledger trusts the machine user (invariant 6), and a registry of homes
// is not built.
//
// THE CALL GRAPH. This file is the one place a gate process names
// fleetHomeRel: TestGateNeverReachesTheFleetLedger exempts repoLedgerFleetAlias
// by name, drops only its edge to the constant, still walks through it, and
// holds everything it reaches to opening no ledger: no store or database/sql
// selector (it does Lstat and Readlink under the fleet home). It reads HOME
// only, never BUDDY_FLEET.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/JsizzleR/buddy-system/internal/fence"
)

// ledgerSidecars are the files SQLite opens by name beside a database: the
// write-ahead log, its shared memory, and the rollback journal (kept although
// the ledger runs in WAL mode: a hot journal left from before WAL mode was set
// is replayed at open; the design pass, Q5).
var ledgerSidecars = []string{"-wal", "-shm", "-journal"}

// fleetAliasErr is the refusal: the repository ledger at path resolves to
// file, which sits in fleet directory dir. Its paths are fenced here although
// every consumer fences the whole error again (a newline then shows escaped
// twice, still on one line): init's and a hook's consumers differ, and one
// line is invariant 9's whole demand (the invariant-3 lens, finding 4).
type fleetAliasErr struct{ path, file, dir string }

// Error puts cause and fix BEFORE the paths: three refusal formats (the gate's
// foreign-repository and reached-directory arms, gh stack's) cut an open
// error at 512 bytes, and three long paths ahead of the fix would cut it off
// (the docs lens, finding 4).
//
// With no link (path IS file: a git directory made inside the fleet home, a
// `git init ~/.buddy/fleets` or a bare `fleets/x.git`) there is no link to
// replace, so the fix names the git directory instead (the Fable
// cross-check, 2b).
func (e fleetAliasErr) Error() string {
	fix := "ask the operator to give this repository a ledger file of its own (the link is the operator's to replace)"
	if e.path == e.file {
		fix = "ask the operator to move this repository's git directory out of the fleet home (no link: its own ledger sits in a fleet's directory)"
	}
	return fmt.Sprintf("a repository ledger is a fleet's file (D-194): %s; a `buddy fleet` verb run outside git can write that file and release this repository's claims. %s resolves to %s, a file in the fleet directory %s",
		fix, fence.Line(e.path, 512), fence.Line(e.file, 512), fence.Line(e.dir, 512))
}

// cannotTell is the refusal when the file SQLite would open for p cannot be
// resolved once a fleet home exists: cause and fix first, then the path and
// the OS error, for the 512-byte arms (the invariant-3 lens, finding 3).
func cannotTell(p string, err error) error {
	return fmt.Errorf("cannot tell whether this repository's ledger is a fleet's file (D-194), because the file SQLite would open cannot be resolved; ask the operator to fix the path: %s: %w", fence.Line(p, 512), err)
}

// repoLedgerFleetAlias refuses a repository ledger that is a fleet's file,
// before anything opens it. nil means no alias (or no fleet home to alias):
// open as before. Called by openLedgerAt, which every gated opener goes
// through, and by init before it creates a ledger.
func repoLedgerFleetAlias(ledger string, env Env) error {
	home := env.getenv("HOME")
	if home == "" || !filepath.IsAbs(home) {
		return nil
	}
	// Lstat: a symlinked fleet home is one fleetLedgerPath refuses for every
	// fleet, so no fleet is reachable there and nothing beside its target is
	// refused (the gate-verdict lens, finding 4). ~/.buddy itself may be a
	// symlink (D-191), and Lstat follows it.
	fleets, err := os.Lstat(filepath.Join(home, fleetHomeRel))
	if err != nil || !fleets.IsDir() {
		return nil
	}
	main, err := resolveLedgerFile(ledger)
	if err != nil {
		return cannotTell(ledger, err)
	}
	if dir, in := inFleetDir(main, fleets); in {
		return fleetAliasErr{path: ledger, file: main, dir: dir}
	}
	for _, side := range ledgerSidecars {
		p := main + side
		li, err := os.Lstat(p)
		switch {
		case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENAMETOOLONG):
			// Absent: SQLite makes it beside main, which is checked. A
			// name too long to exist is no file SQLite can open either
			// (a resolved basename of 248-251 bytes leaves -journal over
			// NAME_MAX and -wal under it, and that ledger opens today; the
			// invariant-3 lens, finding 5b).
			continue
		case err != nil:
			// Not reached without a race: main's directory exists, so a
			// sidecar's Lstat has no ENOTDIR or EACCES of its own.
			return cannotTell(p, err)
		case li.Mode()&os.ModeSymlink == 0:
			continue // a file in main's directory (a hard link: the fleet's walk)
		}
		r, err := resolveLedgerFile(p)
		if err != nil {
			return cannotTell(p, err)
		}
		if dir, in := inFleetDir(r, fleets); in {
			return fleetAliasErr{path: p, file: r, dir: dir}
		}
	}
	return nil
}

// sqliteMaxSymlinks is the pinned SQLite's SQLITE_MAX_SYMLINK (200 in
// modernc.org/sqlite v1.53.0's lib/sqlite.go), compared as its walk does:
// the count of links ALREADY followed must not exceed it, so the 201st link
// is followed and the 202nd is CANTOPEN. A cap of 100 (the C source's
// SQLITE_MAX_SYMLINKS, a different constant) refused init through 101 to
// 201 links that SQLite opens, outside any fleet (the Codex code pass,
// finding 1). Recheck it when go.mod's sqlite pin moves.
const sqliteMaxSymlinks = 200

// resolveLedgerFile is the file SQLite opens for path p: its unix VFS's own
// walk (unixFullPathname's appendOnePathElement), not the kernel's and not
// filepath.EvalSymlinks. Element by element: "" and "." are skipped, ".."
// drops the last element of what is resolved SO FAR, and each other element
// is appended and Lstat'ed; a symlink is replaced by its target (an absolute
// one restarting from /), at most sqliteMaxSymlinks+1 of them; ENOENT
// is NOT an error and the walk goes on. That last rule is why EvalSymlinks
// was wrong (the docs lens, finding 1, reproduced): with `buddy.db ->
// <fleets>/f/nonexist/../buddy.db`, EvalSymlinks stopped at the missing
// directory, the guard saw no file, and init went on to create fleet f's
// ledger, because SQLite takes `..` against `<fleets>/f/nonexist` lexically.
// Following SQLite's rules exactly also covers a DANGLING final link, through
// which SQLite creates its target (init's open), and keeps the physical order
// a link then `..` needs (the design pass, Q5: `/tmp/jump/../buddy.db` with
// jump a link into a fleet).
//
// A sidecar is opened by name beside this resolved path and then by the
// kernel, which follows its link physically and fails on a missing
// directory; walking it SQLite's way can only resolve MORE, so a sidecar it
// places in a fleet's directory is refused even where the open would fail.
func resolveLedgerFile(p string) (string, error) {
	if !filepath.IsAbs(p) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		p = abs
	}
	out, links := "", 0
	var walk func(path string) error
	walk = func(path string) error {
		for _, e := range strings.Split(path, string(filepath.Separator)) {
			switch e {
			case "", ".":
				continue
			case "..":
				if i := strings.LastIndexByte(out, filepath.Separator); i >= 0 {
					out = out[:i]
				}
				continue
			}
			out += string(filepath.Separator) + e
			li, err := os.Lstat(out)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if li.Mode()&os.ModeSymlink == 0 {
				continue
			}
			if links > sqliteMaxSymlinks {
				return syscall.ELOOP
			}
			links++
			t, err := os.Readlink(out)
			if err != nil {
				return err
			}
			if filepath.IsAbs(t) {
				out = ""
			} else {
				out = out[:strings.LastIndexByte(out, filepath.Separator)]
			}
			if err := walk(t); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(p); err != nil {
		return "", err
	}
	if out == "" {
		out = string(filepath.Separator)
	}
	return out, nil
}

// inFleetDir reports whether the resolved file f sits directly in a child
// directory of the fleet home, and names that directory.
//
// BOTH parents, the lexical and the physical (the gate-verdict lens, finding
// 1, reproduced): `/.vol/<dev>/<inode>/buddy.db` on macOS names the fleet's
// directory by inode, SQLite's walk reaches it past an ENOENT at /.vol/<dev>,
// and its lexical grandparent is that missing /.vol/<dev>; only `<dir>/..`,
// spelled raw so nothing cleans it, asks the kernel for the real parent.
func inFleetDir(f string, fleets os.FileInfo) (string, bool) {
	dir := filepath.Dir(f)
	for _, parent := range []string{filepath.Dir(dir), dir + string(filepath.Separator) + ".."} {
		if pi, err := os.Stat(parent); err == nil && os.SameFile(pi, fleets) {
			return dir, true
		}
	}
	return "", false
}
