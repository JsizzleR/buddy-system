package harness

// Tests for the ownership record and fail-closed resume (A-02). Each refusal
// has a positive control in the same table, so "it refused" and "it never ran"
// stay distinguishable; each guard was also mutated out and watched to fail
// (the D-077 continuation's mutation table).

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// testAuthorization mints a launch authorization for backend- and
// process-level tests that run without a controller. It records into a
// private directory bound to a journal nothing else reads.
func testAuthorization(t *testing.T) *LaunchAuthorization {
	t.Helper()
	path := filepath.Join(t.TempDir(), "receipt.json")
	j, err := os.Create(path + ".events")
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	journal, err := fileIdentity(j)
	if err != nil {
		t.Fatal(err)
	}
	return authorizationFor(t, ownershipRecorder{receiptPath: path, journal: journal}, "start", nil)
}

func authorizationFor(t *testing.T, rec ownershipRecorder, operation string, id *Identity) *LaunchAuthorization {
	t.Helper()
	self, err := observeProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := newAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	return &LaunchAuthorization{rec: rec, template: OwnershipRecord{Schema: OwnershipSchema, Attempt: attempt, Operation: operation, Journal: rec.journal, Receipt: rec.receiptPath, Identity: id, Adapter: self}}
}

var truePath = sync.OnceValue(func() string {
	p, err := exec.LookPath("true")
	if err != nil {
		return "/usr/bin/true"
	}
	return p
})

var sleepPath = sync.OnceValue(func() string {
	p, err := exec.LookPath("sleep")
	if err != nil {
		return "/bin/sleep"
	}
	return p
})

// fakeSpawn is how a fake backend goes through the authorization: a real,
// short-lived child is spawned in its own group and recorded, then reaped, so
// the record names a process that is gone and a later resume in the same test
// admits. Nothing records a worker without creating one.
func fakeSpawn(auth *LaunchAuthorization) error {
	cmd := exec.Command(truePath())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err := auth.Spawn(truePath(), func() (int, error) {
		if err := cmd.Start(); err != nil {
			return 0, err
		}
		return cmd.Process.Pid, nil
	}, func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if err != nil {
		return err
	}
	return cmd.Wait()
}

// goneProcess spawns and reaps a child, returning the identity it had: a pid
// that is gone (or, if recycled, carries another start time).
func goneProcess(t *testing.T) ProcessIdentity {
	t.Helper()
	cmd := exec.Command(truePath())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	id, err := observeProcess(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	return id
}

// liveProcess starts a sleeper in its own group and returns its identity; it
// is ended at cleanup, or earlier by the returned function.
func liveProcess(t *testing.T) (ProcessIdentity, func()) {
	t.Helper()
	cmd := exec.Command(sleepPath(), "300")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	id, err := observeProcess(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	end := func() {
		once.Do(func() {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})
	}
	t.Cleanup(end)
	return id, end
}

func journalIdentityOf(t *testing.T, receiptPath string) JournalIdentity {
	t.Helper()
	j, err := os.OpenFile(receiptPath+".events", os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	id, err := fileIdentity(j)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// writeGoneOwnership publishes a spawned record, bound to the receipt's
// journal and identity, whose adapter and leader are both gone: what a
// hand-built receipt needs for its resume to be admissible.
func writeGoneOwnership(t *testing.T, receiptPath string, id Identity) {
	t.Helper()
	writeOwnership(t, receiptPath, id, goneProcess(t), goneProcess(t))
}

func writeOwnership(t *testing.T, receiptPath string, id Identity, adapter, leader ProcessIdentity) OwnershipRecord {
	t.Helper()
	rec := ownershipRecorder{receiptPath: receiptPath, journal: journalIdentityOf(t, receiptPath)}
	exe, err := observeExecutableIdentity(truePath())
	if err != nil {
		t.Fatal(err)
	}
	record := OwnershipRecord{Schema: OwnershipSchema, Attempt: "fixture-attempt", Operation: "resume", Phase: "spawned", Journal: rec.journal, Receipt: receiptPath,
		Identity: &id, Adapter: adapter, WrittenAt: time.Now(), Leader: &leader, Executable: &exe}
	if err := rec.publish(record); err != nil {
		t.Fatal(err)
	}
	return record
}

func readOwnership(t *testing.T, receiptPath string) OwnershipRecord {
	t.Helper()
	b, err := os.ReadFile(receiptPath + ".proc")
	if err != nil {
		t.Fatal(err)
	}
	var rec OwnershipRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

// A started fixture whose fake leader is gone, closed, ready to be resumed by
// a second controller: the shape every resume case starts from.
func closedFixture(t *testing.T) (*fakeLedger, string) {
	t.Helper()
	c, _, l, path := controllerFixture(t)
	c.Close()
	return l, path
}

func resumeWith(t *testing.T, l *fakeLedger, path string) (*fakeBackend, error) {
	t.Helper()
	b, c, err := resumeController(t, l, path)
	t.Cleanup(func() { c.Close() })
	return b, err
}

func resumeController(t *testing.T, l *fakeLedger, path string) (*fakeBackend, *Controller, error) {
	t.Helper()
	b := &fakeBackend{}
	c, err := NewController(b, l, path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Resume(context.Background())
	return b, c, err
}

// The record exists, phase spawned, with the right pid, durable on disk,
// before the worker receives its first byte: the helper child reports what
// `.proc` said when it was still waiting for its first input.
func TestOwnershipRecordPrecedesFirstNativeByte(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	auth := testAuthorization(t)
	procPath := auth.rec.path()
	p, err := StartJSONProcess(context.Background(), auth, exe, []string{"-test.run=^TestJSONProcessHelper$"}, t.TempDir(),
		[]string{"BUDDY_PROCESS_HELPER=1", "BUDDY_PROCESS_MODE=ownership", "BUDDY_PROCESS_RECORD=" + procPath})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	// The first byte the worker receives is this write; the helper read the
	// record BEFORE it blocked on stdin, so what it echoes is the pre-byte state.
	if err := p.Write(map[string]string{"first": "byte"}); err != nil {
		t.Fatal(err)
	}
	var seen struct {
		Record string `json:"record"`
		PID    int    `json:"pid"`
	}
	readJSONLine(t, p, &seen)
	var rec OwnershipRecord
	if err := json.Unmarshal([]byte(seen.Record), &rec); err != nil {
		t.Fatalf("worker saw no complete record before its first byte: %q: %v", seen.Record, err)
	}
	if rec.Phase != "spawned" || rec.Leader == nil || rec.Leader.PID != seen.PID || rec.Leader.PID != p.PID() || rec.Leader.PGID != p.PID() || rec.Schema != OwnershipSchema {
		t.Fatalf("record before the first byte: %+v (worker pid %d)", rec, seen.PID)
	}
	if rec.Executable == nil || rec.Executable.Path != exe {
		t.Fatalf("record names executable %+v, want %s", rec.Executable, exe)
	}
	if !auth.Published() {
		t.Fatal("authorization not published after a recorded spawn")
	}
	if _, err := os.Stat(procPath + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("publication left its temporary file")
	}
}

// A second launch on the same authorization is refused before any child.
func TestLaunchAuthorizationIsSingleUse(t *testing.T) {
	auth := testAuthorization(t)
	if err := fakeSpawn(auth); err != nil {
		t.Fatalf("positive control: first launch refused: %v", err)
	}
	first, err := os.ReadFile(auth.rec.path())
	if err != nil {
		t.Fatal(err)
	}
	started := false
	err = auth.Spawn(truePath(), func() (int, error) { started = true; return 0, errors.New("must not run") }, func() {})
	if err == nil || started || !strings.Contains(err.Error(), "already consumed") {
		t.Fatalf("second launch on one authorization: err=%v started=%v", err, started)
	}
	if again, err := os.ReadFile(auth.rec.path()); err != nil || string(again) != string(first) {
		t.Fatalf("refused second launch rewrote the record: %s -> %s (%v)", first, again, err)
	}
	// And no authorization at all is refused the same way, before a child.
	started = false
	if err := (*LaunchAuthorization)(nil).Spawn(truePath(), func() (int, error) { started = true; return 0, nil }, func() {}); err == nil || started {
		t.Fatalf("nil authorization spawned: err=%v started=%v", err, started)
	}
}

// The launch path itself refuses a missing or unminted authorization before
// any child: the executable here would leave a marker if it ever ran.
func TestStartJSONProcessRefusesUnauthorizedLaunch(t *testing.T) {
	for _, name := range []string{"nil", "zero-value", "control"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "ran")
			var auth *LaunchAuthorization
			switch name {
			case "zero-value":
				auth = &LaunchAuthorization{}
			case "control":
				auth = testAuthorization(t)
			}
			p, err := StartJSONProcess(context.Background(), auth, "/bin/sh", []string{"-c", "echo '{}' > " + marker + "; exec sleep 30"}, dir, nil)
			if name == "control" {
				if err != nil {
					t.Fatalf("control: minted authorization refused: %v", err)
				}
				defer p.Close()
				for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
					if _, err := os.Stat(marker); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("control: the authorized child never ran")
					}
				}
				return
			}
			if err == nil || p != nil {
				t.Fatalf("%s authorization launched: %v", name, err)
			}
			time.Sleep(200 * time.Millisecond)
			if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
				t.Fatalf("%s authorization: a child ran before the refusal", name)
			}
			if _, statErr := os.Stat(".proc"); !os.IsNotExist(statErr) {
				t.Fatalf("%s authorization published a record in the working directory", name)
			}
		})
	}
}

// Persistence failures: intent cannot be written -> no child is created;
// the spawned record cannot be written -> the child is ended, the start
// fails, no live child remains, and the record on disk is the intent.
func TestLaunchAuthorizationPersistenceFailureLeavesNoLiveChild(t *testing.T) {
	for _, phase := range []string{"intent", "spawned", "control"} {
		t.Run(phase, func(t *testing.T) {
			auth := testAuthorization(t)
			dir := filepath.Dir(auth.rec.receiptPath)
			if phase == "intent" {
				if err := os.Chmod(dir, 0500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
			}
			cmd := exec.Command(sleepPath(), "300")
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			started, ended := false, false
			err := auth.Spawn(sleepPath(), func() (int, error) {
				started = true
				if err := cmd.Start(); err != nil {
					return 0, err
				}
				if phase == "spawned" {
					// The directory turns read-only after the child exists, so
					// only the spawned publication fails.
					if err := os.Chmod(dir, 0500); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
				}
				return cmd.Process.Pid, nil
			}, func() {
				ended = true
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			})
			if cmd.Process != nil {
				t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Process.Kill() })
			}
			switch phase {
			case "intent":
				if err == nil || started || !strings.Contains(err.Error(), "no worker was started") {
					t.Fatalf("intent failure: err=%v started=%v", err, started)
				}
				if _, statErr := os.Stat(auth.rec.path()); !os.IsNotExist(statErr) {
					t.Fatal("a record appeared although intent could not be written")
				}
			case "spawned":
				if err == nil || !started || !ended || auth.Published() {
					t.Fatalf("spawned failure: err=%v started=%v ended=%v published=%v", err, started, ended, auth.Published())
				}
				if syscall.Kill(cmd.Process.Pid, 0) == nil {
					t.Fatalf("child %d is still alive after its record could not be published", cmd.Process.Pid)
				}
				_ = os.Chmod(dir, 0700)
				if rec := readOwnership(t, auth.rec.receiptPath); rec.Phase != "intent" {
					t.Fatalf("record after a failed spawned publication is %q, want the intent", rec.Phase)
				}
			case "control":
				if err != nil || !started || ended || !auth.Published() {
					t.Fatalf("control: err=%v started=%v ended=%v published=%v", err, started, ended, auth.Published())
				}
				if rec := readOwnership(t, auth.rec.receiptPath); rec.Phase != "spawned" || rec.Leader.PID != cmd.Process.Pid {
					t.Fatalf("control record: %+v", rec)
				}
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				_ = cmd.Wait()
			}
		})
	}
}

// The pure classifier over fixed observations.
func TestClassifyOwnership(t *testing.T) {
	spawned := func() *OwnershipRecord {
		return &OwnershipRecord{Schema: OwnershipSchema, Attempt: "a", Phase: "spawned", Receipt: "/r", Adapter: ProcessIdentity{PID: 10, Start: 1}, Leader: &ProcessIdentity{PID: 20, Start: 2, PGID: 20, SID: 5}}
	}
	gone := ownershipFacts{Adapter: procGone, Leader: procGone, Group: groupEmpty}
	for _, tc := range []struct {
		name     string
		record   func() *OwnershipRecord
		facts    ownershipFacts
		admit    bool
		waitable bool
		says     string
	}{
		{"all gone admits", spawned, gone, true, false, ""},
		{"recycled pids count as gone", spawned, ownershipFacts{Adapter: procRecycled, Leader: procRecycled, Group: groupEmpty}, true, false, ""},
		{"adapter alive", spawned, ownershipFacts{Adapter: procAlive, Leader: procGone, Group: groupEmpty}, false, true, "adapter"},
		{"adapter alive is this process", spawned, ownershipFacts{Adapter: procAlive, AdapterIsSelf: true, Leader: procGone, Group: groupEmpty}, true, false, ""},
		{"self does not excuse a live leader", spawned, ownershipFacts{Adapter: procAlive, AdapterIsSelf: true, Leader: procAlive, Group: groupMembers}, false, true, "leader is still running"},
		{"leader alive", spawned, ownershipFacts{Adapter: procGone, Leader: procAlive, Group: groupMembers}, false, true, "leader is still running"},
		{"leader gone, group has members", spawned, ownershipFacts{Adapter: procGone, Leader: procGone, Group: groupMembers}, false, true, "still has members"},
		{"group EPERM", spawned, ownershipFacts{Adapter: procGone, Leader: procGone, Group: groupEPERM}, false, true, "EPERM"},
		{"adapter observation error", spawned, ownershipFacts{Adapter: procError, AdapterErr: errors.New("eio"), Leader: procGone, Group: groupEmpty}, false, false, "could not observe the recorded adapter"},
		{"leader observation error", spawned, ownershipFacts{Adapter: procGone, Leader: procError, LeaderErr: errors.New("eio"), Group: groupEmpty}, false, false, "could not observe the recorded worker"},
		{"group probe error", spawned, ownershipFacts{Adapter: procGone, Leader: procGone, Group: groupError, GroupErr: errors.New("einval")}, false, false, "could not probe"},
		{"error outranks alive", spawned, ownershipFacts{Adapter: procAlive, Leader: procError, LeaderErr: errors.New("eio"), Group: groupMembers}, false, false, "could not observe"},
		{"intent-only", func() *OwnershipRecord { r := spawned(); r.Phase = "intent"; r.Leader = nil; return r }, gone, false, false, "intent-only"},
		{"unknown phase", func() *OwnershipRecord { r := spawned(); r.Phase = "ended"; return r }, gone, false, false, "phase"},
		{"spawned without leader", func() *OwnershipRecord { r := spawned(); r.Leader = nil; return r }, gone, false, false, "no usable leader"},
		{"group not the leader's own", func() *OwnershipRecord { r := spawned(); r.Leader.PGID = 21; return r }, gone, false, false, "not the recorded leader"},
		{"group id 1", func() *OwnershipRecord { r := spawned(); r.Leader.PID, r.Leader.PGID = 1, 1; return r }, gone, false, false, "not the recorded leader"},
		{"group id 0", func() *OwnershipRecord { r := spawned(); r.Leader.PID, r.Leader.PGID = 0, 0; return r }, gone, false, false, "not the recorded leader"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyOwnership(tc.record(), tc.facts)
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
	// The intent diagnostic names what the operator needs.
	r := classifyOwnership(&OwnershipRecord{Phase: "intent", Attempt: "att-1", Receipt: "/r", Adapter: ProcessIdentity{PID: 10, Start: 1}}, gone)
	for _, want := range []string{"/r.proc", "att-1", "pid 10", "start 1", "may have started", "verified nothing", "NEW receipt", "never delete"} {
		if !strings.Contains(r.Reason, want) {
			t.Fatalf("intent diagnostic lacks %q: %s", want, r.Reason)
		}
	}
}

// The resume matrix against real processes, through the controller.
func TestControllerResumeOwnershipMatrix(t *testing.T) {
	for _, name := range []string{"gone", "no-record", "other-journal", "other-identity", "unbound-identity", "intent-only", "adapter-alive", "leader-alive", "leader-exited-tool-alive", "recycled-group-id", "newer-intent-supersedes"} {
		t.Run(name, func(t *testing.T) {
			l, path := closedFixture(t)
			id := l.identity
			switch name {
			case "no-record":
				if err := os.Remove(path + ".proc"); err != nil {
					t.Fatal(err)
				}
			case "other-journal":
				// The record of a different receipt, copied here.
				other := filepath.Join(t.TempDir(), "other.json")
				writeGoneOwnership(t, other, id)
				b, err := os.ReadFile(other + ".proc")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path+".proc", b, 0600); err != nil {
					t.Fatal(err)
				}
			case "other-identity":
				writeOwnership(t, path, Identity{"buddy.lifecycle.v1", id.Harness, id.SessionID, "successor"}, goneProcess(t), goneProcess(t))
			case "unbound-identity":
				rec := readOwnership(t, path)
				rec.Identity = nil
				if err := (ownershipRecorder{receiptPath: path, journal: rec.Journal}).publish(rec); err != nil {
					t.Fatal(err)
				}
			case "intent-only":
				rec := readOwnership(t, path)
				rec.Phase, rec.Leader, rec.Executable = "intent", nil, nil
				if err := (ownershipRecorder{receiptPath: path, journal: rec.Journal}).publish(rec); err != nil {
					t.Fatal(err)
				}
			case "adapter-alive":
				adapter, _ := liveProcess(t)
				writeOwnership(t, path, id, adapter, goneProcess(t))
			case "leader-alive":
				leader, _ := liveProcess(t)
				writeOwnership(t, path, id, goneProcess(t), leader)
			case "leader-exited-tool-alive":
				// A shell leader that starts a tool in its group and exits.
				cmd := exec.Command("/bin/sh", "-c", `sleep 300 & echo $!; exit 0`)
				cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
				out, err := cmd.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				leader, err := observeProcess(cmd.Process.Pid)
				if err != nil {
					t.Fatal(err)
				}
				var tool int
				if err := json.NewDecoder(out).Decode(&tool); err != nil || tool == 0 {
					t.Fatalf("tool pid: %d %v", tool, err)
				}
				_ = cmd.Wait()
				t.Cleanup(func() { _ = syscall.Kill(tool, syscall.SIGKILL) })
				if syscall.Kill(tool, 0) != nil {
					t.Fatal("control: the in-group tool is not alive")
				}
				writeOwnership(t, path, id, goneProcess(t), leader)
			case "recycled-group-id":
				// A pid that exists with a different start time is "recycled":
				// not ours and never signalled. Here the recycled pid's own
				// group also exists, and that must still refuse (the group
				// check does not know the group is not ours). Modelled with a
				// live sleeper whose recorded start time is wrong by one.
				leader, _ := liveProcess(t)
				leader.Start--
				writeOwnership(t, path, id, goneProcess(t), leader)
			case "newer-intent-supersedes":
				// An older spawned record naming a gone leader is replaced by
				// a newer attempt's intent, as every spawn does first.
				rec := readOwnership(t, path)
				rec.Attempt, rec.Phase, rec.Leader, rec.Executable = "newer", "intent", nil, nil
				if err := (ownershipRecorder{receiptPath: path, journal: rec.Journal}).publish(rec); err != nil {
					t.Fatal(err)
				}
			}
			b, err := resumeWith(t, l, path)
			if name == "gone" {
				if err != nil || len(l.events) != 1 {
					t.Fatalf("positive control: gone worker refused: %v events=%v", err, l.events)
				}
				return
			}
			if err == nil || len(l.events) != 0 {
				t.Fatalf("%s admitted: err=%v ledger events=%v", name, err, l.events)
			}
			var refusal *AdmissionRefusal
			if !errors.As(err, &refusal) {
				t.Fatalf("%s refused by something other than admission: %v", name, err)
			}
			waitable := name == "adapter-alive" || name == "leader-alive" || name == "leader-exited-tool-alive" || name == "recycled-group-id"
			if refusal.Waitable != waitable {
				t.Fatalf("%s: waitable=%v, want %v: %s", name, refusal.Waitable, waitable, refusal.Reason)
			}
			says := map[string]string{"no-record": ".proc", "other-journal": "another journal", "other-identity": "another one than the receipt", "unbound-identity": "names no captured identity",
				"intent-only": "intent-only", "adapter-alive": "adapter", "leader-alive": "leader is still running", "leader-exited-tool-alive": "still has members", "recycled-group-id": "still has members", "newer-intent-supersedes": "intent-only"}[name]
			if !strings.Contains(refusal.Reason, says) {
				t.Fatalf("%s refusal does not say %q: %s", name, says, refusal.Reason)
			}
			_ = b
		})
	}
}

// A recycled pid (exists, different start time) is treated as gone and never
// signalled, when its numeric group is also gone: a live process whose start
// time differs from the record is left alone and the resume admits.
func TestControllerResumeAdmitsRecycledLeaderPid(t *testing.T) {
	l, path := closedFixture(t)
	// The leader "pid" is this test process with a wrong start time, whose
	// own group is NOT its pid (it is a child of go test), so the group
	// check sees no group under that number... unless it does: then skip,
	// because the observation is honest and the case cannot be modelled here.
	self, err := observeProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if self.PGID == self.PID {
		t.Skip("test process leads its own group; the recycled-pid case needs a pid whose group is gone")
	}
	if syscall.Kill(-self.PID, 0) == nil {
		t.Skip("a group numbered like this process exists")
	}
	leader := ProcessIdentity{PID: self.PID, Start: self.Start - 1, PGID: self.PID, SID: self.SID}
	writeOwnership(t, path, l.identity, goneProcess(t), leader)
	if _, err := resumeWith(t, l, path); err != nil {
		t.Fatalf("recycled pid refused: %v", err)
	}
}

// Crash at each boundary: what the next resume says. The boundaries are
// produced by the authorization's own phases, not by hand-written files.
func TestControllerResumeAfterCrashAtEachBoundary(t *testing.T) {
	for _, boundary := range []string{"after-intent-before-spawn", "after-spawn-before-spawned-record", "after-spawned-record"} {
		t.Run(boundary, func(t *testing.T) {
			l, path := closedFixture(t)
			// A second adapter resumes and crashes at the boundary: model it
			// with the authorization run against a controller that is then
			// abandoned without Close (its journal lock dies with the test's
			// file handle, as a SIGKILLed adapter's does).
			b := &fakeBackend{}
			c, err := NewController(b, l, path)
			if err != nil {
				t.Fatal(err)
			}
			auth, err := c.authorize("resume", &l.identity)
			if err != nil {
				t.Fatal(err)
			}
			var worker *exec.Cmd
			spawnErr := auth.Spawn(sleepPath(), func() (int, error) {
				if boundary == "after-intent-before-spawn" {
					return 0, errors.New("crash before spawn")
				}
				worker = exec.Command(sleepPath(), "300")
				worker.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
				if err := worker.Start(); err != nil {
					return 0, err
				}
				if boundary == "after-spawn-before-spawned-record" {
					// The crash: the spawned record is never written. Modelled
					// by making its publication impossible.
					if err := os.Chmod(filepath.Dir(path), 0500); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(filepath.Dir(path), 0700) })
				}
				return worker.Process.Pid, nil
			}, func() {
				// A crashed adapter ends nothing; the worker keeps running.
			})
			if worker != nil {
				t.Cleanup(func() {
					_ = syscall.Kill(-worker.Process.Pid, syscall.SIGKILL)
					_ = worker.Process.Kill()
					_ = worker.Wait()
				})
			}
			_ = os.Chmod(filepath.Dir(path), 0700)
			c.journal.Close() // the crash releases the flock; nothing else runs
			if boundary == "after-spawned-record" && spawnErr != nil {
				t.Fatal(spawnErr)
			}
			_, refused, err := resumeController(t, l, path)
			refused.Close() // releases the flock for the positive control below
			if err == nil {
				t.Fatalf("resume admitted after a crash %s", boundary)
			}
			var refusal *AdmissionRefusal
			if !errors.As(err, &refusal) {
				t.Fatalf("refused by something other than admission: %v", err)
			}
			if boundary == "after-spawned-record" {
				if !refusal.Waitable || !strings.Contains(refusal.Reason, "leader is still running") {
					t.Fatalf("live worker after a recorded spawn: %v", refusal)
				}
				// Positive control: once the worker is gone the record admits.
				_ = syscall.Kill(-worker.Process.Pid, syscall.SIGKILL)
				_ = worker.Process.Kill()
				_ = worker.Wait()
				if _, err := resumeWith(t, l, path); err != nil {
					t.Fatalf("admission after the recorded worker exited: %v", err)
				}
				return
			}
			if refusal.Waitable || !strings.Contains(refusal.Reason, "intent-only") {
				t.Fatalf("crash %s: %v", boundary, refusal)
			}
		})
	}
}

// A backend that returns a worker without spawning through its authorization
// is refused and closed, on start and on resume.
func TestControllerRefusesUnrecordedBackendLaunch(t *testing.T) {
	ctx := context.Background()
	t.Run("start", func(t *testing.T) {
		b, l := &fakeBackend{unrecorded: true}, &fakeLedger{}
		path := filepath.Join(t.TempDir(), "receipt.json")
		c, err := NewController(b, l, path)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if _, err = c.Start(ctx, "codex", Launch{Worktree: t.TempDir()}, "test"); err == nil || !strings.Contains(err.Error(), "without a published ownership record") || l.identity.Valid() {
			t.Fatalf("unrecorded start: err=%v begun=%v", err, l.identity.Valid())
		}
	})
	t.Run("resume", func(t *testing.T) {
		l, path := closedFixture(t)
		b := &fakeBackend{unrecorded: true}
		c, err := NewController(b, l, path)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if _, err = c.Resume(ctx); err == nil || !strings.Contains(err.Error(), "without a published ownership record") {
			t.Fatalf("unrecorded resume: %v", err)
		}
	})
}

// Start binds the record to the captured identity after the receipt is
// published; a start-written record is admissible by exactly that.
func TestStartWrittenRecordCarriesIdentity(t *testing.T) {
	c, _, l, path := controllerFixture(t)
	rec := readOwnership(t, path)
	if rec.Operation != "start" || rec.Phase != "spawned" || rec.Identity == nil || !rec.Identity.Same(l.identity) {
		t.Fatalf("start record: %+v", rec)
	}
	if rec.Receipt != path || rec.Journal != c.recorder.journal || rec.Adapter.PID != os.Getpid() {
		t.Fatalf("start record binding: %+v", rec)
	}
	if _, err := os.Stat(path + ".start"); !os.IsNotExist(err) {
		t.Fatal(".start left behind")
	}
	// A record left without a receipt refuses a fresh start over it.
	other := filepath.Join(t.TempDir(), "r.json")
	c2, err := NewController(&fakeBackend{}, &fakeLedger{}, other)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	writeGoneOwnership(t, other, l.identity)
	if _, err = c2.Start(context.Background(), "codex", Launch{Worktree: t.TempDir()}, "t"); err == nil || !strings.Contains(err.Error(), "without a receipt") {
		t.Fatalf("start over a record with no receipt: %v", err)
	}
}

// --wait-prior: admits after the prior leader exits within the bound; refuses
// on timeout; refuses immediately on a non-waitable refusal; waits through
// flock contention; and a bound that has already expired refuses even an
// admissible receipt (Codex code review P2, 2026-10-07: AwaitPrior returned
// success without consulting ctx.Err(), so `--wait-prior=1ns` proceeded).
func TestNewControllerAfterPrior(t *testing.T) {
	for _, name := range []string{"admits-once-gone", "timeout", "non-waitable", "flock", "expired-admissible"} {
		t.Run(name, func(t *testing.T) {
			l, path := closedFixture(t)
			var end func()
			var holder *Controller
			switch name {
			case "admits-once-gone", "timeout":
				var leader ProcessIdentity
				leader, end = liveProcess(t)
				writeOwnership(t, path, l.identity, goneProcess(t), leader)
			case "non-waitable":
				if err := os.Remove(path + ".proc"); err != nil {
					t.Fatal(err)
				}
			case "flock":
				var err error
				holder, err = NewController(&fakeBackend{}, l, path)
				if err != nil {
					t.Fatal(err)
				}
			}
			bound := 5 * time.Second
			if name == "timeout" {
				bound = 700 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), bound)
			defer cancel()
			if name == "expired-admissible" {
				cancel() // the bound is already over before the first attempt
			}
			type result struct {
				c   *Controller
				err error
				at  time.Duration
			}
			began := time.Now()
			done := make(chan result, 1)
			go func() {
				c, err := NewControllerAfterPrior(ctx, &fakeBackend{}, l, path)
				done <- result{c, err, time.Since(began)}
			}()
			release := func() {
				time.Sleep(600 * time.Millisecond)
				select {
				case r := <-done:
					t.Errorf("returned before the prior cleared: %v %v", r.c, r.err)
				default:
				}
			}
			switch name {
			case "admits-once-gone":
				release()
				end()
			case "flock":
				release()
				holder.Close()
			}
			var r result
			select {
			case r = <-done:
			case <-time.After(bound + 2*time.Second):
				t.Fatal("wait did not return at its bound")
			}
			if r.c != nil {
				defer r.c.Close()
			}
			switch name {
			case "admits-once-gone", "flock":
				if r.err != nil || r.c == nil {
					t.Fatalf("did not admit: %v", r.err)
				}
				if _, err := r.c.Resume(context.Background()); err != nil {
					t.Fatalf("resume after the wait: %v", err)
				}
			case "timeout":
				if r.err == nil || !strings.Contains(r.err.Error(), "waited") || !strings.Contains(r.err.Error(), "leader is still running") || r.at < 650*time.Millisecond {
					t.Fatalf("timeout: err=%v after %v", r.err, r.at)
				}
				if len(l.events) != 0 {
					t.Fatalf("the wait wrote to the ledger: %v", l.events)
				}
			case "non-waitable":
				if r.err == nil || !strings.Contains(r.err.Error(), ".proc") || r.at > 2*time.Second {
					t.Fatalf("non-waitable refusal did not return at once: err=%v after %v", r.err, r.at)
				}
			case "expired-admissible":
				if r.err == nil || r.c != nil || !strings.Contains(r.err.Error(), "waited") {
					t.Fatalf("an expired bound admitted: err=%v controller=%v", r.err, r.c)
				}
				// Positive control: the same receipt admits under a live bound,
				// so the refusal above was the bound's, and the controller the
				// refusal acquired was closed (the flock is free).
				c2, err := NewControllerAfterPrior(context.Background(), &fakeBackend{}, l, path)
				if err != nil {
					t.Fatalf("control: admissible receipt refused: %v", err)
				}
				c2.Close()
			}
		})
	}
}

// The journal binding is the flocked file's device and inode, and an
// interrupted publication's .tmp is removed when the lock is taken.
func TestOwnershipRecorderCrashConsistency(t *testing.T) {
	l, path := closedFixture(t)
	if err := os.WriteFile(path+".proc.tmp", []byte("torn"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := NewController(&fakeBackend{}, l, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := os.Stat(path + ".proc.tmp"); !os.IsNotExist(err) {
		t.Fatal("stale .proc.tmp survived the lock")
	}
	if _, err := c.Resume(context.Background()); err != nil {
		t.Fatalf("the torn temporary file affected the record: %v", err)
	}
}

func TestObserveProcessPinsByStartTime(t *testing.T) {
	live, end := liveProcess(t)
	again, err := observeProcess(live.PID)
	if err != nil || again != live || live.PGID != live.PID || live.Start == 0 {
		t.Fatalf("live: %+v then %+v (%v)", live, again, err)
	}
	if state, err := observeState(live); state != procAlive || err != nil {
		t.Fatalf("live state %v %v", state, err)
	}
	if state, _ := observeState(ProcessIdentity{PID: live.PID, Start: live.Start + 1}); state != procRecycled {
		t.Fatalf("changed start time read as %v", state)
	}
	if g, err := probeGroup(live.PGID); g != groupMembers || err != nil {
		t.Fatalf("live group %v %v", g, err)
	}
	end()
	if state, err := observeState(live); state != procGone || err != nil {
		t.Fatalf("reaped: %v %v", state, err)
	}
	if g, err := probeGroup(live.PGID); g != groupEmpty || err != nil {
		t.Fatalf("reaped group %v %v", g, err)
	}
	if g, _ := probeGroup(1); g != groupError {
		t.Fatal("group 1 was probed")
	}
	if g, _ := probeGroup(0); g != groupError {
		t.Fatal("group 0 was probed")
	}
}

// Measured 2026-10-07 (darwin 27): kill(-pgid, 0) on a group whose only member
// is an unreaped zombie answers EPERM, not ESRCH. Treating anything but ESRCH
// as absence would admit while the leader's zombie still reserves the group.
func TestProbeGroupZombieOnlyIsNotAbsence(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the zombie-group errno was measured on darwin only")
	}
	cmd := exec.Command(truePath())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	defer cmd.Wait()
	// Positive control first: while it may still run, the group has members
	// or is already zombie-only; it is never absent before the reap.
	deadline := time.Now().Add(5 * time.Second)
	for {
		g, err := probeGroup(pid)
		if g == groupEmpty {
			t.Fatalf("unreaped child's group read as absent (%v)", err)
		}
		if g == groupEPERM {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("child never became a zombie-only group: state %v %v", g, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if r := classifyOwnership(&OwnershipRecord{Phase: "spawned", Adapter: ProcessIdentity{PID: 1 << 30}, Leader: &ProcessIdentity{PID: pid, PGID: pid}}, ownershipFacts{Adapter: procGone, Leader: procAlive, Group: groupEPERM}); r == nil || !r.Waitable {
		t.Fatalf("zombie-only group admitted or not waitable: %v", r)
	}
}
