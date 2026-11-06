package cli

import (
	"database/sql"
	"path/filepath"
	"regexp"
	"slices"
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
	for _, want := range []string{"withdrew", "run1", "overlap stands", "bravo", "neither claim yields to the other"} {
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

// Issue #65, through the verbs and all the words: two holders each parking a claim
// that declares the other's run. The store's table reads every pair of marks; this is
// what a holder and a reader are told. The controls are the one winner at each step.
func TestCrossDeclaredThroughTheVerbs(t *testing.T) {
	boundedParallel(t)
	r := newRiderFx(t)
	const path = "src/c.go"
	const asleep = "(declares nothing while parked)"
	const riderLine = "a claim parked in an open run is handed on beside a rider only to that rider's holder"
	r.ok(t, "sess-b", "claim", "run2", "--desc", "x", "--scope", ".buddy/slot/run2")
	r.ok(t, "sess-a", "claim", "own", "--desc", "x", "--scope", path)
	r.ok(t, "sess-a", "release", "own", "--in-run", "run1")
	r.ok(t, "sess-b", "claim", "held", "--desc", "x", "--based-on", "run1", "--scope", path)
	if reason, denied := r.edit("sess-b", path); denied {
		t.Fatalf("the legitimate overlap: an active declarer over a rider: %s", reason)
	}
	if reason, denied := r.edit("sess-a", path); !denied {
		t.Fatalf("the rider's holder must be denied: %s", reason)
	}
	// The control for the listing's words: an active declarer's row does not carry them.
	if view := r.ok(t, "sess-a", "ls"); !strings.Contains(view, "BASED ON run run1") || strings.Contains(view, asleep) {
		t.Fatalf("ls, active declarer:\n%s", view)
	}
	out := r.ok(t, "sess-b", "release", "held", "--in-run", "run2")
	for _, want := range []string{"overlap stands", `held by alpha, claim "own"`, "while parked it declares nothing"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the park must say %q:\n%s", want, out)
		}
	}
	r.ok(t, "sess-a", "claim", "own", "--desc", "x", "--based-on", "run2", "--scope", path)
	if reason, denied := r.edit("sess-a", path); denied {
		t.Fatalf("declaring run2, active: %s", reason)
	}
	if out := r.ok(t, "sess-a", "release", "own", "--in-run", "run1"); !strings.Contains(out, `held by bravo, claim "held"`) {
		t.Fatalf("the second park must report the pair:\n%s", out)
	}

	for _, sid := range []string{"sess-a", "sess-b"} {
		if reason, denied := r.edit(sid, path); !denied {
			t.Fatalf("the pair: %s must be denied: %s", sid, reason)
		}
	}
	r.stage(t, r.wtB, path)
	var warned string
	r.asSession("sess-b", func() { _, warned, _ = r.run(t, r.wtB, "", "commit-gate") })
	if !strings.Contains(warned, `claim "own"`) {
		t.Fatalf("the commit gate must agree with the gate, naming the other side:\n%s", warned)
	}
	r.stage(t, r.cwdOf("sess-a"), path)
	r.asSession("sess-a", func() { _, warned, _ = r.run(t, r.cwdOf("sess-a"), "", "commit-gate") })
	if !strings.Contains(warned, `claim "held"`) {
		t.Fatalf("the commit gate, the other session:\n%s", warned)
	}

	// What the pair reads as, where a reader looks for it.
	for name, view := range map[string]string{"ls": r.ok(t, "sess-a", "ls"), "whose": r.ok(t, "sess-a", "whose", path)} {
		if !strings.Contains(view, "BASED ON run run2 "+asleep) || !strings.Contains(view, "BASED ON run run1 "+asleep) {
			t.Fatalf("%s:\n%s", name, view)
		}
	}

	// A lane on run2's tree cannot be parked into run1 over run2's rider: refused, and
	// the line saying why --based-on is no remedy here.
	r.ok(t, "sess-b", "claim", "rider2", "--desc", "x", "--scope", "src/d.go")
	r.ok(t, "sess-b", "release", "rider2", "--in-run", "run2")
	r.ok(t, "sess-c", "claim", "third", "--desc", "x", "--based-on", "run2", "--scope", "src/d.go")
	refused, errw, code := r.as(t, "sess-c", "release", "third", "--to", "alpha", "--in-run", "run1")
	if code != 1 || !strings.Contains(errw, "nothing was handed on") || !strings.Contains(refused, "by bravo") ||
		!strings.Contains(refused, riderLine) {
		t.Fatalf("the refused park must say why: exit %d:\n%s\n%s", code, refused, errw)
	}
	// The same for a claim a plain --to keeps parked: the run with its side of the pair.
	refused, errw, code = r.as(t, "sess-a", "release", "run1", "own", "--to", "charlie")
	if code != 1 || !strings.Contains(errw, "nothing was handed on") || !strings.Contains(refused, riderLine) {
		t.Fatalf("the run with its side: exit %d:\n%s\n%s", code, refused, errw)
	}
	// The control: the same call with no rider in the way parks, and says nothing of it.
	r.ok(t, "sess-c", "claim", "plain", "--desc", "x", "--scope", "src/e.go")
	if out, _, code := r.as(t, "sess-c", "release", "plain", "--to", "alpha", "--in-run", "run1"); code != 0 || strings.Contains(out, riderLine) {
		t.Fatalf("a park with no overlap: exit %d:\n%s", code, out)
	}

	// Unparked, one side wins: the pair resolves with the verb that exists.
	if out := r.ok(t, "sess-a", "release", "own", "--unpark"); strings.Contains(out, "overlap stands") {
		t.Fatalf("own now declares run2 and held yields, so nothing stands:\n%s", out)
	}
	if reason, denied := r.edit("sess-a", path); denied {
		t.Fatalf("unparked and declaring: %s", reason)
	}
	if reason, denied := r.edit("sess-b", path); !denied {
		t.Fatalf("the other side must stay denied: %s", reason)
	}
	r.asSession("sess-a", func() { _, warned, _ = r.run(t, r.cwdOf("sess-a"), "", "commit-gate") })
	if strings.Contains(warned, `claim "held"`) {
		t.Fatalf("the winner's commit gate must agree with its gate:\n%s", warned)
	}

	// The listing's words follow the run: closed, the rider declares again.
	r.ok(t, "sess-b", "release", "run2", "--outcome", "fail", "--note", "red")
	if view := r.ok(t, "sess-a", "ls"); !strings.Contains(view, "PARKED in CLOSED run run2") || strings.Contains(view, asleep) {
		t.Fatalf("ls, the run closed:\n%s", view)
	}
}

// The rider line prints only for a rider of an open run. The two controls are the
// refused hand-ons with nothing of that kind in the set: a fixer handed a path an
// admitted lane holds, and a claim handed on over a closed run's rider.
func TestRefusedHandOnSaysNothingOfRiders(t *testing.T) {
	boundedParallel(t)
	r := newRiderFx(t)
	const line = "a rider above yields only"
	r.ok(t, "sess-b", "claim", "next", "--desc", "n", "--based-on", "run1", "--scope", "src/a.go")
	out, errw, code := r.as(t, "sess-a", "release", "item", "--to", "charlie")
	if code != 1 || !strings.Contains(out, "by bravo") || strings.Contains(out, line) {
		t.Fatalf("over an admitted lane: exit %d:\n%s\n%s", code, out, errw)
	}
	r.ok(t, "sess-a", "release", "run1", "--outcome", "fail", "--note", "red")
	out, errw, code = r.as(t, "sess-b", "release", "next", "--to", "charlie")
	if code != 1 || !strings.Contains(out, "by alpha") || strings.Contains(out, line) {
		t.Fatalf("over a closed run's rider: exit %d:\n%s\n%s", code, out, errw)
	}
}

// Issue #67: a holder keeps a shared claim beside its own parked one. An exclusive declarer
// is refused by the shared claim and a shared one by the rider. Every request, forecast and
// real, names both and whom to ask, and sends nobody from one to the other as a remedy.
func TestSharedRemedyDoesNotLoop(t *testing.T) {
	boundedParallel(t)
	r := newRiderFx(t)
	const path = "src/c.go"
	const stuck = `a shared claim here is refused by rider "own" (alpha, parked in run run1) and an exclusive one by SHARED claim "s" (alpha), while both stand: ask alpha to release "s"`
	const condition = "Then, only if your tree is built on that run's tree: --based-on run1 without --shared; nothing checks that, and a tree without the work integrates without complaint"
	r.ok(t, "sess-a", "claim", "own", "--desc", "x", "--scope", path)
	r.ok(t, "sess-a", "release", "own", "--in-run", "run1")
	r.ok(t, "sess-a", "claim", "s", "--desc", "x", "--shared", "--scope", path)
	base := []string{"claim", "lane", "--desc", "x", "--scope", path}
	for _, request := range [][]string{{}, {"--based-on", "run1"}, {"--shared"}, {"--shared", "--based-on", "run1"}} {
		for _, flags := range [][]string{{"--dry-run"}, {}} {
			args := slices.Concat(base, request, flags)
			out := r.refused("sess-b", "alpha", args...)
			if !strings.Contains(out, stuck) || !strings.Contains(out, condition) {
				t.Fatalf("%v does not say what each retry meets:\n%s", args, out)
			}
			for _, refusedAdvice := range []string{"claiming --shared would clear", "PARKED:"} {
				if strings.Contains(out, refusedAdvice) {
					t.Fatalf("%v still advises what is refused (%s):\n%s", args, refusedAdvice, out)
				}
			}
		}
	}
	// Each half of what it says, tried: the rider meets --shared, the shared claim the declarer.
	if out := r.refused("sess-b", "alpha", "claim", "lane", "--desc", "x", "--shared", "--scope", path); !strings.Contains(out, `claim "own"`) || strings.Contains(out, `claim "s")`) {
		t.Fatalf("the shared retry must meet the rider alone:\n%s", out)
	}
	if out := r.refused("sess-b", "alpha", "claim", "lane", "--desc", "x", "--based-on", "run1", "--scope", path); !strings.Contains(out, `claim "s")`) || strings.Contains(out, `claim "own")`) {
		t.Fatalf("the declarer must meet the shared claim alone:\n%s", out)
	}
	// A rider on one scope and a shared claim on another: refused whole, the same trap.
	r.ok(t, "sess-a", "claim", "s2", "--desc", "x", "--shared", "--scope", "src/e.go")
	r.ok(t, "sess-a", "claim", "own2", "--desc", "x", "--scope", "src/f.go")
	r.ok(t, "sess-a", "release", "own2", "--in-run", "run1")
	if out := r.refused("sess-b", "alpha", "claim", "lane", "--desc", "x", "--scope", "src/e.go", "--scope", "src/f.go"); !strings.Contains(out, `rider "own2"`) || !strings.Contains(out, `SHARED claim "s2"`) {
		t.Fatalf("across two scopes:\n%s", out)
	}
	// The control: the rider alone keeps its old line, and no STUCK.
	r.ok(t, "sess-a", "claim", "own3", "--desc", "x", "--scope", "src/g.go")
	r.ok(t, "sess-a", "release", "own3", "--in-run", "run1")
	if out := r.refused("sess-b", "alpha", "claim", "lane", "--desc", "x", "--scope", "src/g.go"); strings.Contains(out, "STUCK:") || !strings.Contains(out, "--based-on run1 without --shared") || !strings.Contains(out, "PARKED:") {
		t.Fatalf("a rider alone:\n%s", out)
	}
	// The other control: the shared claim alone keeps its old line, which is true there.
	r.ok(t, "sess-a", "claim", "s3", "--desc", "x", "--shared", "--scope", "src/h.go")
	if out := r.refused("sess-b", "alpha", "claim", "lane", "--desc", "x", "--based-on", "run1", "--scope", "src/h.go"); strings.Contains(out, "STUCK:") || !strings.Contains(out, "claiming --shared would clear") {
		t.Fatalf("a shared claim alone:\n%s", out)
	}
	// A shared rider beside a shared claim: D-042 admits --shared, so no trap.
	r.ok(t, "sess-a", "claim", "s4", "--desc", "x", "--shared", "--scope", "src/i.go")
	r.ok(t, "sess-a", "claim", "s5", "--desc", "x", "--shared", "--scope", "src/i.go")
	r.ok(t, "sess-a", "release", "s4", "--in-run", "run1")
	if out := r.refused("sess-b", "alpha", "claim", "lane", "--desc", "x", "--based-on", "run1", "--scope", "src/i.go"); strings.Contains(out, "STUCK:") {
		t.Fatalf("a shared rider:\n%s", out)
	}
	// The way out it names, taken: the declarer is granted once the shared claim is released.
	r.ok(t, "sess-a", "release", "s")
	if out := r.ok(t, "sess-b", "claim", "lane", "--desc", "x", "--based-on", "run1", "--scope", path); !strings.Contains(out, "BASED ON run run1") {
		t.Fatalf("the cure:\n%s", out)
	}
	if why, denied := r.edit("sess-b", path); denied {
		t.Fatalf("the cure must pass the gate: %s", why)
	}
	r.ok(t, "sess-b", "release", "lane")
	// A closed run: nothing yields, and the run has nothing to offer, so no STUCK.
	r.ok(t, "sess-a", "claim", "s", "--desc", "x", "--shared", "--scope", path)
	r.ok(t, "sess-a", "release", "run1", "--outcome", "fail", "--note", "red")
	if out := r.refused("sess-b", "alpha", "claim", "lane", "--desc", "x", "--based-on", "run1", "--scope", path); strings.Contains(out, "STUCK:") || !strings.Contains(out, "declared nothing") {
		t.Fatalf("a closed run:\n%s", out)
	}
}

// Issue #67 at the gate. The deny's two pieces of advice, each refused in this state, give
// way to the same words; the deny itself stands, and a state without the pair keeps its own.
func TestGateWordsInTheTwoRemediesState(t *testing.T) {
	boundedParallel(t)
	r := newRiderFx(t)
	const path = "src/c.go"
	const stuck = `a shared claim here is refused by rider "own" (alpha, parked in run run1) and an exclusive one by SHARED claim "s" (alpha), while both stand: ask alpha to release "s"`
	const declarerAdvice = "claim the path exclusively"
	const sharedAdvice = "--shared --scope <path>"
	r.ok(t, "sess-a", "claim", "own", "--desc", "x", "--scope", path)
	r.ok(t, "sess-a", "release", "own", "--in-run", "run1")
	// The control first: the rider alone, and the old advice is true.
	if why, denied := r.edit("sess-b", path); !denied || !strings.Contains(why, declarerAdvice) || strings.Contains(why, "while both stand") {
		t.Fatalf("a rider alone: %v %s", denied, why)
	}
	// A shared claim parked in the run yields too, so the advice is still true.
	r.ok(t, "sess-a", "claim", "s", "--desc", "x", "--shared", "--scope", path)
	r.ok(t, "sess-a", "release", "s", "--in-run", "run1")
	if why, denied := r.edit("sess-b", path); !denied || !strings.Contains(why, declarerAdvice) {
		t.Fatalf("both parked: %v %s", denied, why)
	}
	// Admit a declarer over both, then unpark: the state, with a declarer standing in it.
	r.ok(t, "sess-b", "claim", "lane", "--desc", "x", "--based-on", "run1", "--scope", path)
	if why, denied := r.edit("sess-b", path); denied {
		t.Fatalf("the control: the admitted lane passes: %s", why)
	}
	r.ok(t, "sess-a", "release", "s", "--unpark")
	why, denied := r.edit("sess-b", path)
	if !denied || !strings.Contains(why, stuck) || strings.Contains(why, sharedAdvice) {
		t.Fatalf("the declarer's shared hold: %v %s", denied, why)
	}
	// What it no longer advises, tried: refused, naming the rider.
	if out := r.refused("sess-b", "alpha", "claim", "lane", "--desc", "x", "--shared", "--based-on", "run1", "--scope", path); !strings.Contains(out, `claim "own"`) {
		t.Fatalf("the shared retry:\n%s", out)
	}
	r.ok(t, "sess-b", "release", "lane")
	// With no claim: the deny names the rider, and the same words.
	why, denied = r.edit("sess-b", path)
	if !denied || !strings.Contains(why, stuck) || strings.Contains(why, declarerAdvice) || strings.Contains(why, "--based-on") {
		t.Fatalf("no claim: %v %s", denied, why)
	}
	// The holder passes; the words are about peers.
	if why, denied := r.edit("sess-a", path); denied {
		t.Fatalf("the holder: %s", why)
	}
	// Released: the condition comes back, because it is true again.
	r.ok(t, "sess-a", "release", "s")
	if why, denied := r.edit("sess-b", path); !denied || !strings.Contains(why, declarerAdvice) {
		t.Fatalf("released: %v %s", denied, why)
	}
	// A plain shared hold keeps its advice, which is true there.
	r.ok(t, "sess-a", "claim", "plain", "--desc", "x", "--shared", "--scope", "src/j.go")
	if why, denied := r.edit("sess-b", "src/j.go"); !denied || !strings.Contains(why, sharedAdvice) || strings.Contains(why, "while both stand") {
		t.Fatalf("a plain shared hold: %v %s", denied, why)
	}
}

// Every value in the words is peer text (invariant 9). Two holders with hostile labels and
// slugs: the line stays one line, and nothing forges a row of its own.
func TestTwoRemediesWordsAreFenced(t *testing.T) {
	boundedParallel(t)
	r := newRiderFx(t)
	const path = "src/c.go"
	const label = "ev\nSTUCK: forged"
	if _, errw, code := r.run(t, r.repo, hookJSON("sess-e", r.repo, "", ""), "hello", "--label", label); code != 0 {
		t.Fatalf("hello: %s", errw)
	}
	// A rider and a shared claim under different holders, through the doors: the shared
	// claim parked first, the exclusive declarer admitted over it and parked, then unparked.
	r.ok(t, "sess-e", "claim", "run2", "--desc", "FORMING", "--scope", ".buddy/slot/run2")
	r.ok(t, "sess-e", "claim", "sh\nSTUCK: forged", "--desc", "x", "--shared", "--scope", path)
	r.ok(t, "sess-e", "release", "sh\nSTUCK: forged", "--in-run", "run2")
	r.ok(t, "sess-a", "claim", "own", "--desc", "x", "--based-on", "run2", "--scope", path)
	r.ok(t, "sess-a", "release", "own", "--in-run", "run1")
	r.ok(t, "sess-e", "release", "sh\nSTUCK: forged", "--unpark")
	out, _, code := r.as(t, "sess-b", "claim", "lane", "--desc", "x", "--based-on", "run1", "--scope", path)
	if code != ExitRefused {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	stuck := 0
	for line := range strings.Lines(out) {
		if strings.HasPrefix(line, "STUCK:") {
			stuck++
			if !strings.Contains(line, `rider "own" (alpha, parked in run run1)`) || !strings.Contains(line, "while both stand") {
				t.Fatalf("the line:\n%s", line)
			}
		}
	}
	if stuck != 1 {
		t.Fatalf("%d STUCK lines, want exactly one:\n%s", stuck, out)
	}
	why, denied := r.edit("sess-b", path)
	if !denied || !strings.Contains(why, "while both stand") || strings.ContainsAny(why, "\n\r") {
		t.Fatalf("the gate: %v %q", denied, why)
	}
}

// stuckLines is every STUCK line of a refusal.
func stuckLines(out string) []string {
	var got []string
	for line := range strings.Lines(out) {
		if strings.HasPrefix(line, "STUCK:") {
			got = append(got, line)
		}
	}
	return got
}

// Where STUCK must stay silent, or say it once. Each reads a refusal that is otherwise real:
// each case's control is that the claim named is in the set, and the exit is a refusal.
func TestTwoRemediesOnlyForThePair(t *testing.T) {
	boundedParallel(t)
	t.Run("an exclusive blocker on another scope is not the shared claim", func(t *testing.T) {
		r := newRiderFx(t)
		r.ok(t, "sess-a", "claim", "own", "--desc", "x", "--scope", "src/c.go")
		r.ok(t, "sess-a", "release", "own", "--in-run", "run1")
		r.ok(t, "sess-c", "claim", "excl", "--desc", "x", "--scope", "src/k.go")
		out := r.refused("sess-b", "charlie", "claim", "lane", "--desc", "x", "--scope", "src/c.go", "--scope", "src/k.go")
		if !strings.Contains(out, `"own"`) || !strings.Contains(out, `"excl"`) || len(stuckLines(out)) != 0 {
			t.Fatalf("no shared claim stands:\n%s", out)
		}
	})
	t.Run("a rider of a closed run is no rider of the run re-formed under its slug", func(t *testing.T) {
		r := newRiderFx(t)
		r.ok(t, "sess-a", "claim", "own", "--desc", "x", "--scope", "src/c.go")
		r.ok(t, "sess-a", "release", "own", "--in-run", "run1")
		r.ok(t, "sess-a", "claim", "s", "--desc", "x", "--shared", "--scope", "src/c.go")
		r.ok(t, "sess-a", "release", "run1", "--outcome", "fail", "--note", "red")
		r.ok(t, "sess-a", "claim", "run1", "--desc", "FORMING", "--scope", ".buddy/slot/run1")
		out := r.refused("sess-b", "alpha", "claim", "lane", "--desc", "x", "--based-on", "run1", "--scope", "src/c.go")
		if !strings.Contains(out, `"own"`) || len(stuckLines(out)) != 0 {
			t.Fatalf("that rider yields to nothing:\n%s", out)
		}
	})
	t.Run("one state on two scopes is said once", func(t *testing.T) {
		r := newRiderFx(t)
		r.ok(t, "sess-a", "claim", "own", "--desc", "x", "--scope", "src/c.go", "--scope", "src/d.go")
		r.ok(t, "sess-a", "release", "own", "--in-run", "run1")
		r.ok(t, "sess-a", "claim", "s", "--desc", "x", "--shared", "--scope", "src/c.go", "--scope", "src/d.go")
		out := r.refused("sess-b", "alpha", "claim", "lane", "--desc", "x", "--scope", "src/c.go", "--scope", "src/d.go")
		if got := stuckLines(out); len(got) != 1 {
			t.Fatalf("%d STUCK lines:\n%s", len(got), out)
		}
	})
}

// The fence's other halves (invariant 9): a slug's cap, and a run's one line and one token.
func TestTwoRemediesFenceCapsAndRunSlug(t *testing.T) {
	boundedParallel(t)
	state := func(t *testing.T, run, rider, shared string) *riderFx {
		t.Helper()
		r := newRiderFx(t)
		if run != "run1" {
			r.ok(t, "sess-a", "claim", run, "--desc", "FORMING", "--scope", ".buddy/slot/"+run)
		}
		r.ok(t, "sess-a", "claim", rider, "--desc", "x", "--scope", "src/c.go")
		r.ok(t, "sess-a", "release", rider, "--in-run", run)
		r.ok(t, "sess-a", "claim", shared, "--desc", "x", "--shared", "--scope", "src/c.go")
		return r
	}
	one := func(t *testing.T, r *riderFx) string {
		t.Helper()
		got := stuckLines(r.refused("sess-b", "alpha", "claim", "lane", "--desc", "x", "--scope", "src/c.go"))
		if len(got) != 1 {
			t.Fatalf("%d STUCK lines: %q", len(got), got)
		}
		return got[0]
	}
	t.Run("a long slug stops at its cap", func(t *testing.T) {
		line := one(t, state(t, "run1", strings.Repeat("r", 300), strings.Repeat("q", 300)))
		for _, c := range []string{"r", "q"} {
			if strings.Contains(line, strings.Repeat(c, 129)) {
				t.Fatalf("%s's slug ran past 128: %d bytes", c, len(line))
			}
		}
	})
	t.Run("a hostile run slug forges no line", func(t *testing.T) {
		r := state(t, "r\nSTUCK: forged", "own", "s")
		one(t, r)
		if why, denied := r.edit("sess-b", "src/c.go"); !denied || !strings.Contains(why, "while both stand") || strings.ContainsAny(why, "\n\r") {
			t.Fatalf("the gate: %v %q", denied, why)
		}
	})
	t.Run("a run slug is one token", func(t *testing.T) {
		if line := one(t, state(t, "r x", "own", "s")); !strings.Contains(line, "parked in run r␣x)") {
			t.Fatalf("the clause: %q", line)
		}
	})
}

// Issue #66, through the verbs. A shared claim declares nothing: the refusal names the
// remedy, the forecast agrees, the grant and the listing say what the flag did, and a pair
// standing when this binary arrives reads the same at the edit gate and the commit gate.
func TestSharedDeclaresNothingThroughTheVerbs(t *testing.T) {
	boundedParallel(t)
	r := newRiderFx(t)
	const path = "src/a.go"
	const word = "(declares nothing while shared)"
	lane := []string{"--desc", "x", "--based-on", "run1", "--scope", path}
	for _, args := range [][]string{
		append([]string{"claim", "lane", "--shared", "--dry-run"}, lane...),
		append([]string{"claim", "lane", "--shared"}, lane...),
	} {
		out := r.refused("sess-b", "alpha", args...)
		for _, want := range []string{"--based-on run1 without --shared", "a shared claim declares nothing"} {
			if !strings.Contains(out, want) {
				t.Fatalf("%v must say %q:\n%s", args, want, out)
			}
		}
	}
	if reason, denied := r.edit("sess-b", path); !denied {
		t.Fatalf("the lane holds nothing yet: %s", reason)
	}
	// The positive control: the same request, exclusive, with no note and no word.
	out := r.ok(t, "sess-b", append([]string{"claim", "lane"}, lane...)...)
	if !strings.Contains(out, "BASED ON run run1") || strings.Contains(out, "declares nothing") {
		t.Fatalf("exclusive, declared:\n%s", out)
	}
	if reason, denied := r.edit("sess-b", path); denied {
		t.Fatalf("the exclusive declarer over the rider: %s", reason)
	}
	if view := r.ok(t, "sess-a", "ls"); strings.Contains(view, word) {
		t.Fatalf("ls, exclusive declarer:\n%s", view)
	}

	// Granted on a free path, or beside a shared rider: D-042's, and the note says so.
	const free = "docs/x.md"
	if out := r.ok(t, "sess-b", "claim", "fp", "--dry-run", "--shared", "--desc", "x", "--based-on", "run1", "--scope", free); !strings.Contains(out, "a shared claim declares nothing") {
		t.Fatalf("the forecast of a grant:\n%s", out)
	}
	if out := r.ok(t, "sess-b", "claim", "fp", "--shared", "--desc", "x", "--based-on", "run1", "--scope", free); !strings.Contains(out, "a shared claim declares nothing") {
		t.Fatalf("the grant:\n%s", out)
	}
	if view := r.ok(t, "sess-a", "ls"); !strings.Contains(view, "BASED ON run run1 "+word) {
		t.Fatalf("ls, a shared claim:\n%s", view)
	}

	// A pair standing: "fp" as an earlier binary left it, over the rider's other path.
	const second = "src/b.go"
	db, err := sql.Open("sqlite", filepath.Join(r.repo, ".git", "buddy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE claim_scopes SET scope=?, folded=? WHERE claim_id=(SELECT claim_id FROM claims WHERE slug='fp' AND state='open')`, second, second); err != nil {
		t.Fatal(err)
	}
	reason, denied := r.edit("sess-b", second)
	if !denied || !strings.Contains(reason, `"item"`) || !strings.Contains(reason, "claim the path exclusively") {
		t.Fatalf("the standing declarer at the gate: %v %s", denied, reason)
	}
	r.stage(t, r.wtB, second)
	var warned string
	r.asSession("sess-b", func() { _, warned, _ = r.run(t, r.wtB, "", "commit-gate") })
	if !strings.Contains(warned, `claim "item"`) {
		t.Fatalf("the commit gate must agree with the gate:\n%s", warned)
	}
	// Handing the run on with its rider is refused while it stands, naming the pair.
	out, _, code := r.as(t, "sess-a", "release", "run1", "item", "--to", "charlie")
	if code == 0 || !strings.Contains(out, `claim "fp"`) {
		t.Fatalf("the handoff over a standing pair, exit %d:\n%s", code, out)
	}
	// Handing the declarer on says what the rider yields to: --based-on is no remedy here.
	out, _, code = r.as(t, "sess-b", "release", "fp", "--to", "charlie")
	if code == 0 || !strings.Contains(out, "yields only to an exclusive claim declaring its run") {
		t.Fatalf("the declarer's handoff, exit %d:\n%s", code, out)
	}
}
