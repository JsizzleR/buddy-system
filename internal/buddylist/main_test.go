package buddylist

import (
	"os"
	"testing"

	"github.com/JsizzleR/buddy-system/internal/testguard"
)

// TestMain arms the watchdog, then scrubs git's environment: the live leg's
// tests run git, and nothing of git's local environment may reach them, or a
// fixture's `git -C <tmp>` writes into whatever repository an inherited
// GIT_DIR names — measured, a pre-push from a linked worktree made the real
// repository bare (#49, testguard/gitenv.go). Untagged, so it covers the
// hermetic and live builds.
func TestMain(m *testing.M) {
	// First: the live leg starts the chat server and git as children, and a
	// binary orphaned by a dead `go test`, or wedged, must end (#130, D-220).
	// In the watchdog process Arm never returns.
	testguard.Arm()
	testguard.ScrubGitEnv()
	os.Exit(m.Run())
}
