package buddylist

import (
	"context"
	"log/slog"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// THE CHAT FOLD IS store.Fold's KEY, AFTER A TRIM (D-222's follow-up).
//
// chatd.go restates the one folding rule rather than importing the ledger
// package (its comment says why), and promised "if store.Fold ever changes,
// this line changes with it". D-222 changed store.Fold and the line did not:
// rooms kept ToLower(NFC(s)), so `ſtrasse`, `STRAẞE` and `straße` read as
// three rooms nobody serves beside a served `strasse`, refused naming the
// rooms, where the volume calls the directories they are named after one.
// A promise in a comment is not a gate; this test is. It imports store only
// here, a test dependency, so the chat package still links without it.
//
// The rows are D-222's measured alias table (internal/store/fold_test.go),
// restated because that table is unexported: a drift on EITHER side (store's
// key moving, or this one) splits a row and goes red.
var chatFoldAliases = []struct{ name, a, b string }{
	{"long s", "src", "\u017frc"},
	{"sharp s", "strasse", "stra\u00dfe"},
	{"capital sharp s", "strasse", "stra\u1e9ee"},
	{"final sigma", "\u03c3", "\u03c2"},
	{"ligature fi", "file", "\ufb01le"},
	{"ligature ff", "off", "o\ufb00"},
	{"micro sign", "\u03bc", "\u00b5"},
	{"Greek theta symbol", "\u03b8", "\u03d1"},
	{"ypogegrammeni", "\u03b9", "\u0345"},
	{"alpha with ypogegrammeni", "\u03b1\u03b9", "\u1fb3"},
	{"ypogegrammeni reordered", "\u03b1\u0301\u0345", "\u03b1\u0345\u0301"},
	{"Cyrillic rounded ve", "\u0432", "\u1c80"},
	{"capital I dot above", "i\u0307", "\u0130"},
	{"Kelvin sign", "k", "\u212a"},
	// Already one under the old rule; a key that lost them must fail too.
	{"upper case", "lobby", "LOBBY"},
	{"NFD", "caf\u00e9", "cafe\u0301"},
}

func TestRoomFoldIsStoreFoldAfterTrim(t *testing.T) {
	same := func(s string) {
		t.Helper()
		if got, want := fold(s), store.Fold(strings.TrimSpace(s)); got != want {
			t.Fatalf("chat fold(%+q)=%+q, store.Fold of it trimmed=%+q: the two halves fold apart", s, got, want)
		}
	}
	for _, tc := range chatFoldAliases {
		same(tc.a)
		same(tc.b)
		if fold(tc.a) != fold(tc.b) {
			t.Errorf("%s: %+q and %+q are one name on the volume and in the ledger, two rooms in chat (%+q, %+q)",
				tc.name, tc.a, tc.b, fold(tc.a), fold(tc.b))
		}
		if fold(" "+strings.ToUpper(tc.a)+"\t") != fold(tc.b) {
			t.Errorf("%s: padding and case split %+q from %+q", tc.name, tc.a, tc.b)
		}
	}
	// The narrowing D-222 recorded, on this side too: U+0130 and a plain i are
	// two names on the volume and two scopes, so two rooms. The old rule merged
	// them through Go's ToLower.
	if fold("\u0130zmir") == fold("izmir") {
		t.Errorf("U+0130 and i fold together (%+q): the old rule's over-merge", fold("izmir"))
	}
	// Double-fold rows: the old rule re-composed H+U+0331 on a second pass,
	// and an NFC-ending key composed U+13C2 with U+113C8 under go1.27. Rooms
	// are folded on both sides of a compare, so a key that moves on refolding
	// splits a name from itself.
	for _, s := range []string{"H\u0331", "J\u030c", "\u0130\u0340", "\u13c2\U000113c8"} {
		same(s)
		if k := fold(s); fold(k) != k {
			t.Errorf("fold(fold(%+q))=%+q, fold(%+q)=%+q", s, fold(k), s, k)
		}
	}
	// Every code point, alone and padded, so a drift outside the table above
	// is seen too.
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue
		}
		same(string(r))
	}
	same(" \u00a0Stra\u00dfe\u2003")
	pool := []rune("aAsSiIkK \t\u00a0\u00df\u1e9e\u017f\u03c2\u03c3\u03a3\u0130\u0131\u212a\ufb01\ufb03\u0149\u1fb3\u0345\u00b5\u13a0\uab70\u1c80\u0301\u0307\u0331\u00e9\u13c2\U000113c8")
	rng := rand.New(rand.NewSource(222))
	for i := 0; i < 20000; i++ {
		rs := make([]rune, 1+rng.Intn(6))
		for j := range rs {
			rs[j] = pool[rng.Intn(len(pool))]
		}
		same(string(rs))
	}
}

// The fold reaches a read through canonicalRoom: a served room asked for in
// an alias spelling reads the rows journalled under the served one. Before the
// fix `ſtrasse`, `STRAẞE` and `straße` were unserved names with no rows of
// their own, refused naming the rooms.
//
// Rows carry the spelling they were journalled under and every key is
// computed at read time (no folded column), so moving the rule re-keys
// nothing on disk: a spelling that read a room before reads it now, save the
// one narrowing, which is refused naming the rooms and never answered quiet.
func TestServedRoomReadsUnderTheStoreFold(t *testing.T) {
	for _, tc := range []struct {
		name, configured, journalled string
		reads                        []string // each must return the 2 rows
		refused                      []string // each must be refused naming the rooms
	}{
		// POSITIVE CONTROL: the ASCII spellings that read before read now.
		{"ascii", "strasse", "strasse", []string{"strasse", "STRASSE", " Strasse\t"}, nil},
		{"aliases of an ascii room", "strasse", "strasse", []string{"\u017ftrasse", "STRA\u1e9eE", "stra\u00dfe", "stra\u00dfe "}, nil},
		// A non-ASCII room journalled precomposed (the old key's NFC form):
		// every spelling the old rule read it in reads it still.
		{"non-ascii journalled NFC", "caf\u00e9", "caf\u00e9", []string{"caf\u00e9", "cafe\u0301", "CAF\u00c9", "CAFE\u0301"}, nil},
		{"a non-ascii journalled spelling", "Stra\u00dfe", "stra\u00dfe", []string{"stra\u00dfe", "STRASSE", "Stra\u1e9ee"}, nil},
		// The narrowing: U+0130 and i are two rooms, as they are two files.
		{"dotted capital I", "\u0130zmir", "\u0130zmir", []string{"\u0130zmir", "i\u0307zmir"}, []string{"izmir", "IZMIR"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j, err := OpenJournal(filepath.Join(t.TempDir(), "journal.db"), nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { j.Close() })
			d, err := New(Config{Rooms: []string{tc.configured}, SocketPath: "/nonexistent/s.sock", Journal: j, Log: slog.Default(),
				Dial: func(ctx context.Context) (Conn, error) { return nil, ctx.Err() }})
			if err != nil {
				t.Fatal(err)
			}
			d.append(tc.journalled, "alice", "chat", "one")
			d.append(tc.journalled, "alice", "chat", "two")
			for _, room := range tc.reads {
				resp := d.dispatch(Request{Op: "read", Room: room})
				if resp.Error != "" || len(resp.Msgs) != 2 {
					t.Errorf("read %+q: err=%q msgs=%d, want the room's 2", room, resp.Error, len(resp.Msgs))
				}
			}
			for _, room := range tc.refused {
				resp := d.dispatch(Request{Op: "read", Room: room})
				if resp.Error == "" || !strings.Contains(resp.Error, tc.configured) {
					t.Errorf("read %+q: err=%q msgs=%d, want the refusal naming %+q", room, resp.Error, len(resp.Msgs), tc.configured)
				}
			}
		})
	}
}

// Two configured rooms that are one name under the fold were two joins on the
// server and one key in roomIDs (Codex code pass): `--rooms strasse,straße`,
// joined as ids 1 and 2, sent every Say to `strasse` into whichever joined
// last, and a leave of either deleted the other's entry. `Lobby,lobby` did the
// same under the old rule; the full fold made more pairs one. Refused at New,
// naming both, so the config is fixed rather than half-routed.
func TestRoomsOneUnderTheFoldAreRefused(t *testing.T) {
	j, err := OpenJournal(filepath.Join(t.TempDir(), "journal.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	mk := func(rooms ...string) error {
		_, err := New(Config{Rooms: rooms, SocketPath: "/nonexistent/s.sock", Journal: j, Log: slog.Default(),
			Dial: func(ctx context.Context) (Conn, error) { return nil, ctx.Err() }})
		return err
	}
	for _, rooms := range [][]string{{"strasse", "stra\u00dfe"}, {"Lobby", "ops", " lobby"}, {"src", "\u017frc"}} {
		err := mk(rooms...)
		if err == nil || !strings.Contains(err.Error(), strings.TrimSpace(rooms[0])) || !strings.Contains(err.Error(), strings.TrimSpace(rooms[len(rooms)-1])) {
			t.Errorf("rooms %+q: err=%v, want a refusal naming both", rooms, err)
		}
	}
	// POSITIVE CONTROLS: distinct rooms, padding, and empty entries (a
	// trailing comma) start as before, so a refusal above is the fold's.
	for _, rooms := range [][]string{{"lobby", "ops"}, {"lobby", " ops"}, {"lobby", "", "ops", ""}, {"\u0130zmir", "izmir"}} {
		if err := mk(rooms...); err != nil {
			t.Errorf("rooms %+q: %v, want started", rooms, err)
		}
	}
}

// The own-echo test names the SENDER by the fold (alert.go): an echo of the
// concierge comes back in the server's spelling, which may differ in case. The
// full fold made `straße`, a different account on a server that keeps it apart
// from `strasse`, the concierge too, so a peer's `[worker] item-1 is ready`
// read as worker's own echo and its alert was dropped (Codex code pass): the
// lost alert, the failure ownEcho is written to avoid. A non-ASCII sender is
// the concierge only byte for byte.
func TestOwnEchoSenderIsNotAFoldAlias(t *testing.T) {
	o := ownEcho("worker")
	body := fromSlot("worker") + " item-1 is ready"
	for _, sender := range []string{"strasse", "STRASSE", "Strasse"} {
		if !o.authored("strasse", Msg{Sender: sender, Body: body}) {
			t.Errorf("control: the concierge %q's echo is not recognized", sender)
		}
	}
	if !o.authored("stra\u00dfe", Msg{Sender: "stra\u00dfe", Body: body}) {
		t.Errorf("control: a non-ASCII concierge's byte-identical echo is not recognized")
	}
	for _, tc := range []struct{ self, sender string }{{"strasse", "alice"}, {"strasse", "stra\u00dfe"}, {"stra\u00dfe", "strasse"}, {"src", "\u017frc"}, {"bob", "BOB\u200b"}} {
		if o.authored(tc.self, Msg{Sender: tc.sender, Body: body}) {
			t.Errorf("concierge %+q: a message from %+q read as its own echo, and its alert is lost", tc.self, tc.sender)
		}
	}
}
