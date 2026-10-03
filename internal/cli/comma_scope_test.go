package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// A comma-joined --scope was granted as ONE literal path (D-066, issue #56).
// `claim fix --scope a,b,c` exited 0 and printed "scopes: a,b,c"; the ledger
// held one scope naming a file nobody has, `whose a` answered CLAIMED BY
// (none), a peer's exclusive claim on a was granted over it, and under a
// peer's SHARED claim the gate denied the holder's own edit of a. Measured on
// a reference fleet: 78 claims by 30 of 99 sessions, every one a comma list,
// and no tracked path in either repo contains a comma.

// legacyClaim writes a claim through the store, as a row already in a ledger
// from before the refusal (or a caller of the store API) would be: the CLI no
// longer lets a comma scope in.
func (f *fixture) legacyClaim(t *testing.T, session, slug string, scopes ...string) {
	t.Helper()
	f.legacyClaimMode(t, session, slug, false, scopes...)
}

func (f *fixture) legacyClaimMode(t *testing.T, session, slug string, shared bool, scopes ...string) {
	t.Helper()
	st, err := store.Open(filepath.Join(f.repo, ".git", "buddy.db"), func() time.Time { return f.clock })
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	si, known, err := st.SessionByID(session)
	if err != nil || !known {
		t.Fatalf("%s: known=%v err=%v", session, known, err)
	}
	if err := st.ClaimMode(si.SessionID, si.Incarnation, slug, "legacy", scopes, shared); err != nil {
		t.Fatal(err)
	}
}

// The invariant, whichever remedy lands: a claim that exits 0 covers every
// path its caller named, and a claim that does not cover them exits non-zero
// and writes nothing. (Written against the defect, remedy-agnostic; the
// refusal itself is pinned by the tests after it.)
func TestClaimNeverGrantsACommaListAsOneLiteralScope(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)

	// Positive control: one --scope per path is granted and covers the path,
	// so a failure below is the comma and not a fixture whose whose/claim
	// never answers.
	if _, errw, code := f.run(t, f.repo, "", "claim", "ctl", "--session", "sess-a", "--desc", "c",
		"--scope", "pkg/a.go", "--scope", "pkg/b.go"); code != 0 {
		t.Fatal(errw)
	}
	if out, _, _ := f.run(t, f.repo, "", "whose", "pkg/a.go"); !strings.Contains(out, "ctl") {
		t.Fatalf("control: whose must name the claim covering pkg/a.go: %q", out)
	}
	if _, errw, code := f.run(t, f.repo, "", "release", "ctl", "--session", "sess-a"); code != 0 {
		t.Fatal(errw)
	}

	out, errw, code := f.run(t, f.repo, "", "claim", "fix", "--session", "sess-a", "--desc", "d",
		"--scope", "pkg/a.go,pkg/b.go")
	if code == 0 {
		for _, p := range []string{"pkg/a.go", "pkg/b.go"} {
			if w, _, _ := f.run(t, f.repo, "", "whose", p); !strings.Contains(w, "fix") {
				t.Fatalf("claim exited 0 (%q) but does not cover %s:\n%s", strings.TrimSpace(out), p, w)
			}
		}
		// And a peer must be refused the path the holder was told it holds.
		if _, _, c := f.run(t, f.wtB, "", "claim", "grab", "--session", "sess-b", "--desc", "g",
			"--scope", "pkg/a.go"); c == 0 {
			t.Fatal("a peer was granted pkg/a.go exclusively over a claim that exited 0 naming it")
		}
		return
	}
	// Refused: whole (D-019), and naming the form that works.
	if !strings.Contains(errw, "--scope pkg/a.go --scope pkg/b.go") {
		t.Fatalf("a refused comma scope must name the repeated-flag form: %q", errw)
	}
	if ls, _, _ := f.run(t, f.repo, "", "ls"); strings.Contains(ls, "fix") {
		t.Fatalf("a refused claim wrote a row: %q", ls)
	}
}

// D-066 is REFUSE, not split: a split grant passes the test above and is the
// cut alternative. Every spelling of the flag is refused, with or without
// --shared, and a refused REFRESH of a claim the session already holds leaves
// that claim exactly as it was — scopes, desc and mode — not merely "no new
// row".
func TestACommaScopeIsRefusedWholeAndTheClaimItWouldRefreshStands(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "claim", "fix", "--session", "sess-a", "--desc", "orig",
		"--scope", "pkg/a.go"); code != 0 {
		t.Fatal(errw)
	}
	before, _, _ := f.run(t, f.repo, "", "ls")
	if !strings.Contains(before, "pkg/a.go — orig") {
		t.Fatalf("control: ls must show the claim being refreshed: %q", before)
	}
	for _, args := range [][]string{
		{"--scope", "pkg/a.go,pkg/b.go"},
		{"--scope=pkg/a.go,pkg/b.go"},
		{"-scope", "pkg/a.go,pkg/b.go"},
		{"-scope=pkg/a.go,pkg/b.go"},
		{"--scope", "pkg/x.go", "--scope", "pkg/a.go,pkg/b.go"},
		{"--shared", "--scope", "pkg/a.go,pkg/b.go"},
		{"--scope", "pkg/a.go,"},
		{"--scope", ","},
	} {
		argv := append([]string{"claim", "fix", "--session", "sess-a", "--desc", "changed"}, args...)
		_, errw, code := f.run(t, f.repo, "", argv...)
		if code == 0 || !strings.Contains(errw, "holds a comma") || !strings.Contains(errw, "nothing was claimed") {
			t.Fatalf("%q: a comma scope must be refused whole: code=%d %q", args, code, errw)
		}
		if after, _, _ := f.run(t, f.repo, "", "ls"); after != before {
			t.Fatalf("%q: a refused refresh changed the claim:\nbefore %q\nafter  %q", args, before, after)
		}
	}
	// Positive control: the same refresh without the comma IS applied, so the
	// unchanged rows above are the refusal and not a refresh that never runs.
	if _, errw, code := f.run(t, f.repo, "", "claim", "fix", "--session", "sess-a", "--desc", "changed",
		"--scope", "pkg/a.go", "--scope", "pkg/b.go"); code != 0 {
		t.Fatal(errw)
	}
	if after, _, _ := f.run(t, f.repo, "", "ls"); !strings.Contains(after, "pkg/a.go, pkg/b.go — changed") {
		t.Fatalf("control: the comma-free refresh must apply: %q", after)
	}
}

// The refusal comes before the ledger, as a usage error does: a repo with no
// ledger says what is wrong with the command, not that there is no ledger.
func TestACommaScopeIsRefusedBeforeTheLedgerIsRead(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t) // never `buddy init`ed
	_, errw, code := f.run(t, f.repo, "", "claim", "fix", "--desc", "d", "--scope", "pkg/a.go,pkg/b.go")
	if code == 0 || !strings.Contains(errw, "holds a comma") {
		t.Fatalf("a comma scope must be refused before the ledger is opened: code=%d %q", code, errw)
	}
	// Control: the comma-free claim reaches the ledger and fails THERE, so the
	// refusal above is the comma check and not any claim failing in this repo.
	_, errw, code = f.run(t, f.repo, "", "claim", "fix", "--desc", "d", "--scope", "pkg/a.go")
	if code == 0 || strings.Contains(errw, "holds a comma") {
		t.Fatalf("control: a comma-free claim with no ledger must fail on the ledger: code=%d %q", code, errw)
	}
}

// A forecast must not disagree with the claim it predicts: the dry run refuses
// a comma scope in the claim's own words, exits non-zero, and forecasts
// nothing ("would claim" names no scope a real claim would not take).
func TestClaimDryRunRefusesACommaScopeInTheClaimsOwnWords(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	_, realErr, realCode := f.run(t, f.repo, "", "claim", "fix", "--session", "sess-a", "--desc", "d",
		"--scope", "pkg/a.go,pkg/b.go")
	dryOut, dryErr, dryCode := f.run(t, f.repo, "", "claim", "fix", "--session", "sess-a", "--desc", "d",
		"--scope", "pkg/a.go,pkg/b.go", "--dry-run")
	if realCode == 0 || dryCode == 0 {
		t.Fatalf("both must refuse: claim=%d dry-run=%d", realCode, dryCode)
	}
	if dryErr != realErr {
		t.Fatalf("the dry run's refusal differs from the claim's:\nclaim   %q\ndry-run %q", realErr, dryErr)
	}
	if strings.Contains(dryOut, "would claim") {
		t.Fatalf("a refused dry run forecast a claim: %q", dryOut)
	}
	if ls, _, _ := f.run(t, f.repo, "", "ls"); strings.Contains(ls, "fix") {
		t.Fatalf("a refused claim wrote a row: %q", ls)
	}
	// Control: the repeated form forecasts clean, naming both scopes.
	out, errw, code := f.run(t, f.repo, "", "claim", "fix", "--session", "sess-a", "--desc", "d",
		"--scope", "pkg/a.go", "--scope", "pkg/b.go", "--dry-run")
	if code != 0 || !strings.Contains(out, "would claim: pkg/a.go, pkg/b.go") {
		t.Fatalf("control: the repeated form must forecast clean: code=%d %q %q", code, out, errw)
	}
}

// The refusal's fix line, pasted into a shell, must claim exactly what was
// meant — or not be printed. Each printed command is handed to a REAL sh (and
// zsh, whose EQUALS option expands a bare `=word`, where present) and the argv
// it produces is compared with the paths meant; then it is run as a claim and
// each path is checked covered.
func TestCommaScopeRefusalPrintsOnlyACommandThatClaimsWhatWasMeant(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	shells := []string{"sh"}
	if _, err := exec.LookPath("zsh"); err == nil {
		shells = append(shells, "zsh")
	} else {
		t.Log("zsh not on PATH: the EQUALS leg runs under sh only")
	}
	many := make([]string, 1000)
	for i := range many {
		many[i] = "pkg/f" + strings.Repeat("x", 10) + string(rune('a'+i%26))
	}
	cases := []struct {
		name   string
		scopes []string
		meant  []string // nil: no command may be printed
		want   string   // a substring the refusal must carry
	}{
		{"plain pair", []string{"pkg/a.go,pkg/b.go"}, []string{"pkg/a.go", "pkg/b.go"}, ""},
		{"mixed, padded, trailing", []string{"pkg/x.go", " pkg/a.go , pkg/b.go ,"}, []string{"pkg/x.go", "pkg/a.go", "pkg/b.go"}, ""},
		{"a space in a part", []string{"pkg/x y.go,pkg/z.go"}, []string{"pkg/x y.go", "pkg/z.go"}, ""},
		{"zsh EQUALS", []string{"=sh,pkg/a.go"}, []string{"=sh", "pkg/a.go"}, ""},
		{"an apostrophe", []string{"pkg/it's.go,pkg/b.go"}, []string{"pkg/it's.go", "pkg/b.go"}, ""},
		{"a newline: the fence would alter it", []string{"pkg/a\nb.go,pkg/c.go"}, nil, "one path each"},
		{"a part the repo refuses", []string{"pkg/a.go,../b"}, nil, "escapes the repo"},
		{"nothing but commas", []string{",,"}, nil, "one path each"},
		{"too long to print whole", []string{strings.Join(many, ",")}, nil, "one path each"},
		{"four comma scopes", []string{"a,b", "c,d", "e,f", "g,h"}, []string{"a", "b", "c", "d", "e", "f", "g", "h"}, "and 1 more hold a comma"},
	}
	for _, tc := range cases {
		argv := []string{"claim", "fix", "--session", "sess-a", "--desc", "d"}
		for _, s := range tc.scopes {
			argv = append(argv, "--scope", s)
		}
		_, errw, code := f.run(t, f.repo, "", argv...)
		if code == 0 || !strings.Contains(errw, "nothing was claimed") {
			t.Fatalf("%s: must be refused: code=%d %q", tc.name, code, errw)
		}
		if strings.Count(strings.TrimRight(errw, "\n"), "\n") > 0 {
			t.Fatalf("%s: the refusal must be ONE line: %q", tc.name, errw)
		}
		if tc.want != "" && !strings.Contains(errw, tc.want) {
			t.Fatalf("%s: the refusal must say %q: %q", tc.name, tc.want, errw)
		}
		if len(errw) > 2048 && tc.meant == nil {
			t.Fatalf("%s: a refusal with no command must stay short, got %d bytes", tc.name, len(errw))
		}
		_, cmd, printed := strings.Cut(strings.TrimSpace(errw), "Repeat the flag: ")
		if tc.meant == nil {
			if printed {
				t.Fatalf("%s: printed a command it must not: %q", tc.name, errw)
			}
			continue
		}
		if !printed {
			t.Fatalf("%s: no command printed: %q", tc.name, errw)
		}
		var want []string
		for _, m := range tc.meant {
			want = append(want, "--scope", m)
		}
		for _, sh := range shells {
			shArgs := []string{"-c", `printf '%s\0' ` + cmd}
			if sh == "zsh" {
				shArgs = append([]string{"-f"}, shArgs...)
			}
			raw, err := exec.Command(sh, shArgs...).Output()
			if err != nil {
				t.Fatalf("%s: %s could not parse the printed command %q: %v", tc.name, sh, cmd, err)
			}
			got := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
			if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
				t.Fatalf("%s: %s reads the printed command as %q, meant %q", tc.name, sh, got, want)
			}
		}
		// And the pasted command claims every path meant.
		if _, errw, code := f.run(t, f.repo, "", append([]string{"claim", "fix", "--session", "sess-a", "--desc", "d"}, want...)...); code != 0 {
			t.Fatalf("%s: the printed command was refused: %s", tc.name, errw)
		}
		for _, m := range tc.meant {
			if w, _, _ := f.run(t, f.repo, "", "whose", m); !strings.Contains(w, "fix") {
				t.Fatalf("%s: the printed command does not cover %q:\n%s", tc.name, m, w)
			}
		}
		if _, errw, code := f.run(t, f.repo, "", "release", "fix", "--session", "sess-a"); code != 0 {
			t.Fatal(errw)
		}
	}
}

// ls joined scopes with a bare "," while every other listing joins with ", ",
// so a two-scope claim and a one-scope claim whose path holds a comma printed
// byte-identical scope columns. The literal is made through the store, as a
// row already in a ledger (or a real path with a comma) would be.
func TestLsTellsTwoScopesFromOneScopeContainingAComma(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "claim", "two", "--session", "sess-a", "--desc", "x",
		"--scope", "pkg/c.go", "--scope", "pkg/d.go"); code != 0 {
		t.Fatal(errw)
	}
	f.legacyClaim(t, "sess-b", "one", "pkg/c.go,pkg/d.go")

	ls, _, _ := f.run(t, f.repo, "", "ls")
	cols := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(ls), "\n") {
		// The scopes column: from the first scope to the " — " before the desc.
		head, _, ok := strings.Cut(line, " — ")
		i := strings.Index(head, "pkg/")
		if !ok || i < 0 {
			t.Fatalf("ls row has no scopes column: %q", line)
		}
		cols[strings.Fields(line)[0]] = head[i:]
	}
	if cols["two"] == "" || cols["one"] == "" {
		t.Fatalf("both claims must be listed: %q", ls)
	}
	if cols["two"] == cols["one"] {
		t.Fatalf("ls renders two scopes and one comma-holding scope the same, %q:\n%s", cols["two"], ls)
	}
}

// Every listing of a claim's scopes renders through one function (D-066), so
// every listing must tell a scope holding ", " from two scopes. The pair is
// `pkg/c.go, pkg/d.go`; the one legacy literal `pkg/c.go, pkg/d.go` must
// render as ONE token, `pkg/c.go,␣pkg/d.go`. Each renderer must show BOTH
// shapes: the pair's is the positive control (the renderer printed the
// scopes at all, and did not fence the whole join as one field), the
// literal's is the fix.
func TestEveryScopeListingTellsOneScopeFromTwo(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	const lit, pair, litShown = "pkg/c.go, pkg/d.go", "pkg/c.go, pkg/d.go", "pkg/c.go,␣pkg/d.go"
	if _, errw, code := f.run(t, f.repo, "", "claim", "two", "--session", "sess-a", "--desc", "x",
		"--scope", "pkg/c.go", "--scope", "pkg/d.go"); code != 0 {
		t.Fatal(errw)
	}
	f.legacyClaim(t, "sess-a", "one", lit)
	// The gate's SHARED arm is a second deny sentence: a shared pair and a
	// shared legacy literal, on paths of their own.
	const litS, pairS, litSShown = "pkg/e.go, pkg/f.go", "pkg/e.go, pkg/f.go", "pkg/e.go,␣pkg/f.go"
	if _, errw, code := f.run(t, f.repo, "", "claim", "twos", "--session", "sess-a", "--desc", "x", "--shared",
		"--scope", "pkg/e.go", "--scope", "pkg/f.go"); code != 0 {
		t.Fatal(errw)
	}
	f.legacyClaimMode(t, "sess-a", "ones", true, litS)

	run := func(cwd, stdin string, args ...string) string {
		out, errw, _ := f.run(t, cwd, stdin, args...)
		return out + errw
	}
	gate := func(rel string) string {
		reason, denied := decodeDeny(t, run(f.wtB, hookJSON("sess-b", f.wtB, "Edit", filepath.Join(f.wtB, rel)), "gate"))
		if !denied {
			t.Fatalf("gate: an edit of %q under sess-a's claim must be denied", rel)
		}
		return reason
	}
	f.stage(t, f.wtB, "pkg/c.go", lit)
	var commitGate string
	f.asSession("sess-b", func() { commitGate = run(f.wtB, "", "commit-gate") })
	ls := run(f.repo, "", "ls")
	who := run(f.repo, "", "who", "alpha")
	status := run(f.repo, "", "status", "--session", "sess-a")
	hello := run(f.repo, hookJSON("sess-c", f.repo, "", ""), "hello", "--label", "charlie")
	scopesLine := func(l string) string { return "scopes: " + l }
	inScope := func(l string) string { return `inside scope "` + l + `"` }

	// Each listing is asserted on the output that lists THAT claim, and the
	// list is matched in the listing's own context: the gate's deny, `whose`
	// and the commit gate also print the PATH, and the path of the literal is
	// byte-identical to the pair's rendering, so a bare substring check would
	// pass a broken list on the strength of the path (Codex code pass).
	for _, l := range []struct {
		name            string
		pairOut, litOut string
		pair, shown     string
		wrap            func(string) string
	}{
		{"ls", ls, ls, pair, litShown, func(l string) string { return "  " + l + " — " }},
		{"whose", run(f.repo, "", "whose", "pkg/c.go"), run(f.repo, "", "whose", lit), pair, litShown,
			func(l string) string { return "scopes: " + l + "\n" }},
		{"who", who, who, pair, litShown, scopesLine},
		{"status", status, status, pair, litShown, scopesLine},
		{"hello", hello, hello, pair, litShown, scopesLine},
		{"gate", gate("pkg/c.go"), gate(lit), pair, litShown, inScope},
		{"gate SHARED", gate("pkg/e.go"), gate(litS), pairS, litSShown, inScope},
		{"commit-gate", commitGate, commitGate, pair, litShown, func(l string) string { return "scope " + l + " — " }},
		{"release refusal",
			run(f.repo, "", "release", "two", "--session", "sess-a", "--scope", "pkg/zzz.go"),
			run(f.repo, "", "release", "one", "--session", "sess-a", "--scope", "pkg/zzz.go"),
			pair, litShown, func(l string) string { return "it holds: " + l + " (" }},
	} {
		if !strings.Contains(l.pairOut, l.wrap(l.pair)) {
			t.Errorf("%s: control: the two-scope claim must render as %q:\n%s", l.name, l.wrap(l.pair), l.pairOut)
		}
		if !strings.Contains(l.litOut, l.wrap(l.shown)) {
			t.Errorf("%s: the one legacy scope must render as one token, %q:\n%s", l.name, l.wrap(l.shown), l.litOut)
		}
	}
	if !strings.Contains(gate("pkg/e.go"), "claimed SHARED") {
		t.Error("gate SHARED: control: the shared arm's sentence must be the one under test")
	}
}

// The listings a caller's OWN scopes reach — the claim result line, the dry
// run's forecast, a narrowing release, and both arms of the SLOT note — go
// through the same function. A comma can no longer get there through the CLI,
// so a space stands in: one scope with a space is one token, ␣ inside it.
func TestTheCallersOwnScopeListingsShowOneTokenPerScope(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	const spaced, spacedShown = "pkg/g h.go", "pkg/g␣h.go"
	claim := func(cwd string, args ...string) (string, string, int) {
		return f.run(t, cwd, "", append([]string{"claim"}, args...)...)
	}
	out, _, code := claim(f.repo, "sp", "--session", "sess-a", "--desc", "x", "--scope", spaced, "--scope", "pkg/i.go", "--dry-run")
	if code != 0 || !strings.Contains(out, "would claim: "+spacedShown+", pkg/i.go") {
		t.Fatalf("dry run: %d %q", code, out)
	}
	if out, _, code = claim(f.repo, "sp", "--session", "sess-a", "--desc", "x", "--scope", spaced, "--scope", "pkg/i.go"); code != 0 ||
		!strings.Contains(out, "scopes: "+spacedShown+", pkg/i.go") {
		t.Fatalf("claim: %d %q", code, out)
	}
	if out, _, code = f.run(t, f.repo, "", "release", "sp", "--session", "sess-a", "--scope", "pkg/i.go"); code != 0 ||
		!strings.Contains(out, "still held: "+spacedShown) {
		t.Fatalf("release narrowing: %d %q", code, out)
	}
	if out, _, code = f.run(t, f.repo, "", "release", "sp", "--session", "sess-a", "--scope", spaced); code != 0 ||
		!strings.Contains(out, "released "+spacedShown+" from") {
		t.Fatalf("release of the last scope: %d %q", code, out)
	}

	// SLOT, one and several: the refused claim's note names the slots.
	const slot, slotShown = ".buddy/slot/a b", ".buddy/slot/a␣b"
	if _, errw, code := claim(f.repo, "box", "--session", "sess-a", "--desc", "x", "--scope", slot, "--scope", ".buddy/slot/c"); code != 0 {
		t.Fatal(errw)
	}
	out, _, _ = claim(f.wtB, "mine", "--session", "sess-b", "--desc", "x", "--scope", slot)
	if !strings.Contains(out, "SLOT: "+slotShown+" is a shared resource") {
		t.Fatalf("one slot: %q", out)
	}
	out, _, _ = claim(f.wtB, "mine", "--session", "sess-b", "--desc", "x", "--scope", slot, "--scope", ".buddy/slot/c")
	if !strings.Contains(out, "SLOT: "+slotShown+", .buddy/slot/c are shared resources") {
		t.Fatalf("two slots: %q", out)
	}
}

// One scope longer than the cap shows, cut by its own fence, rather than as a
// count: whole items only, applied to the FIRST item, printed `scopes: ...and
// 1 more not shown` and a gate deny `inside scope "...and 1 more not shown"`.
func TestAScopeLongerThanTheCapShowsCutNotCounted(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	long := "pkg/" + strings.Repeat("a/", 300) + "z.go" // 608 bytes
	if _, errw, code := f.run(t, f.repo, "", "claim", "wide", "--session", "sess-a", "--desc", "x",
		"--scope", long, "--scope", "pkg/next.go"); code != 0 {
		t.Fatal(errw)
	}
	prefix := long[:400]
	if err := os.MkdirAll(filepath.Join(f.wtB, filepath.Dir(long)), 0o755); err != nil {
		t.Fatal(err)
	}
	reason, denied := decodeDeny(t, func() string {
		out, _, _ := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Edit", filepath.Join(f.wtB, long)), "gate")
		return out
	}())
	if !denied {
		t.Fatal("control: an edit of the long scope must be denied")
	}
	status, _, _ := f.run(t, f.repo, "", "status", "--session", "sess-a")
	ls, _, _ := f.run(t, f.repo, "", "ls")
	for name, out := range map[string]string{"gate": reason, "status": status, "ls": ls} {
		if !strings.Contains(out, prefix) || !strings.Contains(out, "…[truncated], ...and 1 more not shown") {
			t.Errorf("%s: the long scope must show cut, then the rest counted:\n%s", name, out)
		}
		if strings.Contains(out, long[:513]) {
			t.Errorf("%s: the long scope must be cut at its 512-byte cap:\n%s", name, out)
		}
	}
}

// The check is in the CLI and not in NormalizeScope (D-066) so that a comma
// scope a ledger already holds can still be narrowed and released by name —
// exactly, the remaining scopes as they were, the claim open until its last
// scope goes. And a comma `release --scope` against a real two-scope claim is
// refused, naming what is held, rather than releasing either scope.
func TestALegacyCommaScopeIsStillReleasedAndNarrowedExactly(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.legacyClaim(t, "sess-a", "old", "pkg/a.go,pkg/b.go", "pkg/x.go")
	if w, _, _ := f.run(t, f.repo, "", "whose", "pkg/a.go,pkg/b.go"); !strings.Contains(w, "old") {
		t.Fatalf("control: the legacy literal must be held: %q", w)
	}
	out, errw, code := f.run(t, f.repo, "", "release", "old", "--session", "sess-a", "--scope", "pkg/x.go")
	if code != 0 || !strings.HasSuffix(strings.TrimSpace(out), "still held: pkg/a.go,pkg/b.go") {
		t.Fatalf("narrowing around a legacy scope: %d %q %q", code, out, errw)
	}
	if ls, _, _ := f.run(t, f.repo, "", "ls"); !strings.Contains(ls, "open") || !strings.Contains(ls, " pkg/a.go,pkg/b.go — legacy") {
		t.Fatalf("the claim must stay open holding exactly the literal: %q", ls)
	}
	out, errw, code = f.run(t, f.repo, "", "release", "old", "--session", "sess-a", "--scope", "pkg/a.go,pkg/b.go")
	if code != 0 || !strings.Contains(out, "that was its last scope, so the claim is released") {
		t.Fatalf("releasing the legacy literal by name: %d %q %q", code, out, errw)
	}
	if ls, _, _ := f.run(t, f.repo, "", "ls"); strings.Contains(ls, "old") {
		t.Fatalf("the released claim is still listed open: %q", ls)
	}

	if _, errw, code := f.run(t, f.repo, "", "claim", "real", "--session", "sess-a", "--desc", "x",
		"--scope", "pkg/a.go", "--scope", "pkg/b.go"); code != 0 {
		t.Fatal(errw)
	}
	_, errw, code = f.run(t, f.repo, "", "release", "real", "--session", "sess-a", "--scope", "pkg/a.go,pkg/b.go")
	if code == 0 || !strings.Contains(errw, "it holds: pkg/a.go, pkg/b.go") {
		t.Fatalf("a comma release --scope must be refused naming what is held: %d %q", code, errw)
	}
	if ls, _, _ := f.run(t, f.repo, "", "ls"); !strings.Contains(ls, "pkg/a.go, pkg/b.go — x") {
		t.Fatalf("a refused release must narrow nothing: %q", ls)
	}
}
