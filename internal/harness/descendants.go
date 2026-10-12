package harness

// ESCAPED DESCENDANTS: OBSERVED AND REPORTED, NEVER SIGNALLED (A-02 narrow
// contract, D-077 2026-10-11 and 2026-10-12 continuations).
//
// The failure (V-12): Claude Code runs every Bash tool shell in a session of
// its own (measured: pgid = sid = shell pid, parent `claude`), so a background
// job of a tool sits outside the worker's process group. Close's group signal
// never reaches it, and a resume's group probe never sees it: the adapter
// closed, the job kept running, and a resume would have admitted a second
// worker beside it.
//
// What the operator chose (2026-10-11), over a proposal that would also have
// killed such processes after a pid and start-time re-read: the adapter
// signals ONLY the group it pins (its own unreaped child's). A pid re-read is
// not a pin: Codex's review showed the descendant can exit, be reaped and have
// its pid reused between the re-read and the kill. So a process that left the
// group is OBSERVED, durably, and REPORTED, and a resume refuses while a
// recorded one lives. Nothing here ever sends a signal; every observation is a
// table read, a kill(pid, 0) or a sysctl.
//
// How a descendant is found. A scan reads the whole process table once and
// walks it breadth-first from the roots: the leader (unreaped, so its pid is
// ours) and every escaped descendant already recorded whose pid and start time
// still match. A row is a child of a node only if its ppid is the node's pid
// AND it started no earlier than the node (a child cannot predate its parent;
// equal is allowed, since microsecond start times can tie). The table is not
// known to be read atomically (and on linux it plainly is not), so a parent
// row can belong to a process that exited, was reaped and had its pid reused
// by a stranger mid-read; the stranger's child would then pass both tests
// (Codex design review, 2026-10-12). So every node a new descendant's chain
// passes through is re-observed AFTER the table read: alive with the same
// start time then means it held its pid for the whole read, so the ppid in the
// row named it. A chain with any node gone or recycled is dropped, and those
// processes stay unattributed. A descendant is ESCAPED iff its pgid differs
// from the leader's; in-group descendants are walked through but not recorded,
// because the group signal and the group probe already cover them. Sharing a
// group or session with anybody never establishes ancestry.
//
// What is kept. The record holds escaped descendants observed alive at the
// latest scan, plus any whose observation failed. One is dropped only on a
// POSITIVE observation, by its own pid: gone, or recycled (another start time,
// so not ours). Absence from a table is never "gone", and a failed table read
// changes nothing that was recorded. At most maxRecordedDescendants are kept;
// past it, `overflow` counts the observations dropped for want of room
// (observations, not identities: an unrecorded process is counted on every
// scan that finds it) and is sticky for the attempt, and a resume refuses.
//
// When. A sampler scans every second while the worker runs (no kqueue
// NOTE_FORK trigger: measured, it caught 3-11 of 20 fast double forks, not
// worth a second code path). Every shutdown the adapter starts (Close, stdout
// EOF) takes one order: write admission off, the sampler stopped and joined,
// a final scan completed, and only then stdin closed. A worker that exits on
// stdin EOF would otherwise reparent its children before anything saw them.
// A leader that exits by itself has already reparented its never-sampled
// children; the scan then walks only from recorded roots.
//
// What the close report says, and what it does not. After the group signals
// and the reap, each identity in the final scan's cohort is observed once
// more: `ended` (observed gone or recycled), `survivors` (alive at
// observed_at), `unknown` (could not be observed). `unattributable` is always
// "unknown": a descendant that left the tree before any scan saw it (a double
// fork, or a single fork that setsid'd and whose parent exited between scans)
// cannot be attributed by any supported unprivileged mechanism, and the report
// never says the tree is empty. The report covers the close-time cohort only;
// earlier exits are counted in descendants.ended.
//
// Persistence. The authorization holds one canonical record; every writer
// (the sampler, Start's identity bind, the close) changes its own fields of
// that record under one mutex, so nobody republishes a stale copy. A scan
// commits only if no other commit happened since it read its starting state,
// and nothing commits after the close seals the record. A publication that
// fails leaves the record dirty, so the next commit retries it; the adapter
// says so on stderr rather than print a report from older contents.

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// maxRecordedDescendants bounds the escaped descendants one record carries.
const maxRecordedDescendants = 256

// descendantScanInterval is the sampler's period.
const descendantScanInterval = time.Second

// processHooks lets a test watch or pace a worker it is about to start. Read
// ONCE, by startJSONProcess, into the process; nil in production.
type processHooks struct {
	// signal replaces syscall.Kill for every signal the process code sends.
	signal func(pid int, sig syscall.Signal) error
	// trace records the shutdown steps in the order they happen.
	trace func(step string)
	// scanEvery replaces descendantScanInterval when non-zero.
	scanEvery time.Duration
	// scanning runs at the start of every periodic scan; a test blocks it
	// to hold the sampler mid-scan.
	scanning func()
	// reading runs before the reader's first read; a test blocks it to hold
	// the reader while the leader writes and exits.
	reading func()
}

var processTestHooks atomic.Pointer[processHooks]

type procRow struct {
	PID, PPID, PGID int
	Start           int64
}

// ObservedProcess is one escaped descendant, as last observed.
type ObservedProcess struct {
	PID       int       `json:"pid"`
	Start     int64     `json:"start"`
	PPID      int       `json:"ppid"`
	PGID      int       `json:"pgid"`
	FirstSeen time.Time `json:"first_seen"`
}

func (o ObservedProcess) identity() ProcessIdentity {
	return ProcessIdentity{PID: o.PID, Start: o.Start, PGID: o.PGID}
}

// DescendantRecord is the sampler's durable observation for one attempt.
type DescendantRecord struct {
	ScanSeq    uint64    `json:"scan_seq"`
	LastScan   time.Time `json:"last_scan"`
	ScanErrors uint64    `json:"scan_errors"`
	// LastError is set while the latest scan could not DISCOVER everything (a
	// table read or an ancestor failed), so a descendant may be unrecorded; a
	// later complete scan clears it, and a resume refuses while it is set. A
	// recorded identity that could not be observed is only kept: ScanErrors
	// counts it, and a resume observes it again.
	LastError string `json:"last_error,omitempty"`
	Overflow  uint64 `json:"overflow,omitempty"`
	// Ended counts recorded escaped descendants later observed gone or
	// recycled during the attempt, the close cohort's included.
	Ended uint64            `json:"ended"`
	Alive []ObservedProcess `json:"alive"`
}

type UnknownProcess struct {
	PID   int    `json:"pid"`
	Start int64  `json:"start"`
	Error string `json:"error"`
}

// CloseReport is what the adapter observed about the escaped descendants when
// it ended its worker. `ended` is observed termination only; nothing in it was
// signalled.
type CloseReport struct {
	Trigger        string            `json:"trigger"`
	CapturedAt     time.Time         `json:"captured_at"`
	CaptureError   string            `json:"capture_error,omitempty"`
	ReapedAt       time.Time         `json:"reaped_at"`
	ObservedAt     time.Time         `json:"observed_at"`
	Group          string            `json:"group"`
	Captured       []ObservedProcess `json:"captured"`
	Ended          []ProcessIdentity `json:"ended"`
	Survivors      []ObservedProcess `json:"survivors"`
	Unknown        []UnknownProcess  `json:"unknown"`
	Unattributable string            `json:"unattributable"`
}

// descendantObserver is the scan's view of the kernel; tests substitute it.
type descendantObserver struct {
	list    func() ([]procRow, error)
	observe func(int) (ProcessIdentity, error)
	now     func() time.Time
}

var systemObserver = descendantObserver{list: listProcesses, observe: observeProcess, now: time.Now}

// candidate is a newly found escaped descendant and the chain of nodes, root
// first and its parent last, whose identity the attribution relied on.
type candidate struct {
	row   procRow
	chain []ProcessIdentity
}

// attribute walks one table from the roots (pure). A root that the table does
// not show with its recorded start time sires nothing in this table.
func attribute(leaderPGID int, roots []ProcessIdentity, table []procRow) []candidate {
	children := map[int][]procRow{}
	byPID := map[int]procRow{}
	for _, r := range table {
		children[r.PPID] = append(children[r.PPID], r)
		byPID[r.PID] = r
	}
	type node struct {
		id    ProcessIdentity
		chain []ProcessIdentity
	}
	// visited also keeps a root from being found again as a new descendant,
	// and ends a cycle a torn table could show.
	visited := map[int]bool{}
	var queue []node
	for _, root := range roots {
		key := ProcessIdentity{PID: root.PID, Start: root.Start}
		if row, ok := byPID[root.PID]; !ok || row.Start != root.Start || visited[root.PID] {
			continue
		}
		visited[root.PID] = true
		queue = append(queue, node{id: key, chain: []ProcessIdentity{key}})
	}
	var out []candidate
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, c := range children[n.id.PID] {
			if c.Start < n.id.Start || visited[c.PID] {
				continue
			}
			visited[c.PID] = true
			key := ProcessIdentity{PID: c.PID, Start: c.Start}
			if c.PGID != leaderPGID {
				out = append(out, candidate{row: c, chain: append([]ProcessIdentity(nil), n.chain...)})
			}
			queue = append(queue, node{id: key, chain: append(append([]ProcessIdentity(nil), n.chain...), key)})
		}
	}
	return out
}

func stateWith(observe func(int) (ProcessIdentity, error), want ProcessIdentity) (processState, error) {
	now, err := observe(want.PID)
	if errors.Is(err, errProcessGone) {
		return procGone, nil
	}
	if err != nil {
		return procError, err
	}
	if now.Start != want.Start {
		return procRecycled, nil
	}
	return procAlive, nil
}

func satAdd(a, b uint64) uint64 {
	if a > math.MaxUint64-b {
		return math.MaxUint64
	}
	return a + b
}

// scanDescendants is one scan: every recorded identity re-observed by its own
// pid, then new escaped descendants attributed from one table read.
func scanDescendants(o descendantObserver, leader ProcessIdentity, prev DescendantRecord) DescendantRecord {
	next := prev
	next.ScanSeq = satAdd(next.ScanSeq, 1)
	next.LastScan = o.now()
	next.Alive = nil
	// errs are DISCOVERY failures: a table that could not be read, or an
	// ancestor that could not be observed, either of which can leave an
	// escaped descendant unrecorded. They set LastError, which a resume
	// refuses on. A recorded identity that cannot be observed right now is
	// not one: it stays recorded, and a resume observes it again itself. Made
	// unresolved, it outlived the identity's own exit in a sealed record and
	// refused every resume for good (Codex code review P2, 2026-10-12).
	var errs []string
	retainErrors := false
	for _, e := range prev.Alive {
		state, _ := stateWith(o.observe, e.identity())
		switch state {
		case procGone, procRecycled:
			next.Ended = satAdd(next.Ended, 1)
		case procError:
			retainErrors = true
			next.Alive = append(next.Alive, e)
		default:
			next.Alive = append(next.Alive, e)
		}
	}
	table, err := o.list()
	if err != nil {
		errs = append(errs, "process table: "+err.Error())
	} else {
		roots := []ProcessIdentity{leader}
		for _, e := range next.Alive {
			roots = append(roots, e.identity())
		}
		verified := map[ProcessIdentity]bool{}
		holds := func(id ProcessIdentity) bool {
			ok, seen := verified[id]
			if !seen {
				state, err := stateWith(o.observe, id)
				if state == procError {
					// The descendant stays unrecorded, and the scan says it
					// could not finish (Codex code review: this was dropped).
					errs = append(errs, fmt.Sprintf("ancestor pid %d: %v", id.PID, err))
				}
				ok = state == procAlive
				verified[id] = ok
			}
			return ok
		}
		kept := map[int]int{}
		for i, e := range next.Alive {
			kept[e.PID] = i
		}
	candidates:
		for _, c := range attribute(leader.PGID, roots, table) {
			for _, link := range c.chain {
				if !holds(link) {
					continue candidates
				}
			}
			found := ObservedProcess{PID: c.row.PID, Start: c.row.Start, PPID: c.row.PPID, PGID: c.row.PGID, FirstSeen: next.LastScan}
			if i, ok := kept[c.row.PID]; ok {
				// The recorded process passed its own check before the table
				// read and the table shows its pid held by a newer process: a
				// pid names one process at a time, so the recorded one ended.
				// Recording both named one pid twice, which admission refuses
				// for good (Codex code review). An older start is no evidence.
				if c.row.Start > next.Alive[i].Start {
					next.Alive[i] = found
					next.Ended = satAdd(next.Ended, 1)
				}
				continue
			}
			if len(next.Alive) >= maxRecordedDescendants {
				next.Overflow = satAdd(next.Overflow, 1)
				continue
			}
			// attribute visits a pid once, so no later candidate shares it.
			next.Alive = append(next.Alive, found)
		}
	}
	next.LastError = ""
	if len(errs) > 0 || retainErrors {
		next.ScanErrors = satAdd(next.ScanErrors, 1)
	}
	if len(errs) > 0 {
		if len(errs) > 3 {
			errs = append(errs[:3], fmt.Sprintf("and %d more", len(errs)-3))
		}
		next.LastError = strings.Join(errs, "; ")
	}
	return next
}

// descendantsDiffer is whether a scan changed anything a resume or the report
// reads; a scan that changed only its sequence and time is not republished.
func descendantsDiffer(prev *DescendantRecord, next DescendantRecord) bool {
	if prev == nil {
		return len(next.Alive) > 0 || next.Overflow > 0 || next.LastError != "" || next.Ended > 0
	}
	if prev.Overflow != next.Overflow || prev.LastError != next.LastError || prev.Ended != next.Ended || len(prev.Alive) != len(next.Alive) {
		return true
	}
	for i := range prev.Alive {
		if prev.Alive[i].PID != next.Alive[i].PID || prev.Alive[i].Start != next.Alive[i].Start {
			return true
		}
	}
	return false
}

// descendantShape is the structural check a resume applies to the record's
// descendant section before probing anything it names.
func descendantShape(d *DescendantRecord) error {
	if d == nil {
		return nil
	}
	if len(d.Alive) > maxRecordedDescendants {
		return fmt.Errorf("names %d escaped descendants, over the %d bound", len(d.Alive), maxRecordedDescendants)
	}
	seen := map[int]bool{}
	for _, e := range d.Alive {
		if e.PID <= 1 || e.Start <= 0 {
			return fmt.Errorf("names an escaped descendant with pid %d and start %d, which identifies nothing", e.PID, e.Start)
		}
		if seen[e.PID] {
			return fmt.Errorf("names pid %d twice", e.PID)
		}
		seen[e.PID] = true
	}
	return nil
}
