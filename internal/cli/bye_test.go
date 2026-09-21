package cli

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// The delayed `bye`, and the live incarnation it used to end (D-025).
//
// This file replaces TestKnownGap_ADelayedByeEndsALiveIncarnation, which
// pinned the WRONG behaviour on purpose so a fix could not land unnoticed.
// The fix: a hook-driven hello registers the harness PROCESS it was spawned
// by (fixture.asProcess plays that part), and a hook-driven bye ends the
// session only when no OTHER registered process is still alive.
//
// Every scenario below was named by the Codex design pass as a way the first
// draft of the fence let the defect back in, so each is pinned: both exit
// orders for two processes on one id, the manual hello that used to wipe the
// pid, the unresolvable anchor that used to fall through to "end", the
// recycled pid, and the unbound session that must keep the old behaviour.

// rowFor returns the roster line for label, or "".
func rowFor(out, label string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, " "+label+" ") {
			return line
		}
	}
	return ""
}

// pidsOn returns the roster row's `pid` annotation value ("100,200 GONE"
// shape) or "". Parsed rather than substring-matched: the row carries the
// worktree path, and a temp directory name once contained the very pid a
// test was asserting absent (measured: it did, in
// ...TestTwoProcesses..._1588962100/001/repo).
func pidsOn(row string) string {
	i := strings.Index(row, "  pid ")
	if i < 0 {
		return ""
	}
	rest := row[i+len("  pid "):]
	if j := strings.Index(rest, "  "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

func TestADelayedByeMustNotEndALiveIncarnation(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)

	// Incarnation 1 runs in harness process 100 and ends cleanly.
	f.asProcess(100, 1, func() {
		if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "bye"); code != 0 {
			t.Fatalf("bye: %s", errw)
		}
	})
	f.kill(100)
	// It is resumed under the SAME session id (`claude --resume` keeps it) in
	// a NEW harness process. Hello mints incarnation 2, registers 200.
	f.asProcess(200, 2, func() {
		if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello", "--label", "alpha"); code != 0 {
			t.Fatalf("re-hello: %s", errw)
		}
		if _, errw, code := f.run(t, f.repo, "", "claim", "api-work", "--session", "sess-a",
			"--desc", "live work", "--scope", "internal/api"); code != 0 {
			t.Fatalf("claim: %s", errw)
		}
	})

	// THE DELAYED BYE: incarnation 1's SessionEnd hook, from process 100,
	// fires late. Same payload as the first — it cannot name an incarnation.
	// Process 100 is gone by now (it is exiting), 200 is alive.
	f.proc.PID, f.proc.Born = 100, 1
	out, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "bye")
	f.proc.PID, f.proc.Born = 0, 0
	if code != 0 {
		t.Fatalf("a refused hook bye must still exit 0 (the hook line ends in exit 0 anyway): %s", errw)
	}
	if out != "" || !strings.Contains(errw, "stays live") || !strings.Contains(errw, "200") || !strings.Contains(errw, "never registered") {
		t.Fatalf("the refusal must go to stderr, name the live process and say the bye was a stranger's:\n%q\n%q", out, errw)
	}

	// Incarnation 2 is alive and working, and the ledger must say so.
	roster, _, _ := f.run(t, f.repo, "", "sessions")
	row := rowFor(roster, "alpha")
	if row == "" || strings.Contains(row, "ended") || !strings.Contains(row, "pid 200") {
		t.Fatalf("the live incarnation must still be live and show its process:\n%s", roster)
	}
	// Its heartbeats still count (Beat is WHERE ended IS NULL).
	f.clock = f.clock.Add(90 * time.Second)
	f.asProcess(200, 2, func() {
		if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "Edit", "internal/api/x.go"), "beat"); code != 0 {
			t.Fatalf("beat: %s", errw)
		}
	})
	roster, _, _ = f.run(t, f.repo, "", "sessions")
	if row := rowFor(roster, "alpha"); !strings.Contains(row, "seen 0s") {
		t.Fatalf("a live session's beat must move seen:\n%s", roster)
	}
	// A PEER starting up runs orphanEnded inside Hello. The claim must
	// survive, because the session is not ended.
	if _, errw, code := f.run(t, f.wtB, hookJSON("sess-c", f.wtB, "", ""), "hello", "--label", "charlie"); code != 0 {
		t.Fatalf("peer hello: %s", errw)
	}
	if ls, _, _ := f.run(t, f.repo, "", "ls"); !strings.Contains(ls, "api-work") || !strings.Contains(ls, "open") {
		t.Fatalf("the live session's claim must survive a peer's hello:\n%s", ls)
	}

	// And the REAL exit of incarnation 2 ends it, from its own process.
	f.asProcess(200, 2, func() {
		if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "bye"); code != 0 {
			t.Fatalf("real bye: %s", errw)
		}
	})
	roster, _, _ = f.run(t, f.repo, "", "sessions")
	if row := rowFor(roster, "alpha"); !strings.Contains(row, "ended") {
		t.Fatalf("the registered process's own bye must end the session:\n%s", roster)
	}
}

// Two harness processes on ONE session id (a second --resume while the first
// still runs), in BOTH exit orders. The first draft of the fence handled only
// "the earlier process exits first"; the later one exiting first ended the
// row under the earlier, still-editing process.
func TestTwoProcessesOnOneSessionIdEndItOnlyWhenTheLastExits(t *testing.T) {
	boundedParallel(t)
	for _, tc := range []struct {
		name       string
		exitFirst  int
		exitSecond int
	}{
		{"earlier process exits first", 100, 200},
		{"later process exits first", 200, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.initAndHello(t)
			f.asProcess(100, 1, func() {
				if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello", "--label", "alpha"); code != 0 {
					t.Fatalf("hello 100: %s", errw)
				}
			})
			// The second process registers itself by its first tool call —
			// its hello refreshes the live row; the beat is what a session
			// does all day, and either binds it.
			f.asProcess(200, 2, func() {
				if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "Bash", ""), "beat"); code != 0 {
					t.Fatalf("beat 200: %s", errw)
				}
			})
			roster, _, _ := f.run(t, f.repo, "", "sessions")
			if row := rowFor(roster, "alpha"); pidsOn(row) != "100,200" {
				t.Fatalf("both processes must show on the row:\n%s", roster)
			}

			born := map[int]int64{100: 1, 200: 2}
			f.asProcess(tc.exitFirst, born[tc.exitFirst], func() {
				if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "bye"); code != 0 {
					t.Fatalf("first bye: %s", errw)
				}
			})
			f.kill(tc.exitFirst)
			roster, _, _ = f.run(t, f.repo, "", "sessions")
			row := rowFor(roster, "alpha")
			if strings.Contains(row, "ended") {
				t.Fatalf("one process exiting must not end a session another process is still in:\n%s", roster)
			}
			if got := pidsOn(row); got != strconv.Itoa(tc.exitSecond) {
				t.Fatalf("the exited process must be gone from the row and the other still on it: pid %q\n%s", got, roster)
			}
			f.asProcess(tc.exitSecond, born[tc.exitSecond], func() {
				if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "bye"); code != 0 {
					t.Fatalf("second bye: %s", errw)
				}
			})
			roster, _, _ = f.run(t, f.repo, "", "sessions")
			if row := rowFor(roster, "alpha"); !strings.Contains(row, "ended") {
				t.Fatalf("the last process's bye must end the session:\n%s", roster)
			}
		})
	}
}

// A hand-run `buddy hello --session X` on a live, hook-registered session
// used to overwrite the pid with the hello's own — with 0 here, it would
// UNBIND the session and let a delayed bye through.
func TestManualHelloDoesNotUnbindAHookRegisteredSession(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.asProcess(200, 2, func() {
		if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello", "--label", "alpha"); code != 0 {
			t.Fatalf("hello: %s", errw)
		}
	})
	// The manual refresh: no hook JSON, so no anchor.
	if _, errw, code := f.run(t, f.repo, "", "hello", "--session", "sess-a"); code != 0 {
		t.Fatalf("manual hello: %s", errw)
	}
	// A stranger's bye (a delayed one from a dead process) must still be
	// refused: the registration survived the manual refresh.
	f.proc.PID, f.proc.Born = 100, 1
	_, errw, _ := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "bye")
	f.proc.PID, f.proc.Born = 0, 0
	if !strings.Contains(errw, "stays live") {
		t.Fatalf("the manual hello unbound the session — a stranger's bye ended it: %q", errw)
	}
	if roster, _, _ := f.run(t, f.repo, "", "sessions"); strings.Contains(rowFor(roster, "alpha"), "ended") {
		t.Fatal("session ended")
	}
}

// A bye whose own anchor could not be resolved must NOT end a session that
// is registered to a live process. The first draft read "anchor unknown" as
// "old behaviour: end", which is the defect with one more step.
func TestByeWithNoAnchorIsRefusedByALiveRegistration(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.asProcess(200, 2, func() {
		if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello", "--label", "alpha"); code != 0 {
			t.Fatalf("hello: %s", errw)
		}
	})
	// Hook-driven bye, anchor unresolved (f.proc is zero here).
	_, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "bye")
	if code != 0 || !strings.Contains(errw, "stays live") || !strings.Contains(errw, "200") {
		t.Fatalf("want a refusal naming 200, got code %d %q", code, errw)
	}
	// The MANUAL form is refused the same way, as an ERROR naming --force,
	// and --force is the operator's explicit act.
	_, errw, code = f.run(t, f.repo, "", "bye", "sess-a")
	if code == 0 || !strings.Contains(errw, "--force") || !strings.Contains(errw, "200") {
		t.Fatalf("manual bye of a live-registered session must refuse and name --force: code %d %q", code, errw)
	}
	if roster, _, _ := f.run(t, f.repo, "", "sessions"); strings.Contains(rowFor(roster, "alpha"), "ended") {
		t.Fatal("refused bye ended the session")
	}
	if _, errw, code := f.run(t, f.repo, "", "bye", "sess-a", "--force"); code != 0 {
		t.Fatalf("--force: %s", errw)
	}
	if roster, _, _ := f.run(t, f.repo, "", "sessions"); !strings.Contains(rowFor(roster, "alpha"), "ended") {
		t.Fatal("--force must end it")
	}
}

// The registered process is DEAD (killed -9, no bye of its own) and a bye
// arrives from elsewhere: nothing alive is being protected, so it ends.
// And a pid that exists but was born at a different time is a RECYCLED pid,
// which reads as dead — the birth time is the discriminator.
func TestByeEndsWhenTheRegisteredProcessIsGoneOrRecycled(t *testing.T) {
	boundedParallel(t)
	for _, tc := range []struct {
		name  string
		after func(f *fixture)
	}{
		{"process gone", func(f *fixture) { f.kill(200) }},
		{"pid recycled", func(f *fixture) { f.alive[200] = 999 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.initAndHello(t)
			f.asProcess(200, 2, func() {
				if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello", "--label", "alpha"); code != 0 {
					t.Fatalf("hello: %s", errw)
				}
			})
			roster, _, _ := f.run(t, f.repo, "", "sessions")
			if row := rowFor(roster, "alpha"); !strings.Contains(row, "pid 200") || strings.Contains(row, "GONE") {
				t.Fatalf("a live registered process prints plainly:\n%s", roster)
			}
			tc.after(f)
			roster, _, _ = f.run(t, f.repo, "", "sessions")
			if row := rowFor(roster, "alpha"); !strings.Contains(row, "pid 200 GONE") {
				t.Fatalf("a dead or recycled process prints GONE:\n%s", roster)
			}
			_, errw, code := f.run(t, f.repo, "", "bye", "sess-a")
			if code != 0 {
				t.Fatalf("bye: %s", errw)
			}
			if roster, _, _ := f.run(t, f.repo, "", "sessions"); !strings.Contains(rowFor(roster, "alpha"), "ended") {
				t.Fatalf("nothing alive was registered, so the bye must end it:\n%s", roster)
			}
		})
	}
}

// A session that registered NO process — a hand-run hello, or a ledger from
// before the table — is unbound, and its bye ends it exactly as before. The
// fence must never turn the hook off for the sessions it cannot bind.
func TestUnboundSessionByeIsTheOldBehaviour(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t) // f.proc is zero: nothing registered
	roster, _, _ := f.run(t, f.repo, "", "sessions")
	if row := rowFor(roster, "alpha"); strings.Contains(row, "pid ") {
		t.Fatalf("an unbound session prints no pid:\n%s", roster)
	}
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "bye"); code != 0 {
		t.Fatalf("bye: %s", errw)
	}
	if roster, _, _ := f.run(t, f.repo, "", "sessions"); !strings.Contains(rowFor(roster, "alpha"), "ended") {
		t.Fatalf("an unbound session's bye ends it:\n%s", roster)
	}
}

// The pane rides the hook-driven hello from the environment the harness
// inherited, is REPLACED on revival (a pane the old incarnation sat in must
// not survive into one that reported none), and is kept by a manual refresh.
func TestTerminalHandleIsRecordedFromTheHookEnvironment(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.env["HERDR_PANE_ID"] = "w14:pA"
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello"); code != 0 {
		t.Fatalf("hello: %s", errw)
	}
	roster, _, _ := f.run(t, f.repo, "", "sessions")
	if row := rowFor(roster, "alpha"); !strings.Contains(row, "pane herdr:w14:pA") {
		t.Fatalf("want the pane on the row:\n%s", roster)
	}
	// A manual hello from a terminal with its own pane must not overwrite it.
	f.env["HERDR_PANE_ID"] = "w1:pZ"
	if _, errw, code := f.run(t, f.repo, "", "hello", "--session", "sess-a"); code != 0 {
		t.Fatalf("manual hello: %s", errw)
	}
	roster, _, _ = f.run(t, f.repo, "", "sessions")
	if row := rowFor(roster, "alpha"); !strings.Contains(row, "pane herdr:w14:pA") {
		t.Fatalf("a manual hello must not overwrite the reported pane:\n%s", roster)
	}
	// Revival with no pane in the environment clears it.
	delete(f.env, "HERDR_PANE_ID")
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "bye"); code != 0 {
		t.Fatalf("bye: %s", errw)
	}
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello"); code != 0 {
		t.Fatalf("re-hello: %s", errw)
	}
	roster, _, _ = f.run(t, f.repo, "", "sessions")
	if row := rowFor(roster, "alpha"); strings.Contains(row, "pane ") {
		t.Fatalf("a revived session that reported no pane must show none:\n%s", roster)
	}
	// A pane with a space in it stays ONE token (D-017).
	f.env["TMUX_PANE"] = "%3 ended 0s"
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello"); code != 0 {
		t.Fatalf("hello: %s", errw)
	}
	roster, _, _ = f.run(t, f.repo, "", "sessions")
	if row := rowFor(roster, "alpha"); !strings.Contains(row, "pane tmux:%3␣ended␣0s") {
		t.Fatalf("the pane column must be one token:\n%s", roster)
	}
}

// A second positional is refused, not dropped: `bye a b` ending a with
// nothing said about b is the quiet wrong-target shape (Codex code pass).
func TestManualByeRefusesASecondSession(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	_, errw, code := f.run(t, f.repo, "", "bye", "sess-a", "sess-b")
	if code == 0 || !strings.Contains(errw, "one session") {
		t.Fatalf("want a refusal: %d %q", code, errw)
	}
	if roster, _, _ := f.run(t, f.repo, "", "sessions"); strings.Contains(rowFor(roster, "alpha"), "ended") {
		t.Fatal("a refused bye must end nothing")
	}
}
