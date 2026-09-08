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
// ORDER IS MOST-SPECIFIC-FIRST AND MUST STAY DETERMINISTIC, because these
// namespaces genuinely overlap: a claim slug may equal another session's label,
// and a label is what `buddy ls` prints in the owner column. The order is
//
//	all  ->  full session id  ->  label  ->  "s-<8hex>" short id  ->  open claim slug
//
// so the more precisely a form identifies a session, the earlier it wins.
//
// SLUGS RESOLVE ONLY AMONG OPEN CLAIMS. `claims_open_slug` is a UNIQUE index
// over state='open', so an open slug names at most one claim by construction
// and this lookup needs no ambiguity arm. A RELEASED slug is deliberately not
// resolvable: the same slug may have been held by several sessions over time,
// so the answer would silently change as history accumulated.
//
// Matching is exact, never folded. These strings are copied out of `buddy ls`
// and `buddy sessions` output, and the queries on the read side (PausedFor,
// Undelivered) match exactly — a second folding rule here would resolve targets
// those queries cannot match, which is the bug this file removes.
func (s *Store) ResolveTarget(target string) (Target, error) {
	if target == AllTarget {
		return Target{ok: true, Raw: target, ID: AllTarget, Via: "all", Live: true}, nil
	}
	if strings.TrimSpace(target) == "" {
		return Target{}, fmt.Errorf("%w: empty target; name a session, a label, a claim slug, or %q", ErrNoSuchTarget, AllTarget)
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
	all, err := s.sessionsWhere("")
	if err != nil {
		return Target{}, err
	}

	// labelOwner answers "is this string some OTHER session's label?", which is
	// the check the canonical-id collision below needs.
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

	var found SessionInfo
	via := ""
	switch {
	default:
		// Exact session id. The primary key, so at most one.
		for _, si := range all {
			if si.SessionID == target {
				found, via = si, "id"
			}
		}
		if via != "" {
			break
		}

		// Exact label. NOT unique — there is no index on sessions.label and
		// Hello never checks one, so `hello --label alpha` twice is legal. The
		// first version took LIMIT 1 and silently picked one, which is also a
		// REGRESSION: the old code stored the label, and both readers' label
		// arm matched every session carrying it.
		var byLabel []SessionInfo
		for _, si := range all {
			if si.Label == target {
				byLabel = append(byLabel, si)
			}
		}
		if len(byLabel) > 1 {
			return Target{}, ambiguous("label", byLabel)
		}
		if len(byLabel) == 1 {
			found, via = byLabel[0], "label"
			break
		}

		// The short form: the last segment of a default label
		// "<worktree-base>/s-<8hex>" (see defaultLabel), which is how sessions
		// are named in the room, in `buddy ls`, and in what peers write. Two
		// sessions whose ids share a first-8-hex prefix live in different
		// worktrees, so they carry different labels but the SAME short form.
		if strings.HasPrefix(target, "s-") && !strings.Contains(target, "/") {
			var byShort []SessionInfo
			for _, si := range all {
				if strings.HasSuffix(si.Label, "/"+target) {
					byShort = append(byShort, si)
				}
			}
			if len(byShort) > 1 {
				return Target{}, ambiguous("short id", byShort)
			}
			if len(byShort) == 1 {
				found, via = byShort[0], "short"
				break
			}
		}

		// The claim slug: the form peers actually use, and the reason this file
		// exists. Unique among OPEN claims by the claims_open_slug partial
		// index, so at most one row can match and there is no ambiguity arm.
		_, owner, taken, err := openSlugOwner(s.db, target)
		if err != nil {
			return Target{}, err
		}
		if taken {
			for _, si := range all {
				if si.SessionID == owner {
					found, via = si, "slug"
				}
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
		return Target{}, fmt.Errorf("%w: %q resolves to session %s, which is also the label of %s — pause/msg would reach both; use a full session id",
			ErrAmbiguousTarget, target, found.SessionID, other.SessionID)
	}
	// Same hazard with the reserved word: a session whose id is literally
	// "all" would turn a targeted row into a fleet-wide one.
	if found.SessionID == AllTarget {
		return Target{}, fmt.Errorf("%w: %q resolves to a session whose id is the reserved word %q, which every reader treats as fleet-wide",
			ErrAmbiguousTarget, target, AllTarget)
	}

	t := Target{ok: true, Raw: target, ID: found.SessionID, Label: found.Label, Via: via, Live: found.Live()}
	if via == "slug" {
		t.Slug = target
	}
	return t, nil
}

// sessionsWhere is the shared row reader for session lookups that can return
// more than one row.
func (s *Store) sessionsWhere(where string, args ...any) ([]SessionInfo, error) {
	rows, err := s.db.Query(`SELECT session_id, incarnation, label, worktree, pid, started, last_seen, COALESCE(ended,0)
		FROM sessions `+where+` ORDER BY label`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionInfo
	for rows.Next() {
		var si SessionInfo
		if err := rows.Scan(&si.SessionID, &si.Incarnation, &si.Label, &si.Worktree,
			&si.PID, unixScan{&si.Started}, unixScan{&si.LastSeen}, unixScan{&si.Ended}); err != nil {
			return nil, err
		}
		out = append(out, si)
	}
	return out, rows.Err()
}
