package cli

// `buddy wait`: a declared wait, and the check a parked session's OWN
// scheduler runs so its prompt cache stays warm and its inbox drains (D-033,
// issue #26). The store file carries the register's why; this one carries the
// verb surface and the ONE rendering of a wait that every view shares.
//
// THE TRIGGER IS NOT BUDDY'S. A session that has declared a wait arms its own
// harness timer (`/loop buddy wait check`), and each firing is one tool call:
// the request that makes it refreshes the cache (a read restarts the entry's
// hour), the beat after it delivers the inbox, and the check prints one
// verdict. Buddy never wakes, schedules or types into a pane — the charter's
// "no wake" stands, and a timer typing into someone's terminal was cut by name.
//
// THE PACING IS FROM THE CHECK ITSELF, NOT FROM THE LEDGER'S CLOCK. The plan
// said `next check in = tier clock + 1h - now - 8m`. Measured while taking
// its own measurements, that is wrong: a check runs BETWEEN its own PreToolUse
// gate and its own PostToolUse beat, and only beat (and Stop's idle) write the
// context observation — so during a check the ledger's newest observation is
// the PREVIOUS request. At the second of two scheduled fires on 2026-09-23 the
// ledger said 06:17:03 while the request running the check was at 06:21:03.
// In a steady loop that observation is the previous ping, fifty minutes old,
// and the formula prints "2m"; the ping after it prints "50m"; the loop pays
// two pings a period. From-now is safe by construction (Fable design pass):
// every request after the check — the one that reads its result, the one that
// makes the scheduling call, the one after THAT call's result — touches the
// cache later still, and the scheduler times the wake from the scheduling
// call, so the gap to the next ping is the delay plus the wake's lateness.
//
// WHY 50 MINUTES. Measured on this box's transcripts (D-033): scheduled
// firings 3,602 s or less after the previous request read the cache (9 of 9),
// firings 3,633-3,660 s after re-wrote it (9 of 9), and a self-paced wake
// fired 0-58 s after the delay it was given, rounding up to the next minute.
// 3,000 s plus 58 is 3,058: nine minutes inside the measured edge. The
// harness's own tool text says any delay up to 3,600 wakes warm; measured, 9
// of 11 one-hour wakes came back cold.
//
// WHAT THE LEDGER OBSERVATION IS STILL FOR: the cache TIER (1h or 5m, from
// the newest record that wrote one, D-020) and whether the last OBSERVED
// request read the prefix or re-wrote it. Both lag one request and are said
// to: "the last observed request (50m ago)", never "this check".

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

const usageWait = "usage: buddy wait [--on <slug>]... [--until <dur>] [--note <text>] [--session <id>]   declare what you wait on\n" +
	"                (default --until 3h, ceiling 12h; no --on is a timer that expires and never lands)\n" +
	"       buddy wait check    one verdict — STILL WAITING / LANDED / EXPIRED / NO WAIT — run by THIS session's\n" +
	"                           own model, armed with: /loop buddy wait check\n" +
	"       buddy wait clear [--session <id>]   withdraw the wait (its next check says NO WAIT)\n" +
	"       buddy wait ls       every open wait, oldest first"

const (
	// waitDefault is the deadline a wait gets when it names none. Three hours
	// covers the serialized ~60-minute tiers and review slots in the field
	// notes twice over; the ceiling is store.WaitCeiling.
	waitDefault = 3 * time.Hour
	// keepAlivePeriod: see the header.
	keepAlivePeriod = 50 * time.Minute
	// maxWaitNote is the cap on a note, measured on the RENDERED form: the
	// note is shown through fence.Line, which expands a line break to three
	// bytes, and a raw-byte cap equal to the render cap still truncates
	// (D-021's finding, the same arithmetic).
	maxWaitNote = 512
)

func cmdWait(args []string, env Env) error {
	if len(args) > 0 {
		switch args[0] {
		case "check":
			return cmdWaitCheck(args[1:], env)
		case "clear":
			return cmdWaitClear(args[1:], env)
		case "ls":
			return cmdWaitLs(args[1:], env)
		}
	}
	return cmdWaitDeclare(args, env)
}

func cmdWaitDeclare(args []string, env Env) error {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	var on multiFlag
	fs.Var(&on, "on", "an OPEN claim slug of another session to wait on (repeatable; resolved once, to that claim)")
	until := fs.Duration("until", waitDefault, "how long to wait before the wait expires (1m..12h)")
	note := fs.String("note", "", "what you mean to do when it lands (shown back to you, fenced)")
	var session string
	sessionFlag(fs, &session)
	if help, err := parseFlags(fs, args, usageWait, env); help || err != nil {
		return err
	}
	if fs.NArg() > 0 {
		// Go's parser stops at the first non-flag, so `buddy wait api-work
		// --until 1h` would otherwise declare a three-hour TIMER with the
		// slug and the flag after it both unread.
		a := fence.Line(fs.Arg(0), 128)
		hint := ""
		if flags, _ := onFlags([]string{fs.Arg(0)}); flags != "" {
			hint = "; did you mean `buddy wait" + flags + "`?"
		}
		return fmt.Errorf("wait takes its claims as --on <slug>, got %s — flags after it would be IGNORED%s\n  %s",
			strconv.Quote(a), hint, usageWait)
	}
	if n := renderedLen(*note); n > maxWaitNote {
		return fmt.Errorf("--note renders to %d bytes and the cap is %d, so %d would be cut silently (a line break renders as ⏎, which is 3 bytes)",
			n, maxWaitNote, n-maxWaitNote)
	}
	st, _, err := mustLedger(env.Cwd, env)
	if err != nil {
		return err
	}
	defer st.Close()
	si, err := whoAmI(st, env, session)
	if err != nil {
		return err
	}
	w, replaced, err := st.DeclareWait(si.SessionID, si.Incarnation, on, *until, *note)
	if err != nil {
		return fencedErr(err)
	}
	now := nowOf(env)
	c, observed := sampleOf(st, si)
	// "THIS session" only when the harness says the caller IS the session
	// that waits: a `--session B` from session A's shell would otherwise tell
	// A to arm a loop whose checks speak for A (Codex code pass).
	where := "THIS session"
	if env.getenv(EnvClaudeSession) != si.SessionID {
		where = "session " + fence.Line(si.Label, 64) + " (a check speaks only for the session whose harness runs it)"
	}
	fmt.Fprintf(env.Stdout, "WAITING on %s — deadline in %s%s\n", targetsPhrase(now, w.Targets), span(w.Deadline.Sub(now)), notePhrase(w))
	fmt.Fprintln(env.Stdout, keepAliveAdvice(now, c, observed, where))
	if replaced != nil {
		fmt.Fprintf(env.Stdout, "replaced your open wait on %s (declared %s ago)\n", targetsPhrase(now, replaced.Targets), span(now.Sub(replaced.Since)))
	}
	return nil
}

// cmdWaitCheck is the keep-alive's one tool call.
//
// IT SPEAKS ONLY FOR THE HARNESS'S OWN SESSION. Its whole meaning — a request
// was just made on this session's cache, `last_check` is the keep-alive's
// heartbeat, a LANDED verdict is being handed to the session that waited — is
// true only when this session's own model ran it through its own harness, and
// the harness says which session that is in $CLAUDE_CODE_SESSION_ID. A
// `--session X` from another shell would stamp a keep-alive that never
// happened on X's row, and could CLEAR X's LANDED before X ever saw it (Fable
// design pass). So there is no --session here; `buddy who <target>` and
// `buddy wait ls` report a wait from outside without touching it.
func cmdWaitCheck(args []string, env Env) error {
	fs := flag.NewFlagSet("wait check", flag.ContinueOnError)
	if help, err := parseFlags(fs, args, usageWait, env); help || err != nil {
		return err
	}
	if err := noStray("wait check", fs, usageWait); err != nil {
		return err
	}
	id := env.getenv(EnvClaudeSession)
	if id == "" {
		return fmt.Errorf("wait check is a session's own keep-alive, run by its model through its harness, and $%s is unset here — from outside, `buddy who <target>` or `buddy wait ls` reports a wait without touching it",
			EnvClaudeSession)
	}
	if b := env.getenv(EnvSession); b != "" && b != id {
		// Not silently ignored: an override that is not honoured, with
		// nothing said, looks exactly like one that was (D-031).
		return fmt.Errorf("$%s names %s but the harness says this is session %s; a check speaks only for the harness's own session",
			EnvSession, fence.Line(b, 128), fence.Line(id, 128))
	}
	st, _, err := mustLedger(env.Cwd, env)
	if err != nil {
		return err
	}
	defer st.Close()
	si, known, err := st.SessionByID(id)
	if err != nil {
		return err
	}
	if !known || !si.Live() {
		return assertedNotLive(st, id, EnvClaudeSession, known, nil)
	}
	r, err := st.WaitCheck(si.SessionID, si.Incarnation)
	if err != nil {
		return fencedErr(err)
	}
	// Everything printed below comes from the one transaction above plus the
	// context observation; nothing is printed before the verdict is known, so
	// a failure is an error and never half a verdict (and never STILL WAITING
	// over an unreadable ledger).
	now := nowOf(env)
	c, observed := sampleOf(st, si)
	stop := "stop the /loop that runs this check (schedule no further check)"
	var b strings.Builder
	w := r.Wait
	switch {
	case !r.Found:
		last := ""
		if w.Decl != "" {
			last = " (" + lastWaitPhrase(now, w) + ")"
		}
		fmt.Fprintf(&b, "NO WAIT: nothing is registered for this session%s — %s\n", last, stop)
	case r.Verdict == store.WaitLanded:
		fmt.Fprintf(&b, "LANDED: %s — the wait is over after %s; %s\n", targetsPhrase(now, w.Targets), span(now.Sub(w.Since)), stop)
		if w.Note != "" {
			fmt.Fprintf(&b, "note: %s\n", fence.Line(w.Note, maxWaitNote))
		}
	case r.Verdict == store.WaitExpired:
		still := ""
		if open := openTargets(w.Targets); len(open) > 0 {
			still = " with " + targetsPhrase(now, open) + " still open"
		}
		fmt.Fprintf(&b, "EXPIRED: the deadline passed %s ago%s — the wait is over after %s; %s, and ask before waiting longer\n",
			span(now.Sub(w.Deadline)), still, span(now.Sub(w.Since)), stop)
		if w.Note != "" {
			fmt.Fprintf(&b, "note: %s\n", fence.Line(w.Note, maxWaitNote))
		}
	default:
		fmt.Fprintf(&b, "STILL WAITING on %s — %s so far, deadline in %s; %s\n",
			targetsPhrase(now, w.Targets), span(now.Sub(w.Since)), span(w.Deadline.Sub(now)), nextCheck(now, w.Deadline, c, observed))
	}
	b.WriteString(observedRequest(now, c, observed) + "\n")
	// ONE write, so "the verdict was written" is one fact and not three.
	if _, err := io.WriteString(env.Stdout, b.String()); err != nil {
		return err // nothing closed: the next check says the same verdict again
	}
	// THE ROW IS CLOSED ONLY AFTER THE VERDICT WAS WRITTEN (D-028's rule, and
	// the Codex code pass's finding): a check that closed first and then
	// failed to write lost its LANDED for good. The close is keyed to the
	// declaration, and its failure is bookkeeping — the verdict has been
	// delivered, and the worst a lost close does is say it once more.
	if r.Found && r.Verdict != store.WaitPending {
		reason := "landed"
		if r.Verdict == store.WaitExpired {
			reason = "expired"
		}
		// Silent, like D-028's marks: a fourth line would break the check's
		// one-to-three, and the only cost of a lost close is a repeat.
		_, _ = st.CloseWait(si.SessionID, w.Decl, reason)
	}
	return nil
}

func cmdWaitClear(args []string, env Env) error {
	fs := flag.NewFlagSet("wait clear", flag.ContinueOnError)
	var session string
	sessionFlag(fs, &session)
	if help, err := parseFlags(fs, args, usageWait, env); help || err != nil {
		return err
	}
	if err := noStray("wait clear", fs, usageWait); err != nil {
		return err
	}
	st, _, err := mustLedger(env.Cwd, env)
	if err != nil {
		return err
	}
	defer st.Close()
	si, err := whoAmI(st, env, session)
	if err != nil {
		return err
	}
	w, err := st.ClearWait(si.SessionID, si.Incarnation)
	if err != nil {
		return fencedErr(err)
	}
	if w == nil {
		fmt.Fprintf(env.Stdout, "no open wait to clear for %s\n", fence.Line(si.Label, 64))
		return nil
	}
	now := nowOf(env)
	fmt.Fprintf(env.Stdout, "cleared the wait of %s on %s (declared %s ago, %d check(s)) — its next `buddy wait check` says NO WAIT; stop the /loop that runs it\n",
		fence.Line(si.Label, 64), targetsPhrase(now, w.Targets), span(now.Sub(w.Since)), w.Checks)
	return nil
}

// cmdWaitLs is the operator's view: every open wait of a live session's
// current incarnation, oldest first, one line each. Rows begin "wait " so a
// label cannot open a line as a notice (the D-028/D-029 review finding).
func cmdWaitLs(args []string, env Env) error {
	fs := flag.NewFlagSet("wait ls", flag.ContinueOnError)
	if help, err := parseFlags(fs, args, usageWait, env); help || err != nil {
		return err
	}
	if err := noStray("wait ls", fs, usageWait); err != nil {
		return err
	}
	st, _, err := mustLedger(env.Cwd, env)
	if err != nil {
		return err
	}
	defer st.Close()
	waits, err := st.OpenWaits()
	if err != nil {
		return err
	}
	if len(waits) == 0 {
		fmt.Fprintln(env.Stdout, "no open waits")
		return nil
	}
	now := nowOf(env)
	labels := map[string]string{}
	if sessions, err := st.Sessions(store.ByLastSeen); err == nil {
		for _, si := range sessions {
			labels[si.SessionID] = si.Label
		}
	}
	for _, w := range waits {
		fmt.Fprintf(env.Stdout, "wait %-24s declared %s ago, %s, %s; on %s%s\n",
			fence.Field(labels[w.SessionID], 64), span(now.Sub(w.Since)), deadlinePhrase(now, w), lastCheckPhrase(now, w),
			targetsPhrase(now, w.Targets), notePhrase(w))
	}
	return nil
}

// ---- the one rendering of a wait, shared by every view ----

// span renders a duration to the minute: the waits here are measured in
// hours and a deadline "in 2h" that is really 2h59m is off by most of an
// hour, so the roster's coarse age() is not used. Go's own duration syntax,
// so a printed "--until 1h12m" can be pasted back.
func span(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// targetPhrase is one awaited claim as the ledger has it now. Slug and
// labels are peer text: fenced, each on this one line.
func targetPhrase(now time.Time, t store.WaitTarget) string {
	name := "claim " + strconv.Quote(fence.Line(t.Slug, 128))
	switch {
	case t.IsOpen():
		held := fmt.Sprintf("held by %s, seen %s ago", fence.Line(t.Holder.Label, 64), age(now, t.Holder.LastSeen))
		return name + " (" + held + ")"
	case t.State == "":
		return name + " closed (its row has since been swept)" + reopenedPhrase(now, t)
	default:
		return fmt.Sprintf("%s %s %s ago%s", name, t.State, span(now.Sub(t.Closed)), reopenedPhrase(now, t))
	}
}

// reopenedPhrase names a different claim open now under the awaited slug
// (Fable design pass): the awaited claim closed, and the slug is held again.
func reopenedPhrase(now time.Time, t store.WaitTarget) string {
	if !t.Reopened {
		return ""
	}
	return fmt.Sprintf(" (the slug is open again: a new claim by %s, taken %s ago)", fence.Line(t.ReopenedBy, 64), span(now.Sub(t.ReopenedAt)))
}

func targetsPhrase(now time.Time, ts []store.WaitTarget) string {
	if len(ts) == 0 {
		return "a timer (no claim named: it never lands, it expires)"
	}
	parts := make([]string, 0, len(ts))
	for _, t := range ts {
		parts = append(parts, targetPhrase(now, t))
	}
	return strings.Join(parts, "; ")
}

func openTargets(ts []store.WaitTarget) []store.WaitTarget {
	var out []store.WaitTarget
	for _, t := range ts {
		if t.IsOpen() {
			out = append(out, t)
		}
	}
	return out
}

// notePhrase is a wait's note as every view shows it: the declarer's own
// reminder, which is peer text to every other reader — fenced, on this line.
func notePhrase(w store.Wait) string {
	if w.Note == "" {
		return ""
	}
	return "; note: " + fence.Line(w.Note, maxWaitNote)
}

// deadlinePhrase is the deadline against the clock, and the verdict when
// the clock and the claims say the wait is over and no check has closed it.
// It is Verdict's rendering — the one computation — so the roster, `who`,
// `wait ls`, `hello` and `msg` cannot disagree about it (Codex design pass).
func deadlinePhrase(now time.Time, w store.Wait) string {
	switch w.Verdict(now) {
	case store.WaitLanded:
		return "LANDED: every claim it waits on has closed (a check clears it)"
	case store.WaitExpired:
		return fmt.Sprintf("EXPIRED: its deadline passed %s ago (a check clears it)", span(now.Sub(w.Deadline)))
	default:
		return "deadline in " + span(w.Deadline.Sub(now))
	}
}

// verdictWord is the roster's one-token form of the same verdict.
func verdictWord(now time.Time, w store.Wait) string {
	switch w.Verdict(now) {
	case store.WaitLanded:
		return " LANDED"
	case store.WaitExpired:
		return " EXPIRED"
	}
	return ""
}

func lastCheckPhrase(now time.Time, w store.Wait) string {
	if w.LastCheck.IsZero() {
		return "no check since declaring"
	}
	return fmt.Sprintf("last check %s ago (%d check(s))", span(now.Sub(w.LastCheck)), w.Checks)
}

// lastWaitPhrase says what became of a session's last wait, for NO WAIT.
func lastWaitPhrase(now time.Time, w store.Wait) string {
	switch {
	case w.IsOpen():
		// An open row NO WAIT still reports is another incarnation's; the
		// check's own cleanup closes those, so this is a belt, not a path.
		return "the last wait on record belongs to an earlier run of this session"
	case w.Reason == "landed":
		return "your last wait LANDED " + span(now.Sub(w.Cleared)) + " ago"
	case w.Reason == "expired":
		return "your last wait EXPIRED " + span(now.Sub(w.Cleared)) + " ago"
	case w.Reason == "cleared":
		return "your last wait was cleared " + span(now.Sub(w.Cleared)) + " ago"
	default:
		return "your last wait ended with an earlier run of this session"
	}
}

// sampleOf is the session's context observation, for its current
// incarnation only (a predecessor's footprint is not this session's).
func sampleOf(st *store.Store, si store.SessionInfo) (store.ContextSample, bool) {
	samples, err := st.ContextSamples()
	if err != nil {
		return store.ContextSample{}, false
	}
	c, ok := samples[si.SessionID]
	if !ok || c.Incarnation != si.Incarnation {
		return store.ContextSample{}, false
	}
	return c, true
}

// cacheTier reads the tier off an observation: the label, whether a
// keep-alive pays on it, and whether any tier was recorded at all. A turn
// that wrote BOTH tiers is judged by the shorter, as the roster judges it
// (D-020): the prompt is wholly hot only while every part is, and keeping
// a five-minute part warm is exactly what does not pay.
func cacheTier(c store.ContextSample, observed bool) (label string, pays, known bool) {
	switch {
	case !observed:
		return "", true, false
	case c.Cache1h > 0 && c.Cache5m > 0:
		return "1h+5m", false, true
	case c.Cache1h > 0:
		return "1h", true, true
	case c.Cache5m > 0:
		return "5m", false, true
	default:
		return "", true, false
	}
}

// nextCheck is STILL WAITING's pacing line. From NOW (see the header), the
// period capped by the deadline so the check that says EXPIRED comes at it
// rather than up to fifty minutes past it. The cap is rounded UP to the
// scheduler's whole minute: rounded to the nearest, 20m29s left scheduled a
// check 29 s BEFORE the deadline, which could only say STILL WAITING and
// schedule one more (Codex code pass); rounded up it lands at or after it,
// and a pending wait always has some time left, so it is never under a
// minute. Seconds are printed beside the minutes because the self-paced form
// hands the scheduler a number, and "50m" read as 50 seconds is a keep-alive
// that pings sixty times an hour.
func nextCheck(now, deadline time.Time, c store.ContextSample, observed bool) string {
	label, pays, known := cacheTier(c, observed)
	if !pays {
		return fmt.Sprintf("no next check: the last observed cache write (%s ago) was on the %s tier, where keeping the cache warm costs more than re-writing it — stop the /loop that runs this check",
			age(now, c.TierAt), label)
	}
	d, why := keepAlivePeriod, ""
	if left := deadline.Sub(now); left < d {
		d, why = (left+time.Minute-1)/time.Minute*time.Minute, ", at the deadline"
	}
	if !known {
		why += "; no cache tier observed for this session yet"
	}
	return fmt.Sprintf("next check in %s (%ds from now%s)", span(d), int(d.Seconds()), why)
}

// keepAliveAdvice is the declaration's second line: whether to arm the
// keep-alive, and exactly how.
func keepAliveAdvice(now time.Time, c store.ContextSample, observed bool, where string) string {
	label, pays, known := cacheTier(c, observed)
	switch {
	case !pays:
		return fmt.Sprintf("keep-alive: NONE — the last observed cache write (%s ago) was on the %s tier, where keeping the cache warm costs more than re-writing it. The wait is recorded; the roster, `who` and `msg` show it, and a check run by hand still reports it",
			age(now, c.TierAt), label)
	case !known:
		return "keep-alive: no cache tier observed for this session yet. Arm it in " + where + ": /loop buddy wait check — each check is one tool call that refreshes the cache and delivers the inbox, and it says which tier it found and when the next is due; fixed fallback: /loop 30m buddy wait check"
	default:
		return fmt.Sprintf("keep-alive: cache %s tier (last written %s ago). Arm it in %s: /loop buddy wait check — each check is one tool call that refreshes the cache and delivers the inbox, and it says when the next is due (%s on this tier); fixed fallback: /loop 30m buddy wait check",
			label, age(now, c.TierAt), where, span(keepAlivePeriod))
	}
}

// observedRequest is the check's last line: what the ledger's newest
// observation of this session's requests says about the cache. It LAGS ONE
// REQUEST and is dated so a reader cannot take it for this check's own.
//
// COLD WRITE is the counts and nothing more (Codex and Fable design passes):
// a request that wrote more than it read was mostly not served from cache.
// That is what a lapsed cache looks like, and it is also what a changed
// prefix or an enormous new tool result looks like; the line says the
// first only conditionally. Residual: the shared system and tool prefix
// (21-30k measured) reads warm even on a cold restart, so a session under
// about 60k can re-write without tripping this — the cheap direction.
func observedRequest(now time.Time, c store.ContextSample, observed bool) string {
	if !observed {
		return "no request of this session has been observed yet"
	}
	head := fmt.Sprintf("last observed request %s ago read %s, wrote %s", age(now, c.TurnAt), tokens(c.CacheRead), tokens(c.CacheWrite))
	switch {
	case c.CacheWrite > c.CacheRead:
		return "COLD WRITE: " + head + " — more of that prompt was written to the cache than read from it: a lapsed cache (for a check, a keep-alive that missed) or a changed or grown prompt; the counts cannot say which"
	case c.CacheRead == 0:
		// Nothing read and nothing written is not "served from cache"
		// (Codex code pass): it is a request the cache played no part in.
		return head + " (nothing read from the cache)"
	default:
		return head + " (mostly read from the cache)"
	}
}

// onFlags renders `--on <slug>` for each slug that can be pasted back and
// still name its claim, and counts the ones that cannot. A slug the fence
// alters (a line break shown as ⏎, a stripped control character) printed as
// a command names a DIFFERENT slug — the pasted line is refused, or waits on
// another claim that happens to carry the rendered name (Codex code pass).
// Such a slug is left out and counted, never printed as something to run.
func onFlags(slugs []string) (flags string, unpastable int) {
	var b strings.Builder
	for _, slug := range slugs {
		if fence.Line(slug, 128) != slug {
			unpastable++
			continue
		}
		b.WriteString(" --on " + shellQuote(slug))
	}
	return b.String(), unpastable
}

// unpastableNote says what onFlags left out.
func unpastableNote(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(" (and %d claim(s) whose slug cannot be pasted back as printed, so it is not written as a command)", n)
}

// shellQuote makes a slug safe to paste into a command line. A slug is
// free text: `buddy wait --on a b` would wait on "a" and refuse "b" (Codex
// design pass). Values made only of the characters below go bare.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._/:@%+=,-", r)) {
			return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		}
	}
	return s
}

// ---- the views ----

// openWaitOf is the session's OPEN wait of its CURRENT incarnation, the only
// one any view other than hello reports as the session's. A read error comes
// back as an error, never as "no wait": the report that prints "none
// declared" must know it (Codex code pass).
func openWaitOf(st *store.Store, si store.SessionInfo) (store.Wait, bool, error) {
	w, ok, err := st.WaitOf(si.SessionID)
	if err != nil {
		return store.Wait{}, false, err
	}
	if !ok || !w.IsOpen() || w.Incarnation != si.Incarnation || !si.Live() {
		return store.Wait{}, false, nil
	}
	return w, true, nil
}

// waitNotice is beat's one-shot LANDED line, and its mark. Marked through the
// returned commit, only after the hook output was written — D-028's pattern:
// a lost write costs nothing permanently, and a lost mark repeats the line
// once, which is the at-least-once every beat-borne notice accepts. Keyed to
// the declaration, so a notice about one wait can never mark its replacement.
// Only LANDED is announced here, and only for the current incarnation's open
// row: EXPIRED is the check's to say, and nothing here closes the row — the
// check that reads the verdict does.
func waitNotice(st *store.Store, me store.SessionInfo, now time.Time) (string, func() error) {
	nothing := func() error { return nil }
	w, ok, err := openWaitOf(st, me)
	if err != nil || !ok || w.Told || w.Verdict(now) != store.WaitLanded {
		return "", nothing
	}
	line := fmt.Sprintf("BUDDY: your wait LANDED — %s%s. `buddy wait check` clears it; then stop the /loop that runs it.\n",
		targetsPhrase(now, w.Targets), notePhrase(w))
	return line, func() error { return st.MarkWaitTold(me.SessionID, w.Decl) }
}

// waitHelloLines is what hello's digest says about a wait, if anything.
//
// The same incarnation (a /compact start, or a second process on a live id;
// NOT a /clear, which mints a new session id and so finds no wait here at all,
// measured in D-034): the wait is still this session's, so it is restated with its
// verdict, and the scheduled check is questioned rather than assumed. The
// harness documents scheduled tasks as session-only and not written to disk;
// whether one survives a given kind of start was NOT measured (D-033,
// measurement 5), so the line asks the session to look, and re-arm only if
// nothing is scheduled — re-arming blind would run two loops.
//
// A predecessor's wait that ended with its run, still inside its deadline:
// named as the earlier run's, never as this session's, with the line that
// would declare it again — only the claims still open, because the others
// have landed.
func waitHelloLines(st *store.Store, si store.SessionInfo, now time.Time) string {
	w, ok, err := st.WaitOf(si.SessionID)
	if err != nil || !ok {
		return ""
	}
	if w.IsOpen() && w.Incarnation == si.Incarnation {
		head := fmt.Sprintf("BUDDY: you are WAITING on %s — declared %s ago, %s%s.",
			targetsPhrase(now, w.Targets), span(now.Sub(w.Since)), deadlinePhrase(now, w), notePhrase(w))
		// The advice follows the verdict and the tier, as the declaration's
		// does (Codex code pass): a wait that is over needs one check to take
		// its verdict, not a loop, and on a tier where a keep-alive does not
		// pay there is nothing to re-arm.
		label, pays, _ := cacheTier(sampleOf(st, si))
		switch {
		case w.Verdict(now) != store.WaitPending:
			return head + " Run `buddy wait check` once to take the verdict; no loop needs arming.\n"
		case !pays:
			return head + " No keep-alive on the " + label + " cache tier.\n"
		default:
			return head + " The harness keeps scheduled checks for the session only, not on disk: if no /loop running `buddy wait check` is scheduled here, re-arm it: /loop buddy wait check\n"
		}
	}
	if w.Incarnation == si.Incarnation || w.Reason != "ended" || !now.Before(w.Deadline) {
		return ""
	}
	left := w.Deadline.Sub(now).Round(time.Minute)
	if left < time.Minute {
		left = time.Minute
	}
	head := fmt.Sprintf("BUDDY: an earlier run of this session id was WAITING on %s (declared %s ago, deadline in %s); that wait ended with it.",
		targetsPhrase(now, w.Targets), span(now.Sub(w.Since)), span(w.Deadline.Sub(now)))
	open := openTargets(w.Targets)
	if len(w.Targets) > 0 && len(open) == 0 {
		return head + " Its claims have all closed since: nothing is left to wait for.\n"
	}
	slugs := make([]string, 0, len(open))
	for _, t := range open {
		slugs = append(slugs, t.Slug)
	}
	flags, unpastable := onFlags(slugs)
	if len(open) > 0 && unpastable == len(open) {
		return head + " Its open claims' slugs cannot be pasted back as printed; `buddy ls` lists them.\n"
	}
	arm := ""
	if _, pays, _ := cacheTier(sampleOf(st, si)); pays {
		arm = ", then /loop buddy wait check"
	}
	return head + " To wait again: buddy wait" + flags + " --until " + span(left) + arm + unpastableNote(unpastable) + "\n"
}

// waiterNote is msg's arm for a recipient with a declared wait: the
// observation (declared when, last check when) and never a forecast of when
// the message will be read.
func waiterNote(st *store.Store, si store.SessionInfo, now time.Time) string {
	w, ok, err := openWaitOf(st, si)
	if err != nil || !ok {
		return "" // a note never costs the send (D-032)
	}
	return fmt.Sprintf("; it declared a wait %s ago on %s, %s, %s", span(now.Sub(w.Since)), targetsPhrase(now, w.Targets),
		deadlinePhrase(now, w), lastCheckPhrase(now, w))
}

// waitersOn lists the open waits that name claimID among their targets.
func waitersOn(st *store.Store, claimID string) ([]store.Wait, error) {
	waits, err := st.OpenWaits()
	if err != nil {
		return nil, err
	}
	var out []store.Wait
	for _, w := range waits {
		for _, t := range w.Targets {
			if t.ClaimID == claimID {
				out = append(out, w)
				break
			}
		}
	}
	return out, nil
}

// waitersPhrase renders waiters for release and WAITED ON: each label with
// the wait's age, and its verdict word when the clock and claims say it is
// over. Labels are peer text in a list: Field'ed, so each stays one token.
func waitersPhrase(st *store.Store, waits []store.Wait, now time.Time) string {
	labels := map[string]string{}
	if sessions, err := st.Sessions(store.ByLastSeen); err == nil {
		for _, si := range sessions {
			labels[si.SessionID] = si.Label
		}
	}
	parts := make([]string, 0, len(waits))
	for _, w := range waits {
		p := fmt.Sprintf("%s %s", fence.Field(labels[w.SessionID], 64), span(now.Sub(w.Since)))
		if w.Verdict(now) == store.WaitExpired {
			p += fmt.Sprintf(" (deadline passed %s ago)", span(now.Sub(w.Deadline)))
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, ", ")
}

// waitSuggestion is the line a refused claim prints after its REFUSED lines:
// the command that would declare a wait on every claim in the way. It
// SUGGESTS and never registers — a refusal is a fact about a claim, not
// about what the refused session means to do next (D-016: declared or
// nothing).
func waitSuggestion(conflicts []store.Conflict) string {
	seen := map[string]bool{}
	var slugs []string
	for _, c := range conflicts {
		if c.Slug == "" || seen[c.Slug] {
			continue
		}
		seen[c.Slug] = true
		slugs = append(slugs, c.Slug)
	}
	flags, unpastable := onFlags(slugs)
	switch {
	case len(slugs) == 0:
		return ""
	case flags == "":
		return "to be told when it frees: buddy wait --on <slug> — the slug(s) above cannot be pasted back as printed, so none is written as a command\n"
	default:
		return "to be told when it frees: buddy wait" + flags + unpastableNote(unpastable) + "\n"
	}
}
