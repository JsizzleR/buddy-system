package cli

// The Fable cross-check's folds (D-193): each finding reproduced as a test
// beside its positive control. The cross-check ran twice, on 9504fa7 and on
// 9fa8dad, and the two reports overlap; a test names the finding, not the run.

import (
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// What `buddy fleet <verb>` prints for the session to run next names
// `buddy fleet`: run bare from the repository, each acted in the home ledger.
// The timer's line was the measured harm: its bare `buddy wait clear` cleared
// the session's HOME wait and left the fleet's timer open. The control is the
// same verb run bare, whose lines stay `buddy ...`.
func TestFleetVerbTimerAndHintsNameTheFleet(t *testing.T) {
	boundedParallel(t)
	fe, _ := bothEnv(t)
	if _, errw, code := fe.as(t, "sess-a", fe.repo, "claim", "h01", "--desc", "d", "--scope", "pkg"); code != 0 {
		t.Fatal(errw)
	}
	fe.hook(t, "hello", "sess-x", fe.repo)
	if _, errw, code := fe.as(t, "sess-x", fe.repo, "wait", "--on", "h01"); code != 0 {
		t.Fatal(errw)
	}
	out, errw, code := fe.as(t, "sess-x", fe.repo, "fleet", "wait", "--until", "2h")
	if code != 0 || !strings.Contains(out, "`buddy fleet wait clear`") {
		t.Fatalf("the fleet timer's end names `buddy fleet wait clear`: %d %q %q", code, out, errw)
	}
	// Followed as printed: the fleet's timer goes, the home's wait stays.
	out, _, _ = fe.as(t, "sess-x", fe.repo, "fleet", "wait", "clear")
	if strings.Contains(out, "h01") || !strings.Contains(out, "its next `buddy fleet wait check` says NO WAIT") {
		t.Fatalf("the fleet's clear: its own wait, and its check named under the selector: %q", out)
	}
	if out, _, _ := fe.as(t, "sess-x", fe.repo, "wait", "ls"); !strings.Contains(out, "h01") {
		t.Fatalf("the home wait must survive the fleet's clear: %q", out)
	}
	// The positional hint and the inbox miss.
	if _, errw, _ := fe.as(t, "sess-x", fe.repo, "fleet", "wait", "i01"); !strings.Contains(errw, "did you mean `buddy fleet wait --on i01`") {
		t.Fatalf("the fleet wait's hint: %q", errw)
	}
	if _, errw, _ := fe.as(t, "sess-x", fe.repo, "fleet", "inbox", "999"); !strings.Contains(errw, "`buddy fleet sent 999` is the sender's view") {
		t.Fatalf("the fleet inbox miss names the fleet's sent: %q", errw)
	}
	// who's EXIT line, with a fleet claim held: releasing it bare would free
	// a home claim of that slug.
	if _, errw, code := fe.as(t, "sess-x", fe.repo, "fleet", "claim", "item", "--desc", "d", "--scope", ".buddy/slot/item"); code != 0 {
		t.Fatal(errw)
	}
	if out, _, _ := fe.as(t, "sess-x", fe.repo, "fleet", "who", "sess-x"); !strings.Contains(out, "`buddy fleet release <slug>` first: item") {
		t.Fatalf("the fleet's who names `buddy fleet release`: %q", out)
	}
	// Controls: the bare verbs keep the bare commands.
	out, _, _ = fe.as(t, "sess-x", fe.repo, "wait", "--until", "2h")
	if !strings.Contains(out, "`buddy wait clear`") {
		t.Fatalf("control: the home timer's end stays bare: %q", out)
	}
	if _, errw, _ := fe.as(t, "sess-x", fe.repo, "wait", "i01"); !strings.Contains(errw, "did you mean `buddy wait --on i01`") {
		t.Fatalf("control: the home wait's hint stays bare: %q", errw)
	}
	if _, errw, _ := fe.as(t, "sess-x", fe.repo, "inbox", "999"); !strings.Contains(errw, "`buddy sent 999` is the sender's view") {
		t.Fatalf("control: the home inbox miss stays bare: %q", errw)
	}
	if _, errw, code := fe.as(t, "sess-x", fe.repo, "claim", "hitem", "--desc", "d", "--scope", "hpkg"); code != 0 {
		t.Fatal(errw)
	}
	if out, _, _ := fe.as(t, "sess-x", fe.repo, "who", "sess-x"); !strings.Contains(out, "`buddy release <slug>` first: hitem") {
		t.Fatalf("control: the home's who stays bare: %q", out)
	}
}

// A claim handed to a waiter in the fleet: beat's LANDED line beside the home
// and `buddy fleet wait check` name `buddy fleet who <slug>` for its scopes;
// bare, from the repository, it answered "no such target". The control is a
// handed claim in the home, whose check stays bare.
func TestFleetLandedHandedNamesTheFleetsWho(t *testing.T) {
	boundedParallel(t)
	fe, _ := bothEnv(t)
	lane := fe.mkdirPlain(t, "lane")
	fe.hook(t, "hello", "lane-1", lane)
	if _, errw, code := fe.as(t, "lane-1", lane, "claim", "i01", "--desc", "d", "--scope", ".buddy/slot/i01"); code != 0 {
		t.Fatal(errw)
	}
	fe.hook(t, "hello", "sess-x", fe.repo)
	if _, errw, code := fe.as(t, "sess-x", fe.repo, "fleet", "wait", "--on", "i01"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := fe.as(t, "lane-1", lane, "release", "i01", "--to", "sess-x"); code != 0 {
		t.Fatal(errw)
	}
	out, _, _ := fe.hook(t, "beat", "sess-x", fe.repo)
	if ctx := promptContext(t, out); !strings.Contains(ctx, "LANDED") || !strings.Contains(ctx, "(`buddy fleet who <slug>` prints them)") {
		t.Fatalf("beat's fleet LANDED names `buddy fleet who`: %q", ctx)
	}
	chk, _, _ := fe.as(t, "sess-x", fe.repo, "fleet", "wait", "check")
	if !strings.Contains(chk, "LANDED") || !strings.Contains(chk, "(`buddy fleet who <slug>` prints them)") {
		t.Fatalf("the fleet's check names `buddy fleet who`: %q", chk)
	}
	// Control: the same hand-on in the home.
	if _, errw, code := fe.as(t, "sess-b", fe.wtB, "claim", "h01", "--desc", "d", "--scope", "hpkg"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := fe.as(t, "sess-x", fe.repo, "wait", "--on", "h01"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := fe.as(t, "sess-b", fe.wtB, "release", "h01", "--to", "sess-x"); code != 0 {
		t.Fatal(errw)
	}
	if chk, _, _ := fe.as(t, "sess-x", fe.repo, "wait", "check"); !strings.Contains(chk, "(`buddy who <slug>` prints them)") {
		t.Fatalf("control: the home's check stays bare: %q", chk)
	}
}

// `buddy fleet sent`'s broadcast tail names `buddy fleet sent <id>` for the
// wake addresses: bare, from the repository, it is the home's message of that
// number, and its wakes are other sessions'. The control is the bare `sent`
// of a home broadcast. A wake needs an idle recipient with a live socket
// (wake_test.go's fixture).
func TestFleetSentNamesTheFleetsReport(t *testing.T) {
	boundedParallel(t)
	fe, _ := bothEnv(t)
	fe.sockDir = shortSockDir(t)
	lane := fe.mkdirPlain(t, "lane")
	fe.asProcess(400, 1, func() { fe.hook(t, "hello", "lane-1", lane) })
	l, err := net.Listen("unix", filepath.Join(fe.sockDir, "400.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	stopIn(t, fe.fixture, "lane-1", lane)
	fe.hook(t, "hello", "sess-x", fe.repo)
	fe.clock = fe.clock.Add(10 * time.Minute)
	if _, errw, code := fe.as(t, "sess-x", fe.repo, "fleet", "msg", "all", "take the router bundle"); code != 0 {
		t.Fatal(errw)
	}
	if out, _, _ := fe.as(t, "sess-x", fe.repo, "fleet", "sent"); !strings.Contains(out, "can be woken: buddy fleet sent ") {
		t.Fatalf("the fleet's sent names `buddy fleet sent <id>`: %q", out)
	}
	// Control: a home broadcast to the same idle process, read bare.
	fe.asProcess(400, 1, func() { fe.hook(t, "hello", "sess-h", fe.wtB) })
	stopIn(t, fe.fixture, "sess-h", fe.wtB)
	fe.clock = fe.clock.Add(10 * time.Minute)
	if _, errw, code := fe.as(t, "sess-x", fe.repo, "msg", "all", "home bundle"); code != 0 {
		t.Fatal(errw)
	}
	if out, _, _ := fe.as(t, "sess-x", fe.repo, "sent"); !strings.Contains(out, "can be woken: buddy sent ") {
		t.Fatalf("control: the home's sent stays bare: %q", out)
	}
}

// busy's home roster note says the fleet row "still moves" only for a session
// the fleet knows: for one neither ledger knows, one document said it moves
// and that the session is not on the fleet's roster. The control is a session
// on the fleet alone (hello from a plain directory), whose fleet row does move.
func TestBothLedgersBusyRosterNoteForNeitherRoster(t *testing.T) {
	boundedParallel(t)
	fe, _ := bothEnv(t)
	out, _, _ := fe.hook(t, "busy", "sess-z", fe.repo)
	ctx := promptContext(t, out)
	if strings.Contains(ctx, "row still moves") || !strings.Contains(ctx, "is not on fleet inv's roster") {
		t.Fatalf("a session neither ledger knows: no fleet row moves: %q", ctx)
	}
	fe.hook(t, "hello", "sess-f", fe.mkdirPlain(t, "lane"))
	out, _, _ = fe.hook(t, "busy", "sess-f", fe.repo)
	if ctx := promptContext(t, out); !strings.Contains(ctx, "row still moves") {
		t.Fatalf("control: a session on the fleet's roster is told its fleet row moves: %q", ctx)
	}
}

// A hand-run `buddy bye <id>` from a governed repository is a bare typed verb:
// it acts in the home ledger alone. It ended a live lane's fleet row with
// --force, or was refused there, and either way said nothing. The control is
// the session's own SessionEnd, which ends both rows
// (TestBothLedgersByeEndsBothRows), here for the lane's own bye.
func TestBothLedgersHandRunByeIsTheHomesAlone(t *testing.T) {
	boundedParallel(t)
	fe, _ := bothEnv(t)
	lane := fe.mkdirPlain(t, "lane")
	fe.asProcess(300, 1, func() { fe.hook(t, "hello", "lane-1", lane) })
	fe.hook(t, "hello", "sess-x", fe.repo)
	before := fleetRow(t, fe, "lane-1")
	for _, args := range [][]string{{"bye", "lane-1"}, {"bye", "lane-1", "--force"}} {
		fe.as(t, "sess-x", fe.repo, args...)
		if after := fleetRow(t, fe, "lane-1"); after != before || strings.Contains(after, "ended") {
			t.Fatalf("%v from the repository changed the fleet row:\nbefore %q\nafter  %q", args, before, after)
		}
	}
	// With its process gone nothing fences the fleet row, so only the
	// directory's jurisdiction keeps a hand-run bye out of it.
	fe.kill(300)
	fe.as(t, "sess-x", fe.repo, "bye", "lane-1")
	if after := fleetRow(t, fe, "lane-1"); strings.Contains(after, "ended") {
		t.Fatalf("a hand-run bye from the repository ended a fleet row: %q", after)
	}
	// Control: the lane's own bye, in the fleet, ends it.
	fe.asProcess(300, 1, func() { fe.hook(t, "bye", "lane-1", lane) })
	if row := fleetRow(t, fe, "lane-1"); !strings.Contains(row, "ended") {
		t.Fatalf("control: the lane's own bye ends its fleet row: %q", row)
	}
}

// Invariant 12 in the fleet beside a home: the fleet half registers the
// session's process at hello and at beat, and a bye is fenced by the fleet's
// own session_procs: a delayed bye from a dead incarnation's process does not
// end the live session's fleet row. No stage-2 test registered a process, so
// HelloFrom, BeatFrom and ByeFrom with an empty ProcRef each passed every
// test (the cross-check's mutants). The control is process 200's own bye.
func TestBothLedgersByeIsFencedByTheFleetsOwnProcs(t *testing.T) {
	for _, how := range []string{"hello", "beat"} {
		t.Run(how, func(t *testing.T) {
			boundedParallel(t)
			fe, _ := bothEnv(t)
			fe.asProcess(100, 1, func() { fe.hook(t, "hello", "sess-x", fe.repo) })
			fe.asProcess(200, 2, func() { fe.hook(t, how, "sess-x", fe.repo) })
			fe.asProcess(100, 1, func() {
				if _, errw, code := fe.hook(t, "bye", "sess-x", fe.repo); code != 0 {
					t.Fatal(errw)
				}
			})
			if row := homeRow(t, fe, "sess-x"); strings.Contains(row, "ended") || row == "" {
				t.Fatalf("premise: the home row stays live under process 200: %q", row)
			}
			if row := fleetRow(t, fe, "sess-x"); strings.Contains(row, "ended") || row == "" {
				t.Fatalf("the fleet row must stay live under process 200 (%s): %q", how, row)
			}
			fe.kill(100)
			fe.asProcess(200, 2, func() { fe.hook(t, "bye", "sess-x", fe.repo) })
			if row := fleetRow(t, fe, "sess-x"); !strings.Contains(row, "ended") {
				t.Fatalf("control: process 200's own bye ends the fleet row: %q", row)
			}
		})
	}
}

// The Stop beside a home samples the base in the home only: a fleet keeps no
// base (D-191's skip). With the fleet's sample asking for the base too, a
// base row was written into the fleet and no test saw it (the cross-check).
// The control is the home's own base row from the same Stop.
func TestBothLedgersStopWritesNoFleetBase(t *testing.T) {
	boundedParallel(t)
	fe, _ := bothEnv(t)
	fe.hook(t, "hello", "sess-x", fe.repo)
	fe.clock = fe.clock.Add(time.Minute)
	if _, errw, code := fe.run(t, fe.repo, stopTranscript(t, fe, "sess-x", fe.clock), "idle"); code != 0 {
		t.Fatal(errw)
	}
	bases := func(path string) map[string]store.Base {
		t.Helper()
		st, err := store.Open(path, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		b, err := st.Bases()
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	home := homeStore(t, fe)
	hb, err := home.Bases()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := hb["sess-x"]; !ok {
		t.Fatalf("control: the Stop records the home's base: %v", hb)
	}
	if _, ok := bases(filepath.Join(fe.home, ".buddy", "fleets", "inv", "buddy.db"))["sess-x"]; ok {
		t.Fatal("a Stop beside a home wrote a base row into the fleet; a fleet keeps none (D-191)")
	}
}

// who's LEDGER line beside a home names the fleet the hooks report to only
// when that fleet is there: the tail is what a lane's brief checks
// (outside-git.md), and it was printed for a name that is absent or
// malformed. The control is fleet_test.go's healthy fleet, whose line keeps it.
func TestWhoLedgerLineBesideABrokenFleet(t *testing.T) {
	for _, name := range []string{"nope", "Inv"} {
		t.Run(name, func(t *testing.T) {
			boundedParallel(t)
			fe, _ := bothEnv(t)
			fe.hook(t, "hello", "sess-x", fe.repo)
			out, _, _ := fe.as(t, "sess-x", fe.repo, "status")
			if !strings.Contains(out, "the hooks also report to fleet inv") {
				t.Fatalf("control: a healthy fleet keeps the tail: %q", out)
			}
			fe.env[EnvFleet] = name
			out, _, code := fe.as(t, "sess-x", fe.repo, "status")
			first, _, _ := strings.Cut(out, "\n")
			if code != 0 || strings.Contains(first, "also report to") || !strings.Contains(first, "the hooks report to no fleet") {
				t.Fatalf("a broken fleet's LEDGER line: code %d %q", code, first)
			}
			// The loss is the fleet's: the home's roster and mail stand (the
			// Codex re-verify, round 1, finding 2).
			if strings.Contains(first, "on no roster and gets no mail") || !strings.Contains(first, "on no fleet roster and gets no fleet mail") {
				t.Fatalf("a broken fleet beside a home costs the fleet's roster and mail only: %q", first)
			}
		})
	}
}

// A Codex hello named by --session (an operator's shell naming the session)
// addresses the fleet's count line to THAT session, as the home's is: a
// `buddy fleet inbox` run where the line is read drains the caller's. The
// control is the home's line beside it, which names its --session.
func TestBothLedgersCodexNamedHelloFleetCountLine(t *testing.T) {
	boundedParallel(t)
	fe, orc := bothEnv(t)
	fe.hook(t, "hello", "sess-x", fe.repo)
	for i := 0; i < 25; i++ {
		fe.as(t, "sess-a", fe.repo, "msg", "sess-x", "home "+strings.Repeat("h", 300))
		fe.as(t, "orc-1", orc, "msg", "sess-x", "fleet "+strings.Repeat("f", 300))
	}
	fe.env[EnvCodexThread] = "thr-operator"
	out, errw, code := fe.run(t, fe.repo, "", "hello", "--session", "sess-x")
	delete(fe.env, EnvCodexThread)
	if code != 0 {
		t.Fatal(errw)
	}
	if !strings.Contains(out, "that session runs `buddy inbox --session sess-x`") {
		t.Fatalf("control: the home's count line is addressed to the named session: %q", out)
	}
	if !strings.Contains(out, "fleet inv message(s) not shown here; no tool call delivers them under Codex: that session runs `buddy fleet inbox` as itself") {
		t.Fatalf("the fleet's count line is addressed to the named session: %q", out)
	}
	if len(out) > helloBudget {
		t.Fatalf("hello is %d bytes, over helloBudget", len(out))
	}
}
