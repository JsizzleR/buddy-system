package cli

import (
	"strings"
	"testing"
)

// #143: `buddy who` and `buddy ls` had no narrow form, so 2,423 of 2,588 `who`
// calls and 869 of 916 `ls` calls on one machine were piped through a filter
// (`ls | grep <slug>`, `who <x> | grep EXIT`, `| head -1`), and a pipe returns
// the FILTER's status: a verb's refusal arrived as exit 0. `ls <slug>...` lists
// those claims alone and exits 1 naming each slug with no row; `who <target>
// --exit` prints the EXIT line alone. Both narrow what is printed and nothing
// else: a view grants and refuses nothing.

func (f *fixture) claimIn(t *testing.T, dir, session, slug, scope string) {
	t.Helper()
	if _, errw, code := f.run(t, dir, "", "claim", slug, "--session", session, "--desc", "work on "+slug, "--scope", scope); code != 0 {
		t.Fatalf("claim %s: %s", slug, errw)
	}
}

func TestLsNamesOnlyTheSlugsAsked(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimIn(t, f.repo, "sess-a", "alpha-work", "internal/a")
	f.claimIn(t, f.wtB, "sess-b", "bravo-work", "internal/b")
	f.claimIn(t, f.wtB, "sess-b", "old-work", "internal/old")
	if _, errw, code := f.run(t, f.wtB, "", "release", "old-work", "--session", "sess-b"); code != 0 {
		t.Fatal(errw)
	}

	// The positive control: unnarrowed, both open claims list.
	out, _, code := f.run(t, f.repo, "", "ls")
	if code != 0 || len(lines(out)) != 2 {
		t.Fatalf("ls must list both open claims: %d\n%s", code, out)
	}

	out, errw, code := f.run(t, f.repo, "", "ls", "alpha-work")
	if code != 0 || len(lines(out)) != 1 || !strings.HasPrefix(out, "alpha-work ") || errw != "" {
		t.Fatalf("ls alpha-work must print its row alone and exit 0: %d %q %q", code, out, errw)
	}
	out, _, code = f.run(t, f.repo, "", "ls", "bravo-work", "alpha-work")
	if code != 0 || len(lines(out)) != 2 {
		t.Fatalf("ls with two slugs must print both rows: %d\n%s", code, out)
	}

	// A miss is exit 1, named on stderr, the rows that matched still printed.
	out, errw, code = f.run(t, f.repo, "", "ls", "alpha-work", "nope")
	if code != 1 || len(lines(out)) != 1 || !strings.Contains(errw, `"nope"`) || strings.Contains(errw, "alpha-work") {
		t.Fatalf("ls alpha-work nope must print alpha's row and exit 1 naming nope alone: %d %q %q", code, out, errw)
	}
	if _, errw, code = f.run(t, f.repo, "", "ls", "nope"); code != 1 || !strings.Contains(errw, `"nope"`) {
		t.Fatalf("ls of a slug with no row must exit 1: %d %q", code, errw)
	}
	// A released claim is a row only under --all, as for the unnarrowed list.
	if _, _, code = f.run(t, f.repo, "", "ls", "old-work"); code != 1 {
		t.Fatalf("a released claim is not open: ls old-work must exit 1, got %d", code)
	}
	out, _, code = f.run(t, f.repo, "", "ls", "--all", "old-work")
	if code != 0 || len(lines(out)) != 1 || !strings.Contains(out, "released") {
		t.Fatalf("ls --all old-work must print the released row: %d\n%s", code, out)
	}
	// Exact, like the open-slug index: another spelling is another slug.
	if _, _, code = f.run(t, f.repo, "", "ls", "ALPHA-WORK"); code != 1 {
		t.Fatalf("ls matches a slug exactly; ALPHA-WORK must miss, got %d", code)
	}

	// The echoed slug is one line (invariant 9).
	_, errw, _ = f.run(t, f.repo, "", "ls", "a\nFAKE ROW")
	if strings.Count(strings.TrimRight(errw, "\n"), "\n") != 0 {
		t.Fatalf("a slug's newline reached stderr as a line break: %q", errw)
	}

	// Usage: --help answers, an unknown flag and a flag after a slug refuse.
	if out, _, code := f.run(t, f.repo, "", "ls", "--help"); code != 0 || !strings.Contains(out, "[<slug>...]") {
		t.Fatalf("ls --help must print the usage with the slug form: %d %q", code, out)
	}
	for _, args := range [][]string{{"ls", "--bogus"}, {"ls", "alpha-work", "--all"}, {"ls", "alpha-work", "--bogus"}} {
		if out, errw, code := f.run(t, f.repo, "", args...); code == 0 || out != "" || !strings.Contains(errw, "refused:") {
			t.Fatalf("%v must refuse and print no rows: %d %q %q", args, code, out, errw)
		}
	}
}

func TestWhoExitPrintsTheExitLineAlone(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimIn(t, f.repo, "sess-a", "alpha-work", "internal/a")
	// A declared wait, so the WAITING register and EXIT's wait clause both
	// print in the full report (Codex code pass: without one, a WAITING line
	// leaking under --exit went unseen).
	f.claimIn(t, f.wtB, "sess-b", "bravo-work", "internal/b")
	if _, errw, code := f.run(t, f.repo, "", "wait", "--on", "bravo-work", "--until", "1h", "--session", "sess-a"); code != 0 {
		t.Fatalf("wait: %s", errw)
	}

	// The positive control: the full report carries the EXIT line among others.
	full, _, code := f.run(t, f.repo, "", "who", "alpha")
	if code != 0 || !strings.Contains(full, "CLAIMS HELD") || !strings.Contains(full, "\nWAITING      on") ||
		!strings.Contains(full, "a wait declared") || !strings.Contains(full, "\nEXIT ") {
		t.Fatalf("who alpha must print the whole report: %d\n%s", code, full)
	}
	var want string
	for _, ln := range lines(full) {
		if strings.HasPrefix(ln, "EXIT ") {
			want = ln + "\n"
		}
	}
	for _, args := range [][]string{{"who", "alpha", "--exit"}, {"who", "--exit", "alpha"}, {"who", "alpha-work", "--exit"}} {
		out, errw, code := f.run(t, f.repo, "", args...)
		if code != 0 || out != want {
			t.Fatalf("%v must print the full report's EXIT line and nothing else:\n got %q\nwant %q\n%s", args, out, want, errw)
		}
	}
	// No target: the caller, as bare who and status do. An EMPTY target is
	// not an absent one (`who "$X"`, X unset): it names nothing and refuses,
	// never the caller, and never lets the next word stand in (Codex code pass).
	f.asSession("sess-a", func() {
		if out, errw, code := f.run(t, f.repo, "", "who", "--exit"); code != 0 || out != want {
			t.Fatalf("who --exit must report the caller's EXIT line: %d %q %q", code, out, errw)
		}
		for _, args := range [][]string{{"who", "", "--exit"}, {"who", ""}, {"who", "", "bravo"}} {
			if out, errw, code := f.run(t, f.repo, "", args...); code == 0 || out != "" {
				t.Fatalf("%q must refuse an empty target and print nothing: %d %q %q", args, code, out, errw)
			}
		}
	})
	// An ended session's EXIT line.
	if _, errw, code := f.run(t, f.wtB, "", "bye", "sess-b"); code != 0 {
		t.Fatal(errw)
	}
	if out, _, code := f.run(t, f.repo, "", "who", "bravo", "--exit"); code != 0 || out != "EXIT         already ended\n" {
		t.Fatalf("who bravo --exit after bye: %d %q", code, out)
	}

	// Usage: --help names the flag, an unknown flag and a second target refuse.
	if out, _, code := f.run(t, f.repo, "", "who", "--help"); code != 0 || !strings.Contains(out, "--exit") {
		t.Fatalf("who --help must name --exit: %d %q", code, out)
	}
	for _, args := range [][]string{{"who", "alpha", "--bogus"}, {"who", "alpha", "bravo"}, {"who", "alpha", "--exit", "bravo"}} {
		if out, errw, code := f.run(t, f.repo, "", args...); code == 0 || out != "" {
			t.Fatalf("%v must refuse and print nothing: %d %q %q", args, code, out, errw)
		}
	}
}
