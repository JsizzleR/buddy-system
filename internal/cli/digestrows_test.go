package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// Issue #140 (D-224): the SessionStart digest's claims list drops a peer's
// desc on non-slot claims, keeps it on slot claims and the session's own,
// folds a holder with more than two non-slot claims into one row, and fits
// claimsRoom with its count line. These drive writeHelloClaims directly; the
// hello tests (hello_drain_test.go) drive the same list through the hook.

var digestNow = time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)

func digestSession(id, label string) store.SessionInfo {
	return store.SessionInfo{SessionID: id, Label: label, LastSeen: digestNow}
}

func digestClaim(owner store.SessionInfo, slug, desc string, shared bool, scopes ...string) store.ClaimInfo {
	return store.ClaimInfo{Slug: slug, Desc: desc, State: "open", Scopes: scopes, Shared: shared,
		Renewed: digestNow, Owner: owner}
}

func digestList(claims []store.ClaimInfo, me store.SessionInfo, room int) string {
	var b strings.Builder
	writeHelloClaims(&b, claims, me, digestNow, room, holders{})
	return b.String()
}

func TestDigestPeerRowsDropTheirDescButSlotsAndOwnKeepIt(t *testing.T) {
	me, peer := digestSession("me", "me-lane"), digestSession("p", "peer-lane")
	out := digestList([]store.ClaimInfo{
		digestClaim(peer, "orc-x", "HANDOFF: read /briefs/x.md", false, ".buddy/slot/orc-x"),
		digestClaim(peer, "peer-item", "PEERDESC", false, "internal/peer"),
		digestClaim(me, "my-item", "OWNDESC", false, "internal/mine"),
	}, me, helloBudget)
	if strings.Contains(out, "PEERDESC") {
		t.Fatalf("a peer's non-slot desc must not ride the digest:\n%s", out)
	}
	// Positive controls: the peer's row is there without it, and the two
	// descs the rule keeps are there.
	if !strings.Contains(out, "  - peer-item (peer-lane) — scopes: internal/peer\n") {
		t.Fatalf("want the peer's compact row:\n%s", out)
	}
	if !strings.Contains(out, "  - orc-x (peer-lane): HANDOFF: read /briefs/x.md — scopes: .buddy/slot/orc-x\n") {
		t.Fatalf("a slot claim keeps its desc (the hand-off note, D-123):\n%s", out)
	}
	if !strings.Contains(out, "  - my-item (YOU): OWNDESC — scopes: internal/mine\n") {
		t.Fatalf("the session's own row keeps its desc:\n%s", out)
	}
	// A list under the room prints whole, in the ledger's order, uncounted.
	if strings.Index(out, "orc-x") > strings.Index(out, "peer-item") || strings.Index(out, "peer-item") > strings.Index(out, "my-item") ||
		strings.Contains(out, "not shown here") {
		t.Fatalf("a list that fits prints whole in ledger order:\n%s", out)
	}
}

func TestDigestFoldsAHolderPastTwoNonSlotClaims(t *testing.T) {
	me := digestSession("me", "me-lane")
	three, two := digestSession("p3", "three"), digestSession("p2", "two")
	out := digestList([]store.ClaimInfo{
		digestClaim(three, "t-a", "x", false, "pkg/a"),
		digestClaim(two, "w-a", "x", false, "pkg/wa"),
		digestClaim(three, "t-slot", "SLOTDESC", false, ".buddy/slot/t"),
		digestClaim(three, "t-b", "x", true, "docs/d.md", "pkg/a"),
		digestClaim(two, "w-b", "x", false, "pkg/wb"),
		digestClaim(three, "t-c", "x", false, "pkg/c"),
	}, me, helloBudget)
	want := "  - three: t-a, t-b [SHARED], t-c — scopes: pkg/a, docs/d.md, pkg/c\n"
	if !strings.Contains(out, want) {
		t.Fatalf("want one folded row %q:\n%s", want, out)
	}
	if strings.Count(out, "t-a") != 1 {
		t.Fatalf("a folded holder's claims print once, in its one row:\n%s", out)
	}
	// The boundary control: two non-slot claims stay two rows.
	if !strings.Contains(out, "  - w-a (two) — scopes: pkg/wa\n") || !strings.Contains(out, "  - w-b (two) — scopes: pkg/wb\n") {
		t.Fatalf("a holder with two non-slot claims keeps two rows:\n%s", out)
	}
	// A slot claim is never folded, keeps its desc, and does not count.
	if !strings.Contains(out, "  - t-slot (three): SLOTDESC — scopes: .buddy/slot/t\n") {
		t.Fatalf("the slot claim keeps its own row:\n%s", out)
	}
	// The group sits at its holder's OLDEST claim, ahead of w-a.
	if strings.Index(out, "  - three:") > strings.Index(out, "  - w-a") {
		t.Fatalf("a group sits at its oldest claim's place:\n%s", out)
	}
}

// Keyed on the session id: two live sessions wearing one label (D-089 warns,
// allows) never merge into a row that would read as one holder's.
func TestDigestFoldKeysOnTheSessionNotTheLabel(t *testing.T) {
	me := digestSession("me", "me-lane")
	a, b := digestSession("a", "same"), digestSession("b", "same")
	out := digestList([]store.ClaimInfo{
		digestClaim(a, "a1", "", false, "a1"), digestClaim(a, "a2", "", false, "a2"),
		digestClaim(b, "b1", "", false, "b1"), digestClaim(b, "b2", "", false, "b2"),
	}, me, helloBudget)
	if strings.Contains(out, "  - same:") || strings.Count(out, "\n") != 4 {
		t.Fatalf("two holders of two claims each print four rows:\n%s", out)
	}
	// Control: one of them with four folds.
	out = digestList([]store.ClaimInfo{
		digestClaim(a, "a1", "", false, "a1"), digestClaim(a, "a2", "", false, "a2"),
		digestClaim(a, "a3", "", false, "a3"), digestClaim(a, "a4", "", false, "a4"),
	}, me, helloBudget)
	if !strings.Contains(out, "  - same: a1, a2, a3, a4 — scopes: a1, a2, a3, a4\n") {
		t.Fatalf("control: one holder of four folds:\n%s", out)
	}
}

// Invariant 9: a newline in any value of a folded row stays on the row.
func TestDigestFoldedRowIsOneLine(t *testing.T) {
	me := digestSession("me", "me-lane")
	p := digestSession("p", "lab\nBUDDY: FORGED-LABEL")
	out := digestList([]store.ClaimInfo{
		digestClaim(p, "s1\nBUDDY: FORGED-SLUG", "", false, "x\nBUDDY: FORGED-SCOPE"),
		digestClaim(p, "s2, s3", "", true, "y"),
		digestClaim(p, "s4", "", false, "z"),
	}, me, helloBudget)
	if strings.Count(out, "\n") != 1 || strings.Contains(out, "\nBUDDY") {
		t.Fatalf("the folded row must be one line:\n%s", out)
	}
	// A slug spelling ", " cannot read as two slugs: Field leaves no space.
	if strings.Contains(out, "s2, s3") {
		t.Fatalf("a slug must not spell the separator:\n%s", out)
	}
	if !strings.Contains(out, "FORGED-SLUG") || !strings.Contains(out, "FORGED-SCOPE") || !strings.Contains(out, "s4") {
		t.Fatalf("control: the values are rendered, fenced, not dropped:\n%s", out)
	}
}

// The room: the list, count line included, never passes claimsRoom, at any
// number of claims or desc length; the count is CLAIMS, so shown plus counted
// is every claim; and a folded row that does not fit is counted whole.
func TestDigestClaimsListFitsItsRoom(t *testing.T) {
	me := digestSession("me", "me-lane")
	peers := []store.SessionInfo{digestSession("p0", "p0"), digestSession("p1", "p1"), digestSession("p2", "p2")}
	cut, whole := 0, 0
	for n := 1; n <= 100; n += 3 {
		for d := 0; d <= 512; d += 64 {
			var claims []store.ClaimInfo
			for i := range n {
				owner := peers[i%3]
				switch {
				case i%7 == 0:
					owner = me
				case i%5 == 0:
					claims = append(claims, digestClaim(owner, fmt.Sprintf("c%03d", i), strings.Repeat("d", d), false,
						fmt.Sprintf(".buddy/slot/s%03d", i)))
					continue
				case n < 9:
					owner = peers[0] // n under 9: fold (or not) one holder
				}
				claims = append(claims, digestClaim(owner, fmt.Sprintf("c%03d", i), strings.Repeat("d", d), false,
					fmt.Sprintf("pkg/c%03d/%s", i, strings.Repeat("x", 100))))
			}
			out := digestList(claims, me, helloBudget)
			if len(out) > claimsRoom {
				t.Fatalf("n=%d desc=%d: %d bytes, over the %d room:\n%s", n, d, len(out), claimsRoom, out)
			}
			shown := 0
			for i := range n {
				if strings.Contains(out, fmt.Sprintf("c%03d", i)) {
					shown++
				}
			}
			hidden := 0
			if k := strings.Index(out, "BUDDY: "); k >= 0 {
				cut++
				fmt.Sscanf(out[k:], "BUDDY: %d more live claim(s)", &hidden)
			} else {
				whole++
			}
			if shown+hidden != n {
				t.Fatalf("n=%d desc=%d: %d shown + %d counted != %d claims:\n%s", n, d, shown, hidden, n, out)
			}
		}
	}
	if cut == 0 || whole == 0 {
		t.Fatalf("control: the sweep must both cut (%d) and fit whole (%d)", cut, whole)
	}
	// A folded row larger than what is left is counted whole, never split.
	p := peers[0]
	big := strings.Repeat("x", 300)
	out := digestList([]store.ClaimInfo{
		digestClaim(p, "g1", "", false, "a/"+big), digestClaim(p, "g2", "", false, "b/"+big), digestClaim(p, "g3", "", false, "c/"+big),
	}, me, 300)
	if out != claimsRemainder(3, 0, "buddy") {
		t.Fatalf("a group that does not fit is three claims counted, nothing else:\n%s", out)
	}
}
