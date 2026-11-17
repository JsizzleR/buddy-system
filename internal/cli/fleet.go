package cli

// THE FLEET LEDGER (D-191; docs/NON-GIT-PLAN.md, stage 1b).
//
// WHAT IT IS FOR. Outside git, buddy was off, completely and silently (the
// plan's N0): every hook verb exits 0 and prints nothing, every typed verb
// exits 1 "not a git repository". The operator's use is about ten INDEPENDENT
// investigations, each in its own plain directory, watched and relayed from one
// coordinator's pane. Those sessions share no repository, so they share no
// ledger, and the harness alone keeps no queue that outlives a turn, no
// declared "waiting, and on what", no outcome with a note, no address that
// survives a coordinator's handoff, and no channel a Codex lane can use (the
// plan's section 4). A fleet ledger is a COORDINATION-ONLY ledger outside every
// project: names, messages, waits and outcomes.
//
// HOW A SESSION FINDS ONE (plan 5.2). Discovery from the filesystem runs
// exactly as before (openRepo). A ledger found there, or one found and
// unreadable, is the session's, and BUDDY_FLEET takes no part in choosing it.
// Only on errNoLedger (provably not a repository, or a repository never `buddy
// init`ed), and only for the five lifecycle hooks and the typed verbs of the
// plan's section 6, does BUDDY_FLEET choose a ledger: unset or empty is
// today's verdict, byte for byte; set, it names
// <HOME>/.buddy/fleets/<name>/buddy.db. (It is READ in more places than it
// chooses: Run's scrub, before discovery; hello's not-used line, status's
// LEDGER line, busy's roster line and the ledger-access explanation, beside a
// home ledger. None of those opens the fleet ledger; the panel's docs lens,
// finding 2, caught the older "read only where" wording.) The
// variable rides the lane's tab at launch the way BUDDY_LABEL and
// BUDDY_HANDOFF_AT ride it (D-089, D-064): one getenv after git said "no
// ledger". Refused alternatives, each in the plan's 5.1: a ledger PATH in the
// environment (5.3: a path can name a repository's own ledger, makes whichever
// binary reads it migrate whatever it names past D-136, and invites the next
// reader to honour it in the gate, D-137's bug under a buddy name; BUDDY_LEDGER
// stays a knob of two reports that buddy never reads); a per-session file
// (stage 2's `join`); a registry keyed by directory, a marker in the
// directory, one default fleet for every session outside a ledger (D-010:
// defaults are narrow), and a ledger above the repositories' (BEYOND-CODE,
// invariants 1, 4, 6).
//
// THE GATE NEVER OPENS ONE (plan 5.4). openFleetLedger is called by
// openSessionLedger, which only the lifecycle hooks, noteHeard and the section-6
// typed verbs call. It is NOT inside openRepo, resolveRepo, discoverRepo or
// openLedgerAt, which cmdGate, openGateRepo and cmdCommitGate share, so
// BUDDY_FLEET, a fleet ledger's rows and a fleet ledger's unreadability can
// change no gate verdict. TestGateNeverReachesTheFleetLedger holds that on
// internal/cli's call graph, from Run's dispatch down; the verdict-table tests
// in fleet_test.go hold it on the gate's output. And nothing a gate could act
// on can be written into one: claim takes only slot scopes and never gh-stack,
// pause and resume refuse (fleetRefusal's callers).
//
// INVARIANT 3, READ FOR A LEDGER THAT IS NOT A SAFETY FEATURE (D-191). "No
// ledger" and "unreadable" stay two verdicts with two different sentences, in
// the two places something reads them: the SessionStart context (hello, and
// busy at every prompt while the failure lasts) and every typed verb's exit.
// Neither is a DENY, on purpose: a DENY is the gate's, and the gate has no part
// in this. The fleet ledger guards nothing, so a closed failure would protect
// nothing, and a refusal keyed on an environment variable is the bug D-137
// closed. Invariant 2's "a ledger that exists but cannot be read denies" speaks
// of the safety hooks, and none reads this ledger. What must not happen is the
// silent collapse, which is why hello and busy print on STDOUT: the wired
// lifecycle lines end `2>/dev/null; exit 0` (README), so stderr and the exit
// status reach nobody.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

// EnvFleet names the fleet a session coordinates in when its working directory
// has no ledger of its own (D-191). Set on the lane's tab at launch (`herdr tab
// create --env BUDDY_FLEET=<name>`); `claude --bg` carries no variable, so a
// fleet lane is a herdr tab. The gate never reads it.
const EnvFleet = "BUDDY_FLEET"

// fleetHomeRel is where fleet ledgers live, under $HOME: <HOME>/.buddy/fleets/
// <name>/buddy.db. The ONE constant behind the location. It is the plan's
// example (NON-GIT-PLAN section 10, question 2), which the operator accepted by
// proceeding and did not name: a later choice changes this line, and a fleet
// home on a synced or network volume is refused in the recipe (the fleet
// skill), not here, because SQLite under a tool that copies a live database is
// NOT MEASURED.
const fleetHomeRel = ".buddy/fleets"

// fleetNameRE is a fleet name: 1 to 64 of a-z, 0-9 and '-', starting with a
// letter or digit. It can spell no path (no '/', no '.', so no `..` and no
// `.git`), and it is matched EXACTLY: not trimmed, not folded. Upper case is
// malformed rather than folded so two spellings can never alias one directory
// on the case-insensitive default volume, and a trailing space pasted into a
// tab's environment is loud at the lane's first turn rather than silently
// honoured (the plan's 5.1).
var fleetNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// fleetKind is which of 5.2's three failure rows a fleet lookup landed in.
type fleetKind int

const (
	fleetMalformed   fleetKind = iota + 1 // BUDDY_FLEET is not a fleet name
	fleetAbsent                           // provably no such fleet here: an ENOENT, nothing else
	fleetUnavailable                      // it may exist and cannot be opened or read
)

// fleetErr is a fleet lookup that did not open a ledger. Its Error is the one
// sentence 5.2's table gives the row; hello prints it, busy repeats it at each
// prompt, and a typed verb exits 1 with it. Every interpolated value is fenced:
// the name is the environment's and the path and cause carry it.
type fleetErr struct {
	kind  fleetKind
	name  string // BUDDY_FLEET as given
	path  string // the ledger path looked for; "" when none could be formed
	cause error  // fleetUnavailable only
	// opened: the ledger opened and a read or write after it failed. Its own
	// sentence (the panel's invariant-3 lens, finding 3): a SQLITE_BUSY past
	// busy_timeout is no symlink and no missing directory, and the lookup's
	// "fix the fleet home" sentence sent the session to repair what was sound.
	opened bool
	// beside: the fleet was looked for BESIDE a home ledger the session still
	// uses (stage 2, D-193). "On no roster and gets no mail" would then be
	// false: the session is on its directory's roster and gets that mail, so
	// the sentence says what is lost is the fleet's (the Codex design pass,
	// Q4: qualify the failure as the fleet's roster and mail only).
	beside bool
}

// rosterLoss is what a fleet that did not open costs the session, in the
// sentence's words.
func (e fleetErr) rosterLoss() string {
	if e.beside {
		return "this session is on no fleet roster and gets no fleet mail (this directory's own ledger works as before)"
	}
	return "this session is on no roster and gets no mail"
}

func (e fleetErr) Unwrap() error { return e.cause }

func (e fleetErr) Error() string {
	name := fence.Line(e.name, 64)
	switch e.kind {
	case fleetMalformed:
		return fmt.Sprintf("%s=%s is not a fleet name (1 to 64 of a-z, 0-9 and '-', starting with a letter or digit): %s; relaunch it with a well-formed name",
			EnvFleet, strconv.Quote(name), e.rosterLoss())
	case fleetAbsent:
		return fmt.Sprintf("%s=%s names no fleet ledger on this machine (looked in %s): %s; its coordinator creates it with `buddy init --fleet %s`",
			EnvFleet, name, fence.Line(e.path, 512), e.rosterLoss(), name)
	}
	where := ""
	if e.path != "" {
		where = " at " + fence.Line(e.path, 512)
	}
	if e.opened {
		return fmt.Sprintf("fleet ledger error (%s): the fleet %s%s opened and then a read or write failed, so this session's last change to it may not have been recorded. It is usually transient (another session holding the write lock): the next hook or verb retries. If it repeats, ask the operator; never delete it, it holds every lane's messages, waits and outcomes. No tool call is refused for this: a fleet ledger guards nothing",
			fence.Line(causeText(e.cause), 1024), name, where)
	}
	return fmt.Sprintf("fleet ledger unavailable (%s): the fleet %s%s cannot be opened or read, so %s. Fix what the cause names (the fleet home, the fleet's directory and its buddy.db must be the user's own real directories and file, not symlinks), or ask the operator; never delete it, it holds every lane's messages, waits and outcomes. No tool call is refused for this: a fleet ledger guards nothing",
		fence.Line(causeText(e.cause), 1024), name, where, e.rosterLoss())
}

func causeText(err error) string {
	if err == nil {
		return "unknown"
	}
	return err.Error()
}

// quiet reports the rows where beat, idle and bye stay silent and exit 0 (5.2):
// a malformed name and an absent fleet are said by hello once and by busy at
// every prompt; beat runs on every tool call and would only repeat it.
func (e fleetErr) quiet() bool { return e.kind == fleetMalformed || e.kind == fleetAbsent }

// verbErr is the error a typed verb exits 1 with. A malformed name ends
// `refused: ...` (D-098's register for a refusal a caller reads through a
// pipe's last line); the other two are the sentence itself.
func (e fleetErr) verbErr() error {
	if e.kind == fleetMalformed {
		return errors.New("refused: " + e.Error())
	}
	return e
}

// fleetLine is the one line hello and busy print, on stdout, for a fleet that
// did not open, or for a fleet ledger that opened and then failed (Codex design
// pass, finding 2: a SQLite file with the right user_version and no tables
// opens, and HelloFrom fails after it; returned as an error, that diagnostic
// went to the stderr the wired line drops). "" for any error that is not a
// fleet's.
func fleetLine(err error) string {
	var fe fleetErr
	if errors.As(err, &fe) {
		return "BUDDY: " + fe.Error() + "\n"
	}
	return ""
}

// fleetOpsErr is a failure AFTER a fleet ledger opened, as the unavailable row.
func fleetOpsErr(rc repoContext, err error) error {
	var fe fleetErr
	if errors.As(err, &fe) {
		return fe
	}
	return fleetErr{kind: fleetUnavailable, name: rc.fleet, path: rc.ledger, cause: err, opened: true}
}

// fleetLedgerPath checks the managed path component by component and returns
// the ledger's path when it is all there, or the verdict that stopped it.
//
// WHY NOT ONE Lstat OF THE LEDGER (Codex design pass, finding 1). Lstat
// follows every component but the last, so <home>/.buddy/fleets/f as a symlink
// to /work/R/.git reached R's own buddy.db: `BUDDY_FLEET=f buddy release c`
// from outside git then released a peer's real claim in R and turned R's gate
// from DENY to ALLOW. A name under one fixed home must reach only ledgers
// `init --fleet` made (the plan's 5.3, reason 2), so the fleet home and the
// fleet's directory must be real directories, the ledger a regular file, and
// the fleet's directory not a git directory (a HEAD entry, which every git
// directory holds). <HOME>/.buddy may be a symlink (it holds the operator's
// other files, briefs among them) but not a dangling one. And a dangling
// symlink anywhere is UNAVAILABLE, never absent: the final Lstat answers ENOENT
// for a missing fleet and for a dangling parent alike, and only the first is
// provably absent (invariant 3's discipline: only a positive ENOENT is "none").
// The window between this walk and the open is the TOCTOU the ledger already
// accepts (invariant 6: it trusts the machine user).
func fleetLedgerPath(env Env, name string) (string, error) {
	home := env.getenv("HOME")
	if home == "" || !filepath.IsAbs(home) {
		return "", fleetErr{kind: fleetUnavailable, name: name,
			cause: fmt.Errorf("$HOME is %s, not an absolute path, so there is no fleet home", strconv.Quote(fence.Line(home, 256)))}
	}
	// $HOME itself must be an existing directory (Codex code pass, finding
	// 6): a HOME that is missing, or a symlink that does not resolve, gave
	// the walk below its ENOENT at <HOME>/.buddy and so "absent", which tells
	// the session to run `init --fleet` where init cannot work. Only an
	// ENOENT under a home that is there proves the fleet is not.
	if hi, err := os.Stat(home); err != nil {
		return "", fleetErr{kind: fleetUnavailable, name: name,
			cause: fmt.Errorf("$HOME (%s) is not an existing directory: %w", fence.Line(home, 256), err)}
	} else if !hi.IsDir() {
		return "", fleetErr{kind: fleetUnavailable, name: name,
			cause: fmt.Errorf("$HOME (%s) is not a directory", fence.Line(home, 256))}
	}
	base := filepath.Join(home, ".buddy")
	fleets := filepath.Join(home, fleetHomeRel)
	dir := filepath.Join(fleets, name)
	ledger := filepath.Join(dir, ledgerName)
	absent := fleetErr{kind: fleetAbsent, name: name, path: ledger}
	bad := func(err error) error {
		return fleetErr{kind: fleetUnavailable, name: name, path: ledger, cause: err}
	}
	// <HOME>/.buddy: a real directory, or a symlink to one.
	fi, err := os.Lstat(base)
	switch {
	case errors.Is(err, syscall.ENOENT):
		return "", absent
	case err != nil:
		return "", bad(err)
	case fi.Mode()&os.ModeSymlink != 0:
		ti, serr := os.Stat(base)
		if serr != nil {
			return "", bad(fmt.Errorf("%s is a symlink that does not resolve: %w", base, serr))
		}
		if !ti.IsDir() {
			return "", bad(fmt.Errorf("%s is not a directory", base))
		}
	case !fi.IsDir():
		return "", bad(fmt.Errorf("%s is not a directory", base))
	}
	// The fleet home and the fleet's own directory: real directories only.
	for _, d := range []string{fleets, dir} {
		fi, err := os.Lstat(d)
		switch {
		case errors.Is(err, syscall.ENOENT):
			return "", absent
		case err != nil:
			return "", bad(err)
		case fi.Mode()&os.ModeSymlink != 0:
			return "", bad(fmt.Errorf("%s is a symlink; the fleet home and each fleet must be real directories, so a name can reach only a ledger `buddy init --fleet` made", d))
		case !fi.IsDir():
			return "", bad(fmt.Errorf("%s is not a directory", d))
		}
	}
	if err := gitMarks(dir); err != nil {
		return "", bad(err)
	}
	// Its sidecars too, each absent or a regular file with one name (the
	// Fable cross-check, finding 1, measured): `ln R/.git/buddy.db-wal
	// <fleet>/buddy.db-wal` while a repository connection was open passed the
	// check above, `BUDDY_FLEET=f buddy ls` listed the repository's file
	// claims, a release appended its frames to R's WAL, and the fleet's close
	// checkpointed R's pages into the fleet's file. SQLite opens these by
	// name beside the ledger, so they are the ledger as much as buddy.db is.
	// Checked BEFORE the ledger's own ENOENT (Codex re-verify, round 2,
	// finding 1): with buddy.db absent the walk said "absent", init went on to
	// create the ledger, and SQLite attached a hard-linked -shm, truncating a
	// repository ledger's shared memory through the alias.
	for _, side := range []string{"-wal", "-shm", "-journal"} {
		p := ledger + side
		si, err := os.Lstat(p)
		switch {
		case errors.Is(err, syscall.ENOENT):
			continue
		case err != nil:
			return "", bad(err)
		case !si.Mode().IsRegular():
			return "", bad(fmt.Errorf("%s is not a regular file (a symlink, or something else); SQLite opens it as part of the fleet ledger", p))
		}
		if st, ok := si.Sys().(*syscall.Stat_t); ok && uint64(st.Nlink) != 1 {
			return "", bad(fmt.Errorf("%s has %d names (hard links); SQLite opens it as part of the fleet ledger, and a second name for it may be a repository's", p, st.Nlink))
		}
	}
	fi, err = os.Lstat(ledger)
	switch {
	case errors.Is(err, syscall.ENOENT):
		return "", absent
	case err != nil:
		return "", bad(err)
	case !fi.Mode().IsRegular():
		return "", bad(fmt.Errorf("%s is not a regular file (a symlink, or something else); a fleet ledger is the file `buddy init --fleet` made", ledger))
	}
	// One name only (Codex code pass, finding 1): a hard link is a regular
	// file under the fleet's real directory, and `ln /w/R/.git/buddy.db
	// <fleet>/buddy.db` passed every check above while every write went to
	// R's own ledger, the alias the symlink checks exist to stop. A ledger
	// init made has one link; SQLite's -wal and -shm are files of their own.
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && uint64(st.Nlink) != 1 {
		return "", bad(fmt.Errorf("%s has %d names (hard links); a fleet ledger is the one file `buddy init --fleet` made, and a second name for it may be a repository's own ledger", ledger, st.Nlink))
	}
	return ledger, nil
}

// gitDirMarks are the entries a git directory, or a git COMMON directory,
// holds and a fleet's directory never does. A deny-list and not an allowlist
// of {buddy.db, -wal, -shm, -journal}: Finder writes .DS_Store into any
// directory a user opens, and a fleet must not go unavailable for that.
//
// HEAD alone was the first check (the design pass). The Codex code pass
// (finding 1) made a fleet's directory a repository's COMMON directory without
// one: objects/ and refs/ there, and a linked worktree's gitdir whose
// `commondir` names it, so buddy in that worktree resolved its ledger to the
// fleet's buddy.db, and `BUDDY_FLEET=f buddy release --session <peer>` freed a
// claim the repository's gate honours. commondir and gitdir are the files a
// linked worktree's own git directory holds.
var gitDirMarks = []string{"HEAD", "objects", "refs", "commondir", "gitdir"}

// gitMarks refuses a directory holding any of gitDirMarks; nil when none.
func gitMarks(dir string) error {
	for _, m := range gitDirMarks {
		_, err := os.Lstat(filepath.Join(dir, m))
		switch {
		case err == nil:
			return fmt.Errorf("%s holds %s, the mark of a git directory; a fleet's directory holds only its ledger", dir, m)
		case !errors.Is(err, syscall.ENOENT):
			return err
		}
	}
	return nil
}

// openFleetLedger opens the fleet BUDDY_FLEET names. errNoLedger when it is
// unset or empty: today's verdict, byte for byte (5.2, step 3). Otherwise a
// fleetErr, or the open ledger and a repoContext whose fleet is the name and
// whose top is "" (a fleet has no root: invariant 10 is kept by recording no
// path, and the row's worktree column holds the cwd at hello, shown and never
// used to place a path).
//
// store.OpenEscaped, so D-136's migration rule (only $HOME/bin/buddy, or
// BUDDY_MIGRATE=1, migrates an existing ledger) and ErrLedgerNewer apply to a
// fleet ledger exactly as to any other, and the file opened is the file the
// walk checked: percent-escaped, so a `?` in $HOME cannot end the name early
// and land on a repository's ledger (Codex code pass, finding 2), and with
// create false, so a file removed after the walk is an error and never a new
// empty ledger (the panel's lifecycle lens, finding 4). No hook or verb creates
// a fleet ledger: one that created what it failed to find would turn a
// mistyped name into a private ledger and a lane that reports to nobody (5.1).
// Only `buddy init --fleet` makes the file. (A zero-byte file a user leaves at
// that exact name is a different case: SQLite reads it as an empty database
// and the first open gives it the schema, as it would any ledger path; the
// panel's invariant-3 lens, finding 5. Nothing but init or the user puts a
// file there.)
//
// KEPT SEPARATE from openSessionLedger for stage 2 (a session with both a home
// and a fleet ledger, the next item): that stage opens both, and calls this
// half on its own.
// fleetFindable is openFleetLedger's name check and path walk without the
// open: whether name is a fleet ledger on this machine, for who's LEDGER line
// beside a home (D-193). It reaches fleetLedgerPath, so the gate's call-graph
// scan forbids it with no entry of its own.
//
// Its error is beside's: who's line is printed only where a home ledger is in
// use, and "this session is on no roster and gets no mail" denied the home's
// roster and mail, which stand (the Codex re-verify, round 1, finding 2).
func fleetFindable(env Env, name string) error {
	if !fleetNameRE.MatchString(name) {
		return fleetErr{kind: fleetMalformed, name: name, beside: true}
	}
	_, err := fleetLedgerPath(env, name)
	if fe := (fleetErr{}); errors.As(err, &fe) {
		fe.beside = true
		err = fe
	}
	return err
}

func openFleetLedger(env Env) (*store.Store, repoContext, error) {
	name := env.getenv(EnvFleet)
	if name == "" {
		return nil, repoContext{}, errNoLedger
	}
	if !fleetNameRE.MatchString(name) {
		return nil, repoContext{}, fleetErr{kind: fleetMalformed, name: name}
	}
	ledger, err := fleetLedgerPath(env, name)
	if err != nil {
		return nil, repoContext{}, err
	}
	rc := repoContext{ledger: ledger, fleet: name}
	st, err := store.OpenEscaped(ledger, env.Now, false)
	if err != nil {
		return nil, rc, fleetErr{kind: fleetUnavailable, name: name, path: ledger, cause: err}
	}
	return st, rc, nil
}

// openSessionLedger is openRepo for the five lifecycle hooks, noteHeard and the
// typed verbs of the plan's section 6: the session's own ledger. openRepo
// first; only its errNoLedger reads the fleet. A found ledger and an unreadable
// one come back exactly as openRepo gave them (5.2, step 1). With BUDDY_FLEET
// unset or empty, openRepo's own errNoLedger and repoContext come back, so a
// typed verb still says "no ledger at <path>" where it did (Codex design pass,
// Q5).
//
// NEVER CALLED BY THE GATE. Nothing reachable from Run's dispatch of `gate` or
// `commit-gate` may name this function or openFleetLedger
// (TestGateNeverReachesTheFleetLedger).
func openSessionLedger(dir string, env Env) (*store.Store, repoContext, error) {
	if env.fleetOnly {
		// `buddy fleet <verb>` (D-193): the fleet and nothing else, whatever
		// the directory or git's variables would find. Unset, it is refused
		// before the verb runs (cmdFleetVerb); errNoLedger here is that row.
		return openFleetLedger(env)
	}
	st, rc, err := openRepo(dir, env)
	if !errors.Is(err, errNoLedger) {
		return st, rc, err
	}
	fst, frc, ferr := openFleetLedger(env)
	if errors.Is(ferr, errNoLedger) {
		return nil, rc, err
	}
	return fst, frc, ferr
}

// mustFleetLedger is mustLedger for the typed verbs of section 6: the same
// words where no ledger is found and BUDDY_FLEET is unset, and the fleet's own
// words where it is set and its ledger does not open.
func mustFleetLedger(dir string, env Env) (*store.Store, repoContext, error) {
	st, rc, err := openSessionLedger(dir, env)
	var fe fleetErr
	if errors.As(err, &fe) {
		return nil, rc, fe.verbErr()
	}
	return verbLedger(dir, st, rc, err)
}

// fleetVerbs are the typed verbs of the plan's section 6, the ones that read a
// fleet ledger where no home ledger is found. lifecycle and adapter (the Codex
// adapter's register, D-069), diagnose (never opens a ledger with SQLite,
// D-072), init (creates where discovery lands), update, uninstall and
// commit-gate stay on the repository alone, and so does every hook verb's own
// dispatch: the five lifecycle hooks call openSessionLedger themselves and the
// gate never does.
var fleetVerbs = map[string]bool{
	"claim": true, "release": true, "ls": true, "sweep": true, "pause": true,
	"resume": true, "msg": true, "sent": true, "inbox": true, "sessions": true,
	"whose": true, "status": true, "who": true, "authority": true, "ids": true,
	"wait": true,
}

// fleetScrub reports whether Run unsets git's local environment for a typed
// verb, as it always does for a hook verb (D-137): exactly when BUDDY_FLEET is
// non-empty and the verb is one of section 6's.
//
// WHY (the plan's 9.1 row, decided in the Codex design pass). A lane outside
// git with BUDDY_FLEET=f and a GIT_DIR/GIT_WORK_TREE inherited from a dotfiles
// shell registers in f through its (scrubbed) hooks, while an unscrubbed `buddy
// msg orc-f` or `buddy release <item>` reads the repository GIT_DIR names: two
// ledgers for one session, and the typed verb acts in the wrong one. A LEDGER
// line on status names it but does not protect the verbs. So with BUDDY_FLEET
// set, a typed verb discovers exactly as the session's hooks do, from the
// filesystem: `cd` selects a repository, and a caller who means to target one
// through git's variables unsets BUDDY_FLEET. What it costs, said once: under
// BUDDY_FLEET, an intentional GIT_DIR=<A> from repository B is ignored and the
// verb acts in B. With BUDDY_FLEET unset, D-137's recorded open stands
// unchanged. commit-gate is never in the set, so a partial commit's temporary
// GIT_INDEX_FILE survives (the charter's git facts).
func fleetScrub(cmd string, env Env) bool {
	return fleetVerbs[cmd] && env.getenv(EnvFleet) != ""
}

// fleetRefusal is the refusal for a verb, or a verb's flag, that has nothing
// to act on in a fleet ledger (the plan's section 6). rc.fleet is "" outside a
// fleet, and then it refuses nothing.
func fleetRefusal(rc repoContext, what, why string) error {
	if rc.fleet == "" {
		return nil
	}
	return fmt.Errorf("refused: %s in fleet %s: %s", what, fence.Line(rc.fleet, 64), why)
}

// fleetNoBrake is pause's and resume's refusal (5.4): a fleet ledger carries
// no brake, because a brake there would need the gate to honour a ledger the
// environment names, the widening this plan was told not to make (its question
// 3). Refused, and not taken as N7 took it, so the roster cannot say PAUSED
// while every write goes through.
const fleetNoBrake = "a fleet ledger carries no brake: the gate never reads it, so a pause here would stop nothing while the roster said PAUSED. Stop a lane in its own pane (Esc, or close its tab)"

// fleetNoPaths is whose's and authority's refusal: both place paths under a
// worktree root, and a fleet has none.
const fleetNoPaths = "a fleet ledger keeps no paths: it has no worktree root to place one under"

// checkFleetScopes is claim's admission rule in a fleet (section 6, Codex
// design pass Q4): every scope a STRICT descendant of .buddy/slot after the
// store's own normalization and fold, none of them gh-stack or under it (the
// one slot the gate reads in a repository's ledger, D-108), and neither
// --shared (a slot is capacity one, D-035; and shared claims exist for files
// every lane appends to, which a fleet does not reserve) nor --based-on (there
// is no run to be built on). Run before the dry-run branch, so a forecast
// refuses with the claim's own words.
//
// Normalized first because the decisive predicate is on what the store would
// key: `.buddy/slot/../x` is `.buddy/x`, `.buddy\slot\gh-stack` is the gh-stack
// slot, and `.buddy/slot/` is the slot root, which covers gh-stack (the
// gh-stack gate accepts a covering ancestor). isSlot alone admits that root.
func checkFleetScopes(rc repoContext, scopes []string, shared, basedOn bool) error {
	if rc.fleet == "" {
		return nil
	}
	if shared {
		return fleetRefusal(rc, "--shared", "a fleet ledger reserves no file, and a slot is one session's at a time; claim a slot of your own")
	}
	if basedOn {
		return fleetRefusal(rc, "--based-on", "a fleet ledger integrates nothing, so there is no run to be built on")
	}
	gh := store.Fold(ghStackSlot)
	for _, s := range scopes {
		n, err := store.NormalizeScope(s)
		if err != nil {
			return fleetRefusal(rc, "scope "+strconv.Quote(fence.Line(s, 128)), "a fleet ledger takes only .buddy/slot/<name> scopes ("+fence.Line(err.Error(), 256)+")")
		}
		f := store.Fold(n)
		switch {
		case !strings.HasPrefix(f, slotPrefix+"/"):
			return fleetRefusal(rc, "scope "+strconv.Quote(fence.Line(s, 128)),
				"a fleet ledger reserves no file: no gate reads it and a path here has no root, so a claim on one would be announced, not locked (invariant 1). Claim a slot: --scope .buddy/slot/<name>")
		case f == gh || strings.HasPrefix(f, gh+"/"):
			return fleetRefusal(rc, "scope "+strconv.Quote(fence.Line(s, 128)),
				".buddy/slot/gh-stack is the one slot the gate reads in a repository's ledger (D-108), and nothing a gate could act on is written into a fleet's")
		}
	}
	return nil
}

// cmdInitFleet creates <HOME>/.buddy/fleets/<name>/buddy.db, or says it is
// there, and prints the environment to launch each lane with (section 6, the
// one new flag). The directories are made 0700 (the plan's 5.2: the fleet home
// is the user's own; not a tenancy boundary, invariant 6), and an existing one
// with group or other bits is tightened to 0700 and said so: Mkdir's mode
// applies only to a directory it creates (Codex design pass, Q5). It adopts no
// directory a lookup would refuse: a symlinked fleet home or fleet, a git
// directory, a ledger that is not a regular file or has a second name, so init
// and every lookup agree on what a fleet is.
//
// ORDER: the whole check before any change (the panel's docs lens, finding
// 3). The first version tightened a directory to 0700 and then refused it, a
// silent change to a directory it would not adopt. And the refusal is init's
// own sentence (the refusals lens, finding 7): the lookup's says "this session
// is on no roster", which is not what an operator creating a fleet is told.
func cmdInitFleet(name string, env Env) error {
	if !fleetNameRE.MatchString(name) {
		return usageRefusal(fmt.Sprintf("%s is not a fleet name: 1 to 64 of a-z, 0-9 and '-', starting with a letter or digit (it can spell no path)",
			strconv.Quote(fence.Line(name, 64))), usageInit)
	}
	home := env.getenv("HOME")
	if home == "" || !filepath.IsAbs(home) {
		return fmt.Errorf("$HOME is %s, not an absolute path, so there is no fleet home; nothing was created", strconv.Quote(fence.Line(home, 256)))
	}
	fleets := filepath.Join(home, fleetHomeRel)
	dir := filepath.Join(fleets, name)
	ledger := filepath.Join(dir, ledgerName)
	refuse := func(cause error) error {
		return fmt.Errorf("refused: init --fleet %s: %s; nothing was created or changed. A fleet's home and directory must be the user's own real directories holding only its ledger, so a name can reach only a ledger this verb made",
			fence.Line(name, 64), fence.Line(causeText(cause), 1024))
	}
	// The same walk every lookup makes. Absent is the one verdict init goes
	// on from; any other names what is wrong, before anything is changed.
	existed := false
	if _, err := fleetLedgerPath(env, name); err == nil {
		existed = true
	} else {
		var fe fleetErr
		if !errors.As(err, &fe) {
			return refuse(err)
		}
		if fe.kind != fleetAbsent {
			return refuse(fe.cause)
		}
	}
	var lax []string // existing directories with group or other bits
	for _, d := range []string{fleets, dir} {
		fi, err := os.Lstat(d)
		switch {
		case errors.Is(err, syscall.ENOENT):
			// MkdirAll for the fleet home: <HOME>/.buddy may not exist yet.
			// Parents it makes get 0700 too; an existing <HOME>/.buddy is
			// the operator's and is left as it is.
			if err := os.MkdirAll(d, 0o700); err != nil {
				return fmt.Errorf("could not create %s: %w", fence.Line(d, 512), err)
			}
		case err != nil:
			return fmt.Errorf("could not read %s: %w", fence.Line(d, 512), err)
		case fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir():
			// The walk refused these already; kept so the chmod below can
			// never follow a link made since.
			return refuse(fmt.Errorf("%s is not a real directory", d))
		case fi.Mode().Perm()&0o077 != 0:
			lax = append(lax, d)
		}
	}
	// Escaped, as every fleet open is (Codex code pass, finding 2): a `?` in
	// $HOME must not end the name and open a repository's ledger instead of
	// creating this one.
	st, err := store.OpenEscaped(ledger, env.Now, true)
	if err != nil {
		return fencedOpenErr(err)
	}
	st.Close()
	// Tightened only after the open succeeded (Codex re-verify, round 1,
	// finding 2): a ledger the walk accepts can still be refused by the open
	// (a newer schema, ErrLedgerNewer), and a chmod before it changed a
	// directory init then refused, with the note never printed.
	for _, d := range lax {
		fi, err := os.Lstat(d)
		if err != nil {
			return fmt.Errorf("could not read %s: %w", fence.Line(d, 512), err)
		}
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			return refuse(fmt.Errorf("%s is not a real directory", d))
		}
		if err := os.Chmod(d, 0o700); err != nil {
			return fmt.Errorf("%s is mode %#o and could not be made 0700: %w", fence.Line(d, 512), fi.Mode().Perm(), err)
		}
		fmt.Fprintf(env.Stdout, "tightened %s from mode %#o to 0700\n", fence.Line(d, 512), fi.Mode().Perm())
	}
	verb := "fleet ledger ready"
	if existed {
		verb = "fleet ledger already there"
	}
	fmt.Fprintf(env.Stdout, "%s: %s\n", verb, fence.Line(ledger, 512))
	fmt.Fprintf(env.Stdout, "launch the coordinator and each lane with %s=%s in its environment (a herdr tab: --env %s=%s), in a directory with no ledger of its own; `claude --bg` carries no variable. The gate never reads a fleet ledger: it holds names, messages, waits and outcomes, and no file claim or brake\n",
		EnvFleet, name, EnvFleet, name)
	return nil
}

// onceFlag is --fleet's value: a second --fleet is refused, never silently
// the last one (the refusals lens, finding 6: `init --fleet a --fleet b`
// created b while the operator may have read a).
type onceFlag struct {
	v   string
	set bool
}

func (o *onceFlag) String() string { return o.v }

func (o *onceFlag) Set(s string) error {
	if o.set {
		return errors.New("--fleet given twice; give one name")
	}
	o.v, o.set = s, true
	return nil
}

// initArgs parses init's arguments: none (a repository's ledger, as always),
// or exactly --fleet <name>.
//
// A bare `--` is refused before the parse (Codex code pass, finding 7): init
// took no arguments at all before this flag, so `buddy init --` was refused;
// the flag package reads `--` as the end of the flags and leaves nothing
// stray, which made it a repository init.
func initArgs(args []string, env Env) (fleet string, help bool, err error) {
	for _, a := range args {
		if a == "--" {
			return "", false, usageRefusal("init takes no positional arguments, and a bare -- is not one of its flags; nothing was created", usageInit)
		}
	}
	var fl onceFlag
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.Var(&fl, "fleet", "create the fleet ledger <HOME>/"+fleetHomeRel+"/<name>/buddy.db")
	fs.SetOutput(io.Discard)
	if help, err := parseFlags(fs, args, usageInit, env); help || err != nil {
		return "", help, err
	}
	if err := noStray("init", fs, usageInit); err != nil {
		return "", false, err
	}
	if fl.set && fl.v == "" {
		return "", false, usageRefusal("--fleet needs a name; nothing was created", usageInit)
	}
	return fl.v, false, nil
}

// quietFleet reports a fleet that did not open in a row where beat, idle and
// bye stay silent (5.2): a malformed name or an absent fleet.
func quietFleet(err error) bool {
	var fe fleetErr
	return errors.As(err, &fe) && fe.quiet()
}

// busyRosterNote is busy's line for a session the ledger it opened does not
// know while BUDDY_FLEET is set, "" otherwise (and always "" with BUDDY_FLEET
// unset: today's prompt, byte for byte). Two rows a panel found silent:
//
//   - The fleet opened and this session is not on its roster (the
//     invariant-3 lens, finding 1): hello ran before `init --fleet`, or met an
//     unavailable fleet that was fixed since. Its mail goes nowhere and no
//     peer sees it, and nothing said so after hello's one line.
//   - A repository's own ledger governs here and this session is not on it
//     (the lifecycle lens, finding 1): a lane registered in its fleet and then
//     cd'd into a repository, or the repository was `buddy init`ed after.
//     Before stage 2 its fleet row stopped moving and went GONE with its slot
//     claim open. Stage 2 (D-193) keeps the fleet row moving (the hooks beat
//     both ledgers), so what is left to say is the home's half: this
//     directory's peers do not see the session and its mail here goes nowhere
//     until it registers.
//
// A known session gets nothing: hello's own not-used line covered it once.
// other says the session's OTHER ledger serves it this prompt (D-193): for the
// first row, a home ledger beside the fleet, whose mail and peers still work
// (the panel's gate lens: "it gets no mail" was false there); for the second,
// a fleet that opened and has not failed, the only case in which its row
// still moves (the docs lens: a fleet that did not open got that sentence
// beside its own "on no fleet roster" one, in one document).
func busyRosterNote(rc repoContext, session string, known bool, env Env, other bool) string {
	f := env.getenv(EnvFleet)
	if known || f == "" {
		return ""
	}
	if rc.fleet != "" && other {
		return fmt.Sprintf("BUDDY: this session is not on fleet %s's roster (its SessionStart ran before the fleet ledger was there, or could not open it): it gets no fleet mail and no fleet peer sees it (this directory's own ledger works as before). %s\n",
			fence.Line(rc.fleet, 64), helloRemedy(session, "registers it in both"))
	}
	if rc.fleet != "" {
		return fmt.Sprintf("BUDDY: this session is not on fleet %s's roster (its SessionStart ran before the fleet ledger was there, or could not open it): it gets no mail and no peer sees it. %s\n",
			fence.Line(rc.fleet, 64), helloRemedy(session, "registers it"))
	}
	if !other {
		return fmt.Sprintf("BUDDY: this session is not on the roster of this directory's own ledger (%s): it started outside it, or before `buddy init` here. Peers here do not see it and its mail here goes nowhere. %s\n",
			fence.Line(rc.ledger, 512), helloRemedy(session, "registers it here"))
	}
	return fmt.Sprintf("BUDDY: this session is not on the roster of this directory's own ledger (%s): it started outside it, or before `buddy init` here. Its fleet %s row still moves, but peers here do not see it and its mail here goes nowhere. %s\n",
		fence.Line(rc.ledger, 512), fence.Line(f, 64), helloRemedy(session, "registers it in both"))
}
