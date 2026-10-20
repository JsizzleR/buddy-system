package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JsizzleR/buddy-system/internal/store"
)

// D-090: STALE keys on the holder. The table is the rule; the fixtures below
// prove every view renders it.
//
// The zero-clock and threshold rows came from staleNote's own table: a
// mutation removing the IsZero guard survived every end-to-end test, and a
// zero time renders as "56 years ago" — an unrecorded clock reported as an
// abandoned holder.
func TestStaleJudgementKeysOnTheHolder(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	old := now.Add(-store.StaleAfter - time.Second)
	alive := map[int]bool{1: true}
	h := holders{
		env:   Env{ProcAlive: func(p store.ProcRef) bool { return alive[p.PID] }},
		procs: map[string][]store.ProcRef{"gone": {{PID: 2}}, "half": {{PID: 1}, {PID: 2}}, "up": {{PID: 1}}},
	}
	live := func(id string) store.SessionInfo { return store.SessionInfo{SessionID: id, LastSeen: now} }
	silent := store.SessionInfo{SessionID: "up", LastSeen: old}
	ended := store.SessionInfo{SessionID: "up", LastSeen: old, Ended: now.Add(-time.Minute)}
	files, slot := []string{"internal/api"}, []string{".buddy/slot/run1"}
	cases := []struct {
		name    string
		renewed time.Time
		owner   store.SessionInfo
		scopes  []string
		state   string
		want    staleKind
	}{
		{"unrecorded clock says nothing", time.Time{}, silent, files, "open", staleNone},
		{"fresh renewal says nothing, even of a quiet holder", now.Add(-time.Minute), silent, files, "open", staleNone},
		{"exactly at the threshold is not yet stale", now.Add(-store.StaleAfter), silent, files, "open", staleNone},
		{"a clock in the FUTURE is not stale", now.Add(time.Hour), silent, files, "open", staleNone},
		{"a released claim says nothing", old, silent, files, "released", staleNone},
		{"unrenewed, holder seen: ask the holder", old, live("up"), files, "open", staleUnrenewed},
		{"unrenewed, holder with no registered process is not GONE", old, live("none"), files, "open", staleUnrenewed},
		{"unrenewed, one of two processes gone is still there", old, live("half"), files, "open", staleUnrenewed},
		{"unrenewed, holder not seen: STALE", old, silent, files, "open", staleQuiet},
		{"unrenewed, every process GONE though seen: STALE", old, live("gone"), files, "open", staleQuiet},
		{"unrenewed, holder ended: STALE", old, ended, files, "open", staleQuiet},
		{"a slot of a live holder says nothing", now.Add(-6 * time.Hour), live("up"), slot, "open", staleNone},
		{"a slot of a quiet holder is STALE", old, silent, slot, "open", staleQuiet},
		{"a slot beside a file is a file claim", old, live("up"), append([]string{"internal/api"}, slot...), "open", staleUnrenewed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := store.ClaimInfo{Slug: "held", State: tc.state, Renewed: tc.renewed, Owner: tc.owner, Scopes: tc.scopes}
			s := h.judge(now, c)
			if s.kind != tc.want {
				t.Fatalf("kind %d, want %d", s.kind, tc.want)
			}
			note := s.refusalNote(now, c.Slug)
			switch tc.want {
			case staleNone:
				if note != "" || s.rowNote(now) != "" || s.helloMark(now) != "" {
					t.Fatalf("nothing to say, said %q / %q / %q", note, s.rowNote(now), s.helloMark(now))
				}
			case staleUnrenewed:
				if !strings.Contains(note, "ask the holder: buddy msg held") || strings.Contains(note, "STALE") || strings.Contains(note, "operator") {
					t.Fatalf("a live holder is the one to ask: %q", note)
				}
				if s.helloMark(now) != "" {
					t.Fatalf("the hello digest marks only a quiet holder: %q", s.helloMark(now))
				}
			case staleQuiet:
				if !strings.Contains(note, "STALE") || !strings.Contains(note, "ask the operator") {
					t.Fatalf("a quiet holder is the operator's: %q", note)
				}
			}
			if tc.want != staleNone && !strings.Contains(note, "it still refuses") {
				t.Fatalf("the note must not read as a takeover: %q", note)
			}
		})
	}
}

// The `buddy msg <slug>` in the note is a command only when it pastes back
// as the slug it names (onFlags' rule).
func TestRefusalNoteWritesOnlyAPastableCommand(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	s := staleness{kind: staleUnrenewed, renewed: now.Add(-time.Hour), seen: now}
	if got := s.refusalNote(now, "a b"); !strings.Contains(got, "buddy msg 'a b'") {
		t.Fatalf("a slug with a space is quoted: %q", got)
	}
	if got := s.refusalNote(now, "a\nb"); strings.Contains(got, "buddy msg a") || !strings.Contains(got, "cannot be pasted back") {
		t.Fatalf("a slug the fence alters is not written as a command: %q", got)
	}
}

// Every view, end to end, for the case the fleet hit: a holder that is
// working (seen seconds ago) on a claim no tool call has renewed. Then the
// same claim once its holder's process is GONE, which is the positive
// control that every view's STALE arm is still armed.
func TestEveryViewAsksTheHolderWhenTheHolderIsLive(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.asProcess(300, 3, func() {
		if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "", ""), "hello"); code != 0 {
			t.Fatalf("hello: %s", errw)
		}
	})
	f.claimFor(t, "sess-a", "held", "holding", "internal/api")
	f.clock = f.clock.Add(store.StaleAfter + time.Minute)
	// A tool call naming a path the claim does not cover: the holder is
	// seen, the claim is not renewed.
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "Read", filepath.Join(f.repo, "README")), "beat"); code != 0 {
		t.Fatalf("beat: %s", errw)
	}
	f.stage(t, f.wtB, "internal/api/x.go")

	views := func() map[string]string {
		v := map[string]string{}
		out, _, code := f.run(t, f.wtB, "", "claim", "wants", "--session", "sess-b", "--desc", "w", "--scope", "internal/api")
		if code == 0 {
			t.Fatal("the claim must refuse whatever the words say (D-022)")
		}
		v["refusal"] = out
		v["dry run"], _, _ = f.run(t, f.wtB, "", "claim", "wants", "--dry-run", "--session", "sess-b", "--scope", "internal/api")
		ls, _, _ := f.run(t, f.repo, "", "ls")
		v["ls"] = rowWith(t, ls, "held")
		whose, _, _ := f.run(t, f.repo, "", "whose", "internal/api/x.go")
		v["whose"] = rowWith(t, whose, "held")
		status, _, _ := f.run(t, f.repo, "", "status", "--session", "sess-a")
		v["status"] = rowWith(t, status, "held")
		hello, _, _ := f.run(t, f.repo, hookJSON("sess-c", f.repo, "", ""), "hello", "--label", "gamma")
		v["hello"] = rowWith(t, hello, "- held")
		f.asSession("sess-b", func() { _, v["commit gate"], _ = f.run(t, f.wtB, "", "commit-gate") })
		return v
	}

	v := views()
	for name, want := range map[string]string{
		"refusal":     "claim unrenewed 31m; holder seen 0s ago; it still refuses, so ask the holder: buddy msg held",
		"dry run":     "claim unrenewed 31m; holder seen 0s ago; it still refuses, so ask the holder: buddy msg held",
		"whose":       "unrenewed 31m, holder seen 0s ago (still refuses — ask the holder)",
		"status":      "unrenewed 31m, holder seen 0s ago (still refuses — ask the holder)",
		"commit gate": "(live, last seen 0s)",
	} {
		if !strings.Contains(v[name], want) {
			t.Fatalf("%s: want %q in:\n%s", name, want, v[name])
		}
	}
	for name, out := range v {
		if strings.Contains(out, "STALE") || strings.Contains(out, "SILENT") || strings.Contains(out, "ask the operator") {
			t.Fatalf("%s blames a live holder:\n%s", name, out)
		}
	}

	f.kill(300)
	v = views()
	for name, want := range map[string]string{
		"refusal":     "STALE: claim unrenewed 31m, holder's harness process GONE, last seen 0s ago; it still refuses, so ask the operator",
		"dry run":     "STALE: claim unrenewed 31m, holder's harness process GONE",
		"ls":          "open STALE",
		"whose":       "STALE (claim unrenewed 31m, holder's harness process GONE, last seen 0s ago; still refuses — ask the operator)",
		"status":      "STALE (claim unrenewed 31m, holder's harness process GONE",
		"hello":       "[STALE: holder's harness process GONE, last seen 0s ago]",
		"commit gate": "live but its harness process is GONE",
	} {
		if !strings.Contains(v[name], want) {
			t.Fatalf("GONE, %s: want %q in:\n%s", name, want, v[name])
		}
	}
}

// A slot names a resource no tool call renews (D-035), so a live holder's
// slot carries no mark at all; a quiet holder's slot is STALE like any claim.
func TestASlotOfALiveHolderCarriesNoMark(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "run1", "the run", ".buddy/slot/run1")
	f.clock = f.clock.Add(6 * time.Hour)
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "Read", filepath.Join(f.repo, "README")), "beat"); code != 0 {
		t.Fatalf("beat: %s", errw)
	}
	refuse := func() string {
		out, _, code := f.run(t, f.wtB, "", "claim", "mine", "--session", "sess-b", "--desc", "x", "--scope", ".buddy/slot/run1")
		if code == 0 {
			t.Fatal("a held slot must refuse")
		}
		return out
	}
	out := refuse()
	ls, _, _ := f.run(t, f.repo, "", "ls")
	if strings.Contains(out, "STALE") || strings.Contains(out, "unrenewed") || strings.Contains(ls, "STALE") {
		t.Fatalf("a live holder's slot is marked:\n%s\n%s", out, ls)
	}
	// Positive control: the same slot once its holder goes quiet.
	f.clock = f.clock.Add(store.StaleAfter + time.Minute)
	if out := refuse(); !strings.Contains(out, "STALE: claim unrenewed 6h, holder not seen 31m; it still refuses, so ask the operator") {
		t.Fatalf("a quiet holder's slot is STALE, and says what is known about the holder:\n%s", out)
	}
}

// rowWith returns the first line of out containing needle, failing when none
// does: a view that dropped the claim must not pass as one with no mark.
func rowWith(t *testing.T, out, needle string) string {
	t.Helper()
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, needle) {
			return ln
		}
	}
	t.Fatalf("no line with %q in:\n%s", needle, out)
	return ""
}

// Codex code pass, D-090: the suggested `buddy msg` must reach the holder. A
// slug resolves LAST (D-013), so a claim slugged like another session's label
// printed a command that messaged that session; the holder's id is used
// instead. Control: a slug that resolves to its holder is printed as is (the
// `buddy msg held` assertions above).
func TestRefusalNoteMessagesTheHolderNotAShadowingName(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "bravo", "named like a peer", "internal/api")
	f.clock = f.clock.Add(store.StaleAfter + time.Minute)
	if _, errw, code := f.run(t, f.repo, hookJSON("sess-a", f.repo, "Read", filepath.Join(f.repo, "README")), "beat"); code != 0 {
		t.Fatalf("beat: %s", errw)
	}
	out, _, code := f.run(t, f.wtB, "", "claim", "wants", "--session", "sess-b", "--desc", "w", "--scope", "internal/api")
	if code == 0 {
		t.Fatal("must refuse")
	}
	if !strings.Contains(out, "ask the holder: buddy msg sess-a") || strings.Contains(out, "buddy msg bravo") {
		t.Fatalf("the command must address the holder, not the session the slug shadows:\n%s", out)
	}
}

// Codex code pass, D-090: a refusal's set is rebuilt from store.ErrRefused,
// which carries the holder's label and no id, so the label is what tells the
// claim the refusal named from one re-taken under the same slug since.
func TestConflictNotesFallBackWhenTheHolderChanged(t *testing.T) {
	boundedParallel(t)
	f := newFixture(t)
	f.initAndHello(t)
	f.claimFor(t, "sess-a", "held", "x", ".buddy/slot/run1")
	st, err := store.Open(filepath.Join(f.repo, ".git", "buddy.db"), func() time.Time { return f.clock })
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := f.clock.Add(store.StaleAfter + time.Minute)
	env := Env{Now: func() time.Time { return now }}
	old := f.clock
	// Control: the refusal named alpha and alpha still holds it, so alpha's
	// own state is judged (not seen for 31m: STALE).
	same := conflictNotes(env, st, []store.Conflict{{Slug: "held", Claimant: "alpha", Renewed: old}}, now)
	if !strings.Contains(same[0], "holder not seen 31m") {
		t.Fatalf("control, same holder: %q", same[0])
	}
	// The refusal named someone else: the live claim under that slug is not
	// the one refused on, so its holder's words must not be borrowed.
	other := conflictNotes(env, st, []store.Conflict{{Slug: "held", Claimant: "somebody-else", Renewed: old}}, now)
	if !strings.Contains(other[0], "its holder could not be re-read") {
		t.Fatalf("a changed holder must fall back: %q", other[0])
	}
}
