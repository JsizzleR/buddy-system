package cli

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// B4's siblings (Codex report B4; a900b07 fixed `hello`'s count line). `buddy
// msg`'s result line ended every live arm in "delivery waits for its next tool
// call", which is Claude's beat. Under Codex beat is refused (D-069) and a
// logical session's lifecycle events deliver no mail, so a Codex recipient's
// message waits for its own `buddy inbox` and the sender was told to expect a
// delivery no tool call makes.
//
// What the ledger knows: a harness process is registered to a session only by
// a Claude hook (hookAnchor refuses Codex; a hand-run hello, heardVerb and a
// logical begin register none). A live registered process is positive
// evidence of a Claude session, whose next tool call does drain. With none,
// the ledger cannot tell, so the line keeps today's words and says so, naming
// Codex's `buddy inbox`. The Claude control keeps the exact old line.
func TestMsgResultLineNamesCodexInbox(t *testing.T) {
	boundedParallel(t)
	const unsure = "; the ledger has no live harness process for it, so it cannot say this is a Claude Code session: under Codex no tool call delivers mail, its own `buddy inbox` does"
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, f *fixture) // registers bravo (sess-b)
		env   map[string]string              // bravo's environment for its own inbox
		inbox []string
		want  string // the arm, as today
		codex bool   // the ledger cannot tell: the clause follows the arm
	}{
		{
			// The positive control: a hook-driven hello inside a live Claude
			// process registers it, and the old line stands, word for word.
			name: "claude",
			setup: func(t *testing.T, f *fixture) {
				f.asProcess(400, 7, func() {
					if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "", ""), "hello", "--label", "bravo"); code != 0 {
						t.Fatal(errw)
					}
				})
				f.clock = f.clock.Add(30 * time.Second)
			},
			want: "queued for bravo — registered 30s ago and not seen since; delivery waits for its next tool call",
		},
		{
			name: "claude-idle",
			setup: func(t *testing.T, f *fixture) {
				f.asProcess(400, 7, func() {
					if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "", ""), "hello", "--label", "bravo"); code != 0 {
						t.Fatal(errw)
					}
				})
				if _, errw, code := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "", ""), "idle"); code != 0 {
					t.Fatal(errw)
				}
				f.clock = f.clock.Add(2 * time.Hour)
			},
			want: "queued for bravo — bravo last reported idle 2h ago; a session waiting at its prompt runs no tool, so delivery waits for its next tool call",
		},
		{
			// A native Codex session registers by a hand-run hello under its
			// thread id: no process.
			name: "codex-fresh",
			setup: func(t *testing.T, f *fixture) {
				f.env[EnvCodexThread] = "sess-b"
				defer delete(f.env, EnvCodexThread)
				if _, errw, code := f.run(t, f.wtB, "", "hello", "--label", "bravo"); code != 0 {
					t.Fatal(errw)
				}
				f.clock = f.clock.Add(30 * time.Second)
			},
			env:   map[string]string{EnvCodexThread: "sess-b"},
			inbox: []string{"inbox"},
			want:  "queued for bravo — registered 30s ago and not seen since; delivery waits for its next tool call",
			codex: true,
		},
		{
			// Its verbs move last_seen (heardVerb) and register nothing.
			name: "codex-seen",
			setup: func(t *testing.T, f *fixture) {
				f.env[EnvCodexThread] = "sess-b"
				defer delete(f.env, EnvCodexThread)
				if _, errw, code := f.run(t, f.wtB, "", "hello", "--label", "bravo"); code != 0 {
					t.Fatal(errw)
				}
				f.clock = f.clock.Add(time.Minute)
				if _, errw, code := f.run(t, f.wtB, "", "status"); code != 0 {
					t.Fatal(errw)
				}
				f.clock = f.clock.Add(4 * time.Second)
			},
			env:   map[string]string{EnvCodexThread: "sess-b"},
			inbox: []string{"inbox"},
			want:  "queued for bravo — last seen 4s ago; delivery waits for its next tool call",
			codex: true,
		},
		{
			name: "codex-stale",
			setup: func(t *testing.T, f *fixture) {
				f.env[EnvCodexThread] = "sess-b"
				defer delete(f.env, EnvCodexThread)
				if _, errw, code := f.run(t, f.wtB, "", "hello", "--label", "bravo"); code != 0 {
					t.Fatal(errw)
				}
				f.clock = f.clock.Add(2 * time.Hour)
			},
			env:   map[string]string{EnvCodexThread: "sess-b"},
			inbox: []string{"inbox"},
			want:  "queued for bravo — NOT SEEN FOR 2h, past the 30m stale mark; delivery waits for its next tool call and nothing in the ledger says one is coming",
			codex: true,
		},
		{
			// A logical Codex session with no adapter register: its idle comes
			// from `lifecycle idle`, and no lifecycle event delivers mail.
			name: "codex-logical-idle",
			setup: func(t *testing.T, f *fixture) {
				tok := lifecycleOK(t, f, "begin", "--session", "sess-b", "--label", "bravo").Incarnation
				lifecycleOK(t, f, "idle", "--session", "sess-b", "--incarnation", tok)
				f.clock = f.clock.Add(2 * time.Hour)
			},
			inbox: []string{"inbox", "--session", "sess-b"},
			want:  "queued for bravo — bravo last reported idle 2h ago; a session waiting at its prompt runs no tool, so delivery waits for its next tool call",
			codex: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if out, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
				t.Fatalf("init: %s %s", out, errw)
			}
			if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello", "--label", "alpha"); code != 0 {
				t.Fatal(errw)
			}
			tc.setup(t, f)
			out, errw, code := f.run(t, f.repo, "", "msg", "bravo", "--from", "ana", "ping")
			if code != 0 {
				t.Fatal(errw)
			}
			want := tc.want
			if tc.codex {
				want += unsure
			}
			// The arm is followed by the id clause, never by more of the arm.
			if !strings.Contains(out, want+" (#") && !strings.Contains(out, want+";") && !strings.Contains(out, want+"\n") {
				t.Fatalf("the result line:\n got %q\nwant %q", out, want)
			}
			if !tc.codex {
				if strings.Contains(out, "Codex") || strings.Contains(out, "buddy inbox") {
					t.Fatalf("a Claude recipient's line must not change:\n%s", out)
				}
				return
			}
			// The premise: only the verb the clause names delivers it.
			if n := f.undeliveredTo(t, "sess-b", "bravo"); n != 1 {
				t.Fatalf("%d queued before the inbox", n)
			}
			for k, v := range tc.env {
				f.env[k] = v
			}
			if out, errw, code := f.run(t, f.wtB, "", tc.inbox...); code != 0 || !strings.Contains(out, "] ping\n") {
				t.Fatalf("buddy inbox must deliver it: %d %q %s", code, out, errw)
			}
			if n := f.undeliveredTo(t, "sess-b", "bravo"); n != 0 {
				t.Fatalf("inbox left %d queued", n)
			}
		})
	}
}

// authority.go: `buddy authority add` promised "sessions are told once, on
// their next tool call". Only beat consumes that notice, and under Codex beat
// is refused, so a Codex session is never told on a tool call; `buddy status`
// is where it can read the AUTHORITY block. The line now says so.
func TestAuthorityAddNamesCodexStatus(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	out, errw, code := f.run(t, f.repo, "", "authority", "add", "PLAN.md")
	if code != 0 {
		t.Fatal(errw)
	}
	const want = "watching PLAN.md: a Claude Code session is told once, on its next tool call, when it changes after it started; a Codex session is not told (no Codex hook delivers this notice): its `buddy status` shows it\n"
	if out != want {
		t.Fatalf("authority add:\n got %q\nwant %q", out, want)
	}
	f.clock = f.clock.Add(time.Hour)
	touch(t, filepath.Join(f.repo, "PLAN.md"), f.clock)
	f.clock = f.clock.Add(time.Minute)
	// Under Codex the beat is refused and tells no one.
	f.env[EnvCodexThread] = "sess-a"
	if out, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "Bash", ""), "beat"); code == 0 || strings.Contains(out, "changed on disk") {
		t.Fatalf("a Codex beat must be refused: %d %q %s", code, out, errw)
	}
	// What the line names shows it.
	if out, errw, code := f.run(t, f.repo, "", "status"); code != 0 || !strings.Contains(out, "AUTHORITY    PLAN.md changed on disk") {
		t.Fatalf("status under Codex must show it: %d %q %s", code, out, errw)
	}
	delete(f.env, EnvCodexThread)
	// The control: a Claude beat is told, once.
	out, errw, code = f.run(t, f.repo, hookJSON("sess-a", f.repo, "Bash", ""), "beat")
	if code != 0 || !strings.Contains(additionalContext(t, out), "PLAN.md changed on disk") {
		t.Fatalf("a Claude beat must carry the notice: %d %q %s", code, out, errw)
	}
}

// dirty.go: `whose`'s advisory said `buddy msg` "reaches a live session after
// its next tool call", the same Claude-only promise; it now names a Codex
// session's `buddy inbox` beside it.
func TestWhoseAdvisoryNamesCodexInbox(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.twoSessionsIn(t, f.repo)
	f.edit(t, f.repo, "sess-a", "CHANGELOG.md", "alpha\n")
	out, errw, code := f.run(t, f.repo, "", "whose", "CHANGELOG.md")
	if code != 0 {
		t.Fatal(errw)
	}
	const want = "`buddy msg <label> \"...\"` reaches a\nlive Claude Code session after its next tool call, a Codex one at its `buddy inbox`.\n"
	if !strings.Contains(out, want) {
		t.Fatalf("whose advisory:\n%s", out)
	}
}

// The Codex hello's named-session hint (a900b07) printed `buddy inbox
// --session <id>` with the id fenced but not shell-quoted (codex-probe's pass
// on a900b07, P2, relayed by orc-19 as a lead). A session id is free text
// (`hello --session` validates nothing), so an id with a space pasted as two
// arguments and the named command refused. It is quoted the way a pasted
// slug is (shellQuote, D-066): bare when safe, so `sess-b`'s line in
// TestCodexHelloCountNamesInbox is unchanged (the control), single-quoted
// otherwise. The test runs exactly what the line names, through a shell.
func TestCodexHelloNamedHintQuotesTheID(t *testing.T) {
	boundedParallel(t)
	for _, id := range []string{"sess x", "it's;echo hi"} {
		t.Run(id, func(t *testing.T) {
			f := newFixture(t)
			if out, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
				t.Fatalf("init: %s %s", out, errw)
			}
			if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello", "--label", "alpha"); code != 0 {
				t.Fatal(errw)
			}
			if _, errw, code := f.run(t, f.wtB, "", "hello", "--session", id, "--label", "bravo"); code != 0 {
				t.Fatal(errw)
			}
			if _, errw, code := f.run(t, f.repo, "", "msg", "bravo", "--from", "ana", "ping"); code != 0 {
				t.Fatal(errw)
			}
			f.env[EnvHarness] = "codex"
			out, errw, code := f.run(t, f.wtB, "", "hello", "--session", id, "--label", "bravo")
			if code != 0 {
				t.Fatal(errw)
			}
			const pre, post = "that session runs `", "`.\n"
			i := strings.Index(out, pre)
			if i < 0 || !strings.HasSuffix(out, post) {
				t.Fatalf("no named hint:\n%s", out)
			}
			cmd := out[i+len(pre) : len(out)-len(post)]
			// The shell's own word splitting, as a paste would get it.
			words, err := exec.Command("/bin/sh", "-c", "for w in "+cmd+"; do printf '%s\\0' \"$w\"; done").Output()
			if err != nil {
				t.Fatalf("the hint does not parse as shell words: %q: %v", cmd, err)
			}
			argv := strings.Split(strings.TrimSuffix(string(words), "\x00"), "\x00")
			if len(argv) != 4 || argv[0] != "buddy" || argv[3] != id {
				t.Fatalf("the pasted hint is %q, not one argument for the id %q", argv, id)
			}
			if out, errw, code := f.run(t, f.wtB, "", argv[1:]...); code != 0 || !strings.Contains(out, "] ping\n") {
				t.Fatalf("the named command must deliver it: %d %q %s", code, out, errw)
			}
		})
	}
}

// A REWRITTEN id is never printed as a command (Codex code pass on the
// quoting fix). fence.Line shows a tab as a space, so quoting the fenced id
// of `sess<TAB>x` printed `buddy inbox --session 'sess x'`, which names a
// different live session and drains ITS mail. Such a line names `buddy inbox`
// run as itself, and that command delivers the right session's mail. The
// control is the pasteable id in TestCodexHelloNamedHintQuotesTheID.
func TestCodexHelloNamedHintNeverNamesARewrittenID(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	if out, errw, code := f.run(t, f.repo, "", "init"); code != 0 {
		t.Fatalf("init: %s %s", out, errw)
	}
	for _, s := range [][2]string{{"sess\tx", "tabbed"}, {"sess x", "spaced"}} {
		if _, errw, code := f.run(t, f.wtB, "", "hello", "--session", s[0], "--label", s[1]); code != 0 {
			t.Fatal(errw)
		}
		if _, errw, code := f.run(t, f.repo, "", "msg", s[1], "--from", "ana", "for "+s[1]); code != 0 {
			t.Fatal(errw)
		}
	}
	f.env[EnvHarness] = "codex"
	out, errw, code := f.run(t, f.wtB, "", "hello", "--session", "sess\tx", "--label", "tabbed")
	if code != 0 {
		t.Fatal(errw)
	}
	const want = "BUDDY: 1 queued message(s) not shown here; no tool call delivers them under Codex: that session runs `buddy inbox` as itself (its id does not paste as printed).\n"
	if !strings.HasSuffix(out, want) || strings.Contains(out, "--session") {
		t.Fatalf("a rewritten id must not be printed as a command:\n%s", out)
	}
	delete(f.env, EnvHarness)
	f.env[EnvCodexThread] = "sess\tx"
	if out, errw, code := f.run(t, f.wtB, "", "inbox"); code != 0 || !strings.Contains(out, "] for tabbed\n") || strings.Contains(out, "for spaced") {
		t.Fatalf("`buddy inbox` as itself must deliver its own mail only: %d %q %s", code, out, errw)
	}
}

// The reserve the claims list leaves for the count line must hold the line,
// at every count and every id shape: the hand-summed 128 + id fell a byte
// short at 100 messages (Codex code pass).
func TestHelloCountReserveHoldsTheLine(t *testing.T) {
	ids := []string{"sess-b", "sess x", "it's;echo hi", "sess\tx", strings.Repeat("a", 128), strings.Repeat("'", 128)}
	for _, n := range []int{1, 9, 99, 100, 999, 99999, 1000000} {
		for _, id := range ids {
			for _, mode := range [][2]bool{{false, false}, {true, false}, {true, true}, {false, true}} {
				line := helloCountLine(mode[0], mode[1], n, id)
				if r := helloCountReserve(mode[0], mode[1], n, id); r < len(line) {
					t.Fatalf("codex=%v named=%v n=%d id=%q: reserve %d < line %d", mode[0], mode[1], n, id, r, len(line))
				}
				// The rest it prints is never more than the count reserved for.
				if m := len(helloCountLine(mode[0], mode[1], n/2+1, id)); m > helloCountReserve(mode[0], mode[1], n, id) {
					t.Fatalf("a smaller rest outgrew the reserve: %d", m)
				}
			}
		}
	}
}
