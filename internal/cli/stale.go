package cli

// STALE, keyed on the HOLDER (D-090). One judgement, every view.
//
// THE FAILURE. A claim renews only when a tool call names a path it covers
// (store.Beat), so two kinds of claim go unrenewed while their holder works:
// a `.buddy/slot/` claim, which names a resource no tool call ever touches,
// and a parked lane's claim, held across a wait in which it edits nothing.
// Every view then marked the claim STALE by its renewal alone, and the
// refusal said "holder last renewed 34m ago; it still refuses, so ask the
// operator" about a holder seen five seconds earlier. On one fleet of 144
// sessions, 6 of 7 claim refusals in one bucket carried that note with every
// holder live, and three decisions turned on it: a lane deferred its work, a
// lane told the operator "only you can free a stale claim" about a six-hour
// slot whose holder was running ("6h stale, seems something off?"), and a
// lane re-claimed "so they aren't swept" — stale never sweeps. staleNote's
// own comment said the intent was "the holder had stopped beating"; what it
// computed was the claim's renewal.
//
// THE RULE. Renewal still decides whether there is anything to say: a claim
// renewed inside StaleAfter, or whose renewal was never recorded, says
// nothing, exactly as before. Past it, the HOLDER decides the words:
//
//   - the holder has ended, every process it registered is GONE, or it has
//     not been seen past StaleAfter: STALE, and "ask the operator" — the
//     wording issue #18 introduced, for the case it was written about;
//   - otherwise the holder is live and seen: "claim unrenewed 34m; holder
//     seen 5s ago", and the move is to ask the HOLDER, `buddy msg <slug>`;
//   - a claim whose every scope is a `.buddy/slot/` path, held by a live
//     holder, says NOTHING. A slot is a resource, not a file: no tool call
//     can renew it, so "unrenewed" is true of every slot held past thirty
//     minutes and tells the reader nothing about whether the run it guards
//     is alive. The holder's liveness is the whole story there, and when
//     the holder is live there is no story.
//
// WORDS ONLY. Nothing here reaches a verdict: a claim refuses, renews, is
// orphaned and is swept exactly as before (D-022, D-026, invariant 11). GONE
// is the roster's diagnostic, read the same way — nothing ends on it.

import (
	"fmt"
	"time"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

// holderState is what the ledger knows about a claim's holder, most
// positive evidence first.
type holderState int

const (
	holderLive   holderState = iota // seen inside StaleAfter, and no registered process known GONE
	holderEnded                     // the session said bye
	holderGone                      // it registered processes and none of them exists any more
	holderSilent                    // not seen past StaleAfter
)

// staleKind is the judgement on one claim.
type staleKind int

const (
	staleNone      staleKind = iota // nothing to say
	staleUnrenewed                  // the claim is unrenewed, its holder is live: ask the holder
	staleQuiet                      // the claim is unrenewed and its holder quiet: STALE, ask the operator
)

// staleness is one claim's judgement with the facts its words quote.
type staleness struct {
	kind    staleKind
	holder  holderState
	renewed time.Time // when the claim last renewed
	seen    time.Time // when the holder was last seen
	ended   time.Time // when the holder ended, if it did
}

// holders is what one view reads, once, to judge every claim it prints: the
// registered processes of every session, for GONE. A view that cannot read
// them judges without GONE (procs nil): a listing must not fail for the
// reason a claim must, and every other fact rides the claim row itself.
type holders struct {
	env   Env
	procs map[string][]store.ProcRef
}

func readHolders(env Env, st *store.Store) holders {
	h := holders{env: env}
	if st != nil {
		if procs, err := st.SessionProcs(); err == nil {
			h.procs = procs
		}
	}
	return h
}

// state classifies a holder. GONE needs EVERY registered process gone: a
// second --resume on one id registers a second process, and one of two gone
// is a session that is still there (the roster prints both).
func (h holders) state(now time.Time, si store.SessionInfo) holderState {
	switch {
	case !si.Live():
		return holderEnded
	case h.allGone(si.SessionID):
		return holderGone
	case now.Sub(si.LastSeen) > store.StaleAfter:
		return holderSilent
	default:
		return holderLive
	}
}

func (h holders) allGone(sessionID string) bool {
	procs := h.procs[sessionID]
	if len(procs) == 0 {
		return false // nothing registered is "cannot say", never GONE
	}
	for _, p := range procs {
		if h.env.procAlive(p) {
			return false
		}
	}
	return true
}

// judge is THE staleness rule (see the header). Every view calls it.
func (h holders) judge(now time.Time, c store.ClaimInfo) staleness {
	s := staleness{renewed: c.Renewed, seen: c.Owner.LastSeen, ended: c.Owner.Ended,
		holder: h.state(now, c.Owner)}
	// The zero-clock guard (issue #13): an unrecorded renewal is not 56
	// years old, and "unknown" is not "abandoned".
	if c.State != "open" || c.Renewed.IsZero() || now.Sub(c.Renewed) <= store.StaleAfter {
		return s
	}
	switch {
	case s.holder != holderLive:
		s.kind = staleQuiet
	case !slotClaim(c.Scopes):
		s.kind = staleUnrenewed
	}
	return s
}

// slotClaim reports whether every scope of a claim is a resource slot.
func slotClaim(scopes []string) bool {
	if len(scopes) == 0 {
		return false
	}
	for _, sc := range scopes {
		if !isSlot(sc) {
			return false
		}
	}
	return true
}

// holderPhrase says what the ledger knows about a quiet or live holder.
func (s staleness) holderPhrase(now time.Time) string {
	switch s.holder {
	case holderEnded:
		return "holder ENDED " + age(now, s.ended) + " ago"
	case holderGone:
		return "holder's harness process GONE, last seen " + age(now, s.seen) + " ago"
	case holderSilent:
		return "holder not seen " + age(now, s.seen)
	default:
		return "holder seen " + age(now, s.seen) + " ago"
	}
}

// refusalNote is the tail of a REFUSED line (claim and dry run). It names
// what to do, because that is what issue #18 found missing: a blocked
// session could not tell "somebody is working on this" from "somebody left".
// msgTarget is a name that resolves to the holder (msgTargetFor); it is
// written as a command only when it pastes back as printed (onFlags' rule).
func (s staleness) refusalNote(now time.Time, msgTarget string) string {
	switch s.kind {
	case staleQuiet:
		return fmt.Sprintf("  — STALE: claim unrenewed %s, %s; it still refuses, so ask the operator",
			age(now, s.renewed), s.holderPhrase(now))
	case staleUnrenewed:
		ask := "buddy msg <the holder> (its name cannot be pasted back as printed)"
		if msgTarget != "" && fence.Line(msgTarget, 128) == msgTarget {
			ask = "buddy msg " + shellQuote(msgTarget)
		}
		return fmt.Sprintf("  — claim unrenewed %s; %s; it still refuses, so ask the holder: %s",
			age(now, s.renewed), s.holderPhrase(now), ask)
	}
	return ""
}

// rowNote is the tail of a claim row in `who`/`status` and `whose`. "still
// refuses" rides both arms: `whose` is the first place a session looks to
// decide whether it may write, and a bare STALE reads as permission (D-022).
func (s staleness) rowNote(now time.Time) string {
	switch s.kind {
	case staleQuiet:
		return fmt.Sprintf("  STALE (claim unrenewed %s, %s; still refuses — ask the operator)", age(now, s.renewed), s.holderPhrase(now))
	case staleUnrenewed:
		return fmt.Sprintf("  unrenewed %s, %s (still refuses — ask the holder)", age(now, s.renewed), s.holderPhrase(now))
	}
	return ""
}

// helloMark is the hello digest's bracket. Only a quiet holder earns one: the
// digest is budgeted (D-036) and read by every session at start, and an
// unrenewed claim of a live holder needs no action from any of them.
func (s staleness) helloMark(now time.Time) string {
	if s.kind != staleQuiet {
		return ""
	}
	return " [STALE: " + s.holderPhrase(now) + "]"
}

// conflictNotes judges a conflict set for printConflicts. A Conflict carries
// the holding claim's slug, and an open slug is one holder, so the claim row
// (with its owner) is found by slug. One that cannot be found, or whose holder
// is not the one the refusal named — released between the refusal and this
// read and re-taken by another session, or a ledger that would not answer —
// falls back to the renewal alone and today's wording, which is the
// conservative direction: it sends the reader to the operator, never to a
// holder that may not exist.
//
// The holder is matched by session id where the conflict carries one (the
// dry run) and by label always: the refusal path rebuilds its set from
// store.ErrRefused, which carries the label and not the id, so without the
// label a slug released and re-taken by a live session between the two reads
// would judge the new holder's claim under the old holder's name (Codex code
// pass). Residual: the same label worn twice (hello warns about it).
func conflictNotes(env Env, st *store.Store, conflicts []store.Conflict, now time.Time) []string {
	bySlug := map[string]store.ClaimInfo{}
	if st != nil {
		if open, err := st.Claims(false); err == nil {
			for _, c := range open {
				bySlug[c.Slug] = c
			}
		}
	}
	h := readHolders(env, st)
	out := make([]string, len(conflicts))
	for i, cf := range conflicts {
		c, ok := bySlug[cf.Slug]
		if !ok || c.Owner.Label != cf.Claimant || (cf.Session != "" && c.Owner.SessionID != cf.Session) {
			out[i] = renewalOnlyNote(now, cf.Renewed)
			continue
		}
		c.Renewed = cf.Renewed // the refusal's own reading of the renewal
		out[i] = h.judge(now, c).refusalNote(now, msgTargetFor(st, cf.Slug, c.Owner.SessionID))
	}
	return out
}

// renewalOnlyNote is the wording when the holder cannot be re-read.
func renewalOnlyNote(now, renewed time.Time) string {
	if renewed.IsZero() || now.Sub(renewed) <= store.StaleAfter {
		return ""
	}
	return fmt.Sprintf("  — STALE: claim unrenewed %s, its holder could not be re-read; it still refuses, so ask the operator", age(now, renewed))
}

// msgTargetFor is the name the refusal tells the reader to message: the slug,
// when the slug resolves to the holder, else the holder's session id. A slug
// is the LAST thing a target resolves to (D-013: all, id, label, short form,
// then slug), so a claim slugged `all` printed `buddy msg all` — a broadcast —
// and one slugged like another session's label addressed that session (Codex
// code pass). The id is the second choice because it is long, and exact.
func msgTargetFor(st *store.Store, slug, holder string) string {
	if st != nil {
		if t, err := st.ResolveTarget(slug); err == nil && t.ID == holder {
			return slug
		}
	}
	return holder
}

// gateHolder is the commit gate's description of a claim's holder, the way
// the reader has to act on it. An open claim whose owner has ENDED needs
// `buddy sweep --force`, not a conversation, and calling that owner "live"
// sends the reader to talk to nobody; OwnerOf applies no liveness filter of
// its own, so this is the only place the gate makes the distinction. It was
// commitgate.go's claimState, which tested the CLAIM's renewal and printed the
// holder's last-seen age beside it, so a holder seen a minute ago read "live
// but SILENT for 1m" (D-090).
func (h holders) gateHolder(now time.Time, c store.ClaimInfo) string {
	switch h.state(now, c.Owner) {
	case holderEnded:
		return "session ENDED — nobody will answer; `buddy sweep --force` releases it"
	case holderGone:
		return "live but its harness process is GONE, last seen " + age(now, c.Owner.LastSeen) + " — it may be gone"
	case holderSilent:
		return "live but SILENT for " + age(now, c.Owner.LastSeen) + " — it may be gone"
	default:
		return "live, last seen " + age(now, c.Owner.LastSeen)
	}
}
