package cli

// The roster's fold (D-091, amending D-015). `buddy sessions` lists the rows
// an orchestrator can act on, and counts the rest on ONE line naming --all.
//
// THE FAILURE. On one fleet `herdr pane close` ran no SessionEnd hook for 68
// of 119 closed lanes, so their rows never ended; with every lane that did
// end, the roster reached 906 rows and 168 KB — 758 ended, 147 `live STALE
// ... pid N GONE`. Sessions grepped it for their own row (`buddy sessions |
// grep s-xxxx`, 15+ times) or `head -30`'d it and read dead rows. Ending
// those rows automatically was the tempting fix and is refuted by D-025: a
// GONE pid is a diagnostic, a wrongly recorded anchor plus an auto-end is
// the delayed-bye defect by another road, and nothing here ends or changes
// any row. The listing folds; the ledger is untouched.
//
// WHAT FOLDS (the operator's decision, 2026-10-20): ended rows older than
// rosterEndedWindow, and live rows whose EVERY registered process is GONE.
// A live row with no registered process is "cannot say", never GONE (a
// Codex or adapter session has none), so it stays. The caller's own row
// always stays: "which row is mine" is the question the marker answers.
//
// WHY 24 HOURS. Long enough that a lane closed in this working day — the one
// a coordinator is confirming it closed, or whose base it is reading — is
// still on the list; short enough that history does not accumulate. Measured
// on that fleet's ledger (67 days of rows, sampled hourly): a trailing 24h
// window held 11 ended rows on average and 67 at most, against 758 ended in
// all; at the snapshot it held 17. A 6h window averaged 3 and would drop
// lanes closed that morning; a 72h window averaged 34.
//
// WHY THIS IS NOT THE HEADER D-015 CUT. D-015 cut a header line because the
// common case is ONE ROW quoted into chat or a peer's context, where a header
// is gone and the labels must ride with the numbers. The fold line labels no
// column and every row still carries its own words; it is a trailer that
// says rows were left out and how to see them — the rule every capped list
// here follows ("a report that quietly drops rows is indistinguishable from
// a smaller one", D-027). It prints only when something folded.

import (
	"fmt"
	"strings"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// rosterEndedWindow is how long an ended row stays on the default roster;
// the fold line spells it "24h".
const rosterEndedWindow = 24 * time.Hour

// foldRoster splits the roster into the rows the default listing prints and
// the one line that counts the rest ("" when nothing folded).
func foldRoster(h holders, sessions []store.SessionInfo, me string, now time.Time) ([]store.SessionInfo, string) {
	var kept []store.SessionInfo
	ended, gone := 0, 0
	for _, si := range sessions {
		switch {
		case si.SessionID == me && me != "":
			kept = append(kept, si)
		case !si.Live() && now.Sub(si.Ended) > rosterEndedWindow:
			ended++
		case si.Live() && h.allGone(si.SessionID):
			gone++
		default:
			kept = append(kept, si)
		}
	}
	var parts []string
	if ended > 0 {
		parts = append(parts, fmt.Sprintf("%d ended more than 24h ago", ended))
	}
	if gone > 0 {
		parts = append(parts, fmt.Sprintf("%d live with every registered process GONE", gone))
	}
	if len(parts) == 0 {
		return kept, ""
	}
	return kept, "(+ " + strings.Join(parts, ", ") + " — not listed: buddy sessions --all)"
}
