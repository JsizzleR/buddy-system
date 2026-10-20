package cli

import (
	"strings"
	"testing"
)

// D-098: a refusal that shows the usage puts its reason on the LAST line, so
// `buddy release ... 2>&1 | tail -1` keeps the why (31 such release calls and
// 20 claim calls on one fleet read a usage line and lost it). Each row is the
// output as `2>&1` would show it: stdout, then stderr.
func TestRefusalReasonIsTheLastLine(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	run := func(args ...string) (out, errw string, code int) {
		f.asSession("sess-a", func() { out, errw, code = f.run(t, f.repo, "", args...) })
		return
	}
	if _, errw, code := run("claim", "job", "--desc", "d", "--scope", "src"); code != 0 {
		t.Fatalf("control: the claim the release rows name must exist: %s", errw)
	}
	for _, tc := range []struct {
		name  string
		args  []string
		last  string // the last line must start with this
		usage string // and the usage must still be printed above it
	}{
		{"release --note without --outcome", []string{"release", "job", "--note", "green"},
			"  refused: --note rides an --outcome", usageRelease},
		{"release --outcome not an outcome", []string{"release", "job", "--outcome", "maybe"},
			`  refused: --outcome "maybe" is not one of`, usageRelease},
		{"release --to empty", []string{"release", "job", "--to", ""},
			"  refused: --to needs a session", usageRelease},
		{"release --to with --outcome", []string{"release", "job", "--to", "bravo", "--outcome", "pass"},
			"  refused: --to hands the whole claim on", usageRelease},
		{"release --outcome with --scope", []string{"release", "job", "--outcome", "pass", "--scope", "src"},
			"  refused: --outcome reports on the job the whole claim guarded", usageRelease},
		{"release unknown flag", []string{"release", "job", "--outcomes", "pass"},
			"  refused: flag provided but not defined: -outcomes", usageRelease},
		{"claim unknown flag", []string{"claim", "x", "--desc", "d", "--scope", "a", "--bogus"},
			"  refused: flag provided but not defined: -bogus", usageClaim},
		// zsh does not split an unquoted $S: the scopes arrive as ONE word.
		{"claim stray positional", []string{"claim", "x", "--desc", "d", "--scope a --scope b"},
			`  refused: flag provided but not defined: -scope a --scope b`, usageClaim},
		{"claim a stray word after the flags", []string{"claim", "x", "--desc", "d", "--scope", "a", "b"},
			`  refused: claim takes no further arguments, got "b"`, usageClaim},
		{"init with an argument", []string{"init", "now"},
			`  refused: init takes no arguments, got "now"`, usageInit},
		{"sweep unknown flag", []string{"sweep", "--nope"},
			"  refused: flag provided but not defined: -nope", usageSweep},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, errw, code := run(tc.args...)
			if code == 0 {
				t.Fatalf("control: this must be refused, and it ran:\n%s%s", out, errw)
			}
			both := strings.TrimRight(out+errw, "\n")
			lines := strings.Split(both, "\n")
			if last := lines[len(lines)-1]; !strings.HasPrefix(last, tc.last) {
				t.Fatalf("`| tail -1` keeps %q; want it to start %q. Whole output:\n%s", last, tc.last, both)
			}
			// The usage is still shown, whole, above the reason.
			if !strings.Contains(both, tc.usage) {
				t.Fatalf("the usage must still be printed:\n%s", both)
			}
			if strings.Count(both, "refused:") != 1 {
				t.Fatalf("one reason line, not more:\n%s", both)
			}
		})
	}
	// Nothing was released by any of them: the claim is still open, and a
	// plain release of it is not refused (the positive control for "refused"
	// meaning the guard and not a broken claim).
	out, errw, code := run("release", "job")
	if code != 0 || strings.Contains(out+errw, "refused:") {
		t.Fatalf("control: a well-formed release goes through: %d\n%s%s", code, out, errw)
	}
}
