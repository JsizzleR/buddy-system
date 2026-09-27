package cli

import (
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

// `buddy sent` (D-043, issue #34, wishlist §4): what became of messages you
// sent. The coordinator in the 2026-09-20 run broadcast seven wrong
// measurements and could not answer "who has seen the correction?". This
// answers the part the ledger knows: for every session a message addressed,
// whether a delivery is RECORDED (a hook wrote it into that session's context
// and marked it), it is still queued, or it expired undelivered (a broadcast
// past its 24h keep). A correction also shows each session's standing with the
// original, which is the row a coordinator acts on: whoever had the wrong
// number and does not yet have the right one.
//
// A REPORT, like `who`: it grants nothing and prints no body. "Recorded
// delivery" is the word and never "read" or "seen" (D-032): it is evidence of
// a write into context. A write whose mark then failed is redelivered, and
// reads here as queued until it is.
//
// A QUEUED ROW NAMES ITS WAKE (issue #42's second half). Measured: four
// recipients sat idle 45–54 minutes with a message queued, and the sender
// found out only by running `sent` and `sessions` and putting the two
// together by hand. So a recipient that still has the message queued, and
// that `msg`'s own rule says needs a wake (D-039: quiet, one live harness
// process, its socket there), gets the same wake clause here, by the same
// function on a fresh observation. The issue asked for "after N minutes";
// no threshold was built, because `msg` prints the same clause at age zero
// and a threshold would only hide a fact the sender can act on now.

const maxSentList = 10

func cmdSent(args []string, env Env) error {
	fs := flag.NewFlagSet("sent", flag.ContinueOnError)
	if help, err := parseFlags(fs, args, usageSent, env); help || err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return fencedErr(fmt.Errorf("sent takes at most one message id; %s", usageSent))
	}
	st, _, err := mustLedger(env.Cwd, env)
	if err != nil {
		return err
	}
	defer st.Close()
	now := nowOf(env)
	w := waker{st: st, env: env, now: now, memo: map[string]string{}}

	if fs.NArg() == 1 {
		id, err := strconv.ParseInt(strings.TrimPrefix(fs.Arg(0), "#"), 10, 64)
		if err != nil || id <= 0 {
			return fencedErr(fmt.Errorf("%q is not a message id (the #N a send prints)", fs.Arg(0)))
		}
		si, ok, err := st.Sent(id)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("no message #%d", id)
		}
		printSent(env, si, now, true, w)
		return nil
	}

	// No id: this sender's newest sends. Identity is the SENDING session, the
	// same resolution a correction's ownership uses; an unresolvable one is
	// refused rather than shown the operator's list.
	sid, known := senderSession(st, env)
	if !known {
		return fmt.Errorf("cannot tell which session is asking; name a message: %s", usageSent)
	}
	list, err := st.SentBy(sid, maxSentList)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintln(env.Stdout, "no messages sent by you are recorded (senders are recorded from D-043 on)")
		return nil
	}
	for _, si := range list {
		printSent(env, si, now, false, w)
	}
	return nil
}

// printSent renders one report: a header line and, when rows is set, one row
// per addressed session. The sender tag and labels are peer-controlled and
// fenced; the target of a direct message is shown by its recipient's label.
//
// A wake rides the row of the session it wakes. Without rows (the list), a
// direct message carries its one wake on the header line, and a broadcast
// counts its wakeable sessions and points at the report that names them, so
// one send stays one line.
func printSent(env Env, si store.SentInfo, now time.Time, rows bool, w waker) {
	to := "all"
	if si.Target != store.AllTarget {
		to = si.Target
		if len(si.Recipients) == 1 && si.Recipients[0].Label != "" {
			to = si.Recipients[0].Label
		}
	}
	head := fmt.Sprintf("#%d to %s from %s, %s ago — %d of %d with a recorded delivery",
		si.ID, fence.Line(to, 64), fence.Line(si.Sender, 64), age(now, si.Created),
		deliveredCount(si), len(si.Recipients))
	if d := declaredKind(si.Kind, si.KindNote); d != "" {
		head += "; " + d
	}
	if si.Supersedes != 0 {
		head += fmt.Sprintf("; corrects #%d", si.Supersedes)
	}
	if len(si.SupersededBy) > 0 {
		by := make([]string, len(si.SupersededBy))
		for i, k := range si.SupersededBy {
			by[i] = "#" + strconv.FormatInt(k, 10)
		}
		head += "; superseded by " + strings.Join(by, ", ")
	}
	if !rows {
		var wakes []string
		for _, r := range si.Recipients {
			if wk := w.wake(r, si.ID); wk != "" {
				wakes = append(wakes, wk)
			}
		}
		switch {
		case len(wakes) == 0:
		case si.Target != store.AllTarget:
			head += "; " + wakes[0]
		default:
			head += fmt.Sprintf("; %d addressed session(s) still have it queued and can be woken: buddy sent %d names each wake address", len(wakes), si.ID)
		}
		fmt.Fprintln(env.Stdout, head)
		return
	}
	fmt.Fprintln(env.Stdout, head)
	for _, r := range si.Recipients {
		label := r.Label
		if label == "" {
			label = r.SessionID
		}
		line := fmt.Sprintf("  %-24s %s", fence.Field(label, 64), deliveryWord(r.Delivery, now))
		if si.Supersedes != 0 {
			line += fmt.Sprintf("   #%d: %s", si.Supersedes, deliveryWord(r.Original, now))
		}
		if wk := w.wake(r, si.ID); wk != "" {
			line += "; " + wk
		}
		fmt.Fprintln(env.Stdout, line)
	}
}

// waker answers, for one addressed session, the D-039 wake clause if it
// still has message id queued, else "". One observation per session per
// report: the list names up to ten sends, and a broadcast addresses every
// live session, so the process register is probed once for each. The memo
// holds the ADDRESS, not the clause: the text names each message's own id
// (D-059), so two sends to one session are two different wakes.
type waker struct {
	st   *store.Store
	env  Env
	now  time.Time
	memo map[string]string
}

func (w waker) wake(r store.SentRecipient, id int64) string {
	if !r.Delivered.IsZero() || r.Expired {
		return ""
	}
	addr, seen := w.memo[r.SessionID]
	if !seen {
		addr = wakeAddr(w.env, observe(w.st, w.env, r.SessionID), w.now)
		w.memo[r.SessionID] = addr
	}
	return wakeFor(addr, id, w.now)
}

// deliveryWord is one message's standing with one session, in the ledger's
// words.
func deliveryWord(d store.Delivery, now time.Time) string {
	switch {
	case !d.Delivered.IsZero():
		return "delivery recorded " + age(now, d.Delivered) + " ago"
	case d.Expired:
		return "expired undelivered"
	default:
		return "queued"
	}
}

func deliveredCount(si store.SentInfo) int {
	n := 0
	for _, r := range si.Recipients {
		if !r.Delivered.IsZero() {
			n++
		}
	}
	return n
}
