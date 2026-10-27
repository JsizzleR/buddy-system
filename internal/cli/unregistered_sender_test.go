package cli

import (
	"strings"
	"testing"
)

// D-105: a session id in the environment that is not a LIVE row in this
// ledger (absent, or ended) signs `unregistered s-<8hex>`, never "operator"
// and never its --from alone; the bare terminal is unchanged. Each row runs
// the dry run, a real send, and bravo's drain, because the stamp is stored at
// the send and every later view renders the stored string.
func TestUnregisteredSenderIsStampedNotOperator(t *testing.T) {
	const ghost = "66a24e29-1111-2222-3333-444444444444"
	cases := []struct {
		name  string
		env   map[string]string // set for the sends only
		end   bool              // bye sess-a first: an ENDED id
		from  string
		stamp string
		hint  string // the session id as the result line pastes it; "" = no clause
	}{
		{"absent claude id", map[string]string{EnvClaudeSession: ghost}, false, "", "unregistered s-66a24e29", ghost},
		{"absent id with --from", map[string]string{EnvClaudeSession: ghost}, false, "review", "unregistered s-66a24e29 (review)", ghost},
		{"absent codex id", map[string]string{EnvCodexThread: ghost}, false, "", "unregistered s-66a24e29", ghost},
		{"absent BUDDY_SESSION", map[string]string{EnvSession: ghost}, false, "", "unregistered s-66a24e29", ghost},
		// The id is pasted as ONE shell word, or not at all.
		{"an id with a space", map[string]string{EnvSession: "two words"}, false, "", "unregistered s-two word", "'two words'"},
		{"an id with a newline", map[string]string{EnvSession: "two\nlines"}, false, "", "unregistered s-two⏎line", "<your session id>"},
		{"ended id", map[string]string{EnvClaudeSession: "sess-a"}, true, "", "unregistered s-sessa", "sess-a"},
		{"ended id with --from", map[string]string{EnvClaudeSession: "sess-a"}, true, "review", "unregistered s-sessa (review)", "sess-a"},
		// Controls: the arm is keyed on an id that does not resolve, not on
		// any id at all. A live session signs its label; the bare terminal
		// signs what it typed or "operator", as before.
		{"live id", map[string]string{EnvClaudeSession: "sess-a"}, false, "", "alpha", ""},
		{"live id with --from", map[string]string{EnvClaudeSession: "sess-a"}, false, "review", "alpha (review)", ""},
		{"bare terminal", nil, false, "", "operator", ""},
		{"bare terminal with --from", nil, false, "ana", "ana", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			boundedParallel(t)
			f := newFixture(t)
			f.initAndHello(t)
			if tc.end {
				if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "bye"); code != 0 {
					t.Fatalf("bye: %s", errw)
				}
			}
			send := func(dry bool) string {
				t.Helper()
				args := []string{"msg", "bravo"}
				if tc.from != "" {
					args = append(args, "--from", tc.from)
				}
				if dry {
					args = append(args, "--dry-run")
				}
				args = append(args, "take the router bundle")
				for k, v := range tc.env {
					f.env[k] = v
				}
				defer func() {
					for k := range tc.env {
						delete(f.env, k)
					}
				}()
				out, errw, code := f.run(t, f.repo, "", args...)
				if code != 0 {
					t.Fatalf("msg must never be refused for want of a signature: %d %s %s", code, out, errw)
				}
				return out
			}
			clause := "--label <name>` registers you here"
			check := func(what, out string) {
				t.Helper()
				if strings.Count(out, "\n") != 1 {
					t.Fatalf("%s: one result line (D-041): %q", what, out)
				}
				if tc.hint == "" {
					if strings.Contains(out, "unregistered") || strings.Contains(out, clause) {
						t.Fatalf("%s: a registered sender or the bare terminal gets no clause: %q", what, out)
					}
					return
				}
				if !strings.Contains(out, `signed "unregistered `) || !strings.Contains(out, "`buddy hello --session "+tc.hint+" "+clause) {
					t.Fatalf("%s: the line must say the sender is not registered here and how to be: %q", what, out)
				}
			}

			dry := send(true)
			if !strings.Contains(dry, " as "+tc.stamp+" — nothing was queued") {
				t.Fatalf("dry run must forecast the stamp %q: %q", tc.stamp, dry)
			}
			check("dry run", dry)
			if n := f.undeliveredTo(t, "sess-b", "bravo"); n != 0 {
				t.Fatalf("a dry run queues nothing: %d", n)
			}

			check("send", send(false))
			ctx := f.beatB(t)
			if !strings.Contains(ctx, "["+tc.stamp+"] take the router bundle") {
				t.Fatalf("the drained line must carry the stamp %q:\n%s", tc.stamp, ctx)
			}
			if tc.hint != "" && strings.Contains(ctx, "[operator]") {
				t.Fatalf("an unregistered sender must never arrive as the operator:\n%s", ctx)
			}
		})
	}
}

// release --to's notice is signed from the ENVIRONMENT, like every send, not
// from the holder whoAmI found: `--session sess-a` run under an unregistered
// id signs `unregistered s-<8hex>`, and the result line says so (orc-6's
// review, D-105). Control: the holder's own id signs its label, no clause.
func TestReleaseToUnderAnUnregisteredIdSaysSo(t *testing.T) {
	const ghost = "66a24e29-1111-2222-3333-444444444444"
	for _, tc := range []struct {
		env, stamp string
		clause     bool
	}{
		{ghost, "unregistered s-66a24e29", true},
		{"sess-a", "alpha", false},
	} {
		t.Run(tc.stamp, func(t *testing.T) {
			boundedParallel(t)
			f := newFixture(t)
			f.initAndHello(t)
			var out, errw string
			var code int
			f.asSession("sess-a", func() {
				out, errw, code = f.run(t, f.repo, "", "claim", "outer", "--desc", "d", "--scope", "src")
			})
			if code != 0 {
				t.Fatalf("claim: %s %s", out, errw)
			}
			f.asSession(tc.env, func() {
				out, errw, code = f.run(t, f.repo, "", "release", "outer", "--session", "sess-a", "--to", "bravo")
			})
			if code != 0 || !strings.Contains(out, "notice queued for them") {
				t.Fatalf("release --to: %d %s %s", code, out, errw)
			}
			if got := strings.Contains(out, "`buddy hello --session "+tc.env+" --label <name>` registers you here"); got != tc.clause {
				t.Fatalf("the result line must name the registration exactly when the notice is signed unregistered (want %v): %q", tc.clause, out)
			}
			if ctx := f.beatB(t); !strings.Contains(ctx, "["+tc.stamp+"] handed you claim") {
				t.Fatalf("the notice must be signed %q:\n%s", tc.stamp, ctx)
			}
		})
	}
}
