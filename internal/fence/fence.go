// Package fence neutralizes untrusted peer/wire text for line-oriented
// output — terminal prints, agent context injections, MCP tool results.
// Anywhere one record renders per line, unfenced text lets a hostile message
// fabricate extra records, forge metadata lines, or drive the terminal with
// escape sequences. Every reader shares this one fence instead of
// remembering to build its own.
package fence

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// breaks collapses every line-break sequence to the marker. Package-level
// because Line runs on every untrusted value a listing renders — a room read
// fences up to 4096-byte bodies row by row — and building the replacer's
// lookup tables per call was pure per-row overhead.
var breaks = strings.NewReplacer(
	"\r\n", "⏎", "\n", "⏎", "\r", "⏎",
	"\u0085", "⏎", "\u2028", "⏎", "\u2029", "⏎",
)

// Line collapses every line break — ASCII, NEL, and the Unicode-mandatory
// LS/PS — to a visible marker so one message can never fabricate additional
// records or metadata lines (Codex finding: the fence was structurally
// spoofable), strips remaining C0 controls and DEL (no ANSI escapes reach a
// terminal or transcript), and bounds the value at max bytes on a rune
// boundary.
func Line(s string, max int) string {
	// Escape a LITERAL marker first, before any real line break becomes one.
	// Otherwise a value already containing \u23ce is byte-indistinguishable
	// from a collapsed newline, and the guarantee every reader advertises
	// ("newlines shown as \u23ce") could not tell a fabricated break from a
	// real one. The property this buys is exactly that: every \u23ce in the
	// output came from this function, so no value fabricates one. It is NOT
	// injective — the six ASCII characters `\u23ce` pass through untouched
	// and render the same as an escaped literal marker — but a reader is
	// never fooled about a LINE by either, which is the fence's contract.
	s = strings.ReplaceAll(s, "\u23ce", "\\u23ce")
	s = breaks.Replace(s)
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\u23ce':
			return r // the marker this function itself inserted above
		case r == '\t':
			return ' '
		case r == '\u00a0':
			return ' ' // NBSP reads as a space and is not one
		case !strconv.IsPrint(r):
			// strconv.IsPrint is the predicate the earlier C0/C1 tests were
			// reaching for, and it also covers the class they missed: Unicode
			// FORMAT characters. Bidi overrides and isolates reorder a filename
			// in the operator's terminal (Trojan Source), and a zero-width space
			// makes one session's label render identically to another's -- and
			// labels are the `buddy msg` / `buddy pause` TARGET namespace, so a
			// spoofable label is an impersonation, not a cosmetic defect.
			return -1
		}
		return r
	}, s)
	if len(s) > max {
		cut := max
		for cut > 0 && !utf8.RuneStart(s[cut]) { // don't split a UTF-8 rune
			cut--
		}
		s = s[:cut] + "…[truncated]"
	}
	return s
}

// Field is Line for a value that occupies a COLUMN in a fixed-width listing:
// it additionally guarantees the result is ONE whitespace-delimited token,
// by showing every space as ␣ the way Line shows every line break as ⏎.
//
// THE FAILURE (issue #6). A listing prints a label with `%-24s`, which is a
// minimum width and not a maximum, and a label is peer free text. A session
// labelled `aaaaaaaaaaaaaaaaaaaaaaaa ended 0s` renders so that the state and
// age columns of its own row are occupied by text it chose: measured on a
// throwaway repo, a field-splitting reader saw state=`ended` age=`0s` for a
// session that was LIVE, with the real state one field further along. Line
// does not stop this and never claimed to — it stops a NEWLINE fabricating a
// whole row, and this needs no newline.
//
// A MARKER, NOT QUOTES. Quoting was the first attempt and it fails the test
// that matters: `strings.Fields(`+"`"+`"a b"`+"`"+`)` is still two tokens, so a reader
// splitting the row is fooled exactly as before and only a human sees the
// difference. A marker makes the value one token for every reader, and it is
// the convention this package already runs on.
//
// NEVER TRUNCATED beyond Line's own cap. A label and a slug are the
// pause/resume/msg target namespace and those matches are exact, so a
// shortened one would read as an addressing target that resolves to nothing.
// As with ⏎, what is rendered is not what you type: a reader who needs the
// literal value reads it from the ledger, not off a column.
func Field(s string, max int) string {
	// Escape a LITERAL marker first, for the reason Line escapes a literal
	// ⏎: otherwise a value containing ␣ is byte-indistinguishable from a
	// space this function replaced, and "every ␣ in the output came from
	// here" stops being true. Same for the empty marker below.
	s = strings.ReplaceAll(s, "␣", "\\u2423")
	s = strings.ReplaceAll(s, "∅", "\\u2205")
	// Line first: it maps tabs and NBSP onto ordinary spaces, so after it
	// runs the only separator left to defend is the space itself.
	out := strings.ReplaceAll(Line(s, max), " ", "␣")
	if out == "" {
		// AN EMPTY COLUMN IS ZERO TOKENS, and the guarantee is ONE (D-017).
		// A field-splitting reader does not see a blank column; it sees the
		// NEXT column's value in this one's position — the same misreading
		// D-017 fixed for an over-wide label, arriving from the other side.
		//
		// Reachable with a non-empty value, which is why a caller cannot
		// prevent it by refusing empties: Line STRIPS non-printing runes, so
		// a label of a single ESC (accepted at hello, which only requires
		// non-empty) fences to "". Codex finding, issue #13: `CLAIMED BY
		// api-work    held 0s` then reads with `held` in the label column.
		return "∅"
	}
	return out
}
