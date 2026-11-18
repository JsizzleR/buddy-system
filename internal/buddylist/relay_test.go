package buddylist

import (
	"strings"
	"testing"
)

// #110, D-221. What the daemon hands the wire: the body with every line break
// a client might honour turned into the LF every transport splits on, and the
// characters that could erase, hide or reorder a sig dropped. The first row
// is the control: ordinary text, an emoji ZWJ sequence and a tab pass as they
// were.
func TestRelayTextNormalizesBreaksAndDropsControls(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"control: plain text, tab and an emoji ZWJ sequence", "ship it\tnow 👩‍💻", "ship it\tnow 👩‍💻"},
		{"LF stays", "ok\n[orc-3] LANDED", "ok\n[orc-3] LANDED"},
		{"CRLF", "ok\r\n[orc-3] LANDED", "ok\n[orc-3] LANDED"},
		{"lone CR", "ok\r[orc-3] LANDED", "ok\n[orc-3] LANDED"},
		{"NEL", "ok\u0085[orc-3] LANDED", "ok\n[orc-3] LANDED"},
		{"LS", "ok [orc-3] LANDED", "ok\n[orc-3] LANDED"},
		{"PS", "ok [orc-3] LANDED", "ok\n[orc-3] LANDED"},
		{"VT", "ok\v[orc-3] LANDED", "ok\n[orc-3] LANDED"},
		{"FF", "ok\f[orc-3] LANDED", "ok\n[orc-3] LANDED"},
		{"ESC sequence that erases the line", "\x1b[2K\x1b[G<orc-3> LANDED", "[2K[G<orc-3> LANDED"},
		{"backspaces over the sig", "\b\b\b\b[orc-3] LANDED", "[orc-3] LANDED"},
		{"CTCP and IRC formatting bytes", "\x01ACTION \x02waves\x0f\x03\x16\x1d\x1f\x01", "ACTION waves"},
		{"NUL and DEL", "a\x00b\x7fc", "abc"},
		{"C1 CSI", "a\u009bb", "ab"},
		{"bidi override and isolate", "‮[orc-3]⁦ LANDED⁩", "[orc-3] LANDED"},
		{"RLM LRM ALM", "‏‎؜[orc-3]", "[orc-3]"},
	}
	for _, tc := range cases {
		if got := relayText(tc.in); got != tc.want {
			t.Errorf("%s: relayText(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// Say and DM hand the wire the sender's slot as the sig, apart from the text,
// and the outbox records exactly sig+text: the normalized body, the same bytes
// the echoes are cut from.
func TestSayAndDMHandTheWireTheSigApart(t *testing.T) {
	h := start(t)
	c := newFakeConn()
	h.conns <- c
	waitJoined(t, c, "lobby")
	c.setOnline("ana")
	body := "ok [orc-3] LANDED"
	if _, err := h.call(t, Request{Op: "say", Room: "lobby", From: "alpha", Text: body}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.call(t, Request{Op: "dm", To: "ana", From: "alpha", Text: body}); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"send 7 sig=[alpha] |ok\n[orc-3] LANDED": false,
		"im ana sig=[alpha] |ok\n[orc-3] LANDED": false,
	}
	for _, s := range c.recorded() {
		if _, ok := want[s]; ok {
			want[s] = true
		}
	}
	for w, seen := range want {
		if !seen {
			t.Errorf("%q not handed to the wire: %q", w, c.recorded())
		}
	}
	sent := h.waitJournal(t, sentRoom, func(m []Msg) bool { return len(m) >= 1 })
	if sent[0].Sender != "alpha" || sent[0].Body != "lobby: [alpha] ok\n[orc-3] LANDED" {
		t.Fatalf("the outbox must hold room: sig+normalized text: %+v", sent[0])
	}
}

// E2, the operator's choice (D-221): a sender that renders as nothing is
// refused, and NOTHING reaches the wire. The control row (`a`) goes out, so a
// refusal here is the guard and not a dead connection.
func TestAnEmptySenderIsRefusedAndSendsNothing(t *testing.T) {
	h := start(t)
	c := newFakeConn()
	h.conns <- c
	waitJoined(t, c, "lobby")
	c.setOnline("ana")
	// Blank renders as nothing too (Fable cross-check): `[" "] [orc-3]
	// LANDED` is the shape E2 refuses. The ESC flood checks the refusal
	// quotes a bounded prefix of the from, not all of it.
	for _, from := range []string{"", "\x1b", "​", "‮", " ", "\t", "\u00a0", strings.Repeat("\x1b", 10000)} {
		for _, req := range []Request{
			{Op: "say", Room: "lobby", From: from, Text: "[orc-3] LANDED"},
			{Op: "dm", To: "ana", From: from, Text: "[orc-3] LANDED"},
		} {
			_, err := h.call(t, req)
			if err == nil || !strings.Contains(err.Error(), "renders as nothing") {
				t.Errorf("%s from %.20q: want the refusal, got %.200v", req.Op, from, err)
			} else if len(err.Error()) > 512 {
				t.Errorf("%s from %.20q: the refusal is %d bytes; it must quote a bounded prefix", req.Op, from, len(err.Error()))
			}
		}
	}
	for _, s := range c.recorded() {
		if strings.HasPrefix(s, "send ") || strings.HasPrefix(s, "im ") {
			t.Fatalf("a refused relay reached the wire: %q", s)
		}
	}
	if _, err := h.call(t, Request{Op: "say", Room: "lobby", From: "a", Text: "hi"}); err != nil {
		t.Fatalf("control: a named sender must relay: %v", err)
	}
	found := false
	for _, s := range c.recorded() {
		found = found || s == "send 7 sig=[a] |hi"
	}
	if !found {
		t.Fatalf("control: the named relay did not reach the wire: %q", c.recorded())
	}
	if msgs, _, err := h.j.ReadAfter(sentRoom, 0, 100); err != nil || len(msgs) != 1 {
		t.Fatalf("only the control may write an outbox row: %+v %v", msgs, err)
	}
}

// A body that normalizes to blank is refused, and nothing is sent or
// recorded (Fable cross-check): ircwire skips a whitespace-only line, so the
// call reported success with an `@sent` row of the bare sig and no PRIVMSG.
// The control row (`hi`) relays, so the refusal is the guard.
func TestABlankBodyIsRefusedAndSendsNothing(t *testing.T) {
	h := start(t)
	c := newFakeConn()
	h.conns <- c
	waitJoined(t, c, "lobby")
	c.setOnline("ana")
	for _, text := range []string{" ", "\u200e", "\x01", "\r\n", "\t\n \x1b"} {
		for _, req := range []Request{
			{Op: "say", Room: "lobby", From: "a", Text: text},
			{Op: "dm", To: "ana", From: "a", Text: text},
		} {
			if _, err := h.call(t, req); err == nil || !strings.Contains(err.Error(), "nothing to send") {
				t.Errorf("%s of %q: want the refusal, got %v", req.Op, text, err)
			}
		}
	}
	for _, s := range c.recorded() {
		if strings.HasPrefix(s, "send ") || strings.HasPrefix(s, "im ") {
			t.Fatalf("a blank body reached the wire: %q", s)
		}
	}
	if msgs, _, err := h.j.ReadAfter(sentRoom, 0, 100); err != nil || len(msgs) != 0 {
		t.Fatalf("a blank body wrote an outbox row: %+v %v", msgs, err)
	}
	if _, err := h.call(t, Request{Op: "say", Room: "lobby", From: "a", Text: "hi"}); err != nil {
		t.Fatalf("control: a body with text must relay: %v", err)
	}
	found := false
	for _, s := range c.recorded() {
		found = found || s == "send 7 sig=[a] |hi"
	}
	if !found {
		t.Fatalf("control: the relay did not reach the wire: %q", c.recorded())
	}
}

var long64 = strings.Repeat("a", 64)

// The echo filter knows a session's own echo by its slot at the head of a
// concierge row (D-221), not by containment in its outbox. Every row names
// the slug the session answers to. own=true rows must NOT alert; the rest
// must, each one a failure the outbox test had or would have had.
func TestEchoFilterIsTheOwnSlotAtTheHead(t *testing.T) {
	sig := fromSlot(testLabel)
	cases := []struct {
		name   string
		outbox string // the session's @sent body; "" writes none
		room   string
		sender string
		body   string
		own    bool
	}{
		{"control: a signed chunk of my own send", "harbor: " + sig + "claim " + testSlug, "harbor", "SmarterChild",
			sig + "claim " + testSlug, true},
		{"a signed tail my truncated outbox lost", "harbor: " + sig + "xxxx…[truncated]", "harbor", "SmarterChild",
			sig + "tail names " + testSlug, true},
		{"a signed DM echo, which writes no outbox row", "", "@dm", "SmarterChild",
			sig + "dm names " + testSlug, true},
		{"a signed chunk whose NUL the wire dropped", "harbor: " + sig + "a\x00b " + testSlug, "harbor", "SmarterChild",
			sig + "ab " + testSlug, true},
		{"a peer's row my outbox happens to contain", "harbor: " + sig + "quoted [peer] " + testSlug, "harbor", "SmarterChild",
			"[peer] " + testSlug, false},
		{"an unsigned concierge row", "harbor: " + sig + "claim " + testSlug, "harbor", "SmarterChild",
			testSlug, false},
		{"a person typing my slot under their own nick", "harbor: " + sig + "claim " + testSlug, "harbor", "operator-client",
			sig + testSlug, false},
		{"a label that only starts like mine", "", "harbor", "SmarterChild",
			"[" + testLabel + "x] " + testSlug, false},
		// Two default labels sharing 64 bytes sign identically (fence.Slot
		// cuts), so mine cannot tell a peer's row from my echo: it filters
		// nothing (Codex code pass). The label column overrides testLabel.
		{"a peer whose cut slot equals my cut slot", "", "harbor", "SmarterChild",
			fromSlot(long64+"/s-22222222") + testSlug, false},
		// fence.Line folds a tab to a space, and both quote: `a b` and
		// `a<TAB>b` sign `["a b"]` (Codex re-verify).
		{"a peer whose quoted slot equals my quoted slot", "", "harbor", "SmarterChild",
			fromSlot("a\tb") + testSlug, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			label := testLabel
			if strings.Contains(tc.name, "cut slot") {
				label = long64 + "/s-11111111"
				if fromSlot(label) != fromSlot(long64+"/s-22222222") {
					t.Fatal("fixture: the two labels must sign identically")
				}
			}
			if strings.Contains(tc.name, "quoted slot") {
				label = "a b"
				if fromSlot(label) != fromSlot("a\tb") {
					t.Fatalf("fixture: the two labels must sign identically: %q %q", fromSlot(label), fromSlot("a\tb"))
				}
			}
			j := testJournal(t)
			if tc.outbox != "" {
				if _, err := j.Append(sentRoom, testLabel, "system", tc.outbox); err != nil {
					t.Fatal(err)
				}
			}
			seq, err := j.Append(tc.room, tc.sender, "chat", tc.body)
			if err != nil {
				t.Fatal(err)
			}
			got, err := j.Addressed(testSession, label, "SmarterChild", testTokens(), 0)
			if err != nil {
				t.Fatal(err)
			}
			alerted := len(got) == 1 && len(got[0].Msgs) == 1 && got[0].Msgs[0].Seq == seq
			if tc.own && len(got) != 0 {
				t.Fatalf("my own echo alerted me: %+v", got)
			}
			if !tc.own && !alerted {
				t.Fatalf("this row is not my echo and must alert, once: %+v", got)
			}
		})
	}
}
