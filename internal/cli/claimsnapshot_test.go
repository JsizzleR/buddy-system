package cli

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// dropSessionRow plants what nothing in buddy does (issue #126): an open claim
// whose holder has no sessions row. Only a hand-edited ledger reaches it.
func (f *fixture) dropSessionRow(t *testing.T, id string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(f.repo, ".git", "buddy.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`DELETE FROM sessions WHERE session_id=?`, id); err != nil {
		t.Fatal(err)
	}
}

// Issue #126. A claim whose holder has no session row loaded zero rows through
// OwnerOf's INNER JOIN, so the gate ALLOWED an edit `buddy claim` refused. Now
// every door holds it, and every view and deny that judges the holder says the
// row is MISSING and names the one recipe that frees it, which the test then
// RUNS and watches the claim go: `sweep --force` and `release --session` both
// need the row and free nothing (panel, measured), so naming either would send
// the operator to a remedy that does not work.
func TestAMissingSessionRowHoldsAndItsRemedyFreesIt(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "held", "holding", "internal/api")
	f.stage(t, f.wtB, "internal/api/x.go")
	edit := hookJSON("sess-b", f.wtB, "Edit", filepath.Join(f.wtB, "internal/api/x.go"))

	views := func() map[string]string {
		v := map[string]string{}
		out, _, _ := f.run(t, f.wtB, edit, "gate")
		reason, denied := decodeDeny(t, out)
		if !denied {
			t.Fatalf("the gate must deny an edit under the claim:\n%s", out)
		}
		v["gate"] = reason
		out, _, code := f.run(t, f.wtB, "", "claim", "wants", "--session", "sess-b", "--desc", "w", "--scope", "internal/api")
		if code == 0 {
			t.Fatal("the claim door must refuse")
		}
		v["refusal"] = out
		whose, _, _ := f.run(t, f.repo, "", "whose", "internal/api/x.go")
		v["whose"] = rowWith(t, whose, "held")
		f.asSession("sess-b", func() { _, v["commit gate"], _ = f.run(t, f.wtB, "", "commit-gate") })
		ls, _, _ := f.run(t, f.repo, "", "ls")
		v["ls"] = rowWith(t, ls, "held")
		return v
	}
	const remedy = "buddy hello --session sess-a </dev/null, then buddy bye sess-a, then a plain buddy sweep"

	// Control: a holder WITH a row is never called missing.
	for name, out := range views() {
		if strings.Contains(out, "MISSING") {
			t.Fatalf("control, %s: a holder with a row is never MISSING:\n%s", name, out)
		}
	}

	f.dropSessionRow(t, "sess-a")
	v := views() // the gate's deny is the #126 fix: it allowed here
	for _, name := range []string{"gate", "refusal", "whose", "commit gate"} {
		if !strings.Contains(v[name], "MISSING") || !strings.Contains(v[name], remedy) {
			t.Fatalf("%s must say the row is MISSING and name the remedy %q:\n%s", name, remedy, v[name])
		}
		for _, wrong := range []string{"sweep --force", "--session <its id>", "SILENT", "live but", "live,", "session  (", "held by  ("} {
			if strings.Contains(v[name], wrong) {
				t.Fatalf("%s names %q, which a missing row makes false:\n%s", name, wrong, v[name])
			}
		}
	}

	if f := strings.Fields(v["whose"]); len(f) < 4 || f[3] != "sess-a" {
		t.Fatalf("whose names the holder by its id (Fable cross-check):\n%s", v["whose"])
	}
	if !strings.Contains(v["commit gate"], `claim "held" held by sess-a (`) {
		t.Fatalf("the commit gate names the holder by its id (Fable cross-check):\n%s", v["commit gate"])
	}
	if f := strings.Fields(v["ls"]); len(f) < 4 || f[1] != "sess-a" || f[3] != "MISSING" {
		t.Fatalf("ls names the holder by its id and its state MISSING (Codex code pass):\n%s", v["ls"])
	}
	if !strings.Contains(v["gate"], "claimed by session sess-a (") {
		t.Fatalf("the gate's deny names the holder by its id when it has no label:\n%s", v["gate"])
	}

	// The remedy, run as named.
	if _, errw, code := f.run(t, f.repo, "", "hello", "--session", "sess-a"); code != 0 {
		t.Fatalf("remedy, hello: %s", errw)
	}
	if out, errw, code := f.run(t, f.repo, "", "bye", "sess-a"); code != 0 {
		t.Fatalf("remedy, bye: %s %s", out, errw)
	}
	if out, errw, code := f.run(t, f.repo, "", "sweep"); code != 0 {
		t.Fatalf("remedy, sweep: %s %s", out, errw)
	}
	if out, _, _ := f.run(t, f.wtB, edit, "gate"); func() bool { _, d := decodeDeny(t, out); return d }() {
		t.Fatalf("after the remedy the edit must be allowed:\n%s", out)
	}
	f.claimFor(t, "sess-b", "wants", "w", "internal/api")
}

// Issue #126 (Codex design pass, Fable panel). gh-stack's slot check admits the
// caller when its OWN claim covers the slot. ClaimsTouching now LEFT JOINs
// sessions, so a claim whose row is gone carries the claim's own session id, and
// matching on that alone turned today's deny for a caller with no row into an
// allow. Control: with its row, the same caller is admitted.
func TestGHStackSlotNeedsTheCallersRow(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	claimGHSlot(t, f, "sess-b", "stacking")
	wantGateBoth(t, f, f.wtB, "sess-b", "gh stack rebase", false, "")
	f.dropSessionRow(t, "sess-b")
	wantGateBoth(t, f, f.wtB, "sess-b", "gh stack rebase", true, "MISSING")
}

// The SHARED arm of the gate's deny returned before the missing-holder words
// (Codex code pass, #126). Its advice (claim alongside) still works; it must also
// say nothing frees the claim itself. Control: with the row, no MISSING.
func TestASharedHoldWithAMissingRowSaysSo(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if out, errw, code := f.run(t, f.repo, "", "claim", "held", "--shared", "--session", "sess-a", "--desc", "holding", "--scope", "internal/api"); code != 0 {
		t.Fatalf("claim: %s %s", out, errw)
	}
	edit := hookJSON("sess-b", f.wtB, "Edit", filepath.Join(f.wtB, "internal/api/x.go"))
	deny := func() string {
		out, _, _ := f.run(t, f.wtB, edit, "gate")
		reason, denied := decodeDeny(t, out)
		if !denied || !strings.Contains(reason, "SHARED") {
			t.Fatalf("the shared hold must deny an unclaimed edit:\n%s", out)
		}
		return reason
	}
	if r := deny(); strings.Contains(r, "MISSING") {
		t.Fatalf("control: a holder with a row is never MISSING:\n%s", r)
	}
	f.dropSessionRow(t, "sess-a")
	r := deny()
	if !strings.Contains(r, "MISSING") || !strings.Contains(r, "buddy hello --session sess-a </dev/null") || !strings.Contains(r, "claimed SHARED by session sess-a (") {
		t.Fatalf("the shared deny must name the holder by id, say MISSING and the remedy:\n%s", r)
	}
}

// The remedy's live-process arm (Fable cross-check, run as named): a holder whose
// row was deleted while its harness still runs keeps its session_procs rows, so
// the plain bye refuses. The words name that and the forced exit; the test runs
// it and watches the claim go. Control: before the forced bye the edit is denied.
func TestAMissingRowWithALiveProcessNamesTheForcedBye(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.asProcess(4242, 7, func() {
		if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello"); code != 0 {
			t.Fatalf("hello: %s", errw)
		}
	})
	f.claimFor(t, "sess-a", "held", "holding", "internal/api")
	f.dropSessionRow(t, "sess-a")
	edit := hookJSON("sess-b", f.wtB, "Edit", filepath.Join(f.wtB, "internal/api/x.go"))
	denied := func() (string, bool) {
		out, _, _ := f.run(t, f.wtB, edit, "gate")
		return decodeDeny(t, out)
	}
	r, d := denied()
	if !d || !strings.Contains(r, "buddy bye sess-a --force") || !strings.Contains(r, "the holder is alive") {
		t.Fatalf("the deny must name the forced exit for a holder whose process runs:\n%s", r)
	}
	if _, _, code := f.run(t, f.repo, "", "hello", "--session", "sess-a"); code != 0 {
		t.Fatal("hello")
	}
	if _, _, code := f.run(t, f.repo, "", "bye", "sess-a"); code == 0 {
		t.Fatal("control: the plain bye must refuse while the holder's process runs")
	}
	if _, d := denied(); !d {
		t.Fatal("control: still denied before the forced bye")
	}
	if out, errw, code := f.run(t, f.repo, "", "bye", "sess-a", "--force"); code != 0 {
		t.Fatalf("bye --force: %s %s", out, errw)
	}
	if out, errw, code := f.run(t, f.repo, "", "sweep"); code != 0 {
		t.Fatalf("sweep: %s %s", out, errw)
	}
	if _, d := denied(); d {
		t.Fatal("after the forced bye and a sweep the edit must be allowed")
	}
}
