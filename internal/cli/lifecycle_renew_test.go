package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// F-26, Codex's stricter point 1: `lifecycle beat --path` was a plain string
// flag, so `--path a --path b` kept only b and a worker patching two claimed
// files every few minutes watched the first claim go STALE. Each claim here
// covers exactly one of the two paths, so a beat that dropped either one leaves
// that claim's renewal behind the clock.
func TestLifecycleBeatRenewsEveryRepeatedPath(t *testing.T) {
	boundedParallel(t)
	f, current := lifecycleFixture(t)
	for _, c := range [][2]string{{"a-work", "a"}, {"b-work", "b"}, {"c-control", "c"}} {
		if _, errw, code := f.run(t, f.repo, "", "claim", c[0], "--session", current.SessionID, "--desc", "work", "--scope", c[1]); code != 0 {
			t.Fatal(errw)
		}
	}
	claimed := f.clock
	f.clock = f.clock.Add(time.Minute)
	lifecycleOK(t, f, "beat", "--session", current.SessionID, "--incarnation", current.Incarnation, "--path", "a/x.go", "--path", "b/y.go")
	st := lifecycleStore(t, f)
	renewed := map[string]time.Time{}
	for _, c := range lifecycleSnapshot(t, st, current.SessionID).Claims {
		renewed[c.Slug] = c.Renewed
	}
	if !renewed["a-work"].Equal(f.clock) || !renewed["b-work"].Equal(f.clock) {
		t.Fatalf("a beat naming a and b must renew both covering claims: %v (now %v)", renewed, f.clock)
	}
	// The control: a claim covering neither observed path does not move.
	if !renewed["c-control"].Equal(claimed) {
		t.Fatalf("a claim covering no observed path was renewed: %v", renewed)
	}
}

type renewReceiptForTest struct {
	lifecycleReceiptForTest
	RenewedClaims *int     `json:"renewed_claims"`
	PlacedPaths   []string `json:"placed_paths"`
}

func lifecycleRenew(t *testing.T, f *fixture, cwd string, current lifecycleReceiptForTest, paths ...string) renewReceiptForTest {
	t.Helper()
	args := []string{"lifecycle", "renew", "--session", current.SessionID, "--incarnation", current.Incarnation}
	for _, p := range paths {
		args = append(args, "--path", p)
	}
	out, errw, code := f.run(t, cwd, "", args...)
	var got renewReceiptForTest
	if code != 0 || errw != "" || json.Unmarshal([]byte(out), &got) != nil || got.Event != "renew" || got.RenewedClaims == nil ||
		got.SessionID != current.SessionID || got.Incarnation != current.Incarnation || got.State != "live" {
		t.Fatalf("lifecycle renew %v: exit=%d stdout=%q stderr=%q", paths, code, out, errw)
	}
	return got
}

// A renewal is the adapter's completed-write observation (D-078): it renews the
// captured incarnation's claims covering the paths a native completion named,
// placed the way the gate adjudicates a write (every spelling it can land on),
// and it is NOT a lifecycle event: idle stays recorded and last_seen does not
// move. Each claim below is the target of exactly one case; the control claim
// covers no path any case names, and a path outside the repo renews nothing.
func TestLifecycleRenewPlacesNativeSpellingsAndTouchesOnlyClaims(t *testing.T) {
	boundedParallel(t)
	f, current := lifecycleFixture(t)
	repo := canon(f.repo)
	if err := os.MkdirAll(filepath.Join(repo, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "real", "target.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real/target.txt", filepath.Join(repo, "link.txt")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(repo), "outside")
	for _, c := range [][2]string{{"abs", "abs"}, {"rel", "rel"}, {"entry", "link.txt"}, {"target", "real/target.txt"}, {"control", "control"}, {"outside", "outside"}} {
		if _, errw, code := f.run(t, f.repo, "", "claim", c[0], "--session", current.SessionID, "--desc", "work", "--scope", c[1]); code != 0 {
			t.Fatal(errw)
		}
	}
	claimed := f.clock
	f.clock = f.clock.Add(time.Minute)
	lifecycleOK(t, f, "idle", "--session", current.SessionID, "--incarnation", current.Incarnation)
	st := lifecycleStore(t, f)
	before := lifecycleSnapshot(t, st, current.SessionID)
	if len(before.Idle) != 1 {
		t.Fatalf("idle fixture not armed: %+v", before.Idle)
	}
	f.clock = f.clock.Add(time.Minute)
	got := lifecycleRenew(t, f, f.repo, current,
		filepath.Join(repo, "abs", "a.go"), "rel/b.go", filepath.Join(repo, "link.txt"), filepath.Join(outside, "x.go"))
	if *got.RenewedClaims != 4 {
		t.Fatalf("renewed %d claims, want abs, rel, the link entry and its target: %+v", *got.RenewedClaims, got)
	}
	after := lifecycleSnapshot(t, st, current.SessionID)
	for _, c := range after.Claims {
		want := f.clock
		if c.Slug == "control" || c.Slug == "outside" {
			want = claimed
		}
		if !c.Renewed.Equal(want) {
			t.Fatalf("claim %s renewed at %v, want %v (placed %v)", c.Slug, c.Renewed, want, got.PlacedPaths)
		}
	}
	if len(after.Idle) != 1 || !after.Session.LastSeen.Equal(before.Session.LastSeen) {
		t.Fatalf("a renewal changed lifecycle state: idle %+v, last_seen %v -> %v", after.Idle, before.Session.LastSeen, after.Session.LastSeen)
	}
	// Outside only: a receipt that says so, and nothing renewed.
	f.clock = f.clock.Add(time.Minute)
	if got := lifecycleRenew(t, f, f.repo, current, filepath.Join(outside, "y.go")); *got.RenewedClaims != 0 || len(got.PlacedPaths) != 0 {
		t.Fatalf("a path outside the repo renewed or placed something: %+v", got)
	}
}

// Codex review of the F-26 code pass, finding 5: a path one of whose gate
// spellings could not be resolved (here an unsearchable ancestor, so the
// dangling-link check cannot Lstat it) was renewed by its lexical spelling and
// reported nothing, so the adapter journalled complete coverage. The receipt
// now names such paths in unresolved_paths. The positive control is the same
// call once the directory is searchable again: nothing unresolved.
func TestLifecycleRenewReportsUnresolvedPlacement(t *testing.T) {
	boundedParallel(t)
	f, current := lifecycleFixture(t)
	repo := canon(f.repo)
	locked := filepath.Join(repo, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	type unresolvedReceipt struct {
		Unresolved []string `json:"unresolved_paths"`
	}
	read := func() unresolvedReceipt {
		out, errw, code := f.run(t, f.repo, "", "lifecycle", "renew", "--session", current.SessionID, "--incarnation", current.Incarnation,
			"--path", filepath.Join(locked, "x.go"))
		var got unresolvedReceipt
		if code != 0 || json.Unmarshal([]byte(out), &got) != nil {
			t.Fatalf("renew: exit=%d out=%q err=%q", code, out, errw)
		}
		return got
	}
	if got := read(); len(got.Unresolved) != 1 {
		t.Fatalf("an unresolvable placement was not reported: %+v", got)
	}
	if err := os.Chmod(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := read(); len(got.Unresolved) != 0 {
		t.Fatalf("positive control: a resolvable path reported unresolved: %+v", got)
	}
}

// renew is refused, with no claim touched, without a path, past the patch
// parser's bound, and for any token but the captured live one.
func TestLifecycleRenewRefusals(t *testing.T) {
	boundedParallel(t)
	f, current := lifecycleFixture(t)
	if _, errw, code := f.run(t, f.repo, "", "claim", "work", "--session", current.SessionID, "--desc", "work", "--scope", "pkg"); code != 0 {
		t.Fatal(errw)
	}
	st := lifecycleStore(t, f)
	f.clock = f.clock.Add(time.Minute)
	before := lifecycleSnapshot(t, st, current.SessionID)
	many := []string{"renew", "--session", current.SessionID, "--incarnation", current.Incarnation}
	for i := 0; i <= maxRenewPaths; i++ {
		many = append(many, "--path", "pkg/a.go")
	}
	for _, args := range [][]string{
		{"renew", "--session", current.SessionID, "--incarnation", current.Incarnation},
		// Codex review finding 7: an empty path was a successful renewal of nothing.
		{"renew", "--session", current.SessionID, "--incarnation", current.Incarnation, "--path", ""},
		{"renew", "--session", current.SessionID, "--incarnation", current.Incarnation, "--path", "pkg/a.go", "--path", ""},
		many,
		{"renew", "--session", current.SessionID, "--incarnation", "not-captured", "--path", "pkg/a.go"},
		{"renew", "--session", current.SessionID, "--path", "pkg/a.go"},
	} {
		lifecycleRefused(t, f, args...)
		if got := lifecycleSnapshot(t, st, current.SessionID); !reflect.DeepEqual(got, before) {
			t.Fatalf("refused %v changed the ledger: %+v", args[:3], got)
		}
	}
	// The positive control: the same session and token, with a path, renews.
	if got := lifecycleRenew(t, f, f.repo, current, "pkg/a.go"); *got.RenewedClaims != 1 {
		t.Fatalf("renew positive control: %+v", got)
	}
	lifecycleOK(t, f, "end", "--session", current.SessionID, "--incarnation", current.Incarnation)
	f.clock = f.clock.Add(time.Minute)
	ended := lifecycleSnapshot(t, st, current.SessionID)
	lifecycleRefused(t, f, "renew", "--session", current.SessionID, "--incarnation", current.Incarnation, "--path", "pkg/a.go")
	if got := lifecycleSnapshot(t, st, current.SessionID); !reflect.DeepEqual(got, ended) {
		t.Fatalf("a renewal after end changed the ledger: %+v", got)
	}
}
