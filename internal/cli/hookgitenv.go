package cli

import "os"

// The hook verbs discover repositories from git's view of the FILESYSTEM, not
// from whatever git environment the session inherited (#76, D-137).
//
// WHAT HAPPENED. A whole-project review (2026-11-09, main 9c509ec) ran the gate
// with GIT_DIR in its environment: sess-1 held dir1, sess-2's Write to
// <repo>/dir1/x.go from cwd <repo>/dir1 was ALLOWED, 0 bytes, where the control
// denied with 480. A relative GIT_DIR makes git say "not a git repository" from
// a subdirectory, which discoverRepo reads as errNoLedger before
// ledgerProvablyAbsent's own GIT_DIR guard can run; an absolute one makes git
// report the cwd as the worktree root, so dir1/x.go placed as x.go and the
// claim missed. A dotfiles shell (GIT_DIR and GIT_WORK_TREE aimed at another
// repository) read every repository on the machine as ledgerless; a
// GIT_COMMON_DIR elsewhere, and a ceiling at the repository root, the same.
// Every gated write from such a session passed every peer's claim, and a
// paused session's Bash passed its brake: invariant 3's two verdicts collapsed
// into one silent allow.
//
// WHY AT THE HOOK VERBS' ENTRY, NOT IN revParse. revParse is shared with the
// commit gate, and in a pre-commit hook git's environment is how git STATES
// what is being committed: a partial commit's temporary GIT_INDEX_FILE must
// survive (the charter's git facts). A hook is a fresh process per call, so
// unsetting the names in the process covers every discovery site at once
// (revParse, ledgerProvablyAbsent's and vanishedCwdAncestor's own os.Getenv
// reads, the per-directory discovery a shell route or a patch target makes)
// and every child the hook spawns. Threading an environment through
// openRepo/resolveRepo/discoverRepo would have missed the os.Getenv reads.
// Denying whenever one is set was cut: it bricks every dotfiles shell in every
// governed repository, and a dotfiles GIT_DIR names a DIFFERENT repository, so
// the filesystem's answer is the one to read anyway.
//
// THE LIST is git's `rev-parse --local-env-vars` (git's own definition of
// "which repository") plus GIT_CEILING_DIRECTORIES, which is not local but
// stops discovery below a root and so reads a governed repository as none.
// Fixed, not asked of git at run time: one more git spawn per hook against a
// 100 ms budget. TestHookGitEnvCoversGitsLocalList fails when this machine's
// git names a variable the copy lacks.
//
// NOT SCRUBBED, each on purpose: GIT_CONFIG_GLOBAL, GIT_CONFIG_SYSTEM and
// GIT_CONFIG_NOSYSTEM (safe.directory lives in global config; stripping it
// turns a trusted repository into a refused one, which D-044's path then
// denies wherever a ledger exists: the brick D-044 was written against, and
// why this is not cleanGitEnv); GIT_DISCOVERY_ACROSS_FILESYSTEM (it only
// WIDENS discovery, and scrubbing it would narrow jurisdiction); GIT_EXEC_PATH
// and the like (the ledger trusts the machine user, invariant 6). Clearing
// GIT_CONFIG_COUNT and GIT_CONFIG_PARAMETERS also drops a command-scope
// safe.directory exception (Codex design pass); a hook that then meets a
// refused repository takes D-044's ledger-file test, which denies only where a
// ledger exists.
var hookGitEnv = []string{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS",
	"GIT_CONFIG_COUNT", "GIT_OBJECT_DIRECTORY", "GIT_DIR", "GIT_WORK_TREE",
	"GIT_IMPLICIT_WORK_TREE", "GIT_GRAFT_FILE", "GIT_INDEX_FILE",
	"GIT_NO_REPLACE_OBJECTS", "GIT_REPLACE_REF_BASE", "GIT_PREFIX",
	"GIT_SHALLOW_FILE", "GIT_COMMON_DIR",
	"GIT_CEILING_DIRECTORIES",
}

// hookVerbs are the verbs a harness runs as hooks: Claude Code's six and the
// fenced Codex `lifecycle` (all of its subcommands, a controller's manual call
// included). Not commit-gate, which keeps git's environment, and not the verbs
// a session or the operator types (claim, status, wait check, ...): those
// refuse nothing a peer relies on, and an operator who sets GIT_DIR for one
// meant it (recorded open in D-137).
var hookVerbs = map[string]bool{
	"hello": true, "bye": true, "beat": true, "idle": true, "busy": true,
	"gate": true, "lifecycle": true,
}

// scrubHookGitEnv unsets every hookGitEnv name that is set, and returns the
// function that puts them back. In production the hook process ends right
// after, so the restore changes nothing; Run calls it anyway so an in-process
// caller (the test suite drives Run) is left as it was, and a later
// commit-gate in the same process still sees the environment it was given
// (Codex design pass, Q1).
func scrubHookGitEnv() (restore func()) {
	var saved [][2]string
	for _, k := range hookGitEnv {
		if v, ok := os.LookupEnv(k); ok {
			saved = append(saved, [2]string{k, v})
			os.Unsetenv(k)
		}
	}
	return func() {
		for _, kv := range saved {
			os.Setenv(kv[0], kv[1])
		}
	}
}
