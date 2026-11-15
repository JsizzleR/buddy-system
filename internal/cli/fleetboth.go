package cli

// A SESSION ON BOTH LEDGERS (NON-GIT-PLAN stage 2, D-193; D-191 is stage 1b).
//
// WHAT CHANGED. In 1b a session whose directory has its own ledger used that
// ledger alone, and BUDDY_FLEET was "not used here": a coordinator living in a
// governed repository (the operator's existing orchestrator pane, the plan's
// question 4) could not coordinate a fleet, and a fleet lane that cd'd into a
// repository dropped off its fleet's roster (its row went GONE with its slot
// claim open, and the coordinator's wait on it never landed: the plan's 9.1
// row closed only by this stage). Now the FIVE LIFECYCLE HOOKS keep the
// session on both: where the filesystem finds a home ledger and BUDDY_FLEET
// names a fleet, hello registers in both, beat and busy heartbeat both and
// drain both inboxes into their one document, idle marks both (or holds the
// turn for mail from either), and bye ends both rows. Nothing else changed:
// the typed verbs still act in the directory's ledger (5.2, step 1), and
// `buddy fleet <verb>` is how a session acts in its fleet from there.
//
// THE GATE STILL NEVER OPENS ONE (5.4). besideOf is called by the lifecycle
// hooks and beatOffSuccess only; it is in fleet_reach_test.go's forbidden list
// with openFleetLedger, and `fleet` (cmdFleetVerb) reaches the fleet only
// through openSessionLedger, which the gate never calls. No brake in a fleet,
// no row crosses between the two ledgers, no schema change.
//
// WHAT THE FLEET HALF NEVER DOES TO THE HOME. Each ledger keeps its own
// (session_id, incarnation), its own session_procs fence for bye, and its own
// idle fence. A fleet that does not open, or fails after it opened, leaves the
// home half exactly as it was and the hook's result the home half's (D4 of the
// design): hello says so in its digest, busy at every prompt (5.2's one line,
// worded for a session the home ledger still serves), and beat, idle and bye
// say nothing. A home ledger that cannot be read is today's verdict, and the
// fleet is not opened at all (D3).
//
// REJECTED, each in the design pass (codex@xhigh): a typed `join` writing a
// by-session file (the plan's option S; D-191 rejected it for 1b, and here its
// lookup would run on every hook of every session on the machine, `/clear`
// would silently drop a joined session while the variable survives `/clear`,
// and it is a second state outside the ledger that chooses which ledger a hook
// opens); the plan's hand path, `(cd <fleet dir> && buddy <verb>)`, as the
// taught route (finding 1: with $HOME a governed worktree, discovery finds
// THAT ledger from the fleet's directory and `release` acts there); home mail
// first and fleet mail in what is left (finding 3: two large home messages
// before every drain kept a large fleet message out for good); a fleet drain
// nested inside the home's admitted Stop (finding 2: it skipped the fleet's own
// incarnation fence).

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

// fleetBeside is the fleet half of a session on both ledgers: the fleet ledger
// opened beside the home, or why it did not open (err, with st nil).
type fleetBeside struct {
	st  *store.Store
	rc  repoContext
	err error
}

// besideOf opens the fleet BUDDY_FLEET names BESIDE a home ledger the
// filesystem found, for the lifecycle hooks only. nil, meaning no fleet half
// at all, when BUDDY_FLEET is unset or empty (today, byte for byte) or rc is a
// fleet's already (1b: no home here).
//
// ONE FILE IS NOT TWO LEDGERS (the design pass, Q2). A repository whose
// ledger resolves to the fleet's own file would otherwise be opened twice,
// and the joint drain's two nested write transactions would wait on each other
// until busy_timeout. Compared by the files SQLite opened (MainFile), not the
// paths asked for. That fleet is unavailable beside this home, with that
// cause; the home half runs as today. Since D-194 the repository side refuses
// both spellings this was written for (a symlinked .git/buddy.db, in
// openLedgerAt's fleet-alias guard; the %XX spelling, in store.Open's escape)
// before this runs, so this is the backstop for the window between that guard
// and the opens (a link made, or the fleet home made inspectable, in between).
func besideOf(home *store.Store, rc repoContext, env Env) *fleetBeside {
	if rc.fleet != "" || env.getenv(EnvFleet) == "" {
		return nil
	}
	st, frc, err := openFleetLedger(env)
	if fe := (fleetErr{}); errors.As(err, &fe) {
		fe.beside = true
		err = fe
	}
	if err == nil && sameLedgerFile(home, st) {
		st.Close()
		st = nil
		err = fleetErr{kind: fleetUnavailable, name: frc.fleet, path: frc.ledger, beside: true,
			cause: errors.New("it is the same file as this directory's own ledger")}
	}
	return &fleetBeside{st: st, rc: frc, err: err}
}

// sameLedgerFile reports whether two open stores are one file. A file that
// cannot be named or stat'ed is not known to be the same, and the fleet opens:
// the worst that costs is the busy_timeout wait this guards against.
func sameLedgerFile(a, b *store.Store) bool {
	fa, err := a.MainFile()
	if err != nil {
		return false
	}
	fb, err := b.MainFile()
	if err != nil {
		return false
	}
	sa, err := os.Stat(fa)
	if err != nil {
		return false
	}
	sb, err := os.Stat(fb)
	if err != nil {
		return false
	}
	return os.SameFile(sa, sb)
}

// ok reports a fleet half that is open and has not failed.
func (fb *fleetBeside) ok() bool { return fb != nil && fb.st != nil }

func (fb *fleetBeside) close() {
	if fb.ok() {
		fb.st.Close()
	}
}

// fail is a fleet failure after the open: the fleet is closed, never touched
// again by this hook, and said to be unavailable where hello and busy speak.
func (fb *fleetBeside) fail(err error) {
	if !fb.ok() {
		return
	}
	fb.st.Close()
	fb.st = nil
	fe := fleetOpsErr(fb.rc, err).(fleetErr)
	fe.beside = true
	fb.err = fe
}

// line is the fleet's 5.2 sentence when it did not open or failed after, ""
// otherwise (and for no fleet half at all).
func (fb *fleetBeside) line() string {
	if fb == nil || fb.err == nil {
		return ""
	}
	return fleetLine(fb.err)
}

// name is the fleet's name, for the lines that name it; "" with no fleet half
// (hello renders an empty fleet block through it).
func (fb *fleetBeside) name() string {
	if fb == nil {
		return ""
	}
	return fb.rc.fleet
}

// labelOf is the label a session's mail may be addressed to in st, "" when
// st does not know the session.
func labelOf(st *store.Store, id string) string {
	if si, known, err := st.SessionByID(id); err == nil && known {
		return si.Label
	}
	return ""
}

// besideHelloLine is hello's line for the fleet beside the home: where it
// stands and how to act in it. It replaces 1b's "is not used here".
func besideHelloLine(fb *fleetBeside, si store.SessionInfo) string {
	if !fb.ok() {
		return fb.line()
	}
	return fmt.Sprintf("BUDDY: also session %s in fleet %s, a coordination-only ledger with no file claim, gate or brake: its mail and waits ride this session's hooks. A bare verb acts in this directory's ledger; act in the fleet with `buddy fleet <verb> ...` (its claims are slots: --scope .buddy/slot/<item>)\n",
		fence.Line(si.Label, 64), fence.Line(fb.name(), 64))
}

// fleetInboxLead is buddy's own line ahead of a fleet's messages in a joint
// drain. It names the ledger the ids belong to (each ledger numbers its own,
// so #12 can be in both) and where an answer goes: a bare `buddy msg` from a
// governed directory is the home ledger's, where a fleet sender is a miss.
// Written after the home block, it cannot be forged by a message: every
// message line is fenced to one line and starts with its id (invariant 9).
func fleetInboxLead(name string) string {
	return fmt.Sprintf("BUDDY: the messages below are fleet %s's (its own ids); answer them with `buddy fleet msg <target> ...`, not a bare msg.\n", fence.Line(name, 64))
}

// fleetLeadMax is fleetInboxLead's worst case, with the long header that
// follows it: what a joint drain holds back for the fleet's block.
var fleetLeadMax = len(fleetInboxLead(strings.Repeat("x", 64))) + len(inboxHeader)

// writeFleetInbox renders a fleet's messages after its lead line, and
// returns their ids. Nothing for no messages.
func writeFleetInbox(b *strings.Builder, name string, msgs []store.InboxMsg, now time.Time) []int64 {
	if len(msgs) == 0 {
		return nil
	}
	b.WriteString(fleetInboxLead(name))
	return writeInbox(b, msgs, now)
}

// mergeDrain picks what ONE drain shows from two ledgers' queued mail: both
// lists merged OLDEST FIRST, a tie going to the home, then boundDrain's bound
// (20 messages, about 8 KiB, never fewer than one) and beatDrain's room (stop
// at the first message that does not fit, never skipping ahead) over the
// merged order.
//
// WHY MERGED (the design pass, finding 3). Home first and the fleet in what is
// left starved the fleet for good: two 3,000-byte home lines queued before
// every drain left no room for one 4,096-byte fleet body. Merged by age, the
// oldest message of either ledger is always next in line, so neither can be
// kept out longer than the mail queued ahead of it.
func mergeDrain(home, fleet []store.InboxMsg, room int, now time.Time) (sh, sf []store.InboxMsg) {
	i, j, total := 0, 0, 0
	for i < len(home) || j < len(fleet) {
		if len(sh)+len(sf) == maxDrainMsgs {
			break
		}
		fromFleet := i >= len(home) || (j < len(fleet) && fleet[j].Created.Before(home[i].Created))
		m := store.InboxMsg{}
		if fromFleet {
			m = fleet[j]
		} else {
			m = home[i]
		}
		n := len(inboxLine(m, nil, now))
		if total += n; total > maxDrainBytes && len(sh)+len(sf) > 0 {
			break
		}
		if room -= n; room < 0 {
			break
		}
		if fromFleet {
			sf = append(sf, m)
			j++
		} else {
			sh = append(sh, m)
			i++
		}
	}
	return sh, sf
}

// drainBoth is drainHook for a session on both ledgers: ONE hook document,
// prefix (buddy's notices) then the home's mail and the fleet's, merged by
// mergeDrain inside room. It returns what drainHook returns, for the HOME:
// wrote says the document went out, err is the home's failure. A fleet failure
// is never the hook's: it costs the fleet's mail this event and nothing else.
//
// LOCKS, AND WHY THE FLEET IS OUTER (the design pass, Q2 and finding 4). Each
// ledger's read and mark stay under its own write lock (store.Drain, D-099),
// so two drains are two nested transactions. A lock is taken only on a ledger
// whose plain read found mail, so nesting happens only when both have some.
// The order is always fleet, then home, and nothing takes a repository's
// lock and then a fleet's, so there is no cycle; and the outer lock is the one
// held while waiting, so a contended home stalls fleet writers rather than a
// contended fleet stalling a repository's (claims, beats).
//
// THE FAILURE PROTOCOL (finding 4):
//   - the fleet's transaction never began (busy, an error before its body):
//     today's drainHook on the home, exactly;
//   - the home failed before the write: the home's error, and the fleet,
//     whose body returns it, marks nothing either;
//   - the document went out and the home's commit failed: the home's error
//     (its mail comes again, at least once, as today), and the fleet still
//     marks what was written;
//   - the fleet's commit failed after the write: its mail comes again; the
//     home is unaffected.
func drainBoth(home *store.Store, hlabel string, fb *fleetBeside, id string, env Env, event, prefix string, bound func([]store.InboxMsg) []store.InboxMsg, room int) (wrote bool, err error) {
	now, prefix0 := nowOf(env), prefix
	homeOnly := func() (bool, error) {
		// Bounded by room as well as by bound: bound (boundDrain's 20
		// messages and 8 KiB) ignores the prefix, which beside a home can
		// carry the fleet's sentence with a path of up to 512 bytes, and the
		// document then passed Claude Code's 10,000-character preview with
		// every message in it marked delivered (the Codex code pass, finding
		// 1). room was taken from the prefix the caller passed; a line added
		// here since is subtracted. The fleet's lead the caller held back is
		// given back, since this document carries no fleet block: beat's room
		// is then beatDrain's own and its drain today's byte for byte (held
		// back, one home message fewer fit beside an empty fleet inbox than
		// with the variable unset: TestBothLedgersEmptyFleetInboxIsTodaysDrain,
		// measured red), while busy's, which today bounds by count and bytes
		// alone, now stops short of helloBudget less its note.
		r := room + fleetLeadMax - (len(prefix) - len(prefix0))
		// The clock is read here, inside the drain's lock, as bound reads
		// its own, and not drainBoth's now: a CORRECTS line's "reached you
		// 59s ago" is a byte longer than the "1m" bound measured a moment
		// later, and a queue that filled beat's room exactly lost its last
		// message beside an empty fleet inbox that today's drain kept (the
		// Codex re-verify, round 1, finding 1;
		// TestDrainBothHomeOnlyMeasuresAtTheDrainsTime).
		capped := func(m []store.InboxMsg) []store.InboxMsg {
			sh, _ := mergeDrain(bound(m), nil, r, nowOf(env))
			return sh
		}
		return drainHook(home, id, hlabel, env, event, prefix, capped)
	}
	// failed closes a fleet that failed after it opened and, at the prompt,
	// names it: busy's line "at every prompt" (D4) held only for a fleet that
	// never opened (the panel's gate lens, P3: a fleet whose inbox read
	// failed was silent beside a home; the Codex code pass, finding 2: so was
	// one whose drain could not take its lock). beat stays silent (D4).
	failed := func(err error) {
		fb.fail(err)
		if event == "UserPromptSubmit" {
			prefix += fb.line()
		}
	}
	if !fb.ok() {
		return homeOnly()
	}
	flabel := labelOf(fb.st, id)
	fq, ferr := fb.st.Undelivered(id, flabel)
	if ferr != nil {
		failed(ferr) // homeOnly reads prefix when it runs
		return homeOnly()
	}
	if len(fq) == 0 {
		return homeOnly()
	}
	hq, err := home.Undelivered(id, hlabel)
	if err != nil {
		return false, err
	}
	var ran bool
	var homeErr, writeErr error
	write := func(hm, fm []store.InboxMsg) (hids, fids []int64, err error) {
		sh, sf := mergeDrain(hm, fm, room, now)
		if len(sh)+len(sf) == 0 && prefix == "" {
			return nil, nil, nil
		}
		var b strings.Builder
		b.WriteString(prefix)
		hids = writeInbox(&b, sh, now)
		fids = writeFleetInbox(&b, fb.name(), sf, now)
		if err := writeHookContext(env, event, b.String()); err != nil {
			writeErr = err
			return nil, nil, err
		}
		wrote = true
		return hids, fids, nil
	}
	derr := fb.st.Drain(id, flabel, func(fm []store.InboxMsg) ([]int64, error) {
		ran = true
		if len(hq) == 0 {
			// Only the fleet has mail: its lock alone, the home's notices
			// riding its document.
			_, fids, err := write(nil, fm)
			return fids, err
		}
		var fids []int64
		herr := home.Drain(id, hlabel, func(hm []store.InboxMsg) ([]int64, error) {
			hids, f, err := write(hm, fm)
			fids = f
			return hids, err
		})
		homeErr = herr
		if herr != nil && !wrote {
			return nil, herr
		}
		return fids, nil
	})
	switch {
	case writeErr != nil:
		// Nothing was marked in either ledger: both drains come again. Never
		// a second write, which could put two documents on one event.
		return false, writeErr
	case !ran:
		// The fleet's transaction never ran its body (its lock busy past
		// busy_timeout, or an error before it): the home's own path, with
		// the fleet named at the prompt.
		if derr != nil {
			failed(derr)
		}
		return homeOnly()
	case !wrote && len(hq) == 0:
		// The fleet-only drain wrote nothing (a concurrent drain took the
		// mail): the home's own path, which still writes the notices.
		return homeOnly()
	}
	return wrote, homeErr
}

// stopBoth is the Stop for a session on both ledgers: each ledger's fence,
// mail read and idle mark in its own transaction (store.StopHold), nested
// fleet outer and home inner (drainBoth's order and its failure protocol), and
// ONE decision between them: mail from either that fits goes out in one
// `decision: block` and neither row is marked idle, since the turn goes on;
// otherwise both are marked. Each fence is the ledger's own (the design pass,
// finding 2): a ledger that refuses this Stop (a late predecessor's turn, or a
// session it never registered, Q7) marks and drains nothing of its own, and
// the other's mail can still hold the turn.
//
// IT ALWAYS NESTS, unlike drainBoth: both ledgers are written on every Stop
// (an idle mark or its retraction), and "held" is one decision over both, so
// a contended home holds the fleet's lock for up to busy_timeout (measured by
// the panel's concurrency lens: 15.3 s for one idle with the home locked).
// Two sequential transactions would split that decision: a home already
// marked idle while fleet mail read under the second lock holds the turn.
// Accepted, as drainBoth's order is: a repository's lock held that long
// already stalls every hook in that repository.
func stopBoth(home *store.Store, fb *fleetBeside, h hookInput, env Env, since, fenceAt time.Time) error {
	id := h.SessionID
	hlabel, flabel := labelOf(home, id), labelOf(fb.st, id)
	now := nowOf(env)
	drain := !h.StopHookActive
	room := helloBudget - len(stopLead) - len(inboxHeader) - fleetLeadMax
	var ran, wrote bool
	var homeErr, writeErr error
	// The fleet's own error is never the hook's (D4). Before its body (its
	// lock past busy_timeout, its fence or read failing) ran stays false and
	// the home's Stop runs alone, below; after the document went out, a
	// failed fleet commit leaves the fleet's mail queued, and it comes again
	// once: the at-least-once every drain accepts (the cross-check asked for
	// these to be named where they happen).
	_, _ = fb.st.StopHold(id, flabel, since, fenceAt, drain, func(fm []store.InboxMsg, _ bool) ([]int64, bool, error) {
		ran = true
		var fids []int64
		_, herr := home.StopHold(id, hlabel, since, fenceAt, drain, func(hm []store.InboxMsg, _ bool) ([]int64, bool, error) {
			sh, sf := mergeDrain(hm, fm, room, now)
			if len(sh)+len(sf) == 0 {
				return nil, false, nil
			}
			var b strings.Builder
			b.WriteString(stopLead)
			hids := writeInbox(&b, sh, now)
			f := writeFleetInbox(&b, fb.name(), sf, now)
			enc, err := json.Marshal(map[string]any{"decision": "block", "reason": b.String()})
			if err == nil {
				_, err = env.Stdout.Write(append(enc, '\n'))
			}
			if err != nil {
				writeErr = err
				return nil, false, err
			}
			wrote, fids = true, f
			return hids, true, nil
		})
		homeErr = herr
		if herr != nil && !wrote {
			return nil, false, herr
		}
		// herr with wrote: the home's commit failed after the document went
		// out, so its mail comes again once while the fleet marks what was
		// written, drainBoth's protocol for the same case.
		return fids, wrote, nil
	})
	if !ran {
		// The fleet's transaction never began: today's Stop on the home.
		return stopTurn(home, h, env, since, fenceAt)
	}
	if writeErr != nil {
		// The write failed, so nothing was marked and the turn is not held:
		// the session IS resting with its mail queued, in both ledgers, as
		// stopTurn records it for one.
		_ = home.MarkIdle(id, since, fenceAt)
		_ = fb.st.MarkIdle(id, since, fenceAt)
		return writeErr
	}
	if homeErr != nil && !wrote {
		// The home failed before any document (its lock busy past
		// busy_timeout, its fence or read erred), and the fleet's body
		// returned that error, so the fleet rolled back too. Nothing holds
		// the turn, so the healthy fleet gets its idle mark through its own
		// fence, and a later fleet msg names a wake (D-099): left unmarked,
		// it named none (the panel's concurrency lens, finding 1, reproduced
		// by holding the home's write lock). The home's error is the hook's,
		// as today.
		_ = fb.st.MarkIdle(id, since, fenceAt)
	}
	return homeErr
}

// beatBeside is beat's fleet half: the heartbeat with its process, the
// footprint, a browser call, and the one-shot LANDED line of a wait declared in
// the fleet. Never a dirty path or an authority notice: the fleet has no root
// (D-191's skips). A subagent's call beats and composes nothing to deliver
// (D-099). Every failure is the fleet's: the fleet is closed and the beat
// goes on for the home.
func beatBeside(fb *fleetBeside, h hookInput, proc store.ProcRef, sub bool, env Env) (landed string, commit func() error) {
	commit = func() error { return nil }
	if !fb.ok() {
		return "", commit
	}
	if err := fb.st.BeatFrom(h.SessionID, "", proc); err != nil {
		fb.fail(err)
		return "", commit
	}
	me, known, err := fb.st.SessionByID(h.SessionID)
	if err != nil {
		fb.fail(err)
		return "", commit
	}
	if known && me.Live() && h.Harness != "codex" {
		if u, ok := lastUsage(h.TranscriptPath); ok {
			_ = fb.st.RecordContext(h.SessionID, me.Incarnation, sampleOfUsage(u, env))
		}
	}
	if known && me.Live() && isBrowserTool(h.ToolName) {
		_ = fb.st.RecordBrowserUse(h.SessionID, me.Incarnation)
	}
	if known && me.Live() && !sub {
		return waitNotice(fb.st, me, nowOf(env), "buddy fleet")
	}
	return "", commit
}

// busyBeside is busy's fleet half before the drain: the idle mark retracted
// and the footprint resampled, as busyTurn does for the home. The handoff line
// is the home's alone (one line a turn). It reports whether the fleet knows
// the session, for the roster note.
func busyBeside(fb *fleetBeside, h hookInput, env Env) (known bool) {
	if !fb.ok() {
		return false
	}
	if err := fb.st.ClearIdle(h.SessionID); err != nil {
		fb.fail(err)
		return false
	}
	me, known, err := fb.st.SessionByID(h.SessionID)
	if err != nil {
		fb.fail(err)
		return false
	}
	if h.Harness != "codex" && !h.subagent() {
		resampleFootprint(fb.st, h, me, known, env)
	}
	return known
}

// busyBesideNote is busy's notice for the fleet beside the home: its 5.2
// sentence at every prompt while it does not open (the line 1b printed only
// for a session with no home), or the roster line for a session the opened
// fleet does not know.
func busyBesideNote(fb *fleetBeside, h hookInput, known bool, env Env) string {
	if fb == nil {
		return ""
	}
	if !fb.ok() {
		return fb.line()
	}
	return busyRosterNote(fb.rc, h.SessionID, known, env, true)
}

// byeBeside ends the session's fleet row beside its home (D-191 left it to
// this stage: L1), fenced by the fleet's own session_procs exactly as the
// home's bye is by its own. Only the session's own SessionEnd calls it, which
// never forces (a hand-run bye is the home's alone: cmdBye). Silent in every
// arm: the home's verdict is the hook's.
func byeBeside(fb *fleetBeside, session string, from store.ProcRef, env Env) {
	if !fb.ok() {
		return
	}
	_, _ = fb.st.ByeFrom(session, from, env.procAlive, false)
}

// helloBeside registers the session in the fleet beside its home (worktree
// column: the home's root, shown and never used to place a path) and returns
// its row. A failure here is the fleet's: fb is marked failed and hello's
// digest carries the sentence.
func helloBeside(fb *fleetBeside, session, label, top string, proc store.ProcRef, terminal string) store.SessionInfo {
	if !fb.ok() {
		return store.SessionInfo{}
	}
	si, err := fb.st.HelloFrom(session, label, top, proc, terminal)
	if err != nil {
		fb.fail(err)
	}
	return si
}

// besideTwinLine is hello's label-worn-twice warning for the fleet (issue
// #17's, cli.go): a label two live fleet sessions wear is refused as
// ambiguous by every `buddy fleet msg` addressed to it. "" otherwise.
func besideTwinLine(fb *fleetBeside, si store.SessionInfo) string {
	if !fb.ok() {
		return ""
	}
	all, err := fb.st.Sessions(store.ByLastSeen)
	if err != nil {
		return ""
	}
	twins := 0
	for _, o := range all {
		if o.Live() && o.Label == si.Label && o.SessionID != si.SessionID {
			twins++
		}
	}
	if twins == 0 {
		return ""
	}
	return fmt.Sprintf("BUDDY: WARNING your label %s is also worn by %d other live session(s) in fleet %s; a msg addressed to that label there is REFUSED as ambiguous — have peers address you by full session id\n",
		strconv.Quote(fence.Line(si.Label, 64)), twins, fence.Line(fb.name(), 64))
}

// besideWaitLines is hello's restatement of a wait the session declared in
// the fleet (waitHelloLines, D-033), under one line naming whose it is: the
// lines read "you are waiting" and would otherwise be taken for the home's.
func besideWaitLines(fb *fleetBeside, si store.SessionInfo, now time.Time, codex bool) string {
	if !fb.ok() {
		return ""
	}
	w := waitHelloLines(fb.st, si, now, codex, "buddy fleet")
	if w == "" {
		return ""
	}
	return fmt.Sprintf("BUDDY: in fleet %s (check or clear it with `buddy fleet wait ...`):\n", fence.Line(fb.name(), 64)) + w
}

// fleetClaimsHeader heads the fleet's claims in hello's digest.
func fleetClaimsHeader(name string) string {
	return fmt.Sprintf("BUDDY fleet %s live claims (slots; act on them with `buddy fleet ...`):\n", fence.Line(name, 64))
}

// helloBesideClaims renders the fleet's claims for the digest inside room
// (writeHelloClaims's own bound and remainder line), "" when the fleet has
// none or failed.
func helloBesideClaims(fb *fleetBeside, si store.SessionInfo, now time.Time, room int, env Env) string {
	if !fb.ok() {
		return ""
	}
	claims, err := fb.st.Claims(false)
	if err != nil {
		fb.fail(err)
		return ""
	}
	if len(claims) == 0 {
		return ""
	}
	var b strings.Builder
	head := fleetClaimsHeader(fb.name())
	b.WriteString(head)
	writeClaimsList(&b, claims, si, now, room-len(head), readHolders(env, fb.st), "buddy fleet")
	return b.String()
}

// fleetCountLine is hello's "not shown here" line for the fleet's mail.
// Named by --session under Codex (an operator's shell or an adapter naming
// the session), it is addressed to THAT session, as helloCountLine's is: a
// `buddy fleet inbox` run where the line is read resolves the caller, not the
// named session (the cross-check). It names no id, so its reserve stays the
// longest fixed wording: the session runs the verb as itself.
func fleetCountLine(codex, named bool, name string, rest int) string {
	if codex && named {
		return fmt.Sprintf("BUDDY: %d queued fleet %s message(s) not shown here; no tool call delivers them under Codex: that session runs `buddy fleet inbox` as itself.\n", rest, fence.Line(name, 64))
	}
	if codex {
		return fmt.Sprintf("BUDDY: %d queued fleet %s message(s) not shown here; run `buddy fleet inbox` to read them.\n", rest, fence.Line(name, 64))
	}
	return fmt.Sprintf("BUDDY: %d queued fleet %s message(s) not shown here; they will arrive after your next tool call.\n", rest, fence.Line(name, 64))
}

// fleetCountReserve is that line's worst case, held back like
// helloCountReserve: the longest wording (Claude's tail is 6 bytes longer
// than Codex's plain one; reserving Codex's let a 59-to-64-byte fleet name
// overrun helloBudget, the panel's tests lens; the named Codex one is longer
// still) at the longest name a fleet can have.
func fleetCountReserve(queued int) int {
	x := strings.Repeat("x", 64)
	return max(len(fleetCountLine(true, true, x, queued)), len(fleetCountLine(true, false, x, queued)), len(fleetCountLine(false, false, x, queued)))
}

// usageFleet is `buddy fleet`'s usage.
const usageFleet = `usage: buddy fleet <verb> [args...]
  run one typed verb in the fleet BUDDY_FLEET names, and never in this
  directory's ledger: from a governed repository a bare verb acts in the
  repository's ledger, and this is how a session on both acts in its fleet.
  <verb> is one of: ` + fleetVerbList + `
  buddy fleet <verb> --help   that verb's usage`

// fleetVerbList is the set cmdFleetVerb runs, in usage order.
const fleetVerbList = "authority claim ids inbox ls msg pause release resume sent sessions status sweep wait who whose"

// cmdFleetVerb is `buddy fleet <verb> [args...]` (D-193): the verb runs with
// its ledger FORCED to the fleet (Env.fleetOnly, honoured by
// openSessionLedger alone, which the gate never calls), so no directory, and no
// git variable, can make it act in a repository's ledger instead.
//
// WHY A VERB (the design pass, finding 1). The plan's hand path cd's into the
// fleet's directory and lets discovery find no ledger there. With $HOME a
// governed worktree (a dotfiles repository `buddy init`ed), discovery finds
// that repository's ledger from the fleet's directory, and `release <slug>`
// released the home claim of the same name. A route that depends on where the
// filesystem says no ledger is cannot be enforced; a forced one can.
//
// Only section 6's typed verbs (fleetVerbs), each with its own parser and its
// own refusals (pause, resume, whose and authority refuse in a fleet, as they
// do from a plain directory). A hook verb, init, the gate and the rest are
// refused by name: they are not this ledger's to run.
func cmdFleetVerb(args []string, env Env, table map[string]verb) int {
	if len(args) == 0 {
		fmt.Fprintf(env.Stderr, "buddy fleet: %v\n", usageRefusal("no verb given", usageFleet))
		return 1
	}
	sub, rest := args[0], args[1:]
	if strings.HasPrefix(sub, "-") {
		fmt.Fprintf(env.Stderr, "buddy fleet: %v\n", usageRefusal(fmt.Sprintf("unknown flag %s: fleet takes a verb first", fence.Line(sub, 64)), usageFleet))
		return 1
	}
	if !fleetVerbs[sub] {
		fmt.Fprintf(env.Stderr, "buddy fleet: %v\n", usageRefusal(fmt.Sprintf("%s is not a verb fleet runs (one of: %s)", fence.Line(sub, 64), fleetVerbList), usageFleet))
		return 1
	}
	v := table[sub]
	if len(rest) > 0 && isHelp(rest[0]) {
		fmt.Fprintln(env.Stdout, v.usage)
		return 0
	}
	if env.getenv(EnvFleet) == "" {
		// A help flag later in the line is the verb's parser's (`claim x
		// --help` prints claim's usage), and the parser never runs here, so it
		// is answered here: with no fleet nothing could act, so no flag region
		// needs drawing (the cross-check: `buddy fleet claim x --help` was
		// refused where the bare verb answered).
		for _, a := range rest {
			if isHelp(a) {
				fmt.Fprintln(env.Stdout, v.usage)
				return 0
			}
		}
		fmt.Fprintf(env.Stderr, "buddy fleet: refused: %s is not set: there is no fleet to act in (a session is launched into one with %s=<name> in its environment)\n", EnvFleet, EnvFleet)
		return 1
	}
	env.fleetOnly = true
	return v.run(rest, env)
}

// buddyCmd is how buddy names itself in a follow-up command it prints for
// the verb in hand: `buddy fleet` when that verb's ledger is forced to the
// fleet (cmdFleetVerb), since a bare verb from a governed repository acts in
// THAT ledger. The panel's gate lens (Fable) measured three: a keep-alive
// armed as `buddy fleet wait` printed it was answered NO WAIT ("stop the
// /loop") by the bare check while the fleet's wait had LANDED; the holder ask
// queued in the home inbox of the home claim's holder; and beat's LANDED line
// for a fleet wait named a check that cleared nothing. The fleet's own lines
// in a hook beside a home pass "buddy fleet" themselves. Otherwise "buddy",
// byte for byte.
func buddyCmd(env Env) string {
	if env.fleetOnly {
		return "buddy fleet"
	}
	return "buddy"
}

// sortedFleetVerbs is fleetVerbs in order, for the test that holds
// fleetVerbList to the set.
func sortedFleetVerbs() []string {
	out := make([]string, 0, len(fleetVerbs))
	for v := range fleetVerbs {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
