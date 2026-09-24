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
	return sessionReport(env, st, rc.top, si, true)
}

func cmdWho(args []string, env Env) error {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return errors.New(usageWho)
	}
	st, rc, err := mustLedger(env.Cwd, env)
	if err != nil {
		return err
	}
	defer st.Close()
	t, err := resolveTargetQuiet(st, args[0])
	if err != nil {
		return err
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
		fmt.Fprintf(env.Stdout, "%s is claim %s, held by:\n", fence.Line(args[0], 128), strconv.Quote(fence.Line(t.Slug, 128)))
	}
	return sessionReport(env, st, rc.top, si, me)
}

// sessionReport renders every register the ledger holds about one session.
// Every value is peer text and fenced; every list is capped like `whose`'s,
// with the total stated when the cap bites, because a report that quietly
// drops rows is indistinguishable from a smaller one.
func sessionReport(env Env, st *store.Store, top string, si store.SessionInfo, me bool) error {
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
	fmt.Fprintf(env.Stdout, "CLAIMS HELD  %d\n", len(held))
	for i, c := range held {
		if i == maxRows {
			fmt.Fprintf(env.Stdout, "  ...and %d more not shown\n", len(held)-maxRows)
			break
		}
		stale := ""
		if !c.Renewed.IsZero() && c.Stale(now) {
			stale = fmt.Sprintf("  STALE (not renewed %s; still refuses)", age(now, c.Renewed))
		}
		scopes := joinCapped(c.Scopes, 512) // whole items, fenced inside, with a count of what was cut
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
		fmt.Fprintf(env.Stdout, "BASE         %s — an observation at the end of its last reported turn\n",
			strings.TrimPrefix(newBaseReader(env.Cwd).note(now, b), "base "))
	} else {
		fmt.Fprintln(env.Stdout, "BASE         (none recorded — the Stop hook records it at the end of each turn)")
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

	// WAITING — what this session has declared it is waiting on (D-033), with
	// the one Verdict every view renders, when its keep-alive last checked in,
	// and the ledger's newest observation of its requests. "none declared"
	// and never "not waiting": no row means the session has not said (D-016).
	// A register that could not be READ fails the report, like every other
	// register here: "none declared" over an error would be an assertion of
	// absence the ledger never made (Codex code pass).
	w, ok, err := openWaitOf(st, si)
	if err != nil {
		return err
	}
	if ok {
		c, observed := sampleOf(st, si)
		fmt.Fprintf(env.Stdout, "WAITING      on %s — declared %s ago, %s, %s; %s%s\n",
			targetsPhrase(now, w.Targets), span(now.Sub(w.Since)), deadlinePhrase(now, w), lastCheckPhrase(now, w),
			observedRequest(now, c, observed), notePhrase(w))
	} else {
		fmt.Fprintln(env.Stdout, "WAITING      none declared")
	}
	// WAITED ON — the other end (field notes §9's `blocked-on`): the sessions
	// that declared a wait on a claim this one holds. Information; nothing
	// here obliges the holder to anything.
	if len(held) > 0 {
		var waiting []store.Wait
		seen := map[string]bool{}
		for _, c := range held {
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
		if len(waiting) == 0 {
			fmt.Fprintln(env.Stdout, "WAITED ON    by no declared wait")
		} else {
			fmt.Fprintf(env.Stdout, "WAITED ON    by %d session(s): %s\n", len(waiting), waitersPhrase(st, waiting, now))
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
			fmt.Fprintf(env.Stdout, "AUTHORITY    no watched file carries a modification time later than this session's start (%s)\n", unreadNote(unread))
		}
		for _, a := range changed {
			fmt.Fprintf(env.Stdout, "AUTHORITY    %s changed on disk %s ago, AFTER this session started (%s ago) — its copy may be stale\n",
				fence.Line(a.Path, 512), age(now, a.ModTime), age(now, si.Started))
		}
	}

	// EXIT — what the LEDGER would be left holding. A description of
	// consequences, never permission: nothing here says a session may be
	// ended, and nothing that could be printed here would make it so.
	switch {
	case !si.Live():
		fmt.Fprintln(env.Stdout, "EXIT         already ended")
	case len(held) > 0:
		slugs := make([]string, 0, len(held))
		for _, c := range held {
			slugs = append(slugs, c.Slug)
		}
		fmt.Fprintf(env.Stdout, "EXIT         ending now would leave %d claim(s) held — freed only when some session next runs hello, claim or sweep — `buddy release <slug>` first: %s\n",
			len(held), joinCapped(slugs, 512))
	default:
		fmt.Fprintln(env.Stdout, "EXIT         no claims held; the ledger would be left holding nothing (dirty paths, if any, are observations and stay in the tree)")
	}
	return nil
}

// joinCapped renders a list of peer values on one line within max bytes,
// WHOLE items only, and says how many did not fit. The first shape fenced
// the join at the cap, and one 512-byte scope then hid every scope after it
// with nothing to say so — a report that quietly drops rows is
// indistinguishable from a smaller one (Codex code pass, D-027).
func joinCapped(items []string, max int) string {
	var b strings.Builder
	shown := 0
	for _, it := range items {
		piece := fence.Line(it, max)
		if shown > 0 {
			piece = ", " + piece
		}
		if b.Len()+len(piece) > max {
			break
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
