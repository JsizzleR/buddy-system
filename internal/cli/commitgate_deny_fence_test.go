package cli

// The commit gate's ledger-unavailable refusal (the follow-up #85 recorded
// open): it told the committer, a model as often as a human, "delete buddy.db
// from `git rev-parse --git-common-dir` to turn the Buddy System off", which
// deletes every claim, wait, outcome and inbox row in the checkout. Now it
// names the cause and the fix in D-084's register, as the PreToolUse gate's
// arm does since #85. Its error was already fenced; its 512-byte cap cut the
// fix off a newer ledger's error at a deep path. Same exit code throughout.

import (
	"strings"
	"testing"
)

func commitGateRefusal(t *testing.T, f *fixture) string {
	t.Helper()
	_, errw, code := f.run(t, f.repo, "", "commit-gate")
	if code != commitGateBroken {
		t.Fatalf("an unreadable ledger must still fail closed with exit %d, got %d: %s", commitGateBroken, code, errw)
	}
	return errw
}

func TestCommitGateLedgerUnavailableNamesTheFix(t *testing.T) {
	boundedParallel(t)

	// The words, on a clean path: the cause, the fix, the bypass, and no
	// deletion advice.
	errw := commitGateRefusal(t, newerLedgerFixture(t, "repo"))
	for _, want := range []string{"ledger unavailable (", "ledger schema is newer", "buddy status", "never delete buddy.db", "--no-verify"} {
		if !strings.Contains(errw, want) {
			t.Errorf("the refusal must say %q:\n%s", want, errw)
		}
	}
	for _, bad := range []string{"delete buddy.db from", "Buddy System off"} {
		if strings.Contains(errw, bad) {
			t.Errorf("the refusal must not advise deleting the ledger (%q):\n%s", bad, errw)
		}
	}

	// A deep repository: the newer ledger's error names a ledger path of about
	// 450 bytes BEFORE its fix, and a 512-byte cap cut the fix off.
	errw = commitGateRefusal(t, newerLedgerFixture(t, strings.Repeat("a", 170)+"/"+strings.Repeat("b", 170)))
	if !strings.Contains(errw, "rebuild and reinstall buddy") {
		t.Errorf("a long error lost its fix:\n%s", errw)
	}

	// The error stays one fenced line (it already was: a regression guard,
	// with the clean case above as its control). A newline in the repository's
	// name reaches the error through the ledger's path.
	errw = commitGateRefusal(t, newerLedgerFixture(t, "repo\n"+fakeLine))
	if !strings.Contains(errw, "⏎"+fakeLine) {
		t.Errorf("the error's text must still be shown, fenced:\n%s", errw)
	}
	for _, line := range strings.Split(errw, "\n") {
		if strings.HasPrefix(line, fakeLine) {
			t.Errorf("a newline in the error became a line of its own:\n%s", errw)
		}
	}
}
