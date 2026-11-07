package store

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// #72, amending D-134: no transfer door moves a claim reaching
// .buddy/slot/browser. The tabs and jobs it marks end with the holder's pane,
// and a slot handed on let that pane pass the close test with them open.
//
// Every door is TransferClaimsInRun; each case below is one door, refused with
// the browser claim in it and, as its positive control, granted with the very
// same call minus that claim, so "refused" cannot mean "never ran".
func TestTransferRefusesTheBrowserSlotAtEveryDoor(t *testing.T) {
	type door struct {
		name  string
		slugs []string // the call, browser claim included
		inRun string
	}
	doors := []door{
		{"one slug", []string{"tabs"}, ""},
		{"several slugs, all or nothing", []string{"work", "tabs", "notes"}, ""},
		{"a run handoff", []string{"orc-p", "run-1", "tabs"}, ""},
		{"--in-run parking with the run's holder", []string{"tabs"}, "run-9"},
	}
	for _, d := range doors {
		t.Run(d.name, func(t *testing.T) {
			st, clk := openTest(t)
			a := hello(t, st, "sess-a", "alpha", "/wt/a")
			b := hello(t, st, "sess-b", "bravo", "/wt/b")
			claims := map[string][]string{
				"tabs":  {".buddy/slot/browser"},
				"work":  {"src/a.go"},
				"notes": {"docs/n.md"},
				"orc-p": {".buddy/slot/orc-p"},
				"run-1": {".buddy/slot/run-1", ".buddy/slot/main"},
			}
			for slug, scopes := range claims {
				if err := st.Claim(a.SessionID, a.Incarnation, slug, "d", scopes); err != nil {
					t.Fatal(err)
				}
			}
			// The recipient holds the open run --in-run names.
			if err := st.Claim(b.SessionID, b.Incarnation, "run-9", "d", []string{".buddy/slot/run-9"}); err != nil {
				t.Fatal(err)
			}
			// A bystander waits on a claim in the set: a refusal must leave its wait.
			c := hello(t, st, "sess-c", "charlie", "/wt/c")
			if _, _, err := st.DeclareWaitReady(c.SessionID, c.Incarnation, []string{d.slugs[0]}, time.Hour, "", "abc"); err != nil {
				t.Fatal(err)
			}
			clk.advance(time.Minute)
			before := map[string]ClaimInfo{}
			for _, slug := range d.slugs {
				before[slug] = claimBySlug(t, st, slug)
			}

			_, _, err := st.TransferClaimsInRun(a.SessionID, a.Incarnation, d.slugs, mustResolve(t, st, "bravo"), d.inRun)
			var tr ErrTransferRefused
			if !errors.As(err, &tr) || !strings.Contains(tr.Why, `claim "tabs" reaches .buddy/slot/browser by ".buddy/slot/browser"`) ||
				!strings.Contains(tr.Why, "release it plainly once the browser work is over") {
				t.Fatalf("want the browser refusal, got %v", err)
			}
			for _, slug := range d.slugs {
				got, was := claimBySlug(t, st, slug), before[slug]
				if got.Owner.SessionID != a.SessionID || got.State != "open" || !got.Renewed.Equal(was.Renewed) || got.ClaimID != was.ClaimID {
					t.Fatalf("refused, so %q must stand as it was, alpha's: %+v, was %+v", slug, got, was)
				}
			}
			if w, ok, err := st.WaitOf(c.SessionID); err != nil || !ok || w.Verdict(clk.now()) != WaitPending {
				t.Fatalf("the bystander's wait must stand: %+v %v %v", w, ok, err)
			}

			// Positive control: the same door without the browser claim moves.
			var rest []string
			for _, s := range d.slugs {
				if s != "tabs" {
					rest = append(rest, s)
				}
			}
			if len(rest) == 0 {
				if err := st.Claim(a.SessionID, a.Incarnation, "plain", "d", []string{"src/p.go"}); err != nil {
					t.Fatal(err)
				}
				rest = []string{"plain"}
			}
			if _, _, err := st.TransferClaimsInRun(a.SessionID, a.Incarnation, rest, mustResolve(t, st, "bravo"), d.inRun); err != nil {
				t.Fatalf("control: the same door without the browser claim must move: %v", err)
			}
			for _, slug := range rest {
				if got := claimBySlug(t, st, slug); got.Owner.SessionID != b.SessionID {
					t.Fatalf("control: %q must now be bravo's: %+v", slug, got)
				}
			}
			if got := claimBySlug(t, st, "tabs"); got.Owner.SessionID != a.SessionID {
				t.Fatalf("the browser slot stays with alpha: %+v", got)
			}
		})
	}
}

// The single-slug wrapper is the same door.
func TestTransferClaimRefusesTheBrowserSlot(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.Claim(a.SessionID, a.Incarnation, "tabs", "d", []string{".buddy/slot/browser"}); err != nil {
		t.Fatal(err)
	}
	var tr ErrTransferRefused
	if _, _, err := st.TransferClaim(a.SessionID, a.Incarnation, "tabs", mustResolve(t, st, "bravo")); !errors.As(err, &tr) ||
		!strings.Contains(tr.Why, "the browser slot stays with its holder") || strings.Contains(tr.Why, "other scopes") {
		t.Fatalf("want the browser refusal, with no split remedy for a claim that is only the slot, got %v", err)
	}
	if err := st.Claim(a.SessionID, a.Incarnation, "plain", "d", []string{"src/p.go"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.TransferClaim(a.SessionID, a.Incarnation, "plain", mustResolve(t, st, "bravo")); err != nil {
		t.Fatalf("control: the wrapper moves a plain claim: %v", err)
	}
}

// Overlap, not equality, under the folding rule: an ancestor holds the slot,
// a path beneath it is in it, and case folds. A sibling name is no slot.
func TestTransferBrowserSlotIsReadByOverlap(t *testing.T) {
	cases := []struct {
		scope  string
		refuse bool
	}{
		{".buddy/slot/browser", true},
		{".buddy/slot/Browser", true},
		{".buddy/slot", true},
		{".buddy", true},
		{".buddy/slot/browser/job", true},
		{".buddy/slot/browsers", false},
		{".buddy/slot/browser-x", false},
		{".buddy/slot/main", false},
	}
	for _, tc := range cases {
		t.Run(tc.scope, func(t *testing.T) {
			st, _ := openTest(t)
			a := hello(t, st, "sess-a", "alpha", "/wt/a")
			hello(t, st, "sess-b", "bravo", "/wt/b")
			// Among other scopes: the call stays all or nothing.
			if err := st.Claim(a.SessionID, a.Incarnation, "x", "d", []string{"src/a.go", tc.scope}); err != nil {
				t.Fatal(err)
			}
			_, _, err := st.TransferClaims(a.SessionID, a.Incarnation, []string{"x"}, mustResolve(t, st, "bravo"))
			if !tc.refuse {
				if err != nil {
					t.Fatalf("%q is not the browser slot; it must move: %v", tc.scope, err)
				}
				return
			}
			var tr ErrTransferRefused
			if !errors.As(err, &tr) {
				t.Fatalf("want the browser refusal, got %v", err)
			}
			if got := claimBySlug(t, st, "x"); got.Owner.SessionID != a.SessionID || len(got.Scopes) != 2 {
				t.Fatalf("refused, so the claim stays whole with alpha: %+v", got)
			}
			if !strings.Contains(tr.Why, `by "`+tc.scope+`", and`) || !strings.Contains(tr.Why, "claim every scope of it reaching the slot alone under another slug") {
				t.Fatalf("the words and the remedy must name the scope actually held, %q: %s", tc.scope, tr.Why)
			}
		})
	}
}

// Parking in place is no transfer (the holder keeps it), and was already
// refused for anything under .buddy; it stays refused.
func TestParkingTheBrowserSlotInPlaceStaysRefused(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	for slug, sc := range map[string]string{"run-1": ".buddy/slot/run-1", "tabs": ".buddy/slot/browser", "work": "src/a.go"} {
		if err := st.Claim(a.SessionID, a.Incarnation, slug, "d", []string{sc}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.ParkClaims(a.SessionID, a.Incarnation, []string{"tabs"}, "run-1"); err == nil || !strings.Contains(err.Error(), "coordination is never parked") {
		t.Fatalf("parking the browser slot in place must stay refused by the .buddy test: %v", err)
	}
	if _, err := st.ParkClaims(a.SessionID, a.Incarnation, []string{"work"}, "run-1"); err != nil {
		t.Fatalf("control: parking a plain claim in place is allowed: %v", err)
	}
}

// Following the words once frees the claim: every reaching scope is named, and
// the remedy claims them as held, so a peer beneath the slot does not refuse it
// (Codex code pass: the first scope alone left {.buddy, .buddy/slot/browser} to
// refuse again, and claiming the whole slot met a peer's browser/b).
func TestTheBrowserRefusalsRemedyWorks(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.Claim(b.SessionID, b.Incarnation, "peer", "d", []string{".buddy/slot/browser/b"}); err != nil {
		t.Fatal(err)
	}
	// Stored out of order: the words sort them.
	held := []string{".buddy/slot/browser/a/x", "src/a.go", ".buddy/slot/browser/a"}
	if err := st.Claim(a.SessionID, a.Incarnation, "x", "d", held); err != nil {
		t.Fatal(err)
	}
	_, _, err := st.TransferClaims(a.SessionID, a.Incarnation, []string{"x"}, mustResolve(t, st, "bravo"))
	var tr ErrTransferRefused
	if !errors.As(err, &tr) || !strings.Contains(tr.Why, `by ".buddy/slot/browser/a", ".buddy/slot/browser/a/x", and`) {
		t.Fatalf("want both reaching scopes named, got %v", err)
	}
	// Do what the words say: claim them alone under another slug, release them
	// from this one, hand on the rest.
	reach := []string{".buddy/slot/browser/a", ".buddy/slot/browser/a/x"}
	if err := st.Claim(a.SessionID, a.Incarnation, "tabs", "d", reach); err != nil {
		t.Fatalf("the remedy's claim must be granted beside the peer: %v", err)
	}
	if _, _, err := st.ReleaseScopesID(a.SessionID, a.Incarnation, "x", reach); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.TransferClaims(a.SessionID, a.Incarnation, []string{"x"}, mustResolve(t, st, "bravo")); err != nil {
		t.Fatalf("after the remedy the rest moves: %v", err)
	}
	if got := claimBySlug(t, st, "tabs"); got.Owner.SessionID != a.SessionID {
		t.Fatalf("the slot's claim stays with alpha: %+v", got)
	}
}

// A long scope cannot push the remedy out of the fence's 1,024-byte line.
func TestTheBrowserRefusalFitsItsLine(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	hello(t, st, "sess-b", "bravo", "/wt/b")
	// A run of `"` doubles under quoting (Fable): capped after it, not before.
	long := ".buddy/slot/browser/" + strings.Repeat(`"`, 600)
	scopes := []string{long + "1", long + "2", long + "3", long + "4", "src/a.go"}
	slug := strings.Repeat(`"`, 600)
	if err := st.Claim(a.SessionID, a.Incarnation, slug, "d", scopes); err != nil {
		t.Fatal(err)
	}
	if err := st.Claim(a.SessionID, a.Incarnation, "more", "d", []string{".buddy/slot/browser/z"}); err != nil {
		t.Fatal(err)
	}
	_, _, err := st.TransferClaims(a.SessionID, a.Incarnation, []string{slug, "more"}, mustResolve(t, st, "bravo"))
	if err == nil || len(err.Error())+len("; nothing was handed on") > 1000 ||
		!strings.Contains(err.Error(), "and 1 more") || !strings.Contains(err.Error(), "then release them from this one") ||
		!strings.HasSuffix(err.Error(), "; 1 other claim(s) named reach it too") {
		t.Fatalf("capped values, the remedy kept, under the line: %d bytes: %v", len(err.Error()), err)
	}
}

// The browser refusal covers every transfer because one statement changes a
// claim's holder. A second one (a new door) must come with its own check, and
// this test says where to look. holderWrites is a tripwire, not a parser: the
// spellings it reads are in the control table below, and the ones it does not
// (a row-value SET, a quoted column, a subquery's WHERE before the assignment, a
// statement built by concatenation) are recorded open in D-134's amendment.
var (
	holderUpdate = regexp.MustCompile(`(?is)UPDATE\s+(?:OR\s+\w+\s+)?["\[]?(?:main\.)?claims["\]]?(?:\s+(?:AS\s+)?\w+)?\s+SET\b([^` + "`" + `]*?)(?:\bWHERE\b|` + "`" + `)`)
	holderCol    = regexp.MustCompile(`(?i)\bsession_id\s*=`)
	holderOther  = regexp.MustCompile(`(?is)\b(?:INSERT\s+OR\s+REPLACE\s+INTO|REPLACE\s+INTO)\s+["\[]?(?:main\.)?claims\b|\bDO\s+UPDATE\s+SET\b[^` + "`" + `]*?\bsession_id\s*=|\bUPDATE\s+(?:OR\s+\w+\s+)?claim_scopes\s+SET\b[^` + "`" + `]*?\bclaim_id\s*=`)
)

func holderWrites(src string) int {
	n := len(holderOther.FindAllStringIndex(src, -1))
	for _, m := range holderUpdate.FindAllStringSubmatch(src, -1) {
		if holderCol.MatchString(m[1]) {
			n++
		}
	}
	return n
}

func TestHolderWritesReadsTheSpellings(t *testing.T) {
	for src, want := range map[string]int{
		"`UPDATE claims SET session_id=?, incarnation=? WHERE claim_id=?`":                                1,
		"`UPDATE OR ABORT claims SET session_id=?, incarnation=? WHERE x=?`":                              1,
		"`UPDATE \"claims\" SET incarnation=?, session_id=? WHERE x=?`":                                   1,
		"`UPDATE main.claims SET session_id=?`":                                                           1,
		"`UPDATE claims AS c SET session_id=? WHERE c.claim_id=?`":                                        1,
		"`INSERT OR REPLACE INTO claims (claim_id) VALUES (?)`":                                           1,
		"`REPLACE INTO claims (claim_id) VALUES (?)`":                                                     1,
		"`INSERT INTO claims (x) VALUES (?) ON CONFLICT(x) DO UPDATE SET session_id=excluded.session_id`": 1,
		"`UPDATE claim_scopes SET claim_id=? WHERE claim_id=?`":                                           1,
		// Inert: a WHERE that selects by holder moves nothing.
		"`UPDATE claims SET state='orphaned', renewed=? WHERE state='open' AND session_id=?`": 0,
		"`UPDATE claims SET in_run='', producer_session='' WHERE claim_id=?`":                 0,
		"`INSERT INTO claims (claim_id, session_id) VALUES (?, ?)`":                           0,
	} {
		if got := holderWrites(src); got != want {
			t.Errorf("holderWrites(%s) = %d, want %d", src, got, want)
		}
	}
}

func TestOneStatementChangesAClaimsHolder(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var where []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i := holderWrites(string(b)); i > 0; i-- {
			where = append(where, f)
		}
	}
	if len(where) != 1 || where[0] != "transfer.go" {
		t.Fatalf("statements changing a claim's holder: %v; want exactly one, in transfer.go. A new door that moves a claim must refuse the browser slot (browserSlotStays, #72)", where)
	}
}

// Past three reaching scopes the list is cut, so the remedy says every scope
// reaching the slot, and following that frees the claim (Codex re-verify: "the
// ones named above" left the fourth to refuse again).
func TestTheBrowserRemedyCoversACutList(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	hello(t, st, "sess-b", "bravo", "/wt/b")
	reach := []string{".buddy/slot/browser/a", ".buddy/slot/browser/b", ".buddy/slot/browser/c", ".buddy/slot/browser/d"}
	if err := st.Claim(a.SessionID, a.Incarnation, "x", "d", append([]string{"src/a.go"}, reach...)); err != nil {
		t.Fatal(err)
	}
	_, _, err := st.TransferClaims(a.SessionID, a.Incarnation, []string{"x"}, mustResolve(t, st, "bravo"))
	if err == nil || !strings.Contains(err.Error(), "and 1 more") || !strings.Contains(err.Error(), "claim every scope of it reaching the slot alone") {
		t.Fatalf("a cut list, and a remedy naming every reaching scope: %v", err)
	}
	if err := st.Claim(a.SessionID, a.Incarnation, "tabs", "d", reach); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ReleaseScopesID(a.SessionID, a.Incarnation, "x", reach); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.TransferClaims(a.SessionID, a.Incarnation, []string{"x"}, mustResolve(t, st, "bravo")); err != nil {
		t.Fatalf("every reaching scope moved out, the rest moves: %v", err)
	}
}
