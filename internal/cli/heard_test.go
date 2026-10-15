package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// F3: a verb a session runs under its harness's native identity records it
// as heard from, so a hookless Codex coordinator that just re-claimed is not
// "NOT SEEN FOR 2h". Only the native identity counts, and nothing revives.

func (f *fixture) lastSeen(t *testing.T, id string) store.SessionInfo {
	t.Helper()
	st, err := store.Open(filepath.Join(f.repo, ".git", "buddy.db"), func() time.Time { return f.clock })
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	si, ok, err := st.SessionByID(id)
	if err != nil || !ok {
		t.Fatalf("session %s: %v %v", id, ok, err)
	}
	return si
}

func TestVerbUnderNativeIdentityIsHeard(t *testing.T) {
	boundedParallel(t)
	cases := []struct {
		name  string
		env   map[string]string
		args  []string
		heard bool
	}{
		// The trial: a Codex thread re-claims.
		{"codex claim", map[string]string{EnvCodexThread: "sess-a"},
			[]string{"claim", "orchestrator", "--desc", "d", "--scope", ".buddy/slot/orchestrator"}, true},
		// A verb that only reads is still the session acting.
		{"codex status", map[string]string{EnvCodexThread: "sess-a"}, []string{"status"}, true},
		{"codex inbox", map[string]string{EnvCodexThread: "sess-a"}, []string{"inbox"}, true},
		{"claude status", map[string]string{EnvClaudeSession: "sess-a"}, []string{"status"}, true},
		// A verb that fails is still the session acting.
		{"codex refused verb", map[string]string{EnvCodexThread: "sess-a"}, []string{"release", "no-such-claim"}, true},
		// --session and BUDDY_SESSION say whom the caller acts FOR: the
		// operator at a terminal is not the session being heard from.
		{"override only", map[string]string{EnvSession: "sess-a"}, []string{"status"}, false},
		{"--session only", map[string]string{}, []string{"status", "--session", "sess-a"}, false},
		// Two native identities that disagree are no identity at all.
		{"disagreeing natives", map[string]string{EnvCodexThread: "sess-a", EnvClaudeSession: "sess-b"}, []string{"status"}, false},
		// Not every verb: diagnose never opens the ledger with SQLite (D-072).
		{"diagnose is not wrapped", map[string]string{EnvCodexThread: "sess-a"}, []string{"diagnose", "--harness", "codex"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.initAndHello(t)
			before := f.lastSeen(t, "sess-a").LastSeen
			f.clock = f.clock.Add(2 * time.Hour)
			f.env = tc.env
			f.run(t, f.repo, "", tc.args...)
			got := f.lastSeen(t, "sess-a").LastSeen
			if tc.heard && !got.Equal(f.clock) {
				t.Fatalf("want last_seen moved to now (%v), got %v", f.clock, got)
			}
			if !tc.heard && !got.Equal(before) {
				t.Fatalf("want last_seen left at %v, got %v", before, got)
			}
			// A bystander is never touched by someone else's verb.
			if b := f.lastSeen(t, "sess-b").LastSeen; !b.Equal(before) && tc.env[EnvClaudeSession] != "sess-b" {
				t.Fatalf("bystander sess-b moved to %v", b)
			}
		})
	}
}

// What the trial's readers saw. The roster and a send to a Codex session that
// re-claimed read as stale before; they read the present now.
func TestHeardSessionIsNotReportedStale(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.clock = f.clock.Add(2 * time.Hour)
	f.env = map[string]string{EnvSession: "sess-b"}
	// Control: before any verb, the quiet session IS reported stale.
	if out, errw, code := f.run(t, f.repo, "", "msg", "alpha", "x"); code != 0 || !strings.Contains(out, "NOT SEEN FOR 2h") {
		t.Fatalf("control: a 2h-quiet session reads NOT SEEN: %d %q %s", code, out, errw)
	}
	f.env = map[string]string{EnvCodexThread: "sess-a"}
	if _, errw, code := f.run(t, f.repo, "", "claim", "orchestrator", "--desc", "d", "--scope", ".buddy/slot/orchestrator"); code != 0 {
		t.Fatal(errw)
	}
	f.clock = f.clock.Add(6 * time.Minute)
	f.env = map[string]string{EnvSession: "sess-b"}
	out, errw, code := f.run(t, f.repo, "", "msg", "alpha", "x")
	if code != 0 || strings.Contains(out, "NOT SEEN") || !strings.Contains(out, "last seen 6m") {
		t.Fatalf("a session that claimed 6m ago reads as stale: %d %q %s", code, out, errw)
	}
	out, _, _ = f.run(t, f.repo, "", "sessions")
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "alpha") && (strings.Contains(line, "STALE") || !strings.Contains(line, "seen 6m")) {
			t.Fatalf("roster row: %q", line)
		}
	}
}

// Invariant 12: a native verb from an ended session is refused as before and
// leaves it ended. Nothing about being heard from revives anything.
func TestHeardNeverRevives(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	if _, errw, code := f.run(t, f.repo, "", "bye", "sess-a"); code != 0 {
		t.Fatal(errw)
	}
	ended := f.lastSeen(t, "sess-a")
	if ended.Ended.IsZero() {
		t.Fatal("control: bye ended sess-a")
	}
	f.clock = f.clock.Add(time.Hour)
	f.env = map[string]string{EnvCodexThread: "sess-a"}
	if _, errw, code := f.run(t, f.repo, "", "status"); code == 0 || !strings.Contains(errw, "has ended") {
		t.Fatalf("an ended native identity must still be refused: %d %s", code, errw)
	}
	got := f.lastSeen(t, "sess-a")
	if got.Ended.IsZero() || !got.LastSeen.Equal(ended.LastSeen) {
		t.Fatalf("ended session touched: ended=%v last_seen %v (was %v)", got.Ended, got.LastSeen, ended.LastSeen)
	}
}
