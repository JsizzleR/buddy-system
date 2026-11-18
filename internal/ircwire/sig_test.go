package ircwire

import (
	"strings"
	"testing"
)

// #110, D-221: a body could open a wire line of its own. The daemon's `[me] `
// went on ONCE, ahead of text that privmsg then cut at every LF and every 400
// bytes, so `ok\n[orc-3] LANDED` and 395 bytes + `[orc-3] LANDED` each put a
// bare `PRIVMSG #lobby :[orc-3] LANDED` on the wire, journaled as the
// concierge relaying orc-3 (reproduced before the fix, both shapes). Each row
// names the payloads the server must receive; every one must open with the
// sig, fit maxChunk, and the pieces must reassemble to the line they came from.
// The empty-sig row is the positive control: an unsigned send's bytes are
// exactly what they were.
func TestEveryPieceOpensWithTheSig(t *testing.T) {
	x395 := strings.Repeat("x", 395)
	x394 := strings.Repeat("x", 394)
	cases := []struct {
		name, sig, text string
		want            []string
	}{
		{"control: no sig, today's bytes", "", "line one\nline two",
			[]string{"line one", "line two"}},
		{"a newline opens a signed line", "[me] ", "ok\n[orc-3] LANDED",
			[]string{"[me] ok", "[me] [orc-3] LANDED"}},
		{"the 400-byte cut opens a signed piece", "[me] ", x395 + "[orc-3] LANDED",
			[]string{"[me] " + x395, "[me] [orc-3] LANDED"}},
		{"exactly the budget is one piece", "[me] ", x395,
			[]string{"[me] " + x395}},
		{"a rune straddling the budget moves whole", "[me] ", x394 + "éy",
			[]string{"[me] " + x394, "[me] éy"}},
		{"empty and blank lines send nothing, not a bare sig", "[me] ", "a\n\n   \n\t\nb",
			[]string{"[me] a", "[me] b"}},
		{"CR and NUL are dropped, no line opened", "[me] ", "a\r[orc-3]\x00 b",
			[]string{"[me] a[orc-3] b"}},
		{"a quoted label sig", `["a b"] `, "ok\n[orc-3] LANDED",
			[]string{`["a b"] ok`, `["a b"] [orc-3] LANDED`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeServer(t)
			c := dialOK(t, f)
			if err := c.ChatSend("#lobby", tc.sig, tc.text); err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				got := f.readLine()
				payload, ok := strings.CutPrefix(got, "PRIVMSG #lobby :")
				if !ok || payload != want {
					t.Fatalf("wire line %q, want payload %q", got, want)
				}
				if !strings.HasPrefix(payload, tc.sig) || len(payload) > maxChunk {
					t.Fatalf("payload %q must open with %q and fit %d bytes (has %d)", payload, tc.sig, maxChunk, len(payload))
				}
			}
			// Nothing more was sent: the probe is the next line.
			if err := c.ChatSend("#lobby", "", "probe"); err != nil {
				t.Fatal(err)
			}
			if got := f.readLine(); got != "PRIVMSG #lobby :probe" {
				t.Fatalf("an extra line went out: %q", got)
			}
		})
	}
}

// IM shares privmsg; one row proves it is signed per piece too.
func TestIMOpensEveryPieceWithTheSig(t *testing.T) {
	f := newFakeServer(t)
	c := dialOK(t, f)
	if err := c.IM("ana", "[me] ", "ok\n[orc-3] LANDED"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"PRIVMSG ana :[me] ok", "PRIVMSG ana :[me] [orc-3] LANDED"} {
		if got := f.readLine(); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

// A sig that is not one line, or that leaves no room for text, is refused and
// sends NOTHING. Each refusal has its positive control: the same call with a
// sound sig goes out, so "it refused" cannot be "it never ran".
func TestABadSigIsRefusedAndSendsNothing(t *testing.T) {
	longTarget := "#" + strings.Repeat("c", 300)
	cases := []struct {
		name, target, badSig, goodSig, text string
	}{
		{"sig with LF", "#lobby", "[a]\n[orc-3] ", "[a] ", "hi"},
		{"sig with CR", "#lobby", "[a]\r", "[a] ", "hi"},
		{"sig with NUL", "#lobby", "[a]\x00", "[a] ", "hi"},
		// No text means no composed line for the per-line check to see: only
		// the sig check stands between this and a later caller (Codex code pass).
		{"sig with LF and no text", "#lobby", "[a]\n[orc-3] ", "[a] ", ""},
		// 510 - len("PRIVMSG  :")-2 - 301 = 197 for text; a 196-byte sig leaves 1.
		{"sig leaving less than a rune", longTarget, "[" + strings.Repeat("s", 193) + "] ", "[s] ", "hi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeServer(t)
			c := dialOK(t, f)
			if err := c.ChatSend(tc.target, tc.badSig, tc.text); err == nil {
				t.Fatal("a bad sig must be refused")
			}
			if err := c.ChatSend(tc.target, tc.goodSig, "hi"); err != nil {
				t.Fatalf("control: a sound sig must send: %v", err)
			}
			if got, want := f.readLine(), "PRIVMSG "+tc.target+" :"+tc.goodSig+"hi"; got != want {
				t.Fatalf("the refused send leaked a line, or the control did not go out: got %q want %q", got, want)
			}
		})
	}
}
