package cli

// What the gate answers over a ledger its process can read but may not write.
//
// CLAUDE.md's jurisdiction line said "an unwritable ledger denies", then "an
// unreadable ledger denies, writable or not", and one gate test stood behind
// either sentence: a bare deny over a 0444 ledger in a 0555 directory
// (TestLedgerAccessGateStillDenies). Measured at 320df13 (2026-11-20,
// go1.27.1 darwin/arm64, modernc.org/sqlite v1.53.0, a buddy built from that
// tree run on a throwaway repository), the gate gives two different answers
// over such a ledger, and neither sentence says which (D-240 holds the
// measurements; the line now reads "a failed ledger open or read denies"):
//
//	file 0444, directory writable, no sidecars    judged by its claims
//	file 0444, directory 0555, no sidecars        refused, READONLY_DIRECTORY (1544)
//	the same, a peer holds it open                judged by its claims
//	the same, the peer closed, sidecars left      judged by its claims
//	file 0000                                     refused, CANTOPEN (14)
//	seatbelt deny-write, no sidecars              refused, CANTOPEN (14)   (darwin file)
//	the same, a peer holds it open                judged by its claims     (darwin file)
//	the same, the peer closed, sidecars gone      refused, CANTOPEN (14)   (darwin file)
//
// What decides is whether SQLite can open the database for READING, and in WAL
// mode that takes more than a readable file: the `-wal` and `-shm` beside it
// must be there or be creatable. A ledger the gate opens and reads is judged,
// though the same process cannot write it; one whose sidecars are absent and
// cannot be made is refused, and so is one whose file cannot be read. A live
// peer is only one way the sidecars come to be there: the two peer-closed
// rows are the same lock with the peer gone, judged where its sidecars stayed
// and refused where they went. No state gave a silent allow. "Judged" below
// is the whole claims verdict, three probes: a peer's path denied naming the
// peer and its slug, the caller's own path allowed with an empty stdout, an
// uncovered path allowed with its note.
//
// THE GATE RUNS IN A CHILD PROCESS, in every row, the control included. A hook
// is a fresh process per call, and two rows are about ANOTHER process holding
// the ledger open. Run inside this process would not be that case: SQLite's
// unix VFS keeps one shared-memory node per inode per process, so a gate here
// would reuse the peer handle's already-open `-shm` and never open a sidecar
// itself. The child is this test binary re-run as TestLedgerAccessChild, with
// an environment of exactly PATH, TMPDIR, a HOME of its own, BUDDY_OWN_CLAIM
// and the child's two variables (a seatbelt row adds CODEX_SANDBOX, as Codex
// does in what its sandbox runs): never the developer's harness identity, and
// never the developer's HOME, where this machine keeps a memory slot the gate
// would read. It runs on the wall clock while the fixture's rows carry the
// fixture's, so every assertion is a substring that sits ahead of any
// staleness wording.
//
// THE PEER IS THIS PROCESS: a store handle opened BEFORE the claims are made
// and kept until the row ends (in a peer-closed row, until the row closes it,
// with the lock already on). So the claims the gate must find exist only as
// frames in the `-wal`; a gate that judged a read-only ledger from its main
// file alone would allow the peer's path, and the row asserts, on a copy of
// the main file, that they are not in it. That makes one trap: a process that
// closes ANY descriptor on a file drops every fcntl lock it holds on that
// file, so one os.ReadFile of the ledger here un-locks the peer. Measured:
// with the peer open another process is refused a write lock on SQLite's
// shared range of the main file, and after that one read it is granted. The
// rows stayed green with the read planted (the sidecars stayed, and so did
// the frames). Why, read from the pinned SQLite and not measured: its unix
// VFS refuses the exclusive lock a last close's checkpoint-and-delete needs
// while another process holds a lock on the -shm (unixIsSharingShmNode,
// written for "a rogue close(open(dbFile))"), and the peer's lock there is on
// another file, which the read did not drop. So no verdict here is known to
// turn on the main file's lock; but a row named "a peer holds it open" should
// not quietly be a peer that dropped its lock. So nothing opens the ledger
// from Go while a peer row runs (Stat and Chmod open nothing; the copy is
// made by `cp`, another process).
//
// What a row cannot pass by accident:
//   - the child never ran the gate, or found no ledger (feature off): an allow
//     is an empty stdout, which is also what both of those print, so no row
//     asserts an allow alone. Each stands beside the deny naming alpha and the
//     slug, which only a read of THIS ledger produces.
//   - the deny came from another arm: the hook's cwd is the linked worktree,
//     where bravo said hello. With the main checkout's the same deny text
//     comes from the foreign-repository arm (the gate places paths by the
//     hook's cwd, not the process's), so a judged reason may not say "TARGET
//     repo" or "buddy ledger unavailable", and a refused one must say the
//     latter with SQLite's words and may not say "claimed by session".
//   - the lock never bound the child: the modes are read back (not as set is a
//     failure, never a skip), the lock is probed from this process (set and
//     still granted, as root: a skip that says so), and in every judged row
//     the same child, in the same state, is then refused a WRITE with SQLite's
//     READONLY (8). That last line needs its own control: the same verb
//     through the same child succeeds while the ledger is writable.
//   - the row is not in the state it names: the sidecars are asserted absent
//     or present immediately before each probe.

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

const (
	// ledgerHeldBy is the deny only a read of the fixture's ledger produces:
	// the path placed in the caller's own repository, the holder's label and
	// the slug. It ends before the description and any staleness wording.
	ledgerHeldBy = `inside scope "pkg" claimed by session alpha (slug "held"`
	// ledgerRefused opens the gate's fail-closed text; the open's error
	// follows the parenthesis.
	ledgerRefused = "buddy ledger unavailable ("
	// ledgerWriteRefused is SQLite refusing a WRITE on a database it opened
	// read-only. The code is part of it: "readonly database" alone also
	// matches READONLY_DIRECTORY (1544), which is a failed OPEN.
	ledgerWriteRefused = "attempt to write a readonly database (8)"
	// ledgerChildDeadline bounds one child. A run takes about 50 ms; a wedged
	// one would otherwise sit until the test timeout's panic, which runs no
	// cleanup and leaves a 0555 directory behind in TMPDIR.
	ledgerChildDeadline = 60 * time.Second
	// seatbeltUnapplied opens what sandbox-exec prints when it cannot apply a
	// profile. Measured (macOS 27.2, a profile inside one that denies
	// file-write*): "sandbox-exec: sandbox_apply: Operation not permitted"
	// alone on stderr, nothing on stdout, exit 71. Here, not in a darwin
	// file, so the recognizer's test runs everywhere.
	seatbeltUnapplied = "sandbox-exec: sandbox_apply:"
)

// ledgerWrap is what the gate's process runs under: nothing (file modes bind
// the process as it is), or a sandbox's argv prefix and the environment that
// sandbox's harness sets in what it runs. cannot is how the wrapper's own
// diagnostic begins when it could not be applied here at all; a row that sees
// it skips, saying so, rather than pass or fail on a sandbox that never
// existed.
type ledgerWrap struct {
	argv   []string
	env    []string
	cannot string
}

// unapplied reports whether the wrapper said it could not be applied: its
// diagnostic opens stderr, and the child never ran, so stdout is empty and the
// exit is the wrapper's own. Not a search of either stream for a word: the
// gate's note and a refused write both print the fixture's path, and under a
// TMPDIR named sandbox_apply-review the two seatbelt peer rows skipped with
// the sandbox applied and every assertion still ahead (Codex code pass, run
// and seen).
func (w ledgerWrap) unapplied(stdout, stderr string, code int) bool {
	return w.cannot != "" && code != 0 && stdout == "" && strings.HasPrefix(stderr, w.cannot)
}

// ledgerGate is one row's fixture: alpha in the main checkout, bravo in the
// linked worktree, and the ledger both share in the main checkout's .git.
type ledgerGate struct {
	f              *fixture
	gitDir, ledger string
	home           string       // the child's HOME, empty of everything
	peer           *store.Store // this process's handle, in a peer row
}

func newLedgerGate(t *testing.T) *ledgerGate {
	t.Helper()
	f := newFixture(t)
	f.initAndHello(t)
	gitDir := filepath.Join(f.repo, ".git")
	return &ledgerGate{f: f, gitDir: gitDir, ledger: filepath.Join(gitDir, ledgerName), home: t.TempDir()}
}

// claim makes the two claims every row is judged by: alpha's on pkg, bravo's
// own on mine. Through Run in this process, on the fixture's clock.
func (g *ledgerGate) claim(t *testing.T) {
	t.Helper()
	for _, c := range [][]string{
		{"claim", "held", "--session", "sess-a", "--desc", "d", "--scope", "pkg"},
		{"claim", "own", "--session", "sess-b", "--desc", "d", "--scope", "mine"},
	} {
		if _, errw, code := g.f.run(t, g.f.repo, "", c...); code != 0 {
			t.Fatalf("%v: exit %d: %s", c, code, errw)
		}
	}
}

// holdOpen makes this process the peer: a store handle on the ledger, kept
// until the row ends (database/sql keeps the connection in its idle pool; the
// store sets no pool limits). The sidecars are checked, not assumed. Its Close
// is registered at once, so it runs after the modes are restored (Cleanup is
// LIFO) and before TempDir's removal.
func (g *ledgerGate) holdOpen(t *testing.T) {
	t.Helper()
	peer, err := store.Open(g.ledger, nil)
	if err != nil {
		t.Fatalf("the peer's open: %v", err)
	}
	t.Cleanup(func() { peer.Close() })
	g.peer = peer
	if wal, shm := g.sidecars(t); wal == nil || shm == nil {
		t.Fatalf("a peer holding the ledger open must have made its -wal and -shm: wal %v shm %v", wal != nil, shm != nil)
	}
}

// peerCloses ends the peer: the last connection this process has on the
// ledger, so SQLite's close checkpoints the -wal into the main file and then
// unlinks both sidecars, where the directory lets it. The checkpoint is what
// shows the close happened in a row whose sidecars stay, where nothing else
// tells a closed peer from a live one: the claims are not in the main file
// the instant before (the gates that ran while the peer lived moved nothing),
// and are in it after.
func (g *ledgerGate) peerCloses(t *testing.T, when string) {
	t.Helper()
	if g.mainFileHolds(t, "held") {
		t.Fatalf("%s: the claims are in the ledger's main file before the peer closed, so its close's checkpoint cannot be told from an earlier one", when)
	}
	if err := g.peer.Close(); err != nil {
		t.Fatalf("%s: the peer's close: %v", when, err)
	}
	if !g.mainFileHolds(t, "held") {
		t.Fatalf("%s: the peer's close did not checkpoint the claims into the ledger's main file, so it is not known to have closed", when)
	}
}

// child runs one buddy call in a fresh process, under w, from the linked
// worktree, with stdout and stderr apart: the gate's stdout is the hook
// document, and an allow is its being empty.
func (g *ledgerGate) child(t *testing.T, w ledgerWrap, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	// Not os.Args[0]: go test may hand over a relative name, and the child
	// starts in another directory.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	argv := append(append([]string{}, w.argv...), exe, "-test.run=^TestLedgerAccessChild$", "-test.count=1")
	ctx, cancel := context.WithTimeout(context.Background(), ledgerChildDeadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = g.f.wtB
	// The child's own test watchdog (testguard) shares its stderr and outlives
	// it by an instant; past this the pipes are abandoned and Run says so.
	cmd.WaitDelay = 10 * time.Second
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "TMPDIR=" + os.Getenv("TMPDIR"), "HOME=" + g.home,
		// Pinned, not inherited and not left to the default: the third probe
		// reads the warn posture's note.
		EnvOwnClaim + "=warn",
		ledgerChildEnv + "=" + strings.Join(args, "\x1f"),
		ledgerChildStdinEnv + "=" + stdin,
	}
	cmd.Env = append(cmd.Env, w.env...)
	var out, errw bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errw
	err = cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
	case ctx.Err() != nil:
		t.Fatalf("child %v did not finish in %s: %v\nstdout: %s\nstderr: %s", args, ledgerChildDeadline, err, out.String(), errw.String())
	case errors.As(err, &ee):
		code = ee.ExitCode()
	default:
		t.Fatalf("child %v: %v\nstdout: %s\nstderr: %s", args, err, out.String(), errw.String())
	}
	if w.unapplied(out.String(), errw.String(), code) {
		t.Skipf("%s cannot be applied here (already inside a sandbox, which may not nest): %s", w.argv[0], errw.String())
	}
	return out.String(), errw.String(), code
}

// gate asks the child's gate about one Edit by bravo in its own worktree.
func (g *ledgerGate) gate(t *testing.T, w ledgerWrap, rel string) (stdout, stderr string, code int) {
	t.Helper()
	return g.child(t, w, hookJSON("sess-b", g.f.wtB, "Edit", filepath.Join(g.f.wtB, rel)), "gate")
}

// sidecars stats the ledger's -wal and -shm; nil for one that is not there.
func (g *ledgerGate) sidecars(t *testing.T) (wal, shm os.FileInfo) {
	t.Helper()
	stat := func(p string) os.FileInfo {
		fi, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		return fi
	}
	return stat(g.ledger + "-wal"), stat(g.ledger + "-shm")
}

// wantNoSidecars: nothing beside the ledger. A handle leaked by an earlier
// step would make a "no sidecars" row a lie, so this is fatal.
func (g *ledgerGate) wantNoSidecars(t *testing.T, when string) {
	t.Helper()
	if wal, shm := g.sidecars(t); wal != nil || shm != nil {
		t.Fatalf("%s: no -wal or -shm may be beside the ledger: wal %v shm %v", when, wal != nil, shm != nil)
	}
}

// wantSidecars: both beside the ledger, and the -wal holding frames.
func (g *ledgerGate) wantSidecars(t *testing.T, when string) {
	t.Helper()
	wal, shm := g.sidecars(t)
	if wal == nil || shm == nil {
		t.Fatalf("%s: the peer's -wal and -shm must be beside the ledger: wal %v shm %v", when, wal != nil, shm != nil)
	}
	if wal.Size() == 0 {
		t.Fatalf("%s: the -wal is empty, so the claims are not in it", when)
	}
}

// dropSidecars removes what a read-only open left behind, for the one row
// where one does (the first row of TestLedgerAccessGateVerdict says why). A
// -wal holding frames is never removed: that would change the ledger under
// test, and nothing read-only can have written one.
func (g *ledgerGate) dropSidecars(t *testing.T, when string) {
	t.Helper()
	if wal, _ := g.sidecars(t); wal != nil && wal.Size() != 0 {
		t.Fatalf("%s: a read-only open left a -wal of %d bytes; removing frames would change the ledger under test", when, wal.Size())
	}
	for _, p := range []string{g.ledger + "-wal", g.ledger + "-shm"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	g.wantNoSidecars(t, when)
}

// mainFileHolds reports whether the ledger's MAIN file, copied without its
// -wal, holds a claim with this slug. The copy is `cp`'s, not this process's:
// see the header's trap.
func (g *ledgerGate) mainFileHolds(t *testing.T, slug string) bool {
	t.Helper()
	cp := filepath.Join(t.TempDir(), "main-only.db")
	if out, err := exec.Command("cp", g.ledger, cp).CombinedOutput(); err != nil {
		t.Fatalf("copy the ledger's main file: %v: %s", err, out)
	}
	// cp carries the source's mode; the copy is nobody's ledger.
	if err := os.Chmod(cp, 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", cp)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM claims WHERE slug = ?`, slug).Scan(&n); err != nil {
		t.Fatalf("read the copy of the main file: %v", err)
	}
	return n > 0
}

// lock sets the ledger file's mode and, with dir non-zero, its directory's,
// then proves both: read back as set, and binding this user.
func (g *ledgerGate) lock(t *testing.T, file, dir os.FileMode) {
	t.Helper()
	chmodT(t, g.ledger, file)
	want := map[string]os.FileMode{g.ledger: file}
	if dir != 0 {
		chmodT(t, g.gitDir, dir)
		want[g.gitDir] = dir
	}
	for p, m := range want {
		// A lock that is not on disk is a broken test, never a skip: a
		// forgotten chmod must not read as "modes do not bind here".
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != m {
			t.Fatalf("the lock did not take: %s is not mode %04o: %v %v", p, m, fi, err)
		}
	}
	// On disk and still granted is the platform's doing (root is skipped
	// already; a capability or a filesystem that ignores modes is the rest):
	// every assertion below would pass or fail for the wrong reason. An open
	// that succeeds here is closed, which un-locks the peer (the header's
	// trap), and the row stops at once.
	open := func() (*os.File, error) { return os.OpenFile(g.ledger, os.O_RDWR, 0) }
	denied := "a write of"
	if file&0o400 == 0 {
		open = func() (*os.File, error) { return os.Open(g.ledger) }
		denied = "a read of"
	}
	if fh, err := open(); err == nil {
		fh.Close()
		t.Skipf("mode %04o does not refuse this process %s the ledger", file, denied)
	}
	if dir != 0 {
		if fh, err := os.CreateTemp(g.gitDir, "probe"); err == nil {
			fh.Close()
			os.Remove(fh.Name())
			t.Skipf("mode %04o does not refuse this process a new file in the ledger's directory", dir)
		}
	}
}

// wantJudged asserts the claims verdict, three probes in a fixed order, each
// from the state the row names (before runs ahead of each).
func (g *ledgerGate) wantJudged(t *testing.T, state string, w ledgerWrap, before func(*testing.T, string)) {
	t.Helper()
	// 1. A peer's path: denied by its claim, from this ledger, by the home arm.
	before(t, state)
	out, errw, code := g.gate(t, w, "pkg/x.go")
	reason, denied := decodeDeny(t, out)
	if code != 0 || !denied || !strings.Contains(reason, ledgerHeldBy) ||
		strings.Contains(reason, "TARGET repo") || strings.Contains(reason, ledgerRefused) {
		t.Errorf("%s: a peer's path must be denied naming alpha and its slug, read from the caller's own ledger: exit %d\nstdout: %s\nstderr: %s", state, code, out, errw)
	}
	// 2. The caller's own path: allowed, which is an EMPTY stdout. Not "not
	// denied": a panic, a warning and feature-off all pass that.
	before(t, state)
	if out, errw, code := g.gate(t, w, "mine/x.go"); out != "" || code != 0 || strings.Contains(errw, "panic") {
		t.Errorf("%s: the caller's own claimed path must be allowed with an empty stdout: exit %d\nstdout: %s\nstderr: %s", state, code, out, errw)
	}
	// 3. A path no claim covers: allowed, with the own-claim arm's note. The
	// note is the third of the verdict a ledger read produces; without it an
	// allow here is indistinguishable from no ledger at all.
	before(t, state)
	out, errw, code = g.gate(t, w, "free.go")
	if _, denied := decodeDeny(t, out); denied || code != 0 || !strings.Contains(out, "BUDDY UNCLAIMED") {
		t.Errorf("%s: an uncovered path must be allowed with its BUDDY UNCLAIMED note: exit %d\nstdout: %s\nstderr: %s", state, code, out, errw)
	}
}

// wantRefused asserts the fail-closed arm answered all three probes with the
// open's error, and that no claim was read on the way.
func (g *ledgerGate) wantRefused(t *testing.T, state string, w ledgerWrap, sqlite string, before func(*testing.T, string)) {
	t.Helper()
	for _, rel := range []string{"pkg/x.go", "mine/x.go", "free.go"} {
		before(t, state)
		out, errw, code := g.gate(t, w, rel)
		reason, denied := decodeDeny(t, out)
		if code != 0 || !denied || !strings.Contains(reason, ledgerRefused) || !strings.Contains(reason, sqlite) ||
			strings.Contains(reason, "claimed by session") {
			t.Errorf("%s: %s must be refused by the ledger-unavailable arm with %q: exit %d\nstdout: %s\nstderr: %s", state, rel, sqlite, code, out, errw)
		}
	}
}

// wantWrites is the write control's own control: the verb it uses, through
// the same child, succeeds while the ledger is writable.
func (g *ledgerGate) wantWrites(t *testing.T, state string, before func(*testing.T, string)) {
	t.Helper()
	before(t, state)
	if out, errw, code := g.child(t, ledgerWrap{}, "", "claim", "wc", "--session", "sess-b", "--desc", "d", "--scope", "ctl"); code != 0 {
		t.Fatalf("%s: the child's claim on a writable ledger: exit %d\nstdout: %s\nstderr: %s", state, code, out, errw)
	}
}

// wantWriteRefused shows the process that was just judged cannot write the
// ledger it was judged from: without it "judged" could mean the lock never
// applied to the child. also is more the refusal must say.
func (g *ledgerGate) wantWriteRefused(t *testing.T, state string, w ledgerWrap, before func(*testing.T, string), also ...string) {
	t.Helper()
	before(t, state)
	out, errw, code := g.child(t, w, "", "claim", "w", "--session", "sess-b", "--desc", "d", "--scope", "other")
	if code == 0 {
		t.Errorf("%s: the judged process wrote the ledger, so the lock never bound it:\nstdout: %s\nstderr: %s", state, out, errw)
		return
	}
	for _, want := range append([]string{ledgerWriteRefused}, also...) {
		if !strings.Contains(errw, want) {
			t.Errorf("%s: the refused write lacks %q:\n%s", state, want, errw)
		}
	}
	if strings.Contains(errw, "inspect ledger schema version") {
		t.Errorf("%s: the write control failed at the OPEN, which is the refused rows' shape, not a refused write:\n%s", state, errw)
	}
}

// ledgerGateRow is one access state and the answer measured for it.
type ledgerGateRow struct {
	name string
	// peer: this process holds the ledger open from before the claims are
	// made, so the sidecars are there and the claims are WAL frames only.
	peer bool
	// peerGone: with the lock on, the claims verdict is seen once more while
	// the peer lives, then the peer closes and the row's own answer is asked
	// for. So within one fixture the close is the only thing that changed.
	// stays says what the close leaves beside the ledger: both sidecars (the
	// directory refused their unlinks), or neither.
	peerGone, stays bool
	// lock makes the ledger unwritable to the gate's process, and returns
	// what that process runs under.
	lock func(t *testing.T, g *ledgerGate) ledgerWrap
	// leaves: the gate's own open leaves sidecars behind in this state, so
	// they are removed before each probe.
	leaves bool
	judged bool
	sqlite string   // on a refusal, SQLite's words inside the reason
	also   []string // on a judged row, more the refused write must say
}

func (r ledgerGateRow) run(t *testing.T) {
	g := newLedgerGate(t)
	if r.peer {
		g.holdOpen(t)
	}
	g.claim(t)
	named := g.wantNoSidecars
	if r.peer {
		named = g.wantSidecars
	} else if !g.mainFileHolds(t, "held") {
		// The peer rows' "not in the main file" below is this instrument
		// answering no; here it is seen to answer yes.
		t.Fatal("control: with no peer the claims are in the ledger's main file, and a copy of it must show them")
	}
	// Control, the ledger writable, the same child and no wrap: what a claims
	// verdict looks like from this runner, and proof the child runs the gate
	// and finds this ledger.
	control := "control (" + r.name + ", before the lock)"
	g.wantJudged(t, control, ledgerWrap{}, named)
	g.wantWrites(t, control, named)
	w := r.lock(t, g)
	if r.peer && g.mainFileHolds(t, "held") {
		t.Fatal("the claims reached the ledger's main file, so this row no longer shows a read-only gate reading them from the -wal")
	}
	state := named
	if r.leaves {
		state = g.dropSidecars
	}
	if r.peerGone {
		g.wantJudged(t, r.name+" (while the peer still holds it open)", w, named)
		g.peerCloses(t, r.name)
		state = g.wantNoSidecars
		if r.stays {
			state = g.wantSidecars
		}
	}
	if !r.judged {
		g.wantRefused(t, r.name, w, r.sqlite, state)
		return
	}
	g.wantJudged(t, r.name, w, state)
	g.wantWriteRefused(t, r.name, w, state, r.also...)
	if r.peer && !r.peerGone && g.mainFileHolds(t, "held") {
		// A peer that closed mid-row leaves this row's sidecars where a
		// directory refuses the unlinks, and every probe above passes on them:
		// its close's checkpoint is the one trace (peerCloses).
		t.Errorf("%s: the claims reached the ledger's main file while the row ran, so the peer did not hold the ledger open throughout", r.name)
	}
}

// modes is a file-mode lock: no wrap, the modes bind the child as they bind
// this process.
func modes(file, dir os.FileMode) func(*testing.T, *ledgerGate) ledgerWrap {
	return func(t *testing.T, g *ledgerGate) ledgerWrap {
		t.Helper()
		g.lock(t, file, dir)
		return ledgerWrap{}
	}
}

func TestLedgerAccessGateVerdict(t *testing.T) {
	boundedParallel(t)
	skipAsRoot(t)
	rows := []ledgerGateRow{
		// SQLite makes the sidecars itself (the directory is writable), reads,
		// and LEAVES them, mode 0444 like the ledger: a read-only connection
		// cannot take the lock the last close's checkpoint-and-delete needs.
		// Measured after one gate call and nothing else: -shm 32768 bytes,
		// -wal 0 bytes. So the row is in the state it names for its first
		// probe only, and a deny from the second would prove a read with
		// sidecars PRESENT, which is another row. They are removed before each
		// probe: the directory is writable and no connection is open.
		{name: "file 0444, directory writable, no sidecars", lock: modes(0o444, 0), leaves: true, judged: true},
		// The next two differ in the peer and nothing else: the same lock
		// refuses when the sidecars are absent and cannot be made. Under file
		// modes the failed create is READONLY_DIRECTORY, an OPEN failing for
		// want of a file to create, not a write the gate attempted.
		{name: "file 0444, directory 0555, no sidecars", lock: modes(0o444, 0o555),
			sqlite: "attempt to write a readonly database (1544)"},
		// Sidecars present is what the verdict turns on, and a peer holding
		// the ledger open is one way they are: under file modes no probe can
		// tell a live peer from one that left them behind, so the row ends by
		// asking the main file, where a closed peer's checkpoint would have
		// put the claims.
		{name: "file 0444, directory 0555, a peer holds it open", peer: true, lock: modes(0o444, 0o555), judged: true},
		// And one that left them behind is judged the same: the peer closes,
		// the 0555 directory refuses its unlinks, and the -wal and -shm no
		// connection holds any more still let the gate's open through.
		// Present or creatable sidecars decide, not a live peer.
		{name: "file 0444, directory 0555, the peer closed and its sidecars stayed", peer: true, peerGone: true, stays: true,
			lock: modes(0o444, 0o555), judged: true},
		{name: "file 0000, no sidecars", lock: modes(0, 0),
			sqlite: "unable to open database file (14)"},
	}
	for _, r := range rows {
		t.Run(r.name, r.run)
	}
}

// A wrapper that could not be applied is known by where it says so, never by
// a word found somewhere in the child's output (ledgerWrap.unapplied says what
// that cost). The first row is the control: the recognizer does fire.
func TestLedgerAccessWrapUnapplied(t *testing.T) {
	w := ledgerWrap{argv: []string{"sandbox-exec"}, cannot: seatbeltUnapplied}
	const under = "/tmp/sandbox_apply-review/001/repo"
	for _, tc := range []struct {
		name           string
		w              ledgerWrap
		stdout, stderr string
		code           int
		want           bool
	}{
		{"the wrapper's own diagnostic", w, "", "sandbox-exec: sandbox_apply: Operation not permitted\n", 71, true},
		{"a refused write naming a ledger under such a path", w, "",
			"buddy claim: attempt to write a readonly database (8)\n  cause: this process may not write the ledger " + under + "/.git/buddy.db\n", 1, false},
		{"a gate document naming such a path", w, `{"hookSpecificOutput":{"additionalContext":"BUDDY UNCLAIMED: no claim of this session covers free.go (in ` + under + `)"}}`, "", 0, false},
		{"the diagnostic's words, but not first on stderr", w, "", "buddy claim: sandbox-exec: sandbox_apply: quoted\n", 1, false},
		{"the diagnostic beside output the child made", w, "{}", "sandbox-exec: sandbox_apply: Operation not permitted\n", 71, false},
		{"no wrapper: nothing to be unapplied", ledgerWrap{}, "", "sandbox-exec: sandbox_apply: Operation not permitted\n", 71, false},
	} {
		if got := tc.w.unapplied(tc.stdout, tc.stderr, tc.code); got != tc.want {
			t.Errorf("%s: unapplied = %v, want %v", tc.name, got, tc.want)
		}
	}
}
