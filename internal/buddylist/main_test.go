package buddylist

import (
	"os"
	"testing"

	"github.com/JsizzleR/buddy-system/internal/testguard"
)

// TestMain exists for one line: the live leg's tests run git, and nothing of
// git's local environment may reach them, or a fixture's `git -C <tmp>`
// writes into whatever repository an inherited GIT_DIR names — measured, a
// pre-push from a linked worktree made the real repository bare (#49,
// testguard/gitenv.go). Untagged, so it covers the hermetic and live builds.
func TestMain(m *testing.M) {
	testguard.ScrubGitEnv()
	os.Exit(m.Run())
}
