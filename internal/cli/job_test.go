package cli

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// D-131 (issue #60): a job declared against a claim reads alive, GONE or CANNOT
// SAY on who. Each real-process test states both platforms' answer, because CI's
// Ubuntu run goes through proc_other.go, which reads no identity at all.

// startJob starts a process that lives until the test ends it.
func startJob(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "600")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	return cmd
}

// reads picks the word this host's kernel can give: everywhere but Darwin a
// declaration can only read CANNOT SAY.
func reads(darwin string) string {
	if runtime.GOOS == "darwin" {
		return darwin
	}
	return ": CANNOT SAY (this platform reads no process identity)"
}

func jobLine(t *testing.T, report string) string {
	t.Helper()
	var found []string
	for _, ln := range lines(report) {
		if strings.HasPrefix(ln, "JOB ") {
			found = append(found, ln)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want one JOB line, got %d:\n%s", len(found), report)
	}
	return found[0]
}

// A declared live process reads alive, killed it reads GONE, and neither reading
// moves anything: who exits 0, the claim stays open and still refuses a peer,
// the gate rules as it did, the rider keeps waiting.
func TestADeclaredJobReadsAliveThenGoneAndRefusesNothing(t *testing.T) {
	boundedParallel(t)
	b := newBatchFx(t)
	job := startJob(t)
	pid := fmt.Sprint(job.Process.Pid)

	// Control: no declaration, no line, so what follows is the flag's doing.
	b.form(t)
	if w := b.ok(t, "sess-a", "who", "herm"); strings.Contains(w, "\nJOB ") {
		t.Fatalf("a claim with no declaration prints no JOB line:\n%s", w)
	}

	out := b.ok(t, "sess-a", "claim", "herm", "--desc", "RUNNING", "--scope", ".buddy/slot/herm", "--scope", "src", "--job", pid)
	if !strings.Contains(out, "job pid "+pid+" declared") {
		t.Fatalf("the claim says what it recorded:\n%s", out)
	}
	b.ok(t, "sess-c", "wait", "--on", "herm")

	holderEdit := hookJSON("sess-a", b.repo, "Edit", filepath.Join(b.repo, "src/x.go"))
	peerEdit := hookJSON("sess-b", b.wtB, "Edit", filepath.Join(b.wtB, "src/x.go"))
	check := func(when string) {
		t.Helper()
		if gate, _, code := b.run(t, b.wtB, peerEdit, "gate"); code != 0 {
			t.Fatalf("%s: the gate's exit moved: %d", when, code)
		} else if _, denied := decodeDeny(t, gate); !denied {
			t.Fatalf("%s: a peer's edit inside the claim is still denied:\n%s", when, gate)
		}
		if gate, _, _ := b.run(t, b.repo, holderEdit, "gate"); strings.Contains(gate, "permissionDecision") {
			t.Fatalf("%s: the holder's own edit is still allowed:\n%s", when, gate)
		}
		if _, _, code := b.as(t, "sess-b", "claim", "other", "--desc", "x", "--scope", "src/y"); code != ExitRefused {
			t.Fatalf("%s: a peer's claim over it still exits %d, got %d", when, ExitRefused, code)
		}
		if c := b.ok(t, "sess-c", "wait", "check"); !strings.HasPrefix(c, "STILL WAITING") {
			t.Fatalf("%s: the rider is still waiting; only the holder's outcome tells it:\n%s", when, c)
		}
	}

	if ln := jobLine(t, b.ok(t, "sess-a", "who", "herm")); !strings.Contains(ln, "pid "+pid+` on claim "herm"`) ||
		!strings.Contains(ln, reads(": alive (the declared process; says nothing of its result or progress)")) {
		t.Fatalf("a live declared process:\n%s", ln)
	}
	check("alive")

	job.Process.Kill()
	job.Wait()

	if ln := jobLine(t, b.ok(t, "sess-a", "who", "herm")); !strings.Contains(ln, reads(": GONE (no such process): it ended, result unknown.")) {
		t.Fatalf("a killed declared process:\n%s", ln)
	}
	check("gone")

	// The holder's own report reads it too, named any way at all.
	if ln := jobLine(t, b.ok(t, "sess-a", "status")); !strings.Contains(ln, reads(": GONE")) {
		t.Fatalf("status:\n%s", ln)
	}
	// A release is still the holder's to make, and the rider hears that.
	b.ok(t, "sess-a", "release", "herm", "--outcome", "aborted", "--note", "killed")
	if c := b.ok(t, "sess-c", "wait", "check"); !strings.HasPrefix(c, `LANDED: claim "herm" released 0s ago, outcome ABORTED "killed"`) {
		t.Fatalf("the holder's outcome reaches the rider:\n%s", c)
	}
}

// A zombie still answers the kernel with its start time, and procAlive calls it
// alive. A job killed and not yet reaped must not read alive.
func TestADeclaredJobKilledAndNotReapedReadsGone(t *testing.T) {
	boundedParallel(t)
	b := newBatchFx(t)
	job := startJob(t)
	b.ok(t, "sess-a", "claim", "herm", "--desc", "RUNNING", "--scope", ".buddy/slot/herm", "--job", fmt.Sprint(job.Process.Pid))

	job.Process.Kill() // and no Wait: this test's process is the parent that has not reaped it
	want := reads(": GONE (exited, not yet reaped)")
	var ln string
	for i := 0; i < 200; i++ {
		ln = jobLine(t, b.ok(t, "sess-a", "who", "herm"))
		if strings.Contains(ln, want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("killed and unreaped, want %q:\n%s", want, ln)
}

// Pid reuse and another boot, against the real kernel: the live process really
// is under that pid, only the recorded identity differs. The control is the same
// process reading alive first.
func TestADeclaredJobIsItsStartTimeAndItsBootNotItsPid(t *testing.T) {
	for _, c := range []struct {
		name, update, want string
	}{
		{"another process under the pid", `UPDATE claims SET job_born=job_born+1 WHERE slug='herm'`, ": GONE (that pid is now another process)"},
		{"a declaration from another boot", `UPDATE claims SET job_boot='another-boot' WHERE slug='herm'`, ": GONE (the machine has rebooted since)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			boundedParallel(t)
			b := newBatchFx(t)
			job := startJob(t)
			b.ok(t, "sess-a", "claim", "herm", "--desc", "RUNNING", "--scope", ".buddy/slot/herm", "--job", fmt.Sprint(job.Process.Pid))
			if ln := jobLine(t, b.ok(t, "sess-a", "who", "herm")); !strings.Contains(ln, reads(": alive")) {
				t.Fatalf("control: the process itself:\n%s", ln)
			}
			db, err := sql.Open("sqlite", "file:"+filepath.Join(b.repo, ".git", "buddy.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(c.update); err != nil {
				t.Fatal(err)
			}
			if ln := jobLine(t, b.ok(t, "sess-a", "who", "herm")); !strings.Contains(ln, reads(c.want)) {
				t.Fatalf("want %q:\n%s", reads(c.want), ln)
			}
		})
	}
}

// runSeamed is fixture.run with the job seams set, for the arms a host's kernel
// will not produce on demand.
func runSeamed(t *testing.T, f *fixture, cwd string, proc func(int) jobProcess, boot func() (string, bool), args ...string) (string, string, int) {
	t.Helper()
	var out, errw bytes.Buffer
	code := Run(args, Env{
		Stdin:     strings.NewReader(""),
		Stdout:    &out,
		Stderr:    &errw,
		Cwd:       cwd,
		Now:       func() time.Time { return f.clock },
		Getenv:    func(k string) string { return f.env[k] },
		Anchor:    func() (harnessProc, bool) { return harnessProc{}, false },
		ProcAlive: func(store.ProcRef) bool { return false },
		ProcTable: func() ([]procEntry, int, bool) { return nil, 0, false },
		SockDir:   f.sockDir,
		ChatStamp: func() string { return f.chatStamp },
		JobProc:   proc,
		BootID:    boot,
	})
	return out.String(), errw.String(), code
}

// Exactly what is printed, on any host. The GONE arm is the brief's question: a
// job that finished normally reads this too, so the line may not call it a
// failure, and its words have to send the reader to the final status line.
func TestAJobLineIsExactlyThis(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	born := f.clock.Add(-5 * time.Minute).UnixMicro()
	yes := func() (string, bool) { return "boot-A", true }
	f.asSession("sess-a", func() {
		out, errw, code := runSeamed(t, f, f.repo, func(int) jobProcess { return jobProcess{State: procRunning, Born: born} }, yes,
			"claim", "herm", "--desc", "RUNNING", "--scope", ".buddy/slot/herm", "--job", "4242")
		if code != 0 || !strings.Contains(out, "job pid 4242 declared, started 5m ago: `buddy who` reads it\n") {
			t.Fatalf("claim: %d %s %s", code, out, errw)
		}
	})
	head := `JOB          pid 4242 on claim "herm", started 5m ago: `
	for _, c := range []struct {
		name string
		boot func() (string, bool)
		proc jobProcess
		want string
	}{
		{"the declared process", yes, jobProcess{State: procRunning, Born: born},
			"alive (the declared process; says nothing of its result or progress)"},
		{"no such process", yes, jobProcess{State: procAbsent},
			"GONE (no such process): it ended, result unknown. A finished job reads this too: its final status line says which"},
		{"a zombie", yes, jobProcess{State: procZombie, Born: born},
			"GONE (exited, not yet reaped): it ended, result unknown. A finished job reads this too: its final status line says which"},
		{"another process", yes, jobProcess{State: procRunning, Born: born + 1},
			"GONE (that pid is now another process): it ended, result unknown. A finished job reads this too: its final status line says which"},
		{"another boot, the same start time", func() (string, bool) { return "boot-B", true }, jobProcess{State: procRunning, Born: born},
			"GONE (the machine has rebooted since): it ended, result unknown. A finished job reads this too: its final status line says which"},
		{"the kernel gave no answer", yes, jobProcess{State: procUnknown},
			"CANNOT SAY (the kernel gave no answer for that pid): neither alive nor GONE, so check it by hand"},
		// What proc_other.go answers, so this arm runs on Darwin too.
		{"a platform that reads nothing", func() (string, bool) { return "", false }, jobProcess{State: procUnknown},
			"CANNOT SAY (this platform reads no process identity): neither alive nor GONE, so check it by hand"},
	} {
		out, errw, code := "", "", 0
		f.asSession("sess-a", func() {
			out, errw, code = runSeamed(t, f, f.repo, func(int) jobProcess { return c.proc }, c.boot, "who", "herm")
		})
		if code != 0 {
			t.Fatalf("%s: who must exit 0 whatever it reads, got %d: %s", c.name, code, errw)
		}
		if got := jobLine(t, out); got != head+c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, head+c.want)
		}
		if strings.Contains(strings.ToLower(c.want), "fail") {
			t.Errorf("%s: a reading is no verdict, and a finished job reads this too:\n%s", c.name, c.want)
		}
	}
}

// A finished job and a killed job read the same, byte for byte: buddy sees only
// that the process ended, and calling either a failure would be a guess.
func TestAJobThatFinishedReadsWhatAKilledOneReads(t *testing.T) {
	boundedParallel(t)
	b := newBatchFx(t)
	// `cat` runs until its stdin closes, then exits 0: alive to declare, then a
	// normal end.
	finished := exec.Command("sh", "-c", `cat >/dev/null; echo 'check.sh: HERMETIC GREEN'`)
	finished.Stdout = io.Discard
	stdin, err := finished.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := finished.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { finished.Process.Kill(); finished.Wait() })
	killed := startJob(t)

	b.ok(t, "sess-a", "claim", "green", "--desc", "x", "--scope", ".buddy/slot/green", "--job", fmt.Sprint(finished.Process.Pid))
	b.ok(t, "sess-a", "claim", "red", "--desc", "x", "--scope", ".buddy/slot/red", "--job", fmt.Sprint(killed.Process.Pid))

	stdin.Close()
	if err := finished.Wait(); err != nil {
		t.Fatalf("the control must exit 0, or this is two kills: %v", err)
	}
	killed.Process.Kill()
	killed.Wait()

	normal := func(ln, slug string, pid int) string {
		ln = strings.Replace(ln, fmt.Sprintf(`pid %d on claim "%s"`, pid, slug), "<job>", 1)
		return regexp.MustCompile(`started \S+ ago`).ReplaceAllString(ln, "started <age> ago")
	}
	green := normal(jobLine(t, b.ok(t, "sess-a", "who", "green")), "green", finished.Process.Pid)
	red := normal(jobLine(t, b.ok(t, "sess-a", "who", "red")), "red", killed.Process.Pid)
	if green != red {
		t.Fatalf("a finished job and a killed one must read the same:\n%s\n%s", green, red)
	}
	if !strings.Contains(green, reads(": GONE")) {
		t.Fatalf("control: the reading is really the GONE arm:\n%s", green)
	}
	// Named by its holder, each claim gets its own line, and neither names the
	// other when named by slug.
	if w := b.ok(t, "sess-a", "who", "alpha"); strings.Count(w, "\nJOB ") != 2 {
		t.Fatalf("every claim the session holds:\n%s", w)
	}
}

// release --to moves the row, so the declaration goes with it, and the new holder
// reads it. A re-claim that names no job keeps it, one that names a job replaces
// it, and a slug released and claimed anew starts with none.
func TestADeclaredJobGoesWithTheClaim(t *testing.T) {
	boundedParallel(t)
	b := newBatchFx(t)
	job := startJob(t)
	pid := fmt.Sprint(job.Process.Pid)
	b.ok(t, "sess-a", "claim", "herm", "--desc", "RUNNING", "--scope", ".buddy/slot/herm", "--job", pid)

	b.ok(t, "sess-a", "release", "herm", "--to", "bravo")
	if w := b.ok(t, "sess-b", "who", "herm"); !strings.Contains(w, "CLAIMS HELD  1") {
		t.Fatalf("control: bravo really holds it now:\n%s", w)
	}
	if ln := jobLine(t, b.ok(t, "sess-b", "status")); !strings.Contains(ln, "pid "+pid) || !strings.Contains(ln, reads(": alive")) {
		t.Fatalf("the new holder reads the declaration:\n%s", ln)
	}
	if w := b.ok(t, "sess-a", "status"); strings.Contains(w, "\nJOB ") {
		t.Fatalf("the old holder no longer carries it:\n%s", w)
	}

	b.ok(t, "sess-b", "claim", "herm", "--desc", "reworded", "--scope", ".buddy/slot/herm")
	if ln := jobLine(t, b.ok(t, "sess-b", "who", "herm")); !strings.Contains(ln, "pid "+pid) {
		t.Fatalf("a re-claim naming no job keeps it:\n%s", ln)
	}

	rerun := startJob(t)
	b.ok(t, "sess-b", "claim", "herm", "--desc", "RED, job killed, one more tier", "--scope", ".buddy/slot/herm", "--job", fmt.Sprint(rerun.Process.Pid))
	if ln := jobLine(t, b.ok(t, "sess-b", "who", "herm")); !strings.Contains(ln, fmt.Sprintf("pid %d ", rerun.Process.Pid)) {
		t.Fatalf("a re-claim naming a job replaces it:\n%s", ln)
	}

	b.ok(t, "sess-b", "release", "herm")
	b.ok(t, "sess-b", "claim", "herm", "--desc", "new", "--scope", ".buddy/slot/herm")
	if w := b.ok(t, "sess-b", "who", "herm"); strings.Contains(w, "\nJOB ") {
		t.Fatalf("a new claim under an old slug declares nothing:\n%s", w)
	}
}

// A job that is already dead at declaration refuses nothing (a reading refuses
// nothing, and a tier that died between its start and the declaration must not
// block the re-claim). The claim is granted, says so in its result, reads GONE,
// and goes on reading GONE when a later process takes that pid.
func TestAJobNotRunningAtDeclarationIsGrantedAndReadsGone(t *testing.T) {
	boot := func() (string, bool) { return "boot-A", true }
	later := func(int) jobProcess { return jobProcess{State: procRunning, Born: 77} }
	const said = "job pid 4242 declared, and it already reads GONE: no such running process"
	const line = `JOB          pid 4242 on claim "c": GONE (it was not running when declared): buddy never saw it run, so check the pid you declared`
	for name, p := range map[string]jobProcess{"absent": {State: procAbsent}, "zombie": {State: procZombie, Born: 77}} {
		t.Run(name, func(t *testing.T) {
			boundedParallel(t)
			f := newFixture(t)
			f.initAndHello(t)
			proc := func(int) jobProcess { return p }
			f.asSession("sess-a", func() {
				out, errw, code := runSeamed(t, f, f.repo, proc, boot, "claim", "c", "--desc", "x", "--scope", "src", "--job", "4242", "--dry-run")
				if code != 0 || !strings.Contains(out, "would declare: "+said+"\n") {
					t.Fatalf("the forecast says what the claim would: %d %s %s", code, out, errw)
				}
				if l, _, code := runSeamed(t, f, f.repo, proc, boot, "ls"); code != 0 || strings.Contains(l, "src") {
					t.Fatalf("a dry run took nothing: %d\n%s", code, l)
				}
				out, errw, code = runSeamed(t, f, f.repo, proc, boot, "claim", "c", "--desc", "x", "--scope", "src", "--job", "4242")
				if code != 0 || !strings.Contains(out, said+"\n") {
					t.Fatalf("granted, and it says so: %d %s %s", code, out, errw)
				}
				out, _, _ = runSeamed(t, f, f.repo, proc, boot, "who", "c")
				if got := jobLine(t, out); got != line {
					t.Fatalf("\n got %s\nwant %s", got, line)
				}
				out, _, _ = runSeamed(t, f, f.repo, later, boot, "who", "c")
				if got := jobLine(t, out); got != line {
					t.Fatalf("a later process under that pid is not the job:\n got %s\nwant %s", got, line)
				}
				out, _, _ = runSeamed(t, f, f.repo, later, func() (string, bool) { return "", false }, "who", "c")
				if got := jobLine(t, out); got != line {
					t.Fatalf("a later boot-id failure does not unsay it:\n got %s\nwant %s", got, line)
				}
			})
			// The claim it granted reserves like any other.
			if _, _, code := f.run(t, f.wtB, "", "claim", "x", "--desc", "x", "--scope", "src/y", "--session", "sess-b"); code != ExitRefused {
				t.Fatalf("control: the granted claim holds its scope, got exit %d", code)
			}
		})
	}
}

// The same against the real kernel, where it answers at all.
func TestARealDeadPidIsGrantedAtDeclaration(t *testing.T) {
	boundedParallel(t)
	b := newBatchFx(t)
	job := startJob(t)
	job.Process.Kill()
	job.Wait()
	out := b.ok(t, "sess-a", "claim", "herm", "--desc", "RED", "--scope", ".buddy/slot/herm", "--job", fmt.Sprint(job.Process.Pid))
	if runtime.GOOS == "darwin" && !strings.Contains(out, "already reads GONE") {
		t.Fatalf("the result says so:\n%s", out)
	}
	if ln := jobLine(t, b.ok(t, "sess-a", "who", "herm")); !strings.Contains(ln, reads(": GONE (it was not running when declared)")) {
		t.Fatalf("who reads it:\n%s", ln)
	}
}

// What `--job` refuses, and that a refused claim declares nothing.
func TestClaimJobFlagRefusals(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	running := func(int) jobProcess { return jobProcess{State: procRunning, Born: 77} }
	boot := func() (string, bool) { return "boot-A", true }
	claim := func(proc func(int) jobProcess, extra ...string) (string, string, int) {
		t.Helper()
		var out, errw string
		var code int
		f.asSession("sess-a", func() {
			out, errw, code = runSeamed(t, f, f.repo, proc, boot, append([]string{"claim", "c", "--desc", "x", "--scope", "src"}, extra...)...)
		})
		return out, errw, code
	}
	for _, v := range []string{"", "abc", "0", "1", "-5", "99999999999", "12x"} {
		_, errw, code := claim(running, "--job", v)
		if code != 1 || !strings.Contains(errw, "refused:") || !strings.Contains(errw, "want a process id above 1") {
			t.Errorf("--job %q: exit %d: %s", v, code, errw)
		}
	}
	if l, errw, code := f.run(t, f.repo, "", "ls"); code != 0 || strings.Contains(l, "src") {
		t.Fatalf("a refused claim took nothing: %d %s\n%s", code, errw, l)
	}

	// A dry run forecasts and writes nothing, the real claim then declares.
	if out, errw, code := claim(running, "--job", "4242", "--dry-run"); code != 0 || !strings.Contains(out, "would declare: job pid 4242 declared") {
		t.Fatalf("dry run: %d %s %s", code, out, errw)
	}
	if l, errw, code := f.run(t, f.repo, "", "ls"); code != 0 || strings.Contains(l, "src") {
		t.Fatalf("a dry run took nothing: %d %s\n%s", code, errw, l)
	}
	if out, errw, code := claim(running, "--job", "4242"); code != 0 || !strings.Contains(out, "job pid 4242 declared") {
		t.Fatalf("positive control, the same flag on a running process: %d %s %s", code, out, errw)
	}
	// And ls is what can see it, so the two checks above could have failed.
	if l, errw, code := f.run(t, f.repo, "", "ls"); code != 0 || !strings.Contains(l, "src") {
		t.Fatalf("control: ls shows a granted claim: %d %s\n%s", code, errw, l)
	}

	// A kernel that cannot say grants the claim: no identity, and it says so.
	f.asSession("sess-b", func() {
		out, errw, code := runSeamed(t, f, f.wtB, func(int) jobProcess { return jobProcess{State: procUnknown} }, boot,
			"claim", "d", "--desc", "x", "--scope", "other", "--job", "4243")
		if code != 0 || !strings.Contains(out, "job pid 4243 declared, with no process identity readable here") {
			t.Fatalf("cannot say at declaration: %d %s %s", code, out, errw)
		}
		out, _, _ = runSeamed(t, f, f.wtB, running, boot, "who", "d")
		if ln := jobLine(t, out); !strings.HasSuffix(ln, "CANNOT SAY (no process identity was recorded when it was declared): neither alive nor GONE, so check it by hand") {
			t.Fatalf("recorded with no identity, it never reads alive, even with a process there:\n%s", ln)
		}
	})

	// A refused claim declares nothing: not a new one, and not over the holder's
	// own job when the refused call is a re-claim. Each report's own control is
	// the job that must still be on it.
	f.asSession("sess-b", func() {
		_, _, code := runSeamed(t, f, f.wtB, running, boot, "claim", "e", "--desc", "x", "--scope", "src/y", "--job", "4244")
		if code != ExitRefused {
			t.Fatalf("a held scope still refuses: %d", code)
		}
		out, errw, code := runSeamed(t, f, f.wtB, running, boot, "who", "bravo")
		if code != 0 || !strings.Contains(out, "pid 4243 ") || strings.Contains(out, "4244") {
			t.Fatalf("a refused new claim declared a job: %d %s\n%s", code, errw, out)
		}
		_, _, code = runSeamed(t, f, f.wtB, running, boot, "claim", "d", "--desc", "x", "--scope", "src/y", "--job", "4245")
		if code != ExitRefused {
			t.Fatalf("a re-claim over a held scope still refuses: %d", code)
		}
		out, errw, code = runSeamed(t, f, f.wtB, running, boot, "who", "d")
		if code != 0 || !strings.Contains(out, "pid 4243 ") || strings.Contains(out, "4245") {
			t.Fatalf("a refused re-claim overwrote the job: %d %s\n%s", code, errw, out)
		}
	})
}

// The platform seam itself, on the host it runs on.
func TestTheProcessIdentityReads(t *testing.T) {
	self := readJobProcess(os.Getpid())
	boot, ok := readBootID()
	if runtime.GOOS != "darwin" {
		if self.State != procUnknown || ok || boot != "" {
			t.Fatalf("this platform reads nothing, so it must say so: %+v %q %v", self, boot, ok)
		}
		return
	}
	if self.State != procRunning || self.Born == 0 {
		t.Fatalf("this process: %+v", self)
	}
	if again := readJobProcess(os.Getpid()); again.Born != self.Born {
		t.Fatalf("a start time is stable: %d, %d", self.Born, again.Born)
	}
	if !ok || boot == "" {
		t.Fatalf("the boot id: %q %v", boot, ok)
	}
	if again, _ := readBootID(); again != boot {
		t.Fatalf("a boot id is stable: %q, %q", boot, again)
	}
	job := startJob(t)
	if p := readJobProcess(job.Process.Pid); p.State != procRunning || p.Born == 0 {
		t.Fatalf("control, the child while it runs: %+v", p)
	}
	job.Process.Kill()
	job.Wait()
	if p := readJobProcess(job.Process.Pid); p.State != procAbsent {
		t.Fatalf("a reaped child's pid: %+v", p)
	}
}
