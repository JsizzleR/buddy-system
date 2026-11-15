package cli

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// The panel's folds on stage 2 (D-193), each a test that died without its
// fold, beside the control that shows the thing it guards was reachable.

// fleetDB opens the fleet's ledger file directly, past buddy, to break it or
// read what buddy wrote there.
func fleetDB(t *testing.T, fe *fleetEnv) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(fe.home, ".buddy", "fleets", "inv", "buddy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// The gate lens (Fable), P2: what `buddy fleet <verb>` and the fleet's own hook
// lines beside a home print for the session to run next must name `buddy
// fleet`. Bare, from a governed repository, each acted in the HOME ledger: the
// keep-alive's bare check answered NO WAIT while the fleet's wait had landed,
// and the holder ask queued in the home. The bare verbs in the same fixture
// are the controls: they still print bare commands, byte for byte.
func TestFleetVerbFollowUpsNameTheFleet(t *testing.T) {
	boundedParallel(t)
	fe, _ := bothEnv(t)
	lane := fe.mkdirPlain(t, "lane")
	fe.hook(t, "hello", "lane-1", lane)
	for _, s := range []string{"i01", "i02"} {
		if _, errw, code := fe.as(t, "lane-1", lane, "claim", s, "--desc", "d", "--scope", ".buddy/slot/"+s); code != 0 {
			t.Fatal(errw)
		}
	}
	if _, errw, code := fe.as(t, "sess-a", fe.repo, "claim", "h01", "--desc", "d", "--scope", "pkg"); code != 0 {
		t.Fatal(errw)
	}
	fe.hook(t, "hello", "sess-x", fe.repo)

	// The claim's wait hint.
	out, _, code := fe.as(t, "sess-x", fe.repo, "fleet", "claim", "i02", "--desc", "d", "--scope", ".buddy/slot/i02")
	if code == 0 || !strings.Contains(out, "to be told when it frees: buddy fleet wait --on i02") {
		t.Fatalf("fleet claim's refusal must name `buddy fleet wait`: code %d %q", code, out)
	}
	out, _, code = fe.as(t, "sess-x", fe.repo, "fleet", "claim", "i02", "--dry-run", "--desc", "d", "--scope", ".buddy/slot/i02")
	if code == 0 || !strings.Contains(out, "to be told when it frees: buddy fleet wait --on i02") {
		t.Fatalf("fleet claim --dry-run must name `buddy fleet wait`: code %d %q", code, out)
	}
	out, _, code = fe.as(t, "sess-x", fe.repo, "claim", "x01", "--desc", "d", "--scope", "pkg")
	if code == 0 || !strings.Contains(out, "to be told when it frees: buddy wait --on h01") {
		t.Fatalf("control: a bare claim's refusal names the bare wait: code %d %q", code, out)
	}

	// The declaration: the keep-alive and the holder ask.
	out, errw, code := fe.as(t, "sess-x", fe.repo, "fleet", "wait", "--on", "i01")
	if code != 0 {
		t.Fatal(errw)
	}
	for _, want := range []string{"/loop buddy fleet wait check", "buddy fleet msg "} {
		if !strings.Contains(out, want) {
			t.Fatalf("fleet wait must print %q: %q", want, out)
		}
	}
	for _, bare := range []string{"/loop buddy wait check", "buddy msg "} {
		if strings.Contains(out, bare) {
			t.Fatalf("fleet wait printed the bare %q, which acts in the home: %q", bare, out)
		}
	}
	out, errw, code = fe.as(t, "sess-x", fe.repo, "wait", "--on", "h01")
	if code != 0 || !strings.Contains(out, "/loop buddy wait check") || !strings.Contains(out, "buddy msg ") || strings.Contains(out, "buddy fleet") {
		t.Fatalf("control: a bare wait prints the bare commands: code %d %q %q", code, out, errw)
	}

	// The parking refusal's report names the fleet's release.
	if _, errw, code := fe.as(t, "sess-x", fe.repo, "fleet", "release", "i02", "--in-run", "r1"); code == 0 || !strings.Contains(errw, "report with buddy fleet release <item>") {
		t.Fatalf("fleet release --in-run must name `buddy fleet release`: code %d %q", code, errw)
	}
	lane2 := fe.mkdirPlain(t, "lane2")
	fe.hook(t, "hello", "lane-2", lane2)
	if _, errw, code := fe.as(t, "lane-2", lane2, "release", "i02", "--in-run", "r1"); code == 0 || !strings.Contains(errw, "report with release <item>") || strings.Contains(errw, "buddy fleet") {
		t.Fatalf("control: from a plain directory 1b's words stand: code %d %q", code, errw)
	}

	// Beat's LANDED for the fleet's wait names the check that clears it.
	if _, errw, code := fe.as(t, "lane-1", lane, "release", "i01", "--outcome", "pass", "--note", "DONE: x"); code != 0 {
		t.Fatal(errw)
	}
	out, _, _ = fe.hook(t, "beat", "sess-x", fe.repo)
	if ctx := promptContext(t, out); !strings.Contains(ctx, "LANDED") || !strings.Contains(ctx, "`buddy fleet wait check` clears it") {
		t.Fatalf("beat's fleet LANDED must name `buddy fleet wait check`: %q", ctx)
	}
	if out, _, _ := fe.as(t, "sess-x", fe.repo, "fleet", "wait", "check"); !strings.Contains(out, "LANDED") {
		t.Fatalf("the named check reads the fleet's verdict: %q", out)
	}
}

// The concurrency lens, finding 1: a home whose Stop fails before any
// document (its write lock held past busy_timeout) rolled the fleet's Stop
// back with it, so the healthy fleet got no idle mark and a later fleet msg
// named no wake (D-099). The fleet now gets its mark alone. The control, with
// no lock, blocks the Stop with the fleet's mail.
func TestBothLedgersStopWithTheHomeLockedStillMarksTheFleet(t *testing.T) {
	for _, hold := range []bool{false, true} {
		t.Run(map[bool]string{false: "control", true: "home locked"}[hold], func(t *testing.T) {
			boundedParallel(t)
			fe, orc := bothEnv(t)
			fe.hook(t, "hello", "sess-x", fe.repo)
			fe.clock = fe.clock.Add(time.Minute)
			if _, errw, code := fe.as(t, "orc-1", orc, "msg", "sess-x", "fleet ruling"); code != 0 {
				t.Fatal(errw)
			}
			if hold {
				gd, err := exec.Command("git", "-C", fe.repo, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
				if err != nil {
					t.Fatal(err)
				}
				db, err := sql.Open("sqlite", "file:"+filepath.Join(strings.TrimSpace(string(gd)), "buddy.db")+"?_txlock=immediate")
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				tx, err := db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if _, err := tx.Exec(`DELETE FROM session_idle WHERE session_id='nobody'`); err != nil {
					t.Fatal(err)
				}
			}
			out, _, _ := fe.run(t, fe.repo, stopTranscript(t, fe, "sess-x", fe.clock), "idle")
			reason, blocked := stopBlock(t, out)
			var marked int
			if err := fleetDB(t, fe).QueryRow(`SELECT count(*) FROM session_idle WHERE session_id='sess-x'`).Scan(&marked); err != nil {
				t.Fatal(err)
			}
			if !hold {
				if !blocked || !strings.Contains(reason, "fleet ruling") || marked != 0 {
					t.Fatalf("control: the fleet's mail holds the turn, no idle mark: blocked %v marked %d %q", blocked, marked, reason)
				}
				return
			}
			if blocked || marked != 1 {
				t.Fatalf("home locked: no document, and the fleet must still be marked idle: blocked %v marked %d", blocked, marked)
			}
			if inb, _, _ := fe.as(t, "sess-x", fe.repo, "fleet", "inbox"); !strings.Contains(inb, "fleet ruling") {
				t.Fatalf("the fleet's mail must still be queued, not lost: %q", inb)
			}
		})
	}
}

// The concurrency lens, finding 2: a fleet that failed after hello read its
// mail (here, at its claims list) closed its store, and the mark after the
// digest dereferenced it: a panic after the digest went out, the home's mark
// lost with it. Now a failed fleet's mail is not shown. The control is the same
// fixture unbroken, whose digest carries the fleet's mail.
func TestBothLedgersHelloSurvivesAFleetFailingMidDigest(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(map[bool]string{false: "control", true: "claims table gone"}[broken], func(t *testing.T) {
			boundedParallel(t)
			fe, orc := bothEnv(t)
			if _, errw, code := fe.as(t, "orc-1", orc, "claim", "item-1", "--desc", "x", "--scope", ".buddy/slot/item-1"); code != 0 {
				t.Fatal(errw)
			}
			fe.hook(t, "hello", "sess-x", fe.mkdirPlain(t, "lane"))
			if _, errw, code := fe.as(t, "orc-1", orc, "msg", "sess-x", "fleet ruling"); code != 0 {
				t.Fatal(errw)
			}
			if _, errw, code := fe.as(t, "sess-a", fe.repo, "msg", "sess-x", "home note"); code == 0 {
				t.Fatalf("premise: sess-x is not yet on the home roster: %s", errw)
			}
			if broken {
				if _, err := fleetDB(t, fe).Exec(`ALTER TABLE claim_scopes RENAME TO claim_scopes_gone`); err != nil {
					t.Fatal(err)
				}
			}
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("hello panicked: %v\n%s", r, debug.Stack())
				}
			}()
			out, errw, code := fe.hook(t, "hello", "sess-x", fe.repo)
			if code != 0 || !strings.Contains(out, "BUDDY: you are session") {
				t.Fatalf("hello: code %d %q %q", code, out, errw)
			}
			if got := strings.Contains(out, "fleet ruling"); got == broken {
				t.Fatalf("fleet mail shown = %v with the fleet broken = %v: %q", got, broken, out)
			}
		})
	}
}

// The gate lens, P3: a fleet that opened and then failed (its inbox read)
// was silent beside a home at every prompt, while the same failure in a
// fleet-only session was named. busy now names it, and the home's mail still
// rides the same document. The control is the healthy fleet, which adds no
// fleet line.
func TestBothLedgersBusyNamesAFleetFailingAfterItOpened(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(map[bool]string{false: "control", true: "inbox table gone"}[broken], func(t *testing.T) {
			boundedParallel(t)
			fe, orc := bothEnv(t)
			fe.hook(t, "hello", "sess-x", fe.repo)
			if _, errw, code := fe.as(t, "orc-1", orc, "msg", "sess-x", "fleet ruling"); code != 0 {
				t.Fatal(errw)
			}
			if _, errw, code := fe.as(t, "sess-a", fe.repo, "msg", "sess-x", "home note"); code != 0 {
				t.Fatal(errw)
			}
			if broken {
				if _, err := fleetDB(t, fe).Exec(`ALTER TABLE inbox RENAME TO inbox_gone`); err != nil {
					t.Fatal(err)
				}
			}
			out, _, code := fe.hook(t, "busy", "sess-x", fe.repo)
			ctx := promptContext(t, out)
			if code != 0 || !strings.Contains(ctx, "home note") {
				t.Fatalf("busy: the home's mail in every case: code %d %q", code, ctx)
			}
			if got := strings.Contains(ctx, "fleet ledger error ("); got != broken {
				t.Fatalf("fleet error named = %v with the fleet broken = %v: %q", got, broken, ctx)
			}
			if !broken && !strings.Contains(ctx, "fleet ruling") {
				t.Fatalf("control: the healthy fleet's mail rides the document: %q", ctx)
			}
		})
	}
}

// The gate lens, P3, and the docs lens: busy's roster notes beside a home
// say only what is true there. A session the fleet does not know, whose home
// still serves it, is told it gets no FLEET mail, not no mail; the fleet-only
// session the fleet does not know (the control) keeps 1b's words.
func TestBothLedgersRosterNoteBesideAHome(t *testing.T) {
	boundedParallel(t)
	fe, _ := bothEnv(t)
	// sess-a said hello before the fleet existed: on the home's roster only.
	ctx := promptContext(t, first(fe.hook(t, "busy", "sess-a", fe.repo)))
	if !strings.Contains(ctx, "it gets no fleet mail and no fleet peer sees it (this directory's own ledger works as before)") || strings.Contains(ctx, "it gets no mail and no peer sees it") {
		t.Fatalf("beside a home the note must say only the fleet half is lost: %q", ctx)
	}
	lane := fe.mkdirPlain(t, "lane")
	ctx = promptContext(t, first(fe.hook(t, "busy", "ghost-1", lane)))
	if !strings.Contains(ctx, "it gets no mail and no peer sees it") {
		t.Fatalf("control: a fleet-only session the fleet does not know keeps 1b's note: %q", ctx)
	}
	// A session on the fleet's roster only (hello in its lane), at a prompt in
	// the repository: the home's note says its fleet row still moves while
	// the fleet opens, and not once the fleet stops opening.
	fe.hook(t, "hello", "lane-1", lane)
	ctx = promptContext(t, first(fe.hook(t, "busy", "lane-1", fe.repo)))
	if !strings.Contains(ctx, "Its fleet inv row still moves") {
		t.Fatalf("control: the fleet opens, so its row moves: %q", ctx)
	}
	fe.env[EnvFleet] = "nope"
	ctx = promptContext(t, first(fe.hook(t, "busy", "lane-1", fe.repo)))
	if strings.Contains(ctx, "row still moves") || !strings.Contains(ctx, "registers it here") || !strings.Contains(ctx, "names no fleet ledger") {
		t.Fatalf("a fleet that does not open: no moving row, the home's own note and the fleet's line: %q", ctx)
	}
}

func first(s string, _ string, _ int) string { return s }

// The tests lens: the fleet's count line is held back at its LONGEST wording
// (Claude's is 6 bytes longer than Codex's plain one; the named Codex one,
// the cross-check's, longer still), at the longest name a fleet can have, so a 59-to-64-byte name cannot push hello's digest over helloBudget.
func TestFleetCountReserveCoversBothWordings(t *testing.T) {
	name := strings.Repeat("a", 64)
	for _, n := range []int{1, 20, 99999} {
		r := fleetCountReserve(n)
		for _, w := range []struct{ codex, named bool }{{false, false}, {true, false}, {true, true}} {
			if l := len(fleetCountLine(w.codex, w.named, name, n)); l > r {
				t.Fatalf("queued %d, codex %v named %v: the line is %d bytes, the reserve %d", n, w.codex, w.named, l, r)
			}
		}
	}
}

// drainEnv is an Env for calling drainBoth directly: stdout captured, the
// fixture's clock and HOME, BUDDY_FLEET naming fleet inv.
func drainEnv(fe *fleetEnv, out *bytes.Buffer) Env {
	return Env{Stdout: out, Stderr: io.Discard, Cwd: fe.repo, Now: func() time.Time { return fe.clock },
		Getenv: func(k string) string {
			switch k {
			case "HOME":
				return fe.home
			case EnvFleet:
				return "inv"
			}
			return ""
		}}
}

func homeStore(t *testing.T, fe *fleetEnv) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(fe.repo, ".git", "buddy.db"), func() time.Time { return fe.clock })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// The Codex code pass, finding 1: with the fleet not open, drainBoth fell
// back to the home's own bound (20 messages, 8 KiB), which ignores the prefix;
// beside a home the prefix can carry the fleet's sentence with a 512-byte
// path, and the document passed the 10,000-character preview with every
// message marked delivered. The fallback is bounded by the caller's room too:
// the document stays under helloBudget, the oldest message rides it, and the
// rest stay queued for the next drain.
func TestDrainBothHomeOnlyStaysInsideTheRoom(t *testing.T) {
	boundedParallel(t)
	fe, _ := bothEnv(t)
	fe.hook(t, "hello", "sess-x", fe.repo)
	for i := 0; i < 20; i++ {
		if _, errw, code := fe.as(t, "sess-a", fe.repo, "msg", "sess-x", fmt.Sprintf("m%02d %s", i, strings.Repeat("m", 400))); code != 0 {
			t.Fatal(errw)
		}
	}
	home := homeStore(t, fe)
	var out bytes.Buffer
	prefix := strings.Repeat("BUDDY: a notice of buddy's own, standing in for a long fleet line\n", 30)
	room := helloBudget - len(prefix) - len(inboxHeader) - fleetLeadMax
	bound := func(m []store.InboxMsg) []store.InboxMsg { return boundDrain(m, fe.clock) }
	fb := &fleetBeside{err: errors.New("did not open")}
	wrote, err := drainBoth(home, labelOf(home, "sess-x"), fb, "sess-x", drainEnv(fe, &out), "UserPromptSubmit", prefix, bound, room)
	ctx := promptContext(t, out.String())
	if err != nil || !wrote || !strings.Contains(ctx, "m00 ") {
		t.Fatalf("the oldest home message must ride the document: wrote %v err %v %q", wrote, err, ctx)
	}
	if len(ctx) > helloBudget {
		t.Fatalf("the document is %d bytes, over helloBudget %d", len(ctx), helloBudget)
	}
	if q, _ := home.Undelivered("sess-x", labelOf(home, "sess-x")); len(q) == 0 {
		t.Fatal("what did not fit must stay queued for the next drain, not be marked delivered")
	}
}

// The Codex re-verify, round 1, finding 1: the home-only fallback measured
// its room at drainBoth's entry while beat's own bound measured inside the
// lock, a moment later. A CORRECTS line reads "reached you 59s ago" at entry
// and "1m" by then, one byte shorter; with beat's room filled exactly at the
// later time the fallback dropped the correction that today's drain (the
// control: drainHook, the variable unset, the same clock) carries. The clock
// returns entry's time once and the later time after.
func TestDrainBothHomeOnlyMeasuresAtTheDrainsTime(t *testing.T) {
	for _, both := range []bool{false, true} {
		t.Run(map[bool]string{false: "control: drainHook", true: "drainBoth, fleet not open"}[both], func(t *testing.T) {
			boundedParallel(t)
			fe, _ := bothEnv(t)
			fe.hook(t, "hello", "sess-x", fe.repo)
			home := homeStore(t, fe)
			label := labelOf(home, "sess-x")
			if _, errw, code := fe.as(t, "sess-a", fe.repo, "msg", "sess-x", "original"); code != 0 {
				t.Fatal(errw)
			}
			q, err := home.Undelivered("sess-x", label)
			if err != nil || len(q) != 1 {
				t.Fatalf("premise: one queued original: %v %v", q, err)
			}
			orig := q[0].ID
			delivered := fe.clock
			if err := home.Drain("sess-x", label, func(m []store.InboxMsg) ([]int64, error) { return []int64{orig}, nil }); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"msg", "sess-x", "filler"}, {"msg", "sess-x", "--supersedes", fmt.Sprint(orig), "fixed"}} {
				if _, errw, code := fe.as(t, "sess-a", fe.repo, args...); code != 0 {
					t.Fatal(errw)
				}
			}
			q, err = home.Undelivered("sess-x", label)
			if err != nil || len(q) != 2 || q[1].Supersedes != orig {
				t.Fatalf("premise: the filler, then the correction: %v %v", q, err)
			}
			entry, later := delivered.Add(59*time.Second), delivered.Add(61*time.Second)
			fe.clock = later
			lines := func(at time.Time) int { return len(inboxLine(q[0], nil, at)) + len(inboxLine(q[1], nil, at)) }
			if lines(entry) != lines(later)+1 {
				t.Fatalf("premise: the correction's line is a byte longer at entry: %d vs %d", lines(entry), lines(later))
			}
			// beat's prefix, sized so its room holds both lines exactly at
			// the later time and one byte short of them at entry.
			n := helloBudget - len(inboxHeader) - lines(later)
			prefix := strings.Repeat("n", n-1) + "\n"
			calls := 0
			var out bytes.Buffer
			env := drainEnv(fe, &out)
			env.Now = func() time.Time {
				if calls++; calls == 1 {
					return entry
				}
				return later
			}
			bound := func(m []store.InboxMsg) []store.InboxMsg { return beatDrain(m, len(prefix), nowOf(env)) }
			var wrote bool
			if both {
				fb := &fleetBeside{err: errors.New("did not open")}
				wrote, err = drainBoth(home, label, fb, "sess-x", env, "PostToolUse", prefix, bound,
					helloBudget-len(prefix)-len(inboxHeader)-fleetLeadMax)
			} else {
				calls++ // drainHook reads no clock at entry: start it at the later time
				wrote, err = drainHook(home, "sess-x", label, env, "PostToolUse", prefix, bound)
			}
			if err != nil || !wrote {
				t.Fatalf("wrote %v err %v", wrote, err)
			}
			ctx := promptContext(t, out.String())
			if !strings.Contains(ctx, "] filler") || !strings.Contains(ctx, "] fixed") {
				t.Fatalf("both messages fit at the drain's time and must ride the document: %q", ctx)
			}
			if left, _ := home.Undelivered("sess-x", label); len(left) != 0 {
				t.Fatalf("nothing stays queued: %v", left)
			}
		})
	}
}

// The Codex code pass, finding 2: a fleet whose drain could not take its
// write lock (another writer held it past busy_timeout) fell back to the home
// silently, its queued mail unnamed at the prompt. busy now names it beside the
// home's mail. The control, with no lock held, carries the fleet's mail.
func TestDrainBothNamesAFleetWhoseLockIsHeld(t *testing.T) {
	for _, hold := range []bool{false, true} {
		t.Run(map[bool]string{false: "control", true: "fleet locked"}[hold], func(t *testing.T) {
			boundedParallel(t)
			fe, orc := bothEnv(t)
			fe.hook(t, "hello", "sess-x", fe.repo)
			if _, errw, code := fe.as(t, "orc-1", orc, "msg", "sess-x", "fleet ruling"); code != 0 {
				t.Fatal(errw)
			}
			if _, errw, code := fe.as(t, "sess-a", fe.repo, "msg", "sess-x", "home note"); code != 0 {
				t.Fatal(errw)
			}
			home := homeStore(t, fe)
			var out bytes.Buffer
			env := drainEnv(fe, &out)
			fb := besideOf(home, repoContext{}, env)
			if !fb.ok() {
				t.Fatalf("premise: the fleet opens beside the home: %v", fb.err)
			}
			defer fb.close()
			if hold {
				db, err := sql.Open("sqlite", "file:"+filepath.Join(fe.home, ".buddy", "fleets", "inv", "buddy.db")+"?_txlock=immediate")
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				tx, err := db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if _, err := tx.Exec(`DELETE FROM session_idle WHERE session_id='nobody'`); err != nil {
					t.Fatal(err)
				}
			}
			bound := func(m []store.InboxMsg) []store.InboxMsg { return boundDrain(m, fe.clock) }
			if _, err := drainBoth(home, labelOf(home, "sess-x"), fb, "sess-x", env, "UserPromptSubmit", "", bound, helloBudget-len(inboxHeader)-fleetLeadMax); err != nil {
				t.Fatal(err)
			}
			ctx := promptContext(t, out.String())
			if !strings.Contains(ctx, "home note") {
				t.Fatalf("the home's mail in every case: %q", ctx)
			}
			if got := strings.Contains(ctx, "fleet ledger error ("); got != hold {
				t.Fatalf("fleet named = %v with its lock held = %v: %q", got, hold, ctx)
			}
			if !hold && !strings.Contains(ctx, "fleet ruling") {
				t.Fatalf("control: the fleet's mail rides the document: %q", ctx)
			}
		})
	}
}

// The Codex code pass, finding 3: the fleet's claims list, capped at its half
// of the room, counted what it left out with a bare `buddy ls`, which from the
// repository lists the HOME's claims. Its remainder names `buddy fleet ls`; the
// home's, in the same digest (the control), keeps `buddy ls`.
func TestHelloFleetClaimsRemainderNamesTheFleet(t *testing.T) {
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
	out, _, code := fe.hook(t, "hello", "sess-x", fe.repo)
	if code != 0 || len(out) > helloBudget {
		t.Fatalf("hello: code %d, %d bytes", code, len(out))
	}
	fleetPart := out[strings.Index(out, "BUDDY fleet inv live claims"):]
	homePart := out[:strings.Index(out, "BUDDY fleet inv live claims")]
	if !strings.Contains(fleetPart, "`buddy fleet ls` lists every one") {
		t.Fatalf("the fleet's remainder must name `buddy fleet ls`:\n%s", fleetPart)
	}
	if !strings.Contains(homePart, "`buddy ls` lists every one") {
		t.Fatalf("control: the home's remainder keeps `buddy ls`:\n%s", homePart)
	}
}
