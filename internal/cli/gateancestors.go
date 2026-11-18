package cli

// gateancestors.go — every DISTINCT governed ancestor of a directory applies
// its claims and its brake additively (#145, D-226).
//
// WHY. D-212 made the first ledger at or above a directory govern it, so a
// nested repository with NO ledger keeps the enclosing repository's claims and
// brake. One WITH a ledger was still its own: governed R, `R/vendor/gov`
// `git init` + `buddy init`, A holding `vendor` in R; B's Edit of
// `R/vendor/gov/x.c` was DENIED from cwd R (D-073 F-03, the home claim
// additive) and ALLOWED from cwd `R/vendor/gov` and from a plain directory,
// and R's `pause all` did not stop B's `touch f` from the nested cwd
// (reproduced at 1bfdf45). The same escape needs no `buddy init` inside R at
// all: a worktree of another governed repository added under R, a governed
// repository copied in, a nested repository inited before R, a ledger-bearing
// BARE repository in R, a nested ledger whose core.worktree is R. So the rule
// is applied where every other jurisdiction rule lives, at the gate, and not
// in `init` (the shape the operator ruled out, 2026-11-18 05:20Z via orc-7:
// it closed one route and left those five open).
//
// THE WALK. From the PHYSICAL directory a verdict is settled for (the hook
// cwd, a target's deepest existing directory, a reached `cd`), never from the
// governing root: walking above `top` missed R when the nested ledger's
// core.worktree made its top R, and a bare governing repository has no top
// (panel lenses a and b). Each .git entry is identified by its COMMON
// DIRECTORY, resolved without git where commonDirOf can, and compared by
// os.SameFile, never by the ledger file (a symlinked `buddy.db` into R's is a
// different repository sharing a file, and R's claims still apply there) and
// never by `<top>/.git`. Per entry, nearest first:
//   - first, an entry whose ledger this process may not look for (a
//     permission error on the way to its buddy.db, entryUnknown) is
//     collected as an error and stepped past with no git (#146);
//   - a common directory already met: the same ledger at another root. Placed
//     there too, UNLESS the nearest entry of that ledger is a REGISTERED
//     linked worktree of it (registeredWorktree), which is then its only
//     root: R's own `R/.claude/worktrees/<lane>` is not double-placed against
//     R's root (the operator's Q2 (i), amending D-073 F-03). A hand-written
//     `gitdir: ../../.git` or a `.git` symlink to R's is no registration and
//     is placed against both roots.
//   - a ledger proved absent without git (ledgerAbsentAt): skipped;
//   - otherwise git is asked, with D-212's arms: a ledger with a root is
//     collected; a rootless one (a bare-layout `proj/.git`) passes; "no ledger"
//     a successful discovery proved passes; a failed discovery where Lstat
//     sees a ledger is "cannot tell", and so is a successful one that
//     PASSED the entry for another repository while a ledger is behind it
//     (passedEntry); any other failure is collected as an error. An error
//     denies wherever it applies (invariant 3).
// A target's walk stops at the home's own entry (gateCache.atHome): the home
// arms read the home's claims, its brake and ITS ancestors (cmdGate).
//
// THE CACHE (panel lens c, Codex design pass C1 and C4). Discoveries are cached
// per entry path, so an entry git had to be asked about is asked once per call
// however many keys walk through it (four ledgerless repositories under one
// unprovable entry took 133.6 ms at base); ledger handles, with the caller's
// brake in them, are cached by common-directory identity, so one ledger is
// opened once per call; an ancestor SET is cached per discovery context (the
// key and the governing ledger), never per ledger: a worktree of an outside
// repository S placed inside R shares S's ledger but not S's ancestors.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

// ancestor is one governed ancestor ledger placed against one root.
type ancestor struct {
	st     *store.Store
	cd     os.FileInfo // its common directory
	top    string      // the root its claims are placed against
	paused string      // its brake for the caller, or ""
	err    error       // exists and cannot be used: denies
	at     string      // the entry's directory, named in an error
}

// handle is one open ledger, by common-directory identity.
type handle struct {
	cd     os.FileInfo
	st     *store.Store
	paused string
	owned  bool // closed by gateCache.closeAll (the home's is cmdGate's)
}

type discovery struct {
	rc  repoContext
	err error
}

// metLedger is a common directory the walk has met, and whether its nearest
// entry was a registered linked worktree of it.
type metLedger struct {
	cd         os.FileInfo
	h          *handle
	met        bool
	registered bool
}

// handleFor returns the open handle of the ledger whose common directory is
// cd, or nil.
func (c *gateCache) handleFor(cd os.FileInfo) *handle {
	if cd == nil {
		return nil
	}
	for _, hd := range c.handles {
		if os.SameFile(hd.cd, cd) {
			return hd
		}
	}
	return nil
}

// addHandle records an open ledger by its common directory.
func (c *gateCache) addHandle(ledger string, st *store.Store, paused string, owned bool) *handle {
	fi, err := os.Stat(filepath.Dir(ledger))
	if err != nil {
		if owned {
			// Not shareable: closeAll still owns it.
			c.orphans = append(c.orphans, st)
		}
		return &handle{st: st, paused: paused}
	}
	hd := &handle{cd: fi, st: st, paused: paused, owned: owned}
	c.handles = append(c.handles, hd)
	return hd
}

// closeAll closes every store this cache opened, each once.
func (c *gateCache) closeAll() {
	done := map[*store.Store]bool{}
	closeOnce := func(st *store.Store) {
		if st != nil && !done[st] {
			done[st] = true
			st.Close()
		}
	}
	for _, hd := range c.handles {
		if !hd.owned && hd.st != nil {
			done[hd.st] = true // the home's: cmdGate closes it
		}
	}
	for _, repo := range c.m {
		closeOnce(repo.st)
	}
	for _, hd := range c.handles {
		closeOnce(hd.st)
	}
	for _, st := range c.orphans {
		closeOnce(st)
	}
}

// discover runs git's discovery from an entry's directory once per call.
func (c *gateCache) discover(entry string) (repoContext, error, bool) {
	if d, ok := c.disc[entry]; ok {
		return d.rc, d.err, true
	}
	if c.limit > 0 && c.opens >= c.limit {
		return repoContext{}, nil, false
	}
	rc, err := gateResolve(filepath.Dir(entry))
	c.opens++
	if c.disc == nil {
		c.disc = map[string]discovery{}
	}
	c.disc[entry] = discovery{rc, err}
	return rc, err, true
}

// commonDirIdentity is the entry's common directory, resolved without git
// (a symlinked .git DIRECTORY through its target, as ledgerAbsentAt does), or
// nil where that cannot be known: git is then asked.
func commonDirIdentity(entry string) os.FileInfo {
	if fi, err := os.Lstat(entry); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(entry)
		if err != nil {
			return nil
		}
		if tfi, err := os.Stat(target); err != nil || !tfi.IsDir() {
			return nil // a symlinked .git FILE: where git resolves it is git's
		}
		entry = target
	}
	common, ok := commonDirOf(entry)
	if !ok {
		return nil
	}
	fi, err := os.Stat(common)
	if err != nil {
		return nil
	}
	return fi
}

// registeredWorktree reports whether the .git entry is a REGISTERED linked
// worktree of the common directory cd whose root git's discovery reported as
// top: its `gitdir:` resolves physically to <cd>/worktrees/<n>, that
// directory's `gitdir` file names this entry back, and git's discovery from
// the entry SUCCEEDED with this entry's directory as the root (the caller's
// top). Two reciprocal files alone are no registration (Codex design pass
// C3): a session can write them, and a stale registration or a lone leftover
// pointer keeps the enclosing placement. The admin HEAD is not checked here:
// git's discovery refuses an admin directory without one, and a check of it
// was a mutant no test could kill. A session that writes under
// R/.git can rewrite R's ledger anyway (invariant 6); the exception is the
// operator's ruling (Q2 (i)) and is forgeable by design.
func registeredWorktree(entry string, cd os.FileInfo, top string) bool {
	if cd == nil || top == "" {
		return false
	}
	fi, err := os.Lstat(entry)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > 4096 {
		return false
	}
	b, err := os.ReadFile(entry)
	if err != nil {
		return false
	}
	rest, ok := strings.CutPrefix(strings.TrimRight(string(b), "\r\n"), "gitdir: ")
	if !ok || rest == "" || strings.ContainsAny(rest, "\n") {
		return false
	}
	if !filepath.IsAbs(rest) {
		rest = filepath.Dir(entry) + "/" + rest // raw, as commonDirOf joins
	}
	admin, err := filepath.EvalSymlinks(rest)
	if err != nil || filepath.Base(filepath.Dir(admin)) != "worktrees" {
		return false
	}
	if wfi, err := os.Stat(filepath.Dir(filepath.Dir(admin))); err != nil || !os.SameFile(wfi, cd) {
		return false
	}
	back, err := os.ReadFile(admin + "/gitdir")
	if err != nil {
		return false
	}
	p := strings.TrimRight(string(back), "\r\n")
	if p == "" || strings.ContainsAny(p, "\n") {
		return false
	}
	if !filepath.IsAbs(p) {
		p = admin + "/" + p
	}
	// The back-pointer names THIS entry: the same directory and the name
	// .git. Compared by the directories, never by the files: a hard-linked
	// copy of a lane's .git file shares its inode, and passed as the lane
	// (Codex code pass).
	pfi, err := os.Stat(filepath.Dir(p))
	efi, err2 := os.Stat(filepath.Dir(entry))
	if err != nil || err2 != nil || filepath.Base(p) != ".git" || !os.SameFile(pfi, efi) {
		return false
	}
	tfi, err := os.Stat(top)
	dfi, err2 := os.Stat(filepath.Dir(entry))
	return err == nil && err2 == nil && os.SameFile(tfi, dfi)
}

// gateAncestors returns the governed ancestors of start, whose governing
// ledger is gov (its handle in gov.st, its common directory from
// gov.rc.ledger). home marks the hook cwd's own walk, which does not stop at
// the home entry it starts in. exhausted reports that the caller's discovery
// budget ran out mid-walk: what was read is returned, nothing is cached.
func gateAncestors(h hookInput, env Env, start string, gov gateRepo, home bool, c *gateCache) (out []ancestor, exhausted bool) {
	var ckey [2]string // the key and the governing ledger, never joined into one string: a path may hold any separator (Codex code pass)
	if !home {
		_, key := repoKeyForDir(start)
		ckey = [2]string{key, gov.rc.ledger}
		if a, ok := c.anc[ckey]; ok {
			return a, false
		}
	}
	var met []*metLedger
	if fi, err := os.Stat(filepath.Dir(gov.rc.ledger)); err == nil {
		hd := c.handleFor(fi)
		if hd == nil {
			hd = &handle{cd: fi, st: gov.st, paused: gov.paused}
		}
		met = append(met, &metLedger{cd: fi, h: hd})
	}
	find := func(cd os.FileInfo) *metLedger {
		if cd == nil {
			return nil
		}
		for _, m := range met {
			if os.SameFile(m.cd, cd) {
				return m
			}
		}
		return nil
	}
	// remeet places an already-met ledger at entry's root: never where its
	// nearest entry is a registered worktree, and not at all for the
	// governor's own first entry (the governor's placement is the caller's).
	remeet := func(m *metLedger, entry string, top string) {
		if !m.met {
			m.met = true
			m.registered = registeredWorktree(entry, m.cd, gov.rc.top)
			return
		}
		if m.registered || top == "" {
			return
		}
		out = append(out, ancestor{st: m.h.st, cd: m.cd, top: top, paused: m.h.paused, at: filepath.Dir(entry)})
	}
	entry, ok := nearestGitEntry(start)
	for ok {
		if !home && c.home != nil && c.atHome(entry) {
			break
		}
		if uerr := entryUnknown(entry); uerr != nil {
			// First, before the identity lookup and any git (#146; panel
			// lens c1): governed P/r with a mode-000 .git inside governed P
			// was discovered as P, re-met as P, and r's claims never read.
			// The deny is this entry's; the walk steps on with no discover.
			out = append(out, ancestor{err: uerr, at: filepath.Dir(entry)})
		} else if m := find(commonDirIdentity(entry)); m != nil {
			top := ""
			if m.met && !m.registered {
				rc, err, ran := c.discover(entry)
				if !ran {
					return out, true
				}
				if err == nil {
					err = passedEntry(entry, rc)
				}
				if err != nil {
					out = append(out, ancestor{err: err, at: filepath.Dir(entry)})
				}
				top = rc.top
			}
			remeet(m, entry, top)
		} else if !ledgerAbsentAt(entry) {
			rc, err, ran := c.discover(entry)
			if !ran {
				return out, true
			}
			if err == nil {
				// git passed this entry for a repository above while a
				// ledger sits behind it (#146): the default arm would place
				// that repository's ledger here instead.
				err = passedEntry(entry, rc)
			}
			switch {
			case errors.Is(err, errNoLedger) && ledgerSeenAt(entry):
				out = append(out, ancestor{at: filepath.Dir(entry), err: ledgerThereErr(entry)})
			case errors.Is(err, errNoLedger):
				// No ledger seen: walked past (D-212's arm).
			case err != nil:
				out = append(out, ancestor{err: err, at: filepath.Dir(entry)})
			default:
				cd, serr := os.Stat(filepath.Dir(rc.ledger))
				if serr != nil {
					out = append(out, ancestor{err: serr, at: filepath.Dir(entry)})
					break
				}
				if m := find(cd); m != nil {
					remeet(m, entry, rc.top) // git found a ledger already met
					break
				}
				hd := c.handleFor(cd)
				if hd == nil {
					st, oerr := openLedgerAt(rc, env)
					switch {
					case errors.Is(oerr, errNoLedger):
						// proved absent by the discovery itself: passed
					case oerr != nil:
						out = append(out, ancestor{err: oerr, at: filepath.Dir(entry)})
					default:
						hd = c.addHandle(rc.ledger, st, gateBrake(h, gateRepo{st: st, rc: rc}).paused, true)
					}
				}
				if hd == nil {
					break
				}
				m := &metLedger{cd: cd, h: hd, met: true, registered: registeredWorktree(entry, cd, rc.top)}
				met = append(met, m)
				if rc.top != "" {
					// A rootless ledger (a bare-layout proj/.git) places
					// nothing and is passed, as D-212 passes it.
					out = append(out, ancestor{st: hd.st, cd: cd, top: rc.top, paused: hd.paused, at: filepath.Dir(entry)})
				}
			}
		}
		dir := filepath.Dir(entry)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		entry, ok = nearestGitEntry(parent)
	}
	if !home {
		if c.anc == nil {
			c.anc = map[[2]string][]ancestor{}
		}
		c.anc[ckey] = out
	}
	return out, false
}

// ancestorBrake is the deny an ancestor's error or brake gives, or "".
func ancestorBrake(h hookInput, a ancestor) string {
	switch {
	case a.err != nil:
		return fmt.Sprintf("buddy: the ledger at %s, which governs this call from above, is unavailable (%s); refusing %s. Fix what the error names, or ask the operator; never delete a ledger.",
			fence.Line(a.at, 512), fence.Line(a.err.Error(), 512), fencedTool(h))
	case a.paused != "":
		return a.paused
	}
	return ""
}

// ancestorClaims places cp against each ancestor's root and denies on a held
// claim. outsideDenies is set where the ancestors were found BY walking up
// from cp's own directory, so a placement outside the root is a spelling the
// gate cannot resolve, refused as gateForeignRepo refuses it; for the home's
// ancestors an outside target is simply not under that root.
func ancestorClaims(h hookInput, env Env, cp string, anc []ancestor, outsideDenies bool, c *gateCache) string {
	for _, a := range anc {
		if reason := ancestorBrake(h, a); reason != "" {
			return reason
		}
		rel, cased, outside := placeCanonical(a.top, cp)
		if outside {
			if outsideDenies {
				return fmt.Sprintf("buddy: %s is under a buddy-governed repo (%s) but could not be placed inside it; refusing %s rather than guessing",
					fence.Line(cp, 512), fence.Line(a.top, 512), fencedTool(h))
			}
			continue
		}
		if inRegisteredWorktree(h, env, cp, a.cd, a.top, c) {
			continue
		}
		if reason := denyIfHeld(a.st, h, env, rel, a.top, c, a.top, cased); reason != "" {
			return reason
		}
	}
	return ""
}

// inRegisteredWorktree reports whether cp lies in a registered linked
// worktree of the ledger whose common directory is cd, nested below root:
// that worktree's root, not root, is where the ledger's claims are placed
// (Q2 (i)). The nearest entry of that ledger between cp and root decides.
func inRegisteredWorktree(h hookInput, env Env, cp string, cd os.FileInfo, root string, c *gateCache) bool {
	if cd == nil {
		return false
	}
	rfi, err := os.Stat(root)
	if err != nil {
		return false
	}
	entry, ok := nearestGitEntry(existingDir(cp))
	for ok {
		dir := filepath.Dir(entry)
		if dfi, err := os.Stat(dir); err == nil && os.SameFile(dfi, rfi) {
			return false
		}
		if ecd := commonDirIdentity(entry); ecd != nil && os.SameFile(ecd, cd) {
			_, key := repoKeyForDir(dir)
			repo := openGateRepo(h, env, dir, key, c)
			if repo.err != nil || repo.walked || repo.off() {
				return false
			}
			return registeredWorktree(entry, cd, repo.rc.top)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		entry, ok = nearestGitEntry(parent)
	}
	return false
}

// mergeAncestors appends b's ancestors not already in a (the same ledger
// placed at the same root).
func mergeAncestors(a, b []ancestor) []ancestor {
	out := a
	for _, x := range b {
		dup := false
		for _, y := range a {
			if x.err == nil && y.err == nil && x.st == y.st && x.top == y.top {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, x)
		}
	}
	return out
}

// openShared is openRepo with the call's open handles looked up between its
// halves (git's discovery, then the ledger open): the D-212 walk reopened a
// ledger an ancestor walk already held (Codex code pass). shared marks a
// store some other record owns, which the caller must not close. A nil cache
// is openRepo through gateResolve.
func (c *gateCache) openShared(dir string, env Env) (*store.Store, repoContext, error, bool) {
	rc, err := gateResolve(dir) // the gate's discovery (gateResolve says why)
	if c == nil {
		st, rc, err := openResolved(rc, err, env)
		return st, rc, err, false
	}
	if err != nil {
		return nil, rc, err, false
	}
	if cd, serr := os.Stat(filepath.Dir(rc.ledger)); serr == nil {
		if hd := c.handleFor(cd); hd != nil {
			return hd.st, rc, nil, true
		}
	}
	st, err := openLedgerAt(rc, env)
	if err != nil && rc.gone != "" && !errors.Is(err, errNoLedger) {
		err = vanishedErr(rc.gone, rc.from, err)
	}
	return st, rc, err, false
}
