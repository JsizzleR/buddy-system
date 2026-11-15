package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// D-194: a repository ledger that is a fleet's file. Every layout here is
// built under the fixture's throwaway HOME (newFleet), never the developer's
// ~/.buddy/fleets.

// aliasEnv is a fleet env with the repository's ledger inited, a peer's claim
// on dir1, and fleet inv made: what every layout below starts from.
func aliasEnv(t *testing.T) (fe *fleetEnv, fleetLedger, ledger string) {
	t.Helper()
	fe = newFleet(t)
	fe.initAndHello(t)
	if err := os.MkdirAll(filepath.Join(fe.repo, "dir1"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustClaim(t, fe.fixture, fe.repo, "peer", "sess-a", "dir1")
	return fe, fe.initFleet(t, "inv"), filepath.Join(fe.repo, ".git", "buddy.db")
}

// moveLedger moves the repository's ledger (no connection is open between
// verbs) to dst, sidecars dropped, and leaves a symlink to it in its place.
func moveLedger(t *testing.T, ledger, dst string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, s := range ledgerSidecars {
		os.Remove(ledger + s)
	}
	if err := os.Rename(ledger, dst); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dst, ledger); err != nil {
		t.Fatal(err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	os.Remove(link)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func mustTouch(t *testing.T, p string) {
	t.Helper()
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// linkChain makes n links, each to the next, the last to target, and returns
// the first.
func linkChain(t *testing.T, target string, n int) string {
	t.Helper()
	d := t.TempDir()
	next := target
	for i := n; i >= 1; i-- {
		l := filepath.Join(d, "c"+itoa(i))
		if err := os.Symlink(next, l); err != nil {
			t.Fatal(err)
		}
		next = l
	}
	return next
}

// volPath is dir addressed by inode under macOS's /.vol, when that works
// here.
func volPath(t *testing.T, dir string) (string, bool) {
	t.Helper()
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "", false
	}
	v := filepath.Join("/.vol", strconv.FormatUint(uint64(st.Dev), 10), strconv.FormatUint(uint64(st.Ino), 10))
	vi, err := os.Stat(v)
	if err != nil || !os.SameFile(vi, fi) {
		return "", false
	}
	return v, true
}

// caseInsensitive reports whether dir's volume folds case.
func caseInsensitive(t *testing.T, dir string) bool {
	t.Helper()
	_, err := os.Stat(strings.ToUpper(dir))
	return err == nil
}

// The verdicts. Each layout runs the gate (a write nobody claims: ALLOW on a
// usable ledger, so a DENY there is the ledger's), a read-only tool (always
// passes), a typed verb, commit-gate and hello, and the deny cases name D-194
// in every one. The positive controls are the same repository with a plain
// ledger, a symlink to a ledger outside the fleet home, a stray link SQLite
// never opens, a repository living under the fleet home, and the fleet home
// unreadable or $HOME unset (no fleet reachable, so no check: the design
// pass, finding 2).
func TestGateRefusesARepositoryLedgerThatIsAFleetsFile(t *testing.T) {
	// t.Parallel only: each subtest takes its own fixture slot, and a parent
	// holding one while its subtests wait for the rest deadlocks the
	// 4-slot bound under the full package (measured: a 10m timeout).
	t.Parallel()
	cases := []struct {
		name   string
		layout func(t *testing.T, fe *fleetEnv, fleetLedger, ledger string)
		deny   bool
	}{
		{"control: a plain ledger", func(*testing.T, *fleetEnv, string, string) {}, false},
		{"buddy.db a symlink to the fleet's buddy.db (D-191 Open, P1)", func(t *testing.T, fe *fleetEnv, fl, l string) {
			for _, s := range ledgerSidecars {
				os.Remove(l + s)
			}
			mustSymlink(t, fl, l)
		}, true},
		{"buddy.db a relative symlink through `..` into the fleet", func(t *testing.T, fe *fleetEnv, fl, l string) {
			rel, err := filepath.Rel(filepath.Dir(l), fl)
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range ledgerSidecars {
				os.Remove(l + s)
			}
			mustSymlink(t, rel, l)
		}, true},
		{"buddy.db a symlink to another file in a fleet's directory", func(t *testing.T, fe *fleetEnv, fl, l string) {
			moveLedger(t, l, filepath.Join(filepath.Dir(fl), "archive.db"))
		}, true},
		{"a plain buddy.db whose -wal links into the fleet", func(t *testing.T, fe *fleetEnv, fl, l string) {
			mustTouch(t, fl+"-wal")
			mustSymlink(t, fl+"-wal", l+"-wal")
		}, true},
		{"a plain buddy.db whose -journal links into the fleet (a hot journal is replayed at open)", func(t *testing.T, fe *fleetEnv, fl, l string) {
			mustSymlink(t, fl+"-journal", l+"-journal")
		}, true},
		{"a plain buddy.db whose -shm links into the fleet", func(t *testing.T, fe *fleetEnv, fl, l string) {
			mustTouch(t, fl+"-shm")
			mustSymlink(t, fl+"-shm", l+"-shm")
		}, true},
		{"buddy.db -> jump/buddy.db, jump a link to the fleet's directory (a link mid-path)", func(t *testing.T, fe *fleetEnv, fl, l string) {
			for _, s := range ledgerSidecars {
				os.Remove(l + s)
			}
			mustSymlink(t, filepath.Dir(fl), filepath.Join(filepath.Dir(l), "jump"))
			mustSymlink(t, "jump/buddy.db", l)
		}, true},
		// A `.` before a `..`: SQLite's walk skips `.`, so `sub/./..` is the
		// fleet's directory; a walk that appended `.` would drop it at `..`
		// and land in sub (the Fable cross-check, 5b).
		{"buddy.db -> <fleet>/sub/./../buddy.db", func(t *testing.T, fe *fleetEnv, fl, l string) {
			if err := os.MkdirAll(filepath.Join(filepath.Dir(fl), "sub"), 0o700); err != nil {
				t.Fatal(err)
			}
			for _, s := range ledgerSidecars {
				os.Remove(l + s)
			}
			mustSymlink(t, filepath.Dir(fl)+"/sub/./../buddy.db", l)
		}, true},
		{"buddy.db through a chain of 8 links ending in the fleet", func(t *testing.T, fe *fleetEnv, fl, l string) {
			for _, s := range ledgerSidecars {
				os.Remove(l + s)
			}
			mustSymlink(t, linkChain(t, fl, 8), l)
		}, true},
		{"control: buddy.db through a chain of 8 links to a ledger outside the fleet home", func(t *testing.T, fe *fleetEnv, fl, l string) {
			dst := filepath.Join(t.TempDir(), "data", "repo.db")
			moveLedger(t, l, dst)
			mustSymlink(t, linkChain(t, dst, 8), l)
		}, false},
		{"buddy.db -> a ledger elsewhere whose -wal links into the fleet (design finding 1)", func(t *testing.T, fe *fleetEnv, fl, l string) {
			dst := filepath.Join(t.TempDir(), "data", "repo.db")
			moveLedger(t, l, dst)
			mustTouch(t, fl+"-wal")
			mustSymlink(t, fl+"-wal", dst+"-wal")
		}, true},
		{"control: a stray link beside the link, which SQLite never opens", func(t *testing.T, fe *fleetEnv, fl, l string) {
			moveLedger(t, l, filepath.Join(t.TempDir(), "data", "repo.db"))
			mustTouch(t, fl+"-wal")
			mustSymlink(t, fl+"-wal", l+"-wal")
		}, false},
		{"control: buddy.db a symlink to a ledger outside the fleet home", func(t *testing.T, fe *fleetEnv, fl, l string) {
			moveLedger(t, l, filepath.Join(t.TempDir(), "data", "repo.db"))
		}, false},
		{"control: a repository ledger living under the fleet home, not in a fleet's directory", func(t *testing.T, fe *fleetEnv, fl, l string) {
			moveLedger(t, l, filepath.Join(fe.home, ".buddy", "fleets", "x", "repo", ".git", "buddy.db"))
		}, false},
		// A symlinked fleet home is one fleetLedgerPath refuses for every
		// fleet, so no fleet verb reaches a file beside its target and the
		// guard Lstats it and makes no check (the gate-verdict lens, finding
		// 4b). A guard that Stat'ed it would follow the link and deny.
		{"control: a symlinked fleet home, buddy.db a link into its target's fleet directory", func(t *testing.T, fe *fleetEnv, fl, l string) {
			fh := filepath.Join(fe.home, ".buddy", "fleets")
			moved := filepath.Join(t.TempDir(), "fleets")
			if err := os.Rename(fh, moved); err != nil {
				t.Fatal(err)
			}
			mustSymlink(t, moved, fh)
			moveLedger(t, l, filepath.Join(moved, "inv", "archive.db"))
		}, false},
		// The no-check arm, seen by an input only the check refuses: a
		// -journal linked to itself (which SQLite, in WAL mode, never
		// opens) denies "cannot tell" once a fleet home is readable (the
		// subtest below) and allows wherever there is none (the Fable
		// cross-check, 5a: the plain-ledger control could not see the arm).
		{"control: no fleet home, a -journal link to itself", func(t *testing.T, fe *fleetEnv, fl, l string) {
			if err := os.RemoveAll(filepath.Join(fe.home, ".buddy", "fleets")); err != nil {
				t.Fatal(err)
			}
			mustSymlink(t, l+"-journal", l+"-journal")
		}, false},
		{"control: the fleet home a regular file, a -journal link to itself", func(t *testing.T, fe *fleetEnv, fl, l string) {
			fh := filepath.Join(fe.home, ".buddy", "fleets")
			if err := os.RemoveAll(fh); err != nil {
				t.Fatal(err)
			}
			mustTouch(t, fh)
			mustSymlink(t, l+"-journal", l+"-journal")
		}, false},
		{"control: ~/.buddy unreadable, a -journal link to itself", func(t *testing.T, fe *fleetEnv, fl, l string) {
			mustSymlink(t, l+"-journal", l+"-journal")
			b := filepath.Join(fe.home, ".buddy")
			if err := os.Chmod(b, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(b, 0o700) })
		}, false},
		{"control: the fleet home unreadable, a plain ledger", func(t *testing.T, fe *fleetEnv, fl, l string) {
			b := filepath.Join(fe.home, ".buddy")
			if err := os.Chmod(b, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(b, 0o700) })
		}, false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			boundedParallel(t)
			fe, fl, l := aliasEnv(t)
			c.layout(t, fe, fl, l)
			assertAliasVerdicts(t, fe, c.deny)
		})
	}

	// The fleet home spelled in another case: the same directory on the
	// case-insensitive default volume, compared by SameFile, not by prefix.
	t.Run("buddy.db a symlink to the fleet home spelled in upper case", func(t *testing.T) {
		boundedParallel(t)
		fe, fl, l := aliasEnv(t)
		if !caseInsensitive(t, fe.home) {
			t.Skip("case-sensitive volume: the upper-case spelling is another directory")
		}
		up := filepath.Join(fe.home, ".BUDDY", "FLEETS", "INV", "buddy.db")
		for _, s := range ledgerSidecars {
			os.Remove(l + s)
		}
		mustSymlink(t, up, l)
		_ = fl
		assertAliasVerdicts(t, fe, true)
	})

	// THE PRODUCTION SHAPE (the tests lens, finding 1): a peer holds the
	// ledger open, so a regular -wal and -shm sit beside it while the check
	// runs. Every other case runs with none (SQLite deletes them at the last
	// close), so a guard that refused a REGULAR sidecar, every gated call on
	// any machine with a fleet home, would pass them all.
	t.Run("control: a plain ledger a peer holds open (regular -wal and -shm)", func(t *testing.T) {
		boundedParallel(t)
		fe, _, l := aliasEnv(t)
		st, err := store.Open(l, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		for _, s := range []string{"-wal", "-shm"} {
			if fi, err := os.Lstat(l + s); err != nil || !fi.Mode().IsRegular() {
				t.Fatalf("the control needs a regular %s beside the held ledger: %v", s, err)
			}
		}
		assertAliasVerdicts(t, fe, false)
	})

	// A sidecar that links to itself: SQLite's walk gives up past 200 links,
	// and so does the guard, which denies "cannot resolve" rather than
	// recursing (the tests lens, finding 3).
	t.Run("a -wal link to itself: cannot resolve, denies", func(t *testing.T) {
		boundedParallel(t)
		fe, _, l := aliasEnv(t)
		mustSymlink(t, l+"-wal", l+"-wal")
		out, _, _ := fe.run(t, fe.repo, hookJSON("sess-b", fe.repo, "Write", filepath.Join(fe.repo, "free.md")), "gate")
		if reason, denied := decodeDeny(t, out); !denied || !strings.Contains(reason, "cannot be resolved") {
			t.Fatalf("a looping sidecar: %q", out)
		}
	})

	// The twin of the three no-fleet-home controls above: the same -journal
	// loop with a readable fleet home denies.
	t.Run("a -journal link to itself: cannot resolve, denies", func(t *testing.T) {
		boundedParallel(t)
		fe, _, l := aliasEnv(t)
		mustSymlink(t, l+"-journal", l+"-journal")
		out, _, _ := fe.run(t, fe.repo, hookJSON("sess-b", fe.repo, "Write", filepath.Join(fe.repo, "free.md")), "gate")
		if reason, denied := decodeDeny(t, out); !denied || !strings.Contains(reason, "cannot be resolved") {
			t.Fatalf("a looping -journal: %q", out)
		}
	})

	// init's refusal through its own consumer, with a newline in the OS
	// error's path (a -wal link into an unsearchable directory so named):
	// one stderr line (invariant 9; the Fable cross-check, 5c: no test fed
	// init a raw PathError).
	t.Run("init: a -wal link into an unsearchable directory with a newline in its name, one line", func(t *testing.T) {
		boundedParallel(t)
		fe, _, l := aliasEnv(t)
		locked := filepath.Join(t.TempDir(), "lo\nck")
		if err := os.MkdirAll(filepath.Join(locked, "sub"), 0o700); err != nil {
			t.Fatal(err)
		}
		mustSymlink(t, filepath.Join(locked, "sub", "x-wal"), l+"-wal")
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(locked, 0o700) })
		_, errw, code := fe.run(t, fe.repo, "", "init")
		if code == 0 || !strings.Contains(errw, "cannot be resolved") || strings.Count(errw, "\n") != 1 || !strings.HasSuffix(errw, "\n") {
			t.Fatalf("init over an unresolvable sidecar: code %d %q", code, errw)
		}
	})

	// A resolved basename of 249 bytes: `-journal` is past NAME_MAX, `-wal`
	// and `-shm` are not. SQLite's hot-journal probe finds no journal and the
	// ledger opens, as it did before D-194, so the guard treats the
	// unstat-able name as absent (the invariant-3 lens, finding 5b), never as
	// "cannot tell".
	t.Run("control: a ledger name too long only for its -journal", func(t *testing.T) {
		boundedParallel(t)
		fe, _, l := aliasEnv(t)
		moveLedger(t, l, filepath.Join(t.TempDir(), strings.Repeat("n", 246)+".db"))
		assertAliasVerdicts(t, fe, false)
	})

	// A RELATIVE $HOME is no fleet home (fleetLedgerPath refuses it): no
	// check, the symlinked ledger is today's. Relative to this test
	// process's cwd, so a guard that dropped the IsAbs test would find the
	// fleet through it and deny (the tests lens, finding 6).
	t.Run("a relative $HOME: no check", func(t *testing.T) {
		boundedParallel(t)
		fe, fl, l := aliasEnv(t)
		for _, s := range ledgerSidecars {
			os.Remove(l + s)
		}
		mustSymlink(t, fl, l)
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(wd, fe.home)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(rel, ".buddy", "fleets")); err != nil {
			t.Fatalf("the relative HOME must reach the fleet home from here: %v", err)
		}
		fe.env["HOME"] = rel
		out, _, _ := fe.run(t, fe.repo, hookJSON("sess-b", fe.repo, "Write", filepath.Join(fe.repo, "free.md")), "gate")
		if _, denied := decodeDeny(t, out); denied {
			t.Fatalf("a relative HOME: %q", out)
		}
	})

	// A sidecar link whose target cannot be resolved (a directory with no
	// search permission): the repository's own file cannot be told apart
	// from a fleet's, so the gate denies, naming D-194, rather than leaving
	// it to SQLite's open (whose CANTOPEN would name no cause).
	t.Run("a -wal link into an unsearchable directory: cannot tell, denies", func(t *testing.T) {
		boundedParallel(t)
		fe, _, l := aliasEnv(t)
		locked := filepath.Join(t.TempDir(), "locked")
		if err := os.MkdirAll(filepath.Join(locked, "sub"), 0o700); err != nil {
			t.Fatal(err)
		}
		mustSymlink(t, filepath.Join(locked, "sub", "x-wal"), l+"-wal")
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(locked, 0o700) })
		out, _, _ := fe.run(t, fe.repo, hookJSON("sess-b", fe.repo, "Write", filepath.Join(fe.repo, "free.md")), "gate")
		if reason, denied := decodeDeny(t, out); !denied || !strings.Contains(reason, "D-194") || !strings.Contains(reason, "cannot be resolved") {
			t.Fatalf("an unresolvable sidecar: %q", out)
		}
	})

	// macOS's /.vol/<dev>/<inode>: the fleet's directory addressed by inode.
	// The KERNEL will not follow a symlink into /.vol (measured: `stat -L`
	// falls back to the link), so openLedgerAt's dangling arm denies this
	// before the guard runs; init, which has no such arm, is where SQLite's
	// own walk reaches the fleet (TestInitRefusesALedgerLinkedIntoAFleet).
	// Pinned here as a deny, whatever arm gives it.
	t.Run("buddy.db -> /.vol/<dev>/<inode of the fleet's directory>/buddy.db: denied", func(t *testing.T) {
		boundedParallel(t)
		fe, fl, l := aliasEnv(t)
		vol, ok := volPath(t, filepath.Dir(fl))
		if !ok {
			t.Skip("no /.vol on this system")
		}
		for _, s := range ledgerSidecars {
			os.Remove(l + s)
		}
		mustSymlink(t, filepath.Join(vol, "buddy.db"), l)
		out, _, _ := fe.run(t, fe.repo, hookJSON("sess-b", fe.repo, "Write", filepath.Join(fe.repo, "free.md")), "gate")
		if _, denied := decodeDeny(t, out); !denied {
			t.Fatalf("a /.vol link into the fleet: %q", out)
		}
	})

	// $HOME unset: no fleet verb can reach a fleet (fleetLedgerPath refuses),
	// so the check does not run and the symlinked ledger is today's (decided
	// in the design pass, Q2; a hook and a fleet verb under DIFFERENT HOMEs is
	// recorded open in D-194). Its control is the same layout with HOME set,
	// the second case above.
	t.Run("$HOME unset: no check", func(t *testing.T) {
		boundedParallel(t)
		fe, fl, l := aliasEnv(t)
		for _, s := range ledgerSidecars {
			os.Remove(l + s)
		}
		mustSymlink(t, fl, l)
		delete(fe.env, "HOME")
		out, _, _ := fe.run(t, fe.repo, hookJSON("sess-b", fe.repo, "Write", filepath.Join(fe.repo, "free.md")), "gate")
		if _, denied := decodeDeny(t, out); denied {
			t.Fatalf("HOME unset: %q", out)
		}
	})
}

// assertAliasVerdicts runs each verdict the brief names over the layout.
func assertAliasVerdicts(t *testing.T, fe *fleetEnv, deny bool) {
	t.Helper()
	out, errw, code := fe.run(t, fe.repo, hookJSON("sess-b", fe.repo, "Write", filepath.Join(fe.repo, "free.md")), "gate")
	// A deny must be THIS rule's (the tests lens, finding 5): a resolve
	// failure also names D-194, and only these words tell them apart.
	why := func(s string) bool {
		return strings.Contains(s, "a file in the fleet directory") && strings.Contains(strings.ToLower(s), "fleets/inv")
	}
	reason, denied := decodeDeny(t, out)
	if code != 0 || denied != deny || (deny && !why(reason)) {
		t.Fatalf("gate, a write nobody claims: code %d denied=%v want %v: %q %q", code, denied, deny, out, errw)
	}
	if deny && strings.Contains(reason, "\n") {
		t.Fatalf("the refusal must be one line (invariant 9): %q", reason)
	}
	// The peer's claim governs on a usable ledger: the guard was armed.
	if !deny {
		out, _, _ = fe.run(t, fe.repo, hookJSON("sess-b", fe.repo, "Write", filepath.Join(fe.repo, "dir1", "x.go")), "gate")
		if _, d := decodeDeny(t, out); !d {
			t.Fatalf("control: a write inside the peer's claim must deny on a usable ledger: %q", out)
		}
	}
	out, _, _ = fe.run(t, fe.repo, hookJSON("sess-b", fe.repo, "Read", filepath.Join(fe.repo, "free.md")), "gate")
	if _, d := decodeDeny(t, out); d {
		t.Fatalf("a read-only tool passes whatever the ledger: %q", out)
	}
	_, errw, code = fe.as(t, "sess-b", fe.repo, "ls")
	if (code != 0) != deny || (deny && !why(errw)) {
		t.Fatalf("buddy ls: code %d, want failure=%v: %q", code, deny, errw)
	}
	_, errw, code = fe.run(t, fe.repo, "", "commit-gate")
	if (code != 0) != deny || (deny && !why(errw)) {
		t.Fatalf("commit-gate: code %d, want failure=%v: %q", code, deny, errw)
	}
	_, errw, code = fe.hook(t, "hello", "sess-h", fe.repo)
	if (code != 0) != deny || (deny && !why(errw)) {
		t.Fatalf("hello: code %d, want failure=%v: %q", code, deny, errw)
	}
}

// The fleet's file is never touched by a refused repository: the gate and a
// typed verb leave its rows and its schema stamp as they were (the check runs
// BEFORE the open, which may migrate: the design pass, Q1). Read with a plain
// read-only connection, never store.Open.
func TestAliasedRepositoryLeavesTheFleetFileAlone(t *testing.T) {
	boundedParallel(t)
	fe, fl, l := aliasEnv(t)
	for _, s := range ledgerSidecars {
		os.Remove(l + s)
	}
	mustSymlink(t, fl, l)
	before, err := os.ReadFile(fl)
	if err != nil {
		t.Fatal(err)
	}
	// The fleet's file unreadable: the guard needs only Lstat and Readlink,
	// so it still refuses by its own rule; a guard moved after the open
	// would fail CANTOPEN first, naming no fleet (the tests lens, finding 2).
	if err := os.Chmod(fl, 0); err != nil {
		t.Fatal(err)
	}
	out, _, _ := fe.run(t, fe.repo, hookJSON("sess-b", fe.repo, "Write", filepath.Join(fe.repo, "free.md")), "gate")
	if reason, denied := decodeDeny(t, out); !denied || !strings.Contains(reason, "a file in the fleet directory") {
		t.Fatalf("the gate over an aliased ledger, the fleet's file unreadable: %q", out)
	}
	if _, errw, code := fe.as(t, "sess-b", fe.repo, "claim", "grab", "--desc", "x", "--scope", "free.md"); code == 0 || !strings.Contains(errw, "a file in the fleet directory") {
		t.Fatalf("claim over an aliased ledger: %d %q", code, errw)
	}
	if err := os.Chmod(fl, 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(fl)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("a refused repository changed the fleet's file")
	}
	for _, s := range []string{"-wal", "-shm"} {
		if _, err := os.Lstat(fl + s); err == nil {
			t.Fatalf("a refused repository opened the fleet's file (%s appeared)", s)
		}
	}
	// Control: the same claim over the repository's own ledger is granted.
	fe2, _, _ := aliasEnv(t)
	if _, errw, code := fe2.as(t, "sess-b", fe2.repo, "claim", "grab", "--desc", "x", "--scope", "free.md"); code != 0 {
		t.Fatalf("control: claim over a plain ledger: %d %s", code, errw)
	}
}

// init refuses to create a ledger through a link into a fleet's directory,
// including a DANGLING one (SQLite would create the target through it) and one
// whose `..` comes after a link (physical order: the design pass, Q5). Its
// controls: init through a dangling link outside the fleet home creates the
// target, as it always has.
func TestInitRefusesALedgerLinkedIntoAFleet(t *testing.T) {
	// t.Parallel only: each subtest takes its own fixture slot, and a parent
	// holding one while its subtests wait for the rest deadlocks the
	// 4-slot bound under the full package (measured: a 10m timeout).
	t.Parallel()
	for _, c := range []struct {
		name   string
		target func(t *testing.T, fe *fleetEnv, fleetDir, scratch string) string
		deny   bool
	}{
		{"a dangling link to a fleet's buddy.db", func(t *testing.T, fe *fleetEnv, fd, _ string) string { return filepath.Join(fd, "buddy.db") }, true},
		{"a dangling link whose .. follows a link into the fleet", func(t *testing.T, fe *fleetEnv, fd, sc string) string {
			if err := os.MkdirAll(filepath.Join(fd, "sub"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(fd, "sub"), filepath.Join(sc, "jump")); err != nil {
				t.Fatal(err)
			}
			// Lexically sc/buddy.db; physically the fleet's.
			return sc + "/jump/../buddy.db"
		}, true},
		{"a RELATIVE dangling link whose .. follows a link into the fleet", func(t *testing.T, fe *fleetEnv, fd, sc string) string {
			if err := os.MkdirAll(filepath.Join(fd, "sub"), 0o700); err != nil {
				t.Fatal(err)
			}
			// jump sits beside the ledger link; lexically jump/../buddy.db
			// is the link itself.
			if err := os.Symlink(filepath.Join(fd, "sub"), filepath.Join(fe.repo, ".git", "jump")); err != nil {
				t.Fatal(err)
			}
			return "jump/../buddy.db"
		}, true},
		// SQLite's own path walk (unixFullPathname) tolerates ENOENT and
		// takes `..` against what it resolved so far, so this creates
		// fleets/inv/buddy.db; Go's EvalSymlinks stops at the missing
		// directory (the docs lens, finding 1).
		{"a dangling link whose .. follows a MISSING directory in the fleet", func(t *testing.T, fe *fleetEnv, fd, _ string) string {
			return filepath.Join(fd, "nonexist") + "/../buddy.db"
		}, true},
		// SQLite's walk tolerates the ENOENT at /.vol/<dev> and opens
		// /.vol/<dev>/<inode>/buddy.db, the fleet's; its lexical
		// grandparent is that missing /.vol/<dev>, so only the PHYSICAL
		// parent, `<dir>/..`, is the fleet home (the gate-verdict lens,
		// finding 1).
		{"a link to the fleet's directory addressed by inode under /.vol", func(t *testing.T, fe *fleetEnv, fd, _ string) string {
			vol, ok := volPath(t, fd)
			if !ok {
				t.Skip("no /.vol on this system")
			}
			return filepath.Join(vol, "buddy.db")
		}, true},
		// A fleet directory not made yet: its physical parent, `<dir>/..`,
		// cannot be stat'ed, so only the LEXICAL parent names the fleet
		// home. Refused now, by this rule's words, rather than left to
		// SQLite's CANTOPEN; `init --fleet later` would make the link live.
		{"a dangling link into a fleet directory not made yet", func(t *testing.T, fe *fleetEnv, _, _ string) string {
			return filepath.Join(fe.home, ".buddy", "fleets", "later", "buddy.db")
		}, true},
		{"control: a dangling link outside the fleet home", func(t *testing.T, fe *fleetEnv, _, sc string) string { return filepath.Join(sc, "repo.db") }, false},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			boundedParallel(t)
			fe := newFleet(t)
			fe.initFleet(t, "inv")
			fd := filepath.Join(fe.home, ".buddy", "fleets", "inv")
			// The fleet's own ledger moves away, so the link dangles.
			if err := os.Rename(filepath.Join(fd, "buddy.db"), filepath.Join(t.TempDir(), "gone.db")); err != nil {
				t.Fatal(err)
			}
			sc := t.TempDir()
			target := c.target(t, fe, fd, sc)
			ledger := filepath.Join(fe.repo, ".git", "buddy.db")
			mustSymlink(t, target, ledger)
			out, errw, code := fe.run(t, fe.repo, "", "init")
			if c.deny {
				if code == 0 || !strings.Contains(errw, "a file in the fleet directory") {
					t.Fatalf("init: code %d %q %q", code, out, errw)
				}
				if _, err := os.Lstat(filepath.Join(fd, "buddy.db")); err == nil {
					t.Fatal("a refused init created the fleet's file")
				}
				return
			}
			if code != 0 {
				t.Fatalf("control: init: code %d %q %q", code, out, errw)
			}
			if _, err := os.Stat(target); err != nil {
				t.Fatalf("control: init through a dangling link creates its target: %v", err)
			}
		})
	}

	// SQLite's own bound (the Codex code pass, finding 1): its walk follows
	// 201 links and refuses the 202nd, counting EVERY link it meets, a
	// directory link in a prefix too (macOS's /var -> private/var: one per
	// absolute hop through an unresolved temp path, measured: a chain built
	// there passed 400). Built in a resolved directory with the exact count
	// (measured by instrumenting the walk: 201 links), both sides: at 201 init completes outside every fleet, as SQLite
	// does; at 202 SQLite would fail CANTOPEN and the guard says "cannot
	// tell". A guard with a smaller cap refused the first (it did, at 100).
	for _, c := range []struct {
		total int
		allow bool
	}{{201, true}, {202, false}} {
		c := c
		t.Run("a dangling link through "+itoa(c.total)+" links in all", func(t *testing.T) {
			boundedParallel(t)
			fe := newFleet(t)
			fe.initFleet(t, "inv")
			// The ledger path buddy walks is git's common directory, which
			// git hands over physical (measured: /private/var, not /var), so
			// no link in its prefix counts.
			ledger := filepath.Join(fe.repo, ".git", "buddy.db")
			sc, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			end := filepath.Join(sc, "repo.db")
			next := end
			for i := c.total - 1; i >= 1; i-- { // the ledger link is one
				l := filepath.Join(sc, "c"+itoa(i))
				if err := os.Symlink(next, l); err != nil {
					t.Fatal(err)
				}
				next = l
			}
			mustSymlink(t, next, ledger)
			out, errw, code := fe.run(t, fe.repo, "", "init")
			if c.allow {
				if code != 0 {
					t.Fatalf("init through %d links, which SQLite follows: code %d %q %q", c.total, code, out, errw)
				}
				if _, err := os.Lstat(end); err != nil {
					t.Fatalf("init through %d links creates its target: %v", c.total, err)
				}
				return
			}
			if code == 0 || !strings.Contains(errw, "cannot be resolved") {
				t.Fatalf("init through %d links, past SQLite's bound: code %d %q %q", c.total, code, out, errw)
			}
		})
	}

	// The repository's OWN file cannot be resolved once a fleet home exists
	// (a ledger link to itself): "cannot tell" is not "safe", so init
	// refuses naming D-194, rather than leaving it to the open's error,
	// which names no cause. The control above is the same init allowed.
	t.Run("a ledger link to itself: cannot tell, refused", func(t *testing.T) {
		boundedParallel(t)
		fe := newFleet(t)
		fe.initFleet(t, "inv")
		ledger := filepath.Join(fe.repo, ".git", "buddy.db")
		mustSymlink(t, ledger, ledger)
		out, errw, code := fe.run(t, fe.repo, "", "init")
		if code == 0 || !strings.Contains(errw, "D-194") || !strings.Contains(errw, "cannot be resolved") {
			t.Fatalf("init over a looping ledger link: code %d %q %q", code, out, errw)
		}
	})
}

// %XX, ? and # in a repository's common directory (D-191's Open paragraph,
// the Fable cross-check's finding 2): with store.Open's raw `file:` spelling
// each opened fleet inv's ledger. Each common directory sits OUTSIDE a fleet's
// directory by the guard's rule (the guard alone does not see them), so what
// closes them is the escape in store.Open: the repository's claim lands in its
// own literal ledger and the fleet's file is untouched. The control is the
// same flow in a repository with a plain spelling.
func TestRepositoryLedgerSpelledWithURIMetacharactersOpensItsOwnFile(t *testing.T) {
	// t.Parallel only: each subtest takes its own fixture slot, and a parent
	// holding one while its subtests wait for the rest deadlocks the
	// 4-slot bound under the full package (measured: a 10m timeout).
	t.Parallel()
	for _, c := range []struct {
		name   string
		gitDir func(t *testing.T, home, scratch string) string
	}{
		{"%XX decoding to the fleet's directory", func(t *testing.T, home, sc string) string {
			if err := os.MkdirAll(filepath.Join(sc, "x"), 0o755); err != nil {
				t.Fatal(err)
			}
			rel, err := filepath.Rel(sc, filepath.Join(home, ".buddy", "fleets", "inv"))
			if err != nil {
				t.Fatal(err)
			}
			return filepath.Join(sc, "x%2f..%2f"+strings.ReplaceAll(rel, "/", "%2f"))
		}},
		{"? ending the name at the fleet's buddy.db", func(t *testing.T, home, _ string) string {
			return filepath.Join(home, ".buddy", "fleets", "inv", "buddy.db?")
		}},
		{"# ending the name at the fleet's buddy.db", func(t *testing.T, home, _ string) string {
			return filepath.Join(home, ".buddy", "fleets", "inv", "buddy.db#")
		}},
		{"control: a plain spelling", func(t *testing.T, _, sc string) string { return filepath.Join(sc, "plain.git") }},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			boundedParallel(t)
			fe := newFleet(t)
			fl := fe.initFleet(t, "inv")
			gd := c.gitDir(t, fe.home, t.TempDir())
			wt := filepath.Join(t.TempDir(), "wt")
			if out, err := exec.Command("git", "init", "-q", "--separate-git-dir="+gd, wt).CombinedOutput(); err != nil {
				t.Fatalf("git init: %v\n%s", err, out)
			}
			// The literal ledger exists (a raw open used to reach the
			// fleet's file only when it did: openLedgerAt Lstats it first).
			lit := filepath.Join(gd, "buddy.db")
			mustTouch(t, lit)
			before, err := os.ReadFile(fl)
			if err != nil {
				t.Fatal(err)
			}
			if _, errw, code := fe.hook(t, "hello", "sess-b", wt); code != 0 {
				t.Fatalf("hello: %d %s", code, errw)
			}
			if _, errw, code := fe.as(t, "sess-b", wt, "claim", "grab", "--desc", "x", "--scope", "free.md"); code != 0 {
				t.Fatalf("claim: %d %s", code, errw)
			}
			if n := claimRows(t, lit, "grab"); n != 1 {
				t.Fatalf("the claim must land in the literal ledger %s: %d rows", lit, n)
			}
			after, err := os.ReadFile(fl)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("the repository's claim reached the fleet's file")
			}
		})
	}
}

// claimRows counts open claims named slug in the ledger at p, read through a
// read-only escaped connection (never store.Open, which may migrate).
func claimRows(t *testing.T, p, slug string) int {
	t.Helper()
	st, err := store.OpenEscaped(p, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cs, err := st.Claims(false)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range cs {
		if c.Slug == slug {
			n++
		}
	}
	return n
}

// Invariant 9 with the values that would break it (the Codex code pass, Q6:
// every layout above has ordinary paths). A path holding a newline renders
// on one line in the refusal itself, and "cannot tell", whose OS error is
// wrapped raw, renders on one line through the consumer every hook and verb
// prints it with (the gates fence the whole reason with fence.Line too).
func TestFleetAliasRefusalsAreOneLine(t *testing.T) {
	t.Parallel()
	nl := "/w/a\nfake row/.git/buddy.db"
	for name, s := range map[string]string{
		"fleetAliasErr":             fleetAliasErr{path: nl, file: nl, dir: "/h/.buddy/fleets/x\ny"}.Error(),
		"cannotTell, fencedOpenErr": fencedOpenErr(cannotTell(nl, errors.New("lstat "+nl+": boom"))).Error(),
	} {
		if strings.ContainsAny(s, "\r\n") {
			t.Errorf("%s spans lines (invariant 9): %q", name, s)
		}
		if !strings.Contains(s, "D-194") {
			t.Errorf("%s lost its cause: %q", name, s)
		}
		// Cause and fix BEFORE the paths: three gate arms cut an open
		// error at 512 bytes (the Fable cross-check, 5e).
		if i, j := strings.Index(s, "ask the operator"), strings.Index(s, "/w/a"); i < 0 || j < 0 || i > j {
			t.Errorf("%s: the fix must come before the paths: %q", name, s)
		}
	}
	// cannotTell fences its own path, whatever its consumer does (5d).
	if s := cannotTell(nl, errors.New("boom")).Error(); strings.ContainsAny(s, "\r\n") {
		t.Errorf("cannotTell's own path spans lines: %q", s)
	}
	// No link (path IS file): the fix names the git directory, not a link
	// (the Fable cross-check, 2b).
	same := fleetAliasErr{path: "/h/.buddy/fleets/.git/buddy.db", file: "/h/.buddy/fleets/.git/buddy.db", dir: "/h/.buddy/fleets/.git"}.Error()
	if !strings.Contains(same, "move this repository's git directory out of the fleet home") || strings.Contains(same, "the link is") {
		t.Errorf("no link, wrong fix: %q", same)
	}
	if s := (fleetAliasErr{path: "/a", file: "/b", dir: "/c"}).Error(); !strings.Contains(s, "the link is the operator's to replace") {
		t.Errorf("a link, wrong fix: %q", s)
	}
}
