package cli

// BUDDY UNINSTALL: run the scripts/uninstall.sh of the checkout this binary was
// installed from (D-125), the way `buddy update` runs its install.sh (D-110).
//
// The operator asked for it (2026-10-31), and for a way to undo chat enabled by
// accident: `--chat` removes the chat half only and leaves the install
// claims-only, so the next `buddy update` does not bring it back. The work is
// the SCRIPT's (its header says why it is shell and in what order it removes
// things); this verb only finds the checkout from anywhere, by the stamp
// install.sh built into this binary, believed as far as checkoutOf checks it
// out, plus a scripts/uninstall.sh there as a regular file (Lstat: a symlink
// would run a script outside the checkout). A stamp that cannot be followed is
// REFUSED, naming the way round it: any clone's own scripts/uninstall.sh does
// the same work from its knobs, so a deleted checkout is not a dead end.
//
// The script removes the very binary running this verb, LAST, by one unlink:
// this process keeps its mapping, and waits for the script and returns its
// status. NOT A HOOK VERB and it opens no ledger: removing buddy is an install
// act (D-083), the operator's from a terminal, never a lane's.

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

const usageUninstall = "usage: buddy uninstall [--chat] [--dry-run]   (run the scripts/uninstall.sh of the checkout this buddy was\n" +
	"       installed from: remove what install.sh installed and report what it leaves; --chat removes the chat\n" +
	"       half only and keeps buddy, claims-only from then on; --dry-run prints the plan and removes nothing)"

// uninstallSource is what cmdUninstall reads from the process: a seam so a
// test can point the verb at a fixture checkout with a stub script.
type uninstallSource struct {
	checkout string // the stamp
	sh       string // the shell the script runs under
}

func cmdUninstall(args []string, env Env) int {
	return runUninstall(args, env, uninstallSource{checkout: installedFrom, sh: "/bin/sh"})
}

func runUninstall(args []string, env Env, src uninstallSource) int {
	refuse := func(err error) int {
		fmt.Fprintf(env.Stderr, "buddy uninstall: %v\n", err)
		return 1
	}
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	chat := fs.Bool("chat", false, "")
	dry := fs.Bool("dry-run", false, "")
	if help, err := parseFlags(fs, args, usageUninstall, env); help {
		return 0
	} else if err != nil {
		return refuse(err)
	}
	if err := noStray("uninstall", fs, usageUninstall); err != nil {
		return refuse(err)
	}
	const without = "to uninstall without it, run scripts/uninstall.sh from any buddy-system clone (git clone https://github.com/JsizzleR/buddy-system): sh <clone>/scripts/uninstall.sh [--chat] [--dry-run]"
	c, err := checkoutOf(src.checkout)
	if err != nil {
		return refuse(fmt.Errorf("%v. Or, %s", err, without))
	}
	script := filepath.Join(c, "scripts", "uninstall.sh")
	if fi, err := os.Lstat(script); err != nil || !fi.Mode().IsRegular() {
		return refuse(fmt.Errorf("the recorded checkout %s has no scripts/uninstall.sh as a regular file (a checkout older than buddy uninstall: pull it); %s", shellQuote(c), without))
	}
	argv := []string{script}
	if *chat {
		argv = append(argv, "--chat")
	}
	if *dry {
		argv = append(argv, "--dry-run")
	}
	fmt.Fprintf(env.Stdout, "running: sh %s%s (from %s)\n", shellQuote(script), flagWords(argv[1:]), shellQuote(c))
	cmd := exec.Command(src.sh, argv...)
	cmd.Dir = c
	// No stdin, as update: the script reads none, and a hook payload has no
	// business reaching it. GIT_* removed: the script's reports read its own
	// checkout's git (D-057).
	cmd.Stdin = nil
	cmd.Stdout = env.Stdout
	cmd.Stderr = env.Stderr
	cmd.Env = cleanGitEnv()
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			rc := ee.ExitCode()
			if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				rc = 128 + int(ws.Signal())
			}
			fmt.Fprintf(env.Stderr, "buddy uninstall: uninstall.sh exited %d\n", rc)
			return rc
		}
		return refuse(fmt.Errorf("could not run sh %s: %v", shellQuote(script), err))
	}
	return 0
}

// flagWords is the flags as they follow the script on the running line.
func flagWords(f []string) string {
	s := ""
	for _, w := range f {
		s += " " + w
	}
	return s
}
