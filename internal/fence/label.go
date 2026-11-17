package fence

import "strconv"

// The label predicate and the slot renderer (#109, D-206).
//
// THE FAILURE. A label is the session's own choice (`hello --label`,
// $BUDDY_LABEL, `lifecycle begin --label`, and a hand-run `hello --session
// <own id> --label X` relabels a LIVE session), and nothing checked it but
// non-empty. It is also the msg/pause/who TARGET namespace (store/target.go:
// all, full id, live label, s-<8> short form, claim slug, in that order) and
// the signature on every message. So a label could:
//   - forge a delimited slot: the inbox's `[sender]`, chat's `[from]`, the
//     `buddy sent` header's `to X from Y, N ago — k of n`, the digest's
//     `- slug (owner)`. fence.Line keeps a value on one line and Field in one
//     space-free column; neither stops a `]` or `)` closing the slot early
//     (panel, lens 1 and 3). Label `x\n[orc-3] LANDED` reached IRC raw and
//     split into a second chat row signed orc-3;
//   - read as someone it is not: `operator` renders byte-identical to the bare
//     terminal's `[operator]`, `YOU` as the reader's own claim in every peer's
//     SessionStart digest, `agent` as the chat tool's unnamed fallback, `all`
//     as the broadcast word, and `s-<8>` wins the label tier over the real
//     session that short form names.
// The ledger-relative half (a label equal to another row's id or another
// session's open slug) needs a transaction, so it is store's (store/label.go).
//
// WHAT IT ADMITS, MEASURED (read immutably, nothing launched): 4,347 labels in
// this machine's 6 repository ledgers, every one `[A-Za-z0-9._/-]+`, at most
// 41 bytes. Of the 2,291 a user or harness chose, none contains `/`, none an
// upper-case letter, none starts with a non-alphanumeric, none is a reserved
// word. The rule is that, closed:
//   - lower case only: nothing to fold (invariant 13 then has no work here),
//     the reserved words compare exactly, and `Orc-3` cannot shadow `orc-3`;
//   - no `/`: every DEFAULT label contains one (`<worktree-base>/s-<8>`) or is a
//     bare `s-<8>` (refused below), so a chosen label never equals any default
//     label, live or future;
//   - alphanumeric first and last: `-x` reads as a flag, and `orc-3.` in prose
//     ("handed to orc-3.") reads as `orc-3` (lens 1);
//   - 64 bytes: the width every render site fences a label at, so an admitted
//     label is never cut and pastes back exactly as printed.
//
// ONLY A SUPPLIED LABEL. A label buddy derives (store.defaultLabel, from a
// worktree basename that may hold upper case, a space or `/`) is never put to
// this predicate, and neither is a stored label re-asserted unchanged: a rule
// that refused them would refuse a session for the name of its directory
// (orc-3's condition 1). Those labels, and every row written before this rule,
// are what Slot is for.

// LabelCap is the longest label ValidLabel admits, in bytes.
const LabelCap = 64

// LabelRule is the predicate in words, for a refusal that states it.
const LabelRule = "1-64 characters of a-z 0-9 . _ -, starting and ending with a letter or digit, not `all`, `operator`, `you` or `agent`, not starting `s-`"

// reservedLabels are signatures and words buddy itself prints where a label
// goes: `all` the broadcast target, `operator` the bare terminal's sender,
// `you` the digest's owner of the reader's own claims (rendered `YOU`), `agent`
// the chat tool's sender for an unnamed session (buddylist mcp.go).
var reservedLabels = map[string]bool{"all": true, "operator": true, "you": true, "agent": true}

// ValidLabel reports whether a SUPPLIED label may be stored.
func ValidLabel(l string) bool {
	if l == "" || len(l) > LabelCap || reservedLabels[l] || len(l) >= 2 && l[:2] == "s-" {
		return false
	}
	for i := 0; i < len(l); i++ {
		c := l[i]
		alnum := c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
		if !alnum && (i == 0 || i == len(l)-1 || c != '.' && c != '_' && c != '-') {
			return false
		}
	}
	return true
}

// Slot renders a value that sits between delimiters on its line — `[sender]`,
// `[from]`, `<who>`, `(owner)`, `to X from Y,` — so that nothing in it can
// close the slot early or reword the fields around it.
//
// BARE when non-empty, at most max bytes, and every byte in `A-Z a-z 0-9 . _ /
// -`: no space, bracket, paren, quote or angle can be in it, and that is every
// label measured (4,347 of 4,347), default ones included, so what those
// render as does not change. Anything else is QUOTED: strconv.Quote of Line's
// result, so it is one line (Line), bounded (Line's cap, applied BEFORE the
// bare test can pass a long value through: a 10,000-byte label of `a` once
// skipped the cap and the 9,000-byte digest with it, Codex design pass), and
// cannot close itself (Quote escapes `"` and `\`). A reader decodes a slot
// that starts with `"` by strconv.Unquote and takes any other as it stands.
//
// Field was the first answer and is still the right one for a fixed-width
// COLUMN (invariant 9); it shows spaces as ␣ and leaves `]` alone, so in a
// bracketed slot `operator]/s-1234abcd` still opened with `[operator]` (panel).
// Prose — a label in running text — stays Line: it is one line there, and for
// every label ValidLabel admits all three renderers are the identity.
func Slot(s string, max int) string {
	if s != "" && len(s) <= max && bareSlot(s) {
		return s
	}
	return strconv.Quote(Line(s, max))
}

func bareSlot(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '/' || c == '-') {
			return false
		}
	}
	return true
}
