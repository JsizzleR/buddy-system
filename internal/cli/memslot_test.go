package cli

// The memory slot end to end (memslot.go, D-238): the gate's verdicts, the
// release, the fail mode, the override, `buddy mem admit`'s place in line,
// and concurrent judges. Every reading is a fake (fixture.mem): a test judged
// by this machine's real memory would pass or fail with the developer's other
// sessions. Each refusal sits beside its positive control, the same call
// allowed once the one thing the refusal is about changes.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// memSlotFixture is a fixture with a HOME, a fake memory reading of free GB
// on a 32 GB machine, and the slot initialized at the default 4 GB floor.
func memSlotFixture(t *testing.T, free float64) (*fixture, *memReading) {
	t.Helper()
	f := newFixture(t)
	f.env["HOME"] = t.TempDir()
	m := &memReading{TotalGB: 32, Level: int(free / 32 * 100), Pressure: 1}
	f.mem = func() (memReading, error) { return *m, nil }
	if out, errw, code := f.run(t, f.repo, "", "mem", "init"); code != 0 {
		t.Fatalf("mem init: %s %s", out, errw)
	}
	return f, m
}

func memDir(f *fixture) string { return filepath.Join(f.env["HOME"], memDirRel) }

func memRows(t *testing.T, f *fixture) []memRow {
	t.Helper()
	b, err := readMemBook(memDir(f))
	if err != nil {
		t.Fatal(err)
	}
	return b.Rows
}

func bashHook(session, cwd, cmd, id string, extra map[string]any) string {
	in := map[string]any{"command": cmd}
	for k, v := range extra {
		in[k] = v
	}
	m := map[string]any{"session_id": session, "cwd": cwd, "tool_name": "Bash", "tool_input": in, "tool_use_id": id}
	b, _ := json.Marshal(m)
	return string(b)
}

func postHook(event, session, cwd, cmd, id string, extra map[string]any) string {
	var m map[string]any
	json.Unmarshal([]byte(bashHook(session, cwd, cmd, id, extra)), &m)
	m["hook_event_name"] = event
	b, _ := json.Marshal(m)
	return string(b)
}

func gateVerdict(t *testing.T, f *fixture, cwd, stdin string) (string, bool) {
	t.Helper()
	out, errw, code := f.run(t, cwd, stdin, "gate")
	if code != 0 || errw != "" {
		t.Fatalf("gate exits 0 and writes nothing to stderr: code %d, %q", code, errw)
	}
	return decodeDeny(t, out)
}

func TestMemGateAdmitsRefusesAndReleases(t *testing.T) {
	boundedParallel(t)
	f, m := memSlotFixture(t, 20)
	dir := t.TempDir() // a plain directory: no repository, no ledger

	// 20 free, floor 4: a 6 GB test fits, and is booked.
	if reason, denied := gateVerdict(t, f, dir, bashHook("s1", dir, "go test ./...", "t1", nil)); denied {
		t.Fatalf("a start that fits is allowed: %s", reason)
	}
	rows := memRows(t, f)
	if len(rows) != 1 || rows[0].GB != 6 || rows[0].Owner != "s1" || rows[0].Call != "t1" || rows[0].Detached {
		t.Fatalf("one foreground row of 6 GB for s1/t1: %+v", rows)
	}
	// A second fits (20-6-6=8), a third does not (20-12-6=2 < 4).
	if _, denied := gateVerdict(t, f, dir, bashHook("s2", dir, "go test ./...", "t2", nil)); denied {
		t.Fatal("the second 6 GB start leaves 8 GB: allowed")
	}
	reason, denied := gateVerdict(t, f, dir, bashHook("s3", dir, "go test ./...", "t3", nil))
	if !denied {
		t.Fatal("the third 6 GB start would leave 2 GB: refused")
	}
	for _, want := range []string{"MEMORY GATE: nothing in this call ran", "buddy mem admit goall --wait 900 && { <the same command>; }", "run_in_background", "under the 4.0 GB floor", "buddy mem status"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the refusal says %q: %s", want, reason)
		}
	}
	if hasBreak(reason) {
		t.Errorf("the refusal is one line: %q", reason)
	}
	if n := len(memRows(t, f)); n != 2 {
		t.Fatalf("a refused call books nothing: %d rows", n)
	}
	// A light call is allowed whatever is booked.
	if _, denied := gateVerdict(t, f, dir, bashHook("s3", dir, "git status", "t4", nil)); denied {
		t.Fatal("a light call is never refused by the slot")
	}
	// PostToolUse releases s1's call; then s3's start fits.
	if _, errw, code := f.run(t, dir, postHook("PostToolUse", "s1", dir, "go test ./...", "t1", nil), "beat"); code != 0 {
		t.Fatalf("beat: %s", errw)
	}
	if _, denied := gateVerdict(t, f, dir, bashHook("s3", dir, "go test ./...", "t3", nil)); denied {
		t.Fatal("after s1's release, s3's start fits")
	}
	// PostToolUseFailure releases too (s2's failed run).
	f.run(t, dir, postHook("PostToolUseFailure", "s2", dir, "go test ./...", "t2", nil), "beat")
	for _, r := range memRows(t, f) {
		if r.Owner == "s2" {
			t.Fatalf("a failed call's row is released: %+v", r)
		}
	}
	// Pressure refuses even with room; the control above had pressure 1.
	m.Pressure = 2
	if reason, denied := gateVerdict(t, f, dir, bashHook("s4", dir, "go test ./x", "t5", nil)); !denied || !strings.Contains(reason, "pressure is warn") {
		t.Fatalf("kernel pressure 2 refuses: %v %s", denied, reason)
	}
	m.Pressure = 1
	m.SwapGB, m.Level = 9, 29
	if reason, denied := gateVerdict(t, f, dir, bashHook("s4", dir, "codex exec x", "t5", nil)); !denied || !strings.Contains(reason, "swap in use") {
		t.Fatalf("swap over 8 GB with under 30%% free refuses: %v %s", denied, reason)
	}
	m.Level = 31 // swap alone is sticky, never a refusal by itself
	if reason, denied := gateVerdict(t, f, dir, bashHook("s4", dir, "codex exec x", "t5", nil)); denied && strings.Contains(reason, "swap") {
		t.Fatalf("swap with 31%% free does not refuse: %s", reason)
	}
}

func TestMemGateDetachedAndBackgroundedFade(t *testing.T) {
	boundedParallel(t)
	f, _ := memSlotFixture(t, 30)
	dir := t.TempDir()
	for _, tc := range []struct {
		cmd      string
		extra    map[string]any
		wantKept float64
	}{
		{"go test ./x", nil, 0}, // the control: released
		{"go test ./x", map[string]any{"run_in_background": true}, 3},   // the tool backgrounded it
		{"go test ./x", map[string]any{"run_in_background": "true"}, 0}, // a string is not the bool: released, and no DENY
		{"go test ./... > /tmp/l 2>&1 &", nil, 6},                       // the shell backgrounded it
		{"herdr agent start a", nil, 1},                                 // a lane outlives its start
		{"go test ./x; herdr agent start a", nil, 1},                    // only the foreground part goes
	} {
		id := fmt.Sprintf("c-%q-%v", tc.cmd, tc.extra)
		if reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, tc.cmd, id, tc.extra)); denied {
			t.Fatalf("%q fits: %s", tc.cmd, reason)
		}
		f.run(t, dir, postHook("PostToolUse", "s", dir, tc.cmd, id, tc.extra), "beat")
		kept := 0.0
		for _, r := range memRows(t, f) {
			if strings.HasPrefix(r.Call, id) {
				kept += r.GB
			}
		}
		if kept != tc.wantKept {
			t.Errorf("%q %v: %v GB kept after PostToolUse, want %v", tc.cmd, tc.extra, kept, tc.wantKept)
		}
	}
}

// Rows are per call: a session's next call never erases its running one, and
// subagents (the parent's session id, their own agent_id) stack.
func TestMemGateRowsArePerCall(t *testing.T) {
	boundedParallel(t)
	f, _ := memSlotFixture(t, 30)
	dir := t.TempDir()
	gateVerdict(t, f, dir, bashHook("s", dir, "go test -race ./...", "a", map[string]any{"run_in_background": true}))
	gateVerdict(t, f, dir, bashHook("s", dir, "go build ./cmd/buddy", "b", nil))
	for _, agent := range []string{"ag1", "ag2"} {
		in := map[string]any{"session_id": "s", "agent_id": agent, "cwd": dir, "tool_name": "Bash",
			"tool_input": map[string]any{"command": "go test ./x"}, "tool_use_id": "c"}
		b, _ := json.Marshal(in)
		gateVerdict(t, f, dir, string(b))
	}
	total := 0.0
	for _, r := range memRows(t, f) {
		total += r.GB
	}
	if total != 6+3+3+3 {
		t.Fatalf("every call stays booked: %v GB in %+v", total, memRows(t, f))
	}
	// The same call judged twice (a re-sent PreToolUse) is one row.
	gateVerdict(t, f, dir, bashHook("s", dir, "go build ./cmd/buddy", "b", nil))
	if n := len(memRows(t, f)); n != 4 {
		t.Fatalf("a call judged again replaces itself: %d rows", n)
	}
}

func TestMemGateFailMode(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()

	// OFF: no config. A heavy start under any reading passes, and no file appears.
	off := newFixture(t)
	off.env["HOME"] = t.TempDir()
	off.mem = func() (memReading, error) { return memReading{TotalGB: 32, Level: 1, Pressure: 4}, nil }
	if _, denied := gateVerdict(t, off, dir, bashHook("s", dir, "sh scripts/check.sh", "x", nil)); denied {
		t.Fatal("with the slot off nothing is refused")
	}
	if _, err := os.Stat(filepath.Join(off.env["HOME"], ".buddy")); err == nil {
		t.Fatal("with the slot off nothing is written")
	}
	// The control: the same reading with the slot on refuses.
	if out, _, code := off.run(t, dir, "", "mem", "init"); code != 0 {
		t.Fatal(out)
	}
	if _, denied := gateVerdict(t, off, dir, bashHook("s", dir, "sh scripts/check.sh", "x", nil)); !denied {
		t.Fatal("control: the slot on refuses a tier at 1% free")
	}

	for _, tc := range []struct {
		name   string
		break_ func(t *testing.T, f *fixture)
		want   string
	}{
		{"corrupt bookings", func(t *testing.T, f *fixture) {
			os.WriteFile(filepath.Join(memDir(f), "bookings.json"), []byte("{trunc"), 0o600)
		}, "bookings.json does not parse"},
		{"newer bookings", func(t *testing.T, f *fixture) {
			os.WriteFile(filepath.Join(memDir(f), "bookings.json"), []byte(`{"version":2}`), 0o600)
		}, "upgrade this binary"},
		{"newer config", func(t *testing.T, f *fixture) {
			os.WriteFile(filepath.Join(memDir(f), "config.json"), []byte(`{"version":9,"floor_gb":4}`), 0o600)
		}, "upgrade this binary"},
		{"config not statable", func(t *testing.T, f *fixture) {
			os.RemoveAll(filepath.Join(f.env["HOME"], ".buddy"))
			os.WriteFile(filepath.Join(f.env["HOME"], ".buddy"), []byte("a file"), 0o600)
		}, "cannot read"},
		{"no memory reading", func(t *testing.T, f *fixture) {
			f.mem = func() (memReading, error) { return memReading{}, fmt.Errorf("sysctl said no") }
		}, "no memory reading: sysctl said no"},
		{"booking unwritable", func(t *testing.T, f *fixture) {
			// The lock and the bookings exist (the control booked), so only the
			// temporary file the write renames over them cannot be made.
			if err := os.Chmod(memDir(f), 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(memDir(f), 0o700) })
		}, "the booking could not be written"},
	} {
		f, _ := memSlotFixture(t, 30)
		// Positive control: before the break, the heavy call is allowed.
		if reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "go test ./x", "ok", nil)); denied {
			t.Fatalf("%s: control allowed: %s", tc.name, reason)
		}
		tc.break_(t, f)
		reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "go test ./x", "h", nil))
		if !denied || !strings.Contains(reason, tc.want) || !strings.Contains(reason, "Ask the operator") {
			t.Errorf("%s: a heavy call is DENIED naming %q: %v %s", tc.name, tc.want, denied, reason)
		}
		if _, denied := gateVerdict(t, f, dir, bashHook("s", dir, "git commit -m 'go test ./... green'", "l", nil)); denied {
			t.Errorf("%s: a light call never touches a broken slot", tc.name)
		}
	}
}

// A claims-phase refusal books nothing, and a claims-phase allow from every
// exit is judged: here a governed, paused session (a deny) and an ungoverned
// directory (the pathless allow).
func TestMemGateRunsAfterTheClaimsPhase(t *testing.T) {
	boundedParallel(t)
	f, _ := memSlotFixture(t, 30)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "pause", "bravo", "--note", "hold"); code != 0 {
		t.Fatal(errw)
	}
	reason, denied := gateVerdict(t, f, f.wtB, bashHook("sess-b", f.wtB, "go test ./...", "p", nil))
	if !denied || !strings.Contains(reason, "paused") {
		t.Fatalf("the claims phase's pause refuses first: %v %s", denied, reason)
	}
	if n := len(memRows(t, f)); n != 0 {
		t.Fatalf("a call the claims phase refused books nothing: %d rows", n)
	}
	// The governed, unpaused session is judged and booked.
	if reason, denied := gateVerdict(t, f, f.repo, bashHook("sess-a", f.repo, "go test ./...", "q", nil)); denied {
		t.Fatalf("an allowed governed call that fits: %s", reason)
	}
	if n := len(memRows(t, f)); n != 1 {
		t.Fatalf("an allowed governed call is booked: %d rows", n)
	}
}

func TestMemGateOverride(t *testing.T) {
	boundedParallel(t)
	f, _ := memSlotFixture(t, 6) // 6 free: nothing heavy fits above 4
	dir := t.TempDir()
	cmd := "BUDDY_MEM_OK='operator said 14:02' sh scripts/check.sh"
	if _, denied := gateVerdict(t, f, dir, bashHook("s", dir, "sh scripts/check.sh", "c", nil)); !denied {
		t.Fatal("control: the bare tier is refused")
	}
	if reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, cmd, "o", nil)); denied {
		t.Fatalf("the override proceeds: %s", reason)
	}
	log, _ := os.ReadFile(filepath.Join(memDir(f), "overrides.log"))
	if !strings.Contains(string(log), "BUDDY_MEM_OK=operator said 14:02") || !strings.Contains(string(log), " s ") {
		t.Fatalf("every override is logged with its session and reason: %q", log)
	}
	if rows := memRows(t, f); len(rows) != 1 || rows[0].GB != 8 {
		t.Fatalf("an overridden tier is still booked: %+v", rows)
	}
	// An empty reason, or an override that cannot be logged, is judged normally.
	if _, denied := gateVerdict(t, f, dir, bashHook("s", dir, "BUDDY_MEM_OK= sh scripts/check.sh", "e", nil)); !denied {
		t.Fatal("an empty reason is no override")
	}
	logPath := filepath.Join(memDir(f), "overrides.log")
	os.Remove(logPath)
	os.Mkdir(logPath, 0o700) // appending to a directory fails
	if _, denied := gateVerdict(t, f, dir, bashHook("s", dir, cmd, "u", nil)); !denied {
		t.Fatal("an override that cannot be logged is not honored")
	}
}

func TestMemGateUnderCoveringAdmitRefused(t *testing.T) {
	boundedParallel(t)
	f, _ := memSlotFixture(t, 30)
	dir := t.TempDir()
	reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "buddy mem admit codex --wait 900 && go test -race ./...", "u", nil))
	if !denied || !strings.Contains(reason, "buddy mem admit goall --wait 900") {
		t.Fatalf("an admit that does not cover its tail is refused, naming the kind: %v %s", denied, reason)
	}
	if _, denied := gateVerdict(t, f, dir, bashHook("s", dir, "buddy mem admit goall --wait 900 && go test -race ./...", "v", nil)); denied {
		t.Fatal("control: a covering admit passes")
	}
	if n := len(memRows(t, f)); n != 0 {
		t.Fatalf("the self-gated tail is booked by the admit at run time, not by the gate: %d rows", n)
	}
}

func TestMemBookPruneAndQueue(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 19, 4, 0, 0, 0, time.UTC)
	ts := unixSec(now)
	alive := map[int]bool{100: true}
	env := Env{
		BootID:    func() (string, bool) { return "boot-2", true },
		ProcAlive: func(p store.ProcRef) bool { return alive[p.PID] },
	}
	book := memBook{Boot: "boot-2", Rows: []memRow{
		{Owner: "a", Call: "1", GB: 6, T: ts - 45, Until: ts + 45},                             // half faded: 3
		{Owner: "b", Call: "2", GB: 6, T: ts - 100, Until: ts - 10},                            // expired
		{Owner: "c", Call: "3", GB: 3, Pending: true, PID: 100, Seq: 5, T: ts, Until: ts + 60}, // a waiter
		{Owner: "d", Call: "4", GB: 3, Pending: true, PID: 200, Seq: 6, T: ts, Until: ts + 60}, // its process is gone
		{Owner: "e", Call: "5", GB: 3, Pending: true, PID: 100, Seq: 7, T: ts - 9, Until: ts},  // past its deadline
		{Owner: "f", Call: "6", GB: 2, T: ts + 3600, Until: ts + 3690},                         // a clock stepped back: full, never more
		{Owner: "g", Call: "7", GB: 9, Pending: true, PID: 100, Seq: 8, T: ts, Until: ts + 60}, // cannot fit an empty machine
	}}
	book.prune(env, now)
	if len(book.Rows) != 4 {
		t.Fatalf("expired, gone and past-deadline rows are dropped: %+v", book.Rows)
	}
	// A newcomer counts a (3), c (3, pending), f (2, clamped); not g (9 > 12-4).
	if got := book.booked(nil, now, 12, 4); got != 8 {
		t.Errorf("a newcomer counts 8 GB, got %v", got)
	}
	// A waiter ahead of c (seq 4) does not count c; one behind it (seq 9) does.
	if got := book.booked(&memRow{Owner: "z", Call: "z", Seq: 4}, now, 12, 4); got != 5 {
		t.Errorf("a waiter ahead counts 5 GB, got %v", got)
	}
	if got := book.booked(&memRow{Owner: "z", Call: "z", Seq: 9}, now, 12, 4); got != 8 {
		t.Errorf("a waiter behind counts 8 GB, got %v", got)
	}
	// Another boot: every row is gone.
	book.Boot = "boot-1"
	book.prune(env, now)
	if len(book.Rows) != 0 || book.Boot != "boot-2" {
		t.Fatalf("rows of another boot are dropped: %+v", book)
	}
}

func TestMemBookCodexReplacesItsOwnForegroundKind(t *testing.T) {
	t.Parallel()
	now := time.Now()
	for _, codex := range []bool{false, true} {
		book := memBook{}
		bookCall(&book, memHookCall{owner: "th", call: "1", codex: codex}, kindCost("goall", false), now)
		bookCall(&book, memHookCall{owner: "th", call: "2", codex: codex}, kindCost("goall", false), now)
		want := 2
		if codex {
			want = 1 // nothing releases a Codex row: a re-run replaces it
		}
		if len(book.Rows) != want {
			t.Errorf("codex %v: %d rows, want %d", codex, len(book.Rows), want)
		}
	}
}

func TestMemAdmit(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()

	// Off: exit 0, nothing booked, said on stderr.
	off := newFixture(t)
	off.env["HOME"] = t.TempDir()
	if _, errw, code := off.run(t, dir, "", "mem", "admit", "lane", "--wait", "600"); code != 0 || !strings.Contains(errw, "off") {
		t.Fatalf("admit with the slot off exits 0: %d %q", code, errw)
	}

	f, m := memSlotFixture(t, 6)
	f.env[EnvClaudeSession] = "s-admit"
	if _, errw, code := f.run(t, dir, "", "mem", "admit", "goall", "--wait", "0"); code != 3 {
		t.Fatalf("refused with no wait: exit 3, got %d %q", code, errw)
	}
	if n := len(memRows(t, f)); n != 0 {
		t.Fatalf("a refused admit with no wait leaves nothing: %d", n)
	}
	// Refused, then the memory frees during its wait: admitted, its pending
	// place seen while it waited and gone after.
	var sawPending bool
	f.sleep = func(time.Duration) {
		for _, r := range memRows(t, f) {
			sawPending = sawPending || (r.Pending && r.Owner == "s-admit" && r.GB == 6)
		}
		m.Level = 100
	}
	if _, errw, code := f.run(t, dir, "", "mem", "admit", "goall", "--wait", "30"); code != 0 {
		t.Fatalf("admitted once memory frees: %d %q", code, errw)
	}
	rows := memRows(t, f)
	if !sawPending || len(rows) != 1 || rows[0].Pending || !rows[0].Detached || rows[0].GB != 6 {
		t.Fatalf("a pending place while waiting (%v), one fading 6 GB row after: %+v", sawPending, rows)
	}
	// Several kinds sum; a newcomer counts the admit's row.
	if _, errw, code := f.run(t, dir, "", "mem", "admit", "goall", "goall"); code != 0 {
		t.Fatalf("100%% of 32 GB holds 12 more: %q", errw)
	}
	for _, args := range [][]string{{"mem", "admit"}, {"mem", "admit", "nosuch"}, {"mem", "admit", "lane", "--wait", "4000"}, {"mem", "admit", "lane", "extra", "--wait", "1"}, {"mem", "bogus"}, {"mem", "status", "x"}} {
		if _, _, code := f.run(t, dir, "", args...); code == 0 {
			t.Errorf("%v is refused", args)
		}
	}
	out, _, code := f.run(t, dir, "", "mem", "status")
	if code != 0 || !strings.Contains(out, "memory slot: ON") || !strings.Contains(out, "s-admit") {
		t.Fatalf("status shows the slot and its rows: %q", out)
	}
}

// Concurrent judges never admit more than fits: ten 6 GB starts against 20
// free and a 4 GB floor admit exactly two. Each judge opens the lock itself,
// as separate hook processes do (flock is per open file description).
func TestMemGateConcurrentJudgesNeverOverAdmit(t *testing.T) {
	boundedParallel(t)
	f, _ := memSlotFixture(t, 20)
	now := f.clock
	env := Env{
		Getenv:     func(k string) string { return f.env[k] },
		Now:        func() time.Time { return now },
		MemReading: f.mem,
		BootID:     func() (string, bool) { return "b", true },
		ProcAlive:  func(store.ProcRef) bool { return true },
	}
	slot := memSlotOf(env)
	var wg sync.WaitGroup
	var mu sync.Mutex
	admitted := 0
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h := hookInput{SessionID: fmt.Sprintf("s%d", i), ToolName: "Bash"}
			h.ToolInput.Command = "go test ./..."
			h.ToolUseID = json.RawMessage(fmt.Sprintf("%q", fmt.Sprint("c", i)))
			if gateMemory(h, env, slot, time.Now()) == "" {
				mu.Lock()
				admitted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if admitted != 2 || len(memRows(t, f)) != 2 {
		t.Fatalf("exactly two of ten fit: %d admitted, %d rows", admitted, len(memRows(t, f)))
	}
}

// A waiter reads the floor on every try (Codex code pass): raised while it
// waits, the raised floor holds. The control: the same rise in free memory
// without the floor change admits the same tier.
func TestMemAdmitRereadsTheFloor(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()
	f, m := memSlotFixture(t, 10) // 9.9 GB free: a tier leaves 1.9, under 4
	f.sleep = func(time.Duration) {
		if err := os.WriteFile(filepath.Join(memDir(f), "config.json"), []byte(`{"version":1,"floor_gb":10}`), 0o600); err != nil {
			t.Error(err)
		}
		m.Level = 44 // 14.1 GB free: a tier leaves 6.1, over 4 and under 10
	}
	if _, errw, code := f.run(t, dir, "", "mem", "admit", "tier", "--wait", "30"); code != 3 {
		t.Fatalf("a floor raised during the wait holds: exit 3, got %d %q", code, errw)
	}
	g, gm := memSlotFixture(t, 10)
	g.sleep = func(time.Duration) { gm.Level = 44 }
	if _, errw, code := g.run(t, dir, "", "mem", "admit", "tier", "--wait", "30"); code != 0 {
		t.Fatalf("control: the same rise under the 4 GB floor admits: %d %q", code, errw)
	}
}

// A call judged again is not counted against itself (Codex code pass): its
// own row is replaced, not stacked. The control: another call of the same
// size is refused against that row.
func TestMemGateRejudgedCallReplacesItsRow(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()
	f, _ := memSlotFixture(t, 13) // 12.8 GB free: one 6 GB start fits, two do not
	if reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "go test ./...", "same", nil)); denied {
		t.Fatalf("the first start fits: %s", reason)
	}
	if reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "go test ./...", "same", nil)); denied {
		t.Fatalf("the same call judged again is not refused against its own row: %s", reason)
	}
	if rows := memRows(t, f); len(rows) != 1 || rows[0].GB != 6 {
		t.Fatalf("one 6 GB row, replaced: %+v", rows)
	}
	if _, denied := gateVerdict(t, f, dir, bashHook("s", dir, "go test ./...", "other", nil)); !denied {
		t.Fatal("control: another 6 GB call is refused against the first")
	}
}

// A wait under one poll still waits, for what it asked (Fable cross-check:
// `--wait 10` exited at once). The control: --wait 30 sleeps two polls.
func TestMemAdmitShortWaitStillWaits(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()
	for _, tc := range []struct {
		wait  string
		slept []time.Duration
	}{
		{"10", []time.Duration{10 * time.Second}},
		{"30", []time.Duration{15 * time.Second, 15 * time.Second}},
		{"20", []time.Duration{15 * time.Second, 5 * time.Second}},
	} {
		f, _ := memSlotFixture(t, 6) // a tier never fits
		var slept []time.Duration
		f.sleep = func(d time.Duration) { slept = append(slept, d) }
		if _, errw, code := f.run(t, dir, "", "mem", "admit", "tier", "--wait", tc.wait); code != 3 {
			t.Fatalf("--wait %s: still refused, exit 3: %d %q", tc.wait, code, errw)
		}
		if fmt.Sprint(slept) != fmt.Sprint(tc.slept) {
			t.Errorf("--wait %s slept %v, want %v", tc.wait, slept, tc.slept)
		}
	}
}
