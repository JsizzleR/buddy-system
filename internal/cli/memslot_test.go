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
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// memSlotFixture is a fixture with a HOME, a fake memory reading of free GB
// on a 32 GB machine, and the slot initialized with the floor it means, 4 GB
// (a bare init would derive 5.33 GB here, D-239). That is a version-2 config
// whose cap, 10.67 GB, is over every figure in the table, so each test built
// on it reads D-238's figures; the tests of the cap, the derived floor and a
// version-1 config make their own (memSlotOn).
func memSlotFixture(t *testing.T, free float64) (*fixture, *memReading) {
	t.Helper()
	f := newFixture(t)
	f.env["HOME"] = t.TempDir()
	m := &memReading{TotalGB: 32, Level: int(free / 32 * 100), Pressure: 1}
	f.mem = func() (memReading, error) { return *m, nil }
	if out, errw, code := f.run(t, f.repo, "", "mem", "init", "--floor-gb", "4"); code != 0 {
		t.Fatalf("mem init: %s %s", out, errw)
	}
	return f, m
}

// memSlotOn is a fixture on a machine of total GB at level percent free, with
// the config written as given: a version-1 one is what D-238's init left on a
// machine, and no init of this binary writes it.
func memSlotOn(t *testing.T, total float64, level int, config string) (*fixture, *memReading) {
	t.Helper()
	f := newFixture(t)
	f.env["HOME"] = t.TempDir()
	m := &memReading{TotalGB: total, Level: level, Pressure: 1}
	f.mem = func() (memReading, error) { return *m, nil }
	if err := os.MkdirAll(memDir(f), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir(f), "config.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return f, m
}

const (
	memV1Floor4 = `{"version":1,"floor_gb":4}`
	memV2Auto   = `{"version":2,"floor":"auto"}`
)

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
	// An under-covered admit INSIDE a covered one: the outer tier covers the
	// whole tail (the call's total is 0), and the inner codex admit still
	// covers 0.5 of 6. "Books nothing" alone would allow it (the mutation
	// battery's survivor); the control nests an admit that covers.
	reason, denied = gateVerdict(t, f, dir, bashHook("s", dir, "buddy mem admit tier && { buddy mem admit codex && go test ./...; }", "n", nil))
	if !denied || !strings.Contains(reason, "its `buddy mem admit codex` covers 0.5 GB") {
		t.Fatalf("an under-covered admit nested in a covered one is refused: %v %s", denied, reason)
	}
	if reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "buddy mem admit tier && { buddy mem admit goall && go test ./...; }", "m", nil)); denied {
		t.Fatalf("control: nested and covering, it passes: %s", reason)
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
		bookCall(&book, memHookCall{owner: "th", call: "1", codex: codex}, new(memWalker).kindCost("goall", false), now)
		bookCall(&book, memHookCall{owner: "th", call: "2", codex: codex}, new(memWalker).kindCost("goall", false), now)
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

// ---- the seam every reader passes through (D-239) ----

// A reading is checked once, in Env.memReading, before anything multiplies or
// compares it. Without the check each of these is ADMITTED: an infinite total
// is infinite headroom, a NaN one makes `left < floor` false, a total of 0
// beside an available figure prices every start from nothing. The control is
// the same heavy call under the fixture's own reading; a light call passes
// under every one.
func TestMemReadingUnusableIsNoReading(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()
	nan, inf := math.NaN(), math.Inf(1)
	for _, tc := range []struct {
		name string
		m    memReading
		want string
	}{
		{"a total of 0 beside an available figure", memReading{TotalGB: 0, AvailGB: 12, Level: 50, Pressure: 1}, "its total is 0 GB"},
		{"a negative total", memReading{TotalGB: -32, Level: 50, Pressure: 1}, "its total is -32 GB"},
		{"an infinite total", memReading{TotalGB: inf, Level: 50, Pressure: 1}, "its total is +Inf GB"},
		{"a NaN total", memReading{TotalGB: nan, Level: 50, Pressure: 1}, "its total is NaN GB"},
		{"available above the total", memReading{TotalGB: 32, AvailGB: 33, Level: 100, Pressure: 1}, "its available figure is 33 GB"},
		{"a negative available figure", memReading{TotalGB: 32, AvailGB: -1, Level: 90, Pressure: 1}, "its available figure is -1 GB"},
		{"a NaN available figure", memReading{TotalGB: 32, AvailGB: nan, Level: 90, Pressure: 1}, "its available figure is NaN GB"},
		{"an infinite available figure", memReading{TotalGB: 32, AvailGB: inf, Level: 90, Pressure: 1}, "its available figure is +Inf GB"},
		{"a level over 100", memReading{TotalGB: 32, Level: 101, Pressure: 1}, "its level is 101"},
		{"a negative level", memReading{TotalGB: 32, Level: -1, Pressure: 1}, "its level is -1"},
		{"a NaN swap figure", memReading{TotalGB: 32, Level: 90, Pressure: 1, SwapGB: nan}, "its swap figure is NaN GB"},
		{"a negative swap figure", memReading{TotalGB: 32, Level: 90, Pressure: 1, SwapGB: -1}, "its swap figure is -1 GB"},
		// Every figure in range, and total times level overflows (Codex code pass).
		{"a finite total whose free figure is not", memReading{TotalGB: 1e308, Level: 100, Pressure: 1}, "its total of 1e+308 GB at level 100 is no finite free figure"},
	} {
		f, m := memSlotFixture(t, 30)
		if reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "go test ./x", "ok", nil)); denied {
			t.Fatalf("%s: control allowed: %s", tc.name, reason)
		}
		*m = tc.m
		reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "go test ./x", "h", nil))
		if !denied || !strings.Contains(reason, "no memory reading: the reading is not usable: "+tc.want) {
			t.Errorf("%s: a heavy call is DENIED naming %q: %v %s", tc.name, tc.want, denied, reason)
		}
		if _, denied := gateVerdict(t, f, dir, bashHook("s", dir, "git status", "l", nil)); denied {
			t.Errorf("%s: a light call is never refused for a reading", tc.name)
		}
		if _, errw, code := f.run(t, dir, "", "mem", "admit", "gotest"); code != 1 || !strings.Contains(errw, tc.want) {
			t.Errorf("%s: admit exits 1 naming it, so an && tail does not run: %d %q", tc.name, code, errw)
		}
		if out, _, code := f.run(t, dir, "", "mem", "status"); code != 0 || !strings.Contains(out, "no memory reading") || !strings.Contains(out, tc.want) {
			t.Errorf("%s: status says heavy starts are refused and why: %d %q", tc.name, code, out)
		}
		// init on a machine with no slot yet writes nothing under such a reading.
		g := newFixture(t)
		g.env["HOME"] = t.TempDir()
		g.mem = func() (memReading, error) { return tc.m, nil }
		if _, errw, code := g.run(t, dir, "", "mem", "init"); code == 0 || !strings.Contains(errw, tc.want) {
			t.Errorf("%s: init refuses: %d %q", tc.name, code, errw)
		}
		if _, err := os.Stat(filepath.Join(g.env["HOME"], ".buddy")); err == nil {
			t.Errorf("%s: a refused init writes nothing", tc.name)
		}
	}
}

// A reader that panics is a reader that failed: the heavy call is denied with
// the cause, and the gate still exits 0 (under D-107's hook line a panic
// exits 2, which blocks the call whatever it was). The light call beside it
// never reaches the reader.
func TestMemReaderPanicIsAnError(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()
	f, _ := memSlotFixture(t, 30)
	if reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "go test ./x", "ok", nil)); denied {
		t.Fatalf("control allowed: %s", reason)
	}
	calls := 0
	f.mem = func() (memReading, error) { calls++; panic("a reader bug") }
	reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "go test ./x", "h", nil))
	if !denied || !strings.Contains(reason, "no memory reading: the memory reader panicked (a reader bug)") {
		t.Fatalf("a heavy call is DENIED naming the panic: %v %s", denied, reason)
	}
	if calls == 0 {
		t.Fatal("control: the heavy call reached the reader")
	}
	calls = 0
	if _, denied := gateVerdict(t, f, dir, bashHook("s", dir, "git commit -m 'go test ./... green'", "l", nil)); denied || calls != 0 {
		t.Fatalf("a light call is allowed and never reaches the reader: denied %v, %d calls", denied, calls)
	}
	if _, errw, code := f.run(t, dir, "", "mem", "admit", "gotest"); code != 1 || !strings.Contains(errw, "panicked") {
		t.Fatalf("admit exits 1 on a panicking reader: %d %q", code, errw)
	}
}

// A reader that supplies the kernel's own available figure is judged on it,
// not on total times level (the Linux reader's seam). The control: the same
// reading without the figure is judged on the level and admits.
func TestMemReadingAvailableFigureIsTheFreeFigure(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()
	f, m := memSlotFixture(t, 30)
	*m = memReading{TotalGB: 32, Level: 90, Pressure: 1, AvailGB: 5}
	reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "go test ./x", "a", nil))
	if !denied || !strings.Contains(reason, "free 5.0 GB - booked 0.0 GB - this start 3.0 GB = 2.0 GB") {
		t.Fatalf("5 GB available holds no 3 GB start above a 4 GB floor, whatever the level says: %v %s", denied, reason)
	}
	m.AvailGB = 0
	if reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "go test ./x", "b", nil)); denied {
		t.Fatalf("control: with no available figure the level (90%% of 32 GB) admits: %s", reason)
	}
}

// The reader's Basis is printed by status and init on ONE line, capped: a
// reader builds it from files a container's host controls, and a newline in it
// could otherwise fabricate a status line. A reader that gives none prints no
// such line (darwin).
func TestMemBasisIsOneFencedLine(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()
	f, m := memSlotFixture(t, 20)
	readingLines := func(out string) (n int, forged bool, longest int) {
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "reading: ") {
				n++
				longest = max(longest, len(l))
			}
			forged = forged || strings.HasPrefix(l, "memory slot: OFF")
		}
		return
	}
	out, _, _ := f.run(t, dir, "", "mem", "status")
	if n, _, _ := readingLines(out); n != 0 {
		t.Fatalf("no Basis, no reading line: %q", out)
	}
	m.Basis = "MemAvailable of /proc/meminfo\nmemory slot: OFF " + strings.Repeat("x", 400)
	for _, args := range [][]string{{"mem", "status"}, {"mem", "init"}} {
		out, errw, code := f.run(t, dir, "", args...)
		n, forged, longest := readingLines(out)
		// 256 bytes of the value, then fence.Line's own mark that it cut it.
		if code != 0 || n != 1 || forged || longest > len("reading: ")+256+len("…[truncated]") || !strings.Contains(out, "reading: MemAvailable of /proc/meminfo") {
			t.Errorf("%v prints the Basis on one fenced line of at most 256 bytes: code %d, %d reading lines, forged %v, longest %d: %q %q", args, code, n, forged, longest, out, errw)
		}
	}
}

// ---- the slot fitted to the machine (D-239) ----

// mib is a figure in GB that is a whole number of MiB, as a capped cost is.
func mib(n float64) float64 { return n / 1024 }

func memConfigBytes(t *testing.T, f *fixture) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(memDir(f), "config.json"))
	if err != nil {
		return "(" + err.Error() + ")"
	}
	return strings.TrimSpace(string(b))
}

// An automatic floor is a sixth of the machine, held to [1, 8] GB, derived at
// every judge. Each row is judged at the edge: a 0.5 GB Codex pass with the
// floor and 0.5 GB available is admitted (left == floor), and 10 MB under it
// is refused naming that floor. The explicit floor beside it does not move.
func TestMemFloorFollowsTheMachine(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()
	for _, tc := range []struct {
		total, floor float64
		says, rule   string
	}{
		{4, 1, "floor 1.0 GB", "held up to the 1.0 GB least"},
		{6, 1, "floor 1.0 GB", "1/6 of this machine's 6.0 GB;"},
		{8, 8.0 / 6, "floor 1.3 GB", "1/6 of this machine's 8.0 GB;"},
		{16, 16.0 / 6, "floor 2.7 GB", "1/6 of this machine's 16.0 GB;"},
		{24, 4, "floor 4.0 GB", "1/6 of this machine's 24.0 GB;"},
		{48, 8, "floor 8.0 GB", "1/6 of this machine's 48.0 GB;"},
		{64, 8, "floor 8.0 GB", "held down to the 8.0 GB most"},
		{256, 8, "floor 8.0 GB", "held down to the 8.0 GB most"},
		// The cap's half of the rule names its own least, and the machine.
		{2, 1, "floor 1.0 GB", "one start books at most 1.0 GB, 1/3 of this machine's 2.0 GB, held up to the 1.0 GB least (config version 2)"},
	} {
		f, m := memSlotOn(t, tc.total, 50, memV2Auto)
		m.AvailGB = tc.floor + 0.5
		if reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "codex exec x", "edge", nil)); denied {
			t.Errorf("%v GB: a 0.5 GB start that leaves exactly the %v GB floor is admitted: %s", tc.total, tc.floor, reason)
		}
		// The header's own floor figure, not only the rule line's (which holds
		// the same words: a status printing the stored floor_gb, 0 under auto,
		// passed on the rule line alone).
		out, _, code := f.run(t, dir, "", "mem", "status")
		if code != 0 || !strings.Contains(out, "  "+tc.says+"  pressure") || !strings.Contains(out, "rule: "+tc.says) || !strings.Contains(out, tc.rule) {
			t.Errorf("%v GB: status says %q in its header and its rule, and %q: %d %q", tc.total, tc.says, tc.rule, code, out)
		}
		g, gm := memSlotOn(t, tc.total, 50, memV2Auto)
		gm.AvailGB = tc.floor + 0.5 - 0.01
		reason, denied := gateVerdict(t, g, dir, bashHook("s", dir, "codex exec x", "under", nil))
		if !denied || !strings.Contains(reason, "under the "+strings.TrimPrefix(tc.says, "floor ")+" floor") {
			t.Errorf("%v GB: 10 MB under the floor is refused naming it: %v %s", tc.total, denied, reason)
		}
	}
	// The floor is never stored: one config, carried to a larger machine,
	// follows it (16 GB: 2.67; 64 GB: 8). An explicit one stays what was set.
	f, m := memSlotOn(t, 16, 50, memV2Auto)
	m.AvailGB = 3.5 // over 2.67 + 0.5
	if reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "codex exec x", "a", nil)); denied {
		t.Fatalf("control: on 16 GB the floor is 2.67 GB: %s", reason)
	}
	f.run(t, dir, postHook("PostToolUse", "s", dir, "codex exec x", "a", nil), "beat")
	m.TotalGB = 64
	if reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "codex exec x", "b", nil)); !denied || !strings.Contains(reason, "under the 8.0 GB floor") {
		t.Fatalf("the same config on 64 GB holds an 8 GB floor: %v %s", denied, reason)
	}
	e, em := memSlotOn(t, 16, 50, `{"version":2,"floor_gb":3}`)
	em.TotalGB, em.AvailGB = 64, 3.5
	if reason, denied := gateVerdict(t, e, dir, bashHook("s", dir, "codex exec x", "c", nil)); denied {
		t.Fatalf("an explicit 3 GB floor is 3 GB on any machine: %s", reason)
	}
	// With no reading there is no total, so an automatic floor has no figure:
	// status names it, never a guess (and never the stored 0). The explicit
	// floor beside it is its figure.
	for config, want := range map[string]string{
		memV2Auto:                    "memory slot: ON, floor auto (1/6 of the machine's memory); no memory reading (sysctl said no): heavy starts are refused",
		`{"version":2,"floor_gb":3}`: "memory slot: ON, floor 3.0 GB; no memory reading (sysctl said no): heavy starts are refused",
	} {
		s, _ := memSlotOn(t, 16, 50, config)
		s.mem = func() (memReading, error) { return memReading{}, fmt.Errorf("sysctl said no") }
		if out, _, code := s.run(t, dir, "", "mem", "status"); code != 0 || !strings.Contains(out, want) {
			t.Errorf("%s: status without a reading says %q: %d %q", config, want, code, out)
		}
	}
}

// Under the fitted slot one start books at most a third of the machine, in
// whole MiB, never under 1 GB; the table's small kinds stay what they are.
// The same commands under a version-1 config on the same machine book the
// table's figures (the control, and D-238 kept).
func TestMemCapOneStartBooksAThirdOfTheMachine(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()
	booked := func(f *fixture, cmd string) (float64, string) {
		id := "c-" + cmd
		reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, cmd, id, nil))
		if denied {
			return -1, reason
		}
		sum := 0.0
		for _, r := range memRows(t, f) {
			if strings.HasPrefix(r.Call, id) {
				sum += r.GB
			}
		}
		// A foreground row goes at PostToolUse; a detached one is left to its
		// own machine (each row below has a fixture to itself).
		f.run(t, dir, postHook("PostToolUse", "s", dir, cmd, id, nil), "beat")
		return sum, ""
	}
	for _, tc := range []struct {
		total  float64
		config string
		cmd    string
		want   float64
	}{
		{8, memV2Auto, "sh scripts/check.sh", mib(2730)}, // 8/3 GB is 2730.67 MiB
		{8, memV2Auto, "go test ./...", mib(2730)},
		{8, memV2Auto, "go test ./x", mib(2730)}, // the table's 3 is over the cap too
		{8, memV2Auto, "claude -p x", 1},         // a lane's 1 GB is not
		{8, memV2Auto, "codex exec x", 0.5},
		{8, memV2Auto, "go test ./a & go test ./b & wait", mib(5460)}, // the cap is per start, summed
		{8, memV2Auto, "podman machine start", mib(2730)},             // a DETACHED start (vm, 4) is capped too
		{8, `{"version":2,"floor_gb":1}`, "sh scripts/check.sh", mib(2730)},
		{16, memV2Auto, "sh scripts/check.sh", mib(5461)},
		{16, memV2Auto, "go test -race ./x", 5}, // race's 5 is under 5.33
		{24, memV2Auto, "sh scripts/check.sh", 8},
		{32, memV2Auto, "sh scripts/check.sh", 8},
		{2, memV2Auto, "claude -p x", 1}, // the cap's 1 GB least: 2/3 GB would book a lane at 0.67
		// Version 1 on the same machines: the table, uncapped.
		{8, `{"version":1,"floor_gb":0.5}`, "go test ./...", 6},
		{16, memV1Floor4, "sh scripts/check.sh", 8},
		{16, memV1Floor4, "go test ./...", 6},
	} {
		f, _ := memSlotOn(t, tc.total, 100, tc.config)
		got, reason := booked(f, tc.cmd)
		if got != tc.want {
			t.Errorf("%v GB %s: %q books %v GB, want %v (%s)", tc.total, tc.config, tc.cmd, got, tc.want, reason)
		}
	}
	// `buddy mem admit` books what the gate's capped walk prices: per kind.
	f, _ := memSlotOn(t, 8, 100, memV2Auto)
	if _, errw, code := f.run(t, dir, "", "mem", "admit", "tier", "lane"); code != 0 || !strings.Contains(errw, "(3.7 GB)") {
		t.Fatalf("admit tier lane on 8 GB books 2.67 + 1: %d %q", code, errw)
	}
	if rows := memRows(t, f); len(rows) != 1 || rows[0].GB != mib(2730)+1 {
		t.Fatalf("one row of 2730 MiB + 1 GB: %+v", rows)
	}
	g, _ := memSlotOn(t, 8, 100, `{"version":1,"floor_gb":0.5}`)
	if _, errw, code := g.run(t, dir, "", "mem", "admit", "goall", "lane"); code != 0 || !strings.Contains(errw, "(7.0 GB)") {
		t.Fatalf("control: under version 1 admit books the table, 6 + 1: %d %q", code, errw)
	}
}

// A version-1 config is D-238's on every machine, under this binary too: the
// explicit floor, the table uncapped, a start that can never fit taught to
// wait and queued. On a 16 GB machine the fitted slot beside it books a tier
// at 5.33 GB over a 2.67 GB floor; nothing but the operator's init moves a
// machine from one to the other.
func TestMemVersion1ConfigIsKeptExactly(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()
	f, _ := memSlotOn(t, 16, 100, memV1Floor4)
	if reason, denied := gateVerdict(t, f, dir, bashHook("s1", dir, "sh scripts/check.sh", "t1", nil)); denied {
		t.Fatalf("16 free less a tier's 8 leaves 8, over the 4 GB floor: %s", reason)
	}
	if rows := memRows(t, f); len(rows) != 1 || rows[0].GB != 8 {
		t.Fatalf("the tier books the table's 8 GB: %+v", rows)
	}
	reason, denied := gateVerdict(t, f, dir, bashHook("s2", dir, "go test ./...", "t2", nil))
	if !denied || !strings.Contains(reason, "free 16.0 GB - booked 8.0 GB - this start 6.0 GB = 2.0 GB, under the 4.0 GB floor") || !strings.Contains(reason, "buddy mem admit goall --wait 900") {
		t.Fatalf("a 6 GB start behind it leaves 2, under the explicit 4 GB floor, and is taught the wait: %v %s", denied, reason)
	}
	// The control: the same two calls under the fitted slot on the same machine.
	g, _ := memSlotOn(t, 16, 100, memV2Auto)
	gateVerdict(t, g, dir, bashHook("s1", dir, "sh scripts/check.sh", "t1", nil))
	if reason, denied := gateVerdict(t, g, dir, bashHook("s2", dir, "go test ./...", "t2", nil)); denied {
		t.Fatalf("control: fitted, 16 - 5.33 - 5.33 leaves 5.33, over the 2.67 GB floor: %s", reason)
	}
	// What an 8 GB machine could never admit is still a WAIT under version 1:
	// the refusal names the wait form, and the admit queues and exits 3.
	h, _ := memSlotOn(t, 8, 100, memV1Floor4)
	reason, denied = gateVerdict(t, h, dir, bashHook("s", dir, "sh scripts/check.sh", "t", nil))
	if !denied || !strings.Contains(reason, "buddy mem admit tier --wait 900") || strings.Contains(reason, "can never hold") {
		t.Fatalf("version 1 refuses a tier on 8 GB as D-238 does, with the wait: %v %s", denied, reason)
	}
	var slept int
	h.sleep = func(time.Duration) { slept++ }
	if _, errw, code := h.run(t, dir, "", "mem", "admit", "tier", "--wait", "30"); code != 3 || slept != 2 || !strings.Contains(errw, "still refused after 30s") {
		t.Fatalf("version 1 queues it and exits 3 after its wait: %d, %d sleeps, %q", code, slept, errw)
	}
	// `mem status` says which it is (a bare `mem init` over it, which says the
	// same and keeps the bytes, is TestMemInitPaths').
	if out, _, _ := f.run(t, dir, "", "mem", "status"); !strings.Contains(out, "rule: floor 4.0 GB, explicit (--floor-gb); costs by the table, uncapped (config version 1)") {
		t.Fatalf("status names the version-1 rule: %q", out)
	}
}

// Every state config.json can be in has one verdict. The unreadable ones deny
// a heavy call naming the cause, pass a light one, and make status and admit
// exit 1 before any wait; the control is the same call under a readable
// config. An unknown word or shape is never guessed at: either guess could
// lower a floor.
func TestMemConfigStates(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()
	for _, tc := range []struct {
		config, want string
	}{
		{`{"version":1,"floor_gb":4,"floor":"auto"}`, "is version 1 and carries a floor word"},
		{`{"version":1,"floor":"auto"}`, "is version 1 and carries a floor word"},
		{`{"version":2,"floor":"auto","floor_gb":4}`, `has both the floor word "auto" and a floor_gb`},
		{`{"version":2,"floor":"auto","floor_gb":-1}`, `has both the floor word "auto" and a floor_gb`},
		{`{"version":2}`, "has no usable floor_gb"},
		{`{"version":2,"floor_gb":0}`, "has no usable floor_gb"},
		{`{"version":2,"floor_gb":-4}`, "has no usable floor_gb"},
		{`{"version":1,"floor_gb":0}`, "has no usable floor_gb"},
		{`{"version":2,"floor":"AUTO"}`, `has the floor word "AUTO", and the only one is "auto"`},
		{`{"version":2,"floor":"pressure"}`, `has the floor word "pressure"`},
		{`{"version":2,"floor":""}`, `has the floor word ""`},
		{`{"version":2,"floor":7}`, "does not parse"},
		{`{"version":2,"floor_gb":"4"}`, "does not parse"},
		{`{"version":3,"floor":"auto"}`, "reads up to 2: upgrade this binary (sh scripts/install.sh)"},
		{`{"version":0,"floor_gb":4}`, "has no usable version (0)"},
		{`{"floor_gb":4}`, "has no usable version (0)"},
		{`{"version":-1,"floor":"auto"}`, "has no usable version (-1)"},
		// A key named twice: encoding/json keeps the last, so these read as a
		// 1 GB floor, as the fitted slot, and as auto with its figure dropped.
		{`{"version":2,"floor_gb":8,"floor_gb":1}`, `names "floor_gb" twice`},
		{`{"version":1,"floor_gb":4,"version":2}`, `names "version" twice`},
		{`{"version":2,"floor":"auto","floor_gb":4,"floor_gb":0}`, `names "floor_gb" twice`},
		{`{"version":2,"floor":"auto","floor":"auto"}`, `names "floor" twice`},
		// The empty string is a key too: returned as "" for "none", its repeat
		// hid the floor_gb's behind it (Codex re-verify 1).
		{`{"":0,"":0,"version":2,"floor_gb":8,"floor_gb":1}`, `names "" twice`},
	} {
		f, _ := memSlotOn(t, 32, 90, memV2Auto)
		if reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "go test ./x", "ok", nil)); denied {
			t.Fatalf("%s: control allowed: %s", tc.config, reason)
		}
		if err := os.WriteFile(filepath.Join(memDir(f), "config.json"), []byte(tc.config), 0o600); err != nil {
			t.Fatal(err)
		}
		reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "go test ./x", "h", nil))
		if !denied || !strings.Contains(reason, tc.want) || !strings.Contains(reason, "Ask the operator") {
			t.Errorf("%s: a heavy call is DENIED naming %q: %v %s", tc.config, tc.want, denied, reason)
		}
		if hasBreak(reason) {
			t.Errorf("%s: the refusal is one line: %q", tc.config, reason)
		}
		if _, denied := gateVerdict(t, f, dir, bashHook("s", dir, "git commit -m 'go test ./... green'", "l", nil)); denied {
			t.Errorf("%s: a light call never touches a broken slot", tc.config)
		}
		// A covered admit is allowed as before: its admit exits 1, so its tail
		// does not run.
		if _, denied := gateVerdict(t, f, dir, bashHook("s", dir, "buddy mem admit gotest --wait 900 && go test ./x", "c", nil)); denied {
			t.Errorf("%s: a covered admit is left to the admit, which refuses", tc.config)
		}
		var slept int
		f.sleep = func(time.Duration) { slept++ }
		if _, errw, code := f.run(t, dir, "", "mem", "admit", "gotest", "--wait", "900"); code != 1 || slept != 0 || !strings.Contains(errw, tc.want) {
			t.Errorf("%s: admit exits 1 before any wait: %d, %d sleeps, %q", tc.config, code, slept, errw)
		}
		if _, errw, code := f.run(t, dir, "", "mem", "status"); code != 1 || !strings.Contains(errw, tc.want) {
			t.Errorf("%s: status exits 1 naming it: %d %q", tc.config, code, errw)
		}
	}
	// The readable shapes, each admitting the same call. The last names no
	// key twice at the TOP level: a repeat inside a value nobody reads is not
	// this file's floor or version.
	for _, config := range []string{memV1Floor4, memV2Auto, `{"version":2,"floor_gb":4}`, `{"version":2,"floor":"auto","floor_gb":0}`, `{"version":2,"floor":null,"floor_gb":4}`,
		`{"version":2,"note":{"floor_gb":1,"floor_gb":2},"floor_gb":4}`} {
		f, _ := memSlotOn(t, 32, 90, config)
		if reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "go test ./x", "ok", nil)); denied {
			t.Errorf("%s is readable: %s", config, reason)
		}
	}
}

// What `buddy mem init` writes, path by path. A bare init never moves a config
// it can read (an operator's version-1 slot stays D-238's across an upgrade);
// --floor-gb is the operator asking for the fitted slot; a value that is not a
// positive number or `auto` writes nothing.
func TestMemInitPaths(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()
	fresh := func(total float64) *fixture {
		f := newFixture(t)
		f.env["HOME"] = t.TempDir()
		f.mem = func() (memReading, error) { return memReading{TotalGB: total, Level: 90, Pressure: 1}, nil }
		return f
	}
	for _, tc := range []struct {
		name   string
		before string // "" = no config
		args   []string
		after  string
		says   []string
	}{
		{"no config, bare", "", nil, memV2Auto, []string{"memory slot turned ON: floor 2.7 GB", "rule: floor 2.7 GB, 1/6 of this machine's 16.0 GB; one start books at most 5.3 GB, 1/3 of this machine's 16.0 GB (config version 2)"}},
		{"no config, a floor", "", []string{"--floor-gb", "4"}, `{"version":2,"floor_gb":4}`, []string{"turned ON: floor 4.0 GB", "rule: floor 4.0 GB, explicit (--floor-gb); one start books at most 5.3 GB"}},
		{"no config, auto", "", []string{"--floor-gb", "auto"}, memV2Auto, []string{"turned ON: floor 2.7 GB"}},
		{"no config, a fraction", "", []string{"--floor-gb=0.5"}, `{"version":2,"floor_gb":0.5}`, []string{"floor 0.5 GB"}},
		{"version 1, bare: kept", memV1Floor4, nil, memV1Floor4, []string{"already on: floor 4.0 GB", "rule: floor 4.0 GB, explicit (--floor-gb); costs by the table, uncapped (config version 1)",
			"kept as it was. Fitted to this machine it would be: floor 2.7 GB, 1/6 of this machine's 16.0 GB; one start books at most 5.3 GB", "`buddy mem init --floor-gb auto`"}},
		{"version 1, --reset: kept", memV1Floor4, []string{"--reset"}, memV1Floor4, []string{"already on: floor 4.0 GB", "every booking and place in line was cleared"}},
		{"version 1, auto: fitted", memV1Floor4, []string{"--floor-gb", "auto"}, memV2Auto, []string{"already on: floor 2.7 GB", "one start books at most 5.3 GB"}},
		{"version 1, the same floor again: fitted, capped", memV1Floor4, []string{"--floor-gb", "4"}, `{"version":2,"floor_gb":4}`, []string{"already on: floor 4.0 GB", "one start books at most 5.3 GB"}},
		{"version 2 auto, bare: kept", memV2Auto, nil, memV2Auto, []string{"already on: floor 2.7 GB"}},
		{"version 2 auto, --reset: kept", memV2Auto, []string{"--reset"}, memV2Auto, []string{"cleared"}},
		{"version 2 explicit, bare: kept", `{"version":2,"floor_gb":3}`, nil, `{"version":2,"floor_gb":3}`, []string{"already on: floor 3.0 GB"}},
		{"version 2 explicit, auto", `{"version":2,"floor_gb":3}`, []string{"--floor-gb", "auto"}, memV2Auto, []string{"floor 2.7 GB"}},
		{"unreadable, --reset: fresh, and said", `{"version":1,"floor_gb":12,"floor":7}`, []string{"--reset"}, memV2Auto,
			[]string{"turned ON: floor 2.7 GB", "the config that was there could not be read (", "does not parse", "and was replaced by this one (--reset)"}},
		{"unreadable, --reset and a floor", `{trunc`, []string{"--reset", "--floor-gb", "6"}, `{"version":2,"floor_gb":6}`, []string{"floor 6.0 GB", "was replaced"}},
		{"a newer version, --reset", `{"version":9,"floor_gb":4}`, []string{"--reset"}, memV2Auto, []string{"upgrade this binary", "was replaced"}},
	} {
		f := fresh(16)
		if tc.before != "" {
			os.MkdirAll(memDir(f), 0o700)
			os.WriteFile(filepath.Join(memDir(f), "config.json"), []byte(tc.before), 0o600)
			os.WriteFile(filepath.Join(memDir(f), "bookings.json"), []byte(`{"version":1,"seq":1,"rows":[{"owner":"o","call":"c","kind":"lane","gb":1,"t":1,"until":99999999999,"seq":1}]}`), 0o600)
		}
		out, errw, code := f.run(t, dir, "", append([]string{"mem", "init"}, tc.args...)...)
		if code != 0 {
			t.Errorf("%s: init exits 0: %d %q", tc.name, code, errw)
			continue
		}
		if got := memConfigBytes(t, f); got != tc.after {
			t.Errorf("%s: config is %s, want %s", tc.name, got, tc.after)
		}
		for _, want := range tc.says {
			if !strings.Contains(out, want) {
				t.Errorf("%s: init says %q: %q", tc.name, want, out)
			}
		}
		if hinted := strings.Contains(out, "kept as it was"); hinted != (tc.before == memV1Floor4 && tc.after == memV1Floor4) {
			t.Errorf("%s: only a kept version-1 config is told what fitting would give: %q", tc.name, out)
		}
		if tc.before != "" {
			wantRows := 1
			if slicesContains(tc.args, "--reset") {
				wantRows = 0
			}
			if n := len(memRows(t, f)); n != wantRows {
				t.Errorf("%s: %d bookings left, want %d (only --reset clears them)", tc.name, n, wantRows)
			}
		}
	}
	// Refused, and nothing written: over no config and over an existing one.
	for _, bad := range []string{"0", "-1", "-0.5", "NaN", "Inf", "+Inf", "1e999", "four", "", "AUTO", "auto "} {
		f := fresh(16)
		_, errw, code := f.run(t, dir, "", "mem", "init", "--floor-gb="+bad)
		if code == 0 || !strings.Contains(errw, "refused: ") {
			t.Errorf("--floor-gb=%q is refused: %d %q", bad, code, errw)
		}
		if _, err := os.Stat(filepath.Join(f.env["HOME"], ".buddy")); err == nil {
			t.Errorf("--floor-gb=%q: a refused init writes nothing", bad)
		}
		g, _ := memSlotOn(t, 16, 90, memV1Floor4)
		if _, _, code := g.run(t, dir, "", "mem", "init", "--reset", "--floor-gb="+bad); code == 0 || memConfigBytes(t, g) != memV1Floor4 {
			t.Errorf("--floor-gb=%q over a config: refused, the config untouched: %d %s", bad, code, memConfigBytes(t, g))
		}
	}
	// A config that cannot be read is not rewritten without --reset.
	f, _ := memSlotOn(t, 16, 90, `{"version":2,"floor":"AUTO"}`)
	for _, args := range [][]string{{"mem", "init"}, {"mem", "init", "--floor-gb", "auto"}} {
		if _, errw, code := f.run(t, dir, "", args...); code == 0 || !strings.Contains(errw, "(--reset rewrites it)") || memConfigBytes(t, f) != `{"version":2,"floor":"AUTO"}` {
			t.Errorf("%v over an unreadable config is refused and leaves it: %d %q %s", args, code, errw, memConfigBytes(t, f))
		}
	}
	// Every init is logged with what it wrote: the version, the floor's word
	// or figure, and whether it reset. One line each, read back whole.
	for _, tc := range []struct {
		before string
		args   []string
		want   string
	}{
		{"", nil, "init version=2 floor=auto reset=false"},
		{memV1Floor4, nil, "init version=1 floor=4.0 reset=false"},
		{memV1Floor4, []string{"--reset"}, "init version=1 floor=4.0 reset=true"},
		{`{trunc`, []string{"--reset", "--floor-gb", "6"}, "init version=2 floor=6.0 reset=true"},
	} {
		h := fresh(16)
		if tc.before != "" {
			os.MkdirAll(memDir(h), 0o700)
			os.WriteFile(filepath.Join(memDir(h), "config.json"), []byte(tc.before), 0o600)
		}
		h.run(t, dir, "", append([]string{"mem", "init"}, tc.args...)...)
		log, _ := os.ReadFile(filepath.Join(memDir(h), "overrides.log"))
		if lines := strings.Split(strings.TrimSpace(string(log)), "\n"); len(lines) != 1 || !strings.Contains(lines[0], "="+tc.want+" :: buddy mem init") {
			t.Errorf("init %v over %q is logged as %q, one line: %q", tc.args, tc.before, tc.want, log)
		}
	}
}

// `init --reset` writes only under the slot's lock, and in an order that
// leaves no floor changed without a word. It used to rename the new config
// into place and THEN take the lock: with the lock held past 2 s (a judge
// hung in its reader holds it) the floor had changed, the command exited 1
// naming only the lock, and nothing was said or logged (Codex code pass,
// Fable cross-check). The control: the same command once the lock is let go.
func TestMemInitResetWritesOnlyUnderTheLock(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()
	const book = `{"version":1,"seq":1,"rows":[{"owner":"o","call":"c","kind":"lane","gb":1,"t":1,"until":99999999999,"seq":1}]}`
	bytesOf := func(f *fixture, name string) string {
		b, err := os.ReadFile(filepath.Join(memDir(f), name))
		if err != nil {
			return "(" + err.Error() + ")"
		}
		return string(b)
	}
	f, _ := memSlotOn(t, 16, 90, `{trunc`)
	os.WriteFile(filepath.Join(memDir(f), "bookings.json"), []byte(book), 0o600)
	lk, err := memLock(memDir(f), 0)
	if err != nil {
		t.Fatal(err)
	}
	_, errw, code := f.run(t, dir, "", "mem", "init", "--reset", "--floor-gb", "6")
	if code == 0 || !strings.Contains(errw, "was held for over 2s") || !strings.Contains(errw, "nothing was written") {
		t.Errorf("with the lock held, init --reset is refused and says nothing was written: %d %q", code, errw)
	}
	if got := memConfigBytes(t, f); got != `{trunc` {
		t.Errorf("the config is untouched: %s", got)
	}
	if got := bytesOf(f, "bookings.json"); got != book {
		t.Errorf("the bookings are untouched: %s", got)
	}
	if _, err := os.Stat(filepath.Join(memDir(f), "overrides.log")); err == nil {
		t.Errorf("nothing is logged for an init that wrote nothing: %s", bytesOf(f, "overrides.log"))
	}
	memUnlock(lk)
	if out, errw, code := f.run(t, dir, "", "mem", "init", "--reset", "--floor-gb", "6"); code != 0 || !strings.Contains(out, "was replaced") {
		t.Fatalf("control: the lock let go, the same init succeeds: %d %q %q", code, out, errw)
	}
	if got, rows := memConfigBytes(t, f), memRows(t, f); got != `{"version":2,"floor_gb":6}` || len(rows) != 0 {
		t.Errorf("control: the config is the new one and the bookings are cleared: %s %+v", got, rows)
	}
	if log := bytesOf(f, "overrides.log"); !strings.Contains(log, "init version=2 floor=6.0 reset=true") {
		t.Errorf("control: and it is logged: %q", log)
	}

	// The bookings cannot be cleared (a directory sits where the file goes):
	// nothing is written, the config least of all.
	g, _ := memSlotOn(t, 16, 90, memV1Floor4)
	os.MkdirAll(filepath.Join(memDir(g), "bookings.json", "x"), 0o700)
	if _, errw, code := g.run(t, dir, "", "mem", "init", "--reset", "--floor-gb", "auto"); code == 0 || !strings.Contains(errw, "the bookings could not be cleared") || !strings.Contains(errw, "nothing was written") {
		t.Errorf("a clear that fails stops the init: %d %q", code, errw)
	}
	if got := memConfigBytes(t, g); got != memV1Floor4 {
		t.Errorf("and the version-1 config is still the operator's: %s", got)
	}
	// The config cannot be written after the clear (a directory sits where it
	// goes): the error says the bookings went, and that the config did not.
	h, _ := memSlotOn(t, 16, 90, memV1Floor4)
	os.WriteFile(filepath.Join(memDir(h), "bookings.json"), []byte(book), 0o600)
	os.Remove(filepath.Join(memDir(h), "config.json"))
	os.MkdirAll(filepath.Join(memDir(h), "config.json", "x"), 0o700)
	if _, errw, code := h.run(t, dir, "", "mem", "init", "--reset"); code == 0 || !strings.Contains(errw, "every booking was cleared (--reset), and the config could not be written, so it is what it was") {
		t.Errorf("a config write that fails after the clear says both: %d %q", code, errw)
	}
	if rows := memRows(t, h); len(rows) != 0 {
		t.Errorf("and the bookings were cleared, as it says: %+v", rows)
	}
	// Without --reset no lock is taken and nothing is cleared: a bare init
	// over a readable config goes through while the lock is held.
	k, _ := memSlotOn(t, 16, 90, memV1Floor4)
	os.WriteFile(filepath.Join(memDir(k), "bookings.json"), []byte(book), 0o600)
	lk, err = memLock(memDir(k), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer memUnlock(lk)
	if _, errw, code := k.run(t, dir, "", "mem", "init", "--floor-gb", "auto"); code != 0 || memConfigBytes(t, k) != memV2Auto || bytesOf(k, "bookings.json") != book {
		t.Errorf("control: without --reset the lock is not asked for: %d %q %s", code, errw, memConfigBytes(t, k))
	}
}

func slicesContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// The gate prices a call at what its admit will BOOK. On 8 GB the table says
// a tier's admit (8) covers two package tests at once (6), and returning there
// as "light" started 5.33 GB of work behind an admit that books 2.67 (Codex
// design pass, panel lenses A and B). The controls: the same call under a
// version-1 config, where the table is the price, and the admit that does
// cover it.
func TestMemGateCoverIsJudgedAtTheCappedPrice(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()
	cmd := "buddy mem admit tier --wait 900 && { go test ./a & go test ./b & wait; }"
	f, _ := memSlotOn(t, 8, 100, memV2Auto)
	reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, cmd, "u", nil))
	if !denied || !strings.Contains(reason, "its `buddy mem admit tier` covers 2.7 GB, and the commands chained after it start 5.3 GB (gotest, gotest)") ||
		!strings.Contains(reason, "buddy mem admit gotest gotest --wait 900") {
		t.Fatalf("on 8 GB a tier's admit does not cover two package tests at once: %v %s", denied, reason)
	}
	if _, denied := gateVerdict(t, f, dir, bashHook("s", dir, "buddy mem admit gotest gotest --wait 900 && { go test ./a & go test ./b & wait; }", "v", nil)); denied {
		t.Fatal("control: the admit the refusal names covers it")
	}
	g, _ := memSlotOn(t, 8, 100, `{"version":1,"floor_gb":1}`)
	if reason, denied := gateVerdict(t, g, dir, bashHook("s", dir, cmd, "u", nil)); denied {
		t.Fatalf("control: under version 1 the table is the price, and 8 covers 6: %s", reason)
	}
	if n := len(memRows(t, f)) + len(memRows(t, g)); n != 0 {
		t.Fatalf("a self-gated call books nothing at the gate: %d rows", n)
	}
	// On 16 GB: goall's admit books 5.33 against a 6 GB pair. Refused for
	// THAT (an unreadable config or a failed reading refuses too, in other
	// words), and the admit it names passes on the same fixture.
	h, _ := memSlotOn(t, 16, 100, memV2Auto)
	reason, denied = gateVerdict(t, h, dir, bashHook("s", dir, "buddy mem admit goall && { go test ./a & go test ./b & wait; }", "w", nil))
	if !denied || !strings.Contains(reason, "its `buddy mem admit goall` covers 5.3 GB, and the commands chained after it start 6.0 GB (gotest, gotest)") {
		t.Fatalf("on 16 GB a goall admit (5.33) does not cover two package tests (6): %v %s", denied, reason)
	}
	if reason, denied := gateVerdict(t, h, dir, bashHook("s", dir, "buddy mem admit gotest gotest && { go test ./a & go test ./b & wait; }", "x", nil)); denied {
		t.Fatalf("control: on 16 GB the admit that names both covers them: %s", reason)
	}
}

// A call its admit covers AT THE CAPPED PRICE is allowed with no verdict, at
// pressure warn too: it is judged when the admit runs. By the table the admit
// under-covers (3 for 6), so the call reaches the second walk, where both are
// 2.67 GB; falling through to a verdict with nothing to book refused the very
// form the gate teaches and printed an admit with no kind (panel lenses A,
// B). The controls: the bare command at that pressure is refused, and the
// same admit under a version-1 config is refused as under-covering.
func TestMemGateCoveredAdmitIsAllowedWithoutAVerdict(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()
	cmd := "buddy mem admit gotest --wait 900 && go test ./..."
	f, m := memSlotOn(t, 8, 10, memV2Auto) // 0.8 GB free: nothing would pass a verdict
	m.Pressure = 2
	if reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, cmd, "c", nil)); denied {
		t.Fatalf("a covered admit is left to the admit: %s", reason)
	}
	if reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "go test ./...", "b", nil)); !denied || !strings.Contains(reason, "pressure is warn") {
		t.Fatalf("control: the bare command is judged and refused: %v %s", denied, reason)
	}
	g, _ := memSlotOn(t, 8, 100, `{"version":1,"floor_gb":1}`)
	if reason, denied := gateVerdict(t, g, dir, bashHook("s", dir, cmd, "c", nil)); !denied || !strings.Contains(reason, "covers 3.0 GB") {
		t.Fatalf("control: under version 1 a gotest admit under-covers a goall tail: %v %s", denied, reason)
	}
}

// The costs are capped with one reading's total and judged with the next
// one's, under the lock. A total that moved between them is refused: capped
// for 8 GB and judged at 24, a tier would book 2.67 GB where it owes 8 (Codex
// design pass). The control: the same two readings agreeing.
func TestMemGateRefusesATotalThatChangedBetweenItsReadings(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()
	f, _ := memSlotOn(t, 8, 100, memV2Auto)
	totals := []float64{8, 24}
	n := 0
	f.mem = func() (memReading, error) {
		m := memReading{TotalGB: totals[min(n, len(totals)-1)], Level: 100, Pressure: 1}
		n++
		return m, nil
	}
	reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "sh scripts/check.sh", "t", nil))
	if !denied || !strings.Contains(reason, "priced for a machine of 8.000 GB, and the memory total read 24.000 GB at its verdict") || !strings.Contains(reason, "Send the same call again") {
		t.Fatalf("a total that changed between the two readings is refused: %v %s", denied, reason)
	}
	if rows := memRows(t, f); len(rows) != 0 {
		t.Fatalf("and books nothing: %+v", rows)
	}
	// A total that FELL is refused the same way: priced at 24 GB's 8 and
	// judged on 8 GB, the tier is over what that machine's cap would book.
	totals, n = []float64{24, 8}, 0
	reason, denied = gateVerdict(t, f, dir, bashHook("s", dir, "sh scripts/check.sh", "t", nil))
	if !denied || !strings.Contains(reason, "priced for a machine of 24.000 GB, and the memory total read 8.000 GB at its verdict") {
		t.Fatalf("a total that fell between the two readings is refused: %v %s", denied, reason)
	}
	if rows := memRows(t, f); len(rows) != 0 {
		t.Fatalf("and books nothing: %+v", rows)
	}
	totals, n = []float64{24, 24}, 0
	if reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "sh scripts/check.sh", "t", nil)); denied || n != 2 {
		t.Fatalf("control: two readings that agree admit it (%d readings): %s", n, reason)
	}
	if rows := memRows(t, f); len(rows) != 1 || rows[0].GB != 8 {
		t.Fatalf("control: booked at this machine's price: %+v", rows)
	}
}

// A start no reading of the machine could admit is refused at once, in words
// that do not teach the wait, by the gate and by admit (exit 3, nothing
// queued, no sleep): its place in line would be infeasible for the whole
// wait (panel lens A). The controls: one start fewer is admitted, and a
// start that fits the machine but not this minute is still a wait.
func TestMemStartThatCanNeverFitIsNotQueued(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()
	f, m := memSlotOn(t, 8, 100, memV2Auto)
	three := "go test ./a & go test ./b & go test ./c & wait"
	reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, three, "n", nil))
	if !denied || !strings.Contains(reason, "(8.0 GB at once), and this machine can never hold that: its 8.0 GB of memory less the 1.3 GB floor is 6.7 GB") ||
		strings.Contains(reason, "--wait 900") || !strings.Contains(reason, "do not send it through `buddy mem admit`. Start them one after another, or fewer at a time.") {
		t.Fatalf("three capped starts at once on 8 GB can never fit, and the refusal does not teach the wait: %v %s", denied, reason)
	}
	if hasBreak(reason) {
		t.Errorf("the refusal is one line: %q", reason)
	}
	if reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "go test ./a & go test ./b & wait", "two", nil)); denied {
		t.Fatalf("control: two of them (5.33 GB over a 1.33 GB floor) fit an idle 8 GB machine: %s", reason)
	}
	f.run(t, dir, postHook("PostToolUse", "s", dir, "go test ./a & go test ./b & wait", "two", nil), "beat")
	var slept int
	f.sleep = func(time.Duration) { slept++ }
	_, errw, code := f.run(t, dir, "", "mem", "admit", "gotest", "gotest", "gotest", "--wait", "900")
	if code != 3 || slept != 0 || !strings.Contains(errw, "waiting will not help") || !strings.Contains(errw, "Start them one after another, or fewer at a time") || !strings.Contains(errw, "holds no place in line") {
		t.Fatalf("admit exits 3 at once: %d, %d sleeps, %q", code, slept, errw)
	}
	if rows := memRows(t, f); len(rows) != 0 {
		t.Fatalf("and holds no place: %+v", rows)
	}
	m.Level = 20 // 1.6 GB free: two fit the machine, not this minute
	if _, errw, code := f.run(t, dir, "", "mem", "admit", "gotest", "gotest", "--wait", "30"); code != 3 || slept != 2 || !strings.Contains(errw, "still refused after 30s") {
		t.Fatalf("control: what fits the machine waits its turn: %d, %d sleeps, %q", code, slept, errw)
	}
	// A waiter whose start stops fitting the machine while it waits (the total
	// fell) gives up its place at once: seven lanes are 7 GB, under 24 GB's 20
	// and over 8 GB's 6.67.
	g, gm := memSlotOn(t, 24, 10, memV2Auto)
	var sawPending bool
	g.sleep = func(time.Duration) {
		for _, r := range memRows(t, g) {
			sawPending = sawPending || r.Pending
		}
		gm.TotalGB = 8
	}
	if _, errw, code := g.run(t, dir, "", "mem", "admit", "lane", "lane", "lane", "lane", "lane", "lane", "lane", "--wait", "900"); code != 3 || !sawPending || !strings.Contains(errw, "waiting will not help") {
		t.Fatalf("a waiter that can no longer fit stops waiting: %d, pending seen %v, %q", code, sawPending, errw)
	}
	if rows := memRows(t, g); len(rows) != 0 {
		t.Fatalf("and leaves no place behind: %+v", rows)
	}
}

// "Never" is the verdict's own arithmetic on an idle machine, asked by the
// gate and by admit alike (neverFits), so the two cannot differ in the last
// place. Written as need > total - floor, a floor of 2.6679687500000004 GB on
// 8 GB did not call two package tests (5.33203125 GB) "never", since 8 less
// that floor rounds to 5.33203125, while every verdict left 2.66796875, under
// the floor: taught the wait, queued, and refused for the whole of it (Codex
// code pass). The control is the floor one unit in the last place lower,
// which the same call leaves exactly: admitted, by both.
func TestMemNeverFitIsTheVerdictsOwnEdge(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()
	two := "go test ./a & go test ./b & wait"
	f, _ := memSlotOn(t, 8, 100, `{"version":2,"floor_gb":2.6679687500000004}`)
	reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, two, "n", nil))
	if !denied || !strings.Contains(reason, "this machine can never hold that") || strings.Contains(reason, "--wait 900") {
		t.Fatalf("what every verdict of an idle machine refuses is told so, not taught the wait: %v %s", denied, reason)
	}
	var slept int
	f.sleep = func(time.Duration) { slept++ }
	if _, errw, code := f.run(t, dir, "", "mem", "admit", "gotest", "gotest", "--wait", "900"); code != 3 || slept != 0 || !strings.Contains(errw, "waiting will not help") {
		t.Fatalf("and admit exits 3 with no wait: %d, %d sleeps, %q", code, slept, errw)
	}
	if rows := memRows(t, f); len(rows) != 0 {
		t.Fatalf("holding no place: %+v", rows)
	}
	g, _ := memSlotOn(t, 8, 100, `{"version":2,"floor_gb":2.66796875}`)
	if reason, denied := gateVerdict(t, g, dir, bashHook("s", dir, two, "n", nil)); denied {
		t.Fatalf("control: leaving exactly the floor, the same call is admitted: %s", reason)
	}
	h, _ := memSlotOn(t, 8, 100, `{"version":2,"floor_gb":2.66796875}`)
	if _, errw, code := h.run(t, dir, "", "mem", "admit", "gotest", "gotest"); code != 0 {
		t.Fatalf("control: and admit books it: %d %q", code, errw)
	}

	// The edge the other way, on the measured machine: 24 GB, floor 4, and
	// 8 + 8 + 3 + 1 = 20 GB at once leaves exactly the floor. It fits: a
	// never-fit that fired at need == total - floor refused what an idle
	// machine admits (the mutation battery's survivor, `>=` in admit).
	four := "sh scripts/check.sh & sh scripts/check.sh & go test ./x & claude -p x & wait"
	a, _ := memSlotOn(t, 24, 100, memV2Auto)
	if reason, denied := gateVerdict(t, a, dir, bashHook("s", dir, four, "e", nil)); denied {
		t.Fatalf("20 GB at once on an idle 24 GB machine leaves the 4 GB floor and is admitted: %s", reason)
	}
	b, _ := memSlotOn(t, 24, 100, memV2Auto)
	if _, errw, code := b.run(t, dir, "", "mem", "admit", "tier", "tier", "gotest", "lane"); code != 0 || !strings.Contains(errw, "(20.0 GB)") {
		t.Fatalf("and admit books the same 20 GB: %d %q", code, errw)
	}
	// One lane more is over it: never, by both.
	c, _ := memSlotOn(t, 24, 100, memV2Auto)
	if reason, denied := gateVerdict(t, c, dir, bashHook("s", dir, "claude -p y & "+four, "o", nil)); !denied || !strings.Contains(reason, "(21.0 GB at once), and this machine can never hold that") {
		t.Fatalf("control: 21 GB at once can never fit 24 GB less 4: %v %s", denied, reason)
	}
	if _, errw, code := c.run(t, dir, "", "mem", "admit", "tier", "tier", "gotest", "lane", "lane"); code != 3 || !strings.Contains(errw, "waiting will not help") {
		t.Fatalf("control: and admit says so: %d %q", code, errw)
	}
}

// For ONE start "fewer at a time" cannot be followed: it is the floor that
// leaves no room ({"version":2,"floor_gb":8} carried to an 8 GB machine
// refuses every heavy call), so both refusals say that, and whose the floor
// is (Fable cross-check). The control is the same start under the automatic
// floor, admitted; the several-start words are held in
// TestMemStartThatCanNeverFitIsNotQueued.
func TestMemNeverFitForOneStartNamesTheFloor(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()
	f, _ := memSlotOn(t, 8, 100, `{"version":2,"floor_gb":8}`)
	reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "go test ./x", "n", nil))
	if !denied || !strings.Contains(reason, "(2.7 GB at once), and this machine can never hold that: its 8.0 GB of memory less the 8.0 GB floor is 0.0 GB") ||
		!strings.Contains(reason, "It is one start, so there is nothing to split: the floor leaves it no room on this machine, and the floor is the operator's (`buddy mem init --floor-gb <n>|auto`).") ||
		strings.Contains(reason, "fewer at a time") || strings.Contains(reason, "--wait 900") {
		t.Fatalf("one start that can never fit is told it is the floor: %v %s", denied, reason)
	}
	if hasBreak(reason) {
		t.Errorf("the refusal is one line: %q", reason)
	}
	// The sleep is a fake, counted: an admit that queued this would otherwise
	// hold the test for its real 900 s (the mutation check met exactly that).
	var slept int
	f.sleep = func(time.Duration) { slept++ }
	_, errw, code := f.run(t, dir, "", "mem", "admit", "gotest", "--wait", "900")
	if code != 3 || slept != 0 || !strings.Contains(errw, "It is one start, so there is nothing to split") || strings.Contains(errw, "fewer at a time") {
		t.Fatalf("admit says the same for one kind, at once: %d, %d sleeps, %q", code, slept, errw)
	}
	g, _ := memSlotOn(t, 8, 100, memV2Auto)
	if reason, denied := gateVerdict(t, g, dir, bashHook("s", dir, "go test ./x", "n", nil)); denied {
		t.Fatalf("control: under the automatic floor the same start is admitted: %s", reason)
	}
}

// The second walk needs a reading the override never needed. When that
// reading fails, or the reader panics, the first walk's costs stand and the
// operator's override is honored and booked as before; without an override
// the call goes on to the verdict's own reading. Under the fitted slot, on 8
// GB, where the cap would have made the tier 2.67 GB.
func TestMemGateFirstReadingFailingLeavesTheTablePrice(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()
	for _, fail := range []struct {
		name string
		fn   func() (memReading, error)
	}{
		{"an error", func() (memReading, error) { return memReading{}, fmt.Errorf("sysctl said no") }},
		{"a panic", func() (memReading, error) { panic("a reader bug") }},
	} {
		f, _ := memSlotOn(t, 8, 100, memV2Auto)
		good := f.mem
		n := 0
		f.mem = func() (memReading, error) {
			n++
			if n == 1 {
				return fail.fn()
			}
			return good()
		}
		if reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, "BUDDY_MEM_OK='operator 14:02' sh scripts/check.sh", "o", nil)); denied {
			t.Fatalf("%s: the override is honored when the pricing reading fails: %s", fail.name, reason)
		}
		if rows := memRows(t, f); len(rows) != 1 || rows[0].GB != 8 {
			t.Fatalf("%s: and booked at the table's price, the first walk's: %+v", fail.name, rows)
		}
		// No override: judged at the table's price against the verdict's
		// reading, refused as a wait (8 GB free less 8 is under the floor),
		// never as "can never fit", which only a capped price can say.
		n = 0
		reason, denied := gateVerdict(t, f, dir, bashHook("s2", dir, "sh scripts/check.sh", "t", nil))
		if !denied || !strings.Contains(reason, "this start 8.0 GB") || !strings.Contains(reason, "buddy mem admit tier --wait 900") || strings.Contains(reason, "can never hold") {
			t.Fatalf("%s: uncapped, the tier is refused as a wait: %v %s", fail.name, denied, reason)
		}
		// The control: the same call with the reader working is priced at 2.67.
		g, _ := memSlotOn(t, 8, 100, memV2Auto)
		if reason, denied := gateVerdict(t, g, dir, bashHook("s", dir, "sh scripts/check.sh", "t", nil)); denied {
			t.Fatalf("%s: control: capped, the tier fits: %s", fail.name, reason)
		}
		if rows := memRows(t, g); len(rows) != 1 || rows[0].GB != mib(2730) {
			t.Fatalf("%s: control: booked at the cap: %+v", fail.name, rows)
		}
	}
}

// priceMem, the order itself: a light call reads nothing; a version-1 or an
// unreadable config prices by the first walk and takes no reading; a second
// walk that panics leaves the first walk's costs (it is known heavy by then,
// and a panic that zeroed it would pass it unbooked: panel lens B).
func TestMemPriceOrder(t *testing.T) {
	boundedParallel(t)
	readings := 0
	envOf := func(f *fixture) Env {
		return Env{Getenv: func(k string) string { return f.env[k] }, MemReading: func() (memReading, error) {
			readings++
			return memReading{TotalGB: 8, Level: 100, Pressure: 1}, nil
		}}
	}
	f, _ := memSlotOn(t, 8, 100, memV2Auto)
	env := envOf(f)
	slot := memSlotOf(env)
	walks := 0
	counting := func(cmd string, limit float64) (memCost, *memWalker, bool) {
		walks++
		return classifyMemCapped(cmd, limit)
	}
	// Light: one walk, no reading, though the config is fitted.
	if p, light := priceMem("git commit -m 'go test ./... green'", env, slot, counting); !light || walks != 1 || readings != 0 || p.capTotal != 0 {
		t.Fatalf("a light call is one walk and no reading: light %v, %d walks, %d readings", light, walks, readings)
	}
	// Heavy under the fitted slot: two walks, one reading, capped.
	walks = 0
	p, light := priceMem("sh scripts/check.sh", env, slot, counting)
	if light || walks != 2 || readings != 1 || p.cost.total() != mib(2730) || p.capTotal != 8 || p.cfgErr != nil {
		t.Fatalf("control: a heavy call is priced twice, the second capped: light %v, %d walks, %d readings, %+v", light, walks, readings, p)
	}
	// The second walk panics (ok false): the first walk's price stands.
	walks, readings = 0, 0
	panicking := func(cmd string, limit float64) (memCost, *memWalker, bool) {
		walks++
		if limit > 0 {
			return memCost{}, &memWalker{}, false
		}
		return classifyMemCapped(cmd, limit)
	}
	p, light = priceMem("sh scripts/check.sh", env, slot, panicking)
	if light || walks != 2 || p.cost.total() != 8 || p.w.leaves != 1 || p.capTotal != 0 {
		t.Fatalf("a second walk that panicked leaves the first walk's 8 GB, uncapped: light %v, %d walks, %+v", light, walks, p)
	}
	// Version 1, and an unreadable config: one walk, no reading.
	for _, config := range []string{memV1Floor4, `{"version":2,"floor":"AUTO"}`} {
		g, _ := memSlotOn(t, 8, 100, config)
		genv := envOf(g)
		walks, readings = 0, 0
		p, light := priceMem("sh scripts/check.sh", genv, memSlotOf(genv), counting)
		if light || walks != 1 || readings != 0 || p.cost.total() != 8 || p.capTotal != 0 || (p.cfgErr != nil) != (config != memV1Floor4) {
			t.Fatalf("%s: one walk, no reading, the table's price: light %v, %d walks, %d readings, %+v", config, light, walks, readings, p)
		}
		// A LIGHT call under the same config opens no config either: under the
		// unreadable one it has no config error to show, and the heavy call
		// just above is the control that the error is there to be had (the
		// config read moved above the light return passed every count here:
		// Codex code pass).
		if p, light := priceMem("git commit -m 'go test ./... green'", genv, memSlotOf(genv), counting); !light || p.cfgErr != nil || p.cfg != (memConfig{}) {
			t.Fatalf("%s: a light call reads no config: light %v, %+v", config, light, p)
		}
	}
}

// A waiter's PENDING place holds what it would book now. Later arrivals count
// it in full, so a figure from an earlier try under another total is not what
// it is waiting for: queued at 5.33 GB on 16 GB, it is 8 GB once the machine
// reads 24. The control is the first sleep, which sees the 5.33.
func TestMemAdmitRepricesItsPendingPlace(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()
	f, m := memSlotOn(t, 16, 10, memV2Auto)
	f.env[EnvClaudeSession] = "s-wait"
	// The waiter is this test's own process, and the fixture's process table
	// is empty unless told: without this line every try's prune dropped the
	// waiter's row, try 2 queued a NEW one at the new price, and the test
	// passed with the re-price removed (the mutation battery's survivor).
	f.alive[os.Getpid()] = 0
	var seen []float64
	f.sleep = func(time.Duration) {
		for _, r := range memRows(t, f) {
			if r.Pending && r.Owner == "s-wait" {
				seen = append(seen, r.GB)
			}
		}
		m.TotalGB = 24
	}
	if _, errw, code := f.run(t, dir, "", "mem", "admit", "tier", "--wait", "30"); code != 3 {
		t.Fatalf("still refused at 10%% free: %d %q", code, errw)
	}
	if len(seen) != 2 || seen[0] != mib(5461) || seen[1] != 8 {
		t.Fatalf("the place was queued at 5461 MiB and re-priced to 8 GB: %v", seen)
	}
}

// Whole-MiB rounding is what makes a tail and the admit that names its kinds
// sum to the same figure in any order. RAM/3 is not a binary fraction on a 10
// GB machine, and unrounded this call was refused as under-covered though it
// admits exactly the kinds the refusal printed: covered ((0+c)+c)+1 =
// 7.666666666666667, tail ((0+c)+1)+c = 7.666666666666668 (panel lens B's
// witness). Both halves are shown: the unrounded cap reproduces it in the
// classifier, and the gate, which rounds, allows the call.
func TestMemCappedSumsAreExact(t *testing.T) {
	boundedParallel(t)
	dir := t.TempDir()
	cmd := "buddy mem admit tier goall lane --wait 900 && { go test ./... & claude -p x & sh scripts/check.sh & wait; }"
	if _, w, ok := classifyMemCapped(cmd, 10.0/3); !ok || w.under == "" {
		t.Fatalf("the witness: at an unrounded 10/3 GB cap the cover and the tail differ in the last place: %q", w.under)
	}
	limit := memConfig{Version: memConfigV2}.startCap(10)
	if limit != mib(3413) {
		t.Fatalf("10 GB caps a start at 3413 MiB: %v", limit)
	}
	if c, w, ok := classifyMemCapped(cmd, limit); !ok || w.under != "" || c.total() != 0 {
		t.Fatalf("rounded to whole MiB the admit covers its tail: %v %q", c.total(), w.under)
	}
	f, _ := memSlotOn(t, 10, 100, memV2Auto)
	if reason, denied := gateVerdict(t, f, dir, bashHook("s", dir, cmd, "x", nil)); denied {
		t.Fatalf("the gate allows the admit that names exactly its tail's kinds: %s", reason)
	}
}
