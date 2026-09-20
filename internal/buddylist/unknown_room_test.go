package buddylist

import (
	"strings"
	"testing"
)

// An unknown room must be REFUSED, not answered empty.
//
// THE FAILURE (2026-09-07): sessions were told to read a room called `lobby`,
// which does not exist. Journal.Read is `WHERE room=? AND seq>?`, so a wrong
// name returns zero rows and renders as `(no messages)` — byte-identical to a
// room nobody is talking in. A session reported "the room is empty, all traffic
// goes through the message hook instead" while its actual project room held
// thousands of messages.
//
// The name in the SessionStart digest is DERIVED from the session's label, and
// a linked worktree's label names the worktree rather than the checkout — while
// the ledger is shared across worktrees. So the guess is wrong for every
// worktree session, and an explicit --label or a typo reaches the same place.
// The fix is on the READ, because that is where every route converges.
//
// Note the asymmetry it closes: Say already refuses `not joined to room %q`.
//
// The two clauses are BOTH load-bearing, and the controls below are what prove
// it: a served room with no traffic must still answer empty, and a room with
// history must stay readable after it leaves the config.
func TestReadRefusesARoomTheDaemonNeitherServesNorRemembers(t *testing.T) {
	h := start(t)
	d := h.d

	// Traffic in the served room, so the refusal below is about the NAME and
	// not about an empty journal. `kind` is CHECKed against
	// ('chat','im','presence','system') and Daemon.append only LOGS a failed
	// insert — so a wrong kind here is a silent no-op that makes the history
	// control below look like a code defect. It did, once.
	d.append("lobby", "alice", "chat", "one")
	d.append("lobby", "alice", "chat", "two")

	t.Run("an unserved, unknown name is refused and names the rooms that exist", func(t *testing.T) {
		resp := d.dispatch(Request{Op: "read", Room: "wtB"})
		if resp.Error == "" {
			t.Fatalf("an unknown room answered OK with %d msgs — this is the 2026-09-07 failure", len(resp.Msgs))
		}
		if !strings.Contains(resp.Error, "does not serve") {
			t.Fatalf("refusal does not say why: %q", resp.Error)
		}
		if !strings.Contains(resp.Error, "lobby") {
			t.Fatalf("refusal must name the rooms that exist, or the caller cannot recover in one step: %q", resp.Error)
		}
	})

	// POSITIVE CONTROL 1 — THE QUIET ROOM. A configured room with nothing new
	// must still answer OK and empty. If this fails, the refusal has swallowed
	// the very case it exists to distinguish, and every quiet room now looks
	// like a typo.
	t.Run("a served room that is merely quiet still answers empty", func(t *testing.T) {
		resp := d.dispatch(Request{Op: "read", Room: "lobby", After: 1 << 40})
		if resp.Error != "" {
			t.Fatalf("a served, quiet room was refused: %q", resp.Error)
		}
		if !resp.OK || len(resp.Msgs) != 0 {
			t.Fatalf("want OK with 0 msgs, got OK=%v msgs=%d", resp.OK, len(resp.Msgs))
		}
	})

	// POSITIVE CONTROL 2 — HISTORY OUTLIVES THE CONFIG. A room dropped from
	// --rooms after accruing rows must stay readable, which is why the
	// predicate is "not served AND no history" rather than "not served".
	t.Run("a room with history but no longer served stays readable", func(t *testing.T) {
		d.append("archived", "bob", "chat", "still here")
		resp := d.dispatch(Request{Op: "read", Room: "archived"})
		if resp.Error != "" {
			t.Fatalf("a room with history was refused: %q", resp.Error)
		}
		if len(resp.Msgs) != 1 {
			t.Fatalf("want the one archived message, got %d", len(resp.Msgs))
		}
	})

	// And the same room, read past its own history, is quiet rather than
	// unknown: having history is a property of the ROOM, not of the window.
	t.Run("an unserved room with history is quiet past its own tail", func(t *testing.T) {
		resp := d.dispatch(Request{Op: "read", Room: "archived", After: 1 << 40})
		if resp.Error != "" {
			t.Fatalf("a known room read past its tail was refused: %q", resp.Error)
		}
		if len(resp.Msgs) != 0 {
			t.Fatalf("want 0 msgs past the tail, got %d", len(resp.Msgs))
		}
	})

	// Folding, because servedRoom has always folded and the two must agree.
	t.Run("the served check folds, as the presence path does", func(t *testing.T) {
		if resp := d.dispatch(Request{Op: "read", Room: "LOBBY"}); resp.Error != "" {
			t.Fatalf("a served room in another case was refused: %q", resp.Error)
		}
	})
}
