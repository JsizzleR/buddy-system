package cli

// A verb that cannot open its ledger prints the open error through errVerb,
// `buddy <verb>: <err>`, and the error names the ledger's path. It was
// printed raw, so a newline in the path was a line of its own, of the
// directory's choosing (Codex, the commit-gate follow-up of #85: the
// `buddy status` both gates' refusals now point to). The verb still fails,
// with the same exit code; only the rendering changed.

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/JsizzleR/buddy-system/internal/store"
)

func TestVerbLedgerOpenErrorIsFenced(t *testing.T) {
	boundedParallel(t)
	calls := []struct {
		name  string
		stdin func(f *fixture) string
		args  []string
	}{
		{"status", func(*fixture) string { return "" }, []string{"status"}},
		{"init", func(*fixture) string { return "" }, []string{"init"}},
		{"hello hook", func(f *fixture) string { return hookJSON("sess-c", f.repo, "", "") }, []string{"hello"}},
		{"beat hook", func(f *fixture) string { return hookJSON("sess-a", f.repo, "Edit", "x.go") }, []string{"beat"}},
		{"idle hook", func(f *fixture) string { return hookJSON("sess-a", f.repo, "", "") }, []string{"idle"}},
		{"busy hook", func(f *fixture) string { return hookJSON("sess-a", f.repo, "", "") }, []string{"busy"}},
		{"bye hook", func(f *fixture) string { return hookJSON("sess-a", f.repo, "", "") }, []string{"bye"}},
		{"lifecycle hook", func(f *fixture) string {
			return fmt.Sprintf(`{"session_id":"w","cwd":%q,"hook_event_name":"Stop"}`, f.repo)
		}, []string{"lifecycle", "hook", "--incarnation", "x"}},
	}
	clean := newerLedgerFixture(t, "repo")
	hostile := newerLedgerFixture(t, "repo\n"+fakeLine)
	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			// Positive control: the clean path reaches the open error and
			// fails on it, one line.
			_, errw, code := clean.run(t, clean.repo, c.stdin(clean), c.args...)
			if code == 0 || !strings.Contains(errw, "ledger schema is newer") || strings.Count(strings.TrimSuffix(errw, "\n"), "\n") != 0 {
				t.Fatalf("control: want a one-line open error, got code %d:\n%s", code, errw)
			}
			_, errw2, code2 := hostile.run(t, hostile.repo, c.stdin(hostile), c.args...)
			if code2 != code {
				t.Errorf("the exit code changed with the path: %d, want %d", code2, code)
			}
			if !strings.Contains(errw2, "ledger schema is newer") || !strings.Contains(errw2, "⏎"+fakeLine) {
				t.Errorf("want the open error, its path fenced:\n%s", errw2)
			}
			if strings.Count(strings.TrimSuffix(errw2, "\n"), "\n") != 0 {
				t.Errorf("a newline in the ledger's path became a line of its own:\n%s", errw2)
			}
		})
	}
}

// The wrapper fences the words only: what inspects the error still sees the
// chain (errors.Is on a sentinel, explainLedgerAccess's errors.As on SQLite's
// code), and a nil stays nil.
func TestFencedOpenErrKeepsTheChain(t *testing.T) {
	inner := fmt.Errorf("%w: /a\nb/buddy.db", store.ErrLedgerNewer)
	got := fencedOpenErr(inner)
	if !errors.Is(got, store.ErrLedgerNewer) {
		t.Errorf("the sentinel is lost through the wrapper: %v", got)
	}
	var coded interface{ Code() int }
	if !errors.As(fencedOpenErr(codedErr{code: 14, msg: "unable to open database file (14)"}), &coded) || coded.Code() != 14 {
		t.Errorf("the SQLite code is lost through the wrapper")
	}
	if strings.Contains(got.Error(), "\n") {
		t.Errorf("control: the wrapper must fence: %q", got.Error())
	}
	if fencedOpenErr(nil) != nil {
		t.Errorf("a nil error must stay nil")
	}
}
