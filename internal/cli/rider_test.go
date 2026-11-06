package cli

import (
	"database/sql"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// D-133 (issue #61), through the verbs: what a lane and a coordinator type and
// read. The ledger's own rule, door by door, is internal/store/parked_test.go;
// this holds the exit codes, the words, the gate's hook and the commit gate.

type riderFx struct {
	*waitFx
	t *testing.T
}

// newRiderFx: alpha (sess-a) holds run1; charlie (sess-c) made the item and
// parked it there; bravo (sess-b) is the new lane, in its own worktree.
func newRiderFx(t *testing.T) *riderFx {
	t.Helper()
	w := newWaitFx(t)
	if _, errw, code := w.run(t, w.repo, hookJSON("sess-c", w.repo, "", ""), "hello", "--label", "charlie"); code != 0 {
		t.Fatalf("hello c: %s", errw)
	}
	w.ok(t, "sess-a", "claim", "run1", "--desc", "FORMING", "--scope", ".buddy/slot/run1", "--scope", ".buddy/slot/main")
	w.ok(t, "sess-c", "claim", "item", "--desc", "model=m class=code the item", "--scope", "src/a.go", "--scope", "src/b.go")
	out := w.ok(t, "sess-c", "release", "item", "--to", "alpha", "--in-run", "run1")
	if !strings.Contains(out, "parked in run run1") {
		t.Fatalf("the handoff does not say it parked:\n%s", out)
	}
	return &riderFx{waitFx: w, t: t}
}

func (r *riderFx) refused(sid string, wantHolder string, args ...string) string {
	r.t.Helper()
	out, errw, code := r.as(r.t, sid, args...)
	if code != ExitRefused {
		r.t.Fatalf("%v as %s: exit %d, want %d (refused): %s %s", args, sid, code, ExitRefused, out, errw)
	}
	if !strings.Contains(out, "by "+wantHolder) {
		r.t.Fatalf("%v: the refusal does not name %s:\n%s", args, wantHolder, out)
	}
	return out
}

// edit is the gate's verdict on a session's Edit, from that session's own tree.
func (r *riderFx) edit(sid, rel string) (string, bool) {
	r.t.Helper()
	cwd := r.cwdOf(sid)
	out, _, _ := r.run(r.t, cwd, hookJSON(sid, cwd, "Edit", filepath.Join(cwd, rel)), "gate")
	return decodeDeny(r.t, out)
}

func (r *riderFx) outcomeOf(slug string) string {
	r.t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(r.repo, ".git", "buddy.db"))
	if err != nil {
		r.t.Fatal(err)
	}
	defer db.Close()
	var got sql.NullString
	if err := db.QueryRow(`SELECT outcome FROM claims WHERE slug=?`, slug).Scan(&got); err != nil {
		r.t.Fatal(err)
	}
	return got.String
}

func TestRiderYieldsThroughTheVerbsAndAllThreeGates(t *testing.T) {
	boundedParallel(t)
	r := newRiderFx(t)
	r.ok(t, "sess-a", "claim", "run2", "--desc", "x", "--scope", ".buddy/slot/run2")

	// Undeclared, or naming another run: refused, exit 4, naming the holder, and
	// the note says the condition beside the flag.
	out := r.refused("sess-b", "alpha", "claim", "next", "--desc", "n", "--scope", "src/a.go")
	for _, want := range []string{"PARKED", "--based-on run1", "nothing checks that"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the refusal must say %q:\n%s", want, out)
		}
	}
	r.refused("sess-b", "alpha", "claim", "next", "--desc", "n", "--based-on", "run2", "--scope", "src/a.go")
	if out := r.refused("sess-b", "alpha", "claim", "next", "--dry-run", "--desc", "n", "--scope", "src/a.go"); !strings.Contains(out, "PARKED") {
		t.Fatalf("the forecast must carry the note its refusal does:\n%s", out)
	}
	reason, denied := r.edit("sess-b", "src/a.go")
	if !denied || !strings.Contains(reason, "parked in run run1") || !strings.Contains(reason, "nothing checks that") || strings.Contains(reason, "--based-on") {
		t.Fatalf("the gate, undeclared: says the condition, never the flag: denied %v: %s", denied, reason)
	}

	// Commit gate, undeclared: the control for the agreement below.
	r.stage(t, r.wtB, "src/a.go")
	var warned string
	r.asSession("sess-b", func() { _, warned, _ = r.run(t, r.wtB, "", "commit-gate") })
	if !strings.Contains(warned, "item") {
		t.Fatalf("the commit gate, undeclared, must report the rider:\n%s", warned)
	}

	// Declared: the dry run, the claim, the gate and the commit gate all agree.
	if out := r.ok(t, "sess-b", "claim", "next", "--dry-run", "--desc", "n", "--based-on", "run1", "--scope", "src/a.go"); !strings.Contains(out, "no conflicts") {
		t.Fatalf("the forecast:\n%s", out)
	}
	if out := r.ok(t, "sess-b", "claim", "next", "--desc", "n", "--based-on", "run1", "--scope", "src/a.go"); !strings.Contains(out, "BASED ON run run1") {
		t.Fatalf("the grant does not say what it declared:\n%s", out)
	}
	if reason, denied := r.edit("sess-b", "src/a.go"); denied {
		t.Fatalf("the gate, declared: %s", reason)
	}
	if reason, denied := r.edit("sess-b", "src/b.go"); !denied {
		t.Fatalf("the rider's other path must still refuse: %s", reason)
	}
	r.asSession("sess-b", func() { _, warned, _ = r.run(t, r.wtB, "", "commit-gate") })
	if strings.Contains(warned, "item") {
		t.Fatalf("the commit gate, declared, must agree with the gate:\n%s", warned)
	}

	// The coordinator's re-claim over the admitted lane is refused, naming it.
	r.refused("sess-a", "bravo", "claim", "item", "--desc", "x", "--scope", "src/a.go", "--scope", "src/b.go")

	// `ls`, `who` and `whose` show the parked status, the run and the owner.
	for name, view := range map[string]string{
		"ls":    r.ok(t, "sess-a", "ls"),
		"who":   r.ok(t, "sess-a", "who", "alpha"),
		"whose": r.ok(t, "sess-a", "whose", "src/a.go"),
	} {
		for _, want := range []string{"PARKED in run run1", "produced by charlie", "BASED ON run run1"} {
			if name == "who" && want == "BASED ON run run1" {
				continue // alpha's own view holds no declaration
			}
			if !strings.Contains(view, want) {
				t.Fatalf("%s must show %q:\n%s", name, want, view)
			}
		}
	}

	// Red: the run closes. Refused at every door, and the words change with it.
	r.ok(t, "sess-a", "release", "run1", "--outcome", "fail", "--note", "red")
	reason, denied = r.edit("sess-b", "src/a.go")
	if !denied || strings.Contains(reason, "parked in run") {
		t.Fatalf("after a red the admitted lane is denied, with no way in offered: denied %v: %s", denied, reason)
	}
	out = r.refused("sess-c", "alpha", "claim", "late", "--desc", "n", "--based-on", "run1", "--scope", "src/a.go", "--scope", "src/b.go")
	if !strings.Contains(out, "bravo") || strings.Count(out, "REFUSED:") != 3 {
		t.Fatalf("a later claim reports every blocker:\n%s", out)
	}
	if !strings.Contains(out, "has closed") || !strings.Contains(out, "declared nothing") || strings.Contains(out, "--based-on run1;") {
		t.Fatalf("the refusal must say the run closed and offer no flag:\n%s", out)
	}
	if view := r.ok(t, "sess-a", "ls"); !strings.Contains(view, "PARKED in CLOSED run run1") {
		t.Fatalf("ls after a red:\n%s", view)
	}

	// The item's own release carries its outcome.
	r.ok(t, "sess-a", "release", "item", "--outcome", "fail", "--note", "ejected")
	if got := r.outcomeOf("item"); got != "fail" {
		t.Fatalf("outcome %q", got)
	}
}

func TestUnparkThroughTheVerb(t *testing.T) {
	boundedParallel(t)
	r := newRiderFx(t)
	r.ok(t, "sess-b", "claim", "next", "--desc", "n", "--based-on", "run1", "--scope", "src/a.go")
	out := r.ok(t, "sess-a", "release", "item", "--unpark")
	for _, want := range []string{"withdrew", "run1", "overlap stands", "bravo"} {
		if !strings.Contains(out, want) {
			t.Fatalf("unpark must say %q:\n%s", want, out)
		}
	}
	if reason, denied := r.edit("sess-b", "src/a.go"); !denied {
		t.Fatalf("ejected: the admitted lane must be denied: %s", reason)
	}
	r.refused("sess-c", "alpha", "claim", "late", "--desc", "n", "--based-on", "run1", "--scope", "src/b.go")
	if _, errw, code := r.as(t, "sess-a", "release", "item", "--unpark"); code != 1 || !strings.Contains(errw, "not parked") {
		t.Fatalf("a second unpark: exit %d: %s", code, errw)
	}
}

func TestParkingRefusalsNameTheReason(t *testing.T) {
	boundedParallel(t)
	r := newRiderFx(t)
	r.ok(t, "sess-c", "claim", "other", "--desc", "x", "--scope", "other/x.go")
	if _, errw, code := r.as(t, "sess-c", "release", "other", "--to", "bravo", "--in-run", "run1"); code != 1 ||
		!strings.Contains(errw, "does not hold that run") || !strings.Contains(errw, "nothing was handed on") {
		t.Fatalf("a recipient not holding the run: exit %d: %s", code, errw)
	}
	if out := r.ok(t, "sess-c", "who", "other"); !strings.Contains(out, "charlie") {
		t.Fatalf("nothing may move:\n%s", out)
	}
	if _, errw, code := r.as(t, "sess-c", "claim", "n", "--desc", "x", "--based-on", "no-such", "--scope", "free/x.go"); code != 1 || !strings.Contains(errw, "no claim of that name") {
		t.Fatalf("a declaration of no run: exit %d: %s", code, errw)
	}
	// An open claim that is no run: exit 1 from the claim and from its dry run, whose
	// usual refusal exit is 4, and nothing is taken.
	r.ok(t, "sess-a", "claim", "not-a-run", "--desc", "x", "--scope", "notes/x")
	for _, args := range [][]string{
		{"claim", "n", "--desc", "x", "--based-on", "not-a-run", "--scope", "free/x.go"},
		{"claim", "n", "--dry-run", "--desc", "x", "--based-on", "not-a-run", "--scope", "free/x.go"},
	} {
		if _, errw, code := r.as(t, "sess-c", args...); code != 1 || !strings.Contains(errw, "not a run") {
			t.Fatalf("%v: exit %d: %s", args, code, errw)
		}
	}
	if got := claimState(t, r.repo, "n"); got != "" {
		t.Fatalf("a refused declaration took a claim: %s", got)
	}
	r.ok(t, "sess-c", "release", "other", "--to", "alpha", "--in-run", "run1") // the control
}

// What a grant says about the declaration it did or did not make, and about a
// rider its re-claim withdrew. Refusals have their own test; these are the words a
// lane reads on exit 0, where nothing else tells it the flag did nothing.
func TestClaimNotesOnAGrant(t *testing.T) {
	boundedParallel(t)
	r := newRiderFx(t)
	// The control: an open run is declared, and the words say so.
	if out := r.ok(t, "sess-b", "claim", "open-run", "--desc", "n", "--based-on", "run1", "--scope", "free/a.go"); !strings.Contains(out, "BASED ON run run1") || strings.Contains(out, "declared nothing") {
		t.Fatalf("declared, run open:\n%s", out)
	}
	// A re-claim of the parked rider on its own scopes withdraws it, and says so.
	r.ok(t, "sess-c", "claim", "item2", "--desc", "x", "--scope", "item2/x.go")
	r.ok(t, "sess-c", "release", "item2", "--to", "alpha", "--in-run", "run1")
	if out := r.ok(t, "sess-a", "claim", "item2", "--desc", "x", "--scope", "item2/x.go"); !strings.Contains(out, "withdrew it from the run") {
		t.Fatalf("a re-claim of a parked rider must say it withdrew it:\n%s", out)
	}
	if out := r.ok(t, "sess-a", "claim", "item2", "--desc", "y", "--scope", "item2/x.go"); strings.Contains(out, "withdrew") {
		t.Fatalf("the control: a second re-claim withdrew nothing:\n%s", out)
	}
	r.ok(t, "sess-a", "release", "run1", "--outcome", "fail", "--note", "red")
	// A closed run: exit 0 on a free path, from the dry run and the claim, saying it
	// declared nothing and never saying BASED ON.
	for _, args := range [][]string{
		{"claim", "closed-run", "--dry-run", "--desc", "n", "--based-on", "run1", "--scope", "free/b.go"},
		{"claim", "closed-run", "--desc", "n", "--based-on", "run1", "--scope", "free/b.go"},
	} {
		if out := r.ok(t, "sess-b", args...); !strings.Contains(out, "declared nothing") || strings.Contains(out, "BASED ON") {
			t.Fatalf("%v:\n%s", args, out)
		}
	}
}

// Malformed uses end in `refused: <reason>` (D-098), before the ledger moves.
// Issue #63, through the verb: a run closes on a red and its coordinator parks the
// rider in the next one, keeping it.
func TestParkInPlaceThroughTheVerb(t *testing.T) {
	boundedParallel(t)
	r := newRiderFx(t)
	r.ok(t, "sess-a", "release", "run1", "--outcome", "fail", "--note", "red")
	r.ok(t, "sess-a", "claim", "run2", "--desc", "FORMING", "--scope", ".buddy/slot/run2", "--scope", ".buddy/slot/main")
	r.refused("sess-b", "alpha", "claim", "next", "--desc", "n", "--based-on", "run2", "--scope", "src/a.go") // the control

	out := r.ok(t, "sess-a", "release", "item", "--in-run", "run2")
	if !strings.Contains(out, `parked "item" in run run2 — it stays open and yours`) || strings.Contains(out, "overlap stands") {
		t.Fatalf("the line:\n%s", out)
	}
	ls := r.ok(t, "sess-a", "ls")
	if !strings.Contains(ls, "PARKED in run run2") || !strings.Contains(ls, "produced by charlie") {
		t.Fatalf("ls must show the new run and the same producer:\n%s", ls)
	}
	r.ok(t, "sess-b", "claim", "next", "--desc", "n", "--based-on", "run2", "--scope", "src/a.go")
	if reason, denied := r.edit("sess-b", "src/a.go"); denied {
		t.Fatalf("the declarer's edit: %s", reason)
	}
	if _, denied := r.edit("sess-b", "src/b.go"); !denied {
		t.Fatal("the gate's control: no claim of its own on src/b.go")
	}
	if got := r.outcomeOf("item"); got != "" {
		t.Fatalf("a park reports no outcome, got %q", got)
	}
	if got := claimState(t, r.repo, "item"); got != "open" {
		t.Fatalf("a park releases nothing: %s", got)
	}
}

// Several slugs, one line; what stands is a pair of claims, in words that do not
// claim a session's verdict, because a second claim may yet clear that session's
// gate (TestParkInPlaceReportsPairsNotVerdicts holds the ledger's side).
func TestParkInPlaceSaysWhatStands(t *testing.T) {
	boundedParallel(t)
	r := newRiderFx(t)
	// A directory over the rider's files, so the line's two scopes differ.
	r.ok(t, "sess-b", "claim", "next", "--desc", "n", "--based-on", "run1", "--scope", "src")
	r.ok(t, "sess-a", "claim", "own", "--desc", "o", "--scope", "own/x.go")
	r.ok(t, "sess-a", "claim", "run2", "--desc", "FORMING", "--scope", ".buddy/slot/run2")
	out := r.ok(t, "sess-a", "release", "item", "own", "--in-run", "run2")
	for _, want := range []string{`parked 2 claims in run run2 in one transaction: "item", "own" — each stays open and yours`,
		`overlap stands: src/a.go (yours) and "src" held by bravo, claim "next"`, "does not yield"} {
		if !strings.Contains(out, want) {
			t.Fatalf("must say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "refuses the other") {
		t.Fatalf("a verdict on a session, which a second claim could make false:\n%s", out)
	}
	if _, denied := r.edit("sess-b", "src/a.go"); !denied {
		t.Fatal("the old declaration must not be admitted by the new run")
	}
}

// What a holder reads when the door refuses it, and exit 1 with nothing changed.
func TestParkInPlaceRefusals(t *testing.T) {
	boundedParallel(t)
	r := newRiderFx(t)
	r.ok(t, "sess-c", "claim", "other", "--desc", "x", "--scope", "other/x.go")
	if _, errw, code := r.as(t, "sess-c", "release", "other", "--in-run", "run1"); code != 1 ||
		!strings.Contains(errw, "does not hold that run") || !strings.Contains(errw, "nothing was parked") {
		t.Fatalf("a run someone else holds: exit %d: %s", code, errw)
	}
	if _, errw, code := r.as(t, "sess-a", "release", "item", "--to", "alpha", "--in-run", "run1"); code != 1 || !strings.Contains(errw, "no --to") {
		t.Fatalf("--to yourself: exit %d: %s", code, errw)
	}
	if _, errw, code := r.as(t, "sess-a", "release", "run1", "--in-run", "run1"); code != 1 || !strings.Contains(errw, "never parked") {
		t.Fatalf("a run in itself: exit %d: %s", code, errw)
	}
	for _, slug := range []string{"run1", "other", "item"} {
		if got := claimState(t, r.repo, slug); got != "open" {
			t.Fatalf("a refused park changed %s: %s", slug, got)
		}
	}
	if ls := r.ok(t, "sess-a", "ls"); strings.Count(ls, "PARKED") != 1 {
		t.Fatalf("only the fixture's rider is parked:\n%s", ls)
	}
	// The control: the same caller, a run it holds, a claim with no slot.
	r.ok(t, "sess-a", "claim", "own", "--desc", "o", "--scope", "own/x.go")
	if out := r.ok(t, "sess-a", "release", "own", "--in-run", "run1"); !strings.Contains(out, `parked "own" in run run1`) {
		t.Fatalf("the control:\n%s", out)
	}
}

// Invariant 9 on this door's own lines: several slugs, an overlap, and a refusal,
// each with a run's or a claim's slug that would fabricate a row or a column.
func TestParkInPlaceWordsAreFenced(t *testing.T) {
	boundedParallel(t)
	r := newRiderFx(t)
	const run = "bad\nFAKE row"
	r.ok(t, "sess-a", "claim", run, "--desc", "x", "--scope", ".buddy/slot/"+run)
	r.ok(t, "sess-a", "claim", "own", "--desc", "o", "--scope", "own/x.go")
	r.ok(t, "sess-b", "claim", "evil\nFAKE row", "--desc", "e", "--based-on", "run1", "--scope", "src/a.go")
	r.ok(t, "sess-c", "claim", "theirs", "--desc", "t", "--scope", "theirs/x.go")
	parked := r.ok(t, "sess-a", "release", "item", "own", "--in-run", run)
	// A holder's label reaches the refusal through %s, so it is the fence that keeps
	// this one line (strconv.Quote covers the slugs, and hides a missing fence on them).
	if _, errw, code := r.run(t, r.repo, hookJSON("sess-d", r.repo, "", ""), "hello", "--label", "lab\nFAKE row"); code != 0 {
		t.Fatalf("hello d: %s", errw)
	}
	r.ok(t, "sess-d", "claim", "run-d", "--desc", "x", "--scope", ".buddy/slot/run-d")
	_, refused, code := r.as(t, "sess-c", "release", "theirs", "--in-run", "run-d")
	if code != 1 || !strings.Contains(refused, "does not hold that run") || !strings.Contains(refused, "FAKE row") {
		t.Fatalf("the control: a run someone else holds, its holder named: exit %d: %s", code, refused)
	}
	if !strings.Contains(parked, "parked 2 claims") || !strings.Contains(parked, "overlap stands") || !strings.Contains(parked, `"evil⏎FAKE row"`) {
		t.Fatalf("the control: the words are not there, the slug fenced:\n%s", parked)
	}
	for name, view := range map[string]string{"parked": parked, "refused": refused} {
		for _, line := range strings.Split(view, "\n") {
			if strings.HasPrefix(line, "FAKE") {
				t.Fatalf("%s: a slug fabricated a row:\n%s", name, view)
			}
		}
	}
	if parkWordSplit.MatchString(parked) {
		t.Fatalf("a run's slug is not one column:\n%s", parked)
	}
}

func TestParkingFlagsRefusedAsUsage(t *testing.T) {
	boundedParallel(t)
	r := newRiderFx(t)
	for _, args := range [][]string{
		{"release", "item", "--in-run", ""},
		{"release", "item", "--in-run", " "},
		{"release", "item", "--in-run", "run1", "--unpark"},
		{"release", "item", "--to", "bravo", "--in-run", ""},
		{"release", "item", "--unpark", "--to", "bravo"},
		{"release", "item", "--unpark", "--outcome", "pass"},
		{"release", "item", "other", "--unpark"},
		{"release", "item", "--unpark", "--scope", "src/a.go"},
		{"claim", "n", "--desc", "x", "--based-on", "", "--scope", "free/x.go"},
	} {
		_, errw, code := r.as(t, "sess-a", args...)
		lines := strings.Split(strings.TrimSpace(errw), "\n")
		if code != 1 || !strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "refused: ") {
			t.Fatalf("%v: exit %d, last line must be refused: <reason> (D-098): %s", args, code, errw)
		}
	}
	// These three would otherwise reach releasePark and drop the flag, so the reason
	// is pinned, not only the shape; --note's own refusal would tell the reader to
	// add an --outcome, which is refused next.
	r.ok(t, "sess-a", "claim", "run2", "--desc", "FORMING", "--scope", ".buddy/slot/run2") // a run they could park in
	for _, args := range [][]string{
		{"release", "item", "--in-run", "run2", "--outcome", "pass"},
		{"release", "item", "--in-run", "run2", "--note", "n"},
		{"release", "item", "--in-run", "run2", "--scope", "src/a.go"},
	} {
		if _, errw, code := r.as(t, "sess-a", args...); code != 1 || !strings.Contains(errw, "--in-run parks the whole claim") {
			t.Fatalf("%v: exit %d: %s", args, code, errw)
		}
	}
	if got := claimState(t, r.repo, "item"); got != "open" {
		t.Fatalf("a usage refusal changed the claim: %s", got)
	}
	if got := r.outcomeOf("item"); got != "" {
		t.Fatalf("a usage refusal reported an outcome: %q", got)
	}
	out := r.ok(t, "sess-a", "ls")
	if strings.Contains(out, "free/x.go") {
		t.Fatalf("a usage refusal took a claim:\n%s", out)
	}
	if !strings.Contains(out, "PARKED in run run1") || strings.Contains(out, "PARKED in run run2") {
		t.Fatalf("a usage refusal moved the mark:\n%s", out)
	}
}

func claimState(t *testing.T, repo, slug string) string {
	t.Helper()
	st, err := store.Open(filepath.Join(repo, ".git", "buddy.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	all, err := st.Claims(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range all {
		if c.Slug == slug {
			return c.State
		}
	}
	return ""
}

var parkWordSplit = regexp.MustCompile(`run \S*FAKE row`)

// Invariant 9: a run's slug is peer text and reaches every listing a claim does.
func TestParkWordsAreFenced(t *testing.T) {
	boundedParallel(t)
	r := newRiderFx(t)
	const run = "bad\nFAKE row"
	r.ok(t, "sess-a", "claim", run, "--desc", "x", "--scope", ".buddy/slot/"+run)
	r.ok(t, "sess-c", "claim", "item2", "--desc", "x", "--scope", "item2/x.go")
	r.ok(t, "sess-c", "release", "item2", "--to", "alpha", "--in-run", run)
	refusal := r.refused("sess-b", "alpha", "claim", "n", "--desc", "x", "--scope", "item2/x.go")
	reason, denied := r.edit("sess-b", "item2/x.go")
	if !denied {
		t.Fatal("the control: the rider must refuse")
	}
	check := func(when string, views map[string]string) {
		t.Helper()
		for name, view := range views {
			if !strings.Contains(view, "PARKED") && !strings.Contains(view, "parked") && !strings.Contains(view, "BASED ON") && !strings.Contains(view, "declared nothing") {
				t.Fatalf("%s %s: the control: the words are not there:\n%s", when, name, view)
			}
			for _, line := range strings.Split(view, "\n") {
				if strings.HasPrefix(line, "FAKE") {
					t.Fatalf("%s %s: a run's slug fabricated a row:\n%s", when, name, view)
				}
			}
			// One COLUMN too (fence.Field), for the words this change prints: a space
			// left in the run's slug would add a token where fence.Line passes above.
			if parkWordSplit.MatchString(view) {
				t.Fatalf("%s %s: a run's slug is not one column:\n%s", when, name, view)
			}
		}
	}
	r.ok(t, "sess-a", "claim", "here", "--desc", "x", "--scope", "here/x.go")
	parkedHere := r.ok(t, "sess-a", "release", "here", "--in-run", run)
	based := r.ok(t, "sess-b", "claim", "declared", "--desc", "x", "--based-on", run, "--scope", "free/x.go")
	check("open", map[string]string{
		"park in place": parkedHere,
		"ls":            r.ok(t, "sess-a", "ls"), "who": r.ok(t, "sess-a", "who", "alpha"), "whose": r.ok(t, "sess-a", "whose", "item2/x.go"),
		"refusal": refusal, "gate": reason, "grant": based, "who declarer": r.ok(t, "sess-a", "who", "bravo"),
	})
	// The same slug through the closed-run and the withdraw words.
	r.ok(t, "sess-a", "release", run, "--outcome", "fail", "--note", "red")
	closedGrant := r.ok(t, "sess-c", "claim", "closed", "--desc", "x", "--based-on", run, "--scope", "free/y.go")
	if !strings.Contains(closedGrant, "declared nothing") {
		t.Fatalf("the control: a closed run:\n%s", closedGrant)
	}
	check("closed", map[string]string{
		"ls": r.ok(t, "sess-a", "ls"), "refusal": r.refused("sess-b", "alpha", "claim", "n", "--desc", "x", "--scope", "item2/x.go"), "grant": closedGrant,
	})
}

// D-133's kept finding, the reason a coordinator never gives back a last scope:
// `release --scope` on it closes the claim with no outcome, and nothing can report
// one afterwards.
func TestLastScopeClosesTheClaimWithNoOutcome(t *testing.T) {
	boundedParallel(t)
	r := newRiderFx(t)
	r.ok(t, "sess-a", "claim", "one-file", "--desc", "d", "--scope", "src/c.go")
	r.ok(t, "sess-a", "release", "one-file", "--scope", "src/c.go")
	if got := claimState(t, r.repo, "one-file"); got != "released" {
		t.Fatalf("the last scope should close the claim: %s", got)
	}
	if _, _, code := r.as(t, "sess-a", "release", "one-file", "--outcome", "pass"); code == 0 {
		t.Fatal("an outcome was taken for a claim the last scope closed")
	}
	if got := r.outcomeOf("one-file"); got != "" {
		t.Fatalf("the last scope recorded outcome %q, want none", got)
	}
}
