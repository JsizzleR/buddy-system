package cli

// The machine-wide memory slot (issue #144 proposal 4; D-238). A heavy start
// (whole-package or -race Go tests, a test tier, a done-check, a Codex pass, a
// lane or VM start) books its expected memory in one file every session on
// the machine shares, and a start that would leave less than the floor free
// is refused by `buddy gate` with the exact form that waits its turn.
//
// WHY. Two hard freezes in 30 minutes on one 24 GB macOS laptop (2026-11-17)
// were swap exhaustion: free memory went from 6.6 GB to 0.1 GB in one minute
// after a coordinator resumed 12 lanes 3 s apart and several re-ran
// whole-package tests, and macOS then SUSPENDED apps, Terminal among them,
// which holds every lane; suspension frees nothing. Briefs that said "check
// memory_pressure first" were followed and the start went ahead anyway:
// advice is not a gate (D-209 changed the advice; this is the gate it left
// open).
//
// WHY BOOKINGS, not a free-memory reading alone. A cold compile of the largest
// package peaked at 2.3 GB of linker RSS while free memory moved 0.3 GB
// (macOS absorbs it from file cache until the cache is gone), so lanes reading
// free memory seconds apart all see headroom and all start. A start is judged
// on  free - booked - its own cost >= floor,  taken under one lock.
//
// WHY NOT THE LEDGER. The ledger is per repository (the git common dir,
// D-001): a lane in one repository cannot see another's fleet, which is the
// whole problem (D-209 cut a ledger verb for exactly this). The home ledger
// carries a schema (a table is a schema bump, and every older copy DENIES,
// D-037) and governs paths, not a machine. So the state is a small JSON file
// under ~/.buddy/mem, rewritten whole under flock(2): tens of rows, no
// migration story beyond a version number.
//
// THE MEASURED RULES KEPT from the operator's earlier local gate (each was an
// incident there): a 4 GB floor; refuse also at kernel pressure >= 2, and on
// swap over 8 GB only with under 30% free (swap stays in use after pressure
// passes, so alone it is no signal); an admitted booking FADES linearly to 0
// over 90 s, because counting it in full double-counted (27 GB "booked"
// against 18 GB free on a healthy box refused every start: by its peak a build
// is in the free reading); a waiter holds a PENDING place that later arrivals
// count in full, and a waiter counts only the pending rows AHEAD of it
// (counting those behind deadlocked seven waiters).
//
// WHAT A REVIEW OF THIS DESIGN CHANGED (panel and Codex design pass):
//   - Rows are per CALL, never replaced by the same session's next call. The
//     local gate kept one row per (session, kind), so a backgrounded 6 GB test
//     was erased by the next foreground build, and three subagents (which run
//     under their parent's session id) booked as one. The owner is the session
//     plus the payload's agent_id. Only a Codex row, which nothing releases,
//     replaces that thread's earlier foreground row of the same kind.
//   - Detached work is never released at PostToolUse (memclassify.go).
//   - A pending row an empty machine could not admit (cost > free - floor)
//     blocks nobody: an 8 GB tier waiting on 10 GB free used to refuse every
//     3 GB start behind it for its whole wait.
//   - A pending row expires at its deadline whatever its process does (a
//     SIGSTOPped waiter cannot clean up), and is dropped once its process is
//     gone, by pid AND birth time (a reused pid is not the waiter).
//   - The file names its boot; rows from another boot are dropped, since every
//     process they describe is gone. The freeze this exists for ends in a
//     forced power-off, and a slot that came back up refusing every start
//     would block the resume recipe. Writes are fsynced before the rename.
//   - Ages are clamped to [0, TTL] and the queue is ordered by a sequence
//     number, so a clock stepped back cannot inflate a weight (a 6 GB row
//     weighed 246 GB at one hour back) or let a newcomer jump the line.
//
// FAIL MODE (the operator's choice 1, 2026-11-19 03:59Z, relayed). The slot is
// a safety device, so it fails CLOSED (invariant 2), but its jurisdiction is
// the calls it classifies HEAVY on a machine that ran `buddy mem init`:
//   - no ~/.buddy/mem/config.json (never initialized, or turned off), or no
//     HOME: OFF. Nothing is parsed or booked. Invariant 3's never-initialized
//     verdict.
//   - a light call: allowed, and the slot is never touched, the override
//     included: a broken slot cannot stop a report, a commit or a message.
//   - a heavy call while the config cannot be stat'ed (anything but "does not
//     exist"), the state cannot be read, parsed or locked, its version is
//     newer, the memory reading fails, or the booking cannot be written: that
//     call is DENIED, naming the cause. Invariant 3's "exists but unreadable".
//     Failing open, as the local gate did, lets a bookings file truncated by
//     a full disk pass every heavy start without a word.
//   - beat's release failing: silent; the row fades within 90 s.
//
// ENFORCEMENT IS COOPERATIVE (D-005). A session can delete the config, write
// garbage into the bookings, or start work through a script the classifier
// does not read. The slot is a seatbelt against bursts nobody meant, not a
// sandbox; the skills say init, --reset and the override are the operator's.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

const (
	memDirRel      = ".buddy/mem"
	memVersion     = 1
	memTTL         = 90 * time.Second
	memDefaultGB   = 4.0
	memLockWait    = 2 * time.Second
	memReleaseWait = 50 * time.Millisecond
	// memHookBudget is how long after the gate started the memory phase may
	// still wait for the lock: README's hook timeout is 10 s, and a hook killed
	// by it fails OPEN under Codex (D-107), so the phase refuses first.
	memHookBudget = 8 * time.Second
	memPoll       = 15 * time.Second
	memMaxWait    = 3600
	// memPendingSlack keeps a pending row a little past its waiter's deadline,
	// so a waiter's last judge still sees its own place.
	memPendingSlack = 30 * time.Second
	memOverrideVar  = "BUDDY_MEM_OK"
	// The thresholds beside the floor, the local gate's measured ones.
	memPressureRefuse = 2
	memSwapGB         = 8.0
	memSwapLevel      = 30
)

const usageMem = "usage: buddy mem init [--floor-gb <n>] [--reset]   (the OPERATOR's act: turn the machine-wide memory slot on, ~/.buddy/mem; --reset clears every booking)\n" +
	"       buddy mem status                    free memory, what is booked and pending, the floor\n" +
	"       buddy mem admit <kind>... [--wait <secs>]   book a heavy start before running it; with --wait, hold a place in line (exit 0 admitted, 3 still refused, 1 an error; 0 with nothing booked when the slot is off)\n" +
	"       (kinds: tier goall race vm gotest check lane codex; `buddy mem admit K --wait 900 && { <cmd>; }` is not judged again by the gate. Refused by the gate: wait that way, run_in_background)"

// memReading is one sample of the machine's memory.
type memReading struct {
	TotalGB  float64
	Level    int // kern.memorystatus_level: percent available
	Pressure int // kern.memorystatus_vm_pressure_level: 1 normal, 2 warn, 4 critical
	SwapGB   float64
}

func (r memReading) freeGB() float64 { return r.TotalGB * float64(r.Level) / 100 }

func (e Env) memReading() (memReading, error) {
	if e.MemReading != nil {
		return e.MemReading()
	}
	return readMemory()
}

type memConfig struct {
	Version int     `json:"version"`
	FloorGB float64 `json:"floor_gb"`
}

type memRow struct {
	Owner string  `json:"owner"`
	Call  string  `json:"call"`
	Kind  string  `json:"kind"`
	GB    float64 `json:"gb"`
	T     float64 `json:"t"`     // unix seconds, booked or queued
	Until float64 `json:"until"` // unix seconds: dropped at or after
	Seq   int64   `json:"seq"`
	// Pending: a waiter's place in line, counted in full; never fades.
	Pending bool `json:"pending,omitempty"`
	// Detached: never released at PostToolUse; it fades.
	Detached bool  `json:"detached,omitempty"`
	PID      int   `json:"pid,omitempty"`
	Born     int64 `json:"born,omitempty"`
}

type memBook struct {
	Version int      `json:"version"`
	Boot    string   `json:"boot,omitempty"`
	Seq     int64    `json:"seq"`
	Rows    []memRow `json:"rows"`
}

// memSlot is where the slot's files are and whether it is on. err is the
// "cannot tell" verdict: the config could not be stat'ed for a reason other
// than its absence. It denies heavy calls only.
type memSlot struct {
	dir string
	on  bool
	err error
}

func memSlotOf(env Env) memSlot {
	home := env.getenv("HOME")
	if home == "" {
		return memSlot{}
	}
	dir := filepath.Join(home, memDirRel)
	_, err := os.Stat(filepath.Join(dir, "config.json"))
	switch {
	case err == nil:
		return memSlot{dir: dir, on: true}
	case errors.Is(err, fs.ErrNotExist):
		return memSlot{dir: dir}
	default:
		return memSlot{dir: dir, on: true, err: fmt.Errorf("cannot read %s: %w", filepath.Join(dir, "config.json"), err)}
	}
}

func readMemConfig(dir string) (memConfig, error) {
	var c memConfig
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s does not parse (%v)", filepath.Join(dir, "config.json"), err)
	}
	if c.Version != memVersion {
		return c, fmt.Errorf("%s is version %d and this buddy reads %d: upgrade this binary (sh scripts/install.sh)", filepath.Join(dir, "config.json"), c.Version, memVersion)
	}
	if c.FloorGB <= 0 || math.IsNaN(c.FloorGB) || math.IsInf(c.FloorGB, 0) {
		return c, fmt.Errorf("%s has no usable floor_gb", filepath.Join(dir, "config.json"))
	}
	return c, nil
}

func readMemBook(dir string) (memBook, error) {
	b, err := os.ReadFile(filepath.Join(dir, "bookings.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return memBook{Version: memVersion}, nil
	}
	if err != nil {
		return memBook{}, err
	}
	var book memBook
	if err := json.Unmarshal(b, &book); err != nil {
		return memBook{}, fmt.Errorf("%s does not parse (%v)", filepath.Join(dir, "bookings.json"), err)
	}
	if book.Version != memVersion {
		return memBook{}, fmt.Errorf("%s is version %d and this buddy reads %d: upgrade this binary (sh scripts/install.sh)", filepath.Join(dir, "bookings.json"), book.Version, memVersion)
	}
	return book, nil
}

// writeMemBook replaces the bookings through a synced temporary file, so a
// forced power-off leaves the old file or the new one, never a torn one.
func writeMemBook(dir string, book memBook) error {
	book.Version = memVersion
	data, err := json.Marshal(book)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".bookings-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	if werr == nil {
		werr = tmp.Sync()
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(name, filepath.Join(dir, "bookings.json"))
	}
	if werr != nil {
		os.Remove(name)
	}
	return werr
}

// memLock takes the slot's exclusive lock, retrying until wait has passed.
// flock(2) is per open file description, so two opens in one process contend
// as two processes do (the -race test relies on it).
func memLock(dir string, wait time.Duration) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, err
		}
		if !time.Now().Before(deadline) {
			f.Close()
			return nil, fmt.Errorf("the memory slot's lock %s was held for over %s", filepath.Join(dir, "lock"), wait)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func memUnlock(f *os.File) {
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck // Close drops it too
	f.Close()
}

func unixSec(t time.Time) float64 { return float64(t.UnixNano()) / 1e9 }

// prune drops what can no longer matter: rows past their until, rows from
// another boot, and pending rows whose waiter is gone.
func (b *memBook) prune(env Env, now time.Time) {
	boot, ok := env.bootID()
	if ok && b.Boot != "" && b.Boot != boot {
		b.Rows = nil
	}
	if ok {
		b.Boot = boot
	}
	t := unixSec(now)
	kept := b.Rows[:0]
	for _, r := range b.Rows {
		if t >= r.Until {
			continue
		}
		if r.Pending && r.PID > 0 && !env.procAlive(store.ProcRef{PID: r.PID, Born: r.Born}) {
			continue
		}
		kept = append(kept, r)
	}
	b.Rows = kept
}

func memWeight(r memRow, now time.Time) float64 {
	if r.Pending {
		return r.GB
	}
	age := math.Min(math.Max(unixSec(now)-r.T, 0), memTTL.Seconds())
	return r.GB * (1 - age/memTTL.Seconds())
}

// booked is the GB a judge counts. A newcomer (me == nil) counts every
// admitted row and every feasible pending row; a waiter counts admitted rows
// and the feasible pending rows ahead of it in line. Feasible: an otherwise
// empty machine could admit it (its cost fits above the floor).
func (b *memBook) booked(me *memRow, now time.Time, free, floor float64) float64 {
	sum := 0.0
	for _, r := range b.Rows {
		if me != nil && r.Owner == me.Owner && r.Call == me.Call {
			continue
		}
		if r.Pending {
			if r.GB > free-floor {
				continue
			}
			if me != nil && r.Seq >= me.Seq {
				continue
			}
		}
		sum += memWeight(r, now)
	}
	return sum
}

// memVerdict is why a start of need GB cannot be admitted now (none: it can),
// and what would be left.
func memVerdict(m memReading, floor, booked, need float64) ([]string, float64) {
	var why []string
	if m.Pressure >= memPressureRefuse {
		level := "warn"
		if m.Pressure >= 4 {
			level = "critical"
		}
		why = append(why, fmt.Sprintf("kernel memory pressure is %s (level %d)", level, m.Pressure))
	}
	if m.SwapGB > memSwapGB && m.Level < memSwapLevel {
		why = append(why, fmt.Sprintf("swap in use %s GB with only %d%% free", gb(m.SwapGB), m.Level))
	}
	left := m.freeGB() - booked - need
	if left < floor {
		why = append(why, fmt.Sprintf("free %s GB - booked %s GB - this start %s GB = %s GB, under the %s GB floor",
			gb(m.freeGB()), gb(booked), gb(need), gb(left), gb(floor)))
	}
	return why, left
}

// ---- the gate's half ----

// memHookCall is what the gate needs from one PreToolUse or PostToolUse
// payload: who, which call, and whether it was backgrounded by the tool.
type memHookCall struct {
	owner, call string
	background  bool
	codex       bool
}

func memCallOf(h hookInput) memHookCall {
	owner := h.SessionID
	if h.AgentID != "" {
		owner += "/" + h.AgentID
	}
	var id string
	if json.Unmarshal(h.ToolUseID, &id) != nil || id == "" {
		sum := sha256.Sum256([]byte(h.ToolInput.Command))
		id = "cmd:" + hex.EncodeToString(sum[:8])
	}
	var bg bool
	_ = json.Unmarshal(h.ToolInput.RunInBackground, &bg) // raw: any other shape is "not backgrounded"
	return memHookCall{owner: owner, call: id, background: bg, codex: h.Harness == "codex"}
}

// memIsShell: the tool call carries a shell command the slot reads.
func memIsShell(h hookInput) bool {
	return h.ToolName != "apply_patch" && h.ToolInput.Command != ""
}

// gateMemory judges one shell call the claims phase allowed. It returns the
// refusal, or "" to allow; it writes nothing on an allow (a
// permissionDecision there would skip the user's permission prompt).
func gateMemory(h hookInput, env Env, slot memSlot, started time.Time) string {
	if !memIsShell(h) {
		return ""
	}
	cost, w := classifyMem(h.ToolInput.Command)
	if cost.total() == 0 && w.under == "" {
		return "" // light: the slot is not consulted, its state not read
	}
	kinds := cost.kinds()
	refuse := func(why string) string {
		return fmt.Sprintf("MEMORY GATE: nothing in this call ran. It starts %s, and the machine-wide memory slot cannot judge it: %s. The slot refuses heavy starts while it cannot read its own state (light commands are not affected). Ask the operator; `buddy mem status` shows the cause.",
			fence.Line(strings.Join(kinds, ", "), 256), fence.Line(why, 1024))
	}
	if slot.err != nil {
		return refuse(slot.err.Error())
	}
	cfg, err := readMemConfig(slot.dir)
	if err != nil {
		return refuse(err.Error())
	}
	if w.under != "" {
		return "MEMORY GATE: nothing in this call ran: " + fence.Line(w.under, 1024) + "."
	}
	call := memCallOf(h)
	now := nowOf(env)
	if w.override != "" {
		if err := logMemOverride(slot.dir, call.owner, w.override, h.ToolInput.Command, now); err == nil {
			// The operator's word skips the verdict, never the booking: an
			// overridden tier is still 8 GB the next judge must see (panel
			// lens C). A booking that fails here is not a refusal: the
			// override was asked for, and it is logged.
			if lk, err := memLock(slot.dir, memLockWait); err == nil {
				if book, err := readMemBook(slot.dir); err == nil {
					book.prune(env, now)
					bookCall(&book, call, cost, now)
					_ = writeMemBook(slot.dir, book)
				}
				memUnlock(lk)
			}
			return ""
		}
		// An override that cannot be logged is not honored: judged normally.
	}
	wait := min(memLockWait, memHookBudget-time.Since(started))
	if wait <= 0 {
		return refuse("the gate ran out of hook time before it could take the slot's lock")
	}
	lk, err := memLock(slot.dir, wait)
	if err != nil {
		return refuse(err.Error())
	}
	defer memUnlock(lk)
	book, err := readMemBook(slot.dir)
	if err != nil {
		return refuse(err.Error())
	}
	m, err := env.memReading()
	if err != nil {
		return refuse("no memory reading: " + err.Error())
	}
	book.prune(env, now)
	// The rows this call's booking replaces are not counted against it (Codex
	// code pass): a Codex re-run of `go test ./...` 5 s after the last one
	// was judged against that run's 5.7 GB still on the books, and refused.
	// Nothing is written on a refusal, so they stay booked then.
	dropReplaced(&book, call, cost)
	need := cost.total()
	why, _ := memVerdict(m, cfg.FloorGB, book.booked(nil, now, m.freeGB(), cfg.FloorGB), need)
	if len(why) > 0 {
		return fmt.Sprintf("MEMORY GATE: nothing in this call ran. It starts %s (%s GB) and the machine cannot hold that now: %s. Two hard freezes came from starts that each looked fine, landing together. To wait your turn (it holds your place, so smaller starts cannot starve you), send `buddy mem admit %s --wait 900 && { <the same command>; }` and run it with run_in_background (the braces keep every statement of it behind the admit). Or lower the cost: one package, not ./...; no -race until the plain run is green. `buddy mem status` shows what is booked. Do not retry the bare command in a loop; an override is the operator's word only.",
			fence.Line(strings.Join(kinds, ", "), 256), gb(need), fence.Line(strings.Join(why, "; "), 1024), fence.Line(strings.Join(kinds, " "), 256))
	}
	bookCall(&book, call, cost, now)
	if err := writeMemBook(slot.dir, book); err != nil {
		return refuse("the booking could not be written: " + err.Error())
	}
	return ""
}

// bookCall adds one call's rows: its foreground part (released when the call
// ends, unless the tool backgrounded it) and its detached part (fades).
func bookCall(book *memBook, call memHookCall, cost memCost, now time.Time) {
	dropReplaced(book, call, cost)
	add := func(suffix string, gbs float64, kinds []string, detached bool) {
		if gbs <= 0 {
			return
		}
		book.Seq++
		book.Rows = append(book.Rows, memRow{
			Owner: call.owner, Call: call.call + suffix, Kind: memKindLabel(kinds), GB: gbs,
			T: unixSec(now), Until: unixSec(now.Add(memTTL)), Seq: book.Seq, Detached: detached,
		})
	}
	add("", cost.fg, cost.fgKinds, call.background)
	add("#detached", cost.det, cost.detKinds, true)
}

// dropReplaced drops the rows one call's booking takes the place of: its own
// rows (the same call judged again), and under Codex, whose rows nothing
// releases, that thread's last foreground row of the same kind, so a
// fix-test loop replaces rather than stacks.
func dropReplaced(book *memBook, call memHookCall, cost memCost) {
	kept := book.Rows[:0]
	for _, r := range book.Rows {
		if r.Owner == call.owner && (r.Call == call.call || r.Call == call.call+"#detached") {
			continue
		}
		if call.codex && !r.Pending && !r.Detached && r.Owner == call.owner && r.Kind == memKindLabel(cost.fgKinds) {
			continue
		}
		kept = append(kept, r)
	}
	book.Rows = kept
}

func memKindLabel(kinds []string) string {
	if len(kinds) == 0 {
		return ""
	}
	s := append([]string(nil), kinds...)
	sort.SliceStable(s, func(i, j int) bool { return memKindCost[s[i]] > memKindCost[s[j]] })
	if len(s) > memKindsShown {
		s = append(s[:memKindsShown], "...")
	}
	return strings.Join(s, "+")
}

func logMemOverride(dir, owner, reason, cmd string, now time.Time) error {
	f, err := os.OpenFile(filepath.Join(dir, "overrides.log"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	line := fmt.Sprintf("%s %s %s=%s :: %s\n", now.UTC().Format(time.RFC3339), fence.Line(owner, 160),
		memOverrideVar, fence.Line(reason, 160), fence.Line(cmd, 240))
	_, werr := f.WriteString(line)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// releaseMem is beat's half: a foreground call that ended drops its row. Only
// for a shell call, only when the slot is on, and cheaply: an unlocked read
// first, and the lock only when a row of this call is there, for 50 ms at
// most. Every failure is silent; the row fades.
func releaseMem(h hookInput, env Env) {
	if !memIsShell(h) {
		return
	}
	slot := memSlotOf(env)
	if !slot.on || slot.err != nil {
		return
	}
	call := memCallOf(h)
	if call.background {
		return
	}
	match := func(b memBook) bool {
		for _, r := range b.Rows {
			if r.Owner == call.owner && r.Call == call.call && !r.Detached && !r.Pending {
				return true
			}
		}
		return false
	}
	if book, err := readMemBook(slot.dir); err != nil || !match(book) {
		return
	}
	lk, err := memLock(slot.dir, memReleaseWait)
	if err != nil {
		return
	}
	defer memUnlock(lk)
	book, err := readMemBook(slot.dir)
	if err != nil {
		return
	}
	kept := book.Rows[:0]
	for _, r := range book.Rows {
		if r.Owner == call.owner && r.Call == call.call && !r.Detached && !r.Pending {
			continue
		}
		kept = append(kept, r)
	}
	book.Rows = kept
	_ = writeMemBook(slot.dir, book)
}

// ---- the verb ----

func cmdMem(args []string, env Env) error {
	if len(args) == 0 {
		return errors.New(usageMem)
	}
	switch args[0] {
	case "init":
		return memInit(args[1:], env)
	case "status":
		if len(args) > 1 {
			if isHelp(args[1]) {
				fmt.Fprintln(env.Stdout, usageMem)
				return nil
			}
			return usageRefusal("mem status takes no arguments", usageMem)
		}
		return memStatus(env)
	case "admit":
		return memAdmit(args[1:], env)
	}
	return usageRefusal(fmt.Sprintf("unknown mem subcommand %s", strconv.Quote(fence.Line(args[0], 64))), usageMem)
}

func memInit(args []string, env Env) error {
	fset := flag.NewFlagSet("mem init", flag.ContinueOnError)
	floor := fset.Float64("floor-gb", 0, "the floor")
	reset := fset.Bool("reset", false, "clear every booking")
	if help, err := parseFlags(fset, args, usageMem, env); help || err != nil {
		return err
	}
	if fset.NArg() > 0 {
		return usageRefusal(fmt.Sprintf("mem init: unexpected argument %s", strconv.Quote(fence.Line(fset.Arg(0), 64))), usageMem)
	}
	home := env.getenv("HOME")
	if home == "" {
		return errors.New("mem init: HOME is not set, so there is no ~/.buddy/mem")
	}
	if *floor < 0 || math.IsNaN(*floor) || math.IsInf(*floor, 0) {
		return errors.New("mem init: --floor-gb must be a positive number")
	}
	m, err := env.memReading()
	if err != nil {
		return fmt.Errorf("mem init: no memory reading on this machine (%v), so the slot cannot judge anything; nothing was written", err)
	}
	dir := filepath.Join(home, memDirRel)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	cfg, err := readMemConfig(dir)
	was := "turned ON"
	if err == nil {
		was = "already on"
	} else if !errors.Is(err, fs.ErrNotExist) && !*reset {
		return fmt.Errorf("mem init: %v (--reset rewrites it)", err)
	}
	if cfg.FloorGB <= 0 || err != nil {
		cfg.FloorGB = memDefaultGB
	}
	if *floor > 0 {
		cfg.FloorGB = *floor
	}
	cfg.Version = memVersion
	data, _ := json.Marshal(cfg)
	tmp := filepath.Join(dir, ".config.json.tmp")
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, "config.json")); err != nil {
		return err
	}
	if *reset {
		lk, err := memLock(dir, memLockWait)
		if err != nil {
			return err
		}
		err = writeMemBook(dir, memBook{})
		memUnlock(lk)
		if err != nil {
			return err
		}
	}
	who, _, _ := nativeSession(env)
	_ = logMemOverride(dir, firstNonEmpty(who, "a terminal"), fmt.Sprintf("init floor_gb=%s reset=%t", gb(cfg.FloorGB), *reset), "buddy mem init", nowOf(env))
	fmt.Fprintf(env.Stdout, "memory slot %s: floor %s GB, %s (free %s GB now). Heavy Bash starts are judged by buddy gate on every session of this machine; delete %s to turn it off.\n",
		was, gb(cfg.FloorGB), dir, gb(m.freeGB()), filepath.Join(dir, "config.json"))
	if *reset {
		fmt.Fprintln(env.Stdout, "every booking and place in line was cleared (--reset)")
	}
	return nil
}

func memStatus(env Env) error {
	slot := memSlotOf(env)
	if !slot.on {
		fmt.Fprintln(env.Stdout, "memory slot: OFF (no ~/.buddy/mem/config.json; `buddy mem init` turns it on, the operator's act)")
		return nil
	}
	if slot.err != nil {
		return slot.err
	}
	cfg, err := readMemConfig(slot.dir)
	if err != nil {
		return err
	}
	lk, err := memLock(slot.dir, memLockWait)
	if err != nil {
		return err
	}
	book, berr := readMemBook(slot.dir)
	memUnlock(lk) // printed after the lock is let go: a paged status must not stall every judge
	if berr != nil {
		return berr
	}
	now := nowOf(env)
	book.prune(env, now)
	m, merr := env.memReading()
	if merr != nil {
		fmt.Fprintf(env.Stdout, "memory slot: ON, floor %s GB; no memory reading (%s): heavy starts are refused\n", gb(cfg.FloorGB), fence.Line(merr.Error(), 256))
	} else {
		fmt.Fprintf(env.Stdout, "memory slot: ON  free %s GB (%d%%)  booked %s GB  floor %s GB  pressure %d  swap %s GB\n",
			gb(m.freeGB()), m.Level, gb(book.booked(nil, now, m.freeGB(), cfg.FloorGB)), gb(cfg.FloorGB), m.Pressure, gb(m.SwapGB))
	}
	for _, r := range book.Rows {
		state := "booked"
		switch {
		case r.Pending:
			state = "PENDING"
		case r.Detached:
			state = "fading"
		}
		age := int(math.Max(unixSec(now)-r.T, 0))
		fmt.Fprintf(env.Stdout, "  %5ds ago  %5s GB  %-7s  %-24s  %s\n", age, gb(memWeight(r, now)), state,
			fence.Field(r.Kind, 24), fence.Field(r.Owner, 64))
	}
	return nil
}

// memAdmit books one heavy start for a script or a refused session: judged
// now, and with --wait, holding a PENDING place re-judged every 15 s. It is a
// typed verb, so the gate never judges it; what it books fades.
func memAdmit(args []string, env Env) error {
	var kinds []string
	for len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		if _, ok := memKindCost[args[0]]; !ok {
			return usageRefusal(fmt.Sprintf("mem admit: unknown kind %s", strconv.Quote(fence.Line(args[0], 64))), usageMem)
		}
		kinds = append(kinds, args[0])
		args = args[1:]
	}
	fset := flag.NewFlagSet("mem admit", flag.ContinueOnError)
	wait := fset.Int("wait", 0, "seconds to hold a place in line")
	if help, err := parseFlags(fset, args, usageMem, env); help || err != nil {
		return err
	}
	if fset.NArg() > 0 {
		return usageRefusal(fmt.Sprintf("mem admit: unexpected argument %s (kinds first, then --wait)", strconv.Quote(fence.Line(fset.Arg(0), 64))), usageMem)
	}
	if len(kinds) == 0 {
		return usageRefusal("mem admit: name at least one kind", usageMem)
	}
	if *wait < 0 || *wait > memMaxWait {
		return usageRefusal(fmt.Sprintf("mem admit: --wait is 0 to %d seconds", memMaxWait), usageMem)
	}
	slot := memSlotOf(env)
	if !slot.on {
		fmt.Fprintln(env.Stderr, "buddy mem admit: the memory slot is off on this machine; nothing booked")
		return nil
	}
	if slot.err != nil {
		return slot.err
	}
	if _, err := readMemConfig(slot.dir); err != nil {
		return err // before any wait: each try reads it again (admitOnce)
	}
	need := 0.0
	for _, k := range kinds {
		need += memKindCost[k]
	}
	owner, _, _ := nativeSession(env)
	if owner == "" {
		owner = fmt.Sprintf("pid%d", os.Getppid())
	}
	pid := os.Getpid()
	call := fmt.Sprintf("admit-%d", pid)
	label := memKindLabel(kinds)
	sigs := make(chan os.Signal, 1)
	if env.Sleep == nil {
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
		defer signal.Stop(sigs)
	}
	start := nowOf(env)
	deadline := start.Add(time.Duration(*wait) * time.Second)
	queued := false
	defer func() {
		if queued { // a waiter that ends, however, leaves no place behind
			if lk, err := memLock(slot.dir, memLockWait); err == nil {
				if book, err := readMemBook(slot.dir); err == nil {
					kept := book.Rows[:0]
					for _, r := range book.Rows {
						if !(r.Pending && r.Owner == owner && r.Call == call) {
							kept = append(kept, r)
						}
					}
					book.Rows = kept
					_ = writeMemBook(slot.dir, book)
				}
				memUnlock(lk)
			}
		}
	}()
	for attempt := 0; ; attempt++ {
		ok, msg, err := admitOnce(env, slot.dir, owner, call, label, need, pid, deadline, *wait > 0)
		if err != nil {
			return err
		}
		fmt.Fprintln(env.Stderr, "buddy mem admit: "+msg)
		if ok {
			queued = false
			return nil
		}
		queued = *wait > 0
		// Tries after the first: one per poll, rounded UP, the last sleeping
		// only what is left (Fable cross-check: `--wait 10` divided to zero
		// tries and exited at once, saying it had waited 10 s).
		poll := int(memPoll.Seconds())
		if attempt >= (*wait+poll-1)/poll {
			return exitErr{code: 3, err: fmt.Errorf("still refused after %ds; nothing booked", *wait)}
		}
		if memSleep(env, time.Duration(min(poll, *wait-attempt*poll))*time.Second, sigs) {
			return exitErr{code: 3, err: errors.New("interrupted while waiting; its place in line was given up")}
		}
	}
}

// memSleep sleeps, and reports whether a signal ended the wait first.
func memSleep(env Env, d time.Duration, sigs chan os.Signal) bool {
	if env.Sleep != nil {
		env.Sleep(d)
		return false
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return false
	case <-sigs:
		return true
	}
}

// admitOnce judges one admit under the lock. Admitted, the pending place (if
// any) becomes a fading booking; refused with a wait, the place is kept.
func admitOnce(env Env, dir string, owner, call, label string, need float64, pid int, deadline time.Time, hold bool) (bool, string, error) {
	lk, err := memLock(dir, memLockWait)
	if err != nil {
		return false, "", err
	}
	defer memUnlock(lk)
	// The config is read on every try, under the lock: a waiter that kept the
	// floor it started with admitted an 8 GB tier under a floor the operator
	// had raised meanwhile (Codex code pass).
	cfg, err := readMemConfig(dir)
	if err != nil {
		return false, "", err
	}
	book, err := readMemBook(dir)
	if err != nil {
		return false, "", err
	}
	m, err := env.memReading()
	if err != nil {
		return false, "", fmt.Errorf("no memory reading: %v", err)
	}
	now := nowOf(env)
	book.prune(env, now)
	var me *memRow
	for i := range book.Rows {
		if book.Rows[i].Owner == owner && book.Rows[i].Call == call && book.Rows[i].Pending {
			me = &book.Rows[i]
		}
	}
	// A first try is a newcomer and counts every waiter already in line; a
	// waiter counts only those ahead of it.
	why, left := memVerdict(m, cfg.FloorGB, book.booked(me, now, m.freeGB(), cfg.FloorGB), need)
	if len(why) == 0 {
		kept := book.Rows[:0]
		for _, r := range book.Rows {
			if !(r.Owner == owner && r.Call == call) {
				kept = append(kept, r)
			}
		}
		book.Rows = kept
		book.Seq++
		book.Rows = append(book.Rows, memRow{Owner: owner, Call: call, Kind: label, GB: need,
			T: unixSec(now), Until: unixSec(now.Add(memTTL)), Seq: book.Seq, Detached: true})
		if err := writeMemBook(dir, book); err != nil {
			return false, "", err
		}
		return true, fmt.Sprintf("admitted %s (%s GB): %s GB left above what is booked", label, gb(need), gb(left)), nil
	}
	if hold && me == nil {
		born := env.jobProc(pid).Born
		book.Seq++
		book.Rows = append(book.Rows, memRow{Owner: owner, Call: call, Kind: label, GB: need, Pending: true,
			T: unixSec(now), Until: unixSec(deadline.Add(memPendingSlack)), Seq: book.Seq, PID: pid, Born: born})
		if err := writeMemBook(dir, book); err != nil {
			return false, "", err
		}
	}
	return false, "refused (" + strings.Join(why, "; ") + ")", nil
}
