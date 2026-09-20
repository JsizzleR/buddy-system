package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// Wishlist §5b and §5d, from a 14-session run: a claim naming one busy path
// was refused whole and named one conflict (a round trip per collision); a
// holder that had finished with two of four scopes could only say so in its
// description, which the gate does not read; and a peer's reply to a message
// signed with a slug bounced, because the slug belonged to a claim that had
// been REFUSED and so never opened.

func TestClaimDryRunReportsTheConflictSetAndTakesNothing(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "claim", "router", "--session", "sess-a", "--desc", "r", "--scope", "internal/router", "--scope", "docs"); code != 0 {
		t.Fatal(errw)
	}
	before, _, _ := f.run(t, f.repo, "", "ls")

	out, _, code := f.run(t, f.wtB, "", "claim", "mine", "--session", "sess-b", "--desc", "m", "--dry-run",
		"--scope", "internal/router/proxy.go", "--scope", "docs/a.md", "--scope", "pkg")
	if code == 0 {
		t.Fatalf("a dry run with conflicts must exit non-zero so nothing passes silently: %q", out)
	}
	for _, want := range []string{"REFUSED", "internal/router/proxy.go", "docs/a.md", "alpha", "router", "pkg"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run output must name %q: %q", want, out)
		}
	}
	if strings.Count(out, "REFUSED") != 2 {
		t.Fatalf("one REFUSED line per conflict: %q", out)
	}
	after, _, _ := f.run(t, f.repo, "", "ls")
	if after != before {
		t.Fatalf("dry run wrote to the board:\n%s\n%s", before, after)
	}

	// No conflicts: exit 0, says what it WOULD claim, still writes nothing.
	out, _, code = f.run(t, f.wtB, "", "claim", "mine", "--session", "sess-b", "--desc", "m", "--dry-run", "--scope", "pkg")
	if code != 0 || !strings.Contains(out, "would claim") || !strings.Contains(out, "pkg") {
		t.Fatalf("clean dry run: code %d, %q", code, out)
	}
	if after, _, _ := f.run(t, f.repo, "", "ls"); after != before {
		t.Fatalf("clean dry run wrote to the board:\n%s\n%s", before, after)
	}
}

func TestClaimRefusalListsEveryConflict(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "claim", "router", "--session", "sess-a", "--desc", "r", "--scope", "internal/router", "--scope", "docs"); code != 0 {
		t.Fatal(errw)
	}
	out, errw, code := f.run(t, f.wtB, "", "claim", "mine", "--session", "sess-b", "--desc", "m",
		"--scope", "internal/router/proxy.go", "--scope", "docs/a.md", "--scope", "pkg")
	if code == 0 {
		t.Fatal("must refuse")
	}
	both := out + errw
	if !strings.Contains(both, "internal/router/proxy.go") || !strings.Contains(both, "docs/a.md") {
		t.Fatalf("a refusal must name every conflict, not the first: %q", both)
	}
	if _, _, code := f.run(t, f.wtB, "", "claim", "mine", "--session", "sess-b", "--desc", "m", "--scope", "pkg"); code != 0 {
		t.Fatal("positive control: the uncontended path is claimable on its own")
	}
}

func TestReleaseScopeNarrowsAndTheGateFollows(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "claim", "w", "--session", "sess-a", "--desc", "d", "--scope", "pkg", "--scope", "docs"); code != 0 {
		t.Fatal(errw)
	}
	docs := filepath.Join(f.wtB, "docs/x.md")
	out, _, _ := f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Edit", docs), "gate")
	if _, denied := decodeDeny(t, out); !denied {
		t.Fatalf("control: docs must be denied before the partial release: %q", out)
	}

	out, errw, code := f.run(t, f.repo, "", "release", "w", "--session", "sess-a", "--scope", "docs")
	if code != 0 {
		t.Fatalf("partial release: %s %s", out, errw)
	}
	if !strings.Contains(out, "docs") || !strings.Contains(out, "pkg") {
		t.Fatalf("output should say what was released and what is still held: %q", out)
	}
	out, _, code = f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Edit", docs), "gate")
	if code != 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("released scope must pass the gate silently: %q", out)
	}
	out, _, _ = f.run(t, f.wtB, hookJSON("sess-b", f.wtB, "Edit", filepath.Join(f.wtB, "pkg/y.go")), "gate")
	if _, denied := decodeDeny(t, out); !denied {
		t.Fatalf("kept scope must still be denied: %q", out)
	}
	ls, _, _ := f.run(t, f.repo, "", "ls")
	if !strings.Contains(ls, "pkg") || strings.Contains(ls, "docs") {
		t.Fatalf("board must show only the kept scope: %q", ls)
	}

	// A scope the claim does not hold is refused, and the refusal lists what is.
	out, errw, code = f.run(t, f.repo, "", "release", "w", "--session", "sess-a", "--scope", "pkg/sub")
	if code == 0 || !strings.Contains(out+errw, "pkg") {
		t.Fatalf("unheld scope: code %d %q %q", code, out, errw)
	}
	// Releasing the last scope releases the claim.
	if _, errw, code := f.run(t, f.repo, "", "release", "w", "--session", "sess-a", "--scope", "pkg"); code != 0 {
		t.Fatal(errw)
	}
	if ls, _, _ := f.run(t, f.repo, "", "ls"); !strings.Contains(ls, "no claims") {
		t.Fatalf("releasing the last scope must release the claim: %q", ls)
	}
}

// A message signed with free text is unanswerable when that text resolves to
// nothing. The sender's LABEL always resolves (it is a pause/msg target by
// construction), so it is the default sender, and an explicit --from that is
// not the label is stamped with it.
func TestMsgSenderIsResolvableByTheRecipient(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)

	f.asSession("sess-a", func() {
		// Default sender: the calling session's label.
		if _, errw, code := f.run(t, f.repo, "", "msg", "bravo", "one"); code != 0 {
			t.Fatal(errw)
		}
		// Explicit --from that is not the label: stamped with the label.
		if _, errw, code := f.run(t, f.repo, "", "msg", "bravo", "--from", "refused-slug", "two"); code != 0 {
			t.Fatal(errw)
		}
		// Explicit --from that IS the label: not doubled.
		if _, errw, code := f.run(t, f.repo, "", "msg", "bravo", "--from", "alpha", "three"); code != 0 {
			t.Fatal(errw)
		}
	})
	// No session in the environment: the operator, as before.
	if _, errw, code := f.run(t, f.repo, "", "msg", "bravo", "four"); code != 0 {
		t.Fatal(errw)
	}
	out, _, _ := f.run(t, f.wtB, "", "inbox", "--session", "sess-b")
	for _, want := range []string{"[alpha] one", "[alpha (refused-slug)] two", "[alpha] three", "[operator] four"} {
		if !strings.Contains(out, want) {
			t.Fatalf("inbox should read %q:\n%s", want, out)
		}
	}
	// And the stamp is a target the recipient can answer to — recovered from
	// the RENDERED line, not typed from knowledge of the fixture: the label is
	// everything between "[" and the first " (" or "]".
	var addr string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "] two") {
			addr = strings.TrimPrefix(line[:strings.Index(line, "]")], "[")
			if i := strings.Index(addr, " ("); i >= 0 {
				addr = addr[:i]
			}
		}
	}
	if addr == "" {
		t.Fatalf("could not recover an address from %q", out)
	}
	f.asSession("sess-b", func() {
		if out, errw, code := f.run(t, f.wtB, "", "msg", addr, "reply"); code != 0 {
			t.Fatalf("reply to the recovered address %q: %s %s", addr, out, errw)
		}
	})
	f.asSession("sess-a", func() {
		if out, _, _ := f.run(t, f.repo, "", "inbox"); !strings.Contains(out, "[bravo] reply") {
			t.Fatalf("the reply must reach the original sender: %q", out)
		}
	})
}
