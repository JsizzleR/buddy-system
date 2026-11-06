package cli

// A job declared against a claim (D-131, issue #60): `buddy claim <run> --job
// <pid>`, read back on `buddy who <run>` as alive, GONE or CANNOT SAY.
//
// THE FAILURE. An integrator's hermetic tier was killed about 3h12m in. Its
// output stopped at `Terminated: 15` with no final status line, about three
// hours of tier were void, and nothing in buddy said so: JOBS (jobs.go) shows
// what is present when sampled, and the run's claim read as before with its
// riders still waiting. A later coordinator found it from the output file and
// the process table.
//
// AN OBSERVATION, NEVER A VERDICT. GONE says the declared process ended, and
// nothing about how. A tier that reached its GREEN summary and exited reads
// GONE, byte for byte what a killed one reads, so the line cannot call either a
// failure: the result is the tier's final status line, which buddy does not
// read, and it establishes success only for the invocation declared. Output
// growth was cut as evidence: it proves neither completion nor progress. No
// reading releases a claim or refuses a call (invariant 10's spirit), and a
// dead job reaches riders only through the coordinator's `release --outcome
// aborted` or a re-claim and a message (the skills).
//
// THE IDENTITY IS (pid, start time, boot), read here at declaration, because a
// pid is a recycled number. Start time alone would very nearly do; the boot id
// says "rebooted" where it can and leaves no same-microsecond case to argue
// about. The declarer never types a start time, so it cannot get one wrong.
//
// WHAT IT CANNOT CATCH. It reads the process named, so a declaration naming a
// wrapper that outlives its tier and keeps printing reads alive over a dead
// tier: the skill says to name the tier's own process. Cooperative, as the rest
// is (D-005).
//
// CANNOT SAY IS NOT A READING OF THE JOB. A platform with no identity to read
// (proc_other.go, Linux included) prints it for every declaration, never alive
// and never GONE, because either word would be a guess (D-016's "no row means
// unknown"). The same goes for a declaration recorded without an identity and
// for a kernel answer that is neither a process nor "no such process".

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

// procState is what the kernel says about a pid.
type procState int

const (
	procUnknown procState = iota // the platform or the kernel cannot say
	procAbsent                   // no such process
	procZombie                   // exited, and its parent has not reaped it
	procRunning
)

// jobProcess is one read of a pid: its state and, when there is a process, its
// start time in microseconds, compared for equality only.
type jobProcess struct {
	State procState
	Born  int64
}

func (e Env) jobProc(pid int) jobProcess {
	if e.JobProc != nil {
		return e.JobProc(pid)
	}
	return readJobProcess(pid)
}

func (e Env) bootID() (string, bool) {
	if e.BootID != nil {
		return e.BootID()
	}
	return readBootID()
}

// parseJobPID is the flag's own check. Pid 0 and 1 are never a job, and above
// MaxInt32 the kernel's pid_t would truncate it into some other process's pid.
func parseJobPID(v string) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil || n <= 1 || n > math.MaxInt32 {
		return 0, fmt.Errorf("want a process id above 1")
	}
	return n, nil
}

// notRunningAtDeclaration is the Born a declaration records for a pid the kernel
// already said was not running. There is no start time to bind to, and a later
// process under that pid must not read as the job, so it reads GONE for good.
const notRunningAtDeclaration = -1

// notRunningWhy is that reading's reason; jobReading words it apart.
const notRunningWhy = "it was not running when declared"

// declaredJob reads the identity a declaration records. A pid that is not
// running is GRANTED, and says it already reads GONE: a reading refuses nothing
// (the first shape refused the claim, and a tier that dies between its start and
// the declaration would then have blocked the coordinator's re-claim at the worst
// moment; the orchestrator's ruling). Only a malformed pid refuses (parseJobPID).
// Where the platform cannot say, the declaration stands with no identity and
// reads CANNOT SAY.
func declaredJob(env Env, pid int) *store.JobDecl {
	p := env.jobProc(pid)
	boot, ok := env.bootID()
	if !ok {
		boot = ""
	}
	switch p.State {
	case procAbsent, procZombie:
		return &store.JobDecl{PID: pid, Born: notRunningAtDeclaration, Boot: boot}
	case procRunning:
		if ok {
			return &store.JobDecl{PID: pid, Born: p.Born, Boot: boot}
		}
	}
	return &store.JobDecl{PID: pid}
}

// declaredLine is claim's confirmation of what it recorded.
func declaredLine(env Env, j store.JobDecl) string {
	if j.Born == notRunningAtDeclaration {
		return fmt.Sprintf("job pid %d declared, and it already reads GONE: no such running process", j.PID)
	}
	if j.Born == 0 {
		return fmt.Sprintf("job pid %d declared, with no process identity readable here: `buddy who` will say CANNOT SAY for it", j.PID)
	}
	return fmt.Sprintf("job pid %d declared, started %s ago: `buddy who` reads it", j.PID, age(nowOf(env), time.UnixMicro(j.Born)))
}

// judgeJob is the reading, over what the kernel said, so every arm is a test on
// any host. It returns the word and the reason beside it.
func judgeJob(j store.JobDecl, boot string, bootOK bool, p jobProcess) (word, why string) {
	switch {
	// First, because it needs nothing read now: the kernel already answered,
	// and a later boot-id failure must not turn GONE into CANNOT SAY (panel).
	case j.Born == notRunningAtDeclaration:
		return "GONE", notRunningWhy
	case !bootOK:
		return "CANNOT SAY", "this platform reads no process identity"
	case j.Born == 0 || j.Boot == "":
		return "CANNOT SAY", "no process identity was recorded when it was declared"
	case j.Boot != boot:
		return "GONE", "the machine has rebooted since"
	}
	switch p.State {
	case procAbsent:
		return "GONE", "no such process"
	case procZombie:
		return "GONE", "exited, not yet reaped"
	case procRunning:
		if p.Born != j.Born {
			return "GONE", "that pid is now another process"
		}
		return "alive", ""
	}
	return "CANNOT SAY", "the kernel gave no answer for that pid"
}

// jobReading is the text after the claim's name: what is printed, whole.
func jobReading(word, why string) string {
	switch word {
	case "alive":
		return "alive (the declared process; says nothing of its result or progress)"
	case "GONE":
		if why == notRunningWhy {
			// Not "it ended": buddy never saw this process, and a mistyped pid
			// reads this too. Taken for a dead tier, it would abort a live one.
			return "GONE (" + why + "): buddy never saw it run, so check the pid you declared"
		}
		return "GONE (" + why + "): it ended, result unknown. A finished job reads this too: its final status line says which"
	}
	return "CANNOT SAY (" + why + "): neither alive nor GONE, so check it by hand"
}

// declaredJobLines renders one JOB line per claim that declares a job, capped
// like every list on the report. Every value but the slug is a number or fixed
// text; the slug is peer text and fenced.
func declaredJobLines(env Env, claims []store.ClaimInfo, maxRows int) []string {
	var declared []store.ClaimInfo
	for _, c := range claims {
		if c.Job.PID != 0 {
			declared = append(declared, c)
		}
	}
	if len(declared) == 0 {
		return nil
	}
	now := nowOf(env)
	boot, bootOK := env.bootID()
	var out []string
	for i, c := range declared {
		if i == maxRows {
			out = append(out, fmt.Sprintf("JOB          ...and %d more not shown", len(declared)-maxRows))
			break
		}
		word, why := judgeJob(c.Job, boot, bootOK, env.jobProc(c.Job.PID))
		var head strings.Builder
		fmt.Fprintf(&head, "JOB          pid %d on claim %s", c.Job.PID, strconv.Quote(fence.Line(c.Slug, 128)))
		if c.Job.Born > 0 {
			fmt.Fprintf(&head, ", started %s ago", age(now, time.UnixMicro(c.Job.Born)))
		}
		out = append(out, head.String()+": "+jobReading(word, why))
	}
	return out
}
