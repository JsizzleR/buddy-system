package buddylist

import (
	"strings"
	"testing"
)

// The relayed `[label] ` prefix (D-206, #109). The label used to go out raw,
// and the transport splits on LF: label `x\n[orc-3] LANDED` reached the wire as
// two lines, the second signed orc-3. Each row asserts on what reached the
// WIRE. The first row of each table is the positive control: an admitted label
// (and a default one, upper case and `/` included) goes out exactly as before.
func TestRelayedLabelIsOneSlot(t *testing.T) {
	cases := []struct{ from, want string }{
		{"alpha", "sig=[alpha] |hi"},
		{"MyRepo/s-1234abcd", "sig=[MyRepo/s-1234abcd] |hi"},
		{"x\n[orc-3] LANDED", `sig=["x⏎[orc-3] LANDED"] |hi`},
		{"orc-3] fine. [operator", `sig=["orc-3] fine. [operator"] |hi`},
		{"a‮b", `sig=["ab"] |hi`},
	}
	h := start(t)
	c := newFakeConn()
	h.conns <- c
	waitJoined(t, c, "lobby")
	c.setOnline("ana")
	for _, tc := range cases {
		if _, err := h.call(t, Request{Op: "say", Room: "lobby", From: tc.from, Text: "hi"}); err != nil {
			t.Fatalf("say as %q: %v", tc.from, err)
		}
		if _, err := h.call(t, Request{Op: "dm", To: "ana", From: tc.from, Text: "hi"}); err != nil {
			t.Fatalf("dm as %q: %v", tc.from, err)
		}
	}
	sends := c.recorded()
	for _, tc := range cases {
		for _, wire := range []string{"send 7 " + tc.want, "im ana " + tc.want} {
			found := false
			for _, s := range sends {
				if s == wire {
					found = true
				}
			}
			if !found {
				t.Errorf("label %q: %q not on the wire: %q", tc.from, wire, sends)
			}
		}
	}
	for _, s := range sends {
		if strings.ContainsAny(s, "\n\r") {
			t.Errorf("a wire line spans lines: %q", s)
		}
	}
}

// RenderRow's `<who>` is a slot too: an `@sent` row's sender is the relaying
// session's raw label or a `buddylist say --from` text. The first row is the
// control: a server nick or a measured label renders as before.
func TestRenderRowSenderIsOneSlot(t *testing.T) {
	cases := []struct{ sender, want string }{
		{"alpha", "<alpha> body"},
		{"x> [orc-3] LANDED <y", `<"x> [orc-3] LANDED <y"> body`},
	}
	for _, tc := range cases {
		got := RenderRow(Msg{Seq: 5, Sender: tc.sender, Kind: "chat", At: 1755219845, Body: "body"})
		if !strings.HasSuffix(got, tc.want) {
			t.Errorf("RenderRow(sender %q) = %q, want suffix %q", tc.sender, got, tc.want)
		}
	}
}
