package cli

import (
	"fmt"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

// labelRefusedLine is hello's WARNING when a ledger refused the label it was
// given and registered the session under its stored or default label instead
// (D-206). A hook-driven hello never fails on a label: a NEW session's first
// SessionStart that refused would leave it unregistered, a miss for every
// `buddy msg` to its id (D-013): a lifecycle hook's failure costing the
// session what invariant 2 keeps a chat failure from costing (lens 3). A known
// session's refused refresh keeps its row. So it falls back and says so, in
// the digest, where the session reads it.
//
// what names where the label came from (`--label`, `$BUDDY_LABEL`, "the
// label"), where the ledger. The refusal's own text carries the label, as a
// slot, and the rule; fenced to one line and capped, so a 10,000-byte
// $BUDDY_LABEL costs the 9,000-byte digest at most this line (Codex design
// pass). The coordinator sees it only if it looks: `buddy msg <label>` then
// goes wherever store's resolveFrom takes that name, never by this refusal: a
// reserved one keeps its meaning (`all` broadcasts), an id, slug or legacy
// row wearing it reaches that session or refuses as ambiguous, one that
// parses as a flag is a usage refusal, and the rest refuse as no such target
// (D-013). So check `buddy who <label>` after a launch (Fable cross-check,
// Codex re-verify rounds 1 and 2).
func labelRefusedLine(err error, what, where, wearing string) string {
	return fmt.Sprintf("BUDDY: WARNING %s was not taken in %s (%s); you wear %s there, the name peers must address you by (or your full session id)\n",
		what, where, fence.Line(err.Error(), 512), fence.Slot(wearing, 64))
}

// labelRefusal is a hand-run hello's answer to a refused label: the ledger's
// reason, that nothing was written, where the label came from, and the way
// out. A $BUDDY_LABEL one names `env -u`: the pane that set it re-reads it on
// every hand-run hello, including the remedies buddy itself prints (`buddy
// hello --session <id> </dev/null`), which would refuse again (lens 2).
func labelRefusal(err error, from string) error {
	fix := "pass an admitted --label, or none to keep yours"
	if from != "--label" {
		fix = "pass an admitted --label, or run it as `env -u " + EnvLabel + " buddy hello ...` to keep yours"
	}
	// Not fencedErr: that flattens the chain, and the fleet defer must still
	// see ErrLabelRefused to say it as itself (a refusal, not a fleet
	// failure). The store's text is already one line: the label in it is a
	// fence.Slot, the holder a fence.Field.
	return fmt.Errorf("%w; nothing was written; the label came from %s: %s", err, from, fix)
}

// slugWornNote warns a claimer when its new slug is a LIVE peer's label
// (Codex design pass, the reverse of D-206's slug clause): target resolution
// takes the live label tier before the slug tier, so a msg or pause addressed
// to the slug reaches that peer, not the holder. A note, never a refusal: the
// claim reserves exactly what it reserved, and the label was there first.
func slugWornNote(st *store.Store, self, slug string) string {
	all, err := st.Sessions(store.ByLastSeen)
	if err != nil {
		return ""
	}
	// Said from what resolution does (Codex code pass): one live wearer takes
	// the name; two or more, the claimer among them or not, make it refuse as
	// ambiguous. The claimer wearing it alone is the lane convention: silent.
	var others []string
	wearers := 0
	for _, o := range all {
		if o.Live() && o.Label == slug {
			wearers++
			if o.SessionID != self {
				others = append(others, fence.Field(o.SessionID, fence.SessionIDCap))
			}
		}
	}
	switch {
	case len(others) == 0:
		return ""
	case wearers > 1:
		return fmt.Sprintf("note: %s is also the label of %d live sessions, so a msg or pause addressed to it is refused as ambiguous; name this claim's holder by session id, or claim under another slug\n",
			fence.Slot(slug, 128), wearers)
	default:
		return fmt.Sprintf("note: %s is also the label of live session %s, and a msg or pause addressed to it reaches that session, not you; name this claim's holder by session id, or claim under another slug\n",
			fence.Slot(slug, 128), others[0])
	}
}
