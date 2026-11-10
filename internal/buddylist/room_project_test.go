package buddylist

import (
	"context"
	"errors"
	"testing"
)

// Issue #83 (a). A session started with BUDDY_LABEL=fable-review has a label
// with no "<project>/" half, and a lane in a linked worktree has one naming the
// WORKTREE; the daemon serves one room per project, named for the main
// checkout. presence.note derived the room from the label alone, so neither
// was ever presented (measured: all 10 nicks presented in 14 days were
// default-labelled main-checkout sessions). The hook client now sends the
// main checkout's name and the room comes from it.
func TestPresenceUsesTheProjectTheClientSends(t *testing.T) {
	h := startPresence(t)
	// Control: a project this daemon serves no room for is still not
	// presented, label or no label. Presence must never invent a room.
	h.d.presence.note("sess-m", "other-lane", "example.org", nil)
	h.d.presence.note("sess-l", "fable-review", "harbor", []string{"review"})
	waitFor(t, "the BUDDY_LABEL session to join its project's room", func() bool {
		return recordedHas(h.script.conn("fable-review"), "join harbor")
	})
	for _, n := range h.script.names() {
		if n == "other-lane" {
			t.Fatalf("an unserved project was dialed: %v", h.script.names())
		}
	}
	if online, wanted := h.d.presence.live(); online != 1 || wanted != 1 {
		t.Fatalf("want exactly the served session: online=%d wanted=%d", online, wanted)
	}
}

// An older hook client sends no project: the label's project half still
// decides, exactly as before, so a daemon upgraded before its clients loses
// nobody's presence.
func TestPresenceWithNoProjectFallsBackToTheLabel(t *testing.T) {
	h := startPresence(t)
	h.d.presence.note("sess-n", "harbor/s-88888888", "", nil)
	waitFor(t, "the default-labelled session to join", func() bool {
		return recordedHas(h.script.conn("harbor-s-88888888"), "join harbor")
	})
}

// Issue #83 (b). The echo filter took any row whose body was a substring of
// one of this session's recent sends for the session's own echo. Its @sent
// rows carry its label and the slugs it announced, so the operator typing an
// addressed message whose whole body is the slug was silently dropped (probe:
// `fix-gate` alone 0 alert rows, `fix-gate status?` 1). Only the concierge
// relays a session's sends, so only the concierge's own rows can be an echo.
func TestAddressedAlertsOnAnOperatorsBareSlug(t *testing.T) {
	j := testJournal(t)
	say(t, j, "harbor", testLabel, "NUMBER CLAIM: slug "+testSlug+".")
	// Positive control: the same bare body from the concierge's own nick is
	// a substring of the send, and still reads as this session's echo.
	peerSays(t, j, "harbor", testSlug)
	got, err := j.Addressed(testSession, testLabel, "SmarterChild", testTokens(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("control: a concierge row inside this session's own send is its echo: %+v", got)
	}
	op, err := j.Append("harbor", "operator-client", "chat", testSlug)
	if err != nil {
		t.Fatal(err)
	}
	got, err = j.Addressed(testSession, testLabel, "smarterchild", testTokens(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Msgs) != 1 || got[0].Msgs[0].Seq != op {
		t.Fatalf("the operator's bare-slug ping must alert, exactly once: %+v", got)
	}
}

// The daemon passes its OWN name to the filter: the dispatch path, not just
// the journal call, is what the hook reaches.
func TestDispatchAlertsKnowsTheConciergesName(t *testing.T) {
	j := testJournal(t)
	d, err := New(Config{Journal: j, SocketPath: "unused.sock", Name: "SmarterChild", Rooms: []string{"harbor"},
		Dial: func(context.Context) (Conn, error) { return nil, errors.New("not dialed") }})
	if err != nil {
		t.Fatal(err)
	}
	say(t, j, "harbor", testLabel, "NUMBER CLAIM: slug "+testSlug+".")
	peerSays(t, j, "harbor", testSlug)
	op, err := j.Append("harbor", "operator-client", "chat", testSlug)
	if err != nil {
		t.Fatal(err)
	}
	resp := d.dispatch(Request{Op: "alerts", Session: testSession, Label: testLabel, Mentions: testTokens()})
	if resp.Error != "" {
		t.Fatal(resp.Error)
	}
	if len(resp.Alerts) != 1 || len(resp.Alerts[0].Msgs) != 1 || resp.Alerts[0].Msgs[0].Seq != op {
		t.Fatalf("want the operator's row and not the concierge's echo: %+v", resp.Alerts)
	}
}

// The alert hook is how a WORKING session is presented (it runs on every tool
// call; the presence op is SessionEnd's), so its request's project must reach
// presence too. The mutation check found this arm unpinned: passing "" there
// passed every other test.
func TestAlertsRequestPresentsInTheProjectsRoom(t *testing.T) {
	h := startPresence(t)
	resp := h.d.dispatch(Request{Op: "alerts", Session: "sess-p", Label: "fable-review", Project: "harbor",
		Mentions: []string{"fable-review"}})
	if resp.Error != "" {
		t.Fatal(resp.Error)
	}
	waitFor(t, "the alert hook's session to join its project's room", func() bool {
		return recordedHas(h.script.conn("fable-review"), "join harbor")
	})
}
