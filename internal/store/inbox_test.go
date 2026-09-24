package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// D-043 (issue #34, wishlist §4): a correction names what it corrects, goes to
// exactly the original's audience, is sent only by the original's sender, and
// withholds nothing.

func send(t *testing.T, st *Store, target, sender string, o SendOpts) int64 {
	t.Helper()
	id, err := st.Send(mustResolve(t, st, target), "x", "body", SendOpts{SenderSession: sender, SenderKnown: true, Supersedes: o.Supersedes})
	if err != nil {
		t.Fatalf("send to %s: %v", target, err)
	}
	return id
}

func TestCorrectionOwnership(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	hello(t, st, "sess-b", "bravo", "/wt/b")
	orig := send(t, st, "bravo", a.SessionID, SendOpts{})
	bravo := mustResolve(t, st, "bravo")

	var nc ErrNotCorrectable
	cases := []struct {
		name string
		o    SendOpts
	}{
		{"another session", SendOpts{SenderSession: "sess-b", SenderKnown: true, Supersedes: orig}},
		{"the operator", SendOpts{SenderSession: "", SenderKnown: true, Supersedes: orig}},
		{"an unresolved sender", SendOpts{SenderKnown: false, Supersedes: orig}},
		{"no such message", SendOpts{SenderSession: a.SessionID, SenderKnown: true, Supersedes: 9999}},
	}
	for _, tc := range cases {
		if _, err := st.Send(bravo, "x", "fix", tc.o); !errors.As(err, &nc) {
			t.Errorf("%s must not correct #%d: %v", tc.name, orig, err)
		}
		if err := st.CheckCorrection(bravo, tc.o); !errors.As(err, &nc) {
			t.Errorf("the dry run must refuse what the send refuses (%s): %v", tc.name, err)
		}
	}
	// Positive control: the sender corrects its own message.
	if _, err := st.Send(bravo, "x", "fix", SendOpts{SenderSession: a.SessionID, SenderKnown: true, Supersedes: orig}); err != nil {
		t.Fatalf("control: the original's sender must be able to correct it: %v", err)
	}
}

// A message whose sender is UNKNOWN cannot be corrected by anybody, the
// operator included: the migration must not turn history into the operator's.
func TestUnknownSenderIsNobodysToCorrect(t *testing.T) {
	st, _ := openTest(t)
	hello(t, st, "sess-b", "bravo", "/wt/b")
	bravo := mustResolve(t, st, "bravo")
	unknown, err := st.Send(bravo, "x", "b", SendOpts{SenderKnown: false})
	if err != nil {
		t.Fatal(err)
	}
	var nc ErrNotCorrectable
	for _, o := range []SendOpts{{SenderSession: "", SenderKnown: true}, {SenderSession: "sess-b", SenderKnown: true}} {
		o.Supersedes = unknown
		if _, err := st.Send(bravo, "x", "fix", o); !errors.As(err, &nc) {
			t.Fatalf("an unknown-sender message must not be correctable by %q: %v", o.SenderSession, err)
		}
	}
	// Control: an operator message IS the operator's.
	op, err := st.Send(bravo, "x", "b", SendOpts{SenderKnown: true})
	if err != nil {
		t.Fatal(err)
	}
	// An UNRESOLVED sender carries an empty session id too, and must not
	// match the operator's.
	if _, err := st.Send(bravo, "x", "fix", SendOpts{SenderKnown: false, Supersedes: op}); !errors.As(err, &nc) {
		t.Fatalf("an unresolved sender must not correct the operator's message: %v", err)
	}
	if _, err := st.Send(bravo, "x", "fix", SendOpts{SenderKnown: true, Supersedes: op}); err != nil {
		t.Fatalf("control: the operator corrects the operator's message: %v", err)
	}
}

func TestCorrectionGoesToTheOriginalsAudience(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	hello(t, st, "sess-b", "bravo", "/wt/b")
	direct := send(t, st, "bravo", a.SessionID, SendOpts{})
	var nc ErrNotCorrectable
	for _, target := range []string{"all", "alpha"} {
		if _, err := st.Send(mustResolve(t, st, target), "x", "fix", SendOpts{SenderSession: a.SessionID, SenderKnown: true, Supersedes: direct}); !errors.As(err, &nc) {
			t.Fatalf("a correction of a message to bravo sent to %s must be refused: %v", target, err)
		}
	}

	// A broadcast's correction inherits the original's SNAPSHOT: a session
	// that started after the original is not sent a correction of a message
	// it never had.
	bcast := send(t, st, "all", a.SessionID, SendOpts{})
	hello(t, st, "sess-c", "charlie", "/wt/c")
	fix := send(t, st, "all", a.SessionID, SendOpts{Supersedes: bcast})
	msgs, err := st.Undelivered("sess-c", "charlie")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if m.ID == fix {
			t.Fatal("a session outside the original's audience must not be sent its correction")
		}
	}
	// Positive control: a fresh broadcast does reach charlie.
	fresh := send(t, st, "all", a.SessionID, SendOpts{})
	if msgs, _ := st.Undelivered("sess-c", "charlie"); len(msgs) != 1 || msgs[0].ID != fresh {
		t.Fatalf("control: an ordinary broadcast reaches charlie: %+v", msgs)
	}
	// And bravo, in the original audience, gets both, linked.
	msgs, err = st.Undelivered("sess-b", "bravo")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]InboxMsg{}
	for _, m := range msgs {
		byID[m.ID] = m
	}
	if !reflect.DeepEqual(byID[bcast].SupersededBy, []int64{fix}) || byID[fix].Supersedes != bcast {
		t.Fatalf("bravo must get the original marked and the correction linked: %+v / %+v", byID[bcast], byID[fix])
	}
}

func TestCorrectionLinksAsTheyStandForTheRecipient(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	hello(t, st, "sess-b", "bravo", "/wt/b")
	orig := send(t, st, "bravo", a.SessionID, SendOpts{})
	if err := st.MarkDelivered("sess-b", []int64{orig}); err != nil {
		t.Fatal(err)
	}
	clk.advance(20 * time.Minute)
	fix1 := send(t, st, "bravo", a.SessionID, SendOpts{Supersedes: orig})
	fix2 := send(t, st, "bravo", a.SessionID, SendOpts{Supersedes: orig})
	msgs, err := st.Undelivered("sess-b", "bravo")
	if err != nil || len(msgs) != 2 {
		t.Fatalf("want the two corrections queued: %+v %v", msgs, err)
	}
	if msgs[0].OrigDelivered.IsZero() || msgs[0].OrigExpired {
		t.Fatalf("the original reached bravo before the correction, and the link must say so: %+v", msgs[0])
	}
	si, ok, err := st.Sent(orig)
	if err != nil || !ok || !reflect.DeepEqual(si.SupersededBy, []int64{fix1, fix2}) {
		t.Fatalf("two corrections of one message are both kept: %+v %v", si, err)
	}
}

// A broadcast that expired before it reached a session: its correction says
// so, and the report says expired rather than queued.
func TestExpiredOriginal(t *testing.T) {
	st, clk := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	hello(t, st, "sess-b", "bravo", "/wt/b")
	orig := send(t, st, "all", a.SessionID, SendOpts{})
	clk.advance(BroadcastKeep + time.Hour)
	fix := send(t, st, "all", a.SessionID, SendOpts{Supersedes: orig})
	msgs, err := st.Undelivered("sess-b", "bravo")
	if err != nil || len(msgs) != 1 || msgs[0].ID != fix || !msgs[0].OrigExpired {
		t.Fatalf("the correction must say the original expired undelivered: %+v %v", msgs, err)
	}
	si, _, err := st.Sent(fix)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range si.Recipients {
		if r.SessionID == "sess-b" && !r.Original.Expired {
			t.Fatalf("the report must say the original expired for bravo, not queued: %+v", r)
		}
	}
}

func TestSentReport(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	hello(t, st, "sess-b", "bravo", "/wt/b")
	hello(t, st, "sess-c", "charlie", "/wt/c")
	id := send(t, st, "all", a.SessionID, SendOpts{})
	if err := st.MarkDelivered("sess-b", []int64{id}); err != nil {
		t.Fatal(err)
	}
	si, ok, err := st.Sent(id)
	if err != nil || !ok || len(si.Recipients) != 3 {
		t.Fatalf("a broadcast's report names its whole snapshot: %+v %v", si, err)
	}
	delivered := 0
	for _, r := range si.Recipients {
		if !r.Delivered.IsZero() {
			delivered++
			if r.SessionID != "sess-b" {
				t.Fatalf("only bravo's delivery is recorded: %+v", r)
			}
		}
	}
	if delivered != 1 {
		t.Fatalf("want 1 recorded delivery, got %d", delivered)
	}
	mine, err := st.SentBy(a.SessionID, 10)
	if err != nil || len(mine) != 1 || mine[0].ID != id {
		t.Fatalf("SentBy lists the session's own sends: %+v %v", mine, err)
	}
	if theirs, _ := st.SentBy("sess-b", 10); len(theirs) != 0 {
		t.Fatalf("and nobody else's: %+v", theirs)
	}
}

// A ledger from before the columns migrates with every message's sender
// UNKNOWN, not the operator's, and gains the index a drain reads.
func TestInboxColumnsMigrateFromV11(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	st, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	hello(t, st, "sess-b", "bravo", "/wt/b")
	if err := st.Msg(mustResolve(t, st, "bravo"), "x", "old"); err != nil {
		t.Fatal(err)
	}
	st.Close()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// Rebuilt in its v11 shape rather than by DROP COLUMN, which cannot
	// re-parse this table's commented CREATE text.
	for _, q := range []string{`DROP INDEX inbox_supersedes`,
		`CREATE TABLE inbox_v11 (msg_id INTEGER PRIMARY KEY AUTOINCREMENT, target TEXT NOT NULL, sender TEXT NOT NULL, body TEXT NOT NULL, created INTEGER NOT NULL)`,
		`INSERT INTO inbox_v11 SELECT msg_id, target, sender, body, created FROM inbox`,
		`DROP TABLE inbox`, `ALTER TABLE inbox_v11 RENAME TO inbox`, `PRAGMA user_version = 11`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	db.Close()

	st, err = Open(path, nil)
	if err != nil {
		t.Fatalf("a v11 ledger must migrate: %v", err)
	}
	defer st.Close()
	var owner sql.NullString
	if err := st.db.QueryRow(`SELECT sender_session FROM inbox`).Scan(&owner); err != nil || owner.Valid {
		t.Fatalf("a pre-D-043 message's sender must read UNKNOWN (NULL), got %+v %v", owner, err)
	}
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='inbox_supersedes'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the migration must create inbox_supersedes: %d %v", n, err)
	}
	// And the operator cannot claim it.
	var nc ErrNotCorrectable
	if _, err := st.Send(mustResolve(t, st, "bravo"), "x", "fix", SendOpts{SenderKnown: true, Supersedes: 1}); !errors.As(err, &nc) {
		t.Fatalf("the operator must not correct a migrated message: %v", err)
	}
}

// A session LABELLED "all" is not addressed by every broadcast: the snapshot is
// the only way into one (Codex code pass, D-043).
func TestTheLabelAllIsNotABroadcastAddress(t *testing.T) {
	st, _ := openTest(t)
	a := hello(t, st, "sess-a", "alpha", "/wt/a")
	id := send(t, st, "all", a.SessionID, SendOpts{})
	hello(t, st, "sess-x", "all", "/wt/x") // started after the broadcast
	if msgs, err := st.Undelivered("sess-x", "all"); err != nil || len(msgs) != 0 {
		t.Fatalf("a session labelled all, outside the snapshot, must get nothing: %+v %v", msgs, err)
	}
	// Control: alpha, in the snapshot, gets it.
	if msgs, _ := st.Undelivered(a.SessionID, "alpha"); len(msgs) != 1 || msgs[0].ID != id {
		t.Fatalf("control: the snapshot's member gets the broadcast: %+v", msgs)
	}
}

// The store refuses a kind no CLI door would write (D-045).
func TestSendRefusesAMalformedKind(t *testing.T) {
	st, _ := openTest(t)
	hello(t, st, "sess-b", "bravo", "/wt/b")
	bravo := mustResolve(t, st, "bravo")
	for _, o := range []SendOpts{
		{SenderKnown: true, Kind: "verified"},
		{SenderKnown: true, Kind: KindMeasured},
		{SenderKnown: true, Kind: KindRelay},
		{SenderKnown: true, Kind: KindLead, KindNote: "x"},
		{SenderKnown: true, Kind: "", KindNote: "x"},
		// A caller that skips the CLI cannot record what the CLI refuses: a
		// note that renders empty, or one past its rendered cap.
		{SenderKnown: true, Kind: KindMeasured, KindNote: "\x1b"},
		{SenderKnown: true, Kind: KindRelay, KindNote: strings.Repeat("a\n", 30)},
	} {
		if _, err := st.Send(bravo, "x", "b", o); err == nil {
			t.Fatalf("kind %q note %q must be refused", o.Kind, o.KindNote)
		}
	}
	id, err := st.Send(bravo, "x", "b", SendOpts{SenderKnown: true, Kind: KindMeasured, KindNote: "rows"})
	if err != nil {
		t.Fatalf("control: a well-formed kind is written: %v", err)
	}
	msgs, _ := st.Undelivered("sess-b", "bravo")
	if len(msgs) != 1 || msgs[0].ID != id || msgs[0].Kind != KindMeasured || msgs[0].KindNote != "rows" {
		t.Fatalf("the kind must read back: %+v", msgs)
	}
}
