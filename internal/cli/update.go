package cli

// BUDDY UPDATE: reinstall from the checkout this binary was installed from, and
// say where things stand first.
//
// The operator asked for it (2026-10-27): once buddy is installed, upgrading
// meant remembering which checkout it came from and that the one upgrade path
// is `sh scripts/install.sh` there (CLAUDE.md, Commands). Asked whether the
// verb should also `git pull`, the operator chose INSTALL ONLY: `buddy` stays
// network-free (CLAUDE.md's repo map: "No network"), so this runs the
// checkout's own install.sh and nothing else, and it tells the user which git
// commands they would run first.
//
// WHERE THE CHECKOUT IS. The user runs this from anywhere, so the cwd says
// nothing. install.sh stamps its checkout's absolute path into every buddy it
// builds (`-ldflags -X …/internal/cli.installedFrom=<root>`). Stamped, not a
// record file beside the binary: a file drifts from the binary it describes
// (a later `go build -o ~/bin/buddy` would inherit a record it did not earn),
// while a stamp travels with exactly the bytes install.sh built and probed. A
// buddy built any other way carries no stamp and is REFUSED, naming the fix,
// never guessed at: the source paths a non-trimpath build embeds would name a
// checkout too, but a plain `go build` in a scratch copy names that copy.
// The stamp is believed only as far as it checks out: it must still exist,
// hold this project's go.mod and its scripts/install.sh.
//
// REPORT FIRST, FROM LOCAL STATE ONLY. The installed build's commit is Go's
// own vcs stamp, read from the FILE install.sh will replace
// (debug/buildinfo.ReadFile of $BUDDY_BIN_DIR/buddy, default ~/bin), which
// is the one the hooks run; the running buddy can be another copy, and the
// report says so when it is. The checkout's HEAD, branch, dirtiness
// and its lag behind local main and origin/main are git reads with no fetch,
// and origin/main is said to be "as of the last fetch", with FETCH_HEAD's age
// when git kept one: nothing here may imply it knows what origin holds NOW.
// Every git child gets cleanGitEnv (no inherited GIT_DIR re-points it),
// GIT_NO_LAZY_FETCH=1 (a partial clone's missing object would otherwise be
// fetched from its promisor remote), core.fsmonitor off (it names a program
// `status` runs; dirty.go) and --no-optional-locks, and the dirtiness read
// switches off every filter driver the checkout configures (dirtyTracked).
//
// TELLS, MOSTLY. A dirty tree and a branch other than main WARN and install:
// the operator asked for guidance, install.sh itself says "UNCOMMITTED
// changes" and installs them, and a lane-tree install by an operator who
// meant it is theirs to make. ONE case refuses: the installed build's commit
// is in this checkout and NOT in HEAD's history. Installing would replace it
// with an older or diverged build, and an older buddy refuses a newer ledger
// (D-037), which turns into a gate that denies every gated call on the
// machine. The refusal names both ways on: move the checkout forward, or run
// install.sh by hand, which is the deliberate act this verb does not take for
// you. An installed commit this checkout does not have cannot be placed, nor
// can one in a shallow checkout, and either is said so, not refused.
//
// NOT A HOOK VERB, and it opens no ledger: it is an install (D-083 lists
// install among what is never delegable), run from a terminal or by an
// orchestrator where it already runs install.sh after a landing.

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/JsizzleR/buddy-system/internal/fence"
)

const usageUpdate = "usage: buddy update [--dry-run]   (reinstall from the checkout this buddy was installed from: report\n" +
	"       where it stands, from local git only, then run its scripts/install.sh; --dry-run reports and installs nothing)"

// installedFrom is the checkout install.sh built this binary in, stamped with
// -ldflags -X. Empty for any other build. A plain var with no initializer:
// -X sets only such a string.
var installedFrom string

// buddyModule is the module path a checkout's go.mod must declare to be this
// project's. A test holds it to the repo's own go.mod.
const buddyModule = "github.com/JsizzleR/buddy-system"

// updateGitBudget bounds each git read. Not a hook: a large tree's status may
// take a while, and a read that times out only says "cannot say".
const updateGitBudget = 10 * time.Second

// updateSource is what cmdUpdate reads from the process, a seam so a test can
// point the verb at a fixture checkout with a stub install.sh and play any
// installed commit, without touching this machine's install.
type updateSource struct {
	checkout string // the stamp
	exe      string // this binary's resolved path, "" when unknown
	sh       string // the shell install.sh runs under
	// buildOf reads the vcs stamp of the Go binary at path; ok=false when
	// there is none there or it is not a Go binary.
	buildOf func(path string) (b builtFrom, ok bool)
}

// builtFrom is a build's own vcs stamp: rev "" when it records none.
type builtFrom struct {
	rev      string
	modified bool
}

func cmdUpdate(args []string, env Env) int {
	src := updateSource{checkout: installedFrom, sh: "/bin/sh", buildOf: readBuilt}
	if p, err := os.Executable(); err == nil {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		src.exe = p
	}
	return runUpdate(args, env, src)
}

// readBuilt is the installed binary's commit, read from the FILE install.sh
// replaces, not from the running process: the hooks run that file, and the
// running buddy may be another copy (a checkout's bin/, a scratch build).
func readBuilt(path string) (builtFrom, bool) {
	bi, err := buildinfo.ReadFile(path)
	if err != nil {
		return builtFrom{}, false
	}
	var b builtFrom
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			b.rev = s.Value
		case "vcs.modified":
			b.modified = s.Value == "true"
		}
	}
	return b, true
}

func runUpdate(args []string, env Env, src updateSource) int {
	refuse := func(err error) int {
		fmt.Fprintf(env.Stderr, "buddy update: %v\n", err)
		return 1
	}
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "")
	if help, err := parseFlags(fs, args, usageUpdate, env); help {
		return 0
	} else if err != nil {
		return refuse(err)
	}
	if err := noStray("update", fs, usageUpdate); err != nil {
		return refuse(err)
	}
	c, err := checkoutOf(src.checkout)
	if err != nil {
		return refuse(err)
	}
	r := assess(c, src, env)
	w := env.Stdout
	for _, l := range r.lines {
		fmt.Fprintln(w, l)
	}
	if len(r.next) > 0 {
		fmt.Fprintln(w, "commands you may want first:")
		for _, l := range r.next {
			fmt.Fprintln(w, "  "+l)
		}
	}
	installCmd := "sh " + shellQuote(filepath.Join(c, "scripts", "install.sh"))
	if r.refusal != "" {
		if *dry {
			fmt.Fprintln(w, "dry run: nothing installed; buddy update would REFUSE:")
		}
		return refuse(errors.New(r.refusal))
	}
	if *dry {
		fmt.Fprintf(w, "dry run: nothing installed; buddy update would run: %s (from %s)\n", installCmd, shellQuote(c))
		return 0
	}
	fmt.Fprintf(w, "running: %s (from %s)\n", installCmd, shellQuote(c))
	cmd := exec.Command(src.sh, filepath.Join(c, "scripts", "install.sh"))
	cmd.Dir = c
	// No stdin: install.sh reads none, and a hook payload a misconfigured
	// hook line would pipe in has no business reaching it (Codex).
	cmd.Stdin = nil
	cmd.Stdout = env.Stdout
	cmd.Stderr = env.Stderr
	// GIT_* removed: install.sh stamps the build from its own checkout's git,
	// and an inherited GIT_DIR (a hook's, a worktree's) would stamp another.
	cmd.Env = cleanGitEnv()
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			rc := ee.ExitCode()
			if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				rc = 128 + int(ws.Signal())
			}
			fmt.Fprintf(env.Stderr, "buddy update: install.sh exited %d\n", rc)
			return rc
		}
		return refuse(fmt.Errorf("could not run %s: %v", installCmd, err))
	}
	return 0
}

// checkoutOf believes the stamp only as far as it checks out. Every refusal
// names the fix, because each means the stamp cannot be followed and only a
// reinstall from a real checkout writes a new one.
func checkoutOf(stamp string) (string, error) {
	const again = "reinstall once from your buddy-system checkout: sh <checkout>/scripts/install.sh"
	if stamp == "" {
		return "", fmt.Errorf("this buddy carries no record of the checkout it was installed from (it was built by something other than scripts/install.sh, or by an install older than buddy update); %s", again)
	}
	if !filepath.IsAbs(stamp) || strings.ContainsFunc(stamp, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", fmt.Errorf("the recorded checkout %s is not a plain absolute path; %s", strconv.Quote(fence.Line(stamp, 256)), again)
	}
	shown := shellQuote(stamp)
	fi, err := os.Stat(stamp)
	if err != nil || !fi.IsDir() {
		return "", fmt.Errorf("the recorded checkout %s is gone (moved or deleted); %s", shown, again)
	}
	mod, err := os.ReadFile(filepath.Join(stamp, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("the recorded checkout %s has no go.mod, so it is not a buddy-system checkout; %s", shown, again)
	}
	if got := modulePath(mod); got != buddyModule {
		return "", fmt.Errorf("the recorded checkout %s declares module %s, not %s; %s", shown, strconv.Quote(fence.Line(got, 128)), buddyModule, again)
	}
	// Lstat: a symlink there would run a script outside the checkout (Codex).
	if fi, err := os.Lstat(filepath.Join(stamp, "scripts", "install.sh")); err != nil || !fi.Mode().IsRegular() {
		return "", fmt.Errorf("the recorded checkout %s has no scripts/install.sh as a regular file; %s", shown, again)
	}
	return stamp, nil
}

// modulePath is go.mod's module directive, "" when there is none. The
// directive's own grammar is one path, optionally quoted, after "module".
func modulePath(mod []byte) string {
	for _, l := range strings.Split(string(mod), "\n") {
		l = strings.TrimSpace(l)
		if i := strings.Index(l, "//"); i >= 0 {
			l = strings.TrimSpace(l[:i])
		}
		rest, ok := strings.CutPrefix(l, "module")
		if !ok || rest == "" || (rest[0] != ' ' && rest[0] != '\t' && rest[0] != '"') {
			continue
		}
		rest = strings.TrimSpace(rest)
		if u, err := strconv.Unquote(rest); err == nil {
			return u
		}
		return rest
	}
	return ""
}

// updateGit is one read-only git command in the checkout, with no fetch of any
// kind possible (see the header).
func updateGit(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), updateGitBudget)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir, "-c", "core.fsmonitor=false", "--no-optional-locks"}, args...)...)
	cmd.Env = append(cleanGitEnv(), "GIT_NO_LAZY_FETCH=1")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

// lagOf is commits only on a, then only on b. ok=false when git cannot place
// either.
func lagOf(dir, a, b string) (onlyA, onlyB int, ok bool) {
	out, err := updateGit(dir, "rev-list", "--left-right", "--count", a+"..."+b)
	if err != nil {
		return 0, 0, false
	}
	f := strings.Fields(out)
	if len(f) != 2 {
		return 0, 0, false
	}
	x, e1 := strconv.Atoi(f[0])
	y, e2 := strconv.Atoi(f[1])
	return x, y, e1 == nil && e2 == nil
}

func lagWords(ahead, behind int, of string) string {
	if ahead == 0 && behind == 0 {
		return "even with " + of
	}
	return fmt.Sprintf("%d ahead, %d behind %s", ahead, behind, of)
}

type updateReport struct {
	lines   []string
	next    []string
	refusal string
}

// assess reads the checkout and the installed build, and says what it saw,
// what the user may want to run first, and whether the install must refuse.
func assess(c string, src updateSource, env Env) updateReport {
	var r updateReport
	add := func(f string, a ...any) { r.lines = append(r.lines, fmt.Sprintf(f, a...)) }
	q := shellQuote(c)
	gitc := "git -C " + q
	now := time.Now()
	if env.Now != nil {
		now = env.Now()
	}

	add("buddy update: from the checkout this buddy was installed from, %s (read locally; nothing fetched)", q)

	// The installed build: the file install.sh replaces, which is the one
	// the hooks run (README spells $HOME/bin/buddy; BUDDY_BIN_DIR moves both).
	binDir := env.getenv("BUDDY_BIN_DIR")
	if binDir == "" {
		if h, err := os.UserHomeDir(); err == nil {
			binDir = filepath.Join(h, "bin")
		}
	} else if !filepath.IsAbs(binDir) {
		binDir = filepath.Join(c, binDir)
	}
	var inst builtFrom
	if binDir == "" {
		add("  installed  no home directory, so where install.sh puts buddy cannot be said")
	} else {
		target := filepath.Join(binDir, "buddy")
		var ok bool
		if inst, ok = src.buildOf(target); !ok {
			add("  installed  no buddy build at %s to compare with; install.sh puts one there", shellQuote(target))
		} else if isSHA(inst.rev) {
			mod := ""
			if inst.modified {
				mod = " plus uncommitted changes"
			}
			add("  installed  %s: built from %s%s", shellQuote(target), inst.rev[:8], mod)
		} else {
			add("  installed  %s: records no commit (built without git's stamp), so what it carries cannot be said", shellQuote(target))
		}
		resolved := target
		if t, err := filepath.EvalSymlinks(target); err == nil {
			resolved = t
		}
		repoBin := filepath.Join(c, "bin", "buddy")
		if t, err := filepath.EvalSymlinks(repoBin); err == nil {
			repoBin = t
		}
		if src.exe != "" && src.exe != resolved && src.exe != repoBin {
			add("  note       this buddy, %s, is not that file: install.sh leaves it as it is", shellQuote(src.exe))
		}
	}

	// The checkout.
	head, err := updateGit(c, "rev-parse", "--verify", "-q", "HEAD^{commit}")
	if err != nil || !isSHA(head) {
		add("  checkout   HEAD cannot be read (not a git checkout, or no commit yet)")
		r.next = append(r.next, "check the checkout: "+gitc+" status")
		return r
	}
	branch, berr := updateGit(c, "symbolic-ref", "-q", "--short", "HEAD")
	on := "detached"
	if berr == nil && branch != "" {
		on = "on branch " + fence.Field(branch, 128)
	}
	dirty := "clean"
	st, serr := dirtyTracked(c)
	switch {
	case serr != nil:
		dirty = "dirtiness cannot be read"
	case st != "":
		dirty = "UNCOMMITTED changes to tracked files: the install builds them, not " + head[:8]
		r.next = append(r.next, "commit or set aside the changes in "+q+" if you meant to install "+head[:8]+": "+gitc+" status")
	}
	add("  checkout   HEAD %s, %s; %s", head[:8], on, dirty)

	// Local main.
	mainName, mainSHA := "", ""
	for _, n := range []string{"main", "master"} {
		if out, err := updateGit(c, "rev-parse", "--verify", "-q", "refs/heads/"+n+"^{commit}"); err == nil && isSHA(out) {
			mainName, mainSHA = n, out
			break
		}
	}
	onMain := berr == nil && branch == mainName && mainName != ""
	if mainName == "" {
		add("  main       no local main or master branch to compare with")
	} else if a, b, ok := lagOf(c, head, mainSHA); !ok {
		add("  main       HEAD cannot be placed against local %s", mainName)
	} else {
		add("  main       HEAD is %s", lagWords(a, b, "local "+mainName))
		if !onMain {
			r.next = append(r.next, fmt.Sprintf("the install builds %s, not %s; to install %s: %s switch %s, then buddy update", strings.TrimPrefix(on, "on "), mainName, mainName, gitc, mainName))
		}
	}

	// origin's main, as git last recorded it. Never "current". Looked for
	// even with no local main (Codex): a checkout whose main was deleted
	// still has origin's.
	oName, osha := mainName, ""
	for _, n := range []string{mainName, "main", "master"} {
		if n == "" {
			continue
		}
		if out, err := updateGit(c, "rev-parse", "--verify", "-q", "refs/remotes/origin/"+n+"^{commit}"); err == nil && isSHA(out) {
			oName, osha = n, out
			break
		}
	}
	mainName = oName
	if osha == "" {
		add("  origin     no origin/main or origin/master recorded here; nothing to compare with")
	} else {
		asOf := fetchedWhen(c, mainName, now)
		if a, b, ok := lagOf(c, head, osha); !ok {
			add("  origin     HEAD cannot be placed against origin/%s (%s)", mainName, asOf)
		} else {
			add("  origin     HEAD is %s, %s; it may have moved since", lagWords(a, b, "origin/"+mainName), asOf)
			if b > 0 {
				if onMain {
					r.next = append(r.next, fmt.Sprintf("%d commit(s) on origin/%s are not in HEAD: %s pull --ff-only, then buddy update again", b, mainName, gitc))
				} else {
					r.next = append(r.next, fmt.Sprintf("%d commit(s) on origin/%s are not in HEAD; it is on main that a pull brings them: %s switch %s && %s pull --ff-only, then buddy update again", b, mainName, gitc, mainName, gitc))
				}
			}
		}
		r.next = append(r.next, "to see whether origin has moved since: "+gitc+" fetch, then buddy update --dry-run")
	}

	// The installed build against HEAD: the one refusal (see the header). A
	// shallow checkout's counts stop at its boundary, so an upgrade can count
	// as a downgrade there (Codex): placed only in a complete history.
	if isSHA(inst.rev) {
		if _, err := updateGit(c, "cat-file", "-e", inst.rev+"^{commit}"); err != nil {
			add("  install    the installed commit %s is not in this checkout, so it cannot be placed against HEAD", inst.rev[:8])
		} else if sh, _ := updateGit(c, "rev-parse", "--is-shallow-repository"); sh != "false" {
			add("  install    this checkout's history is shallow (or unreadable), so the installed commit %s cannot be placed against HEAD", inst.rev[:8])
		} else if only, ahead, ok := lagOf(c, inst.rev, head); !ok {
			add("  install    the installed commit %s cannot be placed against HEAD", inst.rev[:8])
		} else if only > 0 {
			add("  install    the installed commit %s carries %d commit(s) HEAD does not", inst.rev[:8], only)
			r.refusal = fmt.Sprintf("the installed buddy (%s) carries %d commit(s) that HEAD %s in %s does not: installing would replace it with an older or diverged build, and an older buddy refuses a newer ledger (D-037), so the gate would deny every gated call. "+
				"Move the checkout to a commit that contains %s (%s pull --ff-only, or switch to the branch it came from), then buddy update; or, to install this tree anyway, run: sh %s",
				inst.rev[:8], only, head[:8], q, inst.rev[:8], gitc, shellQuote(filepath.Join(c, "scripts", "install.sh")))
		} else if ahead == 0 && !inst.modified {
			add("  install    the installed build is HEAD already; install.sh rebuilds it anyway, with the skill, the daemon and this checkout's wiring")
		} else if ahead == 0 {
			add("  install    the installed build is HEAD plus uncommitted changes; install.sh rebuilds from the tree as it is now")
		} else {
			add("  install    brings %d commit(s) the installed build does not have", ahead)
		}
	}

	return r
}

// fetchedWhen says when origin's main was last seen, in words, without ever
// implying now. FETCH_HEAD is believed only when it records origin's <name>:
// a later fetch of another remote or branch rewrites it (Codex), and its age
// would then date an older observation of origin/<name>. The newer of the
// worktree's own and the common directory's, since a fetch in the main
// checkout writes the common one and a linked worktree's --git-path names
// its own (measured, git 2.54). Otherwise the ref's own reflog: when it last
// CHANGED, which is older than or equal to the last fetch that saw it.
func fetchedWhen(c, name string, now time.Time) string {
	url, _ := updateGit(c, "config", "--get", "remote.origin.url")
	var newest time.Time
	if url != "" {
		if out, err := updateGit(c, "rev-parse", "--path-format=absolute", "--git-path", "FETCH_HEAD", "--git-common-dir"); err == nil {
			f := strings.Split(out, "\n")
			paths := []string{f[0]}
			if len(f) == 2 {
				paths = append(paths, filepath.Join(f[1], "FETCH_HEAD"))
			}
			want := "branch '" + name + "' of " + url
			for _, p := range paths {
				fi, err := os.Stat(p)
				if err != nil || !fi.ModTime().After(newest) {
					continue
				}
				b, err := os.ReadFile(p)
				if err != nil {
					continue
				}
				for _, l := range strings.Split(string(b), "\n") {
					if strings.HasSuffix(l, "\t"+want) {
						newest = fi.ModTime()
						break
					}
				}
			}
		}
	}
	if !newest.IsZero() {
		return "as of the last fetch, " + age(now, newest) + " ago"
	}
	// %gd with --date=unix is the reflog ENTRY's time (ref@{<seconds>}); %ct
	// would be the commit's, which can be weeks older than the fetch.
	if out, err := updateGit(c, "log", "-g", "-n1", "--date=unix", "--format=%gd", "refs/remotes/origin/"+name); err == nil {
		_, at, _ := strings.Cut(out, "@{")
		if sec, err := strconv.ParseInt(strings.TrimSuffix(at, "}"), 10, 64); err == nil {
			return "as of the last fetch; origin/" + name + " last changed here " + age(now, time.Unix(sec, 0)) + " ago"
		}
	}
	return "as of the last fetch (when that was, git did not record)"
}

// dirtyTracked is `git status --porcelain --untracked-files=no` with every
// configured filter driver switched off for this one call. status compares
// a changed file's content through its clean filter, a program the
// checkout's config names, so the report, dry run included, would otherwise
// run it (Codex: a `filter.<x>.clean` that touches a file, or fetches).
// Config is READ to find the drivers, which runs nothing; a driver name a
// -c override cannot spell (an "=" or a line break) leaves dirtiness unread
// rather than risk it. Submodules are not entered: their own config could
// name filters this call never saw.
func dirtyTracked(c string) (string, error) {
	names, err := updateGit(c, "config", "--name-only", "--get-regexp", `^filter\..*\.(clean|smudge|process)$`)
	if err != nil && names != "" {
		return "", err
	}
	args := []string{}
	seen := map[string]bool{}
	for _, k := range strings.Split(names, "\n") {
		if k == "" {
			continue
		}
		d := strings.TrimPrefix(k, "filter.")
		d = d[:strings.LastIndex(d, ".")]
		if strings.ContainsAny(d, "=\r\n") {
			return "", fmt.Errorf("filter driver %q cannot be switched off", d)
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		for _, f := range []string{"clean", "smudge", "process"} {
			args = append(args, "-c", "filter."+d+"."+f+"=")
		}
		args = append(args, "-c", "filter."+d+".required=false")
	}
	args = append(args, "status", "--porcelain", "--untracked-files=no", "--ignore-submodules=all")
	return updateGit(c, args...)
}
