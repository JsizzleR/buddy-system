package cli

import (
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
	if strings.Contains(script, "\ntrap ") {
		t.Errorf("check.sh has a top-level trap: re-read whether a killed tier can still print its summary")
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
