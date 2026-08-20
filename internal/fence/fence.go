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

// Line collapses every line break — ASCII, NEL, and the Unicode-mandatory
// LS/PS — to a visible marker so one message can never fabricate additional
// records or metadata lines (Codex finding: the fence was structurally
// spoofable), strips remaining C0 controls and DEL (no ANSI escapes reach a
// terminal or transcript), and bounds the value at max bytes on a rune
// boundary.
func Line(s string, max int) string {
	// Escape a LITERAL marker first, before any real line break becomes one.
	// Otherwise the marker is not injective: a value already containing \u23ce is
	// byte-indistinguishable from a collapsed newline, so the guarantee every
	// reader advertises ("newlines shown as \u23ce") cannot tell a fabricated
	// break from a real one.
	s = strings.ReplaceAll(s, "\u23ce", "\\u23ce")
	s = strings.NewReplacer(
		"\r\n", "⏎", "\n", "⏎", "\r", "⏎",
		"\u0085", "⏎", "\u2028", "⏎", "\u2029", "⏎",
	).Replace(s)
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
