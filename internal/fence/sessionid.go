package fence

// The session-id predicate: the one rule a native or hand-chosen session id
// must meet before anything registers it (D-201).
//
// WHY IT LIVES HERE AND NOT IN STORE. Two programs must agree on it: `buddy`,
// whose ledger refuses to create a session row for an id outside it, and
// buddy-adapter, which accepts a Codex thread id off the wire and then hands it
// to `buddy lifecycle begin`. If the adapter accepted an id the ledger refuses,
// the worker would already exist when registration failed (thread/start runs
// before begin), the controller would close it and leave its start barrier for
// the operator. One predicate, linked by both, closes that gap by
// construction. buddy-adapter links fence and harness and nothing else (`go
// list -deps ./cmd/buddy-adapter`); importing store would link sqlite into it.
// store.ValidSessionID wraps this for the ledger's own refusal.
//
// WHAT IT ADMITS, MEASURED (file names only, nothing launched): 6199 of 6199
// retained Codex rollouts are UUIDv7, 1230 of 1230 Claude transcripts UUIDv4,
// 79 of 79 `session_id` values in docs/admissions UUID-shaped. Every one is
// [0-9a-f-]{36}. The rule is wider than that on purpose, so a hand-run
// `hello --session sess-a` and the test suite's ids keep working, and closed,
// so widening later stays compatible where narrowing would strand rows.
//
// WHAT IT REFUSES, and the failure each class caused or would cause:
//   - whitespace, controls, line breaks, bidi and zero-width characters: they
//     render differently from what they key (fence.Line strips or rewrites
//     them), so a printed id pasted back names a DIFFERENT session — `sess<TAB>x`
//     printed as `sess x` drained another session's mail (the D-105 era fix);
//   - any non-ASCII byte: the default label cut eight BYTES of the id and could
//     split a rune (store.ShortID now cuts safely, but an id that cannot be cut
//     wrong is simpler than a cut that copes);
//   - `/`: a default label is `<worktree>/s-<8>`, so an id containing `/` can
//     equal another session's label, and target resolution then refuses both;
//   - quotes, backticks, `;`, `|`, `$` and the rest of the shell and markdown
//     metacharacters: ids are printed as commands to paste, inside markdown code
//     spans; with this rule shellQuote is the identity and no backtick can end
//     the span early;
//   - a leading `-`: read as a flag even when quoted (`buddy bye --help`);
//   - `:` — no measured id has one (0 of 7,429), so it is left out, not let in.
//   - the reserved target `all` (a session wearing that id could be braked only
//     by `pause all`), and any `s-` prefix: `hello --session s-1234abcd` would
//     win the exact-id tier over a live session whose default label ends in
//     that short form, and take its messages and its pause.
//
// The cap is 128 bytes: the slug cap, and the width every renderer fences an id
// at, so an admitted id is never cut on display and always pastes back as
// printed.

// SessionIDCap is the longest session id the predicate admits, in bytes.
const SessionIDCap = 128

// SessionIDRule is the predicate in words, for a refusal that may state it.
const SessionIDRule = "1-128 characters of A-Z a-z 0-9 . _ -, starting with a letter or digit, not `all`, not starting `s-`"

// ValidSessionID reports whether id may be registered as a session id.
func ValidSessionID(id string) bool {
	if id == "" || len(id) > SessionIDCap || id == "all" || len(id) >= 2 && id[:2] == "s-" {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		alnum := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !alnum && (i == 0 || c != '.' && c != '_' && c != '-') {
			return false
		}
	}
	return true
}
