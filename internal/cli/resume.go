package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/JsizzleR/buddy-system/internal/fence"
	"github.com/JsizzleR/buddy-system/internal/store"
)

// The brake verbs' echoes (#86, D-139's second open item).
//
// THE FAILURE. `pause fix-74` reached the live holder of slug fix-74; once
// that slug was released, `resume fix-74` resolved by label to the ENDED lane
// that had worn it, cleared nothing, and printed `cleared 0 pause(s) for
// fix-74`: the brake stayed on (fail-safe), and nothing said whom the word had
// reached or which pause still stood. Both echoes now name the session by id,
// and resume says what it left.
//
// WHY RESUME DOES NOT PREFER A PAUSED SESSION. It was the tempting fix: `fix-74`
// once meant the paused session, so lift that one. But a pause row stores the
// id it resolved to, not the word it was given, so nothing links `fix-74` to
// that row; a resume that guessed would lift a brake the operator did not name.
// It names the standing pauses instead, and the operator names one by its id.
//
// Every value here that a peer controls (a label, a slug, a pre-D-013 row's
// raw text) is rendered through fence.Line, one value per line at most
// (invariant 9). Ids are minted by the harness and printed through it too,
// because the store does not promise their alphabet.

// targetWho is the resolved target as the brake echoes print it: the word's
// resolution and the session it reached, by id, with ENDED when it has.
func targetWho(t store.Target) string {
	if t.Via == "all" {
		return store.AllTarget
	}
	s := fence.Line(t.String(), 128) + " = session " + fence.Line(t.ID, 128)
	if !t.Live {
		s += ", which has ENDED"
	}
	return s
}

// pauseEcho is `pause`'s success line. An ENDED target's row is correct and
// durable (hello revives a session under its own id), so it says when the
// brake bites rather than warning that something was "queued".
func pauseEcho(w io.Writer, t store.Target) {
	when := "takes effect on their next mutating tool call"
	if t.Via != "all" && !t.Live {
		when = "takes effect on its next mutating tool call if it comes back"
	}
	fmt.Fprintf(w, "paused %s — %s\n", targetWho(t), when)
}

// standingName renders one name pause rows stand on: a session's label and
// id, `all`, or a pre-D-013 row's raw text, quoted.
func standingName(p store.StandingPause) string {
	switch {
	case p.Target == store.AllTarget:
		return "all (fleet-wide)"
	case p.Label != "":
		// fence.Field, not Line: the entries share one comma-joined line, and
		// a label `x (s-aaaa), victim` would otherwise forge an entry with an
		// id of its choosing (Fable cross-check). Field shows its spaces as ␣.
		return fence.Field(p.Label, 64) + " (" + fence.Field(p.Target, 128) + ")"
	case p.Target == "":
		return `the empty name (resume all lifts it)`
	case !typable(p.Target):
		return fmt.Sprintf("%q (a pre-D-013 row, shown fenced: only its exact stored text, typed, lifts it unless refused as ambiguous)", fence.Line(p.Target, 128))
	default:
		return fmt.Sprintf("%q (a pre-D-013 row: that word, typed, lifts it unless refused as ambiguous)", p.Target)
	}
}

// typable says a name prints exactly as it is stored, so a reader who types
// what is shown types the row's own text. A fenced name is not that: fencing
// can turn a tab into a space, and `resume` given the shown form then reaches
// a different name, possibly another session's pause (Codex code pass:
// "alpha\tbeta" shown as "alpha beta", B's label, lifting B's brake). The same
// test as msgTargetFor's caller in ghstack.go, plus %q's own: a `"` or `\`
// prints escaped, so a name holding one is not shown as typed either (Fable
// cross-check: `a\b` printed "a\\b", typed as shown, lifted another pause).
func typable(v string) bool { return fence.Line(v, 128) == v && strconv.Quote(v) == `"`+v+`"` }

// resumeEcho is `resume`'s report: what it cleared for whom, then one line per
// pause that still brakes that session, then, when it cleared nothing, the
// pauses standing on others.
func resumeEcho(w io.Writer, r store.ResumeResult) {
	fmt.Fprintf(w, "cleared %d pause(s) for %s\n", r.Cleared, targetWho(r.Target))
	for _, k := range r.Kept {
		if k.Target == store.AllTarget {
			fmt.Fprintf(w, "  still paused: a fleet-wide pause stands; `buddy resume all` lifts it, for every session\n")
			continue
		}
		// The name is a peer's: quoted, never spliced into a command line
		// a reader would paste (refusal lens: a space or `$(...)` in it).
		how := "`buddy resume` given that name lifts it for all of them, unless the name is refused as ambiguous"
		if !typable(k.Target) {
			how = "the name is shown fenced, so typing it as shown would name another string"
		}
		fmt.Fprintf(w, "  still paused: a pause on the name %q also brakes %d other session(s) wearing it, so this left it; %s\n",
			fence.Line(k.Target, 128), k.Wearers, how)
	}
	if r.Cleared > 0 || (len(r.Others) == 0 && r.OthersMore == 0) {
		return
	}
	names := make([]string, 0, len(r.Others))
	for _, o := range r.Others {
		names = append(names, standingName(o))
	}
	more := ""
	if r.OthersMore > 0 {
		more = fmt.Sprintf(" (+%d more)", r.OthersMore)
	}
	// "No pause stood on it" only when that is what the ledger says: a kept
	// row above is a pause standing on it (Codex design pass).
	lead := "pauses standing on others"
	if len(r.Kept) == 0 {
		lead = "no pause stood on it; pauses standing now"
		if r.Target.Via == "all" {
			lead = "no fleet-wide pause stood; pauses standing now"
		}
	}
	fmt.Fprintf(w, "  %s: %s%s; name a session by the id in its parentheses\n", lead, strings.Join(names, ", "), more)
}
