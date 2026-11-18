package cli

// The joint budget's reserves (D-193), each pinned by a document sized to land
// in the few bytes that reserve alone guards.
//
// WHY THESE EXIST. The cross-check removed four reserve terms one at a time
// (hello's fleet count line held back from the claims lists and from the
// drain, the fleet's lead and header held back from the Stop's and busy's
// drains) and every test still passed. The budget tests before these queue
// ~400-byte lines, and a drain stops at the first line that does not fit, so
// up to a line of room is left unfilled: that slack absorbed a missing reserve
// of 183 to 501 bytes at the alignments those tests happened to hit. Here the
// lines are sized from what inboxLine renders for the queued rows (read back
// from each ledger), so the drain's total lands exactly where a missing
// reserve shows, or the claims list's slack is held under the line it must
// leave room for.
//
// The fleet is named with 64 bytes, the longest name a fleet can have, so its
// lead line is exactly the length its reserve measures (fleetLeadMax) and its
// count line within 45 bytes of fleetCountReserve. A short name like the other
// tests' "inv" leaves 61 spare bytes a block, which hides part of the window.
//
// Each test asserts its document is at most helloBudget AND a premise that the
// thing the reserve makes room for is in it (both blocks, the count line, a
// list the room cut short), so it cannot pass by printing less; the premise
// passing on the unmutated code is the positive control.

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// budgetFleet is a fleet name at fleetNameRE's 64-byte limit.
var budgetFleet = strings.Repeat("z", 64)

// budgetEnv is bothEnv with the fleet named budgetFleet: the repository
// `buddy init`ed (sess-a "alpha" and sess-b registered), BUDDY_FLEET set, and
// coordinator orc-1 registered in the fleet from a plain directory.
func budgetEnv(t *testing.T) (*fleetEnv, string) {
	t.Helper()
	fe := newFleet(t)
	fe.initAndHello(t)
	fe.initFleet(t, budgetFleet)
	fe.env[EnvFleet] = budgetFleet
	orc := fe.mkdirPlain(t, "orc")
	if _, errw, code := fe.hook(t, "hello", "orc-1", orc); code != 0 {
		t.Fatal(errw)
	}
	return fe, orc
}

// budgetQueued is sess-x's queued mail in each ledger, read past buddy.
func budgetQueued(t *testing.T, fe *fleetEnv) (home, fleet []store.InboxMsg) {
	t.Helper()
	read := func(path string) []store.InboxMsg {
		st, err := store.Open(path, func() time.Time { return fe.clock })
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		q, err := st.Undelivered("sess-x", labelOf(st, "sess-x"))
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	return read(filepath.Join(fe.repo, ".git", "buddy.db")),
		read(filepath.Join(fe.home, ".buddy", "fleets", budgetFleet, "buddy.db"))
}

// lineBytes is what the drains measure for msgs: each rendered line, links in
// their longest form (none here).
func lineBytes(msgs []store.InboxMsg, now time.Time) int {
	n := 0
	for _, m := range msgs {
		n += len(inboxLine(m, nil, now))
	}
	return n
}

// budgetSend queues one --lead message for sess-x (so each block's header is
// the long one the reserves measure), from sess-a in the home or orc-1 in the
// fleet, a second after the last so the merged order is the send order.
func budgetSend(t *testing.T, fe *fleetEnv, orc string, fleet bool, body string) {
	t.Helper()
	fe.clock = fe.clock.Add(time.Second)
	id, cwd := "sess-a", fe.repo
	if fleet {
		id, cwd = "orc-1", orc
	}
	if _, errw, code := fe.as(t, id, cwd, "msg", "sess-x", "--lead", body); code != 0 {
		t.Fatalf("msg from %s: %s", id, errw)
	}
}

// budgetSendSized queues the message that brings sess-x's queued lines, both
// ledgers together, to exactly total bytes, and checks that it did. Its line
// is measured before it is sent from a probe row with the id the ledger will
// give it and the sender's rendered name; the read-back after is the proof.
func budgetSendSized(t *testing.T, fe *fleetEnv, orc string, fleet bool, total int) {
	t.Helper()
	home, fl := budgetQueued(t, fe)
	have := lineBytes(home, fe.clock) + lineBytes(fl, fe.clock)
	same := home
	if fleet {
		same = fl
	}
	if len(same) == 0 {
		t.Fatal("premise: a message from the same sender is queued to measure from")
	}
	last := same[len(same)-1]
	probe := store.InboxMsg{ID: last.ID + 1, From: last.From, Kind: store.KindLead}
	n := total - have - len(inboxLine(probe, nil, fe.clock))
	if n < 1 || n > 4096 {
		t.Fatalf("premise: the sized body is %d bytes (queued %d of %d)", n, have, total)
	}
	budgetSend(t, fe, orc, fleet, strings.Repeat("s", n))
	home, fl = budgetQueued(t, fe)
	if got := lineBytes(home, fe.clock) + lineBytes(fl, fe.clock); got != total {
		t.Fatalf("premise: the queued lines total %d bytes, want exactly %d", got, total)
	}
}

// The fleet's lead and header held back from the Stop's drain (stopBoth's
// room) and from busy's (busyTurn's room to drainBoth). Twenty messages, ten
// a ledger, whose lines total exactly maxDrainBytes: the drain's own caps let
// all of them through, so only the room can stop it.
//
//	Stop: room 9000-165-318-501 = 8,016, so the reason is at most
//	      165 (stopLead) + 318 + 8,016 + 183 (lead) + 318 = 9,000. Without
//	      the 501 the room is 8,517, all 8,192 bytes ride:
//	      165 + 318 + 8,192 + 183 + 318 = 9,176.
//	busy: no note (the session is on both rosters, no handoff declared), so
//	      room 9000-318-501 = 8,181 and the context is at most 9,000. Without
//	      the 501 the room is 8,682: 318 + 8,192 + 183 + 318 = 9,011.
//
// busy's window is the 11 bytes between 8,181 and 8,192, which is why the
// total is exact: a queue a few bytes short of the cap shows no difference.
func TestBothLedgersFullDrainHoldsBackTheFleetBlock(t *testing.T) {
	for _, hook := range []string{"idle", "busy"} {
		t.Run(hook, func(t *testing.T) {
			boundedParallel(t)
			fe, orc := budgetEnv(t)
			if _, errw, code := fe.hook(t, "hello", "sess-x", fe.repo); code != 0 {
				t.Fatal(errw)
			}
			fe.clock = fe.clock.Add(time.Minute)
			for i := 0; i < maxDrainMsgs-1; i++ {
				budgetSend(t, fe, orc, i%2 == 1, fmt.Sprintf("m%02d ", i)+strings.Repeat("q", 376))
			}
			budgetSendSized(t, fe, orc, true, maxDrainBytes)
			var doc string
			if hook == "idle" {
				out, errw, code := fe.run(t, fe.repo, stopTranscript(t, fe, "sess-x", fe.clock), "idle")
				if code != 0 {
					t.Fatalf("idle: %d %s", code, errw)
				}
				reason, blocked := stopBlock(t, out)
				if !blocked || !strings.HasPrefix(reason, stopLead) {
					t.Fatalf("premise: the Stop holds the turn for the mail: %v %q", blocked, out)
				}
				doc = reason
			} else {
				out, errw, code := fe.hook(t, "busy", "sess-x", fe.repo)
				if code != 0 {
					t.Fatalf("busy: %d %s", code, errw)
				}
				doc = promptContext(t, out)
				if !strings.HasPrefix(doc, inboxHeader) {
					t.Fatalf("premise: busy's context is the mail alone, no note ahead of it: %q", doc)
				}
			}
			if strings.Count(doc, inboxHeader) != 2 || !strings.Contains(doc, fleetInboxLead(budgetFleet)) {
				t.Fatalf("premise: both blocks under the long header, the fleet's under its lead (%d bytes):\n%s", len(doc), doc)
			}
			if len(doc) > helloBudget {
				t.Fatalf("%s's document is %d bytes, over helloBudget %d", hook, len(doc), helloBudget)
			}
			// And the window was reached: the room, not a cap, cut the queue
			// short (checked after the budget, so an overrun is named as one).
			if n := strings.Count(doc, "declared LEAD"); n >= maxDrainMsgs {
				t.Fatalf("premise: the room cuts the queue short: %d of %d shown", n, maxDrainMsgs)
			}
		})
	}
}

// The fleet's count line held back from hello's merged drain (the
// fleetCountReserve in mergeDrain's room). The digest ahead of the mail is
// measured first (the same hello with nothing queued; asserted to be the
// prefix of the real one), and the queue is sized so the merged lines reach
// exactly the room that is left WITHOUT that reserve, with one more message
// in each ledger behind them, so both count lines are written:
//
//	R' = 9000 - B - 318 - 128 (helloInboxCountLine) - 501 (fleetLeadMax)
//	without the reserve every line through R' rides, then
//	B + 318 + R' + 183 + 318 + 87 (home count) + 158 (fleet count) = 9,117;
//	with it the room is R' - fleetCountReserve (at least 202), and the
//	digest is at most 9,117 - 202 = 8,915.
func TestBothLedgersHelloDrainHoldsBackTheFleetCountLine(t *testing.T) {
	boundedParallel(t)
	fe, orc := budgetEnv(t)
	if _, errw, code := fe.hook(t, "hello", "sess-x", fe.repo); code != 0 {
		t.Fatal(errw)
	}
	ahead, errw, code := fe.hook(t, "hello", "sess-x", fe.repo)
	if code != 0 || strings.Contains(ahead, "BUDDY MESSAGES") || strings.Contains(ahead, "live claim(s) not shown") {
		t.Fatalf("premise: a digest with nothing queued and no capped list: %d %q %s", code, ahead, errw)
	}
	want := helloBudget - len(ahead) - len(inboxHeader) - helloInboxCountLine - fleetLeadMax
	i := 0
	for ; ; i++ {
		home, fl := budgetQueued(t, fe)
		if want-lineBytes(home, fe.clock)-lineBytes(fl, fe.clock) < 1000 {
			break
		}
		budgetSend(t, fe, orc, i%2 == 1, fmt.Sprintf("m%02d ", i)+strings.Repeat("q", 396))
	}
	budgetSendSized(t, fe, orc, i%2 == 1, want)
	budgetSend(t, fe, orc, false, "home message past the room")
	budgetSend(t, fe, orc, true, "fleet message past the room")
	doc, errw, code := fe.hook(t, "hello", "sess-x", fe.repo)
	if code != 0 || !strings.HasPrefix(doc, ahead) {
		t.Fatalf("premise: the digest ahead of the mail is the one measured: %d %s\n%s", code, errw, doc)
	}
	for _, w := range []string{fleetInboxLead(budgetFleet), "queued message(s) not shown here", "queued fleet " + budgetFleet + " message(s) not shown here"} {
		if !strings.Contains(doc, w) {
			t.Fatalf("premise: the digest carries %q (%d bytes):\n%s", w, len(doc), doc)
		}
	}
	if strings.Count(doc, inboxHeader) != 2 {
		t.Fatalf("premise: both blocks under the long header (%d bytes):\n%s", len(doc), doc)
	}
	if len(doc) > helloBudget {
		t.Fatalf("hello's digest is %d bytes, over helloBudget %d", len(doc), helloBudget)
	}
}

// The fleet's count line held back from hello's claims lists (fleetCountReserve
// in inboxReserve). The home's list is longer than the digest, and only the
// fleet has mail, so the list fills its room, no message fits after it, and
// the fleet's count line is written last. What the list leaves unfilled is
// under one of its 116-byte lines, plus 19 bytes its remainder line's reserve
// (measured with "N of them YOURS" at the whole count) exceeds the line it
// prints: s <= 115 + 19 = 134 at any alignment.
//
//	with the reserve the digest is 9000 - 203 (fleetCountReserve(1)) - s,
//	plus the 158-byte count line: at most 8,955;
//	without it the list takes those 203 bytes: 9000 - s + 158, at least
//	9,024.
//
// Since claimsRoom (D-224) a list's room is at most 3,000 bytes, so the home's
// is cut by the room the rest leaves only when that is less: here waits on
// forty of its claims and forty of the fleet's (restated lines near
// maxHookWaitLine) and a fleet list filling its half take it under 3,000.
// Slot scopes keep every row its own
// (a peer's non-slot claims past two fold into one row that fits).
func TestBothLedgersHelloClaimsHoldBackTheFleetCountLine(t *testing.T) {
	boundedParallel(t)
	fe, orc := budgetEnv(t)
	desc := strings.Repeat("d", 80)
	for i := 0; i < 75; i++ {
		if _, errw, code := fe.as(t, "sess-a", fe.repo, "claim", fmt.Sprintf("h%02d", i), "--desc", desc, "--scope", fmt.Sprintf(".buddy/slot/pkg%02d", i)); code != 0 {
			t.Fatal(errw)
		}
		if _, errw, code := fe.as(t, "orc-1", orc, "claim", fmt.Sprintf("f%02d", i), "--desc", desc, "--scope", fmt.Sprintf(".buddy/slot/f%02d", i)); code != 0 {
			t.Fatal(errw)
		}
	}
	if _, errw, code := fe.hook(t, "hello", "sess-x", fe.repo); code != 0 {
		t.Fatal(errw)
	}
	wait := []string{"wait", "--until", "3h", "--note", strings.Repeat("n", maxWaitNote)}
	for i := 0; i < 40; i++ {
		wait = append(wait, "--on", fmt.Sprintf("h%02d", i))
	}
	if _, errw, code := fe.as(t, "sess-x", fe.repo, wait...); code != 0 {
		t.Fatal(errw)
	}
	// And one in the fleet: the home's wait alone left 2,990 bytes around
	// the lists, 10 short of taking the home's under the cap (measured).
	fwait := []string{"fleet", "wait", "--until", "3h", "--note", strings.Repeat("n", maxWaitNote)}
	for i := 0; i < 40; i++ {
		fwait = append(fwait, "--on", fmt.Sprintf("f%02d", i))
	}
	if _, errw, code := fe.as(t, "sess-x", fe.repo, fwait...); code != 0 {
		t.Fatal(errw)
	}
	budgetSend(t, fe, orc, true, "fleet ruling")
	doc, errw, code := fe.hook(t, "hello", "sess-x", fe.repo)
	if code != 0 {
		t.Fatalf("hello: %d %s", code, errw)
	}
	if !strings.Contains(doc, "more live claim(s) not shown here") || !strings.Contains(doc, "queued fleet "+budgetFleet+" message(s) not shown here") || strings.Contains(doc, "fleet ruling") {
		t.Fatalf("premise: a list its room cut short, then the fleet's count line and no message (%d bytes):\n%s", len(doc), doc)
	}
	home := doc[strings.Index(doc, "BUDDY live claims"):strings.Index(doc, "BUDDY fleet ")]
	if len(home) >= claimsRoom-200 {
		t.Fatalf("premise: the home list's room was under claimsRoom, not the cap (%d bytes):\n%s", len(home), doc)
	}
	if len(doc) > helloBudget {
		t.Fatalf("hello's digest is %d bytes, over helloBudget %d", len(doc), helloBudget)
	}
}
