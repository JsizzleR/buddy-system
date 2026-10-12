package harness

// OWNERSHIP RECORD AND FAIL-CLOSED RESUME (audit A-02, D-077 continuation).
//
// The failure: SIGKILL the adapter while its Codex worker is mid-turn. The
// worker and its tool stay alive (their own process group; the adapter's death
// closes only their stdin). `--resume` then took the now-free journal flock,
// passed every check, and launched a SECOND app-server that received
// thread/resume while the first was still running (measured: 52349 launched
// while 51299 lived). Nothing could have refused: neither the receipt nor the
// journal named a native pid, and a free flock proves only that the adapter
// exited, never that its worker did.
//
// The contract now:
//   - `<receipt>.proc` is the ownership record. Every worker spawn, Start and
//     Resume alike, first publishes a durable INTENT for a fresh attempt, then
//     spawns, then publishes the SPAWNED record (leader pid, kernel start
//     time, pgid, sid, executable identity) before the process is returned to
//     the backend, so before the adapter writes its first native byte. The
//     worker may already be running in the gap between intent and spawned
//     record; the intent covers that gap by refusing the next resume.
//   - The spawn is fenced by a single-use LaunchAuthorization the controller
//     mints under its lock and the journal flock, and the ONE launch path
//     (startJSONProcess) consumes before cmd.Start. A second launch on the
//     same authorization is refused before any child exists. The version
//     probe is a separate, unauthorized child and never touches the record.
//   - Resume admission is passive and decides once after every check: the
//     record must exist (a receipt from before records gets no automatic
//     resume), parse, bind to THIS journal (device + inode of the flocked
//     `.events`) and receipt path, name the saved identity, and be phase
//     spawned; then the recorded adapter must be gone, the recorded leader
//     gone (a pid whose start time differs is recycled: not ours, never
//     signalled, counts as gone), and the recorded group empty (kill(-pgid, 0)
//     answering ESRCH; only ESRCH is absence). Anything else refuses. It
//     signals nothing and writes nothing.
//
// What `.start` fences, answered (the design review's open question): `.start`
// fences only Start, until the receipt is published. Every spawn, resume
// included, is fenced by the flock plus intent-before-spawn: a later admission
// always sees the MOST RECENT attempt, never an older record naming a dead
// leader, because the newer intent replaced it before the newer worker could
// exist. Two resumes cannot both launch: the second either cannot take the
// flock, or reads the first's intent (refuse) or spawned record (adapter,
// leader or group alive: refuse).
//
// EXCLUDED, deliberately:
//   - Rollback of the record. Restoring an older `.proc` over a newer one, with
//     the newer worker alive, admits a resume. That is artifact tampering by
//     the machine user, whom the ledger trusts (invariant 6); no journal
//     attempt row is kept to detect it, and the bindings make no claim to.
//   - Descendants that leave the group (setsid, a shell's job control). The
//     group check cannot see them; the record says so rather than claiming a
//     clean tree.
//   - Killing anything the adapter cannot pin, `--end-prior`, descendant
//     sampling, Setsid: each waits on native qualification or the operator's
//     open choice. Resume admits only what is observed GONE.
//   - Platforms without a start-time pin (neither darwin nor linux): the launch
//     itself refuses, since nothing could later be admitted or ended safely.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
)

const OwnershipSchema = "buddy.ownership.v1"

// ProcessIdentity pins a process: a pid names one only together with its
// kernel start time (D-025). PGID is what Close signals and what admission
// probes; SID is diagnostic (unreadable once the process is a zombie, then 0).
type ProcessIdentity struct {
	PID   int   `json:"pid"`
	Start int64 `json:"start"`
	PGID  int   `json:"pgid,omitempty"`
	SID   int   `json:"sid,omitempty"`
}

// ExecutableIdentity is the resolved file that was exec'd (F-23's identity).
type ExecutableIdentity struct {
	Path    string `json:"path"`
	Dev     uint64 `json:"dev"`
	Ino     uint64 `json:"ino"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mtime_ns"`
}

// JournalIdentity binds a record to the one `.events` file whose flock the
// publishing adapter held: a record copied beside another receipt fails it.
type JournalIdentity struct {
	Dev uint64 `json:"dev"`
	Ino uint64 `json:"ino"`
}

type OwnershipRecord struct {
	Schema     string              `json:"schema"`
	Attempt    string              `json:"attempt"`
	Operation  string              `json:"operation"`
	Phase      string              `json:"phase"`
	Journal    JournalIdentity     `json:"journal"`
	Receipt    string              `json:"receipt"`
	Identity   *Identity           `json:"identity,omitempty"`
	Adapter    ProcessIdentity     `json:"adapter"`
	WrittenAt  time.Time           `json:"written_at"`
	Leader     *ProcessIdentity    `json:"leader,omitempty"`
	Executable *ExecutableIdentity `json:"executable,omitempty"`
}

// ErrJournalOwned is NewController's refusal while another adapter holds the
// journal flock. It is a sentinel so a `--wait-prior` can wait through it
// (and only through it): any other constructor failure refuses at once.
var ErrJournalOwned = errors.New("receipt journal is already owned by another adapter")

// errProcessGone is observeProcess's answer for a pid the kernel does not have.
var errProcessGone = errors.New("no such process")

// ownershipRecorder publishes `<receipt>.proc` crash-consistently: the bytes
// go to `.proc.tmp`, are fsynced, renamed over `.proc`, and the directory is
// fsynced. A reader therefore sees the old record or the new one, never a
// torn one; a leftover `.tmp` is an interrupted publication and is removed
// under the flock before anything reads the record.
type ownershipRecorder struct {
	receiptPath string
	journal     JournalIdentity
}

func (r ownershipRecorder) path() string { return r.receiptPath + ".proc" }

func (r ownershipRecorder) publish(rec OwnershipRecord) error {
	tmp := r.path() + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	err = json.NewEncoder(f).Encode(rec)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, r.path())
	}
	if err == nil {
		err = syncDirectory(r.path())
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (r ownershipRecorder) removeStaleTemp() {
	_ = os.Remove(r.path() + ".tmp")
}

// load reads the record and checks what needs no process observation: schema,
// binding to this journal and receipt, and that it names the saved identity.
// Every failure is a non-waitable refusal: waiting changes none of it.
func (r ownershipRecorder) load(saved Identity) (*OwnershipRecord, *AdmissionRefusal) {
	b, err := os.ReadFile(r.path())
	if os.IsNotExist(err) {
		return nil, &AdmissionRefusal{Reason: fmt.Sprintf("no ownership record at %s: this receipt predates ownership records or the record was removed, so the prior worker cannot be identified; a free journal lock proves only that the adapter exited, never that its worker did; no automatic resume", r.path())}
	}
	if err != nil {
		return nil, &AdmissionRefusal{Reason: "ownership record unreadable: " + err.Error()}
	}
	var rec OwnershipRecord
	if err := json.Unmarshal(b, &rec); err != nil || rec.Schema != OwnershipSchema || rec.Attempt == "" {
		return nil, &AdmissionRefusal{Reason: fmt.Sprintf("ownership record %s is not a %s record", r.path(), OwnershipSchema)}
	}
	if rec.Journal != r.journal || rec.Receipt != r.receiptPath {
		return nil, &AdmissionRefusal{Reason: fmt.Sprintf("ownership record %s is bound to another journal or receipt (journal %d:%d at %q, this journal %d:%d at %q); a record copied from a different receipt identifies nothing here", r.path(), rec.Journal.Dev, rec.Journal.Ino, rec.Receipt, r.journal.Dev, r.journal.Ino, r.receiptPath)}
	}
	if rec.Identity == nil || !rec.Identity.Same(saved) {
		return nil, &AdmissionRefusal{Reason: fmt.Sprintf("ownership record %s names no captured identity, or another one than the receipt (record %+v, receipt %s/%s)", r.path(), rec.Identity, saved.SessionID, saved.Incarnation)}
	}
	return &rec, nil
}

// LaunchAuthorization is the single-use permission to create ONE worker child
// for one attempt. The controller mints it under its lock and the journal
// flock; startJSONProcess consumes it before cmd.Start. It is an explicit
// parameter of every launch path, never a context value, so a launch site
// that lacks one does not compile. After the backend returns, the controller
// also checks that it was published exactly once: an extra assertion, not the
// guard (the design review: a post-return check is too late to be the guard,
// since a crash before it leaves an unrecorded worker).
type LaunchAuthorization struct {
	mu        sync.Mutex
	rec       ownershipRecorder
	template  OwnershipRecord
	consumed  bool
	published bool
	spawned   OwnershipRecord
}

// Spawn runs start exactly once, with a durable intent before it and a durable
// spawned record after it. start returns the leader's pid; end must end the
// group and reap the leader, and is called only when the child exists but its
// record could not be published (the leader is still unreaped, so its group id
// is still ours to signal). A refused second call creates no child.
func (a *LaunchAuthorization) Spawn(executable string, start func() (int, error), end func()) error {
	if a == nil {
		return errors.New("worker launch without a launch authorization is refused; nothing records a worker launched outside the controller")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.rec.receiptPath == "" || a.template.Attempt == "" || a.template.Schema != OwnershipSchema {
		// A zero-value authorization has no recorder: it would publish a
		// record at a relative `.proc` in the cwd and launch an unidentifiable
		// worker (found by a mutant that substituted one for nil). Only the
		// controller mints these; anything else is refused before a child.
		return errors.New("launch authorization was not minted by a controller; refusing to launch a worker nothing would record")
	}
	if a.consumed {
		return errors.New("launch authorization already consumed: a second worker launch for this attempt is refused before any child is created")
	}
	a.consumed = true
	intent := a.template
	intent.Phase = "intent"
	intent.WrittenAt = time.Now()
	if err := a.rec.publish(intent); err != nil {
		return fmt.Errorf("ownership intent could not be made durable; no worker was started: %w", err)
	}
	pid, err := start()
	if err != nil {
		return err
	}
	leader, err := observeProcess(pid)
	var exe ExecutableIdentity
	if err == nil {
		exe, err = observeExecutableIdentity(executable)
	}
	if err != nil {
		end()
		return fmt.Errorf("worker %d started but could not be pinned, so it was ended and its launch refused: %w", pid, err)
	}
	spawned := a.template
	spawned.Phase = "spawned"
	spawned.WrittenAt = time.Now()
	spawned.Leader = &leader
	spawned.Executable = &exe
	if err := a.rec.publish(spawned); err != nil {
		end()
		return fmt.Errorf("worker %d started but its ownership record could not be published, so it was ended and its launch refused: %w", pid, err)
	}
	a.published = true
	a.spawned = spawned
	return nil
}

// Published reports whether a worker was spawned and recorded through this
// authorization. The controller refuses a backend that returned a worker
// without it.
func (a *LaunchAuthorization) Published() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.published
}

// bind republishes the spawned record with the identity Start captured after
// the spawn, so admission can require identity == saved identity for every
// record, start-written ones included. The attempt stays the same.
func (a *LaunchAuthorization) bind(id Identity) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.published {
		return errors.New("no spawned record to bind")
	}
	rec := a.spawned
	rec.Identity = &id
	rec.WrittenAt = time.Now()
	if err := a.rec.publish(rec); err != nil {
		return err
	}
	a.spawned = rec
	return nil
}

// AdmissionRefusal is a resume refusal. Waitable ones are the alive verdicts
// (adapter, leader or group still there), which time may clear; everything
// else (no record, wrong binding, intent-only, an observation that could not
// be made) is refused at once, because waiting cannot change it.
type AdmissionRefusal struct {
	Reason   string
	Waitable bool
}

func (r *AdmissionRefusal) Error() string { return r.Reason }

// Process and group observations, as the classifier sees them.
type processState int

const (
	procAlive    processState = iota // pid exists with the recorded start time (a zombie counts: unreaped)
	procGone                         // no such pid
	procRecycled                     // pid exists with another start time: not ours, never signalled
	procError                        // could not be observed
)

type groupState int

const (
	groupEmpty   groupState = iota // kill(-pgid, 0) answered ESRCH
	groupMembers                   // kill(-pgid, 0) succeeded: a member (an in-group tool) still runs
	groupEPERM                     // members exist that we may not signal (measured: a group whose only member is a zombie)
	groupError                     // any other errno
)

type ownershipFacts struct {
	Adapter, Leader processState
	// AdapterIsSelf: the recorded adapter is THIS process (pid and start
	// time). It holds the flock and has closed the controller that wrote the
	// record, so it is not a prior adapter still controlling the worker; the
	// leader and group checks still decide. Any other live adapter refuses.
	AdapterIsSelf bool
	AdapterErr    error
	LeaderErr     error
	Group         groupState
	GroupErr      error
}

// classifyOwnership is the pure decision over fixed observations: one verdict
// after every check, so "leader gone" cannot admit before the group check has
// run. Observation errors refuse non-waitably and first: an admission that
// cannot say is not an admission.
func classifyOwnership(rec *OwnershipRecord, f ownershipFacts) *AdmissionRefusal {
	if refusal := recordShape(rec); refusal != nil {
		return refusal
	}
	if f.AdapterErr != nil {
		return &AdmissionRefusal{Reason: fmt.Sprintf("could not observe the recorded adapter pid %d: %v", rec.Adapter.PID, f.AdapterErr)}
	}
	if f.LeaderErr != nil {
		return &AdmissionRefusal{Reason: fmt.Sprintf("could not observe the recorded worker leader pid %d: %v", rec.Leader.PID, f.LeaderErr)}
	}
	if f.Group == groupError {
		return &AdmissionRefusal{Reason: fmt.Sprintf("could not probe the recorded worker group %d: %v", rec.Leader.PGID, f.GroupErr)}
	}
	var alive []string
	if f.Adapter == procAlive && !f.AdapterIsSelf {
		alive = append(alive, fmt.Sprintf("the recording adapter (pid %d, start %d) is still running", rec.Adapter.PID, rec.Adapter.Start))
	}
	if f.Leader == procAlive {
		alive = append(alive, fmt.Sprintf("the recorded worker leader is still running (pid %d, start %d, pgid %d, sid %d)", rec.Leader.PID, rec.Leader.Start, rec.Leader.PGID, rec.Leader.SID))
	}
	switch f.Group {
	case groupMembers:
		alive = append(alive, fmt.Sprintf("the recorded worker group %d still has members (an in-group tool of the prior worker may still run; a reused group id would make this a false refusal)", rec.Leader.PGID))
	case groupEPERM:
		alive = append(alive, fmt.Sprintf("the recorded worker group %d still exists but cannot be probed (EPERM: typically an unreaped zombie member)", rec.Leader.PGID))
	}
	if len(alive) > 0 {
		reason := "resume refused: "
		for i, a := range alive {
			if i > 0 {
				reason += "; "
			}
			reason += a
		}
		return &AdmissionRefusal{Reason: reason + "; nothing was signalled; descendants that left the group are not checked; retry when they are gone, or pass --wait-prior DURATION to wait for that passively", Waitable: true}
	}
	return nil
}

// recordShape is the structural check the classifier applies before any
// process fact matters: phase spawned, a leader, and a group that is the
// leader's own private one. It runs BEFORE observeOwnership probes anything,
// so a malformed record never names a group to kill(-pgid, 0), even with
// signal 0 (Codex code review, 2026-10-07: the probe used to precede it).
func recordShape(rec *OwnershipRecord) *AdmissionRefusal {
	if rec.Phase == "intent" {
		return &AdmissionRefusal{Reason: fmt.Sprintf("ownership record %s is intent-only: attempt %s, recorded by adapter pid %d (start %d), published its intent to spawn a worker and no spawned record followed, so a worker may have started before its pid was published, and nothing identifies it; the adapter has verified nothing about that worker; the operator's recourse, after their own check for a stray worker, is to abandon this receipt and start a fresh worker with a NEW receipt; never delete the record or resume this captured session", rec.Receipt+".proc", rec.Attempt, rec.Adapter.PID, rec.Adapter.Start)}
	}
	if rec.Phase != "spawned" || rec.Leader == nil {
		return &AdmissionRefusal{Reason: fmt.Sprintf("ownership record has phase %q and no usable leader; refusing", rec.Phase)}
	}
	if rec.Leader.PGID != rec.Leader.PID || rec.Leader.PGID <= 1 {
		return &AdmissionRefusal{Reason: fmt.Sprintf("ownership record's group %d is not the recorded leader %d's own group (or is not a private group); refusing rather than probe a group that may not be ours", rec.Leader.PGID, rec.Leader.PID)}
	}
	return nil
}

// observeOwnership gathers the facts for one well-shaped record. Every arm is
// passive; a record recordShape refuses is never probed.
func observeOwnership(rec *OwnershipRecord) ownershipFacts {
	var f ownershipFacts
	if recordShape(rec) != nil {
		return f
	}
	f.Adapter, f.AdapterErr = observeState(rec.Adapter)
	if f.Adapter == procAlive {
		if self, err := observeProcess(os.Getpid()); err == nil && self.PID == rec.Adapter.PID && self.Start == rec.Adapter.Start {
			f.AdapterIsSelf = true
		}
	}
	f.Leader, f.LeaderErr = observeState(*rec.Leader)
	f.Group, f.GroupErr = probeGroup(rec.Leader.PGID)
	return f
}

func observeState(want ProcessIdentity) (processState, error) {
	now, err := observeProcess(want.PID)
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

// probeGroup is kill(-pgid, 0): signal 0 delivers nothing. The caller has
// validated pgid as the recorded leader's own private group, above 1, so this
// never names a group the adapter did not create. ESRCH alone is absence.
func probeGroup(pgid int) (groupState, error) {
	if pgid <= 1 {
		return groupError, fmt.Errorf("group %d is not probeable", pgid)
	}
	err := syscall.Kill(-pgid, 0)
	switch {
	case err == nil:
		return groupMembers, nil
	case errors.Is(err, syscall.ESRCH):
		return groupEmpty, nil
	case errors.Is(err, syscall.EPERM):
		return groupEPERM, nil
	default:
		return groupError, err
	}
}

func observeExecutableIdentity(path string) (ExecutableIdentity, error) {
	info, err := os.Stat(path)
	if err != nil {
		return ExecutableIdentity{}, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ExecutableIdentity{}, errors.New("executable identity unavailable on this platform")
	}
	return ExecutableIdentity{Path: path, Dev: uint64(st.Dev), Ino: uint64(st.Ino), Size: info.Size(), ModTime: info.ModTime().UnixNano()}, nil
}

func fileIdentity(f *os.File) (JournalIdentity, error) {
	info, err := f.Stat()
	if err != nil {
		return JournalIdentity{}, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return JournalIdentity{}, errors.New("journal identity unavailable on this platform")
	}
	return JournalIdentity{Dev: uint64(st.Dev), Ino: uint64(st.Ino)}, nil
}

func newAttemptID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// AwaitPrior re-runs the whole passive resume admission every 250 ms until it
// admits, a non-waitable refusal appears, or ctx ends (the `--wait-prior`
// bound). It signals nothing and writes nothing. The eventual Resume runs the
// same admission again under the lock, so this is a wait, not an admission.
func (c *Controller) AwaitPrior(ctx context.Context) error {
	for {
		// The bound is checked before every attempt and again before success:
		// an admission observed after the bound is not the bound's answer
		// (Codex code review P2: with `--wait-prior=1ns` the context expired
		// during construction and a successful admission was returned anyway).
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("the --wait-prior bound ended before the prior worker was checked (%w)", err)
		}
		c.mu.Lock()
		_, err := c.resumeAdmission()
		c.mu.Unlock()
		if err == nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return fmt.Errorf("waited for the prior worker past the --wait-prior bound (%w); the admission observed after it is not this wait's answer", ctxErr)
			}
			return nil
		}
		var refusal *AdmissionRefusal
		if !errors.As(err, &refusal) || !refusal.Waitable {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waited for the prior worker until the --wait-prior bound and it did not clear (%v); last refusal: %w", ctx.Err(), err)
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// NewControllerAfterPrior is `--wait-prior`: it takes the journal (waiting
// through ErrJournalOwned only, which is a live prior adapter), then waits for
// the recorded prior worker through AwaitPrior, and returns a controller whose
// Resume is expected to admit. Any other refusal closes the controller and
// returns it; the caller's 30 s start context begins only after this returns.
func NewControllerAfterPrior(ctx context.Context, b Backend, l Ledger, receiptPath string) (*Controller, error) {
	var c *Controller
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("waited for the prior adapter until the --wait-prior bound (%w)", err)
		}
		var err error
		c, err = NewController(b, l, receiptPath)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrJournalOwned) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waited for the prior adapter until the --wait-prior bound (%v); last refusal: %w", ctx.Err(), err)
		case <-time.After(250 * time.Millisecond):
		}
	}
	if err := c.AwaitPrior(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}
