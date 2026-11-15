package store

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
)

// StopHold (D-193) is StopTurn for a session on two ledgers: the fence, the
// read and the idle mark stay one transaction per ledger, and body (which
// writes the ONE Stop document, perhaps carrying the other ledger's mail)
// decides whether the turn is held. Each row of the contract is a case here,
// and every refusal row has an admitted twin that DOES write, so "nothing was
// written" cannot be "nothing was ever going to be".
func TestStopHoldFencesThisLedgerAndLetsBodyDecideHeld(t *testing.T) {
	type got struct {
		called, admitted bool
		msgs             int
	}
	cases := []struct {
		name      string
		late      bool // the turn ended before this incarnation registered
		unknown   bool // the session never said hello in this ledger
		drain     bool
		retIDs    bool // body returns this ledger's ids
		held      bool // body says a document went out
		wantOut   StopOutcome
		wantGot   got
		delivered bool // this ledger's message marked delivered
		idle      bool // an idle row afterwards
	}{
		{name: "held by this ledger's mail", drain: true, retIDs: true, held: true,
			wantOut: StopDrained, wantGot: got{true, true, 1}, delivered: true},
		{name: "held by the OTHER ledger's mail alone", drain: true, held: true,
			wantOut: StopDrained, wantGot: got{true, true, 1}},
		{name: "not held marks idle", drain: true,
			wantOut: StopIdle, wantGot: got{true, true, 1}, idle: true},
		{name: "no drain reads no mail", drain: false,
			wantOut: StopIdle, wantGot: got{true, true, 0}, idle: true},
		// The fence refuses this ledger's Stop; body still runs (the other
		// ledger's Stop may be this incarnation's) and NOTHING of this
		// ledger's is written, whatever body returns.
		{name: "late turn: held claims nothing", late: true, drain: true, retIDs: true, held: true,
			wantOut: StopNone, wantGot: got{true, false, 0}},
		{name: "late turn: not held marks nothing", late: true, drain: true,
			wantOut: StopNone, wantGot: got{true, false, 0}},
		{name: "unknown session: body still runs", unknown: true, drain: true, held: true,
			wantOut: StopNone, wantGot: got{true, false, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, clk := openTest(t)
			b := hello(t, st, "sess-b", "bravo", "/wt/b")
			turn := clk.t
			id := "sess-a"
			if !tc.unknown {
				a := hello(t, st, id, "alpha", "/wt/a")
				// An earlier toolless turn's mark, for "held" to retract.
				if err := st.MarkIdle(id, turn, turn); err != nil {
					t.Fatal(err)
				}
				if !idleRowExists(t, st, id) {
					t.Fatal("control: the earlier mark must exist")
				}
				clk.advance(time.Minute)
				if tc.late {
					if err := st.Bye(a.SessionID, a.Incarnation); err != nil {
						t.Fatal(err)
					}
					clk.advance(time.Minute)
					hello(t, st, id, "alpha", "/wt/a") // a new incarnation; the turn predates it
				} else {
					turn = clk.t
				}
			}
			var msgID int64
			if !tc.unknown {
				var err error
				if msgID, err = st.Send(mustResolve(t, st, id), "bravo", "hi", SendOpts{SenderSession: b.SessionID, SenderKnown: true}); err != nil {
					t.Fatal(err)
				}
			}
			before := idleRaw(t, st, id)
			var g got
			out, err := st.StopHold(id, "alpha", turn, turn, tc.drain, func(msgs []InboxMsg, admitted bool) ([]int64, bool, error) {
				g = got{true, admitted, len(msgs)}
				var ids []int64
				if tc.retIDs {
					ids = []int64{msgID}
				}
				return ids, tc.held, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if out != tc.wantOut || g != tc.wantGot {
				t.Fatalf("outcome %v body %+v, want %v %+v", out, g, tc.wantOut, tc.wantGot)
			}
			if tc.unknown {
				return
			}
			queued, err := st.Undelivered(id, "alpha")
			if err != nil {
				t.Fatal(err)
			}
			if (len(queued) == 0) != tc.delivered {
				t.Errorf("delivered = %v, want %v", len(queued) == 0, tc.delivered)
			}
			// A fenced-out Stop leaves the row exactly as it found it (the
			// earlier incarnation's mark, which IdleSessions already ignores):
			// neither retracted nor rewritten. An admitted one's row was made
			// above, so "no row" there is the retraction.
			if tc.late {
				if after := idleRaw(t, st, id); after != before || before == "" {
					t.Errorf("a fenced-out Stop must leave the idle row alone: %q -> %q", before, after)
				}
				return
			}
			if got := idleRowExists(t, st, id); got != tc.idle {
				t.Errorf("idle row = %v, want %v", got, tc.idle)
			}
		})
	}
}

// An error from body rolls the whole transaction back: no delivery, no mark,
// and the earlier mark is NOT retracted, so a failed document cannot leave the
// session looking busy with its mail eaten.
func TestStopHoldBodyErrorWritesNothing(t *testing.T) {
	st, clk := openTest(t)
	b := hello(t, st, "sess-b", "bravo", "/wt/b")
	hello(t, st, "sess-a", "alpha", "/wt/a")
	if err := st.MarkIdle("sess-a", clk.t, clk.t); err != nil {
		t.Fatal(err)
	}
	clk.advance(time.Minute)
	id, err := st.Send(mustResolve(t, st, "sess-a"), "bravo", "hi", SendOpts{SenderSession: b.SessionID, SenderKnown: true})
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	if _, err := st.StopHold("sess-a", "alpha", clk.t, clk.t, true, func([]InboxMsg, bool) ([]int64, bool, error) {
		return []int64{id}, true, boom
	}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if q, _ := st.Undelivered("sess-a", "alpha"); len(q) != 1 {
		t.Errorf("a failed body must deliver nothing: %d queued", len(q))
	}
	if !idleRowExists(t, st, "sess-a") {
		t.Error("a failed body must not retract the earlier mark")
	}
}

// idleRaw is the session's raw session_idle row, "" for none.
func idleRaw(t *testing.T, st *Store, id string) string {
	t.Helper()
	var inc string
	var since int64
	err := st.db.QueryRow(`SELECT incarnation, since FROM session_idle WHERE session_id=?`, id).Scan(&inc, &since)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%s@%d", inc, since)
}
