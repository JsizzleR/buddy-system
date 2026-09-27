package testguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every name git lists, and the fallback's, is gone after a scrub; a name
// outside the list survives (the control that it is not clearing GIT_*
// wholesale, which would take GIT_CONFIG_GLOBAL and the like with it).
func TestScrubGitEnvUnsetsGitsLocalEnvironment(t *testing.T) {
	for _, n := range gitLocalEnv {
		t.Setenv(n, "/nowhere/"+n)
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1") // not a local var: must survive
	removed := ScrubGitEnv()
	for _, n := range gitLocalEnv {
		if v, set := os.LookupEnv(n); set {
			t.Errorf("%s still set to %q after the scrub", n, v)
		}
	}
	if len(removed) < len(gitLocalEnv) {
		t.Errorf("reported %d removed, planted %d: %v", len(removed), len(gitLocalEnv), removed)
	}
	if os.Getenv("GIT_CONFIG_NOSYSTEM") != "1" {
		t.Error("GIT_CONFIG_NOSYSTEM is not one of git's local vars and must survive")
	}
}

// With no git to ask, the fixed list still clears GIT_DIR: the arm that
// matters when git cannot run at all.
func TestScrubGitEnvWithoutGitStillClearsGitDir(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // an empty directory: no git on PATH
	t.Setenv("GIT_DIR", "/nowhere/.git/worktrees/x")
	ScrubGitEnv()
	if v, set := os.LookupEnv("GIT_DIR"); set {
		t.Fatalf("GIT_DIR still set to %q with git absent", v)
	}
}

// The fallback is git's list: a name git reports that the copy lacks means the
// copy has drifted, and a machine whose git cannot run would keep it set.
func TestScrubGitEnvFallbackCoversWhatGitReports(t *testing.T) {
	out, err := exec.Command("git", "rev-parse", "--local-env-vars").Output()
	if err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	have := map[string]bool{}
	for _, n := range gitLocalEnv {
		have[n] = true
	}
	for _, n := range strings.Fields(string(out)) {
		if !have[n] {
			t.Errorf("git lists %s as a local env var and the fallback copy does not", n)
		}
	}
}

// The failure it exists for, end to end: an absolute GIT_DIR pointing at a
// "real" repository, a fixture-style `git -C <tmp> init` and `config` after
// the scrub, and the real repository's config untouched. Without the scrub
// (the control, run first) the same commands write into the real one —
// proving the leg is armed on this machine's git.
func TestScrubGitEnvKeepsAFixtureOutOfTheRealRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	real := filepath.Join(t.TempDir(), "real")
	if out, err := exec.Command("git", "init", "-q", real).CombinedOutput(); err != nil {
		t.Fatalf("git init real: %v\n%s", err, out)
	}
	realGitDir := filepath.Join(real, ".git")
	fixture := func(t *testing.T) {
		tmp := t.TempDir()
		for _, args := range [][]string{{"init", "-q"}, {"config", "core.bare", "true"}} {
			cmd := exec.Command("git", append([]string{"-C", tmp}, args...)...)
			cmd.Env = os.Environ()
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
	}
	bare := func() string {
		out, _ := exec.Command("git", "--git-dir", realGitDir, "config", "core.bare").Output()
		return strings.TrimSpace(string(out))
	}

	t.Run("control: an inherited GIT_DIR sends the fixture to the real repository", func(t *testing.T) {
		t.Setenv("GIT_DIR", realGitDir)
		fixture(t)
		if got := bare(); got != "true" {
			t.Fatalf("the control did not reach the real repository (core.bare=%q): the leg is not armed", got)
		}
	})
	if out, err := exec.Command("git", "--git-dir", realGitDir, "config", "core.bare", "false").CombinedOutput(); err != nil {
		t.Fatalf("reset: %v\n%s", err, out)
	}
	t.Run("scrubbed: the fixture stays in its own temp dir", func(t *testing.T) {
		t.Setenv("GIT_DIR", realGitDir)
		ScrubGitEnv()
		fixture(t)
		if got := bare(); got != "false" {
			t.Fatalf("the real repository's core.bare is %q after a scrubbed fixture", got)
		}
	})
}
