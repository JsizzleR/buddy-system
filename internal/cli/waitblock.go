package cli

// `buddy wait --on <slug> --block` (D-095, amending D-033): declare the wait,
// then stay running until its verdict, and exit with it.
//
// THE FAILURE. One fleet of 144 sessions wrote 15+ hand-rolled poll loops —
// `for i in $(seq 1 120); do buddy ls | grep -q <slug> || exit 0; sleep 60;
// done` — next to a declared `wait --on` that recorded 0 checks, plus 6
// `until buddy ls | grep ... | grep -q <its own id>` loops waiting for a
// handover, one of which fired on its own WAITING line. The loops were run
// under the harness's run_in_background because its completion notice is the
// one wake a parked session reliably gets; what they lacked was a verdict.
// The operator decided (2026-10-20) to give that loop to buddy.
//
// THE RULE. The declaration is exactly `wait`'s, printed the same. Then the
// process re-reads the ledger every waitBlockPoll and exits:
//   0  LANDED — released, an outcome reported, or handed to you (D-093)
//   3  EXPIRED
//   4  the wait was cleared (`wait clear`) or replaced by a newer declaration
//   5  the session ended or re-registered: the wait went with it
// printing the line `wait check` prints for that verdict (landedLines,
// expiredLines), then closing the row as a check does, after the write. An
// error (an unreadable ledger) is exit 1 like every verb's.
//
// WHAT IT IS NOT. A wake: buddy still wakes nothing; the harness notices its
// own job ending. A keep-alive check: each read is an autocommit SELECT, so
// `last_check` and `checks` stay the keep-alive's own (D-033), and a session
// that parks longer than the cache lasts still arms `/loop buddy wait check`
// for the cache — the block keeps no cache warm. A write lock: nothing is
// held across a sleep; the one write a poll can make is cleanupEnded's, in its
// own short transaction, and only when a read shows an awaited holder that has
// said bye (without it the block would sit on that claim until its deadline,
// the failure cleanupEnded's header names). It stops on the session's END as
// the ledger records it (bye, or a new incarnation); a harness that dies
// without a bye leaves it running until the deadline, which the 12h ceiling
// bounds. Watching the parent process was not done: how run_in_background
// parents its shell was not measured, and a guess there exits early.

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

// waitBlockPoll is the pause between reads. The measured hand-rolled loops
// polled every 20 s and 60 s; 15 s is under both, and a harness completion
// notice is read at the session's next turn anyway, so a shorter pause buys
// nothing a reader would see. A read is one indexed SELECT of a few rows:
// 240 an hour, against the 12h ceiling at most 2,880.
const waitBlockPoll = 15 * time.Second

// The exit statuses (see the header). 1 is every verb's error and 2 Run's
// usage, so the verdicts start at 3.
const (
	blockLanded  = 0
	blockExpired = 3
	blockCleared = 4
	blockEnded   = 5
)

// waitExit carries a verdict's exit status out through errVerb, which knows
// only "error or not". It is never printed: the verdict line was.
type waitExit struct{ code int }

func (e waitExit) Error() string { return "wait --block exit " + strconv.Itoa(e.code) }

// waitVerb is the table's entry for `wait`: heardVerb's, plus the verdict
// statuses `--block` exits with.
func waitVerb(args []string, env Env) int {
	code := 0
	rc := heardVerb("wait", func(a []string, e Env) error {
		err := cmdWait(a, e)
		var we waitExit
		if errors.As(err, &we) {
			code = we.code
			return nil
		}
		return err
	})(args, env)
	if rc != 0 {
		return rc
	}
	return code
}

func (e Env) sleep(d time.Duration) {
	if e.Sleep != nil {
		e.Sleep(d)
		return
	}
	time.Sleep(d)
}

// blockOn polls the declaration `w` until it has a verdict, and returns that
// verdict's waitExit (nil for LANDED). The declaration's own `decl` is the
// key: a newer declaration is a different wait, and this block was not asked
// to wait on it.
func blockOn(st *store.Store, env Env, si store.SessionInfo, w store.Wait) error {
	fmt.Fprintf(env.Stdout, "BLOCKING until it lands: the ledger is re-read every %s (reads only; not a check) — exit 0 LANDED, %d EXPIRED, %d cleared or replaced, %d this session ended\n",
		span(waitBlockPoll), blockExpired, blockCleared, blockEnded)
	stop := "stop any /loop that runs `buddy wait check` (schedule no further check)"
	for {
		env.sleep(waitBlockPoll)
		now := nowOf(env)
		cur, ok, err := st.SessionByID(si.SessionID)
		if err != nil {
			return err
		}
		if !ok || !cur.Live() || cur.Incarnation != si.Incarnation {
			fmt.Fprintf(env.Stdout, "ENDED: session %s ended or re-registered while blocking, and its wait went with it — %s\n",
				fence.Line(si.Label, 64), stop)
			return waitExit{blockEnded}
		}
		row, ok, err := st.WaitOf(si.SessionID)
		if err != nil {
			return err
		}
		if !ok || row.Decl != w.Decl || row.Incarnation != si.Incarnation {
			fmt.Fprintf(env.Stdout, "REPLACED: a newer declaration replaced the wait this block was waiting on; the new one has its own verdict — %s\n", stop)
			return waitExit{blockCleared}
		}
		if row.IsOpen() && pendingOnEnded(row) {
			// A holder that said bye: the same cleanup a check runs, then
			// read again so the verdict is of the settled ledger.
			if err := st.SettleEnded(); err != nil {
				return err
			}
			if row, _, err = st.WaitOf(si.SessionID); err != nil {
				return err
			}
			if row.Decl != w.Decl {
				continue // replaced in between; the next read says so
			}
		}
		verdict := row.Verdict(now)
		if !row.IsOpen() {
			switch row.Reason {
			case "landed":
				verdict = store.WaitLanded
			case "expired":
				verdict = store.WaitExpired
			case "cleared":
				fmt.Fprintf(env.Stdout, "CLEARED: the wait was withdrawn %s ago (`buddy wait clear`) — %s\n", span(now.Sub(row.Cleared)), stop)
				return waitExit{blockCleared}
			default:
				fmt.Fprintf(env.Stdout, "ENDED: the wait closed with the session's run — %s\n", stop)
				return waitExit{blockEnded}
			}
		}
		var line string
		var exit error
		switch verdict {
		case store.WaitPending:
			continue
		case store.WaitLanded:
			line = landedLines(now, row, stop)
		default:
			line, exit = expiredLines(now, row, stop), waitExit{blockExpired}
		}
		if _, err := io.WriteString(env.Stdout, line); err != nil {
			return err
		}
		// Closed after the verdict was written, as a check closes it, and
		// keyed to the declaration; a lost close only lets a check say it
		// again.
		if row.IsOpen() {
			reason := "landed"
			if verdict == store.WaitExpired {
				reason = "expired"
			}
			_, _ = st.CloseWait(si.SessionID, row.Decl, reason)
		}
		return exit
	}
}

// pendingOnEnded reports whether a target still being waited on is held by a
// session that has said bye: cleanupEnded would orphan it, landing the wait.
func pendingOnEnded(w store.Wait) bool {
	for _, t := range w.Targets {
		if t.Pending() && !t.Holder.Ended.IsZero() {
			return true
		}
	}
	return false
}
