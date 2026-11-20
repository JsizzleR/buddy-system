package cli

// The machine-wide memory slot (issue #144 proposal 4; D-238). A heavy start
// (whole-package or -race Go tests, a test tier, a done-check, a Codex pass, a
// lane or VM start) books its expected memory in one file every session on
// the machine shares, and a start that would leave less than the floor free
// is refused by `buddy gate` with the exact form that waits its turn (all but
// a start this machine could never hold, which is told so instead: D-239).
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
// incident there): a 4 GB floor on its 24 GB machine; refuse also at kernel
// pressure >= 2, and on swap over 8 GB only with under 30% free (swap stays in
// use after pressure passes, so alone it is no signal); an admitted booking
// FADES linearly to 0 over 90 s, because counting it in full double-counted
// (27 GB "booked" against 18 GB free on a healthy box refused every start); a
// waiter holds a PENDING place that later arrivals count in full, and a
// waiter counts only the pending rows AHEAD of it (counting those behind
// deadlocked seven waiters).
//
// WHAT THE FADE IS (D-239's measurement corrects the reason first written
// here, "by its peak a build is in the free reading"). On macOS the level is
// the share of usable memory neither wired nor held by the compressor, so a
// start shows in it only as the kernel compresses or wires for it: 2 GB of
// anonymous memory held for 45 s moved the level by nothing while file cache
// lasted. A faded booking is therefore mostly neither booked nor in the
// reading. The fade is a BURST window, how much may start within 90 s; what
// stops slow accumulation is the level falling once the machine compresses,
// and the pressure clause.
//
// FITTED TO THE MACHINE (D-239). The 4 GB floor and the cost table were one
// 24 GB laptop's: on 8 GB that floor is half the machine, a tier (8 GB) can
// never be admitted, and `git push`, whose hook asks for one, waits out its
// 1800 s and blocks, every time. A config of version 2 is the fitted slot:
// the floor is RAM/6 held to [1, 8] GB, derived at every judge (or the
// operator's explicit one), and no single start books more than RAM/3, never
// under 1 GB. Both shares are the operator's own numbers on the measured
// machine, so a 24 GB machine's verdicts do not change. A version-1 config is
// D-238's, floor and table, until the operator's own init says otherwise.
// NOT scaled with -p or the core count: measured here, a build's peak is one
// large process at a time (841 MB at -p 1, 948 at -p 12), and the -race burst
// on a third of the cores was 87% of the peak. NOT re-based on this
// repository's figures (its Go starts sample at 0.2 to 1.0 GB against 3 to 8
// booked): the slot is machine-wide and reads a command, not which repository
// runs it, and no other repository was measured. No CPU dimension: the
// freezes were memory, and a row fades in 90 s, so a count of rows is not a
// count of what is running.
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
//   - a light call (no kind anywhere in it, priceMem): allowed, and the slot
//     is never touched, the override included: a broken slot cannot stop a
//     report, a commit or a message.
//   - a heavy call while the config cannot be stat'ed (anything but "does not
//     exist"), the state cannot be read, parsed or locked, its version is
//     newer, the memory reading fails or is not usable (Env.memReading), or
//     the booking cannot be written: that call is DENIED, naming the cause.
//     Invariant 3's "exists but unreadable".
//     Failing open, as the local gate did, lets a bookings file truncated by
//     a full disk pass every heavy start without a word.
//   - beat's release failing: silent; the row fades within 90 s.
//
// ENFORCEMENT IS COOPERATIVE (D-005). A session can delete the config, write
// garbage into the bookings, or start work through a script the classifier
// does not read. The slot is a seatbelt against bursts nobody meant, not a
// sandbox; the skills say init, --reset and the override are the operator's.

import (
	"bytes"
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
	memDirRel = ".buddy/mem"
	// The config's versions (D-239). 1 is D-238's and stays D-238's exactly:
	// an explicit floor_gb, the cost table as it is. 2 marks the FITTED slot
	// (the floor a share of the machine or explicit, every start capped at a
	// third of it), written only by this binary's init. A version, not a new
	// field under version 1: two binaries reading one file two ways lowered a
	// floor nobody chose (an older init rewriting a synced auto config), and
	// let an older gate judge an admit's cover by the table while a newer
	// admit booked it capped. An older binary refuses version 2 by D-238's own
	// "its version is newer" rule, naming install.sh.
	memConfigV1 = 1
	memConfigV2 = 2
	// memBookVersion is bookings.json's own version. It does NOT follow the
	// config's: a row is a row under either.
	memBookVersion = 1
	memTTL         = 90 * time.Second
	// The fitted slot's rule (D-239): floor = RAM/6 held to [1, 8] GB, and one
	// start books at most max(RAM/3, 1 GB). Each share is the operator's own
	// number on the one measured machine (4 GB and the tier's 8 GB on 24 GB),
	// so a 24 GB machine's verdicts are the same under either version; the
	// bounds are judgment and no machine in evidence sits at one.
	memFloorShare  = 6.0
	memFloorMinGB  = 1.0
	memFloorMaxGB  = 8.0
	memCapShare    = 3.0
	memCapMinGB    = 1.0
	memAuto        = "auto"
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

const usageMem = "usage: buddy mem init [--floor-gb <n>|auto] [--reset]   (the OPERATOR's act: turn the machine-wide memory slot on, ~/.buddy/mem, fitted to this machine: the floor is 1/6 of its memory, 1 to 8 GB, or the <n> GB named, and one start books at most 1/3 of that memory (never under 1 GB); a config an earlier init wrote keeps its floor and uncapped costs until --floor-gb is given; --reset clears every booking)\n" +
	"       buddy mem status                    free memory, what is booked and pending, the floor and where it came from\n" +
	"       buddy mem admit <kind>... [--wait <secs>]   book a heavy start before running it; with --wait, hold a place in line (exit 0 admitted, 3 still refused, 1 an error; 0 with nothing booked when the slot is off)\n" +
	"       (kinds: tier goall race vm gotest check lane codex; `buddy mem admit K --wait 900 && { <cmd>; }` is not judged again by the gate. Refused by the gate: wait that way, run_in_background)"

// memReading is one sample of the machine's memory, filled by a reader
// (memread_*.go) and judged only after Env.memReading has checked it.
type memReading struct {
	TotalGB  float64
	Level    int // percent available (darwin: kern.memorystatus_level)
	Pressure int // 1 normal, 2 warn, 4 critical (darwin: kern.memorystatus_vm_pressure_level)
	SwapGB   float64
	// AvailGB is the kernel's own available figure, where it reports one in
	// bytes (Linux's MemAvailable); 0 means "not supplied", and free is then
	// TotalGB * Level / 100, as on darwin. A reader that fills it fills Level
	// from it too, so an available figure of exactly 0 reads 0 both ways
	// (Codex design pass, D-239).
	AvailGB float64
	// Basis says, in the reader's words, what the figures were read from
	// (which file, which limit, a swap figure the slot does not judge). Printed
	// by `mem status` and `mem init` on one line (fence.Line, 256); never
	// judged. Empty on darwin.
	Basis string
}

func (r memReading) freeGB() float64 {
	if r.AvailGB > 0 {
		return r.AvailGB
	}
	return r.TotalGB * float64(r.Level) / 100
}

// unusable is why a reading cannot be judged on, or "" when it can. Each
// figure's own RANGE is checked here, once, for every reader: an infinite
// total admits everything (infinite headroom), a NaN one too (`left < floor`
// is false for NaN), a total of 0 beside a positive available figure derives
// a floor and prices every start from nothing (Codex design pass, panel lens
// B; D-239), and a finite total can still overflow the free figure (1e308 GB
// at level 100 is +Inf: Codex code pass). NOT checked, and the reader's
// contract (memReading's comment): that Level agrees with a supplied AvailGB,
// which the swap clause reads beside it, and Pressure, where any value under
// 2 is "normal" as under D-238.
func (r memReading) unusable() string {
	bad := func(v float64) bool { return math.IsNaN(v) || math.IsInf(v, 0) }
	switch {
	case bad(r.TotalGB) || r.TotalGB <= 0:
		return fmt.Sprintf("its total is %g GB, not a positive figure", r.TotalGB)
	case bad(r.AvailGB) || r.AvailGB < 0 || r.AvailGB > r.TotalGB:
		return fmt.Sprintf("its available figure is %g GB, not between 0 and the total %g GB", r.AvailGB, r.TotalGB)
	case r.Level < 0 || r.Level > 100:
		return fmt.Sprintf("its level is %d, not 0 to 100", r.Level)
	case bad(r.SwapGB) || r.SwapGB < 0:
		return fmt.Sprintf("its swap figure is %g GB", r.SwapGB)
	case bad(r.freeGB()):
		return fmt.Sprintf("its total of %g GB at level %d is no finite free figure", r.TotalGB, r.Level)
	}
	return ""
}

// memReading is the ONE way a reading is taken, a test's fake and the real
// reader alike: a reader's panic becomes an error (under D-107's hook line a
// panic in the gate exits 2 and blocks the call), and a reading that is not
// usable is no reading. Either way the caller has an error, and a heavy call
// is denied on it as on a failed sysctl.
func (e Env) memReading() (m memReading, err error) {
	defer func() {
		if p := recover(); p != nil {
			m, err = memReading{}, fmt.Errorf("the memory reader panicked (%v)", p)
		}
	}()
	if e.MemReading != nil {
		m, err = e.MemReading()
	} else {
		m, err = readMemory()
	}
	if err != nil {
		return memReading{}, err
	}
	if why := m.unusable(); why != "" {
		return memReading{}, errors.New("the reading is not usable: " + why)
	}
	return m, nil
}

// basisLine is the reader's Basis as status and init print it: its own line,
// one line (fence.Line: a reader builds it from files a container's host
// controls), or nothing where the reader gave none.
func basisLine(m memReading) string {
	if m.Basis == "" {
		return ""
	}
	return "reading: " + fence.Line(m.Basis, 256) + "\n"
}

// memConfig is config.json. What init writes, and what is readable beside it,
// is readMemConfig's: {"version":1,"floor_gb":N}, {"version":2,"floor_gb":N}
// and {"version":2,"floor":"auto"}. Floor is a pointer so that a `"floor":""`
// somebody typed is a word that is not "auto", not an absent one.
type memConfig struct {
	Version int     `json:"version"`
	FloorGB float64 `json:"floor_gb,omitempty"`
	Floor   *string `json:"floor,omitempty"`
}

func (c memConfig) auto() bool   { return c.Floor != nil }
func (c memConfig) fitted() bool { return c.Version == memConfigV2 }

// floor is the floor a judge holds a start to on a machine of total GB:
// explicit, or under auto a sixth of the machine held to [1, 8] GB. Derived at
// every judge and never stored, so a config carried to another machine
// follows the machine it is on.
func (c memConfig) floor(total float64) float64 {
	if c.auto() {
		return math.Min(math.Max(total/memFloorShare, memFloorMinGB), memFloorMaxGB)
	}
	return c.FloorGB
}

// startCap is the most ONE start books on a machine of total GB under the
// fitted slot, a third of it and never under 1 GB (without that least a lane
// booked 0.67 GB on a 2 GB machine), rounded DOWN to a whole MiB so sums of
// capped costs are exact (memKindGB). 0 under version 1: no cap.
func (c memConfig) startCap(total float64) float64 {
	if !c.fitted() {
		return 0
	}
	return math.Floor(math.Max(total/memCapShare, memCapMinGB)*1024) / 1024
}

// rule says where a judge's figures come from, for status and init.
func (c memConfig) rule(total float64) string {
	floor := "explicit (--floor-gb)"
	if c.auto() {
		floor = fmt.Sprintf("1/%d of this machine's %s GB", int(memFloorShare), gb(total))
		switch share := total / memFloorShare; {
		case share < memFloorMinGB:
			floor += fmt.Sprintf(", held up to the %s GB least", gb(memFloorMinGB))
		case share > memFloorMaxGB:
			floor += fmt.Sprintf(", held down to the %s GB most", gb(memFloorMaxGB))
		}
	}
	if !c.fitted() {
		return fmt.Sprintf("floor %s GB, %s; costs by the table, uncapped (config version %d)", gb(c.floor(total)), floor, c.Version)
	}
	// The cap's half names the machine itself: under an explicit floor "of it"
	// had nothing to point at, and on 2 GB "1.0 GB, 1/3 of it" was half the
	// machine with no word of the least (Fable cross-check).
	least := ""
	if total/memCapShare < memCapMinGB {
		least = fmt.Sprintf(", held up to the %s GB least", gb(memCapMinGB))
	}
	return fmt.Sprintf("floor %s GB, %s; one start books at most %s GB, 1/%d of this machine's %s GB%s (config version %d)",
		gb(c.floor(total)), floor, gb(c.startCap(total)), int(memCapShare), gb(total), least, c.Version)
}

// word is the config's floor as overrides.log and a reading-less status name it.
func (c memConfig) word() string {
	if c.auto() {
		return memAuto
	}
	return gb(c.FloorGB)
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

// readMemConfig reads config.json, and every state it can be in has ONE
// verdict (D-239). Readable: version 1 with a usable floor_gb and no floor
// word (D-238's, kept exactly); version 2 with a usable floor_gb and no word
// (fitted, the floor explicit); version 2 with the word "auto" and no
// floor_gb (fitted, the floor derived). A `"floor":null` is no word and a
// `"floor_gb":0` no figure, as if each were absent (encoding/json's reading,
// and neither can lower a floor: what is left must still be one of the three).
// Everything else is invariant 3's "exists but unreadable", a heavy call
// denied naming it: a newer version; a floor word under version 1 (hand-made,
// no init writes it, and either reading of it could lower a floor); both a
// word and a floor_gb, or neither; any other word ("AUTO", "pressure", ""); a
// floor that is a number, or a floor_gb that is a string (the file does not
// parse); a key named twice. The error never carries the config: a caller
// that has an error has no floor.
func readMemConfig(dir string) (memConfig, error) {
	path := filepath.Join(dir, "config.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return memConfig{}, err
	}
	var c memConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return memConfig{}, fmt.Errorf("%s does not parse (%v)", path, err)
	}
	// encoding/json keeps the LAST of a repeated key without a word:
	// {"version":2,"floor_gb":8,"floor_gb":1} read as a 1 GB floor, and
	// {"version":1,"floor_gb":4,"version":2} as the fitted slot (Codex code
	// pass, Fable cross-check). Byte-equal names only: a key in another case
	// ("Floor_GB"), which encoding/json also matches, is recorded open.
	if k, twice := repeatedKey(b); twice {
		return memConfig{}, fmt.Errorf("%s names %s twice: which one holds is not for this binary to guess", path, strconv.Quote(fence.Line(k, 64)))
	}
	usableGB := c.FloorGB > 0 && !math.IsNaN(c.FloorGB) && !math.IsInf(c.FloorGB, 0)
	switch {
	case c.Version > memConfigV2:
		return memConfig{}, fmt.Errorf("%s is version %d and this buddy reads up to %d: upgrade this binary (sh scripts/install.sh)", path, c.Version, memConfigV2)
	case c.Version < memConfigV1:
		return memConfig{}, fmt.Errorf("%s has no usable version (%d)", path, c.Version)
	case c.Floor != nil && c.Version == memConfigV1:
		return memConfig{}, fmt.Errorf("%s is version 1 and carries a floor word, which only a version 2 config has", path)
	case c.Floor != nil && *c.Floor != memAuto:
		return memConfig{}, fmt.Errorf("%s has the floor word %s, and the only one is %q", path, strconv.Quote(fence.Line(*c.Floor, 64)), memAuto)
	case c.Floor != nil && c.FloorGB != 0:
		return memConfig{}, fmt.Errorf("%s has both the floor word %q and a floor_gb: which one holds is not for this binary to guess", path, memAuto)
	case c.Floor == nil && !usableGB:
		return memConfig{}, fmt.Errorf("%s has no usable floor_gb", path)
	}
	return c, nil
}

// repeatedKey is the first top-level key a JSON object names twice. Only
// called on bytes json.Unmarshal took, so every error here is "not an object"
// and answers false. The key comes back beside a bool, not as "" for none:
// the empty string is a key too, and {"":0,"":0,"version":2,"floor_gb":8,
// "floor_gb":1} stopped at its repeated "" and read as "nothing repeated",
// floor 1 (Codex re-verify 1).
func repeatedKey(b []byte) (key string, twice bool) {
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return "", false
	}
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		k, ok := t.(string)
		if err != nil || !ok {
			return "", false
		}
		if seen[k] {
			return k, true
		}
		seen[k] = true
		var v json.RawMessage
		if dec.Decode(&v) != nil {
			return "", false
		}
	}
	return "", false
}

func readMemBook(dir string) (memBook, error) {
	b, err := os.ReadFile(filepath.Join(dir, "bookings.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return memBook{Version: memBookVersion}, nil
	}
	if err != nil {
		return memBook{}, err
	}
	var book memBook
	if err := json.Unmarshal(b, &book); err != nil {
		return memBook{}, fmt.Errorf("%s does not parse (%v)", filepath.Join(dir, "bookings.json"), err)
	}
	if book.Version != memBookVersion {
		return memBook{}, fmt.Errorf("%s is version %d and this buddy reads %d: upgrade this binary (sh scripts/install.sh)", filepath.Join(dir, "bookings.json"), book.Version, memBookVersion)
	}
	return book, nil
}

// writeMemBook replaces the bookings through a synced temporary file, so a
// forced power-off leaves the old file or the new one, never a torn one.
func writeMemBook(dir string, book memBook) error {
	book.Version = memBookVersion
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

// neverFits: no reading of this machine could admit a start of need GB. It is
// memVerdict's own subtraction with all of the total free and nothing booked,
// and the gate and admit both ask it, so "never" and a verdict cannot differ
// in the last place: written as need > total - floor, a floor of
// 2.6679687500000004 GB on 8 GB did not call 5.33203125 GB "never" (8 less
// that floor rounds to 5.33203125) while every verdict left 2.66796875, under
// the floor, for the whole wait (Codex code pass). A start that leaves
// exactly the floor fits.
func neverFits(m memReading, floor, need float64) bool {
	return m.TotalGB-need < floor
}

// neverFitsAdvice is what the two never-fit refusals tell the caller to do.
// "Fewer at a time" is advice only for a call that starts several: for ONE
// start it is the floor that leaves no room ({"version":2,"floor_gb":8}
// carried to an 8 GB machine refused every heavy call with it), and the
// floor is the operator's (Fable cross-check).
func neverFitsAdvice(starts int) string {
	if starts == 1 {
		return "It is one start, so there is nothing to split: the floor leaves it no room on this machine, and the floor is the operator's (`buddy mem init --floor-gb <n>|auto`)"
	}
	return "Start them one after another, or fewer at a time"
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

// memPriced is what the gate found one command to cost, and under what.
type memPriced struct {
	cost   memCost
	w      *memWalker
	cfg    memConfig
	cfgErr error // the config could not be stat'ed or read: no floor, no cap
	// capTotal is the memory total the costs were capped with. 0: they are
	// the table's (a version-1 config, an unreadable one, or a fitted one
	// whose reading or second walk failed).
	capTotal float64
}

// memClassifier is classifyMemCapped's shape: a parameter of priceMem so a
// test can hand it a second walk that panicked, which no input is known to do.
type memClassifier func(cmd string, limit float64) (memCost, *memWalker, bool)

// priceMem is the gate's pricing of one shell command, in the order D-239
// settled.
//
// (1) The command is read with NO cap, exactly as before D-239. A call in
// which the walk met no kind is LIGHT and nothing more is touched: no config,
// no reading, no lock. The test is "no kind met", not "books nothing": on an
// 8 GB machine `buddy mem admit tier --wait 900 && { go test ./a & go test
// ./b & wait; }` books nothing by the table (8 covers 6) and returned here,
// before the walk that prices it at 5.33 GB against an admit of 2.67 (Codex
// design pass, panel lenses A and B). The cap changes what a kind costs,
// never whether one is there, so this set is the same under any cap.
//
// (2) With a kind in it, the config is read, and NOT refused on here: the
// caller refuses, after the covered-admit allow. Under a readable version 2,
// one reading is taken and the command read a SECOND time with the cap. If
// that reading fails, or the second walk panics, the first walk's costs
// stand, uncapped: each START at the table's figure, never under its capped
// one. (A CALL's total can be less: an admit that covers its tail by the
// table and not at the cap totals 0 against 5.33 GB, the case D-239 records
// open.) Nothing is refused for it yet: the override is honored and booked as
// before, and the verdict's own reading, under the lock, is what refuses. One
// reading before any classifying, as first drafted, put the reader in front
// of every Bash call of a machine with the slot on, light ones included.
func priceMem(cmd string, env Env, slot memSlot, classify memClassifier) (p memPriced, light bool) {
	p.cost, p.w, _ = classify(cmd, 0)
	if p.w.leaves == 0 {
		return p, true
	}
	p.cfgErr = slot.err
	if p.cfgErr == nil {
		p.cfg, p.cfgErr = readMemConfig(slot.dir)
	}
	if p.cfgErr != nil || !p.cfg.fitted() {
		return p, false
	}
	m, err := env.memReading()
	if err != nil {
		return p, false
	}
	if c, w, ok := classify(cmd, p.cfg.startCap(m.TotalGB)); ok {
		p.cost, p.w, p.capTotal = c, w, m.TotalGB
	}
	return p, false
}

// gateMemory judges one shell call the claims phase allowed. It returns the
// refusal, or "" to allow; it writes nothing on an allow (a
// permissionDecision there would skip the user's permission prompt).
func gateMemory(h hookInput, env Env, slot memSlot, started time.Time) string {
	if !memIsShell(h) {
		return ""
	}
	p, light := priceMem(h.ToolInput.Command, env, slot, classifyMemCapped)
	if light {
		return "" // no kind anywhere in it: the slot is not consulted, its state not read
	}
	cost, w, cfg := p.cost, p.w, p.cfg
	if cost.total() == 0 && w.under == "" {
		// An admit covers everything this call starts, at the price the admit
		// will book: allowed with NO verdict, and judged when that admit runs.
		// With an unreadable config too, as before D-239: the admit itself
		// then exits 1 and its tail does not run. Falling through to a verdict
		// with nothing to book refused the very form the gate teaches, at
		// pressure warn, printing an admit with no kind (panel lenses A, B).
		return ""
	}
	kinds := cost.kinds()
	refuse := func(why string) string {
		return fmt.Sprintf("MEMORY GATE: nothing in this call ran. It starts %s, and the machine-wide memory slot cannot judge it: %s. The slot refuses heavy starts while it cannot read its own state (light commands are not affected). Ask the operator; `buddy mem status` shows the cause.",
			fence.Line(strings.Join(kinds, ", "), 256), fence.Line(why, 1024))
	}
	if p.cfgErr != nil {
		return refuse(p.cfgErr.Error())
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
	// The costs were capped with one reading's total and are judged with
	// this one's. A total that moved between the two (a resized container,
	// memory added) leaves a price for another machine: capped at 8 GB and
	// judged at 24, a tier would book 2.67 GB where it owes 8 (Codex design
	// pass). Refused, and a second send prices it afresh.
	if p.capTotal != 0 && m.TotalGB != p.capTotal {
		return fmt.Sprintf("MEMORY GATE: nothing in this call ran. It starts %s, priced for a machine of %s GB, and the memory total read %s GB at its verdict: the total changed while the call was judged. Send the same call again.",
			fence.Line(strings.Join(kinds, ", "), 256), gb3(p.capTotal), gb3(m.TotalGB))
	}
	floor := cfg.floor(m.TotalGB)
	need := cost.total()
	// A start no reading of this machine could admit is told so at once, in
	// words that do not teach the wait: the admit it would be sent to holds a
	// place for 900 s and exits 3, every time (panel lens A; on 8 GB, three
	// `go test` at once are 8.0 GB over a 1.33 GB floor). Only for costs
	// capped with this total: uncapped, a lone tier on that machine reads the
	// same and is not the same.
	if p.capTotal != 0 && neverFits(m, floor, need) {
		return fmt.Sprintf("MEMORY GATE: nothing in this call ran. It starts %s (%s GB at once), and this machine can never hold that: its %s GB of memory less the %s GB floor is %s GB. Waiting will not help, so do not send it through `buddy mem admit`. %s. `buddy mem status` shows the figures; an override is the operator's word only.",
			fence.Line(strings.Join(kinds, ", "), 256), gb(need), gb(m.TotalGB), gb(floor), gb(m.TotalGB-floor), neverFitsAdvice(len(kinds)))
	}
	book.prune(env, now)
	// The rows this call's booking replaces are not counted against it (Codex
	// code pass): a Codex re-run of `go test ./...` 5 s after the last one
	// was judged against that run's 5.7 GB still on the books, and refused.
	// Nothing is written on a refusal, so they stay booked then.
	dropReplaced(&book, call, cost)
	why, _ := memVerdict(m, floor, book.booked(nil, now, m.freeGB(), floor), need)
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

// memInit turns the slot on, or rewrites its config. What it writes (D-239):
//   - no config: version 2, the floor "auto", or the --floor-gb given.
//   - a readable config and no --floor-gb: what it holds, version included.
//     A version-1 config is the operator's D-238 slot, and an upgrade or a
//     bare init never moves it: the cap lowers the tier's booking from 8 to 6
//     GB on an 18 GB machine, and the table's figures are the operator's own
//     gate's. The line it prints says what fitting it would give, and how.
//   - a readable config and --floor-gb <n>|auto: version 2 with that floor.
//     Asking for the floor again is asking for the fitted slot; the line says
//     the cap in figures.
//   - a config that cannot be read: refused, unless --reset, which writes a
//     fresh version 2 (auto, or the --floor-gb given) and says what it
//     replaced and why. It used to write the default 4 GB without a word.
//   - --reset: the lock first, then the bookings, then the config (below).
//
// --floor-gb 0, a negative, NaN, Inf or any word but auto is refused and
// nothing is written (0 used to mean "the default", silently).
func memInit(args []string, env Env) error {
	fset := flag.NewFlagSet("mem init", flag.ContinueOnError)
	floorArg := fset.String("floor-gb", "", "the floor in GB, or auto")
	reset := fset.Bool("reset", false, "clear every booking")
	if help, err := parseFlags(fset, args, usageMem, env); help || err != nil {
		return err
	}
	if fset.NArg() > 0 {
		return usageRefusal(fmt.Sprintf("mem init: unexpected argument %s", strconv.Quote(fence.Line(fset.Arg(0), 64))), usageMem)
	}
	auto := memAuto
	fit := memConfig{Version: memConfigV2, Floor: &auto}
	var asked *memConfig // what --floor-gb names; nil when it was not given
	fset.Visit(func(f *flag.Flag) {
		if f.Name == "floor-gb" {
			asked = &fit
		}
	})
	if asked != nil && *floorArg != memAuto {
		n, err := strconv.ParseFloat(*floorArg, 64)
		if err != nil || n <= 0 || math.IsNaN(n) || math.IsInf(n, 0) {
			return usageRefusal(fmt.Sprintf("mem init: --floor-gb is a number of GB above 0, or auto; got %s, and nothing was written", strconv.Quote(fence.Line(*floorArg, 64))), usageMem)
		}
		asked = &memConfig{Version: memConfigV2, FloorGB: n}
	}
	home := env.getenv("HOME")
	if home == "" {
		return errors.New("mem init: HOME is not set, so there is no ~/.buddy/mem")
	}
	m, err := env.memReading()
	if err != nil {
		return fmt.Errorf("mem init: no memory reading on this machine (%v), so the slot cannot judge anything; nothing was written", err)
	}
	dir := filepath.Join(home, memDirRel)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	cfg, rerr := readMemConfig(dir)
	was, replaced := "turned ON", ""
	switch {
	case rerr == nil:
		was = "already on"
	case errors.Is(rerr, fs.ErrNotExist):
	case !*reset:
		return fmt.Errorf("mem init: %v (--reset rewrites it)", rerr)
	default:
		replaced = rerr.Error()
	}
	kept := rerr == nil && asked == nil
	switch {
	case asked != nil:
		cfg = *asked
	case rerr != nil:
		cfg = fit
	}
	// --reset takes the slot's lock BEFORE it writes anything, clears the
	// bookings under it, and only then writes the config. It used to rename
	// the new config into place first: with the lock held past 2 s (a judge
	// hung in its reader holds it, which is when an operator reaches for
	// --reset) the floor had changed, the command exited 1 naming only the
	// lock, and nothing said or logged what it wrote (Codex code pass, Fable
	// cross-check). In this order a failed lock or a failed clear writes
	// nothing, and the one failure left after a write says what was written.
	write := func() error {
		cleared := ""
		if *reset {
			lk, err := memLock(dir, memLockWait)
			if err != nil {
				return fmt.Errorf("mem init: %w; nothing was written (--reset writes only under that lock)", err)
			}
			defer memUnlock(lk) // let go before anything is printed: a paged init must not stall every judge
			if err := writeMemBook(dir, memBook{}); err != nil {
				return fmt.Errorf("mem init: the bookings could not be cleared (%w); nothing was written", err)
			}
			cleared = "every booking was cleared (--reset), and "
		}
		data, _ := json.Marshal(cfg)
		tmp := filepath.Join(dir, ".config.json.tmp")
		err := os.WriteFile(tmp, append(data, '\n'), 0o600)
		if err == nil {
			err = os.Rename(tmp, filepath.Join(dir, "config.json"))
		}
		if err != nil {
			return fmt.Errorf("mem init: %sthe config could not be written, so it is what it was: %w", cleared, err)
		}
		return nil
	}
	if err := write(); err != nil {
		return err
	}
	who, _, _ := nativeSession(env)
	_ = logMemOverride(dir, firstNonEmpty(who, "a terminal"), fmt.Sprintf("init version=%d floor=%s reset=%t", cfg.Version, cfg.word(), *reset), "buddy mem init", nowOf(env))
	fmt.Fprintf(env.Stdout, "memory slot %s: floor %s GB, %s (free %s GB now). Heavy Bash starts are judged by buddy gate on every session of this machine; delete %s to turn it off.\n",
		was, gb(cfg.floor(m.TotalGB)), dir, gb(m.freeGB()), filepath.Join(dir, "config.json"))
	fmt.Fprintf(env.Stdout, "rule: %s\n", cfg.rule(m.TotalGB))
	if kept && !cfg.fitted() {
		fmt.Fprintf(env.Stdout, "kept as it was. Fitted to this machine it would be: %s. That is `buddy mem init --floor-gb auto`, or `--floor-gb <n>` to keep a floor of your own under the cap.\n", fit.rule(m.TotalGB))
	}
	fmt.Fprint(env.Stdout, basisLine(m))
	if replaced != "" {
		fmt.Fprintf(env.Stdout, "the config that was there could not be read (%s) and was replaced by this one (--reset)\n", fence.Line(replaced, 512))
	}
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
		// No total, so an automatic floor has no figure: it is named, not guessed.
		floor := cfg.word() + " GB"
		if cfg.auto() {
			floor = fmt.Sprintf("%s (1/%d of the machine's memory)", memAuto, int(memFloorShare))
		}
		fmt.Fprintf(env.Stdout, "memory slot: ON, floor %s; no memory reading (%s): heavy starts are refused\n", floor, fence.Line(merr.Error(), 256))
	} else {
		floor := cfg.floor(m.TotalGB)
		fmt.Fprintf(env.Stdout, "memory slot: ON  free %s GB (%d%%)  booked %s GB  floor %s GB  pressure %d  swap %s GB\n",
			gb(m.freeGB()), m.Level, gb(book.booked(nil, now, m.freeGB(), floor)), gb(floor), m.Pressure, gb(m.SwapGB))
		fmt.Fprintf(env.Stdout, "rule: %s\n", cfg.rule(m.TotalGB))
		fmt.Fprint(env.Stdout, basisLine(m))
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
	owner, _, _ := nativeSession(env)
	if owner == "" {
		owner = fmt.Sprintf("pid%d", os.Getppid())
	}
	pid := os.Getpid()
	call := fmt.Sprintf("admit-%d", pid)
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
		res, msg, err := admitOnce(env, slot.dir, owner, call, kinds, pid, deadline, *wait > 0)
		if err != nil {
			return err
		}
		fmt.Fprintln(env.Stderr, "buddy mem admit: "+msg)
		switch res {
		case memAdmitted:
			queued = false
			return nil
		case memNeverFits:
			// queued stays as it is: a place held from an earlier try is given
			// up by the cleanup above.
			return exitErr{code: 3, err: errors.New("no wait can admit it, so it holds no place in line; nothing booked")}
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

// memAdmitResult is one try's outcome.
type memAdmitResult int

const (
	memRefused   memAdmitResult = iota // not now; with a wait, its place is held
	memAdmitted                        // booked
	memNeverFits                       // no reading of this machine could admit it: not queued
)

// admitOnce judges one admit under the lock. Admitted, the pending place (if
// any) becomes a fading booking; refused with a wait, the place is kept.
func admitOnce(env Env, dir string, owner, call string, kinds []string, pid int, deadline time.Time, hold bool) (memAdmitResult, string, error) {
	lk, err := memLock(dir, memLockWait)
	if err != nil {
		return memRefused, "", err
	}
	defer memUnlock(lk)
	// The config is read on every try, under the lock: a waiter that kept the
	// floor it started with admitted an 8 GB tier under a floor the operator
	// had raised meanwhile (Codex code pass).
	cfg, err := readMemConfig(dir)
	if err != nil {
		return memRefused, "", err
	}
	book, err := readMemBook(dir)
	if err != nil {
		return memRefused, "", err
	}
	m, err := env.memReading()
	if err != nil {
		return memRefused, "", fmt.Errorf("no memory reading: %v", err)
	}
	now := nowOf(env)
	book.prune(env, now)
	// Priced on every try, from this try's config and reading, kind by kind as
	// the gate's capped walk prices the tail this admit covers (D-239): an
	// admit priced once at its start would keep a cap, or none, the operator
	// has since changed. That keeps the BOOKING current; it does not re-judge
	// the cover the gate allowed this admit's tail on. A config moved from
	// version 1 to 2 while a waiter is in line books a tier's admit at the cap
	// (2.67 GB on 8 GB) over a tail the gate passed as covered by the table's
	// 8 (5.33 GB at the cap): recorded open in D-239 (Fable cross-check).
	limit, floor := cfg.startCap(m.TotalGB), cfg.floor(m.TotalGB)
	need := 0.0
	for _, k := range kinds {
		need += memKindGB(k, limit)
	}
	label := memKindLabel(kinds)
	var me *memRow
	for i := range book.Rows {
		if book.Rows[i].Owner == owner && book.Rows[i].Call == call && book.Rows[i].Pending {
			me = &book.Rows[i]
		}
	}
	// What no reading of this machine could admit is not queued: its place
	// would be infeasible for the whole wait, and the waiter exits 3 after 900
	// s having been taught to wait (panel lens A). Fitted slot only: under a
	// version-1 config an 8 GB tier on an 8 GB machine waits and exits 3, as
	// D-238 has it.
	if cfg.fitted() && neverFits(m, floor, need) {
		// A place it holds from an earlier try (the total fell while it
		// waited) is given up by memAdmit's own cleanup, which reads the file
		// afresh: `me` here is after the prune, and is not the file's word.
		return memNeverFits, fmt.Sprintf("refused, and waiting will not help: %s is %s GB at once, and this machine's %s GB of memory less the %s GB floor is %s GB. %s",
			label, gb(need), gb(m.TotalGB), gb(floor), gb(m.TotalGB-floor), neverFitsAdvice(len(kinds))), nil
	}
	// A waiter's place holds what it would book NOW: later arrivals count it
	// in full, and a figure from an earlier try (another cap, another total)
	// is not what it is waiting for.
	moved := me != nil && me.GB != need
	if moved {
		me.GB = need
	}
	// A first try is a newcomer and counts every waiter already in line; a
	// waiter counts only those ahead of it.
	why, left := memVerdict(m, floor, book.booked(me, now, m.freeGB(), floor), need)
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
			return memRefused, "", err
		}
		return memAdmitted, fmt.Sprintf("admitted %s (%s GB): %s GB left above what is booked", label, gb(need), gb(left)), nil
	}
	if hold && me == nil {
		born := env.jobProc(pid).Born
		book.Seq++
		book.Rows = append(book.Rows, memRow{Owner: owner, Call: call, Kind: label, GB: need, Pending: true,
			T: unixSec(now), Until: unixSec(deadline.Add(memPendingSlack)), Seq: book.Seq, PID: pid, Born: born})
		moved = true
	}
	if moved {
		if err := writeMemBook(dir, book); err != nil {
			return memRefused, "", err
		}
	}
	return memRefused, "refused (" + strings.Join(why, "; ") + ")", nil
}
