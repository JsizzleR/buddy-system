package testguard

// ScrubGitEnv: a test binary that runs git must not inherit git's LOCAL
// environment (issue #49). TestMain calls it before m.Run, in every package
// whose tests exec git; check.sh fails a package that execs git in a test
// without it.
//
// WHAT HAPPENED (2026-09-27). A push from a LINKED worktree ran the pre-push
// hook, which runs the hermetic tier. Git runs a hook with GIT_DIR set, and in
// a linked worktree it is ABSOLUTE (<repo>/.git/worktrees/<name>). The
// fixtures' `git -C <tmp> …` inherited it, and -C does not override an
// absolute GIT_DIR, so every fixture ran against the REAL repository: the
// refused-repository fixture wrote core.bare=true and
// core.repositoryformatversion=99 into the real .git/config (every git
// command there then died, and the buddy gate failed closed for every
// session), and others created worktrees and branches in it. From the main
// checkout it never bit, only because there the hook's GIT_DIR is the
// relative ".git", which resolves inside each fixture's own temp dir.
//
// WHY HERE AS WELL AS IN THE HOOK AND check.sh. Those two cover the path it
// took. `go test` can be started from inside ANY hook — a machine's own
// hooks.local, a rebase --exec, an editor's — and the fixtures are what
// write, so the binary cleans its own environment. A test that needs GIT_DIR
// sets it with t.Setenv after this has run.
//
// THE LIST is git's own (`git rev-parse --local-env-vars`, which needs no
// repository) united with a fixed copy of it, so a git that cannot run — or
// dies on the very repository it was pointed at — still leaves nothing set.

import (
	"os"
	"os/exec"
	"strings"
)

// gitLocalEnv is `git rev-parse --local-env-vars` as of git 2.54: the
// fallback, never the only source.
var gitLocalEnv = []string{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS",
	"GIT_CONFIG_COUNT", "GIT_OBJECT_DIRECTORY", "GIT_DIR", "GIT_WORK_TREE",
	"GIT_IMPLICIT_WORK_TREE", "GIT_GRAFT_FILE", "GIT_INDEX_FILE",
	"GIT_NO_REPLACE_OBJECTS", "GIT_REPLACE_REF_BASE", "GIT_PREFIX",
	"GIT_SHALLOW_FILE", "GIT_COMMON_DIR",
}

// ScrubGitEnv unsets git's local environment variables in this process, and
// returns the names that were set (for a caller that wants to say so).
func ScrubGitEnv() (removed []string) {
	names := append([]string(nil), gitLocalEnv...)
	if out, err := exec.Command("git", "rev-parse", "--local-env-vars").Output(); err == nil {
		names = append(names, strings.Fields(string(out))...)
	}
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		if _, set := os.LookupEnv(n); set {
			os.Unsetenv(n)
			removed = append(removed, n)
		}
	}
	return removed
}
