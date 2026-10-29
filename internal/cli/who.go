package cli

// `buddy status` and `buddy who <target>`: one session, every register the
// ledger holds about it, on one screen.
//
// THE FAILURES (issues #15, #17, #20, #22, one run). A coordinator released a
// session after checking its work was landed and its tree clean, and it still
// held two claims: the release check looked at git and never at the ledger,
// because nothing in buddy answered "what does this session hold" in one
// place — `ls` answers by claim and `sessions` by count. Two live sessions
// wore one roster name and rulings were attributed to the bare name all
// afternoon; a send to a claim slug bounced; a session credited a finding to
// a third session because a path read like a name — four identifiers for one
// actor and no command that took any one and returned the rest. Winding the
// fleet down, the coordinator asked sessions to CONSENT to being killed and
// they correctly refused: a peer relaying an operator's wish is not the
// operator. What it should have been able to ask was the reportable question
// — what would ending this session leave behind — and that question had no
// verb.
//
// `who` takes any name a session answers to (ResolveTarget: id, label,
// s-<8hex>, an OPEN claim slug) and prints the rest: the cross-reference §2b
// asked for. `status` is `who` on the caller. Both REPORT and grant nothing:
// the EXIT line describes what the ledger would be left holding, never
// permission, and never proof that killing is safe — a coordinator can report
// that a session is safe to kill and must not be able to obtain permission
// to kill it (D-027).
//
// Exit status is 0 for a report that was produced, whatever it says; a
// caller that cannot be resolved, a target that names nothing, an unreadable
// ledger are errors and exit 1 — "always exits 0" must exclude failing to
// produce the report (Codex design pass).

import (
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

func cmdStatus(args []string, env Env) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	var session string
	sessionFlag(fs, &session)
	if help, err := parseFlags(fs, args, usageStatus, env); help || err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("status takes no arguments (`buddy who <target>` asks about another session), got %s",
			strconv.Quote(fence.Line(fs.Arg(0), 64)))
	}
	st, rc, err := mustLedger(env.Cwd, env)
	if err != nil {
		return err
	}
	defer st.Close()
	si, err := whoAmI(st, env, session)
	if err != nil {
		return err
	}
	return sessionReport(env, st, rc.top, si, true, "")
}

// cmdWho. With no argument it reports the caller, exactly as `status` does
// (D-092): "who am I" is the question a session typing a bare `buddy who`
// was asking — a usage error cost it a round trip on one fleet, and
// refusing with "use status" would answer a question it understood with a
// lecture. D-031 refuses what a verb does not understand; an absent target
// is understood.
func cmdWho(args []string, env Env) error {
	if len(args) == 0 {
		return cmdStatus(nil, env)
	}
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return errors.New(usageWho)
	}
	st, rc, err := mustLedger(env.Cwd, env)
	if err != nil {
		return err
	}
	defer st.Close()
	t, err := st.ResolveTarget(args[0])
	if err != nil {
		// A slash is how a path reads, and `who .buddy/slot/x` asked about a
		// path (one fleet's refusals). Labels carry slashes too, so this
		// suggests and never redirects.
		if errors.Is(err, store.ErrNoSuchTarget) && strings.Contains(args[0], "/") {
			return fmt.Errorf("%s; if it is a path, `buddy whose <path>` reports who claims it", fence.Line(err.Error(), 512))
		}
		return errors.New(fence.Line(err.Error(), 512))
	}
	if t.ID == store.AllTarget {
		return errors.New("`all` is every session; `buddy sessions` is the roster")
	}
	si, ok, err := st.SessionByID(t.ID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s resolved to session %s, which is not in the ledger", fence.Line(args[0], 128), fence.Line(t.ID, 128))
	}
	// The caller's own row wears the same marker the roster gives it, best
	// effort for the reason cmdSessions gives: a listing must not fail for
	// the reason a claim must.
	me := false
	if self, err := whoAmI(st, Env{Cwd: env.Cwd, Getenv: env.Getenv, Now: env.Now}, ""); err == nil && self.SessionID == si.SessionID {
		me = true
	}
	if t.Via == "slug" {
		// D-094: every scope, uncut, so "re-claim with the SAME scopes" can be done.
		if err := claimScopesBlock(env, st, t.Slug); err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "%s is claim %s, held by:\n", fence.Line(args[0], 128), strconv.Quote(fence.Line(t.Slug, 128)))
	}
	onSlug := ""
	if t.Via == "slug" {
		onSlug = t.Slug
	}
	return sessionReport(env, st, rc.top, si, me, onSlug)
}

// sessionReport renders every register the ledger holds about one session.
// Every value is peer text and fenced; every list is capped like `whose`'s,
// with the total stated when the cap bites, because a report that quietly
// drops rows is indistinguishable from a smaller one. onSlug is the open
// claim the target was named by, "" for any other name: WAITED ON then
// speaks for that claim alone (D-092).
func sessionReport(env Env, st *store.Store, top string, si store.SessionInfo, me bool, onSlug string) error {
	now := nowOf(env)
	const maxRows = 20

	// Header: the same vocabulary as the roster row, so an operator reading
	// both does not learn two.
	state := "live"
	switch {
	case !si.Live():
		state = "ended " + age(now, si.Ended)
	case now.Sub(si.LastSeen) > store.StaleAfter:
		state = "live STALE"
	}
	mark := "-"
	if me {
		mark = "*"
	}
	var head strings.Builder
	// Field for the id as well as the label: hello accepts any non-empty id,
	// and a space in one would split the parenthesised column in two.
	fmt.Fprintf(&head, "%s %s  (%s)  %s  started %s  seen %s", mark, fence.Field(si.Label, 64), fence.Field(si.SessionID, 128),
		state, age(now, si.Started), age(now, si.LastSeen))
	if si.Live() {
		if _, paused, err := st.PausedFor(si.SessionID, si.Label); err != nil {
			return err
		} else if paused {
			head.WriteString("  PAUSED")
		}
		idle, err := st.IdleSessions()
		if err != nil {
			return err
		}
		if rest, ok := idle[si.SessionID]; ok && rest.Incarnation == si.Incarnation {
			fmt.Fprintf(&head, "  idle %s", age(now, rest.Since))
		}
		procs, err := st.SessionProcs()
		if err != nil {
			return err
		}
		if note := procNote(env, procs[si.SessionID]); note != "" {
			head.WriteString("  " + note)
		}
		if si.Terminal != "" {
			head.WriteString("  pane " + fence.Field(si.Terminal, 64))
		}
	}
	fmt.Fprintln(env.Stdout, head.String())

	// CLAIMS HELD — the register that reserves, and the one a release check
	// forgot (issue #15). Keyed by (session, incarnation) like the roster's
	// count: a revived session is not charged for its predecessor's rows.
	open, err := st.Claims(false)
	if err != nil {
		return err
	}
	var held []store.ClaimInfo
	for _, c := range open {
		if c.Owner.SessionID == si.SessionID && c.Incarnation == si.Incarnation {
			held = append(held, c)
		}
	}
	hs := readHolders(env, st)
	fmt.Fprintf(env.Stdout, "CLAIMS HELD  %d\n", len(held))
	for i, c := range held {
		if i == maxRows {
			fmt.Fprintf(env.Stdout, "  ...and %d more not shown\n", len(held)-maxRows)
			break
		}
		// The holder's words, not the renewal's (D-090).
		stale := hs.judge(now, c).rowNote(now)
		scopes := scopeList(c.Scopes) // whole items, one token each, with a count of what was cut
		fmt.Fprintf(env.Stdout, "  %-24s held %-4s %sscopes: %s%s\n",
			fence.Field(c.Slug, 128), age(now, c.Created), sharedWord(c.Shared), scopes, stale)
	}

	// DIRTY PATHS — observations (invariant 10): a tool call by this session
	// named the path and git has not since reported it clean. Not a lock,
	// and said so on the line.
	dirty, err := st.DirtyPathsOf(si.SessionID)
	if err != nil {
		return err
	}
	if len(dirty) == 0 {
		fmt.Fprintln(env.Stdout, "DIRTY PATHS  (none recorded)")
	} else {
		paths := make([]string, 0, len(dirty))
		for _, d := range dirty {
			paths = append(paths, d.Path)
		}
		fmt.Fprintf(env.Stdout, "DIRTY PATHS  %d recorded to this session (observations, not locks): %s\n",
			len(dirty), joinCapped(paths, 512))
	}

	// BASE (D-038) — the commit this session's tree was on when its last
	// reported turn ended, and where that stands against main NOW. Printed
	// as "(none recorded)" rather than omitted (D-023): absent means the Stop
	// hook has not reported, not that the tree is current.
	bases, err := st.Bases()
	if err != nil {
		return err
	}
	if b, ok := bases[si.SessionID]; ok && b.Incarnation == si.Incarnation {
		fmt.Fprintf(env.Stdout, "BASE         %s, as of its last reported turn\n",
			strings.TrimPrefix(newBaseReader(env.Cwd).note(now, b), "base "))
	} else {
		fmt.Fprintln(env.Stdout, "BASE         (none recorded; the Stop hook records it)")
	}

	// INBOX — messages queued and not yet drained. For a live session that is
	// idle this is the count that will not move until it is prompted (#12).
	msgs, err := st.Undelivered(si.SessionID, si.Label)
	if err != nil {
		return err
	}
	// Dated by the oldest row (issue #24): "3 undelivered" for a session seen
	// 2 s ago is a queue draining, and the same count with an oldest of 2 h is
	// a channel nobody is reading. The count alone cannot tell them apart.
	if len(msgs) == 0 {
		fmt.Fprintln(env.Stdout, "INBOX        0 undelivered")
	} else {
		fmt.Fprintf(env.Stdout, "INBOX        %d undelivered, the oldest %s old\n", len(msgs), age(now, oldestOf(msgs)))
	}
	// ADAPTER — the adapter channel, its own register (review F-24). INBOX
	// counts the legacy channel only, so a full adapter queue used to print
	// as "0 undelivered".
	ab, err := st.AdapterBacklogOf(si.SessionID, si.Incarnation)
	if err != nil {
		return err
	}
	fmt.Fprintln(env.Stdout, "ADAPTER      "+adapterRegister(ab, now))

	// WAITING — what this session has declared it is waiting on (D-033), with
	// the one Verdict every view renders, when its keep-alive last checked in,
	// and the ledger's newest observation of its requests. "none declared"
	// and never "not waiting": no row means the session has not said (D-016).
	// A register that could not be READ fails the report, like every other
	// register here: "none declared" over an error would be an assertion of
	// absence the ledger never made (Codex code pass).
	w, declared, err := openWaitOf(st, si)
	if err != nil {
		return err
	}
	if declared {
		c, observed := sampleOf(st, si)
		fmt.Fprintf(env.Stdout, "WAITING      on %s — declared %s ago, %s, %s; %s%s%s\n",
			targetsPhrase(now, w.Targets), span(now.Sub(w.Since)), deadlinePhrase(now, w), lastCheckPhrase(now, w),
			observedRequest(now, c, observed), readyPhrase(w), notePhrase(w))
	} else {
		fmt.Fprintln(env.Stdout, "WAITING      none declared")
	}
	// WAITED ON — the other end (field notes §9's `blocked-on`): the sessions
	// that declared a wait on a claim this one holds. Information; nothing
	// here obliges the holder to anything.
	//
	// Named by a claim slug, it is THAT claim's waiters, and the line names
	// the claim (D-092). It used to be the holder's whole set: on one fleet
	// `who run32` and `who run33` printed the same three READY riders, each
	// waiting on one of the two, and the reader could not tell which.
	var focus []store.ClaimInfo
	if onSlug != "" {
		for _, c := range open {
			if c.Slug == onSlug {
				focus = append(focus, c)
			}
		}
	} else {
		focus = held
	}
	if len(focus) > 0 {
		var waiting []store.Wait
		seen := map[string]bool{}
		for _, c := range focus {
			ws, err := waitersOn(st, c.ClaimID)
			if err != nil {
				return err
			}
			for _, w := range ws {
				if !seen[w.SessionID] {
					seen[w.SessionID] = true
					waiting = append(waiting, w)
				}
			}
		}
		riders := false
		for _, w := range waiting {
			riders = riders || w.ReadySHA != ""
		}
		on := ""
		if onSlug != "" {
			on = "on claim " + strconv.Quote(fence.Line(onSlug, 128)) + " "
		}
		switch {
		case len(waiting) == 0:
			fmt.Fprintf(env.Stdout, "WAITED ON    %sby no declared wait\n", on)
		case riders:
			// A batched run forming (D-049): the holder reads who is in.
			summary, lines := ridersLines(st, waiting, now)
			fmt.Fprintf(env.Stdout, "WAITED ON    %sby %d session(s), %s:\n", on, len(waiting), summary)
			for _, ln := range lines {
				fmt.Fprintln(env.Stdout, ln)
			}
		default:
			fmt.Fprintf(env.Stdout, "WAITED ON    %sby %d session(s): %s\n", on, len(waiting), waitersPhrase(st, waiting, now))
		}
	}

	// AUTHORITY — watched files whose recorded modification time postdates
	// this session's registration (D-028). Advisory: the file on disk
	// changed after the session started; not that its contents differ from
	// what the session read, nor that it has not re-read them since.
	if top != "" && si.Live() {
		changed, unread, err := authorityChanged(st, top, si)
		if err != nil {
			return err
		}
		if len(changed) == 0 {
			// Worded as the measurement: no watched file carries a later
			// mtime, and how many could not be read at all — not "nothing
			// changed", which a missing file would make untrue.
			fmt.Fprintf(env.Stdout, "AUTHORITY    no watched file's mtime is later than this session's start (%s)\n", unreadNote(unread))
		}
		for _, a := range changed {
			fmt.Fprintf(env.Stdout, "AUTHORITY    %s changed on disk %s ago, AFTER this session started (%s ago) — its copy may be stale\n",
				fence.Line(a.Path, 512), age(now, a.ModTime), age(now, si.Started))
		}
	}

	// JOBS — the shells still running under its harness process (jobs.go,
	// D-065). Beside EXIT because EXIT speaks for the ledger only, and was
	// read as speaking for the session.
	line, jobsClause := jobsLine(env, st, si)
	if line != "" {
		fmt.Fprintln(env.Stdout, line)
	}

	// EXIT — what ending the session would leave behind, on ONE line: the
	// claims it holds, its wait, and its shells (D-092). The close test is
	// all three, and with EXIT speaking for claims alone a coordinator closing
	// lanes hand-rolled `who | grep -E 'CLAIMS|WAITING|JOBS'` ~35 times in 20
	// spellings on one fleet. Still a description of consequences, never
	// permission: nothing here says a session may be ended, and nothing that
	// could be printed here would make it so (D-027). The jobs clause keeps
	// "cannot say" apart from "none".
	//
	// THE TAIL SAYS WHAT ENDING LEAVES AND WHAT TO RUN, NOTHING ELSE (D-123).
	// It also explained who would free a held claim later (hello, claim or
	// sweep) and that dirty paths stay in the tree: 186-222 bytes on a line
	// read ~33 times a day, for facts the skill and the ledger's own verbs
	// already hold. The slugs to release stay, whole.
	if !si.Live() {
		fmt.Fprintln(env.Stdout, "EXIT         already ended")
		return nil
	}
	clauses := []string{"no claims held"}
	if len(held) > 0 {
		clauses[0] = fmt.Sprintf("%d claim(s) held", len(held))
	}
	if declared {
		clauses = append(clauses, "a wait declared"+verdictWord(now, w)+" (WAITING above)")
	} else {
		clauses = append(clauses, "no wait declared")
	}
	if jobsClause != "" {
		clauses = append(clauses, jobsClause)
	}
	tail := " — ending now would leave no claim"
	if len(held) > 0 {
		slugs := make([]string, 0, len(held))
		for _, c := range held {
			slugs = append(slugs, c.Slug)
		}
		tail = fmt.Sprintf(" — ending now would leave the claim(s) held; `buddy release <slug>` first: %s",
			joinCapped(slugs, 512))
	}
	fmt.Fprintln(env.Stdout, "EXIT         "+strings.Join(clauses, "; ")+tail)
	return nil
}

// joinCapped renders a list of peer values on one line within max bytes,
// WHOLE items only, and says how many did not fit. The first shape fenced
// the join at the cap, and one 512-byte scope then hid every scope after it
// with nothing to say so — a report that quietly drops rows is
// indistinguishable from a smaller one (Codex code pass, D-027).
func joinCapped(items []string, max int) string {
	return joinWhole(items, max, func(s string) string { return fence.Line(s, max) })
}

// joinWhole is joinCapped with the fence chosen by the caller: scopeList
// renders each scope as one token (fence.Field) under the same whole-item cap.
//
// The FIRST item always shows, cut by its own fence if it must be. Whole items
// only, applied to the first, printed a claim whose one scope exceeds the cap
// as `scopes: ...and 1 more not shown`, and the gate's deny as `inside scope
// "...and 1 more not shown"`: a count where the only thing to read was the
// path (Codex code pass, D-066). Field can also outgrow its cap, since each
// space becomes a 3-byte ␣ after the cut, so a list may run past max by that
// first item's overshoot; the callers that budget bytes measure the line.
func joinWhole(items []string, max int, render func(string) string) string {
	var b strings.Builder
	shown := 0
	for _, it := range items {
		piece := render(it)
		if shown > 0 {
			piece = ", " + piece
			if b.Len()+len(piece) > max {
				break
			}
		}
		b.WriteString(piece)
		shown++
	}
	if shown < len(items) {
		if shown > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "...and %d more not shown", len(items)-shown)
	}
	return b.String()
}

// unreadNote says how many watched paths were not files that could be read.
func unreadNote(unread int) string {
	if unread == 0 {
		return "every watched file was read"
	}
	return fmt.Sprintf("%d watched path(s) could not be read as a file and were not checked", unread)
}

// adapterRegister is the ADAPTER line's text: what the ledger records about
// the session's adapter channel, "(none)" when it records nothing.
func adapterRegister(a store.AdapterBacklog, now time.Time) string {
	var parts []string
	if a.Current > 0 {
		parts = append(parts, fmt.Sprintf("%d queued for its adapter, the oldest %s old", a.Current, age(now, a.CurrentOldest)))
	}
	if a.Earlier > 0 {
		parts = append(parts, fmt.Sprintf("%d captured for an earlier incarnation, which no fetch by the current one returns", a.Earlier))
	}
	if a.Endpoint {
		parts = append(parts, "a receiving endpoint is registered; it is woken through its adapter, never a legacy socket wake")
	}
	if len(parts) == 0 {
		return "(none)"
	}
	return strings.Join(parts, "; ")
}
