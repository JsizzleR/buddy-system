package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// #111: `authority add --help` stored the row "--help" and exited 0, and
// `add --bogus` stored "--bogus" (reproduced at a96d9f4 and again at 6fb58ac:
// each spent one of the eight watch slots on a flag). A help flag after the
// subcommand answers the usage line and writes nothing (D-031, D-210); any
// other dash-led argument is refused with `refused: <reason>` last (D-098),
// and the way to name a file that really starts with a dash is `./-name`.
func TestAuthoritySubcommandHelpAndFlagsWriteNothing(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	list := func() string {
		t.Helper()
		out, errw, code := f.run(t, f.repo, "", "authority")
		if code != 0 {
			t.Fatalf("authority: %s", errw)
		}
		return strings.TrimSpace(out)
	}
	const builtIns = "watching CLAUDE.md  (always)\nwatching AGENTS.md  (always)"
	for _, sub := range []string{"add", "rm"} {
		for _, h := range []string{"--help", "-h", "-help"} {
			out, errw, code := f.run(t, f.repo, "", "authority", sub, h)
			if code != 0 || strings.TrimSpace(out) != usageAuthority || errw != "" {
				t.Fatalf("authority %s %s answers the usage line on stdout, exit 0: %d %q %q", sub, h, code, out, errw)
			}
		}
		for _, bad := range []string{"--bogus", "-x", "--"} {
			out, errw, code := f.run(t, f.repo, "", "authority", sub, bad)
			last := errw[strings.LastIndex(strings.TrimRight(errw, "\n"), "\n")+1:]
			if code == 0 || out != "" || !strings.HasPrefix(errw, "buddy authority: "+usageAuthority+"\n") || !strings.Contains(last, "refused: ") {
				t.Fatalf("authority %s %s refuses, usage first and refused: last: %d %q %q", sub, bad, code, out, errw)
			}
		}
	}
	// An unknown subcommand ends `refused:` too (it printed a bare usage).
	if _, errw, code := f.run(t, f.repo, "", "authority", "bogus"); code == 0 || !strings.Contains(errw, "\n  refused: authority takes nothing") {
		t.Fatalf("an unknown subcommand refuses with its reason last: %d %q", code, errw)
	}
	// A stray positional after the path refuses the same way, and writes nothing.
	if _, errw, code := f.run(t, f.repo, "", "authority", "add", "docs/a.md", "docs/b.md"); code == 0 || !strings.Contains(errw, "\n  refused: authority add takes one path, got a second, \"docs/b.md\"") {
		t.Fatalf("a stray positional refuses: %d %q", code, errw)
	}
	if got := list(); got != builtIns {
		t.Fatalf("nothing was written by any of those:\n%s", got)
	}
	// Positive control: the guard is not refusing every add. A path that
	// really begins with a dash is named ./-name, and add/rm then work.
	if _, errw, code := f.run(t, f.repo, "", "authority", "add", "./--help"); code != 0 {
		t.Fatalf("./--help is a path: %s", errw)
	}
	if got := list(); got != builtIns+"\nwatching --help" {
		t.Fatalf("the dash-led path is stored by its ./ spelling:\n%s", got)
	}
	if out, errw, code := f.run(t, f.repo, "", "authority", "rm", "./--help"); code != 0 || !strings.Contains(out, "no longer watching --help") {
		t.Fatalf("rm ./--help frees the slot a pre-fix `add --help` took: %d %q %q", code, out, errw)
	}
}

// #117: git writes FETCH_HEAD's "of <url>" with trailing slashes and then a
// trailing ".git" stripped, `user@` dropped from a scheme URL, and insteadOf
// applied (measured, git 2.54: builtin/fetch.c and transport_anonymize_url),
// so a clone of a URL ending in .git never matched and every report fell
// back to the reflog or to "git did not record". Each case is a REAL fetch:
// the hand-written FETCH_HEADs of update_test.go spell the URL as configured,
// which is why they passed.
func TestFetchedWhenMatchesTheURLAsGitWritesIt(t *testing.T) {
	boundedParallel(t)
	d := t.TempDir()
	src := filepath.Join(d, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, src, "init", "-q", "-b", "main")
	gitIn(t, src, "commit", "-q", "--allow-empty", "-m", "x")
	bare := filepath.Join(d, "origin.git")
	gitIn(t, d, "clone", "-q", "--bare", src, bare)
	c := filepath.Join(d, "c")
	gitIn(t, d, "clone", "-q", bare, c)
	cases := []struct{ name, url, insteadOf string }{
		{"plain .git path", bare, ""},
		{"trailing slash", bare + "/", ""},
		{"two trailing slashes", bare + "//", ""},
		{"no .git", filepath.Join(d, "origin"), ""},        // git finds origin.git by itself
		{"relative, trailing slash", "../origin.git/", ""}, // recorded "../origin": no path normalizing
		{"file URL", "file://" + bare + "/", ""},
		{"file URL with user", "file://user@" + bare, ""},
		{"insteadOf", "zz:origin.git", d + "/"},
	}
	for _, tc := range cases {
		gitIn(t, c, "config", "remote.origin.url", tc.url)
		if tc.insteadOf != "" {
			gitIn(t, c, "config", "url."+tc.insteadOf+".insteadOf", "zz:")
		}
		gitIn(t, c, "fetch", "-q", "origin")
		fh, _ := os.ReadFile(filepath.Join(c, ".git", "FETCH_HEAD"))
		got := fetchedWhen(c, "main", time.Now())
		if !strings.HasPrefix(got, "as of the last fetch, ") {
			t.Errorf("%s: url %q, FETCH_HEAD %q: a fetch just now was not read: %q", tc.name, tc.url, fh, got)
		}
		// Positive control for the match: another branch's line is not main's.
		if got := fetchedWhen(c, "other", time.Now()); strings.HasPrefix(got, "as of the last fetch, ") {
			t.Errorf("%s: FETCH_HEAD records main only, yet dated other: %q", tc.name, got)
		}
	}
}

// Codex code pass on #117: git's spelling drops ".git", so a second remote
// at `/srv/p`, a different repository, writes the same `of /srv/p` an origin
// at `/srv/p.git` does. Fetched now, its line must not date origin/main's
// observation from hours ago: the line's object must be origin/main's.
func TestFetchedWhenIgnoresAnotherRemoteSpelledTheSame(t *testing.T) {
	boundedParallel(t)
	d := t.TempDir()
	mk := func(name, msg string) string {
		t.Helper()
		src := filepath.Join(d, name+"-src")
		if err := os.MkdirAll(src, 0o755); err != nil {
			t.Fatal(err)
		}
		gitIn(t, src, "init", "-q", "-b", "main")
		gitIn(t, src, "commit", "-q", "--allow-empty", "-m", msg)
		return src
	}
	gitIn(t, d, "clone", "-q", "--bare", mk("a", "origin's"), filepath.Join(d, "p.git"))
	gitIn(t, d, "clone", "-q", "--bare", mk("b", "another repository's"), filepath.Join(d, "p"))
	c := filepath.Join(d, "c")
	gitIn(t, d, "clone", "-q", filepath.Join(d, "p.git"), c)
	gitIn(t, c, "remote", "add", "other", filepath.Join(d, "p"))
	now := time.Now()
	// Positive control: origin's own fetch is read.
	gitIn(t, c, "fetch", "-q", "origin")
	if got := fetchedWhen(c, "main", now); !strings.HasPrefix(got, "as of the last fetch, ") {
		t.Fatalf("control: origin's fetch is read: %q", got)
	}
	gitIn(t, c, "fetch", "-q", "other")
	fh, _ := os.ReadFile(filepath.Join(c, ".git", "FETCH_HEAD"))
	if !strings.Contains(string(fh), "branch 'main' of "+filepath.Join(d, "p")+"\n") {
		t.Fatalf("the collision this test is about did not happen: %q", fh)
	}
	if got := fetchedWhen(c, "main", now); strings.HasPrefix(got, "as of the last fetch, ") {
		t.Fatalf("another repository's fetch dated origin/main: %q (FETCH_HEAD %q)", got, fh)
	}
}

// The anonymize-and-strip rule on its own, for the shapes a real fetch above
// cannot reach without a network (scp-style, https with a user). The scp and
// https rows are git's source, not a measurement here.
func TestFetchHeadURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://github.com/o/r.git", "https://github.com/o/r"},
		{"https://github.com/o/r.git/", "https://github.com/o/r"},
		{"https://github.com/o/r/", "https://github.com/o/r"},
		{"https://me:tok@github.com/o/r.git", "https://github.com/o/r"},
		{"https://github.com/a@b/r", "https://github.com/a@b/r"}, // @ past the first slash is a path
		{"git@github.com:o/r.git", "github.com:o/r"},
		{"/srv/repos/r.git", "/srv/repos/r"},
		{"/srv/a@b/r.git", "/srv/a@b/r"},     // local: no user to drop
		{"/srv/a@b:c/r.git", "/srv/a@b:c/r"}, // local, though a ':' follows the @
		{"x", "x"},
		{"a.git", "a.git"}, // git strips .git only past its fifth byte
		{"ab.git", "ab"},
		{"/tmp/a\nb.git", `/tmp/a\nb`}, // git escapes a newline after the strip
	} {
		if got := fetchHeadURL(tc.in); got != tc.want {
			t.Errorf("fetchHeadURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// #119: a `buddy fleet wait --block` declared `/loop buddy fleet wait check`
// and then, on its verdict, said to stop the loop that runs `buddy wait
// check`, which is not the loop it armed; CLEARED named `buddy wait clear`.
func TestFleetBlockNamesTheFleetVerbs(t *testing.T) {
	boundedParallel(t)
	for _, verdict := range []string{"landed", "cleared"} {
		t.Run(verdict, func(t *testing.T) {
			fe, _ := bothEnv(t)
			lane := fe.mkdirPlain(t, "lane")
			fe.hook(t, "hello", "lane-1", lane)
			if _, errw, code := fe.as(t, "lane-1", lane, "claim", "i01", "--desc", "d", "--scope", ".buddy/slot/i01"); code != 0 {
				t.Fatal(errw)
			}
			fe.hook(t, "hello", "sess-x", fe.repo)
			var out, errw bytes.Buffer
			pauses := 0
			fe.env[EnvClaudeSession] = "sess-x"
			code := Run([]string{"fleet", "wait", "--on", "i01", "--until", "1h", "--block"}, Env{
				Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errw, Cwd: fe.repo,
				Now:       func() time.Time { return fe.clock },
				Getenv:    func(k string) string { return fe.env[k] },
				Anchor:    func() (harnessProc, bool) { return harnessProc{Ref: fe.proc}, fe.proc.PID != 0 },
				ProcAlive: func(p store.ProcRef) bool { _, ok := fe.alive[p.PID]; return ok },
				Sleep: func(d time.Duration) {
					pauses++
					if pauses > 10 {
						t.Fatalf("still blocking: %s", out.String())
					}
					fe.clock = fe.clock.Add(d)
					if pauses != 2 {
						return
					}
					if verdict == "landed" {
						if _, e, c := fe.as(t, "lane-1", lane, "release", "i01", "--outcome", "pass"); c != 0 {
							t.Fatal(e)
						}
					} else if _, e, c := fe.as(t, "sess-x", fe.repo, "fleet", "wait", "clear"); c != 0 {
						t.Fatal(e)
					}
					fe.env[EnvClaudeSession] = "sess-x"
				},
			})
			s := out.String()
			want := map[string]string{"landed": "LANDED: ", "cleared": "CLEARED: "}[verdict]
			i := strings.Index(s, want)
			if i < 0 {
				t.Fatalf("code=%d, no %s line:\n%s\n%s", code, want, s, errw.String())
			}
			line := s[i:]
			if !strings.Contains(line, "runs `buddy fleet wait check`") {
				t.Errorf("the stop step names the loop the declaration armed:\n%s", line)
			}
			if strings.Contains(line, "`buddy wait ") {
				t.Errorf("a fleet block names a bare verb:\n%s", line)
			}
			if verdict == "cleared" && !strings.Contains(line, "(`buddy fleet wait clear`)") {
				t.Errorf("CLEARED names the fleet's clear:\n%s", line)
			}
		})
	}
}

// #113: `buddy inbox <id>` of an original said its correction was "queued
// for you" after the drain had delivered it, while `sent` showed the
// correction's recorded delivery: the re-read now reads the delivery row of
// each correction it names.
func TestInboxRereadSaysWhenTheCorrectionReachedYou(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	inbox := func(args ...string) string {
		t.Helper()
		out, errw, code := f.run(t, f.repo, "", append([]string{"inbox", "--session", "sess-b"}, args...)...)
		if code != 0 {
			t.Fatalf("inbox %v: %s", args, errw)
		}
		return out
	}
	orig, _ := f.sendAs(t, "sess-a", f.repo, "bravo", "hello bravo")
	fix, _ := f.sendAs(t, "sess-a", f.repo, "bravo", "--supersedes", orig, "fixed text")
	// Positive control: before the drain the correction IS queued, and says so.
	if out := inbox(orig); !strings.Contains(out, "SUPERSEDED by #"+fix+" (queued for you) — ") {
		t.Fatalf("an undelivered correction is queued:\n%s", out)
	}
	f.clock = f.clock.Add(time.Minute)
	if out := inbox(); !strings.Contains(out, "#"+fix+" CORRECTS #"+orig+" (above)") {
		t.Fatalf("the drain delivers both:\n%s", out)
	}
	f.clock = f.clock.Add(2 * time.Minute)
	out := inbox(orig)
	if strings.Contains(out, "queued for you") || !strings.Contains(out, "SUPERSEDED by #"+fix+" (reached you 2m ago) — [alpha] hello bravo") {
		t.Fatalf("a delivered correction is named delivered, with when:\n%s", out)
	}
	if strings.Count(strings.TrimSpace(out), "\n") != 1 {
		t.Fatalf("one message line and one state line:\n%s", out)
	}
}
