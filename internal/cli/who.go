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
	"io"
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
	st, rc, err := mustFleetLedger(env.Cwd, env)
	if err != nil {
		return err
	}
	defer st.Close()
	ledgerLine(env, rc)
	si, err := whoAmI(st, env, session)
	if err != nil {
		return err
	}
	return sessionReport(env, st, rc, si, true, "", false)
}

// cmdWho. With no argument it reports the caller, exactly as `status` does
// (D-092): "who am I" is the question a session typing a bare `buddy who`
// was asking — a usage error cost it a round trip on one fleet, and
// refusing with "use status" would answer a question it understood with a
// lecture. D-031 refuses what a verb does not understand; an absent target
// is understood.
//
// --exit prints the EXIT line alone (#143). 2,423 of 2,588 `who` calls on one
// machine were piped, 330 of them `| grep EXIT`, and a pipe hands back the
// filter's status, so a report that failed read as exit 0. The line is the
// full report's own, computed by the same code; only what is printed narrows.
func cmdWho(args []string, env Env) error {
	// named, not target != "": an empty argument (`who "$X"`, X unset) is a
	// target that names nothing, refused by ResolveTarget as before, never
	// the caller and never a slot the next word fills (Codex code pass).
	target, named := "", false
	rest := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		target, named, rest = args[0], true, args[1:]
	}
	fs := flag.NewFlagSet("who", flag.ContinueOnError)
	exitOnly := fs.Bool("exit", false, "print the EXIT line alone")
	if help, err := parseFlags(fs, rest, usageWho, env); help || err != nil {
		return err
	}
	extra := fs.Args()
	if !named && len(extra) > 0 && !strings.HasPrefix(extra[0], "-") {
		// `who --exit <target>`: the target after the flag.
		target, named, extra = extra[0], true, extra[1:]
	}
	if len(extra) > 0 {
		// One target, as before: `who a b` reporting a alone, or a flag after
		// the target the parser stopped short of, is refused, never dropped.
		return usageRefusal(fmt.Sprintf("who takes one target, got %s after it — flags after it would be IGNORED%s",
			strconv.Quote(fence.Line(extra[0], 64)), unsplitHint(extra[0])), usageWho)
	}
	st, rc, err := mustFleetLedger(env.Cwd, env)
	if err != nil {
		return err
	}
	defer st.Close()
	if !named {
		// No name: the caller, as `status` (D-092).
		if !*exitOnly {
			ledgerLine(env, rc)
		}
		si, err := whoAmI(st, env, "")
		if err != nil {
			return err
		}
		return sessionReport(env, st, rc, si, true, "", *exitOnly)
	}
	if !*exitOnly {
		ledgerLine(env, rc)
	}
	t, err := st.ResolveTarget(target)
	if err != nil {
		// A slash is how a path reads, and `who .buddy/slot/x` asked about a
		// path (one fleet's refusals). Labels carry slashes too, so this
		// suggests and never redirects.
		if errors.Is(err, store.ErrNoSuchTarget) && strings.Contains(target, "/") {
			if rc.fleet != "" {
				// whose is refused in a fleet (it keeps no paths); a slot's
				// holder is on ls (the refusals lens, finding 2).
				return fmt.Errorf("%s; if it is a slot, `buddy ls` lists who holds each", fence.Line(err.Error(), 512))
			}
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
		return fmt.Errorf("%s resolved to session %s, which is not in the ledger", fence.Line(target, 128), fence.Line(t.ID, 128))
	}
	// The caller's own row wears the same marker the roster gives it, best
	// effort for the reason cmdSessions gives: a listing must not fail for
	// the reason a claim must.
	me := false
	if self, err := whoAmI(st, Env{Cwd: env.Cwd, Getenv: env.Getenv, Now: env.Now}, ""); err == nil && self.SessionID == si.SessionID {
		me = true
	}
	if t.Via == "slug" && !*exitOnly {
		// D-094: every scope, uncut, so "re-claim with the SAME scopes" can be done.
		if err := claimScopesBlock(env, st, t.Slug); err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "%s is claim %s, held by:\n", fence.Line(target, 128), strconv.Quote(fence.Line(t.Slug, 128)))
	}
	onSlug := ""
	if t.Via == "slug" {
		onSlug = t.Slug
	}
	return sessionReport(env, st, rc, si, me, onSlug, *exitOnly)
}

// sessionReport renders every register the ledger holds about one session.
// Every value is peer text and fenced; every list is capped like `whose`'s,
// with the total stated when the cap bites, because a report that quietly
// drops rows is indistinguishable from a smaller one. onSlug is the open
// claim the target was named by, "" for any other name: WAITED ON then
// speaks for that claim alone (D-092).
func sessionReport(env Env, st *store.Store, rc repoContext, si store.SessionInfo, me bool, onSlug string, exitOnly bool) error {
	top := rc.top
	// exitOnly (--exit, #143): every register below is still READ, because
	// the EXIT line is built from them, and printed nowhere; only the two that
	// leave the ledger for git or the filesystem (BASE, AUTHORITY) are not
	// read, since EXIT does not use them.
	out := env.Stdout
	if exitOnly {
		out = io.Discard
	}
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
	fmt.Fprintln(out, head.String())

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
	fmt.Fprintf(out, "CLAIMS HELD  %d\n", len(held))
	for i, c := range held {
		if i == maxRows {
			fmt.Fprintf(out, "  ...and %d more not shown\n", len(held)-maxRows)
			break
		}
		// The holder's words, not the renewal's (D-090).
		stale := hs.judge(now, c).rowNote(now)
		scopes := scopeList(c.Scopes) // whole items, one token each, with a count of what was cut
		fmt.Fprintf(out, "  %-24s held %-4s %s%sscopes: %s%s\n",
			fence.Field(c.Slug, 128), age(now, c.Created), sharedWord(c.Shared), parkWords(c), scopes, stale)
	}

	// DIRTY PATHS — observations (invariant 10): a tool call by this session
	// named the path and git has not since reported it clean. Not a lock,
	// and said so on the line.
	dirty, err := st.DirtyPathsOf(si.SessionID)
	if err != nil {
		return err
	}
	// A row in a worktree that is gone is hidden and counted (#125, dirty.go's
	// goneWorktrees); nothing is deleted. Not under --exit: EXIT does not
	// read this register.
	hidden := 0
	var paths []string
	if !exitOnly {
		gone := goneWorktrees(top)
		for _, d := range dirty {
			if gone(d.Worktree) {
				hidden++
				continue
			}
			paths = append(paths, d.Path)
		}
	}
	switch {
	case len(paths) > 0:
		goneTail := ""
		if hidden > 0 {
			goneTail = fmt.Sprintf("; %d more recorded in a worktree that no longer exists, not shown", hidden)
		}
		fmt.Fprintf(out, "DIRTY PATHS  %d recorded to this session (observations, not locks): %s%s\n",
			len(paths), joinCapped(paths, 512), goneTail)
	case hidden > 0:
		fmt.Fprintf(out, "DIRTY PATHS  (none in a worktree that exists; %d recorded in a worktree that no longer exists, not shown)\n", hidden)
	default:
		fmt.Fprintln(out, "DIRTY PATHS  (none recorded)")
	}

	// BASE (D-038) — the commit this session's tree was on when its last
	// reported turn ended, and where that stands against main NOW. Printed
	// as "(none recorded)" rather than omitted (D-023): absent means the Stop
	// hook has not reported, not that the tree is current.
	var bases map[string]store.Base
	if !exitOnly {
		if bases, err = st.Bases(); err != nil {
			return err
		}
	}
	if exitOnly {
		// Not read: EXIT does not use it, and its note runs git.
	} else if rc.fleet != "" {
		// The base sample is git's, and idle records none in a fleet (D-191).
		fmt.Fprintln(out, "BASE         not kept in a fleet ledger")
	} else if b, ok := bases[si.SessionID]; ok && b.Incarnation == si.Incarnation {
		fmt.Fprintf(out, "BASE         %s, as of its last reported turn\n",
			strings.TrimPrefix(newBaseReader(env.Cwd).note(now, b), "base "))
	} else {
		fmt.Fprintln(out, "BASE         (none recorded; the Stop hook records it)")
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
		fmt.Fprintln(out, "INBOX        0 undelivered")
	} else {
		fmt.Fprintf(out, "INBOX        %d undelivered, the oldest %s old\n", len(msgs), age(now, oldestOf(msgs)))
	}
	// ADAPTER — the adapter channel, its own register (review F-24). INBOX
	// counts the legacy channel only, so a full adapter queue used to print
	// as "0 undelivered".
	ab, err := st.AdapterBacklogOf(si.SessionID, si.Incarnation)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "ADAPTER      "+adapterRegister(ab, now))

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
		fmt.Fprintf(out, "WAITING      on %s — declared %s ago, %s, %s; %s%s%s\n",
			waitPhrase(now, w), span(now.Sub(w.Since)), deadlinePhrase(now, w), lastCheckPhrase(now, w),
			observedRequest(now, c, observed), readyPhrase(w), notePhrase(w))
	} else {
		fmt.Fprintln(out, "WAITING      none declared")
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
			fmt.Fprintf(out, "WAITED ON    %sby no declared wait\n", on)
		case riders:
			// A batched run forming (D-049): the holder reads who is in.
			summary, lines := ridersLines(st, waiting, now)
			fmt.Fprintf(out, "WAITED ON    %sby %d session(s), %s:\n", on, len(waiting), summary)
			for _, ln := range lines {
				fmt.Fprintln(out, ln)
			}
		default:
			fmt.Fprintf(out, "WAITED ON    %sby %d session(s): %s\n", on, len(waiting), waitersPhrase(st, waiting, now))
		}
	}

	// AUTHORITY — watched files whose recorded modification time postdates
	// this session's registration (D-028). Advisory: the file on disk
	// changed after the session started; not that its contents differ from
	// what the session read, nor that it has not re-read them since.
	if top != "" && si.Live() && !exitOnly {
		changed, unread, err := authorityChanged(st, top, si)
		if err != nil {
			return err
		}
		if len(changed) == 0 {
			// Worded as the measurement: no watched file carries a later
			// mtime, and how many could not be read at all — not "nothing
			// changed", which a missing file would make untrue.
			fmt.Fprintf(out, "AUTHORITY    no watched file's mtime is later than this session's start (%s)\n", unreadNote(unread))
		}
		for _, a := range changed {
			fmt.Fprintf(out, "AUTHORITY    %s changed on disk %s ago, AFTER this session started (%s ago) — its copy may be stale\n",
				fence.Line(a.Path, 512), age(now, a.ModTime), age(now, si.Started))
		}
	}

	// JOBS — the shells still running under its harness process (jobs.go,
	// D-065). Beside EXIT because EXIT speaks for the ledger only, and was
	// read as speaking for the session.
	line, jobsClause := jobsLine(env, st, si)
	if line != "" {
		fmt.Fprintln(out, line)
	}
	// JOB — each job declared on a claim in focus (job.go, D-131): the other
	// half of JOBS, which sees what is present and never what died. A line only
	// where a job was declared, so every other report reads as it did.
	for _, ln := range declaredJobLines(env, focus, maxRows) {
		fmt.Fprintln(out, ln)
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
	// D-134: what the three clauses above cannot see. A report, so the tail below
	// still speaks for claims alone.
	if c := browserClause(st, si, now); c != "" {
		clauses = append(clauses, c)
	}
	tail := " — ending now would leave no claim"
	if len(held) > 0 {
		slugs := make([]string, 0, len(held))
		for _, c := range held {
			slugs = append(slugs, c.Slug)
		}
		tail = fmt.Sprintf(" — ending now would leave the claim(s) held; `%s release <slug>` first: %s",
			buddyCmd(env), joinCapped(slugs, 512))
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

// ledgerLine names the ledger in use, FIRST, whenever BUDDY_FLEET is set (the
// plan's 9.1: a session can be in a fleet, or launched into one and living
// where a repository's ledger governs, and which ledger a verb read is the
// first question). Printed straight after the open and before any identity or
// target lookup, which can fail and would hide it (Codex design pass, Q5).
// With BUDDY_FLEET unset nothing is printed: today's report, byte for byte.
func ledgerLine(env Env, rc repoContext) {
	f := env.getenv(EnvFleet)
	switch {
	case f == "":
	case rc.fleet != "":
		fmt.Fprintf(env.Stdout, "LEDGER       fleet %s: %s (coordination only: no file claim, gate or brake)\n",
			fence.Field(rc.fleet, 64), fence.Line(rc.ledger, 512))
	default:
		// Stage 2 (D-193): the hooks keep the session on the fleet too, and
		// a typed verb still acts here; `buddy fleet <verb>` acts there. Only
		// a fleet that is there: the tail is what a lane's brief checks for
		// (outside-git.md), and it was printed for a malformed or absent
		// name too (the cross-check). The ledger is found, not opened: a fleet
		// that exists but will not open is the hooks' to report.
		if err := fleetFindable(env, f); err != nil {
			fmt.Fprintf(env.Stdout, "LEDGER       %s, this directory's own (a bare verb acts here); the hooks report to no fleet: %s\n",
				fence.Line(rc.ledger, 512), fence.Line(err.Error(), 512))
			return
		}
		fmt.Fprintf(env.Stdout, "LEDGER       %s, this directory's own (a bare verb acts here); the hooks also report to fleet %s: act there with `buddy fleet <verb>`\n",
			fence.Line(rc.ledger, 512), fence.Field(f, 64))
	}
}
