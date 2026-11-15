package cli

// A session on BOTH ledgers (NON-GIT-PLAN stage 2, D-193): every row the item
// adds (both ledgers present, either missing or unreadable, the joint digest
// and drains, the Stop, bye, the `fleet` selector) as a test beside its
// positive control. Every fleet lives under the fixture's throwaway HOME
// (newFleet), never the developer's.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// bothEnv is a fleet fixture whose repository is `buddy init`ed, with fleet
// "inv" made and BUDDY_FLEET=inv set, and a coordinator orc-1 registered in
// the fleet from a plain directory.
func bothEnv(t *testing.T) (*fleetEnv, string) {
	t.Helper()
	fe := newFleet(t)
	fe.initAndHello(t)
	fe.initFleet(t, "inv")
	fe.env[EnvFleet] = "inv"
	orc := fe.mkdirPlain(t, "orc")
	if _, errw, code := fe.hook(t, "hello", "orc-1", orc); code != 0 {
		t.Fatal(errw)
	}
	return fe, orc
}

// fleetRow is the session's line in the fleet's roster, read with the
// selector from the repository.
func fleetRow(t *testing.T, fe *fleetEnv, id string) string {
	t.Helper()
	out, errw, code := fe.as(t, id, fe.repo, "fleet", "sessions", "--all")
	if code != 0 {
		t.Fatalf("fleet sessions: %d %s", code, errw)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, id) {
			return l
		}
	}
	return ""
}

// homeRow is the session's line in the repository's roster.
func homeRow(t *testing.T, fe *fleetEnv, id string) string {
	t.Helper()
	out, _, _ := fe.as(t, id, fe.repo, "sessions", "--all")
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, id) {
			return l
		}
	}
	return ""
}

// The row "both ledgers present": hello registers in both and says how to act
// in the fleet. The control is the same hello with BUDDY_FLEET unset: the home
// alone, and no fleet line.
func TestBothLedgersHelloRegistersInBoth(t *testing.T) {
	boundedParallel(t)
	fe, _ := bothEnv(t)
	delete(fe.env, EnvFleet)
	out, _, code := fe.hook(t, "hello", "ctl-x", fe.repo)
	if code != 0 || strings.Contains(out, "fleet") {
		t.Fatalf("control: BUDDY_FLEET unset is today's digest: %q", out)
	}
	fe.env[EnvFleet] = "inv"
	if fleetRow(t, fe, "ctl-x") != "" {
		t.Fatal("control: an unset hello must not register in the fleet")
	}
	out, _, code = fe.hook(t, "hello", "sess-x", fe.repo)
	if code != 0 || !strings.Contains(out, "BUDDY: also session ") || !strings.Contains(out, "in fleet inv") || !strings.Contains(out, "`buddy fleet <verb> ...`") {
		t.Fatalf("hello on both ledgers: code %d %q", code, out)
	}
	if strings.Contains(out, "is not used here") {
		t.Fatalf("stage 1b's not-used line survived: %q", out)
	}
	if homeRow(t, fe, "sess-x") == "" || fleetRow(t, fe, "sess-x") == "" {
		t.Fatalf("sess-x must be on both rosters: home %q fleet %q", homeRow(t, fe, "sess-x"), fleetRow(t, fe, "sess-x"))
	}
}

// One document, both inboxes: beat and busy each drain the home's mail and the
// fleet's into their ONE hook document, the fleet's under its own lead line.
// The control: with BUDDY_FLEET unset the same beat drains the home alone and
// the fleet's message stays queued.
func TestBothLedgersDrainBothInboxes(t *testing.T) {
	for _, verb := range []string{"beat", "busy"} {
		t.Run(verb, func(t *testing.T) {
			boundedParallel(t)
			fe, orc := bothEnv(t)
			fe.hook(t, "hello", "sess-x", fe.repo)
			if _, errw, code := fe.as(t, "sess-a", fe.repo, "msg", "sess-x", "home mail"); code != 0 {
				t.Fatal(errw)
			}
			if _, errw, code := fe.as(t, "orc-1", orc, "msg", "sess-x", "fleet mail"); code != 0 {
				t.Fatal(errw)
			}
			delete(fe.env, EnvFleet)
			out, _, _ := fe.hook(t, verb, "sess-x", fe.repo)
			if ctx := promptContext(t, out); !strings.Contains(ctx, "home mail") || strings.Contains(ctx, "fleet mail") {
				t.Fatalf("control: unset drains the home alone: %q", ctx)
			}
			fe.env[EnvFleet] = "inv"
			if _, errw, code := fe.as(t, "sess-a", fe.repo, "msg", "sess-x", "home mail 2"); code != 0 {
				t.Fatal(errw)
			}
			out, _, code := fe.hook(t, verb, "sess-x", fe.repo)
			ctx := promptContext(t, out)
			if code != 0 || !strings.Contains(ctx, "home mail 2") || !strings.Contains(ctx, "fleet mail") {
				t.Fatalf("%s on both ledgers: code %d %q", verb, code, ctx)
			}
			lead := strings.Index(ctx, "the messages below are fleet inv's")
			if lead < 0 || lead > strings.Index(ctx, "fleet mail") || lead < strings.Index(ctx, "home mail 2") {
				t.Fatalf("the fleet's mail must sit under its own lead, after the home's: %q", ctx)
			}
			if out, _, _ := fe.as(t, "orc-1", orc, "sent"); !strings.Contains(out, "1 of 1 with a recorded delivery") {
				t.Fatalf("the fleet message must be marked delivered in the fleet: %q", out)
			}
			// Drained once: the next event carries neither.
			out, _, _ = fe.hook(t, verb, "sess-x", fe.repo)
			if ctx := promptContext(t, out); strings.Contains(ctx, "mail") {
				t.Fatalf("a drained message came again: %q", ctx)
			}
		})
	}
}

// The design pass, finding 3: home mail first and fleet mail in what was left
// kept a large fleet message out for good behind large home messages. Merged
// oldest first, the older fleet message goes in the first drain. The control
// is the same queue with the home's messages older: they go first.
func TestBothLedgersMergeOldestFirst(t *testing.T) {
	for _, fleetOlder := range []bool{true, false} {
		t.Run(fmt.Sprintf("fleetOlder=%v", fleetOlder), func(t *testing.T) {
			boundedParallel(t)
			fe, orc := bothEnv(t)
			fe.hook(t, "hello", "sess-x", fe.repo)
			big := func(c string, n int) string { return strings.Repeat(c, n) }
			sendFleet := func() {
				if _, errw, code := fe.as(t, "orc-1", orc, "msg", "sess-x", "F"+big("f", 4000)); code != 0 {
					t.Fatal(errw)
				}
			}
			sendHome := func() {
				for _, c := range []string{"g", "h"} {
					if _, errw, code := fe.as(t, "sess-a", fe.repo, "msg", "sess-x", "H"+big(c, 3000)); code != 0 {
						t.Fatal(errw)
					}
				}
			}
			if fleetOlder {
				sendFleet()
				fe.clock = fe.clock.Add(time.Minute)
				sendHome()
			} else {
				sendHome()
				fe.clock = fe.clock.Add(time.Minute)
				sendFleet()
			}
			out, _, _ := fe.hook(t, "beat", "sess-x", fe.repo)
			ctx := promptContext(t, out)
			if len(ctx) > helloBudget {
				t.Fatalf("the drain is %d bytes, over helloBudget", len(ctx))
			}
			gotFleet := strings.Contains(ctx, "F"+big("f", 100))
			gotHomeBoth := strings.Contains(ctx, "H"+big("g", 100)) && strings.Contains(ctx, "H"+big("h", 100))
			if fleetOlder && !gotFleet {
				t.Fatalf("the older fleet message must go first: %d bytes, fleet=%v", len(ctx), gotFleet)
			}
			if !fleetOlder && (!gotHomeBoth || gotFleet) {
				t.Fatalf("control: the older home messages go first and the fleet's waits: home=%v fleet=%v", gotHomeBoth, gotFleet)
			}
		})
	}
}

// The rows "either missing or unreadable" beside a home ledger: the home half
// is exactly as it was, hello's digest and busy's every prompt carry the
// fleet's sentence worded for a session the home still serves, and beat, idle
// and bye say nothing and exit 0. The control is the healthy fleet.
func TestBothLedgersFleetFailingBesideAHome(t *testing.T) {
	breaks := []struct {
		name, want string
		brk        func(t *testing.T, fe *fleetEnv)
	}{
		{"malformed", "is not a fleet name", func(t *testing.T, fe *fleetEnv) { fe.env[EnvFleet] = "Inv" }},
		{"absent", "names no fleet ledger on this machine", func(t *testing.T, fe *fleetEnv) { fe.env[EnvFleet] = "nope" }},
		{"unreadable", "fleet ledger unavailable", func(t *testing.T, fe *fleetEnv) {
			p := filepath.Join(fe.home, ".buddy", "fleets", "inv", "buddy.db")
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(p, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, b := range breaks {
		t.Run(b.name, func(t *testing.T) {
			boundedParallel(t)
			fe, _ := bothEnv(t)
			// sess-a said its first hello before the fleet existed; this one
			// registers it in the fleet too, so the control's busy has no
			// roster note of its own to print.
			fe.hook(t, "hello", "sess-a", fe.repo)
			out, _, _ := fe.hook(t, "busy", "sess-a", fe.repo)
			if strings.Contains(promptContext(t, out), "BUDDY_FLEET") || strings.Contains(promptContext(t, out), "fleet ledger") {
				t.Fatalf("control: a healthy fleet adds no line at the prompt: %q", out)
			}
			b.brk(t, fe)
			if _, errw, code := fe.as(t, "sess-b", fe.wtB, "msg", "sess-a", "home mail"); code != 0 {
				t.Fatal(errw)
			}
			out, _, code := fe.hook(t, "hello", "sess-y", fe.repo)
			if code != 0 || !strings.Contains(out, b.want) || !strings.Contains(out, "this directory's own ledger works as before") || !strings.Contains(out, "BUDDY: you are session") {
				t.Fatalf("hello: code %d %q", code, out)
			}
			if homeRow(t, fe, "sess-y") == "" {
				t.Fatal("the home half must register as today")
			}
			out, _, code = fe.hook(t, "busy", "sess-a", fe.repo)
			ctx := promptContext(t, out)
			if code != 0 || !strings.Contains(ctx, b.want) || !strings.Contains(ctx, "home mail") {
				t.Fatalf("busy: the fleet's line and the home's mail in one document: code %d %q", code, ctx)
			}
			for _, v := range []string{"beat", "bye"} {
				if out, errw, code := fe.hook(t, v, "sess-y", fe.repo); code != 0 || out != "" || errw != "" {
					t.Fatalf("%s: silent and exit 0: code %d %q %q", v, code, out, errw)
				}
			}
			if out, errw, code := fe.run(t, fe.repo, stopPayload("sess-a", fe.repo, false, ""), "idle"); code != 0 || out != "" || errw != "" {
				t.Fatalf("idle: silent and exit 0: code %d %q %q", code, out, errw)
			}
			// Silent is not idle-handed: the home half still does each event's
			// work with the fleet broken. bye ended sess-y's home row; beat
			// drains home mail (and says nothing of the fleet); a Stop with
			// nothing queued marks the home row idle, and one with home mail
			// is held for it.
			if row := homeRow(t, fe, "sess-y"); !strings.Contains(row, "ended") {
				t.Fatalf("bye beside a broken fleet must end the home row: %q", row)
			}
			fe.hook(t, "hello", "sess-z", fe.repo)
			if _, errw, code := fe.as(t, "sess-b", fe.wtB, "msg", "sess-z", "home mail z"); code != 0 {
				t.Fatal(errw)
			}
			out, _, code = fe.hook(t, "beat", "sess-z", fe.repo)
			if ctx := promptContext(t, out); code != 0 || !strings.Contains(ctx, "home mail z") || strings.Contains(ctx, b.want) {
				t.Fatalf("beat beside a broken fleet drains the home's mail alone: code %d %q", code, ctx)
			}
			fe.clock = fe.clock.Add(time.Minute)
			if out, errw, code := fe.run(t, fe.repo, stopTranscript(t, fe, "sess-z", fe.clock), "idle"); code != 0 || out != "" {
				t.Fatalf("idle, nothing queued: code %d %q %q", code, out, errw)
			}
			if row := homeRow(t, fe, "sess-z"); !strings.Contains(row, "idle") {
				t.Fatalf("a Stop beside a broken fleet must mark the home row idle: %q", row)
			}
			if _, errw, code := fe.as(t, "sess-b", fe.wtB, "msg", "sess-z", "home ruling z"); code != 0 {
				t.Fatal(errw)
			}
			fe.clock = fe.clock.Add(time.Minute)
			out, _, code = fe.run(t, fe.repo, stopTranscript(t, fe, "sess-z", fe.clock), "idle")
			if reason, blocked := stopBlock(t, out); code != 0 || !blocked || !strings.Contains(reason, "home ruling z") {
				t.Fatalf("a Stop beside a broken fleet is held for home mail: code %d %q", code, out)
			}
		})
	}
}

// The row "home ledger unreadable": today's verdict, and the fleet is not
// opened at all. The control is the healthy home, whose hello registers the
// session in the fleet too.
func TestBothLedgersUnreadableHomeTouchesNoFleet(t *testing.T) {
	boundedParallel(t)
	fe, _ := bothEnv(t)
	fe.hook(t, "hello", "ctl-x", fe.repo)
	if fleetRow(t, fe, "ctl-x") == "" {
		t.Fatal("control: a healthy home's hello registers in the fleet too")
	}
	ledger := filepath.Join(fe.repo, ".git", "buddy.db")
	if err := os.WriteFile(ledger, []byte("not a database, deliberately"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, code := fe.hook(t, "hello", "sess-x", fe.repo); code == 0 {
		t.Fatal("an unreadable home ledger is today's error")
	}
	if fleetRow(t, fe, "sess-x") != "" {
		t.Fatal("the fleet must not be opened when the home cannot be read")
	}
}

// The design pass, Q2: a repository ledger that IS the fleet's file (here a
// symlinked .git/buddy.db) is not opened twice: the fleet is unavailable
// beside it, and a beat with mail does not wait out busy_timeout on itself.
// The control is the same repository with its own ledger.
func TestBothLedgersOneFileIsNotTwoLedgers(t *testing.T) {
	boundedParallel(t)
	fe, orc := bothEnv(t)
	out, _, _ := fe.hook(t, "hello", "ctl-x", fe.repo)
	if !strings.Contains(out, "BUDDY: also session") {
		t.Fatalf("control: %q", out)
	}
	ledger := filepath.Join(fe.repo, ".git", "buddy.db")
	if err := os.Remove(ledger); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"-wal", "-shm"} {
		os.Remove(ledger + s)
	}
	if err := os.Symlink(filepath.Join(fe.home, ".buddy", "fleets", "inv", "buddy.db"), ledger); err != nil {
		t.Fatal(err)
	}
	out, _, code := fe.hook(t, "hello", "sess-x", fe.repo)
	if code != 0 || !strings.Contains(out, "it is the same file as this directory's own ledger") || !strings.Contains(out, "this directory's own ledger works as before") {
		t.Fatalf("hello over an aliased ledger: code %d %q", code, out)
	}
	if _, errw, code := fe.as(t, "orc-1", orc, "msg", "sess-x", "fleet mail"); code != 0 {
		t.Fatal(errw)
	}
	start := time.Now()
	out, _, _ = fe.hook(t, "beat", "sess-x", fe.repo)
	if el := time.Since(start); el > 3*time.Second || !strings.Contains(promptContext(t, out), "fleet mail") {
		t.Fatalf("beat over an aliased ledger: %v, %q", el, out)
	}
}

// bye from a governed repository ends the fleet row too (D-191 left it to this
// stage, L1). The control: a session that said hello with the variable set and
// bye without it keeps its fleet row live.
func TestBothLedgersByeEndsBothRows(t *testing.T) {
	boundedParallel(t)
	fe, _ := bothEnv(t)
	fe.hook(t, "hello", "ctl-x", fe.repo)
	fe.hook(t, "hello", "sess-x", fe.repo)
	delete(fe.env, EnvFleet)
	fe.hook(t, "bye", "ctl-x", fe.repo)
	fe.env[EnvFleet] = "inv"
	if row := fleetRow(t, fe, "ctl-x"); strings.Contains(row, "ended") || row == "" {
		t.Fatalf("control: a bye without the variable leaves the fleet row: %q", row)
	}
	if _, errw, code := fe.hook(t, "bye", "sess-x", fe.repo); code != 0 {
		t.Fatal(errw)
	}
	if row := homeRow(t, fe, "sess-x"); !strings.Contains(row, "ended") {
		t.Fatalf("the home row must end: %q", row)
	}
	if row := fleetRow(t, fe, "sess-x"); !strings.Contains(row, "ended") {
		t.Fatalf("the fleet row must end too: %q", row)
	}
}

// stopTranscript writes a transcript whose last turn ended at `at` and returns
// its Stop payload for id from the repository.
func stopTranscript(t *testing.T, fe *fleetEnv, id string, at time.Time) string {
	t.Helper()
	tr := filepath.Join(t.TempDir(), id+".jsonl")
	writeLines(t, tr, userRecord(at.Add(-time.Second), false), reply(at, 1000))
	b, _ := json.Marshal(map[string]any{"session_id": id, "cwd": fe.repo, "hook_event_name": "Stop", "transcript_path": tr})
	return string(b)
}

// The Stop on two ledgers: mail from either holds the turn in ONE block and
// neither row is marked idle; nothing queued marks both. Each ledger's fence
// is its own (the design pass, finding 2): a fleet row that started after the
// turn is not handed that turn's fleet mail, while the home's still holds it.
// And a ledger that does not know the session cannot stop the other's mail
// (Q7): a fleet lane in a repository it never registered in is held for its
// fleet mail.
func TestBothLedgersStopHoldsForEitherAndFencesEach(t *testing.T) {
	cases := []struct {
		name                string
		home, fleet         bool // mail queued in each
		fleetLate           bool // the fleet row started after the turn
		fleetOnlyRow        bool // the session never said hello in the repository
		block, wantH, wantF bool
	}{
		{"nothing queued", false, false, false, false, false, false, false},
		{"home mail", true, false, false, false, true, true, false},
		{"fleet mail", false, true, false, false, true, false, true},
		{"both", true, true, false, false, true, true, true},
		{"fleet row newer than the turn", true, true, true, false, true, true, false},
		{"unknown to the home", false, true, false, true, true, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			boundedParallel(t)
			fe, orc := bothEnv(t)
			t0 := fe.clock
			switch {
			case tc.fleetOnlyRow:
				fe.hook(t, "hello", "sess-x", fe.mkdirPlain(t, "lane"))
			case tc.fleetLate:
				delete(fe.env, EnvFleet)
				fe.hook(t, "hello", "sess-x", fe.repo)
				fe.env[EnvFleet] = "inv"
				fe.clock = t0.Add(10 * time.Minute)
				fe.hook(t, "hello", "sess-x", fe.mkdirPlain(t, "lane"))
			default:
				fe.hook(t, "hello", "sess-x", fe.repo)
			}
			fe.clock = fe.clock.Add(time.Minute)
			if tc.home {
				if _, errw, code := fe.as(t, "sess-a", fe.repo, "msg", "sess-x", "home ruling"); code != 0 {
					t.Fatal(errw)
				}
			}
			if tc.fleet {
				if _, errw, code := fe.as(t, "orc-1", orc, "msg", "sess-x", "fleet ruling"); code != 0 {
					t.Fatal(errw)
				}
			}
			turn := fe.clock
			if tc.fleetLate {
				turn = t0.Add(5 * time.Minute)
			}
			out, errw, code := fe.run(t, fe.repo, stopTranscript(t, fe, "sess-x", turn), "idle")
			if code != 0 {
				t.Fatalf("idle: %d %s", code, errw)
			}
			reason, blocked := stopBlock(t, out)
			if blocked != tc.block || strings.Contains(reason, "home ruling") != tc.wantH || strings.Contains(reason, "fleet ruling") != tc.wantF {
				t.Fatalf("blocked=%v home=%v fleet=%v, want %v %v %v:\n%s", blocked, strings.Contains(reason, "home ruling"),
					strings.Contains(reason, "fleet ruling"), tc.block, tc.wantH, tc.wantF, out)
			}
			fr := fleetRow(t, fe, "sess-x")
			if tc.block && !tc.fleetLate && strings.Contains(fr, "idle") {
				t.Fatalf("a held turn is not resting in the fleet: %q", fr)
			}
			if !tc.block && !strings.Contains(fr, "idle") {
				t.Fatalf("nothing queued: the fleet row is marked idle: %q", fr)
			}
			if !tc.fleetOnlyRow {
				hr := homeRow(t, fe, "sess-x")
				if tc.block && strings.Contains(hr, "idle") {
					t.Fatalf("a held turn is not resting in the home: %q", hr)
				}
				if !tc.block && !strings.Contains(hr, "idle") {
					t.Fatalf("nothing queued: the home row is marked idle: %q", hr)
				}
			}
			if !tc.block {
				return
			}
			// What held the turn was marked delivered in its own ledger: the
			// next Stop is not held for it again. The fleet mail a late row was
			// not handed is still queued, and holds the next turn alone (the
			// fence defers it; it never loses it).
			fe.clock = fe.clock.Add(time.Minute)
			out, errw, code = fe.run(t, fe.repo, stopTranscript(t, fe, "sess-x", fe.clock), "idle")
			if code != 0 {
				t.Fatalf("second idle: %d %s", code, errw)
			}
			reason, blocked = stopBlock(t, out)
			if strings.Contains(reason, "home ruling") || blocked != tc.fleetLate || strings.Contains(reason, "fleet ruling") != tc.fleetLate {
				t.Fatalf("second Stop: blocked=%v, want %v (only an unhanded fleet message holds it):\n%s", blocked, tc.fleetLate, out)
			}
		})
	}
}

// A wait declared in the fleet lands on the session's next tool call from the
// repository (beat's fleet half), and its footprint and browser call reach the
// fleet's roster. The control is the same beat with the variable unset.
func TestBothLedgersBeatCarriesTheFleetsLanded(t *testing.T) {
	boundedParallel(t)
	fe, orc := bothEnv(t)
	lane := fe.mkdirPlain(t, "lane")
	fe.hook(t, "hello", "lane-1", lane)
	if _, errw, code := fe.as(t, "lane-1", lane, "claim", "i01", "--desc", "d", "--scope", ".buddy/slot/i01"); code != 0 {
		t.Fatal(errw)
	}
	_ = orc
	fe.hook(t, "hello", "sess-x", fe.repo)
	if _, errw, code := fe.as(t, "sess-x", fe.repo, "fleet", "wait", "--on", "i01"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := fe.as(t, "lane-1", lane, "release", "i01", "--outcome", "pass", "--note", "DONE: x"); code != 0 {
		t.Fatal(errw)
	}
	delete(fe.env, EnvFleet)
	out, _, _ := fe.hook(t, "beat", "sess-x", fe.repo)
	if strings.Contains(out, "LANDED") {
		t.Fatalf("control: unset, the fleet's wait is not read: %q", out)
	}
	fe.env[EnvFleet] = "inv"
	out, _, _ = fe.run(t, fe.repo, hookJSON("sess-x", fe.repo, "mcp__claude-in-chrome__navigate", ""), "beat")
	if !strings.Contains(promptContext(t, out), "LANDED") {
		t.Fatalf("beat on both ledgers must carry the fleet's LANDED: %q", out)
	}
	if who, _, _ := fe.as(t, "sess-x", fe.repo, "fleet", "who", "sess-x"); !strings.Contains(who, "browser") {
		t.Fatalf("the browser call must reach the fleet's EXIT line: %q", who)
	}
	// Once per declaration: the LANDED line is marked in the fleet after the
	// write, so the next tool call does not carry it again.
	if out, _, _ := fe.hook(t, "beat", "sess-x", fe.repo); strings.Contains(out, "LANDED") {
		t.Fatalf("the fleet's LANDED came twice: %q", out)
	}
}

// One joint budget (D-034's 9,000 bytes): long claims lists and full inboxes
// in BOTH ledgers still make a digest under helloBudget that shows each
// ledger's claims header and counts what it could not show.
func TestBothLedgersDigestStaysUnderBudget(t *testing.T) {
	boundedParallel(t)
	fe, orc := bothEnv(t)
	desc := strings.Repeat("d", 300)
	for i := 0; i < 40; i++ {
		if _, errw, code := fe.as(t, "sess-a", fe.repo, "claim", fmt.Sprintf("h%02d", i), "--desc", desc, "--scope", fmt.Sprintf("pkg%02d", i)); code != 0 {
			t.Fatal(errw)
		}
		if _, errw, code := fe.as(t, "orc-1", orc, "claim", fmt.Sprintf("f%02d", i), "--desc", desc, "--scope", fmt.Sprintf(".buddy/slot/f%02d", i)); code != 0 {
			t.Fatal(errw)
		}
	}
	fe.hook(t, "hello", "sess-x", fe.repo)
	for i := 0; i < 25; i++ {
		fe.as(t, "sess-a", fe.repo, "msg", "sess-x", "home "+strings.Repeat("m", 400))
		fe.as(t, "orc-1", orc, "msg", "sess-x", "fleet "+strings.Repeat("n", 400))
	}
	// A new incarnation of the same session: hello's drain is the digest's.
	fe.hook(t, "bye", "sess-x", fe.repo)
	out, _, code := fe.hook(t, "hello", "sess-x", fe.repo)
	if code != 0 || len(out) > helloBudget {
		t.Fatalf("digest of %d bytes (budget %d)", len(out), helloBudget)
	}
	// Each list keeps rows of its own: the fleet's takes at most half the
	// room, so a long fleet list cannot push every home row out.
	for _, want := range []string{"BUDDY live claims", "BUDDY fleet inv live claims", "queued fleet inv message(s) not shown here", "queued message(s) not shown here", "scopes: pkg", "scopes: .buddy/slot/f"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the joint digest lacks %q (%d bytes):\n%s", want, len(out), out)
		}
	}
}

// Which ledger a bare verb means (the item's question): the directory's. A
// bare msg from a governed repository to a name only the fleet knows is
// refused as a miss, never rerouted; `buddy fleet msg` reaches it. The
// selector refuses with the variable unset, a verb it does not run, a flag in
// the verb's place, and no verb at all, and answers each sub-verb's --help.
func TestFleetVerbActsInTheFleetOnly(t *testing.T) {
	boundedParallel(t)
	fe, orc := bothEnv(t)
	fe.hook(t, "hello", "sess-x", fe.repo)
	if _, _, code := fe.as(t, "sess-x", fe.repo, "msg", "orc-1", "bare"); code == 0 {
		t.Fatal("a bare msg from the repository to a fleet-only name must be refused as a miss")
	}
	if _, errw, code := fe.as(t, "sess-x", fe.repo, "fleet", "msg", "orc-1", "selected"); code != 0 {
		t.Fatalf("fleet msg: %s", errw)
	}
	if out, _, _ := fe.as(t, "orc-1", orc, "inbox"); !strings.Contains(out, "selected") || strings.Contains(out, "bare") {
		t.Fatalf("the fleet's inbox: %q", out)
	}
	if _, errw, code := fe.as(t, "sess-x", fe.repo, "fleet", "claim", "item", "--desc", "d", "--scope", ".buddy/slot/item"); code != 0 {
		t.Fatalf("a slot claim through the selector: %s", errw)
	}
	if out, _, _ := fe.as(t, "sess-x", fe.repo, "ls"); strings.Contains(out, "item") {
		t.Fatalf("the selector's claim must not be in the home ledger: %q", out)
	}
	if out, _, _ := fe.as(t, "sess-x", fe.repo, "fleet", "ls"); !strings.Contains(out, "item") {
		t.Fatalf("fleet ls: %q", out)
	}
	if _, errw, code := fe.as(t, "sess-x", fe.repo, "fleet", "claim", "notes", "--desc", "d", "--scope", "notes.md"); code == 0 || !strings.Contains(errw, "refused") {
		t.Fatalf("a file claim through the selector is the fleet's refusal: %d %q", code, errw)
	}
	if out, _, code := fe.run(t, fe.repo, "", "fleet", "msg", "--help"); code != 0 || out != usageMsg+"\n" {
		t.Fatalf("fleet msg --help: %d %q", code, out)
	}
	for _, r := range []struct {
		args []string
		why  string
	}{
		{[]string{"fleet"}, "no verb given"},
		{[]string{"fleet", "--bogus"}, "unknown flag --bogus"},
		{[]string{"fleet", "gate"}, "gate is not a verb fleet runs"},
		{[]string{"fleet", "hello"}, "hello is not a verb fleet runs"},
		{[]string{"fleet", "init"}, "init is not a verb fleet runs"},
		{[]string{"fleet", "fleet"}, "fleet is not a verb fleet runs"},
		{[]string{"fleet", "commit-gate"}, "commit-gate is not a verb fleet runs"},
	} {
		if _, errw, code := fe.run(t, fe.repo, "", r.args...); code == 0 || !strings.Contains(errw, "refused:") || !strings.Contains(errw, r.why) {
			t.Fatalf("%v must be refused (%s): %d %q", r.args, r.why, code, errw)
		}
	}
	delete(fe.env, EnvFleet)
	if _, errw, code := fe.as(t, "sess-x", fe.repo, "fleet", "ls"); code == 0 || !strings.Contains(errw, "is not set") {
		t.Fatalf("the selector with BUDDY_FLEET unset: %d %q", code, errw)
	}
	// A verb answers --help before it runs (D-046), the variable unset too:
	// usage needs no fleet.
	if out, errw, code := fe.run(t, fe.repo, "", "fleet", "msg", "--help"); code != 0 || out != usageMsg+"\n" {
		t.Fatalf("fleet msg --help with BUDDY_FLEET unset: %d %q %q", code, out, errw)
	}
	// Later in the line too, as the bare verb's parser answers `claim x
	// --help` (the cross-check: refused here).
	if out, errw, code := fe.run(t, fe.repo, "", "fleet", "claim", "x", "--help"); code != 0 || out != usageClaim+"\n" {
		t.Fatalf("fleet claim x --help with BUDDY_FLEET unset: %d %q %q", code, out, errw)
	}
}

// The design pass, finding 1: with $HOME a governed worktree, the plan's hand
// path (cd into the fleet's directory) finds THAT repository's ledger, and a
// bare `release` acts there (the control, showing the hazard). The selector
// releases the fleet's claim of the same name and leaves the home's open.
func TestFleetVerbCannotReachAGovernedHome(t *testing.T) {
	boundedParallel(t)
	fe := newFleet(t)
	git := exec.Command("git", "init", "-q", fe.home)
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("git init home: %v %s", err, out)
	}
	if _, errw, code := fe.run(t, fe.home, "", "init"); code != 0 {
		t.Fatal(errw)
	}
	fe.initFleet(t, "inv")
	fe.env[EnvFleet] = "inv"
	fdir := filepath.Join(fe.home, ".buddy", "fleets", "inv")
	fe.hook(t, "hello", "sess-x", fe.home)
	if _, errw, code := fe.as(t, "sess-x", fe.home, "claim", "c", "--desc", "home", "--scope", "notes.md"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := fe.as(t, "sess-x", fe.home, "fleet", "claim", "c", "--desc", "fleet", "--scope", ".buddy/slot/c"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := fe.as(t, "sess-x", fdir, "fleet", "release", "c"); code != 0 {
		t.Fatal(errw)
	}
	if out, _, _ := fe.as(t, "sess-x", fe.home, "ls"); !strings.Contains(out, "notes.md") {
		t.Fatalf("the selector released the HOME's claim: %q", out)
	}
	if out, _, _ := fe.as(t, "sess-x", fe.home, "fleet", "ls"); strings.Contains(out, ".buddy/slot/c") {
		t.Fatalf("the selector must release the fleet's claim: %q", out)
	}
	// The control: the hand path from the fleet's directory acts in the home.
	if _, errw, code := fe.as(t, "sess-x", fdir, "release", "c"); code != 0 {
		t.Fatal(errw)
	}
	if out, _, _ := fe.as(t, "sess-x", fe.home, "ls"); strings.Contains(out, "notes.md") {
		t.Fatalf("control: a bare release from the fleet's directory reaches the governed home: %q", out)
	}
}

// fleetVerbList, the usage's list, is exactly the set the selector runs.
func TestFleetVerbListIsTheSet(t *testing.T) {
	if got := strings.Join(sortedFleetVerbs(), " "); got != fleetVerbList {
		t.Fatalf("fleetVerbList %q, fleetVerbs %q", fleetVerbList, got)
	}
}

// mergeDrain's order and bounds, unit by unit: oldest first with a tie going
// to the home, the room spent line by line and stopping at the first message
// that does not fit (never skipping ahead), and boundDrain's 20 messages and
// 8 KiB over the merged order. Every row that leaves a message out has a twin
// one byte roomier, or one message shorter, that takes it, so a bound cannot
// pass by admitting nothing.
func TestMergeDrainOrderAndBounds(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	t0 := now.Add(-time.Hour)
	msg := func(id int64, at time.Time, n int) store.InboxMsg {
		return store.InboxMsg{ID: id, From: "peer", Body: strings.Repeat("b", n), Created: at}
	}
	ln := func(m store.InboxMsg) int { return len(inboxLine(m, nil, now)) }
	sec := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Second) }
	tieH, tieF := msg(1, sec(0), 100), msg(2, sec(0), 100)
	oldF, newH := msg(2, sec(0), 100), msg(1, sec(1), 100)
	oldH, newF := msg(1, sec(0), 100), msg(2, sec(1), 100)
	bigOld, smallNew := msg(1, sec(0), 1000), msg(2, sec(1), 10)
	// Two lines that sum to exactly maxDrainBytes, and a third after them.
	exA := msg(1, sec(0), 4090)
	exB := msg(2, sec(1), maxDrainBytes-ln(exA)-ln(msg(2, sec(1), 0)))
	if n := len(exB.Body); n <= 0 || n > 4096 || ln(exA)+ln(exB) != maxDrainBytes {
		t.Fatalf("premise: the two lines sum to %d, not %d (body %d)", ln(exA)+ln(exB), maxDrainBytes, n)
	}
	exC := msg(3, sec(2), 10)
	var many24H, many24F []store.InboxMsg
	for i := 0; i < 12; i++ {
		many24H = append(many24H, msg(int64(100+i), sec(2*i), 10))
		many24F = append(many24F, msg(int64(200+i), sec(2*i+1), 10))
	}
	ids := func(ms []store.InboxMsg) []int64 {
		out := []int64{}
		for _, m := range ms {
			out = append(out, m.ID)
		}
		return out
	}
	huge := 1 << 20
	cases := []struct {
		name         string
		home, fleet  []store.InboxMsg
		room         int
		wantH, wantF []int64
	}{
		{"a tie goes to the home", []store.InboxMsg{tieH}, []store.InboxMsg{tieF}, ln(tieH), []int64{1}, []int64{}},
		{"a tie, room for both", []store.InboxMsg{tieH}, []store.InboxMsg{tieF}, ln(tieH) + ln(tieF), []int64{1}, []int64{2}},
		{"the older fleet message goes first", []store.InboxMsg{newH}, []store.InboxMsg{oldF}, ln(oldF), []int64{}, []int64{2}},
		{"the older home message goes first", []store.InboxMsg{oldH}, []store.InboxMsg{newF}, ln(oldH), []int64{1}, []int64{}},
		{"room exactly fits both", []store.InboxMsg{oldH}, []store.InboxMsg{newF}, ln(oldH) + ln(newF), []int64{1}, []int64{2}},
		{"one byte short of both", []store.InboxMsg{oldH}, []store.InboxMsg{newF}, ln(oldH) + ln(newF) - 1, []int64{1}, []int64{}},
		{"never skips ahead to a smaller newer one", []store.InboxMsg{bigOld}, []store.InboxMsg{smallNew}, ln(bigOld) - 1, []int64{}, []int64{}},
		{"exactly 8 KiB is inside the bound", []store.InboxMsg{exA, exC}, []store.InboxMsg{exB}, huge, []int64{1}, []int64{2}},
		{"20 messages across both, never 21", many24H, many24F, huge, ids(many24H[:10]), ids(many24F[:10])},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sh, sf := mergeDrain(tc.home, tc.fleet, tc.room, now)
			if fmt.Sprint(ids(sh)) != fmt.Sprint(tc.wantH) || fmt.Sprint(ids(sf)) != fmt.Sprint(tc.wantF) {
				t.Fatalf("home %v fleet %v, want %v %v", ids(sh), ids(sf), tc.wantH, tc.wantF)
			}
		})
	}
}

// bothPayload is a hook payload for sess-x from the repository carrying a
// transcript, shaped for verb.
func bothPayload(fe *fleetEnv, verb, id, tr string) string {
	in := map[string]any{"session_id": id, "cwd": fe.repo, "transcript_path": tr}
	switch verb {
	case "beat":
		in["tool_name"], in["tool_input"] = "Read", map[string]any{}
	case "busy":
		in["hook_event_name"], in["prompt"] = "UserPromptSubmit", "next"
	case "idle":
		in["hook_event_name"] = "Stop"
	}
	b, _ := json.Marshal(in)
	return string(b)
}

// The footprint reaches the fleet's roster from every hook that samples it
// (beat, busy, the Stop), each against the fleet's own incarnation: the
// coordinator picks lanes by it there. The control is the same hook with the
// variable unset: the home's row gets the number and the fleet's does not.
func TestBothLedgersFootprintReachesTheFleet(t *testing.T) {
	for name, verb := range map[string]string{"beat": "beat", "busy": "busy", "stop": "idle"} {
		t.Run(name, func(t *testing.T) {
			boundedParallel(t)
			fe, _ := bothEnv(t)
			fe.hook(t, "hello", "sess-x", fe.repo)
			fe.clock = fe.clock.Add(time.Minute)
			tr := filepath.Join(t.TempDir(), "sess-x.jsonl")
			writeLines(t, tr, userRecord(fe.clock.Add(-time.Second), false), reply(fe.clock, 1000))
			if strings.Contains(fleetRow(t, fe, "sess-x"), "prompt") || strings.Contains(homeRow(t, fe, "sess-x"), "prompt") {
				t.Fatal("premise: no footprint before the hook")
			}
			delete(fe.env, EnvFleet)
			if _, errw, code := fe.run(t, fe.repo, bothPayload(fe, verb, "sess-x", tr), verb); code != 0 {
				t.Fatal(errw)
			}
			fe.env[EnvFleet] = "inv"
			if !strings.Contains(homeRow(t, fe, "sess-x"), "prompt") || strings.Contains(fleetRow(t, fe, "sess-x"), "prompt") {
				t.Fatalf("control: unset, the home's row only: home %q fleet %q", homeRow(t, fe, "sess-x"), fleetRow(t, fe, "sess-x"))
			}
			if _, errw, code := fe.run(t, fe.repo, bothPayload(fe, verb, "sess-x", tr), verb); code != 0 {
				t.Fatal(errw)
			}
			if row := fleetRow(t, fe, "sess-x"); !strings.Contains(row, "prompt") {
				t.Fatalf("%s on both ledgers must record the footprint in the fleet: %q", verb, row)
			}
		})
	}
}

// A browser call's PreToolUse (beatOffSuccess) is recorded in the fleet too,
// silently, so the coordinator reading the lane's EXIT line there sees it
// before closing the pane. The control is the same event with the variable
// unset: the home records it, the fleet does not.
func TestBothLedgersPreToolUseBrowserReachesTheFleet(t *testing.T) {
	boundedParallel(t)
	fe, _ := bothEnv(t)
	fe.hook(t, "hello", "sess-x", fe.repo)
	who := func(args ...string) string {
		out, errw, code := fe.as(t, "sess-x", fe.repo, args...)
		if code != 0 {
			t.Fatalf("%v: %d %s", args, code, errw)
		}
		return out
	}
	pre := eventJSON(t, "PreToolUse", "sess-x", fe.repo, "mcp__claude-in-chrome__navigate")
	delete(fe.env, EnvFleet)
	if out, errw, code := fe.run(t, fe.repo, pre, "beat"); code != 0 || out != "" || errw != "" {
		t.Fatalf("PreToolUse is silent: %d %q %q", code, out, errw)
	}
	fe.env[EnvFleet] = "inv"
	if !strings.Contains(who("who", "sess-x"), "browser") || strings.Contains(who("fleet", "who", "sess-x"), "browser") {
		t.Fatalf("control: unset, the home's EXIT line only:\n%s\n%s", who("who", "sess-x"), who("fleet", "who", "sess-x"))
	}
	if out, errw, code := fe.run(t, fe.repo, pre, "beat"); code != 0 || out != "" || errw != "" {
		t.Fatalf("PreToolUse on both ledgers is silent: %d %q %q", code, out, errw)
	}
	if w := who("fleet", "who", "sess-x"); !strings.Contains(w, "browser") {
		t.Fatalf("the PreToolUse browser call must reach the fleet's EXIT line: %q", w)
	}
}

// A label two live fleet sessions wear is refused as ambiguous by every
// `buddy fleet msg` addressed to it, so hello says so, naming the fleet, the
// moment it becomes true. The control is a label nobody else wears there; and
// the home's own twin line stays silent, the home having no twin.
func TestBothLedgersHelloWarnsOfAFleetTwinLabel(t *testing.T) {
	boundedParallel(t)
	fe, _ := bothEnv(t)
	lane := fe.mkdirPlain(t, "lane")
	if _, errw, code := fe.run(t, lane, hookJSON("lane-1", lane, "", ""), "hello", "--label", "dup"); code != 0 {
		t.Fatal(errw)
	}
	out, _, code := fe.run(t, fe.repo, hookJSON("ctl-x", fe.repo, "", ""), "hello", "--label", "solo")
	if code != 0 || strings.Contains(out, "also worn by") {
		t.Fatalf("control: a label nobody else wears: %d %q", code, out)
	}
	out, _, code = fe.run(t, fe.repo, hookJSON("sess-x", fe.repo, "", ""), "hello", "--label", "dup")
	if code != 0 || !strings.Contains(out, "also worn by 1 other live session(s) in fleet inv") {
		t.Fatalf("hello must warn of the fleet's twin: %d %q", code, out)
	}
	if strings.Contains(out, "other live session(s); a pause") {
		t.Fatalf("the home has no twin, so its own line must not print: %q", out)
	}
}

// A wait the session declared in the fleet is restated by hello under the
// line naming whose it is (besideWaitLines): read bare, "you are WAITING"
// would be taken for the home's. The control is the same hello before the
// wait is declared.
func TestBothLedgersHelloRestatesAFleetWait(t *testing.T) {
	boundedParallel(t)
	fe, _ := bothEnv(t)
	lane := fe.mkdirPlain(t, "lane")
	fe.hook(t, "hello", "lane-1", lane)
	if _, errw, code := fe.as(t, "lane-1", lane, "claim", "i01", "--desc", "d", "--scope", ".buddy/slot/i01"); code != 0 {
		t.Fatal(errw)
	}
	fe.hook(t, "hello", "sess-x", fe.repo)
	lead := "BUDDY: in fleet inv (check or clear it with `buddy fleet wait ...`):\n"
	if out, _, _ := fe.hook(t, "hello", "sess-x", fe.repo); strings.Contains(out, lead) || strings.Contains(out, "WAITING") {
		t.Fatalf("control: no wait declared yet: %q", out)
	}
	if _, errw, code := fe.as(t, "sess-x", fe.repo, "fleet", "wait", "--on", "i01"); code != 0 {
		t.Fatal(errw)
	}
	out, _, _ := fe.hook(t, "hello", "sess-x", fe.repo)
	i := strings.Index(out, lead)
	if i < 0 || !strings.HasPrefix(out[i+len(lead):], "BUDDY: you are WAITING on claim \"i01\"") {
		t.Fatalf("hello must restate the fleet's wait under its lead: %q", out)
	}
}

// `buddy fleet <verb>` never looked at the directory's ledger, so its failure
// on an unwritable fleet ledger names the FLEET's, even from a repository
// whose own ledger is there and writable (ledgeraccess's fleetOnly arm). The
// control is the same verb against the writable fleet.
func TestFleetVerbLedgerAccessNamesTheFleetFromARepository(t *testing.T) {
	boundedParallel(t)
	fe, _ := bothEnv(t)
	fe.hook(t, "hello", "sess-x", fe.repo)
	ledger := filepath.Join(fe.home, ".buddy", "fleets", "inv", "buddy.db")
	if _, errw, code := fe.as(t, "sess-x", fe.repo, "fleet", "status"); code != 0 || strings.Contains(errw, ledgerCause) {
		t.Fatalf("control on a writable fleet ledger: %d %q", code, errw)
	}
	dir := filepath.Dir(ledger)
	if err := os.Chmod(ledger, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(ledger, 0o644) })
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if fh, err := os.OpenFile(ledger, os.O_RDWR, 0); err == nil {
		fh.Close()
		t.Skip("file modes do not refuse this process a write")
	}
	_, errw, code := fe.as(t, "sess-x", fe.repo, "fleet", "status")
	if code != 1 {
		t.Fatalf("an unwritable fleet ledger must fail the verb: code %d %q", code, errw)
	}
	for _, w := range []string{ledgerCause, ledger, "It lives in the fleet home under $HOME"} {
		if !strings.Contains(errw, w) {
			t.Errorf("stderr lacks %q:\n%s", w, errw)
		}
	}
	if strings.Contains(errw, "git common directory") {
		t.Errorf("`buddy fleet` never opened the repository's ledger, so its explanation must not name it:\n%s", errw)
	}
}

// A session the opened fleet does not know (its hello ran before the fleet
// existed) is told so at every prompt from the repository, with the line that
// registers it. The control is the same busy after it registers.
func TestBothLedgersBusySaysTheFleetLacksTheSession(t *testing.T) {
	boundedParallel(t)
	fe, _ := bothEnv(t) // sess-a said hello before the fleet existed
	out, _, _ := fe.hook(t, "busy", "sess-a", fe.repo)
	if ctx := promptContext(t, out); !strings.Contains(ctx, "this session is not on fleet inv's roster") {
		t.Fatalf("busy must say the fleet lacks the session: %q", ctx)
	}
	fe.hook(t, "hello", "sess-a", fe.repo)
	if out, _, _ := fe.hook(t, "busy", "sess-a", fe.repo); strings.Contains(out, "roster") {
		t.Fatalf("control: once registered, nothing: %q", out)
	}
}

// hello's digest drains both inboxes and marks the fleet's messages delivered
// in the fleet, so they do not come again on the next tool call. The control
// is the home's message beside it, marked as it always was.
func TestBothLedgersHelloDeliversAndMarksFleetMail(t *testing.T) {
	boundedParallel(t)
	fe, orc := bothEnv(t)
	fe.hook(t, "hello", "sess-x", fe.repo)
	if _, errw, code := fe.as(t, "sess-a", fe.repo, "msg", "sess-x", "home note"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := fe.as(t, "orc-1", orc, "msg", "sess-x", "fleet note"); code != 0 {
		t.Fatal(errw)
	}
	out, _, code := fe.hook(t, "hello", "sess-x", fe.repo)
	lead := strings.Index(out, "the messages below are fleet inv's")
	if code != 0 || !strings.Contains(out, "home note") || lead < 0 || lead > strings.Index(out, "fleet note") {
		t.Fatalf("hello must carry both inboxes, the fleet's under its lead: %d %q", code, out)
	}
	if s, _, _ := fe.as(t, "sess-a", fe.repo, "sent"); !strings.Contains(s, "1 of 1 with a recorded delivery") {
		t.Fatalf("control: the home message is marked: %q", s)
	}
	if s, _, _ := fe.as(t, "orc-1", orc, "sent"); !strings.Contains(s, "1 of 1 with a recorded delivery") {
		t.Fatalf("the fleet message must be marked delivered in the fleet: %q", s)
	}
	if out, _, _ := fe.hook(t, "beat", "sess-x", fe.repo); strings.Contains(out, "note") {
		t.Fatalf("a message hello delivered came again: %q", out)
	}
}

// busy retracts the idle mark in both ledgers, as the Stop made it in both.
// The positive control is the Stop's own marks, read before the busy.
func TestBothLedgersBusyRetractsBothIdleMarks(t *testing.T) {
	boundedParallel(t)
	fe, _ := bothEnv(t)
	fe.hook(t, "hello", "sess-x", fe.repo)
	fe.clock = fe.clock.Add(time.Minute)
	if _, errw, code := fe.run(t, fe.repo, stopTranscript(t, fe, "sess-x", fe.clock), "idle"); code != 0 {
		t.Fatal(errw)
	}
	if !strings.Contains(homeRow(t, fe, "sess-x"), "idle") || !strings.Contains(fleetRow(t, fe, "sess-x"), "idle") {
		t.Fatalf("control: the Stop marks both: home %q fleet %q", homeRow(t, fe, "sess-x"), fleetRow(t, fe, "sess-x"))
	}
	fe.hook(t, "busy", "sess-x", fe.repo)
	if h, f := homeRow(t, fe, "sess-x"), fleetRow(t, fe, "sess-x"); strings.Contains(h, "idle") || strings.Contains(f, "idle") {
		t.Fatalf("busy must retract both marks: home %q fleet %q", h, f)
	}
}

// beat heartbeats the fleet row too: a session working from the repository
// does not go STALE on its fleet's roster. The control is the same beat with
// the variable unset, which leaves the fleet row stale. Each roster is read as
// a peer (sess-b, orc-1): a typed verb run as sess-x would move its own row.
func TestBothLedgersBeatMovesTheFleetRow(t *testing.T) {
	boundedParallel(t)
	fe, orc := bothEnv(t)
	fe.hook(t, "hello", "sess-x", fe.repo)
	row := func(as, cwd string, args ...string) string {
		out, errw, code := fe.as(t, as, cwd, args...)
		if code != 0 {
			t.Fatalf("%v: %d %s", args, code, errw)
		}
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, "(sess-x)") {
				return l
			}
		}
		t.Fatalf("no sess-x row in:\n%s", out)
		return ""
	}
	home := func() string { return row("sess-b", fe.wtB, "sessions", "--all") }
	fleet := func() string { return row("orc-1", orc, "sessions", "--all") }
	fe.clock = fe.clock.Add(2 * time.Hour)
	delete(fe.env, EnvFleet)
	fe.hook(t, "beat", "sess-x", fe.repo)
	fe.env[EnvFleet] = "inv"
	if h, f := home(), fleet(); strings.Contains(h, "STALE") || !strings.Contains(f, "STALE") {
		t.Fatalf("control: unset, the home's row alone moves: home %q fleet %q", h, f)
	}
	fe.hook(t, "beat", "sess-x", fe.repo)
	if f := fleet(); strings.Contains(f, "STALE") {
		t.Fatalf("beat on both ledgers must move the fleet row: %q", f)
	}
}

// armHomeTrigger makes every insert into one of the home ledger's tables
// fail, and returns its removal: a failure INSIDE the home's transaction, at
// the point the failure protocol names.
func armHomeTrigger(t *testing.T, fe *fleetEnv, table string) func() {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(fe.repo, ".git", "buddy.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER mut_abort BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(ABORT, 'armed by the test'); END`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		defer db.Close()
		if _, err := db.Exec(`DROP TRIGGER mut_abort`); err != nil {
			t.Fatal(err)
		}
	}
}

// The failure protocol's row "the document went out and the home's commit
// failed": the home's error is the hook's (its mail comes again, at least
// once, as today), and the fleet still marks what was written. For beat's
// drain and for the Stop. The positive control that the failure was armed is
// the home message coming again once the trigger is gone.
func TestBothLedgersHomeMarkFailsAfterTheWrite(t *testing.T) {
	for name, verb := range map[string]string{"beat": "beat", "stop": "idle"} {
		t.Run(name, func(t *testing.T) {
			boundedParallel(t)
			fe, orc := bothEnv(t)
			fe.hook(t, "hello", "sess-x", fe.repo)
			fe.clock = fe.clock.Add(time.Minute)
			if _, errw, code := fe.as(t, "sess-a", fe.repo, "msg", "sess-x", "home ruling"); code != 0 {
				t.Fatal(errw)
			}
			if _, errw, code := fe.as(t, "orc-1", orc, "msg", "sess-x", "fleet ruling"); code != 0 {
				t.Fatal(errw)
			}
			disarm := armHomeTrigger(t, fe, "inbox_delivery")
			var out string
			var code int
			if verb == "beat" {
				out, _, code = fe.hook(t, "beat", "sess-x", fe.repo)
				out = promptContext(t, out)
			} else {
				out, _, code = fe.run(t, fe.repo, stopTranscript(t, fe, "sess-x", fe.clock), "idle")
				out, _ = stopBlock(t, out)
			}
			disarm()
			if code == 0 {
				t.Fatalf("the home's failed mark is the hook's error, as today: %q", out)
			}
			if !strings.Contains(out, "home ruling") || !strings.Contains(out, "fleet ruling") {
				t.Fatalf("the document went out with both: %q", out)
			}
			if s, _, _ := fe.as(t, "orc-1", orc, "sent"); !strings.Contains(s, "1 of 1 with a recorded delivery") {
				t.Fatalf("the fleet must still mark what was written: %q", s)
			}
			next, _, _ := fe.hook(t, "beat", "sess-x", fe.repo)
			if ctx := promptContext(t, next); !strings.Contains(ctx, "home ruling") || strings.Contains(ctx, "fleet ruling") {
				t.Fatalf("the home's comes again (the trigger fired), the fleet's does not: %q", ctx)
			}
		})
	}
}

// The failure protocol's row "the home failed before the write" for the Stop,
// here its idle insert failing inside its own transaction: the home's error
// is the hook's and the home is not marked; the fleet, whose transaction
// rolled back with it, still gets its idle mark through its own fence, so a
// later fleet msg names a wake (D-099; the panel's concurrency lens, finding
// 1, reached by a held home lock in fleetboth_fold_test.go, here by an
// in-transaction failure that costs no busy_timeout). The positive control is
// the same Stop with the failure gone: it marks the home too.
func TestBothLedgersHomeIdleFailureStillMarksTheFleet(t *testing.T) {
	boundedParallel(t)
	fe, _ := bothEnv(t)
	fe.hook(t, "hello", "sess-x", fe.repo)
	fe.clock = fe.clock.Add(time.Minute)
	disarm := armHomeTrigger(t, fe, "session_idle")
	_, _, code := fe.run(t, fe.repo, stopTranscript(t, fe, "sess-x", fe.clock), "idle")
	disarm()
	if code == 0 {
		t.Fatal("the home's failed idle mark is the hook's error")
	}
	if h, f := homeRow(t, fe, "sess-x"), fleetRow(t, fe, "sess-x"); strings.Contains(h, "idle") || !strings.Contains(f, "idle") {
		t.Fatalf("the home that failed is not marked; the healthy fleet is: home %q fleet %q", h, f)
	}
	fe.clock = fe.clock.Add(time.Minute)
	if _, errw, code := fe.run(t, fe.repo, stopTranscript(t, fe, "sess-x", fe.clock), "idle"); code != 0 {
		t.Fatal(errw)
	}
	if h := homeRow(t, fe, "sess-x"); !strings.Contains(h, "idle") {
		t.Fatalf("control: the same Stop unfailed marks the home: %q", h)
	}
}

// A write that fails costs nothing in either ledger: a beat carrying only
// fleet mail reports the failure (never a second document on the event, never
// a silent success) and the mail comes again; a Stop that cannot write its
// block holds nothing, so the session IS resting in both ledgers with its mail
// queued, as a one-ledger Stop records it. The positive control is the next
// working drain, which carries what the failed write did not deliver.
func TestBothLedgersFailedWriteMarksNothing(t *testing.T) {
	// Subtests are named for the event, never "idle": t.TempDir carries the
	// name, the roster prints the worktree path, and the rows are read for
	// the word idle.
	for name, verb := range map[string]string{"beat": "beat", "stop": "idle"} {
		t.Run(name, func(t *testing.T) {
			boundedParallel(t)
			fe, orc := bothEnv(t)
			fe.hook(t, "hello", "sess-x", fe.repo)
			fe.clock = fe.clock.Add(time.Minute)
			if verb == "idle" {
				if _, errw, code := fe.as(t, "sess-a", fe.repo, "msg", "sess-x", "home ruling"); code != 0 {
					t.Fatal(errw)
				}
			}
			if _, errw, code := fe.as(t, "orc-1", orc, "msg", "sess-x", "fleet ruling"); code != 0 {
				t.Fatal(errw)
			}
			in := hookJSON("sess-x", fe.repo, "Bash", "")
			if verb == "idle" {
				in = stopTranscript(t, fe, "sess-x", fe.clock)
			}
			var errw strings.Builder
			code := Run([]string{verb}, Env{
				Stdin:  strings.NewReader(in),
				Stdout: &failWriter{n: 0},
				Stderr: &errw,
				Cwd:    fe.repo,
				Now:    func() time.Time { return fe.clock },
				Getenv: func(k string) string { return fe.env[k] },
			})
			if code == 0 {
				t.Fatalf("a %s whose document could not be written must report it", verb)
			}
			if verb == "idle" {
				if h, f := homeRow(t, fe, "sess-x"), fleetRow(t, fe, "sess-x"); !strings.Contains(h, "idle") || !strings.Contains(f, "idle") {
					t.Fatalf("an unwritten hold leaves the session resting in both: home %q fleet %q", h, f)
				}
			}
			out, _, _ := fe.hook(t, "beat", "sess-x", fe.repo)
			ctx := promptContext(t, out)
			if !strings.Contains(ctx, "fleet ruling") || (verb == "idle" && !strings.Contains(ctx, "home ruling")) {
				t.Fatalf("nothing was marked, so the next drain delivers it: %q", ctx)
			}
		})
	}
}

// A fleet whose Stop transaction fails before its body (here its sessions
// table is gone, so its own fence cannot be read) is the row "the fleet's
// transaction never began": today's Stop on the home, exactly, which marks
// the home idle and exits 0. The positive control is the home row, not idle
// before the Stop.
func TestBothLedgersStopWhenTheFleetFailsBeforeItsBody(t *testing.T) {
	boundedParallel(t)
	fe, _ := bothEnv(t)
	fe.hook(t, "hello", "sess-x", fe.repo)
	fe.clock = fe.clock.Add(time.Minute)
	if h := homeRow(t, fe, "sess-x"); strings.Contains(h, "idle") {
		t.Fatalf("premise: not idle before the Stop: %q", h)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(fe.home, ".buddy", "fleets", "inv", "buddy.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE sessions RENAME TO sessions_gone`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if out, errw, code := fe.run(t, fe.repo, stopTranscript(t, fe, "sess-x", fe.clock), "idle"); code != 0 || out != "" {
		t.Fatalf("idle: today's Stop on the home: %d %q %q", code, out, errw)
	}
	if h := homeRow(t, fe, "sess-x"); !strings.Contains(h, "idle") {
		t.Fatalf("the home's Stop must still mark it idle: %q", h)
	}
}

// holdWriteLock takes path's write lock on a connection of its own and
// returns its release.
func holdWriteLock(t *testing.T, path string) func() {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	return sync.OnceFunc(func() {
		conn.ExecContext(ctx, "ROLLBACK")
		conn.Close()
		db.Close()
	})
}

// lockFree reports whether path's write lock can be taken at once (and gives
// it back).
func lockFree(t *testing.T, path string) bool {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return false
	}
	conn.ExecContext(ctx, "ROLLBACK")
	return true
}

// LOCK ORDER (drainBoth's and stopBoth's comment): the fleet is the OUTER
// lock, so a contended fleet stalls nothing of the repository's: while the
// joint drain or Stop waits out the fleet's busy_timeout it holds no home
// lock. And when the fleet's transaction never begins, the hook is today's on
// the home: its mail goes out alone and the fleet's stays queued. The probe's
// positive control: it reads a held home lock as held.
func TestBothLedgersAContendedFleetNeverHoldsTheHome(t *testing.T) {
	for _, hook := range []string{"drain", "stop"} {
		t.Run(hook, func(t *testing.T) {
			boundedParallel(t)
			fe, orc := bothEnv(t)
			fe.hook(t, "hello", "sess-x", fe.repo)
			fe.clock = fe.clock.Add(time.Minute)
			if _, errw, code := fe.as(t, "sess-a", fe.repo, "msg", "sess-x", "home ruling"); code != 0 {
				t.Fatal(errw)
			}
			if _, errw, code := fe.as(t, "orc-1", orc, "msg", "sess-x", "fleet ruling"); code != 0 {
				t.Fatal(errw)
			}
			homePath := filepath.Join(fe.repo, ".git", "buddy.db")
			fleetPath := filepath.Join(fe.home, ".buddy", "fleets", "inv", "buddy.db")
			var out bytes.Buffer
			env := Env{Stdout: &out, Stderr: io.Discard, Cwd: fe.repo, Now: func() time.Time { return fe.clock },
				Getenv: func(k string) string { return fe.env[k] }}
			home, _, err := openRepo(fe.repo, env)
			if err != nil {
				t.Fatal(err)
			}
			defer home.Close()
			fst, frc, err := openFleetLedger(env)
			if err != nil {
				t.Fatal(err)
			}
			fb := &fleetBeside{st: fst, rc: frc}
			defer fb.close()
			held := holdWriteLock(t, homePath)
			if lockFree(t, homePath) {
				t.Fatal("control: the probe must read a held lock as held")
			}
			held()
			if !lockFree(t, homePath) {
				t.Fatal("control: the probe must read a released lock as free")
			}
			release := holdWriteLock(t, fleetPath)
			defer release()
			done := make(chan error, 1)
			var wrote bool
			go func() {
				if hook == "drain" {
					w, err := drainBoth(home, labelOf(home, "sess-x"), fb, "sess-x", env, "PostToolUse", "",
						func(m []store.InboxMsg) []store.InboxMsg { return boundDrain(m, fe.clock) }, helloBudget-len(inboxHeader)-fleetLeadMax)
					wrote = w
					done <- err
					return
				}
				done <- stopBoth(home, fb, hookInput{SessionID: "sess-x", Cwd: fe.repo}, env, fe.clock, fe.clock)
			}()
			// The fleet's busy_timeout is 5 s: for the first 4 the hook waits
			// on the fleet, and must hold nothing of the home's meanwhile.
			for deadline := time.Now().Add(4 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
				select {
				case err := <-done:
					t.Fatalf("returned (%v) before the fleet's busy_timeout: the fleet was not contended", err)
				default:
				}
				if !lockFree(t, homePath) {
					t.Fatal("the hook held the HOME's write lock while it waited on the fleet's")
				}
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			doc := out.String()
			if hook == "drain" {
				doc = promptContext(t, doc)
				if !wrote {
					t.Fatal("the home's own drain must still write")
				}
			} else {
				doc, _ = stopBlock(t, doc)
			}
			if !strings.Contains(doc, "home ruling") || strings.Contains(doc, "fleet ruling") {
				t.Fatalf("the fleet's transaction never began: the home's mail alone: %q", doc)
			}
		})
	}
}

// An empty fleet inbox takes no fleet lock and leaves the home's drain
// today's, byte for byte (drainBoth's plain-read shortcut): the same queue and
// the same notice drained with the variable unset in a twin fixture is the
// control. The notice (a LANDED line with a long note) and a long queue make
// the room bind, where a joint drain's room would differ from today's.
func TestBothLedgersEmptyFleetInboxIsTodaysDrain(t *testing.T) {
	boundedParallel(t)
	build := func(t *testing.T) *fleetEnv {
		fe, _ := bothEnv(t)
		fe.hook(t, "hello", "sess-x", fe.repo)
		if _, errw, code := fe.as(t, "sess-b", fe.wtB, "claim", "w1", "--desc", "d", "--scope", "w1.go"); code != 0 {
			t.Fatal(errw)
		}
		if _, errw, code := fe.as(t, "sess-x", fe.repo, "wait", "--on", "w1", "--note", strings.Repeat("n", 400)); code != 0 {
			t.Fatal(errw)
		}
		if _, errw, code := fe.as(t, "sess-b", fe.wtB, "release", "w1"); code != 0 {
			t.Fatal(errw)
		}
		for i := 0; i < 21; i++ {
			if _, errw, code := fe.as(t, "sess-a", fe.repo, "msg", "sess-x", fmt.Sprintf("m%02d ", i)+strings.Repeat("h", 400)); code != 0 {
				t.Fatal(errw)
			}
		}
		return fe
	}
	ctl := build(t)
	delete(ctl.env, EnvFleet)
	want, _, _ := ctl.hook(t, "beat", "sess-x", ctl.repo)
	if ctx := promptContext(t, want); !strings.Contains(ctx, "LANDED") || strings.Count(ctx, strings.Repeat("h", 400)) >= maxDrainMsgs {
		t.Fatalf("premise: a LANDED notice and a queue the room cuts short: %q", ctx)
	}
	fe := build(t)
	got, _, _ := fe.hook(t, "beat", "sess-x", fe.repo)
	if got != want {
		t.Fatalf("an empty fleet inbox must leave the drain today's:\n got %d bytes %q\nwant %d bytes %q", len(got), got, len(want), want)
	}
}

// One joint budget for the mail too (D-034's 9,000 bytes): with few claims
// and long queues in BOTH ledgers, the digest's merged drain holds back the
// fleet's lead, header and count line, and beat's holds back the fleet's lead
// and header under long notices (two LANDED lines). Each stays under
// helloBudget and says what it could not show. Every message declares a kind
// (--lead), so each block's header is the long one the reserves measure (a
// plain header leaves ~235 spare bytes a block and hides an overrun), and its
// lines are ~400 bytes: the room binds before the 20-message cap, and a
// missing reserve (the fleet's lead and header, ~450 bytes) overruns the
// budget at any alignment, since what is left unfilled is under one line.
func TestBothLedgersMailStaysUnderBudget(t *testing.T) {
	for _, verb := range []string{"hello", "beat"} {
		t.Run(verb, func(t *testing.T) {
			boundedParallel(t)
			fe, orc := bothEnv(t)
			fe.hook(t, "hello", "sess-x", fe.repo)
			if verb == "beat" {
				lane := fe.mkdirPlain(t, "lane")
				fe.hook(t, "hello", "lane-1", lane)
				steps := [][]string{
					{"sess-b", fe.wtB, "claim", "w1", "--desc", "d", "--scope", "w1.go"},
					{"lane-1", lane, "claim", "i01", "--desc", "d", "--scope", ".buddy/slot/i01"},
					{"sess-x", fe.repo, "wait", "--on", "w1", "--note", strings.Repeat("n", 500)},
					{"sess-x", fe.repo, "fleet", "wait", "--on", "i01", "--note", strings.Repeat("o", 500)},
					{"sess-b", fe.wtB, "release", "w1"},
					{"lane-1", lane, "release", "i01", "--outcome", "pass", "--note", "DONE: x"},
				}
				for _, s := range steps {
					if _, errw, code := fe.as(t, s[0], s[1], s[2:]...); code != 0 {
						t.Fatalf("%v: %s", s, errw)
					}
				}
			}
			// A second apart, so the merged order alternates between them.
			for i := 0; i < 21; i++ {
				fe.clock = fe.clock.Add(time.Second)
				if _, errw, code := fe.as(t, "sess-a", fe.repo, "msg", "sess-x", "--lead", fmt.Sprintf("h%02d ", i)+strings.Repeat("h", 360)); code != 0 {
					t.Fatal(errw)
				}
				if _, errw, code := fe.as(t, "orc-1", orc, "msg", "sess-x", "--lead", fmt.Sprintf("f%02d ", i)+strings.Repeat("f", 360)); code != 0 {
					t.Fatal(errw)
				}
			}
			var doc string
			if verb == "hello" {
				doc, _, _ = fe.hook(t, "hello", "sess-x", fe.repo)
				for _, w := range []string{"queued fleet inv message(s) not shown here", "queued message(s) not shown here", "the messages below are fleet inv's"} {
					if !strings.Contains(doc, w) {
						t.Fatalf("the digest lacks %q (%d bytes):\n%s", w, len(doc), doc)
					}
				}
			} else {
				out, _, _ := fe.hook(t, "beat", "sess-x", fe.repo)
				doc = promptContext(t, out)
				if strings.Count(doc, "LANDED") != 2 || !strings.Contains(doc, "the messages below are fleet inv's") {
					t.Fatalf("premise: both LANDED lines and both inboxes (%d bytes):\n%s", len(doc), doc)
				}
			}
			if len(doc) > helloBudget {
				t.Fatalf("%s's document is %d bytes, over helloBudget %d", verb, len(doc), helloBudget)
			}
		})
	}
}

// A Stop whose hook is already active (Claude Code re-running Stop after a
// block) is never held again, on two ledgers as on one: StopHold reads no
// mail for it in either, so a queue in each cannot loop the turn. The control
// is the same queue at an ordinary Stop, held for both, which also proves the
// mail was still queued after the active one.
func TestBothLedgersStopHookActiveHoldsNothing(t *testing.T) {
	boundedParallel(t)
	fe, orc := bothEnv(t)
	fe.hook(t, "hello", "sess-x", fe.repo)
	fe.clock = fe.clock.Add(time.Minute)
	if _, errw, code := fe.as(t, "sess-a", fe.repo, "msg", "sess-x", "home ruling"); code != 0 {
		t.Fatal(errw)
	}
	if _, errw, code := fe.as(t, "orc-1", orc, "msg", "sess-x", "fleet ruling"); code != 0 {
		t.Fatal(errw)
	}
	stop := func(active bool) string {
		tr := filepath.Join(t.TempDir(), "turn.jsonl")
		writeLines(t, tr, userRecord(fe.clock.Add(-time.Second), false), reply(fe.clock, 1000))
		b, _ := json.Marshal(map[string]any{"session_id": "sess-x", "cwd": fe.repo, "hook_event_name": "Stop",
			"transcript_path": tr, "stop_hook_active": active})
		out, errw, code := fe.run(t, fe.repo, string(b), "idle")
		if code != 0 {
			t.Fatalf("idle (active=%v): %d %s", active, code, errw)
		}
		return out
	}
	if out := stop(true); out != "" {
		t.Fatalf("an active Stop hook must not be held again: %q", out)
	}
	fe.clock = fe.clock.Add(time.Minute)
	reason, blocked := stopBlock(t, stop(false))
	if !blocked || !strings.Contains(reason, "home ruling") || !strings.Contains(reason, "fleet ruling") {
		t.Fatalf("control: an ordinary Stop is held for both ledgers' mail: %v %q", blocked, reason)
	}
}
