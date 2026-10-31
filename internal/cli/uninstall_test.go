package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buddy uninstall (D-125): the verb only finds the stamped checkout and runs
// its scripts/uninstall.sh, so every test points it at a FIXTURE checkout
// (update's, with a recording stub uninstall.sh beside the stub install.sh):
// nothing here may run the real script or touch this machine. "It ran", "it
// ran there, with these arguments" is one record; "it never ran" is that
// record's absence, each beside a control in which it did run.

func newUninstallFix(t *testing.T) updFix {
	t.Helper()
	f := newUpdFix(t)
	stub := "#!/bin/sh\ncat > '" + f.rec + ".stdin'\nprintf '%s\\n' \"${GIT_DIR-unset}\" > '" + f.rec + ".git'\n" +
		"printf '%s\\n' \"$*\" > '" + f.rec + ".args'\npwd -P > '" + f.rec + "'\nexit $(cat '" + f.rcFile + "')\n"
	if err := os.WriteFile(filepath.Join(f.c, "scripts", "uninstall.sh"), []byte(stub), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

func runUninst(t *testing.T, src uninstallSource, args ...string) (int, string, string) {
	t.Helper()
	if src.sh == "" {
		src.sh = "/bin/sh"
	}
	var out, errb bytes.Buffer
	rc := runUninstall(args, Env{Stdin: strings.NewReader(`{"hook_event_name":"Stop"}`), Stdout: &out, Stderr: &errb}, src)
	return rc, out.String(), errb.String()
}

func TestUninstallRunsTheStampedCheckoutsScript(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{"--chat"}, "--chat"},
		{[]string{"--dry-run"}, "--dry-run"},
		{[]string{"--dry-run", "--chat"}, "--chat --dry-run"},
	} {
		f := newUninstallFix(t)
		rc, out, errs := runUninst(t, uninstallSource{checkout: f.c}, tc.args...)
		if rc != 0 || !f.ran(t) {
			t.Fatalf("%v: rc=%d ran=%v out=%q stderr=%q", tc.args, rc, f.ran(t), out, errs)
		}
		got, err := os.ReadFile(f.rec + ".args")
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(got)) != tc.want {
			t.Fatalf("%v: the script got %q, want %q", tc.args, strings.TrimSpace(string(got)), tc.want)
		}
		if !strings.HasPrefix(out, "running: sh "+shellQuote(filepath.Join(f.c, "scripts", "uninstall.sh"))) {
			t.Fatalf("%v: out=%q, want the running line first", tc.args, out)
		}
	}
}

// The script gets no stdin (a hook payload has no business reaching it) and no
// inherited GIT_DIR (its reports read its own checkout). Not parallel:
// t.Setenv. The control is the stub's own record, written as it ran.
func TestUninstallGivesTheScriptNoStdinAndNoGitEnv(t *testing.T) {
	f := newUninstallFix(t) // before GIT_DIR: the fixture's own git reads it
	t.Setenv("GIT_DIR", "/elsewhere/.git")
	if rc, out, errs := runUninst(t, uninstallSource{checkout: f.c}); rc != 0 || !f.ran(t) {
		t.Fatalf("rc=%d out=%q stderr=%q", rc, out, errs)
	}
	for suffix, want := range map[string]string{".stdin": "", ".git": "unset"} {
		b, err := os.ReadFile(f.rec + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(string(b)); got != want {
			t.Fatalf("the script's %s record is %q, want %q", suffix, got, want)
		}
	}
}

func TestUninstallPassesTheScriptsStatusThrough(t *testing.T) {
	t.Parallel()
	f := newUninstallFix(t)
	if err := os.WriteFile(f.rcFile, []byte("3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, _, errs := runUninst(t, uninstallSource{checkout: f.c})
	if rc != 3 || !f.ran(t) || !strings.Contains(errs, "uninstall.sh exited 3") {
		t.Fatalf("rc=%d ran=%v stderr=%q, want the script's 3", rc, f.ran(t), errs)
	}
}

// Each refusal runs nothing (the stub's record stays absent), and the control
// beside them is TestUninstallRunsTheStampedCheckoutsScript: the same fixture,
// script in place, runs.
func TestUninstallRefusesACheckoutItCannotRun(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		setup func(t *testing.T, f updFix) string
		want  string
	}{
		{"no stamp", func(t *testing.T, f updFix) string { return "" }, "carries no record"},
		{"checkout gone", func(t *testing.T, f updFix) string { return filepath.Join(f.c, "gone") }, "is gone"},
		{"no uninstall.sh (an older checkout)", func(t *testing.T, f updFix) string {
			if err := os.Remove(filepath.Join(f.c, "scripts", "uninstall.sh")); err != nil {
				t.Fatal(err)
			}
			return f.c
		}, "has no scripts/uninstall.sh as a regular file"},
		{"uninstall.sh a symlink", func(t *testing.T, f updFix) string {
			p := filepath.Join(f.c, "scripts", "uninstall.sh")
			if err := os.Rename(p, p+".real"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(p+".real", p); err != nil {
				t.Fatal(err)
			}
			return f.c
		}, "has no scripts/uninstall.sh as a regular file"},
	}
	for _, tc := range cases {
		f := newUninstallFix(t)
		stamp := tc.setup(t, f)
		rc, out, errs := runUninst(t, uninstallSource{checkout: stamp})
		if rc != 1 || f.ran(t) || out != "" || !strings.Contains(errs, tc.want) {
			t.Fatalf("%s: rc=%d ran=%v out=%q stderr=%q, want a refusal naming %q", tc.name, rc, f.ran(t), out, errs, tc.want)
		}
		// A dead end is never the answer: every refusal names the way round.
		if !strings.Contains(errs, "sh <clone>/scripts/uninstall.sh") {
			t.Fatalf("%s: stderr=%q does not name the clone's script", tc.name, errs)
		}
	}
}

func TestUninstallAnswersHelpAndRefusesWhatItDoesNotKnow(t *testing.T) {
	t.Parallel()
	run := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		rc := Run(append([]string{"uninstall"}, args...), Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb})
		return rc, out.String(), errb.String()
	}
	for _, h := range [][]string{{"--help"}, {"--chat", "--help"}, {"-h"}} {
		if rc, out, _ := run(h...); rc != 0 || !strings.HasPrefix(out, "usage: buddy uninstall") {
			t.Fatalf("%v: rc=%d out=%q", h, rc, out)
		}
	}
	for _, bad := range [][]string{{"--purge"}, {"now"}, {"--chat", "x"}, {"--dry-run=maybe"}} {
		if rc, _, errs := run(bad...); rc != 1 || !strings.Contains(errs, "refused:") {
			t.Fatalf("%v: rc=%d stderr=%q, want a usage refusal", bad, rc, errs)
		}
	}
	// This test binary was not built by install.sh: no stamp, so the real
	// verb refuses before it runs anything.
	if rc, out, errs := run(); rc != 1 || out != "" || !strings.Contains(errs, "carries no record") {
		t.Fatalf("unstamped: rc=%d out=%q stderr=%q", rc, out, errs)
	}
}
