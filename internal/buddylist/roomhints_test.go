package buddylist

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A SERVED ROOM SPELLED ANOTHER WAY MUST NOT READ AS A QUIET ONE (#124).
//
// THE FAILURE (audit reproduction, a96d9f4): the read arm asked
// servesRoom(fold(req.Room)), so `LOBBY` counted as served and was not
// refused, and then Journal.Read ran `room=?` with the RAW spelling: OK, zero
// messages, for a room holding traffic. A send to the same spelling worked,
// because Say folds. That is D-024's failure (an empty read indistinguishable
// from a quiet room) back for a room the daemon does serve. The since_last
// cursor and the ack were keyed by the raw spelling too, so `LOBBY` and
// `lobby` were two cursors for one room.
//
// The fix canonicalises a served name to the daemon's own spelling before the
// journal is asked anything, so the read, the cursor and the ack all key by
// it. A name the daemon does NOT serve keeps its raw spelling, so it is read
// exactly or refused naming the rooms (D-024 unchanged): never quiet.
func TestReadOfAServedRoomInAnotherSpellingReadsTheRoom(t *testing.T) {
	h := start(t)
	d := h.d
	d.append("lobby", "alice", "chat", "one")
	d.append("lobby", "alice", "chat", "two")

	exact := d.dispatch(Request{Op: "read", Room: "lobby"})
	// POSITIVE CONTROL: the exact spelling reads both rows, so a zero below is
	// the spelling and not an empty fixture.
	if exact.Error != "" || len(exact.Msgs) != 2 {
		t.Fatalf("control: exact read got err=%q msgs=%d, want 2", exact.Error, len(exact.Msgs))
	}
	for _, room := range []string{"LOBBY", "Lobby", "lobby ", " LOBBY\t"} {
		resp := d.dispatch(Request{Op: "read", Room: room})
		if resp.Error != "" || len(resp.Msgs) != 2 {
			t.Errorf("read %q: err=%q msgs=%d, want the room's 2 (an OK-and-empty here is #124)", room, resp.Error, len(resp.Msgs))
		}
	}

	t.Run("since_last and ack key one cursor whatever the spelling", func(t *testing.T) {
		first := exact.Msgs[0].Seq
		if r := d.dispatch(Request{Op: "ack", Room: "LOBBY ", Session: "s1", Seq: first}); r.Error != "" || r.Cursor != first {
			t.Fatalf("ack LOBBY: err=%q cursor=%d", r.Error, r.Cursor)
		}
		if cur, err := d.cfg.Journal.Cursor("s1", "lobby"); err != nil || cur != first {
			t.Fatalf("an ack spelled LOBBY left lobby's cursor at %d (err %v), want %d", cur, err, first)
		}
		resp := d.dispatch(Request{Op: "read", Room: "Lobby", Session: "s1", SinceLast: true})
		if resp.Error != "" || resp.Cursor != first || len(resp.Msgs) != 1 || resp.Msgs[0].Body != "two" {
			t.Fatalf("since_last as Lobby: err=%q cursor=%d msgs=%d, want cursor %d and only \"two\"",
				resp.Error, resp.Cursor, len(resp.Msgs), first)
		}
	})

	// An UNSERVED room keeps its exact spelling: one with history in another
	// case is refused naming the rooms, never answered quiet. This holds on the
	// base too; it pins that the canonicalisation reaches only served names.
	t.Run("an unserved room in another spelling is refused, not quiet", func(t *testing.T) {
		d.append("archived", "bob", "chat", "kept")
		if r := d.dispatch(Request{Op: "read", Room: "archived"}); r.Error != "" || len(r.Msgs) != 1 {
			t.Fatalf("control: exact read of archived: err=%q msgs=%d", r.Error, len(r.Msgs))
		}
		r := d.dispatch(Request{Op: "read", Room: "ARCHIVED"})
		if r.Error == "" || !strings.Contains(r.Error, "lobby") {
			t.Fatalf("ARCHIVED answered OK with %d msgs, err=%q: want the refusal naming the served rooms", len(r.Msgs), r.Error)
		}
	})
}

// The journal's spelling wins over the config's (the Codex pass on the first
// draft, which canonicalised to the configured spelling): `--rooms Lobby`
// restarted over retained `lobby` rows turned an exact read of `lobby`, which
// had returned them, into OK and empty. Every spelling, the configured one
// included, reads the rows the journal holds, and an ack keys the cursor of
// the journalled spelling.
func TestServedRoomReadsByTheJournalsSpelling(t *testing.T) {
	// Both directions: a journalled spelling that is its own fold, and one
	// that is not (so the journal's names are folded, not compared raw).
	for _, tc := range []struct{ configured, journalled string }{{"Lobby", "lobby"}, {"lobby", "LoBBy"}} {
		t.Run(tc.configured+" over "+tc.journalled, func(t *testing.T) {
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
			for _, room := range []string{tc.journalled, tc.configured, "LOBBY ", "lObBy"} {
				resp := d.dispatch(Request{Op: "read", Room: room})
				if resp.Error != "" || len(resp.Msgs) != 2 {
					t.Errorf("read %q: err=%q msgs=%d, want 2", room, resp.Error, len(resp.Msgs))
				}
			}
			if r := d.dispatch(Request{Op: "ack", Room: tc.configured, Session: "s1", Seq: 1}); r.Error != "" {
				t.Fatalf("ack: %q", r.Error)
			}
			if cur, err := j.Cursor("s1", tc.journalled); err != nil || cur != 1 {
				t.Fatalf("an ack spelled as configured left the journalled room's cursor at %d (err %v), want 1", cur, err)
			}
			// POSITIVE CONTROL: a configured room with no history in ANY
			// spelling is still a truthful quiet room, not a refusal.
			j2, err := OpenJournal(filepath.Join(t.TempDir(), "journal2.db"), nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { j2.Close() })
			d.cfg.Journal = j2
			if r := d.dispatch(Request{Op: "read", Room: "LOBBY"}); r.Error != "" || !r.OK || len(r.Msgs) != 0 {
				t.Fatalf("a served room with no history: err=%q ok=%v msgs=%d, want OK and empty", r.Error, r.OK, len(r.Msgs))
			}
		})
	}
}

// chat_status(room=...) filtered with `==`, so a room named in another
// spelling than the journal's listed no rows: "(no rooms in the journal)" for
// a busy room, the same quiet-for-busy answer as #124's read.
func TestChatStatusRoomFilterFolds(t *testing.T) {
	deps := MCPDeps{Call: func(req Request, _ time.Duration) (Response, error) {
		return Response{OK: true, Stats: []RoomStat{{Room: "lobby", Retained: 2, NewestSeq: 2}, {Room: "other", Retained: 1, NewestSeq: 1}}}, nil
	}}
	resps := driveMCP(t, deps,
		`{"jsonrpc":"2.0","id":100,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"chat_status","arguments":{"room":"lobby"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"chat_status","arguments":{"room":"LOBBY "}}}`,
	)
	for i, want := range []string{"lobby", "LOBBY "} {
		text, isErr := toolText(t, resps[i+1])
		if isErr || strings.Contains(text, "no rooms in the journal") || !strings.Contains(text, "lobby") || strings.Contains(text, "other") {
			t.Errorf("chat_status room=%q: want lobby's row alone, got:\n%s", want, text)
		}
	}
}
