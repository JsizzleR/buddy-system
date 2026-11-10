package store

import (
	"errors"
	"fmt"
	"strings"
)

// Target resolution for the three verbs that address ANOTHER session: `pause`,
// `resume` and `msg`.
//
// THE FAILURE THIS EXISTS TO END. All three took one argument the help calls
// "<session|label|all>" and wrote it into the ledger verbatim. The matching
// queries on the other side are exact — PausedFor matches (session_id, label,
// "all"), Undelivered matches (session_id, label) — so a target naming nothing
// produced a row matching nothing. Every one of those verbs then printed
// success and exited 0. Measured on this machine's busiest ledger: 31 targeted
// inbox messages, 16 never delivered, and 8 of those addressed in forms the
// delivery query cannot match — five bare "s-<8hex>" short ids and three CLAIM
// SLUGS. `pause` shares the namespace, so `buddy pause <slug>` reported that it
// would take effect on the target's next tool call and paused nobody. That had
// never been caught because `controls` has zero rows in that ledger: the
// operator's brake has never been used in anger.
//
// SLUGS ARE THE FORM THAT MATTERS. D-009 measured a mentions filter built from
// session id and label at ZERO matches over 2313 live room messages — peers
// address each other by claim slug, which lives in the claims table and which
// no target query consulted.
//
// WHY A TYPE AND NOT A STRING. Pause/Resume/Msg take a Target, so the compiler
// is what stops an unresolved string reaching a control or inbox row. A guard's
// review scope is every site that bypasses it; making the resolved value the
// only accepted argument means there is no such site to review.
//
// WHY RESOLUTION HAPPENS AT THE WRITE BOUNDARY. The read side is the gate's hot
// path — PausedFor runs on every mutating tool call against a 100 ms hook
// budget (gate measures 20 ms). Resolving to a canonical session id at write
// time leaves both matching queries exactly as they were, so this change costs
// the gate nothing and cannot alter what an EXISTING row matches.

// ErrNoSuchTarget is returned when a target names no session. It is the whole
// point of this file: the verb refuses and writes nothing, rather than
// reporting success over a row that can never match.
var ErrNoSuchTarget = errors.New("no such target")

// ErrAmbiguousTarget is returned when a short id could name more than one
// session. Refusing beats picking: the operator gets to say which, and a
// silent choice here would pause or message the wrong session.
var ErrAmbiguousTarget = errors.New("ambiguous target")

// AllTarget is the reserved fleet-wide word. It is not a name to resolve — no
// session may shadow it, because a session labelled "all" would otherwise
// swallow every broadcast the operator sent.
const AllTarget = "all"

// Target is a resolved address: the canonical session id a row should carry,
// plus how it was reached, for an echo the operator can check.
type Target struct {
	// ok is set ONLY by ResolveTarget. The exported fields make a Target
	// readable and a hand-built one constructible, so "the compiler is the
	// guard" was overstated: the type stops a STRING reaching a row, and this
	// unexported marker is what stops a forged or zero Target. Pause, Resume
	// and Msg refuse a Target without it.
	ok bool

	Raw   string // exactly what the operator typed
	ID    string // canonical session id, or AllTarget
	Label string // the session's label; empty for AllTarget
	Slug  string // the open claim slug, when Via == "slug"
	Via   string // "all" | "id" | "label" | "short" | "slug"
	Live  bool   // the session has not ended (always true for AllTarget)
	// Incarnation is the session's incarnation AS RESOLVED; empty for
	// AllTarget. A write that binds the target to a row (TransferClaim, D-063)
	// re-reads it inside its transaction and refuses a change: a successor
	// that byes and re-hellos between resolution and the write is a different
	// incarnation from the one that answered.
	Incarnation string
}

// check is the runtime half of the guarantee. The exported fields make a
// Target readable — and constructible — so Pause/Resume/Msg verify that this
// value actually came from ResolveTarget rather than from a struct literal.
// Without it the type only stops a bare string, and a zero Target would insert
// an empty target: the exact row that matches nothing and reports success.
func (t Target) check() error {
	if !t.ok || t.ID == "" {
		return fmt.Errorf("%w: target was not resolved (build it with ResolveTarget)", ErrNoSuchTarget)
	}
	return nil
}

// String renders the resolution for an operator-facing echo. Callers fence it:
// Label and Slug are peer-controlled free text.
func (t Target) String() string {
	switch t.Via {
	case "all", "id", "label":
		return t.Raw
	case "slug":
		return fmt.Sprintf("%s (claim %s)", t.Label, t.Slug)
	default:
		return fmt.Sprintf("%s (%s)", t.Label, t.Raw)
	}
}

// ResolveTarget maps an operator's argument onto exactly one session, or
// refuses.
//
// ORDER IS MOST-SPECIFIC-FIRST, LIVE BEFORE ENDED, AND MUST STAY
// DETERMINISTIC, because these namespaces genuinely overlap: a claim slug may
// equal another session's label, and a label is what `buddy ls` prints in the
// owner column. The order is
//
//	all  ->  full session id  ->  LIVE: label -> "s-<8hex>" short id -> open claim slug
//	                          ->  ENDED: the same three arms, only when nothing live matched
//
// so the more precisely a form identifies a session, the earlier it wins, and
// a session that can act wins over one that cannot.
//
// WHY LIVE FIRST (#80). Session rows are never deleted, and the arms used to
// run over every row. In the ordinary fleet pattern (lane `fix-74` claims slug
// `fix-74`, hands it to its successor with `release --to`, says bye) `pause
// fix-74` resolved to the ENDED lane by its label, and the brake missed the
// live holder of the work; and a label worn by one ended and one live session
// was refused as ambiguous for good, since the ended row never goes away.
// `hello`'s twin warning already counted live sessions only; resolution now
// agrees with it. The ended tier is kept, not dropped: D-013 decided a message
// to a just-ended lane is queued against its id (and warned), not refused.
// "Live" is SessionInfo.Live, the predicate every view uses, not a second one:
// "not ended". A lane killed without a bye stays live until something ends
// it, and still wins by its label (refusal lens); a GONE process is not read
// here.
//
// THE EXACT ID STAYS AHEAD OF BOTH TIERS, ended or not: it is the remedy every
// ambiguity names, so nothing spelled like it may capture it.
//
// AMBIGUITY IS DECIDED INSIDE A TIER. Two live sessions wearing one label
// still refuse, naming only the live candidates; one live and any number of
// ended resolve to the live one; with nothing live, two ended twins refuse
// rather than pick (recency is not the operator's intent: Codex design pass).
// An ambiguity ENDS resolution; it never falls through to a later arm.
//
// SLUGS RESOLVE ONLY AMONG OPEN CLAIMS. `claims_open_slug` is a UNIQUE index
// over state='open', so an open slug names at most one claim by construction
// and this lookup needs no ambiguity arm. A RELEASED slug is deliberately not
// resolvable: the same slug may have been held by several sessions over time,
// so the answer would silently change as history accumulated. An open claim
// whose holder has ENDED (not yet swept) belongs to the ended tier.
//
// Matching is exact, never folded. These strings are copied out of `buddy ls`
// and `buddy sessions` output, and the queries on the read side (PausedFor,
// Undelivered) match exactly — a second folding rule here would resolve targets
// those queries cannot match, which is the bug this file removes.
func (s *Store) ResolveTarget(target string) (Target, error) {
	if target == AllTarget || strings.TrimSpace(target) == "" {
		return resolveFrom(target, nil, "", false)
	}
	// MATCHING HAPPENS IN GO, NOT IN SQL. The first version matched the short
	// form with LIKE, and both of that operator's defaults are wrong here:
	// its metacharacters are not escaped, so a claim slug of the form
	// "s-1111aaa_" was captured by the SHORT arm — which runs first — and
	// pointed at whichever session's label it happened to match; and its
	// default collation folds ASCII case, so "s-1111AAAA" resolved though this
	// file documents matching as exact and never folded. Both verified against
	// sqlite directly. The session table is small and this is the write path,
	// never the gate's, so reading it whole costs nothing worth a subtle query.
	all, owner, taken, err := s.targetSnapshot(target)
	if err != nil {
		return Target{}, err
	}
	return resolveFrom(target, all, owner, taken)
}

// resolveFrom is ResolveTarget over rows already read: every session row and
// the open slug's owner. It reads nothing, so the same rule runs over a read
// snapshot (ResolveTarget) and inside a write transaction (reresolve), and the
// two cannot drift apart (#86).
func resolveFrom(target string, all []SessionInfo, owner string, taken bool) (Target, error) {
	if target == AllTarget {
		return Target{ok: true, Raw: target, ID: AllTarget, Via: "all", Live: true}, nil
	}
	if strings.TrimSpace(target) == "" {
		return Target{}, fmt.Errorf("%w: empty target; name a session, a label, a claim slug, or %q", ErrNoSuchTarget, AllTarget)
	}

	// labelOwner answers "is this string some OTHER session's label?", which is
	// the check the canonical-id collision below needs. It reads EVERY row,
	// ended ones included, on purpose (Codex design pass): an ended session
	// revives under its own id and label, and a row stored against an id that
	// is its label would brake it then.
	labelOwner := func(v, except string) (SessionInfo, bool) {
		for _, si := range all {
			if si.Label == v && si.SessionID != except {
				return si, true
			}
		}
		return SessionInfo{}, false
	}
	ambiguous := func(form string, hits []SessionInfo) error {
		names := make([]string, 0, len(hits))
		for _, h := range hits {
			names = append(names, h.Label+" ("+h.SessionID+")")
		}
		return fmt.Errorf("%w: %q names %d sessions as a %s (%s); use a full session id",
			ErrAmbiguousTarget, target, len(hits), form, strings.Join(names, ", "))
	}

	// among runs the label, short-form and slug arms, in that order, over one
	// tier's rows. It reports via == "" when nothing in the tier matched, and
	// an error (an ambiguity) that ends resolution outright.
	among := func(rows []SessionInfo) (SessionInfo, string, error) {
		// Exact label. NOT unique — there is no index on sessions.label and
		// Hello never checks one, so `hello --label alpha` twice is legal. The
		// first version took LIMIT 1 and silently picked one, which is also a
		// REGRESSION: the old code stored the label, and both readers' label
		// arm matched every session carrying it.
		var byLabel []SessionInfo
		for _, si := range rows {
			if si.Label == target {
				byLabel = append(byLabel, si)
			}
		}
		if len(byLabel) > 1 {
			return SessionInfo{}, "", ambiguous("label", byLabel)
		}
		if len(byLabel) == 1 {
			return byLabel[0], "label", nil
		}

		// The short form: the last segment of a default label
		// "<worktree-base>/s-<8hex>" (see defaultLabel), which is how sessions
		// are named in the room, in `buddy ls`, and in what peers write. Two
		// sessions whose ids share a first-8-hex prefix live in different
		// worktrees, so they carry different labels but the SAME short form.
		if strings.HasPrefix(target, "s-") && !strings.Contains(target, "/") {
			var byShort []SessionInfo
			for _, si := range rows {
				if strings.HasSuffix(si.Label, "/"+target) {
					byShort = append(byShort, si)
				}
			}
			if len(byShort) > 1 {
				return SessionInfo{}, "", ambiguous("short id", byShort)
			}
			if len(byShort) == 1 {
				return byShort[0], "short", nil
			}
		}

		// The claim slug: the form peers actually use, and the reason this file
		// exists. Unique among OPEN claims by the claims_open_slug partial
		// index, so at most one row can match and there is no ambiguity arm.
		if taken {
			for _, si := range rows {
				if si.SessionID == owner {
					return si, "slug", nil
				}
			}
		}
		return SessionInfo{}, "", nil
	}

	var found SessionInfo
	via := ""
	// Exact session id. The primary key, so at most one; ended or live.
	for _, si := range all {
		if si.SessionID == target {
			found, via = si, "id"
		}
	}
	if via == "" {
		var live, ended []SessionInfo
		for _, si := range all {
			if si.Live() {
				live = append(live, si)
			} else {
				ended = append(ended, si)
			}
		}
		for _, tier := range [][]SessionInfo{live, ended} {
			si, v, err := among(tier)
			if err != nil {
				return Target{}, err
			}
			if v != "" {
				found, via = si, v
				break
			}
		}
	}

	if via == "" {
		return Target{}, fmt.Errorf("%w: %q is not a session id, a label, an s-<id> short form, or an open claim slug (see `buddy sessions` and `buddy ls`)",
			ErrNoSuchTarget, target)
	}

	// THE CANONICAL ID MUST NOT BE SOMEBODY ELSE'S NAME. Resolution stores the
	// session id, but the readers still match (session_id, label, "all") — so
	// if another session's LABEL equals this id, the row would reach that
	// session too, and a targeted pause would silently widen. The old code
	// stored the raw argument and hit only the one session. Refusing is the
	// cheap correct answer; the alternative is migrating every legacy row so
	// the readers can drop their label arm, which is a change with its own
	// evidence to gather.
	if other, clash := labelOwner(found.SessionID, found.SessionID); clash {
		// No "use a full session id" here: the full id is the colliding
		// string, so every form refuses alike (refusal lens). `all` reaches
		// a LIVE session only (Codex code pass), so it is offered only when
		// this one is live (Fable cross-check); the other row's label
		// changing is the remedy either way.
		remedy := fmt.Sprintf("%s must wear another label first", other.SessionID)
		if found.Live() {
			remedy = "`all` reaches it (every live session), or " + remedy
		}
		return Target{}, fmt.Errorf("%w: %q resolves to session %s, which is also the label of %s — pause/msg would reach both, by any name; %s",
			ErrAmbiguousTarget, target, found.SessionID, other.SessionID, remedy)
	}
	// Same hazard with the reserved word: a session whose id is literally
	// "all" would turn a targeted row into a fleet-wide one.
	if found.SessionID == AllTarget {
		return Target{}, fmt.Errorf("%w: %q resolves to a session whose id is the reserved word %q, which every reader treats as fleet-wide",
			ErrAmbiguousTarget, target, AllTarget)
	}

	t := Target{ok: true, Raw: target, ID: found.SessionID, Label: found.Label, Via: via, Live: found.Live(), Incarnation: found.Incarnation}
	if via == "slug" {
		t.Slug = target
	}
	return t, nil
}

// targetSnapshot reads every session row and the open slug's owner in ONE
// read snapshot (readSnapshot: a hand-issued BEGIN DEFERRED, which takes no
// write lock under the DSN's _txlock=immediate), so both answers come from
// the same instant.
//
// WHY ONE SNAPSHOT (Codex design pass on #80). Read separately, a resolution
// could name a session that was never the right answer at any moment: ended A
// wears label `x`, live C holds slug `x`; the sessions read sees A ended, A
// then revives as `x` and C hands `x` to B, and the slug read sees B. Before
// the revival the answer was C, after it A, and never B.
//
// THE WRITE THAT FOLLOWS. Msg's is a separate transaction: a message to a
// session that stopped being the answer a moment ago is the ordinary stale
// answer, visible in `buddy sent`. Pause and Resume, the operator's brake,
// re-resolve inside their own write (reresolve, #86).
func (s *Store) targetSnapshot(slug string) (all []SessionInfo, owner string, taken bool, err error) {
	err = s.readSnapshot(func(q querier) error {
		all, owner, taken, err = targetRows(q, slug)
		return err
	})
	if err != nil {
		return nil, "", false, err
	}
	return all, owner, taken, nil
}

// targetRows is what resolution reads: every session row and the open slug's
// owner, over whatever transaction the caller holds.
func targetRows(q querier, slug string) (all []SessionInfo, owner string, taken bool, err error) {
	if all, err = sessionsFrom(q, ""); err != nil {
		return nil, "", false, err
	}
	_, owner, taken, err = openSlugOwner(q, slug)
	return all, owner, taken, err
}

// ErrTargetMoved is a brake verb's refusal when the word it was given no
// longer names the session it named when the command resolved it.
var ErrTargetMoved = errors.New("target moved")

// reresolve resolves t's word again INSIDE the brake's write transaction and
// refuses when the answer is no longer t's session (#86, D-139's third open
// item).
//
// THE WINDOW IT CLOSES. Resolution was one read snapshot and the write another
// transaction, so a revival between them made the word mean someone else:
// ended A wears label `x`, live C holds slug `x`; `pause x` resolves C, A
// revives as `x` before the write, and the row brakes C while `x` now means
// A, and the echo names C as if `x` still did. Inside the write transaction
// (database/sql's Begin is BEGIN IMMEDIATE under this DSN) no writer can move
// a row between this read and the write, so what the verb did and what the
// word meant are one instant.
//
// WHY REFUSE, NOT FOLLOW THE NEW ANSWER (Codex design pass). Following it
// lets `resume x` lift a brake on a session the operator never saw named, and
// lets `pause x` leave the one they meant running while saying "paused". A
// refusal writes nothing, says so, and is re-run in a second; the race needs
// a revival or a slug moving inside milliseconds. The same session id under a
// new incarnation is still the same answer: a brake is on the session, not on
// one incarnation of it.
func reresolve(q querier, t Target) (Target, error) {
	if t.Via == "all" {
		return t, nil
	}
	all, owner, taken, err := targetRows(q, t.Raw)
	if err != nil {
		return Target{}, err
	}
	now, err := resolveFrom(t.Raw, all, owner, taken)
	if err != nil {
		return Target{}, fmt.Errorf("%w: %q named session %s when this command resolved it, and no longer resolves (%v); nothing was written, run it again",
			ErrTargetMoved, t.Raw, t.ID, err)
	}
	if now.ID != t.ID {
		return Target{}, fmt.Errorf("%w: %q named session %s when this command resolved it, and names %s (%q) now; nothing was written, run it again",
			ErrTargetMoved, t.Raw, t.ID, now.ID, now.Label)
	}
	return now, nil
}

// sessionsWhere is the shared row reader for session lookups that can return
// more than one row.
func (s *Store) sessionsWhere(where string, args ...any) ([]SessionInfo, error) {
	return sessionsFrom(s.db, where, args...)
}

func sessionsFrom(q querier, where string, args ...any) ([]SessionInfo, error) {
	rows, err := q.Query(`SELECT session_id, incarnation, label, worktree, pid, terminal, started, last_seen, COALESCE(ended,0)
		FROM sessions `+where+` ORDER BY label`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionInfo
	for rows.Next() {
		var si SessionInfo
		if err := rows.Scan(&si.SessionID, &si.Incarnation, &si.Label, &si.Worktree,
			&si.PID, &si.Terminal, unixScan{&si.Started}, unixScan{&si.LastSeen}, unixScan{&si.Ended}); err != nil {
			return nil, err
		}
		out = append(out, si)
	}
	return out, rows.Err()
}
