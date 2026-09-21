package cli

import (
	"strings"
	"testing"
)

// The id register's verb surface (D-029): every refusal named, every answer
// fenced, and status never claiming to know the artifact.
func TestIDsSeedTakeListAndStatus(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	// Nothing seeded: take refuses and names the remedy; ls says so.
	_, errw, code := f.run(t, f.repo, "", "ids", "take", "record", "5", "--session", "sess-a")
	if code == 0 || !strings.Contains(errw, "seed it first") {
		t.Fatalf("take from an unseeded space: %d %q", code, errw)
	}
	if out, _, code := f.run(t, f.repo, "", "ids", "ls"); code != 0 || !strings.Contains(out, "no id spaces seeded") {
		t.Fatalf("ls with nothing seeded:\n%s", out)
	}
	out, errw, code := f.run(t, f.repo, "", "ids", "seed", "record", "1704")
	if code != 0 || !strings.Contains(out, "space record seeded: ceiling 1704 — next block starts at 1705") {
		t.Fatalf("seed: %d %s %s", code, out, errw)
	}
	out, errw, code = f.run(t, f.repo, "", "ids", "take", "record", "5", "--session", "sess-a", "--note", "cve rows")
	if code != 0 || strings.TrimSpace(out) != "took record 1705..1709 for alpha" {
		t.Fatalf("take: %d %q %s", code, out, errw)
	}
	if _, _, code := f.run(t, f.wtB, "", "ids", "take", "record", "2", "--session", "sess-b"); code != 0 {
		t.Fatal("second take")
	}
	out, _, _ = f.run(t, f.repo, "", "ids", "ls")
	for _, want := range []string{
		"record  ceiling 1711  (next block starts at 1712)",
		"1705..1709     alpha                    taken 0s   cve rows",
		"1710..1711     bravo                    taken 0s",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("ls missing %q:\n%s", want, out)
		}
	}
	// Status, three registers.
	for _, tc := range []struct{ n, want string }{
		{"1706", "record 1706: RESERVED here by alpha (block 1705..1709, taken 0s ago) — cve rows"},
		{"1712", "record 1712: above the ceiling (1711) — UNRESERVED IN THIS REGISTER, which is not \"free\""},
		{"1700", "record 1700: at or below the ceiling (1711) and in no block — NOT AVAILABLE for allocation"},
	} {
		out, _, code := f.run(t, f.repo, "", "ids", "status", "record", tc.n)
		if code != 0 || !strings.Contains(out, tc.want) {
			t.Fatalf("status %s: %d\n%s", tc.n, code, out)
		}
	}
	// Refusals: lowering, negative, junk, unknown space, no return verb.
	if _, errw, code := f.run(t, f.repo, "", "ids", "seed", "record", "1000"); code == 0 || !strings.Contains(errw, "never lowered") {
		t.Fatalf("lower: %d %q", code, errw)
	}
	for _, bad := range [][]string{{"seed", "record", "-1"}, {"take", "record", "1e3"}, {"status", "nope", "1"}, {"return", "record", "1705"}} {
		if _, _, code := f.run(t, f.repo, "", append([]string{"ids"}, bad...)...); code == 0 {
			t.Fatalf("%v must refuse", bad)
		}
	}
	// A note and a space name are peer text: one line each.
	if _, _, code := f.run(t, f.repo, "", "ids", "seed", "sp ace\nfake", "1"); code != 0 {
		t.Fatal("seed odd space")
	}
	if _, _, code := f.run(t, f.repo, "", "ids", "take", "sp ace\nfake", "1", "--session", "sess-a", "--note", "line\nbreak"); code != 0 {
		t.Fatal("take odd")
	}
	out, _, _ = f.run(t, f.repo, "", "ids", "ls", "sp ace\nfake")
	if !strings.Contains(out, "sp␣ace⏎fake  ceiling 2") || !strings.Contains(out, "line⏎break") || strings.Count(out, "\n") != 2 {
		t.Fatalf("fencing:\n%q", out)
	}
}

// Pass-B findings (Codex code pass, D-029): trailing arguments on take are
// refused rather than allocating to the wrong session; a space named to look
// like a notice cannot open a line as one; the top of the number line does
// not wrap.
func TestIDsRefusesTrailingArgumentsAndOddNames(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, _, code := f.run(t, f.repo, "", "ids", "seed", "record", "0"); code != 0 {
		t.Fatal("seed")
	}
	// `take record 5 junk --session sess-b`: Go's parser stops at junk, so
	// --session would never be read and the block would go to whoever the
	// environment names. Refused; nothing allocated.
	_, errw, code := f.run(t, f.repo, "", "ids", "take", "record", "5", "junk", "--session", "sess-b")
	if code == 0 || !strings.Contains(errw, "unexpected argument \"junk\"") {
		t.Fatalf("trailing argument: %d %q", code, errw)
	}
	if out, _, _ := f.run(t, f.repo, "", "ids", "ls", "record"); !strings.Contains(out, "ceiling 0") || strings.Contains(out, "1..5") {
		t.Fatalf("nothing may have been allocated:\n%s", out)
	}
	// A space named "BUDDY:" never starts a line.
	if _, _, code := f.run(t, f.repo, "", "ids", "seed", "BUDDY:", "1"); code != 0 {
		t.Fatal("seed odd")
	}
	out, _, _ := f.run(t, f.repo, "", "ids", "ls")
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "BUDDY:") {
			t.Fatalf("a space name opened a line as a notice:\n%s", out)
		}
	}
	if !strings.Contains(out, "space BUDDY:  ceiling 1") {
		t.Fatalf("the space still lists:\n%s", out)
	}
	// The top of the number line.
	out, _, code = f.run(t, f.repo, "", "ids", "seed", "top", "9223372036854775807")
	if code != 0 || !strings.Contains(out, "exhausted") || strings.Contains(out, "-9223372036854775808") {
		t.Fatalf("seed at MaxInt64: %d %s", code, out)
	}
	if out, _, _ := f.run(t, f.repo, "", "ids", "ls", "top"); !strings.Contains(out, "exhausted") {
		t.Fatalf("ls at MaxInt64:\n%s", out)
	}
	if _, _, code := f.run(t, f.repo, "", "ids", "take", "top", "1", "--session", "sess-a"); code == 0 {
		t.Fatal("take above MaxInt64 must refuse")
	}
}
