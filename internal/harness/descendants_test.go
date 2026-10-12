package harness

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The A-02 narrow contract (D-077, 2026-10-11): descendants that left the
// worker's group are observed, recorded and reported, and never signalled.

var testLeader = ProcessIdentity{PID: 100, Start: 1000, PGID: 100}

func TestAttributeDescendants(t *testing.T) {
	recorded := ProcessIdentity{PID: 200, Start: 500, PGID: 200}
	for _, tc := range []struct {
		name  string
		roots []ProcessIdentity
		table []procRow
		want  []int
	}{
		{"an escaped child is found, an in-group child is not", nil, []procRow{
			{PID: 101, PPID: 100, PGID: 100, Start: 1001}, {PID: 102, PPID: 100, PGID: 102, Start: 1002}}, []int{102}},
		{"an escaped grandchild under an in-group child", nil, []procRow{
			{PID: 101, PPID: 100, PGID: 100, Start: 1001}, {PID: 103, PPID: 101, PGID: 103, Start: 1003}}, []int{103}},
		{"a child that predates its parent is not attributed", nil, []procRow{{PID: 104, PPID: 100, PGID: 104, Start: 999}}, nil},
		{"an equal start time is a child", nil, []procRow{{PID: 105, PPID: 100, PGID: 105, Start: 1000}}, []int{105}},
		{"a recorded root sires", []ProcessIdentity{recorded}, []procRow{
			{PID: 200, PPID: 1, PGID: 200, Start: 500}, {PID: 201, PPID: 200, PGID: 200, Start: 600}}, []int{201}},
		{"a recorded root shown with another start sires nothing", []ProcessIdentity{recorded}, []procRow{
			{PID: 200, PPID: 1, PGID: 200, Start: 700}, {PID: 201, PPID: 200, PGID: 201, Start: 800}}, nil},
		// Started after the leader, as a real child does, so only visited
		// (not the start-time rule) keeps it from being found again.
		{"a recorded root is not found again", []ProcessIdentity{{PID: 210, Start: 1210, PGID: 210}}, []procRow{{PID: 210, PPID: 100, PGID: 210, Start: 1210}}, nil},
		{"sharing the leader's group is not ancestry", nil, []procRow{{PID: 300, PPID: 1, PGID: 100, Start: 2000}, {PID: 301, PPID: 1, PGID: 999, Start: 2000}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			table := append([]procRow{{PID: 100, PPID: 1, PGID: 100, Start: 1000}}, tc.table...)
			var got []int
			for _, c := range attribute(testLeader.PGID, append([]ProcessIdentity{testLeader}, tc.roots...), table) {
				got = append(got, c.row.PID)
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("attributed %v, want %v", got, tc.want)
			}
		})
	}
}

// fakeKernel scripts a process table and the point observations a scan makes.
type fakeKernel struct {
	table    []procRow
	tableErr error
	procs    map[int]ProcessIdentity
	errs     map[int]error
}

func (k *fakeKernel) observer() descendantObserver {
	return descendantObserver{
		list: func() ([]procRow, error) { return k.table, k.tableErr },
		observe: func(pid int) (ProcessIdentity, error) {
			if err := k.errs[pid]; err != nil {
				return ProcessIdentity{}, err
			}
			if id, ok := k.procs[pid]; ok {
				return id, nil
			}
			return ProcessIdentity{}, errProcessGone
		},
		now: func() time.Time { return time.Unix(5000, 0) },
	}
}

func kernelWith(rows ...procRow) *fakeKernel {
	k := &fakeKernel{procs: map[int]ProcessIdentity{100: testLeader}, errs: map[int]error{}}
	k.table = append([]procRow{{PID: 100, PPID: 1, PGID: 100, Start: 1000}}, rows...)
	for _, r := range rows {
		k.procs[r.PID] = ProcessIdentity{PID: r.PID, Start: r.Start, PGID: r.PGID}
	}
	return k
}

func alivePIDs(d DescendantRecord) []int {
	var out []int
	for _, e := range d.Alive {
		out = append(out, e.PID)
	}
	return out
}

func TestScanDescendants(t *testing.T) {
	escaped := procRow{PID: 150, PPID: 100, PGID: 150, Start: 1500}
	recorded := DescendantRecord{Alive: []ObservedProcess{{PID: 150, Start: 1500, PPID: 100, PGID: 150}}}
	t.Run("records an escaped child", func(t *testing.T) {
		got := scanDescendants(kernelWith(escaped).observer(), testLeader, DescendantRecord{})
		if fmt.Sprint(alivePIDs(got)) != "[150]" || got.ScanSeq != 1 || got.LastError != "" || !got.Alive[0].FirstSeen.Equal(time.Unix(5000, 0)) {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("absence from a table is not gone", func(t *testing.T) {
		k := kernelWith(escaped)
		k.table = k.table[:1] // the table misses it; its own pid still answers
		got := scanDescendants(k.observer(), testLeader, recorded)
		if fmt.Sprint(alivePIDs(got)) != "[150]" || got.Ended != 0 {
			t.Fatalf("a table that missed a live recorded descendant dropped it: %+v", got)
		}
	})
	t.Run("observed gone is dropped and counted", func(t *testing.T) {
		got := scanDescendants(kernelWith().observer(), testLeader, recorded)
		if len(got.Alive) != 0 || got.Ended != 1 {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("recycled is dropped and counted", func(t *testing.T) {
		k := kernelWith()
		k.procs[150] = ProcessIdentity{PID: 150, Start: 9999, PGID: 150}
		got := scanDescendants(k.observer(), testLeader, recorded)
		if len(got.Alive) != 0 || got.Ended != 1 {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("an observation error keeps the identity and blocks nothing", func(t *testing.T) {
		// Codex code review P2, 2026-10-12: a transient error on a RECORDED
		// identity used to set the unresolved error, which a sealed record kept
		// and every later resume refused on, even after that process ended.
		// Nothing was undiscovered; the identity is kept and a resume observes
		// it again.
		k := kernelWith(escaped)
		k.table = k.table[:1] // only its kept entry can carry it through this scan
		k.errs[150] = errors.New("eio")
		got := scanDescendants(k.observer(), testLeader, recorded)
		if fmt.Sprint(alivePIDs(got)) != "[150]" || got.ScanErrors != 1 || got.LastError != "" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("a failed table read is unresolved until a complete scan", func(t *testing.T) {
		k := kernelWith(escaped, procRow{PID: 151, PPID: 100, PGID: 151, Start: 1501})
		k.tableErr = errors.New("sysctl failed")
		got := scanDescendants(k.observer(), testLeader, recorded)
		if fmt.Sprint(alivePIDs(got)) != "[150]" || got.ScanErrors != 1 || !strings.Contains(got.LastError, "process table: sysctl failed") {
			t.Fatalf("got %+v", got)
		}
		// A later complete scan clears the unresolved error, not the count.
		got = scanDescendants(kernelWith(escaped).observer(), testLeader, got)
		if got.LastError != "" || got.ScanErrors != 1 {
			t.Fatalf("a complete scan left %+v", got)
		}
	})
	t.Run("a parent whose pid changed hands mid-read attributes nothing", func(t *testing.T) {
		// Codex design review Q1: the table shows in-group 500 under the leader
		// and escaped 501 under 500, but 500 exited and its pid went to a
		// stranger during the read. Re-observing 500 after the read catches it.
		middle := procRow{PID: 500, PPID: 100, PGID: 100, Start: 1500}
		child := procRow{PID: 501, PPID: 500, PGID: 501, Start: 1600}
		k := kernelWith(middle, child)
		if got := scanDescendants(k.observer(), testLeader, DescendantRecord{}); fmt.Sprint(alivePIDs(got)) != "[501]" {
			t.Fatalf("control: an intact chain attributed %v", alivePIDs(got))
		}
		k.procs[500] = ProcessIdentity{PID: 500, Start: 1550, PGID: 500}
		if got := scanDescendants(k.observer(), testLeader, DescendantRecord{}); len(got.Alive) != 0 {
			t.Fatalf("a recycled intermediate attributed %v", alivePIDs(got))
		}
		delete(k.procs, 500)
		if got := scanDescendants(k.observer(), testLeader, DescendantRecord{}); len(got.Alive) != 0 {
			t.Fatalf("a vanished intermediate attributed %v", alivePIDs(got))
		}
	})
	t.Run("an ancestor that cannot be observed is an unresolved error", func(t *testing.T) {
		// Codex code review, 2026-10-12: the chain check discarded its own
		// observation errors, so a descendant went unrecorded and nothing said
		// the scan was incomplete.
		k := kernelWith(procRow{PID: 500, PPID: 100, PGID: 100, Start: 1500}, procRow{PID: 501, PPID: 500, PGID: 501, Start: 1600})
		k.errs[500] = errors.New("eio")
		got := scanDescendants(k.observer(), testLeader, DescendantRecord{})
		if len(got.Alive) != 0 || !strings.Contains(got.LastError, "pid 500: eio") || got.ScanErrors != 1 {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("a recorded pid the table shows held by a newer process is replaced, not named twice", func(t *testing.T) {
		// Codex code review, 2026-10-12: the recorded process passed its own
		// check, then exited and its pid went to a new escaped descendant
		// before the table read. Both were recorded, and a record naming one
		// pid twice refuses every resume, permanently once sealed.
		k := kernelWith(procRow{PID: 150, PPID: 100, PGID: 150, Start: 1700})
		k.procs[150] = ProcessIdentity{PID: 150, Start: 1500, PGID: 150} // what its own check saw, before the read
		got := scanDescendants(k.observer(), testLeader, recorded)
		if len(got.Alive) != 1 || got.Alive[0].PID != 150 || got.Alive[0].Start != 1700 || got.Ended != 1 {
			t.Fatalf("got %+v", got)
		}
		if err := descendantShape(&got); err != nil {
			t.Fatalf("the scan wrote a record admission refuses: %v", err)
		}
		// An older start in the table is no evidence the recorded one ended.
		k = kernelWith(procRow{PID: 150, PPID: 100, PGID: 150, Start: 1400})
		k.procs[150] = ProcessIdentity{PID: 150, Start: 1500, PGID: 150}
		got = scanDescendants(k.observer(), testLeader, recorded)
		if len(got.Alive) != 1 || got.Alive[0].Start != 1500 || got.Ended != 0 {
			t.Fatalf("an older row replaced the recorded process: %+v", got)
		}
	})
	t.Run("capacity", func(t *testing.T) {
		full := func(n int) DescendantRecord {
			var d DescendantRecord
			for i := 0; i < n; i++ {
				d.Alive = append(d.Alive, ObservedProcess{PID: 1000 + i, Start: 2000, PPID: 100, PGID: 1000 + i})
			}
			return d
		}
		kernel := func(d DescendantRecord) *fakeKernel {
			k := kernelWith(escaped)
			for _, e := range d.Alive {
				k.procs[e.PID] = e.identity()
			}
			return k
		}
		below := full(maxRecordedDescendants - 1)
		if got := scanDescendants(kernel(below).observer(), testLeader, below); len(got.Alive) != maxRecordedDescendants || got.Overflow != 0 {
			t.Fatalf("at %d: alive %d overflow %d", maxRecordedDescendants-1, len(got.Alive), got.Overflow)
		}
		at := full(maxRecordedDescendants)
		got := scanDescendants(kernel(at).observer(), testLeader, at)
		if len(got.Alive) != maxRecordedDescendants || got.Overflow != 1 {
			t.Fatalf("at %d: alive %d overflow %d", maxRecordedDescendants, len(got.Alive), got.Overflow)
		}
		at.Overflow = math.MaxUint64
		if got := scanDescendants(kernel(at).observer(), testLeader, at); got.Overflow != math.MaxUint64 {
			t.Fatalf("overflow wrapped to %d", got.Overflow)
		}
	})
}

func TestClassifyOwnershipDescendants(t *testing.T) {
	entry := ObservedProcess{PID: 30, Start: 3, PPID: 20, PGID: 30}
	record := func(d *DescendantRecord) *OwnershipRecord {
		return &OwnershipRecord{Schema: OwnershipSchema, Attempt: "a", Phase: "spawned", Receipt: "/r", Adapter: ProcessIdentity{PID: 10, Start: 1},
			Leader: &ProcessIdentity{PID: 20, Start: 2, PGID: 20}, Descendants: d}
	}
	gone := func(states ...processState) ownershipFacts {
		f := ownershipFacts{Adapter: procGone, Leader: procGone, Group: groupEmpty}
		for _, s := range states {
			var err error
			if s == procError {
				err = errors.New("eio")
			}
			f.Descendants = append(f.Descendants, descendantFact{Entry: entry, State: s, Err: err})
		}
		return f
	}
	one := &DescendantRecord{Alive: []ObservedProcess{entry}}
	for _, tc := range []struct {
		name     string
		record   *OwnershipRecord
		facts    ownershipFacts
		admit    bool
		waitable bool
		says     string
	}{
		{"no descendant section admits", record(nil), gone(), true, false, ""},
		{"a recorded escaped descendant gone admits", record(one), gone(procGone), true, false, ""},
		{"recycled admits", record(one), gone(procRecycled), true, false, ""},
		{"alive refuses, waitably", record(one), gone(procAlive), false, true, "escaped descendant(s) of the prior worker, outside its group, are still running: pid 30"},
		{"an observation error refuses at once", record(one), gone(procError), false, false, "could not observe recorded escaped descendant pid 30"},
		{"a missing observation refuses", record(one), gone(), false, false, "not all observed"},
		{"overflow refuses at once", record(&DescendantRecord{Overflow: 1}), gone(), false, false, "dropped 1 observations"},
		{"an unresolved scan error refuses at once", record(&DescendantRecord{LastError: "process table: eio"}), gone(), false, false, "could not observe everything"},
		{"a descendant naming pid 1 refuses before any probe", record(&DescendantRecord{Alive: []ObservedProcess{{PID: 1, Start: 3}}}), gone(procGone), false, false, "identifies nothing"},
		{"a duplicate pid refuses", record(&DescendantRecord{Alive: []ObservedProcess{entry, entry}}), gone(procGone, procGone), false, false, "twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyOwnership(tc.record, tc.facts)
			if tc.admit {
				if got != nil {
					t.Fatalf("refused: %v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("admitted")
			}
			if got.Waitable != tc.waitable || !strings.Contains(got.Reason, tc.says) {
				t.Fatalf("waitable=%v reason=%q, want waitable=%v saying %q", got.Waitable, got.Reason, tc.waitable, tc.says)
			}
		})
	}
	t.Run("over the bound refuses", func(t *testing.T) {
		d := &DescendantRecord{}
		for i := 0; i <= maxRecordedDescendants; i++ {
			d.Alive = append(d.Alive, ObservedProcess{PID: 1000 + i, Start: 5})
		}
		if got := recordShape(record(d)); got == nil || !strings.Contains(got.Reason, "over the") {
			t.Fatalf("got %v", got)
		}
	})
}

// spawnedAuthorization is an authorization whose "worker" is this test
// process, so the record has a live, pinned leader without a child.
func spawnedAuthorization(t *testing.T) *LaunchAuthorization {
	t.Helper()
	auth := testAuthorization(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Spawn(self, func() (int, error) { return os.Getpid(), nil }, func() {}); err != nil {
		t.Fatal(err)
	}
	return auth
}

func recordedPIDs(t *testing.T, auth *LaunchAuthorization) []int {
	t.Helper()
	rec := readOwnership(t, auth.rec.receiptPath)
	if rec.Descendants == nil {
		return nil
	}
	return alivePIDs(*rec.Descendants)
}

// Codex design review P2: one mutex serializes publication but not
// observation order; a stale scan must never republish over a newer commit or
// over the close, and no writer may drop another's fields.
func TestDescendantPublicationFinality(t *testing.T) {
	obs := func(pid int) DescendantRecord {
		return DescendantRecord{ScanSeq: 1, Alive: []ObservedProcess{{PID: pid, Start: 7, PPID: 1, PGID: pid}}}
	}
	id := Identity{"buddy.lifecycle.v1", "codex", "thread-1", "inc-1"}
	t.Run("a stale scan is rejected", func(t *testing.T) {
		auth := spawnedAuthorization(t)
		_, _, gen, ok := auth.descendantState()
		if !ok || !auth.commitDescendants(gen, obs(4242)) {
			t.Fatal("control: the first commit was refused")
		}
		if auth.commitDescendants(gen, obs(4343)) {
			t.Fatal("a scan that started before another commit was installed over it")
		}
		if got := recordedPIDs(t, auth); fmt.Sprint(got) != "[4242]" {
			t.Fatalf("record holds %v", got)
		}
	})
	t.Run("the close seals the record and later writers keep its fields", func(t *testing.T) {
		auth := spawnedAuthorization(t)
		_, _, gen, _ := auth.descendantState()
		auth.commitDescendants(gen, obs(4242))
		if err := auth.sealClose(CloseReport{Trigger: "close", Survivors: []ObservedProcess{{PID: 5151, Start: 7}}, Unattributable: "unknown"}, obs(5151)); err != nil {
			t.Fatal(err)
		}
		if _, _, _, ok := auth.descendantState(); ok {
			t.Fatal("a sealed record still offered a scan its starting state")
		}
		// The current generation: only the seal can refuse this one.
		if auth.commitDescendants(auth.descGen, obs(4242)) {
			t.Fatal("a scan committed after the close sealed the record")
		}
		if err := auth.sealClose(CloseReport{}, DescendantRecord{}); err == nil {
			t.Fatal("a second close was accepted")
		}
		if err := auth.bind(id); err != nil {
			t.Fatal(err)
		}
		rec := readOwnership(t, auth.rec.receiptPath)
		if rec.Close == nil || len(rec.Close.Survivors) != 1 || rec.Identity == nil || !rec.Identity.Same(id) || fmt.Sprint(alivePIDs(*rec.Descendants)) != "[5151]" {
			t.Fatalf("after bind the record is %+v", rec)
		}
	})
	t.Run("a commit keeps the bound identity", func(t *testing.T) {
		auth := spawnedAuthorization(t)
		if err := auth.bind(id); err != nil {
			t.Fatal(err)
		}
		_, _, gen, _ := auth.descendantState()
		auth.commitDescendants(gen, obs(4242))
		rec := readOwnership(t, auth.rec.receiptPath)
		if rec.Identity == nil || !rec.Identity.Same(id) || rec.Phase != "spawned" || rec.Leader == nil {
			t.Fatalf("a descendant commit dropped another writer's fields: %+v", rec)
		}
	})
	t.Run("an unchanged scan is not republished", func(t *testing.T) {
		auth := spawnedAuthorization(t)
		_, _, gen, _ := auth.descendantState()
		auth.commitDescendants(gen, obs(4242))
		first := readOwnership(t, auth.rec.receiptPath).WrittenAt
		_, d, gen, _ := auth.descendantState()
		d.ScanSeq++
		auth.commitDescendants(gen, d)
		if got := readOwnership(t, auth.rec.receiptPath).WrittenAt; !got.Equal(first) {
			t.Fatalf("a scan that changed nothing republished (%v then %v)", first, got)
		}
	})
	t.Run("a failed publication is retried and the close reports it", func(t *testing.T) {
		auth := spawnedAuthorization(t)
		dir := filepath.Dir(auth.rec.receiptPath)
		if err := os.Chmod(dir, 0500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
		_, _, gen, _ := auth.descendantState()
		auth.commitDescendants(gen, obs(4242))
		if got := recordedPIDs(t, auth); len(got) != 0 {
			t.Fatalf("control: the unwritable directory was written: %v", got)
		}
		if err := os.Chmod(dir, 0700); err != nil {
			t.Fatal(err)
		}
		_, d, gen, _ := auth.descendantState()
		auth.commitDescendants(gen, d) // unchanged, but the record on disk is behind
		if got := recordedPIDs(t, auth); fmt.Sprint(got) != "[4242]" {
			t.Fatalf("a failed publication was not retried: %v", got)
		}
		if err := os.Chmod(dir, 0500); err != nil {
			t.Fatal(err)
		}
		if err := auth.sealClose(CloseReport{Trigger: "close", Unattributable: "unknown"}, d); err == nil {
			t.Fatal("control: the close published into an unwritable directory")
		}
		report, err := auth.closeOutcome()
		if report == nil || err == nil || !strings.Contains(err.Error(), "the close report could not be published") || !strings.Contains(err.Error(), "a resume reads that one") {
			t.Fatalf("closeOutcome = %+v, %v", report, err)
		}
	})
}

// watchProcesses installs hooks for the next StartJSONProcess and returns
// what they saw. Cleared at cleanup.
type processWatch struct {
	mu      sync.Mutex
	signals []string
	targets []int
	steps   []string
}

func watchProcesses(t *testing.T, scanEvery time.Duration) *processWatch {
	t.Helper()
	w := &processWatch{}
	processTestHooks.Store(&processHooks{
		signal: func(pid int, sig syscall.Signal) error {
			w.mu.Lock()
			if sig != 0 {
				w.targets = append(w.targets, pid)
				w.signals = append(w.signals, fmt.Sprintf("%d:%d", pid, sig))
			}
			w.mu.Unlock()
			return syscall.Kill(pid, sig)
		},
		trace: func(step string) {
			w.mu.Lock()
			w.steps = append(w.steps, step)
			w.mu.Unlock()
		},
		scanEvery: scanEvery,
	})
	t.Cleanup(func() { processTestHooks.Store(nil) })
	return w
}

func (w *processWatch) snapshot() (targets []int, steps []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]int(nil), w.targets...), append([]string(nil), w.steps...)
}

// escapedChild is the shell text for a python child that leaves the worker's
// session and group, as Claude Code's tool shells do (setsid), publishes its
// pid to a file by rename, then sleeps or waits for a file to appear.
func escapedChild(pidFile, until string) string {
	wait := "time.sleep(60)"
	if until != "" {
		wait = fmt.Sprintf("[time.sleep(0.01) for _ in iter(lambda: os.path.exists(%q), True)]", until)
	}
	return fmt.Sprintf(`python3 -c 'import os,time; os.setsid(); open(%q,"w").write(str(os.getpid())); os.rename(%q,%q); %s' </dev/null >/dev/null 2>&1 &`,
		pidFile+".tmp", pidFile+".tmp", pidFile, wait)
}

func readPIDFile(t *testing.T, path string) int {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if b, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(string(b)); err == nil && pid > 1 {
				t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("control: no pid in %s", path)
		}
	}
}

func waitRecorded(t *testing.T, auth *LaunchAuthorization, want func([]int) bool, what string) []int {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(auth.rec.path()); err == nil {
			if got := recordedPIDs(t, auth); want(got) {
				return got
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the ownership record never showed %s (last: %v)", what, recordedPIDs(t, auth))
		}
	}
}

func hasPID(pids []int, pid int) bool {
	for _, p := range pids {
		if p == pid {
			return true
		}
	}
	return false
}

// V-12 in a fixture: a worker with one escaped child that keeps running, one
// that ends while the worker still runs, and one in-group child. The sampler
// records the escaped ones and not the in-group one; Close ends the group,
// signals nothing else, and reports the survivor.
func TestJSONProcessRecordsAndReportsEscapedDescendants(t *testing.T) {
	w := watchProcesses(t, 50*time.Millisecond)
	dir := t.TempDir()
	stay, leave, goFile := filepath.Join(dir, "stay"), filepath.Join(dir, "leave"), filepath.Join(dir, "go")
	// The leader stays a shell (no exec), so it reaps the child that ends.
	script := escapedChild(stay, "") + escapedChild(leave, goFile) + ` sleep 60 </dev/null >/dev/null 2>&1 & echo "{\"ingroup\":$!}"; while :; do sleep 1; done`
	auth := testAuthorization(t)
	p, err := StartJSONProcess(context.Background(), auth, "/bin/sh", []string{"-c", script}, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var v struct {
		InGroup int `json:"ingroup"`
	}
	readJSONLine(t, p, &v)
	t.Cleanup(func() { _ = syscall.Kill(v.InGroup, syscall.SIGKILL) })
	stayPID, leavePID := readPIDFile(t, stay), readPIDFile(t, leave)
	got := waitRecorded(t, auth, func(p []int) bool { return hasPID(p, stayPID) && hasPID(p, leavePID) }, "both escaped children")
	if hasPID(got, v.InGroup) {
		t.Fatalf("the in-group child %d was recorded as escaped: %v", v.InGroup, got)
	}
	if err := os.WriteFile(goFile, nil, 0600); err != nil {
		t.Fatal(err)
	}
	waitRecorded(t, auth, func(p []int) bool { return !hasPID(p, leavePID) }, "the ended child dropped")
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := auth.closeOutcome()
	if err != nil || report == nil {
		t.Fatalf("close report: %+v, %v", report, err)
	}
	if report.Trigger != "close" || report.Unattributable != "unknown" || len(report.Captured) != 1 || report.Captured[0].PID != stayPID ||
		len(report.Survivors) != 1 || report.Survivors[0].PID != stayPID || len(report.Ended) != 0 || len(report.Unknown) != 0 {
		t.Fatalf("close report: %+v", report)
	}
	rec := readOwnership(t, auth.rec.receiptPath)
	if rec.Close == nil || rec.Descendants == nil || fmt.Sprint(alivePIDs(*rec.Descendants)) != fmt.Sprint([]int{stayPID}) || rec.Descendants.Ended < 1 {
		t.Fatalf("record after close: %+v", rec)
	}
	// Reported, not signalled: the survivor left the group and is not ours.
	if !alive(stayPID) {
		t.Fatalf("escaped survivor %d is gone after Close", stayPID)
	}
	if !waitGone(v.InGroup, 2*time.Second) {
		t.Fatalf("in-group child %d survived the group kill", v.InGroup)
	}
	targets, _ := w.snapshot()
	if len(targets) == 0 {
		t.Fatal("control: no signal was observed at all")
	}
	for _, target := range targets {
		if target != -p.pgid && target != p.PID() {
			t.Fatalf("a signal went to %d, which is neither the pinned group -%d nor the pinned leader %d (all: %v)", target, p.pgid, p.PID(), targets)
		}
	}
}

// The final capture JOINS the sampler: a scan still running when the shutdown
// began must finish before the capture reads its starting state, or the two
// race for the record and the capture's own result can be the one refused.
// The sampler is held mid-scan; Close must wait for it.
func TestJSONProcessFinalCaptureJoinsTheSampler(t *testing.T) {
	inScan, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	processTestHooks.Store(&processHooks{scanEvery: 20 * time.Millisecond, scanning: func() {
		once.Do(func() { close(inScan); <-release })
	}})
	t.Cleanup(func() { processTestHooks.Store(nil) })
	p, err := StartJSONProcess(context.Background(), testAuthorization(t), "/bin/sh", []string{"-c", `echo "{}"; while :; do sleep 1; done`}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var v struct{}
	readJSONLine(t, p, &v)
	select {
	case <-inScan:
	case <-time.After(5 * time.Second):
		t.Fatal("control: the sampler never scanned")
	}
	closed := make(chan struct{})
	go func() { _ = p.Close(); close(closed) }()
	select {
	case <-closed:
		close(release)
		t.Fatal("Close finished while the sampler was still mid-scan: the capture did not join it")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(terminateGrace + 5*time.Second):
		t.Fatal("Close did not finish once the sampler was released")
	}
}

// Every shutdown the adapter starts takes one order: admission off, capture
// completed, stdin closed, and only then the group signals. A worker that
// exits on stdin EOF otherwise reparents its children before the capture
// (Codex design review P2). No periodic scan runs here, so the escaped child
// can only be in the report if the capture preceded the stdin close.
func TestJSONProcessShutdownCapturesBeforeStdinCloses(t *testing.T) {
	for _, path := range []string{"close", "stdout_eof"} {
		t.Run(path, func(t *testing.T) {
			w := watchProcesses(t, time.Hour)
			dir := t.TempDir()
			pidFile := filepath.Join(dir, "escaped")
			closeOut := ""
			if path == "stdout_eof" {
				closeOut = "exec 1>&-; "
			}
			script := escapedChild(pidFile, "") + fmt.Sprintf(` while [ ! -s %q ]; do sleep 0.01; done; echo "{\"escaped\":$(cat %q)}"; %sread x; exit 0`, pidFile, pidFile, closeOut)
			auth := testAuthorization(t)
			p, err := StartJSONProcess(context.Background(), auth, "/bin/sh", []string{"-c", script}, dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			var v struct {
				Escaped int `json:"escaped"`
			}
			readJSONLine(t, p, &v)
			t.Cleanup(func() { _ = syscall.Kill(v.Escaped, syscall.SIGKILL) })
			if path == "close" {
				_ = p.Close()
			}
			select {
			case <-p.done:
			case <-time.After(terminateGrace + 5*time.Second):
				t.Fatal("the transport did not finish")
			}
			_, steps := w.snapshot()
			if got := strings.Join(steps, ","); got != "admission-off,captured,stdin-closed,group-term" {
				t.Fatalf("shutdown order %s", got)
			}
			report, err := auth.closeOutcome()
			if err != nil || report == nil || report.Trigger != path {
				t.Fatalf("close report %+v, %v", report, err)
			}
			if len(report.Captured) != 1 || report.Captured[0].PID != v.Escaped || len(report.Survivors) != 1 {
				t.Fatalf("the escaped child %d was not captured before stdin closed: %+v", v.Escaped, report)
			}
		})
	}
}

// Resume refuses while an escaped descendant the record names lives, even
// with the leader gone and its group empty, and signals nothing.
func TestControllerResumeRefusesWhileRecordedEscapedDescendantLives(t *testing.T) {
	setDescendants := func(t *testing.T, path string, d *DescendantRecord) {
		t.Helper()
		rec := readOwnership(t, path)
		rec.Descendants = d
		if err := (ownershipRecorder{receiptPath: path, journal: rec.Journal}).publish(rec); err != nil {
			t.Fatal(err)
		}
	}
	entry := func(id ProcessIdentity) *DescendantRecord {
		return &DescendantRecord{Alive: []ObservedProcess{{PID: id.PID, Start: id.Start, PPID: 1, PGID: id.PGID}}}
	}
	t.Run("alive refuses, then admits once it is gone", func(t *testing.T) {
		l, path := closedFixture(t)
		live, end := liveProcess(t)
		setDescendants(t, path, entry(live))
		_, c, err := resumeController(t, l, path)
		c.Close()
		var refusal *AdmissionRefusal
		if !errors.As(err, &refusal) || !refusal.Waitable || !strings.Contains(refusal.Reason, strconv.Itoa(live.PID)) || len(l.events) != 0 {
			t.Fatalf("a live recorded escaped descendant was admitted beside: err=%v events=%v", err, l.events)
		}
		if !alive(live.PID) {
			t.Fatal("the refusal signalled the recorded descendant")
		}
		end()
		if _, err := resumeWith(t, l, path); err != nil || len(l.events) != 1 {
			t.Fatalf("positive control: gone descendant still refused: %v", err)
		}
	})
	t.Run("recycled admits without signalling", func(t *testing.T) {
		l, path := closedFixture(t)
		live, _ := liveProcess(t)
		other := live
		other.Start--
		setDescendants(t, path, entry(other))
		if _, err := resumeWith(t, l, path); err != nil {
			t.Fatalf("a recycled descendant pid refused: %v", err)
		}
		if !alive(live.PID) {
			t.Fatal("a recycled pid was signalled")
		}
	})
	for _, tc := range []struct {
		name string
		d    *DescendantRecord
		says string
	}{
		{"overflow", &DescendantRecord{Overflow: 3}, "dropped 3"},
		{"unresolved scan error", &DescendantRecord{LastError: "process table: eio"}, "could not observe everything"},
		{"malformed", &DescendantRecord{Alive: []ObservedProcess{{PID: 0, Start: 1}}}, "identifies nothing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, path := closedFixture(t)
			setDescendants(t, path, tc.d)
			_, err := resumeWith(t, l, path)
			var refusal *AdmissionRefusal
			if !errors.As(err, &refusal) || refusal.Waitable || !strings.Contains(refusal.Reason, tc.says) || len(l.events) != 0 {
				t.Fatalf("err=%v events=%v", err, l.events)
			}
		})
	}
}
