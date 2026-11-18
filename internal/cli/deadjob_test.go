package cli

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// D-131 (issue #60): a killed job's news reaches the riders only through the
// coordinator, so the skills teach its check and the two ways to say so. These
// tests hold what that teaching rests on; job_test.go holds the JOB line itself. No test covered
// `--outcome aborted` through the CLI before this: internal/store's batch_test
// writes the word straight into the row.

// The first way: nobody is rerunning now, so the run closes with `aborted`.
// The rider's LANDED must carry the word and the note, and must NOT read as
// the outcome-less release (TestARiderIsToldWhenAReleaseReportedNoOutcome is
// the control that the caveat arm is armed).
func TestAKilledRunReachesItsRidersAsAborted(t *testing.T) {
	boundedParallel(t)
	b := newBatchFx(t)
	b.form(t)
	b.ok(t, "sess-c", "wait", "--on", "herm", "--ready", "HEAD")

	note := "tier killed 3h in, no final line; alpha reruns as herm-2"
	rel := b.ok(t, "sess-a", "release", "herm", "--outcome", "aborted", "--note", note)
	if !strings.Contains(rel, `outcome ABORTED "`+note+`" recorded for its waiters`) {
		t.Fatalf("the release says what it recorded:\n%s", rel)
	}
	out := b.ok(t, "sess-c", "wait", "check")
	if !strings.HasPrefix(out, `LANDED: claim "herm" released 0s ago, outcome ABORTED "`+note+`"`) || strings.Contains(out, "No outcome") {
		t.Fatalf("the rider's LANDED carries the outcome and the note, with no no-outcome caveat:\n%s", out)
	}
}

// The second way: the coordinator reruns at once, so it re-claims. The rider's
// wait and READY must stand, the rider must not read LANDED, and the later
// outcome must still reach it: release-then-claim would mint a new claim id and
// lose all three (D-063).
func TestAKilledRunReclaimedForARerunKeepsItsRiders(t *testing.T) {
	boundedParallel(t)
	b := newBatchFx(t)
	b.form(t)
	commitN(t, b.wtC, "cwork", 1)
	c := gitIn(t, b.wtC, "rev-parse", "HEAD")
	b.ok(t, "sess-c", "wait", "--on", "herm", "--ready", "HEAD")

	desc := "RED, job killed, one more tier"
	b.ok(t, "sess-a", "claim", "herm", "--desc", desc, "--scope", ".buddy/slot/herm", "--scope", ".buddy/slot/main")

	if out := b.ok(t, "sess-c", "wait", "check"); !strings.HasPrefix(out, "STILL WAITING") {
		t.Fatalf("a re-claimed run is still open to its rider:\n%s", out)
	}
	if w := b.ok(t, "sess-a", "who", "herm"); !strings.Contains(w, "READY at "+c[:8]) {
		t.Fatalf("who still lists the rider's READY:\n%s", w)
	}
	// `ls` is where a description reads: who prints none.
	if l := b.ok(t, "sess-c", "ls"); !strings.Contains(l, desc) {
		t.Fatalf("ls shows the new description:\n%s", l)
	}
	b.ok(t, "sess-a", "release", "herm", "--outcome", "pass", "--note", "landed on the rerun")
	if out := b.ok(t, "sess-c", "wait", "check"); !strings.HasPrefix(out, `LANDED: claim "herm" released 0s ago, outcome PASS "landed on the rerun"`) {
		t.Fatalf("the rerun's outcome reaches the same wait:\n%s", out)
	}
}

// The teaching itself. The skills say a tier prints a final status line only on
// reaching its end, so this repo's tier must really do that: `set -eu`, no trap
// that could print on a kill, and the summary as the script's last statement.
// Each clause fails if the check, its outcome or the JOBS limit is dropped.
func TestSkillsTeachTheDeadJobCheck(t *testing.T) {
	bodies := readSkills(t)
	fleet := fleetSkill(bodies) // its reference files too: handoff.md holds the handoff's job lines (D-171)
	read := func(path string) string {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	// Prose wraps, so compare with every run of whitespace folded to a space.
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }

	// The one rule the design struck: nothing may teach growth as a healthy sign.
	for where, text := range map[string]string{"fleet skill": fleet, "core skill": bodies[0], "USAGE": read("../../docs/USAGE.md")} {
		for _, gone := range []string{"output is still growing", "output growing;", "must still be growing", "is growing"} {
			if strings.Contains(flat(text), gone) {
				t.Errorf("%s still teaches growth as evidence: %q", where, gone)
			}
		}
	}

	for _, c := range []struct{ where, text, want string }{
		{"fleet skill", fleet, "Without it: a FAILED run, not a slow one"},
		{"fleet skill", fleet, `--outcome aborted --note "<what happened, who reruns>"`},
		{"fleet skill", fleet, "since a re-claim tells nobody"},
		{"fleet skill", fleet, "shows what is present when sampled, never what died"},
		{"fleet skill", fleet, "check that its pid is still that job and that you can read its output"},
		{"fleet skill", fleet, "So check on a schedule, whatever your cache tier"},
		{"fleet skill", fleet, "`ps -p <pid> -o lstart= -o command=`"},
		{"fleet skill", fleet, "its command, pid and start time"},
		{"fleet skill", fleet, "`buddy claim <run> --desc <its note> --scope <every scope it holds> --job <pid>`"},
		{"fleet skill", fleet, "Name the tier's own process and never a wrapper's"},
		{"fleet skill", fleet, "Only two things tell you anything"},
		{"fleet skill", fleet, "which says only that it ended, so look for that line"},
		{"fleet skill", fleet, "A pid not running when declared is granted and reads GONE"},
		{"fleet skill", fleet, "a finished job reads this too"},
		{"fleet skill", fleet, "Output growing or flat says neither"},
		{"core skill", bodies[0], "Whatever the tier, schedule a check"},
		{"core skill", bodies[0], "Gone with no final status line is a failed run, not a slow one; output growing or flat says neither"},
		{"core skill", bodies[0], "ABORTED: that run landed nothing"},
		{"USAGE", read("../../docs/USAGE.md"), "a job that died leaves nothing here"},
		{"USAGE", read("../../docs/USAGE.md"), "Output growth proves neither completion nor progress"},
	} {
		if !strings.Contains(flat(c.text), c.want) {
			t.Errorf("%s no longer says %q", c.where, c.want)
		}
	}

	script := read("../../scripts/check.sh")
	if !strings.Contains(script, "\nset -eu\n") {
		t.Errorf("check.sh no longer runs under set -eu: a failed leg could reach the summary")
	}
	for _, f := range deadJobTrapFaults(script, allowedTrap) {
		t.Error(f)
	}
	const tail = "case \"$TIER\" in\n  hermetic) echo \"check.sh: HERMETIC GREEN"
	i := strings.LastIndex(script, tail)
	if i < 0 {
		t.Fatalf("check.sh's final summary is gone or respelled: the skills' final status line rests on it")
	}
	if j := strings.LastIndex(script, "go test -race ./... -count=1"); j < 0 || j > i {
		t.Errorf("the hermetic race leg must run before the summary (leg at %d, summary at %d)", j, i)
	}
	// One esac after the summary, and it ends the file: a later case statement
	// that fails would otherwise pass with GREEN already printed (Codex code pass).
	if rest := script[i:]; strings.Count(rest, "esac") != 1 || !strings.HasSuffix(strings.TrimRight(rest, "\n"), "\nesac") {
		t.Errorf("check.sh's summary is no longer its last statement")
	}
	// And each arm is one echo and nothing else: `echo ...; false ;;` prints
	// GREEN and exits 1, which the esac check above cannot see (Codex code pass).
	lines := strings.Split(strings.TrimRight(script[i:], "\n"), "\n")
	for _, arm := range lines[1 : len(lines)-1] {
		if !summaryArm.MatchString(arm) {
			t.Errorf("check.sh's summary arm does more than print its line: %q", arm)
		}
	}
}

// One tier's word, one double-quoted echo, the arm's end.
var summaryArm = regexp.MustCompile(`^  (hermetic|live|all)\) +echo "[^"]*" ;;$`)

// THE ONE TRAP check.sh MAY CARRY. This gate refused every top-level trap,
// asking whoever added one to re-read whether a killed tier could still print
// its summary; #118's EXIT trap (5a00e30) turned it red. Re-read, and measured
// (a stand-in tier under sh, dash, bash and zsh, the shell or its child killed
// by TERM, INT, HUP and KILL, with and without the trap): the trap acts only
// on a status of exactly 2, prints a FAIL line to stderr and exits 1. A kill
// leaves 128+signal, which it passes through untouched, and no arm of it can
// print GREEN or exit 0, so every kill ended the same with it as without it.
// (bash, /bin/sh here, sent INT alone while its child runs on, lets the child
// finish and goes on to GREEN at 0, trap or none: a tier that ran to its end.
// A terminal's ^C reaches the child too, and that ends 130.) So the gate
// admits exactly that shape, and nothing broader. The refused alternative,
// #118 without a trap (a guard on the gofmt leg alone), was cut because a
// self-test or `go vet` exiting 2 would still read as "not run".
//
// Two clauses, the grep-gate pair. The first: every trap line is the allowed
// shape (only its message text may change, with no expansion in it), and
// there is at most one. The second holds even with the first's pattern
// loosened in this file: a trap of any spelling that names GREEN, has an exit
// that is not a literal 1..255, or is not one single-quoted action on EXIT
// alone, is refused. Both read a line with its quotes and backslashes
// removed, as the shell's quote removal would (Codex code pass: `"trap"`,
// `ex""it 0`); a command named through a variable (`$t '...' EXIT`) is past
// any grep and not claimed.
var (
	allowedTrap = regexp.MustCompile(`^trap 'rc=\$\?; \[ "\$rc" != 2 \] \|\| \[ -n "\$notrun" \] \|\| \{ echo "(?:[^"\\$` + "`" + `']|\\")*" >&2; exit 1; \}' EXIT$`)
	// The word trap anywhere on a non-comment line, read unquoted: a
	// command's start, after a separator, in a subshell, handed to eval.
	anyTrap  = regexp.MustCompile(`\btrap\b`)
	unquoted = strings.NewReplacer(`"`, "", "'", "", `\`, "", "`", "").Replace
	// The whole line one single-quoted action, on EXIT alone: a suffix
	// check passed `trap '' TERM # ' EXIT` (Codex code pass).
	exitOnly = regexp.MustCompile(`^trap '[^']*' EXIT$`)
	// An exit and its operand, read with each echo's double-quoted text
	// removed (the FAIL message itself says "exit 2"), and only that: a
	// double-quoted trap action is still read.
	echoed  = regexp.MustCompile(`echo "(?:[^"\\]|\\.)*"`)
	anyExit = regexp.MustCompile(`\bexit\b *([^ ;}]*)`)
	// 1..255: `exit 256` leaves 0.
	nonzero = regexp.MustCompile(`^(?:[1-9]|[1-9][0-9]|1[0-9][0-9]|2[0-4][0-9]|25[0-5])$`)
)

func deadJobTrapFaults(script string, allowed *regexp.Regexp) []string {
	var faults, traps []string
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") || !anyTrap.MatchString(unquoted(line)) {
			continue
		}
		traps = append(traps, line)
		// Clause one.
		if !allowed.MatchString(line) {
			faults = append(faults, fmt.Sprintf("check.sh has a trap of another shape: re-read whether a killed tier can still print its summary: %q", line))
		}
		// Clause two, whatever the first admitted.
		if strings.Contains(strings.ToUpper(unquoted(line)), "GREEN") {
			faults = append(faults, fmt.Sprintf("check.sh's trap names GREEN: a killed tier could print its summary: %q", line))
		}
		for _, e := range anyExit.FindAllStringSubmatch(unquoted(echoed.ReplaceAllString(line, `echo ""`)), -1) {
			if !nonzero.MatchString(e[1]) {
				faults = append(faults, fmt.Sprintf("check.sh's trap exits %q, not a literal status 1..255: a killed tier could read as passed: %q", e[1], line))
			}
		}
		if !exitOnly.MatchString(line) {
			faults = append(faults, fmt.Sprintf("check.sh's trap is not one single-quoted action on EXIT alone: a handled kill could resume the tier: %q", line))
		}
	}
	if len(traps) > 1 {
		faults = append(faults, fmt.Sprintf("check.sh has %d traps, one allowed: a later one replaces the first", len(traps)))
	}
	return faults
}

// The gate's controls, each a line in a stand-in script: the allowed trap
// passes (so a red above is not the gate refusing everything), and each
// breakage fires the clause it names, the second clause with the first
// loosened to admit anything.
func TestDeadJobTrapGate(t *testing.T) {
	const ok = `trap 'rc=$?; [ "$rc" != 2 ] || [ -n "$notrun" ] || { echo "check.sh: FAIL -- exit 2 is reserved for \"not run\" (#118)" >&2; exit 1; }' EXIT`
	wrap := func(l string) string { return "set -eu\nnotrun=\n" + l + "\necho done\n" }
	for _, c := range []struct {
		name, line, want string
	}{
		{"allowed", ok, ""},
		{"none", "# trap 'x' EXIT, a comment", ""},
		{"echoes GREEN", strings.Replace(ok, "check.sh: FAIL", "check.sh: HERMETIC GREEN", 1), "names GREEN"},
		{"exits 0", strings.Replace(ok, "exit 1;", "exit 0;", 1), `exits "0"`},
		{"bare exit", strings.Replace(ok, "exit 1;", "exit;", 1), `exits ""`},
		{"exit by variable", strings.Replace(ok, "exit 1;", "exit $rc;", 1), `exits "$rc"`},
		{"exits 256", strings.Replace(ok, "exit 1;", "exit 256;", 1), `exits "256"`},
		{"on INT", strings.Replace(ok, "' EXIT", "' INT", 1), "EXIT alone"},
		{"expansion in message", strings.Replace(ok, "(#118)", "$(date)", 1), "another shape"},
		{"indented", "  " + ok, "another shape"},
		{"in a subshell", "( trap 'echo x' EXIT; true )", "another shape"},
		{"reset", "trap - EXIT", "another shape"},
		{"through eval", `eval "trap 'exit 0' EXIT"`, "another shape"},
		{"quoted name", ok + "\n" + `"trap" 'echo "check.sh: HERMETIC GREEN"; exit 0' EXIT`, "2 traps"},
		{"escaped name", `t\rap 'exit 0' EXIT`, "another shape"},
		{"two", ok + "\n" + ok, "2 traps"},
	} {
		got := strings.Join(deadJobTrapFaults(wrap(c.line), allowedTrap), "\n")
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: faults %q, want one with %q", c.name, got, c.want)
		}
	}

	// The second clause stands on its own: with the first admitting anything,
	// a trap that prints GREEN or exits 0 is still refused.
	anything := regexp.MustCompile(`.`)
	for _, l := range []string{
		`trap 'echo "check.sh: HERMETIC GREEN"; exit 1' EXIT`,
		`trap 'exit 0' EXIT`,
		`trap 'echo "hermetic green"; exit 1' EXIT`,
		`trap 'exit' EXIT`,
		`trap "exit 0" EXIT`,
		`trap 'ex""it 0' EXIT`,
		`trap 'echo "GR""EEN"' EXIT`,
		`trap '' TERM # ' EXIT`,
		`trap 'rm -f x' INT`,
	} {
		if len(deadJobTrapFaults(wrap(l), anything)) == 0 {
			t.Errorf("a loosened first clause admits %q", l)
		}
	}
}
